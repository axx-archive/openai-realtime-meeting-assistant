/**
 * THE STRIDE SIGNAL IS ONE MARK.
 *
 *   npm run test:brand
 *
 * The mark is an aperture — one closed horizontal lens whose OPENNESS carries
 * meaning. It is defined once, in `stride-signal-geometry.mjs`, and every
 * surface reads off that module. These tests pin the canon: the curve, the hard
 * floor on how far it may open, the promise that silence renders the logo
 * exactly, and the rule that the product has one orange.
 *
 * Desktop and native carry runtime-safe prints of these constants. This suite
 * pins the desktop print; the native suite pins its own. Marketing lives in a
 * separately deployed repository and owns an equivalent rendered-contract test.
 * Release verification compares all three without coupling their package graphs.
 */
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import {
  CANVAS,
  CUTS,
  LENS_EXPONENT,
  RATIO_IDLE,
  RATIO_OPEN,
  STRIDE_INK,
  STRIDE_ORANGE,
  lensHalfHeight,
  lensPath,
  peakFor,
  ratioForAmplitude,
  rippleAt,
  safeFit,
  strideSignalSvg,
} from './stride-signal-geometry.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const read = (path) => readFileSync(resolve(root, path), 'utf8');

test('the curve is a lens: closed at the tips, fullest at the centre', () => {
  assert.equal(LENS_EXPONENT, 0.85);
  const peak = 100;
  // Closed at both ends — this is what lets the mark scale to a hairline
  // without the tips turning into blunt stubs.
  assert.equal(lensHalfHeight(0, peak), 0);
  assert.equal(lensHalfHeight(1, peak), 0);
  assert.equal(lensHalfHeight(0.5, peak), peak);
  // Symmetric, and rising toward the middle.
  for (let i = 2; i <= 10; i += 1) {
    const p = i / 20;
    assert.ok(
      Math.abs(lensHalfHeight(p, peak) - lensHalfHeight(1 - p, peak)) < 1e-9,
      `the lens must be symmetric about its centre (p=${p})`,
    );
    assert.ok(
      lensHalfHeight(p, peak) > lensHalfHeight(p - 0.05, peak),
      `rises toward the centre at p=${p}`,
    );
  }
  // The belly: at 0.85 the form is fuller than an ellipse at the quarter point,
  // which is the whole reason for the exponent.
  assert.ok(lensHalfHeight(0.25, peak) > peak * Math.sin(0.25 * Math.PI));
});

test('8:1 is a hard floor on the entire identity, static and moving', () => {
  assert.equal(RATIO_OPEN, 8);
  assert.equal(RATIO_IDLE, 25);

  // A lens is eye-adjacent, and the wider it opens the more it reads as an eye
  // rather than a slot. Nothing in the identity may cross that line — including
  // the static artwork cuts, which is why the small icon sizes find their
  // legibility by getting WIDER rather than by opening further.
  for (const [name, ratio] of Object.entries(CUTS)) {
    assert.ok(ratio >= RATIO_OPEN, `cut "${name}" opens past the floor (${ratio}:1)`);
    assert.ok(ratio <= RATIO_IDLE, `cut "${name}" is tighter than the logo (${ratio}:1)`);
  }
  // And the animation cannot escape it at any amplitude, including out of range.
  for (const amplitude of [-1, 0, 0.1, 0.5, 0.9, 1, 2, Infinity]) {
    const ratio = ratioForAmplitude(amplitude);
    assert.ok(ratio >= RATIO_OPEN - 1e-9, `amplitude ${amplitude} opened to ${ratio}:1`);
    assert.ok(ratio <= RATIO_IDLE + 1e-9, `amplitude ${amplitude} closed past idle to ${ratio}:1`);
  }

  // WITH THE RIPPLE APPLIED — the check the first version of this test missed.
  // A symmetric ripple pushed the crests 18% past the opening and drove the mark
  // to 7.07:1 in a real render. The multiplier must never exceed 1, at any phase,
  // any position, any amplitude, or the floor is decorative rather than real.
  for (const amplitude of [0.1, 0.5, 0.9, 1, 2]) {
    for (let step = 0; step <= 64; step += 1) {
      const p = step / 64;
      for (const time of [0, 0.37, 1.1, 2.9, 7.3]) {
        for (const sign of [-1, 1]) {
          const multiplier = rippleAt(p, time, amplitude, sign);
          assert.ok(
            multiplier <= 1 + 1e-12,
            `ripple opened past the floor: ${multiplier} at amp ${amplitude}, p ${p}, t ${time}`,
          );
          assert.ok(multiplier > 0, `ripple must not invert the edge (${multiplier})`);
        }
      }
    }
  }
});

test('silence renders the logo exactly — the resting-mark law', () => {
  // Amplitude 0 must be the logo to the number, not merely close to it: the
  // resting talk control and the app icon are meant to be the same picture, so
  // pressing the control never swaps one image for another.
  assert.equal(ratioForAmplitude(0), RATIO_IDLE);
  // And the ripple must vanish, or a silent mark would still be undulating.
  for (const p of [0, 0.25, 0.5, 0.75, 1]) {
    for (const sign of [-1, 1]) {
      assert.equal(rippleAt(p, 12.34, 0, sign), 1, `ripple at silence must be exactly 1 (p=${p})`);
    }
  }
  // Full amplitude reaches the floor, so the range is actually used.
  assert.ok(Math.abs(ratioForAmplitude(1) - RATIO_OPEN) < 1e-9);
});

test('the opening is even, because it interpolates the peak and not the ratio', () => {
  // The ratio is a reciprocal of the opening. Interpolating it directly makes
  // the aperture rush at one end of the range and crawl at the other, which
  // reads as a broken control rather than a smooth one. Equal steps of
  // amplitude must give equal steps of HEIGHT.
  const peakAt = (a) => peakFor(CANVAS, ratioForAmplitude(a));
  const steps = [0, 0.25, 0.5, 0.75, 1].map(peakAt);
  const deltas = steps.slice(1).map((v, i) => v - steps[i]);
  for (const delta of deltas) {
    assert.ok(Math.abs(delta - deltas[0]) < 1e-6, `uneven opening: ${deltas.join(', ')}`);
    assert.ok(delta > 0, 'the aperture must open as amplitude rises');
  }
});

test('the outline is a closed path, symmetric about its own axis', () => {
  const d = lensPath(600, CUTS.icon, 40);
  assert.match(d, /^M/, 'starts with a move');
  assert.match(d, /Z$/, 'closes');
  const ys = [...d.matchAll(/-?[\d.]+ (-?[\d.]+)/g)].map((m) => Number(m[1]));
  const top = Math.min(...ys);
  const bottom = Math.max(...ys);
  assert.ok(Math.abs(top + bottom) < 1e-6, 'the lens must straddle y=0 evenly');
  assert.ok(Math.abs(bottom - peakFor(600, CUTS.icon)) < 1e-6, 'peak matches the cut');
});

test('a circular crop never blunts the tips', () => {
  // Launchers crop to a circle and the aperture is mostly width, so the tips
  // are the first thing to go. safeFit has to actually pull them inside.
  for (const ratio of Object.values(CUTS)) {
    for (const radius of [0.4, 0.35]) {
      const width = CANVAS * 0.66;
      const scale = safeFit(width, ratio, radius);
      const halfW = (width * scale) / 2;
      const halfH = peakFor(width, ratio) * scale;
      assert.ok(
        Math.hypot(halfW, halfH) <= radius * CANVAS + 1e-6,
        `${ratio}:1 escapes the ${radius} safe circle`,
      );
    }
  }
  // A useful thing this proved: at the standard 0.66 inset the lens ALREADY
  // fits every safe circle, because a lens is nearly all width and 0.33 of the
  // tile is inside a 0.38 radius. So safeFit is a no-op there, and demanding a
  // shrink would be testing the wrong thing. Assert the property instead.
  assert.equal(safeFit(CANVAS * 0.66, CUTS.micro, 0.4), 1, 'the standard inset needs no shrink');
  // But it must genuinely bite when the mark is set wider than the crop allows.
  const wide = safeFit(CANVAS * 0.9, CUTS.micro, 0.35);
  assert.ok(wide > 0 && wide < 1, `a 0.9 inset must be pulled in, got ${wide}`);
  assert.match(strideSignalSvg({}), /scale\(1\.00000\)/);
});

test('the web app and native app draw the mark in one orange', () => {
  assert.equal(STRIDE_ORANGE, '#FF5A19');
  assert.equal(STRIDE_INK, '#050505');
  // The desktop accent ramp and native tokens both carry the
  // brand hue as a literal. Two different oranges in one product is not a bug
  // anyone files — it is a slow loss of the thing that makes an identity feel
  // deliberate.
  assert.match(read('index.html'), /--ember-500: #FF5A19;/);
  assert.match(read('mobile/src/theme/tokens.ts'), /500: '#FF5A19'/);
});

test('no surface still hardcodes the retired coral', () => {
  /**
   * This test exists because I missed four places by hand.
   *
   * Moving the accent from #FF6B4A to Stride Orange is easy to do
   * incompletely: the token is one line, but the glow shadows, the canvas halo
   * gradient, the mention text-shadow, and the printed report's inlined CSS all
   * carry the colour as a LITERAL, in three notations and with inconsistent
   * spacing. The worst of them sat directly behind the mark on the phone
   * canvas, so it would have been haloed in an orange it is not.
   */
  const retired = [
    /#ff6b4a/i,
    /#f0522f/i,
    /#ff8163/i,
    /#ff9e85/i,
    /rgba\(\s*255\s*,\s*107\s*,\s*74\s*,/i,
  ];
  const surfaces = [
    'index.html',
    'report_print.go',
    'mobile/src/theme/tokens.ts',
    // Carries its OWN copy of the hex on purpose (tokens.ts imports
    // react-native, which node:test cannot load), so it is exactly the file
    // most able to go stale without anyone noticing.
    'mobile/src/__tests__/contrast.test.ts',
    'mobile/src/screens/CanvasScreen.tsx',
    'mobile/src/messaging/MentionComposerInput.tsx',
    'frontend_latency_test.go',
  ];

  for (const surface of surfaces) {
    // Comments may still discuss the old value — that history is worth keeping.
    const source = read(surface)
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .split('\n')
      .filter((line) => !/^\s*(\/\/|\*|#)/.test(line))
      .join('\n');
    for (const pattern of retired) {
      assert.doesNotMatch(
        source,
        pattern,
        `${surface} still declares the retired coral (${pattern.source})`,
      );
    }
  }
});
