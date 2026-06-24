import Foundation
import MeetingAssistRoomRTC

public struct NativeParticipantTrackMetadata: Codable, Equatable, Sendable {
    public var name: String
    public var kind: String
    public var trackId: String
    public var sourceTrackId: String?
    public var streamId: String?

    public init(
        name: String,
        kind: String,
        trackId: String,
        sourceTrackId: String? = nil,
        streamId: String? = nil
    ) {
        self.name = name
        self.kind = kind
        self.trackId = trackId
        self.sourceTrackId = sourceTrackId
        self.streamId = streamId
    }

    public var isVideo: Bool {
        kind.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() == "video"
    }

    public var normalizedName: String? {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    public var trackLabelKeys: [String] {
        Self.normalizedKeys([trackId, sourceTrackId])
    }

    public var reliableStreamId: String? {
        Self.reliableStreamId(streamId)
    }

    public static func normalizedKeys(_ keys: [String?]) -> [String] {
        var result: [String] = []
        var seen = Set<String>()
        for key in keys {
            let trimmed = key?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            guard !trimmed.isEmpty, !seen.contains(trimmed) else { continue }
            seen.insert(trimmed)
            result.append(trimmed)
        }
        return result
    }

    public static func reliableStreamId(_ streamId: String?) -> String? {
        let trimmed = streamId?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !trimmed.isEmpty else { return nil }
        switch trimmed.lowercased() {
        case "-", "stream", "default":
            return nil
        default:
            return trimmed
        }
    }
}

public struct NativeRemoteVideoTrackInfo: Identifiable, Sendable {
    public var track: NativeRemoteVideoTrack
    public var participantName: String?

    public init(track: NativeRemoteVideoTrack, participantName: String? = nil) {
        self.track = track
        self.participantName = participantName
    }

    public var id: String {
        track.id
    }

    public var displayName: String {
        let trimmed = participantName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? track.id : trimmed
    }
}

public typealias NativeRemoteVideoTrackInfoHandler = @Sendable (NativeRemoteVideoTrackInfo) async -> Void
