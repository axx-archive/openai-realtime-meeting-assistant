#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/// Objective-C owns the AVFoundation exception boundary. Swift cannot safely
/// catch the NSExceptions documented by setDynamicAspectRatio.
@interface BonfireCameraDeviceGuard : NSObject

+ (NSDictionary<NSString *, id> *)capabilitiesForDeviceID:(NSString *)deviceID
    NS_SWIFT_NAME(capabilities(deviceID:));

+ (void)setCenterStageEnabled:(BOOL)enabled
                     deviceID:(NSString *)deviceID
                   completion:(void (^)(NSDictionary<NSString *, id> *result))completion
    NS_SWIFT_NAME(setCenterStage(enabled:deviceID:completion:));

+ (void)setWideUprightFramingEnabled:(BOOL)enabled
                            deviceID:(NSString *)deviceID
                          completion:(void (^)(NSDictionary<NSString *, id> *result))completion
    NS_SWIFT_NAME(setWideUprightFraming(enabled:deviceID:completion:));

@end

NS_ASSUME_NONNULL_END
