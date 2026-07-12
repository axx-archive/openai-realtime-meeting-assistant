package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrCanonicalAggregateConflict   = errors.New("canonical aggregate version conflict")
	ErrCanonicalIdempotencyConflict = errors.New("canonical idempotency key conflict")
	ErrCanonicalInvalidEvent        = errors.New("invalid canonical event")
)

// CanonicalPrincipalRef deliberately carries identity, not display metadata.
type CanonicalPrincipalRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// CanonicalEvent is the append-only fact contract. Payloads contain metadata
// only; erasable/user-authored content belongs behind ContentRef.
type CanonicalEvent struct {
	EventID           uuid.UUID             `json:"event_id"`
	TenantID          string                `json:"tenant_id"`
	AggregateType     string                `json:"aggregate_type"`
	AggregateID       string                `json:"aggregate_id"`
	AggregateVersion  int64                 `json:"aggregate_version"`
	EventType         string                `json:"event_type"`
	SchemaVersion     int                   `json:"schema_version"`
	OccurredAt        time.Time             `json:"occurred_at"`
	RecordedAt        time.Time             `json:"recorded_at"`
	Actor             CanonicalPrincipalRef `json:"actor"`
	RoomID            string                `json:"room_id,omitempty"`
	MeetingID         string                `json:"meeting_id,omitempty"`
	CorrelationID     string                `json:"correlation_id,omitempty"`
	CausationID       *uuid.UUID            `json:"causation_id,omitempty"`
	IdempotencyKey    string                `json:"idempotency_key,omitempty"`
	Classification    string                `json:"classification"`
	ConsentSnapshotID *uuid.UUID            `json:"consent_snapshot_id,omitempty"`
	ACLVersion        int64                 `json:"acl_version"`
	Payload           json.RawMessage       `json:"payload"`
	ContentRef        string                `json:"content_ref,omitempty"`
	PayloadSHA256     [32]byte              `json:"payload_sha256"`
	RetainUntil       *time.Time            `json:"retain_until,omitempty"`
}

func (event CanonicalEvent) Validate(registry *CanonicalPayloadRegistry) error {
	if event.EventID == uuid.Nil || strings.TrimSpace(event.TenantID) == "" ||
		strings.TrimSpace(event.AggregateType) == "" || strings.TrimSpace(event.AggregateID) == "" ||
		event.AggregateVersion < 1 || strings.TrimSpace(event.EventType) == "" || event.SchemaVersion < 1 ||
		event.OccurredAt.IsZero() || event.RecordedAt.IsZero() || strings.TrimSpace(event.Actor.Kind) == "" ||
		strings.TrimSpace(event.Actor.ID) == "" || strings.TrimSpace(event.Classification) == "" || event.ACLVersion < 0 {
		return ErrCanonicalInvalidEvent
	}
	if registry == nil {
		return fmt.Errorf("%w: payload registry is required", ErrCanonicalInvalidEvent)
	}
	normalized, err := registry.ValidateAndNormalize(event.EventType, event.SchemaVersion, event.Payload)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCanonicalInvalidEvent, err)
	}
	if sha256.Sum256(normalized) != event.PayloadSHA256 {
		return fmt.Errorf("%w: payload digest mismatch", ErrCanonicalInvalidEvent)
	}
	if !bytes.Equal(normalized, event.Payload) {
		return fmt.Errorf("%w: payload is not canonical JSON", ErrCanonicalInvalidEvent)
	}
	return nil
}

type CanonicalAppendResult struct {
	Event    CanonicalEvent
	Existing bool
}

type CanonicalEventStore interface {
	Append(context.Context, CanonicalEvent) (CanonicalAppendResult, error)
	Events(context.Context) ([]CanonicalEvent, error)
}

// MemoryCanonicalEventStore is a deterministic proof implementation. It
// mirrors the database uniqueness constraints for event IDs, aggregate
// versions, and tenant-scoped idempotency keys.
type MemoryCanonicalEventStore struct {
	mu          sync.Mutex
	registry    *CanonicalPayloadRegistry
	events      []CanonicalEvent
	byEvent     map[uuid.UUID]int
	byAggregate map[string]int
	byIdem      map[string]int
}

func NewMemoryCanonicalEventStore(registry *CanonicalPayloadRegistry) *MemoryCanonicalEventStore {
	return &MemoryCanonicalEventStore{
		registry: registry, byEvent: make(map[uuid.UUID]int),
		byAggregate: make(map[string]int), byIdem: make(map[string]int),
	}
}

func (store *MemoryCanonicalEventStore) Append(_ context.Context, event CanonicalEvent) (CanonicalAppendResult, error) {
	if err := event.Validate(store.registry); err != nil {
		return CanonicalAppendResult{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	fingerprint, err := canonicalEventFingerprint(event)
	if err != nil {
		return CanonicalAppendResult{}, err
	}
	if index, ok := store.byEvent[event.EventID]; ok {
		existing := store.events[index]
		existingFingerprint, _ := canonicalEventFingerprint(existing)
		if existingFingerprint != fingerprint {
			return CanonicalAppendResult{}, ErrCanonicalIdempotencyConflict
		}
		return CanonicalAppendResult{Event: cloneCanonicalEvent(existing), Existing: true}, nil
	}
	if event.IdempotencyKey != "" {
		key := event.TenantID + "\x00" + event.IdempotencyKey
		if index, ok := store.byIdem[key]; ok {
			existing := store.events[index]
			existingFingerprint, _ := canonicalEventFingerprint(existing)
			if existingFingerprint != fingerprint {
				return CanonicalAppendResult{}, ErrCanonicalIdempotencyConflict
			}
			return CanonicalAppendResult{Event: cloneCanonicalEvent(existing), Existing: true}, nil
		}
	}
	aggregateKey := canonicalAggregateKey(event.TenantID, event.AggregateType, event.AggregateID, event.AggregateVersion)
	if _, exists := store.byAggregate[aggregateKey]; exists {
		return CanonicalAppendResult{}, ErrCanonicalAggregateConflict
	}

	copyEvent := cloneCanonicalEvent(event)
	index := len(store.events)
	store.events = append(store.events, copyEvent)
	store.byEvent[event.EventID] = index
	store.byAggregate[aggregateKey] = index
	if event.IdempotencyKey != "" {
		store.byIdem[event.TenantID+"\x00"+event.IdempotencyKey] = index
	}
	return CanonicalAppendResult{Event: cloneCanonicalEvent(copyEvent)}, nil
}

func (store *MemoryCanonicalEventStore) Events(_ context.Context) ([]CanonicalEvent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]CanonicalEvent, len(store.events))
	for index := range store.events {
		result[index] = cloneCanonicalEvent(store.events[index])
	}
	return result, nil
}

func canonicalAggregateKey(tenant, family, object string, version int64) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%020d", tenant, family, object, version)
}

func canonicalEventFingerprint(event CanonicalEvent) (string, error) {
	copyEvent := cloneCanonicalEvent(event)
	copyEvent.EventID = uuid.Nil       // retries may mint a fresh transport/event UUID
	copyEvent.RecordedAt = time.Time{} // ingestion time is not semantic identity
	data, err := json.Marshal(copyEvent)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func cloneCanonicalEvent(event CanonicalEvent) CanonicalEvent {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return event
}

// CanonicalObjectVersionSnapshot is the portable, checksummed state that a
// durable adapter must persist atomically. This layer intentionally performs
// no file I/O.
type CanonicalObjectVersionSnapshot struct {
	Entries  []CanonicalObjectVersionEntry `json:"entries"`
	Checksum [32]byte                      `json:"checksum"`
}

type CanonicalObjectVersionEntry struct {
	Family      string `json:"family"`
	ObjectKey   string `json:"object_key"`
	StateDigest string `json:"state_digest"`
	Version     int64  `json:"version"`
}

type CanonicalObjectVersionMap interface {
	ResolveVersion(family, objectKey, stateDigest string) (version int64, existing bool, err error)
	Snapshot() (CanonicalObjectVersionSnapshot, error)
}

// CanonicalDurableObjectVersionMap is the durability boundary implemented by
// spool/database adapters. A successful Persist must mean fsync-equivalent
// durability for the supplied checksum.
type CanonicalDurableObjectVersionMap interface {
	CanonicalObjectVersionMap
	Persist(context.Context, CanonicalObjectVersionSnapshot) error
}

type MemoryCanonicalObjectVersionMap struct {
	mu       sync.Mutex
	versions map[string]map[string]int64
	max      map[string]int64
}

func NewMemoryCanonicalObjectVersionMap() *MemoryCanonicalObjectVersionMap {
	return &MemoryCanonicalObjectVersionMap{versions: make(map[string]map[string]int64), max: make(map[string]int64)}
}

func (versionMap *MemoryCanonicalObjectVersionMap) ResolveVersion(family, objectKey, stateDigest string) (int64, bool, error) {
	if strings.TrimSpace(family) == "" || strings.TrimSpace(objectKey) == "" || !isHexDigest(stateDigest) {
		return 0, false, errors.New("family, object key, and SHA-256 state digest are required")
	}
	versionMap.mu.Lock()
	defer versionMap.mu.Unlock()
	object := family + "\x00" + objectKey
	if versionMap.versions[object] == nil {
		versionMap.versions[object] = make(map[string]int64)
	}
	if version, ok := versionMap.versions[object][stateDigest]; ok {
		return version, true, nil
	}
	versionMap.max[object]++
	version := versionMap.max[object]
	versionMap.versions[object][stateDigest] = version
	return version, false, nil
}

func (versionMap *MemoryCanonicalObjectVersionMap) Snapshot() (CanonicalObjectVersionSnapshot, error) {
	versionMap.mu.Lock()
	defer versionMap.mu.Unlock()
	var entries []CanonicalObjectVersionEntry
	for object, digests := range versionMap.versions {
		parts := strings.SplitN(object, "\x00", 2)
		for digest, version := range digests {
			entries = append(entries, CanonicalObjectVersionEntry{Family: parts[0], ObjectKey: parts[1], StateDigest: digest, Version: version})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Family != entries[j].Family {
			return entries[i].Family < entries[j].Family
		}
		if entries[i].ObjectKey != entries[j].ObjectKey {
			return entries[i].ObjectKey < entries[j].ObjectKey
		}
		return entries[i].StateDigest < entries[j].StateDigest
	})
	data, err := canonicalJSON(entries)
	if err != nil {
		return CanonicalObjectVersionSnapshot{}, err
	}
	return CanonicalObjectVersionSnapshot{Entries: entries, Checksum: sha256.Sum256(data)}, nil
}

func isHexDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
