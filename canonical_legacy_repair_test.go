package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeCanonicalLegacyRepairEngine struct {
	proofs      []canonicalBoardRepairProof
	observe     int
	appendCalls int
	applyCalls  int
	records     []CanonicalLifecycleJournalRecord
	appendErr   error
	applyErr    error
}

func (fake *fakeCanonicalLegacyRepairEngine) Observe(context.Context) (canonicalBoardRepairProof, error) {
	if fake.observe >= len(fake.proofs) {
		return canonicalBoardRepairProof{}, errors.New("unexpected observation")
	}
	proof := fake.proofs[fake.observe]
	fake.observe++
	return proof, nil
}

func (fake *fakeCanonicalLegacyRepairEngine) AppendLifecycleBatch(records []CanonicalLifecycleJournalRecord) error {
	fake.appendCalls++
	fake.records = append([]CanonicalLifecycleJournalRecord(nil), records...)
	return fake.appendErr
}

func (fake *fakeCanonicalLegacyRepairEngine) ApplyOrdinary(context.Context) error {
	fake.applyCalls++
	return fake.applyErr
}

func legacyRepairTestCandidate(family, id string, version int64) CanonicalRepairCandidate {
	return CanonicalRepairCandidate{Family: family, ObjectID: id, Kind: "tombstone_required", StateDigest: digestText(family + ":" + id), TargetVersion: version}
}

func legacyRepairTestProof(candidates []CanonicalRepairCandidate, count int64) canonicalBoardRepairProof {
	seal := canonicalBoardRepairFileSeal{Size: 1, SHA256: digestText("seal")}
	return canonicalBoardRepairProof{
		Candidates: candidates, PrincipalParity: true, ProjectionReplayValid: true, ParitySHA256: digestText("parity"),
		EventStoreSHA256: digestText("events"), EventHighWater: count, EventCount: count, OutboxCount: count,
		VersionEntryCount: int(count), VersionEntriesSHA256: digestText("versions"), DatabaseSHA256: digestText("database"),
		ImportInputSHA256: digestText("inputs"), Board: seal, Journal: seal, VersionMap: seal, Spool: seal,
	}
}

func legacyRepairTestManifest(candidates ...CanonicalRepairCandidate) canonicalLegacyRepairManifest {
	proof := legacyRepairTestProof(candidates, 100)
	ordered, candidateSHA, err := canonicalLegacyRepairCandidateSet(candidates)
	if err != nil {
		panic(err)
	}
	return canonicalLegacyRepairManifest{
		Schema: canonicalLegacyRepairManifestSchema, ReleaseCommit: "0123456789abcdef0123456789abcdef01234567", TenantID: "bonfire", DataDir: "/app/data",
		DatabaseURLSHA256: digestText("url"), FirstObservationSHA256: digestText("observation-one"), SecondObservationSHA256: digestText("observation-two"), ImportInputSHA256: proof.ImportInputSHA256,
		ProofSHA256: canonicalRepairProofFingerprint(proof), DatabaseSHA256: proof.DatabaseSHA256, CandidateSetSHA256: candidateSHA, Candidates: ordered,
		Board: proof.Board, Journal: proof.Journal, VersionMap: proof.VersionMap, Spool: proof.Spool, VersionEntriesSHA256: proof.VersionEntriesSHA256,
		EventHighWater: proof.EventHighWater, TenantEventCount: proof.EventCount, ImportOutboxCount: proof.OutboxCount,
		VersionEntryCount: proof.VersionEntryCount, PrincipalParity: true, ProjectionReplayValid: true,
		FirstObservedAt: time.Date(2026, 8, 15, 1, 2, 3, 4, time.UTC), SecondObservedAt: time.Date(2026, 8, 15, 1, 2, 33, 4, time.UTC),
	}
}

func TestCanonicalLegacyRepairManifestClosedCandidateContract(t *testing.T) {
	manifest := legacyRepairTestManifest(
		legacyRepairTestCandidate("memory", "m-1", 2),
		legacyRepairTestCandidate("file_assignment", "f:a", 1),
	)
	if err := validateCanonicalLegacyRepairManifest(manifest); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	for name, mutate := range map[string]func(*canonicalLegacyRepairManifest){
		"unsupported family": func(value *canonicalLegacyRepairManifest) { value.Candidates[0].Family = "room" },
		"journal confirmed":  func(value *canonicalLegacyRepairManifest) { value.Candidates[0].ConfirmedByJournal = true },
		"changed digest":     func(value *canonicalLegacyRepairManifest) { value.Candidates[0].StateDigest = digestText("changed") },
		"unsorted": func(value *canonicalLegacyRepairManifest) {
			value.Candidates[0], value.Candidates[1] = value.Candidates[1], value.Candidates[0]
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := manifest
			changed.Candidates = append([]CanonicalRepairCandidate(nil), manifest.Candidates...)
			mutate(&changed)
			if err := validateCanonicalLegacyRepairManifest(changed); err == nil {
				t.Fatal("tampered manifest accepted")
			}
		})
	}
}

func TestCanonicalLegacyRepairManifestRequiresTwoExactStableObservations(t *testing.T) {
	candidates := []CanonicalRepairCandidate{
		legacyRepairTestCandidate("artifact_revision", "artifact-1:1", 1),
		legacyRepairTestCandidate("board_card", "board-1", 1),
		legacyRepairTestCandidate("file_assignment", "file-1:user-1", 1),
		legacyRepairTestCandidate("file_folder", "folder-1", 1),
		legacyRepairTestCandidate("memory", "memory-1", 1),
		legacyRepairTestCandidate("notification", "notification-1", 1),
	}
	proof := legacyRepairTestProof(candidates, 100)
	candidateFingerprint, err := canonicalRepairCandidateDigest(candidates)
	if err != nil {
		t.Fatal(err)
	}
	first := canonicalBoardRepairObservation{
		Schema: "bonfire.canonical-board-repair-observation.v1", ReleaseCommit: "0123456789abcdef0123456789abcdef01234567", TenantID: "bonfire", DataDir: "/app/data",
		DatabaseURLSHA256: digestText("url"), DatabaseSHA256: proof.DatabaseSHA256, ImportInputSHA256: proof.ImportInputSHA256,
		Board: proof.Board, Journal: proof.Journal, VersionMap: proof.VersionMap, VersionEntriesSHA256: proof.VersionEntriesSHA256, Spool: proof.Spool,
		CandidateCount: len(candidates), CandidateFingerprint: candidateFingerprint, ProofFingerprint: canonicalRepairProofFingerprint(proof), Candidates: candidates,
		PrincipalParity: true, ProjectionReplayValid: true, EventHighWater: proof.EventHighWater, TenantEventCount: proof.EventCount,
		OutboxCount: proof.OutboxCount, VersionEntryCount: proof.VersionEntryCount, ObservedAt: time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC),
	}
	second := first
	second.ObservedAt = first.ObservedAt.Add(30 * time.Second)
	manifest, err := canonicalLegacyRepairManifestFromObservations(first, second, digestText("first"), digestText("second"))
	if err != nil || len(manifest.Candidates) != len(candidates) {
		t.Fatalf("stable observations rejected: candidates=%d err=%v", len(manifest.Candidates), err)
	}
	second.DatabaseSHA256 = digestText("changed")
	if _, err := canonicalLegacyRepairManifestFromObservations(first, second, digestText("first"), digestText("second")); err == nil {
		t.Fatal("database drift between observations accepted")
	}
	second = first
	second.ObservedAt = first.ObservedAt.Add(9 * time.Second)
	if _, err := canonicalLegacyRepairManifestFromObservations(first, second, digestText("first"), digestText("second")); err == nil {
		t.Fatal("insufficient observation interval accepted")
	}
}

func TestCanonicalLegacyRepairExecutesOneBatchAndIdempotentReplay(t *testing.T) {
	candidates := []CanonicalRepairCandidate{
		legacyRepairTestCandidate("board_card", "b-1", 4),
		legacyRepairTestCandidate("memory", "m-1", 2),
	}
	manifest := legacyRepairTestManifest(candidates...)
	before := legacyRepairTestProof(manifest.Candidates, 100)
	delta := int64(2 * len(candidates))
	after := legacyRepairTestProof(nil, 100+delta)
	after.Diverged = false
	after.DatabaseSHA256 = digestText("database-after")
	after.EventStoreSHA256 = digestText("events-after")
	after.ParitySHA256 = digestText("parity-after")
	fake := &fakeCanonicalLegacyRepairEngine{proofs: []canonicalBoardRepairProof{before, after, after}}
	now := time.Date(2026, 8, 15, 2, 3, 4, 5, time.UTC)
	receipt, err := executeCanonicalLegacyRepair(context.Background(), manifest, fake, now)
	if err != nil {
		t.Fatalf("execute repair: %v", err)
	}
	if fake.appendCalls != 1 || fake.applyCalls != 2 || len(fake.records) != len(candidates) {
		t.Fatalf("unexpected calls append=%d apply=%d records=%d", fake.appendCalls, fake.applyCalls, len(fake.records))
	}
	for index, record := range fake.records {
		if record.Family != manifest.Candidates[index].Family || record.ObjectID != manifest.Candidates[index].ObjectID ||
			record.StateDigest != manifest.Candidates[index].StateDigest || record.Reason != canonicalLegacyRepairReason ||
			record.EvidenceBasis != canonicalLegacyRepairEvidence || record.At != now.Add(time.Duration(index)*time.Nanosecond) {
			t.Fatalf("record %d mismatch: %+v", index, record)
		}
	}
	receipt.ManifestSHA256 = digestText("manifest")
	receipt.AuthoritySHA256 = digestText("authority")
	receipt.JournalAfter = canonicalBoardRepairFileSeal{Size: receipt.JournalBefore.Size + 1, SHA256: digestText("journal-after")}
	digest, err := canonicalLegacyRepairReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.SelfSHA256 = digest
	if err := validateCanonicalLegacyRepairReceipt(receipt); err != nil {
		t.Fatalf("receipt rejected: %v", err)
	}
	receipt.AppliedEventCount++
	digest, _ = canonicalLegacyRepairReceiptDigest(receipt)
	receipt.SelfSHA256 = digest
	if err := validateCanonicalLegacyRepairReceipt(receipt); err == nil {
		t.Fatal("re-signed impossible delta accepted")
	}
}

func TestCanonicalLegacyRepairExistingReceiptRequiresExactLiveState(t *testing.T) {
	manifest := legacyRepairTestManifest(legacyRepairTestCandidate("memory", "m-1", 1))
	live := legacyRepairTestProof(nil, 102)
	live.DatabaseSHA256 = digestText("database-after")
	live.EventStoreSHA256 = digestText("events-after")
	live.ParitySHA256 = digestText("parity-after")
	receipt := canonicalLegacyRepairReceipt{
		Schema: canonicalLegacyRepairReceiptSchema, Status: "complete", ReleaseCommit: manifest.ReleaseCommit, TenantID: manifest.TenantID,
		ManifestSHA256: digestText("manifest"), AuthoritySHA256: digestText("authority"), CandidateSetSHA256: manifest.CandidateSetSHA256,
		CandidateCount: 1, AppliedEventCount: 2, AppliedOutboxCount: 2, AppliedVersionCount: 2,
		JournalBefore: manifest.Journal, JournalAfter: live.Journal, DatabaseBeforeSHA256: manifest.DatabaseSHA256, DatabaseAfterSHA256: live.DatabaseSHA256,
		BeforeProofSHA256: manifest.ProofSHA256, AfterProofSHA256: canonicalRepairProofFingerprint(live), EventHighWaterBefore: manifest.EventHighWater,
		EventHighWaterAfter: live.EventHighWater, ZeroCandidates: true, PrincipalParity: true, ProjectionReplayValid: true,
		IdempotentSecondReplay: true, CompletedAt: time.Now().UTC(),
	}
	receipt.JournalAfter = canonicalBoardRepairFileSeal{Size: manifest.Journal.Size + 10, SHA256: digestText("journal-after")}
	live.Journal = receipt.JournalAfter
	receipt.AfterProofSHA256 = canonicalRepairProofFingerprint(live)
	digest, _ := canonicalLegacyRepairReceiptDigest(receipt)
	receipt.SelfSHA256 = digest
	if err := validateExistingCanonicalLegacyRepairReceipt(receipt, manifest, receipt.ManifestSHA256, receipt.AuthoritySHA256, live); err != nil {
		t.Fatalf("exact existing receipt rejected: %v", err)
	}
	live.EventHighWater++
	if err := validateExistingCanonicalLegacyRepairReceipt(receipt, manifest, receipt.ManifestSHA256, receipt.AuthoritySHA256, live); err == nil {
		t.Fatal("drifted live state accepted for existing receipt")
	}
}

func TestCanonicalLegacyRepairBatchIsAllOrExactIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deleted-objects.jsonl")
	prefix := []byte(`{"family":"memory","object_id":"old","state_sha256":"` + digestText("old") + `","at":"2026-08-14T00:00:00Z","reason":"ordinary"}` + "\n")
	if err := os.WriteFile(path, prefix, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := legacyRepairTestManifest(
		legacyRepairTestCandidate("file_assignment", "a-1", 1),
		legacyRepairTestCandidate("memory", "m-1", 1),
	)
	records := canonicalLegacyRepairRecords(manifest, time.Date(2026, 8, 15, 4, 5, 6, 7, time.UTC))
	if err := appendCanonicalLegacyRepairBatchAtomic(path, records); err != nil {
		t.Fatalf("atomic batch: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil || string(first[:len(prefix)]) != string(prefix) {
		t.Fatalf("prefix changed raw=%q err=%v", first, err)
	}
	if err := appendCanonicalLegacyRepairBatchAtomic(path, records); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("exact batch replay changed journal bytes")
	}
	partialPath := filepath.Join(dir, "partial.jsonl")
	if err := os.WriteFile(partialPath, append(prefix, mustJSONLine(t, records[0])...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendCanonicalLegacyRepairBatchAtomic(partialPath, records); err == nil {
		t.Fatal("logical partial batch accepted")
	}
}

func mustJSONLine(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func TestCanonicalLegacyRepairRecoveryRequiresBoundedDelta(t *testing.T) {
	manifest := legacyRepairTestManifest(legacyRepairTestCandidate("file_folder", "folder-1", 1))
	partial := legacyRepairTestProof(nil, 101)
	partial.DatabaseSHA256 = digestText("partial")
	partial.ImportInputSHA256 = digestText("journal-changed")
	final := legacyRepairTestProof(nil, 102)
	final.DatabaseSHA256 = digestText("final")
	final.ImportInputSHA256 = digestText("journal-changed")
	fake := &fakeCanonicalLegacyRepairEngine{proofs: []canonicalBoardRepairProof{partial, final, final}}
	if _, err := executeCanonicalLegacyRepairWithRecovery(context.Background(), manifest, fake, time.Now().UTC(), true); err != nil {
		t.Fatalf("bounded recovery failed: %v", err)
	}
	tooFar := legacyRepairTestProof(nil, 103)
	tooFar.DatabaseSHA256 = digestText("too-far")
	bad := &fakeCanonicalLegacyRepairEngine{proofs: []canonicalBoardRepairProof{tooFar}}
	if _, err := executeCanonicalLegacyRepairWithRecovery(context.Background(), manifest, bad, time.Now().UTC(), true); err == nil {
		t.Fatal("out-of-envelope recovery accepted")
	}
}

func TestCanonicalLegacyRepairFailsBeforeAppendOnStateDrift(t *testing.T) {
	manifest := legacyRepairTestManifest(legacyRepairTestCandidate("notification", "n-1", 1))
	drifted := legacyRepairTestProof(manifest.Candidates, 101)
	fake := &fakeCanonicalLegacyRepairEngine{proofs: []canonicalBoardRepairProof{drifted}}
	if _, err := executeCanonicalLegacyRepair(context.Background(), manifest, fake, time.Now().UTC()); err == nil {
		t.Fatal("drifted repair state accepted")
	}
	if fake.appendCalls != 0 || fake.applyCalls != 0 {
		t.Fatalf("drifted repair mutated: append=%d apply=%d", fake.appendCalls, fake.applyCalls)
	}
}

func TestCanonicalLegacyRepairPostgresExactDeltaAndReplay(t *testing.T) {
	ctx, engine, _, _ := setupRealCanonicalRepairFixture(t)
	beforeNormalization, err := engine.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := canonicalBoardNormalizationInput{BeforeFingerprintSHA256: canonicalRepairProofFingerprint(beforeNormalization), MaxApplyPasses: 4}
	if _, err := (canonicalBoardNormalizationRun{input: input, engine: engine}).execute(ctx); err != nil {
		t.Fatalf("normalize fixture: %v", err)
	}
	before, err := engine.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ordered, candidateSHA, err := canonicalLegacyRepairCandidateSet(before.Candidates)
	if err != nil {
		t.Fatal(err)
	}
	manifest := canonicalLegacyRepairManifest{
		Schema: canonicalLegacyRepairManifestSchema, ReleaseCommit: "0123456789abcdef0123456789abcdef01234567", TenantID: engine.manifest.TenantID,
		DataDir: engine.manifest.DataDir, DatabaseURLSHA256: digestText("url"), FirstObservationSHA256: digestText("observation-one"), SecondObservationSHA256: digestText("observation-two"),
		ImportInputSHA256: before.ImportInputSHA256, ProofSHA256: canonicalRepairProofFingerprint(before), DatabaseSHA256: before.DatabaseSHA256,
		CandidateSetSHA256: candidateSHA, Candidates: ordered, Board: before.Board, Journal: before.Journal, VersionMap: before.VersionMap,
		Spool: before.Spool, VersionEntriesSHA256: before.VersionEntriesSHA256, EventHighWater: before.EventHighWater,
		TenantEventCount: before.EventCount, ImportOutboxCount: before.OutboxCount, VersionEntryCount: before.VersionEntryCount,
		PrincipalParity: true, ProjectionReplayValid: true, FirstObservedAt: time.Now().UTC().Add(-30 * time.Second), SecondObservedAt: time.Now().UTC(),
	}
	receipt, err := executeCanonicalLegacyRepair(ctx, manifest, postgresCanonicalLegacyRepairEngine{postgresCanonicalBoardRepairEngine: engine}, time.Now().UTC())
	if err != nil {
		t.Fatalf("execute real generic repair: %v", err)
	}
	if receipt.AppliedEventCount != int64(2*len(ordered)) || receipt.AppliedVersionCount != 2*len(ordered) || !receipt.IdempotentSecondReplay {
		t.Fatalf("unexpected real receipt: %+v", receipt)
	}
	final, err := engine.Observe(ctx)
	if err != nil || len(final.Candidates) != 0 || final.Diverged {
		t.Fatalf("real repair did not converge: candidates=%+v diverged=%v err=%v", final.Candidates, final.Diverged, err)
	}
}
