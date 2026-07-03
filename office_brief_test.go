package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// approvalArtifact seeds an external-write artifact parked at the admin gate,
// stamped with the requester so the round-trip can find who to notify.
func approvalArtifact(t *testing.T, app *kanbanBoardApp, mode string, title string, requesterEmail string) meetingMemoryEntry {
	t.Helper()
	artifact, _, err := app.createOSArtifactWithMetadata(mode, title, "body of "+title, requesterEmail, map[string]string{
		"mode":         mode,
		"title":        title,
		"reviewGate":   "approval_required",
		"threadStatus": codexJobStatusApprovalRequired,
		"requestedBy":  requesterEmail,
	})
	if err != nil {
		t.Fatalf("seed approval artifact: %v", err)
	}
	return artifact
}

func TestMorningBriefComposesEveryInput(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// A finished artifact → the "completed" strip.
	if _, _, err := app.createOSArtifactWithMetadata("research", "Nimbus comp set", "Vision: done.", "Joel", map[string]string{
		"title":  "Nimbus comp set",
		"status": "complete",
	}); err != nil {
		t.Fatalf("seed completed artifact: %v", err)
	}
	// An approval-required item requested by Joel (a non-admin).
	approvalArtifact(t, app, "workflow", "Deal Room share for Nimbus", "joel@shareability.com")
	// A board delta digest.
	if _, _, err := app.memory.appendBoardUpdate("bu-1", "Moved Finish packetizer to In Progress; drafted two follow-ups.", nil); err != nil {
		t.Fatalf("seed board update: %v", err)
	}
	// A chat notification targeted at Joel → unread channel activity.
	if _, err := app.createNotification("joel@shareability.com", notificationKindChat, "#dealflow: new message from Tyler", "chat", "", "thread-1", false); err != nil {
		t.Fatalf("seed chat notification: %v", err)
	}
	// A quarantined entry → the tray.
	if _, ok, err := app.memory.appendTranscript("t-slop", "", "Redundant chatter with no linkage."); err != nil || !ok {
		t.Fatalf("seed transcript: ok=%v err=%v", ok, err)
	}
	expires := time.Now().UTC().Add(20 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "t-slop", "Redundant chatter with no linkage.", map[string]string{
		relevanceMetadataKey: relevanceQuarantined,
		"classifierReason":   "orphaned chatter, never attached, 12 days old",
		"quarantinedAt":      time.Now().UTC().Format(time.RFC3339Nano),
		"expiresAt":          expires,
	}); err != nil {
		t.Fatalf("quarantine transcript: %v", err)
	}

	admin := &userAccount{Email: artifactLibraryAdminEmail, Name: "AJ"}
	joel := &userAccount{Email: "joel@shareability.com", Name: "Joel"}
	tyler := &userAccount{Email: "tyler@shareability.com", Name: "Tyler"}

	// Admin viewer: every section is represented and the tray can delete.
	brief := app.morningBriefPayload(admin)
	if brief["greeting"] != "AJ" {
		t.Fatalf("greeting=%v, want AJ", brief["greeting"])
	}
	approvals := brief["approvals"].(map[string]any)
	if approvals["count"].(int) != 1 {
		t.Fatalf("admin approvals count=%v, want 1", approvals["count"])
	}
	firstApproval := approvals["items"].([]map[string]any)[0]
	if firstApproval["canAct"] != true {
		t.Fatal("admin must be able to act on the pending approval")
	}
	if brief["completed"].(map[string]any)["count"].(int) != 1 {
		t.Fatalf("completed count=%v, want 1", brief["completed"].(map[string]any)["count"])
	}
	board := brief["board"].(map[string]any)
	if len(board["items"].([]map[string]any)) != 1 {
		t.Fatalf("board deltas=%v, want 1", board["items"])
	}
	quarantine := brief["quarantine"].(map[string]any)
	if quarantine["count"].(int) != 1 || quarantine["canDelete"] != true {
		t.Fatalf("admin quarantine=%v, want 1 entry + canDelete", quarantine)
	}

	// Joel (the requester, non-admin): sees his own waiting item, no delete.
	joelBrief := app.morningBriefPayload(joel)
	joelApprovals := joelBrief["approvals"].(map[string]any)
	if joelApprovals["count"].(int) != 1 {
		t.Fatalf("Joel approvals count=%v, want his own 1", joelApprovals["count"])
	}
	joelItem := joelApprovals["items"].([]map[string]any)[0]
	if joelItem["canAct"] != false || joelItem["state"] != "waiting" {
		t.Fatalf("Joel's waiting item must be read-only waiting: %v", joelItem)
	}
	if joelItem["requestedMine"] != true {
		t.Fatal("Joel's own request must be marked mine")
	}
	if joelBrief["unreadChannels"].(map[string]any)["count"].(int) != 1 {
		t.Fatalf("Joel unread channels=%v, want 1", joelBrief["unreadChannels"])
	}
	if joelBrief["quarantine"].(map[string]any)["canDelete"] != false {
		t.Fatal("non-admin must not see the delete affordance")
	}

	// Tyler (uninvolved non-admin): does not see Joel's pending approval.
	tylerBrief := app.morningBriefPayload(tyler)
	if tylerBrief["approvals"].(map[string]any)["count"].(int) != 0 {
		t.Fatalf("Tyler must not see Joel's approval: %v", tylerBrief["approvals"])
	}
	if tylerBrief["unreadChannels"].(map[string]any)["count"].(int) != 0 {
		t.Fatal("Tyler must not see Joel's targeted chat notification")
	}
}

func TestPortfolioHealthStaleFirstWithDials(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	createTestPackage(t, app, "Aurora", "fresh and moving")
	stale := createTestPackage(t, app, "Nimbus", "quiet for a while")

	// Give Nimbus a grill readiness with a trend, then backdate its movement.
	grill, _, err := app.createOSArtifactWithMetadata("grill", "Nimbus grill", "Verdict: promising.", "AJ", map[string]string{
		"mode":           "grill",
		"readinessScore": "6.8",
		"readinessDelta": "+0.6",
	})
	if err != nil {
		t.Fatalf("create grill: %v", err)
	}
	if _, err := app.attachToPackage(stale.ID, "artifact", grill.ID, "AJ"); err != nil {
		t.Fatalf("attach grill: %v", err)
	}
	// Backdate Nimbus's updatedAt to 15 days ago (stale ≥ 10); keep Aurora fresh.
	staleRec, _ := app.venturePackageByID(stale.ID)
	staleRec.UpdatedAt = time.Now().UTC().Add(-15 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := app.persistVenturePackage(staleRec, false); err != nil {
		t.Fatalf("backdate stale package: %v", err)
	}

	packages := app.portfolioHealthPayload(time.Now())
	if len(packages) != 2 {
		t.Fatalf("portfolio has %d packages, want 2", len(packages))
	}
	// Stale-first ordering: Nimbus (15 days) ahead of fresh Aurora.
	if packages[0]["name"] != "Nimbus" {
		t.Fatalf("stale Nimbus must sort first, got %v", packages[0]["name"])
	}
	nimbus := packages[0]
	if nimbus["stale"] != true {
		t.Fatal("Nimbus must be flagged stale")
	}
	if nimbus["readinessScore"] != "6.8" || nimbus["readinessDelta"] != "+0.6" {
		t.Fatalf("Nimbus dial=%v/%v, want 6.8/+0.6", nimbus["readinessScore"], nimbus["readinessDelta"])
	}
	if days, _ := nimbus["freshnessDays"].(int); days < 14 {
		t.Fatalf("Nimbus freshnessDays=%v, want ~15", nimbus["freshnessDays"])
	}
	if nudge := asString(nimbus["nudge"]); !strings.Contains(nudge, "hasn't moved") {
		t.Fatalf("stale Nimbus must carry a nudge line, got %q", nudge)
	}

	// The spoken tool leads with the count and mentions the readiness trend.
	spoken := app.portfolioHealthSpoken(time.Now())
	if !strings.Contains(spoken, "2 packages") || !strings.Contains(spoken, "6.8") {
		t.Fatalf("spoken summary missing count or dial: %q", spoken)
	}
}

func TestPortfolioHealthToolIsPrivateAllowlistedAndKeylessSafe(t *testing.T) {
	if !privateRealtimeVoiceToolAllowed("portfolio_health") {
		t.Fatal("portfolio_health must be private-voice allowlisted")
	}
	app := newIsolatedKanbanBoardApp(t)
	result, changed, err := app.portfolioHealthTool()
	if err != nil || changed {
		t.Fatalf("portfolio_health must never error or mutate: changed=%v err=%v", changed, err)
	}
	if result["ok"] != true || asString(result["spoken"]) == "" {
		t.Fatalf("portfolio_health must return a spoken summary even with no packages: %v", result)
	}
}

// TestApprovalRoundTripFiresEventAndNotifiesRequester proves the loop's two
// halves: the push-channel proposal event reaches every session, and the
// requester gets a direct outcome notification — approve and reject.
func TestApprovalRoundTripFiresEventAndNotifiesRequester(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	observer := dialIsolatedWebsocket(t, server, "tim@shareability.com")
	sendOfficeHello(t, observer)
	drainOfficeReplay(t, observer)

	// Joel (non-admin) requested an external write; AJ (admin) approves it.
	artifact := approvalArtifact(t, kanbanApp, "workflow", "Send investor update", "joel@shareability.com")
	kanbanApp.recordApprovalOutcome(artifact, "approve", "", "AJ")

	event := waitForOSEvent(t, observer, osEventProposal, 2*time.Second)
	if event.Ref != artifact.ID {
		t.Fatalf("proposal event ref=%q, want %q", event.Ref, artifact.ID)
	}
	joelNotes := kanbanApp.notificationsForUser("joel@shareability.com", 20)
	if !containsNotificationText(joelNotes, "Approved") {
		t.Fatalf("requester was not notified of approval: %v", joelNotes)
	}

	// Reject carries the admin's one-line reason back to the requester.
	rejected := approvalArtifact(t, kanbanApp, "workflow", "Publish deck externally", "joel@shareability.com")
	kanbanApp.recordApprovalOutcome(rejected, "reject", "needs two more receipts", "AJ")
	joelNotes = kanbanApp.notificationsForUser("joel@shareability.com", 20)
	if !containsNotificationText(joelNotes, "needs two more receipts") {
		t.Fatalf("rejection reason did not reach the requester: %v", joelNotes)
	}

	// An admin approving their OWN request needs no self-OUTCOME notification.
	// (The gate-entry ping on creation is expected; it is folded into the
	// baseline below so this asserts recordApprovalOutcome adds nothing.)
	ownRequest := approvalArtifact(t, kanbanApp, "workflow", "AJ's own external write", artifactLibraryAdminEmail)
	before := len(kanbanApp.notificationsForUser(artifactLibraryAdminEmail, 50))
	kanbanApp.recordApprovalOutcome(ownRequest, "approve", "", "AJ")
	if after := len(kanbanApp.notificationsForUser(artifactLibraryAdminEmail, 50)); after != before {
		t.Fatalf("self-approval outcome must not notify the admin: before=%d after=%d", before, after)
	}
}

// TestArtifactActionApprovePreservesGoalResumeAndNotifies is the Wave-2
// regression guard: approving a mode=goal artifact through the action endpoint
// still routes through resumeApprovedGoal (the gate transitions to passed) AND
// the requester is notified — the round-trip rides on top without displacing
// the goal-engine wiring.
func TestArtifactActionApprovePreservesGoalResumeAndNotifies(t *testing.T) {
	setupAuthTestEnv(t)
	previous := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previous })

	plan := goalPlan{
		PlanVersion:  goalPlanVersion,
		GoalID:       "agent-thread-goal-approve",
		Objective:    "Ship the Nimbus binder",
		CreatedBy:    "joel@shareability.com",
		Authority:    codexJobAuthorityExternalWrite,
		State:        goalStateApproval,
		Gate:         goalGate{Status: "approval_required", ApprovalRequired: true, Command: "git push origin main"},
		Verification: goalVerification{Verdict: "pending"},
		Subtasks:     []goalSubtask{{ID: "st-1", Title: "Assemble", Mode: "workflow", Status: subtaskComplete}},
	}
	raw, _ := json.Marshal(plan)
	parent, _, err := kanbanApp.createOSArtifactWithMetadata("workflow", plan.Objective, buildGoalScaffold(plan), "joel@shareability.com", map[string]string{
		"mode":         "goal",
		"title":        "Ship the Nimbus binder",
		"goalPlan":     string(raw),
		"currentStage": goalStateApproval,
		"reviewGate":   "approval_required",
		"requestedBy":  "joel@shareability.com",
	})
	if err != nil {
		t.Fatalf("seed parked goal: %v", err)
	}

	// Non-admin cannot approve — the gate stays admin-only.
	forbidden := doArtifactAction(t, "joel@shareability.com", parent.ID, "approve", "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin approve status=%d, want 403", forbidden.Code)
	}

	// Admin approves: resumeApprovedGoal fires (gate → passed) and Joel is told.
	ok := doArtifactAction(t, artifactLibraryAdminEmail, parent.ID, "approve", "")
	if ok.Code != http.StatusAccepted {
		t.Fatalf("admin approve status=%d body=%s, want 202", ok.Code, ok.Body.String())
	}
	updated := mustArtifact(t, kanbanApp, parent.ID)
	resumed, decoded := decodeGoalPlan(updated.Metadata["goalPlan"])
	if !decoded {
		t.Fatal("goal plan missing after approve")
	}
	if resumed.Gate.Status != "passed" {
		t.Fatalf("gate status=%q, want passed — resumeApprovedGoal did not fire", resumed.Gate.Status)
	}
	joelNotes := kanbanApp.notificationsForUser("joel@shareability.com", 20)
	if !containsNotificationText(joelNotes, "Approved") {
		t.Fatalf("goal requester was not notified: %v", joelNotes)
	}
}

// TestArtifactActionRejectIsIdempotent guards the reject path the way the two
// approve paths are guarded: a resubmitted/double-clicked reject on an
// already-rejected artifact is a no-op (4xx) and must NOT re-notify the
// requester or re-fire the push event.
func TestArtifactActionRejectIsIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	previous := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previous })

	artifact := approvalArtifact(t, kanbanApp, "workflow", "Publish externally", "joel@shareability.com")

	first := doArtifactAction(t, artifactLibraryAdminEmail, artifact.ID, "reject", "missing receipts")
	if first.Code != http.StatusOK {
		t.Fatalf("first reject status=%d body=%s, want 200", first.Code, first.Body.String())
	}
	afterOne := len(kanbanApp.notificationsForUser("joel@shareability.com", 50))

	// Second reject on the now-settled artifact: guarded, no re-notify.
	second := doArtifactAction(t, artifactLibraryAdminEmail, artifact.ID, "reject", "changed my mind")
	if second.Code < 400 {
		t.Fatalf("second reject status=%d, want a 4xx no-op", second.Code)
	}
	afterTwo := len(kanbanApp.notificationsForUser("joel@shareability.com", 50))
	if afterTwo != afterOne {
		t.Fatalf("a second reject re-notified the requester: %d → %d", afterOne, afterTwo)
	}
}

func TestArtifactActionRejectRequiresAdmin(t *testing.T) {
	setupAuthTestEnv(t)
	previous := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previous })

	artifact := approvalArtifact(t, kanbanApp, "workflow", "External publish", "joel@shareability.com")
	forbidden := doArtifactAction(t, "tyler@shareability.com", artifact.ID, "reject", "not ready")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin reject status=%d, want 403", forbidden.Code)
	}
}

// TestApprovalGateNotifiesAdminOnce proves the admin's half of the round-trip:
// an artifact entering the external-write gate pings the admin exactly once,
// carries the approval markers (tool=approval + artifactId), and re-arms after
// the artifact leaves the gate.
func TestApprovalGateNotifiesAdminOnce(t *testing.T) {
	previous := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previous })
	app := kanbanApp

	// Creating a parked artifact fires emitOSArtifactEvent → the admin ping.
	artifact := approvalArtifact(t, app, "workflow", "Send the investor update", "joel@shareability.com")

	adminNotes := app.notificationsForUser(artifactLibraryAdminEmail, 20)
	var gateNote map[string]any
	for _, note := range adminNotes {
		if asString(note["tool"]) == "approval" && asString(note["artifactId"]) == artifact.ID {
			gateNote = note
		}
	}
	if gateNote == nil {
		t.Fatalf("admin was not pinged when the artifact entered the gate: %v", adminNotes)
	}
	if !strings.Contains(asString(gateNote["text"]), "Approval needed") {
		t.Fatalf("gate notification text=%q, want an 'Approval needed' line", gateNote["text"])
	}

	// A bookkeeping re-write while still parked must not double-ping.
	if _, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "system", map[string]string{"progressPercent": "70"}); err != nil {
		t.Fatalf("re-write parked artifact: %v", err)
	}
	count := 0
	for _, note := range app.notificationsForUser(artifactLibraryAdminEmail, 50) {
		if asString(note["tool"]) == "approval" && asString(note["artifactId"]) == artifact.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("admin was pinged %d times, want exactly once per gate entry", count)
	}
}

// --- helpers ----------------------------------------------------------------

func doArtifactAction(t *testing.T, email string, id string, action string, reason string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"id":%q,"action":%q,"reason":%q}`, id, action, reason)
	req := httptest.NewRequest(http.MethodPost, "/artifacts/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, email, "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactRunnerActionHandler(recorder, req)
	return recorder
}

func containsNotificationText(notes []map[string]any, substr string) bool {
	for _, note := range notes {
		if strings.Contains(asString(note["text"]), substr) {
			return true
		}
	}
	return false
}
