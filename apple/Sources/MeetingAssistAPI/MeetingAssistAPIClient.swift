import Foundation
import MeetingAssistCore

public struct MeetingAssistAPIClient: Sendable {
    public var baseURL: URL
    public var session: URLSession
    private let decoder: JSONDecoder
    private let encoder: JSONEncoder

    public init(baseURL: URL, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.session = session
        self.decoder = JSONDecoder()
        self.encoder = JSONEncoder()
    }

    public func nativeConfig() async throws -> NativeClientConfig {
        try await get("native/config")
    }

    public func clientConfig(path: String = "client-config") async throws -> ClientRTCConfig {
        try await get(path)
    }

    public func login(name: String, password: String, path: String = "auth/login") async throws -> Participant {
        struct LoginRequest: Encodable { var name: String; var password: String }
        struct LoginResponse: Decodable { var email: String; var name: String }
        let response: LoginResponse = try await post(path, body: LoginRequest(name: name, password: password))
        return Participant(name: response.name, email: response.email)
    }

    public func me(path: String = "auth/me") async throws -> Participant {
        struct MeResponse: Decodable { var email: String; var name: String }
        let response: MeResponse = try await get(path)
        return Participant(name: response.name, email: response.email)
    }

    public func logout(path: String = "auth/logout") async throws {
        struct Empty: Encodable {}
        let _: JSONValue = try await post(path, body: Empty())
    }

    public func get<Response: Decodable>(_ path: String) async throws -> Response {
        let (data, response) = try await session.data(for: URLRequest(url: endpoint(path)))
        try validate(response, data: data)
        return try decoder.decode(Response.self, from: data)
    }

    public func post<Body: Encodable, Response: Decodable>(_ path: String, body: Body) async throws -> Response {
        var request = URLRequest(url: endpoint(path))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try encoder.encode(body)
        let (data, response) = try await session.data(for: request)
        try validate(response, data: data)
        return try decoder.decode(Response.self, from: data)
    }

    private func endpoint(_ path: String) -> URL {
        baseURL.appending(path: path.trimmingCharacters(in: CharacterSet(charactersIn: "/")))
    }

    private func validate(_ response: URLResponse, data: Data) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else {
            throw MeetingAssistAPIError.httpStatus(http.statusCode, String(data: data, encoding: .utf8) ?? "")
        }
    }
}

public enum MeetingAssistAPIError: Error, Equatable {
    case httpStatus(Int, String)
}
