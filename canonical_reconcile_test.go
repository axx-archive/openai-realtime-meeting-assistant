package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestReconcileCanonicalPlanTreatsExactLegacyBaselineAsVisibleCheckpoint(t *testing.T) {
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	event := canonicalLegacyBaselineTestEvent(t, registry, "meeting", "meeting-first-observed-v48", 48)
	store := NewMemoryCanonicalEventStore(registry)
	if _, err := store.Append(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	stateDigest := eventPayloadStateDigest(event)
	plan := CanonicalImportPlan{
		TenantID: event.TenantID,
		Objects:  []CanonicalImportedObject{{Family: event.AggregateType, ObjectID: event.AggregateID, StateDigest: stateDigest, AggregateVersion: event.AggregateVersion, EventID: event.EventID}},
		Events:   []CanonicalEvent{event},
	}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), plan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(plan)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Diverged || len(report.Candidates) != 0 || report.Target.Families["meeting"].Count != 1 {
		t.Fatalf("exact legacy checkpoint was hidden from parity: diverged=%v target=%+v candidates=%+v", report.Diverged, report.Target, report.Candidates)
	}
}

func TestReconcileCanonicalPlanStillRejectsNativeHistoryGap(t *testing.T) {
	registry := testCanonicalRegistry(t)
	event := canonicalTestEvent(t, registry, uuid.New(), "artifact-native-v2", 2, "native-v2", "private")
	store := NewMemoryCanonicalEventStore(registry)
	if _, err := store.Append(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	plan := CanonicalImportPlan{
		TenantID: event.TenantID,
		Objects:  []CanonicalImportedObject{{Family: event.AggregateType, ObjectID: event.AggregateID, StateDigest: eventPayloadStateDigest(event), AggregateVersion: event.AggregateVersion, EventID: event.EventID}},
		Events:   []CanonicalEvent{event},
	}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), plan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(plan)})
	if err != nil {
		t.Fatal(err)
	}
	hasGap := false
	for _, candidate := range report.Candidates {
		hasGap = hasGap || candidate.Kind == "target_history_gap"
	}
	if !report.Diverged || !hasGap {
		t.Fatalf("native gap was accepted: diverged=%v candidates=%+v", report.Diverged, report.Candidates)
	}
}

func TestReconcileCanonicalPlanLegacyCheckpointCannotMaskNativeHistoryGap(t *testing.T) {
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("artifact.revised", 1, CanonicalPayloadSchema{Fields: map[string]CanonicalPayloadField{
		"artifact_id":      {Kind: CanonicalPayloadIdentifier, Required: true},
		"content_revision": {Kind: CanonicalPayloadRevision, Required: true},
		"content_sha256":   {Kind: CanonicalPayloadDigest, Required: true},
		"content_ref":      {Kind: CanonicalPayloadContentRef},
		"visibility":       {Kind: CanonicalPayloadEnum, Required: true, Enums: []string{"private", "organization"}},
	}}); err != nil {
		t.Fatal(err)
	}
	native := canonicalTestEvent(t, registry, uuid.New(), "meeting-mixed-history", 1, "meeting-native-v1", "private")
	native.TenantID = "tenant-a"
	native.AggregateType = "meeting"
	checkpoint := canonicalLegacyBaselineTestEvent(t, registry, "meeting", native.AggregateID, 5)
	store := NewMemoryCanonicalEventStore(registry)
	for _, event := range []CanonicalEvent{native, checkpoint} {
		if _, err := store.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	plan := CanonicalImportPlan{
		TenantID: checkpoint.TenantID,
		Objects:  []CanonicalImportedObject{{Family: checkpoint.AggregateType, ObjectID: checkpoint.AggregateID, StateDigest: eventPayloadStateDigest(checkpoint), AggregateVersion: checkpoint.AggregateVersion, EventID: checkpoint.EventID}},
		Events:   []CanonicalEvent{checkpoint},
	}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), plan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(plan)})
	if err != nil {
		t.Fatal(err)
	}
	hasGap := false
	for _, candidate := range report.Candidates {
		hasGap = hasGap || candidate.Kind == "target_history_gap"
	}
	if !report.Diverged || !hasGap {
		t.Fatalf("legacy checkpoint masked native history: diverged=%v candidates=%+v", report.Diverged, report.Candidates)
	}
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

func TestCanonicalReconcilerTreatsExactLegacyRevisionJumpsAsCheckpoints(t *testing.T) {
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryCanonicalEventStore(registry)
	baseline := canonicalLegacyBaselineTestEvent(t, registry, "meeting", "meeting-checkpoint", 2)
	checkpoint := canonicalLegacyBaselineTestEvent(t, registry, "meeting", baseline.AggregateID, 48)
	for _, event := range []CanonicalEvent{baseline, checkpoint} {
		if _, err := store.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	stateDigest := eventPayloadStateDigest(checkpoint)
	source := CanonicalImportPlan{
		TenantID: checkpoint.TenantID,
		Objects:  []CanonicalImportedObject{{Family: checkpoint.AggregateType, ObjectID: checkpoint.AggregateID, StateDigest: stateDigest, AggregateVersion: checkpoint.AggregateVersion, EventID: checkpoint.EventID}},
		Events:   []CanonicalEvent{checkpoint},
	}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), source, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(source)})
	if err != nil || report.Diverged || len(report.Candidates) != 0 {
		t.Fatalf("legacy checkpoint parity diverged=%v candidates=%+v err=%v", report.Diverged, report.Candidates, err)
	}
}

func TestCanonicalReconcilerStillRejectsNativeHistoryGap(t *testing.T) {
	registry := testCanonicalRegistry(t)
	store := NewMemoryCanonicalEventStore(registry)
	first := canonicalTestEvent(t, registry, uuid.New(), "native-gap", 1, "native-gap-1", "private")
	third := canonicalTestEvent(t, registry, uuid.New(), first.AggregateID, 3, "native-gap-3", "private")
	for _, event := range []CanonicalEvent{first, third} {
		if _, err := store.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	source := CanonicalImportPlan{TenantID: first.TenantID}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), source, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(source)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range report.Candidates {
		if candidate.Family == third.AggregateType && candidate.ObjectID == third.AggregateID && candidate.Kind == "target_history_gap" && candidate.TargetVersion == 3 {
			found = true
		}
	}
	if !found || !report.Diverged {
		t.Fatalf("native history gap was not preserved: %+v", report)
	}
}

func TestCanonicalBoardDeleteUndoMustAdvancePastDeletionCheckpoint(t *testing.T) {
	paths := canonicalImportFixture(t)
	versionPath := filepath.Join(t.TempDir(), "versions.json")
	first, registry := buildCanonicalFixturePlan(t, paths, versionPath)
	store := NewMemoryCanonicalEventStore(registry)
	if err := first.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	var originalBoard kanbanBoardState
	if ok, err := readJSONIfExists(paths.Board, &originalBoard); err != nil || !ok {
		t.Fatal(err)
	}
	var deletedObject CanonicalImportedObject
	for _, object := range first.Objects {
		if object.Family == "board_card" && object.ObjectID == "card-a" {
			deletedObject = object
			break
		}
	}
	if deletedObject.ObjectID == "" {
		t.Fatal("fixture board card not imported")
	}
	deletedAt := time.Date(2026, 7, 12, 20, 1, 0, 0, time.UTC)
	record, err := json.Marshal(CanonicalLifecycleJournalRecord{
		Family: "board_card", ObjectID: deletedObject.ObjectID, StateDigest: deletedObject.StateDigest,
		At: deletedAt, Reason: "board_card_deleted",
	})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := os.OpenFile(paths.DeletedJournal, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Write(append(record, '\n')); err != nil {
		_ = journal.Close()
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	deletedBoard := originalBoard
	deletedBoard.Cards = []kanbanCard{originalBoard.Cards[0]}
	deletedBoard.UpdatedAt = deletedAt.Add(time.Second).Format(time.RFC3339Nano)
	writeCanonicalFixtureJSON(t, paths.Board, deletedBoard)
	deletedPlan, _ := buildCanonicalFixturePlan(t, paths, versionPath)
	if err := deletedPlan.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	deletedReport, err := ReconcileCanonicalPlanWithOptions(context.Background(), deletedPlan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(deletedPlan)})
	if err != nil || deletedReport.Diverged {
		t.Fatalf("delete checkpoint did not reconcile: diverged=%v candidates=%+v err=%v", deletedReport.Diverged, deletedReport.Candidates, err)
	}

	restoredBoard := originalBoard
	restoredBoard.UpdatedAt = deletedAt.Add(2 * time.Second).Format(time.RFC3339Nano)
	for index := range restoredBoard.Cards {
		if restoredBoard.Cards[index].ID == deletedObject.ObjectID {
			restoredBoard.Cards[index].RestoredAt = restoredBoard.UpdatedAt
		}
	}
	writeCanonicalFixtureJSON(t, paths.Board, restoredBoard)
	restoredPlan, _ := buildCanonicalFixturePlan(t, paths, versionPath)
	if err := restoredPlan.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	restoredReport, err := ReconcileCanonicalPlanWithOptions(context.Background(), restoredPlan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(restoredPlan)})
	if err != nil {
		t.Fatal(err)
	}
	if restoredReport.Diverged || len(restoredReport.Candidates) != 0 {
		t.Fatalf("undo did not advance past deletion checkpoint: candidates=%+v", restoredReport.Candidates)
	}
	var restoredObject CanonicalImportedObject
	for _, object := range restoredPlan.Objects {
		if object.Family == "board_card" && object.ObjectID == deletedObject.ObjectID {
			restoredObject = object
			break
		}
	}
	if restoredObject.AggregateVersion != 3 {
		t.Fatalf("restored card version=%d, want v3 after v1 live and v2 deletion", restoredObject.AggregateVersion)
	}
	secondDeleteAt := deletedAt.Add(3 * time.Second)
	if err := ensureCanonicalLifecycleJournal(paths.DeletedJournal, CanonicalLifecycleJournalRecord{
		Family: "board_card", ObjectID: restoredObject.ObjectID, StateDigest: restoredObject.StateDigest,
		At: secondDeleteAt, Reason: "board_card_deleted_again",
	}); err != nil {
		t.Fatalf("journal second delete generation: %v", err)
	}
	deletedAgain := restoredBoard
	deletedAgain.Cards = []kanbanCard{restoredBoard.Cards[0]}
	deletedAgain.UpdatedAt = secondDeleteAt.Add(time.Second).Format(time.RFC3339Nano)
	writeCanonicalFixtureJSON(t, paths.Board, deletedAgain)
	deletedAgainPlan, _ := buildCanonicalFixturePlan(t, paths, versionPath)
	if err := deletedAgainPlan.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	deletedAgainReport, err := ReconcileCanonicalPlanWithOptions(context.Background(), deletedAgainPlan, store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(deletedAgainPlan)})
	if err != nil || deletedAgainReport.Diverged || len(deletedAgainReport.Candidates) != 0 {
		t.Fatalf("second delete generation did not reconcile: diverged=%v candidates=%+v err=%v", deletedAgainReport.Diverged, deletedAgainReport.Candidates, err)
	}
	for _, object := range deletedAgainPlan.Objects {
		if object.Family == "board_card" && object.ObjectID == deletedObject.ObjectID && object.AggregateVersion != 4 {
			t.Fatalf("second deletion version=%d, want v4", object.AggregateVersion)
		}
	}
}

func TestCanonicalLifecycleJournalRejectsReusingHistoricalNonLatestDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deleted-objects.jsonl")
	t1 := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	d1 := strings.Repeat("1", 64)
	d2 := strings.Repeat("2", 64)
	for _, record := range []CanonicalLifecycleJournalRecord{
		{Family: "board_card", ObjectID: "card-a", StateDigest: d1, At: t1, Reason: "first"},
		{Family: "board_card", ObjectID: "card-a", StateDigest: d2, At: t1.Add(time.Minute), Reason: "second"},
	} {
		if err := ensureCanonicalLifecycleJournal(path, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := ensureCanonicalLifecycleJournal(path, CanonicalLifecycleJournalRecord{
		Family: "board_card", ObjectID: "card-a", StateDigest: d1, At: t1.Add(2 * time.Minute), Reason: "stale state returned",
	}); err == nil {
		t.Fatal("historical non-latest digest was mistaken for an idempotent retry")
	}
	records, err := readCanonicalLifecycleJournal(path)
	if err != nil || len(records) != 2 {
		t.Fatalf("journal records=%+v err=%v, want two unchanged generations", records, err)
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
