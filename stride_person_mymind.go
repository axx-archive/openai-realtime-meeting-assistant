package main

// This file establishes the E10-R3 person/MyMind authority boundary. It is a
// closed, body-free domain layer: no HTTP route, worker, provider, or existing
// fixed-user path consumes it, and the associated STRIDE feature remains
// activation-fenced. The in-memory service is the deterministic policy adapter
// used to prove the contract before a later PostgreSQL writer is activated.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrMyMindInvalid                 = errors.New("invalid person or MyMind contract")
	ErrMyMindDenied                  = errors.New("MyMind authority denied")
	ErrMyMindNotFound                = errors.New("person or MyMind object not found")
	ErrMyMindConflict                = errors.New("person or MyMind revision conflict")
	ErrMyMindFeatureDisabled         = errors.New("person/MyMind context is disabled")
	ErrMyMindExportRequired          = errors.New("explicit MyMind export receipt required")
	ErrMyMindCustodyDeletionRequired = errors.New("exact MyMind custody deletion receipt required")
)

const STRIDEGlobalPersonTenant = "global"

func myMindVaultTenant(personID string) string { return "person:" + personID }

type PersonPrincipal struct {
	Header                   STRIDEContractHeader `json:"header"`
	AccountSubjectDigest     string               `json:"accountSubjectDigest"`
	Status                   string               `json:"status"`
	RecoveryRevision         int64                `json:"recoveryRevision"`
	CustodyRevision          int64                `json:"custodyRevision"`
	CustodyDeletionReceiptID string               `json:"custodyDeletionReceiptId,omitempty"`
	DeletedAt                *time.Time           `json:"deletedAt,omitempty"`
}

func (v PersonPrincipal) Validate() error {
	if v.Header.Validate(STRIDEContractPersonPrincipal) != nil || v.Header.TenantID != STRIDEGlobalPersonTenant ||
		!isHexDigest(v.AccountSubjectDigest) || !oneOf(v.Status, "active", "recovery_pending", "deletion_pending", "deleted") ||
		v.RecoveryRevision < 1 || v.CustodyRevision < 1 ||
		(v.Status == "deleted" && (v.DeletedAt == nil || !strideIdentifier(v.CustodyDeletionReceiptID))) ||
		(v.Status != "deleted" && (v.DeletedAt != nil || v.CustodyDeletionReceiptID != "")) {
		return ErrMyMindInvalid
	}
	return nil
}

type WorkspaceMembership struct {
	Header      STRIDEContractHeader `json:"header"`
	PersonID    string               `json:"personId"`
	WorkspaceID string               `json:"workspaceId"`
	Role        string               `json:"role"`
	Status      string               `json:"status"`
	GrantedAt   time.Time            `json:"grantedAt"`
	RevokedAt   *time.Time           `json:"revokedAt,omitempty"`
}

func (v WorkspaceMembership) Validate() error {
	if v.Header.Validate(STRIDEContractWorkspaceMembership) != nil || !strideIdentifier(v.PersonID) || !strideIdentifier(v.WorkspaceID) ||
		v.Header.TenantID != v.WorkspaceID || !oneOf(v.Role, "owner", "admin", "member", "contractor", "freelance") ||
		!oneOf(v.Status, "active", "revoked", "departed") || v.GrantedAt.IsZero() ||
		(v.Status == "active") != (v.RevokedAt == nil) || (v.RevokedAt != nil && v.RevokedAt.Before(v.GrantedAt)) {
		return ErrMyMindInvalid
	}
	return nil
}

type MyMindSourceRef struct {
	PersonID        string `json:"personId"`
	SourceID        string `json:"sourceId"`
	Revision        int64  `json:"revision"`
	ConsentRevision int64  `json:"consentRevision"`
	Digest          string `json:"digest"`
}

func (v MyMindSourceRef) Validate() error {
	if !strideIdentifier(v.PersonID) || !strideIdentifier(v.SourceID) || v.Revision < 1 || v.ConsentRevision < 1 || !isHexDigest(v.Digest) {
		return ErrMyMindInvalid
	}
	return nil
}

type MyMindSource struct {
	Header           STRIDEContractHeader `json:"header"`
	PersonID         string               `json:"personId"`
	SourceKind       string               `json:"sourceKind"`
	BoundWorkspaceID string               `json:"boundWorkspaceId,omitempty"`
	Confidentiality  string               `json:"confidentiality"`
	CustodyRef       string               `json:"custodyRef"`
	AllowedPurposes  []string             `json:"allowedPurposes"`
	ConsentRevision  int64                `json:"consentRevision"`
	ConsentStatus    string               `json:"consentStatus"`
	CreatedAt        time.Time            `json:"createdAt"`
}

func (v MyMindSource) Validate() error {
	if v.Header.Validate(STRIDEContractMyMindSource) != nil || !strideIdentifier(v.PersonID) || v.Header.TenantID != myMindVaultTenant(v.PersonID) ||
		!oneOf(v.SourceKind, "private_import", "preference", "collaboration_pattern", "correction", "reflection", "portable_receipt", "public_work") ||
		!validOptionalSTRIDEID(v.BoundWorkspaceID) || !oneOf(v.Confidentiality, "private", "workspace_confidential", "portable", "public") ||
		!strideIdentifier(v.CustodyRef) || !uniqueMyMindPurposes(v.AllowedPurposes) || v.ConsentRevision < 1 ||
		!oneOf(v.ConsentStatus, "granted", "withdrawn", "deleted") || v.CreatedAt.IsZero() {
		return ErrMyMindInvalid
	}
	if v.Confidentiality == "workspace_confidential" && v.BoundWorkspaceID == "" {
		return ErrMyMindInvalid
	}
	if (v.Confidentiality == "portable" || v.Confidentiality == "public") && v.SourceKind != "portable_receipt" && v.SourceKind != "public_work" {
		return ErrMyMindInvalid
	}
	return nil
}

func (v MyMindSource) Ref() MyMindSourceRef {
	return MyMindSourceRef{PersonID: v.PersonID, SourceID: v.Header.ID, Revision: v.Header.Revision, ConsentRevision: v.ConsentRevision, Digest: v.Header.ContentDigest}
}

type MyMindDestination struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspaceId"`
	AudienceID  string `json:"audienceId"`
}

func (v MyMindDestination) Validate(personID string) error {
	if !oneOf(v.Kind, "private_person", "workspace_thread", "workspace_channel", "workspace_meeting", "public_export") ||
		!strideIdentifier(v.WorkspaceID) || !strideIdentifier(v.AudienceID) {
		return ErrMyMindInvalid
	}
	// An organization-wide destination is intentionally not representable.
	if v.AudienceID == "organization" || v.AudienceID == "workspace" || (v.Kind == "private_person" && v.AudienceID != personID) {
		return ErrMyMindInvalid
	}
	return nil
}

type MyMindDisclosureGrant struct {
	Header             STRIDEContractHeader `json:"header"`
	PersonID           string               `json:"personId"`
	MembershipID       string               `json:"membershipId"`
	MembershipRevision int64                `json:"membershipRevision"`
	Source             MyMindSourceRef      `json:"source"`
	Destination        MyMindDestination    `json:"destination"`
	Purpose            string               `json:"purpose"`
	Modes              []string             `json:"modes"`
	Status             string               `json:"status"`
	GrantedAt          time.Time            `json:"grantedAt"`
	ExpiresAt          *time.Time           `json:"expiresAt,omitempty"`
	RevokedAt          *time.Time           `json:"revokedAt,omitempty"`
}

func (v MyMindDisclosureGrant) Validate() error {
	if v.Header.Validate(STRIDEContractMyMindDisclosureGrant) != nil || !strideIdentifier(v.PersonID) || !strideIdentifier(v.MembershipID) ||
		v.MembershipRevision < 1 || v.Source.Validate() != nil || v.Source.PersonID != v.PersonID || v.Destination.Validate(v.PersonID) != nil ||
		v.Header.TenantID != v.Destination.WorkspaceID || !validMyMindPurpose(v.Purpose) || !uniqueMyMindModes(v.Modes) ||
		!oneOf(v.Status, "active", "revoked", "expired") || v.GrantedAt.IsZero() ||
		(v.Status == "active") != (v.RevokedAt == nil) || (v.ExpiresAt != nil && !v.ExpiresAt.After(v.GrantedAt)) {
		return ErrMyMindInvalid
	}
	return nil
}

type MyMindAuthority string

const (
	MyMindAuthorityAccountRecovery     MyMindAuthority = "account_recovery"
	MyMindAuthorityWorkspaceMembership MyMindAuthority = "workspace_membership"
	MyMindAuthorityCustody             MyMindAuthority = "mymind_custody"
	MyMindAuthorityOrganizationExport  MyMindAuthority = "organization_export"
	MyMindAuthorityDeparture           MyMindAuthority = "departure"
	MyMindAuthorityGlobalDelete        MyMindAuthority = "global_delete"
)

type MyMindAuthorityGrant struct {
	ID           string          `json:"id"`
	Authority    MyMindAuthority `json:"authority"`
	ControllerID string          `json:"controllerId"`
	PersonID     string          `json:"personId,omitempty"`
	WorkspaceID  string          `json:"workspaceId,omitempty"`
	GrantedAt    time.Time       `json:"grantedAt"`
	RevokedAt    *time.Time      `json:"revokedAt,omitempty"`
}

func (v MyMindAuthorityGrant) Validate() error {
	if !strideIdentifier(v.ID) || !strideIdentifier(v.ControllerID) || v.GrantedAt.IsZero() || (v.RevokedAt != nil && v.RevokedAt.Before(v.GrantedAt)) {
		return ErrMyMindInvalid
	}
	switch v.Authority {
	case MyMindAuthorityAccountRecovery, MyMindAuthorityCustody, MyMindAuthorityGlobalDelete:
		if !strideIdentifier(v.PersonID) || v.WorkspaceID != "" {
			return ErrMyMindInvalid
		}
	case MyMindAuthorityWorkspaceMembership, MyMindAuthorityOrganizationExport, MyMindAuthorityDeparture:
		if !strideIdentifier(v.WorkspaceID) || v.PersonID != "" {
			return ErrMyMindInvalid
		}
	default:
		return ErrMyMindInvalid
	}
	return nil
}

type MyMindAuthorityAssertion struct {
	GrantID      string
	ControllerID string
}

type MyMindAssembleRequest struct {
	PersonID           string
	MembershipID       string
	MembershipRevision int64
	WorkspaceID        string
	Destination        MyMindDestination
	Purpose            string
	Modes              []string
	Candidates         []MyMindSourceRef
	At                 time.Time
}

type MyMindContextSelection struct {
	Sources        []MyMindSourceRef `json:"sources"`
	Purpose        string            `json:"purpose"`
	Destination    MyMindDestination `json:"destination"`
	CandidateCount int               `json:"candidateCount"`
	ExcludedCount  int               `json:"excludedCount"`
}

// MyMindPrivateAuthority binds a private custody operation to the canonical
// W1 organization membership and active-session revisions. It deliberately
// does not accept WorkspaceMembership: the legacy E10-R3 membership proof is
// not an authority source for W5 custody.
type MyMindPrivateAuthority struct {
	PersonID             string
	OrganizationID       string
	MembershipID         string
	MembershipRevision   int64
	SessionSubjectDigest string
	SessionRevision      int64
	At                   time.Time
}

func ResolveMyMindPrivateAuthority(person PersonPrincipal, membership OrganizationMembership, session ActiveOrganizationSession, at time.Time) (MyMindPrivateAuthority, error) {
	if person.Validate() != nil || person.Status != "active" || membership.Validate() != nil || session.Validate() != nil || at.IsZero() || membership.Status != "active" || session.Status != "active" ||
		membership.PersonID != person.Header.ID ||
		at.Before(membership.GrantedAt) || at.Before(session.BoundAt) || !at.Before(session.ExpiresAt) || session.PersonID != membership.PersonID || session.OrganizationID != membership.OrganizationID ||
		session.MembershipID != membership.Header.ID || session.MembershipRevision != membership.Header.Revision {
		return MyMindPrivateAuthority{}, ErrMyMindDenied
	}
	return MyMindPrivateAuthority{
		PersonID: membership.PersonID, OrganizationID: membership.OrganizationID, MembershipID: membership.Header.ID,
		MembershipRevision: membership.Header.Revision, SessionSubjectDigest: session.SessionSubjectDigest,
		SessionRevision: session.SessionRevision, At: at.UTC(),
	}, nil
}

type MyMindExportReceipt struct {
	ID                string            `json:"id"`
	PersonID          string            `json:"personId"`
	WorkspaceID       string            `json:"workspaceId"`
	Sources           []MyMindSourceRef `json:"sources"`
	OrganizationGrant string            `json:"organizationGrant"`
	CustodyGrant      string            `json:"custodyGrant"`
	CreatedAt         time.Time         `json:"createdAt"`
}

// MyMindCustodyDeletionEffect is an externally observed destruction result.
// It never performs deletion and carries no body or key material; a future KMS
// adapter must produce this evidence for the exact current custody revision.
type MyMindCustodyDeletionEffect struct {
	Source     MyMindSourceRef `json:"source"`
	CustodyRef string          `json:"custodyRef"`
	Effect     string          `json:"effect"`
	DeletedAt  time.Time       `json:"deletedAt"`
}

func (v MyMindCustodyDeletionEffect) Validate(personID string) error {
	if v.Source.Validate() != nil || v.Source.PersonID != personID || !strideIdentifier(v.CustodyRef) ||
		v.Effect != "body_destroyed" || v.DeletedAt.IsZero() {
		return ErrMyMindInvalid
	}
	return nil
}

type MyMindCustodyDeletionReceipt struct {
	Header                 STRIDEContractHeader          `json:"header"`
	PersonID               string                        `json:"personId"`
	AuthorityGrantID       string                        `json:"authorityGrantId"`
	SourceCount            int                           `json:"sourceCount"`
	SourceManifestDigest   string                        `json:"sourceManifestDigest"`
	ExternalEvidenceDigest string                        `json:"externalEvidenceDigest"`
	Effects                []MyMindCustodyDeletionEffect `json:"effects"`
	RecordedAt             time.Time                     `json:"recordedAt"`
}

func (v MyMindCustodyDeletionReceipt) Validate() error {
	if v.Header.Validate(STRIDEContractMyMindCustodyDeletion) != nil || !strideIdentifier(v.PersonID) ||
		v.Header.TenantID != myMindVaultTenant(v.PersonID) || !strideIdentifier(v.AuthorityGrantID) ||
		v.SourceCount != len(v.Effects) || v.SourceCount < 0 || !isHexDigest(v.SourceManifestDigest) ||
		v.Header.ContentDigest != v.SourceManifestDigest || !isHexDigest(v.ExternalEvidenceDigest) || v.RecordedAt.IsZero() {
		return ErrMyMindInvalid
	}
	seen := make(map[string]bool, len(v.Effects))
	for _, effect := range v.Effects {
		if effect.Validate(v.PersonID) != nil || seen[effect.Source.SourceID] || effect.DeletedAt.After(v.RecordedAt) {
			return ErrMyMindInvalid
		}
		seen[effect.Source.SourceID] = true
	}
	digest, err := myMindCustodyDeletionManifestDigest(v.Effects)
	if err != nil || digest != v.SourceManifestDigest {
		return ErrMyMindInvalid
	}
	return nil
}

// PersonMyMindService owns only body-free authority metadata. Retrieval is
// candidate-bound, so a workspace caller cannot use it to enumerate another
// person's memberships or private source graph.
type PersonMyMindService struct {
	mu          sync.RWMutex
	persons     map[string]PersonPrincipal
	memberships map[string]WorkspaceMembership
	sources     map[string]MyMindSource
	grants      map[string]MyMindDisclosureGrant
	authorities map[string]MyMindAuthorityGrant
	exports     map[string]MyMindExportReceipt
	deletions   map[string]MyMindCustodyDeletionReceipt
}

func NewPersonMyMindService() *PersonMyMindService {
	return &PersonMyMindService{
		persons: make(map[string]PersonPrincipal), memberships: make(map[string]WorkspaceMembership), sources: make(map[string]MyMindSource),
		grants: make(map[string]MyMindDisclosureGrant), authorities: make(map[string]MyMindAuthorityGrant), exports: make(map[string]MyMindExportReceipt),
		deletions: make(map[string]MyMindCustodyDeletionReceipt),
	}
}

func (s *PersonMyMindService) PutPerson(person PersonPrincipal) error {
	if s == nil || person.Validate() != nil || person.Status != "active" {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Person installation is bootstrap-only. Recovery and deletion have
	// separate authority-checked transitions below and cannot be bypassed by a
	// higher-revision replacement.
	if _, ok := s.persons[person.Header.ID]; ok {
		return ErrMyMindConflict
	}
	for _, existing := range s.persons {
		if existing.AccountSubjectDigest == person.AccountSubjectDigest {
			return ErrMyMindConflict
		}
	}
	s.persons[person.Header.ID] = clonePersonPrincipal(person)
	return nil
}

// InstallAuthority is the migration/bootstrap writer seam. It cannot replace
// an authority grant and does not grant any context access by itself.
func (s *PersonMyMindService) InstallAuthority(grant MyMindAuthorityGrant) error {
	if s == nil || grant.Validate() != nil {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.authorities[grant.ID]; exists {
		return ErrMyMindConflict
	}
	s.authorities[grant.ID] = cloneMyMindAuthorityGrant(grant)
	return nil
}

func (s *PersonMyMindService) PutMembership(assertion MyMindAuthorityAssertion, membership WorkspaceMembership) error {
	if s == nil || membership.Validate() != nil {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorizeLocked(assertion, MyMindAuthorityWorkspaceMembership, "", membership.WorkspaceID, membership.Header.CreatedAt) != nil {
		return ErrMyMindDenied
	}
	person, ok := s.persons[membership.PersonID]
	if !ok || person.Status == "deleted" {
		return ErrMyMindNotFound
	}
	if prior, ok := s.memberships[membership.Header.ID]; ok {
		if membership.Header.Revision <= prior.Header.Revision || prior.Status != "active" || membership.PersonID != prior.PersonID ||
			membership.WorkspaceID != prior.WorkspaceID || !membership.GrantedAt.Equal(prior.GrantedAt) {
			return ErrMyMindConflict
		}
	}
	for id, active := range s.memberships {
		if id != membership.Header.ID && active.PersonID == membership.PersonID && active.WorkspaceID == membership.WorkspaceID && active.Status == "active" && membership.Status == "active" {
			return ErrMyMindConflict
		}
	}
	s.memberships[membership.Header.ID] = cloneWorkspaceMembership(membership)
	return nil
}

func (s *PersonMyMindService) PutSource(assertion MyMindAuthorityAssertion, source MyMindSource) error {
	if s == nil || source.Validate() != nil {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorizeLocked(assertion, MyMindAuthorityCustody, source.PersonID, "", source.CreatedAt) != nil {
		return ErrMyMindDenied
	}
	person, ok := s.persons[source.PersonID]
	if !ok || person.Status == "deleted" {
		return ErrMyMindNotFound
	}
	key := myMindSourceKey(source.PersonID, source.Header.ID)
	if prior, ok := s.sources[key]; ok {
		if source.Header.Revision <= prior.Header.Revision || source.ConsentRevision < prior.ConsentRevision || source.PersonID != prior.PersonID ||
			source.SourceKind != prior.SourceKind || source.BoundWorkspaceID != prior.BoundWorkspaceID || source.Confidentiality != prior.Confidentiality || source.CustodyRef != prior.CustodyRef {
			return ErrMyMindConflict
		}
	}
	s.sources[key] = cloneMyMindSource(source)
	return nil
}

func (s *PersonMyMindService) PutDisclosure(assertion MyMindAuthorityAssertion, grant MyMindDisclosureGrant) error {
	if s == nil || grant.Validate() != nil {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorizeLocked(assertion, MyMindAuthorityCustody, grant.PersonID, "", grant.GrantedAt) != nil {
		return ErrMyMindDenied
	}
	membership, ok := s.memberships[grant.MembershipID]
	if !ok || membership.PersonID != grant.PersonID || membership.WorkspaceID != grant.Destination.WorkspaceID || membership.Status != "active" || membership.Header.Revision != grant.MembershipRevision {
		return ErrMyMindDenied
	}
	source, ok := s.sources[myMindSourceKey(grant.PersonID, grant.Source.SourceID)]
	if !ok || source.Ref() != grant.Source || source.ConsentStatus != "granted" || !containsMyMindPurpose(source.AllowedPurposes, grant.Purpose) ||
		(source.BoundWorkspaceID != "" && source.BoundWorkspaceID != grant.Destination.WorkspaceID) {
		return ErrMyMindDenied
	}
	if grant.Status != "active" {
		return ErrMyMindInvalid
	}
	if _, ok := s.grants[grant.Header.ID]; ok {
		return ErrMyMindConflict
	}
	for _, existing := range s.grants {
		if existing.Status == "active" && existing.PersonID == grant.PersonID && existing.MembershipID == grant.MembershipID &&
			existing.Source == grant.Source && existing.Destination == grant.Destination && existing.Purpose == grant.Purpose {
			return ErrMyMindConflict
		}
	}
	s.grants[grant.Header.ID] = cloneMyMindDisclosureGrant(grant)
	return nil
}

func (s *PersonMyMindService) RevokeDisclosure(assertion MyMindAuthorityAssertion, grantID string, at time.Time) error {
	if s == nil || !strideIdentifier(grantID) || at.IsZero() {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.grants[grantID]
	if !ok {
		return ErrMyMindNotFound
	}
	if s.authorizeLocked(assertion, MyMindAuthorityCustody, grant.PersonID, "", at) != nil {
		// A workspace may narrow a disclosure into that workspace, but this
		// authority is never accepted by PutDisclosure and therefore cannot
		// widen or originate a person's MyMind grant.
		if s.authorizeLocked(assertion, MyMindAuthorityWorkspaceMembership, "", grant.Destination.WorkspaceID, at) != nil {
			return ErrMyMindDenied
		}
	}
	if grant.Status == "revoked" {
		return nil
	}
	grant.Status, grant.RevokedAt = "revoked", myMindTimePtr(at.UTC())
	grant.Header.Revision++
	grant.Header.ContentDigest = nextMyMindDigest(grant.Header.ContentDigest, "revoked")
	grant.Header.CreatedAt = at.UTC()
	s.grants[grantID] = grant
	return nil
}

func (s *PersonMyMindService) Assemble(request MyMindAssembleRequest) (MyMindContextSelection, error) {
	if s == nil || !strideIdentifier(request.PersonID) || !strideIdentifier(request.MembershipID) || request.MembershipRevision < 1 ||
		!strideIdentifier(request.WorkspaceID) || request.Destination.WorkspaceID != request.WorkspaceID || request.Destination.Validate(request.PersonID) != nil ||
		!validMyMindPurpose(request.Purpose) || !uniqueMyMindModes(request.Modes) || len(request.Candidates) == 0 || request.At.IsZero() {
		return MyMindContextSelection{}, ErrMyMindInvalid
	}
	seen := make(map[string]bool, len(request.Candidates))
	for _, candidate := range request.Candidates {
		if candidate.Validate() != nil || candidate.PersonID != request.PersonID || seen[candidate.SourceID] {
			return MyMindContextSelection{}, ErrMyMindInvalid
		}
		seen[candidate.SourceID] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	person, ok := s.persons[request.PersonID]
	if !ok || person.Status != "active" {
		return MyMindContextSelection{}, ErrMyMindDenied
	}
	membership, ok := s.memberships[request.MembershipID]
	if !ok || membership.PersonID != request.PersonID || membership.WorkspaceID != request.WorkspaceID || membership.Status != "active" || membership.Header.Revision != request.MembershipRevision {
		return MyMindContextSelection{}, ErrMyMindDenied
	}
	if request.At.Before(membership.GrantedAt) {
		return MyMindContextSelection{}, ErrMyMindDenied
	}
	selection := MyMindContextSelection{Purpose: request.Purpose, Destination: request.Destination, CandidateCount: len(request.Candidates)}
	for _, candidate := range request.Candidates {
		source, exists := s.sources[myMindSourceKey(request.PersonID, candidate.SourceID)]
		if !exists || source.Ref() != candidate || source.ConsentStatus != "granted" || request.At.Before(source.CreatedAt) || !containsMyMindPurpose(source.AllowedPurposes, request.Purpose) ||
			(source.BoundWorkspaceID != "" && source.BoundWorkspaceID != request.WorkspaceID) {
			selection.ExcludedCount++
			continue
		}
		if request.Destination.Kind == "private_person" && onlyMyMindMode(request.Modes, "personalize") {
			selection.Sources = append(selection.Sources, candidate)
			continue
		}
		if !s.hasDisclosureLocked(request, candidate) {
			selection.ExcludedCount++
			continue
		}
		selection.Sources = append(selection.Sources, candidate)
	}
	sort.Slice(selection.Sources, func(i, j int) bool { return selection.Sources[i].SourceID < selection.Sources[j].SourceID })
	return selection, nil
}

func (s *PersonMyMindService) RecoverAccount(assertion MyMindAuthorityAssertion, personID, accountSubjectDigest string, at time.Time) (PersonPrincipal, error) {
	if s == nil || !strideIdentifier(personID) || !isHexDigest(accountSubjectDigest) || at.IsZero() {
		return PersonPrincipal{}, ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorizeLocked(assertion, MyMindAuthorityAccountRecovery, personID, "", at) != nil {
		return PersonPrincipal{}, ErrMyMindDenied
	}
	person, ok := s.persons[personID]
	if !ok || person.Status == "deleted" {
		return PersonPrincipal{}, ErrMyMindNotFound
	}
	for id, existing := range s.persons {
		if id != personID && existing.AccountSubjectDigest == accountSubjectDigest {
			return PersonPrincipal{}, ErrMyMindConflict
		}
	}
	person.AccountSubjectDigest = accountSubjectDigest
	person.RecoveryRevision++
	person.Header.Revision++
	person.Header.ContentDigest = nextMyMindDigest(person.Header.ContentDigest, "recovered:"+accountSubjectDigest)
	person.Header.CreatedAt = at.UTC()
	s.persons[personID] = person
	return clonePersonPrincipal(person), nil
}

func (s *PersonMyMindService) Depart(assertion MyMindAuthorityAssertion, membershipID string, at time.Time) error {
	if s == nil || !strideIdentifier(membershipID) || at.IsZero() {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	membership, ok := s.memberships[membershipID]
	if !ok {
		return ErrMyMindNotFound
	}
	if s.authorizeLocked(assertion, MyMindAuthorityDeparture, "", membership.WorkspaceID, at) != nil {
		return ErrMyMindDenied
	}
	if membership.Status != "active" {
		return nil
	}
	membership.Status, membership.RevokedAt = "departed", myMindTimePtr(at.UTC())
	membership.Header.Revision++
	membership.Header.ContentDigest = nextMyMindDigest(membership.Header.ContentDigest, "departed")
	membership.Header.CreatedAt = at.UTC()
	s.memberships[membershipID] = membership
	for id, grant := range s.grants {
		if grant.MembershipID == membershipID && grant.Status == "active" {
			grant.Status, grant.RevokedAt = "revoked", myMindTimePtr(at.UTC())
			grant.Header.Revision++
			grant.Header.ContentDigest = nextMyMindDigest(grant.Header.ContentDigest, "departure_revoked")
			grant.Header.CreatedAt = at.UTC()
			s.grants[id] = grant
		}
	}
	return nil
}

func (s *PersonMyMindService) Export(assertionOrganization, assertionCustody MyMindAuthorityAssertion, receiptID, personID, membershipID string, sources []MyMindSourceRef, at time.Time) (MyMindExportReceipt, error) {
	if s == nil || !strideIdentifier(receiptID) || !strideIdentifier(personID) || !strideIdentifier(membershipID) || len(sources) == 0 || at.IsZero() {
		return MyMindExportReceipt{}, ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	membership, ok := s.memberships[membershipID]
	if !ok || membership.PersonID != personID || membership.Status != "active" {
		return MyMindExportReceipt{}, ErrMyMindDenied
	}
	if assertionOrganization.GrantID == assertionCustody.GrantID ||
		s.authorizeLocked(assertionOrganization, MyMindAuthorityOrganizationExport, "", membership.WorkspaceID, at) != nil ||
		s.authorizeLocked(assertionCustody, MyMindAuthorityCustody, personID, "", at) != nil {
		return MyMindExportReceipt{}, ErrMyMindDenied
	}
	if _, exists := s.exports[receiptID]; exists {
		return MyMindExportReceipt{}, ErrMyMindConflict
	}
	seen := map[string]bool{}
	for _, ref := range sources {
		source, exists := s.sources[myMindSourceKey(personID, ref.SourceID)]
		if ref.Validate() != nil || ref.PersonID != personID || seen[ref.SourceID] || !exists || source.Ref() != ref || source.ConsentStatus != "granted" ||
			!oneOf(source.Confidentiality, "portable", "public") || (source.BoundWorkspaceID != "" && source.BoundWorkspaceID != membership.WorkspaceID) ||
			!s.hasExportDisclosureLocked(personID, membership, ref, receiptID, at) {
			return MyMindExportReceipt{}, ErrMyMindDenied
		}
		seen[ref.SourceID] = true
	}
	receipt := MyMindExportReceipt{ID: receiptID, PersonID: personID, WorkspaceID: membership.WorkspaceID, Sources: append([]MyMindSourceRef(nil), sources...), OrganizationGrant: assertionOrganization.GrantID, CustodyGrant: assertionCustody.GrantID, CreatedAt: at.UTC()}
	sort.Slice(receipt.Sources, func(i, j int) bool { return receipt.Sources[i].SourceID < receipt.Sources[j].SourceID })
	s.exports[receiptID] = receipt
	return cloneMyMindExportReceipt(receipt), nil
}

func (s *PersonMyMindService) hasExportDisclosureLocked(personID string, membership WorkspaceMembership, source MyMindSourceRef, receiptID string, at time.Time) bool {
	destination := MyMindDestination{Kind: "public_export", WorkspaceID: membership.WorkspaceID, AudienceID: receiptID}
	for _, grant := range s.grants {
		if grant.Status == "active" && !at.Before(grant.GrantedAt) && grant.PersonID == personID && grant.MembershipID == membership.Header.ID &&
			grant.MembershipRevision == membership.Header.Revision && grant.Source == source && grant.Destination == destination &&
			grant.Purpose == "portable_export" && containsAllMyMindModes(grant.Modes, []string{"export"}) &&
			(grant.ExpiresAt == nil || at.Before(*grant.ExpiresAt)) {
			return true
		}
	}
	return false
}

func (s *PersonMyMindService) RecordCustodyDeletion(assertion MyMindAuthorityAssertion, receipt MyMindCustodyDeletionReceipt) error {
	if s == nil || receipt.Validate() != nil || assertion.GrantID != receipt.AuthorityGrantID {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorizeLocked(assertion, MyMindAuthorityCustody, receipt.PersonID, "", receipt.RecordedAt) != nil {
		return ErrMyMindDenied
	}
	person, ok := s.persons[receipt.PersonID]
	if !ok || person.Status != "active" {
		return ErrMyMindDenied
	}
	if _, exists := s.deletions[receipt.Header.ID]; exists {
		return ErrMyMindConflict
	}
	if !s.custodyDeletionReceiptMatchesCurrentLocked(receipt) {
		return ErrMyMindCustodyDeletionRequired
	}
	s.deletions[receipt.Header.ID] = cloneMyMindCustodyDeletionReceipt(receipt)
	return nil
}

func (s *PersonMyMindService) DeleteAccount(assertionDelete, assertionCustody MyMindAuthorityAssertion, personID, exportReceiptID, custodyDeletionReceiptID string, at time.Time) error {
	if s == nil || !strideIdentifier(personID) || !validOptionalSTRIDEID(exportReceiptID) || !strideIdentifier(custodyDeletionReceiptID) || at.IsZero() {
		return ErrMyMindInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if assertionDelete.GrantID == assertionCustody.GrantID ||
		s.authorizeLocked(assertionDelete, MyMindAuthorityGlobalDelete, personID, "", at) != nil ||
		s.authorizeLocked(assertionCustody, MyMindAuthorityCustody, personID, "", at) != nil {
		return ErrMyMindDenied
	}
	if exportReceiptID != "" {
		receipt, ok := s.exports[exportReceiptID]
		if !ok || receipt.PersonID != personID {
			return ErrMyMindExportRequired
		}
	}
	person, ok := s.persons[personID]
	if !ok {
		return ErrMyMindNotFound
	}
	if person.Status == "deleted" {
		if person.CustodyDeletionReceiptID == custodyDeletionReceiptID {
			return nil
		}
		return ErrMyMindDenied
	}
	deletionReceipt, ok := s.deletions[custodyDeletionReceiptID]
	if !ok || deletionReceipt.PersonID != personID || deletionReceipt.RecordedAt.After(at) || !s.custodyDeletionReceiptMatchesCurrentLocked(deletionReceipt) {
		return ErrMyMindCustodyDeletionRequired
	}
	person.Status, person.DeletedAt = "deleted", myMindTimePtr(at.UTC())
	person.CustodyDeletionReceiptID = custodyDeletionReceiptID
	person.Header.Revision++
	person.Header.ContentDigest = nextMyMindDigest(person.Header.ContentDigest, "deleted")
	person.Header.CreatedAt = at.UTC()
	s.persons[personID] = person
	for id, membership := range s.memberships {
		if membership.PersonID == personID && membership.Status == "active" {
			membership.Status, membership.RevokedAt = "revoked", myMindTimePtr(at.UTC())
			membership.Header.Revision++
			membership.Header.ContentDigest = nextMyMindDigest(membership.Header.ContentDigest, "account_deleted")
			membership.Header.CreatedAt = at.UTC()
			s.memberships[id] = membership
		}
	}
	for id, grant := range s.grants {
		if grant.PersonID == personID && grant.Status == "active" {
			grant.Status, grant.RevokedAt = "revoked", myMindTimePtr(at.UTC())
			grant.Header.Revision++
			grant.Header.ContentDigest = nextMyMindDigest(grant.Header.ContentDigest, "account_deleted")
			grant.Header.CreatedAt = at.UTC()
			s.grants[id] = grant
		}
	}
	// Every MyMind custody body has an exact external deletion effect above.
	// Metadata remains as an attribution/tombstone pointer; organization-owned
	// shared records live outside MyMind and are not erased by this transition.
	for key, source := range s.sources {
		if source.PersonID == personID {
			source.ConsentStatus = "deleted"
			source.ConsentRevision++
			source.Header.Revision++
			source.Header.ContentDigest = nextMyMindDigest(source.Header.ContentDigest, "account_deleted")
			source.Header.CreatedAt = at.UTC()
			s.sources[key] = source
		}
	}
	return nil
}

func (s *PersonMyMindService) custodyDeletionReceiptMatchesCurrentLocked(receipt MyMindCustodyDeletionReceipt) bool {
	current := make(map[string]MyMindSource, receipt.SourceCount)
	for _, source := range s.sources {
		if source.PersonID == receipt.PersonID {
			current[source.Header.ID] = source
		}
	}
	if len(current) != receipt.SourceCount {
		return false
	}
	for _, effect := range receipt.Effects {
		source, ok := current[effect.Source.SourceID]
		if !ok || source.Ref() != effect.Source || source.CustodyRef != effect.CustodyRef || effect.Effect != "body_destroyed" {
			return false
		}
	}
	return true
}

func (s *PersonMyMindService) PersonTombstone(assertion MyMindAuthorityAssertion, personID string, at time.Time) (PersonPrincipal, error) {
	if s == nil || !strideIdentifier(personID) || at.IsZero() {
		return PersonPrincipal{}, ErrMyMindInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.authorizeLocked(assertion, MyMindAuthorityGlobalDelete, personID, "", at) != nil &&
		s.authorizeLocked(assertion, MyMindAuthorityAccountRecovery, personID, "", at) != nil {
		return PersonPrincipal{}, ErrMyMindDenied
	}
	person, ok := s.persons[personID]
	if !ok {
		return PersonPrincipal{}, ErrMyMindNotFound
	}
	return clonePersonPrincipal(person), nil
}

func (s *PersonMyMindService) authorizeLocked(assertion MyMindAuthorityAssertion, authority MyMindAuthority, personID, workspaceID string, at time.Time) error {
	grant, ok := s.authorities[assertion.GrantID]
	if !ok || grant.ControllerID != assertion.ControllerID || grant.Authority != authority || grant.RevokedAt != nil || at.Before(grant.GrantedAt) ||
		(personID != "" && grant.PersonID != personID) || (workspaceID != "" && grant.WorkspaceID != workspaceID) {
		return ErrMyMindDenied
	}
	return nil
}

func (s *PersonMyMindService) hasDisclosureLocked(request MyMindAssembleRequest, source MyMindSourceRef) bool {
	for _, grant := range s.grants {
		if grant.Status != "active" || request.At.Before(grant.GrantedAt) || grant.PersonID != request.PersonID || grant.MembershipID != request.MembershipID || grant.MembershipRevision != request.MembershipRevision ||
			grant.Source != source || grant.Destination != request.Destination || grant.Purpose != request.Purpose ||
			(grant.ExpiresAt != nil && !request.At.Before(*grant.ExpiresAt)) || !containsAllMyMindModes(grant.Modes, request.Modes) {
			continue
		}
		return true
	}
	return false
}

// FixedUserPersonCompatibilityAdapter describes the current single-workspace
// identity mapping without activating it as a MyMind context consumer.
type FixedUserPersonCompatibilityAdapter struct {
	WorkspaceID string
	Users       map[string]string
}

func (v FixedUserPersonCompatibilityAdapter) ResolveForContext(userID string) (string, string, error) {
	if !strideIdentifier(v.WorkspaceID) || strings.TrimSpace(userID) == "" || userID != strings.TrimSpace(userID) {
		return "", "", ErrMyMindInvalid
	}
	personID, ok := v.Users[userID]
	if !ok || !strideIdentifier(personID) {
		return "", "", ErrMyMindNotFound
	}
	return "", "", ErrMyMindFeatureDisabled
}

func validMyMindPurpose(value string) bool {
	return oneOf(value, "private_answer", "collaboration", "shared_answer", "meeting_support", "portable_export", "account_custody")
}

func uniqueMyMindPurposes(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !validMyMindPurpose(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func uniqueMyMindModes(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !oneOf(value, "personalize", "cite", "quote", "assert_basis", "export") || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func containsMyMindPurpose(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsAllMyMindModes(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, value := range have {
		set[value] = true
	}
	for _, value := range want {
		if !set[value] {
			return false
		}
	}
	return true
}

func onlyMyMindMode(values []string, want string) bool { return len(values) == 1 && values[0] == want }
func myMindSourceKey(personID, sourceID string) string { return personID + "\x00" + sourceID }
func myMindTimePtr(value time.Time) *time.Time         { copy := value; return &copy }
func nextMyMindDigest(prior, transition string) string {
	sum := sha256.Sum256([]byte(prior + "\x00" + transition))
	return hex.EncodeToString(sum[:])
}

func clonePersonPrincipal(value PersonPrincipal) PersonPrincipal {
	if value.DeletedAt != nil {
		value.DeletedAt = myMindTimePtr(*value.DeletedAt)
	}
	return value
}
func cloneWorkspaceMembership(value WorkspaceMembership) WorkspaceMembership {
	if value.RevokedAt != nil {
		value.RevokedAt = myMindTimePtr(*value.RevokedAt)
	}
	return value
}
func cloneMyMindSource(value MyMindSource) MyMindSource {
	value.AllowedPurposes = append([]string(nil), value.AllowedPurposes...)
	return value
}
func cloneMyMindDisclosureGrant(value MyMindDisclosureGrant) MyMindDisclosureGrant {
	value.Modes = append([]string(nil), value.Modes...)
	if value.ExpiresAt != nil {
		value.ExpiresAt = myMindTimePtr(*value.ExpiresAt)
	}
	if value.RevokedAt != nil {
		value.RevokedAt = myMindTimePtr(*value.RevokedAt)
	}
	return value
}
func cloneMyMindAuthorityGrant(value MyMindAuthorityGrant) MyMindAuthorityGrant {
	if value.RevokedAt != nil {
		value.RevokedAt = myMindTimePtr(*value.RevokedAt)
	}
	return value
}
func cloneMyMindExportReceipt(value MyMindExportReceipt) MyMindExportReceipt {
	value.Sources = append([]MyMindSourceRef(nil), value.Sources...)
	return value
}

func cloneMyMindCustodyDeletionReceipt(value MyMindCustodyDeletionReceipt) MyMindCustodyDeletionReceipt {
	value.Effects = append([]MyMindCustodyDeletionEffect(nil), value.Effects...)
	return value
}

func myMindCustodyDeletionManifestDigest(effects []MyMindCustodyDeletionEffect) (string, error) {
	ordered := append([]MyMindCustodyDeletionEffect(nil), effects...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Source.SourceID < ordered[j].Source.SourceID })
	return STRIDEContractDigest(ordered)
}
