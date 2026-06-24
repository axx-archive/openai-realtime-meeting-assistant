import Foundation

public let meetingAssistNativeProtocolV1 = "native-room-v1"

public struct NativeClientConfig: Codable, Equatable, Sendable {
    public var protocolVersion: String
    public var auth: Auth
    public var room: Room

    public init(protocolVersion: String, auth: Auth, room: Room) {
        self.protocolVersion = protocolVersion
        self.auth = auth
        self.room = room
    }

    public struct Auth: Codable, Equatable, Sendable {
        public var mode: String
        public var loginPath: String
        public var mePath: String
        public var logoutPath: String
    }

    public struct Room: Codable, Equatable, Sendable {
        public var clientConfigPath: String
        public var websocketPath: String
        public var participants: [Participant]
        public var maxParticipants: Int
    }
}

public struct ClientRTCConfig: Codable, Equatable, Sendable {
    public var rtcConfiguration: [String: JSONValue]
    public var protocolVersion: String?
    public var auth: String?
    public var websocketPath: String?
    public var signalingRole: String?
    public var supportedLayers: [String]?
    public var nativeHints: [String: JSONValue]?
}
