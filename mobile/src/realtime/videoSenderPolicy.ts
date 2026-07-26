import type { RTCRtpSendParameters } from 'react-native-webrtc';

type MutableNativeVideoEncoding = {
  maxBitrate: number | null;
  minBitrate: number | null;
  maxFramerate: number | null;
  scaleResolutionDownBy: number | null;
};

// A square 1280x1280 iOS camera frame carries about 78% more pixels than
// 720p. The previous 1.2 Mbps ceiling visibly starved that stream even when
// WebRTC reported several Mbps available. This remains a maximum, not a
// forced rate: congestion control can and will send less on a constrained
// path to protect latency.
export const nativeCameraMaxBitrate = 2_500_000;

export function applyNativeCameraSenderPolicy(parameters: RTCRtpSendParameters): void {
  parameters.encodings.forEach((rawEncoding) => {
    const encoding = rawEncoding as unknown as MutableNativeVideoEncoding;
    encoding.maxBitrate = nativeCameraMaxBitrate;
    encoding.minBitrate = null;
    encoding.maxFramerate = 30;
    encoding.scaleResolutionDownBy = 1;
  });
  parameters.degradationPreference = 'maintain-framerate';
}
