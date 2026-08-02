import AVFoundation
import ExpoModulesCore
import WebRTC

private final class BonfireWebRTCAudioDelegate: NSObject, RTCAudioSessionDelegate {
  weak var owner: BonfireMediaSessionModule?

  func audioSessionDidStartPlayOrRecord(_ session: RTCAudioSession) {
    owner?.scheduleRouteReassertion()
  }

  func audioSessionDidChangeRoute(
    _ session: RTCAudioSession,
    reason: AVAudioSession.RouteChangeReason,
    previousRoute: AVAudioSessionRouteDescription
  ) {
    owner?.scheduleRouteReassertion()
  }

  func audioSessionMediaServerReset(_ session: RTCAudioSession) {
    owner?.scheduleRouteReassertion()
  }
}

public final class BonfireMediaSessionModule: Module {
  private let routeQueue = DispatchQueue(label: "xyz.thebonfire.media-session-route")
  private var activeGeneration: Int64?
  private var latestGeneration: Int64 = 0
  private var retiredThroughGeneration: Int64 = 0
  private var ownsWebRTCActivation = false
  private lazy var webRTCAudioDelegate: BonfireWebRTCAudioDelegate = {
    let delegate = BonfireWebRTCAudioDelegate()
    delegate.owner = self
    return delegate
  }()

  public func definition() -> ModuleDefinition {
    Name("BonfireMediaSession")

    OnCreate {
      RTCAudioSession.sharedInstance().add(self.webRTCAudioDelegate)
    }

    OnDestroy {
      RTCAudioSession.sharedInstance().remove(self.webRTCAudioDelegate)
      self.routeQueue.sync {
        _ = try? self.deactivateVideoMeetingRoute(retiringThrough: nil)
      }
    }

    AsyncFunction("activateVideoMeeting") { (generation: Int64) -> [String: Any] in
      guard generation > 0 else {
        throw Self.generationError("A positive media-session generation is required.")
      }
      let snapshot = try self.routeQueue.sync {
        guard generation > self.retiredThroughGeneration,
              generation >= self.latestGeneration else {
          throw Self.generationError("A stale media-session activation was rejected.")
        }
        // Fence every older callback before touching RTCAudioSession. If this
        // configuration fails, a same-generation terminal call can still close
        // the prior native activation without letting the prior owner reassert.
        self.latestGeneration = generation
        let shouldActivate = !self.ownsWebRTCActivation
        var snapshot = try Self.configureVideoMeetingRoute(activate: shouldActivate)
        self.activeGeneration = generation
        if shouldActivate {
          self.ownsWebRTCActivation = true
        }
        snapshot["generation"] = NSNumber(value: generation)
        return snapshot
      }

      // Keep one delayed pass for older libwebrtc builds whose audio-unit start
      // callback can precede their final category transaction. The delegate is
      // the primary lifecycle edge; this bounded compatibility pass never
      // increments RTCAudioSession's activation count.
      self.routeQueue.asyncAfter(deadline: .now() + 0.35) { [weak self] in
        guard let self,
              self.activeGeneration == generation,
              self.latestGeneration == generation,
              generation > self.retiredThroughGeneration else { return }
        _ = try? Self.configureVideoMeetingRoute(activate: false)
      }
      return snapshot
    }

    AsyncFunction("deactivateVideoMeeting") { (generation: Int64) -> Bool in
      guard generation > 0 else {
        throw Self.generationError("A positive media-session generation is required.")
      }
      return try self.routeQueue.sync { () throws -> Bool in
        try self.deactivateVideoMeetingRoute(retiringThrough: generation)
      }
    }
  }

  private func deactivateVideoMeetingRoute(retiringThrough generation: Int64?) throws -> Bool {
    if let generation {
      latestGeneration = max(latestGeneration, generation)
      retiredThroughGeneration = max(retiredThroughGeneration, generation)
      guard let activeGeneration else { return true }
      // A delayed teardown from an older JS owner is an acknowledged no-op. It
      // must never deactivate a newer room or personal-Realtime generation.
      guard activeGeneration <= generation else { return false }
    }
    activeGeneration = nil
    let rtcSession = RTCAudioSession.sharedInstance()
    rtcSession.lockForConfiguration()
    defer { rtcSession.unlockForConfiguration() }

    _ = try? rtcSession.overrideOutputAudioPort(.none)

    guard ownsWebRTCActivation else { return true }
    try rtcSession.setActive(false)
    ownsWebRTCActivation = false
    return true
  }

  fileprivate func scheduleRouteReassertion() {
    routeQueue.async { [weak self] in
      guard let self,
            let activeGeneration = self.activeGeneration,
            activeGeneration == self.latestGeneration,
            activeGeneration > self.retiredThroughGeneration else { return }
      _ = try? Self.configureVideoMeetingRoute(activate: false)
    }
  }

  private static func generationError(_ message: String) -> NSError {
    NSError(
      domain: "xyz.thebonfire.media-session",
      code: 1,
      userInfo: [NSLocalizedDescriptionKey: message]
    )
  }

  private static func configureVideoMeetingRoute(activate: Bool) throws -> [String: Any] {
    var options: AVAudioSession.CategoryOptions = [
      .defaultToSpeaker,
      .allowBluetoothA2DP,
      .allowAirPlay,
    ]
    options.insert(.allowBluetoothHFP)

    // WebRTC owns and locks the audio session while its VoIP audio unit starts.
    // Configure that owner rather than racing AVAudioSession directly. Updating
    // its global configuration also prevents later audio-unit restarts from
    // restoring the receiver route behind the app's back.
    let configuration = RTCAudioSessionConfiguration.webRTC()
    configuration.category = AVAudioSession.Category.playAndRecord.rawValue
    configuration.mode = AVAudioSession.Mode.videoChat.rawValue
    configuration.categoryOptions = options
    RTCAudioSessionConfiguration.setWebRTC(configuration)

    let rtcSession = RTCAudioSession.sharedInstance()
    rtcSession.lockForConfiguration()
    defer { rtcSession.unlockForConfiguration() }

    var activationSucceeded = false
    do {
      if activate {
        try rtcSession.setConfiguration(configuration, active: true)
        activationSucceeded = true
      } else {
        try rtcSession.setConfiguration(configuration)
      }

      // A Bonfire room is a video meeting, not a telephone call. Preserve wired,
      // Bluetooth, AirPlay, USB, and car routes; only replace the built-in
      // receiver with the built-in speaker.
      let session = rtcSession.session
      let builtInOutputs: Set<AVAudioSession.Port> = [.builtInReceiver, .builtInSpeaker]
      let hasExternalOutput = session.currentRoute.outputs.contains { output in
        !builtInOutputs.contains(output.portType)
      }
      let alreadyOnSpeaker = session.currentRoute.outputs.contains { output in
        output.portType == .builtInSpeaker
      }

      if hasExternalOutput {
        try rtcSession.overrideOutputAudioPort(.none)
      } else if !alreadyOnSpeaker {
        try rtcSession.overrideOutputAudioPort(.speaker)
      }
      return routeSnapshot(session)
    } catch {
      if activationSucceeded {
        _ = try? rtcSession.setActive(false)
      }
      throw error
    }
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
