export const ROOM_CHAT_HISTORY_LIMIT = 200;
export const ROOM_TRANSCRIPT_HISTORY_LIMIT = 200;
export const ROOM_CONVERSATION_UNREAD_LIMIT = 99;

const MAX_WIRE_ID_CODE_POINTS = 512;
const MAX_AUTHOR_CODE_POINTS = 160;
const MAX_CHAT_TEXT_CODE_POINTS = 4_000;
const MAX_TRANSCRIPT_TEXT_CODE_POINTS = 8_000;
const MAX_METADATA_ENTRIES = 64;
const MAX_METADATA_VALUE_CODE_POINTS = 4_096;

export type RoomConversationViewer = Readonly<{
  email?: string | null;
  name?: string | null;
}>;

export type RoomChatMessage = Readonly<{
  id: string;
  name: string;
  text: string;
  createdAt: string;
  roomId: string;
  artifactId?: string;
  workRunId?: string;
  workRootRunId?: string;
  workParentRunId?: string;
  workStatus?: 'queued' | 'running' | 'approval_required' | 'needs_input' | 'complete' | 'needs_attention';
  workFamily?: string;
  workTitle?: string;
  workProgress?: number;
  resultArtifactId?: string;
  resultArtifactType?: 'html_deck' | 'markdown' | 'pdf' | 'image' | 'table' | 'workbook' | 'bundle' | 'file';
  resultArtifactVersion?: number;
  resultArtifactDigest?: string;
  resultTitle?: string;
  authorEmail?: string;
  agentId?: string;
  replyTo?: string;
  model?: string;
  followThroughId?: string;
  followThroughStatus?: 'queued' | 'delivering' | 'delivered' | 'awaiting_input';
  destinationThreadId?: string;
  transient?: boolean;
  error?: boolean;
}>;

export type RoomTranscriptEntry = Readonly<{
  id: string;
  text: string;
  rawText: string;
  createdAt: string;
  roomId: string;
  speaker: string;
  source: string;
  captureSequence?: number;
  metadata: Readonly<Record<string, string>>;
}>;

export type RoomChatDelete = Readonly<{
  id: string;
  roomId: string;
}>;

export type RoomConversationState = Readonly<{
  roomId: string;
  messages: readonly RoomChatMessage[];
  transcriptEntries: readonly RoomTranscriptEntry[];
  unreadCount: number;
  historyEstablished: boolean;
}>;

export type RoomConversationAction =
  | Readonly<{
    type: 'room_chat_history';
    payload: unknown;
    chatOpen?: boolean;
    viewer?: RoomConversationViewer;
  }>
  | Readonly<{
    type: 'room_chat';
    payload: unknown;
    chatOpen: boolean;
    viewer?: RoomConversationViewer;
  }>
  | Readonly<{ type: 'room_chat_delete'; payload: unknown }>
  | Readonly<{ type: 'memory_transcript'; payload: unknown }>
  | Readonly<{ type: 'meeting_transcript_snapshot'; payload: unknown }>
  | Readonly<{ type: 'mark_read' }>
  | Readonly<{ type: 'reset'; roomId?: string }>;

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function limitedCodePoints(value: string, limit: number): string {
  const codePoints = Array.from(value);
  return codePoints.length <= limit ? value : codePoints.slice(0, limit).join('');
}

function wireIdentifier(value: unknown): string {
  if (typeof value !== 'string') return '';
  const trimmed = value.trim();
  if (!trimmed || Array.from(trimmed).length > MAX_WIRE_ID_CODE_POINTS) return '';
  return /[\u0000-\u001f\u007f]/u.test(trimmed) ? '' : trimmed;
}

function normalizedRoomId(value: unknown): string {
  return wireIdentifier(value) || 'office';
}

function normalizedEmail(value: unknown): string {
  const email = wireIdentifier(value).toLowerCase();
  return email.length <= 320 ? email : '';
}

function normalizedResultArtifactType(value: unknown): RoomChatMessage['resultArtifactType'] {
  const kind = wireIdentifier(value).toLowerCase();
  return ['html_deck', 'markdown', 'pdf', 'image', 'table', 'workbook', 'bundle', 'file'].includes(kind)
    ? kind as RoomChatMessage['resultArtifactType']
    : undefined;
}

function normalizedAuthor(value: unknown): string {
  if (typeof value !== 'string') return '';
  return limitedCodePoints(
    value.replace(/[\u0000-\u001f\u007f]+/gu, ' ').replace(/\s+/gu, ' ').trim(),
    MAX_AUTHOR_CODE_POINTS,
  );
}

function normalizedText(value: unknown, limit: number): string {
  if (typeof value !== 'string') return '';
  const text = value
    .replace(/\r\n?/gu, '\n')
    .replace(/[\u0000\u0008\u000b\u000c\u000e-\u001f\u007f]/gu, '')
    .trim();
  return limitedCodePoints(text, limit).trim();
}

/** Preserve the server's RFC3339Nano value; never mint a client clock value. */
function wireTimestamp(value: unknown): string {
  if (typeof value !== 'string') return '';
  const timestamp = value.trim();
  if (!timestamp || timestamp.length > 128 || !Number.isFinite(Date.parse(timestamp))) return '';
  return timestamp;
}

function parseMetadata(value: unknown): Record<string, string> {
  if (!isRecord(value)) return {};
  const metadata: Record<string, string> = {};
  let accepted = 0;
  for (const [rawKey, rawValue] of Object.entries(value)) {
    if (accepted >= MAX_METADATA_ENTRIES) break;
    const key = wireIdentifier(rawKey);
    if (!key || typeof rawValue !== 'string') continue;
    const metadataValue = limitedCodePoints(rawValue.trim(), MAX_METADATA_VALUE_CODE_POINTS);
    if (!metadataValue) continue;
    metadata[key] = metadataValue;
    accepted += 1;
  }
  return metadata;
}

function transcriptCaptureSequence(metadata: Readonly<Record<string, string>>): number | undefined {
  const value = Number(metadata.captureSequence);
  return Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

function orderedTranscriptEntries(values: readonly RoomTranscriptEntry[]): readonly RoomTranscriptEntry[] {
  return [...values].sort((left, right) => {
    if (left.captureSequence && right.captureSequence && left.captureSequence !== right.captureSequence) {
      return left.captureSequence - right.captureSequence;
    }
    const byTime = Date.parse(left.createdAt) - Date.parse(right.createdAt);
    return byTime;
  });
}

function transcriptBody(rawText: string, speaker: string): string {
  if (!speaker) return rawText;
  const prefix = `${speaker}:`;
  if (rawText.slice(0, prefix.length).toLowerCase() !== prefix.toLowerCase()) {
    return rawText;
  }
  return rawText.slice(prefix.length).trim();
}

export function parseRoomChatMessage(payload: unknown): RoomChatMessage | null {
  if (!isRecord(payload)) return null;
  const id = wireIdentifier(payload.id);
  const text = normalizedText(payload.text, MAX_CHAT_TEXT_CODE_POINTS);
  const createdAt = wireTimestamp(payload.createdAt);
  if (!id || !text || !createdAt) return null;

  const name = normalizedAuthor(payload.name) || 'Guest';
  const roomId = normalizedRoomId(payload.roomId);
  const artifactId = wireIdentifier(payload.artifactId);
  const workRunId = wireIdentifier(payload.workRunId);
  const workRootRunId = wireIdentifier(payload.workRootRunId);
  const workParentRunId = wireIdentifier(payload.workParentRunId);
  const rawWorkStatus = wireIdentifier(payload.workStatus).toLowerCase();
  const workStatus = ['queued', 'running', 'approval_required', 'needs_input', 'complete', 'needs_attention'].includes(rawWorkStatus)
    ? rawWorkStatus as RoomChatMessage['workStatus']
    : undefined;
  const workFamily = normalizedText(payload.workFamily, MAX_AUTHOR_CODE_POINTS);
  const workTitle = normalizedText(payload.workTitle, MAX_CHAT_TEXT_CODE_POINTS);
  const rawWorkProgress = Number(payload.workProgress);
  const workProgress = Number.isFinite(rawWorkProgress) && rawWorkProgress >= 0 && rawWorkProgress <= 100
    ? Math.round(rawWorkProgress)
    : undefined;
  const resultArtifactId = wireIdentifier(payload.resultArtifactId);
  const resultArtifactType = normalizedResultArtifactType(payload.resultArtifactType);
  const rawResultArtifactVersion = Number(payload.resultArtifactVersion);
  const resultArtifactVersion = Number.isSafeInteger(rawResultArtifactVersion) && rawResultArtifactVersion > 0
    ? rawResultArtifactVersion
    : undefined;
  const rawResultArtifactDigest = wireIdentifier(payload.resultArtifactDigest).toLowerCase();
  const resultArtifactDigest = /^[0-9a-f]{64}$/u.test(rawResultArtifactDigest)
    ? rawResultArtifactDigest
    : undefined;
  const resultTitle = normalizedText(payload.resultTitle, MAX_CHAT_TEXT_CODE_POINTS);
  const authorEmail = normalizedEmail(payload.authorEmail);
  const agentId = wireIdentifier(payload.agentId).toLowerCase();
  const replyTo = wireIdentifier(payload.replyTo);
  const model = wireIdentifier(payload.model);
  const followThroughId = wireIdentifier(payload.followThroughId);
  const rawFollowThroughStatus = wireIdentifier(payload.followThroughStatus);
  const followThroughStatus = ['queued', 'delivering', 'delivered', 'awaiting_input'].includes(rawFollowThroughStatus)
    ? rawFollowThroughStatus as RoomChatMessage['followThroughStatus']
    : undefined;
  const destinationThreadId = wireIdentifier(payload.destinationThreadId);
  return {
    id,
    name,
    text,
    createdAt,
    roomId,
    ...(artifactId ? { artifactId } : {}),
    ...(workRunId && workStatus ? { workRunId, workStatus } : {}),
    ...(workRunId && workRootRunId ? { workRootRunId } : {}),
    ...(workRunId && workParentRunId ? { workParentRunId } : {}),
    ...(workRunId && workFamily ? { workFamily } : {}),
    ...(workRunId && workTitle ? { workTitle } : {}),
    ...(workRunId && workProgress !== undefined ? { workProgress } : {}),
    ...(workRunId && resultArtifactId ? { resultArtifactId } : {}),
    ...(workRunId && resultArtifactType ? { resultArtifactType } : {}),
    ...(workRunId && resultArtifactVersion !== undefined ? { resultArtifactVersion } : {}),
    ...(workRunId && resultArtifactDigest ? { resultArtifactDigest } : {}),
    ...(workRunId && resultTitle ? { resultTitle } : {}),
    ...(authorEmail ? { authorEmail } : {}),
    ...(agentId ? { agentId } : {}),
    ...(replyTo ? { replyTo } : {}),
    ...(model ? { model } : {}),
    ...(followThroughId && followThroughStatus ? { followThroughId, followThroughStatus } : {}),
    ...(followThroughId && destinationThreadId ? { destinationThreadId } : {}),
    ...(payload.transient === true ? { transient: true } : {}),
    ...(payload.error === true ? { error: true } : {}),
  };
}

export function parseRoomChatHistory(payload: unknown): RoomChatMessage[] | null {
  if (!Array.isArray(payload)) return null;
  const messages: RoomChatMessage[] = [];
  const seen = new Set<string>();
  for (const value of payload) {
    const message = parseRoomChatMessage(value);
    if (!message || seen.has(message.id)) continue;
    seen.add(message.id);
    messages.push(message);
  }
  const latestWorkIndex = new Map<string, number>();
  messages.forEach((message, index) => {
    if (message.workRunId) latestWorkIndex.set(message.workRunId, index);
  });
  return messages.filter((message, index) => !message.workRunId || latestWorkIndex.get(message.workRunId) === index);
}

export function parseRoomChatDelete(payload: unknown): RoomChatDelete | null {
  if (!isRecord(payload)) return null;
  const id = wireIdentifier(payload.id);
  if (!id) return null;
  return { id, roomId: normalizedRoomId(payload.roomId) };
}

export function parseMemoryTranscriptEntry(payload: unknown): RoomTranscriptEntry | null {
  if (!isRecord(payload) || payload.kind !== 'transcript') return null;
  const id = wireIdentifier(payload.id);
  const rawText = normalizedText(payload.text, MAX_TRANSCRIPT_TEXT_CODE_POINTS);
  const createdAt = wireTimestamp(payload.createdAt);
  if (!id || !rawText || !createdAt) return null;

  const metadata = parseMetadata(payload.metadata);
  const speaker = normalizedAuthor(metadata.speaker);
  const text = transcriptBody(rawText, speaker);
  const captureSequence = transcriptCaptureSequence(metadata);
  if (!text) return null;
  return {
    id,
    text,
    rawText,
    createdAt,
    roomId: normalizedRoomId(metadata.roomId),
    speaker,
    source: wireIdentifier(metadata.source),
    ...(captureSequence ? { captureSequence } : {}),
    metadata,
  };
}

export function parseMeetingTranscriptSnapshot(payload: unknown): Readonly<{
  roomId: string;
  meetingId: string;
  entries: readonly RoomTranscriptEntry[];
}> | null {
  if (!isRecord(payload) || payload.contract !== 'meeting-transcript-v1' || !Array.isArray(payload.entries)
    || payload.entries.length > ROOM_TRANSCRIPT_HISTORY_LIMIT) return null;
  const roomId = wireIdentifier(payload.roomId);
  const meetingId = wireIdentifier(payload.meetingId);
  if (!roomId || !meetingId) return null;
  const entries: RoomTranscriptEntry[] = [];
  const seen = new Set<string>();
  const seenSequences = new Set<number>();
  let priorSequence = 0;
  for (const raw of payload.entries) {
    const entry = parseMemoryTranscriptEntry(raw);
    if (!entry || !entry.captureSequence || entry.roomId !== normalizedRoomId(roomId) || entry.metadata.meetingId !== meetingId
      || seen.has(entry.id) || seenSequences.has(entry.captureSequence) || entry.captureSequence <= priorSequence) return null;
    seen.add(entry.id);
    seenSequences.add(entry.captureSequence);
    priorSequence = entry.captureSequence;
    entries.push(entry);
  }
  return { roomId: normalizedRoomId(roomId), meetingId, entries: orderedTranscriptEntries(entries) };
}

export function roomChatMessageIsOwn(
  message: RoomChatMessage,
  viewer: RoomConversationViewer | undefined,
): boolean {
  const authorEmail = normalizedEmail(message.authorEmail);
  const viewerEmail = normalizedEmail(viewer?.email);
  if (authorEmail && viewerEmail) return authorEmail === viewerEmail;

  const authorName = normalizedAuthor(message.name).toLowerCase();
  const viewerName = normalizedAuthor(viewer?.name).toLowerCase();
  return Boolean(authorName && viewerName && authorName === viewerName);
}

/**
 * Room chat is conversation, decisions, and exact typed deliverables. Work
 * lifecycle and follow-through records belong to Activity even when an older
 * server serialized them as chat messages.
 */
export function roomChatMessageBelongsInConversation(message: RoomChatMessage): boolean {
  if (!message.workRunId) return !message.followThroughId;
  const version = Number(message.resultArtifactVersion ?? 0);
  const digest = String(message.resultArtifactDigest ?? '').trim().toLowerCase();
  return message.workStatus === 'complete'
    && Boolean(message.resultArtifactId)
    && Boolean(message.resultArtifactType)
    && Number.isSafeInteger(version)
    && version > 0
    && /^[0-9a-f]{64}$/u.test(digest);
}

function roomWorkRootIdentity(message: RoomChatMessage): string {
  const runId = String(message.workRunId ?? '').trim();
  const rootRunId = String(message.workRootRunId ?? '').trim();
  const parentRunId = String(message.workParentRunId ?? '').trim();
  // A direct run is its own root. Delegated work must carry the server-owned
  // root explicitly; never infer ancestry from display names or event order.
  if (parentRunId && !rootRunId) return '';
  return rootRunId || runId;
}

export function roomConversationFeedMessages(
  messages: readonly RoomChatMessage[],
): readonly RoomChatMessage[] {
  const newestRootResult = new Map<string, number>();
  const newestArtifactResult = new Map<string, number>();
  messages.forEach((message, index) => {
    if (!roomChatMessageBelongsInConversation(message) || !message.workRunId) return;
    const rootIdentity = roomWorkRootIdentity(message);
    if (!rootIdentity) return;
    newestRootResult.set(rootIdentity, index);
    if (message.resultArtifactId) newestArtifactResult.set(message.resultArtifactId, index);
  });
  return messages.filter((message, index) => {
    if (!roomChatMessageBelongsInConversation(message)) return false;
    if (!message.workRunId) return true;
    const rootIdentity = roomWorkRootIdentity(message);
    return Boolean(rootIdentity)
      && newestRootResult.get(rootIdentity) === index
      && newestArtifactResult.get(message.resultArtifactId || '') === index;
  });
}

export function roomConversationActivityMessages(
  messages: readonly RoomChatMessage[],
): readonly RoomChatMessage[] {
  const newestByIdentity = new Map<string, RoomChatMessage>();
  messages.forEach((message) => {
    const identity = message.workRunId
      ? `work:${message.workRunId}`
      : message.followThroughId
        ? `follow:${message.followThroughId}`
        : '';
    if (!identity) return;
    // Reinsert revised runs so iteration order remains durable event order.
    // The sheet presents one row per exact run, never a wall of revisions.
    newestByIdentity.delete(identity);
    newestByIdentity.set(identity, message);
  });
  return [...newestByIdentity.values()];
}

export function latestRoomConversationActivity(
  messages: readonly RoomChatMessage[],
): RoomChatMessage | null {
  const activity = roomConversationActivityMessages(messages);
  return activity.length ? activity[activity.length - 1] : null;
}

function tail<T>(values: readonly T[], limit: number): readonly T[] {
  return values.length <= limit ? values : values.slice(values.length - limit);
}

function sameRoom(left: string, right: string): boolean {
  return normalizedRoomId(left) === normalizedRoomId(right);
}

export function createRoomConversationState(roomId = 'office'): RoomConversationState {
  return {
    roomId: normalizedRoomId(roomId),
    messages: [],
    transcriptEntries: [],
    unreadCount: 0,
    historyEstablished: false,
  };
}

/**
 * Pure state machine for the decoded websocket payloads. A valid history array
 * is authoritative, while malformed frames are ignored so transient protocol
 * corruption cannot erase an already-rendered conversation.
 */
export function roomConversationReducer(
  state: RoomConversationState,
  action: RoomConversationAction,
): RoomConversationState {
  switch (action.type) {
    case 'room_chat_history': {
      const history = parseRoomChatHistory(action.payload);
      if (history === null) return state;
      const messages = tail(
        history.filter((message) => sameRoom(message.roomId, state.roomId)),
        ROOM_CHAT_HISTORY_LIMIT,
      );
      // The first valid replay establishes the read baseline for a fresh join;
      // historical messages are not notifications. Later replays come from a
      // reconnected socket, so count only IDs that were not already delivered,
      // applying the same ownership and open-chat rules as live messages.
      if (!state.historyEstablished) {
        return { ...state, messages, historyEstablished: true };
      }
      const knownIds = new Set(state.messages.map((message) => message.id));
      const newUnread = action.chatOpen
        ? 0
        : messages.reduce((count, message) => (
          !knownIds.has(message.id)
            && roomChatMessageBelongsInConversation(message)
            && !roomChatMessageIsOwn(message, action.viewer)
            ? count + 1
            : count
        ), 0);
      return {
        ...state,
        messages,
        unreadCount: Math.min(
          state.unreadCount + newUnread,
          ROOM_CONVERSATION_UNREAD_LIMIT,
        ),
        historyEstablished: true,
      };
    }
    case 'room_chat': {
      const message = parseRoomChatMessage(action.payload);
      if (!message || !sameRoom(message.roomId, state.roomId)) return state;
      if (state.messages.some((candidate) => candidate.id === message.id)) return state;
      const priorWorkIndex = message.workRunId
        ? state.messages.findIndex((candidate) => candidate.workRunId === message.workRunId)
        : -1;
      const nextMessages = priorWorkIndex >= 0
        ? [...state.messages.filter((_, index) => index !== priorWorkIndex), message]
        : [...state.messages, message];

      const unreadCount = !action.chatOpen
        && roomChatMessageBelongsInConversation(message)
        && !roomChatMessageIsOwn(message, action.viewer)
        ? Math.min(state.unreadCount + 1, ROOM_CONVERSATION_UNREAD_LIMIT)
        : state.unreadCount;
      return {
        ...state,
        messages: tail(nextMessages, ROOM_CHAT_HISTORY_LIMIT),
        unreadCount,
      };
    }
    case 'room_chat_delete': {
      const deletion = parseRoomChatDelete(action.payload);
      if (!deletion || !sameRoom(deletion.roomId, state.roomId)) return state;
      const messages = state.messages.filter((message) => message.id !== deletion.id);
      const transcriptEntries = state.transcriptEntries.filter((entry) => (
        entry.id !== deletion.id || entry.source !== 'room_chat'
      ));
      if (messages.length === state.messages.length && transcriptEntries.length === state.transcriptEntries.length) {
        return state;
      }
      // Unread is an arrival counter, matching the web room-chat surface: a
      // delete does not guess whether the removed message had already been read.
      return { ...state, messages, transcriptEntries };
    }
    case 'memory_transcript': {
      const entry = parseMemoryTranscriptEntry(action.payload);
      if (!entry || !entry.captureSequence || !sameRoom(entry.roomId, state.roomId)) return state;
      if (state.transcriptEntries.some((candidate) => candidate.id === entry.id)) return state;
      return {
        ...state,
        transcriptEntries: tail(
          orderedTranscriptEntries([...state.transcriptEntries, entry]),
          ROOM_TRANSCRIPT_HISTORY_LIMIT,
        ),
      };
    }
    case 'meeting_transcript_snapshot': {
      const snapshot = parseMeetingTranscriptSnapshot(action.payload);
      if (!snapshot || !sameRoom(snapshot.roomId, state.roomId)) return state;
      const merged = new Map<string, RoomTranscriptEntry>();
      state.transcriptEntries
        .filter((entry) => entry.metadata.meetingId === snapshot.meetingId)
        .forEach((entry) => merged.set(entry.id, entry));
      snapshot.entries.forEach((entry) => merged.set(entry.id, entry));
      return { ...state, transcriptEntries: tail(orderedTranscriptEntries([...merged.values()]), ROOM_TRANSCRIPT_HISTORY_LIMIT) };
    }
    case 'mark_read':
      return state.unreadCount === 0 ? state : { ...state, unreadCount: 0 };
    case 'reset':
      return createRoomConversationState(action.roomId ?? state.roomId);
    default:
      return state;
  }
}
