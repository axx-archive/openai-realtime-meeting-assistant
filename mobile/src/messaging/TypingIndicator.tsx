import React, { useEffect, useRef } from 'react';
import { Animated, StyleSheet, Text, View } from 'react-native';

import { duration, ease, useReduceMotion } from '../theme/motion';
import { colors, radius, space, type } from '../theme/tokens';
import { ChatAvatar } from './ChatAvatar';
import { typingIndicatorLabel } from './chatRealtime';

export type TypingParticipant = {
  email: string;
  name: string;
  avatarDataURL?: string;
};

export function TypingIndicator({ participants }: { participants: readonly TypingParticipant[] }) {
  const reduced = useReduceMotion();
  const pulses = useRef([new Animated.Value(0), new Animated.Value(0), new Animated.Value(0)]).current;
  useEffect(() => {
    const animations = pulses.map((pulse, index) => Animated.loop(Animated.sequence([
      Animated.delay(index * 130),
      Animated.timing(pulse, { toValue: 1, duration: duration.fast, easing: ease, useNativeDriver: true }),
      Animated.timing(pulse, { toValue: 0, duration: duration.fast, easing: ease, useNativeDriver: true }),
      Animated.delay(520 - index * 130),
    ])));
    if (reduced) {
      pulses.forEach((pulse) => pulse.setValue(0));
      return;
    }
    animations.forEach((animation) => animation.start());
    return () => animations.forEach((animation) => animation.stop());
  }, [pulses, reduced]);
  if (participants.length === 0) return null;
  const visible = participants.slice(0, 3);
  return (
    <View accessibilityLabel={`${typingIndicatorLabel(participants.map((item) => item.name))}.`} style={styles.row}>
      <View style={styles.avatars}>
        {visible.map((participant, index) => (
          <View key={participant.email} style={[styles.avatar, { marginLeft: index ? -7 : 0, zIndex: visible.length - index }]}>
            <ChatAvatar name={participant.name} avatarDataURL={participant.avatarDataURL} size={24} />
          </View>
        ))}
      </View>
      <View style={styles.bubble}>
        <Text style={styles.label}>{typingIndicatorLabel(participants.map((item) => item.name))}</Text>
        <View accessibilityElementsHidden style={styles.dots}>
          {pulses.map((pulse, index) => (
            <Animated.View
              key={index}
              style={[
                styles.dot,
                reduced ? styles.dotReduced : {
                  opacity: pulse.interpolate({ inputRange: [0, 1], outputRange: [0.34, 0.82] }),
                  transform: [{ translateY: pulse.interpolate({ inputRange: [0, 1], outputRange: [0, -2] }) }],
                },
              ]}
            />
          ))}
        </View>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  row: { minHeight: 38, flexDirection: 'row', alignItems: 'flex-end', gap: space[2], paddingHorizontal: space[4], paddingTop: 4, paddingBottom: space[2] },
  avatars: { minWidth: 24, flexDirection: 'row', alignItems: 'center' },
  avatar: { borderRadius: 13, borderWidth: 1, borderColor: colors.bgApp },
  bubble: { flexDirection: 'row', alignItems: 'center', gap: 6, paddingHorizontal: 11, paddingVertical: 7, borderRadius: radius.full, backgroundColor: colors.surface1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  label: { ...type.caption, color: colors.text2 },
  dots: { height: 10, flexDirection: 'row', alignItems: 'center', gap: 3 },
  dot: { width: 4, height: 4, borderRadius: 2, backgroundColor: colors.text2 },
  dotReduced: { opacity: 0.5 },
});
