import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import type { StudioProject } from '../api/types';
import { homeOperatingBrief, homeOperatingBriefColumns } from '../canvas/homeOperatingBrief';

const project = (id: string, status: StudioProject['status'], overrides: Partial<StudioProject> = {}): StudioProject => ({
  schemaVersion: 1, id, kind: 'document', title: id, revision: 1, status,
  progressPercent: 0, phase: '', phases: [], createdAt: '', updatedAt: '',
  rootRunId: `run-${id}`, rootArtifactId: `artifact-${id}`, href: '', canRename: false,
  ...overrides,
});
const result = { artifactId: 'result', type: 'document', title: 'Result', version: 1, digest: 'a'.repeat(64), canEdit: true, canContinue: false, canPresent: false, canExport: false };

test('Home separates decisions, actual pending review, active work, and ready results', () => {
  const brief = homeOperatingBrief([
    project('input', 'needs_input', { checkpoint: { id: 'c', stageId: 's', question: 'Which region should we launch in?' } }),
    project('attention', 'needs_attention', { attention: { title: 'Source unavailable', body: 'Choose another source.' } }),
    project('review', 'ready', { result: { ...result, canContinue: true, qualityState: 'edited_after_admission' } }),
    project('ready', 'ready', { result }),
    project('accepted', 'ready', { result: { ...result, approvalState: 'approved_exact', qualityState: 'admitted' } }),
    project('queued', 'queued'),
    project('running', 'running', { phase: 'build', phases: [{ id: 'build', label: 'Checking sources', status: 'active' }] }),
    project('stopped', 'stopped'),
  ]);
  assert.deepEqual(brief.judgment.map((item) => item.project.id), ['input', 'attention', 'review']);
  assert.equal(brief.judgment[0].detail, 'Which region should we launch in?');
  assert.equal(brief.judgment[1].detail, 'Choose another source.');
  assert.deepEqual(brief.moving.map((item) => item.detail), ['Waiting to start', 'Checking sources']);
  assert.deepEqual(brief.recent.map((item) => item.project.id), ['ready', 'accepted']);
  assert.ok(brief.recent.every((item) => item.label === 'Ready to open'));
});

test('missing or legacy review metadata never becomes a fabricated human approval request', () => {
  const brief = homeOperatingBrief([
    project('unknown', 'ready', { result: { ...result, canContinue: true } }),
    project('legacy', 'ready', { result: { ...result, canContinue: true, reviewManaged: false, qualityState: 'draft_needs_attention' } }),
    project('no-result', 'ready'),
  ]);
  assert.deepEqual(brief.judgment, []);
  assert.deepEqual(brief.recent.map((item) => item.project.id), ['unknown', 'legacy']);
});

test('operating brief uses available content width and gives large type one column', () => {
  assert.equal(homeOperatingBriefColumns(390, 1), false);
  assert.equal(homeOperatingBriefColumns(744 - 248, 1), false);
  assert.equal(homeOperatingBriefColumns(1024 - 248, 1), false);
  assert.equal(homeOperatingBriefColumns(1120, 1), true);
  assert.equal(homeOperatingBriefColumns(1120, 1.35), false);
  assert.equal(homeOperatingBriefColumns(Number.NaN, 1), false);
});

test('Home Work data stays bound to the requesting session and fails closed on refresh errors', () => {
  const hook = readFileSync(path.resolve(import.meta.dirname, '../canvas/useHomeOperatingBrief.ts'), 'utf8');
  assert.match(hook, /api\.studioProjects\(sessionToken, \{ limit: 100 \}\)/u);
  assert.match(hook, /snapshot\?\.session === sessionToken \? snapshot : null/u);
  assert.match(hook, /currentSession\.current !== sessionToken/u);
  assert.match(hook, /catch \{[\s\S]*setSnapshot\(null\)/u);
  assert.match(hook, /generation\.current \+= 1/u);
  assert.match(hook, /clearInterval\(timer\)/u);
});

test('a rendered Home never leaks old-session work or revives failed/replaced requests', async () => {
  const { registerTestStubModules } = await import('./support/registerTestStubModules');
  const harness = {
    session: 'viewer-a',
    requests: [] as Array<{ session: string; resolve: (value: unknown) => void; reject: (error: Error) => void }>,
  };
  const globalHarness = globalThis as typeof globalThis & { __homeBriefTest?: typeof harness; IS_REACT_ACT_ENVIRONMENT?: boolean };
  globalHarness.__homeBriefTest = harness;
  globalHarness.IS_REACT_ACT_ENVIRONMENT = true;
  registerTestStubModules('home-brief-hook:', {
    'home-brief-hook:@react-navigation/native': `import { useEffect } from 'react'; export const useFocusEffect=callback=>useEffect(callback,[callback]);`,
    'home-brief-hook:../auth/AuthContext': `export const useAuth=()=>({sessionToken:globalThis.__homeBriefTest.session});`,
    'home-brief-hook:../realtime/OfficeEventsContext': `export const useOfficeEvents=()=>({event:null,version:0});`,
    'home-brief-hook:../api/client': `export const api={studioProjects:session=>new Promise((resolve,reject)=>globalThis.__homeBriefTest.requests.push({session,resolve,reject}))};`,
  });
  const React = await import('react');
  const { act, create } = await import('react-test-renderer');
  const { useHomeOperatingBrief } = await import('../canvas/useHomeOperatingBrief');
  let current: ReturnType<typeof useHomeOperatingBrief>;
  const renders: Array<{ session: string; ids: string[] }> = [];
  function Probe() {
    current = useHomeOperatingBrief();
    renders.push({ session: harness.session, ids: current.judgment.map((item) => item.project.id) });
    return null;
  }
  let renderer: import('react-test-renderer').ReactTestRenderer;
  try {
    await act(async () => { renderer = create(React.createElement(Probe)); });
    await act(async () => { harness.requests[0].resolve({ ok: true, projects: [project('private-a', 'needs_input')], hasMore: false }); });
    assert.deepEqual(current!.judgment.map((item) => item.project.id), ['private-a']);
    await act(async () => { void current!.refresh(); });
    harness.session = 'viewer-b';
    await act(async () => { renderer!.update(React.createElement(Probe)); });
    assert.equal(current!.judgment.length, 0);
    await act(async () => { harness.requests[1].resolve({ ok: true, projects: [project('late-a', 'needs_input')], hasMore: false }); });
    assert.equal(current!.judgment.length, 0);
    await act(async () => { harness.requests[2].resolve({ ok: true, projects: [project('private-b', 'needs_input')], hasMore: false }); });
    assert.deepEqual(current!.judgment.map((item) => item.project.id), ['private-b']);
    await act(async () => { void current!.refresh(); });
    await act(async () => { harness.requests[3].reject(new Error('access revoked')); });
    assert.equal(current!.judgment.length, 0);
    assert.equal(current!.ready, false);
    assert.ok(current!.error);
    assert.ok(renders.filter((render) => render.session === 'viewer-b').every((render) => !render.ids.includes('private-a') && !render.ids.includes('late-a')));
  } finally {
    await act(async () => { renderer!.unmount(); });
    delete globalHarness.__homeBriefTest;
  }
});
