import XCTest
@testable import MeetingAssistCore

final class NativeConfigTests: XCTestCase {
    func testDecodesNativeClientConfig() throws {
        let json = """
        {
          "protocolVersion": "native-room-v1",
          "auth": {
            "mode": "cookie",
            "loginPath": "/auth/login",
            "mePath": "/auth/me",
            "logoutPath": "/auth/logout"
          },
          "room": {
            "clientConfigPath": "/client-config",
            "websocketPath": "/websocket",
            "maxParticipants": 7,
            "participants": [{"name":"AJ","email":"aj@shareability.com"}]
          }
        }
        """.data(using: .utf8)!

        let config = try JSONDecoder().decode(NativeClientConfig.self, from: json)
        XCTAssertEqual(config.protocolVersion, meetingAssistNativeProtocolV1)
        XCTAssertEqual(config.auth.mode, "cookie")
        XCTAssertEqual(config.room.websocketPath, "/websocket")
        XCTAssertEqual(config.room.participants.first?.name, "AJ")
    }

    func testDecodesClientConfigAdditiveFields() throws {
        let json = """
        {
          "rtcConfiguration": {"iceServers":[{"urls":["stun:stun.l.google.com:19302"]}]},
          "protocolVersion": "native-room-v1",
          "auth": "cookie",
          "websocketPath": "/websocket",
          "signalingRole": "server-offer",
          "supportedLayers": ["low","medium","high"],
          "nativeHints": {"mediaReadyEvent":"media_ready"}
        }
        """.data(using: .utf8)!

        let config = try JSONDecoder().decode(ClientRTCConfig.self, from: json)
        XCTAssertEqual(config.protocolVersion, meetingAssistNativeProtocolV1)
        XCTAssertEqual(config.signalingRole, "server-offer")
        XCTAssertEqual(config.supportedLayers, ["low", "medium", "high"])
        XCTAssertEqual(config.nativeHints?["mediaReadyEvent"], .string("media_ready"))
    }
}
