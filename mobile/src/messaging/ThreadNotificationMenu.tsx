import React, { useEffect, useRef } from 'react';
import { Animated, Modal, Pressable, StyleSheet, Text, View } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';

import { useReduceMotion, duration, ease } from '../theme/motion';
import { colors, radius, shadow, space, type } from '../theme/tokens';

export type ThreadNotificationLevel = 'all' | 'mentions' | 'none';

const options: Array<{ level: ThreadNotificationLevel; icon: SFSymbol; title: string; detail: string }> = [
  { level: 'all', icon: 'bell.fill', title: 'All', detail: 'Every new message' },
  { level: 'mentions', icon: 'at', title: 'Mentions', detail: 'Only when someone tags you' },
  { level: 'none', icon: 'bell.slash.fill', title: 'None', detail: 'No notifications from this channel' },
];

type Props = {
  visible: boolean;
  level: ThreadNotificationLevel;
  busy: boolean;
  onClose: () => void;
  onChange: (level: ThreadNotificationLevel) => void;
};

export function ThreadNotificationMenu({ visible, level, busy, onClose, onChange }: Props) {
  const reduced = useReduceMotion();
  const progress = useRef(new Animated.Value(0)).current;
  useEffect(() => {
    if (!visible) { progress.setValue(0); return; }
    Animated.timing(progress, { toValue: 1, duration: reduced ? 0 : duration.med, easing: ease, useNativeDriver: true }).start();
  }, [progress, reduced, visible]);

  return (
    <Modal visible={visible} transparent animationType="none" onRequestClose={onClose}>
      <View style={styles.modal}>
        <Pressable onPress={onClose} style={StyleSheet.absoluteFill} accessibilityLabel="Close notification options">
          <Animated.View style={[StyleSheet.absoluteFill, styles.scrim, { opacity: progress }]} />
        </Pressable>
        <Animated.View style={[styles.menu, { opacity: progress, transform: [{ translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [-8, 0] }) }, { scale: progress.interpolate({ inputRange: [0, 1], outputRange: [0.96, 1] }) }] }]}>
          <Text style={styles.eyebrow}>Notify me about</Text>
          {options.map((option) => {
            const selected = option.level === level;
            return (
              <Pressable
                key={option.level}
                accessibilityRole="radio"
                accessibilityState={{ selected, disabled: busy }}
                disabled={busy}
                onPress={() => onChange(option.level)}
                style={({ pressed }) => [styles.option, selected && styles.optionSelected, pressed && styles.pressed]}
              >
                <View style={[styles.icon, selected && styles.iconSelected]}>
                  <SymbolView name={option.icon} tintColor={selected ? colors.onAccent : colors.text2} size={16} />
                </View>
                <View style={styles.copy}>
                  <Text style={styles.title}>{option.title}</Text>
                  <Text style={styles.detail}>{option.detail}</Text>
                </View>
                {selected ? <SymbolView name="checkmark" tintColor={colors.text1} size={15} /> : null}
              </Pressable>
            );
          })}
        </Animated.View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  modal: { flex: 1 },
  scrim: { backgroundColor: 'rgba(0,0,0,0.22)' },
  menu: {
    ...shadow.glass,
    position: 'absolute',
    top: 76,
    right: space[4],
    width: 292,
    padding: 6,
    borderRadius: radius.xl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    backgroundColor: colors.surface1,
  },
  eyebrow: { ...type.label, paddingHorizontal: space[3], paddingTop: space[2], paddingBottom: space[2], color: colors.text3, textTransform: 'uppercase' },
  option: { minHeight: 62, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[2], borderRadius: radius.lg },
  optionSelected: { backgroundColor: colors.accentSoft },
  pressed: { opacity: 0.72, transform: [{ scale: 0.98 }] },
  icon: { width: 36, height: 36, alignItems: 'center', justifyContent: 'center', borderRadius: 18, backgroundColor: colors.surface3 },
  iconSelected: { backgroundColor: colors.accent },
  copy: { flex: 1 },
  title: { ...type.bodyMedium, color: colors.text1 },
  detail: { ...type.caption, color: colors.text2 },
});
