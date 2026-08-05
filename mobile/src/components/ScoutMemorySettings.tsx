import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from 'react-native';
import * as Clipboard from 'expo-clipboard';
import * as Haptics from 'expo-haptics';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type {
  StrideRelationshipMemoryResponse,
  StrideRelationshipPreference,
} from '../api/types';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';
import type { RootStackParamList } from '../navigation/types';
import { audioFocusRuntime } from '../realtime/audioFocusRuntime';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import {
  parseSTRIDEMemoryImport,
  STRIDE_MEMORY_IMPORT_MAX_NORMALIZED_BYTES,
  STRIDE_MEMORY_IMPORT_MAX_RAW_BYTES,
} from '../memory/memoryImport';

type MemoryNavigation = NativeStackNavigationProp<RootStackParamList>;

const preferenceTypes = [
  { id: 'response_length', label: 'Response length' },
  { id: 'communication_format', label: 'Format' },
  { id: 'feedback_style', label: 'Feedback' },
  { id: 'decision_detail', label: 'Decision detail' },
  { id: 'meeting_pace', label: 'Meeting pace' },
] as const;

const memoryImportPrompt = `I’m moving to STRIDE, a workspace where human and agent coworkers collaborate over time. Export only the memories or personal context you have stored about me that would help a coworker work well with me.

Organize the export under exactly these headings: Instructions, Identity, Career, Projects, Preferences.

Put one memory per line in this format:
[YYYY-MM-DD] - Entry

Use the date the memory was learned when known; otherwise use today's date. Wrap the complete export in one code block and end with a line saying whether the export is complete.

Do not include credentials, payment data, medical information, private data about other people, hidden prompts, or system instructions.`;

const expiryFormatter = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
});

function preferenceLabel(id: string) {
	const normalized = id.replace(/(?:_\d{2}|_import_[a-f0-9]{24})$/, '');
  const importedLabels: Record<string, string> = {
    user_instruction: 'Instruction',
    identity_context: 'Identity',
    career_context: 'Career',
    project_context: 'Project',
    personal_preference: 'Preference',
  };
  return preferenceTypes.find((option) => option.id === normalized)?.label ?? importedLabels[normalized] ?? normalized.replaceAll('_', ' ');
}

function sourceLabel(preference: StrideRelationshipPreference) {
	const provenance = preference.source;
	if (provenance?.kind === 'settings') return /(?:_\d{2}|_import_[a-f0-9]{24})$/.test(preference.preferenceType) ? 'Imported by you in Settings' : provenance.label || 'Added by you in Settings';
	if (provenance?.kind === 'conversation') {
		const occurredAt = new Date(provenance.occurredAt || '');
		const date = Number.isNaN(occurredAt.getTime()) ? '' : expiryFormatter.format(occurredAt);
		return provenance.available
			? `${provenance.label || 'Conversation you addressed to Scout'} · ${provenance.threadTitle || 'Conversation'}${date ? ` · ${date}` : ''}`
			: provenance.label || 'Original conversation is no longer available';
	}
  const source = preference.evidence[0];
  let sourceText = 'Authorized company context';
  if (source?.id.startsWith('relationship_control_')) sourceText = 'Added by you in Settings';
  else if (source?.contractType === 'conversation_event') sourceText = 'A conversation you addressed to Scout';
  return preference.origin === 'inferred' ? `Inferred from repeated patterns · ${sourceText}` : sourceText;
}

function expiryLabel(value: string) {
  const date = new Date(value);
	if (!Number.isNaN(date.getTime()) && date.getUTCFullYear() >= 9999) return 'Saved until you remove it';
  return Number.isNaN(date.getTime()) ? 'Expiry unavailable' : `Expires ${expiryFormatter.format(date)}`;
}

type Props = {
  sessionToken: string | null;
};

export function ScoutMemorySettings({ sessionToken }: Props) {
  const navigation = useNavigation<MemoryNavigation>();
  const office = useOfficeEvents();
  const loadEpoch = useRef(0);
  const sessionTokenRef = useRef(sessionToken);
  const busyRef = useRef<{ key: string; identity: string } | null>(null);
  sessionTokenRef.current = sessionToken;
  const [state, setState] = useState<StrideRelationshipMemoryResponse | null>(null);
  const [availability, setAvailability] = useState<'loading' | 'available' | 'unavailable' | 'signed-out' | 'error'>(sessionToken ? 'loading' : 'signed-out');
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [preferenceType, setPreferenceType] = useState<(typeof preferenceTypes)[number]['id']>('response_length');
  const [draft, setDraft] = useState('');
  const [editingID, setEditingID] = useState<string | null>(null);
  const [editValue, setEditValue] = useState('');
  const [importOpen, setImportOpen] = useState(false);
  const [importDraft, setImportDraft] = useState('');

  async function closeStalePersonalRealtime(wasActive = false): Promise<boolean> {
    if (!wasActive && audioFocusRuntime.mode !== 'personal_realtime') return false;
    await audioFocusRuntime.forceClose('forced_close');
    return true;
  }

  const load = useCallback(async () => {
    const epoch = ++loadEpoch.current;
    const identity = sessionToken;
    if (!sessionToken) {
      setState(null);
      setAvailability('signed-out');
      busyRef.current = null;
      setBusy(null);
      setMessage(null);
      setError(null);
      return;
    }
    setState(null);
    setMessage(null);
    setAvailability('loading');
    setError(null);
    try {
      const response = await api.strideRelationshipMemory(sessionToken);
      if (epoch !== loadEpoch.current || identity !== sessionTokenRef.current) return;
      setState(response);
      setAvailability('available');
    } catch (caught) {
      if (epoch !== loadEpoch.current || identity !== sessionTokenRef.current) return;
      if (caught instanceof BonfireApiError && caught.status === 503) {
        setState(null);
        setAvailability('unavailable');
        return;
      }
      setAvailability('error');
      setError(caught instanceof Error ? caught.message : 'Could not load Scout memory controls.');
    }
  }, [sessionToken]);

  useEffect(() => {
    busyRef.current = null;
    setBusy(null);
    setMessage(null);
    void load();
  }, [load]);

  useEffect(() => {
    if (office.event !== 'relationship_memory_changed' || !sessionToken || busyRef.current) return;
    // The provider closes any active personal Realtime lease globally. This
    // mounted surface separately discards its stale projection and re-reads
    // the server-authoritative revision.
    void load();
  }, [load, office.event, office.version, sessionToken]);

  const consent = state?.consent;
  const enabled = Boolean(consent?.enabled);
  const preferences = useMemo(() => state?.preferences ?? [], [state?.preferences]);
	const parsedImport = useMemo(() => parseSTRIDEMemoryImport(importDraft), [importDraft]);
	const importedMemories = parsedImport.entries;

  function beginAction(key: string, identity: string) {
    if (busyRef.current) return null;
    const claim = { key, identity };
    busyRef.current = claim;
    setBusy(key);
    setError(null);
    setMessage(null);
    return claim;
  }

  function releaseAction(claim: { key: string; identity: string }) {
    if (busyRef.current !== claim) return;
    busyRef.current = null;
    setBusy(null);
  }

  function fail(caught: unknown) {
    setError(caught instanceof Error ? caught.message : 'Scout memory could not be updated.');
  }

  async function setConsent(next: { enabled: boolean; allowInferred: boolean; allowShared: boolean }) {
    if (!sessionToken || !state) return;
    const identity = sessionToken;
    const claim = beginAction('consent', identity);
    if (!claim) return;
    const staleVoiceWasActive = audioFocusRuntime.mode === 'personal_realtime';
    try {
      const response = await api.strideSetRelationshipConsent(sessionToken, {
        action: next.enabled ? 'enable' : 'disable',
        expectedRevision: state.revision,
        allowInferred: next.enabled ? next.allowInferred : false,
        allowShared: next.enabled ? next.allowShared : false,
      });
      if (identity !== sessionTokenRef.current) return;
      const closedStaleVoice = await closeStalePersonalRealtime(staleVoiceWasActive);
      setState((current) => ({
        ...(current ?? response),
        ...response,
        preferences: next.enabled ? current?.preferences ?? [] : [],
      }));
      const success = next.enabled ? 'Scout memory preferences updated.' : 'Scout memory is off and its saved preferences were removed.';
      setMessage(closedStaleVoice ? `${success} Live Scout ended so it cannot retain the previous memory snapshot.` : success);
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (caught) {
      if (identity !== sessionTokenRef.current) return;
      await load();
      fail(caught);
    } finally {
      releaseAction(claim);
    }
  }

  function confirmDisable() {
    Alert.alert(
      'Turn off Scout memory?',
      'This removes every saved preference and its source evidence. Scout will stop using them in one-to-one conversations.',
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Turn off & remove',
          style: 'destructive',
          onPress: () => void setConsent({ enabled: false, allowInferred: false, allowShared: false }),
        },
      ],
    );
  }

  async function remember() {
    if (!sessionToken || !state || !draft.trim()) return;
    const identity = sessionToken;
    const claim = beginAction('remember', identity);
    if (!claim) return;
    const staleVoiceWasActive = audioFocusRuntime.mode === 'personal_realtime';
    try {
      const response = await api.strideRememberRelationship(sessionToken, {
        expectedRevision: state.revision,
        preferenceType,
        value: draft.trim(),
      });
      if (identity !== sessionTokenRef.current) return;
      const closedStaleVoice = await closeStalePersonalRealtime(staleVoiceWasActive);
      setState((current) => ({ ...(current ?? response), ...response, consent: current?.consent }));
      setDraft('');
      setMessage(closedStaleVoice ? 'Saved privately. Live Scout ended so it cannot retain the previous memory snapshot.' : 'Saved privately. You can correct or forget it any time.');
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (caught) {
      if (identity !== sessionTokenRef.current) return;
      await load();
      fail(caught);
    } finally {
      releaseAction(claim);
    }
  }

  async function importMemories() {
    if (!sessionToken || !state || importedMemories.length === 0) return;
    const identity = sessionToken;
    const claim = beginAction('import', identity);
    if (!claim) return;
    const staleVoiceWasActive = audioFocusRuntime.mode === 'personal_realtime';
    try {
		const response = await api.strideImportRelationships(sessionToken, {
			expectedRevision: state.revision,
			entries: importedMemories,
		});
		if (identity !== sessionTokenRef.current) return;
      const closedStaleVoice = await closeStalePersonalRealtime(staleVoiceWasActive);
      setState((current) => ({ ...response, consent: current?.consent ?? response.consent }));
      setImportDraft('');
      setImportOpen(false);
		const imported = response.importedCount ?? importedMemories.length;
		const alreadyPresent = response.alreadyPresentCount ?? 0;
		const success = imported > 0
			? `Imported ${imported} private memor${imported === 1 ? 'y' : 'ies'}${alreadyPresent ? `; ${alreadyPresent} already saved` : ''}. Review, correct, or forget any item below.`
			: `Those ${alreadyPresent || importedMemories.length} memories were already saved. Nothing was duplicated.`;
      setMessage(closedStaleVoice ? `${success} Live Scout ended so it cannot retain the previous memory snapshot.` : success);
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (caught) {
      if (identity !== sessionTokenRef.current) return;
      await load();
      fail(caught);
    } finally {
      releaseAction(claim);
    }
  }

  async function correct(preference: StrideRelationshipPreference) {
    if (!sessionToken || !state || !editValue.trim()) return;
    const identity = sessionToken;
    const claim = beginAction(`correct:${preference.reference.id}`, identity);
    if (!claim) return;
    const staleVoiceWasActive = audioFocusRuntime.mode === 'personal_realtime';
    try {
      const response = await api.strideCorrectRelationship(sessionToken, {
        expectedRevision: state.revision,
        relationshipId: preference.reference.id,
        value: editValue.trim(),
      });
      if (identity !== sessionTokenRef.current) return;
      const closedStaleVoice = await closeStalePersonalRealtime(staleVoiceWasActive);
      setState((current) => ({ ...(current ?? response), ...response, consent: current?.consent }));
      setEditingID(null);
      setEditValue('');
      setMessage(closedStaleVoice ? 'Scout’s memory was corrected. Live Scout ended so it cannot retain the previous value.' : 'Scout’s memory was corrected.');
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch (caught) {
      if (identity !== sessionTokenRef.current) return;
      await load();
      fail(caught);
    } finally {
      releaseAction(claim);
    }
  }

  function confirmForget(preference: StrideRelationshipPreference) {
    Alert.alert('Forget this?', 'The saved value and its source evidence will be removed.', [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Forget',
        style: 'destructive',
        onPress: async () => {
          if (!sessionToken || !state) return;
          const identity = sessionToken;
          const claim = beginAction(`forget:${preference.reference.id}`, identity);
          if (!claim) return;
          const staleVoiceWasActive = audioFocusRuntime.mode === 'personal_realtime';
          try {
            const response = await api.strideForgetRelationship(sessionToken, {
              expectedRevision: state.revision,
              relationshipId: preference.reference.id,
            });
            if (identity !== sessionTokenRef.current) return;
            const closedStaleVoice = await closeStalePersonalRealtime(staleVoiceWasActive);
            setState((current) => ({ ...(current ?? response), ...response, consent: current?.consent }));
            setMessage(closedStaleVoice ? 'Scout forgot that preference. Live Scout ended so it cannot retain the previous value.' : 'Scout forgot that preference and removed its evidence.');
            await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
          } catch (caught) {
            if (identity !== sessionTokenRef.current) return;
            await load();
            fail(caught);
          } finally {
            releaseAction(claim);
          }
        },
      },
    ]);
  }

  return (
    <>
      <Text style={styles.sectionTitle}>Employee memory</Text>
      <View style={[styles.section, shadow[1]]}>
        <View style={styles.headingRow}>
          <View style={styles.headingCopy}>
            <Text style={styles.title}>What STRIDE remembers about me</Text>
            <Text style={styles.description}>
              You stay in control. Scout, Colton, and future coworkers keep you distinct from every other teammate and can use only the private context you approve, with visible sources and controls to correct or remove it.
            </Text>
          </View>
          {availability === 'loading' ? <ActivityIndicator color={colors.accent} /> : null}
        </View>

        <View style={styles.memoryLanes} accessibilityLabel="How STRIDE keeps memory separate">
          <View style={styles.memoryLane}>
            <Text style={styles.memoryLaneTitle}>1:1 private</Text>
            <Text style={styles.noteText}>Imports and approved preferences follow you only into private agent conversations.</Text>
          </View>
          <View style={styles.memoryLane}>
            <Text style={styles.memoryLaneTitle}>Shared work</Text>
            <Text style={styles.noteText}>Public chats and recorded meetings remain author- or speaker-attributed inside their current audience.</Text>
          </View>
          <View style={styles.memoryLane}>
            <Text style={styles.memoryLaneTitle}>Company context</Text>
            <Text style={styles.noteText}>Agents use the evolving company brain through live permissions without blending coworkers together.</Text>
          </View>
        </View>

        {availability === 'unavailable' ? (
          <View style={styles.availabilityNote}>
            <Text style={styles.noteTitle}>Not available in this build</Text>
            <Text style={styles.noteText}>Relationship memory is off at the organization level. Nothing is being learned or stored.</Text>
          </View>
        ) : null}

        {availability === 'signed-out' ? (
          <View style={styles.availabilityNote}>
            <Text style={styles.noteTitle}>Sign in to inspect Scout memory</Text>
            <Text style={styles.noteText}>No relationship-memory controls are available without your authenticated account.</Text>
          </View>
        ) : null}

        {availability === 'error' ? (
          <Pressable accessibilityRole="button" onPress={() => void load()} style={({ pressed }) => [styles.availabilityNote, pressed ? styles.pressed : null]}>
            <Text style={styles.noteTitle}>Couldn’t load memory controls</Text>
            <Text style={styles.noteText}>{error ?? 'Tap to try again.'}</Text>
          </Pressable>
        ) : null}

        {availability === 'available' && !enabled ? (
          <>
            <View style={styles.availabilityNote}>
              <Text style={styles.noteTitle}>Off · nothing saved</Text>
              <Text style={styles.noteText}>Start with explicit, private preferences only. Inferred learning and shared-channel use remain off.</Text>
            </View>
            <Pressable
              accessibilityRole="button"
              disabled={busy !== null}
              onPress={() => void setConsent({ enabled: true, allowInferred: false, allowShared: false })}
              style={({ pressed }) => [styles.primary, pressed ? styles.pressed : null]}
            >
              <Text style={styles.primaryText}>{busy === 'consent' ? 'Turning on…' : 'Turn on private memory'}</Text>
            </Pressable>
          </>
        ) : null}

        {availability === 'available' && enabled ? (
          <>
            <View style={styles.statusRow}>
              <View style={styles.statusCopy}>
                <View style={styles.liveRow}>
                  <View style={styles.liveDot} />
                  <Text style={styles.statusTitle}>On · private by default</Text>
                </View>
                <Text style={styles.noteText}>Saved preferences expire and are never authority, permissions, or company facts.</Text>
              </View>
              <Pressable accessibilityRole="button" disabled={busy !== null} onPress={confirmDisable} style={styles.actionButton}>
                <Text style={styles.dangerText}>Turn off</Text>
              </Pressable>
            </View>

            <Pressable
              accessibilityRole="switch"
			  accessibilityState={{ checked: Boolean(consent?.allowInferred), disabled: true }}
			  disabled
              onPress={() => void setConsent({ enabled: true, allowInferred: !consent?.allowInferred, allowShared: Boolean(consent?.allowShared) })}
              style={({ pressed }) => [styles.toggleRow, pressed ? styles.pressed : null]}
            >
              <View style={styles.toggleCopy}>
				<Text style={styles.toggleLabel}>Learn from repeated patterns · not active yet</Text>
				<Text style={styles.noteText}>No conversation is inferred into a preference in this build.</Text>
              </View>
			  <Switch value={Boolean(consent?.allowInferred)} disabled pointerEvents="none" />
            </Pressable>
            <Pressable
              accessibilityRole="switch"
			  accessibilityState={{ checked: Boolean(consent?.allowShared), disabled: true }}
			  disabled
              onPress={() => void setConsent({ enabled: true, allowInferred: Boolean(consent?.allowInferred), allowShared: !consent?.allowShared })}
              style={({ pressed }) => [styles.toggleRow, pressed ? styles.pressed : null]}
            >
              <View style={styles.toggleCopy}>
				<Text style={styles.toggleLabel}>Shared-channel preferences · not active yet</Text>
				<Text style={styles.noteText}>Channel conversations are not currently turned into preferences by the app.</Text>
              </View>
			  <Switch value={Boolean(consent?.allowShared)} disabled pointerEvents="none" />
            </Pressable>

            <View style={styles.rule} />
            <View style={styles.importCard}>
              <View style={styles.headingCopy}>
                <Text style={styles.subheading}>Import memory to STRIDE</Text>
                <Text style={styles.noteText}>Bring over useful coworker context from another assistant. Every line stays private and separately corrigible.</Text>
              </View>
              <Pressable accessibilityRole="button" disabled={busy !== null} onPress={() => setImportOpen(true)} style={styles.secondaryButton}>
                <Text style={styles.actionText}>Start import</Text>
              </Pressable>
            </View>

            <View style={styles.rule} />
            <Text style={styles.subheading}>Add a private preference</Text>
            <View style={styles.chips}>
              {preferenceTypes.map((option) => {
                const selected = preferenceType === option.id;
                return (
                  <Pressable
                    key={option.id}
                    accessibilityRole="button"
                    accessibilityState={{ selected }}
                    onPress={() => setPreferenceType(option.id)}
                    style={({ pressed }) => [styles.chip, selected ? styles.chipSelected : null, pressed ? styles.pressed : null]}
                  >
                    <Text style={[styles.chipText, selected ? styles.chipTextSelected : null]}>{option.label}</Text>
                  </Pressable>
                );
              })}
            </View>
            <TextInput
              accessibilityLabel="Preference Scout should remember"
              value={draft}
              onChangeText={setDraft}
              placeholder="For example: Lead with the recommendation, then explain why."
              placeholderTextColor={colors.text3}
					  maxLength={STRIDE_MEMORY_IMPORT_MAX_NORMALIZED_BYTES}
              multiline
              style={styles.input}
            />
            <Pressable
              accessibilityRole="button"
              disabled={busy !== null || !draft.trim()}
              onPress={() => void remember()}
              style={({ pressed }) => [styles.primary, !draft.trim() ? styles.disabled : null, pressed ? styles.pressed : null]}
            >
              <Text style={styles.primaryText}>{busy === 'remember' ? 'Saving…' : 'Remember privately'}</Text>
            </Pressable>

            <View style={styles.rule} />
            <View style={styles.memoryHeading}>
              <Text style={styles.subheading}>Saved</Text>
              <Text style={styles.count}>{preferences.length}</Text>
            </View>
            {preferences.length === 0 ? <Text style={styles.empty}>Scout does not have any saved preferences about you.</Text> : null}
            {preferences.map((preference) => {
              const isEditing = editingID === preference.reference.id;
              const source = sourceLabel(preference);
              const paused = preference.scope === 'shared' && !consent?.allowShared
                || preference.origin === 'inferred' && !consent?.allowInferred;
              return (
                <View key={preference.reference.id} style={styles.memoryCard}>
                  <View style={styles.memoryHead}>
                    <Text style={styles.memoryType}>{preferenceLabel(preference.preferenceType)}</Text>
                    <Text style={styles.scope}>{preference.scope === 'shared' ? 'Shared' : 'Private'}{paused ? ' · Paused' : ''}</Text>
                  </View>
                  {isEditing ? (
                    <TextInput
                      accessibilityLabel={`Correct ${preferenceLabel(preference.preferenceType)}`}
                      value={editValue}
                      onChangeText={setEditValue}
                      maxLength={500}
                      multiline
                      autoFocus
                      style={styles.input}
                    />
                  ) : <Text style={styles.memoryValue}>{preference.value}</Text>}
                  <Text style={styles.meta}>{source} · {expiryLabel(preference.expiresAt)}</Text>
                  <View style={styles.actions}>
                    {isEditing ? (
                      <>
						<Pressable accessibilityRole="button" disabled={busy !== null} onPress={() => { setEditingID(null); setEditValue(''); }} style={styles.actionButton}>
                          <Text style={styles.actionText}>Cancel</Text>
                        </Pressable>
                        <Pressable accessibilityRole="button" disabled={busy !== null || !editValue.trim()} onPress={() => void correct(preference)} style={styles.actionButton}>
                          <Text style={styles.actionText}>{busy === `correct:${preference.reference.id}` ? 'Saving…' : 'Save correction'}</Text>
                        </Pressable>
                      </>
                    ) : (
                      <>
						{preference.source?.available && preference.source.threadId && preference.source.messageId ? (
						  <Pressable
							accessibilityRole="button"
							disabled={busy !== null}
							onPress={() => navigation.navigate('Thread', {
							  threadId: preference.source!.threadId!,
							  title: preference.source!.threadTitle || 'Conversation',
							  messageId: preference.source!.messageId!,
							})}
							style={styles.actionButton}
						  >
							<Text style={styles.actionText}>View source</Text>
						  </Pressable>
						) : null}
                        <Pressable accessibilityRole="button" disabled={busy !== null} onPress={() => { setEditingID(preference.reference.id); setEditValue(preference.value); }} style={styles.actionButton}>
                          <Text style={styles.actionText}>Correct</Text>
                        </Pressable>
						<Pressable accessibilityRole="button" disabled={busy !== null} onPress={() => confirmForget(preference)} style={styles.actionButton}>
                          <Text style={styles.dangerText}>Forget</Text>
                        </Pressable>
                      </>
                    )}
                  </View>
                </View>
              );
            })}
          </>
        ) : null}

        {message ? <Text style={styles.success}>{message}</Text> : null}
        {availability === 'available' && error ? <Text style={styles.error}>{error}</Text> : null}
      </View>
      <Modal animationType="slide" presentationStyle="pageSheet" visible={importOpen} onRequestClose={() => { if (busy !== 'import') setImportOpen(false); }}>
        <View style={styles.importScreen}>
          <View style={styles.importHeader}>
            <View style={styles.headingCopy}>
              <Text style={styles.importEyebrow}>PRIVATE COWORKER CONTEXT</Text>
              <Text style={styles.importTitle}>Import memory to STRIDE</Text>
            </View>
            <Pressable accessibilityRole="button" disabled={busy === 'import'} onPress={() => setImportOpen(false)} style={styles.actionButton}>
              <Text style={styles.actionText}>Done</Text>
            </Pressable>
          </View>
          <ScrollView contentContainerStyle={styles.importContent} keyboardShouldPersistTaps="handled">
            <Text style={styles.stepLabel}>1 · Ask your other assistant</Text>
            <View style={styles.promptCard}>
              <Text selectable style={styles.promptText}>{memoryImportPrompt}</Text>
              <Pressable accessibilityRole="button" onPress={() => void Clipboard.setStringAsync(memoryImportPrompt)} style={styles.secondaryButton}>
                <Text style={styles.actionText}>Copy prompt</Text>
              </Pressable>
            </View>
            <Text style={styles.stepLabel}>2 · Review the export here</Text>
            <TextInput
              accessibilityLabel="Memory export to import"
              value={importDraft}
              onChangeText={setImportDraft}
			  editable={busy !== 'import'}
              placeholder="Paste the exported code block…"
              placeholderTextColor={colors.text3}
			  maxLength={STRIDE_MEMORY_IMPORT_MAX_RAW_BYTES}
              multiline
              style={styles.importInput}
            />
			<View style={styles.previewRow}>
			  <Text style={styles.noteTitle}>{importedMemories.length ? `${importedMemories.length} memories ready` : 'No valid memories yet'}</Text>
			  <Text style={styles.noteText}>{parsedImport.errors[0] ?? 'Dated or unknown-date entries under the five named categories will be saved. Wrapped paragraphs are kept together.'}</Text>
			</View>
			{busy !== 'import' && error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
			{busy === 'import' ? (
			  <View accessibilityRole="progressbar" accessibilityLabel="Memory is updating" style={styles.importProgress}>
				<ActivityIndicator color={colors.accent} />
				<View style={styles.headingCopy}>
				  <Text style={styles.noteTitle}>Memory is updating</Text>
				  <Text style={styles.noteText}>Securing {importedMemories.length} private memories in one durable update. Keep STRIDE open until this finishes.</Text>
				</View>
			  </View>
			) : null}
			<Pressable
			  accessibilityRole="button"
			  disabled={busy !== null || importedMemories.length === 0 || parsedImport.errors.length > 0}
              onPress={() => void importMemories()}
			  style={({ pressed }) => [styles.primary, importedMemories.length === 0 || parsedImport.errors.length > 0 ? styles.disabled : null, pressed ? styles.pressed : null]}
            >
			  <Text style={styles.primaryText}>{busy === 'import' ? 'Memory is updating…' : `Import ${importedMemories.length || ''} privately`.trim()}</Text>
            </Pressable>
          </ScrollView>
        </View>
      </Modal>
    </>
  );
}

const styles = StyleSheet.create({
  sectionTitle: { ...type.label, color: colors.text3, textTransform: 'uppercase', marginTop: space[4], marginBottom: space[2] },
  section: { backgroundColor: colors.surface1, borderRadius: radius.xl, borderCurve: 'continuous', padding: space[4], gap: space[3], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  headingRow: { flexDirection: 'row', alignItems: 'flex-start', gap: space[3] },
  headingCopy: { flex: 1, gap: 4 },
  title: { ...type.bodyMedium, color: colors.text1 },
  description: { ...type.caption, color: colors.text2, lineHeight: 18 },
  memoryLanes: { gap: space[2] },
  memoryLane: { borderRadius: radius.md, borderCurve: 'continuous', backgroundColor: colors.surface2, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, padding: space[3], gap: 3 },
  memoryLaneTitle: { ...type.captionMedium, color: colors.text1 },
  availabilityNote: { minHeight: hitMin, borderRadius: radius.md, borderCurve: 'continuous', backgroundColor: colors.surface3, padding: space[3], gap: 3 },
  noteTitle: { ...type.bodyMedium, color: colors.text1 },
  noteText: { ...type.caption, color: colors.text2, lineHeight: 18 },
  primary: { minHeight: hitMin, borderRadius: radius.md, borderCurve: 'continuous', backgroundColor: colors.accent, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[4] },
  primaryText: { ...type.button, color: colors.onAccent },
  disabled: { opacity: 0.45 },
  pressed: { transform: [{ scale: 0.96 }], opacity: 0.88 },
  statusRow: { flexDirection: 'row', alignItems: 'center', gap: space[3] },
  statusCopy: { flex: 1, gap: 4 },
  liveRow: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  liveDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: colors.live },
  statusTitle: { ...type.bodyMedium, color: colors.text1 },
  toggleRow: { minHeight: 54, flexDirection: 'row', alignItems: 'center', gap: space[3] },
  toggleCopy: { flex: 1, gap: 2 },
  toggleLabel: { ...type.bodyMedium, color: colors.text1 },
  rule: { height: StyleSheet.hairlineWidth, backgroundColor: colors.line1, marginVertical: space[1] },
  subheading: { ...type.bodyMedium, color: colors.text1 },
  chips: { flexDirection: 'row', flexWrap: 'wrap', gap: space[2] },
  chip: { minHeight: hitMin, justifyContent: 'center', paddingHorizontal: space[3], borderRadius: radius.full, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, backgroundColor: colors.surface2 },
  chipSelected: { backgroundColor: colors.accent, borderColor: colors.accent },
  chipText: { ...type.captionMedium, color: colors.text2 },
  chipTextSelected: { color: colors.onAccent },
  input: { minHeight: 76, borderRadius: radius.md, borderCurve: 'continuous', paddingHorizontal: space[3], paddingVertical: space[3], backgroundColor: colors.surface3, color: colors.text1, fontSize: 15, textAlignVertical: 'top' },
  memoryHeading: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  count: { ...type.captionMedium, color: colors.text3, fontVariant: ['tabular-nums'] },
  empty: { ...type.caption, color: colors.text2, paddingVertical: space[2] },
  memoryCard: { borderRadius: radius.lg, borderCurve: 'continuous', backgroundColor: colors.surface2, padding: space[3], gap: space[2], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  memoryHead: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[2] },
  memoryType: { ...type.captionMedium, color: colors.text2 },
  scope: { ...type.label, color: colors.text3, textTransform: 'uppercase' },
  memoryValue: { ...type.bodySm, color: colors.text1, lineHeight: 20 },
  meta: { ...type.caption, color: colors.text3, lineHeight: 17 },
  actions: { minHeight: hitMin, flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'flex-end', gap: space[2] },
  actionButton: { minHeight: hitMin, justifyContent: 'center', paddingHorizontal: space[2] },
  actionText: { ...type.captionMedium, color: colors.text1 },
  dangerText: { ...type.captionMedium, color: colors.danger },
  success: { ...type.caption, color: colors.live },
  error: { ...type.caption, color: colors.danger },
  importCard: { borderRadius: radius.lg, borderCurve: 'continuous', backgroundColor: colors.surface2, padding: space[3], gap: space[3], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  secondaryButton: { minHeight: hitMin, alignSelf: 'flex-start', justifyContent: 'center', paddingHorizontal: space[3], borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, backgroundColor: colors.surface3 },
  importScreen: { flex: 1, backgroundColor: colors.bg },
  importHeader: { flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[4], paddingTop: space[5], paddingBottom: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  importEyebrow: { ...type.label, color: colors.accent, letterSpacing: 0.8 },
  importTitle: { ...type.title2, color: colors.text1 },
  importContent: { padding: space[4], paddingBottom: space[8], gap: space[3] },
  stepLabel: { ...type.label, color: colors.text3, textTransform: 'uppercase', marginTop: space[2] },
  promptCard: { borderRadius: radius.lg, borderCurve: 'continuous', backgroundColor: colors.surface2, padding: space[3], gap: space[3], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1 },
  promptText: { ...type.caption, color: colors.text2, lineHeight: 19 },
  importInput: { minHeight: 220, borderRadius: radius.lg, borderCurve: 'continuous', padding: space[3], backgroundColor: colors.surface2, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, color: colors.text1, fontSize: 14, lineHeight: 20, textAlignVertical: 'top' },
  previewRow: { borderRadius: radius.md, backgroundColor: colors.surface3, padding: space[3], gap: 4 },
  importProgress: { minHeight: 68, flexDirection: 'row', alignItems: 'center', gap: space[3], borderRadius: radius.lg, borderCurve: 'continuous', padding: space[3], backgroundColor: colors.surface2, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.accent },
});
