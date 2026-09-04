import React, { useCallback, useEffect, useRef, useState } from 'react';
import { ActivityIndicator, FlatList, Keyboard, Modal, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';
import { useIsFocused } from '@react-navigation/native';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { SafeAreaView } from 'react-native-safe-area-context';
import { api } from '../api/client';
import type { MemoryInspectItem } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { Screen } from '../components/Screen';
import { homeScoutOpeningAttempt, submitHomeScoutOpening, type HomeScoutOpeningAttempt } from '../canvas/homeScoutOpening';
import { memoryKinds, memoryKindLabel, memoryQuestion, memorySourceTarget } from '../memory/memoryInspectorModel';
import { useMemoryInspector } from '../memory/useMemoryInspector';
import type { RootStackParamList } from '../navigation/types';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { colors, radius, space, type } from '../theme/tokens';

type Props = NativeStackScreenProps<RootStackParamList, 'Memory'>;
export function MemoryInspectorScreen({ navigation }: Props) {
  const { sessionToken } = useAuth();
  const focused = useIsFocused();
  const office = useOfficeEvents();
  const [query, setQuery] = useState('');
  const [subject, setSubject] = useState('');
  const [kind, setKind] = useState('');
  const [person, setPerson] = useState('');
  const [selectedId, setSelectedId] = useState('');
  const [question, setQuestion] = useState('');
  const [correction, setCorrection] = useState('');
  const [correcting, setCorrecting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState('');
  const attemptRef = useRef<HomeScoutOpeningAttempt | null>(null);
  const busyRef = useRef(false);
  const sessionRef = useRef(sessionToken);
  sessionRef.current = sessionToken;
  const data = useMemoryInspector(sessionToken, focused, subject, kind, person, office.version);
  const selected = data.items.find((item) => item.id === selectedId) ?? null;
  useEffect(() => { const timer = setTimeout(() => setSubject(query.trim()), 250); return () => clearTimeout(timer); }, [query]);
  useEffect(() => { setSelectedId(''); setQuestion(''); setCorrection(''); setActionError(''); attemptRef.current = null; }, [sessionToken]);
  const open = useCallback((item: MemoryInspectItem) => {
    setSelectedId(item.id); setCorrecting(false); setCorrection(''); setQuestion(''); setActionError('');
  }, []);
  const ask = async () => {
    if (!sessionToken || busyRef.current) return;
    const attempt = homeScoutOpeningAttempt(attemptRef.current, memoryQuestion(question, selected));
    if (!attempt) return;
    attemptRef.current = attempt; busyRef.current = true; setBusy(true); setActionError('');
    const result = await submitHomeScoutOpening(attempt, { createThread: (body, key) => api.createScoutThread(sessionToken, body, key) });
    busyRef.current = false;
    if (sessionRef.current !== sessionToken) return;
    setBusy(false);
    if (result.accepted) {
      attemptRef.current = null; setQuestion(''); setSelectedId(''); Keyboard.dismiss();
      navigation.navigate('Thread', { threadId: result.thread.threadId, title: result.thread.title });
    } else setActionError('Scout could not open the conversation. Your question is still here; retry when connected.');
  };
  const correct = async () => {
    if (!sessionToken || !selected || busyRef.current || !correction.trim()) return;
    busyRef.current = true; setBusy(true); setActionError('');
    try {
      await api.correctMemory(sessionToken, selected.id, correction.trim());
      if (sessionRef.current !== sessionToken) return;
      setCorrecting(false); setCorrection(''); await data.refresh();
    } catch (error) {
      if (sessionRef.current === sessionToken) setActionError(error instanceof Error ? error.message : 'The correction could not be saved.');
    } finally { busyRef.current = false; if (sessionRef.current === sessionToken) setBusy(false); }
  };
  const source = (item: MemoryInspectItem, entry: MemoryInspectItem['provenance'][number], index: number) => {
    if (entry.type === 'message' && item.provenance.some((value) => value.type === 'thread')) return null;
    const target = memorySourceTarget(item, entry);
    if (!target) return <Text key={index} selectable style={styles.meta}>{entry.label ? `${entry.label} · ` : ''}{entry.type}: {entry.id}</Text>;
    const label = target.kind === 'meeting' ? 'Open meeting record' : target.kind === 'thread' ? (target.messageId ? 'Open source message' : 'Open conversation') : target.kind === 'person' ? `More from ${entry.id}` : 'Open related decision';
    return <Pressable key={index} accessibilityRole="button" style={styles.action} onPress={() => {
      if (target.kind === 'decision') {
        const next = data.items.find((value) => value.id === target.id);
        if (next) open(next); else setActionError('That decision is outside the current results. Clear the search and filters to look for it.');
        return;
      }
      setSelectedId('');
      if (target.kind === 'meeting') navigation.navigate('Meetings', { meetingId: target.id });
      else if (target.kind === 'thread') navigation.navigate('Thread', { threadId: target.id, title: item.title, messageId: target.messageId });
      else setPerson(target.id);
    }}><Text style={styles.actionText}>{label}</Text></Pressable>;
  };
  const questionComposer = <View style={styles.section}>
    <Text accessibilityRole="header" style={styles.sectionTitle}>{selected ? 'Ask about this record' : 'Ask company memory'}</Text>
    <TextInput accessibilityLabel="Question for company memory" value={question} onChangeText={setQuestion} multiline maxLength={4000} editable={!busy}
      placeholder="What did we decide, and why?" placeholderTextColor={colors.text3} style={styles.input} />
    <Text style={styles.meta}>Opens a private Scout conversation asking for original sources and gaps in the evidence.</Text>
    <Pressable accessibilityRole="button" accessibilityLabel="Ask Scout using company memory" disabled={busy || !question.trim()} accessibilityState={{ disabled: busy || !question.trim(), busy }} onPress={() => { void ask(); }} style={[styles.action, (!question.trim() || busy) && styles.dim]}>
      <Text style={styles.actionText}>{busy ? 'Opening…' : 'Ask Scout →'}</Text>
    </Pressable>
  </View>;
  return <Screen title="Company memory" subtitle="What is remembered, where it came from, and what needs correcting." scroll={false}>
    <FlatList data={data.items} keyExtractor={(item) => item.id} keyboardShouldPersistTaps="handled" keyboardDismissMode="on-drag"
      refreshing={data.loading} onRefresh={() => { void data.refresh(); }} contentContainerStyle={styles.list}
      ListHeaderComponent={<View style={styles.header}>
        <TextInput accessibilityLabel="Search company memory" value={query} onChangeText={setQuery} placeholder="Search decisions, people, or subjects" placeholderTextColor={colors.text3} returnKeyType="search" style={styles.search} />
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.filters}>{memoryKinds.map((filter) => <Pressable key={filter.id} accessibilityRole="button" accessibilityState={{ selected: kind === filter.id }} onPress={() => setKind(filter.id)} style={[styles.filter, kind === filter.id && styles.filterSelected]}><Text style={styles.actionText}>{filter.label}</Text></Pressable>)}</ScrollView>
        {person ? <Pressable accessibilityRole="button" onPress={() => setPerson('')}><Text style={styles.actionText}>From {person} · Clear filter</Text></Pressable> : null}
        {questionComposer}
        <View style={styles.section}>
          <Text accessibilityRole="header" style={styles.sectionTitle}>Remembered records</Text>
          <Text style={styles.meta}>Showing the latest matching records you can access. Open a record to inspect its sources.</Text>
        </View>
        {data.loading ? <ActivityIndicator accessibilityLabel="Loading memory" color={colors.emberText} /> : null}
        {data.error || actionError ? <Pressable accessibilityRole="button" onPress={() => { void data.refresh(); }}><Text style={styles.error}>{data.error || actionError}</Text><Text style={styles.actionText}>Try again</Text></Pressable> : null}
      </View>}
      ListEmptyComponent={!data.loading && !data.error ? <Text style={styles.empty}>{subject || kind || person ? 'No records match these filters. Try another subject or clear a filter.' : 'No remembered records are available yet. Meeting records and conversations remain available in their own spaces.'}</Text> : null}
      renderItem={({ item }) => <Pressable accessibilityRole="button" accessibilityLabel={`Open memory record. ${item.title}`} onPress={() => open(item)} style={styles.card}>
        <Text style={styles.meta}>{memoryKindLabel(item.kind)} · {item.status}</Text>
        <Text style={styles.cardTitle}>{item.title}</Text>
        {item.summary !== item.title ? <Text numberOfLines={3} style={styles.body}>{item.summary}</Text> : null}
        <Text style={styles.meta}>{item.person ? `${item.person} · ` : ''}{new Date(item.at).toLocaleDateString()} · {item.provenance.length} source references</Text>
      </Pressable>} />
    <Modal visible={Boolean(selected)} presentationStyle="formSheet" animationType="slide" onRequestClose={() => setSelectedId('')} allowSwipeDismissal>
      <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
        <View style={styles.detailHeader}><Text style={styles.sectionTitle}>Memory record</Text><Pressable accessibilityRole="button" accessibilityLabel="Close memory record" onPress={() => setSelectedId('')} style={styles.action}><Text style={styles.actionText}>Done</Text></Pressable></View>
        <ScrollView contentContainerStyle={styles.detail} keyboardShouldPersistTaps="handled" keyboardDismissMode="on-drag">
          {selected ? <>
            <Text style={styles.meta}>{memoryKindLabel(selected.kind)} · {selected.status}</Text>
            <Text accessibilityRole="header" style={styles.title}>{selected.title}</Text>
            <Text selectable style={styles.body}>{selected.summary}</Text>
            <Text style={styles.meta}>{selected.person} · {new Date(selected.at).toLocaleString()}</Text>
            <View style={styles.section}><Text accessibilityRole="header" style={styles.sectionTitle}>Sources</Text>{selected.provenance.length ? selected.provenance.map((entry, index) => source(selected, entry, index)) : <Text style={styles.body}>No source references are attached to this record.</Text>}</View>
            {questionComposer}
            {actionError ? <Text accessibilityRole="alert" style={styles.error}>{actionError}</Text> : null}
            {selected.status !== 'forgotten' ? <View style={styles.section}>
              <Text accessibilityRole="header" style={styles.sectionTitle}>Correct the record</Text>
              <Text style={styles.meta}>A correction is recorded with your identity. Original provenance remains visible.</Text>
              {correcting ? <><TextInput accessibilityLabel="Memory correction" multiline value={correction} onChangeText={setCorrection} editable={!busy} maxLength={4000} style={styles.input} placeholder="What should this record say?" placeholderTextColor={colors.text3} /><Pressable accessibilityRole="button" disabled={busy || !correction.trim()} onPress={() => { void correct(); }} style={[styles.action, (!correction.trim() || busy) && styles.dim]}><Text style={styles.actionText}>Save correction</Text></Pressable></> : <Pressable accessibilityRole="button" onPress={() => setCorrecting(true)} style={styles.action}><Text style={styles.actionText}>Add a correction</Text></Pressable>}
            </View> : null}
          </> : null}
        </ScrollView>
      </SafeAreaView>
    </Modal>
  </Screen>;
}
const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  list: { paddingBottom: 120, width: '100%', maxWidth: 1024, alignSelf: 'center' },
  header: { gap: space[4], marginBottom: space[4] },
  search: { ...type.body, minHeight: 48, paddingHorizontal: space[3], borderRadius: radius.lg, backgroundColor: colors.surface1, color: colors.text1 },
  filters: { gap: space[2] },
  filter: { paddingHorizontal: space[3], paddingVertical: space[2], minHeight: 44, justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.surface1 },
  filterSelected: { backgroundColor: colors.emberSoft },
  section: { gap: space[2], paddingTop: space[3], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  sectionTitle: { ...type.title2, color: colors.text1 },
  title: { ...type.title1, color: colors.text1 },
  card: { gap: space[2], padding: space[4], marginBottom: space[3], borderRadius: radius.xl, backgroundColor: colors.surface1 },
  cardTitle: { ...type.bodyMedium, color: colors.text1 },
  body: { ...type.body, color: colors.text2 },
  meta: { ...type.caption, color: colors.text2 },
  empty: { ...type.body, color: colors.text2, paddingVertical: space[5] },
  action: { minHeight: 44, paddingHorizontal: space[3], paddingVertical: space[2], justifyContent: 'center', borderRadius: radius.lg, backgroundColor: colors.surface2 },
  actionText: { ...type.captionMedium, color: colors.text1 },
  input: { ...type.body, minHeight: 88, padding: space[3], textAlignVertical: 'top', borderRadius: radius.lg, backgroundColor: colors.surface1, color: colors.text1 },
  error: { ...type.caption, color: colors.danger },
  detailHeader: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', padding: space[4] },
  detail: { padding: space[5], paddingBottom: space[10], gap: space[4] },
  dim: { opacity: 0.5 },
});
