import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Pressable,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from 'react-native';
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

type MemoryNavigation = NativeStackNavigationProp<RootStackParamList>;

const preferenceTypes = [
  { id: 'response_length', label: 'Response length' },
  { id: 'communication_format', label: 'Format' },
  { id: 'feedback_style', label: 'Feedback' },
  { id: 'decision_detail', label: 'Decision detail' },
  { id: 'meeting_pace', label: 'Meeting pace' },
] as const;

const expiryFormatter = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
});

function preferenceLabel(id: string) {
  return preferenceTypes.find((option) => option.id === id)?.label ?? id.replaceAll('_', ' ');
}

function sourceLabel(preference: StrideRelationshipPreference) {
	const provenance = preference.source;
	if (provenance?.kind === 'settings') return provenance.label || 'Added by you in Settings';
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
      <Text style={styles.sectionTitle}>Scout memory</Text>
      <View style={[styles.section, shadow[1]]}>
        <View style={styles.headingRow}>
          <View style={styles.headingCopy}>
            <Text style={styles.title}>What Scout remembers about me</Text>
            <Text style={styles.description}>
              You stay in control. Scout only saves a preference after consent, shows where it came from, and lets you correct or remove it.
            </Text>
          </View>
          {availability === 'loading' ? <ActivityIndicator color={colors.accent} /> : null}
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
              maxLength={500}
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
});
