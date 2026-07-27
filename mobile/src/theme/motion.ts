import { AccessibilityInfo, Easing, type EasingFunction } from 'react-native';
import { useEffect, useState } from 'react';

/**
 * Motion canon — mirrored from the live web `:root` (`index.html`).
 *
 * The laws this file exists to enforce (design §8):
 *   1. Breathe only while listening. Waveform bars rest STATIC.
 *   2. Ember is earned — agent work and live listening only.
 *   3. Amplitude drives the waveform; never a keyframe loop.
 *   4. Transforms only. Never animate width/height on the waveform.
 *   5. Reduce Motion collapses animation but PRESERVES amplitude response,
 *      because that response is information, not decoration.
 */

/** Web `--dur-*`, verbatim. */
export const duration = {
  fast: 120,
  med: 220,
  slow: 360,
  breathe: 2400,
} as const;

/** Web `--ease: cubic-bezier(0.32, 0.72, 0, 1)`. */
export const ease: EasingFunction = Easing.bezier(0.32, 0.72, 0, 1);
/** Web `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)`. */
export const easeSpring: EasingFunction = Easing.bezier(0.34, 1.25, 0.5, 1);

/**
 * Amplitude sampling. 100ms keeps the JS thread quiet while still reading as
 * responsive — the bars interpolate between samples on the native driver, so
 * perceived smoothness is decoupled from this rate (design §18).
 */
export const meteringIntervalMs = 100;

/**
 * Waveform geometry. Bars scale on Y only — law 4. Defined in a pure module so
 * tests can reach it without importing the RN runtime.
 */
export { waveform } from './waveformGeometry';

/** Subscribes to the OS Reduce Motion setting. */
export function useReduceMotion(): boolean {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    let active = true;
    void AccessibilityInfo.isReduceMotionEnabled().then((value) => {
      if (active) setReduced(value);
    });
    const sub = AccessibilityInfo.addEventListener('reduceMotionChanged', setReduced);
    return () => {
      active = false;
      sub.remove();
    };
  }, []);
  return reduced;
}

/** Subscribes to the OS Reduce Transparency setting (drives the glass fallback). */
export function useReduceTransparency(): boolean {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    let active = true;
    void AccessibilityInfo.isReduceTransparencyEnabled().then((value) => {
      if (active) setReduced(value);
    });
    const sub = AccessibilityInfo.addEventListener('reduceTransparencyChanged', setReduced);
    return () => {
      active = false;
      sub.remove();
    };
  }, []);
  return reduced;
}
