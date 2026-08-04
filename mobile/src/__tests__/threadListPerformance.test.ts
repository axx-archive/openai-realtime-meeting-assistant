import assert from 'node:assert/strict';
import test from 'node:test';

import type { ScoutMessage } from '../api/types';
import {
  nextThreadScrollInteraction,
  shouldFollowThreadTail,
  threadRowPresentationEqual,
  threadRowRecycleType,
  threadMessageContentFamily,
  type ThreadListRow,
} from '../messaging/threadListPerformance';

function row(overrides: Partial<ThreadListRow> = {}): ThreadListRow {
  const message: ScoutMessage = {
    id: 'message-1',
    role: 'user',
    text: 'Status update',
    createdAt: '2026-08-02T22:00:00Z',
  };
  return {
    message,
    own: false,
    showAuthor: true,
    showAvatar: true,
    showCatchUp: false,
    ...overrides,
  };
}

test('keeps a historical row stable when only its wrapper object is rebuilt', () => {
  const original = row();
  const rebuilt = { ...original };

  assert.equal(threadRowPresentationEqual(original, rebuilt), true);
});

test('rerenders a row when run-end or marker presentation changes', () => {
  const original = row();

  assert.equal(threadRowPresentationEqual(original, { ...original, showAvatar: false }), false);
  assert.equal(threadRowPresentationEqual(original, { ...original, boundaryLabel: '4 new messages' }), false);
});

test('rerenders a replaced message even when its id is unchanged', () => {
  const original = row();
  const edited = {
    ...original,
    message: { ...original.message, text: 'Edited status update' },
  };

  assert.equal(threadRowPresentationEqual(original, edited), false);
});

test('separates marker, author, and content-shape recycle pools', () => {
  const teammate = row();
  const scout = row({ message: { ...teammate.message, role: 'scout' } });
  const own = row({ own: true });
  const image = row({ message: { ...teammate.message, files: [{ ref: 'asset-1', name: 'photo.png', mime: 'image/png' }] } });
  const link = row({ message: { ...teammate.message, text: 'https://example.com/story' } });
  const work = row({
    message: {
      ...teammate.message,
      kind: 'thread',
      role: 'scout',
      thread: { id: 'work-1', mode: 'research', query: 'Country Golf', status: 'running' },
    },
  });

  assert.equal(threadRowRecycleType(teammate), 'teammate-text');
  assert.equal(threadRowRecycleType(scout), 'scout-rich');
  assert.equal(threadRowRecycleType(own), 'own-text');
  assert.equal(threadRowRecycleType(image), 'teammate-image');
  assert.equal(threadRowRecycleType(link), 'teammate-link');
  assert.equal(threadRowRecycleType(work), 'scout-work-thread');
  assert.equal(threadRowRecycleType({ ...scout, timelineLabel: 'Today' }), 'marker-scout-rich');
});

test('classifies long and rich message families before they enter FlashList', () => {
  assert.equal(threadMessageContentFamily({ ...row().message, text: 'x'.repeat(701) }), 'long');
  assert.equal(threadMessageContentFamily({ ...row().message, role: 'assistant', text: '**Decision:** Ship it.' }), 'rich');
  assert.equal(threadMessageContentFamily({ ...row().message, role: 'assistant', text: 'x'.repeat(701) }), 'rich-long');
});

test('live tail-follow yields immediately to a human scroll interaction', () => {
  assert.equal(shouldFollowThreadTail(true, false), true);
  assert.equal(shouldFollowThreadTail(true, true), false);
  assert.equal(shouldFollowThreadTail(false, false), false);
});

test('scroll interaction stays fenced from drag through momentum settlement', () => {
  let interacting = nextThreadScrollInteraction(false, 'drag-begin');
  assert.equal(interacting, true);

  interacting = nextThreadScrollInteraction(interacting, 'drag-end', 1.2);
  assert.equal(interacting, true, 'drag end must not expose the native pre-momentum gap');
  interacting = nextThreadScrollInteraction(interacting, 'momentum-begin');
  assert.equal(interacting, true);
  interacting = nextThreadScrollInteraction(interacting, 'momentum-end');
  assert.equal(interacting, false);

  interacting = nextThreadScrollInteraction(false, 'drag-begin');
  interacting = nextThreadScrollInteraction(interacting, 'drag-end', 0);
  assert.equal(interacting, false, 'a settled drag can release without waiting for momentum');
  assert.equal(
    nextThreadScrollInteraction(true, 'drag-end'),
    true,
    'missing native velocity stays fenced until the caller grace timer settles it',
  );
});
