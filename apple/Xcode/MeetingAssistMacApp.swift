import SwiftUI
import MeetingAssistMac

@main
struct MeetingAssistMacApp: App {
    var body: some Scene {
        WindowGroup("MeetingAssist") {
            MeetingAssistMacRootView()
                .frame(minWidth: 900, minHeight: 620)
        }
        .windowResizability(.contentMinSize)
    }
}
