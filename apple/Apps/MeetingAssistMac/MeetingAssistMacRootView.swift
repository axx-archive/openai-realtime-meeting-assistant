import SwiftUI
import MeetingAssistRoomUI

public struct MeetingAssistMacRootView: View {
    public init() {}

    public var body: some View {
        NavigationStack {
            NativeRoomView()
            .navigationTitle("MeetingAssist")
        }
    }
}
