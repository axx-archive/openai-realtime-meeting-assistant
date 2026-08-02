import test from 'node:test';
import assert from 'node:assert/strict';
import { parsePushTarget, resolveAuthorizedPushTarget } from '../push/deepLink';

test('an APNs payload yields only an untrusted notification receipt', () => {
  const candidate = parsePushTarget({
    notificationId: 'n1',
    threadId: 'attacker-thread',
    messageId: 'attacker-message',
  });
  assert.deepEqual(candidate, { notificationId: 'n1' });
});

test('a receipt resolves from the current account authenticated projection', () => {
  const candidate = parsePushTarget({ notificationId: 'n1' });
  assert.ok(candidate);
  const target = resolveAuthorizedPushTarget(candidate, [{
    id: 'n1',
    threadId: 't1',
    messageId: 'm9',
    threadName: '#team',
  }], 'AJ@Shareability.com');
  assert.deepEqual(target, {
    notificationId: 'n1',
    accountKey: 'aj@shareability.com',
    threadId: 't1',
    messageId: 'm9',
    threadName: '#team',
  });
});

test('a delayed target from account A cannot route under account B', () => {
  const candidate = parsePushTarget({ notificationId: 'only-a' });
  assert.ok(candidate);
  assert.equal(resolveAuthorizedPushTarget(candidate, [{
    id: 'only-b',
    threadId: 'b-thread',
  }], 'b@example.com'), null);
});

test('an authenticated record with no thread fails closed', () => {
  const candidate = parsePushTarget({ notificationId: 'digest' });
  assert.ok(candidate);
  assert.equal(resolveAuthorizedPushTarget(candidate, [{ id: 'digest', kind: 'digest' }], 'a@example.com'), null);
});

test('malformed notification ids are rejected rather than coerced', () => {
  assert.equal(parsePushTarget({ notificationId: 12 }), null);
  assert.equal(parsePushTarget({ notificationId: null }), null);
  assert.equal(parsePushTarget({ notificationId: '' }), null);
  assert.equal(parsePushTarget({ notificationId: '   ' }), null);
  assert.equal(parsePushTarget(null), null);
  assert.equal(parsePushTarget(undefined), null);
});

test('authoritative route fields are validated and normalized', () => {
  const candidate = parsePushTarget({ notificationId: ' n1 ' });
  assert.ok(candidate);
  const target = resolveAuthorizedPushTarget(candidate, [{
    id: 'n1',
    threadId: '  t1  ',
    messageId: 99,
    threadName: ' team ',
  }], ' a@example.com ');
  assert.equal(target?.notificationId, 'n1');
  assert.equal(target?.threadId, 't1');
  assert.equal(target?.messageId, null);
  assert.equal(target?.threadName, 'team');
  assert.equal(target?.accountKey, 'a@example.com');
});
