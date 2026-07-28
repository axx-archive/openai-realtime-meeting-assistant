import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveLiveLine, type LiveLineInput } from '../canvas/liveLine';

/**
 * The canvas live line — design §5 of docs/plans/the-table-design.md.
 *
 * The ladder is extracted as a pure function because mobile tests run on plain
 * node:test with no React renderer: a pure function is the only shape of this
 * logic that can be tested at all. It is also the right shape — five priority
 * rungs and a privacy switch is real logic, not rendering.
 */

const base: LiveLineInput = {
  viewerEmail: 'aj@x.com',
  tableThreadId: 'table-1',
  tableName: '#team',
  tableUnreadCount: 0,
  tableLastMessage: null,
  mentions: [],
  liveRooms: 0,
  otherUnreadCount: 0,
  otherUnreadThreads: 0,
  showPreviews: true,
};

test('nothing live renders as absent, not as "Nothing live"', () => {
  const line = resolveLiveLine(base);
  assert.equal(line.kind, 'none');
  assert.equal(line.text, null);
});

test('a mention in the Table outranks rooms and volume', () => {
  const line = resolveLiveLine({
    ...base,
    liveRooms: 3,
    tableUnreadCount: 9,
    mentions: [
      { threadId: 'table-1', threadName: '#team', text: 'can you look?', authorName: 'Dana' },
    ],
  });
  assert.equal(line.kind, 'mention-table');
  assert.equal(line.author, 'Dana');
  assert.equal(line.text, 'can you look?');
  assert.equal(line.mentioned, true);
  assert.equal(line.threadId, 'table-1');
});

test('a mention elsewhere names its channel so you know where to go', () => {
  const line = resolveLiveLine({
    ...base,
    mentions: [
      { threadId: 'pricing', threadName: '#pricing', text: 'need your call', authorName: 'Dana' },
    ],
  });
  assert.equal(line.kind, 'mention-elsewhere');
  assert.equal(line.threadId, 'pricing');
  assert.ok(line.text?.includes('#pricing'), `expected the channel named, got ${line.text}`);
});

// Rung 3 — the single most common state in daily use.
test('unread Table messages render the message itself, not a count', () => {
  const line = resolveLiveLine({
    ...base,
    tableUnreadCount: 3,
    tableLastMessage: {
      authorName: 'Dana',
      authorEmail: 'dana@x.com',
      text: 'Pushed the pricing memo',
    },
  });
  assert.equal(line.kind, 'table');
  assert.equal(line.author, 'Dana');
  assert.equal(line.text, 'Pushed the pricing memo');
  assert.equal(line.threadId, 'table-1');
  assert.equal(line.mentioned, false);
});

// You know what you said. Showing it back reads as a broken feed.
test("the viewer's own last message never renders", () => {
  const line = resolveLiveLine({
    ...base,
    tableUnreadCount: 1,
    tableLastMessage: { authorName: 'AJ', authorEmail: 'AJ@X.com', text: 'shipping it' },
  });
  assert.notEqual(line.kind, 'table');
  assert.equal(line.kind, 'none');
});

// The privacy switch (§5) degrades the line to a count. It must never SILENCE
// it — turning previews off would otherwise hide that anything happened at all.
test('previews off degrades to a count and leaks no message text', () => {
  const line = resolveLiveLine({
    ...base,
    showPreviews: false,
    tableUnreadCount: 4,
    tableLastMessage: { authorName: 'Dana', authorEmail: 'dana@x.com', text: 'secret' },
  });
  assert.equal(line.kind, 'table');
  assert.equal(line.author, null);
  assert.equal(line.text, '4 new in #team');
  assert.ok(!JSON.stringify(line).includes('secret'));
});

test('previews off still hides mention text but keeps the signal', () => {
  const line = resolveLiveLine({
    ...base,
    showPreviews: false,
    mentions: [
      { threadId: 'table-1', threadName: '#team', text: 'classified', authorName: 'Dana' },
    ],
  });
  assert.equal(line.mentioned, true);
  assert.ok(!JSON.stringify(line).includes('classified'));
});

test('rooms outrank ambient unread from other threads', () => {
  const line = resolveLiveLine({
    ...base,
    liveRooms: 2,
    otherUnreadCount: 5,
    otherUnreadThreads: 3,
  });
  assert.equal(line.kind, 'rooms');
  assert.equal(line.text, '2 rooms are live.');
});

test('one live room is singular', () => {
  const line = resolveLiveLine({ ...base, liveRooms: 1 });
  assert.equal(line.text, '1 room is live.');
});

test('ambient unread elsewhere is the last rung before absent', () => {
  const line = resolveLiveLine({ ...base, otherUnreadCount: 5, otherUnreadThreads: 3 });
  assert.equal(line.kind, 'other');
  assert.equal(line.text, '5 unread in 3 threads.');
});

test('one unread in one thread is singular on both counts', () => {
  const line = resolveLiveLine({ ...base, otherUnreadCount: 1, otherUnreadThreads: 1 });
  assert.equal(line.text, '1 unread in 1 thread.');
});

// An unread count with no message body to show must not render a blank line.
test('an unread Table with no known last message falls through', () => {
  const line = resolveLiveLine({ ...base, tableUnreadCount: 2, tableLastMessage: null });
  assert.notEqual(line.text, null);
  assert.equal(line.kind, 'table');
  assert.equal(line.text, '2 new in #team');
});

// Whitespace-only text is the same as no text: it would render an empty line
// under an author name, which reads as a bug.
test('a whitespace-only message does not render as a preview', () => {
  const line = resolveLiveLine({
    ...base,
    tableUnreadCount: 1,
    tableLastMessage: { authorName: 'Dana', authorEmail: 'dana@x.com', text: '   \n ' },
  });
  assert.equal(line.text, '1 new in #team');
  assert.equal(line.author, null);
});

test('message text is collapsed to a single line', () => {
  const line = resolveLiveLine({
    ...base,
    tableUnreadCount: 1,
    tableLastMessage: {
      authorName: 'Dana',
      authorEmail: 'dana@x.com',
      text: 'first line\n\nsecond   line',
    },
  });
  assert.equal(line.text, 'first line second line');
});

// A mention whose threadId matches the Table must take the Table rung, not the
// elsewhere rung — otherwise the line says "in #team" while you are looking at
// a canvas whose chat button goes to exactly that thread.
test('a mention is matched to the Table by thread id, not by name', () => {
  const line = resolveLiveLine({
    ...base,
    tableThreadId: 'table-1',
    mentions: [
      { threadId: 'table-1', threadName: 'renamed', text: 'hi', authorName: 'Dana' },
    ],
  });
  assert.equal(line.kind, 'mention-table');
});
