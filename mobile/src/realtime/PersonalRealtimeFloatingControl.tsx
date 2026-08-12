import React from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { SymbolView } from 'expo-symbols';
import { Waveform } from '../components/Waveform';
import { Glass } from '../theme/glass';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import type { PersonalRealtimeStatus } from './personalRealtimeProtocol';
import type { PersonalRealtimeController } from './PersonalRealtimeContext';

const statusLabels: Record<Exclude<PersonalRealtimeStatus, 'idle'>, string> = {
  connecting: 'Connecting',
  listening: 'Listening',
  hearing: 'Hearing you',
  thinking: 'Thinking',
  talking: 'Talking',
  acting: 'Acting',
  error: 'Needs attention',
};

export function PersonalRealtimeFloatingControl({
  realtime,
  onOpenThread,
}: {
  realtime: PersonalRealtimeController;
  onOpenThread?: (threadId: string) => void;
}) {
  if (realtime.status === 'idle') return null;
  const label = statusLabels[realtime.status];
  const canOpenThread = Boolean(realtime.threadId && onOpenThread);
  const waveformColor = realtime.status === 'error' ? colors.danger : colors.ember;

  return (
    <Glass
      accessibilityLiveRegion="polite"
      radius={radius.full}
      interactive
      tint={realtime.status === 'error' ? colors.danger : undefined}
      style={styles.control}
    >
      <Pressable
        accessibilityHint={canOpenThread ? 'Opens this voice conversation.' : undefined}
        accessibilityLabel={`${label}${canOpenThread ? '. Open voice conversation' : ''}`}
        accessibilityRole={canOpenThread ? 'button' : 'text'}
        disabled={!canOpenThread}
        onPress={() => {
          if (realtime.threadId) onOpenThread?.(realtime.threadId);
        }}
        style={({ pressed }) => [styles.status, pressed && styles.pressed]}
      >
        <View style={styles.waveform}>
          <Waveform
            color={waveformColor}
            height={20}
            listening={realtime.active && realtime.status !== 'connecting'}
            scale={0.42}
            trace={realtime.trace}
          />
        </View>
        <Text maxFontSizeMultiplier={1.6} numberOfLines={1} style={styles.label}>
          {label}
        </Text>
      </Pressable>
      <Pressable
        accessibilityLabel="End Scout voice"
        accessibilityRole="button"
        hitSlop={4}
        onPress={() => {
          void realtime.stop(realtime.status === 'error' ? 'cancelled' : 'completed');
        }}
        style={({ pressed }) => [styles.close, pressed && styles.pressed]}
      >
        <SymbolView name="xmark" size={14} tintColor={colors.text2} />
      </Pressable>
    </Glass>
  );
}

const styles = StyleSheet.create({
  control: {
    minHeight: 48,
    maxWidth: 224,
    flexDirection: 'row',
    alignItems: 'center',
    borderRadius: radius.full,
    borderCurve: 'continuous',
    overflow: 'hidden',
  },
  status: {
    minHeight: 48,
    minWidth: hitMin,
    flexShrink: 1,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    paddingLeft: space[3],
    paddingRight: space[2],
  },
  waveform: {
    width: 42,
    alignItems: 'center',
    overflow: 'hidden',
  },
  label: {
    ...type.captionMedium,
    color: colors.text1,
    flexShrink: 1,
  },
  close: {
    width: hitMin,
    height: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    borderCurve: 'continuous',
  },
  pressed: {
    opacity: 0.82,
    transform: [{ scale: 0.96 }],
  },
});
