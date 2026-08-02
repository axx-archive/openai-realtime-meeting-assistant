import { useCallback, useEffect, useLayoutEffect, useRef } from 'react';
import { Platform } from 'react-native';
import * as Notifications from 'expo-notifications';
import { api, BonfireApiError } from '../api/client';
import {
  confirmPendingPushSessionRevocations,
  currentAuthStorageGeneration,
  writeAuthSecureForGeneration,
} from '../auth/AuthContext';
import {
  EAS_PROJECT_ID,
  PUSH_AUTHORITY_STORAGE_KEY,
  PUSH_TOKEN_STORAGE_KEY,
} from '../config';
import {
  parsePushTarget,
  resolveAuthorizedPushTarget,
  type PushCandidate,
  type PushTarget,
} from './deepLink';
import {
  pushBindingCoordinator,
  type PushBindingAuthority,
  type PushBindingHandlers,
} from './PushBindingCoordinator';
import {
  pushAuthorityLedger,
  type PushRegistrationAuthority,
} from './PushAuthorityLedger';

function bindingAuthority(
  authority: PushRegistrationAuthority,
): PushBindingAuthority | null {
  if (!authority.token) return null;
  return {
    accountKey: authority.accountKey,
    sessionToken: authority.sessionToken,
    token: authority.token,
  };
}

function currentLedgerAuthority(
  authority: PushBindingAuthority,
): PushRegistrationAuthority | null {
  return pushAuthorityLedger.forSession(authority.accountKey, authority.sessionToken)
    .find((candidate) => candidate.token === authority.token) ?? null;
}

async function persistPushAuthorityLedger(): Promise<void> {
  await writeAuthSecureForGeneration(
    PUSH_AUTHORITY_STORAGE_KEY,
    pushAuthorityLedger.serialize(),
    currentAuthStorageGeneration(),
  );
}

const pushBindingHandlers: PushBindingHandlers = {
  register: (authority) => api.registerPushDevice(
    authority.sessionToken,
    authority.token,
    Platform.OS,
  ),
  unregister: async (authority) => {
    const current = currentLedgerAuthority(authority);
    if (current?.deviceCleared) return;
    // A 401 is not deletion proof: the server authenticates before removing
    // its account-bound device record. Preserve and retry this authority until
    // the server explicitly confirms revocation.
    await api.unregisterPushDevice(authority.sessionToken, authority.token, true);
  },
  onRegistered: async (authority) => {
    const current = currentLedgerAuthority(authority)
      ?? pushAuthorityLedger.remember(
        authority.accountKey,
        authority.sessionToken,
        authority.token,
      );
    if (!current) throw new Error('Push registration authority was lost.');
    const superseded = pushAuthorityLedger.registrationSucceeded(current);
    await persistPushAuthorityLedger();
    // A successful token-keyed upsert proves these older bindings were
    // replaced, but their captured sessions must still be retired. Keeping
    // them in the coordinator also protects against an older POST settling
    // after this success and reclaiming last-writer ownership.
    for (const previous of superseded) {
      const binding = bindingAuthority(previous);
      if (binding) void pushBindingCoordinator.retire(binding, pushBindingHandlers);
    }
  },
  onRetired: async (authority) => {
    const current = currentLedgerAuthority(authority)
      ?? pushAuthorityLedger.remember(
        authority.accountKey,
        authority.sessionToken,
        authority.token,
        true,
      );
    if (!current) throw new Error('Push retirement authority was lost.');
    if (!current.deviceCleared) pushAuthorityLedger.markDeviceCleared(current);
    await persistPushAuthorityLedger();
    let loggedOut = false;
    try {
      const receipt = await api.logout(authority.sessionToken, null, true);
      loggedOut = receipt.sessionRevoked === true;
    } catch (error) {
      loggedOut = error instanceof BonfireApiError && error.status === 401;
    }
    if (!loggedOut) throw new Error('Captured push session logout is still pending.');
    pushAuthorityLedger.removeSession(authority.accountKey, authority.sessionToken);
    await persistPushAuthorityLedger();
  },
};

/**
 * Native push registration — design §8 of docs/plans/the-table-design.md.
 *
 * The app shipped with NO push of any kind. Delivery was a websocket that only
 * lived while the app was open, which makes "replaces the team's iPhone group
 * thread" false on day one: iMessage's defining property is that it reaches you.
 *
 * API surface here is pinned to Expo SDK 57 (docs.expo.dev/versions/v57.0.0).
 * Note `shouldShowBanner` / `shouldShowList` — the older `shouldShowAlert` is
 * silently ignored, which fails as "push works but nothing appears".
 */

// Registered once per process, not per mount: this is global handler state and
// re-setting it on every render is churn with no effect.
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldPlaySound: true,
    shouldSetBadge: true,
    shouldShowBanner: true,
    shouldShowList: true,
  }),
});

export type PushRegistrationOptions = {
  sessionToken: string | null;
  accountKey: string | null;
  bootstrapping: boolean;
  /** Called with the thread to open when a notification is tapped. */
  onOpenTarget: (target: PushTarget) => void;
};

export function usePushRegistration({
  sessionToken,
  accountKey,
  bootstrapping,
  onOpenTarget,
}: PushRegistrationOptions) {
  // Kept in a ref so the listener effect does not re-subscribe every time the
  // navigation callback is re-created.
  const openRef = useRef(onOpenTarget);
  openRef.current = onOpenTarget;
  const currentSessionTokenRef = useRef<string | null>(sessionToken);
  const currentAccountKeyRef = useRef<string | null>(accountKey);
  const bootstrappingRef = useRef(bootstrapping);
  const registrationGenerationRef = useRef(0);
  const pendingColdCandidateRef = useRef<PushCandidate | null>(null);

  // Commit the imperative authority fence before passive socket/push effects.
  // Unlike a render-time ref mutation, an abandoned concurrent render cannot
  // permanently fence the still-committed account.
  useLayoutEffect(() => {
    if (
      currentSessionTokenRef.current !== sessionToken
      || currentAccountKeyRef.current !== accountKey
      || bootstrappingRef.current !== bootstrapping
    ) {
      currentSessionTokenRef.current = sessionToken;
      currentAccountKeyRef.current = accountKey;
      bootstrappingRef.current = bootstrapping;
      registrationGenerationRef.current += 1;
    }
  }, [accountKey, bootstrapping, sessionToken]);

  const register = useCallback(async () => {
    if (!sessionToken || !accountKey || !EAS_PROJECT_ID) return;
    const registrationSessionToken = sessionToken;
    const registrationAccountKey = accountKey;
    const registrationGeneration = registrationGenerationRef.current;
    const storageGeneration = currentAuthStorageGeneration();
    const registrationIsCurrent = () => (
      currentSessionTokenRef.current === registrationSessionToken
      && currentAccountKeyRef.current === registrationAccountKey
      && registrationGenerationRef.current === registrationGeneration
    );
    try {
      const existing = await Notifications.getPermissionsAsync();
      if (!registrationIsCurrent()) return;
      let granted = existing.granted;
      if (!granted) {
        const requested = await Notifications.requestPermissionsAsync({
          ios: { allowAlert: true, allowBadge: true, allowSound: true },
        });
        if (!registrationIsCurrent()) return;
        granted = requested.granted;
      }
      // A denied prompt is a normal outcome, not an error. The rest of the app
      // works; only the buzz is missing.
      if (!granted) return;

      const token = await Notifications.getExpoPushTokenAsync({ projectId: EAS_PROJECT_ID });
      if (!registrationIsCurrent()) return;
      // Signing into B remains available while offline, but B may not mutate a
      // shared Expo-token binding until every older exact session is confirmed
      // revoked. The retry is intentionally in-process and account-scoped.
      while (registrationIsCurrent()) {
        if (await confirmPendingPushSessionRevocations()) break;
        await new Promise((resolve) => setTimeout(resolve, 1_000));
      }
      if (!registrationIsCurrent()) return;
      const registrationAuthority = pushAuthorityLedger.remember(
        registrationAccountKey,
        registrationSessionToken,
        token.data,
      );
      if (!registrationAuthority) return;
      // Persist cleanup authority before the network request. Sign-out drains
      // this exact registration, unregisters the token, and only then revokes
      // the session; an offline exit therefore cannot forget A's binding.
      const [storedToken, storedAuthority] = await Promise.all([
        writeAuthSecureForGeneration(
          PUSH_TOKEN_STORAGE_KEY,
          token.data,
          storageGeneration,
        ),
        writeAuthSecureForGeneration(
          PUSH_AUTHORITY_STORAGE_KEY,
          pushAuthorityLedger.serialize(),
          storageGeneration,
        ),
      ]);
      if (!storedToken || !storedAuthority || !registrationIsCurrent()) {
        pushAuthorityLedger.markSessionPending(
          registrationAccountKey,
          registrationSessionToken,
        );
        const staleBinding = bindingAuthority(registrationAuthority);
        if (staleBinding) {
          void pushBindingCoordinator.retire(staleBinding, pushBindingHandlers);
        }
        return;
      }
      const binding = bindingAuthority(registrationAuthority);
      if (!binding) return;
      const registration = pushBindingCoordinator.setDesired(binding, pushBindingHandlers);
      await pushAuthorityLedger.trackRegistration(registrationAuthority, registration);
    } catch {
      // Registration is best-effort and must never block sign-in. A simulator
      // with no APNs entitlement throws here on every launch.
    }
  }, [accountKey, sessionToken]);

  useEffect(() => {
    void register();
  }, [register]);

  // Badge cleanup is local. AuthContext clears authority synchronously, then
  // performs bounded server cleanup with its captured old session token.
  useEffect(() => {
    if (sessionToken) return;
    void Notifications.setBadgeCountAsync(0).catch(() => {});
  }, [sessionToken]);

  const validateAndOpen = useCallback(async (
    candidate: PushCandidate,
    validationSessionToken: string,
    validationAccountKey: string,
    validationGeneration: number,
  ) => {
    try {
      const response = await api.notifications(validationSessionToken);
      if (
        currentSessionTokenRef.current !== validationSessionToken
        || currentAccountKeyRef.current !== validationAccountKey
        || registrationGenerationRef.current !== validationGeneration
      ) return;
      const target = resolveAuthorizedPushTarget(
        candidate,
        response.notifications,
        validationAccountKey,
      );
      if (target) openRef.current(target);
    } catch {
      // A missing, expired, cleared, or unreachable notification fails closed.
      // A push tap should never open a thread under unverified authority.
    }
  }, []);

  const receiveCandidate = useCallback((candidate: PushCandidate) => {
    const validationSessionToken = currentSessionTokenRef.current;
    const validationAccountKey = currentAccountKeyRef.current;
    if (validationSessionToken && validationAccountKey) {
      void validateAndOpen(
        candidate,
        validationSessionToken,
        validationAccountKey,
        registrationGenerationRef.current,
      );
      return;
    }
    pendingColdCandidateRef.current = bootstrappingRef.current ? candidate : null;
  }, [validateAndOpen]);

  useEffect(() => {
    if (bootstrapping) return;
    if (!sessionToken || !accountKey) {
      pendingColdCandidateRef.current = null;
      return;
    }
    const pending = pendingColdCandidateRef.current;
    pendingColdCandidateRef.current = null;
    if (pending) {
      void validateAndOpen(
        pending,
        sessionToken,
        accountKey,
        registrationGenerationRef.current,
      );
    }
  }, [accountKey, bootstrapping, sessionToken, validateAndOpen]);

  useEffect(() => {
    // Cold start: the app was LAUNCHED by the notification, so no listener has
    // been attached yet. Without this the tap opens the canvas and the user has
    // to navigate to the thing they were just told about.
    const initial = Notifications.getLastNotificationResponse();
    const initialCandidate = parsePushTarget(initial?.notification?.request?.content?.data);
    if (initialCandidate) {
      receiveCandidate(initialCandidate);
      // The API exposes the most recently handled response, not only the one
      // that launched this process. Consume it after routing so a later
      // navigator remount cannot reopen a stale thread.
      Notifications.clearLastNotificationResponse();
    }

    const subscription = Notifications.addNotificationResponseReceivedListener((response) => {
      const candidate = parsePushTarget(response.notification.request.content.data);
      if (candidate) {
        receiveCandidate(candidate);
        Notifications.clearLastNotificationResponse();
      }
    });
    return () => subscription.remove();
  }, [receiveCandidate]);
}

/**
 * Unregisters a device token. Called from the sign-out path, before the session
 * token is cleared — the request needs it to authenticate.
 */
export async function unregisterPushDevice(sessionToken: string, token: string): Promise<void> {
  await api.unregisterPushDevice(sessionToken, token);
}

/** Direct mentions only — matches the chat circle's dot rule (§6). */
export async function setMentionBadge(count: number): Promise<void> {
  try {
    await Notifications.setBadgeCountAsync(Math.max(0, count));
  } catch {
    // Badge permission can be denied independently of alerts.
  }
}
