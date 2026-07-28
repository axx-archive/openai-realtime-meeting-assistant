import {
  API_BASE_URL,
  NATIVE_CLIENT_HEADER,
} from '../config';
import type {
  BoardResponse,
  BoardCardInput,
  ConsentDecisionResponse,
  ConsentDisposition,
  ConsentScope,
  ConsentStatus,
  Identity,
  RoomsResponse,
  ScoutThreadDetailResponse,
  ScoutThreadsResponse,
  ScoutFileAttachment,
  ThreadDigestResponse,
} from './types';
import {
  buildConsentDecision,
  parseConsentDecisionResponse,
  parseConsentStatus,
} from './consent';
import { buildApiUrl, buildAuthHeaders } from './requestHelpers';

type UnauthorizedHandler = () => void;
let unauthorizedHandler: UnauthorizedHandler | null = null;

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null) {
  unauthorizedHandler = handler;
}

export class BonfireApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'BonfireApiError';
    this.status = status;
  }
}

type RequestOptions = {
  method?: string;
  body?: unknown;
  sessionToken?: string | null;
  signal?: AbortSignal;
  headers?: Record<string, string>;
};

async function requestWithResponse<T>(
  path: string,
  options: RequestOptions = {},
): Promise<{ data: T; response: Response }> {
  const url = buildApiUrl(API_BASE_URL, path);
  const headers = buildAuthHeaders(
    NATIVE_CLIENT_HEADER,
    options.sessionToken,
    options.headers,
  );

  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json';
  }

  const response = await fetch(url, {
    method: options.method ?? (options.body !== undefined ? 'POST' : 'GET'),
    headers,
    body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    signal: options.signal,
  });

  const text = await response.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }

  if (!response.ok) {
    const message =
      (data && typeof data === 'object' && 'error' in data && typeof (data as { error: unknown }).error === 'string'
        ? (data as { error: string }).error
        : null) ||
      `Request failed (${response.status})`;
    if (response.status === 401) unauthorizedHandler?.();
    throw new BonfireApiError(response.status, message);
  }

  return { data: data as T, response };
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  return (await requestWithResponse<T>(path, options)).data;
}

export const api = {
  login(name: string, password: string): Promise<Identity> {
    return request<Identity>('/auth/login', {
      method: 'POST',
      body: { name: name.trim(), password },
    });
  },

  requestPasswordReset(email: string): Promise<{ ok?: boolean }> {
    return request('/auth/reset/request', {
      method: 'POST',
      body: { email: email.trim() },
    });
  },

  confirmPasswordReset(token: string, newPassword: string): Promise<{ ok?: boolean }> {
    return request('/auth/reset/confirm', {
      method: 'POST',
      body: { token, newPassword },
    });
  },

  async beginPasskeyLogin(): Promise<{ publicKey: Record<string, unknown>; ceremony: string }> {
    const { data, response } = await requestWithResponse<{ publicKey: Record<string, unknown> }>(
      '/auth/passkey/login/begin',
      { method: 'POST', body: {} },
    );
    const ceremony = response.headers.get('X-Bonfire-WebAuthn-Ceremony') ?? '';
    if (!ceremony) throw new Error('The server did not start a native passkey session.');
    return { publicKey: data.publicKey, ceremony };
  },

  finishPasskeyLogin(ceremony: string, credential: unknown): Promise<Identity> {
    return request<Identity>('/auth/passkey/login/finish', {
      method: 'POST',
      body: credential,
      headers: { 'X-Bonfire-WebAuthn-Ceremony': ceremony },
    });
  },

  me(sessionToken: string): Promise<Identity> {
    return request<Identity>('/auth/me', { sessionToken });
  },

  logout(sessionToken: string): Promise<unknown> {
    return request('/auth/logout', { method: 'POST', body: {}, sessionToken });
  },

  changePassword(
    sessionToken: string,
    currentPassword: string,
    newPassword: string,
  ): Promise<Identity> {
    return request<Identity>('/auth/change-password', {
      method: 'POST',
      body: { currentPassword, newPassword },
      sessionToken,
    });
  },

  updateProfile(
    sessionToken: string,
    displayName: string,
    avatarDataURL = '',
  ): Promise<Identity> {
    return request<Identity>('/auth/profile', {
      method: 'POST',
      body: { displayName, avatarDataURL },
      sessionToken,
    });
  },

  setTheme(sessionToken: string, theme: 'light' | 'dark' | 'system'): Promise<Identity> {
    return request<Identity>('/auth/theme', {
      method: 'POST',
      body: { theme },
      sessionToken,
    });
  },

  passkeys(sessionToken: string): Promise<{ passkeys: Array<{ id: string; label: string }> }> {
    return request('/auth/passkeys', { sessionToken });
  },

  async beginPasskeyRegistration(
    sessionToken: string,
  ): Promise<{ publicKey: Record<string, unknown>; ceremony: string }> {
    const { data, response } = await requestWithResponse<{ publicKey: Record<string, unknown> }>(
      '/auth/passkey/register/begin',
      { method: 'POST', body: {}, sessionToken },
    );
    const ceremony = response.headers.get('X-Bonfire-WebAuthn-Ceremony') ?? '';
    if (!ceremony) throw new Error('The server did not start a native passkey session.');
    return { publicKey: data.publicKey, ceremony };
  },

  finishPasskeyRegistration(
    sessionToken: string,
    ceremony: string,
    credential: unknown,
  ): Promise<{ passkeys: Array<{ id: string; label: string }> }> {
    return request('/auth/passkey/register/finish', {
      method: 'POST',
      body: credential,
      sessionToken,
      headers: { 'X-Bonfire-WebAuthn-Ceremony': ceremony },
    });
  },

  deletePasskey(sessionToken: string, id: string) {
    return request<{ passkeys: Array<{ id: string; label: string }> }>('/auth/passkey/delete', {
      method: 'POST',
      body: { id },
      sessionToken,
    });
  },

  rooms(sessionToken: string): Promise<RoomsResponse> {
    return request<RoomsResponse>('/rooms', { sessionToken });
  },

  createRoom(
    sessionToken: string,
    body: { name: string; passcode?: string; guestAccess?: boolean },
  ): Promise<{ ok: boolean; room: import('./types').Room }> {
    return request('/rooms', { method: 'POST', body, sessionToken });
  },

  setRoomPasscode(sessionToken: string, roomId: string, passcode: string) {
    return request<{ ok: boolean; passcodeRequired: boolean }>(
      `/rooms/${encodeURIComponent(roomId)}/passcode`,
      { method: 'POST', body: { passcode }, sessionToken },
    );
  },

  archiveRoom(sessionToken: string, roomId: string) {
    return request<{ ok: boolean }>(`/rooms/${encodeURIComponent(roomId)}/archive`, {
      method: 'POST', body: {}, sessionToken,
    });
  },

  restoreRoom(sessionToken: string, roomId: string) {
    return request<{ ok: boolean }>(`/rooms/${encodeURIComponent(roomId)}/restore`, {
      method: 'POST', body: {}, sessionToken,
    });
  },

  roomGuestLinks(sessionToken: string, roomId: string) {
    return request<{ ok: boolean; links: Array<{ id: string; label?: string; expiresAt?: string }> }>(
      `/rooms/${encodeURIComponent(roomId)}/guest-links`,
      { sessionToken },
    );
  },

  createRoomGuestLink(sessionToken: string, roomId: string, label: string, ttlHours = 72) {
    return request<{ ok: boolean; url: string; link: { id: string; label?: string; expiresAt?: string } }>(
      `/rooms/${encodeURIComponent(roomId)}/guest-links`,
      { method: 'POST', body: { label, ttlHours }, sessionToken },
    );
  },

  revokeRoomGuestLink(sessionToken: string, roomId: string, id: string) {
    return request<{ ok: boolean }>(
      `/rooms/${encodeURIComponent(roomId)}/guest-links/revoke`,
      { method: 'POST', body: { id }, sessionToken },
    );
  },

  participants(sessionToken: string, roomId: string): Promise<Record<string, unknown>> {
    return request(`/participants?room=${encodeURIComponent(roomId)}`, { sessionToken });
  },

  clientConfig(sessionToken: string): Promise<{
    rtcConfiguration: { iceServers?: Array<Record<string, unknown>> };
    websocketPath?: string;
    supportedLayers?: string[];
  }> {
    return request('/client-config', { sessionToken });
  },

  async getConsentStatus(sessionToken: string): Promise<ConsentStatus> {
    const payload = await request<unknown>('/api/consent', { sessionToken });
    return parseConsentStatus(payload);
  },

  async setConsentDecision(
    sessionToken: string,
    scope: ConsentScope,
    disposition: ConsentDisposition,
  ): Promise<ConsentDecisionResponse> {
    const payload = await request<unknown>('/api/consent', {
      method: 'POST',
      body: buildConsentDecision(scope, disposition),
      sessionToken,
    });
    return parseConsentDecisionResponse(payload);
  },

  board(sessionToken: string): Promise<BoardResponse> {
    return request<BoardResponse>('/assistant/board', { sessionToken });
  },

  createBoardCard(sessionToken: string, card: BoardCardInput) {
    return request<{ ok: boolean; changed: boolean; card?: import('./types').BoardCard }>(
      '/assistant/board/cards',
      { method: 'POST', body: card, sessionToken },
    );
  },

  updateBoardCard(sessionToken: string, cardId: string, card: BoardCardInput) {
    return request<{ ok: boolean; changed: boolean; card?: import('./types').BoardCard }>(
      `/assistant/board/cards/${encodeURIComponent(cardId)}`,
      { method: 'PUT', body: card, sessionToken },
    );
  },

  deleteBoardCard(sessionToken: string, cardId: string) {
    return request<{ ok: boolean; changed: boolean }>(
      `/assistant/board/cards/${encodeURIComponent(cardId)}`,
      { method: 'DELETE', sessionToken },
    );
  },

  undoDeleteBoardCard(sessionToken: string) {
    return request<{ ok: boolean; changed: boolean; card?: import('./types').BoardCard }>(
      '/assistant/board/cards/undo',
      { method: 'POST', sessionToken },
    );
  },

  resolveBoardDraft(
    sessionToken: string,
    cardId: string,
    action: 'accept' | 'dismiss',
    reason = '',
  ) {
    return request<{ ok: boolean; action: string; card?: import('./types').BoardCard }>(
      `/assistant/board/drafts/${encodeURIComponent(cardId)}/${action}`,
      { method: 'POST', body: { reason }, sessionToken },
    );
  },

  memory(sessionToken: string): Promise<{ ok: boolean; memory: unknown }> {
    return request('/assistant/memory', { sessionToken });
  },

  meetings(sessionToken: string): Promise<{ ok: boolean; meetings: unknown[]; serverNow?: string }> {
    return request('/assistant/meetings?limit=60', { sessionToken });
  },

  files(sessionToken: string): Promise<{ ok: boolean; files: unknown[]; folders: unknown[] }> {
    return request('/assistant/files', { sessionToken });
  },

  async uploadFile(
    sessionToken: string,
    file: { uri: string; name: string; mime: string },
  ): Promise<{ ok: boolean; file: Record<string, unknown> }> {
    const form = new FormData();
    form.append('file', {
      uri: file.uri,
      name: file.name,
      type: file.mime,
    } as unknown as Blob);
    const response = await fetch(buildApiUrl(API_BASE_URL, '/assistant/files/upload'), {
      method: 'POST',
      headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken),
      body: form,
    });
    const payload = await response.json() as { ok?: boolean; file?: Record<string, unknown>; error?: string };
    if (!response.ok || !payload.file) {
      if (response.status === 401) unauthorizedHandler?.();
      throw new BonfireApiError(response.status, payload.error || 'File upload failed.');
    }
    return { ok: true, file: payload.file };
  },

  createFileFolder(sessionToken: string, name: string) {
    return request<{ ok: boolean; folder: { id: string; name: string } }>(
      '/assistant/files/folders',
      { method: 'POST', body: { name }, sessionToken },
    );
  },

  renameFileFolder(sessionToken: string, id: string, name: string) {
    return request<{ ok: boolean; folder: { id: string; name: string } }>(
      '/assistant/files/folders',
      { method: 'PATCH', body: { id, name }, sessionToken },
    );
  },

  deleteFileFolder(sessionToken: string, id: string) {
    return request<{ ok: boolean }>(
      `/assistant/files/folders?id=${encodeURIComponent(id)}`,
      { method: 'DELETE', sessionToken },
    );
  },

  moveFile(sessionToken: string, fileId: string, folderId = '') {
    return request<{ ok: boolean }>('/assistant/files/move', {
      method: 'POST', body: { fileId, folderId }, sessionToken,
    });
  },

  notifications(sessionToken: string): Promise<{ ok: boolean; notifications: unknown[] }> {
    return request('/assistant/notifications', { sessionToken });
  },

  markNotificationsRead(sessionToken: string, ids: string[]): Promise<{ ok: boolean; marked: number }> {
    return request('/assistant/notifications/read', {
      method: 'POST',
      body: { ids },
      sessionToken,
    });
  },

  clearNotifications(sessionToken: string, ids: string[] = []): Promise<{ ok: boolean }> {
    return request('/assistant/notifications/clear', {
      method: 'POST',
      body: { ids },
      sessionToken,
    });
  },

  brief(sessionToken: string): Promise<{ ok: boolean; brief: unknown }> {
    return request('/assistant/brief', { sessionToken });
  },

  mission(sessionToken: string): Promise<{ ok: boolean; mission: unknown }> {
    return request('/assistant/mission', { sessionToken });
  },

  refreshMission(sessionToken: string): Promise<{ ok: boolean; refreshed: boolean; reason?: string; mission: unknown }> {
    return request('/assistant/mission/refresh', { method: 'POST', sessionToken });
  },

  portfolio(sessionToken: string): Promise<{ ok: boolean; portfolio: unknown }> {
    return request('/assistant/portfolio', { sessionToken });
  },

  scoutThreads(sessionToken: string, includeArchived = false): Promise<ScoutThreadsResponse> {
    const q = includeArchived ? '?archived=true' : '';
    return request<ScoutThreadsResponse>(`/assistant/chat-threads${q}`, { sessionToken });
  },

  scoutThread(sessionToken: string, threadId: string): Promise<ScoutThreadDetailResponse> {
    return request<ScoutThreadDetailResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}`,
      { sessionToken },
    );
  },

  /**
   * Advance this viewer's read marker. Flat route with the id in the body,
   * matching /assistant/threads/follow-up — the server registers plain paths
   * and has no path-parameter router.
   */
  markThreadRead(
    sessionToken: string,
    threadId: string,
    lastReadMessageId: string,
  ): Promise<{ ok: boolean; readAt?: string }> {
    return request('/assistant/threads/read', {
      method: 'POST',
      body: { threadId, lastReadMessageId },
      sessionToken,
    });
  },

  /**
   * Catch-up and the deposit rail in one call — the thread screen needs both
   * on open, and one round trip beats two.
   */
  threadDigest(sessionToken: string, threadId: string): Promise<ThreadDigestResponse> {
    return request<ThreadDigestResponse>(
      `/assistant/threads/digest?threadId=${encodeURIComponent(threadId)}`,
      { sessionToken },
    );
  },

  muteThread(
    sessionToken: string,
    threadId: string,
    muted: boolean,
  ): Promise<{ ok: boolean; muted: boolean }> {
    return request('/assistant/threads/mute', {
      method: 'POST',
      body: { threadId, muted },
      sessionToken,
    });
  },

  registerPushDevice(
    sessionToken: string,
    token: string,
    platform: string,
  ): Promise<{ ok: boolean }> {
    return request('/assistant/push/devices', {
      method: 'POST',
      body: { token, platform },
      sessionToken,
    });
  },

  unregisterPushDevice(sessionToken: string, token: string): Promise<{ ok: boolean }> {
    return request('/assistant/push/devices', {
      method: 'DELETE',
      body: { token },
      sessionToken,
    });
  },

  sendScoutMessage(
    sessionToken: string,
    threadId: string,
    text: string,
    files: ScoutFileAttachment[] = [],
  ): Promise<ScoutThreadDetailResponse> {
    return request<ScoutThreadDetailResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages`,
      { method: 'POST', body: { text, files }, sessionToken },
    );
  },

  updateScoutThread(
    sessionToken: string,
    threadId: string,
    body: { title?: string; archived?: boolean },
  ): Promise<ScoutThreadDetailResponse> {
    return request(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}`,
      { method: 'PATCH', body, sessionToken },
    );
  },

  deleteScoutMessage(sessionToken: string, threadId: string, messageId: string) {
    return request<ScoutThreadDetailResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages/${encodeURIComponent(messageId)}`,
      { method: 'DELETE', sessionToken },
    );
  },

  async uploadScoutAttachment(
    sessionToken: string,
    file: { uri: string; name: string; mime: string },
  ): Promise<ScoutFileAttachment> {
    const localResponse = await fetch(file.uri);
    const body = await localResponse.blob();
    const response = await fetch(buildApiUrl(API_BASE_URL, '/assistant/attachments'), {
      method: 'POST',
      headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken, {
        'Content-Type': file.mime,
      }),
      body,
    });
    const payload = await response.json() as { error?: string; ref?: string; mime?: string; size?: number };
    if (!response.ok || !payload.ref || !payload.mime) {
      if (response.status === 401) unauthorizedHandler?.();
      throw new BonfireApiError(response.status, payload.error || 'Attachment upload failed.');
    }
    return {
      name: file.name,
      kind: file.name.split('.').pop()?.toLowerCase(),
      ref: payload.ref,
      mime: payload.mime,
      size: payload.size,
    };
  },

  /**
   * Uploads a held dictation for transcription against the company vocabulary
   * lane. `durationMs` is the recorder's own measurement — the server clamps it
   * and bills the minute, so it must be the real recording length.
   */
  async transcribeDictation(
    sessionToken: string,
    recording: { uri: string; durationMs: number },
    options: { context?: 'chat' | 'board' | 'search'; threadId?: string } = {},
  ): Promise<{ text: string; durationMs: number; model: string; biased: boolean }> {
    const form = new FormData();
    // React Native's FormData takes the local file URI directly — reading the
    // recording into a JS blob first would double a multi-megabyte recording in
    // memory for no benefit.
    form.append('audio', {
      uri: recording.uri,
      name: 'dictation.m4a',
      type: 'audio/m4a',
    } as unknown as Blob);
    form.append('durationMs', String(Math.round(recording.durationMs)));
    if (options.context) form.append('context', options.context);
    if (options.threadId) form.append('threadId', options.threadId);

    const response = await fetch(buildApiUrl(API_BASE_URL, '/assistant/transcribe'), {
      method: 'POST',
      // Content-Type is deliberately unset: fetch must add the multipart
      // boundary itself, and setting it by hand produces a body the server
      // cannot parse.
      headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken),
      body: form,
    });
    const payload = (await response.json().catch(() => ({}))) as {
      error?: string;
      text?: string;
      durationMs?: number;
      model?: string;
      biased?: boolean;
    };
    if (!response.ok || typeof payload.text !== 'string') {
      if (response.status === 401) unauthorizedHandler?.();
      throw new BonfireApiError(response.status, payload.error || 'Could not transcribe that.');
    }
    return {
      text: payload.text,
      durationMs: payload.durationMs ?? recording.durationMs,
      model: payload.model ?? '',
      biased: Boolean(payload.biased),
    };
  },

  createScoutThread(
    sessionToken: string,
    body: { title?: string; visibility?: string; intake?: string } = {},
  ): Promise<ScoutThreadDetailResponse> {
    return request<ScoutThreadDetailResponse>('/assistant/chat-threads', {
      method: 'POST',
      body,
      sessionToken,
    });
  },

  /** Private Scout query — same path the web OS uses. */
  scoutQuery(
    sessionToken: string,
    query: string,
    history: Array<{ role: string; content: string }> = [],
  ): Promise<{ answer?: string; text?: string; [key: string]: unknown }> {
    return request('/assistant/query', {
      method: 'POST',
      body: { query, mode: 'scout', history },
      sessionToken,
    });
  },

  health(): Promise<{ ok?: boolean; service?: string; version?: string }> {
    return request('/healthz');
  },
};
