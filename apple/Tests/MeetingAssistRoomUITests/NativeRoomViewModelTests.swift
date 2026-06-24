import XCTest
@testable import MeetingAssistCore
@testable import MeetingAssistRoom
@testable import MeetingAssistRoomRTC
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
        XCTAssertTrue(model.isCameraOff)
        XCTAssertFalse(model.hasLocalCamera)
        XCTAssertFalse(model.canUseCameraControls)
    }

    func testJoinWithCameraConnectsWithVideoEnabled() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()

        XCTAssertTrue(session.didJoinWithCamera)
        XCTAssertEqual(model.lifecycle, .connected)
        XCTAssertFalse(model.isCameraOff)
        XCTAssertTrue(model.hasLocalCamera)
        XCTAssertTrue(model.canUseCameraControls)
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

    func testCameraTogglePublishesParticipantMediaState() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await model.setCameraOff(true)

        XCTAssertTrue(session.isCameraOff)
        XCTAssertEqual(session.mediaStatePublishCount, 1)
        XCTAssertEqual(model.statusText, "Camera off")
    }

    func testCameraUnavailableShowsReadableError() async {
        let session = MockRoomSession(error: RoomRTCError.cameraUnavailable)
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()

        XCTAssertTrue(session.didLeave)
        XCTAssertEqual(model.lifecycle, .signedOut)
        XCTAssertEqual(model.errorMessage, "No camera is available on this device.")
    }

    func testRemoteVideoTracksAppendDedupeAndClearOnLeave() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await session.emitRemoteVideoTrack(NativeRemoteVideoTrack(id: "remote-video-1", streamIds: ["meetingassist-native"]))
        await session.emitRemoteVideoTrack(NativeRemoteVideoTrack(id: "remote-video-1", streamIds: ["meetingassist-native"]))

        XCTAssertEqual(model.remoteVideoTracks.map(\.id), ["remote-video-1"])

        await model.leave()

        XCTAssertTrue(model.remoteVideoTracks.isEmpty)
        XCTAssertNil(session.remoteVideoTrackHandler)
    }

    func testRemoteVideoTrackRelabelsWithoutDuplicatingTile() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        let track = NativeRemoteVideoTrack(id: "remote-video-1", streamIds: ["meetingassist-native"])
        await session.emitRemoteVideoTrack(track)
        await session.emitRemoteVideoTrack(NativeRemoteVideoTrackInfo(track: track, participantName: "Caitlyn"))

        XCTAssertEqual(model.remoteVideoTracks.map(\.id), ["remote-video-1"])
        XCTAssertEqual(model.remoteVideoTracks.map(\.displayName), ["Caitlyn"])
    }

    func testRemoteVideoTrackUsesParticipantNameWhenMetadataArrivesFirst() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await session.emitRemoteVideoTrack(
            NativeRemoteVideoTrackInfo(
                track: NativeRemoteVideoTrack(id: "remote-video-1", streamIds: ["meetingassist-native"]),
                participantName: "Caitlyn"
            )
        )

        XCTAssertEqual(model.remoteVideoTracks.map(\.displayName), ["Caitlyn"])
    }

    func testRoomAndBoardSnapshotsUpdateNativeState() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await session.emitRoomSnapshot(
            RoomSnapshot(
                participants: ["Tom", "Caitlyn"],
                capacity: 7,
                occupiedSeats: 2,
                availableSeats: 5,
                mediaStates: ["Caitlyn": ParticipantMediaState(micMuted: true, cameraOff: false)],
                recording: RoomRecordingState(enabled: false, updatedBy: "Caitlyn")
            )
        )
        await session.emitBoardState(
            BoardState(
                cards: [
                    KanbanCard(id: "card-1", status: "In Progress", title: "Native board", owner: "Caitlyn"),
                    KanbanCard(id: "card-2", status: "Backlog", title: "Later")
                ],
                updatedAt: "2026-06-24T21:00:00Z"
            )
        )

        XCTAssertEqual(model.roomParticipants, ["Tom", "Caitlyn"])
        XCTAssertEqual(model.roomCapacity, 7)
        XCTAssertEqual(model.roomAvailableSeats, 5)
        XCTAssertEqual(model.participantMediaStates["Caitlyn"]?.micMuted, true)
        XCTAssertEqual(model.roomRecording.enabled, false)
        XCTAssertEqual(model.roomRecording.updatedBy, "Caitlyn")
        XCTAssertEqual(model.boardCards.map(\.title), ["Native board", "Later"])
        XCTAssertEqual(model.activeBoardCards.map(\.title), ["Native board"])
    }

    func testRecordingAndArchiveControlsDelegateToSession() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinAudioOnly()
        await model.setRecordingEnabled(false)
        await model.archiveMeeting()

        XCTAssertEqual(session.recordingEnabledChanges, [false])
        XCTAssertEqual(session.archiveRequestCount, 1)
        XCTAssertEqual(model.roomRecording.enabled, false)
        XCTAssertEqual(model.statusText, "Archive requested")
        XCTAssertFalse(model.isArchiving)
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
        XCTAssertFalse(model.hasLocalCamera)
        XCTAssertTrue(model.roomParticipants.isEmpty)
        XCTAssertTrue(model.boardCards.isEmpty)
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
    private(set) var remoteVideoTrackHandler: NativeRemoteVideoTrackInfoHandler?
    private(set) var roomSnapshotHandler: NativeRoomSnapshotHandler?
    private(set) var boardStateHandler: NativeBoardStateHandler?
    private(set) var joinedName: String?
    private(set) var joinedPassword: String?
    private(set) var didJoinWithCamera = false
    private(set) var didLeave = false
    private(set) var isMuted = false
    private(set) var isCameraOff = true
    private(set) var mediaStatePublishCount = 0
    private(set) var recordingEnabledChanges: [Bool] = []
    private(set) var archiveRequestCount = 0

    init(error: Error? = nil) {
        self.error = error
    }

    func joinAudioOnly(name: String, password: String) async throws -> NativeRoomJoinResult {
        if let error { throw error }
        joinedName = name
        joinedPassword = password
        return joinResult(name: name)
    }

    func joinWithCamera(name: String, password: String) async throws -> NativeRoomJoinResult {
        if let error { throw error }
        joinedName = name
        joinedPassword = password
        didJoinWithCamera = true
        isCameraOff = false
        return joinResult(name: name)
    }

    private func joinResult(name: String) -> NativeRoomJoinResult {
        NativeRoomJoinResult(
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

    func setRemoteVideoTrackHandler(_ handler: NativeRemoteVideoTrackInfoHandler?) async {
        remoteVideoTrackHandler = handler
    }

    func setRoomSnapshotHandler(_ handler: NativeRoomSnapshotHandler?) async {
        roomSnapshotHandler = handler
    }

    func setBoardStateHandler(_ handler: NativeBoardStateHandler?) async {
        boardStateHandler = handler
    }

    func emitRemoteVideoTrack(_ track: NativeRemoteVideoTrack) async {
        await emitRemoteVideoTrack(NativeRemoteVideoTrackInfo(track: track))
    }

    func emitRemoteVideoTrack(_ trackInfo: NativeRemoteVideoTrackInfo) async {
        await remoteVideoTrackHandler?(trackInfo)
    }

    func setCameraOff(_ off: Bool) async {
        isCameraOff = off
    }

    func setRecordingEnabled(_ enabled: Bool) async throws {
        recordingEnabledChanges.append(enabled)
    }

    func archiveMeeting() async throws {
        archiveRequestCount += 1
    }

    func emitRoomSnapshot(_ snapshot: RoomSnapshot) async {
        await roomSnapshotHandler?(snapshot)
    }

    func emitBoardState(_ state: BoardState) async {
        await boardStateHandler?(state)
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
