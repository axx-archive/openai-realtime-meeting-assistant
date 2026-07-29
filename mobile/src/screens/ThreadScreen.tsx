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
import { File, Paths } from 'expo-file-system';
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
import { CatchUpSheet } from '../messaging/CatchUpSheet';
import { DepositRail } from '../messaging/DepositRail';
import { MessageActionSheet } from '../messaging/MessageActionSheet';
import { MentionComposerInput } from '../messaging/MentionComposerInput';
import { AttachmentSourceSheet } from '../messaging/AttachmentSourceSheet';
import { GifPickerSheet } from '../messaging/GifPickerSheet';
import {
  attachmentBatchMessage,
  maxMessageAttachments,
  prepareAttachmentBatch,
  type AttachmentAssetInput,
} from '../messaging/attachmentSources';
import { ThreadNotificationMenu, type ThreadNotificationLevel } from '../messaging/ThreadNotificationMenu';
import { groupMessageReactions, isOwnMessageForViewer } from '../messaging/messagePresentation';
import { FilePreviewModal } from '../components/FilePreviewModal';

type ThreadRow = {
  message: ScoutMessage;
  own: boolean;
  showAuthor: boolean;
  boundary: boolean;
};
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { Glass } from '../theme/glass';
import { Waveform } from '../components/Waveform';
import { useDictation } from '../voice/useDictation';
import type { RootStackParamList } from '../navigation/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

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
  const [messages, setMessages] = useState<ScoutMessage[]>([]);
  const [draft, setDraft] = useState('');
  const [loading, setLoading] = useState(true);
  const [sending, setSending] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [pendingFiles, setPendingFiles] = useState<ScoutFileAttachment[]>([]);
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
  const [error, setError] = useState<string | null>(null);
  // null means "not yet loaded" — distinct from "" which means never read.
  const [readAt, setReadAt] = useState<string | null>(null);
  const listRef = useRef<FlashListRef<ThreadRow>>(null);
  // Starts FALSE. Initialising it true meant opening a thread with 80 unread
  // and immediately backing out marked all 80 read without the user scrolling
  // a pixel — exactly the "never mark read on open" failure this is here to
  // prevent. It is set true only by reaching the bottom, or by the content
  // being short enough that there is no bottom to reach (below).
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
  const timestampReveal = useRef(new Animated.Value(0)).current;
  const timestampPan = useMemo(() => PanResponder.create({
    onMoveShouldSetPanResponder: (_event, gesture) => (
      gesture.dx < -8 && Math.abs(gesture.dx) > Math.abs(gesture.dy) * 1.35
    ),
    onPanResponderMove: (_event, gesture) => {
      timestampReveal.setValue(Math.max(0, Math.min(1, -gesture.dx / 68)));
    },
    onPanResponderRelease: () => {
      Animated.spring(timestampReveal, { toValue: 0, damping: 18, stiffness: 240, mass: 0.8, useNativeDriver: true }).start();
    },
    onPanResponderTerminate: () => {
      Animated.spring(timestampReveal, { toValue: 0, damping: 18, stiffness: 240, mass: 0.8, useNativeDriver: true }).start();
    },
  }), [timestampReveal]);

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
    // A transcript lands in the DRAFT, never straight into the thread. Holding
    // produces text; posting stays an explicit act (§13.5).
    onTranscript: ({ text }) =>
      setDraft((previous) => (previous ? `${previous.trimEnd()} ${text}` : text)),
  });

	const beginDictation = useCallback(async () => {
		if (dictationDisclosureAcceptedRef.current) {
			if (dictationTouchActiveRef.current) await dictation.start();
			return;
		}
		const stored = await SecureStore.getItemAsync(DICTATION_DISCLOSURE_KEY).catch(() => null);
		if (stored === 'accepted') {
			dictationDisclosureAcceptedRef.current = true;
			if (dictationTouchActiveRef.current) await dictation.start();
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

  const load = useCallback(async () => {
    if (!sessionToken) return;
    try {
      const response = await api.scoutThread(sessionToken, route.params.threadId);
      const next = response.thread?.messages ?? response.messages ?? [];
      setMessages(next);
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
  }, [route.params.threadId, sessionToken]);

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

  // Catch-up and deposits arrive after first paint — the thread must render
  // immediately, and neither is needed to read a message.
  useEffect(() => {
    if (!sessionToken) return;
    let cancelled = false;
    void api
      .threadDigest(sessionToken, route.params.threadId)
      .then((response) => {
        if (!cancelled) setDigest(response);
      })
      .catch(() => {
        // Absent digest simply means no rail and no catch-up affordance. The
        // thread itself is unaffected.
      });
    return () => {
      cancelled = true;
    };
  }, [route.params.threadId, sessionToken]);

  // Reconcile with the authoritative thread on invalidation. Replacement is
  // necessary for cross-device edits and deletes; append-only merging leaves
  // removed messages visible forever.
  useEffect(() => {
    if (office.event !== 'chat_thread' || !sessionToken) return;
    let cancelled = false;
    void (async () => {
      try {
        const response = await api.scoutThread(sessionToken, route.params.threadId);
        if (cancelled) return;
        const next = response.thread?.messages ?? response.messages ?? [];
        const shouldFollow = atBottomRef.current;
        // The fetched snapshot is authoritative. This reconciles same-id edits
        // and reactions and removes deleted messages without changing IDs or
        // timeline order.
        setMessages(next);
        // A background reconciliation should never make the conversation
        // visibly lurch. The user is already at the bottom, so pin it without
        // replaying a scroll animation.
        if (shouldFollow) requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: false }));
      } catch {
        // A dropped refresh leaves what is already on screen. The next event
        // or a manual reopen recovers it.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [office.event, office.version, route.params.threadId, sessionToken]);

	const wasConnectedRef = useRef(office.connected);
	useEffect(() => {
		const reconnected = !wasConnectedRef.current && office.connected;
		wasConnectedRef.current = office.connected;
		if (reconnected) void load();
	}, [load, office.connected]);

  const email = user?.email?.trim().toLowerCase() ?? '';

  // Where the "N new messages" divider goes. -1 means everything is read and
  // no divider renders.
  const boundary = useMemo(
    () => firstUnreadIndex(messages, readAt ?? undefined, email),
    [email, messages, readAt],
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
        const showAuthor =
          !previous ||
          previous.role !== message.role ||
          previous.authorEmail !== message.authorEmail;
        return {
          message,
          own,
          showAuthor,
          // The divider is part of the row above the first unread message
          // rather than a separate list item, so it cannot desync from it.
          boundary: index === boundary,
        };
      }),
    [boundary, email, messages, threadOwnerEmail, threadVisibility],
  );

  const unreadBelow = boundary >= 0 ? messages.length - boundary : 0;

  // Advance the marker on GENUINE reads only — never on open. Marking eighty
  // messages read because the screen appeared is how people lose messages they
  // never saw.
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

  async function send() {
    const text = draft.trim();
    if (!sessionToken || (!text && pendingFiles.length === 0) || sending || uploading) return;
    setSending(true);
    setError(null);
    try {
      const response = editingMessage
        ? await api.updateScoutMessage(sessionToken, route.params.threadId, String(editingMessage.id), text, pendingFiles)
        : await api.sendScoutMessage(sessionToken, route.params.threadId, text, pendingFiles, String(replyingTo?.id ?? ''));
      setDraft('');
      setPendingFiles([]);
      setEditingMessage(null);
      setReplyingTo(null);
      setMessages(response.thread?.messages ?? response.messages ?? []);
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
    const uploaded: ScoutFileAttachment[] = [];
    const failures: string[] = [];
    try {
      for (const asset of batch.accepted) {
        try {
          uploaded.push(await api.uploadScoutAttachment(sessionToken, asset));
        } catch (caught) {
          failures.push(caught instanceof Error ? caught.message : `${asset.name} could not be attached.`);
        }
      }
      if (uploaded.length > 0) {
        setPendingFiles((current) => [...current, ...uploaded].slice(0, maxMessageAttachments));
        void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      }
      const message = [issues, ...failures].filter(Boolean).join(' ');
      setError(message || null);
      return uploaded.length > 0 && failures.length === 0;
    } finally {
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
        quality: 1,
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
    if (uploading || pendingFiles.length >= maxMessageAttachments) return false;
    const safeID = gif.id.replace(/[^a-zA-Z0-9_-]/g, '').slice(0, 80) || 'gif';
    const destination = new File(Paths.cache, `bonfire-giphy-${safeID}.gif`);
    try {
      const downloaded = await File.downloadFileAsync(gif.mediaUrl, destination, { idempotent: true });
      return await uploadAttachmentAssets([{
        uri: downloaded.uri,
        name: `${gif.title?.trim() || 'GIPHY'}.gif`,
        mime: 'image/gif',
        size: downloaded.size,
      }]);
    } catch (caught) {
      const message = caught instanceof Error ? caught.message : 'Could not download that GIF.';
      setError(message);
      throw new Error(message);
    }
  }

  function beginEdit(message: ScoutMessage) {
    setActionMessage(null);
    setReplyingTo(null);
    setEditingMessage(message);
    setDraft(String(message.text ?? message.content ?? ''));
    setPendingFiles(Array.isArray(message.files) ? message.files : []);
    void Haptics.selectionAsync();
  }

  function cancelEdit() {
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
          void api.deleteScoutMessage(sessionToken, route.params.threadId, String(message.id))
            .then((response) => {
              setMessages(response.thread?.messages ?? response.messages ?? []);
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
    try {
      const response = await api.setScoutMessageReaction(sessionToken, route.params.threadId, String(message.id), emoji, active);
      setMessages(response.thread?.messages ?? response.messages ?? []);
      void Haptics.selectionAsync();
    } catch (caught) {
      setError(caught instanceof BonfireApiError ? caught.message : 'Reaction was not saved.');
    }
  }, [route.params.threadId, sessionToken]);

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

      {/* What this conversation produced, in the thread that produced it. */}
      <DepositRail
        deposits={digest?.deposits ?? null}
        onOpenMessage={scrollToMessage}
      />

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
            maintainVisibleContentPosition={{ disabled: true }}
            // Land where you stopped reading, not at the bottom. iMessage's
            // bottom-landing is right for a five-message thread and wrong for
            // an eighty-message one.
            initialScrollIndex={boundary >= 0 ? boundary : undefined}
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
          {pendingFiles.length > 0 || uploading ? (
            <View style={styles.pendingFiles}>
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
              {uploading ? <ActivityIndicator color={colors.text2} size="small" /> : null}
            </View>
          ) : null}
          {listening ? (
            <View style={styles.listening}>
              <Waveform trace={dictation.trace} listening height={30} scale={0.7} />
              <Text style={styles.listeningHint}>Release to transcribe · slide up to cancel</Text>
            </View>
          ) : (
            <MentionComposerInput
              placeholder={
                route.params.title.length > 22
                  ? `Message ${route.params.title.slice(0, 21).trimEnd()}…`
                  : `Message ${route.params.title}`
              }
              value={draft}
              onChangeText={setDraft}
              candidates={participants}
              editable={dictation.state !== 'transcribing'}
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
              accessibilityLabel={listening ? 'Listening' : 'Hold to dictate'}
			  accessibilityHint="Touch and hold to dictate. While recording, slide your finger up to cancel."
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
				void dictation.stop();
			  }}
              style={({ pressed }) => [styles.mic, pressed && styles.micPressed]}
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

            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Send"
              disabled={(!draft.trim() && pendingFiles.length === 0) || sending || uploading}
              onPress={() => void send()}
              style={({ pressed }) => [
                styles.send,
                ((!draft.trim() && pendingFiles.length === 0) || sending || uploading || pressed) && styles.sendDim,
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
    fontWeight: '600',
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
