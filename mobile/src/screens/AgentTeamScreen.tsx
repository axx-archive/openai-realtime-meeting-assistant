import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Modal,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';

import { api, BonfireApiError } from '../api/client';
import type {
  StrideMarketplaceListing,
  StrideMarketplaceResponse,
  StridePrivateAgentTemplateInput,
  StrideRosterResponse,
  StrideRuntimeStatusResponse,
  StrideTeamSeat,
  StrideWorkResponse,
  StrideWorkSuggestion,
  ScoutThread,
} from '../api/types';
import { useAuth } from '../auth/AuthContext';
import type { RootStackParamList } from '../navigation/types';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

type Props = NativeStackScreenProps<RootStackParamList, 'AgentTeam'>;
type Segment = 'team' | 'marketplace' | 'work';
type AgentRecord = StrideTeamSeat | StrideMarketplaceListing;
type SurfaceRecord = AgentRecord | StrideWorkSuggestion;

function words(value: unknown, fallback = ''): string {
  const text = String(value ?? '').trim().replace(/[_-]+/g, ' ');
  return text || fallback;
}

function initials(name: string): string {
  return name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]?.toUpperCase() ?? '').join('') || 'A';
}

export function AgentTeamScreen({ navigation }: Props) {
  const { sessionToken } = useAuth();
  const [segment, setSegment] = useState<Segment>('team');
  const [status, setStatus] = useState<StrideRuntimeStatusResponse | null>(null);
  const [roster, setRoster] = useState<StrideRosterResponse | null>(null);
  const [marketplace, setMarketplace] = useState<StrideMarketplaceResponse | null>(null);
  const [work, setWork] = useState<StrideWorkResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [threads, setThreads] = useState<ScoutThread[]>([]);
  const [destinationWork, setDestinationWork] = useState<StrideWorkSuggestion | null>(null);
  const [detailRecord, setDetailRecord] = useState<AgentRecord | null>(null);
  const [templateOpen, setTemplateOpen] = useState(false);

  const load = useCallback(async (refresh = false) => {
    if (!sessionToken) return;
    refresh ? setRefreshing(true) : setLoading(true);
    try {
      const [nextStatus, nextRoster, nextMarketplace, nextWork, nextThreads] = await Promise.all([
        api.strideStatus(sessionToken),
        api.strideRoster(sessionToken),
        api.strideMarketplace(sessionToken),
        api.strideWork(sessionToken),
        api.scoutThreads(sessionToken),
      ]);
      setStatus(nextStatus);
      setRoster(nextRoster);
      setMarketplace(nextMarketplace);
      setWork(nextWork);
      setThreads(nextThreads.threads ?? []);
      setError(null);
    } catch (cause) {
      setError(cause instanceof BonfireApiError ? cause.message : 'The agent runtime could not be read.');
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [sessionToken]);

  useEffect(() => { void load(); }, [load]);

  const run = useCallback(async (key: string, action: () => Promise<unknown>) => {
    if (!sessionToken || busyKey) return;
    setBusyKey(key);
    try {
      await action();
      await load(true);
    } catch (cause) {
      Alert.alert('Couldn’t complete that', cause instanceof Error ? cause.message : 'Please try again.');
    } finally {
      setBusyKey(null);
    }
  }, [busyKey, load, sessionToken]);

  const records = useMemo<SurfaceRecord[]>(() => {
    if (segment === 'team') return roster?.seats ?? [];
    if (segment === 'marketplace') return marketplace?.listings ?? [];
    return work?.suggestions ?? [];
  }, [marketplace?.listings, roster?.seats, segment, work?.suggestions]);
  const reason = segment === 'team' ? roster?.reason : segment === 'marketplace' ? marketplace?.reason : work?.reason;
  const runtimeState = words(status?.runtime?.state, error ? 'unavailable' : 'checking');
  const projectThreads = useMemo(() => threads.filter((thread) => {
    const title = words(thread.title).toLowerCase().replace(/^#/, '');
    return !thread.archived && !thread.archivedAt && thread.visibility === 'public' && thread.table !== true && title !== 'team' && title !== 'general';
  }), [threads]);

  const header = (
    <>
      <View style={styles.hero}>
        <Text style={styles.eyebrow}>YOUR AGENT TEAM</Text>
        <Text style={styles.title}>Coworkers with names, context, and accountable jobs.</Text>
        <Text style={styles.intro}>Scout remains your chief-of-staff front door. People approve every hire and work outcome; provider execution stays fenced until E10.</Text>
        <View accessibilityRole="text" accessibilityLabel={`Runtime ${runtimeState}, activation fenced`} style={styles.statusPill}>
          <View style={[styles.statusDot, runtimeState === 'standby' && styles.statusDotStandby, runtimeState === 'unavailable' && styles.statusDotError]} />
          <Text style={styles.statusText}>{runtimeState} · activation fenced</Text>
        </View>
      </View>
      <View style={styles.segmented} accessibilityRole="tablist">
        {([['team', 'Team'], ['marketplace', 'Marketplace'], ['work', 'Work']] as const).map(([id, label]) => {
          const selected = segment === id;
          return (
            <Pressable key={id} accessibilityRole="tab" accessibilityState={{ selected }} onPress={() => setSegment(id)} style={({ pressed }) => [styles.segment, selected && styles.segmentSelected, pressed && styles.pressed]}>
              <Text style={[styles.segmentText, selected && styles.segmentTextSelected]} numberOfLines={1}>{label}</Text>
            </Pressable>
          );
        })}
      </View>
      <View style={styles.sectionHead}>
        <Text style={styles.sectionKicker}>{segment === 'team' ? 'ROSTER' : segment === 'marketplace' ? 'CURATED MARKETPLACE' : 'SUGGESTED WORK'}</Text>
        <Text style={styles.sectionTitle}>{segment === 'team' ? 'The people Scout can bring in' : segment === 'marketplace' ? 'Find the next teammate' : 'Outcomes waiting for your decision'}</Text>
        <Text style={styles.sectionNote}>{records.length ? `${records.length} ${records.length === 1 ? 'record' : 'records'} · provider fenced` : words(reason, 'Nothing needs your attention.')}</Text>
        {segment === 'marketplace' && marketplace?.canManage ? <Action label="Create private agent" onPress={() => setTemplateOpen(true)} /> : null}
      </View>
    </>
  );

  return (
    <>
      <SafeAreaView style={styles.safe} edges={['top', 'left', 'right']}>
      <View style={styles.header}>
        <Pressable accessibilityRole="button" accessibilityLabel="Back" hitSlop={8} onPress={() => navigation.goBack()} style={({ pressed }) => [styles.headerButton, pressed && styles.pressed]}>
          <SymbolView name="chevron.left" size={17} tintColor={colors.text1} />
        </Pressable>
        <Text style={styles.headerTitle}>Team</Text>
        <View style={styles.headerButton} />
      </View>
      <FlatList
        data={loading ? [] : records}
        keyExtractor={(item, index) => ('id' in item && item.id) || String(index)}
        renderItem={({ item }) => segment === 'work'
          ? <WorkCard record={item as StrideWorkSuggestion} busy={Boolean(busyKey)} navigation={navigation} onChooseDestination={() => setDestinationWork(item as StrideWorkSuggestion)} run={run} sessionToken={sessionToken ?? ''} />
          : <AgentCard record={item as AgentRecord} marketplace={segment === 'marketplace'} seats={roster?.seats ?? []} busy={Boolean(busyKey)} navigation={navigation} onDetails={() => setDetailRecord(item as AgentRecord)} run={run} sessionToken={sessionToken ?? ''} />}
        ItemSeparatorComponent={() => <View style={styles.cardGap} />}
        ListHeaderComponent={header}
        ListEmptyComponent={loading ? <View style={styles.loading}><ActivityIndicator color={colors.text2} /><Text style={styles.loadingText}>Reading the durable record…</Text></View> : <EmptyState segment={segment} reason={error ?? reason} />}
        ListFooterComponent={<View style={styles.trustGrid}><TrustCard title="Human-approved" copy="Scout can recommend and prepare. A person confirms every hire and material action." /><TrustCard title="Company-owned memory" copy="Agents receive permission-filtered context, never a hidden copy of the company brain." /><TrustCard title="Easy to pause" copy="Pausing revokes new work and access while preserving clear authorship." /></View>}
        contentContainerStyle={styles.content}
        showsVerticalScrollIndicator={false}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void load(true)} />}
      />
      </SafeAreaView>
      {destinationWork ? (
        <ProjectDestinationModal
          busy={Boolean(busyKey)}
          projectThreads={projectThreads}
          record={destinationWork}
          onClose={() => setDestinationWork(null)}
          onSelect={async (body) => {
            let selected = false;
            await run(`destination:${destinationWork.id}`, async () => {
              await api.strideWorkDestination(sessionToken ?? '', destinationWork.id, { revision: destinationWork.revision, ...body });
              selected = true;
            });
            if (selected) setDestinationWork(null);
          }}
        />
      ) : null}
      {detailRecord ? <AgentDetailModal busy={Boolean(busyKey)} listing={marketplace?.listings.find((candidate) => candidate.id === ('listingId' in detailRecord ? detailRecord.listingId : detailRecord.id))} marketplace={!('listingId' in detailRecord)} navigation={navigation} onClose={() => setDetailRecord(null)} record={detailRecord} run={run} sessionToken={sessionToken ?? ''} /> : null}
      {templateOpen ? <PrivateAgentTemplateModal busy={Boolean(busyKey)} onClose={() => setTemplateOpen(false)} onCreate={async (body) => {
        let created = false;
        await run(`private-template:${body.templateId}`, async () => { await api.strideCreatePrivateAgentTemplate(sessionToken ?? '', body); created = true; });
        if (created) setTemplateOpen(false);
      }} /> : null}
    </>
  );
}

function AgentCard({ record, marketplace, seats, busy, navigation, onDetails, run, sessionToken }: { record: AgentRecord; marketplace: boolean; seats: StrideTeamSeat[]; busy: boolean; navigation: Props['navigation']; onDetails: () => void; run: (key: string, action: () => Promise<unknown>) => Promise<void>; sessionToken: string }) {
  const listing = record as StrideMarketplaceListing;
  const seatRecord = record as StrideTeamSeat;
  const id = marketplace ? listing.id : seatRecord.id;
  const name = words(record.displayName, id);
  const role = words(record.category, marketplace ? 'specialist' : 'agent coworker');
  const seat = marketplace ? seats.find((candidate) => candidate.listingId === id) : seatRecord;
  const state = marketplace ? words(listing.availability, 'internal preview') : words(seatRecord.status, 'unavailable');
  const outcome = marketplace ? words(listing.outcomeSummary, listing.personalitySummary) : 'Identity, assignments, learning, and access remain in the durable team record.';
  const action = marketplace
    ? !seat ? { label: 'Start preview', press: () => run(`trial:${id}`, () => api.strideStartTrial(sessionToken, id)) }
      : seat.status === 'trial' ? { label: 'Hire with approval', press: () => run(`hire:${id}`, () => api.strideHire(sessionToken, id, seat.revision)) }
        : { label: 'On your team', press: undefined }
    : seatRecord.status === 'hired_fenced' ? { label: 'Pause', press: () => run(`pause:${id}`, () => api.strideSeatAction(sessionToken, id, 'pause', seatRecord.revision)) }
      : { label: 'Offboard', press: seatRecord.status === 'offboarded' ? undefined : () => Alert.alert('Offboard teammate?', 'Their history stays attributable.', [{ text: 'Cancel', style: 'cancel' }, { text: 'Offboard', style: 'destructive', onPress: () => void run(`offboard:${id}`, () => api.strideSeatAction(sessionToken, id, 'offboard', seatRecord.revision)) }]) };
  return (
    <View style={styles.agentCard} accessibilityLabel={`${name}, ${role}, ${state}`}>
      <View style={styles.agentTop}><View style={styles.avatar}><Text style={styles.avatarText}>{initials(name)}</Text></View><View style={styles.agentIdentity}><Text style={styles.agentName} numberOfLines={1}>{name}</Text><Text style={styles.agentRole}>{role}</Text></View></View>
      <Text style={styles.agentBody}>{outcome}</Text>
      {marketplace ? <Text style={styles.agentPersonality}>{listing.personalitySummary}</Text> : null}
      <View style={styles.agentFoot}><Text style={styles.agentState}>{state.toUpperCase()}</Text><View style={styles.actions}><Action label="Details" disabled={busy} onPress={onDetails} />{!marketplace && seatRecord.directThreadId ? <Action label="Open chat" disabled={busy} onPress={() => navigation.navigate('Thread', { threadId: seatRecord.directThreadId!, title: name })} /> : null}<Action label={action.label} disabled={busy || !action.press} onPress={action.press} /></View></View>
    </View>
  );
}

type DetailSectionRecord = { id: string; title: string; body: string };

function AgentDetailModal({ busy, listing, marketplace, navigation, onClose, record, run, sessionToken }: {
  busy: boolean;
  listing?: StrideMarketplaceListing;
  marketplace: boolean;
  navigation: Props['navigation'];
  onClose: () => void;
  record: AgentRecord;
  run: (key: string, action: () => Promise<unknown>) => Promise<void>;
  sessionToken: string;
}) {
  const candidate = (marketplace ? record : listing) as StrideMarketplaceListing | undefined;
  const seat = marketplace ? undefined : record as StrideTeamSeat;
  const name = words(record.displayName, record.id);
  const [personality, setPersonality] = useState(seat?.config.personalityNotes ?? '');
  const [memberships, setMemberships] = useState((seat?.config.memberships ?? ['team']).join(', '));
  const [perRunBudget, setPerRunBudget] = useState(String(seat?.config.perRunBudgetCents ?? 0));
  const [dailyBudget, setDailyBudget] = useState(String(seat?.config.dailyBudgetCents ?? 0));
  const [learningText, setLearningText] = useState('');
  const [project, setProject] = useState('');
  const [destination, setDestination] = useState('');
  const [responsibility, setResponsibility] = useState('');
  const latestUpdate = seat?.updates[seat.updates.length - 1];
  const latestLearning = seat?.learning[seat.learning.length - 1];
  const sections = useMemo<DetailSectionRecord[]>(() => {
    if (marketplace && candidate) {
      return [
        { id: 'about', title: 'About', body: candidate.outcomeSummary },
        { id: 'personality', title: 'Personality', body: candidate.personalitySummary },
        { id: 'skills', title: 'Skills', body: (candidate.capabilities ?? []).map((item) => words(item)).join(' · ') || 'Capabilities are not published for this legacy preview.' },
        { id: 'access', title: 'Access', body: `${words(candidate.accessSummary, 'Access remains explicitly assigned and provider-fenced.')}\nRequires: ${(candidate.requiredAccess ?? []).map((item) => words(item)).join(', ') || 'review pending'}` },
        { id: 'memory', title: 'Memory & growth', body: words(candidate.memoryPolicy, 'Company-owned learning with human review.') },
        { id: 'results', title: 'Sample outcomes', body: (candidate.sampleOutputs ?? []).join(' · ') },
        { id: 'package', title: 'Package & cost', body: `${candidate.publisher} · ${candidate.version} · ${words(candidate.costBand)}\n${words(candidate.provenance)} · ${words(candidate.visibility)} · updates require human approval` },
        { id: 'evidence', title: 'Verification', body: `Package, deterministic sample, and rollback receipts are present. Live quality and human admission remain pending E10.\nPackage ${words(candidate.packageDigest).slice(0, 16) || 'legacy preview'}…` },
      ];
    }
    if (!seat) return [];
    return [
      { id: 'responsibilities', title: 'Responsibilities', body: seat.assignments.length ? seat.assignments.map((item) => `${words(item.role)} · ${item.responsibility}\n${words(item.projectOrChannel)} → ${words(item.destination)}`).join('\n\n') : 'No project or channel assignment yet.' },
      { id: 'access', title: 'Access', body: `${seat.accessRevoked ? 'Revoked' : 'Human-approved and provider-fenced'} · ${(seat.config.memberships ?? []).map((item) => words(item)).join(', ') || 'no memberships'}` },
      { id: 'memory', title: 'Memory & learning', body: seat.learning.length ? seat.learning.map((item) => `${words(item.status)} · ${item.summary}`).join('\n') : 'No reviewed learning records yet.' },
      { id: 'growth', title: 'Growth & versions', body: latestUpdate ? `${words(latestUpdate.status)} · ${latestUpdate.summary}\n${latestUpdate.semanticDiff ? semanticDiffSummary(latestUpdate.semanticDiff) : 'Legacy update; semantic diff unavailable.'}` : 'No profile updates proposed. Every material change remains opt-in.' },
      { id: 'activity', title: 'Activity', body: seat.lifecycle?.slice(-6).map((item) => `• ${words(item)}`).join('\n') || 'No lifecycle activity.' },
      { id: 'cost', title: 'Cost controls', body: `${seat.config.perRunBudgetCents}¢ per run · ${seat.config.dailyBudgetCents}¢ daily · ${words(seat.config.proactivity)} proactivity` },
      { id: 'identity', title: 'Identity', body: `${seat.listingId} · team revision ${seat.revision}\nAuthored history remains attributable after pause or offboarding.` },
    ];
  }, [candidate, latestUpdate, marketplace, seat]);

  const proposeUpdate = () => {
    if (!seat) return;
    const candidateConfig = {
      personalityNotes: personality.trim(),
      memberships: memberships.split(',').map((item) => item.trim()).filter(Boolean),
      perRunBudgetCents: Number(perRunBudget) || 0,
      dailyBudgetCents: Number(dailyBudget) || 0,
      proactivity: seat.config.proactivity,
    };
    onClose();
    void run(`update:${seat.id}`, () => api.strideProposeAgentUpdate(sessionToken, seat.id, { revision: seat.revision, summary: 'Human-requested profile, access, and budget review.', candidate: candidateConfig }));
  };

  const footer = !seat ? <View style={styles.detailSafety}><Text style={styles.detailSafetyTitle}>Admission stays closed</Text><Text style={styles.detailSafetyBody}>This preview can be inspected and trialed locally. It cannot start a provider session or inherit standing authority.</Text></View> : (
    <View style={styles.detailControls}>
      <Text style={styles.detailControlTitle}>Propose a profile update</Text>
      <Text style={styles.detailControlHint}>Nothing changes until the semantic diff is reviewed and approved.</Text>
      <TextInput accessibilityLabel="Personality notes" editable={!busy} onChangeText={setPersonality} placeholder="Personality notes" placeholderTextColor={colors.text3} style={styles.detailInput} value={personality} />
      <TextInput accessibilityLabel="Channel and project memberships" autoCapitalize="none" editable={!busy} onChangeText={setMemberships} placeholder="team, dog_perfect" placeholderTextColor={colors.text3} style={styles.detailInput} value={memberships} />
      <View style={styles.detailInputRow}><TextInput accessibilityLabel="Per-run budget cents" editable={!busy} inputMode="numeric" onChangeText={setPerRunBudget} placeholder="Per run ¢" placeholderTextColor={colors.text3} style={[styles.detailInput, styles.detailInputHalf]} value={perRunBudget} /><TextInput accessibilityLabel="Daily budget cents" editable={!busy} inputMode="numeric" onChangeText={setDailyBudget} placeholder="Daily ¢" placeholderTextColor={colors.text3} style={[styles.detailInput, styles.detailInputHalf]} value={dailyBudget} /></View>
      <Action label="Preview semantic diff" disabled={busy} onPress={proposeUpdate} />
      {latestUpdate?.status === 'pending' ? <View style={styles.detailActionRow}><Action label="Approve update" disabled={busy} onPress={() => { onClose(); void run(`approve-update:${seat.id}`, () => api.strideResolveAgentUpdate(sessionToken, seat.id, latestUpdate.id, 'approve', seat.revision)); }} /><Action label="Reject & roll back" disabled={busy} onPress={() => { onClose(); void run(`rollback-update:${seat.id}`, () => api.strideResolveAgentUpdate(sessionToken, seat.id, latestUpdate.id, 'rollback', seat.revision)); }} /></View> : null}

      <Text style={styles.detailControlTitle}>Assign a responsibility</Text>
      <View style={styles.detailInputRow}><TextInput accessibilityLabel="Project or channel" autoCapitalize="none" editable={!busy} onChangeText={setProject} placeholder="Project" placeholderTextColor={colors.text3} style={[styles.detailInput, styles.detailInputHalf]} value={project} /><TextInput accessibilityLabel="Destination thread" autoCapitalize="none" editable={!busy} onChangeText={setDestination} placeholder="Thread" placeholderTextColor={colors.text3} style={[styles.detailInput, styles.detailInputHalf]} value={destination} /></View>
      <TextInput accessibilityLabel="Responsibility" editable={!busy} onChangeText={setResponsibility} placeholder="What this coworker owns" placeholderTextColor={colors.text3} style={styles.detailInput} value={responsibility} />
      <Action label="Add assignment" disabled={busy || !project.trim() || !destination.trim() || !responsibility.trim()} onPress={() => { onClose(); void run(`assign:${seat.id}`, () => api.strideAssignAgent(sessionToken, seat.id, { revision: seat.revision, projectOrChannel: project.trim(), role: `${seat.category}_partner`, responsibility: responsibility.trim(), destination: destination.trim() })); }} />

      <Text style={styles.detailControlTitle}>Reviewed learning</Text>
      <TextInput accessibilityLabel="Learning or correction" editable={!busy} multiline onChangeText={setLearningText} placeholder="Add a source-linked lesson or correct the latest one" placeholderTextColor={colors.text3} style={[styles.detailInput, styles.detailInputMultiline]} value={learningText} />
      <View style={styles.detailActionRow}><Action label="Record" disabled={busy || !learningText.trim()} onPress={() => { onClose(); void run(`learn:${seat.id}`, () => api.strideRecordAgentLearning(sessionToken, seat.id, { revision: seat.revision, subject: seat.category, scope: 'team', summary: learningText.trim() })); }} />{latestLearning ? <Action label="Correct latest" disabled={busy || !learningText.trim()} onPress={() => { onClose(); void run(`correct:${seat.id}`, () => api.strideResolveAgentLearning(sessionToken, seat.id, latestLearning.id, 'correct', { revision: seat.revision, summary: learningText.trim() })); }} /> : null}{latestLearning && latestLearning.status !== 'forgotten' ? <Action label="Forget latest" disabled={busy} onPress={() => { onClose(); void run(`forget:${seat.id}`, () => api.strideResolveAgentLearning(sessionToken, seat.id, latestLearning.id, 'forget', { revision: seat.revision, summary: '' })); }} /> : null}</View>

      <View style={styles.detailActionRow}>{seat.directThreadId ? <Action label="Open chat" disabled={busy} onPress={() => { onClose(); navigation.navigate('Thread', { threadId: seat.directThreadId!, title: name }); }} /> : null}<Action label="Clean export receipt" disabled={busy} onPress={() => { onClose(); void run(`export:${seat.id}`, async () => { const response = await api.strideExportAgent(sessionToken, seat.id); Alert.alert('Clean export ready', String(response.export.historicalAttributionHash ?? 'Attribution receipt created.')); }); }} /></View>
    </View>
  );

  return (
    <Modal visible animationType="slide" presentationStyle="formSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.pickerSafe} edges={['top', 'bottom', 'left', 'right']}>
        <View style={styles.pickerHead}><View style={styles.pickerHeading}><Text style={styles.pickerEyebrow}>{marketplace ? 'MARKETPLACE PROFILE' : 'TEAM COWORKER'}</Text><Text style={styles.pickerTitle}>{name}</Text><Text style={styles.detailSubtitle}>{words(record.category)} · {words(marketplace ? candidate?.availability : seat?.status)}</Text></View><Pressable accessibilityRole="button" accessibilityLabel="Close agent details" hitSlop={8} onPress={onClose} style={({ pressed }) => [styles.pickerClose, pressed && styles.pressed]}><SymbolView name="xmark" size={15} tintColor={colors.text2} /></Pressable></View>
        <FlatList data={sections} keyExtractor={(item) => item.id} renderItem={({ item }) => <View style={styles.detailSection}><Text style={styles.detailSectionTitle}>{item.title}</Text><Text style={styles.detailSectionBody}>{item.body}</Text></View>} ItemSeparatorComponent={() => <View style={styles.detailDivider} />} ListFooterComponent={footer} contentContainerStyle={styles.detailList} keyboardShouldPersistTaps="handled" showsVerticalScrollIndicator={false} />
      </SafeAreaView>
    </Modal>
  );
}

function semanticDiffSummary(diff: NonNullable<StrideTeamSeat['updates'][number]['semanticDiff']>): string {
  const changes = [
    diff.personalityChanged && 'personality',
    diff.permissionChanged && `access (${[...diff.membershipsAdded, ...diff.membershipsRemoved].map((item) => words(item)).join(', ')})`,
    diff.costChanged && `cost (${diff.perRunBudgetDeltaCents >= 0 ? '+' : ''}${diff.perRunBudgetDeltaCents}¢/run)`,
    diff.proactivityChanged && 'proactivity',
    diff.runtimeChanged ? 'runtime' : 'runtime unchanged',
  ].filter(Boolean);
  return `${changes.join(' · ')}\n${diff.runtimeSummary}\n${diff.migrationSummary}`;
}

function PrivateAgentTemplateModal({ busy, onClose, onCreate }: { busy: boolean; onClose: () => void; onCreate: (body: StridePrivateAgentTemplateInput) => Promise<void> }) {
  const [name, setName] = useState('');
  const [category, setCategory] = useState('');
  const [outcome, setOutcome] = useState('');
  const [personality, setPersonality] = useState('');
  const [capabilities, setCapabilities] = useState('');
  const [access, setAccess] = useState('approved_project_context');
  const [samples, setSamples] = useState('');
  const split = (value: string) => value.split(',').map((item) => item.trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '_').replace(/^_+|_+$/g, '')).filter(Boolean);
  const templateId = name.trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '_').replace(/^_+|_+$/g, '').slice(0, 48);
  const valid = templateId && split(category).length === 1 && outcome.trim() && personality.trim() && split(capabilities).length && split(access).length && samples.split(',').some((item) => item.trim());
  const fields = [
    { id: 'name', label: 'Name', value: name, set: setName, placeholder: 'Lane' },
    { id: 'category', label: 'Role category', value: category, set: setCategory, placeholder: 'launch' },
    { id: 'outcome', label: 'What they deliver', value: outcome, set: setOutcome, placeholder: 'Turns approved launch context into bounded briefs.' },
    { id: 'personality', label: 'Personality', value: personality, set: setPersonality, placeholder: 'Candid, calm, and concise.' },
    { id: 'capabilities', label: 'Capabilities', value: capabilities, set: setCapabilities, placeholder: 'approved_project_brief' },
    { id: 'access', label: 'Required access', value: access, set: setAccess, placeholder: 'approved_project_context' },
    { id: 'samples', label: 'Sample outcomes', value: samples, set: setSamples, placeholder: 'Launch brief, Risk review' },
  ];
  return (
    <Modal visible animationType="slide" presentationStyle="formSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.pickerSafe} edges={['top', 'bottom', 'left', 'right']}>
        <View style={styles.pickerHead}><View style={styles.pickerHeading}><Text style={styles.pickerEyebrow}>ORGANIZATION-PRIVATE TEMPLATE</Text><Text style={styles.pickerTitle}>Create a teammate, safely.</Text></View><Pressable accessibilityRole="button" accessibilityLabel="Close private agent template" hitSlop={8} onPress={onClose} style={({ pressed }) => [styles.pickerClose, pressed && styles.pressed]}><SymbolView name="xmark" size={15} tintColor={colors.text2} /></Pressable></View>
        <Text style={styles.pickerCopy}>Describe the role, outcomes, personality, and bounded access. Templates cannot contain code, commands, credentials, environment values, hooks, or raw tool-server configuration.</Text>
        <FlatList data={fields} keyExtractor={(item) => item.id} renderItem={({ item }) => <View style={styles.templateField}><Text style={styles.templateLabel}>{item.label.toUpperCase()}</Text><TextInput accessibilityLabel={item.label} editable={!busy} multiline={item.id === 'outcome' || item.id === 'personality'} onChangeText={item.set} placeholder={item.placeholder} placeholderTextColor={colors.text3} style={[styles.detailInput, (item.id === 'outcome' || item.id === 'personality') && styles.detailInputMultiline]} value={item.value} /></View>} ListFooterComponent={<View style={styles.templateFooter}><Text style={styles.detailControlHint}>Private preview · human approval · provider execution fenced · 25¢ per-run ceiling · disabled proactivity</Text><Action label="Create fenced preview" disabled={busy || !valid} onPress={() => void onCreate({ templateId, displayName: name.trim(), category: split(category)[0], outcomeSummary: outcome.trim(), personalitySummary: personality.trim(), sampleOutputs: samples.split(',').map((item) => item.trim()).filter(Boolean), requestedCapabilities: split(capabilities), requiredAccess: split(access), costBand: 'low', memberships: ['team'], perRunBudgetCents: 25, dailyBudgetCents: 100, monthlyBudgetCents: 500, concurrency: 1, proactivity: 'disabled' })} /></View>} contentContainerStyle={styles.detailList} keyboardShouldPersistTaps="handled" />
      </SafeAreaView>
    </Modal>
  );
}

function WorkCard({ record, busy, navigation, onChooseDestination, run, sessionToken }: { record: StrideWorkSuggestion; busy: boolean; navigation: Props['navigation']; onChooseDestination: () => void; run: (key: string, action: () => Promise<unknown>) => Promise<void>; sessionToken: string }) {
  return (
    <View style={styles.agentCard} accessibilityLabel={`${record.title}, ${record.status}`}>
      <Text style={styles.agentName}>{record.title}</Text>
      <Text style={styles.agentBody}>{record.sourceSnippet || record.outcome}</Text>
      <Text style={styles.projectMeta}>{record.destinationThreadId ? `PROJECT · ${record.destinationTitle || 'Selected project'}` : 'PROJECT · CHOOSE BEFORE APPROVAL'}</Text>
      <Text style={styles.timeline}>{record.lifecycle.slice(-3).map((step) => `• ${words(step)}`).join('\n')}</Text>
      <View style={styles.agentFoot}>
        <Text style={styles.agentState}>{record.status.toUpperCase()}</Text>
        <View style={styles.actions}>
          {record.status === 'suggested' ? <Action label={record.destinationThreadId ? 'Change project' : 'Choose project'} disabled={busy} onPress={onChooseDestination} /> : null}
          {record.status === 'suggested' && record.destinationThreadId ? <Action label="Approve & run" disabled={busy} onPress={() => void run(`approve:${record.id}`, () => api.strideWorkDecision(sessionToken, record.id, 'approve', { revision: record.revision }))} /> : null}
          {record.status === 'suggested' ? <Action label="Dismiss" disabled={busy} onPress={() => void run(`dismiss:${record.id}`, () => api.strideWorkDecision(sessionToken, record.id, 'dismiss', { revision: record.revision, reason: 'Dismissed from Suggested work.' }))} /> : null}
          {record.status === 'completed' && record.destinationThreadId ? <Action label="Open project" disabled={busy} onPress={() => navigation.navigate('Thread', { threadId: record.destinationThreadId!, title: record.destinationTitle || record.title })} /> : null}
        </View>
      </View>
    </View>
  );
}

function ProjectDestinationModal({ busy, onClose, onSelect, projectThreads, record }: {
  busy: boolean;
  onClose: () => void;
  onSelect: (body: { mode: 'existing'; threadId: string } | { mode: 'new'; title: string }) => Promise<void>;
  projectThreads: ScoutThread[];
  record: StrideWorkSuggestion;
}) {
  const [newTitle, setNewTitle] = useState(record.title);
  return (
    <Modal visible animationType="slide" presentationStyle="formSheet" onRequestClose={onClose}>
      <SafeAreaView style={styles.pickerSafe} edges={['top', 'bottom', 'left', 'right']}>
        <View style={styles.pickerHead}>
          <View style={styles.pickerHeading}>
            <Text style={styles.pickerEyebrow}>WORK DESTINATION</Text>
            <Text style={styles.pickerTitle}>Choose the project that owns this work</Text>
          </View>
          <Pressable accessibilityRole="button" accessibilityLabel="Close project chooser" disabled={busy} hitSlop={8} onPress={onClose} style={({ pressed }) => [styles.pickerClose, pressed && styles.pressed]}>
            <SymbolView name="xmark" size={15} tintColor={colors.text2} />
          </Pressable>
        </View>
        <Text style={styles.pickerCopy}>Scout will run only after approval, then report progress and completion in the project you choose.</Text>
        <Text style={styles.pickerLabel}>EXISTING PROJECTS</Text>
        <FlatList
          data={projectThreads}
          keyExtractor={(thread) => String(thread.id)}
          renderItem={({ item }) => (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={`Choose ${words(item.title, 'project')}`}
              disabled={busy}
              onPress={() => void onSelect({ mode: 'existing', threadId: String(item.id) })}
              style={({ pressed }) => [styles.projectChoice, pressed && styles.projectChoicePressed, busy && styles.actionDisabled]}
            >
              <Text style={styles.projectChoiceName} numberOfLines={1}>{words(item.title, 'Project')}</Text>
              <Text style={styles.projectChoiceMeta}>{String(item.id) === record.sourceThreadId ? 'SOURCE PROJECT' : 'PROJECT THREAD'}</Text>
            </Pressable>
          )}
          ItemSeparatorComponent={() => <View style={styles.projectGap} />}
          ListEmptyComponent={<Text style={styles.projectEmpty}>No eligible project threads yet. Create one below; company-wide and private conversations cannot receive the run.</Text>}
          contentContainerStyle={styles.projectList}
          keyboardShouldPersistTaps="handled"
        />
        <View style={styles.newProject}>
          <View style={styles.newProjectCopy}>
            <Text style={styles.newProjectTitle}>New project thread</Text>
            <Text style={styles.newProjectHint}>Give this outcome a dedicated home.</Text>
          </View>
          <View style={styles.newProjectRow}>
            <TextInput
              accessibilityLabel="New project thread name"
              autoCapitalize="sentences"
              editable={!busy}
              onChangeText={setNewTitle}
              placeholder="Project name"
              placeholderTextColor={colors.text3}
              returnKeyType="done"
              style={styles.projectInput}
              value={newTitle}
            />
            <Action label="Create" disabled={busy || !newTitle.trim()} onPress={() => void onSelect({ mode: 'new', title: newTitle.trim() })} />
          </View>
        </View>
      </SafeAreaView>
    </Modal>
  );
}

function Action({ label, disabled, onPress }: { label: string; disabled?: boolean; onPress?: () => void }) {
  return <Pressable accessibilityRole="button" accessibilityState={{ disabled: Boolean(disabled) }} disabled={disabled} onPress={onPress} style={({ pressed }) => [styles.action, pressed && styles.pressed, disabled && styles.actionDisabled]}><Text style={styles.actionText}>{label}</Text></Pressable>;
}

function EmptyState({ segment, reason }: { segment: Segment; reason?: string | null }) {
  return <View style={styles.emptyCard}><View style={styles.emptyMark}><SymbolView name="person.2.fill" size={20} tintColor={colors.text2} /></View><View style={styles.emptyCopy}><Text style={styles.emptyTitle}>{segment === 'work' ? 'No suggested work right now.' : segment === 'marketplace' ? 'No previews are available yet.' : 'Your durable agent roster is empty.'}</Text><Text style={styles.emptyBody}>{words(reason, 'Scout will surface the next relevant, permission-safe action here.')}</Text></View></View>;
}

function TrustCard({ title, copy }: { title: string; copy: string }) {
  return <View style={styles.trustCard}><Text style={styles.trustTitle}>{title}</Text><Text style={styles.trustCopy}>{copy}</Text></View>;
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  header: { minHeight: 52, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', paddingHorizontal: space[3] },
  headerButton: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full },
  headerTitle: { ...type.headline, color: colors.text1 },
  pressed: { opacity: 0.66, transform: [{ scale: 0.98 }] },
  content: { paddingHorizontal: space[5], paddingBottom: space[12] },
  hero: { paddingTop: space[6], paddingBottom: space[8], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  eyebrow: { ...type.label, color: colors.text3, letterSpacing: 1.2 },
  title: { marginTop: space[3], color: colors.text1, fontSize: 38, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', letterSpacing: -1.5, lineHeight: 40 },
  intro: { ...type.body, marginTop: space[4], color: colors.text2 },
  statusPill: { alignSelf: 'flex-start', minHeight: 34, marginTop: space[5], paddingHorizontal: space[3], flexDirection: 'row', alignItems: 'center', gap: space[2], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, borderRadius: radius.full, backgroundColor: colors.surface1 },
  statusDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: colors.text3 },
  statusDotStandby: { backgroundColor: colors.ember },
  statusDotError: { backgroundColor: colors.danger },
  statusText: { ...type.label, color: colors.text2, textTransform: 'lowercase' },
  segmented: { flexDirection: 'row', gap: space[1], marginTop: space[6], marginBottom: space[8], padding: space[1], borderRadius: radius.md, backgroundColor: colors.surface3 },
  segment: { flex: 1, minHeight: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.sm, paddingHorizontal: space[1] },
  segmentSelected: { backgroundColor: colors.surface1 },
  segmentText: { ...type.captionMedium, color: colors.text2 },
  segmentTextSelected: { color: colors.text1 },
  sectionHead: { gap: space[2], marginBottom: space[4] },
  sectionKicker: { ...type.label, color: colors.text3, letterSpacing: 1.1 },
  sectionTitle: { ...type.title2, color: colors.text1 },
  sectionNote: { ...type.caption, color: colors.text3 },
  loading: { minHeight: 170, alignItems: 'center', justifyContent: 'center', gap: space[3], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, borderRadius: radius.xl, backgroundColor: colors.surface1 },
  loadingText: { ...type.caption, color: colors.text2 },
  cardGap: { height: space[3] },
  agentCard: { padding: space[5], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, borderRadius: radius.xl, backgroundColor: colors.surface1, borderCurve: 'continuous' },
  agentTop: { flexDirection: 'row', alignItems: 'center', gap: space[3] },
  avatar: { width: 46, height: 46, alignItems: 'center', justifyContent: 'center', borderRadius: radius.lg, backgroundColor: colors.surface3 },
  avatarText: { ...type.bodyMedium, color: colors.text1 },
  agentIdentity: { flex: 1, minWidth: 0 },
  agentName: { ...type.headline, color: colors.text1 },
  agentRole: { ...type.label, marginTop: 3, color: colors.text3, textTransform: 'capitalize' },
  agentBody: { ...type.bodySm, marginVertical: space[5], color: colors.text2 },
  agentPersonality: { ...type.caption, marginTop: -space[3], marginBottom: space[5], color: colors.text3, fontStyle: 'italic' },
  timeline: { ...type.caption, marginBottom: space[4], color: colors.text3, lineHeight: 20 },
  projectMeta: { ...type.label, marginBottom: space[3], color: colors.text3, letterSpacing: 0.7 },
  agentFoot: { paddingTop: space[4], flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[3], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  agentState: { ...type.label, flex: 1, color: colors.text3 },
  actions: { flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'flex-end', gap: space[2] },
  action: { minHeight: hitMin, paddingHorizontal: space[4], alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.surface3 },
  actionDisabled: { opacity: 0.5 },
  actionText: { ...type.captionMedium, color: colors.text1 },
  emptyCard: { padding: space[6], flexDirection: 'row', alignItems: 'flex-start', gap: space[4], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, borderRadius: radius.xl, backgroundColor: colors.surface1 },
  emptyMark: { width: 46, height: 46, alignItems: 'center', justifyContent: 'center', borderRadius: radius.lg, backgroundColor: colors.surface3 },
  emptyCopy: { flex: 1, gap: space[2] },
  emptyTitle: { ...type.headline, color: colors.text1 },
  emptyBody: { ...type.bodySm, color: colors.text2 },
  trustGrid: { gap: space[3], marginTop: space[10] },
  trustCard: { paddingTop: space[4], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  trustTitle: { ...type.bodyMedium, color: colors.text1 },
  trustCopy: { ...type.caption, marginTop: space[1], color: colors.text2 },
  pickerSafe: { flex: 1, backgroundColor: colors.bgApp, paddingHorizontal: space[5] },
  pickerHead: { flexDirection: 'row', alignItems: 'flex-start', justifyContent: 'space-between', gap: space[4], paddingTop: space[6] },
  pickerHeading: { flex: 1, minWidth: 0 },
  pickerEyebrow: { ...type.label, color: colors.text3, letterSpacing: 1.2 },
  pickerTitle: { ...type.title2, marginTop: space[2], color: colors.text1 },
  pickerClose: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, backgroundColor: colors.surface3 },
  pickerCopy: { ...type.bodySm, marginTop: space[4], marginBottom: space[6], color: colors.text2 },
  pickerLabel: { ...type.label, marginBottom: space[3], color: colors.text3, letterSpacing: 1.1 },
  projectList: { flexGrow: 1, paddingBottom: space[5] },
  projectGap: { height: space[2] },
  projectChoice: { minHeight: 62, paddingHorizontal: space[4], paddingVertical: space[3], flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: space[3], borderRadius: radius.lg, backgroundColor: colors.surface1 },
  projectChoicePressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  projectChoiceName: { ...type.bodyMedium, flex: 1, minWidth: 0, color: colors.text1 },
  projectChoiceMeta: { ...type.label, color: colors.text3 },
  projectEmpty: { ...type.bodySm, paddingVertical: space[5], color: colors.text3 },
  newProject: { paddingTop: space[4], paddingBottom: space[4], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  newProjectCopy: { marginBottom: space[3] },
  newProjectTitle: { ...type.bodyMedium, color: colors.text1 },
  newProjectHint: { ...type.caption, marginTop: space[1], color: colors.text3 },
  newProjectRow: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  projectInput: { ...type.bodySm, flex: 1, minHeight: hitMin, paddingHorizontal: space[4], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, borderRadius: radius.full, color: colors.text1, backgroundColor: colors.surface1 },
  detailSubtitle: { ...type.caption, marginTop: space[2], color: colors.text3, textTransform: 'capitalize' },
  detailList: { paddingTop: space[6], paddingBottom: space[12] },
  detailSection: { paddingVertical: space[4] },
  detailSectionTitle: { ...type.label, marginBottom: space[2], color: colors.text3, letterSpacing: 0.9, textTransform: 'uppercase' },
  detailSectionBody: { ...type.bodySm, color: colors.text1, lineHeight: 22 },
  detailDivider: { height: StyleSheet.hairlineWidth, backgroundColor: colors.line1 },
  detailSafety: { marginTop: space[6], padding: space[5], borderRadius: radius.xl, backgroundColor: colors.surface1 },
  detailSafetyTitle: { ...type.bodyMedium, color: colors.text1 },
  detailSafetyBody: { ...type.bodySm, marginTop: space[2], color: colors.text2 },
  detailControls: { marginTop: space[8], gap: space[3], paddingTop: space[6], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1 },
  detailControlTitle: { ...type.headline, marginTop: space[5], color: colors.text1 },
  detailControlHint: { ...type.caption, color: colors.text3 },
  detailInput: { ...type.bodySm, minHeight: hitMin, paddingHorizontal: space[4], paddingVertical: space[3], borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, borderRadius: radius.lg, color: colors.text1, backgroundColor: colors.surface1 },
  detailInputMultiline: { minHeight: 88, textAlignVertical: 'top' },
  detailInputRow: { flexDirection: 'row', gap: space[2] },
  detailInputHalf: { flex: 1 },
  detailActionRow: { flexDirection: 'row', flexWrap: 'wrap', gap: space[2] },
  templateField: { marginBottom: space[4], gap: space[2] },
  templateLabel: { ...type.label, color: colors.text3, letterSpacing: 0.9 },
  templateFooter: { gap: space[4], paddingTop: space[4], paddingBottom: space[8] },
});
