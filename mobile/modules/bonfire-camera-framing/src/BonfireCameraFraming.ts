export type CameraFramingReasonCode =
  | 'native_module_unavailable'
  | 'native_call_failed'
  | 'missing_device_id'
  | 'camera_device_unavailable'
  | 'requires_ios_26'
  | 'front_ultra_wide_unavailable'
  | 'active_camera_not_front_ultra_wide'
  | 'center_stage_unsupported'
  | 'active_format_missing_16x9'
  | 'restore_aspect_ratio_unavailable'
  | 'configuration_lock_failed'
  | 'configuration_failed'
  | 'operation_in_progress'
  | string;

export interface CameraFramingCapabilities {
  platform: string;
  iosVersion: string | null;
  ios26OrNewer: boolean;
  deviceSupported: boolean;
  operational: boolean;
  activeWebRTCCameraAvailable: boolean;
  activeWebRTCCameraAmbiguous: boolean;
  activeDeviceId: string | null;
  activeDevicePosition: 'front' | 'back' | 'unspecified' | null;
  activeDeviceType: string | null;
  frontUltraWideAvailable: boolean;
  centerStageSupported: boolean;
  centerStageActiveFormatSupported: boolean;
  centerStageOperational?: boolean;
  centerStageEnabled: boolean;
  centerStageActive: boolean;
  dynamicAspectRatio16x9HardwareSupported: boolean;
  dynamicAspectRatio16x9ActiveFormatSupported: boolean;
  wideUprightFramingEnabled: boolean;
  dynamicWidth: number;
  dynamicHeight: number;
  reasonCode: CameraFramingReasonCode | null;
  wideUprightReasonCode?: CameraFramingReasonCode | null;
  centerStageReasonCode?: CameraFramingReasonCode | null;
}

export interface CameraFramingOperationResult {
  ok: boolean;
  code: CameraFramingReasonCode;
  message: string;
  capabilities: CameraFramingCapabilities;
}

export interface NativeCameraFramingModule {
  getCapabilities(deviceId: string): Promise<CameraFramingCapabilities>;
  setCenterStageEnabled(enabled: boolean, deviceId: string): Promise<CameraFramingOperationResult>;
  setWideUprightFramingEnabled(enabled: boolean, deviceId: string): Promise<CameraFramingOperationResult>;
}

export interface CameraFramingClient extends NativeCameraFramingModule {}

const unavailableCapabilities = (
  platform: string,
  reasonCode: CameraFramingReasonCode,
): CameraFramingCapabilities => ({
  platform,
  iosVersion: null,
  ios26OrNewer: false,
  deviceSupported: false,
  operational: false,
  activeWebRTCCameraAvailable: false,
  activeWebRTCCameraAmbiguous: false,
  activeDeviceId: null,
  activeDevicePosition: null,
  activeDeviceType: null,
  frontUltraWideAvailable: false,
  centerStageSupported: false,
  centerStageActiveFormatSupported: false,
  centerStageEnabled: false,
  centerStageActive: false,
  dynamicAspectRatio16x9HardwareSupported: false,
  dynamicAspectRatio16x9ActiveFormatSupported: false,
  wideUprightFramingEnabled: false,
  dynamicWidth: 0,
  dynamicHeight: 0,
  reasonCode,
});

const failedResult = (
  platform: string,
  code: CameraFramingReasonCode,
  message: string,
): CameraFramingOperationResult => ({
  ok: false,
  code,
  message,
  capabilities: unavailableCapabilities(platform, code),
});

export function createCameraFramingClient(
  nativeModule: NativeCameraFramingModule | null | undefined,
  platform: string,
): CameraFramingClient {
  return {
    async getCapabilities(deviceId) {
      if (!nativeModule) {
        return unavailableCapabilities(platform, 'native_module_unavailable');
      }
      if (!deviceId) {
        return unavailableCapabilities(platform, 'missing_device_id');
      }
      try {
        return await nativeModule.getCapabilities(deviceId);
      } catch {
        return unavailableCapabilities(platform, 'native_call_failed');
      }
    },

    async setCenterStageEnabled(enabled, deviceId) {
      if (!nativeModule) {
        return failedResult(
          platform,
          'native_module_unavailable',
          'Native camera framing is unavailable in this build.',
        );
      }
      if (!deviceId) {
        return failedResult(platform, 'missing_device_id', 'The current camera identity is unavailable.');
      }
      try {
        return await nativeModule.setCenterStageEnabled(enabled, deviceId);
      } catch {
        return failedResult(
          platform,
          'native_call_failed',
          'Native camera framing failed safely without changing the camera.',
        );
      }
    },

    async setWideUprightFramingEnabled(enabled, deviceId) {
      if (!nativeModule) {
        return failedResult(
          platform,
          'native_module_unavailable',
          'Native camera framing is unavailable in this build.',
        );
      }
      if (!deviceId) {
        return failedResult(platform, 'missing_device_id', 'The current camera identity is unavailable.');
      }
      try {
        return await nativeModule.setWideUprightFramingEnabled(enabled, deviceId);
      } catch {
        return failedResult(
          platform,
          'native_call_failed',
          'Native camera framing failed safely without changing the camera.',
        );
      }
    },
  };
}
