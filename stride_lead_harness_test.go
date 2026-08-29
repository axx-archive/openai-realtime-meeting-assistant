package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type strideLeadResponsesTestProvider struct {
	creates        int
	retrieves      int
	created        STRIDELeadResponsesRequest
	createRequests []STRIDELeadResponsesRequest
	create         STRIDELeadResponsesResult
	retrieve       STRIDELeadResponsesResult
}

func (provider *strideLeadResponsesTestProvider) CreateSTRIDELeadResponse(_ context.Context, request STRIDELeadResponsesRequest) (STRIDELeadResponsesResult, error) {
	provider.creates++
	provider.created = request
	provider.createRequests = append(provider.createRequests, request)
	return provider.create, nil
}

func (provider *strideLeadResponsesTestProvider) RetrieveSTRIDELeadResponse(_ context.Context, _ string) (STRIDELeadResponsesResult, error) {
	provider.retrieves++
	return provider.retrieve, nil
}

func strideLeadHarnessTestRepository(t *testing.T, path string, at time.Time) (*STRIDEWorkRunRepository, STRIDECanonicalWorkRun, STRIDECanonicalAgentAssignment, STRIDECanonicalAgentAssignment) {
	t.Helper()
	repository, err := NewSTRIDEWorkRunRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	run := strideWorkRunTestRun(at)
	created := strideWorkRunTestCommand(run, "lead-run-created", "1", STRIDERunCreated, "Scout opened the approved presentation work", at)
	created.Run = &run
	appendSTRIDEWorkRunTestEvent(t, repository, created)
	scout := strideWorkRunTestAssignment(t, run, "lead-assignment-scout", STRIDEWorkAgentScout, "orchestration", "2", at.Add(time.Second))
	scoutAdded := strideWorkRunTestCommand(run, "lead-scout-added", "3", STRIDEAssignmentAdded, "Scout accepted accountability for the work", at.Add(time.Second))
	scoutAdded.Agent, scoutAdded.AssignmentID, scoutAdded.Assignment = scout.Agent, scout.ID, &scout
	appendSTRIDEWorkRunTestEvent(t, repository, scoutAdded)
	scoutStarted := strideWorkRunTestCommand(run, "lead-scout-started", "4", STRIDEAssignmentStarted, "Scout started the approved work", at.Add(2*time.Second))
	scoutStarted.Agent, scoutStarted.AssignmentID = scout.Agent, scout.ID
	appendSTRIDEWorkRunTestEvent(t, repository, scoutStarted)
	presenter := strideWorkRunTestAssignment(t, run, "lead-assignment-presenter", STRIDEWorkAgentPresenter, "presentation", "5", at.Add(3*time.Second))
	presenter.AssignedBy = STRIDEWorkAgentScout
	presenter.AuthorityFenceDigest, err = presenter.FenceDigest()
	if err != nil {
		t.Fatal(err)
	}
	presenterAdded := strideWorkRunTestCommand(run, "lead-presenter-added", "6", STRIDEAssignmentAdded, "Scout assigned Presenter to create the deck", at.Add(3*time.Second))
	presenterAdded.ActorPrincipal, presenterAdded.Agent, presenterAdded.AssignmentID, presenterAdded.Assignment = STRIDEWorkAgentScout, presenter.Agent, presenter.ID, &presenter
	appendSTRIDEWorkRunTestEvent(t, repository, presenterAdded)
	return repository, run, scout, presenter
}

func strideLeadHarnessTestSpend() STRIDELeadSpendBoundary {
	return STRIDELeadSpendBoundary{
		Approval:          strideWorkRunTestReference(STRIDEContractWorkProposal, "approved-proposal", "a", 1),
		MaximumSpendCents: 500, ExpectedSpendCents: 100, ApprovalFenceDigest: strings.Repeat("b", 64),
	}
}

func TestSTRIDELeadHarnessDefaultsOffAndClassifiesNeedsYouNarrowly(t *testing.T) {
	t.Setenv(strideLeadHarnessShadowEnvironment, "")
	provider := &strideLeadResponsesTestProvider{}
	harness := &STRIDELeadHarness{Enabled: true, WorkRuns: &STRIDEWorkRunRepository{}, Provider: provider}
	if _, err := harness.Run(context.Background(), STRIDELeadHarnessRequest{}); !errors.Is(err, ErrSTRIDELeadHarnessFenced) || provider.creates != 0 {
		t.Fatalf("default-off err=%v creates=%d", err, provider.creates)
	}
	for _, cause := range []STRIDELeadFailureCause{STRIDELeadFailureMissingAuthority, STRIDELeadFailureHumanDecision} {
		if classifySTRIDELeadFailure(cause) != "needs_you" {
			t.Fatalf("%s did not require human", cause)
		}
	}
	for _, cause := range []STRIDELeadFailureCause{STRIDELeadFailureProvider, STRIDELeadFailureParser, STRIDELeadFailureCritic, STRIDELeadFailureInternal} {
		if classifySTRIDELeadFailure(cause) == "needs_you" {
			t.Fatalf("%s falsely became Needs you", cause)
		}
	}
}

func TestSTRIDELeadHarnessRestartRecoversOneCardWithScoutProviderOwnership(t *testing.T) {
	t.Setenv(strideLeadHarnessShadowEnvironment, "true")
	at := time.Date(2026, 8, 25, 21, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "work-runs.jsonl")
	repository, run, scout, presenter := strideLeadHarnessTestRepository(t, path, at)
	provider := &strideLeadResponsesTestProvider{
		create:   STRIDELeadResponsesResult{ResponseID: "resp_lead_1", ConversationID: "conv_lead_1", Model: defaultSTRIDELeadHarnessModel, Status: "queued", EnvelopeDigest: strings.Repeat("c", 64)},
		retrieve: STRIDELeadResponsesResult{ResponseID: "resp_lead_1", ConversationID: "conv_lead_1", Model: defaultSTRIDELeadHarnessModel, Status: "completed", OutputText: "A complete deck brief", EnvelopeDigest: strings.Repeat("d", 64)},
	}
	request := STRIDELeadHarnessRequest{RunID: run.ID, Instructions: "Lead the approved work", Input: "Create the deck", Spend: strideLeadHarnessTestSpend(), Now: at.Add(4 * time.Second)}
	harness := &STRIDELeadHarness{Enabled: true, WorkRuns: repository, Provider: provider}
	first, err := harness.Run(context.Background(), request)
	if err != nil || first.Status != "queued" || first.AssignmentID != scout.ID || provider.creates != 1 {
		t.Fatalf("first=%+v creates=%d err=%v", first, provider.creates, err)
	}
	if provider.created.Model != defaultSTRIDELeadHarnessModel || provider.created.ReasoningEffort != defaultSTRIDELeadHarnessReasoningEffort ||
		provider.created.Metadata["assignment_id"] != scout.ID || provider.created.Metadata["specialist_assignment_id"] != presenter.ID {
		t.Fatalf("quality/ownership wire=%+v", provider.created)
	}
	forged := first
	forged.AssignmentID = presenter.ID
	forged.ObservedAt = at.Add(4500 * time.Millisecond)
	forgedEvent := strideWorkRunEvent(run, "forged-specialist-provider", STRIDEProviderResponseRecorded, presenter.Agent, "Presenter tried to claim the lead provider response", forged.ObservedAt)
	forgedEvent.Agent, forgedEvent.AssignmentID, forgedEvent.ProviderReceipt = presenter.Agent, presenter.ID, &forged
	if _, _, err := repository.Append(forgedEvent); !errors.Is(err, ErrSTRIDEWorkRunConflict) {
		t.Fatalf("specialist provider ownership err=%v", err)
	}
	reopened, err := NewSTRIDEWorkRunRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	request.Now = at.Add(5 * time.Second)
	harness = &STRIDELeadHarness{Enabled: true, WorkRuns: reopened, Provider: provider}
	second, err := harness.Run(context.Background(), request)
	if err != nil || second.Status != "completed" || second.AssignmentID != scout.ID || provider.creates != 1 || provider.retrieves != 1 {
		t.Fatalf("second=%+v creates=%d retrieves=%d err=%v", second, provider.creates, provider.retrieves, err)
	}
	request.Now = at.Add(6 * time.Second)
	third, err := harness.Run(context.Background(), request)
	if err != nil || third.ProviderEnvelopeDigest != second.ProviderEnvelopeDigest || provider.creates != 1 || provider.retrieves != 1 {
		t.Fatalf("idempotent terminal=%+v creates=%d retrieves=%d err=%v", third, provider.creates, provider.retrieves, err)
	}
	card, err := reopened.SideCard(run.ID)
	if err != nil || card.Provider == nil || card.Provider.AssignmentID != scout.ID || len(card.Assignments) != 2 || len(card.Milestones) != 2 {
		t.Fatalf("side card=%+v err=%v", card, err)
	}
	events, err := reopened.Events(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := 0
	for _, event := range events {
		if event.Type == STRIDERunCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("run/card creation events=%d", created)
	}
}

func TestSTRIDELeadHarnessRetriesFailedProviderAsDurableScoutAttempt(t *testing.T) {
	t.Setenv(strideLeadHarnessShadowEnvironment, "true")
	at := time.Date(2026, 8, 25, 21, 30, 0, 0, time.UTC)
	repository, run, scout, presenter := strideLeadHarnessTestRepository(t, "", at)
	provider := &strideLeadResponsesTestProvider{
		create: STRIDELeadResponsesResult{ResponseID: "resp_failed_1", Model: defaultSTRIDELeadHarnessModel, Status: "failed", EnvelopeDigest: strings.Repeat("e", 64)},
	}
	harness := &STRIDELeadHarness{Enabled: true, WorkRuns: repository, Provider: provider}
	request := STRIDELeadHarnessRequest{RunID: run.ID, Instructions: "Lead the approved work", Input: "Create the deck", Spend: strideLeadHarnessTestSpend(), Now: at.Add(4 * time.Second)}
	first, err := harness.Run(context.Background(), request)
	if err != nil || first.Status != "failed" || first.Attempt != 1 || first.AssignmentID != scout.ID {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	provider.create = STRIDELeadResponsesResult{ResponseID: "resp_retry_2", Model: defaultSTRIDELeadHarnessModel, Status: "completed", OutputText: "Complete deck", EnvelopeDigest: strings.Repeat("f", 64)}
	request.Now = at.Add(5 * time.Second)
	second, err := harness.Run(context.Background(), request)
	if err != nil || second.Status != "completed" || second.Attempt != 2 || second.Recovery != "resumed" || second.AssignmentID != scout.ID {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if provider.creates != 2 || len(provider.createRequests) != 2 || provider.createRequests[0].IdempotencyKey == provider.createRequests[1].IdempotencyKey {
		t.Fatalf("create attempts=%+v", provider.createRequests)
	}
	if provider.createRequests[1].Metadata["assignment_id"] != scout.ID || provider.createRequests[1].Metadata["specialist_assignment_id"] != presenter.ID {
		t.Fatalf("retry ownership metadata=%+v", provider.createRequests[1].Metadata)
	}
	card, err := repository.SideCard(run.ID)
	if err != nil || card.Provider == nil || card.Provider.Attempt != 2 || card.Provider.AssignmentID != scout.ID || len(card.Assignments) != 2 {
		t.Fatalf("card=%+v err=%v", card, err)
	}
	events, err := repository.Events(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	runCards := 0
	for _, event := range events {
		if event.Type == STRIDERunCreated {
			runCards++
		}
	}
	if runCards != 1 {
		t.Fatalf("run/card creation events=%d", runCards)
	}
}

type strideLeadHTTPDoerFunc func(*http.Request) (*http.Response, error)

func (function strideLeadHTTPDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestSTRIDELeadResponsesWireStoresBackgroundStateAndSupportsBothContinuityModes(t *testing.T) {
	tools, err := admitSTRIDELeadTools("presentation")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := newSTRIDELeadResponsesClient("secret", "project_1")
	client.httpClient = strideLeadHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("OpenAI-Project") != "project_1" || request.Header.Get("Idempotency-Key") == "" {
			t.Fatalf("headers=%v", request.Header)
		}
		if request.Method == http.MethodGet {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"resp_wire_1","model":"gpt-5.6-sol","status":"completed","previous_response_id":"resp_prior","conversation":null,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"unknown_future_field":true}`))}, nil
		}
		raw, _ := io.ReadAll(request.Body)
		var wire map[string]any
		if json.Unmarshal(raw, &wire) != nil || wire["store"] != true || wire["background"] != true || wire["model"] != defaultSTRIDELeadHarnessModel ||
			wire["previous_response_id"] != "resp_prior" || wire["conversation"] != nil || wire["parallel_tool_calls"] != false {
			t.Fatalf("wire=%s", raw)
		}
		reasoning, _ := wire["reasoning"].(map[string]any)
		if reasoning["effort"] != defaultSTRIDELeadHarnessReasoningEffort {
			t.Fatalf("reasoning=%v", reasoning)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`{"id":"resp_wire_1","model":"gpt-5.6-sol","status":"queued","previous_response_id":"resp_prior","conversation":null,"output":[]}`))}, nil
	})
	request := STRIDELeadResponsesRequest{
		Model: defaultSTRIDELeadHarnessModel, ReasoningEffort: defaultSTRIDELeadHarnessReasoningEffort, Instructions: "server", Input: "work",
		IdempotencyKey: strings.Repeat("1", 64), PreviousResponseID: "resp_prior", Metadata: map[string]string{"run_id": "run_1"},
		Tools: tools.Tools, ToolAgent: tools.Agent, ToolManifestDigest: tools.ManifestDigest, ToolAdmissionDigest: tools.AdmissionDigest,
	}
	created, err := client.CreateSTRIDELeadResponse(context.Background(), request)
	if err != nil || created.Status != "queued" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	request.PreviousResponseID, request.ConversationID = "", "conv_1"
	client.httpClient = strideLeadHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(request.Body)
		if !bytes.Contains(raw, []byte(`"conversation":{"id":"conv_1"}`)) || bytes.Contains(raw, []byte("previous_response_id")) {
			t.Fatalf("conversation wire=%s", raw)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"resp_wire_2","model":"gpt-5.6-sol","status":"queued","conversation":{"id":"conv_1"},"output":[]}`))}, nil
	})
	if _, err := client.CreateSTRIDELeadResponse(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	client.httpClient = strideLeadHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"resp_wire_1","model":"gpt-5.6-sol","status":"completed","previous_response_id":"resp_prior","conversation":null,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"unknown_future_field":true}`))}, nil
	})
	retrieved, err := client.RetrieveSTRIDELeadResponse(context.Background(), "resp_wire_1")
	if err != nil || retrieved.Status != "completed" || retrieved.OutputText != "done" || calls != 1 {
		// calls counts only the first client's initial doer; the later doers are
		// intentionally separate wire-shape assertions.
		t.Fatalf("retrieved=%+v err=%v initialCalls=%d", retrieved, err, calls)
	}
}

func TestSTRIDELeadResponsesRejectsToolManifestExpansionAndMixedContinuity(t *testing.T) {
	tools, err := admitSTRIDELeadTools("research")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	client := newSTRIDELeadResponsesClient("secret", "project_1")
	client.httpClient = strideLeadHTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("network must not be reached")
	})
	request := STRIDELeadResponsesRequest{
		Model: defaultSTRIDELeadHarnessModel, ReasoningEffort: defaultSTRIDELeadHarnessReasoningEffort,
		Instructions: "Lead", Input: "Research", IdempotencyKey: strings.Repeat("1", 64),
		Metadata: map[string]string{"run_id": "run_1"}, Tools: append([]map[string]any(nil), tools.Tools...), ToolAgent: tools.Agent,
		ToolManifestDigest: tools.ManifestDigest, ToolAdmissionDigest: tools.AdmissionDigest,
	}
	request.Tools = append(request.Tools, map[string]any{"type": "function", "name": "unadmitted_tool"})
	if _, err := client.CreateSTRIDELeadResponse(context.Background(), request); !errors.Is(err, ErrSTRIDELeadHarnessInvalid) || called {
		t.Fatalf("expanded manifest err=%v called=%v", err, called)
	}
	request.Tools = tools.Tools
	request.PreviousResponseID, request.ConversationID = "resp_prior", "conv_prior"
	if _, err := client.CreateSTRIDELeadResponse(context.Background(), request); !errors.Is(err, ErrSTRIDELeadHarnessInvalid) || called {
		t.Fatalf("mixed continuity err=%v called=%v", err, called)
	}
}
