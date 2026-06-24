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
}
