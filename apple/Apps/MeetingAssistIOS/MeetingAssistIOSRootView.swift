import SwiftUI
import MeetingAssistCore
import MeetingAssistDesign

public struct MeetingAssistIOSRootView: View {
    @State private var lifecycle: RoomLifecycleState = .signedOut

    public init() {}

    public var body: some View {
        NavigationStack {
            VStack(spacing: 20) {
                RoomStatusBadge(state: lifecycle)
                VStack(spacing: 8) {
                    Text("MeetingAssist")
                        .font(.title.bold())
                    Text("Native room foundation")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                HStack(spacing: 12) {
                    Button("Sign in") { lifecycle = .authenticated }
                    Button("Prepare media") { lifecycle = .preparingMedia }
                    Button("Connected") { lifecycle = .connected }
                }
                .buttonStyle(.borderedProminent)
            }
            .padding()
            .navigationTitle("Room")
        }
    }
}
