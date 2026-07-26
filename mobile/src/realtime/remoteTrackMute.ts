export const remoteVideoMuteGraceMs = 3_000;

export type MutableRemoteVideoTrack = {
  muted: boolean;
  readyState: string;
};

type TimerHandle = unknown;

type TimerScheduler = {
  schedule(callback: () => void, delayMs: number): TimerHandle;
  cancel(handle: TimerHandle): void;
};

type RemoteVideoMuteControllerOptions = {
  graceMs?: number;
  timer?: TimerScheduler;
};

const nativeTimer: TimerScheduler = {
  schedule: (callback, delayMs) => setTimeout(callback, delayMs),
  cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

/** Debounce remote mute blips while providing a deterministic frozen-frame cutoff. */
export function createRemoteVideoMuteController(options: RemoteVideoMuteControllerOptions = {}) {
  const graceMs = options.graceMs ?? remoteVideoMuteGraceMs;
  const timer = options.timer ?? nativeTimer;
  const pending = new Map<MutableRemoteVideoTrack, TimerHandle>();

  const cancel = (track: MutableRemoteVideoTrack) => {
    const handle = pending.get(track);
    if (handle === undefined) return;
    timer.cancel(handle);
    pending.delete(track);
  };

  const cancelAll = () => {
    [...pending.keys()].forEach(cancel);
  };

  const handleMute = (
    track: MutableRemoteVideoTrack,
    isCurrentTrack: () => boolean,
    removeFrozenFeed: () => void,
  ) => {
    if (!isCurrentTrack() || pending.has(track)) return;
    const handle = timer.schedule(() => {
      if (pending.get(track) !== handle) return;
      pending.delete(track);
      if (!isCurrentTrack() || !track.muted || track.readyState === 'ended') return;
      removeFrozenFeed();
    }, graceMs);
    pending.set(track, handle);
  };

  const handleUnmute = (
    track: MutableRemoteVideoTrack,
    isCurrentTrack: () => boolean,
    restoreFeed: () => void,
  ) => {
    cancel(track);
    if (!isCurrentTrack() || track.muted || track.readyState === 'ended') return;
    restoreFeed();
  };

  return { cancel, cancelAll, handleMute, handleUnmute };
}
