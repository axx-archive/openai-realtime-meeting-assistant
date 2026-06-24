import XCTest
import MeetingAssistApple
import MeetingAssistCore

final class MeetingAssistAppleAppTests: XCTestCase {
    @MainActor
    func testIOSRootViewBuildsAgainstNativeProtocol() {
        _ = MeetingAssistIOSRootView()
        XCTAssertEqual(meetingAssistNativeProtocolV1, "native-room-v1")
    }
}
