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
import { LAST_NAME_STORAGE_KEY, SESSION_STORAGE_KEY } from '../config';

type AuthState = {
  user: Identity | null;
  sessionToken: string | null;
  bootstrapping: boolean;
  lastLoginName: string;
  signIn: (name: string, password: string) => Promise<void>;
  signInWithPasskey: () => Promise<void>;
  signOut: () => Promise<void>;
  refreshMe: () => Promise<void>;
  updateIdentity: (identity: Identity) => void;
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
      const [token, savedName] = await Promise.all([
        readSecure(SESSION_STORAGE_KEY),
        readSecure(LAST_NAME_STORAGE_KEY),
      ]);
      if (cancelled) return;
      if (savedName) setLastLoginName(savedName);
      if (!token) {
        setBootstrapping(false);
        return;
      }
      try {
        const me = await api.me(token);
        if (cancelled) return;
        setSessionToken(token);
        setUser(me);
      } catch (err) {
        if (err instanceof BonfireApiError && (err.status === 401 || err.status === 403)) {
          await writeSecure(SESSION_STORAGE_KEY, null);
        }
        if (!cancelled) {
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
    setLastLoginName(name.trim());
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
    setLastLoginName(identity.name);
    setSessionToken(token);
    setUser(identity);
  }, []);

  const signOut = useCallback(async () => {
    const token = sessionToken;
    await clearLocalSession();
    if (token) {
      try {
        await api.logout(token);
      } catch {
        // Local sign-out still succeeds if the network is down.
      }
    }
  }, [sessionToken, clearLocalSession]);

  const refreshMe = useCallback(async () => {
    if (!sessionToken) return;
    const me = await api.me(sessionToken);
    setUser(me);
  }, [sessionToken]);

  const updateIdentity = useCallback((identity: Identity) => setUser(identity), []);

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
      signIn,
      signInWithPasskey,
      signOut,
      refreshMe,
      updateIdentity,
      changePassword,
    }),
    [
      user,
      sessionToken,
      bootstrapping,
      lastLoginName,
      signIn,
      signInWithPasskey,
      signOut,
      refreshMe,
      updateIdentity,
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
