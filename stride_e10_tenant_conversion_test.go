package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type strideE10TenantTestGate struct{ enabled atomic.Bool }

func (g *strideE10TenantTestGate) Enabled() bool { return g != nil && g.enabled.Load() }

type strideE10TenantTestResolver struct {
	mu       sync.RWMutex
	snapshot StrideE10TenantAuthoritySnapshot
	err      error
	calls    atomic.Int64
}

func (r *strideE10TenantTestResolver) WithCurrentTenantAuthority(_ context.Context, _ StrideE10TenantSurface, _ string, use func(StrideE10TenantAuthoritySnapshot) error) error {
	r.calls.Add(1)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.err != nil {
		return r.err
	}
	return use(r.snapshot)
}

func (r *strideE10TenantTestResolver) set(snapshot StrideE10TenantAuthoritySnapshot, err error) {
	r.mu.Lock()
	r.snapshot, r.err = snapshot, err
	r.mu.Unlock()
}

type strideE10TenantTestSink struct {
	mu       sync.Mutex
	receipts []StrideE10TenantDiscrepancyReceipt
	err      error
}

type strideE10TenantTestLegacyIDs struct {
	mu      sync.RWMutex
	persons map[string]string
}

func (m *strideE10TenantTestLegacyIDs) WithMappedLegacyPerson(_ context.Context, digest string, use func(string) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	personID := m.persons[digest]
	if !strideIdentifier(personID) || use == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	return use(personID)
}

func (s *strideE10TenantTestSink) RecordStrideE10TenantDiscrepancy(_ context.Context, receipt StrideE10TenantDiscrepancyReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.receipts = append(s.receipts, receipt)
	return nil
}

func (s *strideE10TenantTestSink) all() []StrideE10TenantDiscrepancyReceipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StrideE10TenantDiscrepancyReceipt(nil), s.receipts...)
}

func strideE10TenantTestSnapshot(now time.Time) StrideE10TenantAuthoritySnapshot {
	const sessionHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	organization := Organization{
		Header: STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "org-one", Revision: 7, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractOrganization, ContentDigest: strings.Repeat("a", 64), CreatedAt: now.Add(-2 * time.Hour)},
		Name:   "Org One", Slug: "org-one", Status: "active", Discoverability: "private", CreatorPersonID: "person-one", PolicyRevision: 1,
		CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	membership := OrganizationMembership{
		Header: STRIDEContractHeader{
			TenantID: "org-one", ID: "membership-one", Revision: 4,
			SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractOrganizationMembership,
			ContentDigest: strings.Repeat("b", 64), CreatedAt: now.Add(-time.Hour),
		},
		PersonID: "person-one", OrganizationID: "org-one", Role: "member", Status: "active", GrantedAt: now.Add(-time.Hour),
	}
	activeSession := ActiveOrganizationSession{
		Header: STRIDEContractHeader{
			TenantID: STRIDEGlobalPersonTenant, ID: "active-session-one", Revision: 9,
			SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractActiveOrganizationSession,
			ContentDigest: strings.Repeat("c", 64), CreatedAt: now.Add(-time.Hour),
		},
		SessionSubjectDigest: sessionHash, PersonID: "person-one", OrganizationID: "org-one",
		MembershipID: "membership-one", MembershipRevision: 4, SessionRevision: 9,
		Status: "active", BoundAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
	return StrideE10TenantAuthoritySnapshot{
		SessionHash: sessionHash,
		Session: sessionRecord{
			Email: "legacy@example.com", Expires: now.Add(time.Hour), PersonID: "person-one",
			ActiveOrganizationID: "org-one", OrganizationMembershipID: "membership-one",
			OrganizationMembershipRev: 4, ActiveOrganizationSessionRev: 9,
		},
		Person:       PersonPrincipal{Header: STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "person-one", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractPersonPrincipal, ContentDigest: strings.Repeat("e", 64), CreatedAt: now.Add(-2 * time.Hour)}, AccountSubjectDigest: strings.Repeat("d", 64), Status: "active", RecoveryRevision: 1, CustodyRevision: 1},
		Organization: organization,
		Membership:   membership, ActiveSession: activeSession,
		Legacy:     StrideE10LegacyPrincipalProjection{TenantID: "org-one", AccountSubjectDigest: strings.Repeat("d", 64)},
		Generation: 12,
	}
}

func TestStrideE10TenantZeroOrganizationIsClosedPersonOnlyCapability(t *testing.T) {
	now := time.Now().UTC()
	converter, gate, resolver, _ := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	gate.enabled.Store(true)
	snapshot := strideE10TenantTestSnapshot(now)
	snapshot.Session.ActiveOrganizationID = ""
	snapshot.Session.OrganizationMembershipID = ""
	snapshot.Session.OrganizationMembershipRev = 0
	snapshot.Session.ActiveOrganizationSessionRev = 0
	snapshot.Organization = Organization{}
	snapshot.Membership = OrganizationMembership{}
	snapshot.ActiveSession = ActiveOrganizationSession{}
	snapshot.Legacy.TenantID = STRIDEGlobalPersonTenant
	resolver.set(snapshot, nil)
	for _, surface := range []StrideE10TenantSurface{StrideE10TenantSurfaceAuthSession, StrideE10TenantSurfaceHTTP} {
		resolution, err := converter.Resolve(context.Background(), surface, snapshot.SessionHash)
		if err != nil || resolution.Capability == nil {
			t.Fatalf("surface=%s zero-org resolve err=%v", surface, err)
		}
		principal := resolution.Capability.Principal
		if principal.TenantID != STRIDEGlobalPersonTenant || principal.PersonID != snapshot.Person.Header.ID || principal.ActiveOrganizationID != "" || principal.OrganizationMembershipID != "" || principal.OrganizationMembershipRev != 0 || principal.ActiveOrganizationSessionRev != 0 {
			t.Fatalf("surface=%s fabricated organization authority: %+v", surface, principal)
		}
	}
	for _, surface := range []StrideE10TenantSurface{StrideE10TenantSurfaceChat, StrideE10TenantSurfaceRoomAdmission, StrideE10TenantSurfaceBoard, StrideE10TenantSurfaceScout, StrideE10TenantSurfaceBrain, StrideE10TenantSurfaceMarketplace, StrideE10TenantSurfaceWorkQueue, StrideE10TenantSurfaceCache, StrideE10TenantSurfaceWorker, StrideE10TenantSurfaceWebSocket} {
		if _, err := converter.Resolve(context.Background(), surface, snapshot.SessionHash); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
			t.Fatalf("zero-org surface %s authorized: %v", surface, err)
		}
	}
	partial := snapshot
	partial.Session.OrganizationMembershipID = "membership-forged"
	resolver.set(partial, nil)
	if _, err := converter.Resolve(context.Background(), StrideE10TenantSurfaceHTTP, snapshot.SessionHash); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("partial zero-org binding authorized: %v", err)
	}
	inactive := snapshot
	inactive.Person.Status = "revoked"
	resolver.set(inactive, nil)
	if _, err := converter.Resolve(context.Background(), StrideE10TenantSurfaceHTTP, snapshot.SessionHash); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("inactive zero-org person authorized: %v", err)
	}
}

func strideE10TenantTestConverter(now time.Time, enabled bool, mode StrideE10TenantConversionMode) (*StrideE10TenantConverter, *strideE10TenantTestGate, *strideE10TenantTestResolver, *strideE10TenantTestSink) {
	gate := &strideE10TenantTestGate{}
	gate.enabled.Store(enabled)
	resolver := &strideE10TenantTestResolver{snapshot: strideE10TenantTestSnapshot(now)}
	sink := &strideE10TenantTestSink{}
	legacyIDs := &strideE10TenantTestLegacyIDs{persons: map[string]string{strings.Repeat("d", 64): "person-one", strings.Repeat("e", 64): "person-attacker"}}
	converter := NewStrideE10TenantConverter(gate, resolver, sink, legacyIDs, StrideE10TenantReceiptKey{
		ID: "tenant-receipt-test", Version: 1, Secret: []byte("tenant-receipt-test-key-32-bytes-minimum"),
	}, mode)
	converter.now = func() time.Time { return now }
	return converter, gate, resolver, sink
}

func TestStrideE10TenantConversionDefaultOffAndClosedCoverage(t *testing.T) {
	now := time.Now().UTC()
	converter, _, resolver, sink := strideE10TenantTestConverter(now, false, StrideE10TenantConversionShadow)
	_, err := converter.Resolve(context.Background(), StrideE10TenantSurfaceProductContext, strings.Repeat("a", 64))
	if !errors.Is(err, ErrStrideE10TenantConversionDisabled) || resolver.calls.Load() != 0 || len(sink.all()) != 0 {
		t.Fatalf("default-off resolve err=%v calls=%d receipts=%d", err, resolver.calls.Load(), len(sink.all()))
	}

	want := []StrideE10TenantSurface{
		StrideE10TenantSurfaceAuthSession, StrideE10TenantSurfaceHTTP, StrideE10TenantSurfaceWebSocket,
		StrideE10TenantSurfaceChat, StrideE10TenantSurfaceDrive, StrideE10TenantSurfaceProductContext,
		StrideE10TenantSurfacePushDelivery, StrideE10TenantSurfaceNotification, StrideE10TenantSurfaceRoomAdmission,
		StrideE10TenantSurfaceArtifactACL, StrideE10TenantSurfaceBoard, StrideE10TenantSurfaceBrain,
		StrideE10TenantSurfaceScout, StrideE10TenantSurfaceMarketplace, StrideE10TenantSurfaceWorkQueue,
		StrideE10TenantSurfaceCache, StrideE10TenantSurfaceWorker,
	}
	got := StrideE10TenantSurfaceInventory()
	if len(got) != len(want) {
		t.Fatalf("surface inventory=%v", got)
	}
	seen := map[StrideE10TenantSurface]bool{}
	for i := range got {
		if got[i] != want[i] || seen[got[i]] {
			t.Fatalf("surface inventory is incomplete/unstable: %v", got)
		}
		seen[got[i]] = true
	}
	coverage := StrideE10TenantSurfaceCoverageInventory()
	if len(coverage) != len(want) {
		t.Fatalf("coverage inventory=%v", coverage)
	}
	active := map[StrideE10TenantSurface]bool{
		StrideE10TenantSurfaceDrive: true, StrideE10TenantSurfacePushDelivery: true,
		StrideE10TenantSurfaceNotification: true, StrideE10TenantSurfaceArtifactACL: true,
		StrideE10TenantSurfaceAuthSession: true, StrideE10TenantSurfaceHTTP: true,
	}
	for i, entry := range coverage {
		if entry.Surface != want[i] || len(entry.LegacySingletons) == 0 || !oneOf(string(entry.HookStatus), string(StrideE10TenantHookPending), string(StrideE10TenantHookActive)) {
			t.Fatalf("missing legacy singleton coverage: %+v", entry)
		}
		wantStatus := StrideE10TenantHookPending
		if active[entry.Surface] {
			wantStatus = StrideE10TenantHookActive
		}
		if entry.HookStatus != wantStatus {
			t.Fatalf("frozen hook status drift for %q: got=%q want=%q", entry.Surface, entry.HookStatus, wantStatus)
		}
	}
	adapters := StrideE10TenantSurfaceAdapters()
	if len(adapters) != len(want) {
		t.Fatalf("executable adapter inventory=%v", adapters)
	}
	for i, adapter := range adapters {
		if adapter.Surface() != want[i] {
			t.Fatalf("adapter %d surface=%q want=%q", i, adapter.Surface(), want[i])
		}
	}
	enabledConverter, _, enabledResolver, enabledSink := strideE10TenantTestConverter(now, true, StrideE10TenantConversionShadow)
	for _, adapter := range adapters {
		resolution, err := adapter.Resolve(context.Background(), enabledConverter, strings.Repeat("a", 64))
		if err != nil || resolution.Capability != nil || resolution.Observation.Surface != adapter.Surface() {
			t.Fatalf("adapter %q failed closed shadow execution: resolution=%+v err=%v", adapter.Surface(), resolution, err)
		}
	}
	if enabledResolver.calls.Load() != int64(len(adapters)) || len(enabledSink.all()) != len(adapters) {
		t.Fatalf("adapters did not exercise conversion: calls=%d receipts=%d", enabledResolver.calls.Load(), len(enabledSink.all()))
	}
	if _, err := converter.Resolve(context.Background(), "future_unreviewed_surface", strings.Repeat("a", 64)); !errors.Is(err, ErrStrideE10TenantConversionDisabled) {
		t.Fatalf("disabled gate must precede surface probing: %v", err)
	}
}

func TestStrideE10TenantFrozenAndExecutableCoverageCannotDrift(t *testing.T) {
	frozen := map[StrideE10TenantSurface]StrideE10TenantHookStatus{}
	for _, entry := range StrideE10TenantSurfaceCoverageInventory() {
		if _, duplicate := frozen[entry.Surface]; duplicate {
			t.Fatalf("duplicate frozen tenant surface %q", entry.Surface)
		}
		frozen[entry.Surface] = entry.HookStatus
	}
	runtime := map[StrideE10TenantSurface]StrideE10TenantHookStatus{}
	for _, entry := range StrideE10TenantRuntimeHookCoverage() {
		if _, duplicate := runtime[entry.Surface]; duplicate {
			t.Fatalf("duplicate executable tenant surface %q", entry.Surface)
		}
		if frozen[entry.Surface] == "" {
			t.Fatalf("executable hook has no closed frozen surface: %q", entry.Surface)
		}
		runtime[entry.Surface] = entry.HookStatus
	}
	for surface, status := range frozen {
		if status == StrideE10TenantHookActive && runtime[surface] != StrideE10TenantHookActive {
			t.Fatalf("frozen surface %q overclaims active without executable proof", surface)
		}
		if runtime[surface] == StrideE10TenantHookActive && status != StrideE10TenantHookActive {
			t.Fatalf("executable surface %q was not reconciled into frozen inventory", surface)
		}
	}
}

func TestStrideE10TenantConversionUsesOnlyCanonicalSessionMembership(t *testing.T) {
	now := time.Now().UTC()
	converter, _, resolver, sink := strideE10TenantTestConverter(now, true, StrideE10TenantConversionShadow)
	attackerSnapshot := strideE10TenantTestSnapshot(now)
	attackerSnapshot.Legacy = StrideE10LegacyPrincipalProjection{TenantID: "attacker-tenant", AccountSubjectDigest: strings.Repeat("e", 64)}
	resolver.set(attackerSnapshot, nil)
	resolution, err := converter.Resolve(context.Background(), StrideE10TenantSurfaceProductContext, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Capability != nil {
		t.Fatal("shadow comparison must never return a usable principal capability")
	}
	receipts := sink.all()
	if len(receipts) != 1 || receipts[0].Matches || strings.Join(receipts[0].MismatchCodes, ",") != "legacy_principal_mismatch,legacy_tenant_mismatch" || receipts[0].Validate() != nil {
		t.Fatalf("receipt=%+v", receipts)
	}
	raw, _ := json.Marshal(receipts[0])
	for _, forbidden := range []string{"attacker@example.com", "legacy@example.com", "attacker-tenant", "person-one", "org-one"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("body-free receipt leaked %q: %s", forbidden, raw)
		}
	}
	for _, plain := range []string{sha256Hex([]byte("attacker@example.com")), sha256Hex([]byte("attacker-tenant")), sha256Hex([]byte("person-one")), sha256Hex([]byte("org-one"))} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("receipt used dictionary-reversible plain digest: %s", raw)
		}
	}
	if err := converter.WithCurrentPrincipal(context.Background(), resolution.Capability, func(StrideE10TenantPrincipal) error { return nil }); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("shadow observation became authorizing: %v", err)
	}

	cutover, _, cutoverResolver, cutoverSink := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	cutoverResolver.set(attackerSnapshot, nil)
	if _, err := cutover.Resolve(context.Background(), StrideE10TenantSurfaceProductContext, strings.Repeat("a", 64)); !errors.Is(err, ErrStrideE10TenantAuthorityStale) || len(cutoverSink.all()) != 1 {
		t.Fatalf("mismatched parity entered cutover: err=%v receipts=%v", err, cutoverSink.all())
	}
}

func TestStrideE10TenantConversionFailsClosedOnStaleRevokedAndCrossOrganization(t *testing.T) {
	now := time.Now().UTC()
	base := strideE10TenantTestSnapshot(now)
	cases := []struct {
		name   string
		mutate func(*StrideE10TenantAuthoritySnapshot)
	}{
		{"missing person", func(s *StrideE10TenantAuthoritySnapshot) { s.Session.PersonID = "" }},
		{"expired session", func(s *StrideE10TenantAuthoritySnapshot) { s.Session.Expires = now }},
		{"stale membership revision", func(s *StrideE10TenantAuthoritySnapshot) { s.Membership.Header.Revision++ }},
		{"cross organization", func(s *StrideE10TenantAuthoritySnapshot) {
			s.Membership.OrganizationID, s.Membership.Header.TenantID = "org-two", "org-two"
		}},
		{"wrong person", func(s *StrideE10TenantAuthoritySnapshot) { s.Membership.PersonID = "person-two" }},
		{"zero generation", func(s *StrideE10TenantAuthoritySnapshot) { s.Generation = 0 }},
		{"revoked membership", func(s *StrideE10TenantAuthoritySnapshot) {
			ended := now.Add(-time.Minute)
			s.Membership.Status, s.Membership.EndedAt = "revoked", &ended
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			converter, _, resolver, sink := strideE10TenantTestConverter(now, true, StrideE10TenantConversionShadow)
			snapshot := base
			tc.mutate(&snapshot)
			resolver.set(snapshot, nil)
			_, err := converter.Resolve(context.Background(), StrideE10TenantSurfaceAuthSession, base.SessionHash)
			if !errors.Is(err, ErrStrideE10TenantAuthorityStale) || len(sink.all()) != 0 {
				t.Fatalf("err=%v receipts=%v", err, sink.all())
			}
		})
	}
}

func TestStrideE10TenantConversionSessionRaceAndRestart(t *testing.T) {
	now := time.Now().UTC()
	converter, _, resolver, sink := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	resolution, err := converter.Resolve(context.Background(), StrideE10TenantSurfaceRoomAdmission, strings.Repeat("a", 64))
	if err != nil || resolution.Capability == nil || len(sink.all()) != 1 || !sink.all()[0].Matches {
		t.Fatalf("initial capability err=%v receipts=%+v", err, sink.all())
	}

	switched := strideE10TenantTestSnapshot(now)
	switched.Session.ActiveOrganizationID = "org-two"
	switched.Session.OrganizationMembershipID = "membership-two"
	switched.Session.OrganizationMembershipRev = 1
	switched.Session.ActiveOrganizationSessionRev++
	switched.Membership.Header.TenantID = "org-two"
	switched.Membership.Header.ID = "membership-two"
	switched.Membership.Header.Revision = 1
	switched.Membership.OrganizationID = "org-two"
	switched.ActiveSession.OrganizationID = "org-two"
	switched.ActiveSession.MembershipID = "membership-two"
	switched.ActiveSession.MembershipRevision = 1
	switched.ActiveSession.SessionRevision++
	switched.ActiveSession.Header.Revision++
	switched.Generation++
	resolver.set(switched, nil)
	if err := converter.WithCurrentPrincipal(context.Background(), resolution.Capability, func(StrideE10TenantPrincipal) error { return nil }); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("session race revalidation=%v", err)
	}

	resolver.set(strideE10TenantTestSnapshot(now), nil)
	restarted := NewStrideE10TenantConverter(converter.gate, resolver, sink, converter.legacyIDs, converter.receiptKey, StrideE10TenantConversionCutover)
	restarted.now = func() time.Time { return now }
	restartedLease, err := restarted.Resolve(context.Background(), StrideE10TenantSurfaceRoomAdmission, strings.Repeat("a", 64))
	if err != nil || restartedLease.Capability == nil || restartedLease.Capability.Principal != resolution.Capability.Principal {
		t.Fatalf("restart lease=%+v err=%v", restartedLease, err)
	}
	if receipts := sink.all(); len(receipts) != 2 || receipts[0].ReceiptID != receipts[1].ReceiptID {
		t.Fatalf("restart receipts are not deterministic: %+v", receipts)
	}
}

func TestStrideE10TenantConversionConcurrentRevalidationFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	converter, _, resolver, _ := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	resolution, err := converter.Resolve(context.Background(), StrideE10TenantSurfaceArtifactACL, strings.Repeat("a", 64))
	if err != nil || resolution.Capability == nil {
		t.Fatal(err)
	}
	valid := strideE10TenantTestSnapshot(now)
	stale := valid
	stale.Session.OrganizationMembershipRev++

	var staleResults atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if index%2 == 0 {
				resolver.set(stale, nil)
			} else {
				resolver.set(valid, nil)
			}
			if errors.Is(converter.WithCurrentPrincipal(context.Background(), resolution.Capability, func(StrideE10TenantPrincipal) error { return nil }), ErrStrideE10TenantAuthorityStale) {
				staleResults.Add(1)
			}
		}(i)
	}
	wg.Wait()
	resolver.set(stale, nil)
	if errors.Is(converter.WithCurrentPrincipal(context.Background(), resolution.Capability, func(StrideE10TenantPrincipal) error { return nil }), ErrStrideE10TenantAuthorityStale) {
		staleResults.Add(1)
	}
	if staleResults.Load() == 0 {
		t.Fatal("concurrent stale authority was never fenced")
	}
}

func TestStrideE10TenantRuntimeValvePreservesOffAndShadowButLinearizesCutover(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	sessionHash := strings.Repeat("a", 64)

	off, _, _, _ := strideE10TenantTestConverter(now, false, StrideE10TenantConversionCutover)
	restore := InstallStrideE10TenantRuntimeConverter(off)
	legacyCalls, canonicalCalls := 0, 0
	if err := withStrideE10TenantRuntimeAuthority(ctx, StrideE10TenantSurfacePushDelivery, sessionHash, func() error {
		legacyCalls++
		return nil
	}, func(StrideE10TenantPrincipal) error {
		canonicalCalls++
		return nil
	}); err != nil || legacyCalls != 1 || canonicalCalls != 0 {
		t.Fatalf("off valve changed legacy behavior: err=%v legacy=%d canonical=%d", err, legacyCalls, canonicalCalls)
	}
	restore()

	shadow, _, shadowResolver, _ := strideE10TenantTestConverter(now, true, StrideE10TenantConversionShadow)
	shadowResolver.set(StrideE10TenantAuthoritySnapshot{}, errors.New("shadow authority unavailable"))
	restore = InstallStrideE10TenantRuntimeConverter(shadow)
	legacyCalls, canonicalCalls = 0, 0
	if err := withStrideE10TenantRuntimeAuthority(ctx, StrideE10TenantSurfacePushDelivery, sessionHash, func() error {
		legacyCalls++
		return nil
	}, func(StrideE10TenantPrincipal) error {
		canonicalCalls++
		return nil
	}); err != nil || legacyCalls != 1 || canonicalCalls != 0 {
		t.Fatalf("shadow suppressed or authorized runtime use: err=%v legacy=%d canonical=%d", err, legacyCalls, canonicalCalls)
	}
	restore()

	cutover, _, resolver, _ := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	restore = InstallStrideE10TenantRuntimeConverter(cutover)
	defer restore()
	entered := make(chan struct{})
	release := make(chan struct{})
	useDone := make(chan error, 1)
	go func() {
		useDone <- withStrideE10TenantRuntimeAuthority(ctx, StrideE10TenantSurfacePushDelivery, sessionHash, func() error {
			return errors.New("legacy callback reached in cutover")
		}, func(principal StrideE10TenantPrincipal) error {
			if principal.PersonID != "person-one" {
				return errors.New("wrong canonical person")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	mutationDone := make(chan struct{})
	go func() {
		next := strideE10TenantTestSnapshot(now)
		next.Generation++
		resolver.set(next, nil)
		close(mutationDone)
	}()
	select {
	case <-mutationDone:
		t.Fatal("canonical authority changed while final use callback was active")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-useDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-mutationDone:
	case <-time.After(time.Second):
		t.Fatal("authority mutation did not resume after canonical use")
	}
}

func TestStrideE10TenantCutoverRejectsUnmappedLegacyIdentity(t *testing.T) {
	now := time.Now().UTC()
	converter, _, resolver, _ := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	snapshot := strideE10TenantTestSnapshot(now)
	snapshot.Legacy = StrideE10LegacyPrincipalProjection{TenantID: "org-one", AccountSubjectDigest: strings.Repeat("f", 64)}
	resolver.set(snapshot, nil)
	if _, err := converter.Resolve(context.Background(), StrideE10TenantSurfacePushDelivery, snapshot.SessionHash); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("unmapped legacy email identity authorized cutover: %v", err)
	}
}
