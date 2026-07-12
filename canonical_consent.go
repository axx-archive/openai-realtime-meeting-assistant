package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ConsentScope string

const (
	ConsentAudioCapture  ConsentScope = "audio_capture"
	ConsentTranscription ConsentScope = "transcription"
	ConsentModelAnalysis ConsentScope = "model_analysis"
	ConsentOrgMemory     ConsentScope = "org_memory"
)

type ConsentDisposition string

const (
	ConsentGranted   ConsentDisposition = "granted"
	ConsentDenied    ConsentDisposition = "denied"
	ConsentWithdrawn ConsentDisposition = "withdrawn"
)

var (
	ErrConsentInvalid  = errors.New("invalid consent record")
	ErrConsentConflict = errors.New("consent record conflict")
)

// ConsentRecord is immutable evidence for one explicit choice. RoomID and
// SittingID are both mandatory: consent in an earlier sitting never follows a
// participant into a later meeting, and a late join has no implied grant.
type ConsentRecord struct {
	ID            string
	TenantID      string
	PrincipalKind ACLPrincipalKind
	PrincipalID   string
	RoomID        string
	SittingID     string
	PolicyVersion string
	Scopes        []ConsentScope
	Disposition   ConsentDisposition
	EvidenceKind  string
	EvidenceRef   string
	RecordedAt    time.Time
}

func (record ConsentRecord) Validate() error {
	if _, err := uuid.Parse(strings.TrimSpace(record.ID)); err != nil {
		return ErrConsentInvalid
	}
	if strings.TrimSpace(record.TenantID) == "" ||
		(record.PrincipalKind != ACLPrincipalUser && record.PrincipalKind != ACLPrincipalGuest) || strings.TrimSpace(record.PrincipalID) == "" ||
		strings.TrimSpace(record.RoomID) == "" || strings.TrimSpace(record.SittingID) == "" ||
		strings.TrimSpace(record.PolicyVersion) == "" || len(record.Scopes) == 0 ||
		strings.TrimSpace(record.EvidenceKind) == "" || strings.TrimSpace(record.EvidenceRef) == "" ||
		record.RecordedAt.IsZero() || !validConsentDisposition(record.Disposition) {
		return ErrConsentInvalid
	}
	seen := make(map[ConsentScope]struct{}, len(record.Scopes))
	for _, scope := range record.Scopes {
		if !validConsentScope(scope) {
			return ErrConsentInvalid
		}
		seen[scope] = struct{}{}
	}
	if len(seen) != len(record.Scopes) {
		return ErrConsentInvalid
	}
	return nil
}

type ConsentQuery struct {
	TenantID      string
	PrincipalKind ACLPrincipalKind
	PrincipalID   string
	RoomID        string
	SittingID     string
	PolicyVersion string
	Scopes        []ConsentScope
}

type ConsentDecision struct {
	Allowed       bool
	MissingScopes []ConsentScope
	RecordIDs     map[ConsentScope]string
}

type ConsentStore interface {
	Append(context.Context, ConsentRecord) (existing bool, err error)
	Effective(context.Context, ConsentQuery) (ConsentDecision, error)
}

type MemoryConsentStore struct {
	mu      sync.Mutex
	records []ConsentRecord
	byID    map[string]int
}

func NewMemoryConsentStore() *MemoryConsentStore {
	return &MemoryConsentStore{byID: make(map[string]int)}
}

func NewConsentRecordID() string { return uuid.New().String() }

func (store *MemoryConsentStore) Append(_ context.Context, record ConsentRecord) (bool, error) {
	parsedID, err := uuid.Parse(strings.TrimSpace(record.ID))
	if err != nil {
		return false, ErrConsentInvalid
	}
	record.ID = parsedID.String()
	if err := record.Validate(); err != nil {
		return false, err
	}
	record = cloneConsentRecord(record)
	store.mu.Lock()
	defer store.mu.Unlock()
	if index, ok := store.byID[record.ID]; ok {
		if !equalConsentRecord(store.records[index], record) {
			return false, ErrConsentConflict
		}
		return true, nil
	}
	store.byID[record.ID] = len(store.records)
	store.records = append(store.records, record)
	return false, nil
}

// Effective folds matching records in append order. The last explicit choice
// for each scope wins; absence, denial, and withdrawal all fail closed.
func (store *MemoryConsentStore) Effective(_ context.Context, query ConsentQuery) (ConsentDecision, error) {
	requested, err := normalizeConsentQuery(query)
	if err != nil {
		return ConsentDecision{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	latest := make(map[ConsentScope]ConsentRecord, len(requested))
	for _, record := range store.records {
		if record.TenantID != query.TenantID || record.PrincipalKind != query.PrincipalKind || record.PrincipalID != query.PrincipalID ||
			record.RoomID != query.RoomID || record.SittingID != query.SittingID || record.PolicyVersion != query.PolicyVersion {
			continue
		}
		for _, scope := range record.Scopes {
			if prior, ok := latest[scope]; !ok || consentRecordNewer(record, prior) {
				latest[scope] = record
			}
		}
	}
	decision := ConsentDecision{Allowed: true, RecordIDs: make(map[ConsentScope]string)}
	for _, scope := range requested {
		record, ok := latest[scope]
		if ok {
			// Preserve the exact denial/withdrawal evidence as well as grant
			// evidence so the caller can explain an immediate track exclusion.
			decision.RecordIDs[scope] = record.ID
		}
		if !ok || record.Disposition != ConsentGranted {
			decision.Allowed = false
			decision.MissingScopes = append(decision.MissingScopes, scope)
			continue
		}
	}
	return decision, nil
}

func consentRecordNewer(candidate, prior ConsentRecord) bool {
	if !candidate.RecordedAt.Equal(prior.RecordedAt) {
		return candidate.RecordedAt.After(prior.RecordedAt)
	}
	// Imported records may share provider timestamp precision. Denial wins an
	// exact-time tie; the record ID then makes equivalent choices deterministic.
	priority := func(disposition ConsentDisposition) int {
		switch disposition {
		case ConsentWithdrawn:
			return 3
		case ConsentDenied:
			return 2
		default:
			return 1
		}
	}
	if priority(candidate.Disposition) != priority(prior.Disposition) {
		return priority(candidate.Disposition) > priority(prior.Disposition)
	}
	return candidate.ID > prior.ID
}

// CanonicalConsentChecker adapts the consent log to AuthorizationKernel. The
// caller pins one policy version; an unavailable or malformed configuration
// returns an error and AuthorizationKernel denies.
type CanonicalConsentChecker struct {
	Store         ConsentStore
	PolicyVersion string
}

func (checker CanonicalConsentChecker) HasConsent(ctx context.Context, principal ACLPrincipal, object ACLObject, rawScope string) (bool, error) {
	if checker.Store == nil || strings.TrimSpace(checker.PolicyVersion) == "" {
		return false, errors.New("consent checker is not configured")
	}
	scope := ConsentScope(strings.TrimSpace(rawScope))
	if !validConsentScope(scope) {
		return false, ErrConsentInvalid
	}
	decision, err := checker.Store.Effective(ctx, ConsentQuery{
		TenantID: principal.TenantID, PrincipalKind: principal.Kind, PrincipalID: principal.ID,
		RoomID: object.RoomID, SittingID: object.SittingID, PolicyVersion: checker.PolicyVersion,
		Scopes: []ConsentScope{scope},
	})
	return decision.Allowed, err
}

func normalizeConsentQuery(query ConsentQuery) ([]ConsentScope, error) {
	if strings.TrimSpace(query.TenantID) == "" || (query.PrincipalKind != ACLPrincipalUser && query.PrincipalKind != ACLPrincipalGuest) || strings.TrimSpace(query.PrincipalID) == "" ||
		strings.TrimSpace(query.RoomID) == "" || strings.TrimSpace(query.SittingID) == "" || strings.TrimSpace(query.PolicyVersion) == "" || len(query.Scopes) == 0 {
		return nil, ErrConsentInvalid
	}
	seen := make(map[ConsentScope]struct{}, len(query.Scopes))
	for _, scope := range query.Scopes {
		if !validConsentScope(scope) {
			return nil, ErrConsentInvalid
		}
		seen[scope] = struct{}{}
	}
	result := make([]ConsentScope, 0, len(seen))
	for scope := range seen {
		result = append(result, scope)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func validConsentScope(scope ConsentScope) bool {
	switch scope {
	case ConsentAudioCapture, ConsentTranscription, ConsentModelAnalysis, ConsentOrgMemory:
		return true
	default:
		return false
	}
}

func validConsentDisposition(disposition ConsentDisposition) bool {
	return disposition == ConsentGranted || disposition == ConsentDenied || disposition == ConsentWithdrawn
}

func cloneConsentRecord(record ConsentRecord) ConsentRecord {
	record.Scopes = append([]ConsentScope(nil), record.Scopes...)
	return record
}

func equalConsentRecord(left, right ConsentRecord) bool {
	if left.ID != right.ID || left.TenantID != right.TenantID || left.PrincipalKind != right.PrincipalKind || left.PrincipalID != right.PrincipalID ||
		left.RoomID != right.RoomID || left.SittingID != right.SittingID || left.PolicyVersion != right.PolicyVersion ||
		left.Disposition != right.Disposition || left.EvidenceKind != right.EvidenceKind || left.EvidenceRef != right.EvidenceRef || !left.RecordedAt.Equal(right.RecordedAt) ||
		len(left.Scopes) != len(right.Scopes) {
		return false
	}
	for index := range left.Scopes {
		if left.Scopes[index] != right.Scopes[index] {
			return false
		}
	}
	return true
}
