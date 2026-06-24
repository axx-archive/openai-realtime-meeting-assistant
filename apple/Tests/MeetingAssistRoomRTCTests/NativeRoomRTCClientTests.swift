import XCTest
@testable import MeetingAssistCore
@testable import MeetingAssistRoomRTC

final class NativeRoomRTCClientTests: XCTestCase {
    func testWebRTCBinaryIsImportable() {
        XCTAssertTrue(WebRTCLinkStatus.isWebRTCImportable)
    }

    func testConfigureAndPrepareAudioOnlyCreatesNativePeerConnection() async throws {
        let client = NativeRoomRTCClient()

        try await client.configure(testClientConfig)
        try await client.prepareLocalMedia(audio: true, video: false)

        XCTAssertEqual(client.lifecycle, .preparingMedia)

        await client.leave()
        XCTAssertEqual(client.lifecycle, .leaving)
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
