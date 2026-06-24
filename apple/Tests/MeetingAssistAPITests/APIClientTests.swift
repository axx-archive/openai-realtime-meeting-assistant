import XCTest
@testable import MeetingAssistAPI
@testable import MeetingAssistCore

final class APIClientTests: XCTestCase {
    func testBuildsBaseClient() {
        let client = MeetingAssistAPIClient(baseURL: URL(string: "https://thebonfire.xyz")!)
        XCTAssertEqual(client.baseURL.host, "thebonfire.xyz")
    }
}
