import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { AccessibilityInfo, Pressable, ScrollView, StyleSheet, Text, TextInput, View, findNodeHandle, type LayoutChangeEvent } from 'react-native';
import { useFocusEffect } from '@react-navigation/native';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { api, BonfireApiError } from '../api/client';
import type {
  MeetingRecordClaim,
  MeetingRecordDetail,
  MeetingRecordIndexItem,
  MeetingRecordReference,
  MeetingRecordTranscriptSegment,
} from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import type { RootStackParamList } from '../navigation/types';
import { isDefinitiveMeetingRecordDenial, meetingRecordDetailRemainsCurrent, meetingRecordReferenceHasExactDestination, meetingRecordReturnLabel, meetingRecordSourceScrollOffset } from './meetingRecordsState';
import { colors, radius, space, type } from '../theme/tokens';

function scalableText<T extends { readonly lineHeight: number }>(value: T) {
  const { lineHeight: _fixedLineHeight, ...style } = value;
  return style;
}

function friendlyDate(raw: string) {
  const value = new Date(raw);
  if (Number.isNaN(value.valueOf())) return 'Meeting';
  return new Intl.DateTimeFormat(undefined, {
    month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit',
  }).format(value);
}

function durationLabel(seconds: number) {
  const minutes = Math.max(0, Math.round(seconds / 60));
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  return `${hours}h${remainder ? ` ${remainder}m` : ''}`;
}

function coverageLabel(state: string) {
  switch (state) {
    case 'full': return 'Sources complete';
    case 'catching_up': return 'Analysis catching up';
    case 'partial_late_start': return 'Opening may be missing';
    case 'partial_gaps': return 'Transcript may have gaps';
    case 'partial_synthesis': return 'Analysis has gaps';
    case 'no_transcript': return 'Transcript unavailable';
    default: return 'Coverage unknown';
  }
}

function claimMeta(claim: MeetingRecordClaim) {
  const parts = [claim.status];
  if (claim.owner) parts.push(claim.owner);
  else if (claim.kind === 'commitment' || claim.kind === 'decision') parts.push('owner unresolved');
  if (claim.dueState === 'unresolved') parts.push('due unresolved');
  return parts.join(' · ');
}

function ClaimSection({ title, claims, empty, onOpenSource, onOpenReference }: { title: string; claims: MeetingRecordClaim[]; empty: string; onOpenSource: (claim: MeetingRecordClaim) => void; onOpenReference: (reference: MeetingRecordReference) => void }) {
  return (
    <View style={styles.section}>
      <Text style={styles.sectionTitle}>{title}</Text>
      {claims.length ? claims.map((claim, index) => (
        <View key={`${claim.kind}-${claim.sources[0]?.segmentId ?? index}`} style={styles.claim}>
          <Text style={styles.claimText}>{claim.text}</Text>
          <Text style={styles.claimMeta}>{claimMeta(claim)}</Text>
          {claim.sources[0] ? (
			<Pressable
			  accessibilityRole="button"
			  accessibilityLabel={`Open exact transcript source, ${claim.sources[0].correctionState}, revision ${claim.sources[0].revision.slice(0, 8)}`}
			  accessibilityHint="Opens the exact transcript interval and keeps this Meeting Record as the return destination"
			  onPress={() => onOpenSource(claim)}
			  style={({ pressed }) => pressed && styles.pressed}
			>
			  <Text style={styles.sourceMeta}>
				{claim.sources[0].speaker ? `${claim.sources[0].speaker} · ` : ''}{friendlyDate(claim.sources[0].at)} · source {claim.sources[0].correctionState}
			  </Text>
			</Pressable>
          ) : null}
		  {[...(claim.work ?? []), ...(claim.projects ?? [])].filter(meetingRecordReferenceHasExactDestination).map(reference => (
			<Pressable
			  key={`${reference.kind}-${reference.id}`}
			  accessibilityRole="button"
			  accessibilityLabel={`Open linked ${reference.kind === 'project' ? 'Project' : 'Work'} ${reference.title}`}
			  onPress={() => onOpenReference(reference)}
			  style={({ pressed }) => pressed && styles.pressed}
			>
			  <Text style={styles.referenceLink}>{reference.kind === 'project' ? 'Project' : 'Work'} · {reference.title}</Text>
			</Pressable>
		  ))}
        </View>
      )) : <Text style={styles.emptySection}>{empty}</Text>}
    </View>
  );
}

function TranscriptRow({ segment, target, targetRef, onTargetLayout }: { segment: MeetingRecordTranscriptSegment; target: boolean; targetRef: React.RefObject<View | null>; onTargetLayout: (event: LayoutChangeEvent) => void }) {
  return (
    <View
      ref={target ? targetRef : undefined}
      accessible={target}
      accessibilityLabel={target ? `Exact transcript source. ${segment.speaker || 'Speaker unresolved'}. ${segment.text}` : undefined}
      onLayout={target ? onTargetLayout : undefined}
      style={[styles.transcriptRow, target && styles.transcriptRowTarget]}
    >
      <View style={styles.transcriptMetaRow}>
        <Text style={styles.transcriptSpeaker}>{segment.speaker || 'Speaker unresolved'}</Text>
        <Text style={styles.transcriptTime}>{friendlyDate(segment.at)}</Text>
      </View>
      <Text style={styles.transcriptText}>{segment.text}</Text>
    </View>
  );
}

type Props = NativeStackScreenProps<RootStackParamList, 'Meetings'>;

export function MeetingsScreen({ navigation, route }: Props) {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const sessionRef = useRef(sessionToken);
  sessionRef.current = sessionToken;
	const selectedIdRef = useRef('');
	const detailRef = useRef<MeetingRecordDetail | null>(null);
	const rowsGenerationRef = useRef(0);
	const detailGenerationRef = useRef(0);
	const conversationGenerationRef = useRef(0);
  const recordScrollRef = useRef<ScrollView>(null);
  const transcriptSectionYRef = useRef(0);
  const sourceTargetRef = useRef<View>(null);
  const [rows, setRows] = useState<MeetingRecordIndexItem[]>([]);
  const [rowsToken, setRowsToken] = useState<string | null>(null);
  const [nextMeetingCursor, setNextMeetingCursor] = useState('');
  const [hasMoreMeetings, setHasMoreMeetings] = useState(false);
  const [detail, setDetail] = useState<MeetingRecordDetail | null>(null);
  const [detailToken, setDetailToken] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState('');
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [transcriptOpen, setTranscriptOpen] = useState(false);
  const [transcriptQuery, setTranscriptQuery] = useState('');
	const [askScoutBusy, setAskScoutBusy] = useState(false);
  const [sourceTargetSegmentId, setSourceTargetSegmentId] = useState('');

  const visibleRows = rowsToken === sessionToken ? rows : [];
  const visibleDetail = detailToken === sessionToken && detail?.id === selectedId ? detail : null;
	selectedIdRef.current = selectedId;
	detailRef.current = detail;

  const loadRows = useCallback(async (refresh = false) => {
    const token = sessionRef.current;
    if (!token) return;
	const generation = ++rowsGenerationRef.current;
    refresh ? setRefreshing(true) : setLoading(true);
    setError(null);
    try {
      const response = await api.meetings(token);
	  if (generation !== rowsGenerationRef.current || sessionRef.current !== token) return;
      setRows(response.meetings);
      setRowsToken(token);
	  setNextMeetingCursor(response.nextCursor ?? '');
	  setHasMoreMeetings(Boolean(response.hasMore && response.nextCursor));
	  const currentSelectedId = selectedIdRef.current;
	  const selectedRowReturned = response.meetings.some(row => row.id === currentSelectedId);
	  if (currentSelectedId && selectedRowReturned && detailRef.current && !meetingRecordDetailRemainsCurrent(response.meetings, currentSelectedId, detailRef.current)) {
		setDetail(null);
		setDetailToken(null);
		setDetailLoading(false);
		conversationGenerationRef.current += 1;
		setAskScoutBusy(false);
	setSourceTargetSegmentId('');
	  }
    } catch (reason) {
	  if (generation !== rowsGenerationRef.current || sessionRef.current !== token) return;
      setError(reason instanceof BonfireApiError ? reason.message : 'Could not load Meeting Records');
      if (!refresh) setRowsToken(token);
    } finally {
	  if (generation === rowsGenerationRef.current && sessionRef.current === token) {
        setLoading(false);
        setRefreshing(false);
      }
    }
  }, []);

	const loadMoreMeetings = useCallback(async () => {
		const token = sessionRef.current;
		const cursor = nextMeetingCursor;
		if (!token || !cursor || refreshing) return;
		const generation = ++rowsGenerationRef.current;
		setRefreshing(true);
		setError(null);
		try {
			const response = await api.meetings(token, cursor);
			if (generation !== rowsGenerationRef.current || sessionRef.current !== token) return;
			setRows(previous => {
				const seen = new Set(previous.map(row => row.id));
				return [...previous, ...response.meetings.filter(row => !seen.has(row.id))];
			});
			setRowsToken(token);
			setNextMeetingCursor(response.nextCursor ?? '');
			setHasMoreMeetings(Boolean(response.hasMore && response.nextCursor));
		} catch (reason) {
			if (generation === rowsGenerationRef.current && sessionRef.current === token) setError(reason instanceof BonfireApiError ? reason.message : 'Could not load older Meeting Records');
		} finally {
			if (generation === rowsGenerationRef.current && sessionRef.current === token) setRefreshing(false);
		}
	}, [nextMeetingCursor, refreshing]);

  const loadDetail = useCallback(async (meetingId: string, options: { cursor?: string; query?: string; segmentId?: string; append?: boolean } = {}) => {
    const token = sessionRef.current;
    if (!token || !meetingId) return;
	const generation = ++detailGenerationRef.current;
    setDetailLoading(true);
    setError(null);
    try {
      const response = await api.meeting(token, meetingId, {
        cursor: options.cursor,
        query: options.query,
		segmentId: options.segmentId,
        transcriptLimit: 100,
      });
	  if (generation !== detailGenerationRef.current || sessionRef.current !== token || selectedId !== meetingId) return;
      setDetail(previous => {
        if (!options.append || !previous || previous.id !== response.meeting.id) return response.meeting;
        return {
          ...response.meeting,
          transcript: {
            ...response.meeting.transcript,
            segments: [...previous.transcript.segments, ...response.meeting.transcript.segments],
          },
        };
      });
      setDetailToken(token);
    } catch (reason) {
	  if (generation !== detailGenerationRef.current || sessionRef.current !== token) return;
      setError(reason instanceof BonfireApiError ? reason.message : 'Could not open this Meeting Record');
	  if (reason instanceof BonfireApiError && isDefinitiveMeetingRecordDenial(reason.status)) {
		detailGenerationRef.current += 1;
		conversationGenerationRef.current += 1;
		setDetail(null);
		setDetailToken(null);
		setDetailLoading(false);
		setAskScoutBusy(false);
	  }
    } finally {
	  if (generation === detailGenerationRef.current && sessionRef.current === token) setDetailLoading(false);
    }
  }, [selectedId]);

  useEffect(() => {
	rowsGenerationRef.current += 1;
	detailGenerationRef.current += 1;
	conversationGenerationRef.current += 1;
    setSelectedId('');
    setDetail(null);
    setDetailToken(null);
    setRowsToken(null);
    setTranscriptOpen(false);
    setTranscriptQuery('');
	setRows([]);
	setNextMeetingCursor('');
	setHasMoreMeetings(false);
	setDetailLoading(false);
	setAskScoutBusy(false);
	setSourceTargetSegmentId('');
	if (sessionToken) void loadRows();
  }, [loadRows, sessionToken]);

	// Blur (for example Ask Scout -> Thread) preserves the exact selected
	// record/transcript position. Returning refreshes current authority and the
	// revision fence without first exposing another session's state.
	useFocusEffect(useCallback(() => {
		if (!sessionRef.current) return;
		void loadRows(true);
		if (selectedIdRef.current) void loadDetail(selectedIdRef.current);
	}, [loadDetail, loadRows]));

  useEffect(() => {
    if (office.event === 'meeting' || office.event === 'meeting_archived' || office.event === 'memory') {
      void loadRows(true);
      if (selectedId) void loadDetail(selectedId);
    }
  }, [loadDetail, loadRows, office.event, office.version, selectedId]);

  const selectMeeting = useCallback((meetingId: string) => {
	 navigation.setParams({ meetingId: undefined, segmentId: undefined });
    setSelectedId(meetingId);
    setDetail(null);
    setDetailToken(null);
    setTranscriptOpen(false);
    setTranscriptQuery('');
	setSourceTargetSegmentId('');
  }, [navigation]);

	useEffect(() => {
		const meetingId = String(route.params?.meetingId ?? '').trim();
		if (!meetingId || selectedId === meetingId) return;
		setSelectedId(meetingId);
		setDetail(null);
		setDetailToken(null);
		setTranscriptOpen(true);
		setTranscriptQuery('');
		setSourceTargetSegmentId(String(route.params?.segmentId ?? '').trim());
	}, [route.params?.meetingId, route.params?.segmentId, selectedId]);

  useEffect(() => {
	if (selectedId) void loadDetail(selectedId, {
		segmentId: route.params?.meetingId === selectedId ? route.params?.segmentId : undefined,
	});
	}, [loadDetail, route.params?.meetingId, route.params?.segmentId, selectedId]);

  const closeDetail = useCallback(() => {
	detailGenerationRef.current += 1;
    setSelectedId('');
    setDetail(null);
    setDetailToken(null);
    setTranscriptOpen(false);
    setTranscriptQuery('');
    setSourceTargetSegmentId('');
    setError(null);
	navigation.setParams({ meetingId: undefined, segmentId: undefined });
	}, [navigation]);

	const askScoutAboutMeeting = useCallback(async () => {
		const token = sessionRef.current;
		const current = visibleDetail;
		if (!token || !current || askScoutBusy) return;
		const generation = ++conversationGenerationRef.current;
		setAskScoutBusy(true);
		setError(null);
		try {
			const response = await api.meetingConversation(token, current.id, current.recordRevision);
			if (generation !== conversationGenerationRef.current || sessionRef.current !== token) return;
			if (!response.thread?.id) throw new Error('Meeting Record conversation is unavailable');
			navigation.navigate('Thread', { threadId: response.thread.id, title: response.thread.title || `Meeting · ${current.title}` });
		} catch (reason) {
			if (generation !== conversationGenerationRef.current || sessionRef.current !== token) return;
			setError(reason instanceof BonfireApiError ? reason.message : 'Could not start a Meeting Record conversation');
		} finally {
			if (generation === conversationGenerationRef.current && sessionRef.current === token) setAskScoutBusy(false);
		}
	}, [askScoutBusy, navigation, visibleDetail]);

	const openClaimSource = useCallback((claim: MeetingRecordClaim) => {
		const source = claim.sources[0];
		if (!source || !selectedId) return;
		setTranscriptOpen(true);
		setTranscriptQuery('');
		setSourceTargetSegmentId(source.segmentId);
		navigation.setParams({ meetingId: selectedId, segmentId: source.segmentId });
		void loadDetail(selectedId, { segmentId: source.segmentId });
	}, [loadDetail, navigation, selectedId]);

	const openClaimReference = useCallback((reference: MeetingRecordReference) => {
		if (!meetingRecordReferenceHasExactDestination(reference)) return;
		const openId = String(reference.openId).trim();
		if (reference.openKind === 'project') {
			navigation.navigate('Thread', { threadId: openId, title: reference.title });
			return;
		}
		navigation.navigate('Files', { fileId: openId });
	}, [navigation]);

  const revealExactSource = useCallback((event: LayoutChangeEvent) => {
    const y = meetingRecordSourceScrollOffset(transcriptSectionYRef.current, event.nativeEvent.layout.y, space[3]);
    requestAnimationFrame(() => {
      recordScrollRef.current?.scrollTo({ y, animated: true });
      setTimeout(() => {
        const handle = findNodeHandle(sourceTargetRef.current);
        if (handle) AccessibilityInfo.setAccessibilityFocus(handle);
        AccessibilityInfo.announceForAccessibility('Exact transcript source opened');
      }, 350);
    });
  }, []);

  const detailMeta = useMemo(() => {
    if (!visibleDetail) return '';
    return [friendlyDate(visibleDetail.startedAt), durationLabel(visibleDetail.durationSeconds), `${visibleDetail.participants.length} people`].join(' · ');
  }, [visibleDetail]);

  if (selectedId) {
    return (
      <Screen
        title="Meeting Record"
        subtitle={visibleDetail ? detailMeta : 'Loading the current authorized revision'}
        loading={detailLoading && !visibleDetail}
        error={error}
        onRetry={() => void loadDetail(selectedId)}
        scrollRef={recordScrollRef}
      >
        {route.params?.returnToRoomId && route.params?.returnMode ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Back to live meeting ${route.params.returnMode}`}
            onPress={() => navigation.goBack()}
            style={({ pressed }) => [styles.returnToRoom, pressed && styles.pressed]}
          >
            <Text style={styles.returnToRoomText}>‹ {meetingRecordReturnLabel(route.params.returnMode)}</Text>
          </Pressable>
        ) : null}
        <Pressable accessibilityRole="button" accessibilityLabel="Back to all meetings" onPress={closeDetail} style={({ pressed }) => [styles.backToList, pressed && styles.pressed]}>
          <Text style={styles.backToListText}>‹ All meetings</Text>
        </Pressable>
        {visibleDetail ? (
          <>
            <View style={styles.hero}>
              <Text style={styles.heroTitle}>{visibleDetail.title}</Text>
              <Text style={styles.heroOutcome}>{visibleDetail.outcomePreview}</Text>
              <Text style={styles.heroMeta}>{coverageLabel(visibleDetail.coverageState)} · revision {visibleDetail.recordRevision.slice(0, 8)}</Text>
            </View>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel="Ask Scout about this meeting"
				accessibilityHint="Starts a private conversation bound to this exact Meeting Record revision"
				disabled={askScoutBusy}
				onPress={() => void askScoutAboutMeeting()}
				style={({ pressed }) => [styles.askScoutAction, pressed && styles.pressed]}
			>
				<Text style={styles.askScoutActionText}>{askScoutBusy ? 'Opening private conversation…' : 'Ask Scout about this meeting'}</Text>
			</Pressable>
			<ClaimSection title="Executive recap" claims={visibleDetail.executiveRecap} empty="No currently grounded recap is available." onOpenSource={openClaimSource} onOpenReference={openClaimReference} />
			<ClaimSection title="What everyone needs to know" claims={visibleDetail.needsToKnow} empty="No currently grounded highlights are available." onOpenSource={openClaimSource} onOpenReference={openClaimReference} />
			<ClaimSection title="Decisions" claims={visibleDetail.decisions} empty="No decision was resolved from the current sources." onOpenSource={openClaimSource} onOpenReference={openClaimReference} />
			<ClaimSection title="Commitments & follow-ups" claims={visibleDetail.commitments} empty="No commitment was resolved from the current sources." onOpenSource={openClaimSource} onOpenReference={openClaimReference} />
			<ClaimSection title="Blockers, risks & unresolved" claims={visibleDetail.blockers} empty="No current blocker or unresolved question was recorded." onOpenSource={openClaimSource} onOpenReference={openClaimReference} />
            <View style={styles.section}>
              <Text style={styles.sectionTitle}>People, Work, Projects & artifacts</Text>
              <Text style={styles.referenceText}>{visibleDetail.people.length ? visibleDetail.people.join(' · ') : 'No people resolved'}</Text>
              {[...visibleDetail.work, ...visibleDetail.projects, ...visibleDetail.artifacts].filter(meetingRecordReferenceHasExactDestination).map(reference => (
				<Pressable
				  key={`${reference.kind}-${reference.id}`}
				  accessibilityRole="button"
				  accessibilityLabel={`Open Meeting Record ${reference.kind === 'project' ? 'Project' : reference.kind === 'work' ? 'Work' : 'artifact'} ${reference.title}`}
				  onPress={() => openClaimReference(reference)}
				  style={({ pressed }) => pressed && styles.pressed}
				>
				  <Text style={styles.referenceLink}>{reference.kind === 'project' ? 'Project' : reference.kind === 'work' ? 'Work' : 'Artifact'} · {reference.title}</Text>
				</Pressable>
			  ))}
              {![...visibleDetail.work, ...visibleDetail.projects, ...visibleDetail.artifacts].some(meetingRecordReferenceHasExactDestination) ? <Text style={styles.emptySection}>No currently authorized Work, Project, or artifact references.</Text> : null}
            </View>
            <View style={styles.section}>
              <Text style={styles.sectionTitle}>Source coverage</Text>
              <Text style={styles.coverageLead}>{coverageLabel(visibleDetail.coverage.state)}</Text>
              <Text style={styles.referenceText}>{visibleDetail.coverage.transcriptCount} transcript segments · {visibleDetail.coverage.unavailableClaims} unavailable claims</Text>
              {visibleDetail.coverage.gaps.map((gap, index) => <Text key={`${gap}-${index}`} style={styles.gap}>• {gap}</Text>)}
            </View>
            <View
              onLayout={(event) => { transcriptSectionYRef.current = event.nativeEvent.layout.y; }}
              style={styles.section}
            >
              <Pressable accessibilityRole="button" accessibilityLabel={transcriptOpen ? 'Hide transcript' : 'Open transcript'} onPress={() => setTranscriptOpen(value => !value)} style={({ pressed }) => [styles.primaryAction, pressed && styles.pressed]}>
                <Text style={styles.primaryActionText}>{transcriptOpen ? 'Hide transcript' : 'Open transcript'}</Text>
              </Pressable>
              {transcriptOpen ? (
                <>
                  <TextInput
                    accessibilityLabel="Search transcript"
                    value={transcriptQuery}
                    onChangeText={setTranscriptQuery}
                    onSubmitEditing={() => void loadDetail(selectedId, { query: transcriptQuery })}
                    returnKeyType="search"
                    placeholder="Search speaker or exact words"
                    placeholderTextColor={colors.text3}
                    style={styles.search}
                  />
                  {visibleDetail.transcript.segments.map(segment => (
                    <TranscriptRow
                      key={`${segment.id}-${segment.revision}`}
                      onTargetLayout={revealExactSource}
                      segment={segment}
                      target={sourceTargetSegmentId === segment.id}
                      targetRef={sourceTargetRef}
                    />
                  ))}
                  {!visibleDetail.transcript.segments.length ? <Text style={styles.emptySection}>No authorized transcript segment matches.</Text> : null}
                  {visibleDetail.transcript.hasMore ? (
                    <Pressable
                      accessibilityRole="button"
                      accessibilityLabel="Load more transcript"
                      disabled={detailLoading}
                      onPress={() => void loadDetail(selectedId, { cursor: visibleDetail.transcript.nextCursor, query: visibleDetail.transcript.query, append: true })}
                      style={({ pressed }) => [styles.secondaryAction, pressed && styles.pressed]}
                    >
                      <Text style={styles.secondaryActionText}>{detailLoading ? 'Loading…' : 'Load more transcript'}</Text>
                    </Pressable>
                  ) : null}
                </>
              ) : null}
            </View>
          </>
        ) : null}
      </Screen>
    );
  }

  return (
    <Screen
      title="Meetings"
      subtitle="Permanent records, grounded in the sources you can read"
      loading={loading}
      error={error}
      onRetry={() => void loadRows()}
      refreshing={refreshing}
      onRefresh={() => void loadRows(true)}
    >
      {!visibleRows.length && !loading ? <Text style={styles.empty}>No authorized Meeting Records yet.</Text> : null}
      {visibleRows.map(row => (
        <Card
          key={`${row.id}-${row.recordRevision}`}
          title={row.title}
          subtitle={row.outcomePreview}
          meta={`${friendlyDate(row.startedAt)} · ${durationLabel(row.durationSeconds)} · ${row.transcriptCount} transcript ${row.transcriptCount === 1 ? 'segment' : 'segments'}`}
          badge={row.active ? 'Live' : coverageLabel(row.coverageState)}
          badgeTone={row.active ? 'live' : row.coverageState === 'full' ? 'muted' : 'warn'}
          onPress={() => selectMeeting(row.id)}
          accessibilityHint="Opens the current authorized Meeting Record."
        />
      ))}
	  {hasMoreMeetings ? (
		<Pressable accessibilityRole="button" accessibilityLabel="Load older Meeting Records" disabled={refreshing} onPress={() => void loadMoreMeetings()} style={({ pressed }) => [styles.secondaryAction, pressed && styles.pressed]}>
		  <Text style={styles.secondaryActionText}>{refreshing ? 'Loading…' : 'Load older meetings'}</Text>
		</Pressable>
	  ) : null}
    </Screen>
  );
}

const styles = StyleSheet.create({
  empty: { ...scalableText(type.bodySm), color: colors.text2 },
  pressed: { opacity: 0.76, transform: [{ scale: 0.98 }] },
  backToList: { minHeight: 44, justifyContent: 'center', alignSelf: 'flex-start', marginBottom: space[3] },
  backToListText: { ...scalableText(type.button), color: colors.accent },
  returnToRoom: { minHeight: 44, justifyContent: 'center', alignSelf: 'flex-start', marginBottom: space[2] },
  returnToRoomText: { ...scalableText(type.button), color: colors.accent },
  hero: { backgroundColor: colors.surface1, borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, padding: space[5], marginBottom: space[4] },
  heroTitle: { ...scalableText(type.title1), color: colors.text1 },
  heroOutcome: { ...scalableText(type.body), color: colors.text1, marginTop: space[3] },
  heroMeta: { ...scalableText(type.label), color: colors.text3, marginTop: space[3] },
  section: { marginBottom: space[5] },
  sectionTitle: { ...scalableText(type.headline), color: colors.text1, marginBottom: space[3] },
  claim: { borderLeftWidth: 2, borderLeftColor: colors.accent, paddingLeft: space[3], marginBottom: space[3] },
  claimText: { ...scalableText(type.bodySm), color: colors.text1 },
  claimMeta: { ...scalableText(type.label), color: colors.text2, marginTop: 5 },
  sourceMeta: { ...scalableText(type.caption), color: colors.text3, marginTop: 3 },
  emptySection: { ...scalableText(type.bodySm), color: colors.text3 },
  referenceText: { ...scalableText(type.bodySm), color: colors.text2 },
  referenceLink: { ...scalableText(type.bodySm), color: colors.accent, marginTop: 6 },
  coverageLead: { ...scalableText(type.bodySm), color: colors.text1, fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', marginBottom: 4 },
  gap: { ...scalableText(type.bodySm), color: colors.text2, marginTop: 6 },
  primaryAction: { minHeight: 46, borderRadius: radius.md, backgroundColor: colors.accent, alignItems: 'center', justifyContent: 'center' },
  primaryActionText: { ...scalableText(type.button), color: colors.onAccent },
  secondaryAction: { minHeight: 44, borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, alignItems: 'center', justifyContent: 'center', marginTop: space[3] },
  secondaryActionText: { ...scalableText(type.button), color: colors.text1 },
  search: { minHeight: 46, borderRadius: radius.md, backgroundColor: colors.surface1, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line1, color: colors.text1, paddingHorizontal: space[3], paddingVertical: space[2], ...scalableText(type.bodySm), marginTop: space[3], marginBottom: space[3] },
  transcriptRow: { paddingVertical: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  transcriptRowTarget: { marginHorizontal: -space[2], paddingHorizontal: space[2], borderRadius: radius.md, backgroundColor: colors.surface1, borderLeftWidth: 2, borderLeftColor: colors.accent },
  transcriptMetaRow: { flexDirection: 'row', justifyContent: 'space-between', gap: space[3] },
  transcriptSpeaker: { ...scalableText(type.label), color: colors.text1, flex: 1 },
  transcriptTime: { ...scalableText(type.caption), color: colors.text3 },
  transcriptText: { ...scalableText(type.bodySm), color: colors.text1, marginTop: 6 },
	askScoutAction: { minHeight: 46, borderRadius: radius.md, backgroundColor: colors.accent, alignItems: 'center', justifyContent: 'center', marginBottom: space[5], paddingHorizontal: space[3] },
	askScoutActionText: { ...scalableText(type.button), color: colors.onAccent, textAlign: 'center' },
});
