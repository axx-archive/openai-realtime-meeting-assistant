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
  report?: {
    agentName: string;
    mode: string;
    status: string;
  };
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

function reportBodyWithoutDuplicateTitle(text: string, title: string): string {
  const match = /^#\s+(.+)\n+/u.exec(text.trimStart());
  if (!match) return text;
  const normalized = (value: string) => value.replace(/[*_`]/gu, '').replace(/\s+/gu, ' ').trim().toLowerCase();
  return normalized(match[1]) === normalized(title)
    ? text.trimStart().slice(match[0].length).trimStart()
    : text;
}

export function LongMessageSheet({ visible, text, authorName, scout, activity = false, report, onClose }: Props) {
  const displayedText = useMemo(
    () => report ? reportBodyWithoutDuplicateTitle(text, authorName) : text,
    [authorName, report, text],
  );
  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.sheet} edges={['left', 'right', 'bottom']}>
        <View style={styles.handle} />
        <View style={styles.header}>
          <View style={styles.headerCopy}>
            <Text style={[styles.eyebrow, scout && styles.eyebrowScout]}>{report ? 'STRIDE · DELIVERABLE' : activity ? 'SCOUT · ACTIVITY' : scout ? 'SCOUT RESPONSE' : 'MESSAGE'}</Text>
            <Text numberOfLines={1} style={styles.title}>{report ? `${report.agentName} · ${report.status}` : authorName}</Text>
          </View>
          <Pressable accessibilityRole="button" accessibilityLabel="Close" onPress={onClose} hitSlop={8} style={({ pressed }) => [styles.close, pressed && styles.closePressed]}>
            <SymbolView name="xmark" size={15} tintColor={colors.text2} />
          </Pressable>
        </View>
        <ScrollView contentInsetAdjustmentBehavior="automatic" showsVerticalScrollIndicator contentContainerStyle={[styles.content, report && styles.reportContent]}>
          {report ? (
            <View style={styles.reportHero}>
              <View style={styles.reportModeRow}>
                <View style={styles.reportSignal} />
                <Text style={styles.reportMode}>{report.mode.toUpperCase()} REPORT</Text>
              </View>
              <Text style={styles.reportTitle}>{authorName}</Text>
              <Text style={styles.reportByline}>Prepared by {report.agentName} · delivered to this conversation</Text>
            </View>
          ) : null}
          {scout ? <ScoutRichText text={displayedText} variant={report ? 'report' : 'message'} /> : <PlainRichText text={displayedText} />}
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
  reportContent: { paddingTop: space[4], paddingBottom: space[10] * 2 },
  reportHero: { gap: space[3], marginBottom: space[6], paddingBottom: space[6], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  reportModeRow: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  reportSignal: { width: 24, height: 3, borderRadius: radius.full, backgroundColor: colors.ember },
  reportMode: { ...type.label, color: colors.emberText, letterSpacing: 0.7 },
  reportTitle: { ...type.title1, color: colors.text1, fontSize: 29, lineHeight: 34, letterSpacing: -0.7 },
  reportByline: { ...type.caption, color: colors.text3 },
  fullBody: { ...type.body, color: colors.text1 },
  link: { color: colors.info, textDecorationLine: 'underline' },
  mention: { ...type.bodyMedium, color: colors.info },
  mentionScout: { color: colors.emberText },
});
