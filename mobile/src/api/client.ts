import {
  API_BASE_URL,
  NATIVE_CLIENT_HEADER,
} from '../config';
import type {
  BoardResponse,
  Identity,
  RoomsResponse,
  ScoutThreadDetailResponse,
  ScoutThreadsResponse,
} from './types';

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
};

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const url = `${API_BASE_URL}${path.startsWith('/') ? path : `/${path}`}`;
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'X-Bonfire-Client': NATIVE_CLIENT_HEADER,
  };

  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json';
  }

  if (options.sessionToken) {
    headers.Authorization = `Bearer ${options.sessionToken}`;
    headers['X-Bonfire-Session'] = options.sessionToken;
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
    throw new BonfireApiError(response.status, message);
  }

  return data as T;
}

export const api = {
  login(name: string, password: string): Promise<Identity> {
    return request<Identity>('/auth/login', {
      method: 'POST',
      body: { name: name.trim(), password },
    });
  },

  me(sessionToken: string): Promise<Identity> {
    return request<Identity>('/auth/me', { sessionToken });
  },

  logout(sessionToken: string): Promise<unknown> {
    return request('/auth/logout', { method: 'POST', body: {}, sessionToken });
  },

  rooms(sessionToken: string): Promise<RoomsResponse> {
    return request<RoomsResponse>('/rooms', { sessionToken });
  },

  board(sessionToken: string): Promise<BoardResponse> {
    return request<BoardResponse>('/assistant/board', { sessionToken });
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
