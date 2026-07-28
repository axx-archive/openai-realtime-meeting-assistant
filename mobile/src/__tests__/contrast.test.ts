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
const PAPER_50 = hexToRgb('#F5F5F7');
const INK_950 = hexToRgb('#09090B');
const WHITE = hexToRgb('#FFFFFF');
const INK_850 = hexToRgb('#141418');
const TEXT1_LIGHT = hexToRgb('#0E0E10');
const TEXT1_DARK = hexToRgb('#F7F7F9');
const EMBER = hexToRgb('#FF6B4A');
const EMBER_TEXT_LIGHT = hexToRgb('#B83A18');

const AA_BODY = 4.5;

test('body text clears AA on the app background in both themes', () => {
  assert.ok(contrast(TEXT1_LIGHT, PAPER_50) >= AA_BODY);
  assert.ok(contrast(TEXT1_DARK, INK_950) >= AA_BODY);
});

test('secondary text clears AA on the app background in both themes', () => {
  const light = composite(TEXT1_LIGHT, 0.6, PAPER_50);
  const dark = composite(TEXT1_DARK, 0.66, INK_950);
  assert.ok(contrast(light, PAPER_50) >= AA_BODY, 'text2 light');
  assert.ok(contrast(dark, INK_950) >= AA_BODY, 'text2 dark');
});

// The failure this audit found. #FF6B4A is tuned to glow against dark; on
// #F5F5F7 it measures 2.59:1, and on an emberSoft chip 2.30:1 — every ember
// label in light mode was effectively decorative.
test('ember TEXT clears AA on the light background', () => {
  const ratio = contrast(EMBER_TEXT_LIGHT, PAPER_50);
  assert.ok(ratio >= AA_BODY, `emberText on bgApp light was ${ratio.toFixed(2)}:1`);
  // And the raw brand coral must NOT be used as light-theme text.
  assert.ok(contrast(EMBER, PAPER_50) < AA_BODY, 'ember unexpectedly passes — retune emberText');
});

test('ember TEXT clears AA on an emberSoft chip in both themes', () => {
  const softLight = composite(EMBER, 0.12, PAPER_50);
  const softDark = composite(EMBER, 0.12, INK_950);
  assert.ok(
    contrast(EMBER_TEXT_LIGHT, softLight) >= AA_BODY,
    `emberText on emberSoft light was ${contrast(EMBER_TEXT_LIGHT, softLight).toFixed(2)}:1`,
  );
  // Dark keeps the coral, which already passes — that is where it belongs.
  assert.ok(contrast(EMBER, softDark) >= AA_BODY);
});

test('text on raised surfaces clears AA in both themes', () => {
  assert.ok(contrast(TEXT1_LIGHT, WHITE) >= AA_BODY);
  assert.ok(contrast(TEXT1_DARK, INK_850) >= AA_BODY);
  assert.ok(contrast(composite(TEXT1_LIGHT, 0.6, WHITE), WHITE) >= AA_BODY);
  assert.ok(contrast(composite(TEXT1_DARK, 0.66, INK_850), INK_850) >= AA_BODY);
});

test('the accent button clears AA', () => {
  assert.ok(contrast(WHITE, TEXT1_LIGHT) >= AA_BODY);
});
