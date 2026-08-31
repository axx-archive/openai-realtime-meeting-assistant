import AppKit
import SwiftUI
import WebKit

public struct MeetingAssistWebView: NSViewRepresentable {
    public static let bridgeName = "strideShellState"

    public static let macPresentationScript = #"""
    document.documentElement.dataset.strideMac = "true";
    const style = document.createElement("style");
    style.id = "stride-mac-presentation";
    style.textContent = `
      html[data-stride-mac="true"] .login-rise,
      html[data-stride-mac="true"] .mount-stagger,
      html[data-stride-mac="true"] .bf-view,
      html[data-stride-mac="true"] .lobby__panel,
      html[data-stride-mac="true"] #officeTool,
      html[data-stride-mac="true"] #chatTool,
      html[data-stride-mac="true"] #memoryTool,
      html[data-stride-mac="true"] #filesTool,
      html[data-stride-mac="true"] #artifactsTool,
      html[data-stride-mac="true"] #researchTool,
      html[data-stride-mac="true"] .hearth-presentation {
        animation: none !important;
        opacity: 1 !important;
        transform: none !important;
      }

      @media (min-width: 480px) {
        html[data-stride-mac="true"] #appShell.is-authed {
          padding-left: 0 !important;
        }

        html[data-stride-mac="true"] #appShell.is-authed > #toolRail {
          display: none !important;
        }

        html[data-stride-mac="true"] #appShell.is-authed:not(.is-in-room) {
          --shell-topbar-height: 0px !important;
        }

        html[data-stride-mac="true"] #appShell.is-authed:not(.is-in-room) > .topbar {
          display: none !important;
        }

        html[data-stride-mac="true"] #appShell.is-authed.is-in-room > .topbar .topbar__back,
        html[data-stride-mac="true"] #appShell.is-authed.is-in-room > .topbar .topbar__brand-lockup,
        html[data-stride-mac="true"] #appShell.is-authed.is-in-room > .topbar .topbar__context-divider,
        html[data-stride-mac="true"] #appShell.is-authed.is-in-room > .topbar .topbar__bell,
        html[data-stride-mac="true"] #appShell.is-authed.is-in-room > .topbar .topbar__mobile-account {
          display: none !important;
        }

        html[data-stride-mac="true"] #appShell.is-authed.is-in-room[data-tool="room"] .meeting-bar {
          left: 50% !important;
        }
      }

      html[data-stride-mac="true"] .notification-panel {
        left: auto !important;
        right: 16px !important;
        top: 16px !important;
        bottom: auto !important;
        width: min(404px, calc(100vw - 32px));
        transform-origin: right top;
      }
    `;
    document.head.appendChild(style);

    (() => {
      const handler = window.webkit?.messageHandlers?.strideShellState;
      if (!handler) return;
      let scheduled = false;
      const text = (selector, fallback = "") =>
        document.querySelector(selector)?.textContent?.trim() || fallback;
      const postState = () => {
        scheduled = false;
        const shell = document.getElementById("appShell");
        const badge = document.getElementById("notificationBadge");
        const liveGroup = document.getElementById("topbarLiveGroup");
        handler.postMessage({
          version: 1,
          authenticated: Boolean(shell?.classList.contains("is-authed")),
          destination: shell?.dataset.pd1Destination || "Home",
          organization: text("#topbarOrganizationName", "STRIDE"),
          connection: text("#statusText", "Connecting"),
          unreadCount: Number.parseInt(badge?.textContent || "0", 10) || 0,
          theme: document.documentElement.dataset.theme === "dark" ? "dark" : "light",
          hasLiveRoom: Boolean(liveGroup && !liveGroup.hidden),
          isInRoom: Boolean(shell?.classList.contains("is-in-room")),
          accountName: text("#topbarAccountName", "Settings")
        });
      };
      const schedule = () => {
        if (scheduled) return;
        scheduled = true;
        requestAnimationFrame(postState);
      };
      const observed = [
        document.documentElement,
        document.getElementById("appShell"),
        document.getElementById("topbarOrganizationName"),
        document.getElementById("statusText"),
        document.getElementById("notificationBadge"),
        document.getElementById("topbarLiveGroup"),
        document.getElementById("topbarAccountName")
      ].filter(Boolean);
      const observer = new MutationObserver(schedule);
      observed.forEach(node => observer.observe(node, {
        attributes: true,
        attributeFilter: ["class", "data-pd1-destination", "data-theme", "hidden"],
        childList: true,
        characterData: true,
        subtree: true
      }));
      schedule();
    })();
    """#

    @ObservedObject var session: MeetingAssistWebSession

    public init(session: MeetingAssistWebSession) {
        self.session = session
    }

    public func makeCoordinator() -> Coordinator {
        Coordinator(session: session)
    }

    public func makeNSView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .default()
        configuration.defaultWebpagePreferences.allowsContentJavaScript = true
        configuration.mediaTypesRequiringUserActionForPlayback = []
        configuration.applicationNameForUserAgent = "STRIDE-macOS/1.0"
        configuration.preferences.isElementFullscreenEnabled = true
        configuration.userContentController.add(context.coordinator, name: Self.bridgeName)
        configuration.userContentController.addUserScript(
            WKUserScript(
                source: Self.macPresentationScript,
                injectionTime: .atDocumentEnd,
                forMainFrameOnly: true
            )
        )

        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = context.coordinator
        webView.uiDelegate = context.coordinator
        webView.allowsBackForwardNavigationGestures = true
        webView.allowsMagnification = true

        context.coordinator.observe(webView)
        session.attach(webView)
        return webView
    }

    public func updateNSView(_ webView: WKWebView, context: Context) {
        context.coordinator.session = session
    }

    public static func dismantleNSView(_ nsView: WKWebView, coordinator: Coordinator) {
        nsView.configuration.userContentController.removeScriptMessageHandler(forName: bridgeName)
    }

    @MainActor
    public final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate, WKScriptMessageHandler {
        var session: MeetingAssistWebSession
        private var observations: [NSKeyValueObservation] = []
        private var activeDownloads: [ObjectIdentifier: WKDownload] = [:]

        init(session: MeetingAssistWebSession) {
            self.session = session
        }

        func observe(_ webView: WKWebView) {
            observations = [
                webView.observe(\.estimatedProgress, options: [.new]) { [weak self, weak webView] _, _ in
                    Task { @MainActor in self?.session.update(from: webView) }
                },
                webView.observe(\.title, options: [.new]) { [weak self, weak webView] _, _ in
                    Task { @MainActor in self?.session.update(from: webView) }
                },
                webView.observe(\.url, options: [.new]) { [weak self, weak webView] _, _ in
                    Task { @MainActor in self?.session.update(from: webView) }
                }
            ]
        }

        public func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
            guard message.name == MeetingAssistWebView.bridgeName,
                  message.frameInfo.isMainFrame else { return }
            let host = message.frameInfo.securityOrigin.host.lowercased()
            guard host == MeetingAssistWebSession.trustedHost || host.hasSuffix(".\(MeetingAssistWebSession.trustedHost)"),
                  let state = MeetingAssistShellState(messageBody: message.body) else { return }
            session.updateShellState(state)
        }

        public func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation?) {
            session.navigationStarted()
        }

        public func webView(_ webView: WKWebView, didFinish navigation: WKNavigation?) {
            session.navigationFinished(webView)
        }

        public func webView(
            _ webView: WKWebView,
            didFail navigation: WKNavigation?,
            withError error: Error
        ) {
            session.navigationFailed(error, webView: webView)
        }

        public func webView(
            _ webView: WKWebView,
            didFailProvisionalNavigation navigation: WKNavigation?,
            withError error: Error
        ) {
            session.navigationFailed(error, webView: webView)
        }

        public func webView(
            _ webView: WKWebView,
            decidePolicyFor navigationAction: WKNavigationAction,
            decisionHandler: @escaping @MainActor @Sendable (WKNavigationActionPolicy) -> Void
        ) {
            guard let url = navigationAction.request.url else {
                decisionHandler(.cancel)
                return
            }

            switch MeetingAssistWebSession.navigationDisposition(for: url) {
            case .inApp:
                if navigationAction.targetFrame == nil {
                    webView.load(navigationAction.request)
                    decisionHandler(.cancel)
                } else {
                    decisionHandler(.allow)
                }
            case .external:
                session.openExternally(url)
                decisionHandler(.cancel)
            case .blocked:
                decisionHandler(.cancel)
            }
        }

        public func webView(
            _ webView: WKWebView,
            decidePolicyFor navigationResponse: WKNavigationResponse,
            decisionHandler: @escaping @MainActor @Sendable (WKNavigationResponsePolicy) -> Void
        ) {
            let response = navigationResponse.response as? HTTPURLResponse
            let disposition = response?.value(forHTTPHeaderField: "Content-Disposition")?.lowercased() ?? ""
            if disposition.contains("attachment") || !navigationResponse.canShowMIMEType {
                decisionHandler(.download)
            } else {
                decisionHandler(.allow)
            }
        }

        public func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
            retain(download)
        }

        public func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
            retain(download)
        }

        public func webView(
            _ webView: WKWebView,
            requestMediaCapturePermissionFor origin: WKSecurityOrigin,
            initiatedByFrame frame: WKFrameInfo,
            type: WKMediaCaptureType,
            decisionHandler: @escaping @MainActor @Sendable (WKPermissionDecision) -> Void
        ) {
            let host = origin.host.lowercased()
            let trusted = host == MeetingAssistWebSession.trustedHost || host.hasSuffix(".\(MeetingAssistWebSession.trustedHost)")
            decisionHandler(trusted && session.webKitMediaCaptureAllowed ? .grant : .deny)
        }

        public func webView(
            _ webView: WKWebView,
            createWebViewWith configuration: WKWebViewConfiguration,
            for navigationAction: WKNavigationAction,
            windowFeatures: WKWindowFeatures
        ) -> WKWebView? {
            guard let url = navigationAction.request.url else { return nil }
            switch MeetingAssistWebSession.navigationDisposition(for: url) {
            case .inApp:
                webView.load(navigationAction.request)
            case .external:
                session.openExternally(url)
            case .blocked:
                break
            }
            return nil
        }

        public func download(
            _ download: WKDownload,
            decideDestinationUsing response: URLResponse,
            suggestedFilename: String
        ) async -> URL? {
            let panel = NSSavePanel()
            panel.nameFieldStringValue = suggestedFilename
            panel.canCreateDirectories = true
            panel.isExtensionHidden = false
            return panel.runModal() == .OK ? panel.url : nil
        }

        public func downloadDidFinish(_ download: WKDownload) {
            activeDownloads.removeValue(forKey: ObjectIdentifier(download))
        }

        public func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
            activeDownloads.removeValue(forKey: ObjectIdentifier(download))
        }

        private func retain(_ download: WKDownload) {
            download.delegate = self
            activeDownloads[ObjectIdentifier(download)] = download
        }
    }
}
