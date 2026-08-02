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
import { useDictation } from '../voice/useDictation';
import { useComposerDictation } from '../voice/useComposerDictation';
import { useScoutConversation } from '../voice/useScoutConversation';
import { usePersonalRealtime } from '../realtime/usePersonalRealtime';
import { officeControlChannelIsLive } from '../realtime/OfficeEventsContext';
import { useAuth } from '../auth/AuthContext';
import type { AudioFocusLease } from '../voice/AudioFocusCoordinator';
import {
  runFallbackVoiceStartSingleflight,
  type FallbackVoiceStartAttempt,
} from '../voice/dictationAudioLifecycle';
import { audioFocusRuntime } from '../realtime/audioFocusRuntime';
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
  const conversation = useScoutConversation();
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
  const fallbackVoiceLeaseRef = useRef<AudioFocusLease | null>(null);
  const fallbackVoiceRequestGenerationRef = useRef(0);
  const fallbackVoiceStartAttemptRef = useRef<FallbackVoiceStartAttempt<AudioFocusLease> | null>(null);

  // Keyless fallback for the live Scout control. On the canvas a transcript is
  // a question, so it feeds the conversation loop rather than the composer.
  const voiceDictation = useDictation({
    context: 'chat',
    threadId: conversation.threadId ?? undefined,
    onTranscript: ({ text }) => {
      void conversation.ask(text);
    },
    // Dark-launch fallback only. Once native Realtime is qualified and enabled,
    // the Canvas never routes through file dictation; composer microphones keep
    // their distinct bounded record → review → transcribe lifecycle.
    legacyUploadOnStop: true,
  });
  const fallbackVoiceLifecycleRef = useRef({
    end: conversation.end,
    cancel: voiceDictation.cancel,
    stop: voiceDictation.stop,
  });
  fallbackVoiceLifecycleRef.current = {
    end: conversation.end,
    cancel: voiceDictation.cancel,
    stop: voiceDictation.stop,
  };

  const listening = realtime.enabled ? realtime.active : voiceDictation.state === 'listening';

  const startFallbackVoiceCapture = useCallback(async (
    requestGeneration: number,
    lease: AudioFocusLease,
  ): Promise<boolean> => runFallbackVoiceStartSingleflight(
    fallbackVoiceStartAttemptRef,
    requestGeneration,
    lease,
    async () => {
      if (
        requestGeneration !== fallbackVoiceRequestGenerationRef.current
        || fallbackVoiceLeaseRef.current !== lease
        || !lease.isCurrent()
        || !officeControlChannelIsLive(sessionToken)
      ) return false;

      const started = await voiceDictation.start();
      if (
        started
        && requestGeneration === fallbackVoiceRequestGenerationRef.current
        && fallbackVoiceLeaseRef.current === lease
        && lease.isCurrent()
        && officeControlChannelIsLive(sessionToken)
      ) return true;

      // Hang-up, takeover, or unmount may land while native permission/prepare/
      // record is suspended. Drain that exact start before its lease can leave.
      voiceDictation.cancel();
      await voiceDictation.stop();
      if (fallbackVoiceLeaseRef.current === lease) {
        fallbackVoiceLeaseRef.current = null;
        conversation.end();
        await lease.release(
          requestGeneration === fallbackVoiceRequestGenerationRef.current ? 'error' : 'cancelled',
        );
      }
      return false;
    },
  ), [conversation, sessionToken, voiceDictation]);

  const handleTap = useCallback(async () => {
    const requestGeneration = ++fallbackVoiceRequestGenerationRef.current;
    if (realtime.enabled) {
      if (realtime.active) {
        await realtime.stop('completed');
      } else {
        if (realtime.status === 'error') await realtime.stop('cancelled');
        await realtime.start();
      }
      return;
    }
    // Three cases, and the middle one is what makes this a LOOP rather than a
    // one-shot: ending a turn does NOT end the conversation.
    if (listening) {
      // Turn over — transcribe and ask. The loop stays open, and the re-arm
      // effect below puts the mic back up once Scout has answered.
      await voiceDictation.stop();
      return;
    }
    if (conversation.open) {
      voiceDictation.cancel();
      conversation.end();
      await voiceDictation.stop();
      const lease = fallbackVoiceLeaseRef.current;
      fallbackVoiceLeaseRef.current = null;
      await lease?.release('completed');
      return;
    }
    if (!sessionToken || !officeControlChannelIsLive(sessionToken)) return;
    let exactLease: AudioFocusLease | null = null;
    const lease = await audioFocusRuntime.acquire('personal_realtime', {
      forceClose: async () => {
        fallbackVoiceRequestGenerationRef.current += 1;
        if (fallbackVoiceLeaseRef.current === exactLease) fallbackVoiceLeaseRef.current = null;
        conversation.end();
        voiceDictation.cancel();
        await voiceDictation.stop();
      },
    });
    exactLease = lease;
    if (
      requestGeneration !== fallbackVoiceRequestGenerationRef.current
      || !lease.isCurrent()
      || !officeControlChannelIsLive(sessionToken)
    ) {
      await lease.release('cancelled');
      return;
    }
    fallbackVoiceLeaseRef.current = lease;
    conversation.start();
    await startFallbackVoiceCapture(requestGeneration, lease);
  }, [conversation, listening, realtime, sessionToken, startFallbackVoiceCapture, voiceDictation]);

  // The re-arm. This is the difference between "tap to dictate a question" and
  // "tap to have a conversation": once the answer lands, the mic comes back up
  // on its own so you can just keep talking (§5).
  const answered = Boolean(conversation.turn?.answer) && !conversation.thinking;
  useEffect(() => {
    if (realtime.enabled || !conversation.open || !answered) return;
    if (voiceDictation.state !== 'idle') return;
    const requestGeneration = fallbackVoiceRequestGenerationRef.current;
    const lease = fallbackVoiceLeaseRef.current;
    if (!sessionToken || !lease?.isCurrent() || !officeControlChannelIsLive(sessionToken)) return;
    void startFallbackVoiceCapture(requestGeneration, lease);
    // The guarded start callback is stable per state; re-running on every render would
    // restart the recorder mid-turn.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [answered, conversation.open, realtime.enabled, sessionToken, startFallbackVoiceCapture, voiceDictation.state]);

  useEffect(() => {
    if (!voiceDictation.permissionDenied) return;
    fallbackVoiceRequestGenerationRef.current += 1;
    conversation.end();
    const lease = fallbackVoiceLeaseRef.current;
    fallbackVoiceLeaseRef.current = null;
    void lease?.release('cancelled');
  }, [conversation, voiceDictation.permissionDenied]);

  useEffect(() => () => {
    fallbackVoiceRequestGenerationRef.current += 1;
    fallbackVoiceLifecycleRef.current.end();
    fallbackVoiceLifecycleRef.current.cancel();
    void fallbackVoiceLifecycleRef.current.stop();
    const lease = fallbackVoiceLeaseRef.current;
    fallbackVoiceLeaseRef.current = null;
    void lease?.release('cancelled');
  }, []);

  const voiceTurn = realtime.enabled ? realtime.turn : conversation.turn;
  const voiceThinking = realtime.enabled
    ? realtime.status === 'thinking' || realtime.status === 'acting'
    : conversation.thinking;
  const voiceError = realtime.enabled ? realtime.error : conversation.error;

  const [composerDraft, setComposerDraft] = useState('');
  const stopVoiceForComposer = useCallback(async () => {
    fallbackVoiceRequestGenerationRef.current += 1;
    if (realtime.active) await realtime.stop('completed');
    if (!realtime.enabled && conversation.open) {
      conversation.end();
      voiceDictation.cancel();
      await voiceDictation.stop();
      const lease = fallbackVoiceLeaseRef.current;
      fallbackVoiceLeaseRef.current = null;
      await lease?.release('completed');
    }
  }, [conversation, realtime, voiceDictation]);
  const submitComposerText = useCallback(async (override?: string) => {
    const text = String(override ?? composerDraft).trim();
    if (!text || conversation.thinking) return;
    await stopVoiceForComposer();
    setComposerDraft('');
    conversation.start();
    await conversation.ask(text);
  }, [composerDraft, conversation, stopVoiceForComposer]);
  const composerDictation = useComposerDictation({
    threadId: conversation.threadId ?? undefined,
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
              trace={realtime.enabled ? realtime.trace : voiceDictation.trace}
              listening={listening}
              source={realtime.enabled && realtime.status === 'talking' ? 'agent' : 'human'}
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

          {/* Scout's turn. Text-primary always — we never build an interaction
              whose output exists only as audio (§9.5). */}
          {voiceTurn ? (
            <View style={styles.turn}>
              {voiceTurn.question ? <Text style={styles.question}>{voiceTurn.question}</Text> : null}
              {voiceThinking ? (
                <ActivityIndicator color={colors.ember} style={styles.thinking} />
              ) : voiceTurn.answer ? (
                <Text style={styles.answer}>{voiceTurn.answer}</Text>
              ) : null}
            </View>
          ) : null}

          {voiceError ? (
            <Text style={styles.error}>{voiceError}</Text>
          ) : null}

          {!realtime.enabled && voiceDictation.error ? (
            <View style={styles.dictationError}>
              <Text style={styles.error}>{voiceDictation.error}</Text>
              <View style={styles.errorActions}>
                {/* The recording is retained, so retry re-sends the same audio
                    rather than asking the user to say it again (§11). */}
                <Pressable onPress={voiceDictation.retry} accessibilityRole="button">
                  <Text style={styles.errorAction}>Retry</Text>
                </Pressable>
                <Pressable onPress={voiceDictation.dismissError} accessibilityRole="button">
                  <Text style={styles.errorActionMuted}>Discard</Text>
                </Pressable>
              </View>
            </View>
          ) : null}

          {!realtime.enabled && voiceDictation.permissionDenied ? (
            <Text style={styles.error}>
              Microphone access is off. Open Threads from the shortcuts to type.
            </Text>
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
                    {composerDictation.state === 'held' || composerDictation.state === 'error' ? (
                      <Text style={styles.composerStateText}>Ready to transcribe</Text>
                    ) : null}
                  </>
                )}
              </View>
              {composerDictation.state === 'listening' ? (
                <Pressable
                  accessibilityLabel="Stop recording"
                  accessibilityRole="button"
                  onPress={() => { void composerDictation.stop(); }}
                  style={({ pressed }) => [styles.composerIcon, pressed && styles.composerPressed]}
                >
                  <SymbolView name="stop.fill" tintColor={colors.text2} size={16} />
                </Pressable>
              ) : null}
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
                accessibilityHint="Records a message, then lets you delete or transcribe and send it"
                accessibilityLabel="Dictate a message"
                accessibilityRole="button"
                onPress={() => { void composerDictation.start(); }}
                style={({ pressed }) => [styles.composerIcon, pressed && styles.composerPressed]}
              >
                <SymbolView name="mic.fill" tintColor={colors.ember} size={19} />
              </Pressable>
              <TextInput
                accessibilityLabel="Message Scout"
                blurOnSubmit={false}
                maxLength={4000}
                onChangeText={setComposerDraft}
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
                accessibilityState={{ disabled: !composerDraft.trim() || conversation.thinking }}
                disabled={!composerDraft.trim() || conversation.thinking}
                onPress={() => { void submitComposerText(); }}
                style={({ pressed }) => [styles.composerSend, (!composerDraft.trim() || conversation.thinking) && styles.composerSendDisabled, pressed && styles.composerPressed]}
              >
                {conversation.thinking ? <ActivityIndicator color={colors.onAccent} size="small" /> : <SymbolView name="arrow.up" tintColor={colors.onAccent} size={18} />}
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
