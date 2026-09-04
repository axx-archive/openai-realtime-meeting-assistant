import React from 'react';
import { StyleSheet, View, useColorScheme, type ColorValue, type ViewProps, type ViewStyle } from 'react-native';
import { BlurView } from 'expo-blur';
import { GlassView, isLiquidGlassAvailable } from 'expo-glass-effect';
import { colors, resolveThemeTint, shadow } from './tokens';
import { useReduceTransparency } from './motion';

/**
 * The Liquid Glass material — design §7.
 *
 *   Glass means "floating above the conversation, temporarily."
 *   NEVER use glass for permanent structure, and never for content you have to
 *   read for more than a moment: glass is a variable backdrop, so text on it has
 *   no contrast guarantee.
 *
 *      Glass                    Not glass
 *      ─────                    ─────────
 *      Dock, Deck sheet         Canvas background
 *      Island, composer         Message bubbles, list rows
 *      Popovers, sheets         Board cards, any sustained reading
 *
 * Every call site goes through this component so no caller ever branches on
 * runtime capability. Three paths (design §7 fallback matrix):
 *
 *   iOS 26 + Liquid Glass available  →  GlassView
 *   iOS 16.4-25 / API unavailable    →  BlurView + hairline border
 *   Reduce Transparency enabled      →  opaque surface, no blur at all
 */

export type GlassProps = ViewProps & {
  /** Corner radius. Applied to whichever backing view is used. */
  radius?: number;
  /**
   * Lets the glass react to touch with the system's interactive glass response.
   * Only meaningful on the GlassView path; harmless elsewhere.
   */
  interactive?: boolean;
  /** Earned-only tint. Pass `colors.ember` when agent work is live — never ambient. */
  tint?: ColorValue;
  /** `'regular'` for chrome, `'clear'` for glass over media. */
  variant?: 'regular' | 'clear';
};

/**
 * Resolved once per process. `isLiquidGlassAvailable()` is a static capability
 * check (OS version + API presence), so re-querying it per render would burn a
 * bridge call on every frame for an answer that cannot change.
 */
const liquidGlass = isLiquidGlassAvailable();

/** True when real Liquid Glass is rendering — for callers that adjust contrast. */
export function usingLiquidGlass(): boolean {
  return liquidGlass;
}

export function Glass({
  radius = 0,
  interactive = false,
  tint,
  variant = 'regular',
  style,
  children,
  ...rest
}: GlassProps) {
  const reduceTransparency = useReduceTransparency();
  const resolvedTint = resolveThemeTint(tint, useColorScheme() === 'dark');
  const shape: ViewStyle = { borderRadius: radius, overflow: 'hidden' };

  /**
   * Elevation is not decoration here — it is what makes the material read as
   * glass at all. Glass means "floating above the conversation" (§7), and on a
   * flat canvas an unshadowed GlassView is just white paper lying on white
   * paper. The shadow lives on an OUTER wrapper because iOS will not draw a
   * shadow on the same view that clips its children with `overflow: hidden`.
   */
  // The material is a BACKGROUND, not a wrapper: it fills the container
  // absolutely while the children lay out normally on top. That way the
  // caller's own style still drives size, padding and margin, and the shadow
  // sits on a view that is not clipping anything.
  const material = reduceTransparency ? (
    <View style={[shape, styles.opaque, StyleSheet.absoluteFill, styles.inert]} />
  ) : liquidGlass ? (
    <>
      <GlassView
        // NOTE: never animate a GlassView by driving `opacity` to 0 — that kills
        // the effect outright. Use `glassEffectStyle.animate` instead.
        glassEffectStyle={variant}
        isInteractive={interactive}
        tintColor={resolvedTint}
        style={[shape, StyleSheet.absoluteFill, styles.inert]}
      />
      {tint !== undefined && resolvedTint === undefined ? (
        <View style={[shape, StyleSheet.absoluteFill, styles.inert, { backgroundColor: tint, opacity: 0.12 }]} />
      ) : null}
    </>
  ) : (
    // Pre-iOS 26: blur plus an explicit hairline, because BlurView alone has no
    // edge and the panel dissolves into the canvas without one.
    <View style={[shape, styles.legacyEdge, StyleSheet.absoluteFill, styles.inert]}>
      <BlurView
        intensity={variant === 'clear' ? 24 : 42}
        tint="systemChromeMaterial"
        style={StyleSheet.absoluteFill}
      />
      {tint ? (
        <View style={[StyleSheet.absoluteFill, { backgroundColor: tint, opacity: 0.12 }]} />
      ) : null}
    </View>
  );

  return (
    <View style={[{ borderRadius: radius }, styles.lift, style]} {...rest}>
      {material}
      {children}
    </View>
  );
}

const styles = StyleSheet.create({
  /**
   * Soft and wide, not dark and tight. A floating control needs to read as
   * hovering a few millimetres off the page; a hard shadow reads as a sticker.
   */
  lift: {
    shadowColor: shadow[2].shadowColor,
    shadowOpacity: 0.1,
    shadowRadius: 18,
    shadowOffset: { width: 0, height: 6 },
  },
  /**
   * The material never takes touches — the children sit on top of it and own
   * every interaction. Declared in style rather than as a `pointerEvents` prop,
   * which React Native now deprecates in favour of the style property.
   */
  inert: {
    pointerEvents: 'none',
  },
  opaque: {
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
  },
  legacyEdge: {
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.glassBorder,
    backgroundColor: 'transparent',
  },
});
