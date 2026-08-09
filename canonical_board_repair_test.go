package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeCanonicalBoardRepairEngine struct {
	proofs       []canonicalBoardRepairProof
	proofIndex   int
	applyCount   int
	appendCount  int
	failAppendAt int
}

func (fake *fakeCanonicalBoardRepairEngine) Observe(context.Context) (canonicalBoardRepairProof, error) {
	if len(fake.proofs) == 0 {
		return canonicalBoardRepairProof{}, errors.New("no proof")
	}
	index := fake.proofIndex
	if index >= len(fake.proofs) {
		index = len(fake.proofs) - 1
	}
	return fake.proofs[index], nil
}

func (fake *fakeCanonicalBoardRepairEngine) ApplyOrdinary(context.Context) error {
	fake.applyCount++
	if fake.proofIndex+1 < len(fake.proofs) {
		fake.proofIndex++
	}
	return nil
}

func (fake *fakeCanonicalBoardRepairEngine) AppendLifecycle(_ context.Context, _ canonicalBoardRepairTarget) error {
	if fake.failAppendAt > 0 && fake.appendCount+1 == fake.failAppendAt {
		return errors.New("injected append interruption")
	}
	fake.appendCount++
	return nil
}

func repairEvidenceFile(name, digest string) canonicalBoardRepairEvidenceFile {
	return canonicalBoardRepairEvidenceFile{Path: name, Size: 1, SHA256: digest}
}

func repairTestTarget(id string, version int64) canonicalBoardRepairTarget {
	return canonicalBoardRepairTarget{
		ObjectID: id, StateSHA256: strings.Repeat("a", 64), TargetVersion: version,
		ObservedAbsenceAt:   time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		EvidenceBasis:       "done_archive_absence",
		SelectedStateRole:   "archive_record",
		SourceRecord:        repairEvidenceFile(id+"-source.json", strings.Repeat("b", 64)),
		ArchiveRecord:       repairEvidenceFile(id+"-archive.json", strings.Repeat("c", 64)),
		PositiveObservation: repairEvidenceFile(id+"-positive.json", strings.Repeat("d", 64)),
		AbsenceEvidence:     repairEvidenceFile(id+"-absence.json", strings.Repeat("e", 64)),
		PriorPrincipals:     []string{"user:member@example.com"},
	}
}

func repairTestTargets() []canonicalBoardRepairTarget {
	targets := make([]canonicalBoardRepairTarget, 0, canonicalBoardRepairExactCount)
	for index := 0; index < canonicalBoardRepairExactCount; index++ {
		targets = append(targets, repairTestTarget("fixture-"+string(rune('a'+index)), int64(index+1)))
	}
	return targets
}

func repairTestManifest(targets ...canonicalBoardRepairTarget) canonicalBoardRepairManifest {
	sort.Slice(targets, func(i, j int) bool { return targets[i].ObjectID < targets[j].ObjectID })
	raw, _ := canonicalJSON(targets)
	candidates := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		candidates = append(candidates, repairCandidate(target))
	}
	terminalSHA, _ := canonicalRepairCandidateDigest(candidates)
	return canonicalBoardRepairManifest{Candidates: targets, CandidateSetSHA256: sha256Hex(raw), TerminalCandidateSHA256: terminalSHA}
}

func bindRepairManifestToProof(manifest *canonicalBoardRepairManifest, proof canonicalBoardRepairProof) {
	manifest.NormalizedProofSHA256 = canonicalRepairProofFingerprint(proof)
	manifest.ImportInputSHA256 = proof.ImportInputSHA256
}

func repairCandidate(target canonicalBoardRepairTarget) CanonicalRepairCandidate {
	return CanonicalRepairCandidate{Family: "board_card", ObjectID: target.ObjectID, Kind: "tombstone_required", StateDigest: target.StateSHA256, TargetVersion: target.TargetVersion}
}

func repairProof(candidates []CanonicalRepairCandidate, highWater int64) canonicalBoardRepairProof {
	principals := map[string][]string{}
	for _, candidate := range candidates {
		if candidate.Family == "board_card" && candidate.Kind == "tombstone_required" {
			principals[candidate.ObjectID] = []string{"user:member@example.com"}
		}
	}
	seal := canonicalBoardRepairFileSeal{Size: 1, SHA256: strings.Repeat("3", 64)}
	emptyJournal := canonicalBoardRepairFileSeal{Size: 0, SHA256: sha256Hex(nil)}
	return canonicalBoardRepairProof{
		Candidates: candidates, Diverged: len(candidates) > 0, PrincipalParity: true, ProjectionReplayValid: true,
		ParitySHA256: strings.Repeat("1", 64), EventStoreSHA256: strings.Repeat("2", 64),
		EventHighWater: highWater, EventCount: highWater, SpoolHighWater: 12, OutboxCount: highWater, VersionEntryCount: int(highWater),
		VersionEntriesSHA256: strings.Repeat("4", 64), DatabaseSHA256: strings.Repeat("5", 64),
		ImportInputSHA256: strings.Repeat("6", 64),
		Board:             seal, Journal: emptyJournal, VersionMap: seal, Spool: seal, PriorPrincipals: principals, JournalRaw: []byte{},
	}
}

func repairFinalProof(before canonicalBoardRepairProof, targets []canonicalBoardRepairTarget) canonicalBoardRepairProof {
	proof := repairProof(nil, before.EventCount+canonicalBoardRepairExactCount)
	raw := append([]byte(nil), before.JournalRaw...)
	records := append([]CanonicalLifecycleJournalRecord(nil), before.JournalRecords...)
	for index, target := range targets {
		record := CanonicalLifecycleJournalRecord{
			Family: "board_card", ObjectID: target.ObjectID, StateDigest: target.StateSHA256,
			At: time.Date(2026, 8, 2, 11, 0, index, 0, time.UTC), Reason: canonicalBoardRepairReason, EvidenceBasis: target.EvidenceBasis,
		}
		encoded, _ := json.Marshal(record)
		raw = append(raw, append(encoded, '\n')...)
		records = append(records, record)
	}
	proof.JournalRaw, proof.JournalRecords = raw, records
	proof.Journal = canonicalBoardRepairFileSeal{Size: int64(len(raw)), SHA256: sha256Hex(raw)}
	return proof
}

func TestCanonicalBoardNormalizationConvergesWithoutLifecycleAppendAndFullSecondReplay(t *testing.T) {
	targets := repairTestTargets()
	terminal := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		terminal = append(terminal, repairCandidate(target))
	}
	beforeCandidates := append([]CanonicalRepairCandidate(nil), terminal...)
	beforeCandidates = append(beforeCandidates,
		CanonicalRepairCandidate{Family: "memory", ObjectID: "ordinary-missing-1", Kind: "missing_event"},
		CanonicalRepairCandidate{Family: "notification", ObjectID: "ordinary-missing-2", Kind: "missing_event"},
		CanonicalRepairCandidate{Family: "board_card", ObjectID: targets[0].ObjectID, Kind: "principal_missing_access", Principal: "user:member@example.com"},
	)
	before := repairProof(beforeCandidates, 100)
	converged := repairProof(terminal, 102)
	// One of the normalized objects can already have a durable version entry
	// even though its event/outbox record is missing. The receipt must record
	// the exact version-map growth without requiring it to equal event growth.
	converged.VersionEntryCount = before.VersionEntryCount + 1
	converged.VersionEntriesSHA256 = strings.Repeat("7", 64)
	input := canonicalBoardNormalizationInput{BeforeFingerprintSHA256: canonicalRepairProofFingerprint(before), MaxApplyPasses: 3}
	engine := &fakeCanonicalBoardRepairEngine{proofs: []canonicalBoardRepairProof{before, converged, converged}}
	receipt, err := (canonicalBoardNormalizationRun{input: input, engine: engine}).execute(context.Background())
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if engine.appendCount != 0 || engine.applyCount != 2 || receipt.ApplyPasses != 2 || receipt.LifecycleAppendCount != 0 || !receipt.ExactTerminalSeven || !receipt.FullZeroDeltaSecondReplay || receipt.JournalBefore != receipt.JournalAfter {
		t.Fatalf("normalization outcome engine=%+v receipt=%+v", engine, receipt)
	}
	if receipt.Delta != (canonicalBoardRepairCountDelta{TenantEvents: 2, ImportOutbox: 2, VersionEntries: 1}) {
		t.Fatalf("normalization recorded wrong exact bounded delta: %+v", receipt.Delta)
	}
}

func TestCanonicalBoardNormalizationRefusesChangedStartAndSecondReplayDrift(t *testing.T) {
	targets := repairTestTargets()
	terminal := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		terminal = append(terminal, repairCandidate(target))
	}
	beforeCandidates := append([]CanonicalRepairCandidate(nil), terminal...)
	beforeCandidates = append(beforeCandidates,
		CanonicalRepairCandidate{Family: "memory", ObjectID: "ordinary-missing-1", Kind: "missing_event"},
		CanonicalRepairCandidate{Family: "notification", ObjectID: "ordinary-missing-2", Kind: "state_mismatch"},
	)
	before := repairProof(beforeCandidates, 100)
	changed := before
	changed.DatabaseSHA256 = strings.Repeat("6", 64)
	engine := &fakeCanonicalBoardRepairEngine{proofs: []canonicalBoardRepairProof{before}}
	input := canonicalBoardNormalizationInput{BeforeFingerprintSHA256: canonicalRepairProofFingerprint(changed), MaxApplyPasses: 2}
	if _, err := (canonicalBoardNormalizationRun{input: input, engine: engine}).execute(context.Background()); err == nil || engine.applyCount != 0 {
		t.Fatalf("changed start accepted err=%v engine=%+v", err, engine)
	}

	converged := repairProof(terminal, 102)
	drifted := converged
	drifted.OutboxCount++
	input.BeforeFingerprintSHA256 = canonicalRepairProofFingerprint(before)
	engine = &fakeCanonicalBoardRepairEngine{proofs: []canonicalBoardRepairProof{before, converged, drifted}}
	if _, err := (canonicalBoardNormalizationRun{input: input, engine: engine}).execute(context.Background()); err == nil || !strings.Contains(err.Error(), "zero-delta") {
		t.Fatalf("full second replay drift accepted err=%v", err)
	}
}

func TestCanonicalBoardRepairExecutesExactSevenAndSecondReplay(t *testing.T) {
	targets := repairTestTargets()
	manifest := repairTestManifest(targets...)
	candidates := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		candidates = append(candidates, repairCandidate(target))
	}
	before := repairProof(candidates, 100)
	bindRepairManifestToProof(&manifest, before)
	final := repairFinalProof(before, targets)
	engine := &fakeCanonicalBoardRepairEngine{proofs: []canonicalBoardRepairProof{before, final, final}}
	receipt, err := (canonicalBoardRepairRun{manifest: manifest, engine: engine}).execute(context.Background())
	if err != nil {
		t.Fatalf("execute exact repair: %v", err)
	}
	if engine.appendCount != canonicalBoardRepairExactCount || engine.applyCount != 2 || receipt.AppliedCount != canonicalBoardRepairExactCount || !receipt.ZeroCandidates || !receipt.IdempotentSecondReplay || receipt.AfterFingerprintSHA256 != canonicalRepairProofFingerprint(final) {
		t.Fatalf("repair outcome engine=%+v receipt=%+v", engine, receipt)
	}
}

func TestCanonicalBoardRepairRejectsUnrelatedSourceOrQueueDriftBeforeMutation(t *testing.T) {
	targets := repairTestTargets()
	manifest := repairTestManifest(targets...)
	candidates := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		candidates = append(candidates, repairCandidate(target))
	}
	normalized := repairProof(candidates, 100)
	bindRepairManifestToProof(&manifest, normalized)
	drifted := normalized
	drifted.ImportInputSHA256 = strings.Repeat("f", 64)
	engine := &fakeCanonicalBoardRepairEngine{proofs: []canonicalBoardRepairProof{drifted}}
	if _, err := (canonicalBoardRepairRun{manifest: manifest, engine: engine}).execute(context.Background()); err == nil || !strings.Contains(err.Error(), "full proof") {
		t.Fatalf("unrelated source drift accepted err=%v", err)
	}
	if engine.applyCount != 0 || engine.appendCount != 0 {
		t.Fatalf("source drift mutated state engine=%+v", engine)
	}
}

func canonicalRepairTestTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCanonicalRepairImportInputFingerprintIncludesUsersAndQueueDrift(t *testing.T) {
	dir := canonicalRepairTestTempDir(t)
	usersPath := filepath.Join(dir, "users.json")
	queueDir := filepath.Join(dir, "queue")
	if err := os.Mkdir(queueDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usersPath, []byte(`[{"email":"member@example.com"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(queueDir, "job.json")
	if err := os.WriteFile(queuePath, []byte(`{"status":"queued"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := CanonicalImportPaths{QueueDirs: []string{queueDir}}
	before, err := canonicalRepairImportInputFingerprint(paths, usersPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usersPath, []byte(`[{"email":"member@example.com"},{"email":"new@example.com"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	afterUsers, err := canonicalRepairImportInputFingerprint(paths, usersPath)
	if err != nil || afterUsers == before {
		t.Fatalf("users drift fingerprint before=%s after=%s err=%v", before, afterUsers, err)
	}
	if err := os.WriteFile(queuePath, []byte(`{"status":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	afterQueue, err := canonicalRepairImportInputFingerprint(paths, usersPath)
	if err != nil || afterQueue == afterUsers {
		t.Fatalf("queue drift fingerprint users=%s queue=%s err=%v", afterUsers, afterQueue, err)
	}
}

func setupRealCanonicalRepairFixture(t *testing.T) (context.Context, *postgresCanonicalBoardRepairEngine, []canonicalBoardRepairTarget, []byte) {
	t.Helper()
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	dir := canonicalRepairTestTempDir(t)
	boardPath := filepath.Join(dir, "kanban-board.json")
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	versionPath := filepath.Join(dir, "canonical", "object-versions.json")
	spoolPath := filepath.Join(dir, "canonical", "mutation-spool.bcs")
	usersPath := filepath.Join(dir, "users.json")
	if err := os.MkdirAll(filepath.Dir(versionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cards := make([]kanbanCard, 0, canonicalBoardRepairExactCount)
	for index := 0; index < canonicalBoardRepairExactCount; index++ {
		cards = append(cards, kanbanCard{ID: "real-card-" + string(rune('a'+index)), Title: "Historical", Status: kanbanStatusDone})
	}
	boardRaw, _ := marshalKanbanBoardState(kanbanBoardState{Cards: cards, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err := os.WriteFile(boardPath, boardRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spoolPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	usersRaw, _ := json.Marshal([]*userAccount{{Email: "member@example.com"}})
	if err := os.WriteFile(usersPath, usersRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	versions, err := OpenFileCanonicalObjectVersionMap(versionPath)
	if err != nil {
		t.Fatal(err)
	}
	initialPaths := CanonicalImportPaths{Board: boardPath, DeletedJournal: journalPath}
	initial, err := (&CanonicalImporter{TenantID: "repair-integration", Registry: registry, Versions: versions, OrgPrincipals: []string{"user:member@example.com"}, Paths: initialPaths}).Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Apply(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncImportGrants(ctx, initial); err != nil {
		t.Fatal(err)
	}
	byID := map[string]CanonicalImportedObject{}
	for _, object := range initial.Objects {
		if object.Family == "board_card" {
			byID[object.ObjectID] = object
		}
	}
	emptyBoard, _ := marshalKanbanBoardState(kanbanBoardState{Cards: nil, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err := os.WriteFile(boardPath, emptyBoard, 0o600); err != nil {
		t.Fatal(err)
	}
	memory := meetingMemoryEntry{ID: "ordinary-normalization-object", Kind: meetingMemoryKindTranscript, Text: "ordinary", CreatedAt: time.Now().UTC(), Metadata: map[string]string{"meetingId": "normalization"}}
	memoryRaw, _ := json.Marshal(memory)
	memoryTwo := meetingMemoryEntry{ID: "ordinary-normalization-object-two", Kind: meetingMemoryKindTranscript, Text: "ordinary two", CreatedAt: time.Now().UTC(), Metadata: map[string]string{"meetingId": "normalization"}}
	memoryTwoRaw, _ := json.Marshal(memoryTwo)
	if err := os.WriteFile(memoryPath, append(append(append([]byte(nil), memoryRaw...), '\n'), append(memoryTwoRaw, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := CanonicalImportPaths{MeetingMemory: memoryPath, Board: boardPath, DeletedJournal: journalPath}
	engine := &postgresCanonicalBoardRepairEngine{
		manifest: canonicalBoardRepairManifest{TenantID: "repair-integration", DataDir: dir}, registry: registry, store: store, versions: versions,
		spoolPath: spoolPath, usersPath: usersPath, journalPath: journalPath, memberPrincipals: []string{"user:member@example.com"}, paths: paths,
	}
	targets := make([]canonicalBoardRepairTarget, 0, canonicalBoardRepairExactCount)
	for _, card := range cards {
		object := byID[card.ID]
		target := repairTestTarget(card.ID, object.AggregateVersion)
		target.StateSHA256 = object.StateDigest
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ObjectID < targets[j].ObjectID })
	journalBefore, _ := os.ReadFile(journalPath)
	return ctx, engine, targets, journalBefore
}

func TestCanonicalRepairOrderedJSONRowsMatchesBoundedPostgresAggregate(t *testing.T) {
	ctx, engine, _, _ := setupRealCanonicalRepairFixture(t)
	const rowsQuery = `SELECT to_jsonb(q)::text FROM (
		SELECT o.object_type,o.object_id,o.state_revision,o.content_revision,o.owner_principal_type,o.owner_principal_id,
			o.room_id,o.meeting_id,o.classification,o.state,encode(o.content_sha256,'hex') AS content_sha256,o.acl_version,
			e.event_id::text AS last_event_id,o.deleted_at,o.retain_until,o.legal_hold
		FROM objects o JOIN canonical_events e ON e.sequence=o.last_event_sequence WHERE o.tenant_id=$1) q
		ORDER BY q.object_type,q.object_id`
	streamed, err := canonicalRepairOrderedJSONRows(ctx, engine.store, rowsQuery, engine.manifest.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	var aggregated string
	if err := engine.store.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(q) ORDER BY q.object_type,q.object_id)::text,'[]') FROM (
		SELECT o.object_type,o.object_id,o.state_revision,o.content_revision,o.owner_principal_type,o.owner_principal_id,
			o.room_id,o.meeting_id,o.classification,o.state,encode(o.content_sha256,'hex') AS content_sha256,o.acl_version,
			e.event_id::text AS last_event_id,o.deleted_at,o.retain_until,o.legal_hold
		FROM objects o JOIN canonical_events e ON e.sequence=o.last_event_sequence WHERE o.tenant_id=$1) q`, engine.manifest.TenantID).Scan(&aggregated); err != nil {
		t.Fatal(err)
	}
	if streamed != aggregated {
		t.Fatalf("streamed ordered JSON does not preserve the aggregate contract")
	}
}

func TestReadOptionalCanonicalLifecycleJournalTreatsLegacyAbsenceAsEmpty(t *testing.T) {
	dir := canonicalRepairTestTempDir(t)
	path := filepath.Join(dir, "deleted-objects.jsonl")
	raw, present, err := readOptionalCanonicalLifecycleJournalSnapshot(path)
	if err != nil || len(raw) != 0 || present {
		t.Fatalf("missing legacy journal raw=%q present=%v err=%v", raw, present, err)
	}
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, present, err = readOptionalCanonicalLifecycleJournalSnapshot(path)
	if err != nil || len(raw) != 0 || !present {
		t.Fatalf("existing empty journal raw=%q present=%v err=%v", raw, present, err)
	}
	if err := os.WriteFile(path, []byte("record\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err = readOptionalCanonicalLifecycleJournal(path)
	if err != nil || string(raw) != "record\n" {
		t.Fatalf("existing journal raw=%q err=%v", raw, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "absent-target"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := readOptionalCanonicalLifecycleJournal(path); err == nil {
		t.Fatal("dangling journal symlink was accepted as legacy absence")
	}
}

func TestCanonicalBoardRepairPostgresNormalizationThenRepairIsCompleteAndIdempotent(t *testing.T) {
	ctx, engine, targets, journalBefore := setupRealCanonicalRepairFixture(t)
	if err := os.Remove(engine.journalPath); err != nil {
		t.Fatal(err)
	}
	before, err := engine.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Candidates) <= canonicalBoardRepairExactCount {
		t.Fatalf("fixture did not require ordinary normalization: %+v", before.Candidates)
	}
	input := canonicalBoardNormalizationInput{BeforeFingerprintSHA256: canonicalRepairProofFingerprint(before), MaxApplyPasses: 4}
	normalization, err := (canonicalBoardNormalizationRun{input: input, engine: engine}).execute(ctx)
	if err != nil {
		t.Fatalf("real normalization: %v", err)
	}
	journalAfterNormalization, _ := os.ReadFile(engine.journalPath)
	if string(journalBefore) != string(journalAfterNormalization) || normalization.LifecycleAppendCount != 0 || normalization.ApplyPasses < 2 {
		t.Fatalf("normalization touched lifecycle journal receipt=%+v", normalization)
	}
	if _, err := os.Lstat(engine.journalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("normalization created the absent legacy lifecycle journal: %v", err)
	}
	normalizedProof, err := engine.Observe(ctx)
	if err != nil || len(normalizedProof.Candidates) != canonicalBoardRepairExactCount {
		t.Fatalf("normalized proof candidates=%+v err=%v", normalizedProof.Candidates, err)
	}
	for index := range targets {
		targets[index].PriorPrincipals = append([]string(nil), normalizedProof.PriorPrincipals[targets[index].ObjectID]...)
	}
	manifest := repairTestManifest(targets...)
	manifest.TenantID, manifest.DataDir = engine.manifest.TenantID, engine.manifest.DataDir
	bindRepairManifestToProof(&manifest, normalizedProof)
	engine.manifest = manifest
	receipt, err := (canonicalBoardRepairRun{manifest: manifest, engine: engine}).execute(ctx)
	if err != nil {
		t.Fatalf("real repair: %v", err)
	}
	final, err := engine.Observe(ctx)
	if err != nil || len(final.Candidates) != 0 || receipt.AfterFingerprintSHA256 != canonicalRepairProofFingerprint(final) || !receipt.IdempotentSecondReplay {
		t.Fatalf("real final proof=%+v receipt=%+v err=%v", final, receipt, err)
	}
	if info, err := os.Lstat(engine.journalPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("authorized repair did not create the lifecycle journal: %v", err)
	}
}

func TestCanonicalBoardRepairPostgresPartialJournalAndDBStateRefused(t *testing.T) {
	ctx, engine, targets, _ := setupRealCanonicalRepairFixture(t)
	before, err := engine.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := canonicalBoardNormalizationInput{BeforeFingerprintSHA256: canonicalRepairProofFingerprint(before), MaxApplyPasses: 4}
	if _, err := (canonicalBoardNormalizationRun{input: input, engine: engine}).execute(ctx); err != nil {
		t.Fatal(err)
	}
	normalized, err := engine.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index := range targets {
		targets[index].PriorPrincipals = append([]string(nil), normalized.PriorPrincipals[targets[index].ObjectID]...)
	}
	manifest := repairTestManifest(targets...)
	manifest.TenantID, manifest.DataDir = engine.manifest.TenantID, engine.manifest.DataDir
	bindRepairManifestToProof(&manifest, normalized)
	engine.manifest = manifest
	if err := engine.AppendLifecycle(ctx, targets[0]); err != nil {
		t.Fatal(err)
	}
	partialJournalProof, err := engine.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalBoardRepairProof(manifest, partialJournalProof); err == nil {
		t.Fatal("real partial journal state was accepted")
	}
	partialPlan, err := engine.buildPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	appended := false
	for _, event := range partialPlan.Events {
		if event.AggregateType == "board_card" && event.AggregateID == targets[0].ObjectID && event.AggregateVersion == targets[0].TargetVersion+1 {
			if _, err := engine.store.Append(ctx, event); err != nil {
				t.Fatal(err)
			}
			appended = true
			break
		}
	}
	if !appended {
		t.Fatal("partial DB replay fixture did not append one repair event")
	}
	partialDBProof, err := engine.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalBoardRepairProof(manifest, partialDBProof); err == nil {
		t.Fatal("real partial journal plus DB state was accepted")
	}
}

func TestCanonicalBoardRepairPostgresUserCorpusDriftFailsClosed(t *testing.T) {
	ctx, engine, _, _ := setupRealCanonicalRepairFixture(t)
	if _, err := engine.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	usersRaw, _ := json.Marshal([]*userAccount{{Email: "member@example.com"}, {Email: "new@example.com"}})
	if err := os.WriteFile(engine.usersPath, usersRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Observe(ctx); err == nil || !strings.Contains(err.Error(), "principal corpus changed") {
		t.Fatalf("users.json drift accepted err=%v", err)
	}
}

func TestCanonicalBoardRepairPartialAppendIsRefusedAndRequiresRestore(t *testing.T) {
	targets := repairTestTargets()
	manifest := repairTestManifest(targets...)
	candidates := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		candidates = append(candidates, repairCandidate(target))
	}
	before := repairProof(candidates, 100)
	bindRepairManifestToProof(&manifest, before)
	interrupted := &fakeCanonicalBoardRepairEngine{proofs: []canonicalBoardRepairProof{before}, failAppendAt: 3}
	if _, err := (canonicalBoardRepairRun{manifest: manifest, engine: interrupted}).execute(context.Background()); err == nil || interrupted.appendCount != 2 {
		t.Fatalf("interruption err=%v engine=%+v", err, interrupted)
	}
	partial := repairProof(candidates[2:], 100)
	partial.Candidates = append(partial.Candidates, CanonicalRepairCandidate{Family: "tombstone", ObjectID: "board_card:" + targets[0].ObjectID, Kind: "missing_event"})
	refusal := &fakeCanonicalBoardRepairEngine{proofs: []canonicalBoardRepairProof{partial}}
	if _, err := (canonicalBoardRepairRun{manifest: manifest, engine: refusal}).execute(context.Background()); err == nil || refusal.applyCount != 0 || refusal.appendCount != 0 {
		t.Fatalf("partial state resumed err=%v engine=%+v", err, refusal)
	}
}

func TestCanonicalBoardRepairNormalizedStartHasNoJournalOrDatabaseBypass(t *testing.T) {
	raw := []byte("sealed")
	seal := canonicalBoardRepairFileSeal{Size: int64(len(raw)), SHA256: sha256Hex(raw)}
	manifest := canonicalBoardRepairManifest{JournalPrefix: seal, VersionMap: seal, Spool: seal, DatabaseSHA256: strings.Repeat("a", 64)}
	if err := validateCanonicalRepairNormalizedStart(manifest, raw, raw, raw, manifest.DatabaseSHA256); err != nil {
		t.Fatal(err)
	}
	partialJournal := append(append([]byte(nil), raw...), []byte("\npartial")...)
	if err := validateCanonicalRepairNormalizedStart(manifest, partialJournal, raw, raw, manifest.DatabaseSHA256); err == nil || !strings.Contains(err.Error(), "cold restore") {
		t.Fatalf("partial journal bypassed normalized start err=%v", err)
	}
	if err := validateCanonicalRepairNormalizedStart(manifest, raw, raw, raw, strings.Repeat("b", 64)); err == nil || !strings.Contains(err.Error(), "cold restore") {
		t.Fatalf("database mismatch bypassed normalized start err=%v", err)
	}
}

func TestCanonicalBoardRepairRejectsPrincipalMismatchAndNonSeven(t *testing.T) {
	targets := repairTestTargets()
	manifest := repairTestManifest(targets...)
	candidates := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		candidates = append(candidates, repairCandidate(target))
	}
	proof := repairProof(candidates, 100)
	proof.PriorPrincipals[targets[0].ObjectID] = []string{"user:wrong@example.com"}
	bindRepairManifestToProof(&manifest, proof)
	if err := validateCanonicalBoardRepairProof(manifest, proof); err == nil || !strings.Contains(err.Error(), "prior principals") {
		t.Fatalf("principal mismatch accepted err=%v", err)
	}
	manifest.Candidates = manifest.Candidates[:6]
	manifest.Schema, manifest.ReleaseCommit, manifest.TenantID, manifest.DataDir, manifest.CloneID = canonicalBoardRepairManifestSchema, strings.Repeat("a", 40), "tenant", "/app/data", "clone"
	manifest.Environment, manifest.EvidenceDir = "production_protected_maintenance", "/evidence"
	manifest.DatabaseURLSHA256, manifest.DatabaseSHA256, manifest.VersionEntriesSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	manifest.NormalizedProofSHA256, manifest.ImportInputSHA256 = strings.Repeat("d", 64), strings.Repeat("e", 64)
	manifest.Board, manifest.JournalPrefix, manifest.VersionMap, manifest.Spool = repairProof(nil, 1).Board, repairProof(nil, 1).Journal, repairProof(nil, 1).VersionMap, repairProof(nil, 1).Spool
	manifest.EvidenceDescriptor = repairEvidenceFile("descriptor.json", strings.Repeat("a", 64))
	manifest.BackupManifest = repairEvidenceFile("backup.json", strings.Repeat("b", 64))
	manifest.NormalizationReceipt = repairEvidenceFile("normalization.json", strings.Repeat("c", 64))
	manifest.CloneAuthority = repairEvidenceFile("clone.json", strings.Repeat("d", 64))
	manifest.ReleaseSourceReceipt = repairEvidenceFile("release.json", strings.Repeat("e", 64))
	manifest.NormalizedObservation = repairEvidenceFile("normalized-observation.json", strings.Repeat("f", 64))
	if err := validateCanonicalBoardRepairInputs(manifest, strings.Repeat("f", 64), "/evidence/receipt.json"); err == nil || !strings.Contains(err.Error(), "exactly seven") {
		t.Fatalf("non-seven manifest accepted err=%v", err)
	}
}

func TestCanonicalBoardRepairManifestGenerationBindsNormalizedProofAndClassifiedEvidence(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", "/app/data/meeting-memory.jsonl")
	targets := repairTestTargets()
	candidates := make([]CanonicalRepairCandidate, 0, len(targets))
	evidenceTargets := make([]canonicalBoardRepairEvidenceTarget, 0, len(targets))
	for _, target := range targets {
		candidates = append(candidates, repairCandidate(target))
		evidenceTargets = append(evidenceTargets, canonicalBoardRepairEvidenceTarget{
			ObjectID: target.ObjectID, ObservedAbsenceAt: target.ObservedAbsenceAt, EvidenceBasis: target.EvidenceBasis, SelectedStateRole: target.SelectedStateRole,
			SourceRecord: target.SourceRecord, ArchiveRecord: target.ArchiveRecord,
			PositiveObservation: target.PositiveObservation, AbsenceEvidence: target.AbsenceEvidence,
		})
	}
	proof := repairProof(candidates, 100)
	descriptor := canonicalBoardRepairEvidenceDescriptor{
		Schema: canonicalBoardRepairEvidenceSchema, ReleaseCommit: strings.Repeat("a", 40), TenantID: "tenant", DataDir: "/app/data",
		CloneID: "clone", Environment: "production_protected_maintenance",
		BackupManifest:        repairEvidenceFile("backup.json", strings.Repeat("a", 64)),
		NormalizationReceipt:  repairEvidenceFile("normalization.json", strings.Repeat("b", 64)),
		CloneAuthority:        repairEvidenceFile("clone.json", strings.Repeat("c", 64)),
		ReleaseSourceReceipt:  repairEvidenceFile("release.json", strings.Repeat("d", 64)),
		NormalizedObservation: repairEvidenceFile("normalized.json", strings.Repeat("e", 64)), Targets: evidenceTargets,
	}
	observation := canonicalBoardRepairObservation{DatabaseURLSHA256: strings.Repeat("f", 64)}
	descriptorFile := repairEvidenceFile("descriptor.json", strings.Repeat("1", 64))
	manifest, err := buildCanonicalBoardRepairManifest("/evidence", descriptor, descriptorFile, observation, proof)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Candidates) != canonicalBoardRepairExactCount || manifest.DatabaseSHA256 != proof.DatabaseSHA256 || manifest.JournalPrefix != proof.Journal || manifest.NormalizationReceipt != descriptor.NormalizationReceipt || manifest.Candidates[0].PriorPrincipals[0] != "user:member@example.com" || manifest.Candidates[0].SelectedStateRole != "archive_record" {
		t.Fatalf("manifest bindings=%+v", manifest)
	}
	if manifest.NormalizedProofSHA256 != canonicalRepairProofFingerprint(proof) || manifest.ImportInputSHA256 != proof.ImportInputSHA256 {
		t.Fatalf("manifest omitted normalized full proof bindings: %+v", manifest)
	}
	manifestRaw, manifestSHA, err := marshalCanonicalBoardRepairManifestFile(manifest)
	if err != nil || len(manifestRaw) == 0 || manifestRaw[len(manifestRaw)-1] != '\n' || manifestSHA != sha256Hex(manifestRaw) {
		t.Fatalf("newline-bearing manifest seal raw=%q sha=%s err=%v", manifestRaw, manifestSHA, err)
	}
	if manifestSHA == sha256Hex(manifestRaw[:len(manifestRaw)-1]) {
		t.Fatal("manifest hash did not bind the terminal newline")
	}
	tampered := descriptor
	tampered.Targets = append([]canonicalBoardRepairEvidenceTarget(nil), descriptor.Targets...)
	tampered.Targets[0].ObjectID = "wrong-private-id"
	if _, err := buildCanonicalBoardRepairManifest("/evidence", tampered, descriptorFile, observation, proof); err == nil {
		t.Fatal("manifest generator accepted evidence/candidate ID mismatch")
	}
}

func TestCanonicalRepairFullFingerprintCoversEverySecondReplayBoundary(t *testing.T) {
	base := repairProof(nil, 10)
	mutations := []func(*canonicalBoardRepairProof){
		func(p *canonicalBoardRepairProof) { p.DatabaseSHA256 = strings.Repeat("6", 64) },
		func(p *canonicalBoardRepairProof) { p.ImportInputSHA256 = strings.Repeat("7", 64) },
		func(p *canonicalBoardRepairProof) { p.OutboxCount++ },
		func(p *canonicalBoardRepairProof) { p.VersionEntryCount++ },
		func(p *canonicalBoardRepairProof) { p.VersionEntriesSHA256 = strings.Repeat("7", 64) },
		func(p *canonicalBoardRepairProof) { p.EventHighWater++ },
		func(p *canonicalBoardRepairProof) { p.SpoolHighWater++ },
		func(p *canonicalBoardRepairProof) { p.Board.SHA256 = strings.Repeat("8", 64) },
		func(p *canonicalBoardRepairProof) { p.Journal.SHA256 = strings.Repeat("9", 64) },
		func(p *canonicalBoardRepairProof) { p.VersionMap.Size++ },
		func(p *canonicalBoardRepairProof) { p.Spool.Size++ },
	}
	baseDigest := canonicalRepairProofFingerprint(base)
	for index, mutate := range mutations {
		changed := base
		mutate(&changed)
		if canonicalRepairProofFingerprint(changed) == baseDigest {
			t.Fatalf("mutation %d omitted from full proof fingerprint", index)
		}
	}
}

func TestCanonicalRepairSpoolPartialTailInspectionNeverMutatesLiveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mutation-spool.bcs")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, _, err := inspectCanonicalRepairSpoolFromScratch(path); err == nil {
		t.Fatal("partial spool tail accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("spool inspection mutated live partial tail")
	}
}

func TestCanonicalRepairEvidenceFilesRejectTamperMissingAndSymlink(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root ownership contract")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "evidence.json")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := canonicalBoardRepairEvidenceFile{Path: "evidence.json", Size: 1, SHA256: sha256Hex([]byte("x"))}
	if _, err := validateCanonicalRepairEvidenceFiles(dir, []canonicalBoardRepairEvidenceFile{ref}); err != nil {
		t.Fatal(err)
	}
	tampered := ref
	tampered.SHA256 = strings.Repeat("f", 64)
	if _, err := validateCanonicalRepairEvidenceFiles(dir, []canonicalBoardRepairEvidenceFile{tampered}); err == nil {
		t.Fatal("tampered evidence accepted")
	}
	missing := ref
	missing.Path = "missing.json"
	if _, err := validateCanonicalRepairEvidenceFiles(dir, []canonicalBoardRepairEvidenceFile{missing}); err == nil {
		t.Fatal("missing evidence accepted")
	}
	if err := os.Symlink(path, filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	link := ref
	link.Path = "link.json"
	if _, err := validateCanonicalRepairEvidenceFiles(dir, []canonicalBoardRepairEvidenceFile{link}); err == nil {
		t.Fatal("symlink evidence accepted")
	}
}

func TestCanonicalBoardRepairReceiptReuseRequiresLiveFullFingerprint(t *testing.T) {
	targets := repairTestTargets()
	manifest := repairTestManifest(targets...)
	manifest.ReleaseCommit, manifest.TenantID, manifest.CloneID, manifest.Environment = strings.Repeat("a", 40), "tenant", "production", "production_protected_maintenance"
	candidates := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		candidates = append(candidates, repairCandidate(target))
	}
	before := repairProof(candidates, 100)
	live := repairFinalProof(before, targets)
	manifest.Board, manifest.JournalPrefix, manifest.VersionMap, manifest.DatabaseSHA256 = before.Board, before.Journal, before.VersionMap, before.DatabaseSHA256
	manifest.VersionEntriesSHA256, manifest.Spool = before.VersionEntriesSHA256, before.Spool
	bindRepairManifestToProof(&manifest, before)
	fingerprint := canonicalRepairProofFingerprint(live)
	beforeState, _ := canonicalRepairStateFromProof(before)
	afterState, _ := canonicalRepairStateFromProof(live)
	completedAt := live.JournalRecords[len(live.JournalRecords)-1].At.Add(time.Second)
	receipt := canonicalBoardRepairReceipt{
		Schema: canonicalBoardRepairReceiptSchema, Status: "complete", ReleaseCommit: manifest.ReleaseCommit, Version: manifest.ReleaseCommit,
		TenantID: manifest.TenantID, CloneID: manifest.CloneID, Environment: manifest.Environment, ManifestSHA256: strings.Repeat("b", 64), AuthoritySHA256: strings.Repeat("c", 64),
		CandidateSetSHA256: manifest.CandidateSetSHA256, CandidateCount: 7, AppliedCount: 7, FirstAppendObserved: true,
		Before: canonicalBoardRepairWatermarks{EventHighWater: before.EventHighWater, CaptureSpoolHighWater: before.SpoolHighWater}, After: canonicalBoardRepairWatermarks{EventHighWater: live.EventHighWater, CaptureSpoolHighWater: live.SpoolHighWater},
		BeforeState: beforeState, AfterState: afterState, Delta: canonicalBoardRepairCountDelta{TenantEvents: 7, ImportOutbox: 7, VersionEntries: 7}, JournalAppendedRecords: append([]CanonicalLifecycleJournalRecord(nil), live.JournalRecords...),
		BeforeCandidateSHA256: beforeState.CandidateSHA256, AfterCandidateSHA256: afterState.CandidateSHA256,
		ZeroCandidates: true, PrincipalParity: true, ProjectionParity: true, IdempotentSecondReplay: true,
		AfterFingerprintSHA256: fingerprint, BoardSHA256: before.Board.SHA256,
		JournalBeforeSHA256: before.Journal.SHA256, JournalAfterSHA256: live.Journal.SHA256,
		VersionMapBeforeSHA256: before.VersionMap.SHA256, VersionMapAfterSHA256: live.VersionMap.SHA256,
		DatabaseBeforeSHA256: before.DatabaseSHA256, DatabaseAfterSHA256: live.DatabaseSHA256, CompletedAt: completedAt,
	}
	if err := validateExistingCanonicalBoardRepairReceipt(receipt, manifest, receipt.ManifestSHA256, receipt.AuthoritySHA256, fingerprint); err != nil {
		t.Fatal(err)
	}
	tampered := live
	tampered.OutboxCount++
	if err := validateExistingCanonicalBoardRepairReceipt(receipt, manifest, receipt.ManifestSHA256, receipt.AuthoritySHA256, canonicalRepairProofFingerprint(tampered)); err == nil {
		t.Fatal("receipt reuse accepted tampered live full proof")
	}
	tamperedReceipt := receipt
	tamperedReceipt.JournalAppendedRecords = append([]CanonicalLifecycleJournalRecord(nil), receipt.JournalAppendedRecords...)
	tamperedReceipt.JournalAppendedRecords[0].ObjectID = "other"
	if err := validateExistingCanonicalBoardRepairReceipt(tamperedReceipt, manifest, receipt.ManifestSHA256, receipt.AuthoritySHA256, fingerprint); err == nil {
		t.Fatal("receipt reuse accepted a journal record not bound to manifest order")
	}
}

func TestCanonicalBoardNormalizationReceiptSelfDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "normalization-receipt.json")
	receipt := canonicalBoardNormalizationReceipt{Schema: canonicalBoardNormalizationReceiptSchema, Status: "complete", ExactTerminalSeven: true, FullZeroDeltaSecondReplay: true}
	if err := writeCanonicalBoardNormalizationReceipt(path, &receipt); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if _, err := decodeCanonicalBoardNormalizationReceipt(raw); err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	object["status"] = "tampered"
	raw, _ = json.Marshal(object)
	if _, err := decodeCanonicalBoardNormalizationReceipt(raw); err == nil {
		t.Fatal("tampered normalization receipt accepted")
	}
}

func TestCanonicalBoardRepairAuthorityIsLiteralAndFresh(t *testing.T) {
	digest := strings.Repeat("a", 64)
	valid := []byte("CONFIRM CANONICAL BOARD REPAIR " + digest + "\n")
	if err := validateCanonicalRepairAuthority(valid, digest); err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalRepairAuthority(valid[:len(valid)-1], digest); err == nil {
		t.Fatal("nonliteral authority accepted")
	}
	path := filepath.Join(t.TempDir(), "authority")
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stale := now.Add(-6 * time.Minute)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalRepairAuthorityFresh(path, now); err == nil {
		t.Fatal("stale authority accepted")
	}
}

func TestRepairMemberPrincipalsFailsClosed(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	if _, err := repairMemberPrincipals(missing); err == nil {
		t.Fatal("missing users accepted")
	}
	path := filepath.Join(dir, "users.json")
	for _, raw := range [][]byte{[]byte("{"), []byte("[]"), []byte("[null]")} {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := repairMemberPrincipals(path); err == nil {
			t.Fatalf("invalid principals accepted: %s", raw)
		}
	}
}

func TestCanonicalImportPathsForRepairUsesRuntimeQueueResolvers(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", "/app/data/meeting-memory.jsonl")
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", "/app/codex-queue/jobs")
	t.Setenv("BONFIRE_RENDER_QUEUE_PATH", "/app/render-queue/jobs")
	paths := canonicalImportPathsForRepair("/app/data")
	want := []string{codexRunnerQueuePath(), renderRunnerQueuePath()}
	if len(paths.QueueDirs) != 2 || paths.QueueDirs[0] != want[0] || paths.QueueDirs[1] != want[1] {
		t.Fatalf("repair queues=%v runtime queues=%v", paths.QueueDirs, want)
	}
}

func TestCanonicalRepairBackfillLifecycleSuppressionIsExactAndOrdinaryRecordsRemainAudited(t *testing.T) {
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	exact := CanonicalLifecycleJournalRecord{Family: "board_card", ObjectID: "card", StateDigest: strings.Repeat("a", 64), At: at, Reason: canonicalBoardRepairReason, EvidenceBasis: "done_archive_absence"}
	if !canonicalRepairBackfillLifecycleOnly(exact, "tombstone") {
		t.Fatal("exact repair backfill did not suppress redundant standalone audit object")
	}
	mutations := []func(*CanonicalLifecycleJournalRecord){
		func(r *CanonicalLifecycleJournalRecord) { r.Reason = "ordinary_delete" },
		func(r *CanonicalLifecycleJournalRecord) { r.EvidenceBasis = "other" },
		func(r *CanonicalLifecycleJournalRecord) { r.Phase = "committed" },
		func(r *CanonicalLifecycleJournalRecord) { r.OperationID = "operation" },
		func(r *CanonicalLifecycleJournalRecord) { r.At = time.Time{} },
	}
	for index, mutate := range mutations {
		changed := exact
		mutate(&changed)
		if canonicalRepairBackfillLifecycleOnly(changed, "tombstone") {
			t.Fatalf("non-repair lifecycle mutation %d lost its standalone audit object", index)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "deleted.jsonl")
	ordinary := exact
	ordinary.Reason = "ordinary_delete"
	raw, _ := json.Marshal(ordinary)
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	objects, err := importLifecycleJournal(path, "tombstone")
	if err != nil || len(objects) != 1 || objects[0].LifecycleOnly {
		t.Fatalf("ordinary lifecycle audit semantics changed objects=%+v err=%v", objects, err)
	}
}

func TestCanonicalRepairExactDeltaAndTimestampContractsRejectNearMisses(t *testing.T) {
	targets := repairTestTargets()
	candidates := make([]CanonicalRepairCandidate, 0, len(targets))
	for _, target := range targets {
		candidates = append(candidates, repairCandidate(target))
	}
	before := repairProof(candidates, 100)
	after := repairFinalProof(before, targets)
	if err := validateCanonicalRepairTransition(before, after, targets); err != nil {
		t.Fatal(err)
	}
	wrongCount := after
	wrongCount.EventCount--
	if err := validateCanonicalRepairTransition(before, wrongCount, targets); err == nil {
		t.Fatal("repair accepted +6 tenant event count")
	}
	stale := after
	stale.JournalRecords = append([]CanonicalLifecycleJournalRecord(nil), after.JournalRecords...)
	stale.JournalRecords[0].At = time.Date(2026, 8, 2, 9, 59, 59, 0, time.UTC)
	if err := validateCanonicalRepairTransition(before, stale, targets); err == nil {
		t.Fatal("repair accepted a journal append before observed absence")
	}
	normalBefore := repairProof(append(candidates,
		CanonicalRepairCandidate{Family: "memory", ObjectID: "extra-1", Kind: "missing_event"},
		CanonicalRepairCandidate{Family: "notification", ObjectID: "extra-2", Kind: "state_mismatch"},
	), 50)
	normalAfter := repairProof(candidates, 52)
	if err := validateCanonicalNormalizationTransition(normalBefore, normalAfter); err != nil {
		t.Fatal(err)
	}
	drift := normalAfter
	drift.Board.Size++
	if err := validateCanonicalNormalizationTransition(normalBefore, drift); err == nil {
		t.Fatal("normalization accepted visible board drift")
	}
}

func TestCanonicalRepairBackupManifestSemanticParser(t *testing.T) {
	required := []string{
		"./postgres.pgcustom", "./postgres.list", "./migrations-before.tsv", "./table-counts-before.tsv", "./private/volumes.inspect.json", "./private/containers.inspect.json",
		"./private/base.env", "./private/legacy-docker-compose.yml", "./private/legacy-Caddyfile", "./private/opt-meetingassist.tar", "./private/opt-meetingassist-workspace.tar",
		"./images/legacy-images.tar", "./meta/legacy-image-map.tsv", "./meta/networks.inspect.json", "./meta/legacy-container-authority.tsv", "./meta/expected-volumes", "./meta/actual-volumes",
		"./volumes/digitalocean_caddy_config.tar", "./volumes/digitalocean_caddy_data.tar", "./volumes/digitalocean_canonical_postgres.tar", "./volumes/digitalocean_codex_home.tar",
		"./volumes/digitalocean_codex_queue.tar", "./volumes/digitalocean_codex_runner_data.tar", "./volumes/digitalocean_meeting_data.tar", "./volumes/digitalocean_usage_ledger.tar",
	}
	var builder strings.Builder
	for index, path := range required {
		fmt.Fprintf(&builder, "%s  %s\n", strings.Repeat(string(rune('a'+index%6)), 64), path)
	}
	if err := validateCanonicalBackupManifest([]byte(builder.String())); err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalBackupManifest([]byte(strings.Replace(builder.String(), "./postgres.pgcustom", "./../escape", 1))); err == nil {
		t.Fatal("unsafe backup manifest path accepted")
	}
	if err := validateCanonicalBackupManifest([]byte(strings.Replace(builder.String(), required[len(required)-1]+"\n", "", 1))); err == nil {
		t.Fatal("backup manifest missing a protected volume accepted")
	}
}

func TestCanonicalRepairReleaseSourceReceiptSemanticParser(t *testing.T) {
	paths := []string{".dockerignore", "Dockerfile", "Dockerfile.render", "go.mod", "go.sum", "deploy/digitalocean/docker-compose.yml", "deploy/digitalocean/Caddyfile", "deploy/digitalocean/bonfire-render-runner-v1.apparmor", "deploy/digitalocean/bonfire-render-runner-v1.seccomp.json", "deploy/digitalocean/release-build-inputs.json", "deploy/digitalocean/release-scope-policy.json", "scripts/bonfire-release.mjs"}
	files := map[string]string{}
	for _, path := range paths {
		files[path] = strings.Repeat("a", 64)
	}
	configDigest, _ := canonicalReleaseConfigDigest(files, paths)
	release := strings.Repeat("b", 40)
	receipt := canonicalReleaseSourceReceipt{Schema: "bonfire.release-source.v3", ReleaseCommit: release, ReviewedRef: release, GitTreeObject: strings.Repeat("c", 40), GitTreeDigest: strings.Repeat("d", 64), ReviewedInventorySHA256: strings.Repeat("e", 64), ScopePolicySHA256: files["deploy/digitalocean/release-scope-policy.json"], SourceArchiveSHA256: strings.Repeat("f", 64), TransitiveInputsSHA256: strings.Repeat("1", 64), BuildConfigSHA256: configDigest, ConfigFiles: files, InputCount: 20, SourceDateEpoch: 1}
	raw, _ := json.Marshal(receipt)
	if err := validateCanonicalReleaseSourceReceipt(raw, release); err != nil {
		t.Fatal(err)
	}
	receipt.BuildConfigSHA256 = strings.Repeat("2", 64)
	raw, _ = json.Marshal(receipt)
	if err := validateCanonicalReleaseSourceReceipt(raw, release); err == nil {
		t.Fatal("release source receipt accepted a forged config digest")
	}
}

func TestCanonicalNormalizationAuthorityMarkerIsEnvironmentDiscriminated(t *testing.T) {
	observation, backup := strings.Repeat("a", 64), strings.Repeat("b", 64)
	production := canonicalBoardNormalizationInput{Environment: "production_protected_maintenance", BeforeObservation: repairEvidenceFile("observation.json", observation), BackupReceipt: repairEvidenceFile("backup", backup)}
	productionWant := "AUTHORIZE CANONICAL BOARD NORMALIZATION " + observation + " " + backup + "\n"
	if got := canonicalNormalizationAuthorityText(production); got != productionWant || strings.Contains(got, "clone-one") {
		t.Fatalf("production marker=%q", got)
	}
	clone := production
	clone.Environment, clone.QualificationRun, clone.CloneID = "isolated_cold_clone", true, "clone-one"
	cloneWant := "AUTHORIZE CANONICAL BOARD NORMALIZATION clone-one " + observation + " " + backup + "\n"
	if got := canonicalNormalizationAuthorityText(clone); got != cloneWant || got == productionWant {
		t.Fatalf("clone marker=%q", got)
	}
	wrongClone := clone
	wrongClone.CloneID = "clone-two"
	if canonicalNormalizationAuthorityText(wrongClone) == cloneWant {
		t.Fatal("clone authority marker did not bind cloneId")
	}
}

func TestCanonicalColdCloneReceiptBindsRunAndChronology(t *testing.T) {
	release, backup := strings.Repeat("a", 40), strings.Repeat("b", 64)
	completed := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	authorized := completed.Add(time.Second)
	receipt := canonicalColdCloneReceipt{
		Schema: canonicalColdCloneReceiptSchema, Status: "complete", ReleaseCommit: release, CloneID: "clone-a", QualificationRun: true, BackupManifestSHA256: backup,
		RestoredVolumeCount: 8, RestoredVolumes: []string{"digitalocean_caddy_config", "digitalocean_caddy_data", "digitalocean_canonical_postgres", "digitalocean_codex_home", "digitalocean_codex_queue", "digitalocean_codex_runner_data", "digitalocean_meeting_data", "digitalocean_usage_ledger"},
		RawVolumeCompare: true, PostgresRestore: true, PostgresDumpSHA256: strings.Repeat("c", 64), MigrationRowsSHA256: strings.Repeat("d", 64), TableCountsSHA256: strings.Repeat("e", 64), CompletedAt: completed,
	}
	if err := validateCanonicalColdCloneBinding(receipt, release, "clone-a", backup, authorized); err != nil {
		t.Fatal(err)
	}
	wrongClone := receipt
	wrongClone.CloneID = "clone-b"
	if err := validateCanonicalColdCloneBinding(wrongClone, release, "clone-a", backup, authorized); err == nil {
		t.Fatal("cold clone receipt replayed across clone IDs")
	}
	notQualification := receipt
	notQualification.QualificationRun = false
	if err := validateCanonicalColdCloneBinding(notQualification, release, "clone-a", backup, authorized); err == nil {
		t.Fatal("generic cold clone receipt authorized a qualification run")
	}
	if err := validateCanonicalColdCloneBinding(receipt, release, "clone-a", backup, completed.Add(-time.Second)); err == nil {
		t.Fatal("authority predating cold restore was accepted")
	}
}

func TestCanonicalNormalizationInputRejectsCrossEnvironmentReplay(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", "/app/data/meeting-memory.jsonl")
	base := canonicalBoardNormalizationInput{Schema: canonicalBoardNormalizationInputSchema, ReleaseCommit: strings.Repeat("a", 40), TenantID: "tenant", DataDir: "/app/data", EvidenceDir: "/evidence", Environment: "production_protected_maintenance", CloneID: "production", DatabaseURLSHA256: strings.Repeat("b", 64), BeforeFingerprintSHA256: strings.Repeat("c", 64), ExpectedTerminalCandidateCount: 7, MaxApplyPasses: 1}
	base.BackupReceipt = repairEvidenceFile("backup", strings.Repeat("d", 64))
	base.FenceReceipt = repairEvidenceFile("fence", strings.Repeat("e", 64))
	base.NormalizationAuthorityMarker = repairEvidenceFile("authority", strings.Repeat("f", 64))
	base.BeforeObservation = repairEvidenceFile("before", strings.Repeat("1", 64))
	productionAsClone := base
	productionAsClone.QualificationRun = true
	if err := validateCanonicalBoardNormalizationInput(productionAsClone, "/evidence/input", "/evidence/receipt"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("production input accepted qualificationRun=true err=%v", err)
	}
	cloneAsProduction := base
	cloneAsProduction.Environment = "isolated_cold_clone"
	if err := validateCanonicalBoardNormalizationInput(cloneAsProduction, "/evidence/input", "/evidence/receipt"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("clone input accepted qualificationRun=false err=%v", err)
	}
}
