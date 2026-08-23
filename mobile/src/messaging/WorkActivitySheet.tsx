import React, { useEffect, useMemo, useRef } from 'react';
import {
  AccessibilityInfo,
  findNodeHandle,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';

import type { ScoutMessage } from '../api/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { useReduceMotion } from '../theme/motion';
import {
  workCustomerPhases,
  workFamilyLabel,
  workProgressPresentation,
} from './workPresentation';
import {
  workActivityPhaseStates,
  workActivityResultPresentation,
} from './workActivityPresentation';
import { workActivityThreadRef } from './workTimeline';

type Props = {
  visible: boolean;
  message: ScoutMessage | null;
  returnFocusHandle?: number | null;
  onClose: () => void;
  onOpenResult?: (message: ScoutMessage) => void;
};

export function WorkActivitySheet({
  visible,
  message,
  returnFocusHandle,
  onClose,
  onOpenResult,
}: Props) {
  const reduceMotion = useReduceMotion();
  const titleRef = useRef<Text>(null);
  const wasVisibleRef = useRef(false);
  const work = workActivityThreadRef(message);
  const progress = workProgressPresentation(work);
  const family = workFamilyLabel(work);
  const phaseStates = useMemo(() => workActivityPhaseStates(message), [message]);
  const resultPresentation = useMemo(() => workActivityResultPresentation(message), [message]);
  const terminal = ['complete', 'completed', 'published'].includes(String(work?.status ?? '').trim().toLowerCase());
  const openResult = resultPresentation?.state === 'open' ? resultPresentation : null;
  const blockedResult = resultPresentation && resultPresentation.state !== 'open'
    ? resultPresentation
    : null;
  const delivered = terminal && Boolean(openResult);
  const displayPercent = progress.percent === null
    ? null
    : terminal
      ? 100
      : progress.percent;
  const needsAttention = ['error', 'failed', 'needs_attention', 'rejected', 'blocked'].includes(
    String(work?.status ?? '').trim().toLowerCase(),
  );
  const statusLabel = blockedResult?.title
    ?? (terminal
      ? (openResult ? 'Delivered' : 'Work complete')
      : ['Needs input', 'Needs attention'].includes(progress.phaseLabel)
        ? progress.phaseLabel
        : progress.phase
        ? `${progress.phase.label} · ${progress.phase.displayLabel}`
        : progress.phaseLabel);
  const progressCopy = blockedResult?.body
    ?? (terminal
      ? openResult?.body ?? 'Scout finished this run without an openable deliverable attached.'
      : progress.progressCopy);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    if (visible) {
      wasVisibleRef.current = true;
      timer = setTimeout(() => {
        const handle = findNodeHandle(titleRef.current);
        if (handle) AccessibilityInfo.setAccessibilityFocus(handle);
      }, 120);
    } else if (wasVisibleRef.current) {
      wasVisibleRef.current = false;
      if (returnFocusHandle) {
        timer = setTimeout(() => AccessibilityInfo.setAccessibilityFocus(returnFocusHandle), 180);
      }
    }
    return () => clearTimeout(timer);
  }, [returnFocusHandle, visible]);

  return (
    <Modal
      animationType={reduceMotion ? 'none' : 'slide'}
      allowSwipeDismissal
      presentationStyle="formSheet"
      visible={visible && Boolean(message)}
      onRequestClose={onClose}
    >
      <SafeAreaView accessibilityViewIsModal style={styles.sheet} edges={['left', 'right', 'bottom']}>
        <View style={styles.card}>
          <View accessibilityElementsHidden style={styles.grabber} />
          <View style={styles.header}>
            <View style={styles.headerCopy}>
              <Text style={styles.eyebrow}>SCOUT · ACTIVITY</Text>
              <Text ref={titleRef} accessibilityRole="header" maxFontSizeMultiplier={2} style={styles.title}>
                {family}
              </Text>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Close activity"
              hitSlop={8}
              onPress={onClose}
              style={({ pressed }) => [styles.close, pressed && styles.pressed]}
            >
              <SymbolView name="xmark" size={15} tintColor={colors.text2} />
            </Pressable>
          </View>

          <ScrollView
            contentInsetAdjustmentBehavior="automatic"
            contentContainerStyle={styles.content}
            showsVerticalScrollIndicator={false}
            style={styles.scroller}
          >
            <View style={styles.hero}>
              <View style={styles.heroStatusRow}>
                <View style={[
                  styles.statusSignal,
                  delivered && styles.statusSignalComplete,
                  needsAttention && styles.statusSignalAttention,
                ]} />
                <Text style={styles.statusText}>{statusLabel}</Text>
                {displayPercent === null ? null : <Text style={styles.percent}>{displayPercent}%</Text>}
              </View>
              <Text maxFontSizeMultiplier={1.8} style={styles.progressCopy}>
                {progressCopy}
              </Text>
              {displayPercent === null ? null : (
                <View
                  accessibilityRole="progressbar"
                  accessibilityLabel={`${displayPercent}% complete`}
                  accessibilityValue={{ min: 0, max: 100, now: displayPercent }}
                  style={styles.progressTrack}
                >
                  <View style={[styles.progressFill, { width: `${displayPercent}%` }]} />
                </View>
              )}
            </View>

            {phaseStates.length > 0 ? (
              <View accessibilityLabel={`${family} stages`} style={styles.phases}>
                {workCustomerPhases.map((phase, index) => {
                  const state = phaseStates[index];
                  return (
                    <View key={phase.id} style={styles.phaseRow}>
                      <View style={styles.phaseRail}>
                        <View style={[
                          styles.phaseMark,
                          state === 'complete' && styles.phaseMarkComplete,
                          state === 'current' && styles.phaseMarkCurrent,
                        ]}>
                          {state === 'complete' ? (
                            <SymbolView name="checkmark" size={10} tintColor={colors.onEmber} />
                          ) : state === 'current' ? (
                            <View style={styles.phaseMarkCore} />
                          ) : null}
                        </View>
                        {index < workCustomerPhases.length - 1 ? (
                          <View style={[styles.phaseLine, state === 'complete' && styles.phaseLineComplete]} />
                        ) : null}
                      </View>
                      <View style={styles.phaseCopy}>
                        <Text
                          maxFontSizeMultiplier={1.8}
                          style={[styles.phaseLabel, state === 'upcoming' && styles.phaseLabelUpcoming]}
                        >
                          {phase.label}
                        </Text>
                        {state === 'current' ? <Text style={styles.phaseNow}>IN PROGRESS</Text> : null}
                      </View>
                    </View>
                  );
                })}
              </View>
            ) : null}

            {progress.needsInput ? (
              <View style={styles.decisionNote}>
                <SymbolView name="person.crop.circle.badge.exclamationmark" size={18} tintColor={colors.emberText} />
                <View style={styles.decisionCopy}>
                  <Text style={styles.decisionTitle}>Your decision is waiting in the channel</Text>
                  <Text style={styles.decisionBody}>Close Activity to review the choices and keep Scout moving.</Text>
                </View>
              </View>
            ) : null}

            {openResult && message && onOpenResult ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={openResult.actionLabel}
                onPress={() => onOpenResult(message)}
                style={({ pressed }) => [styles.openResult, pressed && styles.openResultPressed]}
              >
                <SymbolView
                  name={openResult.kind === 'presentation'
                    ? 'play.fill'
                    : openResult.kind === 'document'
                      ? 'doc.text.fill'
                      : 'checkmark.circle.fill'}
                  size={15}
                  tintColor={colors.onAccent}
                />
                <Text style={styles.openResultText}>{openResult.actionLabel}</Text>
              </Pressable>
            ) : null}
          </ScrollView>
        </View>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  sheet: { flex: 1, justifyContent: 'flex-end', overflow: 'hidden', backgroundColor: colors.bgApp },
  card: { width: '100%', maxWidth: 720, maxHeight: '90%', alignSelf: 'center', overflow: 'hidden', borderTopLeftRadius: radius.xxl, borderTopRightRadius: radius.xxl, borderCurve: 'continuous', backgroundColor: colors.surface2 },
  scroller: { flexShrink: 1 },
  grabber: { alignSelf: 'center', width: 36, height: 5, marginTop: space[2], borderRadius: radius.full, backgroundColor: colors.line2 },
  header: { minHeight: 72, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[5], paddingVertical: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  headerCopy: { flex: 1, minWidth: 0 },
  eyebrow: { ...type.label, color: colors.emberText, letterSpacing: 0.7 },
  title: { ...type.title2, color: colors.text1 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.surface2 },
  pressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  content: { paddingHorizontal: space[5], paddingTop: space[4], paddingBottom: space[10] },
  hero: { gap: space[3], padding: space[4], borderRadius: radius.xl, borderCurve: 'continuous', backgroundColor: colors.surface1 },
  heroStatusRow: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  statusSignal: { width: 8, height: 8, borderRadius: radius.full, backgroundColor: colors.ember },
  statusSignalComplete: { backgroundColor: colors.success },
  statusSignalAttention: { backgroundColor: colors.danger },
  statusText: { ...type.captionMedium, flex: 1, color: colors.text1 },
  percent: { ...type.captionMedium, color: colors.text2, fontVariant: ['tabular-nums'] },
  progressCopy: { ...type.body, color: colors.text2 },
  progressTrack: { height: 5, overflow: 'hidden', borderRadius: radius.full, backgroundColor: colors.surface3 },
  progressFill: { height: '100%', borderRadius: radius.full, backgroundColor: colors.ember },
  phases: { marginTop: space[4] },
  phaseRow: { minHeight: 52, flexDirection: 'row', gap: space[3] },
  phaseRail: { width: 24, alignItems: 'center' },
  phaseMark: { width: 22, height: 22, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface2 },
  phaseMarkComplete: { borderColor: colors.ember, backgroundColor: colors.ember },
  phaseMarkCurrent: { borderColor: colors.ember, backgroundColor: colors.emberSoft },
  phaseMarkCore: { width: 7, height: 7, borderRadius: radius.full, backgroundColor: colors.ember },
  phaseLine: { width: 1, flex: 1, backgroundColor: colors.line1 },
  phaseLineComplete: { backgroundColor: colors.ember },
  phaseCopy: { flex: 1, minWidth: 0, flexDirection: 'row', alignItems: 'flex-start', justifyContent: 'space-between', gap: space[3], paddingTop: 1 },
  phaseLabel: { ...type.bodyMedium, flex: 1, color: colors.text1 },
  phaseLabelUpcoming: { color: colors.text3 },
  phaseNow: { ...type.label, marginTop: 3, color: colors.emberText },
  decisionNote: { flexDirection: 'row', alignItems: 'flex-start', gap: space[3], marginTop: space[4], padding: space[4], borderRadius: radius.lg, backgroundColor: colors.emberSoft },
  decisionCopy: { flex: 1, minWidth: 0, gap: 2 },
  decisionTitle: { ...type.captionMedium, color: colors.text1 },
  decisionBody: { ...type.caption, color: colors.text2 },
  openResult: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], marginTop: space[5], paddingHorizontal: space[4], borderRadius: radius.full, borderCurve: 'continuous', backgroundColor: colors.accent },
  openResultPressed: { opacity: 0.86, transform: [{ scale: 0.96 }] },
  openResultText: { ...type.button, color: colors.onAccent },
});
