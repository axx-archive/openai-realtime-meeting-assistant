import Foundation

public struct Participant: Codable, Equatable, Identifiable, Sendable {
    public var id: String { email.isEmpty ? name : email }
    public var name: String
    public var email: String

    public init(name: String, email: String = "") {
        self.name = name
        self.email = email
    }
}

public struct ParticipantMediaState: Codable, Equatable, Sendable {
    public var micMuted: Bool
    public var cameraOff: Bool
    public var screenSharing: Bool
    public var updatedAt: String?

    public init(
        micMuted: Bool = false,
        cameraOff: Bool = false,
        screenSharing: Bool = false,
        updatedAt: String? = nil
    ) {
        self.micMuted = micMuted
        self.cameraOff = cameraOff
        self.screenSharing = screenSharing
        self.updatedAt = updatedAt
    }
}

public struct RoomRecordingState: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var updatedAt: String?
    public var updatedBy: String?

    public init(enabled: Bool = true, updatedAt: String? = nil, updatedBy: String? = nil) {
        self.enabled = enabled
        self.updatedAt = updatedAt
        self.updatedBy = updatedBy
    }
}

public struct RoomSnapshot: Codable, Equatable, Sendable {
    public var participants: [String]
    public var capacity: Int?
    public var occupiedSeats: Int?
    public var availableSeats: Int?
    public var mediaStates: [String: ParticipantMediaState]?
    public var recording: RoomRecordingState?

    public init(
        participants: [String],
        capacity: Int? = nil,
        occupiedSeats: Int? = nil,
        availableSeats: Int? = nil,
        mediaStates: [String: ParticipantMediaState]? = nil,
        recording: RoomRecordingState? = nil
    ) {
        self.participants = participants
        self.capacity = capacity
        self.occupiedSeats = occupiedSeats
        self.availableSeats = availableSeats
        self.mediaStates = mediaStates
        self.recording = recording
    }
}

public struct KanbanKeyDate: Codable, Equatable, Sendable {
    public var label: String
    public var date: String

    public init(label: String, date: String) {
        self.label = label
        self.date = date
    }
}

public struct KanbanCard: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var status: String
    public var title: String
    public var notes: String?
    public var owner: String?
    public var tags: [String]?
    public var dueDate: String?
    public var keyDates: [KanbanKeyDate]?

    public init(
        id: String,
        status: String,
        title: String,
        notes: String? = nil,
        owner: String? = nil,
        tags: [String]? = nil,
        dueDate: String? = nil,
        keyDates: [KanbanKeyDate]? = nil
    ) {
        self.id = id
        self.status = status
        self.title = title
        self.notes = notes
        self.owner = owner
        self.tags = tags
        self.dueDate = dueDate
        self.keyDates = keyDates
    }
}

public struct BoardState: Codable, Equatable, Sendable {
    public var cards: [KanbanCard]
    public var updatedAt: String?

    public init(cards: [KanbanCard], updatedAt: String? = nil) {
        self.cards = cards
        self.updatedAt = updatedAt
    }
}

public struct BoardCardMutationPayload: Codable, Equatable, Sendable {
    public var cardID: String?
    public var title: String
    public var status: String
    public var owner: String?
    public var tags: [String]
    public var notes: String
    public var dueDate: String
    public var keyDates: [KanbanKeyDate]

    enum CodingKeys: String, CodingKey {
        case cardID = "card_id"
        case title
        case status
        case owner
        case tags
        case notes
        case dueDate
        case keyDates
    }

    public init(
        cardID: String? = nil,
        title: String,
        status: String,
        owner: String? = nil,
        tags: [String] = [],
        notes: String = "",
        dueDate: String = "",
        keyDates: [KanbanKeyDate] = []
    ) {
        self.cardID = cardID
        self.title = title
        self.status = status
        self.owner = owner
        self.tags = tags
        self.notes = notes
        self.dueDate = dueDate
        self.keyDates = keyDates
    }

    public init(card: KanbanCard) {
        self.init(
            cardID: card.id,
            title: card.title,
            status: card.status,
            owner: card.owner,
            tags: card.tags ?? [],
            notes: card.notes ?? "",
            dueDate: card.dueDate ?? "",
            keyDates: card.keyDates ?? []
        )
    }
}

public struct AssistantEvent: Codable, Equatable, Identifiable, Sendable {
    public var eventID: String?
    public var kind: String?
    public var text: String?
    public var message: String?
    public var createdAt: String?
    public var downloadURL: String?
    public var artifact: MemoryEntry?
    public var thread: ScoutChatThread?
    public var actions: [AssistantAction]?
    public var data: [String: JSONValue]?

    public var id: String {
        if let eventID, !eventID.isEmpty {
            return eventID
        }
        return [kind, createdAt, text ?? message]
            .compactMap { $0?.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
            .joined(separator: "|")
    }

    public var displayText: String {
        (text ?? message ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    enum CodingKeys: String, CodingKey {
        case eventID = "id"
        case kind
        case text
        case message
        case createdAt
        case downloadURL = "downloadUrl"
        case artifact
        case thread
        case actions
        case data
    }

    public init(
        eventID: String? = nil,
        kind: String? = nil,
        text: String? = nil,
        message: String? = nil,
        createdAt: String? = nil,
        downloadURL: String? = nil,
        artifact: MemoryEntry? = nil,
        thread: ScoutChatThread? = nil,
        actions: [AssistantAction]? = nil,
        data: [String: JSONValue]? = nil
    ) {
        self.eventID = eventID
        self.kind = kind
        self.text = text
        self.message = message
        self.createdAt = createdAt
        self.downloadURL = downloadURL
        self.artifact = artifact
        self.thread = thread
        self.actions = actions
        self.data = data
    }
}

public struct MemoryEntry: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var kind: String
    public var text: String
    public var createdAt: String?
    public var metadata: [String: String]?

    public init(id: String, kind: String, text: String, createdAt: String? = nil, metadata: [String: String]? = nil) {
        self.id = id
        self.kind = kind
        self.text = text
        self.createdAt = createdAt
        self.metadata = metadata
    }
}

public struct MemoryAnswerResult: Codable, Equatable, Sendable {
    public var query: String
    public var answer: String

    public init(query: String, answer: String) {
        self.query = query
        self.answer = answer
    }
}

public struct MeetingArchiveResult: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var meetingID: String?
    public var archivedAt: String
    public var archivedBy: String?
    public var downloadURL: String
    public var summary: String
    public var email: MeetingArchiveEmailStatus?
    public var artifact: MemoryEntry?

    enum CodingKeys: String, CodingKey {
        case id
        case meetingID = "meetingId"
        case archivedAt
        case archivedBy
        case downloadURL = "downloadUrl"
        case summary
        case email
        case artifact
    }

    public init(
        id: String,
        meetingID: String? = nil,
        archivedAt: String,
        archivedBy: String? = nil,
        downloadURL: String,
        summary: String,
        email: MeetingArchiveEmailStatus? = nil,
        artifact: MemoryEntry? = nil
    ) {
        self.id = id
        self.meetingID = meetingID
        self.archivedAt = archivedAt
        self.archivedBy = archivedBy
        self.downloadURL = downloadURL
        self.summary = summary
        self.email = email
        self.artifact = artifact
    }
}

public struct MeetingArchiveEmailStatus: Codable, Equatable, Sendable {
    public var attempted: Bool
    public var sent: Bool
    public var skipped: Bool
    public var error: String?
    public var reason: String?
    public var recipients: [String]?

    public init(
        attempted: Bool = false,
        sent: Bool = false,
        skipped: Bool = false,
        error: String? = nil,
        reason: String? = nil,
        recipients: [String]? = nil
    ) {
        self.attempted = attempted
        self.sent = sent
        self.skipped = skipped
        self.error = error
        self.reason = reason
        self.recipients = recipients
    }
}

public struct AssistantAction: Codable, Equatable, Identifiable, Sendable {
    public var type: String
    public var tool: String?
    public var mode: String?
    public var artifactID: String?
    public var enabled: Bool?
    public var label: String?

    public var id: String {
        [type, tool, mode, artifactID, label]
            .compactMap { $0?.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
            .joined(separator: "|")
    }

    enum CodingKeys: String, CodingKey {
        case type
        case tool
        case mode
        case artifactID = "artifactId"
        case enabled
        case label
    }

    public init(
        type: String,
        tool: String? = nil,
        mode: String? = nil,
        artifactID: String? = nil,
        enabled: Bool? = nil,
        label: String? = nil
    ) {
        self.type = type
        self.tool = tool
        self.mode = mode
        self.artifactID = artifactID
        self.enabled = enabled
        self.label = label
    }
}

public struct ScoutChatThread: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var mode: String
    public var query: String
    public var status: String
    public var artifact: MemoryEntry?
    public var actions: [AssistantAction]?

    public init(
        id: String,
        mode: String,
        query: String,
        status: String,
        artifact: MemoryEntry? = nil,
        actions: [AssistantAction]? = nil
    ) {
        self.id = id
        self.mode = mode
        self.query = query
        self.status = status
        self.artifact = artifact
        self.actions = actions
    }
}

public struct ScoutChatEvent: Codable, Equatable, Identifiable, Sendable {
    public var eventID: String?
    public var kind: String
    public var text: String
    public var timestamp: String?
    public var artifact: MemoryEntry?
    public var thread: ScoutChatThread?
    public var actions: [AssistantAction]?

    public var id: String {
        if let eventID, !eventID.isEmpty {
            return eventID
        }
        return [kind, timestamp, text]
            .compactMap { $0?.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
            .joined(separator: "|")
    }

    public var displayText: String {
        text.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    enum CodingKeys: String, CodingKey {
        case eventID = "id"
        case kind
        case text
        case timestamp = "ts"
        case artifact
        case thread
        case actions
    }

    public init(
        eventID: String? = nil,
        kind: String,
        text: String,
        timestamp: String? = nil,
        artifact: MemoryEntry? = nil,
        thread: ScoutChatThread? = nil,
        actions: [AssistantAction]? = nil
    ) {
        self.eventID = eventID
        self.kind = kind
        self.text = text
        self.timestamp = timestamp
        self.artifact = artifact
        self.thread = thread
        self.actions = actions
    }
}

public enum RoomLifecycleState: String, Codable, Equatable, Sendable {
    case signedOut
    case authenticated
    case admitted
    case preparingMedia
    case negotiating
    case connected
    case reconnecting
    case leaving
}
