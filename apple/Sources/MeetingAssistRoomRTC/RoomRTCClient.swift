import Foundation
import MeetingAssistCore

#if canImport(WebRTC)
import WebRTC
#endif

public protocol RoomRTCClient: AnyObject, Sendable {
    var lifecycle: RoomLifecycleState { get }
    func prepareLocalMedia(audio: Bool, video: Bool) async throws
    func handleOffer(_ sdp: String) async throws -> String
    func addRemoteCandidate(_ json: String) async throws
    func restartICE() async
    func leave() async
}

public final class NativeRoomRTCClient: RoomRTCClient, @unchecked Sendable {
    public private(set) var lifecycle: RoomLifecycleState = .signedOut

    public init() {}

    public func prepareLocalMedia(audio: Bool, video: Bool) async throws {
        lifecycle = .preparingMedia
    }

    public func handleOffer(_ sdp: String) async throws -> String {
        lifecycle = .negotiating
        throw RoomRTCError.nativeWebRTCImplementationPending
    }

    public func addRemoteCandidate(_ json: String) async throws {}

    public func restartICE() async {
        lifecycle = .reconnecting
    }

    public func leave() async {
        lifecycle = .leaving
    }
}

public enum RoomRTCError: Error, Equatable {
    case nativeWebRTCImplementationPending
}

public enum WebRTCLinkStatus {
    public static var isWebRTCImportable: Bool {
        #if canImport(WebRTC)
        true
        #else
        false
        #endif
    }
}
