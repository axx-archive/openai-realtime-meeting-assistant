import XCTest
@testable import MeetingAssistCore
@testable import MeetingAssistRoomRTC

final class NativeRoomRTCClientTests: XCTestCase {
    private static let disallowedEvidenceLeakTokens = [
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

    func testWebRTCBinaryIsImportable() {
        XCTAssertTrue(WebRTCLinkStatus.isWebRTCImportable)
    }

    func testICEServerDescriptorParsesTurnCredentialsAndMultipleURLs() {
        let descriptors = NativeICEServerDescriptor.parse(from: [
            "iceServers": .array([
                .object(["urls": .string(" stun:stun.example.com:3478 ")]),
                .object([
                    "urls": .array([
                        .string("turn:turn.example.com:3478?transport=udp"),
                        .string("turns:turn.example.com:5349?transport=tcp")
                    ]),
                    "username": .string(" native "),
                    "credential": .string(" secret "),
                    "credentialType": .string("password")
                ])
            ])
        ])

        XCTAssertEqual(descriptors, [
            NativeICEServerDescriptor(
                urls: ["stun:stun.example.com:3478"],
                username: nil,
                credential: nil
            ),
            NativeICEServerDescriptor(
                urls: [
                    "turn:turn.example.com:3478?transport=udp",
                    "turns:turn.example.com:5349?transport=tcp"
                ],
                username: "native",
                credential: "secret"
            )
        ])
        XCTAssertFalse(descriptors[0].isTurnRelay)
        XCTAssertTrue(descriptors[1].isTurnRelay)
    }

    func testICEServerDescriptorSkipsMalformedAndBlankURLServers() {
        let descriptors = NativeICEServerDescriptor.parse(from: [
            "iceServers": .array([
                .object(["urls": .array([.string(" "), .number(7)])]),
                .string("bad-server"),
                .object([
                    "urls": .array([
                        .string("turn:relay.example.com:3478"),
                        .string("")
                    ])
                ])
            ])
        ])

        XCTAssertEqual(descriptors, [
            NativeICEServerDescriptor(
                urls: ["turn:relay.example.com:3478"],
                username: nil,
                credential: nil
            )
        ])
        XCTAssertTrue(descriptors[0].isTurnRelay)
    }

    func testConfigureAndPrepareAudioOnlyCreatesNativePeerConnection() async throws {
        let client = NativeRoomRTCClient()

        try await client.configure(testClientConfig)
        try await client.prepareLocalMedia(audio: true, video: false)

        XCTAssertEqual(client.lifecycle, .preparingMedia)

        await client.leave()
        XCTAssertEqual(client.lifecycle, .leaving)
    }

    func testMediaQualitySnapshotAggregatesBrowserCompatibleStats() {
        let snapshot = NativeRoomRTCClient.mediaQualitySnapshot(from: [
            stat(
                id: "audio-out",
                type: "outbound-rtp",
                values: [
                    "kind": .string("audio"),
                    "bytesSent": .number(12_000),
                    "packetsSent": .number(60)
                ]
            ),
            stat(
                id: "video-out",
                type: "outbound-rtp",
                timestampUs: 4_000_000,
                values: [
                    "kind": .string("video"),
                    "bytesSent": .number(90_000),
                    "framesEncoded": .number(300),
                    "framesSent": .number(280)
                ]
            ),
            stat(
                id: "audio-in",
                type: "inbound-rtp",
                values: [
                    "kind": .string("audio"),
                    "jitter": .number(0.004),
                    "packetsLost": .number(1),
                    "packetsReceived": .number(500)
                ]
            ),
            stat(
                id: "video-in",
                type: "inbound-rtp",
                values: [
                    "kind": .string("video"),
                    "jitter": .number(0.018),
                    "packetsLost": .number(2),
                    "packetsReceived": .number(1_000),
                    "framesDropped": .number(3),
                    "framesDecoded": .number(250)
                ]
            )
        ])

        XCTAssertEqual(snapshot.at, 4_000)
        XCTAssertEqual(snapshot.outboundAudioBytesSent, 12_000)
        XCTAssertEqual(snapshot.outboundAudioPacketsSent, 60)
        XCTAssertEqual(snapshot.outboundVideoBytesSent, 90_000)
        XCTAssertEqual(snapshot.outboundVideoFramesEncoded, 300)
        XCTAssertEqual(snapshot.outboundVideoFramesSent, 280)
        XCTAssertEqual(snapshot.inboundAudioJitter, 0.004)
        XCTAssertEqual(snapshot.inboundAudioLost, 1)
        XCTAssertEqual(snapshot.inboundAudioPacketsReceived, 500)
        XCTAssertEqual(snapshot.inboundVideoJitter, 0.018)
        XCTAssertEqual(snapshot.inboundVideoLost, 2)
        XCTAssertEqual(snapshot.inboundVideoPacketsReceived, 1_000)
        XCTAssertEqual(snapshot.inboundVideoDrops, 3)
        XCTAssertEqual(snapshot.inboundVideoDecoded, 250)
    }

    func testMediaQualitySnapshotResolvesSelectedCandidatePair() {
        let snapshot = NativeRoomRTCClient.mediaQualitySnapshot(from: [
            stat(
                id: "transport",
                type: "transport",
                values: ["selectedCandidatePairId": .string("pair-selected")]
            ),
            stat(
                id: "pair-selected",
                type: "candidate-pair",
                values: [
                    "localCandidateId": .string("local-wifi"),
                    "remoteCandidateId": .string("remote-relay"),
                    "availableOutgoingBitrate": .number(850_000),
                    "currentRoundTripTime": .number(0.082)
                ]
            ),
            stat(
                id: "local-wifi",
                type: "local-candidate",
                values: [
                    "protocol": .string("udp"),
                    "networkType": .string("wifi"),
                    "candidateType": .string("host")
                ]
            ),
            stat(
                id: "remote-relay",
                type: "remote-candidate",
                values: ["candidateType": .string("relay")]
            )
        ])

        XCTAssertEqual(
            snapshot.candidatePair,
            NativeMediaQualityCandidatePair(
                protocol: "udp",
                networkType: "wifi",
                localCandidateType: "host",
                remoteCandidateType: "relay",
                availableOutgoingBitrate: 850_000,
                currentRoundTripTime: 0.082
            )
        )
        XCTAssertEqual(snapshot.outboundRtt, 0.082)
    }

    func testNativeMediaEvidenceSnapshotDerivesAssertionsFromStatsAndRendererObservation() throws {
        let source = NativeMediaQualitySnapshot(
            at: 4_000,
            outboundAudioBytesSent: 12_000,
            outboundAudioPacketsSent: 60,
            outboundVideoBytesSent: 90_000,
            outboundVideoFramesEncoded: 300,
            outboundVideoFramesSent: 280,
            inboundAudioPacketsReceived: 500,
            inboundVideoPacketsReceived: 1_000,
            inboundVideoDecoded: 250,
            outboundRtt: 0.082,
            candidatePair: NativeMediaQualityCandidatePair(
                protocol: "udp",
                networkType: "wifi",
                localCandidateType: "host",
                remoteCandidateType: "relay",
                currentRoundTripTime: 0.082
            )
        )

        let evidence = NativeMediaEvidenceSnapshot(
            source: source,
            capturedAt: "2026-06-29T17:00:00Z",
            client: NativeMediaEvidenceClient(platform: "ios", version: "test"),
            lifecycle: .connected,
            remoteVideoTiles: 1,
            renderer: Self.rendererEvidence()
        )

        XCTAssertEqual(evidence.schemaVersion, 1)
        XCTAssertEqual(evidence.artifactType, "native_device_media")
        XCTAssertEqual(evidence.claimScope, "qa_snapshot")
        XCTAssertFalse(evidence.releaseEligible)
        XCTAssertEqual(evidence.status, "observed")
        XCTAssertEqual(evidence.releaseEvidenceSummary.status, "pending")
        XCTAssertTrue(evidence.mediaAssertions.microphonePublished)
        XCTAssertTrue(evidence.mediaAssertions.cameraPublished)
        XCTAssertTrue(evidence.mediaAssertions.remoteAudioReceived)
        XCTAssertTrue(evidence.mediaAssertions.remoteVideoRendered)
        XCTAssertEqual(evidence.renderer.remoteVideoFramesRendered, 3)
        XCTAssertEqual(evidence.renderer.observedRemoteVideoTracks, 1)
        XCTAssertFalse(evidence.renderer.capturesPixels)
        XCTAssertTrue(evidence.selectedCandidate.relayCandidateSelected)
        XCTAssertEqual(evidence.counters.outboundVideoFramesSent, 280)
        XCTAssertEqual(evidence.stats.observationWindow, "cumulative_peer_connection_stats")
        XCTAssertEqual(evidence.assertionEvidence.cameraPublished.source, "outboundVideoFramesSent")
        XCTAssertEqual(evidence.assertionEvidence.remoteVideoRendered.source, "nativeRemoteVideoRenderer+inboundVideoDecoded")
        XCTAssertTrue(evidence.limitations.contains("Do not mark ReleaseEvidence physicalDeviceMedia as passed from a qa_snapshot artifact."))
        XCTAssertEqual(evidence.client.platform, "ios")

        let encoded = String(data: try JSONEncoder().encode(evidence), encoding: .utf8) ?? ""
        for leaked in Self.disallowedEvidenceLeakTokens {
            XCTAssertFalse(encoded.localizedCaseInsensitiveContains(leaked), "leaked sensitive media evidence detail: \(leaked)")
        }
    }

    func testNativeICEReadinessSummaryRedactsTURNConfigToCounts() throws {
        let readiness = NativeICEReadinessSummary(rtcConfiguration: [
            "iceServers": .array([
                .object(["urls": .string("stun:stun.example.com:19302")]),
                .object([
                    "urls": .array([
                        .string("turns:relay.example.com:5349?transport=tcp"),
                    ]),
                    "username": .string("alice"),
                    "credential": .string("secret"),
                ]),
            ]),
        ])

        XCTAssertTrue(readiness.ok)
        XCTAssertTrue(readiness.hasIceServers)
        XCTAssertEqual(readiness.iceServerCount, 2)
        XCTAssertEqual(readiness.knownUrlCount, 2)
        XCTAssertEqual(readiness.stunCount, 1)
        XCTAssertEqual(readiness.turnsCount, 1)
        XCTAssertEqual(readiness.turnServersWithCredentials, 1)
        XCTAssertEqual(readiness.turnServersMissingCredentials, 0)
        XCTAssertEqual(readiness.relayTransports, ["tcp"])

        let encoded = String(data: try JSONEncoder().encode(readiness), encoding: .utf8) ?? ""
        for leaked in Self.disallowedEvidenceLeakTokens {
            XCTAssertFalse(encoded.localizedCaseInsensitiveContains(leaked), "leaked sensitive ICE readiness detail: \(leaked)")
        }
    }

    func testNativeICEReadinessSummaryReportsWarningsAndErrors() {
        let readiness = NativeICEReadinessSummary(rtcConfiguration: [
            "iceServers": .array([
                .object(["urls": .array([])]),
                .object(["urls": .string("relay.example.com")]),
                .object(["urls": .string("turn:relay.example.com:3478")]),
            ]),
        ])

        XCTAssertFalse(readiness.ok)
        XCTAssertEqual(readiness.iceServerCount, 2)
        XCTAssertEqual(readiness.unknownUrlCount, 1)
        XCTAssertEqual(readiness.turnCount, 1)
        XCTAssertEqual(readiness.turnServersWithCredentials, 0)
        XCTAssertEqual(readiness.turnServersMissingCredentials, 1)
        XCTAssertTrue(readiness.warnings.contains { $0.contains("malformed") })
        XCTAssertTrue(readiness.warnings.contains { $0.contains("unknown scheme") })
        XCTAssertTrue(readiness.errors.contains { $0.contains("none have both username and credential") })
    }

    func testNativeTurnRelayObservationMatchesPromoterInputShape() throws {
        let evidence = Self.turnRelayEvidence(
            candidatePair: NativeMediaQualityCandidatePair(
                protocol: "udp",
                networkType: "wifi",
                localCandidateType: "host",
                remoteCandidateType: "relay",
                currentRoundTripTime: 0.082
            )
        )
        let readiness = NativeICEReadinessSummary(rtcConfiguration: [
            "iceServers": .array([
                .object([
                    "urls": .string("turns:relay.example.com:5349?transport=tcp"),
                    "username": .string("alice"),
                    "credential": .string("secret"),
                ]),
            ]),
        ])

        let observation = try NativeTurnRelayObservation(
            evidence: evidence,
            iceReadiness: readiness,
            network: "restricted guest network"
        )

        XCTAssertEqual(observation.schemaVersion, 1)
        XCTAssertEqual(observation.artifactType, "native_turn_relay_observation")
        XCTAssertEqual(observation.status, "observed")
        XCTAssertEqual(observation.runId, "native-apple-run-test")
        XCTAssertEqual(observation.roomId, "release-room-test")
        XCTAssertEqual(observation.network, "restricted guest network")
        XCTAssertEqual(observation.app.version, "1.0")
        XCTAssertEqual(observation.device.kind, "iphone")
        XCTAssertTrue(observation.device.physical)
        XCTAssertEqual(observation.selectedCandidate.relayProtocol, "turns")
        XCTAssertEqual(observation.selectedCandidate.relayCandidateType, "relay")
        XCTAssertTrue(observation.selectedCandidate.relayCandidateSelected)
        XCTAssertEqual(observation.selectedCandidate.remoteCandidateType, "relay")
        XCTAssertEqual(observation.selectedCandidate.currentRoundTripTime, 0.082)
        XCTAssertTrue(observation.iceReadiness.ok)

        let encoded = String(data: try JSONEncoder().encode(observation), encoding: .utf8) ?? ""
        XCTAssertTrue(encoded.contains("\"native_turn_relay_observation\""))
        for leaked in Self.disallowedEvidenceLeakTokens {
            XCTAssertFalse(encoded.localizedCaseInsensitiveContains(leaked), "leaked sensitive TURN observation detail: \(leaked)")
        }
    }

    func testNativeTurnRelayObservationRejectsUnsafeOrAmbiguousInputs() {
        let relayEvidence = Self.turnRelayEvidence(
            candidatePair: NativeMediaQualityCandidatePair(
                localCandidateType: "relay",
                currentRoundTripTime: 0.082
            )
        )
        let cleanTurn = NativeICEReadinessSummary(rtcConfiguration: [
            "iceServers": .array([
                .object([
                    "urls": .string("turn:relay.example.com:3478"),
                    "username": .string("alice"),
                    "credential": .string("secret"),
                ]),
            ]),
        ])

        XCTAssertThrowsError(try NativeTurnRelayObservation(evidence: relayEvidence, iceReadiness: cleanTurn, network: "")) { error in
            XCTAssertEqual(error as? NativeTurnRelayObservationError, .missingNetwork)
        }

        let nonRelayEvidence = Self.turnRelayEvidence(candidatePair: NativeMediaQualityCandidatePair(localCandidateType: "host", remoteCandidateType: "srflx", currentRoundTripTime: 0.082))
        XCTAssertThrowsError(try NativeTurnRelayObservation(evidence: nonRelayEvidence, iceReadiness: cleanTurn, network: "restricted")) { error in
            XCTAssertEqual(error as? NativeTurnRelayObservationError, .nonRelaySelectedCandidate)
        }

        let zeroRttEvidence = Self.turnRelayEvidence(candidatePair: NativeMediaQualityCandidatePair(localCandidateType: "relay", currentRoundTripTime: 0))
        XCTAssertThrowsError(try NativeTurnRelayObservation(evidence: zeroRttEvidence, iceReadiness: cleanTurn, network: "restricted")) { error in
            XCTAssertEqual(error as? NativeTurnRelayObservationError, .invalidRoundTripTime)
        }

        let ambiguousReadiness = NativeICEReadinessSummary(rtcConfiguration: [
            "iceServers": .array([
                .object([
                    "urls": .array([
                        .string("turn:relay.example.com:3478"),
                        .string("turns:relay.example.com:5349"),
                    ]),
                    "username": .string("alice"),
                    "credential": .string("secret"),
                ]),
            ]),
        ])
        XCTAssertThrowsError(try NativeTurnRelayObservation(evidence: relayEvidence, iceReadiness: ambiguousReadiness, network: "restricted")) { error in
            XCTAssertEqual(error as? NativeTurnRelayObservationError, .ambiguousRelayProtocol)
        }

        let uncleanReadiness = NativeICEReadinessSummary(rtcConfiguration: [
            "iceServers": .array([
                .object(["urls": .string("turn:relay.example.com:3478")]),
            ]),
        ])
        XCTAssertThrowsError(try NativeTurnRelayObservation(evidence: relayEvidence, iceReadiness: uncleanReadiness, network: "restricted")) { error in
            XCTAssertEqual(error as? NativeTurnRelayObservationError, .uncleanICEReadiness)
        }
    }

    func testNativeMediaEvidenceDoesNotTreatEncodedFramesAsCameraProof() {
        let source = NativeMediaQualitySnapshot(
            outboundVideoFramesEncoded: 300,
            inboundVideoDecoded: 250,
            candidatePair: NativeMediaQualityCandidatePair(localCandidateType: "relay")
        )

        let evidence = NativeMediaEvidenceSnapshot(source: source, remoteVideoTiles: 1, renderer: Self.rendererEvidence())

        XCTAssertFalse(evidence.mediaAssertions.cameraPublished)
        XCTAssertFalse(evidence.mediaAssertions.microphonePublished)
        XCTAssertFalse(evidence.mediaAssertions.remoteAudioReceived)
        XCTAssertTrue(evidence.mediaAssertions.remoteVideoRendered)
        XCTAssertEqual(evidence.status, "observed")
        XCTAssertTrue(evidence.selectedCandidate.relayCandidateSelected)
    }

    func testNativeMediaEvidenceRequiresRemoteTileDecodedVideoAndRendererForRemoteVideoRendered() {
        let decodedWithoutTile = NativeMediaEvidenceSnapshot(
            source: NativeMediaQualitySnapshot(inboundVideoDecoded: 250),
            remoteVideoTiles: 0,
            renderer: Self.rendererEvidence()
        )
        let tileWithoutDecoded = NativeMediaEvidenceSnapshot(
            source: NativeMediaQualitySnapshot(inboundVideoPacketsReceived: 250),
            remoteVideoTiles: 1,
            renderer: Self.rendererEvidence()
        )
        let decodedAndTileWithoutRenderer = NativeMediaEvidenceSnapshot(
            source: NativeMediaQualitySnapshot(inboundVideoDecoded: 250),
            remoteVideoTiles: 1
        )

        XCTAssertFalse(decodedWithoutTile.mediaAssertions.remoteVideoRendered)
        XCTAssertFalse(tileWithoutDecoded.mediaAssertions.remoteVideoRendered)
        XCTAssertFalse(decodedAndTileWithoutRenderer.mediaAssertions.remoteVideoRendered)
    }

    func testMediaQualitySnapshotFallsBackToSucceededCandidatePairAndMediaType() {
        let snapshot = NativeRoomRTCClient.mediaQualitySnapshot(from: [
            stat(
                id: "audio-out",
                type: "outbound-rtp",
                values: [
                    "mediaType": .string("audio"),
                    "bytesSent": .number(2_400)
                ]
            ),
            stat(
                id: "pair-fallback",
                type: "candidate-pair",
                values: [
                    "state": .string("succeeded"),
                    "protocol": .string("tcp"),
                    "currentRoundTripTime": .number(0.11)
                ]
            )
        ])

        XCTAssertEqual(snapshot.outboundAudioBytesSent, 2_400)
        XCTAssertEqual(snapshot.candidatePair.protocol, "tcp")
        XCTAssertEqual(snapshot.outboundRtt, 0.11)
    }

    func testMediaQualityDeltasMatchBrowserDeltaShape() {
        let previous = NativeMediaQualitySnapshot(
            at: 1_000,
            outboundAudioBytesSent: 10,
            outboundAudioPacketsSent: 1,
            outboundVideoBytesSent: 100,
            outboundVideoFramesSent: 10,
            inboundAudioLost: 1,
            inboundAudioPacketsReceived: 50,
            inboundVideoLost: 2,
            inboundVideoPacketsReceived: 100,
            inboundVideoDrops: 3,
            inboundVideoDecoded: 30
        )
        let current = NativeMediaQualitySnapshot(
            at: 13_000,
            outboundAudioBytesSent: 70,
            outboundAudioPacketsSent: 7,
            outboundVideoBytesSent: 700,
            outboundVideoFramesSent: 70,
            inboundAudioLost: 2,
            inboundAudioPacketsReceived: 150,
            inboundVideoLost: 4,
            inboundVideoPacketsReceived: 300,
            inboundVideoDrops: 5,
            inboundVideoDecoded: 90
        )

        XCTAssertEqual(
            current.deltas(since: previous),
            NativeMediaQualityDeltas(
                outboundAudioBytesSent: 60,
                outboundAudioPacketsSent: 6,
                outboundVideoBytesSent: 600,
                outboundVideoFramesSent: 60,
                inboundAudioPacketsLost: 1,
                inboundAudioPacketsReceived: 100,
                inboundVideoPacketsLost: 2,
                inboundVideoPacketsReceived: 200,
                inboundVideoDecoded: 60,
                inboundVideoDrops: 2,
                elapsedMs: 12_000
            )
        )
    }

    func testLocalTrackTogglesAreSafeBeforeMediaPreparation() async {
        let client = NativeRoomRTCClient()

        await client.setLocalAudioEnabled(false)
        await client.setLocalVideoEnabled(false)

        XCTAssertEqual(client.lifecycle, .signedOut)
    }

    func testScreenShareRequiresPublishedVideoSender() async throws {
        let client = NativeRoomRTCClient()

        try await client.configure(testClientConfig)
        try await client.prepareLocalMedia(audio: true, video: false)

        do {
            try await client.setScreenShareEnabled(true)
            XCTFail("screen sharing should require a published video sender")
        } catch RoomRTCError.screenShareUnavailable {
        } catch {
            XCTFail("unexpected error: \(error)")
        }

        await client.leave()
    }

    #if os(macOS)
    func testScreenShareTrackSwitchReplacesTrackStartsCaptureAndRestoresCamera() async throws {
        var events: [String] = []
        var installedTrack: String?
        let trackSwitch = NativeScreenShareTrackSwitch(hasScreenCaptureAccess: {
            events.append("permission")
            return true
        })

        let screenTrack = try trackSwitch.start(
            makeScreenTrack: {
                events.append("make-screen")
                return "screen-track"
            },
            installScreenTrack: { track in
                installedTrack = track
                events.append("install-\(track)")
            },
            startCapture: { track in
                events.append("start-\(track)")
            }
        )

        await trackSwitch.stop(
            cameraTrack: "camera-track",
            capturer: "desktop-capturer",
            restoreCameraTrack: { track in
                installedTrack = track
                events.append("restore-\(track ?? "nil")")
            },
            stopCapture: { capturer in
                events.append("stop-\(capturer)")
            }
        )

        XCTAssertEqual(screenTrack, "screen-track")
        XCTAssertEqual(installedTrack, "camera-track")
        XCTAssertEqual(events, [
            "permission",
            "make-screen",
            "install-screen-track",
            "start-screen-track",
            "restore-camera-track",
            "stop-desktop-capturer"
        ])
    }

    func testScreenShareTrackSwitchDeniesBeforeMakingOrInstallingScreenTrack() {
        var events: [String] = []
        let trackSwitch = NativeScreenShareTrackSwitch(hasScreenCaptureAccess: {
            events.append("permission")
            return false
        })

        XCTAssertThrowsError(
            try trackSwitch.start(
                makeScreenTrack: {
                    events.append("make-screen")
                    return "screen-track"
                },
                installScreenTrack: { track in
                    events.append("install-\(track)")
                },
                startCapture: { track in
                    events.append("start-\(track)")
                }
            )
        ) { error in
            XCTAssertEqual(error as? RoomRTCError, .screenCapturePermissionDenied)
        }
        XCTAssertEqual(events, ["permission"])
    }
    #endif

    func testHandleOfferRequiresConfiguration() async throws {
        let client = NativeRoomRTCClient()

        do {
            _ = try await client.handleOffer("v=0\r\n")
            XCTFail("handleOffer should require configure(_:) first")
        } catch RoomRTCError.peerConnectionNotConfigured {
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    func testMediaQualitySnapshotRequiresConfiguration() async throws {
        let client = NativeRoomRTCClient()

        do {
            _ = try await client.mediaQualitySnapshot()
            XCTFail("mediaQualitySnapshot should require configure(_:) first")
        } catch RoomRTCError.peerConnectionNotConfigured {
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    private static func turnRelayEvidence(
        candidatePair: NativeMediaQualityCandidatePair
    ) -> NativeMediaEvidenceSnapshot {
        NativeMediaEvidenceSnapshot(
            source: NativeMediaQualitySnapshot(
                at: 1_000,
                outboundAudioPacketsSent: 12,
                outboundVideoFramesSent: 24,
                inboundAudioPacketsReceived: 36,
                inboundVideoDecoded: 48,
                candidatePair: candidatePair
            ),
            capturedAt: "2026-06-29T17:00:00Z",
            client: NativeMediaEvidenceClient(platform: "ios", version: "test"),
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
            lifecycle: .connected,
            remoteVideoTiles: 1,
            renderer: Self.rendererEvidence(),
            runId: "native-apple-run-test",
            roomId: "release-room-test"
        )
    }

    private static func rendererEvidence() -> NativeMediaEvidenceRendererContext {
        NativeMediaEvidenceRendererContext(
            remoteVideoFramesRendered: 3,
            observedRemoteVideoTracks: 1,
            latestFrameWidth: 1280,
            latestFrameHeight: 720,
            latestRenderedAt: "2026-06-29T17:00:01Z"
        )
    }
}

private func stat(
    id: String,
    type: String,
    timestampUs: Double = 1_000_000,
    values: [String: JSONValue] = [:]
) -> NativeRTCStatisticsEntry {
    NativeRTCStatisticsEntry(id: id, type: type, timestampUs: timestampUs, values: values)
}

private let testClientConfig = ClientRTCConfig(
    rtcConfiguration: [
        "iceServers": .array([
            .object(["urls": .string("stun:stun.l.google.com:19302")]),
            .object([
                "urls": .array([.string("turn:turn.example.com:3478")]),
                "username": .string("native"),
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
