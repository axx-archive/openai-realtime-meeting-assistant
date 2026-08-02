/**
 * THE STRIDE SIGNAL — the mark, as geometry.
 *
 * A print of `scripts/stride-signal-geometry.mjs`, the canonical source shared by
 * the app icon, the desktop centrepiece, and the marketing site. Canon:
 * `docs/stride-signal-canon.md`.
 *
 * Regenerate the constants rather than hand-editing them:
 *   node scripts/stride-signal-geometry.mjs --print-ts
 *
 * Deliberately free of any `react-native` import: `theme/motion.ts` needs
 * AccessibilityInfo, which drags in the RN runtime, and the node:test runner
 * cannot parse RN's Flow-typed entry point. Anything a test must reach lives
 * outside that import graph.
 *
 * ── What the mark is ──────────────────────────────────────────────────────
 *
 * An APERTURE. One closed, symmetric, horizontal lens — a slot that opens
 * because something is listening. It replaces the sliced disc, which the founder
 * rejected before it shipped.
 *
 * There is exactly one curve in the identity:
 *
 *     t(p) = T · sin(pπ)^0.85          p ∈ [0, 1] along the width
 *
 * `t` is the half-height at each point and `T` is the half-height at the centre.
 * The exponent is the whole character of the form: at 1.0 it is a plain ellipse,
 * and below about 0.7 the tips go needle-sharp and brittle. 0.85 keeps a full
 * belly and draws the ends to a clean point, which is what lets the mark survive
 * being scaled to a hairline.
 *
 * The outline is SAMPLED, not bézier-approximated, and every surface reproduces
 * the same sampling — so there is one mark rather than one mark and a near-copy
 * of it.
 *
 * ── What the motion is ────────────────────────────────────────────────────
 *
 * The mark is not one fixed shape. It is one shape at a stated OPENNESS, and
 * openness is the system: a divider is the mark closed, a microphone that can
 * hear you is the mark open. Openness is an aspect ratio, width : full height,
 * so a bigger number is a tighter slot.
 *
 *   RATIO_IDLE = 25   the logo, and the instrument at rest — so pressing the
 *                     control never swaps one picture for another, it just opens
 *   RATIO_OPEN =  8   fully open, at the loudest voice and the smallest icon
 *
 * 8:1 is a HARD FLOOR on the whole identity, not a soft guideline for the
 * animation. A lens is eye-adjacent, and the wider it opens the more it reads as
 * an eye rather than a mouth or a slot — the wrong connotation for a product
 * whose entire job is remembering what people said. Ratified by AJ, 2026-07-29.
 *
 * The side-to-side element is the RIPPLE travelling along the open aperture. It
 * is a detail ON TOP of the lens, never a competing wave, and it only ever
 * closes — see `rippleAt`, which is where the measurement bug lived.
 */

/** The exponent that gives the lens its belly and its points. */
export const LENS_EXPONENT = 0.85;

/** The logo, and the instrument at rest. */
export const RATIO_IDLE = 25;

/** Fully open. A hard floor on the entire identity — see the header. */
export const RATIO_OPEN = 8;

/** How much of the mark's square box (the icon tile) the lens spans. */
export const TILE_INSET = 0.66;

/**
 * The travelling ripple that runs along the open aperture.
 *
 * `depth` is a fraction of the local half-height. An earlier pass let it reach
 * 0.42 and the slot stopped reading as a lens and became an amoeba.
 */
export const RIPPLE = {
  depth: 0.18,
  /** Cycles across the mark's width. */
  frequency: 6.2,
  /** Radians per second the crest travels. Negative would run it leftward. */
  speed: 1.9,
  /** Phase offset between the top and bottom edges, so the two do not mirror. */
  edgeOffset: 0.8,
} as const;

export const aperture = {
  /**
   * Sampling of the instrument's outline.
   *
   * 64 is the desktop's `APERTURE_STEPS`, and matching it is the point: the
   * native instrument and the web instrument then draw the same polygon rather
   * than two curves that merely agree in the limit. `lensPath` keeps the code of
   * record's own default (160) for artwork, where the cost is paid once.
   */
  steps: 64,
  /**
   * Amplitude floor while listening. An open microphone is never perfectly
   * still — silence has to look different from "not listening", or the control
   * cannot answer the only question a voice UI is ever asked.
   *
   * Deliberately NOT unified with the desktop's `STRIDE_SIGNAL_FLOOR` (0.16).
   * The two floors sit on top of different measurements — desktop reads RMS off
   * an AnalyserNode scaled by a full-scale constant, native reads expo-audio's
   * metering — so forcing the numbers equal would be false precision. The
   * GEOMETRY is shared; the ballistics belong to each input lane.
   */
  idleFloor: 0.14,
} as const;

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

/**
 * Half-height of the mark at a normalised position across it.
 *
 * `peak` is the half-height at the centre. Consumers that animate the mark
 * interpolate PEAK, never the ratio — see `ratioForAmplitude`.
 *
 * Exactly closed at the tips. `Math.sin(Math.PI)` is 1.22e-16, not 0, and that
 * residue survives the exponent (it comes out at 2.98e-14) — so without the
 * special case the mark's ends are infinitesimally open and "closed at the tips"
 * becomes a claim the geometry does not actually make. It is a design law, so it
 * holds exactly rather than approximately.
 */
export function lensHalfHeight(p: number, peak: number): number {
  const at = clamp(p, 0, 1);
  if (at === 0 || at === 1) return 0;
  return peak * Math.pow(Math.sin(at * Math.PI), LENS_EXPONENT);
}

/** The centre half-height for a given width and aspect ratio. */
export function peakFor(width: number, ratio: number): number {
  return width / ratio / 2;
}

/**
 * The centre half-height the instrument should hold at a given amplitude.
 *
 * INTERPOLATE THE PEAK, NEVER THE RATIO. The ratio is a reciprocal of the
 * opening, so interpolating it directly makes the aperture rush at one end of
 * the range and crawl at the other, which reads as a broken control. This is the
 * single easiest thing in the mark to get wrong, so the interpolation exists in
 * exactly one place and everything else asks this function.
 */
export function peakForAmplitude(width: number, amplitude: number): number {
  const level = clamp(amplitude, 0, 1);
  const idle = peakFor(width, RATIO_IDLE);
  const open = peakFor(width, RATIO_OPEN);
  return idle + (open - idle) * level;
}

/**
 * The openness the instrument should hold at a given amplitude, as a ratio.
 *
 * Amplitude 0 is the logo exactly; amplitude 1 is RATIO_OPEN exactly. The
 * evenness comes from `peakForAmplitude` above.
 *
 * `width` cancels out of the arithmetic — a ratio is scale-free — so the default
 * of 1 is not a normalisation choice, it is just the cheapest way to ask.
 */
export function ratioForAmplitude(amplitude: number, width = 1): number {
  return width / (peakForAmplitude(width, amplitude) * 2);
}

/**
 * A ripple multiplier for one edge at one position and time.
 *
 * `time` is seconds. `sign` is -1 for the top edge, +1 for the bottom. Returns
 * exactly 1 at amplitude 0, so a silent mark is the exact logo.
 *
 * ── Why the ripple only ever CLOSES ───────────────────────────────────────
 *
 * The obvious form is `1 + depth·amplitude·sin(phase)`, which undulates
 * symmetrically about the stated opening. It is wrong, and measurement caught
 * it on a real desktop render: at full amplitude the crests added 18% on top of
 * RATIO_OPEN and the mark opened to 7.07:1 — straight through the founder-
 * ratified 8:1 floor that exists so a lens never reads as an eye. A decorative
 * detail does not get to breach a ratified constraint.
 *
 * So the multiplier spans [1 − depth·amplitude, 1]: the ripple modulates INWARD
 * from the opening rather than around it. The peak half-height is therefore
 * exactly the one `ratioForAmplitude` promises, the floor holds BY CONSTRUCTION
 * at every amplitude and every phase, and the travelling undulation reads the
 * same.
 */
export function rippleAt(p: number, time: number, amplitude: number, sign: number): number {
  if (amplitude <= 0) return 1;
  // Clamped for the same reason the desktop clamps before rippling: an
  // out-of-range level must not deepen the trough into a pinch the mark was
  // never drawn to survive.
  const level = clamp(amplitude, 0, 1);
  const phase = p * RIPPLE.frequency - time * RIPPLE.speed + (sign > 0 ? RIPPLE.edgeOffset : 0);
  return 1 - RIPPLE.depth * level * (0.5 + 0.5 * Math.sin(phase));
}

/**
 * Sample positions for one (width, steps) pair.
 *
 * `profile` is the unit lens — `sin(pπ)^0.85` — which depends only on `p`, so a
 * frame's work is one multiply per point instead of a `pow` and a `sin`. This is
 * an exact factorisation, not an approximation: `lensHalfHeight` is linear in
 * `peak`, so `peak * profile[i]` is the same float as computing it inline.
 *
 * ONE slot, because the instrument redraws at one width for the life of the
 * screen — so the cache hits every frame and can never grow.
 */
let samples: { width: number; steps: number; xs: string[]; profile: number[] } | null = null;

function sampleLens(width: number, steps: number) {
  if (samples && samples.width === width && samples.steps === steps) return samples;
  const xs: string[] = [];
  const profile: number[] = [];
  for (let index = 0; index <= steps; index += 1) {
    const p = index / steps;
    xs.push((p * width).toFixed(2));
    profile.push(lensHalfHeight(p, 1));
  }
  samples = { width, steps, xs, profile };
  return samples;
}

/**
 * The mark's outline as SVG path data, centred on y = 0.
 *
 * Sampled rather than expressed as béziers on purpose: the sampled curve is the
 * exact shape that was reviewed and approved, and every surface — SVG, Canvas,
 * React Native — reproduces the same sampling. A bézier approximation would
 * introduce a second, slightly different mark. `steps` defaults to the code of
 * record's 160; the instrument passes `aperture.steps`.
 */
export function lensPath(width: number, ratio: number, steps = 160): string {
  const peak = peakFor(width, ratio);
  const { xs, profile } = sampleLens(width, steps);
  const top: string[] = [];
  const bottom: string[] = [];
  for (let index = 0; index <= steps; index += 1) {
    const t = peak * profile[index];
    top.push(`${index === 0 ? 'M' : 'L'}${xs[index]} ${(-t).toFixed(2)}`);
    bottom.push(`L${xs[index]} ${t.toFixed(2)}`);
  }
  return `${top.join(' ')} ${bottom.reverse().join(' ')} Z`;
}

/**
 * The instrument's outline at a given amplitude and moment — opening and ripple
 * computed together, exactly as the desktop's `aperturePathData` does.
 *
 * One function rather than two, because the two are one geometry: the opening is
 * the answer to "can you hear me?" and the ripple is a detail drawn on top of
 * that answer. Splitting them across the module and the component is how the
 * ripple got to breach the floor unnoticed on the web.
 *
 * `rippling` false (Reduce Motion) keeps the opening and drops only the gait.
 */
export function aperturePathData(
  width: number,
  amplitude: number,
  seconds: number,
  rippling = true,
): string {
  const steps = aperture.steps;
  const level = clamp(amplitude, 0, 1);
  // Straight from the one interpolation, with no round trip through the ratio —
  // the same arithmetic the desktop does, in the same order.
  const peak = peakForAmplitude(width, level);
  const { xs, profile } = sampleLens(width, steps);
  const moves = rippling && level > 0;
  const top: string[] = [];
  const bottom: string[] = [];
  for (let index = 0; index <= steps; index += 1) {
    const p = index / steps;
    const base = peak * profile[index];
    const upper = moves ? base * rippleAt(p, seconds, level, -1) : base;
    const lower = moves ? base * rippleAt(p, seconds, level, 1) : base;
    top.push(`${index === 0 ? 'M' : 'L'}${xs[index]} ${(-upper).toFixed(2)}`);
    bottom.push(`L${xs[index]} ${lower.toFixed(2)}`);
  }
  return `${top.join(' ')} ${bottom.reverse().join(' ')} Z`;
}

/**
 * A nested resonance contour for the live instrument.
 *
 * The outer aperture remains the canonical mark. These smaller contours sit
 * entirely inside it and make the travelling audio response legible instead of
 * turning the hero into a stock row of equalizer bars. `scale` contracts the
 * whole contour, including the idle peak, so a quiet microphone cannot make an
 * inner layer poke through the outer silhouette.
 */
export function apertureContourPathData(
  width: number,
  amplitude: number,
  seconds: number,
  scale: number,
  phase = 0,
): string {
  const steps = aperture.steps;
  const level = clamp(amplitude, 0, 1);
  const contourScale = clamp(scale, 0, 1);
  const peak = peakForAmplitude(width, level) * contourScale;
  const { xs, profile } = sampleLens(width, steps);
  const top: string[] = [];
  const bottom: string[] = [];
  for (let index = 0; index <= steps; index += 1) {
    const p = index / steps;
    const base = peak * profile[index];
    const upper = base * rippleAt(p, seconds + phase, level, -1);
    const lower = base * rippleAt(p, seconds + phase, level, 1);
    top.push(`${index === 0 ? 'M' : 'L'}${xs[index]} ${(-upper).toFixed(2)}`);
    bottom.push(`L${xs[index]} ${lower.toFixed(2)}`);
  }
  return `${top.join(' ')} ${bottom.reverse().join(' ')} Z`;
}

/**
 * Collapses the rolling amplitude trace into the single drive level for the
 * aperture.
 *
 * The mark is ONE body, so it takes one number — unlike a bar meter, where each
 * bar is its own moment. Recent samples dominate: the newest third of the trace
 * carries the weight, so a consonant opens the slot immediately while the tail
 * keeps it from flickering shut between syllables.
 */
export function apertureAmplitude(trace: readonly number[], listening: boolean): number {
  // An EMPTY trace is an absent measurement, not silence, so it returns 0 rather
  // than the idle floor: before the recorder has reported once we do not yet
  // know the mic is live, and claiming an open aperture on that is the one lie
  // this control exists to prevent. Real callers hand over a zero-filled buffer
  // from `emptyTrace()`, which does floor.
  if (!listening || trace.length === 0) return 0;
  const recent = trace.slice(Math.floor(trace.length * (2 / 3)));
  const peak = recent.reduce((max, value) => Math.max(max, value), 0);
  const mean = recent.reduce((sum, value) => sum + value, 0) / Math.max(1, recent.length);
  // Peak-weighted so the opening tracks emphasis rather than average loudness.
  const level = peak * 0.7 + mean * 0.3;
  return Math.min(1, Math.max(aperture.idleFloor, level));
}
