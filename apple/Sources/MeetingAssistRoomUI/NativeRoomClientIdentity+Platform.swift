import Foundation
import MeetingAssistRoom
import MeetingAssistRoomRTC

#if os(iOS)
import UIKit
#endif

#if canImport(Darwin)
import Darwin
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

extension NativeMediaEvidenceAppContext {
    @MainActor
    public static var current: NativeMediaEvidenceAppContext {
        let bundle = Bundle.main
        let version = bundleValue("CFBundleShortVersionString", in: bundle)
        let build = bundleValue("CFBundleVersion", in: bundle)
        let client = NativeRoomClientIdentity.current
        return NativeMediaEvidenceAppContext(
            version: version,
            build: build,
            target: targetName,
            clientPlatform: client.platform,
            clientVersion: client.version
        )
    }

    private static var targetName: String {
        #if os(iOS)
        "MeetingAssistAppleApp"
        #elseif os(macOS)
        "MeetingAssistMacApp"
        #else
        "MeetingAssistApple"
        #endif
    }

    private static func bundleValue(_ key: String, in bundle: Bundle) -> String {
        guard let value = bundle.object(forInfoDictionaryKey: key) as? String else { return "" }
        return value.trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

extension NativeMediaEvidenceDeviceContext {
    @MainActor
    public static var current: NativeMediaEvidenceDeviceContext {
        #if os(iOS)
        let device = UIDevice.current
        let simulatorName = ProcessInfo.processInfo.environment["SIMULATOR_DEVICE_NAME"] ?? ""
        let simulatorIdentifier = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] ?? ""
        let hardware = hardwareModelIdentifier()
        let modelParts = [
            simulatorName,
            simulatorIdentifier,
            hardware.isEmpty ? device.model : hardware,
        ].filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
        #if targetEnvironment(simulator)
        let kind = "simulator"
        let physical = false
        #else
        let kind = device.userInterfaceIdiom == .pad ? "ipad" : "iphone"
        let physical = true
        #endif
        return NativeMediaEvidenceDeviceContext(
            kind: kind,
            model: modelParts.joined(separator: " "),
            os: "\(device.systemName) \(device.systemVersion)",
            physical: physical
        )
        #elseif os(macOS)
        let hardware = hardwareModelIdentifier()
        return NativeMediaEvidenceDeviceContext(
            kind: "mac",
            model: hardware,
            os: "macOS \(formattedOperatingSystemVersion())",
            physical: true
        )
        #else
        return NativeMediaEvidenceDeviceContext(
            kind: "apple",
            model: hardwareModelIdentifier(),
            os: ProcessInfo.processInfo.operatingSystemVersionString,
            physical: false
        )
        #endif
    }
}

extension NativeMediaEvidenceCaptureContext {
    @MainActor
    public static var current: NativeMediaEvidenceCaptureContext {
        NativeMediaEvidenceCaptureContext(
            app: NativeMediaEvidenceAppContext.current,
            device: NativeMediaEvidenceDeviceContext.current
        )
    }
}

private func formattedOperatingSystemVersion() -> String {
    let version = ProcessInfo.processInfo.operatingSystemVersion
    return "\(version.majorVersion).\(version.minorVersion).\(version.patchVersion)"
}

private func hardwareModelIdentifier() -> String {
    #if os(macOS)
    var size = 0
    guard sysctlbyname("hw.model", nil, &size, nil, 0) == 0, size > 0 else { return "" }
    var model = [CChar](repeating: 0, count: size)
    guard sysctlbyname("hw.model", &model, &size, nil, 0) == 0 else { return "" }
    let bytes = model.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }
    return String(decoding: bytes, as: UTF8.self)
    #elseif canImport(Darwin)
    var systemInfo = utsname()
    uname(&systemInfo)
    let mirror = Mirror(reflecting: systemInfo.machine)
    let identifier = mirror.children.reduce(into: "") { result, element in
        guard let value = element.value as? Int8, value != 0 else { return }
        result.append(Character(UnicodeScalar(UInt8(value))))
    }
    return identifier
    #else
    return ""
    #endif
}
