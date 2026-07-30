import { DynamicColorIOS, Platform, type ColorValue } from 'react-native';

/**
 * Glass & Ink tokens — mirrored from the live Stride `index.html` :root.
 * Source of truth: deployed web design (see `.superdesign/design-system.md`).
 * Do not invent alternate palettes or revive the retired warm-dark Bonfire look.
 */

export const paper = {
  0: '#FFFFFF',
  50: '#F5F5F7',
  100: '#EDEDF0',
  200: '#E2E2E7',
} as const;

export const ink = {
  950: '#09090B',
  900: '#101013',
  850: '#141418',
  800: '#1B1B21',
  700: '#26262E',
  600: '#34343E',
  500: '#4D4D59',
  400: '#6E6E7A',
  300: '#9A9AA4',
} as const;

export const signal = {
  400: '#5CE08A',
  500: '#30D158',
  600: '#23A847',
} as const;

/**
 * ember — the one warm ignition accent.
 *
 * `500` IS Stride Orange: the same #FF5A19 the Stride Signal is drawn in (see
 * `scripts/stride-signal-geometry.mjs`). It used to be a slightly pinker coral
 * (#FF6B4A), which meant the logo and every accent in the product were two
 * different oranges sitting next to each other. One brand, one orange. The ramp
 * is that hue held at 100% saturation and stepped on lightness (74/65/55/45).
 */
export const ember = {
  300: '#FFA07A',
  400: '#FF7F4D',
  500: '#FF5A19',
  600: '#E64100',
} as const;

const adaptive = (light: string, dark: string): ColorValue =>
  Platform.OS === 'ios' ? DynamicColorIOS({ light, dark }) : light;

/** Semantic aliases — light theme (default). */
export const colors = {
  bg: adaptive(paper[50], ink[950]),
  bgApp: adaptive(paper[50], ink[950]),
  surface1: adaptive(paper[0], ink[850]),
  surface2: adaptive(paper[0], ink[900]),
  surface3: adaptive(paper[100], ink[800]),

  text1: adaptive('#0E0E10', '#F7F7F9'),
  text2: adaptive('rgba(14, 14, 16, 0.60)', 'rgba(247, 247, 249, 0.66)'),
  text3: adaptive('rgba(14, 14, 16, 0.38)', 'rgba(247, 247, 249, 0.42)'),
  textDisabled: adaptive('rgba(14, 14, 16, 0.24)', 'rgba(247, 247, 249, 0.24)'),

  line1: adaptive('rgba(14, 14, 16, 0.08)', 'rgba(255, 255, 255, 0.09)'),
  line2: adaptive('rgba(14, 14, 16, 0.15)', 'rgba(255, 255, 255, 0.16)'),

  accent: adaptive('#0E0E10', '#F7F7F9'),
  accentHover: adaptive('#26262C', '#FFFFFF'),
  accentPress: adaptive('#000000', '#EDEDF0'),
  accentSoft: adaptive('rgba(14, 14, 16, 0.06)', 'rgba(255, 255, 255, 0.08)'),
  onAccent: adaptive('#FFFFFF', '#0E0E10'),

  live: signal[500],
  liveSoft: 'rgba(48, 209, 88, 0.13)',
  success: signal[500],
  danger: '#FF453A',
  dangerSoft: 'rgba(255, 69, 58, 0.10)',
  warn: '#FF9F0A',
  warnSoft: 'rgba(255, 159, 10, 0.14)',
  info: '#0A84FF',
  infoSoft: 'rgba(10, 132, 255, 0.10)',

  /** Earned only — agent work / ignition, never ambient chrome. */
  ember: ember[500],
  emberSoft: 'rgba(255, 90, 25, 0.12)',
  onEmber: '#FFFFFF',

  /**
   * Ember for TEXT AND GLYPHS. Not a second brand colour — the same signal at a
   * luminance that can actually be read.
   *
   * `ember` is Stride Orange, tuned to glow against dark. On the light theme's
   * #F5F5F7 it measures **2.87:1**, and on an emberSoft chip **2.50:1** — both
   * far under the 4.5:1 AA floor for body text, and under even the 3:1 non-text
   * floor for icons. Every ember label in light mode was effectively decorative.
   *
   * #B83A18 measures 5.27:1 on bgApp and **4.60:1** on emberSoft. That second
   * number is the tight one: it clears AA by a tenth of a point, so darkening
   * emberSoft or lightening #B83A18 will break it. `contrast.test.ts` holds the
   * line. Dark mode keeps ember[500], which passes at 6.37:1 on bgApp and
   * 5.68:1 on a soft chip, and is where the orange belongs.
   *
   * FILLS stay `ember`: the Dock tint, the listening glow, the mention dot, and
   * emberSoft backgrounds carry no text and have no contrast requirement.
   * Swapping those would dull the brand for no accessibility gain.
   */
  emberText: adaptive('#B83A18', ember[500]),
  /** Ember text when the surrounding fill is `accent` (dark in light mode, light in dark mode). */
  onAccentEmber: adaptive(ember[500], '#B83A18'),

  glassBorder: adaptive('rgba(14, 14, 16, 0.10)', 'rgba(255, 255, 255, 0.12)'),
  glassPanel: adaptive('rgba(255, 255, 255, 0.72)', 'rgba(20, 20, 24, 0.78)'),
  scrim: adaptive('rgba(14, 14, 16, 0.35)', 'rgba(0, 0, 0, 0.62)'),

  // Legacy aliases used by early screens — map to live tokens.
  bgElevated: adaptive(paper[0], ink[850]),
  bgMuted: adaptive(paper[100], ink[800]),
  text: adaptive('#0E0E10', '#F7F7F9'),
  textSecondary: adaptive('rgba(14, 14, 16, 0.60)', 'rgba(247, 247, 249, 0.66)'),
  textTertiary: adaptive('rgba(14, 14, 16, 0.38)', 'rgba(247, 247, 249, 0.42)'),
  border: adaptive('rgba(14, 14, 16, 0.08)', 'rgba(255, 255, 255, 0.09)'),
  tabInactive: adaptive('rgba(14, 14, 16, 0.38)', 'rgba(247, 247, 249, 0.42)'),
} as const;

export const radius = {
  sm: 8,
  md: 12,
  lg: 16,
  xl: 22,
  xxl: 28,
  full: 999,
} as const;

export const space = {
  1: 4,
  2: 8,
  3: 12,
  4: 16,
  5: 20,
  6: 24,
  8: 32,
  10: 40,
  12: 48,
} as const;

export const hitMin = 44;

export const shadow = {
  1: {
    shadowColor: adaptive('#0E0E10', '#000000'),
    shadowOpacity: 0.1,
    shadowRadius: 2,
    shadowOffset: { width: 0, height: 1 },
  },
  2: {
    shadowColor: adaptive('#0E0E10', '#000000'),
    shadowOpacity: 0.12,
    shadowRadius: 12,
    shadowOffset: { width: 0, height: 8 },
  },
  glass: {
    shadowColor: adaptive('#0E0E10', '#000000'),
    shadowOpacity: 0.1,
    shadowRadius: 16,
    shadowOffset: { width: 0, height: 8 },
  },
  mark: {
    shadowColor: '#000000',
    shadowOpacity: 0.28,
    shadowRadius: 16,
    shadowOffset: { width: 0, height: 12 },
  },
} as const;

/**
 * Type scale (weights + sizes). On iOS, SF Pro is the closest system match to
 * Google Sans Flex; letterSpacing approximates the live --track-* values.
 */
export const type = {
  title1: { fontSize: 28, fontWeight: '600' as const, letterSpacing: -0.6, lineHeight: 32 },
  title2: { fontSize: 21, fontWeight: '600' as const, letterSpacing: -0.34, lineHeight: 26 },
  /** Live login wordmark: 600 22px / -0.022em */
  wordmark: { fontSize: 22, fontWeight: '600' as const, letterSpacing: -0.48, lineHeight: 22 },
  headline: { fontSize: 17, fontWeight: '600' as const, letterSpacing: -0.17, lineHeight: 22 },
  body: { fontSize: 15, fontWeight: '400' as const, letterSpacing: -0.08, lineHeight: 22 },
  bodyMedium: { fontSize: 15, fontWeight: '500' as const, letterSpacing: -0.08, lineHeight: 22 },
  bodySm: { fontSize: 14, fontWeight: '400' as const, letterSpacing: -0.07, lineHeight: 22 },
  caption: { fontSize: 13, fontWeight: '400' as const, letterSpacing: -0.06, lineHeight: 19 },
  captionMedium: { fontSize: 13, fontWeight: '500' as const, letterSpacing: -0.06, lineHeight: 19 },
  /** Mono-ish labels — use system font with wider tracking like --type-label */
  label: { fontSize: 11, fontWeight: '500' as const, letterSpacing: 0.66, lineHeight: 13 },
  labelLg: { fontSize: 13, fontWeight: '500' as const, letterSpacing: 0.52, lineHeight: 17 },
  button: { fontSize: 14, fontWeight: '600' as const, letterSpacing: -0.08, lineHeight: 18 },
} as const;

/** Live product chrome names (tool rail / phone topbar). */
export const product = {
  name: 'Stride',
  wordmark: 'Stride',
  loginCta: 'Enter your office',
  description: 'conversation becomes memory, approved work, and verified results.',
} as const;
