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
    const joinSource = roomSource.slice(
      roomSource.indexOf('const join = useCallback'),
      roomSource.indexOf('const setMuted = useCallback'),
    );
    assert.ok(joinSource.length > 0, 'native join implementation must remain discoverable');
    assert.match(
      joinSource,
      /resetCameraFraming\(true\);[\s\S]*requestedCenterStageRef\.current = false;[\s\S]*wideUprightIntentAfterTransition\([\s\S]*'call-start'/,
      'each call must actively start with Center Stage and Wide Upright off',
    );
    assert.ok(
      joinSource.indexOf('await refreshCameraFramingInternal(true)')
        < joinSource.indexOf('connectSocket();'),
      'initial framing must settle before the camera is offered to the SFU',
    );
    assert.match(
      joinSource,
      /catch \(error\) \{[\s\S]*requestedVideo\.current = false;[\s\S]*mediaDevices\.getUserMedia\(\{ audio: true, video: false \}\)/,
      'camera acquisition failure must fall back to a camera-off room join',
    );
    const roomScreenSource = fs.readFileSync(
      path.join(mobileRoot, 'src', 'screens', 'RoomScreen.tsx'),
      'utf8',
    );
    assert.match(
      roomScreenSource,
      /accessibilityLabel="Join room with camera on and microphone off"[\s\S]*onPress=\{\(\) => joinRoom\(true, false\)\}/,
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

  it('requires confirmed landscape or explicit 9:16 portrait dimensions instead of accepting the square sensor default', () => {
    const guardSource = fs.readFileSync(
      path.join(
        mobileRoot,
        'modules',
        'bonfire-camera-framing',
        'ios',
        'BonfireCameraDeviceGuard.m',
      ),
      'utf8',
    );

    assert.match(
      guardSource,
      /wideEnabled\s*=\s*\[device\.dynamicAspectRatio isEqualToString:AVCaptureAspectRatio16x9\]\s*&&\s*dimensions\.width > 0\s*&&\s*dimensions\.height > 0\s*&&\s*dimensions\.width > dimensions\.height;/,
    );
    assert.match(
      guardSource,
      /currentlyEnabled && currentLandscapeDimensionsValid/,
    );
    assert.match(
      guardSource,
      /currentlyPortrait && currentPortraitDimensionsValid/,
    );
    assert.match(
      guardSource,
      /targetRatio = \[supportedRatios containsObject:AVCaptureAspectRatio9x16\][\s\S]*\? AVCaptureAspectRatio9x16/,
    );
    assert.match(
      guardSource,
      /dimensionsMatchRequestedOrientation = mutation\.isEnabled[\s\S]*\? dimensions\.width > dimensions\.height[\s\S]*: dimensions\.height > dimensions\.width/,
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
