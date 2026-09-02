/**
 * THE STRIDE MOTION GEOMETRY IS PINNED.
 *
 *   npm run test:brand
 *
 * The legacy aperture geometry remains inert while the five-mass Signal Cradle
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
import {
  APPEARANCES,
  CANVAS as STRIKE_CANVAS,
  CUT,
  ROW_AXIS,
  STRIDE_PUTTY,
  STRIDE_PUTTY_SOFT,
  STRIKE_LIFT,
  massesFor,
  radiusFor,
  strikeSvg,
} from './stride-strike-geometry.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const read = (path) => readFileSync(resolve(root, path), 'utf8');
const readBytes = (path) => readFileSync(resolve(root, path));
const sha256 = (value) => createHash('sha256').update(value).digest('hex');

test('The Strike vector is a faithful print of the code of record', () => {
  // Not a hand-drawn file: the source IS the module's output, so a hand edit is
  // erased by the next `npm run brand:regen` and caught here in between.
  assert.equal(read('brand/stride-strike-source.svg'), strikeSvg({ appearance: 'dark' }));
  assert.equal(read('brand/stride-strike-light.svg'), strikeSvg({ appearance: 'light' }));
  assert.equal(read('brand/stride-strike-tinted.svg'), strikeSvg({ appearance: 'tinted' }));
  assert.equal(
    sha256(readBytes('brand/stride-strike-source.svg')),
    '1e861145f455804b608d7c663ff4e9a892957be9f27094dd69f64b5cbdee2423',
  );
  assert.match(read('brand/stride-strike-source.svg'), /Stride — The Strike/);
  assert.match(read('mobile/src/components/BrandMark.tsx'), /stride-logo-(?:mark|black|white)\.png/g);
  assert.match(read('stride-site/app/components/BrandMark.tsx'), /brand-mark-\$\{tone\}\.png/);
});

test('the striking mass is lifted, and lifted by a stated amount', () => {
  // A mass level with the row it is about to hit has already arrived. The lift
  // is the entire difference between a tile that has a before and an after and
  // one that does not, so it is a number and not a nudge.
  assert.equal(STRIKE_LIFT, STRIKE_CANVAS * 0.08);
  // 81.92 / 204.8 is 0.39999999999999997 in binary floating point, not 0.4.
  // The canon number is two fifths of a radius; asserting the exact double
  // would be pinning an artefact of the representation, not the design.
  assert.ok(Math.abs(STRIKE_LIFT / radiusFor(CUT) - 0.4) < 1e-9);

  const masses = massesFor(CUT);
  const striker = masses.find((mass) => mass.role === 'active');
  const row = masses.filter((mass) => mass.role === 'neutral');
  assert.ok(striker.cy < ROW_AXIS, 'the striking mass must ride above the row');
  for (const mass of row) {
    assert.equal(mass.cy, ROW_AXIS, 'only the striker moves — the row is at rest');
  }

  // Below a full radius on purpose: at 1.0r the mass clears the row entirely
  // and the tile reads as two unrelated objects rather than one row with one of
  // them raised.
  assert.ok(STRIKE_LIFT < radiusFor(CUT), 'a lift of a full radius breaks the row');
});

test('the receiving row is in contact, and drawn so contact stays contact', () => {
  /**
   * Two things have to hold at once, and they pull against each other.
   *
   * The masses are EXACTLY tangent, because a cradle row is in contact and
   * spacing them would be a lie about the mechanism. But two tangent circles in
   * ONE asset give Icon Composer a single fused outline to rim, which pinches
   * the contact into a metallic web — the pair stops reading as two steel balls
   * touching and starts reading as one poured shape. Steel does not bleed into
   * steel.
   *
   * So: tangency is asserted on the geometry, and one-mass-per-layer is
   * asserted on the bundle. Either one alone lets the bug back in.
   */
  const row = massesFor(CUT).filter((mass) => mass.role === 'neutral');
  assert.equal(row.length, 2);
  const gap = Math.abs(row[1].cx - row[0].cx) - 2 * radiusFor(CUT);
  assert.equal(gap, 0, `the receiving masses must touch exactly (gap ${gap})`);

  const bundle = JSON.parse(read('mobile/assets/Stride.icon/icon.json'));
  const rowGroup = bundle.groups.find((group) => group.name === 'Receiving row');
  assert.ok(rowGroup, 'the bundle has no receiving row');
  assert.equal(rowGroup.layers.length, 2, 'each mass needs its own layer or the contact fuses');
  const images = rowGroup.layers.map((layer) => layer['image-name']);
  assert.equal(new Set(images).size, 2, 'the two masses must be two distinct assets');
  for (const name of images) {
    const asset = read(`mobile/assets/Stride.icon/Assets/${name}`);
    assert.equal((asset.match(/<circle/g) ?? []).length, 1, `${name} must draw exactly one mass`);
  }
});

test('the icon bundle only uses keys the renderer actually honours', () => {
  /**
   * `*-specializations` are accepted by the parser and silently ignored by the
   * renderer — proved by setting a dark specialization to magenta and watching
   * it never appear. Writing one is worse than not writing it: it reads like a
   * per-appearance override exists when the appearance is coming out of the
   * system instead. If this fails, re-verify with ictool before believing it.
   */
  const raw = read('mobile/assets/Stride.icon/icon.json');
  assert.doesNotMatch(raw, /-specializations/, 'appearance specializations do not apply — do not ship them');

  const bundle = JSON.parse(raw);
  assert.ok(bundle.groups.length <= 4, 'Icon Composer allows at most four visible groups');
  for (const group of bundle.groups) {
    // The masses are steel: momentum conserved through solid bodies. Glass
    // masses would transmit light instead of transmitting force, and the row
    // would sink toward whatever ground it is on — measured at 1.50:1 on dark.
    assert.equal(group.translucency?.enabled, false, `${group.name} must stay solid`);
    assert.equal(group.specular, true, `${group.name} must still catch the OS light`);
  }
  // No baked mask. Every platform applies its own, and one baked in shows up as
  // a dark seam inside the system's.
  for (const group of bundle.groups) {
    for (const layer of group.layers) {
      const asset = read(`mobile/assets/Stride.icon/Assets/${layer['image-name']}`);
      assert.doesNotMatch(asset, /<rect/, 'a layer must not carry a background or a mask');
    }
  }
  assert.match(read('mobile/app.config.ts'), /icon: '\.\/assets\/Stride\.icon'/);
});

test('the legacy vector wordmark remains canonical where it is still used', () => {
  const source = read('brand/stride-wordmark-source.svg');
  const outline = source.match(/ d="([^"]+)"/)?.[1];
  assert.ok(outline, 'the wordmark source has no outline');
  // "stride" is six letters with three counters — d, e, and the eye of the e's
  // terminal. Eight closed rings. A trace that loses one has dropped a counter
  // and the mark is subtly wrong in a way nobody spots until it is on a wall.
  assert.equal((outline.match(/Z/g) ?? []).length, 8, 'the wordmark must keep all eight rings');
  assert.match(source, /fill="currentColor"/, 'one asset, every colourway');

  // The native print carries its own copy because react-native cannot read an
  // SVG off disk. A divergence here is the phone drawing a different wordmark.
  const native = read('mobile/src/theme/strideWordmark.ts');
  assert.ok(native.includes(`'${outline}'`), 'the native wordmark print has drifted — regenerate it');

  // Marketing remains outside this product pass and continues to consume the
  // generated vector. Bonfire itself deliberately uses the founder-supplied
  // black/orange artwork, verified below.
  assert.match(read('stride-site/app/globals.css'), /mask: url\(\/wordmark\.svg\)/);
});

test('every ground that is declared rather than painted is the same putty', () => {
  /**
   * The grounds that live OUTSIDE CSS are the ones that rot.
   *
   * A token is read by everything and noticed immediately when it is wrong.
   * These four are copies of the ground written into config files, each read by
   * exactly one platform at one moment, and each one flashes the OLD ground for
   * a beat before the app paints the new one:
   *
   *   the PWA manifest       the installed app's splash and chrome
   *   the desktop meta tag   mobile Safari's chrome while the shell boots
   *   the Expo splash        the native launch screen
   *   the Android adaptive   launchers that use the colour, not the image
   *
   * All four were still white after the re-ground, and none of them is visible
   * in normal development. Pinned to the canon so a palette move has to update
   * them or fail here.
   */
  assert.equal(APPEARANCES.light.field, STRIDE_PUTTY);

  const manifest = JSON.parse(read('public/manifest.webmanifest'));
  // The web app's chrome follows the DESKTOP ground (softened putty), not the
  // brand tile's field — the PWA splash is the app booting, not the logo.
  assert.equal(manifest.background_color, STRIDE_PUTTY_SOFT);
  assert.equal(manifest.theme_color, STRIDE_PUTTY_SOFT);
  // …and the manifest's icons must be the generated ones, not hand-added paths.
  for (const icon of manifest.icons) {
    assert.match(icon.src, /^\/public\/(icon-192|icon-512|icon-maskable-512|app-icon)\.png$/, icon.src);
  }

  const html = read('index.html');
  assert.match(html, new RegExp(`<meta name="theme-color" content="${STRIDE_PUTTY_SOFT}">`));
  // The boot script and the theme toggle both rewrite it; all copies must agree.
  assert.equal(
    (html.match(new RegExp(`'${STRIDE_PUTTY_SOFT}'`, 'g')) ?? []).length,
    2,
    'both theme-color writers must carry the same ground',
  );

  const config = read('mobile/app.config.ts');
  assert.match(config, new RegExp(`backgroundColor: '${STRIDE_PUTTY}'`));
  assert.equal(
    (config.match(new RegExp(`backgroundColor: '${STRIDE_PUTTY}'`, 'g')) ?? []).length,
    2,
    'the native splash and the Android adaptive ground must both be the putty',
  );
});

test('the two lockup cuts are different tiles, because the ground differs', () => {
  /**
   * `black` and `white` name the GROUND, not the tile's colour: black = for
   * light grounds, white = for dark ones.
   *
   * They were byte-identical — both rendered from the ink tile, from back when
   * there was only one tile. The marketing footer asks for `tone="white"` and
   * sits on `--black` (#050505), so it was painting a near-black tile onto a
   * near-black ground with no visible edge. A caller passing a prop that does
   * nothing is worse than no prop, because the site looks like it made a choice.
   */
  for (const [dark, light] of [
    ['public/brand-mark-black.png', 'public/brand-mark-white.png'],
    ['mobile/assets/stride-logo-black.png', 'mobile/assets/stride-logo-white.png'],
  ]) {
    assert.notEqual(
      sha256(readBytes(dark)),
      sha256(readBytes(light)),
      `${dark} and ${light} are the same tile — the tone prop does nothing`,
    );
  }
  // The site still consumes them through the tone prop, so the fix has to hold
  // at the call site too.
  assert.match(read('stride-site/app/components/BrandMark.tsx'), /brand-mark-\$\{tone\}\.png/);
  assert.match(read('stride-site/app/page.tsx'), /<BrandMark size="nav" tone="white" \/>/);
});

test('no retired ink survives inside a data: URI', () => {
  /**
   * Colours encoded into `data:image/svg+xml` are invisible to a token grep and
   * to every colour sweep that has run on this file. The select chevrons kept
   * the retired cool ink at the retired alpha through an entire re-ground, and
   * only a screenshot caught them.
   *
   * The percent-encoding is why: `rgba(38, 35, 30, 0.75)` is stored as
   * `rgba%2838%2C35%2C30%2C0.75%29`, which matches nothing anyone would search
   * for. So the search happens here instead.
   */
  const html = read('index.html');
  for (const retired of ['rgba%2814%2C14%2C16', 'rgba%280%2C0%2C0', '%230E0E10', '%23F5F5F7']) {
    assert.ok(!html.includes(retired), `a data: URI still carries the retired ink ${retired}`);
  }
  // And the ones that are there must be the solved ladder values.
  assert.ok(html.includes('rgba%2838%2C35%2C30%2C0.75%29'), 'the light chevron must use the warm ink at the text-3 alpha');
});

test('the light theme is grounded on the putty, in both token copies', () => {
  /* The desktop field is putty softened ONE step (2026-08-03): a phone shows
     the ground in slivers between cards, a desktop shows it as a wall. Putty
     itself must survive in the desktop system as the well, or the two surfaces
     stop being the same material. Native keeps putty as its field. */
  assert.match(read('index.html'), new RegExp(`--paper-50: ${STRIDE_PUTTY_SOFT};`));
  assert.match(read('index.html'), new RegExp(`--paper-100: ${STRIDE_PUTTY};`));
  assert.match(read('mobile/src/theme/tokens.ts'), new RegExp(`50: '${STRIDE_PUTTY}'`));
  // The ink is a warm dark grey, not black — near-black on warm putty reads as
  // a printing error.
  assert.match(read('index.html'), /--text-1: #26231E;/);
  assert.match(read('mobile/src/theme/tokens.ts'), /text1: adaptive\('#26231E'/);
  // Orange enters light mode ambiently and nowhere else. Earned stays earned.
  assert.match(read('index.html'), /rgba\(255, 90, 25, 0\.03\), transparent 55%/);
});

test('Bonfire uses the supplied black wordmark on light and orange on dark', () => {
  /**
   * Founder override, 2026-08-04: use the supplied orange Stride logo in dark
   * mode and the supplied black Stride logo in light mode. Marketing remains
   * outside this product pass; this gate covers Bonfire web and native.
   */
  const html = read('index.html');
  assert.match(html, /--wordmark: #000000;/);
  assert.match(html, /--wordmark-image: url\(\/public\/stride-wordmark-black\.png\);/);
  assert.match(html, /--wordmark: var\(--ember-500\);/);
  assert.match(html, /--wordmark-image: url\(\/public\/stride-wordmark-orange\.png\);/);
  assert.equal(
    sha256(readBytes('public/stride-wordmark-black.png')),
    'cf4c49affc0f00293d31ad5149f2c640fa5a1103dc3d3b6a466d46a82ffffa97',
    'the founder-supplied black artwork changed',
  );
  assert.equal(
    sha256(readBytes('public/stride-wordmark-orange.png')),
    '978420efa0620630883e22fa69a90de16dd253dad1b2b25e99a4e97acaffbbab',
    'the founder-supplied orange artwork changed',
  );
  // Placements consume the theme token or themed asset, not one-off cuts.
  // AJ ratified 2026-09-02: wordmark back, no flame, no date, no status by the
  // org name — the rail's top row (#brandMark.topbar__mark) is the fourth
  // placement beside the rail label, the login mark and the bsheet eyebrow.
  assert.equal((html.match(/color: var\(--wordmark\)/g) ?? []).length, 4);
  assert.match(read('mobile/src/theme/tokens.ts'), /wordmark: adaptive\('#000000', '#FF5A19'\)/);
  assert.match(read('mobile/src/components/BrandMark.tsx'), /color = colors\.wordmark/);
  assert.doesNotMatch(read('mobile/src/components/BrandMark.tsx'), /color = colors\.ember/);
});

test('the mark and the name are one lockup, not two marks near each other', () => {
  // Stacked, a tile above a wordmark reads as two separate marks. Every surface
  // that shows both now sets them side by side.
  assert.match(read('index.html'), /<span class="login-lockup">[\s\S]{0,400}?login-wordmark wordmark/);
  assert.match(read('index.html'), /\.login-lockup \{[\s\S]{0,120}?flex/);
  assert.match(read('mobile/src/screens/LoginScreen.tsx'), /<View style=\{styles\.lockup\}>[\s\S]{0,200}?<StrideWordmark/);
  assert.match(read('mobile/src/screens/LoginScreen.tsx'), /lockup: \{\s*flexDirection: 'row'/);
  assert.match(read('stride-site/app/globals.css'), /\.nav-brand,\n\.footer-brand \{\n  display: flex;/);
});

test('the name is never set as type on a brand surface', () => {
  // The whole point of having a wordmark is that the name stops being whatever
  // the font fallback decides it is. These are the four places it used to be
  // typed; a regression here is invisible until a machine without Google Sans
  // Flex loads the page.
  assert.doesNotMatch(read('index.html'), /<h1 class="login-wordmark">Stride<\/h1>/);
  assert.doesNotMatch(read('index.html'), /<span class="bsheet__eyebrow">Stride<\/span>/);
  assert.doesNotMatch(read('stride-site/app/page.tsx'), />STRIDE</);
  assert.doesNotMatch(read('mobile/src/screens/LoginScreen.tsx'), /\{product\.wordmark\}/);

  // …and the accessible name survives the swap. A masked span with no label is
  // a heading screen readers announce as nothing at all.
  assert.match(read('index.html'), /class="login-wordmark wordmark" role="img" aria-label="Stride"/);
  assert.match(read('mobile/src/components/BrandMark.tsx'), /accessibilityLabel="Stride"/);
  assert.match(read('stride-site/app/components/Wordmark.tsx'), /aria-label="Stride"/);
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
  assert.equal((desktop.match(/<g data-cradle-ball/g) ?? []).length, 5);
  assert.match(desktop, /function updateStrideCradle\(level, seconds\)/);
  assert.match(desktop, /function stepStrideCradlePhysics\(state, elapsedSeconds, level, source = 'human'\)/);
  assert.match(desktop, /STRIDE_CRADLE_RESTITUTION = 0\.985/);
  assert.match(desktop, /voiceIslandState === 'talking' \? 'scout' : 'human'/);
  assert.match(desktop, /const tap = strideSignalTaps\.find\(candidate => candidate\.role === role\)/);
  assert.doesNotMatch(desktop, /\|\| strideSignalTaps\[0\]/);
  assert.doesNotMatch(desktop, /strideCradleSource !== source/);
  assert.equal((desktop.match(/class="office-launch__energy"/g) ?? []).length, 5);
  assert.doesNotMatch(desktop, /office-launch__carrier/);
  assert.match(desktop, /STRIDE_CRADLE_LENGTH = 0\.52/);
  assert.match(desktop, /STRIDE_CRADLE_TRANSFER_SECONDS = 0\.26/);
  assert.match(desktop, /const contactEnergy = contactWeight \* 0\.92 \* transferStrength/);
  assert.doesNotMatch(desktop, /office-launch__core|#ffb08c/i);
  assert.doesNotMatch(desktop, /\.office-launch__bars::after/);
  assert.doesNotMatch(desktop, /class="office-launch__thread"/);
  assert.doesNotMatch(desktop, /officeCradleGradient/);
  assert.match(desktop, /analyser\.getByteTimeDomainData\(tap\.data\)/);
  assert.match(native, /BALL_COUNT = 5/);
  assert.match(native, /const spacing = radius \* 2;/);
  assert.match(native, /apertureAmplitude\(trace, listening\)/);
  assert.match(native, /useReduceMotion/);
  assert.match(native, /source\?: StrideCradleSource/);
  assert.match(native, /draw\(amplitudeRef\.current, true\)/);
  assert.match(native, /const isSourceEdge = sourceRef\.current === 'human'/);
  assert.match(native, /strideCradleContactWeights\(physics, BALL_COUNT\)/);
  assert.doesNotMatch(native, /carrierRef|carrierHaloRef/);
  assert.doesNotMatch(native, /coreRefs|ember\[300\]/);
  assert.doesNotMatch(native, /<Line|\bLine,/);
  assert.doesNotMatch(native, /RadialGradient|<Defs|\bStop,/);
  assert.match(physics, /RESTITUTION = 0\.985/);
  assert.match(physics, /PENDULUM_LENGTH_METRES = 0\.52/);
  assert.match(physics, /STRIDE_CRADLE_TRANSFER_SECONDS = 0\.26/);
  assert.match(physics, /leftVelocity = 0/);
  assert.match(physics, /rightVelocity = outgoing/);
  assert.match(physics, /rightVelocity = 0/);
  assert.match(physics, /leftVelocity = -outgoing/);
});
