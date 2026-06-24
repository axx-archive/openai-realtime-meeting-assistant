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

public typealias NativeRoomSnapshotHandler = @Sendable (RoomSnapshot) async -> Void
public typealias NativeBoardStateHandler = @Sendable (BoardState) async -> Void
public typealias NativeUndoAvailabilityHandler = @Sendable (Bool) async -> Void
public typealias NativeAssistantEventsHandler = @Sendable ([AssistantEvent]) async -> Void
public typealias NativeMemoryEntriesHandler = @Sendable ([MemoryEntry]) async -> Void
public typealias NativeMeetingArchiveHandler = @Sendable (MeetingArchiveResult) async -> Void
public typealias NativeScoutChatEventsHandler = @Sendable ([ScoutChatEvent]) async -> Void

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
    private var receiveTask: Task<Void, Never>?
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
        try await join(name: name, password: password, video: false)
    }

    public func joinWithCamera(name: String, password: String) async throws -> NativeRoomJoinResult {
        try await join(name: name, password: password, video: true)
    }

    private func join(name: String, password: String, video: Bool) async throws -> NativeRoomJoinResult {
        stopReceiveLoop()
        resetNegotiationState()
        resetRemoteVideoState()
        resetRoomState()

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

        try await rtc.configure(config)
        await rtc.setLocalCandidateHandler { [weak self] candidate in
            guard let self else { return }
            await self.sendLocalCandidate(candidate)
        }
        await rtc.setRemoteVideoTrackHandler { [weak self] track in
            guard let self else { return }
            await self.handleRemoteVideoTrack(track)
        }

        media.setCameraOff(!video)
        try await rtc.prepareLocalMedia(audio: true, video: video)
        lifecycle = .preparingMedia

        try await sendJSON(
            event: ClientSignalEvent.mediaReady,
            payload: MediaReadyPayload(client: clientIdentity, media: MediaCapabilities(audio: true, video: video))
        )

        let answer = try await waitForOfferAndAnswer()
        try await sendParticipantMediaState()
        lifecycle = .connected
        startReceiveLoop()

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
            try await handleKanbanRoomEvent(event)
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
        stopReceiveLoop()
        await rtc.setLocalCandidateHandler(nil)
        await rtc.setRemoteVideoTrackHandler(nil)
        await rtc.leave()
        await signaling.close()
        resetNegotiationState()
        resetRemoteVideoState()
        resetRoomState()
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
            try await handleKanbanRoomEvent(event)
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

    private func handleKanbanRoomEvent(_ event: RoomEvent<JSONValue>) async throws {
        switch event.event {
        case "participants":
            let snapshot = try decodeKanbanData(RoomSnapshot.self, from: event.data)
            currentRoomSnapshot = snapshot
            await roomSnapshotHandler?(snapshot)
        case "board":
            let state = try decodeKanbanData(BoardState.self, from: event.data)
            currentBoardState = state
            await boardStateHandler?(state)
        case "undo_available":
            let canUndo = try decodeKanbanData(Bool.self, from: event.data)
            currentCanUndoDelete = canUndo
            await undoAvailabilityHandler?(canUndo)
        case "participant_track":
            let metadata = try participantTrackMetadata(from: event.data)
            await handleParticipantTrack(metadata)
        case "assistant_event":
            let assistantEvent = try decodeKanbanData(AssistantEvent.self, from: event.data)
            await appendAssistantEvent(assistantEvent)
            if let artifact = assistantEvent.artifact {
                await appendMemoryEntry(artifact)
            }
        case "memory":
            let entries = try decodeKanbanData([MemoryEntry].self, from: event.data)
            currentMemoryEntries = entries
            await memoryEntriesHandler?(entries)
        case "memory_transcript", "memory_brain", "memory_board_update":
            let entry = try decodeKanbanData(MemoryEntry.self, from: event.data)
            await appendMemoryEntry(entry)
        case "memory_answer":
            let answer = try decodeKanbanData(MemoryAnswerResult.self, from: event.data)
            let entry = MemoryEntry(
                id: "answer-\(currentMemoryEntries.count + 1)",
                kind: "answer",
                text: answer.answer,
                metadata: answer.query.isEmpty ? nil : ["query": answer.query]
            )
            await appendMemoryEntry(entry)
        case "meeting_archived":
            let archive = try decodeKanbanData(MeetingArchiveResult.self, from: event.data)
            currentMeetingArchive = archive
            await meetingArchiveHandler?(archive)
            await appendAssistantEvent(
                AssistantEvent(
                    kind: "archive",
                    text: archive.summary,
                    createdAt: archive.archivedAt,
                    downloadURL: archive.downloadURL,
                    artifact: archive.artifact
                )
            )
            if let artifact = archive.artifact {
                await appendMemoryEntry(artifact)
            }
        case "scout_chat":
            let chatEvent = try decodeKanbanData(ScoutChatEvent.self, from: event.data)
            await appendScoutChatEvent(chatEvent)
            if let artifact = chatEvent.artifact {
                await appendMemoryEntry(artifact)
            }
            if let artifact = chatEvent.thread?.artifact {
                await appendMemoryEntry(artifact)
            }
        default:
            break
        }
    }

    private func appendAssistantEvent(_ event: AssistantEvent) async {
        guard !event.displayText.isEmpty else { return }
        currentAssistantEvents.append(event)
        if currentAssistantEvents.count > 40 {
            currentAssistantEvents.removeFirst(currentAssistantEvents.count - 40)
        }
        await assistantEventsHandler?(currentAssistantEvents)
    }

    private func appendMemoryEntry(_ entry: MemoryEntry) async {
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
    }

    private func appendScoutChatEvent(_ event: ScoutChatEvent) async {
        if event.kind == "reset" {
            currentScoutChatEvents.removeAll()
            if !event.displayText.isEmpty {
                currentScoutChatEvents.append(event)
            }
            await scoutChatEventsHandler?(currentScoutChatEvents)
            return
        }
        guard !event.displayText.isEmpty else { return }
        currentScoutChatEvents.append(event)
        if currentScoutChatEvents.count > 40 {
            currentScoutChatEvents.removeFirst(currentScoutChatEvents.count - 40)
        }
        await scoutChatEventsHandler?(currentScoutChatEvents)
    }

    private func startReceiveLoop() {
        stopReceiveLoop()
        receiveTask = Task { [weak self] in
            await self?.receiveLoop()
        }
    }

    private func stopReceiveLoop() {
        receiveTask?.cancel()
        receiveTask = nil
    }

    private func receiveLoop() async {
        while !Task.isCancelled {
            do {
                let envelope = try await signaling.receive()
                try await handleServerEvent(envelope)
            } catch {
                return
            }
        }
    }

    private func handleRemoteVideoTrack(_ track: NativeRemoteVideoTrack) async {
        remoteVideoTracksByID[track.id] = track
        let info = remoteVideoTrackInfo(for: track)
        await remoteVideoTrackInfoHandler?(info)
        if info.participantName == nil {
            await requestParticipantTrackRefresh(reason: "unlabeled remote video")
        }
    }

    private func handleParticipantTrack(_ metadata: NativeParticipantTrackMetadata) async {
        guard let name = metadata.normalizedName else { return }
        for key in metadata.trackLabelKeys {
            labelsByTrackID[key] = name
        }
        rememberRemoteStreamLabel(metadata.reliableStreamId, name: name)

        for track in remoteVideoTracksByID.values {
            guard remoteVideoTrackMatches(track, metadata: metadata) else { continue }
            await remoteVideoTrackInfoHandler?(remoteVideoTrackInfo(for: track))
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

    private func requestParticipantTrackRefresh(reason: String) async {
        let now = Date()
        if let lastParticipantTrackRequest,
           now.timeIntervalSince(lastParticipantTrackRequest) < 0.9 {
            return
        }
        lastParticipantTrackRequest = now
        try? await sendJSON(event: ClientSignalEvent.requestParticipantTracks, payload: ParticipantTrackRequestPayload(reason: reason))
    }

    private func sendLocalCandidate(_ candidate: RTCIceCandidatePayload) async {
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

private struct SetRecordingPayload: Codable, Equatable, Sendable {
    var enabled: Bool
}

private struct AssistantQueryPayload: Codable, Equatable, Sendable {
    var query: String
}

private struct ScoutChatPayload: Codable, Equatable, Sendable {
    var text: String
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
