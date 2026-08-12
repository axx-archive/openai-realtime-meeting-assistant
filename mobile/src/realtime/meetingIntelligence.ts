export type MeetingIntelligenceFact = Readonly<{
  text: string;
  owner?: string;
  status?: string;
  sourceId?: string;
  at?: string;
}>;

export type MeetingIntelligenceSnapshot = Readonly<{
  contract: 'meeting-intelligence-v1';
  roomId: string;
  meetingId: string;
  revision: string;
  generatedAt: string;
  transcript: Readonly<{
    state: 'listening' | 'transcript_paused' | 'not_listening';
    captureHighWater: number;
    sequenceComplete: boolean;
    segmentCount: number;
    lastSegmentId?: string;
    lastCapturedAt?: string;
  }>;
  notes: Readonly<{
    state: 'current' | 'catching_up';
    revision?: string;
    updatedAt?: string;
    groundedThrough?: string;
    analysisCaptureHighWater: number;
    coverage: 'full' | 'partial_late_start' | 'partial_gaps' | 'partial_synthesis' | 'unknown';
  }>;
  scout: Readonly<{
    state: 'ready' | 'listening' | 'answering' | 'not_caught_up' | 'unavailable';
    groundedThrough?: string;
    sourceCount: number;
  }>;
  recap?: Readonly<{
    title?: string;
    topics: readonly MeetingIntelligenceFact[];
    decisions: readonly MeetingIntelligenceFact[];
    actions: readonly MeetingIntelligenceFact[];
    openQuestions: readonly MeetingIntelligenceFact[];
    risks: readonly MeetingIntelligenceFact[];
    themes: readonly string[];
    sourceCount: number;
  }>;
}>;

export type MeetingIntelligenceState = MeetingIntelligenceSnapshot | null;
export type MeetingIntelligenceAction =
  | Readonly<{ type: 'snapshot'; payload: unknown }>
  | Readonly<{ type: 'transcript_progress'; payload: unknown }>
  | Readonly<{ type: 'reset' }>;

function record(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function identifier(value: unknown): string {
  if (typeof value !== 'string') return '';
  const result = value.trim();
  return result && result.length <= 512 && !/[\u0000-\u001f\u007f]/u.test(result) ? result : '';
}

function timestamp(value: unknown): string {
  if (typeof value !== 'string') return '';
  const result = value.trim();
  return result && result.length <= 128 && Number.isFinite(Date.parse(result)) ? result : '';
}

function boundedText(value: unknown, max = 500): string {
  if (typeof value !== 'string') return '';
  return Array.from(value.replace(/[\u0000-\u001f\u007f]+/gu, ' ').replace(/\s+/gu, ' ').trim()).slice(0, max).join('');
}

function boundedInteger(value: unknown, max = Number.MAX_SAFE_INTEGER): number | null {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= max ? value : null;
}

function enumValue<T extends string>(value: unknown, admitted: readonly T[]): T | null {
  return typeof value === 'string' && admitted.includes(value as T) ? value as T : null;
}

function parseFacts(value: unknown): MeetingIntelligenceFact[] | null {
  if (!Array.isArray(value) || value.length > 32) return null;
  const facts: MeetingIntelligenceFact[] = [];
  for (const raw of value) {
    const item = record(raw);
    const text = boundedText(item?.text, 1_000);
    if (!item || !text) return null;
    const owner = boundedText(item.owner, 160);
    const status = boundedText(item.status, 80);
    const sourceId = identifier(item.sourceId);
    const at = item.at == null || item.at === '' ? '' : timestamp(item.at);
    if (item.at && !at) return null;
    facts.push({ text, ...(owner ? { owner } : {}), ...(status ? { status } : {}), ...(sourceId ? { sourceId } : {}), ...(at ? { at } : {}) });
  }
  return facts;
}

export function parseMeetingIntelligenceSnapshot(payload: unknown): MeetingIntelligenceSnapshot | null {
  const root = record(payload);
  const transcript = record(root?.transcript);
  const notes = record(root?.notes);
  const scout = record(root?.scout);
  if (!root || root.contract !== 'meeting-intelligence-v1' || !transcript || !notes || !scout) return null;
  const roomId = identifier(root.roomId);
  const meetingId = identifier(root.meetingId);
  const revision = identifier(root.revision);
  const generatedAt = timestamp(root.generatedAt);
  const transcriptState = enumValue(transcript.state, ['listening', 'transcript_paused', 'not_listening'] as const);
  const captureHighWater = boundedInteger(transcript.captureHighWater);
  const segmentCount = boundedInteger(transcript.segmentCount, 1_000_000);
  const notesState = enumValue(notes.state, ['current', 'catching_up'] as const);
  const analysisCaptureHighWater = boundedInteger(notes.analysisCaptureHighWater);
  const coverage = enumValue(notes.coverage, ['full', 'partial_late_start', 'partial_gaps', 'partial_synthesis', 'unknown'] as const);
  const scoutState = enumValue(scout.state, ['ready', 'listening', 'answering', 'not_caught_up', 'unavailable'] as const);
  const sourceCount = boundedInteger(scout.sourceCount, 10_000);
  if (!roomId || !meetingId || !revision || !generatedAt || !transcriptState || captureHighWater === null || segmentCount === null || typeof transcript.sequenceComplete !== 'boolean' || !notesState || analysisCaptureHighWater === null || !coverage || !scoutState || sourceCount === null) return null;

  let recap: MeetingIntelligenceSnapshot['recap'];
  if (root.recap != null) {
    const value = record(root.recap);
    const topics = parseFacts(value?.topics);
    const decisions = parseFacts(value?.decisions);
    const actions = parseFacts(value?.actions);
    const openQuestions = parseFacts(value?.openQuestions);
    const risks = parseFacts(value?.risks);
    const recapSourceCount = boundedInteger(value?.sourceCount, 10_000);
    if (!value || !topics || !decisions || !actions || !openQuestions || !risks || recapSourceCount === null || !Array.isArray(value.themes) || value.themes.length > 16) return null;
    const themes = value.themes.map((theme) => boundedText(theme, 240));
    if (themes.some((theme) => !theme)) return null;
    const title = boundedText(value.title, 240);
    recap = { ...(title ? { title } : {}), topics, decisions, actions, openQuestions, risks, themes, sourceCount: recapSourceCount };
  }

  const lastSegmentId = identifier(transcript.lastSegmentId);
  const lastCapturedAt = transcript.lastCapturedAt == null || transcript.lastCapturedAt === '' ? '' : timestamp(transcript.lastCapturedAt);
  const notesRevision = identifier(notes.revision);
  const notesUpdatedAt = notes.updatedAt == null || notes.updatedAt === '' ? '' : timestamp(notes.updatedAt);
  const groundedThrough = notes.groundedThrough == null || notes.groundedThrough === '' ? '' : timestamp(notes.groundedThrough);
  const scoutGroundedThrough = scout.groundedThrough == null || scout.groundedThrough === '' ? '' : timestamp(scout.groundedThrough);
  if ((transcript.lastCapturedAt && !lastCapturedAt) || (notes.updatedAt && !notesUpdatedAt) || (notes.groundedThrough && !groundedThrough) || (scout.groundedThrough && !scoutGroundedThrough)) return null;

  const transcriptHasSegments = segmentCount > 0;
  const analysisIsCurrent = transcriptHasSegments && transcript.sequenceComplete
    && captureHighWater > 0 && analysisCaptureHighWater === captureHighWater;
  if ((!transcriptHasSegments && (captureHighWater !== 0 || lastSegmentId || lastCapturedAt))
    || (transcriptHasSegments && (captureHighWater === 0 || !lastSegmentId || !lastCapturedAt))
    || analysisCaptureHighWater > captureHighWater
    || (notesState === 'current' && (!analysisIsCurrent || !notesRevision || !notesUpdatedAt || !recap))
    || (notesState === 'catching_up' && analysisIsCurrent)
    || (scoutState === 'ready' && notesState !== 'current')) return null;

  return {
    contract: 'meeting-intelligence-v1', roomId, meetingId, revision, generatedAt,
    transcript: { state: transcriptState, captureHighWater, sequenceComplete: transcript.sequenceComplete, segmentCount, ...(lastSegmentId ? { lastSegmentId } : {}), ...(lastCapturedAt ? { lastCapturedAt } : {}) },
    notes: { state: notesState, ...(notesRevision ? { revision: notesRevision } : {}), ...(notesUpdatedAt ? { updatedAt: notesUpdatedAt } : {}), ...(groundedThrough ? { groundedThrough } : {}), analysisCaptureHighWater, coverage },
    scout: { state: scoutState, ...(scoutGroundedThrough ? { groundedThrough: scoutGroundedThrough } : {}), sourceCount },
    ...(recap ? { recap } : {}),
  };
}

export function meetingIntelligenceReducer(
  state: MeetingIntelligenceState,
  action: MeetingIntelligenceAction,
): MeetingIntelligenceState {
  if (action.type === 'reset') return null;
  if (action.type === 'transcript_progress') {
    const entry = record(action.payload);
    const metadata = record(entry?.metadata);
    const meetingId = identifier(metadata?.meetingId);
    const roomId = identifier(metadata?.roomId || 'office');
    const rawSequence = typeof metadata?.captureSequence === 'string' ? metadata.captureSequence.trim() : '';
    const captureSequence = /^[1-9][0-9]{0,15}$/u.test(rawSequence) ? Number(rawSequence) : Number.NaN;
    const entryId = identifier(entry?.id);
    const createdAt = timestamp(entry?.createdAt);
    if (!state || meetingId !== state.meetingId || roomId !== state.roomId || !entryId || !createdAt
        || !Number.isSafeInteger(captureSequence) || captureSequence <= state.transcript.captureHighWater) return state;
    return {
      ...state,
      transcript: {
        ...state.transcript,
        captureHighWater: captureSequence,
        sequenceComplete: state.transcript.sequenceComplete && captureSequence === state.transcript.captureHighWater + 1,
        segmentCount: state.transcript.segmentCount + 1,
        lastSegmentId: entryId,
        lastCapturedAt: createdAt,
      },
      notes: { ...state.notes, state: 'catching_up' },
      scout: {
        ...state.scout,
        state: state.scout.state === 'unavailable' ? 'unavailable' : 'not_caught_up',
      },
    };
  }
  const next = parseMeetingIntelligenceSnapshot(action.payload);
  if (!next) return state;
  if (!state || state.meetingId !== next.meetingId || state.roomId !== next.roomId) return next;
  if (Date.parse(next.generatedAt) < Date.parse(state.generatedAt)) return state;
  if (next.transcript.captureHighWater < state.transcript.captureHighWater) return state;
  return next;
}

export function meetingIntelligenceStatusLabel(snapshot: MeetingIntelligenceSnapshot | null): string {
  if (!snapshot) return 'Meeting notes unavailable';
  if (snapshot.transcript.state === 'not_listening') return 'Not listening';
  if (snapshot.transcript.state === 'transcript_paused') return 'Transcript paused';
  return snapshot.notes.state === 'current' ? 'Listening · Notes current' : 'Listening · Notes catching up';
}
