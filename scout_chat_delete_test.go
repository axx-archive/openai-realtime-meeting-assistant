package main

// Card 073 — delete affordance for misplaced messages, server half. A user
// may remove THEIR OWN message from a scout thread (private or public
// channel) and from the room chat transcript; identity comes from the
// session, so nobody can delete someone else's words. An ordinary generated
// Scout answer may be causally removed with its source message, while durable
// work and unrelated Scout messages survive.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func seedScoutChatUserMessage(t *testing.T, threadID string, viewerEmail string, id string, authorEmail string, text string) {
	t.Helper()
	_, err := kanbanApp.commitScoutChatThreadMessages(viewerEmail, threadID, scoutChatMessageRecord{
		ID:          id,
		Kind:        "message",
		Role:        "user",
		Text:        text,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		AuthorEmail: normalizeAccountEmail(authorEmail),
	})
	if err != nil {
		t.Fatalf("seed message %s: %v", id, err)
	}
}

func TestScoutChatThreadMessageDeleteOwnMessagesOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-aj", "aj@shareability.com", "wrong channel, meant this for ops")
	seedScoutChatUserMessage(t, channel.ID, "tim@shareability.com", "msg-tim", "tim@shareability.com", "keeping this one")
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID:        "msg-scout",
		Kind:      "message",
		Role:      "scout",
		Text:      "noted",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed scout reply: %v", err)
	}

	// Authz: another signed-in user cannot delete aj's message.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("tim@shareability.com", channel.ID, "msg-aj"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("cross-user delete err=%v, want the own-messages refusal", err)
	}
	// Scout's committed reply is nobody's to delete.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-scout"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("scout-reply delete err=%v, want the own-messages refusal", err)
	}
	if thread, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", channel.ID); err != nil || len(thread.Messages) != 3 {
		t.Fatalf("messages=%d err=%v after refused deletes, want all 3 intact", len(thread.Messages), err)
	}

	// The author removes their own message; the persisted record loses it and
	// the preview recomputes from what remains.
	thread, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-aj")
	if err != nil {
		t.Fatalf("own delete: %v", err)
	}
	if len(thread.Messages) != 2 {
		t.Fatalf("messages=%d after delete, want 2", len(thread.Messages))
	}
	for _, message := range thread.Messages {
		if message.ID == "msg-aj" {
			t.Fatal("deleted message still present in the returned thread")
		}
	}
	if thread.Preview != "noted" {
		t.Fatalf("preview=%q, want it recomputed from the surviving newest text", thread.Preview)
	}
	persisted, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("re-read channel: %v", err)
	}
	if len(persisted.Messages) != 2 {
		t.Fatalf("persisted messages=%d, want the deletion durable", len(persisted.Messages))
	}

	// A miss is a 404-shaped error, not a silent success.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-aj"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("re-delete err=%v, want not found", err)
	}
}

func TestScoutChatAdminModeratesOnlyOrdinaryPublicAgentReplies(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	_ = loginAs(t, "tim@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "incident channel", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	kanbanApp.strideRuntime = runtime
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", channel.ID,
		scoutChatMessageRecord{ID: "agent-answer", Kind: "message", Role: "scout", Text: "incorrect answer", CreatedAt: createdAt},
		scoutChatMessageRecord{ID: "human-message", Kind: "message", Role: "user", Text: "keep my words", CreatedAt: createdAt, AuthorEmail: "tim@shareability.com"},
		scoutChatMessageRecord{ID: "agent-work", Kind: "message", Role: "scout", Text: "research running", CreatedAt: createdAt, Thread: &scoutChatThreadRef{ID: "work-1", Status: "running"}},
	); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	member := accountStore().findUser("tim@shareability.com")
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(member, channel.ID, "agent-answer", "bad routing incident"); err == nil || !strings.Contains(err.Error(), "admin-only") {
		t.Fatalf("member moderation err=%v, want admin-only", err)
	}
	admin := accountStore().findUser("aj@shareability.com")
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(admin, channel.ID, "human-message", "bad routing incident"); err == nil || !strings.Contains(err.Error(), "public agent replies") {
		t.Fatalf("human moderation err=%v, want narrow agent-only refusal", err)
	}
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(admin, channel.ID, "agent-work", "bad routing incident"); err == nil || !strings.Contains(err.Error(), "public agent replies") {
		t.Fatalf("durable work moderation err=%v, want narrow ordinary-reply refusal", err)
	}

	thread, receipt, err := kanbanApp.moderateScoutChatThreadMessage(admin, channel.ID, "agent-answer", "bad routing incident")
	if err != nil {
		t.Fatalf("admin moderation: %v", err)
	}
	if scoutChatMessageIndex(thread, "agent-answer") >= 0 || scoutChatMessageIndex(thread, "human-message") < 0 || scoutChatMessageIndex(thread, "agent-work") < 0 {
		t.Fatalf("moderation removed the wrong records: %+v", thread.Messages)
	}
	if receipt.ThreadID != channel.ID || receipt.MessageID != "agent-answer" || receipt.ActorEmail != "aj@shareability.com" || len(receipt.ReasonDigest) != 64 || receipt.DeletedAt == "" {
		t.Fatalf("moderation receipt=%+v", receipt)
	}
	if err := runtime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), strideRuntimePrincipalForEmail("tim@shareability.com"))
		if projectErr != nil {
			return projectErr
		}
		for _, row := range projection {
			if row.SourceID == "agent-answer" {
				t.Fatalf("moderated agent answer survived canonical projection: %+v", row)
			}
		}
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		var latest ConversationEvent
		for _, record := range snapshot.Events {
			if record.Append.Event.SourceID == "agent-answer" {
				latest = record.Append.Event
			}
		}
		if latest.EventType != "delete" || latest.AuthorPrincipal != strideRuntimePrincipalForEmail("aj@shareability.com") {
			t.Fatalf("moderation audit event=%+v", latest)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify canonical moderation projection: %v", err)
	}

	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "private", "private")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", private.ID, scoutChatMessageRecord{ID: "private-agent", Kind: "message", Role: "scout", Text: "private", CreatedAt: createdAt}); err != nil {
		t.Fatalf("seed private thread: %v", err)
	}
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(admin, private.ID, "private-agent", "bad routing incident"); err == nil || !strings.Contains(err.Error(), "public agent replies") {
		t.Fatalf("private moderation err=%v, want public-only refusal", err)
	}
}

func TestScoutChatAdminSupersedesOnlyWithExactVerifiedReplacementAndPreservesArtifacts(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "Bonfire Chat", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	kanbanApp.strideRuntime = runtime
	admin := accountStore().findUser("aj@shareability.com")

	artifactMetadata := func(threadID, status string) map[string]string {
		return map[string]string{
			"threadId": threadID, "mode": "research", "status": status, "threadStatus": status,
			"originKind": agentThreadOriginChannel, "originId": channel.ID, "requestedBy": admin.Email,
		}
	}
	targetMetadata := artifactMetadata("target-run", "error")
	targetMetadata["currentStage"] = "gate_before_shipping"
	targetMetadata["reviewGate"] = "blocked"
	targetMetadata["progressPercent"] = "72"
	targetMetadata["error"] = "max_output_truncation"
	replacementMetadata := artifactMetadata("replacement-run", "complete")
	replacementMetadata["currentStage"] = "verify_goal_completed"
	replacementMetadata["reviewGate"] = "passed"
	replacementMetadata["progressPercent"] = "100"
	if _, _, err := kanbanApp.memory.appendOSArtifact("target-artifact", "bounded failed result", targetMetadata); err != nil {
		t.Fatalf("append target artifact: %v", err)
	}
	if _, _, err := kanbanApp.memory.appendOSArtifact("replacement-artifact", "decision-grade verified report", replacementMetadata); err != nil {
		t.Fatalf("append replacement artifact: %v", err)
	}
	targetCreatedAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	replacementCreatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := kanbanApp.commitScoutChatThreadMessages(admin.Email, channel.ID,
		scoutChatMessageRecord{ID: "target-work", Kind: "thread", Role: "scout", Text: "old work", CreatedAt: targetCreatedAt, Thread: &scoutChatThreadRef{ID: "target-run", Mode: "research", Status: "error", ArtifactID: "target-artifact"}},
		scoutChatMessageRecord{ID: "replacement-work", Kind: "thread", Role: "scout", Text: "new work", CreatedAt: replacementCreatedAt, Thread: &scoutChatThreadRef{ID: "replacement-run", Mode: "research", Status: "complete", ArtifactID: "replacement-artifact"}},
	); err != nil {
		t.Fatalf("seed work cards: %v", err)
	}

	beforeTarget, ok := kanbanApp.osArtifactByID("target-artifact")
	if !ok {
		t.Fatal("target artifact missing before supersession")
	}
	beforeReplacement, ok := kanbanApp.osArtifactByID("replacement-artifact")
	if !ok {
		t.Fatal("replacement artifact missing before supersession")
	}
	beforeReplacementDigest, _ := scoutChatWorkArtifactDigest(beforeReplacement)

	// A stale chat ref cannot authorize removal when the durable target is active.
	activeTarget := beforeTarget
	activeTarget.Metadata = cloneStringMap(beforeTarget.Metadata)
	activeTarget.Metadata["status"] = "running"
	activeTarget.Metadata["threadStatus"] = "running"
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(activeTarget.ID, "", activeTarget.Text, "test", activeTarget.Metadata); err != nil {
		t.Fatalf("make target active: %v", err)
	}
	if _, _, err := kanbanApp.supersedeScoutChatTerminalWork(admin, channel.ID, "target-work", "replacement-work", "superseded by verified replacement"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale terminal status err=%v, want refusal", err)
	}
	activeTarget.Metadata["status"] = "error"
	activeTarget.Metadata["threadStatus"] = "error"
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(activeTarget.ID, "", activeTarget.Text, "test", activeTarget.Metadata); err != nil {
		t.Fatalf("restore terminal target: %v", err)
	}
	terminalTarget, ok := kanbanApp.osArtifactByID("target-artifact")
	if !ok {
		t.Fatal("terminal target missing before supersession")
	}
	terminalTargetDigest, _ := scoutChatWorkArtifactDigest(terminalTarget)

	supersedeAs := func(email, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+channel.ID+"/messages/target-work/supersede", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, req)
		return recorder
	}
	requestBody := `{"replacementMessageId":"replacement-work","reason":"superseded by verified replacement"}`
	if recorder := supersedeAs("tim@shareability.com", requestBody); recorder.Code != http.StatusForbidden {
		t.Fatalf("non-admin supersession status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := supersedeAs(admin.Email, `{"replacementMessageId":"replacement-work","reason":"superseded","extra":true}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field supersession status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := supersedeAs(admin.Email, requestBody+` {}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing-json supersession status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stableSnapshotPath := runtime.config.SnapshotPath
	runtime.config.SnapshotPath = t.TempDir()
	recorder := supersedeAs(admin.Email, requestBody)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("pending supersede route status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(admin.Email, channel.ID)
	if err != nil {
		t.Fatalf("read superseded thread: %v", err)
	}
	if len(thread.ModerationReceipts) != 1 {
		t.Fatalf("moderation receipts=%d, want one", len(thread.ModerationReceipts))
	}
	receipt := thread.ModerationReceipts[0]
	if receipt.ProjectionState != scoutChatModerationPending {
		t.Fatalf("supersession receipt state=%q, want pending before recovery", receipt.ProjectionState)
	}
	_ = runtime.Close()
	config.SnapshotPath = stableSnapshotPath
	runtime, err = NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("restart STRIDE runtime for supersession recovery: %v", err)
	}
	kanbanApp.strideRuntime = runtime
	kanbanApp.recoverScoutChatModerations()
	thread, _, err = kanbanApp.scoutChatThreadByID(admin.Email, channel.ID)
	if err != nil || len(thread.ModerationReceipts) != 1 {
		t.Fatalf("read recovered supersession thread receipts=%d err=%v", len(thread.ModerationReceipts), err)
	}
	receipt = thread.ModerationReceipts[0]
	if scoutChatMessageIndex(thread, "target-work") >= 0 || scoutChatMessageIndex(thread, "replacement-work") < 0 {
		t.Fatalf("supersession removed the wrong card: %+v", thread.Messages)
	}
	if receipt.ProjectionState != scoutChatModerationComplete || receipt.TargetWork == nil || receipt.ReplacementWork == nil ||
		receipt.TargetWork.ArtifactID != "target-artifact" || receipt.ReplacementWork.ArtifactID != "replacement-artifact" {
		t.Fatalf("work supersession receipt=%+v", receipt)
	}
	exactReference, err := scoutChatModerationReference(receipt)
	if err != nil {
		t.Fatalf("derive exact supersession reference: %v", err)
	}
	latest, found, err := kanbanApp.latestSTRIDETeamChatEvent(channel.ID, "target-work")
	if err != nil || !found || latest.EventType != "delete" || !strideConversationEventHasReference(latest, exactReference) {
		t.Fatalf("canonical supersession event=%+v found=%v err=%v", latest, found, err)
	}
	serializedReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal work supersession receipt: %v", err)
	}
	for _, forbidden := range []string{"old work", "bounded failed result", "gate_before_shipping", "max_output_truncation"} {
		if strings.Contains(string(serializedReceipt), forbidden) {
			t.Fatalf("body/progress value %q leaked into body-free receipt: %s", forbidden, serializedReceipt)
		}
	}
	afterTarget, targetFound := kanbanApp.osArtifactByID("target-artifact")
	afterReplacement, replacementFound := kanbanApp.osArtifactByID("replacement-artifact")
	afterTargetDigest, _ := scoutChatWorkArtifactDigest(afterTarget)
	afterReplacementDigest, _ := scoutChatWorkArtifactDigest(afterReplacement)
	if !targetFound || !replacementFound || beforeReplacementDigest != afterReplacementDigest || afterTargetDigest == "" {
		t.Fatalf("artifacts changed or disappeared: target=%v replacement=%v", targetFound, replacementFound)
	}
	// The successful supersession must preserve the exact current terminal
	// artifact image established immediately before the projection mutation.
	if terminalTargetDigest != afterTargetDigest {
		t.Fatalf("target preservation digest terminal=%s after=%s", terminalTargetDigest, afterTargetDigest)
	}
	replayedThread, replayedReceipt, err := kanbanApp.supersedeScoutChatTerminalWork(admin, channel.ID, "target-work", "replacement-work", "superseded by verified replacement")
	if err != nil || replayedReceipt.OperationID != receipt.OperationID || scoutChatMessageIndex(replayedThread, "target-work") >= 0 {
		t.Fatalf("lost-response replay thread=%+v receipt=%+v err=%v", replayedThread, replayedReceipt, err)
	}

	// Restart from the same durable memory file: the replacement card and both
	// artifacts survive while the superseded chat projection stays absent.
	restarted := newKanbanBoardApp()
	restartedThread, _, err := restarted.scoutChatThreadByID(admin.Email, channel.ID)
	if err != nil {
		t.Fatalf("restart thread read: %v", err)
	}
	if scoutChatMessageIndex(restartedThread, "target-work") >= 0 || scoutChatMessageIndex(restartedThread, "replacement-work") < 0 {
		t.Fatalf("restart resurrected superseded work or lost replacement: %+v", restartedThread.Messages)
	}
	if _, ok := restarted.osArtifactByID("target-artifact"); !ok {
		t.Fatal("restart lost superseded target artifact")
	}
	if _, ok := restarted.osArtifactByID("replacement-artifact"); !ok {
		t.Fatal("restart lost replacement artifact")
	}
}

func TestScoutChatAdminModerationRouteRequiresAdminAndReason(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	_ = loginAs(t, "tim@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "incident channel", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	kanbanApp.strideRuntime = runtime
	if _, err := kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "agent-answer", Kind: "message", Role: "scout", Text: "incorrect answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed agent answer: %v", err)
	}
	moderateAs := func(email string, messageID string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+channel.ID+"/messages/"+messageID+"/moderate-delete", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, req)
		return recorder
	}

	if recorder := moderateAs("tim@shareability.com", "agent-answer", `{"reason":"bad routing incident"}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("member moderation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := moderateAs("aj@shareability.com", "agent-answer", `{"reason":""}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty reason status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder := moderateAs("aj@shareability.com", "agent-answer", `{"reason":"bad routing incident"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin moderation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK      bool                       `json:"ok"`
		Receipt scoutChatModerationReceipt `json:"receipt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode moderation response: %v", err)
	}
	if !payload.OK || payload.Receipt.MessageID != "agent-answer" || payload.Receipt.ActorEmail != "aj@shareability.com" {
		t.Fatalf("moderation response=%+v", payload)
	}

	if _, err := kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "agent-answer-pending", Kind: "message", Role: "scout", Text: "another incorrect answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed pending agent answer: %v", err)
	}
	runtime.config.SnapshotPath = t.TempDir()
	recorder = moderateAs("aj@shareability.com", "agent-answer-pending", `{"reason":"bad routing incident"}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("pending moderation status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusAccepted)
	}
	var pendingPayload struct {
		OK       bool                           `json:"ok"`
		Accepted bool                           `json:"accepted"`
		Receipt  scoutChatModerationReceiptView `json:"receipt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &pendingPayload); err != nil {
		t.Fatalf("decode pending moderation response: %v", err)
	}
	if pendingPayload.OK || !pendingPayload.Accepted || pendingPayload.Receipt.ProjectionState != scoutChatModerationPending {
		t.Fatalf("pending moderation falsely reported complete: %+v", pendingPayload)
	}
}

func TestScoutChatSupersessionDoesNotAcceptCanonicalDeleteWithoutExactReference(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	_ = loginAs(t, "tim@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "Bonfire Chat", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	kanbanApp.strideRuntime = runtime
	message := scoutChatMessageRecord{ID: "target-work", Kind: "message", Role: "scout", Text: "old work", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	channel, err = kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", channel.ID, message)
	if err != nil {
		t.Fatalf("commit source: %v", err)
	}
	prior, found, err := kanbanApp.latestSTRIDETeamChatEvent(channel.ID, message.ID)
	if err != nil || !found {
		t.Fatalf("read projected source found=%v err=%v", found, err)
	}
	if _, err := kanbanApp.projectSTRIDETeamChatMessage(channel, message, "delete", "aj@shareability.com"); err != nil {
		t.Fatalf("project unrelated delete: %v", err)
	}
	contentDigest, _ := strideChatMessageContentDigest(false, message)
	receipt := scoutChatModerationReceipt{
		OperationID: "chat_work_supersession_exact_ref", ThreadID: channel.ID, MessageID: message.ID,
		ActorEmail: "aj@shareability.com", ReasonDigest: sha256Hex([]byte("superseded")), TargetContentDigest: contentDigest,
		TargetEventID: prior.Header.ID, TargetEventRevision: prior.ContentRevision, DeletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ProjectionState: scoutChatModerationPending, Target: scoutChatMessageRecord{ID: message.ID, Kind: message.Kind, Role: message.Role, CreatedAt: message.CreatedAt},
		TargetWork:      &scoutChatWorkModerationBinding{MessageID: message.ID, ThreadID: "run-old", ArtifactID: "artifact-old", Status: "error", ArtifactDigest: strings.Repeat("a", 64)},
		ReplacementWork: &scoutChatWorkModerationBinding{MessageID: "replacement-work", ThreadID: "run-new", ArtifactID: "artifact-new", Status: "complete", ArtifactDigest: strings.Repeat("b", 64)},
	}
	if err := kanbanApp.retractSTRIDETeamChatModeration(channel, receipt); !errors.Is(err, ErrSTRIDEConversationConflict) {
		t.Fatalf("wrong-reference canonical delete err=%v, want conflict", err)
	}
}

func TestScoutChatAdminModerationRuntimeUnavailableLeavesSourceIntact(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "incident channel", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "agent-answer", Kind: "message", Role: "scout", Text: "incorrect answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed agent answer: %v", err)
	}
	kanbanApp.strideRuntime = nil
	admin := accountStore().findUser("aj@shareability.com")
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(admin, channel.ID, "agent-answer", "bad routing incident"); !errors.Is(err, ErrSTRIDERuntimeUnavailable) {
		t.Fatalf("runtime-unavailable moderation err=%v", err)
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(admin.Email, channel.ID)
	if err != nil || scoutChatMessageIndex(thread, "agent-answer") < 0 || len(thread.ModerationReceipts) != 0 {
		t.Fatalf("unavailable preflight mutated source/outbox: messageIndex=%d receipts=%+v err=%v", scoutChatMessageIndex(thread, "agent-answer"), thread.ModerationReceipts, err)
	}
}

func invalidateSTRIDERegistryWithAuthenticatedEnvelopeForTest(t *testing.T, config STRIDERuntimeConfig) {
	t.Helper()
	envelope, err := readSTRIDERuntimeSnapshot(config.SnapshotPath)
	if err != nil {
		t.Fatalf("read STRIDE snapshot: %v", err)
	}
	envelope.Payload.Registry.Digest = strings.Repeat("0", 64)
	envelope.Digest, err = STRIDEContractDigest(envelope.Payload)
	if err != nil {
		t.Fatalf("digest invalid-registry snapshot: %v", err)
	}
	envelope.Signature, err = strideSnapshotMAC(config.Authority, strideRuntimeSnapshotDomain, envelope.Payload.Generation, envelope.Digest)
	if err != nil {
		t.Fatalf("sign invalid-registry snapshot: %v", err)
	}
	rawEnvelope, err := canonicalJSON(envelope)
	if err != nil {
		t.Fatalf("write invalid-registry snapshot: %v", err)
	}
	if err := writeFileAtomicallyDurable(config.SnapshotPath, append(rawEnvelope, '\n'), 0o600); err != nil {
		t.Fatalf("write invalid-registry snapshot: %v", err)
	}
	generation, err := readSTRIDERuntimeGeneration(config.GenerationPath)
	if err != nil {
		t.Fatalf("read STRIDE generation: %v", err)
	}
	generation.Payload.SnapshotDigest = envelope.Digest
	generation.Digest, err = STRIDEContractDigest(generation.Payload)
	if err != nil {
		t.Fatalf("digest invalid-registry generation: %v", err)
	}
	generation.Signature, err = strideSnapshotMAC(config.Authority, strideRuntimeGenerationDomain, generation.Payload.Generation, generation.Digest)
	if err != nil {
		t.Fatalf("sign invalid-registry generation: %v", err)
	}
	rawGeneration, err := canonicalJSON(generation)
	if err != nil {
		t.Fatalf("write invalid-registry generation: %v", err)
	}
	if err := writeFileAtomicallyDurable(config.GenerationPath, append(rawGeneration, '\n'), 0o600); err != nil {
		t.Fatalf("write invalid-registry generation: %v", err)
	}
}

func TestScoutChatAdminSupersessionCompletesFromAuthenticatedConversationAbsence(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Bonfire Chat", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	runtimeConfig := strideIntegratedRuntimeConfig(t.TempDir())
	healthyRuntime, err := NewSTRIDERuntime(runtimeConfig)
	if err != nil {
		t.Fatalf("create initial STRIDE runtime: %v", err)
	}
	if err := healthyRuntime.Save(); err != nil {
		t.Fatalf("persist initial STRIDE runtime: %v", err)
	}
	if err := healthyRuntime.Close(); err != nil {
		t.Fatalf("close initial STRIDE runtime: %v", err)
	}
	invalidateSTRIDERegistryWithAuthenticatedEnvelopeForTest(t, runtimeConfig)
	degradedRuntime, err := NewSTRIDERuntime(runtimeConfig)
	if err == nil || degradedRuntime.Health().State != STRIDERuntimeUnavailable {
		t.Fatalf("invalid registry runtime state=%s err=%v, want unavailable", degradedRuntime.Health().State, err)
	}
	kanbanApp.strideRuntime = degradedRuntime
	admin := accountStore().findUser("aj@shareability.com")
	metadata := func(threadID, status string) map[string]string {
		return map[string]string{
			"threadId": threadID, "mode": "research", "status": status, "threadStatus": status,
			"originKind": agentThreadOriginChannel, "originId": channel.ID, "requestedBy": admin.Email,
		}
	}
	targetMetadata := metadata("disabled-target-run", "error")
	targetMetadata["currentStage"] = "gate_before_shipping"
	targetMetadata["reviewGate"] = "blocked"
	targetMetadata["progressPercent"] = "72"
	targetMetadata["error"] = "max_output_truncation"
	replacementMetadata := metadata("disabled-replacement-run", "complete")
	replacementMetadata["currentStage"] = "verify_goal_completed"
	replacementMetadata["reviewGate"] = "passed"
	replacementMetadata["progressPercent"] = "100"
	if _, _, err := kanbanApp.memory.appendOSArtifact("disabled-target-artifact", "failed result", targetMetadata); err != nil {
		t.Fatalf("append target artifact: %v", err)
	}
	if _, _, err := kanbanApp.memory.appendOSArtifact("disabled-replacement-artifact", "verified report", replacementMetadata); err != nil {
		t.Fatalf("append replacement artifact: %v", err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(admin.Email, channel.ID,
		scoutChatMessageRecord{ID: "disabled-target-work", Kind: "thread", Role: "scout", CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), Thread: &scoutChatThreadRef{ID: "disabled-target-run", Mode: "research", Status: "error", ArtifactID: "disabled-target-artifact"}},
		scoutChatMessageRecord{ID: "disabled-replacement-work", Kind: "thread", Role: "scout", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Thread: &scoutChatThreadRef{ID: "disabled-replacement-run", Mode: "research", Status: "complete", ArtifactID: "disabled-replacement-artifact"}},
	); err != nil {
		t.Fatalf("seed work cards: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+channel.ID+"/messages/disabled-target-work/supersede", strings.NewReader(`{"replacementMessageId":"disabled-replacement-work","reason":"superseded by verified replacement"}`))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, admin.Email, "B0NFIRE!") {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated-absence supersession route status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		OK      bool                           `json:"ok"`
		Receipt scoutChatModerationReceiptView `json:"receipt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || !response.OK || response.Receipt.ProjectionState != scoutChatModerationComplete {
		t.Fatalf("authenticated-absence supersession response=%+v err=%v", response, err)
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(admin.Email, channel.ID)
	if err != nil {
		t.Fatalf("read superseded thread: %v", err)
	}
	if scoutChatMessageIndex(thread, "disabled-target-work") >= 0 || scoutChatMessageIndex(thread, "disabled-replacement-work") < 0 || len(thread.ModerationReceipts) != 1 || thread.ModerationReceipts[0].ProjectionState != scoutChatModerationComplete {
		t.Fatalf("authenticated-absence supersession thread=%+v", thread)
	}
	if _, ok := kanbanApp.osArtifactByID("disabled-target-artifact"); !ok {
		t.Fatal("target artifact was removed")
	}
	if _, ok := kanbanApp.osArtifactByID("disabled-replacement-artifact"); !ok {
		t.Fatal("replacement artifact was removed")
	}
	if event, found, err := kanbanApp.latestSTRIDETeamChatEvent(channel.ID, "disabled-target-work"); err != nil || found || event.SourceID != "" {
		t.Fatalf("disabled projection falsely produced canonical event=%+v found=%v err=%v", event, found, err)
	}
}

func TestScoutChatAuthenticatedConversationProofDoesNotHideProjectedSource(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Bonfire Chat", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create enabled runtime: %v", err)
	}
	kanbanApp.strideRuntime = runtime
	message := scoutChatMessageRecord{ID: "projected-source", Kind: "message", Role: "user", Text: "projected", AuthorEmail: "aj@shareability.com", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := runtime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		appendRecord := strideConversationAppend("event_projected_source", message.ID, "message", 1, STRIDEAudience{Visibility: "channel", Principals: []string{"member_aj"}})
		appendRecord.Event.Header.TenantID = canonicalTenantID()
		appendRecord.Event.ThreadID = channel.ID
		_, appendErr := domains.ConversationLedger.Append(appendRecord)
		return appendErr
	}); err != nil {
		t.Fatalf("append projected source: %v", err)
	}
	if err := runtime.Save(); err != nil {
		t.Fatalf("persist projected source: %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close enabled runtime: %v", err)
	}
	invalidateSTRIDERegistryWithAuthenticatedEnvelopeForTest(t, config)
	degradedRuntime, err := NewSTRIDERuntime(config)
	if err == nil || degradedRuntime.Health().State != STRIDERuntimeUnavailable {
		t.Fatalf("invalid registry runtime state=%s err=%v, want unavailable", degradedRuntime.Health().State, err)
	}
	kanbanApp.strideRuntime = degradedRuntime
	latest, found, err := kanbanApp.latestSTRIDETeamChatEvent(channel.ID, message.ID)
	if err != nil || !found || latest.SourceID != message.ID || latest.EventType == "delete" {
		t.Fatalf("authenticated projected source latest=%+v found=%v err=%v", latest, found, err)
	}
}

func TestScoutChatLegacyProjectionAbsenceProofRejectsRestoredAndUnavailableRuntime(t *testing.T) {
	dir := t.TempDir()
	config := strideIntegratedRuntimeConfig(dir)
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create enabled runtime: %v", err)
	}
	if err := runtime.Save(); err != nil {
		t.Fatalf("persist enabled runtime: %v", err)
	}
	if runtime.legacyTeamChatProjectionProvablyAbsent() {
		t.Fatal("enabled runtime falsely proved projection absence")
	}
	runtime.config.Enabled = false
	if runtime.legacyTeamChatProjectionProvablyAbsent() {
		t.Fatal("mutated enabled bit falsely proved projection absence")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close enabled runtime: %v", err)
	}
	restoredRuntime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("restore healthy runtime: %v", err)
	}
	if err := restoredRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		appendRecord := strideConversationAppend("event_after_restore", "source_after_restore", "message", 1, STRIDEAudience{Visibility: "channel", Principals: []string{"member_aj"}})
		appendRecord.Event.Header.TenantID = canonicalTenantID()
		appendRecord.Event.ThreadID = "thread_after_restore"
		_, appendErr := domains.ConversationLedger.Append(appendRecord)
		return appendErr
	}); err != nil {
		t.Fatalf("append after healthy restore: %v", err)
	}
	restoredRuntime.mu.Lock()
	restoredRuntime.failClosedLocked(errors.New("forced post-restore failure"))
	restoredRuntime.mu.Unlock()
	app := &kanbanBoardApp{strideRuntime: restoredRuntime}
	if _, _, err := app.latestSTRIDETeamChatEvent("thread_after_restore", "source_after_restore"); !errors.Is(err, ErrSTRIDERuntimeUnavailable) {
		t.Fatalf("post-restore unavailable runtime err=%v, want unavailable rather than stale absence", err)
	}

	disabledConfig := config
	disabledConfig.Enabled = false
	restoredFilesPresent, err := NewSTRIDERuntime(disabledConfig)
	if err != nil {
		t.Fatalf("create disabled runtime over restored files: %v", err)
	}
	if restoredFilesPresent.legacyTeamChatProjectionProvablyAbsent() {
		t.Fatal("disabled runtime over durable history falsely proved projection absence")
	}
	app = &kanbanBoardApp{strideRuntime: restoredFilesPresent}
	if _, _, err := app.latestSTRIDETeamChatEvent("thread", "message"); !errors.Is(err, ErrSTRIDERuntimeUnavailable) {
		t.Fatalf("disabled runtime with history err=%v, want unavailable", err)
	}

	unavailable := &STRIDERuntime{config: disabledConfig, state: STRIDERuntimeUnavailable}
	if unavailable.legacyTeamChatProjectionProvablyAbsent() {
		t.Fatal("unavailable runtime falsely proved projection absence")
	}
	app.strideRuntime = unavailable
	if _, _, err := app.latestSTRIDETeamChatEvent("thread", "message"); !errors.Is(err, ErrSTRIDERuntimeUnavailable) {
		t.Fatalf("unavailable runtime err=%v, want unavailable", err)
	}
}

func TestScoutChatAdminModerationHumanRetryOutboxSurvivesSaveFailureResponseLossAndRestart(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	_ = loginAs(t, "tim@shareability.com", "B0NFIRE!")
	dataDir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dataDir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dataDir, "board.json"))
	app := newKanbanBoardApp()

	channel, err := app.createScoutChatThread("tim@shareability.com", "Tim", "incident channel", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	runtimeConfig := strideIntegratedRuntimeConfig(t.TempDir())
	runtimeConfig.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(runtimeConfig)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	app.strideRuntime = runtime
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "aj-retry", Kind: "message", Role: "user", Text: "compare the two pitches", AuthorEmail: "aj@shareability.com", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed projected AJ retry: %v", err)
	}

	validSnapshotPath := runtime.config.SnapshotPath
	runtime.config.SnapshotPath = t.TempDir() // snapshot write fails after the canonical delete append
	admin := accountStore().findUser("aj@shareability.com")
	thread, receipt, err := app.moderateScoutChatThreadMessage(admin, channel.ID, "aj-retry", "bad routing incident")
	if err != nil {
		t.Fatalf("moderation with projection save failure should persist a pending outbox: %v", err)
	}
	if receipt.ProjectionState != scoutChatModerationPending || receipt.AttemptCount != 1 || scoutChatMessageIndex(thread, "aj-retry") >= 0 {
		t.Fatalf("pending moderation=%+v messages=%+v", receipt, thread.Messages)
	}

	// A lost HTTP response followed by the same exact retry resolves the durable
	// operation instead of returning message-not-found or creating a new one.
	thread, retried, err := app.moderateScoutChatThreadMessage(admin, channel.ID, "aj-retry", "bad routing incident")
	if err != nil || retried.OperationID != receipt.OperationID || retried.ProjectionState != scoutChatModerationPending || retried.AttemptCount != 2 {
		t.Fatalf("same-process idempotent retry receipt=%+v err=%v", retried, err)
	}
	if len(thread.ModerationReceipts) != 1 {
		t.Fatalf("response-loss retry created duplicate receipts: %+v", thread.ModerationReceipts)
	}

	// Restore the last valid runtime snapshot in a fresh process. The thread's
	// pending receipt is the source of truth and finishes the one canonical
	// delete, then marks itself complete durably.
	runtimeConfig.SnapshotPath = validSnapshotPath
	runtimeConfig.BootstrapEmpty = false
	restoredRuntime, err := NewSTRIDERuntime(runtimeConfig)
	if err != nil {
		t.Fatalf("restore prior runtime snapshot: %v", err)
	}
	t.Cleanup(func() { _ = restoredRuntime.Close() })
	restarted := newKanbanBoardApp()
	restarted.strideRuntime = restoredRuntime
	restarted.recoverScoutChatModerations()
	persisted, _, err := restarted.scoutChatThreadByID(admin.Email, channel.ID)
	if err != nil || len(persisted.ModerationReceipts) != 1 || persisted.ModerationReceipts[0].ProjectionState != scoutChatModerationComplete || persisted.ModerationReceipts[0].CompletedAt == "" {
		t.Fatalf("restart moderation receipt=%+v err=%v", persisted.ModerationReceipts, err)
	}
	if scoutChatMessageIndex(persisted, "aj-retry") >= 0 {
		t.Fatal("restart reconciliation resurrected the deleted chat source")
	}
	if err := restoredRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), strideRuntimePrincipalForEmail("tim@shareability.com"))
		if projectErr != nil {
			return projectErr
		}
		for _, row := range projection {
			if row.SourceID == "aj-retry" {
				t.Fatalf("restart reconciliation left AJ retry recallable: %+v", row)
			}
		}
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		deleteCount := 0
		for _, record := range snapshot.Events {
			if record.Append.Event.SourceID == "aj-retry" && record.Append.Event.EventType == "delete" {
				deleteCount++
			}
		}
		if deleteCount != 1 {
			t.Fatalf("canonical delete count=%d, want exactly one", deleteCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify restarted canonical projection: %v", err)
	}

	completedAttempts := persisted.ModerationReceipts[0].AttemptCount
	_, replayed, err := restarted.moderateScoutChatThreadMessage(admin, channel.ID, "aj-retry", "bad routing incident")
	if err != nil || replayed.ProjectionState != scoutChatModerationComplete || replayed.AttemptCount != completedAttempts {
		t.Fatalf("completed response-loss retry receipt=%+v err=%v", replayed, err)
	}
}

func TestScoutChatThreadMessageDeleteCascadesOnlyToOrdinaryCausedAnswer(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "strategy", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	kanbanApp.strideRuntime = runtime
	projectedSourceIDs := func() map[string]bool {
		t.Helper()
		result := map[string]bool{}
		if err := runtime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
			rows, err := domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), strideRuntimePrincipalForEmail("aj@shareability.com"))
			if err != nil {
				return err
			}
			for _, row := range rows {
				result[row.SourceID] = true
			}
			return nil
		}); err != nil {
			t.Fatalf("project conversation ledger: %v", err)
		}
		return result
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", channel.ID,
		scoutChatMessageRecord{
			ID: "source", Kind: "message", Role: "user", Text: "Compare the two pitches.", CreatedAt: createdAt,
			AuthorEmail: "aj@shareability.com",
		},
		scoutChatMessageRecord{
			ID: "ordinary-answer", Kind: "message", Role: "scout", Text: "The first pitch is stronger.", CreatedAt: createdAt,
			CausedByMessageID: "source",
		},
		scoutChatMessageRecord{
			ID: "durable-work", Kind: "message", Role: "scout", Text: "Research is running.", CreatedAt: createdAt,
			CausedByMessageID: "source", Thread: &scoutChatThreadRef{ID: "work-1"},
		},
		scoutChatMessageRecord{
			ID: "unrelated-answer", Kind: "message", Role: "scout", Text: "Unrelated answer.", CreatedAt: createdAt,
		},
	); err != nil {
		t.Fatalf("seed causal messages: %v", err)
	}
	beforeDelete := projectedSourceIDs()
	if !beforeDelete["source"] || !beforeDelete["ordinary-answer"] {
		t.Fatalf("causal pair did not enter canonical projection: %+v", beforeDelete)
	}

	thread, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "source")
	if err != nil {
		t.Fatalf("delete source: %v", err)
	}
	remaining := make(map[string]bool, len(thread.Messages))
	for _, message := range thread.Messages {
		remaining[message.ID] = true
	}
	if remaining["source"] || remaining["ordinary-answer"] {
		t.Fatalf("source or ordinary caused answer survived: %+v", remaining)
	}
	if !remaining["durable-work"] || !remaining["unrelated-answer"] || len(remaining) != 2 {
		t.Fatalf("durable or unrelated message was removed: %+v", remaining)
	}
	afterDelete := projectedSourceIDs()
	if afterDelete["source"] || afterDelete["ordinary-answer"] || !afterDelete["durable-work"] || !afterDelete["unrelated-answer"] {
		t.Fatalf("canonical projection did not retract exactly the causal pair: %+v", afterDelete)
	}

	persisted, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if len(persisted.Messages) != 2 || scoutChatMessageIndex(persisted, "ordinary-answer") >= 0 {
		t.Fatalf("causal cascade was not durable: %+v", persisted.Messages)
	}
}

func TestScoutChatThreadMessageDeletePrivateThreadStaysOwnerOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Private notes", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	// Pre-stamp message (no authorEmail): owner-only visibility already
	// proves authorship in a private thread, so the owner can still delete it.
	seedScoutChatUserMessage(t, private.ID, "aj@shareability.com", "msg-legacy", "", "posted before the stamp existed")

	if _, err := kanbanApp.deleteScoutChatThreadMessage("tim@shareability.com", private.ID, "msg-legacy"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("outsider delete err=%v, want the thread hidden entirely", err)
	}
	thread, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", private.ID, "msg-legacy")
	if err != nil {
		t.Fatalf("owner delete of pre-stamp message: %v", err)
	}
	if len(thread.Messages) != 0 {
		t.Fatalf("messages=%d, want 0", len(thread.Messages))
	}
}

// A pre-stamp (authorEmail-less) user message in a PUBLIC channel has no
// provable author — nobody may delete it.
func TestScoutChatThreadMessageDeleteUnstampedChannelMessageRefused(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-unstamped", "", "who wrote this?")
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-unstamped"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("unstamped channel delete err=%v, want refusal", err)
	}
}

// The wire contract the delete control's fetch relies on: DELETE
// /assistant/chat-threads/{id}/messages/{messageId}, session-identified,
// 403 on someone else's message, 200 + the updated thread on your own.
func TestAssistantChatThreadMessageDeleteRoute(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-aj", "aj@shareability.com", "wrong channel")

	deleteAs := func(email string, messageID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/assistant/chat-threads/"+channel.ID+"/messages/"+messageID, nil)
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, req)
		return recorder
	}

	if recorder := deleteAs("tim@shareability.com", "msg-aj"); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user delete status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusForbidden)
	}
	recorder := deleteAs("aj@shareability.com", "msg-aj")
	if recorder.Code != http.StatusOK {
		t.Fatalf("own delete status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		OK     bool                  `json:"ok"`
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if !payload.OK || len(payload.Thread.Messages) != 0 {
		t.Fatalf("response=%s, want ok with the message gone", recorder.Body.String())
	}
	if recorder := deleteAs("aj@shareability.com", "msg-aj"); recorder.Code != http.StatusNotFound {
		t.Fatalf("re-delete status=%d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestRoomChatDeleteEnforcesAuthorshipFromSession(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	payload, ok := app.recordRoomChatMessageWithMetadata(officeRoomID, "Tom", "oops, wrong room", map[string]string{
		"authorEmail": "tom@shareability.com",
	})
	if !ok {
		t.Fatal("seed room chat message failed")
	}
	id, _ := payload["id"].(string)
	if authorEmail, _ := payload["authorEmail"].(string); authorEmail != "tom@shareability.com" {
		t.Fatalf("payload authorEmail=%q, want the session stamp on the wire", authorEmail)
	}

	// Someone else — even sharing the display name — cannot delete it.
	if _, ok := app.deleteRoomChatMessage(id, "tim@shareability.com", "Tom"); ok {
		t.Fatal("cross-user room chat delete succeeded, want authz refusal")
	}
	if history := app.roomChatHistory(roomChatHistoryLimit); len(history) != 1 {
		t.Fatalf("history=%d after refused delete, want the message intact", len(history))
	}

	// The author deletes it: gone from the persisted record and from history.
	deleted, ok := app.deleteRoomChatMessage(id, "TOM@shareability.com", "Tom")
	if !ok {
		t.Fatal("author room chat delete failed")
	}
	if deleted["id"] != id {
		t.Fatalf("delete payload id=%v, want %q", deleted["id"], id)
	}
	if history := app.roomChatHistory(roomChatHistoryLimit); len(history) != 0 {
		t.Fatalf("history=%d after delete, want 0", len(history))
	}
	if entries := app.memory.snapshot(0); len(entries) != 0 {
		t.Fatalf("memory entries=%d after delete, want the transcript entry hard-removed", len(entries))
	}
}

// A waiting archive writer makes sync.RWMutex writer-preferring. The delete
// path already owns a lifecycle read lease, so recursively taking a second
// read lease deadlocks the reader and queued writer forever. TryRLock gives
// this regression a deterministic proof that the writer is queued before the
// delete continues; no scheduler sleep is involved.
func TestRoomChatDeleteDoesNotRecursivelyReadLockBehindQueuedLifecycleWriter(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	payload, ok := app.recordRoomChatMessageWithMetadata(officeRoomID, "Tom", "remove after the archive writer queues", map[string]string{
		"authorEmail": "tom@shareability.com",
	})
	if !ok {
		t.Fatal("seed room chat message")
	}
	id, _ := payload["id"].(string)

	deleteEntered := make(chan struct{})
	releaseDelete := make(chan struct{})
	app.roomChatDeleteAfterLifecycleLease = func() {
		close(deleteEntered)
		<-releaseDelete
	}
	deleteDone := make(chan bool, 1)
	go func() {
		_, deleted := app.deleteRoomChatMessage(id, "tom@shareability.com", "Tom")
		deleteDone <- deleted
	}()
	select {
	case <-deleteEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("delete did not acquire its lifecycle read lease")
	}

	writerStarted := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		close(writerStarted)
		app.meetingLifecycleMu.Lock()
		app.meetingLifecycleMu.Unlock()
		close(writerDone)
	}()
	<-writerStarted
	deadline := time.Now().Add(2 * time.Second)
	for app.meetingLifecycleMu.TryRLock() {
		app.meetingLifecycleMu.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("lifecycle writer never queued behind the held delete lease")
		}
		runtime.Gosched()
	}

	close(releaseDelete)
	select {
	case deleted := <-deleteDone:
		if !deleted {
			t.Fatal("authorized delete failed after queued lifecycle writer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("room-chat delete deadlocked on a recursive lifecycle read lock")
	}
	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("queued lifecycle writer remained stranded after delete")
	}
	if _, found := app.memory.entryByID(id); found {
		t.Fatal("authorized delete returned before durable removal")
	}
}

func TestRoomChatDeleteLegacyEntriesFallBackToSpeakerName(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	payload, ok := app.recordRoomChatMessage(officeRoomID, "Tyler", "pre-stamp message")
	if !ok {
		t.Fatal("seed legacy room chat message failed")
	}
	id, _ := payload["id"].(string)

	if _, ok := app.deleteRoomChatMessage(id, "tim@shareability.com", "Tim"); ok {
		t.Fatal("name-mismatched delete of a legacy entry succeeded, want refusal")
	}
	if _, ok := app.deleteRoomChatMessage(id, "tyler@shareability.com", "tyler"); !ok {
		t.Fatal("case-insensitive speaker-name fallback delete failed")
	}
}

func TestRoomChatDeleteOnlyTouchesRoomChatEntries(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	entry, appended, err := app.memory.appendAttributedTranscript("event-spoken", "item-spoken", "Tyler", "dominant", "Boot Barn spoken update.")
	if err != nil || !appended {
		t.Fatalf("append spoken transcript: appended=%v err=%v", appended, err)
	}
	if _, ok := app.deleteRoomChatMessage(entry.ID, "tyler@shareability.com", "Tyler"); ok {
		t.Fatal("spoken transcript deleted through the room chat path, want refusal")
	}
	if entries := app.memory.snapshot(0); len(entries) != 1 {
		t.Fatalf("memory entries=%d, want the spoken transcript untouched", len(entries))
	}
}
