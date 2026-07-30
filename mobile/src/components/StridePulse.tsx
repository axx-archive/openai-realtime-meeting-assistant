import React, { useEffect, useMemo, useRef } from 'react';
import { StyleSheet, View, type ColorValue } from 'react-native';
import Svg, { Path } from 'react-native-svg';
import { colors } from '../theme/tokens';
import { useReduceMotion } from '../theme/motion';
import {
  RATIO_OPEN,
  TILE_INSET,
  apertureAmplitude,
  aperturePathData,
} from '../theme/strideSignal';

/**
 * THE STRIDE SIGNAL, animating — canon: `docs/stride-signal-canon.md` §6.
 *
 * The talk-to-Scout centrepiece is not a level meter wearing our colour: it IS
 * the logo. One closed lens that OPENS because something is listening. Every
 * number comes from `theme/strideSignal`, the same print of
 * `scripts/stride-signal-geometry.mjs` that cuts the app icon — and it is
 * verified byte-identical to the desktop centrepiece's path, so the two surfaces
 * cannot disagree about what the mark is.
 *
 * The laws, enforced here rather than left to call sites:
 *
 *   - REST IS THE LOGO, EXACTLY. `listening === false` draws RATIO_IDLE to the
 *     number, with no ripple. Pressing the control never swaps one picture for
 *     another — it just opens. A resting state that is a slightly different
 *     drawing is a second logo, and a brand with two logos has none.
 *   - GREY AT REST, ORANGE ONLY WHILE LISTENING. Ember is earned: colour means
 *     something is happening. The cost, accepted knowingly, is that the resting
 *     home screen carries almost no brand presence.
 *   - AMPLITUDE, NEVER A KEYFRAME LOOP. The opening is a real microphone number.
 *     A mark that idles on a timer cannot answer "can you hear me?", which is the
 *     only question a voice interface is ever asked.
 *   - 8:1 IS A HARD FLOOR. Past it a lens reads as an eye, which is the wrong
 *     thing to be on a product that remembers what people said. The floor holds
 *     by construction — the ripple only ever closes the aperture — and the box
 *     below reserves room for the fully open cut so opening never clips.
 *   - REDUCED MOTION drops the gait and KEEPS the amplitude answer as a static
 *     opening. Dropping the response too would leave a Reduce Motion user
 *     holding a control that cannot answer the only question it exists for.
 */

/**
 * Frame budget for the path rewrite, ~30fps.
 *
 * The one place this deliberately departs from the desktop, which runs at full
 * rAF. On the web a path rewrite is a direct attribute write; here every rewrite
 * is a Fabric shadow-node mutation, so the cost per frame is not comparable and
 * 60 of them a second buys nothing. Nothing in the motion needs more: the ripple
 * travels at 1.9 rad/s (~0.3Hz) and the metering that drives the opening arrives
 * at ~14fps, so 30fps is comfortably clear of both.
 */
const FRAME_MS = 1000 / 30;

/**
 * Vertical slack in the drawn box, as a multiple of the fully open cut.
 *
 * Mirrors the desktop container (`--aperture-width / 8 * 1.5`). The fully open
 * mark is exactly `width / RATIO_OPEN` tall, so without slack the apex would sit
 * on the viewBox edge and lose half a pixel of antialiasing to the clip; the
 * idle mark occupies under a third of the box.
 */
const BOX_SLACK = 1.5;

type Props = {
  trace: readonly number[];
  listening: boolean;
  /**
   * The BOX — the mark's square tile, exactly as the icon generator means it.
   * The lens spans `TILE_INSET` (0.66) of it, so 391 draws a 258pt mark, which
   * is the presence the canvas hero is designed around. The rendered component
   * is only as tall as the aperture needs, never square.
   */
  size?: number;
  color?: ColorValue;
};

export function StridePulse({
  trace,
  listening,
  size = 391,
  color,
}: Props) {
  const reduceMotion = useReduceMotion();
  // Ember is earned: the mark lights only while the mic is actually open. At rest
  // it is text-2, exactly like the web's `.office-launch__bars`.
  const tint = color ?? (listening ? colors.ember : colors.text2);

  const width = size * TILE_INSET;
  // Rounded because it is a BOX, not geometry — the lens's own height comes from
  // `width` and the cut, so trimming the float tail here changes nothing but the
  // tidiness of the viewBox. The Svg's height and the viewBox height are the same
  // number, so the mark still draws at scale 1.
  const height = Math.round((width / RATIO_OPEN) * BOX_SLACK * 100) / 100;

  // ONE drive level for the whole body. The mark is a single thing, unlike a bar
  // meter where every bar is its own moment.
  const amplitude = apertureAmplitude(trace, listening);
  // Kept in a ref so the loop below reads the newest sample WITHOUT being torn
  // down and restarted on every ~70ms metering tick. Restarting it would reset
  // the ripple's clock 14 times a second, which would read as a stutter rather
  // than a travelling wave.
  const amplitudeRef = useRef(amplitude);
  amplitudeRef.current = amplitude;

  const path = useRef<Path | null>(null);

  /**
   * The path the mark HOLDS when the loop is not running.
   *
   * Under Reduce Motion this tracks amplitude — that response is information,
   * not decoration. Otherwise it is a constant, and deliberately so: a `d` that
   * tracked amplitude while the loop owns the path would have React overwrite
   * the loop's fresh frame with a 14fps-stale one, which is a visible snap
   * fourteen times a second.
   *
   * `apertureAmplitude` returns 0 when not listening, so this expression is the
   * exact logo at rest — the same float arithmetic as `lensPath(width,
   * RATIO_IDLE)`, not a JS approximation that happens to round to the same
   * place.
   */
  const heldAmplitude = reduceMotion ? amplitude : 0;
  const held = useMemo(
    () => aperturePathData(width, heldAmplitude, 0, false),
    [heldAmplitude, width],
  );

  useEffect(() => {
    if (!listening || reduceMotion) return;
    /**
     * THE DRIVE — one rAF loop, one path, mirroring the desktop exactly: the
     * opening and the ripple are computed together per frame, because they are
     * one geometry rather than a shape plus an effect.
     *
     * The clock is real wall time, not a frame counter, so a dropped frame costs
     * a frame of the ripple and never shifts its phase.
     */
    const began = Date.now();
    let frame = 0;
    let drawn = 0;
    const step = () => {
      frame = requestAnimationFrame(step);
      const now = Date.now();
      if (now - drawn < FRAME_MS) return;
      drawn = now;
      path.current?.setNativeProps({
        d: aperturePathData(width, amplitudeRef.current, (now - began) / 1000),
      });
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [listening, reduceMotion, width]);

  useEffect(() => {
    // While the drive is running it owns the path; stay out of its way.
    if (listening && !reduceMotion) return;
    /**
     * Back to the held cut, imperatively — and this write is load-bearing, not a
     * belt-and-braces duplicate of the rendered `d`.
     *
     * The rendered `d` is the SAME STRING before and after a listening spell, so
     * React sees no prop change on the way down and issues no native update,
     * which would leave the mark frozen at whatever the last frame drew. The
     * desktop has the identical trap and the identical cure
     * (`restoreStrideSignalIdlePath`). Effect ordering makes this safe: React
     * runs the drive's cleanup — cancelling the frame — before this body.
     */
    path.current?.setNativeProps({ d: held });
  }, [held, listening, reduceMotion]);

  /**
   * Memoised as an ELEMENT, not just a value.
   *
   * `trace` is a fresh array every metering tick, so this component re-renders
   * ~14 times a second while the drive is running. A re-rendered host node whose
   * props differ is CLONED by Fabric, and a clone is built from React's props —
   * which discards whatever `setNativeProps` wrote. Holding the identical
   * element across those renders makes React bail out of the subtree entirely,
   * so the drive's frames survive; it also skips react-native-svg's per-render
   * prop extraction. The deps are exactly the things that legitimately change
   * the drawing.
   */
  return useMemo(
    () => (
      // Decorative to a screen reader: the Dock announces "Listening" and a
      // duration in words, so the mark never streams geometry into the
      // accessibility tree.
      <View
        style={[styles.mark, { width, height }]}
        accessibilityElementsHidden
        importantForAccessibility="no-hide-descendants"
      >
        <Svg
          width={width}
          height={height}
          // y = 0 is the lens's own centre line, so the path data is the same
          // numbers every other surface draws. The box is the fully open cut
          // plus slack, which is what keeps opening from clipping.
          viewBox={`0 ${-height / 2} ${width} ${height}`}
        >
          <Path ref={path} d={held} fill={tint} />
        </Svg>
      </View>
    ),
    [held, height, tint, width],
  );
}

const styles = StyleSheet.create({
  mark: {
    // The mark has no interactions of its own — it sits inside the canvas's one
    // big tappable area and must never eat a tap meant for it.
    pointerEvents: 'none',
  },
});
