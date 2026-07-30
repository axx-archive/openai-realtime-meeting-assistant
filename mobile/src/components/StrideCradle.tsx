import React, { useEffect, useMemo, useRef } from 'react';
import { StyleSheet, View } from 'react-native';
import Svg, { Circle } from 'react-native-svg';
import { colors, ember } from '../theme/tokens';
import { useReduceMotion } from '../theme/motion';
import { aperture, apertureAmplitude } from '../theme/strideSignal';
import {
  createStrideCradleState,
  stepStrideCradle,
  strideCradleContactWeights,
  strideCradleTransferProgress,
  type StrideCradleSource,
} from '../theme/strideCradle';

/**
 * The Signal Cradle.
 *
 * Five floating masses turn microphone level into a literal equal-mass transfer:
 * the left edge hits three still centres, momentum exits through the far right,
 * then returns. No frame or suspension lines compete with the interaction.
 */
const BALL_COUNT = 5;
const FRAME_MS = 1000 / 30;

type Props = {
  trace: readonly number[];
  listening: boolean;
  source?: StrideCradleSource;
  size?: number;
};

type Point = { x: number; y: number };

function bobPoint(anchorX: number, anchorY: number, length: number, angle: number): Point {
  return {
    x: anchorX + Math.sin(angle) * length,
    y: anchorY + Math.cos(angle) * length,
  };
}

export function StrideCradle({ trace, listening, source = 'human', size = 391 }: Props) {
  const reduceMotion = useReduceMotion();
  const width = size * 0.8;
  const height = width * 0.42;
  const radius = width * 0.055;
  const spacing = radius * 2;
  const anchorY = height * 0.09;
  const restY = height * 0.72;
  const length = restY - anchorY;
  const startX = (width - spacing * (BALL_COUNT - 1)) / 2;
  const anchors = useMemo(
    () => Array.from({ length: BALL_COUNT }, (_, index) => startX + index * spacing),
    [spacing, startX],
  );

  const meteredLevel = apertureAmplitude(trace, listening);
  const energy = meteredLevel <= aperture.idleFloor
    ? 0
    : (meteredLevel - aperture.idleFloor) / (1 - aperture.idleFloor);
  const amplitudeRef = useRef(energy);
  amplitudeRef.current = energy;
  const sourceRef = useRef(source);
  sourceRef.current = source;
  const physicsRef = useRef(createStrideCradleState(energy, source));
  const ballRefs = useRef<Array<Circle | null>>([]);
  const haloRefs = useRef<Array<Circle | null>>([]);
  const energyRefs = useRef<Array<Circle | null>>([]);

  useEffect(() => {
    const draw = (level: number, still: boolean) => {
      const physics = physicsRef.current;
      const transferProgress = still ? null : strideCradleTransferProgress(physics);
      const contactWeights = still
        ? Array.from({ length: BALL_COUNT }, () => 0)
        : strideCradleContactWeights(physics, BALL_COUNT);
      const transferStrength = 0.28 + 0.72 * Math.sqrt(level);
      const handoff = transferProgress === null
        ? 1
        : Math.max(0, Math.min(1, (transferProgress - 0.5) / 0.5));
      const points = anchors.map((anchorX, index) => {
        const angle = index === 0
          ? still ? 0 : physics.leftAngle
          : index === BALL_COUNT - 1
            ? still ? 0 : physics.rightAngle
            : 0;
        return bobPoint(anchorX, anchorY, length, angle);
      });
      const outgoingIndex = physics.transferDirection > 0 ? BALL_COUNT - 1 : 0;

      anchors.forEach((anchorX, index) => {
        const point = points[index];
        const transferActive = transferProgress !== null;
        const isSourceEdge = sourceRef.current === 'human'
          ? index === 0
          : index === BALL_COUNT - 1;
        const isMovingEdge = index === 0
          ? Math.abs(physics.leftAngle) > 0.001 || Math.abs(physics.leftVelocity) > 0.001
          : index === BALL_COUNT - 1
            ? Math.abs(physics.rightAngle) > 0.001 || Math.abs(physics.rightVelocity) > 0.001
            : false;
        const activeEnergy = 0.76 + level * 0.24;
        const edgeEnergy = still
          ? isSourceEdge ? level * 0.3 : 0
          : isMovingEdge
            ? transferActive && index === outgoingIndex
              ? activeEnergy * handoff
              : transferActive ? 0 : activeEnergy
            : 0;
        const contactEnergy = contactWeights[index] * 0.92 * transferStrength;
        const visibleEnergy = Math.max(edgeEnergy, contactEnergy);
        ballRefs.current[index]?.setNativeProps({
          cx: point.x,
          cy: point.y,
          opacity: 0.5,
        });
        energyRefs.current[index]?.setNativeProps({
          cx: point.x,
          cy: point.y,
          opacity: visibleEnergy,
        });
        haloRefs.current[index]?.setNativeProps({
          cx: point.x,
          cy: point.y,
          opacity: visibleEnergy * 0.14,
        });
      });

    };

    if (!listening) {
      physicsRef.current = createStrideCradleState(amplitudeRef.current, sourceRef.current);
      draw(amplitudeRef.current, true);
      return;
    }

    if (reduceMotion) {
      let frame = 0;
      let drawn = 0;
      const step = () => {
        frame = requestAnimationFrame(step);
        const now = Date.now();
        if (now - drawn < FRAME_MS) return;
        drawn = now;
        draw(amplitudeRef.current, true);
      };
      frame = requestAnimationFrame(step);
      return () => cancelAnimationFrame(frame);
    }

    physicsRef.current = createStrideCradleState(amplitudeRef.current, sourceRef.current);
    const began = Date.now();
    let frame = 0;
    let drawn = 0;
    let previous = began;
    const step = () => {
      frame = requestAnimationFrame(step);
      const now = Date.now();
      if (now - drawn < FRAME_MS) return;
      drawn = now;
      stepStrideCradle(
        physicsRef.current,
        (now - previous) / 1000,
        amplitudeRef.current,
        sourceRef.current,
      );
      previous = now;
      draw(amplitudeRef.current, false);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [anchorY, anchors, length, listening, radius, reduceMotion]);

  return useMemo(
    () => (
      <View
        style={[styles.cradle, { width, height }]}
        accessibilityElementsHidden
        importantForAccessibility="no-hide-descendants"
      >
        <Svg width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
          {anchors.map((anchorX, index) => (
            <React.Fragment key={index}>
              <Circle
                ref={(node) => { ballRefs.current[index] = node; }}
                cx={anchorX}
                cy={restY}
                r={radius}
                fill={colors.text2}
                opacity={0.5}
              />
              <Circle
                ref={(node) => { haloRefs.current[index] = node; }}
                cx={anchorX}
                cy={restY}
                r={radius * 1.16}
                fill={ember[500]}
                opacity={0}
              />
              <Circle
                ref={(node) => { energyRefs.current[index] = node; }}
                cx={anchorX}
                cy={restY}
                r={radius}
                fill={ember[500]}
                opacity={0}
              />
            </React.Fragment>
          ))}
        </Svg>
      </View>
    ),
    [anchorY, anchors, height, listening, radius, restY, width],
  );
}

const styles = StyleSheet.create({
  cradle: {
    pointerEvents: 'none',
  },
});
