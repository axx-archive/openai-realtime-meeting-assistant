import React, { useEffect, useMemo, useRef } from 'react';
import { Animated, StyleSheet, View, type ColorValue } from 'react-native';
import { barScales } from '../voice/amplitude';
import { colors } from '../theme/tokens';
import { duration, ease, useReduceMotion } from '../theme/motion';

const bandProfile = [0.42, 0.66, 0.8, 0.9, 0.96, 1, 0.96, 0.88, 0.76, 0.6, 0.36] as const;
const bandShift = [0.17, 0.07, 0.01, -0.02, 0.02, 0, -0.03, 0.03, -0.01, -0.07, -0.14] as const;

type Props = {
  trace: readonly number[];
  listening: boolean;
  size?: number;
  color?: ColorValue;
};

/**
 * The Stride Signal is both identity and instrument.
 *
 * At rest the horizontal bands resolve into the canonical forward-biased disc.
 * While listening, each band responds to a different slice of the real
 * microphone history. Motion therefore means input, never decorative activity.
 */
export function StridePulse({
  trace,
  listening,
  size = 196,
  color,
}: Props) {
  const reduceMotion = useReduceMotion();
  const tint = color ?? (listening ? colors.ember : colors.text2);
  const liveTrace = barScales(trace, listening);
  const scaleValues = useRef(bandProfile.map(() => new Animated.Value(1))).current;
  const shiftValues = useRef(bandProfile.map(() => new Animated.Value(0))).current;

  const targets = useMemo(() => bandProfile.map((_, index) => {
    const start = Math.floor((index * liveTrace.length) / bandProfile.length);
    const end = Math.max(start + 1, Math.floor(((index + 1) * liveTrace.length) / bandProfile.length));
    const slice = liveTrace.slice(start, end);
    const level = slice.reduce((sum, value) => sum + value, 0) / Math.max(1, slice.length);
    if (!listening || reduceMotion) return { scale: 1, shift: 0 };
    return {
      scale: 0.82 + level * 0.28,
      shift: (level - 0.45) * size * 0.075 * (index % 2 === 0 ? 1 : -1),
    };
  }), [liveTrace, listening, reduceMotion, size]);

  useEffect(() => {
    if (listening || reduceMotion) {
      scaleValues.forEach((value, index) => value.setValue(targets[index]?.scale ?? 1));
      shiftValues.forEach((value, index) => value.setValue(targets[index]?.shift ?? 0));
      return;
    }

    Animated.parallel([
      ...scaleValues.map((value, index) => Animated.timing(value, {
        toValue: targets[index]?.scale ?? 1,
        duration: duration.slow,
        easing: ease,
        useNativeDriver: true,
      })),
      ...shiftValues.map((value, index) => Animated.timing(value, {
        toValue: targets[index]?.shift ?? 0,
        duration: duration.slow,
        easing: ease,
        useNativeDriver: true,
      })),
    ]).start();
  }, [ease, listening, reduceMotion, scaleValues, shiftValues, targets]);

  const bandHeight = size * 0.056;
  const gap = size * 0.022;

  return (
    <View
      style={[styles.disc, { width: size, height: size, gap }]}
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
    >
      {bandProfile.map((width, index) => (
        <Animated.View
          key={`${width}-${index}`}
          style={[
            styles.band,
            {
              width: size * width,
              height: bandHeight,
              borderTopLeftRadius: index % 2 === 0 ? bandHeight : 2,
              borderBottomRightRadius: index % 2 === 0 ? 2 : bandHeight,
              borderTopRightRadius: bandHeight,
              borderBottomLeftRadius: bandHeight,
              backgroundColor: tint,
              opacity: listening ? 1 : 0.78,
              transform: [
                { translateX: Animated.add(shiftValues[index], size * bandShift[index] * 0.18) },
                { scaleX: scaleValues[index] },
              ],
            },
          ]}
        />
      ))}
    </View>
  );
}

const styles = StyleSheet.create({
  disc: {
    alignItems: 'center',
    justifyContent: 'center',
    overflow: 'hidden',
    borderRadius: 999,
  },
  band: {
    transformOrigin: 'center',
  },
});
