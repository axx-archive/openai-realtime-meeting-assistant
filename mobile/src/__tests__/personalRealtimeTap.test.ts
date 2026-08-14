import assert from 'node:assert/strict';
import test from 'node:test';
import { runPersonalRealtimeTap } from '../realtime/personalRealtimeTap';

test('one enabled Home waveform tap starts one private Realtime attempt', async () => {
  const calls: string[] = [];
  const outcome = await runPersonalRealtimeTap({
    enabled: true,
    active: false,
    status: 'idle',
    start: async () => { calls.push('start'); },
    stop: async (reason) => { calls.push(`stop:${reason}`); },
  });
  assert.equal(outcome, 'started');
  assert.deepEqual(calls, ['start']);
});

test('an errored transport is closed before the same visible control retries', async () => {
  const calls: string[] = [];
  const outcome = await runPersonalRealtimeTap({
    enabled: true,
    active: false,
    status: 'error',
    start: async () => { calls.push('start'); },
    stop: async (reason) => { calls.push(`stop:${reason}`); },
  });
  assert.equal(outcome, 'started');
  assert.deepEqual(calls, ['stop:cancelled', 'start']);
});

test('an active call stops and a disabled build cannot start', async () => {
  const calls: string[] = [];
  assert.equal(await runPersonalRealtimeTap({
    enabled: true,
    active: true,
    status: 'listening',
    start: async () => { calls.push('start'); },
    stop: async (reason) => { calls.push(`stop:${reason}`); },
  }), 'stopped');
  assert.equal(await runPersonalRealtimeTap({
    enabled: false,
    active: false,
    status: 'idle',
    start: async () => { calls.push('disabled-start'); },
    stop: async () => { calls.push('disabled-stop'); },
  }), 'disabled');
  assert.deepEqual(calls, ['stop:completed']);
});
