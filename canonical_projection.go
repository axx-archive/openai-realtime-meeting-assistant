package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

var ErrCanonicalProjectionOrder = errors.New("canonical projection aggregate version out of order")

type CanonicalProjectedObject struct {
	TenantID         string `json:"tenant_id"`
	AggregateType    string `json:"aggregate_type"`
	AggregateID      string `json:"aggregate_id"`
	AggregateVersion int64  `json:"aggregate_version"`
	EventID          string `json:"event_id"`
	EventType        string `json:"event_type"`
	Classification   string `json:"classification"`
	RoomID           string `json:"room_id"`
	MeetingID        string `json:"meeting_id"`
	ACLVersion       int64  `json:"acl_version"`
	ContentRef       string `json:"content_ref"`
	PayloadSHA256    string `json:"payload_sha256"`
	Deleted          bool   `json:"deleted"`
}

type CanonicalProjection struct {
	objects map[string]CanonicalProjectedObject
	events  map[string]string
}

func NewCanonicalProjection() *CanonicalProjection {
	return &CanonicalProjection{objects: make(map[string]CanonicalProjectedObject), events: make(map[string]string)}
}

func (projection *CanonicalProjection) Apply(event CanonicalEvent) error {
	fingerprint, err := canonicalEventFingerprint(event)
	if err != nil {
		return err
	}
	eventKey := event.EventID.String()
	if existing, ok := projection.events[eventKey]; ok {
		if existing != fingerprint {
			return ErrCanonicalIdempotencyConflict
		}
		return nil
	}
	key := canonicalAggregateKey(event.TenantID, event.AggregateType, event.AggregateID, 0)
	current, exists := projection.objects[key]
	expected := int64(1)
	if exists {
		expected = current.AggregateVersion + 1
	}
	if event.AggregateVersion != expected {
		return ErrCanonicalProjectionOrder
	}
	projection.objects[key] = CanonicalProjectedObject{
		TenantID: event.TenantID, AggregateType: event.AggregateType, AggregateID: event.AggregateID,
		AggregateVersion: event.AggregateVersion, EventID: event.EventID.String(), EventType: event.EventType,
		Classification: event.Classification, RoomID: NormalizeCanonicalRoomID(event.RoomID), MeetingID: event.MeetingID,
		ACLVersion: event.ACLVersion, ContentRef: event.ContentRef,
		PayloadSHA256: hex.EncodeToString(event.PayloadSHA256[:]), Deleted: canonicalDeletionEvent(event.EventType),
	}
	projection.events[eventKey] = fingerprint
	return nil
}

func (projection *CanonicalProjection) Objects() []CanonicalProjectedObject {
	objects := make([]CanonicalProjectedObject, 0, len(projection.objects))
	for _, object := range projection.objects {
		objects = append(objects, object)
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].TenantID != objects[j].TenantID {
			return objects[i].TenantID < objects[j].TenantID
		}
		if objects[i].AggregateType != objects[j].AggregateType {
			return objects[i].AggregateType < objects[j].AggregateType
		}
		return objects[i].AggregateID < objects[j].AggregateID
	})
	return objects
}

func (projection *CanonicalProjection) Checksum() ([32]byte, error) {
	data, err := canonicalJSON(projection.Objects())
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func canonicalDeletionEvent(eventType string) bool {
	return strings.HasSuffix(eventType, ".deleted") || strings.HasSuffix(eventType, ".withdrawn") || strings.HasSuffix(eventType, ".revoked")
}
