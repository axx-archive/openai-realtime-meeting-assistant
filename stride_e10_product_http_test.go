package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type strideE10TestFeatures map[STRIDEFeature]bool

func (f strideE10TestFeatures) Enabled(feature STRIDEFeature) bool { return f[feature] }

type strideE10BackendFunc func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error)

func (f strideE10BackendFunc) Execute(ctx context.Context, p StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
	return f(ctx, p, c)
}

func strideE10TestPrincipal() StrideE10ProductPrincipal {
	return StrideE10ProductPrincipal{
		PersonID: "person-1", ActiveOrganizationID: "org-1",
		OrganizationMembershipID: "membership-1", OrganizationMembershipRev: 4,
		ActiveOrganizationSessionRev: 7,
	}
}

func strideE10TestHandler(features strideE10TestFeatures, backend StrideE10ProductBackend) http.Handler {
	return NewStrideE10ProductHTTP(func(*http.Request) (StrideE10ProductPrincipal, error) {
		return strideE10TestPrincipal(), nil
	}, features, backend)
}

func strideE10Request(method, path, key string, body any) *http.Request {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return req
}

func TestStrideE10ProductHTTPDefaultOffAcrossSurfaces(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/identity/v1/me/profile"},
		{http.MethodGet, "/api/organizations"},
		{http.MethodGet, "/api/work-record/v1/me"},
		{http.MethodGet, "/api/organizations/org-1/contribution-approvals"},
		{http.MethodGet, "/api/network/v1/me/profile"},
		{http.MethodGet, "/api/network/v1/contacts"},
	}
	var calls atomic.Int64
	h := strideE10TestHandler(strideE10TestFeatures{}, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
		calls.Add(1)
		return nil, false, nil
	}))
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, strideE10Request(tc.method, tc.path, "", nil))
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("backend calls = %d, want 0", got)
	}
}

func TestStrideE10ProductHTTPCoworkerSelectorIsExactAndOpaque(t *testing.T) {
	features := strideE10TestFeatures{STRIDEFeaturePersonProfileAuthority: true, STRIDEFeatureOrganizationAuthorityRead: true}
	var calls atomic.Int64
	h := strideE10TestHandler(features, strideE10BackendFunc(func(_ context.Context, _ StrideE10ProductPrincipal, command StrideE10ProductCommand) (any, bool, error) {
		calls.Add(1)
		if command.ResourceID != "coworker-profile" || command.TargetID != "person-2" {
			t.Fatalf("command resource=%q target=%q", command.ResourceID, command.TargetID)
		}
		return map[string]any{"availability": "available", "surface": "coworker-profile", "revision": 1, "items": []any{map[string]any{"id": "person-2", "title": "Coworker", "kind": "coworker-profile-detail", "detail": map[string]any{"kind": "coworker-profile-detail", "displayName": "Coworker", "role": "member", "joinedAt": "2026-08-08T12:00:00Z"}}}}, false, nil
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/coworker-profile?person=person-2", "", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("valid selector status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, path := range []string{"/api/stride/v1/mobile/surfaces/coworker-profile", "/api/stride/v1/mobile/surfaces/coworker-profile?person=person-2&extra=x", "/api/stride/v1/mobile/surfaces/profile?person=person-2"} {
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, strideE10Request(http.MethodGet, path, "", nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("unsafe selector %q status=%d body=%s", path, rr.Code, rr.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("backend calls=%d want 1", calls.Load())
	}
	wrong := strideE10TestHandler(features, strideE10BackendFunc(func(_ context.Context, _ StrideE10ProductPrincipal, _ StrideE10ProductCommand) (any, bool, error) {
		return map[string]any{"availability": "available", "surface": "coworker-profile", "revision": 1, "items": []any{map[string]any{"id": "person-other", "title": "Wrong coworker"}}}, false, nil
	}))
	rr = httptest.NewRecorder()
	wrong.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/coworker-profile?person=person-2", "", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("mismatched target status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestStrideE10ProductHTTPRouteSurface(t *testing.T) {
	cases := []struct {
		method, path, operation string
	}{
		{http.MethodGet, "/api/identity/v1/me/profile", "identity.self_profile"},
		{http.MethodPatch, "/api/identity/v1/me/profile", "identity.self_profile"},
		{http.MethodGet, "/api/identity/v1/people/person-2", "identity.coworker_profile"},
		{http.MethodPost, "/api/organizations", "organizations.collection"},
		{http.MethodPatch, "/api/organizations/org-1/members/membership-1/profile", "organizations.member_profile"},
		{http.MethodPost, "/api/organizations/org-2/join-requests", "organizations.join_requests"},
		{http.MethodGet, "/api/organizations/org-1/join-requests", "organizations.join_requests"},
		{http.MethodDelete, "/api/organizations/org-2/join-requests/request-1", "organizations.close_join_request"},
		{http.MethodPost, "/api/organizations/org-1/join-requests/request-1/decision", "organizations.decide_join_request"},
		{http.MethodPost, "/api/organizations/org-1/members/membership-2/role", "organizations.change_member_role"},
		{http.MethodDelete, "/api/organizations/org-1/members/membership-2", "organizations.revoke_member"},
		{http.MethodPost, "/api/organizations/org-1/ownership-transfer", "organizations.transfer_ownership"},
		{http.MethodGet, "/api/organizations/org-1/audit", "organizations.audit"},
		{http.MethodPost, "/api/organizations/org-1/join-requests/request-1/expire", "organizations.expire_join_request"},
		{http.MethodPost, "/api/organizations/org-1/leave", "organizations.leave"},
		{http.MethodPost, "/api/session/active-organization/server-membership-2", "session.switch_organization"},
		{http.MethodGet, "/api/work-record/v1/me", "work_record.self"},
		{http.MethodPost, "/api/contributions/v1/claims/claim-1/subject-approve", "contributions.subject_review"},
		{http.MethodPost, "/api/contributions/v1/claims/claim-1/subject-dispute", "contributions.subject_review"},
		{http.MethodPost, "/api/contributions/v1/claims/claim-1/publication", "contributions.publish"},
		{http.MethodPost, "/api/contributions/v1/publications/publication-1/withdrawal", "contributions.withdraw"},
		{http.MethodPost, "/api/contributions/v1/claims/claim-1/correction", "contributions.correct"},
		{http.MethodPost, "/api/contributions/v1/claims/claim-1/revocation", "contributions.revoke"},
		{http.MethodPost, "/api/contributions/v1/approvals/approval-1/named-party-decision", "contributions.named_party_decision"},
		{http.MethodPost, "/api/contributions/v1/attestations/attestation-1/revocation", "contributions.revoke_attestation"},
		{http.MethodGet, "/api/organizations/org-1/contribution-approvals", "contributions.approvals"},
		{http.MethodGet, "/api/organizations/org-1/contribution-audit", "contributions.audit"},
		{http.MethodPost, "/api/organizations/org-1/contribution-approvals/approval-1/decision", "contributions.decide_approval"},
		{http.MethodGet, "/api/organizations/org-1/recruiting/grants", "network.recruiting_grants"},
		{http.MethodPost, "/api/organizations/org-1/recruiting/grants", "network.recruiting_grants"},
		{http.MethodPost, "/api/organizations/org-1/recruiting/grants/grant-1/revocation", "network.recruiting_grant_revoke"},
		{http.MethodGet, "/api/organizations/org-1/recruiting/audit", "network.recruiting_audit"},
		{http.MethodGet, "/api/organizations/org-1/recruiting/receipts", "network.recruiting_receipts"},
		{http.MethodGet, "/api/organizations/org-1/recruiting/limits", "network.recruiting_limits"},
		{http.MethodPatch, "/api/network/v1/me/profile", "network.profile"},
		{http.MethodPost, "/api/network/v1/me/profile/publish", "network.profile_publish"},
		{http.MethodPost, "/api/network/v1/me/profile/pause", "network.profile_pause"},
		{http.MethodPost, "/api/network/v1/me/profile/off", "network.profile_off"},
		{http.MethodPost, "/api/network/v1/me/profile/delete", "network.profile_delete"},
		{http.MethodPatch, "/api/network/v1/me/draft", "network.profile_draft"},
		{http.MethodPatch, "/api/network/v1/me/searchable-fields", "network.searchable_fields"},
		{http.MethodPost, "/api/network/v1/me/preview", "network.preview"},
		{http.MethodPost, "/api/work-record/v1/me/export", "work_record.export"},
		{http.MethodGet, "/api/work-record/v1/me/exports/export-1", "work_record.export_download"},
		{http.MethodPost, "/api/work-record/v1/me/deletion", "work_record.delete"},
		{http.MethodPost, "/api/network/v1/me/export", "network.profile_export"},
		{http.MethodGet, "/api/network/v1/me/exports/export-1", "network.profile_export_download"},
		{http.MethodPost, "/api/network/v1/search", "network.search"},
		{http.MethodPost, "/api/network/v1/contacts", "network.contacts"},
		{http.MethodPost, "/api/network/v1/contacts/contact-1/decision", "network.decide_contact"},
		{http.MethodPut, "/api/network/v1/blocks/person-2", "network.block"},
	}
	features := strideE10TestFeatures{
		STRIDEFeaturePersonProfileAuthority: true, STRIDEFeatureOrganizationAuthorityRead: true,
		STRIDEFeatureOrganizationAuthorityWrite: true, STRIDEFeatureActiveOrganizationSession: true,
		STRIDEFeatureContributionReview: true, STRIDEFeatureWorkRecordPrivate: true,
		STRIDEFeatureNetworkProfilePublication: true, STRIDEFeatureNetworkProjectionShadow: true,
		STRIDEFeatureNetworkSearch: true, STRIDEFeatureNetworkContact: true,
	}
	var mu sync.Mutex
	var operations []string
	h := strideE10TestHandler(features, strideE10BackendFunc(func(_ context.Context, _ StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
		mu.Lock()
		operations = append(operations, c.Operation)
		mu.Unlock()
		return nil, false, nil
	}))
	for _, tc := range cases {
		t.Run(tc.operation+tc.method, func(t *testing.T) {
			var key string
			var body any
			if tc.method == http.MethodPost || tc.method == http.MethodPatch || tc.method == http.MethodPut || tc.method == http.MethodDelete {
				key, body = "key-"+tc.operation, map[string]any{"expectedRevision": 0}
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, strideE10Request(tc.method, tc.path, key, body))
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			mu.Lock()
			got := operations[len(operations)-1]
			mu.Unlock()
			if got != tc.operation {
				t.Fatalf("operation=%q, want %q", got, tc.operation)
			}
		})
	}
}

func strideE10MobileProjection(surface string) map[string]any {
	return map[string]any{
		"availability": "available", "surface": surface, "revision": 2,
		"items": []any{map[string]any{"id": "item-1", "title": "Safe title"}},
	}
}

func strideE10MobileTestValues(action string) map[string]any {
	switch action {
	case "profile-update":
		return map[string]any{"displayName": "Ada"}
	case "organization-create":
		return map[string]any{"name": "Acme", "slug": "acme"}
	case "organization-join":
		return map[string]any{"joinCode": "join-code"}
	case "network-draft-save":
		return map[string]any{"intro": "Builder"}
	case "network-search-propose":
		return map[string]any{"query": "systems lead"}
	case "contact-send", "exact-link-contact-send":
		return map[string]any{"purpose": "discuss_work", "note": "Hello", "collaborationType": "collaboration"}
	case "organization-member-role-change":
		return map[string]any{"role": "member"}
	case "network-searchable-fields-update":
		return map[string]any{"fields": []string{"display_name"}}
	case "contribution-named-party-decision":
		return map[string]any{"decision": "approved", "reason": "Reviewed"}
	default:
		return map[string]any{}
	}
}

func TestStrideE10ProductHTTPMobileSurfaceParityAndRawShape(t *testing.T) {
	features := strideE10TestFeatures{
		STRIDEFeaturePersonProfileAuthority: true, STRIDEFeatureOrganizationAuthorityRead: true,
		STRIDEFeatureContributionReview: true, STRIDEFeatureWorkRecordPrivate: true,
		STRIDEFeatureNetworkProfilePublication: true, STRIDEFeatureNetworkProjectionShadow: true,
		STRIDEFeatureNetworkSearch: true, STRIDEFeatureNetworkContact: true,
	}
	surfaces := []string{"profile", "work-record", "network-draft", "organizations", "organization-people", "organization-requests", "contribution-approvals", "network-preview", "network-recruiter-view", "network-search", "contact-inbox", "network-blocks"}
	backend := strideE10BackendFunc(func(_ context.Context, p StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
		if p.ActiveOrganizationID != "org-1" || c.OrganizationID != "org-1" {
			t.Fatalf("active organization was not server-derived: principal=%+v command=%+v", p, c)
		}
		return strideE10MobileProjection(c.ResourceID), false, nil
	})
	h := strideE10TestHandler(features, backend)
	for _, surface := range surfaces {
		t.Run(surface, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/"+surface, "", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["surface"] != surface || payload["availability"] != "available" || payload["data"] != nil {
				t.Fatalf("mobile payload is not raw closed projection: %#v", payload)
			}
		})
	}
}

func TestStrideE10ProductHTTPMobileClosedActionParity(t *testing.T) {
	features := strideE10TestFeatures{
		STRIDEFeatureOrganizationAuthorityRead: true, STRIDEFeatureOrganizationAuthorityWrite: true,
		STRIDEFeatureActiveOrganizationSession: true, STRIDEFeatureNetworkProfilePublication: true,
		STRIDEFeatureNetworkContact: true, STRIDEFeaturePersonProfileAuthority: true,
		STRIDEFeatureWorkRecordPrivate: true, STRIDEFeatureContributionReview: true,
		STRIDEFeatureNetworkProjectionShadow: true, STRIDEFeatureNetworkSearch: true,
	}
	actions := map[string]string{
		"organization-create": "organizations", "organization-join": "organizations",
		"organization-request-approve": "organization-requests", "organization-request-deny": "organization-requests",
		"organization-switch": "organizations", "organization-leave": "organizations",
		"network-publish": "network-preview", "network-pause": "network-preview",
		"contact-accept": "contact-inbox", "contact-decline": "contact-inbox", "contact-withdraw": "contact-inbox",
		"network-block": "network-blocks", "network-unblock": "network-blocks",
		"profile-update": "profile", "network-draft-save": "network-draft",
		"contribution-subject-approve": "work-record", "contribution-subject-dispute": "work-record",
		"contribution-organization-approve": "contribution-approvals", "contribution-organization-deny": "contribution-approvals",
		"contribution-named-party-decision": "contribution-approvals", "contribution-attestation-revoke": "contribution-approvals",
		"contribution-publish": "work-record", "contribution-withdraw": "work-record",
		"network-search-propose": "network-search", "network-search-confirm": "network-search", "contact-send": "network-search", "exact-link-contact-send": "network-recruiter-view",
	}
	backend := strideE10BackendFunc(func(_ context.Context, p StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
		if p.ActiveOrganizationID != "org-1" || c.OrganizationID != "org-1" || c.ExpectedRevision != 3 || c.IdempotencyKey == "" {
			t.Fatalf("mobile authority was not server/CAS derived: principal=%+v command=%+v", p, c)
		}
		var envelope map[string]any
		_ = json.Unmarshal(c.Body, &envelope)
		return strideE10MobileProjection(envelope["surface"].(string)), false, nil
	})
	h := strideE10TestHandler(features, backend)
	for action, surface := range actions {
		t.Run(action, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/server-action-1", "mobile-key-"+action, map[string]any{"action": action, "expectedRevision": 3, "surface": surface, "values": strideE10MobileTestValues(action)}))
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestStrideE10ProductHTTPPrivateDraftIndependentFromPublicationAndSearch(t *testing.T) {
	features := strideE10TestFeatures{STRIDEFeatureWorkRecordPrivate: true}
	var calls atomic.Int64
	h := strideE10TestHandler(features, strideE10BackendFunc(func(_ context.Context, _ StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
		calls.Add(1)
		surface := c.ResourceID
		if c.Method == http.MethodPost {
			surface = "network-preview"
		}
		return strideE10MobileProjection(surface), false, nil
	}))
	draft := httptest.NewRecorder()
	h.ServeHTTP(draft, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-draft", "", nil))
	if draft.Code != http.StatusOK {
		t.Fatalf("private draft status=%d body=%s", draft.Code, draft.Body.String())
	}
	preview := httptest.NewRecorder()
	h.ServeHTTP(preview, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-preview", "", nil))
	if preview.Code != http.StatusOK {
		t.Fatalf("private preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	search := httptest.NewRecorder()
	h.ServeHTTP(search, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/network-search", "", nil))
	if search.Code != http.StatusServiceUnavailable {
		t.Fatalf("network-search status=%d, want 503", search.Code)
	}
	for _, action := range []string{"network-pause", "network-profile-delete"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/action-"+action, "key-"+action, map[string]any{"action": action, "expectedRevision": 2, "surface": "network-preview", "values": map[string]any{}}))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", action, rr.Code, rr.Body.String())
		}
	}
	publish := httptest.NewRecorder()
	h.ServeHTTP(publish, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/action-publish", "key-publish", map[string]any{"action": "network-publish", "expectedRevision": 2, "surface": "network-preview", "values": map[string]any{}}))
	if publish.Code != http.StatusServiceUnavailable {
		t.Fatalf("publish status=%d, want 503", publish.Code)
	}
	if calls.Load() != 4 {
		t.Fatalf("backend calls=%d, want draft+preview+pause+delete", calls.Load())
	}
}

func TestStrideE10ProductHTTPAdvertisedMobileDependenciesFailIndependently(t *testing.T) {
	for surface, spec := range strideE10MobileSurfaces {
		for _, disabled := range spec.features {
			t.Run("surface/"+surface+"/"+string(disabled), func(t *testing.T) {
				features := strideE10TestFeatures{}
				for _, feature := range spec.features {
					features[feature] = true
				}
				features[disabled] = false
				var calls atomic.Int64
				h := strideE10TestHandler(features, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
					calls.Add(1)
					return nil, false, nil
				}))
				path := "/api/stride/v1/mobile/surfaces/" + surface
				if surface == "coworker-profile" {
					path += "?person=person-2"
				}
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, strideE10Request(http.MethodGet, path, "", nil))
				if rr.Code != http.StatusServiceUnavailable || calls.Load() != 0 {
					t.Fatalf("status=%d calls=%d", rr.Code, calls.Load())
				}
			})
		}
	}
	for action, spec := range strideE10MobileActions {
		for _, disabled := range spec.features {
			t.Run("action/"+action+"/"+string(disabled), func(t *testing.T) {
				features := strideE10TestFeatures{}
				for _, feature := range spec.features {
					features[feature] = true
				}
				features[disabled] = false
				var calls atomic.Int64
				h := strideE10TestHandler(features, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
					calls.Add(1)
					return nil, false, nil
				}))
				surface := strideE10MobileActionSurfaces[action]
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/action-dependency", "dependency-key", map[string]any{"action": action, "expectedRevision": 2, "surface": surface, "values": strideE10MobileTestValues(action)}))
				if rr.Code != http.StatusServiceUnavailable || calls.Load() != 0 {
					t.Fatalf("status=%d calls=%d", rr.Code, calls.Load())
				}
			})
		}
	}
}

func TestStrideE10ProductHTTPContributionPublishRequiresPublicationButWithdrawalDoesNot(t *testing.T) {
	features := strideE10TestFeatures{STRIDEFeatureWorkRecordPrivate: true}
	var operationsMu sync.Mutex
	var operations []string
	backend := strideE10BackendFunc(func(_ context.Context, _ StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
		operationsMu.Lock()
		operations = append(operations, c.Operation)
		operationsMu.Unlock()
		return strideE10MobileProjection("work-record"), false, nil
	})
	h := strideE10TestHandler(features, backend)
	publish := httptest.NewRecorder()
	h.ServeHTTP(publish, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/publish-action", "publish-key", map[string]any{
		"action": "contribution-publish", "expectedRevision": 1, "surface": "work-record", "values": map[string]any{},
	}))
	if publish.Code != http.StatusServiceUnavailable {
		t.Fatalf("publish status=%d, want 503; body=%s", publish.Code, publish.Body.String())
	}
	if len(operations) != 0 {
		t.Fatalf("publication-disabled publish called backend: %v", operations)
	}

	withdraw := httptest.NewRecorder()
	h.ServeHTTP(withdraw, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/withdraw-action", "withdraw-key", map[string]any{
		"action": "contribution-withdraw", "expectedRevision": 2, "surface": "work-record", "values": map[string]any{},
	}))
	if withdraw.Code != http.StatusOK {
		t.Fatalf("withdraw status=%d, want 200; body=%s", withdraw.Code, withdraw.Body.String())
	}
	if len(operations) != 1 || operations[0] != "contributions.withdraw" {
		t.Fatalf("withdraw operations=%v", operations)
	}
}

func TestStrideE10ProductHTTPExactLinkContactIsIndependentOfSearchAndConfirmIsClosed(t *testing.T) {
	features := strideE10TestFeatures{STRIDEFeatureNetworkProfilePublication: true, STRIDEFeatureNetworkProjectionShadow: true, STRIDEFeatureNetworkContact: true}
	var calls atomic.Int64
	h := strideE10TestHandler(features, strideE10BackendFunc(func(_ context.Context, _ StrideE10ProductPrincipal, command StrideE10ProductCommand) (any, bool, error) {
		calls.Add(1)
		return strideE10MobileProjection("network-recruiter-view"), false, nil
	}))
	exact := httptest.NewRecorder()
	h.ServeHTTP(exact, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/exact-link-action", "exact-link-key", map[string]any{"action": "exact-link-contact-send", "expectedRevision": 1, "surface": "network-recruiter-view", "values": strideE10MobileTestValues("exact-link-contact-send")}))
	if exact.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("exact-link contact depended on search: status=%d calls=%d body=%s", exact.Code, calls.Load(), exact.Body.String())
	}
	searchContact := httptest.NewRecorder()
	h.ServeHTTP(searchContact, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/search-contact-action", "search-contact-key", map[string]any{"action": "contact-send", "expectedRevision": 1, "surface": "network-search", "values": strideE10MobileTestValues("contact-send")}))
	if searchContact.Code != http.StatusServiceUnavailable || calls.Load() != 1 {
		t.Fatalf("search-result contact bypassed search gate: status=%d calls=%d", searchContact.Code, calls.Load())
	}
	closedConfirm := httptest.NewRecorder()
	h.ServeHTTP(closedConfirm, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/confirm-action", "confirm-key", map[string]any{"action": "network-search-confirm", "expectedRevision": 1, "surface": "network-search", "values": map[string]any{"filters": []any{}}}))
	if closedConfirm.Code != http.StatusBadRequest || calls.Load() != 1 {
		t.Fatalf("closed confirm body must fail before feature admission: status=%d calls=%d", closedConfirm.Code, calls.Load())
	}
	features[STRIDEFeatureNetworkSearch] = true
	closedConfirm = httptest.NewRecorder()
	h.ServeHTTP(closedConfirm, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/confirm-action", "confirm-key", map[string]any{"action": "network-search-confirm", "expectedRevision": 1, "surface": "network-search", "values": map[string]any{"filters": []any{}}}))
	if closedConfirm.Code != http.StatusBadRequest || calls.Load() != 1 {
		t.Fatalf("client filters reached confirm: status=%d calls=%d", closedConfirm.Code, calls.Load())
	}
}

func TestStrideE10ProductHTTPMobileHardNegatives(t *testing.T) {
	all := strideE10TestFeatures{
		STRIDEFeatureOrganizationAuthorityWrite: true,
		STRIDEFeatureNetworkProfilePublication:  true,
		STRIDEFeatureNetworkProjectionShadow:    true,
	}
	var calls atomic.Int64
	backend := strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
		calls.Add(1)
		return strideE10MobileProjection("organizations"), false, nil
	})
	h := strideE10TestHandler(all, backend)
	tests := []struct {
		name, path, key string
		body            any
		want            int
	}{
		{"unknown surface", "/api/stride/v1/mobile/surfaces/admin-secrets", "", nil, http.StatusNotFound},
		{"unknown action", "/api/stride/v1/mobile/actions/a1", "key", map[string]any{"action": "make-admin", "expectedRevision": 1, "surface": "organizations", "values": map[string]any{}}, http.StatusBadRequest},
		{"authority field", "/api/stride/v1/mobile/actions/a1", "key", map[string]any{"action": "organization-create", "expectedRevision": 1, "surface": "organizations", "organizationId": "org-attacker"}, http.StatusBadRequest},
		{"surface mismatch", "/api/stride/v1/mobile/actions/a1", "key", map[string]any{"action": "network-publish", "expectedRevision": 1, "surface": "organizations", "values": map[string]any{}}, http.StatusBadRequest},
		{"revision zero", "/api/stride/v1/mobile/actions/a1", "key", map[string]any{"action": "organization-create", "expectedRevision": 0, "surface": "organizations", "values": strideE10MobileTestValues("organization-create")}, http.StatusBadRequest},
		{"missing key", "/api/stride/v1/mobile/actions/a1", "", map[string]any{"action": "organization-create", "expectedRevision": 1, "surface": "organizations", "values": strideE10MobileTestValues("organization-create")}, http.StatusBadRequest},
		{"unknown value", "/api/stride/v1/mobile/actions/a1", "key", map[string]any{"action": "profile-update", "expectedRevision": 1, "surface": "profile", "values": map[string]any{"displayName": "Ada", "email": "private@example.com"}}, http.StatusBadRequest},
		{"nested authority", "/api/stride/v1/mobile/actions/a1", "key", map[string]any{"action": "contribution-subject-dispute", "expectedRevision": 1, "surface": "work-record", "values": map[string]any{"reason": map[string]any{"personId": "attacker"}}}, http.StatusBadRequest},
		{"oversized value", "/api/stride/v1/mobile/actions/a1", "key", map[string]any{"action": "network-search-propose", "expectedRevision": 1, "surface": "network-search", "values": map[string]any{"query": string(make([]byte, 501))}}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			method := http.MethodPost
			if tc.body == nil {
				method = http.MethodGet
			}
			h.ServeHTTP(rr, strideE10Request(method, tc.path, tc.key, tc.body))
			if rr.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("backend calls=%d, want 0", calls.Load())
	}
}

func TestStrideE10ProductHTTPMobileRejectsUnsafeBackendProjection(t *testing.T) {
	h := strideE10TestHandler(strideE10TestFeatures{STRIDEFeaturePersonProfileAuthority: true}, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
		return map[string]any{"availability": "available", "surface": "profile", "revision": 1, "items": []any{map[string]any{"id": "p1", "title": "Ada", "email": "private@example.com"}}}, false, nil
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/profile", "", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("unsafe projection status=%d, want 500; body=%s", rr.Code, rr.Body.String())
	}
}

func TestStrideE10ProductHTTPMobileProjectionAdmissionHardNegatives(t *testing.T) {
	available := func() map[string]any {
		return map[string]any{
			"availability": "available", "surface": "profile", "revision": 1,
			"items": []any{map[string]any{"id": "person-1", "title": "Ada"}},
		}
	}
	cases := []struct {
		name  string
		value func() any
	}{
		{"wrong action surface", func() any {
			value := available()
			value["items"] = []any{map[string]any{"id": "person-1", "title": "Ada", "actions": []any{map[string]any{"id": "publish-1", "type": "network-publish", "label": "Publish", "expectedRevision": 1}}}}
			return value
		}},
		{"available carries reason", func() any {
			value := available()
			value["reason"] = "should not coexist"
			return value
		}},
		{"unavailable carries revision", func() any {
			return map[string]any{"availability": "unavailable", "surface": "profile", "reason": "Off", "revision": 1}
		}},
		{"unavailable carries items", func() any {
			return map[string]any{"availability": "unavailable", "surface": "profile", "reason": "Off", "items": []any{}}
		}},
		{"unavailable missing reason", func() any {
			return map[string]any{"availability": "unavailable", "surface": "profile"}
		}},
		{"oversized unavailable reason", func() any {
			return map[string]any{"availability": "unavailable", "surface": "profile", "reason": strings.Repeat("r", 241)}
		}},
		{"oversized item id", func() any {
			value := available()
			value["items"] = []any{map[string]any{"id": strings.Repeat("i", 161), "title": "Ada"}}
			return value
		}},
		{"oversized item title", func() any {
			value := available()
			value["items"] = []any{map[string]any{"id": "person-1", "title": strings.Repeat("t", 241)}}
			return value
		}},
		{"oversized optional string", func() any {
			value := available()
			value["items"] = []any{map[string]any{"id": "person-1", "title": "Ada", "summary": strings.Repeat("s", 501)}}
			return value
		}},
		{"oversized action label", func() any {
			value := available()
			value["items"] = []any{map[string]any{"id": "person-1", "title": "Ada", "actions": []any{map[string]any{"id": "update-1", "type": "profile-update", "label": strings.Repeat("l", 121), "expectedRevision": 1}}}}
			return value
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := strideE10TestHandler(strideE10TestFeatures{STRIDEFeaturePersonProfileAuthority: true}, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
				return tc.value(), false, nil
			}))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/profile", "", nil))
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d, want 500; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestStrideE10ProductHTTPMobileAcceptsExactUnavailableProjection(t *testing.T) {
	h := strideE10TestHandler(strideE10TestFeatures{STRIDEFeaturePersonProfileAuthority: true}, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
		return map[string]any{"availability": "unavailable", "surface": "profile", "reason": "This account is not enabled."}, false, nil
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/stride/v1/mobile/surfaces/profile", "", nil))
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), `"data"`) {
		t.Fatalf("exact unavailable projection status/body=%d/%s", rr.Code, rr.Body.String())
	}
}

func TestStrideE10ProductHTTPRequiresEveryParentDependency(t *testing.T) {
	features := strideE10TestFeatures{
		STRIDEFeatureNetworkProfilePublication: true,
		STRIDEFeatureNetworkSearch:             true,
		// projection shadow deliberately remains false
	}
	var calls atomic.Int64
	h := strideE10TestHandler(features, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
		calls.Add(1)
		return nil, false, nil
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, strideE10Request(http.MethodPost, "/api/network/v1/search", "search-1", map[string]any{"expectedRevision": 0}))
	if rr.Code != http.StatusServiceUnavailable || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d; want 503/0", rr.Code, calls.Load())
	}
}

func TestStrideE10ProductHTTPOpaqueCrossTenantAndIncompleteSession(t *testing.T) {
	features := strideE10TestFeatures{STRIDEFeatureOrganizationAuthorityRead: true}
	var calls atomic.Int64
	backend := strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
		calls.Add(1)
		return nil, false, nil
	})

	t.Run("cross tenant", func(t *testing.T) {
		h := strideE10TestHandler(features, backend)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/organizations/org-2/join-requests", "", nil))
		if rr.Code != http.StatusNotFound || rr.Body.String() != "{\"error\":{\"code\":\"not_found\"}}\n" {
			t.Fatalf("unexpected opaque response: %d %q", rr.Code, rr.Body.String())
		}
	})
	t.Run("incomplete legacy session", func(t *testing.T) {
		h := NewStrideE10ProductHTTP(func(*http.Request) (StrideE10ProductPrincipal, error) {
			return StrideE10ProductPrincipal{PersonID: "person-1", ActiveOrganizationID: "org-1"}, nil
		}, features, backend)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/organizations/org-1/join-requests", "", nil))
		if rr.Code != http.StatusNotFound || rr.Body.String() != "{\"error\":{\"code\":\"not_found\"}}\n" {
			t.Fatalf("unexpected opaque response: %d %q", rr.Code, rr.Body.String())
		}
	})
	if calls.Load() != 0 {
		t.Fatalf("backend calls = %d, want 0", calls.Load())
	}
}

func TestStrideE10ProductHTTPDerivesPrincipalAndRequiresCASIdempotency(t *testing.T) {
	features := strideE10TestFeatures{STRIDEFeaturePersonProfileAuthority: true}
	var capturedPrincipal StrideE10ProductPrincipal
	var capturedCommand StrideE10ProductCommand
	h := strideE10TestHandler(features, strideE10BackendFunc(func(_ context.Context, p StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
		capturedPrincipal, capturedCommand = p, c
		return map[string]any{"revision": 10}, false, nil
	}))

	for _, tc := range []struct {
		name, key string
		body      any
	}{
		{"missing idempotency", "", map[string]any{"expectedRevision": 9}},
		{"missing revision", "key-1", map[string]any{"displayName": "Ada"}},
		{"negative revision", "key-1", map[string]any{"expectedRevision": -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, strideE10Request(http.MethodPatch, "/api/identity/v1/me/profile", tc.key, tc.body))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400", rr.Code)
			}
		})
	}

	for _, body := range []any{
		map[string]any{"expectedRevision": 9, "personId": "attacker", "displayName": "Ada"},
		map[string]any{"expectedRevision": 9, "profile": map[string]any{"metadata": map[string]any{"organization_id": "org-attacker"}}},
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, strideE10Request(http.MethodPatch, "/api/identity/v1/me/profile", "authority-key", body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("authority-bearing body status=%d, want 400; body=%s", rr.Code, rr.Body.String())
		}
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, strideE10Request(http.MethodPatch, "/api/identity/v1/me/profile", "key-2", map[string]any{
		"expectedRevision": 9, "displayName": "Ada",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if capturedPrincipal.PersonID != "person-1" || capturedCommand.ExpectedRevision != 9 || capturedCommand.IdempotencyKey != "key-2" {
		t.Fatalf("incorrect derived command: principal=%+v command=%+v", capturedPrincipal, capturedCommand)
	}
}

func TestStrideE10ProductHTTPMobileChooserAndRecipientActionsNeedNoActiveOrganization(t *testing.T) {
	features := strideE10TestFeatures{
		STRIDEFeatureOrganizationAuthorityRead: true,
		STRIDEFeatureActiveOrganizationSession: true,
		STRIDEFeatureNetworkContact:            true,
	}
	backend := strideE10BackendFunc(func(_ context.Context, p StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
		if p.PersonID != "person-1" || p.ActiveOrganizationID != "" || c.OrganizationID != "" {
			t.Fatalf("unexpected authority: principal=%+v command=%+v", p, c)
		}
		var envelope map[string]any
		_ = json.Unmarshal(c.Body, &envelope)
		return strideE10MobileProjection(envelope["surface"].(string)), false, nil
	})
	h := NewStrideE10ProductHTTP(func(*http.Request) (StrideE10ProductPrincipal, error) {
		return StrideE10ProductPrincipal{PersonID: "person-1"}, nil
	}, features, backend)
	for _, tc := range []struct{ action, surface string }{
		{"organization-switch", "organizations"},
		{"contact-accept", "contact-inbox"},
		{"contact-decline", "contact-inbox"},
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/server-action", "key-"+tc.action, map[string]any{"action": tc.action, "expectedRevision": 1, "surface": tc.surface, "values": map[string]any{}}))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", tc.action, rr.Code, rr.Body.String())
		}
	}
}

func TestStrideE10ProductHTTPMobileRejectsCrossControllerOrganizationDecisions(t *testing.T) {
	features := strideE10TestFeatures{
		STRIDEFeatureOrganizationAuthorityRead:  true,
		STRIDEFeatureOrganizationAuthorityWrite: true,
		STRIDEFeatureContributionReview:         true,
	}
	var calls atomic.Int64
	h := NewStrideE10ProductHTTP(func(*http.Request) (StrideE10ProductPrincipal, error) {
		return StrideE10ProductPrincipal{PersonID: "person-1"}, nil
	}, features, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
		calls.Add(1)
		return nil, false, nil
	}))
	for _, action := range []string{"contribution-organization-approve", "contribution-organization-deny"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, strideE10Request(http.MethodPost, "/api/stride/v1/mobile/actions/foreign-org-action", "key-"+action, map[string]any{
			"action": action, "expectedRevision": 1, "surface": "contribution-approvals", "values": map[string]any{},
		}))
		if rr.Code != http.StatusNotFound || rr.Body.String() != "{\"error\":{\"code\":\"not_found\"}}\n" {
			t.Fatalf("%s cross-controller response=%d/%q", action, rr.Code, rr.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("backend calls=%d, want 0", calls.Load())
	}
}

func TestStrideE10ProductHTTPErrorDisclosure(t *testing.T) {
	features := strideE10TestFeatures{STRIDEFeaturePersonProfileAuthority: true}
	request := func(err error) *httptest.ResponseRecorder {
		h := strideE10TestHandler(features, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
			return nil, false, err
		}))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/identity/v1/me/profile", "", nil))
		return rr
	}
	denied, missing := request(ErrStrideE10Denied), request(ErrStrideE10NotFound)
	if denied.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || denied.Body.String() != missing.Body.String() {
		t.Fatalf("denied and missing must be indistinguishable: denied=%d/%q missing=%d/%q", denied.Code, denied.Body.String(), missing.Code, missing.Body.String())
	}
	if conflict := request(ErrStrideE10Conflict); conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d, want 409", conflict.Code)
	}
}

type strideE10IdempotentBackend struct {
	mu      sync.Mutex
	seen    map[string][32]byte
	commits atomic.Int64
}

func (b *strideE10IdempotentBackend) Execute(_ context.Context, _ StrideE10ProductPrincipal, c StrideE10ProductCommand) (any, bool, error) {
	digest := sha256.Sum256(c.Body)
	key := c.Operation + "\x00" + c.IdempotencyKey
	b.mu.Lock()
	defer b.mu.Unlock()
	if prior, ok := b.seen[key]; ok {
		if prior != digest {
			return nil, false, ErrStrideE10Conflict
		}
		return map[string]any{"revision": 2}, true, nil
	}
	b.seen[key] = digest
	b.commits.Add(1)
	return map[string]any{"revision": 2}, false, nil
}

func TestStrideE10ProductHTTPConcurrentIdempotency(t *testing.T) {
	backend := &strideE10IdempotentBackend{seen: make(map[string][32]byte)}
	h := strideE10TestHandler(strideE10TestFeatures{STRIDEFeaturePersonProfileAuthority: true}, backend)
	const workers = 32
	var wg sync.WaitGroup
	statuses := make(chan int, workers)
	replays := make(chan bool, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, strideE10Request(http.MethodPatch, "/api/identity/v1/me/profile", "same-key", map[string]any{"expectedRevision": 1, "displayName": "Ada"}))
			statuses <- rr.Code
			replays <- rr.Header().Get("Idempotency-Replayed") == "true"
		}()
	}
	wg.Wait()
	close(statuses)
	close(replays)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("status=%d, want 200", status)
		}
	}
	replayed := 0
	for replay := range replays {
		if replay {
			replayed++
		}
	}
	if backend.commits.Load() != 1 || replayed != workers-1 {
		t.Fatalf("commits=%d replayed=%d, want 1/%d", backend.commits.Load(), replayed, workers-1)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, strideE10Request(http.MethodPatch, "/api/identity/v1/me/profile", "same-key", map[string]any{"expectedRevision": 1, "displayName": "Grace"}))
	if rr.Code != http.StatusConflict {
		t.Fatalf("same key/different body status=%d, want 409", rr.Code)
	}
}

func TestStrideE10ProductHTTPUnauthorizedResolver(t *testing.T) {
	h := NewStrideE10ProductHTTP(func(*http.Request) (StrideE10ProductPrincipal, error) {
		return StrideE10ProductPrincipal{}, errors.New("no session")
	}, strideE10TestFeatures{}, strideE10BackendFunc(func(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (any, bool, error) {
		t.Fatal("backend must not be called")
		return nil, false, nil
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, strideE10Request(http.MethodGet, "/api/identity/v1/me/profile", "", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rr.Code)
	}
}
