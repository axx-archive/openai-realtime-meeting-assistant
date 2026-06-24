import SwiftUI
import MeetingAssistCore
import MeetingAssistDesign

public struct MeetingAssistMacRootView: View {
    @State private var selection: RoomLifecycleState = .signedOut

    public init() {}

    public var body: some View {
        NavigationSplitView {
            List(RoomLifecycleState.allCasesForNavigation, id: \.self, selection: $selection) { state in
                Text(label(for: state))
            }
            .navigationTitle("MeetingAssist")
        } detail: {
            VStack(alignment: .leading, spacing: 16) {
                RoomStatusBadge(state: selection)
                Text("Native macOS room foundation")
                    .font(.title2.bold())
                Text("This shell is ready for the shared API, signaling, media, and Scout modules.")
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .padding()
        }
    }

    private func label(for state: RoomLifecycleState) -> String {
        switch state {
        case .signedOut: "Signed out"
        case .authenticated: "Authenticated"
        case .admitted: "Admitted"
        case .preparingMedia: "Preparing media"
        case .negotiating: "Negotiating"
        case .connected: "Connected"
        case .reconnecting: "Reconnecting"
        case .leaving: "Leaving"
        }
    }
}

private extension RoomLifecycleState {
    static var allCasesForNavigation: [RoomLifecycleState] {
        [.signedOut, .authenticated, .admitted, .preparingMedia, .negotiating, .connected, .reconnecting, .leaving]
    }
}
