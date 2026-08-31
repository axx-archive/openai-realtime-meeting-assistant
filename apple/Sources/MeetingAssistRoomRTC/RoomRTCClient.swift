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
public typealias NativeMediaRuntimeStateHandler = @Sendable (NativeMediaRuntimeSnapshot) async -> Void

public enum NativeScreenShareRuntimeEvent: String, Equatable, Sendable {
    case capturePaused
    case captureStopped
    case captureError
}

public typealias NativeScreenShareRuntimeStateHandler = @Sendable (NativeScreenShareRuntimeEvent) async -> Void

public enum NativeMediaDeviceKind: String, Equatable, Sendable {
    case audioInput
    case audioOutput
    case camera
}

/// Device names are display-only. This type deliberately is not Codable so a
/// runtime inventory cannot be accidentally promoted into media evidence.
public struct NativeMediaDevice: Equatable, Sendable {
    public var id: String
    public var uiDisplayName: String
    public var kind: NativeMediaDeviceKind
    public var isDefault: Bool
    public var isSelected: Bool

    public init(
        id: String,
        uiDisplayName: String,
        kind: NativeMediaDeviceKind,
        isDefault: Bool = false,
        isSelected: Bool = false
    ) {
        self.id = id
        self.uiDisplayName = uiDisplayName
        self.kind = kind
        self.isDefault = isDefault
        self.isSelected = isSelected
    }
}

public struct NativeMediaDeviceInventory: Equatable, Sendable {
    public var audioInputs: [NativeMediaDevice]
    public var audioOutputs: [NativeMediaDevice]
    public var cameras: [NativeMediaDevice]

    public init(
        audioInputs: [NativeMediaDevice] = [],
        audioOutputs: [NativeMediaDevice] = [],
        cameras: [NativeMediaDevice] = []
    ) {
        self.audioInputs = audioInputs
        self.audioOutputs = audioOutputs
        self.cameras = cameras
    }
}

public enum NativeAudioProcessingMode: String, Codable, Equatable, Sendable {
    case automatic
    case platform
    case software
    case unknown
}

public enum NativeAudioProcessingImplementation: String, Codable, Equatable, Sendable {
    case unknown
    case disabled
    case software
    case platform
    case softwareAndPlatform
}

public enum NativeAudioProcessingRequestResult: String, Codable, Equatable, Sendable {
    case notRequested
    case applied
    case stored
    case rejectedRemoteTrack
    case rejectedInvalidCombination
    case rejectedPlatformUnavailable
    case applyFailed
    case unknownFailure

    public var succeeded: Bool {
        self == .applied || self == .stored
    }
}

public struct NativeAudioProcessingRequest: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var mode: NativeAudioProcessingMode

    public init(enabled: Bool, mode: NativeAudioProcessingMode) {
        self.enabled = enabled
        self.mode = mode
    }
}

public struct NativeAudioProcessingComponentSnapshot: Codable, Equatable, Sendable {
    public var requested: NativeAudioProcessingRequest?
    public var softwareResolved: Bool
    public var softwareActive: Bool
    public var platformAvailable: Bool
    public var platformResolved: Bool
    public var platformActive: Bool
    public var effective: NativeAudioProcessingImplementation

    public init(
        requested: NativeAudioProcessingRequest? = nil,
        softwareResolved: Bool = false,
        softwareActive: Bool = false,
        platformAvailable: Bool = false,
        platformResolved: Bool = false,
        platformActive: Bool = false,
        effective: NativeAudioProcessingImplementation = .unknown
    ) {
        self.requested = requested
        self.softwareResolved = softwareResolved
        self.softwareActive = softwareActive
        self.platformAvailable = platformAvailable
        self.platformResolved = platformResolved
        self.platformActive = platformActive
        self.effective = effective
    }
}

public struct NativePlatformVoiceProcessingSnapshot: Codable, Equatable, Sendable {
    public var enabledRequested: Bool
    public var enabledActive: Bool
    public var bypassedRequested: Bool
    public var bypassedActive: Bool
    public var automaticGainControlRequested: Bool
    public var automaticGainControlActive: Bool

    public init(
        enabledRequested: Bool = false,
        enabledActive: Bool = false,
        bypassedRequested: Bool = false,
        bypassedActive: Bool = false,
        automaticGainControlRequested: Bool = false,
        automaticGainControlActive: Bool = false
    ) {
        self.enabledRequested = enabledRequested
        self.enabledActive = enabledActive
        self.bypassedRequested = bypassedRequested
        self.bypassedActive = bypassedActive
        self.automaticGainControlRequested = automaticGainControlRequested
        self.automaticGainControlActive = automaticGainControlActive
    }
}

public struct NativeAudioProcessingSnapshot: Codable, Equatable, Sendable {
    public var requestResult: NativeAudioProcessingRequestResult
    public var hasAudioProcessingModule: Bool
    public var echoCancellation: NativeAudioProcessingComponentSnapshot
    public var noiseSuppression: NativeAudioProcessingComponentSnapshot
    public var automaticGainControl: NativeAudioProcessingComponentSnapshot
    public var highPassFilter: NativeAudioProcessingComponentSnapshot
    public var platformVoiceProcessing: NativePlatformVoiceProcessingSnapshot

    public init(
        requestResult: NativeAudioProcessingRequestResult = .notRequested,
        hasAudioProcessingModule: Bool = false,
        echoCancellation: NativeAudioProcessingComponentSnapshot = .init(),
        noiseSuppression: NativeAudioProcessingComponentSnapshot = .init(),
        automaticGainControl: NativeAudioProcessingComponentSnapshot = .init(),
        highPassFilter: NativeAudioProcessingComponentSnapshot = .init(),
        platformVoiceProcessing: NativePlatformVoiceProcessingSnapshot = .init()
    ) {
        self.requestResult = requestResult
        self.hasAudioProcessingModule = hasAudioProcessingModule
        self.echoCancellation = echoCancellation
        self.noiseSuppression = noiseSuppression
        self.automaticGainControl = automaticGainControl
        self.highPassFilter = highPassFilter
        self.platformVoiceProcessing = platformVoiceProcessing
    }
}

public enum NativeMediaDegradation: String, Codable, Equatable, Sendable {
    case audioProcessingRequestFailed
    case requestedAudioProcessingInactive
    case platformAudioProcessingFellBackToSoftware
    case platformAudioProcessingUnavailableUsingSoftware
    case selectedAudioInputRemovedUsingDefault
    case selectedAudioOutputRemovedUsingDefault
    case audioDeviceRecoveryFailed
    case selectedCameraRemovedUsingDefault
    case cameraRecoveryFailed
    case captureStopTimedOut
}

/// Processing state is evidence-safe; device inventory is UI-only and is not
/// Codable because it contains user-visible device names.
public struct NativeMediaRuntimeSnapshot: Equatable, Sendable {
    public var devices: NativeMediaDeviceInventory
    public var audioProcessing: NativeAudioProcessingSnapshot
    public var degradations: [NativeMediaDegradation]

    public init(
        devices: NativeMediaDeviceInventory = .init(),
        audioProcessing: NativeAudioProcessingSnapshot = .init(),
        degradations: [NativeMediaDegradation] = []
    ) {
        self.devices = devices
        self.audioProcessing = audioProcessing
        self.degradations = degradations
    }
}

public enum NativeOfferLayoutFailure: String, Codable, Equatable, Sendable {
    case malformedMediaSection
    case missingMediaID
    case duplicateMediaID
    case missingAudioPublisherUplink
    case ambiguousAudioPublisherUplink
    case missingVideoPublisherUplink
    case ambiguousVideoPublisherUplink
    case missingH264PacketizationModeOne
    case missingH264RTX
    case transceiverMappingMismatch
    case transceiverDirectionRejected
    case codecPreferenceRejected
}

internal enum NativeOfferedMediaKind: String, Equatable {
    case audio
    case video
}

internal enum NativeOfferedMediaDirection: String, Equatable {
    case sendReceive = "sendrecv"
    case sendOnly = "sendonly"
    case receiveOnly = "recvonly"
    case inactive

    var remoteReceives: Bool {
        self == .sendReceive || self == .receiveOnly
    }
}

internal struct NativePublisherUplinkLayout: Equatable {
    var audioMID: String
    var videoMID: String
    var preferredH264PayloadTypes: [Int]
    var preferredRTXPayloadTypes: [Int]

    static func parse(_ sdp: String) throws -> NativePublisherUplinkLayout {
        let normalized = sdp.replacingOccurrences(of: "\r\n", with: "\n")
        let lines = normalized.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        var sessionDirection: NativeOfferedMediaDirection = .sendReceive
        var sections: [NativeOfferedMediaSection] = []
        var current: NativeOfferedMediaSection?

        for rawLine in lines {
            let line = rawLine.trimmingCharacters(in: .whitespacesAndNewlines)
            if line.hasPrefix("m=") {
                if let current { sections.append(current) }
                let fields = line.dropFirst(2).split(separator: " ").map(String.init)
                guard fields.count >= 4,
                      let kind = NativeOfferedMediaKind(rawValue: fields[0]) else {
                    current = nil
                    continue
                }
                guard Int(fields[1]) != nil else {
                    throw RoomRTCError.invalidOfferLayout(.malformedMediaSection)
                }
                current = NativeOfferedMediaSection(
                    kind: kind,
                    rejected: fields[1] == "0",
                    mid: nil,
                    direction: sessionDirection,
                    payloadTypes: fields.dropFirst(3).compactMap(Int.init),
                    codecNames: [:],
                    formatParameters: [:]
                )
                continue
            }

            if let direction = Self.direction(from: line) {
                if current == nil {
                    sessionDirection = direction
                } else {
                    current?.direction = direction
                }
                continue
            }
            guard current != nil else { continue }
            if line.hasPrefix("a=mid:") {
                current?.mid = String(line.dropFirst("a=mid:".count))
            } else if line.hasPrefix("a=rtpmap:") {
                let value = line.dropFirst("a=rtpmap:".count)
                let fields = value.split(separator: " ", maxSplits: 1).map(String.init)
                if fields.count == 2, let payloadType = Int(fields[0]) {
                    current?.codecNames[payloadType] = fields[1].split(separator: "/").first.map(String.init)?.lowercased()
                }
            } else if line.hasPrefix("a=fmtp:") {
                let value = line.dropFirst("a=fmtp:".count)
                let fields = value.split(separator: " ", maxSplits: 1).map(String.init)
                if fields.count == 2, let payloadType = Int(fields[0]) {
                    current?.formatParameters[payloadType] = Self.parseFormatParameters(fields[1])
                }
            }
        }
        if let current { sections.append(current) }

        let activeSections = sections.filter { !$0.rejected }
        for section in activeSections where section.mid?.isEmpty != false {
            throw RoomRTCError.invalidOfferLayout(.missingMediaID)
        }
        let mids = activeSections.compactMap(\.mid)
        guard Set(mids).count == mids.count else {
            throw RoomRTCError.invalidOfferLayout(.duplicateMediaID)
        }

        let audioUplinks = activeSections.filter { $0.kind == .audio && $0.direction.remoteReceives }
        guard !audioUplinks.isEmpty else {
            throw RoomRTCError.invalidOfferLayout(.missingAudioPublisherUplink)
        }
        guard audioUplinks.count == 1 else {
            throw RoomRTCError.invalidOfferLayout(.ambiguousAudioPublisherUplink)
        }
        let videoUplinks = activeSections.filter { $0.kind == .video && $0.direction.remoteReceives }
        guard !videoUplinks.isEmpty else {
            throw RoomRTCError.invalidOfferLayout(.missingVideoPublisherUplink)
        }
        guard videoUplinks.count == 1 else {
            throw RoomRTCError.invalidOfferLayout(.ambiguousVideoPublisherUplink)
        }

        let video = videoUplinks[0]
        let h264 = video.payloadTypes.filter { payloadType in
            video.codecNames[payloadType] == "h264"
                && video.formatParameters[payloadType]?["packetization-mode"] == "1"
        }
        guard !h264.isEmpty else {
            throw RoomRTCError.invalidOfferLayout(.missingH264PacketizationModeOne)
        }
        let h264Set = Set(h264.map(String.init))
        let rtx = video.payloadTypes.filter { payloadType in
            video.codecNames[payloadType] == "rtx"
                && video.formatParameters[payloadType].flatMap { $0["apt"] }.map(h264Set.contains) == true
        }
        guard !rtx.isEmpty else {
            throw RoomRTCError.invalidOfferLayout(.missingH264RTX)
        }

        return NativePublisherUplinkLayout(
            audioMID: audioUplinks[0].mid!,
            videoMID: video.mid!,
            preferredH264PayloadTypes: h264,
            preferredRTXPayloadTypes: rtx
        )
    }

    private static func direction(from line: String) -> NativeOfferedMediaDirection? {
        guard line.hasPrefix("a=") else { return nil }
        return NativeOfferedMediaDirection(rawValue: String(line.dropFirst(2)))
    }

    private static func parseFormatParameters(_ value: String) -> [String: String] {
        var parameters: [String: String] = [:]
        for item in value.split(separator: ";") {
            let pair = item.split(separator: "=", maxSplits: 1).map {
                $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            }
            guard pair.count == 2, !pair[0].isEmpty else { continue }
            parameters[pair[0]] = pair[1]
        }
        return parameters
    }
}

private struct NativeOfferedMediaSection {
    var kind: NativeOfferedMediaKind
    var rejected: Bool
    var mid: String?
    var direction: NativeOfferedMediaDirection
    var payloadTypes: [Int]
    var codecNames: [Int: String]
    var formatParameters: [Int: [String: String]]
}

#if canImport(LiveKitWebRTC)
private struct NativeAudioProcessingRequestedConfiguration {
    var echoCancellation: NativeAudioProcessingRequest
    var noiseSuppression: NativeAudioProcessingRequest
    var automaticGainControl: NativeAudioProcessingRequest
    var highPassFilter: NativeAudioProcessingRequest
}
#endif

internal struct NativeVideoCodecDescriptor: Equatable {
    var name: String
    var payloadType: Int?
    var parameters: [String: String]
}

internal enum NativeVideoCodecPreference {
    static func orderedIndices(_ codecs: [NativeVideoCodecDescriptor]) throws -> [Int] {
        let preferredH264 = codecs.indices.filter { index in
            codecs[index].name.caseInsensitiveCompare("H264") == .orderedSame
                && codecs[index].parameters["packetization-mode"] == "1"
        }
        guard !preferredH264.isEmpty else {
            throw RoomRTCError.invalidOfferLayout(.missingH264PacketizationModeOne)
        }
        let payloadTypes = Set(preferredH264.compactMap { codecs[$0].payloadType }.map(String.init))
        let allRTX = codecs.indices.filter {
            codecs[$0].name.caseInsensitiveCompare("rtx") == .orderedSame
        }
        let matchingRTX = allRTX.filter {
            codecs[$0].name.caseInsensitiveCompare("rtx") == .orderedSame
                && codecs[$0].parameters["apt"].map(payloadTypes.contains) == true
        }
        // Some WebRTC builds expose RTX capabilities without stable preferred
        // payload types. In that case the remote offer remains the authority
        // for apt pairing; retain all RTX capabilities immediately after H264.
        let preferredRTX = matchingRTX.isEmpty ? allRTX : matchingRTX
        guard !preferredRTX.isEmpty else {
            throw RoomRTCError.invalidOfferLayout(.missingH264RTX)
        }
        var preferred: [Int] = []
        for h264Index in preferredH264 {
            preferred.append(h264Index)
            guard let payloadType = codecs[h264Index].payloadType else { continue }
            preferred.append(contentsOf: preferredRTX.filter {
                codecs[$0].parameters["apt"] == String(payloadType)
            })
        }
        return preferred + codecs.indices.filter { !preferred.contains($0) }
    }
}

internal enum NativeDeviceSelectionRecovery {
    static func needsDefaultRecovery(selectedID: String?, availableIDs: [String]) -> Bool {
        guard let selectedID else { return false }
        return !availableIDs.contains(selectedID)
    }
}

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

public struct NativeMediaEvidenceClient: Codable, Equatable, Sendable {
    public var platform: String
    public var version: String

    public init(platform: String = "", version: String = "") {
        self.platform = platform
        self.version = version
    }
}

public struct NativeRemoteVideoRenderObservation: Codable, Equatable, Sendable {
    public var renderedFrames: Int
    public var firstRenderedAt: String
    public var latestRenderedAt: String
    public var latestFrameWidth: Int
    public var latestFrameHeight: Int

    public init(
        renderedFrames: Int = 0,
        firstRenderedAt: String = "",
        latestRenderedAt: String = "",
        latestFrameWidth: Int = 0,
        latestFrameHeight: Int = 0
    ) {
        self.renderedFrames = renderedFrames
        self.firstRenderedAt = firstRenderedAt
        self.latestRenderedAt = latestRenderedAt
        self.latestFrameWidth = latestFrameWidth
        self.latestFrameHeight = latestFrameHeight
    }

    public var hasRenderedFrame: Bool {
        renderedFrames > 0 && latestFrameWidth > 0 && latestFrameHeight > 0
    }
}

public struct NativeMediaEvidenceRendererContext: Codable, Equatable, Sendable {
    public var source: String
    public var remoteVideoFramesRendered: Int
    public var observedRemoteVideoTracks: Int
    public var latestFrameWidth: Int
    public var latestFrameHeight: Int
    public var latestRenderedAt: String
    public var capturesPixels: Bool

    public init(
        source: String = "native_remote_video_renderer",
        remoteVideoFramesRendered: Int = 0,
        observedRemoteVideoTracks: Int = 0,
        latestFrameWidth: Int = 0,
        latestFrameHeight: Int = 0,
        latestRenderedAt: String = "",
        capturesPixels: Bool = false
    ) {
        self.source = source
        self.remoteVideoFramesRendered = remoteVideoFramesRendered
        self.observedRemoteVideoTracks = observedRemoteVideoTracks
        self.latestFrameWidth = latestFrameWidth
        self.latestFrameHeight = latestFrameHeight
        self.latestRenderedAt = latestRenderedAt
        self.capturesPixels = capturesPixels
    }

    public init(trackObservations: [NativeRemoteVideoRenderObservation]) {
        let rendered = trackObservations.filter(\.hasRenderedFrame)
        let latest = rendered.max { lhs, rhs in
            lhs.latestRenderedAt < rhs.latestRenderedAt
        }
        self.init(
            remoteVideoFramesRendered: rendered.reduce(0) { $0 + $1.renderedFrames },
            observedRemoteVideoTracks: rendered.count,
            latestFrameWidth: latest?.latestFrameWidth ?? 0,
            latestFrameHeight: latest?.latestFrameHeight ?? 0,
            latestRenderedAt: latest?.latestRenderedAt ?? ""
        )
    }

    public var remoteVideoRendered: Bool {
        remoteVideoFramesRendered > 0
            && observedRemoteVideoTracks > 0
            && latestFrameWidth > 0
            && latestFrameHeight > 0
            && !capturesPixels
    }
}

public struct NativeMediaEvidenceAssertions: Codable, Equatable, Sendable {
    public var cameraPublished: Bool
    public var microphonePublished: Bool
    public var remoteAudioReceived: Bool
    public var remoteVideoRendered: Bool

    public init(
        cameraPublished: Bool = false,
        microphonePublished: Bool = false,
        remoteAudioReceived: Bool = false,
        remoteVideoRendered: Bool = false
    ) {
        self.cameraPublished = cameraPublished
        self.microphonePublished = microphonePublished
        self.remoteAudioReceived = remoteAudioReceived
        self.remoteVideoRendered = remoteVideoRendered
    }

    public var allPassed: Bool {
        cameraPublished
            && microphonePublished
            && remoteAudioReceived
            && remoteVideoRendered
    }
}

public struct NativeMediaEvidenceCandidatePair: Codable, Equatable, Sendable {
    public var `protocol`: String
    public var networkType: String
    public var localCandidateType: String
    public var remoteCandidateType: String
    public var relayCandidateSelected: Bool
    public var currentRoundTripTime: Double

    public init(
        protocol: String = "",
        networkType: String = "",
        localCandidateType: String = "",
        remoteCandidateType: String = "",
        relayCandidateSelected: Bool = false,
        currentRoundTripTime: Double = 0
    ) {
        self.protocol = `protocol`
        self.networkType = networkType
        self.localCandidateType = localCandidateType
        self.remoteCandidateType = remoteCandidateType
        self.relayCandidateSelected = relayCandidateSelected
        self.currentRoundTripTime = currentRoundTripTime
    }

    public init(source: NativeMediaQualityCandidatePair) {
        let localType = source.localCandidateType.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let remoteType = source.remoteCandidateType.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        self.init(
            protocol: source.protocol,
            networkType: source.networkType,
            localCandidateType: source.localCandidateType,
            remoteCandidateType: source.remoteCandidateType,
            relayCandidateSelected: localType == "relay" || remoteType == "relay",
            currentRoundTripTime: source.currentRoundTripTime
        )
    }
}

public struct NativeMediaEvidenceCounters: Codable, Equatable, Sendable {
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

    public init(source: NativeMediaQualitySnapshot) {
        outboundAudioBytesSent = source.outboundAudioBytesSent
        outboundAudioPacketsSent = source.outboundAudioPacketsSent
        outboundVideoBytesSent = source.outboundVideoBytesSent
        outboundVideoFramesEncoded = source.outboundVideoFramesEncoded
        outboundVideoFramesSent = source.outboundVideoFramesSent
        inboundAudioJitter = source.inboundAudioJitter
        inboundAudioLost = source.inboundAudioLost
        inboundAudioPacketsReceived = source.inboundAudioPacketsReceived
        inboundVideoJitter = source.inboundVideoJitter
        inboundVideoLost = source.inboundVideoLost
        inboundVideoPacketsReceived = source.inboundVideoPacketsReceived
        inboundVideoDrops = source.inboundVideoDrops
        inboundVideoDecoded = source.inboundVideoDecoded
        outboundRtt = source.outboundRtt
    }
}

public struct NativeMediaEvidenceAppContext: Codable, Equatable, Sendable {
    public var version: String
    public var build: String
    public var target: String
    public var clientPlatform: String
    public var clientVersion: String

    public init(
        version: String = "",
        build: String = "",
        target: String = "",
        clientPlatform: String = "",
        clientVersion: String = ""
    ) {
        self.version = version
        self.build = build
        self.target = target
        self.clientPlatform = clientPlatform
        self.clientVersion = clientVersion
    }
}

public struct NativeMediaEvidenceDeviceContext: Codable, Equatable, Sendable {
    public var kind: String
    public var model: String
    public var os: String
    public var physical: Bool

    public init(kind: String = "unknown", model: String = "", os: String = "", physical: Bool = false) {
        self.kind = kind
        self.model = model
        self.os = os
        self.physical = physical
    }
}

public struct NativeMediaEvidenceCaptureContext: Codable, Equatable, Sendable {
    public var app: NativeMediaEvidenceAppContext
    public var device: NativeMediaEvidenceDeviceContext
    public var runId: String
    public var roomId: String

    public init(
        app: NativeMediaEvidenceAppContext = NativeMediaEvidenceAppContext(),
        device: NativeMediaEvidenceDeviceContext = NativeMediaEvidenceDeviceContext(),
        runId: String = "",
        roomId: String = ""
    ) {
        self.app = app
        self.device = device
        self.runId = runId
        self.roomId = roomId
    }
}

public struct NativeICEReadinessSummary: Codable, Equatable, Sendable {
    public var ok: Bool
    public var hasIceServers: Bool
    public var iceServerCount: Int
    public var knownUrlCount: Int
    public var unknownUrlCount: Int
    public var stunCount: Int
    public var stunsCount: Int
    public var turnCount: Int
    public var turnsCount: Int
    public var turnServersWithCredentials: Int
    public var turnServersMissingCredentials: Int
    public var relayTransports: [String]
    public var warnings: [String]
    public var errors: [String]

    public init(
        ok: Bool = false,
        hasIceServers: Bool = false,
        iceServerCount: Int = 0,
        knownUrlCount: Int = 0,
        unknownUrlCount: Int = 0,
        stunCount: Int = 0,
        stunsCount: Int = 0,
        turnCount: Int = 0,
        turnsCount: Int = 0,
        turnServersWithCredentials: Int = 0,
        turnServersMissingCredentials: Int = 0,
        relayTransports: [String] = [],
        warnings: [String] = [],
        errors: [String] = []
    ) {
        self.ok = ok
        self.hasIceServers = hasIceServers
        self.iceServerCount = iceServerCount
        self.knownUrlCount = knownUrlCount
        self.unknownUrlCount = unknownUrlCount
        self.stunCount = stunCount
        self.stunsCount = stunsCount
        self.turnCount = turnCount
        self.turnsCount = turnsCount
        self.turnServersWithCredentials = turnServersWithCredentials
        self.turnServersMissingCredentials = turnServersMissingCredentials
        self.relayTransports = relayTransports
        self.warnings = warnings
        self.errors = errors
    }

    public init(rtcConfiguration: [String: JSONValue], requireTURN: Bool = true) {
        guard case .array(let serverValues)? = rtcConfiguration["iceServers"] else {
            self.init(
                ok: !requireTURN,
                hasIceServers: false,
                errors: requireTURN ? ["No ICE servers were found."] : []
            )
            return
        }

        var iceServerCount = 0
        var knownUrlCount = 0
        var unknownUrlCount = 0
        var stunCount = 0
        var stunsCount = 0
        var turnCount = 0
        var turnsCount = 0
        var turnServersWithCredentials = 0
        var turnServersMissingCredentials = 0
        var malformedServerCount = 0
        var relayTransportSet = Set<String>()

        for value in serverValues {
            guard case .object(let server) = value else {
                malformedServerCount += 1
                continue
            }
            let urls = Self.stringList(from: server["urls"])
            if urls.isEmpty {
                malformedServerCount += 1
                continue
            }

            iceServerCount += 1
            let hasCredentials = Self.nonEmptyString(from: server["username"]) != nil
                && Self.nonEmptyString(from: server["credential"]) != nil
            var serverHasTURN = false

            for url in urls {
                switch Self.classifyRelayURL(url) {
                case "stun":
                    knownUrlCount += 1
                    stunCount += 1
                case "stuns":
                    knownUrlCount += 1
                    stunsCount += 1
                case "turn":
                    knownUrlCount += 1
                    turnCount += 1
                    serverHasTURN = true
                    relayTransportSet.insert(Self.relayTransport(url: url, secure: false))
                case "turns":
                    knownUrlCount += 1
                    turnsCount += 1
                    serverHasTURN = true
                    relayTransportSet.insert(Self.relayTransport(url: url, secure: true))
                default:
                    unknownUrlCount += 1
                }
            }

            if serverHasTURN {
                if hasCredentials {
                    turnServersWithCredentials += 1
                } else {
                    turnServersMissingCredentials += 1
                }
            }
        }

        var warnings: [String] = []
        var errors: [String] = []
        let hasIceServers = iceServerCount > 0
        let turnRelayCount = turnCount + turnsCount
        if malformedServerCount > 0 {
            warnings.append("\(malformedServerCount) ICE server entries were ignored because they were malformed or blank.")
        }
        if unknownUrlCount > 0 {
            warnings.append("\(unknownUrlCount) ICE server URLs used an unknown scheme.")
        }
        if !hasIceServers {
            errors.append("No usable ICE servers were found.")
        }
        if knownUrlCount == 0 {
            errors.append("No STUN, STUNS, TURN, or TURNS ICE server URLs were found.")
        }
        if requireTURN && turnRelayCount == 0 {
            errors.append("No TURN or TURNS relay URLs were found.")
        }
        if requireTURN && turnRelayCount > 0 && turnServersWithCredentials == 0 {
            errors.append("TURN relay URLs were found, but none have both username and credential.")
        }
        if requireTURN && turnServersMissingCredentials > 0 {
            warnings.append("\(turnServersMissingCredentials) TURN server entries are missing username or credential.")
        }

        self.init(
            ok: warnings.isEmpty && errors.isEmpty,
            hasIceServers: hasIceServers,
            iceServerCount: iceServerCount,
            knownUrlCount: knownUrlCount,
            unknownUrlCount: unknownUrlCount,
            stunCount: stunCount,
            stunsCount: stunsCount,
            turnCount: turnCount,
            turnsCount: turnsCount,
            turnServersWithCredentials: turnServersWithCredentials,
            turnServersMissingCredentials: turnServersMissingCredentials,
            relayTransports: relayTransportSet.sorted(),
            warnings: warnings,
            errors: errors
        )
    }

    public var unambiguousRelayProtocol: String? {
        switch (turnCount > 0, turnsCount > 0) {
        case (true, false):
            return "turn"
        case (false, true):
            return "turns"
        default:
            return nil
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

    private static func classifyRelayURL(_ url: String) -> String {
        let normalized = url.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if normalized.hasPrefix("turns:") { return "turns" }
        if normalized.hasPrefix("turn:") { return "turn" }
        if normalized.hasPrefix("stuns:") { return "stuns" }
        if normalized.hasPrefix("stun:") { return "stun" }
        return "unknown"
    }

    private static func relayTransport(url: String, secure: Bool) -> String {
        let normalized = url.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard let queryStart = normalized.firstIndex(of: "?") else {
            return secure ? "tls" : "udp"
        }
        let query = normalized[normalized.index(after: queryStart)...]
        for item in query.split(separator: "&") {
            let parts = item.split(separator: "=", maxSplits: 1).map(String.init)
            if parts.count == 2 && parts[0] == "transport" {
                let value = parts[1].trimmingCharacters(in: .whitespacesAndNewlines)
                if !value.isEmpty { return value }
            }
        }
        return secure ? "tls" : "udp"
    }
}

public struct NativeTurnRelaySelectedCandidate: Codable, Equatable, Sendable {
    public var relayProtocol: String
    public var relayCandidateType: String
    public var relayCandidateSelected: Bool
    public var localCandidateType: String
    public var remoteCandidateType: String
    public var currentRoundTripTime: Double
    public var `protocol`: String
    public var networkType: String

    public init(
        relayProtocol: String = "",
        relayCandidateType: String = "",
        relayCandidateSelected: Bool = false,
        localCandidateType: String = "",
        remoteCandidateType: String = "",
        currentRoundTripTime: Double = 0,
        protocol: String = "",
        networkType: String = ""
    ) {
        self.relayProtocol = relayProtocol
        self.relayCandidateType = relayCandidateType
        self.relayCandidateSelected = relayCandidateSelected
        self.localCandidateType = localCandidateType
        self.remoteCandidateType = remoteCandidateType
        self.currentRoundTripTime = currentRoundTripTime
        self.protocol = `protocol`
        self.networkType = networkType
    }

    public init(source: NativeMediaEvidenceCandidatePair, iceReadiness: NativeICEReadinessSummary) {
        let localType = source.localCandidateType.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let remoteType = source.remoteCandidateType.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let selected = localType == "relay" || remoteType == "relay" || source.relayCandidateSelected
        self.init(
            relayProtocol: selected ? iceReadiness.unambiguousRelayProtocol ?? "" : "",
            relayCandidateType: selected ? "relay" : "",
            relayCandidateSelected: selected,
            localCandidateType: source.localCandidateType,
            remoteCandidateType: source.remoteCandidateType,
            currentRoundTripTime: source.currentRoundTripTime,
            protocol: source.protocol,
            networkType: source.networkType
        )
    }
}

public enum NativeTurnRelayObservationError: Error, Equatable, Sendable {
    case missingNetwork
    case uncleanICEReadiness
    case nonRelaySelectedCandidate
    case ambiguousRelayProtocol
    case missingRelayProtocol
    case invalidRoundTripTime
}

public struct NativeTurnRelayObservation: Codable, Equatable, Sendable {
    public var schemaVersion: Int
    public var artifactType: String
    public var status: String
    public var runId: String
    public var roomId: String
    public var network: String
    public var capturedAt: String
    public var app: NativeMediaEvidenceAppContext
    public var device: NativeMediaEvidenceDeviceContext
    public var selectedCandidate: NativeTurnRelaySelectedCandidate
    public var iceReadiness: NativeICEReadinessSummary
    public var sanitization: NativeMediaEvidenceSanitization

    public init(
        evidence: NativeMediaEvidenceSnapshot,
        iceReadiness: NativeICEReadinessSummary,
        network: String
    ) throws {
        let trimmedNetwork = network.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedNetwork.isEmpty else {
            throw NativeTurnRelayObservationError.missingNetwork
        }
        guard iceReadiness.ok, iceReadiness.warnings.isEmpty, iceReadiness.errors.isEmpty else {
            throw NativeTurnRelayObservationError.uncleanICEReadiness
        }
        if iceReadiness.turnCount > 0 && iceReadiness.turnsCount > 0 {
            throw NativeTurnRelayObservationError.ambiguousRelayProtocol
        }
        schemaVersion = 1
        artifactType = "native_turn_relay_observation"
        status = "observed"
        runId = evidence.runId
        roomId = evidence.roomId
        self.network = trimmedNetwork
        capturedAt = evidence.capturedAt
        app = evidence.app
        device = evidence.device
        let candidate = NativeTurnRelaySelectedCandidate(
            source: evidence.selectedCandidate,
            iceReadiness: iceReadiness
        )
        guard candidate.relayCandidateSelected else {
            throw NativeTurnRelayObservationError.nonRelaySelectedCandidate
        }
        guard !candidate.relayProtocol.isEmpty else {
            throw NativeTurnRelayObservationError.missingRelayProtocol
        }
        guard candidate.currentRoundTripTime > 0 else {
            throw NativeTurnRelayObservationError.invalidRoundTripTime
        }
        selectedCandidate = candidate
        self.iceReadiness = iceReadiness
        sanitization = NativeMediaEvidenceSanitization()
    }
}

public struct NativeMediaEvidenceReleaseSummary: Codable, Equatable, Sendable {
    public var status: String
    public var runId: String
    public var roomId: String
    public var device: String
    public var os: String
    public var testedAt: String
    public var mediaAssertions: NativeMediaEvidenceAssertions

    public init(
        status: String = "pending",
        runId: String = "",
        roomId: String = "",
        device: String = "",
        os: String = "",
        testedAt: String = "",
        mediaAssertions: NativeMediaEvidenceAssertions = NativeMediaEvidenceAssertions()
    ) {
        self.status = status
        self.runId = runId
        self.roomId = roomId
        self.device = device
        self.os = os
        self.testedAt = testedAt
        self.mediaAssertions = mediaAssertions
    }
}

public struct NativeMediaEvidenceAssertionDetail: Codable, Equatable, Sendable {
    public var source: String
    public var value: Double
    public var passed: Bool

    public init(source: String, value: Double, passed: Bool) {
        self.source = source
        self.value = value
        self.passed = passed
    }
}

public struct NativeMediaEvidenceAssertionEvidence: Codable, Equatable, Sendable {
    public var cameraPublished: NativeMediaEvidenceAssertionDetail
    public var microphonePublished: NativeMediaEvidenceAssertionDetail
    public var remoteAudioReceived: NativeMediaEvidenceAssertionDetail
    public var remoteVideoRendered: NativeMediaEvidenceAssertionDetail

    public init(
        source: NativeMediaQualitySnapshot,
        remoteVideoTiles: Int,
        renderer: NativeMediaEvidenceRendererContext,
        assertions: NativeMediaEvidenceAssertions
    ) {
        cameraPublished = NativeMediaEvidenceAssertionDetail(
            source: "outboundVideoFramesSent",
            value: source.outboundVideoFramesSent,
            passed: assertions.cameraPublished
        )
        microphonePublished = NativeMediaEvidenceAssertionDetail(
            source: "outboundAudioPacketsSent",
            value: source.outboundAudioPacketsSent,
            passed: assertions.microphonePublished
        )
        remoteAudioReceived = NativeMediaEvidenceAssertionDetail(
            source: "inboundAudioPacketsReceived",
            value: source.inboundAudioPacketsReceived,
            passed: assertions.remoteAudioReceived
        )
        remoteVideoRendered = NativeMediaEvidenceAssertionDetail(
            source: "nativeRemoteVideoRenderer+inboundVideoDecoded",
            value: min(Double(renderer.remoteVideoFramesRendered), source.inboundVideoDecoded),
            passed: assertions.remoteVideoRendered
        )
    }
}

public struct NativeMediaEvidenceStats: Codable, Equatable, Sendable {
    public var observationWindow: String
    public var samples: [NativeMediaEvidenceCounters]
    public var counters: NativeMediaEvidenceCounters
    public var candidatePair: NativeMediaEvidenceCandidatePair

    public init(
        counters: NativeMediaEvidenceCounters,
        candidatePair: NativeMediaEvidenceCandidatePair,
        observationWindow: String = "cumulative_peer_connection_stats",
        samples: [NativeMediaEvidenceCounters] = []
    ) {
        self.observationWindow = observationWindow
        self.samples = samples
        self.counters = counters
        self.candidatePair = candidatePair
    }
}

public struct NativeMediaEvidenceRemoteContext: Codable, Equatable, Sendable {
    public var remoteVideoTracks: Int
    public var labeledRemoteVideoTracks: Int
    public var remoteVideoTiles: Int

    public init(remoteVideoTracks: Int = 0, labeledRemoteVideoTracks: Int = 0, remoteVideoTiles: Int = 0) {
        self.remoteVideoTracks = remoteVideoTracks
        self.labeledRemoteVideoTracks = labeledRemoteVideoTracks
        self.remoteVideoTiles = remoteVideoTiles
    }
}

public struct NativeMediaEvidenceSanitization: Codable, Equatable, Sendable {
    public var omitted: [String]
    public var redactionPolicy: String

    public init(
        omitted: [String] = [
            "cookies",
            "headers",
            "passwords",
            "rawSdp",
            "rawIceCandidates",
            "candidateIds",
            "turnUrls",
            "turnUsername",
            "turnCredential",
            "ipAddresses",
            "apiKeys",
            "teamIds",
            "certificates",
            "provisioningProfiles",
        ],
        redactionPolicy: String = "native_media_error_safe_message"
    ) {
        self.omitted = omitted
        self.redactionPolicy = redactionPolicy
    }
}

public struct NativeMediaEvidenceSnapshot: Codable, Equatable, Sendable {
    public var schemaVersion: Int
    public var artifactType: String
    public var claimScope: String
    public var releaseEligible: Bool
    public var status: String
    public var runId: String
    public var roomId: String
    public var platform: String
    public var capturedAt: String
    public var sampledAt: Double
    public var app: NativeMediaEvidenceAppContext
    public var device: NativeMediaEvidenceDeviceContext
    public var client: NativeMediaEvidenceClient
    public var lifecycle: RoomLifecycleState
    public var remoteVideoTiles: Int
    public var renderer: NativeMediaEvidenceRendererContext
    public var mediaAssertions: NativeMediaEvidenceAssertions
    public var releaseEvidenceSummary: NativeMediaEvidenceReleaseSummary
    public var assertionEvidence: NativeMediaEvidenceAssertionEvidence
    public var selectedCandidate: NativeMediaEvidenceCandidatePair
    public var counters: NativeMediaEvidenceCounters
    public var stats: NativeMediaEvidenceStats
    public var remote: NativeMediaEvidenceRemoteContext
    public var sanitization: NativeMediaEvidenceSanitization
    public var limitations: [String]

    public init(
        source: NativeMediaQualitySnapshot,
        capturedAt: String = "",
        client: NativeMediaEvidenceClient = NativeMediaEvidenceClient(),
        app: NativeMediaEvidenceAppContext = NativeMediaEvidenceAppContext(),
        device: NativeMediaEvidenceDeviceContext = NativeMediaEvidenceDeviceContext(),
        lifecycle: RoomLifecycleState = .signedOut,
        remoteVideoTiles: Int = 0,
        renderer: NativeMediaEvidenceRendererContext = NativeMediaEvidenceRendererContext(),
        runId: String = "",
        roomId: String = ""
    ) {
        let assertions = NativeMediaEvidenceAssertions(
            cameraPublished: source.outboundVideoFramesSent > 0,
            microphonePublished: source.outboundAudioPacketsSent > 0,
            remoteAudioReceived: source.inboundAudioPacketsReceived > 0,
            remoteVideoRendered: remoteVideoTiles > 0 && source.inboundVideoDecoded > 0 && renderer.remoteVideoRendered
        )
        let candidate = NativeMediaEvidenceCandidatePair(source: source.candidatePair)
        let safeCounters = NativeMediaEvidenceCounters(source: source)
        schemaVersion = 1
        artifactType = "native_device_media"
        claimScope = "qa_snapshot"
        releaseEligible = false
        status = "observed"
        self.runId = runId
        self.roomId = roomId
        platform = client.platform
        self.capturedAt = capturedAt
        sampledAt = source.at
        self.app = NativeMediaEvidenceAppContext(
            version: app.version,
            build: app.build,
            target: app.target,
            clientPlatform: app.clientPlatform.isEmpty ? client.platform : app.clientPlatform,
            clientVersion: app.clientVersion.isEmpty ? client.version : app.clientVersion
        )
        self.device = device
        self.client = client
        self.lifecycle = lifecycle
        self.remoteVideoTiles = remoteVideoTiles
        self.renderer = renderer
        mediaAssertions = assertions
        releaseEvidenceSummary = NativeMediaEvidenceReleaseSummary(
            status: "pending",
            runId: runId,
            roomId: roomId,
            device: device.model,
            os: device.os,
            testedAt: capturedAt,
            mediaAssertions: assertions
        )
        assertionEvidence = NativeMediaEvidenceAssertionEvidence(
            source: source,
            remoteVideoTiles: remoteVideoTiles,
            renderer: renderer,
            assertions: assertions
        )
        selectedCandidate = candidate
        counters = safeCounters
        stats = NativeMediaEvidenceStats(counters: safeCounters, candidatePair: candidate)
        remote = NativeMediaEvidenceRemoteContext(
            remoteVideoTracks: remoteVideoTiles,
            labeledRemoteVideoTracks: 0,
            remoteVideoTiles: remoteVideoTiles
        )
        sanitization = NativeMediaEvidenceSanitization()
        limitations = [
            "QA snapshots are cumulative peer-connection observations, not proof of current media health over a fresh interval.",
            "Remote video evidence requires native renderer frame observation and does not include pixels, screenshots, or raw frame data.",
            "QA snapshots are not physical-device release proof unless captured and promoted through the release proof-pack process.",
            "Do not mark ReleaseEvidence physicalDeviceMedia as passed from a qa_snapshot artifact.",
        ]
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
    private let renderLock = NSLock()
    private var renderObservation = NativeRemoteVideoRenderObservation()

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

    public var renderedFrameObservation: NativeRemoteVideoRenderObservation {
        renderLock.withLock { renderObservation }
    }

    public func recordRenderedFrame(width: Int, height: Int, renderedAt: Date = Date()) {
        let normalizedWidth = max(0, width)
        let normalizedHeight = max(0, height)
        let timestamp = Self.iso8601String(renderedAt)
        renderLock.withLock {
            if renderObservation.firstRenderedAt.isEmpty {
                renderObservation.firstRenderedAt = timestamp
            }
            renderObservation.renderedFrames += 1
            renderObservation.latestRenderedAt = timestamp
            renderObservation.latestFrameWidth = normalizedWidth
            renderObservation.latestFrameHeight = normalizedHeight
        }
    }

    private static func iso8601String(_ date: Date) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.string(from: date)
    }
}

public protocol RoomRTCClient: AnyObject, Sendable {
    var lifecycle: RoomLifecycleState { get }
    func configure(_ config: ClientRTCConfig) async throws
    func setLocalCandidateHandler(_ handler: LocalICECandidateHandler?) async
    func setRemoteVideoTrackHandler(_ handler: RemoteVideoTrackHandler?) async
    func setMediaRuntimeStateHandler(_ handler: NativeMediaRuntimeStateHandler?) async
    func setScreenShareRuntimeStateHandler(_ handler: NativeScreenShareRuntimeStateHandler?) async
    func prepareLocalMedia(audio: Bool, video: Bool) async throws
    func selectAudioInput(id: String?) async throws
    func selectAudioOutput(id: String?) async throws
    func selectCamera(id: String?) async throws
    func setLocalAudioEnabled(_ enabled: Bool) async
    func setLocalVideoEnabled(_ enabled: Bool) async
    func setScreenShareEnabled(_ enabled: Bool) async throws
    func handleOffer(_ sdp: String) async throws -> String
    func addRemoteCandidate(_ json: String) async throws
    func restartICE() async
    func mediaQualitySnapshot() async throws -> NativeMediaQualitySnapshot
    func mediaRuntimeSnapshot() async -> NativeMediaRuntimeSnapshot
    func leave() async
}

public extension RoomRTCClient {
    func setMediaRuntimeStateHandler(_ handler: NativeMediaRuntimeStateHandler?) async {}

    func setScreenShareRuntimeStateHandler(_ handler: NativeScreenShareRuntimeStateHandler?) async {}

    func selectAudioInput(id: String?) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    func selectAudioOutput(id: String?) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    func selectCamera(id: String?) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    func mediaRuntimeSnapshot() async -> NativeMediaRuntimeSnapshot {
        NativeMediaRuntimeSnapshot()
    }
}

internal struct NativeCaptureStopState: Equatable, Sendable {
    private(set) var hasUnresolvedStop = false

    @discardableResult
    mutating func record(completed: Bool) -> Bool {
        if !completed {
            hasUnresolvedStop = true
        }
        return completed && !hasUnresolvedStop
    }
}

internal enum NativeStartedCaptureDisposition: Equatable, Sendable {
    case installed
    case staleCaptureStopped
    case staleCaptureStopTimedOut
}

internal func resolveStartedCapture<Capture>(
    _ capture: Capture,
    installIfCurrent: () -> Bool,
    stopStaleCapture: (Capture) async -> Bool
) async -> NativeStartedCaptureDisposition {
    if installIfCurrent() {
        return .installed
    }
    return await stopStaleCapture(capture)
        ? .staleCaptureStopped
        : .staleCaptureStopTimedOut
}

#if canImport(LiveKitWebRTC)
public struct NativeMediaPermissionAuthorizer: Sendable {
    public var microphone: @Sendable () async -> Bool
    public var camera: @Sendable () async -> Bool

    public init(
        microphone: @escaping @Sendable () async -> Bool,
        camera: @escaping @Sendable () async -> Bool
    ) {
        self.microphone = microphone
        self.camera = camera
    }

    public static let system = NativeMediaPermissionAuthorizer(
        microphone: { await authorizationGranted(for: .audio) },
        camera: { await authorizationGranted(for: .video) }
    )

    public static let allowingAllForTesting = NativeMediaPermissionAuthorizer(
        microphone: { true },
        camera: { true }
    )

    private static func authorizationGranted(for mediaType: AVMediaType) async -> Bool {
        await withCheckedContinuation { continuation in
            Task { @MainActor in
                switch AVCaptureDevice.authorizationStatus(for: mediaType) {
                case .authorized:
                    continuation.resume(returning: true)
                case .denied, .restricted:
                    continuation.resume(returning: false)
                case .notDetermined:
                    AVCaptureDevice.requestAccess(for: mediaType) { granted in
                        continuation.resume(returning: granted)
                    }
                @unknown default:
                    continuation.resume(returning: false)
                }
            }
        }
    }
}

private struct NativeRTCDetachedResources {
    var epoch: UInt64
    var peerConnection: LKRTCPeerConnection?
    var cameraCapturer: LKRTCCameraVideoCapturer?
    #if os(macOS)
    var desktopCapturer: LKRTCDesktopCapturer?
    var desktopStartGate: NativeRTCContinuationGate<Void>?
    #endif
}

public final class NativeRoomRTCClient: NSObject, RoomRTCClient, @unchecked Sendable {
    private let factory: LKRTCPeerConnectionFactory
    private let permissionAuthorizer: NativeMediaPermissionAuthorizer
    private let lock = NSLock()
    private let captureInvocationLock = NSLock()
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
    private var desktopCaptureStartGate: NativeRTCContinuationGate<Void>?
    private var desktopCaptureStarted = false
    #endif
    private var localCandidateHandler: LocalICECandidateHandler?
    private var remoteVideoTrackHandler: RemoteVideoTrackHandler?
    private var mediaRuntimeStateHandler: NativeMediaRuntimeStateHandler?
    private var screenShareRuntimeStateHandler: NativeScreenShareRuntimeStateHandler?
    private var remoteVideoTracks: [String: NativeRemoteVideoTrack] = [:]
    private var audioProcessingRequestResult: NativeAudioProcessingRequestResult = .notRequested
    private var requestedAudioProcessing: NativeAudioProcessingRequestedConfiguration?
    private var selectedAudioInputID: String?
    private var selectedAudioOutputID: String?
    private var selectedCameraID: String?
    private var activeCameraID: String?
    private var runtimeDegradations: Set<NativeMediaDegradation> = []
    private var captureStopState = NativeCaptureStopState()
    private var cameraNotificationTokens: [NSObjectProtocol] = []
    private var operationEpoch: UInt64 = 0

    public var lifecycle: RoomLifecycleState {
        lock.withLock { _lifecycle }
    }

    public init(permissionAuthorizer: NativeMediaPermissionAuthorizer = .system) {
        _ = LKRTCInitializeSSL()
        self.permissionAuthorizer = permissionAuthorizer
        self.factory = LKRTCPeerConnectionFactory(
            encoderFactory: LKRTCDefaultVideoEncoderFactory(),
            decoderFactory: LKRTCDefaultVideoDecoderFactory()
        )
        super.init()
        factory.audioDeviceModule.observer = self
        let center = NotificationCenter.default
        cameraNotificationTokens = [
            center.addObserver(
                forName: AVCaptureDevice.wasConnectedNotification,
                object: nil,
                queue: nil
            ) { [weak self] _ in
                self?.publishMediaRuntimeState()
            },
            center.addObserver(
                forName: AVCaptureDevice.wasDisconnectedNotification,
                object: nil,
                queue: nil
            ) { [weak self] notification in
                guard let self, let device = notification.object as? AVCaptureDevice else { return }
                let removedID = device.uniqueID
                Task { await self.recoverCameraIfNeeded(removedID: removedID) }
            }
        ]
    }

    deinit {
        factory.audioDeviceModule.observer = nil
        for token in cameraNotificationTokens {
            NotificationCenter.default.removeObserver(token)
        }
    }

    public func configure(_ config: ClientRTCConfig) async throws {
        try ensureCaptureStopResolved()
        let detached = lock.withLock { () -> NativeRTCDetachedResources in
            let epoch = advanceOperationEpochLocked()
            #if os(macOS)
            let resources = NativeRTCDetachedResources(
                epoch: epoch,
                peerConnection: peerConnection,
                cameraCapturer: cameraCapturer,
                desktopCapturer: desktopCapturer,
                desktopStartGate: desktopCaptureStartGate
            )
            #else
            let resources = NativeRTCDetachedResources(
                epoch: epoch,
                peerConnection: peerConnection,
                cameraCapturer: cameraCapturer
            )
            #endif
            peerConnection = nil
            localAudioTrack = nil
            localVideoTrack = nil
            localVideoSource = nil
            cameraCapturer = nil
            localVideoSender = nil
            audioProcessingRequestResult = .notRequested
            requestedAudioProcessing = nil
            activeCameraID = nil
            runtimeDegradations.removeAll()
            #if os(macOS)
            screenVideoSource = nil
            screenVideoTrack = nil
            desktopCapturer = nil
            desktopCaptureStartGate = nil
            desktopCaptureStarted = false
            #endif
            remoteVideoTracks.removeAll()
            _lifecycle = .signedOut
            return resources
        }
        detached.peerConnection?.close()
        #if os(macOS)
        detached.desktopStartGate?.resume(throwing: RoomRTCError.operationCancelled)
        #endif

        if let existingCapturer = detached.cameraCapturer {
            guard recordCaptureStopResult(await stopCapture(existingCapturer)) else {
                throw RoomRTCError.peerOperationTimedOut("camera_stop_capture_before_configure")
            }
            try ensureOperationCurrent(detached.epoch)
        }
        #if os(macOS)
        if let existingDesktopCapturer = detached.desktopCapturer {
            guard recordCaptureStopResult(await stopDesktopCapture(existingDesktopCapturer)) else {
                throw RoomRTCError.peerOperationTimedOut("desktop_stop_capture_before_configure")
            }
            try ensureOperationCurrent(detached.epoch)
        }
        #endif
        try ensureOperationCurrent(detached.epoch)

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

        let installed = lock.withLock { () -> Bool in
            guard operationEpoch == detached.epoch, peerConnection == nil else { return false }
            peerConnection = connection
            _lifecycle = .authenticated
            return true
        }
        guard installed else {
            connection.close()
            throw RoomRTCError.operationCancelled
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

    public func setMediaRuntimeStateHandler(_ handler: NativeMediaRuntimeStateHandler?) async {
        lock.withLock {
            mediaRuntimeStateHandler = handler
        }
        if let handler {
            await handler(await mediaRuntimeSnapshot())
        }
    }

    public func setScreenShareRuntimeStateHandler(
        _ handler: NativeScreenShareRuntimeStateHandler?
    ) async {
        lock.withLock {
            screenShareRuntimeStateHandler = handler
        }
    }

    public func prepareLocalMedia(audio: Bool, video: Bool) async throws {
        try ensureCaptureStopResolved()
        guard let operation = lock.withLock({ () -> (UInt64, LKRTCPeerConnection)? in
            guard let peerConnection else { return nil }
            return (operationEpoch, peerConnection)
        }) else {
            throw RoomRTCError.peerConnectionNotConfigured
        }

        if audio {
            let microphoneGranted = try await permissionGranted(
                operation: "microphone_permission",
                request: permissionAuthorizer.microphone
            )
            try ensureOperationCurrent(operation.0, connection: operation.1)
            guard microphoneGranted else {
                throw RoomRTCError.microphonePermissionDenied
            }
            let track = factory.audioTrack(withTrackId: "meetingassist-audio-0")
            let options = LKRTCAudioProcessingOptions.communication()
            let result = track.setAudioProcessingOptions(options)
            let requestResult = Self.audioProcessingRequestResult(result.code)
            let installed = lock.withLock { () -> Bool in
                guard operationEpoch == operation.0, peerConnection === operation.1 else {
                    return false
                }
                localAudioTrack = track
                audioProcessingRequestResult = requestResult
                requestedAudioProcessing = Self.requestedConfiguration(options)
                if !requestResult.succeeded {
                    runtimeDegradations.insert(.audioProcessingRequestFailed)
                }
                return true
            }
            guard installed else { throw RoomRTCError.operationCancelled }
            guard result.isSuccess else {
                throw RoomRTCError.audioProcessingRequestFailed(requestResult)
            }
        }

        if video {
            let cameraGranted = try await permissionGranted(
                operation: "camera_permission",
                request: permissionAuthorizer.camera
            )
            try ensureOperationCurrent(operation.0, connection: operation.1)
            guard cameraGranted else {
                throw RoomRTCError.cameraPermissionDenied
            }
            try await prepareLocalVideo(epoch: operation.0, connection: operation.1)
        }

        try ensureOperationCurrent(operation.0, connection: operation.1)
        setLifecycle(.preparingMedia, epoch: operation.0, connection: operation.1)
        publishMediaRuntimeState()
    }

    private func permissionGranted(
        operation: String,
        request: @escaping @Sendable () async -> Bool
    ) async throws -> Bool {
        let gate = NativePermissionContinuationGate(operation: operation)
        return try await withCheckedThrowingContinuation { continuation in
            gate.install(continuation)
            Task {
                gate.resume(returning: await request())
            }
            gate.failAfterDeadline(nanoseconds: 15_000_000_000)
        }
    }

    public func selectAudioInput(id: String?) async throws {
        let module = factory.audioDeviceModule
        let device = try Self.audioDevice(id: id, in: module.inputDevices, kind: .audioInput)
        guard module.trySetInputDevice(device) else {
            throw RoomRTCError.mediaDeviceSelectionFailed(.audioInput)
        }
        lock.withLock {
            selectedAudioInputID = id
            runtimeDegradations.remove(.selectedAudioInputRemovedUsingDefault)
        }
        publishMediaRuntimeState()
    }

    public func selectAudioOutput(id: String?) async throws {
        let module = factory.audioDeviceModule
        let device = try Self.audioDevice(id: id, in: module.outputDevices, kind: .audioOutput)
        guard module.trySetOutputDevice(device) else {
            throw RoomRTCError.mediaDeviceSelectionFailed(.audioOutput)
        }
        lock.withLock {
            selectedAudioOutputID = id
            runtimeDegradations.remove(.selectedAudioOutputRemovedUsingDefault)
        }
        publishMediaRuntimeState()
    }

    public func selectCamera(id: String?) async throws {
        try ensureCaptureStopResolved()
        let device = try Self.cameraDevice(id: id)
        let captureState = lock.withLock { (operationEpoch, peerConnection, cameraCapturer, activeCameraID) }
        lock.withLock {
            selectedCameraID = id
            runtimeDegradations.remove(.selectedCameraRemovedUsingDefault)
            runtimeDegradations.remove(.cameraRecoveryFailed)
        }
        guard let connection = captureState.1, let capturer = captureState.2 else {
            publishMediaRuntimeState()
            return
        }
        guard captureState.3 != device.uniqueID else {
            publishMediaRuntimeState()
            return
        }
        try await switchCameraCapture(
            capturer,
            to: device,
            epoch: captureState.0,
            connection: connection
        )
        publishMediaRuntimeState()
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
            try await stopScreenShare()
        }
        #else
        if enabled {
            throw RoomRTCError.screenShareUnavailable
        }
        #endif
    }

    public func handleOffer(_ sdp: String) async throws -> String {
        guard let operation = lock.withLock({ () -> (UInt64, LKRTCPeerConnection)? in
            guard let peerConnection else { return nil }
            return (operationEpoch, peerConnection)
        }) else {
            throw RoomRTCError.peerConnectionNotConfigured
        }
        let (epoch, connection) = operation

        let layout = try NativePublisherUplinkLayout.parse(sdp)
        try ensureOperationCurrent(epoch, connection: connection)
        setLifecycle(.negotiating, epoch: epoch, connection: connection)
        let offer = LKRTCSessionDescription(type: .offer, sdp: sdp)
        try await setRemoteDescription(offer, on: connection)
        try ensureOperationCurrent(epoch, connection: connection)
        do {
            try bindPreparedTracks(to: layout, on: connection, epoch: epoch)
        } catch {
            connection.close()
            throw error
        }
        try ensureOperationCurrent(epoch, connection: connection)
        let answer = try await answer(on: connection)
        try ensureOperationCurrent(epoch, connection: connection)
        try await setLocalDescription(answer, on: connection)
        try ensureOperationCurrent(epoch, connection: connection)
        setLifecycle(.connected, epoch: epoch, connection: connection)
        publishMediaRuntimeState()
        return answer.sdp
    }

    public func addRemoteCandidate(_ json: String) async throws {
        guard let operation = lock.withLock({ () -> (UInt64, LKRTCPeerConnection)? in
            guard let peerConnection else { return nil }
            return (operationEpoch, peerConnection)
        }) else {
            throw RoomRTCError.peerConnectionNotConfigured
        }
        let (epoch, connection) = operation

        let payload = try decoder.decode(RTCIceCandidatePayload.self, from: Data(json.utf8))
        let candidate = LKRTCIceCandidate(
            sdp: payload.candidate,
            sdpMLineIndex: Int32(payload.sdpMLineIndex ?? 0),
            sdpMid: payload.sdpMid
        )
        try await add(candidate, to: connection)
        try ensureOperationCurrent(epoch, connection: connection)
    }

    public func restartICE() async {
        guard let operation = lock.withLock({ () -> (UInt64, LKRTCPeerConnection)? in
            guard let peerConnection else { return nil }
            return (operationEpoch, peerConnection)
        }) else { return }
        operation.1.restartIce()
        setLifecycle(.reconnecting, epoch: operation.0, connection: operation.1)
    }

    public func mediaQualitySnapshot() async throws -> NativeMediaQualitySnapshot {
        guard let operation = lock.withLock({ () -> (UInt64, LKRTCPeerConnection)? in
            guard let peerConnection else { return nil }
            return (operationEpoch, peerConnection)
        }) else {
            throw RoomRTCError.peerConnectionNotConfigured
        }
        let (epoch, connection) = operation

        let report = try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<LKRTCStatisticsReport, Error>) in
            let gate = NativeRTCContinuationGate(continuation)
            gate.failAfterDeadline(operation: "get_statistics")
            connection.statistics { report in
                gate.resume(returning: report)
            }
        }
        try ensureOperationCurrent(epoch, connection: connection)
        return Self.mediaQualitySnapshot(from: Self.statisticsEntries(from: report))
    }

    public func mediaRuntimeSnapshot() async -> NativeMediaRuntimeSnapshot {
        recoverRemovedAudioDevicesIfNeeded()
        let processing = Self.audioProcessingSnapshot(
            state: factory.audioProcessingState,
            platformState: factory.audioDeviceModule.platformAudioProcessingState,
            requestResult: lock.withLock { audioProcessingRequestResult },
            requested: lock.withLock { requestedAudioProcessing }
        )
        let devices = deviceInventory()
        var degradations = lock.withLock { runtimeDegradations }
        let isConnected = lifecycle == .connected
        if isConnected, lock.withLock({ localAudioTrack != nil }) {
            let requestedComponents = [
                processing.echoCancellation,
                processing.noiseSuppression,
                processing.automaticGainControl,
                processing.highPassFilter
            ].filter { $0.requested?.enabled == true }
            if requestedComponents.contains(where: { $0.effective == .unknown || $0.effective == .disabled }) {
                degradations.insert(.requestedAudioProcessingInactive)
            }
            if requestedComponents.contains(where: { $0.platformAvailable && $0.effective == .software }) {
                degradations.insert(.platformAudioProcessingFellBackToSoftware)
            }
            if requestedComponents.contains(where: {
                $0.requested?.mode == .automatic
                    && !$0.platformAvailable
                    && $0.effective == .software
            }) {
                degradations.insert(.platformAudioProcessingUnavailableUsingSoftware)
            }
        }
        return NativeMediaRuntimeSnapshot(
            devices: devices,
            audioProcessing: processing,
            degradations: degradations.sorted { $0.rawValue < $1.rawValue }
        )
    }

    public func leave() async {
        // Revoke transport and detach every logical media reference before
        // awaiting device-service callbacks. A wedged camera service can delay
        // cleanup only until the bounded stop deadline; it cannot retain a peer,
        // a sender, or room ownership.
        let detached = lock.withLock { () -> NativeRTCDetachedResources in
            let epoch = advanceOperationEpochLocked()
            #if os(macOS)
            let resources = NativeRTCDetachedResources(
                epoch: epoch,
                peerConnection: peerConnection,
                cameraCapturer: cameraCapturer,
                desktopCapturer: desktopCapturer,
                desktopStartGate: desktopCaptureStartGate
            )
            #else
            let resources = NativeRTCDetachedResources(
                epoch: epoch,
                peerConnection: peerConnection,
                cameraCapturer: cameraCapturer
            )
            #endif
            peerConnection = nil
            localAudioTrack = nil
            localVideoTrack = nil
            localVideoSource = nil
            cameraCapturer = nil
            localVideoSender = nil
            activeCameraID = nil
            requestedAudioProcessing = nil
            #if os(macOS)
            screenVideoSource = nil
            screenVideoTrack = nil
            desktopCapturer = nil
            desktopCaptureStartGate = nil
            desktopCaptureStarted = false
            #endif
            localCandidateHandler = nil
            remoteVideoTrackHandler = nil
            mediaRuntimeStateHandler = nil
            screenShareRuntimeStateHandler = nil
            remoteVideoTracks.removeAll()
            _lifecycle = .leaving
            return resources
        }
        // WebRTC may synchronously invoke delegate callbacks from close(). Those
        // callbacks also take this client's lock, so close only after the peer has
        // been logically revoked and the lock has been released.
        detached.peerConnection?.close()
        #if os(macOS)
        detached.desktopStartGate?.resume(throwing: RoomRTCError.operationCancelled)
        #endif

        if let capturer = detached.cameraCapturer {
            recordCaptureStopResult(await stopCapture(capturer))
        }
        #if os(macOS)
        if let existingDesktopCapturer = detached.desktopCapturer {
            recordCaptureStopResult(await stopDesktopCapture(existingDesktopCapturer))
        }
        #endif
    }

    private func ensureCaptureStopResolved() throws {
        guard !lock.withLock({ captureStopState.hasUnresolvedStop }) else {
            throw RoomRTCError.peerOperationTimedOut("capture_stop_unresolved_restart_required")
        }
    }

    @discardableResult
    private func recordCaptureStopResult(_ completed: Bool) -> Bool {
        lock.withLock {
            let accepted = captureStopState.record(completed: completed)
            if !accepted {
                runtimeDegradations.insert(.captureStopTimedOut)
            }
            return accepted
        }
    }

    private func prepareLocalVideo(
        epoch: UInt64,
        connection: LKRTCPeerConnection
    ) async throws {
        try ensureCaptureStopResolved()
        try ensureOperationCurrent(epoch, connection: connection)
        let requestedCameraID = lock.withLock { selectedCameraID }
        let device = try Self.cameraDevice(id: requestedCameraID)
        guard let format = Self.preferredFormat(for: device) else {
            throw RoomRTCError.cameraFormatUnavailable
        }

        let source = factory.videoSource()
        source.adaptOutputFormat(toWidth: 1280, height: 720, fps: 30)
        let capturer = LKRTCCameraVideoCapturer(delegate: source)
        let track = factory.videoTrack(with: source, trackId: "meetingassist-video-0")

        let fps = Self.preferredFPS(for: format)
        do {
            try await startCapture(
                capturer,
                device: device,
                format: format,
                fps: fps,
                epoch: epoch,
                connection: connection
            )
        } catch {
            guard recordCaptureStopResult(await stopCapture(capturer)) else {
                throw RoomRTCError.peerOperationTimedOut("camera_stop_capture_after_start_failure")
            }
            throw error
        }

        let disposition = await resolveStartedCapture(
            capturer,
            installIfCurrent: {
                lock.withLock {
                    guard operationEpoch == epoch, peerConnection === connection else {
                        return false
                    }
                    localVideoSource = source
                    localVideoTrack = track
                    cameraCapturer = capturer
                    activeCameraID = device.uniqueID
                    return true
                }
            },
            stopStaleCapture: { [self] staleCapturer in
                await stopCapture(staleCapturer)
            }
        )
        switch disposition {
        case .installed:
            return
        case .staleCaptureStopped:
            _ = recordCaptureStopResult(true)
            throw RoomRTCError.operationCancelled
        case .staleCaptureStopTimedOut:
            _ = recordCaptureStopResult(false)
            throw RoomRTCError.peerOperationTimedOut("camera_stop_stale_capture")
        }
    }

    private func bindPreparedTracks(
        to layout: NativePublisherUplinkLayout,
        on connection: LKRTCPeerConnection,
        epoch: UInt64
    ) throws {
        try ensureOperationCurrent(epoch, connection: connection)
        let tracks = lock.withLock { (localAudioTrack, localVideoTrack) }
        let audio = try publisherTransceiver(
            mid: layout.audioMID,
            kind: .audio,
            on: connection
        )
        let video = try publisherTransceiver(
            mid: layout.videoMID,
            kind: .video,
            on: connection
        )

        try setPublisherTrack(tracks.0, on: audio)
        try ensureOperationCurrent(epoch, connection: connection)
        try preferH264WithRTX(on: video)
        try ensureOperationCurrent(epoch, connection: connection)
        try setPublisherTrack(tracks.1, on: video)
        let installed = lock.withLock { () -> Bool in
            guard operationEpoch == epoch, peerConnection === connection else { return false }
            localVideoSender = tracks.1 == nil ? nil : video.sender
            return true
        }
        guard installed else { throw RoomRTCError.operationCancelled }
    }

    private func publisherTransceiver(
        mid: String,
        kind: NativeOfferedMediaKind,
        on connection: LKRTCPeerConnection
    ) throws -> LKRTCRtpTransceiver {
        let matches = connection.transceivers.filter { transceiver in
            guard transceiver.mid == mid else { return false }
            switch kind {
            case .audio:
                return transceiver.mediaType == .audio
            case .video:
                return transceiver.mediaType == .video
            }
        }
        guard matches.count == 1 else {
            throw RoomRTCError.invalidOfferLayout(.transceiverMappingMismatch)
        }
        return matches[0]
    }

    private func setPublisherTrack(
        _ track: LKRTCMediaStreamTrack?,
        on transceiver: LKRTCRtpTransceiver
    ) throws {
        transceiver.sender.track = track
        transceiver.sender.streamIds = track == nil ? [] : ["meetingassist-native"]
        var directionError: NSError?
        transceiver.setDirection(track == nil ? .inactive : .sendOnly, error: &directionError)
        if directionError != nil {
            transceiver.sender.track = nil
            transceiver.sender.streamIds = []
            throw RoomRTCError.invalidOfferLayout(.transceiverDirectionRejected)
        }
    }

    private func preferH264WithRTX(on transceiver: LKRTCRtpTransceiver) throws {
        let capabilities = factory.rtpSenderCapabilities(forKind: kLKRTCMediaStreamTrackKindVideo)
        let codecs = capabilities.codecs
        let descriptors = codecs.map { codec in
            NativeVideoCodecDescriptor(
                name: codec.name,
                payloadType: codec.preferredPayloadType?.intValue,
                parameters: codec.parameters
            )
        }
        let ordered = try NativeVideoCodecPreference.orderedIndices(descriptors).map { codecs[$0] }
        do {
            try transceiver.setCodecPreferences(ordered, error: ())
        } catch {
            throw RoomRTCError.invalidOfferLayout(.codecPreferenceRejected)
        }
    }

    private func switchCameraCapture(
        _ capturer: LKRTCCameraVideoCapturer,
        to device: AVCaptureDevice,
        epoch: UInt64,
        connection: LKRTCPeerConnection
    ) async throws {
        try ensureCaptureStopResolved()
        try ensureOperationCurrent(epoch, connection: connection)
        guard let format = Self.preferredFormat(for: device) else {
            throw RoomRTCError.cameraFormatUnavailable
        }
        guard recordCaptureStopResult(await stopCapture(capturer)) else {
            throw RoomRTCError.peerOperationTimedOut("camera_stop_capture")
        }
        try ensureOperationCurrent(epoch, connection: connection)
        do {
            try await startCapture(
                capturer,
                device: device,
                format: format,
                fps: Self.preferredFPS(for: format),
                epoch: epoch,
                connection: connection
            )
        } catch {
            guard recordCaptureStopResult(await stopCapture(capturer)) else {
                throw RoomRTCError.peerOperationTimedOut("camera_stop_capture_after_switch_failure")
            }
            throw error
        }
        let installed = lock.withLock { () -> Bool in
            guard operationEpoch == epoch,
                  peerConnection === connection,
                  cameraCapturer === capturer else { return false }
            activeCameraID = device.uniqueID
            return true
        }
        guard installed else {
            guard recordCaptureStopResult(await stopCapture(capturer)) else {
                throw RoomRTCError.peerOperationTimedOut("camera_stop_stale_switch_capture")
            }
            throw RoomRTCError.operationCancelled
        }
    }

    private func recoverCameraIfNeeded(removedID: String) async {
        let state = lock.withLock {
            (operationEpoch, peerConnection, activeCameraID, selectedCameraID, cameraCapturer)
        }
        guard state.2 == removedID || state.3 == removedID else {
            publishMediaRuntimeState()
            return
        }
        lock.withLock {
            selectedCameraID = nil
            activeCameraID = nil
        }
        guard let connection = state.1, let capturer = state.4 else {
            _ = lock.withLock {
                runtimeDegradations.insert(.selectedCameraRemovedUsingDefault)
            }
            publishMediaRuntimeState()
            return
        }
        guard
              let replacement = Self.preferredCameraDevice(excluding: removedID) else {
            _ = lock.withLock { runtimeDegradations.insert(.cameraRecoveryFailed) }
            publishMediaRuntimeState()
            return
        }
        do {
            try await switchCameraCapture(
                capturer,
                to: replacement,
                epoch: state.0,
                connection: connection
            )
            try ensureOperationCurrent(state.0, connection: connection)
            _ = lock.withLock {
                runtimeDegradations.insert(.selectedCameraRemovedUsingDefault)
            }
        } catch RoomRTCError.operationCancelled {
            return
        } catch {
            lock.withLock {
                guard operationEpoch == state.0, peerConnection === connection else { return }
                runtimeDegradations.insert(.cameraRecoveryFailed)
            }
        }
        if isOperationCurrent(state.0, connection: connection) {
            publishMediaRuntimeState()
        }
    }

    #if os(macOS)
    private func startScreenShare() async throws {
        try ensureCaptureStopResolved()
        guard let operation = lock.withLock({ () -> (UInt64, LKRTCPeerConnection, LKRTCRtpSender)? in
            guard let peerConnection, let localVideoSender else { return nil }
            return (operationEpoch, peerConnection, localVideoSender)
        }) else {
            throw RoomRTCError.screenShareUnavailable
        }
        let (epoch, connection, sender) = operation
        if lock.withLock({ desktopCapturer != nil }) {
            return
        }

        guard Self.screenCaptureAccessGranted() else {
            throw RoomRTCError.screenCapturePermissionDenied
        }
        let source = factory.videoSource()
        source.adaptOutputFormat(toWidth: 1920, height: 1080, fps: 15)
        let capturer = LKRTCDesktopCapturer(defaultScreen: self, capture: source)
        let track = factory.videoTrack(with: source, trackId: "meetingassist-screen-0")
        track.isEnabled = true

        do {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                let gate = NativeRTCContinuationGate(continuation)
                let installed = lock.withLock { () -> Bool in
                    guard operationEpoch == epoch,
                          peerConnection === connection,
                          localVideoSender === sender,
                          desktopCapturer == nil else { return false }
                    sender.track = track
                    screenVideoSource = source
                    screenVideoTrack = track
                    desktopCapturer = capturer
                    desktopCaptureStartGate = gate
                    desktopCaptureStarted = false
                    return true
                }
                guard installed else {
                    gate.resume(throwing: RoomRTCError.operationCancelled)
                    return
                }
                gate.failAfterDeadline(operation: "desktop_start_capture")
                captureInvocationLock.lock()
                let shouldStart = isOperationCurrent(epoch, connection: connection)
                    && lock.withLock { desktopCapturer === capturer }
                if shouldStart {
                    capturer.startCapture(withFPS: 15)
                }
                captureInvocationLock.unlock()
                if !shouldStart {
                    gate.resume(throwing: RoomRTCError.operationCancelled)
                }
            }
            try ensureOperationCurrent(epoch, connection: connection)
            guard lock.withLock({ desktopCapturer === capturer }) else {
                throw RoomRTCError.operationCancelled
            }
        } catch {
            let shouldStop = lock.withLock { () -> Bool in
                guard desktopCapturer === capturer else { return false }
                sender.track = localVideoTrack
                screenVideoSource = nil
                screenVideoTrack = nil
                desktopCapturer = nil
                desktopCaptureStartGate = nil
                desktopCaptureStarted = false
                return true
            }
            if shouldStop,
               !recordCaptureStopResult(await stopDesktopCapture(capturer)) {
                throw RoomRTCError.peerOperationTimedOut("desktop_stop_capture_after_start_failure")
            }
            throw error
        }
    }

    private func stopScreenShare() async throws {
        try ensureCaptureStopResolved()
        let state = lock.withLock { () -> (
            epoch: UInt64,
            connection: LKRTCPeerConnection?,
            capturer: LKRTCDesktopCapturer?,
            startGate: NativeRTCContinuationGate<Void>?
        ) in
            localVideoSender?.track = localVideoTrack
            let value = (
                epoch: operationEpoch,
                connection: peerConnection,
                capturer: desktopCapturer,
                startGate: desktopCaptureStartGate
            )
            screenVideoSource = nil
            screenVideoTrack = nil
            desktopCapturer = nil
            desktopCaptureStartGate = nil
            desktopCaptureStarted = false
            return value
        }
        state.startGate?.resume(throwing: RoomRTCError.screenShareUnavailable)
        if let capturer = state.capturer,
           !recordCaptureStopResult(await stopDesktopCapture(capturer)) {
            throw RoomRTCError.peerOperationTimedOut("desktop_stop_capture")
        }
        if let connection = state.connection {
            try ensureOperationCurrent(state.epoch, connection: connection)
        }
    }

    private func stopDesktopCapture(_ capturer: LKRTCDesktopCapturer) async -> Bool {
        await withCheckedContinuation { (continuation: CheckedContinuation<Bool, Never>) in
            let gate = NativeRTCStopContinuationGate(continuation)
            gate.failAfterDeadline()
            captureInvocationLock.lock()
            capturer.stopCapture {
                gate.resume(returning: true)
            }
            captureInvocationLock.unlock()
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
        fps: Int,
        epoch: UInt64,
        connection: LKRTCPeerConnection
    ) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let gate = NativeRTCContinuationGate(continuation)
            gate.failAfterDeadline(operation: "camera_start_capture")
            captureInvocationLock.lock()
            let shouldStart = isOperationCurrent(epoch, connection: connection)
            if shouldStart {
                capturer.startCapture(with: device, format: format, fps: fps) { error in
                    if let error {
                        gate.resume(throwing: RoomRTCError.cameraCaptureFailed(error.localizedDescription))
                    } else {
                        gate.resume(returning: ())
                    }
                }
            }
            captureInvocationLock.unlock()
            if !shouldStart {
                gate.resume(throwing: RoomRTCError.operationCancelled)
            }
        }
    }

    private func stopCapture(_ capturer: LKRTCCameraVideoCapturer) async -> Bool {
        await withCheckedContinuation { (continuation: CheckedContinuation<Bool, Never>) in
            let gate = NativeRTCStopContinuationGate(continuation)
            gate.failAfterDeadline()
            captureInvocationLock.lock()
            capturer.stopCapture {
                gate.resume(returning: true)
            }
            captureInvocationLock.unlock()
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
            let gate = NativeRTCContinuationGate(continuation)
            gate.failAfterDeadline(operation: "create_answer")
            connection.answer(for: constraints) { description, error in
                if let error {
                    gate.resume(throwing: RoomRTCError.webRTCOperationFailed(error.localizedDescription))
                } else if let description {
                    gate.resume(returning: description)
                } else {
                    gate.resume(throwing: RoomRTCError.missingSessionDescription)
                }
            }
        }
    }

    private func setRemoteDescription(_ description: LKRTCSessionDescription, on connection: LKRTCPeerConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let gate = NativeRTCContinuationGate(continuation)
            gate.failAfterDeadline(operation: "set_remote_description")
            connection.setRemoteDescription(description) { error in
                if let error {
                    gate.resume(throwing: RoomRTCError.webRTCOperationFailed(error.localizedDescription))
                } else {
                    gate.resume(returning: ())
                }
            }
        }
    }

    private func setLocalDescription(_ description: LKRTCSessionDescription, on connection: LKRTCPeerConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let gate = NativeRTCContinuationGate(continuation)
            gate.failAfterDeadline(operation: "set_local_description")
            connection.setLocalDescription(description) { error in
                if let error {
                    gate.resume(throwing: RoomRTCError.webRTCOperationFailed(error.localizedDescription))
                } else {
                    gate.resume(returning: ())
                }
            }
        }
    }

    private func add(_ candidate: LKRTCIceCandidate, to connection: LKRTCPeerConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let gate = NativeRTCContinuationGate(continuation)
            gate.failAfterDeadline(operation: "add_ice_candidate")
            connection.add(candidate) { error in
                if let error {
                    gate.resume(throwing: RoomRTCError.webRTCOperationFailed(error.localizedDescription))
                } else {
                    gate.resume(returning: ())
                }
            }
        }
    }

    @discardableResult
    private func setLifecycle(
        _ state: RoomLifecycleState,
        epoch: UInt64,
        connection: LKRTCPeerConnection
    ) -> Bool {
        lock.withLock {
            guard operationEpoch == epoch, peerConnection === connection else { return false }
            _lifecycle = state
            return true
        }
    }

    private func advanceOperationEpochLocked() -> UInt64 {
        operationEpoch &+= 1
        if operationEpoch == 0 {
            operationEpoch = 1
        }
        return operationEpoch
    }

    private func isOperationCurrent(
        _ epoch: UInt64,
        connection: LKRTCPeerConnection? = nil
    ) -> Bool {
        lock.withLock {
            guard operationEpoch == epoch else { return false }
            if let connection {
                return peerConnection === connection
            }
            return true
        }
    }

    private func ensureOperationCurrent(
        _ epoch: UInt64,
        connection: LKRTCPeerConnection? = nil
    ) throws {
        guard isOperationCurrent(epoch, connection: connection) else {
            throw RoomRTCError.operationCancelled
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

    private func recoverRemovedAudioDevicesIfNeeded() {
        let module = factory.audioDeviceModule
        let selections = lock.withLock { (selectedAudioInputID, selectedAudioOutputID) }
        if NativeDeviceSelectionRecovery.needsDefaultRecovery(
            selectedID: selections.0,
            availableIDs: module.inputDevices.map(\.deviceId)
        ) {
            lock.withLock { selectedAudioInputID = nil }
            let recovered = module.trySetInputDevice(nil)
            _ = lock.withLock {
                if recovered {
                    runtimeDegradations.insert(.selectedAudioInputRemovedUsingDefault)
                } else {
                    runtimeDegradations.insert(.audioDeviceRecoveryFailed)
                }
            }
        }
        if NativeDeviceSelectionRecovery.needsDefaultRecovery(
            selectedID: selections.1,
            availableIDs: module.outputDevices.map(\.deviceId)
        ) {
            lock.withLock { selectedAudioOutputID = nil }
            let recovered = module.trySetOutputDevice(nil)
            _ = lock.withLock {
                if recovered {
                    runtimeDegradations.insert(.selectedAudioOutputRemovedUsingDefault)
                } else {
                    runtimeDegradations.insert(.audioDeviceRecoveryFailed)
                }
            }
        }
    }

    private func deviceInventory() -> NativeMediaDeviceInventory {
        let module = factory.audioDeviceModule
        let activeInputID = module.inputDevice.deviceId
        let activeOutputID = module.outputDevice.deviceId
        let selectedCameraID = lock.withLock {
            activeCameraID ?? self.selectedCameraID
        }
        let defaultCameraID = Self.preferredCameraDevice()?.uniqueID
        return NativeMediaDeviceInventory(
            audioInputs: module.inputDevices.map { device in
                NativeMediaDevice(
                    id: device.deviceId,
                    uiDisplayName: device.name,
                    kind: .audioInput,
                    isDefault: device.isDefault,
                    isSelected: device.deviceId == activeInputID
                )
            },
            audioOutputs: module.outputDevices.map { device in
                NativeMediaDevice(
                    id: device.deviceId,
                    uiDisplayName: device.name,
                    kind: .audioOutput,
                    isDefault: device.isDefault,
                    isSelected: device.deviceId == activeOutputID
                )
            },
            cameras: LKRTCCameraVideoCapturer.captureDevices().map { device in
                NativeMediaDevice(
                    id: device.uniqueID,
                    uiDisplayName: device.localizedName,
                    kind: .camera,
                    isDefault: device.uniqueID == defaultCameraID,
                    isSelected: device.uniqueID == selectedCameraID
                )
            }
        )
    }

    private func publishMediaRuntimeState() {
        let handler = lock.withLock { mediaRuntimeStateHandler }
        guard let handler else { return }
        Task { [weak self] in
            guard let self else { return }
            await handler(await self.mediaRuntimeSnapshot())
        }
    }

    private static func audioDevice(
        id: String?,
        in devices: [LKRTCIODevice],
        kind: NativeMediaDeviceKind
    ) throws -> LKRTCIODevice? {
        guard let id else { return nil }
        guard let device = devices.first(where: { $0.deviceId == id }) else {
            throw RoomRTCError.mediaDeviceUnavailable(kind)
        }
        return device
    }

    private static func cameraDevice(id: String?) throws -> AVCaptureDevice {
        if let id {
            guard let device = LKRTCCameraVideoCapturer.captureDevices().first(where: { $0.uniqueID == id }) else {
                throw RoomRTCError.mediaDeviceUnavailable(.camera)
            }
            return device
        }
        guard let device = preferredCameraDevice() else {
            throw RoomRTCError.cameraUnavailable
        }
        return device
    }

    private static func audioProcessingRequestResult(
        _ code: LKRTCAudioProcessingOptionsResultCode
    ) -> NativeAudioProcessingRequestResult {
        switch code {
        case .applied: .applied
        case .stored: .stored
        case .rejectedRemoteTrack: .rejectedRemoteTrack
        case .rejectedInvalidCombination: .rejectedInvalidCombination
        case .rejectedPlatformUnavailable: .rejectedPlatformUnavailable
        case .applyFailed: .applyFailed
        @unknown default: .unknownFailure
        }
    }

    private static func audioProcessingSnapshot(
        state: LKRTCAudioProcessingState,
        platformState: LKRTCPlatformAudioProcessingState,
        requestResult: NativeAudioProcessingRequestResult,
        requested: NativeAudioProcessingRequestedConfiguration?
    ) -> NativeAudioProcessingSnapshot {
        NativeAudioProcessingSnapshot(
            requestResult: requestResult,
            hasAudioProcessingModule: state.hasAudioProcessingModule,
            echoCancellation: audioProcessingComponent(
                state.echoCancellation,
                requestedFallback: requested?.echoCancellation
            ),
            noiseSuppression: audioProcessingComponent(
                state.noiseSuppression,
                requestedFallback: requested?.noiseSuppression
            ),
            automaticGainControl: audioProcessingComponent(
                state.autoGainControl,
                requestedFallback: requested?.automaticGainControl
            ),
            highPassFilter: audioProcessingComponent(
                state.highPassFilter,
                requestedFallback: requested?.highPassFilter
            ),
            platformVoiceProcessing: NativePlatformVoiceProcessingSnapshot(
                enabledRequested: platformState.isVoiceProcessingEnabledRequested,
                enabledActive: platformState.isVoiceProcessingEnabledActive,
                bypassedRequested: platformState.isVoiceProcessingBypassedRequested,
                bypassedActive: platformState.isVoiceProcessingBypassedActive,
                automaticGainControlRequested: platformState.isVoiceProcessingAGCEnabledRequested,
                automaticGainControlActive: platformState.isVoiceProcessingAGCEnabledActive
            )
        )
    }

    private static func audioProcessingComponent(
        _ state: LKRTCAudioProcessingComponentState,
        requestedFallback: NativeAudioProcessingRequest?
    ) -> NativeAudioProcessingComponentSnapshot {
        NativeAudioProcessingComponentSnapshot(
            requested: state.requested.map {
                NativeAudioProcessingRequest(
                    enabled: $0.isEnabled,
                    mode: audioProcessingMode($0.mode)
                )
            } ?? requestedFallback,
            softwareResolved: state.isSoftwareResolved,
            softwareActive: state.isSoftwareActive,
            platformAvailable: state.isPlatformAvailable,
            platformResolved: state.isPlatformResolved,
            platformActive: state.isPlatformActive,
            effective: audioProcessingImplementation(state.effective)
        )
    }

    private static func requestedConfiguration(
        _ options: LKRTCAudioProcessingOptions
    ) -> NativeAudioProcessingRequestedConfiguration {
        NativeAudioProcessingRequestedConfiguration(
            echoCancellation: NativeAudioProcessingRequest(
                enabled: options.echoCancellation,
                mode: audioProcessingMode(options.echoCancellationMode)
            ),
            noiseSuppression: NativeAudioProcessingRequest(
                enabled: options.noiseSuppression,
                mode: audioProcessingMode(options.noiseSuppressionMode)
            ),
            automaticGainControl: NativeAudioProcessingRequest(
                enabled: options.autoGainControl,
                mode: audioProcessingMode(options.autoGainControlMode)
            ),
            highPassFilter: NativeAudioProcessingRequest(
                enabled: options.highPassFilter,
                mode: audioProcessingMode(options.highPassFilterMode)
            )
        )
    }

    private static func audioProcessingMode(
        _ mode: LKRTCAudioProcessingMode
    ) -> NativeAudioProcessingMode {
        switch mode {
        case .automatic: .automatic
        case .platform: .platform
        case .software: .software
        @unknown default: .unknown
        }
    }

    private static func audioProcessingImplementation(
        _ implementation: LKRTCAudioProcessingImplementation
    ) -> NativeAudioProcessingImplementation {
        switch implementation {
        case .unknown: .unknown
        case .disabled: .disabled
        case .software: .software
        case .platform: .platform
        case .softwareAndPlatform: .softwareAndPlatform
        @unknown default: .unknown
        }
    }

    private static func preferredCameraDevice(excluding excludedID: String? = nil) -> AVCaptureDevice? {
        let devices = LKRTCCameraVideoCapturer.captureDevices().filter { $0.uniqueID != excludedID }
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

extension NativeRoomRTCClient: LKRTCAudioDeviceModuleDelegate {
    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        didReceiveSpeechActivityEvent speechActivityEvent: LKRTCSpeechActivityEvent
    ) {}

    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        didCreateEngine engine: AVAudioEngine
    ) -> Int { 0 }

    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        willEnableEngine engine: AVAudioEngine,
        isPlayoutEnabled: Bool,
        isRecordingEnabled: Bool
    ) -> Int { 0 }

    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        willStartEngine engine: AVAudioEngine,
        isPlayoutEnabled: Bool,
        isRecordingEnabled: Bool
    ) -> Int { 0 }

    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        didStopEngine engine: AVAudioEngine,
        isPlayoutEnabled: Bool,
        isRecordingEnabled: Bool
    ) -> Int { 0 }

    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        didDisableEngine engine: AVAudioEngine,
        isPlayoutEnabled: Bool,
        isRecordingEnabled: Bool
    ) -> Int { 0 }

    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        willReleaseEngine engine: AVAudioEngine
    ) -> Int { 0 }

    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        engine: AVAudioEngine,
        configureInputFromSource source: AVAudioNode?,
        toDestination destination: AVAudioNode,
        format: AVAudioFormat,
        context: [AnyHashable: Any]
    ) -> Int { 0 }

    public func audioDeviceModule(
        _ audioDeviceModule: LKRTCAudioDeviceModule,
        engine: AVAudioEngine,
        configureOutputFromSource source: AVAudioNode,
        toDestination destination: AVAudioNode?,
        format: AVAudioFormat,
        context: [AnyHashable: Any]
    ) -> Int { 0 }

    public func audioDeviceModuleDidUpdateDevices(_ audioDeviceModule: LKRTCAudioDeviceModule) {
        recoverRemovedAudioDevicesIfNeeded()
        publishMediaRuntimeState()
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
        let handler = lock.withLock {
            self.peerConnection === peerConnection ? localCandidateHandler : nil
        }
        let payload = RTCIceCandidatePayload(
            candidate: candidate.sdp,
            sdpMid: candidate.sdpMid,
            sdpMLineIndex: Int(candidate.sdpMLineIndex)
        )

        guard let handler else { return }
        Task { [weak self] in
            guard let self,
                  self.lock.withLock({ self.peerConnection === peerConnection }) else { return }
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
        let handler = lock.withLock { () -> RemoteVideoTrackHandler? in
            guard self.peerConnection === peerConnection else { return nil }
            remoteVideoTracks[remoteTrack.id] = remoteTrack
            return remoteVideoTrackHandler
        }

        guard let handler else { return }
        Task { [weak self] in
            guard let self,
                  self.lock.withLock({ self.peerConnection === peerConnection }) else { return }
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
    public func didSourceCaptureStart(_ capturer: LKRTCDesktopCapturer) {
        let gate = lock.withLock { () -> NativeRTCContinuationGate<Void>? in
            guard desktopCapturer === capturer else { return nil }
            desktopCaptureStarted = true
            let value = desktopCaptureStartGate
            desktopCaptureStartGate = nil
            return value
        }
        gate?.resume(returning: ())
    }

    public func didSourceCapturePaused(_ capturer: LKRTCDesktopCapturer) {
        handleUnexpectedDesktopCaptureEnd(capturer, event: .capturePaused)
    }

    public func didSourceCaptureStop(_ capturer: LKRTCDesktopCapturer) {
        handleUnexpectedDesktopCaptureEnd(capturer, event: .captureStopped)
    }

    public func didSourceCaptureError(_ capturer: LKRTCDesktopCapturer) {
        handleUnexpectedDesktopCaptureEnd(capturer, event: .captureError)
    }

    private func handleUnexpectedDesktopCaptureEnd(
        _ capturer: LKRTCDesktopCapturer,
        event: NativeScreenShareRuntimeEvent
    ) {
        let state = lock.withLock { () -> (
            gate: NativeRTCContinuationGate<Void>?,
            handler: NativeScreenShareRuntimeStateHandler?,
            wasActive: Bool
        )? in
            guard desktopCapturer === capturer else { return nil }
            localVideoSender?.track = localVideoTrack
            let value = (
                gate: desktopCaptureStartGate,
                handler: screenShareRuntimeStateHandler,
                wasActive: desktopCaptureStarted
            )
            screenVideoSource = nil
            screenVideoTrack = nil
            desktopCapturer = nil
            desktopCaptureStartGate = nil
            desktopCaptureStarted = false
            return value
        }
        guard let state else { return }
        state.gate?.resume(throwing: RoomRTCError.screenShareUnavailable)
        Task { [weak self] in
            if state.wasActive {
                await state.handler?(event)
            }
            if event == .capturePaused, let self {
                self.recordCaptureStopResult(await self.stopDesktopCapture(capturer))
            }
        }
    }
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

    public func setMediaRuntimeStateHandler(_ handler: NativeMediaRuntimeStateHandler?) async {}

    public func prepareLocalMedia(audio: Bool, video: Bool) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    public func selectAudioInput(id: String?) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    public func selectAudioOutput(id: String?) async throws {
        throw RoomRTCError.webRTCUnavailable
    }

    public func selectCamera(id: String?) async throws {
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

    public func mediaRuntimeSnapshot() async -> NativeMediaRuntimeSnapshot {
        NativeMediaRuntimeSnapshot(
            degradations: [.audioProcessingRequestFailed]
        )
    }

    public func leave() async {
        lifecycle = .leaving
    }
}
#endif

public enum RoomRTCError: Error, Equatable {
    case cameraPermissionDenied
    case cameraCaptureFailed(String)
    case cameraFormatUnavailable
    case cameraUnavailable
    case missingSessionDescription
    case microphonePermissionDenied
    case operationCancelled
    case peerConnectionCreationFailed
    case peerConnectionNotConfigured
    case peerOperationTimedOut(String)
    case permissionRequestTimedOut(String)
    case screenCapturePermissionDenied
    case screenShareUnavailable
    case trackPublicationFailed(String)
    case webRTCOperationFailed(String)
    case webRTCUnavailable
}

private final class NativePermissionContinuationGate: @unchecked Sendable {
    private let lock = NSLock()
    private let operation: String
    private var continuation: CheckedContinuation<Bool, Error>?

    init(operation: String) {
        self.operation = operation
    }

    func install(_ continuation: CheckedContinuation<Bool, Error>) {
        lock.withLock {
            self.continuation = continuation
        }
    }

    func resume(returning value: Bool) {
        take()?.resume(returning: value)
    }

    func failAfterDeadline(nanoseconds: UInt64) {
        Task.detached { [weak self] in
            try? await Task.sleep(nanoseconds: nanoseconds)
            guard let self else { return }
            self.take()?.resume(
                throwing: RoomRTCError.permissionRequestTimedOut(self.operation)
            )
        }
    }

    private func take() -> CheckedContinuation<Bool, Error>? {
        lock.withLock {
            let value = continuation
            continuation = nil
            return value
        }
    }
}

internal final class NativeRTCContinuationGate<Value: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Value, Error>?

    init(_ continuation: CheckedContinuation<Value, Error>) {
        self.continuation = continuation
    }

    func resume(returning value: Value) {
        take()?.resume(returning: value)
    }

    func resume(throwing error: Error) {
        take()?.resume(throwing: error)
    }

    func failAfterDeadline(operation: String, nanoseconds: UInt64 = 8_000_000_000) {
        Task.detached { [self] in
            try? await Task.sleep(nanoseconds: nanoseconds)
            resume(throwing: RoomRTCError.peerOperationTimedOut(operation))
        }
    }

    private func take() -> CheckedContinuation<Value, Error>? {
        lock.lock()
        defer { lock.unlock() }
        let value = continuation
        continuation = nil
        return value
    }
}

internal final class NativeRTCStopContinuationGate: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Bool, Never>?

    init(_ continuation: CheckedContinuation<Bool, Never>) {
        self.continuation = continuation
    }

    func resume(returning value: Bool) {
        take()?.resume(returning: value)
    }

    func failAfterDeadline(nanoseconds: UInt64 = 5_000_000_000) {
        Task.detached { [self] in
            try? await Task.sleep(nanoseconds: nanoseconds)
            resume(returning: false)
        }
    }

    private func take() -> CheckedContinuation<Bool, Never>? {
        lock.withLock {
            let value = continuation
            continuation = nil
            return value
        }
    }
}

public extension RoomRTCError {
    static func audioProcessingRequestFailed(
        _ result: NativeAudioProcessingRequestResult
    ) -> RoomRTCError {
        .webRTCOperationFailed("native audio processing request failed: \(result.rawValue)")
    }

    static func invalidOfferLayout(_ failure: NativeOfferLayoutFailure) -> RoomRTCError {
        .webRTCOperationFailed("invalid native publisher offer: \(failure.rawValue)")
    }

    static func mediaDeviceSelectionFailed(_ kind: NativeMediaDeviceKind) -> RoomRTCError {
        .webRTCOperationFailed("native \(kind.rawValue) selection failed")
    }

    static func mediaDeviceUnavailable(_ kind: NativeMediaDeviceKind) -> RoomRTCError {
        .webRTCOperationFailed("native \(kind.rawValue) device unavailable")
    }
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
