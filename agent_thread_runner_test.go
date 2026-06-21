package main

import (
	"context"
	"strings"
	"testing"
)

func TestAgentThreadProducesStructuredArtifactWithResponder(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	thread := scoutAgentThread{
		ID:     "agent-thread-research-1",
		Mode:   "research",
		Query:  "identify the evidence needed for Realtime 2 as UI",
		Status: "running",
	}

	var captured openAITextRequest
	output, err := app.produceAgentThreadArtifact(context.Background(), thread, func(_ context.Context, apiKey string, request openAITextRequest) (string, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		captured = request
		return "Vision: Realtime 2 is the operator UI.\n\nGoal: identify evidence.\n\nVerification: artifact complete.", nil
	})
	if err != nil {
		t.Fatalf("produceAgentThreadArtifact: %v", err)
	}
	if !strings.Contains(output, "Vision: Realtime 2") {
		t.Fatalf("output=%q, want responder output", output)
	}
	for _, want := range []string{"Vision", "Context used", "Review against the original goal", "Gate", "stable headings", "Executive Summary", "Search tags", "Thesis", "Sources", "Do not claim you performed browser"} {
		if !strings.Contains(captured.Instructions, want) {
			t.Fatalf("instructions missing %q: %s", want, captured.Instructions)
		}
	}
	if !strings.Contains(captured.Input, thread.Query) || !strings.Contains(captured.Input, "Board and memory context") {
		t.Fatalf("input=%q, want thread query and context", captured.Input)
	}
}

func TestAgentThreadModeContractsDifferentiateResearchAndDesign(t *testing.T) {
	for _, tt := range []struct {
		mode string
		want []string
	}{
		{mode: "research", want: []string{"Executive Summary", "Thesis", "Evidence", "Sources", "Counterarguments", "Recommendation", "Search tags"}},
		{mode: "design", want: []string{"Design intent", "Context and research used", "Core screens", "Responsive behavior", "Implementation handoff"}},
	} {
		got := agentThreadModeContract(tt.mode)
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Fatalf("mode %s contract missing %q: %s", tt.mode, want, got)
			}
		}
	}
}

func TestAgentThreadResearchModeCarriesArtifactContractMetadata(t *testing.T) {
	metadata := agentThreadModeMetadata("research")
	if metadata["artifactContract"] != "research_brief_v2" {
		t.Fatalf("artifactContract=%q, want research_brief_v2", metadata["artifactContract"])
	}
	for _, want := range []string{"executive summary", "sources", "worker evidence"} {
		if !strings.Contains(metadata["artifactHeadings"], want) {
			t.Fatalf("artifactHeadings missing %q: %s", want, metadata["artifactHeadings"])
		}
	}
	if got := agentThreadModeMetadata("design"); got != nil {
		t.Fatalf("design metadata=%v, want nil", got)
	}
}
