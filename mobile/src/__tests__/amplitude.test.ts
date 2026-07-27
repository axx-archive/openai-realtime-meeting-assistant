import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CEILING_DB,
  NOISE_FLOOR_DB,
  barScales,
  emptyTrace,
  normalizeMetering,
  pushTrace,
  smoothAmplitude,
} from '../voice/amplitude';
import { RESTING_PROFILE, waveform } from '../theme/waveformGeometry';

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

test('rest ignores live input entirely — the breathe law', () => {
  // A loud trace while NOT listening must not move the bars at all.
  const loud = Array.from({ length: waveform.barCount }, () => 0.95);
  assert.deepEqual(barScales(loud, false), RESTING_PROFILE.slice(0, waveform.barCount));
});

test('rest is a SCULPTED silhouette, not a flat row of ticks', () => {
  // The regression this pins: collapsing every resting bar to one short uniform
  // height turns the hero of the canvas into an ellipsis glyph. The web canon
  // rests at fourteen varied heights; mobile must too. "Static" != "flat".
  const resting = barScales(emptyTrace(), false);
  const unique = new Set(resting);
  assert.ok(unique.size > 8, `resting profile had only ${unique.size} distinct heights`);
  assert.ok(Math.max(...resting) > 0.8, 'resting silhouette needs a real crest');
  assert.ok(Math.min(...resting) < 0.2, 'resting silhouette needs a real trough');
});

test('the trace scrolls: newest sample lands last, oldest falls off', () => {
  let trace = emptyTrace();
  trace = pushTrace(trace, 0.4);
  trace = pushTrace(trace, 0.9);
  assert.equal(trace.length, waveform.barCount);
  assert.equal(trace[trace.length - 1], 0.9);
  assert.equal(trace[trace.length - 2], 0.4);
});

test('the trace never grows past the bar count', () => {
  let trace = emptyTrace();
  for (let index = 0; index < waveform.barCount * 3; index += 1) {
    trace = pushTrace(trace, index / 100);
  }
  assert.equal(trace.length, waveform.barCount);
});

test('while listening the row reflects the trace and stays within bounds', () => {
  const trace = Array.from({ length: waveform.barCount }, () => 1);
  const bars = barScales(trace, true);
  assert.equal(bars.length, waveform.barCount);
  assert.ok(bars.every((scale) => scale >= waveform.minScale && scale <= 1));
  // The taper weights the centre, so the middle must out-rank the edge.
  const middle = Math.floor(waveform.barCount / 2);
  assert.ok(bars[middle] > bars[0], 'centre should be taller than the edge under a flat trace');
});

test('silence while listening still shows a floor, so the row never vanishes', () => {
  assert.ok(barScales(emptyTrace(), true).every((scale) => scale >= waveform.minScale));
});
