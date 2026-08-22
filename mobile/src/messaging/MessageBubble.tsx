import React, { useMemo, useRef, useState } from 'react';
import { ActivityIndicator, Animated, findNodeHandle, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import { Image } from 'expo-image';
import { SymbolView } from 'expo-symbols';
import * as Linking from 'expo-linking';
import { useMappingHelper } from '@shopify/flash-list';

import type { ScoutAnswerSource, ScoutFileAttachment, ScoutMessage, ScoutWorkThreadRef } from '../api/types';
import { authenticatedFileHeaders, authenticatedFileUrl } from '../files/fileActions';
import { colors, radius, shadow, space, type } from '../theme/tokens';
import { LinkPreviewCard } from './LinkPreviewCard';
import { ScoutRichText } from './ScoutRichText';
import { ChatAvatar } from './ChatAvatar';
import { messageLongPressDelayMs } from './messageGestures';
import { scoutReplyLifecyclePresentation } from './scoutReplyLifecycle';
import {
  extractHttpUrls,
  groupMessageReactions,
  parseMessageTextSegments,
} from './messagePresentation';
import {
  workFamilyLabel,
  workProgressPresentation,
} from './workPresentation';
import {
  InlineArtifactPreview,
  type InlineArtifactKind,
} from './InlineArtifactPreview';

/**
 * Detect the artifact kind from a work message for inline rendering.
 *
 * Checks message.thread.mode (live path) first, then message.work.
 * Does NOT infer kind from title/summary keywords alone — mode must be explicit.
 */
function detectInlineArtifactKind(message: ScoutMessage): InlineArtifactKind | null {
  const resultType = String(message.thread?.resultArtifactType ?? '').toLowerCase();
  if (/^(html_deck|deck|presentation|slides?)$/u.test(resultType)) return 'html_deck';
  if (/^(table|data_table|spreadsheet)$/u.test(resultType)) return 'table';
  if (/^(ideation|ideas|brainstorm)$/u.test(resultType)) return 'ideation';
  if (/^(research|deep_research|report|analysis)$/u.test(resultType)) return 'research';
  if (/^(markdown|document|doc|memo|brief)$/u.test(resultType)) return 'document';

  // Live path: kind=thread uses message.thread.mode
  const threadMode = String(message.thread?.mode ?? '').toLowerCase();
  if (threadMode) {
    if (/^(html_deck|deck|presentation|slides?)$/u.test(threadMode)) return 'html_deck';
    if (/^(table|data_table|spreadsheet)$/u.test(threadMode)) return 'table';
    if (/^(ideation|ideas|brainstorm)$/u.test(threadMode)) return 'ideation';
    if (/^(research|deep_research|report|analysis)$/u.test(threadMode)) return 'research';
    if (/^(markdown|document|doc|memo|brief)$/u.test(threadMode)) return 'document';
  }

  // Governed work path: check explicit artifact kind fields
  const work = message.work;
  if (work) {
    const kind = String(
      (work as Record<string, unknown>).artifactKind ??
      (work as Record<string, unknown>).workKind ??
      (work as Record<string, unknown>).outputKind ??
      ''
    ).toLowerCase();

    if (/^(html_deck|deck|presentation|slides?)$/u.test(kind)) return 'html_deck';
    if (/^(table|data_table|spreadsheet)$/u.test(kind)) return 'table';
    if (/^(ideation|ideas|brainstorm)$/u.test(kind)) return 'ideation';
    if (/^(research|deep_research|report|analysis)$/u.test(kind)) return 'research';
    if (/^(markdown|document|doc|memo|brief)$/u.test(kind)) return 'document';
  }

  // Do NOT infer from title/summary keywords — mode must be explicit
  return null;
}

/**
 * Detect if message body is an HTML deck (Scout's live deliverable path).
 *
 * Mirrors web's artifactIsHTMLDeck: declared metadata type=html_deck, or a body
 * that starts as an HTML document. This is the REAL deliverable path — not a
 * work card, not a text bubble.
 */
function isHtmlDeckBody(text: string): boolean {
  const body = String(text ?? '').trim().toLowerCase();
  return body.startsWith('<!doctype html') || body.startsWith('<html');
}

/**
 * Extract title from HTML deck body.
 * Looks for <title> tag or first <h1>.
 */
function extractDeckTitle(html: string): string {
  const titleMatch = html.match(/<title[^>]*>([^<]+)<\/title>/i);
  if (titleMatch) return titleMatch[1].trim();
  const h1Match = html.match(/<h1[^>]*>([^<]+)<\/h1>/i);
  if (h1Match) return h1Match[1].trim();
  return 'Presentation';
}

// Work cards carry dense state and controls. Preserve substantial Dynamic Type
// growth without letting the largest accessibility categories collapse a
// family or action into one-character columns; the full copy remains in the
// accessibility label and the Activity sheet.
const workSurfaceMaxFontSizeMultiplier = 2;

export type MessageBubbleProps = {
  message: ScoutMessage;
  own: boolean;
  showAuthor: boolean;
  showAvatar?: boolean;
  avatarDataURL?: string;
  sessionToken: string;
  viewerEmail: string;
  timestampReveal: Animated.Value;
  onOpenSource?: (source: ScoutAnswerSource) => void;
  onOpenReplySource?: (messageId: string) => void;
  showReplyContext?: boolean;
  threadReplies?: readonly ScoutMessage[];
  onOpenThread?: (message: ScoutMessage) => void;
  onChangeProject?: (message: ScoutMessage, returnFocusHandle?: number) => void;
  onLongPress?: (message: ScoutMessage, own: boolean, attachment?: { file: ScoutFileAttachment; index: number }) => void;
  onOpenAttachment?: (file: ScoutFileAttachment) => void;
  onToggleReaction?: (message: ScoutMessage, emoji: string, active: boolean) => void;
  onRetryReply?: (message: ScoutMessage) => void;
  onOpenLongMessage?: (text: string, authorName: string, scout: boolean) => void;
  onOpenWorkArtifact?: (message: ScoutMessage, returnFocusHandle?: number) => void;
  onResolveWorkCheckpoint?: (message: ScoutMessage, option: { id: string; label: string; action: string }) => void;
  onChangeWorkProject?: (message: ScoutMessage, returnFocusHandle?: number) => void;
  onResolveProposal?: (message: ScoutMessage, action: 'accepted' | 'dismissed', objective: string) => void;
  proposalObjective?: string;
  onChangeProposalObjective?: (message: ScoutMessage, objective: string) => void;
  onSaveWorkArtifact?: (message: ScoutMessage) => void;
  onOpenSavedWorkArtifact?: (message: ScoutMessage) => void;
  onRegenerateWorkArtifact?: (message: ScoutMessage) => void;
  onRetryGoal?: (message: ScoutMessage) => void;
  onViewArtifactFullscreen?: (message: ScoutMessage) => void;
  onSaveImage?: (message: ScoutMessage) => void;
  onRegenerateImage?: (message: ScoutMessage) => void;
  resolvingProposal?: boolean;
  retryingReply?: boolean;
  savingImage?: boolean;
  regeneratingImage?: boolean;
  imageSaved?: boolean;
  savingWork?: boolean;
  regeneratingWork?: boolean;
  retryingGoal?: boolean;
  workSaved?: boolean;
  workDriveSaveAvailability?: 'checking' | 'available' | 'unavailable';
};

function isScout(message: ScoutMessage): boolean {
  const role = String(message.role ?? '').toLowerCase();
  return role === 'assistant' || role === 'scout';
}

function bodyOf(message: ScoutMessage): string {
  return String(message.text ?? message.content ?? '').trim();
}

function timeOf(message: ScoutMessage): string {
  if (!message.createdAt) return '';
  const at = new Date(String(message.createdAt));
  if (Number.isNaN(at.getTime())) return '';
  return at.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

function workThreadRef(message: ScoutMessage): { ref: ScoutWorkThreadRef; governedRecord: boolean } | null {
  const kind = String(message.kind ?? '').toLowerCase();
  if ((kind === 'thread' || kind === 'artifact') && message.thread) return { ref: message.thread, governedRecord: false };
  if ((kind !== 'work_result' && kind !== 'work_record') || !message.work) return null;
  return {
    governedRecord: true,
    ref: {
      id: message.work.runId || message.work.id,
      mode: 'completed work',
      query: message.work.title,
      status: message.work.status,
      artifactId: message.work.artifactId,
      agentName: message.work.workerName,
      currentStage: message.work.currentStage,
      progressPercent: message.work.progressPercent,
      progressNote: message.work.summary,
      resultTitle: message.work.title,
      resultPreview: message.work.summary,
      provenance: message.work.providerExecutionFenced
        ? 'Deterministic local · provider fenced'
        : 'Provider-backed',
    },
  };
}

function workThreadPresentation(message: ScoutMessage) {
  const work = workThreadRef(message);
  if (!work) return null;
  const ref = work.ref;
  const status = String(ref.status ?? 'running').toLowerCase();
  const complete = status === 'complete' || status === 'completed' || status === 'published';
	const resultQualityState = String(ref.resultQualityState ?? '').trim().toLowerCase();
	const followUpStatus = String(ref.followUpStatus ?? '').toLowerCase();
  const revisionNeedsAttention = complete && (followUpStatus === 'needs_attention' || (!followUpStatus && /revision needs attention/iu.test(String(ref.progressNote ?? ''))));
  const failed = status === 'failed' || status === 'error' || status === 'needs_attention' || status === 'rejected' || status === 'blocked';
  const progress = workProgressPresentation(ref);
  const needsInput = progress.needsInput;
  const decisionStatus = status === 'approval_required' || status === 'needs_input' || status === 'parked';
  const active = status === 'queued' || status === 'running' || (decisionStatus && !needsInput);
  const progressPercent = progress.percent;
  const attentionReason = String(ref.attentionReason ?? '').toLowerCase();
  const family = workFamilyLabel(ref);
  const basePhase = progress.phaseLabel;
  const phase = complete
    ? revisionNeedsAttention ? 'Delivered · revision needs attention' : 'Delivered'
    : basePhase;
  const agentName = String(ref.agentName ?? 'Scout').trim() || 'Scout';
  return {
    ref,
    governedRecord: work.governedRecord,
    active,
    complete,
    failed,
    needsInput,
    agentName,
    delegatedBy: String(ref.delegatedBy ?? '').trim(),
    family,
    phase,
    customerPhase: progress.phase,
    progressCopy: progress.progressCopy,
    mode: String(ref.mode ?? 'work').trim() || 'work',
    query: String(ref.query ?? '').trim() || 'Scout workstream',
    progressPercent,
    resultQualityState,
    attentionReason,
    attentionCopy: attentionReason === 'output_truncated'
      ? 'Scout reached the final report, but the response was cut off before a deliverable could be accepted.'
      : attentionReason === 'quality_gate_failed'
        ? 'Scout finished a draft, but it did not meet the evidence and quality bar.'
        : attentionReason === 'provider_unavailable'
          ? 'The research provider became unavailable before Scout could finish.'
          : failed
            ? 'Scout could not finish this work. View the failure details or try again.'
            : '',
    label: status === 'queued'
      ? 'Queued'
      : status === 'running'
        ? phase
        : resultQualityState === 'edited_after_admission'
          ? 'Edited draft · review required'
          : resultQualityState === 'draft_needs_attention'
            ? 'Draft · needs attention'
            : complete
              ? revisionNeedsAttention ? 'Deliverable ready · revision needs attention' : 'Deliverable ready'
          : needsInput
            ? 'Needs input'
          : failed
            ? 'Needs attention'
            : status.replaceAll('_', ' '),
  };
}

function attachmentLabel(file: ScoutFileAttachment): string {
  const size = Number(file.size ?? 0);
  const detail = size > 0 ? ` · ${size < 1_048_576 ? `${Math.max(1, Math.round(size / 1024))} KB` : `${(size / 1_048_576).toFixed(1)} MB`}` : '';
  return `${file.name}${detail}`;
}

function shortenedMessage(value: string, maxCharacters: number): string {
  if (value.length <= maxCharacters) return value;
  const slice = value.slice(0, maxCharacters).replace(/\s+\S*$/u, '').trimEnd();
  return `${slice || value.slice(0, maxCharacters).trimEnd()}…`;
}

function generatedImageFile(message: ScoutMessage): ScoutFileAttachment | null {
  const image = message.image;
  const ref = String(image?.ref ?? '').trim().toLowerCase();
  if (!/^[0-9a-f]{64}$/u.test(ref)) return null;
  return {
    ref,
    name: String(image?.name ?? '').trim() || 'concept-render.png',
    mime: String(image?.mime ?? '').trim().toLowerCase() || 'image/png',
  };
}

export const MessageBubble = React.memo(function MessageBubble({
  message,
  own,
  showAuthor,
  showAvatar = false,
  avatarDataURL,
  sessionToken,
  viewerEmail,
  timestampReveal,
  onOpenSource,
  onOpenReplySource,
  showReplyContext = true,
  threadReplies = [],
  onOpenThread,
  onChangeProject,
  onLongPress,
  onOpenAttachment,
  onToggleReaction,
  onRetryReply,
  onOpenLongMessage,
  onOpenWorkArtifact,
  onResolveWorkCheckpoint,
  onChangeWorkProject,
  onResolveProposal,
  proposalObjective,
  onChangeProposalObjective,
  onSaveWorkArtifact,
  onOpenSavedWorkArtifact,
  onRegenerateWorkArtifact,
  onRetryGoal,
  onViewArtifactFullscreen,
  onSaveImage,
  onRegenerateImage,
  resolvingProposal = false,
  retryingReply = false,
  savingImage = false,
  regeneratingImage = false,
  imageSaved = false,
  savingWork = false,
  regeneratingWork = false,
  retryingGoal = false,
  workSaved = false,
  workDriveSaveAvailability = 'checking',
}: MessageBubbleProps) {
  const workDetailsTriggerRef = useRef<View>(null);
  const workProjectTriggerRef = useRef<View>(null);
  const projectTriggerRef = useRef<View>(null);
  const lifecycle = scoutReplyLifecyclePresentation(message);
  const workThread = workThreadPresentation(message);
  const explicitResultArtifactId = String(workThread?.ref.resultArtifactId ?? '').trim();
  const authoredResultQuality = String(workThread?.resultQualityState ?? '').trim().toLowerCase();
  const managedAuthoredResult = Boolean(explicitResultArtifactId) && (
    String(workThread?.ref.mode ?? '').trim().toLowerCase() === 'goal'
    || authoredResultQuality !== ''
  );
  const authoredResultAdmitted = managedAuthoredResult
    && authoredResultQuality === 'admitted';
  const authoredResultNeedsAttention = managedAuthoredResult && !authoredResultAdmitted;
  const actionableAuthoredDraft = authoredResultNeedsAttention
    && workThread?.ref.resultCanContinue === true;
  const failedGoal = Boolean(
    (workThread?.failed || actionableAuthoredDraft)
    && !workThread.governedRecord
    && String(workThread.ref.mode ?? '').trim().toLowerCase() === 'goal'
    && String(workThread.ref.artifactId ?? '').trim(),
  );
  const retryingFailedWork = failedGoal ? retryingGoal : regeneratingWork;
  const retryFailedWork = () => failedGoal ? onRetryGoal?.(message) : onRegenerateWorkArtifact?.(message);
  const inlineArtifactKind = workThread ? detectInlineArtifactKind(message) : null;
  const directArtifactMode = /^(html_deck|deck|presentation|slides?)$/u.test(String(workThread?.ref.mode ?? '').toLowerCase());
  // A goal/process artifact id owns lifecycle, not media. Only an explicit
  // ResultArtifactID may drive its preview/actions. Legacy standalone deck
  // runs may use ArtifactID after terminal completion because that id is the
  // deliverable itself, not a goal root.
  const richArtifactId = explicitResultArtifactId || (
    workThread?.complete && directArtifactMode
      ? String(workThread.ref.artifactId ?? '').trim()
      : ''
  );
  const authoredResultCanPresent = Boolean(richArtifactId)
    && (!managedAuthoredResult || (authoredResultAdmitted && workThread?.ref.resultCanPresent === true));
  const showRichWorkResult = Boolean(richArtifactId) || Boolean(workThread?.active && workThread.family === 'Presentation');
  const proposal = message.proposal;
  const proposalKind = String(proposal?.kind ?? '').toLowerCase();
  const proposalStatus = String(proposal?.status ?? '').toLowerCase();
  const exactApproval = String(message.intentOutcome ?? proposal?.intentOutcome ?? '') === 'approval_required'
    || String(proposal?.effectClass ?? '').trim() !== '';
  const workProposal = Boolean(proposal) && (exactApproval || ['workstream', 'tool_run', 'goal_run'].includes(proposalKind));
  const proposalPending = workProposal && !proposalStatus;
  const acceptedProposalLabel = proposalKind === 'workstream'
    ? `${proposal?.agentName || 'Scout'} started this workstream`
    : proposalKind === 'goal_run'
      ? `${proposal?.agentName || 'Scout'} started this goal`
      : `${proposal?.agentName || 'Scout'} started this work`;
  const rawBody = bodyOf(message);
  const body = rawBody || (!lifecycle?.active ? lifecycle?.fallbackText ?? '' : '');
  const messageKind = String(message.kind ?? '').toLowerCase();
  const generatedImagePending = messageKind === 'image_pending';
  const generatedImage = messageKind === 'image' ? generatedImageFile(message) : null;
  const generatedImageUrl = generatedImage ? authenticatedFileUrl(generatedImage) : '';
  const { getMappingKey } = useMappingHelper();
  const files = Array.isArray(message.files) ? message.files : [];
  const scout = isScout(message);
  const replyTo = message.replyTo;
  const viaScout = String(message.postedOnBehalfOf ?? '').trim() !== '';
  const activity = message.activity;
  const publication = message.publication;
  const [activityExpanded, setActivityExpanded] = useState(false);
  const sources = Array.isArray(message.sources) ? message.sources : [];
  // Live html_deck: Scout's deliverable path — not a long message, not a text bubble
  const isLiveHtmlDeck = scout && isHtmlDeckBody(body);
  // html_deck bodies are always >700 chars, but they render as 16:9 deck, not long message
  const longMessage = !isLiveHtmlDeck && (body.length > 700 || body.split('\n').length > 12);
  // A work-reference row is authored by the durable coworker named on the
  // server-owned ref. `delegatedBy` describes the handoff; it never relabels
  // Colton/Marvin's output as a generic Scout response.
  const authorName = workThread?.agentName ?? (scout ? 'Scout' : own ? 'You' : String(message.authorName ?? 'Someone'));
  const inlineBody = longMessage && !scout ? shortenedMessage(body, 560) : body;
  const segments = useMemo(() => parseMessageTextSegments(inlineBody), [inlineBody]);
  const urls = useMemo(() => extractHttpUrls(body), [body]);
  const firstURL = urls[0]?.url ?? '';
  const linkOnly = urls.length === 1 && body.trim() === firstURL;
  const standaloneLinkPreview = linkOnly && !replyTo;
  const attachmentOnly = !body && !replyTo && files.length > 0;
  const reactions = useMemo(
    () => groupMessageReactions(message.reactions, viewerEmail),
    [message.reactions, viewerEmail],
  );
  const timeLabel = useMemo(() => timeOf(message), [message.createdAt]);
  const projectTitle = String(message.project?.title ?? '').trim();
  const projectPending = message.project?.status === 'pending';
  const projectUnavailable = message.project?.status === 'unavailable';
  const projectRemoved = message.project?.status === 'removed';
  const projectCorrectable = own && !projectPending && !projectRemoved && Boolean(onChangeProject);
  const threadParticipants = useMemo(() => {
    const seen = new Set<string>();
    return threadReplies.filter((reply) => {
      const key = String(reply.authorEmail ?? reply.authorName ?? reply.role ?? '').trim().toLowerCase();
      if (!key || seen.has(key) || seen.size >= 3) return false;
      seen.add(key);
      return true;
    });
  }, [threadReplies]);
  const latestThreadReply = threadReplies[threadReplies.length - 1];
  const translated = useMemo(() => ({
    transform: [{ translateX: timestampReveal.interpolate({ inputRange: [0, 1], outputRange: [0, -68] }) }],
  }), [timestampReveal]);
  const timestampOpacity = useMemo(() => ({ opacity: timestampReveal }), [timestampReveal]);

  if (!body && files.length === 0 && !lifecycle && !workThread && !generatedImage) return null;

  return (
    <View style={[styles.row, own && styles.rowOwn, showAuthor && styles.rowNewAuthor, reactions.length > 0 && styles.rowWithReactions]}>
      <Animated.View pointerEvents="none" style={[styles.timestampWrap, timestampOpacity]}>
        {message.editedAt ? <Text style={styles.editedLabel}>Edited</Text> : null}
        <Text style={styles.time}>{timeLabel}</Text>
      </Animated.View>
      {!own && !scout ? (
        <View style={styles.avatarSlot}>
          {showAvatar ? (
            <ChatAvatar name={String(message.authorName ?? 'Someone')} avatarDataURL={avatarDataURL} size={28} />
          ) : null}
        </View>
      ) : null}
      <Animated.View style={[styles.stack, (workThread || workProposal) && styles.stackWork, own && styles.stackOwn, translated]}>
        <Pressable
          accessible={!(workProposal || workThread)}
          accessibilityRole="button"
          accessibilityLabel={`${own ? 'You' : authorName}: ${body || lifecycle?.label || workThread?.label || `${files.length} attachment${files.length === 1 ? '' : 's'}`}. ${message.editedAt ? 'Edited. ' : ''}${timeLabel}`}
          accessibilityHint={longMessage && !workThread ? 'Opens the full message. Touch and hold for message actions' : 'Touch and hold for message actions'}
          accessibilityActions={[{ name: 'longpress', label: 'Show message actions' }]}
          delayLongPress={messageLongPressDelayMs}
          onAccessibilityAction={(event) => {
            if (event.nativeEvent.actionName === 'longpress') onLongPress?.(message, own);
          }}
          onPress={longMessage && !workThread ? () => onOpenLongMessage?.(body, authorName, scout) : undefined}
          onLongPress={() => onLongPress?.(message, own)}
          style={[
            styles.bubble,
            own ? styles.bubbleOwn : styles.bubbleOther,
            scout && styles.bubbleScout,
            (workThread || workProposal) && styles.bubbleWork,
            standaloneLinkPreview && styles.bubbleLinkOnly,
            attachmentOnly && styles.bubbleAttachmentOnly,
          ]}
        >
          {showAuthor && !own ? (
            <Text style={[styles.author, scout && styles.authorScout]}>
		      {authorName}
            </Text>
          ) : null}

          {viaScout ? (
            <View style={[styles.viaChip, own && styles.viaChipOwn]}>
              <Text style={[styles.viaText, own && styles.viaTextOwn]}>via Scout</Text>
            </View>
          ) : null}

          {publication?.kind === 'private_riff' ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={`Shared by ${publication.sharedBy || 'a teammate'} from a private riff grounded in ${publication.sourceTitle || 'the source channel'}`}
              accessibilityHint="Opens the public source checkpoint"
              disabled={!publication.sourceThreadId || !publication.sourceThroughMessageId}
              onPress={() => onOpenSource?.({
                kind: 'chat_thread',
                threadId: publication.sourceThreadId,
                threadTitle: publication.sourceTitle,
                messageId: publication.sourceThroughMessageId,
                quote: 'Private Riff source checkpoint',
              })}
              style={({ pressed }) => [styles.publicationChip, own && styles.viaChipOwn, pressed && styles.replyContextPressed]}
            >
              <SymbolView name="guitars.fill" tintColor={own ? colors.onAccent : colors.emberText} size={12} />
              <Text style={[styles.publicationText, own && styles.viaTextOwn]}>Shared by {publication.sharedBy || 'a teammate'} from a private riff</Text>
            </Pressable>
          ) : null}

          {showReplyContext && replyTo?.messageId ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={`Replying to ${replyTo.authorName}: ${replyTo.text}`}
              accessibilityHint="Jumps to the original message"
              onPress={() => onOpenReplySource?.(replyTo.messageId)}
              onLongPress={() => onLongPress?.(message, own)}
              delayLongPress={messageLongPressDelayMs}
              style={({ pressed }) => [styles.replyContext, pressed && styles.replyContextPressed]}
            >
              <View style={[styles.replyLine, own && styles.replyLineOwn]} />
              <View style={styles.replyCopy}>
                <Text numberOfLines={1} style={[styles.replyAuthor, own && styles.replyAuthorOwn]}>{replyTo.authorName}</Text>
                <Text numberOfLines={2} style={[styles.replyText, own && styles.replyTextOwn]}>{replyTo.text}</Text>
              </View>
            </Pressable>
          ) : null}

          {lifecycle?.active ? (
            <View accessibilityLiveRegion="polite" style={styles.lifecycleActive}>
              <ActivityIndicator color={colors.emberText} size="small" />
              <Text style={styles.lifecycleActiveText}>{lifecycle.label}</Text>
            </View>
          ) : null}

          {generatedImagePending ? (
            <View accessibilityLiveRegion="polite" style={styles.generatedImagePending}>
              <ActivityIndicator color={colors.emberText} size="small" />
              <Text style={styles.generatedImagePendingText}>Generating image…</Text>
            </View>
          ) : null}

          {workProposal && !proposalPending ? (
            <View accessibilityLabel={`${proposal?.agentName || 'Scout'} work ${proposalStatus === 'accepted' ? 'launched' : 'dismissed'}`} style={styles.proposalCompact}>
              <View style={styles.proposalHead}>
                <View style={styles.workIcon}>
                  <SymbolView name={proposalStatus === 'accepted' ? 'checkmark.circle.fill' : 'xmark.circle'} tintColor={proposalStatus === 'accepted' ? colors.success : colors.text3} size={13} />
                </View>
                <Text style={styles.proposalCompactText}>{proposalStatus === 'accepted' ? acceptedProposalLabel : 'Proposed work dismissed'}</Text>
              </View>
            </View>
          ) : workProposal ? (
            <View accessibilityLabel={`${proposal?.agentName || 'Agent'} proposed work. ${proposalObjective || proposal?.objective || proposal?.summary || body}`} style={styles.proposalCard}>
              <View style={styles.proposalHead}>
                <View style={styles.workIcon}>
                  <SymbolView name="sparkles" tintColor={colors.emberText} size={13} />
                </View>
                <Text style={styles.proposalKicker}>{proposal?.agentName || 'Scout'} · proposed work</Text>
              </View>
              <Text style={styles.proposalLabel}>OBJECTIVE</Text>
              {proposalPending ? (
                <TextInput
                  accessibilityLabel={`${proposal?.agentName || 'Scout'} work objective`}
                  editable={!resolvingProposal && !exactApproval}
                  multiline
                  onChangeText={(value) => onChangeProposalObjective?.(message, value)}
                  placeholder="What should Scout accomplish?"
                  placeholderTextColor={colors.text3}
                  selectionColor={colors.info}
                  style={styles.proposalInput}
                  textAlignVertical="top"
                  value={proposalObjective ?? String(proposal?.objective ?? proposal?.summary ?? body)}
                />
              ) : (
                <Text style={styles.proposalSummary}>{proposalObjective || proposal?.objective || proposal?.summary || body}</Text>
              )}
              <Text style={styles.proposalSafety}>{exactApproval ? 'Approval is bound to this exact request. Edit by sending a new message.' : 'Nothing runs until you confirm.'}</Text>
              {proposalPending ? (
                <View style={styles.proposalActions}>
                  <Pressable
                    accessibilityRole="button"
                    accessibilityLabel={`Run ${proposal?.agentName || 'agent'} work once`}
                    accessibilityState={{ disabled: resolvingProposal }}
                    disabled={resolvingProposal}
                    onPress={() => onResolveProposal?.(message, 'accepted', String(exactApproval ? (proposal?.objective ?? proposal?.summary ?? body) : (proposalObjective ?? proposal?.objective ?? proposal?.summary ?? body)).trim())}
                    style={({ pressed }) => [styles.proposalRun, (pressed || resolvingProposal) && styles.proposalPressed]}
                  >
                    {resolvingProposal ? <ActivityIndicator color={colors.onAccent} size="small" /> : <Text style={styles.proposalRunText}>Run once</Text>}
                  </Pressable>
                  <Pressable
                    accessibilityRole="button"
                    accessibilityLabel="Dismiss proposed work"
                    accessibilityState={{ disabled: resolvingProposal }}
                    disabled={resolvingProposal}
                    onPress={() => onResolveProposal?.(message, 'dismissed', String(proposalObjective ?? proposal?.objective ?? proposal?.summary ?? body).trim())}
                    style={({ pressed }) => [styles.proposalDismiss, (pressed || resolvingProposal) && styles.proposalPressed]}
                  >
                    <Text style={styles.proposalDismissText}>Not now</Text>
                  </Pressable>
                </View>
              ) : null}
            </View>
          ) : workThread
            && !(String(workThread.ref.projectTitle ?? '').trim() && workThread.family === 'Research')
            && showRichWorkResult ? (
            <View style={styles.richWorkResult}>
              <InlineArtifactPreview
                kind={inlineArtifactKind ?? 'html_deck'}
                title={String(workThread.ref.resultTitle ?? '').trim() || 'Presentation'}
                text={String(workThread.ref.resultPreview ?? '')}
                agentName={workThread.agentName}
                loading={workThread.active && !richArtifactId}
                needsAttention={workThread.failed || authoredResultNeedsAttention}
                artifactId={richArtifactId || undefined}
                sessionToken={sessionToken}
                desktopEditingOnly={Boolean(inlineArtifactKind === 'html_deck' && richArtifactId && workThread.ref.resultCanEdit === true)}
                onEdit={inlineArtifactKind === 'document' && richArtifactId && workThread.ref.resultCanEdit === true ? () => onOpenWorkArtifact?.(message) : undefined}
                onPresent={authoredResultCanPresent ? () => onViewArtifactFullscreen?.(message) : undefined}
                onExpand={richArtifactId && (inlineArtifactKind !== 'html_deck' || authoredResultCanPresent) ? () => onViewArtifactFullscreen?.(message) : undefined}
              />
              {authoredResultQuality === 'edited_after_admission' ? (
                <Text accessibilityRole="alert" style={styles.workAttentionCopy}>Edited draft · fresh rendered review is required before presenting or exporting</Text>
              ) : workThread.ref.resultApprovalState === 'edited_after_approval' ? (
                <Text accessibilityRole="alert" style={styles.workAttentionCopy}>Edited after approval · review this version before presenting</Text>
              ) : null}
              {(workThread.failed || authoredResultNeedsAttention) && richArtifactId ? (
                <View accessible accessibilityRole="summary" style={styles.checkpointCard}>
                  <View style={styles.checkpointStatusRow}>
                    <SymbolView name="exclamationmark.triangle.fill" tintColor={colors.emberText} size={14} />
                    <Text style={styles.checkpointKicker}>Draft · needs attention</Text>
                  </View>
                  <Text style={styles.checkpointQuestion}>{authoredResultQuality === 'edited_after_admission'
                    ? 'Your saved changes need a fresh rendered review before this version can be presented or exported.'
                    : workThread.attentionCopy || 'Scout saved the best current draft, but it has not passed the final quality review.'}</Text>
                  <View style={styles.workResultActions}>
                    {workThread.ref.resultCanContinue === true ? <Pressable accessibilityRole="button" accessibilityLabel={authoredResultQuality === 'edited_after_admission' ? 'Review saved changes' : 'Continue draft review'} accessibilityState={{ disabled: retryingFailedWork }} disabled={retryingFailedWork} onPress={retryFailedWork} style={({ pressed }) => [styles.workResultAction, pressed && styles.workResultPressed, retryingFailedWork && styles.workResultDisabled]}>
                      {retryingFailedWork ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="arrow.clockwise" tintColor={colors.emberText} size={14} />}
                      <Text style={styles.workResultActionText}>{retryingFailedWork ? 'Starting…' : authoredResultQuality === 'edited_after_admission' ? 'Review changes' : 'Continue'}</Text>
                    </Pressable> : null}
                  </View>
                </View>
              ) : null}
              {workThread.needsInput && workThread.ref.checkpoint?.question ? (
                <View accessible accessibilityRole="summary" style={styles.checkpointCard}>
                  <View style={styles.checkpointStatusRow}>
                    <SymbolView name="questionmark.circle.fill" tintColor={colors.emberText} size={14} />
                    <Text style={styles.checkpointKicker}>Scout needs your decision</Text>
                  </View>
                  <Text style={styles.checkpointQuestion}>{workThread.ref.checkpoint.question}</Text>
                  <View style={styles.checkpointChoices}>
                    {(workThread.ref.checkpoint.options ?? []).map((option) => (
                      <Pressable
                        key={option.id}
                        accessibilityRole="button"
                        accessibilityLabel={option.label}
                        onPress={() => onResolveWorkCheckpoint?.(message, option)}
                        style={({ pressed }) => [styles.checkpointChoice, pressed && styles.workResultPressed]}
                      >
                        <Text style={styles.checkpointChoiceText}>{option.label}</Text>
                      </Pressable>
                    ))}
                  </View>
                </View>
              ) : null}
            </View>
          ) : workThread ? (
            <View style={styles.workCard}>
              <View style={styles.workHead}>
                <View
                  accessible
                  accessibilityRole="summary"
                  accessibilityLabel={`${workThread.agentName}, ${workThread.family} workstream. ${workThread.query}`}
                  style={styles.workIdentity}
                >
                  <View style={styles.workIcon}>
                    <SymbolView name="flame.fill" tintColor={colors.emberText} size={13} />
                  </View>
			      <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workKicker}>
				        {workThread.agentName} · {workThread.family}{workThread.delegatedBy ? ` · via ${workThread.delegatedBy}` : ''}
				      </Text>
                </View>
			    <View
                  accessible
                  role="status"
                  accessibilityLabel={`${workThread.family}: ${workThread.label}`}
                  accessibilityLiveRegion="polite"
                  style={[styles.workStatus, workThread.complete && styles.workStatusComplete, workThread.needsInput && styles.workStatusNeedsInput, workThread.failed && styles.workStatusFailed]}
                >
                  {workThread.active ? <ActivityIndicator color={colors.emberText} size="small" /> : null}
                  {!workThread.active ? (
                    <SymbolView
				      name={workThread.complete ? 'checkmark.circle.fill' : workThread.needsInput ? 'questionmark.circle.fill' : 'exclamationmark.circle.fill'}
				      tintColor={workThread.failed ? colors.danger : workThread.needsInput ? colors.emberText : colors.success}
                      size={13}
                    />
                  ) : null}
				  <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={[styles.workStatusText, workThread.needsInput && styles.workStatusTextNeedsInput, workThread.failed && styles.workStatusTextFailed]}>{workThread.label}</Text>
                </View>
              </View>
              <Text accessibilityRole="header" maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workQuery}>{String(workThread.ref.resultTitle ?? '').trim() || 'Work'}</Text>
              {String(workThread.ref.projectTitle ?? '').trim() ? (
                <View accessible accessibilityLabel={`Project: ${String(workThread.ref.projectTitle).trim()}`} style={styles.workProjectChip}>
                  <SymbolView name="folder.fill" tintColor={colors.emberText} size={12} />
                  <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} numberOfLines={1} style={styles.workProjectText}>Project · {String(workThread.ref.projectTitle).trim()}</Text>
                </View>
              ) : null}
              {workThread.active ? (
                <Text
                  accessibilityRole="progressbar"
                  accessibilityValue={{ min: 0, max: 100, now: workThread.progressPercent }}
                  maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier}
                  style={styles.workProgressCopy}
                >
                  {workThread.progressCopy}{workThread.progressPercent > 0 ? ` · ${workThread.progressPercent}%` : ''}
                </Text>
              ) : null}
              {workThread.failed && workThread.attentionCopy ? <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workAttentionCopy}>{workThread.attentionCopy}</Text> : null}
              {String(workThread.ref.resultPreview ?? '').trim() ? <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} numberOfLines={3} style={styles.workPreview}>{String(workThread.ref.resultPreview)}</Text> : null}
              {String(workThread.ref.provenance ?? '').trim() ? <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} numberOfLines={2} style={styles.workProvenance}>{String(workThread.ref.provenance)}</Text> : null}
              {workThread.needsInput && workThread.ref.checkpoint?.question ? (
                <View accessible accessibilityRole="summary" style={styles.checkpointCard}>
                  <View style={styles.checkpointStatusRow}>
                    <SymbolView name="questionmark.circle.fill" tintColor={colors.emberText} size={14} />
                    <Text style={styles.checkpointKicker}>Scout needs your decision</Text>
                  </View>
                  <Text style={styles.checkpointQuestion}>{workThread.ref.checkpoint.question}</Text>
                  <View style={styles.checkpointChoices}>
                    {(workThread.ref.checkpoint.options ?? []).map((option) => (
                      <Pressable
                        key={option.id}
                        accessibilityRole="button"
                        accessibilityLabel={option.label}
                        onPress={() => onResolveWorkCheckpoint?.(message, option)}
                        style={({ pressed }) => [styles.checkpointChoice, pressed && styles.workResultPressed]}
                      >
                        <Text style={styles.checkpointChoiceText}>{option.label}</Text>
                      </Pressable>
                    ))}
                  </View>
                </View>
              ) : null}
              {workThread.complete ? (
                <View style={styles.workResultActions}>
                  <Pressable ref={workDetailsTriggerRef} accessibilityRole="button" accessibilityLabel="Open deliverable" onPress={() => onOpenWorkArtifact?.(message, findNodeHandle(workDetailsTriggerRef.current) ?? undefined)} style={({ pressed }) => [styles.workResultPrimary, pressed && styles.workResultPressed]}>
                    <SymbolView name="doc.text.fill" tintColor={colors.onAccent} size={14} />
                    <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workResultPrimaryText}>Open</Text>
                  </Pressable>
                  {!workThread.governedRecord ? <Pressable accessibilityRole="button" accessibilityLabel={workSaved ? 'Open saved deliverable in Drive' : workDriveSaveAvailability === 'available' ? 'Save deliverable to Drive' : workDriveSaveAvailability === 'checking' ? 'Checking Save to Drive availability' : 'Save to Drive unavailable'} accessibilityState={{ disabled: savingWork || (!workSaved && workDriveSaveAvailability !== 'available') }} disabled={savingWork || (!workSaved && workDriveSaveAvailability !== 'available')} onPress={() => workSaved ? onOpenSavedWorkArtifact?.(message) : onSaveWorkArtifact?.(message)} style={({ pressed }) => [styles.workResultAction, pressed && styles.workResultPressed, (savingWork || (!workSaved && workDriveSaveAvailability !== 'available')) && styles.workResultDisabled]}>
                    {savingWork ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="externaldrive.fill" tintColor={colors.emberText} size={14} />}
                    <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workResultActionText}>{workSaved ? 'Open in Drive' : savingWork ? 'Saving…' : workDriveSaveAvailability === 'checking' ? 'Checking…' : workDriveSaveAvailability === 'unavailable' ? 'Unavailable' : 'Save'}</Text>
                  </Pressable> : null}
                  {!workThread.governedRecord ? <Pressable accessibilityRole="button" accessibilityLabel="Edit prompt and regenerate deliverable" accessibilityState={{ disabled: regeneratingWork }} disabled={regeneratingWork} onPress={() => onRegenerateWorkArtifact?.(message)} style={({ pressed }) => [styles.workResultAction, pressed && styles.workResultPressed, regeneratingWork && styles.workResultDisabled]}>
                    {regeneratingWork ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="arrow.clockwise" tintColor={colors.emberText} size={14} />}
                    <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workResultActionText}>{regeneratingWork ? 'Starting…' : 'Regenerate'}</Text>
                  </Pressable> : null}
                  {String(workThread.ref.artifactId ?? '').trim() && onChangeWorkProject ? <Pressable ref={workProjectTriggerRef} accessibilityRole="button" accessibilityLabel={`${String(workThread.ref.projectTitle ?? '').trim() ? 'Change' : 'Set'} project for this Work`} accessibilityHint="Corrects the Work result and future continuity without changing the source conversation." onPress={() => onChangeWorkProject(message, findNodeHandle(workProjectTriggerRef.current) ?? undefined)} style={({ pressed }) => [styles.workResultAction, pressed && styles.workResultPressed]}>
                    <SymbolView name="folder.fill" tintColor={colors.emberText} size={14} />
                    <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workResultActionText}>{String(workThread.ref.projectTitle ?? '').trim() ? 'Change project' : 'Set project'}</Text>
                  </Pressable> : null}
                </View>
              ) : workThread.failed ? (
                <View style={styles.workResultActions}>
                  <Pressable ref={workDetailsTriggerRef} accessibilityRole="button" accessibilityLabel={`View ${workThread.family.toLowerCase()} failure details`} onPress={() => onViewArtifactFullscreen?.(message)} style={({ pressed }) => [styles.workResultPrimary, pressed && styles.workResultPressed]}>
                    <SymbolView name="info.circle.fill" tintColor={colors.onAccent} size={14} />
                    <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workResultPrimaryText}>View details</Text>
                  </Pressable>
                  <Pressable accessibilityRole="button" accessibilityLabel={`Retry ${workThread.family.toLowerCase()}`} accessibilityState={{ disabled: retryingFailedWork }} disabled={retryingFailedWork} onPress={retryFailedWork} style={({ pressed }) => [styles.workResultAction, pressed && styles.workResultPressed, retryingFailedWork && styles.workResultDisabled]}>
                    {retryingFailedWork ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="arrow.clockwise" tintColor={colors.emberText} size={14} />}
                    <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workResultActionText}>{retryingFailedWork ? 'Starting…' : 'Retry'}</Text>
                  </Pressable>
                </View>
              ) : (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel="Open live work details"
                  onPress={() => onOpenWorkArtifact?.(message, findNodeHandle(workDetailsTriggerRef.current) ?? undefined)}
                  ref={workDetailsTriggerRef}
                  style={({ pressed }) => [styles.workFoot, pressed && styles.workResultPressed]}
                >
                  <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workFootText}>{workThread.customerPhase?.displayLabel ?? (workThread.progressPercent > 0 ? `${workThread.progressPercent}% complete` : `${workThread.family} in progress`)}</Text>
                  <Text maxFontSizeMultiplier={workSurfaceMaxFontSizeMultiplier} style={styles.workResultActionText}>View activity</Text>
                </Pressable>
              )}
            </View>
          ) : null}

          {!generatedImagePending && !workProposal && !workThread && body && lifecycle?.state === 'canceled' ? (
            <Text style={styles.lifecycleCanceled}>{body}</Text>
          ) : !generatedImagePending && !workThread && body && !linkOnly && scout && isHtmlDeckBody(body) ? (
            /* Scout's live html_deck deliverable — real 16:9 in-thread view, not text bubble */
            <InlineArtifactPreview
              kind="html_deck"
              title={extractDeckTitle(body)}
              text=""
              agentName="Scout"
              htmlContent={body}
              onPresent={onViewArtifactFullscreen ? () => onViewArtifactFullscreen?.(message) : undefined}
            />
          ) : !generatedImagePending && !workThread && body && !linkOnly && scout ? (
            <ScoutRichText text={body} maxCharacters={longMessage ? 560 : undefined} />
          ) : !generatedImagePending && !workThread && body && !linkOnly ? (
            <Text style={[styles.body, own && styles.bodyOwn]}>
              {segments.map((segment, index) => {
                const mappingKey = getMappingKey(`${segment.kind}-${segment.text}`, index);
                if (segment.kind === 'text') return <React.Fragment key={mappingKey}>{segment.text}</React.Fragment>;
                if (segment.kind === 'link') {
                  return (
                    <Text
                      key={mappingKey}
                      accessibilityRole="link"
                      onPress={() => void Linking.openURL(segment.url).catch(() => undefined)}
                      style={[styles.link, own && styles.linkOwn]}
                    >
                      {segment.text}
                    </Text>
                  );
                }
                return (
                  <Text
                    key={mappingKey}
                    accessibilityLabel={`Mention ${segment.text.replace(/^@/, '')}`}
                    style={[
                      styles.mention,
                      own && styles.mentionOwn,
                      segment.scout && styles.mentionScout,
                      segment.scout && own && styles.mentionScoutOwn,
                    ]}
                  >
                    {segment.text.replace(/^@/, '')}
                  </Text>
                );
              })}
            </Text>
          ) : null}

          {generatedImage && generatedImageUrl ? (
            <View style={styles.generatedImageCard}>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={`Open ${message.image?.prompt || 'generated image'}`}
                onPress={() => onOpenAttachment?.(generatedImage)}
                style={({ pressed }) => [styles.generatedImagePreview, pressed && styles.generatedImagePressed]}
              >
                <Image
                  source={{ uri: generatedImageUrl, headers: authenticatedFileHeaders(sessionToken, generatedImage.mime) }}
                  cachePolicy="memory-disk"
                  contentFit="cover"
                  enforceEarlyResizing
                  recyclingKey={generatedImage.ref}
                  transition={180}
                  style={styles.generatedImageVisual}
                />
              </Pressable>
              <View style={styles.generatedImageActions}>
                {message.image?.artifactId ? (
                  <Pressable
                    accessibilityRole="button"
                    accessibilityLabel={imageSaved ? 'Image saved to Drive' : 'Save image to Drive'}
                    accessibilityState={{ disabled: imageSaved || savingImage }}
                    disabled={imageSaved || savingImage}
                    onPress={() => onSaveImage?.(message)}
                    style={({ pressed }) => [styles.generatedImageAction, pressed && styles.generatedImagePressed, (imageSaved || savingImage) && styles.generatedImageActionDisabled]}
                  >
                    {savingImage ? <ActivityIndicator color={colors.emberText} size="small" /> : null}
                    <Text style={styles.generatedImageActionText}>{imageSaved ? 'Saved to Drive' : savingImage ? 'Saving…' : 'Save to Drive'}</Text>
                  </Pressable>
                ) : null}
                {message.image?.prompt ? (
                  <Pressable
                    accessibilityRole="button"
                    accessibilityLabel="Edit prompt and regenerate image"
                    accessibilityState={{ disabled: regeneratingImage }}
                    disabled={regeneratingImage}
                    onPress={() => onRegenerateImage?.(message)}
                    style={({ pressed }) => [styles.generatedImageAction, pressed && styles.generatedImagePressed, regeneratingImage && styles.generatedImageActionDisabled]}
                  >
                    {regeneratingImage ? <ActivityIndicator color={colors.emberText} size="small" /> : null}
                    <Text style={styles.generatedImageActionText}>{regeneratingImage ? 'Regenerating…' : 'Regenerate'}</Text>
                  </Pressable>
                ) : null}
              </View>
            </View>
          ) : null}

          {lifecycle?.state === 'failed' && lifecycle.retryable && onRetryReply ? (
            <Pressable
              accessibilityLabel="Retry Scout reply"
              accessibilityRole="button"
              accessibilityState={{ disabled: retryingReply }}
              disabled={retryingReply}
              onPress={() => onRetryReply(message)}
              style={({ pressed }) => [styles.lifecycleRetry, pressed && styles.lifecycleRetryPressed]}
            >
              {retryingReply ? <ActivityIndicator color={colors.emberText} size="small" /> : null}
              <Text style={styles.lifecycleRetryText}>{retryingReply ? 'Retrying' : 'Retry'}</Text>
            </Pressable>
          ) : null}

          {longMessage && !workThread ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={scout ? 'Read full Scout response' : 'Read full message'}
              onPress={() => onOpenLongMessage?.(body, authorName, scout)}
              style={({ pressed }) => [styles.readMore, own && styles.readMoreOwn, pressed && styles.readMorePressed]}
            >
              <Text style={[styles.readMoreText, own && styles.readMoreTextOwn]}>{scout ? 'Read full response' : 'Read full message'}</Text>
              <SymbolView name="arrow.up.left.and.arrow.down.right" size={12} tintColor={own ? colors.onAccent : colors.text2} />
            </Pressable>
          ) : null}

          {files.map((file, index) => {
            const imageAttachment = file.mime?.toLowerCase().startsWith('image/');
            const image = imageAttachment && authenticatedFileUrl(file);
            return (
              <Pressable
                key={getMappingKey(`${file.ref}-${file.name}`, index)}
                accessibilityRole="button"
                accessibilityLabel={`Open ${file.name}`}
                onPress={() => onOpenAttachment?.(file)}
                onLongPress={() => onLongPress?.(message, own, { file, index })}
                delayLongPress={messageLongPressDelayMs}
                style={({ pressed }) => [
                  styles.attachment,
                  image && styles.attachmentMedia,
                  image && styles.attachmentVisual,
                  pressed && styles.attachmentPressed,
                ]}
              >
                {image ? (
                  <>
                    <Image
                      source={{ uri: image, headers: authenticatedFileHeaders(sessionToken, file.mime) }}
                      cachePolicy="memory-disk"
                      contentFit="cover"
                      enforceEarlyResizing
                      recyclingKey={file.ref}
                      style={styles.attachmentImage}
                    />
                  </>
                ) : (
                  <>
                    <View style={styles.fileIcon}>
                      <SymbolView name="doc.richtext" tintColor={colors.text2} size={20} />
                    </View>
                    <Text numberOfLines={2} style={styles.attachmentText}>{attachmentLabel(file)}</Text>
                    <SymbolView name="chevron.right" tintColor={colors.text3} size={12} />
                  </>
                )}
              </Pressable>
            );
          })}

          {projectTitle && !projectRemoved ? (
            <Pressable
              ref={projectTriggerRef}
              accessible
              accessibilityRole={projectCorrectable ? 'button' : undefined}
              accessibilityState={projectPending ? { disabled: true } : undefined}
              accessibilityLabel={projectCorrectable
                ? `${projectUnavailable ? 'Project unavailable' : 'Project'}: ${projectTitle}. Change project`
                : `${projectPending ? 'Project link pending' : projectUnavailable ? 'Project unavailable' : 'Project'}: ${projectTitle}`}
              accessibilityHint={projectCorrectable ? 'Opens authorized Project choices for only this message.' : undefined}
              disabled={!projectCorrectable}
              onPress={() => onChangeProject?.(message, findNodeHandle(projectTriggerRef.current) ?? undefined)}
              style={[styles.projectContext, own && styles.projectContextOwn]}
            >
              <SymbolView name="folder.fill" tintColor={own ? colors.onAccent : colors.text3} size={10} />
              <Text numberOfLines={1} style={[styles.projectContextText, own && styles.projectContextTextOwn]}>
                {projectPending ? 'Linking' : projectUnavailable ? 'Unavailable' : 'Project'} · {projectTitle}
              </Text>
            </Pressable>
          ) : null}

          {firstURL ? (
            <LinkPreviewCard
              url={firstURL}
              sessionToken={sessionToken}
              own={own}
              seamless={standaloneLinkPreview}
              onLongPress={() => onLongPress?.(message, own)}
            />
          ) : null}
        </Pressable>

        {reactions.length > 0 ? (
          <View style={[styles.reactions, own ? styles.reactionsOwn : styles.reactionsOther]}>
            {reactions.map((reaction, index) => (
              <Pressable
                key={getMappingKey(reaction.emoji, index)}
                accessibilityRole="button"
                accessibilityLabel={`${reaction.emoji}, ${reaction.count} reaction${reaction.count === 1 ? '' : 's'}`}
                onPress={() => onToggleReaction?.(message, reaction.emoji, !reaction.reactedByViewer)}
                hitSlop={{ top: 3, bottom: 3 }}
                style={({ pressed }) => [styles.reactionChip, reaction.reactedByViewer && styles.reactionChipOwn, pressed && styles.reactionPressed]}
              >
                <Text style={styles.reactionEmoji}>{reaction.emoji}</Text>
              </Pressable>
            ))}
          </View>
        ) : null}

        {threadReplies.length > 0 && !replyTo?.messageId ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Open ${threadReplies.length} ${threadReplies.length === 1 ? 'reply' : 'replies'}`}
            accessibilityHint="Opens the thread in a dismissible sheet"
            onPress={() => onOpenThread?.(message)}
            style={({ pressed }) => [styles.threadSummary, own && styles.threadSummaryOwn, pressed && styles.threadSummaryPressed]}
          >
            <View style={styles.threadAvatars}>
              {threadParticipants.map((participant, index) => (
                <View key={String(participant.id)} style={[styles.threadAvatar, index > 0 && styles.threadAvatarOverlap]}>
                  <ChatAvatar
                    name={String(participant.authorName ?? (isScout(participant) ? 'Scout' : 'Teammate'))}
                    avatarDataURL={String(participant.avatarDataURL ?? '') || undefined}
                    size={22}
                  />
                </View>
              ))}
            </View>
            <View style={styles.threadSummaryCopy}>
              <Text style={styles.threadSummaryCount}>
                {threadReplies.length} {threadReplies.length === 1 ? 'reply' : 'replies'}
              </Text>
              <Text numberOfLines={1} style={styles.threadSummaryLatest}>
                {latestThreadReply ? `Latest ${timeOf(latestThreadReply)}` : 'Open thread'}
              </Text>
            </View>
          </Pressable>
        ) : null}

          {scout && sources.length > 0 ? (
          <View style={styles.sources}>
            {sources.map((source, index) => (
              <Pressable
				key={getMappingKey(source.messageId || source.segmentId || `source-${index}`, index)}
                accessibilityRole="button"
                accessibilityLabel={`Source: ${source.author || 'a message'} — ${source.quote}`}
				accessibilityHint={source.kind === 'meeting_transcript' ? 'Opens the exact Meeting Record transcript interval' : 'Scrolls to the source message'}
				onPress={() => onOpenSource?.(source)}
                style={({ pressed }) => [styles.sourceChip, pressed && styles.sourcePressed]}
              >
                <SymbolView name="quote.opening" tintColor={colors.emberText} size={10} />
                <Text style={styles.sourceText} numberOfLines={1}>{source.author || 'message'}</Text>
              </Pressable>
            ))}
          </View>
          ) : null}
          {scout && activity?.status === 'completed' ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={`Worked ${Math.max(0, Math.round(Number(activity.elapsedMs ?? 0) / 1000))} seconds. Considered ${Math.max(0, Number(activity.sourceCount ?? 0))} sources. ${activity.rationale || ''}`}
              accessibilityHint={activity.rationale ? 'Shows or hides the safe answer rationale. Hidden chain-of-thought is never shown.' : undefined}
              accessibilityState={{ expanded: activityExpanded }}
              onPress={() => setActivityExpanded((current) => !current)}
              style={({ pressed }) => [styles.answerActivity, pressed && styles.replyContextPressed]}
            >
              <View style={styles.answerActivityHead}>
                <SymbolView name="checkmark.circle.fill" tintColor={colors.success} size={12} />
                <Text style={styles.answerActivityTitle}>
                  Worked {Math.max(0, Math.round(Number(activity.elapsedMs ?? 0) / 1000))}s · Considered {Math.max(0, Number(activity.sourceCount ?? 0))} sources
                </Text>
                {activity.rationale ? <SymbolView name={activityExpanded ? 'chevron.up' : 'chevron.down'} tintColor={colors.text3} size={10} /> : null}
              </View>
              {activityExpanded && activity.rationale ? <Text style={styles.answerActivityRationale}>{activity.rationale}</Text> : null}
            </Pressable>
          ) : null}
      </Animated.View>
    </View>
  );
});

const styles = StyleSheet.create({
  row: { flexDirection: 'row', paddingHorizontal: space[4], marginBottom: 3 },
  rowNewAuthor: { marginTop: space[3] },
  rowWithReactions: { marginTop: space[5] },
  rowOwn: { justifyContent: 'flex-end' },
  timestampWrap: { position: 'absolute', top: 0, right: space[4], bottom: 0, justifyContent: 'center' },
  avatarSlot: { width: 34, alignSelf: 'stretch', alignItems: 'flex-start', justifyContent: 'flex-end', paddingBottom: 1 },
  time: { fontSize: 11, lineHeight: 13, fontVariant: ['tabular-nums'], color: colors.text3, textAlign: 'right' },
  editedLabel: { fontSize: 10, lineHeight: 12, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', color: colors.text3, textAlign: 'right' },
  stack: { maxWidth: '82%', alignItems: 'flex-start' },
  stackWork: { width: '100%', maxWidth: '100%', alignSelf: 'stretch' },
  stackOwn: { alignItems: 'flex-end' },
  bubble: { paddingHorizontal: space[4], paddingVertical: 10, borderRadius: radius.lg, gap: 2 },
  bubbleWork: { width: '100%', maxWidth: 720, alignSelf: 'stretch' },
  richWorkResult: { width: '100%', gap: space[3] },
  checkpointCard: { gap: space[3], padding: space[4], borderRadius: radius.lg, backgroundColor: colors.surface2, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.ember },
  checkpointStatusRow: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  checkpointKicker: { ...type.captionMedium, color: colors.emberText },
  checkpointQuestion: { ...type.body, color: colors.text1 },
  checkpointChoices: { flexDirection: 'row', flexWrap: 'wrap', gap: space[2] },
  checkpointChoice: { minHeight: 42, justifyContent: 'center', paddingHorizontal: space[4], paddingVertical: space[2], borderRadius: radius.full, backgroundColor: colors.emberSoft },
  checkpointChoiceText: { ...type.captionMedium, color: colors.emberText },
  bubbleOther: { backgroundColor: colors.surface1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, borderBottomLeftRadius: radius.sm },
  bubbleOwn: { backgroundColor: colors.accent, borderBottomRightRadius: radius.sm },
  bubbleScout: { backgroundColor: colors.surface1, borderColor: colors.ember },
  bubbleLinkOnly: { overflow: 'visible', paddingHorizontal: 0, paddingVertical: 0, borderWidth: 0, backgroundColor: 'transparent' },
  bubbleAttachmentOnly: { overflow: 'visible', paddingHorizontal: 0, paddingVertical: 0, borderWidth: 0, backgroundColor: 'transparent' },
  author: { fontSize: 13, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', letterSpacing: -0.05, lineHeight: 17, color: colors.text2, marginBottom: 3 },
  authorScout: { color: colors.emberText },
  viaChip: { alignSelf: 'flex-start', paddingHorizontal: 7, paddingVertical: 2, borderRadius: radius.full, backgroundColor: colors.emberSoft, marginBottom: 4 },
  viaChipOwn: { backgroundColor: 'rgba(0,0,0,0.08)' },
  viaText: { fontSize: 10, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', letterSpacing: 0.3, color: colors.emberText },
  viaTextOwn: { color: colors.onAccent },
  publicationChip: { alignSelf: 'flex-start', minHeight: 28, flexDirection: 'row', alignItems: 'center', gap: 5, paddingHorizontal: 8, paddingVertical: 4, borderRadius: radius.full, backgroundColor: colors.emberSoft, marginBottom: 4 },
  publicationText: { ...type.captionMedium, color: colors.emberText },
  replyContext: { minWidth: 176, maxWidth: 270, flexDirection: 'row', gap: space[2], paddingVertical: 5, marginBottom: 4 },
  replyContextPressed: { opacity: 0.7 },
  replyLine: { width: 3, alignSelf: 'stretch', borderRadius: radius.full, backgroundColor: colors.info },
  replyLineOwn: { backgroundColor: colors.onAccent },
  replyCopy: { flex: 1 },
  replyAuthor: { ...type.captionMedium, color: colors.info },
  replyAuthorOwn: { color: colors.onAccent },
  replyText: { ...type.caption, color: colors.text2 },
  replyTextOwn: { color: colors.onAccent, opacity: 0.68 },
  body: { ...type.body, color: colors.text1 },
  bodyOwn: { color: colors.onAccent },
  lifecycleActive: { minHeight: 28, flexDirection: 'row', alignItems: 'center', gap: space[2] },
  lifecycleActiveText: { ...type.bodySm, color: colors.text2 },
  lifecycleCanceled: { ...type.bodySm, color: colors.text3 },
  projectContext: { minHeight: 24, maxWidth: 250, flexDirection: 'row', alignItems: 'center', gap: 5, marginTop: space[1], paddingHorizontal: 7, borderRadius: radius.full, backgroundColor: colors.surface3 },
  projectContextOwn: { backgroundColor: 'rgba(255,255,255,0.14)' },
  projectContextText: { ...type.label, color: colors.text3, flexShrink: 1 },
  projectContextTextOwn: { color: colors.onAccent, opacity: 0.78 },
  generatedImagePending: { minHeight: 32, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[1] },
  generatedImagePendingText: { ...type.bodySm, color: colors.emberText },
  generatedImageCard: { width: 252, maxWidth: '100%', gap: space[2], marginTop: space[2] },
  generatedImagePreview: { overflow: 'hidden', borderRadius: radius.lg, borderCurve: 'continuous', borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface3 },
  generatedImageVisual: { width: '100%', aspectRatio: 1, backgroundColor: colors.surface3 },
  generatedImageActions: { flexDirection: 'row', flexWrap: 'wrap', gap: space[2] },
  generatedImageAction: { minHeight: 40, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6, paddingHorizontal: space[3], borderRadius: radius.full, borderCurve: 'continuous', backgroundColor: colors.emberSoft },
  generatedImageActionDisabled: { opacity: 0.62 },
  generatedImageActionText: { ...type.captionMedium, color: colors.emberText },
  generatedImagePressed: { opacity: 0.74, transform: [{ scale: 0.98 }] },
  lifecycleRetry: { minHeight: 34, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 6, marginTop: space[2], paddingHorizontal: 10, borderRadius: radius.full, backgroundColor: colors.emberSoft },
  lifecycleRetryPressed: { opacity: 0.68, transform: [{ scale: 0.98 }] },
  lifecycleRetryText: { ...type.captionMedium, color: colors.emberText },
  workCard: { width: '100%', minWidth: 0, maxWidth: 340, alignSelf: 'stretch', gap: space[3], paddingVertical: 5 },
  proposalCard: { width: '100%', minWidth: 0, alignSelf: 'stretch', gap: space[3], paddingVertical: 5 },
  proposalCompact: { width: '100%', minWidth: 0, alignSelf: 'stretch', paddingVertical: space[1] },
  proposalCompactText: { ...type.captionMedium, color: colors.text2, flex: 1 },
  proposalHead: { flexDirection: 'row', alignItems: 'center', gap: 7 },
  proposalKicker: { ...type.captionMedium, color: colors.emberText, flex: 1 },
  proposalLabel: { ...type.label, color: colors.text3, letterSpacing: 0.5 },
  proposalInput: { ...type.bodyMedium, minHeight: 86, padding: space[3], borderRadius: radius.lg, borderCurve: 'continuous', borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, color: colors.text1, backgroundColor: colors.surface1 },
  proposalSummary: { ...type.bodyMedium, color: colors.text1, fontSize: 16, lineHeight: 22 },
  proposalSafety: { ...type.caption, color: colors.text3 },
  proposalActions: { flexDirection: 'row', alignItems: 'center', gap: space[2], paddingTop: space[1] },
  proposalRun: { minHeight: 44, flex: 1, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.accent },
  proposalRunText: { ...type.captionMedium, color: colors.onAccent },
  proposalDismiss: { minHeight: 44, flex: 1, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.surface3 },
  proposalDismissText: { ...type.captionMedium, color: colors.text2 },
  proposalPressed: { opacity: 0.64, transform: [{ scale: 0.96 }] },
  workHead: { alignItems: 'flex-start', gap: space[2] },
  workIdentity: { minWidth: 0, alignSelf: 'stretch', flexDirection: 'row', alignItems: 'center', gap: 7 },
  workIcon: { width: 24, height: 24, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.emberSoft },
  workKicker: { ...type.captionMedium, color: colors.emberText, textTransform: 'capitalize', flexShrink: 1 },
  workStatus: { minHeight: 26, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 5, paddingHorizontal: 9, borderRadius: radius.full, backgroundColor: colors.emberSoft },
  workStatusComplete: { backgroundColor: colors.liveSoft },
	workStatusNeedsInput: { backgroundColor: colors.emberSoft },
  workStatusFailed: { backgroundColor: colors.dangerSoft },
  workStatusText: { ...type.captionMedium, color: colors.emberText },
	workStatusTextNeedsInput: { color: colors.emberText },
  workStatusTextFailed: { color: colors.danger },
  workQuery: { ...type.bodyMedium, color: colors.text1, fontSize: 16, lineHeight: 22 },
  workProjectChip: { minHeight: 28, maxWidth: '100%', alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 6, paddingHorizontal: 9, borderRadius: radius.full, backgroundColor: colors.emberSoft },
  workProjectText: { ...type.captionMedium, color: colors.emberText, flexShrink: 1 },
  workProgressCopy: { ...type.captionMedium, color: colors.emberText },
  workAttentionCopy: { ...type.bodySm, color: colors.text2, lineHeight: 19 },
  workPreview: { ...type.bodySm, color: colors.text2 },
  workProvenance: { ...type.caption, color: colors.text3 },
  workResultActions: { flexDirection: 'row', flexWrap: 'wrap', gap: space[2], paddingTop: space[1] },
  workResultPrimary: { minHeight: 44, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6, paddingHorizontal: space[3], borderRadius: radius.full, backgroundColor: colors.accent },
  workResultPrimaryText: { ...type.captionMedium, color: colors.onAccent },
  workResultAction: { minHeight: 44, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6, paddingHorizontal: space[3], borderRadius: radius.full, backgroundColor: colors.emberSoft },
  workResultActionText: { ...type.captionMedium, color: colors.emberText },
  workResultPressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  workResultDisabled: { opacity: 0.48 },
  workFoot: { minHeight: 30, flexDirection: 'row', alignItems: 'center', gap: 7, paddingTop: space[2], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  workDot: { width: 7, height: 7, borderRadius: radius.full, backgroundColor: colors.text3 },
  workDotActive: { backgroundColor: colors.ember },
  workDotComplete: { backgroundColor: colors.success },
	workDotNeedsInput: { backgroundColor: colors.ember },
  workDotFailed: { backgroundColor: colors.danger },
  workFootText: { ...type.captionMedium, color: colors.text2, flex: 1 },
  link: { color: colors.info, textDecorationLine: 'underline' },
  linkOwn: { color: colors.onAccent, textDecorationColor: 'rgba(14,14,16,0.45)' },
  mention: { ...type.bodyMedium, color: colors.info },
  mentionOwn: { color: colors.onAccent, textDecorationLine: 'underline' },
  mentionScout: { color: colors.emberText },
  mentionScoutOwn: { color: colors.onAccentEmber },
  readMore: { minHeight: 34, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 6, marginTop: space[3], paddingHorizontal: 10, borderRadius: radius.full, backgroundColor: colors.surface3 },
  readMoreOwn: { backgroundColor: 'rgba(255,255,255,0.14)' },
  readMorePressed: { opacity: 0.72, transform: [{ scale: 0.98 }] },
  readMoreText: { ...type.captionMedium, color: colors.text1 },
  readMoreTextOwn: { color: colors.onAccent },
  attachment: { minWidth: 210, maxWidth: 280, minHeight: 58, flexDirection: 'row', alignItems: 'center', gap: space[2], marginTop: space[2], paddingRight: space[3], overflow: 'hidden', borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface1 },
  attachmentMedia: { width: 248, minHeight: 0, flexDirection: 'column', alignItems: 'stretch', gap: 0, paddingRight: 0, backgroundColor: colors.surface1 },
  attachmentVisual: { marginTop: 0, borderWidth: 0, borderRadius: radius.lg, backgroundColor: 'transparent' },
  attachmentPressed: { opacity: 0.76, transform: [{ scale: 0.98 }] },
  attachmentImage: { width: '100%', height: 168, backgroundColor: colors.surface3 },
  fileIcon: { width: 42, height: 42, marginLeft: 7, alignItems: 'center', justifyContent: 'center', borderRadius: radius.sm, backgroundColor: colors.surface3 },
  attachmentText: { ...type.captionMedium, flex: 1, color: colors.text1 },
  reactions: { position: 'absolute', top: -17, zIndex: 2, flexDirection: 'row', gap: 5 },
  reactionsOwn: { left: -8 },
  reactionsOther: { right: -8 },
  reactionChip: { ...shadow[1], width: 34, height: 34, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.glassBorder, backgroundColor: colors.glassPanel },
  reactionChipOwn: { backgroundColor: colors.surface3, transform: [{ scale: 1.02 }] },
  reactionPressed: { transform: [{ scale: 0.96 }], opacity: 0.78 },
  reactionEmoji: { fontSize: 17, lineHeight: 22 },
  threadSummary: { minHeight: 44, maxWidth: 286, flexDirection: 'row', alignItems: 'center', gap: space[2], marginTop: space[2], paddingHorizontal: 8, paddingVertical: 5, borderRadius: radius.full, backgroundColor: colors.surface2 },
  threadSummaryOwn: { alignSelf: 'flex-end' },
  threadSummaryPressed: { opacity: 0.74, transform: [{ scale: 0.96 }] },
  threadAvatars: { minWidth: 24, flexDirection: 'row', alignItems: 'center' },
  threadAvatar: { borderRadius: radius.full, borderWidth: 2, borderColor: colors.bgApp, overflow: 'hidden' },
  threadAvatarOverlap: { marginLeft: -7 },
  threadSummaryCopy: { minWidth: 0, flex: 1 },
  threadSummaryCount: { ...type.captionMedium, color: colors.text1, fontVariant: ['tabular-nums'] },
  threadSummaryLatest: { ...type.label, color: colors.text3, fontVariant: ['tabular-nums'] },
  sources: { flexDirection: 'row', flexWrap: 'wrap', gap: 6, marginTop: 5, marginLeft: space[1] },
  answerActivity: { alignSelf: 'stretch', minHeight: 44, justifyContent: 'center', gap: 4, marginTop: space[2], paddingTop: space[2], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  answerActivityHead: { flexDirection: 'row', alignItems: 'center', gap: 5 },
  answerActivityTitle: { ...type.captionMedium, color: colors.text2 },
  answerActivityRationale: { ...type.caption, color: colors.text3 },
  sourceChip: { flexDirection: 'row', alignItems: 'center', gap: 4, paddingHorizontal: 8, paddingVertical: 3, borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.ember, backgroundColor: colors.emberSoft, maxWidth: 150 },
  sourcePressed: { opacity: 0.6 },
  sourceText: { fontSize: 11, fontFamily: 'GoogleSansFlex_500Medium', fontWeight: '500', color: colors.emberText, flexShrink: 1 },
});
