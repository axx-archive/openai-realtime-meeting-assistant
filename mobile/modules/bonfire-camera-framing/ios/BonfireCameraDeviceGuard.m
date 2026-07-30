#import "BonfireCameraDeviceGuard.h"

#import <AVFoundation/AVFoundation.h>
#import <UIKit/UIKit.h>

static NSString *const BFCodeOK = @"ok";
static NSString *const BFCodeAlreadyApplied = @"already_applied";
static NSString *const BFCodeMissingDeviceID = @"missing_device_id";
static NSString *const BFCodeDeviceUnavailable = @"camera_device_unavailable";
static NSString *const BFCodeRequiresIOS26 = @"requires_ios_26";
static NSString *const BFCodeFrontUltraWideUnavailable = @"front_ultra_wide_unavailable";
static NSString *const BFCodeWrongActiveCamera = @"active_camera_not_front_ultra_wide";
static NSString *const BFCodeCenterStageUnsupported = @"center_stage_unsupported";
static NSString *const BFCodeDynamicRatioUnsupported = @"active_format_missing_16x9";
static NSString *const BFCodeRestoreRatioUnavailable = @"restore_aspect_ratio_unavailable";
static NSString *const BFCodeConfigurationLockFailed = @"configuration_lock_failed";
static NSString *const BFCodeConfigurationFailed = @"configuration_failed";

typedef void (^BFResultCompletion)(NSDictionary<NSString *, id> *result);

/// Consecutive requests for the same state share one native mutation while
/// requests for different states retain FIFO ordering. In particular, a final
/// teardown `false` can never be rejected merely because an enable is settling.
@interface BFWideFramingMutation : NSObject

@property(nonatomic, assign, readonly, getter=isEnabled) BOOL enabled;
@property(nonatomic, strong, readonly) NSMutableArray<BFResultCompletion> *completions;

- (instancetype)initWithEnabled:(BOOL)enabled completion:(BFResultCompletion)completion;
- (void)addCompletion:(BFResultCompletion)completion;

@end

@implementation BFWideFramingMutation

- (instancetype)initWithEnabled:(BOOL)enabled completion:(BFResultCompletion)completion {
  self = [super init];
  if (self != nil) {
    _enabled = enabled;
    _completions = [NSMutableArray arrayWithObject:[completion copy]];
  }
  return self;
}

- (void)addCompletion:(BFResultCompletion)completion {
  [self.completions addObject:[completion copy]];
}

@end

/// The device owns the AVFoundation completion block. Keeping the device weak
/// here prevents device -> block -> lease -> device while still holding the
/// configuration lock until Apple's asynchronous completion is delivered.
@interface BFCameraConfigurationLease : NSObject

@property(nonatomic, weak, readonly) AVCaptureDevice *device;

- (instancetype)initWithLockedDevice:(AVCaptureDevice *)device;
- (BOOL)claimFinishWithLockedDevice:(AVCaptureDevice * _Nullable * _Nonnull)device;

@end


@implementation BFCameraConfigurationLease {
  BOOL _finished;
}

- (instancetype)initWithLockedDevice:(AVCaptureDevice *)device {
  self = [super init];
  if (self != nil) {
    _device = device;
    _finished = NO;
  }
  return self;
}

- (BOOL)claimFinishWithLockedDevice:(AVCaptureDevice * _Nullable * _Nonnull)device {
  @synchronized(self) {
    if (_finished) {
      *device = nil;
      return NO;
    }
    _finished = YES;
    *device = self.device;
    return YES;
  }
}

@end


@implementation BonfireCameraDeviceGuard

// Store the iOS 26 typed-enum values as their underlying NSString type so the
// module remains warning-clean at its iOS 16.4 deployment target.
static NSMutableDictionary<NSString *, NSString *> *savedAspectRatios;
static NSMutableDictionary<NSString *, BFWideFramingMutation *> *activeMutations;
static NSMutableDictionary<NSString *, NSMutableArray<BFWideFramingMutation *> *> *queuedMutations;
// This map, rather than the AVFoundation completion block, retains each locked
// device until its asynchronous mutation reaches exactly-once cleanup.
static NSMutableDictionary<NSString *, AVCaptureDevice *> *lockedDevices;

+ (void)initialize {
  if (self == BonfireCameraDeviceGuard.class) {
    savedAspectRatios = [NSMutableDictionary dictionary];
    activeMutations = [NSMutableDictionary dictionary];
    queuedMutations = [NSMutableDictionary dictionary];
    lockedDevices = [NSMutableDictionary dictionary];
  }
}

+ (NSDictionary<NSString *, id> *)capabilitiesForDeviceID:(NSString *)deviceID {
  AVCaptureDevice *device = [self deviceForID:deviceID];
  AVCaptureDevice *frontUltraWide = [self frontUltraWideDevice];

  BOOL ios26OrNewer = NO;
  BOOL dynamicHardwareSupported = NO;
  BOOL dynamicActiveFormatSupported = NO;
  BOOL wideEnabled = NO;
  NSInteger dynamicWidth = 0;
  NSInteger dynamicHeight = 0;
  if (@available(iOS 26.0, *)) {
    ios26OrNewer = YES;
    // Every active-state/capability read is tied to the WebRTC device ID. A
    // different discovered front camera is never used as a proxy.
    dynamicHardwareSupported = [self device:device hasFormatSupportingRatio:AVCaptureAspectRatio16x9];
    if (device != nil) {
      dynamicActiveFormatSupported =
          [device.activeFormat.supportedDynamicAspectRatios containsObject:AVCaptureAspectRatio16x9];
      CMVideoDimensions dimensions = device.dynamicDimensions;
      dynamicWidth = dimensions.width;
      dynamicHeight = dimensions.height;
      // A persisted ratio is intent, not proof that AVFoundation is producing
      // usable frames. Report wide framing active only after the adaptive
      // camera confirms nonzero landscape output dimensions.
      wideEnabled =
          [device.dynamicAspectRatio isEqualToString:AVCaptureAspectRatio16x9] &&
          dimensions.width > 0 &&
          dimensions.height > 0 &&
          dimensions.width > dimensions.height;
    }
  }

  BOOL centerStageEnabled = NO;
  BOOL centerStageActive = NO;
  BOOL centerStageSupported = NO;
  BOOL centerStageActiveFormatSupported = NO;
  if (@available(iOS 14.5, *)) {
    centerStageEnabled = AVCaptureDevice.isCenterStageEnabled;
    if (device != nil) {
      centerStageSupported = [self centerStageSupportedByDevice:device];
      centerStageActive = device.isCenterStageActive;
      centerStageActiveFormatSupported = device.activeFormat.isCenterStageSupported;
    }
  }

  BOOL frontUltraWideAvailable = frontUltraWide != nil;
  BOOL exactFrontUltraWide = device != nil &&
      device.position == AVCaptureDevicePositionFront &&
      [device.deviceType isEqualToString:AVCaptureDeviceTypeBuiltInUltraWideCamera];
  BOOL deviceSupported = ios26OrNewer && exactFrontUltraWide && dynamicHardwareSupported;
  BOOL operational = deviceSupported && dynamicActiveFormatSupported;

  NSString *wideReasonCode = nil;
  if (deviceID.length == 0) {
    wideReasonCode = BFCodeMissingDeviceID;
  } else if (device == nil) {
    wideReasonCode = BFCodeDeviceUnavailable;
  } else if (!ios26OrNewer) {
    wideReasonCode = BFCodeRequiresIOS26;
  } else if (!exactFrontUltraWide) {
    wideReasonCode = frontUltraWideAvailable ? BFCodeWrongActiveCamera : BFCodeFrontUltraWideUnavailable;
  } else if (!dynamicHardwareSupported || !dynamicActiveFormatSupported) {
    wideReasonCode = BFCodeDynamicRatioUnsupported;
  }

  NSString *centerStageReasonCode = nil;
  if (deviceID.length == 0) {
    centerStageReasonCode = BFCodeMissingDeviceID;
  } else if (device == nil) {
    centerStageReasonCode = BFCodeDeviceUnavailable;
  } else if (!exactFrontUltraWide) {
    centerStageReasonCode = frontUltraWideAvailable ? BFCodeWrongActiveCamera : BFCodeFrontUltraWideUnavailable;
  } else if (!centerStageSupported || !centerStageActiveFormatSupported) {
    centerStageReasonCode = BFCodeCenterStageUnsupported;
  }
  BOOL centerStageOperational = exactFrontUltraWide && centerStageSupported && centerStageActiveFormatSupported;
  NSString *generalReasonCode = (operational || centerStageOperational) ? nil : wideReasonCode;

  return @{
    @"platform" : @"ios",
    @"iosVersion" : UIDevice.currentDevice.systemVersion ?: @"unknown",
    @"ios26OrNewer" : @(ios26OrNewer),
    @"deviceSupported" : @(deviceSupported),
    @"operational" : @(operational),
    @"activeWebRTCCameraAvailable" : @(device != nil),
    @"activeWebRTCCameraAmbiguous" : @NO,
    @"activeDeviceId" : device.uniqueID ?: (id)NSNull.null,
    @"activeDevicePosition" : device != nil ? [self nameForPosition:device.position] : (id)NSNull.null,
    @"activeDeviceType" : device.deviceType ?: (id)NSNull.null,
    @"frontUltraWideAvailable" : @(frontUltraWideAvailable),
    @"centerStageSupported" : @(centerStageSupported),
    @"centerStageActiveFormatSupported" : @(centerStageActiveFormatSupported),
    @"centerStageOperational" : @(centerStageOperational),
    @"centerStageEnabled" : @(centerStageEnabled),
    @"centerStageActive" : @(centerStageActive),
    @"dynamicAspectRatio16x9HardwareSupported" : @(dynamicHardwareSupported),
    @"dynamicAspectRatio16x9ActiveFormatSupported" : @(dynamicActiveFormatSupported),
    @"wideUprightFramingEnabled" : @(wideEnabled),
    @"dynamicWidth" : @(dynamicWidth),
    @"dynamicHeight" : @(dynamicHeight),
    // The general reason stays clear when either feature works. This prevents
    // an iOS 26 requirement for wide framing from falsely describing Center
    // Stage as unavailable on older supported systems.
    @"reasonCode" : generalReasonCode ?: (id)NSNull.null,
    @"wideUprightReasonCode" : wideReasonCode ?: (id)NSNull.null,
    @"centerStageReasonCode" : centerStageReasonCode ?: (id)NSNull.null,
  };
}

+ (void)setCenterStageEnabled:(BOOL)enabled
                     deviceID:(NSString *)deviceID
                   completion:(BFResultCompletion)completion {
  AVCaptureDevice *device = [self deviceForID:deviceID];
  NSString *guardCode = [self guardCodeForDevice:device deviceID:deviceID requireIOS26:NO];
  NSDictionary *capabilities = [self capabilitiesForDeviceID:deviceID];
  if (guardCode != nil) {
    completion([self resultWithOK:NO code:guardCode message:[self messageForCode:guardCode] capabilities:capabilities]);
    return;
  }
  if (@available(iOS 14.5, *)) {
    if (![self centerStageSupportedByDevice:device] || !device.activeFormat.isCenterStageSupported) {
      completion([self resultWithOK:NO
                               code:BFCodeCenterStageUnsupported
                            message:@"The exact active camera format does not support Center Stage."
                       capabilities:capabilities]);
      return;
    }
    @try {
      AVCaptureDevice.centerStageControlMode = AVCaptureCenterStageControlModeCooperative;
      AVCaptureDevice.centerStageEnabled = enabled;
      completion([self resultWithOK:YES
                               code:BFCodeOK
                            message:enabled ? @"Center Stage is on and remains controllable from Control Center."
                                            : @"Center Stage is off and remains controllable from Control Center."
                       capabilities:[self capabilitiesForDeviceID:deviceID]]);
    } @catch (NSException *exception) {
      completion([self resultWithOK:NO
                               code:BFCodeConfigurationFailed
                            message:[NSString stringWithFormat:@"Center Stage could not be changed: %@",
                                                              exception.reason ?: @"unknown AVFoundation error"]
                       capabilities:[self capabilitiesForDeviceID:deviceID]]);
    }
    return;
  }
  completion([self resultWithOK:NO
                           code:BFCodeCenterStageUnsupported
                        message:@"Center Stage is unavailable on this version of iOS."
                   capabilities:capabilities]);
}

+ (void)setWideUprightFramingEnabled:(BOOL)enabled
                            deviceID:(NSString *)deviceID
                          completion:(BFResultCompletion)completion {
  if (deviceID.length == 0) {
    NSDictionary *capabilities = [self capabilitiesForDeviceID:deviceID];
    completion([self resultWithOK:NO
                             code:BFCodeMissingDeviceID
                          message:[self messageForCode:BFCodeMissingDeviceID]
                     capabilities:capabilities]);
    return;
  }

  BFWideFramingMutation *mutation = nil;
  BOOL shouldStart = NO;
  @synchronized(self) {
    BFWideFramingMutation *active = activeMutations[deviceID];
    NSMutableArray<BFWideFramingMutation *> *queue = queuedMutations[deviceID];

    if (active == nil) {
      mutation = [[BFWideFramingMutation alloc] initWithEnabled:enabled completion:completion];
      activeMutations[deviceID] = mutation;
      shouldStart = YES;
    } else if (queue.count == 0 && active.isEnabled == enabled) {
      [active addCompletion:completion];
    } else if (queue.lastObject != nil && queue.lastObject.isEnabled == enabled) {
      [queue.lastObject addCompletion:completion];
    } else {
      mutation = [[BFWideFramingMutation alloc] initWithEnabled:enabled completion:completion];
      if (queue == nil) {
        queue = [NSMutableArray array];
        queuedMutations[deviceID] = queue;
      }
      [queue addObject:mutation];
    }
  }

  if (shouldStart) {
    [self processWideMutation:mutation deviceID:deviceID];
  }
}

+ (void)processWideMutation:(BFWideFramingMutation *)mutation deviceID:(NSString *)deviceID {
  AVCaptureDevice *device = [self deviceForID:deviceID];
  NSString *guardCode = [self guardCodeForDevice:device deviceID:deviceID requireIOS26:YES];
  if (guardCode != nil) {
    [self finishWideMutation:mutation
                    deviceID:deviceID
                      result:[self resultWithOK:NO
                                              code:guardCode
                                           message:[self messageForCode:guardCode]
                                      capabilities:[self capabilitiesForDeviceID:deviceID]]];
    return;
  }

  if (@available(iOS 26.0, *)) {
    [self performWideMutation:mutation device:device deviceID:deviceID];
    return;
  }

  [self finishWideMutation:mutation
                  deviceID:deviceID
                    result:[self resultWithOK:NO
                                            code:BFCodeRequiresIOS26
                                         message:@"Wide upright framing requires iOS 26 or newer."
                                    capabilities:[self capabilitiesForDeviceID:deviceID]]];
}

+ (void)performWideMutation:(BFWideFramingMutation *)mutation
                     device:(AVCaptureDevice *)device
                   deviceID:(NSString *)deviceID API_AVAILABLE(ios(26.0)) {
  NSError *lockError = nil;
  if (![device lockForConfiguration:&lockError]) {
    [self finishWideMutation:mutation
                    deviceID:deviceID
                      result:[self resultWithOK:NO
                                              code:BFCodeConfigurationLockFailed
                                           message:[NSString stringWithFormat:@"The exact camera could not be locked: %@",
                                                                             lockError.localizedDescription ?: @"unknown error"]
                                      capabilities:[self capabilitiesForDeviceID:deviceID]]];
    return;
  }

  NSArray<AVCaptureAspectRatio> *supportedRatios = device.activeFormat.supportedDynamicAspectRatios;
  if (![supportedRatios containsObject:AVCaptureAspectRatio16x9]) {
    [device unlockForConfiguration];
    [self finishWideMutation:mutation
                    deviceID:deviceID
                      result:[self resultWithOK:NO
                                              code:BFCodeDynamicRatioUnsupported
                                           message:@"The exact active WebRTC format does not support dynamic 16:9; Stride left the format unchanged."
                                      capabilities:[self capabilitiesForDeviceID:deviceID]]];
    return;
  }

  AVCaptureAspectRatio currentRatio = device.dynamicAspectRatio;
  BOOL currentlyEnabled = [currentRatio isEqualToString:AVCaptureAspectRatio16x9];
  BOOL currentlyPortrait = [currentRatio isEqualToString:AVCaptureAspectRatio9x16];
  CMVideoDimensions currentDimensions = device.dynamicDimensions;
  BOOL currentDimensionsValid =
      currentDimensions.width > 0 && currentDimensions.height > 0;
  BOOL currentLandscapeDimensionsValid =
      currentDimensionsValid && currentDimensions.width > currentDimensions.height;
  BOOL currentPortraitDimensionsValid =
      currentDimensionsValid && currentDimensions.height > currentDimensions.width;
  // The adaptive front camera initializes to the first supported ratio. On
  // current iPhones that can be 1:1, which is neither Wide Upright nor a safe
  // portrait WebRTC output: the square sensor keeps reporting live while the
  // encoder stops producing frames. "Off" therefore means an explicitly
  // confirmed 9:16 portrait crop, never merely "not 16:9".
  BOOL confirmedAlreadyApplied = mutation.isEnabled
      ? currentlyEnabled && currentLandscapeDimensionsValid
      : currentlyPortrait && currentPortraitDimensionsValid;
  if (confirmedAlreadyApplied) {
    [device unlockForConfiguration];
    if (!mutation.isEnabled) {
      @synchronized(self) {
        [savedAspectRatios removeObjectForKey:deviceID];
      }
    }
    [self finishWideMutation:mutation
                    deviceID:deviceID
                      result:[self resultWithOK:YES
                                              code:BFCodeAlreadyApplied
                                           message:mutation.isEnabled ? @"Wide upright framing is already active."
                                                                      : @"Wide upright framing is already inactive."
                                      capabilities:[self capabilitiesForDeviceID:deviceID]]];
    return;
  }

  AVCaptureAspectRatio targetRatio = AVCaptureAspectRatio16x9;
  if (mutation.isEnabled) {
    if (currentlyPortrait) {
      @synchronized(self) {
        savedAspectRatios[deviceID] = currentRatio;
      }
    }
  } else {
    // Always prefer the portrait counterpart to Wide Upright. Restoring the
    // pre-call 1:1 default reintroduces the zero-frame capture state this guard
    // exists to prevent. Retain a previously confirmed portrait ratio only as
    // a defensive fallback for an unusual active format without 9:16.
    targetRatio = [supportedRatios containsObject:AVCaptureAspectRatio9x16]
        ? AVCaptureAspectRatio9x16
        : nil;
    if (targetRatio == nil) {
      @synchronized(self) {
        targetRatio = savedAspectRatios[deviceID];
      }
    }
    if (targetRatio == nil) {
      [device unlockForConfiguration];
      [self finishWideMutation:mutation
                      deviceID:deviceID
                        result:[self resultWithOK:NO
                                                code:BFCodeRestoreRatioUnavailable
                                             message:@"The active format has no safe prior or portrait aspect ratio to restore."
                                        capabilities:[self capabilitiesForDeviceID:deviceID]]];
      return;
    }
  }

  BFCameraConfigurationLease *lease = [[BFCameraConfigurationLease alloc] initWithLockedDevice:device];
  __weak AVCaptureDevice *weakLockedDevice = device;
  @synchronized(self) {
    lockedDevices[deviceID] = device;
  }

  // AVFoundation's contract says a non-nil handler is called when the ratio is
  // applied and that multiple handlers are delivered FIFO. Do not race that
  // contract with a timer that could unlock the device before Apple's handler.
  // The exact built-in front camera also cannot be physically unplugged; app
  // backgrounding or a WebRTC track teardown does not disconnect the device.
  @try {
    [device setDynamicAspectRatio:targetRatio
               completionHandler:^(__unused CMTime syncTime, NSError *error) {
      AVCaptureDevice *lockedDevice = nil;
      if (![lease claimFinishWithLockedDevice:&lockedDevice]) {
        return;
      }

      @synchronized(self) {
        lockedDevice = lockedDevices[deviceID] ?: lockedDevice;
      }

      // Normally this is the weakly-held original. Re-resolve the exact unique
      // ID if AVFoundation released that wrapper before invoking completion.
      AVCaptureDevice *completionDevice = lockedDevice ?: weakLockedDevice;
      if (completionDevice == nil) {
        completionDevice = [self deviceForID:deviceID];
      }

      BOOL applied = error == nil &&
          completionDevice != nil &&
          [completionDevice.dynamicAspectRatio isEqualToString:targetRatio];
      CMVideoDimensions dimensions = completionDevice != nil
          ? completionDevice.dynamicDimensions
          : (CMVideoDimensions){ 0, 0 };
      BOOL dimensionsMatchRequestedOrientation = mutation.isEnabled
          ? dimensions.width > dimensions.height
          : dimensions.height > dimensions.width;
      if (dimensions.width <= 0 || dimensions.height <= 0 ||
          !dimensionsMatchRequestedOrientation) {
        applied = NO;
      }

      // Match Apple's reference lifecycle: the lock remains held through the
      // async completion, then is released exactly once before callbacks query
      // capabilities or allow the next queued operation to begin.
      [lockedDevice unlockForConfiguration];
      @synchronized(self) {
        if (lockedDevices[deviceID] == lockedDevice) {
          [lockedDevices removeObjectForKey:deviceID];
        }
      }

      if (!applied) {
        NSString *message = error != nil
            ? [NSString stringWithFormat:@"Dynamic 16:9 could not be applied: %@", error.localizedDescription]
            : @"The camera did not confirm the requested framing dimensions.";
        [self finishWideMutation:mutation
                        deviceID:deviceID
                          result:[self resultWithOK:NO
                                                  code:BFCodeConfigurationFailed
                                               message:message
                                          capabilities:[self capabilitiesForDeviceID:deviceID]]];
        return;
      }

      if (!mutation.isEnabled) {
        @synchronized(self) {
          [savedAspectRatios removeObjectForKey:deviceID];
        }
      }
      [self finishWideMutation:mutation
                      deviceID:deviceID
                        result:[self resultWithOK:YES
                                                code:BFCodeOK
                                             message:mutation.isEnabled
                                                 ? @"Wide upright 16:9 is active on the exact camera."
                                                 : @"The camera's prior portrait framing is restored."
                                        capabilities:[self capabilitiesForDeviceID:deviceID]]];
    }];
  } @catch (NSException *exception) {
    AVCaptureDevice *lockedDevice = nil;
    BOOL ownsFinish = [lease claimFinishWithLockedDevice:&lockedDevice];
    if (ownsFinish) {
      @synchronized(self) {
        lockedDevice = lockedDevices[deviceID] ?: lockedDevice;
      }
      [lockedDevice unlockForConfiguration];
      @synchronized(self) {
        if (lockedDevices[deviceID] == lockedDevice) {
          [lockedDevices removeObjectForKey:deviceID];
        }
      }
      [self finishWideMutation:mutation
                      deviceID:deviceID
                        result:[self resultWithOK:NO
                                                code:BFCodeConfigurationFailed
                                             message:[NSString stringWithFormat:@"Dynamic 16:9 could not be applied: %@",
                                                                               exception.reason ?: @"unknown AVFoundation error"]
                                        capabilities:[self capabilitiesForDeviceID:deviceID]]];
    }
  }
}

+ (void)finishWideMutation:(BFWideFramingMutation *)mutation
                   deviceID:(NSString *)deviceID
                     result:(NSDictionary<NSString *, id> *)result {
  NSArray<BFResultCompletion> *completions = nil;
  BFWideFramingMutation *nextMutation = nil;
  @synchronized(self) {
    // Ignore a stale duplicate completion defensively; only the exact active
    // mutation can advance this device's FIFO.
    if (activeMutations[deviceID] != mutation) {
      return;
    }

    completions = [mutation.completions copy];
    NSMutableArray<BFWideFramingMutation *> *queue = queuedMutations[deviceID];
    if (queue.count > 0) {
      nextMutation = queue.firstObject;
      [queue removeObjectAtIndex:0];
      activeMutations[deviceID] = nextMutation;
      if (queue.count == 0) {
        [queuedMutations removeObjectForKey:deviceID];
      }
    } else {
      [activeMutations removeObjectForKey:deviceID];
      [queuedMutations removeObjectForKey:deviceID];
    }
  }

  for (BFResultCompletion completion in completions) {
    completion(result);
  }
  if (nextMutation != nil) {
    [self processWideMutation:nextMutation deviceID:deviceID];
  }
}

+ (AVCaptureDevice *)deviceForID:(NSString *)deviceID {
  if (deviceID.length == 0) {
    return nil;
  }
  return [AVCaptureDevice deviceWithUniqueID:deviceID];
}

+ (AVCaptureDevice *)frontUltraWideDevice {
  AVCaptureDeviceDiscoverySession *session =
      [AVCaptureDeviceDiscoverySession discoverySessionWithDeviceTypes:@[ AVCaptureDeviceTypeBuiltInUltraWideCamera ]
                                                             mediaType:AVMediaTypeVideo
                                                              position:AVCaptureDevicePositionFront];
  return session.devices.firstObject;
}

+ (NSString *)guardCodeForDevice:(AVCaptureDevice *)device
                        deviceID:(NSString *)deviceID
                    requireIOS26:(BOOL)requireIOS26 {
  if (deviceID.length == 0) {
    return BFCodeMissingDeviceID;
  }
  if (requireIOS26) {
    if (@available(iOS 26.0, *)) {
      // Continue with the common exact-device checks.
    } else {
      return BFCodeRequiresIOS26;
    }
  }
  if (device == nil) {
    return BFCodeDeviceUnavailable;
  }
  if (device.position != AVCaptureDevicePositionFront ||
      ![device.deviceType isEqualToString:AVCaptureDeviceTypeBuiltInUltraWideCamera]) {
    return BFCodeWrongActiveCamera;
  }
  return nil;
}

+ (BOOL)centerStageSupportedByDevice:(AVCaptureDevice *)device {
  if (device == nil) {
    return NO;
  }
  if (@available(iOS 14.5, *)) {
    for (AVCaptureDeviceFormat *format in device.formats) {
      if (format.isCenterStageSupported) {
        return YES;
      }
    }
  }
  return NO;
}

+ (BOOL)device:(AVCaptureDevice *)device hasFormatSupportingRatio:(AVCaptureAspectRatio)ratio
    API_AVAILABLE(ios(26.0)) {
  if (device == nil) {
    return NO;
  }
  for (AVCaptureDeviceFormat *format in device.formats) {
    if ([format.supportedDynamicAspectRatios containsObject:ratio]) {
      return YES;
    }
  }
  return NO;
}

+ (NSString *)nameForPosition:(AVCaptureDevicePosition)position {
  switch (position) {
    case AVCaptureDevicePositionFront:
      return @"front";
    case AVCaptureDevicePositionBack:
      return @"back";
    default:
      return @"unspecified";
  }
}

+ (NSString *)messageForCode:(NSString *)code {
  if ([code isEqualToString:BFCodeMissingDeviceID]) {
    return @"The current WebRTC camera identity is unavailable.";
  }
  if ([code isEqualToString:BFCodeDeviceUnavailable]) {
    return @"The selected WebRTC camera is no longer available.";
  }
  if ([code isEqualToString:BFCodeRequiresIOS26]) {
    return @"Wide upright framing requires iOS 26 or newer.";
  }
  if ([code isEqualToString:BFCodeWrongActiveCamera]) {
    return @"The selected WebRTC camera is not the supported front ultra-wide Center Stage camera.";
  }
  return @"The requested camera framing mode is unavailable in the current capture configuration.";
}

+ (NSDictionary<NSString *, id> *)resultWithOK:(BOOL)ok
                                           code:(NSString *)code
                                        message:(NSString *)message
                                   capabilities:(NSDictionary<NSString *, id> *)capabilities {
  return @{
    @"ok" : @(ok),
    @"code" : code,
    @"message" : message,
    @"capabilities" : capabilities,
  };
}

@end
