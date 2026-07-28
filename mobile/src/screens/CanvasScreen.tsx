import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
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
import { LinearGradient } from 'expo-linear-gradient';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { MomentumGlyph } from '../components/BrandMark';
import { Dock } from '../components/Dock';
import { ChatCircle } from '../components/ChatCircle';
import { NavCluster } from '../components/NavCluster';
import { Waveform } from '../components/Waveform';
import { useLiveLine } from '../canvas/useLiveLine';
import { useDictation } from '../voice/useDictation';
import { useScoutConversation } from '../voice/useScoutConversation';
import { Glass } from '../theme/glass';
import { duration, ease, useReduceMotion } from '../theme/motion';
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
 * The greeting carries no name — matching the web canon, whose
 * `officeLaunchGreeting` is simply "good morning."
 *
 * A name in a 36pt headline cannot be made robust. "Good afternoon,
 * Christopher." wraps; a display name stored as "Dr. May" greets you by your
 * title; initials, single names, and non-Latin scripts each break differently.
 * Every fix is another heuristic that is wrong for somebody, and a headline
 * that is occasionally wrong is worse than one that is never personal.
 *
 * The personal content belongs in the live line, which says something that
 * actually matters — who mentioned you, what is live — rather than proving the
 * app can read your account record.
 */
function greeting(): string {
  const hour = new Date().getHours();
  const part = hour < 12 ? 'morning' : hour < 18 ? 'afternoon' : 'evening';
  return `Good ${part}.`;
}

export function CanvasScreen() {
  const navigation = useNavigation<CanvasNav>();
  // `colors.text3` is a DynamicColorIOS pair, which expo-image's tintColor
  // rejects — so the glyph's tint is resolved here instead.
  const dark = useColorScheme() === 'dark';
  const markTint = dark ? 'rgba(247, 247, 249, 0.42)' : 'rgba(14, 14, 16, 0.38)';
  const reduceMotion = useReduceMotion();
  const live = useLiveLine();
  const conversation = useScoutConversation();
  const [typing, setTyping] = useState(false);
  const [navOpen, setNavOpen] = useState(false);
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

  // The live line routes to whatever it is actually talking about. A line that
  // names a specific message and then dumps you in a thread list would make the
  // user navigate twice to reach the thing they were just shown.
  const openLiveTarget = useCallback(() => {
    if (live.threadId) {
      navigation.navigate('Thread', {
        threadId: live.threadId,
        title: live.kind === 'rooms' ? 'Rooms' : '#team',
      });
      return;
    }
    if (live.kind === 'rooms') {
      navigation.navigate('Deck', { segment: 'rooms' });
      return;
    }
    navigation.navigate('Deck', { segment: 'threads' });
  }, [live.kind, live.threadId, navigation]);

  // Cross-fade plus a 4pt rise when the line's content changes. transform and
  // opacity only (motion canon §8.4); Reduce Motion sets the values outright so
  // the CONTENT still updates and only the movement goes away.
  const liveFade = useRef(new Animated.Value(1)).current;
  const liveRise = useRef(new Animated.Value(0)).current;
  useEffect(() => {
    if (!live.text) return;
    if (reduceMotion) {
      liveFade.setValue(1);
      liveRise.setValue(0);
      return;
    }
    liveFade.setValue(0);
    liveRise.setValue(4);
    Animated.parallel([
      Animated.timing(liveFade, {
        toValue: 1,
        duration: duration.med,
        easing: ease,
        useNativeDriver: true,
      }),
      Animated.timing(liveRise, {
        toValue: 0,
        duration: duration.med,
        easing: ease,
        useNativeDriver: true,
      }),
    ]).start();
  }, [ease, liveFade, liveRise, live.author, live.text, reduceMotion]);

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
          <View style={styles.skyAbove} />

          {/* The web's `.office-launch__mark`: the momentum glyph beside a
              small label, in text-3. This is the shell's only brand moment —
              removing the tab bar took away the glyph's old home. */}
          <View style={styles.mark} accessibilityRole="header" accessibilityLabel="Scout">
            <MomentumGlyph size={15} color={markTint} />
            <Text style={styles.markLabel}>SCOUT</Text>
          </View>

          {/* The one big tappable area, matching the web canon. */}
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={listening ? 'Listening' : 'Talk to Scout'}
            onPress={handleTap}
            style={styles.wave}
          >
            {/* Ambient light, EARNED. A resting wash renders as a stray grey
                lozenge — a horizontal gradient inside a rounded box has hard
                top and bottom edges — so the canvas stays truly flat at rest
                and the glow only exists while listening, where it is ember and
                means something. */}
            {listening ? (
              <LinearGradient
                colors={[
                  'rgba(255,107,74,0.00)',
                  'rgba(255,107,74,0.13)',
                  'rgba(255,107,74,0.00)',
                ]}
                start={{ x: 0, y: 0.5 }}
                end={{ x: 1, y: 0.5 }}
                style={styles.glow}
              />
            ) : null}
            <Waveform trace={dictation.trace} listening={listening} />
          </Pressable>

          <Text style={styles.greeting}>{greeting()}</Text>

          {live.text ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={live.author ? `${live.author}: ${live.text}` : live.text}
              accessibilityHint={live.threadId ? 'Opens the thread.' : 'Opens threads.'}
              onPress={openLiveTarget}
              style={({ pressed }) => [styles.liveLine, pressed && styles.pressed]}
            >
              <Animated.Text
                style={[
                  styles.liveText,
                  live.mentioned && styles.liveMention,
                  { opacity: liveFade, transform: [{ translateY: liveRise }] },
                ]}
                // Two lines, then ellipsis. A long message must never push the
                // Dock around — the composition (§9) is load-bearing.
                numberOfLines={2}
                ellipsizeMode="tail"
              >
                {live.author ? <Text style={styles.liveAuthor}>{live.author} · </Text> : null}
                {live.text}
              </Animated.Text>
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

          <View style={styles.skyBelow} />
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
          <View style={styles.dockRow}>
            <View style={styles.navRow}>
              {/* One tap to the team thread, always. The circle yields while
                  the cluster is open rather than competing for the same band. */}
              <ChatCircle
                clusterOpen={navOpen}
                mentioned={live.mentioned}
                onPress={() => {
                  setNavOpen(false);
                  // The TABLE, always — not live.threadId, which follows the
                  // line and can point at a mention in another channel. A
                  // control labelled "Team" that opens #pricing is a bug.
                  if (live.tableThreadId) {
                    navigation.navigate('Thread', {
                      threadId: live.tableThreadId,
                      title: '#team',
                    });
                    return;
                  }
                  navigation.navigate('Deck', { segment: 'threads' });
                }}
              />
            {/* Never routed through the voice pipeline — this is the path that
                still works when the model quota is gone or the mic is denied.
                Sits above the Dock so neither covers the other. */}
            <NavCluster
              open={navOpen}
              onToggle={() => setNavOpen((previous) => !previous)}
              destinations={[
                {
                  id: 'rooms',
                  label: 'Rooms',
                  icon: 'video.fill',
                  emphasis: true,
                  onPress: () => {
                    setNavOpen(false);
                    navigation.navigate('Deck', { segment: 'rooms' });
                  },
                },
                {
                  id: 'threads',
                  label: 'Threads',
                  icon: 'bubble.left.and.bubble.right.fill',
                  onPress: () => {
                    setNavOpen(false);
                    navigation.navigate('Deck', { segment: 'threads' });
                  },
                },
                {
                  id: 'new-room',
                  label: 'New',
                  icon: 'plus',
                  onPress: () => {
                    setNavOpen(false);
                    navigation.navigate('CreateRoom');
                  },
                },
                {
                  id: 'work',
                  label: 'Work',
                  icon: 'rectangle.3.group.fill',
                  onPress: () => {
                    setNavOpen(false);
                    navigation.navigate('Deck', { segment: 'work' });
                  },
                },
              ]}
            />
            </View>
            <Dock
              dictation={dictation.state}
              trace={dictation.trace}
              conversing={conversation.open}
              onTap={handleTap}
              onHoldStart={() => void dictation.start()}
              onHoldEnd={() => void dictation.stop()}
              onHoldCancel={dictation.cancel}
              onReveal={openDeck}
              onKeyboard={handleKeyboard}
            />
          </View>
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
    paddingHorizontal: space[6],
  },
  /**
   * Composition, not centring. Flex spacers weighted 1.25 : 1 sit the content
   * group slightly BELOW true centre.
   *
   * The rule: empty space above content reads as sky and is felt as
   * intentional; the same emptiness below content, stacked above a heavy UI
   * element, reads as a gap where something is missing. Geometric centring gave
   * 185pt above and 232pt below — the wrong way round, and the single biggest
   * reason the screen felt unfinished.
   */
  skyAbove: { flex: 1.25, pointerEvents: 'none' },
  skyBelow: { flex: 1, pointerEvents: 'none' },
  dockRow: {
    // Column, not row: the nav stacks above the Dock so neither covers the
    // other, and the cluster expands upward into empty canvas.
    justifyContent: 'flex-end',
  },
  navRow: {
    // The band above the Dock: chat on the left, the cluster toggle on the
    // right, the cluster expanding right-to-left between them. Bottom-aligned
    // so the circle and the toggle share a baseline even though the cluster's
    // items carry labels below theirs.
    flexDirection: 'row',
    alignItems: 'flex-end',
    justifyContent: 'space-between',
  },
  glow: {
    ...StyleSheet.absoluteFill,
    borderRadius: 999,
    pointerEvents: 'none',
  },
  mark: {
    flexDirection: 'row',
    // Baseline, not box: small caps sit high inside their line box, so centring
    // on the box leaves the glyph looking dropped. A 13pt line height on an
    // 11pt label collapses the slack and the two align optically.
    alignItems: 'center',
    gap: 7,
    marginBottom: space[6],
  },
  markLabel: {
    fontSize: 11,
    fontWeight: '600',
    // Wider than the label token: tracking has to open up as size drops or
    // uppercase text reads as a smudge at 11pt.
    letterSpacing: 1.4,
    lineHeight: 13,
    color: colors.text3,
    textTransform: 'uppercase',
  },
  wave: {
    alignSelf: 'stretch',
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: space[4],
    // 14 keeps a comfortable tap target without inflating the optical gap —
    // padding on a tap target still spends vertical rhythm.
    paddingVertical: 14,
    borderRadius: radius.xxl,
    marginBottom: space[5],
  },
  greeting: {
    // The only sentence on the page, so it carries the page rather than sharing
    // weight with the live line. Tracking tightens as size grows — at 36pt the
    // default spacing reads loose and juvenile.
    fontSize: 36,
    fontWeight: '600',
    letterSpacing: -1,
    lineHeight: 42,
    color: colors.text1,
    textAlign: 'center',
    // Bound to the greeting so the two read as one unit, not two stacked items.
    marginBottom: space[2],
  },
  liveLine: {
    paddingHorizontal: space[3],
    paddingVertical: 6,
    borderRadius: radius.md,
    // Wraps to two lines at most on a long sentence, and keeps the measure
    // short enough to stay readable centred.
    maxWidth: 330,
  },
  pressed: { opacity: 0.6 },
  liveText: {
    fontSize: 15,
    fontWeight: '400',
    letterSpacing: -0.08,
    lineHeight: 21,
    color: colors.text2,
    textAlign: 'center',
  },
  liveMention: {
    color: colors.text1,
  },
  liveAuthor: {
    // The author carries the line's weight; the message stays in text2 so the
    // pair reads as one sentence rather than two competing items.
    color: colors.text1,
    fontWeight: '500',
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
