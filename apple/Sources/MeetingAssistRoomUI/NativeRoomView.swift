import MeetingAssistCore
import MeetingAssistDesign
import Foundation
import SwiftUI

public struct NativeRoomView: View {
    @StateObject private var model: NativeRoomViewModel
    @State private var boardEditorDraft: BoardCardEditorDraft?

    public init(model: NativeRoomViewModel = NativeRoomViewModel()) {
        _model = StateObject(wrappedValue: model)
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                header
                connectionForm
                remoteVideoGrid
                if model.canUseRoomControls || !model.roomParticipants.isEmpty {
                    roomState
                }
                if model.canUseRoomControls || !model.boardCards.isEmpty {
                    boardPreview
                }
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
        .sheet(item: $boardEditorDraft) { draft in
            BoardCardEditor(
                draft: draft,
                statuses: model.boardStatuses,
                canDelete: draft.cardID != nil,
                onSave: saveBoardDraft,
                onDelete: deleteBoardDraft
            )
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

                Button {
                    Task { await model.joinWithCamera() }
                } label: {
                    Label("Join video", systemImage: "video.circle.fill")
                }
                .buttonStyle(.bordered)
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

            Toggle(
                isOn: Binding(
                    get: { !model.isCameraOff },
                    set: { cameraOn in
                        Task { await model.setCameraOff(!cameraOn) }
                    }
                )
            ) {
                Label("Camera", systemImage: model.isCameraOff ? "video.slash.fill" : "video.fill")
            }
            .disabled(!model.canUseCameraControls)

            Button(role: .destructive) {
                Task { await model.leave() }
            } label: {
                Label("Leave", systemImage: "phone.down.fill")
            }
            .disabled(!model.canUseRoomControls && model.lifecycle != .connected)
        }
    }

    private var roomState: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                Label(recordingLabel, systemImage: model.roomRecording.enabled ? "record.circle.fill" : "pause.circle")
                    .foregroundStyle(model.roomRecording.enabled ? .red : .secondary)

                Spacer()

                Button {
                    Task { await model.setRecordingEnabled(!model.roomRecording.enabled) }
                } label: {
                    Label(model.roomRecording.enabled ? "Pause" : "Resume", systemImage: model.roomRecording.enabled ? "pause.fill" : "record.circle")
                }
                .buttonStyle(.bordered)
                .disabled(!model.canUseRoomControls)

                Button {
                    Task { await model.archiveMeeting() }
                } label: {
                    Label("Archive", systemImage: "tray.and.arrow.down.fill")
                }
                .buttonStyle(.bordered)
                .disabled(!model.canUseRoomControls || model.isArchiving)
            }

            if !model.roomParticipants.isEmpty {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 150), spacing: 8)], spacing: 8) {
                    ForEach(model.roomParticipants, id: \.self) { name in
                        participantRow(name)
                    }
                }
            }
        }
    }

    private var boardPreview: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Label("Board", systemImage: "rectangle.3.group.fill")
                    .font(.headline)
                Spacer()
                Text("\(model.boardCards.count) cards")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Button {
                    boardEditorDraft = BoardCardEditorDraft(owner: model.joinedParticipant?.name)
                } label: {
                    Label("New", systemImage: "plus")
                }
                .buttonStyle(.bordered)
                .disabled(!model.canUseRoomControls || model.isBoardMutating)

                Button {
                    Task { await model.undoDeletedBoardCard() }
                } label: {
                    Label("Undo", systemImage: "arrow.uturn.backward")
                }
                .buttonStyle(.bordered)
                .disabled(!model.canUseRoomControls || !model.canUndoDelete || model.isBoardMutating)
            }

            if model.activeBoardCards.isEmpty {
                Text(model.canUseRoomControls ? "No active cards" : "Join to load the board")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(model.activeBoardCards) { card in
                        boardRow(card)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var remoteVideoGrid: some View {
        if !model.remoteVideoTracks.isEmpty {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 220), spacing: 12)], spacing: 12) {
                ForEach(model.remoteVideoTracks) { trackInfo in
                    NativeRemoteVideoTrackView(track: trackInfo.track, displayName: trackInfo.displayName)
                }
            }
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

    private var recordingLabel: String {
        guard let updatedBy = model.roomRecording.updatedBy, !updatedBy.isEmpty else {
            return model.roomRecording.enabled ? "Recording" : "Paused"
        }
        return model.roomRecording.enabled ? "Recording by \(updatedBy)" : "Paused by \(updatedBy)"
    }

    private func participantRow(_ name: String) -> some View {
        let media = model.participantMediaStates[name]
        return HStack(spacing: 8) {
            Text(monogram(for: name))
                .font(.caption.weight(.bold))
                .frame(width: 24, height: 24)
                .background(.secondary.opacity(0.16), in: Circle())

            Text(name)
                .font(.callout)
                .lineLimit(1)

            Spacer(minLength: 4)

            Image(systemName: media?.micMuted == true ? "mic.slash.fill" : "mic.fill")
                .foregroundStyle(media?.micMuted == true ? .secondary : .primary)
            Image(systemName: media?.cameraOff == true ? "video.slash.fill" : "video.fill")
                .foregroundStyle(media?.cameraOff == true ? .secondary : .primary)
            if media?.screenSharing == true {
                Image(systemName: "display")
                    .foregroundStyle(.blue)
            }
        }
        .padding(.vertical, 6)
        .padding(.horizontal, 8)
        .background(.quaternary.opacity(0.25), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private func boardRow(_ card: KanbanCard) -> some View {
        Button {
            boardEditorDraft = BoardCardEditorDraft(card: card)
        } label: {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(card.status)
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.secondary)
                    Spacer()
                    if let owner = card.owner, !owner.isEmpty {
                        Text(owner)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
                Text(card.title)
                    .font(.callout.weight(.semibold))
                    .lineLimit(2)
                if let dueDate = card.dueDate, !dueDate.isEmpty {
                    Label(dueDate, systemImage: "calendar")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .buttonStyle(.plain)
        .disabled(!model.canUseRoomControls || model.isBoardMutating)
        .padding(.vertical, 8)
        .padding(.horizontal, 10)
        .background(.quaternary.opacity(0.22), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private func saveBoardDraft(_ draft: BoardCardEditorDraft) {
        let payload = draft.payload
        boardEditorDraft = nil
        Task {
            if let cardID = draft.cardID {
                await model.updateBoardCard(id: cardID, payload: payload)
            } else {
                await model.createBoardCard(payload)
            }
        }
    }

    private func deleteBoardDraft(_ draft: BoardCardEditorDraft) {
        guard let cardID = draft.cardID else { return }
        boardEditorDraft = nil
        Task { await model.deleteBoardCard(id: cardID) }
    }

    private func monogram(for name: String) -> String {
        String(name.trimmingCharacters(in: .whitespacesAndNewlines).first ?? "B").uppercased()
    }
}

private struct BoardCardEditorDraft: Identifiable {
    let id: String
    var cardID: String?
    var title: String
    var status: String
    var owner: String
    var tagsInput: String
    var notes: String
    var dueDate: String

    init(owner: String?) {
        id = UUID().uuidString
        cardID = nil
        title = ""
        status = "Backlog"
        self.owner = owner ?? ""
        tagsInput = ""
        notes = ""
        dueDate = ""
    }

    init(card: KanbanCard) {
        id = card.id
        cardID = card.id
        title = card.title
        status = card.status
        owner = card.owner ?? ""
        tagsInput = (card.tags ?? []).joined(separator: ", ")
        notes = card.notes ?? ""
        dueDate = card.dueDate ?? ""
    }

    var canSave: Bool {
        !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !status.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var payload: BoardCardMutationPayload {
        let trimmedDueDate = dueDate.trimmingCharacters(in: .whitespacesAndNewlines)
        let keyDates = trimmedDueDate.isEmpty ? [] : [KanbanKeyDate(label: "due", date: trimmedDueDate)]
        return BoardCardMutationPayload(
            cardID: cardID,
            title: title.trimmingCharacters(in: .whitespacesAndNewlines),
            status: status,
            owner: owner.trimmingCharacters(in: .whitespacesAndNewlines),
            tags: tagsInput
                .split(separator: ",")
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty },
            notes: notes.trimmingCharacters(in: .whitespacesAndNewlines),
            dueDate: trimmedDueDate,
            keyDates: keyDates
        )
    }
}

private struct BoardCardEditor: View {
    @Environment(\.dismiss) private var dismiss
    @State private var draft: BoardCardEditorDraft
    @State private var confirmsDelete = false

    var statuses: [String]
    var canDelete: Bool
    var onSave: (BoardCardEditorDraft) -> Void
    var onDelete: (BoardCardEditorDraft) -> Void

    init(
        draft: BoardCardEditorDraft,
        statuses: [String],
        canDelete: Bool,
        onSave: @escaping (BoardCardEditorDraft) -> Void,
        onDelete: @escaping (BoardCardEditorDraft) -> Void
    ) {
        _draft = State(initialValue: draft)
        self.statuses = statuses
        self.canDelete = canDelete
        self.onSave = onSave
        self.onDelete = onDelete
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Title", text: $draft.title)
                    Picker("Status", selection: $draft.status) {
                        ForEach(statuses, id: \.self) { status in
                            Text(status).tag(status)
                        }
                    }
                    TextField("Owner", text: $draft.owner)
                    TextField("Tags", text: $draft.tagsInput)
                    TextField("Due date", text: $draft.dueDate)
                    TextField("Notes", text: $draft.notes, axis: .vertical)
                        .lineLimit(3...6)
                }

                Section {
                    Button {
                        onSave(draft)
                    } label: {
                        Label("Save", systemImage: "checkmark")
                    }
                    .disabled(!draft.canSave)

                    if canDelete {
                        Button(role: .destructive) {
                            confirmsDelete = true
                        } label: {
                            Label("Delete", systemImage: "trash")
                        }
                    }

                    Button(role: .cancel) {
                        dismiss()
                    } label: {
                        Label("Cancel", systemImage: "xmark")
                    }
                }
            }
            .navigationTitle(draft.cardID == nil ? "New card" : "Edit card")
        }
        .frame(minWidth: 360, minHeight: 420)
        .confirmationDialog("Delete card?", isPresented: $confirmsDelete, titleVisibility: .visible) {
            Button("Delete", role: .destructive) {
                onDelete(draft)
            }
            Button("Cancel", role: .cancel) {}
        }
    }
}
