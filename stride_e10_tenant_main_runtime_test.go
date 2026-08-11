package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStrideE10TenantHTTPOuterWrapperPublicAndProtectedCutover(t *testing.T) {
	request, converter, resolver := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	restore := InstallStrideE10TenantRuntimeConverter(converter)
	defer restore()
	seen := false
	handler := strideE10TenantHTTPHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := strideE10TenantPrincipalFromContext(request.Context())
		if !ok || principal.PersonID != "person-one" || !strideE10TenantSurfaceUseBound(request.Context(), StrideE10TenantSurfaceHTTP) {
			t.Error("protected handler lacked canonical HTTP principal")
			return
		}
		seen = true
		writer.WriteHeader(http.StatusNoContent)
	}))
	request.URL.Path = "/api/stride/v1/mobile/surfaces/profile"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !seen || recorder.Code != http.StatusNoContent {
		t.Fatalf("protected result seen=%t status=%d body=%s", seen, recorder.Code, recorder.Body.String())
	}

	resolver.set(StrideE10TenantAuthoritySnapshot{}, errors.New("revoked"))
	seen = false
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request.Clone(request.Context()))
	if seen || recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("revoked protected request reached handler seen=%t status=%d", seen, recorder.Code)
	}

	publicCalled := false
	public := strideE10TenantHTTPHandler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		publicCalled = true
		writer.WriteHeader(http.StatusOK)
	}))
	publicRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	publicRecorder := httptest.NewRecorder()
	public.ServeHTTP(publicRecorder, publicRequest)
	if !publicCalled || publicRecorder.Code != http.StatusOK {
		t.Fatalf("public allowlist was tenant-gated called=%t status=%d", publicCalled, publicRecorder.Code)
	}
	marketplaceCalled := false
	marketplace := strideE10TenantHTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { marketplaceCalled = true }))
	marketplaceRequest := request.Clone(request.Context())
	marketplaceRequest.URL.Path = strideRuntimeAPIBase + "marketplace"
	marketplaceRecorder := httptest.NewRecorder()
	marketplace.ServeHTTP(marketplaceRecorder, marketplaceRequest)
	if marketplaceCalled || marketplaceRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("singleton marketplace did not stay unavailable called=%t status=%d", marketplaceCalled, marketplaceRecorder.Code)
	}
	current := strideE10TenantTestSnapshot(time.Now().UTC())
	current.SessionHash = strideE10SessionHashFromRequest(request)
	current.ActiveSession.SessionSubjectDigest = current.SessionHash
	resolver.set(current, nil)
	for _, deniedPath := range []string{"/assistant/board", "/assistant/chat-threads", "/rooms", "/assistant/query", "/assistant/goal", "/artifacts/open"} {
		called := false
		denied := strideE10TenantHTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		deniedRequest := request.Clone(request.Context())
		deniedRequest.URL.Path = deniedPath
		deniedRecorder := httptest.NewRecorder()
		denied.ServeHTTP(deniedRecorder, deniedRequest)
		if called || deniedRecorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("pending cutover path %s escaped called=%t status=%d", deniedPath, called, deniedRecorder.Code)
		}
	}
	zero := current
	zero.Session.ActiveOrganizationID, zero.Session.OrganizationMembershipID = "", ""
	zero.Session.OrganizationMembershipRev, zero.Session.ActiveOrganizationSessionRev = 0, 0
	zero.Membership, zero.ActiveSession = OrganizationMembership{}, ActiveOrganizationSession{}
	zero.Legacy.TenantID = STRIDEGlobalPersonTenant
	resolver.set(zero, nil)
	called := false
	zeroGeneric := strideE10TenantHTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	zeroRequest := request.Clone(request.Context())
	zeroRequest.URL.Path = "/api/usage/rollup"
	zeroRecorder := httptest.NewRecorder()
	zeroGeneric.ServeHTTP(zeroRecorder, zeroRequest)
	if called || zeroRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("zero-org generic API escaped called=%t status=%d", called, zeroRecorder.Code)
	}
}

func TestStrideE10TenantCutoverRejectsRenderCallbackWithoutCanonicalEnvelope(t *testing.T) {
	_, converter, _ := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	restore := InstallStrideE10TenantRuntimeConverter(converter)
	defer restore()
	t.Setenv("BONFIRE_RUNNER_TOKEN", "render-runner-secret")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	defer func() { kanbanApp = previousApp }()
	artifact, _, err := app.createOSArtifactWithMetadata("artifact", "Render", "unchanged", "tester", map[string]string{"renderJobId": "render-job-cutover"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(renderRunnerCallbackPayload{JobID: "render-job-cutover", ArtifactID: artifact.ID, Status: renderJobStatusRunning})
	request := httptest.NewRequest(http.MethodPost, "/internal/render/jobs/result", bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer render-runner-secret")
	recorder := httptest.NewRecorder()
	internalRenderRunnerResultHandler(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("cutover render callback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	unchanged, _ := app.osArtifactByID(artifact.ID)
	if unchanged.Text != artifact.Text || unchanged.Metadata["renderStatus"] != "" {
		t.Fatalf("cutover render callback changed artifact: %+v", unchanged)
	}
}

func TestStrideE10TenantHTTPSurfaceInventoryAndGuestRoutes(t *testing.T) {
	tests := []struct {
		path      string
		surface   StrideE10TenantSurface
		protected bool
	}{
		{"/assistant/chat-threads", StrideE10TenantSurfaceChat, true},
		{"/assistant/board/cards", StrideE10TenantSurfaceBoard, true},
		{"/assistant/memory", StrideE10TenantSurfaceBrain, true},
		{"/assistant/query", StrideE10TenantSurfaceScout, true},
		{"/assistant/goal", StrideE10TenantSurfaceWorkQueue, true},
		{"/rooms/office/join", StrideE10TenantSurfaceRoomAdmission, true},
		{"/artifacts/open", StrideE10TenantSurfaceProductContext, true},
		{strideRuntimeAPIBase + "marketplace", StrideE10TenantSurfaceMarketplace, true},
		{"/healthz", "", false},
		{"/auth/login", "", false},
		{"/public/app.js", "", false},
		{"/guest/join", "", false},
		{"/websocket", "", false},
	}
	for _, test := range tests {
		surface, protected := strideE10TenantSurfaceForHTTPPath(test.path)
		if surface != test.surface || protected != test.protected {
			t.Fatalf("path=%s surface=%q protected=%t", test.path, surface, protected)
		}
	}
}

func TestStrideE10TenantCacheUsesFullTupleSourceRevisionAndRevalidates(t *testing.T) {
	request, converter, resolver := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	restore := InstallStrideE10TenantRuntimeConverter(converter)
	defer restore()
	resolution, err := converter.Resolve(context.Background(), StrideE10TenantSurfaceCache, strideE10SessionHashFromRequest(request))
	if err != nil || resolution.Capability == nil {
		t.Fatal(err)
	}
	cache := &strideE10TenantCache{}
	if err := cache.Put(resolution.Capability.Principal, "board", "resource-one", 7, "value-seven"); err != nil {
		t.Fatal(err)
	}
	if value, found, err := cache.GetCurrent(context.Background(), resolution.Capability, "board", "resource-one", 7); err != nil || !found || value != "value-seven" {
		t.Fatalf("current cache value=%v found=%t err=%v", value, found, err)
	}
	if _, found, err := cache.GetCurrent(context.Background(), resolution.Capability, "board", "resource-one", 8); err != nil || found {
		t.Fatalf("source-revision cache alias found=%t err=%v", found, err)
	}
	resolver.set(StrideE10TenantAuthoritySnapshot{}, errors.New("revoked"))
	if _, found, err := cache.GetCurrent(context.Background(), resolution.Capability, "board", "resource-one", 7); !errors.Is(err, ErrStrideE10TenantAuthorityStale) || found {
		t.Fatalf("revoked cache found=%t err=%v", found, err)
	}
}

func TestStrideE10TenantWebSocketLeaseRevalidatesEveryEffect(t *testing.T) {
	request, converter, resolver := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	restore := InstallStrideE10TenantRuntimeConverter(converter)
	defer restore()
	lease, err := strideE10BindTenantWebSocket(request)
	if err != nil || !lease.canonical {
		t.Fatalf("bind canonical=%t err=%v", lease.canonical, err)
	}
	effects := 0
	if err := lease.withCurrent(context.Background(), func() error { effects++; return nil }); err != nil || effects != 1 {
		t.Fatalf("current effect count=%d err=%v", effects, err)
	}
	resolver.set(StrideE10TenantAuthoritySnapshot{}, errors.New("revoked"))
	if err := lease.withCurrent(context.Background(), func() error { effects++; return nil }); !errors.Is(err, ErrStrideE10TenantAuthorityStale) || effects != 1 {
		t.Fatalf("revoked websocket effect count=%d err=%v", effects, err)
	}
}

func TestStrideE10TenantCacheRejectsIncompleteAuthorityTuple(t *testing.T) {
	principal := StrideE10TenantPrincipal{TenantID: "org-one", PersonID: "person-one", ActiveOrganizationID: "org-one", OrganizationMembershipID: "membership-one", OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1, AuthorityGeneration: 1}
	for _, mutate := range []func(*StrideE10TenantPrincipal){
		func(value *StrideE10TenantPrincipal) { value.ActiveOrganizationID = "" },
		func(value *StrideE10TenantPrincipal) { value.OrganizationMembershipID = "" },
		func(value *StrideE10TenantPrincipal) { value.AuthorityGeneration = 0 },
	} {
		candidate := principal
		mutate(&candidate)
		if _, err := strideE10TenantCacheKeyFor(candidate, "board", "resource-one", 1); err == nil {
			t.Fatalf("incomplete principal accepted: %+v", candidate)
		}
	}
	if _, err := strideE10TenantCacheKeyFor(principal, "board", "resource-one", 0); err == nil {
		t.Fatal("zero source revision accepted")
	}
}

func TestStrideE10MainResolverDerivesExactPersonAndZeroOrganization(t *testing.T) {
	now := time.Now().UTC()
	digest := sha256Hex([]byte("person@example.com"))
	person := organizationTestPerson("person-main-resolver", '7', now.Add(-time.Hour))
	person.AccountSubjectDigest = digest
	organizations := NewOrganizationAuthorityService()
	if err := organizations.RegisterPerson(person); err != nil {
		t.Fatal(err)
	}
	sessions := &sessionStore{sessions: map[string]sessionRecord{}}
	hash := sha256Hex([]byte("main-resolver-session"))
	sessions.sessions[hash] = sessionRecord{Email: "person@example.com", Expires: now.Add(time.Hour), PersonID: person.Header.ID, AccountSubjectDigest: digest, AuthorityGeneration: 3}
	resolver := &strideE10MainTenantAuthorityResolver{sessions: sessions, organizations: organizations, now: func() time.Time { return now }}
	called := false
	if err := resolver.WithCurrentTenantAuthority(context.Background(), StrideE10TenantSurfaceHTTP, hash, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		called = true
		if snapshot.Person.Header.ID != person.Header.ID || snapshot.Generation != 3 || snapshot.Membership != (OrganizationMembership{}) || snapshot.ActiveSession != (ActiveOrganizationSession{}) {
			t.Fatalf("zero-org snapshot=%+v", snapshot)
		}
		return nil
	}); err != nil || !called {
		t.Fatalf("zero-org resolver called=%t err=%v", called, err)
	}
	sessions.sessions[hash] = sessionRecord{Email: "person@example.com", Expires: now.Add(time.Hour), PersonID: person.Header.ID, AccountSubjectDigest: sha256Hex([]byte("other@example.com")), AuthorityGeneration: 4}
	if err := resolver.WithCurrentTenantAuthority(context.Background(), StrideE10TenantSurfaceHTTP, hash, func(StrideE10TenantAuthoritySnapshot) error { return nil }); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("mismatched account-person mapping authorized: %v", err)
	}
}

func TestStrideE10MainResolverHoldsExactActiveOrganizationThroughUse(t *testing.T) {
	now := time.Now().UTC()
	digest := sha256Hex([]byte("active@example.com"))
	person := organizationTestPerson("person-active-resolver", '8', now.Add(-2*time.Hour))
	person.AccountSubjectDigest = digest
	organizations := NewOrganizationAuthorityService()
	if err := organizations.RegisterPerson(person); err != nil {
		t.Fatal(err)
	}
	organizationID, membershipID := "organization-active-resolver", "membership-active-resolver"
	organization := Organization{
		Header: organizationTestHeader(STRIDEGlobalPersonTenant, organizationID, 1, STRIDEContractOrganization, '7', now.Add(-time.Hour)),
		Name:   "Active Resolver Organization", Slug: "active-resolver-organization", Status: "active", Discoverability: "private",
		CreatorPersonID: person.Header.ID, PolicyRevision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	membership := organizationTestMembership(membershipID, person.Header.ID, organizationID, "admin", 4, now.Add(-time.Hour), "membership-owner-resolver")
	hash := sha256Hex([]byte("active-resolver-session"))
	activeSession := ActiveOrganizationSession{Header: organizationTestHeader(STRIDEGlobalPersonTenant, "active-session-resolver", 9, STRIDEContractActiveOrganizationSession, '9', now.Add(-time.Hour)), SessionSubjectDigest: hash, PersonID: person.Header.ID, OrganizationID: organizationID, MembershipID: membershipID, MembershipRevision: membership.Header.Revision, SessionRevision: 9, Status: "active", BoundAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	organizations.organizations[organizationID] = organization
	organizations.memberships[membershipID] = membership
	organizations.sessions[hash] = activeSession
	sessions := &sessionStore{sessions: map[string]sessionRecord{hash: {Email: "active@example.com", Expires: activeSession.ExpiresAt, PersonID: person.Header.ID, ActiveOrganizationID: organizationID, OrganizationMembershipID: membershipID, OrganizationMembershipRev: membership.Header.Revision, ActiveOrganizationSessionRev: activeSession.SessionRevision, AccountSubjectDigest: digest, AuthorityGeneration: 11}}}
	resolver := &strideE10MainTenantAuthorityResolver{sessions: sessions, organizations: organizations, now: func() time.Time { return now }}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- resolver.WithCurrentTenantAuthority(context.Background(), StrideE10TenantSurfaceBoard, hash, func(snapshot StrideE10TenantAuthoritySnapshot) error {
			if snapshot.Membership.Header.ID != membershipID || snapshot.ActiveSession.SessionRevision != 9 || snapshot.Generation != 11 {
				return errors.New("incorrect active authority snapshot")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	mutated := make(chan struct{})
	go func() {
		sessions.mu.Lock()
		record := sessions.sessions[hash]
		record.AuthorityGeneration++
		sessions.sessions[hash] = record
		sessions.mu.Unlock()
		close(mutated)
	}()
	select {
	case <-mutated:
		t.Fatal("session generation changed during final authority use")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-mutated:
	case <-time.After(time.Second):
		t.Fatal("session mutation remained blocked after final use")
	}
}

func TestStrideE10AuthSuccessMintsCanonicalZeroOrgAndBlocksLegacyBypass(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Now().UTC()
	_, converter, _ := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	restoreConverter := InstallStrideE10TenantRuntimeConverter(converter)
	defer restoreConverter()
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	email := "canonical@example.com"
	digest := sha256Hex([]byte(email))
	person := organizationTestPerson("person-auth-canonical", '6', now.Add(-time.Hour))
	person.AccountSubjectDigest = digest
	if err := runtime.organization.RegisterPerson(person); err != nil {
		t.Fatal(err)
	}
	previousRuntime := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = runtime
	defer func() { strideE10LiveProductRuntime = previousRuntime }()
	token, err := strideE10CreateAuthenticatedSession(email)
	if err != nil {
		t.Fatal(err)
	}
	record, ok := userSessionStore().lookupRecord(token)
	if !ok || record.PersonID != person.Header.ID || record.AccountSubjectDigest != digest || record.AuthorityGeneration != 1 || record.ActiveOrganizationID != "" || record.OrganizationMembershipID != "" || record.OrganizationMembershipRev != 0 || record.ActiveOrganizationSessionRev != 0 {
		t.Fatalf("canonical auth record=%+v ok=%t", record, ok)
	}
	if _, err := userSessionStore().create(email); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy session creation bypassed cutover: %v", err)
	}
	runtime.organization.mu.Lock()
	revoked := runtime.organization.persons[person.Header.ID]
	revoked.Status = "revoked"
	runtime.organization.persons[person.Header.ID] = revoked
	runtime.organization.mu.Unlock()
	if _, err := strideE10CreateAuthenticatedSession(email); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("revoked person authenticated: %v", err)
	}
}
