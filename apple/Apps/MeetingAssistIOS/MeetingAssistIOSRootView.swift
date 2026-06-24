import SwiftUI
import MeetingAssistRoomUI

public struct MeetingAssistIOSRootView: View {
    public init() {}

    public var body: some View {
        NavigationStack {
            NativeRoomView()
            .navigationTitle("Room")
        }
    }
}
