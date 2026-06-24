import SwiftUI
import MeetingAssistCore

public struct RoomStatusBadge: View {
    private let state: RoomLifecycleState

    public init(state: RoomLifecycleState) {
        self.state = state
    }

    public var body: some View {
        Label(title, systemImage: icon)
            .font(.caption.weight(.semibold))
            .foregroundStyle(foreground)
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(.thinMaterial, in: Capsule())
            .accessibilityLabel(title)
    }

    private var title: String {
        switch state {
        case .signedOut: "signed out"
        case .authenticated: "signed in"
        case .admitted: "admitted"
        case .preparingMedia: "preparing media"
        case .negotiating: "negotiating"
        case .connected: "connected"
        case .reconnecting: "reconnecting"
        case .leaving: "leaving"
        }
    }

    private var icon: String {
        switch state {
        case .connected: "checkmark.circle.fill"
        case .reconnecting, .negotiating, .preparingMedia: "arrow.triangle.2.circlepath"
        case .leaving, .signedOut: "circle"
        default: "person.2"
        }
    }

    private var foreground: Color {
        switch state {
        case .connected: .green
        case .reconnecting: .orange
        case .signedOut: .secondary
        default: .primary
        }
    }
}
