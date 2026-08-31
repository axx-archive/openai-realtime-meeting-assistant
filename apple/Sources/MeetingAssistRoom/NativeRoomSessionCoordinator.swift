import Foundation
import MeetingAssistAPI
import MeetingAssistCore
import MeetingAssistMedia
import MeetingAssistRoomRTC
import MeetingAssistSignaling

public protocol NativeRoomAPIProviding: Sendable {
    var baseURL: URL { get }
    func nativeConfig() async throws -> NativeClientConfig
    func nativeDiscovery() async throws -> NativeClientConfig
    func login(name: String, password: String, path: String) async throws -> Participant
    func clientConfig(path: String) async throws -> ClientRTCConfig
}

extension MeetingAssistAPIClient: NativeRoomAPIProviding {}

public extension NativeRoomAPIProviding {
    func nativeDiscovery() async throws -> NativeClientConfig {
        try await nativeConfig()
    }
}

public protocol NativeRoomSignalingTransport: Sendable {
    func connect(to url: URL) async
    func send(event: String, data: String) async throws
    func send(_ envelope: WebSocketEnvelope) async throws
    func receive() async throws -> WebSocketEnvelope
    func close() async
}

public extension NativeRoomSignalingTransport {
    func send(_ envelope: WebSocketEnvelope) async throws {
        try await send(event: envelope.event, data: envelope.data)
    }
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

    public func send(_ envelope: WebSocketEnvelope) async throws {
        try await client.send(envelope)
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

public enum NativeRoomEndpointIdentity {
    public static let storageKey = "meetingassist.native-room.endpoint-id.v1"

    public static func current(defaults: UserDefaults = .standard) -> String {
        if let stored = defaults.string(forKey: storageKey), isValid(stored) {
            return stored
        }
        let minted = "apple-\(UUID().uuidString)"
        defaults.set(minted, forKey: storageKey)
        return minted
    }

    public static func isValid(_ value: String) -> Bool {
        guard (8...64).contains(value.count) else { return false }
        return value.unicodeScalars.allSatisfy { scalar in
            CharacterSet.alphanumerics.contains(scalar) || scalar == "-" || scalar == "_"
        }
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

public typealias NativeRoomSnapshotHandler = @Sendable (RoomSnapshot) async -> Void
public typealias NativeBoardStateHandler = @Sendable (BoardState) async -> Void
public typealias NativeUndoAvailabilityHandler = @Sendable (Bool) async -> Void
public typealias NativeAssistantEventsHandler = @Sendable ([AssistantEvent]) async -> Void
public typealias NativeMemoryEntriesHandler = @Sendable ([MemoryEntry]) async -> Void
public typealias NativeMeetingArchiveHandler = @Sendable (MeetingArchiveResult) async -> Void
public typealias NativeScoutChatEventsHandler = @Sendable ([ScoutChatEvent]) async -> Void
public typealias NativeMediaRecoveryHandler = @Sendable (NativeMediaRecoveryEvent) async -> Void
public typealias NativeMediaEvidenceHandler = @Sendable (NativeMediaEvidenceSnapshot) async -> Void
public typealias NativeMediaEvidenceContextProvider = @Sendable () async -> NativeMediaEvidenceCaptureContext

public struct NativeMediaRecoveryEvent: Equatable, Sendable {
    public var stage: String
    public var message: String
    public var terminal: Bool

    public init(stage: String, message: String, terminal: Bool) {
        self.stage = stage
        self.message = message
        self.terminal = terminal
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
    private let endpointID: String
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()
    private var pendingRemoteCandidates: [RTCIceCandidatePayload] = []
    private var remoteDescriptionReady = false
    private var receiveTask: Task<Void, Never>?
    private var heartbeatTask: Task<Void, Never>?
    private var sessionGeneration: UInt64 = 0
    private var activeJoinGeneration: UInt64?
    private var remoteVideoTrackInfoHandler: NativeRemoteVideoTrackInfoHandler?
    private var remoteVideoTracksByID: [String: NativeRemoteVideoTrack] = [:]
    private var labelsByTrackID: [String: String] = [:]
    private var labelsByStreamID: [String: String] = [:]
    private var streamLabelConflicts: Set<String> = []
    private var lastParticipantTrackRequest: Date?
    private var roomSnapshotHandler: NativeRoomSnapshotHandler?
    private var boardStateHandler: NativeBoardStateHandler?
    private var undoAvailabilityHandler: NativeUndoAvailabilityHandler?
    private var assistantEventsHandler: NativeAssistantEventsHandler?
    private var memoryEntriesHandler: NativeMemoryEntriesHandler?
    private var meetingArchiveHandler: NativeMeetingArchiveHandler?
    private var scoutChatEventsHandler: NativeScoutChatEventsHandler?
    private var currentRoomSnapshot: RoomSnapshot?
    private var currentBoardState: BoardState?
    private var currentCanUndoDelete: Bool?
    private var currentAssistantEvents: [AssistantEvent] = []
    private var currentMemoryEntries: [MemoryEntry] = []
    private var currentMeetingArchive: MeetingArchiveResult?
    private var currentScoutChatEvents: [ScoutChatEvent] = []
    private var mediaRecoveryHandler: NativeMediaRecoveryHandler?
    private var mediaEvidenceHandler: NativeMediaEvidenceHandler?
    private var mediaQualityTask: Task<Void, Never>?
    private var previousMediaQualitySnapshot: NativeMediaQualitySnapshot?
    private var currentMediaEvidenceSnapshot: NativeMediaEvidenceSnapshot?
    private let mediaEvidenceContextProvider: NativeMediaEvidenceContextProvider
    private let mediaQualityReportIntervalNanoseconds: UInt64
    private let heartbeatIntervalNanoseconds: UInt64

    public init(
        api: NativeRoomAPIProviding,
        signaling: NativeRoomSignalingTransport = URLSessionRoomSignalingTransport(),
        rtc: RoomRTCClient = NativeRoomRTCClient(),
        media: MediaSessionCoordinator = MediaSessionCoordinator(),
        clientIdentity: NativeRoomClientIdentity,
        endpointID: String = NativeRoomEndpointIdentity.current(),
        mediaEvidenceContextProvider: @escaping NativeMediaEvidenceContextProvider = { NativeMediaEvidenceCaptureContext() },
        mediaQualityReportIntervalNanoseconds: UInt64 = 12_000_000_000,
        heartbeatIntervalNanoseconds: UInt64 = 15_000_000_000
    ) {
        self.api = api
        self.signaling = signaling
        self.rtc = rtc
        self.media = media
        self.clientIdentity = clientIdentity
        self.endpointID = NativeRoomEndpointIdentity.isValid(endpointID)
            ? endpointID
            : NativeRoomEndpointIdentity.current()
        self.mediaEvidenceContextProvider = mediaEvidenceContextProvider
        self.mediaQualityReportIntervalNanoseconds = mediaQualityReportIntervalNanoseconds
        self.heartbeatIntervalNanoseconds = max(1, heartbeatIntervalNanoseconds)
    }

    public func joinAudioOnly(name: String, password: String) async throws -> NativeRoomJoinResult {
        try await join(name: name, password: password, video: false)
    }

    public func joinWithCamera(name: String, password: String) async throws -> NativeRoomJoinResult {
        try await join(name: name, password: password, video: true)
    }

    private func join(name: String, password: String, video: Bool) async throws -> NativeRoomJoinResult {
        guard activeJoinGeneration == nil else {
            throw NativeRoomControlError.joinAlreadyInProgress
        }
        let generation = beginSessionGeneration()
        activeJoinGeneration = generation

        do {
            await closeSessionResources(lifecycleAfterClose: .signedOut)
            try ensureSessionCurrent(generation)

            let discovery = try await api.nativeDiscovery()
            try ensureSessionCurrent(generation)
            try validate(discovery)
            nativeConfig = discovery

            let signedInParticipant = try await api.login(
                name: name,
                password: password,
                path: discovery.auth.loginPath
            )
            try ensureSessionCurrent(generation)
            participant = signedInParticipant
            lifecycle = .authenticated

            let config = try await api.clientConfig(path: discovery.room.clientConfigPath)
            try ensureSessionCurrent(generation)
            clientConfig = config

            let websocketURL = Self.websocketURL(baseURL: api.baseURL, path: discovery.room.websocketPath)
            await signaling.connect(to: websocketURL)
            try ensureSessionCurrent(generation)
            try await sendJSON(
                event: ClientSignalEvent.participant,
                payload: ParticipantPayload(endpointId: endpointID, client: clientIdentity)
            )
            try ensureSessionCurrent(generation)
            startHeartbeat(generation: generation)

            let admittedName = try await waitForAccessGrant(generation: generation)
            try ensureSessionCurrent(generation)
            participant = Participant(name: admittedName, email: signedInParticipant.email)
            lifecycle = .admitted

            do {
                try await rtc.configure(config)
            } catch {
                await reportMediaError(stage: "configure_peer_connection", error: error, generation: generation)
                throw error
            }
            try ensureSessionCurrent(generation)
            await rtc.setLocalCandidateHandler { [weak self] candidate in
                guard let self else { return }
                await self.sendLocalCandidate(candidate, generation: generation)
            }
            await rtc.setRemoteVideoTrackHandler { [weak self] track in
                guard let self else { return }
                await self.handleRemoteVideoTrack(track, generation: generation)
            }
            await rtc.setScreenShareRuntimeStateHandler { [weak self] event in
                guard let self else { return }
                await self.handleScreenShareRuntimeEvent(event, generation: generation)
            }
            try ensureSessionCurrent(generation)

            media.setCameraOff(!video)
            do {
                try media.configureVideoChatAudioSession()
            } catch {
                await reportMediaError(stage: "configure_audio_session", error: error, generation: generation)
                throw error
            }
            do {
                try await rtc.prepareLocalMedia(audio: true, video: video)
            } catch {
                await reportMediaError(stage: video ? "prepare_camera_media" : "prepare_audio_media", error: error, generation: generation)
                throw error
            }
            try ensureSessionCurrent(generation)
            lifecycle = .preparingMedia

            try await sendJSON(
                event: ClientSignalEvent.mediaReady,
                payload: MediaReadyPayload(client: clientIdentity, media: MediaCapabilities(audio: true, video: video))
            )
            try ensureSessionCurrent(generation)

            let answer = try await waitForOfferAndAnswer(generation: generation)
            try ensureSessionCurrent(generation)
            try await sendParticipantMediaState()
            try ensureSessionCurrent(generation)
            lifecycle = .connected
            activeJoinGeneration = nil
            startReceiveLoop(generation: generation)
            startMediaQualityReporting(generation: generation)

            return NativeRoomJoinResult(
                participant: participant ?? signedInParticipant,
                clientConfig: config,
                websocketURL: websocketURL,
                answeredOffer: answer
            )
        } catch {
            if isSessionCurrent(generation) {
                retireSessionGeneration(generation)
                await closeSessionResources(
                    lifecycleAfterClose: participant == nil ? .signedOut : .authenticated
                )
            }
            if activeJoinGeneration == generation {
                activeJoinGeneration = nil
            }
            throw error
        }
    }

    public func handleServerEvent(_ envelope: WebSocketEnvelope) async throws {
        let generation = sessionGeneration == 0 ? nil : sessionGeneration
        try await handleServerEvent(envelope, generation: generation)
    }

    private func handleServerEvent(
        _ envelope: WebSocketEnvelope,
        generation: UInt64?
    ) async throws {
        try ensureSessionCurrentIfPresent(generation)
        switch envelope.event {
        case ServerSignalEvent.candidate:
            let candidate = try decode(RTCIceCandidatePayload.self, fromJSONString: envelope.data)
            if remoteDescriptionReady {
                do {
                    try await rtc.addRemoteCandidate(envelope.data)
                    try ensureSessionCurrentIfPresent(generation)
                } catch {
                    try ensureSessionCurrentIfPresent(generation)
                    await reportMediaError(stage: "add_remote_candidate", error: error, generation: generation)
                    throw error
                }
            } else {
                try ensureSessionCurrentIfPresent(generation)
                pendingRemoteCandidates.append(candidate)
            }
        case ServerSignalEvent.offer:
            _ = try await answerOffer(envelope, generation: generation)
        case ServerSignalEvent.kanban:
            let event = try kanbanEvent(from: envelope)
            try throwIfTerminalKanbanEvent(event)
            try await handleKanbanRoomEvent(event, generation: generation)
            try ensureSessionCurrentIfPresent(generation)
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

    public func setRemoteVideoTrackHandler(_ handler: NativeRemoteVideoTrackInfoHandler?) async {
        remoteVideoTrackInfoHandler = handler
        guard let handler else { return }
        for track in remoteVideoTracksByID.values {
            await handler(remoteVideoTrackInfo(for: track))
        }
    }

    public func setRoomSnapshotHandler(_ handler: NativeRoomSnapshotHandler?) async {
        roomSnapshotHandler = handler
        guard let handler, let currentRoomSnapshot else { return }
        await handler(currentRoomSnapshot)
    }

    public func setBoardStateHandler(_ handler: NativeBoardStateHandler?) async {
        boardStateHandler = handler
        guard let handler, let currentBoardState else { return }
        await handler(currentBoardState)
    }

    public func setUndoAvailabilityHandler(_ handler: NativeUndoAvailabilityHandler?) async {
        undoAvailabilityHandler = handler
        guard let handler, let currentCanUndoDelete else { return }
        await handler(currentCanUndoDelete)
    }

    public func setAssistantEventsHandler(_ handler: NativeAssistantEventsHandler?) async {
        assistantEventsHandler = handler
        guard let handler, !currentAssistantEvents.isEmpty else { return }
        await handler(currentAssistantEvents)
    }

    public func setMemoryEntriesHandler(_ handler: NativeMemoryEntriesHandler?) async {
        memoryEntriesHandler = handler
        guard let handler, !currentMemoryEntries.isEmpty else { return }
        await handler(currentMemoryEntries)
    }

    public func setMeetingArchiveHandler(_ handler: NativeMeetingArchiveHandler?) async {
        meetingArchiveHandler = handler
        guard let handler, let currentMeetingArchive else { return }
        await handler(currentMeetingArchive)
    }

    public func setScoutChatEventsHandler(_ handler: NativeScoutChatEventsHandler?) async {
        scoutChatEventsHandler = handler
        guard let handler, !currentScoutChatEvents.isEmpty else { return }
        await handler(currentScoutChatEvents)
    }

    public func setMediaRecoveryHandler(_ handler: NativeMediaRecoveryHandler?) async {
        mediaRecoveryHandler = handler
    }

    public func setMediaEvidenceHandler(_ handler: NativeMediaEvidenceHandler?) async {
        mediaEvidenceHandler = handler
        guard let handler, let currentMediaEvidenceSnapshot else { return }
        await handler(currentMediaEvidenceSnapshot)
    }

    public func setMediaRuntimeStateHandler(_ handler: NativeMediaRuntimeStateHandler?) async {
        await rtc.setMediaRuntimeStateHandler(handler)
    }

    public func selectAudioInput(id: String?) async throws {
        try await rtc.selectAudioInput(id: id)
    }

    public func selectAudioOutput(id: String?) async throws {
        try await rtc.selectAudioOutput(id: id)
    }

    public func selectCamera(id: String?) async throws {
        try await rtc.selectCamera(id: id)
    }

    public func mediaRuntimeSnapshot() async -> NativeMediaRuntimeSnapshot {
        await rtc.mediaRuntimeSnapshot()
    }

    public func setMuted(_ muted: Bool) async {
        media.setMuted(muted)
        await rtc.setLocalAudioEnabled(!muted)
    }

    public func setCameraOff(_ off: Bool) async {
        media.setCameraOff(off)
        await rtc.setLocalVideoEnabled(!off)
    }

    public func setRecordingEnabled(_ enabled: Bool) async throws {
        try await sendJSON(event: ClientSignalEvent.setRecording, payload: SetRecordingPayload(enabled: enabled))
    }

    public func archiveMeeting() async throws {
        try await sendJSON(event: ClientSignalEvent.archiveMeeting, payload: EmptyPayload())
    }

    public func askAssistant(_ query: String) async throws {
        try await sendJSON(event: ClientSignalEvent.assistantQuery, payload: AssistantQueryPayload(query: query))
    }

    public func sendScoutChat(_ text: String) async throws {
        try await sendJSON(event: ClientSignalEvent.scoutChat, payload: ScoutChatPayload(text: text))
    }

    public func resetScoutChat() async throws {
        try await sendJSON(event: ClientSignalEvent.scoutChatReset, payload: EmptyPayload())
    }

    public func createBoardCard(_ payload: BoardCardMutationPayload) async throws {
        try await sendJSON(event: ClientSignalEvent.manualCreateTicket, payload: payload)
    }

    public func updateBoardCard(id: String, payload: BoardCardMutationPayload) async throws {
        var updatePayload = payload
        updatePayload.cardID = id
        try await sendJSON(event: ClientSignalEvent.manualUpdateTicket, payload: updatePayload)
    }

    public func deleteBoardCard(id: String) async throws {
        try await sendJSON(event: ClientSignalEvent.manualDeleteTicket, payload: BoardCardDeletePayload(cardID: id))
    }

    public func undoDeletedBoardCard() async throws {
        try await sendJSON(event: ClientSignalEvent.undoDeleteTicket, payload: EmptyPayload())
    }

    public func setScreenSharing(_ sharing: Bool) async throws {
        let generation = sessionGeneration == 0 ? nil : sessionGeneration
        do {
            try await rtc.setScreenShareEnabled(sharing)
            try ensureSessionCurrentIfPresent(generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(
                stage: sharing ? "screen_share_start" : "screen_share_stop",
                error: error,
                generation: generation
            )
            throw error
        }
        media.setScreenSharing(sharing)

        do {
            if sharing {
                try await sendParticipantMediaState()
                try ensureSessionCurrentIfPresent(generation)
                try await sendJSON(event: ClientSignalEvent.screenShareStarted, payload: EmptyPayload())
                try ensureSessionCurrentIfPresent(generation)
            } else {
                try await sendJSON(event: ClientSignalEvent.screenShareStopped, payload: EmptyPayload())
                try ensureSessionCurrentIfPresent(generation)
                try await sendParticipantMediaState()
                try ensureSessionCurrentIfPresent(generation)
            }
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            if sharing {
                await rollbackScreenShareStart(generation: generation)
                try ensureSessionCurrentIfPresent(generation)
            }
            await reportMediaError(
                stage: sharing ? "screen_share_start_signaling" : "screen_share_stop_signaling",
                error: error,
                generation: generation
            )
            throw error
        }
    }

    private func rollbackScreenShareStart(generation: UInt64?) async {
        guard (try? ensureSessionCurrentIfPresent(generation)) != nil else { return }
        do {
            try await rtc.setScreenShareEnabled(false)
        } catch {
            guard (try? ensureSessionCurrentIfPresent(generation)) != nil else { return }
            await reportMediaError(
                stage: "screen_share_start_rollback_capture",
                error: error,
                generation: generation
            )
        }
        guard (try? ensureSessionCurrentIfPresent(generation)) != nil else { return }
        media.setScreenSharing(false)
        do {
            try await sendJSON(event: ClientSignalEvent.screenShareStopped, payload: EmptyPayload())
        } catch {
            guard (try? ensureSessionCurrentIfPresent(generation)) != nil else { return }
            await reportMediaError(
                stage: "screen_share_start_rollback_stop_signal",
                error: error,
                generation: generation
            )
        }
        guard (try? ensureSessionCurrentIfPresent(generation)) != nil else { return }
        do {
            try await sendParticipantMediaState()
        } catch {
            guard (try? ensureSessionCurrentIfPresent(generation)) != nil else { return }
            await reportMediaError(
                stage: "screen_share_start_rollback_media_state",
                error: error,
                generation: generation
            )
        }
    }

    private func handleScreenShareRuntimeEvent(
        _ event: NativeScreenShareRuntimeEvent,
        generation: UInt64
    ) async {
        guard isSessionCurrent(generation) else { return }
        media.setScreenSharing(false)
        do {
            try await sendJSON(event: ClientSignalEvent.screenShareStopped, payload: EmptyPayload())
            try ensureSessionCurrent(generation)
        } catch {
            guard isSessionCurrent(generation) else { return }
            await reportMediaError(
                stage: "screen_share_runtime_stop_signal",
                error: error,
                generation: generation
            )
        }
        guard isSessionCurrent(generation) else { return }
        do {
            try await sendParticipantMediaState()
            try ensureSessionCurrent(generation)
        } catch {
            guard isSessionCurrent(generation) else { return }
            await reportMediaError(
                stage: "screen_share_runtime_media_state",
                error: error,
                generation: generation
            )
        }
        guard isSessionCurrent(generation) else { return }
        await mediaRecoveryHandler?(
            NativeMediaRecoveryEvent(
                stage: "screen_share_ended",
                message: "Screen sharing stopped unexpectedly. Camera video was restored.",
                terminal: false
            )
        )
    }

    public func selectLayer(_ layer: String) async throws {
        try await sendJSON(event: ClientSignalEvent.selectLayer, payload: SelectLayerPayload(layer: layer))
    }

    public func requestICERestart(reason: String) async throws {
        let generation = sessionGeneration == 0 ? nil : sessionGeneration
        await rtc.restartICE()
        try ensureSessionCurrentIfPresent(generation)
        lifecycle = .reconnecting
        do {
            try await sendJSON(event: ClientSignalEvent.restartICE, payload: RestartICEPayload(reason: reason))
            try ensureSessionCurrentIfPresent(generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(stage: "restart_ice", error: error, generation: generation)
            throw error
        }
    }

    public func sendMediaQualityReport() async throws {
        let generation = sessionGeneration == 0 ? nil : sessionGeneration
        try await publishMediaQualityReport(generation: generation)
    }

    public func captureMediaEvidenceSnapshot() async throws -> NativeMediaEvidenceSnapshot {
        let generation = sessionGeneration == 0 ? nil : sessionGeneration
        let snapshot: NativeMediaQualitySnapshot
        do {
            snapshot = try await rtc.mediaQualitySnapshot()
            try ensureSessionCurrentIfPresent(generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(stage: "media_evidence_snapshot", error: error, generation: generation)
            throw error
        }
        let evidence = try await mediaEvidenceSnapshot(
            from: snapshot,
            capturedAt: Self.iso8601String(Date()),
            generation: generation
        )
        try ensureSessionCurrentIfPresent(generation)
        currentMediaEvidenceSnapshot = evidence
        await mediaEvidenceHandler?(evidence)
        try ensureSessionCurrentIfPresent(generation)
        return evidence
    }

    public func captureTurnRelayObservation(network: String) async throws -> NativeTurnRelayObservation {
        let generation = sessionGeneration == 0 ? nil : sessionGeneration
        let trimmedNetwork = network.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let clientConfig else {
            throw RoomRTCError.webRTCOperationFailed("Client RTC config is unavailable.")
        }
        let snapshot: NativeMediaQualitySnapshot
        do {
            snapshot = try await rtc.mediaQualitySnapshot()
            try ensureSessionCurrentIfPresent(generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(stage: "turn_relay_observation", error: error, generation: generation)
            throw error
        }
        let evidence = try await mediaEvidenceSnapshot(
            from: snapshot,
            capturedAt: Self.iso8601String(Date()),
            generation: generation
        )
        try ensureSessionCurrentIfPresent(generation)
        currentMediaEvidenceSnapshot = evidence
        await mediaEvidenceHandler?(evidence)
        try ensureSessionCurrentIfPresent(generation)
        return try NativeTurnRelayObservation(
            evidence: evidence,
            iceReadiness: NativeICEReadinessSummary(rtcConfiguration: clientConfig.rtcConfiguration),
            network: trimmedNetwork
        )
    }

    public func leave() async {
        sessionGeneration &+= 1
        if sessionGeneration == 0 {
            sessionGeneration = 1
        }
        activeJoinGeneration = nil
        await closeSessionResources(lifecycleAfterClose: .leaving)
    }

    private func beginSessionGeneration() -> UInt64 {
        sessionGeneration &+= 1
        if sessionGeneration == 0 {
            sessionGeneration = 1
        }
        return sessionGeneration
    }

    private func isSessionCurrent(_ generation: UInt64) -> Bool {
        generation != 0 && generation == sessionGeneration
    }

    private func ensureSessionCurrent(_ generation: UInt64) throws {
        guard isSessionCurrent(generation) else {
            throw NativeRoomControlError.sessionCancelled
        }
    }

    private func ensureSessionCurrentIfPresent(_ generation: UInt64?) throws {
        guard let generation else { return }
        try ensureSessionCurrent(generation)
    }

    private func retireSessionGeneration(_ generation: UInt64) {
        guard isSessionCurrent(generation) else { return }
        sessionGeneration &+= 1
        if sessionGeneration == 0 {
            sessionGeneration = 1
        }
        if activeJoinGeneration == generation {
            activeJoinGeneration = nil
        }
    }

    private func closeSessionResources(lifecycleAfterClose: RoomLifecycleState) async {
        stopReceiveLoop()
        stopHeartbeat()
        stopMediaQualityReporting()
        media.setScreenSharing(false)
        await rtc.setLocalCandidateHandler(nil)
        await rtc.setRemoteVideoTrackHandler(nil)
        await rtc.setScreenShareRuntimeStateHandler(nil)
        // Free the room seat before any best-effort capture shutdown. The RTC
        // implementation closes the peer immediately and bounds hardware
        // callbacks, but signaling ownership must never depend on a device
        // service returning promptly.
        await signaling.close()
        await rtc.leave()
        resetNegotiationState()
        resetRemoteVideoState()
        resetRoomState()
        lifecycle = lifecycleAfterClose
    }

    private func closeTerminalSession(
        generation: UInt64,
        recovery: NativeMediaRecoveryEvent?
    ) async {
        guard isSessionCurrent(generation) else { return }
        retireSessionGeneration(generation)
        if let recovery {
            await mediaRecoveryHandler?(recovery)
        }
        await closeSessionResources(lifecycleAfterClose: .reconnecting)
    }

    private func startHeartbeat(generation: UInt64) {
        stopHeartbeat()
        heartbeatTask = Task { [weak self] in
            await self?.heartbeatLoop(generation: generation)
        }
    }

    private func stopHeartbeat() {
        heartbeatTask?.cancel()
        heartbeatTask = nil
    }

    private func heartbeatLoop(generation: UInt64) async {
        while !Task.isCancelled && isSessionCurrent(generation) {
            do {
                try await Task.sleep(nanoseconds: heartbeatIntervalNanoseconds)
                try Task.checkCancellation()
                try ensureSessionCurrent(generation)
                try await sendJSON(event: ClientSignalEvent.roomPing, payload: EmptyPayload())
            } catch is CancellationError {
                return
            } catch NativeRoomControlError.sessionCancelled {
                return
            } catch {
                await closeTerminalSession(
                    generation: generation,
                    recovery: NativeMediaRecoveryEvent(
                        stage: "signaling_disconnected",
                        message: "Room signaling disconnected. Rejoin the room.",
                        terminal: true
                    )
                )
                return
            }
        }
    }

    public static func websocketURL(baseURL: URL, path: String) -> URL {
        var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) ?? URLComponents()
        components.scheme = baseURL.scheme == "https" ? "wss" : "ws"
        components.path = joinedPath(baseURL.path, path)
        components.query = nil
        components.fragment = nil
        return components.url ?? baseURL
    }

    private func waitForAccessGrant(generation: UInt64) async throws -> String {
        while true {
            let envelope = try await signaling.receive()
            try ensureSessionCurrent(generation)
            guard envelope.event == ServerSignalEvent.kanban else {
                try await handleServerEvent(envelope, generation: generation)
                continue
            }
            let event = try kanbanEvent(from: envelope)
            try throwIfTerminalKanbanEvent(event)
            if let grantName = try accessGrantName(from: event) {
                return grantName
            }
            try await handleKanbanRoomEvent(event, generation: generation)
            try ensureSessionCurrent(generation)
        }
    }

    private func waitForOfferAndAnswer(generation: UInt64) async throws -> RTCSessionDescriptionPayload {
        while true {
            let envelope = try await signaling.receive()
            try ensureSessionCurrent(generation)
            switch envelope.event {
            case ServerSignalEvent.offer:
                return try await answerOffer(envelope, generation: generation)
            default:
                try await handleServerEvent(envelope, generation: generation)
            }
        }
    }

    private func answerOffer(
        _ envelope: WebSocketEnvelope,
        generation: UInt64?
    ) async throws -> RTCSessionDescriptionPayload {
        try ensureSessionCurrentIfPresent(generation)
        let offer = try decode(RTCSessionDescriptionPayload.self, fromJSONString: envelope.data)
        guard offer.type == "offer" else {
            throw NativeRoomSessionError.unexpectedOfferType(offer.type)
        }
        lifecycle = .negotiating
        let answerSDP: String
        do {
            answerSDP = try await rtc.handleOffer(offer.sdp)
            try ensureSessionCurrentIfPresent(generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(stage: "answer_offer", error: error, generation: generation)
            throw error
        }
        remoteDescriptionReady = true
        do {
            try await flushPendingRemoteCandidates(generation: generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(stage: "add_remote_candidate", error: error, generation: generation)
            throw error
        }
        let answer = RTCSessionDescriptionPayload(type: "answer", sdp: answerSDP)
        do {
            try ensureSessionCurrentIfPresent(generation)
            try await sendJSON(
                event: ClientSignalEvent.answer,
                payload: answer,
                offerId: envelope.offerId,
                revision: envelope.revision
            )
            try ensureSessionCurrentIfPresent(generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(stage: "send_answer", error: error, generation: generation)
            throw error
        }
        return answer
    }

    private func flushPendingRemoteCandidates(generation: UInt64?) async throws {
        try ensureSessionCurrentIfPresent(generation)
        let candidates = pendingRemoteCandidates
        pendingRemoteCandidates.removeAll()
        for candidate in candidates {
            let data = try encoder.encode(candidate)
            try await rtc.addRemoteCandidate(String(decoding: data, as: UTF8.self))
            try ensureSessionCurrentIfPresent(generation)
        }
    }

    private func resetNegotiationState() {
        pendingRemoteCandidates.removeAll()
        remoteDescriptionReady = false
    }

    private func resetRemoteVideoState() {
        remoteVideoTracksByID.removeAll()
        labelsByTrackID.removeAll()
        labelsByStreamID.removeAll()
        streamLabelConflicts.removeAll()
        lastParticipantTrackRequest = nil
    }

    private func resetRoomState() {
        currentRoomSnapshot = nil
        currentBoardState = nil
        currentCanUndoDelete = nil
        currentAssistantEvents.removeAll()
        currentMemoryEntries.removeAll()
        currentMeetingArchive = nil
        currentScoutChatEvents.removeAll()
        currentMediaEvidenceSnapshot = nil
    }

    private func kanbanEvent(from envelope: WebSocketEnvelope) throws -> RoomEvent<JSONValue> {
        guard envelope.event == ServerSignalEvent.kanban else {
            throw NativeRoomSessionError.unexpectedSignal(envelope.event)
        }
        return try decode(RoomEvent<JSONValue>.self, fromJSONString: envelope.data)
    }

    private func decodeKanbanData<T: Decodable>(_ type: T.Type, from data: JSONValue) throws -> T {
        let encoded = try encoder.encode(data)
        return try decoder.decode(type, from: encoded)
    }

    private func participantTrackMetadata(from data: JSONValue) throws -> NativeParticipantTrackMetadata {
        try decodeKanbanData(NativeParticipantTrackMetadata.self, from: data)
    }

    private func throwIfTerminalKanbanEvent(_ event: RoomEvent<JSONValue>) throws {
        switch event.event {
        case "access_denied":
            throw NativeRoomSessionError.accessDenied(event.data.stringValue ?? "Access denied.")
        case "session_replaced":
            throw NativeRoomSessionError.sessionReplaced(event.data.stringValue ?? "This room session was replaced.")
        case "room_closed":
            throw NativeRoomControlError.roomClosed(event.data.stringValue ?? "This room was closed.")
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

    private func sendJSON<T: Encodable>(
        event: String,
        payload: T,
        offerId: String? = nil,
        revision: UInt64? = nil
    ) async throws {
        let data = try encoder.encode(payload)
        try await signaling.send(
            WebSocketEnvelope(
                event: event,
                data: String(decoding: data, as: UTF8.self),
                offerId: offerId,
                revision: revision
            )
        )
    }

    private func handleKanbanRoomEvent(
        _ event: RoomEvent<JSONValue>,
        generation: UInt64?
    ) async throws {
        try ensureSessionCurrentIfPresent(generation)
        switch event.event {
        case "participants":
            let snapshot = try decodeKanbanData(RoomSnapshot.self, from: event.data)
            currentRoomSnapshot = snapshot
            await roomSnapshotHandler?(snapshot)
            try ensureSessionCurrentIfPresent(generation)
        case "board":
            let state = try decodeKanbanData(BoardState.self, from: event.data)
            currentBoardState = state
            await boardStateHandler?(state)
            try ensureSessionCurrentIfPresent(generation)
        case "undo_available":
            let canUndo = try decodeKanbanData(Bool.self, from: event.data)
            currentCanUndoDelete = canUndo
            await undoAvailabilityHandler?(canUndo)
            try ensureSessionCurrentIfPresent(generation)
        case "participant_track":
            let metadata = try participantTrackMetadata(from: event.data)
            try await handleParticipantTrack(metadata, generation: generation)
        case "screen_share_started":
            let payload = try decodeKanbanData(ScreenSharePayload.self, from: event.data)
            try await updateParticipantScreenSharing(
                name: payload.name,
                sharing: true,
                generation: generation
            )
        case "screen_share_stopped":
            let payload = try decodeKanbanData(ScreenSharePayload.self, from: event.data)
            try await updateParticipantScreenSharing(
                name: payload.name,
                sharing: false,
                generation: generation
            )
        case "media_disconnected":
            let message = event.data.stringValue ?? "Media connection ended. Rejoin the room."
            lifecycle = .reconnecting
            await mediaRecoveryHandler?(
                NativeMediaRecoveryEvent(
                    stage: "media_disconnected",
                    message: message,
                    terminal: true
                )
            )
            try ensureSessionCurrentIfPresent(generation)
        case "assistant_event":
            let assistantEvent = try decodeKanbanData(AssistantEvent.self, from: event.data)
            try await appendAssistantEvent(assistantEvent, generation: generation)
            if let artifact = assistantEvent.artifact {
                try await appendMemoryEntry(artifact, generation: generation)
            }
        case "memory":
            let entries = try decodeKanbanData([MemoryEntry].self, from: event.data)
            currentMemoryEntries = entries
            await memoryEntriesHandler?(entries)
            try ensureSessionCurrentIfPresent(generation)
        case "memory_transcript", "memory_brain", "memory_board_update":
            let entry = try decodeKanbanData(MemoryEntry.self, from: event.data)
            try await appendMemoryEntry(entry, generation: generation)
        case "memory_answer":
            let answer = try decodeKanbanData(MemoryAnswerResult.self, from: event.data)
            let entry = MemoryEntry(
                id: "answer-\(currentMemoryEntries.count + 1)",
                kind: "answer",
                text: answer.answer,
                metadata: answer.query.isEmpty ? nil : ["query": answer.query]
            )
            try await appendMemoryEntry(entry, generation: generation)
        case "meeting_archived":
            let archive = try decodeKanbanData(MeetingArchiveResult.self, from: event.data)
            currentMeetingArchive = archive
            await meetingArchiveHandler?(archive)
            try ensureSessionCurrentIfPresent(generation)
            try await appendAssistantEvent(
                AssistantEvent(
                    kind: "archive",
                    text: archive.summary,
                    createdAt: archive.archivedAt,
                    downloadURL: archive.downloadURL,
                    artifact: archive.artifact
                ),
                generation: generation
            )
            if let artifact = archive.artifact {
                try await appendMemoryEntry(artifact, generation: generation)
            }
        case "scout_chat":
            let chatEvent = try decodeKanbanData(ScoutChatEvent.self, from: event.data)
            try await appendScoutChatEvent(chatEvent, generation: generation)
            if let artifact = chatEvent.artifact {
                try await appendMemoryEntry(artifact, generation: generation)
            }
            if let artifact = chatEvent.thread?.artifact {
                try await appendMemoryEntry(artifact, generation: generation)
            }
        default:
            break
        }
    }

    private func appendAssistantEvent(
        _ event: AssistantEvent,
        generation: UInt64?
    ) async throws {
        try ensureSessionCurrentIfPresent(generation)
        guard !event.displayText.isEmpty else { return }
        currentAssistantEvents.append(event)
        if currentAssistantEvents.count > 40 {
            currentAssistantEvents.removeFirst(currentAssistantEvents.count - 40)
        }
        await assistantEventsHandler?(currentAssistantEvents)
        try ensureSessionCurrentIfPresent(generation)
    }

    private func appendMemoryEntry(
        _ entry: MemoryEntry,
        generation: UInt64?
    ) async throws {
        try ensureSessionCurrentIfPresent(generation)
        guard !entry.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        if let index = currentMemoryEntries.firstIndex(where: { $0.id == entry.id }) {
            currentMemoryEntries[index] = entry
        } else {
            currentMemoryEntries.append(entry)
            if currentMemoryEntries.count > 20 {
                currentMemoryEntries.removeFirst(currentMemoryEntries.count - 20)
            }
        }
        await memoryEntriesHandler?(currentMemoryEntries)
        try ensureSessionCurrentIfPresent(generation)
    }

    private func appendScoutChatEvent(
        _ event: ScoutChatEvent,
        generation: UInt64?
    ) async throws {
        try ensureSessionCurrentIfPresent(generation)
        if event.kind == "reset" {
            currentScoutChatEvents.removeAll()
            if !event.displayText.isEmpty {
                currentScoutChatEvents.append(event)
            }
            await scoutChatEventsHandler?(currentScoutChatEvents)
            try ensureSessionCurrentIfPresent(generation)
            return
        }
        guard !event.displayText.isEmpty else { return }
        currentScoutChatEvents.append(event)
        if currentScoutChatEvents.count > 40 {
            currentScoutChatEvents.removeFirst(currentScoutChatEvents.count - 40)
        }
        await scoutChatEventsHandler?(currentScoutChatEvents)
        try ensureSessionCurrentIfPresent(generation)
    }

    private func startReceiveLoop(generation: UInt64) {
        stopReceiveLoop()
        receiveTask = Task { [weak self] in
            await self?.receiveLoop(generation: generation)
        }
    }

    private func stopReceiveLoop() {
        receiveTask?.cancel()
        receiveTask = nil
    }

    private func receiveLoop(generation: UInt64) async {
        while !Task.isCancelled && isSessionCurrent(generation) {
            do {
                let envelope = try await signaling.receive()
                guard isSessionCurrent(generation) else { return }
                try await handleServerEvent(envelope, generation: generation)
                try ensureSessionCurrent(generation)
                if terminalKanbanEvent(in: envelope) == "media_disconnected" {
                    await closeTerminalSession(
                        generation: generation,
                        recovery: nil
                    )
                    return
                }
            } catch is CancellationError {
                return
            } catch {
                guard isSessionCurrent(generation) else { return }
                await closeTerminalSession(
                    generation: generation,
                    recovery: recoveryEvent(for: error)
                )
                return
            }
        }
    }

    private func terminalKanbanEvent(in envelope: WebSocketEnvelope) -> String? {
        guard let event = try? kanbanEvent(from: envelope) else { return nil }
        switch event.event {
        case "media_disconnected", "room_closed", "session_replaced", "access_denied":
            return event.event
        default:
            return nil
        }
    }

    private func recoveryEvent(for error: Error) -> NativeMediaRecoveryEvent {
        switch error {
        case NativeRoomSessionError.sessionReplaced(let message):
            return NativeMediaRecoveryEvent(stage: "session_replaced", message: message, terminal: true)
        case NativeRoomControlError.roomClosed(let message):
            return NativeMediaRecoveryEvent(stage: "room_closed", message: message, terminal: true)
        case NativeRoomSessionError.accessDenied(let message):
            return NativeMediaRecoveryEvent(stage: "access_denied", message: message, terminal: true)
        default:
            return NativeMediaRecoveryEvent(
                stage: "signaling_disconnected",
                message: "Room signaling disconnected. Rejoin the room.",
                terminal: true
            )
        }
    }

    private func startMediaQualityReporting(generation: UInt64) {
        stopMediaQualityReporting()
        let interval = mediaQualityReportIntervalNanoseconds
        mediaQualityTask = Task { [weak self] in
            while !Task.isCancelled {
                do {
                    try await Task.sleep(nanoseconds: interval)
                    if Task.isCancelled {
                        return
                    }
                    try await self?.publishMediaQualityReport(generation: generation)
                } catch is CancellationError {
                    return
                } catch {
                    continue
                }
            }
        }
    }

    private func stopMediaQualityReporting() {
        mediaQualityTask?.cancel()
        mediaQualityTask = nil
        previousMediaQualitySnapshot = nil
    }

    private func publishMediaQualityReport(generation: UInt64?) async throws {
        try ensureSessionCurrentIfPresent(generation)
        let snapshot: NativeMediaQualitySnapshot
        do {
            snapshot = try await rtc.mediaQualitySnapshot()
            try ensureSessionCurrentIfPresent(generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(stage: "media_quality_snapshot", error: error, generation: generation)
            throw error
        }
        let previous = previousMediaQualitySnapshot
        previousMediaQualitySnapshot = snapshot
        let sentAt = Self.iso8601String(Date())
        let evidence = try await mediaEvidenceSnapshot(
            from: snapshot,
            capturedAt: sentAt,
            generation: generation
        )
        try ensureSessionCurrentIfPresent(generation)
        currentMediaEvidenceSnapshot = evidence
        await mediaEvidenceHandler?(evidence)
        try ensureSessionCurrentIfPresent(generation)
        do {
            try await sendJSON(
                event: ClientSignalEvent.mediaQuality,
                payload: NativeMediaQualityPayload(
                    sentAt: sentAt,
                    laggy: false,
                    client: clientIdentity,
                    browser: mediaBrowserPayload(),
                    audio: mediaQualityAudioPayload(),
                    video: mediaQualityVideoPayload(),
                    remote: NativeMediaQualityRemotePayload(remoteVideoTiles: remoteVideoTracksByID.count),
                    stats: snapshot,
                    deltas: snapshot.deltas(since: previous)
                )
            )
            try ensureSessionCurrentIfPresent(generation)
        } catch {
            try ensureSessionCurrentIfPresent(generation)
            await reportMediaError(stage: "media_quality_report", error: error, generation: generation)
            throw error
        }
    }

    private func mediaEvidenceSnapshot(
        from snapshot: NativeMediaQualitySnapshot,
        capturedAt: String,
        generation: UInt64?
    ) async throws -> NativeMediaEvidenceSnapshot {
        let context = await mediaEvidenceContextProvider()
        try ensureSessionCurrentIfPresent(generation)
        return NativeMediaEvidenceSnapshot(
            source: snapshot,
            capturedAt: capturedAt,
            client: NativeMediaEvidenceClient(platform: clientIdentity.platform, version: clientIdentity.version),
            app: context.app,
            device: context.device,
            lifecycle: lifecycle,
            remoteVideoTiles: remoteVideoTracksByID.count,
            renderer: NativeMediaEvidenceRendererContext(
                trackObservations: remoteVideoTracksByID.values.map(\.renderedFrameObservation)
            ),
            runId: context.runId,
            roomId: context.roomId
        )
    }

    private func reportMediaError(
        stage: String,
        error: Error,
        generation: UInt64? = nil
    ) async {
        guard (try? ensureSessionCurrentIfPresent(generation)) != nil else { return }
        try? await sendJSON(
            event: ClientSignalEvent.mediaError,
            payload: NativeMediaErrorPayload(
                sentAt: Self.iso8601String(Date()),
                stage: stage,
                client: clientIdentity,
                browser: mediaBrowserPayload(),
                audio: NativeMediaErrorAudioPayload(
                    mode: "native",
                    processor: "avfoundation",
                    outputSettings: NativeMediaQualityTrackSettings(
                        enabled: !media.participantMediaState.micMuted,
                        readyState: lifecycle == .connected ? "live" : ""
                    )
                ),
                video: NativeMediaErrorVideoPayload(
                    constrained: false,
                    settings: NativeMediaQualityTrackSettings(
                        enabled: !media.participantMediaState.cameraOff,
                        readyState: lifecycle == .connected ? "live" : ""
                    )
                ),
                error: NativeMediaErrorDetailPayload(error: error)
            )
        )
    }

    private func mediaBrowserPayload() -> NativeMediaQualityBrowserPayload {
        NativeMediaQualityBrowserPayload(
            userAgent: "MeetingAssistApple/\(clientIdentity.version)",
            platform: clientIdentity.platform
        )
    }

    private func mediaQualityAudioPayload() -> NativeMediaQualityAudioPayload {
        NativeMediaQualityAudioPayload(
            mode: "native",
            processor: "avfoundation",
            outputSettings: NativeMediaQualityTrackSettings(
                enabled: !media.participantMediaState.micMuted,
                readyState: lifecycle == .connected ? "live" : ""
            )
        )
    }

    private func mediaQualityVideoPayload() -> NativeMediaQualityVideoPayload {
        NativeMediaQualityVideoPayload(
            settings: NativeMediaQualityTrackSettings(
                enabled: !media.participantMediaState.cameraOff,
                readyState: lifecycle == .connected ? "live" : ""
            )
        )
    }

    private static func iso8601String(_ date: Date) -> String {
        ISO8601DateFormatter().string(from: date)
    }

    private func handleRemoteVideoTrack(
        _ track: NativeRemoteVideoTrack,
        generation: UInt64
    ) async {
        guard isSessionCurrent(generation) else { return }
        remoteVideoTracksByID[track.id] = track
        let info = remoteVideoTrackInfo(for: track)
        await remoteVideoTrackInfoHandler?(info)
        guard isSessionCurrent(generation) else { return }
        if info.participantName == nil {
            await requestParticipantTrackRefresh(
                reason: "unlabeled remote video",
                generation: generation
            )
        }
    }

    private func updateParticipantScreenSharing(
        name: String,
        sharing: Bool,
        generation: UInt64?
    ) async throws {
        try ensureSessionCurrentIfPresent(generation)
        let cleanName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanName.isEmpty else { return }

        var snapshot = currentRoomSnapshot ?? RoomSnapshot(participants: [cleanName])
        if !snapshot.participants.contains(where: { $0.caseInsensitiveCompare(cleanName) == .orderedSame }) {
            snapshot.participants.append(cleanName)
        }
        var mediaStates = snapshot.mediaStates ?? [:]
        var state = mediaStates[cleanName] ?? ParticipantMediaState()
        state.screenSharing = sharing
        mediaStates[cleanName] = state
        snapshot.mediaStates = mediaStates
        currentRoomSnapshot = snapshot
        await roomSnapshotHandler?(snapshot)
        try ensureSessionCurrentIfPresent(generation)
    }

    private func handleParticipantTrack(
        _ metadata: NativeParticipantTrackMetadata,
        generation: UInt64?
    ) async throws {
        try ensureSessionCurrentIfPresent(generation)
        guard let name = metadata.normalizedName else { return }
        for key in metadata.trackLabelKeys {
            labelsByTrackID[key] = name
        }
        rememberRemoteStreamLabel(metadata.reliableStreamId, name: name)

        for track in remoteVideoTracksByID.values {
            guard remoteVideoTrackMatches(track, metadata: metadata) else { continue }
            await remoteVideoTrackInfoHandler?(remoteVideoTrackInfo(for: track))
            try ensureSessionCurrentIfPresent(generation)
        }
    }

    private func rememberRemoteStreamLabel(_ streamId: String?, name: String) {
        guard let streamId, !streamLabelConflicts.contains(streamId) else { return }
        if let existingName = labelsByStreamID[streamId],
           existingName.caseInsensitiveCompare(name) != .orderedSame {
            labelsByStreamID.removeValue(forKey: streamId)
            streamLabelConflicts.insert(streamId)
            return
        }
        labelsByStreamID[streamId] = name
    }

    private func remoteVideoTrackInfo(for track: NativeRemoteVideoTrack) -> NativeRemoteVideoTrackInfo {
        NativeRemoteVideoTrackInfo(track: track, participantName: participantName(for: track))
    }

    private func participantName(for track: NativeRemoteVideoTrack) -> String? {
        if let name = labelsByTrackID[track.id] {
            return name
        }
        for streamId in track.streamIds {
            guard let reliableStreamId = NativeParticipantTrackMetadata.reliableStreamId(streamId) else { continue }
            if let name = labelsByStreamID[reliableStreamId] {
                return name
            }
        }
        return nil
    }

    private func remoteVideoTrackMatches(_ track: NativeRemoteVideoTrack, metadata: NativeParticipantTrackMetadata) -> Bool {
        guard metadata.isVideo else {
            return metadata.reliableStreamId.map { track.streamIds.contains($0) } ?? false
        }
        if metadata.trackLabelKeys.contains(track.id) {
            return true
        }
        guard let streamId = metadata.reliableStreamId else { return false }
        return track.streamIds.contains(streamId)
    }

    private func requestParticipantTrackRefresh(reason: String, generation: UInt64) async {
        guard isSessionCurrent(generation) else { return }
        let now = Date()
        if let lastParticipantTrackRequest,
           now.timeIntervalSince(lastParticipantTrackRequest) < 0.9 {
            return
        }
        lastParticipantTrackRequest = now
        guard isSessionCurrent(generation) else { return }
        try? await sendJSON(event: ClientSignalEvent.requestParticipantTracks, payload: ParticipantTrackRequestPayload(reason: reason))
    }

    private func sendLocalCandidate(
        _ candidate: RTCIceCandidatePayload,
        generation: UInt64
    ) async {
        guard isSessionCurrent(generation) else { return }
        do {
            try await sendJSON(event: ClientSignalEvent.candidate, payload: candidate)
        } catch {
            // Candidate trickle is best-effort; ICE restart can recover if one send fails.
        }
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

private struct ParticipantTrackRequestPayload: Codable, Equatable, Sendable {
    var reason: String
}

private struct NativeMediaQualityPayload: Codable, Equatable, Sendable {
    var sentAt: String
    var laggy: Bool
    var client: NativeRoomClientIdentity
    var browser: NativeMediaQualityBrowserPayload
    var audio: NativeMediaQualityAudioPayload
    var video: NativeMediaQualityVideoPayload
    var remote: NativeMediaQualityRemotePayload
    var stats: NativeMediaQualitySnapshot
    var deltas: NativeMediaQualityDeltas
}

private struct NativeMediaErrorPayload: Codable, Equatable, Sendable {
    var sentAt: String
    var stage: String
    var client: NativeRoomClientIdentity
    var browser: NativeMediaQualityBrowserPayload
    var audio: NativeMediaErrorAudioPayload
    var video: NativeMediaErrorVideoPayload
    var error: NativeMediaErrorDetailPayload
}

private struct NativeMediaQualityBrowserPayload: Codable, Equatable, Sendable {
    var safari: Bool = false
    var userAgent: String
    var visibilityState: String = "active"
    var audioContextState: String = "native"
    var platform: String
}

private struct NativeMediaQualityAudioPayload: Codable, Equatable, Sendable {
    var mode: String
    var voiceFocus: Bool = false
    var processing: Bool = false
    var processor: String
    var voiceFocusMetrics = NativeVoiceFocusMetrics()
    var sourceSettings: NativeMediaQualityTrackSettings?
    var outputSettings: NativeMediaQualityTrackSettings
}

private struct NativeMediaErrorAudioPayload: Codable, Equatable, Sendable {
    var mode: String
    var voiceFocus: Bool = false
    var processor: String
    var outputSettings: NativeMediaQualityTrackSettings
}

private struct NativeVoiceFocusMetrics: Codable, Equatable, Sendable {
    var gain: Double = 0
    var suppressionDb: Double = 0
    var noiseBias: Double = 0
    var speechConfidence: Double = 0
}

private struct NativeMediaQualityVideoPayload: Codable, Equatable, Sendable {
    var constrained: Bool = false
    var settings: NativeMediaQualityTrackSettings
}

private struct NativeMediaErrorVideoPayload: Codable, Equatable, Sendable {
    var constrained: Bool = false
    var settings: NativeMediaQualityTrackSettings
}

private struct NativeMediaQualityTrackSettings: Codable, Equatable, Sendable {
    var enabled: Bool
    var readyState: String
}

private struct NativeMediaErrorDetailPayload: Codable, Equatable, Sendable {
    var name: String
    var message: String
    var constraint: String = ""
    var attempts: [String] = []

    init(error: Error) {
        name = String(reflecting: type(of: error))
        message = Self.safeMessage(error)
    }

    private static func safeMessage(_ error: Error) -> String {
        let raw = String(describing: error)
        let redactedCandidates = raw.replacingOccurrences(
            of: #"(?i)candidate:[^\n\r,;)\]}"]*"#,
            with: "candidate:<redacted>",
            options: .regularExpression
        )
        let squashed = redactedCandidates
            .replacingOccurrences(of: "\n", with: " ")
            .replacingOccurrences(of: "\r", with: " ")
        let redacted = squashed
            .replacingOccurrences(
                of: #"(?i)\b(turns?:)[^\s,;)\]}"]+"#,
                with: "$1<redacted>",
                options: .regularExpression
            )
            .replacingOccurrences(
                of: #"\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b"#,
                with: "<redacted-ip>",
                options: .regularExpression
            )
            .replacingOccurrences(
                of: #"(?i)\b(?:(?:[0-9a-f]{1,4}:){2,7}[0-9a-f]{1,4}|(?:[0-9a-f]{1,4}:){1,7}:|:(?::[0-9a-f]{1,4}){1,7}|(?:[0-9a-f]{1,4}:){1,6}:[0-9a-f]{1,4})(?:%\w+)?(?::\d{1,5})?\b"#,
                with: "<redacted-ip>",
                options: .regularExpression
            )
        return String(redacted.prefix(220))
    }
}

private struct NativeMediaQualityRemotePayload: Codable, Equatable, Sendable {
    var remoteVideoTiles: Int
    var remoteAudioMonitors: Int = 0
    var remoteAudioMaxLevel: Double = 0
    var remoteAudioAudibleMonitors: Int = 0
    var remoteAudioPlaybackPaths = NativeRemoteAudioPlaybackPaths()
    var audioContextState: String = "native"
    var missingVideoNames: [String] = []
    var missingAudioNames: [String] = []
    var duplicateVideoNames: [String] = []
    var duplicateAudioNames: [String] = []
    var placeholderVideoTiles: Int = 0
    var placeholderAudioMonitors: Int = 0
    var stalledVideoNames: [String] = []
    var audiblePendingRemotePlayback: Int = 0
}

private struct NativeRemoteAudioPlaybackPaths: Codable, Equatable, Sendable {
    var element: Int = 0
    var webaudio: Int = 0
    var none: Int = 0
}

private struct SetRecordingPayload: Codable, Equatable, Sendable {
    var enabled: Bool
}

private struct AssistantQueryPayload: Codable, Equatable, Sendable {
    var query: String
}

private struct ScoutChatPayload: Codable, Equatable, Sendable {
    var text: String
}

private struct ScreenSharePayload: Codable, Equatable, Sendable {
    var name: String
}

private struct BoardCardDeletePayload: Codable, Equatable, Sendable {
    var cardID: String

    enum CodingKeys: String, CodingKey {
        case cardID = "card_id"
    }
}

private struct EmptyPayload: Codable, Equatable, Sendable {}

public enum NativeRoomSessionError: Error, Equatable {
    case accessDenied(String)
    case missingAccessGrantName
    case sessionReplaced(String)
    case unexpectedOfferType(String)
    case unexpectedSignal(String)
    case unsupportedAuthMode(String)
    case unsupportedProtocol(String)
}

internal enum NativeRoomControlError: LocalizedError {
    case joinAlreadyInProgress
    case roomClosed(String)
    case sessionCancelled

    var errorDescription: String? {
        switch self {
        case .joinAlreadyInProgress:
            return "A native room join is already in progress."
        case .roomClosed(let message):
            return message
        case .sessionCancelled:
            return "The native room session ended before joining."
        }
    }
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
    var endpointId: String
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
