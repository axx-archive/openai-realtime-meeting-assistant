import Foundation
import MeetingAssistRoom

#if os(iOS)
import UIKit
#endif

extension NativeRoomClientIdentity {
    @MainActor
    public static var current: NativeRoomClientIdentity {
        NativeRoomClientIdentity(platform: platformName, version: appVersion)
    }

    @MainActor
    private static var platformName: String {
        #if os(iOS)
        UIDevice.current.userInterfaceIdiom == .pad ? "ipados" : "ios"
        #elseif os(macOS)
        "macos"
        #else
        "apple"
        #endif
    }

    private static var appVersion: String {
        let bundle = Bundle.main
        let version = bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
        let build = bundle.object(forInfoDictionaryKey: "CFBundleVersion") as? String
        return [version, build]
            .compactMap { value in
                guard let value, !value.isEmpty else { return nil }
                return value
            }
            .joined(separator: " ")
    }
}
