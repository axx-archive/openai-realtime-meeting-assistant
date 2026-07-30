/**
 * THE STRIDE MOTION GEOMETRY IS PINNED.
 *
 *   npm run test:brand
 *
 * The legacy aperture geometry remains inert while the six-mass Signal Cradle
 * owns visible audio response. These tests keep that compatibility geometry,
 * the live cradle prints, the new Strike identity source, and the product's one
 * orange from drifting independently.
 *
 * ── The three prints ──────────────────────────────────────────────────────
 *
 * The desktop markup, the native theme module, and the marketing module each
 * carry their own copy of these constants, because none of them can import a
 * Node module at runtime. Each gets a "faithful print" test below.
 *
 * This is the whole reason the file exists. Three hand-copied numbers in three
 * files is how a logo drifts, and the drift is invisible — nobody notices the
 * phone's mark is a percent thinner than the browser's until the two appear in
 * the same screenshot. If one of these fails, do not edit the copy by hand:
 * regenerate it from the code of record.
 */
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
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
  RIPPLE,
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
const readBytes = (path) => readFileSync(resolve(root, path));
const sha256 = (value) => createHash('sha256').update(value).digest('hex');

test('The Strike vector is the static identity source', () => {
  assert.equal(
    sha256(readBytes('brand/stride-strike-source.svg')),
    '6a9ea0e4858dd5d6e15842766b646aa807ee6da9bf6c9d87eb0111820e621475',
  );
  assert.match(read('brand/stride-strike-source.svg'), /Stride — The Strike/);
  assert.match(read('brand/stride-strike-source.svg'), /cx="-61\.44"[\s\S]*cx="614\.4"[\s\S]*cx="1024"/);
  assert.match(read('mobile/src/components/BrandMark.tsx'), /stride-logo-(?:mark|black|white)\.png/g);
  assert.match(read('stride-site/app/components/BrandMark.tsx'), /brand-mark-\$\{tone\}\.png/);
});

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

test('silence renders the inert aperture at its exact rest geometry', () => {
  // Amplitude 0 must remain deterministic even though the aperture is no
  // longer the visible static identity or live instrument.
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

test('every surface draws the mark in one orange', () => {
  assert.equal(STRIDE_ORANGE, '#FF5A19');
  assert.equal(STRIDE_INK, '#050505');
  // The desktop accent ramp, the native tokens, and the site all carry the
  // brand hue as a literal. Two different oranges in one product is not a bug
  // anyone files — it is a slow loss of the thing that makes an identity feel
  // deliberate.
  assert.match(read('index.html'), /--ember-500: #FF5A19;/);
  assert.match(read('mobile/src/theme/tokens.ts'), /500: '#FF5A19'/);
  assert.match(read('stride-site/app/globals.css'), /--orange: #ff5a19;/i);
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
    'stride-site/app/globals.css',
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

/* ── the three prints ─────────────────────────────────────────────────────── */

test('the desktop keeps the legacy aperture geometry inert while the cradle owns visible motion', () => {
  const html = read('index.html');

  // EXACTLY what the code of record draws at the idle cut. Not visually
  // equivalent — identical, so there is one mark and not one mark plus a
  // rounding of it.
  const drawn = html.match(/id="officeAperturePath" d="([^"]+)"/);
  assert.ok(drawn, 'index.html has no aperture path');
  assert.equal(
    drawn[1],
    lensPath(CANVAS * 0.66, RATIO_IDLE, 64),
    'the desktop idle path has drifted from the code of record — regenerate it',
  );

  for (const [name, value] of [
    ['APERTURE_WIDTH', (CANVAS * 0.66).toFixed(2)],
    ['APERTURE_RATIO_IDLE', String(RATIO_IDLE)],
    ['APERTURE_RATIO_OPEN', String(RATIO_OPEN)],
    ['APERTURE_EXPONENT', String(LENS_EXPONENT)],
    ['APERTURE_STEPS', '64'],
  ]) {
    assert.ok(
      html.includes(`const ${name} = ${value}`),
      `desktop ${name} disagrees with the canon (want ${value})`,
    );
  }
});

test('the native module is a faithful print', () => {
  const native = read('mobile/src/theme/strideSignal.ts');
  for (const [name, value] of [
    ['LENS_EXPONENT', LENS_EXPONENT],
    ['RATIO_IDLE', RATIO_IDLE],
    ['RATIO_OPEN', RATIO_OPEN],
  ]) {
    assert.ok(
      native.includes(`export const ${name} = ${value};`),
      `native ${name} disagrees with the canon (want ${value})`,
    );
  }
  // The ripple's numbers, which are what the floor depends on.
  for (const [key, value] of Object.entries(RIPPLE)) {
    assert.ok(
      native.includes(`${key}: ${value},`),
      `native RIPPLE.${key} disagrees with the canon (want ${value})`,
    );
  }
  // It must sample at the desktop's step count, so the two draw the same polygon
  // rather than two curves that merely agree in the limit.
  assert.ok(native.includes('steps: 64,'), 'native must sample at 64 steps like the desktop');
});

test('the Signal Cradle is the visible audio-reactive instrument on desktop and native', () => {
  const desktop = read('index.html');
  const native = read('mobile/src/components/StrideCradle.tsx');
  const physics = read('mobile/src/theme/strideCradle.ts');
  assert.equal((desktop.match(/<g data-cradle-ball/g) ?? []).length, 6);
  assert.match(desktop, /function updateStrideCradle\(level, seconds\)/);
  assert.match(desktop, /function stepStrideCradlePhysics\(state, elapsedSeconds, level, source = 'human'\)/);
  assert.match(desktop, /STRIDE_CRADLE_RESTITUTION = 0\.985/);
  assert.match(desktop, /voiceIslandState === 'talking' \? 'scout' : 'human'/);
  assert.match(desktop, /const tap = strideSignalTaps\.find\(candidate => candidate\.role === role\)/);
  assert.doesNotMatch(desktop, /\|\| strideSignalTaps\[0\]/);
  assert.doesNotMatch(desktop, /strideCradleSource !== source/);
  assert.equal((desktop.match(/class="office-launch__energy"/g) ?? []).length, 6);
  assert.equal((desktop.match(/class="office-launch__carrier"/g) ?? []).length, 1);
  assert.equal((desktop.match(/class="office-launch__carrier-halo"/g) ?? []).length, 1);
  assert.match(desktop, /STRIDE_CRADLE_LENGTH = 0\.52/);
  assert.match(desktop, /STRIDE_CRADLE_TRANSFER_SECONDS = 0\.14/);
  assert.match(desktop, /const contactEnergy = contactWeight \* 0\.42 \* transferStrength/);
  assert.match(desktop, /const carrierEnergy = transferStrength \* \(1 - handoff\)/);
  assert.doesNotMatch(desktop, /office-launch__core|#ffb08c/i);
  assert.doesNotMatch(desktop, /\.office-launch__bars::after/);
  assert.doesNotMatch(desktop, /class="office-launch__thread"/);
  assert.doesNotMatch(desktop, /officeCradleGradient/);
  assert.match(desktop, /analyser\.getByteTimeDomainData\(tap\.data\)/);
  assert.match(native, /BALL_COUNT = 6/);
  assert.match(native, /const spacing = radius \* 2;/);
  assert.match(native, /apertureAmplitude\(trace, listening\)/);
  assert.match(native, /useReduceMotion/);
  assert.match(native, /source\?: StrideCradleSource/);
  assert.match(native, /draw\(amplitudeRef\.current, true\)/);
  assert.match(native, /const isSourceEdge = sourceRef\.current === 'human'/);
  assert.match(native, /strideCradleContactWeights\(physics, BALL_COUNT\)/);
  assert.match(native, /ref=\{carrierRef\}/);
  assert.doesNotMatch(native, /coreRefs|ember\[300\]/);
  assert.doesNotMatch(native, /<Line|\bLine,/);
  assert.doesNotMatch(native, /RadialGradient|<Defs|\bStop,/);
  assert.match(physics, /RESTITUTION = 0\.985/);
  assert.match(physics, /PENDULUM_LENGTH_METRES = 0\.52/);
  assert.match(physics, /STRIDE_CRADLE_TRANSFER_SECONDS = 0\.14/);
  assert.match(physics, /leftVelocity = 0/);
  assert.match(physics, /rightVelocity = outgoing/);
  assert.match(physics, /rightVelocity = 0/);
  assert.match(physics, /leftVelocity = -outgoing/);
});
