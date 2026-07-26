import React, { memo, useCallback, useMemo, useRef, useState } from 'react';
import {
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { ink, radius, shadow, space, type } from '../theme/tokens';

export type RoomConversationMode = 'chat' | 'transcript';

type ChatItem = {
  id: string;
  name: string;
  text: string;
  createdAt: string;
  authorEmail?: string;
};

type TranscriptItem = {
  id: string;
  text: string;
  createdAt: string;
  speaker?: string;
  metadata?: Record<string, unknown>;
};

type Props = {
  visible: boolean;
  mode: RoomConversationMode;
  roomName: string;
  messages: ChatItem[];
  transcriptEntries: TranscriptItem[];
  viewer: { name?: string; email?: string };
  onClose: () => void;
  onDeleteMessage: (id: string) => boolean;
  onModeChange: (mode: RoomConversationMode) => void;
  onSendMessage: (text: string) => boolean;
};

function normalizedIdentity(value: unknown): string {
  return String(value ?? '').trim().toLocaleLowerCase();
}

function chatItemIsOwn(item: ChatItem, viewer: Props['viewer']): boolean {
  const viewerEmail = normalizedIdentity(viewer.email);
  const authorEmail = normalizedIdentity(item.authorEmail);
  if (viewerEmail && authorEmail) return viewerEmail === authorEmail;
  return Boolean(viewer.name) && normalizedIdentity(item.name) === normalizedIdentity(viewer.name);
}

function timeLabel(createdAt: string): string {
  const date = new Date(createdAt);
  if (!Number.isFinite(date.getTime())) return '';
  return date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

function transcriptSpeaker(item: TranscriptItem): string {
  const metadata = item.metadata ?? {};
  for (const candidate of [
    item.speaker,
    metadata.speaker,
    metadata.participant,
    metadata.name,
    metadata.authorName,
  ]) {
    const value = String(candidate ?? '').trim();
    if (value) return value;
  }
  return 'Live transcript';
}

export const RoomConversationSheet = memo(function RoomConversationSheet({
  visible,
  mode,
  roomName,
  messages,
  transcriptEntries,
  viewer,
  onClose,
  onDeleteMessage,
  onModeChange,
  onSendMessage,
}: Props) {
  const safeArea = useSafeAreaInsets();
  const chatListRef = useRef<FlatList<ChatItem>>(null);
  const transcriptListRef = useRef<FlatList<TranscriptItem>>(null);
  const [draft, setDraft] = useState('');
  const [composerError, setComposerError] = useState<string | null>(null);

  const submitDraft = useCallback(() => {
    const text = draft.trim();
    if (!text) return;
    if (!onSendMessage(text)) {
      setComposerError('Messages are unavailable while the call reconnects.');
      return;
    }
    setDraft('');
    setComposerError(null);
  }, [draft, onSendMessage]);

  const deleteMessage = useCallback((id: string) => {
    if (onDeleteMessage(id)) return;
    Alert.alert(
      'Message not deleted',
      'The call is reconnecting. Try again when the status returns to Live.',
    );
  }, [onDeleteMessage]);

  const renderMessage = useCallback(({ item }: { item: ChatItem }) => {
    const own = chatItemIsOwn(item, viewer);
    return (
      <View style={[styles.messageRow, own && styles.messageRowOwn]}>
        <View style={[styles.messageBubble, own && styles.messageBubbleOwn]}>
          <View style={styles.messageMeta}>
            <Text numberOfLines={1} style={[styles.messageAuthor, own && styles.messageAuthorOwn]}>
              {own ? 'You' : item.name}
            </Text>
            <Text style={[styles.messageTime, own && styles.messageTimeOwn]}>{timeLabel(item.createdAt)}</Text>
          </View>
          <Text selectable style={[styles.messageText, own && styles.messageTextOwn]}>{item.text}</Text>
        </View>
        {own ? (
          <Pressable
            accessibilityHint="Removes this message for everyone in the room"
            accessibilityLabel="Delete message"
            accessibilityRole="button"
            hitSlop={8}
            onPress={() => {
              Alert.alert('Delete this message?', 'It will be removed for everyone in the room.', [
                { text: 'Cancel', style: 'cancel' },
                { text: 'Delete', style: 'destructive', onPress: () => deleteMessage(item.id) },
              ]);
            }}
            style={({ pressed }) => [styles.deleteMessage, pressed && styles.pressed]}
          >
            <SymbolView name="trash" tintColor="rgba(255,255,255,0.48)" size={14} />
          </Pressable>
        ) : null}
      </View>
    );
  }, [deleteMessage, viewer]);

  const renderTranscriptEntry = useCallback(({ item }: { item: TranscriptItem }) => (
    <View style={styles.transcriptEntry}>
      <View style={styles.transcriptMeta}>
        <View style={styles.transcriptLiveDot} />
        <Text numberOfLines={1} style={styles.transcriptSpeaker}>{transcriptSpeaker(item)}</Text>
        <Text style={styles.transcriptTime}>{timeLabel(item.createdAt)}</Text>
      </View>
      <Text selectable style={styles.transcriptText}>{item.text}</Text>
    </View>
  ), []);

  const emptyCopy = useMemo(() => mode === 'chat'
    ? { icon: 'bubble.left.and.bubble.right', title: 'No messages yet', body: 'Send a note without interrupting the conversation.' }
    : { icon: 'captions.bubble', title: 'Nothing transcribed yet', body: 'Spoken moments will appear here as the room captures them.' }, [mode]);

  return (
    <Modal
      animationType="slide"
      onRequestClose={onClose}
      presentationStyle="pageSheet"
      visible={visible}
    >
      <KeyboardAvoidingView
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
        keyboardVerticalOffset={0}
        style={styles.sheet}
      >
        <View style={[styles.header, { paddingTop: Math.max(safeArea.top, space[3]) }]}>
          <View style={styles.headerCopy}>
            <Text numberOfLines={1} style={styles.title}>{roomName}</Text>
            <Text style={styles.subtitle}>The call stays connected</Text>
          </View>
          <Pressable
            accessibilityLabel="Close conversation"
            accessibilityRole="button"
            onPress={onClose}
            style={({ pressed }) => [styles.close, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" tintColor="#FFFFFF" size={16} />
          </Pressable>
        </View>

        <View accessibilityLabel="Conversation view" accessibilityRole="tablist" style={styles.segmentedControl}>
          {(['chat', 'transcript'] as const).map((item) => {
            const selected = mode === item;
            return (
              <Pressable
                accessibilityLabel={item === 'chat' ? 'Room chat' : 'Live transcript'}
                accessibilityRole="tab"
                accessibilityState={{ selected }}
                key={item}
                onPress={() => onModeChange(item)}
                style={[styles.segment, selected && styles.segmentSelected]}
              >
                <SymbolView
                  name={item === 'chat' ? 'bubble.left.and.bubble.right.fill' : 'captions.bubble.fill'}
                  tintColor={selected ? ink[950] : 'rgba(255,255,255,0.58)'}
                  size={15}
                />
                <Text style={[styles.segmentText, selected && styles.segmentTextSelected]}>
                  {item === 'chat' ? 'Chat' : 'Transcript'}
                </Text>
              </Pressable>
            );
          })}
        </View>

        {mode === 'chat' ? (
          <>
            <FlatList
              ListEmptyComponent={(
                <View style={styles.emptyState}>
                  <SymbolView name={emptyCopy.icon as 'bubble.left.and.bubble.right'} tintColor="rgba(255,255,255,0.35)" size={28} />
                  <Text style={styles.emptyTitle}>{emptyCopy.title}</Text>
                  <Text style={styles.emptyBody}>{emptyCopy.body}</Text>
                </View>
              )}
              contentContainerStyle={[styles.listContent, !messages.length && styles.emptyListContent]}
              data={messages}
              keyExtractor={(item) => item.id}
              keyboardDismissMode="interactive"
              keyboardShouldPersistTaps="handled"
              onContentSizeChange={() => chatListRef.current?.scrollToEnd({ animated: true })}
              ref={chatListRef}
              renderItem={renderMessage}
              style={styles.list}
            />
            <View style={[styles.composerShell, { paddingBottom: Math.max(safeArea.bottom, space[3]) }]}>
              {composerError ? <Text accessibilityLiveRegion="polite" style={styles.composerError}>{composerError}</Text> : null}
              <View style={styles.composer}>
                <TextInput
                  accessibilityLabel="Message the room"
                  maxLength={4000}
                  multiline
                  onChangeText={(value) => {
                    setDraft(value);
                    if (composerError) setComposerError(null);
                  }}
                  onSubmitEditing={submitDraft}
                  placeholder="Message the room"
                  placeholderTextColor="rgba(255,255,255,0.38)"
                  returnKeyType="send"
                  style={styles.input}
                  value={draft}
                />
                <Pressable
                  accessibilityLabel="Send message"
                  accessibilityRole="button"
                  accessibilityState={{ disabled: !draft.trim() }}
                  disabled={!draft.trim()}
                  onPress={submitDraft}
                  style={({ pressed }) => [styles.send, !draft.trim() && styles.sendDisabled, pressed && styles.pressed]}
                >
                  <SymbolView name="arrow.up" tintColor={ink[950]} size={16} />
                </Pressable>
              </View>
            </View>
          </>
        ) : (
          <FlatList
            ListEmptyComponent={(
              <View style={styles.emptyState}>
                <SymbolView name="captions.bubble" tintColor="rgba(255,255,255,0.35)" size={28} />
                <Text style={styles.emptyTitle}>{emptyCopy.title}</Text>
                <Text style={styles.emptyBody}>{emptyCopy.body}</Text>
              </View>
            )}
            contentContainerStyle={[styles.listContent, !transcriptEntries.length && styles.emptyListContent, { paddingBottom: Math.max(safeArea.bottom, space[5]) }]}
            data={transcriptEntries}
            keyExtractor={(item) => item.id}
            onContentSizeChange={() => transcriptListRef.current?.scrollToEnd({ animated: true })}
            ref={transcriptListRef}
            renderItem={renderTranscriptEntry}
            style={styles.list}
          />
        )}
      </KeyboardAvoidingView>
    </Modal>
  );
});

const styles = StyleSheet.create({
  sheet: { flex: 1, backgroundColor: ink[950] },
  header: {
    minHeight: 72,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: space[4],
    paddingBottom: space[3],
  },
  headerCopy: { flex: 1, minWidth: 0 },
  title: { ...type.headline, color: '#FFFFFF' },
  subtitle: { ...type.caption, marginTop: 1, color: 'rgba(255,255,255,0.48)' },
  close: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22, backgroundColor: 'rgba(255,255,255,0.10)' },
  segmentedControl: {
    minHeight: 52,
    flexDirection: 'row',
    gap: 4,
    marginHorizontal: space[4],
    marginBottom: space[3],
    padding: 4,
    borderRadius: radius.lg,
    backgroundColor: ink[800],
  },
  segment: { flex: 1, minHeight: 44, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 7, borderRadius: radius.md },
  segmentSelected: { backgroundColor: '#FFFFFF' },
  segmentText: { ...type.button, color: 'rgba(255,255,255,0.58)' },
  segmentTextSelected: { color: ink[950] },
  list: { flex: 1 },
  listContent: { gap: space[3], paddingHorizontal: space[4], paddingTop: space[2], paddingBottom: space[5] },
  emptyListContent: { flexGrow: 1 },
  emptyState: { flex: 1, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[8], gap: space[2] },
  emptyTitle: { ...type.headline, color: '#FFFFFF', marginTop: space[2] },
  emptyBody: { ...type.bodySm, maxWidth: 280, textAlign: 'center', color: 'rgba(255,255,255,0.48)' },
  messageRow: { maxWidth: '88%', alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'flex-end', gap: 4 },
  messageRowOwn: { alignSelf: 'flex-end', flexDirection: 'row-reverse' },
  messageBubble: { maxWidth: '100%', paddingHorizontal: space[3], paddingVertical: 10, borderRadius: 18, borderBottomLeftRadius: 6, backgroundColor: ink[800] },
  messageBubbleOwn: { borderBottomLeftRadius: 18, borderBottomRightRadius: 6, backgroundColor: '#FFFFFF' },
  messageMeta: { flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 3 },
  messageAuthor: { ...type.captionMedium, flexShrink: 1, color: '#FFFFFF' },
  messageAuthorOwn: { color: ink[950] },
  messageTime: { fontSize: 10, lineHeight: 13, color: 'rgba(255,255,255,0.38)' },
  messageTimeOwn: { color: 'rgba(9,9,11,0.42)' },
  messageText: { ...type.body, color: 'rgba(255,255,255,0.88)' },
  messageTextOwn: { color: ink[950] },
  deleteMessage: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22 },
  transcriptEntry: { gap: 6, padding: space[4], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.09)', backgroundColor: ink[850] },
  transcriptMeta: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  transcriptLiveDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: '#30D158' },
  transcriptSpeaker: { ...type.captionMedium, flex: 1, color: '#FFFFFF' },
  transcriptTime: { fontSize: 10, lineHeight: 13, color: 'rgba(255,255,255,0.38)' },
  transcriptText: { ...type.body, color: 'rgba(255,255,255,0.82)' },
  composerShell: { ...shadow.mark, paddingHorizontal: space[3], paddingTop: space[2], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: 'rgba(255,255,255,0.09)', backgroundColor: 'rgba(13,13,16,0.96)' },
  composerError: { ...type.caption, marginBottom: space[2], textAlign: 'center', color: '#FF9F0A' },
  composer: { minHeight: 50, flexDirection: 'row', alignItems: 'flex-end', gap: space[2], paddingLeft: space[3], paddingRight: 5, paddingVertical: 5, borderRadius: 25, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.12)', backgroundColor: ink[800] },
  input: { ...type.body, flex: 1, maxHeight: 112, paddingTop: 8, paddingBottom: 7, color: '#FFFFFF' },
  send: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22, backgroundColor: '#FFFFFF' },
  sendDisabled: { opacity: 0.34 },
  pressed: { opacity: 0.76, transform: [{ scale: 0.96 }] },
});
