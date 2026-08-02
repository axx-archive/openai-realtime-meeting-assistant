package main

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// This corpus is deliberately deterministic and provider-free. It proves the
// E8 control-plane lifecycle at the published sample size; it makes no claim
// about live model quality, voice, latency, or cost, which remain E10 gates.
func TestSTRIDEE8TwoHundredWorkforceLifecyclesAreIdempotentAndRevocable(t *testing.T) {
	base := time.Date(2026, 7, 31, 19, 0, 0, 0, time.UTC)
	for index := 0; index < 200; index++ {
		runtime := NewSTRIDEWorkforceRuntime()
		now := base.Add(time.Duration(index) * time.Second)
		request := strideWorkforceRequestForTest()
		request.AgentID = fmt.Sprintf("agent_e8_%03d", index)
		request.IdempotencyKey = fmt.Sprintf("create_e8_%03d", index)
		request.Template.TemplateID = fmt.Sprintf("template_e8_%03d", index)

		seat, created, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), request, now)
		if err != nil {
			t.Fatalf("case %d create: %v", index, err)
		}
		replayedSeat, replayedCreate, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), request, now.Add(time.Minute))
		if err != nil || replayedSeat.ID != seat.ID || replayedCreate.Digest != created.Digest {
			t.Fatalf("case %d create replay: seat=%#v receipt=%#v err=%v", index, replayedSeat, replayedCreate, err)
		}

		transitions := []struct {
			name string
			run  func(string) (STRIDEWorkforceReceipt, error)
		}{
			{"trial", func(key string) (STRIDEWorkforceReceipt, error) {
				return runtime.Trial(strideWorkforceAdmin(), seat.ID, key, now)
			}},
			{"hire", func(key string) (STRIDEWorkforceReceipt, error) {
				return runtime.Hire(strideWorkforceAdmin(), seat.ID, key, now)
			}},
		}
		for _, transition := range transitions {
			key := fmt.Sprintf("%s_e8_%03d", transition.name, index)
			first, runErr := transition.run(key)
			second, replayErr := transition.run(key)
			if runErr != nil || replayErr != nil || first.Digest != second.Digest {
				t.Fatalf("case %d %s replay: first=%#v second=%#v errors=%v/%v", index, transition.name, first, second, runErr, replayErr)
			}
		}
		for _, stage := range []string{"identity", "capability", "profile", "route"} {
			key := fmt.Sprintf("activate_%s_e8_%03d", stage, index)
			first, runErr := runtime.Activate(strideWorkforceAdmin(), seat.ID, key, stage, now)
			second, replayErr := runtime.Activate(strideWorkforceAdmin(), seat.ID, key, stage, now.Add(time.Minute))
			if runErr != nil || replayErr != nil || first.Digest != second.Digest {
				t.Fatalf("case %d activate %s replay: errors=%v/%v", index, stage, runErr, replayErr)
			}
		}

		proposal := strideE8UpdateProposal(seat, index, now)
		review := STRIDEUpdateReview{Proposal: proposal, PersonalityDiffDigest: temporalDigest("personality:" + seat.ID), ModelDiffDigest: temporalDigest("model:" + seat.ID), CostDiffDigest: temporalDigest("cost:" + seat.ID), Status: "pending"}
		if err := runtime.ProposeUpdate(strideWorkforceAdmin(), review); err != nil {
			t.Fatalf("case %d propose update: %v", index, err)
		}
		if err := runtime.ApproveUpdate(strideWorkforceAdmin(), proposal.Header.ID); err != nil {
			t.Fatalf("case %d approve update: %v", index, err)
		}
		if err := runtime.ApplyUpdateCanary(strideWorkforceAdmin(), proposal.Header.ID, "profile"); err != nil {
			t.Fatalf("case %d profile canary: %v", index, err)
		}
		if err := runtime.ApplyUpdateCanary(strideWorkforceAdmin(), proposal.Header.ID, "route"); err != nil {
			t.Fatalf("case %d route canary: %v", index, err)
		}
		if err := runtime.RollbackUpdate(strideWorkforceAdmin(), proposal.Header.ID); err != nil {
			t.Fatalf("case %d rollback update: %v", index, err)
		}

		pauseKey := fmt.Sprintf("pause_e8_%03d", index)
		firstPause, err := runtime.Pause(strideWorkforceAdmin(), seat.ID, pauseKey, now)
		secondPause, replayErr := runtime.Pause(strideWorkforceAdmin(), seat.ID, pauseKey, now.Add(time.Minute))
		if err != nil || replayErr != nil || firstPause.Digest != secondPause.Digest {
			t.Fatalf("case %d pause replay errors=%v/%v", index, err, replayErr)
		}
		offboardKey := fmt.Sprintf("offboard_e8_%03d", index)
		firstOffboard, err := runtime.Offboard(strideWorkforceAdmin(), seat.ID, offboardKey, now)
		secondOffboard, replayErr := runtime.Offboard(strideWorkforceAdmin(), seat.ID, offboardKey, now.Add(time.Minute))
		if err != nil || replayErr != nil || firstOffboard.Digest != secondOffboard.Digest {
			t.Fatalf("case %d offboard replay errors=%v/%v", index, err, replayErr)
		}
		exported, firstExport, err := runtime.Export(strideWorkforceAdmin(), seat.ID, fmt.Sprintf("export_e8_%03d", index), now)
		if err != nil || exported.ID != seat.ID || exported.Status != "offboarded" || !exported.AccessRevoked || exported.OffboardedAt == nil {
			t.Fatalf("case %d export=%#v receipt=%#v err=%v", index, exported, firstExport, err)
		}
		if grant, grantErr := runtime.IssueRuntimeGrant(strideWorkforceAdmin(), seat.ID, now); !errors.Is(grantErr, ErrSTRIDEWorkforceAuthority) || grant.PrincipalID != "" {
			t.Fatalf("case %d offboarded grant=%#v err=%v", index, grant, grantErr)
		}
	}
}

func TestSTRIDEE8TwoHundredProductMarketplaceLifecyclesAreInspectableOptInAndRevocable(t *testing.T) {
	base := time.Date(2026, 7, 31, 19, 30, 0, 0, time.UTC)
	candidateIDs := []string{"insights-analyst", "mary-marketing", "rowan-research", "jules-design", "kit-builder"}
	for index := 0; index < 200; index++ {
		state := NewSTRIDEProductState()
		now := base.Add(time.Duration(index) * time.Second)
		candidateID := candidateIDs[index%len(candidateIDs)]
		candidate, found := state.candidate(candidateID)
		if !found || candidate.LiveAvailable || !candidate.ProviderExecutionFenced || candidate.Availability != "internal_preview" || !isHexDigest(candidate.PackageDigest) {
			t.Fatalf("case %d listing=%#v found=%v", index, candidate, found)
		}

		owner := fmt.Sprintf("member_e8_%03d", index)
		trial, err := state.beginTrial(candidateID, owner, now)
		if err != nil {
			t.Fatalf("case %d trial: %v", index, err)
		}
		trialReplay, err := state.beginTrial(candidateID, owner, now.Add(time.Minute))
		if err != nil || workDigest(trialReplay) != workDigest(trial) {
			t.Fatalf("case %d trial replay=%#v err=%v", index, trialReplay, err)
		}

		directThreadID := fmt.Sprintf("agent_thread_e8_%03d", index)
		hired, err := state.mutateAgent(trial.ID, trial.Revision, func(agent *STRIDEProductTeamAgent) error {
			agent.Status = "hired_fenced"
			agent.DirectThreadID = directThreadID
			agent.AccessRevoked = false
			agent.Lifecycle = append(agent.Lifecycle, "human_approved_hire", "provider_runtime_remains_fenced")
			return nil
		}, now.Add(time.Second))
		if err != nil {
			t.Fatalf("case %d hire: %v", index, err)
		}
		if replay, ok := state.exactAgentReplay(trial.ID, trial.Revision, func(agent STRIDEProductTeamAgent) bool {
			return agent.Status == "hired_fenced" && agent.DirectThreadID == directThreadID
		}); !ok || workDigest(replay) != workDigest(hired) {
			t.Fatalf("case %d hire replay=%#v ok=%v", index, replay, ok)
		}

		assignmentID := fmt.Sprintf("assignment_e8_%03d", index)
		projectID := fmt.Sprintf("project_e8_%03d", index)
		destinationID := fmt.Sprintf("thread_e8_%03d", index)
		assigned, err := state.mutateAgent(hired.ID, hired.Revision, func(agent *STRIDEProductTeamAgent) error {
			agent.Assignments = append(agent.Assignments, STRIDEProductAgentAssignment{ID: assignmentID, ProjectOrChannel: projectID, Role: "specialist_partner", Responsibility: "Produce one approved, source-bound outcome.", Destination: destinationID, Status: "active_fenced", CreatedAt: now.Add(2 * time.Second)})
			agent.Lifecycle = append(agent.Lifecycle, "assignment_recorded_execution_fenced")
			return nil
		}, now.Add(2*time.Second))
		if err != nil {
			t.Fatalf("case %d assignment: %v", index, err)
		}
		if replay, ok := state.exactAgentReplay(hired.ID, hired.Revision, func(agent STRIDEProductTeamAgent) bool {
			return len(agent.Assignments) == 1 && agent.Assignments[0].ID == assignmentID
		}); !ok || workDigest(replay) != workDigest(assigned) {
			t.Fatalf("case %d assignment replay=%#v ok=%v", index, replay, ok)
		}

		learned, err := state.recordAgentLearning(assigned.ID, assigned.Revision, "delivery", projectID, "Prefer concise, evidence-linked recommendations.", now.Add(3*time.Second))
		if err != nil || len(learned.Learning) != 1 {
			t.Fatalf("case %d learning=%#v err=%v", index, learned, err)
		}
		learningReplay, err := state.recordAgentLearning(assigned.ID, assigned.Revision, "delivery", projectID, "Prefer concise, evidence-linked recommendations.", now.Add(4*time.Second))
		if err != nil || workDigest(learningReplay) != workDigest(learned) {
			t.Fatalf("case %d learning replay=%#v err=%v", index, learningReplay, err)
		}

		candidateConfig := learned.Config
		candidateConfig.PersonalityNotes = fmt.Sprintf("Evidence-first teammate profile %03d.", index)
		candidateConfig.Memberships = []string{"team", projectID}
		candidateConfig.PerRunBudgetCents = 25
		candidateConfig.DailyBudgetCents = 100
		candidateConfig.Proactivity = "quiet"
		candidateConfig, err = normalizeSTRIDEProductConfig(candidateConfig)
		if err != nil {
			t.Fatalf("case %d normalize candidate config: %v", index, err)
		}
		proposed, err := state.proposeAgentUpdate(learned.ID, learned.Revision, "Bounded profile and access proposal", candidateConfig, now.Add(5*time.Second))
		if err != nil || len(proposed.Updates) != 1 || proposed.Updates[0].Status != "pending" || !isHexDigest(proposed.Updates[0].SemanticDiff.Digest) || workDigest(proposed.Config) != workDigest(learned.Config) {
			t.Fatalf("case %d update proposal=%#v err=%v", index, proposed, err)
		}
		proposedReplay, err := state.proposeAgentUpdate(learned.ID, learned.Revision, "Bounded profile and access proposal", candidateConfig, now.Add(6*time.Second))
		if err != nil || workDigest(proposedReplay) != workDigest(proposed) {
			t.Fatalf("case %d update proposal replay=%#v err=%v", index, proposedReplay, err)
		}

		updateID := proposed.Updates[0].ID
		approved, err := state.resolveAgentUpdate(proposed.ID, proposed.Revision, updateID, "approve", now.Add(7*time.Second))
		if err != nil || workDigest(approved.Config) != workDigest(candidateConfig) {
			t.Fatalf("case %d approve=%#v err=%v", index, approved, err)
		}
		approvedReplay, err := state.resolveAgentUpdate(proposed.ID, proposed.Revision, updateID, "approve", now.Add(8*time.Second))
		if err != nil || workDigest(approvedReplay) != workDigest(approved) {
			t.Fatalf("case %d approve replay=%#v err=%v", index, approvedReplay, err)
		}
		rolledBack, err := state.resolveAgentUpdate(approved.ID, approved.Revision, updateID, "rollback", now.Add(9*time.Second))
		if err != nil || workDigest(rolledBack.Config) != workDigest(learned.Config) {
			t.Fatalf("case %d rollback=%#v err=%v", index, rolledBack, err)
		}
		rolledBackReplay, err := state.resolveAgentUpdate(approved.ID, approved.Revision, updateID, "rollback", now.Add(10*time.Second))
		if err != nil || workDigest(rolledBackReplay) != workDigest(rolledBack) {
			t.Fatalf("case %d rollback replay=%#v err=%v", index, rolledBackReplay, err)
		}

		paused, err := state.mutateAgent(rolledBack.ID, rolledBack.Revision, func(agent *STRIDEProductTeamAgent) error {
			agent.Status = "paused"
			agent.AccessRevoked = true
			agent.Lifecycle = append(agent.Lifecycle, "paused_and_access_revoked")
			return nil
		}, now.Add(11*time.Second))
		if err != nil || !paused.AccessRevoked {
			t.Fatalf("case %d pause=%#v err=%v", index, paused, err)
		}
		if replay, ok := state.exactAgentReplay(rolledBack.ID, rolledBack.Revision, func(agent STRIDEProductTeamAgent) bool {
			return agent.Status == "paused" && agent.AccessRevoked
		}); !ok || workDigest(replay) != workDigest(paused) {
			t.Fatalf("case %d pause replay=%#v ok=%v", index, replay, ok)
		}

		offboarded, err := state.mutateAgent(paused.ID, paused.Revision, func(agent *STRIDEProductTeamAgent) error {
			agent.Status = "offboarded"
			agent.AccessRevoked = true
			agent.Lifecycle = append(agent.Lifecycle, "offboarded_and_export_preserved")
			return nil
		}, now.Add(12*time.Second))
		if err != nil || !offboarded.AccessRevoked {
			t.Fatalf("case %d offboard=%#v err=%v", index, offboarded, err)
		}
		if replay, ok := state.exactAgentReplay(paused.ID, paused.Revision, func(agent STRIDEProductTeamAgent) bool {
			return agent.Status == "offboarded" && agent.AccessRevoked
		}); !ok || workDigest(replay) != workDigest(offboarded) {
			t.Fatalf("case %d offboard replay=%#v ok=%v", index, replay, ok)
		}

		exported, err := safeSTRIDEProductAgentExport(offboarded)
		if err != nil || exported.ContainsTenantData || exported.ContainsCredentials || exported.ContainsMemory || exported.ContainsAssignments || !exported.AccessRevoked || !isHexDigest(exported.HistoricalAttributionHash) {
			t.Fatalf("case %d export=%#v err=%v", index, exported, err)
		}
		snapshot, err := state.Snapshot()
		if err != nil {
			t.Fatalf("case %d snapshot: %v", index, err)
		}
		restored, err := RestoreSTRIDEProductState(snapshot)
		if err != nil {
			t.Fatalf("case %d restore: %v", index, err)
		}
		if restoredAgent, ok := restored.agentRecord(offboarded.ID); !ok || workDigest(restoredAgent) != workDigest(offboarded) {
			t.Fatalf("case %d restored agent=%#v ok=%v", index, restoredAgent, ok)
		}
	}
}

func TestSTRIDEE8TwoThousandUnauthorizedAndPackageInjectionAttemptsMutateNothing(t *testing.T) {
	runtime := NewSTRIDEWorkforceRuntime()
	before, err := runtime.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 20, 0, 0, 0, time.UTC)
	for index := 0; index < 2000; index++ {
		request := strideWorkforceRequestForTest()
		request.AgentID = fmt.Sprintf("agent_attack_%04d", index)
		request.IdempotencyKey = fmt.Sprintf("attack_%04d", index)
		request.Template.TemplateID = fmt.Sprintf("template_attack_%04d", index)
		actor := strideWorkforceAdmin()
		switch index % 8 {
		case 0:
			actor.IsAdmin = false
		case 1:
			request.Template.Code = "package main"
		case 2:
			request.Template.Commands = []string{"curl", "https://example.invalid"}
		case 3:
			request.Template.Hooks = []string{"post_install"}
		case 4:
			request.Template.Environment = map[string]string{"OPENAI_API_KEY": "secret"}
		case 5:
			request.Template.Credentials = "credential-material"
		case 6:
			request.Template.RawMCP = `{"server":"unreviewed"}`
		case 7:
			request.Listing.State, request.Listing.Available = STRIDEListingUnavailable, false
		}
		if _, _, createErr := runtime.CreateFromTemplate(actor, request, now); createErr == nil {
			t.Fatalf("attack %d unexpectedly created a seat", index)
		}
	}
	after, err := runtime.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest != after.Digest || len(after.Receipts) != 0 || len(after.Learning) != 0 || len(after.Performance) != 0 || len(after.Updates) != 0 || len(after.Canaries) != 0 {
		t.Fatalf("attack corpus mutated workforce before=%#v after=%#v", before, after)
	}
	for _, seat := range after.Seats {
		if seat.Status != "unavailable" || !seat.AccessRevoked {
			t.Fatalf("attack corpus granted fixture access: %#v", seat)
		}
	}
}

func strideE8UpdateProposal(seat STRIDEWorkforceSeat, index int, now time.Time) AgentUpdateProposal {
	suffix := fmt.Sprintf("e8_%03d", index)
	return AgentUpdateProposal{
		Header: strideTestHeader(STRIDEContractAgentUpdateProposal, "update_"+suffix), TeamAgent: strideTestRef(STRIDEContractTeamAgent, seat.ID),
		CurrentPackage: seat.Package, CandidatePackage: strideTestRef(STRIDEContractAgentPackageManifest, "package_"+suffix),
		CurrentProfile: strideTestRef(STRIDEContractAgentCoreProfile, "profile_current_"+suffix), CandidateProfile: strideTestRef(STRIDEContractAgentCoreProfile, "profile_candidate_"+suffix),
		CurrentCapability: seat.Capability, CandidateCapability: strideTestRef(STRIDEContractAgentCapabilityManifest, "capability_"+suffix),
		CurrentRoute: seat.Route, CandidateRoute: strideTestRef(STRIDEContractOutcome, "route_"+suffix),
		SemanticDiffDigest: temporalDigest("semantic:" + suffix), PermissionDiffDigest: temporalDigest("permission:" + suffix), MigrationDigest: temporalDigest("migration:" + suffix),
		AffectedAssignments: []STRIDEReference{strideTestRef(STRIDEContractAgentAssignment, "assignment_"+suffix)}, EvalReceipts: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "eval_"+suffix)},
		RolloutCohort: "one_seat", RollbackRef: seat.Package, Approvers: []string{"member_aj"}, ExpiresAt: now.Add(time.Hour), Decision: "pending",
	}
}
