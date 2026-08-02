import assert from 'node:assert/strict';
import test from 'node:test';

import type { ScoutMessage, ScoutReplyState } from '../api/types';
import { scoutReplyLifecyclePresentation } from '../messaging/scoutReplyLifecycle';

function replyMessage(state: ScoutReplyState, retryable = false): ScoutMessage {
  return {
    id: 'reply-1',
    role: 'assistant',
    createdAt: '2026-08-02T00:00:00Z',
    reply: {
      operationId: 'operation-1',
      inReplyTo: 'user-1',
      state,
      attempt: 1,
      retryable,
      errorCode: state === 'failed' ? 'provider_unavailable' : undefined,
    },
  };
}

test('queued and running placeholders stay visible as active Scout work', () => {
  assert.deepEqual(scoutReplyLifecyclePresentation(replyMessage('queued')), {
    state: 'queued',
    label: 'Scout is queued',
    fallbackText: '',
    active: true,
    retryable: false,
  });
  assert.equal(scoutReplyLifecyclePresentation(replyMessage('running'))?.label, 'Scout is responding');
});

test('completed replies have no placeholder chrome', () => {
  assert.equal(scoutReplyLifecyclePresentation(replyMessage('completed')), null);
});

test('failed and canceled placeholders use safe copy without leaking error codes', () => {
  const failed = scoutReplyLifecyclePresentation(replyMessage('failed', true));
  assert.equal(failed?.fallbackText, "Scout couldn't answer yet. Your message is safe.");
  assert.equal(failed?.retryable, true);
  assert.doesNotMatch(JSON.stringify(failed), /provider_unavailable/);

  const canceled = scoutReplyLifecyclePresentation(replyMessage('canceled'));
  assert.equal(canceled?.fallbackText, 'Scout response canceled.');
  assert.equal(canceled?.active, false);
});
