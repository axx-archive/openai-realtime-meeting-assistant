import Foundation
import MeetingAssistCore

public actor MeetingAssistSignalingClient {
    public private(set) var lifecycle: RoomLifecycleState = .signedOut
    private let session: URLSession
    private var task: URLSessionWebSocketTask?
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    public init(session: URLSession = .shared) {
        self.session = session
    }

    public func connect(to url: URL) {
        task?.cancel(with: .goingAway, reason: nil)
        task = session.webSocketTask(with: url)
        task?.resume()
        lifecycle = .authenticated
    }

    public func send(event: String, data: String = "{}") async throws {
        let envelope = WebSocketEnvelope(event: event, data: data)
        let payload = try encoder.encode(envelope)
        try await task?.send(.data(payload))
    }

    public func sendJSON<T: Encodable>(event: String, payload: T) async throws {
        let data = try encoder.encode(payload)
        try await send(event: event, data: String(decoding: data, as: UTF8.self))
    }

    public func receive() async throws -> WebSocketEnvelope {
        guard let task else { throw MeetingAssistSignalingError.notConnected }
        let message = try await task.receive()
        let data: Data
        switch message {
        case .data(let value):
            data = value
        case .string(let value):
            data = Data(value.utf8)
        @unknown default:
            throw MeetingAssistSignalingError.unsupportedMessage
        }
        return try decoder.decode(WebSocketEnvelope.self, from: data)
    }

    public func close() {
        task?.cancel(with: .normalClosure, reason: nil)
        task = nil
        lifecycle = .leaving
    }
}

public enum MeetingAssistSignalingError: Error, Equatable {
    case notConnected
    case unsupportedMessage
}
