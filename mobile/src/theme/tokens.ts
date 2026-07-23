/**
 * Glass & Ink tokens — mirrored from the live BonfireOS `index.html` :root.
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

export const ember = {
  300: '#FF9E85',
  400: '#FF8163',
  500: '#FF6B4A',
  600: '#F0522F',
} as const;

/** Semantic aliases — light theme (default). */
export const colors = {
  bg: paper[50],
  bgApp: paper[50],
  surface1: paper[0],
  surface2: paper[0],
  surface3: paper[100],

  text1: '#0E0E10',
  text2: 'rgba(14, 14, 16, 0.60)',
  text3: 'rgba(14, 14, 16, 0.38)',
  textDisabled: 'rgba(14, 14, 16, 0.24)',

  line1: 'rgba(14, 14, 16, 0.08)',
  line2: 'rgba(14, 14, 16, 0.15)',

  accent: '#0E0E10',
  accentHover: '#26262C',
  accentPress: '#000000',
  accentSoft: 'rgba(14, 14, 16, 0.06)',
  onAccent: '#FFFFFF',

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
  emberSoft: 'rgba(255, 107, 74, 0.12)',
  onEmber: '#FFFFFF',

  glassBorder: 'rgba(14, 14, 16, 0.10)',
  glassPanel: 'rgba(255, 255, 255, 0.72)',
  scrim: 'rgba(14, 14, 16, 0.35)',

  // Legacy aliases used by early screens — map to live tokens.
  bgElevated: paper[0],
  bgMuted: paper[100],
  text: '#0E0E10',
  textSecondary: 'rgba(14, 14, 16, 0.60)',
  textTertiary: 'rgba(14, 14, 16, 0.38)',
  border: 'rgba(14, 14, 16, 0.08)',
  tabInactive: 'rgba(14, 14, 16, 0.38)',
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
    shadowColor: '#0E0E10',
    shadowOpacity: 0.1,
    shadowRadius: 2,
    shadowOffset: { width: 0, height: 1 },
  },
  2: {
    shadowColor: '#0E0E10',
    shadowOpacity: 0.12,
    shadowRadius: 12,
    shadowOffset: { width: 0, height: 8 },
  },
  glass: {
    shadowColor: '#0E0E10',
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
  name: 'BonfireOS',
  wordmark: 'bonfireOS',
  loginCta: 'Enter your office',
  description: 'your company as intelligence. Meet, decide, and ship together.',
} as const;
