package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHomeSnapshotIsConversationFirstAndServerOwned(t *testing.T) {
	now := time.Date(2026, 8, 11, 19, 0, 0, 0, time.UTC)
	threads := []scoutChatThreadRecord{
		{
			ID: "recent", Title: "Country Golf", OwnerEmail: artifactLibraryAdminEmail,
			CreatedAt: "2026-08-11T18:00:00Z", UpdatedAt: "2026-08-11T18:59:00Z",
			Messages: []scoutChatMessageRecord{{ID: "recent-message", Kind: "message", Role: "scout", Text: "The venue comparison is ready to continue.", CreatedAt: "2026-08-11T18:59:00Z"}},
		},
		{
			ID: "approval", Title: "Investor package", OwnerEmail: artifactLibraryAdminEmail,
			CreatedAt: "2026-08-11T18:00:00Z", UpdatedAt: "2026-08-11T18:58:00Z",
			Messages: []scoutChatMessageRecord{{
				ID: "approval-message", Kind: "thread", Role: "scout", CreatedAt: "2026-08-11T18:58:00Z",
				Thread: &scoutChatThreadRef{ID: "approval-run", Query: "Publish the investor package", Status: "approval_required"},
			}},
		},
		{
			ID: "work", Title: "Pitch STRIDE", OwnerEmail: artifactLibraryAdminEmail,
			CreatedAt: "2026-08-11T18:00:00Z", UpdatedAt: "2026-08-11T18:57:00Z",
			Messages: []scoutChatMessageRecord{{
				ID: "work-message", Kind: "thread", Role: "scout", CreatedAt: "2026-08-11T18:57:00Z",
				Thread: &scoutChatThreadRef{ID: "work-run", Mode: "pitch deck", Query: "Create the STRIDE pitch deck", Status: "running", CurrentStage: "draft_story"},
			}},
		},
	}
	notifications := []map[string]any{{"id": "generic", "read": false, "kind": "info", "text": "A generic update"}}
	rooms := []homeRoomCandidate{{ID: officeRoomID, Name: "the office", SourceRevision: "meeting-7", Live: true, ParticipantCount: 2}}

	snapshot := buildHomeSnapshot(threads, notifications, rooms, now)
	if snapshot.Version != homeSnapshotVersion || snapshot.GeneratedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("snapshot identity=%+v", snapshot)
	}
	if len(snapshot.Items) != 4 {
		t.Fatalf("items=%+v, want four bounded items", snapshot.Items)
	}
	wantKinds := []string{"recent-thread", "needs-you", "active-work", "live-meeting"}
	for index, want := range wantKinds {
		if snapshot.Items[index].Kind != want {
			t.Fatalf("items[%d].kind=%q, want %q", index, snapshot.Items[index].Kind, want)
		}
	}
	if got := snapshot.Items[0].Destination.MessageID; got != "recent-message" {
		t.Fatalf("recent exact message=%q", got)
	}
	if snapshot.Items[1].WorkID != "approval-run" || snapshot.Items[2].WorkID != "work-run" {
		t.Fatalf("work identities=%q/%q", snapshot.Items[1].WorkID, snapshot.Items[2].WorkID)
	}
	if snapshot.Items[2].Eyebrow != "Presentation · Building" || snapshot.Items[2].Detail != "Building the first draft" {
		t.Fatalf("human work copy=%+v", snapshot.Items[2])
	}
	if len(snapshot.Starters) != 4 {
		t.Fatalf("starters=%+v", snapshot.Starters)
	}
	wantStarters := []struct {
		id    string
		label string
	}{
		{id: "continue", label: "Continue"},
		{id: "explore", label: "Explore"},
		{id: "create", label: "Create"},
		{id: "challenge", label: "Challenge"},
	}
	seenSuggestionIDs := map[string]bool{}
	for index, want := range wantStarters {
		starter := snapshot.Starters[index]
		if starter.ID != want.id || starter.Label != want.label || starter.Detail == "" {
			t.Fatalf("starters[%d]=%+v, want stable %q/%q category", index, starter, want.id, want.label)
		}
		if len(starter.Suggestions) < 1 || len(starter.Suggestions) > 4 {
			t.Fatalf("starters[%d].suggestions=%+v, want one to four", index, starter.Suggestions)
		}
		for _, suggestion := range starter.Suggestions {
			if suggestion.ID == "" || suggestion.Text == "" || seenSuggestionIDs[suggestion.ID] {
				t.Fatalf("invalid or duplicate suggestion=%+v", suggestion)
			}
			seenSuggestionIDs[suggestion.ID] = true
			if suggestion.WhyThis == "" || len(suggestion.SourceCoverage) != 1 {
				t.Fatalf("suggestion lacks body-free explanation/provenance=%+v", suggestion)
			}
			if starter.ID == "continue" {
				if suggestion.Destination.Route != "thread" {
					t.Fatalf("Continue destination=%+v, want exact authorized thread", suggestion.Destination)
				}
			} else if suggestion.Destination != (homeDestination{Route: "new-private"}) {
				t.Fatalf("%s destination=%+v, want new private conversation", starter.ID, suggestion.Destination)
			}
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"toolId", "toolTemplate", "provider", "model", "reasoning", "authority", "artifactId"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("home projection leaked forbidden client selector %q: %s", forbidden, encoded)
		}
	}
}

func TestHomeSnapshotIgnoresGenericNotificationsAndCollapsesSameThread(t *testing.T) {
	threads := []scoutChatThreadRecord{{
		ID: "work", Title: "Forecast", OwnerEmail: artifactLibraryAdminEmail,
		CreatedAt: "2026-08-11T18:00:00Z", UpdatedAt: "2026-08-11T18:59:00Z",
		Messages: []scoutChatMessageRecord{{
			ID: "work-message", Kind: "thread", Role: "scout", Text: "The model needs attention.", CreatedAt: "2026-08-11T18:59:00Z",
			Thread: &scoutChatThreadRef{ID: "work-run", Mode: "financial model", Query: "Revise the forecast", Status: "needs_attention"},
		}},
	}}
	snapshot := buildHomeSnapshot(threads, []map[string]any{{
		"id": "generic", "read": false, "kind": "alert", "text": "This is not linked to an action",
	}}, nil, time.Now())
	if len(snapshot.Items) != 1 || snapshot.Items[0].Kind != "needs-you" || snapshot.Items[0].WorkID != "work-run" {
		t.Fatalf("items=%+v, want one exact work intervention", snapshot.Items)
	}
	if len(snapshot.Starters) != 4 {
		t.Fatalf("starters=%+v, want stable categories without private continuation context", snapshot.Starters)
	}
	for _, starter := range snapshot.Starters {
		if len(starter.Suggestions) < 1 || len(starter.Suggestions) > 4 {
			t.Fatalf("bounded starter=%+v, want one to four chief-of-staff suggestions", starter)
		}
		for _, suggestion := range starter.Suggestions {
			if starter.ID == "continue" {
				if suggestion.Destination.Route != "thread" || !strings.Contains(strings.ToLower(suggestion.Text), "forecast") || len(suggestion.SourceCoverage) != 1 {
					t.Fatalf("Continue did not prioritize the exact waiting work: %+v", suggestion)
				}
			} else if suggestion.Destination != (homeDestination{Route: "new-private"}) || !strings.Contains(strings.ToLower(suggestion.Text), "forecast") || len(suggestion.SourceCoverage) != 1 {
				t.Fatalf("non-Continue starter did not use the only authorized waiting context: %+v", starter)
			}
		}
	}
	if !strings.Contains(snapshot.Starters[3].Suggestions[0].WhyThis, "waiting on a decision") {
		t.Fatalf("Challenge did not explain the sparse needs-you recommendation: %+v", snapshot.Starters[3].Suggestions)
	}
}

func TestHomeChiefOfStaffRecommendationsUseSoleActiveWork(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	threads := []scoutChatThreadRecord{{
		ID: "work", Title: "Launch brief", OwnerEmail: artifactLibraryAdminEmail, UpdatedAt: now.Format(time.RFC3339Nano),
		Messages: []scoutChatMessageRecord{{ID: "work-message", Role: "scout", CreatedAt: now.Format(time.RFC3339Nano), Thread: &scoutChatThreadRef{ID: "work-run", Query: "Draft the launch brief", Status: "running"}}},
	}}
	snapshot := buildHomeSnapshot(threads, nil, nil, now)
	create := snapshot.Starters[2].Suggestions
	if len(create) == 0 || !strings.Contains(strings.ToLower(create[0].Text), "launch brief") || create[0].SourceCoverage[0].Kind != "active-work" {
		t.Fatalf("Create ranking=%+v, want the sole active work rather than a generic fallback", create)
	}
}

func TestHomeChiefOfStaffRecommendationsPrioritizeNeedsYouAndActiveWork(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	threads := []scoutChatThreadRecord{
		{ID: "recent", Title: "Customer rollout", OwnerEmail: artifactLibraryAdminEmail, UpdatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), Messages: []scoutChatMessageRecord{{ID: "recent-message", Role: "user", Text: "Review the launch sequence", CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)}}},
		{ID: "decision", Title: "Enterprise pilot", OwnerEmail: artifactLibraryAdminEmail, UpdatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339Nano), Messages: []scoutChatMessageRecord{{ID: "decision-message", Role: "scout", CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339Nano), Thread: &scoutChatThreadRef{ID: "decision-run", Query: "Approve the Enterprise pilot scope", Status: "approval_required"}}}},
		{ID: "work", Title: "Board update", OwnerEmail: artifactLibraryAdminEmail, UpdatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339Nano), Messages: []scoutChatMessageRecord{{ID: "work-message", Role: "scout", CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339Nano), Thread: &scoutChatThreadRef{ID: "work-run", Query: "Draft the August board update", Status: "running"}}}},
	}
	snapshot := buildHomeSnapshot(threads, nil, nil, now)
	continueSuggestions := snapshot.Starters[0].Suggestions
	if len(continueSuggestions) != 3 || continueSuggestions[2].Destination.ThreadID != "decision" || !strings.Contains(continueSuggestions[2].WhyThis, "waiting") {
		t.Fatalf("Continue ranking=%+v, want recent context plus the exact needs-you work", continueSuggestions)
	}
	createSuggestions := snapshot.Starters[2].Suggestions
	if len(createSuggestions) == 0 || !strings.Contains(createSuggestions[0].Text, "August board update") || createSuggestions[0].SourceCoverage[0].Kind != "active-work" {
		t.Fatalf("Create ranking=%+v, want active-work recommendation", createSuggestions)
	}
	challengeSuggestions := snapshot.Starters[3].Suggestions
	if len(challengeSuggestions) == 0 || !strings.Contains(challengeSuggestions[0].Text, "Enterprise pilot") || challengeSuggestions[0].SourceCoverage[0].Kind != "needs-you" {
		t.Fatalf("Challenge ranking=%+v, want waiting decision recommendation", challengeSuggestions)
	}
}

func TestHomeChiefOfStaffRecommendationsSynthesizeOnlyViewerAuthorizedThreads(t *testing.T) {
	now := time.Date(2026, 8, 12, 19, 0, 0, 0, time.UTC)
	threads := []scoutChatThreadRecord{
		{ID: "team", Title: "Launch planning", Visibility: scoutChatVisibilityPublic, UpdatedAt: now.Format(time.RFC3339Nano), Messages: []scoutChatMessageRecord{{ID: "team-message", Text: "The onboarding risk needs an owner."}}},
		{ID: "private", Title: "Customer follow-up", OwnerEmail: artifactLibraryAdminEmail, UpdatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), Messages: []scoutChatMessageRecord{{ID: "private-message", Text: "Revisit the onboarding timeline before Friday."}}},
	}
	snapshot := buildHomeSnapshot(threads, nil, nil, now)
	explore := snapshot.Starters[1].Suggestions
	found := false
	for _, suggestion := range explore {
		if suggestion.ID != "explore-recurring-theme" {
			continue
		}
		found = true
		if !strings.Contains(suggestion.Text, "onboarding") || !strings.Contains(suggestion.Text, "your conversations") || strings.Contains(suggestion.Text, "the team") || len(suggestion.SourceCoverage) != 2 || !strings.Contains(suggestion.WhyThis, "2 conversations") {
			t.Fatalf("recurring theme suggestion=%+v", suggestion)
		}
	}
	if !found {
		t.Fatalf("Explore suggestions=%+v, want cross-source authorized synthesis", explore)
	}
	encoded, _ := json.Marshal(explore)
	if strings.Contains(string(encoded), "needs an owner") || strings.Contains(string(encoded), "before Friday") {
		t.Fatalf("recommendation explanation leaked source bodies: %s", encoded)
	}
}

func TestAssistantHomeHandlerIsAuthenticatedReadOnly(t *testing.T) {
	setupAuthTestEnv(t)
	roomsPath := filepath.Join(t.TempDir(), "rooms.json")
	t.Setenv("BONFIRE_ROOMS_PATH", roomsPath)
	roomStoreMu.Lock()
	delete(roomStoreCache, roomsPath)
	roomStoreMu.Unlock()
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	unauthorized := httptest.NewRecorder()
	assistantHomeHandler(unauthorized, httptest.NewRequest(http.MethodGet, "/assistant/home", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/assistant/home", nil)
	for _, cookie := range loginAs(t, artifactLibraryAdminEmail, defaultMeetingRoomPassword) {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	assistantHomeHandler(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if got := len(kanbanApp.scoutChatThreadsSnapshot(artifactLibraryAdminEmail, false, 100)); got != 0 {
		t.Fatalf("GET created %d threads", got)
	}
	if _, err := os.Stat(roomsPath); !os.IsNotExist(err) {
		t.Fatalf("GET created rooms store: %v", err)
	}

	method := httptest.NewRecorder()
	assistantHomeHandler(method, httptest.NewRequest(http.MethodPost, "/assistant/home", strings.NewReader(`{}`)))
	if method.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", method.Code)
	}
}

func TestHomeWorkFamilyMatchesTheRecurringCatalogWithoutSubstringGuessing(t *testing.T) {
	tests := map[string]string{
		"schedule a weekly customer brief":                   "Scheduled work",
		"regenerate this pitch deck":                         "Revision",
		"research, deck, and workbook investor package":      "Mixed package",
		"turn this transcript into a meeting recap":          "Meeting recap",
		"chart the results in a dashboard":                   "Data visualization",
		"prepare a repository implementation handoff":        "Build",
		"create a project plan and task board":               "Project plan",
		"build a cash flow model in xlsx":                    "Financial model",
		"make a polished PowerPoint presentation":            "Presentation",
		"create a brand illustration":                        "Design",
		"perform cited due diligence":                        "Research",
		"draft a one-pager memo":                             "Document",
		"decode the customer feedback without a deliverable": "Work",
	}
	for query, want := range tests {
		if got := homeWorkFamily(scoutChatThreadRef{Query: query}); got != want {
			t.Errorf("homeWorkFamily(%q)=%q, want %q", query, got, want)
		}
	}
}
