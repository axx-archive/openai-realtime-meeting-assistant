import React, { useCallback, useEffect, useRef } from 'react';
import { Animated, Pressable, StyleSheet, Text, View } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import * as Haptics from 'expo-haptics';
import { GlassContainer } from 'expo-glass-effect';
import { Glass, usingLiquidGlass } from '../theme/glass';
import { duration, ease, easeSpring, useReduceMotion } from '../theme/motion';
import { NAV_ITEM_WIDTH, NAV_ITEMS_RIGHT_INSET } from './dockRowLayout';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

/**
 * The NavCluster — the app's answer to "what if voice isn't available?"
 *
 * The original shell made voice the only FAST path: everything else lived
 * behind a drag-up into the Deck. That is a resilience failure, not a purity
 * win. Voice goes away for ordinary reasons — an expired model quota, no
 * signal, a denied microphone, a room too loud to talk in, a meeting you are
 * already sitting in — and in every one of those cases you still need to start
 * a video room RIGHT NOW. A product that becomes unusable when its headline
 * feature degrades is a demo, not an operating system.
 *
 * So: a floating cluster of glass icon buttons, one tap from the canvas, that
 * never touches the voice pipeline. It is deliberately NOT a tab bar — it does
 * not claim to be the app's spine, it is collapsed by default, and the canvas
 * stays quiet until you ask for it.
 *
 * On iOS 26 the buttons live inside a `GlassContainer`, so they physically
 * merge and separate as the cluster opens — `UIGlassContainerEffect`, the real
 * platform behaviour, not an imitation of it.
 */

export type NavDestination = {
  id: string;
  label: string;
  icon: SFSymbol;
  onPress: () => void;
  /** Ember-tinted: the one action that must survive a dead voice pipeline. */
  emphasis?: boolean;
};

// Geometry lives in dockRowLayout so it can be TESTED — on a 375pt SE the
// expanded cluster and the chat circle overlap by 29pt, and the design only
// survives that because the circle cross-fades out first. See
// __tests__/dockRowLayout.test.ts.
const ITEM_WIDTH = NAV_ITEM_WIDTH;
const ITEM_LABEL_HEIGHT = 16;

export type NavClusterProps = {
  open: boolean;
  onToggle: () => void;
  destinations: NavDestination[];
};

export function NavCluster({ open, onToggle, destinations }: NavClusterProps) {
  const reduceMotion = useReduceMotion();
  const progress = useRef(new Animated.Value(open ? 1 : 0)).current;
  const expandedWidth = hitMin
    + space[5]
    + (destinations.length * ITEM_WIDTH)
    + (Math.max(0, destinations.length - 1) * space[2]);

  useEffect(() => {
    if (reduceMotion) {
      progress.setValue(open ? 1 : 0);
      return;
    }
    Animated.timing(progress, {
      toValue: open ? 1 : 0,
      duration: open ? duration.med : duration.fast,
      easing: open ? easeSpring : ease,
      useNativeDriver: true,
    }).start();
  }, [open, progress, reduceMotion]);

  const toggle = useCallback(() => {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Soft);
    onToggle();
  }, [onToggle]);

  // The cluster opens sideways along the bottom utility row, not upward as a
  // column: a vertical stack on the right edge overlays the greeting and forces
  // 44pt-wide labels that truncate ("Threa…"). Travelling right-to-left keeps it
  // inside its own band of empty canvas.
  const items = destinations.map((destination, index) => {
    const offset = (destinations.length - index) * (ITEM_WIDTH + space[2]);
    return (
      <Animated.View
        key={destination.id}
        style={{
          alignItems: 'center',
          width: ITEM_WIDTH,
          opacity: progress,
          transform: [
            {
              translateX: progress.interpolate({
                inputRange: [0, 1],
                outputRange: [offset, 0],
              }),
            },
            {
              scale: progress.interpolate({ inputRange: [0, 1], outputRange: [0.72, 1] }),
            },
          ],
        }}
      >
        <Glass
          radius={radius.full}
          interactive
          tint={destination.emphasis ? colors.emberSoft : undefined}
          style={styles.item}
        >
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={destination.label}
            onPress={() => {
              void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
              destination.onPress();
            }}
            style={styles.itemPress}
          >
            <SymbolView
              name={destination.icon}
              tintColor={destination.emphasis ? colors.ember : colors.text1}
              size={19}
            />
          </Pressable>
        </Glass>
        <Text style={styles.itemLabel} numberOfLines={1}>
          {destination.label}
        </Text>
      </Animated.View>
    );
  });

  const body = (
    <>
      {/* Absolutely positioned so a COLLAPSED cluster occupies no layout space.
          Left in flow, the hidden items reserve a ~250pt invisible column that
          shoves the Dock to mid-screen. */}
	  <View
		style={[styles.items, !open && styles.inert]}
		accessibilityElementsHidden={!open}
		importantForAccessibility={open ? 'auto' : 'no-hide-descendants'}
	  >
        {items}
      </View>
      <Glass radius={radius.full} interactive style={styles.toggle}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={open ? 'Close shortcuts' : 'Open shortcuts'}
          accessibilityHint="Start a room, open threads, or jump to work without using your voice."
          accessibilityState={{ expanded: open }}
          onPress={toggle}
          style={styles.itemPress}
        >
          {/* No rotation: an `xmark` turned 45° is a PLUS, which reads as "add
              something" rather than "close this". */}
          <SymbolView
            name={open ? 'xmark' : 'square.grid.2x2.fill'}
            tintColor={colors.text1}
            size={18}
          />
        </Pressable>
      </Glass>
    </>
  );

  return (
    <View
      style={[
        styles.wrap,
        open && { width: expandedWidth, height: hitMin + ITEM_LABEL_HEIGHT },
      ]}
    >
      {usingLiquidGlass() ? (
        // `spacing` is the distance at which sibling glass starts to merge, so
        // the collapsed cluster reads as a single body that separates as it
        // opens. Below iOS 26 the Glass fallbacks simply stack.
        <GlassContainer spacing={18} style={[styles.container, open && styles.containerOpen]}>
          {body}
        </GlassContainer>
      ) : (
        <View style={[styles.container, open && styles.containerOpen]}>{body}</View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: {
    // Right-aligned in the Canvas utility row.
    alignSelf: 'flex-end',
    marginRight: space[5],
    marginBottom: space[3],
  },
  container: {
    flexDirection: 'row',
    alignItems: 'flex-end',
    backgroundColor: 'transparent',
  },
  containerOpen: {
    // React Native only dispatches a touch inside an ancestor's measured
    // bounds. The shortcut buttons animate outside the collapsed toggle's
    // 44pt box, so the expanded cluster must own the whole visible hit region.
    width: '100%',
    height: '100%',
    justifyContent: 'flex-end',
    alignItems: 'flex-start',
  },
  items: {
    // Absolute so a COLLAPSED cluster occupies no layout space. Left in flow,
    // the hidden items reserve an invisible band that shoves the Dock upward.
    position: 'absolute',
    // Clear of the toggle by more than the container's merge `spacing`, so the
    // last item and the toggle stay two distinct pieces of glass instead of
    // bridging into a lump.
    right: NAV_ITEMS_RIGHT_INSET,
    bottom: 0,
    flexDirection: 'row',
    alignItems: 'flex-end',
    gap: space[2],
  },
  /** Collapsed items must not swallow taps meant for the canvas beneath. */
  inert: {
    pointerEvents: 'none',
  },
  item: {
    width: hitMin,
    height: hitMin,
  },
  toggle: {
    width: hitMin,
    height: hitMin,
  },
  itemPress: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
  },
  itemLabel: {
    // 10px in a 58pt column fits "Threads" without an ellipsis; the 11pt label
    // token truncated it at 44pt.
    fontSize: 10,
    lineHeight: 12,
    fontFamily: 'GoogleSansFlex_500Medium', fontWeight: '500',
    letterSpacing: 0.3,
    color: colors.text2,
    textAlign: 'center',
    marginTop: 4,
  },
});
