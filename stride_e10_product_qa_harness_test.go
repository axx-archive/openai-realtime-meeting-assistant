package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestStrideE10ProductLiveRenderedQAHarness is an opt-in, test-binary-only
// authenticated server for rendered browser QA against the concrete W1-backed
// runtime. It is never registered by production main and is off unless an
// explicit loopback listen address is supplied.
//
// Example:
//
//	STRIDE_E10_QA_LISTEN=127.0.0.1:18999 STRIDE_E10_QA_DURATION=10m \
//	  go test -v . -run '^TestStrideE10ProductLiveRenderedQAHarness$'
//
// Then open http://127.0.0.1:18999/__qa/login.
func TestStrideE10ProductLiveRenderedQAHarness(t *testing.T) {
	listenAddress := os.Getenv("STRIDE_E10_QA_LISTEN")
	if listenAddress == "" {
		t.Skip("STRIDE_E10_QA_LISTEN is unset")
	}
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("QA harness must bind exact loopback IPv4: %q", listenAddress)
	}
	duration := 10 * time.Minute
	if raw := os.Getenv("STRIDE_E10_QA_DURATION"); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 || parsed > 30*time.Minute {
			t.Fatalf("invalid bounded QA duration %q", raw)
		}
		duration = parsed
	}

	setupAuthTestEnv(t)
	contributionFixture := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, contributionFixture)
	attestation, publication := issuePublishedContribution(t, contributionFixture, claim)
	now := contributionAuthorityTime.Add(10 * time.Minute)
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	runtime.contribution = contributionFixture.service
	if err := runtime.installNetworkPublicationDependencies(publication, []ContributionAttestation{attestation}); err != nil {
		t.Fatal(err)
	}
	profile := NetworkProfileProjection{Header: contributionNetworkHeader(STRIDEContractNetworkProfileProjection, "projection_rendered_qa", STRIDEGlobalPersonTenant), SubjectPersonID: publication.SubjectPersonID, Publication: refForHeader(publication.Header), Fields: []NetworkPublishedField{{FieldKey: "display_name", ValueDigest: sha256Hex([]byte(`"QA Contributor"`)), VisibleValue: json.RawMessage(`"QA Contributor"`), EvidenceLabel: "self_described"}, {FieldKey: "outcome", ValueDigest: attestation.ReleasedFields[0].ValueDigest, EvidenceLabel: "organization_verified_redacted", Claim: ptrSTRIDEReference(refForHeader(publication.Header))}}, Discoverability: "unlisted", Controller: contributionFixture.publisher.Controller, State: "draft", StateChangedAt: now.Add(-2 * time.Minute)}
	profile.FieldsDigest, _ = STRIDEContractDigest(profile.Fields)
	created, _, _, err := runtime.network.PutProfile(profile.Controller, profile, 0, authorityDigest("qa-profile-create"))
	if err != nil {
		t.Fatal(err)
	}
	publishedProfile := cloneNetworkProjection(created)
	publishedProfile.Header = nextAuthorityHeader(created.Header, "publish", now.Add(-time.Minute))
	publishedProfile.State, publishedProfile.Discoverability, publishedProfile.StateChangedAt = "published", "signed_in_network", now.Add(-time.Minute)
	if _, _, _, err := runtime.network.PutProfile(publishedProfile.Controller, publishedProfile, created.Header.Revision, authorityDigest("qa-profile-publish")); err != nil {
		t.Fatal(err)
	}

	personID, organizationID, membershipID := publication.SubjectPersonID, claim.OrganizationID, "membership_rendered_qa_owner"
	memberOneID, memberTwoID := "person_rendered_qa_member", "person_rendered_qa_member_two"
	memberOneMembershipID, memberTwoMembershipID := "membership_rendered_qa_member", "membership_rendered_qa_member_two"
	runtime.organization.persons[personID] = organizationTestPerson(personID, '1', now.Add(-48*time.Hour))
	runtime.organization.persons[memberOneID] = organizationTestPerson(memberOneID, '2', now.Add(-36*time.Hour))
	runtime.organization.persons[memberTwoID] = organizationTestPerson(memberTwoID, '3', now.Add(-30*time.Hour))
	runtime.organization.organizations[organizationID] = Organization{Header: organizationTestHeader(STRIDEGlobalPersonTenant, organizationID, 1, STRIDEContractOrganization, '4', now.Add(-48*time.Hour)), Name: "QA Organization", Slug: "qa-organization", Status: "active", Discoverability: "private", CreatorPersonID: personID, PolicyRevision: 1, CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-time.Hour)}
	ownerMembership := organizationTestMembership(membershipID, personID, organizationID, "owner", 1, now.Add(-24*time.Hour), "")
	memberOne := organizationTestMembership(memberOneMembershipID, memberOneID, organizationID, "member", 1, now.Add(-12*time.Hour), membershipID)
	memberTwo := organizationTestMembership(memberTwoMembershipID, memberTwoID, organizationID, "member", 1, now.Add(-10*time.Hour), membershipID)
	runtime.organization.memberships[membershipID] = ownerMembership
	runtime.organization.memberships[memberOneMembershipID] = memberOne
	runtime.organization.memberships[memberTwoMembershipID] = memberTwo
	runtime.organization.profiles[personID] = PersonProfile{Header: STRIDEContractHeader{ID: "profile_rendered_qa", Revision: 1}, PersonID: personID, DisplayName: "QA Contributor", Status: "active", OpenToEnabled: true, OpenTo: []string{"advisory"}, UpdatedAt: now}
	capability := TalentSearchCapabilityAuthority{ID: "talent_capability_rendered_qa", Revision: 1, OrganizationID: organizationID, ControllerPersonID: personID, MembershipID: membershipID, MembershipRevision: 1, PolicyRevision: 1, Active: true}
	if err := runtime.network.InstallTalentSearchCapabilityAuthority(capability); err != nil {
		t.Fatal(err)
	}
	for _, membership := range []OrganizationMembership{memberOne, memberTwo} {
		if err := runtime.network.InstallMembershipAuthority(NetworkMembershipAuthority{MembershipID: membership.Header.ID, OrganizationID: membership.OrganizationID, PersonID: membership.PersonID, Revision: membership.Header.Revision, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	capabilityAssertion := TalentSearchCapabilityAssertion{AuthorityID: capability.ID, AuthorityRevision: capability.Revision, ControllerPersonID: capability.ControllerPersonID}
	activeGrant := TalentSearchGrant{Header: strideE10LiveHeader(STRIDEContractTalentSearchGrant, organizationID, "talent_grant_rendered_qa", 1, "rendered-qa-grant", now.Add(-30*time.Minute)), OrganizationID: organizationID, MembershipID: memberOne.Header.ID, MembershipRevision: memberOne.Header.Revision, SearcherPersonID: memberOne.PersonID, CapabilityAdministrator: STRIDEControllerRevision{PrincipalID: personID, AuthorityID: capability.ID, AuthorityRevision: capability.Revision, PolicyRevision: capability.PolicyRevision}, PolicyRevision: capability.PolicyRevision, State: "active", GrantedAt: now.Add(-30 * time.Minute), ExpiresAt: now.Add(24 * time.Hour)}
	if _, _, _, err := runtime.network.PutTalentSearchGrant(capabilityAssertion, activeGrant, 0, authorityDigest("rendered-qa-grant-create")); err != nil {
		t.Fatal(err)
	}
	for _, feature := range []STRIDEFeature{STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview, STRIDEFeatureWorkRecordPrivate, STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow, STRIDEFeatureNetworkSearch, STRIDEFeatureNetworkContact} {
		runtime.setFeatureForTest(feature, true)
	}
	token := sha256Hex([]byte("stride-e10-rendered-qa"))
	userSessionStore().mu.Lock()
	userSessionStore().sessions[hashResetToken(token)] = sessionRecord{Email: "qa-network@example.invalid", PersonID: personID, ActiveOrganizationID: organizationID, OrganizationMembershipID: membershipID, OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1, Expires: now.Add(duration + time.Minute)}
	userSessionStore().mu.Unlock()

	mux := http.NewServeMux()
	productHandler := NewStrideE10ProductHTTP(runtime.ResolvePrincipal, runtime, runtime)
	mux.Handle("/api/", productHandler)
	authMeHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, cookieErr := request.Cookie(sessionCookieName)
		if cookieErr != nil || cookie.Value != token {
			http.Error(writer, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"email": "qa-network@example.invalid", "name": "QA Contributor"})
	})
	mux.Handle("/auth/me", authMeHandler)
	mux.HandleFunc("/public/composer-dictation.js", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(writer, request, "public/composer-dictation.js")
	})
	mux.HandleFunc("/sw.js", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(writer, request, "public/sw.js")
	})
	mux.HandleFunc("/__qa/login", func(writer http.ResponseWriter, request *http.Request) {
		http.SetCookie(writer, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(writer, request, "/network/preview", http.StatusFound)
	})
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := os.ReadFile("index.html")
		if readErr != nil {
			http.Error(writer, "index unavailable", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write(body)
	})
	// Preflight the same registered/authenticated handlers the browser consumes.
	// This catches route, session, feature, and response-schema drift before a
	// rendered reviewer can mistake a static shell for live authority.
	authRecorder := httptest.NewRecorder()
	authRequest := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	authRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	authMeHandler.ServeHTTP(authRecorder, authRequest)
	var authPayload map[string]any
	if authRecorder.Code != http.StatusOK || json.Unmarshal(authRecorder.Body.Bytes(), &authPayload) != nil || authPayload["email"] != "qa-network@example.invalid" || authPayload["name"] != "QA Contributor" {
		t.Fatalf("QA /auth/me preflight status=%d body=%s", authRecorder.Code, authRecorder.Body.String())
	}
	for _, assetPath := range []string{"/public/composer-dictation.js", "/sw.js"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, assetPath, nil))
		contentType := recorder.Header().Get("Content-Type")
		body := recorder.Body.Bytes()
		if recorder.Code != http.StatusOK || !strings.HasPrefix(contentType, "text/javascript") || len(body) == 0 || body[0] == '<' {
			t.Fatalf("QA asset preflight %s status=%d content-type=%q body-prefix=%q", assetPath, recorder.Code, contentType, body[:min(len(body), 32)])
		}
	}
	requiredKinds := map[string][]string{
		"work-record":             {"work-record-section", "contribution-evidence"},
		"network-recruiter-view":  {"network-profile-detail", "contribution-evidence"},
		"network-preview":         {"network-state", "network-profile-detail", "contribution-evidence"},
		"organization-recruiting": {"recruiting-governance"},
		"organization-people":     {"membership-detail"},
	}
	requiredActions := map[string][]string{
		"network-preview":         {"network-profile-off"},
		"organization-recruiting": {"organization-recruiting-grant-create", "organization-recruiting-grant-revoke"},
		"organization-people":     {"organization-member-role-change", "organization-ownership-transfer", "organization-member-revoke"},
	}
	for _, surface := range []string{"work-record", "network-recruiter-view", "network-preview", "organization-recruiting", "organization-people"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/stride/v1/mobile/surfaces/"+surface, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		productHandler.ServeHTTP(recorder, request)
		var projection any
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &projection) != nil || strideE10ValidateMobileProjection(projection, surface) != nil {
			direct, directErr := runtime.project(StrideE10ProductPrincipal{PersonID: personID, ActiveOrganizationID: organizationID, OrganizationMembershipID: membershipID, OrganizationMembershipRev: 1, ActiveOrganizationSessionRev: 1}, surface)
			directBody, _ := json.Marshal(direct)
			t.Fatalf("QA registered surface %s status=%d body=%s directErr=%v direct=%s", surface, recorder.Code, recorder.Body.String(), directErr, directBody)
		}
		kinds, actions := strideE10QASemantics(projection)
		for _, kind := range requiredKinds[surface] {
			if !kinds[kind] {
				t.Fatalf("QA registered surface %s omitted required kind %s: body=%s", surface, kind, recorder.Body.String())
			}
		}
		for _, action := range requiredActions[surface] {
			if !actions[action] {
				t.Fatalf("QA registered surface %s omitted server-minted action %s: body=%s", surface, action, recorder.Body.String())
			}
		}
	}

	listener, err := net.Listen("tcp4", listenAddress)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("authenticated W1-backed QA harness: http://%s/__qa/login", listenAddress)
	select {
	case serveErr := <-done:
		t.Fatalf("QA harness stopped early: %v", serveErr)
	case <-time.After(duration):
	}
	if err := server.Close(); err != nil {
		t.Fatal(fmt.Errorf("close QA harness: %w", err))
	}
}

func strideE10QASemantics(projection any) (map[string]bool, map[string]bool) {
	kinds, actions := map[string]bool{}, map[string]bool{}
	envelope, _ := projection.(map[string]any)
	items, _ := envelope["items"].([]any)
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		if kind, _ := item["kind"].(string); kind != "" {
			kinds[kind] = true
		}
		itemActions, _ := item["actions"].([]any)
		for _, rawAction := range itemActions {
			action, _ := rawAction.(map[string]any)
			if actionType, _ := action["type"].(string); actionType != "" {
				actions[actionType] = true
			}
		}
	}
	return kinds, actions
}
