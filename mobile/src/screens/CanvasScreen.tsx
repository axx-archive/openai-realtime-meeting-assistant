import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  useColorScheme,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { MomentumGlyph } from '../components/BrandMark';
import { Dock } from '../components/Dock';
import { Waveform } from '../components/Waveform';
import { useLiveLine } from '../canvas/useLiveLine';
import { useDictation } from '../voice/useDictation';
import { useScoutConversation } from '../voice/useScoutConversation';
import { Glass } from '../theme/glass';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type CanvasNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Canvas — design §4 and §9.
 *
 * The root of the app is a microphone, not a dashboard. "You don't operate it,
 * you just talk" only survives contact with a home screen that has almost
 * nothing to operate, so this page holds exactly three text elements — a mark, a
 * greeting, and one live line — plus the waveform and the Dock.
 *
 * Nothing here blocks first paint: the waveform renders before any network call
 * resolves, because time-to-first-word is the only performance metric that
 * matters (§C3).
 */

/**
 * Honorifics are not names. Accounts here carry values like "Dr. Dana Reed", and
 * taking the first token greets someone as "Dr." — which reads as a bug to the
 * one person guaranteed to see it every morning.
 */
const HONORIFICS = new Set(['dr', 'mr', 'mrs', 'ms', 'mx', 'prof', 'sir', 'rev', 'capt']);

function firstName(name: string | undefined): string | undefined {
  const tokens = name?.trim().split(/\s+/).filter(Boolean) ?? [];
  for (const token of tokens) {
    const bare = token.replace(/\./g, '').toLowerCase();
    if (!bare || HONORIFICS.has(bare)) continue;
    return token;
  }
  return undefined;
}

function greeting(name: string | undefined): string {
  const hour = new Date().getHours();
  const part = hour < 12 ? 'morning' : hour < 18 ? 'afternoon' : 'evening';
  const first = firstName(name);
  return first ? `Good ${part}, ${first}.` : `Good ${part}.`;
}

export function CanvasScreen() {
  const { user } = useAuth();
  const navigation = useNavigation<CanvasNav>();
  // `colors.text3` is a DynamicColorIOS pair, which expo-image's tintColor
  // rejects — so the glyph's tint is resolved here instead.
  const dark = useColorScheme() === 'dark';
  const markTint = dark ? 'rgba(247, 247, 249, 0.42)' : 'rgba(14, 14, 16, 0.38)';
  const live = useLiveLine();
  const conversation = useScoutConversation();
  const [typing, setTyping] = useState(false);
  const [draft, setDraft] = useState('');
  const inputRef = useRef<TextInput>(null);

  // Hold-to-dictate. On the canvas a transcript is a question, so it feeds the
  // conversation loop rather than a text field.
  const dictation = useDictation({
    context: 'chat',
    threadId: conversation.threadId ?? undefined,
    onTranscript: ({ text }) => {
      void conversation.ask(text);
    },
  });

  const listening = dictation.state === 'listening';

  const handleTap = useCallback(() => {
    // Three cases, and the middle one is what makes this a LOOP rather than a
    // one-shot: ending a turn does NOT end the conversation.
    if (listening) {
      // Turn over — transcribe and ask. The loop stays open, and the re-arm
      // effect below puts the mic back up once Scout has answered.
      void dictation.stop();
      return;
    }
    if (conversation.open) {
      conversation.end();
      return;
    }
    conversation.start();
    void dictation.start();
  }, [conversation, dictation, listening]);

  // The re-arm. This is the difference between "tap to dictate a question" and
  // "tap to have a conversation": once the answer lands, the mic comes back up
  // on its own so you can just keep talking (§5).
  const answered = Boolean(conversation.turn?.answer) && !conversation.thinking;
  useEffect(() => {
    if (!conversation.open || !answered) return;
    if (dictation.state !== 'idle' || typing) return;
    void dictation.start();
    // `dictation.start` is stable per state; re-running on every render would
    // restart the recorder mid-turn.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [answered, conversation.open, dictation.state, typing]);

  const handleKeyboard = useCallback(() => {
    setTyping(true);
    conversation.start();
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [conversation]);

  const submitTyped = useCallback(() => {
    const text = draft.trim();
    if (!text) return;
    setDraft('');
    Keyboard.dismiss();
    setTyping(false);
    void conversation.ask(text);
  }, [conversation, draft]);

  const openDeck = useCallback(() => navigation.navigate('Deck', {}), [navigation]);

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
      <KeyboardAvoidingView
        style={styles.fill}
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      >
        <ScrollView
          contentContainerStyle={styles.body}
          keyboardShouldPersistTaps="handled"
          showsVerticalScrollIndicator={false}
        >
          {/* The web's `.office-launch__mark`: the momentum glyph beside a
              small label, in text-3. This is the shell's only brand moment —
              removing the tab bar took away the glyph's old home. */}
          <View style={styles.mark} accessibilityRole="header" accessibilityLabel="Scout">
            <MomentumGlyph size={16} color={markTint} />
            <Text style={styles.markLabel}>SCOUT</Text>
          </View>

          {/* The one big tappable area, matching the web canon. */}
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={listening ? 'Listening' : 'Talk to Scout'}
            onPress={handleTap}
            style={styles.wave}
          >
            <Waveform amplitude={dictation.amplitude} listening={listening} height={72} />
          </Pressable>

          <Text style={styles.greeting}>{greeting(user?.name)}</Text>

          {live.text ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={live.text}
              accessibilityHint="Opens threads."
              onPress={openDeck}
              style={({ pressed }) => [styles.liveLine, pressed && styles.pressed]}
            >
              <Text style={[styles.liveText, live.mentioned && styles.liveMention]}>
                {live.text}
              </Text>
            </Pressable>
          ) : null}

          {/* Scout's turn. Text-primary always — we never build an interaction
              whose output exists only as audio (§9.5). */}
          {conversation.turn ? (
            <View style={styles.turn}>
              <Text style={styles.question}>{conversation.turn.question}</Text>
              {conversation.thinking ? (
                <ActivityIndicator color={colors.ember} style={styles.thinking} />
              ) : conversation.turn.answer ? (
                <Text style={styles.answer}>{conversation.turn.answer}</Text>
              ) : null}
            </View>
          ) : null}

          {conversation.error ? (
            <Text style={styles.error}>{conversation.error}</Text>
          ) : null}

          {dictation.error ? (
            <View style={styles.dictationError}>
              <Text style={styles.error}>{dictation.error}</Text>
              <View style={styles.errorActions}>
                {/* The recording is retained, so retry re-sends the same audio
                    rather than asking the user to say it again (§11). */}
                <Pressable onPress={dictation.retry} accessibilityRole="button">
                  <Text style={styles.errorAction}>Retry</Text>
                </Pressable>
                <Pressable onPress={dictation.dismissError} accessibilityRole="button">
                  <Text style={styles.errorActionMuted}>Discard</Text>
                </Pressable>
              </View>
            </View>
          ) : null}

          {dictation.permissionDenied ? (
            <Text style={styles.error}>
              Microphone access is off. You can still type — tap the keyboard.
            </Text>
          ) : null}
        </ScrollView>

        {typing ? (
          <Glass radius={radius.xl} style={styles.composer}>
            <TextInput
              ref={inputRef}
              style={styles.input}
              placeholder="Ask Scout…"
              placeholderTextColor={colors.text3}
              value={draft}
              onChangeText={setDraft}
              onSubmitEditing={submitTyped}
              returnKeyType="send"
              multiline
              onBlur={() => setTyping(false)}
            />
          </Glass>
        ) : (
          <Dock
            dictation={dictation.state}
            amplitude={dictation.amplitude}
            conversing={conversation.open}
            onTap={handleTap}
            onHoldStart={() => void dictation.start()}
            onHoldEnd={() => void dictation.stop()}
            onHoldCancel={dictation.cancel}
            onReveal={openDeck}
            onKeyboard={handleKeyboard}
          />
        )}
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  fill: { flex: 1 },
  body: {
    flexGrow: 1,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: space[6],
    paddingVertical: space[8],
    gap: space[5],
  },
  mark: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 7,
  },
  markLabel: {
    ...type.label,
    color: colors.text3,
    textTransform: 'uppercase',
  },
  wave: {
    paddingHorizontal: space[10],
    paddingVertical: space[5],
    borderRadius: radius.xxl,
  },
  greeting: {
    ...type.title1,
    color: colors.text1,
    textAlign: 'center',
  },
  liveLine: {
    paddingHorizontal: space[3],
    paddingVertical: space[2],
    borderRadius: radius.md,
  },
  pressed: { opacity: 0.6 },
  liveText: {
    ...type.bodySm,
    color: colors.text2,
    textAlign: 'center',
  },
  liveMention: {
    color: colors.text1,
  },
  turn: {
    alignSelf: 'stretch',
    gap: space[2],
    marginTop: space[4],
  },
  question: {
    ...type.bodySm,
    color: colors.text3,
    textAlign: 'center',
  },
  answer: {
    ...type.body,
    color: colors.text1,
    textAlign: 'center',
  },
  thinking: { alignSelf: 'center' },
  error: {
    ...type.bodySm,
    color: colors.danger,
    textAlign: 'center',
  },
  dictationError: {
    alignSelf: 'stretch',
    gap: space[2],
    alignItems: 'center',
  },
  errorActions: {
    flexDirection: 'row',
    gap: space[5],
  },
  errorAction: {
    ...type.button,
    color: colors.ember,
  },
  errorActionMuted: {
    ...type.button,
    color: colors.text3,
  },
  composer: {
    marginHorizontal: space[5],
    marginBottom: space[2],
    paddingHorizontal: space[4],
    paddingVertical: space[3],
  },
  input: {
    minHeight: 44,
    maxHeight: 140,
    ...type.body,
    color: colors.text1,
    textAlignVertical: 'top',
  },
});
