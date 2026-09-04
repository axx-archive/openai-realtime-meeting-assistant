#!/usr/bin/env node
// One source for web, native and the independently released marketing site.
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const source = await readFile(resolve(root, 'design/stride.tokens.json'), 'utf8');
const tokens = JSON.parse(source);
const digest = createHash('sha256').update(source).digest('hex');
const check = process.argv.includes('--check');
const kebab = (key) => key.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`);

function requireCondition(condition, message) {
  if (!condition) throw new Error(message);
}
function luminance(hex) {
  requireCondition(/^#[0-9a-f]{6}$/i.test(hex), `Contrast requires opaque color: ${hex}`);
  const rgb = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((v) => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
  return rgb[0] * 0.2126 + rgb[1] * 0.7152 + rgb[2] * 0.0722;
}
function contrast(fg, bg, minimum, label) {
  const [high, low] = [luminance(fg), luminance(bg)].sort((a, b) => b - a);
  const ratio = (high + 0.05) / (low + 0.05);
  requireCondition(ratio >= minimum, `${label}: ${ratio.toFixed(2)}:1 is below ${minimum}:1`);
}
requireCondition(tokens.schemaVersion === 1, 'Unsupported token schema');
requireCondition(JSON.stringify(Object.keys(tokens.color.light).sort()) === JSON.stringify(Object.keys(tokens.color.dark).sort()), 'Appearance roles must match');
for (const [theme, palette] of Object.entries(tokens.color)) {
  for (const [role, value] of Object.entries(palette)) {
    requireCondition(/^#[0-9a-f]{6}([0-9a-f]{2})?$/i.test(value), `${theme}.${role}: use sRGB hex`);
  }
  if (theme === 'constant') continue;
  for (const surface of ['canvas', 'surface', 'surfaceInset', 'selection']) {
    for (const text of ['text', 'textSecondary', 'textMuted']) contrast(palette[text], palette[surface], 4.5, `${theme}.${text}/${surface}`);
    contrast(palette.borderControl, palette[surface], 3, `${theme}.borderControl/${surface}`);
    contrast(palette.focus, palette[surface], 3, `${theme}.focus/${surface}`);
  }
  for (const action of ['action', 'actionHover', 'actionPressed']) contrast(palette.onAction, palette[action], 4.5, `${theme}.onAction/${action}`);
  for (const state of ['success', 'warning', 'danger', 'info', 'live']) contrast(palette[state], palette[`${state}Surface`], 4.5, `${theme}.${state}`);
}
const constant = tokens.color.constant;
contrast(constant.onBrand, constant.brandCobalt, 4.5, 'brand');
contrast(constant.onLeave, constant.leaveFill, 4.5, 'leave');
for (const surface of ['stage', 'stageChrome']) {
  contrast(constant.stageText, constant[surface], 4.5, `stageText/${surface}`);
  contrast(constant.stageTextSecondary, constant[surface], 4.5, `stageTextSecondary/${surface}`);
  contrast(constant.stageControlBorder, constant[surface], 3, `stageControlBorder/${surface}`);
}
requireCondition(tokens.size.hitMin >= 44 && tokens.size.controlCompact >= 44, 'Hit targets must be at least 44 logical units');
for (const role of Object.values(tokens.typography.nativeRole)) {
  requireCondition(tokens.typography.nativeFonts[role.font] && role.lineHeight >= role.size, 'Invalid native font role');
}

const entries = (object, prefix, suffix = '') => Object.entries(object).map(([key, value]) => `  --stride-${prefix}-${kebab(key)}: ${value}${suffix};`).join('\n');
const palette = (theme) => entries(tokens.color[theme], 'color');
const shared = [
  entries(constant, 'constant'), entries(tokens.space, 'space', 'px'),
  entries(tokens.radius, 'radius', 'px'), entries(tokens.size, 'size', 'px'),
  ...Object.entries(tokens.typography.role).map(([name, role]) => Object.entries(role).map(([key, value]) => `  --stride-type-${kebab(name)}-${kebab(key)}: ${value}${key.startsWith('size') ? 'px' : key === 'tracking' ? 'em' : ''};`).join('\n')),
  ...['feedback', 'transition', 'reveal'].map((key) => `  --stride-motion-${key}: ${tokens.motion[key]}ms;`),
  `  --stride-motion-ease: cubic-bezier(${tokens.motion.ease.join(', ')});`,
  `  --stride-motion-press-scale: ${tokens.motion.pressScale};`,
  entries(tokens.layout, 'layout', 'px').replace(`${tokens.layout.readingMaxCh}px`, `${tokens.layout.readingMaxCh}ch`),
].join('\n');
const banner = `Generated from design/stride.tokens.json · schema ${tokens.schemaVersion} · sha256 ${digest}. DO NOT EDIT.`;
const css = `/* ${banner} */\n:root {\n${shared}\n}\n:root, [data-theme="light"] {\n${palette('light')}\n}\n[data-theme="dark"] {\n${palette('dark')}\n}\n@media (prefers-color-scheme: dark) {\n  :root:not([data-theme]) {\n${palette('dark').split('\n').map((line) => `  ${line}`).join('\n')}\n  }\n}\n`;
const native = `// ${banner}\nexport const strideTokenSourceSHA = '${digest}';\nexport const strideTokens = ${JSON.stringify(tokens, null, 2)} as const;\nexport const strideLight = strideTokens.color.light;\nexport const strideDark = strideTokens.color.dark;\n`;
const outputs = new Map([
  ['public/design/stride-tokens.css', css],
  ['mobile/src/theme/generatedTokens.ts', native],
  ['design/exports/stride-tokens.v1.css', css],
]);
for (const [path, content] of outputs) {
  if (check) {
    requireCondition(await readFile(resolve(root, path), 'utf8').catch(() => '') === content, `${path} is stale; run node scripts/generate-stride-design-tokens.mjs`);
  } else {
    await mkdir(dirname(resolve(root, path)), { recursive: true });
    await writeFile(resolve(root, path), content);
  }
}
console.log(`STRIDE design schema ${tokens.schemaVersion}: contrast validated; ${outputs.size} outputs ${check ? 'verified' : 'generated'}; ${digest}`);
