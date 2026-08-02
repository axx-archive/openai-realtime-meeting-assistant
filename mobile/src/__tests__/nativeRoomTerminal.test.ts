import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createNativeRoomTerminalAuthority,
  drainNativeRoomMediaTeardown,
  mergeNativeRoomTerminalPresentation,
  NativeMediaOperationTimeoutError,
  waitForNativeRoomTerminalPresentation,
} from '../realtime/nativeRoomTerminal';
import { AudioFocusCoordinator, type AudioFocusLease } from '../voice/AudioFocusCoordinator';

function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

test('owner terminal release re-enters forceClose without deadlocking or releasing twice', async () => {
  const focus = new AudioFocusCoordinator();
  const events: string[] = [];
  let authority!: ReturnType<typeof createNativeRoomTerminalAuthority>;
  authority = createNativeRoomTerminalAuthority({
    teardownNative: async () => { events.push('native:closed'); },
  });
  const exactLease = await focus.acquire('meeting_media', {
    forceClose: (reason) => authority.terminate(reason, 'focus_coordinator'),
  });
  let releases = 0;
  const observedLease: AudioFocusLease = {
    mode: exactLease.mode,
    generation: exactLease.generation,
    isCurrent: () => exactLease.isCurrent(),
    release: (reason) => {
      releases += 1;
      return exactLease.release(reason);
    },
  };
  authority.bindFocusLease(observedLease);

  await authority.terminate('completed');
  await authority.terminate('error');

  assert.equal(releases, 1);
  assert.deepEqual(events, ['native:closed']);
  assert.equal(focus.mode, 'idle');
});

test('takeover waits for a deferred room activation and final native deactivation', async () => {
  const focus = new AudioFocusCoordinator();
  const activation = deferred<void>();
  const events: string[] = [];
  let deactivations = 0;
  let authority!: ReturnType<typeof createNativeRoomTerminalAuthority>;
  authority = createNativeRoomTerminalAuthority({
    teardownNative: (_reason, drainActivations) => drainNativeRoomMediaTeardown({
      generation: 101,
      disposeMedia: () => { events.push('media:disposed'); },
      drainActivations,
      deactivateMediaSession: (generation) => {
        deactivations += 1;
        events.push(`native:deactivate:${generation}:${deactivations}`);
      },
    }),
  });
  const meeting = await focus.acquire('meeting_media', {
    forceClose: (reason) => authority.terminate(reason, 'focus_coordinator'),
  });
  authority.bindFocusLease(meeting);
  void authority.trackActivation(activation.promise.then(() => {
    events.push('native:activate:done');
  }));

  let replacementSettled = false;
  const replacementPromise = focus.acquire('personal_realtime').then((lease) => {
    replacementSettled = true;
    events.push('replacement:granted');
    return lease;
  });
  await new Promise<void>((resolve) => { setImmediate(resolve); });

  assert.equal(replacementSettled, false);
  assert.deepEqual(events, ['media:disposed', 'native:deactivate:101:1']);
  activation.resolve();
  const replacement = await replacementPromise;

  assert.equal(replacement.isCurrent(), true);
  assert.deepEqual(events, [
    'media:disposed',
    'native:deactivate:101:1',
    'native:activate:done',
    'native:deactivate:101:2',
    'replacement:granted',
  ]);
});

test('terminal owner drains a pending focus admission before returning', async () => {
  const focus = new AudioFocusCoordinator();
  const predecessorClose = deferred<void>();
  let predecessorCloseEntered!: () => void;
  const entered = new Promise<void>((resolve) => { predecessorCloseEntered = resolve; });
  await focus.acquire('personal_realtime', {
    forceClose: async () => {
      predecessorCloseEntered();
      await predecessorClose.promise;
    },
  });

  const events: string[] = [];
  let authority!: ReturnType<typeof createNativeRoomTerminalAuthority>;
  authority = createNativeRoomTerminalAuthority({
    teardownNative: async () => { events.push('native:closed'); },
  });
  const admission = focus.acquire('meeting_media', {
    forceClose: (reason) => authority.terminate(reason, 'focus_coordinator'),
  });
  authority.bindFocusAdmission(admission);
  await entered;

  let terminalSettled = false;
  const terminal = authority.terminate('cancelled').then(() => { terminalSettled = true; });
  await new Promise<void>((resolve) => { setImmediate(resolve); });
  assert.equal(terminalSettled, false);
  assert.deepEqual(events, ['native:closed']);

  predecessorClose.resolve();
  const meeting = await admission;
  authority.bindFocusLease(meeting);
  await terminal;

  assert.equal(meeting.isCurrent(), false);
  assert.equal(focus.mode, 'idle');
});

test('a repeated stale terminal call cannot touch the replacement room authority', async () => {
  const focus = new AudioFocusCoordinator();
  let firstCloses = 0;
  let first!: ReturnType<typeof createNativeRoomTerminalAuthority>;
  first = createNativeRoomTerminalAuthority({
    teardownNative: async () => { firstCloses += 1; },
  });
  const firstLease = await focus.acquire('meeting_media', {
    forceClose: (reason) => first.terminate(reason, 'focus_coordinator'),
  });
  first.bindFocusLease(firstLease);

  let secondCloses = 0;
  let second!: ReturnType<typeof createNativeRoomTerminalAuthority>;
  second = createNativeRoomTerminalAuthority({
    teardownNative: async () => { secondCloses += 1; },
  });
  const secondLease = await focus.acquire('meeting_media', {
    forceClose: (reason) => second.terminate(reason, 'focus_coordinator'),
  });
  second.bindFocusLease(secondLease);

  await first.terminate('error');
  assert.equal(firstCloses, 1);
  assert.equal(secondCloses, 0);
  assert.equal(secondLease.isCurrent(), true);
  assert.equal(focus.mode, 'meeting_media');
});

test('final deactivation still runs after disposal failure', async () => {
  const events: string[] = [];
  await assert.rejects(
    drainNativeRoomMediaTeardown({
      generation: 7,
      disposeMedia: () => {
        events.push('media:dispose');
        throw new Error('dispose failed');
      },
      drainActivations: async () => { events.push('activations:drained'); },
      deactivateMediaSession: (generation) => { events.push(`native:deactivate:${generation}`); },
    }),
    /dispose failed/,
  );
  assert.deepEqual(events, [
    'media:dispose',
    'native:deactivate:7',
    'activations:drained',
    'native:deactivate:7',
  ]);
});

test('a focus release failure still joins deferred native teardown', async () => {
  const cleanup = deferred<void>();
  const authority = createNativeRoomTerminalAuthority({
    teardownNative: async () => { await cleanup.promise; },
  });
  authority.bindFocusLease({
    mode: 'meeting_media',
    generation: 1,
    isCurrent: () => true,
    release: async () => { throw new Error('release failed'); },
  });

  let terminalSettled = false;
  const terminal = authority.terminate('error').finally(() => { terminalSettled = true; });
  await new Promise<void>((resolve) => { setImmediate(resolve); });
  assert.equal(terminalSettled, false);

  cleanup.resolve();
  await assert.rejects(terminal, /release failed/);
  assert.equal(terminalSettled, true);
});

test('terminal drain is bounded while its exact-generation final deactivation continues late', async () => {
  const activation = deferred<void>();
  const events: string[] = [];
  const terminal = drainNativeRoomMediaTeardown({
    generation: 303,
    disposeMedia: () => { events.push('media:disposed'); },
    drainActivations: () => activation.promise,
    deactivateMediaSession: (generation) => {
      events.push(`native:deactivate:${generation}`);
    },
    timeoutMs: 10,
  });

  await assert.rejects(terminal, NativeMediaOperationTimeoutError);
  assert.deepEqual(events, ['media:disposed', 'native:deactivate:303']);

  activation.resolve();
  await new Promise<void>((resolve) => { setImmediate(resolve); });
  assert.deepEqual(events, [
    'media:disposed',
    'native:deactivate:303',
    'native:deactivate:303',
  ]);
});

test('a hung room teardown rejects takeover promptly without poisoning the focus queue', async () => {
  const focus = new AudioFocusCoordinator();
  const never = new Promise<void>(() => undefined);
  let authority!: ReturnType<typeof createNativeRoomTerminalAuthority>;
  authority = createNativeRoomTerminalAuthority({
    teardownNative: (_reason, drainActivations) => drainNativeRoomMediaTeardown({
      generation: 404,
      disposeMedia: () => never,
      drainActivations,
      deactivateMediaSession: () => never,
      timeoutMs: 10,
    }),
  });
  const room = await focus.acquire('meeting_media', {
    forceClose: (reason) => authority.terminate(reason, 'focus_coordinator'),
  });
  authority.bindFocusLease(room);
  void authority.trackActivation(never);

  await assert.rejects(
    focus.acquire('personal_realtime'),
    NativeMediaOperationTimeoutError,
  );
  assert.equal(focus.mode, 'idle');

  const recovered = await focus.acquire('composer_dictation');
  assert.equal(recovered.isCurrent(), true);
  assert.equal(focus.mode, 'composer_dictation');
});

test('admission failure presentation supersedes coordinator leave before publication', async () => {
  const admission = deferred<void>();
  const terminal = deferred<void>();
  let presentation = { kind: 'leave' as const, message: null as string | null };
  let published: typeof presentation | null = null;
  const publication = waitForNativeRoomTerminalPresentation(
    terminal.promise,
    admission.promise,
    'focus_coordinator',
  ).then(() => { published = presentation; });

  terminal.resolve();
  await new Promise<void>((resolve) => { setImmediate(resolve); });
  assert.equal(published, null);

  presentation = mergeNativeRoomTerminalPresentation(presentation, {
    kind: 'failure',
    message: 'focus admission failed',
  }) as typeof presentation;
  admission.reject(new Error('focus admission failed'));
  await publication;
  assert.deepEqual(published, { kind: 'failure', message: 'focus admission failed' });
});
