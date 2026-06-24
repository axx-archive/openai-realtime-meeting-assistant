import MeetingAssistCore
import MeetingAssistDesign
import SwiftUI

public struct NativeRoomView: View {
    @StateObject private var model: NativeRoomViewModel

    public init(model: NativeRoomViewModel = NativeRoomViewModel()) {
        _model = StateObject(wrappedValue: model)
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                header
                connectionForm
                controls
                status
            }
            .frame(maxWidth: 720, alignment: .leading)
            .padding()
        }
        .task {
            guard model.roster.isEmpty else { return }
            await model.refreshRoster()
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            RoomStatusBadge(state: model.lifecycle)
            Text("MeetingAssist")
                .font(.largeTitle.bold())
            Text("Native room")
                .font(.headline)
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }

    private var connectionForm: some View {
        VStack(alignment: .leading, spacing: 12) {
            TextField("Room URL", text: $model.baseURLString)
                #if os(iOS)
                .textInputAutocapitalization(.never)
                .keyboardType(.URL)
                #endif
                .textContentType(.URL)
                .autocorrectionDisabled()

            if model.roster.isEmpty {
                TextField("Name", text: $model.selectedName)
                    #if os(iOS)
                    .textInputAutocapitalization(.words)
                    #endif
            } else {
                Picker("Name", selection: $model.selectedName) {
                    ForEach(model.roster) { participant in
                        Text(participant.name).tag(participant.name)
                    }
                }
                .pickerStyle(.menu)
            }

            SecureField("Password", text: $model.password)

            HStack(spacing: 10) {
                Button {
                    Task { await model.refreshRoster() }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
                .disabled(model.isBusy)

                Button {
                    Task { await model.joinAudioOnly() }
                } label: {
                    Label("Join audio", systemImage: "mic.circle.fill")
                }
                .buttonStyle(.borderedProminent)
                .disabled(!model.canJoin)
            }
        }
        .textFieldStyle(.roundedBorder)
    }

    private var controls: some View {
        VStack(alignment: .leading, spacing: 12) {
            Toggle(
                isOn: Binding(
                    get: { model.isMuted },
                    set: { muted in
                        Task { await model.setMuted(muted) }
                    }
                )
            ) {
                Label("Mute", systemImage: model.isMuted ? "mic.slash.fill" : "mic.fill")
            }
            .disabled(!model.canUseRoomControls)

            Button(role: .destructive) {
                Task { await model.leave() }
            } label: {
                Label("Leave", systemImage: "phone.down.fill")
            }
            .disabled(!model.canUseRoomControls && model.lifecycle != .connected)
        }
    }

    private var status: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(model.statusText)
                .font(.callout.weight(.semibold))
            if let participant = model.joinedParticipant {
                Text(participant.name)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            if let errorMessage = model.errorMessage {
                Text(errorMessage)
                    .font(.callout)
                    .foregroundStyle(.red)
            }
        }
        .accessibilityElement(children: .combine)
    }
}
