package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type e6Sources struct{ err error }

func (source *e6Sources) SourcesCurrent(context.Context, string, []STRIDEReference) error {
	return source.err
}

type e6Rights struct{ denied string }

func (rights e6Rights) MayApprove(_ context.Context, principal string, _ STRIDESuggestedWorkCard) error {
	if principal == rights.denied {
		return errors.New("denied")
	}
	return nil
}

type e6Agents struct{ err error }

func (agents e6Agents) ReferencesCurrent(context.Context, STRIDEReference, STRIDEReference, STRIDEReference) error {
	return agents.err
}

type e6Activation struct {
	tenant  string
	enabled bool
}

func (authority *e6Activation) RegistryEnabled(tenantID, feature string, _ time.Time) error {
	if !authority.enabled || tenantID != authority.tenant || feature != "stride_work_orchestration" {
		return errors.New("registry denied")
	}
	return nil
}

type e6ExecutionAuthority struct {
	secret  []byte
	issued  map[string]string
	used    map[string]bool
	revoked map[string]bool
}

func newE6ExecutionAuthority() *e6ExecutionAuthority {
	return &e6ExecutionAuthority{secret: []byte("deterministic-test-authority-only"), issued: map[string]string{}, used: map[string]bool{}, revoked: map[string]bool{}}
}

func (authority *e6ExecutionAuthority) issue(kind, binding string) string {
	id := fmt.Sprintf("%s-%d", kind, len(authority.issued)+1)
	mac := hmac.New(sha256.New, authority.secret)
	_, _ = mac.Write([]byte(kind + "\x00" + id + "\x00" + binding))
	token := id + "." + fmt.Sprintf("%x", mac.Sum(nil))
	authority.issued[token] = kind + "\x00" + binding
	return token
}

func (authority *e6ExecutionAuthority) consume(kind, binding, token string) error {
	if authority.revoked[token] || authority.used[token] || authority.issued[token] != kind+"\x00"+binding {
		return errors.New("attestation denied")
	}
	authority.used[token] = true
	return nil
}

func e6QueueBinding(value STRIDEWorkQueueClaimAttestation) string {
	value.Claim.AuthorityReceipt = ""
	return workDigest(value)
}
func e6CheckpointBinding(value STRIDEWorkCheckpointAttestation) string {
	value.Checkpoint.VerifierReceipt = ""
	return workDigest(value)
}
func e6ElevatedBinding(value STRIDEWorkElevatedEffectAttestation) string {
	value.Approval.AuthorityReceipt = ""
	return workDigest(value)
}
func e6CompletionBinding(value STRIDEWorkCompletionAttestation) string {
	value.Receipt = ""
	return workDigest(value)
}

func (authority *e6ExecutionAuthority) ConsumeQueueClaim(_ context.Context, value STRIDEWorkQueueClaimAttestation) error {
	return authority.consume("queue", e6QueueBinding(value), value.Claim.AuthorityReceipt)
}
func (authority *e6ExecutionAuthority) ConsumeCheckpoint(_ context.Context, value STRIDEWorkCheckpointAttestation) error {
	return authority.consume("checkpoint", e6CheckpointBinding(value), value.Checkpoint.VerifierReceipt)
}
func (authority *e6ExecutionAuthority) ConsumeElevatedApproval(_ context.Context, value STRIDEWorkElevatedEffectAttestation) error {
	return authority.consume("effect", e6ElevatedBinding(value), value.Approval.AuthorityReceipt)
}
func (authority *e6ExecutionAuthority) ConsumeCompletion(_ context.Context, value STRIDEWorkCompletionAttestation) error {
	return authority.consume("complete", e6CompletionBinding(value), value.Receipt)
}

func e6Digest(char byte) string { return strings.Repeat(string(char), 64) }

func e6Ref(kind STRIDEContractType, id string, char byte) STRIDEReference {
	return STRIDEReference{ContractType: kind, ID: id, Revision: 1, Digest: e6Digest(char)}
}

func e6Destination(thread string) STRIDEThreadResolution {
	return STRIDEThreadResolution{
		Status: STRIDEThreadReuse, ThreadID: thread, ACLVersion: 7,
		Audience: STRIDEAudience{Visibility: "project", Principals: []string{"member-aj", "member-erick", "project-dog-perfect"}},
	}
}

func e6Service(now *time.Time) STRIDEWorkOrchestrator {
	execution := newE6ExecutionAuthority()
	return STRIDEWorkOrchestrator{
		Enabled: true, TenantID: "tenant-acme", Activation: &e6Activation{tenant: "tenant-acme", enabled: true}, Execution: execution,
		Store: NewSTRIDEWorkOrchestrationStore(), Sources: &e6Sources{}, ApprovalRights: e6Rights{}, Agents: e6Agents{},
		Now: func() time.Time { return *now }, Random: func(buffer []byte) error {
			for index := range buffer {
				buffer[index] = byte(index + 1)
			}
			return nil
		},
	}
}

func e6Admit(t *testing.T, service STRIDEWorkOrchestrator, now time.Time) STRIDEAdmittedWorkIntent {
	t.Helper()
	intent, err := service.AdmitIntent(context.Background(), STRIDEWorkIntentCandidate{
		ID: "intent-dog-report", OutcomeDigest: e6Digest('a'), CreatedAt: now,
		Evidence: []STRIDEWorkEvidence{{
			AdmissionClass: STRIDEWorkEvidenceAuthorizedProjection, Current: true,
			Ref: e6Ref(STRIDEContractAnalysisProjection, "projection-dog-report", 'b'),
			AttributedSource: func() *STRIDEReference {
				value := e6Ref(STRIDEContractMeetingAgentContribution, "mary-contribution", 'c')
				return &value
			}(),
			People: []string{"member-aj", "member-erick", "agent-mary"}, Projects: []string{"dog-perfect"}, Confidence: .96,
		}},
	})
	if err != nil {
		t.Fatalf("admit intent: %v", err)
	}
	return intent
}

func e6Card(t *testing.T, service STRIDEWorkOrchestrator, now time.Time, authority string) STRIDESuggestedWorkCard {
	t.Helper()
	intent := e6Admit(t, service, now)
	card, err := service.ProposeSuggestedWork(STRIDESuggestedWorkCardSpec{
		ID: "suggested-dog-report", IntentID: intent.ID, Destination: e6Destination("thread-dog-perfect"),
		Owner: "member-aj", Reviewer: "member-erick", Authority: authority,
		Budget: STRIDEWorkBudget{MaxCostMicros: 5_000_000, MaxDuration: time.Hour, MaxAttempts: 2},
		DueAt:  now.Add(4 * time.Hour), ExpiresAt: now.Add(2 * time.Hour),
		ApprovalPolicy: STRIDESuggestedWorkApprovalPolicy{
			EligiblePrincipals: []string{"member-aj", "member-erick"}, Quorum: 2, ExpiresAt: now.Add(time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("propose card: %v", err)
	}
	return card
}

func e6Approve(t *testing.T, service STRIDEWorkOrchestrator, card STRIDESuggestedWorkCard) STRIDEDurableWorkRun {
	t.Helper()
	if _, run, err := service.ApproveSuggestedWork(context.Background(), card.ID, card.Revision, "member-aj"); err != nil || run != nil {
		t.Fatalf("first approval: run=%v err=%v", run, err)
	}
	_, run, err := service.ApproveSuggestedWork(context.Background(), card.ID, card.Revision, "member-erick")
	if err != nil || run == nil {
		t.Fatalf("quorum approval: run=%v err=%v", run, err)
	}
	return *run
}

func TestSTRIDEWorkIntentAdmissionRejectsAgentAndSocialLaunchAuthority(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	service := e6Service(&now)
	base := STRIDEWorkIntentCandidate{
		ID: "intent-one", OutcomeDigest: e6Digest('a'), CreatedAt: now,
		Evidence: []STRIDEWorkEvidence{{AdmissionClass: STRIDEWorkEvidenceAuthorizedProjection, Current: true, Ref: e6Ref(STRIDEContractAnalysisProjection, "projection-one", 'b'), People: []string{"member-aj"}, Confidence: .9}},
	}
	for _, admissionClass := range []string{"specialist_output", "dismissal", "social_mention", "thanks", "agent_chatter"} {
		candidate := base
		candidate.ID = "intent-" + admissionClass
		candidate.Evidence = append([]STRIDEWorkEvidence(nil), base.Evidence...)
		candidate.Evidence[0].AdmissionClass = admissionClass
		if _, err := service.AdmitIntent(context.Background(), candidate); !errors.Is(err, ErrSTRIDEWorkAdmissionDenied) {
			t.Fatalf("%s unexpectedly admitted: %v", admissionClass, err)
		}
	}
	direct := base
	direct.ID = "intent-direct-agent"
	direct.Evidence = append([]STRIDEWorkEvidence(nil), base.Evidence...)
	direct.Evidence[0].Ref = e6Ref(STRIDEContractMeetingAgentContribution, "mary-direct", 'c')
	if _, err := service.AdmitIntent(context.Background(), direct); !errors.Is(err, ErrSTRIDEWorkAdmissionDenied) {
		t.Fatalf("direct agent contribution unexpectedly admitted: %v", err)
	}

	intent := e6Admit(t, service, now)
	if len(intent.RelevantPeople) != 2 || strideWorkContainsString(intent.RelevantPeople, "agent-mary") {
		t.Fatalf("agent was selected as relevant human: %#v", intent.RelevantPeople)
	}
	duplicate := base
	duplicate.ID = "intent-deduplicated"
	duplicate.OutcomeDigest = intent.OutcomeDigest
	duplicate.Evidence = []STRIDEWorkEvidence{{AdmissionClass: STRIDEWorkEvidenceAuthorizedProjection, Current: true, Ref: intent.Evidence[0], People: []string{"member-erick", "member-aj"}, Projects: []string{"dog-perfect"}, Confidence: .96}}
	got, err := service.AdmitIntent(context.Background(), duplicate)
	if err != nil || got.ID != intent.ID {
		t.Fatalf("dedupe failed: got=%#v err=%v", got, err)
	}

	conflict := base
	conflict.ID = "intent-counter-conflict"
	conflict.Evidence = append([]STRIDEWorkEvidence(nil), base.Evidence...)
	ref := e6Ref(STRIDEContractConversationEvent, "counter-one", 'd')
	other := ref
	other.Digest = e6Digest('e')
	conflict.Evidence[0].Counterevidence = []STRIDEReference{ref, other}
	if _, err := service.AdmitIntent(context.Background(), conflict); !errors.Is(err, ErrSTRIDEWorkAdmissionDenied) {
		t.Fatalf("conflicting counterevidence unexpectedly admitted: %v", err)
	}
}

func TestSTRIDEThreadResolverNeverFallsBackToGeneral(t *testing.T) {
	general := STRIDEProjectThreadCandidate{ThreadID: "general", ProjectIDs: []string{"dog-perfect"}, ParticipantIDs: []string{"member-aj"}, Authorized: true, Relevant: true, Audience: e6Destination("general").Audience, ACLVersion: 1}
	dog := general
	dog.ThreadID = "thread-dog-perfect"
	resolution := ResolveSTRIDEProjectThread([]string{"dog-perfect"}, []string{"member-aj"}, []STRIDEProjectThreadCandidate{general, dog})
	if resolution.Status != STRIDEThreadReuse || resolution.ThreadID != dog.ThreadID {
		t.Fatalf("wanted exact relevant reuse, got %#v", resolution)
	}
	second := dog
	second.ThreadID = "thread-dog-perfect-two"
	resolution = ResolveSTRIDEProjectThread([]string{"dog-perfect"}, []string{"member-aj"}, []STRIDEProjectThreadCandidate{dog, second})
	if resolution.Status != STRIDEThreadExplicitChoice || len(resolution.Candidates) != 2 {
		t.Fatalf("ambiguous destination was not explicit: %#v", resolution)
	}
	resolution = ResolveSTRIDEProjectThread([]string{"missing"}, []string{"member-aj"}, []STRIDEProjectThreadCandidate{general})
	if resolution.Status != STRIDEThreadCreateRequired || resolution.ThreadID != "" {
		t.Fatalf("missing destination fell back: %#v", resolution)
	}
}

func TestSTRIDEApprovalIsAtomicAndConsumesExactlyOnce(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	service := e6Service(&now)
	card := e6Card(t, service, now, STRIDEWorkAuthorityInternalWrite)
	var wait sync.WaitGroup
	var mutex sync.Mutex
	runIDs := map[string]struct{}{}
	for index := 0; index < 40; index++ {
		principal := "member-aj"
		if index%2 == 1 {
			principal = "member-erick"
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, run, err := service.ApproveSuggestedWork(context.Background(), card.ID, card.Revision, principal)
			if err != nil {
				t.Errorf("approval: %v", err)
				return
			}
			if run != nil {
				mutex.Lock()
				runIDs[run.ID] = struct{}{}
				mutex.Unlock()
			}
		}()
	}
	wait.Wait()
	if len(runIDs) != 1 || len(service.Store.Runs) != 1 {
		t.Fatalf("approval created %d observed/%d stored runs", len(runIDs), len(service.Store.Runs))
	}
	stored := service.Store.Cards[card.ID]
	if stored.Status != "approved" || stored.ConsumedRunID == "" || len(stored.Endorsements) != 2 {
		t.Fatalf("card not atomically consumed: %#v", stored)
	}
}

func TestSTRIDEWorkRunLifecycleQueueEffectsArtifactsAndRestore(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	service := e6Service(&now)
	card := e6Card(t, service, now, STRIDEWorkAuthorityExternalWrite)
	run := e6Approve(t, service, card)
	capability := e6Ref(STRIDEContractAgentCapabilityManifest, "marketing-capability", 'f')
	stage := STRIDEWorkRouteSnapshot{StageID: "research-stage", RouteID: "route-research", RouteRevision: 1, RouteDigest: e6Digest('1'), CapabilityRefs: []STRIDEReference{capability}, Authority: STRIDEWorkAuthorityExternalWrite, MaxCostMicros: 1_000_000, MaxDuration: 10 * time.Minute}
	if _, err := service.SetStageRoute(run.ID, STRIDERunQueued, stage); err != nil {
		t.Fatalf("set route: %v", err)
	}
	if _, err := service.TransitionRun(run.ID, STRIDERunRunning, stage.StageID, ""); !errors.Is(err, ErrSTRIDEWorkEffectApproval) {
		t.Fatalf("elevated stage ran without approval: %v", err)
	}
	storedRun := service.Store.Runs[run.ID]
	authority := service.Execution.(*e6ExecutionAuthority)
	approval := STRIDEElevatedEffectApproval{ID: "effect-approval-one", Revision: 1, TenantID: service.TenantID, RunID: run.ID, StageID: stage.StageID, Authority: stage.Authority, BindingDigest: elevatedEffectBindingDigest(storedRun, stage), ExpiresAt: now.Add(time.Hour), Current: true}
	approval.AuthorityReceipt = authority.issue("effect", e6ElevatedBinding(STRIDEWorkElevatedEffectAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Approval: approval}))
	if err := service.RegisterElevatedEffectApproval(approval); err != nil {
		t.Fatalf("register effect approval: %v", err)
	}
	if err := service.AuthorizeElevatedStage(context.Background(), run.ID, stage.StageID, approval.ID); err != nil {
		t.Fatalf("consume effect approval: %v", err)
	}
	run, err := service.TransitionRun(run.ID, STRIDERunRunning, stage.StageID, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	claim := STRIDEWorkQueueClaim{JobID: "queue-job-one", StageID: stage.StageID, ClaimGeneration: 2, FencingTokenDigest: e6Digest('2'), LeaseExpiresAt: now.Add(time.Minute)}
	claim.AuthorityReceipt = authority.issue("queue", e6QueueBinding(STRIDEWorkQueueClaimAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: claim}))
	if _, err := service.BindQueueClaim(run.ID, claim); err != nil {
		t.Fatalf("bind queue claim: %v", err)
	}
	stale := claim
	stale.ClaimGeneration = 1
	stale.FencingTokenDigest = e6Digest('3')
	stale.AuthorityReceipt = authority.issue("queue", e6QueueBinding(STRIDEWorkQueueClaimAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: stale}))
	if _, err := service.BindQueueClaim(run.ID, stale); !errors.Is(err, ErrSTRIDEWorkState) {
		t.Fatalf("stale fencing generation accepted: %v", err)
	}
	teamAgent := e6Ref(STRIDEContractTeamAgent, "agent-mary", '4')
	assignment := e6Ref(STRIDEContractAgentAssignment, "assignment-mary", '5')
	for _, trigger := range []string{"mention", "thanks", "agent_chatter"} {
		if _, err := service.IssueDelegation(context.Background(), run.ID, stage.StageID, trigger, teamAgent, capability, assignment, []string{"web-search"}, time.Minute, 10); !errors.Is(err, ErrSTRIDEDelegationDenied) {
			t.Fatalf("%s launched delegation: %v", trigger, err)
		}
	}
	delegation, err := service.IssueDelegation(context.Background(), run.ID, stage.StageID, "human_approved_work_run", teamAgent, capability, assignment, []string{"web-search"}, time.Minute, 10)
	if err != nil || delegation.Token == "" || delegation.Grant.MaxDelegationHops != 0 || strings.Contains(workDigest(service.Store.Delegations), delegation.Token) {
		t.Fatalf("delegation boundary failed: token=%q grant=%#v err=%v", delegation.Token, delegation.Grant, err)
	}
	artifact := STRIDEWorkArtifactBinding{ID: "artifact-one", RunID: run.ID, StageID: stage.StageID, Artifact: e6Ref(STRIDEContractOutcome, "report-artifact", '6'), Evidence: run.Evidence, Destination: run.Destination, Audience: run.Destination.Audience, CreatedAt: now}
	if _, err := service.RecordArtifact(artifact); err != nil {
		t.Fatalf("record artifact: %v", err)
	}
	checkpoint := STRIDEWorkCheckpoint{ID: "checkpoint-one", StageID: stage.StageID, Status: "passed", EvidenceDigest: e6Digest('7'), CreatedAt: now}
	checkpoint.VerifierReceipt = authority.issue("checkpoint", e6CheckpointBinding(STRIDEWorkCheckpointAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: *service.Store.Runs[run.ID].QueueClaim, Checkpoint: checkpoint}))
	if _, err := service.AddCheckpoint(run.ID, checkpoint); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, err := service.TransitionRun(run.ID, STRIDERunCompleted, "", ""); !errors.Is(err, ErrSTRIDEWorkState) {
		t.Fatalf("shape-only completion unexpectedly accepted: %v", err)
	}
	completion := STRIDEWorkCompletionAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: *service.Store.Runs[run.ID].QueueClaim, Checkpoints: service.Store.Runs[run.ID].Checkpoints}
	completionReceipt := authority.issue("complete", e6CompletionBinding(completion))
	run, err = service.CompleteRun(context.Background(), run.ID, completionReceipt)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	outcome := STRIDEWorkOutcomeBinding{ID: "outcome-one", RunID: run.ID, Verdict: "accepted", ArtifactIDs: []string{artifact.ID}, Evidence: run.Evidence, Destination: run.Destination, Audience: run.Destination.Audience, Reviewer: run.Reviewer, CompletedAt: now}
	if _, err := service.RecordOutcome(outcome); err != nil {
		t.Fatalf("record outcome: %v", err)
	}
	feedback := STRIDEWorkFeedback{ID: "feedback-rerun-one", RunID: run.ID, Kind: "rerun_request", Author: "member-aj", BodyDigest: e6Digest('8'), CreatedAt: now, Rerun: true}
	if _, err := service.AddFeedback(feedback); err != nil {
		t.Fatalf("record rerun feedback: %v", err)
	}
	if _, err := service.ProposeSuggestedWork(STRIDESuggestedWorkCardSpec{
		ID: "suggested-dog-report-rerun", IntentID: card.IntentID, Destination: run.Destination,
		Owner: run.Owner, Reviewer: run.Reviewer, Authority: run.Authority,
		Budget: run.Budget, DueAt: now.Add(4 * time.Hour), ExpiresAt: now.Add(2 * time.Hour),
		ApprovalPolicy: STRIDESuggestedWorkApprovalPolicy{EligiblePrincipals: []string{run.Owner, run.Reviewer}, Quorum: 2, ExpiresAt: now.Add(time.Hour)},
		ParentRunID:    run.ID, ParentFeedbackID: feedback.ID,
	}); err != nil {
		t.Fatalf("propose typed rerun: %v", err)
	}
	payload, digest, err := service.Store.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored, err := RestoreSTRIDEWorkOrchestrationStore(payload, digest)
	if err != nil || len(restored.Runs) != 1 || len(restored.Artifacts) != 1 || len(restored.Delegations) != 1 {
		t.Fatalf("restore: store=%#v err=%v", restored, err)
	}
	payload[len(payload)-1] ^= 1
	if _, err := RestoreSTRIDEWorkOrchestrationStore(payload, digest); !errors.Is(err, ErrSTRIDEWorkSnapshotInvalid) {
		t.Fatalf("tampered snapshot restored: %v", err)
	}
}

func TestSTRIDEInvalidationRequiresCurrentSourceReauthorization(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	service := e6Service(&now)
	card := e6Card(t, service, now, STRIDEWorkAuthorityInternalWrite)
	run := e6Approve(t, service, card)
	stage := STRIDEWorkRouteSnapshot{StageID: "draft-stage", RouteID: "route-draft", RouteRevision: 1, RouteDigest: e6Digest('8'), CapabilityRefs: []STRIDEReference{e6Ref(STRIDEContractAgentCapabilityManifest, "draft-capability", '9')}, Authority: STRIDEWorkAuthorityInternalWrite, MaxCostMicros: 1000, MaxDuration: time.Minute}
	if _, err := service.SetStageRoute(run.ID, STRIDERunQueued, stage); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransitionRun(run.ID, STRIDERunRunning, stage.StageID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InvalidateSources(run.ID, "source was edited", run.Evidence[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransitionRun(run.ID, STRIDERunRunning, stage.StageID, ""); !errors.Is(err, ErrSTRIDEWorkState) {
		t.Fatalf("invalidated run resumed: %v", err)
	}
	sources := service.Sources.(*e6Sources)
	sources.err = errors.New("revoked")
	if _, err := service.ReauthorizeRunSources(context.Background(), run.ID); !errors.Is(err, ErrSTRIDEWorkSourceChanged) {
		t.Fatalf("revoked source reauthorized: %v", err)
	}
	sources.err = nil
	if _, err := service.ReauthorizeRunSources(context.Background(), run.ID); err != nil {
		t.Fatalf("reauthorize: %v", err)
	}
	if _, err := service.TransitionRun(run.ID, STRIDERunRunning, stage.StageID, ""); err != nil {
		t.Fatalf("explicit resume: %v", err)
	}
}

func TestSTRIDEAuthorityAttestationsRejectShapeForgeryReplayRevocationStaleAndCrossTenant(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	service := e6Service(&now)
	card := e6Card(t, service, now, STRIDEWorkAuthorityInternalWrite)
	run := e6Approve(t, service, card)
	stage := STRIDEWorkRouteSnapshot{StageID: "draft-stage", RouteID: "route-draft", RouteRevision: 1, RouteDigest: e6Digest('a'), CapabilityRefs: []STRIDEReference{e6Ref(STRIDEContractAgentCapabilityManifest, "draft-capability", 'b')}, Authority: STRIDEWorkAuthorityInternalWrite, MaxCostMicros: 1000, MaxDuration: time.Minute}
	if _, err := service.SetStageRoute(run.ID, STRIDERunQueued, stage); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransitionRun(run.ID, STRIDERunRunning, stage.StageID, ""); err != nil {
		t.Fatal(err)
	}
	authority := service.Execution.(*e6ExecutionAuthority)
	claim := STRIDEWorkQueueClaim{JobID: "queue-job-authority", StageID: stage.StageID, ClaimGeneration: 1, FencingTokenDigest: e6Digest('c'), LeaseExpiresAt: now.Add(time.Minute), AuthorityReceipt: "forged"}
	if _, err := service.BindQueueClaim(run.ID, claim); !errors.Is(err, ErrSTRIDEWorkAuthority) {
		t.Fatalf("forged claim err=%v", err)
	}
	cross := claim
	cross.AuthorityReceipt = authority.issue("queue", e6QueueBinding(STRIDEWorkQueueClaimAttestation{TenantID: "tenant-other", RunID: run.ID, Stage: stage, Claim: cross}))
	if _, err := service.BindQueueClaim(run.ID, cross); !errors.Is(err, ErrSTRIDEWorkAuthority) {
		t.Fatalf("cross-tenant claim err=%v", err)
	}
	revoked := claim
	revoked.AuthorityReceipt = authority.issue("queue", e6QueueBinding(STRIDEWorkQueueClaimAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: revoked}))
	authority.revoked[revoked.AuthorityReceipt] = true
	if _, err := service.BindQueueClaim(run.ID, revoked); !errors.Is(err, ErrSTRIDEWorkAuthority) {
		t.Fatalf("revoked claim err=%v", err)
	}
	stale := claim
	stale.LeaseExpiresAt = now
	stale.AuthorityReceipt = authority.issue("queue", e6QueueBinding(STRIDEWorkQueueClaimAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: stale}))
	if _, err := service.BindQueueClaim(run.ID, stale); !errors.Is(err, ErrSTRIDEWorkState) {
		t.Fatalf("stale claim err=%v", err)
	}
	claim.AuthorityReceipt = authority.issue("queue", e6QueueBinding(STRIDEWorkQueueClaimAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: claim}))
	if _, err := service.BindQueueClaim(run.ID, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BindQueueClaim(run.ID, claim); !errors.Is(err, ErrSTRIDEWorkState) {
		t.Fatalf("queue receipt replay err=%v", err)
	}

	checkpoint := STRIDEWorkCheckpoint{ID: "checkpoint-authority", StageID: stage.StageID, Status: "passed", EvidenceDigest: e6Digest('d'), CreatedAt: now, VerifierReceipt: "forged"}
	if _, err := service.AddCheckpoint(run.ID, checkpoint); !errors.Is(err, ErrSTRIDEWorkAuthority) {
		t.Fatalf("forged checkpoint err=%v", err)
	}
	checkpoint.VerifierReceipt = authority.issue("checkpoint", e6CheckpointBinding(STRIDEWorkCheckpointAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: *service.Store.Runs[run.ID].QueueClaim, Checkpoint: checkpoint}))
	if _, err := service.AddCheckpoint(run.ID, checkpoint); err != nil {
		t.Fatal(err)
	}
	completion := STRIDEWorkCompletionAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: *service.Store.Runs[run.ID].QueueClaim, Checkpoints: service.Store.Runs[run.ID].Checkpoints}
	if _, err := service.CompleteRun(context.Background(), run.ID, "forged"); !errors.Is(err, ErrSTRIDEWorkAuthority) {
		t.Fatalf("forged completion err=%v", err)
	}
	completionReceipt := authority.issue("complete", e6CompletionBinding(completion))
	if _, err := service.CompleteRun(context.Background(), run.ID, completionReceipt); err != nil {
		t.Fatal(err)
	}
}

func TestSTRIDEEnabledBooleanCannotBypassRegistryAuthority(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	service := e6Service(&now)
	card := e6Card(t, service, now, STRIDEWorkAuthorityInternalWrite)
	otherTenant := service
	otherTenant.TenantID = "tenant-other"
	otherTenant.Activation = &e6Activation{tenant: "tenant-other", enabled: true}
	if _, _, err := otherTenant.ApproveSuggestedWork(context.Background(), card.ID, card.Revision, "member-aj"); !errors.Is(err, ErrSTRIDEWorkApproval) {
		t.Fatalf("cross-tenant approval accepted: %v", err)
	}
	service.Activation.(*e6Activation).enabled = false
	if _, err := service.AdmitIntent(context.Background(), STRIDEWorkIntentCandidate{}); !errors.Is(err, ErrSTRIDEWorkDisabled) {
		t.Fatalf("disabled registry bypassed by boolean: %v", err)
	}
	service.Activation = nil
	if _, err := service.AdmitIntent(context.Background(), STRIDEWorkIntentCandidate{}); !errors.Is(err, ErrSTRIDEWorkDisabled) {
		t.Fatalf("missing registry bypassed by boolean: %v", err)
	}
}
