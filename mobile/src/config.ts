import Constants from 'expo-constants';

/**
 * Production Stride backend — same host as the web app.
 * Override with EXPO_PUBLIC_API_BASE_URL for local/staging builds.
 */
const extra = (Constants.expoConfig?.extra ?? {}) as {
  apiBaseUrl?: string;
  webAppUrl?: string;
  eas?: { projectId?: string };
};

/**
 * Required by `getExpoPushTokenAsync`. Absent in bare/dev contexts where the
 * EAS block was not injected — registration skips rather than throwing, so a
 * missing id degrades push instead of breaking sign-in.
 */
export const EAS_PROJECT_ID = extra.eas?.projectId ?? null;

export const API_BASE_URL = (
  process.env.EXPO_PUBLIC_API_BASE_URL ||
  extra.apiBaseUrl ||
  'https://thebonfire.xyz'
).replace(/\/$/, '');

export const WEB_APP_URL = (
  process.env.EXPO_PUBLIC_WEB_APP_URL ||
  extra.webAppUrl ||
  API_BASE_URL
).replace(/\/$/, '');

/**
 * Native private Realtime remains a build-time release gate. Build 75's
 * production profile enables the voice-first surface explicitly;
 * local and ad-hoc builds still stay off unless they opt in.
 */
export const NATIVE_REALTIME_VOICE_ENABLED =
  process.env.EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED === 'true';

/** Sent on every request so the server can return native session tokens. */
export const NATIVE_CLIENT_HEADER = 'expo';

export const SESSION_STORAGE_KEY = 'bonfire.sessionToken.v1';
export const LAST_NAME_STORAGE_KEY = 'bonfire.lastLoginName.v1';
export const PUSH_TOKEN_STORAGE_KEY = 'bonfire.expoPushToken.v1';
export const PUSH_AUTHORITY_STORAGE_KEY = 'bonfire.pushAuthority.v1';
