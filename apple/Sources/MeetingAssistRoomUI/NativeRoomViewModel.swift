import Foundation
import MeetingAssistAPI
import MeetingAssistCore
import MeetingAssistRoom
import MeetingAssistRoomRTC

public protocol NativeRoomConfigLoading: Sendable {
    func nativeConfig() async throws -> NativeClientConfig
}

extension MeetingAssistAPIClient: NativeRoomConfigLoading {}

public protocol NativeRoomSessionControlling: Sendable {
    func joinAudioOnly(name: String, password: String) async throws -> NativeRoomJoinResult
    func joinWithCamera(name: String, password: String) async throws -> NativeRoomJoinResult
    func setRemoteVideoTrackHandler(_ handler: NativeRemoteVideoTrackInfoHandler?) async
    func setRoomSnapshotHandler(_ handler: NativeRoomSnapshotHandler?) async
    func setBoardStateHandler(_ handler: NativeBoardStateHandler?) async
    func setUndoAvailabilityHandler(_ handler: NativeUndoAvailabilityHandler?) async
    func setAssistantEventsHandler(_ handler: NativeAssistantEventsHandler?) async
    func setMemoryEntriesHandler(_ handler: NativeMemoryEntriesHandler?) async
    func setMeetingArchiveHandler(_ handler: NativeMeetingArchiveHandler?) async
    func setScoutChatEventsHandler(_ handler: NativeScoutChatEventsHandler?) async
    func setMediaRecoveryHandler(_ handler: NativeMediaRecoveryHandler?) async
    func setMediaEvidenceHandler(_ handler: NativeMediaEvidenceHandler?) async
    func setMuted(_ muted: Bool) async
    func setCameraOff(_ off: Bool) async
    func setScreenSharing(_ sharing: Bool) async throws
    func requestICERestart(reason: String) async throws
    func captureMediaEvidenceSnapshot() async throws -> NativeMediaEvidenceSnapshot
    func setRecordingEnabled(_ enabled: Bool) async throws
    func archiveMeeting() async throws
    func askAssistant(_ query: String) async throws
    func sendScoutChat(_ text: String) async throws
    func resetScoutChat() async throws
    func createBoardCard(_ payload: BoardCardMutationPayload) async throws
    func updateBoardCard(id: String, payload: BoardCardMutationPayload) async throws
    func deleteBoardCard(id: String) async throws
    func undoDeletedBoardCard() async throws
    func sendParticipantMediaState() async throws
    func leave() async
    func currentLifecycle() async -> RoomLifecycleState
}

extension NativeRoomSessionCoordinator: NativeRoomSessionControlling {
    public func currentLifecycle() async -> RoomLifecycleState {
        lifecycle
    }
}

public typealias NativeRoomConfigLoaderFactory = @Sendable (URL) -> NativeRoomConfigLoading
public typealias NativeRoomSessionFactory = @Sendable (URL) -> NativeRoomSessionControlling

@MainActor
public final class NativeRoomViewModel: ObservableObject {
    @Published public var baseURLString: String
    @Published public var selectedName: String
    @Published public var password: String
    @Published public private(set) var roster: [Participant] = []
    @Published public private(set) var lifecycle: RoomLifecycleState = .signedOut
    @Published public private(set) var statusText = "Ready"
    @Published public private(set) var errorMessage: String?
    @Published public private(set) var isBusy = false
    @Published public private(set) var isMuted = false
    @Published public private(set) var isCameraOff = true
    @Published public private(set) var isScreenSharing = false
    @Published public private(set) var hasLocalCamera = false
    @Published public private(set) var joinedParticipant: Participant?
    @Published public private(set) var remoteVideoTracks: [NativeRemoteVideoTrackInfo] = []
    @Published public private(set) var roomParticipants: [String] = []
    @Published public private(set) var roomCapacity: Int?
    @Published public private(set) var roomAvailableSeats: Int?
    @Published public private(set) var participantMediaStates: [String: ParticipantMediaState] = [:]
    @Published public private(set) var roomRecording = RoomRecordingState()
    @Published public private(set) var boardCards: [KanbanCard] = []
    @Published public private(set) var boardUpdatedAt: String?
    @Published public private(set) var canUndoDelete = false
    @Published public private(set) var isBoardMutating = false
    @Published public private(set) var isArchiving = false
    @Published public private(set) var assistantEvents: [AssistantEvent] = []
    @Published public private(set) var memoryEntries: [MemoryEntry] = []
    @Published public private(set) var latestArchive: MeetingArchiveResult?
    @Published public private(set) var scoutChatEvents: [ScoutChatEvent] = []
    @Published public private(set) var isScoutChatSending = false
    @Published public private(set) var latestMediaEvidence: NativeMediaEvidenceSnapshot?
    @Published public private(set) var isCapturingMediaEvidence = false

    public let boardStatuses = ["Backlog", "In Progress", "Blocked", "Done"]

    private let configLoaderFactory: NativeRoomConfigLoaderFactory
    private let sessionFactory: NativeRoomSessionFactory
    private var session: NativeRoomSessionControlling?

    public init(
        baseURLString: String = "https://thebonfire.xyz",
        selectedName: String = "",
        password: String = "",
        configLoaderFactory: @escaping NativeRoomConfigLoaderFactory = { baseURL in
            MeetingAssistAPIClient(baseURL: baseURL)
        },
        sessionFactory: NativeRoomSessionFactory? = nil
    ) {
        self.baseURLString = baseURLString
        self.selectedName = selectedName
        self.password = password
        self.configLoaderFactory = configLoaderFactory
        let clientIdentity = NativeRoomClientIdentity.current
        self.sessionFactory = sessionFactory ?? { baseURL in
            NativeRoomSessionCoordinator(
                api: MeetingAssistAPIClient(baseURL: baseURL),
                clientIdentity: clientIdentity,
                mediaEvidenceContextProvider: {
                    await MainActor.run {
                        NativeMediaEvidenceCaptureContext.current
                    }
                }
            )
        }
    }

    public var canJoin: Bool {
        !isBusy
            && lifecycle != .connected
            && normalizedBaseURL() != nil
            && !selectedName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    public var canUseRoomControls: Bool {
        lifecycle == .connected || lifecycle == .reconnecting
    }

    public var canUseCameraControls: Bool {
        canUseRoomControls && hasLocalCamera
    }

    public var canUseScreenShareControls: Bool {
        canUseCameraControls
    }

    public var latestMediaEvidenceJSON: String? {
        guard let latestMediaEvidence else { return nil }
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let data = try? encoder.encode(latestMediaEvidence) else { return nil }
        return String(data: data, encoding: .utf8)
    }

    public var activeBoardCards: [KanbanCard] {
        let activeStatuses = ["In Progress", "Blocked"]
        let active = boardCards.filter { activeStatuses.contains($0.status) }
        if !active.isEmpty {
            return active
        }
        return boardCards.filter { $0.status == "Backlog" }.prefix(4).map { $0 }
    }

    public var recentMemoryEntries: [MemoryEntry] {
        Array(memoryEntries.suffix(4).reversed())
    }

    public var latestArchiveDownloadURL: URL? {
        guard let downloadURL = latestArchive?.downloadURL else { return nil }
        return resolvedDownloadURL(downloadURL)
    }

    public func assistantDownloadURL(for event: AssistantEvent) -> URL? {
        resolvedDownloadURL(event.downloadURL)
    }

    public func resolvedDownloadURL(_ value: String?) -> URL? {
        guard let value, !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        if let url = URL(string: value), url.scheme != nil {
            return url
        }
        guard let baseURL = normalizedBaseURL() else {
            return URL(string: value)
        }
        return URL(string: value, relativeTo: baseURL)?.absoluteURL
    }

    public func refreshRoster() async {
        guard let baseURL = normalizedBaseURL() else {
            setError("Enter a valid MeetingAssist URL.")
            return
        }

        isBusy = true
        errorMessage = nil
        statusText = "Loading roster"

        do {
            let config = try await configLoaderFactory(baseURL).nativeConfig()
            roster = config.room.participants
            if selectedName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
               let first = config.room.participants.first {
                selectedName = first.name
            }
            statusText = "Roster loaded"
        } catch {
            setError(displayMessage(for: error))
        }

        isBusy = false
    }

    public func joinAudioOnly() async {
        await join(video: false)
    }

    public func joinWithCamera() async {
        await join(video: true)
    }

    private func join(video: Bool) async {
        guard canJoin, let baseURL = normalizedBaseURL() else {
            setError("Enter a valid room URL and name.")
            return
        }

        let name = selectedName.trimmingCharacters(in: .whitespacesAndNewlines)
        isBusy = true
        errorMessage = nil
        resetRoomState()
        latestMediaEvidence = nil
        isCapturingMediaEvidence = false
        statusText = "Joining"
        lifecycle = .authenticated

        let newSession = sessionFactory(baseURL)
        await newSession.setRemoteVideoTrackHandler { [weak self] trackInfo in
            await self?.upsertRemoteVideoTrack(trackInfo)
        }
        await newSession.setRoomSnapshotHandler { [weak self] snapshot in
            await self?.applyRoomSnapshot(snapshot)
        }
        await newSession.setBoardStateHandler { [weak self] board in
            await self?.applyBoardState(board)
        }
        await newSession.setUndoAvailabilityHandler { [weak self] canUndo in
            await self?.applyUndoAvailability(canUndo)
        }
        await newSession.setAssistantEventsHandler { [weak self] events in
            await self?.applyAssistantEvents(events)
        }
        await newSession.setMemoryEntriesHandler { [weak self] entries in
            await self?.applyMemoryEntries(entries)
        }
        await newSession.setMeetingArchiveHandler { [weak self] archive in
            await self?.applyMeetingArchive(archive)
        }
        await newSession.setScoutChatEventsHandler { [weak self] events in
            await self?.applyScoutChatEvents(events)
        }
        await newSession.setMediaRecoveryHandler { [weak self] event in
            await self?.applyMediaRecoveryEvent(event)
        }
        await newSession.setMediaEvidenceHandler { [weak self] evidence in
            await self?.applyMediaEvidence(evidence)
        }

        do {
            let result = if video {
                try await newSession.joinWithCamera(name: name, password: password)
            } else {
                try await newSession.joinAudioOnly(name: name, password: password)
            }
            session = newSession
            joinedParticipant = result.participant
            isCameraOff = !video
            hasLocalCamera = video
            lifecycle = await newSession.currentLifecycle()
            statusText = "Connected as \(result.participant.name)"
        } catch {
            await newSession.setRemoteVideoTrackHandler(nil)
            await newSession.setRoomSnapshotHandler(nil)
            await newSession.setBoardStateHandler(nil)
            await newSession.setUndoAvailabilityHandler(nil)
            await newSession.setAssistantEventsHandler(nil)
            await newSession.setMemoryEntriesHandler(nil)
            await newSession.setMeetingArchiveHandler(nil)
            await newSession.setScoutChatEventsHandler(nil)
            await newSession.setMediaRecoveryHandler(nil)
            await newSession.setMediaEvidenceHandler(nil)
            await newSession.leave()
            session = nil
            joinedParticipant = nil
            isCameraOff = true
            hasLocalCamera = false
            isBusy = false
            resetRoomState()
            lifecycle = .signedOut
            setError(displayMessage(for: error))
        }

        isBusy = false
    }

    public func captureMediaEvidence() async {
        guard let session, canUseRoomControls else { return }

        isCapturingMediaEvidence = true
        errorMessage = nil
        statusText = "Capturing media evidence"
        defer { isCapturingMediaEvidence = false }

        do {
            let evidence = try await session.captureMediaEvidenceSnapshot()
            applyMediaEvidence(evidence)
            statusText = "Media evidence captured"
        } catch {
            setError(displayMessage(for: error))
        }
    }

    public func setMuted(_ muted: Bool) async {
        isMuted = muted
        guard let session else { return }

        await session.setMuted(muted)
        do {
            try await session.sendParticipantMediaState()
            statusText = muted ? "Muted" : "Unmuted"
        } catch {
            setError(displayMessage(for: error))
        }
    }

    public func setCameraOff(_ off: Bool) async {
        isCameraOff = off
        guard let session else { return }

        await session.setCameraOff(off)
        do {
            try await session.sendParticipantMediaState()
            statusText = off ? "Camera off" : "Camera on"
        } catch {
            setError(displayMessage(for: error))
        }
    }

    public func setScreenSharing(_ sharing: Bool) async {
        guard let session else { return }

        statusText = sharing ? "Starting screen share" : "Stopping screen share"
        do {
            try await session.setScreenSharing(sharing)
            isScreenSharing = sharing
            statusText = sharing ? "Sharing screen" : "Screen share stopped"
        } catch {
            if sharing {
                isScreenSharing = false
            }
            setError(displayMessage(for: error))
        }
    }

    public func requestMediaRecovery(reason: String) async {
        guard let session, canUseRoomControls else { return }

        errorMessage = nil
        statusText = "Reconnecting media"
        lifecycle = .reconnecting
        do {
            try await session.requestICERestart(reason: reason)
            lifecycle = await session.currentLifecycle()
            statusText = "Media reconnect requested"
        } catch {
            setError(displayMessage(for: error))
        }
    }

    public func setRecordingEnabled(_ enabled: Bool) async {
        guard let session else { return }
        do {
            try await session.setRecordingEnabled(enabled)
            roomRecording.enabled = enabled
            statusText = enabled ? "Recording on" : "Recording off"
        } catch {
            setError(displayMessage(for: error))
        }
    }

    public func archiveMeeting() async {
        guard let session else { return }

        isArchiving = true
        statusText = "Generating notes"
        do {
            try await session.archiveMeeting()
            statusText = "Archive requested"
        } catch {
            setError(displayMessage(for: error))
        }
        isArchiving = false
    }

    public func askAssistant(_ query: String) async {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let session, !trimmed.isEmpty else { return }

        statusText = "Asking Scout"
        do {
            try await session.askAssistant(trimmed)
            statusText = "Scout is checking"
        } catch {
            setError(displayMessage(for: error))
        }
    }

    public func sendScoutChat(_ text: String) async {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let session, !trimmed.isEmpty else { return }

        isScoutChatSending = true
        statusText = "Sending to Scout"
        do {
            try await session.sendScoutChat(trimmed)
            statusText = "Scout is thinking"
        } catch {
            setError(displayMessage(for: error))
        }
        isScoutChatSending = false
    }

    public func resetScoutChat() async {
        guard let session else { return }

        do {
            try await session.resetScoutChat()
            scoutChatEvents = []
            statusText = "New Scout thread"
        } catch {
            setError(displayMessage(for: error))
        }
    }

    public func createBoardCard(_ payload: BoardCardMutationPayload) async {
        guard let session else { return }

        isBoardMutating = true
        statusText = "Creating card"
        do {
            try await session.createBoardCard(payload)
            statusText = "Card create requested"
        } catch {
            setError(displayMessage(for: error))
        }
        isBoardMutating = false
    }

    public func updateBoardCard(id: String, payload: BoardCardMutationPayload) async {
        guard let session else { return }

        isBoardMutating = true
        statusText = "Updating card"
        do {
            try await session.updateBoardCard(id: id, payload: payload)
            statusText = "Card update requested"
        } catch {
            setError(displayMessage(for: error))
        }
        isBoardMutating = false
    }

    public func deleteBoardCard(id: String) async {
        guard let session else { return }

        isBoardMutating = true
        statusText = "Deleting card"
        do {
            try await session.deleteBoardCard(id: id)
            statusText = "Card delete requested"
        } catch {
            setError(displayMessage(for: error))
        }
        isBoardMutating = false
    }

    public func undoDeletedBoardCard() async {
        guard let session else { return }

        isBoardMutating = true
        statusText = "Restoring card"
        do {
            try await session.undoDeletedBoardCard()
            statusText = "Card restore requested"
        } catch {
            setError(displayMessage(for: error))
        }
        isBoardMutating = false
    }

    public func leave() async {
        guard let session else {
            lifecycle = .signedOut
            return
        }

        isBusy = true
        await session.setRemoteVideoTrackHandler(nil)
        await session.setRoomSnapshotHandler(nil)
        await session.setBoardStateHandler(nil)
        await session.setUndoAvailabilityHandler(nil)
        await session.setAssistantEventsHandler(nil)
        await session.setMemoryEntriesHandler(nil)
        await session.setMeetingArchiveHandler(nil)
        await session.setScoutChatEventsHandler(nil)
        await session.setMediaRecoveryHandler(nil)
        await session.setMediaEvidenceHandler(nil)
        await session.leave()
        self.session = nil
        joinedParticipant = nil
        isMuted = false
        isCameraOff = true
        isScreenSharing = false
        hasLocalCamera = false
        isCapturingMediaEvidence = false
        resetRoomState()
        lifecycle = .signedOut
        statusText = "Left room"
        isBusy = false
    }

    private func normalizedBaseURL() -> URL? {
        let trimmed = baseURLString.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), url.scheme != nil, url.host != nil else { return nil }
        return url
    }

    private func setError(_ message: String) {
        errorMessage = message
        statusText = "Needs attention"
    }

    private func resetRoomState() {
        remoteVideoTracks = []
        roomParticipants = []
        roomCapacity = nil
        roomAvailableSeats = nil
        participantMediaStates = [:]
        roomRecording = RoomRecordingState()
        boardCards = []
        boardUpdatedAt = nil
        canUndoDelete = false
        isBoardMutating = false
        isArchiving = false
        isScreenSharing = false
        assistantEvents = []
        memoryEntries = []
        latestArchive = nil
        scoutChatEvents = []
        isScoutChatSending = false
    }

    private func applyRoomSnapshot(_ snapshot: RoomSnapshot) {
        roomParticipants = snapshot.participants
        roomCapacity = snapshot.capacity
        roomAvailableSeats = snapshot.availableSeats
        participantMediaStates = snapshot.mediaStates ?? [:]
        if let recording = snapshot.recording {
            roomRecording = recording
        }
    }

    private func applyBoardState(_ state: BoardState) {
        boardCards = state.cards
        boardUpdatedAt = state.updatedAt
    }

    private func applyUndoAvailability(_ canUndo: Bool) {
        canUndoDelete = canUndo
    }

    private func applyAssistantEvents(_ events: [AssistantEvent]) {
        assistantEvents = events.filter { !$0.displayText.isEmpty }
    }

    private func applyMemoryEntries(_ entries: [MemoryEntry]) {
        memoryEntries = entries.filter { !$0.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }

    private func applyMeetingArchive(_ archive: MeetingArchiveResult) {
        latestArchive = archive
        statusText = "Archive ready"
        isArchiving = false
    }

    private func applyScoutChatEvents(_ events: [ScoutChatEvent]) {
        scoutChatEvents = events.filter { !$0.displayText.isEmpty }
    }

    private func applyMediaEvidence(_ evidence: NativeMediaEvidenceSnapshot) {
        latestMediaEvidence = evidence
    }

    private func applyMediaRecoveryEvent(_ event: NativeMediaRecoveryEvent) async {
        errorMessage = event.message
        if event.terminal {
            statusText = "Media disconnected"
            let endedSession = session
            session = nil
            joinedParticipant = nil
            isMuted = false
            isCameraOff = true
            isScreenSharing = false
            hasLocalCamera = false
            isBusy = false
            isCapturingMediaEvidence = false
            resetRoomState()
            lifecycle = .signedOut
            await endedSession?.setRemoteVideoTrackHandler(nil)
            await endedSession?.setRoomSnapshotHandler(nil)
            await endedSession?.setBoardStateHandler(nil)
            await endedSession?.setUndoAvailabilityHandler(nil)
            await endedSession?.setAssistantEventsHandler(nil)
            await endedSession?.setMemoryEntriesHandler(nil)
            await endedSession?.setMeetingArchiveHandler(nil)
            await endedSession?.setScoutChatEventsHandler(nil)
            await endedSession?.setMediaRecoveryHandler(nil)
            await endedSession?.setMediaEvidenceHandler(nil)
            await endedSession?.leave()
            return
        }

        lifecycle = .reconnecting
        statusText = "Media reconnect needed"
    }

    private func upsertRemoteVideoTrack(_ trackInfo: NativeRemoteVideoTrackInfo) {
        if let index = remoteVideoTracks.firstIndex(where: { $0.id == trackInfo.id }) {
            remoteVideoTracks[index] = trackInfo
        } else {
            remoteVideoTracks.append(trackInfo)
        }
    }

    private func displayMessage(for error: Error) -> String {
        if let roomError = error as? NativeRoomSessionError {
            switch roomError {
            case .accessDenied(let message), .sessionReplaced(let message):
                return message
            case .unsupportedAuthMode(let mode):
                return "Unsupported auth mode: \(mode)"
            case .unsupportedProtocol(let version):
                return "Unsupported native protocol: \(version)"
            case .missingAccessGrantName:
                return "Room admission did not include a participant name."
            case .unexpectedOfferType(let type):
                return "Unexpected WebRTC offer type: \(type)"
            case .unexpectedSignal(let event):
                return "Unexpected signaling event: \(event)"
            }
        }
        if let rtcError = error as? RoomRTCError {
            switch rtcError {
            case .cameraUnavailable:
                return "No camera is available on this device."
            case .cameraFormatUnavailable:
                return "The camera does not expose a supported video format."
            case .cameraCaptureFailed(let message), .webRTCOperationFailed(let message):
                return message
            case .missingSessionDescription:
                return "The room did not provide a usable media description."
            case .peerConnectionCreationFailed:
                return "Could not create a native WebRTC connection."
            case .peerConnectionNotConfigured:
                return "The native WebRTC connection was not configured."
            case .screenCapturePermissionDenied:
                return "Allow Screen Recording for MeetingAssist in System Settings, then try sharing again."
            case .screenShareUnavailable:
                return "Screen sharing is unavailable in this native build."
            case .trackPublicationFailed(let kind):
                return "Could not publish native \(kind)."
            case .webRTCUnavailable:
                return "Native WebRTC is unavailable in this build."
            }
        }
        return error.localizedDescription
    }

}
