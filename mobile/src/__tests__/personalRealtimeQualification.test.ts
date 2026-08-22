import assert from 'node:assert/strict';
import test from 'node:test';
import {
  NativeClientConfigCache,
  privateRealtimeVoiceIsQualified,
  type NativeClientConfig,
} from '../realtime/personalRealtimeQualification';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

const config = (qualified: unknown): NativeClientConfig => ({
  rtcConfiguration: {},
  privateRealtimeVoiceQualified: qualified as boolean,
});

test('native client config coalesces one session request and reuses its bounded cache', async () => {
  let now = 1_000;
  const pending = deferred<NativeClientConfig>();
  const calls: string[] = [];
  const cache = new NativeClientConfigCache((token) => {
    calls.push(token);
    return pending.promise;
  }, () => now);

  const first = cache.load('session-a');
  const second = cache.load('session-a');
  await Promise.resolve();
  assert.deepEqual(calls, ['session-a']);
  pending.resolve(config(true));
  assert.strictEqual(await first, await second);

  now += 10_000;
  assert.strictEqual(await cache.load('session-a'), await first);
  assert.deepEqual(calls, ['session-a']);
});

test('forced qualification refresh still coalesces concurrent callers', async () => {
  const requests: Array<ReturnType<typeof deferred<NativeClientConfig>>> = [];
  const cache = new NativeClientConfigCache(async () => {
    const request = deferred<NativeClientConfig>();
    requests.push(request);
    return request.promise;
  });

  const first = cache.load('session-a', { force: true });
  const second = cache.load('session-a', { force: true });
  await Promise.resolve();
  assert.equal(requests.length, 1);
  requests[0].resolve(config(true));
  await Promise.all([first, second]);
});

test('an account switch fences a late config response from the replacement cache', async () => {
  const requests = new Map<string, ReturnType<typeof deferred<NativeClientConfig>>>();
  const calls: string[] = [];
  const cache = new NativeClientConfigCache((token) => {
    calls.push(token);
    const request = deferred<NativeClientConfig>();
    requests.set(token, request);
    return request.promise;
  });

  const accountA = cache.load('session-a');
  await Promise.resolve();
  const accountB = cache.load('session-b');
  await Promise.resolve();
  requests.get('session-a')!.resolve(config(true));
  requests.get('session-b')!.resolve(config(false));
  assert.equal(privateRealtimeVoiceIsQualified(await accountA), true);
  assert.equal(privateRealtimeVoiceIsQualified(await accountB), false);
  assert.equal(privateRealtimeVoiceIsQualified(await cache.load('session-b')), false);
  assert.deepEqual(calls, ['session-a', 'session-b']);
});

test('qualification is fail-closed and accepts only the exact server boolean', () => {
  assert.equal(privateRealtimeVoiceIsQualified(config(true)), true);
  for (const value of [false, undefined, null, 1, 'true']) {
    assert.equal(privateRealtimeVoiceIsQualified(config(value)), false);
  }
});
