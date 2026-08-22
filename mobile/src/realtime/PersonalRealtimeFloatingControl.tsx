import React from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { SymbolView } from 'expo-symbols';
import { Waveform } from '../components/Waveform';
import { Glass } from '../theme/glass';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { waveformRowWidth } from '../theme/waveformGeometry';
import type { PersonalRealtimeController } from './PersonalRealtimeContext';
import { runPersonalRealtimeTap } from './personalRealtimeTap';
import { personalRealtimeIslandModel } from './personalRealtimeIslandModel';
import { safePersonalRealtimeErrorMessage } from './personalRealtimeProtocol';

export const PERSONAL_REALTIME_ISLAND_WAVEFORM_SCALE = 0.42;
export const PERSONAL_REALTIME_ISLAND_WAVEFORM_WIDTH = waveformRowWidth(
  PERSONAL_REALTIME_ISLAND_WAVEFORM_SCALE,
);

export function PersonalRealtimeFloatingControl({
  realtime,
  onOpenThread,
  startAllowed = true,
}: {
  realtime: PersonalRealtimeController;
  onOpenThread?: (threadId: string) => void;
  startAllowed?: boolean;
}) {
  if (
    (!realtime.enabled || !startAllowed)
    && realtime.status === 'idle'
    && !realtime.tearingDown
  ) return null;
  const island = personalRealtimeIslandModel({
    enabled: realtime.enabled && startAllowed,
    status: realtime.status,
    threadId: realtime.threadId,
    canOpenThread: startAllowed && Boolean(onOpenThread),
    tearingDown: realtime.tearingDown,
  });
  const canOpenThread = island.action === 'open_thread';
  const primaryEnabled = island.action !== 'wait';
  const waveformColor = realtime.status === 'error'
    ? colors.danger
    : realtime.status === 'idle'
      ? colors.text2
      : colors.ember;

  if (realtime.status === 'error') {
    const errorMessage = safePersonalRealtimeErrorMessage(realtime.error);
    const retryEnabled = island.action === 'retry';
    return (
      <Glass
        accessibilityLiveRegion="assertive"
        radius={radius.xl}
        interactive
        tint={colors.danger}
        style={[styles.control, styles.errorControl]}
      >
        <Text
          accessibilityRole="alert"
          maxFontSizeMultiplier={1.6}
          numberOfLines={3}
          style={styles.errorMessage}
        >
          {errorMessage}
        </Text>
        <View style={styles.errorActions}>
          <Pressable
            accessibilityHint="Closes the failed connection, checks current availability, then tries again."
            accessibilityLabel="Try again"
            accessibilityRole="button"
            accessibilityState={{ disabled: !retryEnabled }}
            disabled={!retryEnabled}
            onPress={() => { void runPersonalRealtimeTap(realtime); }}
            style={({ pressed }) => [styles.errorRetry, pressed && styles.pressed]}
          >
            <SymbolView name="arrow.clockwise" size={14} tintColor={colors.text1} />
            <Text maxFontSizeMultiplier={1.6} style={styles.errorActionLabel}>Try again</Text>
          </Pressable>
          <Pressable
            accessibilityHint="Stops and closes this voice session."
            accessibilityLabel="Stop Scout voice"
            accessibilityRole="button"
            onPress={() => { void realtime.stop('cancelled'); }}
            style={({ pressed }) => [styles.errorStop, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" size={14} tintColor={colors.text2} />
            <Text maxFontSizeMultiplier={1.6} style={styles.errorStopLabel}>Stop</Text>
          </Pressable>
        </View>
      </Glass>
    );
  }

  return (
    <Glass
      accessibilityLiveRegion="polite"
      radius={radius.full}
      interactive
      style={styles.control}
    >
      <Pressable
        accessibilityHint={island.action === 'start'
          ? 'Starts a private, full-duplex conversation with Scout and saves it in your private chat.'
          : island.action === 'retry'
            ? 'Closes the failed private voice connection, then tries again.'
            : canOpenThread
              ? 'Opens the exact private chat saved for this voice conversation.'
              : undefined}
        accessibilityLabel={`${island.label}${canOpenThread ? '. Open private conversation' : ''}`}
        accessibilityRole="button"
        accessibilityState={{ disabled: !primaryEnabled }}
        disabled={!primaryEnabled}
        onPress={() => {
          if (island.action === 'start' || island.action === 'retry') {
            void runPersonalRealtimeTap(realtime);
          } else if (island.action === 'open_thread' && realtime.threadId) {
            onOpenThread?.(realtime.threadId);
          }
        }}
        style={({ pressed }) => [styles.status, pressed && styles.pressed]}
      >
        <View style={styles.waveform}>
          <Waveform
            color={waveformColor}
            height={20}
            listening={realtime.active && !['connecting', 'thinking', 'acting'].includes(realtime.status)}
            scale={PERSONAL_REALTIME_ISLAND_WAVEFORM_SCALE}
            trace={realtime.trace}
          />
        </View>
        <Text maxFontSizeMultiplier={1.6} numberOfLines={1} style={styles.label}>
          {island.label}
        </Text>
      </Pressable>
      {island.showClose ? (
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
      ) : null}
    </Glass>
  );
}

const styles = StyleSheet.create({
  control: {
    minHeight: 48,
    maxWidth: 240,
    flexDirection: 'row',
    alignItems: 'center',
    borderRadius: radius.full,
    borderCurve: 'continuous',
    overflow: 'hidden',
  },
  errorControl: {
    width: 300,
    maxWidth: 300,
    paddingTop: space[3],
    flexDirection: 'column',
    alignItems: 'stretch',
  },
  errorMessage: {
    ...type.caption,
    color: colors.text1,
    paddingHorizontal: space[3],
  },
  errorActions: {
    minHeight: 48,
    flexDirection: 'row',
    alignItems: 'center',
  },
  errorRetry: {
    minHeight: 48,
    minWidth: hitMin,
    flex: 1,
    paddingHorizontal: space[3],
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
  },
  errorActionLabel: {
    ...type.captionMedium,
    color: colors.text1,
  },
  errorStop: {
    minHeight: 48,
    minWidth: 84,
    paddingHorizontal: space[3],
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[2],
  },
  errorStopLabel: {
    ...type.captionMedium,
    color: colors.text2,
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
    width: PERSONAL_REALTIME_ISLAND_WAVEFORM_WIDTH,
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
