import { useCallback, useEffect, useState } from 'react';
import * as SecureStore from 'expo-secure-store';

/**
 * Settings → "Show message previews" — design §5.
 *
 * The canvas now renders a teammate's actual words on the app's home screen,
 * which is the point: it is what makes the canvas feel inhabited rather than
 * like a dead tool. But this is a work app that gets screen-shared and handed
 * across desks, so the switch has to exist.
 *
 * Default ON. Off degrades the line to a count ("4 new in #team") — it never
 * silences it, because hiding that anything happened would be a worse setting
 * than showing too much.
 */

const KEY = 'bonfire.canvas.showPreviews';

async function read(): Promise<boolean> {
  try {
    const stored = await SecureStore.getItemAsync(KEY);
    // Absent means never chosen, which is the default: on.
    return stored === null ? true : stored === 'true';
  } catch {
    // SecureStore can fail on simulator/web edge cases the same way auth
    // tolerates (AuthContext.tsx:46). Fall back to the default rather than
    // blanking the canvas.
    return true;
  }
}

export function useShowPreviews(): {
  showPreviews: boolean;
  setShowPreviews: (next: boolean) => void;
} {
  const [showPreviews, setValue] = useState(true);

  useEffect(() => {
    let cancelled = false;
    void read().then((stored) => {
      if (!cancelled) setValue(stored);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const setShowPreviews = useCallback((next: boolean) => {
    // Optimistic: the toggle must feel instant, and a failed write only means
    // the choice does not survive a restart.
    setValue(next);
    void SecureStore.setItemAsync(KEY, next ? 'true' : 'false').catch(() => {});
  }, []);

  return { showPreviews, setShowPreviews };
}
