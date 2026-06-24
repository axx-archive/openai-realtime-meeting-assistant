import XCTest
@testable import MeetingAssistSignaling
@testable import MeetingAssistCore

final class SignalingClientTests: XCTestCase {
    func testStartsSignedOut() async {
        let client = MeetingAssistSignalingClient()
        let state = await client.lifecycle
        XCTAssertEqual(state, .signedOut)
    }
}
