import assert from 'node:assert/strict';
import test from 'node:test';
import { AudioFocusCoordinator, type AudioFocusLease } from '../voice/AudioFocusCoordinator';
import {
  beginDictationCapture,
  finishDictationCapture,
  runFallbackVoiceStartSingleflight,
  settleDictationFocusLease,
  type FallbackVoiceStartAttempt,
} from '../voice/dictationAudioLifecycle';

async function parkMeeting(events: string[]): Promise<{ focus: AudioFocusCoordinator; lease: AudioFocusLease }> {
  const focus = new AudioFocusCoordinator();
  await focus.acquire('meeting_media', {
    parkRoomMute: () => {
      events.push('room:park:false');
      return false;
    },
    restoreRoomMute: (wasMuted) => {
      events.push(`room:restore:${wasMuted}`);
    },
  });
  const lease = await focus.acquire('composer_dictation');
  assert.equal(focus.mode, 'composer_dictation');
  return { focus, lease };
}

for (const failureStage of ['audio_mode', 'prepare', 'record'] as const) {
  test(`a ${failureStage} start failure cleans partial capture and restores the parked meeting`, async () => {
    const events: string[] = [];
    const { focus, lease } = await parkMeeting(events);
    const failure = new Error(failureStage);
    const result = await beginDictationCapture({
      requestPermission: async () => {
        events.push('permission');
        return true;
      },
      enableRecordingMode: async () => {
        events.push('audio:recording');
        if (failureStage === 'audio_mode') throw failure;
      },
      prepare: async () => {
        events.push('recorder:prepare');
        if (failureStage === 'prepare') throw failure;
      },
      record: () => {
        events.push('recorder:record');
        if (failureStage === 'record') throw failure;
      },
      stillRequested: () => true,
      stopPartialCapture: async () => {
        events.push('recorder:stop-partial');
      },
      restoreAudioMode: async () => {
        events.push('audio:playback');
      },
      discardPartialFile: () => {
        events.push('file:discard-partial');
      },
    });

    assert.equal(result.status, 'failed');
    if (result.status !== 'failed') return;
    assert.equal(result.failure.stage, failureStage);
    assert.equal(result.failure.error, failure);
    assert.deepEqual(result.cleanupFailures, []);

    const releaseFailure = await settleDictationFocusLease(lease, 'error', (exactLease) => {
      assert.equal(exactLease, lease);
      events.push(`focus:fence:${exactLease.generation}`);
    });
    assert.equal(releaseFailure, null);
    assert.equal(focus.mode, 'meeting_media');
    assert.equal(events.at(-1), 'room:restore:false');
    assert.ok(events.indexOf('audio:playback') < events.indexOf(`focus:fence:${lease.generation}`));
    assert.ok(events.includes('file:discard-partial'));
    assert.equal(events.includes('recorder:stop-partial'), failureStage !== 'audio_mode');
  });
}

test('a partial-recorder cleanup failure cannot skip audio reset, file cleanup, or exact lease release', async () => {
  const events: string[] = [];
  const { focus, lease } = await parkMeeting(events);
  const result = await beginDictationCapture({
    requestPermission: async () => true,
    enableRecordingMode: async () => { events.push('audio:recording'); },
    prepare: async () => { throw new Error('prepare'); },
    record: () => {},
    stillRequested: () => true,
    stopPartialCapture: async () => {
      events.push('recorder:stop-partial');
      throw new Error('stop cleanup');
    },
    restoreAudioMode: async () => { events.push('audio:playback'); },
    discardPartialFile: () => { events.push('file:discard-partial'); },
  });
  assert.equal(result.status, 'failed');
  if (result.status !== 'failed') return;
  assert.deepEqual(result.cleanupFailures.map(({ stage }) => stage), ['stop']);
  assert.deepEqual(events.slice(-3), ['recorder:stop-partial', 'audio:playback', 'file:discard-partial']);

  await settleDictationFocusLease(lease, 'error', () => { events.push('focus:fence'); });
  assert.equal(focus.mode, 'meeting_media');
  assert.equal(events.at(-1), 'room:restore:false');
});

test('stop-and-unload failure still resets audio and restores the exact parked-room mute state', async () => {
  const events: string[] = [];
  const { focus, lease } = await parkMeeting(events);
  const stopFailure = new Error('stop');
  const result = await finishDictationCapture({
    stopAndUnload: async () => {
      events.push('recorder:stop-and-unload');
      throw stopFailure;
    },
    restoreAudioMode: async () => { events.push('audio:playback'); },
  });
  assert.equal(result.stopFailure, stopFailure);
  assert.equal(result.audioModeFailure, null);
  assert.deepEqual(events.slice(-2), ['recorder:stop-and-unload', 'audio:playback']);

  await settleDictationFocusLease(lease, 'error', () => { events.push('focus:fence'); });
  assert.equal(focus.mode, 'meeting_media');
  assert.equal(events.at(-1), 'room:restore:false');
});

test('audio-mode reset failure still releases the exact lease and restores the parked room', async () => {
  const events: string[] = [];
  const { focus, lease } = await parkMeeting(events);
  const audioModeFailure = new Error('audio mode reset');
  const result = await finishDictationCapture({
    stopAndUnload: async () => { events.push('recorder:stop-and-unload'); },
    restoreAudioMode: async () => {
      events.push('audio:playback');
      throw audioModeFailure;
    },
  });
  assert.equal(result.stopFailure, null);
  assert.equal(result.audioModeFailure, audioModeFailure);

  await settleDictationFocusLease(lease, 'error', () => { events.push('focus:fence'); });
  assert.equal(focus.mode, 'meeting_media');
  assert.equal(events.at(-1), 'room:restore:false');
});

test('lease settlement fences before release and cannot clear a newer generation', async () => {
  const events: string[] = [];
  const oldLease: AudioFocusLease = {
    mode: 'composer_dictation',
    generation: 7,
    isCurrent: () => false,
    release: async () => {
      events.push('old:release');
      return false;
    },
  };
  const newLease: AudioFocusLease = {
    mode: 'composer_dictation',
    generation: 8,
    isCurrent: () => true,
    release: async () => {
      events.push('new:release');
      return true;
    },
  };
  let slot: AudioFocusLease | null = newLease;
  await settleDictationFocusLease(oldLease, 'error', (exactLease) => {
    events.push(`fence:${exactLease.generation}`);
    if (slot === exactLease) slot = null;
  });
  assert.equal(slot, newLease);
  assert.deepEqual(events, ['fence:7', 'old:release']);
});

test('hang-up during deferred native record start cleans capture and never becomes listening', async () => {
  const events: string[] = [];
  let stillRequested = true;
  let finishRecord!: () => void;
  const recordStarted = new Promise<void>((resolve) => { finishRecord = resolve; });
  let enteredRecord!: () => void;
  const entered = new Promise<void>((resolve) => { enteredRecord = resolve; });

  const start = beginDictationCapture({
    requestPermission: async () => true,
    enableRecordingMode: async () => { events.push('audio:recording'); },
    prepare: async () => { events.push('recorder:prepare'); },
    record: async () => {
      events.push('recorder:record:start');
      enteredRecord();
      await recordStarted;
      events.push('recorder:record:return');
    },
    stillRequested: () => stillRequested,
    stopPartialCapture: async () => { events.push('recorder:stop-partial'); },
    restoreAudioMode: async () => { events.push('audio:playback'); },
    discardPartialFile: () => { events.push('file:discard-partial'); },
  });

  await entered;
  stillRequested = false; // Canvas hang-up synchronously cancels this generation.
  finishRecord();
  const result = await start;

  assert.equal(result.status, 'cancelled');
  assert.deepEqual(events.slice(-3), [
    'recorder:stop-partial',
    'audio:playback',
    'file:discard-partial',
  ]);
  assert.equal(events.includes('listening'), false);
});

test('same-generation Canvas rearm calls share one deferred native start', async () => {
  const lease = { generation: 11 };
  const slot: { current: FallbackVoiceStartAttempt<typeof lease> | null } = { current: null };
  let resolveStart!: (started: boolean) => void;
  const deferredStart = new Promise<boolean>((resolve) => { resolveStart = resolve; });
  let starts = 0;
  let cancels = 0;
  let ends = 0;
  let releases = 0;
  const work = async () => {
    starts += 1;
    const started = await deferredStart;
    if (!started) {
      cancels += 1;
      ends += 1;
      releases += 1;
    }
    return started;
  };

  const first = runFallbackVoiceStartSingleflight(slot, 7, lease, work);
  const duplicate = runFallbackVoiceStartSingleflight(slot, 7, lease, work);
  await Promise.resolve();
  assert.equal(starts, 1);
  assert.equal(first, duplicate);

  resolveStart(true);
  assert.deepEqual(await Promise.all([first, duplicate]), [true, true]);
  assert.equal(slot.current, null);
  assert.deepEqual({ starts, cancels, ends, releases }, { starts: 1, cancels: 0, ends: 0, releases: 0 });
});
