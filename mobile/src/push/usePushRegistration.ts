import { useCallback, useEffect, useRef } from 'react';
import { Platform } from 'react-native';
import * as Notifications from 'expo-notifications';
import { api } from '../api/client';
import { EAS_PROJECT_ID } from '../config';
import { parsePushTarget, type PushTarget } from './deepLink';

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
  /** Called with the thread to open when a notification is tapped. */
  onOpenTarget: (target: PushTarget) => void;
};

export function usePushRegistration({ sessionToken, onOpenTarget }: PushRegistrationOptions) {
  // Held so logout can unregister the exact token that was registered.
  const tokenRef = useRef<string | null>(null);
  // Kept in a ref so the listener effect does not re-subscribe every time the
  // navigation callback is re-created.
  const openRef = useRef(onOpenTarget);
  openRef.current = onOpenTarget;

  const register = useCallback(async () => {
    if (!sessionToken || !EAS_PROJECT_ID) return;
    try {
      const existing = await Notifications.getPermissionsAsync();
      let granted = existing.granted;
      if (!granted) {
        const requested = await Notifications.requestPermissionsAsync({
          ios: { allowAlert: true, allowBadge: true, allowSound: true },
        });
        granted = requested.granted;
      }
      // A denied prompt is a normal outcome, not an error. The rest of the app
      // works; only the buzz is missing.
      if (!granted) return;

      const token = await Notifications.getExpoPushTokenAsync({ projectId: EAS_PROJECT_ID });
      tokenRef.current = token.data;
      await api.registerPushDevice(sessionToken, token.data, Platform.OS);
    } catch {
      // Registration is best-effort and must never block sign-in. A simulator
      // with no APNs entitlement throws here on every launch.
    }
  }, [sessionToken]);

  useEffect(() => {
    void register();
  }, [register]);

  // Unregister on sign-out. This is not housekeeping: a token left bound to the
  // previous account delivers their messages to whoever signs in next on this
  // phone.
  useEffect(() => {
    if (sessionToken) return;
    const token = tokenRef.current;
    if (!token) return;
    tokenRef.current = null;
    void Notifications.setBadgeCountAsync(0).catch(() => {});
  }, [sessionToken]);

  useEffect(() => {
    // Cold start: the app was LAUNCHED by the notification, so no listener has
    // been attached yet. Without this the tap opens the canvas and the user has
    // to navigate to the thing they were just told about.
    const initial = Notifications.getLastNotificationResponse();
    const initialTarget = parsePushTarget(initial?.notification?.request?.content?.data);
    if (initialTarget) openRef.current(initialTarget);

    const subscription = Notifications.addNotificationResponseReceivedListener((response) => {
      const target = parsePushTarget(response.notification.request.content.data);
      if (target) openRef.current(target);
    });
    return () => subscription.remove();
  }, []);
}

/**
 * Unregisters a device token. Called from the sign-out path, before the session
 * token is cleared — the request needs it to authenticate.
 */
export async function unregisterPushDevice(sessionToken: string, token: string): Promise<void> {
  try {
    await api.unregisterPushDevice(sessionToken, token);
  } catch {
    // Best-effort: the server also prunes on DeviceNotRegistered.
  }
}

/** Direct mentions only — matches the chat circle's dot rule (§6). */
export async function setMentionBadge(count: number): Promise<void> {
  try {
    await Notifications.setBadgeCountAsync(Math.max(0, count));
  } catch {
    // Badge permission can be denied independently of alerts.
  }
}
