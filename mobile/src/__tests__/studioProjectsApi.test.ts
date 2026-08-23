import assert from 'node:assert/strict';
import test from 'node:test';
import { registerTestStubModules } from './support/registerTestStubModules';

test('Studio project reads and rename use the canonical viewer-authorized endpoint', async () => {
  registerTestStubModules('studio-projects-api-stub:', {
    'studio-projects-api-stub:expo-file-system': 'export class File {}',
    'studio-projects-api-stub:../config': "export const API_BASE_URL='https://example.test'; export const NATIVE_CLIENT_HEADER='expo';",
  });
  const originalFetch = globalThis.fetch;
  const requests: Array<{ url: string; init?: RequestInit }> = [];
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    requests.push({ url: String(input), init });
    return new Response(JSON.stringify(
      init?.method === 'PATCH'
        ? { ok: true, project: { id: 'studio-1', title: 'Renamed' } }
        : String(input).includes('?id=')
          ? { ok: true, project: { id: 'studio-1' } }
          : { ok: true, projects: [], hasMore: false },
    ), { status: 200, headers: { 'content-type': 'application/json' } });
  }) as typeof fetch;

  try {
    const { api } = await import('../api/client');
    await api.studioProjects('native-session', {
      kind: 'presentation',
      before: '2026-08-23T12:00:00Z|project / 1',
      limit: 25,
    });
    await api.studioProject('native-session', 'project / 1');
    await api.renameStudioProject('native-session', {
      id: 'project / 1',
      title: 'Renamed',
      expectedRevision: 4,
    });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map(({ url }) => url), [
    'https://example.test/api/studio-projects/v1?kind=presentation&before=2026-08-23T12%3A00%3A00Z%7Cproject%20%2F%201&limit=25',
    'https://example.test/api/studio-projects/v1?id=project%20%2F%201',
    'https://example.test/api/studio-projects/v1',
  ]);
  assert.deepEqual(requests.map(({ init }) => init?.method), ['GET', 'GET', 'PATCH']);
  for (const request of requests) {
    assert.equal((request.init?.headers as Record<string, string>).Authorization, 'Bearer native-session');
  }
  assert.equal(requests[2].init?.body, JSON.stringify({
    id: 'project / 1',
    title: 'Renamed',
    expectedRevision: 4,
  }));
});

test('Studio project detail fails locally when the project identity is empty', async () => {
  registerTestStubModules('studio-project-empty-api-stub:', {
    'studio-project-empty-api-stub:expo-file-system': 'export class File {}',
    'studio-project-empty-api-stub:../config': "export const API_BASE_URL='https://example.test'; export const NATIVE_CLIENT_HEADER='expo';",
  });
  const { api } = await import('../api/client');
  await assert.rejects(api.studioProject('native-session', '   '), /Studio project is invalid/u);
});
