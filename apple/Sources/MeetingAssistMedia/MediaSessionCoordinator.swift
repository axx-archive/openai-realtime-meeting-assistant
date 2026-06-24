import Foundation
import MeetingAssistCore

#if canImport(AVFoundation)
import AVFoundation
#endif

public final class MediaSessionCoordinator: @unchecked Sendable {
    public private(set) var participantMediaState = ParticipantMediaState()

    public init() {}

    public func setMuted(_ muted: Bool) {
        participantMediaState.micMuted = muted
    }

    public func setCameraOff(_ off: Bool) {
        participantMediaState.cameraOff = off
    }

    public func setScreenSharing(_ sharing: Bool) {
        participantMediaState.screenSharing = sharing
    }

    #if os(iOS)
    public func configureVideoChatAudioSession() throws {
        let session = AVAudioSession.sharedInstance()
        try session.setCategory(.playAndRecord, mode: .videoChat, options: [.allowBluetoothHFP, .allowBluetoothA2DP, .defaultToSpeaker])
        try session.setActive(true)
    }
    #endif
}
