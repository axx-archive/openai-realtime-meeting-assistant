import assert from 'node:assert/strict';
import test from 'node:test';
import type { StudioProject } from '../api/types';
import { registerTestStubModules } from './support/registerTestStubModules';

const result = { artifactId: 'result-1', version: 4, digest: 'a'.repeat(64) };
const project: StudioProject = {
  schemaVersion: 1, id: 'root-1', kind: 'document', title: 'Launch decision', revision: 7,
  status: 'ready', progressPercent: 100, phase: 'ready', phases: [], createdAt: '', updatedAt: '',
  rootRunId: 'run-1', rootArtifactId: 'root-1', href: '', canRename: false,
  result: { ...result, type: 'document', title: 'Result', canEdit: false, canContinue: false, canPresent: false, canExport: false },
  feedback: { reviewState: 'unreviewed', history: [], historyTruncated: false, canReview: true, canObserveOutcome: false },
  execution: { status: 'observed', provider: 'OpenAI', requestedModel: 'model-requested', actualModel: 'model-observed', reasoningEffort: 'high', qualification: 'not_evaluated', fallbackUsed: true },
  assurance: { type: 'same_provider_rendered_review', status: 'passed', independent: false },
};

test('native feedback sends the exact result and acceptance identity through PATCH only', async () => {
  registerTestStubModules('work-feedback-api:', {
    'work-feedback-api:expo-file-system': 'export class File {}',
    'work-feedback-api:../config': "export const API_BASE_URL='https://example.test'; export const NATIVE_CLIENT_HEADER='expo';",
  });
  const originalFetch = globalThis.fetch;
  const requests: Array<{ url: string; init?: RequestInit }> = [];
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    requests.push({ url: String(input), init });
    return new Response(JSON.stringify({ ok: true, rerunStarted: false, feedback: project.feedback }), { status: 200, headers: { 'content-type': 'application/json' } });
  }) as typeof fetch;
  try {
    const { api } = await import('../api/client');
    const feedback = { type: 'outcome' as const, verdict: 'helped' as const, note: 'Saved one review cycle', idempotencyKey: 'operation-123', result, acceptedReviewId: 'review-1' };
    await api.studioWorkFeedback('viewer-token', { id: 'root-1', expectedRevision: 7, feedback });
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, 'https://example.test/api/studio-projects/v1');
    assert.equal(requests[0].init?.method, 'PATCH');
    assert.deepEqual(JSON.parse(String(requests[0].init?.body)), { id: 'root-1', expectedRevision: 7, feedback });
  } finally { globalThis.fetch = originalFetch; }
});

test('human review requires a reason for changes and keeps machine evidence distinct', async () => {
  registerTestStubModules('work-evidence-panel:', {
    'work-evidence-panel:react-native': `export const Pressable='Pressable'; export const Text='Text'; export const TextInput='TextInput'; export const View='View'; export const StyleSheet={create:value=>value,hairlineWidth:1};`,
    'work-evidence-panel:../theme/tokens': `const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const space=proxy; export const type=proxy;`,
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = await import('react');
  const { act, create } = await import('react-test-renderer');
  const { WorkEvidencePanel } = await import('../work/WorkEvidencePanel');
  const decisions: unknown[] = [];
  const onFeedback = async (selected: StudioProject, decision: unknown) => { decisions.push({ project: selected.id, decision }); return true; };
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(React.createElement(WorkEvidencePanel, { project, busy: false, onFeedback })); });
  const text = () => JSON.stringify(renderer!.toJSON());
  assert.match(text(), /Rendered review by the same provider/u);
  assert.match(text(), /it is not independent model review/u);
  assert.match(text(), /model-observed/u);
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Request changes' }).props.onPress(); });
  assert.equal(decisions.length, 0);
  assert.match(text(), /Describe what needs to change/u);
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Review note' }).props.onChangeText('Use actual customer evidence.'); });
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Request changes' }).props.onPress(); });
  assert.deepEqual(decisions, [{ project: 'root-1', decision: { type: 'review', verdict: 'revision_requested', note: 'Use actual customer evidence.' } }]);
  const review = { id: 'review-1', rootId: 'root-1', type: 'review' as const, verdict: 'accepted' as const, note: 'Approved scope', result, actorId: 'a', actorName: 'AJ', at: '2026-09-04T12:00:00Z' };
  const accepted = { ...project, feedback: { ...project.feedback!, reviewState: 'accepted' as const, currentReview: review, canObserveOutcome: true, history: [review] } };
  await act(async () => { renderer!.update(React.createElement(WorkEvidencePanel, { project: accepted, busy: false, onFeedback })); });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Review note' }).length, 0);
  assert.match(text(), /Change your review/u);
  assert.match(text(), /Reported outcome/u);
  assert.match(text(), /not independently verified proof/u);
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Reported outcome note' }).props.onChangeText('Launch converted three pilots.'); });
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'It helped' }).props.onPress(); });
  assert.deepEqual(decisions[1], { project: 'root-1', decision: { type: 'outcome', verdict: 'helped', note: 'Launch converted three pilots.' } });
  await act(async () => { renderer!.update(React.createElement(WorkEvidencePanel, { project: { ...accepted, feedback: { ...accepted.feedback, canReview: false, canObserveOutcome: false } }, busy: false, onFeedback })); });
  assert.equal(renderer!.root.findAllByType('Pressable' as any).length, 0);
  const opened: string[] = [];
  await act(async () => { renderer!.update(React.createElement(WorkEvidencePanel, { project: { ...accepted, priorFeedbackEvidence: [{ id: 'source-1', rootId: 'prior-root', result, acceptanceId: 'prior-review', href: '/work' }] }, busy: false, onOpenWork: (id) => opened.push(id) })); });
  assert.match(text(), /included as context/u);
  assert.match(text(), /does not establish that they improved/u);
  await act(async () => { renderer!.root.findByType('Pressable' as any).props.onPress(); });
  assert.deepEqual(opened, ['prior-root']);
  await act(async () => { renderer!.unmount(); });
});

test('selected detail hides prior accounts and selections and clears failed refreshes', async () => {
  const pending: Array<{ session: string; id: string; resolve: (value: unknown) => void; reject: (error: unknown) => void }> = [];
  (globalThis as any).__workDetailFetch = (session: string, id: string) => new Promise((resolve, reject) => pending.push({ session, id, resolve, reject }));
  registerTestStubModules('work-detail-hook:', {
    'work-detail-hook:../api/client': 'export const api={studioProject:(session,id)=>globalThis.__workDetailFetch(session,id)};',
  });
  const React = await import('react');
  const { act, create } = await import('react-test-renderer');
  const { useSelectedWorkDetail } = await import('../work/useSelectedWorkDetail');
  let current: ReturnType<typeof useSelectedWorkDetail>;
  const renders: Array<StudioProject | null> = [];
  function Harness({ session, id, revision }: { session: string; id: string; revision: number }) {
    current = useSelectedWorkDetail(session, id, true, revision);
    renders.push(current.project);
    return null;
  }
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(React.createElement(Harness, { session: 'A', id: 'root-1', revision: 0 })); });
  assert.equal(pending.length, 1);
  await act(async () => { pending[0].resolve({ ok: true, project }); });
  assert.equal(current!.project?.id, 'root-1');
  const start = renders.length;
  await act(async () => { renderer!.update(React.createElement(Harness, { session: 'B', id: 'root-1', revision: 0 })); });
  assert.ok(renders.slice(start).every((value) => value === null));
  await act(async () => { renderer!.update(React.createElement(Harness, { session: 'B', id: 'root-2', revision: 0 })); });
  await act(async () => { pending[1].resolve({ ok: true, project }); });
  assert.equal(current!.project, null);
  await act(async () => { pending[2].resolve({ ok: true, project: { ...project, id: 'root-2' } }); });
  assert.equal((current!.project as StudioProject | null)?.id, 'root-2');
  await act(async () => { renderer!.update(React.createElement(Harness, { session: 'B', id: 'root-2', revision: 1 })); });
  await act(async () => { pending[3].reject(new Error('Access revoked')); });
  assert.equal(current!.project, null);
  assert.match(current!.error, /could not refresh/u);
  await act(async () => { renderer!.unmount(); });
  delete (globalThis as any).__workDetailFetch;
});
