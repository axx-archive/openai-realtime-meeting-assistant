import test from 'node:test';
import assert from 'node:assert/strict';
import { strideTokens, strideLight, strideDark } from '../theme/generatedTokens';
type RGB = [number, number, number];
function rgb(hex: string, backdrop?: RGB): RGB {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16)) as RGB;
  if (hex.length !== 9) return c;
  assert.ok(backdrop, 'alpha requires its actual backdrop');
  const a = parseInt(hex.slice(7, 9), 16) / 255;
  return c.map((v, i) => v * a + backdrop![i] * (1 - a)) as RGB;
}
function luminance(rgb: RGB) {
  const c = rgb.map(x => { const v = x / 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
  return c[0] * 0.2126 + c[1] * 0.7152 + c[2] * 0.0722;
}
function ratio(a: RGB, b: RGB) { const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); }
function readable(fg: string, bg: string, label: string, min = 4.5) {
  const actual = ratio(rgb(fg, rgb(bg)), rgb(bg));
  assert.ok(actual >= min, `${label}: ${actual.toFixed(2)}:1 < ${min}:1`);
}
for (const [name, p] of Object.entries({ light: strideLight, dark: strideDark })) {
  test(`${name}: generated text and control roles contrast with every content surface`, () => {
    for (const surface of ['canvas', 'surface', 'surfaceInset', 'selection'] as const) {
      for (const text of ['text', 'textSecondary', 'textMuted', 'textDisabled', 'brandText'] as const) readable(p[text], p[surface], `${name}.${text}/${surface}`);
      readable(p.borderControl, p[surface], `${name}.borderControl/${surface}`, 3);
    }
  });
  test(`${name}: statuses remain readable and separate from action`, () => {
    for (const state of ['success', 'warning', 'danger', 'info', 'live'] as const) {
      readable(p[state], p[`${state}Surface`], `${name}.${state}/surface`);
      readable(p[state], p.canvas, `${name}.${state}/canvas`);
    }
    for (const state of ['success', 'warning', 'danger'] as const) assert.notEqual(p[state], p.action);
  });
  test(`${name}: action variants and actual glass composites remain readable`, () => {
    for (const fill of ['action', 'actionHover', 'actionPressed'] as const) {
      readable(p.onAction, p[fill], `${name}.onAction/${fill}`);
      readable(p.onActionBrand, p[fill], `${name}.onActionBrand/${fill}`);
    }
    for (const surface of ['canvas', 'surfaceInset'] as const) for (const text of ['text', 'textSecondary', 'textMuted'] as const) {
      assert.ok(ratio(rgb(p[text]), rgb(p.glassPanel, rgb(p[surface]))) >= 4.5, `${name}.${text}/glass/${surface}`);
    }
  });
}
test('fixed brand and call surfaces keep distinct contrasting foregrounds', () => {
  const c = strideTokens.color.constant;
  readable(c.onBrand, c.brandCobalt, 'onBrand'); readable(c.onLeave, c.leaveFill, 'onLeave');
  for (const fill of [c.stage, c.stageChrome]) {
    readable(c.stageText, fill, 'call text'); readable(c.stageTextSecondary, fill, 'call secondary'); readable(c.stageControlBorder, fill, 'call edge', 3);
  }
});
