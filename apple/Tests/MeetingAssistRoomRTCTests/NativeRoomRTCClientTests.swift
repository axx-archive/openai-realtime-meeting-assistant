import XCTest
@testable import MeetingAssistCore
@testable import MeetingAssistRoomRTC

final class NativeRoomRTCClientTests: XCTestCase {
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
