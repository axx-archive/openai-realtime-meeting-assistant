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

public struct AssistantEvent: Codable, Equatable, Sendable {
    public var kind: String?
    public var text: String?
    public var data: [String: JSONValue]?
}

public struct MemoryEntry: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var kind: String
    public var text: String
    public var createdAt: String?
    public var metadata: [String: String]?
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
