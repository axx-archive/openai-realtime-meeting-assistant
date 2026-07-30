import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { ChatCircle } from '../components/ChatCircle';
import { canvasCradleComposition } from '../components/CanvasCradleComposition';
import { NavCluster } from '../components/NavCluster';
import { StrideCradle } from '../components/StrideCradle';
import { useLiveLine } from '../canvas/useLiveLine';
import { liveLineDisplay } from '../canvas/liveLineDisplay';
import { useDictation } from '../voice/useDictation';
import { useScoutConversation } from '../voice/useScoutConversation';
import { duration, ease, useReduceMotion } from '../theme/motion';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type CanvasNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Canvas — design §4 and §9.
 *
 * The root of the app is a microphone, not a dashboard. "You don't operate it,
 * you just talk" only survives contact with a home screen that has almost
 * nothing to operate, so this page holds a greeting, one live line, and the
 * Signal Cradle. The cradle itself is the voice control.
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
  const reduceMotion = useReduceMotion();
  const live = useLiveLine();
  const lineDisplay = liveLineDisplay(live);
  const conversation = useScoutConversation();
  const [navOpen, setNavOpen] = useState(false);

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
    if (dictation.state !== 'idle') return;
    void dictation.start();
    // `dictation.start` is stable per state; re-running on every render would
    // restart the recorder mid-turn.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [answered, conversation.open, dictation.state]);

  // The live line routes to whatever it is actually talking about. A line that
  // names a specific message and then dumps you in a thread list would make the
  // user navigate twice to reach the thing they were just shown.
  const openLiveTarget = useCallback(() => {
    if (live.threadId) {
		navigation.navigate('Thread', {
		  threadId: live.threadId,
		  title: live.threadTitle ?? '#team',
		  messageId: live.messageId ?? undefined,
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
      <ScrollView
        contentContainerStyle={canvasCradleComposition.body}
        keyboardShouldPersistTaps="handled"
        showsVerticalScrollIndicator={false}
      >
        <View style={canvasCradleComposition.skyAbove} />

        <Pressable
          accessibilityRole="button"
          accessibilityLabel={listening ? 'Listening' : 'Talk to Scout'}
          accessibilityHint={listening ? 'Tap to end this voice conversation.' : 'Tap to start a voice conversation.'}
          onPress={handleTap}
          style={({ pressed }) => [canvasCradleComposition.wave, pressed && styles.wavePressed]}
        >
          <StrideCradle trace={dictation.trace} listening={listening} />
        </Pressable>

        <View style={canvasCradleComposition.copyBlock}>
          <Text style={styles.greeting}>{greeting()}</Text>

          {lineDisplay.visible ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={lineDisplay.accessibilityLabel}
              accessibilityHint={lineDisplay.accessibilityHint}
              onPress={openLiveTarget}
              style={({ pressed }) => [styles.liveLine, pressed && styles.pressed]}
            >
              <Animated.Text
                style={[
                  styles.liveText,
                  live.mentioned && styles.liveMention,
                  { opacity: liveFade, transform: [{ translateY: liveRise }] },
                ]}
                numberOfLines={2}
                ellipsizeMode="tail"
              >
                {lineDisplay.authorSpan ? (
                  <Text style={styles.liveAuthor}>{lineDisplay.authorSpan}</Text>
                ) : null}
                {lineDisplay.bodySpan}
              </Animated.Text>
            </Pressable>
          ) : null}
        </View>

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

        {conversation.error ? <Text style={styles.error}>{conversation.error}</Text> : null}

        {dictation.error ? (
          <View style={styles.dictationError}>
            <Text style={styles.error}>{dictation.error}</Text>
            <View style={styles.errorActions}>
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
            Microphone access is off. Enable it in Settings to talk to Scout.
          </Text>
        ) : null}

        <View style={canvasCradleComposition.skyBelow} />
      </ScrollView>

      <View style={styles.navOverlay} pointerEvents="box-none">
        <ChatCircle
          clusterOpen={navOpen}
          mentioned={live.mentioned}
          onPress={() => {
            setNavOpen(false);
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
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  navOverlay: {
    position: 'absolute',
    left: 0,
    right: 0,
    bottom: 0,
    flexDirection: 'row',
    alignItems: 'flex-end',
    justifyContent: 'space-between',
  },
  wavePressed: { transform: [{ scale: 0.98 }] },
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
    color: colors.emberText,
  },
  errorActionMuted: {
    ...type.button,
    color: colors.text3,
  },
});
