import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Dimensions,
  FlatList,
  KeyboardAvoidingView,
  Modal,
  type NativeScrollEvent,
  type NativeSyntheticEvent,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Waveform } from './Waveform';
import { hitMin, ink, radius, shadow, space, type } from '../theme/tokens';
import { useComposerDictation } from '../voice/useComposerDictation';
import { parseMentions } from '../messaging/mentions';
import {
  nextThreadScrollInteraction,
  shouldFollowThreadTail,
} from '../messaging/threadListPerformance';
import {
  meetingIntelligenceStatusLabel,
  type MeetingIntelligenceFact,
  type MeetingIntelligenceSnapshot,
} from '../realtime/meetingIntelligence';

export type RoomConversationMode = 'recap' | 'transcript' | 'chat';

type ChatItem = {
  id: string;
  name: string;
  text: string;
  createdAt: string;
  authorEmail?: string;
  agentId?: string;
  artifactId?: string;
  workRunId?: string;
  workStatus?: 'queued' | 'running' | 'approval_required' | 'complete' | 'needs_attention';
  workFamily?: string;
  workTitle?: string;
  workProgress?: number;
  followThroughId?: string;
  followThroughStatus?: 'queued' | 'delivering' | 'delivered' | 'awaiting_input';
  transient?: boolean;
  error?: boolean;
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
  intelligence: MeetingIntelligenceSnapshot | null;
  viewer: { name?: string; email?: string };
  onClose: () => void;
  onDeleteMessage: (id: string) => boolean;
  onModeChange: (mode: RoomConversationMode) => void;
  onSendMessage: (text: string) => boolean;
  onOpenArtifact?: (artifactId: string, title: string) => void;
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

const conversationMomentumGraceMs = 200;

function useConversationTailFollow<T>(
  listRef: React.RefObject<FlatList<T> | null>,
  active: boolean,
) {
  const atBottomRef = useRef(false);
  const interactingRef = useRef(false);
  const initialTailRef = useRef(true);
  const layoutHeightRef = useRef(0);
  const momentumGraceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearMomentumGrace = useCallback(() => {
    if (momentumGraceRef.current) clearTimeout(momentumGraceRef.current);
    momentumGraceRef.current = null;
  }, []);

  const settle = useCallback((offset: number, content: number, layout: number) => {
    clearMomentumGrace();
    interactingRef.current = false;
    atBottomRef.current = offset + layout >= content - 48;
  }, [clearMomentumGrace]);

  useEffect(() => {
    if (active) {
      initialTailRef.current = true;
      atBottomRef.current = false;
      interactingRef.current = false;
    }
    return clearMomentumGrace;
  }, [active, clearMomentumGrace]);

  return {
    onLayout: (event: { nativeEvent: { layout: { height: number } } }) => {
      layoutHeightRef.current = event.nativeEvent.layout.height;
    },
    onContentSizeChange: (_width: number, height: number) => {
      const initial = initialTailRef.current;
      const follow = initial || shouldFollowThreadTail(atBottomRef.current, interactingRef.current);
      if (layoutHeightRef.current > 0 && height <= layoutHeightRef.current) {
        atBottomRef.current = true;
      }
      if (!follow) return;
      initialTailRef.current = false;
      atBottomRef.current = true;
      requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: false }));
    },
    onScroll: (event: NativeSyntheticEvent<NativeScrollEvent>) => {
      if (interactingRef.current) return;
      const { contentOffset, contentSize, layoutMeasurement } = event.nativeEvent;
      atBottomRef.current = contentOffset.y + layoutMeasurement.height >= contentSize.height - 48;
    },
    onScrollBeginDrag: () => {
      clearMomentumGrace();
      interactingRef.current = nextThreadScrollInteraction(interactingRef.current, 'drag-begin');
      atBottomRef.current = false;
    },
    onScrollEndDrag: (event: NativeSyntheticEvent<NativeScrollEvent>) => {
      const { contentOffset, contentSize, layoutMeasurement, velocity } = event.nativeEvent;
      const remainsInteracting = nextThreadScrollInteraction(interactingRef.current, 'drag-end', velocity?.y);
      interactingRef.current = remainsInteracting;
      if (!remainsInteracting) {
        settle(contentOffset.y, contentSize.height, layoutMeasurement.height);
        return;
      }
      clearMomentumGrace();
      momentumGraceRef.current = setTimeout(() => {
        settle(contentOffset.y, contentSize.height, layoutMeasurement.height);
      }, conversationMomentumGraceMs);
    },
    onMomentumScrollBegin: () => {
      clearMomentumGrace();
      interactingRef.current = nextThreadScrollInteraction(interactingRef.current, 'momentum-begin');
    },
    onMomentumScrollEnd: (event: NativeSyntheticEvent<NativeScrollEvent>) => {
      const { contentOffset, contentSize, layoutMeasurement } = event.nativeEvent;
      interactingRef.current = nextThreadScrollInteraction(interactingRef.current, 'momentum-end');
      settle(contentOffset.y, contentSize.height, layoutMeasurement.height);
    },
  };
}

const RoomMessageText = memo(function RoomMessageText({
  text,
  own,
  scout,
}: {
  text: string;
  own: boolean;
  scout: boolean;
}) {
  const segments = useMemo(() => parseMentions(text), [text]);
  return (
    <Text selectable style={[styles.messageText, own && styles.messageTextOwn, scout && styles.messageTextScout]}>
      {segments.map((segment, index) => segment.kind === 'mention' ? (
        <Text
          key={`${index}-${segment.text}`}
          style={segment.scout ? styles.messageMentionScout : styles.messageMention}
        >
          {segment.text}
        </Text>
      ) : segment.text)}
    </Text>
  );
});

export const RoomConversationSheet = memo(function RoomConversationSheet({
  visible,
  mode,
  roomName,
  messages,
  transcriptEntries,
  intelligence,
  viewer,
  onClose,
  onDeleteMessage,
  onModeChange,
  onSendMessage,
  onOpenArtifact,
}: Props) {
  const safeArea = useSafeAreaInsets();
  const chatListRef = useRef<FlatList<ChatItem>>(null);
  const transcriptListRef = useRef<FlatList<TranscriptItem>>(null);
  const chatTail = useConversationTailFollow(chatListRef, visible && mode === 'chat');
  const transcriptTail = useConversationTailFollow(transcriptListRef, visible && mode === 'transcript');
  const composerInputRef = useRef<TextInput>(null);
  const sheetRef = useRef<View>(null);
  const [draft, setDraft] = useState('');
  const [composerError, setComposerError] = useState<string | null>(null);
  const [keyboardOffset, setKeyboardOffset] = useState(8);

  const measureSheetKeyboardOffset = useCallback(() => {
    requestAnimationFrame(() => {
      sheetRef.current?.measureInWindow((_x, y, _width, height) => {
        // Keep the room composer on the same page-sheet keyboard contract as
        // threads: sheet layout is local, but iOS keyboard bounds are global.
        const screenHeight = Dimensions.get('screen').height;
        const sheetTop = Math.max(0, y, screenHeight - height);
        const nextOffset = Math.max(8, Math.round(sheetTop) + 8);
        setKeyboardOffset((current) => current === nextOffset ? current : nextOffset);
      });
    });
  }, []);

  const sendComposerText = useCallback((candidate: string): boolean => {
    const text = candidate.trim();
    if (!text) return false;
    if (!onSendMessage(text)) {
      setComposerError('Messages are unavailable while the call reconnects.');
      return false;
    }
    setDraft('');
    setComposerError(null);
    return true;
  }, [onSendMessage]);

  const submitDraft = useCallback(() => {
    void sendComposerText(draft);
  }, [draft, sendComposerText]);

  const insertScoutMention = useCallback(() => {
    setDraft((current) => {
      if (/(^|[^\p{L}\p{N}])@scout(?![\p{L}\p{N}])/iu.test(current)) return current;
      if (!current) return '@Scout ';
      return `${current}${/\s$/u.test(current) ? '' : ' '}@Scout `;
    });
    setComposerError(null);
    requestAnimationFrame(() => composerInputRef.current?.focus());
  }, []);

  const composerDictation = useComposerDictation({
    onTranscript: ({ text }) => {
      // Show the committed transcript in the composer before using the same
      // room-chat transport as typed text. A reconnect failure retains it for
      // an explicit retry instead of losing the user's spoken message.
      setDraft(text);
      void sendComposerText(text);
    },
  });
  const discardDictationRef = useRef(composerDictation.discard);
  discardDictationRef.current = composerDictation.discard;
  useEffect(() => {
    // A hidden sheet has no visible recording indicator or stop control. Never
    // let capture continue when the modal closes or switches to Transcript.
    if (visible && mode === 'chat') return;
    void discardDictationRef.current();
  }, [mode, visible]);

  const deleteMessage = useCallback((id: string) => {
    if (onDeleteMessage(id)) return;
    Alert.alert(
      'Message not deleted',
      'The call is reconnecting. Try again when the status returns to Live.',
    );
  }, [onDeleteMessage]);

  const renderMessage = useCallback(({ item }: { item: ChatItem }) => {
    const own = chatItemIsOwn(item, viewer);
    const scout = normalizedIdentity(item.agentId) === 'scout';
    return (
      <View style={[styles.messageRow, own && styles.messageRowOwn, scout && styles.messageRowScout]}>
        <View style={[styles.messageBubble, own && styles.messageBubbleOwn, scout && styles.messageBubbleScout, item.error && styles.messageBubbleError]}>
          <View style={styles.messageMeta}>
            <Text numberOfLines={1} style={[styles.messageAuthor, own && styles.messageAuthorOwn, scout && styles.messageAuthorScout]}>
              {own ? 'You' : scout ? 'Scout' : item.name}
            </Text>
            <Text style={[styles.messageTime, own && styles.messageTimeOwn]}>{timeLabel(item.createdAt)}</Text>
          </View>
          <RoomMessageText own={own} scout={scout} text={item.text} />
          {item.artifactId && item.workRunId && item.workStatus ? (
            <Pressable
              accessibilityHint="Opens the current activity or completed deliverable"
              accessibilityLabel={`Open ${item.workTitle || item.workFamily || 'Scout work'}`}
              accessibilityRole="button"
              onPress={() => onOpenArtifact?.(item.artifactId!, item.workTitle || item.workFamily || 'Scout work')}
              style={({ pressed }) => [styles.workCard, pressed && styles.pressed]}
            >
              <View style={styles.workCardHeader}>
                <Text numberOfLines={1} style={styles.workCardFamily}>{item.workFamily || 'Scout work'}</Text>
                <Text style={[styles.workCardStatus, item.workStatus === 'needs_attention' && styles.workCardStatusAttention]}>
                  {item.workStatus === 'complete' ? 'Delivered' : item.workStatus === 'needs_attention' ? 'Needs attention' : item.workStatus === 'approval_required' ? 'Needs approval' : 'Working'}
                </Text>
              </View>
              <Text numberOfLines={2} style={styles.workCardTitle}>{item.workTitle || 'Open work'}</Text>
              {typeof item.workProgress === 'number' ? (
                <View style={styles.workProgressTrack}>
                  <View style={[styles.workProgressFill, { width: `${item.workProgress}%` as `${number}%` }]} />
                </View>
              ) : null}
              <View style={styles.workCardOpenRow}>
                <Text style={styles.workCardOpen}>Open</Text>
                <SymbolView name="arrow.up.right" tintColor="#FF8A5B" size={12} />
              </View>
            </Pressable>
          ) : null}
          {item.followThroughId && item.followThroughStatus ? (
            <Text style={styles.followThroughStatus}>
              {item.followThroughStatus === 'awaiting_input' ? 'Needs a destination' : 'Scheduled work'}
            </Text>
          ) : null}
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

  const renderRecapFacts = useCallback((title: string, facts: readonly MeetingIntelligenceFact[]) => {
    if (!facts.length) return null;
    return (
      <View style={styles.recapSection}>
        <Text style={styles.recapSectionTitle}>{title}</Text>
        {facts.map((fact, index) => (
          <View key={`${title}-${fact.sourceId ?? index}-${fact.text}`} style={styles.recapFact}>
            <View style={styles.recapBullet} />
            <View style={styles.recapFactCopy}>
              <Text selectable style={styles.recapFactText}>{fact.text}</Text>
              {fact.owner || fact.status ? (
                <Text style={styles.recapFactMeta}>{[fact.owner, fact.status].filter(Boolean).join(' · ')}</Text>
              ) : null}
            </View>
          </View>
        ))}
      </View>
    );
  }, []);

  return (
    <Modal
      animationType="slide"
      onShow={measureSheetKeyboardOffset}
      onRequestClose={onClose}
      presentationStyle="pageSheet"
      visible={visible}
    >
      <View
        ref={sheetRef}
        style={styles.sheet}
        onLayout={measureSheetKeyboardOffset}
      >
        <KeyboardAvoidingView
          behavior={Platform.OS === 'ios' ? 'padding' : undefined}
          keyboardVerticalOffset={keyboardOffset}
          style={styles.fill}
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
          {(['recap', 'transcript', 'chat'] as const).map((item) => {
            const selected = mode === item;
            return (
              <Pressable
                accessibilityLabel={item === 'chat' ? 'Room chat' : item === 'transcript' ? 'Live transcript' : 'Meeting recap'}
                accessibilityRole="tab"
                accessibilityState={{ selected }}
                key={item}
                onPress={() => onModeChange(item)}
                style={[styles.segment, selected && styles.segmentSelected]}
              >
                <SymbolView
                  name={item === 'chat' ? 'bubble.left.and.bubble.right.fill' : item === 'transcript' ? 'captions.bubble.fill' : 'doc.text.fill'}
                  tintColor={selected ? ink[950] : 'rgba(255,255,255,0.58)'}
                  size={15}
                />
                <Text style={[styles.segmentText, selected && styles.segmentTextSelected]}>
                  {item === 'chat' ? 'Chat' : item === 'transcript' ? 'Transcript' : 'Recap'}
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
              onContentSizeChange={chatTail.onContentSizeChange}
              onLayout={chatTail.onLayout}
              onMomentumScrollBegin={chatTail.onMomentumScrollBegin}
              onMomentumScrollEnd={chatTail.onMomentumScrollEnd}
              onScroll={chatTail.onScroll}
              onScrollBeginDrag={chatTail.onScrollBeginDrag}
              onScrollEndDrag={chatTail.onScrollEndDrag}
              ref={chatListRef}
              renderItem={renderMessage}
              scrollEventThrottle={100}
              style={styles.list}
            />
            <View style={[styles.composerShell, { paddingBottom: Math.max(safeArea.bottom, space[3]) }]}>
              {composerDictation.state === 'idle' && !/(^|[^\p{L}\p{N}])@scout(?![\p{L}\p{N}])/iu.test(draft) ? (
                <Pressable
                  accessibilityHint="Inserts a Scout mention so Scout answers in the shared room chat"
                  accessibilityLabel="Ask Scout in room chat"
                  accessibilityRole="button"
                  onPress={insertScoutMention}
                  style={({ pressed }) => [styles.scoutMentionShortcut, pressed && styles.pressed]}
                >
                  <SymbolView name="sparkles" tintColor="#FF7A45" size={13} />
                  <Text style={styles.scoutMentionShortcutLabel}>@Scout</Text>
                </Pressable>
              ) : null}
              {composerError || composerDictation.error ? (
                <Text accessibilityLiveRegion="polite" style={styles.composerError}>
                  {composerError || composerDictation.error}
                </Text>
              ) : null}
              <View style={styles.composer}>
                {composerDictation.state === 'listening' || composerDictation.state === 'held' || composerDictation.state === 'transcribing' || composerDictation.state === 'error' ? (
                  <>
                    <Pressable
                      accessibilityLabel="Delete dictated message"
                      accessibilityRole="button"
                      onPress={() => { void composerDictation.discard(); }}
                      style={({ pressed }) => [styles.composerIcon, pressed && styles.pressed]}
                    >
                      <SymbolView name="xmark" tintColor="rgba(255,255,255,0.66)" size={17} />
                    </Pressable>
                    <View style={styles.voiceBody}>
                      {composerDictation.state === 'transcribing' ? (
                        <View style={styles.transcribingRow}>
                          <ActivityIndicator color="rgba(255,255,255,0.66)" size="small" />
                          <Text accessibilityLiveRegion="polite" style={styles.voiceState}>Transcribing</Text>
                        </View>
                      ) : (
                        <>
                          <Waveform
                            color="rgba(255,255,255,0.72)"
                            height={27}
                            listening={composerDictation.state === 'listening'}
                            scale={0.55}
                            trace={composerDictation.trace}
                          />
                          <Text style={styles.voiceState}>
                            {composerDictation.state === 'listening'
                              ? 'Recording · send when finished'
                              : composerDictation.state === 'error'
                                ? 'Recording saved · try send again'
                                : 'Ready to send'}
                          </Text>
                        </>
                      )}
                    </View>
                    <Pressable
                      accessibilityLabel="Transcribe and send"
                      accessibilityRole="button"
                      accessibilityState={{ disabled: composerDictation.state === 'transcribing' }}
                      disabled={composerDictation.state === 'transcribing'}
                      onPress={() => { void composerDictation.commit(); }}
                      style={({ pressed }) => [styles.send, composerDictation.state === 'transcribing' && styles.sendDisabled, pressed && styles.pressed]}
                    >
                      <SymbolView name="arrow.up" tintColor={ink[950]} size={16} />
                    </Pressable>
                  </>
                ) : (
                  <>
                    <Pressable
                      accessibilityHint="Starts dictation. Press Send once when you are finished to transcribe and post it to the room."
                      accessibilityLabel="Dictate a room message"
                      accessibilityRole="button"
                      onPress={() => { void composerDictation.start(); }}
                      style={({ pressed }) => [styles.composerIcon, pressed && styles.pressed]}
                    >
                      <SymbolView name="mic.fill" tintColor="#FF5A19" size={18} />
                    </Pressable>
                    <TextInput
                      accessibilityLabel="Message the room or mention Scout"
                      maxLength={4000}
                      multiline
                      onChangeText={(value) => {
                        setDraft(value);
                        if (composerError) setComposerError(null);
                      }}
                      onSubmitEditing={submitDraft}
                      placeholder="Message the room or @Scout"
                      placeholderTextColor="rgba(255,255,255,0.38)"
                      ref={composerInputRef}
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
                  </>
                )}
              </View>
            </View>
          </>
        ) : mode === 'transcript' ? (
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
            onContentSizeChange={transcriptTail.onContentSizeChange}
            onLayout={transcriptTail.onLayout}
            onMomentumScrollBegin={transcriptTail.onMomentumScrollBegin}
            onMomentumScrollEnd={transcriptTail.onMomentumScrollEnd}
            onScroll={transcriptTail.onScroll}
            onScrollBeginDrag={transcriptTail.onScrollBeginDrag}
            onScrollEndDrag={transcriptTail.onScrollEndDrag}
            ref={transcriptListRef}
            renderItem={renderTranscriptEntry}
            scrollEventThrottle={100}
            style={styles.list}
          />
        ) : (
          <ScrollView contentContainerStyle={[styles.recapContent, { paddingBottom: Math.max(safeArea.bottom, space[6]) }]} style={styles.list}>
            <View style={styles.recapStatusCard}>
              <View style={styles.recapStatusRow}>
                <View style={[styles.recapStatusDot, intelligence?.transcript.state !== 'listening' && styles.recapStatusDotWarning]} />
                <Text accessibilityLiveRegion="polite" style={styles.recapStatusText}>{meetingIntelligenceStatusLabel(intelligence)}</Text>
              </View>
              {intelligence?.notes.groundedThrough ? (
                <Text style={styles.recapCoverage}>
                  Through {timeLabel(intelligence.notes.groundedThrough)} · {intelligence.recap?.sourceCount ?? 0} sources
                </Text>
              ) : null}
            </View>
            {intelligence?.recap ? (
              <>
                {intelligence.recap.title ? <Text style={styles.recapTitle}>{intelligence.recap.title}</Text> : null}
                {renderRecapFacts('Decisions', intelligence.recap.decisions)}
                {renderRecapFacts('Actions', intelligence.recap.actions)}
                {renderRecapFacts('Open questions', intelligence.recap.openQuestions)}
                {renderRecapFacts('Topics', intelligence.recap.topics)}
                {renderRecapFacts('Risks', intelligence.recap.risks)}
              </>
            ) : (
              <View style={styles.emptyState}>
                <SymbolView name="doc.text" tintColor="rgba(255,255,255,0.35)" size={28} />
                <Text style={styles.emptyTitle}>Notes are catching up</Text>
                <Text style={styles.emptyBody}>The transcript remains live. One evolving recap will appear here when its source coverage is verified.</Text>
              </View>
            )}
          </ScrollView>
        )}
        </KeyboardAvoidingView>
      </View>
    </Modal>
  );
});

const styles = StyleSheet.create({
  sheet: { flex: 1, backgroundColor: ink[950] },
  fill: { flex: 1 },
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
  messageRowScout: { maxWidth: '94%' },
  messageBubble: { maxWidth: '100%', paddingHorizontal: space[3], paddingVertical: 10, borderRadius: 18, borderBottomLeftRadius: 6, backgroundColor: ink[800] },
  messageBubbleOwn: { borderBottomLeftRadius: 18, borderBottomRightRadius: 6, backgroundColor: '#FFFFFF' },
  messageBubbleScout: { borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,90,25,0.42)', backgroundColor: 'rgba(255,90,25,0.10)' },
  messageBubbleError: { borderColor: 'rgba(255,159,10,0.62)' },
  messageMeta: { flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 3 },
  messageAuthor: { ...type.captionMedium, flexShrink: 1, color: '#FFFFFF' },
  messageAuthorOwn: { color: ink[950] },
  messageAuthorScout: { color: '#FF8A5B' },
  messageTime: { fontSize: 10, lineHeight: 13, color: 'rgba(255,255,255,0.38)' },
  messageTimeOwn: { color: 'rgba(9,9,11,0.42)' },
  messageText: { ...type.body, color: 'rgba(255,255,255,0.88)' },
  messageTextOwn: { color: ink[950] },
  messageTextScout: { color: 'rgba(255,255,255,0.92)' },
  followThroughStatus: { ...type.captionMedium, marginTop: 7, color: '#FF8A5B' },
	workCard: { minWidth: 230, gap: 7, marginTop: space[3], padding: space[3], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,138,91,0.35)', backgroundColor: 'rgba(9,9,11,0.48)' },
	workCardHeader: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[2] },
	workCardFamily: { ...type.captionMedium, flex: 1, color: 'rgba(255,255,255,0.62)' },
	workCardStatus: { ...type.captionMedium, color: '#30D158' },
	workCardStatusAttention: { color: '#FF9F0A' },
	workCardTitle: { ...type.body, color: '#FFFFFF' },
	workProgressTrack: { height: 3, overflow: 'hidden', borderRadius: 2, backgroundColor: 'rgba(255,255,255,0.12)' },
	workProgressFill: { height: 3, borderRadius: 2, backgroundColor: '#FF8A5B' },
	workCardOpenRow: { flexDirection: 'row', alignItems: 'center', gap: 5 },
	workCardOpen: { ...type.captionMedium, color: '#FF8A5B' },
  messageMention: { fontWeight: '600', color: '#82B7FF' },
  messageMentionScout: { fontWeight: '700', color: '#FF8A5B' },
  deleteMessage: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22 },
  transcriptEntry: { gap: 6, padding: space[4], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.09)', backgroundColor: ink[850] },
  transcriptMeta: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  transcriptLiveDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: '#30D158' },
  transcriptSpeaker: { ...type.captionMedium, flex: 1, color: '#FFFFFF' },
  transcriptTime: { fontSize: 10, lineHeight: 13, color: 'rgba(255,255,255,0.38)' },
  transcriptText: { ...type.body, color: 'rgba(255,255,255,0.82)' },
  recapContent: { flexGrow: 1, gap: space[4], paddingHorizontal: space[4], paddingTop: space[2] },
  recapStatusCard: { gap: 5, padding: space[3], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.10)', backgroundColor: ink[850] },
  recapStatusRow: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  recapStatusDot: { width: 8, height: 8, borderRadius: 4, backgroundColor: '#30D158' },
  recapStatusDotWarning: { backgroundColor: '#FF9F0A' },
  recapStatusText: { ...type.captionMedium, flex: 1, color: '#FFFFFF' },
  recapCoverage: { ...type.caption, color: 'rgba(255,255,255,0.48)' },
  recapTitle: { ...type.title2, color: '#FFFFFF' },
  recapSection: { gap: space[2] },
  recapSectionTitle: { ...type.label, color: '#FF8A5B', textTransform: 'uppercase', letterSpacing: 0.7 },
  recapFact: { flexDirection: 'row', alignItems: 'flex-start', gap: space[2] },
  recapBullet: { width: 6, height: 6, marginTop: 8, borderRadius: 3, backgroundColor: 'rgba(255,255,255,0.38)' },
  recapFactCopy: { minWidth: 0, flex: 1, gap: 2 },
  recapFactText: { ...type.body, color: 'rgba(255,255,255,0.88)' },
  recapFactMeta: { ...type.caption, color: 'rgba(255,255,255,0.46)' },
  composerShell: { ...shadow.mark, paddingHorizontal: space[3], paddingTop: space[2], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: 'rgba(255,255,255,0.09)', backgroundColor: 'rgba(13,13,16,0.96)' },
  scoutMentionShortcut: { minHeight: hitMin, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: space[2], paddingHorizontal: 13, borderRadius: hitMin / 2, backgroundColor: 'rgba(255,90,25,0.10)' },
  scoutMentionShortcutLabel: { ...type.captionMedium, color: '#FF8A5B' },
  composerError: { ...type.caption, marginBottom: space[2], textAlign: 'center', color: '#FF9F0A' },
  composer: { minHeight: 50, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingLeft: 5, paddingRight: 5, paddingVertical: 5, borderRadius: 25, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.12)', backgroundColor: ink[800] },
  composerIcon: { width: 40, height: 40, alignItems: 'center', justifyContent: 'center', borderRadius: 20 },
  voiceBody: { flex: 1, minWidth: 0, minHeight: 38, alignItems: 'center', justifyContent: 'center', overflow: 'hidden' },
  transcribingRow: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  voiceState: { ...type.caption, color: 'rgba(255,255,255,0.58)' },
  input: { ...type.body, flex: 1, maxHeight: 112, minHeight: 40, paddingTop: 8, paddingBottom: 7, color: '#FFFFFF' },
  send: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22, backgroundColor: '#FFFFFF' },
  sendDisabled: { opacity: 0.34 },
  pressed: { opacity: 0.76, transform: [{ scale: 0.96 }] },
});
