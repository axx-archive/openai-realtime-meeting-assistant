import assert from 'node:assert/strict';
import test from 'node:test';

import {
  applyChatThreadEvent,
  chatMessageEventRegressesProjectAdmission,
  chatThreadEventJournalCovers,
  isMessageRunEnd,
  reconcileChatThreadSnapshot,
  resolveChatThreadSnapshot,
  typingIndicatorLabel,
} from '../messaging/chatRealtime';

const messages = [
  { id: 'one', role: 'user', authorEmail: 'erick@example.com', text: 'one', createdAt: '2026-01-01T00:00:00Z' },
  { id: 'two', role: 'user', authorEmail: 'erick@example.com', text: 'two', createdAt: '2026-01-01T00:00:01Z' },
  { id: 'three', role: 'user', authorEmail: 'aj@example.com', text: 'three', createdAt: '2026-01-01T00:00:02Z' },
];

test('matching chat events append, replace, and delete immediately by message id', () => {
  const appended = applyChatThreadEvent(messages, 'thread-1', {
    id: 'thread-1',
    message: { id: 'four', role: 'user', text: 'four', createdAt: '2026-01-01T00:00:03Z' },
  });
  assert.deepEqual(appended.map((message) => message.id), ['one', 'two', 'three', 'four']);

  const replaced = applyChatThreadEvent(appended, 'thread-1', {
    id: 'thread-1',
    message: { id: 'two', role: 'user', text: 'edited', createdAt: '2026-01-01T00:00:01Z' },
  });
  assert.equal(replaced[1]?.text, 'edited');

  const deleted = applyChatThreadEvent(replaced, 'thread-1', { id: 'thread-1', deletedMessageId: 'one' });
  assert.deepEqual(deleted.map((message) => message.id), ['two', 'three', 'four']);
});

test('a late pre-confirmation socket frame cannot regress the Project Send response', () => {
  const confirmed = {
    id: 'project-user', role: 'user', createdAt: '2026-01-01T00:00:00Z',
    project: { status: 'confirmed' as const, projectId: 'project-one', projectRevision: 1, title: 'Launch Plan', basis: 'selected' },
  };
  const pending = {
    ...confirmed,
    project: { status: 'pending' as const, title: 'Launch Plan', basis: 'selected' },
  };
  assert.equal(chatMessageEventRegressesProjectAdmission(confirmed, pending), true);
  assert.equal(applyChatThreadEvent([confirmed], 'thread-1', { id: 'thread-1', message: pending })[0], confirmed);

  const queued = { id: 'project-reply', role: 'scout', createdAt: '2026-01-01T00:00:01Z', reply: { operationId: 'operation-one', inReplyTo: 'project-user', state: 'queued' as const, attempt: 0 } };
  const projectPending = { ...queued, reply: { ...queued.reply, state: 'project_pending' as const } };
  assert.equal(applyChatThreadEvent([queued], 'thread-1', { id: 'thread-1', message: projectPending })[0], queued);
});

test('events for another thread preserve the same message array', () => {
  assert.equal(applyChatThreadEvent(messages, 'thread-1', {
    id: 'thread-2',
    message: { id: 'foreign', role: 'user', createdAt: '' },
  }), messages);
});

test('fallback reconciliation preserves identity unless the transcript changed', () => {
  const current = [{ ...messages[0] }];
  const identical = [{ ...current[0] }];
  assert.equal(reconcileChatThreadSnapshot(current, identical), current);
  const changed = [{ ...current[0], text: 'edited' }];
  assert.deepEqual(reconcileChatThreadSnapshot(current, changed), changed);
  assert.notEqual(reconcileChatThreadSnapshot(current, changed), current);
});

test('a socket message that lands during initial load survives the stale load response', () => {
  const socketEvent = {
    id: 'thread-1',
    message: messages[1],
  };
  const afterSocket = applyChatThreadEvent([], 'thread-1', socketEvent);
  const decision = resolveChatThreadSnapshot(
    afterSocket,
    [{ ...messages[0] }],
    'thread-1',
    0,
    1,
    [{ generation: 1, payload: socketEvent }],
  );
  assert.equal(decision.accepted, true);
  assert.equal(decision.replayed, true);
  assert.deepEqual(decision.messages.map((message) => message.id), ['one', 'two']);
});

test('a socket edit that lands during a mutation survives the stale mutation response', () => {
  const beforeMutation = [{ ...messages[0] }];
  const socketEvent = {
    id: 'thread-1',
    message: { ...messages[0], text: 'newer socket edit' },
  };
  const afterSocket = applyChatThreadEvent(beforeMutation, 'thread-1', socketEvent);
  const decision = resolveChatThreadSnapshot(
    afterSocket,
    beforeMutation,
    'thread-1',
    4,
    5,
    [{ generation: 5, payload: socketEvent }],
  );
  assert.equal(decision.accepted, true);
  assert.equal(decision.messages[0]?.text, 'newer socket edit');
});

test('a socket delete is replayed over a stale mutation snapshot', () => {
  const socketEvent = { id: 'thread-1', deletedMessageId: 'two' };
  const current = applyChatThreadEvent(messages, 'thread-1', socketEvent);
  const decision = resolveChatThreadSnapshot(
    current,
    messages,
    'thread-1',
    8,
    9,
    [{ generation: 9, payload: socketEvent }],
  );
  assert.equal(decision.accepted, true);
  assert.deepEqual(decision.messages.map((message) => message.id), ['one', 'three']);
});

test('an incomplete bounded event journal fails safe without overwriting the live transcript', () => {
  const current = [{ ...messages[2] }];
  const decision = resolveChatThreadSnapshot(current, messages, 'thread-1', 10, 12, [
    { generation: 12, payload: { id: 'thread-1', message: messages[2] } },
  ]);
  assert.equal(decision.accepted, false);
  assert.equal(decision.messages, current);
  assert.equal(chatThreadEventJournalCovers(10, 12, [
    { generation: 12, payload: { id: 'thread-1' } },
  ]), false);
});

test('avatars appear only at the end of a consecutive author run', () => {
  assert.equal(isMessageRunEnd(messages, 0), false);
  assert.equal(isMessageRunEnd(messages, 1), true);
  assert.equal(isMessageRunEnd(messages, 2), true);
});

test('typing labels remain concise for one or many people', () => {
  assert.equal(typingIndicatorLabel(['Erick']), 'Erick is typing');
  assert.equal(typingIndicatorLabel(['Erick', 'Tim']), 'Erick and Tim are typing');
  assert.equal(typingIndicatorLabel(['Erick', 'Tim', 'Joel']), 'Erick and 2 others are typing');
});
