import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { describe, it } from 'node:test';

type CameraPluginTestApi = {
  adaptiveFrontCameraMarker: string;
  adaptiveCenterStageFormatSelection: string;
  adaptivePositionCameraSelection: string;
  assertCenterStagePatch: (source: string, sourcePath: string) => void;
  centerStageMarker: string;
  cooperativeCenterStageBlock: string;
  defaultPositionCameraSelection: string;
  forcedCenterStageBlock: string;
  patchCenterStageSource: (source: string, sourcePath?: string) => string;
  resolveWebRTCCameraSource: (projectRoot: string) => string;
  safeCenterStageFormatSelection: string;
  unsafeCenterStageFormatSelection: string;
};

const pluginPath = require.resolve(
  '../../plugins/withWebRTCMultitaskingCamera.js',
);
const mobileRoot = path.resolve(path.dirname(pluginPath), '..');
const plugin = require(pluginPath) as {
  __testing: CameraPluginTestApi;
};
const {
  adaptiveFrontCameraMarker,
  adaptiveCenterStageFormatSelection,
  adaptivePositionCameraSelection,
  assertCenterStagePatch,
  centerStageMarker,
  cooperativeCenterStageBlock,
  defaultPositionCameraSelection,
  forcedCenterStageBlock,
  patchCenterStageSource,
  resolveWebRTCCameraSource,
  safeCenterStageFormatSelection,
  unsafeCenterStageFormatSelection,
} = plugin.__testing;

describe('iOS WebRTC camera prebuild patch', () => {
  it('turns the installed dependency into an idempotent adaptive camera patch', () => {
    const sourcePath = resolveWebRTCCameraSource(mobileRoot);
    const installedSource = fs.readFileSync(sourcePath, 'utf8');
    const patchedSource = patchCenterStageSource(installedSource, sourcePath);

    assert.match(patchedSource, new RegExp(centerStageMarker));
    assert.match(
      patchedSource,
      /centerStageControlMode = AVCaptureCenterStageControlModeCooperative;/,
    );
    assert.doesNotMatch(
      patchedSource,
      /AVCaptureDevice\.centerStageEnabled\s*=\s*(?:YES|NO)\s*;/,
    );
    assert.match(
      patchedSource,
      /centerStageEnabled && centerStageSupported && !format\.isCenterStageSupported/,
    );
    assert.match(patchedSource, new RegExp(adaptiveFrontCameraMarker));
    assert.match(patchedSource, /AVCaptureDeviceTypeBuiltInUltraWideCamera/);
    assert.match(patchedSource, /adaptiveFrontFormatAvailable/);
    assert.match(
      patchedSource,
      /format\.isCenterStageSupported && supportsLandscapeUpright/,
    );
    assert.match(
      patchedSource,
      /discoverySessionWithDeviceTypes:@\[ AVCaptureDeviceTypeBuiltInUltraWideCamera \]/,
    );
    assert.equal(
      patchCenterStageSource(patchedSource, sourcePath),
      patchedSource,
    );
  });

  it('patches the pristine EAS dependency shape, not only the local prebuilt copy', () => {
    const sourcePath = resolveWebRTCCameraSource(mobileRoot);
    const patchedSource = patchCenterStageSource(
      fs.readFileSync(sourcePath, 'utf8'),
      sourcePath,
    );
    const pristineSource = patchedSource
      .replace(cooperativeCenterStageBlock, forcedCenterStageBlock)
      .replace(adaptiveCenterStageFormatSelection, unsafeCenterStageFormatSelection)
      .replace(adaptivePositionCameraSelection, defaultPositionCameraSelection);

    assert.notEqual(pristineSource, patchedSource);
    assert.equal(
      patchCenterStageSource(pristineSource, sourcePath),
      patchedSource,
    );
  });

  it('migrates the stable WebRTC camera patch back to the reviewed adaptive path', () => {
    const sourcePath = resolveWebRTCCameraSource(mobileRoot);
    const adaptiveSource = patchCenterStageSource(
      fs.readFileSync(sourcePath, 'utf8'),
      sourcePath,
    );
    const stableSource = adaptiveSource
      .replace(adaptiveCenterStageFormatSelection, safeCenterStageFormatSelection)
      .replace(adaptivePositionCameraSelection, defaultPositionCameraSelection);

    assert.doesNotMatch(stableSource, new RegExp(adaptiveFrontCameraMarker));
    assert.equal(
      patchCenterStageSource(stableSource, sourcePath),
      adaptiveSource,
    );
  });

  it('routes both initial user capture and rear-to-front switching through the native selector', () => {
    const roomSource = fs.readFileSync(
      path.join(mobileRoot, 'src', 'realtime', 'useNativeRoom.ts'),
      'utf8',
    );
    const cameraSource = patchCenterStageSource(
      fs.readFileSync(resolveWebRTCCameraSource(mobileRoot), 'utf8'),
    );

    assert.match(
      roomSource,
      /nativeCameraConstraints = \{ facingMode: 'user', width: 1280, height: 720, frameRate: 30 \}/,
    );
    assert.match(
      roomSource,
      /mediaDevices\.getUserMedia\(\{[\s\S]*video: withVideo \? nativeCameraConstraints : false/,
    );
    assert.match(
      roomSource,
      /await track\.applyConstraints\(\{[\s\S]*\.\.\.nativeCameraConstraints,[\s\S]*facingMode: targetFacingMode/,
    );
    assert.match(
      cameraSource,
      /if \(!deviceId\) \{[\s\S]*deviceId = \[self findDeviceForPosition:position\]\.uniqueID;/,
    );
    assert.match(
      cameraSource,
      /if \(self\.running && hasChanged\) \{[\s\S]*\[self startCapture\];/,
    );
  });

  it('fails closed if the reviewed adaptive camera selection is changed unexpectedly', () => {
    const sourcePath = resolveWebRTCCameraSource(mobileRoot);
    const patchedSource = patchCenterStageSource(
      fs.readFileSync(sourcePath, 'utf8'),
      sourcePath,
    );
    const regressedSource = patchedSource.replace(adaptivePositionCameraSelection, [
      '- (AVCaptureDevice *)findDeviceForPosition:(AVCaptureDevicePosition)position {',
      '    return nil;',
      '}',
    ].join('\n'));

    assert.throws(
      () => assertCenterStagePatch(regressedSource, sourcePath),
      /adaptive ultra-wide camera selection is missing, duplicated, or still using WebRTC's default selector/,
    );
    assert.throws(
      () => patchCenterStageSource(regressedSource, sourcePath),
      /no longer matches the reviewed patched or unpatched shape/,
    );
  });

  it('fails closed if the reviewed adaptive format filter is changed unexpectedly', () => {
    const sourcePath = resolveWebRTCCameraSource(mobileRoot);
    const patchedSource = patchCenterStageSource(
      fs.readFileSync(sourcePath, 'utf8'),
      sourcePath,
    );
    const regressedSource = patchedSource.replace(
      adaptiveCenterStageFormatSelection,
      safeCenterStageFormatSelection,
    );

    assert.throws(
      () => assertCenterStagePatch(regressedSource, sourcePath),
      /adaptive Center Stage format selection patch is missing, duplicated, or still using the default format path/,
    );
  });

  it('fails closed if a forced Center Stage assignment returns', () => {
    const sourcePath = resolveWebRTCCameraSource(mobileRoot);
    const patchedSource = patchCenterStageSource(
      fs.readFileSync(sourcePath, 'utf8'),
      sourcePath,
    );
    const regressedSource = patchedSource.replace(
      centerStageMarker,
      `${centerStageMarker}\n    AVCaptureDevice.centerStageEnabled = YES;`,
    );

    assert.throws(
      () => assertCenterStagePatch(regressedSource, sourcePath),
      /still assigns centerStageEnabled/,
    );
    assert.throws(
      () => patchCenterStageSource(regressedSource, sourcePath),
      /no longer matches the reviewed patched or unpatched shape/,
    );
  });

  it('fails closed when an upstream source change invalidates the reviewed patch', () => {
    assert.throws(
      () => patchCenterStageSource('@implementation VideoCaptureController\n@end'),
      /no longer matches the reviewed patched or unpatched shape/,
    );
  });
});
