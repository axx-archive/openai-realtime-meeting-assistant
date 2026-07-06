package main

// The in-thread goal reporter (packaging OS P0-2/P0-3): a running goal
// narrates its stage deliverables — and its checkpoint parks — into the chat
// thread that launched it, as they happen. Persistence and the chat_thread
// fan-out ride commitScoutChatThreadMessages, so every viewer of the origin
// thread sees the run unfold without polling the artifact library.

import (
	"fmt"
	"strings"
	"time"
)

// goalStageRoleReportable gates the stage narration by role: stages that
// produce a deliverable worth reading (panel/judges/synthesizer/writer/compile)
// post; plumbing stages (gate/render/human_checkpoint — and free-form subtasks,
// which carry no role) stay quiet.
func goalStageRoleReportable(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case processRolePanel, processRoleJudges, processRoleSynthesizer, processRoleWriter, processRoleCompile:
		return true
	}
	return false
}

// goalOriginChatThread resolves the chat thread a goal parent narrates into,
// reusing deliverArtifactToOrigin's channel guards verbatim: an archived
// thread or a non-public channel never accepts an owner-context write.
// private_thread origins commit as the owner the same way. Room/tool/absent
// origins have no chat thread — the caller silently skips.
func (app *kanbanBoardApp) goalOriginChatThread(parent meetingMemoryEntry) (scoutChatThreadRecord, bool) {
	originKind := strings.TrimSpace(parent.Metadata["originKind"])
	originID := strings.TrimSpace(parent.Metadata["originId"])
	if originID == "" || (originKind != agentThreadOriginChannel && originKind != agentThreadOriginPrivateThread) {
		return scoutChatThreadRecord{}, false
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, originID)
	if !ok {
		return scoutChatThreadRecord{}, false
	}
	thread, ok := decodeScoutChatThreadEntry(entry)
	if !ok {
		return scoutChatThreadRecord{}, false
	}
	if thread.ArchivedAt != "" {
		return scoutChatThreadRecord{}, false
	}
	if originKind == agentThreadOriginChannel && scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return scoutChatThreadRecord{}, false
	}
	return thread, true
}

// postGoalOriginMessage commits one scout message into the goal's origin chat
// thread. Silent skip on any guard failure — the creator notification remains
// the fallback signal.
func (app *kanbanBoardApp) postGoalOriginMessage(parentID string, message scoutChatMessageRecord) {
	if app == nil || app.memory == nil {
		return
	}
	parent, ok := app.osArtifactByID(strings.TrimSpace(parentID))
	if !ok {
		return
	}
	thread, ok := app.goalOriginChatThread(parent)
	if !ok {
		return
	}
	message.ID = fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano())
	message.Role = firstNonEmptyString(message.Role, "scout")
	message.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, message); err != nil {
		log.Errorf("Failed to post goal %s message to chat thread %s: %v", parentID, thread.ID, err)
	}
}

// postGoalStageMessage narrates one completed stage deliverable into the
// goal's origin thread as it lands: one line plus a tappable ref to the stage
// artifact. Role-gated by goalStageRoleReportable.
func (app *kanbanBoardApp) postGoalStageMessage(parentID string, stageTitle string, role string, artifactID string, line string) {
	if !goalStageRoleReportable(role) {
		return
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(line) == "" {
		return
	}
	app.postGoalOriginMessage(parentID, scoutChatMessageRecord{
		Kind: "artifact",
		Role: "scout",
		Text: line,
		Thread: &scoutChatThreadRef{
			ArtifactID: strings.TrimSpace(artifactID),
			Mode:       "workflow",
			Query:      stageTitle,
			Status:     "complete",
		},
	})
}

// goalStageMessageLine builds the one narration line a landed stage posts:
// "<title> is in — <note>", with a "(revision N)" suffix when the stage
// re-completed after a send-back or gate redo.
func goalStageMessageLine(title string, note string, revisions int) string {
	line := strings.TrimSpace(title) + " is in"
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		line += " — " + trimmed
	}
	if revisions > 0 {
		line += fmt.Sprintf(" (revision %d)", revisions)
	}
	return line
}

// postGoalCheckpointMessage posts a checkpoint park into the origin thread as
// the call-to-action: a kind:"thread" ref to the GOAL PARENT artifact, so the
// client's latest-wins rule mounts the full goalcard (choice card included) at
// the bottom of the thread. The ref ID carries the goal's agentThreadID
// (metadata["threadId"]) so scoutChatThreadHasAgentRef keeps deduping the
// final origin delivery.
func (app *kanbanBoardApp) postGoalCheckpointMessage(parentID string, question string) {
	if app == nil || app.memory == nil {
		return
	}
	parent, ok := app.osArtifactByID(strings.TrimSpace(parentID))
	if !ok {
		return
	}
	app.postGoalOriginMessage(parentID, scoutChatMessageRecord{
		Kind: "thread",
		Role: "scout",
		Text: "parked — " + compactAssistantLine(question),
		Thread: &scoutChatThreadRef{
			ID:         strings.TrimSpace(parent.Metadata["threadId"]),
			Mode:       "goal",
			Query:      firstNonEmptyString(strings.TrimSpace(parent.Metadata["threadQuery"]), strings.TrimSpace(parent.Metadata["objective"])),
			Status:     codexJobStatusApprovalRequired,
			ArtifactID: parent.ID,
		},
	})
}
