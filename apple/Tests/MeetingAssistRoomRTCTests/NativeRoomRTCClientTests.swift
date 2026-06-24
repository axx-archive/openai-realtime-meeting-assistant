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
