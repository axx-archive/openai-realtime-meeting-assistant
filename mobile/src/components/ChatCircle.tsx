import React, { useEffect, useRef } from 'react';
import { Animated, Pressable, StyleSheet, View } from 'react-native';
import { SymbolView } from 'expo-symbols';
import * as Haptics from 'expo-haptics';
import { Glass } from '../theme/glass';
import { duration, ease, useReduceMotion } from '../theme/motion';
import { colors, hitMin, radius, space } from '../theme/tokens';

/**
 * The chat circle — design §6 of docs/plans/the-table-design.md.
 *
 * One permanent glass circle in the row above the Dock, so the team thread is
 * always one tap away:
 *
 *     rest    [chat] ................... [nav toggle]
 *     open           [Room][Threads][Live][Work] [x]
 *
 * It cannot live ON the Dock: that surface is full-width and tap / hold /
 * drag-up / trailing-keyboard are all spent, and a fifth affordance would
 * collide with gestures the shell design calls load-bearing.
 *
 * It is one circle, not five. A tab bar is permanent structure that declares
 * itself the app's spine — which is what made the old shell a dashboard. A
 * single control for the single highest-frequency destination is not that.
 */

export type ChatCircleProps = {
  /** True while the NavCluster is open — the circle yields. */
  clusterOpen: boolean;
  /** An unread DIRECT MENTION. Never ambient volume. */
  mentioned: boolean;
  onPress: () => void;
};

export function ChatCircle({ clusterOpen, mentioned, onPress }: ChatCircleProps) {
  const reduceMotion = useReduceMotion();
  const progress = useRef(new Animated.Value(clusterOpen ? 0 : 1)).current;

  useEffect(() => {
    const toValue = clusterOpen ? 0 : 1;
    if (reduceMotion) {
      progress.setValue(toValue);
      return;
    }
    Animated.timing(progress, {
      toValue,
      duration: clusterOpen ? duration.fast : duration.med,
      easing: ease,
      useNativeDriver: true,
    }).start();
  }, [clusterOpen, progress, reduceMotion]);

  return (
    // Cross-fades out while the cluster is open. Four 58pt labelled items plus
    // this 44pt circle plus the toggle does not fit an iPhone SE's 375pt, and
    // the circle's function is covered meanwhile — Threads is one of the four.
    // Yielding beats colliding, and it leaves NavCluster's right-to-left
    // geometry (and its absolute-positioning rule) completely untouched.
    <Animated.View
      style={[
        styles.wrap,
        {
          opacity: progress,
          transform: [
            { scale: progress.interpolate({ inputRange: [0, 1], outputRange: [0.8, 1] }) },
          ],
        },
      ]}
      pointerEvents={clusterOpen ? 'none' : 'auto'}
    >
      <Glass radius={radius.full} interactive style={styles.circle}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Team"
          accessibilityHint="Opens your team's thread."
          accessibilityState={{ busy: mentioned }}
          onPress={() => {
            void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
            onPress();
          }}
          style={styles.press}
        >
          <SymbolView
            name="bubble.left.and.bubble.right.fill"
            tintColor={colors.text1}
            size={19}
          />
        </Pressable>
      </Glass>

      {/* Direct mentions only. This dot moved OFF the Dock deliberately: the
          Dock means "talk to Scout", and hanging a message badge there
          conflates two unrelated things. A badge belongs on the control that
          takes you to the messages. Ambient volume never earns it — otherwise
          it is lit permanently and stops meaning anything. */}
      {mentioned ? <View style={styles.dot} pointerEvents="none" /> : null}
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  wrap: {
    // Mirrors NavCluster's own insets exactly (marginRight/marginBottom), so
    // the two ends of the row sit on the same optical margin as the Dock's
    // pill edge below them. Vertical alignment comes from the parent row.
    marginLeft: space[5],
    marginBottom: space[3],
  },
  circle: {
    width: hitMin,
    height: hitMin,
  },
  press: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
  },
  dot: {
    position: 'absolute',
    top: 1,
    right: 1,
    width: 9,
    height: 9,
    borderRadius: radius.full,
    backgroundColor: colors.ember,
    // A hairline ring keeps the dot legible against whatever the glass is
    // sitting over — an ember dot on an ember-ish backdrop vanishes.
    borderWidth: 1.5,
    borderColor: colors.bgApp,
  },
});
