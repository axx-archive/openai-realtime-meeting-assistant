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
import { runPersonalRealtimeTap } from '../realtime/personalRealtimeTap';
import { useComposerDictation } from '../voice/useComposerDictation';
import { explicitProjectAttachmentEnabled, safeProjectContextFromResponse } from '../messaging/projectContextPreflight';
import type { HomeStarterDestination } from '../api/types';
import type { HomeProjectChoice } from '../api/types';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type CanvasNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Canvas — design §4 and §9 (STRIDE mobile E2E evolution).
 *
 * Home is continuity: last work and threads to resume. The greeting and
 * starter cards (Continue / Explore / Create / Challenge Canvas) are gone.
 * The root is a conversation, not a dashboard. Realtime voice and one ordinary
 * text field are two inputs to the same private Scout contract.
 *
 * Nothing here blocks first paint: the compact voice control renders before
 * current context resolves, then the server-owned snapshot fills in beneath
 * the composer without changing what the primary input means.
 */

export function CanvasScreen() {
  const navigation = useNavigation<CanvasNav>();
  const { sessionToken } = useAuth();
  const home = useHomeCanvas();
  const realtime = usePersonalRealtimeContext();
  const listening = realtime.active;
  const [draft, setDraft] = useState('');
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
      const context = safeProjectContextFromResponse(response);
      if (!context) throw new Error('Project context is unavailable.');
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
    if (!explicitProjectAttachmentEnabled || !sessionToken || draftDestination?.route === 'thread') {
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
    Keyboard.dismiss();
    await runPersonalRealtimeTap(realtime);
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
    const projectContextToken = explicitProjectAttachmentEnabled && projectSessionToken === sessionToken && selectedProject?.text === text ? selectedProject.token : '';
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

  // A Realtime start can spend a few seconds waiting for the authenticated
  // office control channel before iOS capture and the provider offer begin.
  // Never make that honest startup look like a dead button: publish the live
  // state immediately, while still reserving persistent copy for real runtime
  // errors and active voice truth.
  const voiceNotice = realtime.error || (() => {
    switch (realtime.status) {
      case 'connecting': return 'Connecting to Scout…';
      case 'listening':
      case 'hearing': return 'Scout is listening';
      case 'thinking': return 'Scout is thinking';
      case 'acting': return 'Scout is working';
      case 'talking': return 'Scout is speaking';
      default: return null;
    }
  })();
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

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
      <ScrollView
        contentContainerStyle={canvasCradleComposition.body}
        automaticallyAdjustKeyboardInsets
        keyboardShouldPersistTaps="handled"
        showsVerticalScrollIndicator={false}
      >
          <View style={[canvasCradleComposition.skyAbove, keyboardVisible && styles.keyboardSky]} />

          {/* Home is continuity — no greeting, no starters. Just live meeting + composer. */}
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
              {!dictationActive && realtime.enabled ? (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel={realtime.status === 'connecting' ? 'Connecting to Scout' : listening ? 'End private voice chat' : 'Start a new private voice chat with Scout'}
                  accessibilityHint={listening ? 'Ends this voice conversation.' : 'Starts a full-duplex voice conversation and saves both sides in one private chat.'}
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

          {/* Starters (Continue/Explore/Create/Challenge) removed — Home is continuity only. */}

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
          {home.freshness === 'loading' && home.continuity.length === 0 ? (
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
		  <Modal animationType="slide" presentationStyle="pageSheet" visible={explicitProjectAttachmentEnabled && projectChooserOpen && projectSessionToken === sessionToken} onRequestClose={() => setProjectChooserOpen(false)}>
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
  // When the software keyboard owns the lower half of the screen, elastic sky
  // would preserve empty space at the expense of the editable suggestions.
  // Collapse only that ornament; the composer and every available action keep
  // their stable order and remain reachable without a hidden first scroll.
  keyboardSky: { flex: 0, height: space[2] },
  // starterPressed retained for project change buttons
  starterPressed: { opacity: 0.62 },
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
  liveMeetingDot: { width: 7, height: 7, borderRadius: radius.full, backgroundColor: colors.ember },
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
