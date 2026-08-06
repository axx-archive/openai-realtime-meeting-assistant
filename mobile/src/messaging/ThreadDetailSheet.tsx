import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';

import type { ChatMentionCandidate, ScoutFileAttachment, ScoutMessage } from '../api/types';
import { Glass } from '../theme/glass';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { isOwnMessageForViewer } from './messagePresentation';
import { MessageBubble } from './MessageBubble';
import { MentionComposerInput } from './MentionComposerInput';

type Props = {
  visible: boolean;
  title: string;
  root: ScoutMessage | null;
  replies: readonly ScoutMessage[];
  viewerEmail: string;
  threadVisibility: string;
  threadOwnerEmail: string;
  sessionToken: string;
  participantAvatars: ReadonlyMap<string, string>;
  mentionCandidates: ChatMentionCandidate[];
  sending: boolean;
  uploading: boolean;
  error?: string;
  pendingFiles: readonly ScoutFileAttachment[];
  stagingFiles: ReadonlyArray<{ id: string; name: string; mime: string }>;
  onClose: () => void;
  onSend: (text: string, files: readonly ScoutFileAttachment[]) => Promise<boolean>;
  onAddAttachment: () => void;
  onRemoveAttachment: (file: ScoutFileAttachment) => void;
  onOpenAttachment: (file: ScoutFileAttachment) => void;
  onLongPress: (message: ScoutMessage, own: boolean) => void;
  onToggleReaction: (message: ScoutMessage, emoji: string, active: boolean) => void;
  onRetryReply: (message: ScoutMessage) => void;
  onResolveProposal: (message: ScoutMessage, action: 'accepted' | 'dismissed') => void;
  resolvingProposalID?: string | null;
  onOpenLongMessage: (text: string, authorName: string, scout: boolean) => void;
  onOpenWorkArtifact: (message: ScoutMessage) => void;
};

export function ThreadDetailSheet({
  visible,
  title,
  root,
  replies,
  viewerEmail,
  threadVisibility,
  threadOwnerEmail,
  sessionToken,
  participantAvatars,
  mentionCandidates,
  sending,
  uploading,
  error,
  pendingFiles,
  stagingFiles,
  onClose,
  onSend,
  onAddAttachment,
  onRemoveAttachment,
  onOpenAttachment,
  onLongPress,
  onToggleReaction,
  onRetryReply,
  onResolveProposal,
  resolvingProposalID,
  onOpenLongMessage,
  onOpenWorkArtifact,
}: Props) {
  const [draft, setDraft] = useState('');
  const scrollRef = useRef<ScrollView>(null);
  const timestampReveal = useRef(new Animated.Value(0)).current;
  const previousReplyCountRef = useRef(0);

  const conversation = useMemo(() => root ? [root, ...replies] : [], [replies, root]);

  useEffect(() => {
    if (!visible) {
      setDraft('');
      previousReplyCountRef.current = 0;
      return;
    }
    previousReplyCountRef.current = replies.length;
  }, [root?.id, visible]);

  useEffect(() => {
    if (!visible) return;
    if (replies.length > previousReplyCountRef.current) {
      requestAnimationFrame(() => scrollRef.current?.scrollToEnd({ animated: true }));
    }
    previousReplyCountRef.current = replies.length;
  }, [replies.length, visible]);

  const submit = async () => {
    const text = draft.trim();
    if ((!text && pendingFiles.length === 0) || sending || uploading) return;
    if (await onSend(text, pendingFiles)) {
      setDraft('');
    }
  };

  return (
    <Modal
      visible={visible}
      animationType="slide"
      presentationStyle="pageSheet"
      onRequestClose={onClose}
    >
      <SafeAreaView style={styles.safe} edges={['left', 'right', 'bottom']}>
        <KeyboardAvoidingView
          style={styles.fill}
          behavior={Platform.OS === 'ios' ? 'padding' : undefined}
          keyboardVerticalOffset={8}
        >
          <View style={styles.handle} />
          <View style={styles.header}>
            <View style={styles.headerCopy}>
              <Text style={styles.eyebrow}>THREAD</Text>
              <Text numberOfLines={1} style={styles.title}>{title}</Text>
              <Text style={styles.meta}>
                {replies.length} {replies.length === 1 ? 'reply' : 'replies'}
              </Text>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Close thread"
              hitSlop={8}
              onPress={onClose}
              style={({ pressed }) => [styles.close, pressed && styles.pressed]}
            >
              <SymbolView name="xmark" size={15} tintColor={colors.text2} />
            </Pressable>
          </View>

          <ScrollView
            ref={scrollRef}
            contentInsetAdjustmentBehavior="automatic"
            keyboardShouldPersistTaps="handled"
            contentContainerStyle={styles.conversation}
          >
            {conversation.map((message, index) => {
              const own = isOwnMessageForViewer(message, {
                viewerEmail,
                threadVisibility,
                threadOwnerEmail,
              });
              const email = String(message.authorEmail ?? '').trim().toLowerCase();
              const avatarDataURL = String(message.avatarDataURL ?? participantAvatars.get(email) ?? '') || undefined;
              return (
                <React.Fragment key={String(message.id)}>
                  {index === 1 ? (
                    <View accessibilityRole="header" style={styles.replyDivider}>
                      <View style={styles.rule} />
                      <Text style={styles.replyDividerText}>REPLIES</Text>
                      <View style={styles.rule} />
                    </View>
                  ) : null}
                  <View style={styles.messageRow}>
                    <MessageBubble
                      message={message}
                      own={own}
                      showAuthor
                      showAvatar
                      avatarDataURL={avatarDataURL}
                      sessionToken={sessionToken}
                      viewerEmail={viewerEmail}
                      timestampReveal={timestampReveal}
                      onOpenSource={() => scrollRef.current?.scrollTo({ y: 0, animated: true })}
                      onOpenReplySource={() => scrollRef.current?.scrollTo({ y: 0, animated: true })}
                      showReplyContext={false}
                      onOpenAttachment={onOpenAttachment}
                      onLongPress={onLongPress}
                      onToggleReaction={onToggleReaction}
                      onRetryReply={onRetryReply}
                      onResolveProposal={onResolveProposal}
                      resolvingProposal={resolvingProposalID === String(message.id)}
                      onOpenLongMessage={onOpenLongMessage}
                      onOpenWorkArtifact={onOpenWorkArtifact}
                    />
                  </View>
                </React.Fragment>
              );
            })}
            {replies.length === 0 ? <View style={styles.emptyBreath} /> : null}
          </ScrollView>

          {error ? <Text accessibilityLiveRegion="polite" style={styles.error}>{error}</Text> : null}
          <Glass radius={radius.xl} style={styles.composer}>
            {pendingFiles.length > 0 || stagingFiles.length > 0 ? (
              <View accessibilityLabel="Reply attachments" style={styles.pendingFiles}>
                {stagingFiles.map((file) => (
                  <View key={file.id} style={[styles.pendingFile, styles.stagingFile]}>
                    <Text numberOfLines={1} style={styles.pendingFileText}>{file.name}</Text>
                    <ActivityIndicator color={colors.text2} size="small" />
                  </View>
                ))}
                {pendingFiles.map((file) => (
                  <View key={`${file.ref}-${file.name}`} style={styles.pendingFile}>
                    <SymbolView name={file.mime.startsWith('image/') ? 'photo' : 'doc.richtext'} tintColor={colors.text2} size={14} />
                    <Text numberOfLines={1} style={styles.pendingFileText}>{file.name}</Text>
                    <Pressable
                      accessibilityRole="button"
                      accessibilityLabel={`Remove ${file.name}`}
                      onPress={() => onRemoveAttachment(file)}
                      style={({ pressed }) => [styles.pendingRemove, pressed && styles.pressed]}
                    >
                      <SymbolView name="xmark" tintColor={colors.text3} size={10} />
                    </Pressable>
                  </View>
                ))}
              </View>
            ) : null}
            <View style={styles.composerRow}>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Add attachment to reply"
                accessibilityState={{ disabled: sending || uploading }}
                disabled={sending || uploading}
                onPress={onAddAttachment}
                style={({ pressed }) => [styles.attachment, (pressed || sending || uploading) && styles.sendDim]}
              >
                <SymbolView name="plus" size={20} tintColor={colors.text2} />
              </Pressable>
              <View style={styles.input}>
                <MentionComposerInput
                  accessibilityLabel="Reply in thread"
                  candidates={mentionCandidates}
                  editable={!sending && !uploading}
                  onChangeText={setDraft}
                  placeholder="Reply in thread…"
                  value={draft}
                />
              </View>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Send threaded reply"
                accessibilityState={{ disabled: sending || uploading || (!draft.trim() && pendingFiles.length === 0) }}
                disabled={sending || uploading || (!draft.trim() && pendingFiles.length === 0)}
                onPress={() => { void submit(); }}
                style={({ pressed }) => [styles.send, (pressed || sending || uploading || (!draft.trim() && pendingFiles.length === 0)) && styles.sendDim]}
              >
                {sending ? (
                  <ActivityIndicator color={colors.onAccent} size="small" />
                ) : (
                  <SymbolView name="arrow.up" size={18} tintColor={colors.onAccent} />
                )}
              </Pressable>
            </View>
          </Glass>
        </KeyboardAvoidingView>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  fill: { flex: 1 },
  handle: { alignSelf: 'center', width: 36, height: 5, marginTop: space[2], borderRadius: radius.full, backgroundColor: colors.line2 },
  header: { minHeight: 82, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[5], paddingBottom: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  headerCopy: { flex: 1, minWidth: 0, gap: 1 },
  eyebrow: { ...type.label, color: colors.emberText },
  title: { ...type.headline, color: colors.text1 },
  meta: { ...type.caption, color: colors.text3, fontVariant: ['tabular-nums'] },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.md, backgroundColor: colors.surface3 },
  pressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  conversation: { paddingTop: space[3], paddingBottom: space[8] },
  messageRow: { position: 'relative' },
  replyDivider: { flexDirection: 'row', alignItems: 'center', gap: space[3], marginHorizontal: space[4], marginTop: space[5], marginBottom: space[2] },
  rule: { flex: 1, height: StyleSheet.hairlineWidth, backgroundColor: colors.line1 },
  replyDividerText: { ...type.label, color: colors.text3 },
  emptyBreath: { height: space[10] },
  error: { ...type.caption, color: colors.danger, paddingHorizontal: space[5], paddingBottom: space[2] },
  composer: { minHeight: 58, maxHeight: 360, gap: space[2], marginHorizontal: space[4], marginTop: space[2], marginBottom: space[3], padding: 7 },
  composerRow: { flexDirection: 'row', alignItems: 'flex-end', gap: space[2] },
  attachment: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  input: { flex: 1, minHeight: hitMin, maxHeight: 328, justifyContent: 'center', paddingTop: 10, paddingBottom: 4 },
  pendingFiles: { flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 6 },
  pendingFile: { minHeight: 34, maxWidth: '100%', flexDirection: 'row', alignItems: 'center', gap: 6, paddingLeft: 10, paddingRight: 4, borderRadius: radius.full, backgroundColor: colors.surface3 },
  stagingFile: { opacity: 0.72, paddingRight: 10 },
  pendingFileText: { ...type.captionMedium, minWidth: 0, maxWidth: 220, flexShrink: 1, color: colors.text1 },
  pendingRemove: { width: 34, height: 34, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  send: { width: hitMin, height: hitMin, flex: 0, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.accent },
  sendDim: { opacity: 0.46, transform: [{ scale: 0.96 }] },
});
