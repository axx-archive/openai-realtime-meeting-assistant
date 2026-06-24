import MeetingAssistRoomRTC
import SwiftUI

#if canImport(LiveKitWebRTC)
@preconcurrency import LiveKitWebRTC
#endif

public struct NativeRemoteVideoTrackView: View {
    public let track: NativeRemoteVideoTrack

    public init(track: NativeRemoteVideoTrack) {
        self.track = track
    }

    public var body: some View {
        ZStack(alignment: .bottomLeading) {
            renderer
                .aspectRatio(16 / 9, contentMode: .fit)
                .background(.black)
                .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))

            Text(track.id)
                .font(.caption2.monospaced())
                .foregroundStyle(.white)
                .lineLimit(1)
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(.black.opacity(0.65), in: Capsule())
                .padding(8)
        }
        .accessibilityLabel("Remote video \(track.id)")
    }

    @ViewBuilder
    private var renderer: some View {
        #if canImport(LiveKitWebRTC)
        NativeVideoRenderer(track: track)
        #else
        Rectangle().fill(.black)
        #endif
    }
}

#if canImport(LiveKitWebRTC) && os(iOS)
private struct NativeVideoRenderer: UIViewRepresentable {
    let track: NativeRemoteVideoTrack

    final class Coordinator {
        let track: NativeRemoteVideoTrack

        init(track: NativeRemoteVideoTrack) {
            self.track = track
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(track: track)
    }

    func makeUIView(context: Context) -> LKRTCMTLVideoView {
        let view = LKRTCMTLVideoView(frame: .zero)
        view.videoContentMode = .scaleAspectFill
        view.isEnabled = true
        track.addRenderer(view)
        return view
    }

    func updateUIView(_ uiView: LKRTCMTLVideoView, context: Context) {}

    static func dismantleUIView(_ uiView: LKRTCMTLVideoView, coordinator: Coordinator) {
        coordinator.track.removeRenderer(uiView)
    }
}
#elseif canImport(LiveKitWebRTC) && os(macOS)
private struct NativeVideoRenderer: NSViewRepresentable {
    let track: NativeRemoteVideoTrack

    final class Coordinator {
        let track: NativeRemoteVideoTrack

        init(track: NativeRemoteVideoTrack) {
            self.track = track
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(track: track)
    }

    func makeNSView(context: Context) -> LKRTCMTLVideoView {
        let view = LKRTCMTLVideoView(frame: .zero)
        view.isEnabled = true
        track.addRenderer(view)
        return view
    }

    func updateNSView(_ nsView: LKRTCMTLVideoView, context: Context) {}

    static func dismantleNSView(_ nsView: LKRTCMTLVideoView, coordinator: Coordinator) {
        coordinator.track.removeRenderer(nsView)
    }
}
#endif
