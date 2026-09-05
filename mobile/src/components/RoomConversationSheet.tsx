import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  AccessibilityInfo,
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
  findNodeHandle,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { api } from '../api/client';
import type {
  ScoutResultAssetRef,
  ScoutResultTableRef,
  ScoutResultWorkbookRef,
} from '../api/types';
import { Waveform } from './Waveform';
import { hitMin, radius, shadow, space, type } from '../theme/tokens';
import { callColors } from '../theme/callTokens';
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
import {
  latestRoomConversationActivity,
  roomConversationActivityMessages,
  roomConversationFeedMessages,
  type RoomChatMessage,
} from '../realtime/roomConversation';
import { InlineArtifactPreview, type InlineArtifactKind } from '../messaging/InlineArtifactPreview';
import { workRefHasClosedResultEnvelope } from '../messaging/workTimeline';

export type RoomConversationMode = 'recap' | 'transcript' | 'chat';

type ChatItem = RoomChatMessage;

type TranscriptItem = {
  id: string;
  text: string;
  createdAt: string;
  speaker?: string;
  metadata?: Record<string, unknown>;
};

export type RoomResultArtifactRef = Readonly<{
  artifactId: string;
  artifactType: 'html_deck' | 'markdown';
  artifactVersion: number;
  artifactDigest: string;
  title: string;
}>;

type Props = {
  visible: boolean;
  mode: RoomConversationMode;
  roomName: string;
  messages: ChatItem[];
  transcriptEntries: TranscriptItem[];
  intelligence: MeetingIntelligenceSnapshot | null;
  sessionToken: string;
  viewer: { name?: string; email?: string };
  onClose: () => void;
  onDeleteMessage: (id: string) => boolean;
  onModeChange: (mode: RoomConversationMode) => void;
  onSendMessage: (text: string) => boolean;
  onOpenArtifact?: (result: RoomResultArtifactRef) => void;
  onOpenResultAsset?: (asset: ScoutResultAssetRef) => void;
  meetingRecordAvailable?: boolean;
  onOpenMeetingRecord?: (mode: Exclude<RoomConversationMode, 'chat'>) => void;
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

function resultAssets(metadata: Readonly<Record<string, string>>): ScoutResultAssetRef[] {
  try {
    const parsed = JSON.parse(String(metadata.assets ?? '')) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.slice(0, 12).flatMap((candidate) => {
      if (!candidate || typeof candidate !== 'object' || Array.isArray(candidate)) return [];
      const source = candidate as Record<string, unknown>;
      const ref = String(source.ref ?? '').trim().toLowerCase();
      const kind = String(source.kind ?? '').trim().toLowerCase();
      if (!/^[0-9a-f]{64}$/u.test(ref) || kind === 'page_image') return [];
      return [{
        ref,
        kind,
        mime: String(source.mime ?? '').trim().toLowerCase() || 'application/octet-stream',
        name: String(source.name ?? '').trim().slice(0, 180) || 'file',
      }];
    });
  } catch {
    return [];
  }
}

function resultTable(metadata: Readonly<Record<string, string>>): ScoutResultTableRef | undefined {
  try {
    const parsed = JSON.parse(String(metadata.tablePreview ?? '')) as Partial<ScoutResultTableRef>;
    if (!Array.isArray(parsed.columns) || !parsed.columns.length || !Array.isArray(parsed.rows)) return undefined;
    const columns = parsed.columns.slice(0, 12).map((value) => String(value ?? '').trim().slice(0, 180));
    if (columns.some((value) => !value)) return undefined;
    return {
      columns,
      rows: parsed.rows.slice(0, 20).filter(Array.isArray).map((row) => (
        columns.map((_, index) => String(row[index] ?? '').trim().slice(0, 180))
      )),
      truncated: parsed.truncated === true || parsed.rows.length > 20,
    };
  } catch {
    return undefined;
  }
}

function resultWorkbook(metadata: Readonly<Record<string, string>>): ScoutResultWorkbookRef | undefined {
  try {
    const parsed = JSON.parse(String(metadata.workbookPreview ?? '')) as Partial<ScoutResultWorkbookRef>;
    const fileName = String(parsed.fileName ?? '').trim().slice(0, 180);
    const sheetCount = Number(parsed.sheetCount);
    const formulaCount = Number(parsed.formulaCount);
    if (!fileName || !Number.isSafeInteger(sheetCount) || sheetCount < 1 || !Number.isSafeInteger(formulaCount) || formulaCount < 0) return undefined;
    return {
      fileName,
      mime: String(parsed.mime ?? '').trim().toLowerCase() || 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      sheetCount,
      formulaCount,
      inputPolicy: String(parsed.inputPolicy ?? '').trim() || undefined,
      sheets: (Array.isArray(parsed.sheets) ? parsed.sheets : []).slice(0, 12).flatMap((sheet) => {
        const name = String(sheet?.name ?? '').trim().slice(0, 180);
        return name ? [{ name, purpose: String(sheet?.purpose ?? '').trim().slice(0, 180) || undefined }] : [];
      }),
    };
  } catch {
    return undefined;
  }
}

function roomResultKind(value: RoomChatMessage['resultArtifactType']): InlineArtifactKind | null {
  if (value === 'html_deck') return 'html_deck';
  if (value === 'markdown') return 'document';
  if (value && ['pdf', 'image', 'table', 'workbook', 'bundle', 'file'].includes(value)) return value;
  return null;
}

function roomActivityStatus(item: RoomChatMessage): string {
  if (item.followThroughStatus === 'awaiting_input') return 'Needs input';
  if (item.followThroughStatus === 'delivered') return 'Delivered';
  if (item.followThroughStatus) return 'Working';
  if (item.workStatus === 'complete') return 'Delivered';
  if (item.workStatus === 'needs_attention') return 'Needs attention';
  if (item.workStatus === 'needs_input' || item.workStatus === 'approval_required') return 'Needs input';
  if (item.workStatus === 'queued') return 'Queued';
  return 'Working';
}

const RoomTypedResult = memo(function RoomTypedResult({
  message,
  sessionToken,
  onOpenArtifact,
  onOpenResultAsset,
}: {
  message: RoomChatMessage;
  sessionToken: string;
  onOpenArtifact?: Props['onOpenArtifact'];
  onOpenResultAsset?: Props['onOpenResultAsset'];
}) {
  const [result, setResult] = useState<{
    kind: InlineArtifactKind;
    title: string;
    text: string;
    assets: ScoutResultAssetRef[];
    table?: ScoutResultTableRef;
    workbook?: ScoutResultWorkbookRef;
  } | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const artifactId = String(message.resultArtifactId ?? '').trim();
  const artifactVersion = Number(message.resultArtifactVersion ?? 0);
  const artifactDigest = String(message.resultArtifactDigest ?? '').trim().toLowerCase();
  const declaredType = message.resultArtifactType;
  const openRef = declaredType === 'html_deck' || declaredType === 'markdown' ? {
    artifactId,
    artifactType: declaredType,
    artifactVersion,
    artifactDigest,
    title: String(message.resultTitle ?? message.workTitle ?? '').trim() || 'Deliverable',
  } satisfies RoomResultArtifactRef : null;

  useEffect(() => {
    let active = true;
    setResult(null);
    setLoading(true);
    setFailed(false);
    if (!sessionToken || !artifactId || !declaredType || !Number.isSafeInteger(artifactVersion) || artifactVersion < 1 || !/^[0-9a-f]{64}$/u.test(artifactDigest)) {
      setLoading(false);
      setFailed(true);
      return () => { active = false; };
    }
    void api.artifact(sessionToken, artifactId).then((response) => {
      if (!active) return;
      const artifact = response.artifacts[0];
      const metadata = artifact?.metadata ?? {};
      const kind = roomResultKind(declaredType);
      const loadedType = String(metadata.type ?? '').trim().toLowerCase();
      const loadedVersion = Number(metadata.artifactVersion ?? 0);
      const loadedDigest = String(metadata.contentDigest ?? '').trim().toLowerCase();
      if (!artifact || artifact.id !== artifactId || !kind || loadedType !== declaredType
        || loadedVersion !== artifactVersion || loadedDigest !== artifactDigest) {
        throw new Error('Exact result revision is unavailable.');
      }
      const assets = resultAssets(metadata);
      const table = resultTable(metadata);
      const workbook = resultWorkbook(metadata);
      if (declaredType !== 'html_deck' && declaredType !== 'markdown' && !workRefHasClosedResultEnvelope({
        id: message.workRunId || artifactId,
        mode: 'work',
        query: message.workTitle || '',
        status: 'complete',
        resultArtifactId: artifactId,
        resultArtifactType: declaredType,
        resultArtifactVersion: artifactVersion,
        resultArtifactDigest: artifactDigest,
        resultAssets: assets,
        resultTable: table,
        resultWorkbook: workbook,
      })) throw new Error('Closed result preview is unavailable.');
      if (declaredType === 'markdown' && !String(artifact.text ?? '').trim()) throw new Error('Document preview is unavailable.');
      setResult({
        kind,
        title: String(message.resultTitle ?? message.workTitle ?? metadata.title ?? '').trim() || 'Deliverable',
        text: declaredType === 'markdown' ? String(artifact.text ?? '').trim() : '',
        assets,
        table,
        workbook,
      });
      setLoading(false);
    }).catch(() => {
      if (!active) return;
      setLoading(false);
      setFailed(true);
    });
    return () => { active = false; };
  }, [artifactDigest, artifactId, artifactVersion, declaredType, message.workRunId, message.workTitle, message.resultTitle, sessionToken]);

  if (loading) return <View accessibilityRole="progressbar" style={styles.resultLoading}><ActivityIndicator color={callColors.action} /><Text style={styles.resultLoadingText}>Loading exact deliverable…</Text></View>;
  if (failed || !result) return <View accessibilityRole="alert" style={styles.resultLoading}><SymbolView name="exclamationmark.triangle" tintColor={callColors.warning} size={18} /><Text style={styles.resultLoadingText}>Exact preview unavailable</Text></View>;
  return (
    <InlineArtifactPreview
      agentName="Scout"
      artifactId={artifactId}
      artifactVersion={artifactVersion}
      artifactDigest={artifactDigest}
      assets={result.assets}
      kind={result.kind}
      onExpand={result.kind === 'document' && openRef ? () => onOpenArtifact?.({ ...openRef, title: result.title }) : undefined}
      onOpenAsset={onOpenResultAsset}
      onPresent={result.kind === 'html_deck' && openRef ? () => onOpenArtifact?.({ ...openRef, title: result.title }) : undefined}
      sessionToken={sessionToken}
      table={result.table}
      text={result.text}
      title={result.title}
      workbook={result.workbook}
    />
  );
});

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
          style={[segment.scout ? styles.messageMentionScout : styles.messageMention, own && styles.messageMentionOwn]}
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
  sessionToken,
  viewer,
  onClose,
  onDeleteMessage,
  onModeChange,
  onSendMessage,
  onOpenArtifact,
  onOpenResultAsset,
  meetingRecordAvailable,
  onOpenMeetingRecord,
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
  const [activityOpen, setActivityOpen] = useState(false);
  const [keyboardOffset, setKeyboardOffset] = useState(8);
  const recapListRef = useRef<ScrollView>(null);
  const meetingRecordButtonRef = useRef<React.ElementRef<typeof Pressable>>(null);
  const transcriptOffsetRef = useRef(0);
  const recapOffsetRef = useRef(0);
  const restoreMeetingOriginRef = useRef(false);
  const feedMessages = useMemo(() => [...roomConversationFeedMessages(messages)], [messages]);
  const activityMessages = useMemo(() => [...roomConversationActivityMessages(messages)], [messages]);
  const activityRows = useMemo(() => [...activityMessages].reverse(), [activityMessages]);
  const latestActivity = useMemo(() => latestRoomConversationActivity(messages), [messages]);

  const renderActivityItem = useCallback(({ item }: { item: RoomChatMessage }) => {
    const status = roomActivityStatus(item);
    const delegated = Boolean(item.workParentRunId && item.workRootRunId);
    const content = (
      <>
        <View style={styles.activityRowTop}>
          <Text numberOfLines={1} style={styles.activityFamily}>{item.workFamily || (item.followThroughId ? 'Follow-through' : 'Scout work')}</Text>
          <Text style={[styles.activityStatus, (status === 'Needs attention' || status === 'Needs input') && styles.activityStatusAttention]}>{status}</Text>
        </View>
        <Text numberOfLines={2} style={styles.activityRowTitle}>{item.workTitle || item.text || 'Scout activity'}</Text>
        <Text style={styles.activityTopology}>{delegated ? 'Delegated work' : 'Root work'} · {timeLabel(item.createdAt)}</Text>
        {typeof item.workProgress === 'number' ? (
          <View style={styles.workProgressTrack}>
            <View style={[styles.workProgressFill, { width: `${item.workProgress}%` as `${number}%` }]} />
          </View>
        ) : null}
      </>
    );
    // Activity is the bounded process surface. Lifecycle artifacts may contain
    // implementation JSON/HTML and are never opened as customer deliverables;
    // exact typed results already have their own verified preview in the feed.
    return <View style={styles.activityRow}>{content}</View>;
  }, []);

  useEffect(() => {
    if (!visible || mode !== 'chat' || !activityMessages.length) setActivityOpen(false);
  }, [activityMessages.length, mode, visible]);

  useEffect(() => {
    if (!visible || !restoreMeetingOriginRef.current || mode === 'chat') return;
    restoreMeetingOriginRef.current = false;
    requestAnimationFrame(() => {
      if (mode === 'transcript') transcriptListRef.current?.scrollToOffset({ offset: transcriptOffsetRef.current, animated: false });
      else recapListRef.current?.scrollTo({ y: recapOffsetRef.current, animated: false });
      requestAnimationFrame(() => {
        const handle = findNodeHandle(meetingRecordButtonRef.current);
        if (handle) AccessibilityInfo.setAccessibilityFocus(handle);
      });
    });
  }, [mode, visible]);

  const openPermanentRecord = useCallback(() => {
    if (mode === 'chat' || !meetingRecordAvailable || !onOpenMeetingRecord) return;
    restoreMeetingOriginRef.current = true;
    onOpenMeetingRecord(mode);
  }, [meetingRecordAvailable, mode, onOpenMeetingRecord]);

  const meetingRecordAction = meetingRecordAvailable && mode !== 'chat' ? (
    <Pressable
      ref={meetingRecordButtonRef}
      accessibilityHint="Opens the permanent governed record. Back returns to this exact live meeting view."
      accessibilityLabel="Open permanent Meeting Record"
      accessibilityRole="button"
      onPress={openPermanentRecord}
      style={({ pressed }) => [styles.meetingRecordAction, pressed && styles.pressed]}
    >
      <SymbolView name="doc.text.magnifyingglass" tintColor={callColors.action} size={15} />
      <Text style={styles.meetingRecordActionText}>Open Meeting Record</Text>
    </Pressable>
  ) : null;

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
    const typedResult = Boolean(item.workRunId && item.resultArtifactId && item.resultArtifactType);
    return (
      <View style={[styles.messageRow, own && styles.messageRowOwn, scout && styles.messageRowScout]}>
        <View style={[styles.messageBubble, typedResult && styles.messageBubbleResult, own && styles.messageBubbleOwn, scout && styles.messageBubbleScout, item.error && styles.messageBubbleError]}>
          <View style={styles.messageMeta}>
            <Text numberOfLines={1} style={[styles.messageAuthor, own && styles.messageAuthorOwn, scout && styles.messageAuthorScout]}>
              {own ? 'You' : scout ? 'Scout' : item.name}
            </Text>
            <Text style={[styles.messageTime, own && styles.messageTimeOwn]}>{timeLabel(item.createdAt)}</Text>
          </View>
          {typedResult ? (
            <RoomTypedResult
              message={item}
              onOpenArtifact={onOpenArtifact}
              onOpenResultAsset={onOpenResultAsset}
              sessionToken={sessionToken}
            />
          ) : <RoomMessageText own={own} scout={scout} text={item.text} />}
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
            <SymbolView name="trash" tintColor={callColors.textSecondary} size={14} />
          </Pressable>
        ) : null}
      </View>
    );
  }, [deleteMessage, onOpenArtifact, onOpenResultAsset, sessionToken, viewer]);

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
            <SymbolView name="xmark" tintColor={callColors.text} size={16} />
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
                  tintColor={selected ? callColors.onSelected : callColors.textSecondary}
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
                  <SymbolView name={emptyCopy.icon as 'bubble.left.and.bubble.right'} tintColor={callColors.textSecondary} size={28} />
                  <Text style={styles.emptyTitle}>{emptyCopy.title}</Text>
                  <Text style={styles.emptyBody}>{emptyCopy.body}</Text>
                </View>
              )}
              contentContainerStyle={[styles.listContent, !feedMessages.length && styles.emptyListContent]}
              data={feedMessages}
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
            {latestActivity ? (
              <Pressable
                accessibilityHint="Opens work progress, completion, failures, and requests for input outside the conversation"
                accessibilityLabel={`Open Activity. ${latestActivity.workTitle || latestActivity.workFamily || 'Scout work'}`}
                accessibilityRole="button"
                onPress={() => setActivityOpen(true)}
                style={({ pressed }) => [styles.activityPill, pressed && styles.pressed]}
              >
                <View style={styles.activityPillDot} />
                <Text numberOfLines={1} style={styles.activityPillTitle}>Activity</Text>
                <Text numberOfLines={1} style={styles.activityPillStatus}>
                  {latestActivity.workStatus === 'complete'
                    ? 'Delivered'
                    : latestActivity.workStatus === 'needs_attention'
                      ? 'Needs attention'
                      : latestActivity.workStatus === 'needs_input' || latestActivity.workStatus === 'approval_required' || latestActivity.followThroughStatus === 'awaiting_input'
                        ? 'Needs input'
                        : 'Working'}
                </Text>
                <SymbolView name="chevron.up" tintColor={callColors.textSecondary} size={12} />
              </Pressable>
            ) : null}
            <View style={[styles.composerShell, { paddingBottom: Math.max(safeArea.bottom, space[3]) }]}>
              {composerDictation.state === 'idle' && !/(^|[^\p{L}\p{N}])@scout(?![\p{L}\p{N}])/iu.test(draft) ? (
                <Pressable
                  accessibilityHint="Inserts a Scout mention so Scout answers in the shared room chat"
                  accessibilityLabel="Ask Scout in room chat"
                  accessibilityRole="button"
                  onPress={insertScoutMention}
                  style={({ pressed }) => [styles.scoutMentionShortcut, pressed && styles.pressed]}
                >
                  <SymbolView name="sparkles" tintColor={callColors.action} size={13} />
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
                      <SymbolView name="xmark" tintColor={callColors.textSecondary} size={17} />
                    </Pressable>
                    <View style={styles.voiceBody}>
                      {composerDictation.state === 'transcribing' ? (
                        <View style={styles.transcribingRow}>
                          <ActivityIndicator color={callColors.textSecondary} size="small" />
                          <Text accessibilityLiveRegion="polite" style={styles.voiceState}>Transcribing</Text>
                        </View>
                      ) : (
                        <>
                          <Waveform
                            color={callColors.textSecondary}
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
                      <SymbolView name="arrow.up" tintColor={callColors.onSelected} size={16} />
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
                      <SymbolView name="mic.fill" tintColor={callColors.action} size={18} />
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
                      placeholderTextColor={callColors.textSecondary}
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
                      <SymbolView name="arrow.up" tintColor={callColors.onSelected} size={16} />
                    </Pressable>
                  </>
                )}
              </View>
            </View>
          </>
        ) : mode === 'transcript' ? (
          <FlatList
            ListHeaderComponent={meetingRecordAction}
            ListEmptyComponent={(
              <View style={styles.emptyState}>
                <SymbolView name="captions.bubble" tintColor={callColors.textSecondary} size={28} />
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
            onScroll={(event) => {
              transcriptOffsetRef.current = event.nativeEvent.contentOffset.y;
              transcriptTail.onScroll(event);
            }}
            onScrollBeginDrag={transcriptTail.onScrollBeginDrag}
            onScrollEndDrag={transcriptTail.onScrollEndDrag}
            ref={transcriptListRef}
            renderItem={renderTranscriptEntry}
            scrollEventThrottle={100}
            style={styles.list}
          />
        ) : (
          <ScrollView
            contentContainerStyle={[styles.recapContent, { paddingBottom: Math.max(safeArea.bottom, space[6]) }]}
            onScroll={(event) => { recapOffsetRef.current = event.nativeEvent.contentOffset.y; }}
            ref={recapListRef}
            scrollEventThrottle={100}
            style={styles.list}
          >
            {meetingRecordAction}
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
                <SymbolView name="doc.text" tintColor={callColors.textSecondary} size={28} />
                <Text style={styles.emptyTitle}>Notes are catching up</Text>
                <Text style={styles.emptyBody}>The transcript remains live. One evolving recap will appear here when its source coverage is verified.</Text>
              </View>
            )}
          </ScrollView>
        )}
        </KeyboardAvoidingView>
        <Modal
          animationType="slide"
          onRequestClose={() => setActivityOpen(false)}
          presentationStyle="pageSheet"
          visible={activityOpen}
        >
          <View style={[styles.activitySheet, { paddingTop: Math.max(safeArea.top, space[3]) }]}>
            <View style={styles.activityHeader}>
              <View style={styles.activityHeaderCopy}>
                <Text accessibilityRole="header" style={styles.activityTitle}>Activity</Text>
                <Text style={styles.activitySubtitle}>Scout work stays here, outside the conversation.</Text>
              </View>
              <Pressable
                accessibilityLabel="Close Activity"
                accessibilityRole="button"
                onPress={() => setActivityOpen(false)}
                style={({ pressed }) => [styles.activityClose, pressed && styles.pressed]}
              >
                <SymbolView name="xmark" tintColor={callColors.text} size={16} />
              </Pressable>
            </View>
			<FlatList
			  contentContainerStyle={[styles.activityList, { paddingBottom: Math.max(safeArea.bottom, space[5]) }]}
			  data={activityRows}
			  initialNumToRender={8}
			  keyExtractor={(item) => item.workRunId || item.followThroughId || item.id}
			  maxToRenderPerBatch={8}
			  removeClippedSubviews={Platform.OS === 'android'}
			  renderItem={renderActivityItem}
			  showsVerticalScrollIndicator={false}
			  windowSize={7}
			/>
          </View>
        </Modal>
      </View>
    </Modal>
  );
});

const styles = StyleSheet.create({
  sheet: { flex: 1, backgroundColor: callColors.canvas },
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
  title: { ...type.headline, color: callColors.text },
  subtitle: { ...type.caption, marginTop: 1, color: callColors.textSecondary },
  close: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.borderControl, backgroundColor: callColors.control },
  segmentedControl: {
    minHeight: 52,
    flexDirection: 'row',
    gap: 4,
    marginHorizontal: space[4],
    marginBottom: space[3],
    padding: 4,
    borderRadius: radius.lg,
    backgroundColor: callColors.surface,
  },
  segment: { flex: 1, minHeight: 44, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 7, borderRadius: radius.md },
  segmentSelected: { backgroundColor: callColors.selected },
  segmentText: { ...type.button, color: callColors.textSecondary },
  segmentTextSelected: { color: callColors.onSelected },
  list: { flex: 1 },
  listContent: { gap: space[3], paddingHorizontal: space[4], paddingTop: space[2], paddingBottom: space[5] },
  emptyListContent: { flexGrow: 1 },
  emptyState: { flex: 1, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[8], gap: space[2] },
  emptyTitle: { ...type.headline, color: callColors.text, marginTop: space[2] },
  emptyBody: { ...type.bodySm, maxWidth: 280, textAlign: 'center', color: callColors.textSecondary },
  messageRow: { maxWidth: '88%', alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'flex-end', gap: 4 },
  messageRowOwn: { alignSelf: 'flex-end', flexDirection: 'row-reverse' },
  messageRowScout: { maxWidth: '94%' },
  messageBubble: { maxWidth: '100%', paddingHorizontal: space[3], paddingVertical: 10, borderRadius: 18, borderBottomLeftRadius: 6, backgroundColor: callColors.surface },
  messageBubbleResult: { width: '100%', paddingHorizontal: 0, paddingBottom: 0, overflow: 'hidden', backgroundColor: 'transparent' },
  messageBubbleOwn: { borderBottomLeftRadius: 18, borderBottomRightRadius: 6, backgroundColor: callColors.selected },
  messageBubbleScout: { borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.action, backgroundColor: callColors.actionSurface },
  messageBubbleError: { borderColor: callColors.warning },
  messageMeta: { flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 3 },
  messageAuthor: { ...type.captionMedium, flexShrink: 1, color: callColors.text },
  messageAuthorOwn: { color: callColors.onSelected },
  messageAuthorScout: { color: callColors.action },
  messageTime: { fontSize: 10, lineHeight: 13, color: callColors.textSecondary },
  messageTimeOwn: { color: callColors.onSelectedSecondary },
  messageText: { ...type.body, color: callColors.text },
  messageTextOwn: { color: callColors.onSelected },
  messageTextScout: { color: callColors.text },
  followThroughStatus: { ...type.captionMedium, marginTop: 7, color: callColors.action },
	resultLoading: { minWidth: 240, minHeight: 120, alignItems: 'center', justifyContent: 'center', flexDirection: 'row', gap: space[2], padding: space[4], borderRadius: radius.lg, backgroundColor: callColors.surface },
	resultLoadingText: { ...type.captionMedium, color: callColors.textSecondary },
	workCard: { minWidth: 230, gap: 7, marginTop: space[3], padding: space[3], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.action, backgroundColor: callColors.surface },
	workCardHeader: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[2] },
	workCardFamily: { ...type.captionMedium, flex: 1, color: callColors.textSecondary },
	workCardStatus: { ...type.captionMedium, color: callColors.action },
	workCardStatusAttention: { color: callColors.warning },
	workCardTitle: { ...type.body, color: callColors.text },
	workProgressTrack: { height: 3, overflow: 'hidden', borderRadius: 2, backgroundColor: callColors.control },
	workProgressFill: { height: 3, borderRadius: 2, backgroundColor: callColors.action },
	workCardOpenRow: { flexDirection: 'row', alignItems: 'center', gap: 5 },
	workCardOpen: { ...type.captionMedium, color: callColors.action },
  messageMention: { fontWeight: '600', color: callColors.action },
  messageMentionScout: { fontWeight: '700', color: callColors.action },
  messageMentionOwn: { color: callColors.onSelected, textDecorationLine: 'underline' },
  deleteMessage: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22 },
  transcriptEntry: { gap: 6, padding: space[4], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.border, backgroundColor: callColors.surface },
  transcriptMeta: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  transcriptLiveDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: callColors.speaking },
  transcriptSpeaker: { ...type.captionMedium, flex: 1, color: callColors.text },
  transcriptTime: { fontSize: 10, lineHeight: 13, color: callColors.textSecondary },
  transcriptText: { ...type.body, color: callColors.text },
  recapContent: { flexGrow: 1, gap: space[4], paddingHorizontal: space[4], paddingTop: space[2] },
  recapStatusCard: { gap: 5, padding: space[3], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.border, backgroundColor: callColors.surface },
  recapStatusRow: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  recapStatusDot: { width: 8, height: 8, borderRadius: 4, backgroundColor: callColors.action },
  recapStatusDotWarning: { backgroundColor: callColors.warning },
  recapStatusText: { ...type.captionMedium, flex: 1, color: callColors.text },
  recapCoverage: { ...type.caption, color: callColors.textSecondary },
  recapTitle: { ...type.title2, color: callColors.text },
  recapSection: { gap: space[2] },
  recapSectionTitle: { ...type.label, color: callColors.action, textTransform: 'uppercase', letterSpacing: 0.7 },
  recapFact: { flexDirection: 'row', alignItems: 'flex-start', gap: space[2] },
  recapBullet: { width: 6, height: 6, marginTop: 8, borderRadius: 3, backgroundColor: callColors.textSecondary },
  recapFactCopy: { minWidth: 0, flex: 1, gap: 2 },
  recapFactText: { ...type.body, color: callColors.text },
  recapFactMeta: { ...type.caption, color: callColors.textSecondary },
  meetingRecordAction: { minHeight: 44, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 7, paddingHorizontal: space[3], borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.action, backgroundColor: callColors.actionSurface },
  meetingRecordActionText: { ...type.button, color: callColors.action },
  activityPill: { minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: space[2], marginHorizontal: space[4], marginBottom: space[2], paddingHorizontal: space[3], borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.action, backgroundColor: callColors.surface },
  activityPillDot: { width: 8, height: 8, borderRadius: 4, backgroundColor: callColors.action },
  activityPillTitle: { ...type.captionMedium, color: callColors.text },
  activityPillStatus: { ...type.caption, flex: 1, textAlign: 'right', color: callColors.textSecondary },
  activitySheet: { flex: 1, backgroundColor: callColors.canvas },
  activityHeader: { minHeight: 72, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[4], paddingBottom: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: callColors.border },
  activityHeaderCopy: { minWidth: 0, flex: 1 },
  activityTitle: { ...type.title2, color: callColors.text },
  activitySubtitle: { ...type.caption, marginTop: 2, color: callColors.textSecondary },
  activityClose: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.borderControl, backgroundColor: callColors.control },
  activityList: { gap: space[3], padding: space[4] },
  activityRow: { minHeight: 116, gap: space[2], padding: space[4], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.border, backgroundColor: callColors.surface },
  activityRowTop: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  activityFamily: { ...type.label, flex: 1, color: callColors.textSecondary, textTransform: 'uppercase', letterSpacing: 0.6 },
  activityStatus: { ...type.captionMedium, color: callColors.action },
  activityStatusAttention: { color: callColors.warning },
  activityRowTitle: { ...type.body, color: callColors.text },
  activityTopology: { ...type.caption, color: callColors.textSecondary },
  composerShell: { ...shadow.mark, paddingHorizontal: space[3], paddingTop: space[2], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: callColors.border, backgroundColor: callColors.surface },
  scoutMentionShortcut: { minHeight: hitMin, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: space[2], paddingHorizontal: 13, borderRadius: hitMin / 2, backgroundColor: callColors.actionSurface },
  scoutMentionShortcutLabel: { ...type.captionMedium, color: callColors.action },
  composerError: { ...type.caption, marginBottom: space[2], textAlign: 'center', color: callColors.warning },
  composer: { minHeight: 50, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingLeft: 5, paddingRight: 5, paddingVertical: 5, borderRadius: 25, borderWidth: StyleSheet.hairlineWidth, borderColor: callColors.borderControl, backgroundColor: callColors.surface },
  composerIcon: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 20 },
  voiceBody: { flex: 1, minWidth: 0, minHeight: 38, alignItems: 'center', justifyContent: 'center', overflow: 'hidden' },
  transcribingRow: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  voiceState: { ...type.caption, color: callColors.textSecondary },
  input: { ...type.body, flex: 1, maxHeight: 112, minHeight: 40, paddingTop: 8, paddingBottom: 7, color: callColors.text },
  send: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22, backgroundColor: callColors.selected },
  sendDisabled: { opacity: 0.34 },
  pressed: { opacity: 0.76, transform: [{ scale: 0.96 }] },
});
