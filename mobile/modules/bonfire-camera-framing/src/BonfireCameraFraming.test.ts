import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createCameraFramingClient,
  type NativeCameraFramingModule,
} from './BonfireCameraFraming';

test('fails closed when the iOS native module is absent', async () => {
  const client = createCameraFramingClient(undefined, 'ios');

  const capabilities = await client.getCapabilities('camera-1');
  assert.equal(capabilities.operational, false);
  assert.equal(capabilities.reasonCode, 'native_module_unavailable');

  const result = await client.setWideUprightFramingEnabled(true, 'camera-1');
  assert.equal(result.ok, false);
  assert.equal(result.code, 'native_module_unavailable');
  assert.equal(result.capabilities.wideUprightFramingEnabled, false);
});

test('fails closed when a native call rejects unexpectedly', async () => {
  const nativeModule: NativeCameraFramingModule = {
    getCapabilities: async () => {
      throw new Error('simulated bridge failure');
    },
    setCenterStageEnabled: async () => {
      throw new Error('simulated bridge failure');
    },
    setWideUprightFramingEnabled: async () => {
      throw new Error('simulated bridge failure');
    },
  };
  const client = createCameraFramingClient(nativeModule, 'ios');

  assert.equal((await client.getCapabilities('camera-1')).reasonCode, 'native_call_failed');
  assert.equal((await client.setCenterStageEnabled(true, 'camera-1')).code, 'native_call_failed');
  assert.equal((await client.setWideUprightFramingEnabled(true, 'camera-1')).ok, false);
});

test('passes the exact WebRTC camera identity and requested setting to native', async () => {
  const calls: Array<[string, boolean | string, string?]> = [];
  const capabilities = {
    platform: 'ios',
    iosVersion: '26.0',
    ios26OrNewer: true,
    deviceSupported: true,
    operational: true,
    activeWebRTCCameraAvailable: true,
    activeWebRTCCameraAmbiguous: false,
    activeDeviceId: 'exact-camera-id',
    activeDevicePosition: 'front' as const,
    activeDeviceType: 'AVCaptureDeviceTypeBuiltInUltraWideCamera',
    frontUltraWideAvailable: true,
    centerStageSupported: true,
    centerStageActiveFormatSupported: true,
    centerStageEnabled: false,
    centerStageActive: false,
    dynamicAspectRatio16x9HardwareSupported: true,
    dynamicAspectRatio16x9ActiveFormatSupported: true,
    wideUprightFramingEnabled: false,
    dynamicWidth: 1080,
    dynamicHeight: 1920,
    reasonCode: null,
  };
  const nativeModule: NativeCameraFramingModule = {
    getCapabilities: async (deviceId) => {
      calls.push(['capabilities', deviceId]);
      return capabilities;
    },
    setCenterStageEnabled: async (enabled, deviceId) => {
      calls.push(['center-stage', enabled, deviceId]);
      return { ok: true, code: 'ok', message: 'applied', capabilities };
    },
    setWideUprightFramingEnabled: async (enabled, deviceId) => {
      calls.push(['wide-upright', enabled, deviceId]);
      return { ok: true, code: 'ok', message: 'applied', capabilities };
    },
  };
  const client = createCameraFramingClient(nativeModule, 'ios');

  await client.getCapabilities('exact-camera-id');
  await client.setCenterStageEnabled(true, 'exact-camera-id');
  await client.setWideUprightFramingEnabled(true, 'exact-camera-id');

  assert.deepEqual(calls, [
    ['capabilities', 'exact-camera-id'],
    ['center-stage', true, 'exact-camera-id'],
    ['wide-upright', true, 'exact-camera-id'],
  ]);
});

test('rejects an empty camera identity without calling native', async () => {
  let nativeCalls = 0;
  const nativeModule: NativeCameraFramingModule = {
    getCapabilities: async () => {
      nativeCalls += 1;
      throw new Error('should not run');
    },
    setCenterStageEnabled: async () => {
      nativeCalls += 1;
      throw new Error('should not run');
    },
    setWideUprightFramingEnabled: async () => {
      nativeCalls += 1;
      throw new Error('should not run');
    },
  };
  const client = createCameraFramingClient(nativeModule, 'ios');

  assert.equal((await client.getCapabilities('')).reasonCode, 'missing_device_id');
  assert.equal((await client.setCenterStageEnabled(true, '')).code, 'missing_device_id');
  assert.equal((await client.setWideUprightFramingEnabled(true, '')).code, 'missing_device_id');
  assert.equal(nativeCalls, 0);
});
