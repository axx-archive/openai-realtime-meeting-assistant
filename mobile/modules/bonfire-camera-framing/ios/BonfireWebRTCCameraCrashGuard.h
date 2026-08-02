#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/// Installs a process-local exception boundary around the bundled M124
/// RTCCameraVideoCapturer output-settings update. iOS 26 adaptive front-camera
/// formats can reject the fixed output dimensions that M124 applies, raising an
/// Objective-C exception on WebRTC's capture queue before JavaScript can react.
@interface BonfireWebRTCCameraCrashGuard : NSObject

+ (void)install;
+ (NSDictionary<NSString *, id> *)status;

@end

NS_ASSUME_NONNULL_END
