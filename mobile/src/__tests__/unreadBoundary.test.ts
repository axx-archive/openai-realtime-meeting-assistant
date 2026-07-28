import test from 'node:test';
import assert from 'node:assert/strict';
import { firstUnreadIndex } from '../messaging/unreadBoundary';

const at = (iso: string, email = 'dana@x.com') => ({ id: iso, createdAt: iso, authorEmail: email });

test('the boundary sits at the first message the viewer has not read', () => {
  const messages = [
    at('2026-07-28T10:00:00Z'),
    at('2026-07-28T10:05:00Z'),
    at('2026-07-28T10:06:00Z'),
  ];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:01:00Z', 'aj@x.com'), 1);
});

test('all read yields -1 so no divider renders', () => {
  const messages = [at('2026-07-28T10:00:00Z')];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:01:00Z', 'aj@x.com'), -1);
});

test('no marker means the first message from someone else opens the run', () => {
  const messages = [at('2026-07-28T10:00:00Z')];
  assert.equal(firstUnreadIndex(messages, undefined, 'aj@x.com'), 0);
});

// Sending from another device would otherwise draw a "new messages" line
// directly above your own text.
test("the viewer's own message never starts the unread run", () => {
  const messages = [at('2026-07-28T10:05:00Z', 'aj@x.com'), at('2026-07-28T10:06:00Z')];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:01:00Z', 'aj@x.com'), 1);
});

test('own-message matching is case-insensitive', () => {
  const messages = [at('2026-07-28T10:05:00Z', 'AJ@X.com')];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:01:00Z', 'aj@x.com'), -1);
});

// Mirrors the server's threadUnreadCount: an unplaceable message counts as
// read. The two sides disagreeing would put a divider where the count says
// there is nothing.
test('an unparseable timestamp is treated as read', () => {
  const messages = [at('not-a-time'), at('2026-07-28T10:06:00Z')];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:01:00Z', 'aj@x.com'), 1);
});

test('an empty thread has no boundary', () => {
  assert.equal(firstUnreadIndex([], '2026-07-28T10:00:00Z', 'aj@x.com'), -1);
});

// A message exactly AT the marker was read — the marker means "read through
// this moment", so a strict comparison is required or the last message you
// read reappears as new every time you open the thread.
test('a message exactly at the marker is read, not unread', () => {
  const messages = [at('2026-07-28T10:00:00Z')];
  assert.equal(firstUnreadIndex(messages, '2026-07-28T10:00:00Z', 'aj@x.com'), -1);
});
