import XCTest
@testable import MeetingAssistSignaling
@testable import MeetingAssistCore

final class SignalingClientTests: XCTestCase {
    func testStartsSignedOut() async {
        let client = MeetingAssistSignalingClient()
        let state = await client.lifecycle
        XCTAssertEqual(state, .signedOut)
    }

    func testSendBeforeConnectThrows() async {
        let client = MeetingAssistSignalingClient()

        do {
            try await client.send(event: ClientSignalEvent.participant)
            XCTFail("send should throw before a websocket task is connected")
        } catch MeetingAssistSignalingError.notConnected {
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }
}
