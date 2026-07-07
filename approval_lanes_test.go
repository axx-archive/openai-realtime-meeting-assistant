package main

// Card 069 — the approval-lane governance default. approvalLaneFor is the one
// classifier (088 auto-select reads it), approvalLanesPayload the one taxonomy
// (067's ticker reads it off GET /assistant/tools), every launch/proposal
// stamps metadata["approvalLane"], the decision itself rides the ledger as
// PROPOSED with a ratify door, and the heavy lane gains the 2-member consensus
// path beside the admin.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApprovalLaneFor(t *testing.T) {
	deployAuthority := codexJobAuthorityForThread(scoutAgentThread{Mode: "workflow", Query: "commit, push, and deploy this to production"})
	if deployAuthority != codexJobAuthorityExternalWrite {
		t.Fatalf("deploy-phrase authority=%q, want external_write (the heavy-lane fixture)", deployAuthority)
	}

	for _, tc := range []struct {
		name           string
		mode           string
		toolTemplate   string
		authority      string
		systemProposed bool
		want           string
	}{
		{name: "human research read_only", mode: "research", authority: codexJobAuthorityReadOnly, want: approvalLaneAuto},
		{name: "human design workspace_write", mode: "design", authority: codexJobAuthorityWorkspaceWrite, want: approvalLaneAuto},
		{name: "blank authority defaults workspace_write", mode: "workflow", want: approvalLaneAuto},
		{name: "goal loop", mode: "goal", authority: codexJobAuthorityWorkspaceWrite, want: approvalLaneStandard},
		{name: "tool template routes the goal engine", mode: "workflow", toolTemplate: "packaging_studio", authority: codexJobAuthorityWorkspaceWrite, want: approvalLaneStandard},
		{name: "system-proposed is never auto", mode: "research", authority: codexJobAuthorityReadOnly, systemProposed: true, want: approvalLaneStandard},
		{name: "external_write is heavy", mode: "workflow", authority: codexJobAuthorityExternalWrite, want: approvalLaneHeavy},
		{name: "deploy-phrase class is heavy", mode: "workflow", authority: deployAuthority, want: approvalLaneHeavy},
		{name: "heavy outranks standard", mode: "goal", toolTemplate: "packaging_studio", authority: codexJobAuthorityExternalWrite, systemProposed: true, want: approvalLaneHeavy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := approvalLaneFor(tc.mode, tc.toolTemplate, tc.authority, tc.systemProposed); got != tc.want {
				t.Fatalf("approvalLaneFor(%q,%q,%q,%v)=%q, want %q", tc.mode, tc.toolTemplate, tc.authority, tc.systemProposed, got, tc.want)
			}
		})
	}
}

func TestApprovalLanesPayloadShape(t *testing.T) {
	lanes := approvalLanesPayload()
	if len(lanes) != 3 {
		t.Fatalf("lanes=%d, want 3", len(lanes))
	}
	wantIDs := []string{approvalLaneAuto, approvalLaneStandard, approvalLaneHeavy}
	for index, lane := range lanes {
		if lane["id"] != wantIDs[index] {
			t.Fatalf("lane[%d] id=%v, want %q", index, lane["id"], wantIDs[index])
		}
		for _, key := range []string{"label", "rule", "approvers"} {
			if value, _ := lane[key].(string); strings.TrimSpace(value) == "" {
				t.Fatalf("lane %q missing %q: %v", wantIDs[index], key, lane)
			}
		}
		// No invented numbers: the repo has no token-cost meter, so a lane rule
		// must never carry a dollar figure.
		if rule, _ := lane["rule"].(string); strings.Contains(rule, "$") {
			t.Fatalf("lane %q rule carries a dollar figure the repo cannot back: %q", wantIDs[index], rule)
		}
	}
}

// GET /assistant/tools carries the lanes block beside the tool groups — the
// one taxonomy door the ticker and the palette already read.
func TestAssistantToolsHandlerCarriesLanes(t *testing.T) {
	setupAuthTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/assistant/tools", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantToolsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK     bool             `json:"ok"`
		Groups []map[string]any `json:"groups"`
		Lanes  []map[string]any `json:"lanes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.OK || len(payload.Groups) == 0 {
		t.Fatalf("payload=%s, want ok with tool groups intact", recorder.Body.String())
	}
	if len(payload.Lanes) != 3 || payload.Lanes[0]["id"] != approvalLaneAuto || payload.Lanes[2]["id"] != approvalLaneHeavy {
		t.Fatalf("lanes=%v, want the auto/standard/heavy taxonomy", payload.Lanes)
	}
}

// Every human thread launch stamps its lane: quick single passes are auto,
// a deploy-phrase query classifies heavy from launch (it WILL park).
func TestLaunchAgentThreadStampsApprovalLane(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	quick, err := app.launchAgentThreadWithOrigin("research", "map the operator landscape", "AJ", map[string]string{"originKind": agentThreadOriginTool})
	if err != nil {
		t.Fatalf("launch quick pass: %v", err)
	}
	if quick.Artifact.Metadata["approvalLane"] != approvalLaneAuto {
		t.Fatalf("quick-pass lane=%q, want auto: %v", quick.Artifact.Metadata["approvalLane"], quick.Artifact.Metadata)
	}

	heavy, err := app.launchAgentThreadWithOrigin("workflow", "commit, push, and deploy this to production", "AJ", map[string]string{"originKind": agentThreadOriginTool})
	if err != nil {
		t.Fatalf("launch heavy thread: %v", err)
	}
	if heavy.Artifact.Metadata["approvalLane"] != approvalLaneHeavy {
		t.Fatalf("deploy-phrase lane=%q, want heavy: %v", heavy.Artifact.Metadata["approvalLane"], heavy.Artifact.Metadata)
	}
}

// A /goal loop stamps standard at launch — one member approval is the lane
// rule, and the requester's own tap already collected it.
func TestGoalLaunchStampsApprovalLane(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective: "Assemble the Nimbus brief",
		CreatedBy: "aj@shareability.com",
		Authority: codexJobAuthorityWorkspaceWrite,
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	if thread.Artifact.Metadata["approvalLane"] != approvalLaneStandard {
		t.Fatalf("goal lane=%q, want standard: %v", thread.Artifact.Metadata["approvalLane"], thread.Artifact.Metadata)
	}
}

// Scout-proposed work is never auto: the proposal stamps standard (heavy on a
// deploy-phrase query) and the wire payload carries the lane.
func TestProposeCodexTaskStampsApprovalLane(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, _, err := app.proposeCodexTask(map[string]any{
		"title": "Audit the pricing page",
		"mode":  "research",
		"query": "audit the pricing page copy and report findings",
	}, "board_worker")
	if err != nil {
		t.Fatalf("proposeCodexTask: %v", err)
	}
	proposal, _ := result["proposal"].(map[string]any)
	if proposal["approvalLane"] != approvalLaneStandard {
		t.Fatalf("proposal lane=%v, want standard: %v", proposal["approvalLane"], proposal)
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, asString(proposal["id"]))
	if !ok || entry.Metadata["approvalLane"] != approvalLaneStandard {
		t.Fatalf("stored proposal lane=%q, want standard", entry.Metadata["approvalLane"])
	}

	heavy, _, err := app.proposeCodexTask(map[string]any{
		"title": "Ship the fix",
		"mode":  "workflow",
		"query": "commit, push, and deploy the fix to production",
	}, "board_worker")
	if err != nil {
		t.Fatalf("proposeCodexTask heavy: %v", err)
	}
	heavyProposal, _ := heavy["proposal"].(map[string]any)
	if heavyProposal["approvalLane"] != approvalLaneHeavy {
		t.Fatalf("deploy-phrase proposal lane=%v, want heavy", heavyProposal["approvalLane"])
	}
}

// The governance default seeds exactly once as PROPOSED, stays invisible to
// every active lane until ratified, and never re-seeds after the team flips
// it active (the boot-loop guard).
func TestSeedProposedGovernanceDecisionIdempotent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.seedProposedGovernanceDecision()
	app.seedProposedGovernanceDecision()
	entries := app.memory.entriesOfKind(meetingMemoryKindDecision, 0)
	if len(entries) != 1 {
		t.Fatalf("decision rows=%d, want exactly 1 after a double seed", len(entries))
	}
	seeded := entries[0]
	if seeded.Metadata["status"] != decisionStatusProposed || seeded.Text != governanceLanesDecisionStatement {
		t.Fatalf("seeded row=%v %q, want the proposed governance statement", seeded.Metadata, seeded.Text)
	}
	if seeded.Metadata["madeBy"] != "Scout" || !strings.Contains(seeded.Metadata["context"], "069") {
		t.Fatalf("seeded provenance=%v, want Scout + card 069 context", seeded.Metadata)
	}
	// Proposed rows ground nothing: excluded from the Scout query pinning and
	// the dedupe/already-recorded lanes built on activeDecisionEntries.
	if active := app.activeDecisionEntries(10); len(active) != 0 {
		t.Fatalf("active entries=%d, want 0 while the default awaits ratification", len(active))
	}
	// The mission ledger still SHOWS it (inactive tail) so the team can act.
	snapshot := app.decisionLedgerSnapshot(10)
	if len(snapshot) != 1 || snapshot[0]["status"] != decisionStatusProposed {
		t.Fatalf("ledger snapshot=%v, want the proposed row visible", snapshot)
	}

	// Ratify, then re-seed: the scan covers ALL rows, so a ratified copy never
	// re-seeds as proposed.
	if _, changed, err := app.markDecisionRatified(seeded.ID, "AJ"); err != nil || !changed {
		t.Fatalf("markDecisionRatified: changed=%v err=%v", changed, err)
	}
	app.seedProposedGovernanceDecision()
	if entries := app.memory.entriesOfKind(meetingMemoryKindDecision, 0); len(entries) != 1 {
		t.Fatalf("decision rows=%d after ratify+reseed, want still 1", len(entries))
	}
	if active := app.activeDecisionEntries(10); len(active) != 1 || active[0].Metadata["ratifiedBy"] != "AJ" {
		t.Fatalf("active entries=%v, want the ratified decision with its stamp", active)
	}
}

func TestAssistantDecisionRatifyHandler(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	kanbanApp.seedProposedGovernanceDecision()
	seeded := kanbanApp.memory.entriesOfKind(meetingMemoryKindDecision, 0)[0]
	if _, _, err := kanbanApp.memory.appendDecision("decision-superseded", "Old pick.", map[string]string{"status": decisionStatusSuperseded}); err != nil {
		t.Fatalf("append superseded: %v", err)
	}
	body := fmt.Sprintf(`{"decisionId":%q}`, seeded.ID)

	// Method gate.
	rec := httptest.NewRecorder()
	assistantDecisionRatifyHandler(rec, httptest.NewRequest(http.MethodGet, "/assistant/decisions/ratify", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d, want 405", rec.Code)
	}

	// Session gate: signed-out flips nothing.
	rec = httptest.NewRecorder()
	assistantDecisionRatifyHandler(rec, httptest.NewRequest(http.MethodPost, "/assistant/decisions/ratify", strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want 401", rec.Code)
	}
	if active := kanbanApp.activeDecisionEntries(5); len(active) != 0 {
		t.Fatalf("active entries=%d, want 0 after the rejected call", len(active))
	}

	// Any signed-in member ratifies — the default was recorded for the team.
	cookies := loginAs(t, "tyler@shareability.com", "B0NFIRE!")
	post := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/decisions/ratify", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		assistantDecisionRatifyHandler(rec, req)
		return rec
	}
	rec = post(body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK       bool           `json:"ok"`
		Changed  bool           `json:"changed"`
		Decision map[string]any `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.OK || !payload.Changed {
		t.Fatalf("payload=%+v, want ok+changed", payload)
	}
	if payload.Decision["status"] != decisionStatusActive || payload.Decision["ratifiedBy"] != "Tyler" {
		t.Fatalf("decision payload=%v, want active with the ratifier stamped", payload.Decision)
	}
	if active := kanbanApp.activeDecisionEntries(5); len(active) != 1 || active[0].ID != seeded.ID {
		t.Fatalf("active entries=%v, want the ratified default in the active lane", active)
	}

	// Idempotent retry reports changed=false; unknown ids 404; superseded 400.
	rec = post(body)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"changed":true`) {
		t.Fatalf("retry status=%d body=%s, want 200 with changed=false", rec.Code, rec.Body.String())
	}
	if rec := post(`{"decisionId":"decision-missing"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing-id status=%d, want 404", rec.Code)
	}
	if rec := post(`{"decisionId":"decision-superseded"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("superseded status=%d, want 400", rec.Code)
	}
}

// The heavy-lane consensus door: two DISTINCT non-admin members equal one
// admin. One endorsement parks (202, 1/2, no job); the same member twice stays
// 1/2; the second distinct member executes the approve path exactly once; a
// late approve after the ship is refused.
func TestArtifactActionEndorsementConsensusLaunchesOnce(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)

	artifact := approvalArtifact(t, kanbanApp, "workflow", "Deploy the release", "joel@shareability.com")

	// First member: endorsement recorded, nothing launches.
	first := doArtifactAction(t, "tyler@shareability.com", artifact.ID, "approve", "")
	if first.Code != http.StatusAccepted || !strings.Contains(first.Body.String(), "endorsement recorded (1/2)") {
		t.Fatalf("first endorse status=%d body=%s, want 202 with 1/2", first.Code, first.Body.String())
	}
	parked, _ := kanbanApp.osArtifactByID(artifact.ID)
	if !artifactAwaitingApproval(parked.Metadata) {
		t.Fatalf("one endorsement un-parked the gate: %v", parked.Metadata)
	}
	if endorsements := decodeApprovalEndorsements(parked.Metadata[approvalEndorsementsKey]); len(endorsements) != 1 || endorsements[0] != "tyler@shareability.com" {
		t.Fatalf("endorsements=%v, want tyler recorded once", endorsements)
	}
	store := newCodexRunnerJobStore(queueDir)
	if job, _ := store.claimNext("probe"); job != nil {
		t.Fatalf("a single endorsement queued a job: %+v", job)
	}

	// The same member twice stays 1/2 — endorsements dedupe on the email.
	repeat := doArtifactAction(t, "tyler@shareability.com", artifact.ID, "approve", "")
	if repeat.Code != http.StatusAccepted || !strings.Contains(repeat.Body.String(), "endorsement recorded (1/2)") {
		t.Fatalf("repeat endorse status=%d body=%s, want 202 still at 1/2", repeat.Code, repeat.Body.String())
	}

	// The second DISTINCT member completes the consensus and ships.
	second := doArtifactAction(t, "caitlyn@shareability.com", artifact.ID, "approve", "")
	if second.Code != http.StatusAccepted {
		t.Fatalf("second endorse status=%d body=%s, want 202", second.Code, second.Body.String())
	}
	shipped, _ := kanbanApp.osArtifactByID(artifact.ID)
	if shipped.Metadata["threadStatus"] != codexJobStatusQueued || shipped.Metadata["reviewGate"] != "approved" {
		t.Fatalf("consensus did not execute the approve path: %v", shipped.Metadata)
	}
	if shipped.Metadata["approvedBy"] != "Caitlyn" {
		t.Fatalf("approvedBy=%q, want the executing endorser", shipped.Metadata["approvedBy"])
	}
	if strings.TrimSpace(shipped.Metadata[approvalConsensusAtKey]) == "" {
		t.Fatalf("consensus stamp missing: %v", shipped.Metadata)
	}
	if strings.TrimSpace(shipped.Metadata[artifactHumanApprovedAtKey]) == "" {
		t.Fatalf("consensus approve did not stamp the durable human approval: %v", shipped.Metadata)
	}
	job, err := store.claimNext("probe")
	if err != nil || job == nil {
		t.Fatalf("claimNext job=%v err=%v, want exactly one approved job", job, err)
	}
	if job.Authority != codexJobAuthorityExternalWrite {
		t.Fatalf("job authority=%q, want external_write", job.Authority)
	}
	if extra, _ := store.claimNext("probe"); extra != nil {
		t.Fatalf("consensus double-launched: %+v", extra)
	}

	// Once shipped, the gate is gone: a further non-admin approve is 403 (the
	// pre-069 admin-only refusal), never a fresh endorsement round.
	late := doArtifactAction(t, "tim@shareability.com", artifact.ID, "approve", "")
	if late.Code != http.StatusForbidden {
		t.Fatalf("late approve status=%d body=%s, want 403", late.Code, late.Body.String())
	}
}

// Reject stays admin-only even while endorsements are pending.
func TestArtifactActionRejectStaysAdminOnlyUnderConsensus(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", t.TempDir())

	artifact := approvalArtifact(t, kanbanApp, "workflow", "Send the outreach email", "joel@shareability.com")
	if rec := doArtifactAction(t, "tyler@shareability.com", artifact.ID, "approve", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("endorse status=%d, want 202", rec.Code)
	}
	if rec := doArtifactAction(t, "tyler@shareability.com", artifact.ID, "reject", "not yet"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin reject status=%d, want 403", rec.Code)
	}
	if rec := doArtifactAction(t, artifactLibraryAdminEmail, artifact.ID, "reject", "not yet"); rec.Code != http.StatusOK {
		t.Fatalf("admin reject status=%d, want 200", rec.Code)
	}
	// The rejected gate is closed: an endorsement afterwards is refused.
	if rec := doArtifactAction(t, "caitlyn@shareability.com", artifact.ID, "approve", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("post-reject approve status=%d, want 403", rec.Code)
	}
}
