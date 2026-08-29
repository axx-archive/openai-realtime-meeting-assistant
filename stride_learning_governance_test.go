package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func strideLearningTestRef(kind STRIDEContractType, id, char string) STRIDEReference {
	return STRIDEReference{ContractType: kind, ID: id, Revision: 1, Digest: strings.Repeat(char, 64)}
}

func strideLearningTestCandidate(t *testing.T, id string, scope STRIDELearningScope, scopeID string, sourceScope STRIDELearningScope, sourceAudience, candidateAudience STRIDEAudience, supersedes string, at time.Time) STRIDEReviewedLearningCandidate {
	t.Helper()
	source := strideLearningTestRef(STRIDEContractSourceEpisode, "source-episode-1", "1")
	learning := AgentLearningRecord{
		Header:  STRIDEContractHeader{TenantID: "company-1", ID: "learning-record-" + id, Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractAgentLearningRecord, ContentDigest: strings.Repeat("2", 64), CreatedAt: at},
		AgentID: STRIDEWorkAgentResearcher, Kind: "domain", Subject: scopeID, Scope: string(scope), LessonDigest: strings.Repeat("3", 64),
		Evidence: []STRIDEReference{source}, Confidence: .8, FirstObserved: at, LastObserved: at, ReinforcementCount: 1,
		Audience: candidateAudience, Status: "candidate",
	}
	candidate := STRIDEReviewedLearningCandidate{
		ID: id, TenantID: "company-1", IdempotencyKeyDigest: strings.Repeat("4", 64), Agent: STRIDEWorkAgentResearcher,
		Scope: scope, ScopeID: scopeID, Impact: "procedure", Learning: learning,
		SourceEpisodes: []STRIDEReference{source}, WorkRuns: []STRIDEReference{strideLearningTestRef(STRIDEContractWorkRun, "work-run-1", "5")},
		Outcomes: []STRIDEReference{strideLearningTestRef(STRIDEContractOutcome, "outcome-1", "6")},
		Authority: STRIDELearningAuthorityFence{
			SourceScope: sourceScope, SourceScopeID: scopeID, SourceAudience: sourceAudience, CandidateAudience: candidateAudience,
			ACLRevision: 3, ConsentRevision: 2, PurgeGeneration: 1, SourceAuthorityDigest: strings.Repeat("7", 64), ObservedAt: at,
		},
		SupersedesCandidateID: supersedes, ProposedBy: "system-learning-review", ProposedAt: at,
	}
	fence, err := candidate.FenceDigest()
	if err != nil {
		t.Fatal(err)
	}
	candidate.AuthorityFenceDigest = fence
	return candidate
}

func strideLearningTestEvaluation(t *testing.T, candidate STRIDEReviewedLearningCandidate, before, after STRIDELearningEvalMetrics, at time.Time) STRIDELearningEvaluationReceipt {
	t.Helper()
	candidateDigest, err := candidate.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := STRIDELearningEvaluationReceipt{
		ID: "learning-eval-" + candidate.ID, TenantID: candidate.TenantID, CandidateID: candidate.ID, CandidateDigest: candidateDigest, Agent: candidate.Agent,
		DatasetDigest: strings.Repeat("8", 64), HeldOut: true, SampleCount: 25, Before: before, After: after,
		Evaluator: "system-eval-heldout", EvaluatedAt: at,
	}
	receipt.ReceiptDigest, err = receipt.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func strideLearningTestEvent(candidate STRIDEReviewedLearningCandidate, id, char string, eventType STRIDELearningAuditEventType, actorKind, actor, summary string, at time.Time) STRIDELearningAuditEvent {
	return STRIDELearningAuditEvent{
		ID: id, TenantID: candidate.TenantID, IdempotencyKeyDigest: strings.Repeat(char, 64), Type: eventType, CandidateID: candidate.ID,
		ActorKind: actorKind, Actor: actor, Summary: summary, OccurredAt: at,
	}
}

func appendSTRIDELearningTestEvent(t *testing.T, repository *STRIDELearningAuditRepository, event STRIDELearningAuditEvent) STRIDELearningAuditEvent {
	t.Helper()
	stored, appended, err := repository.Append(event)
	if err != nil || !appended {
		t.Fatalf("append %s appended=%v err=%v", event.Type, appended, err)
	}
	return stored
}

func TestSTRIDELearningCandidateRequiresExactOutcomesAndCannotWidenPrivateScope(t *testing.T) {
	at := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	private := STRIDEAudience{Visibility: "private", Principals: []string{"user-aj"}}
	candidate := strideLearningTestCandidate(t, "candidate-1", STRIDELearningPerson, "user-aj", STRIDELearningPerson, private, private, "", at)
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}

	missingOutcome := candidate
	missingOutcome.Outcomes = nil
	if !errors.Is(missingOutcome.Validate(), ErrSTRIDELearningInvalid) {
		t.Fatal("candidate without exact outcome lineage was accepted")
	}
	widened := candidate
	widened.Authority.CandidateAudience = STRIDEAudience{Visibility: "project", Principals: []string{"user-aj"}}
	widened.AuthorityFenceDigest, _ = widened.FenceDigest()
	if !errors.Is(widened.Validate(), ErrSTRIDELearningInvalid) && !errors.Is(widened.Validate(), ErrSTRIDELearningPrivacy) {
		t.Fatal("private learning widened into project scope")
	}
	company := candidate
	company.Scope, company.ScopeID, company.Learning.Scope, company.Learning.Subject = STRIDELearningCompany, company.TenantID, string(STRIDELearningCompany), company.TenantID
	company.AuthorityFenceDigest, _ = company.FenceDigest()
	if !errors.Is(company.Validate(), ErrSTRIDELearningPrivacy) {
		t.Fatalf("person source widened to company: %v", company.Validate())
	}
	critic := candidate
	critic.Agent, critic.Learning.AgentID = "critic", "critic"
	critic.AuthorityFenceDigest, _ = critic.FenceDigest()
	if !errors.Is(critic.Validate(), ErrSTRIDELearningInvalid) {
		t.Fatal("critic became a learning agent")
	}
}

func TestCompletedWorkLearningEntersGovernedAuditAndCannotUseLegacyApprovalBypass(t *testing.T) {
	app, _, _, work := launchSTRIDEWorkRunIntegrationFixture(t, "research")
	committed, _, err := app.updateOSArtifactWithMetadata(work.Artifact.ID, "", "# Governed result\n\nEvidence-backed result.", STRIDEWorkAgentResearcher, map[string]string{
		"status": "complete", "threadStatus": "complete", "goalStatus": "verified", "latestThreadRun": work.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	app.recordSTRIDEWorkRunTerminal(work, committed, "complete")
	learningID := "learning-governed-work-1"
	proposedAt := time.Now().UTC()
	if err := app.proposeGovernedLearningFromCompletedWork(STRIDEWorkAgentResearcher, learningID, work, committed, "Use primary evidence before drafting the recommendation.", proposedAt); err != nil {
		t.Fatal(err)
	}
	state, err := app.learningAudits.State(learningID)
	if err != nil || state.Status != "candidate" || len(state.Candidate.SourceEpisodes) != 1 || len(state.Candidate.WorkRuns) != 1 || len(state.Candidate.Outcomes) != 1 {
		t.Fatalf("governed candidate=%+v err=%v", state, err)
	}
	agent := STRIDEProductTeamAgent{Learning: []STRIDEProductAgentLearning{{ID: learningID, Origin: "completed_work"}}}
	if err := app.authorizeCompletedWorkLearningResolution(agent, learningID, "approve", "user-aj", proposedAt.Add(time.Minute), nil); !errors.Is(err, ErrSTRIDELearningGate) {
		t.Fatalf("legacy approve bypass error=%v, want governed gate", err)
	}
	evaluation := strideLearningTestEvaluation(t, state.Candidate,
		STRIDELearningEvalMetrics{Quality: .7, Citation: .9, Grounding: .9, Privacy: 1, CostMicros: 1000, LatencyMS: 1000},
		STRIDELearningEvalMetrics{Quality: .8, Citation: .95, Grounding: .95, Privacy: 1, CostMicros: 1000, LatencyMS: 1000}, proposedAt.Add(2*time.Minute))
	policy := STRIDELearningActivationPolicy{MinQualityDelta: .05, MinCitation: .9, MinGrounding: .9, MinPrivacy: 1, MaxCostIncreaseRatio: .1, MaxLatencyIncreaseRatio: .1}
	if err := app.recordGovernedLearningEvaluation(learningID, evaluation, policy, proposedAt.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := app.authorizeCompletedWorkLearningResolution(agent, learningID, "approve", "user-aj", proposedAt.Add(4*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	active, err := app.learningAudits.State(learningID)
	if err != nil || active.Status != "active" {
		t.Fatalf("active state=%+v err=%v", active, err)
	}
	if err := app.authorizeCompletedWorkLearningResolution(agent, learningID, "correct", "user-aj", proposedAt.Add(5*time.Minute), nil); !errors.Is(err, ErrSTRIDELearningGate) {
		t.Fatalf("legacy correction bypass error=%v, want governed gate", err)
	}
	if err := app.authorizeCompletedWorkLearningResolution(agent, learningID, "forget", "user-aj", proposedAt.Add(6*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	forgotten, err := app.learningAudits.State(learningID)
	if err != nil || forgotten.Status != "forgotten" {
		t.Fatalf("forgotten state=%+v err=%v", forgotten, err)
	}
}

func governedCompletedWorkLearningFixture(t *testing.T, suffix string) (*kanbanBoardApp, STRIDEProductTeamAgent, STRIDELearningCandidateState, time.Time) {
	t.Helper()
	app, _, _, work := launchSTRIDEWorkRunIntegrationFixture(t, "research")
	committed, _, err := app.updateOSArtifactWithMetadata(work.Artifact.ID, "", "# Governed result\n\nEvidence-backed result.", STRIDEWorkAgentResearcher, map[string]string{
		"status": "complete", "threadStatus": "complete", "goalStatus": "verified", "latestThreadRun": work.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	app.recordSTRIDEWorkRunTerminal(work, committed, "complete")
	learningID := "learning-authority-" + suffix
	proposedAt := time.Now().UTC()
	if err := app.proposeGovernedLearningFromCompletedWork(STRIDEWorkAgentResearcher, learningID, work, committed, "Keep every recommendation bound to current source authority.", proposedAt); err != nil {
		t.Fatal(err)
	}
	state, err := app.learningAudits.State(learningID)
	if err != nil {
		t.Fatal(err)
	}
	agent := STRIDEProductTeamAgent{Learning: []STRIDEProductAgentLearning{{ID: learningID, Origin: "completed_work", Status: "pending"}}}
	return app, agent, state, proposedAt
}

func qualifyGovernedCompletedWorkLearning(t *testing.T, app *kanbanBoardApp, state STRIDELearningCandidateState, at time.Time) {
	t.Helper()
	evaluation := strideLearningTestEvaluation(t, state.Candidate,
		STRIDELearningEvalMetrics{Quality: .7, Citation: .9, Grounding: .9, Privacy: 1, CostMicros: 1000, LatencyMS: 1000},
		STRIDELearningEvalMetrics{Quality: .8, Citation: .95, Grounding: .95, Privacy: 1, CostMicros: 1000, LatencyMS: 1000}, at.Add(time.Minute))
	policy := STRIDELearningActivationPolicy{MinQualityDelta: .05, MinCitation: .9, MinGrounding: .9, MinPrivacy: 1, MaxCostIncreaseRatio: .1, MaxLatencyIncreaseRatio: .1}
	if err := app.recordGovernedLearningEvaluation(state.Candidate.ID, evaluation, policy, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func invalidateGovernedLearningSource(t *testing.T, app *kanbanBoardApp, state STRIDELearningCandidateState, cause string, at time.Time) {
	t.Helper()
	ref := state.Candidate.SourceEpisodes[0]
	if cause == SourceEpisodeTombstonePurge {
		generation, err := app.sourceEpisodes.CurrentPurgeGeneration(context.Background(), state.Candidate.TenantID)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.sourceEpisodes.AdvanceTenantPurgeGeneration(context.Background(), state.Candidate.TenantID, generation+1, at.UTC()); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := app.tombstoneNativeSourceEpisode(state.Candidate.TenantID, ref.ID, cause, at.UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestGovernedLearningFailsClosedOnACLConsentAndPurgeDriftBeforeEvaluationOrRatification(t *testing.T) {
	for _, cause := range []string{SourceEpisodeTombstoneACL, SourceEpisodeTombstoneConsent, SourceEpisodeTombstonePurge} {
		cause := cause
		t.Run(cause+"_before_evaluation", func(t *testing.T) {
			app, _, state, at := governedCompletedWorkLearningFixture(t, strings.ReplaceAll(cause, "_", "-")+"-eval")
			invalidateGovernedLearningSource(t, app, state, cause, at.Add(time.Minute))
			evaluation := strideLearningTestEvaluation(t, state.Candidate,
				STRIDELearningEvalMetrics{Quality: .7, Citation: .9, Grounding: .9, Privacy: 1, CostMicros: 1000, LatencyMS: 1000},
				STRIDELearningEvalMetrics{Quality: .8, Citation: .95, Grounding: .95, Privacy: 1, CostMicros: 1000, LatencyMS: 1000}, at.Add(2*time.Minute))
			policy := STRIDELearningActivationPolicy{MinQualityDelta: .05, MinCitation: .9, MinGrounding: .9, MinPrivacy: 1, MaxCostIncreaseRatio: .1, MaxLatencyIncreaseRatio: .1}
			if err := app.recordGovernedLearningEvaluation(state.Candidate.ID, evaluation, policy, at.Add(3*time.Minute)); !errors.Is(err, ErrSTRIDELearningPrivacy) {
				t.Fatalf("evaluation after %s error=%v, want privacy fence", cause, err)
			}
			unchanged, err := app.learningAudits.State(state.Candidate.ID)
			if err != nil || unchanged.Evaluation != nil || unchanged.Qualification != nil || unchanged.Status != "candidate" {
				t.Fatalf("evaluation leaked after %s: state=%+v err=%v", cause, unchanged, err)
			}
		})

		t.Run(cause+"_before_ratification", func(t *testing.T) {
			app, agent, state, at := governedCompletedWorkLearningFixture(t, strings.ReplaceAll(cause, "_", "-")+"-ratify")
			qualifyGovernedCompletedWorkLearning(t, app, state, at)
			invalidateGovernedLearningSource(t, app, state, cause, at.Add(3*time.Minute))
			var committed atomic.Bool
			if err := app.authorizeCompletedWorkLearningResolution(agent, state.Candidate.ID, "approve", "user-aj", at.Add(4*time.Minute), func() error {
				committed.Store(true)
				return nil
			}); !errors.Is(err, ErrSTRIDELearningPrivacy) {
				t.Fatalf("ratification after %s error=%v, want privacy fence", cause, err)
			}
			blocked, err := app.learningAudits.State(state.Candidate.ID)
			if err != nil || blocked.Status == "active" || committed.Load() {
				t.Fatalf("ratification leaked after %s: state=%+v committed=%v err=%v", cause, blocked, committed.Load(), err)
			}
		})
	}
}

func TestGovernedLearningAuthorityLeaseSerializesRatificationAgainstRevocationAndExcludesItImmediately(t *testing.T) {
	app, agent, state, at := governedCompletedWorkLearningFixture(t, "ratification-race")
	qualifyGovernedCompletedWorkLearning(t, app, state, at)
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	ratified := make(chan error, 1)
	go func() {
		ratified <- app.authorizeCompletedWorkLearningResolution(agent, state.Candidate.ID, "approve", "user-aj", at.Add(3*time.Minute), func() error {
			close(commitEntered)
			<-releaseCommit
			return nil
		})
	}()
	select {
	case <-commitEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("ratification never entered the authority-held commit")
	}
	revoked := make(chan error, 1)
	go func() {
		ref := state.Candidate.SourceEpisodes[0]
		revoked <- app.tombstoneNativeSourceEpisode(state.Candidate.TenantID, ref.ID, SourceEpisodeTombstoneACL, at.Add(4*time.Minute))
	}()
	select {
	case err := <-revoked:
		t.Fatalf("revocation crossed the ratification lease: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-ratified; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	learning := STRIDEProductAgentLearning{ID: state.Candidate.ID, Origin: "completed_work", Status: "reviewed"}
	if app.completedWorkLearningAdmitted(learning) {
		t.Fatal("revoked active learning remained eligible for provider context")
	}
	if err := app.authorizeCompletedWorkLearningResolution(agent, state.Candidate.ID, "forget", "user-aj", at.Add(5*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	forgotten, err := app.learningAudits.State(state.Candidate.ID)
	if err != nil || forgotten.Status != "forgotten" || app.completedWorkLearningAdmitted(learning) {
		t.Fatalf("forgotten learning remained admitted: state=%+v err=%v", forgotten, err)
	}
}

func TestSTRIDELearningActivationGateUsesHeldOutQualityGroundingPrivacyCostAndLatency(t *testing.T) {
	at := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	project := STRIDEAudience{Visibility: "project", Principals: []string{"user-aj", "user-sam"}}
	candidate := strideLearningTestCandidate(t, "candidate-1", STRIDELearningProject, "project-1", STRIDELearningProject, project, project, "", at)
	before := STRIDELearningEvalMetrics{Quality: .70, Citation: .90, Grounding: .92, Privacy: 1, CostMicros: 1000, LatencyMS: 1000}
	after := STRIDELearningEvalMetrics{Quality: .78, Citation: .94, Grounding: .95, Privacy: 1, CostMicros: 1050, LatencyMS: 1050}
	receipt := strideLearningTestEvaluation(t, candidate, before, after, at.Add(time.Minute))
	policy := STRIDELearningActivationPolicy{MinQualityDelta: .05, MinCitation: .9, MinGrounding: .9, MinPrivacy: 1, MaxCostIncreaseRatio: .1, MaxLatencyIncreaseRatio: .1}
	qualification, err := QualifySTRIDELearningActivation(candidate, receipt, policy, at.Add(2*time.Minute))
	if err != nil || !qualification.Eligible || qualification.Validate() != nil {
		t.Fatalf("qualification=%+v err=%v", qualification, err)
	}

	privacyRegression := receipt
	privacyRegression.After.Privacy = .99
	privacyRegression.ReceiptDigest, _ = privacyRegression.Digest()
	if _, err := QualifySTRIDELearningActivation(candidate, privacyRegression, policy, at.Add(2*time.Minute)); !errors.Is(err, ErrSTRIDELearningGate) {
		t.Fatalf("privacy regression gate error=%v", err)
	}
	groundingRegression := receipt
	groundingRegression.After.Grounding = .91
	groundingRegression.ReceiptDigest, _ = groundingRegression.Digest()
	if _, err := QualifySTRIDELearningActivation(candidate, groundingRegression, policy, at.Add(2*time.Minute)); !errors.Is(err, ErrSTRIDELearningGate) {
		t.Fatalf("grounding regression gate error=%v", err)
	}
	costRegression := receipt
	costRegression.After.CostMicros = 1200
	costRegression.ReceiptDigest, _ = costRegression.Digest()
	if _, err := QualifySTRIDELearningActivation(candidate, costRegression, policy, at.Add(2*time.Minute)); !errors.Is(err, ErrSTRIDELearningGate) {
		t.Fatalf("cost regression gate error=%v", err)
	}
	notHeldOut := receipt
	notHeldOut.HeldOut = false
	notHeldOut.ReceiptDigest, _ = notHeldOut.Digest()
	if _, err := QualifySTRIDELearningActivation(candidate, notHeldOut, policy, at.Add(2*time.Minute)); !errors.Is(err, ErrSTRIDELearningInvalid) {
		t.Fatalf("non-held-out evaluation error=%v", err)
	}
}

func TestSTRIDELearningRequiresHumanReviewAndRatificationBeforeActivation(t *testing.T) {
	at := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	project := STRIDEAudience{Visibility: "project", Principals: []string{"user-aj", "user-sam"}}
	candidate := strideLearningTestCandidate(t, "candidate-1", STRIDELearningProject, "project-1", STRIDELearningProject, project, project, "", at)
	receipt := strideLearningTestEvaluation(t, candidate,
		STRIDELearningEvalMetrics{Quality: .7, Citation: .9, Grounding: .9, Privacy: 1, CostMicros: 1000, LatencyMS: 1000},
		STRIDELearningEvalMetrics{Quality: .8, Citation: .95, Grounding: .95, Privacy: 1, CostMicros: 1000, LatencyMS: 1000}, at.Add(time.Minute))
	policy := STRIDELearningActivationPolicy{MinQualityDelta: .05, MinCitation: .9, MinGrounding: .9, MinPrivacy: 1, MaxCostIncreaseRatio: .1, MaxLatencyIncreaseRatio: .1}
	qualification, err := QualifySTRIDELearningActivation(candidate, receipt, policy, at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewSTRIDELearningAuditRepository(filepath.Join(t.TempDir(), "learning-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	proposed := strideLearningTestEvent(candidate, "audit-proposed", "a", STRIDELearningCandidateProposed, "system", "system-learning-review", "Learning candidate proposed from accepted work", at)
	proposed.Candidate = &candidate
	appendSTRIDELearningTestEvent(t, repository, proposed)
	evaluated := strideLearningTestEvent(candidate, "audit-evaluated", "b", STRIDELearningEvaluationRecorded, "system", "system-eval-heldout", "Held-out before and after evaluation recorded", at.Add(time.Minute))
	evaluated.Evaluation = &receipt
	appendSTRIDELearningTestEvent(t, repository, evaluated)
	qualified := strideLearningTestEvent(candidate, "audit-qualified", "c", STRIDELearningActivationQualified, "system", "system-eval-heldout", "Candidate passed the activation quality gate", at.Add(2*time.Minute))
	qualified.Qualification = &qualification
	appendSTRIDELearningTestEvent(t, repository, qualified)

	candidateDigest, _ := candidate.Digest()
	ratification := STRIDELearningHumanRatification{ID: "ratification-1", CandidateID: candidate.ID, CandidateDigest: candidateDigest, QualificationID: qualification.ID, QualificationDigest: qualification.QualificationDigest, Decision: "approved", RatifiedBy: "user-aj", RatifiedAt: at.Add(3 * time.Minute)}
	activate := strideLearningTestEvent(candidate, "audit-activated", "d", STRIDELearningActivationRatified, "human", "user-aj", "AJ approved the reviewed learning change", at.Add(3*time.Minute))
	activate.Ratification = &ratification
	if _, _, err := repository.Append(activate); !errors.Is(err, ErrSTRIDELearningConflict) {
		t.Fatalf("activation before human review error=%v", err)
	}
	reviewed := strideLearningTestEvent(candidate, "audit-reviewed", "e", STRIDELearningCandidateReviewed, "human", "user-sam", "Sam reviewed the evidence and evaluation", at.Add(3*time.Minute))
	appendSTRIDELearningTestEvent(t, repository, reviewed)
	provider := activate
	provider.ID, provider.IdempotencyKeyDigest, provider.ActorKind, provider.Actor = "audit-provider", strings.Repeat("f", 64), "system", "provider-openai"
	if _, _, err := repository.Append(provider); !errors.Is(err, ErrSTRIDELearningInvalid) {
		t.Fatalf("provider self-activation error=%v", err)
	}
	activate.IdempotencyKeyDigest = strings.Repeat("9", 64)
	appendSTRIDELearningTestEvent(t, repository, activate)
	state, err := repository.State(candidate.ID)
	if err != nil || state.Status != "active" || state.Ratification == nil || state.Ratification.RatifiedBy != "user-aj" {
		t.Fatalf("active state=%+v err=%v", state, err)
	}

	restarted, err := NewSTRIDELearningAuditRepository(repository.path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.State(candidate.ID)
	if err != nil || replayed.Status != "active" || len(replayed.Audit) != 5 {
		t.Fatalf("replayed state=%+v err=%v", replayed, err)
	}
}

func TestSTRIDELearningCorrectionForgetAndSupersessionAreImmutableEvents(t *testing.T) {
	at := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	private := STRIDEAudience{Visibility: "private", Principals: []string{"user-aj"}}
	original := strideLearningTestCandidate(t, "candidate-original", STRIDELearningPerson, "user-aj", STRIDELearningPerson, private, private, "", at)
	replacement := strideLearningTestCandidate(t, "candidate-replacement", STRIDELearningPerson, "user-aj", STRIDELearningPerson, private, private, original.ID, at.Add(time.Minute))
	replacement.IdempotencyKeyDigest = strings.Repeat("a", 64)
	repository, _ := NewSTRIDELearningAuditRepository("")
	proposed := strideLearningTestEvent(original, "audit-proposed", "1", STRIDELearningCandidateProposed, "system", "system-learning-review", "Original private candidate proposed", at)
	proposed.Candidate = &original
	appendSTRIDELearningTestEvent(t, repository, proposed)
	corrected := strideLearningTestEvent(replacement, "audit-corrected", "2", STRIDELearningCandidateCorrected, "human", "user-aj", "AJ corrected the private learning candidate", at.Add(time.Minute))
	corrected.TargetCandidateID, corrected.Candidate = original.ID, &replacement
	appendSTRIDELearningTestEvent(t, repository, corrected)
	originalState, _ := repository.State(original.ID)
	replacementState, _ := repository.State(replacement.ID)
	if originalState.Status != "corrected" || replacementState.Status != "candidate" {
		t.Fatalf("correction original=%s replacement=%s", originalState.Status, replacementState.Status)
	}
	superseded := strideLearningTestEvent(original, "audit-superseded", "3", STRIDELearningCandidateSuperseded, "human", "user-aj", "AJ superseded the original candidate", at.Add(2*time.Minute))
	superseded.ReplacementCandidateID = replacement.ID
	appendSTRIDELearningTestEvent(t, repository, superseded)
	forgotten := strideLearningTestEvent(replacement, "audit-forgotten", "4", STRIDELearningCandidateForgotten, "human", "user-aj", "AJ forgot the replacement candidate", at.Add(3*time.Minute))
	appendSTRIDELearningTestEvent(t, repository, forgotten)
	originalState, _ = repository.State(original.ID)
	replacementState, _ = repository.State(replacement.ID)
	if originalState.Status != "superseded" || replacementState.Status != "forgotten" || replacementState.Ratification != nil {
		t.Fatalf("terminal learning states original=%+v replacement=%+v", originalState, replacementState)
	}
	if len(originalState.Audit) < 3 || len(replacementState.Audit) < 2 {
		t.Fatal("correction, supersession, or forget audit history was lost")
	}
}

func TestSTRIDELearningAuditRejectsForgedGateAndTampering(t *testing.T) {
	at := time.Date(2026, 8, 25, 17, 0, 0, 0, time.UTC)
	audience := STRIDEAudience{Visibility: "project", Principals: []string{"project-1"}}
	candidate := strideLearningTestCandidate(t, "candidate-forged-gate", STRIDELearningProject, "project-1", STRIDELearningProject, audience, audience, "", at)
	before := STRIDELearningEvalMetrics{Quality: .8, Citation: .95, Grounding: .95, Privacy: 1, CostMicros: 100, LatencyMS: 100}
	after := STRIDELearningEvalMetrics{Quality: .9, Citation: .96, Grounding: .94, Privacy: .99, CostMicros: 100, LatencyMS: 100}
	receipt := strideLearningTestEvaluation(t, candidate, before, after, at.Add(time.Minute))
	policy := STRIDELearningActivationPolicy{MinQualityDelta: .05, MinCitation: .9, MinGrounding: .9, MinPrivacy: .99, MaxCostIncreaseRatio: .1, MaxLatencyIncreaseRatio: .1}
	qualification, gateErr := QualifySTRIDELearningActivation(candidate, receipt, policy, at.Add(2*time.Minute))
	if !errors.Is(gateErr, ErrSTRIDELearningGate) {
		t.Fatalf("expected privacy and grounding regression to fail gate, got %v", gateErr)
	}
	qualification.Eligible = true
	qualification.QualificationDigest, _ = qualification.Digest()

	path := filepath.Join(t.TempDir(), "learning-audit.jsonl")
	repository, err := NewSTRIDELearningAuditRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	proposed := strideLearningTestEvent(candidate, "audit-forged-proposed", "a", STRIDELearningCandidateProposed, "system", "system-learning-review", "Researcher proposed a project learning candidate", at)
	proposed.Candidate = &candidate
	appendSTRIDELearningTestEvent(t, repository, proposed)
	evaluated := strideLearningTestEvent(candidate, "audit-forged-evaluated", "b", STRIDELearningEvaluationRecorded, "system", "system-eval-heldout", "Held-out evaluation recorded regressions", at.Add(time.Minute))
	evaluated.Evaluation = &receipt
	appendSTRIDELearningTestEvent(t, repository, evaluated)
	qualified := strideLearningTestEvent(candidate, "audit-forged-qualified", "c", STRIDELearningActivationQualified, "system", "system-eval-heldout", "Forged activation qualification", at.Add(2*time.Minute))
	qualified.Qualification = &qualification
	if _, _, err := repository.Append(qualified); !errors.Is(err, ErrSTRIDELearningConflict) {
		t.Fatalf("forged qualification append error=%v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), "Researcher proposed", "Researcher altered!", 1)
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSTRIDELearningAuditRepository(path); !errors.Is(err, ErrSTRIDELearningInvalid) {
		t.Fatalf("tampered audit reload error=%v", err)
	}
}
