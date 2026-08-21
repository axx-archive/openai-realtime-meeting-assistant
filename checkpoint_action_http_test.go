package main

// The /artifacts/action door's checkpoint teeth (Wave 5 fix): the handler must
// decode {choice} and forward it through resumeApprovedGoalWithChoice. Before
// this fix the payload struct silently dropped choice and the goal branch
// called resumeApprovedGoal (choice=""), so every negative option — "hold the
// package" at ship_approval, "send back for changes" at founder_pass — decayed
// into a silent PROCEED. The engine tests exercise the app seam directly;
// these two drive the REAL HTTP handler end to end.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicGoalCheckpointProjectsOntoRootAndOpaqueChoiceIsReplaySafe(t *testing.T) {
	app, user, channel, source, binding := newAcceptedPublicWorkFixture(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	app.apiKey = "test-key"
	launched := installFakeChildRunner(t)
	definition := processProbeDefinition()
	definition.ID = "public_checkpoint_probe"
	definition.Hidden = false
	registerProcessDefinitionForTest(t, definition)

	proposal := scoutRouterProposalForToolID(definition.ID, "Run the checkpoint projection probe", source.Text)
	if proposal == nil {
		t.Fatal("process probe proposal unavailable")
	}
	proposal.IntentOutcome = string(conversationIntentApprovalRequired)
	proposal.EffectClass = "expanded_audience"
	proposal.Status = "accepted"
	var err error
	channel, err = app.commitScoutChatThreadMessages(user.Email, channel.ID, scoutChatMessageRecord{
		ID: "proposal-public-checkpoint", Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, channel, "proposal-public-checkpoint", *proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	work := response["agentThread"].(scoutAgentThread)
	rootID := response["answer"].(scoutChatMessageRecord).ID
	goalCheckpointProjectionPersistProbe = func(meetingMemoryEntry) error { return fmt.Errorf("injected projection loss") }
	t.Cleanup(func() { goalCheckpointProjectionPersistProbe = nil })
	app.runGoalThread(work.Artifact.ID)
	waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)

	rootCard := func() scoutChatMessageRecord {
		t.Helper()
		current, _, loadErr := app.scoutChatThreadByID(user.Email, channel.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		matching := make([]scoutChatMessageRecord, 0, 1)
		for _, message := range current.Messages {
			if message.Kind == "thread" && message.Thread != nil && message.Thread.ID == work.ID {
				matching = append(matching, message)
			}
		}
		if len(matching) != 1 || matching[0].ID != rootID || matching[0].ReplyTo != nil {
			t.Fatalf("checkpoint projected %d work cards instead of updating root %q: %+v", len(matching), rootID, matching)
		}
		return matching[0]
	}
	card := rootCard()
	if card.Thread.Checkpoint != nil {
		t.Fatalf("projection failpoint unexpectedly updated root: %+v", card.Thread)
	}
	restarted := newKanbanBoardApp()
	restarted.apiKey = "test-key"
	app = restarted
	kanbanApp = restarted
	app.reconcileGoalThreadsAtBoot()
	card = rootCard()
	deadline := time.Now().Add(3 * time.Second)
	for card.Thread.Checkpoint == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		card = rootCard()
	}
	if card.Thread.Status != codexJobStatusApprovalRequired || card.Thread.Checkpoint == nil ||
		card.Thread.Checkpoint.Question == "" || len(card.Thread.Checkpoint.Options) != 2 {
		t.Fatalf("root checkpoint projection=%+v", card.Thread)
	}
	checkpoint := card.Thread.Checkpoint
	for _, option := range checkpoint.Options {
		if !validGoalCheckpointChoiceID(checkpoint.ID, "goal-checkpoint-") ||
			!validGoalCheckpointChoiceID(option.ID, "checkpoint-option-") || option.Label == "" || option.Action == "" {
			t.Fatalf("checkpoint option is not server-bound/display-safe: checkpoint=%+v option=%+v", checkpoint, option)
		}
	}
	findOption := func(action string) scoutChatWorkCheckpointOptionRef {
		for _, option := range checkpoint.Options {
			if option.Action == action {
				return option
			}
		}
		t.Fatalf("checkpoint has no %s option: %+v", action, checkpoint.Options)
		return scoutChatWorkCheckpointOptionRef{}
	}
	hold, proceed := findOption(processCheckpointActionHold), findOption(processCheckpointActionProceed)
	body := func(optionID string) string {
		return fmt.Sprintf(`{"id":%q,"action":"approve","checkpointId":%q,"checkpointOptionId":%q}`, work.Artifact.ID, checkpoint.ID, optionID)
	}

	// The public card is visible to teammates, but its durable choice effect is
	// still an approval action. A non-admin cannot consume the opaque option.
	nonAdmin := postArtifactAction(t, loginAs(t, "tim@shareability.com", "B0NFIRE!"), body(hold.ID))
	if nonAdmin.Code != http.StatusForbidden {
		t.Fatalf("non-admin checkpoint choice status=%d body=%s", nonAdmin.Code, nonAdmin.Body.String())
	}
	// A valid-looking but unissued option id fails closed and leaves the same
	// goal parked with no resolution receipt.
	staleID := hold.ID[:len(hold.ID)-1] + map[bool]string{true: "0", false: "1"}[hold.ID[len(hold.ID)-1] != '0']
	adminCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	stale := postArtifactAction(t, adminCookies, body(staleID))
	if stale.Code != http.StatusBadRequest {
		t.Fatalf("stale checkpoint option status=%d body=%s", stale.Code, stale.Body.String())
	}
	staleCheckpointID := checkpoint.ID[:len(checkpoint.ID)-1] + map[bool]string{true: "0", false: "1"}[checkpoint.ID[len(checkpoint.ID)-1] != '0']
	staleCheckpoint := postArtifactAction(t, adminCookies, fmt.Sprintf(`{"id":%q,"action":"approve","checkpointId":%q,"checkpointOptionId":%q}`, work.Artifact.ID, staleCheckpointID, hold.ID))
	if staleCheckpoint.Code != http.StatusBadRequest {
		t.Fatalf("stale checkpoint status=%d body=%s", staleCheckpoint.Code, staleCheckpoint.Body.String())
	}
	parked := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	if parked.Checkpoint == nil || parked.Checkpoint.Held || len(parked.CheckpointReceipts) != 0 {
		t.Fatalf("denied choices mutated the goal: %+v", parked)
	}

	first := postArtifactAction(t, adminCookies, body(hold.ID))
	if first.Code != http.StatusAccepted {
		t.Fatalf("hold status=%d body=%s", first.Code, first.Body.String())
	}
	if len(*launched) != 1 {
		t.Fatalf("hold launched another job: %+v", *launched)
	}
	plan := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	if plan.Checkpoint == nil || !plan.Checkpoint.Held || len(plan.CheckpointReceipts) != 1 {
		t.Fatalf("hold did not commit one receipt on the exact goal: %+v", plan.Checkpoint)
	}

	retry := postArtifactAction(t, adminCookies, body(hold.ID))
	if retry.Code != http.StatusAccepted {
		t.Fatalf("hold replay status=%d body=%s", retry.Code, retry.Body.String())
	}
	var replayPayload map[string]any
	if err := json.Unmarshal(retry.Body.Bytes(), &replayPayload); err != nil || replayPayload["replayed"] != true {
		t.Fatalf("hold replay response=%s err=%v", retry.Body.String(), err)
	}
	plan = waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	if len(plan.CheckpointReceipts) != 1 || len(*launched) != 1 {
		t.Fatalf("duplicate retry created a second effect: receipts=%+v launches=%+v", plan.CheckpointReceipts, *launched)
	}

	resume := postArtifactAction(t, adminCookies, body(proceed.ID))
	if resume.Code != http.StatusAccepted {
		t.Fatalf("proceed status=%d body=%s", resume.Code, resume.Body.String())
	}
	plan = waitForGoalStage(t, app, work.Artifact.ID, goalStateVerified)
	if plan.GoalID != work.ID || len(plan.CheckpointReceipts) != 2 || plan.Checkpoint.Choice != proceed.Label {
		t.Fatalf("choice did not resume the exact same goal: goal=%q work=%q plan=%+v", plan.GoalID, work.ID, plan)
	}
	card = rootCard()
	if card.Thread.Status != codexJobStatusComplete || card.Thread.Checkpoint != nil {
		t.Fatalf("completed root retained checkpoint/running state: %+v", card.Thread)
	}
}

func postArtifactAction(t *testing.T, cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/artifacts/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactRunnerActionHandler(recorder, req)
	return recorder
}

func opaqueCheckpointFixture(t *testing.T, action string) (*kanbanBoardApp, scoutAgentThread, string, string) {
	t.Helper()
	app := newIsolatedKanbanBoardApp(t)
	work, checkpointID, optionID := opaqueCheckpointFixtureForApp(t, app, action)
	return app, work, checkpointID, optionID
}

func opaqueCheckpointFixtureForApp(t *testing.T, app *kanbanBoardApp, action string) (scoutAgentThread, string, string) {
	t.Helper()
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)
	processID := "process_probe"
	if action == processCheckpointActionRevise {
		processID = "checkpoint_recovery_revise_" + sha256Hex([]byte(t.Name()))[:12]
		registerReviseProbeForTest(t, processID)
	}
	work, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Checkpoint recovery " + action, CreatedBy: "aj@shareability.com", ToolTemplate: processID,
	})
	if err != nil {
		t.Fatal(err)
	}
	app.runGoalThread(work.Artifact.ID)
	plan := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	if plan.Checkpoint == nil {
		t.Fatal("checkpoint missing")
	}
	cpID := goalCheckpointID(work.Artifact.ID, plan.Checkpoint)
	for index, option := range plan.Checkpoint.Options {
		if option.action() == action {
			return work, cpID, goalCheckpointOptionID(cpID, option, index)
		}
	}
	t.Fatalf("checkpoint has no %s option: %+v", action, plan.Checkpoint.Options)
	return scoutAgentThread{}, "", ""
}

func checkpointReceiptFor(t *testing.T, app *kanbanBoardApp, artifactID, checkpointID, optionID string) goalCheckpointResolutionReceipt {
	t.Helper()
	artifact := mustArtifact(t, app, artifactID)
	plan, ok := decodeGoalPlan(artifact.Metadata["goalPlan"])
	if !ok {
		t.Fatal("goal plan missing")
	}
	for _, receipt := range plan.CheckpointReceipts {
		if receipt.CheckpointID == checkpointID && receipt.OptionID == optionID {
			return receipt
		}
	}
	t.Fatalf("resolution receipt missing: %+v", plan.CheckpointReceipts)
	return goalCheckpointResolutionReceipt{}
}

func TestOpaqueCheckpointOptionKeepsExactActionAndTargetWithRevisionNote(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	processID := "checkpoint_prefix_collision_" + sha256Hex([]byte(t.Name()))[:12]
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID: processID, Version: 1, Title: "Prefix collision probe", Authority: toolAuthorityWorkspaceWrite, Hidden: true,
		Stages: []ProcessStage{
			{ID: "w1", Title: "Draft", Role: processRoleWriter},
			{ID: "approval", Title: "Review", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"}, CheckpointSpec: &ProcessCheckpointSpec{
				Question: "What next?", Options: []ProcessCheckpointOption{
					{Label: "send"},
					{Label: "send back", Action: processCheckpointActionRevise, Target: "w1"},
				},
			}},
		},
	})
	work, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{Objective: "Prefix collision", CreatedBy: "aj@shareability.com", ToolTemplate: processID})
	if err != nil {
		t.Fatal(err)
	}
	app.runGoalThread(work.Artifact.ID)
	plan := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	checkpointID := goalCheckpointID(work.Artifact.ID, plan.Checkpoint)
	reviseID := goalCheckpointOptionID(checkpointID, plan.Checkpoint.Options[1], 1)
	replayed, bound, err := app.resumeApprovedGoalBound(work.Artifact.ID, "AJ", "tighten the ending", checkpointID, reviseID, nil)
	if err != nil || replayed || !bound {
		t.Fatalf("exact revise replayed=%v bound=%v err=%v", replayed, bound, err)
	}
	plan = waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	if len(*launched) != 2 || (*launched)[1].subtaskID != "w1" {
		t.Fatalf("opaque revise executed the wrong option/target: %+v", *launched)
	}
	receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, reviseID)
	if receipt.Action != processCheckpointActionRevise || receipt.Target != "w1" || receipt.Choice != "send back — tighten the ending" {
		t.Fatalf("receipt lost exact option authority: %+v", receipt)
	}
	replayed, bound, err = app.resumeApprovedGoalBound(work.Artifact.ID, "AJ", "different note", checkpointID, reviseID, nil)
	if err != nil || !replayed || !bound || len(*launched) != 2 {
		t.Fatalf("retry replayed=%v bound=%v err=%v launches=%+v", replayed, bound, err, *launched)
	}
	if replay := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, reviseID); replay.Choice != receipt.Choice {
		t.Fatalf("retry changed the first decision: before=%+v after=%+v", receipt, replay)
	}
}

func TestOpaqueCheckpointActionsRecoverWriteFailureAndLostAck(t *testing.T) {
	for _, action := range []string{processCheckpointActionProceed, processCheckpointActionHold, processCheckpointActionRevise} {
		t.Run(action+"/write_failure", func(t *testing.T) {
			app, work, checkpointID, optionID := opaqueCheckpointFixture(t, action)
			failed := false
			goalCheckpointTransitionPersistProbe = func(got string) error {
				if got == action && !failed {
					failed = true
					return fmt.Errorf("injected %s transition write failure", action)
				}
				return nil
			}
			t.Cleanup(func() { goalCheckpointTransitionPersistProbe = nil })
			if replayed, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, optionID); err == nil || replayed {
				t.Fatalf("write failure result replayed=%v err=%v", replayed, err)
			}
			if receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionClaimed {
				t.Fatalf("write failure receipt=%+v, want durable claim", receipt)
			}
			goalCheckpointTransitionPersistProbe = nil
			if replayed, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, optionID); err != nil || !replayed {
				t.Fatalf("claimed finalization replayed=%v err=%v", replayed, err)
			}
			if receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalized {
				t.Fatalf("final receipt=%+v", receipt)
			}
		})

		t.Run(action+"/lost_ack", func(t *testing.T) {
			app, work, checkpointID, optionID := opaqueCheckpointFixture(t, action)
			goalCheckpointResolutionAfterCommitProbe = func(got string) error {
				if got == action {
					return fmt.Errorf("injected %s lost acknowledgement", action)
				}
				return nil
			}
			t.Cleanup(func() { goalCheckpointResolutionAfterCommitProbe = nil })
			if replayed, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, optionID); err == nil || replayed {
				t.Fatalf("lost ack result replayed=%v err=%v", replayed, err)
			}
			goalCheckpointResolutionAfterCommitProbe = nil
			if replayed, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, optionID); err != nil || !replayed {
				t.Fatalf("lost ack replay=%v err=%v", replayed, err)
			}
			if receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalized {
				t.Fatalf("lost ack receipt=%+v", receipt)
			}
		})
	}
}

func TestCheckpointRejectsAlternateOptionWhileClaimOutstanding(t *testing.T) {
	app, work, checkpointID, holdID := opaqueCheckpointFixture(t, processCheckpointActionHold)
	plan := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	proceedID := ""
	for index, option := range plan.Checkpoint.Options {
		if option.action() == processCheckpointActionProceed {
			proceedID = goalCheckpointOptionID(checkpointID, option, index)
		}
	}
	if proceedID == "" {
		t.Fatal("proceed option missing")
	}
	goalCheckpointTransitionPersistProbe = func(action string) error {
		if action == processCheckpointActionHold {
			return fmt.Errorf("injected hold transition failure")
		}
		return nil
	}
	t.Cleanup(func() { goalCheckpointTransitionPersistProbe = nil })
	if _, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, holdID); err == nil {
		t.Fatal("hold transition failure was not surfaced")
	}
	if _, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, proceedID); err == nil || !strings.Contains(err.Error(), "different checkpoint choice") {
		t.Fatalf("alternate option err=%v", err)
	}
	parked := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	if len(parked.CheckpointReceipts) != 1 || parked.CheckpointReceipts[0].OptionID != holdID || parked.CheckpointReceipts[0].State != goalCheckpointResolutionClaimed {
		t.Fatalf("alternate option created another claim: %+v", parked.CheckpointReceipts)
	}
	goalCheckpointTransitionPersistProbe = nil
	goalCheckpointResolutionAfterEffectsProbe = func(string) error { return fmt.Errorf("injected finalizing crash") }
	t.Cleanup(func() { goalCheckpointResolutionAfterEffectsProbe = nil })
	if _, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, holdID); err == nil {
		t.Fatal("finalizing crash was not surfaced")
	}
	if receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, holdID); receipt.State != goalCheckpointResolutionFinalizing {
		t.Fatalf("receipt=%+v, want finalizing", receipt)
	}
	if _, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, proceedID); err == nil || !strings.Contains(err.Error(), "different checkpoint choice") {
		t.Fatalf("alternate finalizing option err=%v", err)
	}
}

func TestCheckpointBootRedrivesCommittedPreDriveTransition(t *testing.T) {
	app, work, checkpointID, optionID := opaqueCheckpointFixture(t, processCheckpointActionProceed)
	parked := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	alternateID := ""
	for index, option := range parked.Checkpoint.Options {
		if option.action() != processCheckpointActionProceed {
			alternateID = goalCheckpointOptionID(checkpointID, option, index)
			break
		}
	}
	if alternateID == "" {
		t.Fatal("alternate checkpoint option missing")
	}
	goalCheckpointAfterTransitionPersistProbe = func(string) error { return fmt.Errorf("injected crash before drive") }
	t.Cleanup(func() { goalCheckpointAfterTransitionPersistProbe = nil })
	if _, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, optionID); err == nil {
		t.Fatal("pre-drive crash was not surfaced")
	}
	crashed := waitForGoalStage(t, app, work.Artifact.ID, goalStateExecute)
	if len(crashed.CheckpointReceipts) != 1 || crashed.CheckpointReceipts[0].State != goalCheckpointResolutionCommitted ||
		!crashed.CheckpointReceipts[0].DriveNeeded || crashed.CheckpointReceipts[0].DriveCompletedAt != "" || crashed.CheckpointReceipts[0].EffectiveOutcome != processCheckpointActionProceed {
		t.Fatalf("pre-drive receipt=%+v", crashed.CheckpointReceipts)
	}
	if _, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, alternateID); err == nil || !strings.Contains(err.Error(), "different checkpoint choice") {
		t.Fatalf("alternate committed option err=%v", err)
	}
	if latest := waitForGoalStage(t, app, work.Artifact.ID, goalStateExecute); len(latest.CheckpointReceipts) != 1 {
		t.Fatalf("alternate committed option created another receipt: %+v", latest.CheckpointReceipts)
	}
	goalCheckpointAfterTransitionPersistProbe = nil
	restarted := newKanbanBoardApp()
	restarted.apiKey = "test-key"
	recoveryDone := make(chan string, 1)
	goalCheckpointResolutionRecoveryDoneProbe = func(id string) { recoveryDone <- id }
	t.Cleanup(func() { goalCheckpointResolutionRecoveryDoneProbe = nil })
	restarted.reconcileGoalThreadsAtBoot()
	select {
	case <-recoveryDone:
	case <-time.After(5 * time.Second):
		t.Fatal("pre-drive recovery did not finish")
	}
	waitForGoalStage(t, restarted, work.Artifact.ID, goalStateVerified)
	if receipt := checkpointReceiptFor(t, restarted, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalized || receipt.DriveCompletedAt == "" {
		t.Fatalf("recovered receipt=%+v", receipt)
	}
}

func TestCheckpointActionReauthorizesSnapshotInsideGoalLock(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	work, checkpointID, optionID := opaqueCheckpointFixtureForApp(t, app, processCheckpointActionHold)
	goalCheckpointActionAfterAuthorizationProbe = func() {
		artifact := mustArtifact(t, app, work.Artifact.ID)
		thread, _, err := app.scoutChatThreadByID("aj@shareability.com", artifact.Metadata["originId"])
		if err != nil {
			t.Fatalf("load checkpoint origin: %v", err)
		}
		thread.OwnerEmail = "tim@shareability.com"
		if err := app.saveScoutChatThread(thread); err != nil {
			t.Fatalf("revoke checkpoint origin: %v", err)
		}
	}
	t.Cleanup(func() { goalCheckpointActionAfterAuthorizationProbe = nil })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	body := fmt.Sprintf(`{"id":%q,"action":"approve","checkpointId":%q,"checkpointOptionId":%q}`, work.Artifact.ID, checkpointID, optionID)
	response := postArtifactAction(t, cookies, body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("revoked checkpoint status=%d body=%s", response.Code, response.Body.String())
	}
	artifact := mustArtifact(t, app, work.Artifact.ID)
	plan, ok := decodeGoalPlan(artifact.Metadata["goalPlan"])
	if !ok || len(plan.CheckpointReceipts) != 0 || plan.Checkpoint == nil || plan.Checkpoint.Held {
		t.Fatalf("revoked action created an effect: %+v", plan)
	}
}

func TestCheckpointHTTPRetryFinalizesApprovalAfterLostAck(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	work, checkpointID, optionID := opaqueCheckpointFixtureForApp(t, app, processCheckpointActionProceed)
	goalCheckpointResolutionAfterCommitProbe = func(action string) error {
		if action == processCheckpointActionProceed {
			return fmt.Errorf("injected crash after transition commit")
		}
		return nil
	}
	t.Cleanup(func() { goalCheckpointResolutionAfterCommitProbe = nil })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	body := fmt.Sprintf(`{"id":%q,"action":"approve","checkpointId":%q,"checkpointOptionId":%q}`, work.Artifact.ID, checkpointID, optionID)
	first := postArtifactAction(t, cookies, body)
	if first.Code != http.StatusBadRequest {
		t.Fatalf("lost-ack status=%d body=%s", first.Code, first.Body.String())
	}
	crashed := mustArtifact(t, app, work.Artifact.ID)
	if crashed.Metadata[artifactHumanApprovedAtKey] != "" || crashed.Metadata[checkpointApprovalOutcomeEffectMetadataKey] != "" {
		t.Fatalf("post-transition effects ran before finalizer: %+v", crashed.Metadata)
	}
	if receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionCommitted {
		t.Fatalf("crash receipt=%+v, want committed", receipt)
	}
	goalCheckpointResolutionAfterCommitProbe = nil
	goalCheckpointResolutionAfterEffectsProbe = func(action string) error { return fmt.Errorf("injected crash after %s effects", action) }
	t.Cleanup(func() { goalCheckpointResolutionAfterEffectsProbe = nil })
	second := postArtifactAction(t, cookies, body)
	if second.Code != http.StatusBadRequest {
		t.Fatalf("post-effects lost-ack status=%d body=%s", second.Code, second.Body.String())
	}
	if receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalizing {
		t.Fatalf("post-effects receipt=%+v, want finalizing", receipt)
	}
	goalCheckpointResolutionAfterEffectsProbe = nil
	retry := postArtifactAction(t, cookies, body)
	if retry.Code != http.StatusAccepted {
		t.Fatalf("final retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(retry.Body.Bytes(), &payload); err != nil || payload["replayed"] != true {
		t.Fatalf("retry payload=%s err=%v", retry.Body.String(), err)
	}
	final := mustArtifact(t, app, work.Artifact.ID)
	if final.Metadata[artifactHumanApprovedAtKey] == "" || final.Metadata[checkpointApprovalOutcomeEffectMetadataKey] == "" {
		t.Fatalf("approval finalizer did not repair stamp/fanout marker: %+v", final.Metadata)
	}
	if receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalized {
		t.Fatalf("final receipt=%+v", receipt)
	}
	notifications := 0
	for _, record := range app.notifications {
		if record.ArtifactID == work.Artifact.ID && strings.HasPrefix(record.ID, "notification-approval-outcome-") {
			notifications++
		}
	}
	signals := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		if entry.Metadata["artifactId"] == work.Artifact.ID && entry.Metadata["event"] == signalEventProposalApproved {
			signals++
		}
	}
	if notifications != 0 || signals != 1 {
		t.Fatalf("idempotent fanout notifications=%d signals=%d", notifications, signals)
	}
}

func TestDirectCheckpointHTTPRetryFinalizesApprovalAfterLostAck(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	work, checkpointID, optionID := opaqueCheckpointFixtureForApp(t, app, processCheckpointActionProceed)
	parked := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	choice := ""
	for index, option := range parked.Checkpoint.Options {
		if goalCheckpointOptionID(checkpointID, option, index) == optionID {
			choice = option.Label
			break
		}
	}
	if choice == "" {
		t.Fatal("direct proceed label missing")
	}
	goalCheckpointResolutionAfterCommitProbe = func(action string) error {
		if action == processCheckpointActionProceed {
			return fmt.Errorf("injected direct crash after transition commit")
		}
		return nil
	}
	t.Cleanup(func() { goalCheckpointResolutionAfterCommitProbe = nil })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	body := fmt.Sprintf(`{"id":%q,"action":"approve","choice":%q}`, work.Artifact.ID, choice)
	first := postArtifactAction(t, cookies, body)
	if first.Code != http.StatusBadRequest {
		t.Fatalf("direct lost-ack status=%d body=%s", first.Code, first.Body.String())
	}
	receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID)
	if receipt.State != goalCheckpointResolutionCommitted || !receipt.HumanApproval {
		t.Fatalf("direct committed receipt=%+v", receipt)
	}
	crashed := mustArtifact(t, app, work.Artifact.ID)
	if crashed.Metadata[artifactHumanApprovedAtKey] != "" || crashed.Metadata[checkpointApprovalOutcomeEffectMetadataKey] != "" {
		t.Fatalf("direct post-transition effects ran before finalizer: %+v", crashed.Metadata)
	}

	goalCheckpointResolutionAfterCommitProbe = nil
	goalCheckpointResolutionAfterEffectsProbe = func(action string) error { return fmt.Errorf("injected direct crash after %s effects", action) }
	t.Cleanup(func() { goalCheckpointResolutionAfterEffectsProbe = nil })
	second := postArtifactAction(t, cookies, body)
	if second.Code != http.StatusBadRequest {
		t.Fatalf("direct post-effects lost-ack status=%d body=%s", second.Code, second.Body.String())
	}
	if receipt = checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalizing {
		t.Fatalf("direct post-effects receipt=%+v", receipt)
	}

	goalCheckpointResolutionAfterEffectsProbe = nil
	retry := postArtifactAction(t, cookies, body)
	if retry.Code != http.StatusAccepted {
		t.Fatalf("direct final retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(retry.Body.Bytes(), &payload); err != nil || payload["replayed"] != true {
		t.Fatalf("direct retry payload=%s err=%v", retry.Body.String(), err)
	}
	final := mustArtifact(t, app, work.Artifact.ID)
	if final.Metadata[artifactHumanApprovedAtKey] == "" || final.Metadata[checkpointApprovalOutcomeEffectMetadataKey] == "" {
		t.Fatalf("direct approval finalizer did not repair stamp/fanout: %+v", final.Metadata)
	}
	if receipt = checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalized || !receipt.HumanApproval {
		t.Fatalf("direct final receipt=%+v", receipt)
	}
	signals := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		if entry.Metadata["artifactId"] == work.Artifact.ID && entry.Metadata["event"] == signalEventProposalApproved {
			signals++
		}
	}
	if signals != 1 {
		t.Fatalf("direct approval fanout signals=%d, want exactly 1", signals)
	}
}

func TestOptionlessDirectCheckpointClaimRecoversAfterRestart(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	work, _, _ := opaqueCheckpointFixtureForApp(t, app, processCheckpointActionProceed)
	artifact := mustArtifact(t, app, work.Artifact.ID)
	plan, ok := decodeGoalPlan(artifact.Metadata["goalPlan"])
	if !ok || plan.Checkpoint == nil {
		t.Fatal("checkpoint plan missing")
	}
	plan.Checkpoint.Options = nil // persisted legacy optionless checkpoint
	plan.Checkpoint.ID = goalCheckpointID(work.Artifact.ID, plan.Checkpoint)
	checkpointRaw, _ := json.Marshal(plan.Checkpoint)
	planRaw, _ := json.Marshal(plan)
	if _, _, err := app.updateOSArtifactWithMetadata(work.Artifact.ID, "", artifact.Text, "test", map[string]string{
		"goalPlan": string(planRaw), "checkpoint": string(checkpointRaw),
	}); err != nil {
		t.Fatal(err)
	}

	const choice = "Founder says ship with confidence"
	checkpointID := plan.Checkpoint.ID
	optionID := goalCheckpointFreeformOptionID(checkpointID, choice)
	if optionID != goalCheckpointFreeformOptionID(checkpointID, "  founder  SAYS ship with CONFIDENCE  ") {
		t.Fatal("canonical free-form request identity is unstable")
	}
	goalCheckpointResolutionAfterClaimProbe = func(string) error { return fmt.Errorf("injected optionless crash after claim") }
	t.Cleanup(func() { goalCheckpointResolutionAfterClaimProbe = nil })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	body := fmt.Sprintf(`{"id":%q,"action":"approve","choice":%q}`, work.Artifact.ID, choice)
	first := postArtifactAction(t, cookies, body)
	if first.Code != http.StatusBadRequest {
		t.Fatalf("optionless claim crash status=%d body=%s", first.Code, first.Body.String())
	}
	receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID)
	if receipt.State != goalCheckpointResolutionClaimed || !receipt.HumanApproval || receipt.Choice != choice {
		t.Fatalf("optionless claim=%+v", receipt)
	}
	alternate := postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","choice":"ship a different way"}`, work.Artifact.ID))
	if alternate.Code != http.StatusBadRequest || !strings.Contains(alternate.Body.String(), "different checkpoint choice") {
		t.Fatalf("optionless alternate status=%d body=%s", alternate.Code, alternate.Body.String())
	}
	latest := mustArtifact(t, app, work.Artifact.ID)
	latestPlan, _ := decodeGoalPlan(latest.Metadata["goalPlan"])
	if len(latestPlan.CheckpointReceipts) != 1 {
		t.Fatalf("optionless alternate minted another receipt: %+v", latestPlan.CheckpointReceipts)
	}

	goalCheckpointResolutionAfterClaimProbe = nil
	restarted := newKanbanBoardApp()
	restarted.apiKey = "test-key"
	kanbanApp = restarted
	recoveryDone := make(chan string, 1)
	goalCheckpointResolutionRecoveryDoneProbe = func(id string) { recoveryDone <- id }
	t.Cleanup(func() { goalCheckpointResolutionRecoveryDoneProbe = nil })
	restarted.reconcileGoalThreadsAtBoot()
	select {
	case got := <-recoveryDone:
		if got != work.Artifact.ID {
			t.Fatalf("optionless recovered goal=%q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("optionless checkpoint recovery did not finish")
	}
	waitForGoalStage(t, restarted, work.Artifact.ID, goalStateVerified)
	receipt = checkpointReceiptFor(t, restarted, work.Artifact.ID, checkpointID, optionID)
	if receipt.State != goalCheckpointResolutionFinalized || !receipt.HumanApproval || receipt.DriveCompletedAt == "" {
		t.Fatalf("optionless recovered receipt=%+v", receipt)
	}
	final := mustArtifact(t, restarted, work.Artifact.ID)
	if final.Metadata[artifactHumanApprovedAtKey] == "" || final.Metadata[checkpointApprovalOutcomeEffectMetadataKey] == "" {
		t.Fatalf("optionless recovery missed approval effects: %+v", final.Metadata)
	}
}

func TestBlankDirectCheckpointApprovalRecoversCommittedReceiptAfterRestart(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	work, checkpointID, proceedOptionID := opaqueCheckpointFixtureForApp(t, app, processCheckpointActionProceed)
	goalCheckpointResolutionAfterCommitProbe = func(action string) error {
		if action == processCheckpointActionProceed {
			return fmt.Errorf("injected blank approval crash after commit")
		}
		return nil
	}
	t.Cleanup(func() { goalCheckpointResolutionAfterCommitProbe = nil })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	body := fmt.Sprintf(`{"id":%q,"action":"approve","choice":""}`, work.Artifact.ID)
	first := postArtifactAction(t, cookies, body)
	if first.Code != http.StatusBadRequest {
		t.Fatalf("blank direct lost-ack status=%d body=%s", first.Code, first.Body.String())
	}
	receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, proceedOptionID)
	if receipt.State != goalCheckpointResolutionCommitted || !receipt.HumanApproval || receipt.Choice != "" || receipt.Action != processCheckpointActionProceed {
		t.Fatalf("blank direct committed receipt=%+v", receipt)
	}
	crashed := mustArtifact(t, app, work.Artifact.ID)
	if crashed.Metadata[artifactHumanApprovedAtKey] != "" || crashed.Metadata[checkpointApprovalOutcomeEffectMetadataKey] != "" {
		t.Fatalf("blank approval effects ran before restart finalizer: %+v", crashed.Metadata)
	}

	goalCheckpointResolutionAfterCommitProbe = nil
	restarted := newKanbanBoardApp()
	restarted.apiKey = "test-key"
	kanbanApp = restarted
	recoveryDone := make(chan string, 1)
	goalCheckpointResolutionRecoveryDoneProbe = func(id string) { recoveryDone <- id }
	t.Cleanup(func() { goalCheckpointResolutionRecoveryDoneProbe = nil })
	restarted.reconcileGoalThreadsAtBoot()
	select {
	case got := <-recoveryDone:
		if got != work.Artifact.ID {
			t.Fatalf("blank direct recovered goal=%q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blank direct checkpoint recovery did not finish")
	}
	waitForGoalStage(t, restarted, work.Artifact.ID, goalStateVerified)
	receipt = checkpointReceiptFor(t, restarted, work.Artifact.ID, checkpointID, proceedOptionID)
	if receipt.State != goalCheckpointResolutionFinalized || !receipt.HumanApproval {
		t.Fatalf("blank direct recovered receipt=%+v", receipt)
	}
	final := mustArtifact(t, restarted, work.Artifact.ID)
	if final.Metadata[artifactHumanApprovedAtKey] == "" || final.Metadata[checkpointApprovalOutcomeEffectMetadataKey] == "" {
		t.Fatalf("blank direct recovery missed approval effects: %+v", final.Metadata)
	}
}

func TestBlankDirectCheckpointApprovalRejectsAmbiguousProceedDefault(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	work, _, _ := opaqueCheckpointFixtureForApp(t, app, processCheckpointActionProceed)
	artifact := mustArtifact(t, app, work.Artifact.ID)
	plan, ok := decodeGoalPlan(artifact.Metadata["goalPlan"])
	if !ok || plan.Checkpoint == nil || len(plan.Checkpoint.Options) < 2 {
		t.Fatal("checkpoint options missing")
	}
	plan.Checkpoint.Options[1].Action = processCheckpointActionProceed
	checkpointRaw, _ := json.Marshal(plan.Checkpoint)
	planRaw, _ := json.Marshal(plan)
	if _, _, err := app.updateOSArtifactWithMetadata(work.Artifact.ID, "", artifact.Text, "test", map[string]string{
		"goalPlan": string(planRaw), "checkpoint": string(checkpointRaw),
	}); err != nil {
		t.Fatal(err)
	}
	response := postArtifactAction(t, loginAs(t, "aj@shareability.com", "B0NFIRE!"), fmt.Sprintf(`{"id":%q,"action":"approve","choice":""}`, work.Artifact.ID))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "multiple proceed options") {
		t.Fatalf("ambiguous blank approval status=%d body=%s", response.Code, response.Body.String())
	}
	latest := mustArtifact(t, app, work.Artifact.ID)
	latestPlan, _ := decodeGoalPlan(latest.Metadata["goalPlan"])
	if latestPlan.State != goalStateApproval || len(latestPlan.CheckpointReceipts) != 0 {
		t.Fatalf("ambiguous blank approval created an effect: %+v", latestPlan)
	}
}

func TestBlankDirectCheckpointApprovalCannotPoisonHeldCheckpoint(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	work, checkpointID, proceedOptionID := opaqueCheckpointFixtureForApp(t, app, processCheckpointActionProceed)
	artifact := mustArtifact(t, app, work.Artifact.ID)
	plan, ok := decodeGoalPlan(artifact.Metadata["goalPlan"])
	if !ok || plan.Checkpoint == nil {
		t.Fatal("checkpoint plan missing")
	}
	plan.Checkpoint.Held = true
	plan.Checkpoint.HeldBy = "AJ"
	plan.Checkpoint.HeldAt = time.Now().UTC().Format(time.RFC3339Nano)
	checkpointRaw, _ := json.Marshal(plan.Checkpoint)
	planRaw, _ := json.Marshal(plan)
	if _, _, err := app.updateOSArtifactWithMetadata(work.Artifact.ID, "", artifact.Text, "test", map[string]string{
		"goalPlan": string(planRaw), "checkpoint": string(checkpointRaw),
	}); err != nil {
		t.Fatal(err)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	blank := postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","choice":""}`, work.Artifact.ID))
	if blank.Code != http.StatusBadRequest || !strings.Contains(blank.Body.String(), "explicit proceed choice") {
		t.Fatalf("blank held approval status=%d body=%s", blank.Code, blank.Body.String())
	}
	parked := mustArtifact(t, app, work.Artifact.ID)
	parkedPlan, _ := decodeGoalPlan(parked.Metadata["goalPlan"])
	if parkedPlan.State != goalStateApproval || parkedPlan.Checkpoint == nil || !parkedPlan.Checkpoint.Held || len(parkedPlan.CheckpointReceipts) != 0 {
		t.Fatalf("blank held approval poisoned checkpoint: %+v", parkedPlan)
	}

	explicit := postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","choice":"ship"}`, work.Artifact.ID))
	if explicit.Code != http.StatusAccepted {
		t.Fatalf("explicit held proceed status=%d body=%s", explicit.Code, explicit.Body.String())
	}
	waitForGoalStage(t, app, work.Artifact.ID, goalStateVerified)
	receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, proceedOptionID)
	if receipt.State != goalCheckpointResolutionFinalized || receipt.Choice != "ship" || !receipt.HumanApproval {
		t.Fatalf("explicit held proceed receipt=%+v", receipt)
	}
}

func TestApprovalOutcomeOnceUsesDeterministicNotificationAndSignal(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "Approval outcome", "done", "AJ", map[string]string{
		"requestedBy": "tim@shareability.com", "originKind": "channel", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	effectID := "checkpoint-finalization-0123456789abcdef01234567"
	for attempt := 0; attempt < 2; attempt++ {
		if err := app.recordApprovalOutcomeOnce(artifact, "approve", "", "AJ", effectID); err != nil {
			t.Fatal(err)
		}
	}
	notifications, signals := 0, 0
	for _, record := range app.notifications {
		if record.ArtifactID == artifact.ID && strings.HasPrefix(record.ID, "notification-approval-outcome-") {
			notifications++
		}
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		if entry.Metadata["artifactId"] == artifact.ID && strings.HasPrefix(entry.ID, "signal-approval-outcome-") {
			signals++
		}
	}
	if notifications != 1 || signals != 1 {
		t.Fatalf("deterministic outcome effects notifications=%d signals=%d", notifications, signals)
	}
}

func TestCheckpointClaimFinalizesAfterRestart(t *testing.T) {
	app, work, checkpointID, optionID := opaqueCheckpointFixture(t, processCheckpointActionProceed)
	goalCheckpointResolutionAfterClaimProbe = func(string) error { return fmt.Errorf("injected crash after durable claim") }
	t.Cleanup(func() { goalCheckpointResolutionAfterClaimProbe = nil })
	if replayed, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, optionID); err == nil || replayed {
		t.Fatalf("claim crash replayed=%v err=%v", replayed, err)
	}
	if receipt := checkpointReceiptFor(t, app, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionClaimed {
		t.Fatalf("claim was not durable: %+v", receipt)
	}
	goalCheckpointResolutionAfterClaimProbe = nil
	restarted := newKanbanBoardApp()
	restarted.apiKey = "test-key"
	recoveryDone := make(chan string, 1)
	goalCheckpointResolutionRecoveryDoneProbe = func(parentID string) { recoveryDone <- parentID }
	t.Cleanup(func() { goalCheckpointResolutionRecoveryDoneProbe = nil })
	restarted.reconcileGoalThreadsAtBoot()
	waitForGoalStage(t, restarted, work.Artifact.ID, goalStateVerified)
	select {
	case recoveredID := <-recoveryDone:
		if recoveredID != work.Artifact.ID {
			t.Fatalf("recovered goal=%q, want %q", recoveredID, work.Artifact.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("checkpoint recovery goroutine did not finish")
	}
	if receipt := checkpointReceiptFor(t, restarted, work.Artifact.ID, checkpointID, optionID); receipt.State != goalCheckpointResolutionFinalized {
		t.Fatalf("restart did not finalize claim: %+v", receipt)
	}
}

func TestHeldCheckpointRejectsNonProceedBeforeClaim(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)
	processID := "checkpoint_held_bound_" + sha256Hex([]byte(t.Name()))[:12]
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID: processID, Version: 1, Title: "Held bound probe", Authority: toolAuthorityWorkspaceWrite, Hidden: true,
		Stages: []ProcessStage{
			{ID: "w1", Title: "Draft", Role: processRoleWriter},
			{ID: "approval", Title: "Approve", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"}, CheckpointSpec: &ProcessCheckpointSpec{
				Question: "What next?", Options: []ProcessCheckpointOption{
					{Label: "proceed"},
					{Label: "hold", Action: processCheckpointActionHold},
					{Label: "revise", Action: processCheckpointActionRevise, Target: "w1"},
				},
			}},
		},
	})
	work, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{Objective: "Held claim probe", CreatedBy: "aj@shareability.com", ToolTemplate: processID})
	if err != nil {
		t.Fatal(err)
	}
	app.runGoalThread(work.Artifact.ID)
	plan := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	checkpointID := goalCheckpointID(work.Artifact.ID, plan.Checkpoint)
	optionID := func(action string) string {
		for index, option := range plan.Checkpoint.Options {
			if option.action() == action {
				return goalCheckpointOptionID(checkpointID, option, index)
			}
		}
		t.Fatalf("missing %s option: %+v", action, plan.Checkpoint.Options)
		return ""
	}
	holdID, reviseID := optionID(processCheckpointActionHold), optionID(processCheckpointActionRevise)
	if replayed, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, holdID); err != nil || replayed {
		t.Fatalf("hold replayed=%v err=%v", replayed, err)
	}
	if replayed, err := app.resumeApprovedGoalWithCheckpointOption(work.Artifact.ID, "AJ", checkpointID, reviseID); err == nil || replayed {
		t.Fatalf("held revise replayed=%v err=%v, want refusal", replayed, err)
	}
	parked := waitForGoalStage(t, app, work.Artifact.ID, goalStateApproval)
	if parked.Checkpoint == nil || !parked.Checkpoint.Held || len(parked.CheckpointReceipts) != 1 {
		t.Fatalf("held refusal created a resolution claim: %+v", parked)
	}
	for _, receipt := range parked.CheckpointReceipts {
		if receipt.OptionID == reviseID {
			t.Fatalf("held revise persisted an impossible claim: %+v", receipt)
		}
	}
}

// A hold-action choice posted through POST /artifacts/action keeps the goal
// PARKED with Held=true — tapping "hold the package" actually holds, and a
// hold is never recorded as a human sign-off (no share-unlocking approval
// stamp).
func TestArtifactActionHTTPHoldChoiceKeepsGoalParked(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)

	thread, err := launchConversationOwnedGoalForTest(t, kanbanApp, goalLaunchSpec{
		Objective:    "Probe the HTTP hold door",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	kanbanApp.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	recorder := postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","choice":"hold"}`, thread.Artifact.ID))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("hold choice status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}

	artifact := mustArtifact(t, kanbanApp, thread.Artifact.ID)
	if artifact.Metadata["currentStage"] != goalStateApproval || artifact.Metadata["reviewGate"] != "approval_required" {
		t.Fatalf("hold choice un-parked the goal: %v", artifact.Metadata)
	}
	plan, ok := decodeGoalPlan(artifact.Metadata["goalPlan"])
	if !ok {
		t.Fatal("goal plan missing after the HTTP hold")
	}
	if plan.Checkpoint == nil || !plan.Checkpoint.Held || plan.Checkpoint.ResolvedAt != "" {
		t.Fatalf("HTTP hold choice did not hold the checkpoint: %+v", plan.Checkpoint)
	}
	if artifact.Metadata[artifactHumanApprovedAtKey] != "" || artifact.Metadata[artifactHumanApprovedByKey] != "" {
		t.Fatalf("a hold must not stamp the durable human-approval record: %v", artifact.Metadata)
	}

	// The subsequent proceed-action choice, through the same HTTP door, is the
	// only way forward.
	recorder = postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","choice":"ship"}`, thread.Artifact.ID))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("proceed after hold status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}
	plan = waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateVerified)
	if plan.Checkpoint == nil || plan.Checkpoint.Choice != "ship" || plan.Checkpoint.ResolvedAt == "" {
		t.Fatalf("proceed choice did not resolve the held checkpoint: %+v", plan.Checkpoint)
	}
}

// A revise-action choice posted through POST /artifacts/action re-queues the
// option's target stage with the choice text as revision notes and re-parks
// the checkpoint — the founder's send-back notes reach the pipeline.
func TestArtifactActionHTTPReviseChoiceRequeuesTarget(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	registerReviseProbeForTest(t, "process_http_revise_probe")

	thread, err := launchConversationOwnedGoalForTest(t, kanbanApp, goalLaunchSpec{
		Objective:    "Probe the HTTP revise door",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_http_revise_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	kanbanApp.runGoalThread(thread.Artifact.ID)
	parked := waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)
	checkpointID := goalCheckpointID(thread.Artifact.ID, parked.Checkpoint)
	optionID := ""
	for index, option := range parked.Checkpoint.Options {
		if option.action() == processCheckpointActionRevise {
			optionID = goalCheckpointOptionID(checkpointID, option, index)
			break
		}
	}
	if optionID == "" {
		t.Fatal("revise checkpoint option missing")
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	const revisionNote = "Keep the opening. Rebuild every incomplete rendered slide."
	recorder := postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","checkpointId":%q,"checkpointOptionId":%q,"checkpointNote":%q}`, thread.Artifact.ID, checkpointID, optionID, revisionNote))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("revise choice status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}

	// The target re-queued (a second w1 launch carrying the notes) and the
	// checkpoint RE-PARKED, unresolved — the send-back was not a proceed.
	plan := waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)
	if plan.Checkpoint == nil || plan.Checkpoint.StageID != "pass" || plan.Checkpoint.ResolvedAt != "" {
		t.Fatalf("checkpoint did not re-park after the HTTP send-back: %+v", plan.Checkpoint)
	}
	if len(*launched) != 2 || (*launched)[1].subtaskID != "w1" {
		t.Fatalf("launched children=%+v, want the initial w1 + one send-back redo", *launched)
	}
	if !strings.Contains((*launched)[1].query, revisionNote) {
		t.Fatalf("redo query does not carry the HTTP choice notes:\n%s", (*launched)[1].query)
	}

	// A send-back is NOT a sign-off: no durable human-approval stamp, no
	// "approved · sent" fan-out — the founder asked for changes.
	updated, _ := kanbanApp.osArtifactByID(thread.Artifact.ID)
	if updated.Metadata["humanApprovedBy"] != "" || updated.Metadata["humanApprovedAt"] != "" {
		t.Fatalf("send-back stamped human approval: %v", updated.Metadata)
	}
	if plan.Checkpoint.LastAction != processCheckpointActionRevise {
		t.Fatalf("checkpoint lastAction=%q, want revise", plan.Checkpoint.LastAction)
	}
}
