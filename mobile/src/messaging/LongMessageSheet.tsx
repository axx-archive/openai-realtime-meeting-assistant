import React, { useEffect, useMemo, useRef } from 'react';
import { Animated, Modal, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SymbolView } from 'expo-symbols';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import * as Linking from 'expo-linking';

import { useReduceMotion, duration, ease } from '../theme/motion';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';
import { parseMessageTextSegments } from './messagePresentation';
import { ScoutRichText } from './ScoutRichText';

type Props = {
  visible: boolean;
  text: string;
  authorName: string;
  scout: boolean;
  onClose: () => void;
};

function PlainRichText({ text }: { text: string }) {
  const segments = useMemo(() => parseMessageTextSegments(text), [text]);
  return (
    <Text style={styles.fullBody}>
      {segments.map((segment, index) => {
        if (segment.kind === 'text') return <React.Fragment key={index}>{segment.text}</React.Fragment>;
        if (segment.kind === 'link') {
          return <Text key={index} accessibilityRole="link" onPress={() => void Linking.openURL(segment.url).catch(() => undefined)} style={styles.link}>{segment.text}</Text>;
        }
        return <Text key={index} style={[styles.mention, segment.scout && styles.mentionScout]}>{segment.text.replace(/^@/, '')}</Text>;
      })}
    </Text>
  );
}

export function LongMessageSheet({ visible, text, authorName, scout, onClose }: Props) {
  const insets = useSafeAreaInsets();
  const reduceMotion = useReduceMotion();
  const scrim = useRef(new Animated.Value(0)).current;
  const sheet = useRef(new Animated.Value(28)).current;

  useEffect(() => {
    if (!visible) return;
    scrim.setValue(0);
    sheet.setValue(reduceMotion ? 0 : 28);
    Animated.parallel([
      Animated.timing(scrim, { toValue: 1, duration: reduceMotion ? 1 : duration.fast, easing: ease, useNativeDriver: true }),
      Animated.timing(sheet, { toValue: 0, duration: reduceMotion ? 1 : duration.slow, easing: ease, useNativeDriver: true }),
    ]).start();
  }, [reduceMotion, scrim, sheet, visible]);

  return (
    <Modal visible={visible} transparent statusBarTranslucent animationType="none" onRequestClose={onClose}>
      <View style={styles.modal}>
        <Animated.View style={[StyleSheet.absoluteFill, styles.scrim, { opacity: scrim }]}>
          <Pressable accessibilityLabel="Close full message" onPress={onClose} style={StyleSheet.absoluteFill} />
        </Animated.View>
        <Animated.View style={[styles.sheet, { paddingBottom: Math.max(insets.bottom, space[4]), transform: [{ translateY: sheet }] }]}>
          <View style={styles.handle} />
          <View style={styles.header}>
            <View style={styles.headerCopy}>
              <Text style={[styles.eyebrow, scout && styles.eyebrowScout]}>{scout ? 'SCOUT RESPONSE' : 'MESSAGE'}</Text>
              <Text numberOfLines={1} style={styles.title}>{authorName}</Text>
            </View>
            <Pressable accessibilityRole="button" accessibilityLabel="Close" onPress={onClose} hitSlop={8} style={({ pressed }) => [styles.close, pressed && styles.closePressed]}>
              <SymbolView name="xmark" size={15} tintColor={colors.text2} />
            </Pressable>
          </View>
          <ScrollView showsVerticalScrollIndicator contentContainerStyle={styles.content}>
            {scout ? <ScoutRichText text={text} /> : <PlainRichText text={text} />}
          </ScrollView>
        </Animated.View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  modal: { flex: 1, justifyContent: 'flex-end' },
  scrim: { backgroundColor: colors.scrim },
  sheet: { ...shadow.glass, maxHeight: '88%', minHeight: '48%', overflow: 'hidden', borderTopLeftRadius: radius.xxl, borderTopRightRadius: radius.xxl, borderWidth: StyleSheet.hairlineWidth, borderBottomWidth: 0, borderColor: colors.glassBorder, backgroundColor: colors.surface1 },
  handle: { alignSelf: 'center', width: 36, height: 5, marginTop: space[2], borderRadius: radius.full, backgroundColor: colors.line2 },
  header: { minHeight: 70, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[5], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  headerCopy: { flex: 1 },
  eyebrow: { ...type.captionMedium, color: colors.text3, letterSpacing: 0.5 },
  eyebrowScout: { color: colors.emberText },
  title: { ...type.title2, color: colors.text1 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: hitMin / 2, backgroundColor: colors.surface3 },
  closePressed: { opacity: 0.7, transform: [{ scale: 0.96 }] },
  content: { paddingHorizontal: space[5], paddingTop: space[5], paddingBottom: space[10] },
  fullBody: { ...type.body, color: colors.text1 },
  link: { color: colors.info, textDecorationLine: 'underline' },
  mention: { ...type.bodyMedium, color: colors.info },
  mentionScout: { color: colors.emberText },
});
