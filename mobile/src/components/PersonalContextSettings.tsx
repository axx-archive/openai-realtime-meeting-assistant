import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import * as Clipboard from 'expo-clipboard';
import * as Haptics from 'expo-haptics';
import { api, BonfireApiError } from '../api/client';
import type { StridePersonalContextSource } from '../api/types';
import { colors, radius, shadow, space, type } from '../theme/tokens';

type Props = { sessionToken: string | null };

function opaqueID(prefix: string): string {
  const uuid = globalThis.crypto?.randomUUID?.();
  if (uuid) return `${prefix}_${uuid.replaceAll('-', '_')}`;
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2, 14)}`;
}

export function PersonalContextSettings({ sessionToken }: Props) {
  const epoch = useRef(0);
  const tokenRef = useRef(sessionToken);
  tokenRef.current = sessionToken;
  const [status, setStatus] = useState<'loading' | 'available' | 'unavailable' | 'error' | 'signed-out'>(sessionToken ? 'loading' : 'signed-out');
  const [sources, setSources] = useState<StridePersonalContextSource[]>([]);
  const [draft, setDraft] = useState('');
  const [kind, setKind] = useState<'preference' | 'reflection'>('preference');
  const [editing, setEditing] = useState<string | null>(null);
  const [editBody, setEditBody] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState('');

  const load = useCallback(async () => {
    const current = ++epoch.current;
    const token = sessionToken;
    setMessage('');
    if (!token) {
      setSources([]);
      setStatus('signed-out');
      return;
    }
    setStatus('loading');
    try {
      const response = await api.stridePersonalContextSources(token);
      if (current !== epoch.current || token !== tokenRef.current) return;
      setSources(response);
      setStatus('available');
    } catch (caught) {
      if (current !== epoch.current || token !== tokenRef.current) return;
      setSources([]);
      setStatus(caught instanceof BonfireApiError && caught.status === 503 ? 'unavailable' : 'error');
    }
  }, [sessionToken]);

  useEffect(() => { void load(); }, [load]);

  async function addSource() {
    const body = draft.trim();
    const token = sessionToken;
    if (!token || !body || busy) return;
    const identity = token;
    setBusy('add');
    setMessage('');
    try {
      const source = await api.stridePutPersonalContext(token, {
        idempotencyKey: opaqueID('context_add'),
        sourceId: opaqueID('context'),
        kind,
        body,
        expectedRevision: 0,
      });
      if (identity !== tokenRef.current) return;
      setSources((current) => [...current, source].sort((a, b) => a.sourceId.localeCompare(b.sourceId)));
      setDraft('');
      setMessage('Saved privately.');
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    } catch {
      if (identity === tokenRef.current) setMessage('Personal context could not be saved. Reload and try again.');
    } finally {
      if (identity === tokenRef.current) setBusy(null);
    }
  }

  async function correct(source: StridePersonalContextSource) {
    const body = editBody.trim();
    const token = sessionToken;
    if (!token || !body || busy) return;
    const identity = token;
    setBusy(source.sourceId);
    setMessage('');
    try {
      const updated = await api.strideCorrectPersonalContext(token, source.sourceId, {
        idempotencyKey: opaqueID('context_correct'), body, expectedRevision: source.revision,
      });
      if (identity !== tokenRef.current) return;
      setSources((current) => current.map((item) => item.sourceId === updated.sourceId ? updated : item));
      setEditing(null);
      setEditBody('');
      setMessage('Correction saved privately.');
    } catch {
      if (identity === tokenRef.current) {
        setMessage('That context changed before your correction. Reload to see the current version.');
        void load();
      }
    } finally {
      if (identity === tokenRef.current) setBusy(null);
    }
  }

  function confirmForget(source: StridePersonalContextSource) {
    Alert.alert('Forget this personal context?', 'This permanently destroys the source encryption key. It cannot be recovered from an older backup.', [
      { text: 'Cancel', style: 'cancel' },
      { text: 'Forget permanently', style: 'destructive', onPress: () => void forget(source) },
    ]);
  }

  async function forget(source: StridePersonalContextSource) {
    const token = sessionToken;
    if (!token || busy) return;
    const identity = token;
    setBusy(source.sourceId);
    setMessage('');
    try {
      await api.strideForgetPersonalContext(token, source.sourceId, {
        idempotencyKey: opaqueID('context_forget'), expectedRevision: source.revision,
      });
      if (identity !== tokenRef.current) return;
      setSources((current) => current.filter((item) => item.sourceId !== source.sourceId));
      setMessage('Context forgotten and its source key destroyed.');
    } catch {
      if (identity === tokenRef.current) {
        setMessage('The forget operation was not confirmed. Reload before trying another change.');
        void load();
      }
    } finally {
      if (identity === tokenRef.current) setBusy(null);
    }
  }

  async function exportContext() {
    const token = sessionToken;
    if (!token || busy) return;
    const identity = token;
    setBusy('export');
    try {
      const value = await api.strideExportPersonalContext(token);
      if (identity !== tokenRef.current) return;
      await Clipboard.setStringAsync(JSON.stringify(value, null, 2));
      setMessage('A private copy was placed on your clipboard.');
    } catch {
      if (identity === tokenRef.current) setMessage('The private export could not be created.');
    } finally {
      if (identity === tokenRef.current) setBusy(null);
    }
  }

  return (
    <>
      <Text style={styles.sectionTitle}>Personal context</Text>
      <View style={[styles.section, shadow[1]]}>
        <View style={styles.headingRow}>
          <View style={styles.headingCopy}>
            <Text style={styles.title}>Private context you control</Text>
            <Text style={styles.body}>Only context you add here is stored. Each source is encrypted separately and can be corrected, exported, or permanently forgotten.</Text>
          </View>
          {status === 'loading' ? <ActivityIndicator color={colors.text2} /> : null}
        </View>
        {status === 'unavailable' ? <Text style={styles.notice}>Private custody is not active for this release. Nothing entered here is stored.</Text> : null}
        {status === 'error' ? (
          <Pressable accessibilityRole="button" onPress={() => void load()} style={styles.secondary}><Text style={styles.secondaryText}>Reload personal context</Text></Pressable>
        ) : null}
        {status === 'available' ? (
          <>
            <View style={styles.kindRow}>
              {(['preference', 'reflection'] as const).map((option) => (
                <Pressable key={option} accessibilityRole="radio" accessibilityState={{ checked: kind === option }} onPress={() => setKind(option)} style={[styles.chip, kind === option && styles.chipActive]}>
                  <Text style={[styles.chipText, kind === option && styles.chipTextActive]}>{option === 'preference' ? 'Preference' : 'Reflection'}</Text>
                </Pressable>
              ))}
            </View>
            <TextInput
              accessibilityLabel="New personal context"
              multiline
              maxLength={16_384}
              onChangeText={setDraft}
              placeholder="Add context that helps a trusted coworker work well with you."
              placeholderTextColor={colors.text3}
              style={styles.input}
              value={draft}
            />
            <Pressable accessibilityRole="button" disabled={!draft.trim() || Boolean(busy)} onPress={() => void addSource()} style={({ pressed }) => [styles.primary, pressed && styles.pressed]}>
              <Text style={styles.primaryText}>{busy === 'add' ? 'Saving…' : 'Save privately'}</Text>
            </Pressable>
            {sources.length === 0 ? <Text style={styles.empty}>No personal context is stored.</Text> : null}
            {sources.map((source) => (
              <View key={source.sourceId} style={styles.source}>
                <Text style={styles.sourceKind}>{source.kind}</Text>
                {editing === source.sourceId ? (
                  <TextInput accessibilityLabel="Correct personal context" multiline maxLength={16_384} onChangeText={setEditBody} style={styles.input} value={editBody} />
                ) : <Text style={styles.sourceBody}>{source.body}</Text>}
                <View style={styles.actions}>
                  {editing === source.sourceId ? (
                    <Pressable accessibilityRole="button" onPress={() => void correct(source)}><Text style={styles.action}>Save correction</Text></Pressable>
                  ) : (
                    <Pressable accessibilityRole="button" onPress={() => { setEditing(source.sourceId); setEditBody(source.body); }}><Text style={styles.action}>Correct</Text></Pressable>
                  )}
                  <Pressable accessibilityRole="button" onPress={() => confirmForget(source)}><Text style={styles.destructive}>Forget</Text></Pressable>
                </View>
              </View>
            ))}
            <Pressable accessibilityRole="button" disabled={Boolean(busy)} onPress={() => void exportContext()} style={styles.secondary}>
              <Text style={styles.secondaryText}>{busy === 'export' ? 'Creating copy…' : 'Copy private export'}</Text>
            </Pressable>
          </>
        ) : null}
        {message ? <Text accessibilityLiveRegion="polite" style={styles.message}>{message}</Text> : null}
      </View>
    </>
  );
}

const styles = StyleSheet.create({
  sectionTitle: { ...type.caption, color: colors.text2, marginTop: space[5], marginBottom: space[2], textTransform: 'uppercase', letterSpacing: 0.8 },
  section: { backgroundColor: colors.surface1, borderRadius: radius.lg, padding: space[4], gap: space[3] },
  headingRow: { flexDirection: 'row', alignItems: 'flex-start', gap: space[3] },
  headingCopy: { flex: 1, gap: space[1] },
  title: { ...type.bodyMedium, color: colors.text1 },
  body: { ...type.caption, color: colors.text2, lineHeight: 19 },
  notice: { ...type.caption, color: colors.text2, backgroundColor: colors.surface2, borderRadius: radius.md, padding: space[3] },
  kindRow: { flexDirection: 'row', gap: space[2] },
  chip: { borderRadius: 999, paddingHorizontal: space[3], paddingVertical: space[2], backgroundColor: colors.surface2 },
  chipActive: { backgroundColor: colors.text1 },
  chipText: { ...type.caption, color: colors.text2 },
  chipTextActive: { color: colors.bg },
  input: { minHeight: 92, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, borderRadius: radius.md, padding: space[3], color: colors.text1, backgroundColor: colors.surface2, textAlignVertical: 'top' },
  primary: { minHeight: 46, borderRadius: radius.md, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.text1 },
  primaryText: { ...type.button, color: colors.bg },
  secondary: { minHeight: 44, borderRadius: radius.md, alignItems: 'center', justifyContent: 'center', borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2 },
  secondaryText: { ...type.button, color: colors.text1 },
  empty: { ...type.caption, color: colors.text3, paddingVertical: space[2] },
  source: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1, paddingTop: space[3], gap: space[2] },
  sourceKind: { ...type.caption, color: colors.text3, textTransform: 'uppercase', letterSpacing: 0.7 },
  sourceBody: { ...type.body, color: colors.text1 },
  actions: { flexDirection: 'row', gap: space[4] },
  action: { ...type.caption, color: colors.text1 },
  destructive: { ...type.caption, color: colors.danger },
  message: { ...type.caption, color: colors.text2 },
  pressed: { opacity: 0.75 },
});
