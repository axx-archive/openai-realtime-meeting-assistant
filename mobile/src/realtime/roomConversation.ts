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
  authorEmail?: string;
  agentId?: string;
  replyTo?: string;
  model?: string;
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
  const authorEmail = normalizedEmail(payload.authorEmail);
  const agentId = wireIdentifier(payload.agentId).toLowerCase();
  const replyTo = wireIdentifier(payload.replyTo);
  const model = wireIdentifier(payload.model);
  return {
    id,
    name,
    text,
    createdAt,
    roomId,
    ...(artifactId ? { artifactId } : {}),
    ...(authorEmail ? { authorEmail } : {}),
    ...(agentId ? { agentId } : {}),
    ...(replyTo ? { replyTo } : {}),
    ...(model ? { model } : {}),
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
  return messages;
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
  if (!text) return null;
  return {
    id,
    text,
    rawText,
    createdAt,
    roomId: normalizedRoomId(metadata.roomId),
    speaker,
    source: wireIdentifier(metadata.source),
    metadata,
  };
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
          !knownIds.has(message.id) && !roomChatMessageIsOwn(message, action.viewer)
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

      const unreadCount = !action.chatOpen && !roomChatMessageIsOwn(message, action.viewer)
        ? Math.min(state.unreadCount + 1, ROOM_CONVERSATION_UNREAD_LIMIT)
        : state.unreadCount;
      return {
        ...state,
        messages: tail([...state.messages, message], ROOM_CHAT_HISTORY_LIMIT),
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
      if (!entry || !sameRoom(entry.roomId, state.roomId)) return state;
      if (state.transcriptEntries.some((candidate) => candidate.id === entry.id)) return state;
      return {
        ...state,
        transcriptEntries: tail(
          [...state.transcriptEntries, entry],
          ROOM_TRANSCRIPT_HISTORY_LIMIT,
        ),
      };
    }
    case 'mark_read':
      return state.unreadCount === 0 ? state : { ...state, unreadCount: 0 };
    case 'reset':
      return createRoomConversationState(action.roomId ?? state.roomId);
    default:
      return state;
  }
}
