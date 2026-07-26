export type NativeVideoCodecCapability = {
  mimeType: string;
  payloadType?: number;
  preferredPayloadType?: number;
  sdpFmtpLine?: string;
};

export class NativeH264UnavailableError extends Error {
  constructor() {
    super('This iPhone did not expose the H.264 camera encoder required for a reliable call.');
    this.name = 'NativeH264UnavailableError';
  }
}

function normalizedMimeType(codec: NativeVideoCodecCapability): string {
  return String(codec.mimeType ?? '').trim().toLowerCase();
}

function payloadType(codec: NativeVideoCodecCapability): number | null {
  const raw = codec.payloadType ?? codec.preferredPayloadType;
  const value = Number(raw);
  return Number.isInteger(value) && value >= 0 && value <= 127 ? value : null;
}

function fmtpParameter(codec: NativeVideoCodecCapability, name: string): string {
  const target = name.toLowerCase();
  for (const part of String(codec.sdpFmtpLine ?? '').split(';')) {
    const [rawName, ...rawValue] = part.trim().split('=');
    if (rawName?.trim().toLowerCase() === target) return rawValue.join('=').trim();
  }
  return '';
}

function h264PreferenceRank(codec: NativeVideoCodecCapability): number {
  const profile = fmtpParameter(codec, 'profile-level-id').toLowerCase();
  // Constrained Baseline is the server's interoperability envelope. Preserve
  // the installed capability order within each rank.
  if (profile.startsWith('42e0')) return 0;
  if (profile.startsWith('4200')) return 1;
  return 2;
}

/**
 * Select a camera-uplink-only H.264 envelope. VP8 is deliberately excluded:
 * the M124 iOS VP8 encoder produced valid initial media but did not produce a
 * usable late-subscriber recovery frame after repeated upstream PLIs.
 */
export function nativeH264UplinkCodecPreferences<T extends NativeVideoCodecCapability>(
  codecs: readonly T[],
): T[] {
  const h264 = codecs
    .map((codec, index) => ({ codec, index }))
    .filter(({ codec }) => (
      normalizedMimeType(codec) === 'video/h264'
      && fmtpParameter(codec, 'packetization-mode') === '1'
    ))
    .sort((left, right) => (
      h264PreferenceRank(left.codec) - h264PreferenceRank(right.codec)
      || left.index - right.index
    ))
    .map(({ codec }) => codec);

  if (h264.length === 0) throw new NativeH264UnavailableError();

  const h264PayloadTypes = new Set(
    h264.map(payloadType).filter((value): value is number => value !== null),
  );
  const matchingRtx = codecs.filter((codec) => {
    if (normalizedMimeType(codec) !== 'video/rtx') return false;
    const apt = Number(fmtpParameter(codec, 'apt'));
    return Number.isInteger(apt) && h264PayloadTypes.has(apt);
  });

  return [...h264, ...matchingRtx];
}
