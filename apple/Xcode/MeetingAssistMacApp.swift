import SwiftUI
import MeetingAssistMac

@main
struct MeetingAssistMacApp: App {
    @StateObject private var updates = MeetingAssistUpdateController()

    var body: some Scene {
        WindowGroup("STRIDE") {
            MeetingAssistMacRootView(updates: updates)
                .frame(minWidth: 860, minHeight: 620)
        }
        .defaultSize(width: 1240, height: 820)
        .windowResizability(.contentMinSize)
        .windowToolbarStyle(.unified(showsTitle: false))
        .commands {
            CommandMenu("Workspace") {
                Button("Home") {
                    NotificationCenter.default.post(name: .strideGoHome, object: nil)
                }
                .keyboardShortcut("1", modifiers: .command)

                Button("Rooms") {
                    NotificationCenter.default.post(name: .strideGoRooms, object: nil)
                }
                .keyboardShortcut("2", modifiers: .command)

                Button("Conversations") {
                    NotificationCenter.default.post(name: .strideGoConversations, object: nil)
                }
                .keyboardShortcut("3", modifiers: .command)

                Button("Work") {
                    NotificationCenter.default.post(name: .strideGoWork, object: nil)
                }
                .keyboardShortcut("4", modifiers: .command)

                Button("Drive") {
                    NotificationCenter.default.post(name: .strideGoDrive, object: nil)
                }
                .keyboardShortcut("5", modifiers: .command)

                Divider()

                Button("Reload") {
                    NotificationCenter.default.post(name: .strideReload, object: nil)
                }
                .keyboardShortcut("r", modifiers: .command)

                Divider()

                Button("Back") {
                    NotificationCenter.default.post(name: .strideGoBack, object: nil)
                }
                .keyboardShortcut("[", modifiers: .command)

                Button("Forward") {
                    NotificationCenter.default.post(name: .strideGoForward, object: nil)
                }
                .keyboardShortcut("]", modifiers: .command)

                Divider()

                Button("Open in Browser") {
                    NotificationCenter.default.post(name: .strideOpenInBrowser, object: nil)
                }
                .keyboardShortcut("o", modifiers: [.command, .option])

                Divider()

                Button("Check for Updates…") {
                    NotificationCenter.default.post(name: .strideCheckForUpdates, object: nil)
                }
                .disabled(!updates.canCheckForUpdates)
            }
        }
    }
}
