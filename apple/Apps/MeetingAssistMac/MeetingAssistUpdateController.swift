import Foundation
import Sparkle

public enum MeetingAssistUpdatePhase: Equatable, Sendable {
    case unconfigured
    case idle
    case checking
    case current
    case available(version: String)
    case downloading(version: String)
    case preparing(version: String)
    case ready(version: String)
    case installing(version: String)
    case failed(message: String)

    public var showsSidebarCard: Bool {
        switch self {
        case .unconfigured, .idle:
            false
        default:
            true
        }
    }

    public var isBusy: Bool {
        switch self {
        case .checking, .downloading, .preparing, .installing:
            true
        default:
            false
        }
    }
}

public struct MeetingAssistUpdateConfiguration: Equatable, Sendable {
    public let feedURL: URL?
    public let publicEDKey: String?
    public let installedVersion: String

    public var isConfigured: Bool {
        guard feedURL?.scheme?.lowercased() == "https",
              feedURL?.host?.isEmpty == false,
              let publicEDKey,
              let keyData = Data(base64Encoded: publicEDKey),
              keyData.count == 32 else { return false }
        return true
    }

    public init(feedURL: URL?, publicEDKey: String?, installedVersion: String) {
        self.feedURL = feedURL
        self.publicEDKey = publicEDKey?.trimmingCharacters(in: .whitespacesAndNewlines)
        self.installedVersion = installedVersion
    }

    public static func bundle(_ bundle: Bundle = .main) -> Self {
        let info = bundle.infoDictionary ?? [:]
        let feed = (info["SUFeedURL"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let key = (info["SUPublicEDKey"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let marketing = info["CFBundleShortVersionString"] as? String ?? "1.0"
        let build = info["CFBundleVersion"] as? String ?? "dev"
        return Self(
            feedURL: feed.isEmpty ? nil : URL(string: feed),
            publicEDKey: key.isEmpty ? nil : key,
            installedVersion: "\(marketing) (\(build))"
        )
    }
}

@MainActor
public final class MeetingAssistUpdateController: NSObject, ObservableObject {
    // Sparkle's public ObjC error enum imports without named Swift cases.
    private static let noUpdateErrorCode = 1001

    @Published public private(set) var phase: MeetingAssistUpdatePhase
    @Published public private(set) var canCheckForUpdates = false
    public let configuration: MeetingAssistUpdateConfiguration

    private var updaterController: SPUStandardUpdaterController?
    private var canCheckObservation: NSKeyValueObservation?
    private var transientReset: Task<Void, Never>?

    public init(
        configuration: MeetingAssistUpdateConfiguration = .bundle(),
        startUpdater: Bool = true,
        previewVersion: String? = ProcessInfo.processInfo.environment["STRIDE_UPDATE_PREVIEW_VERSION"]
    ) {
        self.configuration = configuration
        if let previewVersion, !previewVersion.isEmpty {
            phase = .available(version: previewVersion)
        } else {
            phase = configuration.isConfigured ? .idle : .unconfigured
        }
        super.init()

        guard configuration.isConfigured, previewVersion?.isEmpty != false else { return }
        let controller = SPUStandardUpdaterController(
            startingUpdater: startUpdater,
            updaterDelegate: self,
            userDriverDelegate: self
        )
        updaterController = controller
        canCheckObservation = controller.updater.observe(\.canCheckForUpdates, options: [.initial, .new]) { [weak self] updater, _ in
            Task { @MainActor in
                self?.canCheckForUpdates = updater.canCheckForUpdates
            }
        }
    }

    deinit {
        canCheckObservation?.invalidate()
        transientReset?.cancel()
    }

    public func checkForUpdates() {
        guard let updaterController else { return }
        transientReset?.cancel()
        if case .available = phase {
            // Sparkle re-focuses the existing update session and preserves the
            // truthful downloaded/installing stage when an update is pending.
        } else {
            phase = .checking
        }
        updaterController.checkForUpdates(nil)
    }

    private func version(for item: SUAppcastItem) -> String {
        let version = item.displayVersionString.trimmingCharacters(in: .whitespacesAndNewlines)
        return version.isEmpty ? item.versionString : version
    }

    private func showCurrentBriefly() {
        phase = .current
        transientReset?.cancel()
        transientReset = Task { [weak self] in
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled else { return }
            self?.phase = .idle
        }
    }

    private func showFailure(_ error: Error) {
        let message = (error as NSError).localizedDescription
            .trimmingCharacters(in: .whitespacesAndNewlines)
        phase = .failed(message: message.isEmpty ? "Try again in a moment." : message)
    }
}

extension MeetingAssistUpdateController: SPUUpdaterDelegate {
    public func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        // The standard user driver decides whether this is an immediate alert
        // or a gentle sidebar reminder in the callback below.
    }

    public func updaterDidNotFindUpdate(_ updater: SPUUpdater, error: Error) {
        showCurrentBriefly()
    }

    public func updater(_ updater: SPUUpdater, willDownloadUpdate item: SUAppcastItem, with request: NSMutableURLRequest) {
        let version = version(for: item)
        phase = .downloading(version: version)
    }

    public func updater(_ updater: SPUUpdater, didDownloadUpdate item: SUAppcastItem) {
        let version = version(for: item)
        phase = .preparing(version: version)
    }

    public func updater(_ updater: SPUUpdater, failedToDownloadUpdate item: SUAppcastItem, error: Error) {
        showFailure(error)
    }

    public func updater(_ updater: SPUUpdater, willExtractUpdate item: SUAppcastItem) {
        phase = .preparing(version: version(for: item))
    }

    public func updater(_ updater: SPUUpdater, didExtractUpdate item: SUAppcastItem) {
        phase = .ready(version: version(for: item))
    }

    public func updater(_ updater: SPUUpdater, willInstallUpdate item: SUAppcastItem) {
        phase = .installing(version: version(for: item))
    }

    public func updater(_ updater: SPUUpdater, didAbortWithError error: Error) {
        let nsError = error as NSError
        if nsError.domain == SUSparkleErrorDomain as String, nsError.code == Self.noUpdateErrorCode {
            showCurrentBriefly()
        } else {
            showFailure(error)
        }
    }

    public func updater(
        _ updater: SPUUpdater,
        didFinishUpdateCycleFor updateCheck: SPUUpdateCheck,
        error: Error?
    ) {
        guard let error else {
            if !phase.showsSidebarCard { phase = .idle }
            return
        }
        let nsError = error as NSError
        if nsError.domain == SUSparkleErrorDomain as String, nsError.code == Self.noUpdateErrorCode {
            showCurrentBriefly()
        } else if case .failed = phase {
            return
        } else {
            showFailure(error)
        }
    }
}

// Sparkle documents this delegate as main-thread-only. The binary module does
// not currently import that annotation into Swift 6, so preserve it here.
extension MeetingAssistUpdateController: @preconcurrency SPUStandardUserDriverDelegate {
    public var supportsGentleScheduledUpdateReminders: Bool { true }

    public func standardUserDriverShouldHandleShowingScheduledUpdate(
        _ update: SUAppcastItem,
        andInImmediateFocus immediateFocus: Bool
    ) -> Bool {
        // Critical releases keep Sparkle's immediate standard alert. Routine
        // releases wait politely in STRIDE's sidebar until the user chooses.
        update.isCriticalUpdate
    }

    public func standardUserDriverWillHandleShowingUpdate(
        _ handleShowingUpdate: Bool,
        forUpdate update: SUAppcastItem,
        state: SPUUserUpdateState
    ) {
        guard !handleShowingUpdate else { return }
        let version = version(for: update)
        phase = .available(version: version)
    }

    public func standardUserDriverWillFinishUpdateSession() {
        if case .installing = phase { return }
        phase = .idle
    }
}
