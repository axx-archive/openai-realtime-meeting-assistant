import Foundation
#if os(iOS)
import AVFoundation
#endif
#if canImport(Network)
import Network
#endif
import SwiftUI

public final class NativeConnectivityMonitor: ObservableObject, @unchecked Sendable {
    private let queue = DispatchQueue(label: "meetingassist.native.connectivity")
    private let lock = NSLock()
    private var recoveryPolicy = NativeConnectivityRecoveryPolicy()

    #if canImport(Network)
    private var monitor: NWPathMonitor?
    #endif

    public init() {}

    deinit {
        stop()
    }

    public func start(onRecovery: @escaping @MainActor @Sendable (String) -> Void) {
        #if canImport(Network)
        stop()
        let monitor = NWPathMonitor()
        lock.lock()
        self.monitor = monitor
        lock.unlock()
        monitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            let signature = NativeNetworkPathSignature(path: path)
            guard let reason = self.record(signature) else { return }
            Task { @MainActor in
                onRecovery(reason)
            }
        }
        monitor.start(queue: queue)
        #endif
    }

    public func stop() {
        #if canImport(Network)
        let currentMonitor: NWPathMonitor?
        lock.lock()
        currentMonitor = monitor
        monitor = nil
        recoveryPolicy = NativeConnectivityRecoveryPolicy()
        lock.unlock()
        currentMonitor?.cancel()
        #endif
    }

    func record(_ signature: NativeNetworkPathSignature) -> String? {
        lock.lock()
        defer { lock.unlock() }
        return recoveryPolicy.record(signature)
    }
}

struct NativeConnectivityRecoveryPolicy: Sendable {
    private var lastSignature: NativeNetworkPathSignature?
    private var sawDisconnectedPath = false

    mutating func record(_ signature: NativeNetworkPathSignature) -> String? {
        let previous = lastSignature
        lastSignature = signature

        guard signature.isSatisfied else {
            sawDisconnectedPath = true
            return nil
        }

        guard let previous else {
            sawDisconnectedPath = false
            return nil
        }

        if sawDisconnectedPath || !previous.isSatisfied {
            sawDisconnectedPath = false
            return NativeMediaRecoveryReason.networkRecovered.rawValue
        }

        if previous.connectivityKey != signature.connectivityKey {
            return NativeMediaRecoveryReason.networkChanged.rawValue
        }

        return nil
    }
}

enum NativeMediaRecoveryReason: String, Sendable {
    case appForegrounded = "native-foreground"
    case audioInterruptionEnded = "native-audio-interruption-ended"
    case audioRouteChanged = "native-audio-route-change"
    case audioServicesReset = "native-audio-services-reset"
    case networkRecovered = "native-network-recovered"
    case networkChanged = "native-network-change"
}

public final class NativeAudioRecoveryMonitor: ObservableObject, @unchecked Sendable {
    private let center: NotificationCenter
    private let lock = NSLock()
    private let recoveryPolicy = NativeAudioRecoveryPolicy()
    private var tokens: [NSObjectProtocol] = []

    public init(center: NotificationCenter = .default) {
        self.center = center
    }

    deinit {
        stop()
    }

    public func start(onRecovery: @escaping @MainActor @Sendable (String) -> Void) {
        stop()
        #if os(iOS)
        let notificationNames: [Notification.Name] = [
            AVAudioSession.interruptionNotification,
            AVAudioSession.routeChangeNotification,
            AVAudioSession.mediaServicesWereResetNotification
        ]
        let newTokens = notificationNames.map { name in
            center.addObserver(forName: name, object: AVAudioSession.sharedInstance(), queue: nil) { [weak self] notification in
                guard let self, let event = self.audioEvent(from: notification) else { return }
                guard let reason = self.recoveryPolicy.recoveryReason(for: event) else { return }
                Task { @MainActor in
                    onRecovery(reason)
                }
            }
        }
        lock.lock()
        tokens = newTokens
        lock.unlock()
        #endif
    }

    public func stop() {
        let currentTokens: [NSObjectProtocol]
        lock.lock()
        currentTokens = tokens
        tokens = []
        lock.unlock()

        for token in currentTokens {
            center.removeObserver(token)
        }
    }

    #if os(iOS)
    private func audioEvent(from notification: Notification) -> NativeAudioRecoveryEvent? {
        switch notification.name {
        case AVAudioSession.interruptionNotification:
            return interruptionEvent(from: notification.userInfo ?? [:])
        case AVAudioSession.routeChangeNotification:
            return routeChangeEvent(from: notification.userInfo ?? [:])
        case AVAudioSession.mediaServicesWereResetNotification:
            return .mediaServicesReset
        default:
            return nil
        }
    }

    private func interruptionEvent(from userInfo: [AnyHashable: Any]) -> NativeAudioRecoveryEvent? {
        guard let rawValue = userInfo[AVAudioSessionInterruptionTypeKey] as? UInt,
              let type = AVAudioSession.InterruptionType(rawValue: rawValue) else {
            return nil
        }

        switch type {
        case .began:
            return .interruptionBegan
        case .ended:
            let optionsRawValue = userInfo[AVAudioSessionInterruptionOptionKey] as? UInt ?? 0
            let options = AVAudioSession.InterruptionOptions(rawValue: optionsRawValue)
            return .interruptionEnded(shouldResume: options.contains(.shouldResume))
        @unknown default:
            return nil
        }
    }

    private func routeChangeEvent(from userInfo: [AnyHashable: Any]) -> NativeAudioRecoveryEvent? {
        guard let rawValue = userInfo[AVAudioSessionRouteChangeReasonKey] as? UInt,
              let reason = AVAudioSession.RouteChangeReason(rawValue: rawValue) else {
            return .routeChanged
        }

        if reason == .categoryChange {
            return .routeCategoryChanged
        }
        return .routeChanged
    }
    #endif
}

struct NativeAudioRecoveryPolicy: Sendable {
    func recoveryReason(for event: NativeAudioRecoveryEvent) -> String? {
        switch event {
        case .interruptionBegan:
            return nil
        case .interruptionEnded(let shouldResume):
            return shouldResume ? NativeMediaRecoveryReason.audioInterruptionEnded.rawValue : nil
        case .mediaServicesReset:
            return NativeMediaRecoveryReason.audioServicesReset.rawValue
        case .routeCategoryChanged:
            return nil
        case .routeChanged:
            return NativeMediaRecoveryReason.audioRouteChanged.rawValue
        }
    }
}

enum NativeAudioRecoveryEvent: Equatable, Sendable {
    case interruptionBegan
    case interruptionEnded(shouldResume: Bool)
    case mediaServicesReset
    case routeCategoryChanged
    case routeChanged
}

struct NativeNetworkPathSignature: Equatable, Sendable {
    var status: NativeNetworkPathStatus
    var isExpensive: Bool
    var isConstrained: Bool
    var interfaces: Set<NativeNetworkInterface>

    init(
        status: NativeNetworkPathStatus,
        isExpensive: Bool = false,
        isConstrained: Bool = false,
        interfaces: Set<NativeNetworkInterface> = []
    ) {
        self.status = status
        self.isExpensive = isExpensive
        self.isConstrained = isConstrained
        self.interfaces = interfaces
    }

    var isSatisfied: Bool {
        status == .satisfied
    }

    var connectivityKey: ConnectivityKey {
        ConnectivityKey(isExpensive: isExpensive, isConstrained: isConstrained, interfaces: interfaces)
    }

    struct ConnectivityKey: Equatable, Sendable {
        var isExpensive: Bool
        var isConstrained: Bool
        var interfaces: Set<NativeNetworkInterface>
    }
}

enum NativeNetworkPathStatus: Equatable, Sendable {
    case satisfied
    case unsatisfied
    case requiresConnection
}

enum NativeNetworkInterface: String, Hashable, Sendable {
    case cellular
    case loopback
    case other
    case wiredEthernet
    case wifi
}

#if canImport(Network)
extension NativeNetworkPathSignature {
    init(path: NWPath) {
        self.init(
            status: NativeNetworkPathStatus(path.status),
            isExpensive: path.isExpensive,
            isConstrained: path.isConstrained,
            interfaces: NativeNetworkInterface.interfaces(for: path)
        )
    }
}

extension NativeNetworkPathStatus {
    init(_ status: NWPath.Status) {
        switch status {
        case .satisfied:
            self = .satisfied
        case .requiresConnection:
            self = .requiresConnection
        default:
            self = .unsatisfied
        }
    }
}

extension NativeNetworkInterface {
    static func interfaces(for path: NWPath) -> Set<NativeNetworkInterface> {
        var interfaces = Set<NativeNetworkInterface>()
        if path.usesInterfaceType(.cellular) {
            interfaces.insert(.cellular)
        }
        if path.usesInterfaceType(.loopback) {
            interfaces.insert(.loopback)
        }
        if path.usesInterfaceType(.other) {
            interfaces.insert(.other)
        }
        if path.usesInterfaceType(.wiredEthernet) {
            interfaces.insert(.wiredEthernet)
        }
        if path.usesInterfaceType(.wifi) {
            interfaces.insert(.wifi)
        }
        return interfaces
    }
}
#endif
