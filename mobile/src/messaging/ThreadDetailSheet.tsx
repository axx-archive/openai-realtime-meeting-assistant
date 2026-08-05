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
  TextInput,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';

import type { ScoutFileAttachment, ScoutMessage } from '../api/types';
import { Glass } from '../theme/glass';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { isOwnMessageForViewer } from './messagePresentation';
import { MessageBubble } from './MessageBubble';
import {
  compactComposerHeight,
  composerHeight as measureComposerHeight,
  expandedComposerMaxHeight,
} from './composerMeasurement';

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
  focusComposer: boolean;
  sending: boolean;
  error?: string;
  onClose: () => void;
  onSend: (text: string) => Promise<boolean>;
  onOpenAttachment: (file: ScoutFileAttachment) => void;
  onLongPress: (message: ScoutMessage, own: boolean) => void;
  onToggleReaction: (message: ScoutMessage, emoji: string, active: boolean) => void;
  onRetryReply: (message: ScoutMessage) => void;
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
  focusComposer,
  sending,
  error,
  onClose,
  onSend,
  onOpenAttachment,
  onLongPress,
  onToggleReaction,
  onRetryReply,
  onOpenLongMessage,
  onOpenWorkArtifact,
}: Props) {
  const [draft, setDraft] = useState('');
  const inputRef = useRef<TextInput>(null);
  const scrollRef = useRef<ScrollView>(null);
  const inputWidthRef = useRef(0);
  const nativeContentHeightRef = useRef(compactComposerHeight);
  const [measuredComposerHeight, setMeasuredComposerHeight] = useState(compactComposerHeight);
  const timestampReveal = useRef(new Animated.Value(0)).current;
  const previousReplyCountRef = useRef(0);

  const conversation = useMemo(() => root ? [root, ...replies] : [], [replies, root]);

  useEffect(() => {
    if (!draft) {
      nativeContentHeightRef.current = compactComposerHeight;
      setMeasuredComposerHeight(compactComposerHeight);
    }
  }, [draft]);

  useEffect(() => {
    if (!visible) {
      setDraft('');
      previousReplyCountRef.current = 0;
      return;
    }
    previousReplyCountRef.current = replies.length;
    if (focusComposer) requestAnimationFrame(() => inputRef.current?.focus());
  }, [focusComposer, root?.id, visible]);

  useEffect(() => {
    if (!visible) return;
    if (replies.length > previousReplyCountRef.current) {
      requestAnimationFrame(() => scrollRef.current?.scrollToEnd({ animated: true }));
    }
    previousReplyCountRef.current = replies.length;
  }, [replies.length, visible]);

  const submit = async () => {
    const text = draft.trim();
    if (!text || sending) return;
    if (await onSend(text)) {
      setDraft('');
      requestAnimationFrame(() => inputRef.current?.focus());
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
                    onOpenLongMessage={onOpenLongMessage}
                    onOpenWorkArtifact={onOpenWorkArtifact}
                  />
                </React.Fragment>
              );
            })}
            {replies.length === 0 ? <View style={styles.emptyBreath} /> : null}
          </ScrollView>

          {error ? <Text accessibilityLiveRegion="polite" style={styles.error}>{error}</Text> : null}
          <Glass radius={radius.xl} style={styles.composer}>
            <TextInput
              ref={inputRef}
              accessibilityLabel="Reply in thread"
              editable={!sending}
              multiline
              onChangeText={(nextValue) => {
                if (!nextValue) nativeContentHeightRef.current = compactComposerHeight;
                setMeasuredComposerHeight(measureComposerHeight(
                  nextValue,
                  nativeContentHeightRef.current,
                  expandedComposerMaxHeight,
                  inputWidthRef.current,
                ));
                setDraft(nextValue);
              }}
              onContentSizeChange={(event) => {
                nativeContentHeightRef.current = event.nativeEvent.contentSize.height;
                setMeasuredComposerHeight(measureComposerHeight(
                  draft,
                  nativeContentHeightRef.current,
                  expandedComposerMaxHeight,
                  inputWidthRef.current,
                ));
              }}
              onLayout={(event) => {
                inputWidthRef.current = event.nativeEvent.layout.width;
                setMeasuredComposerHeight(measureComposerHeight(
                  draft,
                  nativeContentHeightRef.current,
                  expandedComposerMaxHeight,
                  inputWidthRef.current,
                ));
              }}
              onSubmitEditing={() => { void submit(); }}
              placeholder="Reply in thread…"
              placeholderTextColor={colors.text3}
              returnKeyType="default"
              scrollEnabled={measuredComposerHeight >= expandedComposerMaxHeight}
              selectionColor={colors.info}
              style={[styles.input, { height: measuredComposerHeight }]}
              textAlignVertical="center"
              value={draft}
            />
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Send threaded reply"
              accessibilityState={{ disabled: sending || !draft.trim() }}
              disabled={sending || !draft.trim()}
              onPress={() => { void submit(); }}
              style={({ pressed }) => [styles.send, (pressed || sending || !draft.trim()) && styles.sendDim]}
            >
              {sending ? (
                <ActivityIndicator color={colors.onAccent} size="small" />
              ) : (
                <SymbolView name="arrow.up" size={18} tintColor={colors.onAccent} />
              )}
            </Pressable>
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
  replyDivider: { flexDirection: 'row', alignItems: 'center', gap: space[3], marginHorizontal: space[4], marginTop: space[5], marginBottom: space[2] },
  rule: { flex: 1, height: StyleSheet.hairlineWidth, backgroundColor: colors.line1 },
  replyDividerText: { ...type.label, color: colors.text3 },
  emptyBreath: { height: space[10] },
  error: { ...type.caption, color: colors.danger, paddingHorizontal: space[5], paddingBottom: space[2] },
  composer: { minHeight: 58, maxHeight: 164, flexDirection: 'row', alignItems: 'flex-end', gap: space[2], marginHorizontal: space[4], marginTop: space[2], marginBottom: space[3], paddingLeft: space[4], paddingRight: 7, paddingVertical: 7 },
  input: { ...type.body, flex: 1, minHeight: hitMin, maxHeight: 132, paddingTop: 10, paddingBottom: 10, color: colors.text1 },
  send: { width: hitMin, height: hitMin, flex: 0, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.accent },
  sendDim: { opacity: 0.46, transform: [{ scale: 0.96 }] },
});
