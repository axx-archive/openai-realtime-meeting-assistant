import React, { useMemo } from 'react';
import { Modal, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SymbolView } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';
import * as Linking from 'expo-linking';

import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { parseMessageTextSegments } from './messagePresentation';
import { ScoutRichText } from './ScoutRichText';

type Props = {
  visible: boolean;
  text: string;
  authorName: string;
  scout: boolean;
  activity?: boolean;
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

export function LongMessageSheet({ visible, text, authorName, scout, activity = false, onClose }: Props) {
  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.sheet} edges={['left', 'right', 'bottom']}>
        <View style={styles.handle} />
        <View style={styles.header}>
          <View style={styles.headerCopy}>
            <Text style={[styles.eyebrow, scout && styles.eyebrowScout]}>{activity ? 'SCOUT · ACTIVITY' : scout ? 'SCOUT RESPONSE' : 'MESSAGE'}</Text>
            <Text numberOfLines={1} style={styles.title}>{authorName}</Text>
          </View>
          <Pressable accessibilityRole="button" accessibilityLabel="Close" onPress={onClose} hitSlop={8} style={({ pressed }) => [styles.close, pressed && styles.closePressed]}>
            <SymbolView name="xmark" size={15} tintColor={colors.text2} />
          </Pressable>
        </View>
        <ScrollView contentInsetAdjustmentBehavior="automatic" showsVerticalScrollIndicator contentContainerStyle={styles.content}>
          {scout ? <ScoutRichText text={text} /> : <PlainRichText text={text} />}
        </ScrollView>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  sheet: { flex: 1, overflow: 'hidden', backgroundColor: colors.bgApp },
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
