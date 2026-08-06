package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionAmbientReplayStageRunnerIsNonMutating(t *testing.T) {
	text := "AJ said the team will validate the pricing page before launch."
	entry := meetingMemoryEntry{
		ID:        "transcript-replay-1",
		Kind:      meetingMemoryKindTranscript,
		Text:      text,
		CreatedAt: time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		Metadata:  map[string]string{"roomId": officeRoomID, "meetingId": "sitting-replay-1"},
	}
	store := &meetingMemoryStore{
		path:              filepath.Join(t.TempDir(), "memory.jsonl"),
		entries:           []meetingMemoryEntry{entry},
		seen:              map[string]struct{}{entry.ID: {}},
		meetingIDs:        map[string]string{},
		bootLatestIDs:     map[string]string{},
		bootLatestRoomIDs: map[string]map[string]string{},
	}
	app := &kanbanBoardApp{
		memory: store,
		cards:  []kanbanCard{{ID: "card-1", Title: "Existing card", Status: kanbanStatusBacklog}},
	}

	sourceDigest := digestBrainString(entry.Text)
	manifest := AmbientReplayManifest{
		Schema:               ambientReplaySchema,
		Digest:               strings.Repeat("a", 64),
		SourceManifestDigest: strings.Repeat("b", 64),
		RoomID:               officeRoomID,
		SittingID:            "sitting-replay-1",
		Sources: []AmbientReplaySource{{
			ObjectID: entry.ID, CaptureSequence: 1, ContentRevision: 1, ContentDigest: sourceDigest,
			ACLVersion: 1, PurgeGeneration: 0, OccurredStart: entry.CreatedAt,
			OccurredEnd: entry.CreatedAt.Add(time.Minute), RoomID: officeRoomID, SittingID: entry.Metadata["meetingId"],
		}},
		Stages: []AmbientReplayStageSpec{{
			Name: "brain", Provider: providerOpenAI, Model: meetingBrainModel(), PromptTokenCap: 32000,
			OutputTokenCap: 2400, CallCap: 1, CostMicrosCap: 1000000,
		}},
	}
	input := []AmbientReplayArtifact{{ID: entry.ID, Kind: "transcript", Digest: sourceDigest, SourceManifestDigest: manifest.SourceManifestDigest, ManifestDigest: manifest.Digest}}

	runner := newProductionAmbientReplayStageRunner(app)
	runner.responder = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Seat != seatBrain || !strings.Contains(request.Input, text) {
			t.Fatalf("replay request lost source binding: seat=%q input=%q", request.Seat, request.Input)
		}
		return "## Overview\nPricing validation is a launch prerequisite.", nil
	}
	result, err := runner.RunAmbientReplayStage(context.Background(), manifest, manifest.Stages[0], input)
	if err != nil {
		t.Fatalf("run replay stage: %v", err)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Kind != "brain" || result.Artifacts[0].Text == "" {
		t.Fatalf("unexpected replay artifacts: %#v", result.Artifacts)
	}
	if result.Usage.Calls != 1 || !result.Usage.Estimated {
		t.Fatalf("unexpected estimated usage: %#v", result.Usage)
	}
	if got := len(store.snapshot(0)); got != 1 {
		t.Fatalf("replay mutated memory store: %d entries", got)
	}
	if len(app.cards) != 1 || app.cards[0].Title != "Existing card" {
		t.Fatalf("replay mutated board: %#v", app.cards)
	}
}

func TestAmbientReplayUsageCaptureUsesWireTokens(t *testing.T) {
	capture := &ambientReplayUsageCapture{}
	ctx := withAmbientReplayUsageCapture(context.Background(), capture)
	captureAmbientReplayUsage(ctx, &openAIResponsesUsage{
		InputTokens:  10,
		OutputTokens: 4,
	}, true)
	usage, ok := capture.snapshot()
	if !ok || usage.Calls != 1 || usage.InputTokens != 10 || usage.OutputTokens != 4 || usage.Estimated {
		t.Fatalf("unexpected wire usage: %#v ok=%v", usage, ok)
	}
}
