import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { ChatCircle } from '../components/ChatCircle';
import { canvasCradleComposition } from '../components/CanvasCradleComposition';
import { NavCluster } from '../components/NavCluster';
import { StrideCradle } from '../components/StrideCradle';
import { Waveform } from '../components/Waveform';
import { useLiveLine } from '../canvas/useLiveLine';
import { liveLineDisplay } from '../canvas/liveLineDisplay';
import {
  homeScoutOpeningAttempt,
  submitHomeScoutOpening,
  type HomeScoutOpeningAttempt,
} from '../canvas/homeScoutOpening';
import { useComposerDictation } from '../voice/useComposerDictation';
import { usePersonalRealtime } from '../realtime/usePersonalRealtime';
import { useAuth } from '../auth/AuthContext';
import { api, BonfireApiError } from '../api/client';
import { duration, ease, useReduceMotion } from '../theme/motion';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type CanvasNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Canvas — design §4 and §9.
 *
 * The root of the app is a conversation, not a dashboard. Realtime Scout stays
 * the primary gesture, while the quiet bottom composer offers an explicit
 * typed or recorded-message path without pretending that dictation is a live
 * Scout call. The Signal Cradle remains the live conversation control.
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
  const { sessionToken } = useAuth();
  // No compact lockup competes with the live cradle on the Canvas.
  const reduceMotion = useReduceMotion();
  const live = useLiveLine();
  const lineDisplay = liveLineDisplay(live);
  const handleRealtimeActions = useCallback((actions: Array<Record<string, unknown>>) => {
    for (const action of actions) {
      const actionType = String(action.type ?? '').trim();
      const tool = String(action.tool ?? action.mode ?? '').trim();
      if (!['open_tool', 'assistant_mode'].includes(actionType)) continue;
      if (tool === 'chat') navigation.navigate('Deck', { segment: 'threads' });
      else if (['workflow', 'research', 'design', 'grill'].includes(tool)) {
        navigation.navigate('Deck', { segment: 'work' });
      } else if (tool === 'board') navigation.navigate('Board');
      else if (tool === 'artifacts' || tool === 'files') navigation.navigate('Files');
      else if (tool === 'meetings') navigation.navigate('Meetings');
      else if (tool === 'memory') navigation.navigate('Memory');
      else if (tool === 'intelligence') navigation.navigate('Intelligence');
      else if (tool === 'notifications' || tool === 'alerts') navigation.navigate('Alerts');
      else if (tool === 'settings') navigation.navigate('Settings');
    }
  }, [navigation]);
  const realtime = usePersonalRealtime({ onActions: handleRealtimeActions });
  const [navOpen, setNavOpen] = useState(false);
  const listening = realtime.active;

  const handleTap = useCallback(async () => {
    // The cradle has one stable meaning: a full-duplex Realtime Scout call.
    // Dictation is available only from composer microphones below.
    if (!realtime.enabled) return;
    if (realtime.active) {
      await realtime.stop('completed');
    } else {
      if (realtime.status === 'error') await realtime.stop('cancelled');
      await realtime.start();
    }
  }, [realtime]);

  const voiceError = realtime.enabled
    ? realtime.error
    : 'Live Scout is unavailable in this build. Composer dictation remains available from the mic.';

  const [composerDraft, setComposerDraft] = useState('');
  const [composerOpening, setComposerOpening] = useState(false);
  const [composerOpeningError, setComposerOpeningError] = useState<string | null>(null);
  const composerOpeningRef = useRef(false);
  const composerOpeningAttemptRef = useRef<HomeScoutOpeningAttempt | null>(null);
  const stopVoiceForComposer = useCallback(async () => {
    if (realtime.active) await realtime.stop('completed');
  }, [realtime]);
  const submitComposerText = useCallback(async (override?: string) => {
    if (!sessionToken || composerOpeningRef.current) return;
    const attempt = homeScoutOpeningAttempt(
      composerOpeningAttemptRef.current,
      String(override ?? composerDraft),
    );
    if (!attempt) return;

    // Pin the recoverable draft and key before teardown or network work. A
    // failed retry must address the same durable server operation.
    composerOpeningAttemptRef.current = attempt;
    composerOpeningRef.current = true;
    setComposerDraft(attempt.text);
    setComposerOpening(true);
    setComposerOpeningError(null);

    const result = await submitHomeScoutOpening(attempt, {
      stopVoice: stopVoiceForComposer,
      createThread: (body, idempotencyKey) => api.createScoutThread(
        sessionToken,
        body,
        idempotencyKey,
      ),
    });
    if (result.accepted) {
      // Acceptance is the only point that consumes the draft/key. The server
      // has committed the opening turn and reply placeholder, so the native
      // stack enters the thread without targeting a particular message.
      composerOpeningAttemptRef.current = null;
      setComposerDraft('');
      setComposerOpeningError(null);
      navigation.navigate('Thread', {
        threadId: result.thread.threadId,
        title: result.thread.title,
      });
    } else {
      setComposerOpeningError(
        result.error instanceof BonfireApiError
          ? result.error.message
          : 'Scout could not open that thread. Your message is still here.',
      );
    }
    composerOpeningRef.current = false;
    setComposerOpening(false);
  }, [composerDraft, navigation, sessionToken, stopVoiceForComposer]);
  const composerDictation = useComposerDictation({
    context: 'scout',
    // The home recording belongs to the not-yet-created opening turn, never a
    // prior fallback voice thread.
    threadId: undefined,
    onTranscript: ({ text }) => {
      // Mirror the final text into the field for continuity, then use the same
      // private-thread send path as typed input. The hook generation-fences
      // late completions, so one accepted transcript posts exactly once.
      setComposerDraft(text);
      void submitComposerText(text);
    },
  });

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

          {/* One voice control. Static identity belongs to launch/login chrome. */}
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={realtime.status === 'connecting' ? 'Connecting to Scout' : listening ? 'Listening' : 'Talk to Scout'}
            accessibilityHint={listening ? 'Tap to end this voice conversation.' : 'Tap to start a voice conversation.'}
            onPress={() => { void handleTap(); }}
            style={({ pressed }) => [canvasCradleComposition.wave, pressed && styles.wavePressed]}
          >
            <StrideCradle
              trace={realtime.trace}
              listening={listening}
              source={realtime.status === 'talking' ? 'agent' : 'human'}
            />
          </Pressable>

          <View style={canvasCradleComposition.copyBlock}>
            <Text style={styles.greeting}>{greeting()}</Text>

            {/* Every decision about WHAT text appears lives in liveLineDisplay,
                so it can be asserted without a React renderer — this component
                keeps only the styling and the motion. */}
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
                  // Two lines, then ellipsis. A long message must never push the
                  // bottom utility row around — the composition is load-bearing.
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

          {voiceError ? (
            <Text style={styles.error}>{voiceError}</Text>
          ) : null}

        <View style={canvasCradleComposition.skyBelow} />
      </ScrollView>

      <View style={styles.navRow} pointerEvents="box-none">
        {/* One tap to the team thread, always. The circle yields while the
            cluster is open rather than competing for the same band. */}
        <ChatCircle
          clusterOpen={navOpen}
          mentioned={live.mentioned}
          onPress={() => {
            setNavOpen(false);
            // The TABLE, always — not live.threadId, which follows the line and
            // can point at a mention in another channel.
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
        {/* Never routed through the voice pipeline — this path still works
            when the model quota is gone or the mic is denied. */}
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

      <KeyboardAvoidingView
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
        pointerEvents="box-none"
        style={styles.composerDock}
      >
        {composerDictation.error ? (
          <Text accessibilityLiveRegion="polite" numberOfLines={2} style={styles.composerError}>
            {composerDictation.error}
          </Text>
        ) : null}
        {composerOpeningError ? (
          <Text accessibilityLiveRegion="polite" numberOfLines={2} style={styles.composerError}>
            {composerOpeningError}
          </Text>
        ) : null}
        <View style={styles.composerBar}>
          {composerDictation.state === 'listening' || composerDictation.state === 'held' || composerDictation.state === 'transcribing' || composerDictation.state === 'error' ? (
            <>
              <Pressable
                accessibilityLabel="Delete dictated message"
                accessibilityRole="button"
                onPress={() => { void composerDictation.discard(); }}
                style={({ pressed }) => [styles.composerIcon, pressed && styles.composerPressed]}
              >
                <SymbolView name="xmark" tintColor={colors.text2} size={18} />
              </Pressable>
              <View style={styles.composerVoiceBody}>
                {composerDictation.state === 'transcribing' ? (
                  <View style={styles.transcribingRow}>
                    <ActivityIndicator color={colors.text2} size="small" />
                    <Text accessibilityLiveRegion="polite" style={styles.composerStateText}>Transcribing</Text>
                  </View>
                ) : (
                  <>
                    <Waveform trace={composerDictation.trace} listening={composerDictation.state === 'listening'} height={28} scale={0.58} />
                    <Text style={styles.composerStateText}>
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
                style={({ pressed }) => [styles.composerSend, composerDictation.state === 'transcribing' && styles.composerSendDisabled, pressed && styles.composerPressed]}
              >
                <SymbolView name="arrow.up" tintColor={colors.onAccent} size={18} />
              </Pressable>
            </>
          ) : (
            <>
              <Pressable
                accessibilityHint="Starts dictation. Press Send once when you are finished to transcribe and open the Scout chat."
                accessibilityLabel="Dictate a message"
                accessibilityRole="button"
                accessibilityState={{ disabled: composerOpening }}
                disabled={composerOpening}
                onPress={() => { void composerDictation.start(); }}
                style={({ pressed }) => [styles.composerIcon, composerOpening && styles.composerSendDisabled, pressed && styles.composerPressed]}
              >
                <SymbolView name="mic.fill" tintColor={colors.ember} size={19} />
              </Pressable>
              <TextInput
                accessibilityLabel="Message Scout"
                blurOnSubmit={false}
                editable={!composerOpening}
                maxLength={4000}
                onChangeText={(value) => {
                  setComposerDraft(value);
                  if (value.trim() !== composerOpeningAttemptRef.current?.text) {
                    setComposerOpeningError(null);
                  }
                }}
                onFocus={() => { void stopVoiceForComposer(); }}
                onSubmitEditing={() => { void submitComposerText(); }}
                placeholder="Message Scout"
                placeholderTextColor={colors.text3}
                returnKeyType="send"
                style={styles.composerInput}
                value={composerDraft}
              />
              <Pressable
                accessibilityLabel="Send message"
                accessibilityRole="button"
                accessibilityState={{ disabled: !composerDraft.trim() || composerOpening }}
                disabled={!composerDraft.trim() || composerOpening}
                onPress={() => { void submitComposerText(); }}
                style={({ pressed }) => [styles.composerSend, (!composerDraft.trim() || composerOpening) && styles.composerSendDisabled, pressed && styles.composerPressed]}
              >
                {composerOpening ? <ActivityIndicator color={colors.onAccent} size="small" /> : <SymbolView name="arrow.up" tintColor={colors.onAccent} size={18} />}
              </Pressable>
            </>
          )}
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
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
  navRow: {
    // A quiet utility band at the safe-area edge. The centre stays empty so the
    // Signal remains the only voice control.
    flexDirection: 'row',
    alignItems: 'flex-end',
    justifyContent: 'space-between',
    position: 'absolute',
    left: 0,
    right: 0,
    // Clear the composer. Its controls remain independently reachable rather
    // than overlapping the two navigation circles on compact phones.
    bottom: 78,
  },
  wavePressed: {
    transform: [{ scale: 0.96 }],
    opacity: 0.88,
  },
  greeting: {
    // The only sentence on the page, so it carries the page rather than sharing
    // weight with the live line. Tracking tightens as size grows — at 36pt the
    // default spacing reads loose and juvenile.
    fontSize: 36,
    fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600',
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
    fontFamily: 'GoogleSansFlex_400Regular', fontWeight: '400',
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
    fontFamily: 'GoogleSansFlex_500Medium', fontWeight: '500',
  },
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
  composerDock: {
    position: 'absolute',
    left: space[3],
    right: space[3],
    bottom: 0,
    gap: space[1],
  },
  composerError: {
    ...type.caption,
    color: colors.danger,
    textAlign: 'center',
  },
  composerBar: {
    minHeight: 56,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    paddingHorizontal: 6,
    paddingVertical: 6,
    borderRadius: radius.xxl,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
    backgroundColor: colors.surface1,
  },
  composerIcon: {
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
  },
  composerPressed: {
    opacity: 0.72,
    transform: [{ scale: 0.96 }],
  },
  composerVoiceBody: {
    flex: 1,
    minWidth: 0,
    minHeight: 38,
    alignItems: 'center',
    justifyContent: 'center',
    overflow: 'hidden',
  },
  transcribingRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
  },
  composerStateText: {
    ...type.caption,
    color: colors.text2,
  },
  composerSend: {
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    backgroundColor: colors.accent,
  },
  composerSendDisabled: { opacity: 0.34 },
  composerInput: {
    ...type.body,
    flex: 1,
    minWidth: 0,
    minHeight: 42,
    paddingHorizontal: 2,
    paddingVertical: 10,
    color: colors.text1,
  },
});
