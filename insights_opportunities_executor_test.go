package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type deterministicInsightsProvider struct {
	t                *testing.T
	mu               sync.Mutex
	outcomes         []string
	orchestrateCalls int
	generateCalls    int
	reviewCalls      int
	mutateReport     func(*InsightsOpportunitiesReport)
}

func (verifier *insightsTestAuthorizationVerifier) RecoverInsightsOpportunitiesApproval(_ context.Context, _ ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget) (InsightsOpportunitiesApprovalConsumption, error) {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	receipt, ok := verifier.receipts[target.ApprovalID]
	want, _ := insightsApprovalTargetDigest(target)
	if !ok || receipt.BindingDigest != want {
		return InsightsOpportunitiesApprovalConsumption{}, errors.New("durable approval receipt not found")
	}
	return receipt, nil
}

func (provider *deterministicInsightsProvider) execution(seat InsightsOpportunitiesRouteSeat, suffix string) InsightsOpportunitiesProviderExecution {
	return InsightsOpportunitiesProviderExecution{
		Provider: "fixture-anthropic", Model: seat.Model, Effort: seat.Effort, Request: "fixture-" + suffix,
		Usage: InsightsOpportunitiesUsage{InputTokens: 11, CachedInputTokens: 3, OutputTokens: 7},
	}
}

func (provider *deterministicInsightsProvider) Orchestrate(_ context.Context, _ InsightsOpportunitiesRequest, _ *InsightsOpportunitiesReport, _ *InsightsOpportunitiesCriticVerdict) (InsightsOpportunitiesOrchestrationPlan, InsightsOpportunitiesProviderExecution, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.orchestrateCalls++
	return InsightsOpportunitiesOrchestrationPlan{Focus: []string{"decision-ready evidence"}, Constraints: []string{"cite only supplied evidence"}}, provider.execution(insightsOpportunitiesStaticRoute().Orchestration, "orchestrate"), nil
}

func (provider *deterministicInsightsProvider) Generate(_ context.Context, request InsightsOpportunitiesRequest, _ InsightsOpportunitiesOrchestrationPlan, revision int, _ *InsightsOpportunitiesReport, _ *InsightsOpportunitiesCriticVerdict) (InsightsOpportunitiesReport, InsightsOpportunitiesProviderExecution, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.generateCalls++
	report := validInsightsReport(provider.t, request)
	report.ReportID = "report-" + string(rune('0'+revision))
	if provider.mutateReport != nil {
		provider.mutateReport(&report)
	}
	return report, provider.execution(insightsOpportunitiesStaticRoute().Generation, "generate"), nil
}

func (provider *deterministicInsightsProvider) Review(_ context.Context, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport) (InsightsOpportunitiesCriticVerdict, InsightsOpportunitiesProviderExecution, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.reviewCalls++
	outcome := insightsCriticAccept
	if len(provider.outcomes) >= report.Revision {
		outcome = provider.outcomes[report.Revision-1]
	}
	verdict := InsightsOpportunitiesCriticVerdict{VerdictID: "verdict-" + string(rune('0'+report.Revision)), Outcome: outcome}
	for _, claim := range report.Claims {
		finding := InsightsOpportunitiesCriticFinding{TargetType: insightsTargetClaim, TargetID: claim.ClaimID, Verdict: outcome}
		if outcome == insightsCriticAccept {
			finding.EvidenceIDs = append([]string(nil), claim.EvidenceIDs...)
		} else {
			finding.RequiredActions = []string{"resolve evidence conflict"}
		}
		verdict.Findings = append(verdict.Findings, finding)
	}
	for _, opportunity := range report.Opportunities {
		finding := InsightsOpportunitiesCriticFinding{TargetType: insightsTargetOpportunity, TargetID: opportunity.OpportunityID, Verdict: outcome}
		if outcome == insightsCriticAccept {
			finding.EvidenceIDs = append([]string(nil), opportunity.EvidenceIDs...)
		} else {
			finding.RequiredActions = []string{"revise the opportunity"}
		}
		verdict.Findings = append(verdict.Findings, finding)
	}
	_ = request
	return verdict, provider.execution(insightsOpportunitiesStaticRoute().Review, "review"), nil
}

type deterministicInsightsWriter struct {
	mu          sync.Mutex
	receipts    map[string]InsightsOpportunitiesWorkspaceWriteReceipt
	calls       int
	mutations   int
	fail        bool
	beforeGuard func()
}

func (writer *deterministicInsightsWriter) WriteInsightsOpportunitiesReport(ctx context.Context, principal ACLPrincipal, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport, authority InsightsOpportunitiesWorkspaceWriteAuthority, key string, guard InsightsOpportunitiesPublicationGuard) (InsightsOpportunitiesWorkspaceWriteReceipt, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.calls++
	if err := authority.validate(insightsPublicationTarget(principal, request, report), key); err != nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, err
	}
	if guard == nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, errors.New("publication guard is required")
	}
	if writer.beforeGuard != nil {
		writer.beforeGuard()
	}
	if err := guard(ctx, true); err != nil {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, err
	}
	if writer.fail {
		return InsightsOpportunitiesWorkspaceWriteReceipt{}, errors.New("fixture writer unavailable")
	}
	if prior, ok := writer.receipts[key]; ok {
		return prior, nil
	}
	if writer.receipts == nil {
		writer.receipts = map[string]InsightsOpportunitiesWorkspaceWriteReceipt{}
	}
	receipt := InsightsOpportunitiesWorkspaceWriteReceipt{IdempotencyKey: key, ArtifactID: "artifact-" + report.RunID, ReportDigest: report.ReportDigest, WrittenAt: time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)}
	writer.receipts[key] = receipt
	writer.mutations++
	return receipt, nil
}

type deterministicInsightsEvidenceReauthorizer struct {
	mu    sync.Mutex
	err   error
	hook  func()
	calls int
}

func (resolver *deterministicInsightsEvidenceReauthorizer) ReauthorizeEvidence(_ context.Context, _ ACLPrincipal, _ []RetrievalSnapshotSource) error {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.calls++
	if resolver.hook != nil {
		resolver.hook()
	}
	return resolver.err
}

func newDeterministicInsightsExecutor(t *testing.T, request InsightsOpportunitiesRequest, provider *deterministicInsightsProvider, verifier *insightsTestAuthorizationVerifier, writer *deterministicInsightsWriter, path string) (*InsightsOpportunitiesExecutor, ACLPrincipal, *MemoryACLStore) {
	t.Helper()
	principal, kernel, purge := insightsAuthorizationFixture(request)
	store, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if verifier == nil {
		verifier = &insightsTestAuthorizationVerifier{}
	}
	if writer == nil {
		writer = &deterministicInsightsWriter{}
	}
	return &InsightsOpportunitiesExecutor{
		Store: store, Kernel: kernel, Purge: purge, Evidence: &deterministicInsightsEvidenceReauthorizer{}, Verifier: verifier, Generation: provider, Review: provider, Writer: writer,
		Now: func() time.Time { return time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC) }, Enabled: func() bool { return true },
	}, principal, kernel.Store.(*MemoryACLStore)
}

func validExecutorFeedback(t *testing.T, run InsightsOpportunitiesRun, principal ACLPrincipal, key string) InsightsOpportunitiesFeedback {
	t.Helper()
	report := run.Reports[len(run.Reports)-1]
	feedback := InsightsOpportunitiesFeedback{
		Schema: insightsOpportunitiesFeedbackSchema, FeedbackID: "feedback-" + key, WorkflowID: insightsOpportunitiesProcessID,
		WorkflowVersion: insightsOpportunitiesProcessVersion, RunID: run.RunID, ReportID: report.ReportID, ReportDigest: report.ReportDigest,
		TargetType: insightsTargetOpportunity, TargetID: report.Opportunities[0].OpportunityID, Action: insightsFeedbackRequestRevision,
		Reason: "Human reviewer requests a tighter next action.", ActorID: principal.ID, At: time.Date(2026, 7, 22, 21, 0, 0, 0, time.UTC),
		EvidenceIDs: []string{"evidence-1"}, IdempotencyKey: key,
	}
	feedback.ActionDigest, _ = insightsFeedbackActionDigest(feedback)
	return feedback
}

func TestInsightsOpportunitiesDedicatedExecutorDeterministicTenScenarioCorpus(t *testing.T) {
	t.Run("conflicting evidence is terminally rejected", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t, outcomes: []string{insightsCriticReject}, mutateReport: func(report *InsightsOpportunitiesReport) {
			report.Opportunities[0].CounterevidenceIDs = []string{"evidence-1"}
		}}
		executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
		run, err := executor.Execute(context.Background(), principal, request)
		if err != nil || run.Status != insightsRunRejected || run.Publication != nil || len(run.Reports) != 1 {
			t.Fatalf("conflict run=%+v err=%v", run, err)
		}
	})

	t.Run("missing evidence fails before checkpoint", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t, mutateReport: func(report *InsightsOpportunitiesReport) { report.Claims[0].EvidenceIDs = nil }}
		executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
		if _, err := executor.Execute(context.Background(), principal, request); err == nil || !strings.Contains(err.Error(), "no evidence") {
			t.Fatalf("missing evidence err=%v", err)
		}
		run, _ := executor.Store.Run(request.RunID)
		if len(run.Candidates) != 0 || len(run.Reports) != 0 {
			t.Fatalf("invalid candidate became durable: %+v", run)
		}
	})

	t.Run("invented source fails closed", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t, mutateReport: func(report *InsightsOpportunitiesReport) { report.Claims[0].EvidenceIDs = []string{"invented-source"} }}
		executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
		if _, err := executor.Execute(context.Background(), principal, request); err == nil || !strings.Contains(err.Error(), "unknown id") {
			t.Fatalf("invented evidence err=%v", err)
		}
	})

	t.Run("stale evidence blocks approval consumption", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t}
		executor, principal, acl := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
		ref, _ := request.EvidenceSnapshot.Sources[0].Evidence.ACLRefs()
		acl.mu.Lock()
		object := acl.Objects[aclObjectKey(ref)]
		object.CurrentContentRevision++
		acl.Objects[aclObjectKey(ref)] = object
		acl.mu.Unlock()
		if _, err := executor.Execute(context.Background(), principal, request); err == nil || !strings.Contains(err.Error(), "reauthorization") {
			t.Fatalf("stale evidence err=%v", err)
		}
	})

	t.Run("revoked evidence blocks execution", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t}
		executor, principal, acl := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
		ref, _ := request.EvidenceSnapshot.Sources[0].Evidence.ACLRefs()
		now := time.Now().UTC()
		acl.mu.Lock()
		grant := acl.Grants[aclObjectKey(ref)][0]
		grant.RevokedAt = &now
		acl.Grants[aclObjectKey(ref)] = []ACLGrant{grant}
		acl.mu.Unlock()
		if _, err := executor.Execute(context.Background(), principal, request); err == nil || !strings.Contains(err.Error(), "reauthorization") {
			t.Fatalf("revoked evidence err=%v", err)
		}
	})

	t.Run("unauthorized feedback is not persisted", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t}
		verifier := &insightsTestAuthorizationVerifier{}
		executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, verifier, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
		run, err := executor.Execute(context.Background(), principal, request)
		if err != nil {
			t.Fatal(err)
		}
		feedback := validExecutorFeedback(t, run, principal, "unauthorized")
		verifier.deny = true
		if _, err := executor.SubmitFeedback(context.Background(), principal, run.RunID, feedback); err == nil {
			t.Fatal("unauthorized feedback was accepted")
		}
		stored, _ := executor.Store.Run(run.RunID)
		if len(stored.Feedback) != 0 {
			t.Fatal("unauthorized feedback became durable")
		}
	})

	t.Run("critic rejection never writes workspace", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t, outcomes: []string{insightsCriticReject}}
		writer := &deterministicInsightsWriter{}
		executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, writer, filepath.Join(t.TempDir(), "runs.jsonl"))
		run, err := executor.Execute(context.Background(), principal, request)
		if err != nil || run.Status != insightsRunRejected || writer.calls != 0 {
			t.Fatalf("reject run=%+v calls=%d err=%v", run, writer.calls, err)
		}
	})

	t.Run("bounded revise creates exactly two immutable revisions and stores human feedback", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t, outcomes: []string{insightsCriticRevise, insightsCriticAccept}}
		executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
		run, err := executor.Execute(context.Background(), principal, request)
		if err != nil || run.Status != insightsRunAccepted || len(run.Reports) != 2 || run.Reports[1].ParentReportDigest != run.Reports[0].ReportDigest {
			t.Fatalf("revision run=%+v err=%v", run, err)
		}
		feedback := validExecutorFeedback(t, run, principal, "human-revision")
		run, err = executor.SubmitFeedback(context.Background(), principal, run.RunID, feedback)
		if err != nil || len(run.Feedback) != 1 || run.Feedback[0].Action != insightsFeedbackRequestRevision {
			t.Fatalf("human feedback run=%+v err=%v", run, err)
		}
		report := run.Reports[len(run.Reports)-1]
		pilot := InsightsOpportunitiesPilotReview{
			Schema: insightsOpportunitiesPilotSchema, PilotReviewID: "pilot-durable-1", RunID: run.RunID, ReportID: report.ReportID, ReportDigest: report.ReportDigest,
			ReviewerID: principal.ID, ReviewedAt: time.Date(2026, 7, 22, 22, 0, 0, 0, time.UTC), ReleaseCommit: "fixture-commit",
			ProcessVersion: insightsOpportunitiesProcessVersion, SchemaVersion: insightsOpportunitiesReportSchema, PromptVersion: report.PromptVersion,
			InputManifestDigest: request.RequestDigest, EvidenceManifestDigest: request.EvidenceSnapshot.SnapshotID, Outcome: insightsCriticAccept,
		}
		pilot.ReviewDigest, _ = insightsPilotReviewDigest(pilot)
		run, err = executor.SubmitPilotReview(context.Background(), principal, run.RunID, pilot)
		if err != nil || len(run.PilotReviews) != 1 {
			t.Fatalf("durable pilot run=%+v err=%v", run, err)
		}
		reopened, err := OpenInsightsOpportunitiesRunStore(executor.Store.path)
		if err != nil {
			t.Fatal(err)
		}
		replayed, _ := reopened.Run(run.RunID)
		if len(replayed.Feedback) != 1 || len(replayed.PilotReviews) != 1 || reopened.PilotQualification("fixture-commit", report.PromptVersion).Qualified {
			t.Fatalf("durable typed review replay=%+v qualification=%+v", replayed, reopened.PilotQualification("fixture-commit", report.PromptVersion))
		}
	})

	t.Run("restart resumes publication without regenerating or reconsuming", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t}
		verifier := &insightsTestAuthorizationVerifier{}
		writer := &deterministicInsightsWriter{fail: true}
		path := filepath.Join(t.TempDir(), "runs.jsonl")
		executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, verifier, writer, path)
		// Simulate power loss after the server-durable direct-once consume but
		// before the local approval checkpoint. Execute must recover the exact
		// receipt; consuming a second approval is forbidden.
		if _, _, err := executor.Store.startIntent(request, principal, time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		if _, err := request.ValidateAuthorized(context.Background(), principal, executor.Kernel, executor.Purge, verifier); err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Execute(context.Background(), principal, request); err == nil {
			t.Fatal("fixture writer outage did not interrupt execution")
		}
		writer.fail = false
		reopened, err := OpenInsightsOpportunitiesRunStore(path)
		if err != nil {
			t.Fatal(err)
		}
		executor.Store = reopened
		run, err := executor.Execute(context.Background(), principal, request)
		if err != nil || run.Status != insightsRunAccepted || provider.generateCalls != 1 || len(verifier.consumeTargets) != 1 {
			t.Fatalf("resumed run=%+v generate=%d consumes=%d err=%v", run, provider.generateCalls, len(verifier.consumeTargets), err)
		}
	})

	t.Run("duplicate invocation and feedback are idempotent", func(t *testing.T) {
		request := validInsightsRequest(t)
		provider := &deterministicInsightsProvider{t: t}
		verifier := &insightsTestAuthorizationVerifier{}
		writer := &deterministicInsightsWriter{}
		executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, verifier, writer, filepath.Join(t.TempDir(), "runs.jsonl"))
		first, err := executor.Execute(context.Background(), principal, request)
		if err != nil {
			t.Fatal(err)
		}
		second, err := executor.Execute(context.Background(), principal, request)
		if err != nil || second.Publication == nil || first.Publication.ReportDigest != second.Publication.ReportDigest || provider.generateCalls != 1 || writer.calls != 1 || len(verifier.consumeTargets) != 1 {
			t.Fatalf("duplicate first=%+v second=%+v gen=%d writes=%d consumes=%d err=%v", first, second, provider.generateCalls, writer.calls, len(verifier.consumeTargets), err)
		}
		feedback := validExecutorFeedback(t, second, principal, "same-key")
		if _, err := executor.SubmitFeedback(context.Background(), principal, second.RunID, feedback); err != nil {
			t.Fatal(err)
		}
		final, err := executor.SubmitFeedback(context.Background(), principal, second.RunID, feedback)
		if err != nil || len(final.Feedback) != 1 {
			t.Fatalf("duplicate feedback count=%d err=%v", len(final.Feedback), err)
		}
	})
}

func TestInsightsOpportunitiesExecutorFlagDefaultsOffAndRejectsWrongActualRoute(t *testing.T) {
	t.Setenv(insightsOpportunitiesEnabledEnv, "")
	request := validInsightsRequest(t)
	provider := &deterministicInsightsProvider{t: t}
	executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
	executor.Enabled = nil
	if _, err := executor.Execute(context.Background(), principal, request); !errors.Is(err, ErrInsightsOpportunitiesDisabled) {
		t.Fatalf("default-off err=%v", err)
	}
	executor.Enabled = func() bool { return true }
	bad := &wrongRouteInsightsProvider{deterministicInsightsProvider: provider}
	executor.Generation, executor.Review = bad, bad
	if _, err := executor.Execute(context.Background(), principal, request); err == nil || !strings.Contains(err.Error(), "pinned orchestration") {
		t.Fatalf("wrong route err=%v", err)
	}
}

func TestInsightsOpportunitiesJournalPreflightKeepsInvalidTransitionReopenable(t *testing.T) {
	request := validInsightsRequest(t)
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	store, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatal(err)
	}
	principal, _, _ := insightsAuthorizationFixture(request)
	if _, _, err := store.startIntent(request, principal, time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	invalid := InsightsOpportunitiesWorkspaceWriteReceipt{
		IdempotencyKey: "invalid-before-approval", ArtifactID: "artifact-1", ReportDigest: strings.Repeat("a", 64),
		WrittenAt: time.Date(2026, 7, 22, 18, 1, 0, 0, time.UTC),
	}
	if err := store.publish(request.RunID, invalid); !errors.Is(err, ErrInsightsOpportunitiesConflict) {
		t.Fatalf("invalid transition err=%v", err)
	}
	approval := InsightsOpportunitiesApprovalConsumption{RunID: request.RunID}
	if err := store.checkpointApproval(request.RunID, approval, time.Date(2026, 7, 22, 18, 2, 0, 0, time.UTC)); err != nil {
		t.Fatalf("valid append after rejected transition: %v", err)
	}
	reopened, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatalf("reopen after invalid transition: %v", err)
	}
	run, ok := reopened.Run(request.RunID)
	if !ok || run.Status != insightsRunAwaitingReport || run.Publication != nil {
		t.Fatalf("replayed run=%+v found=%v", run, ok)
	}
}

func TestInsightsOpportunitiesJournalRejectsProviderCandidateAtReplayLimit(t *testing.T) {
	request := validInsightsRequest(t)
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	store, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatal(err)
	}
	principal, _, _ := insightsAuthorizationFixture(request)
	stamp := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	if _, _, err := store.startIntent(request, principal, stamp); err != nil {
		t.Fatal(err)
	}
	if err := store.checkpointApproval(request.RunID, InsightsOpportunitiesApprovalConsumption{RunID: request.RunID}, stamp.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	oversize := InsightsOpportunitiesCandidate{
		Report: InsightsOpportunitiesReport{
			RunID: request.RunID, Revision: 1,
			Opportunities: []InsightsOpportunity{{OpportunityID: "provider-output", ExpectedImpact: strings.Repeat("x", insightsExecutorJournalMaxEventBytes)}},
		},
		CreatedAt: stamp.Add(2 * time.Minute),
	}
	if err := store.appendCandidate(request.RunID, oversize); !errors.Is(err, ErrInsightsOpportunitiesEventTooLarge) {
		t.Fatalf("oversize provider candidate err=%v", err)
	}
	report := validInsightsReport(t, request)
	small := InsightsOpportunitiesCandidate{Report: report, Verdict: validInsightsCriticVerdict(t, report, request), CreatedAt: stamp.Add(3 * time.Minute)}
	if err := store.appendCandidate(request.RunID, small); err != nil {
		t.Fatalf("small append after oversize rejection: %v", err)
	}
	if _, err := OpenInsightsOpportunitiesRunStore(path); err != nil {
		t.Fatalf("reopen after oversize provider candidate: %v", err)
	}
}

func TestInsightsOpportunitiesPublicationReauthorizesAfterCriticCheckpoint(t *testing.T) {
	request := validInsightsRequest(t)
	provider := &deterministicInsightsProvider{t: t}
	writer := &deterministicInsightsWriter{}
	executor, principal, acl := newDeterministicInsightsExecutor(t, request, provider, nil, writer, filepath.Join(t.TempDir(), "runs.jsonl"))
	stamp := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	if _, _, err := executor.Store.startIntent(request, principal, stamp); err != nil {
		t.Fatal(err)
	}
	receipt, err := request.ValidateAuthorized(context.Background(), principal, executor.Kernel, executor.Purge, executor.Verifier)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Store.checkpointApproval(request.RunID, receipt, stamp.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	run, _ := executor.Store.Run(request.RunID)
	candidate, err := executor.buildCandidate(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Store.appendCandidate(request.RunID, candidate); err != nil {
		t.Fatal(err)
	}
	run, _ = executor.Store.Run(request.RunID)
	if err := executor.checkpointCandidate(context.Background(), principal, run); err != nil {
		t.Fatal(err)
	}

	// Revoke the exact source after the critic transition is durable but before
	// the workspace writer is reached. Publication must fail closed and remain
	// resumable instead of writing from an authorization decision made earlier.
	ref, _ := request.EvidenceSnapshot.Sources[0].Evidence.ACLRefs()
	revokedAt := stamp.Add(4 * time.Minute)
	acl.mu.Lock()
	grant := acl.Grants[aclObjectKey(ref)][0]
	grant.RevokedAt = &revokedAt
	acl.Grants[aclObjectKey(ref)] = []ACLGrant{grant}
	acl.mu.Unlock()

	run, _ = executor.Store.Run(request.RunID)
	if run.Status != insightsRunAwaitingPublication {
		t.Fatalf("checkpoint status=%q", run.Status)
	}
	if _, err := executor.publish(context.Background(), principal, run); err == nil || !strings.Contains(err.Error(), "publication evidence reauthorization failed") {
		t.Fatalf("publication after revocation err=%v", err)
	}
	if writer.calls != 0 {
		t.Fatalf("workspace writer called after revocation: %d", writer.calls)
	}
	stored, _ := executor.Store.Run(request.RunID)
	if stored.Status != insightsRunAwaitingPublication || stored.Publication != nil {
		t.Fatalf("revoked publication changed durable state: %+v", stored)
	}
}

func TestInsightsOpportunitiesJournalRecoversOnlyTornFinalRecord(t *testing.T) {
	request := validInsightsRequest(t)
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	store, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatal(err)
	}
	principal, _, _ := insightsAuthorizationFixture(request)
	if _, _, err := store.startIntent(request, principal, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"version":1,"sequence":2,"type":"approval_checkpoint"`); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	reopened, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatalf("recover torn final record: %v", err)
	}
	if err := reopened.checkpointApproval(request.RunID, InsightsOpportunitiesApprovalConsumption{RunID: request.RunID}, time.Now().UTC()); err != nil {
		t.Fatalf("append after tail recovery: %v", err)
	}
	if _, err := OpenInsightsOpportunitiesRunStore(path); err != nil {
		t.Fatalf("reopen recovered journal: %v", err)
	}
}

func TestInsightsOpportunitiesCrashWindowsResumeCheckpointAndPublication(t *testing.T) {
	request := validInsightsRequest(t)
	provider := &deterministicInsightsProvider{t: t}
	verifier := &insightsTestAuthorizationVerifier{}
	writer := &deterministicInsightsWriter{}
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, verifier, writer, path)
	stamp := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	if _, _, err := executor.Store.startIntent(request, principal, stamp); err != nil {
		t.Fatal(err)
	}
	approval, err := request.ValidateAuthorized(context.Background(), principal, executor.Kernel, executor.Purge, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Store.checkpointApproval(request.RunID, approval, stamp.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	run, _ := executor.Store.Run(request.RunID)
	candidate, err := executor.buildCandidate(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Store.appendCandidate(request.RunID, candidate); err != nil {
		t.Fatal(err)
	}
	run, _ = executor.Store.Run(request.RunID)
	transition, err := candidate.Verdict.CheckpointAuthorized(context.Background(), principal, executor.Kernel, executor.Purge, verifier, candidate.Report, run.Request)
	if err != nil || !transition.Advanced {
		t.Fatalf("server checkpoint transition=%+v err=%v", transition, err)
	}

	// Crash after the server transition but before its local journal event.
	reopened, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatal(err)
	}
	executor.Store = reopened
	if _, err := executor.Execute(context.Background(), principal, request); err != nil {
		t.Fatal(err)
	}
	resumed, _ := executor.Store.Run(request.RunID)
	if len(resumed.Checkpoints) != 1 || !resumed.Checkpoints[0].Resumed || provider.generateCalls != 1 {
		t.Fatalf("checkpoint resume run=%+v generate=%d", resumed, provider.generateCalls)
	}

	// Recreate the publication crash window on a second run: the writer has
	// committed, but workspace_published has not reached the run journal.
	request2 := validInsightsRequest(t)
	request2.RunID = "run-publication-crash"
	request2.RequestID = "request-publication-crash"
	request2.Approval.ApprovalID = "approval-publication-crash"
	resignInsightsRequest(t, &request2)
	path2 := filepath.Join(t.TempDir(), "runs2.jsonl")
	executor2, principal2, _ := newDeterministicInsightsExecutor(t, request2, provider, verifier, writer, path2)
	writer.fail = true
	if _, err := executor2.Execute(context.Background(), principal2, request2); err == nil {
		t.Fatal("expected publication interruption")
	}
	writer.fail = false
	pending, _ := executor2.Store.Run(request2.RunID)
	target, authority, err := executor2.authorizePublication(context.Background(), principal2, request2, pending.Reports[0], digestBrainString(strings.Join([]string{insightsOpportunitiesProcessID, pending.RunID, pending.Reports[0].ReportDigest, pending.Request.Approval.WorkspaceWriteDigest}, "\x00")))
	if err != nil {
		t.Fatal(err)
	}
	key := authority.IdempotencyKey
	if err := authority.validate(target, key); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteInsightsOpportunitiesReport(context.Background(), principal2, request2, pending.Reports[0], authority, key, func(context.Context, bool) error { return nil }); err != nil {
		t.Fatal(err)
	}
	mutationsBefore := writer.mutations
	reopened2, err := OpenInsightsOpportunitiesRunStore(path2)
	if err != nil {
		t.Fatal(err)
	}
	executor2.Store = reopened2
	completed, err := executor2.Execute(context.Background(), principal2, request2)
	if err != nil || completed.Status != insightsRunAccepted || writer.mutations != mutationsBefore {
		t.Fatalf("publication resume run=%+v mutations=%d/%d err=%v", completed, writer.mutations, mutationsBefore, err)
	}
}

func TestInsightsOpportunitiesHumanRevisionFeedbackDrivesAtMostOneRevision(t *testing.T) {
	request := validInsightsRequest(t)
	provider := &deterministicInsightsProvider{t: t, outcomes: []string{insightsCriticAccept, insightsCriticAccept}}
	executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
	run, err := executor.Execute(context.Background(), principal, request)
	if err != nil || len(run.Reports) != 1 {
		t.Fatalf("initial run=%+v err=%v", run, err)
	}
	run, err = executor.SubmitFeedback(context.Background(), principal, run.RunID, validExecutorFeedback(t, run, principal, "revision-one"))
	if err != nil || run.Status != insightsRunAccepted || len(run.Reports) != 2 || provider.generateCalls != 2 || run.Reports[1].ParentReportDigest != run.Reports[0].ReportDigest {
		t.Fatalf("feedback revision run=%+v generate=%d err=%v", run, provider.generateCalls, err)
	}
	run, err = executor.SubmitFeedback(context.Background(), principal, run.RunID, validExecutorFeedback(t, run, principal, "revision-two"))
	if err != nil || len(run.Reports) != 2 || provider.generateCalls != 2 {
		t.Fatalf("bounded feedback run=%+v generate=%d err=%v", run, provider.generateCalls, err)
	}
}

func TestInsightsOpportunitiesConcurrentFirstExecuteHasOneOwner(t *testing.T) {
	request := validInsightsRequest(t)
	provider := &deterministicInsightsProvider{t: t}
	verifier := &insightsTestAuthorizationVerifier{}
	writer := &deterministicInsightsWriter{}
	executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, verifier, writer, filepath.Join(t.TempDir(), "runs.jsonl"))
	const callers = 16
	results := make(chan error, callers)
	for range callers {
		go func() {
			run, err := executor.Execute(context.Background(), principal, request)
			if err == nil && (run.Status != insightsRunAccepted || len(run.Reports) != 1) {
				err = errors.New("concurrent execute returned incomplete run")
			}
			results <- err
		}()
	}
	for range callers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if provider.orchestrateCalls != 1 || provider.generateCalls != 1 || provider.reviewCalls != 1 || writer.mutations != 1 || len(verifier.consumeTargets) != 1 {
		t.Fatalf("provider=%d/%d/%d writer=%d approvals=%d", provider.orchestrateCalls, provider.generateCalls, provider.reviewCalls, writer.mutations, len(verifier.consumeTargets))
	}
}

func TestInsightsOpportunitiesFailedHumanRevisionPreservesTruthfulRevisionOnePublication(t *testing.T) {
	for _, terminalOutcome := range []string{insightsCriticReject, insightsCriticRevise} {
		t.Run(terminalOutcome, func(t *testing.T) {
			request := validInsightsRequest(t)
			provider := &deterministicInsightsProvider{t: t, outcomes: []string{insightsCriticAccept, terminalOutcome}}
			writer := &deterministicInsightsWriter{}
			path := filepath.Join(t.TempDir(), "runs.jsonl")
			executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, writer, path)
			first, err := executor.Execute(context.Background(), principal, request)
			if err != nil || first.Publication == nil || len(first.Publications) != 1 {
				t.Fatalf("first=%+v err=%v", first, err)
			}
			firstReceipt := *first.Publication
			result, err := executor.SubmitFeedback(context.Background(), principal, first.RunID, validExecutorFeedback(t, first, principal, "failed-revision"))
			wantStatus := insightsRunRejected
			if terminalOutcome == insightsCriticRevise {
				wantStatus = insightsRunRevisionExhausted
			}
			if err != nil || result.Status != wantStatus || result.Publication == nil || *result.Publication != firstReceipt || len(result.Publications) != 1 || writer.mutations != 1 {
				t.Fatalf("result=%+v mutations=%d err=%v", result, writer.mutations, err)
			}
			reopened, err := OpenInsightsOpportunitiesRunStore(path)
			if err != nil {
				t.Fatal(err)
			}
			persisted, _ := reopened.Run(request.RunID)
			if persisted.Publication == nil || *persisted.Publication != firstReceipt || len(persisted.Publications) != 1 {
				t.Fatalf("restart lost truthful publication: %+v", persisted)
			}
		})
	}
}

func TestInsightsOpportunitiesGetReauthorizesEvidenceBeforeReturningRun(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv(insightsOpportunitiesEnabledEnv, "true")
	request := validInsightsRequest(t)
	request.PrincipalID = "aj@shareability.com"
	request.EvidenceSnapshot.PrincipalID = request.PrincipalID
	request.EvidenceSnapshot.SnapshotID, _ = request.EvidenceSnapshot.CanonicalID()
	request.RecallCoverage.SnapshotID = request.EvidenceSnapshot.SnapshotID
	request.RecallCoverage.Digest, _ = request.RecallCoverage.CanonicalDigest()
	request.Approval.ApprovedBy = request.PrincipalID
	resignInsightsRequest(t, &request)
	provider := &deterministicInsightsProvider{t: t}
	executor, principal, acl := newDeterministicInsightsExecutor(t, request, provider, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
	if _, err := executor.Execute(context.Background(), principal, request); err != nil {
		t.Fatal(err)
	}
	restore := installInsightsOpportunitiesExecutor(executor)
	t.Cleanup(restore)
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	ref, _ := request.EvidenceSnapshot.Sources[0].Evidence.ACLRefs()
	revokedAt := time.Now().UTC()
	acl.mu.Lock()
	grant := acl.Grants[aclObjectKey(ref)][0]
	grant.RevokedAt = &revokedAt
	acl.Grants[aclObjectKey(ref)] = []ACLGrant{grant}
	acl.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "https://bonfire.test/api/insights-opportunities/v1/runs/"+request.RunID, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	insightsOpportunitiesExecutorHandler(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("revoked evidence GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInsightsOpportunitiesFeedbackReplayResumesRevisionAfterCrash(t *testing.T) {
	request := validInsightsRequest(t)
	provider := &deterministicInsightsProvider{t: t, outcomes: []string{insightsCriticAccept, insightsCriticAccept}}
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, nil, nil, path)
	run, err := executor.Execute(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	feedback := validExecutorFeedback(t, run, principal, "crash-before-revision-execute")
	if err := feedback.ValidateAuthorized(context.Background(), principal, executor.Kernel, executor.Purge, executor.Verifier, run.Reports[0], run.Request); err != nil {
		t.Fatal(err)
	}
	if err := executor.Store.appendFeedback(run.RunID, feedback); err != nil {
		t.Fatal(err)
	}

	// Simulate process loss after the feedback fsync but before SubmitFeedback
	// can call Execute. Replaying the same idempotency key must resume revision 2.
	reopened, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatal(err)
	}
	executor.Store = reopened
	resumed, err := executor.SubmitFeedback(context.Background(), principal, run.RunID, feedback)
	if err != nil || resumed.Status != insightsRunAccepted || len(resumed.Reports) != 2 || provider.generateCalls != 2 {
		t.Fatalf("resumed=%+v generate=%d err=%v", resumed, provider.generateCalls, err)
	}
}

func TestInsightsOpportunitiesPublicationGuardRejectsInterleavedRevocationAndBodyDrift(t *testing.T) {
	for _, scenario := range []struct {
		name string
		arm  func(*insightsTestAuthorizationVerifier, *deterministicInsightsEvidenceReauthorizer)
	}{
		{name: "membership revoked after capability issue", arm: func(verifier *insightsTestAuthorizationVerifier, _ *deterministicInsightsEvidenceReauthorizer) {
			verifier.deny = true
		}},
		{name: "source body changed after capability issue", arm: func(_ *insightsTestAuthorizationVerifier, evidence *deterministicInsightsEvidenceReauthorizer) {
			evidence.err = ErrRetrievalSnapshotStale
		}},
		{name: "org memory consent withdrawn after capability issue", arm: func(_ *insightsTestAuthorizationVerifier, evidence *deterministicInsightsEvidenceReauthorizer) {
			evidence.err = ErrBrainSourceConsentAbsent
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := validInsightsRequest(t)
			provider := &deterministicInsightsProvider{t: t}
			verifier := &insightsTestAuthorizationVerifier{}
			writer := &deterministicInsightsWriter{fail: true}
			executor, principal, _ := newDeterministicInsightsExecutor(t, request, provider, verifier, writer, filepath.Join(t.TempDir(), "runs.jsonl"))
			if _, err := executor.Execute(context.Background(), principal, request); err == nil {
				t.Fatal("expected fixture publication interruption")
			}
			pending, _ := executor.Store.Run(request.RunID)
			writer.fail = false
			writer.beforeGuard = func() {
				scenario.arm(verifier, executor.Evidence.(*deterministicInsightsEvidenceReauthorizer))
			}
			if _, err := executor.publish(context.Background(), principal, pending); err == nil {
				t.Fatal("interleaved authority/source change reached workspace mutation")
			}
			if writer.mutations != 0 {
				t.Fatalf("workspace mutations=%d", writer.mutations)
			}
			stored, _ := executor.Store.Run(request.RunID)
			if stored.Status != insightsRunAwaitingPublication || stored.Publication != nil {
				t.Fatalf("failed guard changed durable run: %+v", stored)
			}
		})
	}
}

func TestProductionInsightsPublicationCapabilityRejectsFabrication(t *testing.T) {
	setupAuthTestEnv(t)
	verifier := &productionInsightsAuthorizationVerifier{}
	for index := range verifier.secret {
		verifier.secret[index] = byte(index + 1)
	}
	principal := ACLPrincipal{TenantID: canonicalTenantID(), ID: "aj@shareability.com", Kind: ACLPrincipalUser}
	target := InsightsOpportunitiesAuthorizationTarget{
		Purpose: "workspace_report_publication", TenantID: principal.TenantID, ResourceType: "workspace_destination", ResourceID: "workspace:insights",
		ArtifactDestination: "workspace:insights", ActorID: principal.ID, Action: toolAuthorityWorkspaceWrite,
		CriticOutcome: insightsCriticAccept, Terminal: true, RunID: "run-capability", ReportDigest: strings.Repeat("a", 64), ReportRevision: 1,
	}
	authority, err := verifier.AuthorizeInsightsOpportunitiesPublication(context.Background(), principal, target, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	replacement := byte('0')
	if authority.AuthorityID[len(authority.AuthorityID)-1] == replacement {
		replacement = '1'
	}
	authority.AuthorityID = authority.AuthorityID[:len(authority.AuthorityID)-1] + string(replacement)
	if err := verifier.VerifyInsightsOpportunitiesPublication(context.Background(), principal, target, "key-1", authority); err == nil {
		t.Fatal("fabricated publication capability was accepted")
	}
	valid, err := verifier.AuthorizeInsightsOpportunitiesPublication(context.Background(), principal, target, "key-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.VerifyInsightsOpportunitiesPublication(context.Background(), principal, target, "key-2", valid); err != nil {
		t.Fatal(err)
	}
	if err := verifier.VerifyInsightsOpportunitiesPublication(context.Background(), principal, target, "key-2", valid); err == nil {
		t.Fatal("consumed production capability authorized a second mutation")
	}
}

func TestInsightsOpportunitiesGetBindsAuthorizationToLoadedRevision(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv(insightsOpportunitiesEnabledEnv, "true")
	request := validInsightsRequest(t)
	request.PrincipalID = "aj@shareability.com"
	request.EvidenceSnapshot.PrincipalID = request.PrincipalID
	request.EvidenceSnapshot.SnapshotID, _ = request.EvidenceSnapshot.CanonicalID()
	request.RecallCoverage.SnapshotID = request.EvidenceSnapshot.SnapshotID
	request.RecallCoverage.Digest, _ = request.RecallCoverage.CanonicalDigest()
	request.Approval.ApprovedBy = request.PrincipalID
	resignInsightsRequest(t, &request)
	verifier := &insightsTestAuthorizationVerifier{}
	executor, principal, _ := newDeterministicInsightsExecutor(t, request, &deterministicInsightsProvider{t: t}, verifier, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
	run, err := executor.Execute(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	loadedDigest := run.Reports[0].ReportDigest
	mutated := false
	verifier.requirementHook = func(requirement string, target InsightsOpportunitiesAuthorizationTarget) {
		if mutated || requirement != insightsRequirementActiveOrganizationMember || target.Purpose != "insights_run_read" {
			return
		}
		mutated = true
		executor.Store.mu.Lock()
		stored := executor.Store.runs[run.RunID]
		revision := stored.Reports[0]
		revision.ReportID, revision.Revision, revision.ParentReportDigest = "report-2", 2, revision.ReportDigest
		revision.GeneratedAt = revision.GeneratedAt.Add(time.Minute)
		revision.ReportDigest = ""
		revision.ReportDigest, _ = insightsReportDigest(revision)
		stored.Reports = append(stored.Reports, revision)
		executor.Store.mu.Unlock()
	}
	restore := installInsightsOpportunitiesExecutor(executor)
	t.Cleanup(restore)
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	req := httptest.NewRequest(http.MethodGet, "https://bonfire.test/api/insights-opportunities/v1/runs/"+run.RunID, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	insightsOpportunitiesExecutorHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var returned InsightsOpportunitiesRun
	if err := json.Unmarshal(recorder.Body.Bytes(), &returned); err != nil {
		t.Fatal(err)
	}
	if len(returned.Reports) != 1 || returned.Reports[0].ReportDigest != loadedDigest {
		t.Fatalf("GET returned revision not bound to authorization: %+v", returned.Reports)
	}
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	var readTarget *InsightsOpportunitiesAuthorizationTarget
	for index := range verifier.targets {
		if verifier.targets[index].Purpose == "insights_run_read" {
			candidate := verifier.targets[index]
			readTarget = &candidate
		}
	}
	if readTarget == nil || readTarget.ReportRevision != 1 || readTarget.ReportDigest != loadedDigest {
		t.Fatalf("read authorization target=%+v", readTarget)
	}
}

func TestInsightsOpportunitiesGetRejectsCurrentBodyDriftAndConsentWithdrawal(t *testing.T) {
	for _, sourceErr := range []error{ErrRetrievalSnapshotStale, ErrBrainSourceConsentAbsent} {
		t.Run(sourceErr.Error(), func(t *testing.T) {
			setupAuthTestEnv(t)
			t.Setenv(insightsOpportunitiesEnabledEnv, "true")
			request := validInsightsRequest(t)
			request.PrincipalID = "aj@shareability.com"
			request.EvidenceSnapshot.PrincipalID = request.PrincipalID
			request.EvidenceSnapshot.SnapshotID, _ = request.EvidenceSnapshot.CanonicalID()
			request.RecallCoverage.SnapshotID = request.EvidenceSnapshot.SnapshotID
			request.RecallCoverage.Digest, _ = request.RecallCoverage.CanonicalDigest()
			request.Approval.ApprovedBy = request.PrincipalID
			resignInsightsRequest(t, &request)
			executor, principal, _ := newDeterministicInsightsExecutor(t, request, &deterministicInsightsProvider{t: t}, nil, nil, filepath.Join(t.TempDir(), "runs.jsonl"))
			if _, err := executor.Execute(context.Background(), principal, request); err != nil {
				t.Fatal(err)
			}
			executor.Evidence.(*deterministicInsightsEvidenceReauthorizer).err = sourceErr
			restore := installInsightsOpportunitiesExecutor(executor)
			t.Cleanup(restore)
			cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
			req := httptest.NewRequest(http.MethodGet, "https://bonfire.test/api/insights-opportunities/v1/runs/"+request.RunID, nil)
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
			recorder := httptest.NewRecorder()
			insightsOpportunitiesExecutorHandler(recorder, req)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestInsightsOpportunitiesFoldRejectsUnknownOutcomesAndIllegalCheckpoints(t *testing.T) {
	request := validInsightsRequest(t)
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	store, err := OpenInsightsOpportunitiesRunStore(path)
	if err != nil {
		t.Fatal(err)
	}
	principal, _, _ := insightsAuthorizationFixture(request)
	stamp := time.Now().UTC()
	if _, _, err := store.startIntent(request, principal, stamp); err != nil {
		t.Fatal(err)
	}
	if err := store.checkpointApproval(request.RunID, InsightsOpportunitiesApprovalConsumption{RunID: request.RunID}, stamp); err != nil {
		t.Fatal(err)
	}
	report := validInsightsReport(t, request)
	verdict := validInsightsCriticVerdict(t, report, request)
	unknown := InsightsOpportunitiesCandidate{Report: report, Verdict: verdict, CreatedAt: stamp}
	unknown.Verdict.Outcome = "invented_outcome"
	unknown.Report.RunMetadata.CriticOutcome = unknown.Verdict.Outcome
	if err := store.appendCandidate(request.RunID, unknown); !errors.Is(err, ErrInsightsOpportunitiesConflict) {
		t.Fatalf("unknown outcome err=%v", err)
	}
	illegal := InsightsOpportunitiesCandidate{Report: report, Verdict: verdict, CreatedAt: stamp}
	illegal.Report.Terminal = false
	illegal.Report.ReportDigest = ""
	illegal.Report.ReportDigest, _ = insightsReportDigest(illegal.Report)
	illegal.Verdict.ReportDigest = illegal.Report.ReportDigest
	if err := store.appendCandidate(request.RunID, illegal); !errors.Is(err, ErrInsightsOpportunitiesConflict) {
		t.Fatalf("illegal terminal combination err=%v", err)
	}
	valid := InsightsOpportunitiesCandidate{Report: report, Verdict: verdict, CreatedAt: stamp}
	if err := store.appendCandidate(request.RunID, valid); err != nil {
		t.Fatal(err)
	}
	if err := store.checkpoint(request.RunID, InsightsOpportunitiesCriticCheckpoint{ReportDigest: report.ReportDigest, VerdictDigest: verdict.VerdictDigest, At: stamp}); !errors.Is(err, ErrInsightsOpportunitiesConflict) {
		t.Fatalf("empty checkpoint id err=%v", err)
	}
	if _, err := OpenInsightsOpportunitiesRunStore(path); err != nil {
		t.Fatalf("invalid folds poisoned journal: %v", err)
	}
}

func TestInsightsOpportunitiesCanonicalFenceBlocksSourceRevokeAndPurgeRaces(t *testing.T) {
	for _, mutation := range []string{"source", "revoke", "purge"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, canonical, registry := migratedPostgresCanonicalStore(t)
			request := validInsightsRequest(t)
			// The isolated canonical fixture starts with an empty purge ledger;
			// bind the fence canary to that exact generation rather than the
			// synthetic generation used by the workflow fixture.
			request.EvidenceSnapshot.PurgeGeneration = 0
			evidence := request.EvidenceSnapshot.Sources[0].Evidence
			payload, payloadDigest, err := NewCanonicalEventPayload(registry, "artifact.revised", 1, map[string]any{
				"artifact_id": evidence.ObjectID, "content_revision": evidence.ContentRevision, "content_sha256": evidence.ContentDigest, "visibility": "organization",
			})
			if err != nil {
				t.Fatal(err)
			}
			event := CanonicalEvent{
				EventID: uuid.New(), TenantID: evidence.TenantID, AggregateType: evidence.SourceFamily, AggregateID: evidence.ObjectID,
				AggregateVersion: 1, EventType: "artifact.revised", SchemaVersion: 1, OccurredAt: time.Now().UTC(), RecordedAt: time.Now().UTC(),
				Actor: CanonicalPrincipalRef{Kind: "service", ID: "insights-test"}, RoomID: evidence.RoomID, MeetingID: evidence.SittingID,
				IdempotencyKey: "insights-authority-v1", Classification: "internal", ACLVersion: evidence.ACLVersion, Payload: payload, PayloadSHA256: payloadDigest,
			}
			if _, err := canonical.Append(ctx, event); err != nil {
				t.Fatal(err)
			}
			grantID := uuid.New()
			if _, err := canonical.pool.Exec(ctx, `INSERT INTO object_grants (
				grant_id,tenant_id,object_type,object_id,acl_version,revision,subject_type,subject_id,action,
				granted_by_type,granted_by_id,conditions
			) VALUES ($1,$2,$3,$4,$5,$6,'user',$7,'read_content','service','test','{}'::jsonb)`,
				grantID, evidence.TenantID, evidence.SourceFamily, evidence.ObjectID, evidence.ACLVersion, evidence.ContentRevision, request.PrincipalID); err != nil {
				t.Fatal(err)
			}
			writer := appInsightsOpportunitiesWorkspaceWriter{postgres: canonical}
			entered, release, fenceDone, mutationDone := make(chan struct{}), make(chan struct{}), make(chan error, 1), make(chan error, 1)
			go func() {
				fenceDone <- writer.withCanonicalInsightsSourceFence(ctx, request, func() error {
					close(entered)
					<-release
					return nil
				})
			}()
			select {
			case <-entered:
			case err := <-fenceDone:
				t.Fatalf("source authority fence failed before commit: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("source authority fence did not reach commit")
			}
			go func() {
				switch mutation {
				case "source":
					next := event
					next.EventID, next.AggregateVersion, next.IdempotencyKey = uuid.New(), 2, "insights-authority-v2"
					next.OccurredAt, next.RecordedAt = time.Now().UTC(), time.Now().UTC()
					nextPayload, nextDigest, payloadErr := NewCanonicalEventPayload(registry, "artifact.revised", 1, map[string]any{
						"artifact_id": evidence.ObjectID, "content_revision": int64(2), "content_sha256": strings.Repeat("d", 64), "visibility": "organization",
					})
					if payloadErr != nil {
						mutationDone <- payloadErr
						return
					}
					next.Payload, next.PayloadSHA256 = nextPayload, nextDigest
					_, appendErr := canonical.Append(ctx, next)
					mutationDone <- appendErr
				case "revoke":
					_, updateErr := canonical.pool.Exec(ctx, `UPDATE object_grants SET revoked_at=now() WHERE grant_id=$1`, grantID)
					mutationDone <- updateErr
				case "purge":
					evidenceMap := map[RetentionResourceClass]string{}
					for _, class := range mandatoryPurgeClasses {
						evidenceMap[class] = "deleted"
					}
					mutationDone <- NewPostgresPurgeLedger(canonical.pool).RecordPurge(ctx, PurgeLedgerEntry{
						Key: RetentionKey{TenantID: evidence.TenantID, ObjectType: evidence.SourceFamily, ObjectID: evidence.ObjectID, RevisionID: "1"}, ContentDigest: evidence.ContentDigest,
						PolicyID: "insights-test", PurgedAt: time.Now().UTC(), DestructionEvidence: evidenceMap,
					})
				}
			}()
			select {
			case err := <-mutationDone:
				t.Fatalf("%s mutation crossed active authority fence: %v", mutation, err)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			if err := <-fenceDone; err != nil {
				t.Fatal(err)
			}
			if err := <-mutationDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInsightsOpportunitiesLocalCommitBlocksPostGuardBodyAndWorkspaceRaces(t *testing.T) {
	for _, mutation := range []string{"body", "workspace"} {
		t.Run(mutation, func(t *testing.T) {
			request := validInsightsRequest(t)
			source := &request.EvidenceSnapshot.Sources[0].Evidence
			sourceBody := "authoritative transcript body"
			source.ContentDigest = digestBrainString(sourceBody)
			dir := t.TempDir()
			store, err := newMeetingMemoryStore(filepath.Join(dir, "meeting-memory.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			source.RoomID = officeRoomID
			source.SittingID = store.ensureMeetingID(officeRoomID)
			if _, appended, err := store.appendEntryForMeeting(source.RoomID, meetingMemoryKindTranscript, source.ObjectID, sourceBody, map[string]string{"roomId": source.RoomID, "meetingId": source.SittingID}, ""); err != nil || !appended {
				t.Fatalf("source append=%v err=%v", appended, err)
			}
			report := validInsightsReport(t, request)
			artifactID := insightsWorkspaceArtifactID(report.RunID)
			metadata := map[string]string{
				"mode": "research", "title": "Insights & Opportunities", "status": "draft", "published": "false", "visibility": "organization",
				"workflow": insightsOpportunitiesProcessID, "workflowVersion": "1", "runId": report.RunID, "requestDigest": request.RequestDigest,
				"reportDigest": report.ReportDigest, "reportRevision": "1", "artifactDestination": request.ArtifactDestination,
				"workspaceWriteDigest": request.Approval.WorkspaceWriteDigest, "idempotencyKey": "local-race", "createdBy": request.PrincipalID, "updatedBy": request.PrincipalID,
			}
			entered, release, mutationDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			guard := func(_ context.Context, bodies bool) error {
				if bodies {
					return errors.New("local conditional commit requested an unlocked body reauthorization")
				}
				close(entered)
				<-release
				return nil
			}
			type commitResult struct {
				receipt InsightsOpportunitiesWorkspaceWriteReceipt
				err     error
			}
			commitDone := make(chan commitResult, 1)
			go func() {
				receipt, _, _, commitErr := store.commitInsightsOpportunitiesArtifact(context.Background(), request, report, artifactID, renderInsightsOpportunitiesReport(report), metadata, "local-race", guard)
				commitDone <- commitResult{receipt: receipt, err: commitErr}
			}()
			select {
			case <-entered:
			case result := <-commitDone:
				t.Fatalf("local conditional commit failed before guard: receipt=%+v err=%v", result.receipt, result.err)
			case <-time.After(5 * time.Second):
				t.Fatal("local conditional commit did not reach guard")
			}
			go func() {
				if mutation == "body" {
					store.mu.Lock()
					for index := range store.entries {
						if store.entries[index].ID == source.ObjectID {
							store.entries[index].Text = "changed after commit boundary"
						}
					}
					store.mu.Unlock()
				} else {
					_, _, _ = store.appendOSArtifact(artifactID, "competing workspace body", map[string]string{"title": "racer"})
				}
				close(mutationDone)
			}()
			select {
			case <-mutationDone:
				t.Fatalf("%s mutation crossed local conditional commit", mutation)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			result := <-commitDone
			if result.err != nil || result.receipt.ReportDigest != report.ReportDigest {
				t.Fatalf("commit receipt=%+v err=%v", result.receipt, result.err)
			}
			<-mutationDone
		})
	}
}

type wrongRouteInsightsProvider struct {
	deterministicInsightsProvider *deterministicInsightsProvider
}

func (provider *wrongRouteInsightsProvider) Orchestrate(ctx context.Context, request InsightsOpportunitiesRequest, report *InsightsOpportunitiesReport, verdict *InsightsOpportunitiesCriticVerdict) (InsightsOpportunitiesOrchestrationPlan, InsightsOpportunitiesProviderExecution, error) {
	plan, execution, err := provider.deterministicInsightsProvider.Orchestrate(ctx, request, report, verdict)
	execution.Model = "invented-model"
	return plan, execution, err
}

func (provider *wrongRouteInsightsProvider) Generate(ctx context.Context, request InsightsOpportunitiesRequest, plan InsightsOpportunitiesOrchestrationPlan, revision int, report *InsightsOpportunitiesReport, verdict *InsightsOpportunitiesCriticVerdict) (InsightsOpportunitiesReport, InsightsOpportunitiesProviderExecution, error) {
	return provider.deterministicInsightsProvider.Generate(ctx, request, plan, revision, report, verdict)
}

func (provider *wrongRouteInsightsProvider) Review(ctx context.Context, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport) (InsightsOpportunitiesCriticVerdict, InsightsOpportunitiesProviderExecution, error) {
	return provider.deterministicInsightsProvider.Review(ctx, request, report)
}
