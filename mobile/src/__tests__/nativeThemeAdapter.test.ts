import test from 'node:test';
import assert from 'node:assert/strict';
import { strideTokens, strideLight, strideDark } from '../theme/generatedTokens';
import { navigationThemeColors } from '../navigation/navigationTheme';
import { registerTestStubModules } from './support/registerTestStubModules';

test('native compatibility adapters read generated colors, geometry and typography', async () => {
  registerTestStubModules('theme-adapter-stub:', {'theme-adapter-stub:react-native': `export const Platform={OS:'ios'}; export const DynamicColorIOS=value=>({dynamic:value});`});
  const { colors, paper, ink, ember, radius, space, fonts, hitMin, type, resolveThemeTint } = await import('../theme/tokens');
  const pairs = { bg: 'canvas', bgApp: 'canvas', surface1: 'surface', surface2: 'surface', surface3: 'surfaceInset', text1: 'text', text2: 'textSecondary', text3: 'textMuted', accent: 'action', onAccent: 'onAction', danger: 'danger', warn: 'warning', success: 'success', live: 'live', emberText: 'brandText', text: 'text', textSecondary: 'textSecondary', textTertiary: 'textMuted', border: 'border' } as const;
  for (const [alias, role] of Object.entries(pairs)) assert.deepEqual(colors[alias as keyof typeof pairs], {dynamic: {light: strideLight[role], dark: strideDark[role]}}, alias);
  assert.equal(resolveThemeTint(colors.danger, false), strideLight.danger);
  assert.equal(resolveThemeTint(colors.danger, true), strideDark.danger);
  assert.equal(resolveThemeTint(colors.emberSoft, false), strideLight.selection);
  assert.equal(resolveThemeTint(colors.emberSoft, true), strideDark.selection);
  assert.equal(resolveThemeTint(colors.ember, true), strideTokens.color.constant.brandCobalt);
  assert.equal(resolveThemeTint(undefined, true), undefined);
  assert.equal(resolveThemeTint({nativeUnknown: true} as never, true), undefined, 'unknown native colors must not stringify or freeze');
  assert.equal(colors.ember, strideTokens.color.constant.brandCobalt); assert.equal(colors.onEmber, strideTokens.color.constant.onBrand);
  assert.equal(ember[500], colors.ember); assert.equal(paper[50], strideLight.canvas); assert.equal(ink[950], strideTokens.color.constant.stage); assert.equal(ink[850], strideTokens.color.constant.stageChrome);
  assert.equal(radius.sm, strideTokens.radius.control); assert.equal(radius.xl, strideTokens.radius.sheet); assert.equal(space, strideTokens.space); assert.equal(hitMin, strideTokens.size.hitMin); assert.equal(fonts, strideTokens.typography.nativeFonts);
  for (const role of Object.keys(type) as Array<keyof typeof type>) {
    const e = strideTokens.typography.nativeRole[role];
    assert.deepEqual(type[role], {fontFamily: fonts[e.font], fontSize: e.size, fontWeight: e.weight, letterSpacing: e.tracking, lineHeight: e.lineHeight});
  }
});
test('navigation resolves theme strings from the same generated palette', () => {
  for (const dark of [false, true]) { const p = dark ? strideDark : strideLight;
    assert.deepEqual(navigationThemeColors(dark), {background: p.canvas, card: p.surface, text: p.text, border: p.border, primary: p.action, notification: p.action});
  }
});

test('glass resolves adaptive SDK tints on theme changes and preserves unknown native colors', async () => {
  const state = globalThis as typeof globalThis & { __glassTheme?: string; __glassReduced?: boolean; IS_REACT_ACT_ENVIRONMENT?: boolean };
  state.__glassTheme = 'light'; state.__glassReduced = false; state.IS_REACT_ACT_ENVIRONMENT = true;
  registerTestStubModules('theme-glass-stub:', {
    'theme-glass-stub:react-native': `export const View='View'; export const StyleSheet={create:v=>v,absoluteFill:{},hairlineWidth:1}; export const useColorScheme=()=>globalThis.__glassTheme;`,
    'theme-glass-stub:expo-glass-effect': `export const GlassView='GlassView'; export const isLiquidGlassAvailable=()=>true;`,
    'theme-glass-stub:expo-blur': `export const BlurView='BlurView';`,
    'theme-glass-stub:./motion': `export const useReduceTransparency=()=>globalThis.__glassReduced;`,
  });
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { colors } = await import('../theme/tokens');
  const { Glass } = await import('../theme/glass');
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(React.createElement(Glass, {tint: colors.danger})); });
  assert.equal(renderer!.root.findByType('GlassView' as never).props.tintColor, strideLight.danger);
  state.__glassTheme = 'dark';
  await act(async () => { renderer!.update(React.createElement(Glass, {tint: colors.danger})); });
  assert.equal(renderer!.root.findByType('GlassView' as never).props.tintColor, strideDark.danger);
  const nativeColor = { semantic: ['labelColor'] };
  await act(async () => { renderer!.update(React.createElement(Glass, {tint: nativeColor as never})); });
  assert.equal(renderer!.root.findByType('GlassView' as never).props.tintColor, undefined);
  assert.ok(renderer!.root.findAllByType('View' as never).some(view => [view.props.style].flat(5).some(style => style?.backgroundColor === nativeColor)), 'unknown opaque color stays on RN instead of entering a string-only SDK prop');
  state.__glassReduced = true;
  await act(async () => { renderer!.update(React.createElement(Glass, {tint: colors.danger})); });
  assert.equal(renderer!.root.findAllByType('GlassView' as never).length, 0);
  await act(async () => renderer!.unmount());
});
