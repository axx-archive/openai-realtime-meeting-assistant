import React, { useMemo } from 'react';
import { ActivityIndicator, Animated, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import { Image } from 'expo-image';
import { SymbolView } from 'expo-symbols';
import * as Linking from 'expo-linking';
import { useMappingHelper } from '@shopify/flash-list';

import type { ScoutFileAttachment, ScoutMessage } from '../api/types';
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

export type MessageBubbleProps = {
  message: ScoutMessage;
  own: boolean;
  showAuthor: boolean;
  showAvatar?: boolean;
  avatarDataURL?: string;
  sessionToken: string;
  viewerEmail: string;
  timestampReveal: Animated.Value;
  onOpenSource?: (messageId: string) => void;
  onOpenReplySource?: (messageId: string) => void;
  showReplyContext?: boolean;
  threadReplies?: readonly ScoutMessage[];
  onOpenThread?: (message: ScoutMessage) => void;
  onLongPress?: (message: ScoutMessage, own: boolean, attachment?: { file: ScoutFileAttachment; index: number }) => void;
  onOpenAttachment?: (file: ScoutFileAttachment) => void;
  onToggleReaction?: (message: ScoutMessage, emoji: string, active: boolean) => void;
  onRetryReply?: (message: ScoutMessage) => void;
  onOpenLongMessage?: (text: string, authorName: string, scout: boolean) => void;
  onOpenWorkArtifact?: (message: ScoutMessage) => void;
  onResolveProposal?: (message: ScoutMessage, action: 'accepted' | 'dismissed', objective: string) => void;
  proposalObjective?: string;
  onChangeProposalObjective?: (message: ScoutMessage, objective: string) => void;
  onSaveWorkArtifact?: (message: ScoutMessage) => void;
  onRegenerateWorkArtifact?: (message: ScoutMessage) => void;
  onSaveImage?: (message: ScoutMessage) => void;
  onRegenerateImage?: (message: ScoutMessage) => void;
  resolvingProposal?: boolean;
  retryingReply?: boolean;
  savingImage?: boolean;
  regeneratingImage?: boolean;
  imageSaved?: boolean;
  savingWork?: boolean;
  regeneratingWork?: boolean;
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

function workThreadPresentation(message: ScoutMessage) {
  if (String(message.kind ?? '').toLowerCase() !== 'thread' || !message.thread) return null;
  const status = String(message.thread.status ?? 'running').toLowerCase();
  const active = status === 'queued' || status === 'running';
  const complete = status === 'complete' || status === 'published';
	const followUpStatus = String(message.thread.followUpStatus ?? '').toLowerCase();
	const revisionNeedsAttention = complete && (followUpStatus === 'needs_attention' || (!followUpStatus && /revision needs attention/iu.test(String(message.thread.progressNote ?? ''))));
  const failed = status === 'failed' || status === 'error' || status === 'needs_attention' || status === 'rejected';
  const needsInput = status === 'approval_required' || status === 'needs_input' || status === 'parked';
  const stage = String(message.thread.currentStage ?? '').toLowerCase();
  const progressPercent = Math.max(0, Math.min(100, Math.round(Number(message.thread.progressPercent ?? 0))));
  const attentionReason = String(message.thread.attentionReason ?? '').toLowerCase();
  const phase = complete
    ? revisionNeedsAttention ? 'Delivered · revision needs attention' : 'Delivered'
    : needsInput
      ? 'Needs input'
      : failed
        ? 'Needs attention'
        : /deliver|verify_goal_completed/u.test(stage)
          ? 'Delivering'
          : /gate|review|verif/u.test(stage)
            ? 'Verifying'
            : /build|draft|synth|execute|research|source|evidence/u.test(stage)
              ? 'Gathering evidence'
              : status === 'queued'
                ? 'Queued'
                : 'Understanding';
  const agentName = String(message.thread.agentName ?? 'Scout').trim() || 'Scout';
  return {
    active,
    complete,
    failed,
    needsInput,
    agentName,
	delegatedBy: String(message.thread.delegatedBy ?? '').trim(),
    phase,
    mode: String(message.thread.mode ?? 'work').trim() || 'work',
    query: String(message.thread.query ?? '').trim() || 'Scout workstream',
    progressPercent,
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
  onLongPress,
  onOpenAttachment,
  onToggleReaction,
  onRetryReply,
  onOpenLongMessage,
  onOpenWorkArtifact,
  onResolveProposal,
  proposalObjective,
  onChangeProposalObjective,
  onSaveWorkArtifact,
  onRegenerateWorkArtifact,
  onSaveImage,
  onRegenerateImage,
  resolvingProposal = false,
  retryingReply = false,
  savingImage = false,
  regeneratingImage = false,
  imageSaved = false,
  savingWork = false,
  regeneratingWork = false,
  workSaved = false,
  workDriveSaveAvailability = 'checking',
}: MessageBubbleProps) {
  const lifecycle = scoutReplyLifecyclePresentation(message);
  const workThread = workThreadPresentation(message);
  const proposal = message.proposal;
  const workProposal = String(proposal?.kind ?? '').toLowerCase() === 'workstream';
  const proposalStatus = String(proposal?.status ?? '').toLowerCase();
  const proposalPending = workProposal && !proposalStatus;
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
  const sources = Array.isArray(message.sources) ? message.sources : [];
  const longMessage = body.length > 700 || body.split('\n').length > 12;
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
          accessibilityHint={workThread ? 'Opens live work details or the completed deliverable' : longMessage ? 'Opens the full message. Touch and hold for message actions' : 'Touch and hold for message actions'}
          accessibilityActions={[{ name: 'longpress', label: 'Show message actions' }]}
          delayLongPress={messageLongPressDelayMs}
          onAccessibilityAction={(event) => {
            if (event.nativeEvent.actionName === 'longpress') onLongPress?.(message, own);
          }}
          onPress={workThread && !workThread.complete ? () => onOpenWorkArtifact?.(message) : longMessage ? () => onOpenLongMessage?.(body, authorName, scout) : undefined}
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
                <Text style={styles.proposalCompactText}>{proposalStatus === 'accepted' ? `${proposal?.agentName || 'Scout'} started this research` : 'Proposed work dismissed'}</Text>
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
                  editable={!resolvingProposal}
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
              <Text style={styles.proposalSafety}>Nothing runs until you confirm.</Text>
              {proposalPending ? (
                <View style={styles.proposalActions}>
                  <Pressable
                    accessibilityRole="button"
                    accessibilityLabel={`Run ${proposal?.agentName || 'agent'} work once`}
                    accessibilityState={{ disabled: resolvingProposal }}
                    disabled={resolvingProposal}
                    onPress={() => onResolveProposal?.(message, 'accepted', String(proposalObjective ?? proposal?.objective ?? proposal?.summary ?? body).trim())}
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
          ) : workThread ? (
            <View
              accessibilityLabel={`${workThread.mode} workstream. ${workThread.label}. ${workThread.query}`}
              accessibilityLiveRegion="polite"
              style={styles.workCard}
            >
              <View style={styles.workHead}>
                <View style={styles.workIdentity}>
                  <View style={styles.workIcon}>
                    <SymbolView name="flame.fill" tintColor={colors.emberText} size={13} />
                  </View>
			      <Text style={styles.workKicker}>
			        {workThread.agentName} · {workThread.mode}{workThread.delegatedBy ? ` · via ${workThread.delegatedBy}` : ''}
			      </Text>
                </View>
			    <View style={[styles.workStatus, workThread.complete && styles.workStatusComplete, workThread.needsInput && styles.workStatusNeedsInput, workThread.failed && styles.workStatusFailed]}>
                  {workThread.active ? <ActivityIndicator color={colors.emberText} size="small" /> : null}
                  {!workThread.active ? (
                    <SymbolView
				      name={workThread.complete ? 'checkmark.circle.fill' : workThread.needsInput ? 'questionmark.circle.fill' : 'exclamationmark.circle.fill'}
				      tintColor={workThread.failed ? colors.danger : workThread.needsInput ? colors.emberText : colors.success}
                      size={13}
                    />
                  ) : null}
				  <Text style={[styles.workStatusText, workThread.needsInput && styles.workStatusTextNeedsInput, workThread.failed && styles.workStatusTextFailed]}>{workThread.label}</Text>
                </View>
              </View>
              <Text numberOfLines={2} style={styles.workQuery}>{String(message.thread?.resultTitle ?? '').trim() || workThread.query}</Text>
              {workThread.active ? (
                <Text style={styles.workProgressCopy}>
                  {workThread.phase}{workThread.progressPercent > 0 ? ` · ${workThread.progressPercent}%` : ''}
                </Text>
              ) : null}
              {workThread.failed && workThread.attentionCopy ? <Text style={styles.workAttentionCopy}>{workThread.attentionCopy}</Text> : null}
              {String(message.thread?.resultPreview ?? '').trim() ? <Text numberOfLines={3} style={styles.workPreview}>{String(message.thread?.resultPreview)}</Text> : null}
              {String(message.thread?.provenance ?? '').trim() ? <Text numberOfLines={2} style={styles.workProvenance}>{String(message.thread?.provenance)}</Text> : null}
              {workThread.complete ? (
                <View style={styles.workResultActions}>
                  <Pressable accessibilityRole="button" accessibilityLabel="Open deliverable" onPress={() => onOpenWorkArtifact?.(message)} style={({ pressed }) => [styles.workResultPrimary, pressed && styles.workResultPressed]}>
                    <SymbolView name="doc.text.fill" tintColor={colors.onAccent} size={14} />
                    <Text style={styles.workResultPrimaryText}>Open</Text>
                  </Pressable>
                  <Pressable accessibilityRole="button" accessibilityLabel={workSaved ? 'Deliverable saved to Drive' : workDriveSaveAvailability === 'available' ? 'Save deliverable to Drive' : workDriveSaveAvailability === 'checking' ? 'Checking Save to Drive availability' : 'Save to Drive unavailable'} accessibilityState={{ disabled: savingWork || workSaved || workDriveSaveAvailability !== 'available' }} disabled={savingWork || workSaved || workDriveSaveAvailability !== 'available'} onPress={() => onSaveWorkArtifact?.(message)} style={({ pressed }) => [styles.workResultAction, pressed && styles.workResultPressed, (savingWork || workSaved || workDriveSaveAvailability !== 'available') && styles.workResultDisabled]}>
                    {savingWork ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="externaldrive.fill" tintColor={colors.emberText} size={14} />}
                    <Text style={styles.workResultActionText}>{workSaved ? 'Saved' : savingWork ? 'Saving…' : workDriveSaveAvailability === 'checking' ? 'Checking…' : workDriveSaveAvailability === 'unavailable' ? 'Unavailable' : 'Save'}</Text>
                  </Pressable>
                  <Pressable accessibilityRole="button" accessibilityLabel="Edit prompt and regenerate deliverable" accessibilityState={{ disabled: regeneratingWork }} disabled={regeneratingWork} onPress={() => onRegenerateWorkArtifact?.(message)} style={({ pressed }) => [styles.workResultAction, pressed && styles.workResultPressed, regeneratingWork && styles.workResultDisabled]}>
                    {regeneratingWork ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="arrow.clockwise" tintColor={colors.emberText} size={14} />}
                    <Text style={styles.workResultActionText}>{regeneratingWork ? 'Starting…' : 'Regenerate'}</Text>
                  </Pressable>
                </View>
              ) : workThread.failed ? (
                <View style={styles.workResultActions}>
                  <Pressable accessibilityRole="button" accessibilityLabel="View research failure details" onPress={() => onOpenWorkArtifact?.(message)} style={({ pressed }) => [styles.workResultPrimary, pressed && styles.workResultPressed]}>
                    <SymbolView name="info.circle.fill" tintColor={colors.onAccent} size={14} />
                    <Text style={styles.workResultPrimaryText}>View details</Text>
                  </Pressable>
                  <Pressable accessibilityRole="button" accessibilityLabel="Retry research" accessibilityState={{ disabled: regeneratingWork }} disabled={regeneratingWork} onPress={() => onRegenerateWorkArtifact?.(message)} style={({ pressed }) => [styles.workResultAction, pressed && styles.workResultPressed, regeneratingWork && styles.workResultDisabled]}>
                    {regeneratingWork ? <ActivityIndicator color={colors.emberText} size="small" /> : <SymbolView name="arrow.clockwise" tintColor={colors.emberText} size={14} />}
                    <Text style={styles.workResultActionText}>{regeneratingWork ? 'Starting…' : 'Retry research'}</Text>
                  </Pressable>
                </View>
              ) : (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel={`Open live work details. ${workThread.phase}${workThread.progressPercent > 0 ? `, ${workThread.progressPercent}% complete` : ''}`}
                  onPress={() => onOpenWorkArtifact?.(message)}
                  style={({ pressed }) => [styles.workFoot, pressed && styles.workResultPressed]}
                >
                  <Text style={styles.workFootText}>{workThread.progressPercent > 0 ? `${workThread.progressPercent}% complete` : 'Research in progress'}</Text>
                  <SymbolView name="chevron.right" tintColor={colors.text3} size={12} />
                </Pressable>
              )}
            </View>
          ) : null}

          {!generatedImagePending && !workProposal && !workThread && body && lifecycle?.state === 'canceled' ? (
            <Text style={styles.lifecycleCanceled}>{body}</Text>
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
                key={getMappingKey(source.messageId, index)}
                accessibilityRole="button"
                accessibilityLabel={`Source: ${source.author || 'a message'} — ${source.quote}`}
                accessibilityHint="Scrolls to the source message"
                onPress={() => onOpenSource?.(source.messageId)}
                style={({ pressed }) => [styles.sourceChip, pressed && styles.sourcePressed]}
              >
                <SymbolView name="quote.opening" tintColor={colors.emberText} size={10} />
                <Text style={styles.sourceText} numberOfLines={1}>{source.author || 'message'}</Text>
              </Pressable>
            ))}
          </View>
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
  bubbleWork: { width: '100%', maxWidth: 372, alignSelf: 'stretch' },
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
  sourceChip: { flexDirection: 'row', alignItems: 'center', gap: 4, paddingHorizontal: 8, paddingVertical: 3, borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.ember, backgroundColor: colors.emberSoft, maxWidth: 150 },
  sourcePressed: { opacity: 0.6 },
  sourceText: { fontSize: 11, fontFamily: 'GoogleSansFlex_500Medium', fontWeight: '500', color: colors.emberText, flexShrink: 1 },
});
