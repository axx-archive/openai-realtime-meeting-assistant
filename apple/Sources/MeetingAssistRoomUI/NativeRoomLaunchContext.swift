import Foundation

public struct NativeRoomLaunchContext: Equatable, Sendable {
    public var baseURLString: String?
    public var selectedName: String?
    public var releaseRunId: String?
    public var releaseRoomId: String?

    public init(
        baseURLString: String? = nil,
        selectedName: String? = nil,
        releaseRunId: String? = nil,
        releaseRoomId: String? = nil
    ) {
        self.baseURLString = baseURLString
        self.selectedName = selectedName
        self.releaseRunId = releaseRunId
        self.releaseRoomId = releaseRoomId
    }

    public init(url: URL) throws {
        guard url.scheme?.lowercased() == "meetingassist" else {
            throw NativeRoomLaunchError.unsupportedScheme
        }

        let route = Self.routeName(url)
        guard route == "room" || route == "join" else {
            throw NativeRoomLaunchError.unsupportedRoute
        }

        let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        for item in items where Self.sensitiveQueryNames.contains(item.name.lowercased()) {
            throw NativeRoomLaunchError.secretQueryItem
        }

        let roomURL = Self.firstValue(for: ["url", "roomUrl", "baseUrl"], in: items)
        let name = Self.firstValue(for: ["name", "participant"], in: items)
        let runId = Self.firstValue(for: ["runId", "releaseRunId"], in: items)
        let roomId = Self.firstValue(for: ["roomId", "releaseRoomId"], in: items)

        let normalizedURL = try Self.normalizedRoomURL(roomURL)
        let normalizedName = Self.normalizedName(name)
        let normalizedRunId = try Self.normalizedEvidenceIdentifier(runId)
        let normalizedRoomId = try Self.normalizedEvidenceIdentifier(roomId)
        guard normalizedURL != nil || normalizedName != nil || normalizedRunId != nil || normalizedRoomId != nil else {
            throw NativeRoomLaunchError.emptyLaunchContext
        }

        self.baseURLString = normalizedURL
        self.selectedName = normalizedName
        self.releaseRunId = normalizedRunId
        self.releaseRoomId = normalizedRoomId
    }

    private static let sensitiveQueryNames = Set([
        "password",
        "pwd",
        "pass",
        "token",
        "session",
        "cookie",
        "authorization",
        "auth",
    ])

    private static func routeName(_ url: URL) -> String {
        if let host = url.host?.trimmingCharacters(in: .whitespacesAndNewlines),
           !host.isEmpty {
            return host.lowercased()
        }
        return url.pathComponents
            .first { $0 != "/" }?
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased() ?? ""
    }

    private static func firstValue(for names: [String], in items: [URLQueryItem]) -> String? {
        for name in names {
            if let value = items.first(where: { $0.name.caseInsensitiveCompare(name) == .orderedSame })?.value {
                return value
            }
        }
        return nil
    }

    private static func normalizedRoomURL(_ value: String?) throws -> String? {
        guard let value else { return nil }
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        guard let url = URL(string: trimmed),
              let scheme = url.scheme?.lowercased(),
              ["http", "https"].contains(scheme),
              url.host != nil else {
            throw NativeRoomLaunchError.invalidRoomURL
        }
        return trimmed
    }

    private static func normalizedName(_ value: String?) -> String? {
        let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? nil : trimmed
    }

    private static func normalizedEvidenceIdentifier(_ value: String?) throws -> String? {
        let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !trimmed.isEmpty else { return nil }
        guard trimmed.count <= 120,
              trimmed.range(of: #"^[A-Za-z0-9][A-Za-z0-9._:-]*$"#, options: .regularExpression) != nil,
              !secretLike(trimmed) else {
            throw NativeRoomLaunchError.invalidEvidenceBinding
        }
        return trimmed
    }

    private static func secretLike(_ value: String) -> Bool {
        value.range(of: #"sk-[A-Za-z0-9_-]{20,}"#, options: .regularExpression) != nil ||
            value.range(of: #"-----BEGIN [A-Z ]*PRIVATE KEY-----"#, options: .regularExpression) != nil ||
            value.range(of: #"\b[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b"#, options: .regularExpression) != nil ||
            value.localizedCaseInsensitiveContains("bearer ")
    }
}

public enum NativeRoomLaunchError: LocalizedError, Equatable {
    case unsupportedScheme
    case unsupportedRoute
    case invalidRoomURL
    case invalidEvidenceBinding
    case secretQueryItem
    case emptyLaunchContext

    public var errorDescription: String? {
        switch self {
        case .unsupportedScheme:
            return "Unsupported launch link."
        case .unsupportedRoute:
            return "MeetingAssist launch links must open the room route."
        case .invalidRoomURL:
            return "Launch link room URL must be an http or https URL."
        case .invalidEvidenceBinding:
            return "Launch link run and room IDs must be short non-secret identifiers."
        case .secretQueryItem:
            return "Launch links cannot contain passwords, tokens, cookies, or other secrets."
        case .emptyLaunchContext:
            return "Launch link did not include a room URL, participant name, run ID, or room ID."
        }
    }
}
