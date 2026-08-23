import assert from 'node:assert/strict';
import test from 'node:test';

import type { ScoutMessage } from '../api/types';
import {
  maxWorkActivityDismissals,
  parseWorkActivityDismissalLedger,
  recordWorkActivityDismissal,
  workActivityDismissalStorageKey,
  workActivityIsDismissed,
  workActivitySurfaceIdentity,
} from '../messaging/workActivityDismissal';

function work(overrides: Partial<NonNullable<ScoutMessage['thread']>> = {}): ScoutMessage {
  return {
    id: 'message-1',
    kind: 'thread',
    role: 'scout',
    createdAt: '2026-08-22T12:00:00Z',
    thread: {
      id: 'run-1',
      mode: 'goal',
      processId: 'packaging_studio',
      query: 'Build the deck',
      status: 'running',
      artifactId: 'goal-1',
      currentStage: 'research',
      progressPercent: 18,
      ...overrides,
    },
  };
}

test('dismissal is scoped to viewer and work while progress churn stays quiet', () => {
  const start = workActivitySurfaceIdentity('like-a-farmer', work());
  const later = workActivitySurfaceIdentity('like-a-farmer', work({ currentStage: 'layout', progressPercent: 72 }));
  assert.deepEqual(later, start);
  assert.notEqual(
    workActivitySurfaceIdentity('ball-dogs', work())?.workKey,
    start?.workKey,
  );
  assert.notEqual(
    workActivityDismissalStorageKey('aj@example.com'),
    workActivityDismissalStorageKey('tyler@example.com'),
  );
  assert.doesNotMatch(workActivityDismissalStorageKey('aj@example.com'), /aj|example/u);
});

test('new decisions, terminal states, review outcomes, and exact results surface again', () => {
  const active = workActivitySurfaceIdentity('thread-1', work())!;
  const ledger = recordWorkActivityDismissal(parseWorkActivityDismissalLedger(null), active, 100);
  assert.equal(workActivityIsDismissed(ledger, active), true);

  const decision = workActivitySurfaceIdentity('thread-1', work({
    status: 'needs_input',
    checkpoint: {
      id: 'audience',
      stageId: 'story',
      question: 'Who should lead?',
      options: [{ id: 'buyers', label: 'Buyers', action: 'proceed' }],
    },
  }))!;
  const delivered = workActivitySurfaceIdentity('thread-1', work({
    status: 'complete',
    resultArtifactId: 'deck-v1',
    resultQualityState: 'admitted',
  }))!;
  const revised = workActivitySurfaceIdentity('thread-1', work({
    status: 'complete',
    resultArtifactId: 'deck-v2',
    resultQualityState: 'admitted',
  }))!;
  assert.notEqual(decision.versionKey, active.versionKey);
  assert.notEqual(delivered.versionKey, active.versionKey);
  assert.notEqual(revised.versionKey, delivered.versionKey);
  assert.equal(workActivityIsDismissed(ledger, decision), false);
  assert.equal(workActivityIsDismissed(ledger, delivered), false);
});

test('persisted ledger keeps only the newest version per work item and stays bounded', () => {
  const original = workActivitySurfaceIdentity('thread', work())!;
  const complete = workActivitySurfaceIdentity('thread', work({ status: 'complete' }))!;
  let ledger = recordWorkActivityDismissal(parseWorkActivityDismissalLedger(null), original, 1);
  ledger = recordWorkActivityDismissal(ledger, complete, 2);
  assert.equal(ledger.records.length, 1);
  assert.equal(workActivityIsDismissed(ledger, original), false);
  assert.equal(workActivityIsDismissed(ledger, complete), true);

  for (let index = 0; index < maxWorkActivityDismissals + 7; index += 1) {
    const identity = workActivitySurfaceIdentity(`thread-${index}`, work({ artifactId: `goal-${index}` }))!;
    ledger = recordWorkActivityDismissal(ledger, identity, index + 10);
  }
  assert.equal(ledger.records.length, maxWorkActivityDismissals);
  assert.deepEqual(parseWorkActivityDismissalLedger(JSON.stringify(ledger)), ledger);
  assert.deepEqual(parseWorkActivityDismissalLedger('{bad json'), { version: 1, records: [] });
});

test('generic governed work keeps viewer-local Activity dismissal identity', () => {
  const governed: ScoutMessage = {
    id: 'message-governed',
    kind: 'work_result',
    role: 'scout',
    createdAt: '2026-08-22T12:00:00Z',
    work: {
      id: 'record-1', runId: 'run-1', title: 'Completed work', status: 'complete', workerName: 'Scout',
      currentStage: 'done', summary: 'Ready.', artifactId: 'artifact-1',
      artifactHref: '/api/stride/v1/work/runs/run-1/artifact', evidenceHref: '', providerExecutionFenced: false,
    },
  };
  const identity = workActivitySurfaceIdentity('thread-1', governed);
  assert.ok(identity);
  const ledger = recordWorkActivityDismissal(parseWorkActivityDismissalLedger(null), identity!, 100);
  assert.equal(workActivityIsDismissed(ledger, identity!), true);
});
