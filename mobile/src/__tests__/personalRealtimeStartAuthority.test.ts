import assert from 'node:assert/strict';
import test from 'node:test';
import {
  personalRealtimeStartAuthorityIsCurrent,
  runPersonalRealtimeGuardedStage,
  type PersonalRealtimeStartAuthorityLive,
  type PersonalRealtimeStartAuthoritySnapshot,
} from '../realtime/personalRealtimeStartAuthority';
import { releasePersonalRealtimeTerminalFocus } from '../realtime/personalRealtimeTerminal';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
}

async function runGuardedAdmission(options: {
  isCurrent(): boolean;
  qualification(): Promise<boolean>;
  control(): Promise<boolean>;
  acquire(): Promise<{ isCurrent(): boolean; release(): Promise<boolean> }>;
  retireStale(lease: { release(): Promise<boolean> }): Promise<void>;
  capture(): void;
}): Promise<void> {
  const qualification = await runPersonalRealtimeGuardedStage({
    isCurrent: options.isCurrent,
    run: options.qualification,
  });
  if (!qualification.current || !qualification.value) return;

  const control = await runPersonalRealtimeGuardedStage({
    isCurrent: options.isCurrent,
    run: options.control,
  });
  if (!control.current || !control.value) return;

  const focus = await runPersonalRealtimeGuardedStage({
    isCurrent: options.isCurrent,
    run: options.acquire,
    retireStale: options.retireStale,
  });
  if (!focus.current || !focus.value.isCurrent()) return;
  options.capture();
}

test('a late qualification completion cannot advance to control, focus, or capture', async () => {
  let authorityCurrent = true;
  const qualification = deferred<boolean>();
  let controlStarts = 0;
  let focusStarts = 0;
  let captures = 0;
  const attempt = runGuardedAdmission({
    isCurrent: () => authorityCurrent,
    qualification: () => qualification.promise,
    control: async () => { controlStarts += 1; return true; },
    acquire: async () => {
      focusStarts += 1;
      return { isCurrent: () => true, release: async () => true };
    },
    retireStale: async () => undefined,
    capture: () => { captures += 1; },
  });
  authorityCurrent = false;
  qualification.resolve(true);
  await attempt;
  assert.deepEqual({ controlStarts, focusStarts, captures }, {
    controlStarts: 0,
    focusStarts: 0,
    captures: 0,
  });
});

test('server qualification revocation synchronously fences a same-token startup before focus or capture', async () => {
  const snapshot: PersonalRealtimeStartAuthoritySnapshot = {
    sessionToken: 'session-a',
    authStorageGeneration: 8,
    connectionGeneration: 13,
    qualificationEpoch: 21,
  };
  const live: PersonalRealtimeStartAuthorityLive = {
    mounted: true,
    liveSessionToken: 'session-a',
    qualifiedAuthorityToken: 'session-a',
    authStorageGeneration: 8,
    connectionGeneration: 13,
    qualificationEpoch: 21,
  };
  const control = deferred<boolean>();
  let controlStarts = 0;
  let focusStarts = 0;
  let captures = 0;
  const attempt = runGuardedAdmission({
    isCurrent: () => personalRealtimeStartAuthorityIsCurrent(snapshot, live),
    qualification: async () => true,
    control: () => { controlStarts += 1; return control.promise; },
    acquire: async () => {
      focusStarts += 1;
      return { isCurrent: () => true, release: async () => true };
    },
    retireStale: async () => undefined,
    capture: () => { captures += 1; },
  });
  for (let index = 0; index < 10 && controlStarts === 0; index += 1) await Promise.resolve();
  assert.equal(controlStarts, 1);

  // `/client-config` returned false while the authenticated token stayed the
  // same. The hook performs these ref writes before setState/effect teardown.
  live.qualifiedAuthorityToken = '';
  live.qualificationEpoch += 1;
  control.resolve(true);
  await attempt;

  assert.deepEqual({ focusStarts, captures }, { focusStarts: 0, captures: 0 });
  assert.equal(personalRealtimeStartAuthorityIsCurrent(snapshot, live), false);
});

test('a late control completion cannot advance to focus or capture', async () => {
  let authorityCurrent = true;
  const control = deferred<boolean>();
  let controlStarts = 0;
  let focusStarts = 0;
  let captures = 0;
  const attempt = runGuardedAdmission({
    isCurrent: () => authorityCurrent,
    qualification: async () => true,
    control: () => { controlStarts += 1; return control.promise; },
    acquire: async () => {
      focusStarts += 1;
      return { isCurrent: () => true, release: async () => true };
    },
    retireStale: async () => undefined,
    capture: () => { captures += 1; },
  });
  for (let index = 0; index < 10 && controlStarts === 0; index += 1) await Promise.resolve();
  assert.equal(controlStarts, 1);
  authorityCurrent = false;
  control.resolve(true);
  await attempt;
  assert.deepEqual({ focusStarts, captures }, { focusStarts: 0, captures: 0 });
});

test('a focus lease granted after authority loss is retired with stale-release cleanup fallback', async () => {
  let authorityCurrent = true;
  const focus = deferred<{ isCurrent(): boolean; release(): Promise<boolean> }>();
  let focusStarts = 0;
  let releases = 0;
  let cleanupFallbacks = 0;
  let captures = 0;
  const attempt = runGuardedAdmission({
    isCurrent: () => authorityCurrent,
    qualification: async () => true,
    control: async () => true,
    acquire: () => {
      focusStarts += 1;
      return focus.promise;
    },
    retireStale: (lease) => releasePersonalRealtimeTerminalFocus(
      lease,
      async () => { cleanupFallbacks += 1; },
      'cancelled',
    ),
    capture: () => { captures += 1; },
  });
  for (let index = 0; index < 10 && focusStarts === 0; index += 1) await Promise.resolve();
  assert.equal(focusStarts, 1);
  authorityCurrent = false;
  focus.resolve({
    isCurrent: () => false,
    release: async () => { releases += 1; return false; },
  });
  await attempt;
  assert.deepEqual({ focusStarts, releases, cleanupFallbacks, captures }, {
    focusStarts: 1,
    releases: 1,
    cleanupFallbacks: 1,
    captures: 0,
  });
});
