package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func canonicalTestEvent(t *testing.T, registry *CanonicalPayloadRegistry, id uuid.UUID, aggregateID string, version int64, idem, visibility string) CanonicalEvent {
	t.Helper()
	digest := sha256.Sum256([]byte("content"))
	payload, payloadDigest, err := NewCanonicalEventPayload(registry, "artifact.revised", 1, map[string]any{
		"artifact_id": aggregateID, "content_revision": version, "content_sha256": hexDigest(digest), "visibility": visibility,
	})
	if err != nil {
		t.Fatal(err)
	}
	return CanonicalEvent{
		EventID: id, TenantID: "tenant-1", AggregateType: "artifact", AggregateID: aggregateID,
		AggregateVersion: version, EventType: "artifact.revised", SchemaVersion: 1,
		OccurredAt: time.Unix(version, 0).UTC(), RecordedAt: time.Unix(version+100, 0).UTC(),
		Actor: CanonicalPrincipalRef{Kind: "user", ID: "u-1"}, RoomID: "", IdempotencyKey: idem,
		Classification: "internal", ACLVersion: 1, Payload: payload, PayloadSHA256: payloadDigest,
	}
}

func hexDigest(digest [32]byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, len(digest)*2)
	for i, value := range digest {
		result[i*2] = digits[value>>4]
		result[i*2+1] = digits[value&15]
	}
	return string(result)
}

func TestMemoryCanonicalEventStoreIdempotencyAndVersionConflict(t *testing.T) {
	registry := testCanonicalRegistry(t)
	store := NewMemoryCanonicalEventStore(registry)
	event := canonicalTestEvent(t, registry, uuid.MustParse("01950c74-7d49-7cc2-ae84-51f3be0a8978"), "a", 1, "request-1", "private")
	first, err := store.Append(context.Background(), event)
	if err != nil || first.Existing {
		t.Fatalf("first append = %+v %v", first, err)
	}
	repeat, err := store.Append(context.Background(), event)
	if err != nil || !repeat.Existing || repeat.Event.EventID != event.EventID {
		t.Fatalf("repeat append = %+v %v", repeat, err)
	}
	retried := event
	retried.EventID = uuid.New()
	retried.RecordedAt = retried.RecordedAt.Add(time.Minute)
	repeat, err = store.Append(context.Background(), retried)
	if err != nil || !repeat.Existing || repeat.Event.EventID != event.EventID {
		t.Fatalf("regenerated retry append = %+v %v", repeat, err)
	}

	conflictingID := canonicalTestEvent(t, registry, uuid.New(), "a", 1, "request-2", "private")
	if _, err := store.Append(context.Background(), conflictingID); !errors.Is(err, ErrCanonicalAggregateConflict) {
		t.Fatalf("version conflict = %v", err)
	}
	conflictingIdem := canonicalTestEvent(t, registry, uuid.New(), "b", 1, "request-1", "organization")
	if _, err := store.Append(context.Background(), conflictingIdem); !errors.Is(err, ErrCanonicalIdempotencyConflict) {
		t.Fatalf("idempotency conflict = %v", err)
	}
}

func TestCanonicalProjectionReplayIsDeterministic(t *testing.T) {
	registry := testCanonicalRegistry(t)
	events := []CanonicalEvent{
		canonicalTestEvent(t, registry, uuid.MustParse("01950c74-7d49-7cc2-ae84-51f3be0a8978"), "b", 1, "b-1", "private"),
		canonicalTestEvent(t, registry, uuid.MustParse("01950c74-7d49-7cc2-ae84-51f3be0a8979"), "a", 1, "a-1", "organization"),
		canonicalTestEvent(t, registry, uuid.MustParse("01950c74-7d49-7cc2-ae84-51f3be0a8980"), "a", 2, "a-2", "private"),
	}
	first := NewCanonicalProjection()
	second := NewCanonicalProjection()
	for _, event := range events {
		if err := first.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range events {
		if err := second.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	checksum1, err := first.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	checksum2, err := second.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	if checksum1 != checksum2 {
		t.Fatal("replay checksum changed")
	}
	if err := first.Apply(events[2]); err != nil {
		t.Fatalf("duplicate event must be idempotent: %v", err)
	}
	checksum3, _ := first.Checksum()
	if checksum1 != checksum3 {
		t.Fatal("duplicate event changed projection")
	}

	outOfOrder := canonicalTestEvent(t, registry, uuid.New(), "c", 2, "c-2", "private")
	if err := first.Apply(outOfOrder); !errors.Is(err, ErrCanonicalProjectionOrder) {
		t.Fatalf("out of order = %v", err)
	}
}
