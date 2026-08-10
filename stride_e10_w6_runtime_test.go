package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type strideE10W6TestSessions struct {
	mu      sync.Mutex
	current StrideE10W6CurrentSession
	active  bool
}

func (s *strideE10W6TestSessions) WithCurrentStrideE10W6Session(_ context.Context, organizationID, hash string, use func(StrideE10W6CurrentSession) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active || s.current.OrganizationID != organizationID || s.current.SessionHash != hash {
		return ErrSTRIDENetworkShadowAuthority
	}
	return use(s.current)
}

type strideE10W6TestPurgeExecutor struct{}

func (strideE10W6TestPurgeExecutor) PurgeSTRIDENetworkShadowStore(context.Context, DerivedPurgeReceipt, string) error {
	return nil
}

type strideE10W6RuntimeFixture struct {
	config   StrideE10W6RuntimeConfig
	live     *StrideE10ProductLiveRuntime
	sessions *strideE10W6TestSessions
	policy   W6NetworkPolicyRevision
	qual     W6NetworkQualificationReceipt
	binding  StrideE10W6RuntimeBinding
	snapshot STRIDENetworkShadowSnapshot
}

func newStrideE10W6RuntimeFixture(t *testing.T) strideE10W6RuntimeFixture {
	t.Helper()
	networkFixture := newNetworkAuthorityFixture(t)
	live := NewStrideE10ProductLiveRuntime(func() time.Time { return networkFixture.now.Add(2 * time.Minute) })
	live.network = networkFixture.service
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	key := W6ManagedMACKey{ID: "w6_runtime_key", Version: 3, Secret: []byte(strings.Repeat("r", 32))}
	keys, err := newStrideE10W6ManagedKeyring(key, nil)
	if err != nil {
		t.Fatalf("managed keyring: %v", err)
	}
	policyValue := w6TestPolicy(networkFixture.now)
	policyValue.Revision = networkFixture.grant.PolicyRevision
	policy, err := SignW6NetworkPolicy(context.Background(), keys, policyValue)
	if err != nil {
		t.Fatalf("sign policy: %v", err)
	}
	qualificationValue := w6QualificationFixture(policy, networkFixture.now, 5, 2)
	qualificationValue.Profiles[0].PersonID = networkFixture.profile.SubjectPersonID
	qualificationValue.Profiles[0].Profile = referenceFromHeader(networkFixture.profile.Header)
	qualificationValue.Profiles[0].Publication = referenceFromHeader(networkFixture.publication.Header)
	qualification, err := SignW6NetworkQualification(context.Background(), keys, policy, qualificationValue)
	if err != nil {
		t.Fatalf("sign qualification: %v", err)
	}
	qd, _ := STRIDEContractDigest(qualification)
	binding, err := SignStrideE10W6RuntimeBinding(key, StrideE10W6RuntimeBinding{TenantID: networkFixture.grant.OrganizationID, CohortID: "cohort_pilot", PolicyID: policy.PolicyID, PolicyRevision: policy.Revision, QualificationReceiptID: qualification.ReceiptID, QualificationRevision: qualification.Revision, QualificationDigest: qd, Enabled: true, BoundAt: networkFixture.now.Add(-time.Minute), ExpiresAt: networkFixture.now.Add(30 * time.Minute)})
	if err != nil {
		t.Fatalf("sign binding: %v", err)
	}
	config := StrideE10W6RuntimeConfig{PolicyPath: filepath.Join(root, "policy.json"), QualificationPath: filepath.Join(root, "qualification.json"), BindingPath: filepath.Join(root, "binding.json"), ShadowSnapshotPath: filepath.Join(root, "shadow.json"), PurgeStorePath: filepath.Join(root, "purges.json"), Key: key, MinimumKeyVersion: key.Version, PurgeExecutor: strideE10W6TestPurgeExecutor{}, MinimumGeneration: 1, Now: func() time.Time { return networkFixture.now.Add(2 * time.Minute) }}
	store, err := newStrideE10W6FilePurgeStore(config.PurgeStorePath, keys)
	if err != nil {
		t.Fatalf("purge store: %v", err)
	}
	resolver := &strideE10W6LiveAuthorityResolver{network: live.network}
	sessions := &strideE10W6TestSessions{active: true, current: StrideE10W6CurrentSession{SessionHash: sha256Hex([]byte("session_hash_current")), PersonID: networkFixture.grant.SearcherPersonID, OrganizationID: networkFixture.grant.OrganizationID, MembershipID: networkFixture.grant.MembershipID, MembershipRevision: networkFixture.grant.MembershipRevision, ActiveOrganizationSessionID: "active_session_recruiter", ActiveOrganizationSessionRev: 4}}
	shadow := NewSTRIDENetworkShadowService(STRIDENetworkShadowConfig{Enabled: true, SearchOrganizationID: binding.TenantID, Now: config.Now, PurgeAuthority: resolver, AuthorityResolver: resolver, SearchAuthority: &strideE10W6LiveSearchAuthorityResolver{network: live.network, sessions: sessions}, SnapshotKeys: keys, MinimumSnapshotGeneration: 1, MinimumSnapshotKeyVersion: key.Version, PurgeReceipts: store, PurgeExecutor: config.PurgeExecutor, PurgeMaxAttempts: 3})
	policyAuthority := NewW6NetworkPolicyAuthority(keys)
	if err := policyAuthority.Install(context.Background(), policy); err != nil {
		t.Fatalf("install policy: %v", err)
	}
	qualificationAuthority := NewW6NetworkQualificationAuthority(keys)
	if err := qualificationAuthority.Install(context.Background(), policy, qualification, config.Now()); err != nil {
		t.Fatalf("install qualification: %v", err)
	}
	if err := shadow.BindCurrentW6Policy(context.Background(), policyAuthority, qualificationAuthority, policy.Revision, binding.CohortID, config.Now()); err != nil {
		t.Fatalf("bind shadow policy: %v", err)
	}
	admission := strideNetworkShadowAdmission(t, false)
	if _, _, err := shadow.Ingest(admission); err != nil {
		t.Fatalf("ingest shadow admission: %v", err)
	}
	snapshot, err := shadow.Snapshot()
	if err != nil {
		t.Fatalf("snapshot shadow: %v", err)
	}
	for path, value := range map[string]any{config.PolicyPath: policy, config.QualificationPath: qualification, config.BindingPath: binding, config.ShadowSnapshotPath: snapshot} {
		if err := writeStrideE10W6JSON(path, value); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return strideE10W6RuntimeFixture{config: config, live: live, sessions: sessions, policy: policy, qual: qualification, binding: binding, snapshot: snapshot}
}

func TestStrideE10W6ProductionRuntimeRestartDefaultOffAndReadiness(t *testing.T) {
	fixture := newStrideE10W6RuntimeFixture(t)
	runtime, err := InstallStrideE10W6ProductionRuntime(context.Background(), fixture.live, fixture.sessions, fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	ready := runtime.Readiness(context.Background())
	if !ready.Ready || ready.TenantID != fixture.binding.TenantID || ready.CohortID != fixture.binding.CohortID || ready.PolicyRevision != fixture.policy.Revision || ready.QualificationRevision != fixture.qual.Revision || ready.QueryParserProviderEnabled || ready.SemanticRerankerEnabled || !ready.DeterministicParser {
		t.Fatalf("wrong readiness: %+v", ready)
	}
	for feature, enabled := range fixture.live.features {
		if enabled {
			t.Fatalf("install activated feature %q", feature)
		}
	}
	if err := runtime.PersistShadow(); err != nil {
		t.Fatal(err)
	}
	// Restart authenticates all durable artifacts and reconstitutes W3 state.
	restartedLive := NewStrideE10ProductLiveRuntime(fixture.config.Now)
	restartedLive.network = fixture.live.network
	restarted, err := InstallStrideE10W6ProductionRuntime(context.Background(), restartedLive, fixture.sessions, fixture.config)
	if err != nil || !restarted.Readiness(context.Background()).Ready {
		t.Fatalf("restart: runtime=%v err=%v", restarted, err)
	}
}

func TestStrideE10W6InvalidConfigurationDoesNotReplaceCurrentReadinessOrBindLive(t *testing.T) {
	fixture := newStrideE10W6RuntimeFixture(t)
	if _, err := InstallStrideE10W6ProductionRuntime(context.Background(), fixture.live, fixture.sessions, fixture.config); err != nil {
		t.Fatal(err)
	}
	before := strideE10W6RuntimeReadinessSnapshot()
	other := NewStrideE10ProductLiveRuntime(fixture.config.Now)
	invalid := fixture.config
	invalid.Key.Secret = nil
	if _, err := InstallStrideE10W6ProductionRuntime(context.Background(), other, fixture.sessions, invalid); !errors.Is(err, ErrStrideE10W6RuntimeInvalid) {
		t.Fatalf("invalid config: %v", err)
	}
	after := strideE10W6RuntimeReadinessSnapshot()
	if !before.Ready || after != before {
		t.Fatalf("invalid attempt replaced readiness: before=%+v after=%+v", before, after)
	}
	other.network.mu.Lock()
	bound := other.network.w6Policy != nil || other.network.w6Qualification != nil || other.network.w6Shadow != nil
	other.network.mu.Unlock()
	if bound {
		t.Fatal("invalid config bound live authority")
	}
}

func TestStrideE10W6ProductionRuntimeRejectsTamperWrongKeyExpiryLagAndDivergenceWithoutBindingLive(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *strideE10W6RuntimeFixture)
	}{
		{"policy_tamper", func(t *testing.T, f *strideE10W6RuntimeFixture) {
			body, _ := os.ReadFile(f.config.PolicyPath)
			var v W6NetworkPolicyRevision
			_ = json.Unmarshal(body, &v)
			v.Limits.ResultsPerSearch++
			_ = writeStrideE10W6JSON(f.config.PolicyPath, v)
		}},
		{"wrong_key", func(_ *testing.T, f *strideE10W6RuntimeFixture) {
			f.config.Key.Secret = []byte(strings.Repeat("x", 32))
		}},
		{"expired_binding", func(t *testing.T, f *strideE10W6RuntimeFixture) {
			v := f.binding
			v.ExpiresAt = f.config.Now().Add(-time.Minute)
			v, _ = SignStrideE10W6RuntimeBinding(f.config.Key, v)
			_ = writeStrideE10W6JSON(f.config.BindingPath, v)
		}},
		{"lagged_shadow", func(t *testing.T, f *strideE10W6RuntimeFixture) {
			v := f.snapshot
			v.IndexedRevision--
			resignStrideE10W6Snapshot(t, f.config.Key, &v)
			_ = writeStrideE10W6JSON(f.config.ShadowSnapshotPath, v)
		}},
		{"diverged_shadow", func(t *testing.T, f *strideE10W6RuntimeFixture) {
			v := f.snapshot
			v.Records[0].Admission.Canonical.Fields[0].VisibleValue = json.RawMessage(`"Different"`)
			v.Records[0].Admission.Canonical.Fields[0].ValueDigest = sha256Hex(v.Records[0].Admission.Canonical.Fields[0].VisibleValue)
			v.Records[0].Admission.Canonical.FieldsDigest, _ = STRIDEContractDigest(v.Records[0].Admission.Canonical.Fields)
			v.Records[0].Admission.Canonical.Header.ContentDigest = sha256Hex([]byte("coherent divergence"))
			v.Records[0].Comparison, _ = validateSTRIDENetworkShadowAdmission(v.Records[0].Admission)
			resignStrideE10W6Snapshot(t, f.config.Key, &v)
			_ = writeStrideE10W6JSON(f.config.ShadowSnapshotPath, v)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStrideE10W6RuntimeFixture(t)
			test.mutate(t, &fixture)
			if _, err := InstallStrideE10W6ProductionRuntime(context.Background(), fixture.live, fixture.sessions, fixture.config); err == nil {
				t.Fatal("invalid runtime installed")
			}
			fixture.live.network.mu.Lock()
			bound := fixture.live.network.w6Policy != nil || fixture.live.network.w6Qualification != nil || fixture.live.network.w6Shadow != nil
			fixture.live.network.mu.Unlock()
			if bound {
				t.Fatal("failed install partially bound live authority")
			}
		})
	}
}

func resignStrideE10W6Snapshot(t *testing.T, key W6ManagedMACKey, snapshot *STRIDENetworkShadowSnapshot) {
	t.Helper()
	snapshot.Digest = shadowSnapshotDigest(*snapshot)
	var err error
	snapshot.Signature, err = signSTRIDENetworkShadowSnapshot(STRIDENetworkShadowSnapshotKey{KeyID: key.ID, Version: key.Version, Key: key.Secret}, snapshot.Generation, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStrideE10W6RuntimePurgeBacklogAndCurrentAuthorityFailClosed(t *testing.T) {
	t.Run("purge backlog", func(t *testing.T) {
		fixture := newStrideE10W6RuntimeFixture(t)
		keys, keyErr := newStrideE10W6ManagedKeyring(fixture.config.Key, fixture.config.RetainedKeys)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		store, err := newStrideE10W6FilePurgeStore(fixture.config.PurgeStorePath, keys)
		if err != nil {
			t.Fatal(err)
		}
		work := newSTRIDENetworkShadowPurgeWork(strideE10W6QueuedPurgeReceipt(fixture.live.network.now()))
		if _, err := store.CreateSTRIDENetworkShadowPurgeWork(context.Background(), work); err != nil {
			t.Fatal(err)
		}
		if _, err := InstallStrideE10W6ProductionRuntime(context.Background(), fixture.live, fixture.sessions, fixture.config); err == nil {
			t.Fatal("backlogged purge admitted")
		}
	})
	t.Run("withdrawn current publication", func(t *testing.T) {
		fixture := newStrideE10W6RuntimeFixture(t)
		fixture.live.network.mu.Lock()
		publication := fixture.live.network.publications[fixture.snapshot.Records[0].Admission.Publication.Header.ID]
		publication.State = "withdrawn"
		publication.Visibility = "private"
		publication.StateChangedAt = fixture.config.Now()
		fixture.live.network.publications[publication.Header.ID] = publication
		fixture.live.network.mu.Unlock()
		if _, err := InstallStrideE10W6ProductionRuntime(context.Background(), fixture.live, fixture.sessions, fixture.config); err == nil {
			t.Fatal("withdrawn authority restored")
		}
	})
	t.Run("revoked current attestation", func(t *testing.T) {
		fixture := newStrideE10W6RuntimeFixture(t)
		fixture.live.network.mu.Lock()
		attestation := fixture.live.network.attestations[fixture.snapshot.Records[0].Admission.Attestations[0].Header.ID]
		now := fixture.config.Now()
		attestation.State, attestation.RevokedAt = "revoked", &now
		fixture.live.network.attestations[attestation.Header.ID] = attestation
		fixture.live.network.mu.Unlock()
		if _, err := InstallStrideE10W6ProductionRuntime(context.Background(), fixture.live, fixture.sessions, fixture.config); err == nil {
			t.Fatal("revoked attestation authority restored")
		}
	})
}

func strideE10W6QueuedPurgeReceipt(now time.Time) DerivedPurgeReceipt {
	stores := make([]PurgeStoreResult, 0, len(contributionPurgeStores))
	for _, store := range contributionPurgeStores {
		stores = append(stores, PurgeStoreResult{Store: store, State: "queued", AttemptCount: 1})
	}
	return DerivedPurgeReceipt{
		Header:          STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "purge_runtime_pending", Revision: 1, SchemaVersion: 1, ContractType: STRIDEContractDerivedPurgeReceipt, ContentDigest: sha256Hex([]byte("purge runtime pending")), CreatedAt: now},
		SubjectPersonID: "person_candidate", Trigger: STRIDEReference{ContractType: STRIDEContractNetworkProfileProjection, ID: "network_profile_candidate", Revision: 2, Digest: strideTestDigest("5")},
		PurgeGeneration: 1, AffectedFieldsDigest: sha256Hex([]byte("affected fields")), Stores: stores, EligibilityFencedAt: now, RecordedAt: now, State: "queued",
	}
}

func TestStrideE10W6SearchAuthorityHeldThroughFinalCopyAndRevocation(t *testing.T) {
	fixture := newStrideE10W6RuntimeFixture(t)
	resolver := &strideE10W6LiveSearchAuthorityResolver{network: fixture.live.network, sessions: fixture.sessions}
	expectation := STRIDENetworkShadowSearchAuthorityExpectation{OrganizationID: fixture.binding.TenantID, SessionHash: fixture.sessions.current.SessionHash, ActiveOrganizationSessionID: fixture.sessions.current.ActiveOrganizationSessionID}
	wrongSession := expectation
	wrongSession.ActiveOrganizationSessionID = "active_session_other"
	if err := resolver.WithCurrentSTRIDENetworkShadowSearchAuthority(context.Background(), wrongSession, func(STRIDENetworkShadowSearchAuthoritySnapshot) error { return nil }); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
		t.Fatalf("same hash with wrong active session admitted: %v", err)
	}
	done := make(chan struct{})
	err := resolver.WithCurrentSTRIDENetworkShadowSearchAuthority(context.Background(), expectation, func(snapshot STRIDENetworkShadowSearchAuthoritySnapshot) error {
		if snapshot.PersonID != fixture.sessions.current.PersonID || snapshot.Grant.ID == "" {
			t.Fatalf("wrong snapshot: %+v", snapshot)
		}
		go func() {
			fixture.sessions.mu.Lock()
			fixture.sessions.active = false
			fixture.sessions.mu.Unlock()
			close(done)
		}()
		select {
		case <-done:
			t.Fatal("session authority changed before final copy")
		case <-time.After(10 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if err := resolver.WithCurrentSTRIDENetworkShadowSearchAuthority(context.Background(), expectation, func(STRIDENetworkShadowSearchAuthoritySnapshot) error { return nil }); !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
		t.Fatalf("revoked session admitted: %v", err)
	}
}

func TestStrideE10W6SessionSwitchAfterShadowCopyBlockedThroughReceiptWrite(t *testing.T) {
	fixture := newStrideE10W6RuntimeFixture(t)
	runtime, err := InstallStrideE10W6ProductionRuntime(context.Background(), fixture.live, fixture.sessions, fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.live.network.mu.Lock()
	var grant TalentSearchGrant
	for _, current := range fixture.live.network.grants {
		grant = cloneTalentSearchGrant(current)
		break
	}
	fixture.live.network.mu.Unlock()
	proposal, err := ProposeW6NetworkInterpretation(fixture.policy, "problem_class:growth")
	if err != nil {
		t.Fatal(err)
	}
	confirmation := W6NetworkInterpretationConfirmation{ProposalID: proposal.ProposalID, Revision: proposal.Revision, PolicyRevision: fixture.policy.Revision, ProposalDigest: proposal.Digest}
	request := NetworkSearchRequest{GrantRef: referenceFromHeader(grant.Header), SearcherPersonID: grant.SearcherPersonID, OrganizationID: grant.OrganizationID, MembershipID: grant.MembershipID, MembershipRevision: grant.MembershipRevision, SessionHash: fixture.sessions.current.SessionHash, ActiveSessionID: fixture.sessions.current.ActiveOrganizationSessionID, OriginalQueryDigest: proposal.OriginalQueryDigest, StructuredFilters: append([]NetworkSearchFilter(nil), proposal.Filters...), InterpretationConfirmed: true, PolicyRevision: fixture.policy.Revision, CohortID: fixture.binding.CohortID, Interpretation: &proposal, Confirmation: &confirmation, Limit: 1, IdempotencyKeyDigest: sha256Hex([]byte("held-through-receipt")), At: fixture.config.Now()}
	done := make(chan struct{})
	var receipt NetworkSearchReceipt
	err = runtime.shadow.WithCurrentSearchAdmission(context.Background(), STRIDENetworkShadowSearchRequest{OrganizationID: grant.OrganizationID, SessionHash: request.SessionHash, ActiveOrganizationSessionID: request.ActiveSessionID, ExpectedSnapshotRevision: runtime.shadow.revision, Filters: append([]NetworkSearchFilter(nil), request.StructuredFilters...)}, func(results []STRIDENetworkShadowSearchResult) error {
		go func() {
			fixture.sessions.mu.Lock()
			fixture.sessions.current.ActiveOrganizationSessionID = "active_session_switched"
			fixture.sessions.mu.Unlock()
			close(done)
		}()
		select {
		case <-done:
			return errors.New("session switched after shadow copy before receipt write")
		case <-time.After(10 * time.Millisecond):
		}
		var writeErr error
		receipt, _, writeErr = fixture.live.network.searchWithPolicyLocked(request, fixture.policy, true, results, runtime.shadow.revision)
		return writeErr
	})
	if err != nil || receipt.Header.ID == "" {
		t.Fatalf("held search receipt: %+v err=%v", receipt, err)
	}
	<-done
	fixture.live.network.mu.Lock()
	_, persisted := fixture.live.network.searchDisclosures[receipt.Header.ID]
	fixture.live.network.mu.Unlock()
	if !persisted {
		t.Fatal("receipt returned before disclosure persistence")
	}
}

func TestStrideE10W6SearchAuthorityRejectsRevokedMembershipAndGrant(t *testing.T) {
	for _, name := range []string{"membership", "grant", "capability"} {
		t.Run(name, func(t *testing.T) {
			fixture := newStrideE10W6RuntimeFixture(t)
			fixture.live.network.mu.Lock()
			switch name {
			case "membership":
				v := fixture.live.network.membershipAuthorities[fixture.sessions.current.MembershipID]
				v.Active = false
				fixture.live.network.membershipAuthorities[v.MembershipID] = v
			case "grant":
				v := fixture.live.network.grants["talent_grant_recruiter"]
				v.State = "revoked"
				now := fixture.config.Now()
				v.RevokedAt = &now
				fixture.live.network.grants[v.Header.ID] = v
			case "capability":
				v := fixture.live.network.capabilityAuthorities["talent_capability_admin"]
				v.Active = false
				fixture.live.network.capabilityAuthorities[v.ID] = v
			}
			fixture.live.network.mu.Unlock()
			resolver := &strideE10W6LiveSearchAuthorityResolver{network: fixture.live.network, sessions: fixture.sessions}
			err := resolver.WithCurrentSTRIDENetworkShadowSearchAuthority(context.Background(), STRIDENetworkShadowSearchAuthorityExpectation{OrganizationID: fixture.binding.TenantID, SessionHash: fixture.sessions.current.SessionHash, ActiveOrganizationSessionID: fixture.sessions.current.ActiveOrganizationSessionID}, func(STRIDENetworkShadowSearchAuthoritySnapshot) error { return nil })
			if !errors.Is(err, ErrSTRIDENetworkShadowAuthority) {
				t.Fatalf("revoked %s admitted: %v", name, err)
			}
		})
	}
}

func TestStrideE10W6DurablePurgeStoreRestartTamperAndIdempotency(t *testing.T) {
	fixture := newStrideE10W6RuntimeFixture(t)
	keys, err := newStrideE10W6ManagedKeyring(fixture.config.Key, fixture.config.RetainedKeys)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newStrideE10W6FilePurgeStore(fixture.config.PurgeStorePath, keys)
	if err != nil {
		t.Fatal(err)
	}
	work := newSTRIDENetworkShadowPurgeWork(strideE10W6QueuedPurgeReceipt(fixture.config.Now()))
	created, err := store.CreateSTRIDENetworkShadowPurgeWork(context.Background(), work)
	if err != nil || !created {
		t.Fatalf("create: created=%t err=%v", created, err)
	}
	before, _ := os.ReadFile(fixture.config.PurgeStorePath)
	created, err = store.CreateSTRIDENetworkShadowPurgeWork(context.Background(), work)
	after, _ := os.ReadFile(fixture.config.PurgeStorePath)
	if err != nil || created || string(before) != string(after) {
		t.Fatalf("idempotent retry mutated store: created=%t err=%v", created, err)
	}
	restarted, err := newStrideE10W6FilePurgeStore(fixture.config.PurgeStorePath, keys)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := restarted.GetSTRIDENetworkShadowPurgeWork(context.Background(), work.Receipt.Header.ID)
	if err != nil || !found || got.Version != work.Version {
		t.Fatalf("restart: found=%t got=%+v err=%v", found, got, err)
	}
	var envelope map[string]any
	if json.Unmarshal(after, &envelope) != nil {
		t.Fatal("decode")
	}
	envelope["generation"] = envelope["generation"].(float64) + 1
	tampered, _ := json.Marshal(envelope)
	if err := os.WriteFile(fixture.config.PurgeStorePath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newStrideE10W6FilePurgeStore(fixture.config.PurgeStorePath, keys); err == nil {
		t.Fatal("tampered purge store admitted")
	}
}
