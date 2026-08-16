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
  privateRiffDisplayedPassNumber,
  privateRiffFreshnessSummary,
  privateRiffPacificDateTime,
  privateRiffSourceTitle,
} from './privateRiff';

type Props = {
  visible: boolean;
  riff: PrivateRiffBinding | null;
  viewingEpisodeId?: string | null;
  error?: string;
  onClose: () => void;
  onViewEpisode: (episodeId: string) => void;
};

export function PrivateRiffContextSheet({ visible, riff, viewingEpisodeId, error, onClose, onViewEpisode }: Props) {
  if (!riff) return null;
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
              <Text style={styles.summaryBody}>{privateRiffFreshnessSummary(riff)}. Each episode keeps its own server-authorized checkpoint.</Text>
            </View>
          </View>

          <View style={styles.facts}>
            <Fact label="Source" value={privateRiffSourceTitle(riff)} />
            <Fact label="Through" value={`${riff.throughAuthorName || 'Channel message'} · ${privateRiffPacificDateTime(riff.throughCreatedAt)}`} />
            <Fact label="Coverage" value={`${riff.messageCount} ${riff.messageCount === 1 ? 'message' : 'messages'} · revision ${riff.contextRevision}`} />
            <Fact label="Episode" value={riff.activeEpisodeId ? `Pass ${privateRiffDisplayedPassNumber(riff)}` : 'Legacy Riff'} />
            <Fact label="Agent" value={riff.agentName || 'Scout'} />
            <Fact label="Captured" value={privateRiffPacificDateTime(riff.capturedAt)} />
            <Fact label="Brain" value={privateRiffPacificDateTime(riff.brainCapturedAt)} />
          </View>

          {riff.episodes && riff.episodes.length > 0 ? (
            <View style={styles.passes}>
              <View style={styles.passesHeading}>
                <Text accessibilityRole="header" style={styles.passesTitle}>Riff passes</Text>
                <Text style={styles.passesCount}>{riff.episodes.length}</Text>
              </View>
              {riff.episodes.map((episode, index) => {
                const pass = index + 1;
                const active = episode.id === riff.activeEpisodeId;
                const viewed = episode.id === (riff.viewedEpisodeId || riff.activeEpisodeId);
                const busy = Boolean(viewingEpisodeId);
                return (
                  <View key={episode.id} style={styles.passRow}>
                    <View style={styles.passCopy}>
                      <Text style={styles.passTitle}>Pass {pass}{active ? ' · Current' : viewed ? ' · Viewing' : ''}</Text>
                      <Text style={styles.passMeta}>
                        {privateRiffPacificDateTime(episode.throughCreatedAt ?? episode.createdAt)} · {episode.messageCount} source {episode.messageCount === 1 ? 'message' : 'messages'}
                      </Text>
                    </View>
                    {!viewed ? (
                      <Pressable
                        accessibilityRole="button"
                        accessibilityLabel={`View pass ${pass}`}
                        accessibilityHint="Opens that pass read-only without changing the current pass"
                        accessibilityState={{ disabled: busy || !riff.sourceAvailable }}
                        disabled={busy || !riff.sourceAvailable}
                        onPress={() => onViewEpisode(episode.id)}
                        style={({ pressed }) => [styles.resume, pressed && styles.pressed, (busy || !riff.sourceAvailable) && styles.disabled]}
                      >
                        {viewingEpisodeId === episode.id ? <ActivityIndicator color={colors.emberText} size="small" /> : <Text style={styles.resumeText}>View pass</Text>}
                      </Pressable>
                    ) : (
                      <View accessibilityLabel={`Pass ${pass}, ${active ? 'current' : 'viewing'}`} style={styles.currentBadge}>
                        <Text style={styles.currentText}>{active ? 'Current' : 'Viewing'}</Text>
                      </View>
                    )}
                  </View>
                );
              })}
            </View>
          ) : null}

          {!riff.sourceAvailable ? (
            <View accessibilityRole="alert" style={styles.unavailable}>
              <Text style={styles.unavailableTitle}>Source unavailable</Text>
              <Text style={styles.unavailableBody}>{riff.unavailableReason || 'This channel checkpoint can no longer be authorized.'}</Text>
            </View>
          ) : null}
          {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
        </ScrollView>
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
  passes: { overflow: 'hidden', borderRadius: radius.lg, backgroundColor: colors.surface1 },
  passesHeading: { minHeight: 48, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[4], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  passesTitle: { ...type.bodyMedium, flex: 1, color: colors.text1 },
  passesCount: { ...type.label, color: colors.text3 },
  passRow: { minHeight: 68, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[4], paddingVertical: space[2], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  passCopy: { flex: 1, minWidth: 0, gap: 2 },
  passTitle: { ...type.bodyMedium, color: colors.text1 },
  passMeta: { ...type.caption, color: colors.text3 },
  resume: { minWidth: 104, minHeight: hitMin, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[3], borderRadius: radius.full, backgroundColor: colors.emberSoft },
  resumeText: { ...type.button, color: colors.emberText },
  currentBadge: { minWidth: 72, minHeight: hitMin, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[3], borderRadius: radius.full, backgroundColor: colors.surface2 },
  currentText: { ...type.label, color: colors.text2 },
  error: { ...type.bodySm, color: colors.danger },
  pressed: { opacity: 0.8, transform: [{ scale: 0.98 }] },
  disabled: { opacity: 0.4 },
});
