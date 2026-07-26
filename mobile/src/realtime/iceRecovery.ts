export const disconnectedIceRestartGraceMs = 3_000;

export type RecoverableIcePeer = {
  connectionState: string;
  restartIce(): void;
};

type TimerHandle = unknown;

type TimerScheduler = {
  schedule(callback: () => void, delayMs: number): TimerHandle;
  cancel(handle: TimerHandle): void;
};

type DisconnectedIceRestartControllerOptions = {
  graceMs?: number;
  timer?: TimerScheduler;
};

const nativeTimer: TimerScheduler = {
  schedule: (callback, delayMs) => setTimeout(callback, delayMs),
  cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

/**
 * Defers ICE repair for transient disconnects while keeping failed peers on the
 * immediate recovery path. A scheduled repair belongs to one exact peer, so a
 * replacement connection can never receive a stale restart.
 */
export function createDisconnectedIceRestartController(
  options: DisconnectedIceRestartControllerOptions = {},
) {
  const graceMs = options.graceMs ?? disconnectedIceRestartGraceMs;
  const timer = options.timer ?? nativeTimer;
  let pending: { handle: TimerHandle; peer: RecoverableIcePeer } | null = null;

  const cancel = () => {
    if (!pending) return;
    timer.cancel(pending.handle);
    pending = null;
  };

  const cancelForPeer = (peer: RecoverableIcePeer) => {
    if (pending?.peer === peer) cancel();
  };

  const handleConnectionStateChange = (
    peer: RecoverableIcePeer,
    currentPeer: () => RecoverableIcePeer | null,
    signalRestart: () => void,
  ) => {
    if (currentPeer() !== peer) {
      cancelForPeer(peer);
      return;
    }

    if (peer.connectionState === 'failed') {
      cancel();
      peer.restartIce();
      signalRestart();
      return;
    }

    if (peer.connectionState !== 'disconnected') {
      cancelForPeer(peer);
      return;
    }

    if (pending?.peer === peer) return;
    cancel();
    const handle = timer.schedule(() => {
      if (pending?.handle !== handle) return;
      pending = null;
      if (currentPeer() !== peer || peer.connectionState !== 'disconnected') return;
      peer.restartIce();
      signalRestart();
    }, graceMs);
    pending = { handle, peer };
  };

  return { cancel, handleConnectionStateChange };
}
