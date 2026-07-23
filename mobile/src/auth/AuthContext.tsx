import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';
import * as SecureStore from 'expo-secure-store';
import { api, BonfireApiError } from '../api/client';
import type { Identity } from '../api/types';
import { LAST_NAME_STORAGE_KEY, SESSION_STORAGE_KEY } from '../config';

type AuthState = {
  user: Identity | null;
  sessionToken: string | null;
  bootstrapping: boolean;
  lastLoginName: string;
  signIn: (name: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
  refreshMe: () => Promise<void>;
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

  const signOut = useCallback(async () => {
    const token = sessionToken;
    setUser(null);
    setSessionToken(null);
    await writeSecure(SESSION_STORAGE_KEY, null);
    if (token) {
      try {
        await api.logout(token);
      } catch {
        // Local sign-out still succeeds if the network is down.
      }
    }
  }, [sessionToken]);

  const refreshMe = useCallback(async () => {
    if (!sessionToken) return;
    const me = await api.me(sessionToken);
    setUser(me);
  }, [sessionToken]);

  const value = useMemo(
    () => ({
      user,
      sessionToken,
      bootstrapping,
      lastLoginName,
      signIn,
      signOut,
      refreshMe,
    }),
    [user, sessionToken, bootstrapping, lastLoginName, signIn, signOut, refreshMe],
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
