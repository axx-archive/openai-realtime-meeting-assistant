import assert from 'node:assert/strict';
import test from 'node:test';

import {
  homeScoutOpeningAttempt,
  homeScoutOpeningBody,
  submitHomeScoutOpening,
} from '../canvas/homeScoutOpening';

test('an unchanged failed draft reuses its exact operation key', () => {
  let sequence = 0;
  const createKey = () => `key-${++sequence}`;
  const first = homeScoutOpeningAttempt(null, '  Map the launch risks  ', createKey);
  assert.deepEqual(first, { text: 'Map the launch risks', idempotencyKey: 'key-1' });
  assert.equal(homeScoutOpeningAttempt(first, 'Map the launch risks', createKey), first);
  assert.deepEqual(homeScoutOpeningAttempt(first, 'Map different risks', createKey), {
    text: 'Map different risks',
    idempotencyKey: 'key-2',
  });
});

test('the private-only opening mode atomically carries only the first message', () => {
  assert.deepEqual(homeScoutOpeningBody({ text: 'Hello Scout', idempotencyKey: 'key-1' }), {
    openingMessage: { text: 'Hello Scout' },
  });
});

test('create preserves persistent voice and acceptance returns a route with no message target', async () => {
  const events: string[] = [];
  const attempt = { text: 'Open a new thread', idempotencyKey: 'home-scout-key' };
  const result = await submitHomeScoutOpening(attempt, {
    createThread: async (body, key) => {
      events.push('create-thread');
      assert.deepEqual(body, {
        openingMessage: { text: attempt.text },
      });
      assert.equal(key, attempt.idempotencyKey);
      return {
        ok: true,
        thread: {
          id: 'thread-1',
          title: 'Server title',
          visibility: 'private',
          ownerEmail: 'owner@example.com',
          createdBy: 'Owner',
          createdAt: '2026-08-02T00:00:00Z',
          updatedAt: '2026-08-02T00:00:00Z',
          messageCount: 2,
          preview: '',
        },
      };
    },
  });
  assert.deepEqual(events, ['create-thread']);
  assert.equal(result.accepted, true);
  if (!result.accepted) return;
  assert.deepEqual(result.thread, { threadId: 'thread-1', title: 'Server title' });
  assert.deepEqual(Object.keys(result.thread).sort(), ['threadId', 'title']);
});

test('a failed create returns the same draft and key for retry', async () => {
  const attempt = { text: 'Please keep this', idempotencyKey: 'same-key' };
  const failure = new Error('offline');
  const result = await submitHomeScoutOpening(attempt, {
    createThread: async () => { throw failure; },
  });
  assert.deepEqual(result, { accepted: false, attempt, error: failure });
});
