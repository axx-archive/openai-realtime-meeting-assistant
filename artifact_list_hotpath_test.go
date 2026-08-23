package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestArtifactsListAuthorizesAndBoundsBeforeBodyHydration(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	previousBodyProbe := artifactBodyReadProbe
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
		artifactBodyReadProbe = previousBodyProbe
	})

	// This body is deliberately much larger than a list excerpt and older than
	// the bounded window. The directory snapshot and list response must never
	// hydrate it; the exact-id route below still returns every byte.
	hugeBody := "# Historical source\n\n" + strings.Repeat("body-never-read-by-list ", 50_000)
	huge, _, err := kanbanApp.createOSArtifactWithMetadata("research", "historical", hugeBody, "AJ", map[string]string{"visibility": "organization"})
	if err != nil {
		t.Fatal(err)
	}
	storedHuge, found := kanbanApp.osArtifactByID(huge.ID)
	if !found {
		t.Fatal("large artifact missing after creation")
	}

	publishedIDs := make([]string, 0, 12)
	for index := 0; index < 12; index++ {
		artifact, _, createErr := kanbanApp.createOSArtifactWithMetadata("research", fmt.Sprintf("published-%02d", index), fmt.Sprintf("published body %02d", index), "AJ", map[string]string{
			"visibility": "organization",
			"published":  "true",
			"status":     "published",
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		publishedIDs = append(publishedIDs, artifact.ID)
	}

	privateIDs := make(map[string]bool)
	for index := 0; index < 6; index++ {
		artifact, _, createErr := kanbanApp.createOSArtifactWithMetadata("research", fmt.Sprintf("private-%02d", index), fmt.Sprintf("PRIVATE-BODY-%02d", index), "AJ", map[string]string{
			"visibility":  "private",
			"requestedBy": "aj@shareability.com",
			"published":   "true",
			"status":      "published",
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		privateIDs[artifact.ID] = true
	}

	recentIDs := make([]string, 0, 120)
	selectedLargeBody := "# Current deck\n\n" + strings.Repeat("CURRENT-LARGE-BODY ", 50_000)
	for index := 0; index < 120; index++ {
		body := fmt.Sprintf("recent body %03d", index)
		if index == 119 {
			body = selectedLargeBody
		}
		artifact, _, createErr := kanbanApp.createOSArtifactWithMetadata("research", fmt.Sprintf("recent-%03d", index), body, "AJ", map[string]string{"visibility": "organization"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		recentIDs = append(recentIDs, artifact.ID)
	}

	for _, candidate := range kanbanApp.memory.artifactListAuthorizationSnapshot() {
		if candidate.Entry.Text != "" {
			t.Fatalf("body-free directory retained %d body bytes for %s", len(candidate.Entry.Text), candidate.Entry.ID)
		}
	}

	bodyReads := make(map[string]int)
	artifactBodyReadProbe = func(id string) { bodyReads[id]++ }
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	response := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts", "", cookies, artifactsHandler)
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Artifacts          []meetingMemoryEntry `json:"artifacts"`
		PublishedArtifacts []meetingMemoryEntry `json:"publishedArtifacts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Artifacts) != 100 {
		t.Fatalf("recent count=%d, want 100", len(payload.Artifacts))
	}
	for index, artifact := range payload.Artifacts {
		if want := recentIDs[index+20]; artifact.ID != want {
			t.Fatalf("recent[%d]=%s, want %s", index, artifact.ID, want)
		}
	}
	selectedLarge := payload.Artifacts[len(payload.Artifacts)-1]
	if selectedLarge.Metadata["bodyTrimmed"] != "true" || len([]rune(selectedLarge.Text)) != artifactListExcerptRunes || len(selectedLarge.Text) >= len(selectedLargeBody) {
		t.Fatalf("selected large body was not bounded to the list excerpt: runes=%d metadata=%v", len([]rune(selectedLarge.Text)), selectedLarge.Metadata)
	}
	if len(payload.PublishedArtifacts) != 10 {
		t.Fatalf("published count=%d, want 10", len(payload.PublishedArtifacts))
	}
	for index, artifact := range payload.PublishedArtifacts {
		if want := publishedIDs[index+2]; artifact.ID != want {
			t.Fatalf("published[%d]=%s, want %s", index, artifact.ID, want)
		}
	}
	if len(bodyReads) != 110 {
		t.Fatalf("unique hydrated bodies=%d, want bounded recent/published union of 110", len(bodyReads))
	}
	if bodyReads[huge.ID] != 0 {
		t.Fatal("out-of-window large body was hydrated")
	}
	for id := range privateIDs {
		if bodyReads[id] != 0 || strings.Contains(response.Body.String(), id) {
			t.Fatalf("unauthorized artifact %s was hydrated or disclosed", id)
		}
	}
	for id, count := range bodyReads {
		if count != 1 {
			t.Fatalf("artifact %s hydrated %d times; published/recent overlap must reuse the exact snapshot", id, count)
		}
	}

	// The optimization is list-only. Exact-id reads remain the full-body route.
	clear(bodyReads)
	exact := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?id="+huge.ID, "", cookies, artifactsHandler)
	if exact.Code != http.StatusOK || bodyReads[huge.ID] != 1 {
		t.Fatalf("exact read status=%d bodyReads=%v", exact.Code, bodyReads)
	}
	var exactPayload struct {
		Artifacts []meetingMemoryEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(exact.Body.Bytes(), &exactPayload); err != nil {
		t.Fatal(err)
	}
	if len(exactPayload.Artifacts) != 1 {
		t.Fatalf("exact route returned %d artifacts, want one", len(exactPayload.Artifacts))
	}
	if exactPayload.Artifacts[0].Text != storedHuge.Text {
		t.Fatalf("exact route returned %d body bytes, want full stored %d", len(exactPayload.Artifacts[0].Text), len(storedHuge.Text))
	}
}

func TestArtifactListRunePrefixIsUnicodeSafe(t *testing.T) {
	prefix, trimmed := artifactListRunePrefix("aé🙂b", 3)
	if !trimmed || prefix != "aé🙂" {
		t.Fatalf("prefix=%q trimmed=%t", prefix, trimmed)
	}
	whole, trimmed := artifactListRunePrefix("aé🙂", 3)
	if trimmed || whole != "aé🙂" {
		t.Fatalf("whole=%q trimmed=%t", whole, trimmed)
	}
	stored := meetingMemoryEntry{ID: "small", Text: "small", Metadata: map[string]string{"title": "Stored"}}
	projected := artifactListEntryView(stored)
	projected.Metadata["title"] = "Response mutation"
	if stored.Metadata["title"] != "Stored" {
		t.Fatal("untrimmed list projection aliased durable metadata")
	}
}

func TestArtifactsListConditionalPollIsAuthorizedAndBodyFree(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	previousBodyProbe := artifactBodyReadProbe
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
		artifactBodyReadProbe = previousBodyProbe
	})
	organization, _, err := kanbanApp.createOSArtifactWithMetadata("research", "running", "organization body", "AJ", map[string]string{"visibility": "organization", "status": "running"})
	if err != nil {
		t.Fatal(err)
	}
	private, _, err := kanbanApp.createOSArtifactWithMetadata("research", "private", "private body", "AJ", map[string]string{"visibility": "private", "requestedBy": "aj@shareability.com"})
	if err != nil {
		t.Fatal(err)
	}
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	bodyReads := 0
	artifactBodyReadProbe = func(string) { bodyReads++ }

	request := func(etag string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/artifacts", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		artifactsHandler(recorder, req)
		return recorder
	}

	first := request("")
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" || bodyReads != 1 || strings.Contains(first.Body.String(), private.ID) {
		t.Fatalf("first poll status=%d etag=%q bodyReads=%d body=%s", first.Code, etag, bodyReads, first.Body.String())
	}
	bodyReads = 0
	unchanged := request(etag)
	if unchanged.Code != http.StatusNotModified || unchanged.Header().Get("ETag") != etag || unchanged.Body.Len() != 0 || bodyReads != 0 {
		t.Fatalf("unchanged poll status=%d etag=%q bytes=%d bodyReads=%d", unchanged.Code, unchanged.Header().Get("ETag"), unchanged.Body.Len(), bodyReads)
	}

	// A hidden object's mutation neither leaks bytes nor invalidates this
	// principal's response validator.
	if _, _, err := kanbanApp.updateOSArtifact(private.ID, "private", "private replacement", "AJ"); err != nil {
		t.Fatal(err)
	}
	bodyReads = 0
	privateChanged := request(etag)
	if privateChanged.Code != http.StatusNotModified || privateChanged.Header().Get("ETag") != etag || bodyReads != 0 {
		t.Fatalf("private mutation became an ETag oracle: status=%d etag=%q bodyReads=%d", privateChanged.Code, privateChanged.Header().Get("ETag"), bodyReads)
	}

	if _, _, err := kanbanApp.updateOSArtifact(organization.ID, "running", "organization replacement", "AJ"); err != nil {
		t.Fatal(err)
	}
	bodyReads = 0
	changed := request(etag)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == "" || changed.Header().Get("ETag") == etag || bodyReads != 1 || !strings.Contains(changed.Body.String(), "organization replacement") {
		t.Fatalf("authorized mutation status=%d etag=%q old=%q bodyReads=%d body=%s", changed.Code, changed.Header().Get("ETag"), etag, bodyReads, changed.Body.String())
	}
}

func TestArtifactsListConditionalPollDoesNotRetainConcurrentlyRevokedRow(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	previousAfterCheck := artifactAuthorizationAfterCheckProbe
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
		artifactAuthorizationAfterCheckProbe = previousAfterCheck
	})
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "visible", "visible body", "AJ", map[string]string{"visibility": "organization"})
	if err != nil {
		t.Fatal(err)
	}
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	first := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts", "", cookies, artifactsHandler)
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" || !strings.Contains(first.Body.String(), artifact.ID) {
		t.Fatalf("initial list status=%d etag=%q body=%s", first.Code, etag, first.Body.String())
	}

	mutated := false
	artifactAuthorizationAfterCheckProbe = func() {
		if mutated {
			return
		}
		mutated = true
		if _, _, updateErr := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "visible", "revoked body", "AJ", map[string]string{
			"visibility": "private", "requestedBy": "aj@shareability.com",
		}); updateErr != nil {
			t.Fatalf("concurrent revoke: %v", updateErr)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/artifacts", nil)
	req.Header.Set("If-None-Match", etag)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactsHandler(recorder, req)
	if !mutated || recorder.Code == http.StatusNotModified || strings.Contains(recorder.Body.String(), artifact.ID) || recorder.Header().Get("ETag") == etag {
		t.Fatalf("conditional revoke retained stale row: mutated=%t status=%d etag=%q body=%s", mutated, recorder.Code, recorder.Header().Get("ETag"), recorder.Body.String())
	}
}

func TestArtifactListPollingSendsConditionalValidator(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, want := range []string{
		"let artifactListETag = ''",
		"{ 'If-None-Match': artifactListETag }",
		"if (response.status === 304)",
		"artifactListETag = response.headers.get('ETag') || ''",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("artifact polling is missing %q", want)
		}
	}
	identityReset := strings.Index(source, "resetPersistedScoutChatState()")
	if identityReset < 0 || !strings.Contains(source[identityReset:], "artifactListETag = ''") {
		t.Fatal("identity change does not clear the principal-scoped artifact validator")
	}
}

var artifactListBenchmarkSink meetingMemoryEntry

func BenchmarkArtifactListEntryViewTenMegabyteBody(b *testing.B) {
	entry := meetingMemoryEntry{
		ID:       "large-deck",
		Kind:     meetingMemoryKindOSArtifact,
		Text:     strings.Repeat("x", 10<<20),
		Metadata: map[string]string{"title": "Large deck", "visibility": "organization"},
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(entry.Text)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		artifactListBenchmarkSink = artifactListEntryView(entry)
	}
	b.StopTimer()
	if got := len([]rune(artifactListBenchmarkSink.Text)); got != artifactListExcerptRunes {
		b.Fatalf("excerpt runes=%d, want %d", got, artifactListExcerptRunes)
	}
}

func TestArtifactsListCursorRevalidatesAuthorizationHeaderWithoutBodyRead(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	previousAfterCheck := artifactAuthorizationAfterCheckProbe
	previousBodyProbe := artifactBodyReadProbe
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
		artifactAuthorizationAfterCheckProbe = previousAfterCheck
		artifactBodyReadProbe = previousBodyProbe
	})

	for index := 0; index < 2; index++ {
		if _, _, err := kanbanApp.createOSArtifactWithMetadata("research", fmt.Sprintf("older-%d", index), fmt.Sprintf("older body %d", index), "AJ", map[string]string{"visibility": "organization"}); err != nil {
			t.Fatal(err)
		}
	}
	cursor, _, err := kanbanApp.createOSArtifactWithMetadata("research", "cursor", "cursor body", "AJ", map[string]string{"visibility": "organization"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := kanbanApp.createOSArtifactWithMetadata("research", "newest", "newest body", "AJ", map[string]string{"visibility": "organization"}); err != nil {
		t.Fatal(err)
	}

	mutated := false
	artifactAuthorizationAfterCheckProbe = func() {
		if mutated {
			return
		}
		mutated = true
		if _, _, updateErr := kanbanApp.updateOSArtifact(cursor.ID, "cursor", "concurrent replacement", "AJ"); updateErr != nil {
			t.Fatalf("concurrent cursor update: %v", updateErr)
		}
	}
	bodyReads := 0
	artifactBodyReadProbe = func(string) { bodyReads++ }
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	response := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts?before="+cursor.ID+"&limit=2", "", cookies, artifactsHandler)
	if !mutated || response.Code != http.StatusNotFound || bodyReads != 0 {
		t.Fatalf("cursor race mutated=%t status=%d bodyReads=%d body=%s", mutated, response.Code, bodyReads, response.Body.String())
	}
}
