package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type canonicalTestParityACL map[string]map[string]bool

func (resolver canonicalTestParityACL) CanReadCanonicalObject(_ context.Context, principal string, event CanonicalEvent) (bool, error) {
	return resolver[event.AggregateType+"\x00"+event.AggregateID][principal], nil
}

func canonicalParityACLFromPlan(plan CanonicalImportPlan) canonicalTestParityACL {
	resolver := canonicalTestParityACL{}
	for _, object := range plan.Objects {
		key := object.Family + "\x00" + object.ObjectID
		resolver[key] = map[string]bool{}
		for _, principal := range object.Principals {
			resolver[key][principal] = true
		}
	}
	return resolver
}

func TestCanonicalReconcilerReportsMissingWithoutWriting(t *testing.T) {
	paths := canonicalImportFixture(t)
	plan, registry := buildCanonicalFixturePlan(t, paths, filepath.Join(t.TempDir(), "versions.json"))
	store := NewMemoryCanonicalEventStore(registry)
	for _, event := range plan.Events[:len(plan.Events)-1] {
		if _, err := store.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := store.Events(context.Background())
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), plan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(plan)})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Diverged || len(report.Candidates) != 1 || report.Candidates[0].Kind != "missing_event" || report.Candidates[0].Event == nil {
		t.Fatalf("report=%+v", report)
	}
	after, _ := store.Events(context.Background())
	if len(after) != len(before) {
		t.Fatalf("reconciler mutated store: %d -> %d", len(before), len(after))
	}
}

func TestCanonicalReconcilerTargetVisibilityComesFromACLResolver(t *testing.T) {
	paths := canonicalImportFixture(t)
	plan, registry := buildCanonicalFixturePlan(t, paths, filepath.Join(t.TempDir(), "versions.json"))
	store := NewMemoryCanonicalEventStore(registry)
	if err := plan.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	resolver := canonicalParityACLFromPlan(plan)
	var target CanonicalImportedObject
	for _, object := range plan.Objects {
		if len(object.Principals) > 0 {
			target = object
			break
		}
	}
	key := target.Family + "\x00" + target.ObjectID
	missingPrincipal := target.Principals[0]
	delete(resolver[key], missingPrincipal)
	resolver[key]["user:unexpected"] = true
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), plan, store, CanonicalReconcileOptions{ACL: resolver, TestedPrincipals: []string{"user:unexpected"}})
	if err != nil {
		t.Fatal(err)
	}
	missing, extra := false, false
	for _, candidate := range report.Candidates {
		if candidate.Family == target.Family && candidate.ObjectID == target.ObjectID && candidate.Principal == missingPrincipal && candidate.Kind == "principal_missing_access" {
			missing = true
		}
		if candidate.Family == target.Family && candidate.ObjectID == target.ObjectID && candidate.Principal == "user:unexpected" && candidate.Kind == "principal_extra_access" {
			extra = true
		}
	}
	if !missing || !extra || !report.Diverged || !report.PrincipalParityProven {
		t.Fatalf("ACL parity report=%+v", report)
	}
}

func TestCanonicalReconcilerCollapsesContiguousHistoryToCurrentProjection(t *testing.T) {
	paths := canonicalImportFixture(t)
	versionPath := filepath.Join(t.TempDir(), "versions.json")
	first, registry := buildCanonicalFixturePlan(t, paths, versionPath)
	store := NewMemoryCanonicalEventStore(registry)
	if err := first.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	var board kanbanBoardState
	if ok, err := readJSONIfExists(paths.Board, &board); err != nil || !ok {
		t.Fatal(err)
	}
	board.Cards[0].Title = "changed current state"
	writeCanonicalFixtureJSON(t, paths.Board, board)
	second, _ := buildCanonicalFixturePlan(t, paths, versionPath)
	if err := second.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), second, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(second)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Diverged || len(report.Candidates) != 0 {
		t.Fatalf("contiguous history did not reduce to current projection: %+v", report)
	}
	if report.Target.Families["board_card"].Count != report.Source.Families["board_card"].Count {
		t.Fatalf("history inflated target count: source=%+v target=%+v", report.Source.Families["board_card"], report.Target.Families["board_card"])
	}
}

func TestCanonicalReconcilerEmitsTypedStateMismatchEvidence(t *testing.T) {
	paths := canonicalImportFixture(t)
	versionPath := filepath.Join(t.TempDir(), "versions.json")
	first, registry := buildCanonicalFixturePlan(t, paths, versionPath)
	store := NewMemoryCanonicalEventStore(registry)
	if err := first.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	var board kanbanBoardState
	if ok, err := readJSONIfExists(paths.Board, &board); err != nil || !ok {
		t.Fatal(err)
	}
	changedID := board.Cards[0].ID
	board.Cards[0].Title = "new source state not yet imported"
	writeCanonicalFixtureJSON(t, paths.Board, board)
	second, _ := buildCanonicalFixturePlan(t, paths, versionPath)
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), second, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(second)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range report.Candidates {
		if candidate.Family == "board_card" && candidate.ObjectID == changedID && candidate.Kind == "state_mismatch" {
			found = true
			if candidate.SourceStateDigest == "" || candidate.TargetStateDigest == "" || candidate.SourceStateDigest == candidate.TargetStateDigest || candidate.SourceVersion != 2 || candidate.TargetVersion != 1 {
				t.Fatalf("state mismatch lacks evidence: %+v", candidate)
			}
		}
	}
	if !found {
		t.Fatalf("state mismatch missing: %+v", report.Candidates)
	}
	before, _ := store.Events(context.Background())
	after, _ := store.Events(context.Background())
	if len(before) != len(after) {
		t.Fatal("state mismatch reconciliation mutated store")
	}
}

func TestCanonicalReconcilerIsStrictlyTenantScoped(t *testing.T) {
	paths := canonicalImportFixture(t)
	plan, registry := buildCanonicalFixturePlan(t, paths, filepath.Join(t.TempDir(), "versions.json"))
	store := NewMemoryCanonicalEventStore(registry)
	if err := plan.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	foreignSame := plan.Events[0]
	foreignSame.EventID = uuid.New()
	foreignSame.TenantID = "tenant-b"
	foreignSame.IdempotencyKey = "tenant-b-same"
	if _, err := store.Append(context.Background(), foreignSame); err != nil {
		t.Fatal(err)
	}
	foreignOnly := foreignSame
	foreignOnly.EventID = uuid.New()
	foreignOnly.AggregateID = "tenant-b-only"
	foreignOnly.IdempotencyKey = "tenant-b-only"
	payload, digest, err := NewCanonicalEventPayload(registry, canonicalLegacyImportEventType, 1, map[string]any{
		"object_id": "tenant-b-only", "source_kind": foreignOnly.AggregateType, "source_revision": int64(1),
		"room_id": "office", "status": "active", "deleted": false, "payload_sha256": strings.Repeat("e", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	foreignOnly.Payload, foreignOnly.PayloadSHA256 = payload, digest
	if _, err := store.Append(context.Background(), foreignOnly); err != nil {
		t.Fatal(err)
	}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), plan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(plan)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Diverged || len(report.Candidates) != 0 {
		t.Fatalf("foreign tenant contaminated tenant A parity: %+v", report)
	}
	for _, family := range report.Target.Families {
		for _, id := range family.IDs {
			if id == foreignOnly.AggregateID {
				t.Fatalf("foreign-only object appeared in target parity: %+v", report.Target)
			}
		}
	}
}

func TestCanonicalReconcilerRejectsBlankTenant(t *testing.T) {
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ReconcileCanonicalPlanWithStore(context.Background(), CanonicalImportPlan{}, NewMemoryCanonicalEventStore(registry))
	if err == nil || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("blank tenant error=%v", err)
	}
}

func TestCanonicalReconcilerParityAfterIdempotentApply(t *testing.T) {
	paths := canonicalImportFixture(t)
	plan, registry := buildCanonicalFixturePlan(t, paths, filepath.Join(t.TempDir(), "versions.json"))
	store := NewMemoryCanonicalEventStore(registry)
	if err := plan.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), plan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(plan)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Diverged || len(report.Candidates) != 0 {
		t.Fatalf("parity report=%+v", report)
	}
}

func TestCanonicalReconcilerRequiresJournalBeforeTombstone(t *testing.T) {
	paths := canonicalImportFixture(t)
	plan, registry := buildCanonicalFixturePlan(t, paths, filepath.Join(t.TempDir(), "versions.json"))
	store := NewMemoryCanonicalEventStore(registry)
	if err := plan.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	// Add the object whose deletion journal is already present as memory:old.
	var template CanonicalEvent
	for _, event := range plan.Events {
		if event.AggregateType == "memory" {
			template = event
			break
		}
	}
	template.EventID = canonicalImportUUIDForTest(t, "memory-old")
	template.AggregateID = "old"
	template.IdempotencyKey = "extra-memory-old"
	payload, digest, err := NewCanonicalEventPayload(registry, canonicalLegacyImportEventType, 1, map[string]any{
		"object_id": "old", "source_kind": "memory", "source_revision": int64(1), "room_id": "office", "status": "active", "deleted": false,
		"payload_sha256": stringRepeatForTest("d", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	template.Payload, template.PayloadSHA256 = payload, digest
	if _, err := store.Append(context.Background(), template); err != nil {
		t.Fatal(err)
	}
	report, err := ReconcileCanonicalPlanWithStore(context.Background(), plan, store)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range report.Candidates {
		if candidate.Family == "memory" && candidate.ObjectID == "old" && candidate.Kind == "tombstone_required" {
			found = true
			if !candidate.ConfirmedByJournal {
				t.Fatalf("candidate not journal-confirmed: %+v", candidate)
			}
		}
	}
	if !found {
		t.Fatalf("report lacks memory/old tombstone candidate: %+v", report.Candidates)
	}
}

func canonicalImportUUIDForTest(t *testing.T, value string) uuid.UUID {
	t.Helper()
	return uuid.NewSHA1(canonicalImportNamespace, []byte(value))
}
func stringRepeatForTest(value string, count int) string { return strings.Repeat(value, count) }
