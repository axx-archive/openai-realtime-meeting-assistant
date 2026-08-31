import XCTest
@testable import MeetingAssistCore

final class SignalingModelTests: XCTestCase {
    func testWebSocketEnvelopeUsesExistingWireShape() throws {
        let envelope = WebSocketEnvelope(event: ClientSignalEvent.mediaReady, data: "{}")
        let data = try JSONEncoder().encode(envelope)
        let decoded = try JSONDecoder().decode(WebSocketEnvelope.self, from: data)
        XCTAssertEqual(decoded.event, "media_ready")
        XCTAssertEqual(decoded.data, "{}")
        XCTAssertNil(decoded.offerId)
        XCTAssertNil(decoded.revision)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertNil(object["offerId"])
        XCTAssertNil(object["revision"])
    }

    func testWebSocketEnvelopePreservesOfferCorrelation() throws {
        let envelope = WebSocketEnvelope(
            event: ServerSignalEvent.offer,
            data: "{\"type\":\"offer\"}",
            offerId: "offer-session-7",
            revision: 7
        )

        let data = try JSONEncoder().encode(envelope)
        let decoded = try JSONDecoder().decode(WebSocketEnvelope.self, from: data)

        XCTAssertEqual(decoded, envelope)
        XCTAssertEqual(decoded.offerId, "offer-session-7")
        XCTAssertEqual(decoded.revision, 7)
    }

    func testScreenShareEventsUseBrowserWireNames() {
        XCTAssertEqual(ClientSignalEvent.screenShareStarted, "screen_share_started")
        XCTAssertEqual(ClientSignalEvent.screenShareStopped, "screen_share_stopped")
    }

    func testRoomHeartbeatUsesServerWireName() {
        XCTAssertEqual(ClientSignalEvent.roomPing, "room_ping")
    }
}
