import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';
import * as SecureStore from 'expo-secure-store';
import * as Passkeys from 'react-native-passkeys';
import { api, BonfireApiError, setUnauthorizedHandler } from '../api/client';
import type { Identity } from '../api/types';
import { LAST_NAME_STORAGE_KEY, PUSH_TOKEN_STORAGE_KEY, SESSION_STORAGE_KEY } from '../config';
import {
  DEFAULT_MOBILE_THEME,
  resolveInstalledThemePreference,
  type MobileThemePreference,
} from '../theme/appearancePreference';
import {
  readInstalledThemePreference,
  writeInstalledThemePreference,
} from '../theme/mobileAppearanceStore';

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
  updateIdentity: (identity: Identity) => void;
  changeThemePreference: (preference: MobileThemePreference) => Promise<void>;
  changePassword: (currentPassword: string, newPassword: string) => Promise<void>;
};

const AuthContext = createContext<AuthState | null>(null);

async function readSecure(key: string): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(key);
  } catch {
    return null;
  }
}

async function writeSecure(key: string, value: string | null): Promise<void> {
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

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<Identity | null>(null);
  const [sessionToken, setSessionToken] = useState<string | null>(null);
  const [bootstrapping, setBootstrapping] = useState(true);
  const [lastLoginName, setLastLoginName] = useState('');
  const [themePreference, setThemePreference] = useState<MobileThemePreference>(
    DEFAULT_MOBILE_THEME,
  );

  const clearLocalSession = useCallback(async () => {
    setUser(null);
    setSessionToken(null);
    await writeSecure(SESSION_STORAGE_KEY, null);
  }, []);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      void clearLocalSession();
    });
    return () => setUnauthorizedHandler(null);
  }, [clearLocalSession]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [token, savedName, installedTheme] = await Promise.all([
        readSecure(SESSION_STORAGE_KEY),
        readSecure(LAST_NAME_STORAGE_KEY),
        readInstalledThemePreference(),
      ]);
      if (cancelled) return;
      if (savedName) setLastLoginName(savedName);
      if (!token) {
        if (installedTheme) setThemePreference(installedTheme.preference);
        setBootstrapping(false);
        return;
      }
      try {
        const me = await api.me(token);
        if (cancelled) return;
        const preference = resolveInstalledThemePreference(
          installedTheme,
          me.email,
          me.themePref,
        );
        setThemePreference(preference);
        await writeInstalledThemePreference(me.email, preference);
        if (cancelled) return;
        setSessionToken(token);
        setUser(me);
      } catch (err) {
        if (err instanceof BonfireApiError && (err.status === 401 || err.status === 403)) {
          await writeSecure(SESSION_STORAGE_KEY, null);
        }
        if (!cancelled) {
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
    const identity = await api.login(name, password);
    const token = identity.sessionToken;
    if (!token) {
      throw new Error(
        'Server did not return a session token. Deploy the mobile session support and try again.',
      );
    }
    await writeSecure(SESSION_STORAGE_KEY, token);
    await writeSecure(LAST_NAME_STORAGE_KEY, name.trim());
    const preference = resolveInstalledThemePreference(
      await readInstalledThemePreference(),
      identity.email,
      identity.themePref,
    );
    await writeInstalledThemePreference(identity.email, preference);
    setLastLoginName(name.trim());
    setThemePreference(preference);
    setSessionToken(token);
    setUser(identity);
  }, []);

  const signInWithPasskey = useCallback(async () => {
    if (!Passkeys.isSupported()) {
      throw new Error('Passkeys are not available on this device.');
    }
    const begin = await api.beginPasskeyLogin();
    const credential = await Passkeys.get(begin.publicKey as never);
    if (!credential) throw new Error('Passkey sign-in was cancelled.');
    const identity = await api.finishPasskeyLogin(begin.ceremony, credential);
    const token = identity.sessionToken;
    if (!token) throw new Error('The server did not return a native session.');
    await writeSecure(SESSION_STORAGE_KEY, token);
    await writeSecure(LAST_NAME_STORAGE_KEY, identity.name);
    const preference = resolveInstalledThemePreference(
      await readInstalledThemePreference(),
      identity.email,
      identity.themePref,
    );
    await writeInstalledThemePreference(identity.email, preference);
    setLastLoginName(identity.name);
    setThemePreference(preference);
    setSessionToken(token);
    setUser(identity);
  }, []);

  const signOut = useCallback(async () => {
    const token = sessionToken;
    if (token) {
      try {
		const deviceToken = await readSecure(PUSH_TOKEN_STORAGE_KEY);
		await api.logout(token, deviceToken);
      } catch {
        // Local sign-out still succeeds if the network is down.
      }
    }
	await writeSecure(PUSH_TOKEN_STORAGE_KEY, null);
	await clearLocalSession();
  }, [sessionToken, clearLocalSession]);

  const refreshMe = useCallback(async () => {
    if (!sessionToken) return;
    const me = await api.me(sessionToken);
    setUser(me);
  }, [sessionToken]);

  const updateIdentity = useCallback((identity: Identity) => setUser(identity), []);

  const changeThemePreference = useCallback(
    async (preference: MobileThemePreference) => {
      if (!sessionToken || !user) throw new Error('Sign in again to change appearance.');
      const identity = await api.setTheme(sessionToken, preference);
      await writeInstalledThemePreference(user.email, preference);
      setThemePreference(preference);
      setUser(identity);
    },
    [sessionToken, user],
  );

  const changePassword = useCallback(
    async (currentPassword: string, newPassword: string) => {
      if (!sessionToken) throw new Error('Sign in again to change your password.');
      const identity = await api.changePassword(sessionToken, currentPassword, newPassword);
      const nextToken = identity.sessionToken;
      if (!nextToken) throw new Error('The server did not return the rotated session.');
      await writeSecure(SESSION_STORAGE_KEY, nextToken);
      setSessionToken(nextToken);
      setUser(identity);
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
