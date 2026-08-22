import assert from 'node:assert/strict';
import test from 'node:test';
import { personalRealtimeIslandModel } from '../realtime/personalRealtimeIslandModel';

test('the global island gives each truthful voice state one predictable primary action', () => {
  assert.deepEqual(personalRealtimeIslandModel({
    enabled: true,
    status: 'idle',
    threadId: null,
    canOpenThread: true,
  }), { label: 'Talk to Scout', action: 'start', showClose: false });

  assert.deepEqual(personalRealtimeIslandModel({
    enabled: true,
    status: 'connecting',
    threadId: null,
    canOpenThread: true,
  }), { label: 'Connecting', action: 'wait', showClose: true });

  assert.deepEqual(personalRealtimeIslandModel({
    enabled: true,
    status: 'listening',
    threadId: 'private-thread-73',
    canOpenThread: true,
  }), { label: 'Listening', action: 'open_thread', showClose: true });

  assert.deepEqual(personalRealtimeIslandModel({
    enabled: true,
    status: 'acting',
    threadId: 'private-thread-73',
    canOpenThread: true,
  }), { label: 'Working', action: 'open_thread', showClose: true });

  assert.deepEqual(personalRealtimeIslandModel({
    enabled: true,
    status: 'error',
    threadId: null,
    canOpenThread: true,
  }), { label: 'Try again', action: 'retry', showClose: true });
});

test('an unqualified local build has no actionable idle launcher', () => {
  assert.deepEqual(personalRealtimeIslandModel({
    enabled: false,
    status: 'idle',
    threadId: null,
    canOpenThread: true,
  }), { label: 'Talk to Scout', action: 'wait', showClose: false });
});

test('teardown stays visibly terminal until exact media cleanup completes', () => {
  assert.deepEqual(personalRealtimeIslandModel({
    enabled: false,
    status: 'listening',
    threadId: 'retiring-thread',
    canOpenThread: false,
    tearingDown: true,
  }), { label: 'Stopping', action: 'wait', showClose: true });
});
