import React, { memo, useEffect, useMemo, useRef } from 'react';
import { Animated, Easing, StyleSheet, View, type ColorValue } from 'react-native';
import { useReduceMotion } from '../theme/motion';

const BAR_COUNT = 21;
const REST_PROFILE = [
  0.18, 0.28, 0.22, 0.42, 0.31, 0.56, 0.38,
  0.72, 0.48, 0.86, 0.58, 0.78, 0.46, 0.68,
  0.36, 0.54, 0.29, 0.43, 0.2, 0.31, 0.17,
] as const;

type Props = {
  color: ColorValue;
  compact?: boolean;
  speaking: boolean;
};

/**
 * The visible feed for an invited agent. One native-driven phase animates all
 * bars through distinct envelopes, so the result reads as speech without a JS
 * render loop. At rest it becomes a sculpted, static signature in that agent's
 * assigned color. The home-screen Signal Cradle remains unique to Scout.
 */
export const AgentSpeakingWaveform = memo(function AgentSpeakingWaveform({
  color,
  compact = false,
  speaking,
}: Props) {
  const reduceMotion = useReduceMotion();
  const phase = useRef(new Animated.Value(0)).current;
  const envelopes = useMemo(() => Array.from({ length: BAR_COUNT }, (_, index) => {
    const sample = (turn: number) => {
      const carrier = 0.5 + 0.5 * Math.sin(turn * Math.PI * 2 + index * 1.17);
      const secondary = 0.5 + 0.5 * Math.sin(turn * Math.PI * 4 - index * 0.73);
      return Math.min(1, 0.14 + (0.58 * carrier + 0.28 * secondary) * (0.72 + REST_PROFILE[index] * 0.28));
    };
    return [sample(0), sample(0.25), sample(0.5), sample(0.75), sample(1)];
  }), []);

  useEffect(() => {
    phase.stopAnimation();
    phase.setValue(0);
    if (!speaking || reduceMotion) return undefined;
    const loop = Animated.loop(Animated.timing(phase, {
      toValue: 1,
      duration: 1120,
      easing: Easing.linear,
      useNativeDriver: true,
    }));
    loop.start();
    return () => loop.stop();
  }, [phase, reduceMotion, speaking]);

  const height = compact ? 34 : 72;
  const width = compact ? 2 : 3;
  const gap = compact ? 2 : 4;
  return (
    <View
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
      style={[styles.row, { gap, height }]}
    >
      {REST_PROFILE.map((rest, index) => {
        const scaleY = speaking && !reduceMotion
          ? phase.interpolate({
            inputRange: [0, 0.25, 0.5, 0.75, 1],
            outputRange: envelopes[index],
          })
          : rest;
        return (
          <Animated.View
            key={index}
            style={{
              width,
              height,
              borderRadius: width,
              backgroundColor: color,
              opacity: speaking ? 1 : 0.62,
              transform: [{ scaleY }],
            }}
          />
        );
      })}
    </View>
  );
});

const styles = StyleSheet.create({
  row: {
    alignItems: 'center',
    flexDirection: 'row',
    justifyContent: 'center',
  },
});
