import assert from 'node:assert/strict';
import test from 'node:test';
import {
  readThreadDetailCache,
  readThreadListCache,
  writeThreadDetailCache,
  writeThreadListCache,
} from '../messaging/threadCache';

test('thread cache serves navigation immediately and clears across accounts', () => {
  const firstScope = 'first@example.com';
  writeThreadListCache(firstScope, [{ id: 'team', title: 'Team' }]);
  writeThreadDetailCache(firstScope, 'team', {
    thread: {
      id: 'team',
      messages: [{ id: 'm1', role: 'user', text: 'Ready', createdAt: '2026-07-30T00:00:00Z' }],
    },
  });

  assert.equal(readThreadListCache(firstScope)?.[0]?.id, 'team');
  assert.equal(readThreadDetailCache(firstScope, 'team')?.thread?.messages?.[0]?.id, 'm1');

  assert.equal(readThreadListCache('second@example.com'), null);
  assert.equal(readThreadDetailCache('second@example.com', 'team'), null);
  assert.equal(readThreadListCache(firstScope), null);
});

test('thread detail cache stays bounded to the eight most recent threads', () => {
  const scope = 'bounded@example.com';
  for (let index = 0; index < 10; index += 1) {
    writeThreadDetailCache(scope, `thread-${index}`, {
      thread: { id: `thread-${index}`, messages: [] },
    });
  }

  assert.equal(readThreadDetailCache(scope, 'thread-0'), null);
  assert.equal(readThreadDetailCache(scope, 'thread-1'), null);
  assert.equal(readThreadDetailCache(scope, 'thread-9')?.thread?.id, 'thread-9');
});
