package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type strideLeadShadowTestArtifacts struct {
	staged    int
	committed int
	delivered int
	last      STRIDELeadCandidateArtifactRequest
}

func (adapter *strideLeadShadowTestArtifacts) StageCandidate(_ context.Context, request STRIDELeadCandidateArtifactRequest) (STRIDELeadCandidateArtifactReceipt, error) {
	adapter.staged++
	adapter.last = request
	bodyDigest := sha256Hex([]byte(strings.TrimSpace(request.Body)))
	receipt := STRIDELeadCandidateArtifactReceipt{
		RunID: request.Run.ID, AssignmentID: request.Assignment.ID, ProviderResponseID: request.Provider.ResponseID,
		OutputKind: request.Run.OutputKind, ArtifactType: artifactTypeMarkdown,
		Artifact:   STRIDEReference{ContractType: STRIDEContractOutcome, ID: "shadow-candidate-1", Revision: 1, Digest: bodyDigest},
		StorageRef: "shadow-candidate:shadow-candidate-1", BodyDigest: bodyDigest,
		NativeRenderValidated: true, OpenValidated: true, EditabilityValidated: true, ValidatedAt: request.ObservedAt,
	}
	receipt.ValidationReceiptDigest = sha256Hex([]byte("validated\x00" + bodyDigest))
	return receipt, nil
}

func (adapter *strideLeadShadowTestArtifacts) CommitCandidateToDrive(_ context.Context, _ STRIDELeadCandidateArtifactReceipt, _ STRIDELeadBenchmarkPromotion) (STRIDELeadDriveCommitReceipt, error) {
	adapter.committed++
	return STRIDELeadDriveCommitReceipt{}, errors.New("shadow candidate must not enter Drive")
}

func (adapter *strideLeadShadowTestArtifacts) DeliverCandidateToChannel(_ context.Context, _ STRIDELeadDriveCommitReceipt, _ string, _ STRIDELeadBenchmarkPromotion) (STRIDELeadChannelDeliveryReceipt, error) {
	adapter.delivered++
	return STRIDELeadChannelDeliveryReceipt{}, errors.New("shadow candidate must not enter the channel")
}

func TestSTRIDELeadShadowFactoryRequiresDedicatedAdminConfiguration(t *testing.T) {
	t.Setenv(strideLeadHarnessShadowEnvironment, "true")
	t.Setenv(strideLeadShadowAPIKeyEnvironment, "")
	t.Setenv(strideLeadShadowProjectEnvironment, "")
	t.Setenv(strideLeadShadowSpendCeilingEnvironment, "")
	repository, err := NewSTRIDEWorkRunRepository("")
	if err != nil {
		t.Fatal(err)
	}
	if runtime, err := newSTRIDELeadShadowRuntimeFromEnvironment(repository); runtime != nil || !errors.Is(err, ErrSTRIDELeadHarnessInvalid) {
		t.Fatalf("missing admin config runtime=%v err=%v", runtime, err)
	}
	t.Setenv(strideLeadShadowAPIKeyEnvironment, "dedicated-shadow-key")
	t.Setenv(strideLeadShadowProjectEnvironment, "project_shadow_1")
	t.Setenv(strideLeadShadowSpendCeilingEnvironment, "0")
	if runtime, err := newSTRIDELeadShadowRuntimeFromEnvironment(repository); runtime != nil || !errors.Is(err, ErrSTRIDELeadHarnessInvalid) {
		t.Fatalf("zero ceiling runtime=%v err=%v", runtime, err)
	}
	t.Setenv(strideLeadShadowSpendCeilingEnvironment, "25")
	t.Setenv(strideLeadShadowCandidateDirEnvironment, filepath.Join(t.TempDir(), "private-candidates"))
	runtime, err := newSTRIDELeadShadowRuntimeFromEnvironment(repository)
	if err != nil || runtime == nil || runtime.SpendCeiling != 25 || runtime.Promotion != nil {
		t.Fatalf("configured runtime=%+v err=%v", runtime, err)
	}
}

func TestAcceptedPublicWorkLaunchesShadowAsynchronouslyAndRestartRecoversCandidateWithoutPublishing(t *testing.T) {
	t.Setenv(strideLeadHarnessShadowEnvironment, "true")
	t.Setenv(strideLeadShadowProjectEnvironment, "project_shadow_test")
	at := time.Date(2026, 8, 25, 23, 0, 0, 0, time.UTC)
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindWorkstream, IntentOutcome: string(conversationIntentApprovalRequired), EffectClass: "expanded_audience",
		Mode: "research", Objective: "Research the launch plan with exact cited evidence", Query: source.Text,
		Lane: approvalLaneStandard, WeightLabel: scoutProposalWeightQuickPass, Summary: "Research prepared", Status: "accepted",
	}
	var err error
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: "proposal-shadow-lead", Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		CausedByMessageID: source.ID, CreatedAt: at.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	previousStarter := startAgentThreadAsync
	legacyStarts := 0
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { legacyStarts++ }
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })

	provider := &strideLeadResponsesTestProvider{
		create:   STRIDELeadResponsesResult{ResponseID: "resp_shadow_1", ConversationID: "conv_shadow_1", Model: defaultSTRIDELeadHarnessModel, Status: "queued", EnvelopeDigest: strings.Repeat("1", 64)},
		retrieve: STRIDELeadResponsesResult{ResponseID: "resp_shadow_1", ConversationID: "conv_shadow_1", Model: defaultSTRIDELeadHarnessModel, Status: "completed", OutputText: completeResearchArtifactForTest(), EnvelopeDigest: strings.Repeat("2", 64)},
	}
	firstDone := make(chan error, 1)
	var idle atomic.Bool
	app.strideLeadShadow = &STRIDELeadShadowRuntime{
		Harness: &STRIDELeadHarness{Enabled: true, WorkRuns: app.workRuns, Provider: provider}, Artifacts: &strideLeadShadowTestArtifacts{},
		SpendCeiling: 25, PollInterval: time.Millisecond, MaxPolls: 1, Now: func() time.Time { return at.Add(time.Second) },
		Done: func(_ string, err error) { firstDone <- err }, Idle: idle.Load, active: map[string]struct{}{},
	}
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, "proposal-shadow-lead", proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	work := response["agentThread"].(scoutAgentThread)
	if legacyStarts != 1 || provider.creates != 0 {
		t.Fatalf("media-busy launch legacy starts=%d provider creates=%d", legacyStarts, provider.creates)
	}
	idle.Store(true)
	app.scheduleSTRIDELeadShadowRetrySweep()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shadow launch did not return asynchronously")
	}
	if legacyStarts != 1 || provider.creates != 1 {
		t.Fatalf("legacy starts=%d provider creates=%d", legacyStarts, provider.creates)
	}
	queued, err := app.workRuns.SideCard(work.ID)
	if err != nil || queued.Provider == nil || queued.Provider.Status != "queued" {
		t.Fatalf("queued card=%+v err=%v", queued, err)
	}

	reopened, err := NewSTRIDEWorkRunRepository(app.workRuns.path)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := &strideLeadShadowTestArtifacts{}
	secondDone := make(chan error, 1)
	restarted := &STRIDELeadShadowRuntime{
		Harness: &STRIDELeadHarness{Enabled: true, WorkRuns: reopened, Provider: provider}, Artifacts: artifacts,
		SpendCeiling: 25, PollInterval: time.Millisecond, MaxPolls: 2, Now: func() time.Time { return at.Add(2 * time.Second) },
		Done: func(_ string, err error) { secondDone <- err }, active: map[string]struct{}{},
	}
	restarted.Schedule(work)
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restarted shadow recovery did not finish")
	}
	card, err := reopened.SideCard(work.ID)
	if err != nil || card.Provider == nil || card.Provider.Status != "completed" || card.Provider.AssignmentID == artifacts.last.Assignment.ID ||
		len(card.ArtifactLineage) != 1 || !strideLeadCandidateMilestone(card) {
		t.Fatalf("recovered card=%+v adapter=%+v err=%v", card, artifacts.last, err)
	}
	if provider.creates != 1 || provider.retrieves != 2 || artifacts.staged != 1 || artifacts.committed != 0 || artifacts.delivered != 0 {
		t.Fatalf("creates=%d retrieves=%d staged=%d drive=%d delivered=%d", provider.creates, provider.retrieves, artifacts.staged, artifacts.committed, artifacts.delivered)
	}
	thirdDone := make(chan error, 1)
	restarted.Done = func(_ string, err error) { thirdDone <- err }
	restarted.Schedule(work)
	select {
	case err := <-thirdDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idempotent shadow replay did not finish")
	}
	if provider.creates != 1 || provider.retrieves != 2 || artifacts.staged != 1 {
		t.Fatalf("idempotent replay duplicated work: creates=%d retrieves=%d staged=%d", provider.creates, provider.retrieves, artifacts.staged)
	}
	events, err := reopened.Events(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	for _, event := range events {
		if event.Type == STRIDERunCreated {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("durable cards=%d, want one", runs)
	}
}

func TestNativeSTRIDELeadShadowAdapterRejectsUnrenderableDeckWithoutFalseReceipt(t *testing.T) {
	at := time.Date(2026, 8, 25, 23, 30, 0, 0, time.UTC)
	_, run, scout, presenter := strideLeadHarnessTestRepository(t, "", at)
	receipt := STRIDELeadProviderReceipt{
		RunID: run.ID, AssignmentID: scout.ID, Provider: providerOpenAI, Model: defaultSTRIDELeadHarnessModel,
		ResponseID: "resp_invalid_deck", Status: "completed", Recovery: "created", Attempt: 1,
		RequestDigest: strings.Repeat("1", 64), SpendBoundaryDigest: strings.Repeat("2", 64), SourceManifestDigest: strings.Repeat("3", 64),
		AuthorityFenceDigest: scout.AuthorityFenceDigest, ToolAdmissionDigest: strings.Repeat("4", 64), ProviderEnvelopeDigest: strings.Repeat("5", 64), ObservedAt: at,
	}
	adapter, err := newNativeSTRIDELeadShadowArtifactAdapter(filepath.Join(t.TempDir(), "candidates"))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := adapter.StageCandidate(context.Background(), STRIDELeadCandidateArtifactRequest{
		Run: run, Assignment: presenter, Provider: receipt,
		Body: "<!doctype html><html><body><section>flat and uneditable</section></body></html>", ObservedAt: at,
	})
	if !errors.Is(err, ErrSTRIDELeadCandidateInvalid) || candidate.Artifact.ID != "" {
		t.Fatalf("false validation receipt=%+v err=%v", candidate, err)
	}
}
