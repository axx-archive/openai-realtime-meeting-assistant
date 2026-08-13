import { File } from "expo-file-system";

import { API_BASE_URL, NATIVE_CLIENT_HEADER } from "../config";
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
  LinkPreview,
  GiphySearchResult,
  ChatMentionCandidate,
  ThreadDigestResponse,
  StrideRuntimeStatusResponse,
  StrideRosterResponse,
  StrideMarketplaceResponse,
  StrideSeatMutationResponse,
  StrideTeamSeat,
  StrideWorkMutationResponse,
  StrideWorkResponse,
  StrideWorkArtifactResponse,
  StrideMeetingSpecialistStatusResponse,
  StrideMeetingSpecialistInvitation,
  RoomAgentsResponse,
  StridePrivateAgentTemplateInput,
  StrideRelationshipMemoryResponse,
  StridePersonalContextSource,
  StridePersonalContextExport,
  ArtifactDispositionReceipt,
  ArtifactDispositionRef,
  ArtifactDriveSaveCapability,
  ArtifactResponse,
  HomeResponse,
  HomeProjectContextResponse,
  ProjectCorrectionResponse,
} from "./types";
import {
  buildConsentDecision,
  parseConsentDecisionResponse,
  parseConsentStatus,
} from "./consent";
import {
  buildApiUrl,
  buildAuthHeaders,
  buildIdempotencyHeaders,
} from "./requestHelpers";
import {
  fenceUnauthorizedResponse,
  readTextAfterUnauthorizedFence,
} from "./unauthorizedBoundary";
import {
  parseStridePersonalContextExport,
  parseStridePersonalContextSource,
  parseStridePersonalContextSources,
} from "../personalContext/parser";

export { setUnauthorizedHandler } from "./unauthorizedBoundary";

export class BonfireApiError extends Error {
  status: number;
  data: unknown;

  constructor(status: number, message: string, data: unknown = null) {
    super(message);
    this.name = "BonfireApiError";
    this.status = status;
    this.data = data;
  }
}

type RequestOptions = {
  method?: string;
  body?: unknown;
  sessionToken?: string | null;
  signal?: AbortSignal;
  headers?: Record<string, string>;
  suppressUnauthorizedHandler?: boolean;
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
    headers["Content-Type"] = "application/json";
  }

  const response = await fetch(url, {
    method: options.method ?? (options.body !== undefined ? "POST" : "GET"),
    headers,
    body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    signal: options.signal,
  });

  const text = await readTextAfterUnauthorizedFence(
    response,
    options.sessionToken,
    options.suppressUnauthorizedHandler,
  );
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
      (data &&
      typeof data === "object" &&
      "error" in data &&
      typeof (data as { error: unknown }).error === "string"
        ? (data as { error: string }).error
        : null) || `Request failed (${response.status})`;
    throw new BonfireApiError(response.status, message, data);
  }

  return { data: data as T, response };
}

export async function request<T>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  return (await requestWithResponse<T>(path, options)).data;
}

export const api = {
  login(name: string, password: string): Promise<Identity> {
    return request<Identity>("/auth/login", {
      method: "POST",
      body: { name: name.trim(), password },
    });
  },

  requestPasswordReset(email: string): Promise<{ ok?: boolean }> {
    return request("/auth/reset/request", {
      method: "POST",
      body: { email: email.trim() },
    });
  },

  confirmPasswordReset(
    token: string,
    newPassword: string,
  ): Promise<{ ok?: boolean }> {
    return request("/auth/reset/confirm", {
      method: "POST",
      body: { token, newPassword },
    });
  },

  async beginPasskeyLogin(): Promise<{
    publicKey: Record<string, unknown>;
    ceremony: string;
  }> {
    const { data, response } = await requestWithResponse<{
      publicKey: Record<string, unknown>;
    }>("/auth/passkey/login/begin", { method: "POST", body: {} });
    const ceremony = response.headers.get("X-Bonfire-WebAuthn-Ceremony") ?? "";
    if (!ceremony)
      throw new Error("The server did not start a native passkey session.");
    return { publicKey: data.publicKey, ceremony };
  },

  finishPasskeyLogin(ceremony: string, credential: unknown): Promise<Identity> {
    return request<Identity>("/auth/passkey/login/finish", {
      method: "POST",
      body: credential,
      headers: { "X-Bonfire-WebAuthn-Ceremony": ceremony },
    });
  },

  me(sessionToken: string): Promise<Identity> {
    return request<Identity>("/auth/me", { sessionToken });
  },

  logout(
    sessionToken: string,
    deviceToken?: string | null,
    suppressUnauthorizedHandler = false,
  ): Promise<{
    ok: boolean;
    sessionRevoked: boolean;
    deviceBindingRemoved: boolean;
    deviceCleanupPending: boolean;
  }> {
    return request("/auth/logout", {
      method: "POST",
      body: { deviceToken: deviceToken?.trim() || undefined },
      sessionToken,
      suppressUnauthorizedHandler,
    });
  },

  changePassword(
    sessionToken: string,
    currentPassword: string,
    newPassword: string,
  ): Promise<Identity> {
    return request<Identity>("/auth/change-password", {
      method: "POST",
      body: { currentPassword, newPassword },
      sessionToken,
    });
  },

  updateProfile(
    sessionToken: string,
    displayName: string,
    avatarDataURL = "",
  ): Promise<Identity> {
    return request<Identity>("/auth/profile", {
      method: "POST",
      body: { displayName, avatarDataURL },
      sessionToken,
    });
  },

  setTheme(
    sessionToken: string,
    theme: "light" | "dark" | "system",
  ): Promise<Identity> {
    return request<Identity>("/auth/theme", {
      method: "POST",
      body: { theme },
      sessionToken,
    });
  },

  passkeys(
    sessionToken: string,
  ): Promise<{ passkeys: Array<{ id: string; label: string }> }> {
    return request("/auth/passkeys", { sessionToken });
  },

  async beginPasskeyRegistration(
    sessionToken: string,
  ): Promise<{ publicKey: Record<string, unknown>; ceremony: string }> {
    const { data, response } = await requestWithResponse<{
      publicKey: Record<string, unknown>;
    }>("/auth/passkey/register/begin", {
      method: "POST",
      body: {},
      sessionToken,
    });
    const ceremony = response.headers.get("X-Bonfire-WebAuthn-Ceremony") ?? "";
    if (!ceremony)
      throw new Error("The server did not start a native passkey session.");
    return { publicKey: data.publicKey, ceremony };
  },

  finishPasskeyRegistration(
    sessionToken: string,
    ceremony: string,
    credential: unknown,
  ): Promise<{ passkeys: Array<{ id: string; label: string }> }> {
    return request("/auth/passkey/register/finish", {
      method: "POST",
      body: credential,
      sessionToken,
      headers: { "X-Bonfire-WebAuthn-Ceremony": ceremony },
    });
  },

  deletePasskey(sessionToken: string, id: string) {
    return request<{ passkeys: Array<{ id: string; label: string }> }>(
      "/auth/passkey/delete",
      {
        method: "POST",
        body: { id },
        sessionToken,
      },
    );
  },

  rooms(sessionToken: string): Promise<RoomsResponse> {
    return request<RoomsResponse>("/rooms", { sessionToken });
  },

  home(sessionToken: string): Promise<HomeResponse> {
    return request<HomeResponse>("/assistant/home", { sessionToken });
  },

  projectContext(
    sessionToken: string,
    body: {
      text: string;
      destination: { route: 'new-private' } | { route: 'thread'; threadId: string };
      createTitle?: string;
      attachmentHandles?: Array<{ sourceId: string; sourceRevision: string }>;
      replyToMessageId?: string;
    },
  ): Promise<HomeProjectContextResponse> {
    return request<HomeProjectContextResponse>('/assistant/project-context', { method: 'POST', body, sessionToken });
  },

  meetingSpecialists(
    sessionToken: string,
    roomId: string,
  ): Promise<StrideMeetingSpecialistStatusResponse> {
    return request(
      `/api/stride/v1/meeting-specialists?roomId=${encodeURIComponent(roomId)}`,
      { sessionToken },
    );
  },

  roomAgents(
    sessionToken: string,
    roomId: string,
  ): Promise<RoomAgentsResponse> {
    return request(
      `/api/rooms/agents/scout?roomId=${encodeURIComponent(roomId)}`,
      { sessionToken },
    );
  },

  setRoomScout(
    sessionToken: string,
    roomId: string,
    action: "invite" | "dismiss",
  ): Promise<RoomAgentsResponse> {
    return request("/api/rooms/agents/scout", {
      method: "POST",
      body: { roomId, action },
      sessionToken,
    });
  },

  requestMeetingSpecialist(
    sessionToken: string,
    roomId: string,
    agentId: string,
    purpose: string,
    idempotencyKey: string,
  ): Promise<{
    ok: boolean;
    invitation: StrideMeetingSpecialistInvitation;
    providerSessionStarted: false;
  }> {
    return request("/api/stride/v1/meeting-specialists/invitations", {
      method: "POST",
      body: { roomId, agentId, purpose, idempotencyKey },
      sessionToken,
    });
  },

  resolveMeetingSpecialist(
    sessionToken: string,
    roomId: string,
    invitationId: string,
    revision: number,
    decision: "approved" | "declined" | "dismissed",
  ): Promise<{
    ok: boolean;
    invitation: StrideMeetingSpecialistInvitation;
    providerSessionStarted: false;
  }> {
    return request(
      `/api/stride/v1/meeting-specialists/invitations/${encodeURIComponent(invitationId)}`,
      {
        method: "POST",
        body: { roomId, revision, decision },
        sessionToken,
      },
    );
  },

  createRoom(
    sessionToken: string,
    body: { name: string; passcode?: string; guestAccess?: boolean },
  ): Promise<{ ok: boolean; room: import("./types").Room }> {
    return request("/rooms", { method: "POST", body, sessionToken });
  },

  setRoomPasscode(sessionToken: string, roomId: string, passcode: string) {
    return request<{ ok: boolean; passcodeRequired: boolean }>(
      `/rooms/${encodeURIComponent(roomId)}/passcode`,
      { method: "POST", body: { passcode }, sessionToken },
    );
  },

  archiveRoom(sessionToken: string, roomId: string) {
    return request<{ ok: boolean }>(
      `/rooms/${encodeURIComponent(roomId)}/archive`,
      {
        method: "POST",
        body: {},
        sessionToken,
      },
    );
  },

  restoreRoom(sessionToken: string, roomId: string) {
    return request<{ ok: boolean }>(
      `/rooms/${encodeURIComponent(roomId)}/restore`,
      {
        method: "POST",
        body: {},
        sessionToken,
      },
    );
  },

  roomGuestLinks(sessionToken: string, roomId: string) {
    return request<{
      ok: boolean;
      links: Array<{ id: string; label?: string; expiresAt?: string }>;
    }>(`/rooms/${encodeURIComponent(roomId)}/guest-links`, { sessionToken });
  },

  createRoomGuestLink(
    sessionToken: string,
    roomId: string,
    label: string,
    ttlHours = 72,
  ) {
    return request<{
      ok: boolean;
      url: string;
      link: { id: string; label?: string; expiresAt?: string };
    }>(`/rooms/${encodeURIComponent(roomId)}/guest-links`, {
      method: "POST",
      body: { label, ttlHours },
      sessionToken,
    });
  },

  revokeRoomGuestLink(sessionToken: string, roomId: string, id: string) {
    return request<{ ok: boolean }>(
      `/rooms/${encodeURIComponent(roomId)}/guest-links/revoke`,
      { method: "POST", body: { id }, sessionToken },
    );
  },

  participants(
    sessionToken: string,
    roomId: string,
  ): Promise<Record<string, unknown>> {
    return request(`/participants?room=${encodeURIComponent(roomId)}`, {
      sessionToken,
    });
  },

  clientConfig(sessionToken: string): Promise<{
    rtcConfiguration: { iceServers?: Array<Record<string, unknown>> };
    websocketPath?: string;
    supportedLayers?: string[];
  }> {
    return request("/client-config", { sessionToken });
  },

  realtimeOffer(
    sessionToken: string,
    sdp: string,
    voiceSessionId: string,
  ): Promise<{
    ok: boolean;
    sdp: string;
    voiceSessionId: string;
    threadId: string;
    transportRevision: number;
  }> {
    return request("/assistant/realtime-offer", {
      method: "POST",
      body: { sdp, voiceSessionId },
      sessionToken,
    });
  },

  realtimeTool(
    sessionToken: string,
    voiceSessionId: string,
    threadId: string,
    callId: string,
    name: string,
    argumentsValue: Record<string, unknown>,
    signal?: AbortSignal,
  ): Promise<{
    ok?: boolean;
    result?: Record<string, unknown>;
    error?: string;
    actions?: Array<Record<string, unknown>>;
  }> {
    return request("/assistant/realtime-tool", {
      method: "POST",
      body: { voiceSessionId, threadId, callId, name, arguments: argumentsValue },
      sessionToken,
      signal,
    });
  },

  realtimeUsage(
    sessionToken: string,
    payload: { callId: string; model: string; usage: Record<string, unknown> },
  ): Promise<{ ok: boolean }> {
    return request("/assistant/realtime/usage", {
      method: "POST",
      body: payload,
      sessionToken,
    });
  },

  realtimeMilestone(
    sessionToken: string,
    milestoneOrBinding:
      | "peer_connected"
      | "data_channel_open"
      | "remote_track"
      | "first_audio"
      | "response_done"
      | "transport_error"
      | {
        voiceSessionId: string;
        threadId: string;
        transportRevision: number;
        operationId: string;
        milestone:
          | "peer_connected"
          | "data_channel_open"
          | "remote_track"
          | "first_audio"
          | "response_done"
          | "transport_error";
      },
  ): Promise<{ ok: boolean }> {
    return request("/assistant/realtime/milestone", {
      method: "POST",
      body: typeof milestoneOrBinding === "string"
        ? { milestone: milestoneOrBinding }
        : milestoneOrBinding,
      sessionToken,
    });
  },

  async getConsentStatus(sessionToken: string): Promise<ConsentStatus> {
    const payload = await request<unknown>("/api/consent", { sessionToken });
    return parseConsentStatus(payload);
  },

  async setConsentDecision(
    sessionToken: string,
    scope: ConsentScope,
    disposition: ConsentDisposition,
  ): Promise<ConsentDecisionResponse> {
    const payload = await request<unknown>("/api/consent", {
      method: "POST",
      body: buildConsentDecision(scope, disposition),
      sessionToken,
    });
    return parseConsentDecisionResponse(payload);
  },

  board(sessionToken: string): Promise<BoardResponse> {
    return request<BoardResponse>("/assistant/board", { sessionToken });
  },

  artifact(
    sessionToken: string,
    artifactId: string,
  ): Promise<ArtifactResponse> {
    return request(`/artifacts?id=${encodeURIComponent(artifactId)}`, {
      sessionToken,
    });
  },

  artifactDisposition(
    sessionToken: string,
    body: {
      operationId: string;
      action: "open" | "save" | "discard";
      artifact: ArtifactDispositionRef;
      folderId?: string;
      fileName?: string;
      confirmationId?: string;
    },
  ): Promise<{ ok: boolean; receipt: ArtifactDispositionReceipt }> {
    return request("/api/artifact-dispositions/v1", {
      method: "POST",
      body,
      sessionToken,
    });
  },

  artifactDriveSaveCapability(
    sessionToken: string,
  ): Promise<ArtifactDriveSaveCapability> {
    return request("/api/artifact-drive-saves/v1", { sessionToken });
  },

  saveArtifactToDrive(
    sessionToken: string,
    body: {
      operationId: string;
      artifact: ArtifactDispositionRef;
      folderId?: string;
      fileName?: string;
    },
  ): Promise<{ ok: boolean; receipt: ArtifactDispositionReceipt }> {
    return request("/api/artifact-drive-saves/v1", {
      method: "POST",
      body,
      sessionToken,
    });
  },

  createBoardCard(sessionToken: string, card: BoardCardInput) {
    return request<{
      ok: boolean;
      changed: boolean;
      card?: import("./types").BoardCard;
    }>("/assistant/board/cards", { method: "POST", body: card, sessionToken });
  },

  updateBoardCard(sessionToken: string, cardId: string, card: BoardCardInput) {
    return request<{
      ok: boolean;
      changed: boolean;
      card?: import("./types").BoardCard;
    }>(`/assistant/board/cards/${encodeURIComponent(cardId)}`, {
      method: "PUT",
      body: card,
      sessionToken,
    });
  },

  deleteBoardCard(sessionToken: string, cardId: string) {
    return request<{ ok: boolean; changed: boolean }>(
      `/assistant/board/cards/${encodeURIComponent(cardId)}`,
      { method: "DELETE", sessionToken },
    );
  },

  undoDeleteBoardCard(sessionToken: string) {
    return request<{
      ok: boolean;
      changed: boolean;
      card?: import("./types").BoardCard;
    }>("/assistant/board/cards/undo", { method: "POST", sessionToken });
  },

  resolveBoardDraft(
    sessionToken: string,
    cardId: string,
    action: "accept" | "dismiss",
    reason = "",
  ) {
    return request<{
      ok: boolean;
      action: string;
      card?: import("./types").BoardCard;
    }>(`/assistant/board/drafts/${encodeURIComponent(cardId)}/${action}`, {
      method: "POST",
      body: { reason },
      sessionToken,
    });
  },

  memory(sessionToken: string): Promise<{ ok: boolean; memory: unknown }> {
    return request("/assistant/memory", { sessionToken });
  },

  meetings(
    sessionToken: string,
  ): Promise<{ ok: boolean; meetings: unknown[]; serverNow?: string }> {
    return request("/assistant/meetings?limit=60", { sessionToken });
  },

  files(
    sessionToken: string,
  ): Promise<{ ok: boolean; files: unknown[]; folders: unknown[] }> {
    return request("/assistant/files", { sessionToken });
  },

  async uploadFile(
    sessionToken: string,
    file: { uri: string; name: string; mime: string },
  ): Promise<{ ok: boolean; file: Record<string, unknown> }> {
    const form = new FormData();
    form.append("file", {
      uri: file.uri,
      name: file.name,
      type: file.mime,
    } as unknown as Blob);
    const response = await fetch(
      buildApiUrl(API_BASE_URL, "/assistant/files/upload"),
      {
        method: "POST",
        headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken),
        body: form,
      },
    );
    fenceUnauthorizedResponse(response.status, sessionToken);
    const payload = (await response.json()) as {
      ok?: boolean;
      file?: Record<string, unknown>;
      error?: string;
    };
    if (!response.ok || !payload.file) {
      throw new BonfireApiError(
        response.status,
        payload.error || "File upload failed.",
      );
    }
    return { ok: true, file: payload.file };
  },

  createFileFolder(sessionToken: string, name: string) {
    return request<{ ok: boolean; folder: { id: string; name: string } }>(
      "/assistant/files/folders",
      { method: "POST", body: { name }, sessionToken },
    );
  },

  renameFileFolder(sessionToken: string, id: string, name: string) {
    return request<{ ok: boolean; folder: { id: string; name: string } }>(
      "/assistant/files/folders",
      { method: "PATCH", body: { id, name }, sessionToken },
    );
  },

  deleteFileFolder(sessionToken: string, id: string) {
    return request<{ ok: boolean }>(
      `/assistant/files/folders?id=${encodeURIComponent(id)}`,
      { method: "DELETE", sessionToken },
    );
  },

  moveFile(sessionToken: string, fileId: string, folderId = "") {
    return request<{ ok: boolean }>("/assistant/files/move", {
      method: "POST",
      body: { fileId, folderId },
      sessionToken,
    });
  },

  renameFile(sessionToken: string, id: string, name: string) {
    return request<{ ok: boolean; file?: Record<string, unknown> }>(
      "/assistant/files",
      {
        method: "PATCH",
        body: { id, name },
        sessionToken,
      },
    );
  },

  notifications(
    sessionToken: string,
  ): Promise<{ ok: boolean; notifications: unknown[] }> {
    return request("/assistant/notifications", { sessionToken });
  },

  markNotificationsRead(
    sessionToken: string,
    ids: string[],
  ): Promise<{ ok: boolean; marked: number }> {
    return request("/assistant/notifications/read", {
      method: "POST",
      body: { ids },
      sessionToken,
    });
  },

  clearNotifications(
    sessionToken: string,
    ids: string[] = [],
  ): Promise<{ ok: boolean }> {
    return request("/assistant/notifications/clear", {
      method: "POST",
      body: { ids },
      sessionToken,
    });
  },

  brief(sessionToken: string): Promise<{ ok: boolean; brief: unknown }> {
    return request("/assistant/brief", { sessionToken });
  },

  mission(sessionToken: string): Promise<{ ok: boolean; mission: unknown }> {
    return request("/assistant/mission", { sessionToken });
  },

  refreshMission(sessionToken: string): Promise<{
    ok: boolean;
    refreshed: boolean;
    reason?: string;
    mission: unknown;
  }> {
    return request("/assistant/mission/refresh", {
      method: "POST",
      sessionToken,
    });
  },

  portfolio(
    sessionToken: string,
  ): Promise<{ ok: boolean; portfolio: unknown }> {
    return request("/assistant/portfolio", { sessionToken });
  },

  scoutThreads(
    sessionToken: string,
    includeArchived = false,
  ): Promise<ScoutThreadsResponse> {
    const q = includeArchived ? "?archived=true" : "";
    return request<ScoutThreadsResponse>(`/assistant/chat-threads${q}`, {
      sessionToken,
    });
  },

  scoutThreadIndex(
    sessionToken: string,
  ): Promise<ScoutThreadsResponse> {
    return request<ScoutThreadsResponse>('/assistant/chat-threads?view=index', {
      sessionToken,
    });
  },

  scoutThread(
    sessionToken: string,
    threadId: string,
  ): Promise<ScoutThreadDetailResponse> {
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
    return request("/assistant/threads/read", {
      method: "POST",
      body: { threadId, lastReadMessageId },
      sessionToken,
    });
  },

  /**
   * Catch-up and the deposit rail in one call — the thread screen needs both
   * on open, and one round trip beats two.
   */
  threadDigest(
    sessionToken: string,
    threadId: string,
  ): Promise<ThreadDigestResponse> {
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
    return request("/assistant/threads/mute", {
      method: "POST",
      body: { threadId, muted },
      sessionToken,
    });
  },

  setThreadNotificationLevel(
    sessionToken: string,
    threadId: string,
    level: "all" | "mentions" | "none",
  ): Promise<{
    ok: boolean;
    muted: boolean;
    level: "all" | "mentions" | "none";
  }> {
    return request("/assistant/threads/mute", {
      method: "POST",
      body: { threadId, level },
      sessionToken,
    });
  },

  chatParticipants(
    sessionToken: string,
  ): Promise<{ ok: boolean; participants: ChatMentionCandidate[] }> {
    return request("/assistant/chat-participants", { sessionToken });
  },

  registerPushDevice(
    sessionToken: string,
    token: string,
    platform: string,
  ): Promise<{ ok: boolean }> {
    return request("/assistant/push/devices", {
      method: "POST",
      body: { token, platform },
      sessionToken,
    });
  },

  unregisterPushDevice(
    sessionToken: string,
    token: string,
    suppressUnauthorizedHandler = false,
  ): Promise<{ ok: boolean }> {
    return request("/assistant/push/devices", {
      method: "DELETE",
      body: { token },
      sessionToken,
      suppressUnauthorizedHandler,
    });
  },

  sendScoutMessage(
    sessionToken: string,
    threadId: string,
    text: string,
    files: ScoutFileAttachment[] = [],
    replyToMessageId = "",
    operationId = "",
	projectContextToken = "",
  ): Promise<ScoutThreadDetailResponse> {
    return request<ScoutThreadDetailResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages`,
      {
        method: "POST",
        body: { text, files, replyToMessageId, operationId, ...(projectContextToken ? { projectContextToken } : {}) },
        sessionToken,
      },
    );
  },

  projectCorrection(
    sessionToken: string,
    threadId: string,
    messageId: string,
  ): Promise<ProjectCorrectionResponse> {
    return request<ProjectCorrectionResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages/${encodeURIComponent(messageId)}/project`,
      { sessionToken },
    );
  },

  updateProjectCorrection(
    sessionToken: string,
    threadId: string,
    messageId: string,
    body: { operationId: string; correctionToken: string },
  ): Promise<ProjectCorrectionResponse> {
    return request<ProjectCorrectionResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages/${encodeURIComponent(messageId)}/project`,
      { method: "PATCH", body, sessionToken },
    );
  },

  resolveScoutProposal(
    sessionToken: string,
    threadId: string,
    messageId: string,
    action: "accepted" | "dismissed",
    objective = "",
  ): Promise<ScoutThreadDetailResponse> {
    return request<ScoutThreadDetailResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/proposal`,
      { method: "POST", body: { messageId, action, objective }, sessionToken },
    );
  },

  followUpArtifact(
    sessionToken: string,
    artifactId: string,
    text: string,
  ): Promise<{ ok: boolean; artifact?: Record<string, unknown> }> {
    return request("/assistant/threads/follow-up", {
      method: "POST",
      body: { artifactId, text },
      sessionToken,
    });
  },

  regenerateScoutImage(
    sessionToken: string,
    threadId: string,
    messageId: string,
    prompt: string,
  ): Promise<ScoutThreadDetailResponse> {
    return request<ScoutThreadDetailResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages/${encodeURIComponent(messageId)}/regenerate`,
      { method: "POST", body: { prompt }, sessionToken },
    );
  },

  saveArtifactToFiles(
    sessionToken: string,
    artifactId: string,
  ): Promise<{ ok: boolean; file?: Record<string, unknown> }> {
    return request("/assistant/files/save", {
      method: "POST",
      body: { artifactId },
      sessionToken,
    });
  },

  saveChatAttachmentToFiles(
    sessionToken: string,
    sourceFileId: string,
    fileName: string,
    folderId: string,
  ): Promise<{ ok: boolean; file?: Record<string, unknown> }> {
    return request("/assistant/files/save", {
      method: "POST",
      body: { sourceFileId, fileName, folderId },
      sessionToken,
    });
  },

  updateScoutThread(
    sessionToken: string,
    threadId: string,
    body: { title?: string; archived?: boolean },
  ): Promise<ScoutThreadDetailResponse> {
    return request(`/assistant/chat-threads/${encodeURIComponent(threadId)}`, {
      method: "PATCH",
      body,
      sessionToken,
    });
  },

  deleteScoutMessage(
    sessionToken: string,
    threadId: string,
    messageId: string,
  ) {
    return request<ScoutThreadDetailResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages/${encodeURIComponent(messageId)}`,
      { method: "DELETE", sessionToken },
    );
  },

  updateScoutMessage(
    sessionToken: string,
    threadId: string,
    messageId: string,
    text: string,
    files: ScoutFileAttachment[],
  ): Promise<ScoutThreadDetailResponse> {
    return request(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages/${encodeURIComponent(messageId)}`,
      { method: "PATCH", body: { text, files }, sessionToken },
    );
  },

  setScoutMessageReaction(
    sessionToken: string,
    threadId: string,
    messageId: string,
    emoji: string,
    active: boolean,
  ): Promise<ScoutThreadDetailResponse> {
    const base = `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages/${encodeURIComponent(messageId)}/reaction`;
    return request(
      active ? base : `${base}?emoji=${encodeURIComponent(emoji)}`,
      {
        method: active ? "PUT" : "DELETE",
        ...(active ? { body: { emoji } } : {}),
        sessionToken,
      },
    );
  },

  linkPreview(
    sessionToken: string,
    url: string,
  ): Promise<{ ok: boolean; preview: LinkPreview }> {
    return request(`/assistant/link-preview?url=${encodeURIComponent(url)}`, {
      sessionToken,
    });
  },

  searchGiphy(
    sessionToken: string,
    query: string,
    signal?: AbortSignal,
  ): Promise<{ ok: boolean; results: GiphySearchResult[] }> {
    return request(
      `/assistant/giphy/search?q=${encodeURIComponent(query.trim())}&limit=20`,
      { sessionToken, signal },
    );
  },

  async importGiphy(
    sessionToken: string,
    threadId: string,
    gif: Pick<GiphySearchResult, "id" | "title" | "mediaUrl">,
  ): Promise<ScoutFileAttachment> {
    const response = await request<{
      ok?: boolean;
      file?: ScoutFileAttachment;
      attachment?: ScoutFileAttachment;
      ref?: string;
      name?: string;
      mime?: string;
      size?: number;
      kind?: string;
      sourceId?: string;
      sourceRevision?: string;
    }>("/assistant/giphy/import", {
      method: "POST",
      body: { url: gif.mediaUrl, title: gif.title, id: gif.id, threadId },
      sessionToken,
    });
    const attachment = response.file ?? response.attachment ?? response;
    if (!attachment.ref || !attachment.mime) {
      throw new Error("The GIF import completed without a usable attachment.");
    }
    return {
      name: attachment.name || `${gif.title.trim() || "GIPHY"}.gif`,
      kind: attachment.kind || "gif",
      ref: attachment.ref,
      mime: attachment.mime,
      size: attachment.size,
      sourceId: attachment.sourceId,
      sourceRevision: attachment.sourceRevision,
    };
  },

  async uploadScoutAttachment(
    sessionToken: string,
    threadId: string,
    file: { uri: string; name: string; mime: string },
  ): Promise<ScoutFileAttachment> {
    const form = new FormData();
    // Expo SDK 57's WinterCG fetch accepts Blob-compatible FileSystem Files,
    // not React Native's legacy { uri, name, type } FormData shape. File reads
    // the picker/cache URI directly without a second JS-memory copy.
    form.append("file", new File(file.uri));
    const response = await fetch(
      buildApiUrl(
        API_BASE_URL,
        `/assistant/attachments?threadId=${encodeURIComponent(threadId)}`,
      ),
      {
        method: "POST",
        // Deliberately omit Content-Type so fetch adds the multipart boundary.
        headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken),
        body: form,
      },
    );
    fenceUnauthorizedResponse(response.status, sessionToken);
    const rawPayload = await response.text();
    let payload: {
      error?: string;
      ref?: string;
      mime?: string;
      size?: number;
      sourceId?: string;
      sourceRevision?: string;
    } = {};
    try {
      payload = rawPayload ? (JSON.parse(rawPayload) as typeof payload) : {};
    } catch {
      throw new BonfireApiError(
        response.status,
        response.ok
          ? "The attachment service returned an unreadable response."
          : `Attachment upload failed (${response.status}).`,
      );
    }
    if (!response.ok || !payload.ref || !payload.mime) {
      throw new BonfireApiError(
        response.status,
        payload.error || "Attachment upload failed.",
      );
    }
    return {
      name: file.name,
      kind: file.name.split(".").pop()?.toLowerCase(),
      ref: payload.ref,
      mime: payload.mime,
      size: payload.size,
      sourceId: payload.sourceId,
      sourceRevision: payload.sourceRevision,
    };
  },

  async attachDriveFile(
    sessionToken: string,
    threadId: string,
    fileId: string,
  ): Promise<ScoutFileAttachment> {
    const response = await request<{
      ok: boolean;
      attachment?: ScoutFileAttachment;
    }>("/assistant/attachments/from-file", {
      method: "POST",
      body: { threadId, fileId },
      sessionToken,
    });
    const attachment = response.attachment;
    if (
      !attachment?.ref ||
      !attachment.mime ||
      !attachment.sourceId ||
      !attachment.sourceRevision
    ) {
      throw new Error("Drive did not return an exact authorized attachment.");
    }
    return attachment;
  },

  /**
   * Uploads a held dictation for transcription against the company vocabulary
   * lane. `durationMs` is the recorder's own measurement — the server clamps it
   * and bills the minute, so it must be the real recording length.
   */
  async transcribeDictation(
    sessionToken: string,
    recording: { uri: string; durationMs: number },
    options: {
      context?: "scout" | "chat" | "board" | "search";
      threadId?: string;
    } = {},
  ): Promise<{
    text: string;
    durationMs: number;
    model: string;
    biased: boolean;
  }> {
    const form = new FormData();
    // Use the same SDK 57 File-backed multipart path as chat attachments.
    form.append("audio", new File(recording.uri));
    form.append("durationMs", String(Math.round(recording.durationMs)));
    if (options.context) form.append("context", options.context);
    if (options.threadId) form.append("threadId", options.threadId);

    const response = await fetch(
      buildApiUrl(API_BASE_URL, "/assistant/transcribe"),
      {
        method: "POST",
        // Content-Type is deliberately unset: fetch must add the multipart
        // boundary itself, and setting it by hand produces a body the server
        // cannot parse.
        headers: buildAuthHeaders(NATIVE_CLIENT_HEADER, sessionToken),
        body: form,
      },
    );
    fenceUnauthorizedResponse(response.status, sessionToken);
    const payload = (await response.json().catch(() => ({}))) as {
      error?: string;
      text?: string;
      durationMs?: number;
      model?: string;
      biased?: boolean;
    };
    if (!response.ok || typeof payload.text !== "string") {
      throw new BonfireApiError(
        response.status,
        payload.error || "Could not transcribe that.",
      );
    }
    return {
      text: payload.text,
      durationMs: payload.durationMs ?? recording.durationMs,
      model: payload.model ?? "",
      biased: Boolean(payload.biased),
    };
  },

  createScoutThread(
    sessionToken: string,
    body: {
      title?: string;
      visibility?: string;
      intake?: string;
      operationId?: string;
      openingMessage?: { text: string; projectContextToken?: string };
    } = {},
    idempotencyKey = "",
  ): Promise<ScoutThreadDetailResponse> {
    return request<ScoutThreadDetailResponse>("/assistant/chat-threads", {
      method: "POST",
      body,
      sessionToken,
      headers: buildIdempotencyHeaders(idempotencyKey),
    });
  },

  retryScoutReply(
    sessionToken: string,
    threadId: string,
    replyId: string,
  ): Promise<ScoutThreadDetailResponse> {
    return request<ScoutThreadDetailResponse>(
      `/assistant/chat-threads/${encodeURIComponent(threadId)}/messages/${encodeURIComponent(replyId)}/retry`,
      { method: "POST", body: {}, sessionToken },
    );
  },

  /** Private Scout query — same path the web OS uses. */
  scoutQuery(
    sessionToken: string,
    query: string,
    history: Array<{ role: string; content: string }> = [],
  ): Promise<{ answer?: string; text?: string; [key: string]: unknown }> {
    return request("/assistant/query", {
      method: "POST",
      body: { query, mode: "scout", history },
      sessionToken,
    });
  },

  strideStatus(sessionToken: string): Promise<StrideRuntimeStatusResponse> {
    return request("/api/stride/v1/status", { sessionToken });
  },

  strideRelationshipMemory(
    sessionToken: string,
  ): Promise<StrideRelationshipMemoryResponse> {
    return request("/api/stride/v1/coworker/relationships", { sessionToken });
  },

  stridePersonalContextSources(
    sessionToken: string,
  ): Promise<StridePersonalContextSource[]> {
    return request<unknown>("/api/mymind/v1/sources", { sessionToken }).then(
      parseStridePersonalContextSources,
    );
  },

  stridePutPersonalContext(
    sessionToken: string,
    body: {
      idempotencyKey: string;
      sourceId: string;
      kind: "preference" | "reflection";
      body: string;
      expectedRevision: number;
    },
  ): Promise<StridePersonalContextSource> {
    return request<unknown>("/api/mymind/v1/sources", {
      method: "POST",
      body,
      sessionToken,
    }).then(parseStridePersonalContextSource);
  },

  strideCorrectPersonalContext(
    sessionToken: string,
    sourceId: string,
    body: { idempotencyKey: string; body: string; expectedRevision: number },
  ): Promise<StridePersonalContextSource> {
    return request<unknown>(
      `/api/mymind/v1/sources/${encodeURIComponent(sourceId)}/correct`,
      { method: "POST", body, sessionToken },
    ).then(parseStridePersonalContextSource);
  },

  strideForgetPersonalContext(
    sessionToken: string,
    sourceId: string,
    body: { idempotencyKey: string; expectedRevision: number },
  ): Promise<{ forgotten: boolean }> {
    return request(
      `/api/mymind/v1/sources/${encodeURIComponent(sourceId)}/forget`,
      { method: "POST", body, sessionToken },
    );
  },

  strideExportPersonalContext(
    sessionToken: string,
  ): Promise<StridePersonalContextExport> {
    return request<unknown>("/api/mymind/v1/export", { sessionToken }).then(
      parseStridePersonalContextExport,
    );
  },

  strideSetRelationshipConsent(
    sessionToken: string,
    body: {
      action: "enable" | "disable";
      expectedRevision: number;
      allowInferred: boolean;
      allowShared: boolean;
    },
  ): Promise<StrideRelationshipMemoryResponse> {
    return request("/api/stride/v1/coworker/relationships/consent", {
      method: "POST",
      body,
      sessionToken,
    });
  },

  strideRememberRelationship(
    sessionToken: string,
    body: { expectedRevision: number; preferenceType: string; value: string },
  ): Promise<StrideRelationshipMemoryResponse> {
    return request("/api/stride/v1/coworker/relationships/remember", {
      method: "POST",
      body: { action: "remember", scope: "private", ...body },
      sessionToken,
    });
  },

  strideImportRelationships(
    sessionToken: string,
    body: {
      expectedRevision: number;
      entries: Array<{ category: string; date: string; value: string }>;
    },
  ): Promise<StrideRelationshipMemoryResponse> {
    return request("/api/stride/v1/coworker/relationships/import", {
      method: "POST",
      body: { action: "import", ...body },
      sessionToken,
    });
  },

  strideCorrectRelationship(
    sessionToken: string,
    body: { expectedRevision: number; relationshipId: string; value: string },
  ): Promise<StrideRelationshipMemoryResponse> {
    return request("/api/stride/v1/coworker/relationships/correct", {
      method: "POST",
      body: { action: "correct", ...body },
      sessionToken,
    });
  },

  strideForgetRelationship(
    sessionToken: string,
    body: { expectedRevision: number; relationshipId: string },
  ): Promise<StrideRelationshipMemoryResponse> {
    return request("/api/stride/v1/coworker/relationships/forget", {
      method: "POST",
      body: { action: "forget", ...body },
      sessionToken,
    });
  },

  strideRoster(sessionToken: string): Promise<StrideRosterResponse> {
    return request("/api/stride/v1/roster", { sessionToken });
  },

  strideMarketplace(sessionToken: string): Promise<StrideMarketplaceResponse> {
    return request("/api/stride/v1/marketplace", { sessionToken });
  },

  strideWork(sessionToken: string): Promise<StrideWorkResponse> {
    return request("/api/stride/v1/work", { sessionToken });
  },

  strideWorkArtifact(
    sessionToken: string,
    href: string,
  ): Promise<StrideWorkArtifactResponse> {
    const normalized = href.trim();
    if (!/^\/api\/stride\/v1\/work\/runs\/[a-z0-9_-]+\/artifact$/u.test(normalized)) {
      return Promise.reject(new Error("That governed work artifact link is invalid."));
    }
    return request(normalized, { sessionToken });
  },

  strideStartTrial(
    sessionToken: string,
    listingId: string,
  ): Promise<StrideSeatMutationResponse> {
    return request(
      `/api/stride/v1/marketplace/${encodeURIComponent(listingId)}/trial`,
      {
        method: "POST",
        body: {},
        sessionToken,
      },
    );
  },

  strideHire(
    sessionToken: string,
    listingId: string,
    revision: number,
  ): Promise<StrideSeatMutationResponse> {
    return request(
      `/api/stride/v1/marketplace/${encodeURIComponent(listingId)}/hire`,
      {
        method: "POST",
        body: { revision },
        sessionToken,
      },
    );
  },

  strideCreatePrivateAgentTemplate(
    sessionToken: string,
    body: StridePrivateAgentTemplateInput,
  ): Promise<{
    ok: boolean;
    listing: import("./types").StrideMarketplaceListing;
    created: boolean;
    organizationPrivate: true;
    liveAdmissionFenced: true;
    providerCalls: 0;
  }> {
    return request("/api/stride/v1/marketplace/templates", {
      method: "POST",
      body,
      sessionToken,
    });
  },

  strideSeatAction(
    sessionToken: string,
    agentId: string,
    action: "pause" | "offboard",
    revision: number,
  ): Promise<StrideSeatMutationResponse> {
    return request(
      `/api/stride/v1/roster/${encodeURIComponent(agentId)}/${action}`,
      {
        method: "POST",
        body: { revision },
        sessionToken,
      },
    );
  },

  strideAssignAgent(
    sessionToken: string,
    agentId: string,
    body: {
      revision: number;
      projectOrChannel: string;
      role: string;
      responsibility: string;
      destination: string;
    },
  ): Promise<StrideSeatMutationResponse> {
    return request(
      `/api/stride/v1/roster/${encodeURIComponent(agentId)}/assign`,
      {
        method: "POST",
        body,
        sessionToken,
      },
    );
  },

  strideProposeAgentUpdate(
    sessionToken: string,
    agentId: string,
    body: {
      revision: number;
      summary: string;
      candidate: StrideTeamSeat["config"];
    },
  ): Promise<StrideSeatMutationResponse> {
    return request(
      `/api/stride/v1/roster/${encodeURIComponent(agentId)}/updates`,
      {
        method: "POST",
        body,
        sessionToken,
      },
    );
  },

  strideResolveAgentUpdate(
    sessionToken: string,
    agentId: string,
    updateId: string,
    action: "approve" | "rollback",
    revision: number,
  ): Promise<StrideSeatMutationResponse> {
    return request(
      `/api/stride/v1/roster/${encodeURIComponent(agentId)}/updates/${encodeURIComponent(updateId)}/${action}`,
      {
        method: "POST",
        body: { revision },
        sessionToken,
      },
    );
  },

  strideRecordAgentLearning(
    sessionToken: string,
    agentId: string,
    body: { revision: number; subject: string; scope: string; summary: string },
  ): Promise<StrideSeatMutationResponse> {
    return request(
      `/api/stride/v1/roster/${encodeURIComponent(agentId)}/learning`,
      {
        method: "POST",
        body,
        sessionToken,
      },
    );
  },

  strideResolveAgentLearning(
    sessionToken: string,
    agentId: string,
    learningId: string,
    action: "approve" | "correct" | "forget",
    body: { revision: number; summary: string },
  ): Promise<StrideSeatMutationResponse> {
    return request(
      `/api/stride/v1/roster/${encodeURIComponent(agentId)}/learning/${encodeURIComponent(learningId)}/${action}`,
      {
        method: "POST",
        body,
        sessionToken,
      },
    );
  },

  strideExportAgent(
    sessionToken: string,
    agentId: string,
  ): Promise<{
    ok: boolean;
    export: Record<string, unknown>;
    providerRuntimeExported: false;
  }> {
    return request(
      `/api/stride/v1/roster/${encodeURIComponent(agentId)}/export`,
      { sessionToken },
    );
  },

  strideWorkDestination(
    sessionToken: string,
    suggestionId: string,
    body: {
      revision: number;
      mode: "existing" | "new";
      threadId?: string;
      title?: string;
    },
  ): Promise<StrideWorkMutationResponse> {
    return request(
      `/api/stride/v1/work/suggestions/${encodeURIComponent(suggestionId)}/destination`,
      {
        method: "POST",
        body,
        sessionToken,
      },
    );
  },

  strideWorkDecision(
    sessionToken: string,
    suggestionId: string,
    action: "approve" | "dismiss",
    body: { revision: number; reason?: string },
  ): Promise<StrideWorkMutationResponse> {
    return request(
      `/api/stride/v1/work/suggestions/${encodeURIComponent(suggestionId)}/${action}`,
      {
        method: "POST",
        body,
        sessionToken,
      },
    );
  },

  health(): Promise<{ ok?: boolean; service?: string; version?: string }> {
    return request("/healthz");
  },
};
