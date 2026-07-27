// Imported from the pure geometry module rather than `theme/motion`, which
// pulls in the react-native runtime and would make this file untestable.
import { waveform } from '../theme/waveformGeometry';

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
 * Spreads one amplitude across the bar row.
 *
 * A single microphone gives one number, but one number rendered as five
 * identical bars looks like a loading indicator. The profile weights the centre
 * bars highest so the row reads as a voice — and it is a fixed shape scaled by
 * live amplitude, never an independent per-bar animation, so the row still obeys
 * the breathe-only-while-listening law (design §8).
 */
const PROFILE = [0.45, 0.78, 1, 0.78, 0.45];

export function barScales(amplitude: number, listening: boolean): number[] {
  if (!listening) {
    // Rest is STATIC. This is the law, and it is why the waveform carries
    // information at all.
    return PROFILE.map(() => waveform.restScale);
  }
  return PROFILE.slice(0, waveform.barCount).map((weight) =>
    Math.max(waveform.minScale, Math.min(1, amplitude * weight)),
  );
}
