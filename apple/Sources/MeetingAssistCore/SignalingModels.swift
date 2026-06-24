import Foundation

public struct WebSocketEnvelope: Codable, Equatable, Sendable {
    public var event: String
    public var data: String

    public init(event: String, data: String) {
        self.event = event
        self.data = data
    }
}

public struct RoomEvent<Data: Codable & Equatable & Sendable>: Codable, Equatable, Sendable {
    public var event: String
    public var data: Data

    public init(event: String, data: Data) {
        self.event = event
        self.data = data
    }
}

public enum ClientSignalEvent {
    public static let participant = "participant"
    public static let mediaReady = "media_ready"
    public static let answer = "answer"
    public static let candidate = "candidate"
    public static let restartICE = "restart_ice"
    public static let selectLayer = "select_layer"
    public static let participantMediaState = "participant_media_state"
    public static let mediaQuality = "media_quality"
    public static let mediaError = "media_error"
}

public enum ServerSignalEvent {
    public static let offer = "offer"
    public static let candidate = "candidate"
    public static let kanban = "kanban"
}
