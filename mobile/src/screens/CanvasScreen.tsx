import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Keyboard,
  Modal,
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
import { useFocusEffect, useNavigation } from '@react-navigation/native';
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
import { usePersonalRealtimeContext } from '../realtime/PersonalRealtimeContext';
import { useComposerDictation } from '../voice/useComposerDictation';
import type { HomeStarterDestination, HomeStarterSuggestion } from '../api/types';
import type { HomeProjectChoice } from '../api/types';
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
  const realtime = usePersonalRealtimeContext();
  const listening = realtime.active;
  const [draft, setDraft] = useState('');
  const [activeStarterID, setActiveStarterID] = useState<string | null>(null);
  const [draftDestination, setDraftDestination] = useState<HomeStarterDestination | null>(null);
  const [keyboardVisible, setKeyboardVisible] = useState(false);
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState('');
  const [projectAvailable, setProjectAvailable] = useState(false);
  const [projectSessionToken, setProjectSessionToken] = useState('');
  const [projectChoices, setProjectChoices] = useState<HomeProjectChoice[]>([]);
  const [selectedProject, setSelectedProject] = useState<(HomeProjectChoice & { text: string }) | null>(null);
  const [projectChooserOpen, setProjectChooserOpen] = useState(false);
  const [newProjectTitle, setNewProjectTitle] = useState('');
  const [projectError, setProjectError] = useState('');
  const [projectFocusGeneration, setProjectFocusGeneration] = useState(0);
  const largeHomeType = fontScale >= 1.5;
  const activeStarter = home.starters.find((starter) => starter.id === activeStarterID);
  const inputRef = useRef<TextInput>(null);
  const draftRef = useRef(draft);
  const draftDestinationRef = useRef<HomeStarterDestination | null>(draftDestination);
  const projectRequestGenerationRef = useRef(0);
  const projectScopeKeyRef = useRef('');
  const sessionTokenRef = useRef(sessionToken);
  const openingAttemptRef = useRef<HomeScoutOpeningAttempt | null>(null);
  const threadAttemptRef = useRef<{ key: string; operationId: string } | null>(null);
  draftRef.current = draft;
  draftDestinationRef.current = draftDestination;
  sessionTokenRef.current = sessionToken;
  const dictation = useComposerDictation({
    context: 'scout',
    onTranscript: ({ text }) => {
      projectRequestGenerationRef.current += 1;
      setDraft(text);
      setDraftDestination(null);
      setProjectChoices([]);
      setSelectedProject(null);
      setProjectChooserOpen(false);
      requestAnimationFrame(() => inputRef.current?.focus());
    },
  });
  const dictationActive = dictation.state !== 'idle';
  const dictationCanCommit = ['listening', 'held', 'error'].includes(dictation.state);

  const refreshProjectContext = useCallback(async (createTitle = '') => {
    if (!sessionToken) return;
    const text = draft.trim();
    const destination = draftDestination?.route === 'thread'
      ? { route: 'thread' as const, threadId: draftDestination.threadId }
      : { route: 'new-private' as const };
    const destinationKey = JSON.stringify(destination);
    const generation = ++projectRequestGenerationRef.current;
    try {
      const response = await api.projectContext(sessionToken, {
        text,
        destination,
        ...(createTitle ? { createTitle } : {}),
      });
      const currentDestination = draftDestinationRef.current?.route === 'thread'
        ? { route: 'thread' as const, threadId: draftDestinationRef.current.threadId }
        : { route: 'new-private' as const };
      if (generation !== projectRequestGenerationRef.current
        || sessionTokenRef.current !== sessionToken
        || draftRef.current.trim() !== text
        || JSON.stringify(currentDestination) !== destinationKey) return;
      const context = response.projectContext;
      if (projectScopeKeyRef.current && context.scopeKey && projectScopeKeyRef.current !== context.scopeKey) {
        setSelectedProject(null);
      }
      projectScopeKeyRef.current = String(context.scopeKey ?? '');
      setProjectSessionToken(sessionToken);
      setProjectAvailable(Boolean(context.available));
      setProjectChoices(Array.isArray(context.choices) ? context.choices : []);
      if (createTitle && context.suggested?.token) setSelectedProject({ ...context.suggested, text });
      else if (context.suggested?.token) setSelectedProject((current) => current ?? { ...context.suggested!, text });
      setProjectError('');
    } catch (error) {
      if (generation !== projectRequestGenerationRef.current || sessionTokenRef.current !== sessionToken) return;
      setProjectAvailable(false);
      setProjectSessionToken(sessionToken);
      setProjectChoices([]);
      setSelectedProject(null);
      setProjectError(error instanceof Error ? error.message : 'Project context is unavailable.');
    }
  }, [draft, draftDestination, sessionToken]);

  useEffect(() => {
    projectRequestGenerationRef.current += 1;
    projectScopeKeyRef.current = '';
    setProjectAvailable(false);
    setProjectSessionToken('');
    setProjectChoices([]);
    setSelectedProject(null);
    setProjectChooserOpen(false);
    setProjectError('');
  }, [sessionToken]);

  useFocusEffect(
    useCallback(() => {
      projectRequestGenerationRef.current += 1;
      projectScopeKeyRef.current = '';
      setProjectAvailable(false);
      setProjectChoices([]);
      setSelectedProject(null);
      setProjectChooserOpen(false);
      setProjectFocusGeneration((current) => current + 1);
      return undefined;
    }, [sessionToken]),
  );

  useEffect(() => {
    const show = Keyboard.addListener('keyboardDidShow', () => setKeyboardVisible(true));
    const hide = Keyboard.addListener('keyboardDidHide', () => setKeyboardVisible(false));
    return () => {
      show.remove();
      hide.remove();
    };
  }, []);

  useEffect(() => {
    if (!sessionToken || draftDestination?.route === 'thread') {
      setProjectAvailable(false);
      setProjectChoices([]);
      setSelectedProject(null);
      return;
    }
    const timer = setTimeout(() => { void refreshProjectContext(); }, 240);
    return () => clearTimeout(timer);
  }, [draft, draftDestination, projectFocusGeneration, refreshProjectContext, sessionToken]);

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
    const projectContextToken = projectSessionToken === sessionToken && selectedProject?.text === text ? selectedProject.token : '';
    const attempt = homeScoutOpeningAttempt(openingAttemptRef.current, draft, undefined, projectContextToken);
    if (!attempt) return;
    openingAttemptRef.current = attempt;
    setSending(true);
    setSendError('');
    const result = await submitHomeScoutOpening(attempt, {
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
      setSelectedProject(null);
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
  }, [draft, draftDestination, navigation, projectSessionToken, selectedProject, sending, sessionToken]);

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
    projectRequestGenerationRef.current += 1;
    setDraft(suggestion.text);
    setDraftDestination(suggestion.destination);
    openingAttemptRef.current = null;
    threadAttemptRef.current = null;
    setSendError('');
    setProjectChoices([]);
    setSelectedProject(null);
    setProjectChooserOpen(false);
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
                  projectRequestGenerationRef.current += 1;
                  setDraft(value);
                  if (selectedProject?.text !== value.trim()) setSelectedProject(null);
                  setProjectChoices([]);
                  setProjectChooserOpen(false);
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
            {draftDestination?.route !== 'thread' && projectAvailable && projectSessionToken === sessionToken ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={selectedProject ? `Project: ${selectedProject.title}. Change project` : 'Add project'}
                accessibilityHint="Chooses an authorized project for this message. Nothing is linked until Send."
                onPress={() => setProjectChooserOpen(true)}
                style={({ pressed }) => [styles.projectChip, pressed && styles.starterPressed]}
              >
                <SymbolView name="folder" size={14} tintColor={colors.text3} />
                <Text maxFontSizeMultiplier={1.8} style={styles.projectChipText}>
                  {selectedProject ? `${selectedProject.suggested ? 'Suggested' : 'Project'} · ${selectedProject.title}` : 'Add project'}
                </Text>
              </Pressable>
            ) : null}
            {sendError ? <Text accessibilityRole="alert" maxFontSizeMultiplier={1.8} style={styles.sendError}>{sendError}</Text> : null}
          </View>

          {home.starters.length > 0 && !activeStarter ? (
            <View accessibilityLabel="Ways to start" style={styles.starters}>
              {home.starters.map((starter) => (
                <Pressable
                  key={starter.id}
                  disabled={!home.startersReady}
                  accessibilityRole="button"
                  accessibilityLabel={starter.label}
                  accessibilityHint={home.startersReady ? 'Shows editable message suggestions.' : 'Suggestions are loading.'}
                  accessibilityState={{ disabled: !home.startersReady, expanded: activeStarterID === starter.id }}
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
                  accessibilityHint={`${suggestion.whyThis} Fills the editable message field. Nothing is sent until you press Send.`}
                  onPress={() => useStarterSuggestion(suggestion)}
                  style={({ pressed }) => [styles.suggestionRow, pressed && styles.suggestionRowPressed]}
                >
                  <View style={styles.suggestionCopy}>
                    <Text maxFontSizeMultiplier={1.8} style={styles.suggestionText}>{suggestion.text}</Text>
                    <Text maxFontSizeMultiplier={1.8} style={styles.suggestionWhy}>{suggestion.whyThis}</Text>
                  </View>
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
		  <Modal animationType="slide" presentationStyle="pageSheet" visible={projectChooserOpen && projectSessionToken === sessionToken} onRequestClose={() => setProjectChooserOpen(false)}>
		<SafeAreaView style={styles.projectSheet}>
		  <View style={styles.projectSheetHeader}>
			<Text accessibilityRole="header" maxFontSizeMultiplier={1.8} style={styles.projectSheetTitle}>Choose a project</Text>
			<Pressable accessibilityRole="button" accessibilityLabel="Close project chooser" onPress={() => setProjectChooserOpen(false)} style={styles.projectSheetClose}><SymbolView name="xmark" size={17} tintColor={colors.text1} /></Pressable>
		  </View>
		  <ScrollView contentContainerStyle={styles.projectSheetBody} keyboardShouldPersistTaps="handled">
				{[{ title: 'No project', token: '' } as HomeProjectChoice, ...(projectSessionToken === sessionToken ? projectChoices : [])].map((choice) => {
			  const selected = String(selectedProject?.token ?? '') === choice.token;
			  return <Pressable key={choice.token || 'none'} accessibilityRole="radio" accessibilityState={{ selected }} accessibilityLabel={choice.title} onPress={() => { setSelectedProject(choice.token ? { ...choice, text: draft.trim() } : null); setProjectChooserOpen(false); }} style={({ pressed }) => [styles.projectChoice, selected && styles.projectChoiceSelected, pressed && styles.starterPressed]}><Text maxFontSizeMultiplier={1.8} style={styles.projectChoiceText}>{choice.title}</Text>{selected ? <SymbolView name="checkmark" size={16} tintColor={colors.ember} /> : null}</Pressable>;
			})}
			<View style={styles.projectCreateRow}>
			  <TextInput accessibilityLabel="New private project name" maxLength={120} onChangeText={setNewProjectTitle} placeholder="New private project" placeholderTextColor={colors.text3} style={styles.projectCreateInput} value={newProjectTitle} />
			  <Pressable accessibilityRole="button" accessibilityLabel="Add new private project" disabled={!newProjectTitle.trim()} onPress={() => { const title = newProjectTitle.trim(); if (!title) return; void refreshProjectContext(title).then(() => { setNewProjectTitle(''); setProjectChooserOpen(false); }); }} style={({ pressed }) => [styles.projectCreateButton, !newProjectTitle.trim() && styles.composerSendDisabled, pressed && styles.starterPressed]}><Text style={styles.projectCreateButtonText}>Add</Text></Pressable>
			</View>
			{projectError ? <Text accessibilityRole="alert" maxFontSizeMultiplier={1.8} style={styles.sendError}>{projectError}</Text> : null}
		  </ScrollView>
		</SafeAreaView>
	  </Modal>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  projectChip: { minHeight: 44, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[3], borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, backgroundColor: colors.surface1 },
  projectChipText: { ...type.caption, color: colors.text2 },
  projectSheet: { flex: 1, backgroundColor: colors.bgApp },
  projectSheetHeader: { minHeight: 64, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', paddingHorizontal: space[4], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  projectSheetTitle: { ...type.title2, color: colors.text1 },
  projectSheetClose: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.surface2 },
  projectSheetBody: { gap: space[2], padding: space[4] },
  projectChoice: { minHeight: 52, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', paddingHorizontal: space[3], borderRadius: radius.lg, backgroundColor: colors.surface1 },
  projectChoiceSelected: { backgroundColor: colors.surface2 },
  projectChoiceText: { ...type.body, color: colors.text1 },
  projectCreateRow: { flexDirection: 'row', alignItems: 'center', gap: space[2], marginTop: space[3], paddingTop: space[3], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  projectCreateInput: { ...type.body, minWidth: 0, height: 48, flex: 1, paddingHorizontal: space[3], borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, color: colors.text1, backgroundColor: colors.surface1 },
  projectCreateButton: { minWidth: 64, height: 48, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[3], borderRadius: radius.lg, backgroundColor: colors.text1 },
  projectCreateButtonText: { ...type.captionMedium, color: colors.bgApp },
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
  suggestionCopy: { minWidth: 0, flex: 1, gap: 2 },
  suggestionText: { ...type.caption, color: colors.text2 },
  suggestionWhy: { ...type.label, color: colors.text3 },
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
