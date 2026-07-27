import React, { useEffect, useMemo, useRef } from 'react';
import { Animated, StyleSheet, View, type ColorValue } from 'react-native';
import { barScales } from '../voice/amplitude';
import { colors } from '../theme/tokens';
import { duration, ease, useReduceMotion, waveform } from '../theme/motion';

/**
 * The instrument — design §3.
 *
 * These bars are a render of the microphone, not a keyframe loop. That is the
 * whole point: an animated waveform cannot tell you whether it is actually
 * hearing you, and "is this thing listening?" is the central anxiety of every
 * voice interface. This one answers continuously, by physics.
 *
 * Two laws are enforced here rather than left to call sites:
 *   - Rest is STATIC (§8 law 1). `listening=false` renders a flat, calm line.
 *   - Transforms only (§8 law 4). Bars have a fixed layout height and scale on
 *     Y; animating height would relayout the row every sample. This is the same
 *     width→transform lesson already paid for on the web client.
 */

export type WaveformProps = {
  /** 0..1, already smoothed by `useDictation`. */
  amplitude: number;
  /** While false the bars rest static — the breathe-only-while-listening law. */
  listening: boolean;
  /** Ember while listening, muted ink at rest. Ember is earned (§8 law 2). */
  color?: ColorValue;
  height?: number;
};

export function Waveform({ amplitude, listening, color, height = waveform.height }: WaveformProps) {
  const reduceMotion = useReduceMotion();
  const tint = color ?? (listening ? colors.ember : colors.text2);

  const targets = barScales(amplitude, listening);
  const values = useRef(targets.map((scale) => new Animated.Value(scale))).current;

  useEffect(() => {
    const animations = values.map((value, index) =>
      Animated.timing(value, {
        toValue: targets[index] ?? waveform.restScale,
        // One sampling interval, so each sample lands exactly as the next
        // arrives — the row reads as continuous rather than stepped.
        duration: listening ? duration.fast : duration.slow,
        easing: ease,
        useNativeDriver: true,
      }),
    );
    Animated.parallel(animations).start();
    // `targets` is a fresh array each render; the join is the actual dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [targets.join(','), listening]);

  const bars = useMemo(
    () => values.slice(0, waveform.barCount),
    [values],
  );

  // Reduce Motion: one static level bar instead of an animated row. The
  // amplitude RESPONSE survives — it is information about whether the mic hears
  // you, not decoration — but nothing tweens between samples.
  if (reduceMotion) {
    return (
      <View
        style={[styles.row, { height }]}
        accessibilityElementsHidden
        importantForAccessibility="no-hide-descendants"
      >
        <View
          style={[
            styles.reducedTrack,
            { backgroundColor: tint, height: Math.max(4, height * (listening ? amplitude : 0.06)) },
          ]}
        />
      </View>
    );
  }

  return (
    // Decorative to a screen reader: the STATE is announced by the Dock, which
    // says "Listening" and a duration rather than streaming bar heights (§9.5).
    <View
      style={[styles.row, { height }]}
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
    >
      {bars.map((value, index) => (
        <Animated.View
          key={index}
          style={[
            styles.bar,
            {
              height,
              backgroundColor: tint,
              opacity: listening ? 1 : waveform.restOpacity,
              transform: [{ scaleY: value }],
            },
          ]}
        />
      ))}
    </View>
  );
}

const styles = StyleSheet.create({
  row: {
    flexDirection: 'row',
    alignItems: 'flex-end',
    justifyContent: 'center',
    gap: waveform.barGap,
  },
  bar: {
    width: waveform.barWidth,
    borderRadius: waveform.barWidth,
    // Bottom origin matches the web `.office-launch__bars` canon, so the row
    // grows up off a baseline instead of blooming from its middle.
    transformOrigin: 'bottom',
  },
  reducedTrack: {
    width: waveform.barWidth * waveform.barCount + waveform.barGap * (waveform.barCount - 1),
    borderRadius: waveform.barWidth,
  },
});
