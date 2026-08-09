package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type strideE10W5TestCustody struct {
	inspect []MyMindPrivateSource
	put     MyMindPrivateSource
	err     error
	calls   []string
}

func (s *strideE10W5TestCustody) Put(_ context.Context, _ MyMindPrivateAuthority, _, sourceID, kind, body string, revision int64) (MyMindPrivateSource, error) {
	s.calls = append(s.calls, "put:"+sourceID+":"+kind+":"+body)
	value := s.put
	value.Revision = revision + 1
	return value, s.err
}
func (s *strideE10W5TestCustody) Correct(_ context.Context, _ MyMindPrivateAuthority, _, sourceID, body string, revision int64) (MyMindPrivateSource, error) {
	s.calls = append(s.calls, "correct:"+sourceID+":"+body)
	value := s.put
	value.Revision = revision + 1
	return value, s.err
}
func (s *strideE10W5TestCustody) Inspect(context.Context, MyMindPrivateAuthority) ([]MyMindPrivateSource, error) {
	s.calls = append(s.calls, "inspect")
	return append([]MyMindPrivateSource(nil), s.inspect...), s.err
}
func (s *strideE10W5TestCustody) Forget(_ context.Context, _ MyMindPrivateAuthority, _, sourceID string, _ int64) error {
	s.calls = append(s.calls, "forget:"+sourceID)
	return s.err
}
func (s *strideE10W5TestCustody) Export(context.Context, MyMindPrivateAuthority) (MyMindPrivateExport, error) {
	s.calls = append(s.calls, "export")
	return MyMindPrivateExport{PersonID: "person-one", ManifestDigest: strings.Repeat("a", 64)}, s.err
}
func (s *strideE10W5TestCustody) Rotate(context.Context, MyMindPrivateAuthority, string) error {
	s.calls = append(s.calls, "rotate")
	return s.err
}
func (s *strideE10W5TestCustody) DeletePerson(context.Context, MyMindPrivateAuthority, string) error {
	s.calls = append(s.calls, "delete")
	return s.err
}

func strideE10W5TestAuthority() MyMindPrivateAuthority {
	return MyMindPrivateAuthority{PersonID: "person-one", OrganizationID: "org-one", MembershipID: "membership-one", MembershipRevision: 1, SessionSubjectDigest: strings.Repeat("b", 64), SessionRevision: 1, At: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)}
}

func TestStrideE10W5HTTPIsPrivateFailClosedAndBodyBounded(t *testing.T) {
	custody := &strideE10W5TestCustody{inspect: []MyMindPrivateSource{{PersonID: "person-one", SourceID: "source-one", Revision: 1, Kind: "reflection", Body: "private words", BodyDigest: strings.Repeat("c", 64), ConsentRevision: 1, UpdatedAt: time.Now().UTC()}}}
	resolve := func(*http.Request) (MyMindPrivateAuthority, error) { return strideE10W5TestAuthority(), nil }

	off := NewStrideE10W5HTTP(custody, resolve, func() bool { return false })
	offRecorder := httptest.NewRecorder()
	off.ServeHTTP(offRecorder, httptest.NewRequest(http.MethodGet, "/api/mymind/v1/sources", nil))
	if offRecorder.Code != http.StatusServiceUnavailable || len(custody.calls) != 0 || offRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("feature-off custody route was not inert: status=%d calls=%v headers=%v", offRecorder.Code, custody.calls, offRecorder.Header())
	}

	denied := NewStrideE10W5HTTP(custody, func(*http.Request) (MyMindPrivateAuthority, error) { return MyMindPrivateAuthority{}, ErrMyMindDenied }, func() bool { return true })
	deniedRecorder := httptest.NewRecorder()
	denied.ServeHTTP(deniedRecorder, httptest.NewRequest(http.MethodGet, "/api/mymind/v1/sources", nil))
	if deniedRecorder.Code != http.StatusNotFound || len(custody.calls) != 0 {
		t.Fatalf("denied authority was distinguishable or reached custody: status=%d calls=%v", deniedRecorder.Code, custody.calls)
	}

	live := NewStrideE10W5HTTP(custody, resolve, func() bool { return true })
	readRecorder := httptest.NewRecorder()
	live.ServeHTTP(readRecorder, httptest.NewRequest(http.MethodGet, "/api/mymind/v1/sources", nil))
	if readRecorder.Code != http.StatusOK || readRecorder.Header().Get("Cache-Control") != "no-store" || readRecorder.Header().Get("Pragma") != "no-cache" || !strings.Contains(readRecorder.Body.String(), "private words") {
		t.Fatalf("private inspection response mismatch: status=%d headers=%v body=%s", readRecorder.Code, readRecorder.Header(), readRecorder.Body.String())
	}

	badRecorder := httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodPost, "/api/mymind/v1/sources", strings.NewReader(`{"idempotencyKey":"idem-one","sourceId":"source-one","kind":"reflection","body":"private","expectedRevision":0,"personId":"person-two"}`))
	bad.Header.Set("Content-Type", "application/json")
	live.ServeHTTP(badRecorder, bad)
	if badRecorder.Code != http.StatusBadRequest || len(custody.calls) != 1 {
		t.Fatalf("unknown authority field reached custody: status=%d calls=%v", badRecorder.Code, custody.calls)
	}

	oversizedRecorder := httptest.NewRecorder()
	oversizedBody := `{"idempotencyKey":"idem-two","sourceId":"source-one","kind":"reflection","body":"` + strings.Repeat("x", myMindCustodyMaxBody) + `","expectedRevision":0}`
	oversized := httptest.NewRequest(http.MethodPost, "/api/mymind/v1/sources", strings.NewReader(oversizedBody))
	oversized.Header.Set("Content-Type", "application/json")
	live.ServeHTTP(oversizedRecorder, oversized)
	if oversizedRecorder.Code != http.StatusBadRequest || len(custody.calls) != 1 {
		t.Fatalf("oversized body reached custody: status=%d calls=%v", oversizedRecorder.Code, custody.calls)
	}
}

func TestStrideE10W5HTTPClosedMutationsAndOpaqueErrors(t *testing.T) {
	custody := &strideE10W5TestCustody{put: MyMindPrivateSource{PersonID: "person-one", SourceID: "source-one", Kind: "correction", Body: "fixed", BodyDigest: strings.Repeat("d", 64), ConsentRevision: 2, UpdatedAt: time.Now().UTC()}}
	handler := NewStrideE10W5HTTP(custody, func(*http.Request) (MyMindPrivateAuthority, error) { return strideE10W5TestAuthority(), nil }, func() bool { return true })

	request := func(method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		handler.ServeHTTP(recorder, r)
		return recorder
	}
	if got := request(http.MethodPost, "/api/mymind/v1/sources", `{"idempotencyKey":"idem-put","sourceId":"source-one","kind":"reflection","body":"private","expectedRevision":0}`); got.Code != http.StatusOK {
		t.Fatalf("put failed: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/mymind/v1/sources/source-one/correct", `{"idempotencyKey":"idem-correct","body":"fixed","expectedRevision":1}`); got.Code != http.StatusOK {
		t.Fatalf("correct failed: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/mymind/v1/sources/source-one/forget", `{"idempotencyKey":"idem-forget","expectedRevision":2}`); got.Code != http.StatusOK {
		t.Fatalf("forget failed: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/mymind/v1/sources/correct", `{"idempotencyKey":"idem-guessed","body":"wrong","expectedRevision":1}`); got.Code != http.StatusNotFound {
		t.Fatalf("malformed source selector did not fail opaque: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/mymind/v1/export", ""); got.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/mymind/v1/rotate", `{"idempotencyKey":"idem-rotate"}`); got.Code != http.StatusOK {
		t.Fatalf("rotate failed: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodDelete, "/api/mymind/v1/person", `{"idempotencyKey":"idem-delete"}`); got.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", got.Code, got.Body.String())
	}
	if want := []string{"put:source-one:reflection:private", "correct:source-one:fixed", "forget:source-one", "export", "rotate", "delete"}; strings.Join(custody.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("closed route dispatch mismatch: got %v want %v", custody.calls, want)
	}

	custody.err = ErrMyMindCustodyDenied
	opaque := request(http.MethodGet, "/api/mymind/v1/sources", "")
	var payload map[string]string
	if json.Unmarshal(opaque.Body.Bytes(), &payload) != nil || opaque.Code != http.StatusNotFound || payload["error"] != "not_found" {
		t.Fatalf("custody denial leaked detail: status=%d body=%s", opaque.Code, opaque.Body.String())
	}
	custody.err = ErrMyMindCustodyConflict
	conflict := request(http.MethodPost, "/api/mymind/v1/rotate", `{"idempotencyKey":"idem-rotate"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict was not explicit: status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	custody.err = errors.New("secret backend detail")
	unknown := request(http.MethodGet, "/api/mymind/v1/export", "")
	if unknown.Code != http.StatusNotFound || strings.Contains(unknown.Body.String(), "secret") {
		t.Fatalf("backend detail leaked: status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestStrideE10W5RuntimeUsesExactCurrentW4Authority(t *testing.T) {
	now := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	personID, organizationID, membershipID := "person-one", "org-one", "membership-one"
	accountDigest := strings.Repeat("a", 64)
	person := PersonPrincipal{Header: strideE10LiveHeader(STRIDEContractPersonPrincipal, STRIDEGlobalPersonTenant, personID, 1, "person", now), AccountSubjectDigest: accountDigest, Status: "active", RecoveryRevision: 1, CustodyRevision: 1}
	membership := OrganizationMembership{Header: strideE10LiveHeader(STRIDEContractOrganizationMembership, organizationID, membershipID, 1, "membership", now), PersonID: personID, OrganizationID: organizationID, Role: "member", Status: "active", GrantedAt: now.Add(-time.Hour)}
	token := "w5-current-session-token"
	sessionDigest := hashResetToken(token)
	active := ActiveOrganizationSession{Header: strideE10LiveHeader(STRIDEContractActiveOrganizationSession, STRIDEGlobalPersonTenant, "active-session-one", 1, "session", now), SessionSubjectDigest: sessionDigest, PersonID: personID, OrganizationID: organizationID, MembershipID: membershipID, MembershipRevision: 1, SessionRevision: 1, Status: "active", BoundAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	if person.Validate() != nil || membership.Validate() != nil || active.Validate() != nil {
		t.Fatal("invalid W5 runtime authority fixture")
	}
	runtime.organization.persons[personID] = person
	runtime.organization.memberships[membershipID] = membership
	runtime.organization.sessions[active.Header.ID] = active

	store := userSessionStore()
	store.mu.Lock()
	prior, hadPrior := store.sessions[sessionDigest]
	store.sessions[sessionDigest] = sessionRecord{Email: "w5@example.invalid", PersonID: personID, AccountSubjectDigest: accountDigest, ActiveOrganizationID: organizationID, OrganizationMembershipID: membershipID, OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1, AuthorityGeneration: 1, Expires: now.Add(time.Hour)}
	store.mu.Unlock()
	defer func() {
		store.mu.Lock()
		if hadPrior {
			store.sessions[sessionDigest] = prior
		} else {
			delete(store.sessions, sessionDigest)
		}
		store.mu.Unlock()
	}()

	priorRuntime := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = runtime
	defer func() {
		uninstallStrideE10W5ProductRuntime()
		strideE10LiveProductRuntime = priorRuntime
	}()
	custody := &strideE10W5TestCustody{inspect: []MyMindPrivateSource{}}
	if err := installStrideE10W5ProductRuntime(custody); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/mymind/v1/sources", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	off := httptest.NewRecorder()
	strideE10W5ProductHandler(off, request.Clone(request.Context()))
	if off.Code != http.StatusServiceUnavailable || len(custody.calls) != 0 {
		t.Fatalf("feature-off runtime reached custody: status=%d calls=%v", off.Code, custody.calls)
	}
	runtime.features[STRIDEFeaturePersonMyMindContext] = true
	live := httptest.NewRecorder()
	strideE10W5ProductHandler(live, request.Clone(request.Context()))
	if live.Code != http.StatusOK || strings.Join(custody.calls, ",") != "inspect" {
		t.Fatalf("current authority did not reach custody: status=%d calls=%v body=%s", live.Code, custody.calls, live.Body.String())
	}

	requested := MyMindPrivateAuthority{PersonID: personID, OrganizationID: organizationID, MembershipID: membershipID, MembershipRevision: 1, SessionSubjectDigest: sessionDigest, SessionRevision: 1, At: now}
	resolver := &strideE10W5CanonicalAuthorityResolver{runtime: runtime}
	entered, release, resolved := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		resolved <- resolver.WithCurrentMyMindPrivateAuthority(context.Background(), requested, func(MyMindPrivateAuthority) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	mutated := make(chan struct{})
	go func() {
		store.mu.Lock()
		record := store.sessions[sessionDigest]
		record.ActiveOrganizationSessionRev = 2
		store.sessions[sessionDigest] = record
		store.mu.Unlock()
		close(mutated)
	}()
	select {
	case <-mutated:
		t.Fatal("session authority mutated while the W5 final-use callback was held")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-resolved; err != nil {
		t.Fatalf("held authority callback failed: %v", err)
	}
	select {
	case <-mutated:
	case <-time.After(time.Second):
		t.Fatal("session authority writer did not resume after the W5 callback")
	}
	stale := httptest.NewRecorder()
	strideE10W5ProductHandler(stale, request.Clone(request.Context()))
	if stale.Code != http.StatusNotFound || len(custody.calls) != 1 {
		t.Fatalf("stale session reached custody: status=%d calls=%v", stale.Code, custody.calls)
	}
}
