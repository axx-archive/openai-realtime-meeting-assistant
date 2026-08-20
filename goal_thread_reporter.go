package main

// The in-thread goal reporter (packaging OS P0-2/P0-3): a running goal
// narrates its stage deliverables — and its checkpoint parks — into the chat
// thread that launched it, as they happen. Persistence and the chat_thread
// fan-out ride commitScoutChatThreadMessages, so every viewer of the origin
// thread sees the run unfold without polling the artifact library.

import (
	"encoding/json"
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
	messageID := fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano())
	if err := app.postGoalOriginMessageOnce(parentID, messageID, message); err != nil {
		log.Errorf("Failed to post goal %s message: %v", parentID, err)
	}
}

// postGoalOriginMessageOnce persists a caller-bound message ID and treats an
// existing ID as the acknowledged effect. Checkpoint finalization uses it so
// a crash after chat persistence cannot append a duplicate manifest on boot.
func (app *kanbanBoardApp) postGoalOriginMessageOnce(parentID, messageID string, message scoutChatMessageRecord) error {
	if app == nil || app.memory == nil {
		return fmt.Errorf("goal origin is unavailable")
	}
	parent, ok := app.osArtifactByID(strings.TrimSpace(parentID))
	if !ok {
		return fmt.Errorf("goal artifact not found")
	}
	thread, ok := app.goalOriginChatThread(parent)
	if !ok {
		return nil
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return fmt.Errorf("goal message id is required")
	}
	for _, existing := range thread.Messages {
		if existing.ID == messageID {
			return nil
		}
	}
	message.ID = messageID
	message.Role = firstNonEmptyString(message.Role, "scout")
	message.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, message); err != nil {
		return err
	}
	return nil
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

func scoutChatCheckpointRefForArtifact(artifact meetingMemoryEntry) *scoutChatWorkCheckpointRef {
	if strings.TrimSpace(artifact.Metadata["mode"]) != "goal" ||
		strings.ToLower(strings.TrimSpace(firstNonEmptyString(artifact.Metadata["threadStatus"], artifact.Metadata["status"]))) != codexJobStatusApprovalRequired {
		return nil
	}
	checkpoint := goalProcessCheckpoint{}
	if err := json.Unmarshal([]byte(artifact.Metadata["checkpoint"]), &checkpoint); err != nil ||
		strings.TrimSpace(checkpoint.ResolvedAt) != "" || strings.TrimSpace(checkpoint.Question) == "" {
		return nil
	}
	checkpointID := goalCheckpointID(artifact.ID, &checkpoint)
	if len(checkpoint.Options) > processCheckpointMaxOptions {
		return nil
	}
	ref := &scoutChatWorkCheckpointRef{
		ID: checkpointID, StageID: strings.TrimSpace(checkpoint.StageID), Question: trimForStorage(strings.TrimSpace(checkpoint.Question), 480),
	}
	seenLabels := map[string]bool{}
	for index, option := range checkpoint.Options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		// Labels are authority-bearing human choices. Never truncate them into
		// an ambiguous display value, and fail closed on legacy/corrupt records
		// that collide after the same normalization used by choice matching.
		if len([]rune(label)) > processCheckpointMaxLabelRunes || seenLabels[strings.ToLower(label)] {
			return nil
		}
		seenLabels[strings.ToLower(label)] = true
		ref.Options = append(ref.Options, scoutChatWorkCheckpointOptionRef{
			ID: goalCheckpointOptionID(checkpointID, option, index), Label: label, Action: option.action(),
		})
	}
	return ref
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
	thread, hasOrigin := app.goalOriginChatThread(parent)
	threadID := strings.TrimSpace(parent.Metadata["threadId"])
	if hasOrigin && threadID != "" && scoutChatThreadHasAgentRef(thread, threadID) {
		// Public work already owns one root channel card. Project the checkpoint
		// onto that durable card instead of appending another card whose choices
		// can drift away from the run it resumes.
		app.updateScoutChatThreadRefs(threadID, codexJobStatusApprovalRequired, parent.ID)
		return
	}
	app.postGoalOriginMessage(parentID, scoutChatMessageRecord{
		Kind: "thread",
		Role: "scout",
		Text: "parked — " + compactAssistantLine(question),
		Thread: &scoutChatThreadRef{
			ID:         threadID,
			Mode:       "goal",
			Query:      firstNonEmptyString(strings.TrimSpace(parent.Metadata["threadQuery"]), strings.TrimSpace(parent.Metadata["objective"])),
			Status:     codexJobStatusApprovalRequired,
			ArtifactID: parent.ID,
			Checkpoint: scoutChatCheckpointRefForArtifact(parent),
		},
	})
}

// postGoalSalvagedDraftMessage posts a salvaged draft into the origin thread
// when a goal parks on NEEDS ATTENTION. This ensures the draft is visible
// in-thread instead of just being attached to the package silently.
func (app *kanbanBoardApp) postGoalSalvagedDraftMessage(parentID string, draftArtifactID string, gap string) {
	if app == nil || app.memory == nil {
		return
	}
	draftArtifactID = strings.TrimSpace(draftArtifactID)
	if draftArtifactID == "" {
		return
	}
	draft, ok := app.osArtifactByID(draftArtifactID)
	if !ok {
		return
	}
	draftTitle := firstNonEmptyString(strings.TrimSpace(draft.Metadata["title"]), "Draft")
	gap = strings.TrimSpace(gap)
	text := draftTitle + " is saved — needs attention"
	if gap != "" {
		text += ": " + compactAssistantLine(gap)
	}
	app.postGoalOriginMessage(parentID, scoutChatMessageRecord{
		Kind: "artifact",
		Role: "scout",
		Text: text,
		Thread: &scoutChatThreadRef{
			ArtifactID: draftArtifactID,
			Mode:       firstNonEmptyString(strings.TrimSpace(draft.Metadata["mode"]), "artifacts"),
			Query:      draftTitle,
			Status:     "needs_attention",
		},
	})
}
