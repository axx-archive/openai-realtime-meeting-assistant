import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Animated,
  Keyboard,
  KeyboardAvoidingView,
  PanResponder,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import * as SecureStore from 'expo-secure-store';
import { SafeAreaView } from 'react-native-safe-area-context';
import * as Haptics from 'expo-haptics';
import * as DocumentPicker from 'expo-document-picker';
import * as ImagePicker from 'expo-image-picker';
import { Image } from 'expo-image';
import { SymbolView } from 'expo-symbols';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { useFocusEffect } from '@react-navigation/native';
import * as Linking from 'expo-linking';
import { FlashList, type FlashListRef } from '@shopify/flash-list';
import { api, BonfireApiError } from '../api/client';
import type { ChatMentionCandidate, GiphySearchResult, ScoutFileAttachment, ScoutMessage, ThreadDigestResponse } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { MessageBubble } from '../messaging/MessageBubble';
import { firstUnreadIndex } from '../messaging/unreadBoundary';
import { buildTimelineMarkers } from '../messaging/timelineMarkers';
import { CatchUpSheet } from '../messaging/CatchUpSheet';
import { MessageActionSheet } from '../messaging/MessageActionSheet';
import { MentionComposerInput } from '../messaging/MentionComposerInput';
import { AttachmentSourceSheet } from '../messaging/AttachmentSourceSheet';
import { GifPickerSheet } from '../messaging/GifPickerSheet';
import {
  attachmentBatchMessage,
  maxConcurrentAttachmentUploads,
  maxMessageAttachments,
  prepareAttachmentBatch,
  type AttachmentAssetInput,
} from '../messaging/attachmentSources';
import { ThreadNotificationMenu, type ThreadNotificationLevel } from '../messaging/ThreadNotificationMenu';
import { groupMessageReactions, isOwnMessageForViewer } from '../messaging/messagePresentation';
import {
  applyChatThreadEvent,
  chatThreadEventJournalCovers,
  isMessageRunEnd,
  maxChatThreadEventJournal,
  resolveChatThreadSnapshot,
  type ChatThreadEventPayload,
  type ChatTypingEventPayload,
  type SequencedChatThreadEvent,
} from '../messaging/chatRealtime';
import { TypingIndicator, type TypingParticipant } from '../messaging/TypingIndicator';
import { FilePreviewModal } from '../components/FilePreviewModal';
import {
  shouldBeginTimestampReveal,
  timestampRevealProgress,
} from '../messaging/messageGestures';

type ThreadRow = {
  message: ScoutMessage;
  own: boolean;
  showAuthor: boolean;
  showAvatar: boolean;
  avatarDataURL?: string;
  timelineLabel?: string;
  boundary: boolean;
};
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { Glass } from '../theme/glass';
import { Waveform } from '../components/Waveform';
import { useDictation } from '../voice/useDictation';
import type { AudioFocusLease } from '../voice/AudioFocusCoordinator';
import { audioFocusRuntime } from '../realtime/audioFocusRuntime';
import type { RootStackParamList } from '../navigation/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { useReduceMotion } from '../theme/motion';

type Props = NativeStackScreenProps<RootStackParamList, 'Thread'>;
const DICTATION_DISCLOSURE_KEY = 'bonfire.dictation.serverDisclosure.v1';

/**
 * A thread — design §14.
 *
 * Rebuilt from a card list plus an "Ask" box into genuine messaging. The
 * composer is glass (it floats above the conversation, §7); the bubbles are not
 * (they ARE the conversation).
 *
 * The mic is a peer of the keyboard, not an afterthought: holding it dictates
 * into the draft with company-vocabulary transcription, which is the whole
 * reason to type work messages here rather than in Slack (§10).
 */
export function ThreadScreen({ route, navigation }: Props) {
  const { sessionToken, user } = useAuth();
  const office = useOfficeEvents();
  const reduceMotion = useReduceMotion();
  const [messages, setMessages] = useState<ScoutMessage[]>([]);
  const [draft, setDraft] = useState('');
  const [loading, setLoading] = useState(true);
  const [sending, setSending] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [pendingFiles, setPendingFiles] = useState<ScoutFileAttachment[]>([]);
  const [stagingFiles, setStagingFiles] = useState<Array<{ id: string; name: string; mime: string; uri?: string }>>([]);
  const [editingMessage, setEditingMessage] = useState<ScoutMessage | null>(null);
  const [replyingTo, setReplyingTo] = useState<ScoutMessage | null>(null);
  const [actionMessage, setActionMessage] = useState<{ message: ScoutMessage; own: boolean } | null>(null);
  const [previewFile, setPreviewFile] = useState<ScoutFileAttachment | null>(null);
  const [participants, setParticipants] = useState<ChatMentionCandidate[]>([{ name: 'Scout', kind: 'scout' }]);
  const [threadVisibility, setThreadVisibility] = useState('private');
  const [threadOwnerEmail, setThreadOwnerEmail] = useState('');
  const [notificationLevel, setNotificationLevel] = useState<ThreadNotificationLevel>('all');
  const [notificationMenuOpen, setNotificationMenuOpen] = useState(false);
  const [notificationBusy, setNotificationBusy] = useState(false);
  const [attachmentSourceOpen, setAttachmentSourceOpen] = useState(false);
  const [gifPickerOpen, setGifPickerOpen] = useState(false);
  const [typingParticipants, setTypingParticipants] = useState<TypingParticipant[]>([]);
  const [error, setError] = useState<string | null>(null);
  // null means "not yet loaded" — distinct from "" which means never read.
  const [readAt, setReadAt] = useState<string | null>(null);
  const listRef = useRef<FlashListRef<ThreadRow>>(null);
  // Starts false while the thread loads. A normal open flips it true once the
  // bottom-rendered list is on screen; a targeted message link stays false
  // until the viewer actually reaches the latest message.
  const atBottomRef = useRef(false);
  const listHeightRef = useRef(0);
	const lastMarkedMessageIDRef = useRef<string | null>(null);
	const markingMessageIDRef = useRef<string | null>(null);
  const [digest, setDigest] = useState<ThreadDigestResponse | null>(null);
  const [catchUpOpen, setCatchUpOpen] = useState(false);
  const dictationDisclosureAcceptedRef = useRef(false);
  const dictationDisclosureOpenRef = useRef(false);
  const dictationTouchStartYRef = useRef<number | null>(null);
  const dictationTouchActiveRef = useRef(false);
  const dictationFocusLeaseRef = useRef<AudioFocusLease | null>(null);
  const typingExpiryTimersRef = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());
  const typingIdleTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const typingActiveRef = useRef(false);
  const typingLastSignalAtRef = useRef(0);
  const transcriptGenerationRef = useRef(0);
  const transcriptEventJournalRef = useRef<SequencedChatThreadEvent[]>([]);
  const applyTranscriptSnapshot = useCallback((generationAtRequest: number, next: ScoutMessage[]) => {
    const currentGeneration = transcriptGenerationRef.current;
    const journal = [...transcriptEventJournalRef.current];
    const canResolve = chatThreadEventJournalCovers(generationAtRequest, currentGeneration, journal);
    if (!canResolve) return false;
    setMessages((current) => resolveChatThreadSnapshot(
      current,
      next,
      route.params.threadId,
      generationAtRequest,
      currentGeneration,
      journal,
    ).messages);
    return true;
  }, [route.params.threadId]);
  const timestampReveal = useRef(new Animated.Value(0)).current;
  const timestampPan = useMemo(() => PanResponder.create({
    onMoveShouldSetPanResponder: (_event, gesture) => shouldBeginTimestampReveal(gesture.dx, gesture.dy),
    onPanResponderMove: (_event, gesture) => {
      timestampReveal.setValue(timestampRevealProgress(gesture.dx));
    },
    onPanResponderRelease: () => {
      if (reduceMotion) timestampReveal.setValue(0);
      else Animated.spring(timestampReveal, { toValue: 0, damping: 18, stiffness: 240, mass: 0.8, useNativeDriver: true }).start();
    },
    onPanResponderTerminate: () => {
      if (reduceMotion) timestampReveal.setValue(0);
      else Animated.spring(timestampReveal, { toValue: 0, damping: 18, stiffness: 240, mass: 0.8, useNativeDriver: true }).start();
    },
  }), [reduceMotion, timestampReveal]);

  // Scroll to a cited message. Both the catch-up and the deposit rail point at
  // real messages, so both need to be able to land on one.
  const scrollToMessage = useCallback(
    (messageId: string) => {
      const index = messages.findIndex((message) => String(message.id) === messageId);
      if (index >= 0) listRef.current?.scrollToIndex({ index, animated: true });
    },
    [messages],
  );

  const dictation = useDictation({
    context: 'chat',
    threadId: route.params.threadId,
    // Dictation takes the same ordinary send path as typed text. The hook
    // generation-fences late results, so this callback runs once per Send.
    onTranscript: ({ text }) => { void send(text); },
  });
  const dictationLifecycleRef = useRef({ cancel: dictation.cancel, stop: dictation.stop });
  dictationLifecycleRef.current = { cancel: dictation.cancel, stop: dictation.stop };

	const beginDictation = useCallback(async () => {
		const startCapture = async () => {
			let exactLease: AudioFocusLease | null = null;
			const lease = await audioFocusRuntime.acquire('composer_dictation', {
				forceClose: async () => {
					if (exactLease) {
						if (dictationFocusLeaseRef.current === exactLease) dictationFocusLeaseRef.current = null;
						dictation.fenceFocusLease(exactLease);
					}
					dictation.cancel();
					await dictation.stop(); // park the local clip; never send it on takeover.
				},
			});
			exactLease = lease;
			if (!lease.isCurrent() || !dictationTouchActiveRef.current) {
				await lease.release('cancelled');
				return;
			}
			dictationFocusLeaseRef.current = lease;
			const started = await dictation.start(lease);
			if (!started && dictationFocusLeaseRef.current === lease) dictationFocusLeaseRef.current = null;
		};
		if (dictationDisclosureAcceptedRef.current) {
			if (dictationTouchActiveRef.current) await startCapture();
			return;
		}
		const stored = await SecureStore.getItemAsync(DICTATION_DISCLOSURE_KEY).catch(() => null);
		if (stored === 'accepted') {
			dictationDisclosureAcceptedRef.current = true;
			if (dictationTouchActiveRef.current) await startCapture();
			return;
		}
		if (dictationDisclosureOpenRef.current) return;
		dictationDisclosureOpenRef.current = true;
		Alert.alert(
			'Voice transcription',
			'Your voice is sent to Bonfire to transcribe with your company\'s vocabulary, then the audio is deleted. Only the text stays.',
			[
				{ text: 'Not now', style: 'cancel', onPress: () => { dictationDisclosureOpenRef.current = false; } },
				{
					text: 'I understand',
					onPress: () => {
						dictationDisclosureOpenRef.current = false;
						dictationDisclosureAcceptedRef.current = true;
						void SecureStore.setItemAsync(DICTATION_DISCLOSURE_KEY, 'accepted');
					},
				},
			],
		);
	}, [dictation]);

	useEffect(() => () => {
		dictationTouchActiveRef.current = false;
		dictationTouchStartYRef.current = null;
		dictationLifecycleRef.current.cancel();
		void dictationLifecycleRef.current.stop();
		const lease = dictationFocusLeaseRef.current;
		dictationFocusLeaseRef.current = null;
		void lease?.release('cancelled');
	}, []);

  const load = useCallback(async () => {
    if (!sessionToken) return;
    const generationAtRequest = transcriptGenerationRef.current;
    try {
      const response = await api.scoutThread(sessionToken, route.params.threadId);
      const next = response.thread?.messages ?? response.messages ?? [];
      applyTranscriptSnapshot(generationAtRequest, next);
      setThreadVisibility(String(response.thread?.visibility ?? 'private'));
      setThreadOwnerEmail(String(response.thread?.ownerEmail ?? ''));
      const level = String(response.notificationLevel ?? (response.muted ? 'mentions' : 'all'));
      setNotificationLevel(level === 'mentions' || level === 'none' ? level : 'all');
      // Captured once, on the FIRST load only. If it tracked every refresh the
      // divider would jump to the bottom the moment the marker advanced, and
      // the "80 new messages" line would vanish while you were still reading
      // through them.
      setReadAt((current) => (current === null ? String(response.readAt ?? '') : current));
      setError(null);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not load this thread.');
    } finally {
      setLoading(false);
    }
  }, [applyTranscriptSnapshot, route.params.threadId, sessionToken]);

  useFocusEffect(useCallback(() => {
    void load();
  }, [load]));

  useEffect(() => {
    const messageId = route.params.messageId;
    if (!loading && messageId) {
      requestAnimationFrame(() => scrollToMessage(messageId));
    }
  }, [loading, route.params.messageId, scrollToMessage]);

  useEffect(() => {
    if (!sessionToken) return;
    let active = true;
    void api.chatParticipants(sessionToken).then((response) => {
      if (active && response.participants.length > 0) setParticipants(response.participants);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [sessionToken]);

  // Catch-up arrives after first paint; it is not needed to read a message.
  useEffect(() => {
    if (!sessionToken) return;
    let cancelled = false;
    void api
      .threadDigest(sessionToken, route.params.threadId)
      .then((response) => {
        if (!cancelled) setDigest(response);
      })
      .catch(() => {
        // An absent digest simply means no catch-up affordance.
      });
    return () => {
      cancelled = true;
    };
  }, [route.params.threadId, sessionToken]);

  const reconcileThread = useCallback(async () => {
    if (!sessionToken) return;
    const generationAtStart = transcriptGenerationRef.current;
    try {
      const response = await api.scoutThread(sessionToken, route.params.threadId);
      if (generationAtStart !== transcriptGenerationRef.current) return;
      const shouldFollow = atBottomRef.current;
      const next = response.thread?.messages ?? response.messages ?? [];
      if (!applyTranscriptSnapshot(generationAtStart, next)) return;
      setThreadVisibility(String(response.thread?.visibility ?? 'private'));
      setThreadOwnerEmail(String(response.thread?.ownerEmail ?? ''));
      if (shouldFollow) requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: false }));
    } catch {
      // Recovery is intentionally silent; the live transcript remains usable
      // and the next 12-second pass or socket frame can self-heal it.
    }
  }, [applyTranscriptSnapshot, route.params.threadId, sessionToken]);

  // The socket is the fast path: matching message additions, replacements,
  // and deletions land immediately without waiting on a network round trip.
  useEffect(() => {
    if (office.event !== 'chat_thread' || !sessionToken) return;
    const payload = office.data as ChatThreadEventPayload | null;
    if (!payload || String(payload.id ?? '') !== route.params.threadId) return;
    const generation = transcriptGenerationRef.current + 1;
    transcriptGenerationRef.current = generation;
    transcriptEventJournalRef.current = [
      ...transcriptEventJournalRef.current,
      { generation, payload },
    ].slice(-maxChatThreadEventJournal);
    const shouldFollow = atBottomRef.current;
    setMessages((current) => applyChatThreadEvent(current, route.params.threadId, payload));
    if (payload?.visibility) setThreadVisibility(String(payload.visibility));
    const authorEmail = String(payload?.message?.authorEmail ?? '').trim().toLowerCase();
    if (authorEmail) {
      const timer = typingExpiryTimersRef.current.get(authorEmail);
      if (timer) clearTimeout(timer);
      typingExpiryTimersRef.current.delete(authorEmail);
      setTypingParticipants((current) => current.filter((participant) => participant.email !== authorEmail));
    }
    if (shouldFollow) requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: false }));
  }, [office.data, office.event, office.version, route.params.threadId, sessionToken]);

  // Socket delivery can be missed during suspension or a half-open network
  // transition. A bounded authoritative pass repairs drift without making the
  // transcript flash or showing transient recovery errors.
  useFocusEffect(useCallback(() => {
    const timer = setInterval(() => void reconcileThread(), 12_000);
    return () => clearInterval(timer);
  }, [reconcileThread]));

	const wasConnectedRef = useRef(office.connected);
	useEffect(() => {
		const reconnected = !wasConnectedRef.current && office.connected;
		wasConnectedRef.current = office.connected;
		if (reconnected) void load();
	}, [load, office.connected]);

  const email = user?.email?.trim().toLowerCase() ?? '';
  const participantByEmail = useMemo(() => new Map(
    participants
      .filter((participant) => participant.email)
      .map((participant) => [String(participant.email).trim().toLowerCase(), participant]),
  ), [participants]);

  useEffect(() => {
    if (office.event !== 'chat_typing') return;
    const payload = office.data as ChatTypingEventPayload | null;
    if (String(payload?.threadId ?? '') !== route.params.threadId) return;
    const actorEmail = String(payload?.email ?? '').trim().toLowerCase();
    if (!actorEmail || actorEmail === email) return;
    const priorTimer = typingExpiryTimersRef.current.get(actorEmail);
    if (priorTimer) clearTimeout(priorTimer);
    typingExpiryTimersRef.current.delete(actorEmail);
    if (payload?.typing === false) {
      setTypingParticipants((current) => current.filter((participant) => participant.email !== actorEmail));
      return;
    }
    const known = participantByEmail.get(actorEmail);
    const participant: TypingParticipant = {
      email: actorEmail,
      name: String(payload?.name ?? known?.name ?? actorEmail.split('@')[0] ?? 'Someone'),
      avatarDataURL: String(payload?.avatarDataURL ?? known?.avatarDataURL ?? '') || undefined,
    };
    setTypingParticipants((current) => [
      ...current.filter((candidate) => candidate.email !== actorEmail),
      participant,
    ]);
    const timer = setTimeout(() => {
      typingExpiryTimersRef.current.delete(actorEmail);
      setTypingParticipants((current) => current.filter((candidate) => candidate.email !== actorEmail));
    }, 4_500);
    typingExpiryTimersRef.current.set(actorEmail, timer);
  }, [email, office.data, office.event, office.version, participantByEmail, route.params.threadId]);

  useEffect(() => () => {
    typingExpiryTimersRef.current.forEach((timer) => clearTimeout(timer));
    typingExpiryTimersRef.current.clear();
  }, []);

  useEffect(() => {
    if (office.connected) return;
    typingExpiryTimersRef.current.forEach((timer) => clearTimeout(timer));
    typingExpiryTimersRef.current.clear();
    setTypingParticipants([]);
  }, [office.connected]);

  const stopTyping = useCallback((notify = true) => {
    if (typingIdleTimerRef.current) clearTimeout(typingIdleTimerRef.current);
    typingIdleTimerRef.current = null;
    const wasActive = typingActiveRef.current;
    typingActiveRef.current = false;
    typingLastSignalAtRef.current = 0;
    if (notify && wasActive) {
      office.send('chat_typing', { threadId: route.params.threadId, typing: false });
    }
  }, [office.send, route.params.threadId]);

  const changeDraft = useCallback((value: string) => {
    setDraft(value);
    if (threadVisibility !== 'public' || editingMessage || !sessionToken || !value.trim()) {
      stopTyping();
      return;
    }
    const now = Date.now();
    if (!typingActiveRef.current || now - typingLastSignalAtRef.current >= 1_800) {
      office.send('chat_typing', { threadId: route.params.threadId, typing: true });
      typingLastSignalAtRef.current = now;
    }
    typingActiveRef.current = true;
    if (typingIdleTimerRef.current) clearTimeout(typingIdleTimerRef.current);
    typingIdleTimerRef.current = setTimeout(() => stopTyping(), 2_800);
  }, [editingMessage, office.send, route.params.threadId, sessionToken, stopTyping, threadVisibility]);

  useEffect(() => () => stopTyping(), [stopTyping]);

  useEffect(() => {
    if (!office.connected) stopTyping(false);
  }, [office.connected, stopTyping]);

  useEffect(() => {
    if (typingParticipants.length > 0 && atBottomRef.current) {
      requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: false }));
    }
  }, [typingParticipants.length]);

  // Where the "N new messages" divider goes. -1 means everything is read and
  // no divider renders.
  const boundary = useMemo(
    () => firstUnreadIndex(messages, readAt ?? undefined, email),
    [email, messages, readAt],
  );

  const timelineLabels = useMemo(
    () => buildTimelineMarkers(messages),
    [messages],
  );

  const rows = useMemo(
    () =>
      messages.map((message, index) => {
        const own = isOwnMessageForViewer(message, {
          viewerEmail: email,
          threadVisibility,
          threadOwnerEmail,
        });
        const previous = messages[index - 1];
        const showAvatar = !own
          && String(message.role ?? '').toLowerCase() === 'user'
          && isMessageRunEnd(messages, index);
        const knownParticipant = participantByEmail.get(String(message.authorEmail ?? '').trim().toLowerCase());
        const showAuthor =
          !previous ||
          previous.role !== message.role ||
          previous.authorEmail !== message.authorEmail;
        return {
          message,
          own,
          showAuthor,
          showAvatar,
          avatarDataURL: String(message.avatarDataURL ?? knownParticipant?.avatarDataURL ?? '') || undefined,
          timelineLabel: timelineLabels[index],
          // The divider is part of the row above the first unread message
          // rather than a separate list item, so it cannot desync from it.
          boundary: index === boundary,
        };
      }),
    [boundary, email, messages, participantByEmail, threadOwnerEmail, threadVisibility, timelineLabels],
  );

  const unreadBelow = boundary >= 0 ? messages.length - boundary : 0;

  // Advance the marker only when the latest message is genuinely on screen.
  // Normal opens now land there; targeted links to older messages do not.
  const markRead = useCallback(() => {
    if (!sessionToken || messages.length === 0) return;
	const last = messages[messages.length - 1];
	if (!last?.id) return;
	const messageID = String(last.id);
	if (lastMarkedMessageIDRef.current === messageID || markingMessageIDRef.current === messageID) return;
	markingMessageIDRef.current = messageID;
	void api.markThreadRead(sessionToken, route.params.threadId, messageID)
		.then(() => { lastMarkedMessageIDRef.current = messageID; })
		.catch(() => {
			// Best-effort: a failed mark just means the thread still shows unread,
			// which is the safe direction to fail in.
		})
		.finally(() => {
			if (markingMessageIDRef.current === messageID) markingMessageIDRef.current = null;
		});
  }, [messages, route.params.threadId, sessionToken]);

  // Leaving the thread while at the bottom counts as having read it.
  useEffect(() => {
    return () => {
      if (atBottomRef.current) markRead();
    };
  }, [markRead]);

  async function send(textOverride?: string) {
    const text = (textOverride ?? draft).trim();
    const messageFiles = textOverride === undefined ? pendingFiles : [];
    if (!sessionToken || (!text && messageFiles.length === 0) || sending || uploading) return;
    stopTyping();
    setSending(true);
    setError(null);
    const generationAtRequest = transcriptGenerationRef.current;
    try {
      const response = editingMessage
        ? await api.updateScoutMessage(sessionToken, route.params.threadId, String(editingMessage.id), text, messageFiles)
        : await api.sendScoutMessage(sessionToken, route.params.threadId, text, messageFiles, String(replyingTo?.id ?? ''));
      if (textOverride === undefined) {
        setDraft('');
        setPendingFiles([]);
        setEditingMessage(null);
        setReplyingTo(null);
      }
      applyTranscriptSnapshot(generationAtRequest, response.thread?.messages ?? response.messages ?? []);
      Keyboard.dismiss();
	  atBottomRef.current = true;
	  requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: false }));
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Message did not send.');
    } finally {
      setSending(false);
    }
  }

  async function uploadAttachmentAssets(assets: readonly AttachmentAssetInput[]): Promise<boolean> {
    if (!sessionToken || uploading) return false;
    const remaining = maxMessageAttachments - pendingFiles.length;
    const batch = prepareAttachmentBatch(assets, remaining);
    const issues = attachmentBatchMessage(batch);
    if (batch.accepted.length === 0) {
      if (issues) setError(issues);
      return false;
    }

    setUploading(true);
    setError(issues || null);
    const staging = batch.accepted.map((asset, index) => ({
      id: `${Date.now()}-${index}-${asset.name}`,
      name: asset.name,
      mime: asset.mime,
      uri: asset.uri,
    }));
    setStagingFiles((current) => [...current, ...staging]);
    try {
      const outcomes: Array<{ file: ScoutFileAttachment | null; error: string }> = [];
      for (let index = 0; index < batch.accepted.length; index += maxConcurrentAttachmentUploads) {
        const chunk = batch.accepted.slice(index, index + maxConcurrentAttachmentUploads);
        outcomes.push(...await Promise.all(chunk.map(async (asset) => {
          try {
            return { file: await api.uploadScoutAttachment(sessionToken, route.params.threadId, asset), error: '' };
          } catch (caught) {
            return {
              file: null,
              error: caught instanceof Error ? caught.message : `${asset.name} could not be attached.`,
            };
          }
        })));
      }
      const uploaded = outcomes.flatMap((outcome) => outcome.file ? [outcome.file] : []);
      const failures = outcomes.map((outcome) => outcome.error).filter(Boolean);
      if (uploaded.length > 0) {
        setPendingFiles((current) => [...current, ...uploaded].slice(0, maxMessageAttachments));
        void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      }
      const message = [issues, ...failures].filter(Boolean).join(' ');
      setError(message || null);
      return uploaded.length > 0 && failures.length === 0;
    } finally {
      const stagingIDs = new Set(staging.map((file) => file.id));
      setStagingFiles((current) => current.filter((file) => !stagingIDs.has(file.id)));
      setUploading(false);
    }
  }

  async function pickFiles() {
    if (uploading || pendingFiles.length >= maxMessageAttachments) return;
    try {
      const result = await DocumentPicker.getDocumentAsync({
        type: ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'application/pdf'],
        multiple: true,
        copyToCacheDirectory: true,
      });
      if (result.canceled) return;
      await uploadAttachmentAssets(result.assets.map((asset) => ({
        uri: asset.uri,
        name: asset.name,
        mime: asset.mimeType,
        size: asset.size,
      })));
    } catch {
      setError('Could not open Files. Please try again.');
    }
  }

  async function pickPhotos() {
    if (uploading || pendingFiles.length >= maxMessageAttachments) return;
    try {
      const result = await ImagePicker.launchImageLibraryAsync({
        mediaTypes: ['images'],
        allowsMultipleSelection: true,
        selectionLimit: maxMessageAttachments - pendingFiles.length,
        // Photo-library images are message previews, not archival masters. A
        // modest JPEG compression keeps staging responsive while preserving
        // enough resolution for the full-screen viewer.
        quality: 0.82,
        preferredAssetRepresentationMode: ImagePicker.UIImagePickerPreferredAssetRepresentationMode.Compatible,
        shouldDownloadFromNetwork: true,
      });
      if (result.canceled) return;
      await uploadAttachmentAssets(result.assets.map((asset, index) => ({
        uri: asset.uri,
        name: asset.fileName || `Photo ${index + 1}.jpg`,
        mime: asset.mimeType,
        size: asset.fileSize,
      })));
    } catch {
      setError('Could not open your photo library. Please try again.');
    }
  }

  async function addGiphyGif(gif: GiphySearchResult): Promise<boolean> {
    if (!sessionToken || uploading || pendingFiles.length >= maxMessageAttachments) return false;
    setUploading(true);
    setError(null);
    const stagingID = `giphy-${gif.id}-${Date.now()}`;
    setStagingFiles((current) => [...current, {
      id: stagingID,
      name: `${gif.title?.trim() || 'GIPHY'}.gif`,
      mime: 'image/gif',
      uri: gif.previewUrl,
    }]);
    try {
      // Let the server fetch and validate its own trusted GIPHY URL. Avoiding
      // a device download followed by a second upload makes selection feel
      // immediate and avoids holding the animation twice on mobile data.
      const attachment = await api.importGiphy(sessionToken, route.params.threadId, gif);
      setPendingFiles((current) => [...current, attachment].slice(0, maxMessageAttachments));
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      return true;
    } catch (caught) {
      const message = caught instanceof Error ? caught.message : 'Could not import that GIF.';
      setError(message);
      throw new Error(message);
    } finally {
      setStagingFiles((current) => current.filter((file) => file.id !== stagingID));
      setUploading(false);
    }
  }

  function beginEdit(message: ScoutMessage) {
    stopTyping();
    setActionMessage(null);
    setReplyingTo(null);
    setEditingMessage(message);
    setDraft(String(message.text ?? message.content ?? ''));
    setPendingFiles(Array.isArray(message.files) ? message.files : []);
    void Haptics.selectionAsync();
  }

  function cancelEdit() {
    stopTyping();
    setEditingMessage(null);
    setDraft('');
    setPendingFiles([]);
  }

  function beginReply(message: ScoutMessage) {
    setActionMessage(null);
    setEditingMessage(null);
    setReplyingTo(message);
    void Haptics.selectionAsync();
  }

  function cancelReply() {
    setReplyingTo(null);
  }

  function confirmDelete(message: ScoutMessage) {
    setActionMessage(null);
    Alert.alert('Delete message?', 'This removes it for everyone in the channel.', [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Delete',
        style: 'destructive',
        onPress: () => {
          if (!sessionToken) return;
          const generationAtRequest = transcriptGenerationRef.current;
          void api.deleteScoutMessage(sessionToken, route.params.threadId, String(message.id))
            .then((response) => {
              applyTranscriptSnapshot(generationAtRequest, response.thread?.messages ?? response.messages ?? []);
              if (String(editingMessage?.id) === String(message.id)) cancelEdit();
              void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
            })
            .catch((caught) => setError(caught instanceof BonfireApiError ? caught.message : 'Message was not deleted.'));
        },
      },
    ]);
  }

  const toggleReaction = useCallback(async (message: ScoutMessage, emoji: string, active: boolean) => {
    if (!sessionToken) return;
    setActionMessage(null);
    const generationAtRequest = transcriptGenerationRef.current;
    try {
      const response = await api.setScoutMessageReaction(sessionToken, route.params.threadId, String(message.id), emoji, active);
      applyTranscriptSnapshot(generationAtRequest, response.thread?.messages ?? response.messages ?? []);
      void Haptics.selectionAsync();
    } catch (caught) {
      setError(caught instanceof BonfireApiError ? caught.message : 'Reaction was not saved.');
    }
  }, [applyTranscriptSnapshot, route.params.threadId, sessionToken]);

  async function changeNotificationLevel(level: ThreadNotificationLevel) {
    if (!sessionToken || notificationBusy) return;
    setNotificationBusy(true);
    try {
      const response = await api.setThreadNotificationLevel(sessionToken, route.params.threadId, level);
      setNotificationLevel(response.level);
      setNotificationMenuOpen(false);
      void Haptics.selectionAsync();
    } catch (caught) {
      setError(caught instanceof BonfireApiError ? caught.message : 'Notification setting was not saved.');
    } finally {
      setNotificationBusy(false);
    }
  }

  const listening = dictation.state === 'listening';

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
      <View style={styles.header}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Back"
          hitSlop={10}
          onPress={() => navigation.goBack()}
          style={styles.back}
        >
          <SymbolView name="chevron.left" tintColor={colors.text1} size={19} />
        </Pressable>
        <Text style={styles.title} numberOfLines={1}>
          {route.params.title}
        </Text>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={`Notifications: ${notificationLevel}`}
          accessibilityHint="Choose all messages, mentions only, or none"
          onPress={() => setNotificationMenuOpen(true)}
          style={({ pressed }) => [styles.headerAction, pressed && styles.headerActionPressed]}
        >
          <SymbolView
            name={notificationLevel === 'none' ? 'bell.slash.fill' : notificationLevel === 'mentions' ? 'bell.badge.fill' : 'bell.fill'}
            tintColor={colors.text2}
            size={18}
          />
        </Pressable>
      </View>

      <CatchUpSheet
        visible={catchUpOpen}
        catchUp={digest?.catchUp ?? null}
        onClose={() => setCatchUpOpen(false)}
        onOpenMessage={(messageId) => {
          setCatchUpOpen(false);
          scrollToMessage(messageId);
        }}
      />

      <ThreadNotificationMenu
        visible={notificationMenuOpen}
        level={notificationLevel}
        busy={notificationBusy}
        onClose={() => setNotificationMenuOpen(false)}
        onChange={(level) => void changeNotificationLevel(level)}
      />

      <FilePreviewModal
        file={previewFile}
        sessionToken={sessionToken ?? ''}
        onClose={() => setPreviewFile(null)}
      />

      <AttachmentSourceSheet
        visible={attachmentSourceOpen}
        onClose={() => setAttachmentSourceOpen(false)}
        onPhotos={() => void pickPhotos()}
        onFiles={() => void pickFiles()}
        onGifs={() => setGifPickerOpen(true)}
      />

      <GifPickerSheet
        visible={gifPickerOpen}
        sessionToken={sessionToken ?? ''}
        onClose={() => setGifPickerOpen(false)}
        onSelect={addGiphyGif}
      />

      <MessageActionSheet
        visible={Boolean(actionMessage)}
        own={Boolean(actionMessage?.own)}
        snippet={String(actionMessage?.message.text ?? actionMessage?.message.content ?? '')}
        reactions={actionMessage?.message.reactions ?? []}
        onClose={() => setActionMessage(null)}
        onReact={(emoji) => {
          if (!actionMessage) return;
          const current = groupMessageReactions(actionMessage.message.reactions, email)
            .find((reaction) => reaction.emoji === emoji);
          void toggleReaction(actionMessage.message, emoji, !current?.reactedByViewer);
        }}
        onReply={() => { if (actionMessage) beginReply(actionMessage.message); }}
        onEdit={() => { if (actionMessage) beginEdit(actionMessage.message); }}
        onDelete={() => { if (actionMessage) confirmDelete(actionMessage.message); }}
      />

      <KeyboardAvoidingView
        style={styles.fill}
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
        keyboardVerticalOffset={8}
      >
        <View style={styles.fill} {...timestampPan.panHandlers}>
          {loading ? (
            <ActivityIndicator color={colors.accent} style={styles.loading} />
          ) : (
            <FlashList
            ref={listRef}
            data={rows}
            // Stable identity on the message id. Index keys would recycle
            // bubbles onto the wrong messages as the list grows.
            keyExtractor={(row) => String(row.message.id)}
            contentContainerStyle={styles.list}
            keyboardShouldPersistTaps="handled"
            maintainVisibleContentPosition={{ disabled: true, startRenderingFromBottom: true }}
            ListFooterComponent={typingParticipants.length > 0 ? (
              <TypingIndicator participants={typingParticipants} />
            ) : null}
            // FlashList lays out from the latest message immediately; onLoad
            // makes a final non-animated correction after variable-height
            // bubbles have been measured. Explicit message links override
            // this in the focused-message effect above.
            onLoad={() => {
              if (route.params.messageId) return;
              atBottomRef.current = true;
              listRef.current?.scrollToEnd({ animated: false });
              markRead();
            }}
            onScroll={(event) => {
              const { contentOffset, contentSize, layoutMeasurement } = event.nativeEvent;
              atBottomRef.current =
                contentOffset.y + layoutMeasurement.height >= contentSize.height - 48;
              if (atBottomRef.current) markRead();
            }}
            scrollEventThrottle={200}
            onLayout={(event) => {
              listHeightRef.current = event.nativeEvent.layout.height;
            }}
            // A thread short enough to fit on screen has no bottom to scroll
            // to, so onScroll never fires and it would stay unread forever.
            // Fitting entirely on screen IS having read it.
            onContentSizeChange={(_width, height) => {
              if (listHeightRef.current > 0 && height <= listHeightRef.current) {
                atBottomRef.current = true;
                markRead();
              }
            }}
            renderItem={({ item }) => (
              <>
                {item.timelineLabel ? (
                  <View style={styles.timelineMarker}>
                    <Text
                      accessibilityRole="header"
                      style={styles.timelineMarkerLabel}
                    >
                      {item.timelineLabel}
                    </Text>
                  </View>
                ) : null}
                {item.boundary ? (
                  <>
                    <View style={styles.boundary}>
                      <View style={styles.boundaryRule} />
                      <Text style={styles.boundaryLabel}>
                        {unreadBelow} new {unreadBelow === 1 ? 'message' : 'messages'}
                      </Text>
                      <View style={styles.boundaryRule} />
                    </View>
                    {/* The catch-up lives ON the boundary, which is the exact
                        moment the question "what did I miss?" is asked. */}
                    {digest?.catchUp?.bullets?.length ? (
                      <Pressable
                        accessibilityRole="button"
                        accessibilityLabel="Catch me up"
                        onPress={() => setCatchUpOpen(true)}
                        style={({ pressed }) => [styles.catchUp, pressed && styles.pressedRow]}
                      >
                        <SymbolView name="text.line.first.and.arrowtriangle.forward" tintColor={colors.emberText} size={14} />
                        <Text style={styles.catchUpText}>Catch me up</Text>
                      </Pressable>
                    ) : null}
                  </>
                ) : null}
                <MessageBubble
                  message={item.message}
                  own={item.own}
                  showAuthor={item.showAuthor}
                  showAvatar={item.showAvatar}
                  avatarDataURL={item.avatarDataURL}
                  sessionToken={sessionToken ?? ''}
                  viewerEmail={email}
                  timestampReveal={timestampReveal}
                  onOpenSource={scrollToMessage}
                  onOpenReplySource={scrollToMessage}
                  onOpenAttachment={setPreviewFile}
                  onLongPress={(message, own) => {
                    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium);
                    setActionMessage({ message, own });
                  }}
                  onToggleReaction={(message, emoji, active) => void toggleReaction(message, emoji, active)}
                />
              </>
            )}
            />
          )}
        </View>

        {error ? <Text style={styles.error}>{error}</Text> : null}
        {dictation.error ? (
          <View style={styles.dictationError}>
            <Text style={styles.error}>{dictation.error}</Text>
            <Pressable onPress={dictation.retry} accessibilityRole="button">
              <Text style={styles.retry}>Retry</Text>
            </Pressable>
          </View>
        ) : null}

        {editingMessage ? (
          <View style={styles.editingBar}>
            <View style={styles.editingCopy}>
              <Text style={styles.editingTitle}>Editing message</Text>
              <Text style={styles.editingHint} numberOfLines={1}>Send replaces it in the same spot</Text>
            </View>
            <Pressable accessibilityRole="button" accessibilityLabel="Cancel editing" onPress={cancelEdit} style={({ pressed }) => [styles.editingCancel, pressed && styles.headerActionPressed]}>
              <SymbolView name="xmark" tintColor={colors.text2} size={14} />
            </Pressable>
          </View>
        ) : null}

        {replyingTo ? (
          <View style={styles.replyingBar}>
            <View style={styles.replyingMark} />
            <View style={styles.editingCopy}>
              <Text style={styles.replyingTitle}>
                Replying to {String(replyingTo.authorName ?? (replyingTo.role === 'scout' || replyingTo.role === 'assistant' ? 'Scout' : 'message'))}
              </Text>
              <Text style={styles.editingHint} numberOfLines={1}>{String(replyingTo.text ?? replyingTo.content ?? 'Attachment')}</Text>
            </View>
            <Pressable accessibilityRole="button" accessibilityLabel="Cancel reply" onPress={cancelReply} style={({ pressed }) => [styles.editingCancel, pressed && styles.headerActionPressed]}>
              <SymbolView name="xmark" tintColor={colors.text2} size={14} />
            </Pressable>
          </View>
        ) : null}

        <Glass radius={radius.xl} style={styles.composer}>
          {pendingFiles.length > 0 || stagingFiles.length > 0 || uploading ? (
            <View style={styles.pendingFiles}>
              {stagingFiles.map((file) => (
                <View key={file.id} style={[styles.pendingFile, styles.stagingFile]}>
                  {file.mime.startsWith('image/') && file.uri ? (
                    <Image source={{ uri: file.uri }} contentFit="cover" cachePolicy="memory-disk" style={styles.stagingThumb} />
                  ) : null}
                  <Text style={styles.pendingFileText} numberOfLines={1}>{file.name}</Text>
                  <ActivityIndicator color={colors.text2} size="small" />
                </View>
              ))}
              {pendingFiles.map((file) => (
                <View key={`${file.ref}-${file.name}`} style={styles.pendingFile}>
                  <SymbolView name={file.mime.startsWith('image/') ? 'photo' : 'doc.richtext'} tintColor={colors.text2} size={14} />
                  <Text style={styles.pendingFileText} numberOfLines={1}>{file.name}</Text>
                  <Pressable
                    accessibilityRole="button"
                    accessibilityLabel={`Remove ${file.name}`}
                    onPress={() => setPendingFiles((current) => current.filter((candidate) => candidate.ref !== file.ref))}
                    style={({ pressed }) => [styles.pendingRemove, pressed && styles.headerActionPressed]}
                  >
                    <SymbolView name="xmark" tintColor={colors.text3} size={10} />
                  </Pressable>
                </View>
              ))}
            </View>
          ) : null}
          {listening || dictation.state === 'held' || dictation.state === 'transcribing' ? (
            <View style={styles.listening}>
              <Waveform trace={dictation.trace} listening height={30} scale={0.7} />
              <Text style={styles.listeningHint}>
                {listening ? 'Release to stop · slide up to cancel' : dictation.state === 'held' ? 'Ready to transcribe' : 'Transcribing'}
              </Text>
            </View>
          ) : (
            <MentionComposerInput
              placeholder={
                route.params.title.length > 22
                  ? `Message ${route.params.title.slice(0, 21).trimEnd()}…`
                  : `Message ${route.params.title}`
              }
              value={draft}
              onChangeText={changeDraft}
              onBlur={() => stopTyping()}
              candidates={participants}
              editable
            />
          )}

          <View style={styles.composerActions}>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Add attachment"
              accessibilityState={{ disabled: uploading || pendingFiles.length >= maxMessageAttachments }}
              disabled={uploading || pendingFiles.length >= maxMessageAttachments}
              onPress={() => setAttachmentSourceOpen(true)}
              style={({ pressed }) => [styles.mic, pressed && styles.micPressed, (uploading || pendingFiles.length >= maxMessageAttachments) && styles.sendDim]}
            >
              <SymbolView name="plus" tintColor={colors.text2} size={20} />
            </Pressable>
            <Pressable
              accessibilityRole="button"
			  accessibilityLabel={listening ? 'Stop dictation' : 'Hold to dictate'}
			  accessibilityHint="Touch and hold to dictate. Release to hold the clip, then use Send to transcribe."
			  disabled={dictation.state !== 'idle'}
			  onPressIn={(event) => {
				dictationTouchActiveRef.current = true;
				dictationTouchStartYRef.current = event.nativeEvent.pageY;
				void beginDictation();
			  }}
			  onPressMove={(event) => {
				const startY = dictationTouchStartYRef.current;
				if (startY !== null && event.nativeEvent.pageY < startY - 44) dictation.cancel();
			  }}
			  onPressOut={() => {
				dictationTouchActiveRef.current = false;
				dictationTouchStartYRef.current = null;
				void dictation.stop().finally(() => {
					const lease = dictationFocusLeaseRef.current;
					dictationFocusLeaseRef.current = null;
					void lease?.release('completed');
				});
			  }}
              style={({ pressed }) => [styles.mic, pressed && styles.micPressed, dictation.state !== 'idle' && styles.sendDim]}
            >
              {dictation.state === 'transcribing' ? (
                <ActivityIndicator color={colors.ember} />
              ) : (
                <SymbolView
                  name={listening ? 'waveform' : 'mic.fill'}
                  tintColor={listening ? colors.ember : colors.text2}
                  size={20}
                />
              )}
            </Pressable>

            {dictation.state === 'held' || dictation.state === 'error' ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Delete dictated clip"
                onPress={dictation.delete}
                style={({ pressed }) => [styles.mic, pressed && styles.micPressed]}
              >
                <SymbolView name="xmark" tintColor={colors.text2} size={18} />
              </Pressable>
            ) : null}

            <Pressable
              accessibilityRole="button"
              accessibilityLabel={dictation.state === 'held' || dictation.state === 'error' ? 'Transcribe and send dictated clip' : 'Send'}
              disabled={dictation.state === 'transcribing' || (dictation.state !== 'held' && dictation.state !== 'error' && (!draft.trim() && pendingFiles.length === 0 || sending || uploading))}
              onPress={() => {
                if (dictation.state === 'held' || dictation.state === 'error') dictation.send();
                else void send();
              }}
              style={({ pressed }) => [
                styles.send,
                (dictation.state === 'transcribing' || (dictation.state !== 'held' && dictation.state !== 'error' && ((!draft.trim() && pendingFiles.length === 0) || sending || uploading || pressed))) && styles.sendDim,
              ]}
            >
              {sending ? (
                <ActivityIndicator color={colors.onAccent} />
              ) : (
                <SymbolView name="arrow.up" tintColor={colors.onAccent} size={18} />
              )}
            </Pressable>
          </View>
        </Glass>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  fill: { flex: 1 },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingHorizontal: space[4],
    paddingBottom: space[3],
  },
  back: {
    width: 42,
    height: 42,
    borderRadius: 15,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  headerAction: {
    width: hitMin,
    height: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.md,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  headerActionPressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  title: {
    ...type.title2,
    color: colors.text1,
    flex: 1,
  },
	mute: {
		width: 42,
		height: 42,
		borderRadius: 15,
		alignItems: 'center',
		justifyContent: 'center',
		backgroundColor: colors.surface1,
		borderWidth: StyleSheet.hairlineWidth,
		borderColor: colors.line1,
	},
  loading: { paddingVertical: space[10] },
  timelineMarker: {
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: space[4],
    paddingTop: space[4],
    paddingBottom: space[2],
  },
  timelineMarkerLabel: {
    ...type.captionMedium,
    color: colors.text3,
    fontVariant: ['tabular-nums'],
    textAlign: 'center',
  },
  boundary: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingHorizontal: space[4],
    paddingTop: space[4],
    paddingBottom: space[2],
  },
  boundaryRule: {
    flex: 1,
    height: StyleSheet.hairlineWidth,
    backgroundColor: colors.ember,
  },
  boundaryLabel: {
    fontSize: 11,
    fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600',
    letterSpacing: 0.4,
    color: colors.emberText,
    textTransform: 'uppercase',
  },
  list: {
    paddingTop: space[2],
    // Clears the glass composer floating above the list bottom, so the last
    // message never sits tucked under it.
    paddingBottom: space[5],
  },
  composer: {
    marginHorizontal: space[4],
    marginBottom: space[2],
    paddingHorizontal: space[4],
    paddingTop: space[3],
    paddingBottom: space[2],
    gap: space[2],
  },
  editingBar: {
    minHeight: 48,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    marginHorizontal: space[5],
    paddingHorizontal: space[3],
    borderLeftWidth: 3,
    borderLeftColor: colors.info,
  },
  editingCopy: { flex: 1 },
  editingTitle: { ...type.captionMedium, color: colors.info },
  editingHint: { ...type.caption, color: colors.text2 },
  editingCancel: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  replyingBar: {
    minHeight: 52,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    marginHorizontal: space[5],
    paddingHorizontal: space[3],
  },
  replyingMark: { width: 3, alignSelf: 'stretch', marginVertical: 8, borderRadius: radius.full, backgroundColor: colors.info },
  replyingTitle: { ...type.captionMedium, color: colors.info },
  pendingFiles: { flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 6 },
  pendingFile: { maxWidth: '100%', minHeight: 32, flexDirection: 'row', alignItems: 'center', gap: 6, paddingLeft: 9, paddingRight: 3, borderRadius: radius.full, backgroundColor: colors.surface3 },
  stagingFile: { paddingLeft: 3, paddingRight: 8, opacity: 0.86 },
  stagingThumb: { width: 26, height: 26, borderRadius: radius.full, backgroundColor: colors.surface2 },
  pendingFileText: { ...type.caption, maxWidth: 190, color: colors.text1 },
  pendingRemove: { width: 28, height: 28, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  listening: {
    minHeight: 40,
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[2],
  },
  listeningHint: {
    ...type.caption,
    color: colors.emberText,
  },
  composerActions: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'flex-end',
    gap: space[2],
  },
  mic: {
    width: hitMin,
    height: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
  },
  micPressed: { backgroundColor: colors.emberSoft },
  send: {
    width: hitMin,
    height: hitMin,
    borderRadius: radius.full,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.accent,
  },
  sendDim: { opacity: 0.4 },
  error: {
    ...type.bodySm,
    color: colors.danger,
    paddingHorizontal: space[5],
    paddingBottom: space[2],
  },
  dictationError: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingHorizontal: space[5],
  },
  retry: { ...type.button, color: colors.ember },
  catchUp: {
    flexDirection: 'row',
    alignItems: 'center',
    alignSelf: 'center',
    gap: 6,
    paddingHorizontal: space[4],
    paddingVertical: 7,
    marginBottom: space[3],
    borderRadius: radius.full,
    backgroundColor: colors.emberSoft,
  },
  catchUpText: {
    ...type.button,
    color: colors.emberText,
  },
  pressedRow: { opacity: 0.6 },
});
