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

import type { StudioProject, StudioProjectCheckpoint } from '../api/types';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';
import {
  studioProjectKindLabel,
  studioProjectOpenTarget,
  studioProjectResultIsFinal,
  studioProjectStatusLabel,
} from './studioProjectModel';

type CheckpointOption = NonNullable<StudioProjectCheckpoint['options']>[number];

type DetailProps = {
  project: StudioProject | null;
  compact?: boolean;
  busyAction?: string;
  actionError?: string;
  onClose?: () => void;
  onOpenResult: (project: StudioProject) => void;
  onOpenSource: (project: StudioProject) => void;
  onContinueResult: (project: StudioProject) => void;
  onResolveCheckpoint: (project: StudioProject, option: CheckpointOption) => void;
  onRename: (project: StudioProject) => void;
};

function projectStatusTone(project: StudioProject) {
  if (project.status === 'ready') return styles.signalReady;
  if (project.status === 'needs_input' || project.status === 'needs_attention') return styles.signalAttention;
  if (project.status === 'stopped') return styles.signalStopped;
  return styles.signalActive;
}

export function WorkProjectDetail({
  project,
  compact = false,
  busyAction = '',
  actionError = '',
  onClose,
  onOpenResult,
  onOpenSource,
  onContinueResult,
  onResolveCheckpoint,
  onRename,
}: DetailProps) {
  if (!project) {
    return (
      <View accessibilityLabel="Select work to see its details" style={styles.emptyDetail}>
        <View style={styles.emptyMark}>
          <SymbolView name="square.stack.3d.up" tintColor={colors.text3} size={22} />
        </View>
        <Text style={styles.emptyTitle}>Select a project</Text>
        <Text style={styles.emptyBody}>Its progress, decisions, and finished work will appear here.</Text>
      </View>
    );
  }

  const target = studioProjectOpenTarget(project);
  const checkpointOptions = project.checkpoint?.options ?? [];
  const active = ['queued', 'running'].includes(project.status);
  const decision = project.status === 'needs_input'
    && Boolean(project.checkpoint?.question)
    && checkpointOptions.length > 0;
	const hasSource = Boolean(String(project.source?.threadId ?? '').trim());
  const needsSourceAction = (project.status === 'needs_input' || project.status === 'needs_attention')
    && !decision
	&& hasSource;
  const resultLabel = project.kind === 'presentation' ? 'Presentation' : 'Research report';
	const finalReady = studioProjectResultIsFinal(project);

  return (
    <View style={styles.detail}>
      <View style={styles.detailHeader}>
        <View style={styles.detailHeaderCopy}>
          <Text style={styles.detailEyebrow}>{studioProjectKindLabel(project.kind).toUpperCase()} STUDIO</Text>
          <Text accessibilityRole="header" maxFontSizeMultiplier={1.8} style={styles.detailTitle}>{project.title}</Text>
        </View>
        {onClose ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Close project"
            hitSlop={8}
            onPress={onClose}
            style={({ pressed }) => [styles.close, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" tintColor={colors.text2} size={15} />
          </Pressable>
        ) : null}
      </View>

      <ScrollView
        contentInsetAdjustmentBehavior="automatic"
        contentContainerStyle={[styles.detailBody, compact && styles.detailBodyCompact]}
        showsVerticalScrollIndicator={false}
      >
        {actionError ? (
          <View accessibilityLiveRegion="assertive" accessibilityRole="alert" style={styles.actionError}>
            <SymbolView name="exclamationmark.triangle.fill" tintColor={colors.emberText} size={14} />
            <Text style={styles.actionErrorText}>{actionError}</Text>
          </View>
        ) : null}
        <View style={styles.statusCard}>
          <View style={styles.statusLine}>
            <View accessibilityElementsHidden style={[styles.signal, projectStatusTone(project)]} />
            <Text style={styles.statusLabel}>{studioProjectStatusLabel(project.status)}</Text>
            <Text style={styles.percent}>{Math.max(0, Math.min(100, project.progressPercent))}%</Text>
          </View>
          <View
            accessibilityRole="progressbar"
            accessibilityLabel={`${project.progressPercent}% complete`}
            accessibilityValue={{ min: 0, max: 100, now: project.progressPercent }}
            style={styles.progressTrack}
          >
            <View style={[styles.progressFill, { width: `${Math.max(0, Math.min(100, project.progressPercent))}%` }]} />
          </View>
          {active ? <Text style={styles.statusCopy}>Scout is moving this through the Studio.</Text> : null}
        </View>

        {project.phases.length > 0 ? (
          <View accessibilityLabel={`${resultLabel} progress`} style={styles.phases}>
            {project.phases.map((phase, index) => {
              const complete = phase.status === 'complete';
              const current = !complete && phase.status !== 'upcoming';
              return (
                <View key={phase.id} style={styles.phaseRow}>
                  <View style={styles.phaseRail}>
                    <View style={[styles.phaseMark, complete && styles.phaseMarkComplete, current && styles.phaseMarkCurrent]}>
                      {complete ? <SymbolView name="checkmark" tintColor={colors.onEmber} size={9} /> : current ? <View style={styles.phaseCore} /> : null}
                    </View>
                    {index < project.phases.length - 1 ? <View style={[styles.phaseLine, complete && styles.phaseLineComplete]} /> : null}
                  </View>
                  <View style={styles.phaseCopy}>
                    <Text style={[styles.phaseLabel, phase.status === 'upcoming' && styles.phaseUpcoming]}>{phase.label}</Text>
                    {current ? <Text style={styles.phaseNow}>{studioProjectStatusLabel(project.status).toUpperCase()}</Text> : null}
                  </View>
                </View>
              );
            })}
          </View>
        ) : null}

        {decision ? (
          <View style={styles.decisionCard}>
            <View style={styles.decisionHead}>
              <SymbolView name="person.crop.circle.badge.exclamationmark" tintColor={colors.emberText} size={18} />
              <Text style={styles.decisionKicker}>SCOUT NEEDS YOUR CALL</Text>
            </View>
            <Text style={styles.decisionQuestion}>{project.checkpoint?.question}</Text>
            <View style={styles.decisionActions}>
              {checkpointOptions.map((option) => {
                const busy = busyAction === `checkpoint:${option.id}`;
                return (
                  <Pressable
                    key={option.id}
                    accessibilityRole="button"
                    accessibilityLabel={option.label}
                    accessibilityState={{ busy, disabled: Boolean(busyAction) }}
                    disabled={Boolean(busyAction)}
                    onPress={() => onResolveCheckpoint(project, option)}
                    style={({ pressed }) => [styles.decisionAction, pressed && styles.pressed, Boolean(busyAction) && styles.disabled]}
                  >
                    {busy ? <ActivityIndicator color={colors.onAccent} size="small" /> : null}
                    <Text style={styles.decisionActionText}>{option.label}</Text>
                  </Pressable>
                );
              })}
            </View>
          </View>
        ) : null}

        {needsSourceAction ? (
          <View style={styles.sourceActionCard}>
            <View style={styles.sourceActionCopy}>
              <Text style={styles.sourceActionTitle}>{project.status === 'needs_input' ? 'Scout needs your input' : 'Scout needs help moving this forward'}</Text>
              <Text style={styles.sourceActionBody}>{project.checkpoint?.question || 'Open the source conversation to answer or adjust the direction.'}</Text>
            </View>
          </View>
        ) : null}

        {project.result ? (
          <View style={styles.resultCard}>
            <View style={styles.resultIcon}>
              <SymbolView name={project.kind === 'presentation' ? 'rectangle.stack.fill' : 'doc.text.fill'} tintColor={colors.emberText} size={20} />
            </View>
            <View style={styles.resultCopy}>
			  <Text style={styles.resultEyebrow}>{finalReady ? resultLabel.toUpperCase() : 'DRAFT · REVIEW NEEDED'}</Text>
			  <Text numberOfLines={2} style={styles.resultTitle}>{project.result.title || project.title}</Text>
			  {project.result.preview ? <Text numberOfLines={3} style={styles.resultPreview}>{project.result.preview}</Text> : null}
			  {!finalReady ? <Text style={styles.resultReviewNote}>{project.result.canContinue
				? 'Revise this exact draft and return it to review.'
				: 'Review is required before final presentation or download.'}</Text> : null}
            </View>
			{project.result.canContinue ? (
			  <Pressable
				accessibilityRole="button"
				accessibilityLabel={project.result.qualityState === 'edited_after_admission' ? 'Review exact edited draft' : 'Continue exact draft review'}
				accessibilityState={{ busy: busyAction === 'continue', disabled: Boolean(busyAction) }}
				disabled={Boolean(busyAction)}
				onPress={() => onContinueResult(project)}
				style={({ pressed }) => [styles.continueResult, pressed && styles.openResultPressed, Boolean(busyAction) && styles.disabled]}
			  >
				{busyAction === 'continue' ? <ActivityIndicator color={colors.onAccent} size="small" /> : <SymbolView name="checkmark.seal" tintColor={colors.onAccent} size={14} />}
				<Text style={styles.openResultText}>{project.result.qualityState === 'edited_after_admission' ? 'Review changes' : 'Continue review'}</Text>
			  </Pressable>
			) : null}
            {target ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={target.kind === 'deck'
                  ? target.canPresent ? 'Present exact presentation' : 'Preview exact presentation draft'
                  : 'Open exact research report'}
                onPress={() => onOpenResult(project)}
                style={({ pressed }) => [styles.openResult, pressed && styles.openResultPressed]}
              >
                <SymbolView name={target.kind === 'deck' ? target.canPresent ? 'play.fill' : 'eye.fill' : 'arrow.up.right'} tintColor={colors.onAccent} size={14} />
                <Text style={styles.openResultText}>{target.kind === 'deck' ? target.canPresent ? 'Present' : 'Preview' : 'Open'}</Text>
              </Pressable>
            ) : (
              <View style={styles.resultUnavailable}>
                <Text style={styles.resultUnavailableText}>{project.kind === 'presentation' && project.result.canEdit
                  ? 'Draft saved · edit on desktop'
                  : 'Preview available when this version is ready'}</Text>
              </View>
            )}
            {project.kind === 'presentation' && project.result.canEdit ? (
              <View style={styles.desktopNote}>
                <SymbolView name="desktopcomputer" tintColor={colors.text3} size={13} />
                <Text style={styles.desktopNoteText}>Full slide editing is available on desktop.</Text>
              </View>
            ) : null}
          </View>
        ) : (
          <View style={styles.pendingResult}>
            <SymbolView name="sparkles" tintColor={colors.emberText} size={18} />
            <View style={styles.pendingResultCopy}>
              <Text style={styles.pendingResultTitle}>The finished work will land here</Text>
              <Text style={styles.pendingResultBody}>You can keep working elsewhere; Scout will update this project here.</Text>
            </View>
          </View>
        )}

        {project.companyProject?.title ? (
          <View style={styles.filedRow}>
            <SymbolView name="folder.fill" tintColor={colors.text3} size={13} />
            <Text numberOfLines={1} style={styles.filedText}>Filed with {project.companyProject.title}</Text>
          </View>
        ) : null}

		{hasSource ? (
		  <Pressable
			accessibilityRole="button"
			accessibilityLabel="Open source conversation"
			onPress={() => onOpenSource(project)}
			style={({ pressed }) => [styles.sourceLink, pressed && styles.pressed]}
		  >
			<SymbolView name="bubble.left.and.bubble.right" tintColor={colors.text2} size={14} />
			<Text style={styles.sourceLinkText}>Source conversation</Text>
			<SymbolView name="chevron.right" tintColor={colors.text3} size={12} />
		  </Pressable>
		) : null}

        {project.canRename ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Rename Studio project"
            accessibilityState={{ disabled: Boolean(busyAction) }}
            disabled={Boolean(busyAction)}
            onPress={() => onRename(project)}
            style={({ pressed }) => [styles.rename, pressed && styles.pressed, Boolean(busyAction) && styles.disabled]}
          >
            {busyAction === 'rename' ? <ActivityIndicator color={colors.text2} size="small" /> : <SymbolView name="pencil" tintColor={colors.text2} size={14} />}
            <Text style={styles.renameText}>Rename</Text>
          </Pressable>
        ) : null}
      </ScrollView>
    </View>
  );
}

type SheetProps = Omit<DetailProps, 'compact' | 'onClose'> & {
  visible: boolean;
  onClose: () => void;
};

export function WorkProjectSheet({ visible, project, onClose, ...detailProps }: SheetProps) {
  return (
    <Modal
      allowSwipeDismissal
      animationType="slide"
      presentationStyle="formSheet"
      visible={visible && Boolean(project)}
      onRequestClose={onClose}
    >
      <SafeAreaView accessibilityViewIsModal style={styles.sheet} edges={['left', 'right', 'bottom']}>
        <WorkProjectDetail {...detailProps} compact onClose={onClose} project={project} />
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  sheet: { flex: 1, backgroundColor: colors.bgApp },
  detail: { flex: 1, minWidth: 0, backgroundColor: colors.bgApp },
  detailHeader: { minHeight: 76, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[5], paddingVertical: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  detailHeaderCopy: { flex: 1, minWidth: 0 },
  detailEyebrow: { ...type.label, color: colors.emberText, letterSpacing: 0.7 },
  detailTitle: { ...type.title2, marginTop: 3, color: colors.text1 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.surface2 },
  detailBody: { gap: space[4], padding: space[5], paddingBottom: space[10] },
  detailBodyCompact: { paddingTop: space[4] },
  actionError: { flexDirection: 'row', alignItems: 'flex-start', gap: space[2], borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.ember, backgroundColor: colors.emberSoft, padding: space[3] },
  actionErrorText: { ...type.caption, color: colors.text1, flex: 1 },
  statusCard: { gap: space[3], padding: space[4], borderRadius: radius.xl, borderCurve: 'continuous', backgroundColor: colors.surface1, ...shadow[1] },
  statusLine: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  signal: { width: 8, height: 8, borderRadius: radius.full },
  signalActive: { backgroundColor: colors.ember },
  signalReady: { backgroundColor: colors.success },
  signalAttention: { backgroundColor: colors.warn },
  signalStopped: { backgroundColor: colors.text3 },
  statusLabel: { ...type.captionMedium, flex: 1, color: colors.text1 },
  percent: { ...type.captionMedium, color: colors.text2, fontVariant: ['tabular-nums'] },
  progressTrack: { height: 5, overflow: 'hidden', borderRadius: radius.full, backgroundColor: colors.surface3 },
  progressFill: { height: '100%', borderRadius: radius.full, backgroundColor: colors.ember },
  statusCopy: { ...type.caption, color: colors.text2 },
  phases: { paddingHorizontal: space[1] },
  phaseRow: { minHeight: 48, flexDirection: 'row', gap: space[3] },
  phaseRail: { width: 24, alignItems: 'center' },
  phaseMark: { width: 22, height: 22, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface2 },
  phaseMarkComplete: { borderColor: colors.ember, backgroundColor: colors.ember },
  phaseMarkCurrent: { borderColor: colors.ember, backgroundColor: colors.emberSoft },
  phaseCore: { width: 7, height: 7, borderRadius: radius.full, backgroundColor: colors.ember },
  phaseLine: { width: 1, flex: 1, backgroundColor: colors.line1 },
  phaseLineComplete: { backgroundColor: colors.ember },
  phaseCopy: { flex: 1, minWidth: 0, flexDirection: 'row', alignItems: 'flex-start', justifyContent: 'space-between', gap: space[3], paddingTop: 1 },
  phaseLabel: { ...type.bodyMedium, color: colors.text1 },
  phaseUpcoming: { color: colors.text3 },
  phaseNow: { ...type.label, maxWidth: '48%', color: colors.emberText, textAlign: 'right' },
  decisionCard: { gap: space[3], padding: space[4], borderRadius: radius.xl, borderCurve: 'continuous', backgroundColor: colors.emberSoft },
  decisionHead: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  decisionKicker: { ...type.label, flex: 1, color: colors.emberText },
  decisionQuestion: { ...type.bodyMedium, color: colors.text1 },
  decisionActions: { gap: space[2] },
  decisionAction: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], paddingHorizontal: space[4], borderRadius: radius.full, backgroundColor: colors.accent },
  decisionActionText: { ...type.button, color: colors.onAccent, textAlign: 'center' },
  sourceActionCard: { gap: space[3], padding: space[4], borderRadius: radius.xl, borderCurve: 'continuous', backgroundColor: colors.surface1 },
  sourceActionCopy: { gap: 2 },
  sourceActionTitle: { ...type.bodyMedium, color: colors.text1 },
  sourceActionBody: { ...type.caption, color: colors.text2 },
	  sourceLink: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[3], borderRadius: radius.lg, backgroundColor: colors.surface2 },
	  sourceLinkText: { ...type.captionMedium, flex: 1, color: colors.text2 },
  resultCard: { gap: space[3], padding: space[4], borderRadius: radius.xl, borderCurve: 'continuous', backgroundColor: colors.surface1, ...shadow[1] },
  resultIcon: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: radius.lg, backgroundColor: colors.emberSoft },
  resultCopy: { gap: 2 },
  resultEyebrow: { ...type.label, color: colors.text3 },
  resultTitle: { ...type.headline, color: colors.text1 },
  resultPreview: { ...type.caption, marginTop: space[1], color: colors.text2 },
	resultReviewNote: { ...type.captionMedium, marginTop: space[1], color: colors.warn },
  openResult: { minHeight: 48, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], borderRadius: radius.full, backgroundColor: colors.accent },
	continueResult: { minHeight: 48, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], borderRadius: radius.full, backgroundColor: colors.accent },
  openResultPressed: { opacity: 0.86, transform: [{ scale: 0.96 }] },
  openResultText: { ...type.button, color: colors.onAccent },
  resultUnavailable: { minHeight: 44, justifyContent: 'center', paddingHorizontal: space[4], borderRadius: radius.full, backgroundColor: colors.surface3 },
  resultUnavailableText: { ...type.captionMedium, color: colors.text2, textAlign: 'center' },
  desktopNote: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  desktopNoteText: { ...type.caption, flex: 1, color: colors.text3 },
  pendingResult: { flexDirection: 'row', alignItems: 'flex-start', gap: space[3], padding: space[4], borderRadius: radius.xl, borderCurve: 'continuous', backgroundColor: colors.surface1 },
  pendingResultCopy: { flex: 1, minWidth: 0 },
  pendingResultTitle: { ...type.bodyMedium, color: colors.text1 },
  pendingResultBody: { ...type.caption, marginTop: 2, color: colors.text2 },
  filedRow: { minHeight: 40, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[2] },
  filedText: { ...type.caption, flex: 1, color: colors.text3 },
  rename: { minHeight: hitMin, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[4], borderRadius: radius.full, backgroundColor: colors.surface2 },
  renameText: { ...type.captionMedium, color: colors.text2 },
  pressed: { opacity: 0.78, transform: [{ scale: 0.96 }] },
  disabled: { opacity: 0.58 },
  emptyDetail: { flex: 1, alignItems: 'center', justifyContent: 'center', gap: space[2], padding: space[8] },
  emptyMark: { width: 52, height: 52, alignItems: 'center', justifyContent: 'center', borderRadius: radius.xl, backgroundColor: colors.surface2 },
  emptyTitle: { ...type.headline, color: colors.text1, textAlign: 'center' },
  emptyBody: { ...type.caption, maxWidth: 280, color: colors.text2, textAlign: 'center' },
});
