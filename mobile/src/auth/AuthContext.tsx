import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { Platform } from 'react-native';
import * as SecureStore from 'expo-secure-store';
import * as Passkeys from 'react-native-passkeys';
import { api, BonfireApiError, setUnauthorizedHandler } from '../api/client';
import type { Identity } from '../api/types';
import {
  LAST_NAME_STORAGE_KEY,
  PUSH_AUTHORITY_STORAGE_KEY,
  PUSH_TOKEN_STORAGE_KEY,
  SESSION_STORAGE_KEY,
} from '../config';
import {
  DEFAULT_MOBILE_THEME,
  resolveInstalledThemePreference,
  type MobileThemePreference,
} from '../theme/appearancePreference';
import {
  readInstalledThemePreference,
  writeInstalledThemePreference,
} from '../theme/mobileAppearanceStore';
import { audioFocusRuntime } from '../realtime/audioFocusRuntime';
import {
  pushBindingCoordinator,
  type PushBindingAuthority,
  type PushBindingHandlers,
} from '../push/PushBindingCoordinator';
import {
  pushAuthorityLedger,
  type PushRegistrationAuthority,
} from '../push/PushAuthorityLedger';
import { SecureWriteSequencer } from './SecureWriteSequencer';
import { identityUpdateIsAuthorized } from './sessionAuthority';

const DEVICE_TOKEN_READ_TIMEOUT_MS = 1_000;
const LOCAL_AUTH_CLEANUP_TIMEOUT_MS = 5_000;
const REMOTE_LOGOUT_TIMEOUT_MS = 5_000;
const SECURE_READ_TIMEOUT_MS = 2_000;
const SECURE_WRITE_OWNERSHIP_TIMEOUT_MS = 1_500;

let authStorageGeneration = 0;

// A local sign-out is immediate, but a replacement account may not claim this
// installation until the old server session is confirmed dead. This closes the
// only interval in which an already-started old registration could still pass
// the server's exact-session push gate and become a transient last writer.
const pendingSessionRevocations = new Set<string>();
const confirmedSessionRevocations = new Set<string>();
const sessionRevocationRetryTimers = new Map<string, ReturnType<typeof setTimeout>>();

type SecureReadOutcome =
  | { status: 'value'; value: string }
  | { status: 'missing' }
  | { status: 'unavailable' };

type AuthState = {
  user: Identity | null;
  sessionToken: string | null;
  bootstrapping: boolean;
  lastLoginName: string;
  themePreference: MobileThemePreference;
  signIn: (name: string, password: string) => Promise<void>;
  signInWithPasskey: () => Promise<void>;
  signOut: () => Promise<void>;
  refreshMe: () => Promise<void>;
  updateIdentity: (identity: Identity, expectedSessionToken: string) => void;
  changeThemePreference: (preference: MobileThemePreference) => Promise<void>;
  changePassword: (currentPassword: string, newPassword: string) => Promise<void>;
};

const AuthContext = createContext<AuthState | null>(null);

function advanceAuthStorageGeneration(): number {
  authStorageGeneration += 1;
  return authStorageGeneration;
}

export function currentAuthStorageGeneration(): number {
  return authStorageGeneration;
}

async function readSecureOutcome(key: string): Promise<SecureReadOutcome> {
  try {
    const value = await SecureStore.getItemAsync(key);
    return value == null ? { status: 'missing' } : { status: 'value', value };
  } catch {
    return { status: 'unavailable' };
  }
}

async function readSecureRaw(key: string): Promise<string | null> {
  const outcome = await readSecureOutcome(key);
  return outcome.status === 'value' ? outcome.value : null;
}

async function writeSecureRaw(key: string, value: string | null): Promise<void> {
  try {
    if (value == null || value === '') {
      await SecureStore.deleteItemAsync(key);
    } else {
      await SecureStore.setItemAsync(key, value);
    }
  } catch {
    // SecureStore can fail on web/sim edge cases; auth still works in-memory.
  }
}

const secureWriteSequencer = new SecureWriteSequencer(
  writeSecureRaw,
  SECURE_WRITE_OWNERSHIP_TIMEOUT_MS,
);

export function readAuthSecure(key: string): Promise<string | null> {
  return resolveWithin(readSecureRaw(key), SECURE_READ_TIMEOUT_MS, null);
}

export function writeAuthSecureForGeneration(
  key: string,
  value: string | null,
  generation: number,
): Promise<boolean> {
  return secureWriteSequencer.write(
    key,
    value,
    () => generation === authStorageGeneration,
  );
}

async function resolveWithin<T>(work: Promise<T>, timeoutMs: number, fallback: T): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | null = null;
  try {
    return await Promise.race([
      work,
      new Promise<T>((resolve) => {
        timer = setTimeout(() => resolve(fallback), timeoutMs);
      }),
    ]);
  } catch {
    return fallback;
  } finally {
    if (timer) clearTimeout(timer);
  }
}

async function settleWithin(work: Promise<unknown>, timeoutMs: number): Promise<void> {
  await resolveWithin(
    work.then(
      () => true,
      () => false,
    ),
    timeoutMs,
    false,
  );
}

function persistPushAuthorityLedger(generation: number): Promise<boolean> {
  return writeAuthSecureForGeneration(
    PUSH_AUTHORITY_STORAGE_KEY,
    pushAuthorityLedger.serialize(),
    generation,
  );
}

async function logoutCapturedSession(
  authority: PushRegistrationAuthority,
  generation: number,
): Promise<boolean> {
  const loggedOut = await resolveWithin(
    api.logout(authority.sessionToken, null, true).then(
      (receipt) => receipt.sessionRevoked === true,
      (error) => error instanceof BonfireApiError && error.status === 401,
    ),
    REMOTE_LOGOUT_TIMEOUT_MS,
    false,
  );
  if (!loggedOut) return false;
  pushAuthorityLedger.removeSession(authority.accountKey, authority.sessionToken);
  await persistPushAuthorityLedger(generation);
  return true;
}

async function confirmSessionRevoked(sessionToken: string): Promise<boolean> {
  const token = sessionToken.trim();
  if (!token || confirmedSessionRevocations.has(token)) return true;
  pendingSessionRevocations.add(token);
  const revoked = await resolveWithin(
    api.logout(token, null, true).then(
      (receipt) => receipt.sessionRevoked === true,
      (error) => error instanceof BonfireApiError && error.status === 401,
    ),
    REMOTE_LOGOUT_TIMEOUT_MS,
    false,
  );
  if (revoked) {
    const retryTimer = sessionRevocationRetryTimers.get(token);
    if (retryTimer) clearTimeout(retryTimer);
    sessionRevocationRetryTimers.delete(token);
    pendingSessionRevocations.delete(token);
    confirmedSessionRevocations.add(token);
  } else if (!sessionRevocationRetryTimers.has(token)) {
    sessionRevocationRetryTimers.set(token, setTimeout(() => {
      sessionRevocationRetryTimers.delete(token);
      void confirmSessionRevoked(token);
    }, REMOTE_LOGOUT_TIMEOUT_MS));
  }
  return revoked;
}

export async function confirmPendingPushSessionRevocations(): Promise<boolean> {
  const sessionTokens = new Set(pendingSessionRevocations);
  for (const authority of pushAuthorityLedger.pending()) {
    if (!confirmedSessionRevocations.has(authority.sessionToken)) {
      sessionTokens.add(authority.sessionToken);
    }
  }
  if (!sessionTokens.size) return true;
  const results = await Promise.all(
    [...sessionTokens].map((token) => confirmSessionRevoked(token)),
  );
  return results.every(Boolean);
}

function pushBindingAuthority(
  authority: PushRegistrationAuthority,
): PushBindingAuthority | null {
  if (!authority.token) return null;
  return {
    accountKey: authority.accountKey,
    sessionToken: authority.sessionToken,
    token: authority.token,
  };
}

function currentPushAuthority(
  authority: PushBindingAuthority,
): PushRegistrationAuthority | null {
  return pushAuthorityLedger.forSession(authority.accountKey, authority.sessionToken)
    .find((candidate) => candidate.token === authority.token) ?? null;
}

function authPushBindingHandlers(generation: number): PushBindingHandlers {
  const handlers: PushBindingHandlers = {
    register: (authority) => api.registerPushDevice(
      authority.sessionToken,
      authority.token,
      Platform.OS,
    ),
    unregister: async (authority) => {
      const current = currentPushAuthority(authority);
      if (current?.deviceCleared) return;
      // The server rejects an unauthenticated DELETE before touching the
      // account-bound device record. Keep 401 work durable and pending; only a
      // successful revocation response is allowed to advance to logout.
      await api.unregisterPushDevice(authority.sessionToken, authority.token, true);
    },
    onRegistered: async (authority) => {
      const current = currentPushAuthority(authority)
        ?? pushAuthorityLedger.remember(
          authority.accountKey,
          authority.sessionToken,
          authority.token,
        );
      if (!current) throw new Error('Push registration authority was lost.');
      const superseded = pushAuthorityLedger.registrationSucceeded(current);
      await persistPushAuthorityLedger(generation);
      for (const previous of superseded) {
        const binding = pushBindingAuthority(previous);
        if (binding) void pushBindingCoordinator.retire(binding, handlers);
      }
    },
    onRetired: async (authority) => {
      const current = currentPushAuthority(authority)
        ?? pushAuthorityLedger.remember(
          authority.accountKey,
          authority.sessionToken,
          authority.token,
          true,
        );
      if (!current) throw new Error('Push retirement authority was lost.');
      const cleared = current.deviceCleared
        ? current
        : pushAuthorityLedger.markDeviceCleared(current);
      await persistPushAuthorityLedger(generation);
      if (!await logoutCapturedSession(cleared, generation)) {
        throw new Error('Captured push session logout is still pending.');
      }
    },
  };
  return handlers;
}

async function retryPendingPushRevocations(generation: number): Promise<void> {
  for (const authority of pushAuthorityLedger.pending()) {
    // Session invalidation is the privacy boundary. Record deletion is still
    // retried for hygiene, but an expired cleanup credential cannot make this
    // row delivery-eligible once the exact server session is gone.
    void confirmSessionRevoked(authority.sessionToken);
    const binding = pushBindingAuthority(authority);
    if (binding) {
      // The coordinator retains this work after the bootstrap effect returns,
      // retries offline failures in-process, and reasserts a newer desired
      // owner after any late old-account request.
      void pushBindingCoordinator.retire(binding, authPushBindingHandlers(generation));
    } else if (authority.deviceCleared) {
      void logoutCapturedSession(authority, generation);
    }
  }
}

function requireCurrentAuthGeneration(generation: number): void {
  if (generation !== currentAuthStorageGeneration()) {
    throw new Error('That authentication attempt was superseded. Please try again.');
  }
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<Identity | null>(null);
  const [sessionToken, setSessionToken] = useState<string | null>(null);
  const currentSessionTokenRef = useRef<string | null>(null);
  const currentAccountKeyRef = useRef<string | null>(null);
  const [bootstrapping, setBootstrapping] = useState(true);
  const [lastLoginName, setLastLoginName] = useState('');
  const [themePreference, setThemePreference] = useState<MobileThemePreference>(
    DEFAULT_MOBILE_THEME,
  );

  const beginLocalSessionClear = useCallback((expectedSessionToken: string | null) => {
    if (
      expectedSessionToken !== null
      && currentSessionTokenRef.current !== expectedSessionToken
    ) return null;

    const generation = advanceAuthStorageGeneration();
    // `forceClose` invalidates the active lease and every queued acquisition
    // synchronously, before its returned teardown promise can await native I/O.
    // Do this at the auth linearization point so no personal Realtime, meeting,
    // or dictation request can finish admission under a cleared identity.
    const focusClose = audioFocusRuntime.forceClose('forced_close');
    currentSessionTokenRef.current = null;
    currentAccountKeyRef.current = null;
    setUser(null);
    setSessionToken(null);
    const cleanup = settleWithin(
      Promise.allSettled([
        focusClose,
        writeAuthSecureForGeneration(SESSION_STORAGE_KEY, null, generation),
      ]),
      LOCAL_AUTH_CLEANUP_TIMEOUT_MS,
    );
    return { generation, cleanup };
  }, []);

  useEffect(() => {
    setUnauthorizedHandler((requestSessionToken) => {
      // A request may complete after its account has signed out or been
      // replaced. Only the exact token that is still active owns the right to
      // clear local auth; a late A/401 can never sign B out.
      if (!requestSessionToken || currentSessionTokenRef.current !== requestSessionToken) return;
      confirmedSessionRevocations.add(requestSessionToken);
      pendingSessionRevocations.delete(requestSessionToken);
      const capturedAccountKey = currentAccountKeyRef.current;
      let capturedAuthorities = capturedAccountKey
        ? pushAuthorityLedger.markSessionPending(capturedAccountKey, requestSessionToken)
        : [];
      // Fence local authority synchronously, but preserve the exact old push
      // credential before doing so. A 401 from an ordinary app request must
      // enter the same durable cleanup lane as an explicit sign-out.
      const localClear = beginLocalSessionClear(requestSessionToken);
      if (!localClear) return;
      if (!capturedAuthorities.length && capturedAccountKey) {
        const unknown = pushAuthorityLedger.remember(
          capturedAccountKey,
          requestSessionToken,
          null,
          true,
        );
        if (unknown) capturedAuthorities = [unknown];
      }
      void persistPushAuthorityLedger(localClear.generation);
      const handlers = authPushBindingHandlers(localClear.generation);
      for (const authority of capturedAuthorities) {
        const binding = pushBindingAuthority(authority);
        if (binding) void pushBindingCoordinator.retire(binding, handlers);
      }
      void localClear.cleanup;
    });
    return () => setUnauthorizedHandler(null);
  }, [beginLocalSessionClear]);

  useEffect(() => {
    const bootstrapGeneration = advanceAuthStorageGeneration();
    let cancelled = false;
    (async () => {
      const [token, savedName, installedTheme, pushAuthorityRaw, legacyPushToken] = await Promise.all([
        readAuthSecure(SESSION_STORAGE_KEY),
        readAuthSecure(LAST_NAME_STORAGE_KEY),
        readInstalledThemePreference(),
        readAuthSecure(PUSH_AUTHORITY_STORAGE_KEY),
        readAuthSecure(PUSH_TOKEN_STORAGE_KEY),
      ]);
      if (cancelled || bootstrapGeneration !== currentAuthStorageGeneration()) return;
      pushAuthorityLedger.hydrate(pushAuthorityRaw);
      if (legacyPushToken) {
        for (const pending of pushAuthorityLedger.pending()) {
          if (!pending.token) {
            pushAuthorityLedger.attachTokenToPendingSession(
              pending.accountKey,
              pending.sessionToken,
              legacyPushToken,
            );
          }
        }
      }
      void retryPendingPushRevocations(bootstrapGeneration);
      if (savedName) setLastLoginName(savedName);
      if (!token) {
        if (installedTheme) setThemePreference(installedTheme.preference);
        setBootstrapping(false);
        return;
      }
      try {
        const me = await api.me(token);
        if (cancelled || bootstrapGeneration !== currentAuthStorageGeneration()) return;
        const preference = resolveInstalledThemePreference(
          installedTheme,
          me.email,
          me.themePref,
        );
        setThemePreference(preference);
        await writeInstalledThemePreference(me.email, preference);
        if (cancelled || bootstrapGeneration !== currentAuthStorageGeneration()) return;
        currentSessionTokenRef.current = token;
        currentAccountKeyRef.current = me.email.trim().toLowerCase();
        setSessionToken(token);
        setUser(me);
      } catch (err) {
        if (err instanceof BonfireApiError && (err.status === 401 || err.status === 403)) {
          if (!cancelled && bootstrapGeneration === currentAuthStorageGeneration()) {
            // The handler ignores bootstrap 401s because no token is active
            // yet. Fence synchronously, then let durable cleanup finish in the
            // background so a stuck native close/store cannot wedge launch.
            const focusClose = audioFocusRuntime.forceClose('forced_close');
            currentSessionTokenRef.current = null;
            currentAccountKeyRef.current = null;
            setSessionToken(null);
            setUser(null);
            void settleWithin(
              Promise.allSettled([
                focusClose,
                writeAuthSecureForGeneration(SESSION_STORAGE_KEY, null, bootstrapGeneration),
              ]),
              LOCAL_AUTH_CLEANUP_TIMEOUT_MS,
            );
          }
        } else if (!cancelled) {
          if (installedTheme) setThemePreference(installedTheme.preference);
          setSessionToken(null);
          setUser(null);
        }
      } finally {
        if (!cancelled) setBootstrapping(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const signIn = useCallback(async (name: string, password: string) => {
    const generation = advanceAuthStorageGeneration();
    void writeAuthSecureForGeneration(SESSION_STORAGE_KEY, null, generation);
    const identity = await api.login(name, password);
    requireCurrentAuthGeneration(generation);
    const token = identity.sessionToken;
    if (!token) {
      throw new Error(
        'Server did not return a session token. Deploy the mobile session support and try again.',
      );
    }
    if (!await writeAuthSecureForGeneration(SESSION_STORAGE_KEY, token, generation)) {
      requireCurrentAuthGeneration(generation);
    }
    await writeAuthSecureForGeneration(LAST_NAME_STORAGE_KEY, name.trim(), generation);
    const preference = resolveInstalledThemePreference(
      await readInstalledThemePreference(),
      identity.email,
      identity.themePref,
    );
    requireCurrentAuthGeneration(generation);
    await writeInstalledThemePreference(identity.email, preference);
    requireCurrentAuthGeneration(generation);
    setLastLoginName(name.trim());
    setThemePreference(preference);
    currentSessionTokenRef.current = token;
    currentAccountKeyRef.current = identity.email.trim().toLowerCase();
    setSessionToken(token);
    setUser(identity);
  }, []);

  const signInWithPasskey = useCallback(async () => {
    const generation = advanceAuthStorageGeneration();
    void writeAuthSecureForGeneration(SESSION_STORAGE_KEY, null, generation);
    if (!Passkeys.isSupported()) {
      throw new Error('Passkeys are not available on this device.');
    }
    const begin = await api.beginPasskeyLogin();
    requireCurrentAuthGeneration(generation);
    const credential = await Passkeys.get(begin.publicKey as never);
    requireCurrentAuthGeneration(generation);
    if (!credential) throw new Error('Passkey sign-in was cancelled.');
    const identity = await api.finishPasskeyLogin(begin.ceremony, credential);
    requireCurrentAuthGeneration(generation);
    const token = identity.sessionToken;
    if (!token) throw new Error('The server did not return a native session.');
    if (!await writeAuthSecureForGeneration(SESSION_STORAGE_KEY, token, generation)) {
      requireCurrentAuthGeneration(generation);
    }
    await writeAuthSecureForGeneration(LAST_NAME_STORAGE_KEY, identity.name, generation);
    const preference = resolveInstalledThemePreference(
      await readInstalledThemePreference(),
      identity.email,
      identity.themePref,
    );
    requireCurrentAuthGeneration(generation);
    await writeInstalledThemePreference(identity.email, preference);
    requireCurrentAuthGeneration(generation);
    setLastLoginName(identity.name);
    setThemePreference(preference);
    currentSessionTokenRef.current = token;
    currentAccountKeyRef.current = identity.email.trim().toLowerCase();
    setSessionToken(token);
    setUser(identity);
  }, []);

  const signOut = useCallback(async () => {
    // Capture the old authority before clearing React state. The clear itself
    // is intentionally started without an await: microphone ownership is
    // fenced and the signed-out tree is selected immediately, while storage
    // and remote revocation continue as bounded best-effort cleanup.
    const capturedSessionToken = currentSessionTokenRef.current ?? sessionToken;
    const capturedAccountKey = currentAccountKeyRef.current ?? user?.email.trim().toLowerCase() ?? '';
    let capturedAuthorities = capturedSessionToken && capturedAccountKey
      ? pushAuthorityLedger.markSessionPending(capturedAccountKey, capturedSessionToken)
      : [];
    const localClear = beginLocalSessionClear(capturedSessionToken);
    if (!localClear) return;
    // Start exact server-session revocation immediately after the synchronous
    // local microphone/auth fence and before any token lookup or device cleanup
    // await. A replacement sign-in is blocked until this is confirmed, so a
    // delayed A registration can never become an eligible writer under B.
    const immediateSessionRevocation = capturedSessionToken
      ? confirmSessionRevoked(capturedSessionToken)
      : Promise.resolve(true);
    const initialLedgerPersist = persistPushAuthorityLedger(localClear.generation);
    const legacyTokenRead = capturedAuthorities.length || !capturedSessionToken || !capturedAccountKey
      ? { status: 'missing' } as SecureReadOutcome
      : await resolveWithin<SecureReadOutcome>(
          readSecureOutcome(PUSH_TOKEN_STORAGE_KEY),
          DEVICE_TOKEN_READ_TIMEOUT_MS,
          { status: 'unavailable' },
        );
    if (!capturedAuthorities.length && capturedSessionToken && capturedAccountKey) {
      const authority = pushAuthorityLedger.remember(
        capturedAccountKey,
        capturedSessionToken,
        legacyTokenRead.status === 'value' ? legacyTokenRead.value : null,
        true,
        legacyTokenRead.status === 'missing',
      );
      if (authority) capturedAuthorities = [authority];
      await persistPushAuthorityLedger(localClear.generation);
    }

    const retirementHandlers = authPushBindingHandlers(localClear.generation);
    const remoteCleanup = Promise.all(capturedAuthorities.map(async (authority) => {
      const binding = pushBindingAuthority(authority);
      if (binding) {
        // This promise may outlive the bounded sign-out UI wait. The process
        // coordinator keeps owning it, retries failures, and observes a late
        // server-committed registration so cleanup is not abandoned at 2s.
        await pushBindingCoordinator.retire(binding, retirementHandlers);
      } else if (authority.deviceCleared) {
        await logoutCapturedSession(authority, localClear.generation);
      }
    })).then(() => undefined);

    await settleWithin(
      Promise.allSettled([
        localClear.cleanup,
        initialLedgerPersist,
        immediateSessionRevocation,
        remoteCleanup,
      ]),
      REMOTE_LOGOUT_TIMEOUT_MS,
    );
  }, [sessionToken, user?.email, beginLocalSessionClear]);

  const refreshMe = useCallback(async () => {
    if (!sessionToken) return;
    const requestSessionToken = sessionToken;
    const me = await api.me(sessionToken);
    if (currentSessionTokenRef.current !== requestSessionToken) return;
    setUser(me);
  }, [sessionToken]);

  const updateIdentity = useCallback((
    identity: Identity,
    expectedSessionToken: string,
  ) => {
    // Settings requests can complete after their screen unmounts. A late A
    // profile response must not repopulate, overwrite B, or overwrite a newer
    // same-account session.
    setUser((current) => (
      identityUpdateIsAuthorized(
        currentSessionTokenRef.current,
        expectedSessionToken,
        current?.email,
        identity.email,
      ) ? identity : current
    ));
  }, []);

  const changeThemePreference = useCallback(
    async (preference: MobileThemePreference) => {
      if (!sessionToken || !user) throw new Error('Sign in again to change appearance.');
      const requestSessionToken = sessionToken;
      const identity = await api.setTheme(requestSessionToken, preference);
      if (currentSessionTokenRef.current !== requestSessionToken) return;
      await writeInstalledThemePreference(user.email, preference);
      if (currentSessionTokenRef.current !== requestSessionToken) return;
      setThemePreference(preference);
      setUser(identity);
    },
    [sessionToken, user],
  );

  const changePassword = useCallback(
    async (currentPassword: string, newPassword: string) => {
      if (!sessionToken) throw new Error('Sign in again to change your password.');
      const requestSessionToken = sessionToken;
      const requestGeneration = currentAuthStorageGeneration();
      const identity = await api.changePassword(requestSessionToken, currentPassword, newPassword);
      if (
        currentSessionTokenRef.current !== requestSessionToken
        || currentAuthStorageGeneration() !== requestGeneration
      ) return;
      const nextToken = identity.sessionToken;
      if (!nextToken) throw new Error('The server did not return the rotated session.');
      const rotationGeneration = advanceAuthStorageGeneration();
      // The server has already invalidated the old token. Install the new
      // imperative authority before storage I/O so a late request using the
      // old token cannot clear the successfully rotated session.
      currentSessionTokenRef.current = nextToken;
      currentAccountKeyRef.current = identity.email.trim().toLowerCase();
      setSessionToken(nextToken);
      setUser(identity);
      if (!await writeAuthSecureForGeneration(SESSION_STORAGE_KEY, nextToken, rotationGeneration)) {
        requireCurrentAuthGeneration(rotationGeneration);
      }
    },
    [sessionToken],
  );

  const value = useMemo(
    () => ({
      user,
      sessionToken,
      bootstrapping,
      lastLoginName,
      themePreference,
      signIn,
      signInWithPasskey,
      signOut,
      refreshMe,
      updateIdentity,
      changeThemePreference,
      changePassword,
    }),
    [
      user,
      sessionToken,
      bootstrapping,
      lastLoginName,
      themePreference,
      signIn,
      signInWithPasskey,
      signOut,
      refreshMe,
      updateIdentity,
      changeThemePreference,
      changePassword,
    ],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error('useAuth must be used within AuthProvider');
  }
  return ctx;
}
