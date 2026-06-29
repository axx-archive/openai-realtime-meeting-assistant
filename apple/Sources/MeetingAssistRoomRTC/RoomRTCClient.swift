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

public struct NativeMediaEvidenceClient: Codable, Equatable, Sendable {
    public var platform: String
    public var version: String

    public init(platform: String = "", version: String = "") {
        self.platform = platform
        self.version = version
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
            source: "remoteVideoTiles+inboundVideoDecoded",
            value: min(Double(remoteVideoTiles), source.inboundVideoDecoded),
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
        runId: String = "",
        roomId: String = ""
    ) {
        let assertions = NativeMediaEvidenceAssertions(
            cameraPublished: source.outboundVideoFramesSent > 0,
            microphonePublished: source.outboundAudioPacketsSent > 0,
            remoteAudioReceived: source.inboundAudioPacketsReceived > 0,
            remoteVideoRendered: remoteVideoTiles > 0 && source.inboundVideoDecoded > 0
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
