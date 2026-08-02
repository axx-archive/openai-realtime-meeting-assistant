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
