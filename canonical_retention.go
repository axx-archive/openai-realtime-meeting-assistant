package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const RawAudioRetentionTTL = 72 * time.Hour

type RetentionResourceClass string

const (
	RetentionRevisionBody RetentionResourceClass = "revision_body"
	RetentionBlob         RetentionResourceClass = "blob"
	RetentionEmbedding    RetentionResourceClass = "embedding"
	RetentionDigest       RetentionResourceClass = "digest"
	RetentionExcerpt      RetentionResourceClass = "excerpt"
	RetentionCache        RetentionResourceClass = "cache"
	RetentionExport       RetentionResourceClass = "export"
	RetentionBackup       RetentionResourceClass = "backup"
	RetentionTombstone    RetentionResourceClass = "tombstone"
)

var mandatoryPurgeClasses = []RetentionResourceClass{
	RetentionRevisionBody, RetentionBlob, RetentionEmbedding, RetentionDigest,
	RetentionExcerpt, RetentionCache, RetentionExport, RetentionBackup,
}

type RetentionState string

const (
	RetentionActive       RetentionState = "active"
	RetentionSoftDeleted  RetentionState = "soft_deleted"
	RetentionPurgePlanned RetentionState = "purge_planned"
	RetentionPurging      RetentionState = "purging"
	RetentionPurged       RetentionState = "purged"
)

var (
	ErrRetentionInvalid             = errors.New("invalid retention record")
	ErrRetentionNotFound            = errors.New("retention record not found")
	ErrRetentionReadDenied          = errors.New("retained content is not readable")
	ErrRetentionLegalHold           = errors.New("legal hold blocks purge")
	ErrRetentionIncomplete          = errors.New("purge is incomplete")
	ErrRetentionRestoreGate         = errors.New("purge ledger restore gate unavailable")
	ErrRetentionRestoreResurrection = errors.New("restore would resurrect purged content")
)

type RetentionKey struct {
	TenantID   string
	ObjectID   string
	RevisionID string
}

// RetentionHeader is immutable audit metadata. It contains no body pointer or
// user-authored strings, so erasing RetentionBody never requires rewriting the
// historical header.
type RetentionHeader struct {
	Key                 RetentionKey
	EventType           string
	OccurredAt          time.Time
	RecordedAt          time.Time
	ActorPseudonymousID string
	ContentDigest       string
	Classification      string
}

type RetentionBody struct {
	BodyID      string
	Key         RetentionKey
	Kind        string
	CreatedAt   time.Time
	RetainUntil *time.Time
	References  map[RetentionResourceClass][]string
	Bytes       []byte
}

type RetentionPurgeTask struct {
	Class               RetentionResourceClass
	References          []string
	Completed           bool
	DestructionEvidence string
}

type RetentionPurgePlan struct {
	ID        string
	Key       RetentionKey
	PolicyID  string
	CreatedAt time.Time
	Tasks     []RetentionPurgeTask
	Tombstone map[string]string
}

type RetentionRecord struct {
	Header        RetentionHeader
	State         RetentionState
	SoftDeletedAt *time.Time
	LegalHold     bool
	LegalHoldID   string
	Plan          *RetentionPurgePlan
	BodyPresent   bool
}

type RetentionStore interface {
	Register(context.Context, RetentionHeader, RetentionBody) (existing bool, err error)
	ReadBody(context.Context, RetentionKey) (RetentionBody, error)
	SoftDelete(context.Context, RetentionKey, time.Time) (RetentionRecord, error)
	SetLegalHold(context.Context, RetentionKey, string, bool) (RetentionRecord, error)
	PlanPurge(context.Context, RetentionKey, string, time.Time) (RetentionPurgePlan, error)
	BeginPurge(context.Context, RetentionKey) (RetentionRecord, error)
	CompletePurgeTarget(context.Context, RetentionKey, RetentionResourceClass, string) (RetentionRecord, error)
	FinalizePurge(context.Context, RetentionKey) (RetentionRecord, error)
	Record(context.Context, RetentionKey) (RetentionRecord, error)
}

type MemoryRetentionStore struct {
	mu      sync.Mutex
	records map[RetentionKey]*memoryRetentionRecord
	ledger  PurgeLedger
}

type memoryRetentionRecord struct {
	header        RetentionHeader
	body          *RetentionBody
	state         RetentionState
	softDeletedAt *time.Time
	legalHold     bool
	legalHoldID   string
	plan          *RetentionPurgePlan
}

func NewMemoryRetentionStore(ledger PurgeLedger) *MemoryRetentionStore {
	return &MemoryRetentionStore{records: make(map[RetentionKey]*memoryRetentionRecord), ledger: ledger}
}

func (store *MemoryRetentionStore) Register(_ context.Context, header RetentionHeader, body RetentionBody) (bool, error) {
	if err := validateRetentionPair(header, body); err != nil {
		return false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.records[header.Key]; ok {
		if equalRetentionHeader(existing.header, header) && existing.body != nil && equalRetentionBody(*existing.body, body) {
			return true, nil
		}
		return false, ErrRetentionInvalid
	}
	copyBody := cloneRetentionBody(body)
	store.records[header.Key] = &memoryRetentionRecord{header: header, body: &copyBody, state: RetentionActive}
	return false, nil
}

func (store *MemoryRetentionStore) ReadBody(_ context.Context, key RetentionKey) (RetentionBody, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[key]
	if !ok {
		return RetentionBody{}, ErrRetentionNotFound
	}
	if record.state != RetentionActive || record.body == nil {
		return RetentionBody{}, ErrRetentionReadDenied
	}
	return cloneRetentionBody(*record.body), nil
}

func (store *MemoryRetentionStore) SoftDelete(_ context.Context, key RetentionKey, at time.Time) (RetentionRecord, error) {
	if at.IsZero() {
		return RetentionRecord{}, ErrRetentionInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[key]
	if !ok {
		return RetentionRecord{}, ErrRetentionNotFound
	}
	if record.softDeletedAt == nil {
		stamp := at.UTC()
		record.softDeletedAt = &stamp
		if record.state == RetentionActive {
			record.state = RetentionSoftDeleted
		}
	}
	return snapshotRetentionRecord(record), nil
}

func (store *MemoryRetentionStore) SetLegalHold(_ context.Context, key RetentionKey, holdID string, active bool) (RetentionRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[key]
	if !ok {
		return RetentionRecord{}, ErrRetentionNotFound
	}
	if record.state == RetentionPurged {
		return snapshotRetentionRecord(record), nil
	}
	if active && strings.TrimSpace(holdID) == "" {
		return RetentionRecord{}, ErrRetentionInvalid
	}
	record.legalHold = active
	if active {
		record.legalHoldID = strings.TrimSpace(holdID)
	} else {
		record.legalHoldID = ""
	}
	return snapshotRetentionRecord(record), nil
}

func (store *MemoryRetentionStore) PlanPurge(_ context.Context, key RetentionKey, policyID string, at time.Time) (RetentionPurgePlan, error) {
	if !safeMachineToken(policyID, 128) || at.IsZero() {
		return RetentionPurgePlan{}, ErrRetentionInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[key]
	if !ok {
		return RetentionPurgePlan{}, ErrRetentionNotFound
	}
	if record.legalHold {
		return RetentionPurgePlan{}, ErrRetentionLegalHold
	}
	if record.plan != nil {
		if record.plan.PolicyID != strings.TrimSpace(policyID) {
			return RetentionPurgePlan{}, ErrRetentionInvalid
		}
		return cloneRetentionPlan(*record.plan), nil
	}
	if record.state == RetentionPurged || record.body == nil {
		return RetentionPurgePlan{}, ErrRetentionInvalid
	}
	if record.softDeletedAt == nil {
		stamp := at.UTC()
		record.softDeletedAt = &stamp
	}
	plan := buildRetentionPurgePlan(record.header, *record.body, strings.TrimSpace(policyID), at.UTC())
	record.plan = &plan
	record.state = RetentionPurgePlanned
	return cloneRetentionPlan(plan), nil
}

func (store *MemoryRetentionStore) BeginPurge(_ context.Context, key RetentionKey) (RetentionRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[key]
	if !ok {
		return RetentionRecord{}, ErrRetentionNotFound
	}
	if record.legalHold {
		return RetentionRecord{}, ErrRetentionLegalHold
	}
	if record.state == RetentionPurged || record.state == RetentionPurging {
		return snapshotRetentionRecord(record), nil
	}
	if record.plan == nil || record.state != RetentionPurgePlanned {
		return RetentionRecord{}, ErrRetentionInvalid
	}
	record.state = RetentionPurging
	return snapshotRetentionRecord(record), nil
}

func (store *MemoryRetentionStore) CompletePurgeTarget(_ context.Context, key RetentionKey, class RetentionResourceClass, evidence string) (RetentionRecord, error) {
	if !mandatoryPurgeClass(class) || !validDestructionEvidence(evidence) {
		return RetentionRecord{}, ErrRetentionInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[key]
	if !ok {
		return RetentionRecord{}, ErrRetentionNotFound
	}
	if record.legalHold {
		return RetentionRecord{}, ErrRetentionLegalHold
	}
	if record.state == RetentionPurged {
		return snapshotRetentionRecord(record), nil
	}
	if record.plan == nil || record.state != RetentionPurging {
		return RetentionRecord{}, ErrRetentionInvalid
	}
	for index := range record.plan.Tasks {
		if record.plan.Tasks[index].Class == class {
			if record.plan.Tasks[index].Completed {
				if record.plan.Tasks[index].DestructionEvidence != strings.TrimSpace(evidence) {
					return RetentionRecord{}, ErrRetentionInvalid
				}
			} else {
				record.plan.Tasks[index].Completed = true
				record.plan.Tasks[index].DestructionEvidence = strings.TrimSpace(evidence)
				// Storage keys and filenames are erasable data too. Once their
				// target class is destroyed, retain only the safe proof code.
				record.plan.Tasks[index].References = nil
			}
			return snapshotRetentionRecord(record), nil
		}
	}
	return RetentionRecord{}, ErrRetentionInvalid
}

func (store *MemoryRetentionStore) FinalizePurge(ctx context.Context, key RetentionKey) (RetentionRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[key]
	if !ok {
		return RetentionRecord{}, ErrRetentionNotFound
	}
	if record.state == RetentionPurged {
		return snapshotRetentionRecord(record), nil
	}
	if record.legalHold {
		return RetentionRecord{}, ErrRetentionLegalHold
	}
	if record.plan == nil || record.state != RetentionPurging || !purgePlanComplete(*record.plan) {
		return RetentionRecord{}, ErrRetentionIncomplete
	}
	if store.ledger == nil {
		return RetentionRecord{}, ErrRetentionRestoreGate
	}
	record.plan.Tombstone["destruction_evidence"] = destructionEvidenceSummary(record.plan.Tasks)
	entry := purgeLedgerEntry(record.header, *record.plan)
	if err := store.ledger.RecordPurge(ctx, entry); err != nil {
		return RetentionRecord{}, fmt.Errorf("record purge ledger: %w", err)
	}
	record.body = nil
	record.state = RetentionPurged
	return snapshotRetentionRecord(record), nil
}

func (store *MemoryRetentionStore) Record(_ context.Context, key RetentionKey) (RetentionRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[key]
	if !ok {
		return RetentionRecord{}, ErrRetentionNotFound
	}
	return snapshotRetentionRecord(record), nil
}

func RawAudioRetainUntil(createdAt time.Time) time.Time {
	return createdAt.UTC().Add(RawAudioRetentionTTL)
}

func RawAudioPurgeDue(body RetentionBody, now time.Time) bool {
	return body.Kind == "raw_audio" && body.RetainUntil != nil && !now.UTC().Before(body.RetainUntil.UTC())
}

type PurgeLedgerEntry struct {
	Key                 RetentionKey
	ContentDigest       string
	PolicyID            string
	PurgedAt            time.Time
	DestructionEvidence map[RetentionResourceClass]string
}

type PurgeLedger interface {
	RecordPurge(context.Context, PurgeLedgerEntry) error
	LookupPurge(context.Context, RetentionKey) (PurgeLedgerEntry, bool, error)
}

type MemoryPurgeLedger struct {
	mu      sync.Mutex
	entries map[RetentionKey]PurgeLedgerEntry
}

func NewMemoryPurgeLedger() *MemoryPurgeLedger {
	return &MemoryPurgeLedger{entries: make(map[RetentionKey]PurgeLedgerEntry)}
}

func (ledger *MemoryPurgeLedger) RecordPurge(_ context.Context, entry PurgeLedgerEntry) error {
	if err := validatePurgeLedgerEntry(entry); err != nil {
		return err
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if prior, ok := ledger.entries[entry.Key]; ok {
		if prior.ContentDigest != entry.ContentDigest || prior.PolicyID != entry.PolicyID || !equalDestructionEvidence(prior.DestructionEvidence, entry.DestructionEvidence) {
			return ErrRetentionInvalid
		}
		return nil
	}
	entry.DestructionEvidence = cloneDestructionEvidence(entry.DestructionEvidence)
	ledger.entries[entry.Key] = entry
	return nil
}

func validatePurgeLedgerEntry(entry PurgeLedgerEntry) error {
	if !validRetentionKey(entry.Key) || !isHexDigest(entry.ContentDigest) || !safeMachineToken(entry.PolicyID, 128) || entry.PurgedAt.IsZero() || len(entry.DestructionEvidence) != len(mandatoryPurgeClasses) {
		return ErrRetentionInvalid
	}
	for class, evidence := range entry.DestructionEvidence {
		if !mandatoryPurgeClass(class) || !validDestructionEvidence(evidence) {
			return ErrRetentionInvalid
		}
	}
	return nil
}

func (ledger *MemoryPurgeLedger) LookupPurge(_ context.Context, key RetentionKey) (PurgeLedgerEntry, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.entries[key]
	entry.DestructionEvidence = cloneDestructionEvidence(entry.DestructionEvidence)
	return entry, ok, nil
}

type RestoreCandidate struct {
	Key             RetentionKey
	Class           RetentionResourceClass
	TombstoneFields map[string]string
}

type RestorePurgeLedgerGate struct{ Ledger PurgeLedger }

// Validate must run before restored services become readable. A purge-ledger
// hit rejects every restored derived/content resource; only a tombstone whose
// keys are on the non-sensitive allowlist may survive.
func (gate RestorePurgeLedgerGate) Validate(ctx context.Context, candidates []RestoreCandidate) error {
	if gate.Ledger == nil {
		return ErrRetentionRestoreGate
	}
	for _, candidate := range candidates {
		entry, purged, err := gate.Ledger.LookupPurge(ctx, candidate.Key)
		if err != nil {
			return ErrRetentionRestoreGate
		}
		if !purged {
			continue
		}
		if candidate.Class != RetentionTombstone || !safeRetentionTombstone(candidate.TombstoneFields) ||
			candidate.TombstoneFields["tenant_id"] != candidate.Key.TenantID || candidate.TombstoneFields["object_id"] != candidate.Key.ObjectID ||
			candidate.TombstoneFields["revision_id"] != candidate.Key.RevisionID || candidate.TombstoneFields["content_digest"] != entry.ContentDigest ||
			candidate.TombstoneFields["policy_id"] != entry.PolicyID {
			return ErrRetentionRestoreResurrection
		}
	}
	return nil
}

var retentionTombstoneAllowlist = map[string]struct{}{
	"tenant_id": {}, "object_id": {}, "revision_id": {}, "event_type": {}, "occurred_at": {}, "recorded_at": {},
	"actor_pseudonymous_id": {}, "content_digest": {}, "policy_id": {}, "purge_status": {}, "destruction_evidence": {},
}

func safeRetentionTombstone(fields map[string]string) bool {
	if fields == nil || !safeMachineToken(fields["tenant_id"], 128) || !safeMachineToken(fields["object_id"], 128) || !safeMachineToken(fields["revision_id"], 128) ||
		!safeMachineToken(fields["event_type"], 96) || !canonicalRetentionTimestamp(fields["occurred_at"]) || !canonicalRetentionTimestamp(fields["recorded_at"]) ||
		!validActorPseudonym(fields["actor_pseudonymous_id"]) || !isHexDigest(fields["content_digest"]) ||
		!safeMachineToken(fields["policy_id"], 128) || fields["purge_status"] != string(RetentionPurged) || !validDestructionEvidenceSummary(fields["destruction_evidence"]) {
		return false
	}
	for field := range fields {
		if _, ok := retentionTombstoneAllowlist[field]; !ok {
			return false
		}
	}
	return true
}

func canonicalRetentionTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.UTC().Format(time.RFC3339Nano) == value
}

func validateRetentionPair(header RetentionHeader, body RetentionBody) error {
	if !validRetentionKey(header.Key) || header.Key != body.Key || !safeMachineToken(header.EventType, 96) || header.OccurredAt.IsZero() || header.RecordedAt.IsZero() ||
		!validActorPseudonym(header.ActorPseudonymousID) || !isHexDigest(header.ContentDigest) || !validRetentionClassification(header.Classification) ||
		strings.TrimSpace(body.BodyID) == "" || strings.TrimSpace(body.Kind) == "" || body.CreatedAt.IsZero() {
		return ErrRetentionInvalid
	}
	if body.Kind == "raw_audio" {
		if body.RetainUntil == nil || !body.RetainUntil.UTC().Equal(RawAudioRetainUntil(body.CreatedAt)) {
			return ErrRetentionInvalid
		}
	}
	for class := range body.References {
		if !mandatoryPurgeClass(class) {
			return ErrRetentionInvalid
		}
	}
	return nil
}

func validRetentionKey(key RetentionKey) bool {
	return safeMachineToken(key.TenantID, 128) && safeMachineToken(key.ObjectID, 128) && safeMachineToken(key.RevisionID, 128)
}

func mandatoryPurgeClass(class RetentionResourceClass) bool {
	for _, required := range mandatoryPurgeClasses {
		if class == required {
			return true
		}
	}
	return false
}

func buildRetentionPurgePlan(header RetentionHeader, body RetentionBody, policyID string, at time.Time) RetentionPurgePlan {
	tasks := make([]RetentionPurgeTask, 0, len(mandatoryPurgeClasses))
	for _, class := range mandatoryPurgeClasses {
		references := append([]string(nil), body.References[class]...)
		sort.Strings(references)
		tasks = append(tasks, RetentionPurgeTask{Class: class, References: references})
	}
	return RetentionPurgePlan{
		ID:  fmt.Sprintf("purge:%s:%s:%s:%s", header.Key.TenantID, header.Key.ObjectID, header.Key.RevisionID, policyID),
		Key: header.Key, PolicyID: policyID, CreatedAt: at, Tasks: tasks,
		Tombstone: map[string]string{
			"tenant_id": header.Key.TenantID, "object_id": header.Key.ObjectID, "revision_id": header.Key.RevisionID,
			"event_type": header.EventType, "occurred_at": header.OccurredAt.UTC().Format(time.RFC3339Nano), "recorded_at": header.RecordedAt.UTC().Format(time.RFC3339Nano),
			"actor_pseudonymous_id": header.ActorPseudonymousID, "content_digest": header.ContentDigest, "policy_id": policyID,
			"purge_status": string(RetentionPurged),
		},
	}
}

func purgePlanComplete(plan RetentionPurgePlan) bool {
	if len(plan.Tasks) != len(mandatoryPurgeClasses) {
		return false
	}
	for _, task := range plan.Tasks {
		if !mandatoryPurgeClass(task.Class) || !task.Completed || !validDestructionEvidence(task.DestructionEvidence) || len(task.References) != 0 {
			return false
		}
	}
	return true
}

func purgeLedgerEntry(header RetentionHeader, plan RetentionPurgePlan) PurgeLedgerEntry {
	evidence := make(map[RetentionResourceClass]string, len(plan.Tasks))
	for _, task := range plan.Tasks {
		evidence[task.Class] = task.DestructionEvidence
	}
	return PurgeLedgerEntry{Key: header.Key, ContentDigest: header.ContentDigest, PolicyID: plan.PolicyID, PurgedAt: plan.CreatedAt, DestructionEvidence: evidence}
}

func destructionEvidenceSummary(tasks []RetentionPurgeTask) string {
	parts := make([]string, 0, len(tasks))
	for _, task := range tasks {
		parts = append(parts, string(task.Class)+"="+task.DestructionEvidence)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func validDestructionEvidence(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "sha256:") {
		return isHexDigest(strings.TrimPrefix(value, "sha256:"))
	}
	switch value {
	case "deleted", "invalidated", "not_present", "key_destroyed", "backup_manifest_updated":
		return true
	default:
		return false
	}
}

func validDestructionEvidenceSummary(value string) bool {
	parts := strings.Split(value, ";")
	if len(parts) != len(mandatoryPurgeClasses) {
		return false
	}
	seen := make(map[RetentionResourceClass]struct{}, len(parts))
	for _, part := range parts {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			return false
		}
		class := RetentionResourceClass(pair[0])
		if !mandatoryPurgeClass(class) || !validDestructionEvidence(pair[1]) {
			return false
		}
		seen[class] = struct{}{}
	}
	return len(seen) == len(mandatoryPurgeClasses)
}

func safeMachineToken(value string, max int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("-_.:@/", char) {
			continue
		}
		return false
	}
	return true
}

func validRetentionClassification(value string) bool {
	switch value {
	case "public", "internal", "confidential", "restricted", "raw_audio":
		return true
	default:
		return false
	}
}

func validActorPseudonym(value string) bool {
	const prefix = "hmac-sha256:"
	return strings.HasPrefix(value, prefix) && isHexDigest(strings.TrimPrefix(value, prefix))
}

func snapshotRetentionRecord(record *memoryRetentionRecord) RetentionRecord {
	result := RetentionRecord{Header: record.header, State: record.state, LegalHold: record.legalHold, LegalHoldID: record.legalHoldID, BodyPresent: record.body != nil}
	if record.softDeletedAt != nil {
		stamp := *record.softDeletedAt
		result.SoftDeletedAt = &stamp
	}
	if record.plan != nil {
		plan := cloneRetentionPlan(*record.plan)
		result.Plan = &plan
	}
	return result
}

func cloneRetentionBody(body RetentionBody) RetentionBody {
	body.Bytes = append([]byte(nil), body.Bytes...)
	if body.RetainUntil != nil {
		stamp := *body.RetainUntil
		body.RetainUntil = &stamp
	}
	references := body.References
	body.References = make(map[RetentionResourceClass][]string, len(references))
	for class, refs := range references {
		body.References[class] = append([]string(nil), refs...)
	}
	return body
}

func cloneRetentionPlan(plan RetentionPurgePlan) RetentionPurgePlan {
	plan.Tasks = append([]RetentionPurgeTask(nil), plan.Tasks...)
	for index := range plan.Tasks {
		plan.Tasks[index].References = append([]string(nil), plan.Tasks[index].References...)
	}
	tombstone := plan.Tombstone
	plan.Tombstone = make(map[string]string, len(tombstone))
	for key, value := range tombstone {
		plan.Tombstone[key] = value
	}
	return plan
}

func equalRetentionHeader(left, right RetentionHeader) bool {
	return left.Key == right.Key && left.EventType == right.EventType && left.OccurredAt.Equal(right.OccurredAt) && left.RecordedAt.Equal(right.RecordedAt) &&
		left.ActorPseudonymousID == right.ActorPseudonymousID && left.ContentDigest == right.ContentDigest && left.Classification == right.Classification
}

func equalRetentionBody(left, right RetentionBody) bool {
	if left.BodyID != right.BodyID || left.Key != right.Key || left.Kind != right.Kind || !left.CreatedAt.Equal(right.CreatedAt) || string(left.Bytes) != string(right.Bytes) {
		return false
	}
	if (left.RetainUntil == nil) != (right.RetainUntil == nil) || (left.RetainUntil != nil && !left.RetainUntil.Equal(*right.RetainUntil)) || len(left.References) != len(right.References) {
		return false
	}
	for class, refs := range left.References {
		other := right.References[class]
		if len(refs) != len(other) {
			return false
		}
		for index := range refs {
			if refs[index] != other[index] {
				return false
			}
		}
	}
	return true
}

func cloneDestructionEvidence(source map[RetentionResourceClass]string) map[RetentionResourceClass]string {
	result := make(map[RetentionResourceClass]string, len(source))
	for class, evidence := range source {
		result[class] = evidence
	}
	return result
}

func equalDestructionEvidence(left, right map[RetentionResourceClass]string) bool {
	if len(left) != len(right) {
		return false
	}
	for class, evidence := range left {
		if right[class] != evidence {
			return false
		}
	}
	return true
}
