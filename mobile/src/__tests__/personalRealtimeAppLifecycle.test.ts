import assert from 'node:assert/strict';
import test from 'node:test';
import {
  PersonalRealtimeTerminalLatch,
  personalRealtimeAppLifecycleAction,
} from '../realtime/personalRealtimeAppLifecycle';

test('transient iOS interruption states retain the exact private voice transport', () => {
  assert.equal(
    personalRealtimeAppLifecycleAction('active', 'inactive', 'listening'),
    'retain',
  );
  assert.equal(
    personalRealtimeAppLifecycleAction('inactive', 'active', 'talking'),
    'retain',
  );
});

test('a real background or unowned app state ends private capture exactly once', () => {
  assert.equal(
    personalRealtimeAppLifecycleAction('inactive', 'background', 'hearing'),
    'stop',
  );
  assert.equal(
    personalRealtimeAppLifecycleAction('active', 'unknown', 'thinking'),
    'stop',
  );
  assert.equal(
    personalRealtimeAppLifecycleAction('background', 'background', 'connecting'),
    'retain',
  );
});

test('settled transports never schedule redundant lifecycle teardown', () => {
  assert.equal(
    personalRealtimeAppLifecycleAction('active', 'background', 'idle'),
    'retain',
  );
  assert.equal(
    personalRealtimeAppLifecycleAction('active', 'background', 'error'),
    'retain',
  );
});

test('unknown then background shares one terminal teardown even after close settles', async () => {
  let closeCalls = 0;
  const latch = new PersonalRealtimeTerminalLatch();
  const first = latch.run(async () => {
    closeCalls += 1;
    await new Promise<void>((finish) => setTimeout(finish, 0));
  });
  const second = latch.run(async () => {
    closeCalls += 1;
  });
  assert.strictEqual(first, second);
  assert.equal(closeCalls, 0, 'the close is admitted once before its microtask starts');
  await Promise.all([first, second]);
  assert.equal(closeCalls, 1);
  await latch.run(async () => { closeCalls += 1; });
  assert.equal(closeCalls, 1, 'the background chain remains latched after teardown settles');
});

test('foreground re-arms a later independent background boundary', async () => {
  const latch = new PersonalRealtimeTerminalLatch();
  let closeCalls = 0;
  await latch.run(async () => { closeCalls += 1; });
  latch.rearm();
  await latch.run(async () => { closeCalls += 1; });
  assert.equal(closeCalls, 2);
});

test('foreground during teardown re-arms only after the first close settles', async () => {
  const latch = new PersonalRealtimeTerminalLatch();
  let finish!: () => void;
  let closeCalls = 0;
  const first = latch.run(async () => {
    closeCalls += 1;
    await new Promise<void>((resolve) => { finish = resolve; });
  });
  await Promise.resolve();
  latch.rearm();
  assert.strictEqual(latch.run(async () => { closeCalls += 1; }), first);
  finish();
  await first;
  await latch.run(async () => { closeCalls += 1; });
  assert.equal(closeCalls, 2);
});
