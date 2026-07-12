package main

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func testCanonicalRegistry(t *testing.T) *CanonicalPayloadRegistry {
	t.Helper()
	registry := NewCanonicalPayloadRegistry()
	err := registry.Register("artifact.revised", 1, CanonicalPayloadSchema{Fields: map[string]CanonicalPayloadField{
		"artifact_id":      {Kind: CanonicalPayloadIdentifier, Required: true},
		"content_revision": {Kind: CanonicalPayloadRevision, Required: true},
		"content_sha256":   {Kind: CanonicalPayloadDigest, Required: true},
		"content_ref":      {Kind: CanonicalPayloadContentRef},
		"visibility":       {Kind: CanonicalPayloadEnum, Required: true, Enums: []string{"private", "organization"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestCanonicalPayloadRegistryFailsClosed(t *testing.T) {
	registry := testCanonicalRegistry(t)
	digest := "8d969eef6ecad3c29a3a629280e686cff8ca5a86a4a1bb5c9f2d7f2e725a67c6"
	valid := json.RawMessage(`{"visibility":"private","content_sha256":"` + digest + `","content_revision":2,"artifact_id":"a-1"}`)
	normalized, err := registry.ValidateAndNormalize("artifact.revised", 1, valid)
	if err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	// Optional absent fields stay absent, while keys are canonicalized.
	want := `{"artifact_id":"a-1","content_revision":2,"content_sha256":"` + digest + `","visibility":"private"}`
	if string(normalized) != want {
		t.Fatalf("normalized payload = %s", normalized)
	}

	cases := []struct {
		name      string
		eventType string
		raw       string
	}{
		{"unknown schema", "artifact.created", string(valid)},
		{"unknown field", "artifact.revised", `{"artifact_id":"a","content_revision":1,"content_sha256":"` + digest + `","visibility":"private","extra":true}`},
		{"content body", "artifact.revised", `{"artifact_id":"a","content_revision":1,"content_sha256":"` + digest + `","visibility":"private","body":"secret"}`},
		{"wrong type", "artifact.revised", `{"artifact_id":"a","content_revision":1.5,"content_sha256":"` + digest + `","visibility":"private"}`},
		{"bad enum", "artifact.revised", `{"artifact_id":"a","content_revision":1,"content_sha256":"` + digest + `","visibility":"world"}`},
		{"multiple values", "artifact.revised", string(valid) + `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := registry.ValidateAndNormalize(tc.eventType, 1, json.RawMessage(tc.raw)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	if err := registry.Register("unsafe.created", 1, CanonicalPayloadSchema{Fields: map[string]CanonicalPayloadField{"prompt": {Kind: CanonicalPayloadIdentifier}}}); err == nil {
		t.Fatal("expected prohibited schema field rejection")
	}
}

func TestCanonicalImportIdentityIgnoresRecordOrder(t *testing.T) {
	stateA := map[string]any{"id": "a", "count": 2}
	stateB := map[string]any{"count": 2, "id": "a"}
	digestA, err := CanonicalStateDigest(stateA)
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := CanonicalStateDigest(stateB)
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestB {
		t.Fatalf("state digest depends on map order: %s != %s", digestA, digestB)
	}
	key, err := CanonicalLegacyObjectKey("board.card", "room-1", "card-a")
	if err != nil {
		t.Fatal(err)
	}
	id1, err := CanonicalImportEventID("tenant-a", "board.card", key, "created", digestA)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := CanonicalImportEventID("tenant-a", "board.card", key, "created", digestB)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 || id1.Version() != 5 {
		t.Fatalf("non-deterministic UUIDv5: %s %s", id1, id2)
	}
	otherTenant, err := CanonicalImportEventID("tenant-b", "board.card", key, "created", digestB)
	if err != nil {
		t.Fatal(err)
	}
	if otherTenant == id1 {
		t.Fatal("identical legacy objects in different tenants shared an event ID")
	}
	if NormalizeCanonicalRoomID("  ") != "office" {
		t.Fatal("blank legacy room must normalize to office")
	}
}

func TestCanonicalStateDigestRejectsUnsafeLargeIntegers(t *testing.T) {
	if _, err := CanonicalStateDigest(map[string]any{"sequence": int64(9007199254740992)}); err == nil {
		t.Fatal("unsafe JCS integer was accepted and could collide cross-language")
	}
}

func TestCanonicalJSONRFC8785EdgeCases(t *testing.T) {
	got, err := canonicalJSON(map[string]any{"z": -0.0, "html": "<>&", "a": 1e-7})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":1e-7,"html":"<>&","z":0}`
	if string(got) != want {
		t.Fatalf("JCS output = %s, want %s", got, want)
	}
}

func TestCanonicalJSONRejectsUnsafeIntegralSpellings(t *testing.T) {
	for _, raw := range []string{"9007199254740993", "9007199254740993.0", "9.007199254740993e15"} {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		if _, err := canonicalJSON(value); err == nil {
			t.Fatalf("unsafe integral spelling %s was accepted", raw)
		}
	}
}

func TestCanonicalPayloadSchemaIsImmutableAfterRegistration(t *testing.T) {
	registry := NewCanonicalPayloadRegistry()
	enums := []string{"private"}
	fields := map[string]CanonicalPayloadField{
		"visibility": {Kind: CanonicalPayloadEnum, Required: true, Enums: enums},
	}
	if err := registry.Register("artifact.visibility", 1, CanonicalPayloadSchema{Fields: fields}); err != nil {
		t.Fatal(err)
	}
	fields["title"] = CanonicalPayloadField{Kind: CanonicalPayloadIdentifier}
	enums[0] = "mutated"
	if _, err := registry.ValidateAndNormalize("artifact.visibility", 1, json.RawMessage(`{"visibility":"private"}`)); err != nil {
		t.Fatalf("registered schema changed through caller aliases: %v", err)
	}
	if _, err := registry.ValidateAndNormalize("artifact.visibility", 1, json.RawMessage(`{"visibility":"mutated"}`)); err == nil {
		t.Fatal("mutated caller enum changed registered schema")
	}
	if err := registry.Register("unsafe.status", 1, CanonicalPayloadSchema{Fields: map[string]CanonicalPayloadField{
		"status": {Kind: CanonicalPayloadIdentifier},
	}}); err == nil {
		t.Fatal("status registered with unrestricted identifier kind")
	}
}

func TestCanonicalPayloadSchemaUsesPositiveMetadataVocabulary(t *testing.T) {
	for _, field := range []string{"title", "summary", "description", "message", "notes", "excerpt", "api_key", "credential"} {
		registry := NewCanonicalPayloadRegistry()
		err := registry.Register("unsafe.created", 1, CanonicalPayloadSchema{Fields: map[string]CanonicalPayloadField{
			field: {Kind: CanonicalPayloadIdentifier, Required: true},
		}})
		if err == nil {
			t.Fatalf("immutable authored/secret field %q was accepted", field)
		}
	}
}

func TestCanonicalObjectVersionMapStableAndChecksummed(t *testing.T) {
	versionMap := NewMemoryCanonicalObjectVersionMap()
	digests := []string{
		hex.EncodeToString(make([]byte, 32)),
		hex.EncodeToString(append(make([]byte, 31), 1)),
	}
	version, existing, err := versionMap.ResolveVersion("artifact", "a", digests[0])
	if err != nil || version != 1 || existing {
		t.Fatalf("first resolve = %d %v %v", version, existing, err)
	}
	version, existing, err = versionMap.ResolveVersion("artifact", "a", digests[0])
	if err != nil || version != 1 || !existing {
		t.Fatalf("repeat resolve = %d %v %v", version, existing, err)
	}
	if _, _, err := versionMap.ResolveVersion("artifact", "0-earlier-unrelated", digests[1]); err != nil {
		t.Fatal(err)
	}
	version, existing, err = versionMap.ResolveVersion("artifact", "a", digests[0])
	if err != nil || version != 1 || !existing {
		t.Fatalf("unrelated record changed stable version = %d %v %v", version, existing, err)
	}
	version, existing, err = versionMap.ResolveVersion("artifact", "a", digests[1])
	if err != nil || version != 2 || existing {
		t.Fatalf("new state resolve = %d %v %v", version, existing, err)
	}
	other, _, err := versionMap.ResolveVersion("artifact", "b", digests[1])
	if err != nil || other != 1 {
		t.Fatalf("versions must be per object: %d %v", other, err)
	}
	first, err := versionMap.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := versionMap.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum || len(first.Entries) != 4 {
		t.Fatal("snapshot is not stable")
	}
}
