package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func strideNetworkShadowAdmission(t *testing.T, divergent bool) STRIDENetworkShadowAdmission {
	t.Helper()
	fixture := newNetworkAuthorityFixture(t)
	canonical := cloneNetworkProjection(fixture.profile)
	legacy := cloneNetworkProjection(fixture.profile)
	legacy.Header.ID = "legacy_network_profile_candidate"
	legacy.Header.ContentDigest = sha256Hex([]byte("legacy"))
	if divergent {
		canonical.Fields[2].VisibleValue = json.RawMessage(`["hybrid"]`)
		canonical.Fields[2].ValueDigest = sha256Hex(canonical.Fields[2].VisibleValue)
		canonical.FieldsDigest, _ = STRIDEContractDigest(canonical.Fields)
		canonical.Header.ContentDigest = sha256Hex([]byte("divergent"))
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy shadow candidate invalid: %v", err)
	}
	if err := canonical.Validate(); err != nil {
		t.Fatalf("canonical shadow candidate invalid: %v", err)
	}
	return STRIDENetworkShadowAdmission{Legacy: legacy, Canonical: canonical, Publication: fixture.publication, Attestations: []ContributionAttestation{fixture.attestation}}
}

func strideNetworkShadowConfig() STRIDENetworkShadowConfig {
	return STRIDENetworkShadowConfig{Enabled: true, SearchOrganizationID: "org_recruiter", Now: func() time.Time { return time.Date(2026, 8, 8, 18, 2, 0, 0, time.UTC) }, PurgeAuthority: strideNetworkShadowTestPurgeAuthority{}, AuthorityResolver: strideNetworkShadowTestAuthority{}, SearchAuthority: strideNetworkShadowTestSearchAuthority{}, SnapshotKeys: strideNetworkShadowTestKeys(), MinimumSnapshotGeneration: 1, MinimumSnapshotKeyVersion: 1, PurgeReceipts: newStrideNetworkShadowTestPurgeStore(), PurgeExecutor: &strideNetworkShadowTestPurgeExecutor{}, PurgeMaxAttempts: 3}
}

func TestSTRIDENetworkShadowW6HealthCapabilityHeldThroughFinalUse(t *testing.T) {
	config := strideNetworkShadowConfig()
	store := config.PurgeReceipts.(*strideNetworkShadowTestPurgeStore)
	service := NewSTRIDENetworkShadowService(config)
	now := config.Now()
	policyAuthority, keys, policy := w6TestPolicyAuthority(t, now)
	qualification, err := SignW6NetworkQualification(context.Background(), keys, policy, w6QualificationFixture(policy, now, 5, 2))
	if err != nil {
		t.Fatal(err)
	}
	qualification.Profiles[0].PersonID = strideNetworkShadowAdmission(t, false).Canonical.SubjectPersonID
	qualification.Profiles[0].Profile = referenceFromHeader(strideNetworkShadowAdmission(t, false).Canonical.Header)
	qualification.Profiles[0].Publication = referenceFromHeader(strideNetworkShadowAdmission(t, false).Publication.Header)
	qualification, err = SignW6NetworkQualification(context.Background(), keys, policy, qualification)
	if err != nil {
		t.Fatal(err)
	}
	qualificationAuthority := NewW6NetworkQualificationAuthority(keys)
	if err := qualificationAuthority.Install(context.Background(), policy, qualification, now); err != nil {
		t.Fatal(err)
	}
	if err := service.BindCurrentW6Policy(context.Background(), policyAuthority, qualificationAuthority, policy.Revision, "cohort_pilot", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	expectation := W6ShadowHealthExpectation{OrganizationID: "org_recruiter", PolicyRevision: policy.Revision}
	called := false
	done := make(chan struct{})
	if err := service.WithHealthyCurrentW6Shadow(context.Background(), expectation, func(snapshot W6ShadowHealthSnapshot) error {
		called = true
		if snapshot.SnapshotRevision != snapshot.IndexedRevision || !snapshot.PurgeWorkerHealthy {
			t.Fatalf("unhealthy snapshot: %+v", snapshot)
		}
		// Try to create lag while the capability is held. It must wait until the
		// final-use callback releases the read lock.
		go func() { service.mu.Lock(); service.indexedRevision--; service.mu.Unlock(); close(done) }()
		select {
		case <-done:
			t.Fatal("health capability was not held through final use")
		case <-time.After(10 * time.Millisecond):
		}
		return nil
	}); err != nil || !called {
		t.Fatalf("health capability: called=%t err=%v", called, err)
	}
	<-done
	if err := service.WithHealthyCurrentW6Shadow(context.Background(), expectation, func(W6ShadowHealthSnapshot) error { return nil }); !errors.Is(err, ErrSTRIDENetworkShadowLagged) {
		t.Fatalf("lag admitted: %v", err)
	}

	service.mu.Lock()
	service.indexedRevision = service.revision
	service.mu.Unlock()
	service.w6HealthMu.Lock()
	service.mu.Lock()
	for _, record := range service.records {
		personID := record.admission.Canonical.SubjectPersonID
		entry := service.w6QualifiedProfiles[personID]
		entry.AttestationCount++
		service.w6QualifiedProfiles[personID] = entry
		break
	}
	service.mu.Unlock()
	service.w6HealthMu.Unlock()
	if err := service.WithHealthyCurrentW6Shadow(context.Background(), expectation, func(W6ShadowHealthSnapshot) error { return nil }); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
		t.Fatalf("wrong signed attestation count admitted: %v", err)
	}
	service.w6HealthMu.Lock()
	service.mu.Lock()
	for _, record := range service.records {
		personID := record.admission.Canonical.SubjectPersonID
		entry := service.w6QualifiedProfiles[personID]
		entry.AttestationCount--
		service.w6QualifiedProfiles[personID] = entry
		break
	}
	service.mu.Unlock()
	service.w6HealthMu.Unlock()
	service.w6HealthMu.Lock()
	service.mu.Lock()
	var base strideNetworkShadowRecord
	for _, record := range service.records {
		base = record
		break
	}
	extra := strideNetworkShadowRecord{admission: cloneContract(base.admission), comparison: base.comparison}
	extra.admission.Legacy.SubjectPersonID = "person_unqualified"
	extra.admission.Canonical.SubjectPersonID = "person_unqualified"
	extra.admission.Publication.SubjectPersonID = "person_unqualified"
	extra.admission.Legacy.Controller.PrincipalID = "person_unqualified"
	extra.admission.Canonical.Controller.PrincipalID = "person_unqualified"
	extra.admission.Publication.Controller.PrincipalID = "person_unqualified"
	for index := range extra.admission.Attestations {
		extra.admission.Attestations[index].SubjectPersonID = "person_unqualified"
	}
	extra.comparison.SubjectPersonID = "person_unqualified"
	if _, err := validateSTRIDENetworkShadowAdmission(extra.admission); err != nil {
		service.mu.Unlock()
		service.w6HealthMu.Unlock()
		t.Fatalf("extra qualification-negative record was not otherwise valid: %v", err)
	}
	service.records["person_unqualified"] = extra
	service.mu.Unlock()
	service.w6HealthMu.Unlock()
	if err := service.WithHealthyCurrentW6Shadow(context.Background(), expectation, func(W6ShadowHealthSnapshot) error { return nil }); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
		t.Fatalf("extra valid restored shadow record outside qualification manifest admitted: %v", err)
	}
	service.w6HealthMu.Lock()
	service.mu.Lock()
	delete(service.records, "person_unqualified")
	service.mu.Unlock()
	service.w6HealthMu.Unlock()
	queued := STRIDENetworkShadowPurgeWork{Receipt: DerivedPurgeReceipt{Header: STRIDEContractHeader{ID: "purge_pending"}}, State: strideNetworkShadowPurgeQueued, Version: 1}
	store.mu.Lock()
	store.works["purge_pending"] = queued
	store.mu.Unlock()
	if err := service.WithHealthyCurrentW6Shadow(context.Background(), expectation, func(W6ShadowHealthSnapshot) error { return nil }); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
		t.Fatalf("queued purge admitted: %v", err)
	}
	if err := service.WithHealthyCurrentW6Shadow(context.Background(), W6ShadowHealthExpectation{OrganizationID: "org_recruiter", PolicyRevision: policy.Revision + 1}, func(W6ShadowHealthSnapshot) error { return nil }); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
		t.Fatalf("wrong policy admitted: %v", err)
	}
}

type strideNetworkShadowTestSearchAuthority struct {
	with func(STRIDENetworkShadowSearchAuthorityExpectation, func(STRIDENetworkShadowSearchAuthoritySnapshot) error) error
}

func (a strideNetworkShadowTestSearchAuthority) WithCurrentSTRIDENetworkShadowSearchAuthority(_ context.Context, expectation STRIDENetworkShadowSearchAuthorityExpectation, use func(STRIDENetworkShadowSearchAuthoritySnapshot) error) error {
	if a.with != nil {
		return a.with(expectation, use)
	}
	return use(strideNetworkShadowSearchAuthoritySnapshot(expectation))
}

func strideNetworkShadowSearchAuthoritySnapshot(expectation STRIDENetworkShadowSearchAuthorityExpectation) STRIDENetworkShadowSearchAuthoritySnapshot {
	return STRIDENetworkShadowSearchAuthoritySnapshot{
		Generation: 1, SessionHash: expectation.SessionHash, PersonID: "person_recruiter", OrganizationID: expectation.OrganizationID,
		MembershipID: "membership_recruiter", MembershipRevision: 1, ActiveOrganizationSessionID: "active_session_recruiter", ActiveOrganizationSessionRev: 1,
		Grant:               STRIDEReference{ID: "talent_grant_recruiter", ContractType: STRIDEContractTalentSearchGrant, Revision: 1, Digest: sha256Hex([]byte("talent grant recruiter"))},
		GrantOrganizationID: expectation.OrganizationID, GrantSearcherPersonID: "person_recruiter", GrantMembershipID: "membership_recruiter", GrantMembershipRevision: 1, GrantState: "active",
	}
}

type strideNetworkShadowTestPurgeStore struct {
	mu     sync.Mutex
	works  map[string]STRIDENetworkShadowPurgeWork
	states []string
}

func newStrideNetworkShadowTestPurgeStore() *strideNetworkShadowTestPurgeStore {
	return &strideNetworkShadowTestPurgeStore{works: map[string]STRIDENetworkShadowPurgeWork{}}
}

func (s *strideNetworkShadowTestPurgeStore) CreateSTRIDENetworkShadowPurgeWork(_ context.Context, work STRIDENetworkShadowPurgeWork) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, exists := s.works[work.Receipt.Header.ID]
	if exists {
		if !sameSTRIDENetworkShadowPurgeIdentity(prior.Receipt, work.Receipt) {
			return false, errors.New("purge receipt identity conflict")
		}
		return false, nil
	}
	s.works[work.Receipt.Header.ID] = cloneContract(work)
	s.states = append(s.states, work.State)
	return true, nil
}

func (s *strideNetworkShadowTestPurgeStore) GetSTRIDENetworkShadowPurgeWork(_ context.Context, id string) (STRIDENetworkShadowPurgeWork, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	work, exists := s.works[id]
	return cloneContract(work), exists, nil
}

func (s *strideNetworkShadowTestPurgeStore) ListSTRIDENetworkShadowPurgeWork(context.Context) ([]STRIDENetworkShadowPurgeWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]STRIDENetworkShadowPurgeWork, 0, len(s.works))
	for _, work := range s.works {
		result = append(result, cloneContract(work))
	}
	return result, nil
}

func (s *strideNetworkShadowTestPurgeStore) CompareAndSwapSTRIDENetworkShadowPurgeWork(_ context.Context, expected uint64, work STRIDENetworkShadowPurgeWork) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, exists := s.works[work.Receipt.Header.ID]
	if !exists || prior.Version != expected {
		return false, nil
	}
	if !sameSTRIDENetworkShadowPurgeIdentity(prior.Receipt, work.Receipt) {
		return false, errors.New("purge receipt identity conflict")
	}
	s.works[work.Receipt.Header.ID] = cloneContract(work)
	s.states = append(s.states, work.State)
	return true, nil
}

type strideNetworkShadowTestPurgeExecutor struct {
	mu       sync.Mutex
	failures map[string]int
	calls    map[string]int
}

func (e *strideNetworkShadowTestPurgeExecutor) PurgeSTRIDENetworkShadowStore(_ context.Context, receipt DerivedPurgeReceipt, store string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.calls == nil {
		e.calls = map[string]int{}
	}
	key := receipt.Header.ID + ":" + store
	e.calls[key]++
	if e.failures[key] > 0 {
		e.failures[key]--
		return errors.New("bounded injected purge failure")
	}
	return nil
}

type strideNetworkShadowTestKeyManager struct {
	current STRIDENetworkShadowSnapshotKey
	keys    map[string]STRIDENetworkShadowSnapshotKey
}

func strideNetworkShadowTestKeys() strideNetworkShadowTestKeyManager {
	key := STRIDENetworkShadowSnapshotKey{KeyID: "shadow_snapshot", Version: 1, Key: []byte("0123456789abcdef0123456789abcdef")}
	return strideNetworkShadowTestKeyManager{current: key, keys: map[string]STRIDENetworkShadowSnapshotKey{"shadow_snapshot:1": key}}
}

func (m strideNetworkShadowTestKeyManager) CurrentSTRIDENetworkShadowSnapshotKey() (STRIDENetworkShadowSnapshotKey, error) {
	return m.current, nil
}

func (m strideNetworkShadowTestKeyManager) ResolveSTRIDENetworkShadowSnapshotKey(id string, version uint64) (STRIDENetworkShadowSnapshotKey, error) {
	key, ok := m.keys[id+":"+fmt.Sprint(version)]
	if !ok {
		return STRIDENetworkShadowSnapshotKey{}, errors.New("unknown snapshot key")
	}
	return key, nil
}

type strideNetworkShadowTestPurgeAuthority struct{}

func (strideNetworkShadowTestPurgeAuthority) AuthorizeSTRIDEDerivedPurge(receipt DerivedPurgeReceipt) bool {
	return receipt.Validate() == nil
}

type strideNetworkShadowTestAuthority struct {
	resolve    func(STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error)
	revalidate func(STRIDENetworkShadowAuthoritySnapshot) error
	with       func([]STRIDENetworkShadowAuthoritySnapshot, func() error) error
}

func (a strideNetworkShadowTestAuthority) ResolveCurrentSTRIDENetworkShadowAuthority(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
	if a.resolve != nil {
		return a.resolve(expectation)
	}
	attestations := make([]STRIDENetworkShadowAttestationAuthority, len(expectation.Attestations))
	for index, reference := range expectation.Attestations {
		attestations[index] = STRIDENetworkShadowAttestationAuthority{Reference: reference, State: "active"}
	}
	return STRIDENetworkShadowAuthoritySnapshot{Generation: 1, SubjectPersonID: expectation.SubjectPersonID, Publication: expectation.Publication, PublicationState: "published", PublicationVisibility: "signed_in_network", Attestations: attestations}, nil
}

func (a strideNetworkShadowTestAuthority) WithCurrentSTRIDENetworkShadowAuthorities(snapshots []STRIDENetworkShadowAuthoritySnapshot, use func() error) error {
	if a.with != nil {
		return a.with(snapshots, use)
	}
	if a.revalidate != nil {
		for _, snapshot := range snapshots {
			if err := a.revalidate(snapshot); err != nil {
				return err
			}
		}
	}
	return use()
}

func advanceSTRIDENetworkShadowAdmission(admission STRIDENetworkShadowAdmission, at time.Time) STRIDENetworkShadowAdmission {
	next := cloneContract(admission)
	priorAttestation := referenceFromHeader(next.Attestations[0].Header)
	next.Attestations[0].Header.Revision++
	next.Attestations[0].Header.ContentDigest = sha256Hex([]byte("attestation-next"))
	next.Attestations[0].Supersedes = &priorAttestation
	priorPublication := referenceFromHeader(next.Publication.Header)
	next.Publication.Header.Revision++
	next.Publication.Header.ContentDigest = sha256Hex([]byte("publication-next"))
	next.Publication.Supersedes = &priorPublication
	next.Publication.StateChangedAt = at.UTC()
	next.Publication.Attestations = []STRIDEReference{referenceFromHeader(next.Attestations[0].Header)}
	publication := referenceFromHeader(next.Publication.Header)
	for _, profile := range []*NetworkProfileProjection{&next.Legacy, &next.Canonical} {
		profile.Header.Revision++
		profile.Header.ContentDigest = sha256Hex([]byte(profile.Header.ID + "-next"))
		profile.Publication = publication
		profile.PurgeGeneration++
		profile.StateChangedAt = at.UTC()
		for index := range profile.Fields {
			if profile.Fields[index].Claim != nil {
				claim := publication
				profile.Fields[index].Claim = &claim
			}
		}
		profile.FieldsDigest, _ = STRIDEContractDigest(profile.Fields)
	}
	return next
}

func strideNetworkShadowSearch(revision int64) STRIDENetworkShadowSearchRequest {
	return STRIDENetworkShadowSearchRequest{OrganizationID: "org_recruiter", SessionHash: strings.Repeat("a", 64), ActiveOrganizationSessionID: "active_session_recruiter", ExpectedSnapshotRevision: revision, Filters: []NetworkSearchFilter{networkFilter("problem_class", "growth")}}
}

func TestSTRIDENetworkShadowDefaultOffAndEligibleOnly(t *testing.T) {
	disabled := NewSTRIDENetworkShadowService(STRIDENetworkShadowConfig{})
	if _, _, err := disabled.Ingest(STRIDENetworkShadowAdmission{}); !errors.Is(err, ErrSTRIDENetworkShadowDisabled) {
		t.Fatalf("disabled ingest: %v", err)
	}
	if _, err := disabled.Search(STRIDENetworkShadowSearchRequest{}); !errors.Is(err, ErrSTRIDENetworkShadowDisabled) {
		t.Fatalf("disabled search: %v", err)
	}

	service := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
	admission := strideNetworkShadowAdmission(t, false)
	if err := admission.Legacy.Validate(); err != nil {
		t.Fatalf("legacy validate: %v %+v", err, admission.Legacy)
	}
	if err := admission.Canonical.Validate(); err != nil {
		t.Fatalf("canonical validate: %v", err)
	}
	if err := admission.Publication.Validate(); err != nil {
		t.Fatalf("publication validate: %v", err)
	}
	if err := admission.Attestations[0].Validate(); err != nil {
		t.Fatalf("attestation validate: %v", err)
	}
	comparison, updated, err := service.Ingest(admission)
	if err != nil || !updated || !comparison.Equivalent {
		t.Fatalf("eligible ingest: comparison=%+v updated=%t err=%v", comparison, updated, err)
	}
	if _, updated, err := service.Ingest(admission); err != nil || updated {
		t.Fatalf("exact replay updated=%t err=%v", updated, err)
	}

	invalid := admission
	invalid.Canonical.State = "draft"
	invalid.Canonical.Discoverability = "unlisted"
	if _, _, err := service.Ingest(invalid); !errors.Is(err, ErrSTRIDENetworkShadowInvalid) {
		t.Fatalf("draft admitted: %v", err)
	}
	stale := admission
	stale.Attestations[0].Header.Revision++
	stale.Attestations[0].Header.ContentDigest = sha256Hex([]byte("stale-ref"))
	if _, _, err := NewSTRIDENetworkShadowService(strideNetworkShadowConfig()).Ingest(stale); !errors.Is(err, ErrSTRIDENetworkShadowInvalid) {
		t.Fatalf("non-exact attestation admitted: %v", err)
	}
}

func TestSTRIDENetworkShadowExactLinkIsCurrentButNeverSearchable(t *testing.T) {
	admission := strideNetworkShadowAdmission(t, false)
	admission.Legacy.Discoverability, admission.Canonical.Discoverability, admission.Publication.Visibility = "exact_link", "exact_link", "exact_link"
	config := strideNetworkShadowConfig()
	config.AuthorityResolver = strideNetworkShadowTestAuthority{resolve: func(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
		attestations := make([]STRIDENetworkShadowAttestationAuthority, len(expectation.Attestations))
		for index, reference := range expectation.Attestations {
			attestations[index] = STRIDENetworkShadowAttestationAuthority{Reference: reference, State: "active"}
		}
		return STRIDENetworkShadowAuthoritySnapshot{Generation: 1, SubjectPersonID: expectation.SubjectPersonID, Publication: expectation.Publication, PublicationState: "published", PublicationVisibility: "exact_link", Attestations: attestations}, nil
	}}
	service := NewSTRIDENetworkShadowService(config)
	if _, _, err := service.Ingest(admission); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := service.Snapshot()
	results, err := service.Search(strideNetworkShadowSearch(snapshot.Revision))
	if err != nil || len(results) != 0 {
		t.Fatalf("exact-link profile became searchable: results=%+v err=%v", results, err)
	}
	called := false
	if err := service.WithCurrentExactLinkProjection(referenceFromHeader(admission.Canonical.Header), func(result STRIDENetworkShadowSearchResult) error {
		called = true
		if result.Projection != referenceFromHeader(admission.Canonical.Header) || len(result.Fields) == 0 {
			t.Fatalf("wrong exact-link copy: %+v", result)
		}
		return nil
	}); err != nil || !called {
		t.Fatalf("exact-link final copy called=%t err=%v", called, err)
	}
}

func TestSTRIDENetworkShadowSearchRefusesDivergenceLagAndCrossTenant(t *testing.T) {
	diverged := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
	if comparison, _, err := diverged.Ingest(strideNetworkShadowAdmission(t, true)); err != nil || comparison.Equivalent {
		t.Fatalf("divergence comparison=%+v err=%v", comparison, err)
	}
	if _, err := diverged.Search(strideNetworkShadowSearch(1)); !errors.Is(err, ErrSTRIDENetworkShadowDiverged) {
		t.Fatalf("divergent search: %v", err)
	}

	service := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
	if _, _, err := service.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(strideNetworkShadowSearch(2)); !errors.Is(err, ErrSTRIDENetworkShadowLagged) {
		t.Fatalf("future revision search: %v", err)
	}
	cross := strideNetworkShadowSearch(1)
	cross.OrganizationID = "org_other"
	if _, err := service.Search(cross); !errors.Is(err, ErrSTRIDENetworkShadowCrossTenant) {
		t.Fatalf("cross tenant search: %v", err)
	}
	results, err := service.Search(strideNetworkShadowSearch(1))
	if err != nil || len(results) != 1 || results[0].Projection.ID != "network_profile_candidate" {
		t.Fatalf("search results=%+v err=%v", results, err)
	}
}

func TestSTRIDENetworkShadowSearchMatchesCanonicalW1Semantics(t *testing.T) {
	admission := strideNetworkShadowAdmission(t, false)
	cases := []struct {
		name    string
		filters []NetworkSearchFilter
		at      time.Time
	}{
		{name: "decoded scalar", filters: []NetworkSearchFilter{networkFilter("problem_class", "growth")}},
		{name: "decoded array member", filters: []NetworkSearchFilter{networkFilter("work_mode", "async")}},
		{name: "raw visible commitment fallback", filters: []NetworkSearchFilter{networkFilter("problem_class", `"growth"`)}},
		{name: "verification label", filters: []NetworkSearchFilter{networkFilter("verification_label", "organization_verified_opaque")}},
		{name: "freshness current", filters: []NetworkSearchFilter{networkFilter("freshness_bucket", "last_30_days")}},
		{name: "freshness older", filters: []NetworkSearchFilter{networkFilter("freshness_bucket", "older")}, at: admission.Canonical.StateChangedAt.Add(100 * 24 * time.Hour)},
		{name: "nonmatch", filters: []NetworkSearchFilter{networkFilter("problem_class", "scale")}},
		{name: "all filters match", filters: []NetworkSearchFilter{networkFilter("problem_class", "growth"), networkFilter("work_mode", "async")}},
		{name: "all filters require every match", filters: []NetworkSearchFilter{networkFilter("problem_class", "growth"), networkFilter("work_mode", "onsite")}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
			at := test.at
			if at.IsZero() {
				at = admission.Canonical.StateChangedAt.Add(time.Minute)
			}
			service.config.Now = func() time.Time { return at }
			if _, _, err := service.Ingest(admission); err != nil {
				t.Fatal(err)
			}
			canonicalMatch := true
			for _, filter := range test.filters {
				found := false
				for _, field := range admission.Canonical.Fields {
					if networkFieldMatchesFilter(field, admission.Canonical, filter, at) {
						found = true
						break
					}
				}
				canonicalMatch = canonicalMatch && found
			}
			request := strideNetworkShadowSearch(1)
			request.Filters = test.filters
			results, err := service.Search(request)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(results) == 1; got != canonicalMatch {
				t.Fatalf("shadow match=%t canonical W1 match=%t results=%+v", got, canonicalMatch, results)
			}
		})
	}
}

func TestSTRIDENetworkShadowNeverIndexesHiddenCommitments(t *testing.T) {
	admission := strideNetworkShadowAdmission(t, false)
	for _, profile := range []*NetworkProfileProjection{&admission.Legacy, &admission.Canonical} {
		profile.Fields[1].VisibleValue = nil
		profile.FieldsDigest, _ = STRIDEContractDigest(profile.Fields)
		profile.Header.ContentDigest = sha256Hex([]byte(profile.Header.ID + "-hidden"))
	}
	service := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
	if comparison, _, err := service.Ingest(admission); err != nil || !comparison.Equivalent {
		t.Fatalf("hidden valid admission comparison=%+v err=%v", comparison, err)
	}
	for _, key := range []string{
		networkSearchIndexKey("problem_class", admission.Canonical.Fields[1].ValueDigest),
		networkSearchIndexKey("verification_label", sha256Hex([]byte(admission.Canonical.Fields[1].EvidenceLabel))),
	} {
		if service.index[key][admission.Canonical.SubjectPersonID] {
			t.Fatalf("hidden commitment indexed under %q", key)
		}
	}
	request := strideNetworkShadowSearch(1)
	request.Filters = []NetworkSearchFilter{networkFilter("problem_class", `"growth"`)}
	results, err := service.Search(request)
	if err != nil || len(results) != 0 {
		t.Fatalf("hidden commitment searchable results=%+v err=%v", results, err)
	}
	visibleRequest := strideNetworkShadowSearch(1)
	visibleRequest.Filters = []NetworkSearchFilter{networkFilter("work_mode", "async")}
	results, err = service.Search(visibleRequest)
	if err != nil || len(results) != 1 || len(results[0].Fields) != 2 {
		t.Fatalf("visible match did not return exact visible projection: results=%+v err=%v", results, err)
	}
	for _, field := range results[0].Fields {
		if len(field.VisibleValue) == 0 {
			t.Fatalf("visible result leaked hidden commitment: %+v", field)
		}
	}

	hiddenDrift := cloneContract(admission)
	hiddenDrift.Canonical.Fields[1].ValueDigest = sha256Hex([]byte("different hidden commitment"))
	hiddenDrift.Canonical.FieldsDigest, _ = STRIDEContractDigest(hiddenDrift.Canonical.Fields)
	hiddenDrift.Canonical.Header.ContentDigest = sha256Hex([]byte("hidden-only canonical drift"))
	hiddenService := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
	comparison, _, err := hiddenService.Ingest(hiddenDrift)
	if err != nil || !comparison.Equivalent {
		t.Fatalf("hidden-only legacy/canonical drift affected parity: comparison=%+v err=%v", comparison, err)
	}
	if expectation := shadowAuthorityExpectation(hiddenDrift); len(expectation.Attestations) != 0 {
		t.Fatalf("hidden-only evidence entered current authority expectation: %+v", expectation.Attestations)
	}
	visibleRequest.ExpectedSnapshotRevision = 1
	results, err = hiddenService.Search(visibleRequest)
	if err != nil || len(results) != 1 {
		t.Fatalf("hidden evidence authority affected visible match: results=%+v err=%v", results, err)
	}
}

func TestSTRIDENetworkShadowSearchFencesPostIngestAuthorityChanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*STRIDENetworkShadowAuthoritySnapshot)
	}{
		{"publication withdrawn", func(snapshot *STRIDENetworkShadowAuthoritySnapshot) { snapshot.PublicationState = "withdrawn" }},
		{"publication superseded", func(snapshot *STRIDENetworkShadowAuthoritySnapshot) {
			snapshot.Publication.Revision++
			snapshot.Publication.Digest = sha256Hex([]byte("superseding publication"))
		}},
		{"attestation revoked", func(snapshot *STRIDENetworkShadowAuthoritySnapshot) { snapshot.Attestations[0].State = "revoked" }},
		{"attestation superseded", func(snapshot *STRIDENetworkShadowAuthoritySnapshot) {
			snapshot.Attestations[0].Reference.Revision++
			snapshot.Attestations[0].Reference.Digest = sha256Hex([]byte("superseding attestation"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := strideNetworkShadowConfig()
			base := strideNetworkShadowTestAuthority{}
			config.AuthorityResolver = strideNetworkShadowTestAuthority{resolve: func(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
				snapshot, _ := base.ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
				tc.mutate(&snapshot)
				snapshot.ResolvedAt = time.Now().UTC()
				switch tc.name {
				case "publication withdrawn":
					snapshot.ResolvedTerminalReason, snapshot.ResolvedTerminalTarget = "withdrawn", expectation.Publication
				case "publication superseded":
					snapshot.ResolvedTerminalReason, snapshot.ResolvedTerminalTarget = "superseded", expectation.Publication
				case "attestation revoked":
					snapshot.ResolvedTerminalReason, snapshot.ResolvedTerminalTarget = "revoked", expectation.Attestations[0]
				case "attestation superseded":
					snapshot.ResolvedTerminalReason, snapshot.ResolvedTerminalTarget = "superseded", expectation.Attestations[0]
				}
				return snapshot, nil
			}}
			service := NewSTRIDENetworkShadowService(config)
			admission := strideNetworkShadowAdmission(t, false)
			if _, _, err := service.Ingest(admission); err != nil {
				t.Fatal(err)
			}
			if results, err := service.Search(strideNetworkShadowSearch(1)); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) || results != nil {
				t.Fatalf("stale authority search results=%+v err=%v", results, err)
			}
			snapshot, _ := service.Snapshot()
			if snapshot.Revision != 2 || len(snapshot.Records) != 0 || len(snapshot.Purges) != 1 || len(snapshot.PublicationFences) != 1 || len(snapshot.AttestationFences) != 1 {
				t.Fatalf("authority was not synchronously fenced: %+v", snapshot)
			}
		})
	}
}

func TestSTRIDENetworkShadowSearchRevalidationOutagePreservesRows(t *testing.T) {
	config := strideNetworkShadowConfig()
	config.AuthorityResolver = strideNetworkShadowTestAuthority{revalidate: func(STRIDENetworkShadowAuthoritySnapshot) error {
		return errors.New("authority changed after resolve")
	}}
	service := NewSTRIDENetworkShadowService(config)
	if _, _, err := service.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	if results, err := service.Search(strideNetworkShadowSearch(1)); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) || results != nil {
		t.Fatalf("authority race returned results=%+v err=%v", results, err)
	}
	snapshot, _ := service.Snapshot()
	if len(snapshot.Records) != 1 || snapshot.Revision != 1 || len(snapshot.Purges) != 0 {
		t.Fatalf("transient authority outage mutated rows: %+v", snapshot)
	}
}

func TestSTRIDENetworkShadowSearchResolveOutageOrAmbiguityNeverPurges(t *testing.T) {
	for _, resolver := range []strideNetworkShadowTestAuthority{
		{resolve: func(STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
			return STRIDENetworkShadowAuthoritySnapshot{}, errors.New("canonical authority unavailable")
		}},
		{resolve: func(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
			snapshot, _ := (strideNetworkShadowTestAuthority{}).ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
			snapshot.PublicationState = "unknown"
			return snapshot, nil
		}},
	} {
		config := strideNetworkShadowConfig()
		config.AuthorityResolver = resolver
		service := NewSTRIDENetworkShadowService(config)
		if _, _, err := service.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
			t.Fatal(err)
		}
		if results, err := service.Search(strideNetworkShadowSearch(1)); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) || results != nil {
			t.Fatalf("authority outage returned results=%+v err=%v", results, err)
		}
		snapshot, _ := service.Snapshot()
		if len(snapshot.Records) != 1 || snapshot.Revision != 1 || len(snapshot.Purges) != 0 || len(snapshot.PublicationFences) != 0 || len(snapshot.AttestationFences) != 0 {
			t.Fatalf("ambiguous authority caused destructive fence: %+v", snapshot)
		}
	}
}

func TestSTRIDENetworkShadowSearchRequiresCurrentPersonSessionMembershipAndGrant(t *testing.T) {
	for _, mutate := range []func(*STRIDENetworkShadowSearchAuthoritySnapshot){
		func(snapshot *STRIDENetworkShadowSearchAuthoritySnapshot) { snapshot.PersonID = "" },
		func(snapshot *STRIDENetworkShadowSearchAuthoritySnapshot) {
			snapshot.SessionHash = strings.Repeat("b", 64)
		},
		func(snapshot *STRIDENetworkShadowSearchAuthoritySnapshot) { snapshot.MembershipRevision = 0 },
		func(snapshot *STRIDENetworkShadowSearchAuthoritySnapshot) { snapshot.GrantState = "revoked" },
		func(snapshot *STRIDENetworkShadowSearchAuthoritySnapshot) {
			snapshot.GrantSearcherPersonID = "person_other"
		},
		func(snapshot *STRIDENetworkShadowSearchAuthoritySnapshot) {
			snapshot.Grant.ContractType = STRIDEContractNetworkSearchReceipt
		},
	} {
		config := strideNetworkShadowConfig()
		config.SearchAuthority = strideNetworkShadowTestSearchAuthority{with: func(expectation STRIDENetworkShadowSearchAuthorityExpectation, use func(STRIDENetworkShadowSearchAuthoritySnapshot) error) error {
			snapshot := strideNetworkShadowSearchAuthoritySnapshot(expectation)
			mutate(&snapshot)
			return use(snapshot)
		}}
		service := NewSTRIDENetworkShadowService(config)
		if _, _, err := service.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
			t.Fatal(err)
		}
		if results, err := service.Search(strideNetworkShadowSearch(1)); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) || results != nil {
			t.Fatalf("invalid search authority returned results=%+v err=%v", results, err)
		}
		snapshot, _ := service.Snapshot()
		if len(snapshot.Records) != 1 || len(snapshot.Purges) != 0 {
			t.Fatalf("invalid search caller mutated index: %+v", snapshot)
		}
	}
}

func TestSTRIDENetworkShadowSearchPrincipalHeldThroughFinalCopy(t *testing.T) {
	var authorityMu sync.Mutex
	entered := make(chan struct{})
	release := make(chan struct{})
	config := strideNetworkShadowConfig()
	config.SearchAuthority = strideNetworkShadowTestSearchAuthority{with: func(expectation STRIDENetworkShadowSearchAuthorityExpectation, use func(STRIDENetworkShadowSearchAuthoritySnapshot) error) error {
		authorityMu.Lock()
		defer authorityMu.Unlock()
		return use(strideNetworkShadowSearchAuthoritySnapshot(expectation))
	}}
	config.AuthorityResolver = strideNetworkShadowTestAuthority{with: func(_ []STRIDENetworkShadowAuthoritySnapshot, use func() error) error {
		close(entered)
		<-release
		return use()
	}}
	service := NewSTRIDENetworkShadowService(config)
	if _, _, err := service.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		results, err := service.Search(strideNetworkShadowSearch(1))
		if err == nil && len(results) != 1 {
			err = fmt.Errorf("results=%d", len(results))
		}
		done <- err
	}()
	<-entered
	mutated := make(chan struct{})
	go func() {
		authorityMu.Lock()
		authorityMu.Unlock()
		close(mutated)
	}()
	select {
	case <-mutated:
		t.Fatal("search principal changed before final result copy")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-mutated:
	case <-time.After(time.Second):
		t.Fatal("search principal mutation remained blocked")
	}
}

func TestSTRIDENetworkShadowRejectsSameRevisionEquivocation(t *testing.T) {
	for _, authority := range []string{"publication", "attestation"} {
		t.Run(authority, func(t *testing.T) {
			service := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
			admission := strideNetworkShadowAdmission(t, false)
			if _, _, err := service.Ingest(admission); err != nil {
				t.Fatal(err)
			}
			equivocated := cloneContract(admission)
			if authority == "publication" {
				equivocated.Publication.Header.ContentDigest = sha256Hex([]byte("equivocated publication"))
				publication := referenceFromHeader(equivocated.Publication.Header)
				for _, profile := range []*NetworkProfileProjection{&equivocated.Legacy, &equivocated.Canonical} {
					profile.Publication = publication
					profile.StateChangedAt = profile.StateChangedAt.Add(time.Second)
					for index := range profile.Fields {
						if profile.Fields[index].Claim != nil {
							claim := publication
							profile.Fields[index].Claim = &claim
						}
					}
					profile.FieldsDigest, _ = STRIDEContractDigest(profile.Fields)
				}
			} else {
				equivocated.Attestations[0].Header.ContentDigest = sha256Hex([]byte("equivocated attestation"))
				equivocated.Publication.Attestations[0] = referenceFromHeader(equivocated.Attestations[0].Header)
				equivocated.Canonical.StateChangedAt = equivocated.Canonical.StateChangedAt.Add(time.Second)
				equivocated.Legacy.StateChangedAt = equivocated.Legacy.StateChangedAt.Add(time.Second)
			}
			if _, _, err := service.Ingest(equivocated); !errors.Is(err, ErrSTRIDENetworkShadowConflict) {
				t.Fatalf("same-revision %s equivocation admitted: %v", authority, err)
			}
		})
	}
}

func TestSTRIDENetworkShadowSynchronousFencesAndMonotonicReceipts(t *testing.T) {
	for _, reason := range []string{"revalidation", "revoke", "purge"} {
		t.Run(reason, func(t *testing.T) {
			service := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
			admission := strideNetworkShadowAdmission(t, false)
			if _, _, err := service.Ingest(admission); err != nil {
				t.Fatal(err)
			}
			receipt, err := service.fenceResolvedAuthority(admission.Canonical.SubjectPersonID, referenceFromHeader(admission.Publication.Header), reason, time.Date(2026, 8, 8, 20, 0, 0, 0, time.UTC))
			if err != nil || receipt.PurgeGeneration != 1 || receipt.Validate() != nil || len(receipt.Stores) != len(contributionPurgeStores) {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			results, err := service.Search(strideNetworkShadowSearch(2))
			if err != nil || len(results) != 0 {
				t.Fatalf("fenced search results=%+v err=%v", results, err)
			}
			if applied, err := service.ApplyPurge(receipt); err != nil || applied {
				t.Fatalf("purge replay applied=%t err=%v", applied, err)
			}

			if _, _, err := service.Ingest(admission); !errors.Is(err, ErrSTRIDENetworkShadowConflict) {
				t.Fatalf("same fenced authority resurrected: %v", err)
			}
			advanced := advanceSTRIDENetworkShadowAdmission(admission, time.Date(2026, 8, 8, 20, 30, 0, 0, time.UTC))
			if err := advanced.Publication.Validate(); err != nil {
				t.Fatalf("advanced publication invalid: %v", err)
			}
			if err := advanced.Attestations[0].Validate(); err != nil {
				t.Fatalf("advanced attestation invalid: %v", err)
			}
			if err := advanced.Legacy.Validate(); err != nil {
				t.Fatalf("advanced legacy invalid: %v", err)
			}
			if err := advanced.Canonical.Validate(); err != nil {
				t.Fatalf("advanced canonical invalid: %v", err)
			}
			if _, updated, err := service.Ingest(advanced); err != nil || !updated {
				t.Fatalf("new exact authority not admitted: updated=%t err=%v", updated, err)
			}
			second, err := service.fenceResolvedAuthority(advanced.Canonical.SubjectPersonID, referenceFromHeader(advanced.Attestations[0].Header), reason, time.Date(2026, 8, 8, 21, 0, 0, 0, time.UTC))
			if err != nil || second.PurgeGeneration != 2 {
				t.Fatalf("second receipt=%+v err=%v", second, err)
			}
			snapshot, err := service.Snapshot()
			if err != nil || len(snapshot.PublicationFences) != 1 || len(snapshot.AttestationFences) != 1 {
				t.Fatalf("fence snapshot=%+v err=%v", snapshot, err)
			}
			restored, err := RestoreSTRIDENetworkShadowService(strideNetworkShadowConfig(), snapshot)
			if err != nil {
				t.Fatalf("restore fenced snapshot: %v", err)
			}
			if _, _, err := restored.Ingest(advanced); !errors.Is(err, ErrSTRIDENetworkShadowConflict) {
				t.Fatalf("restart resurrected fenced authority: %v", err)
			}
		})
	}
}

func TestSTRIDENetworkShadowExportedPurgeRequiresAuthority(t *testing.T) {
	authorized := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
	admission := strideNetworkShadowAdmission(t, false)
	if _, _, err := authorized.Ingest(admission); err != nil {
		t.Fatal(err)
	}
	receipt, err := authorized.fenceResolvedAuthority(admission.Canonical.SubjectPersonID, referenceFromHeader(admission.Publication.Header), "purge", time.Date(2026, 8, 8, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedConfig := strideNetworkShadowConfig()
	unauthorizedConfig.PurgeAuthority = nil
	unauthorized := NewSTRIDENetworkShadowService(unauthorizedConfig)
	if _, _, err := unauthorized.Ingest(admission); err != nil {
		t.Fatal(err)
	}
	if _, err := unauthorized.ApplyPurge(receipt); !errors.Is(err, ErrSTRIDENetworkShadowInvalid) {
		t.Fatalf("unauthorized purge accepted: %v", err)
	}
}

func TestSTRIDENetworkShadowSnapshotRestartIsDeterministicAndBodyFree(t *testing.T) {
	service := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
	if _, _, err := service.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot()
	if err != nil || !isHexDigest(snapshot.Digest) || snapshot.Revision != snapshot.IndexedRevision {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	encoded, _ := json.Marshal(snapshot)
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"mymind", "agentmind", "sourcebody", "contactchannel", "ranking_score", "\"score\""} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("snapshot leaked %q", forbidden)
		}
	}
	restored, err := RestoreSTRIDENetworkShadowService(strideNetworkShadowConfig(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, _ := restored.Snapshot()
	if rebuilt.Digest != snapshot.Digest {
		t.Fatalf("restart digest=%s want=%s", rebuilt.Digest, snapshot.Digest)
	}
	results, err := restored.Search(strideNetworkShadowSearch(snapshot.Revision))
	if err != nil || len(results) != 1 {
		t.Fatalf("restored results=%+v err=%v", results, err)
	}

	lagged := snapshot
	lagged.IndexedRevision--
	lagged.Digest = shadowSnapshotDigest(lagged)
	if _, err := RestoreSTRIDENetworkShadowService(strideNetworkShadowConfig(), lagged); !errors.Is(err, ErrSTRIDENetworkShadowInvalid) {
		t.Fatalf("lagged restore: %v", err)
	}
	if _, err := RestoreSTRIDENetworkShadowService(STRIDENetworkShadowConfig{Enabled: true, SearchOrganizationID: "org_other"}, snapshot); !errors.Is(err, ErrSTRIDENetworkShadowCrossTenant) {
		t.Fatalf("cross tenant restore: %v", err)
	}
}

func TestSTRIDENetworkShadowConcurrentReplayIsLinearizable(t *testing.T) {
	service := NewSTRIDENetworkShadowService(strideNetworkShadowConfig())
	admission := strideNetworkShadowAdmission(t, false)
	const workers = 32
	var updates atomic.Int64
	var failures atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, updated, err := service.Ingest(admission)
			if err != nil {
				failures.Add(1)
				return
			}
			if updated {
				updates.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 || updates.Load() != 1 {
		t.Fatalf("failures=%d updates=%d", failures.Load(), updates.Load())
	}
	snapshot, _ := service.Snapshot()
	if snapshot.Revision != 1 || snapshot.IndexedRevision != 1 {
		t.Fatalf("snapshot revisions=%d/%d", snapshot.Revision, snapshot.IndexedRevision)
	}
}

func TestSTRIDENetworkShadowSearchAuthorityCapabilityCoversFinalCopy(t *testing.T) {
	var authorityMu sync.RWMutex
	var authorityGeneration uint64 = 1
	writerAttempted := make(chan struct{})
	writerChanged := make(chan struct{})
	copyCompleted := make(chan struct{})
	config := strideNetworkShadowConfig()
	base := strideNetworkShadowTestAuthority{}
	config.AuthorityResolver = strideNetworkShadowTestAuthority{
		resolve: func(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
			authorityMu.RLock()
			defer authorityMu.RUnlock()
			snapshot, _ := base.ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
			snapshot.Generation = authorityGeneration
			return snapshot, nil
		},
		with: func(snapshots []STRIDENetworkShadowAuthoritySnapshot, use func() error) error {
			authorityMu.RLock()
			defer authorityMu.RUnlock()
			for _, snapshot := range snapshots {
				if snapshot.Generation != authorityGeneration {
					return errors.New("authority generation changed")
				}
			}
			if err := use(); err != nil {
				return err
			}
			close(copyCompleted)
			<-writerAttempted
			select {
			case <-writerChanged:
				return errors.New("writer changed authority before final copy capability released")
			default:
				return nil
			}
		},
	}
	service := NewSTRIDENetworkShadowService(config)
	if _, _, err := service.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	go func() {
		<-copyCompleted
		close(writerAttempted)
		authorityMu.Lock()
		authorityGeneration++
		authorityMu.Unlock()
		close(writerChanged)
	}()
	results, err := service.Search(strideNetworkShadowSearch(1))
	if err != nil || len(results) != 1 {
		t.Fatalf("linearized search results=%+v err=%v", results, err)
	}
	select {
	case <-writerChanged:
	case <-time.After(time.Second):
		t.Fatal("authority writer never resumed after final result copy")
	}
}

func TestSTRIDENetworkShadowSnapshotMACRejectsTamperWrongKeyAndStaleGeneration(t *testing.T) {
	config := strideNetworkShadowConfig()
	service := NewSTRIDENetworkShadowService(config)
	admission := strideNetworkShadowAdmission(t, false)
	if _, _, err := service.Ingest(admission); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot()
	if err != nil || snapshot.KeyID != "shadow_snapshot" || snapshot.KeyVersion != 1 || snapshot.Generation != 2 || !isHexDigest(snapshot.Signature) {
		t.Fatalf("authenticated snapshot=%+v err=%v", snapshot, err)
	}

	tampered := cloneContract(snapshot)
	tampered.Records[0].Comparison.LegacyDigest = sha256Hex([]byte("coherent digest tamper"))
	tampered.Digest = shadowSnapshotDigest(tampered)
	if _, err := RestoreSTRIDENetworkShadowService(config, tampered); !errors.Is(err, ErrSTRIDENetworkShadowInvalid) {
		t.Fatalf("coherent digest tamper accepted: %v", err)
	}

	wrongKey := STRIDENetworkShadowSnapshotKey{KeyID: "shadow_snapshot", Version: 1, Key: []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")}
	wrongConfig := config
	wrongConfig.SnapshotKeys = strideNetworkShadowTestKeyManager{current: wrongKey, keys: map[string]STRIDENetworkShadowSnapshotKey{"shadow_snapshot:1": wrongKey}}
	if _, err := RestoreSTRIDENetworkShadowService(wrongConfig, snapshot); !errors.Is(err, ErrSTRIDENetworkShadowInvalid) {
		t.Fatalf("wrong key accepted: %v", err)
	}

	receipt, err := service.fenceResolvedAuthority(admission.Canonical.SubjectPersonID, admission.PublicationRef(), "purge", time.Date(2026, 8, 8, 22, 0, 0, 0, time.UTC))
	if err != nil || receipt.PurgeGeneration != 1 {
		t.Fatalf("purge receipt=%+v err=%v", receipt, err)
	}
	current, err := service.Snapshot()
	if err != nil || current.Generation != 3 {
		t.Fatalf("current snapshot=%+v err=%v", current, err)
	}
	rollbackConfig := config
	rollbackConfig.MinimumSnapshotGeneration = current.Generation
	if _, err := RestoreSTRIDENetworkShadowService(rollbackConfig, snapshot); !errors.Is(err, ErrSTRIDENetworkShadowInvalid) {
		t.Fatalf("stale pre-purge resurrection accepted: %v", err)
	}
}

func TestSTRIDENetworkShadowSnapshotKeyRotationIsVersionFenced(t *testing.T) {
	oldConfig := strideNetworkShadowConfig()
	oldService := NewSTRIDENetworkShadowService(oldConfig)
	if _, _, err := oldService.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	oldSnapshot, err := oldService.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	oldKey, _ := oldConfig.SnapshotKeys.ResolveSTRIDENetworkShadowSnapshotKey("shadow_snapshot", 1)
	newKey := STRIDENetworkShadowSnapshotKey{KeyID: "shadow_snapshot", Version: 2, Key: []byte("abcdef0123456789abcdef0123456789")}
	rotatedKeys := strideNetworkShadowTestKeyManager{current: newKey, keys: map[string]STRIDENetworkShadowSnapshotKey{"shadow_snapshot:1": oldKey, "shadow_snapshot:2": newKey}}
	rotatedConfig := oldConfig
	rotatedConfig.SnapshotKeys = rotatedKeys
	rotatedConfig.MinimumSnapshotKeyVersion = 2
	if _, err := RestoreSTRIDENetworkShadowService(rotatedConfig, oldSnapshot); !errors.Is(err, ErrSTRIDENetworkShadowInvalid) {
		t.Fatalf("retired key version restored: %v", err)
	}

	rotatedService := NewSTRIDENetworkShadowService(rotatedConfig)
	if _, _, err := rotatedService.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	rotatedSnapshot, err := rotatedService.Snapshot()
	if err != nil || rotatedSnapshot.KeyVersion != 2 {
		t.Fatalf("rotated snapshot=%+v err=%v", rotatedSnapshot, err)
	}
	if _, err := RestoreSTRIDENetworkShadowService(rotatedConfig, rotatedSnapshot); err != nil {
		t.Fatalf("current rotated key rejected: %v", err)
	}
}

func TestSTRIDENetworkShadowRestoreReconcilesCurrentRowsAndFences(t *testing.T) {
	baseConfig := strideNetworkShadowConfig()
	service := NewSTRIDENetworkShadowService(baseConfig)
	admission := strideNetworkShadowAdmission(t, false)
	if _, _, err := service.Ingest(admission); err != nil {
		t.Fatal(err)
	}
	rowSnapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	baseAuthority := strideNetworkShadowTestAuthority{}
	staleRowConfig := baseConfig
	staleRowConfig.AuthorityResolver = strideNetworkShadowTestAuthority{resolve: func(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
		current, _ := baseAuthority.ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
		current.PublicationState = "withdrawn"
		return current, nil
	}}
	if _, err := RestoreSTRIDENetworkShadowService(staleRowConfig, rowSnapshot); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
		t.Fatalf("withdrawn restored row accepted: %v", err)
	}

	if _, err := service.fenceResolvedAuthority(admission.Canonical.SubjectPersonID, admission.PublicationRef(), "revoke", time.Date(2026, 8, 8, 23, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	fencedSnapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	staleFenceConfig := baseConfig
	staleFenceConfig.MinimumSnapshotGeneration = fencedSnapshot.Generation
	staleFenceConfig.AuthorityResolver = strideNetworkShadowTestAuthority{resolve: func(expectation STRIDENetworkShadowAuthorityExpectation) (STRIDENetworkShadowAuthoritySnapshot, error) {
		current, _ := baseAuthority.ResolveCurrentSTRIDENetworkShadowAuthority(expectation)
		current.Attestations[0].Reference.Digest = sha256Hex([]byte("same-revision fence equivocation"))
		return current, nil
	}}
	if _, err := RestoreSTRIDENetworkShadowService(staleFenceConfig, fencedSnapshot); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
		t.Fatalf("equivocated fenced authority accepted: %v", err)
	}
}

func TestSTRIDENetworkShadowDurablePurgePartialFailureRetryRestartAndCompletion(t *testing.T) {
	config := strideNetworkShadowConfig()
	store := config.PurgeReceipts.(*strideNetworkShadowTestPurgeStore)
	executor := config.PurgeExecutor.(*strideNetworkShadowTestPurgeExecutor)
	service := NewSTRIDENetworkShadowService(config)
	admission := strideNetworkShadowAdmission(t, false)
	if _, _, err := service.Ingest(admission); err != nil {
		t.Fatal(err)
	}
	receipt, err := service.fenceResolvedAuthority(admission.Canonical.SubjectPersonID, admission.PublicationRef(), "purge", time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Stores) != 13 || len(receipt.Stores) != len(contributionPurgeStores) {
		t.Fatalf("purge stores=%d, want exact 13", len(receipt.Stores))
	}
	work, found, err := store.GetSTRIDENetworkShadowPurgeWork(context.Background(), receipt.Header.ID)
	if err != nil || !found || work.State != strideNetworkShadowPurgeQueued || work.Version != 1 {
		t.Fatalf("durable queued work=%+v found=%t err=%v", work, found, err)
	}
	firstKey := receipt.Header.ID + ":" + contributionPurgeStores[0]
	executor.failures = map[string]int{firstKey: 1}
	failed, processed, err := service.ProcessPurgeWork(context.Background(), time.Date(2026, 8, 9, 1, 1, 0, 0, time.UTC))
	if err == nil || !processed || failed.State != strideNetworkShadowPurgeFailed || failed.Escalated || !isHexDigest(failed.FailureDigest) || failed.Receipt.Stores[0].AttemptCount != 1 {
		t.Fatalf("partial failure work=%+v processed=%t err=%v", failed, processed, err)
	}
	if strings.Contains(failed.FailureDigest, "injected") {
		t.Fatal("failure evidence retained raw error text")
	}

	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := RestoreSTRIDENetworkShadowService(config, snapshot)
	if err != nil {
		t.Fatalf("restart with partial purge: %v", err)
	}
	retried, processed, err := restarted.ProcessPurgeWork(context.Background(), time.Date(2026, 8, 9, 1, 2, 0, 0, time.UTC))
	if err != nil || !processed || retried.State != strideNetworkShadowPurgeQueued || retried.Receipt.Stores[0].State != "completed" || retried.Receipt.Stores[0].AttemptCount != 2 {
		t.Fatalf("restart retry work=%+v processed=%t err=%v", retried, processed, err)
	}
	for index := 1; index < len(contributionPurgeStores); index++ {
		completed, didWork, runErr := restarted.ProcessPurgeWork(context.Background(), time.Date(2026, 8, 9, 1, 2+index, 0, 0, time.UTC))
		if runErr != nil || !didWork {
			t.Fatalf("store %d completion work=%+v did=%t err=%v", index, completed, didWork, runErr)
		}
	}
	final, found, err := store.GetSTRIDENetworkShadowPurgeWork(context.Background(), receipt.Header.ID)
	if err != nil || !found || final.State != strideNetworkShadowPurgeCompleted || final.Receipt.State != "completed" || final.Escalated {
		t.Fatalf("final durable purge=%+v found=%t err=%v", final, found, err)
	}
	for _, result := range final.Receipt.Stores {
		if result.State != "completed" || result.CompletedAt == nil || result.AttemptCount < 1 {
			t.Fatalf("incomplete exact store result: %+v", result)
		}
	}
	if executor.calls[firstKey] != 2 {
		t.Fatalf("first store calls=%d, want exact retry", executor.calls[firstKey])
	}
	store.mu.Lock()
	stateHistory := append([]string(nil), store.states...)
	store.mu.Unlock()
	for _, want := range []string{strideNetworkShadowPurgeQueued, strideNetworkShadowPurgeRunning, strideNetworkShadowPurgeFailed, strideNetworkShadowPurgeCompleted} {
		if !slices.Contains(stateHistory, want) {
			t.Fatalf("durable state history %v missing %q", stateHistory, want)
		}
	}
	finalSnapshot, err := restarted.Snapshot()
	if err != nil || len(finalSnapshot.Purges) != 1 || finalSnapshot.Purges[0].State != "completed" || len(finalSnapshot.PurgeHighWaters) != 1 || finalSnapshot.PurgeHighWaters[0].Generation != 1 || finalSnapshot.Generation <= snapshot.Generation {
		t.Fatalf("completed purge snapshot=%+v err=%v", finalSnapshot, err)
	}
}

func TestSTRIDENetworkShadowDurablePurgeBoundsFailureAndEscalates(t *testing.T) {
	config := strideNetworkShadowConfig()
	store := config.PurgeReceipts.(*strideNetworkShadowTestPurgeStore)
	executor := config.PurgeExecutor.(*strideNetworkShadowTestPurgeExecutor)
	service := NewSTRIDENetworkShadowService(config)
	admission := strideNetworkShadowAdmission(t, false)
	if _, _, err := service.Ingest(admission); err != nil {
		t.Fatal(err)
	}
	receipt, err := service.fenceResolvedAuthority(admission.Canonical.SubjectPersonID, admission.PublicationRef(), "revoke", time.Date(2026, 8, 9, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	key := receipt.Header.ID + ":" + contributionPurgeStores[0]
	executor.failures = map[string]int{key: config.PurgeMaxAttempts}
	for attempt := 1; attempt <= config.PurgeMaxAttempts; attempt++ {
		work, processed, runErr := service.ProcessPurgeWork(context.Background(), time.Date(2026, 8, 9, 2, attempt, 0, 0, time.UTC))
		if runErr == nil || !processed || work.State != strideNetworkShadowPurgeFailed || work.Receipt.Stores[0].AttemptCount != attempt {
			t.Fatalf("attempt %d work=%+v processed=%t err=%v", attempt, work, processed, runErr)
		}
	}
	work, found, err := store.GetSTRIDENetworkShadowPurgeWork(context.Background(), receipt.Header.ID)
	if err != nil || !found || !work.Escalated || work.Receipt.State != "failed_escalated" || work.Receipt.Stores[0].State != "failed_escalated" || work.Receipt.Stores[0].AttemptCount != config.PurgeMaxAttempts || !isHexDigest(work.FailureDigest) || !isHexDigest(work.EscalationDigest) {
		t.Fatalf("bounded escalation=%+v found=%t err=%v", work, found, err)
	}
	if next, processed, err := service.ProcessPurgeWork(context.Background(), time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC)); err != nil || processed || next.Version != 0 {
		t.Fatalf("escalated work retried next=%+v processed=%t err=%v", next, processed, err)
	}
	if executor.calls[key] != config.PurgeMaxAttempts {
		t.Fatalf("executor attempts=%d want=%d", executor.calls[key], config.PurgeMaxAttempts)
	}
}
