import XCTest
@testable import MeetingAssistCore
@testable import MeetingAssistMedia
@testable import MeetingAssistRoom
@testable import MeetingAssistRoomRTC

final class NativeRoomSessionCoordinatorTests: XCTestCase {
    func testJoinConfiguresAudioSessionBeforePreparingLocalMedia() async throws {
        let api = MockNativeRoomAPI()
        let signaling = MockSignalingTransport(envelopes: [
            kanbanEnvelope(event: "participants", data: .object(["participants": .array([])])),
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"))
            )
        ])
        let steps = JoinStepRecorder()
        let media = MediaSessionCoordinator(audioSessionConfigurator: {
            steps.append("configure-audio-session")
        })
        let rtc = MockRoomRTCClient(answerSDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=sendrecv\r\n") {
            steps.append("prepare-local-media")
        }
        let coordinator = NativeRoomSessionCoordinator(
            api: api,
            signaling: signaling,
            rtc: rtc,
            media: media,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        _ = try await coordinator.joinAudioOnly(name: "Tom", password: "B0NFIRE!")

        XCTAssertEqual(steps.values, ["configure-audio-session", "prepare-local-media"])
    }

    func testJoinAudioOnlyRunsCookieAuthServerOfferSequence() async throws {
        let api = MockNativeRoomAPI()
        let signaling = MockSignalingTransport(envelopes: [
            kanbanEnvelope(event: "participants", data: .object(["participants": .array([])])),
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.candidate,
                data: encodedJSONString(RTCIceCandidatePayload(candidate: "candidate:0 1 udp 1 127.0.0.1 9 typ host"))
            ),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(answerSDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=sendrecv\r\n")
        let coordinator = NativeRoomSessionCoordinator(
            api: api,
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        let result = try await coordinator.joinAudioOnly(name: "Tom", password: "B0NFIRE!")

        XCTAssertEqual(result.participant, Participant(name: "Tom", email: "tom@example.com"))
        XCTAssertEqual(result.websocketURL.absoluteString, "wss://thebonfire.xyz/websocket")
        XCTAssertEqual(result.clientConfig.protocolVersion, meetingAssistNativeProtocolV1)
        XCTAssertEqual(result.answeredOffer.type, "answer")
        let lifecycle = await coordinator.lifecycle
        XCTAssertEqual(lifecycle, .connected)

        let sentEvents = signaling.sent.map(\.event)
        XCTAssertEqual(sentEvents, [
            ClientSignalEvent.participant,
            ClientSignalEvent.mediaReady,
            ClientSignalEvent.answer,
            ClientSignalEvent.participantMediaState
        ])

        let mediaReady = try decodeSentPayload(MediaReadyAssertionPayload.self, from: signaling.sent[1].data)
        XCTAssertEqual(mediaReady.client.platform, "ios")
        XCTAssertTrue(mediaReady.media.audio)
        XCTAssertFalse(mediaReady.media.video)

        XCTAssertEqual(rtc.preparedAudio, true)
        XCTAssertEqual(rtc.preparedVideo, false)
        XCTAssertFalse(rtc.localAudioEnabledChanges.contains(false))
        XCTAssertFalse(rtc.localVideoEnabledChanges.contains(true))
        XCTAssertEqual(rtc.configured?.websocketPath, "/websocket")
        XCTAssertEqual(rtc.handledOffers, ["v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"])
        XCTAssertEqual(rtc.remoteCandidates.count, 1)
    }

    func testJoinWithCameraAdvertisesVideoAndPublishesCameraState() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(answerSDP: "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\na=sendrecv\r\n")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        _ = try await coordinator.joinWithCamera(name: "Tom", password: "B0NFIRE!")

        XCTAssertEqual(rtc.preparedAudio, true)
        XCTAssertEqual(rtc.preparedVideo, true)

        let sent = signaling.sent
        XCTAssertEqual(sent.map(\.event), [
            ClientSignalEvent.participant,
            ClientSignalEvent.mediaReady,
            ClientSignalEvent.answer,
            ClientSignalEvent.participantMediaState
        ])
        let mediaReady = try decodeSentPayload(MediaReadyAssertionPayload.self, from: sent[1].data)
        XCTAssertTrue(mediaReady.media.audio)
        XCTAssertTrue(mediaReady.media.video)
        let mediaState = try decodeSentPayload(ParticipantMediaState.self, from: sent[3].data)
        XCTAssertFalse(mediaState.cameraOff)
    }

    func testGeneratedLocalCandidateUsesExistingCandidateEvent() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        _ = try await coordinator.joinAudioOnly(name: "Tom", password: "B0NFIRE!")
        await rtc.emitLocalCandidate(
            RTCIceCandidatePayload(
                candidate: "candidate:local",
                sdpMid: "0",
                sdpMLineIndex: 0,
                usernameFragment: "native"
            )
        )

        let sent = signaling.sent
        XCTAssertEqual(sent.map(\.event), [
            ClientSignalEvent.participant,
            ClientSignalEvent.mediaReady,
            ClientSignalEvent.answer,
            ClientSignalEvent.participantMediaState,
            ClientSignalEvent.candidate
        ])
        XCTAssertEqual(
            try decodeSentPayload(RTCIceCandidatePayload.self, from: sent[4].data),
            RTCIceCandidatePayload(candidate: "candidate:local", sdpMid: "0", sdpMLineIndex: 0, usernameFragment: "native")
        )
    }

    func testRestartIceAndLayerSelectionUseExistingEvents() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "macos", version: "test")
        )

        try await coordinator.requestICERestart(reason: "native-network-change")
        try await coordinator.selectLayer("low")

        let sent = signaling.sent
        XCTAssertEqual(sent.map(\.event), [ClientSignalEvent.restartICE, ClientSignalEvent.selectLayer])
        XCTAssertEqual(try decodeSentPayload(RestartAssertionPayload.self, from: sent[0].data).reason, "native-network-change")
        XCTAssertEqual(try decodeSentPayload(SelectLayerAssertionPayload.self, from: sent[1].data).layer, "low")
        let lifecycle = await coordinator.lifecycle
        XCTAssertEqual(lifecycle, .reconnecting)
        XCTAssertTrue(rtc.didRestartICE)
    }

    func testParticipantMediaStatePublicationUsesExistingEvent() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        await coordinator.setMuted(true)
        await coordinator.setCameraOff(true)
        try await coordinator.sendParticipantMediaState()

        let sent = signaling.sent
        XCTAssertEqual(sent.map(\.event), [ClientSignalEvent.participantMediaState])
        let state = try decodeSentPayload(ParticipantMediaState.self, from: sent[0].data)
        XCTAssertTrue(state.micMuted)
        XCTAssertTrue(state.cameraOff)
        XCTAssertFalse(state.screenSharing)
        XCTAssertEqual(rtc.localAudioEnabledChanges, [false])
        XCTAssertEqual(rtc.localVideoEnabledChanges, [false])
    }

    func testScreenShareStartStopUsesBrowserCompatibleSignals() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "macos", version: "test")
        )

        try await coordinator.setScreenSharing(true)
        try await coordinator.setScreenSharing(false)

        let sent = signaling.sent
        XCTAssertEqual(sent.map(\.event), [
            ClientSignalEvent.participantMediaState,
            ClientSignalEvent.screenShareStarted,
            ClientSignalEvent.screenShareStopped,
            ClientSignalEvent.participantMediaState
        ])
        XCTAssertEqual(rtc.screenShareEnabledChanges, [true, false])
        XCTAssertTrue(try decodeSentPayload(ParticipantMediaState.self, from: sent[0].data).screenSharing)
        XCTAssertEqual(sent[1].data, "{}")
        XCTAssertEqual(sent[2].data, "{}")
        XCTAssertFalse(try decodeSentPayload(ParticipantMediaState.self, from: sent[3].data).screenSharing)
    }

    func testScreenShareStartFailureDoesNotAnnounceShare() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let rtc = MockRoomRTCClient(
            answerSDP: "answer",
            screenShareError: RoomRTCError.screenCapturePermissionDenied
        )
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "macos", version: "test")
        )

        do {
            try await coordinator.setScreenSharing(true)
            XCTFail("permission denial should fail before announcing screen share")
        } catch RoomRTCError.screenCapturePermissionDenied {
        } catch {
            XCTFail("unexpected error: \(error)")
        }

        XCTAssertEqual(signaling.sent.map(\.event), [])
        XCTAssertEqual(rtc.screenShareEnabledChanges, [true])
    }

    func testIncomingScreenShareEventsUpdateRoomSnapshotMediaState() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "macos", version: "test")
        )
        let snapshots = RoomSnapshotCollector()
        await coordinator.setRoomSnapshotHandler { snapshot in
            await snapshots.append(snapshot)
        }

        try await coordinator.handleServerEvent(roomSnapshotEnvelope(participants: ["Tom", "Caitlyn"], recordingEnabled: true))
        try await coordinator.handleServerEvent(screenShareEnvelope(event: "screen_share_started", name: "Caitlyn"))
        try await coordinator.handleServerEvent(screenShareEnvelope(event: "screen_share_stopped", name: "Caitlyn"))

        let screenSharingStates = await snapshots.screenSharingStates(for: "Caitlyn")
        XCTAssertEqual(screenSharingStates, [false, true, false])
    }

    func testRoomAndBoardSnapshotsAreEmittedDuringJoin() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            roomSnapshotEnvelope(participants: ["Tom", "Caitlyn"], recordingEnabled: false),
            accessGrantedEnvelope(name: "Tom"),
            boardEnvelope(cards: [
                KanbanCard(id: "card-1", status: "In Progress", title: "Native board", owner: "Caitlyn")
            ]),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        let roomSnapshots = RoomSnapshotCollector()
        let boardStates = BoardStateCollector()
        await coordinator.setRoomSnapshotHandler { snapshot in
            await roomSnapshots.append(snapshot)
        }
        await coordinator.setBoardStateHandler { board in
            await boardStates.append(board)
        }

        _ = try await coordinator.joinAudioOnly(name: "Tom", password: "B0NFIRE!")

        let participantSnapshots = await roomSnapshots.participants()
        let recordingStates = await roomSnapshots.recordingStates()
        let boardTitles = await boardStates.titles()
        XCTAssertEqual(participantSnapshots, [["Tom", "Caitlyn"]])
        XCTAssertEqual(recordingStates, [false])
        XCTAssertEqual(boardTitles, [["Native board"]])
    }

    func testRecordingAndArchiveUseExistingRoomEvents() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "macos", version: "test")
        )

        try await coordinator.setRecordingEnabled(false)
        try await coordinator.archiveMeeting()

        XCTAssertEqual(signaling.sent.map(\.event), [ClientSignalEvent.setRecording, ClientSignalEvent.archiveMeeting])
        XCTAssertEqual(try decodeSentPayload(RecordingAssertionPayload.self, from: signaling.sent[0].data).enabled, false)
        XCTAssertEqual(signaling.sent[1].data, "{}")
    }

    func testAssistantAndScoutChatUseExistingRoomEvents() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "macos", version: "test")
        )

        try await coordinator.askAssistant("What changed?")
        try await coordinator.sendScoutChat("What is blocked?")
        try await coordinator.resetScoutChat()

        XCTAssertEqual(signaling.sent.map(\.event), [
            ClientSignalEvent.assistantQuery,
            ClientSignalEvent.scoutChat,
            ClientSignalEvent.scoutChatReset
        ])
        XCTAssertEqual(try decodeSentPayload(AssistantQueryAssertionPayload.self, from: signaling.sent[0].data).query, "What changed?")
        XCTAssertEqual(try decodeSentPayload(ScoutChatAssertionPayload.self, from: signaling.sent[1].data).text, "What is blocked?")
        XCTAssertEqual(signaling.sent[2].data, "{}")
    }

    func testBoardMutationsUseExistingRoomEvents() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "macos", version: "test")
        )
        let payload = BoardCardMutationPayload(
            title: "Native edit",
            status: "In Progress",
            owner: "Caitlyn",
            tags: ["native", "board"],
            notes: "Full-card update",
            dueDate: "2026-07-01",
            keyDates: [KanbanKeyDate(label: "due", date: "2026-07-01")]
        )

        try await coordinator.createBoardCard(payload)
        try await coordinator.updateBoardCard(id: "card-1", payload: payload)
        try await coordinator.deleteBoardCard(id: "card-1")
        try await coordinator.undoDeletedBoardCard()

        XCTAssertEqual(signaling.sent.map(\.event), [
            ClientSignalEvent.manualCreateTicket,
            ClientSignalEvent.manualUpdateTicket,
            ClientSignalEvent.manualDeleteTicket,
            ClientSignalEvent.undoDeleteTicket
        ])
        let created = try decodeSentPayload(BoardCardMutationPayload.self, from: signaling.sent[0].data)
        XCTAssertNil(created.cardID)
        XCTAssertEqual(created.title, "Native edit")
        XCTAssertEqual(created.status, "In Progress")
        XCTAssertEqual(created.tags, ["native", "board"])
        XCTAssertEqual(created.keyDates, [KanbanKeyDate(label: "due", date: "2026-07-01")])

        let updated = try decodeSentPayload(BoardCardMutationPayload.self, from: signaling.sent[1].data)
        XCTAssertEqual(updated.cardID, "card-1")
        XCTAssertEqual(updated.title, "Native edit")
        XCTAssertEqual(try decodeSentPayload(BoardDeleteAssertionPayload.self, from: signaling.sent[2].data).cardID, "card-1")
        XCTAssertEqual(signaling.sent[3].data, "{}")
    }

    func testUndoAvailabilityIsEmittedAndReplayed() async throws {
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: MockSignalingTransport(envelopes: []),
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        let first = UndoAvailabilityCollector()
        let replay = UndoAvailabilityCollector()
        await coordinator.setUndoAvailabilityHandler { canUndo in
            await first.append(canUndo)
        }

        try await coordinator.handleServerEvent(kanbanEnvelope(event: "undo_available", data: .bool(true)))
        await coordinator.setUndoAvailabilityHandler { canUndo in
            await replay.append(canUndo)
        }

        let firstValues = await first.values()
        let replayValues = await replay.values()
        XCTAssertEqual(firstValues, [true])
        XCTAssertEqual(replayValues, [true])
    }

    func testAssistantMemoryAndArchiveEventsAreEmittedAndReplayed() async throws {
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: MockSignalingTransport(envelopes: []),
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        let assistantEvents = AssistantEventsCollector()
        let memoryEntries = MemoryEntriesCollector()
        let archives = MeetingArchiveCollector()
        let replayedAssistantEvents = AssistantEventsCollector()
        let replayedMemoryEntries = MemoryEntriesCollector()
        let replayedArchives = MeetingArchiveCollector()
        await coordinator.setAssistantEventsHandler { events in
            await assistantEvents.append(events)
        }
        await coordinator.setMemoryEntriesHandler { entries in
            await memoryEntries.append(entries)
        }
        await coordinator.setMeetingArchiveHandler { archive in
            await archives.append(archive)
        }

        try await coordinator.handleServerEvent(assistantEventEnvelope(kind: "answer", text: "The board is healthy."))
        try await coordinator.handleServerEvent(
            memoryEnvelope(entries: [
                MemoryEntry(id: "memory-1", kind: "brain", text: "Launch plan summarized.", createdAt: "2026-06-24T21:00:00Z")
            ])
        )
        try await coordinator.handleServerEvent(
            memoryEntryEnvelope(event: "memory_transcript", entry: MemoryEntry(id: "memory-2", kind: "transcript", text: "Tom: We need native Scout.", metadata: ["speaker": "Tom"]))
        )
        try await coordinator.handleServerEvent(
            kanbanEnvelope(
                event: "memory_answer",
                data: .object([
                    "query": .string("What changed?"),
                    "answer": .string("Native Scout feed was added.")
                ])
            )
        )
        try await coordinator.handleServerEvent(
            archiveEnvelope(
                MeetingArchiveResult(
                    id: "meeting-20260624",
                    meetingID: "meeting-current",
                    archivedAt: "2026-06-24T21:10:00Z",
                    archivedBy: "Tom",
                    downloadURL: "/archives/meeting-20260624.json?key=signed",
                    summary: "Tom archived meeting meeting-20260624 with 2 transcript item(s), 1 board card(s), 2 participant(s), and 1 project status item(s).",
                    email: MeetingArchiveEmailStatus(skipped: true),
                    artifact: MemoryEntry(id: "meeting-20260624-artifact", kind: "os_artifact", text: "Meeting artifact")
                )
            )
        )
        await coordinator.setAssistantEventsHandler { events in
            await replayedAssistantEvents.append(events)
        }
        await coordinator.setMemoryEntriesHandler { entries in
            await replayedMemoryEntries.append(entries)
        }
        await coordinator.setMeetingArchiveHandler { archive in
            await replayedArchives.append(archive)
        }

        let assistantTexts = await assistantEvents.latestTexts()
        let replayedAssistantTexts = await replayedAssistantEvents.latestTexts()
        let memoryKinds = await memoryEntries.latestKinds()
        let replayedMemoryKinds = await replayedMemoryEntries.latestKinds()
        let archiveIDs = await archives.ids()
        let replayedArchiveIDs = await replayedArchives.ids()
        XCTAssertEqual(assistantTexts, ["The board is healthy.", "Tom archived meeting meeting-20260624 with 2 transcript item(s), 1 board card(s), 2 participant(s), and 1 project status item(s)."])
        XCTAssertEqual(replayedAssistantTexts, assistantTexts)
        XCTAssertEqual(memoryKinds, ["brain", "transcript", "answer", "os_artifact"])
        XCTAssertEqual(replayedMemoryKinds, memoryKinds)
        XCTAssertEqual(archiveIDs, ["meeting-20260624"])
        XCTAssertEqual(replayedArchiveIDs, ["meeting-20260624"])
    }

    func testScoutChatEventsAreEmittedAndReplayed() async throws {
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: MockSignalingTransport(envelopes: []),
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        let scoutChat = ScoutChatEventsCollector()
        let replay = ScoutChatEventsCollector()
        await coordinator.setScoutChatEventsHandler { events in
            await scoutChat.append(events)
        }

        try await coordinator.handleServerEvent(scoutChatEnvelope(kind: "query", text: "What is blocked?"))
        try await coordinator.handleServerEvent(scoutChatEnvelope(kind: "status", text: "thinking..."))
        try await coordinator.handleServerEvent(scoutChatEnvelope(kind: "answer", text: "Native Scout chat is next."))
        await coordinator.setScoutChatEventsHandler { events in
            await replay.append(events)
        }

        let kinds = await scoutChat.latestKinds()
        let replayedKinds = await replay.latestKinds()
        XCTAssertEqual(kinds, ["query", "status", "answer"])
        XCTAssertEqual(replayedKinds, kinds)

        try await coordinator.handleServerEvent(scoutChatEnvelope(kind: "reset", text: "new Scout thread started"))
        let resetKinds = await replay.latestKinds()
        XCTAssertEqual(resetKinds, ["reset"])
    }

    func testScoutChatThreadPayloadPreservesThreadActionsAndArtifact() async throws {
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: MockSignalingTransport(envelopes: []),
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        let scoutChat = ScoutChatEventsCollector()
        let memoryEntries = MemoryEntriesCollector()
        await coordinator.setScoutChatEventsHandler { events in
            await scoutChat.append(events)
        }
        await coordinator.setMemoryEntriesHandler { entries in
            await memoryEntries.append(entries)
        }

        try await coordinator.handleServerEvent(scoutChatThreadEnvelope())

        let latestEvents = await scoutChat.latestEvents()
        let memoryKinds = await memoryEntries.latestKinds()
        XCTAssertEqual(latestEvents.map(\.kind), ["thread"])
        XCTAssertEqual(latestEvents.first?.thread?.id, "agent-thread-research-1")
        XCTAssertEqual(latestEvents.first?.thread?.artifact?.id, "artifact-1")
        XCTAssertEqual(latestEvents.first?.actions?.first?.artifactID, "artifact-1")
        XCTAssertEqual(memoryKinds, ["os_artifact"])
    }

    func testParticipantTrackMetadataLabelsLaterRemoteVideoTrack() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            participantTrackEnvelope(name: "Caitlyn", kind: "video", trackId: "forwarded-video-1", sourceTrackId: "source-video-1", streamId: "stream-1"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        let emitted = RemoteVideoInfoCollector()
        await coordinator.setRemoteVideoTrackHandler { info in
            await emitted.append(info)
        }

        _ = try await coordinator.joinWithCamera(name: "Tom", password: "B0NFIRE!")
        await rtc.emitRemoteVideoTrack(NativeRemoteVideoTrack(id: "forwarded-video-1", streamIds: ["stream-1"]))

        let displayNames = await emitted.displayNames()
        XCTAssertEqual(displayNames, ["Caitlyn"])
    }

    func testParticipantTrackMetadataRelabelsExistingRemoteVideoTrack() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        let emitted = RemoteVideoInfoCollector()
        await coordinator.setRemoteVideoTrackHandler { info in
            await emitted.append(info)
        }

        _ = try await coordinator.joinWithCamera(name: "Tom", password: "B0NFIRE!")
        await rtc.emitRemoteVideoTrack(NativeRemoteVideoTrack(id: "forwarded-video-2", streamIds: ["stream-2"]))
        try await coordinator.handleServerEvent(
            participantTrackEnvelope(name: "Caitlyn", kind: "video", trackId: "forwarded-video-2", sourceTrackId: "source-video-2", streamId: "stream-2")
        )

        let displayNames = await emitted.displayNames()
        XCTAssertEqual(displayNames, ["forwarded-video-2", "Caitlyn"])
        XCTAssertEqual(signaling.sent.last?.event, ClientSignalEvent.requestParticipantTracks)
    }

    func testAccessDeniedStopsJoinBeforeMediaReady() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            kanbanEnvelope(event: "access_denied", data: .string("Room is full."))
        ])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        do {
            _ = try await coordinator.joinAudioOnly(name: "Tom", password: "B0NFIRE!")
            XCTFail("join should fail on access_denied")
        } catch NativeRoomSessionError.accessDenied("Room is full.") {
        } catch {
            XCTFail("unexpected error: \(error)")
        }

        XCTAssertEqual(signaling.sent.map(\.event), [ClientSignalEvent.participant])
        XCTAssertNil(rtc.configured)
        XCTAssertNil(rtc.preparedAudio)
    }

    func testLeaveResetsNegotiationStateBeforeReuse() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.candidate,
                data: encodedJSONString(RTCIceCandidatePayload(candidate: "candidate:old"))
            ),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(answerSDP: "answer")
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        _ = try await coordinator.joinAudioOnly(name: "Tom", password: "B0NFIRE!")
        XCTAssertEqual(rtc.remoteCandidates.count, 1)

        await coordinator.leave()
        try await coordinator.handleServerEvent(
            WebSocketEnvelope(
                event: ServerSignalEvent.candidate,
                data: encodedJSONString(RTCIceCandidatePayload(candidate: "candidate:new-session"))
            )
        )

        XCTAssertEqual(rtc.remoteCandidates.count, 1)
    }

    func testWebsocketURLPreservesBasePathAndUsesSecureScheme() {
        let url = NativeRoomSessionCoordinator.websocketURL(
            baseURL: URL(string: "https://example.com/app")!,
            path: "/websocket"
        )

        XCTAssertEqual(url.absoluteString, "wss://example.com/app/websocket")
    }
}

private final class MockNativeRoomAPI: NativeRoomAPIProviding, @unchecked Sendable {
    let baseURL = URL(string: "https://thebonfire.xyz")!

    func nativeConfig() async throws -> NativeClientConfig {
        NativeClientConfig(
            protocolVersion: meetingAssistNativeProtocolV1,
            auth: .init(mode: "cookie", loginPath: "/auth/login", mePath: "/auth/me", logoutPath: "/auth/logout"),
            room: .init(
                clientConfigPath: "/client-config",
                websocketPath: "/websocket",
                participants: [Participant(name: "Tom", email: "tom@example.com")],
                maxParticipants: 7
            )
        )
    }

    func login(name: String, password: String, path: String) async throws -> Participant {
        XCTAssertEqual(name, "Tom")
        XCTAssertEqual(password, "B0NFIRE!")
        XCTAssertEqual(path, "/auth/login")
        return Participant(name: "Tom", email: "tom@example.com")
    }

    func clientConfig(path: String) async throws -> ClientRTCConfig {
        XCTAssertEqual(path, "/client-config")
        return ClientRTCConfig(
            rtcConfiguration: ["iceServers": .array([])],
            protocolVersion: meetingAssistNativeProtocolV1,
            auth: "cookie",
            websocketPath: "/websocket",
            signalingRole: "server-offer",
            supportedLayers: ["low", "medium", "high"],
            nativeHints: nil
        )
    }
}

private final class MockSignalingTransport: NativeRoomSignalingTransport, @unchecked Sendable {
    private var envelopes: [WebSocketEnvelope]
    private(set) var connectedURL: URL?
    private(set) var sent: [WebSocketEnvelope] = []

    init(envelopes: [WebSocketEnvelope]) {
        self.envelopes = envelopes
    }

    func connect(to url: URL) async {
        connectedURL = url
    }

    func send(event: String, data: String) async throws {
        sent.append(WebSocketEnvelope(event: event, data: data))
    }

    func receive() async throws -> WebSocketEnvelope {
        if envelopes.isEmpty {
            throw MockError.noEnvelope
        }
        return envelopes.removeFirst()
    }

    func close() async {}
}

private final class MockRoomRTCClient: RoomRTCClient, @unchecked Sendable {
    private(set) var lifecycle: RoomLifecycleState = .signedOut
    private(set) var configured: ClientRTCConfig?
    private var localCandidateHandler: LocalICECandidateHandler?
    private var remoteVideoTrackHandler: RemoteVideoTrackHandler?
    private(set) var preparedAudio: Bool?
    private(set) var preparedVideo: Bool?
    private(set) var handledOffers: [String] = []
    private(set) var remoteCandidates: [String] = []
    private(set) var localAudioEnabledChanges: [Bool] = []
    private(set) var localVideoEnabledChanges: [Bool] = []
    private(set) var screenShareEnabledChanges: [Bool] = []
    private(set) var didRestartICE = false
    private let answerSDP: String
    private let screenShareError: Error?
    private let onPrepareLocalMedia: (@Sendable () -> Void)?

    init(
        answerSDP: String,
        screenShareError: Error? = nil,
        onPrepareLocalMedia: (@Sendable () -> Void)? = nil
    ) {
        self.answerSDP = answerSDP
        self.screenShareError = screenShareError
        self.onPrepareLocalMedia = onPrepareLocalMedia
    }

    func configure(_ config: ClientRTCConfig) async throws {
        configured = config
    }

    func setLocalCandidateHandler(_ handler: LocalICECandidateHandler?) async {
        localCandidateHandler = handler
    }

    func setRemoteVideoTrackHandler(_ handler: RemoteVideoTrackHandler?) async {
        remoteVideoTrackHandler = handler
    }

    func emitLocalCandidate(_ candidate: RTCIceCandidatePayload) async {
        await localCandidateHandler?(candidate)
    }

    func emitRemoteVideoTrack(_ track: NativeRemoteVideoTrack) async {
        await remoteVideoTrackHandler?(track)
    }

    func prepareLocalMedia(audio: Bool, video: Bool) async throws {
        onPrepareLocalMedia?()
        preparedAudio = audio
        preparedVideo = video
        lifecycle = .preparingMedia
    }

    func setLocalAudioEnabled(_ enabled: Bool) async {
        localAudioEnabledChanges.append(enabled)
    }

    func setLocalVideoEnabled(_ enabled: Bool) async {
        localVideoEnabledChanges.append(enabled)
    }

    func setScreenShareEnabled(_ enabled: Bool) async throws {
        screenShareEnabledChanges.append(enabled)
        if let screenShareError, enabled {
            throw screenShareError
        }
    }

    func handleOffer(_ sdp: String) async throws -> String {
        handledOffers.append(sdp)
        lifecycle = .negotiating
        return answerSDP
    }

    func addRemoteCandidate(_ json: String) async throws {
        remoteCandidates.append(json)
    }

    func restartICE() async {
        didRestartICE = true
        lifecycle = .reconnecting
    }

    func leave() async {
        lifecycle = .leaving
    }
}

private struct MediaReadyAssertionPayload: Decodable {
    var client: NativeRoomClientIdentity
    var media: MediaAssertionPayload
}

private struct MediaAssertionPayload: Decodable {
    var audio: Bool
    var video: Bool
}

private struct RestartAssertionPayload: Decodable {
    var reason: String
}

private struct SelectLayerAssertionPayload: Decodable {
    var layer: String
}

private struct RecordingAssertionPayload: Decodable {
    var enabled: Bool
}

private struct AssistantQueryAssertionPayload: Decodable {
    var query: String
}

private struct ScoutChatAssertionPayload: Decodable {
    var text: String
}

private struct BoardDeleteAssertionPayload: Decodable {
    var cardID: String

    enum CodingKeys: String, CodingKey {
        case cardID = "card_id"
    }
}

private enum MockError: Error {
    case noEnvelope
}

private actor RemoteVideoInfoCollector {
    private var values: [NativeRemoteVideoTrackInfo] = []

    func append(_ info: NativeRemoteVideoTrackInfo) {
        values.append(info)
    }

    func displayNames() -> [String] {
        values.map(\.displayName)
    }
}

private actor RoomSnapshotCollector {
    private var values: [RoomSnapshot] = []

    func append(_ snapshot: RoomSnapshot) {
        values.append(snapshot)
    }

    func participants() -> [[String]] {
        values.map(\.participants)
    }

    func recordingStates() -> [Bool?] {
        values.map { $0.recording?.enabled }
    }

    func screenSharingStates(for name: String) -> [Bool?] {
        values.map { $0.mediaStates?[name]?.screenSharing }
    }
}

private actor BoardStateCollector {
    private var values: [BoardState] = []

    func append(_ state: BoardState) {
        values.append(state)
    }

    func titles() -> [[String]] {
        values.map { $0.cards.map(\.title) }
    }
}

private actor UndoAvailabilityCollector {
    private var storedValues: [Bool] = []

    func append(_ value: Bool) {
        storedValues.append(value)
    }

    func values() -> [Bool] {
        storedValues
    }
}

private actor AssistantEventsCollector {
    private var values: [[AssistantEvent]] = []

    func append(_ events: [AssistantEvent]) {
        values.append(events)
    }

    func latestTexts() -> [String] {
        values.last?.map(\.displayText) ?? []
    }
}

private actor MemoryEntriesCollector {
    private var values: [[MemoryEntry]] = []

    func append(_ entries: [MemoryEntry]) {
        values.append(entries)
    }

    func latestKinds() -> [String] {
        values.last?.map(\.kind) ?? []
    }

}

private actor MeetingArchiveCollector {
    private var values: [MeetingArchiveResult] = []

    func append(_ archive: MeetingArchiveResult) {
        values.append(archive)
    }

    func ids() -> [String] {
        values.map(\.id)
    }
}

private actor ScoutChatEventsCollector {
    private var values: [[ScoutChatEvent]] = []

    func append(_ events: [ScoutChatEvent]) {
        values.append(events)
    }

    func latestKinds() -> [String] {
        values.last?.map(\.kind) ?? []
    }

    func latestEvents() -> [ScoutChatEvent] {
        values.last ?? []
    }
}

private func accessGrantedEnvelope(name: String) -> WebSocketEnvelope {
    kanbanEnvelope(event: "access_granted", data: .object(["name": .string(name)]))
}

private func participantTrackEnvelope(
    name: String,
    kind: String,
    trackId: String,
    sourceTrackId: String? = nil,
    streamId: String? = nil
) -> WebSocketEnvelope {
    var data: [String: JSONValue] = [
        "name": .string(name),
        "kind": .string(kind),
        "trackId": .string(trackId)
    ]
    if let sourceTrackId {
        data["sourceTrackId"] = .string(sourceTrackId)
    }
    if let streamId {
        data["streamId"] = .string(streamId)
    }
    return kanbanEnvelope(event: "participant_track", data: .object(data))
}

private func roomSnapshotEnvelope(participants: [String], recordingEnabled: Bool = true) -> WebSocketEnvelope {
    kanbanEnvelope(
        event: "participants",
        data: .object([
            "participants": .array(participants.map(JSONValue.string)),
            "capacity": .number(7),
            "occupiedSeats": .number(Double(participants.count)),
            "availableSeats": .number(Double(max(0, 7 - participants.count))),
            "mediaStates": .object([
                "Tom": .object([
                    "micMuted": .bool(false),
                    "cameraOff": .bool(true),
                    "screenSharing": .bool(false)
                ]),
                "Caitlyn": .object([
                    "micMuted": .bool(true),
                    "cameraOff": .bool(false),
                    "screenSharing": .bool(false)
                ])
            ]),
            "recording": .object([
                "enabled": .bool(recordingEnabled),
                "updatedBy": .string("Caitlyn")
            ])
        ])
    )
}

private func screenShareEnvelope(event: String, name: String) -> WebSocketEnvelope {
    kanbanEnvelope(event: event, data: .object(["name": .string(name)]))
}

private func boardEnvelope(cards: [KanbanCard]) -> WebSocketEnvelope {
    kanbanEnvelope(event: "board", data: encodedJSONValue(BoardState(cards: cards, updatedAt: "2026-06-24T21:00:00Z")))
}

private func assistantEventEnvelope(kind: String, text: String) -> WebSocketEnvelope {
    kanbanEnvelope(
        event: "assistant_event",
        data: encodedJSONValue(
            AssistantEvent(
                kind: kind,
                text: text,
                createdAt: "2026-06-24T21:00:00Z"
            )
        )
    )
}

private func memoryEnvelope(entries: [MemoryEntry]) -> WebSocketEnvelope {
    kanbanEnvelope(event: "memory", data: encodedJSONValue(entries))
}

private func memoryEntryEnvelope(event: String, entry: MemoryEntry) -> WebSocketEnvelope {
    kanbanEnvelope(event: event, data: encodedJSONValue(entry))
}

private func archiveEnvelope(_ archive: MeetingArchiveResult) -> WebSocketEnvelope {
    kanbanEnvelope(event: "meeting_archived", data: encodedJSONValue(archive))
}

private func scoutChatEnvelope(kind: String, text: String) -> WebSocketEnvelope {
    kanbanEnvelope(
        event: "scout_chat",
        data: encodedJSONValue(ScoutChatEvent(kind: kind, text: text, timestamp: "2026-06-24T21:00:00Z"))
    )
}

private func scoutChatThreadEnvelope() -> WebSocketEnvelope {
    let artifact = MemoryEntry(
        id: "artifact-1",
        kind: "os_artifact",
        text: "Research thread artifact",
        createdAt: "2026-06-24T21:00:00Z",
        metadata: ["title": "Native Scout research", "mode": "research", "source": "scout_thread"]
    )
    let action: JSONValue = .object([
        "type": .string("open_tool"),
        "tool": .string("chat"),
        "artifactId": .string("artifact-1"),
        "enabled": .bool(true),
        "label": .string("Open thread")
    ])
    return kanbanEnvelope(
        event: "scout_chat",
        data: .object([
            "kind": .string("thread"),
            "text": .string("Research thread launched"),
            "ts": .string("2026-06-24T21:00:00Z"),
            "artifact": encodedJSONValue(artifact),
            "actions": .array([action]),
            "thread": .object([
                "id": .string("agent-thread-research-1"),
                "mode": .string("research"),
                "query": .string("Research native client readiness"),
                "status": .string("running"),
                "artifact": encodedJSONValue(artifact),
                "actions": .array([action])
            ])
        ])
    )
}

private func kanbanEnvelope(event: String, data: JSONValue) -> WebSocketEnvelope {
    WebSocketEnvelope(
        event: ServerSignalEvent.kanban,
        data: encodedJSONString(RoomEvent(event: event, data: data))
    )
}

private final class JoinStepRecorder: @unchecked Sendable {
    private let lock = NSLock()
    private var recordedValues: [String] = []

    var values: [String] {
        lock.withLock { recordedValues }
    }

    func append(_ value: String) {
        lock.withLock {
            recordedValues.append(value)
        }
    }
}

private func encodedJSONString<T: Encodable>(_ value: T) -> String {
    let data = try! JSONEncoder().encode(value)
    return String(decoding: data, as: UTF8.self)
}

private func encodedJSONValue<T: Encodable>(_ value: T) -> JSONValue {
    let data = try! JSONEncoder().encode(value)
    return try! JSONDecoder().decode(JSONValue.self, from: data)
}

private func decodeSentPayload<T: Decodable>(_ type: T.Type, from data: String) throws -> T {
    try JSONDecoder().decode(type, from: Data(data.utf8))
}
