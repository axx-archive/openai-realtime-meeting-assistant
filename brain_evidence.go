package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrBrainEvidenceInvalid   = errors.New("brain evidence is invalid")
	ErrRetrievalSnapshotStale = errors.New("retrieval snapshot is stale or unauthorized")
)

type BrainEvidenceTrust string

const (
	BrainEvidenceTrusted        BrainEvidenceTrust = "trusted_internal"
	BrainEvidenceUntrustedGuest BrainEvidenceTrust = "untrusted_guest"
)

// BrainGuestOrigin is server-derived provenance for content that originated
// with a guest. Raw guest tokens are never stored; the session key and consent
// snapshot are represented only by stable SHA-256 digests.
type BrainGuestOrigin struct {
	SessionKeyHash        string `json:"sessionKeyHash"`
	GuestLinkID           string `json:"guestLinkId"`
	RoomID                string `json:"roomId"`
	SittingID             string `json:"sittingId"`
	ConsentSnapshotDigest string `json:"consentSnapshotDigest"`
}

func (origin BrainGuestOrigin) Validate() error {
	if !isHexDigest(origin.SessionKeyHash) || strings.TrimSpace(origin.GuestLinkID) == "" ||
		strings.TrimSpace(origin.RoomID) == "" || strings.TrimSpace(origin.SittingID) == "" ||
		!isHexDigest(origin.ConsentSnapshotDigest) {
		return ErrBrainEvidenceInvalid
	}
	return nil
}

// BrainEvidenceRef is a body-free edge to one immutable primary source
// revision. At least one temporal interval or text span is required so an
// asserted claim never cites an object only at whole-document granularity.
type BrainEvidenceRef struct {
	TenantID        string             `json:"tenantId"`
	SourceFamily    string             `json:"sourceFamily"`
	ObjectID        string             `json:"objectId"`
	ContentRevision int64              `json:"contentRevision"`
	ACLVersion      int64              `json:"aclVersion"`
	ContentDigest   string             `json:"contentDigest"`
	RoomID          string             `json:"roomId,omitempty"`
	SittingID       string             `json:"sittingId,omitempty"`
	OccurredStart   time.Time          `json:"occurredStart,omitempty"`
	OccurredEnd     time.Time          `json:"occurredEnd,omitempty"`
	SpanStart       int64              `json:"spanStart,omitempty"`
	SpanEnd         int64              `json:"spanEnd,omitempty"`
	PurgeGeneration int64              `json:"purgeGeneration"`
	Trust           BrainEvidenceTrust `json:"trust"`
	GuestOrigin     *BrainGuestOrigin  `json:"guestOrigin,omitempty"`
}

func (ref BrainEvidenceRef) Validate() error {
	if strings.TrimSpace(ref.TenantID) == "" || strings.TrimSpace(ref.SourceFamily) == "" || strings.TrimSpace(ref.ObjectID) == "" ||
		ref.ContentRevision < 1 || ref.ACLVersion < 1 || !isHexDigest(ref.ContentDigest) || ref.PurgeGeneration < 0 {
		return ErrBrainEvidenceInvalid
	}
	if ref.Trust != BrainEvidenceTrusted && ref.Trust != BrainEvidenceUntrustedGuest {
		return ErrBrainEvidenceInvalid
	}
	if ref.Trust == BrainEvidenceUntrustedGuest {
		if ref.GuestOrigin == nil || ref.GuestOrigin.Validate() != nil || ref.GuestOrigin.RoomID != ref.RoomID || ref.GuestOrigin.SittingID != ref.SittingID {
			return ErrBrainEvidenceInvalid
		}
	} else if ref.GuestOrigin != nil {
		return ErrBrainEvidenceInvalid
	}
	hasTime := !ref.OccurredStart.IsZero() || !ref.OccurredEnd.IsZero()
	if hasTime && (ref.OccurredStart.IsZero() || ref.OccurredEnd.IsZero() || !ref.OccurredStart.Before(ref.OccurredEnd)) {
		return ErrBrainEvidenceInvalid
	}
	hasSpan := ref.SpanStart != 0 || ref.SpanEnd != 0
	if hasSpan && (ref.SpanStart < 0 || ref.SpanEnd <= ref.SpanStart) {
		return ErrBrainEvidenceInvalid
	}
	if !hasTime && !hasSpan {
		return ErrBrainEvidenceInvalid
	}
	return nil
}

func (ref BrainEvidenceRef) ACLRefs() (ACLObjectRef, ACLRevisionRef) {
	return ACLObjectRef{TenantID: ref.TenantID, Type: ref.SourceFamily, ID: ref.ObjectID, ACLVersion: ref.ACLVersion},
		ACLRevisionRef{ContentRevision: ref.ContentRevision, ContentDigest: ref.ContentDigest}
}

type BrainClaimStatus string

const (
	BrainClaimAsserted    BrainClaimStatus = "asserted"
	BrainClaimInferred    BrainClaimStatus = "inferred"
	BrainClaimUnsupported BrainClaimStatus = "unsupported"
	BrainClaimSuperseded  BrainClaimStatus = "superseded"
)

type BrainGenerationProvenance struct {
	Provider            string    `json:"provider"`
	Model               string    `json:"model"`
	RouteSeat           string    `json:"routeSeat"`
	ReasoningEffort     string    `json:"reasoningEffort"`
	PromptVersion       string    `json:"promptVersion"`
	RetrievalSnapshotID string    `json:"retrievalSnapshotId"`
	GeneratedAt         time.Time `json:"generatedAt"`
}

func (provenance BrainGenerationProvenance) Validate() error {
	if strings.TrimSpace(provenance.Provider) == "" || strings.TrimSpace(provenance.Model) == "" ||
		strings.TrimSpace(provenance.RouteSeat) == "" || strings.TrimSpace(provenance.ReasoningEffort) == "" ||
		strings.TrimSpace(provenance.PromptVersion) == "" || !isHexDigest(provenance.RetrievalSnapshotID) || provenance.GeneratedAt.IsZero() {
		return ErrBrainEvidenceInvalid
	}
	return nil
}

// BrainClaim stores only an assertion digest/content reference in lineage.
// User-authored/model prose remains in the erasable revision body.
type BrainClaim struct {
	ClaimID         string                    `json:"claimId"`
	ClaimType       string                    `json:"claimType"`
	AssertionDigest string                    `json:"assertionDigest"`
	ContentRef      string                    `json:"contentRef,omitempty"`
	Status          BrainClaimStatus          `json:"status"`
	Confidence      float64                   `json:"confidence"`
	OccurredAt      time.Time                 `json:"occurredAt,omitempty"`
	ValidFrom       time.Time                 `json:"validFrom,omitempty"`
	ValidUntil      time.Time                 `json:"validUntil,omitempty"`
	Evidence        []BrainEvidenceRef        `json:"evidence"`
	Generation      BrainGenerationProvenance `json:"generation"`
}

func (claim BrainClaim) Validate() error {
	if strings.TrimSpace(claim.ClaimID) == "" || strings.TrimSpace(claim.ClaimType) == "" || !isHexDigest(claim.AssertionDigest) ||
		claim.Confidence < 0 || claim.Confidence > 1 || !validBrainClaimStatus(claim.Status) {
		return ErrBrainEvidenceInvalid
	}
	if !claim.ValidUntil.IsZero() && (claim.ValidFrom.IsZero() || !claim.ValidFrom.Before(claim.ValidUntil)) {
		return ErrBrainEvidenceInvalid
	}
	if err := claim.Generation.Validate(); err != nil {
		return err
	}
	for _, evidence := range claim.Evidence {
		if err := evidence.Validate(); err != nil {
			return err
		}
	}
	if claim.Status == BrainClaimAsserted && len(claim.Evidence) == 0 {
		return fmt.Errorf("%w: asserted claim has no primary evidence", ErrBrainEvidenceInvalid)
	}
	return nil
}

func (claim BrainClaim) HasUntrustedEvidence() bool {
	for _, evidence := range claim.Evidence {
		if evidence.Trust == BrainEvidenceUntrustedGuest {
			return true
		}
	}
	return false
}

func validBrainClaimStatus(status BrainClaimStatus) bool {
	switch status {
	case BrainClaimAsserted, BrainClaimInferred, BrainClaimUnsupported, BrainClaimSuperseded:
		return true
	default:
		return false
	}
}

type RetrievalSnapshotSource struct {
	EvidenceID string           `json:"evidenceId"`
	Evidence   BrainEvidenceRef `json:"evidence"`
}

// RetrievalSnapshot freezes the exact authorized source revisions used for one
// prompt. It is reauthorized at prompt, critic, publication, and read time.
type RetrievalSnapshot struct {
	SnapshotID          string                    `json:"snapshotId"`
	TenantID            string                    `json:"tenantId"`
	PrincipalKind       ACLPrincipalKind          `json:"principalKind"`
	PrincipalID         string                    `json:"principalId"`
	Query               string                    `json:"query"`
	QueryDigest         string                    `json:"queryDigest"`
	Temporal            TemporalQuery             `json:"temporal"`
	SourceHighWater     uint64                    `json:"sourceHighWater"`
	ProjectionHighWater uint64                    `json:"projectionHighWater"`
	PurgeGeneration     int64                     `json:"purgeGeneration"`
	Sources             []RetrievalSnapshotSource `json:"sources"`
	CreatedAt           time.Time                 `json:"createdAt"`
}

func (snapshot RetrievalSnapshot) Validate() error {
	if !isHexDigest(snapshot.SnapshotID) || strings.TrimSpace(snapshot.TenantID) == "" || !validACLPrincipalKind(snapshot.PrincipalKind) ||
		strings.TrimSpace(snapshot.PrincipalID) == "" || strings.TrimSpace(snapshot.Query) == "" || !isHexDigest(snapshot.QueryDigest) ||
		snapshot.QueryDigest != digestBrainString(snapshot.Query) || snapshot.PurgeGeneration < 0 || snapshot.CreatedAt.IsZero() {
		return ErrBrainEvidenceInvalid
	}
	if snapshot.PrincipalKind == ACLPrincipalGuest || snapshot.PrincipalKind == ACLPrincipalCapability {
		return ErrBrainEvidenceInvalid
	}
	if err := snapshot.Temporal.Validate(); err != nil {
		return err
	}
	wantSnapshotID, err := snapshot.CanonicalID()
	if err != nil || snapshot.SnapshotID != wantSnapshotID {
		return ErrBrainEvidenceInvalid
	}
	seenEvidenceIDs := make(map[string]bool, len(snapshot.Sources))
	seenRevisions := make(map[string]bool, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		if strings.TrimSpace(source.EvidenceID) == "" || seenEvidenceIDs[source.EvidenceID] {
			return ErrBrainEvidenceInvalid
		}
		seenEvidenceIDs[source.EvidenceID] = true
		if err := source.Evidence.Validate(); err != nil || source.Evidence.TenantID != snapshot.TenantID || source.Evidence.PurgeGeneration != snapshot.PurgeGeneration {
			return ErrBrainEvidenceInvalid
		}
		if snapshot.Temporal.RoomID != "" && source.Evidence.RoomID != snapshot.Temporal.RoomID {
			return ErrBrainEvidenceInvalid
		}
		if snapshot.Temporal.SittingID != "" && source.Evidence.SittingID != snapshot.Temporal.SittingID {
			return ErrBrainEvidenceInvalid
		}
		if !source.Evidence.OccurredStart.IsZero() && (source.Evidence.OccurredStart.Before(snapshot.Temporal.StartUTC) || source.Evidence.OccurredEnd.After(snapshot.Temporal.EndUTC)) {
			return ErrBrainEvidenceInvalid
		}
		key := source.Evidence.SourceFamily + "\x00" + source.Evidence.ObjectID + "\x00" + fmt.Sprint(source.Evidence.ContentRevision)
		if seenRevisions[key] {
			return ErrBrainEvidenceInvalid
		}
		seenRevisions[key] = true
	}
	return nil
}

// CanonicalID binds the complete prompt input inventory. SnapshotID itself is
// excluded from the material to avoid a self-referential digest.
func (snapshot RetrievalSnapshot) CanonicalID() (string, error) {
	material := struct {
		TenantID            string                    `json:"tenantId"`
		PrincipalKind       ACLPrincipalKind          `json:"principalKind"`
		PrincipalID         string                    `json:"principalId"`
		Query               string                    `json:"query"`
		QueryDigest         string                    `json:"queryDigest"`
		Temporal            TemporalQuery             `json:"temporal"`
		SourceHighWater     uint64                    `json:"sourceHighWater"`
		ProjectionHighWater uint64                    `json:"projectionHighWater"`
		PurgeGeneration     int64                     `json:"purgeGeneration"`
		Sources             []RetrievalSnapshotSource `json:"sources"`
		CreatedAt           time.Time                 `json:"createdAt"`
	}{snapshot.TenantID, snapshot.PrincipalKind, snapshot.PrincipalID, snapshot.Query, snapshot.QueryDigest, snapshot.Temporal,
		snapshot.SourceHighWater, snapshot.ProjectionHighWater, snapshot.PurgeGeneration, snapshot.Sources, snapshot.CreatedAt}
	raw, err := canonicalJSON(material)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func digestBrainString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (snapshot RetrievalSnapshot) HasUntrustedEvidence() bool {
	for _, source := range snapshot.Sources {
		if source.Evidence.Trust == BrainEvidenceUntrustedGuest {
			return true
		}
	}
	return false
}

type BrainPurgeGenerationResolver interface {
	CurrentPurgeGeneration(context.Context, string) (int64, error)
}

func ReauthorizeRetrievalSnapshot(ctx context.Context, kernel AuthorizationKernel, purgeResolver BrainPurgeGenerationResolver, principal ACLPrincipal, snapshot RetrievalSnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if principal.TenantID != snapshot.TenantID || principal.Kind != snapshot.PrincipalKind || principal.ID != snapshot.PrincipalID {
		return ErrRetrievalSnapshotStale
	}
	if purgeResolver == nil {
		return ErrRetrievalSnapshotStale
	}
	currentPurgeGeneration, err := purgeResolver.CurrentPurgeGeneration(ctx, snapshot.TenantID)
	if err != nil || currentPurgeGeneration != snapshot.PurgeGeneration {
		return ErrRetrievalSnapshotStale
	}
	for _, source := range snapshot.Sources {
		objectRef, revisionRef := source.Evidence.ACLRefs()
		if decision := kernel.Authorize(ctx, principal, ACLReadContent, objectRef, revisionRef); !decision.Allowed || decision.ACLVersion != source.Evidence.ACLVersion {
			return ErrRetrievalSnapshotStale
		}
	}
	return nil
}
