export type ScreenShareTrack = {
  id: string;
  kind: string;
  enabled: boolean;
  readyState?: string;
  stop: () => void;
  release?: () => void;
};

export type ScreenShareSender<TTrack extends ScreenShareTrack = ScreenShareTrack> = {
  readonly track: TTrack | null;
  replaceTrack: (track: TTrack | null) => Promise<void>;
  getStats: () => Promise<Map<string, Record<string, unknown>>>;
};

export type ScreenShareProgress = Readonly<{
  bytesSent: number;
  framesSent: number;
}>;

export type SerializedVideoSenderMutations = {
  run: <T>(sender: object, mutation: () => Promise<T>) => Promise<T>;
};

/**
 * One video transceiver is shared by the camera and ReplayKit. Serialize every
 * replace/configure sequence so a stale camera rollback, reconnect, or stop
 * cannot overwrite a newer screen-share intent on that sender.
 */
export function createSerializedVideoSenderMutations(): SerializedVideoSenderMutations {
  const tails = new WeakMap<object, Promise<void>>();
  return {
    run<T>(sender: object, mutation: () => Promise<T>): Promise<T> {
      const tail = tails.get(sender) ?? Promise.resolve();
      const result = tail.then(mutation);
      const nextTail = result.then(() => undefined, () => undefined);
      tails.set(sender, nextTail);
      void nextTail.then(() => {
        if (tails.get(sender) === nextTail) tails.delete(sender);
      });
      return result;
    },
  };
}

function finiteNumber(value: unknown): number {
  const number = Number(value);
  return Number.isFinite(number) && number >= 0 ? number : 0;
}

/** Pulls monotonic outbound-video counters from a sender-scoped stats report. */
export function screenShareProgress(
  report: ReadonlyMap<string, Record<string, unknown>>,
): ScreenShareProgress {
  let bytesSent = 0;
  let framesSent = 0;
  report.forEach((entry) => {
    if (entry.type !== 'outbound-rtp' || entry.isRemote === true) return;
    const kind = String(entry.kind ?? entry.mediaType ?? '').toLowerCase();
    if (kind && kind !== 'video') return;
    bytesSent = Math.max(bytesSent, finiteNumber(entry.bytesSent));
    framesSent = Math.max(framesSent, finiteNumber(entry.framesSent ?? entry.framesEncoded));
  });
  return { bytesSent, framesSent };
}

export function screenShareMadeProgress(
  baseline: ScreenShareProgress,
  current: ScreenShareProgress,
): boolean {
  return current.bytesSent > baseline.bytesSent && current.framesSent > baseline.framesSent;
}

/** Prevents an awaited stop from publishing into a later leave/rejoin session. */
export function screenShareStopIsCurrent(
  stopOperation: number,
  currentOperation: number,
  logicalSession: unknown,
  currentSession: unknown,
): boolean {
  return stopOperation === currentOperation && logicalSession === currentSession;
}

/** A duplicate stop must not steal ownership from the in-flight stop. */
export function screenShareStopShouldBegin(
  requested: boolean,
  announced: boolean,
  hasDisplayStream: boolean,
): boolean {
  return requested || announced || hasDisplayStream;
}

/**
 * react-native-webrtc resolves replaceTrack even when native replacement fails.
 * Always verify the sender getter, and roll back if this operation became stale.
 */
export async function installScreenShareTrack<TTrack extends ScreenShareTrack>(options: {
  sender: ScreenShareSender<TTrack>;
  track: TTrack;
  isCurrent: () => boolean;
}): Promise<{ outcome: 'installed' | 'cancelled'; previousTrack: TTrack | null }> {
  const { sender, track, isCurrent } = options;
  const previousTrack = sender.track;
  if (!isCurrent()) return { outcome: 'cancelled', previousTrack };

  track.enabled = true;
  await sender.replaceTrack(track);
  if (sender.track !== track) {
    throw new Error('The screen-share track could not be attached.');
  }
  if (isCurrent()) return { outcome: 'installed', previousTrack };

  const rollbackTrack = previousTrack?.readyState === 'live' ? previousTrack : null;
  await sender.replaceTrack(rollbackTrack);
  if (sender.track !== rollbackTrack) {
    throw new Error('The previous video track could not be restored after screen-share cancellation.');
  }
  return { outcome: 'cancelled', previousTrack };
}

/** Restores the camera/null slot before the ReplayKit stream is released. */
export async function restoreAfterScreenShare<TTrack extends ScreenShareTrack>(options: {
  sender: ScreenShareSender<TTrack>;
  screenTrack: TTrack;
  restoreTrack: TTrack | null;
}): Promise<void> {
  const { sender, screenTrack, restoreTrack } = options;
  if (sender.track !== screenTrack) return;
  await sender.replaceTrack(restoreTrack);
  if (sender.track !== restoreTrack) {
    throw new Error('The camera could not be restored after screen sharing.');
  }
}
