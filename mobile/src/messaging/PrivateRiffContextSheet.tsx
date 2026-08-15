import React from 'react';
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';

import type { PrivateRiffBinding } from '../api/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import {
  privateRiffCheckpointSummary,
  privateRiffHasUpdates,
  privateRiffPacificDateTime,
  privateRiffSourceTitle,
} from './privateRiff';

type Props = {
  visible: boolean;
  riff: PrivateRiffBinding | null;
  refreshing: boolean;
  error?: string;
  onClose: () => void;
  onRefresh: () => void;
};

export function PrivateRiffContextSheet({ visible, riff, refreshing, error, onClose, onRefresh }: Props) {
  if (!riff) return null;
  const hasUpdates = privateRiffHasUpdates(riff);
  const updateCount = Math.max(0, Number(riff.newMessageCount ?? 0));
  return (
    <Modal
      visible={visible}
      animationType="slide"
      presentationStyle="pageSheet"
      onRequestClose={onClose}
    >
      <SafeAreaView style={styles.safe} edges={['top', 'bottom', 'left', 'right']}>
        <View style={styles.header}>
          <View style={styles.heading}>
            <Text style={styles.eyebrow}>PRIVATE RIFF</Text>
            <Text accessibilityRole="header" style={styles.title}>Context checkpoint</Text>
          </View>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Close context checkpoint"
            hitSlop={8}
            onPress={onClose}
            style={({ pressed }) => [styles.close, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" size={16} tintColor={colors.text2} />
          </Pressable>
        </View>
        <ScrollView contentInsetAdjustmentBehavior="automatic" contentContainerStyle={styles.content}>
          <View accessibilityRole="summary" style={styles.summary}>
            <SymbolView name="guitars.fill" size={17} tintColor={colors.emberText} />
            <View style={styles.summaryCopy}>
              <Text style={styles.summaryTitle}>{privateRiffCheckpointSummary(riff)}</Text>
              <Text style={styles.summaryBody}>This private conversation uses an immutable, server-authorized snapshot. New public messages never enter silently.</Text>
            </View>
          </View>

          <View style={styles.facts}>
            <Fact label="Source" value={privateRiffSourceTitle(riff)} />
            <Fact label="Through" value={`${riff.throughAuthorName || 'Channel message'} · ${privateRiffPacificDateTime(riff.throughCreatedAt)}`} />
            <Fact label="Coverage" value={`${riff.messageCount} ${riff.messageCount === 1 ? 'message' : 'messages'} · revision ${riff.contextRevision}`} />
            <Fact label="Agent" value={riff.agentName || 'Scout'} />
            <Fact label="Captured" value={privateRiffPacificDateTime(riff.capturedAt)} />
            <Fact label="Brain" value={privateRiffPacificDateTime(riff.brainCapturedAt)} />
          </View>

          {!riff.sourceAvailable ? (
            <View accessibilityRole="alert" style={styles.unavailable}>
              <Text style={styles.unavailableTitle}>Source unavailable</Text>
              <Text style={styles.unavailableBody}>{riff.unavailableReason || 'This channel checkpoint can no longer be authorized.'}</Text>
            </View>
          ) : null}
          {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
        </ScrollView>
        <View style={styles.footer}>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={hasUpdates ? `Update context with ${updateCount} new messages` : 'Update context'}
            accessibilityHint="Creates a new immutable checkpoint after reauthorizing the source channel"
            accessibilityState={{ disabled: refreshing || !riff.sourceAvailable }}
            disabled={refreshing || !riff.sourceAvailable}
            onPress={onRefresh}
            style={({ pressed }) => [styles.refresh, pressed && styles.pressed, (refreshing || !riff.sourceAvailable) && styles.disabled]}
          >
            {refreshing ? <ActivityIndicator color={colors.onAccent} size="small" /> : <SymbolView name="arrow.clockwise" size={16} tintColor={colors.onAccent} />}
            <Text style={styles.refreshText}>{refreshing ? 'Updating…' : hasUpdates ? `Update with ${updateCount} new` : 'Refresh context'}</Text>
          </Pressable>
        </View>
      </SafeAreaView>
    </Modal>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <View style={styles.fact}>
      <Text style={styles.factLabel}>{label}</Text>
      <Text style={styles.factValue}>{value}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  header: { minHeight: 58, flexDirection: 'row', alignItems: 'center', paddingHorizontal: space[4], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  heading: { flex: 1, minWidth: 0 },
  eyebrow: { ...type.label, color: colors.emberText },
  title: { ...type.headline, color: colors.text1 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  content: { gap: space[5], padding: space[5] },
  summary: { flexDirection: 'row', gap: space[3], padding: space[4], borderRadius: radius.lg, backgroundColor: colors.emberSoft },
  summaryCopy: { flex: 1, minWidth: 0, gap: space[1] },
  summaryTitle: { ...type.bodyMedium, color: colors.text1 },
  summaryBody: { ...type.bodySm, color: colors.text2 },
  facts: { borderRadius: radius.lg, overflow: 'hidden', backgroundColor: colors.surface1 },
  fact: { minHeight: 60, gap: 3, justifyContent: 'center', paddingHorizontal: space[4], paddingVertical: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  factLabel: { ...type.label, color: colors.text3, textTransform: 'uppercase' },
  factValue: { ...type.body, color: colors.text1 },
  unavailable: { gap: space[1], padding: space[4], borderRadius: radius.lg, backgroundColor: colors.surface1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.danger },
  unavailableTitle: { ...type.bodyMedium, color: colors.danger },
  unavailableBody: { ...type.bodySm, color: colors.text2 },
  error: { ...type.bodySm, color: colors.danger },
  footer: { padding: space[4], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  refresh: { minHeight: 52, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], borderRadius: radius.full, backgroundColor: colors.accent },
  refreshText: { ...type.button, color: colors.onAccent },
  pressed: { opacity: 0.8, transform: [{ scale: 0.98 }] },
  disabled: { opacity: 0.4 },
});
