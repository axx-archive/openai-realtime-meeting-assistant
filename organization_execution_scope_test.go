package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type organizationExecutionFixture struct {
	runtime                      *StrideE10ProductLiveRuntime
	token, hash, personID, email string
}

func newOrganizationExecutionFixture(t *testing.T) *organizationExecutionFixture {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_TENANT_ID", "organization_1")
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "organization_1")
	_, converter, _ := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionShadow, true)
	restore := InstallStrideE10TenantRuntimeConverter(converter)
	t.Cleanup(restore)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return time.Now().UTC() })
	previous := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = runtime
	t.Cleanup(func() { strideE10LiveProductRuntime = previous })
	fixture := &organizationExecutionFixture{runtime: runtime, token: "organization-execution-test-token", personID: "person_execution_test", email: "execution@example.com"}
	fixture.hash = hashResetToken(fixture.token)
	now := time.Now().UTC().Add(-time.Hour)
	person := organizationTestPerson(fixture.personID, 'c', now)
	person.AccountSubjectDigest = sha256Hex([]byte(fixture.email))
	if err := runtime.organization.RegisterPerson(person); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		org, owner, event := organizationTestCreate(person.Header.ID, i, now)
		if err := runtime.organization.CreateOrganization(person.Header.ID, org, owner, event); err != nil {
			t.Fatal(err)
		}
	}
	fixture.bind(1, 1)
	return fixture
}
func (f *organizationExecutionFixture) bind(org int, revision int64) {
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	orgID, memberID := fmt.Sprintf("organization_%d", org), fmt.Sprintf("membership_%d_owner", org)
	sessions := userSessionStore()
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	f.runtime.organization.mu.Lock()
	defer f.runtime.organization.mu.Unlock()
	f.runtime.organization.sessions[f.hash] = ActiveOrganizationSession{Header: organizationTestHeader(STRIDEGlobalPersonTenant, "active_execution_test", revision, STRIDEContractActiveOrganizationSession, 'e', now), SessionSubjectDigest: f.hash, PersonID: f.personID, OrganizationID: orgID, MembershipID: memberID, MembershipRevision: 1, SessionRevision: revision, Status: "active", BoundAt: now, ExpiresAt: expires}
	sessions.sessions[f.hash] = sessionRecord{Email: f.email, Expires: expires, PersonID: f.personID, ActiveOrganizationID: orgID, OrganizationMembershipID: memberID, OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: revision, AccountSubjectDigest: sha256Hex([]byte(f.email)), AuthorityGeneration: uint64(revision + 1)}
}
func (f *organizationExecutionFixture) request(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+f.token)
	return request
}

type executionUnreadBody struct{ read bool }

func (b *executionUnreadBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (b *executionUnreadBody) Close() error             { return nil }

func TestOrganizationExecutionShadowRejectsOtherOrgBeforeLegacyUse(t *testing.T) {
	f := newOrganizationExecutionFixture(t)
	f.bind(2, 2)
	paths := []string{"/assistant/chat-threads", "/assistant/chat-threads/thread-one", "/assistant/studio-projects", "/assistant/memory", "/assistant/query", "/assistant/goal", "/assistant/threads", "/artifacts/report", "/assistant/files/file-one", "/rooms", "/participants", "/archives/old", "/api/usage/rollup", "/calendar/event.ics", "/calendar/meetings.ics", "/signals/survey", "/client-config", "/native/config", "/websocket"}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(method+path, func(t *testing.T) {
				called := false
				body := &executionUnreadBody{}
				request := f.request(method, path+"?tenantId=organization_1&orgId=organization_1")
				request.Body = body
				rr := httptest.NewRecorder()
				strideE10TenantHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					_, _ = io.ReadAll(r.Body)
					_, _ = w.Write([]byte("BONFIRE CONTENT"))
				})).ServeHTTP(rr, request)
				if called || body.read || rr.Code != http.StatusConflict || strings.Contains(rr.Body.String(), "BONFIRE CONTENT") {
					t.Fatalf("scope bypass called=%v read=%v status=%d body=%s", called, body.read, rr.Code, rr.Body.String())
				}
			})
		}
	}
}
func TestOrganizationExecutionPreservesDefaultAndAuthorityManagement(t *testing.T) {
	f := newOrganizationExecutionFixture(t)
	called := 0
	handler := strideE10TenantHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Proves the wrapper does not hold session locks around ordinary handlers.
		if _, ok := userSessionStore().lookupRecord(f.token); !ok {
			t.Error("missing session")
		}
		called++
		_, _ = w.Write([]byte("permitted"))
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, f.request(http.MethodGet, "/assistant/chat-threads"))
	if rr.Code != http.StatusOK || called != 1 {
		t.Fatalf("default denied %d %s", rr.Code, rr.Body.String())
	}
	f.bind(2, 2)
	for _, route := range []struct{ method, path string }{{"GET", "/api/organizations"}, {"POST", "/api/organizations"}, {"POST", "/api/session/active-organization/membership_1_owner"}, {"GET", "/api/identity/v1/me/profile"}, {"GET", "/api/stride/v1/mobile/surfaces/organizations"}, {"POST", "/api/stride/v1/mobile/actions/server_minted_action"}, {"GET", "/api/stride/v1/mobile/surfaces/network-preview"}, {"GET", "/healthz"}, {"GET", "/"}} {
		before := called
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, f.request(route.method, route.path))
		if called != before+1 || rr.Code != http.StatusOK {
			t.Fatalf("authority/public route blocked %s %s: %d", route.method, route.path, rr.Code)
		}
	}
	for _, path := range []string{"/api/organizations-not-authority", "/api/stride/v1/mobile/surfaces/unknown-legacy", "/api/stride/v1/mobile/actions/not-an-action/extra"} {
		before := called
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, f.request("GET", path))
		if called != before || rr.Code != http.StatusConflict {
			t.Fatalf("prefix exemption %s", path)
		}
	}
	f.bind(1, 3)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, f.request("GET", "/assistant/chat-threads"))
	if rr.Code != http.StatusOK {
		t.Fatal("switch back unavailable")
	}
	// Existing email-only sessions remain governed by the legacy handler.
	sessions := userSessionStore()
	sessions.mu.Lock()
	sessions.sessions[f.hash] = sessionRecord{Email: f.email, Expires: time.Now().Add(time.Hour)}
	sessions.mu.Unlock()
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, f.request("GET", "/assistant/chat-threads"))
	if rr.Code != http.StatusOK {
		t.Fatal("legacy session behavior changed")
	}
}
func TestOrganizationExecutionRevalidatesMembershipAndResponse(t *testing.T) {
	for _, mode := range []string{"revoked", "revision", "session", "zero_org", "tenant_configuration"} {
		t.Run(mode, func(t *testing.T) {
			f := newOrganizationExecutionFixture(t)
			switch mode {
			case "revoked", "revision":
				f.runtime.organization.mu.Lock()
				member := f.runtime.organization.memberships["membership_1_owner"]
				if mode == "revoked" {
					member.Status = "revoked"
				} else {
					member.Header.Revision++
				}
				f.runtime.organization.memberships[member.Header.ID] = member
				f.runtime.organization.mu.Unlock()
			case "session":
				f.runtime.organization.mu.Lock()
				delete(f.runtime.organization.sessions, f.hash)
				f.runtime.organization.mu.Unlock()
			case "zero_org":
				sessions := userSessionStore()
				sessions.mu.Lock()
				record := sessions.sessions[f.hash]
				record.ActiveOrganizationID = ""
				record.OrganizationMembershipID = ""
				record.OrganizationMembershipRev = 0
				record.ActiveOrganizationSessionRev = 0
				sessions.sessions[f.hash] = record
				sessions.mu.Unlock()
			case "tenant_configuration":
				t.Setenv("BONFIRE_TENANT_ID", "organization_2")
			}
			called := false
			rr := httptest.NewRecorder()
			strideE10TenantHTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(rr, f.request("GET", "/assistant/studio-projects"))
			if called || rr.Code != http.StatusConflict {
				t.Fatalf("stale scope passed mode=%s status=%d", mode, rr.Code)
			}
		})
	}
	f := newOrganizationExecutionFixture(t)
	rr := httptest.NewRecorder()
	strideE10TenantHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.bind(2, 2)
		_, _ = w.Write([]byte("BONFIRE SECRET AFTER SWITCH"))
	})).ServeHTTP(rr, f.request("GET", "/assistant/memory"))
	if strings.Contains(rr.Body.String(), "BONFIRE SECRET") || rr.Code != http.StatusConflict {
		t.Fatalf("response survived switch: %d %s", rr.Code, rr.Body.String())
	}
}
func TestOrganizationExecutionSocketScopeExpiresOnSwitchAndRevocation(t *testing.T) {
	f := newOrganizationExecutionFixture(t)
	lease, err := strideE10BindTenantWebSocket(f.request("GET", "/websocket"))
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	if err := lease.withCurrentWrite(context.Background(), func() error { writes++; return nil }); err != nil || writes != 1 {
		t.Fatalf("default write failed %v", err)
	}
	f.bind(2, 2)
	if _, err := strideE10BindTenantWebSocket(f.request("GET", "/websocket")); err == nil {
		t.Fatal("other org upgraded")
	}
	if err := lease.withCurrentWrite(context.Background(), func() error { writes++; return nil }); !errors.Is(err, errOrganizationExecutionUnavailable) || writes != 1 {
		t.Fatal("old socket wrote after switch")
	}
	f.bind(1, 3)
	if err := lease.withCurrentWrite(context.Background(), func() error { writes++; return nil }); err == nil || writes != 1 {
		t.Fatal("old lease resurrected after switch back")
	}
	current, err := strideE10BindTenantWebSocket(f.request("GET", "/websocket"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := userSessionStore()
	sessions.mu.Lock()
	delete(sessions.sessions, f.hash)
	sessions.mu.Unlock()
	if err := current.withCurrent(context.Background(), func() error { t.Fatal("revoked socket read admitted"); return nil }); err == nil {
		t.Fatal("revoked lease allowed")
	}
}
func TestOrganizationExecutionSocketReadDoesNotHoldLocksAndRechecksFrame(t *testing.T) {
	f := newOrganizationExecutionFixture(t)
	connected := make(chan struct{})
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lease, err := strideE10BindTenantWebSocket(r)
		if err != nil {
			result <- err
			return
		}
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		writer := &threadSafeWriter{Conn: conn, tenantLease: &lease}
		close(connected)
		_, _, err = writer.ReadTenantMessage(context.Background())
		result <- err
	}))
	defer server.Close()
	headers := http.Header{"Authorization": []string{"Bearer " + f.token}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/websocket", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-connected
	switched := make(chan struct{})
	go func() { f.bind(2, 2); close(switched) }()
	select {
	case <-switched:
	case <-time.After(time.Second):
		t.Fatal("blocking socket read held organization authority locks")
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("old-scope command")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, errOrganizationExecutionUnavailable) {
			t.Fatalf("post-read error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("socket frame did not finish")
	}
}
func TestOrganizationExecutionGuestModeRequiresActualGuestGrant(t *testing.T) {
	f := newOrganizationExecutionFixture(t)
	f.bind(2, 2)
	request := f.request("GET", "/websocket?as=guest")
	if _, err := strideE10BindTenantWebSocket(request); err == nil {
		t.Fatal("query parameter bypassed membership")
	}
	guestToken, err := userSessionStore().createGuest("room-explicit-guest", "Guest")
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: guestCookieName, Value: guestToken})
	lease, err := strideE10BindTenantWebSocket(request)
	if err != nil || lease.sessionHash != "" || lease.executionScope != nil {
		t.Fatalf("explicit guest grant not narrowed: %+v %v", lease, err)
	}
}

type organizationExecutionSlowWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
	writes  int
}

func (w *organizationExecutionSlowWriter) Header() http.Header { return w.header }
func (*organizationExecutionSlowWriter) WriteHeader(int)       {}
func (w *organizationExecutionSlowWriter) Write(body []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		close(w.entered)
		<-w.release
	}
	return len(body), nil
}
func TestOrganizationExecutionSlowHTTPWriteDoesNotHoldAuthorityLock(t *testing.T) {
	f := newOrganizationExecutionFixture(t)
	scope, err := organizationExecutionScopeForSession(context.Background(), f.hash)
	if err != nil {
		t.Fatal(err)
	}
	slow := &organizationExecutionSlowWriter{header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{})}
	writer := &organizationExecutionResponseWriter{ResponseWriter: slow, ctx: context.Background(), hash: f.hash, scope: scope}
	done := make(chan error, 1)
	go func() { _, err := writer.Write([]byte("already authorized chunk")); done <- err }()
	<-slow.entered
	switched := make(chan struct{})
	go func() { f.bind(2, 2); close(switched) }()
	select {
	case <-switched:
	case <-time.After(time.Second):
		close(slow.release)
		t.Fatal("slow peer blocked session switch")
	}
	close(slow.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("new chunk")); !errors.Is(err, errOrganizationExecutionUnavailable) || slow.writes != 1 {
		t.Fatalf("post-switch chunk escaped: writes=%d error=%v", slow.writes, err)
	}
}
func TestOrganizationExecutionSlowSocketWriteDoesNotHoldAuthorityLock(t *testing.T) {
	f := newOrganizationExecutionFixture(t)
	lease, err := strideE10BindTenantWebSocket(f.request("GET", "/websocket"))
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- lease.withCurrentWrite(context.Background(), func() error { close(entered); <-release; return nil })
	}()
	<-entered
	switched := make(chan struct{})
	go func() { f.bind(2, 2); close(switched) }()
	select {
	case <-switched:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("socket writer blocked session switch")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	called := false
	if err := lease.withCurrentWrite(context.Background(), func() error { called = true; return nil }); !errors.Is(err, errOrganizationExecutionUnavailable) || called {
		t.Fatal("post-switch socket frame escaped")
	}
}
