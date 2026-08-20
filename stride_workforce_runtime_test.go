package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func strideSnapshotAuthorityForTest() STRIDESnapshotMACAuthority {
	return STRIDESnapshotMACAuthority{KeyID: "snapshot_key_test", Key: []byte("0123456789abcdef0123456789abcdef")}
}

func resignWorkforceSnapshotForTest(t *testing.T, snapshot *STRIDEWorkforceSnapshot) {
	t.Helper()
	var err error
	snapshot.Digest, err = strideWorkforceSnapshotStateDigest(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Signature, err = strideSnapshotMAC(strideSnapshotAuthorityForTest(), "stride_workforce", snapshot.Generation, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
}

func strideWorkforceRequestForTest() STRIDEWorkforceHireRequest {
	manifest := strideMarketplaceManifestForTest()
	listing := STRIDEMarketplaceListingRecord{Listing: strideMarketplaceListingForTest(manifest, true), State: STRIDEListingAvailable, Available: true}
	template := STRIDEAgentTemplate{TemplateID: "template_mary", Package: strideMarketplaceManifestReference(manifest), Category: "marketing", OutcomeDigest: strideTestDigest("a"), PersonalityDigest: strideTestDigest("b"), Evidence: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "mary_evidence")}, AccessSummaryDigest: strideTestDigest("c"), CostBand: "low", Memberships: []string{"team"}, PerRunBudgetCents: 1, DailyBudgetCents: 10, MonthlyBudgetCents: 100, Concurrency: 1, Proactivity: "disabled"}
	return STRIDEWorkforceHireRequest{IdempotencyKey: "create_mary", AgentID: "agent_mary", Template: template, Listing: listing, Owner: "member_aj", Capability: strideTestRef(STRIDEContractAgentCapabilityManifest, "mary_capability"), Route: strideTestRef(STRIDEContractOutcome, "mary_route")}
}

func strideWorkforceAdmin() STRIDEWorkforceActor {
	return STRIDEWorkforceActor{ID: "member_aj", IsAdmin: true}
}

func TestSTRIDEWorkforceLifecycleIsIdempotentAndRuntimeDefaultOff(t *testing.T) {
	runtime := NewSTRIDEWorkforceRuntime()
	now := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	request := strideWorkforceRequestForTest()
	fixture := runtime.ScoutRosterView().Seats[0]
	if fixture.Status != "unavailable" || !fixture.AccessRevoked {
		t.Fatalf("fixture is not unavailable: %#v", fixture)
	}
	seat, receipt, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), request, now)
	if err != nil || seat.Status != "draft_hire" || seat.OrgIdentity != "org_agent:agent_mary" || seat.DirectThread != "thread_agent_agent_mary" || !seat.AccessRevoked {
		t.Fatalf("create: %#v %#v %v", seat, receipt, err)
	}
	second, duplicate, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), request, now.Add(time.Minute))
	if err != nil || second.ID != seat.ID || duplicate.Digest != receipt.Digest {
		t.Fatalf("idempotent create: %#v %#v %v", second, duplicate, err)
	}
	if _, err := runtime.Trial(strideWorkforceAdmin(), seat.ID, "trial_mary", now); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := runtime.Trial(strideWorkforceAdmin(), seat.ID, "trial_mary", now.Add(time.Minute)); err != nil || duplicate.After != "trial_pending" {
		t.Fatalf("idempotent trial: %#v %v", duplicate, err)
	}
	if _, err := runtime.Hire(strideWorkforceAdmin(), seat.ID, "hire_mary", now); err != nil {
		t.Fatal(err)
	}
	for index, stage := range []string{"identity", "capability", "profile", "route"} {
		if _, err := runtime.Activate(strideWorkforceAdmin(), seat.ID, "activate_mary_"+stage, stage, now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatalf("activate %s: %v", stage, err)
		}
	}
	reviewView := runtime.ScoutRosterView()
	var reviewSeat STRIDEWorkforceSeat
	for _, candidate := range reviewView.Seats {
		if candidate.ID == seat.ID {
			reviewSeat = candidate
		}
	}
	if reviewSeat.Status != "review_required" || reviewSeat.ActivationStage != "review" || !reviewSeat.AccessRevoked {
		t.Fatalf("route activation bypassed human review: %#v", reviewSeat)
	}
	if _, err := runtime.IssueRuntimeGrant(strideWorkforceAdmin(), seat.ID, now); !errors.Is(err, ErrSTRIDEWorkforceAuthority) {
		t.Fatalf("pre-review runtime grant error=%v", err)
	}
	if _, err := runtime.Review(strideWorkforceAdmin(), seat.ID, "review_mary", now.Add(4*time.Minute)); err != nil {
		t.Fatalf("review: %v", err)
	}
	view := runtime.ScoutRosterView()
	var mary STRIDEWorkforceSeat
	for _, candidate := range view.Seats {
		if candidate.ID == seat.ID {
			mary = candidate
		}
	}
	if mary.Status != "active" || mary.ActivationStage != "complete" || mary.AccessRevoked {
		t.Fatalf("active roster record invalid: %#v", mary)
	}
	activeSnapshot, err := runtime.AuthenticatedSnapshot(strideSnapshotAuthorityForTest(), 1)
	if err != nil {
		t.Fatal(err)
	}
	activeRestored, err := RestoreSTRIDEWorkforceRuntime(activeSnapshot, STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 1})
	if err != nil {
		t.Fatalf("restore receipt-proven active seat: %v", err)
	}
	activeView := activeRestored.ScoutRosterView()
	foundActive := false
	for _, candidate := range activeView.Seats {
		foundActive = foundActive || candidate.ID == seat.ID && candidate.Status == "active" && !candidate.AccessRevoked
	}
	if !foundActive {
		t.Fatal("receipt-proven active seat was not restored")
	}
	if grant, err := runtime.IssueRuntimeGrant(strideWorkforceAdmin(), seat.ID, now); !errors.Is(err, ErrSTRIDEWorkforceFenced) || !grant.Fenced || !grant.ExpiresAt.Equal(now.Add(15*time.Minute)) || !isHexDigest(grant.CapabilityTokenDigest) {
		t.Fatalf("runtime grant=%#v error=%v", grant, err)
	}
	if _, err := runtime.Pause(strideWorkforceAdmin(), seat.ID, "pause_mary", now); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Quarantine(strideWorkforceAdmin(), seat.ID, "quarantine_mary", now); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Offboard(strideWorkforceAdmin(), seat.ID, "offboard_mary", now); err != nil {
		t.Fatal(err)
	}
	exported, _, err := runtime.Export(strideWorkforceAdmin(), seat.ID, "export_mary", now)
	if err != nil || exported.Status != "offboarded" || exported.OffboardedAt == nil {
		t.Fatalf("export/offboard: %#v %v", exported, err)
	}
	if _, err := runtime.IssueRuntimeGrant(strideWorkforceAdmin(), seat.ID, now); !errors.Is(err, ErrSTRIDEWorkforceAuthority) {
		t.Fatalf("offboard grant error=%v", err)
	}
}

func TestSTRIDEWorkforceInternalPreviewSeatSurvivesAuthenticatedRestore(t *testing.T) {
	runtime := NewSTRIDEWorkforceRuntime()
	now := time.Date(2026, 7, 30, 19, 30, 0, 0, time.UTC)
	config := STRIDERuntimeConfig{
		Enabled: true, TenantID: "bonfire", ProductPreviewEnabled: true,
		Authority: strideSnapshotAuthorityForTest(),
	}
	activation, err := mintSTRIDEProductActivationReceipt(config, 1, STRIDEProductScopeMarketplace, now)
	if err != nil {
		t.Fatal(err)
	}
	product := NewSTRIDEProductState()
	agent, err := product.beginTrial("mary-marketing", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = product.mutateAgent(agent.ID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
		value.Status = "hired_fenced"
		value.DirectThreadID = "thread_mary_preview"
		value.AccessRevoked = false
		value.Lifecycle = append(value.Lifecycle, "human_approved_hire", "direct_thread_created", "provider_runtime_remains_fenced")
		return nil
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := runtime.installFencedInternalPreviewSeat(strideWorkforceAdmin(), activation, agent, now)
	if err != nil || installed.Status != "active" || installed.ActivationStage != "complete" || installed.AccessRevoked {
		t.Fatalf("install preview seat=%+v err=%v", installed, err)
	}
	if meetingSpecialistContainsString(installed.Memberships, "organization") {
		t.Fatalf("hire installed implicit organization authority: %+v", installed.Memberships)
	}
	if replayed, replayErr := runtime.installFencedInternalPreviewSeat(strideWorkforceAdmin(), activation, agent, now.Add(time.Minute)); replayErr != nil || mustSTRIDEWorkforceDigest(replayed) != mustSTRIDEWorkforceDigest(installed) {
		t.Fatalf("replay preview seat=%+v err=%v", replayed, replayErr)
	}

	snapshot, err := runtime.AuthenticatedSnapshot(strideSnapshotAuthorityForTest(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Receipts) != 8 {
		t.Fatalf("preview receipt chain length=%d, want 8", len(snapshot.Receipts))
	}
	createReceipts, reviewReceipts := 0, 0
	for _, lifecycleReceipt := range snapshot.Receipts {
		if lifecycleReceipt.Action == "create" {
			createReceipts++
			if !isHexDigest(lifecycleReceipt.RequestDigest) {
				t.Fatalf("preview create request digest=%q", lifecycleReceipt.RequestDigest)
			}
		}
		if lifecycleReceipt.Action == "review" && lifecycleReceipt.Before == "review_required" && lifecycleReceipt.After == "active" {
			reviewReceipts++
		}
	}
	if createReceipts != 1 {
		t.Fatalf("preview create receipt count=%d, want 1", createReceipts)
	}
	if reviewReceipts != 1 {
		t.Fatalf("preview review receipt count=%d, want 1", reviewReceipts)
	}
	restored, err := RestoreSTRIDEWorkforceRuntime(snapshot, STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 1})
	if err != nil {
		t.Fatalf("restore preview seat: %v", err)
	}
	var restoredSeat STRIDEWorkforceSeat
	for _, candidate := range restored.ScoutRosterView().Seats {
		if candidate.ID == installed.ID {
			restoredSeat = candidate
		}
	}
	if mustSTRIDEWorkforceDigest(restoredSeat) != mustSTRIDEWorkforceDigest(installed) {
		t.Fatalf("restored preview seat=%+v want=%+v", restoredSeat, installed)
	}

	withoutReceipts := snapshot
	withoutReceipts.Receipts = nil
	resignWorkforceSnapshotForTest(t, &withoutReceipts)
	if _, err := RestoreSTRIDEWorkforceRuntime(withoutReceipts, STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 1}); !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("receipt-free preview restore error=%v", err)
	}
}

func TestSTRIDEWorkforceRestoreGrandfathersOnlyPreReviewGateRouteActivation(t *testing.T) {
	runtime := NewSTRIDEWorkforceRuntime()
	now := time.Date(2026, 8, 5, 2, 53, 9, 0, time.UTC)
	request := strideWorkforceRequestForTest()
	seat, _, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), request, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Trial(strideWorkforceAdmin(), seat.ID, "trial_legacy", now); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Hire(strideWorkforceAdmin(), seat.ID, "hire_legacy", now); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"identity", "capability", "profile", "route"} {
		if _, err := runtime.Activate(strideWorkforceAdmin(), seat.ID, "activate_legacy_"+stage, stage, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.Review(strideWorkforceAdmin(), seat.ID, "review_legacy", now); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.AuthenticatedSnapshot(strideSnapshotAuthorityForTest(), 128)
	if err != nil {
		t.Fatal(err)
	}
	legacy := snapshot
	legacy.Receipts = append([]STRIDEWorkforceReceipt(nil), snapshot.Receipts...)
	legacy.Seats = append([]STRIDEWorkforceSeat(nil), snapshot.Seats...)
	filtered := legacy.Receipts[:0]
	for _, receipt := range legacy.Receipts {
		if receipt.Action == "review" {
			continue
		}
		if receipt.Action == "activate_route" {
			receipt.After = "active"
			receipt.Digest = newSTRIDEWorkforceReceipt(receipt.Action, receipt.IdempotencyKey, receipt.AgentID, receipt.Before, receipt.After, receipt.At).Digest
		}
		filtered = append(filtered, receipt)
	}
	legacy.Receipts = filtered
	resignWorkforceSnapshotForTest(t, &legacy)
	policy := STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 128}
	if restored, err := RestoreSTRIDEWorkforceRuntime(legacy, policy); err != nil {
		t.Fatalf("restore signed pre-cutover lifecycle: %v", err)
	} else if view := restored.ScoutRosterView(); len(view.Seats) == 0 {
		t.Fatal("restored pre-cutover roster is empty")
	}

	postCutover := legacy
	postCutover.Receipts = append([]STRIDEWorkforceReceipt(nil), legacy.Receipts...)
	postCutover.Seats = append([]STRIDEWorkforceSeat(nil), legacy.Seats...)
	for index := range postCutover.Receipts {
		if postCutover.Receipts[index].Action != "activate_route" {
			continue
		}
		postCutover.Receipts[index].At = time.Unix(strideWorkforceReviewGateCutoverUnix, 0).UTC()
		postCutover.Receipts[index].Digest = newSTRIDEWorkforceReceipt(postCutover.Receipts[index].Action, postCutover.Receipts[index].IdempotencyKey, postCutover.Receipts[index].AgentID, postCutover.Receipts[index].Before, postCutover.Receipts[index].After, postCutover.Receipts[index].At).Digest
		for seatIndex := range postCutover.Seats {
			if postCutover.Seats[seatIndex].ID == postCutover.Receipts[index].AgentID {
				postCutover.Seats[seatIndex].UpdatedAt = postCutover.Receipts[index].At
			}
		}
	}
	resignWorkforceSnapshotForTest(t, &postCutover)
	if _, err := RestoreSTRIDEWorkforceRuntime(postCutover, policy); !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("post-cutover route-only active restore error=%v", err)
	}
}

func TestSTRIDEWorkforceAuthorityCanaryAndLearningBoundaries(t *testing.T) {
	runtime := NewSTRIDEWorkforceRuntime()
	now := time.Now().UTC()
	request := strideWorkforceRequestForTest()
	seat, _, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), request, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.CreateFromTemplate(STRIDEWorkforceActor{ID: "member_erick"}, request, now); !errors.Is(err, ErrSTRIDEAdminRequired) {
		t.Fatalf("non-admin create error=%v", err)
	}
	if err := runtime.RegisterCanary(strideWorkforceAdmin(), STRIDEWorkforceCanaryManifest{SeatID: seat.ID, RouteDescriptionID: "route_mary", Synthetic: true, OneSeat: true, ProfileChange: true, RouteChange: true, Status: "draft"}); !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("combined canary error=%v", err)
	}
	if err := runtime.RegisterCanary(strideWorkforceAdmin(), STRIDEWorkforceCanaryManifest{SeatID: seat.ID, RouteDescriptionID: "route_mary", Synthetic: true, OneSeat: true, Status: "qualified"}); !errors.Is(err, ErrSTRIDEWorkforceFenced) {
		t.Fatalf("synthetic qualify error=%v", err)
	}
	learning := AgentLearningRecord{Header: strideTestHeader(STRIDEContractAgentLearningRecord, "learning_mary"), AgentID: seat.ID, Kind: "domain", Subject: "marketing", Scope: "team", LessonDigest: strideTestDigest("a"), Evidence: []STRIDEReference{strideTestRef(STRIDEContractConversationEvent, "learning_source")}, Confidence: .5, FirstObserved: now, LastObserved: now, Audience: strideTestAudience(), Status: "candidate"}
	if err := runtime.RecordLearning(strideWorkforceAdmin(), learning); err != nil {
		t.Fatalf("learning: %v", err)
	}
	learning.Subject = "medical_history"
	if err := runtime.RecordLearning(strideWorkforceAdmin(), learning); !errors.Is(err, ErrSTRIDEWorkforceSensitive) {
		t.Fatalf("sensitive learning error=%v", err)
	}
	learning.Subject = "marketing"
	learning.Status = "corrected"
	if err := runtime.CorrectLearning(strideWorkforceAdmin(), learning); err != nil {
		t.Fatalf("correct: %v", err)
	}
	if err := runtime.ForgetLearning(strideWorkforceAdmin(), learning.Header.ID); err != nil {
		t.Fatal(err)
	}
	if err := runtime.PurgeLearning(strideWorkforceAdmin(), learning.Header.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSTRIDEWorkforcePerformanceUpdatesAndRestartSnapshot(t *testing.T) {
	runtime := NewSTRIDEWorkforceRuntime()
	now := time.Now().UTC()
	seat, _, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), strideWorkforceRequestForTest(), now)
	if err != nil {
		t.Fatal(err)
	}
	evaluation := strideTestRef(STRIDEContractOutcome, "evaluation_mary")
	performance := AgentPerformanceReceipt{Header: strideTestHeader(STRIDEContractAgentPerformanceReceipt, "receipt_mary"), Assignment: strideTestRef(STRIDEContractAgentAssignment, "assignment_mary"), WorkRun: strideTestRef(STRIDEContractWorkRun, "run_mary"), Output: strideTestRef(STRIDEContractOutcome, "output_mary"), CriteriaDigest: strideTestDigest("a"), Evidence: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "evidence_mary"), evaluation}, Reviewer: "member_aj", FeedbackDigest: strideTestDigest("b"), Verdict: "accepted", Route: seat.Route, Profile: strideTestRef(STRIDEContractAgentCoreProfile, "mary_profile"), Package: seat.Package, CostCents: 1, LatencyMS: 2, EligibleClaims: []string{"marketing"}}
	if eligible, err := runtime.RecordPerformance(strideWorkforceAdmin(), seat.ID, performance, STRIDEPerformanceApproval{}); eligible || !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("unapproved performance: eligible=%v err=%v", eligible, err)
	}
	if eligible, err := runtime.RecordPerformance(strideWorkforceAdmin(), seat.ID, performance, STRIDEPerformanceApproval{Capability: seat.Capability, Evaluation: evaluation, ApprovedBy: "member_aj"}); err != nil || !eligible {
		t.Fatalf("approved performance: eligible=%v err=%v", eligible, err)
	}
	proposal := AgentUpdateProposal{Header: strideTestHeader(STRIDEContractAgentUpdateProposal, "update_mary"), TeamAgent: strideTestRef(STRIDEContractTeamAgent, seat.ID), CurrentPackage: seat.Package, CandidatePackage: strideTestRef(STRIDEContractAgentPackageManifest, "package_mary_v2"), CurrentProfile: strideTestRef(STRIDEContractAgentCoreProfile, "mary_profile"), CandidateProfile: strideTestRef(STRIDEContractAgentCoreProfile, "mary_profile_v2"), CurrentCapability: seat.Capability, CandidateCapability: strideTestRef(STRIDEContractAgentCapabilityManifest, "mary_capability_v2"), CurrentRoute: seat.Route, CandidateRoute: strideTestRef(STRIDEContractOutcome, "mary_route_v2"), SemanticDiffDigest: strideTestDigest("c"), PermissionDiffDigest: strideTestDigest("d"), MigrationDigest: strideTestDigest("e"), AffectedAssignments: []STRIDEReference{strideTestRef(STRIDEContractAgentAssignment, "assignment_mary")}, EvalReceipts: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "eval_mary")}, RolloutCohort: "one_seat", RollbackRef: strideTestRef(STRIDEContractAgentPackageManifest, "package_mary_v1"), Approvers: []string{"member_aj"}, ExpiresAt: now.Add(time.Hour), Decision: "pending"}
	review := STRIDEUpdateReview{Proposal: proposal, PersonalityDiffDigest: strideTestDigest("f"), ModelDiffDigest: strideTestDigest("a"), CostDiffDigest: strideTestDigest("b"), Status: "pending"}
	if err := runtime.ProposeUpdate(strideWorkforceAdmin(), review); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := runtime.ApproveUpdate(strideWorkforceAdmin(), proposal.Header.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := runtime.ApplyUpdateCanary(strideWorkforceAdmin(), proposal.Header.ID, "route"); !errors.Is(err, ErrSTRIDEWorkforceState) {
		t.Fatalf("route before profile error=%v", err)
	}
	if err := runtime.ApplyUpdateCanary(strideWorkforceAdmin(), proposal.Header.ID, "profile"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ApplyUpdateCanary(strideWorkforceAdmin(), proposal.Header.ID, "route"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RollbackUpdate(strideWorkforceAdmin(), proposal.Header.ID); err != nil {
		t.Fatalf("rollback update: %v", err)
	}
	snapshot, err := runtime.AuthenticatedSnapshot(strideSnapshotAuthorityForTest(), 7)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSTRIDEWorkforceRuntime(snapshot, STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 7})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	second, err := restored.Snapshot()
	if err != nil || second.Digest != snapshot.Digest {
		t.Fatalf("snapshot drift: %q %q %v", snapshot.Digest, second.Digest, err)
	}
}

func TestSTRIDEWorkforceRestoreRejectsRecomputedDigestRollbackAndFabricatedAuthority(t *testing.T) {
	runtime := NewSTRIDEWorkforceRuntime()
	now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
	seat, _, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), strideWorkforceRequestForTest(), now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.AuthenticatedSnapshot(strideSnapshotAuthorityForTest(), 10)
	if err != nil {
		t.Fatal(err)
	}
	policy := STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 10}

	// A content digest is not authenticity: changing state and recomputing it
	// must still fail under the original MAC.
	recomputed := snapshot
	recomputed.Seats = append([]STRIDEWorkforceSeat(nil), snapshot.Seats...)
	for index := range recomputed.Seats {
		if recomputed.Seats[index].ID == seat.ID {
			recomputed.Seats[index].Status = "active"
			recomputed.Seats[index].ActivationStage = "complete"
			recomputed.Seats[index].AccessRevoked = false
		}
	}
	recomputed.Digest, err = strideWorkforceSnapshotStateDigest(recomputed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSTRIDEWorkforceRuntime(recomputed, policy); !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("recomputed digest restore error=%v", err)
	}

	// Even a snapshot sealed by the trusted producer is rejected if canonical
	// lifecycle receipts do not prove its privileged active state.
	resignWorkforceSnapshotForTest(t, &recomputed)
	if _, err := RestoreSTRIDEWorkforceRuntime(recomputed, policy); !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("receipt-free active state restore error=%v", err)
	}

	qualifiedSynthetic := snapshot
	qualifiedSynthetic.Canaries = append(qualifiedSynthetic.Canaries, STRIDEWorkforceCanaryManifest{SeatID: seat.ID, RouteDescriptionID: "route_mary", Synthetic: true, OneSeat: true, Status: "qualified"})
	resignWorkforceSnapshotForTest(t, &qualifiedSynthetic)
	if _, err := RestoreSTRIDEWorkforceRuntime(qualifiedSynthetic, policy); !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("qualified synthetic canary restore error=%v", err)
	}

	if _, err := RestoreSTRIDEWorkforceRuntime(snapshot, STRIDESnapshotRestorePolicy{Authority: strideSnapshotAuthorityForTest(), MinimumGeneration: 11}); !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("rollback generation restore error=%v", err)
	}
	if unsigned, err := runtime.Snapshot(); err != nil {
		t.Fatal(err)
	} else if _, err := RestoreSTRIDEWorkforceRuntime(unsigned, policy); !errors.Is(err, ErrSTRIDEWorkforceInvalid) {
		t.Fatalf("unsigned restore error=%v", err)
	}
}

func TestSTRIDEWorkforceConcurrentReadSnapshotsAreRaceSafe(t *testing.T) {
	runtime := NewSTRIDEWorkforceRuntime()
	now := time.Now().UTC()
	if _, _, err := runtime.CreateFromTemplate(strideWorkforceAdmin(), strideWorkforceRequestForTest(), now); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func() { defer group.Done(); _, _ = runtime.Snapshot(); _ = runtime.ScoutRosterView() }()
	}
	group.Wait()
}
