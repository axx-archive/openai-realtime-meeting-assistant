#import "BonfireWebRTCCameraCrashGuard.h"

#import <AVFoundation/AVFoundation.h>
#import <objc/message.h>
#import <objc/runtime.h>
#import <os/log.h>

typedef void (*BFWebRTCUpdatePixelFormatIMP)(
    id capturer,
    SEL selector,
    AVCaptureDeviceFormat *format);

static BFWebRTCUpdatePixelFormatIMP BFOriginalUpdatePixelFormat = NULL;
static BOOL BFWebRTCCameraCrashGuardInstalled = NO;
static NSUInteger BFWebRTCCameraCrashGuardInterventions = 0;
static NSString *BFWebRTCCameraCrashGuardLastReason = nil;

@interface BonfireWebRTCCameraCrashGuard ()

+ (BOOL)shouldOmitFixedOutputDimensionsForCapturer:(id)capturer
                                             format:(AVCaptureDeviceFormat *)format;
+ (void)recordIntervention:(NSString *)reason exception:(NSException * _Nullable)exception;

@end


static os_log_t BFWebRTCCameraCrashLog(void) {
  static os_log_t log;
  static dispatch_once_t onceToken;
  dispatch_once(&onceToken, ^{
    log = os_log_create("xyz.thebonfire.app", "WebRTCCameraCrashGuard");
  });
  return log;
}

static void BFGuardedUpdateVideoDataOutputPixelFormat(
    id capturer,
    SEL selector,
    AVCaptureDeviceFormat *format) {
  if ([BonfireWebRTCCameraCrashGuard
          shouldOmitFixedOutputDimensionsForCapturer:capturer
                                               format:format]) {
    // RTCCameraVideoCapturer already installed its preferred supported pixel
    // format when it created AVCaptureVideoDataOutput. The later M124 rewrite
    // exists for virtual desktop cameras; omitting its fixed width and height
    // lets iOS 26 own the adaptive front camera's dynamic output dimensions.
    [BonfireWebRTCCameraCrashGuard
        recordIntervention:@"adaptive_front_camera_omitted_fixed_output_dimensions"
                  exception:nil];
    return;
  }

  if (BFOriginalUpdatePixelFormat == NULL) {
    [BonfireWebRTCCameraCrashGuard
        recordIntervention:@"web_rtc_pixel_format_implementation_unavailable"
                  exception:nil];
    return;
  }

  @try {
    BFOriginalUpdatePixelFormat(capturer, selector, format);
  } @catch (NSException *exception) {
    // An Objective-C exception on RTCDispatcherCaptureSession otherwise
    // terminates the process with SIGABRT. Keep the output's already-valid
    // initial pixel-format settings and allow WebRTC to finish starting.
    [BonfireWebRTCCameraCrashGuard
        recordIntervention:@"web_rtc_output_settings_exception"
                  exception:exception];
  }
}


@implementation BonfireWebRTCCameraCrashGuard

+ (void)install {
  if (@available(iOS 26.0, *)) {
    @synchronized(self) {
      if (BFWebRTCCameraCrashGuardInstalled) {
        return;
      }

      Class capturerClass = NSClassFromString(@"RTCCameraVideoCapturer");
      SEL selector = NSSelectorFromString(@"updateVideoDataOutputPixelFormat:");
      Method method = capturerClass != Nil
          ? class_getInstanceMethod(capturerClass, selector)
          : NULL;
      if (method == NULL) {
        BFWebRTCCameraCrashGuardLastReason =
            @"web_rtc_pixel_format_method_unavailable";
        os_log_error(
            BFWebRTCCameraCrashLog(),
            "Could not install the iOS 26 WebRTC camera crash guard: method unavailable");
        return;
      }

      IMP currentImplementation = method_getImplementation(method);
      if (currentImplementation == (IMP)BFGuardedUpdateVideoDataOutputPixelFormat) {
        BFWebRTCCameraCrashGuardInstalled = YES;
        return;
      }

      BFOriginalUpdatePixelFormat =
          (BFWebRTCUpdatePixelFormatIMP)currentImplementation;
      method_setImplementation(
          method,
          (IMP)BFGuardedUpdateVideoDataOutputPixelFormat);
      BFWebRTCCameraCrashGuardInstalled = YES;
      BFWebRTCCameraCrashGuardLastReason = nil;
      os_log_info(
          BFWebRTCCameraCrashLog(),
          "Installed the iOS 26 WebRTC adaptive-camera crash guard");
    }
  }
}

+ (NSDictionary<NSString *, id> *)status {
  @synchronized(self) {
    return @{
      @"installed" : @(BFWebRTCCameraCrashGuardInstalled),
      @"interventions" : @(BFWebRTCCameraCrashGuardInterventions),
      @"lastReason" : BFWebRTCCameraCrashGuardLastReason ?: (id)NSNull.null,
    };
  }
}

+ (BOOL)shouldOmitFixedOutputDimensionsForCapturer:(id)capturer
                                             format:(AVCaptureDeviceFormat *)format {
  if (format == nil) {
    return NO;
  }
  if (@available(iOS 26.0, *)) {
    SEL captureSessionSelector = NSSelectorFromString(@"captureSession");
    if (![capturer respondsToSelector:captureSessionSelector]) {
      return NO;
    }

    AVCaptureSession *captureSession = nil;
    @try {
      captureSession = ((AVCaptureSession *(*)(id, SEL))objc_msgSend)(
          capturer,
          captureSessionSelector);
    } @catch (__unused NSException *exception) {
      return NO;
    }

    for (AVCaptureInput *input in captureSession.inputs) {
      if (![input isKindOfClass:AVCaptureDeviceInput.class]) {
        continue;
      }
      AVCaptureDevice *device = ((AVCaptureDeviceInput *)input).device;
      BOOL adaptiveFrontCamera =
          device.position == AVCaptureDevicePositionFront &&
          [device.deviceType
              isEqualToString:AVCaptureDeviceTypeBuiltInUltraWideCamera];
      BOOL adaptiveFormat =
          [format.supportedDynamicAspectRatios
              containsObject:AVCaptureAspectRatio16x9];
      if (adaptiveFrontCamera && adaptiveFormat) {
        return YES;
      }
    }
  }
  return NO;
}

+ (void)recordIntervention:(NSString *)reason exception:(NSException * _Nullable)exception {
  @synchronized(self) {
    BFWebRTCCameraCrashGuardInterventions += 1;
    BFWebRTCCameraCrashGuardLastReason = [reason copy];
  }

  if (exception != nil) {
    os_log_fault(
        BFWebRTCCameraCrashLog(),
        "Prevented WebRTC camera capture abort reason=%{public}@ exception=%{public}@",
        reason,
        exception.name);
  } else {
    os_log_info(
        BFWebRTCCameraCrashLog(),
        "Applied WebRTC camera capture guard reason=%{public}@",
        reason);
  }
}

@end
