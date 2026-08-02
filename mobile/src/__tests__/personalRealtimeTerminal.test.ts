import assert from 'node:assert/strict';
import test from 'node:test';
import {
  closePersonalRealtimeStartup,
  closePersonalRealtimeTransportResources,
  drainPersonalRealtimeStartup,
  personalRealtimeCleanupScope,
  releasePersonalRealtimeTerminalFocus,
} from '../realtime/personalRealtimeTerminal';
import { NativeMediaOperationTimeoutError } from '../realtime/nativeRoomTerminal';
import { AudioFocusCoordinator } from '../voice/AudioFocusCoordinator';

function transportHarness() {
  const events: string[] = [];
  const dataChannel = {
    onopen: () => {},
    onmessage: () => {},
    onerror: () => {},
    onclose: () => {},
    close: () => { events.push('channel.close'); },
  };
  const peer = {
    ontrack: () => {},
    onconnectionstatechange: () => {},
    onicegatheringstatechange: () => {},
    close: () => { events.push('peer.close'); },
  };
  const stream = {
    getTracks: () => [
      { stop: () => { events.push('track.1.stop'); } },
      { stop: () => { events.push('track.2.stop'); } },
    ],
  };
  const close = () => closePersonalRealtimeTransportResources({
    dataChannel,
    peer,
    stream,
    deactivateMediaSession: () => { events.push('media.deactivate'); },
  });
  return { close, dataChannel, events, peer };
}

test('late personal cleanup cannot claim replacement transport refs', () => {
  assert.equal(personalRealtimeCleanupScope(10, 10), 'owned');
  assert.equal(personalRealtimeCleanupScope(10, null), 'detached');
  assert.equal(personalRealtimeCleanupScope(10, 11), 'replacement');
  assert.equal(personalRealtimeCleanupScope(null, null), 'owned');
});

test('terminal Realtime cleanup closes channel, peer, tracks, and native media session', async () => {
  const harness = transportHarness();
  await harness.close();

  assert.equal(harness.dataChannel.onopen, null);
  assert.equal(harness.dataChannel.onmessage, null);
  assert.equal(harness.dataChannel.onerror, null);
  assert.equal(harness.dataChannel.onclose, null);
  assert.equal(harness.peer.ontrack, null);
  assert.equal(harness.peer.onconnectionstatechange, null);
  assert.equal(harness.peer.onicegatheringstatechange, null);
  assert.deepEqual(harness.events, [
    'channel.close',
    'peer.close',
    'track.1.stop',
    'track.2.stop',
    'media.deactivate',
  ]);
});

test('terminal Realtime failure releases the exact lease and its force-close performs cleanup once', async () => {
  const harness = transportHarness();
  const leaseReasons: string[] = [];
  const lease = {
    release: async (reason = 'completed') => {
      leaseReasons.push(reason);
      await harness.close();
      return true;
    },
  };

  await releasePersonalRealtimeTerminalFocus(lease, harness.close);
  assert.deepEqual(leaseReasons, ['error']);
  assert.equal(harness.events.filter((event) => event === 'peer.close').length, 1);
  assert.equal(harness.events.filter((event) => event.endsWith('.stop')).length, 2);
  assert.equal(harness.events.filter((event) => event === 'media.deactivate').length, 1);
});

test('a stale terminal lease falls back to direct transport cleanup', async () => {
  const harness = transportHarness();
  let releases = 0;
  await releasePersonalRealtimeTerminalFocus({
    release: async () => { releases += 1; return false; },
  }, harness.close);

  assert.equal(releases, 1);
  assert.deepEqual(harness.events, [
    'channel.close',
    'peer.close',
    'track.1.stop',
    'track.2.stop',
    'media.deactivate',
  ]);
});

test('a bounded lease timeout leaves its exact late cleanup in charge', async () => {
  let fallbackCleanups = 0;
  await assert.rejects(
    releasePersonalRealtimeTerminalFocus({
      release: async () => {
        throw new NativeMediaOperationTimeoutError('Personal Realtime teardown', 10);
      },
    }, async () => { fallbackCleanups += 1; }),
    NativeMediaOperationTimeoutError,
  );
  assert.equal(fallbackCleanups, 0);
});

test('startup failure publication waits for deferred exact transport and media cleanup', async () => {
  const events: string[] = [];
  let finishCleanup!: () => void;
  let cleanupEntered!: () => void;
  const entered = new Promise<void>((resolve) => { cleanupEntered = resolve; });
  const cleanup = async () => {
    events.push('cleanup:start');
    cleanupEntered();
    await new Promise<void>((resolve) => { finishCleanup = resolve; });
    events.push('cleanup:done');
  };
  const lease = {
    release: async () => {
      events.push('lease:release');
      await cleanup();
      return true;
    },
  };

  const publishStartupFailure = (async () => {
    await releasePersonalRealtimeTerminalFocus(lease, cleanup);
    events.push('ui:error');
  })();
  await entered;
  assert.deepEqual(events, ['lease:release', 'cleanup:start']);
  finishCleanup();
  await publishStartupFailure;
  assert.deepEqual(events, ['lease:release', 'cleanup:start', 'cleanup:done', 'ui:error']);
});

test('failed client config drains deferred media activation before deactivation and error publication', async () => {
  const events: string[] = [];
  let fenced = false;
  let resolveActivation!: () => void;
  const activation = new Promise<void>((resolve) => { resolveActivation = resolve; }).then(() => {
    events.push('media:activate');
  });
  const stream = { getTracks: () => [{ stop: () => { events.push('track:stop'); } }] };

  const startup = (async () => {
    try {
      await drainPersonalRealtimeStartup(
        Promise.reject(new Error('config failed')),
        Promise.resolve(stream),
        activation,
        () => { fenced = true; events.push('generation:fence'); },
      );
    } catch {
      stream.getTracks().forEach((track) => track.stop());
      events.push('media:deactivate');
      events.push('ui:error');
    }
  })();

  await new Promise<void>((resolve) => { setImmediate(resolve); });
  assert.equal(fenced, true);
  assert.equal(events.includes('media:deactivate'), false);
  assert.equal(events.includes('ui:error'), false);
  resolveActivation();
  await startup;
  assert.ok(events.indexOf('media:activate') < events.indexOf('media:deactivate'));
  assert.ok(events.indexOf('media:deactivate') < events.indexOf('ui:error'));
});

test('failed client config drains deferred getUserMedia and the generation fence stops its late stream', async () => {
  const events: string[] = [];
  let generation = 41;
  const connectionGeneration = generation;
  let resolveCapture!: (stream: { getTracks(): Array<{ stop(): void }> }) => void;
  const rawCapture = new Promise<{ getTracks(): Array<{ stop(): void }> }>((resolve) => {
    resolveCapture = resolve;
  });
  const capture = rawCapture.then((stream) => {
    if (generation !== connectionGeneration) stream.getTracks().forEach((track) => track.stop());
    return stream;
  });
  const stream = { getTracks: () => [{ stop: () => { events.push('late-track:stop'); } }] };

  const startup = (async () => {
    try {
      await drainPersonalRealtimeStartup(
        Promise.reject(new Error('config failed')),
        capture,
        Promise.resolve().then(() => { events.push('media:activate'); }),
        () => { generation += 1; events.push('generation:fence'); },
      );
    } catch {
      events.push('media:deactivate');
      events.push('ui:error');
    }
  })();

  await new Promise<void>((resolve) => { setImmediate(resolve); });
  assert.equal(events.includes('media:deactivate'), false);
  assert.equal(events.includes('ui:error'), false);
  resolveCapture(stream);
  await startup;
  assert.ok(events.indexOf('generation:fence') < events.indexOf('late-track:stop'));
  assert.ok(events.indexOf('late-track:stop') < events.indexOf('media:deactivate'));
  assert.ok(events.indexOf('media:deactivate') < events.indexOf('ui:error'));
});

test('stop during deferred activation cannot publish idle before a post-drain close', async () => {
  const events: string[] = [];
  let resolveActivation!: () => void;
  const activation = new Promise<void>((resolve) => { resolveActivation = resolve; })
    .then(() => { events.push('media:activate'); });
  const startup = drainPersonalRealtimeStartup(
    Promise.reject(new Error('config failed')),
    Promise.resolve({}),
    activation,
    () => { events.push('generation:fence'); },
  );
  const cleanup = async (publishIdle: boolean) => {
    events.push(publishIdle ? 'media:deactivate:final' : 'media:deactivate:initial');
    if (publishIdle) events.push('ui:idle');
  };

  const close = closePersonalRealtimeStartup(startup, cleanup);
  await new Promise<void>((resolve) => { setImmediate(resolve); });
  assert.equal(events.includes('ui:idle'), false);

  resolveActivation();
  await close;
  assert.ok(events.indexOf('media:activate') < events.indexOf('media:deactivate:final'));
  assert.ok(events.indexOf('media:deactivate:final') < events.indexOf('ui:idle'));
});

test('bounded personal teardown keeps its late finalizer on the stale native generation', async () => {
  const startup = deferred<void>();
  const initialNativeClose = deferred<void>();
  const events: string[] = [];
  const staleGeneration = 10;
  let activeGeneration: number | null = staleGeneration;
  let cleanupCalls = 0;
  const cleanup = async (publishIdle: boolean) => {
    cleanupCalls += 1;
    events.push(`cleanup:${staleGeneration}:${publishIdle ? 'final' : 'initial'}`);
    if (cleanupCalls === 1) await initialNativeClose.promise;
    if (activeGeneration !== null && activeGeneration <= staleGeneration) {
      activeGeneration = null;
    }
  };

  await assert.rejects(
    closePersonalRealtimeStartup(startup.promise, cleanup, 10),
    NativeMediaOperationTimeoutError,
  );
  activeGeneration = 11; // replacement activation after the bounded failure
  startup.resolve();
  await new Promise<void>((resolve) => { setImmediate(resolve); });
  initialNativeClose.resolve();
  await new Promise<void>((resolve) => { setImmediate(resolve); });

  assert.deepEqual(events, [
    'cleanup:10:initial',
    'cleanup:10:final',
  ]);
  assert.equal(activeGeneration, 11, 'late stale cleanup cannot deactivate the replacement');
});

test('hung personal activation cannot wedge later audio-focus requests', async () => {
  const focus = new AudioFocusCoordinator();
  const never = new Promise<void>(() => undefined);
  await focus.acquire('personal_realtime', {
    forceClose: () => closePersonalRealtimeStartup(never, async () => { await never; }, 10),
  });

  await assert.rejects(
    focus.acquire('meeting_media'),
    NativeMediaOperationTimeoutError,
  );
  assert.equal(focus.mode, 'idle');
  const recovered = await focus.acquire('composer_dictation');
  assert.equal(recovered.isCurrent(), true);
});

function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}
