import Foundation
import MeetingAssistCore

#if canImport(AVFoundation)
import AVFoundation
#endif

public typealias MediaAudioSessionConfigurator = @Sendable () throws -> Void

public final class MediaSessionCoordinator: @unchecked Sendable {
    private let lock = NSLock()
    private let audioSessionConfigurator: MediaAudioSessionConfigurator
    private var _participantMediaState = ParticipantMediaState()

    public var participantMediaState: ParticipantMediaState {
        lock.withLock { _participantMediaState }
    }

    public init(audioSessionConfigurator: MediaAudioSessionConfigurator? = nil) {
        self.audioSessionConfigurator = audioSessionConfigurator ?? Self.platformAudioSessionConfigurator
    }

    public func setMuted(_ muted: Bool) {
        lock.withLock {
            _participantMediaState.micMuted = muted
        }
    }

    public func setCameraOff(_ off: Bool) {
        lock.withLock {
            _participantMediaState.cameraOff = off
        }
    }

    public func setScreenSharing(_ sharing: Bool) {
        lock.withLock {
            _participantMediaState.screenSharing = sharing
        }
    }

    public func configureVideoChatAudioSession() throws {
        try audioSessionConfigurator()
    }

    private static func platformAudioSessionConfigurator() throws {
        #if os(iOS)
        let session = AVAudioSession.sharedInstance()
        try session.setCategory(.playAndRecord, mode: .videoChat, options: [.allowBluetoothHFP, .allowBluetoothA2DP, .defaultToSpeaker])
        try session.setActive(true)
        #endif
    }
}
