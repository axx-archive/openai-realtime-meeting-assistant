import assert from 'node:assert/strict';
import test from 'node:test';
import { registerTestStubModules } from './support/registerTestStubModules';

test('Files capability reads use the authoritative deck and document endpoints', async () => {
  registerTestStubModules('files-studio-access-stub:', {
    'files-studio-access-stub:expo-file-system': 'export class File {}',
    'files-studio-access-stub:../config': "export const API_BASE_URL='https://example.test'; export const NATIVE_CLIENT_HEADER='expo';",
  });
  const originalFetch = globalThis.fetch;
  const requests: Array<{ url: string; init?: RequestInit }> = [];
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    requests.push({ url: String(input), init });
    const canWrite = String(input).includes('/artifacts/deck?');
    return new Response(JSON.stringify({ ok: true, canWrite }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  }) as typeof fetch;

  let deckAccess: { ok: boolean; canWrite: boolean };
  let documentAccess: { ok: boolean; canWrite: boolean };
  try {
    const { api } = await import('../api/client');
    deckAccess = await api.artifactStudioAccess('native-session', 'deck / owner', 'deck');
    documentAccess = await api.artifactStudioAccess(
      'native-session',
      'document / shared',
      'document',
    );
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(deckAccess, { ok: true, canWrite: true });
  assert.deepEqual(documentAccess, { ok: true, canWrite: false });
  assert.deepEqual(
    requests.map(({ url }) => url),
    [
      'https://example.test/artifacts/deck?id=deck%20%2F%20owner',
      'https://example.test/artifacts/document?id=document%20%2F%20shared',
    ],
  );
  for (const request of requests) {
    assert.equal(request.init?.method, 'GET');
    assert.equal(
      (request.init?.headers as Record<string, string>).Authorization,
      'Bearer native-session',
    );
  }
});
