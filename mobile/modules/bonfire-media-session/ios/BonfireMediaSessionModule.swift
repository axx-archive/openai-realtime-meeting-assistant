import AVFoundation
import ExpoModulesCore

public final class BonfireMediaSessionModule: Module {
  private var meetingActive = false

  public func definition() -> ModuleDefinition {
    Name("BonfireMediaSession")

    AsyncFunction("activateVideoMeeting") { () -> [String: Any] in
      let session = AVAudioSession.sharedInstance()
      self.meetingActive = true
      let snapshot = try Self.configureVideoMeetingRoute(session)

      // WebRTC can rewrite AVAudioSession once its first remote audio track is
      // attached. Reassert after that transaction settles; the generation flag
      // prevents a late callback from reopening audio after Leave.
      DispatchQueue.main.asyncAfter(deadline: .now() + 0.35) { [weak self] in
        guard self?.meetingActive == true else { return }
        try? Self.configureVideoMeetingRoute(AVAudioSession.sharedInstance())
      }
      return snapshot
    }

    AsyncFunction("deactivateVideoMeeting") { () -> Bool in
      self.meetingActive = false
      let session = AVAudioSession.sharedInstance()
      try? session.overrideOutputAudioPort(.none)
      try session.setActive(false, options: .notifyOthersOnDeactivation)
      return true
    }
  }

  private static func configureVideoMeetingRoute(_ session: AVAudioSession) throws -> [String: Any] {
    var options: AVAudioSession.CategoryOptions = [
      .defaultToSpeaker,
      .allowBluetoothA2DP,
      .allowAirPlay,
    ]
    if #available(iOS 26.0, *) {
      options.insert(.allowBluetoothHFP)
    } else {
      options.insert(.allowBluetooth)
    }

    try session.setCategory(.playAndRecord, mode: .videoChat, options: options)
    try session.setActive(true)

    // A Bonfire room is a video meeting, not a telephone call. WebRTC may
    // explicitly select the receiver after category activation, so the
    // defaultToSpeaker option alone is insufficient. Override only when every
    // current output is built-in; wired, Bluetooth, AirPlay, USB, and car
    // routes are all classified as external and remain user-owned.
    let builtInOutputs: Set<AVAudioSession.Port> = [.builtInReceiver, .builtInSpeaker]
    let hasExternalOutput = session.currentRoute.outputs.contains { output in
      !builtInOutputs.contains(output.portType)
    }
    try session.overrideOutputAudioPort(hasExternalOutput ? .none : .speaker)
    return routeSnapshot(session)
  }

  private static func routeSnapshot(_ session: AVAudioSession) -> [String: Any] {
    let outputs = session.currentRoute.outputs.map { output in
      [
        "name": output.portName,
        "type": output.portType.rawValue,
      ]
    }
    return [
      "category": session.category.rawValue,
      "mode": session.mode.rawValue,
      "outputs": outputs,
    ]
  }
}
