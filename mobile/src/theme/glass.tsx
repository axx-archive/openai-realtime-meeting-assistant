import React from 'react';
import { StyleSheet, View, type ViewProps, type ViewStyle } from 'react-native';
import { BlurView } from 'expo-blur';
import { GlassView, isLiquidGlassAvailable } from 'expo-glass-effect';
import { colors } from './tokens';
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
  tint?: string;
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
  const shape: ViewStyle = { borderRadius: radius, overflow: 'hidden' };

  // Reduce Transparency wins over everything: the user asked for no material.
  if (reduceTransparency) {
    return (
      <View style={[shape, styles.opaque, style]} {...rest}>
        {children}
      </View>
    );
  }

  if (liquidGlass) {
    return (
      <GlassView
        // NOTE: never animate a GlassView by driving `opacity` to 0 — that kills
        // the effect outright. Use `glassEffectStyle.animate` instead.
        glassEffectStyle={variant}
        isInteractive={interactive}
        tintColor={tint}
        style={[shape, style]}
        {...rest}
      >
        {children}
      </GlassView>
    );
  }

  // Pre-iOS 26: blur plus an explicit hairline, because BlurView alone has no
  // edge and the panel dissolves into the canvas without one.
  return (
    <View style={[shape, styles.legacyEdge, style]} {...rest}>
      <BlurView
        intensity={variant === 'clear' ? 24 : 42}
        tint="systemChromeMaterial"
        style={StyleSheet.absoluteFill}
      />
      {tint ? <View style={[StyleSheet.absoluteFill, { backgroundColor: tint, opacity: 0.12 }]} /> : null}
      {children}
    </View>
  );
}

const styles = StyleSheet.create({
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
