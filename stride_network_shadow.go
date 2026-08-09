package main

// This file is an isolated, provider-free W3 shadow index. It stores only the
// body-minimized publication, attestation, and network projection contracts.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrSTRIDENetworkShadowDisabled    = errors.New("stride network shadow is disabled")
	ErrSTRIDENetworkShadowInvalid     = errors.New("invalid stride network shadow input")
	ErrSTRIDENetworkShadowConflict    = errors.New("stride network shadow revision conflict")
	ErrSTRIDENetworkShadowDiverged    = errors.New("stride network shadow projections diverged")
	ErrSTRIDENetworkShadowLagged      = errors.New("stride network shadow index is not current")
	ErrSTRIDENetworkShadowAuthority   = errors.New("stride network shadow authority is not current")
	ErrSTRIDENetworkShadowCrossTenant = errors.New("stride network shadow tenant mismatch")
)

type STRIDENetworkShadowConfig struct {
	Enabled                   bool
	SearchOrganizationID      string
	Now                       func() time.Time
	PurgeAuthority            STRIDENetworkShadowPurgeAuthority
	AuthorityResolver         STRIDENetworkShadowAuthorityResolver
	SearchAuthority           STRIDENetworkShadowSearchAuthorityResolver
	SnapshotKeys              STRIDENetworkShadowSnapshotKeyManager
	MinimumSnapshotGeneration uint64
	MinimumSnapshotKeyVersion uint64
	PurgeReceipts             STRIDENetworkShadowPurgeReceiptStore
	PurgeExecutor             STRIDENetworkShadowPurgeExecutor
	PurgeMaxAttempts          int
}

const strideNetworkShadowSnapshotDomain = "stride_network_shadow"

// STRIDENetworkShadowSnapshotKey is supplied by a managed key service. Key is
// never serialized; snapshots bind the stable key ID and monotonic key version.
type STRIDENetworkShadowSnapshotKey struct {
	KeyID   string
	Version uint64
	Key     []byte
}

type STRIDENetworkShadowSnapshotKeyManager interface {
	CurrentSTRIDENetworkShadowSnapshotKey() (STRIDENetworkShadowSnapshotKey, error)
	ResolveSTRIDENetworkShadowSnapshotKey(string, uint64) (STRIDENetworkShadowSnapshotKey, error)
}

// STRIDENetworkShadowPurgeAuthority is the fail-closed seam to the current
// contribution/network controller. A purge receipt is not authority merely
// because it is structurally valid.
type STRIDENetworkShadowPurgeAuthority interface {
	AuthorizeSTRIDEDerivedPurge(DerivedPurgeReceipt) bool
}

const (
	strideNetworkShadowPurgeQueued             = "queued"
	strideNetworkShadowPurgeRunning            = "running"
	strideNetworkShadowPurgeCompleted          = "completed"
	strideNetworkShadowPurgeFailed             = "failed"
	strideNetworkShadowDefaultPurgeMaxAttempts = 3
)

// STRIDENetworkShadowPurgeWork is body-free durable worker state. Failures are
// represented only by a bounded domain-separated digest and escalation flag.
type STRIDENetworkShadowPurgeWork struct {
	Receipt          DerivedPurgeReceipt `json:"receipt"`
	State            string              `json:"state"`
	ActiveStore      string              `json:"activeStore,omitempty"`
	FailureDigest    string              `json:"failureDigest,omitempty"`
	EscalationDigest string              `json:"escalationDigest,omitempty"`
	Escalated        bool                `json:"escalated"`
	Version          uint64              `json:"version"`
	UpdatedAt        time.Time           `json:"updatedAt"`
}

// Store implementations must make Create and CompareAndSwap durable before
// returning. Create is idempotent only for the exact receipt identity.
type STRIDENetworkShadowPurgeReceiptStore interface {
	CreateSTRIDENetworkShadowPurgeWork(context.Context, STRIDENetworkShadowPurgeWork) (bool, error)
	GetSTRIDENetworkShadowPurgeWork(context.Context, string) (STRIDENetworkShadowPurgeWork, bool, error)
	ListSTRIDENetworkShadowPurgeWork(context.Context) ([]STRIDENetworkShadowPurgeWork, error)
	CompareAndSwapSTRIDENetworkShadowPurgeWork(context.Context, uint64, STRIDENetworkShadowPurgeWork) (bool, error)
}

// Purge store operations must be idempotent for receipt ID, generation, and
// store. The worker may retry after a lost response or process restart.
type STRIDENetworkShadowPurgeExecutor interface {
	PurgeSTRIDENetworkShadowStore(context.Context, DerivedPurgeReceipt, string) error
}

type STRIDENetworkShadowAuthorityExpectation struct {
	SubjectPersonID string
	Publication     STRIDEReference
	Attestations    []STRIDEReference
}

type STRIDENetworkShadowAttestationAuthority struct {
	Reference STRIDEReference
	State     string
}

type STRIDENetworkShadowAuthoritySnapshot struct {
	Generation             uint64
	SubjectPersonID        string
	Publication            STRIDEReference
	PublicationState       string
	PublicationVisibility  string
	Attestations           []STRIDENetworkShadowAttestationAuthority
	ResolvedTerminalReason string
	ResolvedTerminalTarget STRIDEReference
	ResolvedAt             time.Time
}

// STRIDENetworkShadowAuthorityResolver must resolve canonical current rows,
// never a cached shadow copy. WithCurrent... must hold the canonical authority
// read capability through completion of use, so a successful callback is the
// linearization point for the final result copy or restored-state admission.
type STRIDENetworkShadowAuthorityResolver interface {
	ResolveCurrentSTRIDENetworkShadowAuthority(STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error)
	WithCurrentSTRIDENetworkShadowAuthorities([]STRIDENetworkShadowAuthoritySnapshot, func() error) error
}

type STRIDENetworkShadowSearchAuthorityExpectation struct {
	OrganizationID              string
	SessionHash                 string
	ActiveOrganizationSessionID string
	Authorities                 []STRIDENetworkShadowAuthoritySnapshot
}

type STRIDENetworkShadowSearchAuthoritySnapshot struct {
	Generation                   uint64
	SessionHash                  string
	PersonID                     string
	OrganizationID               string
	MembershipID                 string
	MembershipRevision           int64
	ActiveOrganizationSessionID  string
	ActiveOrganizationSessionRev int64
	Grant                        STRIDEReference
	GrantOrganizationID          string
	GrantSearcherPersonID        string
	GrantMembershipID            string
	GrantMembershipRevision      int64
	GrantState                   string
}

// WithCurrent... must hold the current session, membership, active-org
// binding, and talent_searcher grant through the final result copy.
type STRIDENetworkShadowSearchAuthorityResolver interface {
	WithCurrentSTRIDENetworkShadowSearchAuthority(context.Context, STRIDENetworkShadowSearchAuthorityExpectation, func(STRIDENetworkShadowSearchAuthoritySnapshot) error) error
}

type STRIDENetworkShadowCombinedSearchAuthorityResolver interface {
	STRIDENetworkShadowSearchAuthorityResolver
	STRIDENetworkShadowCombinedAuthorityValidation()
}

type STRIDENetworkShadowAdmission struct {
	Legacy       NetworkProfileProjection
	Canonical    NetworkProfileProjection
	Publication  PublishedContributionClaim
	Attestations []ContributionAttestation
}

type STRIDENetworkShadowComparison struct {
	SubjectPersonID string          `json:"subjectPersonId"`
	Legacy          STRIDEReference `json:"legacy"`
	Canonical       STRIDEReference `json:"canonical"`
	LegacyDigest    string          `json:"legacyDigest"`
	CanonicalDigest string          `json:"canonicalDigest"`
	Equivalent      bool            `json:"equivalent"`
}

type STRIDENetworkShadowSearchRequest struct {
	OrganizationID              string
	SessionHash                 string
	ActiveOrganizationSessionID string
	ExpectedSnapshotRevision    int64
	Filters                     []NetworkSearchFilter
}

// STRIDENetworkShadowDisclosureRequest revalidates an already-authorized,
// post-limit disclosure set. It deliberately contains no query or filters, so
// a final copy cannot silently broaden or rerun the original search.
type STRIDENetworkShadowDisclosureRequest struct {
	OrganizationID              string
	SessionHash                 string
	ActiveOrganizationSessionID string
	ExpectedSnapshotRevision    int64
	Results                     []STRIDENetworkShadowSearchResult
}

type STRIDENetworkShadowContactAuthorityExpectation struct {
	OrganizationID              string
	SessionHash                 string
	ActiveOrganizationSessionID string
	Grant                       STRIDEReference
}

type STRIDENetworkShadowSearchResult struct {
	Projection STRIDEReference         `json:"projection"`
	Fields     []NetworkPublishedField `json:"fields"`
}

type STRIDENetworkShadowSnapshotRecord struct {
	Admission  STRIDENetworkShadowAdmission  `json:"admission"`
	Comparison STRIDENetworkShadowComparison `json:"comparison"`
}

type STRIDENetworkShadowPurgeHighWater struct {
	SubjectPersonID string `json:"subjectPersonId"`
	Generation      int64  `json:"generation"`
}

type STRIDENetworkShadowAuthorityHighWater struct {
	SubjectPersonID string             `json:"subjectPersonId"`
	ID              string             `json:"id"`
	ContractType    STRIDEContractType `json:"contractType"`
	Revision        int64              `json:"revision"`
	Digest          string             `json:"digest"`
}

type STRIDENetworkShadowSnapshot struct {
	KeyID                string                                  `json:"keyId"`
	KeyVersion           uint64                                  `json:"keyVersion"`
	Generation           uint64                                  `json:"generation"`
	SearchOrganizationID string                                  `json:"searchOrganizationId"`
	Revision             int64                                   `json:"revision"`
	IndexedRevision      int64                                   `json:"indexedRevision"`
	Records              []STRIDENetworkShadowSnapshotRecord     `json:"records"`
	Purges               []DerivedPurgeReceipt                   `json:"purges"`
	PurgeHighWaters      []STRIDENetworkShadowPurgeHighWater     `json:"purgeHighWaters"`
	PublicationFences    []STRIDENetworkShadowAuthorityHighWater `json:"publicationFences"`
	AttestationFences    []STRIDENetworkShadowAuthorityHighWater `json:"attestationFences"`
	Digest               string                                  `json:"digest"`
	Signature            string                                  `json:"signature"`
}

type strideNetworkShadowFencedAuthority struct {
	subjectPersonID string
	reference       STRIDEReference
}

type strideNetworkShadowRecord struct {
	admission  STRIDENetworkShadowAdmission
	comparison STRIDENetworkShadowComparison
}

type STRIDENetworkShadowService struct {
	mu                     sync.RWMutex
	w6HealthMu             sync.RWMutex
	w6HealthPolicyRevision int64
	w6QualifiedProfiles    map[string]W6ConsentedProfileQualification
	config                 STRIDENetworkShadowConfig
	revision               int64
	indexedRevision        int64
	records                map[string]strideNetworkShadowRecord
	index                  map[string]map[string]bool
	publicationHighWater   map[string]STRIDEReference
	attestationHighWater   map[string]STRIDEReference
	publicationFence       map[string]strideNetworkShadowFencedAuthority
	attestationFence       map[string]strideNetworkShadowFencedAuthority
	purgeHighWater         map[string]int64
	purges                 map[string]DerivedPurgeReceipt
}

// BindCurrentW6Policy advances the health capability only from an exact
// managed-MAC policy authority. Construction leaves the revision at zero.
func (s *STRIDENetworkShadowService) BindCurrentW6Policy(ctx context.Context, policy *W6NetworkPolicyAuthority, qualification *W6NetworkQualificationAuthority, revision int64, cohortID string, at time.Time) error {
	if s == nil || policy == nil || qualification == nil {
		return ErrSTRIDENetworkShadowAuthority
	}
	return policy.WithCurrentW6Policy(ctx, revision, cohortID, at, func(current W6NetworkPolicyRevision) error {
		return qualification.WithCurrentW6Qualification(ctx, current, cohortID, at, func(receipt W6NetworkQualificationReceipt) error {
			manifest := make(map[string]W6ConsentedProfileQualification, len(receipt.Profiles))
			for _, profile := range receipt.Profiles {
				manifest[profile.PersonID] = cloneContract(profile)
			}
			s.w6HealthMu.Lock()
			defer s.w6HealthMu.Unlock()
			s.mu.Lock()
			defer s.mu.Unlock()
			if current.Revision <= s.w6HealthPolicyRevision {
				return ErrSTRIDENetworkShadowConflict
			}
			s.w6HealthPolicyRevision = current.Revision
			s.w6QualifiedProfiles = manifest
			return nil
		})
	})
}

func NewSTRIDENetworkShadowService(config STRIDENetworkShadowConfig) *STRIDENetworkShadowService {
	return &STRIDENetworkShadowService{config: config, records: map[string]strideNetworkShadowRecord{}, index: map[string]map[string]bool{}, w6QualifiedProfiles: map[string]W6ConsentedProfileQualification{}, publicationHighWater: map[string]STRIDEReference{}, attestationHighWater: map[string]STRIDEReference{}, publicationFence: map[string]strideNetworkShadowFencedAuthority{}, attestationFence: map[string]strideNetworkShadowFencedAuthority{}, purgeHighWater: map[string]int64{}, purges: map[string]DerivedPurgeReceipt{}}
}

// WithHealthyCurrentW6Shadow is the W6 search capability. It holds the shadow
// generation/currentness read lock through final result use and refuses lag,
// divergence, an unconfigured purge worker, or any unfinished/failed durable
// purge work. A zero policy revision never authorizes.
func (s *STRIDENetworkShadowService) WithHealthyCurrentW6Shadow(ctx context.Context, expectation W6ShadowHealthExpectation, use func(W6ShadowHealthSnapshot) error) error {
	if s == nil || !s.config.Enabled || use == nil || !strideIdentifier(expectation.OrganizationID) || expectation.PolicyRevision < 1 ||
		expectation.OrganizationID != s.config.SearchOrganizationID || expectation.PolicyRevision != s.w6HealthPolicyRevision || !s.purgeWorkerConfigured() {
		return ErrSTRIDENetworkShadowAuthority
	}
	s.w6HealthMu.RLock()
	defer s.w6HealthMu.RUnlock()
	work, err := s.config.PurgeReceipts.ListSTRIDENetworkShadowPurgeWork(ctx)
	if err != nil {
		return ErrSTRIDENetworkShadowAuthority
	}
	for _, item := range work {
		if item.State != strideNetworkShadowPurgeCompleted {
			return ErrSTRIDENetworkShadowAuthority
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.revision < 1 || s.indexedRevision != s.revision {
		return ErrSTRIDENetworkShadowLagged
	}
	if len(s.records) == 0 || len(s.w6QualifiedProfiles) == 0 {
		return ErrSTRIDENetworkShadowAuthority
	}
	for _, record := range s.records {
		if !record.comparison.Equivalent {
			return ErrSTRIDENetworkShadowDiverged
		}
		qualified, ok := s.w6QualifiedProfiles[record.admission.Canonical.SubjectPersonID]
		if !ok || qualified.PersonID != record.admission.Canonical.SubjectPersonID || qualified.Profile != referenceFromHeader(record.admission.Canonical.Header) || qualified.Publication != referenceFromHeader(record.admission.Publication.Header) ||
			qualified.AttestationCount != len(record.admission.Attestations) || qualified.AttestationCount != len(record.admission.Publication.Attestations) {
			return ErrSTRIDENetworkShadowAuthority
		}
	}
	var purgeGeneration int64
	var lastCompleted time.Time
	for _, receipt := range s.purges {
		if receipt.PurgeGeneration > purgeGeneration {
			purgeGeneration = receipt.PurgeGeneration
		}
		if receipt.State == "completed" && receipt.RecordedAt.After(lastCompleted) {
			lastCompleted = receipt.RecordedAt
		}
	}
	snapshot := W6ShadowHealthSnapshot{OrganizationID: expectation.OrganizationID, PolicyRevision: expectation.PolicyRevision, Generation: uint64(s.revision), SnapshotRevision: s.revision, IndexedRevision: s.indexedRevision, PurgeGeneration: purgeGeneration, LastCompletedPurge: lastCompleted, PurgeWorkerHealthy: true}
	if !snapshot.valid(expectation) {
		return ErrSTRIDENetworkShadowAuthority
	}
	return use(snapshot)
}

// WithCurrentSearchDisclosures copies only the exact post-limit projections
// recorded by the search receipt. Current session/grant and publication/
// attestation capabilities are held through the caller's final use.
func (s *STRIDENetworkShadowService) WithCurrentSearchDisclosures(ctx context.Context, request STRIDENetworkShadowDisclosureRequest, use func([]STRIDENetworkShadowSearchResult) error) error {
	if s == nil || !s.config.Enabled || ctx == nil || use == nil || request.OrganizationID != s.config.SearchOrganizationID ||
		!strideIdentifier(request.OrganizationID) || !validStrideE10SessionHash(request.SessionHash) || !strideIdentifier(request.ActiveOrganizationSessionID) || request.ExpectedSnapshotRevision < 1 || len(request.Results) == 0 || s.config.AuthorityResolver == nil || s.config.SearchAuthority == nil {
		return ErrSTRIDENetworkShadowInvalid
	}
	s.mu.RLock()
	if request.ExpectedSnapshotRevision != s.revision || s.indexedRevision != s.revision {
		s.mu.RUnlock()
		return ErrSTRIDENetworkShadowLagged
	}
	records := make([]strideNetworkShadowRecord, 0, len(request.Results))
	for _, wanted := range request.Results {
		record, ok := s.recordsByProjectionLocked(wanted.Projection)
		if !ok || !record.comparison.Equivalent || record.admission.Canonical.State != "published" || record.admission.Canonical.Discoverability != "signed_in_network" || !sameNetworkPublishedFields(networkVisiblePublishedFields(record.admission.Canonical.Fields), wanted.Fields) {
			s.mu.RUnlock()
			return ErrSTRIDENetworkShadowAuthority
		}
		records = append(records, strideNetworkShadowRecord{admission: cloneContract(record.admission), comparison: record.comparison})
	}
	s.mu.RUnlock()
	snapshots := make([]STRIDENetworkShadowAuthoritySnapshot, 0, len(records))
	for _, record := range records {
		expected := shadowAuthorityExpectation(record.admission)
		current, err := s.config.AuthorityResolver.ResolveCurrentSTRIDENetworkShadowAuthority(expected)
		if err != nil {
			return ErrSTRIDENetworkShadowAuthority
		}
		if !validCurrentShadowAuthority(expected, current) {
			if validTerminalShadowAuthority(expected, current) {
				_, _ = s.fenceResolvedAuthority(record.admission.Canonical.SubjectPersonID, current.ResolvedTerminalTarget, "revalidation", current.ResolvedAt.UTC())
			}
			return ErrSTRIDENetworkShadowAuthority
		}
		snapshots = append(snapshots, current)
	}
	expectation := STRIDENetworkShadowSearchAuthorityExpectation{OrganizationID: request.OrganizationID, SessionHash: request.SessionHash, ActiveOrganizationSessionID: request.ActiveOrganizationSessionID, Authorities: snapshots}
	err := s.config.SearchAuthority.WithCurrentSTRIDENetworkShadowSearchAuthority(ctx, expectation, func(search STRIDENetworkShadowSearchAuthoritySnapshot) error {
		if !validSTRIDENetworkShadowSearchAuthority(expectation, search) {
			return ErrSTRIDENetworkShadowAuthority
		}
		finalUse := func() error {
			s.mu.RLock()
			defer s.mu.RUnlock()
			if request.ExpectedSnapshotRevision != s.revision || s.indexedRevision != s.revision {
				return ErrSTRIDENetworkShadowLagged
			}
			copied := make([]STRIDENetworkShadowSearchResult, 0, len(request.Results))
			for _, wanted := range request.Results {
				record, ok := s.recordsByProjectionLocked(wanted.Projection)
				if !ok || !record.comparison.Equivalent || record.admission.Canonical.State != "published" || record.admission.Canonical.Discoverability != "signed_in_network" {
					return ErrSTRIDENetworkShadowAuthority
				}
				fields := networkVisiblePublishedFields(record.admission.Canonical.Fields)
				if !sameNetworkPublishedFields(fields, wanted.Fields) {
					return ErrSTRIDENetworkShadowAuthority
				}
				copied = append(copied, STRIDENetworkShadowSearchResult{Projection: wanted.Projection, Fields: fields})
			}
			return use(copied)
		}
		if _, combined := s.config.SearchAuthority.(STRIDENetworkShadowCombinedSearchAuthorityResolver); combined {
			return finalUse()
		}
		return s.config.AuthorityResolver.WithCurrentSTRIDENetworkShadowAuthorities(snapshots, finalUse)
	})
	if err != nil {
		byPerson := make(map[string]strideNetworkShadowRecord, len(records))
		for _, record := range records {
			byPerson[record.admission.Canonical.SubjectPersonID] = record
		}
		s.reconcileFailedSearchAuthorities(byPerson)
		if !errors.Is(err, ErrSTRIDENetworkShadowLagged) && !errors.Is(err, ErrSTRIDENetworkShadowDiverged) {
			return ErrSTRIDENetworkShadowAuthority
		}
	}
	return err
}

// WithCurrentContactAuthority holds the exact session, membership and grant
// capability through the contact writer callback.
func (s *STRIDENetworkShadowService) WithCurrentContactAuthority(ctx context.Context, expectation STRIDENetworkShadowContactAuthorityExpectation, use func() error) error {
	if s == nil || ctx == nil || use == nil || !strideIdentifier(expectation.OrganizationID) || !validStrideE10SessionHash(expectation.SessionHash) || !strideIdentifier(expectation.ActiveOrganizationSessionID) || expectation.Grant.Validate() != nil || s.config.SearchAuthority == nil {
		return ErrSTRIDENetworkShadowInvalid
	}
	searchExpectation := STRIDENetworkShadowSearchAuthorityExpectation{OrganizationID: expectation.OrganizationID, SessionHash: expectation.SessionHash, ActiveOrganizationSessionID: expectation.ActiveOrganizationSessionID}
	return s.config.SearchAuthority.WithCurrentSTRIDENetworkShadowSearchAuthority(ctx, searchExpectation, func(snapshot STRIDENetworkShadowSearchAuthoritySnapshot) error {
		if !validSTRIDENetworkShadowSearchAuthority(searchExpectation, snapshot) || snapshot.ActiveOrganizationSessionID != expectation.ActiveOrganizationSessionID || snapshot.Grant != expectation.Grant {
			return ErrSTRIDENetworkShadowAuthority
		}
		return use()
	})
}

func (s *STRIDENetworkShadowService) recordsByProjectionLocked(reference STRIDEReference) (strideNetworkShadowRecord, bool) {
	for _, record := range s.records {
		if referenceFromHeader(record.admission.Canonical.Header) == reference {
			return record, true
		}
	}
	return strideNetworkShadowRecord{}, false
}

func (s *STRIDENetworkShadowService) purgeWorkerConfigured() bool {
	return s != nil && s.config.PurgeReceipts != nil && s.config.PurgeExecutor != nil && s.purgeMaxAttempts() >= 1 && s.purgeMaxAttempts() <= 10
}

func (s *STRIDENetworkShadowService) purgeMaxAttempts() int {
	if s == nil || s.config.PurgeMaxAttempts == 0 {
		return strideNetworkShadowDefaultPurgeMaxAttempts
	}
	return s.config.PurgeMaxAttempts
}

func (s *STRIDENetworkShadowService) Ingest(admission STRIDENetworkShadowAdmission) (STRIDENetworkShadowComparison, bool, error) {
	if s == nil || !s.config.Enabled {
		return STRIDENetworkShadowComparison{}, false, ErrSTRIDENetworkShadowDisabled
	}
	if !s.purgeWorkerConfigured() {
		return STRIDENetworkShadowComparison{}, false, ErrSTRIDENetworkShadowInvalid
	}
	comparison, err := validateSTRIDENetworkShadowAdmission(admission)
	if err != nil {
		return STRIDENetworkShadowComparison{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkHighWatersLocked(admission); err != nil {
		return STRIDENetworkShadowComparison{}, false, err
	}
	personID := admission.Canonical.SubjectPersonID
	if prior, ok := s.records[personID]; ok {
		priorDigest, _ := STRIDEContractDigest(prior.admission)
		nextDigest, _ := STRIDEContractDigest(admission)
		if priorDigest == nextDigest {
			return prior.comparison, false, nil
		}
		if !admission.Canonical.StateChangedAt.After(prior.admission.Canonical.StateChangedAt) {
			return STRIDENetworkShadowComparison{}, false, ErrSTRIDENetworkShadowConflict
		}
	}
	s.records[personID] = strideNetworkShadowRecord{admission: cloneContract(admission), comparison: comparison}
	s.updateHighWatersLocked(admission)
	s.rebuildIndexLocked()
	s.revision++
	s.indexedRevision = s.revision
	return comparison, true, nil
}

func validateSTRIDENetworkShadowAdmission(admission STRIDENetworkShadowAdmission) (STRIDENetworkShadowComparison, error) {
	legacy, canonical, publication := admission.Legacy, admission.Canonical, admission.Publication
	if legacy.Validate() != nil || canonical.Validate() != nil || publication.Validate() != nil || legacy.State != "published" || canonical.State != "published" || !oneOf(legacy.Discoverability, "signed_in_network", "exact_link") || canonical.Discoverability != legacy.Discoverability || publication.State != "published" || !oneOf(publication.Visibility, "signed_in_network", "exact_link") || legacy.Discoverability != publication.Visibility || legacy.SubjectPersonID != canonical.SubjectPersonID || canonical.SubjectPersonID != publication.SubjectPersonID {
		return STRIDENetworkShadowComparison{}, ErrSTRIDENetworkShadowInvalid
	}
	publicationRef := referenceFromHeader(publication.Header)
	if legacy.Publication != publicationRef || canonical.Publication != publicationRef {
		return STRIDENetworkShadowComparison{}, ErrSTRIDENetworkShadowInvalid
	}
	attestations := make(map[string]ContributionAttestation, len(admission.Attestations))
	for _, attestation := range admission.Attestations {
		if attestation.Validate() != nil || attestation.State != "active" || attestation.SubjectPersonID != publication.SubjectPersonID {
			return STRIDENetworkShadowComparison{}, ErrSTRIDENetworkShadowInvalid
		}
		if _, exists := attestations[attestation.Header.ID]; exists {
			return STRIDENetworkShadowComparison{}, ErrSTRIDENetworkShadowInvalid
		}
		attestations[attestation.Header.ID] = attestation
	}
	if len(attestations) != len(publication.Attestations) {
		return STRIDENetworkShadowComparison{}, ErrSTRIDENetworkShadowInvalid
	}
	for _, ref := range publication.Attestations {
		attestation, ok := attestations[ref.ID]
		if !ok || referenceFromHeader(attestation.Header) != ref {
			return STRIDENetworkShadowComparison{}, ErrSTRIDENetworkShadowInvalid
		}
	}
	if !shadowProjectionEvidenceEligible(legacy, publicationRef, attestations) || !shadowProjectionEvidenceEligible(canonical, publicationRef, attestations) {
		return STRIDENetworkShadowComparison{}, ErrSTRIDENetworkShadowInvalid
	}
	legacyDigest := shadowVisibleProjectionDigest(legacy)
	canonicalDigest := shadowVisibleProjectionDigest(canonical)
	return STRIDENetworkShadowComparison{SubjectPersonID: canonical.SubjectPersonID, Legacy: referenceFromHeader(legacy.Header), Canonical: referenceFromHeader(canonical.Header), LegacyDigest: legacyDigest, CanonicalDigest: canonicalDigest, Equivalent: legacyDigest == canonicalDigest}, nil
}

// WithCurrentExactLinkProjection admits one exact-link target without making
// it searchable. Publication and attestation authority remain held through the
// final copied disclosure.
func (s *STRIDENetworkShadowService) WithCurrentExactLinkProjection(reference STRIDEReference, use func(STRIDENetworkShadowSearchResult) error) error {
	if s == nil || !s.config.Enabled || reference.Validate() != nil || reference.ContractType != STRIDEContractNetworkProfileProjection || use == nil || s.config.AuthorityResolver == nil {
		return ErrSTRIDENetworkShadowInvalid
	}
	s.mu.RLock()
	var record strideNetworkShadowRecord
	found := false
	for _, candidate := range s.records {
		if referenceFromHeader(candidate.admission.Canonical.Header) == reference {
			record, found = candidate, true
			break
		}
	}
	s.mu.RUnlock()
	if !found || !record.comparison.Equivalent || record.admission.Canonical.Discoverability != "exact_link" {
		return ErrSTRIDENetworkShadowAuthority
	}
	expectation := shadowAuthorityExpectation(record.admission)
	snapshot, err := s.config.AuthorityResolver.ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
	if err != nil || !validCurrentShadowAuthority(expectation, snapshot) {
		return ErrSTRIDENetworkShadowAuthority
	}
	return s.config.AuthorityResolver.WithCurrentSTRIDENetworkShadowAuthorities([]STRIDENetworkShadowAuthoritySnapshot{snapshot}, func() error {
		s.mu.RLock()
		defer s.mu.RUnlock()
		current, ok := s.records[record.admission.Canonical.SubjectPersonID]
		if !ok || referenceFromHeader(current.admission.Canonical.Header) != reference || !current.comparison.Equivalent || current.admission.Canonical.Discoverability != "exact_link" {
			return ErrSTRIDENetworkShadowAuthority
		}
		return use(STRIDENetworkShadowSearchResult{Projection: reference, Fields: networkVisiblePublishedFields(current.admission.Canonical.Fields)})
	})
}

func shadowProjectionEvidenceEligible(profile NetworkProfileProjection, publication STRIDEReference, attestations map[string]ContributionAttestation) bool {
	for _, field := range networkVisiblePublishedFields(profile.Fields) {
		if field.EvidenceLabel == "self_described" {
			continue
		}
		if field.Claim == nil || *field.Claim != publication {
			return false
		}
		eligible := false
		for _, attestation := range attestations {
			if attestation.VerificationTier != field.EvidenceLabel {
				continue
			}
			for _, released := range attestation.ReleasedFields {
				if networkReleasedFieldMatches(field.FieldKey, released.FieldKey) && field.ValueDigest == released.ValueDigest {
					eligible = true
					break
				}
			}
			if eligible {
				break
			}
		}
		if !eligible {
			return false
		}
	}
	return true
}

func shadowVisibleProjectionDigest(profile NetworkProfileProjection) string {
	visibleFields := networkVisiblePublishedFields(profile.Fields)
	visibleFieldsDigest, _ := STRIDEContractDigest(visibleFields)
	value := struct {
		Publication                          STRIDEReference         `json:"publication"`
		Fields                               []NetworkPublishedField `json:"fields"`
		FieldsDigest, Discoverability, State string
		PurgeGeneration                      int64
	}{profile.Publication, visibleFields, visibleFieldsDigest, profile.Discoverability, profile.State, profile.PurgeGeneration}
	digest, _ := STRIDEContractDigest(value)
	return digest
}

func (s *STRIDENetworkShadowService) checkHighWatersLocked(admission STRIDENetworkShadowAdmission) error {
	publication := admission.Publication
	publicationRef := referenceFromHeader(publication.Header)
	if fenced := s.publicationFence[publication.Header.ID].reference; fenced.Revision > 0 && publication.Header.Revision <= fenced.Revision {
		return ErrSTRIDENetworkShadowConflict
	}
	if !shadowAuthorityAdvances(s.publicationHighWater[publication.Header.ID], publicationRef, publication.Supersedes) {
		return ErrSTRIDENetworkShadowConflict
	}
	for _, attestation := range admission.Attestations {
		attestationRef := referenceFromHeader(attestation.Header)
		if fenced := s.attestationFence[attestation.Header.ID].reference; fenced.Revision > 0 && attestation.Header.Revision <= fenced.Revision {
			return ErrSTRIDENetworkShadowConflict
		}
		if !shadowAuthorityAdvances(s.attestationHighWater[attestation.Header.ID], attestationRef, attestation.Supersedes) {
			return ErrSTRIDENetworkShadowConflict
		}
	}
	return nil
}

func shadowAuthorityAdvances(high, next STRIDEReference, supersedes *STRIDEReference) bool {
	if high.Revision == 0 {
		return true
	}
	if next.Revision < high.Revision || next.Revision == high.Revision && next.Digest != high.Digest {
		return false
	}
	if next.Revision == high.Revision {
		return next == high
	}
	return supersedes != nil && *supersedes == high && next.Revision == high.Revision+1
}

func (s *STRIDENetworkShadowService) updateHighWatersLocked(admission STRIDENetworkShadowAdmission) {
	publication := admission.Publication
	if publication.Header.Revision > s.publicationHighWater[publication.Header.ID].Revision {
		s.publicationHighWater[publication.Header.ID] = referenceFromHeader(publication.Header)
	}
	for _, attestation := range admission.Attestations {
		if attestation.Header.Revision > s.attestationHighWater[attestation.Header.ID].Revision {
			s.attestationHighWater[attestation.Header.ID] = referenceFromHeader(attestation.Header)
		}
	}
}

func (s *STRIDENetworkShadowService) Search(request STRIDENetworkShadowSearchRequest) ([]STRIDENetworkShadowSearchResult, error) {
	var results []STRIDENetworkShadowSearchResult
	err := s.WithCurrentSearchAdmission(context.Background(), request, func(current []STRIDENetworkShadowSearchResult) error {
		results = cloneContract(current)
		return nil
	})
	return results, err
}

// WithCurrentSearchAdmission holds the exact session, active-session,
// membership and grant capability through policy admission and receipt use.
func (s *STRIDENetworkShadowService) WithCurrentSearchAdmission(ctx context.Context, request STRIDENetworkShadowSearchRequest, use func([]STRIDENetworkShadowSearchResult) error) error {
	if s == nil || !s.config.Enabled {
		return ErrSTRIDENetworkShadowDisabled
	}
	if request.OrganizationID != s.config.SearchOrganizationID {
		return ErrSTRIDENetworkShadowCrossTenant
	}
	if ctx == nil || use == nil || !strideIdentifier(request.OrganizationID) || !validStrideE10SessionHash(request.SessionHash) || !strideIdentifier(request.ActiveOrganizationSessionID) || request.ExpectedSnapshotRevision < 1 || len(request.Filters) == 0 || s.config.Now == nil {
		return ErrSTRIDENetworkShadowInvalid
	}
	at := s.config.Now().UTC()
	if at.IsZero() {
		return ErrSTRIDENetworkShadowInvalid
	}
	for _, filter := range request.Filters {
		if filter.Validate() != nil {
			return ErrSTRIDENetworkShadowInvalid
		}
	}
	if s.config.AuthorityResolver == nil || s.config.SearchAuthority == nil {
		return ErrSTRIDENetworkShadowAuthority
	}
	records, authorities, err := s.prepareCurrentSearchRecords(request.ExpectedSnapshotRevision)
	if err != nil {
		return err
	}
	expectation := STRIDENetworkShadowSearchAuthorityExpectation{OrganizationID: request.OrganizationID, SessionHash: request.SessionHash, ActiveOrganizationSessionID: request.ActiveOrganizationSessionID, Authorities: authorities}
	err = s.config.SearchAuthority.WithCurrentSTRIDENetworkShadowSearchAuthority(ctx, expectation, func(snapshot STRIDENetworkShadowSearchAuthoritySnapshot) error {
		if !validSTRIDENetworkShadowSearchAuthority(expectation, snapshot) {
			return ErrSTRIDENetworkShadowAuthority
		}
		finalUse := func() error {
			s.mu.RLock()
			defer s.mu.RUnlock()
			if request.ExpectedSnapshotRevision != s.revision || s.indexedRevision != s.revision {
				return ErrSTRIDENetworkShadowLagged
			}
			results := s.searchRecordsLocked(records, request.Filters, at)
			return use(results)
		}
		if _, combined := s.config.SearchAuthority.(STRIDENetworkShadowCombinedSearchAuthorityResolver); combined {
			return finalUse()
		}
		return s.config.AuthorityResolver.WithCurrentSTRIDENetworkShadowAuthorities(authorities, finalUse)
	})
	if err != nil {
		s.reconcileFailedSearchAuthorities(records)
		if !errors.Is(err, ErrSTRIDENetworkShadowLagged) && !errors.Is(err, ErrSTRIDENetworkShadowDiverged) {
			return ErrSTRIDENetworkShadowAuthority
		}
	}
	return err
}

func validSTRIDENetworkShadowSearchAuthority(expectation STRIDENetworkShadowSearchAuthorityExpectation, snapshot STRIDENetworkShadowSearchAuthoritySnapshot) bool {
	return snapshot.Generation > 0 && snapshot.SessionHash == expectation.SessionHash && snapshot.OrganizationID == expectation.OrganizationID && snapshot.ActiveOrganizationSessionID == expectation.ActiveOrganizationSessionID &&
		strideIdentifier(snapshot.PersonID) && strideIdentifier(snapshot.MembershipID) && snapshot.MembershipRevision > 0 &&
		strideIdentifier(snapshot.ActiveOrganizationSessionID) && snapshot.ActiveOrganizationSessionRev > 0 && snapshot.Grant.Validate() == nil &&
		snapshot.Grant.ContractType == STRIDEContractTalentSearchGrant && snapshot.GrantState == "active" &&
		snapshot.GrantOrganizationID == snapshot.OrganizationID && snapshot.GrantSearcherPersonID == snapshot.PersonID &&
		snapshot.GrantMembershipID == snapshot.MembershipID && snapshot.GrantMembershipRevision == snapshot.MembershipRevision
}

func (s *STRIDENetworkShadowService) prepareCurrentSearchRecords(expectedRevision int64) (map[string]strideNetworkShadowRecord, []STRIDENetworkShadowAuthoritySnapshot, error) {
	s.mu.RLock()
	if expectedRevision != s.revision || s.indexedRevision != s.revision {
		s.mu.RUnlock()
		return nil, nil, ErrSTRIDENetworkShadowLagged
	}
	records := make(map[string]strideNetworkShadowRecord, len(s.records))
	for personID, record := range s.records {
		if !record.comparison.Equivalent {
			s.mu.RUnlock()
			return nil, nil, ErrSTRIDENetworkShadowDiverged
		}
		records[personID] = strideNetworkShadowRecord{admission: cloneContract(record.admission), comparison: record.comparison}
	}
	s.mu.RUnlock()
	people := make([]string, 0, len(records))
	for personID := range records {
		people = append(people, personID)
	}
	sort.Strings(people)
	authorities := make([]STRIDENetworkShadowAuthoritySnapshot, 0, len(people))
	for _, personID := range people {
		expected := shadowAuthorityExpectation(records[personID].admission)
		current, err := s.config.AuthorityResolver.ResolveCurrentSTRIDENetworkShadowAuthority(expected)
		if err != nil {
			return nil, nil, ErrSTRIDENetworkShadowAuthority
		}
		if !validCurrentShadowAuthority(expected, current) {
			if validTerminalShadowAuthority(expected, current) {
				_, _ = s.fenceResolvedAuthority(personID, current.ResolvedTerminalTarget, "revalidation", current.ResolvedAt.UTC())
			}
			return nil, nil, ErrSTRIDENetworkShadowAuthority
		}
		authorities = append(authorities, current)
	}
	return records, authorities, nil
}

func (s *STRIDENetworkShadowService) reconcileFailedSearchAuthorities(records map[string]strideNetworkShadowRecord) {
	for personID, record := range records {
		expected := shadowAuthorityExpectation(record.admission)
		current, err := s.config.AuthorityResolver.ResolveCurrentSTRIDENetworkShadowAuthority(expected)
		if err != nil || validCurrentShadowAuthority(expected, current) {
			continue
		}
		if validTerminalShadowAuthority(expected, current) {
			_, _ = s.fenceResolvedAuthority(personID, current.ResolvedTerminalTarget, "revalidation", current.ResolvedAt.UTC())
		}
	}
}

func (s *STRIDENetworkShadowService) searchRecordsLocked(records map[string]strideNetworkShadowRecord, filters []NetworkSearchFilter, at time.Time) []STRIDENetworkShadowSearchResult {
	people := make([]string, 0, len(records))
	for personID, record := range records {
		if record.admission.Canonical.Discoverability != "signed_in_network" || record.admission.Canonical.State != "published" {
			continue
		}
		matched := true
		for _, filter := range filters {
			found := false
			for _, field := range networkVisiblePublishedFields(record.admission.Canonical.Fields) {
				if networkFieldMatchesFilter(field, record.admission.Canonical, filter, at) {
					found = true
					break
				}
			}
			if !found {
				matched = false
				break
			}
		}
		if matched {
			people = append(people, personID)
		}
	}
	sort.Strings(people)
	results := make([]STRIDENetworkShadowSearchResult, 0, len(people))
	for _, personID := range people {
		profile := records[personID].admission.Canonical
		results = append(results, STRIDENetworkShadowSearchResult{Projection: referenceFromHeader(profile.Header), Fields: networkVisiblePublishedFields(profile.Fields)})
	}
	return results
}

func (s *STRIDENetworkShadowService) searchWithCurrentAuthority(request STRIDENetworkShadowSearchRequest, at time.Time) ([]STRIDENetworkShadowSearchResult, error) {
	s.mu.RLock()
	if request.ExpectedSnapshotRevision != s.revision || s.indexedRevision != s.revision {
		s.mu.RUnlock()
		return nil, ErrSTRIDENetworkShadowLagged
	}
	records := make(map[string]strideNetworkShadowRecord, len(s.records))
	for personID, record := range s.records {
		if !record.comparison.Equivalent {
			s.mu.RUnlock()
			return nil, ErrSTRIDENetworkShadowDiverged
		}
		records[personID] = strideNetworkShadowRecord{admission: cloneContract(record.admission), comparison: record.comparison}
	}
	s.mu.RUnlock()

	people := make([]string, 0, len(records))
	for personID := range records {
		people = append(people, personID)
	}
	sort.Strings(people)
	snapshots := make([]STRIDENetworkShadowAuthoritySnapshot, 0, len(records))
	for _, personID := range people {
		record := records[personID]
		expectation := shadowAuthorityExpectation(record.admission)
		snapshot, err := s.config.AuthorityResolver.ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
		if err != nil {
			return nil, ErrSTRIDENetworkShadowAuthority
		}
		if !validCurrentShadowAuthority(expectation, snapshot) {
			if validTerminalShadowAuthority(expectation, snapshot) {
				_, _ = s.fenceResolvedAuthority(personID, snapshot.ResolvedTerminalTarget, "revalidation", snapshot.ResolvedAt.UTC())
			}
			return nil, ErrSTRIDENetworkShadowAuthority
		}
		snapshots = append(snapshots, snapshot)
	}

	var results []STRIDENetworkShadowSearchResult
	var useErr error
	authorityErr := s.config.AuthorityResolver.WithCurrentSTRIDENetworkShadowAuthorities(snapshots, func() error {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if request.ExpectedSnapshotRevision != s.revision || s.indexedRevision != s.revision {
			useErr = ErrSTRIDENetworkShadowLagged
			return useErr
		}
		var candidates map[string]bool
		for _, filter := range request.Filters {
			matched := s.index[networkSearchIndexKey(filter.Field, filter.ValueDigest)]
			if filter.Field == "freshness_bucket" {
				matched = map[string]bool{}
				for personID, record := range s.records {
					for _, field := range networkVisiblePublishedFields(record.admission.Canonical.Fields) {
						if networkFieldMatchesFilter(field, record.admission.Canonical, filter, at) {
							matched[personID] = true
							break
						}
					}
				}
			}
			if candidates == nil {
				candidates = cloneShadowSet(matched)
				continue
			}
			for personID := range candidates {
				if !matched[personID] {
					delete(candidates, personID)
				}
			}
		}
		matchedPeople := make([]string, 0, len(candidates))
		for personID := range candidates {
			matchedPeople = append(matchedPeople, personID)
		}
		sort.Strings(matchedPeople)
		results = make([]STRIDENetworkShadowSearchResult, 0, len(matchedPeople))
		for _, personID := range matchedPeople {
			projection := s.records[personID].admission.Canonical
			results = append(results, STRIDENetworkShadowSearchResult{Projection: referenceFromHeader(projection.Header), Fields: networkVisiblePublishedFields(projection.Fields)})
		}
		return nil
	})
	if authorityErr != nil {
		if useErr != nil {
			return nil, useErr
		}
		return nil, ErrSTRIDENetworkShadowAuthority
	}
	return results, nil
}

func (a STRIDENetworkShadowAdmission) PublicationRef() STRIDEReference {
	return referenceFromHeader(a.Publication.Header)
}

func shadowAuthorityExpectation(admission STRIDENetworkShadowAdmission) STRIDENetworkShadowAuthorityExpectation {
	refsByID := map[string]STRIDEReference{}
	for _, field := range networkVisiblePublishedFields(admission.Canonical.Fields) {
		if field.EvidenceLabel == "self_described" {
			continue
		}
		for _, attestation := range admission.Attestations {
			if attestation.VerificationTier != field.EvidenceLabel {
				continue
			}
			for _, released := range attestation.ReleasedFields {
				if networkReleasedFieldMatches(field.FieldKey, released.FieldKey) && field.ValueDigest == released.ValueDigest {
					refsByID[attestation.Header.ID] = referenceFromHeader(attestation.Header)
				}
			}
		}
	}
	refs := make([]STRIDEReference, 0, len(refsByID))
	for _, reference := range refsByID {
		refs = append(refs, reference)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	return STRIDENetworkShadowAuthorityExpectation{SubjectPersonID: admission.Canonical.SubjectPersonID, Publication: admission.PublicationRef(), Attestations: refs}
}

func validCurrentShadowAuthority(expectation STRIDENetworkShadowAuthorityExpectation, snapshot STRIDENetworkShadowAuthoritySnapshot) bool {
	if snapshot.Generation == 0 || snapshot.SubjectPersonID != expectation.SubjectPersonID || snapshot.Publication != expectation.Publication || snapshot.PublicationState != "published" || !oneOf(snapshot.PublicationVisibility, "signed_in_network", "exact_link") || len(snapshot.Attestations) != len(expectation.Attestations) {
		return false
	}
	actual := append([]STRIDENetworkShadowAttestationAuthority(nil), snapshot.Attestations...)
	sort.Slice(actual, func(i, j int) bool { return actual[i].Reference.ID < actual[j].Reference.ID })
	for index, expected := range expectation.Attestations {
		if actual[index].State != "active" || actual[index].Reference != expected {
			return false
		}
	}
	return true
}

func validTerminalShadowAuthority(expectation STRIDENetworkShadowAuthorityExpectation, snapshot STRIDENetworkShadowAuthoritySnapshot) bool {
	if snapshot.Generation == 0 || snapshot.SubjectPersonID != expectation.SubjectPersonID || snapshot.ResolvedAt.IsZero() ||
		!oneOf(snapshot.ResolvedTerminalReason, "withdrawn", "revoked", "superseded") || snapshot.ResolvedTerminalTarget.Validate() != nil {
		return false
	}
	if snapshot.ResolvedTerminalTarget == expectation.Publication {
		return snapshot.ResolvedTerminalReason == "withdrawn" || snapshot.ResolvedTerminalReason == "superseded"
	}
	for _, attestation := range expectation.Attestations {
		if snapshot.ResolvedTerminalTarget == attestation {
			return snapshot.ResolvedTerminalReason == "revoked" || snapshot.ResolvedTerminalReason == "superseded"
		}
	}
	return false
}

func cloneShadowSet(value map[string]bool) map[string]bool {
	result := map[string]bool{}
	for key, enabled := range value {
		if enabled {
			result[key] = true
		}
	}
	return result
}

func (s *STRIDENetworkShadowService) rebuildIndexLocked() {
	s.index = map[string]map[string]bool{}
	for personID, record := range s.records {
		if record.admission.Canonical.Discoverability != "signed_in_network" {
			continue
		}
		for _, field := range networkVisiblePublishedFields(record.admission.Canonical.Fields) {
			for _, key := range networkFieldStaticIndexKeys(field) {
				if s.index[key] == nil {
					s.index[key] = map[string]bool{}
				}
				s.index[key][personID] = true
			}
		}
	}
}

// fenceResolvedAuthority is unexported: it is reached only after the service's
// current-authority resolver proves a stale row, or by focused same-package
// tests. External fencing must pass a controller-issued receipt to ApplyPurge.
func (s *STRIDENetworkShadowService) fenceResolvedAuthority(subjectPersonID string, trigger STRIDEReference, reason string, at time.Time) (DerivedPurgeReceipt, error) {
	if s == nil || !s.config.Enabled {
		return DerivedPurgeReceipt{}, ErrSTRIDENetworkShadowDisabled
	}
	if !strideIdentifier(subjectPersonID) || trigger.Validate() != nil || !oneOf(reason, "revalidation", "revoke", "purge") || at.IsZero() {
		return DerivedPurgeReceipt{}, ErrSTRIDENetworkShadowInvalid
	}
	if !s.purgeWorkerConfigured() {
		return DerivedPurgeReceipt{}, ErrSTRIDENetworkShadowInvalid
	}
	s.w6HealthMu.Lock()
	defer s.w6HealthMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[subjectPersonID]
	if !ok || !shadowTriggerMatches(record.admission, trigger) {
		return DerivedPurgeReceipt{}, ErrSTRIDENetworkShadowInvalid
	}
	generation := s.purgeHighWater[subjectPersonID] + 1
	if generation <= record.admission.Canonical.PurgeGeneration {
		generation = record.admission.Canonical.PurgeGeneration + 1
	}
	digest := sha256Hex([]byte(subjectPersonID + "\x00" + trigger.ID + "\x00" + fmt.Sprint(generation) + "\x00" + reason))
	stores := make([]PurgeStoreResult, 0, len(contributionPurgeStores))
	for _, store := range contributionPurgeStores {
		stores = append(stores, PurgeStoreResult{Store: store, State: "queued", AttemptCount: 1})
	}
	receipt := DerivedPurgeReceipt{Header: STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "purge_" + digest[:24], Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractDerivedPurgeReceipt, ContentDigest: digest, CreatedAt: at.UTC()}, SubjectPersonID: subjectPersonID, Trigger: trigger, PurgeGeneration: generation, AffectedFieldsDigest: record.admission.Canonical.FieldsDigest, Stores: stores, EligibilityFencedAt: at.UTC(), RecordedAt: at.UTC(), State: "queued"}
	if err := s.persistNewPurgeWork(context.Background(), receipt); err != nil {
		return DerivedPurgeReceipt{}, ErrSTRIDENetworkShadowInvalid
	}
	if err := s.applyPurgeLocked(receipt); err != nil {
		return DerivedPurgeReceipt{}, err
	}
	return cloneContract(receipt), nil
}

func (s *STRIDENetworkShadowService) ApplyPurge(receipt DerivedPurgeReceipt) (bool, error) {
	if s == nil || !s.config.Enabled {
		return false, ErrSTRIDENetworkShadowDisabled
	}
	if receipt.Validate() != nil || receipt.Header.TenantID != STRIDEGlobalPersonTenant {
		return false, ErrSTRIDENetworkShadowCrossTenant
	}
	if s.config.PurgeAuthority == nil || !s.config.PurgeAuthority.AuthorizeSTRIDEDerivedPurge(receipt) {
		return false, ErrSTRIDENetworkShadowInvalid
	}
	if !s.purgeWorkerConfigured() {
		return false, ErrSTRIDENetworkShadowInvalid
	}
	s.w6HealthMu.Lock()
	defer s.w6HealthMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipt.PurgeGeneration == s.purgeHighWater[receipt.SubjectPersonID] {
		prior, ok := s.purges[receipt.Header.ID]
		if ok && prior.Header.ContentDigest == receipt.Header.ContentDigest {
			return false, nil
		}
		return false, ErrSTRIDENetworkShadowConflict
	}
	if receipt.PurgeGeneration < s.purgeHighWater[receipt.SubjectPersonID] {
		return false, ErrSTRIDENetworkShadowConflict
	}
	if record, ok := s.records[receipt.SubjectPersonID]; ok && !shadowTriggerMatches(record.admission, receipt.Trigger) {
		return false, ErrSTRIDENetworkShadowInvalid
	}
	if err := s.persistNewPurgeWork(context.Background(), receipt); err != nil {
		return false, ErrSTRIDENetworkShadowInvalid
	}
	if err := s.applyPurgeLocked(receipt); err != nil {
		return false, err
	}
	return true, nil
}

func (s *STRIDENetworkShadowService) applyPurgeLocked(receipt DerivedPurgeReceipt) error {
	if receipt.Validate() != nil || receipt.PurgeGeneration <= s.purgeHighWater[receipt.SubjectPersonID] {
		return ErrSTRIDENetworkShadowConflict
	}
	if record, ok := s.records[receipt.SubjectPersonID]; ok {
		publication := referenceFromHeader(record.admission.Publication.Header)
		if publication.Revision > s.publicationFence[publication.ID].reference.Revision {
			s.publicationFence[publication.ID] = strideNetworkShadowFencedAuthority{subjectPersonID: receipt.SubjectPersonID, reference: publication}
		}
		for _, attestation := range record.admission.Attestations {
			attestationRef := referenceFromHeader(attestation.Header)
			if attestationRef.Revision > s.attestationFence[attestationRef.ID].reference.Revision {
				s.attestationFence[attestationRef.ID] = strideNetworkShadowFencedAuthority{subjectPersonID: receipt.SubjectPersonID, reference: attestationRef}
			}
		}
	}
	delete(s.records, receipt.SubjectPersonID)
	s.purgeHighWater[receipt.SubjectPersonID] = receipt.PurgeGeneration
	s.purges[receipt.Header.ID] = cloneContract(receipt)
	s.rebuildIndexLocked()
	s.revision++
	s.indexedRevision = s.revision
	return nil
}

func newSTRIDENetworkShadowPurgeWork(receipt DerivedPurgeReceipt) STRIDENetworkShadowPurgeWork {
	work := STRIDENetworkShadowPurgeWork{Receipt: cloneContract(receipt), State: strideNetworkShadowPurgeQueued, Version: 1, UpdatedAt: receipt.RecordedAt.UTC()}
	if receipt.State == "completed" {
		work.State = strideNetworkShadowPurgeCompleted
	} else if receipt.State == "failed_escalated" {
		work.State = strideNetworkShadowPurgeFailed
		work.Escalated = true
		work.FailureDigest = sha256Hex([]byte("stride-network-shadow-restored-failure\x00" + receipt.Header.ID))
		work.EscalationDigest = sha256Hex([]byte("stride-network-shadow-restored-escalation\x00" + work.FailureDigest))
	}
	return work
}

func sameSTRIDENetworkShadowPurgeIdentity(left, right DerivedPurgeReceipt) bool {
	return left.Header.ID == right.Header.ID && left.Header.ContentDigest == right.Header.ContentDigest && left.SubjectPersonID == right.SubjectPersonID && left.Trigger == right.Trigger && left.PurgeGeneration == right.PurgeGeneration && left.AffectedFieldsDigest == right.AffectedFieldsDigest && left.EligibilityFencedAt.Equal(right.EligibilityFencedAt)
}

func validSTRIDENetworkShadowPurgeWork(work STRIDENetworkShadowPurgeWork) bool {
	if work.Receipt.Validate() != nil || work.Version == 0 || work.UpdatedAt.IsZero() || !oneOf(work.State, strideNetworkShadowPurgeQueued, strideNetworkShadowPurgeRunning, strideNetworkShadowPurgeCompleted, strideNetworkShadowPurgeFailed) {
		return false
	}
	if work.State == strideNetworkShadowPurgeRunning {
		if !oneOf(work.ActiveStore, contributionPurgeStores...) {
			return false
		}
	} else if work.ActiveStore != "" {
		return false
	}
	if work.State == strideNetworkShadowPurgeCompleted && work.Receipt.State != "completed" || work.State != strideNetworkShadowPurgeCompleted && work.Receipt.State == "completed" {
		return false
	}
	if work.Escalated != (work.Receipt.State == "failed_escalated") || work.Escalated != (work.EscalationDigest != "") || work.FailureDigest != "" && !isHexDigest(work.FailureDigest) || work.EscalationDigest != "" && !isHexDigest(work.EscalationDigest) {
		return false
	}
	return true
}

func (s *STRIDENetworkShadowService) persistNewPurgeWork(ctx context.Context, receipt DerivedPurgeReceipt) error {
	work := newSTRIDENetworkShadowPurgeWork(receipt)
	created, err := s.config.PurgeReceipts.CreateSTRIDENetworkShadowPurgeWork(ctx, work)
	if err != nil {
		return err
	}
	if created {
		return nil
	}
	existing, found, err := s.config.PurgeReceipts.GetSTRIDENetworkShadowPurgeWork(ctx, receipt.Header.ID)
	if err != nil || !found || !validSTRIDENetworkShadowPurgeWork(existing) || !sameSTRIDENetworkShadowPurgeIdentity(existing.Receipt, receipt) {
		return ErrSTRIDENetworkShadowConflict
	}
	return nil
}

// ProcessPurgeWork executes at most one exact store attempt. Callers schedule
// it asynchronously; the service never starts goroutines or provider work on
// construction. Durable CAS makes concurrent workers and restarts idempotent.
func (s *STRIDENetworkShadowService) ProcessPurgeWork(ctx context.Context, now time.Time) (STRIDENetworkShadowPurgeWork, bool, error) {
	if s == nil || !s.config.Enabled {
		return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowDisabled
	}
	if ctx == nil || now.IsZero() || !s.purgeWorkerConfigured() {
		return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowInvalid
	}
	s.w6HealthMu.Lock()
	defer s.w6HealthMu.Unlock()
	works, err := s.config.PurgeReceipts.ListSTRIDENetworkShadowPurgeWork(ctx)
	if err != nil {
		return STRIDENetworkShadowPurgeWork{}, false, err
	}
	sort.Slice(works, func(i, j int) bool { return works[i].Receipt.Header.ID < works[j].Receipt.Header.ID })
	for _, work := range works {
		if !validSTRIDENetworkShadowPurgeWork(work) {
			return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowInvalid
		}
		if work.State == strideNetworkShadowPurgeCompleted || work.Escalated {
			continue
		}
		return s.processOnePurgeStore(ctx, work, now.UTC())
	}
	return STRIDENetworkShadowPurgeWork{}, false, nil
}

func (s *STRIDENetworkShadowService) processOnePurgeStore(ctx context.Context, work STRIDENetworkShadowPurgeWork, now time.Time) (STRIDENetworkShadowPurgeWork, bool, error) {
	s.mu.RLock()
	local, exists := s.purges[work.Receipt.Header.ID]
	s.mu.RUnlock()
	if !exists || !sameSTRIDENetworkShadowPurgeIdentity(local, work.Receipt) {
		return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowConflict
	}
	storeIndex := -1
	for index := range work.Receipt.Stores {
		if work.Receipt.Stores[index].State != "completed" {
			storeIndex = index
			break
		}
	}
	if storeIndex < 0 {
		return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowInvalid
	}
	if work.State == strideNetworkShadowPurgeRunning || work.State == strideNetworkShadowPurgeFailed {
		work.Receipt.Stores[storeIndex].AttemptCount++
	}
	if work.Receipt.Stores[storeIndex].AttemptCount > s.purgeMaxAttempts() {
		return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowInvalid
	}
	claimed := cloneContract(work)
	claimed.State = strideNetworkShadowPurgeRunning
	claimed.ActiveStore = claimed.Receipt.Stores[storeIndex].Store
	claimed.FailureDigest = ""
	claimed.Version++
	claimed.UpdatedAt = now
	swapped, err := s.config.PurgeReceipts.CompareAndSwapSTRIDENetworkShadowPurgeWork(ctx, work.Version, claimed)
	if err != nil {
		return STRIDENetworkShadowPurgeWork{}, false, err
	}
	if !swapped {
		return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowConflict
	}

	result := cloneContract(claimed)
	executeErr := s.config.PurgeExecutor.PurgeSTRIDENetworkShadowStore(ctx, cloneContract(claimed.Receipt), claimed.ActiveStore)
	result.ActiveStore = ""
	result.Version++
	result.UpdatedAt = now
	if executeErr == nil {
		completedAt := now
		result.Receipt.Stores[storeIndex].State = "completed"
		result.Receipt.Stores[storeIndex].CompletedAt = &completedAt
		allComplete := true
		for _, store := range result.Receipt.Stores {
			allComplete = allComplete && store.State == "completed"
		}
		if allComplete {
			result.State = strideNetworkShadowPurgeCompleted
			result.Receipt.State = "completed"
		} else {
			result.State = strideNetworkShadowPurgeQueued
		}
	} else {
		failure := sha256Hex([]byte(fmt.Sprintf("stride-network-shadow-purge-failure\x00%s\x00%d\x00%s\x00%d\x00%s", result.Receipt.Header.ID, result.Receipt.PurgeGeneration, claimed.ActiveStore, result.Receipt.Stores[storeIndex].AttemptCount, executeErr.Error())))
		result.State = strideNetworkShadowPurgeFailed
		result.FailureDigest = failure
		if result.Receipt.Stores[storeIndex].AttemptCount >= s.purgeMaxAttempts() {
			result.Receipt.Stores[storeIndex].State = "failed_escalated"
			result.Receipt.State = "failed_escalated"
			result.Escalated = true
			result.EscalationDigest = sha256Hex([]byte("stride-network-shadow-purge-escalation\x00" + failure))
		}
	}
	if !validSTRIDENetworkShadowPurgeWork(result) {
		return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowInvalid
	}
	swapped, err = s.config.PurgeReceipts.CompareAndSwapSTRIDENetworkShadowPurgeWork(ctx, claimed.Version, result)
	if err != nil {
		return STRIDENetworkShadowPurgeWork{}, false, err
	}
	if !swapped {
		return STRIDENetworkShadowPurgeWork{}, false, ErrSTRIDENetworkShadowConflict
	}
	s.mu.Lock()
	s.purges[result.Receipt.Header.ID] = cloneContract(result.Receipt)
	s.revision++
	s.indexedRevision = s.revision
	s.mu.Unlock()
	return cloneContract(result), true, executeErr
}

func shadowTriggerMatches(admission STRIDENetworkShadowAdmission, trigger STRIDEReference) bool {
	if trigger.ID == admission.Publication.Header.ID && trigger.ContractType == STRIDEContractPublishedContributionClaim && trigger.Revision >= admission.Publication.Header.Revision {
		return true
	}
	for _, attestation := range admission.Attestations {
		if trigger.ID == attestation.Header.ID && trigger.ContractType == STRIDEContractContributionAttestation && trigger.Revision >= attestation.Header.Revision {
			return true
		}
	}
	return false
}

func (s *STRIDENetworkShadowService) Snapshot() (STRIDENetworkShadowSnapshot, error) {
	if s == nil || !s.config.Enabled {
		return STRIDENetworkShadowSnapshot{}, ErrSTRIDENetworkShadowDisabled
	}
	if s.config.SnapshotKeys == nil {
		return STRIDENetworkShadowSnapshot{}, ErrSTRIDENetworkShadowInvalid
	}
	key, err := s.config.SnapshotKeys.CurrentSTRIDENetworkShadowSnapshotKey()
	if err != nil || !validSTRIDENetworkShadowSnapshotKey(key) || key.Version < s.config.MinimumSnapshotKeyVersion {
		return STRIDENetworkShadowSnapshot{}, ErrSTRIDENetworkShadowInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	generation := uint64(s.revision) + 1
	if generation < s.config.MinimumSnapshotGeneration {
		return STRIDENetworkShadowSnapshot{}, ErrSTRIDENetworkShadowInvalid
	}
	snapshot := STRIDENetworkShadowSnapshot{KeyID: key.KeyID, KeyVersion: key.Version, Generation: generation, SearchOrganizationID: s.config.SearchOrganizationID, Revision: s.revision, IndexedRevision: s.indexedRevision}
	people := make([]string, 0, len(s.records))
	for personID := range s.records {
		people = append(people, personID)
	}
	sort.Strings(people)
	for _, personID := range people {
		record := s.records[personID]
		snapshot.Records = append(snapshot.Records, STRIDENetworkShadowSnapshotRecord{Admission: cloneContract(record.admission), Comparison: record.comparison})
	}
	purgeIDs := make([]string, 0, len(s.purges))
	for id := range s.purges {
		purgeIDs = append(purgeIDs, id)
	}
	sort.Strings(purgeIDs)
	for _, id := range purgeIDs {
		snapshot.Purges = append(snapshot.Purges, cloneContract(s.purges[id]))
	}
	highPeople := make([]string, 0, len(s.purgeHighWater))
	for personID := range s.purgeHighWater {
		highPeople = append(highPeople, personID)
	}
	sort.Strings(highPeople)
	for _, personID := range highPeople {
		snapshot.PurgeHighWaters = append(snapshot.PurgeHighWaters, STRIDENetworkShadowPurgeHighWater{personID, s.purgeHighWater[personID]})
	}
	snapshot.PublicationFences = snapshotAuthorityHighWaters(s.publicationFence)
	snapshot.AttestationFences = snapshotAuthorityHighWaters(s.attestationFence)
	snapshot.Digest = shadowSnapshotDigest(snapshot)
	snapshot.Signature, err = signSTRIDENetworkShadowSnapshot(key, snapshot.Generation, snapshot.Digest)
	if err != nil {
		return STRIDENetworkShadowSnapshot{}, ErrSTRIDENetworkShadowInvalid
	}
	return snapshot, nil
}

func snapshotAuthorityHighWaters(values map[string]strideNetworkShadowFencedAuthority) []STRIDENetworkShadowAuthorityHighWater {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]STRIDENetworkShadowAuthorityHighWater, 0, len(ids))
	for _, id := range ids {
		value := values[id]
		result = append(result, STRIDENetworkShadowAuthorityHighWater{SubjectPersonID: value.subjectPersonID, ID: id, ContractType: value.reference.ContractType, Revision: value.reference.Revision, Digest: value.reference.Digest})
	}
	return result
}

func shadowSnapshotDigest(snapshot STRIDENetworkShadowSnapshot) string {
	snapshot.Digest = ""
	snapshot.Signature = ""
	digest, _ := STRIDEContractDigest(snapshot)
	return digest
}

func validSTRIDENetworkShadowSnapshotKey(key STRIDENetworkShadowSnapshotKey) bool {
	return strideIdentifier(key.KeyID) && key.Version > 0 && len(key.Key) >= strideSnapshotMinimumMACKeyBytes
}

func signSTRIDENetworkShadowSnapshot(key STRIDENetworkShadowSnapshotKey, generation uint64, digest string) (string, error) {
	if !validSTRIDENetworkShadowSnapshotKey(key) || generation == 0 || !isHexDigest(digest) {
		return "", ErrSTRIDENetworkShadowInvalid
	}
	material, err := canonicalJSON(struct {
		Domain     string `json:"domain"`
		KeyID      string `json:"keyId"`
		KeyVersion uint64 `json:"keyVersion"`
		Generation uint64 `json:"generation"`
		Digest     string `json:"digest"`
	}{strideNetworkShadowSnapshotDomain, key.KeyID, key.Version, generation, digest})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key.Key)
	_, _ = mac.Write(material)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func verifySTRIDENetworkShadowSnapshot(key STRIDENetworkShadowSnapshotKey, snapshot STRIDENetworkShadowSnapshot) bool {
	if key.KeyID != snapshot.KeyID || key.Version != snapshot.KeyVersion {
		return false
	}
	want, err := signSTRIDENetworkShadowSnapshot(key, snapshot.Generation, snapshot.Digest)
	if err != nil {
		return false
	}
	wantBytes, wantErr := hex.DecodeString(want)
	gotBytes, gotErr := hex.DecodeString(snapshot.Signature)
	return wantErr == nil && gotErr == nil && hmac.Equal(wantBytes, gotBytes)
}

func RestoreSTRIDENetworkShadowService(config STRIDENetworkShadowConfig, snapshot STRIDENetworkShadowSnapshot) (*STRIDENetworkShadowService, error) {
	if !config.Enabled {
		return nil, ErrSTRIDENetworkShadowDisabled
	}
	if config.SearchOrganizationID != snapshot.SearchOrganizationID {
		return nil, ErrSTRIDENetworkShadowCrossTenant
	}
	if config.SnapshotKeys == nil || config.AuthorityResolver == nil || config.SearchAuthority == nil || config.PurgeReceipts == nil || config.PurgeExecutor == nil || config.PurgeMaxAttempts < 0 || config.PurgeMaxAttempts > 10 || snapshot.Revision < 0 || snapshot.IndexedRevision != snapshot.Revision || snapshot.Generation != uint64(snapshot.Revision)+1 || snapshot.Generation < config.MinimumSnapshotGeneration || snapshot.KeyVersion < config.MinimumSnapshotKeyVersion || !strideIdentifier(snapshot.KeyID) || !isHexDigest(snapshot.Digest) || shadowSnapshotDigest(snapshot) != snapshot.Digest {
		return nil, ErrSTRIDENetworkShadowInvalid
	}
	key, err := config.SnapshotKeys.ResolveSTRIDENetworkShadowSnapshotKey(snapshot.KeyID, snapshot.KeyVersion)
	if err != nil || !verifySTRIDENetworkShadowSnapshot(key, snapshot) {
		return nil, ErrSTRIDENetworkShadowInvalid
	}
	service := NewSTRIDENetworkShadowService(config)
	rowAuthorities := make([]STRIDENetworkShadowAuthoritySnapshot, 0, len(snapshot.Records))
	for _, item := range snapshot.Records {
		comparison, err := validateSTRIDENetworkShadowAdmission(item.Admission)
		if err != nil || comparison != item.Comparison {
			return nil, ErrSTRIDENetworkShadowInvalid
		}
		personID := item.Admission.Canonical.SubjectPersonID
		if _, exists := service.records[personID]; exists {
			return nil, ErrSTRIDENetworkShadowInvalid
		}
		service.records[personID] = strideNetworkShadowRecord{admission: cloneContract(item.Admission), comparison: comparison}
		service.updateHighWatersLocked(item.Admission)
		expectation := shadowAuthorityExpectation(item.Admission)
		current, resolveErr := config.AuthorityResolver.ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
		if resolveErr != nil || !validCurrentShadowAuthority(expectation, current) {
			return nil, ErrSTRIDENetworkShadowAuthority
		}
		rowAuthorities = append(rowAuthorities, current)
	}
	for _, high := range snapshot.PurgeHighWaters {
		if !strideIdentifier(high.SubjectPersonID) || high.Generation < 1 || service.purgeHighWater[high.SubjectPersonID] != 0 {
			return nil, ErrSTRIDENetworkShadowInvalid
		}
		service.purgeHighWater[high.SubjectPersonID] = high.Generation
	}
	if !restoreAuthorityHighWaters(service.publicationFence, snapshot.PublicationFences) || !restoreAuthorityHighWaters(service.attestationFence, snapshot.AttestationFences) {
		return nil, ErrSTRIDENetworkShadowInvalid
	}
	for _, record := range service.records {
		if err := service.checkHighWatersLocked(record.admission); err != nil {
			return nil, ErrSTRIDENetworkShadowInvalid
		}
	}
	for _, receipt := range snapshot.Purges {
		if receipt.Validate() != nil || receipt.Header.TenantID != STRIDEGlobalPersonTenant || receipt.PurgeGeneration > service.purgeHighWater[receipt.SubjectPersonID] {
			return nil, ErrSTRIDENetworkShadowInvalid
		}
		if _, exists := service.purges[receipt.Header.ID]; exists {
			return nil, ErrSTRIDENetworkShadowInvalid
		}
		service.purges[receipt.Header.ID] = cloneContract(receipt)
	}
	if err := service.reconcileDurablePurgeWork(context.Background()); err != nil {
		return nil, err
	}
	fenceExpectations, ok := restoredFenceExpectations(service.publicationFence, service.attestationFence)
	if !ok {
		return nil, ErrSTRIDENetworkShadowInvalid
	}
	fenceAuthorities := make([]STRIDENetworkShadowAuthoritySnapshot, 0, len(fenceExpectations))
	for _, expectation := range fenceExpectations {
		current, resolveErr := config.AuthorityResolver.ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
		if resolveErr != nil || !validFencedShadowAuthority(expectation, current) {
			return nil, ErrSTRIDENetworkShadowAuthority
		}
		fenceAuthorities = append(fenceAuthorities, current)
	}
	authorities := append(rowAuthorities, fenceAuthorities...)
	if err := config.AuthorityResolver.WithCurrentSTRIDENetworkShadowAuthorities(authorities, func() error {
		service.revision, service.indexedRevision = snapshot.Revision, snapshot.IndexedRevision
		service.rebuildIndexLocked()
		return nil
	}); err != nil {
		return nil, ErrSTRIDENetworkShadowAuthority
	}
	return service, nil
}

func (s *STRIDENetworkShadowService) reconcileDurablePurgeWork(ctx context.Context) error {
	works, err := s.config.PurgeReceipts.ListSTRIDENetworkShadowPurgeWork(ctx)
	if err != nil {
		return ErrSTRIDENetworkShadowInvalid
	}
	seen := make(map[string]bool, len(works))
	for _, work := range works {
		if !validSTRIDENetworkShadowPurgeWork(work) || seen[work.Receipt.Header.ID] {
			return ErrSTRIDENetworkShadowInvalid
		}
		seen[work.Receipt.Header.ID] = true
		receipt, exists := s.purges[work.Receipt.Header.ID]
		if !exists || !sameSTRIDENetworkShadowPurgeIdentity(receipt, work.Receipt) || work.Receipt.PurgeGeneration > s.purgeHighWater[work.Receipt.SubjectPersonID] {
			return ErrSTRIDENetworkShadowConflict
		}
		s.purges[work.Receipt.Header.ID] = cloneContract(work.Receipt)
	}
	for id, receipt := range s.purges {
		if seen[id] {
			continue
		}
		if err := s.persistNewPurgeWork(ctx, receipt); err != nil {
			return ErrSTRIDENetworkShadowInvalid
		}
	}
	return nil
}

func restoreAuthorityHighWaters(target map[string]strideNetworkShadowFencedAuthority, values []STRIDENetworkShadowAuthorityHighWater) bool {
	for _, value := range values {
		ref := STRIDEReference{ID: value.ID, ContractType: value.ContractType, Revision: value.Revision, Digest: value.Digest}
		if !strideIdentifier(value.SubjectPersonID) || ref.Validate() != nil || target[value.ID].reference.Revision != 0 {
			return false
		}
		target[value.ID] = strideNetworkShadowFencedAuthority{subjectPersonID: value.SubjectPersonID, reference: ref}
	}
	return true
}

func restoredFenceExpectations(publications, attestations map[string]strideNetworkShadowFencedAuthority) ([]STRIDENetworkShadowAuthorityExpectation, bool) {
	byPerson := map[string]*STRIDENetworkShadowAuthorityExpectation{}
	for _, fenced := range publications {
		expectation := byPerson[fenced.subjectPersonID]
		if expectation == nil {
			expectation = &STRIDENetworkShadowAuthorityExpectation{SubjectPersonID: fenced.subjectPersonID}
			byPerson[fenced.subjectPersonID] = expectation
		}
		if expectation.Publication.Revision != 0 {
			return nil, false
		}
		expectation.Publication = fenced.reference
	}
	for _, fenced := range attestations {
		expectation := byPerson[fenced.subjectPersonID]
		if expectation == nil {
			return nil, false
		}
		expectation.Attestations = append(expectation.Attestations, fenced.reference)
	}
	people := make([]string, 0, len(byPerson))
	for personID := range byPerson {
		people = append(people, personID)
	}
	sort.Strings(people)
	result := make([]STRIDENetworkShadowAuthorityExpectation, 0, len(people))
	for _, personID := range people {
		expectation := byPerson[personID]
		if expectation.Publication.Revision == 0 {
			return nil, false
		}
		sort.Slice(expectation.Attestations, func(i, j int) bool { return expectation.Attestations[i].ID < expectation.Attestations[j].ID })
		result = append(result, *expectation)
	}
	return result, true
}

func validFencedShadowAuthority(expectation STRIDENetworkShadowAuthorityExpectation, snapshot STRIDENetworkShadowAuthoritySnapshot) bool {
	if snapshot.Generation == 0 || snapshot.SubjectPersonID != expectation.SubjectPersonID || snapshot.Publication.ID != expectation.Publication.ID || snapshot.Publication.ContractType != expectation.Publication.ContractType || snapshot.Publication.Revision < expectation.Publication.Revision || snapshot.Publication.Revision == expectation.Publication.Revision && snapshot.Publication.Digest != expectation.Publication.Digest || !oneOf(snapshot.PublicationState, "published", "superseded", "withdrawn") || snapshot.PublicationState == "published" && snapshot.PublicationVisibility != "signed_in_network" || snapshot.PublicationState != "published" && snapshot.PublicationVisibility != "private" {
		return false
	}
	actual := make(map[string]STRIDENetworkShadowAttestationAuthority, len(snapshot.Attestations))
	for _, authority := range snapshot.Attestations {
		if _, duplicate := actual[authority.Reference.ID]; duplicate {
			return false
		}
		actual[authority.Reference.ID] = authority
	}
	for _, expected := range expectation.Attestations {
		authority, exists := actual[expected.ID]
		if !exists || authority.Reference.ContractType != expected.ContractType || authority.Reference.Revision < expected.Revision || authority.Reference.Revision == expected.Revision && authority.Reference.Digest != expected.Digest || !oneOf(authority.State, "active", "superseded", "revoked") {
			return false
		}
	}
	return true
}
