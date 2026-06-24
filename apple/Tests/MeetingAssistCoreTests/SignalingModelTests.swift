import XCTest
@testable import MeetingAssistCore

final class SignalingModelTests: XCTestCase {
    func testWebSocketEnvelopeUsesExistingWireShape() throws {
        let envelope = WebSocketEnvelope(event: ClientSignalEvent.mediaReady, data: "{}")
        let data = try JSONEncoder().encode(envelope)
        let decoded = try JSONDecoder().decode(WebSocketEnvelope.self, from: data)
        XCTAssertEqual(decoded.event, "media_ready")
        XCTAssertEqual(decoded.data, "{}")
    }

    func testScreenShareEventsUseBrowserWireNames() {
        XCTAssertEqual(ClientSignalEvent.screenShareStarted, "screen_share_started")
        XCTAssertEqual(ClientSignalEvent.screenShareStopped, "screen_share_stopped")
    }
}
