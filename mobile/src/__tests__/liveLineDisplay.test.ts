import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveLiveLine } from '../canvas/liveLine';
import { liveLineDisplay } from '../canvas/liveLineDisplay';

/**
 * End-to-end through the canvas's text path: real thread data in, the exact
 * strings that reach the screen out.
 *
 * These run resolveLiveLine and liveLineDisplay together on purpose. Testing
 * them separately would prove each half works and still miss the seam, which
 * is where "the canvas renders their words" actually lives.
 */

const base = {
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

// The headline behaviour of the whole wave: a teammate's actual words on the
// home screen.
test("a teammate's message reaches the screen as author and body", () => {
  const display = liveLineDisplay(
    resolveLiveLine({
      ...base,
      tableUnreadCount: 1,
      tableLastMessage: {
        authorName: 'Dana',
        authorEmail: 'dana@x.com',
        text: 'Pushed the pricing memo, needs eyes before 2',
      },
    }),
  );
  assert.equal(display.visible, true);
  assert.equal(display.authorSpan, 'Dana · ');
  assert.equal(display.bodySpan, 'Pushed the pricing memo, needs eyes before 2');
});

// A screen reader must hear one sentence, not a name, a pause, and an
// unattributed message.
test('VoiceOver hears the author and message as one phrase', () => {
  const display = liveLineDisplay(
    resolveLiveLine({
      ...base,
      tableUnreadCount: 1,
      tableLastMessage: { authorName: 'Dana', authorEmail: 'd@x.com', text: 'memo is up' },
    }),
  );
  assert.equal(display.accessibilityLabel, 'Dana: memo is up');
});

// The hint must match where the tap actually goes, or VoiceOver promises one
// destination and the app delivers another.
test('the hint matches the real destination', () => {
  const toThread = liveLineDisplay(
    resolveLiveLine({
      ...base,
      tableUnreadCount: 1,
      tableLastMessage: { authorName: 'Dana', authorEmail: 'd@x.com', text: 'memo is up' },
    }),
  );
  assert.equal(toThread.accessibilityHint, 'Opens the thread.');

  const toList = liveLineDisplay(resolveLiveLine({ ...base, liveRooms: 2 }));
  assert.equal(toList.accessibilityHint, 'Opens threads.');
});

test('nothing live renders nothing at all', () => {
  const display = liveLineDisplay(resolveLiveLine(base));
  assert.equal(display.visible, false);
  assert.equal(display.bodySpan, '');
  // An invisible line must not leave an announceable empty element behind.
  assert.equal(display.accessibilityLabel, '');
});

// Counts have no author, so no separator should be emitted — "· 4 new in #team"
// with a leading middot is the bug this catches.
test('a count renders without an author span or a stray separator', () => {
  const display = liveLineDisplay(
    resolveLiveLine({
      ...base,
      showPreviews: false,
      tableUnreadCount: 4,
      tableLastMessage: { authorName: 'Dana', authorEmail: 'd@x.com', text: 'secret' },
    }),
  );
  assert.equal(display.authorSpan, null);
  assert.equal(display.bodySpan, '4 new in #team');
  assert.ok(!display.bodySpan.includes('·'));
  assert.equal(display.accessibilityLabel, '4 new in #team');
});

// The privacy switch must not leak through the accessibility layer either — a
// preview hidden visually but announced by VoiceOver is still a leak.
test('previews off leaks no message text anywhere in the display', () => {
  const display = liveLineDisplay(
    resolveLiveLine({
      ...base,
      showPreviews: false,
      tableUnreadCount: 2,
      tableLastMessage: { authorName: 'Dana', authorEmail: 'd@x.com', text: 'classified' },
    }),
  );
  assert.ok(!JSON.stringify(display).includes('classified'));
});

test('a mention elsewhere still names its channel on screen', () => {
  const display = liveLineDisplay(
    resolveLiveLine({
      ...base,
      mentions: [
        { threadId: 'pricing', threadName: '#pricing', text: 'need your call', authorName: 'Dana' },
      ],
    }),
  );
  assert.ok(display.bodySpan.includes('#pricing'));
  assert.ok(display.accessibilityLabel.startsWith('Dana:'));
});

// A newline in a message must never reach the canvas — two lines is the hard
// cap and the vertical rhythm is load-bearing.
test('a multi-line message is flattened before it reaches the screen', () => {
  const display = liveLineDisplay(
    resolveLiveLine({
      ...base,
      tableUnreadCount: 1,
      tableLastMessage: {
        authorName: 'Dana',
        authorEmail: 'd@x.com',
        text: 'first line\n\n\nsecond line',
      },
    }),
  );
  assert.ok(!display.bodySpan.includes('\n'));
  assert.equal(display.bodySpan, 'first line second line');
});
