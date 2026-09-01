import SwiftUI
import MeetingAssistCore
import MeetingAssistRoomUI

private enum MeetingAssistMacSurface: Equatable {
    case web
    case nativeMedia
}

public struct MeetingAssistMacRootView: View {
    @StateObject private var session: MeetingAssistWebSession
    @StateObject private var updates: MeetingAssistUpdateController
    @StateObject private var nativeRoom: NativeRoomViewModel
    @State private var surface = MeetingAssistMacSurface.web
    @State private var nativeMediaNotice: String?
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency
    @Environment(\.colorSchemeContrast) private var colorSchemeContrast

    private let ember = Color(red: 1, green: 90 / 255, blue: 25 / 255)
    private let shellBlack = Color(nsColor: NSColor(calibratedWhite: 0.012, alpha: 1))

    public init(
        session: MeetingAssistWebSession = MeetingAssistWebSession(),
        updates: MeetingAssistUpdateController = MeetingAssistUpdateController(),
        nativeRoom: NativeRoomViewModel = NativeRoomViewModel()
    ) {
        _session = StateObject(wrappedValue: session)
        _updates = StateObject(wrappedValue: updates)
        _nativeRoom = StateObject(wrappedValue: nativeRoom)
    }

    public var body: some View {
        shell
            .onReceive(NotificationCenter.default.publisher(for: .strideGoHome)) { _ in
                openWebWorkspace(.home)
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideGoRooms)) { _ in
                openWebWorkspace(.rooms)
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideGoConversations)) { _ in
                openWebWorkspace(.conversations)
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideGoWork)) { _ in
                openWebWorkspace(.work)
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideGoDrive)) { _ in
                openWebWorkspace(.drive)
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideReload)) { _ in
                session.reload()
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideGoBack)) { _ in
                session.goBack()
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideGoForward)) { _ in
                session.goForward()
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideOpenInBrowser)) { _ in
                session.openCurrentPageInBrowser()
            }
            .onReceive(NotificationCenter.default.publisher(for: .strideCheckForUpdates)) { _ in
                updates.checkForUpdates()
            }
            .onChange(of: nativeRoom.lifecycle) { previous, current in
                nativeLifecycleChanged(from: previous, to: current)
            }
            .alert(
                "Native media",
                isPresented: Binding(
                    get: { nativeMediaNotice != nil },
                    set: { if !$0 { nativeMediaNotice = nil } }
                )
            ) {
                Button("OK", role: .cancel) { nativeMediaNotice = nil }
            } message: {
                Text(nativeMediaNotice ?? "")
            }
    }

    private var shell: some View {
        NavigationSplitView {
            MacSidebar(
                session: session,
                updates: updates,
                isNativeMediaSelected: surface == .nativeMedia,
                canOpenNativeMedia: !session.shellState.isInRoom,
                onSelectWorkspace: openWebWorkspace,
                onOpenNativeMedia: requestNativeMediaSurface
            )
                .navigationSplitViewColumnWidth(min: 184, ideal: 218, max: 260)
        } detail: {
            detailSurface
                .navigationSplitViewColumnWidth(min: 640, ideal: 1_000)
        }
        .navigationSplitViewStyle(.balanced)
        .tint(ember)
        .preferredColorScheme(.dark)
        .background(shellBlack)
        .background(WindowChromeConfigurator())
        .toolbarBackground(shellBlack, for: .windowToolbar)
        .toolbarBackground(.visible, for: .windowToolbar)
        .toolbar {
            ToolbarItemGroup(placement: .navigation) {
                Button {
                    session.goBack()
                } label: {
                    Image(systemName: "chevron.backward")
                }
                .help("Back")
                .disabled(surface != .web || !session.canGoBack)
                .accessibilityLabel("Go back")

                Button {
                    session.goForward()
                } label: {
                    Image(systemName: "chevron.forward")
                }
                .help("Forward")
                .disabled(surface != .web || !session.canGoForward)
                .accessibilityLabel("Go forward")
            }
        }
    }

    private var detailSurface: some View {
        ZStack {
            webSurface
                .opacity(surface == .web ? 1 : 0)
                .allowsHitTesting(surface == .web)
                .accessibilityHidden(surface != .web)

            if surface == .nativeMedia {
                nativeMediaSurface
                    .transition(.opacity)
            }
        }
    }

    private var nativeMediaSurface: some View {
        ZStack {
            shellBlack
            RadialGradient(
                colors: [ember.opacity(0.08), .clear],
                center: .topTrailing,
                startRadius: 12,
                endRadius: 430
            )

            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    Label("Native media owner", systemImage: "waveform.badge.mic")
                        .font(.subheadline.weight(.semibold))
                    Text(session.mediaOwner == .nativeActive ? "in call" : "ready to join")
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Return to web") {
                        returnToWebWithoutJoining()
                    }
                    .disabled(nativeRoom.isBusy || nativeRoom.canUseRoomControls || session.mediaOwner == .nativeActive)
                    .help("Leave the native room before returning to the web meeting path")
                }
                .padding(.horizontal, 18)
                .frame(minHeight: 48)

                Divider()

                NativeRoomView(model: nativeRoom)
            }
            .background(.black.opacity(0.18), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .strokeBorder(.white.opacity(0.10), lineWidth: 1)
            }
            .padding(.top, 8)
            .padding(.trailing, 12)
            .padding(.bottom, 12)
            .padding(.leading, 10)
        }
        .navigationTitle("")
    }

    private func requestNativeMediaSurface() {
        Task { @MainActor in
            let result = await session.requestNativeMediaOwnership()
            switch result {
            case .acquired, .alreadyNative:
                withAnimation(.easeOut(duration: 0.16)) {
                    surface = .nativeMedia
                }
            case .webMeetingActive:
                nativeMediaNotice = "Leave the current web meeting before choosing native media. STRIDE never runs both capture owners at once."
            case .webViewUnavailable:
                nativeMediaNotice = "The web workspace is not ready. Native media was not started; the web path remains available."
            case .webCaptureDidNotStop:
                nativeMediaNotice = "Web capture did not release cleanly. Native media was not started."
            }
        }
    }

    private func openWebWorkspace(_ workspace: MeetingAssistWorkspace) {
        if surface == .nativeMedia {
            guard !nativeRoom.isBusy,
                  !nativeRoom.canUseRoomControls,
                  session.mediaOwner != .nativeActive else {
                nativeMediaNotice = "Leave the native room before returning to the web workspace. Media ownership never changes mid-call."
                return
            }
            Task { @MainActor in
                await nativeRoom.leave()
                _ = session.releaseNativeMediaOwnership(nativeSessionEnded: true)
                surface = .web
                session.load(workspace)
            }
            return
        }
        session.load(workspace)
    }

    private func returnToWebWithoutJoining() {
        openWebWorkspace(session.currentWorkspace)
    }

    private func nativeLifecycleChanged(from previous: RoomLifecycleState, to current: RoomLifecycleState) {
        switch current {
        case .connected, .reconnecting:
            _ = session.markNativeMediaActive()
        case .signedOut where previous != .signedOut:
            let hadActiveNativeCall = session.mediaOwner == .nativeActive
            _ = session.releaseNativeMediaOwnership(nativeSessionEnded: true)
            surface = .web
            if let error = nativeRoom.errorMessage {
                nativeMediaNotice = hadActiveNativeCall
                    ? "The native media session ended: \(error) The web meeting path is ready for an explicit rejoin."
                    : "Native media could not initialize: \(error) The web meeting path remains available."
            }
        default:
            break
        }
    }

    private var webSurface: some View {
        ZStack {
            shellBlack

            RadialGradient(
                colors: [ember.opacity(0.065), .clear],
                center: .topTrailing,
                startRadius: 12,
                endRadius: 430
            )

            ZStack {
                MeetingAssistWebView(session: session)

                if session.isLoading {
                    VStack {
                        ProgressView(value: session.estimatedProgress)
                            .progressViewStyle(.linear)
                            .tint(ember)
                            .accessibilityLabel("Loading STRIDE")
                        Spacer()
                    }
                    .allowsHitTesting(false)
                }

                if let failure = session.failure {
                    failurePanel(failure)
                        .transition(.opacity.combined(with: .scale(scale: 0.98)))
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .strokeBorder(.white.opacity(colorSchemeContrast == .increased ? 0.25 : 0.10), lineWidth: 1)
                    .allowsHitTesting(false)
            }
            .shadow(color: .black.opacity(reduceTransparency ? 0.16 : 0.44), radius: 22, y: 10)
            .padding(.top, 8)
            .padding(.trailing, 12)
            .padding(.bottom, 12)
            .padding(.leading, 10)
        }
        .background(shellBlack)
        .navigationTitle("")
    }

    @ViewBuilder
    private func failurePanel(_ failure: MeetingAssistWebFailure) -> some View {
        let content = VStack(spacing: 18) {
            Image(systemName: "wifi.exclamationmark")
                .font(.system(size: 30, weight: .medium))
                .foregroundStyle(.secondary)

            VStack(spacing: 6) {
                Text(failure.title)
                    .font(.title2.weight(.semibold))
                    .multilineTextAlignment(.center)
                Text(failure.message)
                    .font(.body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .textSelection(.enabled)
            }

            HStack(spacing: 10) {
                Button("Try Again") {
                    session.reload()
                }
                .buttonStyle(.borderedProminent)
                .tint(ember)
                .controlSize(.large)

                Button("Open in Browser") {
                    session.openCurrentPageInBrowser()
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            }
        }
        .padding(28)
        .frame(width: 390)
        .accessibilityElement(children: .contain)

        if #available(macOS 26.0, *), !reduceTransparency {
            content
                .glassEffect(.regular, in: .rect(cornerRadius: 26))
                .shadow(color: .black.opacity(0.16), radius: 28, y: 14)
        } else {
            content
                .background(
                    reduceTransparency ? AnyShapeStyle(Color(nsColor: .windowBackgroundColor)) : AnyShapeStyle(.regularMaterial),
                    in: RoundedRectangle(cornerRadius: 26, style: .continuous)
                )
                .overlay {
                    RoundedRectangle(cornerRadius: 26, style: .continuous)
                        .strokeBorder(.primary.opacity(0.08))
                }
                .shadow(color: .black.opacity(0.14), radius: 24, y: 12)
        }
    }
}

private struct MacSidebar: View {
    @ObservedObject var session: MeetingAssistWebSession
    @ObservedObject var updates: MeetingAssistUpdateController
    let isNativeMediaSelected: Bool
    let canOpenNativeMedia: Bool
    let onSelectWorkspace: (MeetingAssistWorkspace) -> Void
    let onOpenNativeMedia: () -> Void
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var isOrganizationHovering = false

    private let ember = Color(red: 1, green: 90 / 255, blue: 25 / 255)
    private let shellBlack = Color(nsColor: NSColor(calibratedWhite: 0.012, alpha: 1))

    var body: some View {
        VStack(spacing: 0) {
            brand

            ScrollView {
                VStack(alignment: .leading, spacing: 5) {
                    Text("Workspace")
                        .font(.system(size: 10, weight: .semibold))
                        .tracking(0.7)
                        .textCase(.uppercase)
                        .foregroundStyle(.white.opacity(0.38))
                        .padding(.horizontal, 10)
                        .padding(.top, 4)
                        .padding(.bottom, 2)

                    ForEach(MeetingAssistWorkspace.allCases) { workspace in
                        SidebarWorkspaceButton(
                            workspace: workspace,
                            isSelected: !isNativeMediaSelected && session.currentWorkspace == workspace,
                            ember: ember
                        ) {
                            guard isNativeMediaSelected || workspace != session.currentWorkspace else { return }
                            onSelectWorkspace(workspace)
                        }
                        .help("Open \(workspace.title) (⌘\(workspaceShortcut(workspace)))")
                    }

                    if session.shellState.hasLiveRoom && !session.shellState.isInRoom {
                        Text("Live")
                            .font(.system(size: 10, weight: .semibold))
                            .tracking(0.7)
                            .textCase(.uppercase)
                            .foregroundStyle(.white.opacity(0.38))
                            .padding(.horizontal, 10)
                            .padding(.top, 12)
                            .padding(.bottom, 2)

                        SidebarUtilityButton(
                            title: "Return to live room",
                            systemImage: "waveform.circle.fill",
                            accent: ember
                        ) {
                            session.perform(.returnToLiveRoom)
                        }
                    }

                    Text("Native room preview")
                        .font(.system(size: 10, weight: .semibold))
                        .tracking(0.7)
                        .textCase(.uppercase)
                        .foregroundStyle(.white.opacity(0.38))
                        .padding(.horizontal, 10)
                        .padding(.top, 12)
                        .padding(.bottom, 2)

                    SidebarUtilityButton(
                        title: "Native room",
                        systemImage: "waveform.badge.mic",
                        accent: isNativeMediaSelected ? ember : nil,
                        action: onOpenNativeMedia
                    )
                    .disabled(!canOpenNativeMedia)
                    .help(canOpenNativeMedia ? "Join the live room with native Mac media" : "Leave the web meeting before switching media owner")
                }
                .padding(.horizontal, 8)
                .padding(.bottom, 14)
            }
            .scrollIndicators(.hidden)

            Rectangle()
                .fill(.white.opacity(0.065))
                .frame(height: 1)

            VStack(spacing: 2) {
                SidebarUtilityButton(
                    title: "Notifications",
                    systemImage: "bell",
                    badge: session.shellState.unreadCount
                ) {
                    session.perform(.notifications)
                }

                SidebarUtilityButton(title: "Appearance", systemImage: "circle.lefthalf.filled") {
                    session.perform(.toggleAppearance)
                }

                SidebarUtilityButton(
                    title: session.shellState.accountName,
                    systemImage: "person.crop.circle"
                ) {
                    session.perform(.profileSettings)
                }
            }
            .padding(8)

            if updates.phase.showsSidebarCard {
                SidebarUpdateCard(updates: updates)
                    .padding(.horizontal, 8)
                    .padding(.bottom, 10)
                    .transition(.opacity.combined(with: .move(edge: .bottom)))
            }

            connectionStatus
                .padding(.horizontal, 14)
                .padding(.bottom, 12)
        }
        .background {
            ZStack {
                shellBlack
                LinearGradient(
                    colors: [.white.opacity(0.035), .clear, ember.opacity(0.018)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
            }
            .ignoresSafeArea()
        }
        .overlay(alignment: .trailing) {
            Rectangle()
                .fill(.white.opacity(0.055))
                .frame(width: 1)
                .allowsHitTesting(false)
        }
    }

    private var brand: some View {
        VStack(alignment: .leading, spacing: 10) {
            Image("StrideWordmark")
                .resizable()
                .scaledToFit()
                .frame(width: 68, height: 18, alignment: .leading)
                .accessibilityLabel("STRIDE")

            Button {
                session.perform(.organizationSettings)
            } label: {
                HStack(spacing: 6) {
                    Text(session.shellState.organization)
                        .font(.subheadline.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    Image(systemName: "chevron.up.chevron.down")
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundStyle(.tertiary)
                }
                .padding(.horizontal, 8)
                .frame(minHeight: 40)
                .contentShape(Rectangle())
                .background(
                    .white.opacity(isOrganizationHovering ? 0.065 : 0),
                    in: RoundedRectangle(cornerRadius: 10, style: .continuous)
                )
            }
            .buttonStyle(SidebarPressButtonStyle())
            .onHover { hovering in
                if reduceMotion {
                    isOrganizationHovering = hovering
                } else {
                    withAnimation(.easeOut(duration: 0.14)) {
                        isOrganizationHovering = hovering
                    }
                }
            }
            .help("Organization settings")
            .disabled(!session.shellState.isAuthenticated)
        }
        .padding(.horizontal, 14)
        .padding(.top, 14)
        .padding(.bottom, 10)
    }

    private var connectionStatus: some View {
        HStack(spacing: 7) {
            Circle()
                .fill(connectionColor)
                .frame(width: 6, height: 6)
            Text(session.shellState.connectionStatus.lowercased())
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Spacer()
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Connection: \(session.shellState.connectionStatus)")
    }

    private var connectionColor: Color {
        let status = session.shellState.connectionStatus.lowercased()
        if status.contains("ready") || status.contains("connected") { return .green }
        if status.contains("connect") { return .yellow }
        return .secondary
    }

    private func workspaceShortcut(_ workspace: MeetingAssistWorkspace) -> Int {
        MeetingAssistWorkspace.allCases.firstIndex(of: workspace).map { $0 + 1 } ?? 1
    }
}

private struct SidebarUpdateCard: View {
    @ObservedObject var updates: MeetingAssistUpdateController
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var isHovering = false

    private let ember = Color(red: 1, green: 90 / 255, blue: 25 / 255)

    var body: some View {
        Button {
            updates.checkForUpdates()
        } label: {
            HStack(spacing: 10) {
                statusIcon
                    .frame(width: 18, height: 18)

                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .font(.system(size: 12, weight: .semibold))
                        .lineLimit(1)
                    Text(detail)
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                Spacer(minLength: 4)

                if !updates.phase.isBusy {
                    Image(systemName: actionSymbol)
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(isAvailable ? ember : .secondary)
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 9)
            .contentShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            .modifier(
                SidebarUpdateSurface(
                    background: cardBackground,
                    border: cardBorder
                )
            )
        }
        .buttonStyle(SidebarPressButtonStyle())
        .onHover { hovering in
            guard !reduceMotion else {
                isHovering = hovering
                return
            }
            withAnimation(.easeOut(duration: 0.16)) {
                isHovering = hovering
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(title). \(detail)")
        .accessibilityHint("Opens the STRIDE software update window")
    }

    @ViewBuilder
    private var statusIcon: some View {
        if updates.phase.isBusy {
            ProgressView()
                .controlSize(.small)
                .accessibilityHidden(true)
        } else {
            Image(systemName: leadingSymbol)
                .font(.system(size: 14, weight: .semibold))
                .foregroundStyle(isAvailable ? ember : .secondary)
                .accessibilityHidden(true)
        }
    }

    private var isAvailable: Bool {
        switch updates.phase {
        case .available, .ready:
            true
        default:
            false
        }
    }

    private var title: String {
        switch updates.phase {
        case .checking: "Checking for updates…"
        case .current: "You’re up to date"
        case .available: "Ready to update!"
        case .downloading: "Downloading update…"
        case .preparing: "Preparing update…"
        case .ready: "Restart to update"
        case .installing: "Installing update…"
        case .failed: "Couldn’t update"
        case .unconfigured, .idle: "Software update"
        }
    }

    private var detail: String {
        switch updates.phase {
        case let .available(version): "STRIDE \(version) is available"
        case let .downloading(version): "Downloading STRIDE \(version)"
        case let .preparing(version): "Preparing STRIDE \(version)"
        case let .ready(version): "STRIDE \(version) is ready"
        case let .installing(version): "Installing STRIDE \(version)"
        case let .failed(message): message
        case .checking: "This only takes a moment"
        case .current: "STRIDE \(updates.configuration.installedVersion)"
        case .unconfigured, .idle: "STRIDE \(updates.configuration.installedVersion)"
        }
    }

    private var leadingSymbol: String {
        switch updates.phase {
        case .current: "checkmark.circle.fill"
        case .available: "arrow.down.circle.fill"
        case .ready: "arrow.clockwise.circle.fill"
        case .failed: "exclamationmark.triangle.fill"
        default: "arrow.triangle.2.circlepath"
        }
    }

    private var actionSymbol: String {
        switch updates.phase {
        case .failed: "arrow.clockwise"
        case .current: "checkmark"
        default: "arrow.right.circle"
        }
    }

    private var cardBackground: Color {
        if isAvailable { return ember.opacity(isHovering ? 0.17 : 0.12) }
        return Color.primary.opacity(isHovering ? 0.09 : 0.055)
    }

    private var cardBorder: Color {
        if isAvailable { return ember.opacity(isHovering ? 0.42 : 0.28) }
        return Color.primary.opacity(0.08)
    }
}

private struct SidebarUtilityButton: View {
    let title: String
    let systemImage: String
    var badge = 0
    var accent: Color? = nil
    let action: () -> Void

    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 9) {
                Image(systemName: systemImage)
                    .frame(width: 17)
                    .foregroundStyle(accent ?? .white.opacity(0.72))
                Text(title)
                    .lineLimit(1)
                    .foregroundStyle(accent ?? .white.opacity(0.82))
                Spacer(minLength: 8)
                if badge > 0 {
                    Text(badge > 99 ? "99+" : String(badge))
                        .font(.caption2.monospacedDigit().weight(.semibold))
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(.tint, in: Capsule())
                        .foregroundStyle(.white)
                        .accessibilityLabel("\(badge) unread")
                }
            }
            .font(.system(size: 13, weight: .medium))
            .padding(.horizontal, 9)
            .frame(minHeight: 40)
            .contentShape(Rectangle())
            .background(.white.opacity(isHovering ? 0.065 : 0), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
        .buttonStyle(SidebarPressButtonStyle())
        .onHover { hovering in
            withAnimation(.easeOut(duration: 0.14)) {
                isHovering = hovering
            }
        }
        .accessibilityLabel(title)
    }
}

private struct SidebarWorkspaceButton: View {
    let workspace: MeetingAssistWorkspace
    let isSelected: Bool
    let ember: Color
    let action: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 9) {
                Capsule()
                    .fill(isSelected ? ember : .clear)
                    .frame(width: 2, height: 16)
                    .shadow(color: isSelected ? ember.opacity(0.45) : .clear, radius: 5)

                Image(systemName: workspace.systemImage)
                    .font(.system(size: 13, weight: .medium))
                    .frame(width: 17)
                    .foregroundStyle(isSelected ? ember : .white.opacity(0.64))

                Text(workspace.title)
                    .font(.system(size: 13, weight: isSelected ? .semibold : .medium))
                    .foregroundStyle(.white.opacity(isSelected ? 0.96 : 0.78))
                    .lineLimit(1)

                Spacer(minLength: 8)
            }
            .padding(.horizontal, 8)
            .frame(minHeight: 40)
            .contentShape(RoundedRectangle(cornerRadius: 11, style: .continuous))
            .modifier(SidebarItemSurface(isSelected: isSelected, isHovering: isHovering))
        }
        .buttonStyle(SidebarPressButtonStyle())
        .onHover { hovering in
            if reduceMotion {
                isHovering = hovering
            } else {
                withAnimation(.easeOut(duration: 0.14)) {
                    isHovering = hovering
                }
            }
        }
        .accessibilityLabel(workspace.title)
        .accessibilityAddTraits(isSelected ? .isSelected : [])
    }
}

private struct SidebarItemSurface: ViewModifier {
    let isSelected: Bool
    let isHovering: Bool

    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    @ViewBuilder
    func body(content: Content) -> some View {
        if isSelected {
            if #available(macOS 26.0, *), !reduceTransparency {
                content
                    .background(.white.opacity(0.025), in: RoundedRectangle(cornerRadius: 11, style: .continuous))
                    .glassEffect(.regular, in: .rect(cornerRadius: 11))
                    .overlay {
                        RoundedRectangle(cornerRadius: 11, style: .continuous)
                            .strokeBorder(.white.opacity(0.105), lineWidth: 1)
                    }
            } else {
                content
                    .background(
                        reduceTransparency ? AnyShapeStyle(Color.white.opacity(0.11)) : AnyShapeStyle(.thinMaterial),
                        in: RoundedRectangle(cornerRadius: 11, style: .continuous)
                    )
                    .overlay {
                        RoundedRectangle(cornerRadius: 11, style: .continuous)
                            .strokeBorder(.white.opacity(0.10), lineWidth: 1)
                    }
            }
        } else {
            content
                .background(
                    .white.opacity(isHovering ? 0.06 : 0),
                    in: RoundedRectangle(cornerRadius: 11, style: .continuous)
                )
        }
    }
}

private struct SidebarUpdateSurface: ViewModifier {
    let background: Color
    let border: Color

    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    @ViewBuilder
    func body(content: Content) -> some View {
        if #available(macOS 26.0, *), !reduceTransparency {
            content
                .background(background, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                .glassEffect(.regular, in: .rect(cornerRadius: 10))
                .overlay {
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .strokeBorder(border, lineWidth: 1)
                }
        } else {
            content
                .background(background, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                .overlay {
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .strokeBorder(border, lineWidth: 1)
                }
        }
    }
}

private struct SidebarPressButtonStyle: ButtonStyle {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed && !reduceMotion ? 0.96 : 1)
            .animation(reduceMotion ? nil : .easeOut(duration: 0.12), value: configuration.isPressed)
    }
}

private struct WindowChromeConfigurator: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView {
        let view = NSView(frame: .zero)
        configure(view)
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        configure(nsView)
    }

    private func configure(_ view: NSView) {
        DispatchQueue.main.async {
            guard let window = view.window else { return }
            window.appearance = NSAppearance(named: .darkAqua)
            window.backgroundColor = NSColor(calibratedWhite: 0.012, alpha: 1)
            window.isOpaque = true
            window.titleVisibility = .hidden
            window.titlebarAppearsTransparent = true
            window.titlebarSeparatorStyle = .none
            window.styleMask.insert(.fullSizeContentView)
            window.toolbarStyle = .unified
            window.toolbar?.showsBaselineSeparator = false
        }
    }
}
