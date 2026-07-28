import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
	Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import * as SecureStore from 'expo-secure-store';
import { SafeAreaView } from 'react-native-safe-area-context';
import * as Haptics from 'expo-haptics';
import { SymbolView } from 'expo-symbols';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { useFocusEffect } from '@react-navigation/native';
import * as Linking from 'expo-linking';
import { FlashList, type FlashListRef } from '@shopify/flash-list';
import { api, BonfireApiError } from '../api/client';
import type { ScoutMessage, ThreadDigestResponse } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { MessageBubble } from '../messaging/MessageBubble';
import { firstUnreadIndex } from '../messaging/unreadBoundary';
import { CatchUpSheet } from '../messaging/CatchUpSheet';
import { DepositRail } from '../messaging/DepositRail';
import { shareOrSaveRemoteFile } from '../files/fileActions';

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
	const [muted, setMuted] = useState(false);
	const [muting, setMuting] = useState(false);
	const dictationDisclosureAcceptedRef = useRef(false);
	const dictationDisclosureOpenRef = useRef(false);
	const dictationTouchStartYRef = useRef<number | null>(null);
	const dictationTouchActiveRef = useRef(false);

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
	  setMuted(Boolean(response.muted));
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
		setMessages(next);
		if (shouldFollow) requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: true }));
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
        const own =
          String(message.role ?? '').toLowerCase() === 'user' &&
          (!message.authorEmail || String(message.authorEmail).toLowerCase() === email);
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
    [boundary, email, messages],
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
    if (!sessionToken || !text || sending) return;
    setSending(true);
    setError(null);
    try {
      const response = await api.sendScoutMessage(sessionToken, route.params.threadId, text);
      setDraft('');
      setMessages(response.thread?.messages ?? response.messages ?? []);
	  atBottomRef.current = true;
	  requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: true }));
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Message did not send.');
    } finally {
      setSending(false);
    }
  }

  const listening = dictation.state === 'listening';

	async function toggleMuted() {
		if (!sessionToken || muting) return;
		const next = !muted;
		setMuting(true);
		setMuted(next);
		try {
			const response = await api.muteThread(sessionToken, route.params.threadId, next);
			setMuted(response.muted);
			void Haptics.selectionAsync();
		} catch (err) {
			setMuted(!next);
			setError(err instanceof BonfireApiError ? err.message : 'Could not update notifications.');
		} finally {
			setMuting(false);
		}
	}

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
			accessibilityRole="switch"
			accessibilityLabel="Mute this thread"
			accessibilityState={{ checked: muted, disabled: muting }}
			disabled={muting}
			onPress={() => void toggleMuted()}
			style={({ pressed }) => [styles.mute, pressed && styles.pressedRow]}
		>
			<SymbolView name={muted ? 'bell.slash.fill' : 'bell.fill'} tintColor={muted ? colors.text3 : colors.text2} size={17} />
		</Pressable>
      </View>

      {/* What this conversation produced, in the thread that produced it. */}
      <DepositRail
        deposits={digest?.deposits ?? null}
        onOpenMessage={scrollToMessage}
        onOpenLink={(url) => void Linking.openURL(url).catch(() => {})}
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

      <KeyboardAvoidingView
        style={styles.fill}
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
        keyboardVerticalOffset={8}
      >
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
                  onOpenSource={scrollToMessage}
				  onOpenAttachment={(file) => {
					if (!sessionToken) return;
					void shareOrSaveRemoteFile(sessionToken, file).catch((err) => {
						setError(err instanceof Error ? err.message : 'Could not open that attachment.');
					});
				  }}
                />
              </>
            )}
          />
        )}

        {error ? <Text style={styles.error}>{error}</Text> : null}
        {dictation.error ? (
          <View style={styles.dictationError}>
            <Text style={styles.error}>{dictation.error}</Text>
            <Pressable onPress={dictation.retry} accessibilityRole="button">
              <Text style={styles.retry}>Retry</Text>
            </Pressable>
          </View>
        ) : null}

        <Glass radius={radius.xl} style={styles.composer}>
          {listening ? (
            <View style={styles.listening}>
              <Waveform trace={dictation.trace} listening height={30} scale={0.7} />
              <Text style={styles.listeningHint}>Release to transcribe · slide up to cancel</Text>
            </View>
          ) : (
            <TextInput
              style={styles.input}
              // Channel names run long ("#Open-source model delegation
              // benchmark"), and a placeholder that wraps to two lines doubles
              // the composer's height before a single character is typed.
              placeholder={
                route.params.title.length > 22
                  ? `Message ${route.params.title.slice(0, 21).trimEnd()}…`
                  : `Message ${route.params.title}`
              }
              placeholderTextColor={colors.text3}
              value={draft}
              onChangeText={setDraft}
              multiline
              editable={dictation.state !== 'transcribing'}
            />
          )}

          <View style={styles.composerActions}>
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
              disabled={!draft.trim() || sending}
              onPress={() => void send()}
              style={({ pressed }) => [
                styles.send,
                (!draft.trim() || sending || pressed) && styles.sendDim,
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
  input: {
    minHeight: 40,
    maxHeight: 132,
    ...type.body,
    color: colors.text1,
    textAlignVertical: 'top',
  },
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
