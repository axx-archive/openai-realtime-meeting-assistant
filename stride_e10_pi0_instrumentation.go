package main

// PI0-A is deliberately route-free and inactive. This file defines only
// body-minimized contracts, managed-MAC verification, and deterministic
// validation/recovery machinery. It installs no runtime and collects nothing.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrStridePI0Invalid     = errors.New("invalid stride pi0 instrumentation contract")
	ErrStridePI0Unavailable = errors.New("stride pi0 managed authority unavailable")
	ErrStridePI0Conflict    = errors.New("stride pi0 instrumentation conflict")
	errStridePI0NoMutation  = errors.New("stride pi0 no mutation")
)

const (
	stridePI0EventSchema       = "stride.pi0.lifecycle-event.v1"
	stridePI0JournalSchema     = "stride.pi0.compound-journal.v1"
	stridePI0MetricSchema      = "stride.pi0.metric-definition-manifest.v1"
	stridePI0ComparisonSchema  = "stride.pi0.prior-workflow-comparison.v1"
	stridePI0EventMACDomain    = "meetingassist/stride/pi0/event/v1"
	stridePI0JournalMACDomain  = "meetingassist/stride/pi0/compound-journal/v1"
	stridePI0MetricMACDomain   = "meetingassist/stride/pi0/metric-manifest/v1"
	stridePI0CompareMACDomain  = "meetingassist/stride/pi0/prior-workflow/v1"
	stridePI0BindingMACDomain  = "meetingassist/stride/pi0/measurement-artifact-binding/v1"
	stridePI0OperationDomain   = "pi0/compound-operation/v1"
	stridePI0IdempotencyDomain = "pi0/idempotency/v1"
	stridePI0SourceDomain      = "pi0/source-manifest/v1"
	stridePI0OutputDomain      = "pi0/output/v1"
	stridePI0PublicTraceDomain = "pi0/public-trace/v1"
	stridePI0CarrierMACDomain  = "meetingassist/stride/pi0/file-carrier/v1"
	stridePI0CarrierTxnDomain  = "meetingassist/stride/pi0/file-carrier-transaction/v1"
	stridePI0AppendMACDomain   = "meetingassist/stride/pi0/append-receipt/v1"
)

type StridePI0ManagedMACKey struct {
	ID      string
	Version uint64
	Secret  []byte
}

type StridePI0ManagedMACKeyring interface {
	CurrentStridePI0ManagedMACKey(context.Context) (StridePI0ManagedMACKey, error)
	ResolveStridePI0ManagedMACKey(context.Context, string, uint64) (StridePI0ManagedMACKey, error)
	ResolveStridePI0ManagedMACKeyVersion(context.Context, uint64) (StridePI0ManagedMACKey, error)
	CurrentStridePI0PrivateCommitmentKey(context.Context) (StridePI0ManagedMACKey, error)
	ResolveStridePI0PrivateCommitmentKey(context.Context, string, uint64) (StridePI0ManagedMACKey, error)
	ResolveStridePI0PrivateCommitmentKeyVersion(context.Context, uint64) (StridePI0ManagedMACKey, error)
	CurrentStridePI0PublicTraceKey(context.Context) (StridePI0ManagedMACKey, error)
	ResolveStridePI0PublicTraceKey(context.Context, string, uint64) (StridePI0ManagedMACKey, error)
	ResolveStridePI0PublicTraceKeyVersion(context.Context, uint64) (StridePI0ManagedMACKey, error)
}

type StridePI0ManagedCommitment struct {
	Domain     string `json:"domain"`
	KeyID      string `json:"keyId"`
	KeyVersion uint64 `json:"keyVersion"`
	Digest     string `json:"digest"`
}

func stridePI0SeparatedKeysAtVersion(ctx context.Context, keys StridePI0ManagedMACKeyring, version uint64) (StridePI0ManagedMACKey, StridePI0ManagedMACKey, StridePI0ManagedMACKey, error) {
	if keys == nil || version == 0 {
		return StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
	}
	state, stateErr := keys.ResolveStridePI0ManagedMACKeyVersion(ctx, version)
	private, privateErr := keys.ResolveStridePI0PrivateCommitmentKeyVersion(ctx, version)
	public, publicErr := keys.ResolveStridePI0PublicTraceKeyVersion(ctx, version)
	if stateErr != nil || privateErr != nil || publicErr != nil || !stridePI0ValidKey(state) || !stridePI0ValidKey(private) || !stridePI0ValidKey(public) || state.Version != version || private.Version != version || public.Version != version {
		return StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
	}
	roles := []StridePI0ManagedMACKey{state, private, public}
	for i := range roles {
		for j := i + 1; j < len(roles); j++ {
			if roles[i].ID == roles[j].ID || hmac.Equal(roles[i].Secret, roles[j].Secret) {
				return StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
			}
		}
	}
	return state, private, public, nil
}

func stridePI0CurrentSeparatedKeys(ctx context.Context, keys StridePI0ManagedMACKeyring) (StridePI0ManagedMACKey, StridePI0ManagedMACKey, StridePI0ManagedMACKey, error) {
	if keys == nil {
		return StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
	}
	state, stateErr := keys.CurrentStridePI0ManagedMACKey(ctx)
	private, privateErr := keys.CurrentStridePI0PrivateCommitmentKey(ctx)
	public, publicErr := keys.CurrentStridePI0PublicTraceKey(ctx)
	if stateErr != nil || privateErr != nil || publicErr != nil || !stridePI0ValidKey(state) || !stridePI0ValidKey(private) || !stridePI0ValidKey(public) {
		return StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
	}
	if state.Version != private.Version || state.Version != public.Version {
		return StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
	}
	resolvedState, resolvedPrivate, resolvedPublic, err := stridePI0SeparatedKeysAtVersion(ctx, keys, state.Version)
	if err != nil || resolvedState.ID != state.ID || !hmac.Equal(resolvedState.Secret, state.Secret) || resolvedPrivate.ID != private.ID || !hmac.Equal(resolvedPrivate.Secret, private.Secret) || resolvedPublic.ID != public.ID || !hmac.Equal(resolvedPublic.Secret, public.Secret) {
		return StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
	}
	return resolvedState, resolvedPrivate, resolvedPublic, nil
}

func stridePI0CurrentStateKey(ctx context.Context, keys StridePI0ManagedMACKeyring) (StridePI0ManagedMACKey, error) {
	state, _, _, err := stridePI0CurrentSeparatedKeys(ctx, keys)
	return state, err
}

func stridePI0ResolveStateKey(ctx context.Context, keys StridePI0ManagedMACKeyring, id string, version uint64) (StridePI0ManagedMACKey, error) {
	state, _, _, err := stridePI0SeparatedKeysAtVersion(ctx, keys, version)
	if err != nil || state.ID != id {
		return StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
	}
	resolved, resolveErr := keys.ResolveStridePI0ManagedMACKey(ctx, id, version)
	if resolveErr != nil || !stridePI0ValidKey(resolved) || resolved.ID != state.ID || resolved.Version != state.Version || !hmac.Equal(resolved.Secret, state.Secret) {
		return StridePI0ManagedMACKey{}, ErrStridePI0Unavailable
	}
	return state, nil
}

func (c StridePI0ManagedCommitment) validate(domain string) error {
	if c.Domain != domain || !strideIdentifier(c.KeyID) || c.KeyVersion == 0 || !isHexDigest(c.Digest) {
		return ErrStridePI0Invalid
	}
	return nil
}

func MintStridePI0ManagedCommitment(ctx context.Context, keys StridePI0ManagedMACKeyring, domain string, values ...string) (StridePI0ManagedCommitment, error) {
	if keys == nil || !oneOf(domain, stridePI0OperationDomain, stridePI0IdempotencyDomain, stridePI0SourceDomain, stridePI0OutputDomain, stridePI0PublicTraceDomain) || len(values) == 0 || len(values) > 32 {
		return StridePI0ManagedCommitment{}, ErrStridePI0Invalid
	}
	_, privateKey, publicKey, err := stridePI0CurrentSeparatedKeys(ctx, keys)
	key := privateKey
	if domain == stridePI0PublicTraceDomain {
		key = publicKey
	}
	if err != nil || !stridePI0ValidKey(key) {
		return StridePI0ManagedCommitment{}, ErrStridePI0Unavailable
	}
	digest, err := stridePI0CommitmentDigest(key.Secret, domain, values)
	if err != nil {
		return StridePI0ManagedCommitment{}, err
	}
	return StridePI0ManagedCommitment{Domain: domain, KeyID: key.ID, KeyVersion: key.Version, Digest: digest}, nil
}

func VerifyStridePI0ManagedCommitment(ctx context.Context, keys StridePI0ManagedMACKeyring, commitment StridePI0ManagedCommitment, domain string, values ...string) error {
	if keys == nil || commitment.validate(domain) != nil || len(values) == 0 || len(values) > 32 {
		return ErrStridePI0Invalid
	}
	stateKey, privateKey, publicKey, separationErr := stridePI0SeparatedKeysAtVersion(ctx, keys, commitment.KeyVersion)
	if separationErr != nil {
		return ErrStridePI0Unavailable
	}
	key, err := keys.ResolveStridePI0PrivateCommitmentKey(ctx, commitment.KeyID, commitment.KeyVersion)
	expectedRole := privateKey
	otherRoles := []StridePI0ManagedMACKey{stateKey, publicKey}
	if domain == stridePI0PublicTraceDomain {
		key, err = keys.ResolveStridePI0PublicTraceKey(ctx, commitment.KeyID, commitment.KeyVersion)
		expectedRole = publicKey
		otherRoles = []StridePI0ManagedMACKey{stateKey, privateKey}
	}
	if err != nil || !stridePI0ValidKey(key) || key.ID != commitment.KeyID || key.Version != commitment.KeyVersion || key.ID != expectedRole.ID || !hmac.Equal(key.Secret, expectedRole.Secret) {
		return ErrStridePI0Unavailable
	}
	for _, other := range otherRoles {
		if key.ID == other.ID || hmac.Equal(key.Secret, other.Secret) {
			return ErrStridePI0Unavailable
		}
	}
	want, err := stridePI0CommitmentDigest(key.Secret, domain, values)
	if err != nil || !hmac.Equal([]byte(want), []byte(commitment.Digest)) {
		return ErrStridePI0Invalid
	}
	return nil
}

func stridePI0CommitmentDigest(secret []byte, domain string, values []string) (string, error) {
	payload, err := canonicalJSON(struct {
		Domain string   `json:"domain"`
		Values []string `json:"values"`
	}{domain, values})
	if err != nil {
		return "", ErrStridePI0Invalid
	}
	return stridePI0MAC(secret, payload), nil
}

// StridePI0CurrentAuthority must resolve canonical session/membership or
// action-bound service authority and hold it through the supplied callback.
// PI0 does not provide a permissive implementation.
type StridePI0CurrentAuthority interface {
	WithCurrentStridePI0Principal(context.Context, StridePI0Principal, func() error) error
}

type StridePI0EffectEvidenceKey struct {
	ID      string
	Version uint64
	Secret  []byte
}

type StridePI0EffectEvidenceKeyring interface {
	CurrentStridePI0EffectEvidenceKey(context.Context) (StridePI0EffectEvidenceKey, error)
	ResolveStridePI0EffectEvidenceKey(context.Context, string, uint64) (StridePI0EffectEvidenceKey, error)
}

type StridePI0Reference struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

func (r StridePI0Reference) validate() error {
	if !oneOf(r.Type, "conversation", "transcript", "intent", "suggestion", "run", "effect", "artifact", "review", "verification", "outcome", "work_record", "publication", "contact", "block", "grant", "policy_verdict", "network_search_receipt", "consent", "policy", "service", "journal", "model", "prompt_config", "evidence_manifest", "provider_receipt") || !strideIdentifier(r.ID) || r.Revision < 1 || !isHexDigest(r.Digest) {
		return ErrStridePI0Invalid
	}
	return nil
}

type StridePI0Aggregate struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

func (a StridePI0Aggregate) validate() error {
	return StridePI0Reference(a).validate()
}

type StridePI0Principal struct {
	Kind                 string `json:"kind"`
	PersonID             string `json:"personId,omitempty"`
	OrganizationID       string `json:"organizationId"`
	MembershipID         string `json:"membershipId,omitempty"`
	MembershipRevision   int64  `json:"membershipRevision,omitempty"`
	SessionSubjectDigest string `json:"sessionSubjectDigest,omitempty"`
	SessionRevision      int64  `json:"sessionRevision,omitempty"`
	ServiceID            string `json:"serviceId,omitempty"`
	ControllerRevision   int64  `json:"controllerRevision,omitempty"`
}

func (p StridePI0Principal) validate() error {
	if !strideIdentifier(p.OrganizationID) {
		return ErrStridePI0Invalid
	}
	switch p.Kind {
	case "human":
		if !strideIdentifier(p.PersonID) || !strideIdentifier(p.MembershipID) || p.MembershipRevision < 1 || !isHexDigest(p.SessionSubjectDigest) || p.SessionRevision < 1 || p.ServiceID != "" || p.ControllerRevision != 0 {
			return ErrStridePI0Invalid
		}
	case "service":
		if p.PersonID != "" || p.MembershipID != "" || p.MembershipRevision != 0 || p.SessionSubjectDigest != "" || p.SessionRevision != 0 || !strideIdentifier(p.ServiceID) || p.ControllerRevision < 1 {
			return ErrStridePI0Invalid
		}
	default:
		return ErrStridePI0Invalid
	}
	return nil
}

type StridePI0Audience struct {
	Visibility   string   `json:"visibility"`
	PrincipalIDs []string `json:"principalIds"`
	ACLVersion   int64    `json:"aclVersion"`
}

func (a StridePI0Audience) validate() error {
	if !oneOf(a.Visibility, "private", "organization", "named_parties", "public") || a.ACLVersion < 1 || len(a.PrincipalIDs) > 16 || (a.Visibility != "public" && len(a.PrincipalIDs) == 0) || !stridePI0UniqueIdentifiers(a.PrincipalIDs) {
		return ErrStridePI0Invalid
	}
	if a.Visibility == "public" && len(a.PrincipalIDs) != 0 {
		return ErrStridePI0Invalid
	}
	return nil
}

type StridePI0Quality struct {
	Status              string `json:"status"`
	Reason              string `json:"reason"`
	ObservedSourceCount int64  `json:"observedSourceCount"`
	ExpectedSourceCount int64  `json:"expectedSourceCount"`
}

func (q StridePI0Quality) validate() error {
	if q.ObservedSourceCount < 0 || q.ExpectedSourceCount < 0 || !oneOf(q.Status, "known", "partial", "unknown", "not_applicable") || !oneOf(q.Reason, "none", "legacy_gap", "source_unavailable", "authority_unavailable", "consent_unavailable", "policy_unavailable", "digest_unavailable", "late_arrival", "clock_uncertain", "recovery_pending", "unsupported") {
		return ErrStridePI0Invalid
	}
	switch q.Status {
	case "known":
		if q.Reason != "none" || q.ObservedSourceCount != q.ExpectedSourceCount {
			return ErrStridePI0Invalid
		}
	case "partial":
		if q.Reason == "none" || q.ExpectedSourceCount < 1 || q.ObservedSourceCount >= q.ExpectedSourceCount {
			return ErrStridePI0Invalid
		}
	case "unknown":
		if q.Reason == "none" || q.ExpectedSourceCount != 0 {
			return ErrStridePI0Invalid
		}
	case "not_applicable":
		if q.Reason != "none" || q.ObservedSourceCount != 0 || q.ExpectedSourceCount != 0 {
			return ErrStridePI0Invalid
		}
	}
	return nil
}

type StridePI0Fence struct {
	Generation uint64               `json:"generation"`
	Refs       []StridePI0Reference `json:"refs"`
}

func (f *StridePI0Fence) validate() error {
	if f == nil {
		return nil
	}
	if f.Generation < 1 || len(f.Refs) < 1 || len(f.Refs) > 16 || !stridePI0UniqueReferences(f.Refs) {
		return ErrStridePI0Invalid
	}
	for _, ref := range f.Refs {
		if ref.validate() != nil {
			return ErrStridePI0Invalid
		}
	}
	return nil
}

type StridePI0Retention struct {
	Class       string             `json:"class"`
	PolicyRef   StridePI0Reference `json:"policyRef"`
	RetainUntil time.Time          `json:"retainUntil"`
}

func (r StridePI0Retention) validate(recordedAt time.Time) error {
	if !oneOf(r.Class, "source_link_short", "private_work_lifecycle", "authorized_disclosure_audit", "purge_receipt_body_free") || r.PolicyRef.Type != "policy" || r.PolicyRef.validate() != nil || r.RetainUntil.IsZero() || !r.RetainUntil.After(recordedAt) {
		return ErrStridePI0Invalid
	}
	maximum := map[string]time.Duration{
		"source_link_short":           30 * 24 * time.Hour,
		"private_work_lifecycle":      365 * 24 * time.Hour,
		"authorized_disclosure_audit": 730 * 24 * time.Hour,
		"purge_receipt_body_free":     730 * 24 * time.Hour,
	}[r.Class]
	if r.RetainUntil.Sub(recordedAt) > maximum {
		return ErrStridePI0Invalid
	}
	return nil
}

type StridePI0LifecycleEvent struct {
	Schema                string                     `json:"schema"`
	EventID               string                     `json:"eventId"`
	EventType             string                     `json:"eventType"`
	TenantID              string                     `json:"tenantId"`
	Aggregate             StridePI0Aggregate         `json:"aggregate"`
	TraceID               string                     `json:"traceId"`
	ParentEventID         string                     `json:"parentEventId,omitempty"`
	CausationRefs         []StridePI0Reference       `json:"causationRefs"`
	Principal             StridePI0Principal         `json:"principal"`
	SubjectRefs           []StridePI0Reference       `json:"subjectRefs"`
	Audience              StridePI0Audience          `json:"audience"`
	ConsentRefs           []StridePI0Reference       `json:"consentRefs"`
	PolicyRefs            []StridePI0Reference       `json:"policyRefs"`
	Provenance            string                     `json:"provenance"`
	ProvenanceRefs        []StridePI0Reference       `json:"provenanceRefs"`
	IdempotencyCommitment StridePI0ManagedCommitment `json:"idempotencyCommitment"`
	SourceCommitment      StridePI0ManagedCommitment `json:"sourceCommitment"`
	OutputCommitment      StridePI0ManagedCommitment `json:"outputCommitment"`
	PublicTraceCommitment StridePI0ManagedCommitment `json:"publicTraceCommitment"`
	Decision              string                     `json:"decision,omitempty"`
	State                 string                     `json:"state,omitempty"`
	FailureClass          string                     `json:"failureClass,omitempty"`
	JournalOperationID    string                     `json:"journalOperationId,omitempty"`
	OccurredAt            time.Time                  `json:"occurredAt"`
	EffectiveAt           time.Time                  `json:"effectiveAt"`
	RecordedAt            time.Time                  `json:"recordedAt"`
	Quality               StridePI0Quality           `json:"quality"`
	Revocation            *StridePI0Fence            `json:"revocation"`
	Purge                 *StridePI0Fence            `json:"purge"`
	Retention             StridePI0Retention         `json:"retention"`
	KeyID                 string                     `json:"keyId"`
	KeyVersion            uint64                     `json:"keyVersion"`
	MAC                   string                     `json:"mac"`
}

var stridePI0EventTypes = map[string]int{
	"source.bound_to_trace": 0, "source.corrected": 0, "source.retracted": 0,
	"intent.admitted": 1, "intent.rejected": 1,
	"suggestion.created": 2, "suggestion.revised": 2, "suggestion.endorsed": 2, "suggestion.approved": 2, "suggestion.dismissed": 2, "suggestion.expired": 2,
	"run.created": 3, "run.queued": 3, "run.started": 3, "run.state_changed": 3, "run.source_invalidated": 3, "run.intervention_requested": 3, "run.intervention_resolved": 3, "run.cancelled": 3, "run.failed": 3, "run.completed": 3,
	"effect.requested": 4, "effect.approved": 4, "effect.applied": 4, "effect.failed": 4, "effect.reconciled": 4,
	"artifact.created": 5, "artifact.revised": 5, "artifact.review_requested": 5, "artifact.review_decided": 5, "artifact.verification_recorded": 5, "artifact.adopted": 5, "artifact.rejected": 5, "artifact.withdrawn": 5, "artifact.publication_changed": 5,
	"outcome.recorded": 6, "outcome.corrected": 6, "outcome.rejected": 6, "outcome.withdrawn": 6,
	"work_record.claim_created": 7, "work_record.subject_decided": 7, "work_record.named_party_decided": 7, "work_record.organization_decided": 7, "work_record.attested": 7, "work_record.corrected": 7, "work_record.revoked": 7,
	"publication.contribution_published": 8, "publication.contribution_withdrawn": 8, "publication.network_state_changed": 8,
	"collaboration.search_admitted": 9, "collaboration.contact_requested": 9, "collaboration.contact_decided": 9, "collaboration.block_changed": 9,
	"lifecycle.corrected": 10, "lifecycle.reconciled": 10, "lifecycle.revoked": 10, "lifecycle.purged": 10,
}

type stridePI0EventReferenceRule struct {
	AggregateType     string
	CausationRequired []string
	CausationOneOf    []string
	CausationAllowed  []string
	CausationMin      int
	CausationMax      int
	SubjectRequired   []string
	SubjectOneOf      []string
	SubjectAllowed    []string
	SubjectMin        int
	SubjectMax        int
	Consent           string
}

func stridePI0EventRule(aggregate string, causation ...string) stridePI0EventReferenceRule {
	return stridePI0EventReferenceRule{AggregateType: aggregate, CausationOneOf: append([]string(nil), causation...), CausationAllowed: append([]string(nil), causation...), CausationMin: 1, CausationMax: 1, SubjectRequired: []string{aggregate}, SubjectAllowed: []string{aggregate}, SubjectMin: 1, SubjectMax: 1, Consent: "required"}
}

func stridePI0RequiredEventRule(aggregate string, causation ...string) stridePI0EventReferenceRule {
	rule := stridePI0EventRule(aggregate, causation...)
	rule.CausationRequired, rule.CausationOneOf = append([]string(nil), causation...), nil
	rule.CausationMin, rule.CausationMax = len(causation), len(causation)
	return rule
}

func stridePI0NoSubjectNoConsentRule(aggregate string, causation ...string) stridePI0EventReferenceRule {
	rule := stridePI0RequiredEventRule(aggregate, causation...)
	rule.SubjectRequired, rule.SubjectAllowed, rule.SubjectMin, rule.SubjectMax = nil, nil, 0, 0
	rule.Consent = "not_applicable"
	return rule
}

func stridePI0SearchAdmissionRule() stridePI0EventReferenceRule {
	rule := stridePI0RequiredEventRule("contact", "grant", "policy_verdict", "network_search_receipt")
	rule.SubjectRequired, rule.SubjectAllowed, rule.SubjectMin, rule.SubjectMax = nil, nil, 0, 0
	return rule
}

func stridePI0LifecycleCorrectionRule() stridePI0EventReferenceRule {
	rule := stridePI0RequiredEventRule("journal", "journal", "journal")
	rule.SubjectRequired, rule.SubjectAllowed, rule.SubjectMin, rule.SubjectMax = []string{"journal", "journal"}, []string{"journal"}, 2, 2
	rule.Consent = "not_applicable"
	return rule
}

var stridePI0EventReferenceRules = map[string]stridePI0EventReferenceRule{
	"source.bound_to_trace": stridePI0EventRule("intent", "conversation", "transcript"), "source.corrected": stridePI0EventRule("intent", "intent"), "source.retracted": stridePI0EventRule("intent", "intent"),
	"intent.admitted": stridePI0EventRule("intent", "conversation", "transcript"), "intent.rejected": stridePI0EventRule("intent", "conversation", "transcript"),
	"suggestion.created": stridePI0EventRule("suggestion", "intent"), "suggestion.revised": stridePI0EventRule("suggestion", "suggestion"), "suggestion.endorsed": stridePI0EventRule("suggestion", "suggestion"), "suggestion.approved": stridePI0EventRule("suggestion", "suggestion"), "suggestion.dismissed": stridePI0EventRule("suggestion", "suggestion"), "suggestion.expired": stridePI0EventRule("suggestion", "suggestion"),
	"run.created": stridePI0EventRule("run", "suggestion"), "run.queued": stridePI0EventRule("run", "run"), "run.started": stridePI0EventRule("run", "run"), "run.state_changed": stridePI0EventRule("run", "run"), "run.source_invalidated": stridePI0RequiredEventRule("run", "run", "intent"), "run.intervention_requested": stridePI0EventRule("run", "run"), "run.intervention_resolved": stridePI0EventRule("run", "run"), "run.cancelled": stridePI0EventRule("run", "run"), "run.failed": stridePI0EventRule("run", "run"), "run.completed": stridePI0EventRule("run", "run"),
	"effect.requested": stridePI0EventRule("effect", "run"), "effect.approved": stridePI0EventRule("effect", "effect"), "effect.applied": stridePI0RequiredEventRule("effect", "effect", "provider_receipt"), "effect.failed": stridePI0RequiredEventRule("effect", "effect", "provider_receipt"), "effect.reconciled": stridePI0RequiredEventRule("effect", "effect", "provider_receipt", "journal"),
	"artifact.created": stridePI0EventRule("artifact", "run", "effect", "artifact"), "artifact.revised": stridePI0EventRule("artifact", "artifact"), "artifact.review_requested": stridePI0EventRule("artifact", "artifact", "review"), "artifact.review_decided": stridePI0EventRule("artifact", "artifact", "review"), "artifact.verification_recorded": stridePI0EventRule("artifact", "artifact", "verification"), "artifact.adopted": stridePI0EventRule("artifact", "artifact", "review"), "artifact.rejected": stridePI0EventRule("artifact", "artifact", "review"), "artifact.withdrawn": stridePI0EventRule("artifact", "artifact"), "artifact.publication_changed": stridePI0EventRule("artifact", "artifact", "publication"),
	"outcome.recorded": stridePI0EventRule("outcome", "artifact", "outcome", "review"), "outcome.corrected": stridePI0EventRule("outcome", "outcome"), "outcome.rejected": stridePI0EventRule("outcome", "outcome", "review"), "outcome.withdrawn": stridePI0EventRule("outcome", "outcome"),
	"work_record.claim_created": stridePI0EventRule("work_record", "artifact", "outcome", "work_record"), "work_record.subject_decided": stridePI0EventRule("work_record", "work_record", "review"), "work_record.named_party_decided": stridePI0EventRule("work_record", "work_record", "review"), "work_record.organization_decided": stridePI0EventRule("work_record", "work_record", "review"), "work_record.attested": stridePI0EventRule("work_record", "work_record", "verification"), "work_record.corrected": stridePI0EventRule("work_record", "work_record"), "work_record.revoked": stridePI0EventRule("work_record", "work_record"),
	"publication.contribution_published": stridePI0EventRule("publication", "work_record", "publication"), "publication.contribution_withdrawn": stridePI0EventRule("publication", "publication", "work_record"), "publication.network_state_changed": stridePI0EventRule("publication", "publication"),
	"collaboration.search_admitted": stridePI0SearchAdmissionRule(), "collaboration.contact_requested": stridePI0RequiredEventRule("contact", "contact", "publication"), "collaboration.contact_decided": stridePI0EventRule("contact", "contact"), "collaboration.block_changed": stridePI0EventRule("block", "block", "contact"),
	"lifecycle.corrected": stridePI0LifecycleCorrectionRule(), "lifecycle.reconciled": stridePI0NoSubjectNoConsentRule("journal", "journal", "provider_receipt"), "lifecycle.revoked": stridePI0NoSubjectNoConsentRule("journal", "journal"), "lifecycle.purged": stridePI0NoSubjectNoConsentRule("journal", "journal"),
}

func stridePI0EventReferencesMatch(event StridePI0LifecycleEvent) bool {
	rule, ok := stridePI0EventReferenceRules[event.EventType]
	if !ok || len(stridePI0EventReferenceRules) != 57 || rule.AggregateType != event.Aggregate.Type || !oneOf(rule.Consent, "required", "not_applicable") {
		return false
	}
	if rule.Consent == "required" && len(event.ConsentRefs) == 0 || rule.Consent == "not_applicable" && len(event.ConsentRefs) != 0 {
		return false
	}
	causes, subjects := make([]string, len(event.CausationRefs)), make([]string, len(event.SubjectRefs))
	for i, ref := range event.CausationRefs {
		causes[i] = ref.Type
	}
	for i, ref := range event.SubjectRefs {
		subjects[i] = ref.Type
	}
	return stridePI0ReferenceTypesMatch(causes, rule.CausationRequired, rule.CausationOneOf, rule.CausationAllowed, rule.CausationMin, rule.CausationMax) && stridePI0ReferenceTypesMatch(subjects, rule.SubjectRequired, rule.SubjectOneOf, rule.SubjectAllowed, rule.SubjectMin, rule.SubjectMax)
}

func stridePI0ReferenceTypesMatch(actual, required, oneOf, allowed []string, minimum, maximum int) bool {
	if minimum < 0 || maximum < minimum || len(actual) < minimum || len(actual) > maximum {
		return false
	}
	counts := map[string]int{}
	for _, value := range actual {
		if !containsSTRIDEString(allowed, value) {
			return false
		}
		counts[value]++
	}
	requiredCounts := map[string]int{}
	for _, value := range required {
		requiredCounts[value]++
	}
	for value, count := range requiredCounts {
		if counts[value] < count {
			return false
		}
	}
	if len(oneOf) > 0 {
		found := false
		for _, value := range oneOf {
			if counts[value] > 0 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func stridePI0VerifyEventCommitments(ctx context.Context, keys StridePI0ManagedMACKeyring, event StridePI0LifecycleEvent) error {
	checks := []struct {
		commitment StridePI0ManagedCommitment
		domain     string
		values     []string
	}{
		{event.IdempotencyCommitment, stridePI0IdempotencyDomain, []string{event.TenantID, event.EventID, event.EventType}},
		{event.SourceCommitment, stridePI0SourceDomain, []string{event.TenantID, event.TraceID, event.Aggregate.Type, event.Aggregate.ID}},
		{event.OutputCommitment, stridePI0OutputDomain, []string{event.EventID, event.Aggregate.Digest, event.Decision, event.State, event.FailureClass}},
		{event.PublicTraceCommitment, stridePI0PublicTraceDomain, []string{event.TenantID, event.TraceID}},
	}
	for _, check := range checks {
		if VerifyStridePI0ManagedCommitment(ctx, keys, check.commitment, check.domain, check.values...) != nil {
			return ErrStridePI0Invalid
		}
	}
	return nil
}

func (e StridePI0LifecycleEvent) validateUnsigned() error {
	if e.Schema != stridePI0EventSchema || !strideIdentifier(e.EventID) || !strideIdentifier(e.TenantID) || e.Aggregate.validate() != nil || !strideIdentifier(e.TraceID) || (e.ParentEventID != "" && !strideIdentifier(e.ParentEventID)) || len(e.CausationRefs) > 16 || len(e.SubjectRefs) > 16 || len(e.ConsentRefs) > 16 || len(e.PolicyRefs) < 1 || len(e.PolicyRefs) > 16 || len(e.ProvenanceRefs) > 8 || e.Principal.validate() != nil || e.Audience.validate() != nil || !oneOf(e.Provenance, "direct_human", "deterministic_system", "model_assisted", "tool_result", "provider_import", "legacy_import") || e.IdempotencyCommitment.validate(stridePI0IdempotencyDomain) != nil || e.SourceCommitment.validate(stridePI0SourceDomain) != nil || e.OutputCommitment.validate(stridePI0OutputDomain) != nil || e.PublicTraceCommitment.validate(stridePI0PublicTraceDomain) != nil || e.OccurredAt.IsZero() || e.EffectiveAt.IsZero() || e.RecordedAt.IsZero() || e.EffectiveAt.Before(e.OccurredAt) || e.RecordedAt.Before(e.EffectiveAt) || e.Quality.validate() != nil || e.Revocation.validate() != nil || e.Purge.validate() != nil || e.Retention.validate(e.RecordedAt) != nil || !strideIdentifier(e.KeyID) || e.KeyVersion < 1 {
		return ErrStridePI0Invalid
	}
	if _, ok := stridePI0EventTypes[e.EventType]; !ok {
		return ErrStridePI0Invalid
	}
	if !stridePI0EventAggregateMatches(e.EventType, e.Aggregate.Type) {
		return ErrStridePI0Invalid
	}
	for _, refs := range [][]StridePI0Reference{e.CausationRefs, e.SubjectRefs, e.ConsentRefs, e.PolicyRefs, e.ProvenanceRefs} {
		if !stridePI0UniqueReferences(refs) {
			return ErrStridePI0Invalid
		}
		for _, ref := range refs {
			if ref.validate() != nil {
				return ErrStridePI0Invalid
			}
		}
	}
	if !stridePI0ReferencesSorted(e.CausationRefs) {
		return ErrStridePI0Invalid
	}
	for _, ref := range e.ConsentRefs {
		if ref.Type != "consent" {
			return ErrStridePI0Invalid
		}
	}
	for _, ref := range e.PolicyRefs {
		if ref.Type != "policy" {
			return ErrStridePI0Invalid
		}
	}
	if e.EventType == "source.bound_to_trace" {
		if e.ParentEventID != "" || len(e.CausationRefs) < 1 || e.Aggregate.Type != "intent" {
			return ErrStridePI0Invalid
		}
		for _, ref := range e.CausationRefs {
			if !oneOf(ref.Type, "conversation", "transcript") {
				return ErrStridePI0Invalid
			}
		}
	} else if e.ParentEventID == "" {
		return ErrStridePI0Invalid
	}
	if strings.HasPrefix(e.EventType, "effect.") {
		if !strideIdentifier(e.JournalOperationID) || e.Aggregate.Type != "effect" {
			return ErrStridePI0Invalid
		}
	} else if e.JournalOperationID != "" {
		return ErrStridePI0Invalid
	}
	if strings.HasSuffix(e.EventType, ".failed") && !oneOf(e.FailureClass, "authority", "policy", "provider", "timeout", "invalid_output", "persistence", "unknown") {
		return ErrStridePI0Invalid
	}
	if !strings.HasSuffix(e.EventType, ".failed") && e.FailureClass != "" {
		return ErrStridePI0Invalid
	}
	requiresRevocation := strings.HasSuffix(e.EventType, ".retracted") || strings.HasSuffix(e.EventType, ".withdrawn") || strings.HasSuffix(e.EventType, ".revoked") || e.EventType == "run.source_invalidated" || (e.EventType == "artifact.publication_changed" && e.State == "revoked") || (e.EventType == "collaboration.block_changed" && e.State == "revoked")
	requiresPurge := e.EventType == "lifecycle.purged" || (e.EventType == "publication.network_state_changed" && oneOf(e.State, "off", "deleted"))
	if requiresRevocation != (e.Revocation != nil) || requiresPurge != (e.Purge != nil) {
		return ErrStridePI0Invalid
	}
	if !stridePI0ValidEventDecision(e.EventType, e.Decision) {
		return ErrStridePI0Invalid
	}
	if !stridePI0ValidEventState(e.EventType, e.State) {
		return ErrStridePI0Invalid
	}
	if !stridePI0EventReferencesMatch(e) {
		return ErrStridePI0Invalid
	}
	if !stridePI0ValidProvenanceRefs(e.Provenance, e.ProvenanceRefs) || !stridePI0RetentionMatchesEvent(e.EventType, e.Retention.Class) {
		return ErrStridePI0Invalid
	}
	return nil
}

func stridePI0EventPayload(e StridePI0LifecycleEvent) ([]byte, error) {
	e.MAC = ""
	return canonicalJSON(struct {
		Domain string                  `json:"domain"`
		Event  StridePI0LifecycleEvent `json:"event"`
	}{stridePI0EventMACDomain, e})
}

func SealStridePI0LifecycleEvent(ctx context.Context, keys StridePI0ManagedMACKeyring, event StridePI0LifecycleEvent) (StridePI0LifecycleEvent, error) {
	if keys == nil {
		return StridePI0LifecycleEvent{}, ErrStridePI0Unavailable
	}
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil || !stridePI0ValidKey(key) {
		return StridePI0LifecycleEvent{}, ErrStridePI0Unavailable
	}
	event.KeyID, event.KeyVersion, event.MAC = key.ID, key.Version, ""
	if event.validateUnsigned() != nil || stridePI0VerifyEventCommitments(ctx, keys, event) != nil {
		return StridePI0LifecycleEvent{}, ErrStridePI0Invalid
	}
	payload, err := stridePI0EventPayload(event)
	if err != nil {
		return StridePI0LifecycleEvent{}, ErrStridePI0Invalid
	}
	event.MAC = stridePI0MAC(key.Secret, payload)
	return event, nil
}

func VerifyStridePI0LifecycleEvent(ctx context.Context, keys StridePI0ManagedMACKeyring, event StridePI0LifecycleEvent) error {
	if keys == nil || event.validateUnsigned() != nil || stridePI0VerifyEventCommitments(ctx, keys, event) != nil || !isHexDigest(event.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, event.KeyID, event.KeyVersion)
	if err != nil || !stridePI0ValidKey(key) || key.ID != event.KeyID || key.Version != event.KeyVersion {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0EventPayload(event)
	if err != nil || !hmac.Equal([]byte(event.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

func DecodeAndVerifyStridePI0LifecycleEvent(ctx context.Context, keys StridePI0ManagedMACKeyring, raw []byte) (StridePI0LifecycleEvent, error) {
	var event StridePI0LifecycleEvent
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil || stridePI0EnsureEOF(decoder) != nil || VerifyStridePI0LifecycleEvent(ctx, keys, event) != nil {
		return StridePI0LifecycleEvent{}, ErrStridePI0Invalid
	}
	canonical, err := canonicalJSON(event)
	if err != nil || !bytes.Equal(bytes.TrimSpace(raw), canonical) {
		return StridePI0LifecycleEvent{}, ErrStridePI0Invalid
	}
	return event, nil
}

func VerifyStridePI0TraceGraph(ctx context.Context, keys StridePI0ManagedMACKeyring, events []StridePI0LifecycleEvent) error {
	if len(events) < 1 || len(events) > 4096 {
		return ErrStridePI0Invalid
	}
	seen := map[string]StridePI0LifecycleEvent{}
	lastRevision := map[string]int64{}
	seenDigest := map[string]string{}
	rootCount := 0
	var tenant, trace string
	for i, event := range events {
		if VerifyStridePI0LifecycleEvent(ctx, keys, event) != nil || seen[event.EventID].EventID != "" {
			return ErrStridePI0Invalid
		}
		if i == 0 {
			tenant, trace = event.TenantID, event.TraceID
		}
		if event.TenantID != tenant || event.TraceID != trace {
			return ErrStridePI0Invalid
		}
		if event.ParentEventID == "" {
			rootCount++
		} else {
			parent, ok := seen[event.ParentEventID]
			if !ok || event.OccurredAt.Before(parent.OccurredAt) || event.EffectiveAt.Before(parent.EffectiveAt) || stridePI0EventTypes[event.EventType] < stridePI0EventTypes[parent.EventType] && !strings.HasPrefix(event.EventType, "lifecycle.") {
				return ErrStridePI0Invalid
			}
		}
		aggregateKey := event.Aggregate.Type + "\x00" + event.Aggregate.ID
		if prior := lastRevision[aggregateKey]; prior != 0 {
			if event.Aggregate.Revision != prior+1 || event.Aggregate.Digest == seenDigest[aggregateKey] {
				return ErrStridePI0Invalid
			}
		}
		lastRevision[aggregateKey] = event.Aggregate.Revision
		seenDigest[aggregateKey] = event.Aggregate.Digest
		seen[event.EventID] = event
	}
	if rootCount != 1 || events[0].EventType != "source.bound_to_trace" {
		return ErrStridePI0Invalid
	}
	return nil
}

type StridePI0Postimage struct {
	Store     string `json:"store"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	Digest    string `json:"digest"`
	HighWater int64  `json:"highWater"`
}

type StridePI0RecoveryAuthority interface {
	WithStridePI0RecoveryAuthority(context.Context, string, string, func() error) error
}

type StridePI0EffectReceipt struct {
	Schema               string               `json:"schema"`
	ReceiptID            string               `json:"receiptId"`
	OperationID          string               `json:"operationId"`
	OperationFingerprint string               `json:"operationFingerprint"`
	AdapterID            string               `json:"adapterId"`
	AdapterOperationID   string               `json:"adapterOperationId"`
	Outcome              string               `json:"outcome"`
	Postimages           []StridePI0Postimage `json:"postimages"`
	IssuedAt             time.Time            `json:"issuedAt"`
	EvidenceKeyID        string               `json:"evidenceKeyId"`
	EvidenceKeyVersion   uint64               `json:"evidenceKeyVersion"`
	MAC                  string               `json:"mac"`
}

type StridePI0EffectReceiptResolver interface {
	ResolveStridePI0EffectReceipt(context.Context, string, string, string) (StridePI0EffectReceipt, error)
}

func (r StridePI0EffectReceipt) validateUnsigned() error {
	if r.Schema != "stride.pi0.effect-receipt.v1" || !strideIdentifier(r.ReceiptID) || !strideIdentifier(r.OperationID) || !isHexDigest(r.OperationFingerprint) || !strideIdentifier(r.AdapterID) || !strideIdentifier(r.AdapterOperationID) || !oneOf(r.Outcome, "applied", "not_applied", "compensated") || len(r.Postimages) < 1 || len(r.Postimages) > 32 || !stridePI0ValidPostimages(r.Postimages) || r.IssuedAt.IsZero() || !strideIdentifier(r.EvidenceKeyID) || r.EvidenceKeyVersion < 1 {
		return ErrStridePI0Invalid
	}
	return nil
}

func SealStridePI0EffectReceipt(ctx context.Context, keys StridePI0EffectEvidenceKeyring, receipt StridePI0EffectReceipt) (StridePI0EffectReceipt, error) {
	if keys == nil {
		return StridePI0EffectReceipt{}, ErrStridePI0Unavailable
	}
	key, err := keys.CurrentStridePI0EffectEvidenceKey(ctx)
	if err != nil || !stridePI0ValidEvidenceKey(key) {
		return StridePI0EffectReceipt{}, ErrStridePI0Unavailable
	}
	receipt.Schema, receipt.EvidenceKeyID, receipt.EvidenceKeyVersion, receipt.MAC = "stride.pi0.effect-receipt.v1", key.ID, key.Version, ""
	if receipt.validateUnsigned() != nil {
		return StridePI0EffectReceipt{}, ErrStridePI0Invalid
	}
	payload, err := stridePI0EffectReceiptPayload(receipt)
	if err != nil {
		return StridePI0EffectReceipt{}, ErrStridePI0Invalid
	}
	receipt.MAC = stridePI0MAC(key.Secret, payload)
	return receipt, nil
}

func VerifyStridePI0EffectReceipt(ctx context.Context, keys StridePI0EffectEvidenceKeyring, receipt StridePI0EffectReceipt) error {
	if keys == nil || receipt.validateUnsigned() != nil || !isHexDigest(receipt.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := keys.ResolveStridePI0EffectEvidenceKey(ctx, receipt.EvidenceKeyID, receipt.EvidenceKeyVersion)
	if err != nil || !stridePI0ValidEvidenceKey(key) || key.ID != receipt.EvidenceKeyID || key.Version != receipt.EvidenceKeyVersion {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0EffectReceiptPayload(receipt)
	if err != nil || !hmac.Equal([]byte(receipt.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

func (p StridePI0Postimage) validate() error {
	if !strideIdentifier(p.Store) || !strideIdentifier(p.Type) || !strideIdentifier(p.ID) || p.Revision < 1 || !isHexDigest(p.Digest) || p.HighWater < 1 {
		return ErrStridePI0Invalid
	}
	return nil
}

type StridePI0JournalEvent struct {
	EventID   string `json:"eventId"`
	EventType string `json:"eventType"`
	Digest    string `json:"digest"`
}

type StridePI0CompoundJournal struct {
	Schema                   string                     `json:"schema"`
	OperationID              string                     `json:"operationId"`
	TenantID                 string                     `json:"tenantId"`
	TraceID                  string                     `json:"traceId"`
	Aggregate                StridePI0Aggregate         `json:"aggregate"`
	Principal                StridePI0Principal         `json:"principal"`
	OperationFingerprint     string                     `json:"operationFingerprint"`
	OperationCommitment      StridePI0ManagedCommitment `json:"operationCommitment"`
	IdempotencyCommitment    StridePI0ManagedCommitment `json:"idempotencyCommitment"`
	RequestedEvents          []StridePI0JournalEvent    `json:"requestedEvents"`
	Preimages                []StridePI0Postimage       `json:"preimages"`
	ExpectedPostimages       []StridePI0Postimage       `json:"expectedPostimages"`
	ActualPostimages         []StridePI0Postimage       `json:"actualPostimages"`
	CommittedEffectPostimage []StridePI0Postimage       `json:"committedEffectPostimage"`
	EffectAdapterID          string                     `json:"effectAdapterId"`
	AdapterOperationID       string                     `json:"adapterOperationId"`
	ExpectedEffectReceiptID  string                     `json:"expectedEffectReceiptId"`
	EffectReceiptDigest      string                     `json:"effectReceiptDigest,omitempty"`
	AuthorityEnvelopeDigest  string                     `json:"authorityEnvelopeDigest"`
	Phase                    string                     `json:"phase"`
	PhaseGeneration          uint64                     `json:"phaseGeneration"`
	Reconciliation           string                     `json:"reconciliation,omitempty"`
	UpdatedAt                time.Time                  `json:"updatedAt"`
	KeyID                    string                     `json:"keyId"`
	KeyVersion               uint64                     `json:"keyVersion"`
	MAC                      string                     `json:"mac"`
}

var stridePI0JournalTransitions = map[string][]string{
	"prepared":            {"effect_requested", "effect_failed", "recovery_required", "denied", "quarantined"},
	"effect_requested":    {"effect_approved", "effect_failed", "recovery_required", "denied", "quarantined"},
	"effect_approved":     {"effect_applied", "effect_failed", "recovery_required", "denied", "quarantined"},
	"effect_applied":      {"events_written", "events_reconciled", "recovery_required", "quarantined"},
	"events_written":      {"postimages_verified", "recovery_required", "quarantined"},
	"postimages_verified": {"committed", "recovery_required", "quarantined"},
	"effect_failed":       {"reconciled", "recovery_required", "quarantined"},
	"recovery_required":   {"effect_reconciled", "quarantined"},
	"effect_reconciled":   {"events_reconciled", "effect_requested"},
	"events_reconciled":   {"postimages_verified", "quarantined"},
	"reconciled":          {"committed"},
	"denied":              {},
	"quarantined":         {},
}

func (j StridePI0CompoundJournal) validateUnsigned() error {
	if j.Schema != stridePI0JournalSchema || !strideIdentifier(j.OperationID) || !strideIdentifier(j.TenantID) || !strideIdentifier(j.TraceID) || j.Aggregate.validate() != nil || j.Principal.validate() != nil || !isHexDigest(j.OperationFingerprint) || j.OperationCommitment.validate(stridePI0OperationDomain) != nil || j.IdempotencyCommitment.validate(stridePI0IdempotencyDomain) != nil || len(j.RequestedEvents) < 1 || len(j.RequestedEvents) > 16 || len(j.Preimages) < 1 || len(j.ExpectedPostimages) < 1 || len(j.Preimages) > 32 || len(j.ExpectedPostimages) > 32 || len(j.ActualPostimages) > 32 || len(j.CommittedEffectPostimage) > 32 || !strideIdentifier(j.EffectAdapterID) || !strideIdentifier(j.AdapterOperationID) || !strideIdentifier(j.ExpectedEffectReceiptID) || !validOptionalDigest(j.EffectReceiptDigest) || !isHexDigest(j.AuthorityEnvelopeDigest) || (!oneOf(j.Phase, "prepared", "effect_requested", "effect_approved", "effect_applied", "events_written", "postimages_verified", "committed", "effect_failed", "reconciled", "recovery_required", "effect_reconciled", "events_reconciled", "denied", "quarantined")) || j.PhaseGeneration < 1 || j.UpdatedAt.IsZero() || !strideIdentifier(j.KeyID) || j.KeyVersion < 1 {
		return ErrStridePI0Invalid
	}
	if (j.Phase == "prepared" || j.Phase == "effect_requested" || j.Phase == "effect_approved") && len(j.ActualPostimages) != 0 {
		return ErrStridePI0Invalid
	}
	if oneOf(j.Phase, "prepared", "effect_requested", "effect_approved", "effect_failed", "denied") && len(j.CommittedEffectPostimage) != 0 {
		return ErrStridePI0Invalid
	}
	if j.Reconciliation == "applied" && oneOf(j.Phase, "effect_applied", "recovery_required", "effect_reconciled", "events_written", "events_reconciled", "postimages_verified", "committed") && !stridePI0EqualPostimages(j.ExpectedPostimages, j.CommittedEffectPostimage) {
		return ErrStridePI0Invalid
	}
	if j.Phase == "postimages_verified" && (!stridePI0EqualPostimages(j.ExpectedPostimages, j.ActualPostimages) || j.Reconciliation != "applied") {
		return ErrStridePI0Invalid
	}
	if j.Phase == "committed" && ((j.Reconciliation == "applied" && !stridePI0EqualPostimages(j.ExpectedPostimages, j.ActualPostimages)) || (oneOf(j.Reconciliation, "not_applied", "compensated") && !stridePI0EqualPostimages(j.Preimages, j.ActualPostimages)) || j.Reconciliation == "" || j.Reconciliation == "quarantined") {
		return ErrStridePI0Invalid
	}
	if j.Reconciliation != "" && !oneOf(j.Reconciliation, "applied", "not_applied", "compensated", "quarantined") {
		return ErrStridePI0Invalid
	}
	if oneOf(j.Phase, "effect_applied", "events_written", "postimages_verified", "committed", "effect_reconciled", "events_reconciled") && !isHexDigest(j.EffectReceiptDigest) {
		return ErrStridePI0Invalid
	}
	seenEvents := map[string]bool{}
	for _, event := range j.RequestedEvents {
		if !strideIdentifier(event.EventID) || !stridePI0KnownEventType(event.EventType) || !isHexDigest(event.Digest) || seenEvents[event.EventID] {
			return ErrStridePI0Invalid
		}
		seenEvents[event.EventID] = true
	}
	for _, images := range [][]StridePI0Postimage{j.Preimages, j.ExpectedPostimages, j.ActualPostimages, j.CommittedEffectPostimage} {
		if !stridePI0ValidPostimages(images) {
			return ErrStridePI0Invalid
		}
	}
	return nil
}

func PrepareStridePI0CompoundJournal(ctx context.Context, keys StridePI0ManagedMACKeyring, authority StridePI0CurrentAuthority, journal StridePI0CompoundJournal) (StridePI0CompoundJournal, error) {
	if authority == nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	journal.Schema, journal.Phase, journal.PhaseGeneration, journal.Reconciliation, journal.ActualPostimages, journal.CommittedEffectPostimage, journal.MAC = stridePI0JournalSchema, "prepared", 1, "", nil, nil, ""
	operationCommitment, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0OperationDomain, journal.TenantID, journal.OperationID, journal.OperationFingerprint)
	if err != nil {
		return StridePI0CompoundJournal{}, err
	}
	journal.OperationCommitment = operationCommitment
	commitment, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0IdempotencyDomain, journal.TenantID, journal.OperationID, journal.OperationFingerprint)
	if err != nil {
		return StridePI0CompoundJournal{}, err
	}
	journal.IdempotencyCommitment = commitment
	var sealed StridePI0CompoundJournal
	err = authority.WithCurrentStridePI0Principal(ctx, journal.Principal, func() error {
		var sealErr error
		sealed, sealErr = stridePI0SealJournal(ctx, keys, journal)
		return sealErr
	})
	if err != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	return sealed, nil
}

func TransitionStridePI0CompoundJournal(ctx context.Context, keys StridePI0ManagedMACKeyring, authority StridePI0CurrentAuthority, current StridePI0CompoundJournal, nextPhase string, actual []StridePI0Postimage, reconciliation string, at time.Time) (StridePI0CompoundJournal, error) {
	if authority == nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	var next StridePI0CompoundJournal
	err := authority.WithCurrentStridePI0Principal(ctx, current.Principal, func() error {
		var transitionErr error
		next, transitionErr = stridePI0TransitionJournal(ctx, keys, current, nextPhase, actual, reconciliation, "", at)
		return transitionErr
	})
	if err != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	return next, nil
}

func stridePI0TransitionJournal(ctx context.Context, keys StridePI0ManagedMACKeyring, current StridePI0CompoundJournal, nextPhase string, actual []StridePI0Postimage, reconciliation, effectReceiptDigest string, at time.Time) (StridePI0CompoundJournal, error) {
	if VerifyStridePI0CompoundJournal(ctx, keys, current) != nil || !containsSTRIDEString(stridePI0JournalTransitions[current.Phase], nextPhase) || at.IsZero() || at.Before(current.UpdatedAt) {
		return StridePI0CompoundJournal{}, ErrStridePI0Conflict
	}
	next := current
	next.Phase, next.PhaseGeneration, next.UpdatedAt, next.MAC = nextPhase, current.PhaseGeneration+1, at.UTC(), ""
	next.ActualPostimages = append([]StridePI0Postimage(nil), actual...)
	next.Reconciliation = reconciliation
	if reconciliation == "applied" && oneOf(nextPhase, "effect_applied", "recovery_required", "effect_reconciled") {
		next.CommittedEffectPostimage = append([]StridePI0Postimage(nil), actual...)
	}
	if effectReceiptDigest != "" {
		next.EffectReceiptDigest = effectReceiptDigest
	}
	if oneOf(nextPhase, "effect_applied", "effect_failed", "events_written", "effect_reconciled", "events_reconciled", "postimages_verified", "committed", "reconciled") && len(actual) == 0 {
		return StridePI0CompoundJournal{}, ErrStridePI0Invalid
	}
	return stridePI0SealJournal(ctx, keys, next)
}

type StridePI0PostimageReader interface {
	ReadStridePI0Postimages(context.Context, string, []StridePI0Postimage) ([]StridePI0Postimage, error)
}

// RecoverStridePI0CompoundJournal never invokes the external effect. It reads
// the exact authoritative destinations and moves only to a recovery phase. A
// caller must persist the returned sealed phase before any compensation or
// missing-event append. This is the pre-seal recovery boundary.
func RecoverStridePI0CompoundJournal(ctx context.Context, keys StridePI0ManagedMACKeyring, evidenceKeys StridePI0EffectEvidenceKeyring, recoveryAuthority StridePI0RecoveryAuthority, reader StridePI0PostimageReader, receipts StridePI0EffectReceiptResolver, current StridePI0CompoundJournal, at time.Time) (StridePI0CompoundJournal, error) {
	if recoveryAuthority == nil || reader == nil || receipts == nil || VerifyStridePI0CompoundJournal(ctx, keys, current) != nil || current.Phase == "committed" || current.Phase == "denied" || current.Phase == "quarantined" {
		return StridePI0CompoundJournal{}, ErrStridePI0Invalid
	}
	var next StridePI0CompoundJournal
	err := recoveryAuthority.WithStridePI0RecoveryAuthority(ctx, current.OperationID, current.OperationFingerprint, func() error {
		actual, readErr := reader.ReadStridePI0Postimages(ctx, current.OperationID, append([]StridePI0Postimage(nil), current.ExpectedPostimages...))
		if readErr != nil || len(actual) == 0 || !stridePI0ValidPostimages(actual) {
			return ErrStridePI0Unavailable
		}
		receipt, receiptErr := receipts.ResolveStridePI0EffectReceipt(ctx, current.EffectAdapterID, current.AdapterOperationID, current.ExpectedEffectReceiptID)
		if receiptErr != nil || VerifyStridePI0EffectReceipt(ctx, evidenceKeys, receipt) != nil || receipt.ReceiptID != current.ExpectedEffectReceiptID || receipt.OperationID != current.OperationID || receipt.OperationFingerprint != current.OperationFingerprint || receipt.AdapterID != current.EffectAdapterID || receipt.AdapterOperationID != current.AdapterOperationID || !stridePI0EqualPostimages(receipt.Postimages, actual) {
			var transitionErr error
			next, transitionErr = stridePI0TransitionJournal(ctx, keys, current, "quarantined", actual, "quarantined", "", at)
			return transitionErr
		}
		receiptRaw, digestErr := canonicalJSON(receipt)
		if digestErr != nil {
			return ErrStridePI0Invalid
		}
		receiptSum := sha256.Sum256(receiptRaw)
		receiptDigest := hex.EncodeToString(receiptSum[:])
		reconciliation := "quarantined"
		if receipt.Outcome == "applied" && stridePI0EqualPostimages(current.ExpectedPostimages, actual) {
			reconciliation = "applied"
		} else if oneOf(receipt.Outcome, "not_applied", "compensated") && stridePI0EqualPostimages(current.Preimages, actual) {
			reconciliation = receipt.Outcome
		}
		nextPhase := "recovery_required"
		if current.Phase == "recovery_required" {
			nextPhase = "effect_reconciled"
		}
		var transitionErr error
		next, transitionErr = stridePI0TransitionJournal(ctx, keys, current, nextPhase, actual, reconciliation, receiptDigest, at)
		return transitionErr
	})
	if err != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	return next, nil
}

// RecordStridePI0EffectReceipt is the normal-path bridge from approved to an
// independently receipted effect. It does not append lifecycle events.
func RecordStridePI0EffectReceipt(ctx context.Context, keys StridePI0ManagedMACKeyring, evidenceKeys StridePI0EffectEvidenceKeyring, authority StridePI0CurrentAuthority, current StridePI0CompoundJournal, receipt StridePI0EffectReceipt, at time.Time) (StridePI0CompoundJournal, error) {
	if authority == nil || current.Phase != "effect_approved" || VerifyStridePI0CompoundJournal(ctx, keys, current) != nil || VerifyStridePI0EffectReceipt(ctx, evidenceKeys, receipt) != nil || receipt.ReceiptID != current.ExpectedEffectReceiptID || receipt.OperationID != current.OperationID || receipt.OperationFingerprint != current.OperationFingerprint || receipt.AdapterID != current.EffectAdapterID || receipt.AdapterOperationID != current.AdapterOperationID {
		return StridePI0CompoundJournal{}, ErrStridePI0Invalid
	}
	raw, err := canonicalJSON(receipt)
	if err != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Invalid
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	nextPhase, reconciliation := "effect_failed", receipt.Outcome
	if receipt.Outcome == "applied" {
		nextPhase, reconciliation = "effect_applied", "applied"
	}
	var next StridePI0CompoundJournal
	err = authority.WithCurrentStridePI0Principal(ctx, current.Principal, func() error {
		var transitionErr error
		next, transitionErr = stridePI0TransitionJournal(ctx, keys, current, nextPhase, receipt.Postimages, reconciliation, digest, at)
		return transitionErr
	})
	if err != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	return next, nil
}

// TerminalizeStridePI0CompoundJournal records only a body-free denial or
// quarantine under recovery authority. It invokes no product/provider effect.
func TerminalizeStridePI0CompoundJournal(ctx context.Context, keys StridePI0ManagedMACKeyring, recoveryAuthority StridePI0RecoveryAuthority, current StridePI0CompoundJournal, terminal string, actual []StridePI0Postimage, at time.Time) (StridePI0CompoundJournal, error) {
	if recoveryAuthority == nil || !oneOf(terminal, "denied", "quarantined") || len(actual) == 0 {
		return StridePI0CompoundJournal{}, ErrStridePI0Invalid
	}
	reconciliation := "quarantined"
	if terminal == "denied" {
		if !stridePI0EqualPostimages(current.Preimages, actual) {
			return StridePI0CompoundJournal{}, ErrStridePI0Invalid
		}
		reconciliation = "not_applied"
	}
	var next StridePI0CompoundJournal
	err := recoveryAuthority.WithStridePI0RecoveryAuthority(ctx, current.OperationID, current.OperationFingerprint, func() error {
		var transitionErr error
		next, transitionErr = stridePI0TransitionJournal(ctx, keys, current, terminal, actual, reconciliation, "", at)
		return transitionErr
	})
	if err != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	return next, nil
}

func VerifyStridePI0CompoundJournal(ctx context.Context, keys StridePI0ManagedMACKeyring, journal StridePI0CompoundJournal) error {
	if keys == nil || journal.validateUnsigned() != nil || VerifyStridePI0ManagedCommitment(ctx, keys, journal.OperationCommitment, stridePI0OperationDomain, journal.TenantID, journal.OperationID, journal.OperationFingerprint) != nil || VerifyStridePI0ManagedCommitment(ctx, keys, journal.IdempotencyCommitment, stridePI0IdempotencyDomain, journal.TenantID, journal.OperationID, journal.OperationFingerprint) != nil || !isHexDigest(journal.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, journal.KeyID, journal.KeyVersion)
	if err != nil || !stridePI0ValidKey(key) {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0JournalPayload(journal)
	if err != nil || !hmac.Equal([]byte(journal.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

func stridePI0SealJournal(ctx context.Context, keys StridePI0ManagedMACKeyring, journal StridePI0CompoundJournal) (StridePI0CompoundJournal, error) {
	if keys == nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil || !stridePI0ValidKey(key) {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	journal.KeyID, journal.KeyVersion, journal.MAC = key.ID, key.Version, ""
	if journal.validateUnsigned() != nil || VerifyStridePI0ManagedCommitment(ctx, keys, journal.OperationCommitment, stridePI0OperationDomain, journal.TenantID, journal.OperationID, journal.OperationFingerprint) != nil || VerifyStridePI0ManagedCommitment(ctx, keys, journal.IdempotencyCommitment, stridePI0IdempotencyDomain, journal.TenantID, journal.OperationID, journal.OperationFingerprint) != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Invalid
	}
	payload, err := stridePI0JournalPayload(journal)
	if err != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Invalid
	}
	journal.MAC = stridePI0MAC(key.Secret, payload)
	return journal, nil
}

func stridePI0JournalPayload(journal StridePI0CompoundJournal) ([]byte, error) {
	journal.MAC = ""
	return canonicalJSON(struct {
		Domain  string                   `json:"domain"`
		Journal StridePI0CompoundJournal `json:"journal"`
	}{stridePI0JournalMACDomain, journal})
}

type StridePI0MetricDefinition struct {
	MetricID           string   `json:"metricId"`
	Revision           int64    `json:"revision"`
	EligibleEventTypes []string `json:"eligibleEventTypes"`
	Numerator          string   `json:"numerator"`
	Denominator        string   `json:"denominator"`
	Unit               string   `json:"unit"`
	TimeOrigin         string   `json:"timeOrigin"`
	TimeTerminal       string   `json:"timeTerminal"`
	UnknownRule        string   `json:"unknownRule"`
	WindowDays         []int    `json:"windowDays"`
	SuppressionMinimum int      `json:"suppressionMinimum"`
	Purpose            string   `json:"purpose"`
	OwnerRole          string   `json:"ownerRole"`
	ReviewerRole       string   `json:"reviewerRole"`
}

var stridePI0MetricIDs = []string{"artifact_adoption", "attribution_failure", "completion_correction", "human_intervention", "permission_failure", "provider_model_cost", "qualitative_trust_pull", "reliability", "repeat_use", "retention", "time_to_useful_outcome"}

type stridePI0MetricSpec struct {
	events      []string
	numerator   string
	denominator string
	unit        string
	origin      string
	terminal    string
	windows     []int
}

var stridePI0MetricSpecs = map[string]stridePI0MetricSpec{
	"artifact_adoption": {
		events: []string{"artifact.adopted", "artifact.rejected", "artifact.withdrawn"}, numerator: "exact_adopted_artifacts", denominator: "eligible_reviewed_artifacts", unit: "rate",
	},
	"attribution_failure": {
		events: []string{"artifact.created", "outcome.recorded"}, numerator: "eligible_objects_missing_exact_attribution", denominator: "eligible_artifacts_and_outcomes", unit: "rate",
	},
	"completion_correction": {
		events: []string{"outcome.corrected", "outcome.withdrawn", "run.completed", "work_record.corrected", "work_record.revoked"}, numerator: "completed_corrected_or_withdrawn_traces", denominator: "eligible_admitted_traces_with_closed_window", unit: "rate",
	},
	"human_intervention": {
		events: []string{"run.intervention_requested", "run.intervention_resolved"}, numerator: "runs_with_exact_intervention_request", denominator: "eligible_started_runs", unit: "rate",
	},
	"permission_failure": {
		events: []string{"effect.failed", "lifecycle.revoked", "run.failed"}, numerator: "current_authority_denials_and_revocations", denominator: "eligible_boundary_requests", unit: "rate",
	},
	"provider_model_cost": {
		events: []string{"effect.applied", "run.completed"}, numerator: "authorized_metering_receipt_currency_minor", denominator: "eligible_metered_runs_and_effects", unit: "currency_minor",
	},
	"qualitative_trust_pull": {
		events: []string{"research.case_closed"}, numerator: "consented_closed_codebook_responses", denominator: "eligible_consented_research_cases", unit: "qualitative_count",
	},
	"reliability": {
		events: []string{"effect.failed", "effect.reconciled", "lifecycle.reconciled", "run.cancelled", "run.completed", "run.failed"}, numerator: "closed_terminal_and_reconciliation_classes", denominator: "eligible_admitted_runs_and_effects", unit: "rate",
	},
	"repeat_use": {
		events: []string{"outcome.recorded", "source.bound_to_trace"}, numerator: "opted_in_subjects_with_second_eligible_trace", denominator: "eligible_opted_in_subjects_with_first_completed_trace", unit: "rate", windows: []int{7, 30, 90},
	},
	"retention": {
		events: []string{"outcome.recorded", "source.bound_to_trace"}, numerator: "opted_in_subjects_returning_in_window", denominator: "eligible_uncensored_opted_in_subjects", unit: "rate", windows: []int{7, 30, 90},
	},
	"time_to_useful_outcome": {
		events: []string{"outcome.corrected", "outcome.recorded", "outcome.rejected", "outcome.withdrawn", "source.bound_to_trace"}, numerator: "eligible_traces_with_reviewed_current_outcome", denominator: "eligible_admitted_traces_closed_or_censored", unit: "duration_ms", origin: "source.bound_to_trace", terminal: "outcome.recorded",
	},
}

type StridePI0MetricDefinitionManifest struct {
	Schema                string                      `json:"schema"`
	ManifestID            string                      `json:"manifestId"`
	Revision              int64                       `json:"revision"`
	SourceSchemaDigest    string                      `json:"sourceSchemaDigest"`
	SourceHighWaterDigest string                      `json:"sourceHighWaterDigest"`
	MeasurementRelease    string                      `json:"measurementRelease"`
	ConsentPolicyRef      StridePI0Reference          `json:"consentPolicyRef"`
	CohortPolicyRef       StridePI0Reference          `json:"cohortPolicyRef"`
	Definitions           []StridePI0MetricDefinition `json:"definitions"`
	FrozenAt              time.Time                   `json:"frozenAt"`
	KeyID                 string                      `json:"keyId"`
	KeyVersion            uint64                      `json:"keyVersion"`
	MAC                   string                      `json:"mac"`
}

func (m StridePI0MetricDefinitionManifest) validateUnsigned() error {
	if m.Schema != stridePI0MetricSchema || !strideIdentifier(m.ManifestID) || m.Revision < 1 || !isHexDigest(m.SourceSchemaDigest) || !isHexDigest(m.SourceHighWaterDigest) || !isHexDigest(m.MeasurementRelease) || m.ConsentPolicyRef.Type != "policy" || m.ConsentPolicyRef.validate() != nil || m.CohortPolicyRef.Type != "policy" || m.CohortPolicyRef.validate() != nil || len(m.Definitions) != len(stridePI0MetricIDs) || m.FrozenAt.IsZero() || !strideIdentifier(m.KeyID) || m.KeyVersion < 1 {
		return ErrStridePI0Invalid
	}
	ids := make([]string, 0, len(m.Definitions))
	for _, definition := range m.Definitions {
		spec, known := stridePI0MetricSpecs[definition.MetricID]
		if !known || definition.Revision < 1 || len(definition.EligibleEventTypes) < 1 || len(definition.EligibleEventTypes) > 16 || definition.Numerator != spec.numerator || definition.Denominator != spec.denominator || definition.Unit != spec.unit || definition.TimeOrigin != spec.origin || definition.TimeTerminal != spec.terminal || definition.UnknownRule != "missing_or_incomplete_is_unknown" || definition.Purpose != "founder_product_learning" || !strideIdentifier(definition.OwnerRole) || !strideIdentifier(definition.ReviewerRole) || definition.OwnerRole == definition.ReviewerRole || definition.SuppressionMinimum < 5 || !stridePI0ExactInts(definition.WindowDays, spec.windows) {
			return ErrStridePI0Invalid
		}
		if !sort.StringsAreSorted(definition.EligibleEventTypes) || !stridePI0ExactStrings(definition.EligibleEventTypes, spec.events) {
			return ErrStridePI0Invalid
		}
		for _, eventType := range definition.EligibleEventTypes {
			if !stridePI0KnownEventType(eventType) && eventType != "research.case_closed" {
				return ErrStridePI0Invalid
			}
		}
		ids = append(ids, definition.MetricID)
	}
	sort.Strings(ids)
	if !stridePI0ExactStrings(ids, stridePI0MetricIDs) {
		return ErrStridePI0Invalid
	}
	return nil
}

func SealStridePI0MetricManifest(ctx context.Context, keys StridePI0ManagedMACKeyring, manifest StridePI0MetricDefinitionManifest) (StridePI0MetricDefinitionManifest, error) {
	if keys == nil {
		return StridePI0MetricDefinitionManifest{}, ErrStridePI0Unavailable
	}
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil || !stridePI0ValidKey(key) {
		return StridePI0MetricDefinitionManifest{}, ErrStridePI0Unavailable
	}
	manifest.Schema, manifest.KeyID, manifest.KeyVersion, manifest.MAC = stridePI0MetricSchema, key.ID, key.Version, ""
	if manifest.validateUnsigned() != nil {
		return StridePI0MetricDefinitionManifest{}, ErrStridePI0Invalid
	}
	payload, err := stridePI0MetricPayload(manifest)
	if err != nil {
		return StridePI0MetricDefinitionManifest{}, ErrStridePI0Invalid
	}
	manifest.MAC = stridePI0MAC(key.Secret, payload)
	return manifest, nil
}

func VerifyStridePI0MetricManifest(ctx context.Context, keys StridePI0ManagedMACKeyring, manifest StridePI0MetricDefinitionManifest) error {
	if keys == nil || manifest.validateUnsigned() != nil || !isHexDigest(manifest.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, manifest.KeyID, manifest.KeyVersion)
	if err != nil || !stridePI0ValidKey(key) {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0MetricPayload(manifest)
	if err != nil || !hmac.Equal([]byte(manifest.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

type StridePI0PriorWorkflowComparison struct {
	Schema                  string    `json:"schema"`
	ComparisonID            string    `json:"comparisonId"`
	PriorReleaseDigest      string    `json:"priorReleaseDigest"`
	CurrentReleaseDigest    string    `json:"currentReleaseDigest"`
	FixtureManifestDigest   string    `json:"fixtureManifestDigest"`
	MetricManifestDigest    string    `json:"metricManifestDigest"`
	EligibilityPolicyDigest string    `json:"eligibilityPolicyDigest"`
	AssignmentMethod        string    `json:"assignmentMethod"`
	MissingDataRule         string    `json:"missingDataRule"`
	Hypothesis              string    `json:"hypothesis"`
	SampleSizeRuleDigest    string    `json:"sampleSizeRuleDigest"`
	ObservationWindowDays   int       `json:"observationWindowDays"`
	OwnerRole               string    `json:"ownerRole"`
	ReviewerRole            string    `json:"reviewerRole"`
	FrozenAt                time.Time `json:"frozenAt"`
	KeyID                   string    `json:"keyId"`
	KeyVersion              uint64    `json:"keyVersion"`
	MAC                     string    `json:"mac"`
}

type StridePI0MeasurementArtifactBinding struct {
	Schema     string    `json:"schema"`
	Kind       string    `json:"kind"`
	Digest     string    `json:"digest"`
	Revision   int64     `json:"revision"`
	IssuedAt   time.Time `json:"issuedAt"`
	KeyID      string    `json:"keyId"`
	KeyVersion uint64    `json:"keyVersion"`
	MAC        string    `json:"mac"`
}

const stridePI0MeasurementBindingSchema = "stride.pi0.measurement-artifact-binding.v1"

type StridePI0MeasurementBindingResolver interface {
	ResolveStridePI0MetricManifest(context.Context, string) (StridePI0MetricDefinitionManifest, StridePI0MeasurementArtifactBinding, error)
	ResolveStridePI0MeasurementArtifact(context.Context, string, string) (StridePI0MeasurementArtifactBinding, error)
}

func (b StridePI0MeasurementArtifactBinding) validateUnsigned() error {
	if b.Schema != stridePI0MeasurementBindingSchema || !oneOf(b.Kind, "metric_manifest", "fixture_manifest", "eligibility_policy") || !isHexDigest(b.Digest) || b.Revision < 1 || b.IssuedAt.IsZero() || !strideIdentifier(b.KeyID) || b.KeyVersion < 1 {
		return ErrStridePI0Invalid
	}
	return nil
}

func stridePI0MeasurementBindingPayload(binding StridePI0MeasurementArtifactBinding) ([]byte, error) {
	binding.MAC = ""
	return canonicalJSON(struct {
		Domain  string                              `json:"domain"`
		Binding StridePI0MeasurementArtifactBinding `json:"binding"`
	}{stridePI0BindingMACDomain, binding})
}

func SealStridePI0MeasurementArtifactBinding(ctx context.Context, keys StridePI0ManagedMACKeyring, binding StridePI0MeasurementArtifactBinding) (StridePI0MeasurementArtifactBinding, error) {
	if keys == nil {
		return StridePI0MeasurementArtifactBinding{}, ErrStridePI0Unavailable
	}
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil || !stridePI0ValidKey(key) {
		return StridePI0MeasurementArtifactBinding{}, ErrStridePI0Unavailable
	}
	binding.Schema, binding.KeyID, binding.KeyVersion, binding.MAC = stridePI0MeasurementBindingSchema, key.ID, key.Version, ""
	if binding.validateUnsigned() != nil {
		return StridePI0MeasurementArtifactBinding{}, ErrStridePI0Invalid
	}
	payload, err := stridePI0MeasurementBindingPayload(binding)
	if err != nil {
		return StridePI0MeasurementArtifactBinding{}, ErrStridePI0Invalid
	}
	binding.MAC = stridePI0MAC(key.Secret, payload)
	return binding, nil
}

func VerifyStridePI0MeasurementArtifactBinding(ctx context.Context, keys StridePI0ManagedMACKeyring, binding StridePI0MeasurementArtifactBinding) error {
	if keys == nil || binding.validateUnsigned() != nil || !isHexDigest(binding.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, binding.KeyID, binding.KeyVersion)
	if err != nil || !stridePI0ValidKey(key) || key.ID != binding.KeyID || key.Version != binding.KeyVersion {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0MeasurementBindingPayload(binding)
	if err != nil || !hmac.Equal([]byte(binding.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

func stridePI0MetricManifestDigest(manifest StridePI0MetricDefinitionManifest) (string, error) {
	raw, err := canonicalJSON(manifest)
	if err != nil {
		return "", ErrStridePI0Invalid
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func verifyStridePI0ComparisonBindings(ctx context.Context, keys StridePI0ManagedMACKeyring, resolver StridePI0MeasurementBindingResolver, comparison StridePI0PriorWorkflowComparison) error {
	if resolver == nil {
		return ErrStridePI0Unavailable
	}
	manifest, metricBinding, err := resolver.ResolveStridePI0MetricManifest(ctx, comparison.MetricManifestDigest)
	if err != nil || VerifyStridePI0MetricManifest(ctx, keys, manifest) != nil || VerifyStridePI0MeasurementArtifactBinding(ctx, keys, metricBinding) != nil || metricBinding.Kind != "metric_manifest" || metricBinding.Digest != comparison.MetricManifestDigest || metricBinding.Revision != manifest.Revision || manifest.MeasurementRelease != comparison.CurrentReleaseDigest {
		return ErrStridePI0Unavailable
	}
	digest, err := stridePI0MetricManifestDigest(manifest)
	if err != nil || digest != comparison.MetricManifestDigest {
		return ErrStridePI0Invalid
	}
	for _, target := range []struct{ kind, digest string }{{"fixture_manifest", comparison.FixtureManifestDigest}, {"eligibility_policy", comparison.EligibilityPolicyDigest}} {
		binding, resolveErr := resolver.ResolveStridePI0MeasurementArtifact(ctx, target.kind, target.digest)
		if resolveErr != nil || VerifyStridePI0MeasurementArtifactBinding(ctx, keys, binding) != nil || binding.Kind != target.kind || binding.Digest != target.digest {
			return ErrStridePI0Unavailable
		}
	}
	return nil
}

func (c StridePI0PriorWorkflowComparison) validateUnsigned() error {
	if c.Schema != stridePI0ComparisonSchema || !strideIdentifier(c.ComparisonID) || !isHexDigest(c.PriorReleaseDigest) || !isHexDigest(c.CurrentReleaseDigest) || c.PriorReleaseDigest == c.CurrentReleaseDigest || !isHexDigest(c.FixtureManifestDigest) || !isHexDigest(c.MetricManifestDigest) || !isHexDigest(c.EligibilityPolicyDigest) || !oneOf(c.AssignmentMethod, "paired_identical_fixture", "prospective_randomized", "prospective_matched") || !oneOf(c.MissingDataRule, "unknown_excluded_with_counts", "right_censored_with_counts") || !oneOf(c.Hypothesis, "non_inferiority", "superiority") || !isHexDigest(c.SampleSizeRuleDigest) || c.ObservationWindowDays < 1 || c.ObservationWindowDays > 365 || !strideIdentifier(c.OwnerRole) || !strideIdentifier(c.ReviewerRole) || c.OwnerRole == c.ReviewerRole || c.FrozenAt.IsZero() || !strideIdentifier(c.KeyID) || c.KeyVersion < 1 {
		return ErrStridePI0Invalid
	}
	return nil
}

func SealStridePI0PriorWorkflowComparison(ctx context.Context, keys StridePI0ManagedMACKeyring, resolver StridePI0MeasurementBindingResolver, comparison StridePI0PriorWorkflowComparison) (StridePI0PriorWorkflowComparison, error) {
	if keys == nil || resolver == nil {
		return StridePI0PriorWorkflowComparison{}, ErrStridePI0Unavailable
	}
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil || !stridePI0ValidKey(key) {
		return StridePI0PriorWorkflowComparison{}, ErrStridePI0Unavailable
	}
	comparison.Schema, comparison.KeyID, comparison.KeyVersion, comparison.MAC = stridePI0ComparisonSchema, key.ID, key.Version, ""
	if comparison.validateUnsigned() != nil || verifyStridePI0ComparisonBindings(ctx, keys, resolver, comparison) != nil {
		return StridePI0PriorWorkflowComparison{}, ErrStridePI0Invalid
	}
	payload, err := stridePI0ComparisonPayload(comparison)
	if err != nil {
		return StridePI0PriorWorkflowComparison{}, ErrStridePI0Invalid
	}
	comparison.MAC = stridePI0MAC(key.Secret, payload)
	return comparison, nil
}

func VerifyStridePI0PriorWorkflowComparison(ctx context.Context, keys StridePI0ManagedMACKeyring, resolver StridePI0MeasurementBindingResolver, comparison StridePI0PriorWorkflowComparison) error {
	if keys == nil || resolver == nil || comparison.validateUnsigned() != nil || !isHexDigest(comparison.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, comparison.KeyID, comparison.KeyVersion)
	if err != nil || !stridePI0ValidKey(key) {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0ComparisonPayload(comparison)
	if err != nil || !hmac.Equal([]byte(comparison.MAC), []byte(stridePI0MAC(key.Secret, payload))) || verifyStridePI0ComparisonBindings(ctx, keys, resolver, comparison) != nil {
		return ErrStridePI0Invalid
	}
	return nil
}

func stridePI0MetricPayload(manifest StridePI0MetricDefinitionManifest) ([]byte, error) {
	manifest.MAC = ""
	return canonicalJSON(struct {
		Domain   string                            `json:"domain"`
		Manifest StridePI0MetricDefinitionManifest `json:"manifest"`
	}{stridePI0MetricMACDomain, manifest})
}

func stridePI0ComparisonPayload(comparison StridePI0PriorWorkflowComparison) ([]byte, error) {
	comparison.MAC = ""
	return canonicalJSON(struct {
		Domain     string                           `json:"domain"`
		Comparison StridePI0PriorWorkflowComparison `json:"comparison"`
	}{stridePI0CompareMACDomain, comparison})
}

func stridePI0EffectReceiptPayload(receipt StridePI0EffectReceipt) ([]byte, error) {
	receipt.MAC = ""
	return canonicalJSON(struct {
		Domain  string                 `json:"domain"`
		Receipt StridePI0EffectReceipt `json:"receipt"`
	}{"meetingassist/stride/pi0/effect-receipt/v1", receipt})
}

func stridePI0ValidKey(key StridePI0ManagedMACKey) bool {
	return strideIdentifier(key.ID) && key.Version > 0 && len(key.Secret) >= 32
}

func stridePI0ValidEvidenceKey(key StridePI0EffectEvidenceKey) bool {
	return strideIdentifier(key.ID) && key.Version > 0 && len(key.Secret) >= 32
}

func stridePI0MAC(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func stridePI0KnownEventType(value string) bool {
	_, ok := stridePI0EventTypes[value]
	return ok
}

func stridePI0EventAggregateMatches(eventType, aggregateType string) bool {
	prefix := strings.SplitN(eventType, ".", 2)[0]
	switch prefix {
	case "source", "intent":
		return aggregateType == "intent"
	case "suggestion":
		return aggregateType == "suggestion"
	case "run":
		return aggregateType == "run"
	case "effect":
		return aggregateType == "effect"
	case "artifact":
		return aggregateType == "artifact"
	case "outcome":
		return aggregateType == "outcome"
	case "work_record":
		return aggregateType == "work_record"
	case "publication":
		return aggregateType == "publication"
	case "collaboration":
		return oneOf(aggregateType, "publication", "contact", "block")
	case "lifecycle":
		return oneOf(aggregateType, "intent", "suggestion", "run", "effect", "artifact", "outcome", "work_record", "publication", "contact", "block", "journal")
	default:
		return false
	}
}

func stridePI0ValidEventDecision(eventType, decision string) bool {
	allowed := map[string][]string{
		"intent.rejected":                  {"rejected"},
		"run.intervention_resolved":        {"approved", "denied", "supplied", "expired", "cancelled"},
		"effect.failed":                    {"not_applied", "partial_unknown", "compensated"},
		"artifact.review_decided":          {"approved", "changes_requested", "rejected"},
		"artifact.verification_recorded":   {"passed", "failed", "partial", "unknown"},
		"artifact.rejected":                {"rejected"},
		"outcome.rejected":                 {"rejected"},
		"work_record.subject_decided":      {"approved", "denied"},
		"work_record.named_party_decided":  {"approved", "denied"},
		"work_record.organization_decided": {"approved", "denied"},
		"collaboration.contact_decided":    {"accepted", "declined", "withdrawn", "expired"},
	}
	values, requiresDecision := allowed[eventType]
	if !requiresDecision {
		return decision == ""
	}
	return containsSTRIDEString(values, decision)
}

func stridePI0ValidEventState(eventType, state string) bool {
	allowed := map[string][]string{
		"run.state_changed":                 {"queued", "running", "waiting", "completed", "failed", "cancelled"},
		"run.intervention_requested":        {"input", "review", "effect_approval", "source_refresh", "recovery"},
		"effect.reconciled":                 {"applied", "not_applied", "compensated", "quarantined"},
		"artifact.publication_changed":      {"private", "organization", "exact_link", "revoked", "expired"},
		"publication.network_state_changed": {"off", "draft", "live", "paused", "deleted"},
		"collaboration.block_changed":       {"active", "revoked"},
		"lifecycle.reconciled":              {"applied", "not_applied", "compensated", "quarantined"},
	}
	values, required := allowed[eventType]
	if !required {
		return state == ""
	}
	return containsSTRIDEString(values, state)
}

func stridePI0ValidProvenanceRefs(provenance string, refs []StridePI0Reference) bool {
	if provenance == "model_assisted" {
		if len(refs) != 3 {
			return false
		}
		types := []string{refs[0].Type, refs[1].Type, refs[2].Type}
		sort.Strings(types)
		return stridePI0ExactStrings(types, []string{"evidence_manifest", "model", "prompt_config"})
	}
	if oneOf(provenance, "tool_result", "provider_import") {
		return len(refs) == 1 && refs[0].Type == "provider_receipt"
	}
	return len(refs) == 0
}

func stridePI0RetentionMatchesEvent(eventType, retentionClass string) bool {
	if strings.HasPrefix(eventType, "source.") {
		return retentionClass == "source_link_short"
	}
	if eventType == "lifecycle.purged" {
		return retentionClass == "purge_receipt_body_free"
	}
	if strings.HasPrefix(eventType, "work_record.") || strings.HasPrefix(eventType, "publication.") || strings.HasPrefix(eventType, "collaboration.") {
		return retentionClass == "authorized_disclosure_audit"
	}
	return retentionClass == "private_work_lifecycle"
}

func stridePI0UniqueIdentifiers(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if !strideIdentifier(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func stridePI0UniqueReferences(values []StridePI0Reference) bool {
	seen := map[string]bool{}
	for _, value := range values {
		key := fmt.Sprintf("%s\x00%s\x00%d\x00%s", value.Type, value.ID, value.Revision, value.Digest)
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func stridePI0ReferencesSorted(values []StridePI0Reference) bool {
	for i := 1; i < len(values); i++ {
		prior := fmt.Sprintf("%s\x00%s\x00%020d\x00%s", values[i-1].Type, values[i-1].ID, values[i-1].Revision, values[i-1].Digest)
		current := fmt.Sprintf("%s\x00%s\x00%020d\x00%s", values[i].Type, values[i].ID, values[i].Revision, values[i].Digest)
		if prior >= current {
			return false
		}
	}
	return true
}

func stridePI0ValidPostimages(values []StridePI0Postimage) bool {
	seen := map[string]bool{}
	for _, value := range values {
		key := value.Store + "\x00" + value.Type + "\x00" + value.ID
		if value.validate() != nil || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func stridePI0EqualPostimages(left, right []StridePI0Postimage) bool {
	if len(left) != len(right) {
		return false
	}
	l, r := append([]StridePI0Postimage(nil), left...), append([]StridePI0Postimage(nil), right...)
	sort.Slice(l, func(i, j int) bool {
		return l[i].Store+"\x00"+l[i].Type+"\x00"+l[i].ID < l[j].Store+"\x00"+l[j].Type+"\x00"+l[j].ID
	})
	sort.Slice(r, func(i, j int) bool {
		return r[i].Store+"\x00"+r[i].Type+"\x00"+r[i].ID < r[j].Store+"\x00"+r[j].Type+"\x00"+r[j].ID
	})
	for i := range l {
		if l[i] != r[i] {
			return false
		}
	}
	return true
}

func stridePI0ExactStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func stridePI0ExactInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

const (
	stridePI0CarrierSchema       = "stride.pi0.file-carrier.v1"
	stridePI0CarrierTxnSchema    = "stride.pi0.file-carrier-transaction.v1"
	stridePI0AppendReceiptSchema = "stride.pi0.event-append-receipt.v1"
)

// StridePI0CarrierHighWater is deliberately held outside the carrier file.
// An implementation must provide monotonic compare-and-swap persistence; the
// carrier refuses to open if its authenticated file is behind or ahead of it.
type StridePI0CarrierHighWater struct {
	Generation uint64 `json:"generation"`
	Digest     string `json:"digest"`
}

type StridePI0CarrierHighWaterStore interface {
	ReadStridePI0CarrierHighWater(context.Context, string) (StridePI0CarrierHighWater, error)
	CompareAndSwapStridePI0CarrierHighWater(context.Context, string, StridePI0CarrierHighWater, StridePI0CarrierHighWater) error
}

type StridePI0EventAppendReceipt struct {
	Schema                   string                  `json:"schema"`
	ReceiptID                string                  `json:"receiptId"`
	OperationID              string                  `json:"operationId"`
	OperationFingerprint     string                  `json:"operationFingerprint"`
	JournalPhaseGeneration   uint64                  `json:"journalPhaseGeneration"`
	CommittedEffectPostimage []StridePI0Postimage    `json:"committedEffectPostimage"`
	RecoveryFencePostimage   StridePI0Postimage      `json:"recoveryFencePostimage"`
	Events                   []StridePI0JournalEvent `json:"events"`
	CarrierGeneration        uint64                  `json:"carrierGeneration"`
	CarrierHighWater         uint64                  `json:"carrierHighWater"`
	IssuedAt                 time.Time               `json:"issuedAt"`
	KeyID                    string                  `json:"keyId"`
	KeyVersion               uint64                  `json:"keyVersion"`
	MAC                      string                  `json:"mac"`
}

func (r StridePI0EventAppendReceipt) validateUnsigned() error {
	if r.Schema != stridePI0AppendReceiptSchema || !strideIdentifier(r.ReceiptID) || !strideIdentifier(r.OperationID) || !isHexDigest(r.OperationFingerprint) || r.JournalPhaseGeneration < 1 || len(r.CommittedEffectPostimage) < 1 || !stridePI0ValidPostimages(r.CommittedEffectPostimage) || r.RecoveryFencePostimage.validate() != nil || len(r.Events) < 1 || len(r.Events) > 16 || r.CarrierGeneration < 1 || r.CarrierHighWater < 1 || r.IssuedAt.IsZero() || !strideIdentifier(r.KeyID) || r.KeyVersion < 1 {
		return ErrStridePI0Invalid
	}
	seen := map[string]bool{}
	for _, event := range r.Events {
		if !strideIdentifier(event.EventID) || !stridePI0KnownEventType(event.EventType) || !isHexDigest(event.Digest) || seen[event.EventID] {
			return ErrStridePI0Invalid
		}
		seen[event.EventID] = true
	}
	return nil
}

func stridePI0AppendReceiptPayload(receipt StridePI0EventAppendReceipt) ([]byte, error) {
	receipt.MAC = ""
	return canonicalJSON(struct {
		Domain  string                      `json:"domain"`
		Receipt StridePI0EventAppendReceipt `json:"receipt"`
	}{stridePI0AppendMACDomain, receipt})
}

func stridePI0SealAppendReceipt(ctx context.Context, keys StridePI0ManagedMACKeyring, receipt StridePI0EventAppendReceipt) (StridePI0EventAppendReceipt, error) {
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil || !stridePI0ValidKey(key) {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Unavailable
	}
	receipt.Schema, receipt.KeyID, receipt.KeyVersion, receipt.MAC = stridePI0AppendReceiptSchema, key.ID, key.Version, ""
	if receipt.validateUnsigned() != nil {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Invalid
	}
	payload, err := stridePI0AppendReceiptPayload(receipt)
	if err != nil {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Invalid
	}
	receipt.MAC = stridePI0MAC(key.Secret, payload)
	return receipt, nil
}

func VerifyStridePI0EventAppendReceipt(ctx context.Context, keys StridePI0ManagedMACKeyring, receipt StridePI0EventAppendReceipt) error {
	if keys == nil || receipt.validateUnsigned() != nil || !isHexDigest(receipt.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, receipt.KeyID, receipt.KeyVersion)
	if err != nil || !stridePI0ValidKey(key) || key.ID != receipt.KeyID || key.Version != receipt.KeyVersion {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0AppendReceiptPayload(receipt)
	if err != nil || !hmac.Equal([]byte(receipt.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

type stridePI0CarrierState struct {
	Schema         string                        `json:"schema"`
	Generation     uint64                        `json:"generation"`
	HighWater      uint64                        `json:"highWater"`
	Journals       []StridePI0CompoundJournal    `json:"journals"`
	Events         []StridePI0LifecycleEvent     `json:"events"`
	AppendReceipts []StridePI0EventAppendReceipt `json:"appendReceipts"`
	KeyID          string                        `json:"keyId"`
	KeyVersion     uint64                        `json:"keyVersion"`
	MAC            string                        `json:"mac"`
}

type stridePI0CarrierTransaction struct {
	Schema     string                    `json:"schema"`
	Prior      StridePI0CarrierHighWater `json:"prior"`
	Next       StridePI0CarrierHighWater `json:"next"`
	NextState  stridePI0CarrierState     `json:"nextState"`
	KeyID      string                    `json:"keyId"`
	KeyVersion uint64                    `json:"keyVersion"`
	MAC        string                    `json:"mac"`
}

type StridePI0FileCarrier struct {
	path      string
	lockPath  string
	txnPath   string
	keys      StridePI0ManagedMACKeyring
	highWater StridePI0CarrierHighWaterStore
	mu        sync.Mutex
}

func OpenStridePI0FileCarrier(ctx context.Context, path string, keys StridePI0ManagedMACKeyring, highWater StridePI0CarrierHighWaterStore) (*StridePI0FileCarrier, error) {
	if keys == nil || highWater == nil || path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrStridePI0Invalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, ErrStridePI0Unavailable
	}
	carrier := &StridePI0FileCarrier{path: path, lockPath: path + ".lock", txnPath: path + ".txn", keys: keys, highWater: highWater}
	err := carrier.withLock(func() error {
		if _, txnErr := os.Stat(carrier.txnPath); txnErr == nil {
			if err := carrier.recoverLocked(ctx); err != nil {
				return err
			}
			_, err := carrier.readLocked(ctx)
			return err
		} else if !errors.Is(txnErr, os.ErrNotExist) {
			return ErrStridePI0Unavailable
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			state := stridePI0CarrierState{Schema: stridePI0CarrierSchema, Generation: 1, HighWater: 1}
			sealed, err := stridePI0SealCarrierState(ctx, keys, state)
			if err != nil {
				return err
			}
			return carrier.commitLocked(ctx, stridePI0CarrierState{}, sealed)
		} else if err != nil {
			return ErrStridePI0Unavailable
		}
		if err := carrier.recoverLocked(ctx); err != nil {
			return err
		}
		_, err := carrier.readLocked(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return carrier, nil
}

func (c *StridePI0FileCarrier) withLock(effect func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, err := os.OpenFile(c.lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return ErrStridePI0Unavailable
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return ErrStridePI0Unavailable
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return effect()
}

func stridePI0CarrierStatePayload(state stridePI0CarrierState) ([]byte, error) {
	state.MAC = ""
	return canonicalJSON(struct {
		Domain string                `json:"domain"`
		State  stridePI0CarrierState `json:"state"`
	}{stridePI0CarrierMACDomain, state})
}

func stridePI0SealCarrierState(ctx context.Context, keys StridePI0ManagedMACKeyring, state stridePI0CarrierState) (stridePI0CarrierState, error) {
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil || !stridePI0ValidKey(key) {
		return stridePI0CarrierState{}, ErrStridePI0Unavailable
	}
	state.Schema, state.KeyID, state.KeyVersion, state.MAC = stridePI0CarrierSchema, key.ID, key.Version, ""
	if err := stridePI0ValidateCarrierCollections(ctx, keys, state); err != nil {
		return stridePI0CarrierState{}, err
	}
	payload, err := stridePI0CarrierStatePayload(state)
	if err != nil {
		return stridePI0CarrierState{}, ErrStridePI0Invalid
	}
	state.MAC = stridePI0MAC(key.Secret, payload)
	return state, nil
}

func stridePI0VerifyCarrierState(ctx context.Context, keys StridePI0ManagedMACKeyring, state stridePI0CarrierState) error {
	if state.Schema != stridePI0CarrierSchema || state.Generation < 1 || state.HighWater < 1 || state.HighWater < state.Generation || !strideIdentifier(state.KeyID) || state.KeyVersion < 1 || !isHexDigest(state.MAC) || stridePI0ValidateCarrierCollections(ctx, keys, state) != nil {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, state.KeyID, state.KeyVersion)
	if err != nil || !stridePI0ValidKey(key) || key.ID != state.KeyID || key.Version != state.KeyVersion {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0CarrierStatePayload(state)
	if err != nil || !hmac.Equal([]byte(state.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

func stridePI0ValidateCarrierCollections(ctx context.Context, keys StridePI0ManagedMACKeyring, state stridePI0CarrierState) error {
	if len(state.Journals) > 4096 || len(state.Events) > 65536 || len(state.AppendReceipts) > 4096 {
		return ErrStridePI0Invalid
	}
	journalIDs, eventIDs, receiptOps := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, journal := range state.Journals {
		if VerifyStridePI0CompoundJournal(ctx, keys, journal) != nil || journalIDs[journal.OperationID] || (i > 0 && state.Journals[i-1].OperationID >= journal.OperationID) {
			return ErrStridePI0Invalid
		}
		journalIDs[journal.OperationID] = true
	}
	for i, event := range state.Events {
		if VerifyStridePI0LifecycleEvent(ctx, keys, event) != nil || eventIDs[event.EventID] || (i > 0 && state.Events[i-1].EventID >= event.EventID) {
			return ErrStridePI0Invalid
		}
		eventIDs[event.EventID] = true
	}
	for i, receipt := range state.AppendReceipts {
		if VerifyStridePI0EventAppendReceipt(ctx, keys, receipt) != nil || receiptOps[receipt.OperationID] || (i > 0 && state.AppendReceipts[i-1].OperationID >= receipt.OperationID) {
			return ErrStridePI0Invalid
		}
		receiptOps[receipt.OperationID] = true
	}
	return nil
}

func stridePI0CarrierDigest(state stridePI0CarrierState) (string, error) {
	raw, err := canonicalJSON(state)
	if err != nil {
		return "", ErrStridePI0Invalid
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func stridePI0CarrierTransactionPayload(txn stridePI0CarrierTransaction) ([]byte, error) {
	txn.MAC = ""
	return canonicalJSON(struct {
		Domain      string                      `json:"domain"`
		Transaction stridePI0CarrierTransaction `json:"transaction"`
	}{stridePI0CarrierTxnDomain, txn})
}

func stridePI0SealCarrierTransaction(ctx context.Context, keys StridePI0ManagedMACKeyring, txn stridePI0CarrierTransaction) (stridePI0CarrierTransaction, error) {
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil || !stridePI0ValidKey(key) {
		return stridePI0CarrierTransaction{}, ErrStridePI0Unavailable
	}
	txn.KeyID, txn.KeyVersion, txn.MAC = key.ID, key.Version, ""
	if txn.Schema != stridePI0CarrierTxnSchema || txn.Next.Generation != txn.Prior.Generation+1 || !isHexDigest(txn.Next.Digest) || (txn.Prior.Generation > 0 && !isHexDigest(txn.Prior.Digest)) {
		return stridePI0CarrierTransaction{}, ErrStridePI0Invalid
	}
	payload, err := stridePI0CarrierTransactionPayload(txn)
	if err != nil {
		return stridePI0CarrierTransaction{}, ErrStridePI0Invalid
	}
	txn.MAC = stridePI0MAC(key.Secret, payload)
	return txn, nil
}

func stridePI0VerifyCarrierTransaction(ctx context.Context, keys StridePI0ManagedMACKeyring, txn stridePI0CarrierTransaction) error {
	if txn.Schema != stridePI0CarrierTxnSchema || txn.Next.Generation != txn.Prior.Generation+1 || !isHexDigest(txn.Next.Digest) || (txn.Prior.Generation > 0 && !isHexDigest(txn.Prior.Digest)) || !strideIdentifier(txn.KeyID) || txn.KeyVersion < 1 || !isHexDigest(txn.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, txn.KeyID, txn.KeyVersion)
	if err != nil || !stridePI0ValidKey(key) || key.ID != txn.KeyID || key.Version != txn.KeyVersion {
		return ErrStridePI0Unavailable
	}
	payload, err := stridePI0CarrierTransactionPayload(txn)
	if err != nil || !hmac.Equal([]byte(txn.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

func (c *StridePI0FileCarrier) readLocked(ctx context.Context) (stridePI0CarrierState, error) {
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return stridePI0CarrierState{}, ErrStridePI0Unavailable
	}
	var state stridePI0CarrierState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || stridePI0EnsureEOF(decoder) != nil || stridePI0VerifyCarrierState(ctx, c.keys, state) != nil {
		return stridePI0CarrierState{}, ErrStridePI0Invalid
	}
	digest, err := stridePI0CarrierDigest(state)
	if err != nil {
		return stridePI0CarrierState{}, err
	}
	highWater, err := c.highWater.ReadStridePI0CarrierHighWater(ctx, c.path)
	if err != nil || highWater != (StridePI0CarrierHighWater{Generation: state.Generation, Digest: digest}) {
		return stridePI0CarrierState{}, ErrStridePI0Conflict
	}
	return state, nil
}

func (c *StridePI0FileCarrier) commitLocked(ctx context.Context, prior, next stridePI0CarrierState) error {
	priorMark := StridePI0CarrierHighWater{}
	if prior.Generation > 0 {
		digest, err := stridePI0CarrierDigest(prior)
		if err != nil {
			return err
		}
		priorMark = StridePI0CarrierHighWater{Generation: prior.Generation, Digest: digest}
	}
	nextDigest, err := stridePI0CarrierDigest(next)
	if err != nil {
		return err
	}
	nextMark := StridePI0CarrierHighWater{Generation: next.Generation, Digest: nextDigest}
	txn, err := stridePI0SealCarrierTransaction(ctx, c.keys, stridePI0CarrierTransaction{Schema: stridePI0CarrierTxnSchema, Prior: priorMark, Next: nextMark, NextState: next})
	if err != nil {
		return err
	}
	if err := stridePI0AtomicWrite(c.txnPath, txn); err != nil {
		return err
	}
	if err := c.highWater.CompareAndSwapStridePI0CarrierHighWater(ctx, c.path, priorMark, nextMark); err != nil {
		return ErrStridePI0Conflict
	}
	if err := stridePI0AtomicWrite(c.path, next); err != nil {
		return err
	}
	if err := os.Remove(c.txnPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStridePI0Unavailable
	}
	return stridePI0SyncDirectory(filepath.Dir(c.path))
}

func (c *StridePI0FileCarrier) recoverLocked(ctx context.Context) error {
	raw, err := os.ReadFile(c.txnPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrStridePI0Unavailable
	}
	var txn stridePI0CarrierTransaction
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&txn) != nil || stridePI0EnsureEOF(decoder) != nil || stridePI0VerifyCarrierTransaction(ctx, c.keys, txn) != nil || stridePI0VerifyCarrierState(ctx, c.keys, txn.NextState) != nil {
		return ErrStridePI0Invalid
	}
	digest, err := stridePI0CarrierDigest(txn.NextState)
	if err != nil || txn.Next != (StridePI0CarrierHighWater{Generation: txn.NextState.Generation, Digest: digest}) {
		return ErrStridePI0Invalid
	}
	mark, err := c.highWater.ReadStridePI0CarrierHighWater(ctx, c.path)
	if err != nil {
		return ErrStridePI0Unavailable
	}
	if mark == txn.Prior {
		if txn.Prior.Generation > 0 {
			rawState, readErr := os.ReadFile(c.path)
			if readErr != nil {
				return ErrStridePI0Unavailable
			}
			var priorState stridePI0CarrierState
			priorDecoder := json.NewDecoder(bytes.NewReader(rawState))
			priorDecoder.DisallowUnknownFields()
			if priorDecoder.Decode(&priorState) != nil || stridePI0EnsureEOF(priorDecoder) != nil || stridePI0VerifyCarrierState(ctx, c.keys, priorState) != nil {
				return ErrStridePI0Invalid
			}
			priorDigest, digestErr := stridePI0CarrierDigest(priorState)
			if digestErr != nil || txn.Prior != (StridePI0CarrierHighWater{Generation: priorState.Generation, Digest: priorDigest}) {
				return ErrStridePI0Conflict
			}
		}
		if err := c.highWater.CompareAndSwapStridePI0CarrierHighWater(ctx, c.path, txn.Prior, txn.Next); err != nil {
			return ErrStridePI0Conflict
		}
	} else if mark != txn.Next {
		return ErrStridePI0Conflict
	}
	if err := stridePI0AtomicWrite(c.path, txn.NextState); err != nil {
		return err
	}
	if err := os.Remove(c.txnPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStridePI0Unavailable
	}
	return stridePI0SyncDirectory(filepath.Dir(c.path))
}

func stridePI0AtomicWrite(path string, value any) error {
	raw, err := canonicalJSON(value)
	if err != nil {
		return ErrStridePI0Invalid
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".pi0-*")
	if err != nil {
		return ErrStridePI0Unavailable
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return ErrStridePI0Unavailable
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return ErrStridePI0Unavailable
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return ErrStridePI0Unavailable
	}
	if err := temp.Close(); err != nil {
		return ErrStridePI0Unavailable
	}
	if err := os.Rename(tempPath, path); err != nil {
		return ErrStridePI0Unavailable
	}
	return stridePI0SyncDirectory(dir)
}

func stridePI0SyncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return ErrStridePI0Unavailable
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return ErrStridePI0Unavailable
	}
	return nil
}

func (c *StridePI0FileCarrier) mutate(ctx context.Context, change func(*stridePI0CarrierState) error) error {
	return c.withLock(func() error {
		if err := c.recoverLocked(ctx); err != nil {
			return err
		}
		state, err := c.readLocked(ctx)
		if err != nil {
			return err
		}
		next := state
		next.Journals = append([]StridePI0CompoundJournal(nil), state.Journals...)
		next.Events = append([]StridePI0LifecycleEvent(nil), state.Events...)
		next.AppendReceipts = append([]StridePI0EventAppendReceipt(nil), state.AppendReceipts...)
		if err := change(&next); err != nil {
			if errors.Is(err, errStridePI0NoMutation) {
				return nil
			}
			return err
		}
		next.Generation, next.HighWater = state.Generation+1, state.HighWater+1
		sealed, err := stridePI0SealCarrierState(ctx, c.keys, next)
		if err != nil {
			return err
		}
		return c.commitLocked(ctx, state, sealed)
	})
}

func (c *StridePI0FileCarrier) ReadOperation(ctx context.Context, operationID string) (StridePI0CompoundJournal, StridePI0EventAppendReceipt, error) {
	if c == nil || !strideIdentifier(operationID) {
		return StridePI0CompoundJournal{}, StridePI0EventAppendReceipt{}, ErrStridePI0Invalid
	}
	var journal StridePI0CompoundJournal
	var receipt StridePI0EventAppendReceipt
	err := c.withLock(func() error {
		if err := c.recoverLocked(ctx); err != nil {
			return err
		}
		state, err := c.readLocked(ctx)
		if err != nil {
			return err
		}
		for _, candidate := range state.Journals {
			if candidate.OperationID == operationID {
				journal = candidate
				break
			}
		}
		for _, candidate := range state.AppendReceipts {
			if candidate.OperationID == operationID {
				receipt = candidate
				break
			}
		}
		if journal.OperationID == "" {
			return ErrStridePI0Unavailable
		}
		return nil
	})
	return journal, receipt, err
}

func (c *StridePI0FileCarrier) CreateJournal(ctx context.Context, journal StridePI0CompoundJournal) error {
	if c == nil || VerifyStridePI0CompoundJournal(ctx, c.keys, journal) != nil || journal.Phase != "prepared" {
		return ErrStridePI0Invalid
	}
	return c.mutate(ctx, func(state *stridePI0CarrierState) error {
		for _, current := range state.Journals {
			if current.OperationID != journal.OperationID {
				continue
			}
			left, _ := canonicalJSON(current)
			right, _ := canonicalJSON(journal)
			if bytes.Equal(left, right) {
				return errStridePI0NoMutation
			}
			return ErrStridePI0Conflict
		}
		state.Journals = append(state.Journals, journal)
		sort.Slice(state.Journals, func(i, j int) bool { return state.Journals[i].OperationID < state.Journals[j].OperationID })
		return nil
	})
}

func (c *StridePI0FileCarrier) CompareAndSwapJournal(ctx context.Context, prior, next StridePI0CompoundJournal) error {
	if c == nil || VerifyStridePI0CompoundJournal(ctx, c.keys, prior) != nil || VerifyStridePI0CompoundJournal(ctx, c.keys, next) != nil || prior.OperationID != next.OperationID || prior.OperationFingerprint != next.OperationFingerprint || next.PhaseGeneration != prior.PhaseGeneration+1 {
		return ErrStridePI0Invalid
	}
	return c.mutate(ctx, func(state *stridePI0CarrierState) error {
		for i, current := range state.Journals {
			if current.OperationID != prior.OperationID {
				continue
			}
			currentRaw, _ := canonicalJSON(current)
			priorRaw, _ := canonicalJSON(prior)
			if !bytes.Equal(currentRaw, priorRaw) {
				return ErrStridePI0Conflict
			}
			state.Journals[i] = next
			return nil
		}
		return ErrStridePI0Unavailable
	})
}

func stridePI0EventDescriptor(event StridePI0LifecycleEvent) (StridePI0JournalEvent, error) {
	raw, err := canonicalJSON(event)
	if err != nil {
		return StridePI0JournalEvent{}, ErrStridePI0Invalid
	}
	sum := sha256.Sum256(raw)
	return StridePI0JournalEvent{EventID: event.EventID, EventType: event.EventType, Digest: hex.EncodeToString(sum[:])}, nil
}

// AppendEventsOnce atomically records exact authenticated lifecycle events and
// an append receipt tied to the effect postimage while current authority is
// held through the final durable carrier write. Recovery uses the separate
// recovery-only entrypoint below and cannot mint or alter an event envelope.
func (c *StridePI0FileCarrier) AppendEventsOnce(ctx context.Context, authority StridePI0CurrentAuthority, principal StridePI0Principal, operationID, fingerprint string, fence StridePI0Postimage, events []StridePI0LifecycleEvent, at time.Time) (StridePI0EventAppendReceipt, error) {
	if authority == nil || principal.validate() != nil {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Unavailable
	}
	var receipt StridePI0EventAppendReceipt
	err := authority.WithCurrentStridePI0Principal(ctx, principal, func() error {
		var appendErr error
		receipt, appendErr = c.appendEventsOnce(ctx, principal, operationID, fingerprint, fence, events, at)
		return appendErr
	})
	if err != nil {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Unavailable
	}
	return receipt, nil
}

func (c *StridePI0FileCarrier) appendEventsOnce(ctx context.Context, principal StridePI0Principal, operationID, fingerprint string, fence StridePI0Postimage, events []StridePI0LifecycleEvent, at time.Time) (StridePI0EventAppendReceipt, error) {
	if c == nil || !strideIdentifier(operationID) || !isHexDigest(fingerprint) || fence.validate() != nil || len(events) < 1 || len(events) > 16 || at.IsZero() {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Invalid
	}
	descriptors := make([]StridePI0JournalEvent, len(events))
	for i, event := range events {
		if VerifyStridePI0LifecycleEvent(ctx, c.keys, event) != nil {
			return StridePI0EventAppendReceipt{}, ErrStridePI0Invalid
		}
		descriptor, err := stridePI0EventDescriptor(event)
		if err != nil {
			return StridePI0EventAppendReceipt{}, err
		}
		descriptors[i] = descriptor
	}
	var result StridePI0EventAppendReceipt
	err := c.mutate(ctx, func(state *stridePI0CarrierState) error {
		journalIndex := -1
		for i := range state.Journals {
			if state.Journals[i].OperationID == operationID {
				journalIndex = i
				break
			}
		}
		if journalIndex < 0 {
			return ErrStridePI0Unavailable
		}
		journal := state.Journals[journalIndex]
		if journal.Principal != principal || journal.OperationFingerprint != fingerprint || !stridePI0JournalEventsEqual(journal.RequestedEvents, descriptors) {
			return ErrStridePI0Conflict
		}
		for _, event := range events {
			if event.Principal != journal.Principal || event.TenantID != journal.TenantID || event.TraceID != journal.TraceID || event.Aggregate != journal.Aggregate {
				return ErrStridePI0Conflict
			}
		}
		for _, existing := range state.AppendReceipts {
			if existing.OperationID != operationID {
				continue
			}
			if existing.OperationFingerprint != fingerprint || existing.RecoveryFencePostimage != fence || !stridePI0JournalEventsEqual(existing.Events, descriptors) {
				return ErrStridePI0Conflict
			}
			result = existing
			return errStridePI0NoMutation
		}
		if !oneOf(journal.Phase, "effect_applied", "effect_reconciled") || journal.Reconciliation != "applied" || !stridePI0EqualPostimages(journal.ExpectedPostimages, journal.ActualPostimages) || !stridePI0EqualPostimages(journal.ExpectedPostimages, journal.CommittedEffectPostimage) {
			return ErrStridePI0Conflict
		}
		for _, event := range events {
			for _, existing := range state.Events {
				if existing.EventID != event.EventID {
					continue
				}
				left, _ := canonicalJSON(existing)
				right, _ := canonicalJSON(event)
				if !bytes.Equal(left, right) {
					return ErrStridePI0Conflict
				}
				goto nextEvent
			}
			state.Events = append(state.Events, event)
		nextEvent:
		}
		sort.Slice(state.Events, func(i, j int) bool { return state.Events[i].EventID < state.Events[j].EventID })
		nextJournal, err := stridePI0TransitionJournal(ctx, c.keys, journal, "events_reconciled", journal.ActualPostimages, "applied", journal.EffectReceiptDigest, at)
		if err != nil {
			return err
		}
		state.Journals[journalIndex] = nextJournal
		receipt := StridePI0EventAppendReceipt{ReceiptID: operationID + "_append", OperationID: operationID, OperationFingerprint: fingerprint, JournalPhaseGeneration: nextJournal.PhaseGeneration, CommittedEffectPostimage: append([]StridePI0Postimage(nil), journal.CommittedEffectPostimage...), RecoveryFencePostimage: fence, Events: descriptors, CarrierGeneration: state.Generation + 1, CarrierHighWater: state.HighWater + 1, IssuedAt: at.UTC()}
		sealed, err := stridePI0SealAppendReceipt(ctx, c.keys, receipt)
		if err != nil {
			return err
		}
		state.AppendReceipts = append(state.AppendReceipts, sealed)
		sort.Slice(state.AppendReceipts, func(i, j int) bool { return state.AppendReceipts[i].OperationID < state.AppendReceipts[j].OperationID })
		result = sealed
		return nil
	})
	return result, err
}

func (c *StridePI0FileCarrier) appendEventsOnceForRecovery(ctx context.Context, recoveryAuthority StridePI0RecoveryAuthority, operationID, fingerprint string, fence StridePI0Postimage, events []StridePI0LifecycleEvent, at time.Time) (StridePI0EventAppendReceipt, error) {
	if recoveryAuthority == nil {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Unavailable
	}
	var receipt StridePI0EventAppendReceipt
	err := recoveryAuthority.WithStridePI0RecoveryAuthority(ctx, operationID, fingerprint, func() error {
		journal, _, readErr := c.ReadOperation(ctx, operationID)
		if readErr != nil {
			return readErr
		}
		var appendErr error
		receipt, appendErr = c.appendEventsOnce(ctx, journal.Principal, operationID, fingerprint, fence, events, at)
		return appendErr
	})
	if err != nil {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Unavailable
	}
	return receipt, nil
}

func stridePI0JournalEventsEqual(left, right []StridePI0JournalEvent) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (c *StridePI0FileCarrier) RetryNotApplied(ctx context.Context, authority StridePI0CurrentAuthority, operationID string, at time.Time) (StridePI0CompoundJournal, error) {
	journal, _, err := c.ReadOperation(ctx, operationID)
	if err != nil || journal.Phase != "effect_reconciled" || journal.Reconciliation != "not_applied" || !stridePI0EqualPostimages(journal.Preimages, journal.ActualPostimages) {
		return StridePI0CompoundJournal{}, ErrStridePI0Conflict
	}
	next, err := TransitionStridePI0CompoundJournal(ctx, c.keys, authority, journal, "effect_requested", nil, "", at)
	if err != nil {
		return StridePI0CompoundJournal{}, err
	}
	if err := c.CompareAndSwapJournal(ctx, journal, next); err != nil {
		return StridePI0CompoundJournal{}, err
	}
	return next, nil
}

func (c *StridePI0FileCarrier) ReadOperationForCaller(ctx context.Context, authority StridePI0CurrentAuthority, principal StridePI0Principal, operationID string) (StridePI0CompoundJournal, error) {
	if authority == nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	var journal StridePI0CompoundJournal
	err := authority.WithCurrentStridePI0Principal(ctx, principal, func() error {
		var readErr error
		journal, _, readErr = c.ReadOperation(ctx, operationID)
		if readErr != nil {
			return readErr
		}
		if journal.Principal != principal {
			return ErrStridePI0Unavailable
		}
		return nil
	})
	if err != nil {
		return StridePI0CompoundJournal{}, ErrStridePI0Unavailable
	}
	return journal, nil
}

// StridePI0AuthoritativeEventStore is an independent append-once destination.
// Recovery never trusts a successful Append return: it requires an exact
// authenticated readback before the carrier may seal the append receipt.
type StridePI0AuthoritativeEventStore interface {
	ReadStridePI0LifecycleEvent(context.Context, string) (StridePI0LifecycleEvent, bool, error)
	AppendStridePI0LifecycleEventOnce(context.Context, string, string, StridePI0LifecycleEvent) error
}

// StridePI0RecoveryFenceInstaller installs and independently reads the current
// body-free visibility/purge fence before any repaired success event becomes
// visible. The returned postimage is persisted in the append receipt.
type StridePI0RecoveryFenceInstaller interface {
	InstallAndReadStridePI0RecoveryFence(context.Context, string, string, []StridePI0Postimage) (StridePI0Postimage, error)
}

// RepairStridePI0CarrierOperation performs no provider effect. It proves an
// already-applied effect from the managed provider receipt and destination
// postimages, repairs the append-once event postimage, and commits the journal
// under recovery-only authority. Human/session revocation therefore cannot
// strand an applied effect, while ReadOperationForCaller remains denied.
func RepairStridePI0CarrierOperation(ctx context.Context, carrier *StridePI0FileCarrier, evidenceKeys StridePI0EffectEvidenceKeyring, recoveryAuthority StridePI0RecoveryAuthority, reader StridePI0PostimageReader, receipts StridePI0EffectReceiptResolver, fences StridePI0RecoveryFenceInstaller, eventStore StridePI0AuthoritativeEventStore, operationID string, events []StridePI0LifecycleEvent, at time.Time) (StridePI0CompoundJournal, error) {
	if carrier == nil || evidenceKeys == nil || recoveryAuthority == nil || reader == nil || receipts == nil || fences == nil || eventStore == nil || !strideIdentifier(operationID) || at.IsZero() {
		return StridePI0CompoundJournal{}, ErrStridePI0Invalid
	}
	initial, _, err := carrier.ReadOperation(ctx, operationID)
	if err != nil {
		return StridePI0CompoundJournal{}, err
	}
	var result StridePI0CompoundJournal
	err = recoveryAuthority.WithStridePI0RecoveryAuthority(ctx, operationID, initial.OperationFingerprint, func() error {
		journal, _, err := carrier.ReadOperation(ctx, operationID)
		if err != nil {
			return err
		}
		if journal.Phase == "committed" {
			result = journal
			return nil
		}
		for !oneOf(journal.Phase, "effect_reconciled", "events_reconciled", "postimages_verified", "quarantined") {
			next, recoverErr := RecoverStridePI0CompoundJournal(ctx, carrier.keys, evidenceKeys, recoveryAuthority, reader, receipts, journal, at.Add(time.Duration(journal.PhaseGeneration)*time.Nanosecond))
			if recoverErr != nil {
				return recoverErr
			}
			if err := carrier.CompareAndSwapJournal(ctx, journal, next); err != nil {
				return err
			}
			journal = next
		}
		if journal.Phase == "quarantined" || journal.Reconciliation == "quarantined" {
			return ErrStridePI0Conflict
		}
		if journal.Reconciliation != "applied" {
			result = journal
			return ErrStridePI0Unavailable
		}
		if len(events) != len(journal.RequestedEvents) {
			return ErrStridePI0Conflict
		}
		descriptors := make([]StridePI0JournalEvent, len(events))
		for i, event := range events {
			if VerifyStridePI0LifecycleEvent(ctx, carrier.keys, event) != nil {
				return ErrStridePI0Invalid
			}
			descriptor, err := stridePI0EventDescriptor(event)
			if err != nil {
				return err
			}
			descriptors[i] = descriptor
		}
		if !stridePI0JournalEventsEqual(journal.RequestedEvents, descriptors) {
			return ErrStridePI0Conflict
		}
		fence, fenceErr := fences.InstallAndReadStridePI0RecoveryFence(ctx, operationID, journal.OperationFingerprint, append([]StridePI0Postimage(nil), journal.CommittedEffectPostimage...))
		if fenceErr != nil || fence.validate() != nil {
			return ErrStridePI0Unavailable
		}
		if journal.Phase == "effect_reconciled" {
			for i, event := range events {
				stored, found, readErr := eventStore.ReadStridePI0LifecycleEvent(ctx, event.EventID)
				if readErr != nil {
					return ErrStridePI0Unavailable
				}
				if !found {
					if appendErr := eventStore.AppendStridePI0LifecycleEventOnce(ctx, operationID, journal.OperationFingerprint, event); appendErr != nil {
						return ErrStridePI0Unavailable
					}
					stored, found, readErr = eventStore.ReadStridePI0LifecycleEvent(ctx, event.EventID)
				}
				storedDescriptor, descErr := stridePI0EventDescriptor(stored)
				if readErr != nil || !found || VerifyStridePI0LifecycleEvent(ctx, carrier.keys, stored) != nil || descErr != nil || storedDescriptor != descriptors[i] {
					return ErrStridePI0Conflict
				}
			}
			if _, err := carrier.appendEventsOnceForRecovery(ctx, recoveryAuthority, operationID, journal.OperationFingerprint, fence, append([]StridePI0LifecycleEvent(nil), events...), at.Add(time.Duration(journal.PhaseGeneration+1)*time.Nanosecond)); err != nil {
				return err
			}
			journal, _, err = carrier.ReadOperation(ctx, operationID)
			if err != nil {
				return err
			}
		}
		_, appendReceipt, readErr := carrier.ReadOperation(ctx, operationID)
		if readErr != nil || VerifyStridePI0EventAppendReceipt(ctx, carrier.keys, appendReceipt) != nil || appendReceipt.OperationFingerprint != journal.OperationFingerprint || appendReceipt.RecoveryFencePostimage != fence || !stridePI0JournalEventsEqual(appendReceipt.Events, descriptors) || !stridePI0EqualPostimages(appendReceipt.CommittedEffectPostimage, journal.CommittedEffectPostimage) {
			return ErrStridePI0Conflict
		}
		actual, readErr := reader.ReadStridePI0Postimages(ctx, operationID, append([]StridePI0Postimage(nil), journal.ExpectedPostimages...))
		drifted := readErr != nil || !stridePI0EqualPostimages(actual, journal.CommittedEffectPostimage)
		for i, event := range events {
			stored, found, eventErr := eventStore.ReadStridePI0LifecycleEvent(ctx, event.EventID)
			storedDescriptor, descErr := stridePI0EventDescriptor(stored)
			if eventErr != nil || !found || VerifyStridePI0LifecycleEvent(ctx, carrier.keys, stored) != nil || descErr != nil || storedDescriptor != descriptors[i] {
				drifted = true
				break
			}
		}
		if drifted {
			quarantined, transitionErr := stridePI0TransitionJournal(ctx, carrier.keys, journal, "quarantined", journal.ActualPostimages, "quarantined", journal.EffectReceiptDigest, at.Add(time.Duration(journal.PhaseGeneration+1)*time.Nanosecond))
			if transitionErr != nil {
				return transitionErr
			}
			if err := carrier.CompareAndSwapJournal(ctx, journal, quarantined); err != nil {
				return err
			}
			result = quarantined
			return ErrStridePI0Conflict
		}
		phases := []string{"postimages_verified", "committed"}
		if journal.Phase == "postimages_verified" {
			phases = []string{"committed"}
		}
		for _, phase := range phases {
			next, transitionErr := stridePI0TransitionJournal(ctx, carrier.keys, journal, phase, journal.ActualPostimages, "applied", journal.EffectReceiptDigest, at.Add(time.Duration(journal.PhaseGeneration+1)*time.Nanosecond))
			if transitionErr != nil {
				return transitionErr
			}
			if err := carrier.CompareAndSwapJournal(ctx, journal, next); err != nil {
				return err
			}
			journal = next
		}
		result = journal
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func stridePI0EnsureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return ErrStridePI0Invalid
}
