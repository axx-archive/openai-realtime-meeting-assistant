export type ReleasableRemoteMediaStream = object & {
  release(releaseTracks?: boolean): void;
};

export type RetirableRemoteVideoEntry = {
  stream: ReleasableRemoteMediaStream;
  track: {
    onmute: unknown;
    onunmute: unknown;
    onended: unknown;
  };
};

/**
 * A one-track MediaStream created for RTCView is registered in the native
 * react-native-webrtc stream table. Retire it only after React has committed a
 * feed list that no longer references it, and never release its remote track.
 */
export function createRemoteStreamRetirementQueue() {
  const pending = new Set<ReleasableRemoteMediaStream>();
  const released = new WeakSet<ReleasableRemoteMediaStream>();

  const retire = (entry: RetirableRemoteVideoEntry): boolean => {
    entry.track.onmute = null;
    entry.track.onunmute = null;
    entry.track.onended = null;
    if (released.has(entry.stream) || pending.has(entry.stream)) return false;
    pending.add(entry.stream);
    return true;
  };

  const flush = (activeStreams: ReadonlySet<ReleasableRemoteMediaStream>): number => {
    let releaseCount = 0;
    pending.forEach((stream) => {
      if (activeStreams.has(stream)) return;
      pending.delete(stream);
      if (released.has(stream)) return;
      released.add(stream);
      // false removes only the wrapper from the native localStreams registry.
      // The remote MediaStreamTrack remains owned by its RTCPeerConnection.
      stream.release(false);
      releaseCount += 1;
    });
    return releaseCount;
  };

  return {
    retire,
    flush,
    flushAll: () => flush(new Set()),
    pendingCount: () => pending.size,
  };
}
