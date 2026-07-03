package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	codexProposalStatusProposed  = "proposed"
	codexProposalStatusConfirmed = "confirmed"
	codexProposalStatusDismissed = "dismissed"
	// codexProposalHistoryLimit is how many proposals replay to a newly
	// admitted participant (pending cards plus recent resolutions).
	codexProposalHistoryLimit = 20
)

const (
	codexProposalActionConfirm = "confirm"
	codexProposalActionDismiss = "dismiss"
)

// proposeCodexTask executes the propose_codex_task tool: it records a kind
// codex_proposal memory entry (UI state — excluded from Scout search context
// and from the client memory timeline), broadcasts a codex_proposal card to
// the room, and posts an everyone-notification. It NEVER launches the agent
// thread itself; a human confirms via POST /assistant/proposals/{id}/action.
// proposedBy stamps the requesting identity (the private voice path passes
// the signed-in user's email); empty falls back to the shared "board_worker"
// provenance used by the board worker and the shared room voice.
func (app *kanbanBoardApp) proposeCodexTask(args map[string]any, proposedBy string) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}
	proposedBy = strings.TrimSpace(proposedBy)
	if proposedBy == "" {
		proposedBy = "board_worker"
	}
	title := canonicalizeBoardText(asString(args["title"]))
	if title == "" {
		return nil, false, fmt.Errorf("title is required")
	}
	mode := normalizeAgentThreadMode(asString(args["mode"]))
	if mode == "" {
		return nil, false, fmt.Errorf("mode must be one of artifacts, research, design, grill, or workflow")
	}
	query := canonicalizeBoardText(asString(args["query"]))
	if query == "" {
		return nil, false, fmt.Errorf("query is required")
	}

	id := durableTimestampID("codex-proposal", time.Now())
	text := fmt.Sprintf("Scout proposes %s task: %s — %s", assistantToolLabel(mode), title, query)
	metadata := map[string]string{
		"title":      title,
		"mode":       mode,
		"query":      query,
		"status":     codexProposalStatusProposed,
		"proposedBy": proposedBy,
	}
	// Board linkage captured at propose time: an explicit card_id wins,
	// otherwise the title fuzzy-matches an existing card. No match just means
	// no auto-advance later — never an error.
	if card, ok := app.matchBoardCard(title, asString(args["card_id"])); ok {
		metadata["cardId"] = card.ID
	}
	// Package linkage captured at propose time: package_id resolves by id or
	// name; an unknown package just means no binder auto-attach later.
	if packageRef := strings.TrimSpace(asString(args["package_id"])); packageRef != "" {
		if record, ok := app.findPackageByNameOrID(packageRef); ok {
			metadata["packageId"] = record.ID
		}
	}
	entry, appended, err := app.memory.appendCodexProposal(id, text, metadata)
	if err != nil {
		return nil, false, err
	}
	if !appended {
		return nil, false, fmt.Errorf("proposal was not saved")
	}

	payload := codexProposalPayload(entry)
	broadcastOfficeKanbanEvent("codex_proposal", payload)
	// Unified push channel: the same proposal on the typed stream (title only)
	// so surfaces beyond the room cards learn of it. Wave 8's approval
	// round-trip subscribes to these proposal events.
	broadcastOSEvent(osEvent{
		Kind:          osEventProposal,
		Ref:           entry.ID,
		Title:         title,
		OriginSurface: "room",
		Actor:         proposedBy,
	})
	// Everyone-notification: any signed-in user may confirm, so the durable
	// nudge is a broadcast; tool "room" routes the click to the room where
	// the proposal cards live.
	if _, err := app.createNotification("", notificationKindTask, "Scout proposes: "+title+" — confirm to launch", "room", "", "", false); err != nil {
		log.Errorf("Failed to create codex proposal notification for %s: %v", entry.ID, err)
	}

	// changed=false: proposing never mutates the board itself.
	return map[string]any{
		"ok":       true,
		"proposal": payload,
	}, false, nil
}

// codexProposalPayload shapes a codex_proposal memory entry into the wire
// payload used by the codex_proposal broadcast, the codex_proposals admission
// replay, and the proposal action endpoint.
func codexProposalPayload(entry meetingMemoryEntry) map[string]any {
	payload := map[string]any{
		"id":         entry.ID,
		"title":      entry.Metadata["title"],
		"mode":       entry.Metadata["mode"],
		"query":      entry.Metadata["query"],
		"status":     entry.Metadata["status"],
		"proposedBy": entry.Metadata["proposedBy"],
		"createdAt":  entry.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	for _, key := range []string{"confirmedBy", "dismissedBy", "threadId", "threadArtifactId", "resolvedAt", "cardId", "packageId"} {
		if value := strings.TrimSpace(entry.Metadata[key]); value != "" {
			payload[key] = value
		}
	}
	return payload
}

// codexProposalsSnapshot returns the newest proposals, oldest first, shaped
// like codex_proposal broadcast payloads.
func (app *kanbanBoardApp) codexProposalsSnapshot(limit int) []map[string]any {
	proposals := []map[string]any{}
	if app == nil || app.memory == nil {
		return proposals
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindCodexProposal, limit) {
		proposals = append(proposals, codexProposalPayload(entry))
	}
	return proposals
}

// resolveCodexProposal applies a confirm or dismiss from a signed-in user.
// Confirm launches an agent thread as the confirming user (the full existing
// runner/artifact pipeline); dismiss just settles the proposal. Transitions
// only happen from status "proposed" — a proposal that is already resolved
// reports its settled state without launching anything again, which makes a
// double confirm idempotent. Returns (payload, launched, error).
func (app *kanbanBoardApp) resolveCodexProposal(id string, action string, userName string, userEmail string) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("proposals are unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, fmt.Errorf("proposal id is required")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if action != codexProposalActionConfirm && action != codexProposalActionDismiss {
		return nil, false, fmt.Errorf("action must be %q or %q", codexProposalActionConfirm, codexProposalActionDismiss)
	}

	app.proposalMu.Lock()
	defer app.proposalMu.Unlock()

	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, id)
	if !ok {
		return nil, false, fmt.Errorf("proposal not found")
	}
	if entry.Metadata["status"] != codexProposalStatusProposed {
		return codexProposalPayload(entry), false, nil
	}

	resolvedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if action == codexProposalActionConfirm {
		// Persist the confirmed transition BEFORE launching: if the launch
		// bookkeeping fails afterwards the proposal is already settled, so a
		// retry cannot double-launch. If the launch itself fails, revert to
		// proposed so the human can confirm again.
		updated, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindCodexProposal, id, entry.Text, map[string]string{
			"status":           codexProposalStatusConfirmed,
			"confirmedBy":      strings.TrimSpace(userName),
			"confirmedByEmail": normalizeAccountEmail(userEmail),
			"resolvedAt":       resolvedAt,
		})
		if err != nil {
			return nil, false, err
		}

		// Room-confirmed proposals are the room's work: completion posts the
		// artifact card back into the origin meeting's chat.
		thread, err := app.launchAgentThreadWithOrigin(entry.Metadata["mode"], entry.Metadata["query"], userName, map[string]string{
			"originKind":      agentThreadOriginRoom,
			"originId":        id,
			"originMeetingId": app.memory.currentMeetingID(),
		})
		if err != nil {
			if _, _, revertErr := app.memory.updateEntryWithMetadata(meetingMemoryKindCodexProposal, id, entry.Text, map[string]string{
				"status":           codexProposalStatusProposed,
				"confirmedBy":      "",
				"confirmedByEmail": "",
				"resolvedAt":       "",
			}); revertErr != nil {
				log.Errorf("Failed to revert codex proposal %s after launch error: %v", id, revertErr)
			}
			return nil, false, err
		}

		// Stamp the thread linkage in a follow-up update. The proposal is
		// already confirmed, so a failure here only loses the linkage — it can
		// never re-open the proposal for a second launch.
		threadStamp := map[string]string{
			"threadId":         thread.ID,
			"threadArtifactId": thread.Artifact.ID,
		}
		// Board linkage: the propose-time cardId wins; when it is absent, retry
		// the fuzzy match at confirm time (the board worker may have created
		// the card in a later pass than the proposal).
		cardID := strings.TrimSpace(entry.Metadata["cardId"])
		if cardID == "" {
			if card, ok := app.matchBoardCard(entry.Metadata["title"], ""); ok {
				cardID = card.ID
				threadStamp["cardId"] = cardID
			}
		}
		stamped, _, stampErr := app.memory.updateEntryWithMetadata(meetingMemoryKindCodexProposal, id, entry.Text, threadStamp)
		if stampErr != nil {
			log.Errorf("Failed to stamp thread linkage on codex proposal %s: %v", id, stampErr)
		} else {
			updated = stamped
		}
		// Bidirectional stamps + auto-advance. Mirrors the linkage-stamp-
		// after-commit pattern above: a failure only loses the link, it
		// never re-opens the settled proposal. The propose-time packageId
		// rides onto the artifact so the terminal hook can auto-attach the
		// finished deliverable to its venture package.
		artifactStamp := map[string]string{}
		if cardID != "" {
			artifactStamp["boardCardId"] = cardID
			artifactStamp["proposalId"] = id
		}
		if packageID := strings.TrimSpace(entry.Metadata["packageId"]); packageID != "" {
			artifactStamp["packageId"] = packageID
			artifactStamp["proposalId"] = id
		}
		if len(artifactStamp) > 0 {
			if _, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", thread.Artifact.Text, "", artifactStamp); err != nil {
				log.Errorf("Failed to stamp board linkage on artifact %s: %v", thread.Artifact.ID, err)
			}
		}
		if cardID != "" {
			app.advanceLinkedCard(cardID, kanbanStatusInProgress, "confirmed: "+entry.Metadata["title"])
		}

		payload := codexProposalPayload(updated)
		broadcastOfficeKanbanEvent("codex_proposal", payload)
		return payload, true, nil
	}

	updated, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindCodexProposal, id, entry.Text, map[string]string{
		"status":           codexProposalStatusDismissed,
		"dismissedBy":      strings.TrimSpace(userName),
		"dismissedByEmail": normalizeAccountEmail(userEmail),
		"resolvedAt":       resolvedAt,
	})
	if err != nil {
		return nil, false, err
	}

	payload := codexProposalPayload(updated)
	broadcastOfficeKanbanEvent("codex_proposal", payload)
	return payload, false, nil
}

// assistantProposalActionHandler serves POST /assistant/proposals/{id}/action
// with the same origin + session guards as the chat-threads handlers. Any
// signed-in user may confirm or dismiss.
func assistantProposalActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "proposals are unavailable")
		return
	}

	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/assistant/proposals/"), "/")
	parts := strings.Split(suffix, "/")
	if suffix == "" || len(parts) != 2 || parts[0] == "" || parts[1] != "action" {
		http.NotFound(w, r)
		return
	}

	payload := struct {
		Action string `json:"action"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read proposal action")
		return
	}

	proposal, launched, err := kanbanApp.resolveCodexProposal(parts[0], payload.Action, user.Name, user.Email)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"proposal": proposal,
		"launched": launched,
	})
}
