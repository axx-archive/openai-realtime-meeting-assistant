package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupArtifactAuthorizationSlice(t *testing.T) ([]*http.Cookie, meetingMemoryEntry, meetingMemoryEntry) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })
	org, _, err := kanbanApp.createOSArtifactWithMetadata("research", "org", "organization body", "AJ", map[string]string{"visibility": "organization"})
	if err != nil {
		t.Fatal(err)
	}
	private, _, err := kanbanApp.createOSArtifactWithMetadata("research", "private", "private body", "AJ", map[string]string{"visibility": "private", "requestedBy": "aj@shareability.com"})
	if err != nil {
		t.Fatal(err)
	}
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), org, private
}

func artifactAuthorizationRequest(t *testing.T, method, target, body string, cookies []*http.Cookie, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	return recorder
}

func TestLegacyArtifactPolicyPreservesOrganizationAndAllowsOnlyPrivateOwner(t *testing.T) {
	_, org, private := setupArtifactAuthorizationSlice(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	authorizer := LegacyCompatibleObjectAuthorizer{}
	if !authorizer.AuthorizeArtifact(context.Background(), user, ACLReadContent, org) {
		t.Fatal("explicit organization artifact was denied")
	}
	if !authorizer.AuthorizeArtifact(context.Background(), user, ACLReadContent, private) {
		t.Fatal("private artifact owner was denied")
	}
	if authorizer.AuthorizeArtifact(context.Background(), &userAccount{Email: "tim@shareability.com"}, ACLReadContent, private) {
		t.Fatal("private artifact allowed a non-owner")
	}
}

func TestPrivateThreadOwnerTakesPrecedenceOverMetadataOwner(t *testing.T) {
	setupArtifactAuthorizationSlice(t)
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "thread private", "thread secret", "AJ", map[string]string{
		"originSurface": "chat:" + thread.ID,
		"requestedBy":   "tim@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(artifact.ID)
	if !found || header.OwnerEmail != "aj@shareability.com" || header.Visibility != scoutChatVisibilityPrivate {
		t.Fatalf("header=%+v found=%v", header, found)
	}
	authorizer := LegacyCompatibleObjectAuthorizer{}
	if !authorizer.AuthorizeArtifactHeader(context.Background(), &userAccount{Email: "aj@shareability.com"}, ACLReadContent, header) {
		t.Fatal("private thread owner denied")
	}
	if authorizer.AuthorizeArtifactHeader(context.Background(), &userAccount{Email: "tim@shareability.com"}, ACLReadContent, header) {
		t.Fatal("metadata owner overrode private thread owner")
	}
}

func TestCanonicalKernelCanAuthorizeExactPrivateArtifact(t *testing.T) {
	_, _, private := setupArtifactAuthorizationSlice(t)
	tenant := "tenant-1"
	private.Metadata["tenantId"] = tenant
	ref := ACLObjectRef{TenantID: tenant, Type: "artifact", ID: private.ID, ACLVersion: 1}
	object := ACLObject{Ref: ref, CurrentContentRevision: int64(artifactVersion(private)), CurrentContentDigest: private.Metadata[artifactContentDigestMetadataKey]}
	store := &MemoryACLStore{Objects: map[string]ACLObject{aclObjectKey(ref): object}, Grants: map[string][]ACLGrant{}}
	store.Grants[aclObjectKey(ref)] = []ACLGrant{{
		ID: "private-read", TenantID: tenant, ObjectType: "artifact", ObjectID: private.ID, ACLVersion: 1,
		SubjectKind: ACLSubjectPrincipal, SubjectID: "aj@shareability.com", SubjectPrincipalKind: ACLPrincipalUser,
		Actions: []ACLAction{ACLReadContent},
	}}
	kernel := AuthorizationKernel{Store: store}
	authorizer := LegacyCompatibleObjectAuthorizer{Kernel: &kernel, CanonicalRequired: true, TenantID: tenant}
	if !authorizer.AuthorizeArtifact(context.Background(), &userAccount{Email: "aj@shareability.com"}, ACLReadContent, private) {
		t.Fatal("exact canonical private grant was denied")
	}
	if authorizer.AuthorizeArtifact(context.Background(), &userAccount{Email: "tim@shareability.com"}, ACLReadContent, private) {
		t.Fatal("ungranted private principal was allowed")
	}
}

func TestArtifactHeaderRejectsBlankOrCollidingTenantBeforeFallback(t *testing.T) {
	t.Setenv("BONFIRE_TENANT_ID", "tenant-a")
	authorizer := LegacyCompatibleObjectAuthorizer{}
	user := &userAccount{Email: "aj@shareability.com"}
	base := ArtifactAuthorizationHeader{TenantID: "tenant-a", ObjectID: "artifact-1", ACLVersion: 1, ContentRevision: 1, ContentDigest: strings.Repeat("a", 64), Visibility: "organization"}
	if !authorizer.AuthorizeArtifactHeader(context.Background(), user, ACLReadContent, base) {
		t.Fatal("matching tenant organization artifact denied")
	}
	blank := base
	blank.TenantID = ""
	if authorizer.AuthorizeArtifactHeader(context.Background(), user, ACLReadContent, blank) {
		t.Fatal("blank tenant reached legacy fallback")
	}
	collision := base
	collision.TenantID = "tenant-b"
	if authorizer.AuthorizeArtifactHeader(context.Background(), user, ACLReadContent, collision) {
		t.Fatal("colliding tenant reached legacy fallback")
	}
}

func TestLegacyBootProjectionBackfillsOrganizationAndPrivateOwnerWithoutRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	now := time.Now().UTC()
	entries := []meetingMemoryEntry{
		{ID: "legacy-org", Kind: meetingMemoryKindOSArtifact, Text: "legacy organization body", CreatedAt: now, Metadata: map[string]string{"title": "Legacy org"}},
		{ID: "thread-private", Kind: meetingMemoryKindScoutChat, Text: "not decoded during authorization", CreatedAt: now, Metadata: map[string]string{"ownerEmail": "aj@shareability.com", "visibility": scoutChatVisibilityPrivate}},
		{ID: "legacy-private", Kind: meetingMemoryKindOSArtifact, Text: "legacy private body", CreatedAt: now, Metadata: map[string]string{"originSurface": "chat:thread-private"}},
	}
	var persisted strings.Builder
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		persisted.Write(raw)
		persisted.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(persisted.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	org, found := store.artifactAuthorizationHeaderByID("legacy-org")
	if !found || org.TenantID != canonicalArtifactTenantID() || org.Visibility != "organization" || org.ContentDigest == "" {
		t.Fatalf("legacy org header=%+v", org)
	}
	private, found := store.artifactAuthorizationHeaderByID("legacy-private")
	if !found || private.Visibility != scoutChatVisibilityPrivate || private.OwnerEmail != "aj@shareability.com" || private.ContentDigest == "" {
		t.Fatalf("legacy private header=%+v", private)
	}
	authorizer := LegacyCompatibleObjectAuthorizer{}
	if !authorizer.AuthorizeArtifactHeader(context.Background(), &userAccount{Email: "aj@shareability.com"}, ACLReadContent, private) ||
		authorizer.AuthorizeArtifactHeader(context.Background(), &userAccount{Email: "tim@shareability.com"}, ACLReadContent, private) {
		t.Fatal("legacy private owner policy mismatch")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != persisted.String() {
		t.Fatal("boot projection rewrote persisted JSONL")
	}
}

func TestArtifactsHandlersFilterAndOpaqueDenyPrivate(t *testing.T) {
	ownerCookies, org, private := setupArtifactAuthorizationSlice(t)
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	list := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts", "", cookies, artifactsHandler)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var payload struct {
		Artifacts []meetingMemoryEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Artifacts) != 1 || payload.Artifacts[0].ID != org.ID || strings.Contains(list.Body.String(), private.Text) {
		t.Fatalf("filtered list leaked private artifact: %s", list.Body.String())
	}
	ownerRead := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?id="+private.ID, "", ownerCookies, artifactsHandler)
	if ownerRead.Code != http.StatusOK || !strings.Contains(ownerRead.Body.String(), private.Text) {
		t.Fatalf("private owner read status=%d body=%s", ownerRead.Code, ownerRead.Body.String())
	}
	denied := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?id="+private.ID, "", cookies, artifactsHandler)
	missing := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?id=missing", "", cookies, artifactsHandler)
	if denied.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || denied.Body.String() != missing.Body.String() {
		t.Fatalf("private/missing oracle: private=%d %q missing=%d %q", denied.Code, denied.Body.String(), missing.Code, missing.Body.String())
	}

	patchBody := fmt.Sprintf(`{"id":%q,"text":"replaced private body"}`, private.ID)
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts", patchBody, cookies, artifactsHandler)
	if patch.Code != http.StatusNotFound {
		t.Fatalf("private patch status=%d body=%s", patch.Code, patch.Body.String())
	}
	unchanged, _ := kanbanApp.osArtifactByID(private.ID)
	if unchanged.Text != private.Text {
		t.Fatalf("denied patch changed body to %q", unchanged.Text)
	}

	token := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/render-token?id="+private.ID, "", cookies, artifactRenderTokenHandler)
	if token.Code != http.StatusNotFound {
		t.Fatalf("private render token status=%d body=%s", token.Code, token.Body.String())
	}
	export := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, private.ID), cookies, artifactExportPDFHandler)
	if export.Code != http.StatusNotFound {
		t.Fatalf("private export status=%d body=%s", export.Code, export.Body.String())
	}
	open := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/open", fmt.Sprintf(`{"id":%q}`, private.ID), cookies, artifactOpenHandler)
	if open.Code != http.StatusNotFound {
		t.Fatalf("private open status=%d body=%s", open.Code, open.Body.String())
	}
}

func TestBlobAuthorizationPrecedesETagAndRequiresOwningArtifact(t *testing.T) {
	_, org, private := setupArtifactAuthorizationSlice(t)
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	privateRef, err := putBlob([]byte("private blob"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.appendArtifactAsset(private.ID, artifactAsset{Ref: privateRef, Mime: "application/pdf", Kind: "pdf"}); err != nil {
		t.Fatal(err)
	}
	privateRequest := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+privateRef, nil)
	privateRequest.Header.Set("If-None-Match", `"`+privateRef+`"`)
	for _, cookie := range cookies {
		privateRequest.AddCookie(cookie)
	}
	privateRecorder := httptest.NewRecorder()
	artifactBlobHandler(privateRecorder, privateRequest)
	if privateRecorder.Code != http.StatusNotFound || privateRecorder.Header().Get("ETag") != "" {
		t.Fatalf("private ETag oracle status=%d headers=%v", privateRecorder.Code, privateRecorder.Header())
	}

	unownedRef, err := putBlob([]byte("known but unowned"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	unowned := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/blob?ref="+unownedRef, "", cookies, artifactBlobHandler)
	if unowned.Code != http.StatusNotFound || unowned.Header().Get("ETag") != "" {
		t.Fatalf("known hash granted authority status=%d headers=%v", unowned.Code, unowned.Header())
	}

	orgRef, err := putBlob([]byte("organization blob"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.appendArtifactAsset(org.ID, artifactAsset{Ref: orgRef, Mime: "application/pdf", Kind: "pdf"}); err != nil {
		t.Fatal(err)
	}
	orgRequest := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+orgRef, nil)
	orgRequest.Header.Set("If-None-Match", `"`+orgRef+`"`)
	for _, cookie := range cookies {
		orgRequest.AddCookie(cookie)
	}
	orgRecorder := httptest.NewRecorder()
	artifactBlobHandler(orgRecorder, orgRequest)
	if orgRecorder.Code != http.StatusNotModified || orgRecorder.Header().Get("ETag") != `"`+orgRef+`"` {
		t.Fatalf("authorized ETag status=%d headers=%v", orgRecorder.Code, orgRecorder.Header())
	}
}

func TestBlobAuthorizationPreservesDirectFileVisibility(t *testing.T) {
	cookies, _, _ := setupArtifactAuthorizationSlice(t)
	ref, err := putBlob([]byte("team file"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-team", "Team file", map[string]string{
		"blobRef": ref,
		"name":    "team.pdf",
	}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+ref, nil)
	request.Header.Set("If-None-Match", `"`+ref+`"`)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactBlobHandler(recorder, request)
	if recorder.Code != http.StatusNotModified || recorder.Header().Get("ETag") != `"`+ref+`"` {
		t.Fatalf("direct file status=%d headers=%v", recorder.Code, recorder.Header())
	}
}

func TestBlobAuthorizationFollowsChatThreadVisibility(t *testing.T) {
	setupArtifactAuthorizationSlice(t)
	ajCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	privateRef, err := putBlob([]byte("private attachment"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	privateThread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Private", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(privateThread.OwnerEmail, privateThread.ID, scoutChatMessageRecord{
		ID: "private-file-message", Kind: "message", Role: "user", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files: []scoutChatFileAttachment{{Name: "private.pdf", Ref: privateRef, Mime: "application/pdf"}},
	}); err != nil {
		t.Fatal(err)
	}

	publicRef, err := putBlob([]byte("public attachment"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	publicThread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Public", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(publicThread.OwnerEmail, publicThread.ID, scoutChatMessageRecord{
		ID: "public-file-message", Kind: "message", Role: "user", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files: []scoutChatFileAttachment{{Name: "public.pdf", Ref: publicRef, Mime: "application/pdf"}},
	}); err != nil {
		t.Fatal(err)
	}

	requestBlob := func(ref string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+ref, nil)
		request.Header.Set("If-None-Match", `"`+ref+`"`)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		artifactBlobHandler(recorder, request)
		return recorder
	}

	privateOwner := requestBlob(privateRef, ajCookies)
	if privateOwner.Code != http.StatusNotModified || privateOwner.Header().Get("ETag") == "" {
		t.Fatalf("private owner status=%d headers=%v", privateOwner.Code, privateOwner.Header())
	}
	privateNonOwner := requestBlob(privateRef, timCookies)
	if privateNonOwner.Code != http.StatusNotFound || privateNonOwner.Header().Get("ETag") != "" {
		t.Fatalf("private non-owner status=%d headers=%v", privateNonOwner.Code, privateNonOwner.Header())
	}
	publicMember := requestBlob(publicRef, timCookies)
	if publicMember.Code != http.StatusNotModified || publicMember.Header().Get("ETag") == "" {
		t.Fatalf("public member status=%d headers=%v", publicMember.Code, publicMember.Header())
	}
}

func TestClientMemorySnapshotOmitsArtifactIdentityAndBody(t *testing.T) {
	_, _, private := setupArtifactAuthorizationSlice(t)
	snapshot, err := json.Marshal(kanbanApp.memorySnapshotForClients(100))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(snapshot), private.ID) || strings.Contains(string(snapshot), private.Text) {
		t.Fatalf("shared snapshot leaked private artifact: %s", snapshot)
	}
}

func TestArtifactHandlerAuthorizesHeaderBeforeBodyFetch(t *testing.T) {
	ownerCookies, _, private := setupArtifactAuthorizationSlice(t)
	nonOwnerCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	previousProbe := artifactBodyReadProbe
	bodyReads := 0
	artifactBodyReadProbe = func(string) { bodyReads++ }
	t.Cleanup(func() { artifactBodyReadProbe = previousProbe })

	denied := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?id="+private.ID, "", nonOwnerCookies, artifactsHandler)
	if denied.Code != http.StatusNotFound || bodyReads != 0 {
		t.Fatalf("denied status=%d bodyReads=%d", denied.Code, bodyReads)
	}
	allowed := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?id="+private.ID, "", ownerCookies, artifactsHandler)
	if allowed.Code != http.StatusOK || bodyReads != 1 {
		t.Fatalf("owner status=%d bodyReads=%d", allowed.Code, bodyReads)
	}
}

func TestAuthorizedArtifactRejectsConcurrentHeaderMutation(t *testing.T) {
	ownerCookies, _, private := setupArtifactAuthorizationSlice(t)
	previousAfterCheck := artifactAuthorizationAfterCheckProbe
	previousBodyProbe := artifactBodyReadProbe
	mutated := false
	bodyReads := 0
	artifactBodyReadProbe = func(string) { bodyReads++ }
	artifactAuthorizationAfterCheckProbe = func() {
		if mutated {
			return
		}
		mutated = true
		if _, _, err := kanbanApp.updateOSArtifact(private.ID, "private", "concurrent replacement", "AJ"); err != nil {
			t.Fatalf("concurrent update: %v", err)
		}
	}
	t.Cleanup(func() {
		artifactAuthorizationAfterCheckProbe = previousAfterCheck
		artifactBodyReadProbe = previousBodyProbe
	})

	response := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?id="+private.ID, "", ownerCookies, artifactsHandler)
	if response.Code != http.StatusNotFound || bodyReads != 0 || strings.Contains(response.Body.String(), "concurrent replacement") {
		t.Fatalf("TOCTOU response status=%d reads=%d body=%s", response.Code, bodyReads, response.Body.String())
	}
}

func TestAuthorizedArtifactListDropsConcurrentMutation(t *testing.T) {
	ownerCookies, org, private := setupArtifactAuthorizationSlice(t)
	previousAfterCheck := artifactAuthorizationAfterCheckProbe
	mutated := false
	artifactAuthorizationAfterCheckProbe = func() {
		if mutated {
			return
		}
		mutated = true
		if _, _, err := kanbanApp.updateOSArtifact(org.ID, "org", "new organization body", "AJ"); err != nil {
			t.Fatalf("concurrent update: %v", err)
		}
	}
	t.Cleanup(func() { artifactAuthorizationAfterCheckProbe = previousAfterCheck })

	response := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts", "", ownerCookies, artifactsHandler)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "new organization body") {
		t.Fatalf("list TOCTOU status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), private.ID) {
		t.Fatalf("stable private artifact missing: %s", response.Body.String())
	}
}

func TestConditionalArtifactMutationRejectsStaleAuthorizedHeader(t *testing.T) {
	_, _, private := setupArtifactAuthorizationSlice(t)
	header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(private.ID)
	if !found {
		t.Fatal("authorization header missing")
	}
	if _, _, err := kanbanApp.updateOSArtifact(private.ID, "private", "concurrent winner", "AJ"); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := kanbanApp.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, private.ID, "private", "stale overwrite", "AJ", nil); err == nil || changed {
		t.Fatalf("stale mutation changed=%v err=%v", changed, err)
	}
	stored, _ := kanbanApp.osArtifactByID(private.ID)
	if stored.Text != "concurrent winner" {
		t.Fatalf("stale mutation overwrote body: %q", stored.Text)
	}
}

func TestConditionalArtifactMutationAllowsOnlyOneConcurrentWinner(t *testing.T) {
	_, _, private := setupArtifactAuthorizationSlice(t)
	header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(private.ID)
	if !found {
		t.Fatal("authorization header missing")
	}
	start := make(chan struct{})
	type result struct {
		entry   meetingMemoryEntry
		changed bool
		err     error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, body := range []string{"winner one", "winner two"} {
		body := body
		go func() {
			ready.Done()
			<-start
			entry, changed, err := kanbanApp.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, private.ID, "private", body, "AJ", nil)
			results <- result{entry: entry, changed: changed, err: err}
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results
	winners := 0
	for _, outcome := range []result{first, second} {
		if outcome.err == nil && outcome.changed {
			winners++
			if outcome.entry.Metadata[artifactContentDigestMetadataKey] != artifactCapabilityDigest(outcome.entry) {
				t.Fatalf("winner digest mismatch: %+v", outcome.entry.Metadata)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d first=%+v second=%+v", winners, first, second)
	}
}

func TestConditionalArtifactMetadataMutationUsesExactHeader(t *testing.T) {
	_, _, private := setupArtifactAuthorizationSlice(t)
	header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(private.ID)
	if !found {
		t.Fatal("authorization header missing")
	}
	updated, changed, err := kanbanApp.memory.updateOSArtifactMetadataIfHeaderMatches(header, private.ID, map[string]string{"savedToFiles": "true"})
	if err != nil || !changed || updated.Metadata["savedToFiles"] != "true" || updated.Text != private.Text {
		t.Fatalf("metadata update changed=%v err=%v entry=%+v", changed, err, updated)
	}
	if _, _, err := kanbanApp.updateOSArtifact(private.ID, "private", "new revision", "AJ"); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := kanbanApp.memory.updateOSArtifactMetadataIfHeaderMatches(header, private.ID, map[string]string{"savedToFilesBy": "stale"}); err == nil || changed {
		t.Fatalf("stale metadata update changed=%v err=%v", changed, err)
	}
}

func TestConditionalArtifactMetadataRollbackRestoresAbsenceAndRejectsNewRevision(t *testing.T) {
	_, _, private := setupArtifactAuthorizationSlice(t)
	prior := make(map[string]string, len(private.Metadata))
	for key, value := range private.Metadata {
		prior[key] = value
	}
	header, _ := kanbanApp.memory.artifactAuthorizationHeaderByID(private.ID)
	stamped, changed, err := kanbanApp.memory.updateOSArtifactMetadataIfHeaderMatches(header, private.ID, map[string]string{
		"savedToFiles": "true", "savedToFilesBy": "AJ", "savedToFilesAt": "now",
	})
	if err != nil || !changed {
		t.Fatalf("stamp changed=%v err=%v", changed, err)
	}
	stampedHeader := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(stamped))
	restored, changed, err := kanbanApp.memory.restoreOSArtifactMetadataIfHeaderMatches(stampedHeader, private.ID, prior, []string{"savedToFiles", "savedToFilesBy", "savedToFilesAt"})
	if err != nil || !changed {
		t.Fatalf("restore changed=%v err=%v", changed, err)
	}
	for _, key := range []string{"savedToFiles", "savedToFilesBy", "savedToFilesAt"} {
		if _, exists := restored.Metadata[key]; exists {
			t.Fatalf("rollback preserved absent key %q", key)
		}
	}
	stamped, _, err = kanbanApp.memory.updateOSArtifactMetadataIfHeaderMatches(stampedHeader, private.ID, map[string]string{"savedToFiles": "true"})
	if err != nil {
		t.Fatal(err)
	}
	preRevisionHeader := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(stamped))
	if _, _, err := kanbanApp.updateOSArtifact(private.ID, "private", "newer body", "AJ"); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := kanbanApp.memory.restoreOSArtifactMetadataIfHeaderMatches(preRevisionHeader, private.ID, prior, []string{"savedToFiles"}); err == nil || changed {
		t.Fatalf("stale rollback changed=%v err=%v", changed, err)
	}
}

type revisionCaptureAuthorizer struct {
	wantRevision int64
	wantDigest   string
	seen         []ACLRevisionRef
}

func (authorizer *revisionCaptureAuthorizer) AuthorizeArtifactHeader(_ context.Context, _ *userAccount, _ ACLAction, header ArtifactAuthorizationHeader) bool {
	revision := ACLRevisionRef{ContentRevision: header.ContentRevision, ContentDigest: header.ContentDigest}
	authorizer.seen = append(authorizer.seen, revision)
	return revision.ContentRevision == authorizer.wantRevision && revision.ContentDigest == authorizer.wantDigest
}

func TestHistoricalBlobAuthorizesExactRevision(t *testing.T) {
	cookies, _, private := setupArtifactAuthorizationSlice(t)
	updated, _, err := kanbanApp.updateOSArtifact(private.ID, "private", "private body v2", "AJ")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Metadata[artifactContentDigestMetadataKey] != artifactCapabilityDigest(updated) {
		t.Fatalf("current digest=%q", updated.Metadata[artifactContentDigestMetadataKey])
	}
	history := artifactVersionHistory(updated)
	if len(history) != 1 || !validBlobRef(history[0].BodyBlobRef) || history[0].ContentDigest == "" {
		t.Fatalf("history=%+v", history)
	}
	capture := &revisionCaptureAuthorizer{wantRevision: int64(history[0].V), wantDigest: history[0].ContentDigest}
	previous := artifactObjectAuthorizer
	artifactObjectAuthorizer = capture
	t.Cleanup(func() { artifactObjectAuthorizer = previous })

	response := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/blob?ref="+history[0].BodyBlobRef, "", cookies, artifactBlobHandler)
	if response.Code != http.StatusOK || len(capture.seen) == 0 {
		t.Fatalf("historical status=%d seen=%+v body=%s", response.Code, capture.seen, response.Body.String())
	}
	if got := capture.seen[0]; got.ContentRevision != int64(history[0].V) || got.ContentDigest != history[0].ContentDigest {
		t.Fatalf("authorized revision=%+v want v%d %s", got, history[0].V, history[0].ContentDigest)
	}
}
