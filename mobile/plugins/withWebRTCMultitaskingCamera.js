const fs = require('node:fs');
const path = require('node:path');
const {
  createRunOncePlugin,
  withAppDelegate,
  withDangerousMod,
} = require('@expo/config-plugins');

const pluginName = 'with-webrtc-multitasking-camera';
const pluginVersion = '1.7.0';
const appDelegateMarker = '// BonfireOS: keep WebRTC camera capture available during iOS call multitasking.';
const bridgingImport = '#import <react-native-webrtc/WebRTCModuleOptions.h>';
const centerStageMarker = '// BonfireOS: preserve the user and system Center Stage choice.';
const adaptiveFrontCameraMarker =
  '// BonfireOS: prefer the iOS 26 square-sensor front camera when WebRTC can use its adaptive format.';
const forcedCenterStageAssignment = /AVCaptureDevice\.centerStageEnabled\s*=\s*(?:YES|NO)\s*;/g;
const pictureInPictureScaleMarker =
  '// BonfireOS: honor the requested PiP gravity when rotating mobile frames.';
const pictureInPictureObjectFitMarker =
  '// BonfireOS: recalculate the rotation transform when PiP fit changes.';
const defaultPictureInPictureScale = [
  '        CGFloat scale = 1;',
  '        if (rotation == 90 || rotation == 270) {',
  '            CGSize size = self.bounds.size;',
  '            scale = size.height / size.width;',
  '        }',
].join('\n');
const gravityAwarePictureInPictureScale = [
  `        ${pictureInPictureScaleMarker}`,
  '        CGFloat scale = 1;',
  '        if (rotation == 90 || rotation == 270) {',
  '            CGSize size = self.bounds.size;',
  '            CGFloat width = MAX(1, size.width);',
  '            CGFloat height = MAX(1, size.height);',
  '            CGFloat widthToHeight = width / height;',
  '            CGFloat heightToWidth = height / width;',
  '            BOOL fillsBounds =',
  '                [self.sampleBufferLayer.videoGravity isEqualToString:AVLayerVideoGravityResizeAspectFill];',
  '            scale = fillsBounds',
  '                ? MAX(widthToHeight, heightToWidth)',
  '                : MIN(widthToHeight, heightToWidth);',
  '        }',
].join('\n');
const defaultPictureInPictureObjectFit = [
  '- (void)setObjectFit:(RTCVideoViewObjectFit)fit {',
  '    if (fit == RTCVideoViewObjectFitCover) {',
  '        self.sampleView.sampleBufferLayer.videoGravity = AVLayerVideoGravityResizeAspectFill;',
  '    } else {',
  '        self.sampleView.sampleBufferLayer.videoGravity = AVLayerVideoGravityResizeAspect;',
  '    }',
  '}',
].join('\n');
const recalculatingPictureInPictureObjectFit = [
  '- (void)setObjectFit:(RTCVideoViewObjectFit)fit {',
  `    ${pictureInPictureObjectFitMarker}`,
  '    if (fit == RTCVideoViewObjectFitCover) {',
  '        self.sampleView.sampleBufferLayer.videoGravity = AVLayerVideoGravityResizeAspectFill;',
  '    } else {',
  '        self.sampleView.sampleBufferLayer.videoGravity = AVLayerVideoGravityResizeAspect;',
  '    }',
  '    [self.sampleView requestScaleRecalculation];',
  '}',
].join('\n');

const forcedCenterStageBlock = [
  '    // Enable Center Stage when the device supports it; cooperative with Control Center.',
  '    if (@available(iOS 16.0, *)) {',
  '        BOOL centerStageSupported = NO;',
  '        for (AVCaptureDeviceFormat *fmt in [RTCCameraVideoCapturer supportedFormatsForDevice:self.device]) {',
  '            if (fmt.isCenterStageSupported) {',
  '                centerStageSupported = YES;',
  '                break;',
  '            }',
  '        }',
  '        if (centerStageSupported) {',
  '            AVCaptureDevice.centerStageControlMode = AVCaptureCenterStageControlModeCooperative;',
  '            AVCaptureDevice.centerStageEnabled = YES;',
  '        } else if (AVCaptureDevice.isCenterStageEnabled) {',
  '            AVCaptureDevice.centerStageEnabled = NO;',
  '        }',
  '    }',
].join('\n');

const cooperativeCenterStageBlock = [
  `    ${centerStageMarker}`,
  '    if (@available(iOS 16.0, *)) {',
  '        BOOL centerStageSupported = NO;',
  '        for (AVCaptureDeviceFormat *fmt in [RTCCameraVideoCapturer supportedFormatsForDevice:self.device]) {',
  '            if (fmt.isCenterStageSupported) {',
  '                centerStageSupported = YES;',
  '                break;',
  '            }',
  '        }',
  '        if (centerStageSupported) {',
  '            AVCaptureDevice.centerStageControlMode = AVCaptureCenterStageControlModeCooperative;',
  '        }',
  '    }',
].join('\n');

const unsafeCenterStageFormatSelection = [
  '    BOOL centerStageEnabled = NO;',
  '    if (@available(iOS 16.0, *)) {',
  '        centerStageEnabled = AVCaptureDevice.isCenterStageEnabled;',
  '    }',
  '',
  '    for (AVCaptureDeviceFormat *format in formats) {',
  '        // Center Stage only permits supported formats.',
  '        if (@available(iOS 16.0, *)) {',
  '            if (centerStageEnabled && !format.isCenterStageSupported) {',
].join('\n');

const safeCenterStageFormatSelection = [
  '    BOOL centerStageEnabled = NO;',
  '    BOOL centerStageSupported = NO;',
  '    if (@available(iOS 16.0, *)) {',
  '        centerStageEnabled = AVCaptureDevice.isCenterStageEnabled;',
  '        for (AVCaptureDeviceFormat *format in formats) {',
  '            if (format.isCenterStageSupported) {',
  '                centerStageSupported = YES;',
  '                break;',
  '            }',
  '        }',
  '    }',
  '',
  '    for (AVCaptureDeviceFormat *format in formats) {',
  '        // Honor Center Stage when this camera supports it without changing the user setting.',
  '        if (@available(iOS 16.0, *)) {',
  '            if (centerStageEnabled && centerStageSupported && !format.isCenterStageSupported) {',
].join('\n');

const adaptiveCenterStageFormatSelection = [
  '    BOOL centerStageEnabled = NO;',
  '    BOOL centerStageSupported = NO;',
  '    if (@available(iOS 16.0, *)) {',
  '        centerStageEnabled = AVCaptureDevice.isCenterStageEnabled;',
  '        for (AVCaptureDeviceFormat *format in formats) {',
  '            if (format.isCenterStageSupported) {',
  '                centerStageSupported = YES;',
  '                break;',
  '            }',
  '        }',
  '    }',
  '',
  '    BOOL adaptiveFrontFormatAvailable = NO;',
  '    if (@available(iOS 26.0, *)) {',
  '        if (device.position == AVCaptureDevicePositionFront &&',
  '            [device.deviceType isEqualToString:AVCaptureDeviceTypeBuiltInUltraWideCamera]) {',
  '            for (AVCaptureDeviceFormat *format in formats) {',
  '                BOOL supportsLandscapeUpright =',
  '                    [format.supportedDynamicAspectRatios containsObject:AVCaptureAspectRatio16x9];',
  '                if (format.isCenterStageSupported && supportsLandscapeUpright) {',
  '                    adaptiveFrontFormatAvailable = YES;',
  '                    break;',
  '                }',
  '            }',
  '        }',
  '    }',
  '',
  '    for (AVCaptureDeviceFormat *format in formats) {',
  '        // Keep both Bonfire framing controls operational on the square-sensor front camera.',
  '        if (@available(iOS 26.0, *)) {',
  '            if (adaptiveFrontFormatAvailable) {',
  '                BOOL supportsLandscapeUpright =',
  '                    [format.supportedDynamicAspectRatios containsObject:AVCaptureAspectRatio16x9];',
  '                if (!format.isCenterStageSupported || !supportsLandscapeUpright) {',
  '                    continue;',
  '                }',
  '            }',
  '        }',
  '        // Honor Center Stage when this camera supports it without changing the user setting.',
  '        if (@available(iOS 16.0, *)) {',
  '            if (centerStageEnabled && centerStageSupported && !format.isCenterStageSupported) {',
].join('\n');

const defaultPositionCameraSelection = [
  '- (AVCaptureDevice *)findDeviceForPosition:(AVCaptureDevicePosition)position {',
  '    NSArray<AVCaptureDevice *> *captureDevices = [RTCCameraVideoCapturer captureDevices];',
  '    for (AVCaptureDevice *device in captureDevices) {',
  '        if (device.position == position) {',
  '            return device;',
  '        }',
  '    }',
  '',
  '    return [captureDevices firstObject];',
  '}',
].join('\n');

const adaptivePositionCameraSelection = [
  '- (AVCaptureDevice *)findDeviceForPosition:(AVCaptureDevicePosition)position {',
  `    ${adaptiveFrontCameraMarker}`,
  '    if (@available(iOS 26.0, *)) {',
  '        if (position == AVCaptureDevicePositionFront) {',
  '            AVCaptureDeviceDiscoverySession *frontCameraSession =',
  '                [AVCaptureDeviceDiscoverySession',
  '                    discoverySessionWithDeviceTypes:@[ AVCaptureDeviceTypeBuiltInUltraWideCamera ]',
  '                    mediaType:AVMediaTypeVideo',
  '                    position:AVCaptureDevicePositionFront];',
  '            for (AVCaptureDevice *candidate in frontCameraSession.devices) {',
  '                NSArray<AVCaptureDeviceFormat *> *candidateFormats =',
  '                    [RTCCameraVideoCapturer supportedFormatsForDevice:candidate];',
  '                for (AVCaptureDeviceFormat *format in candidateFormats) {',
  '                    BOOL supportsLandscapeUpright =',
  '                        [format.supportedDynamicAspectRatios containsObject:AVCaptureAspectRatio16x9];',
  '                    if (format.isCenterStageSupported && supportsLandscapeUpright) {',
  '                        return candidate;',
  '                    }',
  '                }',
  '            }',
  '        }',
  '    }',
  '',
  '    NSArray<AVCaptureDevice *> *captureDevices = [RTCCameraVideoCapturer captureDevices];',
  '    for (AVCaptureDevice *device in captureDevices) {',
  '        if (device.position == position) {',
  '            return device;',
  '        }',
  '    }',
  '',
  '    return [captureDevices firstObject];',
  '}',
].join('\n');

function occurrenceCount(source, value) {
  return source.split(value).length - 1;
}

function assertCenterStagePatch(source, sourcePath) {
  const forcedAssignments = source.match(forcedCenterStageAssignment) ?? [];
  if (forcedAssignments.length > 0) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: react-native-webrtc still assigns centerStageEnabled.`,
    );
  }
  if (occurrenceCount(source, cooperativeCenterStageBlock) !== 1) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: the cooperative Center Stage patch is missing or duplicated.`,
    );
  }
  if (occurrenceCount(source, adaptiveCenterStageFormatSelection) !== 1 ||
      occurrenceCount(source, safeCenterStageFormatSelection) !== 0 ||
      occurrenceCount(source, unsafeCenterStageFormatSelection) !== 0) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: the adaptive Center Stage format selection patch is missing, duplicated, or still using the default format path.`,
    );
  }
  if (occurrenceCount(source, adaptivePositionCameraSelection) !== 1 ||
      occurrenceCount(source, defaultPositionCameraSelection) !== 0) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: the adaptive ultra-wide camera selection is missing, duplicated, or still using WebRTC's default selector.`,
    );
  }
}

function patchCenterStageSource(source, sourcePath = 'VideoCaptureController.m') {
  const forcedBlockCount = occurrenceCount(source, forcedCenterStageBlock);
  const cooperativeBlockCount = occurrenceCount(source, cooperativeCenterStageBlock);
  const unsafeFormatCount = occurrenceCount(source, unsafeCenterStageFormatSelection);
  const safeFormatCount = occurrenceCount(source, safeCenterStageFormatSelection);
  const adaptiveFormatCount = occurrenceCount(source, adaptiveCenterStageFormatSelection);
  const defaultCameraCount = occurrenceCount(source, defaultPositionCameraSelection);
  const adaptiveCameraCount = occurrenceCount(source, adaptivePositionCameraSelection);

  const centerStageControlShapeValid =
    (forcedBlockCount === 1 && cooperativeBlockCount === 0) ||
    (forcedBlockCount === 0 && cooperativeBlockCount === 1);
  const formatShapeValid =
    unsafeFormatCount + safeFormatCount + adaptiveFormatCount === 1;
  const cameraShapeValid = defaultCameraCount + adaptiveCameraCount === 1;

  if (!centerStageControlShapeValid || !formatShapeValid || !cameraShapeValid) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: the react-native-webrtc camera source no longer matches the reviewed patched or unpatched shape.`,
    );
  }

  let patchedSource = source;
  if (forcedBlockCount === 1) {
    patchedSource = patchedSource.replace(forcedCenterStageBlock, cooperativeCenterStageBlock);
  }
  if (unsafeFormatCount === 1) {
    patchedSource = patchedSource.replace(
      unsafeCenterStageFormatSelection,
      adaptiveCenterStageFormatSelection,
    );
  } else if (safeFormatCount === 1) {
    patchedSource = patchedSource.replace(
      safeCenterStageFormatSelection,
      adaptiveCenterStageFormatSelection,
    );
  }
  if (defaultCameraCount === 1) {
    patchedSource = patchedSource.replace(
      defaultPositionCameraSelection,
      adaptivePositionCameraSelection,
    );
  }

  assertCenterStagePatch(patchedSource, sourcePath);
  return patchedSource;
}

function resolveWebRTCCameraSource(projectRoot) {
  let packageJson;
  try {
    packageJson = require.resolve('react-native-webrtc/package.json', {
      paths: [projectRoot],
    });
  } catch (error) {
    throw new Error(
      `${pluginName} could not resolve react-native-webrtc from ${projectRoot}: ${error.message}`,
    );
  }
  return path.join(
    path.dirname(packageJson),
    'ios',
    'RCTWebRTC',
    'VideoCaptureController.m',
  );
}

function resolveWebRTCPictureInPictureSource(projectRoot) {
  let packageJson;
  try {
    packageJson = require.resolve('react-native-webrtc/package.json', {
      paths: [projectRoot],
    });
  } catch (error) {
    throw new Error(
      `${pluginName} could not resolve react-native-webrtc from ${projectRoot}: ${error.message}`,
    );
  }
  return path.join(
    path.dirname(packageJson),
    'ios',
    'RCTWebRTC',
    'SampleBufferVideoCallView.m',
  );
}

function resolveWebRTCPictureInPictureControllerSource(projectRoot) {
  let packageJson;
  try {
    packageJson = require.resolve('react-native-webrtc/package.json', {
      paths: [projectRoot],
    });
  } catch (error) {
    throw new Error(
      `${pluginName} could not resolve react-native-webrtc from ${projectRoot}: ${error.message}`,
    );
  }
  return path.join(
    path.dirname(packageJson),
    'ios',
    'RCTWebRTC',
    'PIPController.m',
  );
}

function patchPictureInPictureScale(source, sourcePath = 'SampleBufferVideoCallView.m') {
  const defaultCount = occurrenceCount(source, defaultPictureInPictureScale);
  const patchedCount = occurrenceCount(source, gravityAwarePictureInPictureScale);
  if (defaultCount + patchedCount !== 1) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: the react-native-webrtc PiP scale source no longer matches the reviewed patched or unpatched shape.`,
    );
  }
  const patchedSource = defaultCount === 1
    ? source.replace(defaultPictureInPictureScale, gravityAwarePictureInPictureScale)
    : source;
  if (
    occurrenceCount(patchedSource, gravityAwarePictureInPictureScale) !== 1
    || occurrenceCount(patchedSource, defaultPictureInPictureScale) !== 0
  ) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: the gravity-aware PiP scale patch is missing or duplicated.`,
    );
  }
  return patchedSource;
}

function patchPictureInPictureObjectFit(source, sourcePath = 'PIPController.m') {
  const defaultCount = occurrenceCount(source, defaultPictureInPictureObjectFit);
  const patchedCount = occurrenceCount(source, recalculatingPictureInPictureObjectFit);
  if (defaultCount + patchedCount !== 1) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: the react-native-webrtc PiP object-fit source no longer matches the reviewed patched or unpatched shape.`,
    );
  }
  const patchedSource = defaultCount === 1
    ? source.replace(defaultPictureInPictureObjectFit, recalculatingPictureInPictureObjectFit)
    : source;
  if (
    occurrenceCount(patchedSource, recalculatingPictureInPictureObjectFit) !== 1
    || occurrenceCount(patchedSource, defaultPictureInPictureObjectFit) !== 0
  ) {
    throw new Error(
      `${pluginName} refuses to prebuild ${sourcePath}: the PiP object-fit recalculation patch is missing or duplicated.`,
    );
  }
  return patchedSource;
}

function withWebRTCMultitaskingCamera(config) {
  config = withAppDelegate(config, (appDelegateConfig) => {
    if (appDelegateConfig.modResults.language !== 'swift') {
      throw new Error(`${pluginName} requires the Expo Swift AppDelegate.`);
    }

    let source = appDelegateConfig.modResults.contents;
    if (!source.includes(appDelegateMarker)) {
      const launchAnchor = '    let delegate = ReactNativeDelegate()';
      if (!source.includes(launchAnchor)) {
        throw new Error(`${pluginName} could not find the AppDelegate launch anchor.`);
      }
      source = source.replace(
        launchAnchor,
        [
          `    ${appDelegateMarker}`,
          '    let webRTCOptions = WebRTCModuleOptions.sharedInstance()',
          '    webRTCOptions.enableMultitaskingCameraAccess = true',
          '',
          launchAnchor,
        ].join('\n'),
      );
      appDelegateConfig.modResults.contents = source;
    }
    return appDelegateConfig;
  });

  // react-native-webrtc is an Objective-C pod. Expose its public options
  // header to the generated Swift AppDelegate through Expo's bridging header.
  // Patch the dependency during every prebuild so a clean EAS install cannot
  // silently force Center Stage back on and override the user's camera choice.
  // On iOS 26, select the front ultra-wide only when WebRTC can use a format
  // that supports both Center Stage and the square sensor's adaptive 16:9 mode.
  config = withDangerousMod(config, ['ios', async (dangerousConfig) => {
    const iosRoot = dangerousConfig.modRequest.platformProjectRoot;
    const cameraSource = resolveWebRTCCameraSource(
      dangerousConfig.modRequest.projectRoot,
    );
    const originalCameraSource = fs.readFileSync(cameraSource, 'utf8');
    const patchedCameraSource = patchCenterStageSource(
      originalCameraSource,
      cameraSource,
    );
    if (patchedCameraSource !== originalCameraSource) {
      fs.writeFileSync(cameraSource, patchedCameraSource);
    }

    const pictureInPictureSource = resolveWebRTCPictureInPictureSource(
      dangerousConfig.modRequest.projectRoot,
    );
    const originalPictureInPictureSource = fs.readFileSync(pictureInPictureSource, 'utf8');
    const patchedPictureInPictureSource = patchPictureInPictureScale(
      originalPictureInPictureSource,
      pictureInPictureSource,
    );
    if (patchedPictureInPictureSource !== originalPictureInPictureSource) {
      fs.writeFileSync(pictureInPictureSource, patchedPictureInPictureSource);
    }

    const pictureInPictureControllerSource = resolveWebRTCPictureInPictureControllerSource(
      dangerousConfig.modRequest.projectRoot,
    );
    const originalPictureInPictureControllerSource = fs.readFileSync(
      pictureInPictureControllerSource,
      'utf8',
    );
    const patchedPictureInPictureControllerSource = patchPictureInPictureObjectFit(
      originalPictureInPictureControllerSource,
      pictureInPictureControllerSource,
    );
    if (patchedPictureInPictureControllerSource !== originalPictureInPictureControllerSource) {
      fs.writeFileSync(pictureInPictureControllerSource, patchedPictureInPictureControllerSource);
    }

    const projectDirectories = fs.readdirSync(iosRoot, { withFileTypes: true })
      .filter((entry) => entry.isDirectory() && !entry.name.endsWith('.xcodeproj') && entry.name !== 'Pods')
      .map((entry) => path.join(iosRoot, entry.name));
    const bridgingHeader = projectDirectories
      .map((directory) => fs.readdirSync(directory)
        .find((name) => name.endsWith('-Bridging-Header.h')))
      .map((name, index) => name ? path.join(projectDirectories[index], name) : '')
      .find(Boolean);

    if (!bridgingHeader) {
      throw new Error(`${pluginName} could not find the generated bridging header.`);
    }
    const header = fs.readFileSync(bridgingHeader, 'utf8');
    if (!header.includes(bridgingImport)) {
      fs.writeFileSync(bridgingHeader, `${header.trimEnd()}\n${bridgingImport}\n`);
    }
    return dangerousConfig;
  }]);

  return config;
}

const plugin = createRunOncePlugin(
  withWebRTCMultitaskingCamera,
  pluginName,
  pluginVersion,
);

plugin.__testing = {
  adaptiveFrontCameraMarker,
  adaptiveCenterStageFormatSelection,
  adaptivePositionCameraSelection,
  assertCenterStagePatch,
  centerStageMarker,
  cooperativeCenterStageBlock,
  defaultPositionCameraSelection,
  defaultPictureInPictureObjectFit,
  forcedCenterStageBlock,
  defaultPictureInPictureScale,
  gravityAwarePictureInPictureScale,
  recalculatingPictureInPictureObjectFit,
  patchCenterStageSource,
  patchPictureInPictureObjectFit,
  patchPictureInPictureScale,
  pictureInPictureObjectFitMarker,
  pictureInPictureScaleMarker,
  resolveWebRTCCameraSource,
  resolveWebRTCPictureInPictureControllerSource,
  resolveWebRTCPictureInPictureSource,
  safeCenterStageFormatSelection,
  unsafeCenterStageFormatSelection,
};

module.exports = plugin;
