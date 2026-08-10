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
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stridePI0TestKeyring struct {
	current uint64
	keys    map[uint64]StridePI0ManagedMACKey
	private map[uint64]StridePI0ManagedMACKey
	public  map[uint64]StridePI0ManagedMACKey
}

func (k *stridePI0TestKeyring) CurrentStridePI0PrivateCommitmentKey(context.Context) (StridePI0ManagedMACKey, error) {
	key, ok := k.private[k.current]
	if !ok {
		return StridePI0ManagedMACKey{}, errors.New("missing private commitment key")
	}
	return key, nil
}

func (k *stridePI0TestKeyring) ResolveStridePI0PrivateCommitmentKey(_ context.Context, id string, version uint64) (StridePI0ManagedMACKey, error) {
	key, ok := k.private[version]
	if !ok || key.ID != id {
		return StridePI0ManagedMACKey{}, errors.New("missing private commitment key")
	}
	return key, nil
}

func (k *stridePI0TestKeyring) ResolveStridePI0PrivateCommitmentKeyVersion(_ context.Context, version uint64) (StridePI0ManagedMACKey, error) {
	key, ok := k.private[version]
	if !ok {
		return StridePI0ManagedMACKey{}, errors.New("missing private commitment key")
	}
	return key, nil
}

func (k *stridePI0TestKeyring) CurrentStridePI0PublicTraceKey(context.Context) (StridePI0ManagedMACKey, error) {
	key, ok := k.public[k.current]
	if !ok {
		return StridePI0ManagedMACKey{}, errors.New("missing public key")
	}
	return key, nil
}

func (k *stridePI0TestKeyring) ResolveStridePI0PublicTraceKey(_ context.Context, id string, version uint64) (StridePI0ManagedMACKey, error) {
	key, ok := k.public[version]
	if !ok || key.ID != id {
		return StridePI0ManagedMACKey{}, errors.New("missing public key")
	}
	return key, nil
}

func (k *stridePI0TestKeyring) ResolveStridePI0PublicTraceKeyVersion(_ context.Context, version uint64) (StridePI0ManagedMACKey, error) {
	key, ok := k.public[version]
	if !ok {
		return StridePI0ManagedMACKey{}, errors.New("missing public key")
	}
	return key, nil
}

func (k *stridePI0TestKeyring) CurrentStridePI0ManagedMACKey(context.Context) (StridePI0ManagedMACKey, error) {
	key, ok := k.keys[k.current]
	if !ok {
		return StridePI0ManagedMACKey{}, errors.New("missing key")
	}
	return key, nil
}

func (k *stridePI0TestKeyring) ResolveStridePI0ManagedMACKey(_ context.Context, id string, version uint64) (StridePI0ManagedMACKey, error) {
	key, ok := k.keys[version]
	if !ok || key.ID != id {
		return StridePI0ManagedMACKey{}, errors.New("missing key")
	}
	return key, nil
}

func (k *stridePI0TestKeyring) ResolveStridePI0ManagedMACKeyVersion(_ context.Context, version uint64) (StridePI0ManagedMACKey, error) {
	key, ok := k.keys[version]
	if !ok {
		return StridePI0ManagedMACKey{}, errors.New("missing key")
	}
	return key, nil
}

func newStridePI0TestKeyring() *stridePI0TestKeyring {
	return &stridePI0TestKeyring{current: 1, keys: map[uint64]StridePI0ManagedMACKey{
		1: {ID: "pi0_test_key", Version: 1, Secret: []byte(strings.Repeat("a", 32))},
		2: {ID: "pi0_test_key", Version: 2, Secret: []byte(strings.Repeat("b", 32))},
	}, private: map[uint64]StridePI0ManagedMACKey{
		1: {ID: "pi0_private_commitment_key", Version: 1, Secret: []byte(strings.Repeat("c", 32))},
		2: {ID: "pi0_private_commitment_key", Version: 2, Secret: []byte(strings.Repeat("d", 32))},
	}, public: map[uint64]StridePI0ManagedMACKey{
		1: {ID: "pi0_public_trace_key", Version: 1, Secret: []byte(strings.Repeat("p", 32))},
		2: {ID: "pi0_public_trace_key", Version: 2, Secret: []byte(strings.Repeat("q", 32))},
	}}
}

type stridePI0TestEvidenceKeyring struct {
	key StridePI0EffectEvidenceKey
}

func (k *stridePI0TestEvidenceKeyring) CurrentStridePI0EffectEvidenceKey(context.Context) (StridePI0EffectEvidenceKey, error) {
	return k.key, nil
}

func (k *stridePI0TestEvidenceKeyring) ResolveStridePI0EffectEvidenceKey(_ context.Context, id string, version uint64) (StridePI0EffectEvidenceKey, error) {
	if k.key.ID != id || k.key.Version != version {
		return StridePI0EffectEvidenceKey{}, errors.New("missing evidence key")
	}
	return k.key, nil
}

type stridePI0TestCurrentAuthority struct {
	revoked bool
	calls   int
	gate    sync.Mutex
	held    atomic.Bool
}

func (a *stridePI0TestCurrentAuthority) WithCurrentStridePI0Principal(_ context.Context, principal StridePI0Principal, effect func() error) error {
	a.gate.Lock()
	defer a.gate.Unlock()
	a.calls++
	if a.revoked || principal.validate() != nil || effect == nil {
		return ErrStridePI0Unavailable
	}
	a.held.Store(true)
	defer a.held.Store(false)
	return effect()
}

func (a *stridePI0TestCurrentAuthority) setRevoked(value bool) {
	a.gate.Lock()
	defer a.gate.Unlock()
	a.revoked = value
}

type stridePI0TestRecoveryAuthority struct {
	allowed bool
	calls   int
}

func (a *stridePI0TestRecoveryAuthority) WithStridePI0RecoveryAuthority(_ context.Context, operationID, fingerprint string, effect func() error) error {
	a.calls++
	if !a.allowed || !strideIdentifier(operationID) || !isHexDigest(fingerprint) || effect == nil {
		return ErrStridePI0Unavailable
	}
	return effect()
}

func stridePI0TestDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func stridePI0TestRef(kind, id string, revision int64) StridePI0Reference {
	return StridePI0Reference{Type: kind, ID: id, Revision: revision, Digest: stridePI0TestDigest(kind + id)}
}

// This oracle is intentionally independent of stridePI0EventReferenceRules.
// A production-rule mutation must fail the fixtures rather than rewriting the
// expected contract used to construct them.
func stridePI0ExpectedOneOf(aggregate string, causes ...string) stridePI0EventReferenceRule {
	return stridePI0EventReferenceRule{AggregateType: aggregate, CausationOneOf: append([]string(nil), causes...), CausationAllowed: append([]string(nil), causes...), CausationMin: 1, CausationMax: 1, SubjectRequired: []string{aggregate}, SubjectAllowed: []string{aggregate}, SubjectMin: 1, SubjectMax: 1, Consent: "required"}
}

func stridePI0ExpectedRequired(aggregate string, causes ...string) stridePI0EventReferenceRule {
	rule := stridePI0ExpectedOneOf(aggregate, causes...)
	rule.CausationRequired, rule.CausationOneOf = append([]string(nil), causes...), nil
	rule.CausationMin, rule.CausationMax = len(causes), len(causes)
	return rule
}

func stridePI0ExpectedNoSubject(aggregate string, causes ...string) stridePI0EventReferenceRule {
	rule := stridePI0ExpectedRequired(aggregate, causes...)
	rule.SubjectRequired, rule.SubjectAllowed, rule.SubjectMin, rule.SubjectMax, rule.Consent = nil, nil, 0, 0, "not_applicable"
	return rule
}

var stridePI0ExpectedEventRules = map[string]stridePI0EventReferenceRule{
	"source.bound_to_trace": stridePI0ExpectedOneOf("intent", "conversation", "transcript"), "source.corrected": stridePI0ExpectedOneOf("intent", "intent"), "source.retracted": stridePI0ExpectedOneOf("intent", "intent"),
	"intent.admitted": stridePI0ExpectedOneOf("intent", "conversation", "transcript"), "intent.rejected": stridePI0ExpectedOneOf("intent", "conversation", "transcript"),
	"suggestion.created": stridePI0ExpectedOneOf("suggestion", "intent"), "suggestion.revised": stridePI0ExpectedOneOf("suggestion", "suggestion"), "suggestion.endorsed": stridePI0ExpectedOneOf("suggestion", "suggestion"), "suggestion.approved": stridePI0ExpectedOneOf("suggestion", "suggestion"), "suggestion.dismissed": stridePI0ExpectedOneOf("suggestion", "suggestion"), "suggestion.expired": stridePI0ExpectedOneOf("suggestion", "suggestion"),
	"run.created": stridePI0ExpectedOneOf("run", "suggestion"), "run.queued": stridePI0ExpectedOneOf("run", "run"), "run.started": stridePI0ExpectedOneOf("run", "run"), "run.state_changed": stridePI0ExpectedOneOf("run", "run"), "run.source_invalidated": stridePI0ExpectedRequired("run", "run", "intent"), "run.intervention_requested": stridePI0ExpectedOneOf("run", "run"), "run.intervention_resolved": stridePI0ExpectedOneOf("run", "run"), "run.cancelled": stridePI0ExpectedOneOf("run", "run"), "run.failed": stridePI0ExpectedOneOf("run", "run"), "run.completed": stridePI0ExpectedOneOf("run", "run"),
	"effect.requested": stridePI0ExpectedOneOf("effect", "run"), "effect.approved": stridePI0ExpectedOneOf("effect", "effect"), "effect.applied": stridePI0ExpectedRequired("effect", "effect", "provider_receipt"), "effect.failed": stridePI0ExpectedRequired("effect", "effect", "provider_receipt"), "effect.reconciled": stridePI0ExpectedRequired("effect", "effect", "provider_receipt", "journal"),
	"artifact.created": stridePI0ExpectedOneOf("artifact", "run", "effect", "artifact"), "artifact.revised": stridePI0ExpectedOneOf("artifact", "artifact"), "artifact.review_requested": stridePI0ExpectedOneOf("artifact", "artifact", "review"), "artifact.review_decided": stridePI0ExpectedOneOf("artifact", "artifact", "review"), "artifact.verification_recorded": stridePI0ExpectedOneOf("artifact", "artifact", "verification"), "artifact.adopted": stridePI0ExpectedOneOf("artifact", "artifact", "review"), "artifact.rejected": stridePI0ExpectedOneOf("artifact", "artifact", "review"), "artifact.withdrawn": stridePI0ExpectedOneOf("artifact", "artifact"), "artifact.publication_changed": stridePI0ExpectedOneOf("artifact", "artifact", "publication"),
	"outcome.recorded": stridePI0ExpectedOneOf("outcome", "artifact", "outcome", "review"), "outcome.corrected": stridePI0ExpectedOneOf("outcome", "outcome"), "outcome.rejected": stridePI0ExpectedOneOf("outcome", "outcome", "review"), "outcome.withdrawn": stridePI0ExpectedOneOf("outcome", "outcome"),
	"work_record.claim_created": stridePI0ExpectedOneOf("work_record", "artifact", "outcome", "work_record"), "work_record.subject_decided": stridePI0ExpectedOneOf("work_record", "work_record", "review"), "work_record.named_party_decided": stridePI0ExpectedOneOf("work_record", "work_record", "review"), "work_record.organization_decided": stridePI0ExpectedOneOf("work_record", "work_record", "review"), "work_record.attested": stridePI0ExpectedOneOf("work_record", "work_record", "verification"), "work_record.corrected": stridePI0ExpectedOneOf("work_record", "work_record"), "work_record.revoked": stridePI0ExpectedOneOf("work_record", "work_record"),
	"publication.contribution_published": stridePI0ExpectedOneOf("publication", "work_record", "publication"), "publication.contribution_withdrawn": stridePI0ExpectedOneOf("publication", "publication", "work_record"), "publication.network_state_changed": stridePI0ExpectedOneOf("publication", "publication"),
	"collaboration.search_admitted":   {AggregateType: "contact", CausationRequired: []string{"grant", "policy_verdict", "network_search_receipt"}, CausationAllowed: []string{"grant", "policy_verdict", "network_search_receipt"}, CausationMin: 3, CausationMax: 3, SubjectMin: 0, SubjectMax: 0, Consent: "required"},
	"collaboration.contact_requested": stridePI0ExpectedRequired("contact", "contact", "publication"), "collaboration.contact_decided": stridePI0ExpectedOneOf("contact", "contact"), "collaboration.block_changed": stridePI0ExpectedOneOf("block", "block", "contact"),
	"lifecycle.corrected":  {AggregateType: "journal", CausationRequired: []string{"journal", "journal"}, CausationAllowed: []string{"journal", "journal"}, CausationMin: 2, CausationMax: 2, SubjectRequired: []string{"journal", "journal"}, SubjectAllowed: []string{"journal"}, SubjectMin: 2, SubjectMax: 2, Consent: "not_applicable"},
	"lifecycle.reconciled": stridePI0ExpectedNoSubject("journal", "journal", "provider_receipt"), "lifecycle.revoked": stridePI0ExpectedNoSubject("journal", "journal"), "lifecycle.purged": stridePI0ExpectedNoSubject("journal", "journal"),
}

func stridePI0TestEvent(at time.Time, eventID, eventType, parent, aggregateType, aggregateID string, revision int64) StridePI0LifecycleEvent {
	retentionClass := "private_work_lifecycle"
	retentionDuration := 365 * 24 * time.Hour
	if strings.HasPrefix(eventType, "source.") {
		retentionClass = "source_link_short"
		retentionDuration = 30 * 24 * time.Hour
	} else if eventType == "lifecycle.purged" {
		retentionClass = "purge_receipt_body_free"
		retentionDuration = 730 * 24 * time.Hour
	} else if strings.HasPrefix(eventType, "work_record.") || strings.HasPrefix(eventType, "publication.") || strings.HasPrefix(eventType, "collaboration.") {
		retentionClass = "authorized_disclosure_audit"
		retentionDuration = 730 * 24 * time.Hour
	}
	rule := stridePI0ExpectedEventRules[eventType]
	causeTypes := append([]string(nil), rule.CausationRequired...)
	if len(rule.CausationOneOf) > 0 {
		causeTypes = append(causeTypes, rule.CausationOneOf[0])
	}
	for len(causeTypes) < rule.CausationMin {
		causeTypes = append(causeTypes, rule.CausationAllowed[0])
	}
	causes := make([]StridePI0Reference, len(causeTypes))
	for i, causeType := range causeTypes {
		causes[i] = stridePI0TestRef(causeType, fmt.Sprintf("causation_%02d", i), int64(i+1))
	}
	sort.Slice(causes, func(i, j int) bool { return causes[i].Type+"\x00"+causes[i].ID < causes[j].Type+"\x00"+causes[j].ID })
	subjectTypes := append([]string(nil), rule.SubjectRequired...)
	if len(rule.SubjectOneOf) > 0 {
		subjectTypes = append(subjectTypes, rule.SubjectOneOf[0])
	}
	for len(subjectTypes) < rule.SubjectMin {
		subjectTypes = append(subjectTypes, rule.SubjectAllowed[0])
	}
	subjects := make([]StridePI0Reference, len(subjectTypes))
	for i, subjectType := range subjectTypes {
		subjects[i] = stridePI0TestRef(subjectType, fmt.Sprintf("subject_%02d", i), int64(i+1))
	}
	event := StridePI0LifecycleEvent{
		Schema:        stridePI0EventSchema,
		EventID:       eventID,
		EventType:     eventType,
		TenantID:      "tenant_alpha",
		Aggregate:     StridePI0Aggregate{Type: aggregateType, ID: aggregateID, Revision: revision, Digest: stridePI0TestDigest(eventID + "aggregate")},
		TraceID:       "trace_alpha",
		ParentEventID: parent,
		CausationRefs: causes,
		Principal: StridePI0Principal{
			Kind: "human", PersonID: "person_alpha", OrganizationID: "organization_alpha", MembershipID: "membership_alpha",
			MembershipRevision: 4, SessionSubjectDigest: stridePI0TestDigest("session"), SessionRevision: 7,
		},
		SubjectRefs: subjects,
		Audience:    StridePI0Audience{Visibility: "organization", PrincipalIDs: []string{"organization_alpha"}, ACLVersion: 8},
		ConsentRefs: []StridePI0Reference{stridePI0TestRef("consent", "consent_alpha", 2)},
		PolicyRefs:  []StridePI0Reference{stridePI0TestRef("policy", "policy_alpha", 6)},
		Provenance:  "direct_human",
		OccurredAt:  at,
		EffectiveAt: at.Add(time.Second),
		RecordedAt:  at.Add(2 * time.Second),
		Quality:     StridePI0Quality{Status: "known", Reason: "none", ObservedSourceCount: 1, ExpectedSourceCount: 1},
		Retention: StridePI0Retention{
			Class: retentionClass, PolicyRef: stridePI0TestRef("policy", "retention_alpha", 1), RetainUntil: at.Add(retentionDuration),
		},
	}
	if rule.Consent == "not_applicable" {
		event.ConsentRefs = nil
	}
	if allowed, ok := map[string][]string{
		"intent.rejected": {"rejected"}, "run.intervention_resolved": {"approved"}, "effect.failed": {"not_applied"}, "artifact.review_decided": {"approved"}, "artifact.verification_recorded": {"passed"}, "artifact.rejected": {"rejected"}, "outcome.rejected": {"rejected"}, "work_record.subject_decided": {"approved"}, "work_record.named_party_decided": {"approved"}, "work_record.organization_decided": {"approved"}, "collaboration.contact_decided": {"accepted"},
	}[eventType]; ok {
		event.Decision = allowed[0]
	}
	if allowed, ok := map[string][]string{
		"run.state_changed": {"queued"}, "run.intervention_requested": {"input"}, "effect.reconciled": {"applied"}, "artifact.publication_changed": {"private"}, "publication.network_state_changed": {"draft"}, "collaboration.block_changed": {"active"}, "lifecycle.reconciled": {"applied"},
	}[eventType]; ok {
		event.State = allowed[0]
	}
	if strings.HasSuffix(eventType, ".failed") {
		event.FailureClass = "provider"
	}
	if strings.HasPrefix(eventType, "effect.") {
		event.JournalOperationID = "operation_alpha"
	}
	if strings.HasSuffix(eventType, ".retracted") || strings.HasSuffix(eventType, ".withdrawn") || strings.HasSuffix(eventType, ".revoked") || eventType == "run.source_invalidated" {
		event.Revocation = &StridePI0Fence{Generation: 1, Refs: []StridePI0Reference{stridePI0TestRef(aggregateType, "revoked_aggregate", 1)}}
	}
	if eventType == "lifecycle.purged" {
		event.Purge = &StridePI0Fence{Generation: 1, Refs: []StridePI0Reference{stridePI0TestRef("journal", "purged_journal", 1)}}
	}
	return event
}

func TestStridePI0ManagedCommitmentsDomainSeparationAndRotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	domains := []string{stridePI0OperationDomain, stridePI0IdempotencyDomain, stridePI0SourceDomain, stridePI0OutputDomain, stridePI0PublicTraceDomain}
	for _, domain := range domains {
		domain := domain
		t.Run(domain, func(t *testing.T) {
			commitment, err := MintStridePI0ManagedCommitment(ctx, keys, domain, "tenant_alpha", "opaque_value")
			if err != nil || VerifyStridePI0ManagedCommitment(ctx, keys, commitment, domain, "tenant_alpha", "opaque_value") != nil {
				t.Fatalf("valid commitment: %+v %v", commitment, err)
			}
			wrongDomain := domains[(indexStridePI0String(domains, domain)+1)%len(domains)]
			substituted := commitment
			substituted.Domain = wrongDomain
			if err := VerifyStridePI0ManagedCommitment(ctx, keys, substituted, wrongDomain, "tenant_alpha", "opaque_value"); err == nil {
				t.Fatalf("domain substitution accepted: %v", err)
			}
			plain := sha256.Sum256([]byte("tenant_alphaopaque_value"))
			forged := commitment
			forged.Digest = hex.EncodeToString(plain[:])
			if err := VerifyStridePI0ManagedCommitment(ctx, keys, forged, domain, "tenant_alpha", "opaque_value"); !errors.Is(err, ErrStridePI0Invalid) {
				t.Fatalf("plain sha accepted: %v", err)
			}
			if err := VerifyStridePI0ManagedCommitment(ctx, keys, commitment, domain, "tenant_alpha", "changed"); !errors.Is(err, ErrStridePI0Invalid) {
				t.Fatalf("changed body accepted: %v", err)
			}
			keys.current = 2
			rotated, err := MintStridePI0ManagedCommitment(ctx, keys, domain, "tenant_alpha", "opaque_value")
			if err != nil || rotated.KeyVersion != 2 || VerifyStridePI0ManagedCommitment(ctx, keys, commitment, domain, "tenant_alpha", "opaque_value") != nil || VerifyStridePI0ManagedCommitment(ctx, keys, rotated, domain, "tenant_alpha", "opaque_value") != nil {
				t.Fatalf("rotation/retired key: old=%+v new=%+v err=%v", commitment, rotated, err)
			}
			keys.current = 1
		})
	}
	retiredPrivate, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0SourceDomain, "tenant_alpha", "retired_private")
	if err != nil {
		t.Fatal(err)
	}
	delete(keys.private, retiredPrivate.KeyVersion)
	if err := VerifyStridePI0ManagedCommitment(ctx, keys, retiredPrivate, stridePI0SourceDomain, "tenant_alpha", "retired_private"); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("retired private commitment key remained usable: %v", err)
	}
	keys.private[1] = StridePI0ManagedMACKey{ID: "pi0_private_commitment_key", Version: 1, Secret: []byte(strings.Repeat("c", 32))}
	stateKey := keys.keys[1]
	forgedDigest, err := stridePI0CommitmentDigest(stateKey.Secret, stridePI0SourceDomain, []string{"tenant_alpha", "opaque_value"})
	if err != nil {
		t.Fatal(err)
	}
	forgedStateRole := StridePI0ManagedCommitment{Domain: stridePI0SourceDomain, KeyID: stateKey.ID, KeyVersion: stateKey.Version, Digest: forgedDigest}
	if err := VerifyStridePI0ManagedCommitment(ctx, keys, forgedStateRole, stridePI0SourceDomain, "tenant_alpha", "opaque_value"); err == nil {
		t.Fatal("state-MAC key was accepted in the private commitment role")
	}
	originalPrivate := keys.private[1]
	keys.private[1] = stateKey
	if err := VerifyStridePI0ManagedCommitment(ctx, keys, forgedStateRole, stridePI0SourceDomain, "tenant_alpha", "opaque_value"); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("resolver-level private/state role reuse did not fail closed: %v", err)
	}
	if _, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0SourceDomain, "tenant_alpha", "opaque_value"); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("private/state role reuse did not fail closed: %v", err)
	}
	keys.private[1] = originalPrivate
	privateCommitment, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0SourceDomain, "tenant_alpha", "private_public_separation")
	if err != nil {
		t.Fatal(err)
	}
	publicCommitment, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0PublicTraceDomain, "tenant_alpha", "private_public_separation")
	if err != nil {
		t.Fatal(err)
	}
	originalPublic := keys.public[1]
	for _, test := range []struct {
		name string
		key  StridePI0ManagedMACKey
	}{
		{"same key id", StridePI0ManagedMACKey{ID: originalPrivate.ID, Version: 1, Secret: append([]byte(nil), originalPublic.Secret...)}},
		{"same secret different id", StridePI0ManagedMACKey{ID: originalPublic.ID, Version: 1, Secret: append([]byte(nil), originalPrivate.Secret...)}},
	} {
		t.Run("private_public_"+test.name, func(t *testing.T) {
			keys.public[1] = test.key
			if _, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0SourceDomain, "tenant_alpha", "private_public_separation"); !errors.Is(err, ErrStridePI0Unavailable) {
				t.Fatalf("private mint accepted reused public role: %v", err)
			}
			if _, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0PublicTraceDomain, "tenant_alpha", "private_public_separation"); !errors.Is(err, ErrStridePI0Unavailable) {
				t.Fatalf("public mint accepted reused private role: %v", err)
			}
			if err := VerifyStridePI0ManagedCommitment(ctx, keys, privateCommitment, stridePI0SourceDomain, "tenant_alpha", "private_public_separation"); !errors.Is(err, ErrStridePI0Unavailable) {
				t.Fatalf("private verify accepted reused public role: %v", err)
			}
			if err := VerifyStridePI0ManagedCommitment(ctx, keys, publicCommitment, stridePI0PublicTraceDomain, "tenant_alpha", "private_public_separation"); !errors.Is(err, ErrStridePI0Unavailable) {
				t.Fatalf("public verify accepted reused private role: %v", err)
			}
			keys.public[1] = originalPublic
		})
	}
}

func indexStridePI0String(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func TestStridePI0AllEventTypesUseClosedReferenceAndStateSchemas(t *testing.T) {
	t.Parallel()
	if len(stridePI0EventTypes) != 57 || len(stridePI0EventReferenceRules) != 57 || len(stridePI0ExpectedEventRules) != 57 {
		t.Fatalf("taxonomy/rules/oracle = %d/%d/%d, want 57/57/57", len(stridePI0EventTypes), len(stridePI0EventReferenceRules), len(stridePI0ExpectedEventRules))
	}
	keys := newStridePI0TestKeyring()
	at := time.Date(2026, 8, 9, 16, 0, 0, 0, time.UTC)
	types := make([]string, 0, len(stridePI0EventTypes))
	for eventType := range stridePI0EventTypes {
		types = append(types, eventType)
	}
	sort.Strings(types)
	for i, eventType := range types {
		eventType := eventType
		t.Run(eventType, func(t *testing.T) {
			rule, ok := stridePI0ExpectedEventRules[eventType]
			if !ok || len(rule.CausationAllowed) == 0 || rule.CausationMin < 1 || (rule.SubjectMax > 0 && len(rule.SubjectAllowed) == 0) {
				t.Fatalf("missing closed rule: %+v", rule)
			}
			if production, found := stridePI0EventReferenceRules[eventType]; !found || !reflect.DeepEqual(production, rule) {
				t.Fatalf("production rule differs from independent oracle: got=%+v want=%+v", production, rule)
			}
			aggregate := rule.AggregateType
			parent := "event_parent"
			if eventType == "source.bound_to_trace" {
				parent = ""
			}
			event := stridePI0TestEvent(at.Add(time.Duration(i)*time.Minute), fmt.Sprintf("event_%03d", i), eventType, parent, aggregate, fmt.Sprintf("aggregate_%03d", i), 1)
			_ = sealStridePI0TestEvent(t, keys, event)
			if rule.SubjectMin > 0 {
				missing := event
				missing.SubjectRefs = nil
				missing = mintStridePI0TestCommitments(t, keys, missing)
				if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, missing); !errors.Is(err, ErrStridePI0Invalid) {
					t.Fatalf("missing subject accepted: %v", err)
				}
			}
			forbidden := event
			forbidden.SubjectRefs = []StridePI0Reference{stridePI0TestRef(stridePI0ForbiddenReference(rule.SubjectAllowed), "forbidden_subject", 1)}
			forbidden = mintStridePI0TestCommitments(t, keys, forbidden)
			if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, forbidden); !errors.Is(err, ErrStridePI0Invalid) {
				t.Fatalf("forbidden subject accepted: %v", err)
			}
			wrongCause := event
			wrongCause.CausationRefs = []StridePI0Reference{stridePI0TestRef(stridePI0ForbiddenReference(rule.CausationAllowed), "forbidden_cause", 1)}
			wrongCause = mintStridePI0TestCommitments(t, keys, wrongCause)
			if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, wrongCause); !errors.Is(err, ErrStridePI0Invalid) {
				t.Fatalf("forbidden causation accepted: %v", err)
			}
			missingCause := event
			missingCause.CausationRefs = nil
			missingCause = mintStridePI0TestCommitments(t, keys, missingCause)
			if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, missingCause); !errors.Is(err, ErrStridePI0Invalid) {
				t.Fatalf("missing required/one-of cause accepted: %v", err)
			}
			wrongConsent := event
			if rule.Consent == "required" {
				wrongConsent.ConsentRefs = nil
			} else {
				wrongConsent.ConsentRefs = []StridePI0Reference{stridePI0TestRef("consent", "unexpected_consent", 1)}
			}
			wrongConsent = mintStridePI0TestCommitments(t, keys, wrongConsent)
			if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, wrongConsent); !errors.Is(err, ErrStridePI0Invalid) {
				t.Fatalf("wrong consent applicability accepted: %v", err)
			}
			wrongState := event
			switch {
			case wrongState.Decision != "":
				wrongState.Decision = ""
			case wrongState.State != "":
				wrongState.State = ""
			case wrongState.FailureClass != "":
				wrongState.FailureClass = ""
			case wrongState.Revocation != nil:
				wrongState.Revocation = nil
			case wrongState.Purge != nil:
				wrongState.Purge = nil
			default:
				wrongState.Decision = "approved"
			}
			wrongState = mintStridePI0TestCommitments(t, keys, wrongState)
			if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, wrongState); !errors.Is(err, ErrStridePI0Invalid) {
				t.Fatalf("wrong state accepted: %v", err)
			}
		})
	}
}

func stridePI0ForbiddenReference(allowed []string) string {
	for _, candidate := range []string{"service", "block", "provider_receipt", "transcript", "contact"} {
		if !containsSTRIDEString(allowed, candidate) {
			return candidate
		}
	}
	return "model"
}

func TestStridePI0EventReferenceOneOfCardinalityAndConsentApplicability(t *testing.T) {
	t.Parallel()
	keys := newStridePI0TestKeyring()
	at := time.Date(2026, 8, 9, 17, 0, 0, 0, time.UTC)
	conversation := stridePI0TestEvent(at, "event_conversation", "source.bound_to_trace", "", "intent", "intent_conversation", 1)
	if len(conversation.CausationRefs) != 1 || conversation.CausationRefs[0].Type != "conversation" {
		t.Fatalf("conversation fixture: %+v", conversation.CausationRefs)
	}
	_ = sealStridePI0TestEvent(t, keys, conversation)
	transcript := stridePI0TestEvent(at, "event_transcript", "source.bound_to_trace", "", "intent", "intent_transcript", 1)
	transcript.CausationRefs = []StridePI0Reference{stridePI0TestRef("transcript", "transcript_only", 1)}
	_ = sealStridePI0TestEvent(t, keys, transcript)
	both := conversation
	both.CausationRefs = []StridePI0Reference{stridePI0TestRef("conversation", "conversation_both", 1), stridePI0TestRef("transcript", "transcript_both", 1)}
	both = mintStridePI0TestCommitments(t, keys, both)
	if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, both); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("one-of cardinality admitted both: %v", err)
	}

	reconciled := stridePI0TestEvent(at, "event_reconciled", "lifecycle.reconciled", "event_parent", "journal", "journal_reconciled", 1)
	if len(reconciled.SubjectRefs) != 0 || len(reconciled.ConsentRefs) != 0 {
		t.Fatalf("N/A subject/consent not empty: %+v %+v", reconciled.SubjectRefs, reconciled.ConsentRefs)
	}
	_ = sealStridePI0TestEvent(t, keys, reconciled)
	withSubject := reconciled
	withSubject.SubjectRefs = []StridePI0Reference{stridePI0TestRef("journal", "unexpected_subject", 1)}
	withSubject = mintStridePI0TestCommitments(t, keys, withSubject)
	if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, withSubject); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("N/A subject admitted: %v", err)
	}
	withConsent := reconciled
	withConsent.ConsentRefs = []StridePI0Reference{stridePI0TestRef("consent", "unexpected_consent", 1)}
	withConsent = mintStridePI0TestCommitments(t, keys, withConsent)
	if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, withConsent); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("N/A consent admitted: %v", err)
	}

	applied := stridePI0TestEvent(at, "event_applied", "effect.applied", "event_parent", "effect", "effect_applied", 1)
	if len(applied.CausationRefs) != 2 {
		t.Fatalf("required effect refs: %+v", applied.CausationRefs)
	}
	missingProvider := applied
	missingProvider.CausationRefs = []StridePI0Reference{stridePI0TestRef("effect", "effect_only", 1)}
	missingProvider = mintStridePI0TestCommitments(t, keys, missingProvider)
	if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, missingProvider); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("missing provider receipt admitted: %v", err)
	}
	search := stridePI0TestEvent(at, "event_search", "collaboration.search_admitted", "event_parent", "contact", "search_admission", 1)
	if len(search.SubjectRefs) != 0 || len(search.CausationRefs) != 3 {
		t.Fatalf("search bindings: causes=%+v subjects=%+v", search.CausationRefs, search.SubjectRefs)
	}
	missingGrant := search
	missingGrant.CausationRefs = append([]StridePI0Reference(nil), search.CausationRefs[1:]...)
	missingGrant = mintStridePI0TestCommitments(t, keys, missingGrant)
	if _, err := SealStridePI0LifecycleEvent(context.Background(), keys, missingGrant); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("missing search grant admitted: %v", err)
	}
}

func sealStridePI0TestEvent(t *testing.T, keys StridePI0ManagedMACKeyring, event StridePI0LifecycleEvent) StridePI0LifecycleEvent {
	t.Helper()
	event = mintStridePI0TestCommitments(t, keys, event)
	sealed, err := SealStridePI0LifecycleEvent(context.Background(), keys, event)
	if err != nil {
		t.Fatalf("seal event %s: %v", event.EventType, err)
	}
	return sealed
}

func mintStridePI0TestCommitments(t *testing.T, keys StridePI0ManagedMACKeyring, event StridePI0LifecycleEvent) StridePI0LifecycleEvent {
	t.Helper()
	commitments := []struct {
		destination *StridePI0ManagedCommitment
		domain      string
		values      []string
	}{
		{&event.IdempotencyCommitment, stridePI0IdempotencyDomain, []string{event.TenantID, event.EventID, event.EventType}},
		{&event.SourceCommitment, stridePI0SourceDomain, []string{event.TenantID, event.TraceID, event.Aggregate.Type, event.Aggregate.ID}},
		{&event.OutputCommitment, stridePI0OutputDomain, []string{event.EventID, event.Aggregate.Digest, event.Decision, event.State, event.FailureClass}},
		{&event.PublicTraceCommitment, stridePI0PublicTraceDomain, []string{event.TenantID, event.TraceID}},
	}
	for _, item := range commitments {
		commitment, err := MintStridePI0ManagedCommitment(context.Background(), keys, item.domain, item.values...)
		if err != nil {
			t.Fatalf("mint commitment %s: %v", item.domain, err)
		}
		*item.destination = commitment
	}
	return event
}

func TestStridePI0LifecycleEventAndTraceGraph(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	root := stridePI0TestEvent(at, "event_source", "source.bound_to_trace", "", "intent", "intent_alpha", 1)
	intent := stridePI0TestEvent(at.Add(time.Minute), "event_intent", "intent.admitted", root.EventID, "intent", "intent_alpha", 2)
	suggestion := stridePI0TestEvent(at.Add(2*time.Minute), "event_suggestion", "suggestion.created", intent.EventID, "suggestion", "suggestion_alpha", 1)
	run := stridePI0TestEvent(at.Add(3*time.Minute), "event_run", "run.created", suggestion.EventID, "run", "run_alpha", 1)
	artifact := stridePI0TestEvent(at.Add(4*time.Minute), "event_artifact", "artifact.created", run.EventID, "artifact", "artifact_alpha", 1)
	outcome := stridePI0TestEvent(at.Add(5*time.Minute), "event_outcome", "outcome.recorded", artifact.EventID, "outcome", "outcome_alpha", 1)

	events := []StridePI0LifecycleEvent{root, intent, suggestion, run, artifact, outcome}
	for i := range events {
		events[i] = sealStridePI0TestEvent(t, keys, events[i])
	}
	if err := VerifyStridePI0TraceGraph(ctx, keys, events); err != nil {
		t.Fatalf("valid trace: %v", err)
	}

	raw, err := canonicalJSON(events[0])
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAndVerifyStridePI0LifecycleEvent(ctx, keys, raw)
	if err != nil || decoded.EventID != events[0].EventID {
		t.Fatalf("strict decode: event=%+v err=%v", decoded, err)
	}

	var withBody map[string]any
	if err := jsonUnmarshalForStridePI0Test(raw, &withBody); err != nil {
		t.Fatal(err)
	}
	withBody["body"] = "private text"
	unknownRaw, err := canonicalJSON(withBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAndVerifyStridePI0LifecycleEvent(ctx, keys, unknownRaw); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("unknown/private key accepted: %v", err)
	}

	tampered := events[2]
	tampered.OutputCommitment.Digest = stridePI0TestDigest("forged")
	if err := VerifyStridePI0LifecycleEvent(ctx, keys, tampered); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("tampered event accepted: %v", err)
	}

	wrongTenant := append([]StridePI0LifecycleEvent(nil), events...)
	wrongTenant[4] = stridePI0TestEvent(at.Add(4*time.Minute), "event_cross_tenant", "artifact.created", run.EventID, "artifact", "artifact_cross", 1)
	wrongTenant[4].TenantID = "tenant_beta"
	wrongTenant[4] = sealStridePI0TestEvent(t, keys, wrongTenant[4])
	if err := VerifyStridePI0TraceGraph(ctx, keys, wrongTenant); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("cross-tenant graph accepted: %v", err)
	}

	passive := stridePI0TestEvent(at, "event_passive", "source.observed", "", "intent", "intent_passive", 1)
	if _, err := SealStridePI0LifecycleEvent(ctx, keys, passive); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("passive observation accepted: %v", err)
	}
	if _, err := SealStridePI0LifecycleEvent(ctx, nil, root); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("caller-constructed event accepted without managed keys: %v", err)
	}

	wrongDecision := stridePI0TestEvent(at, "event_review", "artifact.review_decided", "event_artifact_parent", "artifact", "artifact_reviewed", 1)
	wrongDecision.Decision = "accepted"
	if _, err := SealStridePI0LifecycleEvent(ctx, keys, wrongDecision); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("wrong closed decision accepted: %v", err)
	}
	wrongAggregate := stridePI0TestEvent(at, "event_wrong_aggregate", "outcome.recorded", "event_artifact_parent", "artifact", "artifact_wrong", 1)
	if _, err := SealStridePI0LifecycleEvent(ctx, keys, wrongAggregate); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("wrong event aggregate accepted: %v", err)
	}
	modelAssisted := stridePI0TestEvent(at, "event_model", "artifact.created", "event_run_parent", "artifact", "artifact_model", 1)
	modelAssisted.Provenance = "model_assisted"
	if _, err := SealStridePI0LifecycleEvent(ctx, keys, modelAssisted); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("model provenance without exact manifest refs accepted: %v", err)
	}
	modelAssisted.ProvenanceRefs = []StridePI0Reference{
		stridePI0TestRef("model", "model_route", 1),
		stridePI0TestRef("prompt_config", "prompt_config", 1),
		stridePI0TestRef("evidence_manifest", "evidence_manifest", 1),
	}
	_ = sealStridePI0TestEvent(t, keys, modelAssisted)
	extendedSourceRetention := root
	extendedSourceRetention.Retention.RetainUntil = extendedSourceRetention.RecordedAt.Add(31 * 24 * time.Hour)
	if _, err := SealStridePI0LifecycleEvent(ctx, keys, extendedSourceRetention); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("overlong source-link retention accepted: %v", err)
	}

	revisionGap := append([]StridePI0LifecycleEvent(nil), events...)
	revisionGap[1] = stridePI0TestEvent(at.Add(time.Minute), "event_intent_gap", "intent.admitted", root.EventID, "intent", "intent_alpha", 3)
	revisionGap[1] = sealStridePI0TestEvent(t, keys, revisionGap[1])
	if err := VerifyStridePI0TraceGraph(ctx, keys, revisionGap); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("aggregate revision gap accepted: %v", err)
	}

	keys.current = 2
	rotated := stridePI0TestEvent(at, "event_rotated", "source.bound_to_trace", "", "intent", "intent_rotated", 1)
	rotated = sealStridePI0TestEvent(t, keys, rotated)
	if rotated.KeyVersion != 2 || VerifyStridePI0LifecycleEvent(ctx, keys, events[0]) != nil || VerifyStridePI0LifecycleEvent(ctx, keys, rotated) != nil {
		t.Fatal("managed-key rotation did not retain old verification")
	}
}

// Keep encoding/json out of production helper surfaces while still exercising
// strict decoding with a recursively unknown private field.
func jsonUnmarshalForStridePI0Test(raw []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	return decoder.Decode(destination)
}

type stridePI0TestPostimageReader struct {
	actual []StridePI0Postimage
	err    error
	calls  int
}

func (r *stridePI0TestPostimageReader) ReadStridePI0Postimages(context.Context, string, []StridePI0Postimage) ([]StridePI0Postimage, error) {
	r.calls++
	return append([]StridePI0Postimage(nil), r.actual...), r.err
}

type stridePI0TestReceiptResolver struct {
	receipt StridePI0EffectReceipt
	err     error
	calls   int
}

func (r *stridePI0TestReceiptResolver) ResolveStridePI0EffectReceipt(context.Context, string, string, string) (StridePI0EffectReceipt, error) {
	r.calls++
	return r.receipt, r.err
}

func stridePI0TestJournal(at time.Time) StridePI0CompoundJournal {
	return StridePI0CompoundJournal{
		OperationID: "operation_alpha", TenantID: "tenant_alpha", TraceID: "trace_alpha",
		Aggregate:            StridePI0Aggregate{Type: "effect", ID: "effect_alpha", Revision: 1, Digest: stridePI0TestDigest("effect")},
		Principal:            StridePI0Principal{Kind: "service", OrganizationID: "organization_alpha", ServiceID: "service_runner", ControllerRevision: 3},
		OperationFingerprint: stridePI0TestDigest(stridePI0OperationDomain + "operation_alpha"),
		RequestedEvents:      []StridePI0JournalEvent{{EventID: "event_effect", EventType: "effect.applied", Digest: stridePI0TestDigest("event_effect")}},
		Preimages: []StridePI0Postimage{
			{Store: "artifact_store", Type: "artifact", ID: "artifact_alpha", Revision: 1, Digest: stridePI0TestDigest("before"), HighWater: 10},
		},
		ExpectedPostimages: []StridePI0Postimage{
			{Store: "artifact_store", Type: "artifact", ID: "artifact_alpha", Revision: 2, Digest: stridePI0TestDigest("after"), HighWater: 11},
		},
		EffectAdapterID: "artifact_writer", AdapterOperationID: "adapter_operation_alpha", ExpectedEffectReceiptID: "effect_receipt_alpha",
		AuthorityEnvelopeDigest: stridePI0TestDigest("authority"), UpdatedAt: at,
	}
}

func stridePI0TestEffectReceipt(t *testing.T, keys StridePI0EffectEvidenceKeyring, journal StridePI0CompoundJournal, postimages []StridePI0Postimage, outcome string, at time.Time) StridePI0EffectReceipt {
	t.Helper()
	receipt, err := SealStridePI0EffectReceipt(context.Background(), keys, StridePI0EffectReceipt{
		ReceiptID: journal.ExpectedEffectReceiptID, OperationID: journal.OperationID, OperationFingerprint: journal.OperationFingerprint,
		AdapterID: journal.EffectAdapterID, AdapterOperationID: journal.AdapterOperationID, Outcome: outcome,
		Postimages: append([]StridePI0Postimage(nil), postimages...), IssuedAt: at,
	})
	if err != nil {
		t.Fatalf("seal effect receipt: %v", err)
	}
	return receipt
}

type stridePI0TestHighWater struct {
	mu              sync.Mutex
	values          map[string]StridePI0CarrierHighWater
	failAfterCommit bool
	beforeCAS       func() error
}

func (s *stridePI0TestHighWater) ReadStridePI0CarrierHighWater(_ context.Context, path string) (StridePI0CarrierHighWater, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[path], nil
}

func (s *stridePI0TestHighWater) CompareAndSwapStridePI0CarrierHighWater(_ context.Context, path string, prior, next StridePI0CarrierHighWater) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.beforeCAS != nil {
		if err := s.beforeCAS(); err != nil {
			return err
		}
		s.beforeCAS = nil
	}
	if s.values[path] != prior {
		return ErrStridePI0Conflict
	}
	s.values[path] = next
	if s.failAfterCommit {
		s.failAfterCommit = false
		return errors.New("lost high-water response")
	}
	return nil
}

type stridePI0TestEventStore struct {
	mu             sync.Mutex
	events         map[string]StridePI0LifecycleEvent
	appendCalls    int
	failAfterWrite bool
	fences         *stridePI0TestFenceInstaller
}

func (s *stridePI0TestEventStore) ReadStridePI0LifecycleEvent(_ context.Context, eventID string) (StridePI0LifecycleEvent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, found := s.events[eventID]
	return event, found, nil
}

func (s *stridePI0TestEventStore) AppendStridePI0LifecycleEventOnce(_ context.Context, operationID, fingerprint string, event StridePI0LifecycleEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fences != nil && s.fences.count() == 0 {
		return errors.New("event append preceded recovery fence")
	}
	if !strideIdentifier(operationID) || !isHexDigest(fingerprint) {
		return ErrStridePI0Invalid
	}
	s.appendCalls++
	if existing, found := s.events[event.EventID]; found {
		left, _ := canonicalJSON(existing)
		right, _ := canonicalJSON(event)
		if !bytes.Equal(left, right) {
			return ErrStridePI0Conflict
		}
	} else {
		s.events[event.EventID] = event
	}
	if s.failAfterWrite {
		s.failAfterWrite = false
		return errors.New("lost append response")
	}
	return nil
}

type stridePI0TestFenceInstaller struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *stridePI0TestFenceInstaller) InstallAndReadStridePI0RecoveryFence(_ context.Context, operationID, fingerprint string, committed []StridePI0Postimage) (StridePI0Postimage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return StridePI0Postimage{}, f.err
	}
	if !strideIdentifier(operationID) || !isHexDigest(fingerprint) || len(committed) == 0 {
		return StridePI0Postimage{}, ErrStridePI0Invalid
	}
	return StridePI0Postimage{Store: "pi0_fence_store", Type: "journal", ID: operationID + "_fence", Revision: 1, Digest: stridePI0TestDigest(operationID + fingerprint), HighWater: 1}, nil
}

func (f *stridePI0TestFenceInstaller) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func stridePI0PrepareCarrierJournal(t *testing.T, keys StridePI0ManagedMACKeyring, authority StridePI0CurrentAuthority, event StridePI0LifecycleEvent, at time.Time) StridePI0CompoundJournal {
	t.Helper()
	descriptor, err := stridePI0EventDescriptor(event)
	if err != nil {
		t.Fatal(err)
	}
	draft := stridePI0TestJournal(at)
	draft.TenantID = event.TenantID
	draft.TraceID = event.TraceID
	draft.Aggregate = event.Aggregate
	draft.Principal = event.Principal
	draft.RequestedEvents = []StridePI0JournalEvent{descriptor}
	journal, err := PrepareStridePI0CompoundJournal(context.Background(), keys, authority, draft)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func stridePI0AppliedCarrierForDraft(t *testing.T, keys StridePI0ManagedMACKeyring, authority StridePI0CurrentAuthority, draft StridePI0CompoundJournal, at time.Time) (*StridePI0FileCarrier, StridePI0CompoundJournal, StridePI0Postimage, *stridePI0TestHighWater) {
	t.Helper()
	ctx := context.Background()
	journal, err := PrepareStridePI0CompoundJournal(ctx, keys, authority, draft)
	if err != nil {
		t.Fatal(err)
	}
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	carrier, err := OpenStridePI0FileCarrier(ctx, filepath.Join(t.TempDir(), "carrier.json"), keys, highWater)
	if err != nil || carrier.CreateJournal(ctx, journal) != nil {
		t.Fatalf("create carrier: %v", err)
	}
	for index, phase := range []string{"effect_requested", "effect_approved"} {
		next, transitionErr := TransitionStridePI0CompoundJournal(ctx, keys, authority, journal, phase, nil, "", at.Add(time.Duration(index+1)*time.Second))
		if transitionErr != nil || carrier.CompareAndSwapJournal(ctx, journal, next) != nil {
			t.Fatalf("persist %s: %v", phase, transitionErr)
		}
		journal = next
	}
	evidenceKeys := &stridePI0TestEvidenceKeyring{key: StridePI0EffectEvidenceKey{ID: "pi0_evidence_key", Version: 4, Secret: []byte(strings.Repeat("e", 32))}}
	receipt := stridePI0TestEffectReceipt(t, evidenceKeys, journal, journal.ExpectedPostimages, "applied", at.Add(3*time.Second))
	applied, err := RecordStridePI0EffectReceipt(ctx, keys, evidenceKeys, authority, journal, receipt, at.Add(3*time.Second))
	if err != nil || carrier.CompareAndSwapJournal(ctx, journal, applied) != nil {
		t.Fatalf("persist applied: %v", err)
	}
	fence := StridePI0Postimage{Store: "pi0_fence_store", Type: "journal", ID: applied.OperationID + "_fence", Revision: 1, Digest: stridePI0TestDigest("authority_fence"), HighWater: 1}
	return carrier, applied, fence, highWater
}

func TestStridePI0AppendEventsBindsExactAuthorityTupleBeforeMutationAndReplay(t *testing.T) {
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	current := &stridePI0TestCurrentAuthority{}
	at := time.Date(2026, 8, 9, 18, 20, 0, 0, time.UTC)
	base := sealStridePI0TestEvent(t, keys, stridePI0TestEvent(at, "event_effect", "effect.applied", "event_parent", "effect", "effect_alpha", 1))

	mutations := []struct {
		name   string
		mutate func(*StridePI0LifecycleEvent)
	}{
		{"person", func(event *StridePI0LifecycleEvent) { event.Principal.PersonID = "person_other" }},
		{"organization", func(event *StridePI0LifecycleEvent) { event.Principal.OrganizationID = "organization_other" }},
		{"tenant", func(event *StridePI0LifecycleEvent) { event.TenantID = "tenant_other" }},
		{"trace", func(event *StridePI0LifecycleEvent) { event.TraceID = "trace_other" }},
		{"aggregate", func(event *StridePI0LifecycleEvent) {
			event.Aggregate.ID = "effect_other"
			event.Aggregate.Digest = stridePI0TestDigest("effect_other")
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			crossed := base
			test.mutate(&crossed)
			crossed = sealStridePI0TestEvent(t, keys, crossed)
			descriptor, err := stridePI0EventDescriptor(crossed)
			if err != nil {
				t.Fatal(err)
			}
			draft := stridePI0TestJournal(at)
			draft.TenantID, draft.TraceID, draft.Aggregate, draft.Principal = base.TenantID, base.TraceID, base.Aggregate, base.Principal
			draft.RequestedEvents = []StridePI0JournalEvent{descriptor}
			carrier, applied, fence, _ := stridePI0AppliedCarrierForDraft(t, keys, current, draft, at)
			before, err := os.ReadFile(carrier.path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := carrier.AppendEventsOnce(ctx, current, applied.Principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{crossed}, at.Add(4*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
				t.Fatalf("cross-%s event admitted: %v", test.name, err)
			}
			after, err := os.ReadFile(carrier.path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("cross-%s event mutated carrier: %v", test.name, err)
			}
		})
	}

	descriptor, err := stridePI0EventDescriptor(base)
	if err != nil {
		t.Fatal(err)
	}
	draft := stridePI0TestJournal(at)
	draft.TenantID, draft.TraceID, draft.Aggregate, draft.Principal = base.TenantID, base.TraceID, base.Aggregate, base.Principal
	draft.RequestedEvents = []StridePI0JournalEvent{descriptor}
	carrier, applied, fence, _ := stridePI0AppliedCarrierForDraft(t, keys, current, draft, at)
	if _, err := carrier.AppendEventsOnce(ctx, current, applied.Principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{base}, at.Add(4*time.Second)); err != nil {
		t.Fatalf("initial append: %v", err)
	}
	before, err := os.ReadFile(carrier.path)
	if err != nil {
		t.Fatal(err)
	}
	other := applied.Principal
	other.PersonID = "person_other"
	if _, err := carrier.AppendEventsOnce(ctx, current, other, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{base}, at.Add(5*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("cross-principal receipt replay admitted: %v", err)
	}
	after, err := os.ReadFile(carrier.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("cross-principal replay mutated carrier: %v", err)
	}
}

func TestStridePI0HistoricalRoleCollisionsFailEveryVerificationPathWhileCurrentRotationRemainsUsable(t *testing.T) {
	at := time.Date(2026, 8, 9, 18, 25, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*stridePI0TestKeyring)
	}{
		{"state_private_same_id", func(keys *stridePI0TestKeyring) {
			key := keys.private[1]
			key.ID = keys.keys[1].ID
			keys.private[1] = key
		}},
		{"state_private_same_secret", func(keys *stridePI0TestKeyring) {
			key := keys.private[1]
			key.Secret = append([]byte(nil), keys.keys[1].Secret...)
			keys.private[1] = key
		}},
		{"state_public_same_id", func(keys *stridePI0TestKeyring) {
			key := keys.public[1]
			key.ID = keys.keys[1].ID
			keys.public[1] = key
		}},
		{"state_public_same_secret", func(keys *stridePI0TestKeyring) {
			key := keys.public[1]
			key.Secret = append([]byte(nil), keys.keys[1].Secret...)
			keys.public[1] = key
		}},
		{"private_public_same_id", func(keys *stridePI0TestKeyring) {
			key := keys.public[1]
			key.ID = keys.private[1].ID
			keys.public[1] = key
		}},
		{"private_public_same_secret", func(keys *stridePI0TestKeyring) {
			key := keys.public[1]
			key.Secret = append([]byte(nil), keys.private[1].Secret...)
			keys.public[1] = key
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			keys := newStridePI0TestKeyring()
			current := &stridePI0TestCurrentAuthority{}
			event := sealStridePI0TestEvent(t, keys, stridePI0TestEvent(at, "event_effect", "effect.applied", "event_parent", "effect", "effect_alpha", 1))
			descriptor, err := stridePI0EventDescriptor(event)
			if err != nil {
				t.Fatal(err)
			}
			draft := stridePI0TestJournal(at)
			draft.TenantID, draft.TraceID, draft.Aggregate, draft.Principal = event.TenantID, event.TraceID, event.Aggregate, event.Principal
			draft.RequestedEvents = []StridePI0JournalEvent{descriptor}
			carrier, applied, fence, highWater := stridePI0AppliedCarrierForDraft(t, keys, current, draft, at)
			appendReceipt, err := carrier.AppendEventsOnce(ctx, current, applied.Principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			privateCommitment, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0SourceDomain, "tenant_alpha", "historical_private")
			if err != nil {
				t.Fatal(err)
			}
			publicCommitment, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0PublicTraceDomain, "tenant_alpha", "historical_public")
			if err != nil {
				t.Fatal(err)
			}

			keys.current = 2
			test.mutate(keys)
			if _, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0SourceDomain, "tenant_alpha", "current_v2"); err != nil {
				t.Fatalf("clean current rotation stopped minting: %v", err)
			}
			currentEvent := stridePI0TestEvent(at.Add(time.Hour), "event_current", "effect.applied", "event_parent", "effect", "effect_current", 1)
			if _, err := SealStridePI0LifecycleEvent(ctx, keys, mintStridePI0TestCommitments(t, keys, currentEvent)); err != nil {
				t.Fatalf("clean current rotation stopped state sealing: %v", err)
			}
			for name, verify := range map[string]func() error{
				"event":          func() error { return VerifyStridePI0LifecycleEvent(ctx, keys, event) },
				"journal":        func() error { return VerifyStridePI0CompoundJournal(ctx, keys, applied) },
				"append receipt": func() error { return VerifyStridePI0EventAppendReceipt(ctx, keys, appendReceipt) },
				"private commitment": func() error {
					return VerifyStridePI0ManagedCommitment(ctx, keys, privateCommitment, stridePI0SourceDomain, "tenant_alpha", "historical_private")
				},
				"public commitment": func() error {
					return VerifyStridePI0ManagedCommitment(ctx, keys, publicCommitment, stridePI0PublicTraceDomain, "tenant_alpha", "historical_public")
				},
			} {
				if err := verify(); err == nil {
					t.Fatalf("%s verification accepted historical role collision", name)
				}
			}
			if _, err := OpenStridePI0FileCarrier(ctx, carrier.path, keys, highWater); err == nil {
				t.Fatal("carrier verification accepted historical role collision")
			}
		})
	}
}

func TestStridePI0FileCarrierCrashRecoveryAppendOnceAndRevokedCaller(t *testing.T) {
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	evidenceKeys := &stridePI0TestEvidenceKeyring{key: StridePI0EffectEvidenceKey{ID: "pi0_evidence_key", Version: 4, Secret: []byte(strings.Repeat("e", 32))}}
	current := &stridePI0TestCurrentAuthority{}
	recovery := &stridePI0TestRecoveryAuthority{allowed: true}
	at := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	event := sealStridePI0TestEvent(t, keys, stridePI0TestEvent(at, "event_effect", "effect.applied", "event_parent", "effect", "effect_alpha", 1))
	journal := stridePI0PrepareCarrierJournal(t, keys, current, event, at)
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	path := filepath.Join(t.TempDir(), "pi0-carrier.json")
	carrier, err := OpenStridePI0FileCarrier(ctx, path, keys, highWater)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := carrier.CreateJournal(ctx, journal); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i, phase := range []string{"effect_requested", "effect_approved"} {
		next, transitionErr := TransitionStridePI0CompoundJournal(ctx, keys, current, journal, phase, nil, "", at.Add(time.Duration(i+1)*time.Second))
		if transitionErr != nil || carrier.CompareAndSwapJournal(ctx, journal, next) != nil {
			t.Fatalf("persist %s: %v", phase, transitionErr)
		}
		journal = next
	}
	receipt := stridePI0TestEffectReceipt(t, evidenceKeys, journal, journal.ExpectedPostimages, "applied", at.Add(3*time.Second))
	reader := &stridePI0TestPostimageReader{actual: journal.ExpectedPostimages}
	resolver := &stridePI0TestReceiptResolver{receipt: receipt}
	fences := &stridePI0TestFenceInstaller{}
	store := &stridePI0TestEventStore{events: map[string]StridePI0LifecycleEvent{}, failAfterWrite: true, fences: fences}
	current.setRevoked(true)
	if _, err := RepairStridePI0CarrierOperation(ctx, carrier, evidenceKeys, recovery, reader, resolver, fences, store, journal.OperationID, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("lost append response not retained: %v", err)
	}
	partial, _, err := carrier.ReadOperation(ctx, journal.OperationID)
	if err != nil || partial.Phase != "effect_reconciled" || partial.Reconciliation != "applied" || store.appendCalls != 1 {
		t.Fatalf("pre-seal recovery: %+v calls=%d err=%v", partial, store.appendCalls, err)
	}
	fence, err := fences.InstallAndReadStridePI0RecoveryFence(ctx, journal.OperationID, journal.OperationFingerprint, partial.CommittedEffectPostimage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := carrier.appendEventsOnceForRecovery(ctx, recovery, journal.OperationID, journal.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(5*time.Second)); err != nil {
		t.Fatalf("persist append before simulated crash: %v", err)
	}
	eventsReconciled, _, err := carrier.ReadOperation(ctx, journal.OperationID)
	if err != nil || eventsReconciled.Phase != "events_reconciled" {
		t.Fatalf("pre-crash append phase: %+v err=%v", eventsReconciled, err)
	}

	reopened, err := OpenStridePI0FileCarrier(ctx, path, keys, highWater)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	committed, err := RepairStridePI0CarrierOperation(ctx, reopened, evidenceKeys, recovery, reader, resolver, fences, store, journal.OperationID, []StridePI0LifecycleEvent{event}, at.Add(6*time.Second))
	if err != nil || committed.Phase != "committed" || store.appendCalls != 1 {
		t.Fatalf("repair after revoke/restart: %+v calls=%d err=%v", committed, store.appendCalls, err)
	}
	if _, err := reopened.ReadOperationForCaller(ctx, current, journal.Principal, journal.OperationID); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("revoked caller read repaired operation: %v", err)
	}
	_, appendReceipt, err := reopened.ReadOperation(ctx, journal.OperationID)
	if err != nil || VerifyStridePI0EventAppendReceipt(ctx, keys, appendReceipt) != nil || !stridePI0EqualPostimages(appendReceipt.CommittedEffectPostimage, journal.ExpectedPostimages) {
		t.Fatalf("append receipt: %+v err=%v", appendReceipt, err)
	}
	replay, err := reopened.appendEventsOnceForRecovery(ctx, recovery, journal.OperationID, journal.OperationFingerprint, appendReceipt.RecoveryFencePostimage, []StridePI0LifecycleEvent{event}, at.Add(7*time.Second))
	if err != nil || replay.MAC != appendReceipt.MAC {
		t.Fatalf("append replay changed receipt: %+v err=%v", replay, err)
	}
}

func TestStridePI0CurrentAuthorityHeldThroughFinalAppendAndAppliedCannotRetry(t *testing.T) {
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	evidenceKeys := &stridePI0TestEvidenceKeyring{key: StridePI0EffectEvidenceKey{ID: "pi0_evidence_key", Version: 4, Secret: []byte(strings.Repeat("e", 32))}}
	current := &stridePI0TestCurrentAuthority{}
	at := time.Date(2026, 8, 9, 18, 30, 0, 0, time.UTC)
	event := sealStridePI0TestEvent(t, keys, stridePI0TestEvent(at, "event_effect", "effect.applied", "event_parent", "effect", "effect_alpha", 1))
	journal := stridePI0PrepareCarrierJournal(t, keys, current, event, at)
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	path := filepath.Join(t.TempDir(), "authority-carrier.json")
	carrier, err := OpenStridePI0FileCarrier(ctx, path, keys, highWater)
	if err != nil || carrier.CreateJournal(ctx, journal) != nil {
		t.Fatalf("create carrier: %v", err)
	}
	for i, phase := range []string{"effect_requested", "effect_approved"} {
		next, transitionErr := TransitionStridePI0CompoundJournal(ctx, keys, current, journal, phase, nil, "", at.Add(time.Duration(i+1)*time.Second))
		if transitionErr != nil || carrier.CompareAndSwapJournal(ctx, journal, next) != nil {
			t.Fatalf("persist %s: %v", phase, transitionErr)
		}
		journal = next
	}
	receipt := stridePI0TestEffectReceipt(t, evidenceKeys, journal, journal.ExpectedPostimages, "applied", at.Add(3*time.Second))
	applied, err := RecordStridePI0EffectReceipt(ctx, keys, evidenceKeys, current, journal, receipt, at.Add(3*time.Second))
	if err != nil || carrier.CompareAndSwapJournal(ctx, journal, applied) != nil {
		t.Fatalf("persist applied: %v", err)
	}
	if _, err := carrier.RetryNotApplied(ctx, current, applied.OperationID, at.Add(4*time.Second)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("applied operation became retryable: %v", err)
	}
	fence := StridePI0Postimage{Store: "pi0_fence_store", Type: "journal", ID: applied.OperationID + "_fence", Revision: 1, Digest: stridePI0TestDigest("authority_fence"), HighWater: 1}
	revokeStarted, revokeDone := make(chan struct{}), make(chan struct{})
	highWater.mu.Lock()
	highWater.beforeCAS = func() error {
		if !current.held.Load() {
			return errors.New("current authority was released before final carrier CAS")
		}
		go func() {
			close(revokeStarted)
			current.setRevoked(true)
			close(revokeDone)
		}()
		<-revokeStarted
		select {
		case <-revokeDone:
			return errors.New("revocation interleaved inside held authority callback")
		default:
			return nil
		}
	}
	highWater.mu.Unlock()
	appendReceipt, err := carrier.AppendEventsOnce(ctx, current, applied.Principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(5*time.Second))
	if err != nil || VerifyStridePI0EventAppendReceipt(ctx, keys, appendReceipt) != nil {
		t.Fatalf("authority-held append: %+v err=%v", appendReceipt, err)
	}
	select {
	case <-revokeDone:
	case <-time.After(time.Second):
		t.Fatal("revocation did not complete after final append released authority")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := carrier.AppendEventsOnce(ctx, current, applied.Principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(6*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("revoked append replay admitted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("revoked append changed carrier bytes: %v", err)
	}
}

func TestStridePI0FileCarrierCASRestartTamperReplayAndNotAppliedRetry(t *testing.T) {
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	current := &stridePI0TestCurrentAuthority{}
	at := time.Date(2026, 8, 9, 19, 0, 0, 0, time.UTC)
	event := sealStridePI0TestEvent(t, keys, stridePI0TestEvent(at, "event_effect", "effect.applied", "event_parent", "effect", "effect_alpha", 1))
	journal := stridePI0PrepareCarrierJournal(t, keys, current, event, at)
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	path := filepath.Join(t.TempDir(), "carrier.json")
	carrier, err := OpenStridePI0FileCarrier(ctx, path, keys, highWater)
	if err != nil {
		t.Fatal(err)
	}
	initialBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := carrier.CreateJournal(ctx, journal); err != nil {
		t.Fatal(err)
	}
	next, err := TransitionStridePI0CompoundJournal(ctx, keys, current, journal, "effect_requested", nil, "", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var successes int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if carrier.CompareAndSwapJournal(ctx, journal, next) == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("CAS successes=%d, want 1", successes)
	}
	if err := os.WriteFile(path, initialBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStridePI0FileCarrier(ctx, path, keys, highWater); !errors.Is(err, ErrStridePI0Conflict) && !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("replayed state accepted: %v", err)
	}

	path2 := filepath.Join(t.TempDir(), "carrier2.json")
	highWater2 := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	carrier2, err := OpenStridePI0FileCarrier(ctx, path2, keys, highWater2)
	if err != nil {
		t.Fatal(err)
	}
	if err := carrier2.CreateJournal(ctx, journal); err != nil {
		t.Fatal(err)
	}
	requested, err := TransitionStridePI0CompoundJournal(ctx, keys, current, journal, "effect_requested", nil, "", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	highWater2.mu.Lock()
	highWater2.failAfterCommit = true
	highWater2.mu.Unlock()
	if err := carrier2.CompareAndSwapJournal(ctx, journal, requested); err == nil {
		t.Fatal("lost high-water response unexpectedly succeeded")
	}
	carrier2, err = OpenStridePI0FileCarrier(ctx, path2, keys, highWater2)
	if err != nil {
		t.Fatalf("transaction restart recovery: %v", err)
	}
	recoveredRequested, _, err := carrier2.ReadOperation(ctx, journal.OperationID)
	if err != nil || recoveredRequested.Phase != "effect_requested" {
		t.Fatalf("transaction did not resume exact phase: %+v err=%v", recoveredRequested, err)
	}
	requested = recoveredRequested
	approved, err := TransitionStridePI0CompoundJournal(ctx, keys, current, requested, "effect_approved", nil, "", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := carrier2.CompareAndSwapJournal(ctx, requested, approved); err != nil {
		t.Fatal(err)
	}
	evidenceKeys := &stridePI0TestEvidenceKeyring{key: StridePI0EffectEvidenceKey{ID: "pi0_evidence_key", Version: 4, Secret: []byte(strings.Repeat("e", 32))}}
	notAppliedReceipt := stridePI0TestEffectReceipt(t, evidenceKeys, approved, approved.Preimages, "not_applied", at.Add(3*time.Second))
	recovery := &stridePI0TestRecoveryAuthority{allowed: true}
	store := &stridePI0TestEventStore{events: map[string]StridePI0LifecycleEvent{}}
	_, repairErr := RepairStridePI0CarrierOperation(ctx, carrier2, evidenceKeys, recovery, &stridePI0TestPostimageReader{actual: approved.Preimages}, &stridePI0TestReceiptResolver{receipt: notAppliedReceipt}, &stridePI0TestFenceInstaller{}, store, approved.OperationID, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second))
	if !errors.Is(repairErr, ErrStridePI0Unavailable) {
		t.Fatalf("not-applied should require retry authority: %v", repairErr)
	}
	current.setRevoked(true)
	if _, err := carrier2.RetryNotApplied(ctx, current, approved.OperationID, at.Add(5*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("revoked retry accepted: %v", err)
	}
	current.setRevoked(false)
	if retried, err := carrier2.RetryNotApplied(ctx, current, approved.OperationID, at.Add(5*time.Second)); err != nil || retried.Phase != "effect_requested" {
		t.Fatalf("current-authority retry: %+v err=%v", retried, err)
	}

	raw, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 1
	if err := os.WriteFile(path2, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStridePI0FileCarrier(ctx, path2, keys, highWater2); err == nil {
		t.Fatal("tampered carrier accepted")
	}
}

func TestStridePI0FileCarrierAmbiguousReceiptQuarantinesAndFences(t *testing.T) {
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	evidenceKeys := &stridePI0TestEvidenceKeyring{key: StridePI0EffectEvidenceKey{ID: "pi0_evidence_key", Version: 4, Secret: []byte(strings.Repeat("e", 32))}}
	current := &stridePI0TestCurrentAuthority{}
	recovery := &stridePI0TestRecoveryAuthority{allowed: true}
	at := time.Date(2026, 8, 9, 20, 0, 0, 0, time.UTC)
	event := sealStridePI0TestEvent(t, keys, stridePI0TestEvent(at, "event_effect", "effect.applied", "event_parent", "effect", "effect_alpha", 1))
	journal := stridePI0PrepareCarrierJournal(t, keys, current, event, at)
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	carrier, err := OpenStridePI0FileCarrier(ctx, filepath.Join(t.TempDir(), "carrier.json"), keys, highWater)
	if err != nil {
		t.Fatal(err)
	}
	if err := carrier.CreateJournal(ctx, journal); err != nil {
		t.Fatal(err)
	}
	for i, phase := range []string{"effect_requested", "effect_approved"} {
		next, transitionErr := TransitionStridePI0CompoundJournal(ctx, keys, current, journal, phase, nil, "", at.Add(time.Duration(i+1)*time.Second))
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		if err := carrier.CompareAndSwapJournal(ctx, journal, next); err != nil {
			t.Fatal(err)
		}
		journal = next
	}
	receipt := stridePI0TestEffectReceipt(t, evidenceKeys, journal, journal.ExpectedPostimages, "applied", at.Add(3*time.Second))
	receipt.MAC = stridePI0TestDigest("forged_receipt")
	store := &stridePI0TestEventStore{events: map[string]StridePI0LifecycleEvent{}}
	fences := &stridePI0TestFenceInstaller{}
	_, err = RepairStridePI0CarrierOperation(ctx, carrier, evidenceKeys, recovery, &stridePI0TestPostimageReader{actual: journal.ExpectedPostimages}, &stridePI0TestReceiptResolver{receipt: receipt}, fences, store, journal.OperationID, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second))
	if !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("ambiguous receipt not fenced: %v", err)
	}
	quarantined, _, readErr := carrier.ReadOperation(ctx, journal.OperationID)
	if readErr != nil || quarantined.Phase != "quarantined" || quarantined.Reconciliation != "quarantined" || store.appendCalls != 0 {
		t.Fatalf("quarantine state/effects: %+v calls=%d err=%v", quarantined, store.appendCalls, readErr)
	}
	if _, err := RepairStridePI0CarrierOperation(ctx, carrier, evidenceKeys, recovery, &stridePI0TestPostimageReader{actual: journal.ExpectedPostimages}, &stridePI0TestReceiptResolver{receipt: receipt}, fences, store, journal.OperationID, []StridePI0LifecycleEvent{event}, at.Add(5*time.Second)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("quarantine replay escaped fence: %v", err)
	}
}

func TestStridePI0CompoundJournalStateMachineAndRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	evidenceKeys := &stridePI0TestEvidenceKeyring{key: StridePI0EffectEvidenceKey{ID: "pi0_evidence_key", Version: 4, Secret: []byte(strings.Repeat("e", 32))}}
	currentAuthority := &stridePI0TestCurrentAuthority{}
	recoveryAuthority := &stridePI0TestRecoveryAuthority{allowed: true}
	at := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	journal, err := PrepareStridePI0CompoundJournal(ctx, keys, currentAuthority, stridePI0TestJournal(at))
	if err != nil || VerifyStridePI0CompoundJournal(ctx, keys, journal) != nil || journal.Phase != "prepared" {
		t.Fatalf("prepare: phase=%s err=%v", journal.Phase, err)
	}

	if _, err := TransitionStridePI0CompoundJournal(ctx, keys, currentAuthority, journal, "effect_applied", journal.ExpectedPostimages, "applied", at.Add(time.Second)); !errors.Is(err, ErrStridePI0Unavailable) && !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("unsealed effect jump accepted: %v", err)
	}

	for index, step := range []string{"effect_requested", "effect_approved"} {
		journal, err = TransitionStridePI0CompoundJournal(ctx, keys, currentAuthority, journal, step, nil, "", at.Add(time.Duration(index+1)*time.Second))
		if err != nil {
			t.Fatalf("transition %s: %v", step, err)
		}
	}
	receipt := stridePI0TestEffectReceipt(t, evidenceKeys, journal, journal.ExpectedPostimages, "applied", at.Add(3*time.Second))
	journal, err = RecordStridePI0EffectReceipt(ctx, keys, evidenceKeys, currentAuthority, journal, receipt, at.Add(3*time.Second))
	if err != nil || journal.Phase != "effect_applied" || !isHexDigest(journal.EffectReceiptDigest) {
		t.Fatalf("record effect receipt: %+v err=%v", journal, err)
	}
	for index, step := range []string{"events_written", "postimages_verified", "committed"} {
		journal, err = TransitionStridePI0CompoundJournal(ctx, keys, currentAuthority, journal, step, journal.ExpectedPostimages, "applied", at.Add(time.Duration(index+4)*time.Second))
		if err != nil {
			t.Fatalf("post-effect transition %s: %v", step, err)
		}
	}
	if journal.Phase != "committed" || !stridePI0EqualPostimages(journal.ExpectedPostimages, journal.ActualPostimages) {
		t.Fatalf("journal not committed exactly: %+v", journal)
	}

	prepared, err := PrepareStridePI0CompoundJournal(ctx, keys, currentAuthority, stridePI0TestJournal(at))
	if err != nil {
		t.Fatal(err)
	}
	requested, err := TransitionStridePI0CompoundJournal(ctx, keys, currentAuthority, prepared, "effect_requested", nil, "", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := TransitionStridePI0CompoundJournal(ctx, keys, currentAuthority, requested, "effect_approved", nil, "", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	lostReceipt := stridePI0TestEffectReceipt(t, evidenceKeys, approved, approved.ExpectedPostimages, "applied", at.Add(3*time.Second))
	reader := &stridePI0TestPostimageReader{actual: approved.ExpectedPostimages}
	resolver := &stridePI0TestReceiptResolver{receipt: lostReceipt}
	currentAuthority.setRevoked(true)
	revokedCalls := currentAuthority.calls
	if _, err := RecordStridePI0EffectReceipt(ctx, keys, evidenceKeys, currentAuthority, approved, lostReceipt, at.Add(3*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("revoked caller recorded effect: %v", err)
	}
	if currentAuthority.calls != revokedCalls+1 || reader.calls != 0 || resolver.calls != 0 {
		t.Fatal("revoked current authority reached effect recovery dependencies")
	}
	recoveryRequired, err := RecoverStridePI0CompoundJournal(ctx, keys, evidenceKeys, recoveryAuthority, reader, resolver, approved, at.Add(3*time.Second))
	if err != nil || recoveryRequired.Phase != "recovery_required" || recoveryRequired.Reconciliation != "applied" || reader.calls != 1 || resolver.calls != 1 {
		t.Fatalf("lost-response pre-seal: journal=%+v calls=%d err=%v", recoveryRequired, reader.calls, err)
	}
	effectReconciled, err := RecoverStridePI0CompoundJournal(ctx, keys, evidenceKeys, recoveryAuthority, reader, resolver, recoveryRequired, at.Add(4*time.Second))
	if err != nil || effectReconciled.Phase != "effect_reconciled" || effectReconciled.Reconciliation != "applied" {
		t.Fatalf("effect reconciliation: %+v err=%v", effectReconciled, err)
	}

	tampered := recoveryRequired
	tampered.OperationFingerprint = stridePI0TestDigest("changed")
	beforeReaderCalls, beforeReceiptCalls := reader.calls, resolver.calls
	if _, err := RecoverStridePI0CompoundJournal(ctx, keys, evidenceKeys, recoveryAuthority, reader, resolver, tampered, at.Add(5*time.Second)); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("tampered recovery accepted: %v", err)
	}
	if reader.calls != beforeReaderCalls || resolver.calls != beforeReceiptCalls {
		t.Fatal("tampered unsealed journal reached recovery dependencies")
	}

	tamperedReceipt := lostReceipt
	tamperedReceipt.Postimages[0].Digest = stridePI0TestDigest("forged_effect_postimage")
	receiptQuarantine, err := RecoverStridePI0CompoundJournal(ctx, keys, evidenceKeys, recoveryAuthority, &stridePI0TestPostimageReader{actual: approved.ExpectedPostimages}, &stridePI0TestReceiptResolver{receipt: tamperedReceipt}, approved, at.Add(3*time.Second))
	if err != nil || receiptQuarantine.Phase != "quarantined" || receiptQuarantine.Reconciliation != "quarantined" {
		t.Fatalf("tampered effect/event receipt not quarantined: %+v err=%v", receiptQuarantine, err)
	}

	missing := &stridePI0TestPostimageReader{actual: approved.Preimages}
	notAppliedReceipt := stridePI0TestEffectReceipt(t, evidenceKeys, approved, approved.Preimages, "not_applied", at.Add(3*time.Second))
	notApplied, err := RecoverStridePI0CompoundJournal(ctx, keys, evidenceKeys, recoveryAuthority, missing, &stridePI0TestReceiptResolver{receipt: notAppliedReceipt}, approved, at.Add(3*time.Second))
	if err != nil || notApplied.Reconciliation != "not_applied" {
		t.Fatalf("not-applied recovery: %+v err=%v", notApplied, err)
	}
	empty := &stridePI0TestPostimageReader{}
	if _, err := RecoverStridePI0CompoundJournal(ctx, keys, evidenceKeys, recoveryAuthority, empty, resolver, approved, at.Add(3*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("empty authoritative readback accepted: %v", err)
	}

	denied, err := TerminalizeStridePI0CompoundJournal(ctx, keys, recoveryAuthority, prepared, "denied", prepared.Preimages, at.Add(time.Second))
	if err != nil || denied.Phase != "denied" || denied.Reconciliation != "not_applied" {
		t.Fatalf("denied terminal: %+v err=%v", denied, err)
	}
	drifted := append([]StridePI0Postimage(nil), prepared.Preimages...)
	drifted[0].Digest = stridePI0TestDigest("ambiguous")
	quarantined, err := TerminalizeStridePI0CompoundJournal(ctx, keys, recoveryAuthority, prepared, "quarantined", drifted, at.Add(time.Second))
	if err != nil || quarantined.Phase != "quarantined" || quarantined.Reconciliation != "quarantined" {
		t.Fatalf("quarantine terminal: %+v err=%v", quarantined, err)
	}
}

func stridePI0TestMetricManifest(at time.Time) StridePI0MetricDefinitionManifest {
	definitions := make([]StridePI0MetricDefinition, 0, len(stridePI0MetricIDs))
	for _, metricID := range stridePI0MetricIDs {
		spec := stridePI0MetricSpecs[metricID]
		definition := StridePI0MetricDefinition{
			MetricID: metricID, Revision: 1, EligibleEventTypes: append([]string(nil), spec.events...), Numerator: spec.numerator,
			Denominator: spec.denominator, Unit: spec.unit, TimeOrigin: spec.origin, TimeTerminal: spec.terminal,
			WindowDays: append([]int(nil), spec.windows...), UnknownRule: "missing_or_incomplete_is_unknown", SuppressionMinimum: 5,
			Purpose: "founder_product_learning", OwnerRole: "measurement_owner", ReviewerRole: "privacy_reviewer",
		}
		definitions = append(definitions, definition)
	}
	return StridePI0MetricDefinitionManifest{
		ManifestID: "metric_manifest_alpha", Revision: 1, SourceSchemaDigest: stridePI0TestDigest("schema"),
		SourceHighWaterDigest: stridePI0TestDigest("highwater"), MeasurementRelease: stridePI0TestDigest("release"),
		ConsentPolicyRef: stridePI0TestRef("policy", "consent_policy", 1), CohortPolicyRef: stridePI0TestRef("policy", "cohort_policy", 1),
		Definitions: definitions, FrozenAt: at,
	}
}

type stridePI0TestMeasurementResolver struct {
	manifest StridePI0MetricDefinitionManifest
	metric   StridePI0MeasurementArtifactBinding
	fixture  StridePI0MeasurementArtifactBinding
	policy   StridePI0MeasurementArtifactBinding
	err      error
}

func (r *stridePI0TestMeasurementResolver) ResolveStridePI0MetricManifest(_ context.Context, digest string) (StridePI0MetricDefinitionManifest, StridePI0MeasurementArtifactBinding, error) {
	if r.err != nil || r.metric.Digest != digest {
		return StridePI0MetricDefinitionManifest{}, StridePI0MeasurementArtifactBinding{}, errors.New("metric binding unavailable")
	}
	return r.manifest, r.metric, nil
}

func (r *stridePI0TestMeasurementResolver) ResolveStridePI0MeasurementArtifact(_ context.Context, kind, digest string) (StridePI0MeasurementArtifactBinding, error) {
	if r.err != nil {
		return StridePI0MeasurementArtifactBinding{}, r.err
	}
	for _, binding := range []StridePI0MeasurementArtifactBinding{r.fixture, r.policy} {
		if binding.Kind == kind && binding.Digest == digest {
			return binding, nil
		}
	}
	return StridePI0MeasurementArtifactBinding{}, errors.New("measurement binding unavailable")
}

func TestStridePI0MetricManifestAndPriorWorkflowComparison(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	keys := newStridePI0TestKeyring()
	at := time.Date(2026, 8, 9, 14, 0, 0, 0, time.UTC)
	manifest, err := SealStridePI0MetricManifest(ctx, keys, stridePI0TestMetricManifest(at))
	if err != nil || VerifyStridePI0MetricManifest(ctx, keys, manifest) != nil {
		t.Fatalf("metric manifest: %v", err)
	}

	missing := stridePI0TestMetricManifest(at)
	missing.Definitions = missing.Definitions[:len(missing.Definitions)-1]
	if _, err := SealStridePI0MetricManifest(ctx, keys, missing); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("incomplete founder metrics accepted: %v", err)
	}

	wrongTime := stridePI0TestMetricManifest(at)
	for index := range wrongTime.Definitions {
		if wrongTime.Definitions[index].MetricID == "time_to_useful_outcome" {
			wrongTime.Definitions[index].TimeTerminal = "run.completed"
		}
	}
	if _, err := SealStridePI0MetricManifest(ctx, keys, wrongTime); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("wrong useful-outcome definition accepted: %v", err)
	}

	widenedUniverse := stridePI0TestMetricManifest(at)
	widenedUniverse.Definitions[0].EligibleEventTypes = append(widenedUniverse.Definitions[0].EligibleEventTypes, "run.completed")
	sort.Strings(widenedUniverse.Definitions[0].EligibleEventTypes)
	if _, err := SealStridePI0MetricManifest(ctx, keys, widenedUniverse); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("caller-widened metric universe accepted: %v", err)
	}

	tampered := manifest
	tampered.Definitions = append([]StridePI0MetricDefinition(nil), manifest.Definitions...)
	tampered.Definitions[0].Denominator = "actor_drilldown"
	if err := VerifyStridePI0MetricManifest(ctx, keys, tampered); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("tampered metric manifest accepted: %v", err)
	}

	metricDigest, err := stridePI0MetricManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	fixtureDigest, policyDigest := stridePI0TestDigest("fixtures"), stridePI0TestDigest("eligibility")
	metricBinding, err := SealStridePI0MeasurementArtifactBinding(ctx, keys, StridePI0MeasurementArtifactBinding{Kind: "metric_manifest", Digest: metricDigest, Revision: 1, IssuedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	fixtureBinding, err := SealStridePI0MeasurementArtifactBinding(ctx, keys, StridePI0MeasurementArtifactBinding{Kind: "fixture_manifest", Digest: fixtureDigest, Revision: 1, IssuedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	policyBinding, err := SealStridePI0MeasurementArtifactBinding(ctx, keys, StridePI0MeasurementArtifactBinding{Kind: "eligibility_policy", Digest: policyDigest, Revision: 1, IssuedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &stridePI0TestMeasurementResolver{manifest: manifest, metric: metricBinding, fixture: fixtureBinding, policy: policyBinding}
	comparison := StridePI0PriorWorkflowComparison{
		ComparisonID: "comparison_alpha", PriorReleaseDigest: stridePI0TestDigest("prior"), CurrentReleaseDigest: stridePI0TestDigest("current"),
		FixtureManifestDigest: fixtureDigest, MetricManifestDigest: metricDigest, EligibilityPolicyDigest: policyDigest,
		AssignmentMethod: "paired_identical_fixture", MissingDataRule: "unknown_excluded_with_counts", Hypothesis: "non_inferiority",
		SampleSizeRuleDigest: stridePI0TestDigest("sample"), ObservationWindowDays: 30, OwnerRole: "measurement_owner", ReviewerRole: "independent_reviewer", FrozenAt: at,
	}
	resolver.manifest.MeasurementRelease = comparison.CurrentReleaseDigest
	resolver.manifest, err = SealStridePI0MetricManifest(ctx, keys, resolver.manifest)
	if err != nil {
		t.Fatal(err)
	}
	metricDigest, err = stridePI0MetricManifestDigest(resolver.manifest)
	if err != nil {
		t.Fatal(err)
	}
	resolver.metric, err = SealStridePI0MeasurementArtifactBinding(ctx, keys, StridePI0MeasurementArtifactBinding{Kind: "metric_manifest", Digest: metricDigest, Revision: 1, IssuedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	comparison.MetricManifestDigest = metricDigest
	comparison, err = SealStridePI0PriorWorkflowComparison(ctx, keys, resolver, comparison)
	if err != nil || VerifyStridePI0PriorWorkflowComparison(ctx, keys, resolver, comparison) != nil {
		t.Fatalf("comparison manifest: %v", err)
	}
	unresolved := *resolver
	unresolved.err = errors.New("resolver unavailable")
	if err := VerifyStridePI0PriorWorkflowComparison(ctx, keys, &unresolved, comparison); !errors.Is(err, ErrStridePI0Invalid) && !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("unresolved measurement authority admitted: %v", err)
	}
	wrongFixture := *resolver
	wrongFixture.fixture, err = SealStridePI0MeasurementArtifactBinding(ctx, keys, StridePI0MeasurementArtifactBinding{Kind: "fixture_manifest", Digest: stridePI0TestDigest("other_fixture"), Revision: 1, IssuedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyStridePI0PriorWorkflowComparison(ctx, keys, &wrongFixture, comparison); !errors.Is(err, ErrStridePI0Invalid) && !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("wrong fixture binding admitted: %v", err)
	}
	wrongPolicy := *resolver
	wrongPolicy.policy, err = SealStridePI0MeasurementArtifactBinding(ctx, keys, StridePI0MeasurementArtifactBinding{Kind: "eligibility_policy", Digest: stridePI0TestDigest("other_policy"), Revision: 1, IssuedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyStridePI0PriorWorkflowComparison(ctx, keys, &wrongPolicy, comparison); !errors.Is(err, ErrStridePI0Invalid) && !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("wrong policy binding admitted: %v", err)
	}
	wrongMetric := *resolver
	wrongMetric.manifest.Definitions[0].Denominator = "caller_substituted"
	if err := VerifyStridePI0PriorWorkflowComparison(ctx, keys, &wrongMetric, comparison); !errors.Is(err, ErrStridePI0Invalid) && !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("unverified metric manifest admitted: %v", err)
	}
	comparison.CurrentReleaseDigest = comparison.PriorReleaseDigest
	if err := VerifyStridePI0PriorWorkflowComparison(ctx, keys, resolver, comparison); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("same-release comparison accepted: %v", err)
	}
}
