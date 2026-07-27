/**
 * Waveform geometry — deliberately free of any `react-native` import.
 *
 * `theme/motion.ts` needs AccessibilityInfo and Easing, which drags in the RN
 * runtime; the node:test runner cannot parse RN's Flow-typed entry point, so
 * anything a test needs to reach has to live outside that import graph. These
 * are plain numbers, so they live here and `motion.ts` re-exports them.
 */
export const waveform = {
  barCount: 5,
  barWidth: 4,
  barGap: 6,
  /** Full height at unity amplitude. */
  height: 64,
  /** Resting scale — a calm, clearly-static line (web `.bf-wave-bar` opacity 0.3). */
  restScale: 0.14,
  restOpacity: 0.42,
  /** Floor while listening, so the bars never fully collapse mid-speech. */
  minScale: 0.12,
} as const;
