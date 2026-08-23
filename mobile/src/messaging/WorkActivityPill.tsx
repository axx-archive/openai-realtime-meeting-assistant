import React, { memo, useCallback, useMemo, useRef } from 'react';
import {
  Animated,
  findNodeHandle,
  PanResponder,
  Pressable,
  StyleSheet,
  Text,
  useWindowDimensions,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';

import type { ScoutMessage } from '../api/types';
import { colors, radius, space, type } from '../theme/tokens';
import { workActivityPillLabel } from './workPresentation';
import { workActivityThreadRef } from './workTimeline';

type Props = {
  message: ScoutMessage;
  stacked: boolean;
  reduceMotion: boolean;
  onOpen: (returnFocusHandle?: number) => void;
  onDismiss: () => void;
};

const swipeIntentDistance = 10;
const swipeDismissDistance = 72;
const swipeHorizontalBias = 1.45;
const swipeDismissVelocity = 0.55;

export const WorkActivityPill = memo(function WorkActivityPill({
  message,
  stacked,
  reduceMotion,
  onOpen,
  onDismiss,
}: Props) {
  const window = useWindowDimensions();
  const openRef = useRef<View>(null);
  const translateX = useRef(new Animated.Value(0)).current;
  const opacity = useRef(new Animated.Value(1)).current;
  const dismissingRef = useRef(false);

  const restore = useCallback(() => {
    if (reduceMotion) {
      translateX.setValue(0);
      opacity.setValue(1);
      return;
    }
    Animated.parallel([
      Animated.spring(translateX, {
        toValue: 0,
        damping: 20,
        stiffness: 260,
        mass: 0.8,
        useNativeDriver: true,
      }),
      Animated.timing(opacity, {
        toValue: 1,
        duration: 120,
        useNativeDriver: true,
      }),
    ]).start();
  }, [opacity, reduceMotion, translateX]);

  const dismiss = useCallback((direction: -1 | 1 = 1) => {
    if (dismissingRef.current) return;
    dismissingRef.current = true;
    if (reduceMotion) {
      onDismiss();
      return;
    }
    Animated.parallel([
      Animated.timing(translateX, {
        toValue: direction * (window.width + 80),
        duration: 180,
        useNativeDriver: true,
      }),
      Animated.timing(opacity, {
        toValue: 0,
        duration: 140,
        useNativeDriver: true,
      }),
    ]).start(({ finished }) => {
      if (finished) onDismiss();
      else {
        dismissingRef.current = false;
        restore();
      }
    });
  }, [onDismiss, opacity, reduceMotion, restore, translateX, window.width]);

  const pan = useMemo(() => PanResponder.create({
    onMoveShouldSetPanResponder: (_event, gesture) => (
      Math.abs(gesture.dx) > swipeIntentDistance
      && Math.abs(gesture.dx) > Math.abs(gesture.dy) * swipeHorizontalBias
    ),
    onPanResponderMove: (_event, gesture) => {
      translateX.setValue(gesture.dx);
      opacity.setValue(Math.max(0.46, 1 - Math.abs(gesture.dx) / Math.max(window.width, 1)));
    },
    onPanResponderRelease: (_event, gesture) => {
      const direction: -1 | 1 = gesture.dx < 0 ? -1 : 1;
      if (Math.abs(gesture.dx) >= swipeDismissDistance || Math.abs(gesture.vx) >= swipeDismissVelocity) {
        dismiss(direction);
        return;
      }
      restore();
    },
    onPanResponderTerminate: restore,
  }), [dismiss, opacity, restore, translateX, window.width]);

  const work = workActivityThreadRef(message);
  const agentName = String(work?.agentName ?? 'Scout').trim() || 'Scout';
  const label = workActivityPillLabel(work);

  return (
    <Animated.View
      {...pan.panHandlers}
      style={[
        styles.pill,
        stacked && styles.pillStacked,
        { opacity, transform: [{ translateX }] },
      ]}
    >
      <Pressable
        ref={openRef}
        accessibilityRole="button"
        accessibilityLabel={`${agentName}, ${label}`}
        accessibilityHint="Opens work activity. Swipe horizontally or use Dismiss status to hide this update for you."
        focusable
        onPress={() => onOpen(findNodeHandle(openRef.current) ?? undefined)}
        style={({ pressed }) => [
          styles.open,
          pressed && styles.pressed,
        ]}
      >
        <View style={styles.signal}>
          <View style={styles.barShort} />
          <View style={styles.barTall} />
          <View style={styles.barMid} />
        </View>
        <Text
          maxFontSizeMultiplier={1.8}
          numberOfLines={stacked ? 2 : 1}
          style={styles.text}
        >
          {label}
        </Text>
        <SymbolView name="chevron.up" tintColor={colors.text3} size={12} />
      </Pressable>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel="Dismiss work status"
        accessibilityHint="Hides this update only for you. A new run or important status change will appear again."
        hitSlop={2}
        onPress={() => dismiss(1)}
        style={({ pressed }) => [styles.close, pressed && styles.closePressed]}
      >
        <SymbolView name="xmark" tintColor={colors.text3} size={11} />
      </Pressable>
    </Animated.View>
  );
});

const styles = StyleSheet.create({
  pill: {
    minHeight: 46,
    flexDirection: 'row',
    alignItems: 'center',
    marginHorizontal: space[4],
    marginBottom: space[2],
    paddingLeft: space[1],
    paddingRight: 2,
    borderRadius: radius.lg,
    borderCurve: 'continuous',
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    backgroundColor: colors.surface2,
  },
  pillStacked: { minHeight: 56 },
  open: {
    minWidth: 0,
    minHeight: 44,
    flex: 1,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    paddingLeft: space[2],
  },
  pressed: { opacity: 0.76, transform: [{ scale: 0.96 }] },
  signal: {
    width: 26,
    height: 26,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 2,
    borderRadius: radius.sm,
    backgroundColor: colors.emberSoft,
  },
  barShort: { width: 2, height: 7, borderRadius: radius.full, backgroundColor: colors.emberText },
  barTall: { width: 2, height: 13, borderRadius: radius.full, backgroundColor: colors.emberText },
  barMid: { width: 2, height: 9, borderRadius: radius.full, backgroundColor: colors.emberText },
  text: { ...type.captionMedium, minWidth: 0, flex: 1, color: colors.text1, fontVariant: ['tabular-nums'] },
  close: {
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    borderCurve: 'continuous',
  },
  closePressed: { backgroundColor: colors.surface3, transform: [{ scale: 0.96 }] },
});
