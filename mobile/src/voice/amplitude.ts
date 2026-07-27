// Imported from the pure geometry module rather than `theme/motion`, which
// pulls in the react-native runtime and would make this file untestable.
import { RESTING_PROFILE, TRACE_TAPER, waveform } from '../theme/waveformGeometry';

/**
 * dBFS → bar heights. This is the file that makes the waveform an *instrument*
 * rather than an animation (design §3): every bar height traces back to a real
 * microphone sample, so motion on screen means "I can hear you" and stillness
 * means "I can't."
 */

/**
 * Anything below this reads as room tone, not speech. expo-audio reports metering
 * in dBFS: 0 is clipping, -160 is digital silence. Normal speech into a phone
 * held at conversational distance lands around -30..-10.
 */
export const NOISE_FLOOR_DB = -50;
/** Where we treat the signal as full scale. Above this you are shouting. */
export const CEILING_DB = -8;

/**
 * Maps a dBFS reading to 0..1.
 *
 * The curve is deliberately NOT linear in dB. A linear map spends most of its
 * range on levels that never occur in practice, so the bars barely move during
 * normal speech — the exact failure that makes most waveform UIs look fake. The
 * square root expands the quiet end where conversational speech actually sits.
 */
export function normalizeMetering(db: number | undefined): number {
  if (db == null || !Number.isFinite(db)) return 0;
  const clamped = Math.min(CEILING_DB, Math.max(NOISE_FLOOR_DB, db));
  const linear = (clamped - NOISE_FLOOR_DB) / (CEILING_DB - NOISE_FLOOR_DB);
  return Math.sqrt(linear);
}

/**
 * Asymmetric smoothing: rise fast, fall slow.
 *
 * Attack is near-instant so a consonant registers on the very next sample —
 * latency here reads directly as "it isn't hearing me." Release is gradual so
 * the bars don't strobe on the natural gaps between syllables. This is the same
 * shape as an audio level meter's ballistics, for the same reason.
 */
export function smoothAmplitude(previous: number, next: number): number {
  const coefficient = next > previous ? 0.55 : 0.14;
  return previous + (next - previous) * coefficient;
}

/**
 * Appends one sample to the rolling trace, oldest first.
 *
 * The live waveform is a SCROLLING HISTORY of the last couple of seconds of your
 * voice, not a symmetric pulse. A pulse is decoration — every bar shows the same
 * number. A trace is a record: each bar is a distinct moment you actually spoke,
 * so you can see the shape of the sentence you just said. It is also what a
 * person expects a waveform to mean.
 */
export function pushTrace(history: readonly number[], sample: number): number[] {
  const next = history.length >= waveform.barCount ? history.slice(1) : history.slice();
  next.push(sample);
  return next;
}

/** A trace buffer pre-filled with silence, so the row starts flat and fills in. */
export function emptyTrace(): number[] {
  return Array.from({ length: waveform.barCount }, () => 0);
}

/**
 * Resolves the bar heights to render.
 *
 * At rest this returns the sculpted RESTING_PROFILE — static, per the
 * breathe-only-while-listening law, but still shaped like a voice. Rest means
 * "not animating"; it does not mean "flat". A row of identical short ticks reads
 * as an ellipsis glyph, not as the hero of the screen.
 */
export function barScales(trace: readonly number[], listening: boolean): number[] {
  if (!listening) {
    return RESTING_PROFILE.slice(0, waveform.barCount);
  }
  return Array.from({ length: waveform.barCount }, (_, index) => {
    const sample = trace[index] ?? 0;
    const shaped = sample * (TRACE_TAPER[index] ?? 1);
    return Math.max(waveform.minScale, Math.min(1, shaped));
  });
}
