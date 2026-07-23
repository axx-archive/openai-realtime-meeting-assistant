package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type insightsTestPurgeResolver struct {
	generation int64
	err        error
}

func (r insightsTestPurgeResolver) CurrentPurgeGeneration(context.Context, string) (int64, error) {
	return r.generation, r.err
}

type insightsTestAuthorizationVerifier struct {
	mu               sync.Mutex
	deny             bool
	denyRequirements map[string]bool
	actions          []ACLAction
	requirements     []string
	targets          []InsightsOpportunitiesAuthorizationTarget
	consumeTargets   []InsightsOpportunitiesAuthorizationTarget
	consumed         map[string]bool
	receipts         map[string]InsightsOpportunitiesApprovalConsumption
	runTargets       map[string]InsightsOpportunitiesAuthorizationTarget
	expectedApproval *InsightsOpportunitiesAuthorizationTarget
	authorizeHook    func(ACLAction, InsightsOpportunitiesAuthorizationTarget)
	requirementHook  func(string, InsightsOpportunitiesAuthorizationTarget)
}

func (v *insightsTestAuthorizationVerifier) AuthorizeInsightsOpportunities(_ context.Context, _ ACLPrincipal, action ACLAction, target InsightsOpportunitiesAuthorizationTarget) ACLDecision {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.actions = append(v.actions, action)
	v.targets = append(v.targets, target)
	if v.authorizeHook != nil {
		v.authorizeHook(action, target)
	}
	if v.deny {
		return ACLDecision{DenialCode: ACLDenialNotFound}
	}
	return ACLDecision{Allowed: true, MatchedGrantID: "server-grant", ACLVersion: 1, Obligations: []string{"audit"}}
}

func (v *insightsTestAuthorizationVerifier) VerifyInsightsOpportunitiesRequirement(_ context.Context, _ ACLPrincipal, requirement string, target InsightsOpportunitiesAuthorizationTarget) ACLDecision {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.requirements = append(v.requirements, requirement)
	v.targets = append(v.targets, target)
	if v.requirementHook != nil {
		v.requirementHook(requirement, target)
	}
	if v.deny || v.denyRequirements[requirement] {
		return ACLDecision{DenialCode: ACLDenialNotFound}
	}
	return ACLDecision{Allowed: true, MatchedGrantID: "requirement-grant", ACLVersion: 1, Obligations: []string{"audit"}}
}

func (v *insightsTestAuthorizationVerifier) ConsumeInsightsOpportunitiesApproval(_ context.Context, _ ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget) InsightsOpportunitiesApprovalConsumption {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.consumeTargets = append(v.consumeTargets, target)
	if v.deny || target.ApprovalID == "" || (v.expectedApproval != nil && target != *v.expectedApproval) {
		return InsightsOpportunitiesApprovalConsumption{}
	}
	if v.consumed == nil {
		v.consumed = make(map[string]bool)
		v.receipts = make(map[string]InsightsOpportunitiesApprovalConsumption)
	}
	if v.consumed[target.ApprovalID] {
		return InsightsOpportunitiesApprovalConsumption{}
	}
	v.consumed[target.ApprovalID] = true
	binding, _ := insightsApprovalTargetDigest(target)
	receipt := InsightsOpportunitiesApprovalConsumption{
		Consumed: true, ConsumptionID: "consumption-" + target.ApprovalID, CheckpointID: "checkpoint-" + target.ApprovalID,
		RunID: target.RunID, BindingDigest: binding,
		Decision: ACLDecision{Allowed: true, MatchedGrantID: "approval-grant", ACLVersion: 1, Obligations: []string{"audit"}},
	}
	v.receipts[target.ApprovalID] = receipt
	return receipt
}

func (v *insightsTestAuthorizationVerifier) ResumeInsightsOpportunitiesApproval(_ context.Context, _ ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget, receipt InsightsOpportunitiesApprovalConsumption) ACLDecision {
	v.mu.Lock()
	defer v.mu.Unlock()
	stored, ok := v.receipts[target.ApprovalID]
	binding, _ := insightsApprovalTargetDigest(target)
	if v.deny || !ok || !reflect.DeepEqual(stored, receipt) || receipt.BindingDigest != binding {
		return ACLDecision{DenialCode: ACLDenialNotFound}
	}
	return ACLDecision{Allowed: true, MatchedGrantID: "resume-grant", ACLVersion: 1, Obligations: []string{"audit"}}
}

func (v *insightsTestAuthorizationVerifier) AdvanceInsightsOpportunitiesRun(_ context.Context, _ ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget) InsightsOpportunitiesRunTransition {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.deny || target.RunID == "" || target.ReportRevision < 1 || target.ReportRevision > 2 {
		return InsightsOpportunitiesRunTransition{}
	}
	if v.runTargets == nil {
		v.runTargets = make(map[string]InsightsOpportunitiesAuthorizationTarget)
	}
	prior, found := v.runTargets[target.RunID]
	decision := ACLDecision{Allowed: true, MatchedGrantID: "run-transition-grant", ACLVersion: 1, Obligations: []string{"audit"}}
	if found && prior == target {
		return InsightsOpportunitiesRunTransition{Resumed: true, CheckpointID: "run-checkpoint-" + target.RunID, Decision: decision}
	}
	if (!found && target.ReportRevision != 1) || (found && (target.ReportRevision != prior.ReportRevision+1 || target.ParentReportDigest != prior.ReportDigest)) {
		return InsightsOpportunitiesRunTransition{}
	}
	v.runTargets[target.RunID] = target
	return InsightsOpportunitiesRunTransition{Advanced: true, CheckpointID: "run-checkpoint-" + target.RunID, Decision: decision}
}

func (v *insightsTestAuthorizationVerifier) AuthorizeInsightsOpportunitiesPublication(_ context.Context, _ ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget, key string) (InsightsOpportunitiesWorkspaceWriteAuthority, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.requirements = append(v.requirements, insightsRequirementActiveOrganizationMember)
	v.actions = append(v.actions, ACLWrite)
	v.targets = append(v.targets, target)
	if v.deny || v.denyRequirements[insightsRequirementActiveOrganizationMember] {
		return InsightsOpportunitiesWorkspaceWriteAuthority{}, errors.New("publication authority denied")
	}
	digest, err := CanonicalStateDigest(target)
	if err != nil {
		return InsightsOpportunitiesWorkspaceWriteAuthority{}, err
	}
	return InsightsOpportunitiesWorkspaceWriteAuthority{
		AuthorityID:    "publication-authority-" + key,
		TargetDigest:   digest,
		IdempotencyKey: key,
		Decision:       ACLDecision{Allowed: true, MatchedGrantID: "publication-grant", ACLVersion: 1, Obligations: []string{"audit"}},
	}, nil
}

func (v *insightsTestAuthorizationVerifier) VerifyInsightsOpportunitiesPublication(_ context.Context, _ ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget, key string, authority InsightsOpportunitiesWorkspaceWriteAuthority) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.deny || v.denyRequirements[insightsRequirementActiveOrganizationMember] {
		return errors.New("publication authority denied")
	}
	return authority.validate(target, key)
}

func validInsightsRequest(t *testing.T) InsightsOpportunitiesRequest {
	t.Helper()
	start := time.Date(2026, time.July, 22, 16, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	contentDigest := strings.Repeat("c", 64)
	query := "Find decision-ready opportunities."
	snapshot := RetrievalSnapshot{
		TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: "member-1",
		Query: query, QueryDigest: digestBrainString(query),
		Temporal: TemporalQuery{
			StartUTC: start, EndUTC: end, Timezone: "UTC", Interpretation: TemporalExplicitRange,
			InterpretationNote: "Explicit five-minute fixture range.",
		},
		SourceHighWater: 12, ProjectionHighWater: 12, PurgeGeneration: 3,
		Sources: []RetrievalSnapshotSource{{
			EvidenceID: "evidence-1",
			Evidence: BrainEvidenceRef{
				TenantID: "tenant-1", SourceFamily: "transcript", ObjectID: "meeting-object-1",
				ContentRevision: 1, ACLVersion: 1, ContentDigest: contentDigest,
				OccurredStart: start, OccurredEnd: end, PurgeGeneration: 3, Trust: BrainEvidenceTrusted,
			},
		}},
		CreatedAt: end.Add(time.Second),
	}
	var err error
	snapshot.SnapshotID, err = snapshot.CanonicalID()
	if err != nil {
		t.Fatal(err)
	}
	coverage := RecallCoverage{
		SnapshotID: snapshot.SnapshotID, Status: RecallCoverageComplete,
		RequestedStartUTC: start, RequestedEndUTC: end, ResolvedStartUTC: start, ResolvedEndUTC: end,
		Timezone: "UTC", SourceHighWater: 12, ProjectionHighWater: 12,
		Sources:           []RecallSourceCoverage{{SourceFamily: "transcript", ObjectID: "meeting-object-1", ContentDigest: contentDigest, Status: RecallSourceFresh}},
		AuthorizedSources: 1, FreshSources: 1,
		Lanes: RecallLaneCoverage{Lexical: RecallLaneActive, Semantic: RecallLaneNotRequired, Digest: RecallLaneActive, Raw: RecallLaneActive},
		AsOf:  end,
	}
	coverage.Digest, err = coverage.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	request := InsightsOpportunitiesRequest{
		Schema: insightsOpportunitiesRequestSchema, RequestID: "request-1", RunID: "run-1", TenantID: "tenant-1",
		PrincipalKind: ACLPrincipalUser, PrincipalID: "member-1", PromptVersion: "prompt-v1",
		ArtifactDestination: "workspace:reports", EvidenceSnapshot: snapshot, RecallCoverage: coverage,
	}
	request.RequestDigest, err = insightsRequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Approval = InsightsOpportunitiesApproval{
		ApprovalID: "approval-1", ApprovalKind: insightsApprovalDirectOnce, ApprovedBy: "member-1", ApprovedAt: end,
		RequestRevisionDigest: request.RequestDigest, EvidenceSnapshotDigest: snapshot.SnapshotID, RecallCoverageDigest: coverage.Digest,
		ProcessVersion: insightsOpportunitiesProcessVersion, PromptVersion: request.PromptVersion,
		ArtifactDestination: request.ArtifactDestination, Action: toolAuthorityWorkspaceWrite,
	}
	request.Approval.WorkspaceWriteDigest, err = insightsWorkspaceActionDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func resignInsightsRequest(t *testing.T, request *InsightsOpportunitiesRequest) {
	t.Helper()
	var err error
	request.RecallCoverage.Digest, err = request.RecallCoverage.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	request.RequestDigest, err = insightsRequestDigest(*request)
	if err != nil {
		t.Fatal(err)
	}
	request.Approval.RequestRevisionDigest = request.RequestDigest
	request.Approval.EvidenceSnapshotDigest = request.EvidenceSnapshot.SnapshotID
	request.Approval.RecallCoverageDigest = request.RecallCoverage.Digest
	request.Approval.WorkspaceWriteDigest, err = insightsWorkspaceActionDigest(*request)
	if err != nil {
		t.Fatal(err)
	}
}

func addInsightsRequestSource(t *testing.T, request *InsightsOpportunitiesRequest, evidenceID, objectID string, status RecallSourceStatus) {
	t.Helper()
	start := request.EvidenceSnapshot.Temporal.StartUTC.Add(time.Minute)
	digest := digestBrainString("body for " + evidenceID)
	request.EvidenceSnapshot.Sources = append(request.EvidenceSnapshot.Sources, RetrievalSnapshotSource{
		EvidenceID: evidenceID,
		Evidence: BrainEvidenceRef{
			TenantID: request.TenantID, SourceFamily: "transcript", ObjectID: objectID,
			ContentRevision: 1, ACLVersion: 1, ContentDigest: digest,
			OccurredStart: start, OccurredEnd: start.Add(time.Minute), PurgeGeneration: request.EvidenceSnapshot.PurgeGeneration, Trust: BrainEvidenceTrusted,
		},
	})
	var err error
	request.EvidenceSnapshot.SnapshotID, err = request.EvidenceSnapshot.CanonicalID()
	if err != nil {
		t.Fatal(err)
	}
	request.RecallCoverage.SnapshotID = request.EvidenceSnapshot.SnapshotID
	request.RecallCoverage.Sources = append(request.RecallCoverage.Sources, RecallSourceCoverage{SourceFamily: "transcript", ObjectID: objectID, ContentDigest: digest, Status: status})
	request.RecallCoverage.AuthorizedSources++
	switch status {
	case RecallSourceFresh:
		request.RecallCoverage.FreshSources++
	case RecallSourcePartial:
		request.RecallCoverage.PartialSources++
	case RecallSourceStale:
		request.RecallCoverage.StaleSources++
	case RecallSourceMissing:
		request.RecallCoverage.MissingSources++
	case RecallSourceFailed:
		request.RecallCoverage.FailedSources++
	case RecallSourceOmitted:
		request.RecallCoverage.OmittedSources++
	}
	request.RecallCoverage.Status = deriveRecallCoverageStatus(request.RecallCoverage)
	if request.RecallCoverage.Status == RecallCoverageComplete {
		request.RecallCoverage.Reason = ""
	} else {
		request.RecallCoverage.Reason = "fixture includes non-fresh primary evidence"
	}
	resignInsightsRequest(t, request)
}

func validInsightsReport(t *testing.T, request InsightsOpportunitiesRequest) InsightsOpportunitiesReport {
	t.Helper()
	claimText := "The customer asked for a pilot."
	claimDigest, err := CanonicalStateDigest(struct {
		Text string `json:"text"`
	}{claimText})
	if err != nil {
		t.Fatal(err)
	}
	report := InsightsOpportunitiesReport{
		Schema: insightsOpportunitiesReportSchema, ReportID: "report-1", RunID: request.RunID, Revision: 1, Terminal: true, RequestDigest: request.RequestDigest,
		EvidenceSnapshotID: request.EvidenceSnapshot.SnapshotID, EvidenceSnapshotDigest: request.EvidenceSnapshot.SnapshotID,
		RecallCoverageDigest: request.RecallCoverage.Digest, RecallCoverageStatus: request.RecallCoverage.Status,
		RecallCoverageReason:      request.RecallCoverage.Reason,
		ContainsUntrustedEvidence: request.EvidenceSnapshot.HasUntrustedEvidence(), ProcessVersion: insightsOpportunitiesProcessVersion,
		PromptVersion: request.PromptVersion, ArtifactDestination: request.ArtifactDestination,
		GeneratedAt: time.Date(2026, time.July, 22, 17, 0, 0, 0, time.UTC), ActualRoute: insightsOpportunitiesStaticRoute(),
		RunMetadata: InsightsOpportunitiesRunMetadata{
			OrchestrationProvider: "anthropic", GenerationProvider: "anthropic", ReviewProvider: "anthropic", PromptVersion: request.PromptVersion,
			Retries: 0, CriticOutcome: insightsCriticAccept,
			Usage: map[string]InsightsOpportunitiesUsage{
				"orchestration": {InputTokens: 1, OutputTokens: 1}, "generation": {InputTokens: 1, OutputTokens: 1}, "review": {InputTokens: 1, OutputTokens: 1},
			},
		},
		Claims: []InsightsOpportunitiesClaim{{ClaimID: "claim-1", State: BrainClaimAsserted, Text: claimText, AssertionDigest: claimDigest, EvidenceIDs: []string{"evidence-1"}}},
		Opportunities: []InsightsOpportunity{{
			OpportunityID: "opportunity-1", ClaimIDs: []string{"claim-1"}, EvidenceIDs: []string{"evidence-1"}, Confidence: .8,
			ExpectedImpact: "Validate demand.", RecommendedNextAction: "Schedule a pilot review.", ProposedOwner: "member-1", DecisionStatus: insightsDecisionProposed,
		}},
	}
	report.ReportDigest, err = insightsReportDigest(report)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func validInsightsCriticVerdict(t *testing.T, report InsightsOpportunitiesReport, request InsightsOpportunitiesRequest) InsightsOpportunitiesCriticVerdict {
	t.Helper()
	verdict := InsightsOpportunitiesCriticVerdict{
		Schema: insightsOpportunitiesCriticSchema, VerdictID: "verdict-1", ReviewerID: request.PrincipalID, RunID: report.RunID, ReportID: report.ReportID,
		ReportDigest: report.ReportDigest, EvidenceSnapshotDigest: request.EvidenceSnapshot.SnapshotID, Route: insightsOpportunitiesStaticRoute().Review, Outcome: insightsCriticAccept,
		Findings: []InsightsOpportunitiesCriticFinding{
			{TargetType: insightsTargetClaim, TargetID: "claim-1", Verdict: insightsCriticAccept, EvidenceIDs: []string{"evidence-1"}},
			{TargetType: insightsTargetOpportunity, TargetID: "opportunity-1", Verdict: insightsCriticAccept, EvidenceIDs: []string{"evidence-1"}},
		},
	}
	var err error
	verdict.VerdictDigest, err = insightsCriticVerdictDigest(verdict)
	if err != nil {
		t.Fatal(err)
	}
	return verdict
}

func insightsAuthorizationFixture(request InsightsOpportunitiesRequest) (ACLPrincipal, AuthorizationKernel, BrainPurgeGenerationResolver) {
	principal := ACLPrincipal{TenantID: request.TenantID, ID: request.PrincipalID, Kind: request.PrincipalKind}
	ref, revision := request.EvidenceSnapshot.Sources[0].Evidence.ACLRefs()
	object := ACLObject{Ref: ref, CurrentContentRevision: revision.ContentRevision, CurrentContentDigest: revision.ContentDigest}
	store := &MemoryACLStore{Objects: map[string]ACLObject{aclObjectKey(ref): object}, Grants: map[string][]ACLGrant{}}
	store.Grants[aclObjectKey(ref)] = []ACLGrant{{
		ID: "read-source", TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
		SubjectKind: ACLSubjectPrincipal, SubjectID: principal.ID, SubjectPrincipalKind: principal.Kind, Actions: []ACLAction{ACLReadContent},
	}}
	return principal, AuthorizationKernel{Store: store}, insightsTestPurgeResolver{generation: request.EvidenceSnapshot.PurgeGeneration}
}

func TestInsightsOpportunitiesClosedRequestAndCanonicalDigests(t *testing.T) {
	request := validInsightsRequest(t)
	if err := request.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeInsightsOpportunitiesRequest(raw); err != nil {
		t.Fatalf("closed decoder rejected valid request: %v", err)
	}
	unknown := strings.TrimSuffix(string(raw), "}") + `,"actorCanWriteDestination":true}`
	if _, err := DecodeInsightsOpportunitiesRequest([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("self-attested authority field err=%v, want strict rejection", err)
	}

	tampered := request
	tampered.PromptVersion = "prompt-v2"
	if err := tampered.Validate(); err == nil || !strings.Contains(err.Error(), "requestDigest") {
		t.Fatalf("tampered request err=%v, want canonical digest rejection", err)
	}
	tampered = request
	tampered.RecallCoverage.Digest = strings.Repeat("d", 64)
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered coverage digest was accepted")
	}
	tampered = request
	tampered.Approval.WorkspaceWriteDigest = strings.Repeat("e", 64)
	if err := tampered.Validate(); err == nil || !strings.Contains(err.Error(), "approval does not bind") {
		t.Fatalf("tampered action digest err=%v, want rejection", err)
	}
}

func TestInsightsOpportunitiesAuthorityIsServerVerified(t *testing.T) {
	request := validInsightsRequest(t)
	principal, kernel, purgeResolver := insightsAuthorizationFixture(request)
	if _, err := request.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, nil); err == nil || !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("nil verifier err=%v, want fail-closed", err)
	}
	verifier := &insightsTestAuthorizationVerifier{}
	receipt, err := request.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, verifier)
	if err != nil {
		t.Fatalf("server-authorized request rejected: %v", err)
	}
	if err := request.ResumeAuthorized(context.Background(), principal, kernel, purgeResolver, verifier, receipt); err != nil {
		t.Fatalf("durable request checkpoint did not resume: %v", err)
	}
	if receipt.CheckpointID == "" || receipt.RunID != request.RunID || !isHexDigest(receipt.BindingDigest) {
		t.Fatalf("approval consumption lost restart handle: %+v", receipt)
	}
	tamperedReceipt := receipt
	tamperedReceipt.BindingDigest = strings.Repeat("f", 64)
	if err := request.ResumeAuthorized(context.Background(), principal, kernel, purgeResolver, verifier, tamperedReceipt); err == nil {
		t.Fatal("tampered restart receipt was accepted")
	}
	if len(verifier.actions) != 1 || verifier.actions[0] != ACLWrite {
		t.Fatalf("authorization actions=%v, want workspace write", verifier.actions)
	}
	if len(verifier.requirements) != 1 || verifier.requirements[0] != insightsRequirementActiveOrganizationMember {
		t.Fatalf("authorization requirements=%v, want active organization membership", verifier.requirements)
	}
	if len(verifier.consumeTargets) != 1 {
		t.Fatalf("approval consumptions=%d, want one", len(verifier.consumeTargets))
	}
	consumedTarget := verifier.consumeTargets[0]
	if consumedTarget.ApprovalID != request.Approval.ApprovalID || consumedTarget.ApprovalKind != request.Approval.ApprovalKind ||
		!consumedTarget.ApprovedAt.Equal(request.Approval.ApprovedAt) || consumedTarget.ActorID != request.Approval.ApprovedBy ||
		consumedTarget.ContentDigest != request.RequestDigest || consumedTarget.EvidenceSnapshotDigest != request.EvidenceSnapshot.SnapshotID ||
		consumedTarget.RequestRevisionDigest != request.RequestDigest ||
		consumedTarget.RecallCoverageDigest != request.RecallCoverage.Digest || consumedTarget.ProcessVersion != request.Approval.ProcessVersion ||
		consumedTarget.PromptVersion != request.PromptVersion || consumedTarget.Action != toolAuthorityWorkspaceWrite ||
		consumedTarget.WorkspaceWriteDigest != request.Approval.WorkspaceWriteDigest || consumedTarget.ArtifactDestination != request.ArtifactDestination {
		t.Fatalf("approval consumption target lost binding: %+v", consumedTarget)
	}
	if _, err := request.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, verifier); err == nil || !strings.Contains(err.Error(), "replayed") {
		t.Fatalf("approval replay err=%v, want atomic consume-once rejection", err)
	}
	mismatch := principal
	mismatch.ID = "someone-else"
	if _, err := request.ValidateAuthorized(context.Background(), mismatch, kernel, purgeResolver, verifier); err == nil {
		t.Fatal("mismatched authenticated principal was accepted")
	}
	denied := &insightsTestAuthorizationVerifier{deny: true}
	if _, err := request.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, denied); err == nil {
		t.Fatal("denied server authorization was accepted")
	}
	membershipDenied := &insightsTestAuthorizationVerifier{denyRequirements: map[string]bool{insightsRequirementActiveOrganizationMember: true}}
	if _, err := request.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, membershipDenied); err == nil || !strings.Contains(err.Error(), insightsRequirementActiveOrganizationMember) {
		t.Fatalf("request membership denial err=%v, want independent requirement failure", err)
	}
	if _, err := request.ValidateAuthorized(context.Background(), principal, kernel, insightsTestPurgeResolver{generation: 4}, verifier); err == nil || !strings.Contains(err.Error(), "reauthorization") {
		t.Fatalf("stale purge generation err=%v, want reauthorization failure", err)
	}

	mismatchCases := []struct {
		name   string
		mutate func(*InsightsOpportunitiesAuthorizationTarget)
	}{
		{"request digest", func(target *InsightsOpportunitiesAuthorizationTarget) {
			target.RequestRevisionDigest = strings.Repeat("9", 64)
		}},
		{"snapshot digest", func(target *InsightsOpportunitiesAuthorizationTarget) {
			target.EvidenceSnapshotDigest = strings.Repeat("a", 64)
		}},
		{"coverage digest", func(target *InsightsOpportunitiesAuthorizationTarget) {
			target.RecallCoverageDigest = strings.Repeat("b", 64)
		}},
		{"process version", func(target *InsightsOpportunitiesAuthorizationTarget) { target.ProcessVersion++ }},
		{"prompt version", func(target *InsightsOpportunitiesAuthorizationTarget) { target.PromptVersion = "other-prompt" }},
		{"action", func(target *InsightsOpportunitiesAuthorizationTarget) { target.Action = codexJobAuthorityExternalWrite }},
		{"workspace digest", func(target *InsightsOpportunitiesAuthorizationTarget) {
			target.WorkspaceWriteDigest = strings.Repeat("d", 64)
		}},
	}
	for _, tc := range mismatchCases {
		t.Run("atomic binding mismatch "+tc.name, func(t *testing.T) {
			expected := consumedTarget
			tc.mutate(&expected)
			mismatchVerifier := &insightsTestAuthorizationVerifier{expectedApproval: &expected}
			if _, err := request.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, mismatchVerifier); err == nil || !strings.Contains(err.Error(), "atomically consumed") {
				t.Fatalf("mismatch err=%v, want fail-closed atomic comparison", err)
			}
		})
	}

	concurrentVerifier := &insightsTestAuthorizationVerifier{expectedApproval: &consumedTarget}
	const attempts = 12
	results := make(chan error, attempts)
	var group sync.WaitGroup
	for index := 0; index < attempts; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := request.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, concurrentVerifier)
			results <- err
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent direct_once successes=%d, want exactly one", successes)
	}
}

func TestInsightsOpportunitiesRejectsForceAcceptAndUnsafeApproval(t *testing.T) {
	request := validInsightsRequest(t)
	request.ForceAccept = true
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "ForceAccept") {
		t.Fatalf("ForceAccept err=%v, want permanent prohibition", err)
	}
	request = validInsightsRequest(t)
	request.Approval.Action = codexJobAuthorityExternalWrite
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "workspace_write") {
		t.Fatalf("external_write approval err=%v, want rejection", err)
	}
	request = validInsightsRequest(t)
	request.Approval.ApprovalKind = "standing"
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "direct") {
		t.Fatalf("standing approval err=%v, want rejection", err)
	}
}

func TestInsightsOpportunitiesReportUsesSharedEvidenceAndCoverage(t *testing.T) {
	request := validInsightsRequest(t)
	report := validInsightsReport(t, request)
	if err := report.Validate(request); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	raw, _ := json.Marshal(report)
	if _, err := DecodeInsightsOpportunitiesReport(raw, request); err != nil {
		t.Fatalf("strict report decoder rejected valid report: %v", err)
	}
	nestedUnknown := strings.Replace(string(raw), `"claimId":`, `"modelAuthority":true,"claimId":`, 1)
	if _, err := DecodeInsightsOpportunitiesReport([]byte(nestedUnknown), request); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("nested report field err=%v, want strict rejection", err)
	}
	if _, err := DecodeInsightsOpportunitiesReport(append(append([]byte(nil), raw...), []byte(` {}`)...), request); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing report JSON err=%v, want rejection", err)
	}
	extraUsage := validInsightsReport(t, request)
	extraUsage.RunMetadata.Usage["unapproved_dynamic_seat"] = InsightsOpportunitiesUsage{InputTokens: 1, OutputTokens: 1}
	extraUsage.ReportDigest, _ = insightsReportDigest(extraUsage)
	if err := extraUsage.Validate(request); err == nil || !strings.Contains(err.Error(), "exactly the pinned route seats") {
		t.Fatalf("extra usage seat err=%v, want closed usage rejection", err)
	}
	report.Claims[0].EvidenceIDs = nil
	if err := report.Validate(request); err == nil || !strings.Contains(err.Error(), "has no evidence") {
		t.Fatalf("unbound asserted claim err=%v, want evidence rejection", err)
	}
	report = validInsightsReport(t, request)
	report.Claims[0].EvidenceIDs = []string{"invented-evidence"}
	if err := report.Validate(request); err == nil || !strings.Contains(err.Error(), "unknown id") {
		t.Fatalf("invented evidence err=%v, want source-bound rejection", err)
	}
	report = validInsightsReport(t, request)
	report.RecallCoverageDigest = strings.Repeat("a", 64)
	if err := report.Validate(request); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("coverage binding err=%v, want rejection", err)
	}
	subset := validInsightsRequest(t)
	subset.RecallCoverage.Sources = nil
	subset.RecallCoverage.AuthorizedSources = 0
	subset.RecallCoverage.FreshSources = 0
	subset.RecallCoverage.Status = RecallCoveragePartial
	subset.RecallCoverage.Reason = "retrieval omitted a source"
	resignInsightsRequest(t, &subset)
	if err := subset.Validate(); err == nil || !strings.Contains(err.Error(), "source counts differ") {
		t.Fatalf("coverage subset err=%v, want exact-set rejection", err)
	}

	failedRequest := validInsightsRequest(t)
	addInsightsRequestSource(t, &failedRequest, "evidence-failed", "meeting-object-failed", RecallSourceFailed)
	failedReport := validInsightsReport(t, failedRequest)
	failedReport.Claims[0].EvidenceIDs = []string{"evidence-failed"}
	if err := failedReport.Validate(failedRequest); err == nil || !strings.Contains(err.Error(), "unknown id") {
		t.Fatalf("failed evidence claim err=%v, want unusable-source rejection", err)
	}
	failedReport = validInsightsReport(t, failedRequest)
	failedReport.Opportunities[0].EvidenceIDs = []string{"evidence-failed"}
	if err := failedReport.Validate(failedRequest); err == nil || !strings.Contains(err.Error(), "unknown id") {
		t.Fatalf("failed evidence opportunity err=%v, want unusable-source rejection", err)
	}

	partialRequest := validInsightsRequest(t)
	addInsightsRequestSource(t, &partialRequest, "evidence-partial", "meeting-object-partial", RecallSourcePartial)
	partialReport := validInsightsReport(t, partialRequest)
	partialReport.Claims[0].State = BrainClaimInferred
	partialReport.Claims[0].EvidenceIDs = []string{"evidence-partial"}
	partialReport.Opportunities[0].EvidenceIDs = []string{"evidence-partial"}
	partialReport.ReportDigest, _ = insightsReportDigest(partialReport)
	if err := partialReport.Validate(partialRequest); err != nil {
		t.Fatalf("explicit partial-evidence policy rejected inferred usable evidence: %v", err)
	}
	partialReport.Claims[0].State = BrainClaimAsserted
	partialReport.ReportDigest, _ = insightsReportDigest(partialReport)
	if err := partialReport.Validate(partialRequest); err == nil || !strings.Contains(err.Error(), "fresh primary") {
		t.Fatalf("partial-only asserted claim err=%v, want fresh-primary rejection", err)
	}
}

func TestInsightsOpportunitiesReportRevisionChainIsBoundedAndTerminal(t *testing.T) {
	request := validInsightsRequest(t)
	first := validInsightsReport(t, request)
	first.RunMetadata.CriticOutcome = insightsCriticRevise
	first.Terminal = false
	first.ReportDigest, _ = insightsReportDigest(first)
	if err := first.Validate(request); err != nil {
		t.Fatalf("valid first revision rejected: %v", err)
	}
	second := first
	second.Revision = 2
	second.ParentReportDigest = first.ReportDigest
	second.RunMetadata.Retries = 1
	second.RunMetadata.CriticOutcome = insightsCriticAccept
	second.Terminal = true
	second.ReportDigest, _ = insightsReportDigest(second)
	if err := second.Validate(request); err != nil {
		t.Fatalf("valid terminal second revision rejected: %v", err)
	}
	missingParent := second
	missingParent.ParentReportDigest = ""
	missingParent.ReportDigest, _ = insightsReportDigest(missingParent)
	if err := missingParent.Validate(request); err == nil || !strings.Contains(err.Error(), "revision chain") {
		t.Fatalf("second revision without parent err=%v", err)
	}
	unbounded := second
	unbounded.Revision = 3
	unbounded.RunMetadata.Retries = 2
	unbounded.ReportDigest, _ = insightsReportDigest(unbounded)
	if err := unbounded.Validate(request); err == nil {
		t.Fatal("third model revision was accepted")
	}
}

func TestInsightsOpportunitiesCriticCheckpointPreventsTerminalRewriteAndResumesExactTransition(t *testing.T) {
	request := validInsightsRequest(t)
	principal, kernel, purgeResolver := insightsAuthorizationFixture(request)
	verifier := &insightsTestAuthorizationVerifier{}

	rejectedReport := validInsightsReport(t, request)
	rejectedReport.RunMetadata.CriticOutcome = insightsCriticReject
	rejectedReport.Terminal = true
	rejectedReport.ReportDigest, _ = insightsReportDigest(rejectedReport)
	rejectedVerdict := validInsightsCriticVerdict(t, rejectedReport, request)
	rejectedVerdict.Outcome = insightsCriticReject
	for index := range rejectedVerdict.Findings {
		rejectedVerdict.Findings[index].Verdict = insightsCriticReject
		rejectedVerdict.Findings[index].EvidenceIDs = nil
		rejectedVerdict.Findings[index].RequiredActions = []string{"stop"}
	}
	rejectedVerdict.VerdictDigest, _ = insightsCriticVerdictDigest(rejectedVerdict)
	if err := rejectedVerdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, verifier, rejectedReport, request); err != nil {
		t.Fatalf("terminal rejection was not checkpointed: %v", err)
	}
	rewritten := validInsightsReport(t, request)
	rewrittenVerdict := validInsightsCriticVerdict(t, rewritten, request)
	if err := rewrittenVerdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, verifier, rewritten, request); err == nil || !strings.Contains(err.Error(), "checkpointed") {
		t.Fatalf("terminal reject-to-accept rewrite err=%v", err)
	}

	resumeVerifier := &insightsTestAuthorizationVerifier{}
	first := validInsightsReport(t, request)
	first.RunMetadata.CriticOutcome = insightsCriticRevise
	first.Terminal = false
	first.ReportDigest, _ = insightsReportDigest(first)
	firstVerdict := validInsightsCriticVerdict(t, first, request)
	firstVerdict.Outcome = insightsCriticRevise
	for index := range firstVerdict.Findings {
		firstVerdict.Findings[index].Verdict = insightsCriticRevise
		firstVerdict.Findings[index].EvidenceIDs = nil
		firstVerdict.Findings[index].RequiredActions = []string{"revise"}
	}
	firstVerdict.VerdictDigest, _ = insightsCriticVerdictDigest(firstVerdict)
	if err := firstVerdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, resumeVerifier, first, request); err != nil {
		t.Fatalf("first revision transition failed: %v", err)
	}
	second := validInsightsReport(t, request)
	second.Revision = 2
	second.ParentReportDigest = first.ReportDigest
	second.RunMetadata.Retries = 1
	second.ReportDigest, _ = insightsReportDigest(second)
	secondVerdict := validInsightsCriticVerdict(t, second, request)
	if err := secondVerdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, resumeVerifier, second, request); err != nil {
		t.Fatalf("bounded second revision transition failed: %v", err)
	}
	if err := secondVerdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, resumeVerifier, second, request); err != nil {
		t.Fatalf("exact checkpoint transition did not resume idempotently: %v", err)
	}
}

func TestInsightsOpportunitiesCriticIsStrictCompleteAndFailClosed(t *testing.T) {
	request := validInsightsRequest(t)
	report := validInsightsReport(t, request)
	verdict := validInsightsCriticVerdict(t, report, request)
	if err := verdict.Validate(report, request); err != nil {
		t.Fatalf("valid verdict rejected: %v", err)
	}
	raw, err := json.Marshal(verdict)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeInsightsOpportunitiesCriticVerdict(raw, report, request); err != nil {
		t.Fatalf("strict verdict decoder rejected valid verdict: %v", err)
	}
	unknown := strings.TrimSuffix(string(raw), "}") + `,"score":10}`
	if _, err := DecodeInsightsOpportunitiesCriticVerdict([]byte(unknown), report, request); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown critic field err=%v, want strict rejection", err)
	}
	acceptCases := []struct {
		name   string
		mutate func(*InsightsOpportunitiesCriticFinding)
	}{
		{"missing evidence ids", func(f *InsightsOpportunitiesCriticFinding) { f.EvidenceIDs = nil }},
		{"declared missing evidence", func(f *InsightsOpportunitiesCriticFinding) { f.MissingEvidence = []string{"customer confirmation"} }},
		{"counterevidence", func(f *InsightsOpportunitiesCriticFinding) { f.CounterevidenceIDs = []string{"evidence-1"} }},
		{"required action", func(f *InsightsOpportunitiesCriticFinding) { f.RequiredActions = []string{"verify this"} }},
	}
	for _, tc := range acceptCases {
		t.Run("accept "+tc.name, func(t *testing.T) {
			inconsistent := validInsightsCriticVerdict(t, report, request)
			tc.mutate(&inconsistent.Findings[0])
			inconsistent.VerdictDigest, _ = insightsCriticVerdictDigest(inconsistent)
			if err := inconsistent.Validate(report, request); err == nil || !strings.Contains(err.Error(), "accepted critic finding") {
				t.Fatalf("inconsistent accept err=%v, want strict rejection", err)
			}
		})
	}

	missing := verdict
	missing.Findings = missing.Findings[:1]
	missing.VerdictDigest, _ = insightsCriticVerdictDigest(missing)
	if err := missing.Validate(report, request); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("missing target err=%v, want exact coverage rejection", err)
	}
	duplicate := verdict
	duplicate.Findings[1] = duplicate.Findings[0]
	duplicate.VerdictDigest, _ = insightsCriticVerdictDigest(duplicate)
	if err := duplicate.Validate(report, request); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate finding err=%v, want rejection", err)
	}

	report.RunMetadata.CriticOutcome = insightsCriticReject
	report.ReportDigest, _ = insightsReportDigest(report)
	reject := validInsightsCriticVerdict(t, report, request)
	reject.Outcome = insightsCriticReject
	reject.Findings[0].Verdict = insightsCriticReject
	reject.Findings[0].RequiredActions = []string{"Remove unsupported claim."}
	reject.Findings[1].Verdict = insightsCriticRevise
	reject.Findings[1].RequiredActions = []string{"Add counterevidence."}
	reject.VerdictDigest, _ = insightsCriticVerdictDigest(reject)
	if err := reject.Validate(report, request); err != nil {
		t.Fatalf("reject precedence verdict rejected: %v", err)
	}
	tampered := reject
	tampered.Findings[1].MissingEvidence = []string{"new material"}
	if err := tampered.Validate(report, request); err == nil || !strings.Contains(err.Error(), "verdictDigest") {
		t.Fatalf("tampered verdict err=%v, want digest rejection", err)
	}
	mismatch := reject
	mismatch.Outcome = insightsCriticRevise
	mismatch.VerdictDigest, _ = insightsCriticVerdictDigest(mismatch)
	if err := mismatch.Validate(report, request); err == nil || !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("weakened aggregate err=%v, want reject precedence", err)
	}

	crossRequest := validInsightsRequest(t)
	addInsightsRequestSource(t, &crossRequest, "evidence-2", "meeting-object-2", RecallSourceFresh)
	crossReport := validInsightsReport(t, crossRequest)
	crossVerdict := validInsightsCriticVerdict(t, crossReport, crossRequest)
	crossVerdict.Findings[0].EvidenceIDs = []string{"evidence-2"}
	crossVerdict.VerdictDigest, _ = insightsCriticVerdictDigest(crossVerdict)
	if err := crossVerdict.Validate(crossReport, crossRequest); err == nil || !strings.Contains(err.Error(), "exact target") {
		t.Fatalf("cross-bound critic evidence err=%v, want target-local rejection", err)
	}

	partialRequest := validInsightsRequest(t)
	addInsightsRequestSource(t, &partialRequest, "evidence-partial", "meeting-object-partial", RecallSourcePartial)
	partialReport := validInsightsReport(t, partialRequest)
	partialReport.Claims[0].EvidenceIDs = []string{"evidence-1", "evidence-partial"}
	partialReport.ReportDigest, _ = insightsReportDigest(partialReport)
	partialVerdict := validInsightsCriticVerdict(t, partialReport, partialRequest)
	partialVerdict.Findings[0].EvidenceIDs = []string{"evidence-partial"}
	partialVerdict.VerdictDigest, _ = insightsCriticVerdictDigest(partialVerdict)
	if err := partialVerdict.Validate(partialReport, partialRequest); err == nil || !strings.Contains(err.Error(), "fresh primary") {
		t.Fatalf("partial-only asserted accept err=%v, want fresh-primary rejection", err)
	}
}

func TestInsightsOpportunitiesCriticRequiresFreshReviewerAuthority(t *testing.T) {
	request := validInsightsRequest(t)
	report := validInsightsReport(t, request)
	verdict := validInsightsCriticVerdict(t, report, request)

	t.Run("authorized", func(t *testing.T) {
		principal, kernel, purgeResolver := insightsAuthorizationFixture(request)
		verifier := &insightsTestAuthorizationVerifier{}
		if err := verdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, verifier, report, request); err != nil {
			t.Fatalf("authorized critic verdict rejected: %v", err)
		}
		if len(verifier.requirements) != 1 || verifier.requirements[0] != insightsRequirementActiveOrganizationMember ||
			len(verifier.actions) != 1 || verifier.actions[0] != ACLApprove {
			t.Fatalf("critic authority requirements=%v actions=%v", verifier.requirements, verifier.actions)
		}
	})

	t.Run("revision drift", func(t *testing.T) {
		principal, kernel, purgeResolver := insightsAuthorizationFixture(request)
		store := kernel.Store.(*MemoryACLStore)
		ref, _ := request.EvidenceSnapshot.Sources[0].Evidence.ACLRefs()
		store.mu.Lock()
		object := store.Objects[aclObjectKey(ref)]
		object.CurrentContentRevision++
		object.CurrentContentDigest = strings.Repeat("d", 64)
		store.Objects[aclObjectKey(ref)] = object
		store.mu.Unlock()
		if err := verdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, &insightsTestAuthorizationVerifier{}, report, request); err == nil || !strings.Contains(err.Error(), "reauthorization") {
			t.Fatalf("revision drift err=%v, want critic reauthorization failure", err)
		}
	})

	t.Run("revoked grant", func(t *testing.T) {
		principal, kernel, purgeResolver := insightsAuthorizationFixture(request)
		store := kernel.Store.(*MemoryACLStore)
		ref, _ := request.EvidenceSnapshot.Sources[0].Evidence.ACLRefs()
		now := time.Now().UTC()
		store.mu.Lock()
		grant := store.Grants[aclObjectKey(ref)][0]
		grant.RevokedAt = &now
		store.Grants[aclObjectKey(ref)] = []ACLGrant{grant}
		store.mu.Unlock()
		if err := verdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, &insightsTestAuthorizationVerifier{}, report, request); err == nil || !strings.Contains(err.Error(), "reauthorization") {
			t.Fatalf("revoked source err=%v, want critic reauthorization failure", err)
		}
	})

	t.Run("purge drift", func(t *testing.T) {
		principal, kernel, _ := insightsAuthorizationFixture(request)
		if err := verdict.ValidateAuthorized(context.Background(), principal, kernel, insightsTestPurgeResolver{generation: request.EvidenceSnapshot.PurgeGeneration + 1}, &insightsTestAuthorizationVerifier{}, report, request); err == nil || !strings.Contains(err.Error(), "reauthorization") {
			t.Fatalf("purge drift err=%v, want critic reauthorization failure", err)
		}
	})

	t.Run("reviewer mismatch", func(t *testing.T) {
		principal, kernel, purgeResolver := insightsAuthorizationFixture(request)
		principal.ID = "different-reviewer"
		if err := verdict.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, &insightsTestAuthorizationVerifier{}, report, request); err == nil || !strings.Contains(err.Error(), "actor") {
			t.Fatalf("reviewer mismatch err=%v, want authenticated actor failure", err)
		}
	})
}

func TestInsightsOpportunitiesFeedbackAndPilotRequireFreshServerAuthority(t *testing.T) {
	request := validInsightsRequest(t)
	report := validInsightsReport(t, request)
	principal, kernel, purgeResolver := insightsAuthorizationFixture(request)
	verifier := &insightsTestAuthorizationVerifier{}
	feedback := InsightsOpportunitiesFeedback{
		Schema: insightsOpportunitiesFeedbackSchema, FeedbackID: "feedback-1", WorkflowID: insightsOpportunitiesProcessID,
		WorkflowVersion: insightsOpportunitiesProcessVersion, RunID: report.RunID, ReportID: report.ReportID, ReportDigest: report.ReportDigest,
		TargetType: insightsTargetOpportunity, TargetID: "opportunity-1", Action: insightsFeedbackRequestRevision,
		Reason: "Need a customer-specific owner.", ActorID: principal.ID, At: time.Now().UTC(), EvidenceIDs: []string{"evidence-1"}, IdempotencyKey: "feedback-key-1",
	}
	feedback.ActionDigest, _ = insightsFeedbackActionDigest(feedback)
	if err := feedback.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, verifier, report, request); err != nil {
		t.Fatalf("authorized feedback rejected: %v", err)
	}
	feedbackRaw, _ := json.Marshal(feedback)
	feedbackWithRole := strings.TrimSuffix(string(feedbackRaw), "}") + `,"actorCanWriteDestination":true}`
	if _, err := DecodeInsightsOpportunitiesFeedback([]byte(feedbackWithRole), report, request); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("feedback authority boolean err=%v, want strict rejection", err)
	}
	tamperedFeedback := feedback
	tamperedFeedback.Reason = "different"
	if err := tamperedFeedback.Validate(report, request); err == nil || !strings.Contains(err.Error(), "actionDigest") {
		t.Fatalf("tampered feedback err=%v, want digest rejection", err)
	}
	if err := feedback.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, nil, report, request); err == nil {
		t.Fatal("feedback accepted without server verifier")
	}
	feedbackMembershipDenied := &insightsTestAuthorizationVerifier{denyRequirements: map[string]bool{insightsRequirementActiveOrganizationMember: true}}
	if err := feedback.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, feedbackMembershipDenied, report, request); err == nil || !strings.Contains(err.Error(), insightsRequirementActiveOrganizationMember) {
		t.Fatalf("feedback membership denial err=%v, want independent requirement failure", err)
	}

	pilot := InsightsOpportunitiesPilotReview{
		Schema: insightsOpportunitiesPilotSchema, PilotReviewID: "pilot-1", RunID: report.RunID, ReportID: report.ReportID, ReportDigest: report.ReportDigest,
		ReviewerID: principal.ID, ReviewedAt: time.Now().UTC(), ReleaseCommit: "abc123", ProcessVersion: insightsOpportunitiesProcessVersion,
		SchemaVersion: insightsOpportunitiesReportSchema, PromptVersion: report.PromptVersion,
		InputManifestDigest: request.RequestDigest, EvidenceManifestDigest: request.EvidenceSnapshot.SnapshotID, Outcome: insightsCriticAccept,
	}
	pilot.ReviewDigest, _ = insightsPilotReviewDigest(pilot)
	verifier.actions = nil
	verifier.requirements = nil
	if err := pilot.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, verifier, report, request); err != nil {
		t.Fatalf("authorized pilot review rejected: %v", err)
	}
	pilotRaw, _ := json.Marshal(pilot)
	pilotWithRole := strings.TrimSuffix(string(pilotRaw), "}") + `,"reviewerRoles":["pilot_reviewer"]}`
	if _, err := DecodeInsightsOpportunitiesPilotReview([]byte(pilotWithRole), report, request); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("pilot self-attested role err=%v, want strict rejection", err)
	}
	if len(verifier.actions) != 2 || verifier.actions[0] != ACLApprove || verifier.actions[1] != ACLWrite {
		t.Fatalf("pilot actions=%v, want approve then write", verifier.actions)
	}
	if len(verifier.requirements) != 2 || verifier.requirements[0] != insightsRequirementActiveOrganizationMember || verifier.requirements[1] != insightsRequirementPilotReviewerRole {
		t.Fatalf("pilot requirements=%v, want independent membership and reviewer-role checks", verifier.requirements)
	}
	pilotMembershipDenied := &insightsTestAuthorizationVerifier{denyRequirements: map[string]bool{insightsRequirementActiveOrganizationMember: true}}
	if err := pilot.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, pilotMembershipDenied, report, request); err == nil || !strings.Contains(err.Error(), insightsRequirementActiveOrganizationMember) {
		t.Fatalf("pilot membership denial err=%v, want independent requirement failure", err)
	}
	pilotRoleDenied := &insightsTestAuthorizationVerifier{denyRequirements: map[string]bool{insightsRequirementPilotReviewerRole: true}}
	if err := pilot.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, pilotRoleDenied, report, request); err == nil || !strings.Contains(err.Error(), insightsRequirementPilotReviewerRole) {
		t.Fatalf("pilot role denial err=%v, want independent requirement failure", err)
	}
	critic := validInsightsCriticVerdict(t, report, request)
	mutatedReport := report
	mutatedReport.GeneratedAt = mutatedReport.GeneratedAt.Add(time.Second)
	mutatedReport.ReportDigest, _ = insightsReportDigest(mutatedReport)
	if err := critic.Validate(mutatedReport, request); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("critic accepted mutated report err=%v", err)
	}
	if err := feedback.Validate(mutatedReport, request); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("feedback accepted mutated report err=%v", err)
	}
	if err := pilot.Validate(mutatedReport, request); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("pilot accepted mutated report err=%v", err)
	}
	pilot.ReviewerID = "someone-else"
	pilot.ReviewDigest, _ = insightsPilotReviewDigest(pilot)
	if err := pilot.ValidateAuthorized(context.Background(), principal, kernel, purgeResolver, verifier, report, request); err == nil {
		t.Fatal("self-attested pilot reviewer was accepted")
	}
}

func TestInsightsOpportunitiesRemainsUnregisteredUntilDedicatedExecutor(t *testing.T) {
	for _, flag := range []string{"", "true"} {
		t.Run("flag="+flag, func(t *testing.T) {
			t.Setenv(insightsOpportunitiesEnabledEnv, flag)
			if _, ok := processByID(insightsOpportunitiesProcessID); ok {
				t.Fatal("W2C process must remain absent from direct lookup")
			}
			for _, group := range buildToolsPayload() {
				for _, tool := range group.Tools {
					if tool.ID == insightsOpportunitiesProcessID {
						t.Fatal("W2C process leaked into public visibility")
					}
				}
			}
		})
	}
	t.Setenv(insightsOpportunitiesEnabledEnv, "true")
	if !insightsOpportunitiesRequested() {
		t.Fatal("intent flag was not observable")
	}
	def := insightsOpportunitiesProcessDefinition()
	if def.Authority != toolAuthorityWorkspaceWrite || !def.Hidden {
		t.Fatalf("inert definition authority/hidden=%q/%v", def.Authority, def.Hidden)
	}
	critic, ok := def.stageByID("critic")
	if !ok || critic.GateSpec == nil || critic.GateSpec.ForceAccept {
		t.Fatalf("critic ForceAccept=%+v, want permanently false", critic.GateSpec)
	}
	if err := registerProcessDefinition(def); err == nil || !strings.Contains(err.Error(), "dedicated W2C executor") {
		t.Fatalf("generic W2C registration err=%v, want fail-closed refusal", err)
	}
	route := insightsOpportunitiesStaticRoute()
	if route.Orchestration.Model != "claude-fable-5" || route.Orchestration.Effort != "high" || route.Generation.Model != "claude-fable-5" || route.Generation.Effort != "high" || route.Review.Model != "claude-opus-4-8" {
		t.Fatalf("static route drifted: %+v", route)
	}
}
