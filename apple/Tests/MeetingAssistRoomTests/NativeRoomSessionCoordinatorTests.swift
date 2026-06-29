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

    func testMediaQualityReportUsesExistingEventAndBrowserCompatiblePayload() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(
            answerSDP: "answer",
            mediaQualitySnapshots: [
                NativeMediaQualitySnapshot(
                    at: 1_000,
                    outboundAudioBytesSent: 1_200,
                    outboundAudioPacketsSent: 12,
                    outboundVideoBytesSent: 9_000,
                    outboundVideoFramesSent: 90,
                    inboundAudioPacketsReceived: 80,
                    inboundVideoPacketsReceived: 160,
                    outboundRtt: 0.08,
                    candidatePair: NativeMediaQualityCandidatePair(
                        protocol: "udp",
                        networkType: "wifi",
                        localCandidateType: "host",
                        remoteCandidateType: "relay",
                        currentRoundTripTime: 0.08
                    )
                )
            ]
        )
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        _ = try await coordinator.joinAudioOnly(name: "Tom", password: "B0NFIRE!")
        try await coordinator.sendMediaQualityReport()

        XCTAssertEqual(signaling.sent.map(\.event), [
            ClientSignalEvent.participant,
            ClientSignalEvent.mediaReady,
            ClientSignalEvent.answer,
            ClientSignalEvent.participantMediaState,
            ClientSignalEvent.mediaQuality
        ])
        let payload = try decodeSentPayload(MediaQualityAssertionPayload.self, from: signaling.sent[4].data)
        XCTAssertEqual(payload.client.platform, "ios")
        XCTAssertEqual(payload.client.version, "test")
        XCTAssertEqual(payload.browser.platform, "ios")
        XCTAssertFalse(payload.browser.safari)
        XCTAssertEqual(payload.audio.mode, "native")
        XCTAssertEqual(payload.audio.processor, "avfoundation")
        XCTAssertEqual(payload.audio.outputSettings.readyState, "live")
        XCTAssertTrue(payload.audio.outputSettings.enabled)
        XCTAssertFalse(payload.video.settings.enabled)
        XCTAssertEqual(payload.stats.outboundRtt, 0.08)
        XCTAssertEqual(payload.stats.candidatePair.localCandidateType, "host")
        XCTAssertNil(payload.deltas.elapsedMs)
    }

    func testMediaQualitySnapshotFailureReportsNativeMediaError() async throws {
        let signaling = MockSignalingTransport(envelopes: [])
        let rtc = MockRoomRTCClient(answerSDP: "answer", mediaQualityError: RoomRTCError.peerConnectionNotConfigured)
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        await coordinator.setCameraOff(true)

        do {
            try await coordinator.sendMediaQualityReport()
            XCTFail("media quality snapshot failure should rethrow")
        } catch RoomRTCError.peerConnectionNotConfigured {
        } catch {
            XCTFail("unexpected error: \(error)")
        }

        XCTAssertEqual(signaling.sent.map(\.event), [ClientSignalEvent.mediaError])
        let payload = try decodeSentPayload(MediaErrorAssertionPayload.self, from: signaling.sent[0].data)
        XCTAssertEqual(payload.stage, "media_quality_snapshot")
        XCTAssertEqual(payload.client.platform, "ios")
        XCTAssertEqual(payload.browser.platform, "ios")
        XCTAssertEqual(payload.audio.mode, "native")
        XCTAssertEqual(payload.audio.processor, "avfoundation")
        XCTAssertFalse(payload.video.settings.enabled)
        XCTAssertTrue(payload.error.name.contains("RoomRTCError"))
        XCTAssertTrue(payload.error.message.contains("peerConnectionNotConfigured"))
    }

    func testCaptureMediaEvidenceSnapshotDerivesProofFromNativeStats() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(
            answerSDP: "answer",
            mediaQualitySnapshots: [
                NativeMediaQualitySnapshot(
                    at: 1_000,
                    outboundAudioBytesSent: 1_200,
                    outboundAudioPacketsSent: 12,
                    outboundVideoBytesSent: 9_000,
                    outboundVideoFramesEncoded: 95,
                    outboundVideoFramesSent: 90,
                    inboundAudioPacketsReceived: 80,
                    inboundVideoPacketsReceived: 160,
                    inboundVideoDecoded: 140,
                    outboundRtt: 0.08,
                    candidatePair: NativeMediaQualityCandidatePair(
                        protocol: "udp",
                        networkType: "wifi",
                        localCandidateType: "host",
                        remoteCandidateType: "relay",
                        currentRoundTripTime: 0.08
                    )
                )
            ]
        )
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test"),
            mediaEvidenceContextProvider: {
                nativeEvidenceTestContext()
            }
        )

        _ = try await coordinator.joinWithCamera(name: "Tom", password: "B0NFIRE!")
        await coordinator.setMuted(true)
        await coordinator.setCameraOff(true)
        let remoteTrack = NativeRemoteVideoTrack(id: "remote-video-1", streamIds: ["stream-1"])
        remoteTrack.recordRenderedFrame(width: 1280, height: 720)
        await rtc.emitRemoteVideoTrack(remoteTrack)
        let evidence = try await coordinator.captureMediaEvidenceSnapshot()

        XCTAssertEqual(evidence.artifactType, "native_device_media")
        XCTAssertEqual(evidence.claimScope, "qa_snapshot")
        XCTAssertFalse(evidence.releaseEligible)
        XCTAssertEqual(evidence.status, "observed")
        XCTAssertEqual(evidence.releaseEvidenceSummary.status, "pending")
        XCTAssertEqual(evidence.client.platform, "ios")
        XCTAssertEqual(evidence.app.version, "1.0")
        XCTAssertEqual(evidence.app.build, "15")
        XCTAssertEqual(evidence.app.target, "MeetingAssistAppleApp")
        XCTAssertEqual(evidence.device.kind, "iphone")
        XCTAssertEqual(evidence.device.model, "iPhone physical")
        XCTAssertEqual(evidence.device.os, "iOS 26.5")
        XCTAssertTrue(evidence.device.physical)
        XCTAssertEqual(evidence.releaseEvidenceSummary.device, "iPhone physical")
        XCTAssertEqual(evidence.releaseEvidenceSummary.os, "iOS 26.5")
        XCTAssertEqual(evidence.runId, "native-apple-run-test")
        XCTAssertEqual(evidence.roomId, "release-room-test")
        XCTAssertEqual(evidence.releaseEvidenceSummary.runId, "native-apple-run-test")
        XCTAssertEqual(evidence.releaseEvidenceSummary.roomId, "release-room-test")
        XCTAssertEqual(evidence.lifecycle, .connected)
        XCTAssertEqual(evidence.remoteVideoTiles, 1)
        XCTAssertEqual(evidence.renderer.remoteVideoFramesRendered, 1)
        XCTAssertEqual(evidence.renderer.latestFrameWidth, 1280)
        XCTAssertEqual(evidence.renderer.latestFrameHeight, 720)
        XCTAssertTrue(evidence.mediaAssertions.microphonePublished)
        XCTAssertTrue(evidence.mediaAssertions.cameraPublished)
        XCTAssertTrue(evidence.mediaAssertions.remoteAudioReceived)
        XCTAssertTrue(evidence.mediaAssertions.remoteVideoRendered)
        XCTAssertEqual(evidence.assertionEvidence.cameraPublished.value, 90)
        XCTAssertEqual(evidence.assertionEvidence.remoteVideoRendered.source, "nativeRemoteVideoRenderer+inboundVideoDecoded")
        XCTAssertTrue(evidence.selectedCandidate.relayCandidateSelected)

        let encoded = String(data: try JSONEncoder().encode(evidence), encoding: .utf8) ?? ""
        for leaked in disallowedMediaEvidenceLeakTokens {
            XCTAssertFalse(encoded.localizedCaseInsensitiveContains(leaked), "leaked sensitive media evidence detail: \(leaked)")
        }
    }

    func testCaptureTurnRelayObservationExportsSanitizedRestrictiveNetworkProof() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(
            answerSDP: "answer",
            mediaQualitySnapshots: [
                NativeMediaQualitySnapshot(
                    at: 1_000,
                    outboundAudioPacketsSent: 12,
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
                )
            ]
        )
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(
                clientRTCConfig: ClientRTCConfig(
                    rtcConfiguration: [
                        "iceServers": .array([
                            .object(["urls": .string("stun:stun.example.com:19302")]),
                            .object([
                                "urls": .string("turns:relay.example.com:5349?transport=tcp"),
                                "username": .string("alice"),
                                "credential": .string("secret")
                            ])
                        ])
                    ],
                    protocolVersion: meetingAssistNativeProtocolV1,
                    auth: "cookie",
                    websocketPath: "/websocket",
                    signalingRole: "server-offer",
                    supportedLayers: ["low", "medium", "high"],
                    nativeHints: nil
                )
            ),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test"),
            mediaEvidenceContextProvider: {
                nativeEvidenceTestContext()
            }
        )

        _ = try await coordinator.joinWithCamera(name: "Tom", password: "B0NFIRE!")
        let observation = try await coordinator.captureTurnRelayObservation(network: " restricted guest network ")

        XCTAssertEqual(observation.artifactType, "native_turn_relay_observation")
        XCTAssertEqual(observation.status, "observed")
        XCTAssertEqual(observation.runId, "native-apple-run-test")
        XCTAssertEqual(observation.roomId, "release-room-test")
        XCTAssertEqual(observation.network, "restricted guest network")
        XCTAssertEqual(observation.selectedCandidate.relayProtocol, "turns")
        XCTAssertEqual(observation.selectedCandidate.relayCandidateType, "relay")
        XCTAssertTrue(observation.selectedCandidate.relayCandidateSelected)
        XCTAssertEqual(observation.selectedCandidate.remoteCandidateType, "relay")
        XCTAssertEqual(observation.selectedCandidate.currentRoundTripTime, 0.08)
        XCTAssertEqual(observation.iceReadiness.turnsCount, 1)
        XCTAssertEqual(observation.iceReadiness.turnServersWithCredentials, 1)
        XCTAssertEqual(observation.iceReadiness.relayTransports, ["tcp"])

        let encoded = String(data: try JSONEncoder().encode(observation), encoding: .utf8) ?? ""
        for leaked in disallowedMediaEvidenceLeakTokens {
            XCTAssertFalse(encoded.localizedCaseInsensitiveContains(leaked), "leaked sensitive TURN observation detail: \(leaked)")
        }
        XCTAssertFalse(encoded.localizedCaseInsensitiveContains("relay.example.com"))
        XCTAssertFalse(encoded.localizedCaseInsensitiveContains("stun.example.com"))
        XCTAssertFalse(encoded.localizedCaseInsensitiveContains("alice"))
        XCTAssertFalse(encoded.localizedCaseInsensitiveContains("secret"))
    }

    func testMediaQualityReportPublishesMediaEvidenceHandler() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom"),
            WebSocketEnvelope(
                event: ServerSignalEvent.offer,
                data: encodedJSONString(RTCSessionDescriptionPayload(type: "offer", sdp: "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\n"))
            )
        ])
        let rtc = MockRoomRTCClient(
            answerSDP: "answer",
            mediaQualitySnapshots: [
                NativeMediaQualitySnapshot(
                    at: 1_000,
                    outboundAudioPacketsSent: 12,
                    outboundVideoFramesSent: 90,
                    inboundAudioPacketsReceived: 80,
                    inboundVideoDecoded: 140
                )
            ]
        )
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test"),
            mediaEvidenceContextProvider: {
                nativeEvidenceTestContext()
            }
        )
        let collector = MediaEvidenceCollector()
        await coordinator.setMediaEvidenceHandler { evidence in
            await collector.append(evidence)
        }

        _ = try await coordinator.joinWithCamera(name: "Tom", password: "B0NFIRE!")
        let remoteTrack = NativeRemoteVideoTrack(id: "remote-video-1", streamIds: ["stream-1"])
        remoteTrack.recordRenderedFrame(width: 1280, height: 720)
        await rtc.emitRemoteVideoTrack(remoteTrack)
        try await coordinator.sendMediaQualityReport()

        let values = await collector.values()
        XCTAssertEqual(values.count, 1)
        XCTAssertEqual(values.first?.status, "observed")
        XCTAssertEqual(values.first?.app.version, "1.0")
        XCTAssertEqual(values.first?.app.build, "15")
        XCTAssertEqual(values.first?.app.target, "MeetingAssistAppleApp")
        XCTAssertEqual(values.first?.device.kind, "iphone")
        XCTAssertEqual(values.first?.device.model, "iPhone physical")
        XCTAssertEqual(values.first?.device.os, "iOS 26.5")
        XCTAssertEqual(values.first?.device.physical, true)
        XCTAssertEqual(values.first?.runId, "native-apple-run-test")
        XCTAssertEqual(values.first?.roomId, "release-room-test")
        XCTAssertEqual(values.first?.mediaAssertions.cameraPublished, true)
        XCTAssertEqual(values.first?.mediaAssertions.remoteVideoRendered, true)
    }

    func testNativeMediaErrorRedactsNetworkSecretsFromMessage() async throws {
        let rawMessage = """
        ICE failed candidate:842163049 1 udp 1677729535 192.168.1.25 56143 typ host raddr 10.0.0.2 rport 60000;
        retry url turn:alice:secret@203.0.113.55:3478?transport=tcp;
        relay 198.51.100.24:5349;
        remote [2001:db8::1]:3478
        """
        let signaling = MockSignalingTransport(envelopes: [])
        let rtc = MockRoomRTCClient(
            answerSDP: "answer",
            mediaQualityError: RedactionProbeError(description: rawMessage)
        )
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        do {
            try await coordinator.sendMediaQualityReport()
            XCTFail("media quality snapshot failure should rethrow")
        } catch is RedactionProbeError {
        } catch {
            XCTFail("unexpected error: \(error)")
        }

        XCTAssertEqual(signaling.sent.map(\.event), [ClientSignalEvent.mediaError])
        let payload = try decodeSentPayload(MediaErrorAssertionPayload.self, from: signaling.sent[0].data)
        XCTAssertEqual(payload.stage, "media_quality_snapshot")
        XCTAssertTrue(payload.error.message.contains("candidate:<redacted>"))
        XCTAssertTrue(payload.error.message.contains("turn:<redacted>"))
        XCTAssertTrue(payload.error.message.contains("<redacted-ip>"))
        XCTAssertLessThanOrEqual(payload.error.message.count, 220)
        for leaked in [
            "candidate:842163049",
            "192.168.1.25",
            "10.0.0.2",
            "alice:secret",
            "203.0.113.55",
            "198.51.100.24",
            "2001:db8::1"
        ] {
            XCTAssertFalse(payload.error.message.contains(leaked), "leaked sensitive media diagnostic detail: \(leaked)")
        }
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
        await coordinator.setCameraOff(false)

        do {
            try await coordinator.setScreenSharing(true)
            XCTFail("permission denial should fail before announcing screen share")
        } catch RoomRTCError.screenCapturePermissionDenied {
        } catch {
            XCTFail("unexpected error: \(error)")
        }

        XCTAssertEqual(signaling.sent.map(\.event), [ClientSignalEvent.mediaError])
        let payload = try decodeSentPayload(MediaErrorAssertionPayload.self, from: signaling.sent[0].data)
        XCTAssertEqual(payload.stage, "screen_share_start")
        XCTAssertEqual(payload.client.platform, "macos")
        XCTAssertEqual(payload.audio.mode, "native")
        XCTAssertEqual(payload.video.settings.enabled, true)
        XCTAssertTrue(payload.error.name.contains("RoomRTCError"))
        XCTAssertEqual(rtc.screenShareEnabledChanges, [true])
    }

    func testPrepareLocalMediaFailureReportsNativeMediaErrorBeforeRethrow() async throws {
        let signaling = MockSignalingTransport(envelopes: [
            accessGrantedEnvelope(name: "Tom")
        ])
        let rtc = MockRoomRTCClient(
            answerSDP: "answer",
            prepareLocalMediaError: RoomRTCError.cameraUnavailable
        )
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: signaling,
            rtc: rtc,
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )

        do {
            _ = try await coordinator.joinWithCamera(name: "Tom", password: "B0NFIRE!")
            XCTFail("camera failure should stop join")
        } catch RoomRTCError.cameraUnavailable {
        } catch {
            XCTFail("unexpected error: \(error)")
        }

        XCTAssertEqual(signaling.sent.map(\.event), [
            ClientSignalEvent.participant,
            ClientSignalEvent.mediaError
        ])
        let payload = try decodeSentPayload(MediaErrorAssertionPayload.self, from: signaling.sent[1].data)
        XCTAssertEqual(payload.stage, "prepare_camera_media")
        XCTAssertEqual(payload.client.platform, "ios")
        XCTAssertTrue(payload.video.settings.enabled)
        XCTAssertTrue(payload.error.message.contains("cameraUnavailable"))
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

    func testMediaDisconnectedEventNotifiesRecoveryHandler() async throws {
        let coordinator = NativeRoomSessionCoordinator(
            api: MockNativeRoomAPI(),
            signaling: MockSignalingTransport(envelopes: []),
            rtc: MockRoomRTCClient(answerSDP: "answer"),
            clientIdentity: NativeRoomClientIdentity(platform: "ios", version: "test")
        )
        let recovery = MediaRecoveryCollector()
        await coordinator.setMediaRecoveryHandler { event in
            await recovery.append(event)
        }

        try await coordinator.handleServerEvent(
            kanbanEnvelope(event: "media_disconnected", data: .string("media negotiation stalled; rejoin the room."))
        )

        let events = await recovery.values()
        XCTAssertEqual(
            events,
            [
                NativeMediaRecoveryEvent(
                    stage: "media_disconnected",
                    message: "media negotiation stalled; rejoin the room.",
                    terminal: true
                )
            ]
        )
        let lifecycle = await coordinator.lifecycle
        XCTAssertEqual(lifecycle, .reconnecting)
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
    private let clientRTCConfig: ClientRTCConfig

    init(clientRTCConfig: ClientRTCConfig = MockNativeRoomAPI.defaultClientRTCConfig) {
        self.clientRTCConfig = clientRTCConfig
    }

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
        return clientRTCConfig
    }

    private static let defaultClientRTCConfig = ClientRTCConfig(
        rtcConfiguration: ["iceServers": .array([])],
        protocolVersion: meetingAssistNativeProtocolV1,
        auth: "cookie",
        websocketPath: "/websocket",
        signalingRole: "server-offer",
        supportedLayers: ["low", "medium", "high"],
        nativeHints: nil
    )
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
    private var mediaQualitySnapshots: [NativeMediaQualitySnapshot]
    private let answerSDP: String
    private let prepareLocalMediaError: Error?
    private let handleOfferError: Error?
    private let remoteCandidateError: Error?
    private let mediaQualityError: Error?
    private let screenShareError: Error?
    private let onPrepareLocalMedia: (@Sendable () -> Void)?

    init(
        answerSDP: String,
        mediaQualitySnapshots: [NativeMediaQualitySnapshot] = [],
        prepareLocalMediaError: Error? = nil,
        handleOfferError: Error? = nil,
        remoteCandidateError: Error? = nil,
        mediaQualityError: Error? = nil,
        screenShareError: Error? = nil,
        onPrepareLocalMedia: (@Sendable () -> Void)? = nil
    ) {
        self.answerSDP = answerSDP
        self.mediaQualitySnapshots = mediaQualitySnapshots
        self.prepareLocalMediaError = prepareLocalMediaError
        self.handleOfferError = handleOfferError
        self.remoteCandidateError = remoteCandidateError
        self.mediaQualityError = mediaQualityError
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
        if let prepareLocalMediaError {
            throw prepareLocalMediaError
        }
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
        if let handleOfferError {
            throw handleOfferError
        }
        lifecycle = .negotiating
        return answerSDP
    }

    func addRemoteCandidate(_ json: String) async throws {
        if let remoteCandidateError {
            throw remoteCandidateError
        }
        remoteCandidates.append(json)
    }

    func restartICE() async {
        didRestartICE = true
        lifecycle = .reconnecting
    }

    func mediaQualitySnapshot() async throws -> NativeMediaQualitySnapshot {
        if let mediaQualityError {
            throw mediaQualityError
        }
        guard !mediaQualitySnapshots.isEmpty else {
            return NativeMediaQualitySnapshot()
        }
        return mediaQualitySnapshots.removeFirst()
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

private struct MediaQualityAssertionPayload: Decodable {
    var client: NativeRoomClientIdentity
    var browser: MediaQualityBrowserAssertionPayload
    var audio: MediaQualityAudioAssertionPayload
    var video: MediaQualityVideoAssertionPayload
    var stats: NativeMediaQualitySnapshot
    var deltas: NativeMediaQualityDeltas
}

private struct MediaErrorAssertionPayload: Decodable {
    var stage: String
    var client: NativeRoomClientIdentity
    var browser: MediaQualityBrowserAssertionPayload
    var audio: MediaErrorAudioAssertionPayload
    var video: MediaQualityVideoAssertionPayload
    var error: MediaErrorDetailAssertionPayload
}

private struct MediaQualityBrowserAssertionPayload: Decodable {
    var safari: Bool
    var platform: String
}

private struct MediaQualityAudioAssertionPayload: Decodable {
    var mode: String
    var processor: String
    var outputSettings: MediaQualityTrackSettingsAssertionPayload
}

private struct MediaErrorAudioAssertionPayload: Decodable {
    var mode: String
    var processor: String
}

private struct MediaQualityVideoAssertionPayload: Decodable {
    var settings: MediaQualityTrackSettingsAssertionPayload
}

private struct MediaQualityTrackSettingsAssertionPayload: Decodable {
    var enabled: Bool
    var readyState: String
}

private struct MediaErrorDetailAssertionPayload: Decodable {
    var name: String
    var message: String
    var constraint: String
    var attempts: [String]
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

private struct RedactionProbeError: Error, CustomStringConvertible {
    var description: String
}

private let disallowedMediaEvidenceLeakTokens = [
    "candidate:842163049",
    "a=candidate",
    "v=0",
    "a=ice-ufrag",
    "turn:alice",
    "turns:relay",
    "alice:secret",
    "192.168.",
    "10.0.0.",
    "203.0.113.",
    "2001:db8",
    "Cookie:",
    "Authorization:",
    "Bearer ",
    "sk-proj-",
    "ABCDE12345",
    "BEGIN PRIVATE KEY",
    ".mobileprovision",
    "localCandidateId",
    "remoteCandidateId",
]

private func nativeEvidenceTestContext() -> NativeMediaEvidenceCaptureContext {
    NativeMediaEvidenceCaptureContext(
        app: NativeMediaEvidenceAppContext(
            version: "1.0",
            build: "15",
            target: "MeetingAssistAppleApp",
            clientPlatform: "ios",
            clientVersion: "test"
        ),
        device: NativeMediaEvidenceDeviceContext(
            kind: "iphone",
            model: "iPhone physical",
            os: "iOS 26.5",
            physical: true
        ),
        runId: "native-apple-run-test",
        roomId: "release-room-test"
    )
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

private actor MediaRecoveryCollector {
    private var storedValues: [NativeMediaRecoveryEvent] = []

    func append(_ value: NativeMediaRecoveryEvent) {
        storedValues.append(value)
    }

    func values() -> [NativeMediaRecoveryEvent] {
        storedValues
    }
}

private actor MediaEvidenceCollector {
    private var storedValues: [NativeMediaEvidenceSnapshot] = []

    func append(_ value: NativeMediaEvidenceSnapshot) {
        storedValues.append(value)
    }

    func values() -> [NativeMediaEvidenceSnapshot] {
        storedValues
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
