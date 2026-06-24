import Foundation
import MeetingAssistCore

public struct ScoutThread: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var title: String
    public var status: String
    public var updatedAt: String?
}

public struct ScoutMessage: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var role: String
    public var text: String
    public var createdAt: String?
}

public enum ScoutVoiceBoundary {
    public static let privateRealtimeOfferPath = "/assistant/realtime-offer"
    public static let sharedRoomWebsocketEvent = "voice_control"
}
