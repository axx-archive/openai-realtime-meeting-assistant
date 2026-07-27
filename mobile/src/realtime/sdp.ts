export type RemoteMediaDirection = 'sendrecv' | 'sendonly' | 'recvonly' | 'inactive';

export type RemoteMediaSection = {
  kind: string;
  direction: RemoteMediaDirection;
  trackId: string;
};

/** Index WebRTC media sections by MID, preserving the offerer's direction. */
export function remoteMediaSections(sdp: string): Map<string, RemoteMediaSection> {
  const sections = new Map<string, RemoteMediaSection>();
  const chunks = String(sdp || '').split(/\r?\nm=/);
  for (let index = 1; index < chunks.length; index += 1) {
    const lines = `m=${chunks[index]}`.split(/\r?\n/);
    const kind = lines[0]?.slice(2).split(/\s+/)[0] ?? '';
    const mid = lines.find((line) => line.startsWith('a=mid:'))?.slice(6) ?? '';
    if (!mid || !kind) continue;
    const direction = (['sendrecv', 'sendonly', 'recvonly', 'inactive'] as const)
      .find((candidate) => lines.includes(`a=${candidate}`)) ?? 'sendrecv';
    const msid = lines.find((line) => line.startsWith('a=msid:'))?.slice(7).trim() ?? '';
    const trackId = msid.split(/\s+/)[1] ?? '';
    sections.set(mid, { kind, direction, trackId });
  }
  return sections;
}

/** A server-recvonly m-line is the single native uplink seam. */
export function isServerUplinkSection(
  section: RemoteMediaSection | undefined,
): section is RemoteMediaSection {
  return section?.direction === 'recvonly';
}

/** Resolve the fixed native publication slot by sender identity, never by the
 * receiver kind. A room can contain several same-kind remote downlinks, whose
 * receiver tracks are deliberately unrelated to the local uplink. */
export function nativeUplinkTransceiverForSender<TTransceiver extends { sender: unknown }>(
  transceivers: readonly TTransceiver[],
  sender: unknown | null | undefined,
): TTransceiver | null {
  if (!sender) return null;
  return transceivers.find((candidate) => candidate.sender === sender) ?? null;
}

/**
 * Return the server's active video downlinks. Null means an active m-line had
 * no msid track identity, so pruning would be unsafe; an empty array is an
 * authoritative offer with no active remote video.
 */
export function offeredRemoteVideoTrackIds(
  sections: ReadonlyMap<string, RemoteMediaSection>,
): string[] | null {
  const downlinks = [...sections.values()].filter((section) => (
    section.kind === 'video' && (section.direction === 'sendonly' || section.direction === 'sendrecv')
  ));
  if (downlinks.some((section) => !section.trackId)) return null;
  return downlinks.map((section) => section.trackId);
}

/**
 * Return any fixed native uplink kinds that the answer failed to negotiate as
 * sendonly. This is a fail-closed check for react-native-webrtc's asynchronous
 * transceiver direction setter before later null-to-track replacement relies on
 * those publication slots.
 */
export function nativeUplinkAnswerDirection(
  kind: 'audio' | 'video',
  microphoneRequested: boolean,
): 'sendonly' | 'inactive' {
  return kind === 'audio' && !microphoneRequested ? 'inactive' : 'sendonly';
}

/**
 * Return any fixed native uplink whose negotiated direction does not match the
 * current publication intent. A quiet join deliberately keeps its audio m-line
 * inactive: negotiating a trackless sendonly audio slot makes WebRTC start the
 * iOS recording AudioUnit even though the UI and roster both say muted.
 */
export function unexpectedNativeUplinkDirectionKinds(
  answerSdp: string,
  uplinkMids: ReadonlyMap<'audio' | 'video', string>,
  microphoneRequested: boolean,
): Array<'audio' | 'video'> {
  const answerSections = remoteMediaSections(answerSdp);
  return (['audio', 'video'] as const).filter((kind) => {
    const mid = uplinkMids.get(kind);
    const section = mid ? answerSections.get(mid) : undefined;
    return !section
      || section.kind !== kind
      || section.direction !== nativeUplinkAnswerDirection(kind, microphoneRequested);
  });
}

function mediaSectionLinesForMid(sdp: string, mid: string): string[] | null {
  const chunks = String(sdp || '').split(/\r?\nm=/);
  for (let index = 1; index < chunks.length; index += 1) {
    const lines = `m=${chunks[index]}`.split(/\r?\n/);
    if (lines.includes(`a=mid:${mid}`)) return lines;
  }
  return null;
}

/**
 * Validate the exact native camera uplink after createAnswer. Only H.264
 * primaries and RTX entries that repair one of those primaries may remain.
 */
export function nativeVideoUplinkCodecViolation(answerSdp: string, videoMid: string): string | null {
  const lines = mediaSectionLinesForMid(answerSdp, videoMid);
  if (!lines) return 'video uplink section is missing';
  const media = lines[0]?.trim().split(/\s+/) ?? [];
  if (media[0] !== 'm=video') return 'uplink MID is not video';
  const payloadTypes = media.slice(3).filter(Boolean);
  if (payloadTypes.length === 0) return 'video uplink has no codecs';

  const codecByPayload = new Map<string, string>();
  const fmtpByPayload = new Map<string, string>();
  lines.forEach((line) => {
    const rtpMap = /^a=rtpmap:(\d+)\s+([^/\s]+)/i.exec(line);
    if (rtpMap) codecByPayload.set(rtpMap[1], rtpMap[2].toLowerCase());
    const fmtp = /^a=fmtp:(\d+)\s+(.+)$/i.exec(line);
    if (fmtp) fmtpByPayload.set(fmtp[1], fmtp[2]);
  });

  const h264Payloads = new Set(
    payloadTypes.filter((payload) => codecByPayload.get(payload) === 'h264'),
  );
  if (h264Payloads.size === 0) return 'video uplink did not negotiate H.264';

  for (const payload of payloadTypes) {
    const codec = codecByPayload.get(payload);
    if (codec === 'h264') {
      if (!/(?:^|;)\s*packetization-mode=1(?:;|$)/i.test(fmtpByPayload.get(payload) ?? '')) {
        return `H.264 payload ${payload} is not packetization-mode 1`;
      }
      continue;
    }
    if (codec === 'rtx') {
      const apt = /(?:^|;)\s*apt=(\d+)(?:;|$)/i.exec(fmtpByPayload.get(payload) ?? '')?.[1] ?? '';
      if (!h264Payloads.has(apt)) return `RTX payload ${payload} does not repair H.264`;
      continue;
    }
    return `unexpected native video codec ${codec || `payload ${payload}`}`;
  }
  return null;
}
