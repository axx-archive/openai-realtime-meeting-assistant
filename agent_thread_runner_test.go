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
	for _, want := range []string{"Vision", "Review against the original goal", "Shipping gate", "Do not claim you performed browser"} {
		if !strings.Contains(captured.Instructions, want) {
			t.Fatalf("instructions missing %q: %s", want, captured.Instructions)
		}
	}
	if !strings.Contains(captured.Input, thread.Query) || !strings.Contains(captured.Input, "Board and memory context") {
		t.Fatalf("input=%q, want thread query and context", captured.Input)
	}
}
