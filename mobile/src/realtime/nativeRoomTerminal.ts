import type {
  AudioFocusLease,
  AudioFocusTerminalReason,
} from '../voice/AudioFocusCoordinator';

export type NativeRoomTerminalSource = 'owner' | 'focus_coordinator';
export type NativeRoomTerminalPresentationKind = 'leave' | 'failure' | 'unmount';

export type NativeRoomTerminalPresentation = {
  kind: NativeRoomTerminalPresentationKind | null;
  message: string | null;
};

export class NativeMediaOperationTimeoutError extends Error {
  constructor(operation: string, timeoutMs: number) {
    super(`${operation} did not finish within ${timeoutMs}ms.`);
    this.name = 'NativeMediaOperationTimeoutError';
  }
}

/**
 * Bound only the caller's wait. The native promise remains observed and is
 * allowed to finish behind its generation fence, so a late rejection is never
 * unhandled and a late completion cannot regain authority.
 */
export function waitForBoundedNativeOperation<T>(
  operation: Promise<T>,
  timeoutMs: number,
  label: string,
): Promise<T> {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    return Promise.reject(new NativeMediaOperationTimeoutError(label, timeoutMs));
  }
  return new Promise<T>((resolve, reject) => {
    let settled = false;
    const timeout = setTimeout(() => {
      if (settled) return;
      settled = true;
      reject(new NativeMediaOperationTimeoutError(label, timeoutMs));
    }, timeoutMs);
    void operation.then(
      (value) => {
        if (settled) return;
        settled = true;
        clearTimeout(timeout);
        resolve(value);
      },
      (error) => {
        if (settled) return;
        settled = true;
        clearTimeout(timeout);
        reject(error);
      },
    );
  });
}

/** Failure beats a routine leave, while unmount always suppresses late UI. */
export function mergeNativeRoomTerminalPresentation(
  current: NativeRoomTerminalPresentation,
  incoming: NativeRoomTerminalPresentation,
): NativeRoomTerminalPresentation {
  if (current.kind === 'unmount' || incoming.kind === 'unmount') {
    return { kind: 'unmount', message: null };
  }
  if (incoming.kind === 'failure') {
    return { kind: 'failure', message: incoming.message ?? current.message };
  }
  if (current.kind === 'failure') return current;
  return current.kind ? current : incoming;
}

/**
 * A focus callback may terminalize a not-yet-granted room while acquire() is
 * still unwinding. Delay only UI publication (never the coordinator callback)
 * until that admission settles, giving its catch path time to promote leave to
 * failure with the actual admission error.
 */
export async function waitForNativeRoomTerminalPresentation(
  terminal: Promise<void>,
  focusAdmission: Promise<unknown> | null,
  source: NativeRoomTerminalSource,
): Promise<void> {
  if (source !== 'focus_coordinator' || !focusAdmission) {
    await terminal;
    return;
  }
  const [terminalResult] = await Promise.allSettled([terminal, focusAdmission]);
  if (terminalResult.status === 'rejected') throw terminalResult.reason;
}

type NativeRoomTerminalAuthorityOptions = {
  teardownNative(reason: AudioFocusTerminalReason, drainActivations: () => Promise<void>): Promise<void>;
};

export type NativeRoomTerminalAuthority = {
  isTerminal(): boolean;
  bindFocusAdmission(admission: Promise<AudioFocusLease>): void;
  bindFocusLease(lease: AudioFocusLease): void;
  trackActivation<T>(activation: Promise<T>): Promise<T>;
  terminate(
    reason: AudioFocusTerminalReason,
    source?: NativeRoomTerminalSource,
  ): Promise<void>;
};

/**
 * Owns one room's audio authority from pending admission through terminal
 * native teardown. The focus-coordinator entry point returns only the native
 * teardown promise: returning the owner completion would wait on the same
 * lease.release() call that invoked forceClose and deadlock the focus queue.
 */
export function createNativeRoomTerminalAuthority(
  options: NativeRoomTerminalAuthorityOptions,
): NativeRoomTerminalAuthority {
  let terminal = false;
  let focusAdmission: Promise<AudioFocusLease> | null = null;
  let focusLease: AudioFocusLease | null = null;
  let focusReleaseStarted = false;
  let coordinatorOwnsRelease = false;
  let nativeTeardown: Promise<void> | null = null;
  let ownerCompletion: Promise<void> | null = null;
  const activations = new Set<Promise<unknown>>();

  const drainActivations = async (): Promise<void> => {
    // terminal is set before teardown starts, so no new activation can be
    // admitted. allSettled drains late native completions without allowing a
    // failed activation to skip final deactivation.
    await Promise.allSettled([...activations]);
  };

  const beginNativeTeardown = (reason: AudioFocusTerminalReason): Promise<void> => {
    terminal = true;
    if (!nativeTeardown) {
      try {
        nativeTeardown = Promise.resolve(options.teardownNative(reason, drainActivations));
      } catch (error) {
        nativeTeardown = Promise.reject(error);
      }
    }
    return nativeTeardown;
  };

  const authority: NativeRoomTerminalAuthority = {
    isTerminal: () => terminal,
    bindFocusAdmission: (admission) => {
      if (focusAdmission && focusAdmission !== admission) {
        throw new Error('Native room focus admission is already bound.');
      }
      focusAdmission = admission;
    },
    bindFocusLease: (lease) => {
      if (focusLease && focusLease !== lease) {
        throw new Error('Native room focus lease is already bound.');
      }
      focusLease = lease;
    },
    trackActivation: <T>(activation: Promise<T>): Promise<T> => {
      if (terminal) {
        return Promise.reject(new Error('Native room audio authority is already terminal.'));
      }
      activations.add(activation);
      void activation.then(
        () => { activations.delete(activation); },
        () => { activations.delete(activation); },
      );
      return activation;
    },
    terminate: (
      reason: AudioFocusTerminalReason,
      source: NativeRoomTerminalSource = 'owner',
    ): Promise<void> => {
      const exactNativeTeardown = beginNativeTeardown(reason);
      if (source === 'focus_coordinator') {
        // AudioFocusCoordinator already owns invalidation/release in this
        // branch. Null our handle exactly once and let its close() await the
        // exact native work before granting a replacement microphone owner.
        coordinatorOwnsRelease = true;
        focusLease = null;
        return exactNativeTeardown;
      }
      if (!ownerCompletion) {
        ownerCompletion = (async () => {
          let terminalError: unknown;
          let exactLease = focusLease;
          if (!exactLease && focusAdmission && !coordinatorOwnsRelease) {
            try {
              exactLease = await focusAdmission;
            } catch (error) {
              // Admission failure cannot skip local/native cleanup.
              terminalError = error;
            }
          }
          if (!coordinatorOwnsRelease && exactLease && !focusReleaseStarted) {
            focusReleaseStarted = true;
            focusLease = null;
            try {
              await exactLease.release(reason);
            } catch (error) {
              if (terminalError === undefined) terminalError = error;
            }
          }
          try {
            await exactNativeTeardown;
          } catch (error) {
            if (terminalError === undefined) terminalError = error;
          }
          if (terminalError !== undefined) throw terminalError;
        })();
      }
      return ownerCompletion;
    },
  };
  return authority;
}

/**
 * Stops current resources immediately, drains every activation that was
 * already admitted, then deactivates once more. The second deactivation is the
 * fence against a delayed native activation resolving after an early close.
 */
export async function drainNativeRoomMediaTeardown(options: {
  generation: number;
  disposeMedia(): void | Promise<void>;
  drainActivations(): Promise<void>;
  deactivateMediaSession(generation: number): void | Promise<unknown>;
  timeoutMs?: number;
}): Promise<void> {
  let firstError: unknown;
  const observe = async (operation: void | Promise<unknown>): Promise<void> => {
    try {
      await operation;
    } catch (error) {
      if (firstError === undefined) firstError = error;
    }
  };

  const begin = (operation: () => void | Promise<unknown>): Promise<void> => {
    try {
      return observe(operation());
    } catch (error) {
      if (firstError === undefined) firstError = error;
      return Promise.resolve();
    }
  };

  const disposal = begin(options.disposeMedia);
  // Retire this generation immediately. The native generation high-water is
  // the safety boundary when activation completion is delayed past JS timeout.
  const initialDeactivation = begin(() => options.deactivateMediaSession(options.generation));
  const activationDrain = begin(options.drainActivations);

  // This continuation intentionally survives a bounded caller timeout. Once a
  // delayed activation reports completion, issue one last deactivation for the
  // SAME generation. Native code ignores it if a newer owner already won.
  const finalDeactivation = activationDrain.then(() => (
    begin(() => options.deactivateMediaSession(options.generation))
  ));
  const terminalDrain = Promise.all([
    disposal,
    initialDeactivation,
    finalDeactivation,
  ]).then(() => {
    if (firstError !== undefined) throw firstError;
  });

  try {
    await waitForBoundedNativeOperation(
      terminalDrain,
      options.timeoutMs ?? 2_500,
      'Native room teardown',
    );
  } catch (error) {
    // Keep the continuation observed after the bounded handoff. It may still
    // release exact-device camera work and retire the old native generation.
    void terminalDrain.catch(() => undefined);
    throw error;
  }
}
