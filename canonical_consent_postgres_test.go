package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresConsentStoreIsImmutableRestartSafeAndDependencyAware(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store := NewPostgresConsentStore(canonical)
	baseTime := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	audio := consentFixture("pg-audio", ConsentGranted, ConsentAudioCapture)
	audio.RecordedAt = baseTime
	transcription := consentFixture("pg-transcription", ConsentGranted, ConsentTranscription)
	transcription.RecordedAt = baseTime.Add(time.Second)
	for _, record := range []ConsentRecord{audio, transcription} {
		if existing, err := store.Append(ctx, record); err != nil || existing {
			t.Fatalf("append %s existing=%v err=%v", record.Scopes[0], existing, err)
		}
	}
	query := ConsentQuery{
		TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: "user-1",
		RoomID: "room-1", SittingID: "sitting-1", PolicyVersion: "policy-v1",
		Scopes: []ConsentScope{ConsentTranscription},
	}
	decision, err := store.Effective(ctx, query)
	if err != nil || !decision.Allowed || len(decision.RecordIDs) != 2 {
		t.Fatalf("effective decision=%+v err=%v", decision, err)
	}
	if existing, err := store.Append(ctx, audio); err != nil || !existing {
		t.Fatalf("idempotent retry existing=%v err=%v", existing, err)
	}
	conflict := audio
	conflict.Disposition = ConsentDenied
	if _, err := store.Append(ctx, conflict); !errors.Is(err, ErrConsentConflict) {
		t.Fatalf("conflict err=%v", err)
	}

	cutoff := uint64(104)
	withdrawal := consentFixture("pg-withdraw", ConsentWithdrawn, ConsentAudioCapture)
	withdrawal.RecordedAt = baseTime.Add(2 * time.Second)
	withdrawal.LastAcceptedCaptureSequence = &cutoff
	if _, err := store.Append(ctx, withdrawal); err != nil {
		t.Fatal(err)
	}
	// A new wrapper has no process memory and must still observe PostgreSQL's
	// durable withdrawal after restart.
	restarted := NewPostgresConsentStore(canonical)
	decision, err = restarted.Effective(context.Background(), query)
	if err != nil || decision.Allowed || decision.RecordIDs[ConsentAudioCapture] != withdrawal.ID {
		t.Fatalf("restart withdrawal decision=%+v err=%v", decision, err)
	}
	stored, err := restarted.readConsentRecord(ctx, withdrawal.ID)
	if err != nil || stored.LastAcceptedCaptureSequence == nil || *stored.LastAcceptedCaptureSequence != cutoff {
		t.Fatalf("stored withdrawal=%+v err=%v", stored, err)
	}
}

func TestPostgresConsentStoreNeverCrossesPrincipalRoomSittingOrPolicy(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store := NewPostgresConsentStore(canonical)
	record := consentFixture("pg-exact", ConsentGranted, ConsentAudioCapture)
	if _, err := store.Append(ctx, record); err != nil {
		t.Fatal(err)
	}
	base := ConsentQuery{
		TenantID: record.TenantID, PrincipalKind: record.PrincipalKind, PrincipalID: record.PrincipalID,
		RoomID: record.RoomID, SittingID: record.SittingID, PolicyVersion: record.PolicyVersion,
		Scopes: []ConsentScope{ConsentAudioCapture},
	}
	mutations := []func(*ConsentQuery){
		func(query *ConsentQuery) { query.TenantID = "tenant-2" },
		func(query *ConsentQuery) { query.PrincipalID = "user-2" },
		func(query *ConsentQuery) { query.RoomID = "room-2" },
		func(query *ConsentQuery) { query.SittingID = "sitting-2" },
		func(query *ConsentQuery) { query.PolicyVersion = "policy-v2" },
	}
	for index, mutate := range mutations {
		query := base
		mutate(&query)
		decision, err := store.Effective(ctx, query)
		if err != nil || decision.Allowed {
			t.Fatalf("mutation %d decision=%+v err=%v", index, decision, err)
		}
	}
}
