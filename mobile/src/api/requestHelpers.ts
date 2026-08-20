export function buildApiUrl(base: string, path: string): string {
  const root = base.replace(/\/$/, '');
  return `${root}${path.startsWith('/') ? path : `/${path}`}`;
}

export function buildAuthHeaders(
  nativeClient: string,
  sessionToken?: string | null,
  extra: Record<string, string> = {},
): Record<string, string> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'X-Bonfire-Client': nativeClient,
    ...extra,
  };
  if (sessionToken) headers.Authorization = `Bearer ${sessionToken}`;
  return headers;
}

export function buildIdempotencyHeaders(idempotencyKey: string): Record<string, string> {
  const key = idempotencyKey.trim();
  return key ? { 'Idempotency-Key': key } : {};
}

export const AUTH_REQUEST_TIMEOUT_MS = 15_000;

export type ApiTransportErrorCode = 'network' | 'timeout' | 'unexpected_response';

export class ApiTransportError extends Error {
  readonly code: ApiTransportErrorCode;

  constructor(code: ApiTransportErrorCode) {
    super(code === 'timeout'
      ? 'Stride couldn\'t reach the office in time. Check your connection and try again.'
      : code === 'unexpected_response'
        ? 'Stride received an unexpected response. Check your connection and try again.'
        : 'Stride couldn\'t reach the office. Check your connection and try again.');
    this.name = 'ApiTransportError';
    this.code = code;
  }
}

export function apiTransportError(didTimeout: boolean): ApiTransportError {
  return new ApiTransportError(didTimeout ? 'timeout' : 'network');
}

export function parseApiResponseText(text: string): unknown {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    throw new ApiTransportError('unexpected_response');
  }
}

export function apiErrorMessage(status: number, data: unknown): string {
  const candidate = data
    && typeof data === 'object'
    && 'error' in data
    && typeof (data as { error: unknown }).error === 'string'
      ? (data as { error: string }).error.trim()
      : '';
  if (
    candidate
    && candidate.length <= 300
    && !/<\/?(?:html|head|body|script|style)\b|<!doctype/i.test(candidate)
  ) {
    return candidate;
  }
  return `Stride couldn't complete that request (${status}). Try again.`;
}

export type RequestDeadline = {
  signal: AbortSignal;
  didTimeout: () => boolean;
  dispose: () => void;
};

export function createRequestDeadline(
  parentSignal: AbortSignal | undefined,
  timeoutMs: number,
): RequestDeadline {
  const controller = new AbortController();
  let timedOut = false;

  const abortFromParent = () => controller.abort();
  if (parentSignal?.aborted) {
    abortFromParent();
  } else {
    parentSignal?.addEventListener('abort', abortFromParent, { once: true });
  }

  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);

  return {
    signal: controller.signal,
    didTimeout: () => timedOut,
    dispose: () => {
      clearTimeout(timer);
      parentSignal?.removeEventListener('abort', abortFromParent);
    },
  };
}
