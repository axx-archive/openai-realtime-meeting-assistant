import { DynamicColorIOS, Platform, type ColorValue, type TextStyle } from 'react-native';
import { strideTokens, strideLight, strideDark } from './generatedTokens';

/** Compatibility names for existing screens. Values come from design/stride.tokens.json. */
const adaptivePairs = new Map<ColorValue, { light: string; dark: string }>();
const adaptive = (light: string, dark: string): ColorValue => {
  const color = Platform.OS === 'ios' ? DynamicColorIOS({ light, dark }) : light;
  adaptivePairs.set(color, { light, dark });
  return color;
};
/** String-only SDK props resolve registered tokens; unknown native colors remain opaque. */
export function resolveThemeTint(color: ColorValue | undefined, dark: boolean): string | undefined {
  if (color === undefined) return undefined;
  const pair = adaptivePairs.get(color);
  if (pair) return dark ? pair.dark : pair.light;
  return typeof color === 'string' ? color : undefined;
}
const semantic = (key: keyof typeof strideLight & keyof typeof strideDark): ColorValue =>
  adaptive(strideLight[key], strideDark[key]);
const constant = strideTokens.color.constant;

/** Legacy ramps retain their API, with fixed call-stage colors for existing call surfaces. */
export const paper = { 0: strideLight.surface, 50: strideLight.canvas, 100: strideLight.surfaceInset, 200: strideLight.border } as const;
export const ink = {
  950: constant.stage,
  900: constant.stage,
  850: constant.stageChrome,
  800: constant.stageChrome,
  700: strideDark.border,
  600: strideDark.borderControl,
  500: strideDark.textMuted,
  400: strideDark.textSecondary,
  300: constant.stageTextSecondary,
} as const;
export const signal = { 400: strideDark.success, 500: constant.speaking, 600: strideLight.success } as const;
/** Temporary activity/action aliases; these do not define success or capture state. */
export const ember = { 300: strideDark.actionHover, 400: strideDark.action, 500: constant.brandCobalt, 600: strideLight.actionPressed } as const;

export const colors = {
  bg: semantic('canvas'), bgApp: semantic('canvas'),
  surface1: semantic('surface'), surface2: semantic('surface'), surface3: semantic('surfaceInset'),
  text1: semantic('text'), text2: semantic('textSecondary'), text3: semantic('textMuted'), textDisabled: semantic('textDisabled'),
  line1: semantic('border'), line2: semantic('borderControl'),
  accent: semantic('action'), accentHover: semantic('actionHover'), accentPress: semantic('actionPressed'),
  accentSoft: semantic('selection'), onAccent: semantic('onAction'),
  live: semantic('live'), liveSoft: semantic('liveSurface'),
  success: semantic('success'), danger: semantic('danger'), dangerSoft: semantic('dangerSurface'),
  warn: semantic('warning'), warnSoft: semantic('warningSurface'),
  info: semantic('info'), infoSoft: semantic('infoSurface'),
  ember: constant.brandCobalt, emberSoft: semantic('selection'), onEmber: constant.onBrand,
  emberText: semantic('brandText'), wordmark: semantic('wordmark'), onAccentEmber: semantic('onActionBrand'),
  glassBorder: semantic('glassBorder'), glassPanel: semantic('glassPanel'), scrim: semantic('scrim'),
  bgElevated: semantic('surface'), bgMuted: semantic('surfaceInset'),
  text: semantic('text'), textSecondary: semantic('textSecondary'), textTertiary: semantic('textMuted'),
  border: semantic('border'), tabInactive: semantic('textMuted'),
} as const;

export const radius = {
  sm: strideTokens.radius.control,
  md: strideTokens.radius.surface,
  lg: strideTokens.radius.surface,
  xl: strideTokens.radius.sheet,
  xxl: strideTokens.radius.sheet,
  full: strideTokens.radius.pill,
} as const;
export const space = strideTokens.space;
export const hitMin = strideTokens.size.hitMin;

/** Native elevation approximates the shared surface hierarchy; all colors are semantic. */
export const shadow = {
  1: { shadowColor: semantic('shadowColor'), shadowOpacity: 0.1, shadowRadius: 2, shadowOffset: { width: 0, height: 1 } },
  2: { shadowColor: semantic('shadowColor'), shadowOpacity: 0.12, shadowRadius: 12, shadowOffset: { width: 0, height: 8 } },
  glass: { shadowColor: semantic('shadowColor'), shadowOpacity: 0.1, shadowRadius: 16, shadowOffset: { width: 0, height: 8 } },
  mark: { shadowColor: constant.videoLetterbox, shadowOpacity: 0.28, shadowRadius: 16, shadowOffset: { width: 0, height: 12 } },
} as const;

export const fonts = strideTokens.typography.nativeFonts;
const nativeRole = strideTokens.typography.nativeRole;
function textRole(role: keyof typeof nativeRole) {
  const value = nativeRole[role];
  return { fontFamily: fonts[value.font], fontSize: value.size, fontWeight: String(value.weight) as TextStyle['fontWeight'], letterSpacing: value.tracking, lineHeight: value.lineHeight };
}
export const type = {
  title1: textRole('title1'),
  title2: textRole('title2'),
  wordmark: textRole('wordmark'),
  headline: textRole('headline'),
  body: textRole('body'),
  bodyMedium: textRole('bodyMedium'),
  bodySm: textRole('bodySm'),
  caption: textRole('caption'),
  captionMedium: textRole('captionMedium'),
  label: textRole('label'),
  labelLg: textRole('labelLg'),
  button: textRole('button'),
} as const;

export const product = {
  name: 'Stride', wordmark: 'Stride', loginCta: 'Enter your office',
  description: 'conversation becomes memory, approved work, and verified results.',
} as const;
