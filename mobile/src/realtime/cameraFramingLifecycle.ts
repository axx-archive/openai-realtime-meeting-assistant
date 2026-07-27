import type { CameraFramingCapabilities } from '../../modules/bonfire-camera-framing';

export type CameraFramingState = {
  checked: boolean;
  checking: boolean;
  applying: boolean;
  pendingControl: 'centerStage' | 'wideUpright' | null;
  wideUprightSupported: boolean;
  wideUprightEnabled: boolean;
  centerStageSupported: boolean;
  centerStageEnabled: boolean;
  centerStageActive: boolean;
  dynamicWidth: number;
  dynamicHeight: number;
  message: string | null;
};

export type CameraFramingTelemetry = {
  activeDeviceType: string | null;
  centerStageSupported: boolean;
  centerStageEnabled: boolean;
  centerStageActive: boolean;
  wideUprightSupported: boolean;
  wideUprightEnabled: boolean;
  dynamicWidth: number;
  dynamicHeight: number;
  reasonCode: string | null;
  wideUprightReasonCode: string | null;
  centerStageReasonCode: string | null;
};

export type CameraFramingTrackIdentity = {
  trackId: string;
  deviceId: string;
};

export type CameraFramingTrack = {
  id: string;
  enabled: boolean;
  kind: string;
  readyState: string;
  getSettings(): { deviceId?: string; facingMode?: string };
};

export type CameraFramingOperation = CameraFramingTrackIdentity & {
  generation: number;
};

export function emptyCameraFramingState(): CameraFramingState {
  return {
    checked: false,
    checking: false,
    applying: false,
    pendingControl: null,
    wideUprightSupported: false,
    wideUprightEnabled: false,
    centerStageSupported: false,
    centerStageEnabled: false,
    centerStageActive: false,
    dynamicWidth: 0,
    dynamicHeight: 0,
    message: null,
  };
}

/** Only capture geometry can require rebuilding the native RTC renderer. */
export function cameraFramingRenderRevision(state: Pick<
  CameraFramingState,
  'wideUprightEnabled' | 'dynamicWidth' | 'dynamicHeight'
>): string {
  return [
    state.wideUprightEnabled ? 'wide' : 'portrait',
    `${state.dynamicWidth}x${state.dynamicHeight}`,
  ].join(':');
}

export function readLiveCameraTrackIdentity(
  track: CameraFramingTrack | null | undefined,
  requireFrontCamera = true,
): CameraFramingTrackIdentity | null {
  if (!track || track.kind !== 'video' || track.readyState !== 'live' || !track.enabled) return null;
  try {
    const settings = track.getSettings();
    const deviceId = String(settings.deviceId ?? '').trim();
    const facingMode = String(settings.facingMode ?? '').trim();
    if (!deviceId || (requireFrontCamera && facingMode !== 'user')) return null;
    return { trackId: track.id, deviceId };
  } catch {
    return null;
  }
}

export function cameraFramingStateFromCapabilities(
  capabilities: CameraFramingCapabilities,
  expectedDeviceId?: string,
): CameraFramingState {
  const exactFrontCameraIsSelected = capabilities.activeWebRTCCameraAvailable
    && !capabilities.activeWebRTCCameraAmbiguous
    && (!expectedDeviceId || capabilities.activeDeviceId === expectedDeviceId)
    && capabilities.activeDevicePosition === 'front';
  const exactAdaptiveFrontCameraIsSelected = exactFrontCameraIsSelected
    && (
      capabilities.activeDeviceType === 'AVCaptureDeviceTypeBuiltInUltraWideCamera'
      || capabilities.activeDeviceType === 'builtInUltraWideCamera'
    );
  const wideUprightSupported = exactAdaptiveFrontCameraIsSelected
    && capabilities.operational
    && capabilities.dynamicAspectRatio16x9HardwareSupported
    && capabilities.dynamicAspectRatio16x9ActiveFormatSupported;
  const legacyExactFrontUltraWide = capabilities.centerStageOperational === undefined
    && (
      capabilities.activeDeviceType === 'AVCaptureDeviceTypeBuiltInUltraWideCamera'
      || capabilities.activeDeviceType === 'builtInUltraWideCamera'
    );
  const centerStageSupported = exactAdaptiveFrontCameraIsSelected
    && (capabilities.centerStageOperational === true || legacyExactFrontUltraWide)
    && capabilities.centerStageSupported
    && capabilities.centerStageActiveFormatSupported;

  return {
    checked: true,
    checking: false,
    applying: false,
    pendingControl: null,
    wideUprightSupported,
    wideUprightEnabled: wideUprightSupported && capabilities.wideUprightFramingEnabled,
    centerStageSupported,
    centerStageEnabled: centerStageSupported && capabilities.centerStageEnabled,
    centerStageActive: centerStageSupported && capabilities.centerStageActive,
    dynamicWidth: wideUprightSupported ? capabilities.dynamicWidth : 0,
    dynamicHeight: wideUprightSupported ? capabilities.dynamicHeight : 0,
    // Routine capability discovery is intentionally silent. Only a failed
    // user-triggered operation should put copy in this field.
    message: null,
  };
}

export function centerStageControlStatus(state: Pick<
  CameraFramingState,
  'pendingControl' | 'centerStageEnabled' | 'centerStageActive'
>): 'Updating…' | 'On' | 'Starting…' | 'Off' {
  if (state.pendingControl === 'centerStage') return 'Updating…';
  if (!state.centerStageEnabled) return 'Off';
  return state.centerStageActive ? 'On' : 'Starting…';
}

// Project native capability evidence onto a deliberately fixed, privacy-safe
// telemetry shape. In particular, neither the WebRTC device ID nor the native
// AVCapture unique ID may leave the device in a media-quality heartbeat.
export function cameraFramingTelemetryFromCapabilities(
  capabilities: CameraFramingCapabilities | null | undefined,
  expectedDeviceId?: string,
): CameraFramingTelemetry | null {
  if (!capabilities) return null;
  const state = cameraFramingStateFromCapabilities(capabilities, expectedDeviceId);
  return {
    activeDeviceType: capabilities.activeDeviceType,
    centerStageSupported: state.centerStageSupported,
    centerStageEnabled: state.centerStageEnabled,
    centerStageActive: state.centerStageActive,
    wideUprightSupported: state.wideUprightSupported,
    wideUprightEnabled: state.wideUprightEnabled,
    dynamicWidth: state.dynamicWidth,
    dynamicHeight: state.dynamicHeight,
    reasonCode: capabilities.reasonCode,
    wideUprightReasonCode: capabilities.wideUprightReasonCode ?? null,
    centerStageReasonCode: capabilities.centerStageReasonCode ?? null,
  };
}

function identityKey(identity: CameraFramingTrackIdentity): string {
  return `${identity.trackId}\u0000${identity.deviceId}`;
}

export function createCameraFramingGenerationGuard() {
  let generation = 0;
  let activeKey: string | null = null;

  return {
    begin(identity: CameraFramingTrackIdentity): CameraFramingOperation {
      generation += 1;
      activeKey = identityKey(identity);
      return { ...identity, generation };
    },

    isCurrent(
      operation: CameraFramingOperation,
      currentIdentity: CameraFramingTrackIdentity | null,
    ): boolean {
      return currentIdentity !== null
        && operation.generation === generation
        && identityKey(operation) === activeKey
        && identityKey(currentIdentity) === activeKey;
    },

    retire(): void {
      generation += 1;
      activeKey = null;
    },
  };
}

export function createCameraFramingOperationQueue() {
  let tail: Promise<void> = Promise.resolve();

  return {
    run<T>(operation: () => Promise<T>): Promise<T> {
      const queued = tail.then(operation, operation);
      tail = queued.then(
        () => undefined,
        () => undefined,
      );
      return queued;
    },
  };
}

export function cooperativeCenterStageIntentAfterRefresh(
  currentIntent: boolean | null,
  supported: boolean,
  enabled: boolean,
): boolean | null {
  if (currentIntent === null) return null;
  return supported ? enabled : null;
}

export function explicitFramingIntentAfterResult(
  requested: boolean,
  operationSucceeded: boolean,
): boolean | null {
  return operationSucceeded ? requested : null;
}

export function wideUprightIntentAfterTransition(
  currentIntent: boolean | null,
  transition: 'call-start' | 'camera-reset' | 'call-end',
): boolean | null {
  // The adaptive iOS 26 front camera needs an explicit dynamic aspect ratio.
  // Prefer its landscape-upright output when the exact active device proves the
  // capability is operational; unsupported cameras simply ignore this intent.
  // This also gives desktop peers a full 16:9 frame without cropping or bars.
  if (transition === 'call-start') return true;
  if (transition === 'call-end') return null;
  return currentIntent;
}

export function wideFramingRestoreDeviceId(
  appliedDeviceId: string | null,
  currentIdentity: CameraFramingTrackIdentity | null,
): string | null {
  return appliedDeviceId ?? currentIdentity?.deviceId ?? null;
}
