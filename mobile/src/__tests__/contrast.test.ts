import test from 'node:test';
import assert from 'node:assert/strict';

/**
 * Contrast pins — the Wave D design audit.
 *
 * These assert on the token VALUES, which the existing suite never did: it
 * pinned where tokens are USED, so a palette change could quietly drop text
 * below the readability floor with every test still green. That is exactly how
 * the ember failure below survived until it was measured.
 *
 * Compositing is the trap worth naming: `emberSoft` is a 12% alpha, so
 * measuring the literal rgba against the text colour reports a ratio nobody
 * ever sees. Alpha colours must be flattened onto the surface actually behind
 * them before the ratio means anything.
 */

type RGB = [number, number, number];

function hexToRgb(hex: string): RGB {
  const value = hex.replace('#', '');
  return [
    parseInt(value.slice(0, 2), 16),
    parseInt(value.slice(2, 4), 16),
    parseInt(value.slice(4, 6), 16),
  ];
}

/** Flattens a translucent colour onto its backdrop. */
function composite(fg: RGB, alpha: number, bg: RGB): RGB {
  return [
    Math.round(fg[0] * alpha + bg[0] * (1 - alpha)),
    Math.round(fg[1] * alpha + bg[1] * (1 - alpha)),
    Math.round(fg[2] * alpha + bg[2] * (1 - alpha)),
  ];
}

function luminance([r, g, b]: RGB): number {
  const channel = (raw: number) => {
    const v = raw / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

function contrast(a: RGB, b: RGB): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

// Values mirrored from theme/tokens.ts. tokens.ts imports react-native, which
// node:test cannot load, so the pins carry their own copies — a divergence here
// is itself a finding worth failing on.
// Light mode is grounded on WARM PUTTY. The ground's luminance fell from 0.90
// to 0.57 when it moved off white paper, so every value below was re-solved —
// none of them survived the move unchanged.
const PAPER_50 = hexToRgb('#CFC5B7');   // the ground
const PAPER_0 = hexToRgb('#EDE8DF');    // panels — the ground lifted
const PAPER_100 = hexToRgb('#C2B7A7');  // wells — the WORST CASE for text
const INK_950 = hexToRgb('#09090B');
const INK_850 = hexToRgb('#141418');
const TEXT1_LIGHT = hexToRgb('#26231E'); // warm dark grey, not black
const TEXT1_DARK = hexToRgb('#F7F7F9');
const ON_ACCENT_LIGHT = hexToRgb('#FFFDF8');
// Stride Orange — the same value the Stride Signal is drawn in.
const EMBER = hexToRgb('#FF5A19');
const EMBER_TEXT_LIGHT = hexToRgb('#86290F');
const TEXT2_ALPHA = 0.87;
const TEXT3_ALPHA = 0.75;
const EMBER_SOFT_ALPHA = 0.14;
// Founder-supplied cuts: black on light; Stride Orange on dark.
const WORDMARK_LIGHT = hexToRgb('#000000');
const WORDMARK_DARK = hexToRgb('#FF5A19');

const AA_BODY = 4.5;

test('body text clears AA on the app background in both themes', () => {
  assert.ok(contrast(TEXT1_LIGHT, PAPER_50) >= AA_BODY);
  assert.ok(contrast(TEXT1_DARK, INK_950) >= AA_BODY);
});

test('secondary text clears AA on the app background in both themes', () => {
  const light = composite(TEXT1_LIGHT, TEXT2_ALPHA, PAPER_50);
  const dark = composite(TEXT1_DARK, 0.66, INK_950);
  assert.ok(contrast(light, PAPER_50) >= AA_BODY, 'text2 light');
  assert.ok(contrast(dark, INK_950) >= AA_BODY, 'text2 dark');
});

/**
 * The ground is not the worst case any more.
 *
 * On white paper the app background and the deepest well were close enough that
 * measuring against the background was near enough. On putty the well is a full
 * step darker, and text3 clears the floor there by nine hundredths — so the
 * whole ladder has to be measured against the WELL or the tightest number in
 * the system goes unmeasured.
 */
test('the whole light ladder clears AA on the worst-case surface', () => {
  const rungs: Array<[string, number]> = [
    ['text1', 1],
    ['text2', TEXT2_ALPHA],
    ['text3', TEXT3_ALPHA],
  ];
  for (const [name, alpha] of rungs) {
    const ratio = contrast(composite(TEXT1_LIGHT, alpha, PAPER_100), PAPER_100);
    assert.ok(ratio >= AA_BODY, `${name} on the well was ${ratio.toFixed(2)}:1`);
  }
});

test('the putty ramp keeps its order: panels lift, wells sink', () => {
  // The relationship, not the values — this is what let every screen survive
  // the re-ground without being re-thought.
  assert.ok(luminance(PAPER_0) > luminance(PAPER_50), 'panels must lift off the ground');
  assert.ok(luminance(PAPER_50) > luminance(PAPER_100), 'wells must sink under it');
});

// The failure the Wave D audit found, made worse by the re-ground: Stride
// Orange is tuned to glow against dark. It was 2.87:1 on white paper and is
// 1.83:1 on putty, and the old darkened cut (#B83A18) fell to 3.37:1 and stopped
// clearing AA too. Both had to move.
test('ember TEXT clears AA on the light background', () => {
  const ratio = contrast(EMBER_TEXT_LIGHT, PAPER_50);
  assert.ok(ratio >= AA_BODY, `emberText on bgApp light was ${ratio.toFixed(2)}:1`);
  // And on the well, which is the tight one now.
  const onWell = contrast(EMBER_TEXT_LIGHT, PAPER_100);
  assert.ok(onWell >= AA_BODY, `emberText on the well was ${onWell.toFixed(2)}:1`);
  // The raw brand orange must NOT be used as light-theme text.
  assert.ok(contrast(EMBER, PAPER_50) < AA_BODY, 'ember unexpectedly passes — retune emberText');
  // Nor may the retired cut come back.
  assert.ok(contrast(hexToRgb('#B83A18'), PAPER_50) < AA_BODY, '#B83A18 no longer clears the putty ground');
});

test('ember TEXT clears AA on an emberSoft chip in both themes', () => {
  const softLight = composite(EMBER, EMBER_SOFT_ALPHA, PAPER_50);
  const softDark = composite(EMBER, EMBER_SOFT_ALPHA, INK_950);
  assert.ok(
    contrast(EMBER_TEXT_LIGHT, softLight) >= AA_BODY,
    `emberText on emberSoft light was ${contrast(EMBER_TEXT_LIGHT, softLight).toFixed(2)}:1`,
  );
  // Dark keeps the brand orange, which already passes — that is where it belongs.
  assert.ok(contrast(EMBER, softDark) >= AA_BODY);
});

test('text on raised surfaces clears AA in both themes', () => {
  assert.ok(contrast(TEXT1_LIGHT, PAPER_0) >= AA_BODY);
  assert.ok(contrast(TEXT1_DARK, INK_850) >= AA_BODY);
  assert.ok(contrast(composite(TEXT1_LIGHT, TEXT3_ALPHA, PAPER_0), PAPER_0) >= AA_BODY);
  assert.ok(contrast(composite(TEXT1_DARK, 0.66, INK_850), INK_850) >= AA_BODY);
});

test('the accent button clears AA', () => {
  assert.ok(contrast(ON_ACCENT_LIGHT, TEXT1_LIGHT) >= AA_BODY);
});

/** Founder override, 2026-08-04: black on light; Stride Orange on dark. */
test('the wordmark follows the supplied black and orange theme cuts', () => {
  assert.deepEqual(WORDMARK_LIGHT, [0, 0, 0], 'light mode must use the black wordmark');
  assert.deepEqual(WORDMARK_DARK, EMBER, 'dark mode must use Stride Orange');
  assert.ok(contrast(WORDMARK_LIGHT, PAPER_50) >= 3, 'the black wordmark must read on putty');
  assert.ok(contrast(WORDMARK_DARK, INK_950) >= 3, 'the orange wordmark must read on true black');
});
