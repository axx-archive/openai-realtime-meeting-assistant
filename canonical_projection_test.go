package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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

func canonicalLegacyBoardTestEvent(t *testing.T, registry *CanonicalPayloadRegistry, aggregateID string, deleted bool) CanonicalEvent {
	t.Helper()
	stateDigest := strings.Repeat("a", 64)
	objectKey, err := CanonicalLegacyObjectKey("board_card", aggregateID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := CanonicalImportEventID("tenant-a", "board_card", objectKey, canonicalLegacyImportEventType, stateDigest)
	if err != nil {
		t.Fatal(err)
	}
	payload, payloadDigest, err := NewCanonicalEventPayload(registry, canonicalLegacyImportEventType, 1, map[string]any{
		"object_id": aggregateID, "source_kind": "board_card", "source_revision": 1,
		"room_id": "office", "status": "unknown", "payload_sha256": stateDigest, "deleted": deleted,
		"content_revision": 1, "content_sha256": stateDigest, "content_ref": "legacy:board-card:" + aggregateID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 7, 10, 12, 0, 0, 123456000, time.UTC)
	return CanonicalEvent{
		EventID: id, TenantID: "tenant-a", AggregateType: "board_card", AggregateID: aggregateID, AggregateVersion: 1,
		EventType: canonicalLegacyImportEventType, SchemaVersion: 1, OccurredAt: stamp, RecordedAt: stamp,
		Actor: CanonicalPrincipalRef{Kind: "service", ID: "legacy-import"}, RoomID: "office",
		IdempotencyKey: "legacy-import/" + id.String(), Classification: "internal", ACLVersion: 1,
		Payload: payload, ContentRef: "legacy:board-card:" + aggregateID, PayloadSHA256: payloadDigest,
	}
}

func canonicalLegacyBaselineTestEvent(t *testing.T, registry *CanonicalPayloadRegistry, family, aggregateID string, version int64) CanonicalEvent {
	t.Helper()
	stateDigest := hexDigest(sha256.Sum256([]byte(family + "/" + aggregateID + "/" + strconv.FormatInt(version, 10))))
	objectKey, err := CanonicalLegacyObjectKey(family, aggregateID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := CanonicalImportEventID("tenant-a", family, objectKey, canonicalLegacyImportEventType, stateDigest)
	if err != nil {
		t.Fatal(err)
	}
	payload, payloadDigest, err := NewCanonicalEventPayload(registry, canonicalLegacyImportEventType, 1, map[string]any{
		"object_id": aggregateID, "source_kind": family, "source_revision": version,
		"room_id": "office", "status": "active", "payload_sha256": stateDigest, "deleted": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 7, 10, 13, 0, 0, 123456000, time.UTC)
	return CanonicalEvent{
		EventID: id, TenantID: "tenant-a", AggregateType: family, AggregateID: aggregateID, AggregateVersion: version,
		EventType: canonicalLegacyImportEventType, SchemaVersion: 1, OccurredAt: stamp, RecordedAt: stamp,
		Actor: CanonicalPrincipalRef{Kind: "service", ID: "legacy-import"}, RoomID: "office",
		IdempotencyKey: "legacy-import/" + id.String(), Classification: "internal", ACLVersion: 1,
		Payload: payload, PayloadSHA256: payloadDigest,
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

func TestMemoryCanonicalLegacyImportRetryIgnoresOnlyOccurrenceDrift(t *testing.T) {
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryCanonicalEventStore(registry)
	event := canonicalLegacyBoardTestEvent(t, registry, "card-a", false)
	if _, err := store.Append(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	retry := event
	retry.OccurredAt = retry.OccurredAt.Add(18 * 24 * time.Hour)
	retry.RecordedAt = retry.RecordedAt.Add(18 * 24 * time.Hour)
	if result, err := store.Append(context.Background(), retry); err != nil || !result.Existing {
		t.Fatalf("legacy timestamp-only retry result=%+v err=%v", result, err)
	}

	retained := event.OccurredAt.Add(time.Hour)
	mutations := map[string]func(*CanonicalEvent){
		"event id":          func(candidate *CanonicalEvent) { candidate.EventID = uuid.New() },
		"tenant":            func(candidate *CanonicalEvent) { candidate.TenantID = "tenant-b" },
		"aggregate type":    func(candidate *CanonicalEvent) { candidate.AggregateType = "archive" },
		"aggregate id":      func(candidate *CanonicalEvent) { candidate.AggregateID = "card-b" },
		"aggregate version": func(candidate *CanonicalEvent) { candidate.AggregateVersion = 2 },
		"schema":            func(candidate *CanonicalEvent) { candidate.SchemaVersion = 2 },
		"actor":             func(candidate *CanonicalEvent) { candidate.Actor.ID = "other-importer" },
		"room":              func(candidate *CanonicalEvent) { candidate.RoomID = "room-b" },
		"idempotency":       func(candidate *CanonicalEvent) { candidate.IdempotencyKey = "legacy-import/other" },
		"classification":    func(candidate *CanonicalEvent) { candidate.Classification = "restricted" },
		"acl":               func(candidate *CanonicalEvent) { candidate.ACLVersion = 2 },
		"content ref":       func(candidate *CanonicalEvent) { candidate.ContentRef = "legacy:board-card:other" },
		"retention":         func(candidate *CanonicalEvent) { candidate.RetainUntil = &retained },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := retry
			mutate(&candidate)
			equal, err := canonicalEventsIdempotentlyEqual(event, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if equal {
				t.Fatal("semantic drift was accepted")
			}
		})
	}
	payloadMutations := map[string]func(map[string]any){
		"payload object":           func(payload map[string]any) { payload["object_id"] = "card-b" },
		"payload source":           func(payload map[string]any) { payload["source_kind"] = "archive" },
		"payload source revision":  func(payload map[string]any) { payload["source_revision"] = 2 },
		"payload room":             func(payload map[string]any) { payload["room_id"] = "room-b" },
		"payload status":           func(payload map[string]any) { payload["status"] = "active" },
		"payload content revision": func(payload map[string]any) { payload["content_revision"] = 2 },
		"payload content digest":   func(payload map[string]any) { payload["content_sha256"] = strings.Repeat("b", 64) },
		"payload state digest":     func(payload map[string]any) { payload["payload_sha256"] = strings.Repeat("b", 64) },
		"payload content ref":      func(payload map[string]any) { payload["content_ref"] = "legacy:board-card:card-b" },
	}
	for name, mutate := range payloadMutations {
		t.Run(name, func(t *testing.T) {
			candidate := retry
			var payload map[string]any
			if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			mutate(payload)
			candidate.Payload, candidate.PayloadSHA256, err = NewCanonicalEventPayload(registry, canonicalLegacyImportEventType, 1, payload)
			if err != nil {
				t.Fatal(err)
			}
			equal, err := canonicalEventsIdempotentlyEqual(event, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if equal {
				t.Fatal("payload binding drift was accepted")
			}
		})
	}
	deleted := canonicalLegacyBoardTestEvent(t, registry, event.AggregateID, true)
	deleted.OccurredAt = retry.OccurredAt
	if equal, err := canonicalEventsIdempotentlyEqual(event, deleted); err != nil || equal {
		t.Fatalf("deleted board import equality=%v err=%v", equal, err)
	}
	otherFamily := retry
	otherFamily.AggregateType = "archive"
	if equal, err := canonicalEventsIdempotentlyEqual(event, otherFamily); err != nil || equal {
		t.Fatalf("other-family equality=%v err=%v", equal, err)
	}

	native := canonicalTestEvent(t, testCanonicalRegistry(t), uuid.New(), "native-a", 1, "native-1", "private")
	nativeStore := NewMemoryCanonicalEventStore(testCanonicalRegistry(t))
	if _, err := nativeStore.Append(context.Background(), native); err != nil {
		t.Fatal(err)
	}
	nativeRetry := native
	nativeRetry.OccurredAt = nativeRetry.OccurredAt.Add(time.Second)
	if _, err := nativeStore.Append(context.Background(), nativeRetry); !errors.Is(err, ErrCanonicalIdempotencyConflict) {
		t.Fatalf("native timestamp drift error=%v, want idempotency conflict", err)
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

func TestCanonicalProjectionLegacyCheckpointCannotJumpOverNativeHistory(t *testing.T) {
	registry := testCanonicalRegistry(t)
	native := canonicalTestEvent(t, registry, uuid.New(), "meeting-projection-native", 1, "meeting-projection-v1", "private")
	native.TenantID = "tenant-a"
	native.AggregateType = "meeting"
	projection := NewCanonicalProjection()
	if err := projection.Apply(native); err != nil {
		t.Fatal(err)
	}
	legacyRegistry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := canonicalLegacyBaselineTestEvent(t, legacyRegistry, "meeting", native.AggregateID, 5)
	if err := projection.Apply(checkpoint); !errors.Is(err, ErrCanonicalProjectionOrder) {
		t.Fatalf("legacy checkpoint over native projection error=%v, want ErrCanonicalProjectionOrder", err)
	}
}
