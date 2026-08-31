import XCTest
@testable import MeetingAssistAPI
@testable import MeetingAssistCore
@testable import MeetingAssistRoom
@testable import MeetingAssistRoomRTC
@testable import MeetingAssistSignaling

final class NativeRoomLiveIntegrationTests: XCTestCase {
    func testOptInLocalSignalingDeliversPublisherOffer() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard let rawURL = environment["STRIDE_NATIVE_LIVE_URL"],
              let baseURL = URL(string: rawURL),
              let name = environment["STRIDE_NATIVE_LIVE_NAME"],
              let password = environment["STRIDE_NATIVE_LIVE_PASSWORD"] else {
            throw XCTSkip("Set the STRIDE_NATIVE_LIVE_* variables to run the isolated local-room check.")
        }
        guard ["127.0.0.1", "localhost"].contains(baseURL.host?.lowercased() ?? "") else {
            XCTFail("The opt-in live test is restricted to a loopback server.")
            return
        }

        let api = MeetingAssistAPIClient(baseURL: baseURL)
        let discovery = try await api.nativeDiscovery()
        _ = try await api.login(name: name, password: password, path: discovery.auth.loginPath)
        _ = try await api.clientConfig(path: discovery.room.clientConfigPath)

        let client = MeetingAssistSignalingClient()
        let websocketURL = NativeRoomSessionCoordinator.websocketURL(
            baseURL: baseURL,
            path: discovery.room.websocketPath
        )
        await client.connect(to: websocketURL)
        defer { Task { await client.close() } }

        let endpointID = UUID().uuidString.lowercased()
        let hello = "{\"endpointId\":\"\(endpointID)\",\"client\":{\"platform\":\"macos\",\"version\":\"local-qa\"}}"
        try await client.send(event: "participant", data: hello)

        var admitted = false
        while !admitted {
            let envelope = try await client.receive()
            guard envelope.event == "kanban",
                  let data = envelope.data.data(using: .utf8),
                  let event = try? JSONDecoder().decode(RoomEvent<JSONValue>.self, from: data) else {
                continue
            }
            admitted = event.event == "access_granted"
        }

        try await client.send(
            event: "media_ready",
            data: "{\"client\":{\"platform\":\"macos\",\"version\":\"local-qa\"},\"media\":{\"audio\":true,\"video\":false}}"
        )

        var receivedOffer = false
        while !receivedOffer {
            let envelope = try await client.receive()
            receivedOffer = envelope.event == "offer"
        }
        XCTAssertTrue(receivedOffer)
    }

    func testOptInLocalPublisherOfferCanBeAnswered() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard let rawURL = environment["STRIDE_NATIVE_LIVE_URL"],
              let baseURL = URL(string: rawURL),
              let name = environment["STRIDE_NATIVE_LIVE_NAME"],
              let password = environment["STRIDE_NATIVE_LIVE_PASSWORD"] else {
            throw XCTSkip("Set the STRIDE_NATIVE_LIVE_* variables to run the isolated local-room check.")
        }
        guard ["127.0.0.1", "localhost"].contains(baseURL.host?.lowercased() ?? "") else {
            XCTFail("The opt-in live test is restricted to a loopback server.")
            return
        }

        let api = MeetingAssistAPIClient(baseURL: baseURL)
        let discovery = try await api.nativeDiscovery()
        _ = try await api.login(name: name, password: password, path: discovery.auth.loginPath)
        let config = try await api.clientConfig(path: discovery.room.clientConfigPath)

        let client = MeetingAssistSignalingClient()
        let websocketURL = NativeRoomSessionCoordinator.websocketURL(
            baseURL: baseURL,
            path: discovery.room.websocketPath
        )
        await client.connect(to: websocketURL)
        defer { Task { await client.close() } }

        let endpointID = UUID().uuidString.lowercased()
        let hello = "{\"endpointId\":\"\(endpointID)\",\"client\":{\"platform\":\"macos\",\"version\":\"local-qa\"}}"
        try await client.send(event: "participant", data: hello)

        var admitted = false
        while !admitted {
            let envelope = try await client.receive()
            guard envelope.event == "kanban",
                  let data = envelope.data.data(using: .utf8),
                  let event = try? JSONDecoder().decode(RoomEvent<JSONValue>.self, from: data) else {
                continue
            }
            admitted = event.event == "access_granted"
        }

        let rtc = NativeRoomRTCClient(permissionAuthorizer: .allowingAllForTesting)
        try await rtc.configure(config)
        try await rtc.prepareLocalMedia(audio: true, video: false)
        defer { Task { await rtc.leave() } }

        try await client.send(
            event: "media_ready",
            data: "{\"client\":{\"platform\":\"macos\",\"version\":\"local-qa\"},\"media\":{\"audio\":true,\"video\":false}}"
        )

        var offerEnvelope: WebSocketEnvelope?
        while offerEnvelope == nil {
            let envelope = try await client.receive()
            if envelope.event == "offer" {
                offerEnvelope = envelope
            }
        }
        let envelope = try XCTUnwrap(offerEnvelope)
        let offer = try JSONDecoder().decode(
            RTCSessionDescriptionPayload.self,
            from: Data(envelope.data.utf8)
        )
        let answerSDP = try await rtc.handleOffer(offer.sdp)
        XCTAssertFalse(answerSDP.isEmpty)
        let answer = RTCSessionDescriptionPayload(type: "answer", sdp: answerSDP)
        let answerData = try JSONEncoder().encode(answer)
        try await client.send(
            WebSocketEnvelope(
                event: "answer",
                data: String(decoding: answerData, as: UTF8.self),
                offerId: envelope.offerId,
                revision: envelope.revision
            )
        )
    }

    func testOptInLocalAudioJoinAndCleanup() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard let rawURL = environment["STRIDE_NATIVE_LIVE_URL"],
              let baseURL = URL(string: rawURL),
              let name = environment["STRIDE_NATIVE_LIVE_NAME"],
              let password = environment["STRIDE_NATIVE_LIVE_PASSWORD"] else {
            throw XCTSkip("Set the STRIDE_NATIVE_LIVE_* variables to run the isolated local-room check.")
        }
        guard ["127.0.0.1", "localhost"].contains(baseURL.host?.lowercased() ?? "") else {
            XCTFail("The opt-in live test is restricted to a loopback server.")
            return
        }

        let signaling = RecordingRoomSignalingTransport()
        let coordinator = NativeRoomSessionCoordinator(
            api: MeetingAssistAPIClient(baseURL: baseURL),
            signaling: signaling,
            clientIdentity: NativeRoomClientIdentity(platform: "macos", version: "local-qa")
        )

        do {
            _ = try await withTimeout(seconds: 40) {
                try await coordinator.joinAudioOnly(name: name, password: password)
            }
            let connectedLifecycle = await coordinator.lifecycle
            XCTAssertEqual(connectedLifecycle, .connected)
            let evidence = try await coordinator.captureMediaEvidenceSnapshot()
            XCTAssertEqual(evidence.lifecycle, .connected)
        } catch {
            let trace = await signaling.sanitizedTrace()
            XCTFail("Local native join failed at signaling events \(trace): \(String(reflecting: type(of: error)))")
        }

        await coordinator.leave()
        let leftLifecycle = await coordinator.lifecycle
        XCTAssertEqual(leftLifecycle, .signedOut)
    }

    private func withTimeout<T: Sendable>(
        seconds: UInt64,
        operation: @escaping @Sendable () async throws -> T
    ) async throws -> T {
        try await withThrowingTaskGroup(of: T.self) { group in
            group.addTask { try await operation() }
            group.addTask {
                try await Task.sleep(nanoseconds: seconds * 1_000_000_000)
                throw NativeRoomLiveTestError.timeout
            }
            defer { group.cancelAll() }
            guard let result = try await group.next() else {
                throw NativeRoomLiveTestError.timeout
            }
            return result
        }
    }
}

private enum NativeRoomLiveTestError: Error {
    case timeout
}

private actor RecordingRoomSignalingTransport: NativeRoomSignalingTransport {
    private let transport = URLSessionRoomSignalingTransport()
    private var trace: [String] = []

    func connect(to url: URL) async {
        trace.append("connect")
        await transport.connect(to: url)
    }

    func send(event: String, data: String) async throws {
        trace.append("send:\(event)")
        try await transport.send(event: event, data: data)
    }

    func send(_ envelope: WebSocketEnvelope) async throws {
        trace.append("send:\(envelope.event)")
        try await transport.send(envelope)
    }

    func receive() async throws -> WebSocketEnvelope {
        let envelope = try await transport.receive()
        trace.append("receive:\(envelope.event)")
        return envelope
    }

    func close() async {
        trace.append("close")
        await transport.close()
    }

    func sanitizedTrace() -> [String] {
        trace
    }
}
