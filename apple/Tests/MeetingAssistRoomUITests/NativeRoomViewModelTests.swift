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

    func testScreenShareToggleDelegatesAndResetsOnLeave() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await model.setScreenSharing(true)
        await model.setScreenSharing(false)
        await model.setScreenSharing(true)

        XCTAssertEqual(session.screenSharingChanges, [true, false, true])
        XCTAssertTrue(model.isScreenSharing)
        XCTAssertTrue(model.canUseScreenShareControls)
        XCTAssertEqual(model.statusText, "Sharing screen")

        await model.leave()

        XCTAssertFalse(model.isScreenSharing)
    }

    func testScreenShareUnavailableShowsReadableError() async {
        let session = MockRoomSession(screenShareError: RoomRTCError.screenShareUnavailable)
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await model.setScreenSharing(true)

        XCTAssertFalse(model.isScreenSharing)
        XCTAssertEqual(model.errorMessage, "Screen sharing is unavailable in this native build.")
    }

    func testScreenRecordingPermissionErrorIsActionable() async {
        let session = MockRoomSession(screenShareError: RoomRTCError.screenCapturePermissionDenied)
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await model.setScreenSharing(true)

        XCTAssertFalse(model.isScreenSharing)
        XCTAssertEqual(model.errorMessage, "Allow Screen Recording for MeetingAssist in System Settings, then try sharing again.")
    }

    func testRemoteScreenShareSnapshotUpdatesParticipantState() async {
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
                mediaStates: ["Caitlyn": ParticipantMediaState(micMuted: false, cameraOff: false, screenSharing: true)]
            )
        )

        XCTAssertEqual(model.participantMediaStates["Caitlyn"]?.screenSharing, true)
    }

    func testMediaRecoveryRequestsIceRestartForConnectedSession() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await model.requestMediaRecovery(reason: NativeMediaRecoveryReason.appForegrounded.rawValue)

        XCTAssertEqual(session.iceRestartReasons, ["native-foreground"])
        XCTAssertEqual(model.lifecycle, .reconnecting)
        XCTAssertEqual(model.statusText, "Media reconnect requested")
        XCTAssertNil(model.errorMessage)
    }

    func testMediaRecoveryNoopsBeforeJoining() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.requestMediaRecovery(reason: NativeMediaRecoveryReason.networkChanged.rawValue)

        XCTAssertTrue(session.iceRestartReasons.isEmpty)
        XCTAssertEqual(model.lifecycle, .signedOut)
        XCTAssertEqual(model.statusText, "Ready")
    }

    func testMediaRecoveryFailureShowsReadableError() async {
        let session = MockRoomSession(iceRestartError: NativeRoomSessionError.unexpectedSignal("restart_ice"))
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await model.requestMediaRecovery(reason: NativeMediaRecoveryReason.networkRecovered.rawValue)

        XCTAssertEqual(session.iceRestartReasons, ["native-network-recovered"])
        XCTAssertEqual(model.errorMessage, "Unexpected signaling event: restart_ice")
        XCTAssertEqual(model.statusText, "Needs attention")
    }

    func testTerminalMediaRecoveryEventLeavesBrokenSessionAndAllowsRejoin() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await session.emitMediaRecoveryEvent(
            NativeMediaRecoveryEvent(
                stage: "media_disconnected",
                message: "media negotiation stalled; rejoin the room.",
                terminal: true
            )
        )

        XCTAssertTrue(session.didLeave)
        XCTAssertEqual(model.lifecycle, .signedOut)
        XCTAssertNil(model.joinedParticipant)
        XCTAssertTrue(model.isCameraOff)
        XCTAssertFalse(model.hasLocalCamera)
        XCTAssertFalse(model.isScreenSharing)
        XCTAssertTrue(model.canJoin)
        XCTAssertEqual(model.statusText, "Media disconnected")
        XCTAssertEqual(model.errorMessage, "media negotiation stalled; rejoin the room.")
    }

    func testTerminalMediaRecoveryEventClearsBusyStateAndAllowsRejoin() async {
        let session = MockRoomSession()
        let loader = SuspendedConfigLoader()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in loader },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        let refreshTask = Task {
            await model.refreshRoster()
        }
        await loader.waitUntilRequested()
        XCTAssertTrue(model.isBusy)

        await session.emitMediaRecoveryEvent(
            NativeMediaRecoveryEvent(
                stage: "media_disconnected",
                message: "media negotiation stalled; rejoin the room.",
                terminal: true
            )
        )

        XCTAssertTrue(session.didLeave)
        XCTAssertEqual(model.lifecycle, .signedOut)
        XCTAssertTrue(model.canJoin)
        XCTAssertFalse(model.isBusy)
        XCTAssertEqual(model.statusText, "Media disconnected")
        await loader.resume()
        await refreshTask.value
    }

    func testConnectivityRecoveryPolicyOnlySignalsRealRecoveryOrPathChange() {
        var policy = NativeConnectivityRecoveryPolicy()

        XCTAssertNil(policy.record(.init(status: .satisfied, interfaces: [.wifi])))
        XCTAssertNil(policy.record(.init(status: .satisfied, interfaces: [.wifi])))
        XCTAssertNil(policy.record(.init(status: .unsatisfied, interfaces: [])))
        XCTAssertEqual(
            policy.record(.init(status: .satisfied, interfaces: [.wifi])),
            "native-network-recovered"
        )
        XCTAssertEqual(
            policy.record(.init(status: .satisfied, isExpensive: true, interfaces: [.cellular])),
            "native-network-change"
        )
        XCTAssertNil(policy.record(.init(status: .satisfied, isExpensive: true, interfaces: [.cellular])))
    }

    func testAudioRecoveryPolicyOnlySignalsRecoverableEvents() {
        let policy = NativeAudioRecoveryPolicy()

        XCTAssertNil(policy.recoveryReason(for: .interruptionBegan))
        XCTAssertNil(policy.recoveryReason(for: .interruptionEnded(shouldResume: false)))
        XCTAssertNil(policy.recoveryReason(for: .routeCategoryChanged))
        XCTAssertEqual(
            policy.recoveryReason(for: .interruptionEnded(shouldResume: true)),
            "native-audio-interruption-ended"
        )
        XCTAssertEqual(
            policy.recoveryReason(for: .routeChanged),
            "native-audio-route-change"
        )
        XCTAssertEqual(
            policy.recoveryReason(for: .mediaServicesReset),
            "native-audio-services-reset"
        )
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

    func testCaptureMediaEvidenceUpdatesExportJSONFromSessionStats() async {
        let evidence = Self.passingMediaEvidence()
        let session = MockRoomSession(mediaEvidenceSnapshot: evidence)
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await model.captureMediaEvidence()

        XCTAssertEqual(model.latestMediaEvidence, evidence)
        XCTAssertEqual(model.statusText, "Media evidence captured")
        XCTAssertFalse(model.isCapturingMediaEvidence)
        let json = model.latestMediaEvidenceJSON ?? ""
        XCTAssertTrue(json.contains("\"mediaAssertions\""))
        XCTAssertTrue(json.contains("\"cameraPublished\""))
        XCTAssertFalse(json.localizedCaseInsensitiveContains("candidate:"))
        XCTAssertFalse(json.localizedCaseInsensitiveContains("turn:"))
        XCTAssertFalse(json.localizedCaseInsensitiveContains("alice:secret"))
        XCTAssertFalse(json.localizedCaseInsensitiveContains("Bearer "))
        XCTAssertFalse(json.localizedCaseInsensitiveContains("BEGIN PRIVATE KEY"))
    }

    func testMediaEvidenceHandlerUpdatesLatestEvidence() async {
        let evidence = Self.passingMediaEvidence()
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinWithCamera()
        await session.emitMediaEvidence(evidence)

        XCTAssertEqual(model.latestMediaEvidence, evidence)
        XCTAssertEqual(model.latestMediaEvidenceJSON?.contains("\"status\" : \"observed\""), true)
        XCTAssertEqual(model.latestMediaEvidenceJSON?.contains("\"releaseEligible\" : false"), true)
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

    func testUndoAvailabilityUpdatesNativeState() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinAudioOnly()
        await session.emitUndoAvailability(true)

        XCTAssertTrue(model.canUndoDelete)
    }

    func testAssistantMemoryAndArchiveUpdatesNativeState() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinAudioOnly()
        let assistantArchive = AssistantEvent(
            kind: "archive",
            text: "Archive ready",
            downloadURL: "/archives/latest.json?key=signed"
        )
        await session.emitAssistantEvents([
            AssistantEvent(kind: "query", text: "What is blocked?"),
            AssistantEvent(kind: "answer", text: "Native Scout rendering is next."),
            assistantArchive
        ])
        await session.emitMemoryEntries([
            MemoryEntry(id: "memory-1", kind: "brain", text: "Native plan summarized."),
            MemoryEntry(id: "memory-2", kind: "transcript", text: "Tom: Add Scout.", metadata: ["speaker": "Tom"])
        ])
        await session.emitMeetingArchive(
            MeetingArchiveResult(
                id: "meeting-20260624",
                archivedAt: "2026-06-24T21:10:00Z",
                archivedBy: "Tom",
                downloadURL: "/archives/meeting-20260624.json?key=signed",
                summary: "Tom archived meeting meeting-20260624 with 2 transcript item(s), 1 board card(s), 2 participant(s), and 1 project status item(s).",
                email: MeetingArchiveEmailStatus(skipped: true)
            )
        )

        XCTAssertEqual(model.assistantEvents.map(\.displayText), ["What is blocked?", "Native Scout rendering is next.", "Archive ready"])
        XCTAssertEqual(model.memoryEntries.map(\.kind), ["brain", "transcript"])
        XCTAssertEqual(model.recentMemoryEntries.map(\.id), ["memory-2", "memory-1"])
        XCTAssertEqual(model.latestArchive?.id, "meeting-20260624")
        XCTAssertEqual(model.latestArchiveDownloadURL?.host, "example.com")
        XCTAssertEqual(model.latestArchiveDownloadURL?.path, "/archives/meeting-20260624.json")
        XCTAssertEqual(model.assistantDownloadURL(for: assistantArchive)?.host, "example.com")
        XCTAssertEqual(model.assistantDownloadURL(for: assistantArchive)?.path, "/archives/latest.json")
        XCTAssertEqual(model.statusText, "Archive ready")

        await model.leave()

        XCTAssertTrue(model.assistantEvents.isEmpty)
        XCTAssertTrue(model.memoryEntries.isEmpty)
        XCTAssertNil(model.latestArchive)
        XCTAssertNil(session.assistantEventsHandler)
        XCTAssertNil(session.memoryEntriesHandler)
        XCTAssertNil(session.meetingArchiveHandler)
    }

    func testAssistantAndPrivateScoutChatDelegateToSession() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )

        await model.joinAudioOnly()
        await model.askAssistant("What changed?")
        await model.sendScoutChat("What is blocked?")
        await session.emitScoutChatEvents([
            ScoutChatEvent(kind: "query", text: "What is blocked?"),
            ScoutChatEvent(kind: "answer", text: "Native private Scout chat.")
        ])
        await model.resetScoutChat()

        XCTAssertEqual(session.assistantQueries, ["What changed?"])
        XCTAssertEqual(session.scoutChatMessages, ["What is blocked?"])
        XCTAssertEqual(session.scoutChatResetCount, 1)
        XCTAssertEqual(model.scoutChatEvents.map(\.displayText), [])
        XCTAssertEqual(model.statusText, "New Scout thread")
        XCTAssertFalse(model.isScoutChatSending)
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

    func testBoardMutationsDelegateToSession() async {
        let session = MockRoomSession()
        let model = NativeRoomViewModel(
            baseURLString: "https://example.com",
            selectedName: "Tom",
            configLoaderFactory: { _ in MockConfigLoader(participants: []) },
            sessionFactory: { _ in session }
        )
        let payload = BoardCardMutationPayload(
            title: "Native card",
            status: "In Progress",
            owner: "Caitlyn",
            tags: ["native"],
            notes: "Ship native board edit",
            dueDate: "2026-07-01",
            keyDates: [KanbanKeyDate(label: "due", date: "2026-07-01")]
        )

        await model.joinAudioOnly()
        await model.createBoardCard(payload)
        await model.updateBoardCard(id: "card-1", payload: payload)
        await model.deleteBoardCard(id: "card-1")
        await session.emitUndoAvailability(true)
        await model.undoDeletedBoardCard()

        XCTAssertEqual(session.createdBoardCards, [payload])
        XCTAssertEqual(session.updatedBoardCards.map(\.id), ["card-1"])
        XCTAssertEqual(session.updatedBoardCards.map(\.payload), [payload])
        XCTAssertEqual(session.deletedBoardCardIDs, ["card-1"])
        XCTAssertEqual(session.undoDeleteRequestCount, 1)
        XCTAssertEqual(model.statusText, "Card restore requested")
        XCTAssertFalse(model.isBoardMutating)
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
        XCTAssertFalse(model.canUndoDelete)
        XCTAssertEqual(model.statusText, "Left room")
    }

    private static func passingMediaEvidence() -> NativeMediaEvidenceSnapshot {
        NativeMediaEvidenceSnapshot(
            source: NativeMediaQualitySnapshot(
                at: 1_000,
                outboundAudioBytesSent: 1_200,
                outboundAudioPacketsSent: 12,
                outboundVideoBytesSent: 9_000,
                outboundVideoFramesSent: 90,
                inboundAudioPacketsReceived: 80,
                inboundVideoDecoded: 140,
                candidatePair: NativeMediaQualityCandidatePair(
                    protocol: "udp",
                    networkType: "wifi",
                    localCandidateType: "host",
                    remoteCandidateType: "relay",
                    currentRoundTripTime: 0.08
                )
            ),
            capturedAt: "2026-06-29T17:00:00Z",
            client: NativeMediaEvidenceClient(platform: "ios", version: "test"),
            lifecycle: .connected,
            remoteVideoTiles: 1
        )
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

private actor SuspendedConfigLoader: NativeRoomConfigLoading {
    private var requested = false
    private var requestWaiters: [CheckedContinuation<Void, Never>] = []
    private var configContinuation: CheckedContinuation<NativeClientConfig, Error>?

    func nativeConfig() async throws -> NativeClientConfig {
        requested = true
        requestWaiters.forEach { $0.resume() }
        requestWaiters.removeAll()
        return try await withCheckedThrowingContinuation { continuation in
            configContinuation = continuation
        }
    }

    func waitUntilRequested() async {
        if requested { return }
        await withCheckedContinuation { continuation in
            requestWaiters.append(continuation)
        }
    }

    func resume(participants: [Participant] = []) {
        configContinuation?.resume(returning: Self.config(participants: participants))
        configContinuation = nil
    }

    private static func config(participants: [Participant]) -> NativeClientConfig {
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
    private let screenShareError: Error?
    private let iceRestartError: Error?
    private(set) var remoteVideoTrackHandler: NativeRemoteVideoTrackInfoHandler?
    private(set) var roomSnapshotHandler: NativeRoomSnapshotHandler?
    private(set) var boardStateHandler: NativeBoardStateHandler?
    private(set) var undoAvailabilityHandler: NativeUndoAvailabilityHandler?
    private(set) var assistantEventsHandler: NativeAssistantEventsHandler?
    private(set) var memoryEntriesHandler: NativeMemoryEntriesHandler?
    private(set) var meetingArchiveHandler: NativeMeetingArchiveHandler?
    private(set) var scoutChatEventsHandler: NativeScoutChatEventsHandler?
    private(set) var mediaRecoveryHandler: NativeMediaRecoveryHandler?
    private(set) var mediaEvidenceHandler: NativeMediaEvidenceHandler?
    private(set) var joinedName: String?
    private(set) var joinedPassword: String?
    private(set) var didJoinWithCamera = false
    private(set) var didLeave = false
    private(set) var isMuted = false
    private(set) var isCameraOff = true
    private(set) var screenSharingChanges: [Bool] = []
    private(set) var iceRestartReasons: [String] = []
    private(set) var mediaStatePublishCount = 0
    private(set) var recordingEnabledChanges: [Bool] = []
    private(set) var archiveRequestCount = 0
    private(set) var createdBoardCards: [BoardCardMutationPayload] = []
    private(set) var updatedBoardCards: [(id: String, payload: BoardCardMutationPayload)] = []
    private(set) var deletedBoardCardIDs: [String] = []
    private(set) var undoDeleteRequestCount = 0
    private(set) var assistantQueries: [String] = []
    private(set) var scoutChatMessages: [String] = []
    private(set) var scoutChatResetCount = 0

    private var lifecycle: RoomLifecycleState = .connected
    private let mediaEvidenceSnapshot: NativeMediaEvidenceSnapshot

    init(
        error: Error? = nil,
        screenShareError: Error? = nil,
        iceRestartError: Error? = nil,
        mediaEvidenceSnapshot: NativeMediaEvidenceSnapshot = NativeMediaEvidenceSnapshot(source: NativeMediaQualitySnapshot())
    ) {
        self.error = error
        self.screenShareError = screenShareError
        self.iceRestartError = iceRestartError
        self.mediaEvidenceSnapshot = mediaEvidenceSnapshot
    }

    func joinAudioOnly(name: String, password: String) async throws -> NativeRoomJoinResult {
        if let error { throw error }
        joinedName = name
        joinedPassword = password
        lifecycle = .connected
        return joinResult(name: name)
    }

    func joinWithCamera(name: String, password: String) async throws -> NativeRoomJoinResult {
        if let error { throw error }
        joinedName = name
        joinedPassword = password
        didJoinWithCamera = true
        isCameraOff = false
        lifecycle = .connected
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

    func setUndoAvailabilityHandler(_ handler: NativeUndoAvailabilityHandler?) async {
        undoAvailabilityHandler = handler
    }

    func setAssistantEventsHandler(_ handler: NativeAssistantEventsHandler?) async {
        assistantEventsHandler = handler
    }

    func setMemoryEntriesHandler(_ handler: NativeMemoryEntriesHandler?) async {
        memoryEntriesHandler = handler
    }

    func setMeetingArchiveHandler(_ handler: NativeMeetingArchiveHandler?) async {
        meetingArchiveHandler = handler
    }

    func setScoutChatEventsHandler(_ handler: NativeScoutChatEventsHandler?) async {
        scoutChatEventsHandler = handler
    }

    func setMediaRecoveryHandler(_ handler: NativeMediaRecoveryHandler?) async {
        mediaRecoveryHandler = handler
    }

    func setMediaEvidenceHandler(_ handler: NativeMediaEvidenceHandler?) async {
        mediaEvidenceHandler = handler
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

    func setScreenSharing(_ sharing: Bool) async throws {
        if let screenShareError { throw screenShareError }
        screenSharingChanges.append(sharing)
    }

    func requestICERestart(reason: String) async throws {
        iceRestartReasons.append(reason)
        if let iceRestartError { throw iceRestartError }
        lifecycle = .reconnecting
    }

    func captureMediaEvidenceSnapshot() async throws -> NativeMediaEvidenceSnapshot {
        await mediaEvidenceHandler?(mediaEvidenceSnapshot)
        return mediaEvidenceSnapshot
    }

    func setRecordingEnabled(_ enabled: Bool) async throws {
        recordingEnabledChanges.append(enabled)
    }

    func archiveMeeting() async throws {
        archiveRequestCount += 1
    }

    func askAssistant(_ query: String) async throws {
        assistantQueries.append(query)
    }

    func sendScoutChat(_ text: String) async throws {
        scoutChatMessages.append(text)
    }

    func resetScoutChat() async throws {
        scoutChatResetCount += 1
    }

    func createBoardCard(_ payload: BoardCardMutationPayload) async throws {
        createdBoardCards.append(payload)
    }

    func updateBoardCard(id: String, payload: BoardCardMutationPayload) async throws {
        updatedBoardCards.append((id: id, payload: payload))
    }

    func deleteBoardCard(id: String) async throws {
        deletedBoardCardIDs.append(id)
    }

    func undoDeletedBoardCard() async throws {
        undoDeleteRequestCount += 1
    }

    func emitRoomSnapshot(_ snapshot: RoomSnapshot) async {
        await roomSnapshotHandler?(snapshot)
    }

    func emitBoardState(_ state: BoardState) async {
        await boardStateHandler?(state)
    }

    func emitUndoAvailability(_ canUndo: Bool) async {
        await undoAvailabilityHandler?(canUndo)
    }

    func emitAssistantEvents(_ events: [AssistantEvent]) async {
        await assistantEventsHandler?(events)
    }

    func emitMemoryEntries(_ entries: [MemoryEntry]) async {
        await memoryEntriesHandler?(entries)
    }

    func emitMeetingArchive(_ archive: MeetingArchiveResult) async {
        await meetingArchiveHandler?(archive)
    }

    func emitScoutChatEvents(_ events: [ScoutChatEvent]) async {
        await scoutChatEventsHandler?(events)
    }

    func emitMediaRecoveryEvent(_ event: NativeMediaRecoveryEvent) async {
        await mediaRecoveryHandler?(event)
    }

    func emitMediaEvidence(_ evidence: NativeMediaEvidenceSnapshot) async {
        await mediaEvidenceHandler?(evidence)
    }

    func sendParticipantMediaState() async throws {
        mediaStatePublishCount += 1
    }

    func leave() async {
        didLeave = true
        lifecycle = .signedOut
    }

    func currentLifecycle() async -> RoomLifecycleState {
        lifecycle
    }
}
