/**
 * Waveform geometry — deliberately free of any `react-native` import.
 *
 * `theme/motion.ts` needs AccessibilityInfo and Easing, which drags in the RN
 * runtime; the node:test runner cannot parse RN's Flow-typed entry point, so
 * anything a test needs to reach has to live outside that import graph.
 */

export const waveform = {
  /** Enough bars to read as a voice trace rather than a row of dots. */
  barCount: 28,
  barWidth: 3,
  barGap: 5,
  /** The hero size. This is the largest object on the canvas by design. */
  height: 92,
  /** Floor while listening, so quiet passages still show a spine. */
  minScale: 0.06,
} as const;

/**
 * Exact horizontal footprint of the rendered row. Keeping this calculation
 * beside the canonical bar geometry prevents compact containers from silently
 * clipping bars when bar count, width, gap, or scale changes.
 */
export function waveformRowWidth(
  scale = 1,
  barCount: number = waveform.barCount,
): number {
  if (!Number.isFinite(scale) || scale <= 0 || !Number.isSafeInteger(barCount) || barCount <= 0) {
    return 0;
  }
  return (
    barCount * waveform.barWidth * scale
    + Math.max(0, barCount - 1) * waveform.barGap * scale
  );
}

/**
 * The RESTING silhouette — the correction that matters most.
 *
 * "Rest is static" (the breathe-only-while-listening law) means the bars do not
 * ANIMATE. It does not mean they are flat. The web canon renders its resting
 * office waveform at fourteen varied heights — 34%, 58%, 82%, 46%, 96%, … — so
 * at rest it still reads as a waveform: a voice, paused. Collapsing every bar to
 * one short uniform height turns the hero of the screen into an ellipsis glyph.
 *
 * These values are a hand-tuned envelope: a quiet lead-in, two crests, a dip,
 * and a decaying tail — the shape of a spoken sentence.
 */
export const RESTING_PROFILE: readonly number[] = [
  0.10, 0.16, 0.26, 0.20, 0.34, 0.48, 0.40, 0.58,
  0.72, 0.62, 0.84, 0.96, 0.80, 0.66, 0.74, 0.88,
  0.70, 0.54, 0.62, 0.46, 0.38, 0.50, 0.32, 0.24,
  0.30, 0.18, 0.14, 0.09,
];

/**
 * Per-bar weighting for the live trace. Newest samples land at the centre and
 * age outward, so the loudest part of what you just said sits where the eye
 * already is. The taper stops the trace ending in a hard vertical edge.
 */
export const TRACE_TAPER: readonly number[] = Array.from(
  { length: waveform.barCount },
  (_, index) => {
    const centred = Math.abs(index - (waveform.barCount - 1) / 2) / ((waveform.barCount - 1) / 2);
    // Cosine falloff — smooth at the centre, gentle at the edges.
    return 0.35 + 0.65 * Math.cos((centred * Math.PI) / 2) ** 1.4;
  },
);
