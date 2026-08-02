import assert from 'node:assert/strict';
import test from 'node:test';
import { AudioFocusCoordinator } from '../voice/AudioFocusCoordinator';

test('audio focus serializes mutually exclusive owners and records terminal supersession', async () => {
  const focus = new AudioFocusCoordinator();
  const terminal: string[] = [];
  const dictation = await focus.acquire('composer_dictation', {
    forceClose: (reason) => { terminal.push(`dictation:${reason}`); },
  });
  const meeting = await focus.acquire('meeting_media');

  assert.equal(dictation.isCurrent(), false);
  assert.equal(meeting.isCurrent(), true);
  assert.equal(focus.mode, 'meeting_media');
  assert.deepEqual(terminal, ['dictation:superseded_by_meeting_media']);
});

test('a stale lease cannot release a newer microphone generation', async () => {
  const focus = new AudioFocusCoordinator();
  const first = await focus.acquire('personal_realtime');
  const second = await focus.acquire('composer_dictation');

  assert.equal(await first.release(), false);
  assert.equal(second.isCurrent(), true);
  assert.equal(focus.mode, 'composer_dictation');
});

test('private in-room dictation restores the exact previous room mute state on forced close', async () => {
  const focus = new AudioFocusCoordinator();
  const restored: boolean[] = [];
  await focus.acquire('composer_dictation', {
    parkRoomMute: () => true,
    restoreRoomMute: (wasMuted) => { restored.push(wasMuted); },
  });
  await focus.acquire('personal_realtime');
  assert.deepEqual(restored, [true]);
});

test('composer dictation parks a live meeting and resumes it without leaving', async () => {
  const focus = new AudioFocusCoordinator();
  const events: string[] = [];
  const meeting = await focus.acquire('meeting_media', {
    forceClose: (reason) => { events.push(`left:${reason}`); },
    parkRoomMute: () => { events.push('parked'); return true; },
    restoreRoomMute: (wasMuted) => { events.push(`restored:${wasMuted}`); },
  });
  const dictation = await focus.acquire('composer_dictation');

  assert.equal(meeting.isCurrent(), false);
  assert.equal(dictation.isCurrent(), true);
  assert.deepEqual(events, ['parked']);

  assert.equal(await dictation.release('completed'), true);
  assert.equal(meeting.isCurrent(), true);
  assert.equal(focus.mode, 'meeting_media');
  assert.deepEqual(events, ['parked', 'restored:true']);
});

test('leaving a meeting while dictation is parked closes both generations safely', async () => {
  const focus = new AudioFocusCoordinator();
  const events: string[] = [];
  const meeting = await focus.acquire('meeting_media', {
    forceClose: () => { events.push('meeting-closed'); },
    parkRoomMute: () => false,
    restoreRoomMute: (wasMuted) => { events.push(`restored:${wasMuted}`); },
  });
  await focus.acquire('composer_dictation', {
    forceClose: () => { events.push('dictation-closed'); },
  });

  assert.equal(await meeting.release('completed'), true);
  assert.equal(focus.mode, 'idle');
  assert.deepEqual(events, ['dictation-closed', 'restored:false', 'meeting-closed']);
});

test('acquire waits for forced-close acknowledgement before granting the next lease', async () => {
  const focus = new AudioFocusCoordinator();
  let close!: () => void;
  let entered!: () => void;
  const closeEntered = new Promise<void>((resolve) => { entered = resolve; });
  const first = await focus.acquire('personal_realtime', {
    forceClose: () => new Promise<void>((resolve) => { close = resolve; entered(); }),
  });
  let settled = false;
  const next = focus.acquire('meeting_media').then((lease) => { settled = true; return lease; });
  await closeEntered;
  assert.equal(settled, false);
  assert.equal(first.isCurrent(), false, 'old generation is fenced before its close resolves');
  close();
  const lease = await next;
  assert.equal(lease.isCurrent(), true);
});

test('overlapping takeovers close a superseded pending owner and only grant the latest intent', async () => {
  const focus = new AudioFocusCoordinator();
  const terminal: string[] = [];
  let allowFirstClose!: () => void;
  let firstCloseEntered!: () => void;
  const closeEntered = new Promise<void>((resolve) => { firstCloseEntered = resolve; });
  const first = await focus.acquire('composer_dictation', {
    forceClose: (reason) => new Promise<void>((resolve) => {
      terminal.push(`first:${reason}`);
      allowFirstClose = resolve;
      firstCloseEntered();
    }),
  });

  const middlePromise = focus.acquire('personal_realtime', {
    forceClose: (reason) => { terminal.push(`middle:${reason}`); },
  });
  await closeEntered;
  const latestPromise = focus.acquire('meeting_media', {
    forceClose: (reason) => { terminal.push(`latest:${reason}`); },
  });

  assert.equal(first.isCurrent(), false, 'the original lease is fenced while its close is pending');
  allowFirstClose();
  const [middle, latest] = await Promise.all([middlePromise, latestPromise]);

  assert.equal(middle.isCurrent(), false, 'the superseded pending lease is never granted');
  assert.equal(await middle.release(), false, 'a stale pending lease cannot release the winner');
  assert.equal(latest.isCurrent(), true);
  assert.equal(focus.mode, 'meeting_media');
  assert.deepEqual(terminal, [
    'first:superseded_by_personal_realtime',
    'middle:superseded_by_meeting_media',
  ]);
});

test('a superseded slow meeting park restores mute, closes the pending composer, and leaves for the winner', async () => {
  const focus = new AudioFocusCoordinator();
  const events: string[] = [];
  let finishPark!: () => void;
  let parkEntered!: () => void;
  const enteredPark = new Promise<void>((resolve) => { parkEntered = resolve; });
  const meeting = await focus.acquire('meeting_media', {
    forceClose: (reason) => { events.push(`meeting:${reason}`); },
    parkRoomMute: () => new Promise<boolean>((resolve) => {
      events.push('meeting:park');
      finishPark = () => resolve(false);
      parkEntered();
    }),
    restoreRoomMute: (wasMuted) => { events.push(`meeting:restore:${wasMuted}`); },
  });
  const composerPromise = focus.acquire('composer_dictation', {
    forceClose: (reason) => { events.push(`composer:${reason}`); },
  });
  await enteredPark;
  const realtimePromise = focus.acquire('personal_realtime');

  assert.equal(meeting.isCurrent(), false);
  finishPark();
  const [composer, realtime] = await Promise.all([composerPromise, realtimePromise]);

  assert.equal(composer.isCurrent(), false);
  assert.equal(realtime.isCurrent(), true);
  assert.deepEqual(events, [
    'meeting:park',
    'composer:superseded_by_personal_realtime',
    'meeting:restore:false',
    'meeting:superseded_by_personal_realtime',
  ]);
});

test('release is linearizable and idempotent and closes an owner exactly once', async () => {
  const focus = new AudioFocusCoordinator();
  const terminal: string[] = [];
  const lease = await focus.acquire('personal_realtime', {
    forceClose: (reason) => { terminal.push(reason); },
  });

  const firstRelease = lease.release('completed');
  const duplicateRelease = lease.release('cancelled');
  assert.equal(lease.isCurrent(), false, 'release intent fences the lease synchronously');
  assert.equal(await firstRelease, true);
  assert.equal(await duplicateRelease, false);
  assert.equal(await lease.release('error'), false);
  assert.equal(focus.mode, 'idle');
  assert.deepEqual(terminal, ['completed']);
});

test('a predecessor close failure aborts and closes the pending owner without poisoning later focus', async () => {
  const focus = new AudioFocusCoordinator();
  const terminal: string[] = [];
  await focus.acquire('composer_dictation', {
    forceClose: (reason) => {
      terminal.push(`first:${reason}`);
      throw new Error('native close failed');
    },
  });

  await assert.rejects(
    focus.acquire('personal_realtime', {
      forceClose: (reason) => { terminal.push(`pending:${reason}`); },
    }),
    /native close failed/,
  );
  assert.equal(focus.mode, 'idle');
  assert.deepEqual(terminal, [
    'first:superseded_by_personal_realtime',
    'pending:error',
  ]);

  const recovered = await focus.acquire('meeting_media');
  assert.equal(recovered.isCurrent(), true);
  assert.equal(focus.mode, 'meeting_media');
});
