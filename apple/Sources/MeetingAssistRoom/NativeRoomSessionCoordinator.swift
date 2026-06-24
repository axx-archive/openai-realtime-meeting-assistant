import Foundation
import MeetingAssistAPI
import MeetingAssistCore
import MeetingAssistMedia
import MeetingAssistRoomRTC
import MeetingAssistSignaling

public protocol NativeRoomAPIProviding: Sendable {
    var baseURL: URL { get }
    func nativeConfig() async throws -> NativeClientConfig
    func login(name: String, password: String, path: String) async throws -> Participant
    func clientConfig(path: String) async throws -> ClientRTCConfig
}

extension MeetingAssistAPIClient: NativeRoomAPIProviding {}

public protocol NativeRoomSignalingTransport: Sendable {
    func connect(to url: URL) async
    func send(event: String, data: String) async throws
    func receive() async throws -> WebSocketEnvelope
    func close() async
}

public struct URLSessionRoomSignalingTransport: NativeRoomSignalingTransport {
    private let client: MeetingAssistSignalingClient

    public init(client: MeetingAssistSignalingClient = MeetingAssistSignalingClient()) {
        self.client = client
    }

    public func connect(to url: URL) async {
        await client.connect(to: url)
    }

    public func send(event: String, data: String) async throws {
        try await client.send(event: event, data: data)
    }

    public func receive() async throws -> WebSocketEnvelope {
        try await client.receive()
    }

    public func close() async {
        await client.close()
    }
}

public struct NativeRoomClientIdentity: Codable, Equatable, Sendable {
    public var platform: String
    public var version: String

    public init(platform: String, version: String) {
        self.platform = platform
        self.version = version
    }
}

public struct NativeRoomJoinResult: Equatable, Sendable {
    public var participant: Participant
    public var clientConfig: ClientRTCConfig
    public var websocketURL: URL
    public var answeredOffer: RTCSessionDescriptionPayload

    public init(
        participant: Participant,
        clientConfig: ClientRTCConfig,
        websocketURL: URL,
        answeredOffer: RTCSessionDescriptionPayload
    ) {
        self.participant = participant
        self.clientConfig = clientConfig
        self.websocketURL = websocketURL
        self.answeredOffer = answeredOffer
    }
}

public actor NativeRoomSessionCoordinator {
    public private(set) var lifecycle: RoomLifecycleState = .signedOut
    public private(set) var participant: Participant?
    public private(set) var nativeConfig: NativeClientConfig?
    public private(set) var clientConfig: ClientRTCConfig?

    private let api: NativeRoomAPIProviding
    private let signaling: NativeRoomSignalingTransport
    private let rtc: RoomRTCClient
    private let media: MediaSessionCoordinator
    private let clientIdentity: NativeRoomClientIdentity
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()
    private var pendingRemoteCandidates: [RTCIceCandidatePayload] = []
    private var remoteDescriptionReady = false

    public init(
        api: NativeRoomAPIProviding,
        signaling: NativeRoomSignalingTransport = URLSessionRoomSignalingTransport(),
        rtc: RoomRTCClient = NativeRoomRTCClient(),
        media: MediaSessionCoordinator = MediaSessionCoordinator(),
        clientIdentity: NativeRoomClientIdentity
    ) {
        self.api = api
        self.signaling = signaling
        self.rtc = rtc
        self.media = media
        self.clientIdentity = clientIdentity
    }

    public func joinAudioOnly(name: String, password: String) async throws -> NativeRoomJoinResult {
        resetNegotiationState()

        let discovery = try await api.nativeConfig()
        try validate(discovery)
        nativeConfig = discovery

        let signedInParticipant = try await api.login(
            name: name,
            password: password,
            path: discovery.auth.loginPath
        )
        participant = signedInParticipant
        lifecycle = .authenticated

        let config = try await api.clientConfig(path: discovery.room.clientConfigPath)
        clientConfig = config

        let websocketURL = Self.websocketURL(baseURL: api.baseURL, path: discovery.room.websocketPath)
        await signaling.connect(to: websocketURL)
        try await sendJSON(event: ClientSignalEvent.participant, payload: ParticipantPayload(client: clientIdentity))

        let admittedName = try await waitForAccessGrant()
        participant = Participant(name: admittedName, email: signedInParticipant.email)
        lifecycle = .admitted

        try await rtc.prepareLocalMedia(audio: true, video: false)
        lifecycle = .preparingMedia

        try await sendJSON(
            event: ClientSignalEvent.mediaReady,
            payload: MediaReadyPayload(client: clientIdentity, media: MediaCapabilities(audio: true, video: false))
        )

        let answer = try await waitForOfferAndAnswer()
        lifecycle = .connected

        return NativeRoomJoinResult(
            participant: participant ?? signedInParticipant,
            clientConfig: config,
            websocketURL: websocketURL,
            answeredOffer: answer
        )
    }

    public func handleServerEvent(_ envelope: WebSocketEnvelope) async throws {
        switch envelope.event {
        case ServerSignalEvent.candidate:
            let candidate = try decode(RTCIceCandidatePayload.self, fromJSONString: envelope.data)
            if remoteDescriptionReady {
                try await rtc.addRemoteCandidate(envelope.data)
            } else {
                pendingRemoteCandidates.append(candidate)
            }
        case ServerSignalEvent.offer:
            _ = try await answerOffer(envelope)
        case ServerSignalEvent.kanban:
            let event = try kanbanEvent(from: envelope)
            try throwIfTerminalKanbanEvent(event)
            if let grantName = try accessGrantName(from: event) {
                participant = Participant(name: grantName, email: participant?.email ?? "")
                lifecycle = .admitted
            }
        default:
            break
        }
    }

    public func sendParticipantMediaState() async throws {
        try await sendJSON(event: ClientSignalEvent.participantMediaState, payload: media.participantMediaState)
    }

    public func setMuted(_ muted: Bool) {
        media.setMuted(muted)
    }

    public func setCameraOff(_ off: Bool) {
        media.setCameraOff(off)
    }

    public func setScreenSharing(_ sharing: Bool) {
        media.setScreenSharing(sharing)
    }

    public func selectLayer(_ layer: String) async throws {
        try await sendJSON(event: ClientSignalEvent.selectLayer, payload: SelectLayerPayload(layer: layer))
    }

    public func requestICERestart(reason: String) async throws {
        await rtc.restartICE()
        lifecycle = .reconnecting
        try await sendJSON(event: ClientSignalEvent.restartICE, payload: RestartICEPayload(reason: reason))
    }

    public func leave() async {
        await rtc.leave()
        await signaling.close()
        resetNegotiationState()
        lifecycle = .leaving
    }

    public static func websocketURL(baseURL: URL, path: String) -> URL {
        var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) ?? URLComponents()
        components.scheme = baseURL.scheme == "https" ? "wss" : "ws"
        components.path = joinedPath(baseURL.path, path)
        components.query = nil
        components.fragment = nil
        return components.url ?? baseURL
    }

    private func waitForAccessGrant() async throws -> String {
        while true {
            let envelope = try await signaling.receive()
            guard envelope.event == ServerSignalEvent.kanban else {
                try await handleServerEvent(envelope)
                continue
            }
            let event = try kanbanEvent(from: envelope)
            try throwIfTerminalKanbanEvent(event)
            if let grantName = try accessGrantName(from: event) {
                return grantName
            }
        }
    }

    private func waitForOfferAndAnswer() async throws -> RTCSessionDescriptionPayload {
        while true {
            let envelope = try await signaling.receive()
            switch envelope.event {
            case ServerSignalEvent.offer:
                return try await answerOffer(envelope)
            default:
                try await handleServerEvent(envelope)
            }
        }
    }

    private func answerOffer(_ envelope: WebSocketEnvelope) async throws -> RTCSessionDescriptionPayload {
        let offer = try decode(RTCSessionDescriptionPayload.self, fromJSONString: envelope.data)
        guard offer.type == "offer" else {
            throw NativeRoomSessionError.unexpectedOfferType(offer.type)
        }
        lifecycle = .negotiating
        let answerSDP = try await rtc.handleOffer(offer.sdp)
        remoteDescriptionReady = true
        try await flushPendingRemoteCandidates()
        let answer = RTCSessionDescriptionPayload(type: "answer", sdp: answerSDP)
        try await sendJSON(event: ClientSignalEvent.answer, payload: answer)
        return answer
    }

    private func flushPendingRemoteCandidates() async throws {
        let candidates = pendingRemoteCandidates
        pendingRemoteCandidates.removeAll()
        for candidate in candidates {
            let data = try encoder.encode(candidate)
            try await rtc.addRemoteCandidate(String(decoding: data, as: UTF8.self))
        }
    }

    private func resetNegotiationState() {
        pendingRemoteCandidates.removeAll()
        remoteDescriptionReady = false
    }

    private func kanbanEvent(from envelope: WebSocketEnvelope) throws -> RoomEvent<JSONValue> {
        guard envelope.event == ServerSignalEvent.kanban else {
            throw NativeRoomSessionError.unexpectedSignal(envelope.event)
        }
        return try decode(RoomEvent<JSONValue>.self, fromJSONString: envelope.data)
    }

    private func throwIfTerminalKanbanEvent(_ event: RoomEvent<JSONValue>) throws {
        switch event.event {
        case "access_denied":
            throw NativeRoomSessionError.accessDenied(event.data.stringValue ?? "Access denied.")
        case "session_replaced":
            throw NativeRoomSessionError.sessionReplaced(event.data.stringValue ?? "This room session was replaced.")
        default:
            break
        }
    }

    private func accessGrantName(from event: RoomEvent<JSONValue>) throws -> String? {
        guard event.event == "access_granted" else { return nil }
        if case .object(let data) = event.data, case .string(let name)? = data["name"] {
            return name
        }
        throw NativeRoomSessionError.missingAccessGrantName
    }

    private func validate(_ config: NativeClientConfig) throws {
        guard config.protocolVersion == meetingAssistNativeProtocolV1 else {
            throw NativeRoomSessionError.unsupportedProtocol(config.protocolVersion)
        }
        guard config.auth.mode == "cookie" else {
            throw NativeRoomSessionError.unsupportedAuthMode(config.auth.mode)
        }
    }

    private func sendJSON<T: Encodable>(event: String, payload: T) async throws {
        let data = try encoder.encode(payload)
        try await signaling.send(event: event, data: String(decoding: data, as: UTF8.self))
    }

    private func decode<T: Decodable>(_ type: T.Type, fromJSONString string: String) throws -> T {
        try decoder.decode(type, from: Data(string.utf8))
    }

    private static func joinedPath(_ basePath: String, _ path: String) -> String {
        let cleanBase = basePath.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let cleanPath = path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        switch (cleanBase.isEmpty, cleanPath.isEmpty) {
        case (true, true): return "/"
        case (true, false): return "/" + cleanPath
        case (false, true): return "/" + cleanBase
        case (false, false): return "/" + cleanBase + "/" + cleanPath
        }
    }
}

public enum NativeRoomSessionError: Error, Equatable {
    case accessDenied(String)
    case missingAccessGrantName
    case sessionReplaced(String)
    case unexpectedOfferType(String)
    case unexpectedSignal(String)
    case unsupportedAuthMode(String)
    case unsupportedProtocol(String)
}

private extension JSONValue {
    var stringValue: String? {
        if case .string(let value) = self {
            return value
        }
        return nil
    }
}

private struct ParticipantPayload: Encodable {
    var client: NativeRoomClientIdentity
}

private struct MediaReadyPayload: Encodable {
    var client: NativeRoomClientIdentity
    var media: MediaCapabilities
}

private struct MediaCapabilities: Encodable {
    var audio: Bool
    var video: Bool
}

private struct RestartICEPayload: Encodable {
    var reason: String
}

private struct SelectLayerPayload: Encodable {
    var layer: String
}
