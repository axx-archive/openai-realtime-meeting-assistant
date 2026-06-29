import Foundation
import MeetingAssistCore

#if canImport(AVFoundation)
import AVFoundation
#endif

#if os(macOS)
import CoreGraphics
#endif

#if canImport(LiveKitWebRTC)
@preconcurrency import LiveKitWebRTC
#endif

public typealias LocalICECandidateHandler = @Sendable (RTCIceCandidatePayload) async -> Void
public typealias RemoteVideoTrackHandler = @Sendable (NativeRemoteVideoTrack) async -> Void

public struct NativeMediaQualityCandidatePair: Codable, Equatable, Sendable {
    public var `protocol`: String
    public var networkType: String
    public var localCandidateType: String
    public var remoteCandidateType: String
    public var availableOutgoingBitrate: Double
    public var currentRoundTripTime: Double

    public init(
        protocol: String = "",
        networkType: String = "",
        localCandidateType: String = "",
        remoteCandidateType: String = "",
        availableOutgoingBitrate: Double = 0,
        currentRoundTripTime: Double = 0
    ) {
        self.protocol = `protocol`
        self.networkType = networkType
        self.localCandidateType = localCandidateType
        self.remoteCandidateType = remoteCandidateType
        self.availableOutgoingBitrate = availableOutgoingBitrate
        self.currentRoundTripTime = currentRoundTripTime
    }
}

public struct NativeMediaQualityDeltas: Codable, Equatable, Sendable {
    public var outboundAudioBytesSent: Double?
    public var outboundAudioPacketsSent: Double?
    public var outboundVideoBytesSent: Double?
    public var outboundVideoFramesSent: Double?
    public var inboundAudioPacketsLost: Double?
    public var inboundAudioPacketsReceived: Double?
    public var inboundVideoPacketsLost: Double?
    public var inboundVideoPacketsReceived: Double?
    public var inboundVideoDecoded: Double?
    public var inboundVideoDrops: Double?
    public var elapsedMs: Double?

    public init(
        outboundAudioBytesSent: Double? = nil,
        outboundAudioPacketsSent: Double? = nil,
        outboundVideoBytesSent: Double? = nil,
        outboundVideoFramesSent: Double? = nil,
        inboundAudioPacketsLost: Double? = nil,
        inboundAudioPacketsReceived: Double? = nil,
        inboundVideoPacketsLost: Double? = nil,
        inboundVideoPacketsReceived: Double? = nil,
        inboundVideoDecoded: Double? = nil,
        inboundVideoDrops: Double? = nil,
        elapsedMs: Double? = nil
    ) {
        self.outboundAudioBytesSent = outboundAudioBytesSent
        self.outboundAudioPacketsSent = outboundAudioPacketsSent
        self.outboundVideoBytesSent = outboundVideoBytesSent
        self.outboundVideoFramesSent = outboundVideoFramesSent
        self.inboundAudioPacketsLost = inboundAudioPacketsLost
        self.inboundAudioPacketsReceived = inboundAudioPacketsReceived
        self.inboundVideoPacketsLost = inboundVideoPacketsLost
        self.inboundVideoPacketsReceived = inboundVideoPacketsReceived
        self.inboundVideoDecoded = inboundVideoDecoded
        self.inboundVideoDrops = inboundVideoDrops
        self.elapsedMs = elapsedMs
    }
}

public struct NativeMediaQualitySnapshot: Codable, Equatable, Sendable {
    public var at: Double
    public var outboundAudioBytesSent: Double
    public var outboundAudioPacketsSent: Double
    public var outboundVideoBytesSent: Double
    public var outboundVideoFramesEncoded: Double
    public var outboundVideoFramesSent: Double
    public var inboundAudioJitter: Double
    public var inboundAudioLost: Double
    public var inboundAudioPacketsReceived: Double
    public var inboundVideoJitter: Double
    public var inboundVideoLost: Double
    public var inboundVideoPacketsReceived: Double
    public var inboundVideoDrops: Double
    public var inboundVideoDecoded: Double
    public var outboundRtt: Double
    public var candidatePair: NativeMediaQualityCandidatePair

    public init(
        at: Double = 0,
        outboundAudioBytesSent: Double = 0,
        outboundAudioPacketsSent: Double = 0,
        outboundVideoBytesSent: Double = 0,
        outboundVideoFramesEncoded: Double = 0,
        outboundVideoFramesSent: Double = 0,
        inboundAudioJitter: Double = 0,
        inboundAudioLost: Double = 0,
        inboundAudioPacketsReceived: Double = 0,
        inboundVideoJitter: Double = 0,
        inboundVideoLost: Double = 0,
        inboundVideoPacketsReceived: Double = 0,
        inboundVideoDrops: Double = 0,
        inboundVideoDecoded: Double = 0,
        outboundRtt: Double = 0,
        candidatePair: NativeMediaQualityCandidatePair = NativeMediaQualityCandidatePair()
    ) {
        self.at = at
        self.outboundAudioBytesSent = outboundAudioBytesSent
        self.outboundAudioPacketsSent = outboundAudioPacketsSent
        self.outboundVideoBytesSent = outboundVideoBytesSent
        self.outboundVideoFramesEncoded = outboundVideoFramesEncoded
        self.outboundVideoFramesSent = outboundVideoFramesSent
        self.inboundAudioJitter = inboundAudioJitter
        self.inboundAudioLost = inboundAudioLost
        self.inboundAudioPacketsReceived = inboundAudioPacketsReceived
        self.inboundVideoJitter = inboundVideoJitter
        self.inboundVideoLost = inboundVideoLost
        self.inboundVideoPacketsReceived = inboundVideoPacketsReceived
        self.inboundVideoDrops = inboundVideoDrops
        self.inboundVideoDecoded = inboundVideoDecoded
        self.outboundRtt = outboundRtt
        self.candidatePair = candidatePair
    }

    public func deltas(since previous: NativeMediaQualitySnapshot?) -> NativeMediaQualityDeltas {
        guard let previous else { return NativeMediaQualityDeltas() }
        return NativeMediaQualityDeltas(
            outboundAudioBytesSent: outboundAudioBytesSent - previous.outboundAudioBytesSent,
            outboundAudioPacketsSent: outboundAudioPacketsSent - previous.outboundAudioPacketsSent,
            outboundVideoBytesSent: outboundVideoBytesSent - previous.outboundVideoBytesSent,
            outboundVideoFramesSent: outboundVideoFramesSent - previous.outboundVideoFramesSent,
            inboundAudioPacketsLost: inboundAudioLost - previous.inboundAudioLost,
            inboundAudioPacketsReceived: inboundAudioPacketsReceived - previous.inboundAudioPacketsReceived,
            inboundVideoPacketsLost: inboundVideoLost - previous.inboundVideoLost,
            inboundVideoPacketsReceived: inboundVideoPacketsReceived - previous.inboundVideoPacketsReceived,
            inboundVideoDecoded: inboundVideoDecoded - previous.inboundVideoDecoded,
            inboundVideoDrops: inboundVideoDrops - previous.inboundVideoDrops,
            elapsedMs: at - previous.at
        )
    }
}

struct NativeRTCStatisticsEntry: Equatable, Sendable {
    var id: String
    var type: String
    var timestampUs: Double
    var values: [String: JSONValue]

    init(id: String, type: String, timestampUs: Double, values: [String: JSONValue]) {
        self.id = id
        self.type = type
        self.timestampUs = timestampUs
        self.values = values
    }
}

struct NativeICEServerDescriptor: Equatable, Sendable {
    var urls: [String]
    var username: String?
    var credential: String?

    static func parse(from rtcConfiguration: [String: JSONValue]) -> [NativeICEServerDescriptor] {
        guard case .array(let servers)? = rtcConfiguration["iceServers"] else { return [] }
        return servers.compactMap { value in
            guard case .object(let server) = value else { return nil }
            let urls = stringList(from: server["urls"])
            guard !urls.isEmpty else { return nil }
            return NativeICEServerDescriptor(
                urls: urls,
                username: nonEmptyString(from: server["username"]),
                credential: nonEmptyString(from: server["credential"])
            )
        }
    }

    var isTurnRelay: Bool {
        urls.contains { url in
            let normalized = url.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            return normalized.hasPrefix("turn:") || normalized.hasPrefix("turns:")
        }
    }

    private static func stringList(from value: JSONValue?) -> [String] {
        switch value {
        case .string(let string):
            return normalizedStrings([string])
        case .array(let values):
            return normalizedStrings(values.compactMap { item in
                if case .string(let string) = item { return string }
                return nil
            })
        default:
            return []
        }
    }

    private static func normalizedStrings(_ values: [String]) -> [String] {
        values
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    private static func nonEmptyString(from value: JSONValue?) -> String? {
        guard case .string(let string) = value else { return nil }
        let trimmed = string.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }
}

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
    func setScreenShareEnabled(_ enabled: Bool) async throws
    func handleOffer(_ sdp: String) async throws -> String
    func addRemoteCandidate(_ json: String) async throws
    func restartICE() async
    func mediaQualitySnapshot() async throws -> NativeMediaQualitySnapshot
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
    private var localVideoSender: LKRTCRtpSender?
    #if os(macOS)
    private var screenVideoSource: LKRTCVideoSource?
    private var screenVideoTrack: LKRTCVideoTrack?
    private var desktopCapturer: LKRTCDesktopCapturer?
    private let screenShareTrackSwitch: NativeScreenShareTrackSwitch
    #endif
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
        #if os(macOS)
        self.screenShareTrackSwitch = NativeScreenShareTrackSwitch()
        #endif
        super.init()
    }

    public func configure(_ config: ClientRTCConfig) async throws {
        let existingCapturer = lock.withLock { cameraCapturer }
        if let existingCapturer {
            await stopCapture(existingCapturer)
        }
        #if os(macOS)
        let existingDesktopCapturer: LKRTCDesktopCapturer? = lock.withLock { self.desktopCapturer }
        if let existingDesktopCapturer {
            await stopDesktopCapture(existingDesktopCapturer)
        }
        #endif

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
            localVideoSender = nil
            #if os(macOS)
            screenVideoSource = nil
            screenVideoTrack = nil
            desktopCapturer = nil
            #endif
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

    public func setScreenShareEnabled(_ enabled: Bool) async throws {
        #if os(macOS)
        if enabled {
            try await startScreenShare()
        } else {
            await stopScreenShare()
        }
        #else
        if enabled {
            throw RoomRTCError.screenShareUnavailable
        }
        #endif
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

    public func mediaQualitySnapshot() async throws -> NativeMediaQualitySnapshot {
        guard let connection = lock.withLock({ peerConnection }) else {
            throw RoomRTCError.peerConnectionNotConfigured
        }

        let report = await withCheckedContinuation { (continuation: CheckedContinuation<LKRTCStatisticsReport, Never>) in
            connection.statistics { report in
                continuation.resume(returning: report)
            }
        }
        return Self.mediaQualitySnapshot(from: Self.statisticsEntries(from: report))
    }

    public func leave() async {
        let capturer = lock.withLock { cameraCapturer }
        if let capturer {
            await stopCapture(capturer)
        }
        #if os(macOS)
        let existingDesktopCapturer: LKRTCDesktopCapturer? = lock.withLock { self.desktopCapturer }
        if let existingDesktopCapturer {
            await stopDesktopCapture(existingDesktopCapturer)
        }
        #endif

        lock.withLock {
            peerConnection?.close()
            peerConnection = nil
            localAudioTrack = nil
            localVideoTrack = nil
            localVideoSource = nil
            cameraCapturer = nil
            localVideoSender = nil
            #if os(macOS)
            screenVideoSource = nil
            screenVideoTrack = nil
            desktopCapturer = nil
            #endif
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
        guard let sender = connection.add(track, streamIds: ["meetingassist-native"]) else {
            await stopCapture(capturer)
            throw RoomRTCError.trackPublicationFailed("video")
        }

        lock.withLock {
            localVideoSource = source
            localVideoTrack = track
            cameraCapturer = capturer
            localVideoSender = sender
        }
    }

    #if os(macOS)
    private func startScreenShare() async throws {
        guard let sender = lock.withLock({ localVideoSender }) else {
            throw RoomRTCError.screenShareUnavailable
        }
        if lock.withLock({ desktopCapturer != nil }) {
            return
        }

        let bundle = try screenShareTrackSwitch.start(
            makeScreenTrack: { [factory] () -> NativeDesktopScreenShareBundle in
                let source = factory.videoSource()
                source.adaptOutputFormat(toWidth: 1920, height: 1080, fps: 15)
                let capturer = LKRTCDesktopCapturer(defaultScreen: self, capture: source)
                let track = factory.videoTrack(with: source, trackId: "meetingassist-screen-0")
                track.isEnabled = true
                return NativeDesktopScreenShareBundle(source: source, capturer: capturer, track: track)
            },
            installScreenTrack: { bundle in
                sender.track = bundle.track
            },
            startCapture: { bundle in
                bundle.capturer.startCapture(withFPS: 15)
            }
        )

        lock.withLock {
            screenVideoSource = bundle.source
            screenVideoTrack = bundle.track
            desktopCapturer = bundle.capturer
        }
    }

    private func stopScreenShare() async {
        let state = lock.withLock {
            (
                sender: localVideoSender,
                cameraTrack: localVideoTrack,
                capturer: desktopCapturer
            )
        }

        await screenShareTrackSwitch.stop(
            cameraTrack: state.cameraTrack,
            capturer: state.capturer,
            restoreCameraTrack: { cameraTrack in
                state.sender?.track = cameraTrack
            },
            stopCapture: { capturer in
                await stopDesktopCapture(capturer)
            }
        )

        lock.withLock {
            screenVideoSource = nil
            screenVideoTrack = nil
            desktopCapturer = nil
        }
    }

    private func stopDesktopCapture(_ capturer: LKRTCDesktopCapturer) async {
        await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
            capturer.stopCapture {
                continuation.resume()
            }
        }
    }

    fileprivate static func screenCaptureAccessGranted() -> Bool {
        if #available(macOS 10.15, *) {
            if CGPreflightScreenCaptureAccess() {
                return true
            }
            return CGRequestScreenCaptureAccess()
        }
        return true
    }
    #endif

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
        NativeICEServerDescriptor.parse(from: rtcConfiguration).map { server in
            if server.username != nil || server.credential != nil {
                return LKRTCIceServer(
                    urlStrings: server.urls,
                    username: server.username,
                    credential: server.credential
                )
            }
            return LKRTCIceServer(urlStrings: server.urls)
        }
    }

    static func mediaQualitySnapshot(from entries: [NativeRTCStatisticsEntry]) -> NativeMediaQualitySnapshot {
        var snapshot = NativeMediaQualitySnapshot(
            at: entries.map(\.timestampUs).max().map { $0 / 1_000 } ?? 0
        )
        let entriesByID = Dictionary(uniqueKeysWithValues: entries.map { ($0.id, $0) })
        var selectedCandidatePair: NativeRTCStatisticsEntry?

        for entry in entries {
            switch entry.type {
            case "transport":
                if let selectedPairID = stringValue(entry, "selectedCandidatePairId"),
                   let pair = entriesByID[selectedPairID] {
                    selectedCandidatePair = pair
                }
            case "inbound-rtp":
                switch mediaKind(entry) {
                case "audio":
                    snapshot.inboundAudioJitter = max(snapshot.inboundAudioJitter, numberValue(entry, "jitter"))
                    snapshot.inboundAudioLost += numberValue(entry, "packetsLost")
                    snapshot.inboundAudioPacketsReceived += numberValue(entry, "packetsReceived")
                case "video":
                    snapshot.inboundVideoJitter = max(snapshot.inboundVideoJitter, numberValue(entry, "jitter"))
                    snapshot.inboundVideoLost += numberValue(entry, "packetsLost")
                    snapshot.inboundVideoPacketsReceived += numberValue(entry, "packetsReceived")
                    snapshot.inboundVideoDrops += numberValue(entry, "framesDropped")
                    snapshot.inboundVideoDecoded += numberValue(entry, "framesDecoded")
                default:
                    break
                }
            case "outbound-rtp":
                switch mediaKind(entry) {
                case "audio":
                    snapshot.outboundAudioBytesSent += numberValue(entry, "bytesSent")
                    snapshot.outboundAudioPacketsSent += numberValue(entry, "packetsSent")
                case "video":
                    snapshot.outboundVideoBytesSent += numberValue(entry, "bytesSent")
                    snapshot.outboundVideoFramesEncoded += numberValue(entry, "framesEncoded")
                    snapshot.outboundVideoFramesSent += numberValue(entry, "framesSent")
                default:
                    break
                }
            case "candidate-pair":
                if boolValue(entry, "nominated") || stringValue(entry, "state") == "succeeded" {
                    selectedCandidatePair = selectedCandidatePair ?? entry
                    snapshot.outboundRtt = max(snapshot.outboundRtt, numberValue(entry, "currentRoundTripTime"))
                }
            default:
                break
            }
        }

        if let selectedCandidatePair {
            snapshot.outboundRtt = max(snapshot.outboundRtt, numberValue(selectedCandidatePair, "currentRoundTripTime"))
            snapshot.candidatePair = candidatePairSummary(selectedCandidatePair, entriesByID: entriesByID)
        }
        return snapshot
    }

    private static func statisticsEntries(from report: LKRTCStatisticsReport) -> [NativeRTCStatisticsEntry] {
        report.statistics.values.map { stat in
            NativeRTCStatisticsEntry(
                id: stat.id,
                type: stat.type,
                timestampUs: stat.timestamp_us,
                values: stat.values.compactMapValues(jsonValue)
            )
        }
    }

    private static func candidatePairSummary(
        _ candidatePair: NativeRTCStatisticsEntry,
        entriesByID: [String: NativeRTCStatisticsEntry]
    ) -> NativeMediaQualityCandidatePair {
        let localCandidate = stringValue(candidatePair, "localCandidateId").flatMap { entriesByID[$0] }
        let remoteCandidate = stringValue(candidatePair, "remoteCandidateId").flatMap { entriesByID[$0] }
        let pairProtocol = stringValue(candidatePair, "protocol")
        return NativeMediaQualityCandidatePair(
            protocol: pairProtocol ?? stringValue(localCandidate, "protocol") ?? "",
            networkType: stringValue(localCandidate, "networkType") ?? "",
            localCandidateType: stringValue(localCandidate, "candidateType") ?? "",
            remoteCandidateType: stringValue(remoteCandidate, "candidateType") ?? "",
            availableOutgoingBitrate: numberValue(candidatePair, "availableOutgoingBitrate"),
            currentRoundTripTime: numberValue(candidatePair, "currentRoundTripTime")
        )
    }

    private static func mediaKind(_ entry: NativeRTCStatisticsEntry) -> String {
        stringValue(entry, "kind") ?? stringValue(entry, "mediaType") ?? ""
    }

    private static func stringValue(_ entry: NativeRTCStatisticsEntry?, _ key: String) -> String? {
        guard let value = entry?.values[key] else { return nil }
        if case .string(let string) = value {
            return string
        }
        return nil
    }

    private static func numberValue(_ entry: NativeRTCStatisticsEntry?, _ key: String) -> Double {
        guard let value = entry?.values[key] else { return 0 }
        switch value {
        case .number(let number):
            return number
        case .bool(let bool):
            return bool ? 1 : 0
        default:
            return 0
        }
    }

    private static func boolValue(_ entry: NativeRTCStatisticsEntry, _ key: String) -> Bool {
        guard let value = entry.values[key] else { return false }
        switch value {
        case .bool(let bool):
            return bool
        case .number(let number):
            return number != 0
        default:
            return false
        }
    }

    private static func jsonValue(from object: NSObject) -> JSONValue? {
        if let number = object as? NSNumber {
            if CFGetTypeID(number) == CFBooleanGetTypeID() {
                return .bool(number.boolValue)
            }
            return .number(number.doubleValue)
        }
        if let string = object as? NSString {
            return .string(string as String)
        }
        if let array = object as? [NSObject] {
            return .array(array.compactMap(jsonValue))
        }
        if let dictionary = object as? [String: NSObject] {
            return .object(dictionary.compactMapValues(jsonValue))
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

#if os(macOS)
private struct NativeDesktopScreenShareBundle {
    let source: LKRTCVideoSource
    let capturer: LKRTCDesktopCapturer
    let track: LKRTCVideoTrack
}

internal struct NativeScreenShareTrackSwitch {
    private let hasScreenCaptureAccess: () -> Bool

    init(hasScreenCaptureAccess: @escaping () -> Bool = NativeRoomRTCClient.screenCaptureAccessGranted) {
        self.hasScreenCaptureAccess = hasScreenCaptureAccess
    }

    @discardableResult
    func start<ScreenTrack>(
        makeScreenTrack: () -> ScreenTrack,
        installScreenTrack: (ScreenTrack) -> Void,
        startCapture: (ScreenTrack) -> Void
    ) throws -> ScreenTrack {
        guard hasScreenCaptureAccess() else {
            throw RoomRTCError.screenCapturePermissionDenied
        }
        let screenTrack = makeScreenTrack()
        installScreenTrack(screenTrack)
        startCapture(screenTrack)
        return screenTrack
    }

    func stop<CameraTrack, Capturer>(
        cameraTrack: CameraTrack?,
        capturer: Capturer?,
        restoreCameraTrack: (CameraTrack?) -> Void,
        stopCapture: (Capturer) async -> Void
    ) async {
        restoreCameraTrack(cameraTrack)
        if let capturer {
            await stopCapture(capturer)
        }
    }
}

extension NativeRoomRTCClient: LKRTCDesktopCapturerDelegate {
    public func didSourceCaptureStart(_ capturer: LKRTCDesktopCapturer) {}

    public func didSourceCapturePaused(_ capturer: LKRTCDesktopCapturer) {}

    public func didSourceCaptureStop(_ capturer: LKRTCDesktopCapturer) {}

    public func didSourceCaptureError(_ capturer: LKRTCDesktopCapturer) {}
}
#endif
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

    public func setScreenShareEnabled(_ enabled: Bool) async throws {
        if enabled {
            throw RoomRTCError.webRTCUnavailable
        }
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

    public func mediaQualitySnapshot() async throws -> NativeMediaQualitySnapshot {
        throw RoomRTCError.webRTCUnavailable
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
    case screenCapturePermissionDenied
    case screenShareUnavailable
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
