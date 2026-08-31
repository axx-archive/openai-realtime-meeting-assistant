import AppKit
import Foundation
import WebKit

public enum MeetingAssistNavigationDisposition: Equatable, Sendable {
    case inApp
    case external
    case blocked
}

public enum MeetingAssistWorkspace: String, CaseIterable, Identifiable, Sendable {
    case home = "/index.html"
    case rooms = "/video"
    case conversations = "/conversations"
    case work = "/work"
    case drive = "/drive"

    public var id: Self { self }

    public var title: String {
        switch self {
        case .home: "Home"
        case .rooms: "Rooms"
        case .conversations: "Conversations"
        case .work: "Work"
        case .drive: "Drive"
        }
    }

    public var systemImage: String {
        switch self {
        case .home: "house"
        case .rooms: "video"
        case .conversations: "bubble.left.and.bubble.right"
        case .work: "checklist"
        case .drive: "square.stack.3d.up"
        }
    }

    public var url: URL {
        URL(string: "https://thebonfire.xyz\(rawValue)")!
    }

    public init?(destination: String) {
        switch destination.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "home": self = .home
        case "video", "rooms": self = .rooms
        case "conversations": self = .conversations
        case "work": self = .work
        case "drive": self = .drive
        default: return nil
        }
    }

    public init?(url: URL) {
        let path = url.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        switch path.lowercased() {
        case "", "index.html": self = .home
        case "video": self = .rooms
        case "conversations": self = .conversations
        case "work": self = .work
        case "drive": self = .drive
        default: return nil
        }
    }
}

public enum MeetingAssistResolvedTheme: String, Equatable, Sendable {
    case light
    case dark
}

public enum MeetingAssistWebAction: Equatable, Sendable {
    case notifications
    case toggleAppearance
    case profileSettings
    case organizationSettings
    case returnToLiveRoom
}

/// The mutually-exclusive capture owner for the Mac shell. Ownership is chosen
/// before joining and never migrates while a call is active.
public enum MeetingAssistMediaOwner: String, Equatable, Sendable {
    case webKit
    case nativePreparing
    case nativeActive
}

public enum MeetingAssistNativeMediaOwnershipResult: Equatable, Sendable {
    case acquired
    case alreadyNative
    case webMeetingActive
    case webViewUnavailable
    case webCaptureDidNotStop
}

public struct MeetingAssistShellState: Equatable, Sendable {
    public var isAuthenticated: Bool
    public var workspace: MeetingAssistWorkspace?
    public var organization: String
    public var connectionStatus: String
    public var unreadCount: Int
    public var resolvedTheme: MeetingAssistResolvedTheme
    public var hasLiveRoom: Bool
    public var isInRoom: Bool
    public var accountName: String

    public static let initial = MeetingAssistShellState(
        isAuthenticated: false,
        workspace: .home,
        organization: "STRIDE",
        connectionStatus: "Connecting",
        unreadCount: 0,
        resolvedTheme: .dark,
        hasLiveRoom: false,
        isInRoom: false,
        accountName: "Settings"
    )

    public init(
        isAuthenticated: Bool,
        workspace: MeetingAssistWorkspace?,
        organization: String,
        connectionStatus: String,
        unreadCount: Int,
        resolvedTheme: MeetingAssistResolvedTheme,
        hasLiveRoom: Bool,
        isInRoom: Bool,
        accountName: String
    ) {
        self.isAuthenticated = isAuthenticated
        self.workspace = workspace
        self.organization = organization
        self.connectionStatus = connectionStatus
        self.unreadCount = min(max(unreadCount, 0), 999)
        self.resolvedTheme = resolvedTheme
        self.hasLiveRoom = hasLiveRoom
        self.isInRoom = isInRoom
        self.accountName = accountName
    }

    public init?(messageBody: Any) {
        guard let body = messageBody as? [String: Any], body["version"] as? Int == 1 else { return nil }
        let theme = (body["theme"] as? String).flatMap(MeetingAssistResolvedTheme.init(rawValue:)) ?? .dark

        self.init(
            isAuthenticated: body["authenticated"] as? Bool ?? false,
            workspace: (body["destination"] as? String).flatMap(MeetingAssistWorkspace.init(destination:)),
            organization: Self.clean(body["organization"] as? String, fallback: "STRIDE"),
            connectionStatus: Self.clean(body["connection"] as? String, fallback: "Connecting"),
            unreadCount: body["unreadCount"] as? Int ?? 0,
            resolvedTheme: theme,
            hasLiveRoom: body["hasLiveRoom"] as? Bool ?? false,
            isInRoom: body["isInRoom"] as? Bool ?? false,
            accountName: Self.clean(body["accountName"] as? String, fallback: "Settings")
        )
    }

    private static func clean(_ value: String?, fallback: String) -> String {
        let normalized = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return normalized.isEmpty ? fallback : normalized
    }
}

public struct MeetingAssistWebFailure: Equatable, Sendable {
    public var title: String
    public var message: String

    public init(title: String, message: String) {
        self.title = title
        self.message = message
    }
}

@MainActor
public final class MeetingAssistWebSession: ObservableObject {
    public static let productionAppURL = URL(string: "https://thebonfire.xyz/index.html")!
    public static let trustedHost = "thebonfire.xyz"

    @Published public private(set) var pageTitle = "STRIDE"
    @Published public private(set) var currentURL = productionAppURL
    @Published public private(set) var canGoBack = false
    @Published public private(set) var canGoForward = false
    @Published public private(set) var isLoading = true
    @Published public private(set) var estimatedProgress = 0.05
    @Published public private(set) var failure: MeetingAssistWebFailure?
    @Published public private(set) var shellState = MeetingAssistShellState.initial
    @Published public private(set) var mediaOwner = MeetingAssistMediaOwner.webKit

    weak var webView: WKWebView?

    public init() {}

    public var currentWorkspace: MeetingAssistWorkspace {
        shellState.workspace ?? MeetingAssistWorkspace(url: currentURL) ?? .home
    }

    public var webKitMediaCaptureAllowed: Bool {
        mediaOwner == .webKit
    }

    /// Stops any existing WebKit capture and verifies that it is fully released
    /// before the native room is allowed to initialize capture.
    public func requestNativeMediaOwnership() async -> MeetingAssistNativeMediaOwnershipResult {
        switch mediaOwner {
        case .nativePreparing, .nativeActive:
            return .alreadyNative
        case .webKit:
            break
        }

        guard !shellState.isInRoom else {
            return .webMeetingActive
        }
        guard let webView else {
            return .webViewUnavailable
        }

        mediaOwner = .nativePreparing
        await webView.setCameraCaptureState(.none)
        await webView.setMicrophoneCaptureState(.none)

        guard self.webView === webView,
              mediaOwner == .nativePreparing,
              webView.cameraCaptureState == .none,
              webView.microphoneCaptureState == .none else {
            mediaOwner = .webKit
            return .webCaptureDidNotStop
        }
        return .acquired
    }

    @discardableResult
    public func markNativeMediaActive() -> Bool {
        guard mediaOwner == .nativePreparing else { return false }
        mediaOwner = .nativeActive
        return true
    }

    /// Releasing an active native owner requires proof from the caller that the
    /// native session has ended. This prevents a silent mid-call fallback.
    @discardableResult
    public func releaseNativeMediaOwnership(nativeSessionEnded: Bool) -> Bool {
        switch mediaOwner {
        case .webKit:
            return true
        case .nativePreparing:
            mediaOwner = .webKit
            return true
        case .nativeActive:
            guard nativeSessionEnded else { return false }
            mediaOwner = .webKit
            return true
        }
    }

    func attach(_ webView: WKWebView) {
        guard self.webView !== webView else { return }
        self.webView = webView
        loadHome()
    }

    public func loadHome() {
        load(.home)
    }

    public func load(_ workspace: MeetingAssistWorkspace) {
        failure = nil
        shellState.workspace = workspace
        let request = URLRequest(
            url: workspace.url,
            cachePolicy: .reloadRevalidatingCacheData,
            timeoutInterval: 30
        )
        webView?.load(request)
    }

    public func reload() {
        failure = nil
        if webView?.isLoading == true {
            webView?.stopLoading()
            update(from: webView)
        } else if webView?.url == nil {
            loadHome()
        } else {
            webView?.reloadFromOrigin()
        }
    }

    public func goBack() {
        guard webView?.canGoBack == true else { return }
        webView?.goBack()
    }

    public func goForward() {
        guard webView?.canGoForward == true else { return }
        webView?.goForward()
    }

    public func openCurrentPageInBrowser() {
        let candidate = webView?.url ?? currentURL
        guard Self.navigationDisposition(for: candidate) != .blocked else { return }
        NSWorkspace.shared.open(candidate)
    }

    public func perform(_ action: MeetingAssistWebAction) {
        guard let webView else { return }
        webView.evaluateJavaScript(Self.script(for: action))
    }

    func openExternally(_ url: URL) {
        guard Self.navigationDisposition(for: url) == .external else { return }
        NSWorkspace.shared.open(url)
    }

    func navigationStarted() {
        isLoading = true
        estimatedProgress = max(estimatedProgress, 0.08)
        failure = nil
    }

    func navigationFinished(_ webView: WKWebView) {
        failure = nil
        update(from: webView)
    }

    func navigationFailed(_ error: Error, webView: WKWebView) {
        update(from: webView)

        let nsError = error as NSError
        guard nsError.code != NSURLErrorCancelled else { return }
        failure = MeetingAssistWebFailure(
            title: "STRIDE is out of reach",
            message: Self.displayMessage(for: nsError)
        )
    }

    func update(from webView: WKWebView?) {
        guard let webView else { return }
        currentURL = webView.url ?? currentURL
        pageTitle = Self.cleanPageTitle(webView.title)
        canGoBack = webView.canGoBack
        canGoForward = webView.canGoForward
        isLoading = webView.isLoading
        estimatedProgress = webView.isLoading ? max(0.05, webView.estimatedProgress) : 1
        if let workspace = MeetingAssistWorkspace(url: currentURL), shellState.workspace == nil {
            shellState.workspace = workspace
        }
    }

    func updateShellState(_ state: MeetingAssistShellState) {
        shellState = state
    }

    public static func navigationDisposition(for url: URL) -> MeetingAssistNavigationDisposition {
        guard let scheme = url.scheme?.lowercased() else { return .blocked }

        switch scheme {
        case "about", "blob", "data":
            return .inApp
        case "https":
            guard let host = url.host?.lowercased() else { return .blocked }
            return host == trustedHost || host.hasSuffix(".\(trustedHost)") ? .inApp : .external
        case "http", "mailto", "tel":
            return .external
        default:
            return .blocked
        }
    }

    public static func cleanPageTitle(_ title: String?) -> String {
        let normalized = title?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !normalized.isEmpty else { return "STRIDE" }
        return normalized.caseInsensitiveCompare("meetingassist") == .orderedSame ? "STRIDE" : normalized
    }

    public static func script(for action: MeetingAssistWebAction) -> String {
        switch action {
        case .notifications:
            "document.getElementById('notificationBell')?.click();"
        case .toggleAppearance:
            "document.getElementById('themeToggle')?.click();"
        case .profileSettings:
            "document.getElementById('topbarMobileAccount')?.click();"
        case .organizationSettings:
            "document.getElementById('topbarMobileAccount')?.click(); requestAnimationFrame(() => document.querySelector('[data-settings-section=organizations]')?.click());"
        case .returnToLiveRoom:
            "document.getElementById('topbarLivePill')?.click();"
        }
    }

    private static func displayMessage(for error: NSError) -> String {
        switch error.code {
        case NSURLErrorNotConnectedToInternet:
            return "Check your internet connection, then try again."
        case NSURLErrorTimedOut:
            return "The workspace took too long to respond. Try again in a moment."
        case NSURLErrorCannotFindHost, NSURLErrorCannotConnectToHost:
            return "The STRIDE workspace could not be reached."
        default:
            return "The workspace could not be loaded. Try again, or continue in your browser."
        }
    }
}

public extension Notification.Name {
    static let strideGoHome = Notification.Name("co.thebonfire.stride.command.home")
    static let strideGoRooms = Notification.Name("co.thebonfire.stride.command.rooms")
    static let strideGoConversations = Notification.Name("co.thebonfire.stride.command.conversations")
    static let strideGoWork = Notification.Name("co.thebonfire.stride.command.work")
    static let strideGoDrive = Notification.Name("co.thebonfire.stride.command.drive")
    static let strideReload = Notification.Name("co.thebonfire.stride.command.reload")
    static let strideGoBack = Notification.Name("co.thebonfire.stride.command.back")
    static let strideGoForward = Notification.Name("co.thebonfire.stride.command.forward")
    static let strideOpenInBrowser = Notification.Name("co.thebonfire.stride.command.open-in-browser")
    static let strideCheckForUpdates = Notification.Name("co.thebonfire.stride.command.check-for-updates")
}
