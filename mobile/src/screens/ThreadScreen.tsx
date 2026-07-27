import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import * as Haptics from 'expo-haptics';
import { SymbolView } from 'expo-symbols';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type { ScoutMessage } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { MessageBubble } from '../messaging/MessageBubble';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { Glass } from '../theme/glass';
import { Waveform } from '../components/Waveform';
import { useDictation } from '../voice/useDictation';
import type { RootStackParamList } from '../navigation/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

type Props = NativeStackScreenProps<RootStackParamList, 'Thread'>;

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
  const scrollRef = useRef<ScrollView>(null);

  const dictation = useDictation({
    context: 'chat',
    threadId: route.params.threadId,
    // A transcript lands in the DRAFT, never straight into the thread. Holding
    // produces text; posting stays an explicit act (§13.5).
    onTranscript: ({ text }) =>
      setDraft((previous) => (previous ? `${previous.trimEnd()} ${text}` : text)),
  });

  const load = useCallback(async () => {
    if (!sessionToken) return;
    try {
      const response = await api.scoutThread(sessionToken, route.params.threadId);
      setMessages(response.thread?.messages ?? response.messages ?? []);
      setError(null);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not load this thread.');
    } finally {
      setLoading(false);
    }
  }, [route.params.threadId, sessionToken]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (office.event === 'chat_thread') void load();
  }, [load, office.event, office.version]);

  const email = user?.email?.trim().toLowerCase() ?? '';
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
        return { message, own, showAuthor };
      }),
    [email, messages],
  );

  async function send() {
    const text = draft.trim();
    if (!sessionToken || !text || sending) return;
    setSending(true);
    setError(null);
    try {
      const response = await api.sendScoutMessage(sessionToken, route.params.threadId, text);
      setDraft('');
      setMessages(response.thread?.messages ?? response.messages ?? []);
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Message did not send.');
    } finally {
      setSending(false);
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
      </View>

      <KeyboardAvoidingView
        style={styles.fill}
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
        keyboardVerticalOffset={8}
      >
        {loading ? (
          <ActivityIndicator color={colors.accent} style={styles.loading} />
        ) : (
          <ScrollView
            ref={scrollRef}
            contentContainerStyle={styles.list}
            keyboardShouldPersistTaps="handled"
            onContentSizeChange={() => scrollRef.current?.scrollToEnd({ animated: true })}
          >
            {rows.map(({ message, own, showAuthor }) => (
              <MessageBubble
                key={String(message.id)}
                message={message}
                own={own}
                showAuthor={showAuthor}
              />
            ))}
          </ScrollView>
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
              accessibilityHint="Touch and hold to dictate a message."
              onPressIn={() => void dictation.start()}
              onPressOut={() => void dictation.stop()}
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
  loading: { paddingVertical: space[10] },
  list: {
    paddingTop: space[2],
    paddingBottom: space[4],
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
    color: colors.ember,
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
});
