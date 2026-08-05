import assert from 'node:assert/strict';
import test from 'node:test';

import type { ScoutMessage } from '../api/types';
import { buildThreadReplyTopology } from '../messaging/threadReplyTopology';

function message(id: string, replyTo?: string): ScoutMessage {
  return {
    id,
    role: 'user',
    text: id,
    createdAt: `2026-08-05T12:0${id.length}:00Z`,
    ...(replyTo ? { replyTo: { messageId: replyTo, authorName: 'Root', text: replyTo } } : {}),
  };
}

test('projects replies out of the channel feed and keeps them under the root', () => {
  const root = message('root');
  const reply = message('reply', 'root');
  const next = message('next');
  const topology = buildThreadReplyTopology([root, reply, next]);

  assert.deepEqual(topology.feedMessages.map((entry) => entry.id), ['root', 'next']);
  assert.deepEqual(topology.repliesFor(root).map((entry) => entry.id), ['reply']);
  assert.equal(topology.rootFor(reply)?.id, 'root');
});

test('nested replies resolve to the same root-owned conversation', () => {
  const root = message('root');
  const first = message('first', 'root');
  const nested = message('nested', 'first');
  const topology = buildThreadReplyTopology([root, first, nested]);

  assert.deepEqual(topology.feedMessages.map((entry) => entry.id), ['root']);
  assert.deepEqual(topology.repliesFor(nested).map((entry) => entry.id), ['first', 'nested']);
  assert.equal(topology.rootFor(nested)?.id, 'root');
});

test('keeps an orphaned or cyclic reply visible instead of silently losing it', () => {
  const orphan = message('orphan', 'missing');
  const first = message('first', 'second');
  const second = message('second', 'first');
  const topology = buildThreadReplyTopology([orphan, first, second]);

  assert.deepEqual(topology.feedMessages.map((entry) => entry.id), ['orphan', 'first', 'second']);
});
