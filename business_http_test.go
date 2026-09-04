package main

import (
	"context"
	"encoding/json"
	"github.com/openai/openai-realtime-meeting-assistant/internal/business"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type businessHTTPFake struct {
	actor      business.Actor
	setupCalls int
	role       string
}

func (f *businessHTTPFake) ListOrganizations(_ context.Context, a business.Actor) ([]business.Organization, error) {
	f.actor = a
	return []business.Organization{{ID: "org_own", Name: "Private"}}, nil
}
func (f *businessHTTPFake) GetMembership(_ context.Context, s business.Scope) (business.Membership, error) {
	return business.Membership{Role: f.role, Status: "active"}, nil
}
func (f *businessHTTPFake) ListBusinesses(_ context.Context, s business.Scope) ([]business.Business, error) {
	return []business.Business{}, nil
}
func (f *businessHTTPFake) GetBusiness(_ context.Context, s business.Scope, id string) (business.Business, error) {
	if s.OrganizationID != "org_own" || id != "biz_own" {
		return business.Business{}, business.ErrNotFound
	}
	return business.Business{ID: id, OrganizationID: s.OrganizationID, Name: "Private", Revision: 1, Status: "draft"}, nil
}
func (f *businessHTTPFake) GetBudget(context.Context, business.Scope, string) (business.Budget, error) {
	return business.Budget{FundedMicros: 1000, CapMicros: 500, ReservedMicros: 20}, nil
}
func (f *businessHTTPFake) SetupBusiness(_ context.Context, a business.Actor, in business.SetupBusinessArgs) (business.SetupBusinessResult, error) {
	f.actor = a
	f.setupCalls++
	return business.SetupBusinessResult{Organization: business.Organization{ID: "org_own"}, Business: business.Business{ID: "biz_own"}}, nil
}
func (f *businessHTTPFake) UpdateBusinessAction(context.Context, business.Scope, business.BusinessAction) (business.Business, error) {
	return business.Business{}, business.ErrConflict
}
func businessHTTPTestHandler(f *businessHTTPFake) *businessHTTP {
	return &businessHTTP{store: f, authenticate: func(*http.Request) (businessViewer, error) {
		return businessViewer{"person_authenticated", "Person"}, nil
	}}
}
func TestBusinessHTTPRejectsIdentityInjectionAndCrossTenant(t *testing.T) {
	f := &businessHTTPFake{role: "owner"}
	h := businessHTTPTestHandler(f)
	for _, body := range []string{`{"actor":{"kind":"agent","id":"other"}}`, `{"organization":{"id":"org_own"},"personId":"other"}`, `{} {}`} {
		r := httptest.NewRequest("POST", businessAPIBase+"businesses", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || f.setupCalls != 0 {
			t.Fatalf("injected request accepted: %d %s", w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", businessAPIBase+"businesses/biz_foreign", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 || f.actor.ID != "person_authenticated" {
		t.Fatalf("tenant lookup %d actor=%+v", w.Code, f.actor)
	}
}
func TestBusinessHTTPDetailHasNoInventedMetricsOrAuthority(t *testing.T) {
	f := &businessHTTPFake{role: "member"}
	h := businessHTTPTestHandler(f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", businessAPIBase+"businesses/biz_own", nil))
	var out map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	b := out["budget"].(map[string]any)
	c := out["capabilities"].(map[string]any)
	if b["spentMicros"] != nil || b["unpricedCalls"] != nil || b["allowanceMicros"] != float64(500) || c["updatePolicy"] != false || c["hireAgent"] != false {
		t.Fatalf("invented data/authority: %+v", out)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private response cached")
	}
}
func TestBusinessHTTPMutationOriginUsesActualCredentialSource(t *testing.T) {
	r := httptest.NewRequest("POST", "https://example.com/api/business/v1/businesses", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "ambient"})
	r.Header.Set("Authorization", "Bearer injected")
	r.Header.Set("Origin", "https://evil.example")
	if businessMutationOriginAllowed(r) {
		t.Fatal("injected bearer bypassed cookie CSRF")
	}
	r.Header.Set("Origin", "https://example.com")
	if !businessMutationOriginAllowed(r) {
		t.Fatal("same-origin mutation rejected")
	}
	r.Header.Set("Origin", "null")
	if businessMutationOriginAllowed(r) {
		t.Fatal("opaque origin accepted")
	}
	r.Header.Set("X-Bonfire-Client", "ios")
	if !businessMutationOriginAllowed(r) {
		t.Fatal("explicit native credential rejected")
	}
}
func TestBusinessHTTPDisabledAndUnauthenticated(t *testing.T) {
	w := httptest.NewRecorder()
	h := &businessHTTP{authenticate: func(*http.Request) (businessViewer, error) { return businessViewer{}, business.ErrDenied }}
	h.ServeHTTP(w, httptest.NewRequest("GET", businessAPIBase+"context", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	h.authenticate = func(*http.Request) (businessViewer, error) { return businessViewer{"person", "Person"}, nil }
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", businessAPIBase+"context", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	if _, legacy := strideE10TenantSurfaceForHTTPPath(businessAPIBase + "context"); legacy {
		t.Fatal("Business wrapped in legacy organization authority")
	}
	if _, legacy := strideE10TenantSurfaceForHTTPPath("/api/business-adjacent/private"); !legacy {
		t.Fatal("adjacent route bypasses legacy gate")
	}
}
