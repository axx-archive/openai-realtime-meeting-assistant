export const remoteVideoStallIntervals = 3;
export const remoteVideoIceRestartIntervals = 6;
export const remoteVideoIceRestartCooldownMs = 60_000;
export const remoteVideoMaxIceRestartsPerConnection = 2;

export type RemoteVideoProgressSample = {
  framesDecoded: number;
  bytesReceived: number;
};

export type RemoteVideoProgressState = RemoteVideoProgressSample & {
  stagnantIntervals: number;
  stalled: boolean;
};

export type RemoteVideoRecoveryState = {
  iceRestartCount: number;
  lastIceRestartAt: number | null;
};

type StatRecord = Record<string, unknown>;

function numberValue(value: unknown): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
}

function mediaKind(stat: StatRecord): string {
  return String(stat.kind ?? stat.mediaType ?? '');
}

/** Read the receiver-local inbound counters exposed by react-native-webrtc. */
export function remoteVideoProgressSample(
  report: ReadonlyMap<string, StatRecord>,
): RemoteVideoProgressSample | null {
  let found = false;
  let framesDecoded = 0;
  let bytesReceived = 0;
  report.forEach((raw) => {
    const stat = raw ?? {};
    if (stat.type !== 'inbound-rtp' || mediaKind(stat) !== 'video') return;
    found = true;
    framesDecoded += numberValue(stat.framesDecoded);
    bytesReceived += numberValue(stat.bytesReceived);
  });
  return found ? { framesDecoded, bytesReceived } : null;
}

/**
 * A remote receiver can remain `live` and never emit `mute` after its decoder
 * wedges. Three receiver-local samples without a decoded frame are enough to
 * cover the frozen last frame with the participant placeholder. Authoritative
 * camera-off state disables the watch and rebases it for the next camera-on.
 */
export function nextRemoteVideoProgressState(
  previous: RemoteVideoProgressState | undefined,
  sample: RemoteVideoProgressSample,
  shouldMonitor: boolean,
): { state: RemoteVideoProgressState; becameStalled: boolean; becameHealthy: boolean } {
  if (!shouldMonitor || !previous) {
    return {
      state: { ...sample, stagnantIntervals: 0, stalled: false },
      becameStalled: false,
      becameHealthy: Boolean(previous?.stalled && !shouldMonitor),
    };
  }

  const countersReset = sample.framesDecoded < previous.framesDecoded
    || sample.bytesReceived < previous.bytesReceived;
  const framesAdvanced = sample.framesDecoded > previous.framesDecoded;
  if (countersReset || framesAdvanced) {
    return {
      state: { ...sample, stagnantIntervals: 0, stalled: false },
      becameStalled: false,
      becameHealthy: previous.stalled && framesAdvanced,
    };
  }

  const stagnantIntervals = previous.stagnantIntervals + 1;
  const stalled = stagnantIntervals >= remoteVideoStallIntervals;
  return {
    state: { ...sample, stagnantIntervals, stalled },
    becameStalled: stalled && !previous.stalled,
    becameHealthy: false,
  };
}

/** A fresh peer owns a fresh, transport-wide recovery budget. */
export function createRemoteVideoRecoveryState(): RemoteVideoRecoveryState {
  return { iceRestartCount: 0, lastIceRestartAt: null };
}

/**
 * Decide whether a still-wedged remote subscription needs a bounded ICE
 * restart. The input contains only frame-capable tracks monitored in the
 * current stats pass. Exactly one stalled track may escalate when another
 * track is advancing (proving the transport is otherwise healthy), or when it
 * is the only remote video. This keeps a room-wide congestion event from
 * producing a transport restart storm.
 *
 * The returned state assumes the restart signal was sent. Callers should only
 * commit it after a successful transport-wide signal.
 */
export function nextRemoteVideoRecoveryDecision(
  previous: RemoteVideoRecoveryState,
  monitoredTracks: readonly RemoteVideoProgressState[],
  nowMs: number,
): { state: RemoteVideoRecoveryState; shouldRestartIce: boolean } {
  const stalledTracks = monitoredTracks.filter((track) => track.stalled);
  const persistentStall = stalledTracks.length === 1
    && stalledTracks[0].stagnantIntervals >= remoteVideoIceRestartIntervals;
  const hasAdvancingPeer = monitoredTracks.some((track) => (
    !track.stalled && track.stagnantIntervals === 0
  ));
  const isolatedDeadBinding = persistentStall
    && (monitoredTracks.length === 1 || hasAdvancingPeer);
  const cooldownElapsed = previous.lastIceRestartAt === null
    || nowMs - previous.lastIceRestartAt >= remoteVideoIceRestartCooldownMs;
  const withinBudget = previous.iceRestartCount < remoteVideoMaxIceRestartsPerConnection;

  if (!isolatedDeadBinding || !cooldownElapsed || !withinBudget) {
    return { state: previous, shouldRestartIce: false };
  }

  return {
    state: {
      iceRestartCount: previous.iceRestartCount + 1,
      lastIceRestartAt: nowMs,
    },
    shouldRestartIce: true,
  };
}
