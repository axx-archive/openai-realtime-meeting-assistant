export type NativeRoomQuality = {
  label: 'Live' | 'Catching up' | 'Connection weak';
  receivedFramesPerSecond: number;
  jitterBufferMs: number;
  packetLossPercent: number;
  roundTripTimeMs: number;
};

export type NativeRoomStatsSnapshot = NativeRoomQuality & {
  at: number;
  inboundVideoPacketsReceived: number;
  inboundVideoPacketsLost: number;
  inboundVideoFramesDecoded: number;
  inboundVideoFramesDropped: number;
  inboundVideoJitter: number;
  inboundVideoJitterBufferDelay: number;
  inboundVideoJitterBufferEmittedCount: number;
  inboundAudioPacketsReceived: number;
  inboundAudioPacketsLost: number;
  inboundAudioJitterBufferDelay: number;
  inboundAudioJitterBufferEmittedCount: number;
  outboundVideoBytesSent: number;
  outboundVideoBytesDelta: number;
  outboundVideoFramesEncoded: number;
  outboundVideoFramesSent: number;
  outboundVideoFramesSentDelta: number;
  outboundVideoFrameWidth: number;
  outboundVideoFrameHeight: number;
  outboundVideoFramesPerSecond: number;
  outboundVideoTargetBitrate: number;
  outboundVideoQualityLimitationReason: string;
  availableOutgoingBitrate: number;
  candidatePair: {
    protocol: string;
    networkType: string;
    localCandidateType: string;
    remoteCandidateType: string;
    availableOutgoingBitrate: number;
    currentRoundTripTime: number;
  } | null;
};

type StatRecord = Record<string, unknown>;

function numberValue(value: unknown): number {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
}

function mediaKind(stat: StatRecord): string {
  return String(stat.kind ?? stat.mediaType ?? '');
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function selectedCandidatePair(report: ReadonlyMap<string, StatRecord>): StatRecord | null {
  let selectedPairId = '';
  let selectedFallback: StatRecord | null = null;
  let nominatedFallback: StatRecord | null = null;

  report.forEach((raw) => {
    const stat = raw ?? {};
    if (stat.type === 'transport' && typeof stat.selectedCandidatePairId === 'string') {
      selectedPairId ||= stat.selectedCandidatePairId;
      return;
    }
    if (stat.type !== 'candidate-pair') return;
    if (!selectedFallback && stat.selected === true) selectedFallback = stat;
    if (!nominatedFallback && stat.nominated === true && stat.state === 'succeeded') nominatedFallback = stat;
  });

  if (selectedPairId) {
    const transportPair = report.get(selectedPairId);
    if (transportPair?.type === 'candidate-pair') return transportPair;
  }
  return selectedFallback ?? nominatedFallback;
}

/** Keep camera recovery tied to active, established outbound media intervals. */
export function nextZeroOutboundVideoIntervalCount(
  current: number,
  shouldMonitor: boolean,
  hasPreviousSample: boolean,
  outboundVideoBytesDelta: number,
): number {
  if (!shouldMonitor || !hasPreviousSample || outboundVideoBytesDelta > 0) return 0;
  return current + 1;
}

/** Summarize the cross-platform subset exposed by react-native-webrtc. */
export function summarizeNativeRoomStats(
  report: ReadonlyMap<string, StatRecord>,
  previous: NativeRoomStatsSnapshot | null,
  at = Date.now(),
): NativeRoomStatsSnapshot {
  const totals = {
    inboundVideoPacketsReceived: 0,
    inboundVideoPacketsLost: 0,
    inboundVideoFramesDecoded: 0,
    inboundVideoFramesDropped: 0,
    inboundVideoJitter: 0,
    inboundVideoJitterBufferDelay: 0,
    inboundVideoJitterBufferEmittedCount: 0,
    inboundAudioPacketsReceived: 0,
    inboundAudioPacketsLost: 0,
    inboundAudioJitterBufferDelay: 0,
    inboundAudioJitterBufferEmittedCount: 0,
    outboundVideoBytesSent: 0,
    outboundVideoFramesEncoded: 0,
    outboundVideoFramesSent: 0,
    outboundVideoFrameWidth: 0,
    outboundVideoFrameHeight: 0,
    outboundVideoFramesPerSecond: 0,
    outboundVideoTargetBitrate: 0,
    outboundVideoQualityLimitationReason: '',
    roundTripTimeMs: 0,
  };

  report.forEach((raw) => {
    const stat = raw ?? {};
    if (stat.type === 'inbound-rtp' && mediaKind(stat) === 'video') {
      totals.inboundVideoPacketsReceived += numberValue(stat.packetsReceived);
      totals.inboundVideoPacketsLost += numberValue(stat.packetsLost);
      totals.inboundVideoFramesDecoded += numberValue(stat.framesDecoded);
      totals.inboundVideoFramesDropped += numberValue(stat.framesDropped);
      totals.inboundVideoJitter = Math.max(totals.inboundVideoJitter, numberValue(stat.jitter));
      totals.inboundVideoJitterBufferDelay += numberValue(stat.jitterBufferDelay);
      totals.inboundVideoJitterBufferEmittedCount += numberValue(stat.jitterBufferEmittedCount);
    } else if (stat.type === 'inbound-rtp' && mediaKind(stat) === 'audio') {
      totals.inboundAudioPacketsReceived += numberValue(stat.packetsReceived);
      totals.inboundAudioPacketsLost += numberValue(stat.packetsLost);
      totals.inboundAudioJitterBufferDelay += numberValue(stat.jitterBufferDelay);
      totals.inboundAudioJitterBufferEmittedCount += numberValue(stat.jitterBufferEmittedCount);
    } else if (stat.type === 'outbound-rtp' && mediaKind(stat) === 'video') {
      totals.outboundVideoBytesSent += numberValue(stat.bytesSent);
      totals.outboundVideoFramesEncoded += numberValue(stat.framesEncoded);
      totals.outboundVideoFramesSent += numberValue(stat.framesSent);
      totals.outboundVideoFrameWidth = Math.max(totals.outboundVideoFrameWidth, numberValue(stat.frameWidth));
      totals.outboundVideoFrameHeight = Math.max(totals.outboundVideoFrameHeight, numberValue(stat.frameHeight));
      totals.outboundVideoFramesPerSecond += numberValue(stat.framesPerSecond);
      totals.outboundVideoTargetBitrate += numberValue(stat.targetBitrate);
      const qualityLimitationReason = stringValue(stat.qualityLimitationReason);
      if (qualityLimitationReason && qualityLimitationReason !== 'none') {
        totals.outboundVideoQualityLimitationReason = qualityLimitationReason;
      } else if (!totals.outboundVideoQualityLimitationReason) {
        totals.outboundVideoQualityLimitationReason = qualityLimitationReason;
      }
    }
  });

  const pair = selectedCandidatePair(report);
  const localCandidate = pair?.localCandidateId && typeof pair.localCandidateId === 'string'
    ? report.get(pair.localCandidateId)
    : null;
  const remoteCandidate = pair?.remoteCandidateId && typeof pair.remoteCandidateId === 'string'
    ? report.get(pair.remoteCandidateId)
    : null;
  const candidatePair = pair ? {
    protocol: stringValue(pair.protocol) || stringValue(localCandidate?.protocol),
    networkType: stringValue(localCandidate?.networkType),
    localCandidateType: stringValue(localCandidate?.candidateType),
    remoteCandidateType: stringValue(remoteCandidate?.candidateType),
    availableOutgoingBitrate: numberValue(pair.availableOutgoingBitrate),
    currentRoundTripTime: numberValue(pair.currentRoundTripTime),
  } : null;
  totals.roundTripTimeMs = (candidatePair?.currentRoundTripTime ?? 0) * 1000;

  const elapsedSeconds = previous ? Math.max(0.001, (at - previous.at) / 1000) : 0;
  const decodedDelta = previous ? Math.max(0, totals.inboundVideoFramesDecoded - previous.inboundVideoFramesDecoded) : 0;
  const receivedDelta = previous ? Math.max(0, totals.inboundVideoPacketsReceived - previous.inboundVideoPacketsReceived) : 0;
  const lostDelta = previous ? Math.max(0, totals.inboundVideoPacketsLost - previous.inboundVideoPacketsLost) : 0;
  const videoPacketLossPercent = previous && receivedDelta + lostDelta > 0
    ? (lostDelta / (receivedDelta + lostDelta)) * 100
    : 0;
  const audioReceivedDelta = previous ? Math.max(0, totals.inboundAudioPacketsReceived - previous.inboundAudioPacketsReceived) : 0;
  const audioLostDelta = previous ? Math.max(0, totals.inboundAudioPacketsLost - previous.inboundAudioPacketsLost) : 0;
  const audioPacketLossPercent = previous && audioReceivedDelta + audioLostDelta > 0
    ? (audioLostDelta / (audioReceivedDelta + audioLostDelta)) * 100
    : 0;
  // A busy healthy video stream must not dilute impaired audio, including in
  // audio-only rooms. Idle/silent streams alone do not imply packet loss.
  const packetLossPercent = Math.max(videoPacketLossPercent, audioPacketLossPercent);
  const jitterBufferDelayDelta = previous
    ? Math.max(0, totals.inboundVideoJitterBufferDelay - previous.inboundVideoJitterBufferDelay)
    : 0;
  const jitterBufferEmittedDelta = previous
    ? Math.max(0, totals.inboundVideoJitterBufferEmittedCount - previous.inboundVideoJitterBufferEmittedCount)
    : 0;
  const videoJitterBufferMs = jitterBufferEmittedDelta > 0
    ? (jitterBufferDelayDelta / jitterBufferEmittedDelta) * 1000
    : 0;
  const audioJitterBufferDelayDelta = previous
    ? Math.max(0, totals.inboundAudioJitterBufferDelay - previous.inboundAudioJitterBufferDelay)
    : 0;
  const audioJitterBufferEmittedDelta = previous
    ? Math.max(0, totals.inboundAudioJitterBufferEmittedCount - previous.inboundAudioJitterBufferEmittedCount)
    : 0;
  const audioJitterBufferMs = audioJitterBufferEmittedDelta > 0
    ? (audioJitterBufferDelayDelta / audioJitterBufferEmittedDelta) * 1000
    : 0;
  const jitterBufferMs = Math.max(videoJitterBufferMs, audioJitterBufferMs);
  const receivedFramesPerSecond = previous ? decodedDelta / elapsedSeconds : 0;
  const outboundVideoBytesDelta = previous
    ? Math.max(0, totals.outboundVideoBytesSent - previous.outboundVideoBytesSent)
    : 0;
  const outboundVideoFramesSentDelta = previous
    ? Math.max(0, totals.outboundVideoFramesSent - previous.outboundVideoFramesSent)
    : 0;
  const label = packetLossPercent >= 8 || jitterBufferMs >= 700 || totals.roundTripTimeMs >= 900
    ? 'Connection weak'
    : packetLossPercent >= 2 || jitterBufferMs >= 300 || totals.roundTripTimeMs >= 450
      ? 'Catching up'
      : 'Live';

  return {
    ...totals,
    at,
    label,
    receivedFramesPerSecond,
    jitterBufferMs,
    packetLossPercent,
    outboundVideoBytesDelta,
    outboundVideoFramesSentDelta,
    availableOutgoingBitrate: candidatePair?.availableOutgoingBitrate ?? 0,
    candidatePair,
  };
}
