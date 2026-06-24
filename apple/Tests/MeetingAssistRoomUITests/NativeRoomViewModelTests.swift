import XCTest
@testable import MeetingAssistCore
@testable import MeetingAssistRoom
@testable import MeetingAssistRoomUI

@MainActor
final class NativeRoomViewModelTests: XCTestCase {
    func testRefreshRosterLoadsParticipantsAndSelectsFirstName() async {
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            configLoaderFactory: { _ in
                MockConfigLoader(participants: [
                    Participant(name: "Tom", email: "tom@example.com"),
                    Participant(name: "Caitlyn", email: "caitlyn@example.com")
                ])
            },
            sessionFactory: { _ in MockRoomSession() }
        )

        await model.refreshRoster()

        XCTAssertEqual(model.roster.map(\.name), ["Tom", "Caitlyn"])
        XCTAssertEqual(model.selectedName, "Tom")
        XCTAssertEqual(model.statusText, "Roster loaded")
        XCTAssertNil(model.errorMessage)
    }

    func testJoinAudioOnlyConnectsAndStoresParticipant() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            password: "B0NFIRE!",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinAudioOnly()

        XCTAssertEqual(session.joinedName, "Tom")
        XCTAssertEqual(session.joinedPassword, "B0NFIRE!")
        XCTAssertEqual(model.lifecycle, .connected)
        XCTAssertEqual(model.joinedParticipant?.name, "Tom")
        XCTAssertEqual(model.statusText, "Connected as Tom")
    }

    func testJoinFailureLeavesSessionAndReturnsToSignedOut() async {
        let session = MockRoomSession(error: NativeRoomSessionError.accessDenied("Room is full."))
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinAudioOnly()

        XCTAssertTrue(session.didLeave)
        XCTAssertEqual(model.lifecycle, .signedOut)
        XCTAssertEqual(model.errorMessage, "Room is full.")
    }

    func testMutePublishesParticipantMediaState() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinAudioOnly()
        await model.setMuted(true)

        XCTAssertTrue(session.isMuted)
        XCTAssertEqual(session.mediaStatePublishCount, 1)
        XCTAssertEqual(model.statusText, "Muted")
    }

    func testLeaveResetsJoinedState() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinAudioOnly()
        await model.setMuted(true)
        await model.leave()

        XCTAssertTrue(session.didLeave)
        XCTAssertEqual(model.lifecycle, .signedOut)
        XCTAssertNil(model.joinedParticipant)
        XCTAssertFalse(model.isMuted)
        XCTAssertEqual(model.statusText, "Left room")
    }
}

private struct MockConfigLoader: NativeRoomConfigLoading {
    var participants: [Participant]

    func nativeConfig() async throws -> NativeClientConfig {
        NativeClientConfig(
            protocolVersion: meetingAssistNativeProtocolV1,
            auth: .init(mode: "cookie", loginPath: "/auth/login", mePath: "/auth/me", logoutPath: "/auth/logout"),
            room: .init(
                clientConfigPath: "/client-config",
                websocketPath: "/websocket",
                participants: participants,
                maxParticipants: 7
            )
        )
    }
}

private final class MockRoomSession: NativeRoomSessionControlling, @unchecked Sendable {
    private let error: Error?
    private(set) var joinedName: String?
    private(set) var joinedPassword: String?
    private(set) var didLeave = false
    private(set) var isMuted = false
    private(set) var mediaStatePublishCount = 0

    init(error: Error? = nil) {
        self.error = error
    }

    func joinAudioOnly(name: String, password: String) async throws -> NativeRoomJoinResult {
        if let error { throw error }
        joinedName = name
        joinedPassword = password
        return NativeRoomJoinResult(
            participant: Participant(name: name, email: "\(name.lowercased())@example.com"),
            clientConfig: ClientRTCConfig(
                rtcConfiguration: [:],
                protocolVersion: meetingAssistNativeProtocolV1,
                auth: "cookie",
                websocketPath: "/websocket",
                signalingRole: "server-offer",
                supportedLayers: ["low", "medium", "high"],
                nativeHints: nil
            ),
            websocketURL: URL(string: "wss://example.com/websocket")!,
            answeredOffer: RTCSessionDescriptionPayload(type: "answer", sdp: "v=0\r\n")
        )
    }

    func setMuted(_ muted: Bool) async {
        isMuted = muted
    }

    func sendParticipantMediaState() async throws {
        mediaStatePublishCount += 1
    }

    func leave() async {
        didLeave = true
    }

    func currentLifecycle() async -> RoomLifecycleState {
        .connected
    }
}
