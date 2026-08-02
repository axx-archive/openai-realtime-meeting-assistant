import assert from 'node:assert/strict';
import test from 'node:test';
import { canDeleteDictation, canSendDictation } from '../voice/dictationLifecycle';

test('only a held or retryable clip can be sent', () => {
  assert.equal(canSendDictation('held', true), true);
  assert.equal(canSendDictation('error', true), true);
  assert.equal(canSendDictation('listening', true), false);
  assert.equal(canSendDictation('transcribing', true), false);
});

test('delete can invalidate a held, pending, or failed clip', () => {
  for (const state of ['held', 'transcribing', 'error'] as const) {
    assert.equal(canDeleteDictation(state, true), true);
  }
  assert.equal(canDeleteDictation('held', false), false);
  assert.equal(canDeleteDictation('listening', true), false);
});
