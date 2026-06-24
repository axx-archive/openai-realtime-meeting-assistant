import Foundation
import MeetingAssistCore

#if canImport(AVFoundation)
import AVFoundation
#endif

#if canImport(LiveKitWebRTC)
@preconcurrency import LiveKitWebRTC
#endif

public typealias LocalICECandidateHandler = @Sendable (RTCIceCandidatePayload) async -> Void
public typealias RemoteVideoTrackHandler = @Sendable (NativeRemoteVideoTrack) async -> Void

public final class NativeRemoteVideoTrack: Identifiable, @unchecked Sendable {
    public let id: String
    public let streamIds: [String]

    #if canImport(LiveKitWebRTC)
    fileprivate let track: LKRTCVideoTrack?

    public init(id: String, streamIds: [String] = []) {
        self.id = id
        self.streamIds = streamIds
        self.track = nil
    }

    fileprivate init(track: LKRTCVideoTrack, streamIds: [String]) {
        self.track = track
        self.streamIds = streamIds
        self.id = track.trackId
    }

    public func addRenderer(_ renderer: LKRTCVideoRenderer) {
        track?.add(renderer)
    }

    public func removeRenderer(_ renderer: LKRTCVideoRenderer) {
        track?.remove(renderer)
    }
    #else
    public init(id: String, streamIds: [String] = []) {
        self.id = id
        self.streamIds = streamIds
    }
    #endif
}

public protocol RoomRTCClient: AnyObject, Sendable {
    var lifecycle: RoomLifecycleState { get }
    func configure(_ config: ClientRTCConfig) async throws
    func setLocalCandidateHandler(_ handler: LocalICECandidateHandler?) async
    func setRemoteVideoTrackHandler(_ handler: RemoteVideoTrackHandler?) async
    func prepareLocalMedia(audio: Bool, video: Bool) async throws
    func setLocalAudioEnabled(_ enabled: Bool) async
    func setLocalVideoEnabled(_ enabled: Bool) async
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
    private var localVideoTrack: LKRTCVideoTrack?
    private var localVideoSource: LKRTCVideoSource?
    private var cameraCapturer: LKRTCCameraVideoCapturer?
    private var localCandidateHandler: LocalICECandidateHandler?
    private var remoteVideoTrackHandler: RemoteVideoTrackHandler?
    private var remoteVideoTracks: [String: NativeRemoteVideoTrack] = [:]

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
        let existingCapturer = lock.withLock { cameraCapturer }
        if let existingCapturer {
            await stopCapture(existingCapturer)
        }

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
            localVideoTrack = nil
            localVideoSource = nil
            cameraCapturer = nil
            remoteVideoTracks.removeAll()
            _lifecycle = .authenticated
        }
    }

    public func setLocalCandidateHandler(_ handler: LocalICECandidateHandler?) async {
        lock.withLock {
            localCandidateHandler = handler
        }
    }

    public func setRemoteVideoTrackHandler(_ handler: RemoteVideoTrackHandler?) async {
        let tracks = lock.withLock {
            remoteVideoTrackHandler = handler
            return Array(remoteVideoTracks.values)
        }
        guard let handler else { return }
        for track in tracks {
            await handler(track)
        }
    }

    public func prepareLocalMedia(audio: Bool, video: Bool) async throws {
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

        if video {
            try await prepareLocalVideo(on: connection)
        }

        setLifecycle(.preparingMedia)
    }

    public func setLocalAudioEnabled(_ enabled: Bool) async {
        lock.withLock {
            localAudioTrack?.isEnabled = enabled
        }
    }

    public func setLocalVideoEnabled(_ enabled: Bool) async {
        lock.withLock {
            localVideoTrack?.isEnabled = enabled
        }
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
        let capturer = lock.withLock { cameraCapturer }
        if let capturer {
            await stopCapture(capturer)
        }

        lock.withLock {
            peerConnection?.close()
            peerConnection = nil
            localAudioTrack = nil
            localVideoTrack = nil
            localVideoSource = nil
            cameraCapturer = nil
            localCandidateHandler = nil
            remoteVideoTrackHandler = nil
            remoteVideoTracks.removeAll()
            _lifecycle = .leaving
        }
    }

    private func prepareLocalVideo(on connection: LKRTCPeerConnection) async throws {
        guard let device = Self.preferredCameraDevice() else {
            throw RoomRTCError.cameraUnavailable
        }
        guard let format = Self.preferredFormat(for: device) else {
            throw RoomRTCError.cameraFormatUnavailable
        }

        let source = factory.videoSource()
        source.adaptOutputFormat(toWidth: 1280, height: 720, fps: 30)
        let capturer = LKRTCCameraVideoCapturer(delegate: source)
        let track = factory.videoTrack(with: source, trackId: "meetingassist-video-0")

        let fps = Self.preferredFPS(for: format)
        try await startCapture(capturer, device: device, format: format, fps: fps)
        guard connection.add(track, streamIds: ["meetingassist-native"]) != nil else {
            await stopCapture(capturer)
            throw RoomRTCError.trackPublicationFailed("video")
        }

        lock.withLock {
            localVideoSource = source
            localVideoTrack = track
            cameraCapturer = capturer
        }
    }

    private func startCapture(
        _ capturer: LKRTCCameraVideoCapturer,
        device: AVCaptureDevice,
        format: AVCaptureDevice.Format,
        fps: Int
    ) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            capturer.startCapture(with: device, format: format, fps: fps) { error in
                if let error {
                    continuation.resume(throwing: RoomRTCError.cameraCaptureFailed(error.localizedDescription))
                } else {
                    continuation.resume(returning: ())
                }
            }
        }
    }

    private func stopCapture(_ capturer: LKRTCCameraVideoCapturer) async {
        await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
            capturer.stopCapture {
                continuation.resume()
            }
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

    private static func preferredCameraDevice() -> AVCaptureDevice? {
        let devices = LKRTCCameraVideoCapturer.captureDevices()
        #if os(iOS)
        return devices.first(where: { $0.position == .front }) ?? devices.first
        #else
        return devices.first
        #endif
    }

    private static func preferredFormat(for device: AVCaptureDevice) -> AVCaptureDevice.Format? {
        LKRTCCameraVideoCapturer.supportedFormats(for: device).max { lhs, rhs in
            let lhsDimensions = CMVideoFormatDescriptionGetDimensions(lhs.formatDescription)
            let rhsDimensions = CMVideoFormatDescriptionGetDimensions(rhs.formatDescription)
            let lhsPixels = Int(lhsDimensions.width) * Int(lhsDimensions.height)
            let rhsPixels = Int(rhsDimensions.width) * Int(rhsDimensions.height)
            if lhsPixels == rhsPixels {
                return preferredFPS(for: lhs) < preferredFPS(for: rhs)
            }
            return lhsPixels < rhsPixels
        }
    }

    private static func preferredFPS(for format: AVCaptureDevice.Format) -> Int {
        let maxFPS = format.videoSupportedFrameRateRanges
            .map(\.maxFrameRate)
            .max() ?? 30
        return max(1, min(30, Int(maxFPS.rounded(.down))))
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

    public func peerConnection(
        _ peerConnection: LKRTCPeerConnection,
        didAdd receiver: LKRTCRtpReceiver,
        streams mediaStreams: [LKRTCMediaStream]
    ) {
        guard let videoTrack = receiver.track as? LKRTCVideoTrack else { return }
        let remoteTrack = NativeRemoteVideoTrack(
            track: videoTrack,
            streamIds: mediaStreams.map(\.streamId)
        )
        let handler = lock.withLock {
            remoteVideoTracks[remoteTrack.id] = remoteTrack
            return remoteVideoTrackHandler
        }

        guard let handler else { return }
        Task {
            await handler(remoteTrack)
        }
    }
}
#else
public final class NativeRoomRTCClient: RoomRTCClient, @unchecked Sendable {
    public private(set) var lifecycle: RoomLifecycleState = .signedOut

    public init() {}

    public func configure(_ config: ClientRTCConfig) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    public func setLocalCandidateHandler(_ handler: LocalICECandidateHandler?) async {}

    public func setRemoteVideoTrackHandler(_ handler: RemoteVideoTrackHandler?) async {}

    public func prepareLocalMedia(audio: Bool, video: Bool) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    public func setLocalAudioEnabled(_ enabled: Bool) async {}

    public func setLocalVideoEnabled(_ enabled: Bool) async {}

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
    case cameraCaptureFailed(String)
    case cameraFormatUnavailable
    case cameraUnavailable
    case missingSessionDescription
    case peerConnectionCreationFailed
    case peerConnectionNotConfigured
    case trackPublicationFailed(String)
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
