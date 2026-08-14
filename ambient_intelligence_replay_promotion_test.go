package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestProductionAmbientReplayPromotionRecoversReceiptFirstWithoutSecondModelRun(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store := &PostgresAmbientReplayStore{pool: canonical.pool}
	memoryPath := filepath.Join(t.TempDir(), "meeting-memory.jsonl")
	memory, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	meetingID := "meeting-replay-promotion"
	source := AmbientReplaySource{
		ObjectID: "transcript-replay-promotion-1", CaptureSequence: 41, ContentRevision: 1,
		ContentDigest: digestBrainString("AJ: Ship the transcript recall repair today."), ACLVersion: 3, PurgeGeneration: 7,
		OccurredStart: now.Add(-10 * time.Minute), OccurredEnd: now.Add(-9 * time.Minute),
		ConsentFenceDigest: digestBrainString("promotion-consent"), RoomID: officeRoomID, SittingID: meetingID,
	}
	if _, appended, err := memory.appendAmbientEntry(meetingMemoryKindTranscript, source.ObjectID, "AJ: Ship the transcript recall repair today.", map[string]string{
		"roomId": officeRoomID, "meetingId": meetingID, "captureSequence": "41", "visibility": "organization",
	}); err != nil || !appended {
		t.Fatalf("seed transcript appended=%v err=%v", appended, err)
	}
	app := &kanbanBoardApp{memory: memory}
	authority := &replayTestAuthority{snapshot: AmbientReplayAuthoritySnapshot{
		Sources: []AmbientReplaySource{source}, CursorDigests: map[string]string{"meeting_digest": digestBrainString("promotion-cursor")},
		PurgeGeneration: 7, ReleaseCommit: "0123456789abcdef0123456789abcdef01234567",
		ReleaseTreeDigest: digestBrainString("promotion-tree"), ReleaseReceiptDigest: digestBrainString("promotion-release"),
	}}
	engine := &AmbientReplayEngine{Authority: authority, Store: store, Now: func() time.Time { return now }}
	manifest, err := engine.Plan(ctx, AmbientReplayPlanRequest{
		IdempotencyKey: digestBrainString("promotion-recovery-plan"), TenantID: "bonfire", RoomID: officeRoomID, SittingID: meetingID,
		StageNames: []string{"brain", "meeting_digest"}, AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference,
		RollbackFloor: replayTestRollbackFloor, ExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	executionID := "45454545-4545-4454-8454-454545454545"
	if _, existing, err := store.BeginExecution(ctx, manifest, executionID, now); err != nil || existing {
		t.Fatalf("begin execution existing=%v err=%v", existing, err)
	}
	input := []AmbientReplayArtifact{{ID: source.ObjectID, Kind: "transcript", Digest: source.ContentDigest, SourceManifestDigest: manifest.SourceManifestDigest, ManifestDigest: manifest.Digest}}
	inputDigest, _ := digestAmbientReplayArtifacts(input)
	brainArtifact := AmbientReplayArtifact{ID: "ambient-replay:" + manifest.Digest[:16] + ":brain", Kind: "brain",
		Digest: digestBrainString("promotion brain artifact"), SourceManifestDigest: manifest.SourceManifestDigest, ManifestDigest: manifest.Digest, Text: "## Overview\nTranscript recall repair ships today."}
	brainOutputDigest, _ := digestAmbientReplayArtifacts([]AmbientReplayArtifact{brainArtifact})
	brainPrepared := AmbientReplayStageReceipt{ManifestDigest: manifest.Digest, ExecutionID: executionID, Stage: "brain", Ordinal: 0,
		Status: "prepared", InputDigest: inputDigest, SourceDigest: manifest.SourceManifestDigest, StartedAt: now}
	if err := store.RecordStageReceipt(ctx, brainPrepared); err != nil {
		t.Fatal(err)
	}
	brainCompleted := brainPrepared
	brainCompleted.Status, brainCompleted.OutputDigest, brainCompleted.CompletedAt = "completed", brainOutputDigest, now.Add(time.Second)
	brainCompleted.Usage = AmbientReplayUsage{Calls: 1, InputTokens: 50, OutputTokens: 20}
	if err := store.RecordStageReceipt(ctx, brainCompleted); err != nil {
		t.Fatal(err)
	}
	artifact := AmbientReplayArtifact{
		ID: "ambient-replay:" + manifest.Digest[:16] + ":meeting_digest", Kind: "meeting_digest",
		Digest: digestBrainString("promotion meeting digest artifact"), SourceManifestDigest: manifest.SourceManifestDigest, ManifestDigest: manifest.Digest,
		Text: `{"meetingId":"wrong","title":"Daily operating meeting","topics":[{"t":"Transcript recall repair ships today","anchor":"transcript-replay-promotion-1","importance":5}]}`,
	}
	outputDigest, _ := digestAmbientReplayArtifacts([]AmbientReplayArtifact{artifact})
	meetingInputDigest, _ := digestAmbientReplayArtifacts([]AmbientReplayArtifact{brainArtifact})
	prepared := AmbientReplayStageReceipt{ManifestDigest: manifest.Digest, ExecutionID: executionID, Stage: "meeting_digest", Ordinal: 1,
		Status: "prepared", InputDigest: meetingInputDigest, SourceDigest: manifest.SourceManifestDigest, StartedAt: now.Add(time.Second)}
	if err := store.RecordStageReceipt(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	completed := prepared
	completed.Status, completed.OutputDigest, completed.CompletedAt = "completed", outputDigest, now.Add(time.Second)
	completed.Usage = AmbientReplayUsage{Calls: 1, InputTokens: 50, OutputTokens: 20}
	if err := store.RecordStageReceipt(ctx, completed); err != nil {
		t.Fatal(err)
	}
	crash := errors.New("simulated process loss after authority receipt")
	promoter := newProductionAmbientReplayPromoter(app, store)
	promoter.now = func() time.Time { return now.Add(2 * time.Second) }
	promoter.afterReceipt = func() error { return crash }
	if err := promoter.PromoteAmbientReplay(ctx, manifest, executionID, []AmbientReplayArtifact{artifact}); !errors.Is(err, ErrAmbientReplayPromotionPending) {
		t.Fatalf("promotion crash err=%v", err)
	}
	if _, found := memory.currentDigest(meetingMemoryKindMeetingDigest, meetingID); found {
		t.Fatal("canonical digest became visible before receipt-backed recovery")
	}
	if _, found, err := store.LoadAmbientReplayPromotionReceipt(ctx, manifest.Digest); err != nil || !found {
		t.Fatalf("promotion receipt found=%v err=%v", found, err)
	}

	restartedMemory, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &kanbanBoardApp{memory: restartedMemory}
	recovery := newProductionAmbientReplayPromoter(restarted, store)
	recovery.now = func() time.Time { return now.Add(3 * time.Second) }
	if err := recovery.RecoverAmbientReplayPromotions(ctx); err != nil {
		t.Fatalf("recover promotion: %v", err)
	}
	digest, found := restartedMemory.currentDigest(meetingMemoryKindMeetingDigest, meetingID)
	if !found || digest.Metadata[ambientReplayPromotionManifestMetadataKey] != manifest.Digest || digest.Text == "" {
		t.Fatalf("recovered digest=%+v found=%v", digest, found)
	}
	payload, ok := parseMeetingDigest(digest.Text)
	if !ok || payload.MeetingID != meetingID || len(payload.Topics) != 1 || payload.Topics[0].Anchor != source.ObjectID {
		t.Fatalf("recovered payload=%+v ok=%v", payload, ok)
	}
	_, status, err := store.LoadManifest(ctx, manifest.Digest)
	if err != nil || status != "completed" {
		t.Fatalf("manifest status=%q err=%v", status, err)
	}
	entryCount := len(restartedMemory.snapshot(0))
	if err := recovery.RecoverAmbientReplayPromotions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(restartedMemory.snapshot(0)); got != entryCount {
		t.Fatalf("receipt replay duplicated memory entries: before=%d after=%d", entryCount, got)
	}
}
