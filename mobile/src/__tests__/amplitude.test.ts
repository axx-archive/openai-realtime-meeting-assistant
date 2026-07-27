import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CEILING_DB,
  NOISE_FLOOR_DB,
  barScales,
  normalizeMetering,
  smoothAmplitude,
} from '../voice/amplitude';
import { waveform } from '../theme/waveformGeometry';

/**
 * The waveform is an instrument, not an animation (design §3) — so these are
 * the tests that keep it honest. The one that matters most is the LAST one:
 * bars must rest static when not listening, which is the
 * breathe-only-while-listening law inherited from the web client.
 */

test('metering maps to 0..1 across the usable range', () => {
  assert.equal(normalizeMetering(NOISE_FLOOR_DB), 0);
  assert.equal(normalizeMetering(CEILING_DB), 1);
  assert.ok(normalizeMetering(-30) > 0 && normalizeMetering(-30) < 1);
});

test('levels outside the range clamp rather than overshoot', () => {
  // -160 dBFS is digital silence; 0 is clipping. Neither may push a bar past
  // its bounds or the row visibly breaks its layout.
  assert.equal(normalizeMetering(-160), 0);
  assert.equal(normalizeMetering(0), 1);
});

test('missing or non-finite metering reads as silence, not NaN', () => {
  // expo-audio omits `metering` entirely until the recorder is primed; a NaN
  // here would propagate into a transform and blank the waveform.
  assert.equal(normalizeMetering(undefined), 0);
  assert.equal(normalizeMetering(Number.NaN), 0);
});

test('the curve expands the quiet end where conversational speech sits', () => {
  // A linear-in-dB map spends most of its range on levels that never occur, so
  // the bars barely move during normal speech. The midpoint must land well
  // above 0.5 for the row to read as responsive.
  const midpoint = (NOISE_FLOOR_DB + CEILING_DB) / 2;
  assert.ok(normalizeMetering(midpoint) > 0.65, `midpoint was ${normalizeMetering(midpoint)}`);
});

test('smoothing attacks faster than it releases', () => {
  const rise = smoothAmplitude(0.2, 0.9) - 0.2;
  const fall = 0.9 - smoothAmplitude(0.9, 0.2);
  assert.ok(rise > fall, `attack ${rise} should exceed release ${fall}`);
});

test('smoothing always moves toward the target and never overshoots it', () => {
  assert.ok(smoothAmplitude(0.2, 0.9) < 0.9);
  assert.ok(smoothAmplitude(0.9, 0.2) > 0.2);
});

test('bars rest STATIC when not listening — the breathe law', () => {
  const resting = barScales(0.95, false);
  // Loud input while NOT listening must still produce a flat, calm line.
  assert.ok(resting.every((scale) => scale === waveform.restScale));
});

test('while listening the row is centre-weighted and stays within bounds', () => {
  const bars = barScales(1, true);
  assert.equal(bars.length, waveform.barCount);
  assert.ok(bars.every((scale) => scale >= waveform.minScale && scale <= 1));
  const middle = Math.floor(waveform.barCount / 2);
  assert.ok(bars[middle] >= bars[0], 'centre bar should not be shorter than the edge');
});

test('silence while listening still shows a floor, so the row never vanishes', () => {
  assert.ok(barScales(0, true).every((scale) => scale >= waveform.minScale));
});
