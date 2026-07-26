import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import type { CameraFramingCapabilities } from '../../modules/bonfire-camera-framing';
import {
  cameraFramingStateFromCapabilities,
  centerStageControlStatus,
  cameraFramingTelemetryFromCapabilities,
  createCameraFramingGenerationGuard,
  createCameraFramingOperationQueue,
  cooperativeCenterStageIntentAfterRefresh,
  explicitFramingIntentAfterResult,
  readLiveCameraTrackIdentity,
  wideFramingRestoreDeviceId,
  wideUprightIntentAfterTransition,
} from '../realtime/cameraFramingLifecycle';

const supportedCapabilities: CameraFramingCapabilities = {
  platform: 'ios',
  iosVersion: '26.0',
  ios26OrNewer: true,
  deviceSupported: true,
  operational: true,
  activeWebRTCCameraAvailable: true,
  activeWebRTCCameraAmbiguous: false,
  activeDeviceId: 'front-camera',
  activeDevicePosition: 'front',
  activeDeviceType: 'builtInUltraWideCamera',
  frontUltraWideAvailable: true,
  centerStageSupported: true,
  centerStageActiveFormatSupported: true,
  centerStageEnabled: true,
  centerStageActive: true,
  dynamicAspectRatio16x9HardwareSupported: true,
  dynamicAspectRatio16x9ActiveFormatSupported: true,
  wideUprightFramingEnabled: true,
  dynamicWidth: 1920,
  dynamicHeight: 1080,
  reasonCode: null,
};

describe('camera framing lifecycle', () => {
  it('binds async work to both the live track and exact native device', () => {
    const guard = createCameraFramingGenerationGuard();
    const firstIdentity = { trackId: 'track-1', deviceId: 'front-camera' };
    const first = guard.begin(firstIdentity);

    assert.equal(guard.isCurrent(first, firstIdentity), true);
    assert.equal(guard.isCurrent(first, { ...firstIdentity, deviceId: 'rear-camera' }), false);

    const replacement = guard.begin({ trackId: 'track-2', deviceId: 'front-camera' });
    assert.equal(guard.isCurrent(first, firstIdentity), false);
    assert.equal(guard.isCurrent(replacement, { trackId: 'track-2', deviceId: 'front-camera' }), true);

    guard.retire();
    assert.equal(guard.isCurrent(replacement, { trackId: 'track-2', deviceId: 'front-camera' }), false);
  });

  it('fails closed when track identity is missing, disabled, ended, or rear-facing', () => {
    const track = {
      id: 'track-1',
      enabled: true,
      kind: 'video',
      readyState: 'live',
      getSettings: () => ({ deviceId: 'front-camera', facingMode: 'user' }),
    };

    assert.deepEqual(readLiveCameraTrackIdentity(track), {
      trackId: 'track-1',
      deviceId: 'front-camera',
    });
    assert.equal(readLiveCameraTrackIdentity({ ...track, enabled: false }), null);
    assert.equal(readLiveCameraTrackIdentity({ ...track, readyState: 'ended' }), null);
    assert.equal(readLiveCameraTrackIdentity({
      ...track,
      getSettings: () => ({ deviceId: 'rear-camera', facingMode: 'environment' }),
    }), null);
    assert.equal(readLiveCameraTrackIdentity({
      ...track,
      getSettings: () => ({ facingMode: 'user' }),
    }), null);
  });

  it('exposes framing only for an exact operational front camera and keeps refresh silent', () => {
    assert.deepEqual(cameraFramingStateFromCapabilities(supportedCapabilities, 'front-camera'), {
      checked: true,
      checking: false,
      applying: false,
      pendingControl: null,
      wideUprightSupported: true,
      wideUprightEnabled: true,
      centerStageSupported: true,
      centerStageEnabled: true,
      centerStageActive: true,
      dynamicWidth: 1920,
      dynamicHeight: 1080,
      message: null,
    });

    const ambiguous = cameraFramingStateFromCapabilities({
      ...supportedCapabilities,
      activeWebRTCCameraAmbiguous: true,
      reasonCode: 'camera_device_unavailable',
    });
    assert.equal(ambiguous.wideUprightSupported, false);
    assert.equal(ambiguous.wideUprightEnabled, false);
    assert.equal(ambiguous.centerStageSupported, false);
    assert.equal(ambiguous.centerStageEnabled, false);
    assert.equal(ambiguous.message, null);

    const wrongExactDevice = cameraFramingStateFromCapabilities(
      supportedCapabilities,
      'different-front-camera',
    );
    assert.equal(wrongExactDevice.wideUprightSupported, false);
    assert.equal(wrongExactDevice.centerStageSupported, false);

    const centerStageWithoutDynamicAspectRatio = cameraFramingStateFromCapabilities({
      ...supportedCapabilities,
      operational: false,
      dynamicAspectRatio16x9HardwareSupported: false,
      dynamicAspectRatio16x9ActiveFormatSupported: false,
      wideUprightFramingEnabled: false,
      reasonCode: 'requires_ios_26',
    }, 'front-camera');
    assert.equal(centerStageWithoutDynamicAspectRatio.wideUprightSupported, false);
    assert.equal(centerStageWithoutDynamicAspectRatio.centerStageSupported, true);

    const wideCameraWithGlobalCenterStageOn = cameraFramingStateFromCapabilities({
      ...supportedCapabilities,
      activeDeviceType: 'AVCaptureDeviceTypeBuiltInWideAngleCamera',
      centerStageOperational: false,
      centerStageEnabled: true,
      centerStageActive: false,
    }, 'front-camera');
    assert.equal(wideCameraWithGlobalCenterStageOn.wideUprightSupported, false);
    assert.equal(wideCameraWithGlobalCenterStageOn.wideUprightEnabled, false);
    assert.equal(wideCameraWithGlobalCenterStageOn.centerStageSupported, false);
    assert.equal(wideCameraWithGlobalCenterStageOn.centerStageEnabled, false);
    assert.equal(wideCameraWithGlobalCenterStageOn.dynamicWidth, 0);
    assert.equal(wideCameraWithGlobalCenterStageOn.dynamicHeight, 0);

    const reportedNonOperationalUltraWide = cameraFramingStateFromCapabilities({
      ...supportedCapabilities,
      centerStageOperational: false,
    }, 'front-camera');
    assert.equal(reportedNonOperationalUltraWide.centerStageSupported, false);
    assert.equal(reportedNonOperationalUltraWide.centerStageEnabled, false);

    const legacyWideCameraPayload = cameraFramingStateFromCapabilities({
      ...supportedCapabilities,
      activeDeviceType: 'AVCaptureDeviceTypeBuiltInWideAngleCamera',
      centerStageOperational: undefined,
    }, 'front-camera');
    assert.equal(legacyWideCameraPayload.centerStageSupported, false);
  });

  it('labels Center Stage on only after the active device confirms it is active', () => {
    assert.equal(centerStageControlStatus({
      pendingControl: 'centerStage',
      centerStageEnabled: false,
      centerStageActive: false,
    }), 'Updating…');
    assert.equal(centerStageControlStatus({
      pendingControl: null,
      centerStageEnabled: true,
      centerStageActive: false,
    }), 'Starting…');
    assert.equal(centerStageControlStatus({
      pendingControl: null,
      centerStageEnabled: true,
      centerStageActive: true,
    }), 'On');
    assert.equal(centerStageControlStatus({
      pendingControl: null,
      centerStageEnabled: false,
      centerStageActive: false,
    }), 'Off');
  });

  it('projects only privacy-safe framing evidence into media telemetry', () => {
    const telemetry = cameraFramingTelemetryFromCapabilities({
      ...supportedCapabilities,
      reasonCode: 'active_camera_not_front_ultra_wide',
      wideUprightReasonCode: 'active_format_missing_16x9',
      centerStageReasonCode: 'center_stage_unsupported',
    }, 'front-camera');

    assert.deepEqual(telemetry, {
      activeDeviceType: 'builtInUltraWideCamera',
      centerStageSupported: true,
      centerStageEnabled: true,
      centerStageActive: true,
      wideUprightSupported: true,
      wideUprightEnabled: true,
      dynamicWidth: 1920,
      dynamicHeight: 1080,
      reasonCode: 'active_camera_not_front_ultra_wide',
      wideUprightReasonCode: 'active_format_missing_16x9',
      centerStageReasonCode: 'center_stage_unsupported',
    });
    assert.equal(JSON.stringify(telemetry).includes('front-camera'), false);
    assert.deepEqual(Object.keys(telemetry ?? {}).sort(), [
      'activeDeviceType',
      'centerStageActive',
      'centerStageEnabled',
      'centerStageReasonCode',
      'centerStageSupported',
      'dynamicHeight',
      'dynamicWidth',
      'reasonCode',
      'wideUprightEnabled',
      'wideUprightReasonCode',
      'wideUprightSupported',
    ]);
    assert.equal(cameraFramingTelemetryFromCapabilities(null), null);
  });

  it('serializes a teardown restore behind an in-flight framing mutation', async () => {
    const queue = createCameraFramingOperationQueue();
    const events: string[] = [];
    let releaseEnable: (() => void) | undefined;
    const enableGate = new Promise<void>((resolve) => { releaseEnable = resolve; });

    const enable = queue.run(async () => {
      events.push('enable:start');
      await enableGate;
      events.push('enable:end');
    });
    const restore = queue.run(async () => {
      events.push('restore');
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.deepEqual(events, ['enable:start']);

    releaseEnable?.();
    await Promise.all([enable, restore]);
    assert.deepEqual(events, ['enable:start', 'enable:end', 'restore']);
  });

  it('adopts cooperative system state and never retains a failed direct intent', () => {
    assert.equal(cooperativeCenterStageIntentAfterRefresh(null, true, true), null);
    assert.equal(cooperativeCenterStageIntentAfterRefresh(true, true, false), false);
    assert.equal(cooperativeCenterStageIntentAfterRefresh(false, false, true), null);
    assert.equal(explicitFramingIntentAfterResult(true, true), true);
    assert.equal(explicitFramingIntentAfterResult(true, false), null);
  });

  it('defaults wide-upright off per call, preserves explicit opt-in across camera resets, and clears on leave', () => {
    let intent = wideUprightIntentAfterTransition(null, 'call-start');
    assert.equal(intent, false);

    intent = explicitFramingIntentAfterResult(true, true);
    assert.equal(wideUprightIntentAfterTransition(intent, 'camera-reset'), true);
    assert.equal(wideUprightIntentAfterTransition(intent, 'camera-reset'), true);

    intent = wideUprightIntentAfterTransition(intent, 'call-end');
    assert.equal(intent, null);
    assert.equal(wideUprightIntentAfterTransition(intent, 'call-start'), false);
  });

  it('restores the device that was actually widened before considering the current track', () => {
    const current = { trackId: 'track-2', deviceId: 'new-front-camera' };
    assert.equal(wideFramingRestoreDeviceId('old-front-camera', current), 'old-front-camera');
    assert.equal(wideFramingRestoreDeviceId(null, current), 'new-front-camera');
    assert.equal(wideFramingRestoreDeviceId(null, null), null);
  });
});
