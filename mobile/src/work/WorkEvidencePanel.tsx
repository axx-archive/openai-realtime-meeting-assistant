import React, { useEffect, useState } from 'react';
import { Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import type { StudioProject, StudioWorkFeedbackInput } from '../api/types';
import { colors, radius, space, type } from '../theme/tokens';

export type WorkFeedbackDecision = Pick<StudioWorkFeedbackInput, 'type' | 'verdict' | 'note'>;
export type WorkFeedbackAction = (project: StudioProject, decision: WorkFeedbackDecision) => Promise<boolean>;

const verdictLabel = (verdict?: string) => ({
  accepted: 'Accepted', revision_requested: 'Changes requested', helped: 'Helped',
  did_not_help: 'Did not help', inconclusive: 'Too early to tell', unreviewed: 'Awaiting human review',
}[verdict ?? ''] || 'Not recorded');

export function WorkEvidencePanel({ project, busy, onFeedback, onOpenWork }: {
  project: StudioProject;
  busy: boolean;
  onFeedback?: WorkFeedbackAction;
  onOpenWork?: (id: string) => void;
}) {
  const [note, setNote] = useState('');
  const [outcomeNote, setOutcomeNote] = useState('');
  const [editingReview, setEditingReview] = useState(project.feedback?.reviewState === 'unreviewed');
  const [editingOutcome, setEditingOutcome] = useState(!project.feedback?.currentOutcome);
  const [validation, setValidation] = useState('');
  const feedback = project.feedback;
  useEffect(() => {
    setEditingReview(feedback?.reviewState === 'unreviewed');
    setNote('');
  }, [feedback?.currentReview?.id, feedback?.reviewState]);
  useEffect(() => {
    setEditingOutcome(!feedback?.currentOutcome);
    setOutcomeNote('');
  }, [feedback?.currentOutcome?.id]);
  const execution = project.execution;
  const assurance = project.assurance;
  const submit = async (kind: WorkFeedbackDecision['type'], verdict: WorkFeedbackDecision['verdict']) => {
    if (busy || !onFeedback) return;
    if (verdict === 'revision_requested' && !note.trim()) {
      setValidation('Describe what needs to change before requesting a revision.');
      return;
    }
    setValidation('');
    const currentNote = kind === 'outcome' ? outcomeNote : note;
    if (await onFeedback(project, { type: kind, verdict, note: currentNote.trim() })) {
      if (kind === 'outcome') setOutcomeNote('');
      else setNote('');
    }
  };
  const action = (label: string, kind: WorkFeedbackDecision['type'], verdict: WorkFeedbackDecision['verdict']) => (
    <Pressable key={verdict} accessibilityRole="button" accessibilityLabel={label}
      accessibilityState={{ disabled: busy, busy }} disabled={busy}
      onPress={() => { void submit(kind, verdict); }}
      style={({ pressed }) => [styles.action, (pressed || busy) && styles.dim]}>
      <Text style={styles.actionText}>{label}</Text>
    </Pressable>
  );
  return (
    <View style={styles.root}>
      <View style={styles.section}>
        <Text accessibilityRole="header" style={styles.title}>Evidence</Text>
        <Text style={styles.body}>Version {project.result?.version ?? '—'} · {project.result?.digest ? project.result.digest.slice(0, 12) : 'No exact result attached'}</Text>
        <Text style={styles.label}>Execution</Text>
        <Text style={styles.body}>{execution?.status === 'observed'
          ? `${execution.provider} · ${execution.actualModel || execution.requestedModel}`
          : 'Execution evidence is unavailable for this result.'}</Text>
        {execution?.status === 'observed' ? <Text style={styles.meta}>
          {execution.reasoningEffort ? `${execution.reasoningEffort} reasoning. ` : ''}
          {execution.fallbackUsed ? 'A fallback route was used. ' : ''}
          {execution.qualification === 'not_evaluated' ? 'Route quality has not been evaluated.' : `Qualification: ${execution.qualification}`}
        </Text> : null}
        <Text style={styles.label}>Machine review</Text>
        <Text style={styles.body}>{assurance?.type === 'same_provider_rendered_review'
          ? 'Rendered review by the same provider'
          : assurance?.independent ? 'Independent review' : 'Independent review has not been performed.'}</Text>
        {assurance?.status === 'independent_review_unavailable' ? <Text style={styles.meta}>Required independent review is unavailable. Human judgment is still needed.</Text>
          : assurance?.status === 'passed' ? <Text style={styles.meta}>Review passed{assurance.independent ? '.' : '; it is not independent model review.'}</Text>
            : null}
      </View>
      {project.priorFeedbackEvidence?.length ? <View style={styles.section}>
        <Text accessibilityRole="header" style={styles.title}>Earlier work in context</Text>
        <Text style={styles.meta}>These reviewed results were included as context. This does not establish that they improved this result.</Text>
        {project.priorFeedbackEvidence.map((source) => <Pressable key={source.id} accessibilityRole="button"
          disabled={!onOpenWork} onPress={() => onOpenWork?.(source.rootId)} style={styles.action}>
          <Text style={styles.actionText}>Open earlier work · version {source.result.version}</Text>
          <Text style={styles.meta}>{source.result.digest.slice(0, 12)}</Text>
        </Pressable>)}
      </View> : null}
      {feedback ? <View style={styles.section}>
        <Text accessibilityRole="header" style={styles.title}>Human judgment</Text>
        <Text style={styles.state}>{verdictLabel(feedback.reviewState)}</Text>
        {feedback.currentReview ? <Text style={styles.meta}>
          {feedback.currentReview.actorName} · {new Date(feedback.currentReview.at).toLocaleDateString()}
        </Text> : null}
        {feedback.currentReview?.note ? <Text style={styles.body}>{feedback.currentReview.note}</Text> : null}
        <Text style={styles.meta}>Your review applies to this exact version. Acceptance does not publish it or change its machine-review status.</Text>
        {feedback.canReview && onFeedback && !editingReview ? <Pressable accessibilityRole="button" onPress={() => { setNote(feedback.currentReview?.note ?? ''); setEditingReview(true); }} style={styles.action}><Text style={styles.actionText}>Change your review</Text></Pressable> : null}
        {feedback.canReview && onFeedback && editingReview ? <>
          <TextInput accessibilityLabel="Review note" multiline maxLength={2000} value={note}
            onChangeText={(value) => { setNote(value); setValidation(''); }} editable={!busy}
            placeholder="What worked, or what should change?" placeholderTextColor={colors.text3}
            style={styles.input} />
          {validation ? <Text accessibilityRole="alert" style={styles.error}>{validation}</Text> : null}
          <View style={styles.actions}>
            {action('Accept this version', 'review', 'accepted')}
            {action('Request changes', 'review', 'revision_requested')}
          </View>
          <Text style={styles.meta}>Requesting changes records your feedback. It does not start another run.</Text>
        </> : null}
        {feedback.reviewState === 'accepted' ? <View style={styles.outcome}>
          <Text accessibilityRole="header" style={styles.title}>Reported outcome</Text>
          <Text style={styles.body}>{feedback.currentOutcome ? verdictLabel(feedback.currentOutcome.verdict) : 'No outcome recorded yet.'}</Text>
          {feedback.currentOutcome?.note ? <Text style={styles.body}>{feedback.currentOutcome.note}</Text> : null}
          {feedback.canObserveOutcome && onFeedback && !editingOutcome ? <Pressable accessibilityRole="button" onPress={() => { setOutcomeNote(feedback.currentOutcome?.note ?? ''); setEditingOutcome(true); }} style={styles.action}><Text style={styles.actionText}>Update reported outcome</Text></Pressable> : null}
          {feedback.canObserveOutcome && onFeedback && editingOutcome ? <View>
            <TextInput accessibilityLabel="Reported outcome note" multiline maxLength={2000} value={outcomeNote} editable={!busy}
              onChangeText={setOutcomeNote} placeholder="What happened after you used the work?" placeholderTextColor={colors.text3} style={styles.input} />
            <View style={styles.actions}>
            {action('It helped', 'outcome', 'helped')}
            {action('It did not help', 'outcome', 'did_not_help')}
            {action('Too early to tell', 'outcome', 'inconclusive')}
            </View>
          </View> : null}
          <Text style={styles.meta}>A human-reported outcome, not independently verified proof. It is separate from accepting the work.</Text>
        </View> : null}
        {feedback.history.length > 0 ? <View style={styles.history}>
          <Text style={styles.label}>Recent review history</Text>
          {feedback.history.slice(-3).reverse().map((event) => <Text key={event.id} style={styles.meta}>
            {verdictLabel(event.verdict)} · {event.actorName} · version {event.result.version}
          </Text>)}
          {feedback.historyTruncated ? <Text style={styles.meta}>Earlier reviews are not included in this view.</Text> : null}
        </View> : null}
      </View> : null}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { gap: space[5] },
  section: { paddingTop: space[4], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1, gap: space[2] },
  title: { ...type.title2, color: colors.text1 },
  label: { ...type.captionMedium, color: colors.text1, marginTop: space[2] },
  body: { ...type.body, color: colors.text2 },
  meta: { ...type.caption, color: colors.text2 },
  state: { ...type.bodyMedium, color: colors.emberText },
  input: { ...type.body, color: colors.text1, backgroundColor: colors.surface1, borderColor: colors.line2, borderWidth: StyleSheet.hairlineWidth, borderRadius: radius.lg, minHeight: 100, padding: space[3], marginTop: space[3], textAlignVertical: 'top' },
  actions: { flexDirection: 'row', flexWrap: 'wrap', gap: space[2], marginTop: space[2] },
  action: { minHeight: 44, justifyContent: 'center', paddingHorizontal: space[3], paddingVertical: space[2], borderRadius: radius.lg, backgroundColor: colors.surface2, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2 },
  actionText: { ...type.captionMedium, color: colors.text1 },
  dim: { opacity: 0.55 },
  error: { ...type.caption, color: colors.danger },
  outcome: { gap: space[2], marginTop: space[4] },
  history: { gap: space[2], marginTop: space[3] },
});
