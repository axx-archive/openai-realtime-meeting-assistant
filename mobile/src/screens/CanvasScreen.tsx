import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Keyboard,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import {
  homeScoutOpeningAttempt,
  submitHomeScoutOpening,
  type HomeScoutOpeningAttempt,
} from '../canvas/homeScoutOpening';
import { canvasCradleComposition } from '../components/CanvasCradleComposition';
import { Waveform } from '../components/Waveform';
import { useHomeCanvas } from '../canvas/useLiveLine';
import { createConversationOperationId } from '../conversations/newConversation';
import { usePersonalRealtime } from '../realtime/usePersonalRealtime';
import { useComposerDictation } from '../voice/useComposerDictation';
import type { HomeStarterDestination, HomeStarterSuggestion } from '../api/types';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type CanvasNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Canvas — design §4 and §9.
 *
 * The root of the app is a conversation, not a dashboard. Realtime voice and
 * one ordinary text field are two inputs to the same private Scout contract.
 * Work remains the place to browse or create named chats and channels; Home
 * never asks the person to choose a tool or deliverable before speaking.
 *
 * Nothing here blocks first paint: the compact voice control renders before
 * current context resolves, then the server-owned snapshot fills in beneath
 * the composer without changing what the primary input means.
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
 * Personal context belongs in the bounded continuation rows below the composer,
 * where every item resolves to an exact server-owned destination.
 */
function greeting(): string {
  const hour = new Date().getHours();
  const part = hour < 12 ? 'morning' : hour < 18 ? 'afternoon' : 'evening';
  return `Good ${part}.`;
}

function starterPresentation(id: 'continue' | 'explore' | 'create' | 'challenge') {
  switch (id) {
    case 'continue': return { icon: 'arrow.clockwise' as const, color: colors.success };
    case 'explore': return { icon: 'scope' as const, color: colors.info };
    case 'create': return { icon: 'sparkles' as const, color: '#A78BFA' };
    case 'challenge': return { icon: 'diamond' as const, color: colors.ember };
  }
}

export function CanvasScreen() {
  const navigation = useNavigation<CanvasNav>();
  const { fontScale } = useWindowDimensions();
  const { sessionToken } = useAuth();
  const home = useHomeCanvas();
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
  const listening = realtime.active;
  const [draft, setDraft] = useState('');
  const [activeStarterID, setActiveStarterID] = useState<string | null>(null);
  const [draftDestination, setDraftDestination] = useState<HomeStarterDestination | null>(null);
  const [keyboardVisible, setKeyboardVisible] = useState(false);
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState('');
  const largeHomeType = fontScale >= 1.5;
  const activeStarter = home.starters.find((starter) => starter.id === activeStarterID);
  const inputRef = useRef<TextInput>(null);
  const openingAttemptRef = useRef<HomeScoutOpeningAttempt | null>(null);
  const threadAttemptRef = useRef<{ key: string; operationId: string } | null>(null);
  const dictation = useComposerDictation({
    context: 'scout',
    onTranscript: ({ text }) => {
      setDraft(text);
      setDraftDestination(null);
      requestAnimationFrame(() => inputRef.current?.focus());
    },
  });
  const dictationActive = dictation.state !== 'idle';
  const dictationCanCommit = ['listening', 'held', 'error'].includes(dictation.state);

  useEffect(() => {
    const show = Keyboard.addListener('keyboardDidShow', () => setKeyboardVisible(true));
    const hide = Keyboard.addListener('keyboardDidHide', () => setKeyboardVisible(false));
    return () => {
      show.remove();
      hide.remove();
    };
  }, []);

  const handleTap = useCallback(async () => {
    // The cradle has one stable meaning: a full-duplex Realtime Scout call.
    // Home also accepts an ordinary typed turn below; neither path asks the
    // person to choose a tool, template, model, or deliverable first.
    if (!realtime.enabled) return;
    Keyboard.dismiss();
    if (realtime.active) {
      await realtime.stop('completed');
    } else {
      if (realtime.status === 'error') await realtime.stop('cancelled');
      await realtime.start();
    }
  }, [realtime]);

  const sendOpening = useCallback(async () => {
    if (!sessionToken || sending) return;
    const text = draft.trim();
    if (!text) return;
    if (draftDestination?.route === 'thread') {
      const operationKey = JSON.stringify({ threadId: draftDestination.threadId, text });
      const threadAttempt = threadAttemptRef.current?.key === operationKey
        ? threadAttemptRef.current
        : { key: operationKey, operationId: createConversationOperationId() };
      threadAttemptRef.current = threadAttempt;
      setSending(true);
      setSendError('');
      try {
        if (realtime.active || realtime.status === 'error') await realtime.stop('cancelled');
        await api.sendScoutMessage(
          sessionToken,
          draftDestination.threadId,
          text,
          [],
          '',
          threadAttempt.operationId,
        );
        threadAttemptRef.current = null;
        openingAttemptRef.current = null;
        setDraft('');
        setDraftDestination(null);
        Keyboard.dismiss();
        navigation.navigate('Thread', {
          threadId: draftDestination.threadId,
          title: draftDestination.title || 'Conversation',
        });
      } catch (error) {
        setSendError(
          error instanceof BonfireApiError
            ? error.message
            : error instanceof Error
              ? error.message
              : 'Scout could not continue that conversation. Your message is still here.',
        );
      } finally {
        setSending(false);
      }
      return;
    }
    const attempt = homeScoutOpeningAttempt(openingAttemptRef.current, draft);
    if (!attempt) return;
    openingAttemptRef.current = attempt;
    setSending(true);
    setSendError('');
    const result = await submitHomeScoutOpening(attempt, {
      stopVoice: async () => {
        if (realtime.active || realtime.status === 'error') {
          await realtime.stop('cancelled');
        }
      },
      createThread: (body, idempotencyKey) => api.createScoutThread(
        sessionToken,
        body,
        idempotencyKey,
      ),
    });
    if (result.accepted) {
      openingAttemptRef.current = null;
      threadAttemptRef.current = null;
      setDraft('');
      setDraftDestination(null);
      Keyboard.dismiss();
      navigation.navigate('Thread', {
        threadId: result.thread.threadId,
        title: result.thread.title,
      });
    } else {
      setDraft(result.attempt.text);
      setSendError(
        result.error instanceof BonfireApiError
          ? result.error.message
          : result.error instanceof Error
            ? result.error.message
            : 'Scout could not open that conversation. Your message is still here.',
      );
    }
    setSending(false);
  }, [draft, draftDestination, navigation, realtime, sending, sessionToken]);

  // The disabled mic already communicates capability. Reserve copy below the
  // composer for a real runtime problem; a permanent policy sentence is
  // superfluous on the most important screen in the product.
  const voiceNotice = realtime.error;
  const liveMeeting = home.continuity.find((item) => item.kind === 'live-meeting');
  const continuityItems = home.continuity.filter((item) => item.kind !== 'live-meeting');

  const openContinuity = useCallback((item: (typeof home.continuity)[number]) => {
    const destination = item.destination;
    if (destination.route === 'alerts') {
      navigation.navigate('Alerts');
      return;
    }
    if (destination.route === 'room') {
      navigation.navigate('Room', { roomId: destination.roomId, title: destination.title || 'Meeting' });
      return;
    }
    navigation.navigate('Thread', {
      threadId: destination.threadId,
      title: destination.title || 'Conversation',
      messageId: destination.messageId,
    });
  }, [home.continuity, navigation]);

  const useStarterSuggestion = useCallback((suggestion: HomeStarterSuggestion) => {
    setDraft(suggestion.text);
    setDraftDestination(suggestion.destination);
    openingAttemptRef.current = null;
    threadAttemptRef.current = null;
    setSendError('');
    requestAnimationFrame(() => {
      inputRef.current?.focus();
      inputRef.current?.setNativeProps({
        selection: { start: suggestion.text.length, end: suggestion.text.length },
      });
    });
  }, []);

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
      <ScrollView
        contentContainerStyle={canvasCradleComposition.body}
        automaticallyAdjustKeyboardInsets
        keyboardShouldPersistTaps="handled"
        showsVerticalScrollIndicator={false}
      >
          <View style={[canvasCradleComposition.skyAbove, keyboardVisible && styles.keyboardSky]} />

          <View style={[canvasCradleComposition.copyBlock, styles.homeCopyBlock]}>
            <Text maxFontSizeMultiplier={1.35} style={styles.greeting}>{greeting()}</Text>
            {liveMeeting ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={`${liveMeeting.title} is live. ${liveMeeting.detail}. Open meeting.`}
                onPress={() => openContinuity(liveMeeting)}
                style={({ pressed }) => [styles.liveMeetingJump, pressed && styles.liveMeetingJumpPressed]}
              >
                <View accessibilityElementsHidden style={styles.liveMeetingDot} />
                <Text maxFontSizeMultiplier={1.5} numberOfLines={1} style={styles.liveMeetingTitle}>{liveMeeting.title}</Text>
                <Text maxFontSizeMultiplier={1.5} numberOfLines={1} style={styles.liveMeetingDetail}>{liveMeeting.detail}</Text>
                <SymbolView name="chevron.right" size={12} tintColor={colors.text3} />
              </Pressable>
            ) : null}
          </View>

          <View style={styles.composerBlock}>
            <View style={styles.composer}>
              {dictationActive ? (
                <View accessibilityLiveRegion="polite" style={styles.dictationState}>
                  <Waveform trace={dictation.trace} listening={dictation.state === 'listening'} height={24} scale={0.62} />
                  <Text maxFontSizeMultiplier={1.3} style={styles.dictationStatus}>
                    {dictation.state === 'listening'
                      ? 'Recording'
                      : dictation.state === 'held'
                        ? 'Ready to transcribe'
                        : dictation.state === 'transcribing'
                          ? 'Transcribing'
                          : 'Recording saved'}
                  </Text>
                </View>
              ) : <TextInput
                ref={inputRef}
                accessibilityLabel="Message Scout from Home"
                autoComplete="off"
                importantForAutofill="no"
                editable={Boolean(sessionToken) && !sending}
                enterKeyHint="send"
                maxLength={4000}
                onChangeText={(value) => {
                  setDraft(value);
                  if (openingAttemptRef.current?.text !== value.trim()) openingAttemptRef.current = null;
                  const threadID = draftDestination?.route === 'thread' ? draftDestination.threadId : '';
                  if (threadAttemptRef.current?.key !== JSON.stringify({ threadId: threadID, text: value.trim() })) {
                    threadAttemptRef.current = null;
                  }
                  if (!value.trim()) setDraftDestination(null);
                  if (sendError) setSendError('');
                }}
                onSubmitEditing={() => { void sendOpening(); }}
                placeholder="Message Scout"
                placeholderTextColor={colors.text3}
                returnKeyType="send"
                selectionColor={colors.info}
                textContentType="none"
                maxFontSizeMultiplier={1.6}
                style={styles.composerInput}
                value={draft}
              />}
              {!dictationActive ? (
                <Pressable
                  accessibilityLabel="Dictate a message"
                  accessibilityHint="Records a bounded message for transcription into this composer."
                  accessibilityRole="button"
                  onPress={() => { void dictation.start(); }}
                  style={({ pressed }) => [styles.composerMic, pressed && styles.composerActionPressed]}
                >
                  <SymbolView name="mic" size={20} tintColor={colors.text2} />
                </Pressable>
              ) : null}
              {dictationCanCommit ? (
                <Pressable
                  accessibilityLabel="Delete dictated clip"
                  accessibilityRole="button"
                  onPress={() => { void dictation.discard(); }}
                  style={({ pressed }) => [styles.composerMic, pressed && styles.composerActionPressed]}
                >
                  <SymbolView name="xmark" size={17} tintColor={colors.text2} />
                </Pressable>
              ) : null}
              {!dictationActive ? (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel={!realtime.enabled ? 'Voice unavailable' : realtime.status === 'connecting' ? 'Connecting to Scout' : listening ? 'End private voice chat' : 'Start a new private voice chat with Scout'}
                  accessibilityHint={listening ? 'Ends this voice conversation.' : 'Starts a full-duplex voice conversation and saves both sides in one private chat.'}
                  accessibilityState={{ disabled: !realtime.enabled }}
                  disabled={!realtime.enabled}
                  onPress={() => { void handleTap(); }}
                  style={({ pressed }) => [styles.composerVoice, listening && styles.composerVoiceLive, pressed && styles.composerActionPressed]}
                >
                  <SymbolView name="waveform" size={23} tintColor={listening ? colors.onEmber : colors.bgApp} />
                </Pressable>
              ) : null}
              <Pressable
                accessibilityLabel={dictationCanCommit
                  ? 'Transcribe dictated clip'
                  : draftDestination?.route === 'thread'
                    ? `Send message in ${draftDestination.title || 'existing conversation'}`
                    : 'Send message to a new private Scout conversation'}
                accessibilityRole="button"
                accessibilityState={{ disabled: dictation.state === 'transcribing' || (!dictationCanCommit && (sending || !draft.trim() || !sessionToken)) }}
                disabled={dictation.state === 'transcribing' || (!dictationCanCommit && (sending || !draft.trim() || !sessionToken))}
                onPress={() => { if (dictationCanCommit) void dictation.commit(); else void sendOpening(); }}
                style={({ pressed }) => [
                  styles.composerSend,
                  (dictation.state === 'transcribing' || (!dictationCanCommit && (sending || !draft.trim() || !sessionToken))) && styles.composerSendDisabled,
                  pressed && styles.composerSendPressed,
                ]}
              >
                {sending ? (
                  <ActivityIndicator color={colors.onAccent} size="small" />
                ) : (
                  <Text maxFontSizeMultiplier={1} style={styles.composerSendGlyph}>↑</Text>
                )}
              </Pressable>
            </View>
            {draftDestination?.route === 'thread' ? (
              <View
                accessibilityLabel={`This message will continue in ${draftDestination.title || 'an existing conversation'}`}
                style={styles.draftDestination}
              >
                <Text maxFontSizeMultiplier={1.8} style={styles.draftDestinationText}>
                  Continue in {draftDestination.title || 'existing conversation'}
                </Text>
                <Pressable
                  accessibilityLabel="Send as a new private conversation instead"
                  accessibilityRole="button"
                  onPress={() => {
                    setDraftDestination(null);
                    threadAttemptRef.current = null;
                  }}
                  style={({ pressed }) => [styles.draftDestinationAction, pressed && styles.starterPressed]}
                >
                  <Text maxFontSizeMultiplier={1.6} style={styles.draftDestinationActionText}>Change</Text>
                </Pressable>
              </View>
            ) : null}
            {sendError ? <Text accessibilityRole="alert" maxFontSizeMultiplier={1.8} style={styles.sendError}>{sendError}</Text> : null}
          </View>

          {home.starters.length > 0 && !activeStarter ? (
            <View accessibilityLabel="Ways to start" style={styles.starters}>
              {home.starters.map((starter) => (
                <Pressable
                  key={starter.id}
                  accessibilityRole="button"
                  accessibilityLabel={starter.label}
                  accessibilityHint="Shows editable message suggestions."
                  accessibilityState={{ expanded: activeStarterID === starter.id }}
                  onPress={() => setActiveStarterID(starter.id)}
                  style={({ pressed }) => [
                    styles.starter,
                    largeHomeType && styles.starterLargeType,
                    activeStarterID === starter.id && styles.starterSelected,
                    pressed && styles.starterPressed,
                  ]}
                >
                  <View style={styles.starterHeading}>
                    <SymbolView name={starterPresentation(starter.id).icon} size={14} tintColor={starterPresentation(starter.id).color} />
                    <Text maxFontSizeMultiplier={1.6} style={styles.starterLabel}>{starter.label}</Text>
                  </View>
                </Pressable>
              ))}
            </View>
          ) : null}

          {activeStarter ? (
            <View accessibilityLabel={`${activeStarter.label} suggestions`} style={styles.suggestionSurface}>
              <Pressable
                accessibilityLabel="Back to starter categories"
                accessibilityRole="button"
                onPress={() => setActiveStarterID(null)}
                style={({ pressed }) => [styles.suggestionHeadingControl, pressed && styles.starterPressed]}
              >
                <SymbolView name="chevron.left" size={13} tintColor={colors.text3} />
                <Text maxFontSizeMultiplier={1.6} style={styles.suggestionHeading}>{activeStarter.label}</Text>
              </Pressable>
              {activeStarter.suggestions.map((suggestion) => (
                <Pressable
                  key={suggestion.id}
                  accessibilityRole="button"
                  accessibilityLabel={suggestion.text}
                  accessibilityHint="Fills the editable message field. Nothing is sent until you press Send."
                  onPress={() => useStarterSuggestion(suggestion)}
                  style={({ pressed }) => [styles.suggestionRow, pressed && styles.suggestionRowPressed]}
                >
                  <Text maxFontSizeMultiplier={1.8} style={styles.suggestionText}>{suggestion.text}</Text>
                  <Text accessibilityElementsHidden maxFontSizeMultiplier={1} style={styles.suggestionArrow}>›</Text>
                </Pressable>
              ))}
            </View>
          ) : null}

          {voiceNotice ? (
            <Text accessibilityRole={realtime.error ? 'alert' : 'text'} maxFontSizeMultiplier={1.35} style={[styles.voiceNotice, realtime.error && styles.voiceError]}>{voiceNotice}</Text>
          ) : null}

          {continuityItems.length > 0 ? (
            <View accessibilityLabel="Your current context" style={styles.continuity}>
              {continuityItems.map((item) => (
                <Pressable
                  key={item.id}
                  accessibilityRole="button"
                  accessibilityLabel={`${item.eyebrow}. ${item.title}. ${item.detail}`}
                  onPress={() => openContinuity(item)}
                  style={({ pressed }) => [styles.continuityRow, pressed && styles.continuityRowPressed]}
                >
                  <View style={styles.continuityCopy}>
                    <Text maxFontSizeMultiplier={1.6} style={styles.continuityEyebrow}>{item.eyebrow}</Text>
                    <Text maxFontSizeMultiplier={1.6} style={styles.continuityTitle}>{item.title}</Text>
                    <Text maxFontSizeMultiplier={1.8} style={styles.continuityDetail}>{item.detail}</Text>
                  </View>
                  <Text accessibilityElementsHidden maxFontSizeMultiplier={1} style={styles.continuityArrow}>›</Text>
                </Pressable>
              ))}
            </View>
          ) : null}
          {home.freshness === 'loading' && home.continuity.length === 0 && home.starters.length === 0 ? (
            <ActivityIndicator accessibilityLabel="Loading your current context" color={colors.text3} size="small" style={styles.homeLoading} />
          ) : null}
          {home.refreshError ? (
            <Pressable accessibilityRole="button" onPress={() => { void home.refresh(); }} style={styles.refreshError}>
              <Text accessibilityRole="alert" maxFontSizeMultiplier={1.8} style={styles.refreshErrorText}>{home.refreshError}</Text>
              <Text maxFontSizeMultiplier={1.6} style={styles.refreshAction}>{home.refreshing ? 'Refreshing…' : 'Try again'}</Text>
            </Pressable>
          ) : null}

        <View style={[canvasCradleComposition.skyBelow, keyboardVisible && styles.keyboardSky]} />
      </ScrollView>

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
  wavePressed: {
    transform: [{ scale: 0.96 }],
    opacity: 0.88,
  },
  voiceControl: {
    minWidth: 168,
    minHeight: 58,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[2],
    paddingHorizontal: space[3],
    paddingVertical: space[1],
    borderRadius: radius.full,
    borderCurve: 'continuous',
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    backgroundColor: colors.surface1,
    marginBottom: space[4],
  },
  voiceControlLive: {
    borderColor: colors.ember,
    backgroundColor: colors.emberSoft,
  },
  voiceMic: {
    width: 34,
    height: 34,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
  },
  voiceMicLive: { backgroundColor: colors.ember },
  greeting: {
    // The only sentence on the page, so it carries the page rather than sharing
    // weight with the live line. Tracking tightens as size grows — at 36pt the
    // default spacing reads loose and juvenile.
    fontSize: 32,
    fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600',
    letterSpacing: -1,
    lineHeight: 38,
    color: colors.text1,
    textAlign: 'center',
    maxWidth: '100%',
    flexShrink: 1,
    // Bound to the greeting so the two read as one unit, not two stacked items.
    marginBottom: space[2],
  },
  homeCopyBlock: { width: '100%', minHeight: 0 },
  // When the software keyboard owns the lower half of the screen, elastic sky
  // would preserve empty space at the expense of the editable suggestions.
  // Collapse only that ornament; the composer and every available action keep
  // their stable order and remain reachable without a hidden first scroll.
  keyboardSky: { flex: 0, height: space[2] },
  starters: {
    width: '100%',
    maxWidth: 560,
    marginTop: space[3],
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: space[2],
  },
  starter: {
    minWidth: 148,
    minHeight: 72,
    flexGrow: 1,
    flexBasis: '47%',
    justifyContent: 'center',
    gap: 6,
    paddingHorizontal: 14,
    paddingVertical: 12,
    borderRadius: radius.lg,
    borderCurve: 'continuous',
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    backgroundColor: colors.surface1,
    shadowColor: '#000000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 8,
  },
  starterLargeType: {
    flexBasis: '100%',
    minHeight: 0,
  },
  starterSelected: {
    borderColor: colors.line2,
    backgroundColor: colors.surface2,
  },
  starterPressed: { opacity: 0.62 },
  starterHeading: { flexDirection: 'row', alignItems: 'center', gap: 7 },
  starterLabel: { ...type.captionMedium, color: colors.text1 },
  suggestionSurface: {
    width: '100%',
    maxWidth: 560,
    marginTop: space[4],
    gap: 2,
  },
  suggestionHeadingControl: {
    minHeight: 40,
    alignSelf: 'flex-start',
    flexDirection: 'row',
    alignItems: 'center',
    gap: 5,
    paddingHorizontal: space[2],
  },
  suggestionHeading: {
    ...type.label,
    color: colors.text3,
  },
  suggestionRow: {
    minHeight: 44,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    paddingHorizontal: space[2],
    paddingVertical: space[2],
    borderRadius: radius.md,
    borderCurve: 'continuous',
  },
  suggestionRowPressed: { backgroundColor: colors.surface2, transform: [{ scale: 0.96 }] },
  suggestionText: { ...type.caption, minWidth: 0, flex: 1, color: colors.text2 },
  suggestionArrow: { fontSize: 19, lineHeight: 22, color: colors.text3 },
  composerBlock: {
    width: '100%',
    maxWidth: 560,
    marginTop: space[4],
    gap: space[2],
  },
  liveMeetingJump: {
    minHeight: 34,
    maxWidth: 360,
    flexDirection: 'row',
    alignItems: 'center',
    alignSelf: 'center',
    gap: space[2],
    marginTop: space[3],
    paddingHorizontal: space[3],
    paddingVertical: space[1],
    borderRadius: radius.full,
    backgroundColor: colors.surface1,
  },
  liveMeetingJumpPressed: { transform: [{ scale: 0.98 }], opacity: 0.86 },
  liveMeetingDot: { width: 7, height: 7, borderRadius: radius.full, backgroundColor: colors.success },
  liveMeetingTitle: { ...type.caption, flexShrink: 1, fontWeight: '600', color: colors.text1 },
  liveMeetingDetail: { ...type.caption, flexShrink: 1, color: colors.text3 },
  composer: {
    minHeight: 56,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    paddingLeft: space[4],
    paddingRight: 6,
    borderRadius: radius.xxl,
    borderCurve: 'continuous',
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'transparent',
    backgroundColor: colors.surface1,
  },
  composerInput: {
    ...type.body,
    minWidth: 0,
    minHeight: 52,
    flex: 1,
    paddingVertical: 0,
    color: colors.text1,
  },
  dictationState: {
    minWidth: 0,
    minHeight: 44,
    flex: 1,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
  },
  dictationStatus: { ...type.caption, color: colors.text2 },
  composerMic: {
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
  },
  composerVoice: {
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    backgroundColor: colors.text1,
  },
  composerVoiceLive: { backgroundColor: colors.ember },
  composerActionPressed: { transform: [{ scale: 0.95 }], opacity: 0.88 },
  composerSend: {
    width: 44,
    height: 44,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: radius.full,
    backgroundColor: colors.accent,
  },
  composerSendDisabled: { opacity: 0.32 },
  composerSendPressed: { transform: [{ scale: 0.94 }] },
  composerSendGlyph: {
    color: colors.onAccent,
    fontSize: 23,
    fontFamily: 'GoogleSansFlex_600SemiBold',
    fontWeight: '600',
    lineHeight: 25,
  },
  draftDestination: {
    minHeight: 32,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: space[2],
    paddingHorizontal: space[2],
  },
  draftDestinationText: { ...type.caption, minWidth: 0, color: colors.text2 },
  draftDestinationAction: {
    minHeight: 32,
    justifyContent: 'center',
    paddingHorizontal: space[2],
    borderRadius: radius.full,
    borderCurve: 'continuous',
  },
  draftDestinationActionText: { ...type.captionMedium, color: colors.info },
  sendError: {
    ...type.caption,
    color: colors.danger,
    textAlign: 'center',
  },
  voiceNotice: {
    ...type.caption,
    color: colors.text3,
    textAlign: 'center',
  },
  voiceError: { color: colors.danger },
  continuity: {
    width: '100%',
    maxWidth: 620,
    marginTop: space[8],
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: colors.line1,
  },
  continuityRow: {
    minHeight: 76,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingVertical: space[3],
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line1,
  },
  continuityRowPressed: { opacity: 0.56 },
  continuityCopy: { minWidth: 0, flex: 1, gap: 2 },
  continuityEyebrow: {
    ...type.label,
    color: colors.ember,
    textTransform: 'uppercase',
    letterSpacing: 0.7,
  },
  continuityTitle: { ...type.bodyMedium, color: colors.text1 },
  continuityDetail: { ...type.caption, color: colors.text2 },
  continuityArrow: { fontSize: 28, lineHeight: 30, color: colors.text3 },
  refreshError: {
    width: '100%',
    maxWidth: 620,
    marginTop: space[4],
    alignItems: 'center',
    gap: space[2],
  },
  refreshErrorText: { ...type.caption, color: colors.text2, textAlign: 'center' },
  refreshAction: { ...type.captionMedium, color: colors.info },
  homeLoading: { marginTop: space[5] },
});
