import Foundation
import MeetingAssistCore

#if canImport(LiveKitWebRTC)
@preconcurrency import LiveKitWebRTC
#endif

public typealias LocalICECandidateHandler = @Sendable (RTCIceCandidatePayload) async -> Void

public protocol RoomRTCClient: AnyObject, Sendable {
    var lifecycle: RoomLifecycleState { get }
    func configure(_ config: ClientRTCConfig) async throws
    func setLocalCandidateHandler(_ handler: LocalICECandidateHandler?) async
    func prepareLocalMedia(audio: Bool, video: Bool) async throws
    func handleOffer(_ sdp: String) async throws -> String
    func addRemoteCandidate(_ json: String) async throws
    func restartICE() async
    func leave() async
}

#if canImport(LiveKitWebRTC)
public final class NativeRoomRTCClient: NSObject, RoomRTCClient, @unchecked Sendable {
    private let factory: LKRTCPeerConnectionFactory
    private let lock = NSLock()
    private let decoder = JSONDecoder()
    private var _lifecycle: RoomLifecycleState = .signedOut
    private var peerConnection: LKRTCPeerConnection?
    private var localAudioTrack: LKRTCAudioTrack?
    private var localCandidateHandler: LocalICECandidateHandler?

    public var lifecycle: RoomLifecycleState {
        lock.withLock { _lifecycle }
    }

    public override init() {
        _ = LKRTCInitializeSSL()
        self.factory = LKRTCPeerConnectionFactory(
            encoderFactory: LKRTCDefaultVideoEncoderFactory(),
            decoderFactory: LKRTCDefaultVideoDecoderFactory()
        )
        super.init()
    }

    public func configure(_ config: ClientRTCConfig) async throws {
        let rtcConfiguration = LKRTCConfiguration()
        rtcConfiguration.iceServers = Self.iceServers(from: config.rtcConfiguration)
        rtcConfiguration.sdpSemantics = .unifiedPlan
        rtcConfiguration.continualGatheringPolicy = .gatherContinually
        rtcConfiguration.bundlePolicy = .maxBundle
        rtcConfiguration.rtcpMuxPolicy = .require

        let constraints = LKRTCMediaConstraints(
            mandatoryConstraints: nil,
            optionalConstraints: ["DtlsSrtpKeyAgreement": kLKRTCMediaConstraintsValueTrue]
        )

        guard let connection = factory.peerConnection(
            with: rtcConfiguration,
            constraints: constraints,
            delegate: self
        ) else {
            throw RoomRTCError.peerConnectionCreationFailed
        }

        lock.withLock {
            peerConnection?.close()
            peerConnection = connection
            localAudioTrack = nil
            _lifecycle = .authenticated
        }
    }

    public func setLocalCandidateHandler(_ handler: LocalICECandidateHandler?) async {
        lock.withLock {
            localCandidateHandler = handler
        }
    }

    public func prepareLocalMedia(audio: Bool, video: Bool) async throws {
        guard !video else { throw RoomRTCError.nativeVideoCapturePending }
        guard let connection = lock.withLock({ peerConnection }) else {
            throw RoomRTCError.peerConnectionNotConfigured
        }

        if audio {
            let track = factory.audioTrack(withTrackId: "meetingassist-audio-0")
            _ = connection.add(track, streamIds: ["meetingassist-native"])
            lock.withLock {
                localAudioTrack = track
            }
        }

        setLifecycle(.preparingMedia)
    }

    public func handleOffer(_ sdp: String) async throws -> String {
        guard let connection = lock.withLock({ peerConnection }) else {
            throw RoomRTCError.peerConnectionNotConfigured
        }

        setLifecycle(.negotiating)
        let offer = LKRTCSessionDescription(type: .offer, sdp: sdp)
        try await setRemoteDescription(offer, on: connection)
        let answer = try await answer(on: connection)
        try await setLocalDescription(answer, on: connection)
        setLifecycle(.connected)
        return answer.sdp
    }

    public func addRemoteCandidate(_ json: String) async throws {
        guard let connection = lock.withLock({ peerConnection }) else {
            throw RoomRTCError.peerConnectionNotConfigured
        }

        let payload = try decoder.decode(RTCIceCandidatePayload.self, from: Data(json.utf8))
        let candidate = LKRTCIceCandidate(
            sdp: payload.candidate,
            sdpMLineIndex: Int32(payload.sdpMLineIndex ?? 0),
            sdpMid: payload.sdpMid
        )
        try await add(candidate, to: connection)
    }

    public func restartICE() async {
        let connection = lock.withLock { peerConnection }
        connection?.restartIce()
        setLifecycle(.reconnecting)
    }

    public func leave() async {
        lock.withLock {
            peerConnection?.close()
            peerConnection = nil
            localAudioTrack = nil
            localCandidateHandler = nil
            _lifecycle = .leaving
        }
    }

    private func answer(on connection: LKRTCPeerConnection) async throws -> LKRTCSessionDescription {
        let constraints = LKRTCMediaConstraints(
            mandatoryConstraints: [
                kLKRTCMediaConstraintsOfferToReceiveAudio: kLKRTCMediaConstraintsValueTrue,
                kLKRTCMediaConstraintsOfferToReceiveVideo: kLKRTCMediaConstraintsValueTrue
            ],
            optionalConstraints: nil
        )

        return try await withCheckedThrowingContinuation { continuation in
            connection.answer(for: constraints) { description, error in
                if let error {
                    continuation.resume(throwing: RoomRTCError.webRTCOperationFailed(error.localizedDescription))
                } else if let description {
                    continuation.resume(returning: description)
                } else {
                    continuation.resume(throwing: RoomRTCError.missingSessionDescription)
                }
            }
        }
    }

    private func setRemoteDescription(_ description: LKRTCSessionDescription, on connection: LKRTCPeerConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            connection.setRemoteDescription(description) { error in
                if let error {
                    continuation.resume(throwing: RoomRTCError.webRTCOperationFailed(error.localizedDescription))
                } else {
                    continuation.resume(returning: ())
                }
            }
        }
    }

    private func setLocalDescription(_ description: LKRTCSessionDescription, on connection: LKRTCPeerConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            connection.setLocalDescription(description) { error in
                if let error {
                    continuation.resume(throwing: RoomRTCError.webRTCOperationFailed(error.localizedDescription))
                } else {
                    continuation.resume(returning: ())
                }
            }
        }
    }

    private func add(_ candidate: LKRTCIceCandidate, to connection: LKRTCPeerConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            connection.add(candidate) { error in
                if let error {
                    continuation.resume(throwing: RoomRTCError.webRTCOperationFailed(error.localizedDescription))
                } else {
                    continuation.resume(returning: ())
                }
            }
        }
    }

    private func setLifecycle(_ state: RoomLifecycleState) {
        lock.withLock {
            _lifecycle = state
        }
    }

    private static func iceServers(from rtcConfiguration: [String: JSONValue]) -> [LKRTCIceServer] {
        guard case .array(let servers)? = rtcConfiguration["iceServers"] else { return [] }
        return servers.compactMap { value in
            guard case .object(let server) = value else { return nil }
            let urls = stringList(from: server["urls"])
            guard !urls.isEmpty else { return nil }
            let username = string(from: server["username"])
            let credential = string(from: server["credential"])
            if username != nil || credential != nil {
                return LKRTCIceServer(urlStrings: urls, username: username, credential: credential)
            }
            return LKRTCIceServer(urlStrings: urls)
        }
    }

    private static func stringList(from value: JSONValue?) -> [String] {
        switch value {
        case .string(let string):
            return [string]
        case .array(let values):
            return values.compactMap { item in
                if case .string(let string) = item { return string }
                return nil
            }
        default:
            return []
        }
    }

    private static func string(from value: JSONValue?) -> String? {
        if case .string(let string) = value {
            return string
        }
        return nil
    }
}

extension NativeRoomRTCClient: LKRTCPeerConnectionDelegate {
    public func peerConnection(_ peerConnection: LKRTCPeerConnection, didChange stateChanged: LKRTCSignalingState) {}

    public func peerConnection(_ peerConnection: LKRTCPeerConnection, didAdd stream: LKRTCMediaStream) {}

    public func peerConnection(_ peerConnection: LKRTCPeerConnection, didRemove stream: LKRTCMediaStream) {}

    public func peerConnectionShouldNegotiate(_ peerConnection: LKRTCPeerConnection) {}

    public func peerConnection(_ peerConnection: LKRTCPeerConnection, didChange newState: LKRTCIceConnectionState) {}

    public func peerConnection(_ peerConnection: LKRTCPeerConnection, didChange newState: LKRTCIceGatheringState) {}

    public func peerConnection(_ peerConnection: LKRTCPeerConnection, didGenerate candidate: LKRTCIceCandidate) {
        let handler = lock.withLock { localCandidateHandler }
        let payload = RTCIceCandidatePayload(
            candidate: candidate.sdp,
            sdpMid: candidate.sdpMid,
            sdpMLineIndex: Int(candidate.sdpMLineIndex)
        )

        guard let handler else { return }
        Task {
            await handler(payload)
        }
    }

    public func peerConnection(_ peerConnection: LKRTCPeerConnection, didRemove candidates: [LKRTCIceCandidate]) {}

    public func peerConnection(_ peerConnection: LKRTCPeerConnection, didOpen dataChannel: LKRTCDataChannel) {}
}
#else
public final class NativeRoomRTCClient: RoomRTCClient, @unchecked Sendable {
    public private(set) var lifecycle: RoomLifecycleState = .signedOut

    public init() {}

    public func configure(_ config: ClientRTCConfig) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    public func setLocalCandidateHandler(_ handler: LocalICECandidateHandler?) async {}

    public func prepareLocalMedia(audio: Bool, video: Bool) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    public func handleOffer(_ sdp: String) async throws -> String {
        throw RoomRTCError.webRTCUnavailable
    }

    public func addRemoteCandidate(_ json: String) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    public func restartICE() async {
        lifecycle = .reconnecting
    }

    public func leave() async {
        lifecycle = .leaving
    }
}
#endif

public enum RoomRTCError: Error, Equatable {
    case missingSessionDescription
    case nativeVideoCapturePending
    case peerConnectionCreationFailed
    case peerConnectionNotConfigured
    case webRTCOperationFailed(String)
    case webRTCUnavailable
}

public enum WebRTCLinkStatus {
    public static var isWebRTCImportable: Bool {
        #if canImport(LiveKitWebRTC)
        true
        #else
        false
        #endif
    }
}
