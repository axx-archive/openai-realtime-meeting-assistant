import XCTest
import MeetingAssistMac

@MainActor
final class MeetingAssistMacAppTests: XCTestCase {
    func testMacRootViewBuildsAgainstCurrentWebWorkspace() {
        _ = MeetingAssistMacRootView()
        XCTAssertEqual(MeetingAssistWebSession.productionAppURL.absoluteString, "https://thebonfire.xyz/index.html")
        XCTAssertEqual(
            MeetingAssistWorkspace.allCases.map(\.url.absoluteString),
            [
                "https://thebonfire.xyz/index.html",
                "https://thebonfire.xyz/video",
                "https://thebonfire.xyz/conversations",
                "https://thebonfire.xyz/work",
                "https://thebonfire.xyz/drive"
            ]
        )
    }

    func testTrustedWorkspaceNavigationStaysInsideApp() {
        XCTAssertEqual(
            MeetingAssistWebSession.navigationDisposition(for: URL(string: "https://thebonfire.xyz/index.html?bell=1")!),
            .inApp
        )
        XCTAssertEqual(
            MeetingAssistWebSession.navigationDisposition(for: URL(string: "https://media.thebonfire.xyz/artifact/1")!),
            .inApp
        )
    }

    func testExternalAndUnsafeNavigationLeaveOrStopAtNativeBoundary() {
        XCTAssertEqual(
            MeetingAssistWebSession.navigationDisposition(for: URL(string: "https://developer.apple.com/")!),
            .external
        )
        XCTAssertEqual(
            MeetingAssistWebSession.navigationDisposition(for: URL(string: "javascript:alert(1)")!),
            .blocked
        )
    }

    func testEmptyOrLegacyPageTitlesResolveToStride() {
        XCTAssertEqual(MeetingAssistWebSession.cleanPageTitle(nil), "STRIDE")
        XCTAssertEqual(MeetingAssistWebSession.cleanPageTitle("  "), "STRIDE")
        XCTAssertEqual(MeetingAssistWebSession.cleanPageTitle("MeetingAssist"), "STRIDE")
        XCTAssertEqual(MeetingAssistWebSession.cleanPageTitle("Scout — STRIDE"), "Scout — STRIDE")
    }

    func testWorkspacePresentationAndRouteParsingStayInSync() {
        XCTAssertEqual(MeetingAssistWorkspace.allCases.map(\.title), ["Home", "Rooms", "Conversations", "Work", "Drive"])
        XCTAssertEqual(MeetingAssistWorkspace(destination: "Video"), .rooms)
        XCTAssertEqual(MeetingAssistWorkspace(destination: "rooms"), .rooms)
        XCTAssertEqual(MeetingAssistWorkspace(url: URL(string: "https://thebonfire.xyz/work")!), .work)
        XCTAssertNil(MeetingAssistWorkspace(destination: "Unknown"))
    }

    func testVersionedShellStateParsingIsDefensive() {
        let state = MeetingAssistShellState(messageBody: [
            "version": 1,
            "authenticated": true,
            "destination": "Conversations",
            "organization": " Bonfire ",
            "connection": "ready",
            "unreadCount": 1_500,
            "theme": "light",
            "hasLiveRoom": true,
            "isInRoom": false,
            "accountName": " AJ "
        ])

        XCTAssertEqual(state?.workspace, .conversations)
        XCTAssertEqual(state?.organization, "Bonfire")
        XCTAssertEqual(state?.unreadCount, 999)
        XCTAssertEqual(state?.resolvedTheme, .light)
        XCTAssertEqual(state?.accountName, "AJ")
        XCTAssertNil(MeetingAssistShellState(messageBody: ["version": 2]))
        XCTAssertNil(MeetingAssistShellState(messageBody: "not a message"))
    }

    func testMacPresentationSuppressesOnlyAuthenticatedDuplicateChrome() {
        let script = MeetingAssistWebView.macPresentationScript
        XCTAssertTrue(script.contains("#appShell.is-authed > #toolRail"))
        XCTAssertTrue(script.contains("#appShell.is-authed:not(.is-in-room) > .topbar"))
        XCTAssertTrue(script.contains("#appShell.is-authed.is-in-room > .topbar"))
        XCTAssertFalse(script.contains("#appShell:not(.is-authed) > .topbar"))
    }

    func testNativeActionsMapToFixedWhitelistedScripts() {
        XCTAssertTrue(MeetingAssistWebSession.script(for: .notifications).contains("notificationBell"))
        XCTAssertTrue(MeetingAssistWebSession.script(for: .toggleAppearance).contains("themeToggle"))
        XCTAssertTrue(MeetingAssistWebSession.script(for: .profileSettings).contains("topbarMobileAccount"))
        XCTAssertTrue(MeetingAssistWebSession.script(for: .organizationSettings).contains("organizations"))
        XCTAssertTrue(MeetingAssistWebSession.script(for: .returnToLiveRoom).contains("topbarLivePill"))
    }

    func testNativeMediaOwnershipFailsClosedWithoutAnAttachedWebView() async {
        let session = MeetingAssistWebSession()
        XCTAssertEqual(session.mediaOwner, .webKit)
        XCTAssertTrue(session.webKitMediaCaptureAllowed)

        // With no attached WKWebView, native initialization fails closed and
        // WebKit remains the only permitted capture owner.
        let result = await session.requestNativeMediaOwnership()
        XCTAssertEqual(result, .webViewUnavailable)
        XCTAssertEqual(session.mediaOwner, .webKit)
        XCTAssertTrue(session.webKitMediaCaptureAllowed)
    }

    func testUpdateConfigurationRequiresHTTPSAndAnEd25519PublicKey() {
        let publicKey = Data(repeating: 7, count: 32).base64EncodedString()
        XCTAssertTrue(
            MeetingAssistUpdateConfiguration(
                feedURL: URL(string: "https://thebonfire.xyz/downloads/stride/appcast.xml"),
                publicEDKey: publicKey,
                installedVersion: "1.0 (19)"
            ).isConfigured
        )
        XCTAssertFalse(
            MeetingAssistUpdateConfiguration(
                feedURL: URL(string: "http://thebonfire.xyz/appcast.xml"),
                publicEDKey: publicKey,
                installedVersion: "1.0 (19)"
            ).isConfigured
        )
        XCTAssertFalse(
            MeetingAssistUpdateConfiguration(
                feedURL: URL(string: "https://thebonfire.xyz/appcast.xml"),
                publicEDKey: Data(repeating: 7, count: 31).base64EncodedString(),
                installedVersion: "1.0 (19)"
            ).isConfigured
        )
    }

    func testUpdaterPhasesExposeOnlyTruthfulSidebarStates() {
        XCTAssertFalse(MeetingAssistUpdatePhase.unconfigured.showsSidebarCard)
        XCTAssertFalse(MeetingAssistUpdatePhase.idle.showsSidebarCard)
        XCTAssertTrue(MeetingAssistUpdatePhase.available(version: "1.1").showsSidebarCard)
        XCTAssertTrue(MeetingAssistUpdatePhase.downloading(version: "1.1").isBusy)
        XCTAssertFalse(MeetingAssistUpdatePhase.ready(version: "1.1").isBusy)
    }

    func testUnconfiguredLocalUpdaterDoesNotStartSparkle() {
        let updates = MeetingAssistUpdateController(
            configuration: MeetingAssistUpdateConfiguration(
                feedURL: nil,
                publicEDKey: nil,
                installedVersion: "1.0 (19)"
            )
        )
        XCTAssertEqual(updates.phase, .unconfigured)
        XCTAssertFalse(updates.canCheckForUpdates)
    }
}
