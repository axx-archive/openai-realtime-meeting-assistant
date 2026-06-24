import XCTest
import MeetingAssistCore
import MeetingAssistMac

final class MeetingAssistMacAppTests: XCTestCase {
    @MainActor
    func testMacRootViewBuildsAgainstNativeProtocol() {
        _ = MeetingAssistMacRootView()
        XCTAssertEqual(meetingAssistNativeProtocolV1, "native-room-v1")
    }
}
