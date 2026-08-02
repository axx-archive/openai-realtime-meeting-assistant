import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  LENS_EXPONENT,
  RATIO_IDLE,
  RATIO_OPEN,
  RIPPLE,
  TILE_INSET,
  aperture,
  apertureAmplitude,
  apertureContourPathData,
  aperturePathData,
  lensHalfHeight,
  lensPath,
  peakFor,
  peakForAmplitude,
  ratioForAmplitude,
  rippleAt,
} from '../theme/strideSignal';

/**
 * The Stride Signal is identity AND instrument, so the geometry that draws the
 * logo is the same geometry that animates under the microphone. These tests guard
 * the seam between those two jobs.
 *
 * Two of them matter more than the rest:
 *
 *   - THE RESTING-LOGO LAW. At amplitude 0 the instrument must draw RATIO_IDLE to
 *     the number, because that is what makes the talk control and the app icon
 *     the same picture. A "resting state" that is a slightly different drawing is
 *     a second logo, and a brand with two logos has none.
 *   - THE 8:1 FLOOR, WITH THE RIPPLE APPLIED. Past 8:1 a lens reads as an eye,
 *     which is the wrong connotation for a product whose job is remembering what
 *     people said, so the floor binds the whole identity rather than being a soft
 *     guideline for the animation. It is also the bug that actually happened: a
 *     symmetric ripple (`1 + depth·sin`) put the crests 18% past the stated
 *     opening and drove a real desktop render to 7.07:1. Checking
 *     `ratioForAmplitude` alone would have passed that. The check below does not.
 */

/** The width the desktop centrepiece draws at (1024 · TILE_INSET). */
const WIDTH = 1024 * TILE_INSET;

/** Half-heights out of serialised path data — every command carries `x y`. */
function halfHeights(d: string): number[] {
  const tokens = d.replace(/ Z$/, '').split(' ');
  return tokens.filter((_, index) => index % 2 === 1).map((value) => Math.abs(Number(value)));
}

/**
 * The most a serialised coordinate can exceed the true one. `toFixed(2)` is the
 * only lossy step between the geometry and the SVG, so measurements taken off
 * path data get exactly this much slack and not an invented epsilon.
 */
const ROUNDING = 0.005;

/*
 * The cross-surface "faithful print" check — that this module and index.html draw
 * the byte-identical path — lives in `scripts/stride-signal-consistency.test.mjs`
 * at the repository root, together with the desktop and marketing prints. A mobile
 * test reaching three directories up to read the web app couples two suites that
 * have no other reason to know about each other; the root suite already owns
 * cross-surface consistency, and canon §8 names it as the home for these.
 */

test('the constants are the print of the code of record', () => {
  // Regenerate with `node scripts/stride-signal-geometry.mjs --print-ts` rather
  // than editing them. Pinned because a drifted exponent or a drifted cut is a
  // second logo that no visual test on this surface would catch.
  assert.equal(LENS_EXPONENT, 0.85);
  assert.equal(RATIO_IDLE, 25);
  assert.equal(RATIO_OPEN, 8);
  assert.equal(TILE_INSET, 0.66);
  assert.deepEqual({ ...RIPPLE }, { depth: 0.18, frequency: 6.2, speed: 1.9, edgeOffset: 0.8 });
});

test('the lens is closed EXACTLY at both tips', () => {
  // Not "closed to within a rounding error". `Math.sin(Math.PI)` is 1.22e-16 and
  // the exponent carries that residue up to 2.98e-14, which would leave the mark
  // infinitesimally open and make "closed at the tips" a claim the geometry does
  // not actually make. `lensHalfHeight` special-cases the endpoints for exactly
  // this reason, so the assertion is equality.
  for (const peak of [1, 0.02, 13.5168, 42.24, 1e6]) {
    assert.equal(lensHalfHeight(0, peak), 0);
    assert.equal(lensHalfHeight(1, peak), 0);
    // Out of range clamps into the closed tips rather than producing a stray
    // value: callers sample `p` from a loop, and an off-by-one must not draw.
    assert.equal(lensHalfHeight(-0.4, peak), 0);
    assert.equal(lensHalfHeight(1.7, peak), 0);
  }
});

test('the lens peaks exactly at the centre and is symmetric about it', () => {
  // sin(π/2) is exactly 1, so the centre height is the stated peak to the bit —
  // which is what lets `ratioForAmplitude` promise an opening and the drawing
  // deliver precisely that one.
  for (const peak of [1, 13.5168, 42.24]) {
    assert.equal(lensHalfHeight(0.5, peak), peak);
    for (let step = 1; step < 500; step += 1) {
      const p = step / 1000;
      const left = lensHalfHeight(p, peak);
      const right = lensHalfHeight(1 - p, peak);
      // The residue is the same 3e-14 that forces the endpoint special case; the
      // shape is symmetric, the floating-point evaluation of it is not quite.
      assert.ok(Math.abs(left - right) < 1e-12 * peak, `asymmetric at p=${p}`);
      assert.ok(left <= peak, `p=${p} rose above the peak`);
      // Monotonically rising to the centre — a lens has one belly, not two.
      assert.ok(left > lensHalfHeight(p - 0.001, peak), `p=${p} did not rise`);
    }
  }
});

test('amplitude 0 is the logo, exactly — the resting-logo law', () => {
  for (const width of [1, WIDTH, 258.06, 33]) {
    assert.equal(ratioForAmplitude(0, width), RATIO_IDLE);
    assert.equal(peakForAmplitude(width, 0), peakFor(width, RATIO_IDLE));
  }
  // End to end: the instrument at rest draws the logo's own path data, at every
  // moment in the ripple's clock. Pressing the control cannot swap one picture
  // for another, because at amplitude 0 there is only one picture to draw.
  const logo = lensPath(WIDTH, RATIO_IDLE, aperture.steps);
  for (const seconds of [0, 0.31, 2.7, 91.4]) {
    assert.equal(aperturePathData(WIDTH, 0, seconds, true), logo);
    assert.equal(aperturePathData(WIDTH, 0, seconds, false), logo);
  }
  // Silence is not the same input as "not listening", but it resolves to the same
  // drawing when there is no measurement at all.
  assert.equal(aperturePathData(WIDTH, apertureAmplitude([], true), 0, true), logo);
});

test('the opening is EVEN — the peak interpolates, never the ratio', () => {
  // The ratio is a reciprocal of the opening. Interpolating it directly makes the
  // aperture rush at one end of the range and crawl at the other, which reads as
  // a broken control rather than a smooth one, so the interpolation happens on
  // the peak. Equal steps of amplitude, equal steps of height.
  const levels = [0, 0.2, 0.4, 0.6, 0.8, 1];
  const peaks = levels.map((level) => peakForAmplitude(WIDTH, level));
  const first = peaks[1] - peaks[0];
  for (let index = 2; index < peaks.length; index += 1) {
    const step = peaks[index] - peaks[index - 1];
    assert.ok(Math.abs(step - first) < 1e-12, `step ${index} was ${step}, not ${first}`);
  }
  // The same amplitudes through the public round trip stay even, so a consumer
  // that asks for a ratio and converts it back is not handed a different curve.
  const roundTrip = levels.map((level) => peakFor(WIDTH, ratioForAmplitude(level, WIDTH)));
  for (let index = 0; index < peaks.length; index += 1) {
    assert.ok(Math.abs(roundTrip[index] - peaks[index]) < 1e-9, `round trip drifted at ${index}`);
  }
  // And the reason this test exists: the RATIO's own steps are wildly uneven.
  // 25 → 16.3 → 12.1 → 9.6 → 8. Lerping that is the mistake being guarded.
  const ratios = levels.map((level) => ratioForAmplitude(level, WIDTH));
  assert.ok(
    Math.abs((ratios[1] - ratios[0]) - (ratios[5] - ratios[4])) > 5,
    'the ratio steps are suspiciously even — is the wrong quantity being lerped?',
  );
});

test('the ripple only ever CLOSES the aperture', () => {
  // The whole floor rests on this: the multiplier spans [1 - depth·amplitude, 1],
  // so the ripple modulates INWARD from the stated opening instead of around it.
  // A symmetric form would put crests OUTSIDE the opening, and outside the
  // opening at full amplitude is outside the identity.
  for (const amplitude of [0.01, 0.14, 0.5, 0.99, 1, 4]) {
    let lowest = 1;
    for (let step = 0; step <= 400; step += 1) {
      const p = step / 400;
      for (const seconds of [0, 0.17, 1.03, 5.5, 40.9]) {
        // -1 is the top edge, +1 the bottom; they carry a phase offset from each
        // other so the two edges never mirror into a pulsing tube.
        for (const sign of [-1, 1]) {
          const value = rippleAt(p, seconds, amplitude, sign);
          assert.ok(value <= 1, `ripple opened past the cut: ${value}`);
          assert.ok(value > 0, `ripple pinched the lens shut: ${value}`);
          lowest = Math.min(lowest, value);
        }
      }
    }
    // It must still actually ripple — a multiplier pinned at 1 would pass the
    // assertion above while drawing a dead mark.
    assert.ok(lowest < 1 - RIPPLE.depth * Math.min(1, amplitude) * 0.9, 'the ripple is flat');
  }
  // Rest is the logo here too: exactly 1, so silence adds nothing at all.
  for (const seconds of [0, 3.3]) {
    for (let step = 0; step <= 40; step += 1) {
      assert.equal(rippleAt(step / 40, seconds, 0, -1), 1);
      assert.equal(rippleAt(step / 40, seconds, -2, 1), 1);
    }
  }

  // The two edges are offset in phase, so at a given moment they are not the same
  // multiplier. Mirrored edges would read as a tube inflating, not a ripple.
  assert.notEqual(rippleAt(0.3, 0.4, 1, -1), rippleAt(0.3, 0.4, 1, 1));
});

test('resonance contours stay nested inside the canonical Signal shell', () => {
  for (const amplitude of [0, aperture.idleFloor, 0.4, 0.75, 1]) {
    for (let tick = 0; tick < 60; tick += 1) {
      const seconds = tick / 30;
      const shell = halfHeights(aperturePathData(WIDTH, amplitude, seconds, true));
      for (const [scale, phase] of [[0.58, 0.34], [0.19, 0.72]] as const) {
        const contour = halfHeights(
          apertureContourPathData(WIDTH, amplitude, seconds, scale, phase),
        );
        assert.equal(contour.length, shell.length);
        for (let index = 0; index < shell.length; index += 1) {
          assert.ok(
            contour[index] <= shell[index] + ROUNDING,
            `scale ${scale} escaped the shell at amplitude ${amplitude}, point ${index}`,
          );
        }
      }
    }
  }
});

test('the 8:1 floor holds at every amplitude, at every ripple phase', () => {
  // The floor binds STATIC ARTWORK AS WELL AS ANIMATION, so it is checked on the
  // drawn path rather than on the promise: this measures the widest half-height
  // the instrument ever serialises and converts it back to an aspect ratio. It is
  // the check that catches a decorative detail breaching a ratified constraint,
  // which is precisely what happened on the web.
  const amplitudes = [-3, -0.001, 0, 0.001, 0.14, 0.33, 0.5, 0.72, 0.99, 1, 1.0001, 2, 250];
  const promised = peakForAmplitude(WIDTH, 1);
  for (const amplitude of amplitudes) {
    for (let tick = 0; tick < 240; tick += 1) {
      // Sweeps well over a full ripple cycle at a granularity finer than a frame.
      const seconds = tick * 0.0131;
      const tallest = Math.max(...halfHeights(aperturePathData(WIDTH, amplitude, seconds, true)));
      assert.ok(
        tallest <= promised + ROUNDING,
        `amplitude ${amplitude} at ${seconds}s opened to ${tallest}, past ${promised}`,
      );
      const effective = WIDTH / (2 * (tallest - ROUNDING));
      assert.ok(
        effective >= RATIO_OPEN,
        `amplitude ${amplitude} at ${seconds}s reached ${effective.toFixed(3)}:1`,
      );
    }
    // The promise itself never breaches the floor either, in or out of range.
    assert.ok(ratioForAmplitude(amplitude, WIDTH) >= RATIO_OPEN);
    assert.ok(ratioForAmplitude(amplitude, WIDTH) <= RATIO_IDLE);
  }
  // Full amplitude must actually REACH the floor, or the instrument is quietly
  // holding back the top of its range.
  assert.equal(ratioForAmplitude(1, WIDTH), RATIO_OPEN);
});

test('lensPath is closed and straddles y = 0', () => {
  for (const ratio of [RATIO_IDLE, 12, RATIO_OPEN]) {
    const d = lensPath(WIDTH, ratio, aperture.steps);
    assert.ok(d.startsWith('M0.00 0.00 '), `path does not open at the closed tip: ${d.slice(0, 24)}`);
    assert.ok(d.endsWith(' L0.00 0.00 Z'), `path does not close back at the tip: ${d.slice(-24)}`);

    const ys = d.replace(/ Z$/, '').split(' ').filter((_, index) => index % 2 === 1).map(Number);
    // One closed outline around the centre line: a top edge, a bottom edge, and
    // both tips on y = 0. A path that sat entirely above or below zero would
    // render as a mark hanging off its own baseline on every surface at once.
    assert.ok(ys.some((y) => y < 0), 'no top edge');
    assert.ok(ys.some((y) => y > 0), 'no bottom edge');
    assert.equal(ys[0], 0);
    assert.equal(ys[ys.length - 1], 0);
    assert.equal(ys.length, (aperture.steps + 1) * 2);

    // Mirrored: the bottom half is the top half reversed and negated. Summed
    // rather than negated-and-compared because `assert.equal` is Object.is, and
    // Object.is(0, -0) is false.
    const top = ys.slice(0, aperture.steps + 1);
    const bottom = ys.slice(aperture.steps + 1).reverse();
    for (let index = 0; index <= aperture.steps; index += 1) {
      assert.equal(bottom[index] + top[index], 0, `edges do not mirror at ${index}`);
      assert.ok(top[index] <= 0 && bottom[index] >= 0, `edges crossed at ${index}`);
    }
    // The widest point is the promised peak, to the serialiser's resolution.
    const tallest = Math.max(...ys.map(Math.abs));
    assert.ok(Math.abs(tallest - peakFor(WIDTH, ratio)) <= ROUNDING, `peak was ${tallest}`);
  }
});

test('rest ignores live input entirely — the breathe law', () => {
  // A loud trace while NOT listening must not open the mark at all, however loud
  // the room is.
  const loud = Array.from({ length: 28 }, () => 0.95);
  assert.equal(apertureAmplitude(loud, false), 0);
});

test('while listening the level stays between the idle floor and full scale', () => {
  const silence = Array.from({ length: 28 }, () => 0);
  const full = Array.from({ length: 28 }, () => 1);
  // Silence with the mic open is NOT stillness: an open microphone has to look
  // different from a closed one or the control cannot answer "are you hearing
  // me?" at all.
  assert.equal(apertureAmplitude(silence, true), aperture.idleFloor);
  assert.equal(apertureAmplitude(full, true), 1);

  // Nothing in between may escape the band either — an overshoot past 1 would
  // drive the aperture through the 8:1 floor by the front door.
  for (let seed = 0; seed < 64; seed += 1) {
    const trace = Array.from({ length: 28 }, (_, index) => ((seed * 7 + index * 11) % 21) / 20);
    const level = apertureAmplitude(trace, true);
    assert.ok(
      level >= aperture.idleFloor && level <= 1,
      `level ${level} escaped the band`,
    );
  }
  // Out-of-range samples cannot push the opening past the cut either. The trace
  // comes from `normalizeMetering`, which already bounds itself — this documents
  // that the mark does not depend on that being true.
  assert.equal(apertureAmplitude(Array.from({ length: 12 }, () => 4), true), 1);
});

test('the newest third of the trace carries the weight', () => {
  // The opening has to track the syllable you are speaking now, not the average
  // of the last three seconds — otherwise the mark stays open after you stop.
  const half = 14;
  const risen = [...Array.from({ length: half }, () => 0), ...Array.from({ length: half }, () => 1)];
  const fallen = [...Array.from({ length: half }, () => 1), ...Array.from({ length: half }, () => 0)];
  assert.ok(
    apertureAmplitude(risen, true) > apertureAmplitude(fallen, true),
    'a rising trace must drive harder than a decaying one',
  );
  // Peak-weighted, not averaged: one emphatic sample in the recent window has to
  // register, because emphasis is the thing a level meter exists to show.
  const flat = Array.from({ length: 12 }, () => 0.3);
  const spiked = [...flat.slice(0, 11), 0.9];
  assert.ok(apertureAmplitude(spiked, true) > apertureAmplitude(flat, true));
});

test('a trace we have never sampled reads as no signal', () => {
  // Documents the guard rather than the floor: an EMPTY array is not silence, it
  // is the absence of a measurement (a recorder that has not yet reported), and
  // inventing an idle floor for it would mean the mark opens before the mic is
  // live. Callers hand over a zero-filled buffer for real silence.
  assert.equal(apertureAmplitude([], true), 0);
});
