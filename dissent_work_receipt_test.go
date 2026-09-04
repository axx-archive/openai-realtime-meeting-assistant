package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func dissentReceiptTestThread() scoutAgentThread {
	return scoutAgentThread{ID: "document-run", Artifact: meetingMemoryEntry{ID: "document-draft", Metadata: map[string]string{"outputContract": documentReportOutputContract}}}
}

func dissentReceiptTestRequest() openAITextRequest {
	return openAITextRequest{Model: defaultMeetingBrainModel, ReasoningEffort: defaultMeetingBrainReasoningEffort, Input: "Private source text", Instructions: "Draft the requested document", MaxOutputTokens: 1000}
}

func dissentReceiptTestUsage() openAIResponsesUsage {
	return openAIResponsesUsage{InputTokens: 23, OutputTokens: 17, TotalTokens: 40}
}

func TestDissentDocumentReceiptBindsExecutorAndOutputWithoutPrivateContext(t *testing.T) {
	thread := dissentReceiptTestThread()
	ctx, collector := withDissentDocumentReceipt(context.Background(), thread)
	calls := 0
	output, err := callDocumentWorkWithReceipt(ctx, thread, "secret-key", dissentReceiptTestRequest(), func(ctx context.Context, key string, request openAITextRequest) (string, error) {
		calls++
		usage := dissentReceiptTestUsage()
		captureOpenAIResponseReceipt(ctx, "resp_document", request.Model, &usage)
		return "  # Decision\nThe draft.  ", nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("output=%q err=%v calls=%d", output, err, calls)
	}
	metadata, err := collector.mergeMetadata(map[string]string{"existing": "retained"})
	if err != nil || metadata["existing"] != "retained" {
		t.Fatalf("metadata=%v err=%v", metadata, err)
	}
	raw := metadata[dissentDocumentReceiptKey]
	if strings.Contains(raw, "Private source text") || strings.Contains(raw, "secret-key") || strings.Contains(raw, "The draft.") {
		t.Fatal("receipt leaked request, key, or work product")
	}
	if err := verifyDissentDocumentReceipt(raw, thread.Artifact.ID, output); err != nil {
		t.Fatal(err)
	}
	if metadata["dissentQualification"] != "not_evaluated" || metadata["dissentAssurance"] != "not_performed" {
		t.Fatalf("false assurance claim: %v", metadata)
	}
	for _, mutation := range []struct{ raw, id, body string }{
		{raw, "another-document", output}, {raw, thread.Artifact.ID, output + " changed"},
		{strings.Replace(raw, "resp_document", "resp_changed", 1), thread.Artifact.ID, output},
	} {
		if verifyDissentDocumentReceipt(mutation.raw, mutation.id, mutation.body) == nil {
			t.Fatal("accepted changed receipt binding")
		}
	}
}

func TestDissentDocumentReceiptDoesNotInventMissingProviderEvidence(t *testing.T) {
	thread := dissentReceiptTestThread()
	ctx, collector := withDissentDocumentReceipt(context.Background(), thread)
	output, err := callDocumentWorkWithReceipt(ctx, thread, "key", dissentReceiptTestRequest(), func(context.Context, string, openAITextRequest) (string, error) { return "# Draft", nil })
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := collector.mergeMetadata(nil)
	if metadata["dissentExecutionEvidence"] != "unavailable" {
		t.Fatalf("metadata=%v", metadata)
	}
	var receipt dissentDocumentExecutionReceipt
	if err := json.Unmarshal([]byte(metadata[dissentDocumentReceiptKey]), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ResponseID != "" || receipt.ActualModel != "" || receipt.Usage != nil {
		t.Fatalf("invented evidence: %+v", receipt)
	}
	if verifyDissentDocumentReceipt(metadata[dissentDocumentReceiptKey], thread.Artifact.ID, output) == nil {
		t.Fatal("missing evidence verified")
	}
}

func TestDissentDocumentReceiptRejectsUnobservedRouteChangeAndBadUsage(t *testing.T) {
	for _, scenario := range []string{"wrong-model", "negative-usage", "cached-over-input"} {
		t.Run(scenario, func(t *testing.T) {
			thread := dissentReceiptTestThread()
			ctx, collector := withDissentDocumentReceipt(context.Background(), thread)
			_, err := callDocumentWorkWithReceipt(ctx, thread, "key", dissentReceiptTestRequest(), func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
				model, usage := request.Model, dissentReceiptTestUsage()
				switch scenario {
				case "wrong-model":
					model = "unadmitted"
				case "negative-usage":
					usage.InputTokens = -1
				case "cached-over-input":
					usage.InputTokensDetails.CachedTokens = 24
				}
				captureOpenAIResponseReceipt(ctx, "resp_document", model, &usage)
				return "# Draft", nil
			})
			if err == nil {
				t.Fatal("invalid execution accepted")
			}
			metadata, _ := collector.mergeMetadata(nil)
			if len(metadata) != 0 {
				t.Fatal("invalid evidence persisted")
			}
		})
	}
}

func TestDissentDocumentReceiptRecordsObservedExistingFallback(t *testing.T) {
	thread := dissentReceiptTestThread()
	ctx, collector := withDissentDocumentReceipt(context.Background(), thread)
	output, err := callDocumentWorkWithReceipt(ctx, thread, "key", dissentReceiptTestRequest(), func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		usage := dissentReceiptTestUsage()
		captureOpenAIResponseReceipt(ctx, "resp_fallback", "fallback-model", &usage)
		stampProviderCallProvenance(ctx, providerCallProvenance{Provider: providerOpenAI, PrimaryModel: request.Model, Model: "fallback-model", FallbackUsed: true})
		return "# Draft", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := collector.mergeMetadata(nil)
	if !strings.Contains(metadata[dissentDocumentReceiptKey], `"fallbackUsed":true`) {
		t.Fatal("fallback was hidden")
	}
	if err := verifyDissentDocumentReceipt(metadata[dissentDocumentReceiptKey], thread.Artifact.ID, output); err != nil {
		t.Fatal(err)
	}
}

func TestDissentDocumentReceiptRefusesToolExpansionBeforeDispatch(t *testing.T) {
	thread := dissentReceiptTestThread()
	ctx, _ := withDissentDocumentReceipt(context.Background(), thread)
	request := dissentReceiptTestRequest()
	request.EnableWebSearch = true
	_, err := callDocumentWorkWithReceipt(ctx, thread, "key", request, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("provider dispatched")
		return "", nil
	})
	if err == nil {
		t.Fatal("tools admitted")
	}
	thread.Artifact.Metadata["outputContract"] = "another-contract"
	if _, collector := withDissentDocumentReceipt(context.Background(), thread); collector != nil {
		t.Fatal("unrelated artifact acquired receipt scope")
	}
}

func TestDissentDocumentReceiptDoesNotBypassHostTerminalGate(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	installFakeResponder(t, goalResponderRoutes{})
	previousStart := startAgentThreadAsync
	var dispatched []scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { dispatched = append(dispatched, thread) }
	t.Cleanup(func() { startAgentThreadAsync = previousStart })
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID: "document_receipt_probe", Version: 1, Title: "Document receipt probe", Description: "One bounded document writer.", Authority: toolAuthorityWorkspaceWrite, Hidden: true,
		Stages: []ProcessStage{{ID: "write", Title: "Write the document", Role: processRoleWriter, Mode: "artifacts", OutputContract: documentReportOutputContract}},
	})
	root, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{Objective: "Draft a brief internal document", CreatedBy: "aj@shareability.com", ToolTemplate: "document_receipt_probe"})
	if err != nil {
		t.Fatal(err)
	}
	app.runGoalThread(root.Artifact.ID)
	if len(dispatched) != 1 {
		t.Fatalf("dispatched children=%d", len(dispatched))
	}
	thread := dispatched[0]
	// This test ends at the document's durable terminal boundary; downstream
	// review has separate rendered-admission tests.
	foldGoalChildAsync = func(*kanbanBoardApp, string, string, meetingMemoryEntry, string) {}
	calls := 0
	createOpenAITextResponse = func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		usage := dissentReceiptTestUsage()
		captureOpenAIResponseReceipt(ctx, "resp_persisted_document", request.Model, &usage)
		return "# Team decision\n\nThe team should review the proposed draft before adopting it.", nil
	}
	app.runAgentThread(thread)
	stored, ok := app.osArtifactByID(thread.Artifact.ID)
	if !ok || stored.Metadata["threadStatus"] != "error" || calls != 1 {
		t.Fatalf("status=%v calls=%d", stored.Metadata, calls)
	}
	// A probe process is deliberately not Document Studio and cannot satisfy
	// its factual claim gate. Provider evidence persists, but never verifies as
	// the host's replacement error artifact or upgrades that artifact to done.
	if err := verifyDissentDocumentReceipt(stored.Metadata[dissentDocumentReceiptKey], stored.ID, stored.Text); err == nil {
		t.Fatal("provider receipt bypassed the host's rejected result")
	}
	reloaded, err := newMeetingMemoryStore(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	durable, ok := reloaded.entryByKindAndID(meetingMemoryKindOSArtifact, stored.ID)
	if !ok {
		t.Fatal("terminal artifact missing after reload")
	}
	if err := verifyDissentDocumentReceipt(durable.Metadata[dissentDocumentReceiptKey], durable.ID, "# Team decision\n\nThe team should review the proposed draft before adopting it."); err != nil {
		t.Fatal(err)
	}
}
