import MeetingAssistCore
import MeetingAssistDesign
import MeetingAssistRoomRTC
import Foundation
import SwiftUI
import UniformTypeIdentifiers

#if os(iOS)
import UIKit
#elseif os(macOS)
import AppKit
#endif

public struct NativeRoomView: View {
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var model: NativeRoomViewModel
    @StateObject private var audioRecoveryMonitor = NativeAudioRecoveryMonitor()
    @StateObject private var connectivityMonitor = NativeConnectivityMonitor()
    @State private var boardEditorDraft: BoardCardEditorDraft?
    @State private var scoutChatDraft = ""
    @State private var roomScoutDraft = ""
    @State private var mediaEvidenceCopied = false
    @State private var mediaEvidenceSaved = false
    @State private var turnRelayObservationCopied = false
    @State private var turnRelayObservationSaved = false
    @State private var evidenceExportDocument = NativeEvidenceExportDocument()
    @State private var evidenceExportFilename = "native-evidence.json"
    @State private var evidenceExportKind: NativeEvidenceExportKind?
    @State private var isShowingEvidenceExporter = false
    @State private var evidenceExportError: String?

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
                if model.canUseRoomControls || model.latestMediaEvidence != nil || model.latestTurnRelayObservation != nil {
                    mediaEvidencePanel
                }
                if model.canUseRoomControls || !model.boardCards.isEmpty {
                    boardPreview
                }
                if model.canUseRoomControls || !model.assistantEvents.isEmpty || !model.memoryEntries.isEmpty || model.latestArchive != nil {
                    scoutMemoryPanel
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
        .task {
            connectivityMonitor.start { reason in
                Task { await model.requestMediaRecovery(reason: reason) }
            }
        }
        .task {
            audioRecoveryMonitor.start { reason in
                Task { await model.requestMediaRecovery(reason: reason) }
            }
        }
        .onChange(of: scenePhase) { _, phase in
            guard phase == .active else { return }
            Task { await model.requestMediaRecovery(reason: NativeMediaRecoveryReason.appForegrounded.rawValue) }
        }
        .onOpenURL { url in
            model.applyLaunchURL(url)
        }
        .onDisappear {
            audioRecoveryMonitor.stop()
            connectivityMonitor.stop()
        }
        .fileExporter(
            isPresented: $isShowingEvidenceExporter,
            document: evidenceExportDocument,
            contentType: .json,
            defaultFilename: evidenceExportFilename,
            onCompletion: handleEvidenceExport
        )
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

            #if os(macOS)
            Toggle(
                isOn: Binding(
                    get: { model.isScreenSharing },
                    set: { sharing in
                        Task { await model.setScreenSharing(sharing) }
                    }
                )
            ) {
                Label("Share screen", systemImage: "display")
            }
            .disabled(!model.canUseScreenShareControls)
            #endif

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

    private var mediaEvidencePanel: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Label("QA Evidence", systemImage: "checkmark.shield")
                    .font(.headline)
                Spacer()
                Button {
                    mediaEvidenceCopied = false
                    mediaEvidenceSaved = false
                    evidenceExportError = nil
                    Task { await model.captureMediaEvidence() }
                } label: {
                    Label("Capture", systemImage: "waveform.path.ecg.rectangle")
                }
                .buttonStyle(.bordered)
                .disabled(!model.canUseRoomControls || model.isCapturingMediaEvidence)

                Button {
                    copyMediaEvidenceJSON()
                } label: {
                    Label(mediaEvidenceCopied ? "Copied" : "Copy", systemImage: mediaEvidenceCopied ? "checkmark" : "doc.on.doc")
                }
                .buttonStyle(.bordered)
                .disabled(model.latestMediaEvidenceJSON == nil)

                Button {
                    saveMediaEvidenceJSON()
                } label: {
                    Label(mediaEvidenceSaved ? "Saved" : "Save", systemImage: mediaEvidenceSaved ? "checkmark.circle" : "square.and.arrow.down")
                }
                .buttonStyle(.bordered)
                .disabled(model.latestMediaEvidenceJSON == nil)
            }

            if let evidence = model.latestMediaEvidence {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 116), spacing: 8)], spacing: 8) {
                    mediaEvidenceFlag("Mic", evidence.mediaAssertions.microphonePublished)
                    mediaEvidenceFlag("Camera", evidence.mediaAssertions.cameraPublished)
                    mediaEvidenceFlag("Remote audio", evidence.mediaAssertions.remoteAudioReceived)
                    mediaEvidenceFlag("Remote video", evidence.mediaAssertions.remoteVideoRendered)
                    mediaEvidenceFlag("Relay", evidence.selectedCandidate.relayCandidateSelected)
                }

                Text(mediaEvidenceCandidateSummary(evidence))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)

                if let json = model.latestMediaEvidenceJSON {
                    Text(json)
                        .font(.caption.monospaced())
                        .lineLimit(12)
                        .textSelection(.enabled)
                        .padding(8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(.quaternary.opacity(0.18), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                }
            } else {
                Text("No evidence captured")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            VStack(alignment: .leading, spacing: 8) {
                TextField("Release run ID", text: $model.releaseRunId)
                    .textFieldStyle(.roundedBorder)
                    .disabled(model.canUseRoomControls)
                TextField("Release room ID", text: $model.releaseRoomId)
                    .textFieldStyle(.roundedBorder)
                    .disabled(model.canUseRoomControls)
            }

            Divider()

            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Label("TURN Relay", systemImage: "network")
                        .font(.subheadline.weight(.semibold))
                    Spacer()
                    Button {
                        turnRelayObservationCopied = false
                        turnRelayObservationSaved = false
                        evidenceExportError = nil
                        Task { await model.captureTurnRelayObservation() }
                    } label: {
                        Label("Capture", systemImage: "antenna.radiowaves.left.and.right")
                    }
                    .buttonStyle(.bordered)
                    .disabled(!model.canUseRoomControls || model.isCapturingTurnRelayObservation || model.turnRelayNetwork.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                    Button {
                        copyTurnRelayObservationJSON()
                    } label: {
                        Label(turnRelayObservationCopied ? "Copied" : "Copy", systemImage: turnRelayObservationCopied ? "checkmark" : "doc.on.doc")
                    }
                    .buttonStyle(.bordered)
                    .disabled(model.latestTurnRelayObservationJSON == nil)

                    Button {
                        saveTurnRelayObservationJSON()
                    } label: {
                        Label(turnRelayObservationSaved ? "Saved" : "Save", systemImage: turnRelayObservationSaved ? "checkmark.circle" : "square.and.arrow.down")
                    }
                    .buttonStyle(.bordered)
                    .disabled(model.latestTurnRelayObservationJSON == nil)
                }

                TextField("Network", text: $model.turnRelayNetwork)
                    .textFieldStyle(.roundedBorder)
                    .disabled(!model.canUseRoomControls)

                if let observation = model.latestTurnRelayObservation {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 116), spacing: 8)], spacing: 8) {
                        mediaEvidenceFlag("Relay", observation.selectedCandidate.relayCandidateSelected)
                        mediaEvidenceFlag("ICE", observation.iceReadiness.ok)
                    }
                    Text(turnRelayCandidateSummary(observation))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                    if let json = model.latestTurnRelayObservationJSON {
                        Text(json)
                            .font(.caption.monospaced())
                            .lineLimit(12)
                            .textSelection(.enabled)
                            .padding(8)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(.quaternary.opacity(0.18), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                    }
                }
            }

            if let evidenceExportError {
                Label(evidenceExportError, systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.red)
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

    private var scoutMemoryPanel: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Label("Scout", systemImage: "sparkles")
                    .font(.headline)
                Spacer()
                Text("\(model.memoryEntries.count) saved")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if !model.assistantEvents.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(Array(model.assistantEvents.suffix(4))) { event in
                        assistantRow(event)
                    }
                }
            } else {
                Text(model.canUseRoomControls ? "Scout is quiet" : "Join to load Scout")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            if let latestArchive = model.latestArchive {
                archiveRow(latestArchive)
            }

            if !model.recentMemoryEntries.isEmpty {
                Divider()
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(model.recentMemoryEntries) { entry in
                        memoryRow(entry)
                    }
                }
            }

            Divider()
            roomScoutComposer
            privateScoutChat
        }
    }

    private var roomScoutComposer: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Room Scout")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                TextField("Ask the room", text: $roomScoutDraft)
                    .textFieldStyle(.roundedBorder)
                    .disabled(!model.canUseRoomControls)
                Button {
                    let query = roomScoutDraft
                    roomScoutDraft = ""
                    Task { await model.askAssistant(query) }
                } label: {
                    Label("Ask", systemImage: "paperplane.fill")
                }
                .buttonStyle(.bordered)
                .disabled(!model.canUseRoomControls || roomScoutDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
    }

    private var privateScoutChat: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Private Scout")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer()
                Button {
                    Task { await model.resetScoutChat() }
                } label: {
                    Label("New", systemImage: "plus.message")
                }
                .buttonStyle(.bordered)
                .disabled(!model.canUseRoomControls)
            }

            if model.scoutChatEvents.isEmpty {
                Text(model.canUseRoomControls ? "Ask Scout privately" : "Join to chat privately with Scout")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(Array(model.scoutChatEvents.suffix(5))) { event in
                        scoutChatRow(event)
                    }
                }
            }

            HStack(spacing: 8) {
                TextField("Message Scout", text: $scoutChatDraft)
                    .textFieldStyle(.roundedBorder)
                    .disabled(!model.canUseRoomControls || model.isScoutChatSending)
                Button {
                    let text = scoutChatDraft
                    scoutChatDraft = ""
                    Task { await model.sendScoutChat(text) }
                } label: {
                    Label("Send", systemImage: "paperplane")
                }
                .buttonStyle(.borderedProminent)
                .disabled(!model.canUseRoomControls || model.isScoutChatSending || scoutChatDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
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

    private func mediaEvidenceFlag(_ title: String, _ passed: Bool) -> some View {
        HStack(spacing: 6) {
            Image(systemName: passed ? "checkmark.circle.fill" : "xmark.circle")
                .foregroundStyle(passed ? .green : .secondary)
            Text(title)
                .font(.caption.weight(.semibold))
                .lineLimit(1)
            Spacer(minLength: 0)
        }
        .padding(.vertical, 6)
        .padding(.horizontal, 8)
        .background(.quaternary.opacity(0.22), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private func mediaEvidenceCandidateSummary(_ evidence: NativeMediaEvidenceSnapshot) -> String {
        let candidate = evidence.selectedCandidate
        let parts = [
            candidate.protocol,
            candidate.networkType,
            candidate.localCandidateType,
            candidate.remoteCandidateType,
        ]
        if parts.allSatisfy({ $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }),
           candidate.currentRoundTripTime == 0 {
            return "Candidate stats unavailable"
        }
        let rtt = String(format: "%.0f ms", candidate.currentRoundTripTime * 1_000)
        return "Candidate \(candidate.protocol)/\(candidate.networkType) local \(candidate.localCandidateType) remote \(candidate.remoteCandidateType), RTT \(rtt)"
    }

    private func turnRelayCandidateSummary(_ observation: NativeTurnRelayObservation) -> String {
        let candidate = observation.selectedCandidate
        let rtt = String(format: "%.0f ms", candidate.currentRoundTripTime * 1_000)
        return "\(observation.network): \(candidate.relayProtocol) relay, local \(candidate.localCandidateType), remote \(candidate.remoteCandidateType), RTT \(rtt)"
    }

    private func copyMediaEvidenceJSON() {
        guard let json = model.latestMediaEvidenceJSON else { return }
        #if os(iOS)
        UIPasteboard.general.string = json
        #elseif os(macOS)
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(json, forType: .string)
        #endif
        mediaEvidenceCopied = true
    }

    private func saveMediaEvidenceJSON() {
        guard let json = model.latestMediaEvidenceJSON,
              let filename = model.latestMediaEvidenceSuggestedFilename else { return }
        evidenceExportDocument = NativeEvidenceExportDocument(json: json)
        evidenceExportFilename = filename
        evidenceExportKind = .media
        evidenceExportError = nil
        isShowingEvidenceExporter = true
    }

    private func copyTurnRelayObservationJSON() {
        guard let json = model.latestTurnRelayObservationJSON else { return }
        #if os(iOS)
        UIPasteboard.general.string = json
        #elseif os(macOS)
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(json, forType: .string)
        #endif
        turnRelayObservationCopied = true
    }

    private func saveTurnRelayObservationJSON() {
        guard let json = model.latestTurnRelayObservationJSON,
              let filename = model.latestTurnRelayObservationSuggestedFilename else { return }
        evidenceExportDocument = NativeEvidenceExportDocument(json: json)
        evidenceExportFilename = filename
        evidenceExportKind = .turnRelay
        evidenceExportError = nil
        isShowingEvidenceExporter = true
    }

    private func handleEvidenceExport(_ result: Result<URL, Error>) {
        defer { evidenceExportKind = nil }
        switch result {
        case .success:
            evidenceExportError = nil
            switch evidenceExportKind {
            case .media:
                mediaEvidenceSaved = true
            case .turnRelay:
                turnRelayObservationSaved = true
            case nil:
                break
            }
        case .failure(let error):
            evidenceExportError = error.localizedDescription
        }
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

    private func assistantRow(_ event: AssistantEvent) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(assistantLabel(event))
                .font(.caption2.weight(.semibold))
                .foregroundStyle(.secondary)
            Text(event.displayText)
                .font(.callout)
                .lineLimit(3)
            if let url = model.assistantDownloadURL(for: event) {
                Link(destination: url) {
                    Label("Download archive", systemImage: "arrow.down.doc")
                }
                .font(.caption.weight(.semibold))
            }
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.2), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private func archiveRow(_ archive: MeetingArchiveResult) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "archivebox.fill")
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 4) {
                Text("Archive ready")
                    .font(.callout.weight(.semibold))
                Text(archive.summary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                if let url = model.latestArchiveDownloadURL {
                    Link(destination: url) {
                        Label("Download archive", systemImage: "arrow.down.doc")
                    }
                    .font(.caption.weight(.semibold))
                }
            }
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.22), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private func memoryRow(_ entry: MemoryEntry) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 6) {
                Text(memoryKindLabel(entry.kind))
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.secondary)
                if let speaker = entry.metadata?["speaker"], !speaker.isEmpty {
                    Text(speaker)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
            Text(memoryDisplayText(entry))
                .font(.caption)
                .lineLimit(2)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func scoutChatRow(_ event: ScoutChatEvent) -> some View {
        let isUser = event.kind == "query"
        let isError = event.kind == "error"
        return VStack(alignment: isUser ? .trailing : .leading, spacing: 3) {
            Text(scoutChatLabel(event.kind))
                .font(.caption2.weight(.semibold))
                .foregroundStyle(.secondary)
            Text(event.displayText)
                .font(.caption)
                .lineLimit(4)
            if let thread = event.thread {
                VStack(alignment: isUser ? .trailing : .leading, spacing: 3) {
                    Text(scoutThreadTitle(thread))
                        .font(.caption.weight(.semibold))
                    Text(thread.query)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                    if let actions = event.actions ?? thread.actions, !actions.isEmpty {
                        Text(actions.map(scoutActionLabel).joined(separator: " · "))
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    }
                }
                .padding(.top, 2)
            } else if let actions = event.actions, !actions.isEmpty {
                Text(actions.map(scoutActionLabel).joined(separator: " · "))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 7)
        .padding(.horizontal, 10)
        .frame(maxWidth: .infinity, alignment: isUser ? .trailing : .leading)
        .background(
            isError ? Color.red.opacity(0.12) : (isUser ? Color.accentColor.opacity(0.12) : Color.secondary.opacity(0.1)),
            in: RoundedRectangle(cornerRadius: 8, style: .continuous)
        )
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

    private func assistantLabel(_ event: AssistantEvent) -> String {
        let kind = (event.kind ?? "status").trimmingCharacters(in: .whitespacesAndNewlines)
        return kind.isEmpty ? "status" : kind
    }

    private func memoryKindLabel(_ kind: String) -> String {
        switch kind {
        case "answer":
            return "answer"
        case "archive":
            return "archive"
        case "brain":
            return "brain"
        case "board_update":
            return "board"
        case "os_artifact":
            return "artifact"
        default:
            return "transcript"
        }
    }

    private func memoryDisplayText(_ entry: MemoryEntry) -> String {
        let raw = entry.text.trimmingCharacters(in: .whitespacesAndNewlines)
        if entry.kind == "archive" {
            return raw
                .replacingOccurrences(of: " item(s)", with: " items")
                .replacingOccurrences(of: " card(s)", with: " cards")
                .replacingOccurrences(of: " participant(s)", with: " participants")
        }
        return raw
    }

    private func scoutChatLabel(_ kind: String) -> String {
        switch kind {
        case "query":
            return "you"
        case "answer":
            return "scout"
        case "thread":
            return "thread"
        case "error":
            return "error"
        case "reset":
            return "new thread"
        default:
            return kind.isEmpty ? "scout" : kind
        }
    }

    private func scoutThreadTitle(_ thread: ScoutChatThread) -> String {
        let mode = thread.mode.trimmingCharacters(in: .whitespacesAndNewlines)
        let status = thread.status.trimmingCharacters(in: .whitespacesAndNewlines)
        let title = thread.artifact?.metadata?["title"]?.trimmingCharacters(in: .whitespacesAndNewlines)
        if let title, !title.isEmpty {
            return status.isEmpty ? title : "\(title) · \(status)"
        }
        if !mode.isEmpty && !status.isEmpty {
            return "\(mode) thread · \(status)"
        }
        return mode.isEmpty ? "Scout thread" : "\(mode) thread"
    }

    private func scoutActionLabel(_ action: AssistantAction) -> String {
        if let label = action.label?.trimmingCharacters(in: .whitespacesAndNewlines), !label.isEmpty {
            return label
        }
        let tool = action.tool?.trimmingCharacters(in: .whitespacesAndNewlines)
        let mode = action.mode?.trimmingCharacters(in: .whitespacesAndNewlines)
        switch (action.type, tool, mode) {
        case ("open_tool", let tool?, _):
            return "open \(tool)"
        case ("select_artifact", _, _):
            return "select artifact"
        case (_, _, let mode?):
            return mode
        default:
            return action.type.trimmingCharacters(in: .whitespacesAndNewlines)
        }
    }
}

private enum NativeEvidenceExportKind {
    case media
    case turnRelay
}

private struct NativeEvidenceExportDocument: FileDocument {
    static var readableContentTypes: [UTType] { [.json] }

    var json: String

    init(json: String = "") {
        self.json = json
    }

    init(configuration: ReadConfiguration) throws {
        if let data = configuration.file.regularFileContents,
           let json = String(data: data, encoding: .utf8) {
            self.json = json
        } else {
            json = ""
        }
    }

    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper {
        FileWrapper(regularFileWithContents: Data(json.utf8))
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
