import Foundation
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
    case networkRecovered = "native-network-recovered"
    case networkChanged = "native-network-change"
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
