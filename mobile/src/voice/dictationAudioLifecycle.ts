import type { AudioFocusLease, AudioFocusTerminalReason } from './AudioFocusCoordinator';

export type DictationStartFailureStage = 'permission' | 'audio_mode' | 'prepare' | 'record';

export type DictationCleanupFailure = {
  stage: 'stop' | 'audio_mode' | 'discard';
  error: unknown;
};

export type DictationStartResult =
  | { status: 'started' }
  | { status: 'permission_denied' }
  | { status: 'cancelled'; cleanupFailures: DictationCleanupFailure[] }
  | {
      status: 'failed';
      failure: { stage: DictationStartFailureStage; error: unknown };
      cleanupFailures: DictationCleanupFailure[];
    };

type StartBoundary = {
  requestPermission: () => Promise<boolean>;
  enableRecordingMode: () => Promise<void>;
  prepare: () => Promise<void>;
  record: () => void | Promise<void>;
  stillRequested: () => boolean;
  stopPartialCapture: () => Promise<void>;
  restoreAudioMode: () => Promise<void>;
  discardPartialFile: () => void;
};

async function cleanPartialStart(
  boundary: Pick<StartBoundary, 'stopPartialCapture' | 'restoreAudioMode' | 'discardPartialFile'>,
  recorderMayBeDirty: boolean,
  audioModeMayBeDirty: boolean,
): Promise<DictationCleanupFailure[]> {
  const failures: DictationCleanupFailure[] = [];
  if (recorderMayBeDirty) {
    try {
      await boundary.stopPartialCapture();
    } catch (error) {
      failures.push({ stage: 'stop', error });
    }
  }
  if (audioModeMayBeDirty) {
    try {
      await boundary.restoreAudioMode();
    } catch (error) {
      failures.push({ stage: 'audio_mode', error });
    }
  }
  try {
    boundary.discardPartialFile();
  } catch (error) {
    failures.push({ stage: 'discard', error });
  }
  return failures;
}

/**
 * Owns the native recorder boundary without React state. Mark resources dirty
 * before awaiting native calls because iOS may mutate the audio session before
 * rejecting. Every unsuccessful start therefore gets a best-effort recorder
 * stop, audio-mode restore, and partial-file cleanup in a deterministic order.
 */
export async function beginDictationCapture(boundary: StartBoundary): Promise<DictationStartResult> {
  let stage: DictationStartFailureStage = 'permission';
  let audioModeMayBeDirty = false;
  let recorderMayBeDirty = false;

  try {
    if (!(await boundary.requestPermission())) return { status: 'permission_denied' };

    stage = 'audio_mode';
    audioModeMayBeDirty = true;
    await boundary.enableRecordingMode();

    stage = 'prepare';
    recorderMayBeDirty = true;
    await boundary.prepare();

    if (!boundary.stillRequested()) {
      return {
        status: 'cancelled',
        cleanupFailures: await cleanPartialStart(boundary, recorderMayBeDirty, audioModeMayBeDirty),
      };
    }

    stage = 'record';
    await boundary.record();

    // Native record start can itself suspend while iOS activates the audio
    // session. A hang-up during that await must not publish a listening state
    // after the conversation has already closed.
    if (!boundary.stillRequested()) {
      return {
        status: 'cancelled',
        cleanupFailures: await cleanPartialStart(boundary, recorderMayBeDirty, audioModeMayBeDirty),
      };
    }
    return { status: 'started' };
  } catch (error) {
    return {
      status: 'failed',
      failure: { stage, error },
      cleanupFailures: await cleanPartialStart(boundary, recorderMayBeDirty, audioModeMayBeDirty),
    };
  }
}

type FinishBoundary = {
  /** expo-audio calls this stop(); it is the stop-and-unload native boundary. */
  stopAndUnload: () => Promise<void>;
  restoreAudioMode: () => Promise<void>;
};

export type DictationFinishResult = {
  stopFailure: unknown | null;
  audioModeFailure: unknown | null;
};

/** Reset the recording audio mode even when native stop/unload rejects. */
export async function finishDictationCapture(boundary: FinishBoundary): Promise<DictationFinishResult> {
  let stopFailure: unknown | null = null;
  let audioModeFailure: unknown | null = null;
  try {
    await boundary.stopAndUnload();
  } catch (error) {
    stopFailure = error;
  } finally {
    try {
      await boundary.restoreAudioMode();
    } catch (error) {
      audioModeFailure = error;
    }
  }
  return { stopFailure, audioModeFailure };
}

/**
 * Fence the captured generation before releasing it. The fence prevents the
 * coordinator's close callback from recursively releasing the same lease, and
 * passing the lease explicitly prevents a late completion from touching a
 * newer capture stored by the hook.
 */
export async function settleDictationFocusLease(
  lease: AudioFocusLease | null | undefined,
  reason: AudioFocusTerminalReason,
  fence: (exactLease: AudioFocusLease) => void,
): Promise<unknown | null> {
  if (!lease) return null;
  fence(lease);
  try {
    await lease.release(reason);
    return null;
  } catch (error) {
    return error;
  }
}

export type FallbackVoiceStartAttempt<Lease> = {
  generation: number;
  lease: Lease;
  promise: Promise<boolean>;
};

/**
 * Canvas can rerender while iOS permission/prepare/record is suspended. Calls
 * for the same conversation generation must share that one native start;
 * treating the duplicate hook result as a new failure would cancel the real
 * in-flight capture.
 */
export function runFallbackVoiceStartSingleflight<Lease>(
  slot: { current: FallbackVoiceStartAttempt<Lease> | null },
  generation: number,
  lease: Lease,
  run: () => Promise<boolean>,
): Promise<boolean> {
  const active = slot.current;
  if (active) {
    return active.generation === generation && active.lease === lease
      ? active.promise
      : Promise.resolve(false);
  }

  let exactAttempt!: FallbackVoiceStartAttempt<Lease>;
  const promise = Promise.resolve()
    .then(run)
    .finally(() => {
      if (slot.current === exactAttempt) slot.current = null;
    });
  exactAttempt = { generation, lease, promise };
  slot.current = exactAttempt;
  return promise;
}
