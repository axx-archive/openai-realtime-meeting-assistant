package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CanonicalRuntime is the production bridge between the durable legacy files
// and the canonical event store. A file mutation is fenced as:
//
//	durable prepare -> durable legacy mutation -> durable commit -> PG append
//
// The spool therefore makes every crash outcome recoverable. Required mode
// propagates every fence/store failure to the caller; shadow mode keeps serving
// while making the failure visible in readiness.
type CanonicalRuntime struct {
	mu                  sync.Mutex
	reconcileMu         sync.Mutex
	mode                CanonicalMode
	dataDir             string
	root                string
	tenantID            string
	registry            *CanonicalPayloadRegistry
	spool               *CanonicalCaptureSpool
	versions            *FileCanonicalObjectVersionMap
	postgres            *PostgresCanonicalStore
	events              CanonicalEventStore
	reconcileSignal     chan struct{}
	reconcileStop       chan struct{}
	reconcileWG         sync.WaitGroup
	reconcileCtx        context.Context
	reconcileCancel     context.CancelFunc
	dirtyHighWater      uint64
	reconciledHighWater uint64
	outboxKnown         bool
	outboxPending       int64
	outboxFailed        int64
	outboxOldestSeconds int64
	checkpointValid     bool
	checkpointHighWater uint64
	checkpointSourceSHA string

	healthErr error
	lastOK    time.Time
}

type CanonicalRuntimeSnapshot struct {
	Mode                string   `json:"mode"`
	Healthy             bool     `json:"healthy"`
	Required            bool     `json:"required"`
	Database            bool     `json:"database"`
	HighWater           uint64   `json:"highWater"`
	DirtyHighWater      uint64   `json:"dirtyHighWater"`
	ReconciledHighWater uint64   `json:"reconciledHighWater"`
	Pending             int      `json:"pending"`
	FrozenFamilies      []string `json:"frozenFamilies,omitempty"`
	CoveredFamilies     []string `json:"coveredFamilies,omitempty"`
	UncoveredFamilies   []string `json:"uncoveredFamilies,omitempty"`
	LastSuccess         string   `json:"lastSuccess,omitempty"`
	Error               string   `json:"error,omitempty"`
	OutboxPending       int64    `json:"outboxPending"`
	OutboxFailed        int64    `json:"outboxFailed"`
	OutboxOldestSeconds int64    `json:"outboxOldestSeconds"`
	OutboxKnown         bool     `json:"outboxKnown"`
	CheckpointValid     bool     `json:"checkpointValid"`
	CheckpointHighWater uint64   `json:"checkpointHighWater"`
}

var canonicalRuntimeState struct {
	sync.RWMutex
	runtime *CanonicalRuntime
}

var canonicalLifecycleJournalMu sync.Mutex

func readCanonicalLifecycleJournal(path string) ([]CanonicalLifecycleJournalRecord, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []CanonicalLifecycleJournalRecord
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var existing CanonicalLifecycleJournalRecord
		if err := json.Unmarshal(line, &existing); err != nil {
			return nil, err
		}
		records = append(records, existing)
	}
	return records, nil
}

func ensureCanonicalLifecycleJournal(path string, record CanonicalLifecycleJournalRecord) error {
	canonicalLifecycleJournalMu.Lock()
	defer canonicalLifecycleJournalMu.Unlock()
	records, err := readCanonicalLifecycleJournal(path)
	if err != nil {
		return err
	}
	for _, existing := range records {
		if existing.Family != record.Family || existing.ObjectID != record.ObjectID {
			continue
		}
		if existing.StateDigest != record.StateDigest {
			return fmt.Errorf("conflicting lifecycle journal for %s/%s", record.Family, record.ObjectID)
		}
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return appendFileDurably(path, append(encoded, '\n'), 0o600)
}

func initializeCanonicalRuntime(ctx context.Context) (*CanonicalRuntime, error) {
	modeText, err := canonicalModeFromEnvironment()
	if err != nil {
		return nil, err
	}
	mode := CanonicalMode(modeText)
	dataDir := filepath.Dir(meetingMemoryPath())
	runtime := &CanonicalRuntime{mode: mode, dataDir: dataDir, tenantID: canonicalTenantID(), lastOK: time.Now().UTC()}
	if mode == CanonicalModeOff {
		setCanonicalRuntime(runtime)
		return runtime, nil
	}

	runtime.root = filepath.Join(dataDir, "canonical")
	if err := os.MkdirAll(runtime.root, 0o700); err != nil {
		return nil, fmt.Errorf("create canonical runtime directory: %w", err)
	}
	runtime.registry, err = NewCanonicalImportPayloadRegistry()
	if err != nil {
		return nil, err
	}
	runtime.spool, err = OpenCanonicalCaptureSpool(filepath.Join(runtime.root, "mutation-spool.bcs"), mode)
	if err != nil {
		return nil, fmt.Errorf("open canonical mutation spool: %w", err)
	}
	runtime.versions, err = OpenFileCanonicalObjectVersionMap(filepath.Join(runtime.root, "object-versions.json"))
	if err != nil {
		return nil, fmt.Errorf("open canonical object versions: %w", err)
	}

	// Install before recovery/import. Canonical-owned paths are explicitly
	// excluded from mutation capture, preventing recursive fencing.
	setCanonicalRuntime(runtime)
	recovery, err := runtime.spool.Recover(runtime.resolveLegacyState)
	if err != nil {
		return nil, fmt.Errorf("recover canonical mutation spool: %w", err)
	}
	if len(recovery.FrozenFamilies) > 0 {
		recoveryErr := fmt.Errorf("canonical recovery froze families: %s", strings.Join(recovery.FrozenFamilies, ","))
		if mode == CanonicalModeRequired {
			return nil, recoveryErr
		}
		runtime.markFailure(recoveryErr)
	}
	if err := runtime.materializeCommittedDeleteJournals(); err != nil {
		if mode == CanonicalModeRequired {
			return nil, fmt.Errorf("canonical delete-journal recovery: %w", err)
		}
		runtime.markFailure(fmt.Errorf("canonical delete-journal recovery degraded: %w", err))
	}
	if err := runtime.completeJournaledBlobDeletes(); err != nil {
		if mode == CanonicalModeRequired {
			return nil, fmt.Errorf("canonical blob-delete recovery: %w", err)
		}
		runtime.markFailure(fmt.Errorf("canonical blob-delete recovery degraded: %w", err))
	}
	if err := recoverJournaledExpiredGuestLinks(roomsFilePath(), filepath.Join(runtime.dataDir, "evicted-objects.jsonl")); err != nil {
		if mode == CanonicalModeRequired {
			return nil, fmt.Errorf("canonical guest-link expiry recovery: %w", err)
		}
		runtime.markFailure(fmt.Errorf("canonical guest-link expiry recovery degraded: %w", err))
	}
	// A missing/corrupt/mismatched checkpoint is not trusted. Boot continues
	// into the full importer/PG parity reconciliation below and rewrites it.
	_, _ = runtime.loadReconcileCheckpoint()

	databaseURL := strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_DATABASE_URL"))
	if databaseURL != "" {
		runtime.postgres, err = OpenPostgresCanonicalStore(ctx, databaseURL, runtime.registry)
		if err == nil {
			err = runtime.postgres.ApplyMigrations(ctx)
			if err == nil {
				runtime.events = runtime.postgres
			}
		}
		if err != nil {
			if runtime.postgres != nil {
				runtime.postgres.Close()
				runtime.postgres = nil
			}
			if mode == CanonicalModeRequired {
				return nil, fmt.Errorf("canonical PostgreSQL is required: %w", err)
			}
			runtime.markFailure(fmt.Errorf("canonical PostgreSQL degraded: %w", err))
		}
	} else if mode == CanonicalModeRequired {
		return nil, errors.New("BONFIRE_CANONICAL_DATABASE_URL is required in canonical required mode")
	} else {
		runtime.markFailure(errors.New("canonical PostgreSQL is not configured"))
	}

	// A boot scan validates every registered legacy family even when shadow PG
	// is absent. A source+spool-bound checkpoint may take the read-only resume
	// path; otherwise boot performs the full import/grant/reconcile sequence.
	plan, buildErr := runtime.buildLegacyPlan(ctx)
	bootResumed := false
	if buildErr != nil {
		if mode == CanonicalModeRequired {
			return nil, fmt.Errorf("canonical boot import: %w", buildErr)
		}
		runtime.markFailure(fmt.Errorf("canonical boot scan degraded: %w", buildErr))
	} else if runtime.postgres != nil {
		bootResumed, err = runtime.tryResumeCheckpoint(ctx, plan)
		if err != nil {
			bootResumed = false
		}
		if !bootResumed {
			if err := plan.Apply(ctx, runtime.postgres); err != nil {
				if mode == CanonicalModeRequired {
					return nil, fmt.Errorf("canonical boot import: %w", err)
				}
				runtime.markFailure(fmt.Errorf("canonical boot import degraded: %w", err))
			} else if err := runtime.postgres.SyncImportGrants(ctx, plan); err != nil {
				if mode == CanonicalModeRequired {
					return nil, fmt.Errorf("canonical boot grants: %w", err)
				}
				runtime.markFailure(fmt.Errorf("canonical boot grants degraded: %w", err))
			} else {
				runtime.markSuccess()
			}
		}
	}
	if runtime.events != nil {
		runtime.dirtyHighWater = runtime.spoolHighWater()
		if !bootResumed {
			if reconcileErr := runtime.Reconcile(ctx); reconcileErr != nil {
				if mode == CanonicalModeRequired {
					return nil, fmt.Errorf("canonical boot parity: %w", reconcileErr)
				}
				runtime.markFailure(reconcileErr)
			}
		}
		runtime.startReconcileLoop()
	}
	return runtime, nil
}

func (runtime *CanonicalRuntime) materializeCommittedDeleteJournals() error {
	for _, captured := range runtime.spool.CommittedFacts() {
		var fact struct {
			Deleted bool `json:"deleted"`
		}
		if json.Unmarshal(captured.Prepare.Fact, &fact) != nil || !fact.Deleted || captured.Prepare.Family != "blob" {
			continue
		}
		ref := filepath.Base(captured.Prepare.ObjectKey)
		if !validBlobRef(ref) {
			continue // metadata deletion is represented by the data object's journal
		}
		if _, err := os.Stat(captured.Prepare.ObjectKey); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		record := CanonicalLifecycleJournalRecord{Family: "blob", ObjectID: ref, StateDigest: ref, At: time.Now().UTC(), Reason: "recovered_committed_blob_delete"}
		if err := ensureCanonicalLifecycleJournal(filepath.Join(runtime.dataDir, "evicted-objects.jsonl"), record); err != nil {
			return err
		}
	}
	return nil
}

func (runtime *CanonicalRuntime) completeJournaledBlobDeletes() error {
	path := filepath.Join(runtime.dataDir, "evicted-objects.jsonl")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record CanonicalLifecycleJournalRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		if record.Family != "blob" || !validBlobRef(record.ObjectID) {
			continue
		}
		dataPath, metaPath, err := blobPaths(record.ObjectID)
		if err != nil {
			return err
		}
		if rawMeta, metaErr := os.ReadFile(metaPath); metaErr == nil {
			var meta blobMeta
			if json.Unmarshal(rawMeta, &meta) == nil {
				if created, parseErr := time.Parse(time.RFC3339Nano, meta.CreatedAt); parseErr == nil && created.After(record.At) {
					continue // a newer explicit recreation wins over the old intent
				}
			}
		}
		for _, target := range []string{metaPath, dataPath} {
			if _, statErr := os.Stat(target); errors.Is(statErr, os.ErrNotExist) {
				continue
			} else if statErr != nil {
				return statErr
			}
			if err := canonicalFenceRemoveMutation(target, func() error { return os.Remove(target) }); err != nil {
				return err
			}
		}
	}
	return nil
}

func canonicalTenantID() string {
	if value := strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_TENANT_ID")); value != "" {
		return value
	}
	return "bonfire"
}

func setCanonicalRuntime(runtime *CanonicalRuntime) {
	canonicalRuntimeState.Lock()
	canonicalRuntimeState.runtime = runtime
	canonicalRuntimeState.Unlock()
}

func currentCanonicalRuntime() *CanonicalRuntime {
	canonicalRuntimeState.RLock()
	defer canonicalRuntimeState.RUnlock()
	return canonicalRuntimeState.runtime
}

func closeCanonicalRuntime() {
	canonicalRuntimeState.Lock()
	runtime := canonicalRuntimeState.runtime
	canonicalRuntimeState.runtime = nil
	canonicalRuntimeState.Unlock()
	if runtime != nil {
		if runtime.reconcileStop != nil {
			if runtime.reconcileCancel != nil {
				runtime.reconcileCancel()
			}
			close(runtime.reconcileStop)
			runtime.reconcileWG.Wait()
		}
		if runtime.postgres != nil {
			runtime.postgres.Close()
		}
	}
}

func (runtime *CanonicalRuntime) buildLegacyPlan(ctx context.Context) (CanonicalImportPlan, error) {
	memberPrincipals := runtimeMemberPrincipals()
	importer := &CanonicalImporter{
		TenantID:      runtime.tenantID,
		Registry:      runtime.registry,
		Versions:      runtime.versions,
		OrgPrincipals: memberPrincipals,
		Paths: CanonicalImportPaths{
			MeetingMemory: meetingMemoryPath(), Board: kanbanBoardPath(), Rooms: roomsFilePath(),
			Meetings: meetingsPath(), Notifications: notificationsPath(), ShareLinks: shareLinksPath(),
			FileFolders: fileFoldersFilePath(), QueueDirs: []string{codexRunnerQueuePath(), renderRunnerQueuePath()},
			ArchivesDir: filepath.Join(runtime.dataDir, "archives"), BlobsDir: filepath.Join(runtime.dataDir, "blobs"),
			DeletedJournal: filepath.Join(runtime.dataDir, "deleted-objects.jsonl"),
			EvictedJournal: filepath.Join(runtime.dataDir, "evicted-objects.jsonl"),
		},
	}
	return importer.Build(ctx)
}

func runtimeMemberPrincipals() []string {
	raw, err := os.ReadFile(usersFilePath())
	if err != nil {
		return nil
	}
	var users []*userAccount
	if json.Unmarshal(raw, &users) != nil {
		return nil
	}
	principals := make([]string, 0, len(users))
	for _, user := range users {
		if user != nil {
			if email := normalizeAccountEmail(user.Email); email != "" {
				principals = append(principals, "user:"+email)
			}
		}
	}
	return uniqueSortedStrings(principals)
}

// canonicalFenceFileMutation is called by the shared atomic/append primitives.
// Unknown and derived stores are deliberately ignored; callers can inspect the
// coverage list in canonicalRuntimeFamilyForPath.
func canonicalFenceFileMutation(path string, after []byte, mutate func() error) error {
	runtime := currentCanonicalRuntime()
	if runtime == nil || runtime.mode == CanonicalModeOff || runtime.ownsPath(path) {
		return mutate()
	}
	family, ok := runtime.familyForPath(path)
	if !ok {
		return mutate()
	}
	return runtime.mutateFile(context.Background(), family, path, after, false, mutate)
}

func canonicalFenceAppendMutation(path string, appended []byte, mutate func() error) error {
	runtime := currentCanonicalRuntime()
	if runtime == nil || runtime.mode == CanonicalModeOff || runtime.ownsPath(path) {
		return mutate()
	}
	family, ok := runtime.familyForPath(path)
	if !ok {
		return mutate()
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	before, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return runtime.prepareFailureOrShadow(fmt.Errorf("read legacy state before canonical prepare: %w", err), mutate)
	}
	after := append(append([]byte(nil), before...), appended...)
	return runtime.mutateFileLocked(context.Background(), family, path, before, err == nil, after, false, mutate)
}

func canonicalFenceRemoveMutation(path string, mutate func() error) error {
	runtime := currentCanonicalRuntime()
	if runtime == nil || runtime.mode == CanonicalModeOff || runtime.ownsPath(path) {
		return mutate()
	}
	family, ok := runtime.familyForPath(path)
	if !ok {
		return mutate()
	}
	return runtime.mutateFile(context.Background(), family, path, nil, true, mutate)
}

func (runtime *CanonicalRuntime) mutateFile(ctx context.Context, family, path string, after []byte, deleted bool, mutate func() error) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	before, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return runtime.prepareFailureOrShadow(fmt.Errorf("read legacy state before canonical prepare: %w", readErr), mutate)
	}
	return runtime.mutateFileLocked(ctx, family, path, before, readErr == nil, after, deleted, mutate)
}

func (runtime *CanonicalRuntime) mutateFileLocked(ctx context.Context, family, path string, before []byte, beforeExists bool, after []byte, deleted bool, mutate func() error) error {
	beforeDigest := ""
	if beforeExists {
		beforeDigest = sha256Hex(before)
	}
	afterDigest := sha256Hex(after)
	objectKey := filepath.Clean(path)
	mutationID := uuid.NewString()
	fact, _ := json.Marshal(map[string]any{"source_kind": family, "object_id": sha256Hex([]byte(objectKey)), "payload_sha256": afterDigest, "deleted": deleted})
	if _, err := runtime.spool.Prepare(mutationID, family, objectKey, beforeDigest, afterDigest, fact); err != nil {
		return runtime.prepareFailureOrShadow(fmt.Errorf("canonical prepare: %w", err), mutate)
	}
	if err := mutate(); err != nil {
		if errors.Is(err, ErrDurableReplaceAmbiguous) {
			visible, visibleErr := os.ReadFile(path)
			if visibleErr == nil && sha256Hex(visible) == afterDigest {
				if _, commitErr := runtime.spool.Commit(mutationID); commitErr != nil {
					runtime.healthErr = fmt.Errorf("ambiguous legacy publication resolved to after-state but canonical commit failed: %w", commitErr)
					return runtime.healthErr
				}
				runtime.markDirtyLocked()
				return err
			}
			if (errors.Is(visibleErr, os.ErrNotExist) && beforeDigest == "") || (visibleErr == nil && sha256Hex(visible) == beforeDigest) {
				_, _ = runtime.spool.Abort(mutationID)
				return err
			}
			// Neither generation is provable. Leave the prepare pending so boot
			// recovery freezes this family instead of inventing an outcome.
			runtime.healthErr = fmt.Errorf("ambiguous legacy publication has unrecognized visible state: %w", err)
			return err
		}
		if _, abortErr := runtime.spool.Abort(mutationID); abortErr != nil {
			runtime.healthErr = fmt.Errorf("legacy mutation failed (%v), canonical abort failed: %w", err, abortErr)
		}
		return err
	}
	if _, err := runtime.spool.Commit(mutationID); err != nil {
		// A durable local commit marker is mandatory in both shadow and required
		// modes; never report success after losing the capture chain.
		runtime.healthErr = fmt.Errorf("legacy mutation committed but canonical commit failed: %w", err)
		return runtime.healthErr
	}
	runtime.markDirtyLocked()
	runtime.markSuccessLocked()
	return nil
}

func (runtime *CanonicalRuntime) prepareFailureOrShadow(err error, mutate func() error) error {
	runtime.healthErr = err
	// Shadow remains available for reads, but once enabled its durable local
	// capture is mandatory for covered writes. Reject rather than silently
	// mutate outside the chain.
	return err
}

func (runtime *CanonicalRuntime) appendMutationEvent(ctx context.Context, family, objectKey, digest string) error {
	const mutationAggregate = "legacy_file_mutation"
	version, _, err := runtime.versions.ResolveVersionDurably(ctx, mutationAggregate, objectKey, digest)
	if err != nil {
		return err
	}
	eventID, err := CanonicalImportEventID(runtime.tenantID, mutationAggregate, objectKey, canonicalLegacyImportEventType, digest)
	if err != nil {
		return err
	}
	payload, payloadDigest, err := NewCanonicalEventPayload(runtime.registry, canonicalLegacyImportEventType, 1, map[string]any{
		"object_id": sha256Hex([]byte(objectKey)), "source_kind": family, "source_revision": version,
		"room_id": officeRoomID, "status": "active", "deleted": false, "payload_sha256": digest,
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = runtime.events.Append(ctx, CanonicalEvent{
		EventID: eventID, TenantID: runtime.tenantID, AggregateType: mutationAggregate, AggregateID: objectKey,
		AggregateVersion: version, EventType: canonicalLegacyImportEventType, SchemaVersion: 1,
		OccurredAt: now, RecordedAt: now, Actor: CanonicalPrincipalRef{Kind: "service", ID: "canonical-runtime"},
		RoomID: officeRoomID, IdempotencyKey: "runtime/" + eventID.String(), Classification: "internal",
		ACLVersion: 1, Payload: payload, PayloadSHA256: payloadDigest,
	})
	return err
}

func (runtime *CanonicalRuntime) spoolHighWater() uint64 {
	if runtime == nil || runtime.spool == nil {
		return 0
	}
	runtime.spool.mu.Lock()
	defer runtime.spool.mu.Unlock()
	if runtime.spool.next == 0 {
		return 0
	}
	return runtime.spool.next - 1
}

func (runtime *CanonicalRuntime) markDirtyLocked() {
	runtime.dirtyHighWater = runtime.spoolHighWater()
	if runtime.reconcileSignal != nil {
		select {
		case runtime.reconcileSignal <- struct{}{}:
		default:
		}
	}
}

func (runtime *CanonicalRuntime) startReconcileLoop() {
	if runtime.reconcileStop != nil {
		return
	}
	runtime.reconcileSignal = make(chan struct{}, 1)
	runtime.reconcileStop = make(chan struct{})
	runtime.reconcileCtx, runtime.reconcileCancel = context.WithCancel(context.Background())
	runtime.reconcileWG.Add(1)
	go runtime.reconcileLoop()
}

func (runtime *CanonicalRuntime) reconcileLoop() {
	defer runtime.reconcileWG.Done()
	retry := 0
	for {
		if retry == 0 {
			select {
			case <-runtime.reconcileStop:
				return
			case <-runtime.reconcileCtx.Done():
				return
			case <-runtime.reconcileSignal:
			}
		}
		delay := 250 * time.Millisecond
		if retry > 0 {
			shift := retry - 1
			if shift > 7 {
				shift = 7
			}
			delay = time.Duration(1<<shift) * 250 * time.Millisecond
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			// Deterministic bounded jitter avoids synchronized replicas without a
			// shared/global PRNG or nondeterministic tests.
			delay += time.Duration((retry*7919)%101) * delay / 1000
		}
		timer := time.NewTimer(delay)
		select {
		case <-runtime.reconcileStop:
			timer.Stop()
			return
		case <-runtime.reconcileCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := runtime.Reconcile(runtime.reconcileCtx); err != nil {
			if errors.Is(err, context.Canceled) || runtime.reconcileCtx.Err() != nil {
				return
			}
			retry++
			continue
		}
		retry = 0
	}
}

type canonicalReconcileCheckpoint struct {
	Format       int       `json:"format"`
	TenantID     string    `json:"tenant_id"`
	HighWater    uint64    `json:"high_water"`
	SpoolSHA256  string    `json:"spool_sha256"`
	SourceSHA256 string    `json:"source_sha256"`
	ReconciledAt time.Time `json:"reconciled_at"`
}

func (runtime *CanonicalRuntime) checkpointPath() string {
	return filepath.Join(runtime.root, "reconcile-checkpoint.json")
}

func (runtime *CanonicalRuntime) spoolDigestThrough(highWater uint64) (string, error) {
	facts := runtime.spool.CommittedFacts()
	bounded := make([]CanonicalCapturedFact, 0, len(facts))
	for _, fact := range facts {
		if fact.Commit.Sequence <= highWater {
			bounded = append(bounded, fact)
		}
	}
	encoded, err := canonicalJSON(bounded)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded), nil
}

func (runtime *CanonicalRuntime) loadReconcileCheckpoint() (bool, error) {
	runtime.checkpointValid = false
	runtime.checkpointHighWater = 0
	runtime.checkpointSourceSHA = ""
	raw, err := os.ReadFile(runtime.checkpointPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var checkpoint canonicalReconcileCheckpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return false, nil // corruption forces full reconcile
	}
	if err := ensureJSONEOF(decoder); err != nil || checkpoint.Format != 1 || checkpoint.TenantID != runtime.tenantID || checkpoint.HighWater > runtime.spoolHighWater() || !isHexDigest(checkpoint.SourceSHA256) {
		return false, nil
	}
	digest, err := runtime.spoolDigestThrough(checkpoint.HighWater)
	if err != nil {
		return false, err
	}
	if digest != checkpoint.SpoolSHA256 {
		return false, nil
	}
	runtime.checkpointValid = true
	runtime.checkpointHighWater = checkpoint.HighWater
	runtime.checkpointSourceSHA = checkpoint.SourceSHA256
	runtime.reconciledHighWater = checkpoint.HighWater
	return true, nil
}

func canonicalPlanSourceDigest(plan CanonicalImportPlan) (string, error) {
	source := BuildCanonicalParitySnapshot(plan.Objects)
	encoded, err := canonicalJSON(source)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded), nil
}

func (runtime *CanonicalRuntime) parityOptions(plan CanonicalImportPlan) CanonicalReconcileOptions {
	acl := CanonicalParityACLResolver(planParityACL(plan))
	if runtime.postgres != nil {
		acl = NewPostgresCanonicalParityACL(runtime.postgres, runtime.tenantID)
	}
	return CanonicalReconcileOptions{ACL: acl, TestedPrincipals: plan.TestedPrincipals}
}

func (runtime *CanonicalRuntime) tryResumeCheckpoint(ctx context.Context, plan CanonicalImportPlan) (bool, error) {
	if !runtime.checkpointValid || runtime.events == nil || runtime.checkpointHighWater != runtime.spoolHighWater() {
		return false, nil
	}
	sourceDigest, err := canonicalPlanSourceDigest(plan)
	if err != nil {
		return false, err
	}
	if sourceDigest != runtime.checkpointSourceSHA {
		runtime.checkpointValid = false
		return false, nil
	}
	report, err := ReconcileCanonicalPlanWithOptions(ctx, plan, canonicalLegacyEventView{CanonicalEventStore: runtime.events}, runtime.parityOptions(plan))
	if err != nil || report.Diverged {
		runtime.checkpointValid = false
		return false, err
	}
	if runtime.postgres != nil {
		if err := runtime.drainCanonicalImportOutbox(ctx); err != nil {
			return false, err
		}
	}
	runtime.dirtyHighWater = runtime.checkpointHighWater
	runtime.reconciledHighWater = runtime.checkpointHighWater
	runtime.markSuccess()
	return true, nil
}

// Reconcile applies the deterministic per-object importer identities at a
// stable mutation high-water. Failure leaves dirtyHighWater unchanged and is
// therefore retryable and visible in readiness.
func (runtime *CanonicalRuntime) Reconcile(ctx context.Context) error {
	if runtime == nil || runtime.events == nil {
		return ErrCanonicalStoreUnhealthy
	}
	// Only one full import may publish checkpoint and health state at a time.
	// This is intentionally separate from mu so snapshots and covered writes
	// remain available throughout the potentially expensive scan and apply.
	runtime.reconcileMu.Lock()
	defer runtime.reconcileMu.Unlock()
	runtime.mu.Lock()
	target := runtime.dirtyHighWater
	runtime.mu.Unlock()
	// Reading the committed set validates the complete local chain through the
	// target high-water before reconstructing logical object identities.
	for _, fact := range runtime.spool.CommittedFacts() {
		if fact.Commit.Sequence <= target && fact.Prepare.AfterStateDigest == "" {
			return errors.New("canonical committed fact is missing an after-state digest")
		}
	}
	plan, err := runtime.buildLegacyPlan(ctx)
	if err == nil {
		err = plan.Apply(ctx, runtime.events)
	}
	if err == nil && runtime.postgres != nil {
		err = runtime.postgres.SyncImportGrants(ctx, plan)
	}
	var report CanonicalReconcileReport
	if err == nil {
		report, err = ReconcileCanonicalPlanWithOptions(ctx, plan, canonicalLegacyEventView{CanonicalEventStore: runtime.events}, runtime.parityOptions(plan))
		if err == nil && report.Diverged {
			err = fmt.Errorf("canonical parity diverged with %d repair candidates", len(report.Candidates))
		}
	}
	if err != nil {
		failure := fmt.Errorf("canonical reconcile failed at high-water %d: %w", target, err)
		runtime.markFailure(failure)
		return failure
	}
	if runtime.postgres != nil {
		if err := runtime.drainCanonicalImportOutbox(ctx); err != nil {
			failure := fmt.Errorf("canonical outbox drain failed at high-water %d: %w", target, err)
			runtime.markFailure(failure)
			return failure
		}
	}
	spoolDigest, digestErr := runtime.spoolDigestThrough(target)
	if digestErr != nil {
		return digestErr
	}
	sourceDigest, digestErr := canonicalPlanSourceDigest(plan)
	if digestErr != nil {
		return digestErr
	}
	checkpoint := canonicalReconcileCheckpoint{Format: 1, TenantID: runtime.tenantID, HighWater: target, SpoolSHA256: spoolDigest, SourceSHA256: sourceDigest, ReconciledAt: time.Now().UTC()}
	encoded, encodeErr := json.Marshal(checkpoint)
	if encodeErr != nil {
		return encodeErr
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.dirtyHighWater != target {
		// The import may have safely observed newer state, but its checkpoint
		// cannot claim a spool high-water that changed underneath it. A queued
		// signal will reconcile the newer stable boundary without ever blocking
		// covered legacy writes for the duration of this full scan.
		runtime.markSuccessLocked()
		return nil
	}
	if err := writeFileAtomicallyDurable(runtime.checkpointPath(), encoded, 0o600); err != nil {
		runtime.healthErr = fmt.Errorf("persist canonical reconcile checkpoint: %w", err)
		return runtime.healthErr
	}
	runtime.reconciledHighWater = target
	runtime.checkpointValid = true
	runtime.checkpointHighWater = target
	runtime.checkpointSourceSHA = sourceDigest
	runtime.markSuccessLocked()
	return nil
}

func (runtime *CanonicalRuntime) drainCanonicalImportOutbox(ctx context.Context) error {
	if runtime == nil || runtime.postgres == nil || runtime.postgres.pool == nil {
		return ErrCanonicalStoreUnhealthy
	}
	const batchSize = 100
	for batches := 0; batches < 100; batches++ {
		tx, err := runtime.postgres.pool.Begin(ctx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT o.outbox_id,e.sequence,e.tenant_id,e.aggregate_type,e.aggregate_id,e.aggregate_version
			FROM outbox o JOIN canonical_events e ON e.event_id=o.event_id
			WHERE o.topic=$1 AND o.delivered_at IS NULL AND o.available_at<=now()
			AND (o.leased_until IS NULL OR o.leased_until<now())
			ORDER BY o.outbox_id LIMIT $2 FOR UPDATE OF o SKIP LOCKED`, canonicalLegacyImportEventType, batchSize)
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		type item struct {
			id, sequence, version  int64
			tenant, family, object string
		}
		var items []item
		for rows.Next() {
			var candidate item
			if err := rows.Scan(&candidate.id, &candidate.sequence, &candidate.tenant, &candidate.family, &candidate.object, &candidate.version); err != nil {
				rows.Close()
				_ = tx.Rollback(ctx)
				return err
			}
			items = append(items, candidate)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			_ = tx.Rollback(ctx)
			return err
		}
		rows.Close()
		for _, candidate := range items {
			if _, err := tx.Exec(ctx, `UPDATE outbox SET leased_by='canonical-local',leased_until=now()+interval '30 seconds',attempts=attempts+1 WHERE outbox_id=$1`, candidate.id); err != nil {
				_ = tx.Rollback(ctx)
				return err
			}
			var stateRevision, lastSequence int64
			err := tx.QueryRow(ctx, `SELECT state_revision,last_event_sequence FROM objects
				WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`, candidate.tenant, candidate.family, candidate.object).Scan(&stateRevision, &lastSequence)
			if err != nil || stateRevision < candidate.version || lastSequence < candidate.sequence {
				if _, updateErr := tx.Exec(ctx, `UPDATE outbox SET leased_by=NULL,leased_until=NULL,available_at=now()+interval '30 seconds',last_error_code='projection_mismatch' WHERE outbox_id=$1`, candidate.id); updateErr != nil {
					_ = tx.Rollback(ctx)
					return updateErr
				}
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE outbox SET delivered_at=now(),leased_by=NULL,leased_until=NULL,last_error_code=NULL WHERE outbox_id=$1`, candidate.id); err != nil {
				_ = tx.Rollback(ctx)
				return err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		if len(items) < batchSize {
			break
		}
	}
	var pending, failed int64
	var oldest float64
	err := runtime.postgres.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE last_error_code IS NOT NULL),
		COALESCE(EXTRACT(EPOCH FROM (now()-min(available_at))),0)
		FROM outbox WHERE topic=$1 AND delivered_at IS NULL`, canonicalLegacyImportEventType).Scan(&pending, &failed, &oldest)
	if err != nil {
		return err
	}
	outboxOldestSeconds := int64(0)
	if oldest > 0 {
		outboxOldestSeconds = int64(oldest)
	}
	runtime.mu.Lock()
	runtime.outboxPending = pending
	runtime.outboxFailed = failed
	runtime.outboxOldestSeconds = outboxOldestSeconds
	runtime.outboxKnown = true
	runtime.mu.Unlock()
	if pending > 0 {
		return fmt.Errorf("canonical import outbox still has %d pending rows (%d failed)", pending, failed)
	}
	return nil
}

type canonicalLegacyEventView struct{ CanonicalEventStore }

func (view canonicalLegacyEventView) Events(ctx context.Context) ([]CanonicalEvent, error) {
	events, err := view.CanonicalEventStore.Events(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, family := range canonicalLegacyFamilies {
		allowed[family] = true
	}
	filtered := events[:0]
	for _, event := range events {
		if allowed[event.AggregateType] && event.EventType == canonicalLegacyImportEventType {
			filtered = append(filtered, event)
		}
	}
	return filtered, nil
}

type planParityACL CanonicalImportPlan

func (plan planParityACL) CanReadCanonicalObject(_ context.Context, principal string, event CanonicalEvent) (bool, error) {
	for _, object := range plan.Objects {
		if object.Family == event.AggregateType && object.ObjectID == event.AggregateID {
			for _, allowed := range object.Principals {
				if allowed == principal {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, nil
}

func (runtime *CanonicalRuntime) resolveLegacyState(_ string, objectKey string) (string, bool, error) {
	raw, err := os.ReadFile(objectKey)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sha256Hex(raw), true, nil
}

func (runtime *CanonicalRuntime) ownsPath(path string) bool {
	if runtime == nil || runtime.root == "" {
		return false
	}
	rel, err := filepath.Rel(runtime.root, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (runtime *CanonicalRuntime) familyForPath(path string) (string, bool) {
	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	switch base {
	case filepath.Base(meetingMemoryPath()):
		return "memory", true
	case filepath.Base(kanbanBoardPath()):
		return "board_card", true
	case filepath.Base(roomsFilePath()):
		return "room", true
	case filepath.Base(meetingsPath()):
		return "meeting", true
	case filepath.Base(notificationsPath()):
		return "notification", true
	case filepath.Base(shareLinksPath()):
		return "share_link", true
	case filepath.Base(fileFoldersFilePath()):
		return "file_folder", true
	}
	for _, queue := range []string{codexRunnerQueuePath(), renderRunnerQueuePath()} {
		if rel, err := filepath.Rel(queue, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "queue_job", true
		}
	}
	for dir, family := range map[string]string{
		filepath.Join(runtime.dataDir, "archives"): "archive",
		filepath.Join(runtime.dataDir, "blobs"):    "blob",
	} {
		if rel, err := filepath.Rel(dir, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return family, true
		}
	}
	switch base {
	case "deleted-objects.jsonl":
		return "tombstone", true
	case "evicted-objects.jsonl":
		return "eviction", true
	}
	return "", false
}

func canonicalRuntimeCoverage() (covered, uncovered []string) {
	covered = []string{
		"memory", "artifact_revision", "board_card", "room", "guest_link", "meeting", "notification",
		"share_link", "file_folder", "file_assignment", "queue_job", "archive", "blob", "tombstone", "eviction",
	}
	known := make(map[string]bool, len(covered))
	for _, family := range covered {
		known[family] = true
	}
	for _, family := range canonicalLegacyFamilies {
		if !known[family] {
			uncovered = append(uncovered, family)
		}
	}
	sort.Strings(covered)
	sort.Strings(uncovered)
	return covered, uncovered
}

func (runtime *CanonicalRuntime) failOrDegrade(err error) error {
	// Callers hold runtime.mu so a family mutation is serialized from prepare
	// through its health transition.
	runtime.healthErr = err
	if runtime.mode == CanonicalModeRequired {
		return err
	}
	return nil
}

func (runtime *CanonicalRuntime) markFailure(err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.healthErr = err
}

func (runtime *CanonicalRuntime) markSuccess() {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.markSuccessLocked()
}

func (runtime *CanonicalRuntime) markSuccessLocked() {
	runtime.healthErr = nil
	runtime.lastOK = time.Now().UTC()
}

func canonicalRuntimeSnapshot() CanonicalRuntimeSnapshot {
	runtime := currentCanonicalRuntime()
	if runtime == nil {
		return CanonicalRuntimeSnapshot{Mode: "uninitialized", Healthy: false}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	snapshot := CanonicalRuntimeSnapshot{Mode: string(runtime.mode), Required: runtime.mode == CanonicalModeRequired, Database: runtime.postgres != nil}
	if runtime.mode == CanonicalModeOff {
		snapshot.Healthy = true
		return snapshot
	}
	if runtime.spool != nil {
		runtime.spool.mu.Lock()
		if runtime.spool.next > 0 {
			snapshot.HighWater = runtime.spool.next - 1
		}
		for mutation := range runtime.spool.byMutation {
			_, _, terminal := runtime.spool.mutationState(mutation)
			if !terminal {
				snapshot.Pending++
			}
		}
		for family, frozen := range runtime.spool.frozen {
			if frozen {
				snapshot.FrozenFamilies = append(snapshot.FrozenFamilies, family)
			}
		}
		runtime.spool.mu.Unlock()
	}
	snapshot.DirtyHighWater = runtime.dirtyHighWater
	snapshot.ReconciledHighWater = runtime.reconciledHighWater
	snapshot.OutboxKnown = runtime.outboxKnown
	snapshot.OutboxPending = runtime.outboxPending
	snapshot.OutboxFailed = runtime.outboxFailed
	snapshot.OutboxOldestSeconds = runtime.outboxOldestSeconds
	snapshot.CheckpointValid = runtime.checkpointValid
	snapshot.CheckpointHighWater = runtime.checkpointHighWater
	sort.Strings(snapshot.FrozenFamilies)
	snapshot.CoveredFamilies, snapshot.UncoveredFamilies = canonicalRuntimeCoverage()
	outboxHealthy := runtime.postgres == nil || (snapshot.OutboxKnown && snapshot.OutboxPending == 0 && snapshot.OutboxFailed == 0)
	checkpointHealthy := runtime.postgres == nil || (snapshot.CheckpointValid && snapshot.CheckpointHighWater == snapshot.ReconciledHighWater)
	snapshot.Healthy = runtime.healthErr == nil && snapshot.Pending == 0 && len(snapshot.FrozenFamilies) == 0 && len(snapshot.UncoveredFamilies) == 0 && runtime.events != nil && snapshot.DirtyHighWater == snapshot.ReconciledHighWater && outboxHealthy && checkpointHealthy
	if !runtime.lastOK.IsZero() {
		snapshot.LastSuccess = runtime.lastOK.Format(time.RFC3339Nano)
	}
	if runtime.healthErr != nil {
		snapshot.Error = runtime.healthErr.Error()
	} else if runtime.postgres == nil {
		snapshot.Error = "canonical PostgreSQL is not configured"
	}
	return snapshot
}

func sha256Hex(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
