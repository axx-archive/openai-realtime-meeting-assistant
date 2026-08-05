import { DynamicColorIOS, Platform, type ColorValue } from 'react-native';

/**
 * Glass & Ink tokens — mirrored from the live Stride `index.html` :root.
 * Source of truth: deployed web design (see `.superdesign/design-system.md`).
 * Do not invent alternate palettes or revive the retired warm-dark Bonfire look.
 */

/**
 * paper — WARM PUTTY, not white.
 *
 * Light mode is grounded on the same #CFC5B7 the app icon's light appearance is
 * built on, so the tile on the home screen and the app behind it are one
 * material. The names stay `paper` because the role did not change: this is
 * still the stock the ink is printed on, it is just a warmer stock.
 *
 * The ramp keeps the old relationship exactly — panels lift OFF the ground,
 * wells sink UNDER it — so no screen had to be re-thought, only re-measured.
 * Mirrored from index.html's `--paper-*`; contrast.test.ts holds both copies to
 * the same numbers.
 */
export const paper = {
  0: '#EDE8DF',
  50: '#CFC5B7',
  100: '#C2B7A7',
  200: '#B2A695',
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

  /**
   * The ink ladder, re-solved for the putty ground.
   *
   * THE INK IS A WARM DARK GREY, NOT BLACK. Near-black on warm putty reads as a
   * printing error — the two sit on opposite sides of neutral and the eye sees
   * the mismatch before it sees the words. This is the "putty / dark grey"
   * pairing.
   *
   * The ground's luminance fell from 0.90 to 0.57, so every alpha that used to
   * clear AA had to move; nothing here was carried over. On the worst-case
   * surface (paper[100] = #C2B7A7): text1 7.9:1 · text2 6.1:1 · text3 4.6:1.
   * text3 sits nine hundredths over the floor — do not lower without re-solving.
   */
  text1: adaptive('#26231E', '#F7F7F9'),
  text2: adaptive('rgba(38, 35, 30, 0.87)', 'rgba(247, 247, 249, 0.66)'),
  text3: adaptive('rgba(38, 35, 30, 0.75)', 'rgba(247, 247, 249, 0.42)'),
  textDisabled: adaptive('rgba(38, 35, 30, 0.43)', 'rgba(247, 247, 249, 0.24)'),

  // Hairlines gained weight: an 8% ink line that read on #EDEDF0 is nearly
  // invisible on a ground this dark.
  line1: adaptive('rgba(38, 35, 30, 0.12)', 'rgba(255, 255, 255, 0.09)'),
  line2: adaptive('rgba(38, 35, 30, 0.22)', 'rgba(255, 255, 255, 0.16)'),

  accent: adaptive('#26231E', '#F7F7F9'),
  accentHover: adaptive('#3A362E', '#FFFFFF'),
  accentPress: adaptive('#16140F', '#EDEDF0'),
  accentSoft: adaptive('rgba(38, 35, 30, 0.08)', 'rgba(255, 255, 255, 0.08)'),
  onAccent: adaptive('#FFFDF8', '#0E0E10'),

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
  emberSoft: 'rgba(255, 90, 25, 0.14)',
  onEmber: '#FFFFFF',

  /**
   * Ember for TEXT AND GLYPHS. Not a second brand colour — the same signal at a
   * luminance that can actually be read.
   *
   * `ember` is Stride Orange, tuned to glow against dark. It was already only
   * **2.87:1** on the old white paper; on the putty ground it measures
   * **1.83:1**. A darker ground makes an already-failing value worse, so the
   * darkened cut had to move too: #B83A18 fell to 3.37:1 on putty and no longer
   * clears AA either.
   *
   * #86290F measures 5.27:1 on bgApp, **4.55:1** on the worst-case well, and
   * 4.80:1 on an emberSoft chip. The WELL is the tight one now — darkening the
   * well or lightening this breaks AA. `contrast.test.ts` holds the line. Dark
   * mode keeps ember[500], which passes at 6.37:1 on bgApp and 5.68:1 on a soft
   * chip, and is where the orange belongs.
   *
   * FILLS stay `ember`: the Dock tint, the listening glow, the mention dot, and
   * emberSoft backgrounds carry no text and have no contrast requirement.
   * Swapping those would dull the brand for no accessibility gain.
   */
  emberText: adaptive('#86290F', ember[500]),

  /** Founder-supplied wordmark cuts: black on light, Stride Orange on dark. */
  wordmark: adaptive('#000000', '#FF5A19'),
  /** Ember text when the surrounding fill is `accent` (dark in light mode, light in dark mode). */
  onAccentEmber: adaptive(ember[500], '#B83A18'),

  // The glass is WARM. White glass over a putty ground composites to a dead
  // grey and makes every panel look like it is on the wrong wallpaper.
  glassBorder: adaptive('rgba(38, 35, 30, 0.14)', 'rgba(255, 255, 255, 0.12)'),
  glassPanel: adaptive('rgba(255, 253, 248, 0.72)', 'rgba(20, 20, 24, 0.78)'),
  scrim: adaptive('rgba(38, 35, 30, 0.35)', 'rgba(0, 0, 0, 0.62)'),

  // Legacy aliases used by early screens — map to live tokens.
  bgElevated: adaptive(paper[0], ink[850]),
  bgMuted: adaptive(paper[100], ink[800]),
  text: adaptive('#26231E', '#F7F7F9'),
  textSecondary: adaptive('rgba(38, 35, 30, 0.87)', 'rgba(247, 247, 249, 0.66)'),
  textTertiary: adaptive('rgba(38, 35, 30, 0.75)', 'rgba(247, 247, 249, 0.42)'),
  border: adaptive('rgba(38, 35, 30, 0.12)', 'rgba(255, 255, 255, 0.09)'),
  tabInactive: adaptive('rgba(38, 35, 30, 0.75)', 'rgba(247, 247, 249, 0.42)'),
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
    shadowColor: adaptive('#26231E', '#000000'),
    shadowOpacity: 0.1,
    shadowRadius: 2,
    shadowOffset: { width: 0, height: 1 },
  },
  2: {
    shadowColor: adaptive('#26231E', '#000000'),
    shadowOpacity: 0.12,
    shadowRadius: 12,
    shadowOffset: { width: 0, height: 8 },
  },
  glass: {
    shadowColor: adaptive('#26231E', '#000000'),
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
export const fonts = {
  sansRegular: 'GoogleSansFlex_400Regular',
  sansMedium: 'GoogleSansFlex_500Medium',
  sansSemiBold: 'GoogleSansFlex_600SemiBold',
  sansBold: 'GoogleSansFlex_700Bold',
  monoRegular: 'GeistMono_400Regular',
  monoMedium: 'GeistMono_500Medium',
  monoSemiBold: 'GeistMono_600SemiBold',
} as const;

export const type = {
  title1: { fontFamily: fonts.sansSemiBold, fontSize: 28, fontWeight: '600' as const, letterSpacing: -0.6, lineHeight: 32 },
  title2: { fontFamily: fonts.sansSemiBold, fontSize: 21, fontWeight: '600' as const, letterSpacing: -0.34, lineHeight: 26 },
  /** Live login wordmark: 600 22px / -0.022em */
  wordmark: { fontFamily: fonts.sansSemiBold, fontSize: 22, fontWeight: '600' as const, letterSpacing: -0.48, lineHeight: 22 },
  headline: { fontFamily: fonts.sansSemiBold, fontSize: 17, fontWeight: '600' as const, letterSpacing: -0.17, lineHeight: 22 },
  body: { fontFamily: fonts.sansRegular, fontSize: 15, fontWeight: '400' as const, letterSpacing: -0.08, lineHeight: 22 },
  bodyMedium: { fontFamily: fonts.sansMedium, fontSize: 15, fontWeight: '500' as const, letterSpacing: -0.08, lineHeight: 22 },
  bodySm: { fontFamily: fonts.sansRegular, fontSize: 14, fontWeight: '400' as const, letterSpacing: -0.07, lineHeight: 22 },
  caption: { fontFamily: fonts.sansRegular, fontSize: 13, fontWeight: '400' as const, letterSpacing: -0.06, lineHeight: 19 },
  captionMedium: { fontFamily: fonts.sansMedium, fontSize: 13, fontWeight: '500' as const, letterSpacing: -0.06, lineHeight: 19 },
  /** Mono-ish labels — use system font with wider tracking like --type-label */
  label: { fontFamily: fonts.monoMedium, fontSize: 11, fontWeight: '500' as const, letterSpacing: 0.66, lineHeight: 13 },
  labelLg: { fontFamily: fonts.monoMedium, fontSize: 13, fontWeight: '500' as const, letterSpacing: 0.52, lineHeight: 17 },
  button: { fontFamily: fonts.sansSemiBold, fontSize: 14, fontWeight: '600' as const, letterSpacing: -0.08, lineHeight: 18 },
} as const;

/** Live product chrome names (tool rail / phone topbar). */
export const product = {
  name: 'Stride',
  wordmark: 'Stride',
  loginCta: 'Enter your office',
  description: 'conversation becomes memory, approved work, and verified results.',
} as const;
