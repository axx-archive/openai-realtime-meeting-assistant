import type { AudioFocusLease, AudioFocusTerminalReason } from '../voice/AudioFocusCoordinator';
import {
  NativeMediaOperationTimeoutError,
  waitForBoundedNativeOperation,
} from './nativeRoomTerminal';

export type PersonalRealtimeCleanupScope = 'owned' | 'detached' | 'replacement';

/**
 * Decide whether a terminal continuation still owns the hook's shared
 * transport refs. Once a newer media-session generation exists, stale cleanup
 * may retire only its exact native generation; it must not detach the newer
 * peer, stream, callbacks, or UI state.
 */
export function personalRealtimeCleanupScope(
  expectedGeneration: number | null,
  currentGeneration: number | null,
): PersonalRealtimeCleanupScope {
  if (expectedGeneration === null || currentGeneration === expectedGeneration) {
    return 'owned';
  }
  return currentGeneration === null ? 'detached' : 'replacement';
}

type ClosableDataChannel = {
  onopen?: unknown;
  onmessage?: unknown;
  onerror?: unknown;
  onclose?: unknown;
  close(): void;
};

type ClosablePeer = {
  ontrack?: unknown;
  onconnectionstatechange?: unknown;
  onicegatheringstatechange?: unknown;
  close(): void;
};

type StoppableStream = {
  getTracks(): Array<{ stop(): void }>;
};

/**
 * Close every local transport resource even when one browser/native handle is
 * already terminal. References are detached by the caller before this runs so
 * a late callback cannot target a newer session.
 */
export async function closePersonalRealtimeTransportResources(options: {
  dataChannel: ClosableDataChannel | null;
  peer: ClosablePeer | null;
  stream: StoppableStream | null;
  deactivateMediaSession(): void | Promise<unknown>;
}): Promise<void> {
  if (options.dataChannel) {
    options.dataChannel.onopen = null;
    options.dataChannel.onmessage = null;
    options.dataChannel.onerror = null;
    options.dataChannel.onclose = null;
    try { options.dataChannel.close(); } catch { /* Already closed. */ }
  }
  if (options.peer) {
    options.peer.ontrack = null;
    options.peer.onconnectionstatechange = null;
    options.peer.onicegatheringstatechange = null;
    try { options.peer.close(); } catch { /* Already closed. */ }
  }
  for (const track of options.stream?.getTracks() ?? []) {
    try { track.stop(); } catch { /* Already stopped. */ }
  }
  await options.deactivateMediaSession();
}

/**
 * Releasing the focus lease normally invokes the registered force-close hook,
 * which performs transport cleanup. If the lease was already stale or its
 * callback failed, run the same cleanup directly so terminal UI never leaves a
 * microphone or peer alive.
 */
export async function releasePersonalRealtimeTerminalFocus(
  lease: Pick<AudioFocusLease, 'release'> | null,
  cleanupTransport: () => Promise<void>,
  reason: AudioFocusTerminalReason = 'error',
): Promise<void> {
  let released = false;
  let releaseError: unknown;
  try {
    released = Boolean(await lease?.release(reason));
  } catch (error) {
    releaseError = error;
  }
  // A terminal timeout means the lease's exact-generation cleanup is already
  // running behind its native fence. Starting a second generic cleanup here can
  // only re-wedge the caller (and could target newer JS refs), so surface the
  // honest failure and leave that observed continuation in charge.
  if (!released && !(releaseError instanceof NativeMediaOperationTimeoutError)) {
    await cleanupTransport();
  }
  if (releaseError !== undefined) throw releaseError;
}

/**
 * Start all Realtime prerequisites together, fence on the first rejection, and
 * still drain every sibling before surfacing that failure. This prevents a
 * delayed native media activation from resolving after terminal deactivation.
 */
export async function drainPersonalRealtimeStartup<A, B, C>(
  first: Promise<A>,
  second: Promise<B>,
  third: Promise<C>,
  onFirstFailure: (error: unknown) => void,
): Promise<[A, B, C]> {
  let firstFailure: unknown;
  let failed = false;
  const observe = async <T>(promise: Promise<T>): Promise<T> => {
    try {
      return await promise;
    } catch (error) {
      if (!failed) {
        failed = true;
        firstFailure = error;
        onFirstFailure(error);
      }
      throw error;
    }
  };
  const settled = await Promise.allSettled([
    observe(first),
    observe(second),
    observe(third),
  ]);
  if (failed) throw firstFailure;
  return [
    (settled[0] as PromiseFulfilledResult<A>).value,
    (settled[1] as PromiseFulfilledResult<B>).value,
    (settled[2] as PromiseFulfilledResult<C>).value,
  ];
}

/**
 * A focus transition must not finish while an old startup can still activate
 * native audio. Close what already exists without publishing an idle state,
 * drain the exact attempt, then close once more before allowing the next audio
 * owner (or an idle UI) to proceed.
 */
export async function closePersonalRealtimeStartup(
  startup: Promise<unknown> | null,
  cleanupTransport: (publishIdle: boolean) => Promise<void>,
  timeoutMs = 2_500,
): Promise<void> {
  let cleanupError: unknown;
  const observeCleanup = async (publishIdle: boolean): Promise<void> => {
    try {
      await cleanupTransport(publishIdle);
    } catch (error) {
      if (cleanupError === undefined) cleanupError = error;
    }
  };

  // Retire this session immediately, while the late continuation remains bound
  // to the same media-session generation captured by cleanupTransport.
  const initialCleanup = observeCleanup(false);
  const startupSettled = startup?.catch(() => undefined) ?? Promise.resolve();
  const finalCleanup = startupSettled.then(() => observeCleanup(true));
  const terminalDrain = Promise.all([initialCleanup, finalCleanup]).then(() => {
    if (cleanupError !== undefined) throw cleanupError;
  });
  try {
    await waitForBoundedNativeOperation(
      terminalDrain,
      timeoutMs,
      'Personal Realtime teardown',
    );
  } catch (error) {
    // Do not cancel the exact-generation finalizer: the native high-water makes
    // it harmless to a replacement even if it completes after this bounded
    // caller has failed visibly and released the coordinator queue.
    void terminalDrain.catch(() => undefined);
    throw error;
  }
}
