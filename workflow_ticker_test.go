package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// appendTickerProposal seeds a codex_proposal memory entry with an explicit
// metadata shape so a test can drive the ticker's eligibility branches directly.
func appendTickerProposal(t *testing.T, app *kanbanBoardApp, id string, metadata map[string]string) {
	t.Helper()
	if metadata["title"] == "" {
		metadata["title"] = id
	}
	if metadata["query"] == "" {
		metadata["query"] = "Research " + id + " and draft a brief."
	}
	if metadata["mode"] == "" {
		metadata["mode"] = "research"
	}
	if _, appended, err := app.memory.appendCodexProposal(id, "Scout proposes "+id, metadata); err != nil || !appended {
		t.Fatalf("append proposal %s: appended=%v err=%v", id, appended, err)
	}
}

func stubTickerLaunches(t *testing.T, capture *scoutAgentThread) *int {
	t.Helper()
	count := 0
	previous := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) {
		count++
		if capture != nil {
			*capture = thread
		}
	}
	t.Cleanup(func() { startAgentThreadAsync = previous })
	return &count
}

// A proposed, standard-lane proposal (the board worker's never-auto-run draft)
// is never launched by a ticker pass — the draft pin is provable.
func TestWorkflowTickerNeverLaunchesDraftProposals(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	launches := stubTickerLaunches(t, nil)

	appendTickerProposal(t, app, "codex-proposal-draft", map[string]string{
		"status": codexProposalStatusProposed,
		// no lane / laneApprovedBy → standard draft, human confirm required
	})
	// An auto_run lane WITHOUT a recorded approval is still a draft.
	appendTickerProposal(t, app, "codex-proposal-lane-unapproved", map[string]string{
		"status": codexProposalStatusProposed,
		"lane":   codexProposalLaneAutoRun,
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 0 || *launches != 0 {
		t.Fatalf("draft pass launched=%d stub=%d, want 0", got, *launches)
	}
}

// A confirmed proposal whose launch never stamped a threadId (the crash gap in
// resolveCodexProposal) relaunches exactly once past the grace window; the
// second pass is a no-op once the relaunch stamps the threadId.
func TestWorkflowTickerRelaunchesConfirmedButUnstamped(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	launches := stubTickerLaunches(t, nil)

	id := "codex-proposal-stuck"
	appendTickerProposal(t, app, id, map[string]string{
		"status":           codexProposalStatusConfirmed,
		"confirmedBy":      "Tom",
		"confirmedByEmail": "tom@shareability.com",
		"resolvedAt":       time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano),
		// threadId intentionally absent — the stamp never landed.
	})

	// Inside the grace window the ticker leaves it alone.
	fresh := "codex-proposal-fresh-confirm"
	appendTickerProposal(t, app, fresh, map[string]string{
		"status":           codexProposalStatusConfirmed,
		"confirmedBy":      "Tom",
		"confirmedByEmail": "tom@shareability.com",
		"resolvedAt":       time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339Nano),
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 1 || *launches != 1 {
		t.Fatalf("first pass launched=%d stub=%d, want exactly the past-grace stuck proposal", got, *launches)
	}

	entry, _ := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, id)
	if strings.TrimSpace(entry.Metadata["threadId"]) == "" {
		t.Fatalf("relaunch must stamp a threadId, got metadata=%#v", entry.Metadata)
	}
	if entry.Metadata["confirmedBy"] != "Tom" {
		t.Fatalf("relaunch must preserve the original confirmer, got confirmedBy=%q", entry.Metadata["confirmedBy"])
	}

	// Second pass: the stamped proposal is done, the fresh confirm is still in
	// grace → no new launch.
	if got := app.runWorkflowTickerOnce(time.Now()); got != 0 || *launches != 1 {
		t.Fatalf("second pass launched=%d stub=%d, want no relaunch after the stamp", got, *launches)
	}
}

// A confirmed-but-unstamped proposal whose launch ALREADY ran (it recorded its
// proposal_confirmed signal) but whose follow-up threadId stamp write failed
// non-fatally must NOT be relaunched by the ticker — a second launch would
// double the agent thread and re-settle the notification. The empty threadId is
// lost linkage, not the crash gap Case A recovers.
func TestWorkflowTickerDoesNotRelaunchSettledUnstampedProposal(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	launches := stubTickerLaunches(t, nil)

	id := "codex-proposal-settled-unstamped"
	appendTickerProposal(t, app, id, map[string]string{
		"status":           codexProposalStatusConfirmed,
		"confirmedBy":      "Tom",
		"confirmedByEmail": "tom@shareability.com",
		"resolvedAt":       time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano),
		// threadId absent — the stamp write failed non-fatally AFTER a real launch.
	})
	// launchApprovedProposal records the confirm signal right after the
	// successful launch, so this signal is the durable proof the launch ran even
	// though the later threadId stamp never landed.
	app.recordSignalEvent("Tom", signalEventProposalConfirmed, signalValencePositive, "agent-thread-artifact-x", "", map[string]string{
		"proposalId": id,
		"title":      id,
		"mode":       "research",
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 0 || *launches != 0 {
		t.Fatalf("settled-but-unstamped proposal launched=%d stub=%d, want 0 (no double launch)", got, *launches)
	}

	// The proposal is untouched — still confirmed, still unstamped.
	entry, _ := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, id)
	if entry.Metadata["status"] != codexProposalStatusConfirmed {
		t.Fatalf("status=%q, want it left confirmed (no revert, no relaunch)", entry.Metadata["status"])
	}
}

// An auto_run-lane proposal WITH a recorded standing approval launches with an
// honest "workflow ticker · standing approval" confirmedBy; the same lane
// WITHOUT laneApprovedBy never launches.
func TestWorkflowTickerAutoRunLaneRequiresStandingApproval(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	launches := stubTickerLaunches(t, nil)

	approved := "codex-proposal-auto-approved"
	appendTickerProposal(t, app, approved, map[string]string{
		"status":         codexProposalStatusProposed,
		"lane":           codexProposalLaneAutoRun,
		"laneApprovedBy": "aj@shareability.com",
	})
	appendTickerProposal(t, app, "codex-proposal-auto-noapprover", map[string]string{
		"status": codexProposalStatusProposed,
		"lane":   codexProposalLaneAutoRun,
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 1 || *launches != 1 {
		t.Fatalf("auto_run pass launched=%d stub=%d, want only the standing-approved proposal", got, *launches)
	}

	entry, _ := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, approved)
	if entry.Metadata["status"] != codexProposalStatusConfirmed {
		t.Fatalf("standing-approved proposal status=%q, want confirmed", entry.Metadata["status"])
	}
	if by := entry.Metadata["confirmedBy"]; !strings.HasPrefix(by, workflowTickerAgentName+" · standing approval:") {
		t.Fatalf("confirmedBy=%q, want the standing-approval provenance", by)
	}
}

// W0-7/8: a ticker launch records the proposal-lifecycle launched event
// (path=ticker) plus one workflow-run provenance row per launch shape — Case A
// carries the original confirmer as approver, Case B the standing approval's
// grantor. The terminal outcome is the agent-thread runner's row (joined on
// thread_id), so the ticker records exactly the launch.
func TestWorkflowTickerLaunchRecordsProposalAndWorkflowProvenance(t *testing.T) {
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 17, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	defer func() { usageLedgerNow = prevNow }()

	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("BONFIRE_WORKFLOW_TICKER_MAX_PER_PASS", "2")
	launches := stubTickerLaunches(t, nil)

	caseA := "codex-proposal-provenance-a"
	appendTickerProposal(t, app, caseA, map[string]string{
		"status":           codexProposalStatusConfirmed,
		"confirmedBy":      "Tom",
		"confirmedByEmail": "tom@shareability.com",
		"resolvedAt":       time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano),
		"proposedBy":       "board_worker",
		"approvalLane":     "standard",
	})
	caseB := "codex-proposal-provenance-b"
	appendTickerProposal(t, app, caseB, map[string]string{
		"status":         codexProposalStatusProposed,
		"lane":           codexProposalLaneAutoRun,
		"laneApprovedBy": "aj@shareability.com",
		"proposedBy":     "board_worker",
		"approvalLane":   "auto",
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 2 || *launches != 2 {
		t.Fatalf("pass launched=%d stub=%d, want both provenance proposals", got, *launches)
	}

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	launchedPaths := map[string]map[string]any{}
	runs := map[string]map[string]any{}
	for _, row := range rows {
		switch row["type"] {
		case telemetryTypeProposal:
			if row["kind"] != proposalEventLaunched {
				continue
			}
			fields := row["fields"].(map[string]any)
			id, _ := fields["proposal_id"].(string)
			if id == "" {
				// The ticker threads its proposal id onto the choke point's
				// single launched row, so its rows carry the id; any id-less
				// launched row is not the ticker's.
				continue
			}
			launchedPaths[id] = fields
		case telemetryTypeWorkflowRun:
			run := row["fields"].(map[string]any)["run"].(map[string]any)
			if run["trigger_surface"] == triggerSurfaceTicker {
				runs[run["proposal_id"].(string)] = run
			}
		}
	}

	fieldsA, ok := launchedPaths[caseA]
	if !ok || fieldsA["path"] != triggerSurfaceTicker || fieldsA["lane"] != codexProposalLaneStandard {
		t.Fatalf("case-A launched event = %v, want path=ticker lane=standard", fieldsA)
	}
	fieldsB, ok := launchedPaths[caseB]
	if !ok || fieldsB["path"] != triggerSurfaceTicker || fieldsB["lane"] != codexProposalLaneAutoRun {
		t.Fatalf("case-B launched event = %v, want path=ticker lane=auto_run", fieldsB)
	}

	runA, ok := runs[caseA]
	if !ok {
		t.Fatalf("case-A workflow_run row missing: %v", runs)
	}
	if runA["workflow_id"] != "research" || runA["approver"] != "Tom" ||
		runA["proposer"] != "board_worker" || runA["lane"] != "standard" ||
		runA["outcome"] != workflowOutcomeLaunched {
		t.Fatalf("case-A run provenance = %v", runA)
	}
	if threadID, _ := runA["thread_id"].(string); strings.TrimSpace(threadID) == "" {
		t.Fatalf("case-A run must carry the launched thread id: %v", runA)
	}
	runB, ok := runs[caseB]
	if !ok {
		t.Fatalf("case-B workflow_run row missing: %v", runs)
	}
	if runB["approver"] != "aj@shareability.com" || runB["lane"] != "auto" {
		t.Fatalf("case-B run must carry the standing approval's grantor: %v", runB)
	}
}

// grill mode stays manual, an external-write-phrase query parks at the codex
// gate (skipped here as defense in depth), and no more than the per-pass cap
// launch even when more are eligible.
func TestWorkflowTickerSkipsUnsafeAndRespectsCap(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("BONFIRE_WORKFLOW_TICKER_MAX_PER_PASS", "2")
	launches := stubTickerLaunches(t, nil)

	// grill stays manual even with a standing approval.
	appendTickerProposal(t, app, "codex-proposal-grill", map[string]string{
		"status":         codexProposalStatusProposed,
		"mode":           "grill",
		"lane":           codexProposalLaneAutoRun,
		"laneApprovedBy": "aj@shareability.com",
	})
	// external-write phrase parks at the approval gate — the ticker refuses it.
	appendTickerProposal(t, app, "codex-proposal-deploy", map[string]string{
		"status":         codexProposalStatusProposed,
		"mode":           "workflow",
		"query":          "Deploy the release to the VPS and restart production.",
		"lane":           codexProposalLaneAutoRun,
		"laneApprovedBy": "aj@shareability.com",
	})
	// Four safe, eligible auto_run proposals; only the cap should launch.
	for _, id := range []string{"codex-proposal-cap-a", "codex-proposal-cap-b", "codex-proposal-cap-c", "codex-proposal-cap-d"} {
		appendTickerProposal(t, app, id, map[string]string{
			"status":         codexProposalStatusProposed,
			"mode":           "research",
			"query":          "Research the " + id + " landscape and draft a brief.",
			"lane":           codexProposalLaneAutoRun,
			"laneApprovedBy": "aj@shareability.com",
		})
	}

	if got := app.runWorkflowTickerOnce(time.Now()); got != 2 || *launches != 2 {
		t.Fatalf("pass launched=%d stub=%d, want the per-pass cap of 2", got, *launches)
	}

	// The unsafe proposals never confirmed.
	for _, id := range []string{"codex-proposal-grill", "codex-proposal-deploy"} {
		entry, _ := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, id)
		if entry.Metadata["status"] != codexProposalStatusProposed {
			t.Fatalf("unsafe proposal %s status=%q, want it left proposed", id, entry.Metadata["status"])
		}
	}
}

// 068 routing: a valid public originThreadId wins outright.
func TestWorkflowTickerRoutesToOriginThread(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var launched scoutAgentThread
	stubTickerLaunches(t, &launched)

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "wildcard operations", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	appendTickerProposal(t, app, "codex-proposal-origin", map[string]string{
		"status":         codexProposalStatusProposed,
		"lane":           codexProposalLaneAutoRun,
		"laneApprovedBy": "aj@shareability.com",
		"originThreadId": channel.ID,
		"title":          "Totally unrelated title",
		"query":          "Research something with no channel-title overlap at all.",
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 1 {
		t.Fatalf("launched=%d, want 1", got)
	}
	if launched.Artifact.Metadata["originKind"] != agentThreadOriginChannel || launched.Artifact.Metadata["originId"] != channel.ID {
		t.Fatalf("origin=%q/%q, want the captured channel %q", launched.Artifact.Metadata["originKind"], launched.Artifact.Metadata["originId"], channel.ID)
	}
	// The originating thread wins with no routeNote (it is not a fallback).
	if note := launched.Artifact.Metadata["routeNote"]; note != "" {
		t.Fatalf("routeNote=%q, want none for a captured origin thread", note)
	}
}

// 068 routing: a private/archived originThreadId is ignored, and the token-set
// best-match public channel wins instead.
func TestWorkflowTickerRoutesToBestMatchChannel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var launched scoutAgentThread
	stubTickerLaunches(t, &launched)

	// A private thread with a PERFECT title match the ticker must still refuse
	// to deliver into (visibility wins over similarity).
	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "rodeo creator landscape brief", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	// The public channel whose title overlaps the proposal decisively.
	match, err := app.createScoutChatThread("aj@shareability.com", "AJ", "rodeo creator landscape brief", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create match channel: %v", err)
	}

	appendTickerProposal(t, app, "codex-proposal-bestmatch", map[string]string{
		"status":         codexProposalStatusProposed,
		"lane":           codexProposalLaneAutoRun,
		"laneApprovedBy": "aj@shareability.com",
		"originThreadId": private.ID, // private → must fall through
		"title":          "Rodeo creator landscape",
		"query":          "Rodeo creator landscape brief",
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 1 {
		t.Fatalf("launched=%d, want 1", got)
	}
	if launched.Artifact.Metadata["originId"] != match.ID {
		t.Fatalf("origin channel=%q, want the best-match channel %q (private origin ignored)", launched.Artifact.Metadata["originId"], match.ID)
	}
	if note := launched.Artifact.Metadata["routeNote"]; !strings.Contains(note, "best match") {
		t.Fatalf("routeNote=%q, want a best-match disclosure", note)
	}
}

// 068 routing: no channels + a resolvable owner mints #general, and the launch
// card posted into it carries the routeNote disclosure.
func TestWorkflowTickerRoutesToGeneralWithRouteNote(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var launched scoutAgentThread
	stubTickerLaunches(t, &launched)

	appendTickerProposal(t, app, "codex-proposal-general", map[string]string{
		"status":         codexProposalStatusProposed,
		"lane":           codexProposalLaneAutoRun,
		"laneApprovedBy": "aj@shareability.com",
		"title":          "Comparable exits brief",
		"query":          "Research comparable exits and draft a brief.",
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 1 {
		t.Fatalf("launched=%d, want 1", got)
	}
	if launched.Artifact.Metadata["originKind"] != agentThreadOriginChannel {
		t.Fatalf("originKind=%q, want the #general channel origin", launched.Artifact.Metadata["originKind"])
	}
	if note := launched.Artifact.Metadata["routeNote"]; !strings.Contains(note, "#general") {
		t.Fatalf("routeNote=%q, want a #general fallback disclosure", note)
	}

	channel, err := app.publicChannelByName("general")
	if err != nil {
		t.Fatalf("#general not created: %v", err)
	}
	foundLaunchCard := false
	for _, message := range channel.Messages {
		if message.Thread != nil && message.Thread.ID == launched.ID {
			foundLaunchCard = true
			if message.Thread.Status != "running" {
				t.Fatalf("launch card status=%q, want running", message.Thread.Status)
			}
			if !strings.Contains(message.Text, "#general") {
				t.Fatalf("launch card text=%q, want it to carry the routeNote", message.Text)
			}
		}
	}
	if !foundLaunchCard {
		t.Fatalf("#general missing the running launch card for thread %s", launched.ID)
	}
}

// 068 routing: no channels and no resolvable owner email falls back to the
// creator-notification-only tool origin — never an owner-less channel.
func TestWorkflowTickerRoutesToToolWhenNoOwnerEmail(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var launched scoutAgentThread
	stubTickerLaunches(t, &launched)

	appendTickerProposal(t, app, "codex-proposal-noowner", map[string]string{
		"status": codexProposalStatusProposed,
		"lane":   codexProposalLaneAutoRun,
		// A non-email standing approver: honored as approval, but yields no
		// owner email to mint #general under.
		"laneApprovedBy": "AJ",
	})

	if got := app.runWorkflowTickerOnce(time.Now()); got != 1 {
		t.Fatalf("launched=%d, want 1", got)
	}
	if launched.Artifact.Metadata["originKind"] != agentThreadOriginTool {
		t.Fatalf("originKind=%q, want the notification-only tool origin", launched.Artifact.Metadata["originKind"])
	}
}

// The HTTP confirm and a ticker pass share proposalMu, and a confirm stamps the
// threadId — so a tick after a confirm can never double-launch.
func TestWorkflowTickerDoesNotDoubleLaunchAfterConfirm(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	launches := stubTickerLaunches(t, nil)

	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Ordering guarantees",
		"mode":  "research",
		"query": "Research ordering guarantees and draft a brief.",
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	id := asString(result["proposal"].(map[string]any)["id"])

	if _, launchedConfirm, err := app.resolveCodexProposal(id, "confirm", "Tom", "tom@shareability.com"); err != nil || !launchedConfirm {
		t.Fatalf("confirm launched=%v err=%v", launchedConfirm, err)
	}
	if *launches != 1 {
		t.Fatalf("after confirm launches=%d, want 1", *launches)
	}

	// A ticker pass sees the confirmed+stamped proposal and does nothing.
	if got := app.runWorkflowTickerOnce(time.Now()); got != 0 || *launches != 1 {
		t.Fatalf("ticker after confirm launched=%d stub=%d, want no second launch", got, *launches)
	}
}

// deliverArtifactToOrigin appends the routeNote to the channel completion card
// when no in-channel launch ref exists (the fallback delivery path).
func TestDeliverArtifactAppendsRouteNoteToChannelCompletion(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Research growth levers", "Growth brief body", "AJ", map[string]string{
		"title":      "Growth levers brief",
		"mode":       "research",
		"originKind": agentThreadOriginChannel,
		"originId":   channel.ID,
		"routeNote":  "routed to #general — no originating thread",
		"threadId":   "agent-thread-research-unreffed",
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	app.deliverArtifactToOrigin(artifact, "agent-thread-research-unreffed")

	delivered, err := app.publicChannelByName("growth")
	if err != nil {
		t.Fatalf("channel lookup: %v", err)
	}
	found := false
	for _, message := range delivered.Messages {
		if message.Thread != nil && message.Thread.ArtifactID == artifact.ID {
			found = true
			if !strings.Contains(message.Text, "routed to #general") {
				t.Fatalf("completion card text=%q, want the appended routeNote", message.Text)
			}
		}
	}
	if !found {
		t.Fatalf("channel missing the completion card for artifact %s", artifact.ID)
	}
}

// The readiness snapshot exposes the ticker's config, and the disable env turns
// it off.
func TestReadinessWorkflowTickerSnapshot(t *testing.T) {
	t.Setenv("BONFIRE_WORKFLOW_TICKER_INTERVAL", "5m")
	t.Setenv("BONFIRE_WORKFLOW_TICKER_DISABLED", "")
	snapshot := readinessWorkflowTickerSnapshot()
	if snapshot["enabled"] != true {
		t.Fatalf("snapshot enabled=%v, want true", snapshot["enabled"])
	}
	if snapshot["interval"] != "5m0s" {
		t.Fatalf("snapshot interval=%v, want 5m0s", snapshot["interval"])
	}

	t.Setenv("BONFIRE_WORKFLOW_TICKER_DISABLED", "true")
	if readinessWorkflowTickerSnapshot()["enabled"] != false {
		t.Fatal("disable env must turn the ticker off in the readiness snapshot")
	}
}
