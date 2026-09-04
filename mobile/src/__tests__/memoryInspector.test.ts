import assert from 'node:assert/strict';
import test from 'node:test';
import type { MemoryInspectItem } from '../api/types';
import { memoryQuestion, memorySourceTarget } from '../memory/memoryInspectorModel';
import { registerTestStubModules } from './support/registerTestStubModules';

const item: MemoryInspectItem = { id: 'note-1', kind: 'note', title: 'Pilot', summary: 'Two weeks', at: '2026-09-04T12:00:00Z', status: 'active', provenance: [{ type: 'thread', id: 'thread-1' }, { type: 'message', id: 'message-1' }, { type: 'meeting', id: 'meeting-1' }, { type: 'signals', id: '3' }] };
test('memory provenance opens exact original conversation message and meeting, never invented links', () => {
  assert.deepEqual(memorySourceTarget(item, item.provenance[0]), { kind: 'thread', id: 'thread-1', messageId: 'message-1' });
  assert.deepEqual(memorySourceTarget(item, item.provenance[2]), { kind: 'meeting', id: 'meeting-1' });
  assert.equal(memorySourceTarget(item, item.provenance[3]), null);
  assert.equal(memoryQuestion('  ', item), '');
  assert.match(memoryQuestion('Why two weeks?', item), /memory record note-1: Pilot/u);
  assert.match(memoryQuestion('Why two weeks?', item), /thread:thread-1, message:message-1/u);
  assert.match(memoryQuestion('Why two weeks?', item), /say when evidence is missing or conflicting/u);
});

test('memory inspector queries server filters and correction uses existing audited endpoint', async () => {
  registerTestStubModules('memory-api:', {
    'memory-api:expo-file-system': 'export class File {}',
    'memory-api:../config': "export const API_BASE_URL='https://example.test'; export const NATIVE_CLIENT_HEADER='expo';",
  });
  const original = globalThis.fetch;
  const requests: Array<{ url: string; init?: RequestInit }> = [];
  globalThis.fetch = (async (url: unknown, init?: RequestInit) => {
    requests.push({ url: String(url), init });
    return new Response(JSON.stringify({ ok: true, items: [item] }), { status: 200, headers: { 'content-type': 'application/json' } });
  }) as typeof fetch;
  try {
    const { api } = await import('../api/client');
    await api.memoryInspect('session', { subject: 'pilot & adoption', kinds: 'note', person: 'AJ' });
    const url = new URL(requests[0].url);
    assert.equal(url.pathname, '/assistant/memory/inspect');
    assert.equal(url.searchParams.get('subject'), 'pilot & adoption');
    assert.equal(url.searchParams.get('kinds'), 'note');
    await api.correctMemory('session', 'note-1', 'Measure adoption first');
    assert.equal(requests[1].init?.method, 'POST');
    assert.deepEqual(JSON.parse(String(requests[1].init?.body)), { id: 'note-1', action: 'correct', correction: 'Measure adoption first' });
  } finally { globalThis.fetch = original; }
});

test('memory cache clears across account/filter switches and rejects late or failed reads', async () => {
  const pending: Array<{ resolve: (value: unknown) => void; reject: (error: unknown) => void }> = [];
  (globalThis as any).__memoryFetch = () => new Promise((resolve, reject) => pending.push({ resolve, reject }));
  registerTestStubModules('memory-hook:', { 'memory-hook:../api/client': 'export const api={memoryInspect:()=>globalThis.__memoryFetch()};' });
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  const React = await import('react');
  const { act, create } = await import('react-test-renderer');
  const { useMemoryInspector } = await import('../memory/useMemoryInspector');
  let current: ReturnType<typeof useMemoryInspector>;
  const rendered: Array<MemoryInspectItem[]> = [];
  function Harness({ session, subject, revision }: { session: string; subject: string; revision: number }) {
    current = useMemoryInspector(session, true, subject, '', '', revision); rendered.push(current.items); return null;
  }
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(React.createElement(Harness, { session: 'A', subject: '', revision: 0 })); });
  await act(async () => { pending[0].resolve({ ok: true, items: [item] }); });
  assert.equal(current!.items.length, 1);
  const start = rendered.length;
  await act(async () => { renderer!.update(React.createElement(Harness, { session: 'B', subject: '', revision: 0 })); });
  assert.ok(rendered.slice(start).every((items) => items.length === 0));
  await act(async () => { renderer!.update(React.createElement(Harness, { session: 'B', subject: 'pilot', revision: 0 })); });
  await act(async () => { pending[1].resolve({ ok: true, items: [item] }); });
  assert.equal(current!.items.length, 0);
  await act(async () => { pending[2].resolve({ ok: true, items: [item] }); });
  assert.equal(current!.items.length, 1);
  await act(async () => { renderer!.update(React.createElement(Harness, { session: 'B', subject: 'pilot', revision: 1 })); });
  await act(async () => { pending[3].reject(new Error('Revoked')); });
  assert.equal(current!.items.length, 0);
  assert.match(current!.error, /could not refresh/u);
  await act(async () => { renderer!.unmount(); });
  delete (globalThis as any).__memoryFetch;
});
