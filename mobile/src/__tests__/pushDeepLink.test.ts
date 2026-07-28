import test from 'node:test';
import assert from 'node:assert/strict';
import { parsePushTarget } from '../push/deepLink';

test('a thread notification resolves to its thread and message', () => {
  const target = parsePushTarget({ threadId: 't1', messageId: 'm9', threadName: '#team' });
  assert.deepEqual(target, { threadId: 't1', messageId: 'm9', threadName: '#team' });
});

test('a thread with no message still resolves — the thread is the target', () => {
  const target = parsePushTarget({ threadId: 't1' });
  assert.deepEqual(target, { threadId: 't1', messageId: null, threadName: null });
});

// A notification is a request to see ONE thing. Falling back to the canvas
// would make the user navigate twice to reach what they were told about.
test('a payload with no thread yields null rather than a canvas fallback', () => {
  assert.equal(parsePushTarget({ kind: 'digest' }), null);
  assert.equal(parsePushTarget(null), null);
  assert.equal(parsePushTarget(undefined), null);
  assert.equal(parsePushTarget('nonsense'), null);
  assert.equal(parsePushTarget(42), null);
});

// This data crosses a process boundary from a push service. A non-string id
// must be rejected, not String()-ed into a route param that fails to resolve.
test('a non-string threadId is rejected rather than coerced', () => {
  assert.equal(parsePushTarget({ threadId: 12 }), null);
  assert.equal(parsePushTarget({ threadId: null }), null);
  assert.equal(parsePushTarget({ threadId: { id: 'x' } }), null);
});

test('a blank or whitespace threadId is not a target', () => {
  assert.equal(parsePushTarget({ threadId: '' }), null);
  assert.equal(parsePushTarget({ threadId: '   ' }), null);
});

test('surrounding whitespace is trimmed off ids', () => {
  const target = parsePushTarget({ threadId: '  t1  ', messageId: ' m9 ' });
  assert.equal(target?.threadId, 't1');
  assert.equal(target?.messageId, 'm9');
});

test('a non-string messageId degrades to null without losing the thread', () => {
  const target = parsePushTarget({ threadId: 't1', messageId: 99 });
  assert.equal(target?.threadId, 't1');
  assert.equal(target?.messageId, null);
});
