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

    public init(micMuted: Bool = false, cameraOff: Bool = false, screenSharing: Bool = false) {
        self.micMuted = micMuted
        self.cameraOff = cameraOff
        self.screenSharing = screenSharing
    }
}

public struct RoomSnapshot: Codable, Equatable, Sendable {
    public var participants: [String]
    public var capacity: Int?
    public var mediaStates: [String: ParticipantMediaState]?
}

public struct KanbanCard: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var status: String
    public var title: String
    public var notes: String?
    public var owner: String?
    public var tags: [String]?
    public var dueDate: String?
}

public struct BoardState: Codable, Equatable, Sendable {
    public var cards: [KanbanCard]
    public var updatedAt: String?
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
