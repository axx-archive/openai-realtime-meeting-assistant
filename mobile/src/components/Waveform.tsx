import React, { useEffect, useRef } from 'react';
import { Animated, StyleSheet, View, type ColorValue } from 'react-native';
import { barScales } from '../voice/amplitude';
import { colors } from '../theme/tokens';
import { duration, ease, useReduceMotion } from '../theme/motion';
import { waveform } from '../theme/waveformGeometry';

/**
 * The instrument — design §3.
 *
 * These bars are a render of the microphone, not a keyframe loop. That is the
 * whole point: an animated waveform cannot tell you whether it is actually
 * hearing you, and "is this thing listening?" is the central anxiety of every
 * voice interface. This one answers continuously, by physics.
 *
 * Two laws are enforced here rather than left to call sites:
 *
 *   - Rest is STATIC (§8 law 1) — but static is not flat. At rest the row holds
 *     a sculpted silhouette, matching the web canon's fourteen varied resting
 *     heights. A row of identical ticks is an ellipsis, not a waveform.
 *   - Transforms only (§8 law 4). Bars have a fixed layout height and scale on
 *     Y; animating height would relayout the row on every sample.
 */

export type WaveformProps = {
  /** Rolling amplitude history, oldest first. Ignored while not listening. */
  trace: readonly number[];
  /** While false the bars hold the resting silhouette, unanimated. */
  listening: boolean;
  /** Ember while listening, muted ink at rest. Ember is earned (§8 law 2). */
  color?: ColorValue;
  height?: number;
  /** Scales bar width/gap for compact placements (the Dock, the composer). */
  scale?: number;
};

export function Waveform({
  trace,
  listening,
  color,
  height = waveform.height,
  scale = 1,
}: WaveformProps) {
  const reduceMotion = useReduceMotion();
  // text2, not text3. At 38% ink knocked back to 0.55 opacity the resting wave
  // sat around 21% black, which reads as a disabled control rather than the
  // hero of the screen. The web canon rests this at --text-2.
  const tint = color ?? (listening ? colors.ember : colors.text2);

  const targets = barScales(trace, listening);
  const values = useRef(targets.map((value) => new Animated.Value(value))).current;

  useEffect(() => {
    /**
     * While listening, push values STRAIGHT to native rather than starting
     * animations.
     *
     * Tweening each sample meant spawning 28 `Animated.timing`s every sampling
     * interval — ~280 animation starts a second, which is what produced RN's
     * "onAnimatedValueUpdate with no listeners" warning. A scrolling trace does
     * not need per-sample easing anyway: the motion the eye reads is the row
     * SHIFTING, and at this sample rate `setValue` is both smoother and
     * effectively free. Reduce Motion takes the same path, since amplitude
     * response is information rather than decoration.
     */
    if (listening || reduceMotion) {
      values.forEach((value, index) => value.setValue(targets[index] ?? 0));
      return;
    }
    // Settling back to rest is a single, rare transition — worth easing.
    Animated.parallel(
      values.map((value, index) =>
        Animated.timing(value, {
          toValue: targets[index] ?? 0,
          duration: duration.slow,
          easing: ease,
          useNativeDriver: true,
        }),
      ),
    ).start();
    // `targets` is a fresh array each render; its contents are the dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [targets.join(','), listening, reduceMotion]);

  return (
    // Decorative to a screen reader: the STATE is announced by the Dock, which
    // says "Listening" and a duration rather than streaming bar heights (§9.5).
    <View
      style={[styles.row, { height, gap: waveform.barGap * scale }]}
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
    >
      {values.map((value, index) => (
        <Animated.View
          key={index}
          style={[
            styles.bar,
            {
              height,
              width: waveform.barWidth * scale,
              borderRadius: waveform.barWidth * scale,
              backgroundColor: tint,
              opacity: listening ? 1 : 0.72,
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
    // Centre origin reads as an audio meter rather than a bar chart, and keeps
    // the row optically balanced against the greeting beneath it.
    alignItems: 'center',
    justifyContent: 'center',
  },
  bar: {
    transformOrigin: 'center',
  },
});
