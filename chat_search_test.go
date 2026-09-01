package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var chatSearchFixtureSequence atomic.Int64

func chatSearchMessage(id, role, authorEmail, text string, at time.Time) scoutChatMessageRecord {
	authorName := participantNameForEmail(authorEmail)
	if role == "scout" {
		authorName = scoutParticipantName
	}
	return scoutChatMessageRecord{ID: id, Kind: "message", Role: role, Text: text, CreatedAt: at.Format(time.RFC3339Nano), AuthorName: authorName, AuthorEmail: authorEmail}
}

func seedScoutChatThreadForSearch(t *testing.T, owner, title, visibility, conversationKind string, members []string, messages ...scoutChatMessageRecord) scoutChatThreadRecord {
	t.Helper()
	now := time.Now().UTC()
	threadID := fmt.Sprintf("scout-chat-search-%d-%d", now.UnixNano(), chatSearchFixtureSequence.Add(1))
	thread, err := kanbanApp.createScoutChatThreadRecordWithKind(threadID, owner, participantNameForEmail(owner), title, visibility, members, conversationKind, now)
	if err != nil {
		t.Fatalf("create search fixture %q: %v", title, err)
	}
	thread.Messages = messages
	if err := kanbanApp.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save search fixture %q: %v", title, err)
	}
	return thread
}

func searchChatAs(t *testing.T, email string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/assistant/chat-search?"+query.Encode(), nil)
	if email != "" {
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			request.AddCookie(cookie)
		}
	}
	response := httptest.NewRecorder()
	assistantChatSearchHandler(response, request)
	return response
}

func decodeChatSearchResults(t *testing.T, response *httptest.ResponseRecorder) []chatSearchResult {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		OK      bool               `json:"ok"`
		Query   string             `json:"query"`
		Results []chatSearchResult `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode search: %v body=%s", err, response.Body.String())
	}
	if !payload.OK || payload.Results == nil {
		t.Fatalf("search payload=%s, want ok + results array", response.Body.String())
	}
	return payload.Results
}

func chatSearchThreadIDs(results []chatSearchResult) map[string]bool {
	ids := map[string]bool{}
	for _, result := range results {
		ids[result.ThreadID] = true
	}
	return ids
}

func TestChatSearchFindsPublicChannelAndOwnPrivateThread(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj, tom := "aj@shareability.com", "tom@shareability.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	channel := seedScoutChatThreadForSearch(t, tom, "Office channel", scoutChatVisibilityPublic, "", nil,
		chatSearchMessage("m-channel-1", "user", tom, "Roadmap review at noon, bring the deck", base),
		chatSearchMessage("m-channel-2", "user", tom, "Lunch is on the third floor", base.Add(time.Minute)),
	)
	private := seedScoutChatThreadForSearch(t, aj, "Scout notes", scoutChatVisibilityPrivate, "", nil,
		chatSearchMessage("m-private-1", "user", aj, "Draft the quarterly ROADMAP sync agenda", base.Add(2*time.Minute)),
		chatSearchMessage("m-private-2", "scout", "", "Here is the roadmap agenda you asked for.", base.Add(3*time.Minute)),
	)

	results := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}}))
	if len(results) != 3 {
		t.Fatalf("results=%d %+v, want 3 roadmap hits", len(results), results)
	}
	if results[0].MessageID != "m-private-2" || results[1].MessageID != "m-private-1" || results[2].MessageID != "m-channel-1" {
		t.Fatalf("results are not newest-first: %+v", results)
	}
	if results[0].AuthorName != scoutParticipantName || results[1].AuthorName != "AJ" || results[1].AuthorEmail != aj {
		t.Fatalf("author fields wrong: %+v", results[:2])
	}
	if results[1].ThreadID != private.ID || results[1].ThreadTitle != "Scout notes" || results[1].Visibility != scoutChatVisibilityPrivate {
		t.Fatalf("private hit fields wrong: %+v", results[1])
	}
	if results[2].ThreadID != channel.ID || results[2].ThreadTitle != "Office channel" || results[2].Visibility != scoutChatVisibilityPublic || results[2].ConversationKind != "" {
		t.Fatalf("channel hit fields wrong: %+v", results[2])
	}
	for _, result := range results {
		if !strings.Contains(strings.ToLower(result.Snippet), "roadmap") {
			t.Fatalf("snippet lost the match: %+v", result)
		}
		if result.CreatedAt == "" {
			t.Fatalf("hit without createdAt: %+v", result)
		}
	}
	if results[1].Snippet != "Draft the quarterly ROADMAP sync agenda" {
		t.Fatalf("short message snippet should be the full plain text, got %q", results[1].Snippet)
	}
}

func TestChatSearchNeverExposesAnotherUsersPrivateThread(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj, tom := "aj@shareability.com", "tom@shareability.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	secret := seedScoutChatThreadForSearch(t, tom, "Tom private", scoutChatVisibilityPrivate, "", nil,
		chatSearchMessage("m-secret-1", "user", tom, "roadmap canary that must never leak", base),
	)
	own := seedScoutChatThreadForSearch(t, aj, "AJ private", scoutChatVisibilityPrivate, "", nil,
		chatSearchMessage("m-own-1", "user", aj, "my own roadmap note", base.Add(time.Minute)),
	)

	asAJ := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}}))
	ids := chatSearchThreadIDs(asAJ)
	if ids[secret.ID] || !ids[own.ID] || len(asAJ) != 1 {
		t.Fatalf("aj results leaked or missed: %+v", asAJ)
	}
	if strings.Contains(strings.ToLower(searchChatAs(t, aj, url.Values{"q": {"canary"}}).Body.String()), "canary that must never leak") {
		t.Fatal("another user's private message text leaked through search")
	}
	asTom := decodeChatSearchResults(t, searchChatAs(t, tom, url.Values{"q": {"roadmap"}}))
	tomIDs := chatSearchThreadIDs(asTom)
	if !tomIDs[secret.ID] || tomIDs[own.ID] || len(asTom) != 1 {
		t.Fatalf("tom results leaked or missed: %+v", asTom)
	}
}

func TestChatSearchHumanGroupAppearsOnlyForMembers(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj, tim, tom := "aj@shareability.com", "tim@shareability.com", "tom@shareability.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	group := seedScoutChatThreadForSearch(t, aj, "Launch crew", scoutChatVisibilityPublic, scoutChatConversationKindHumanGroup, []string{tim},
		chatSearchMessage("m-group-1", "user", tim, "Launch roadmap locked for Friday", base),
	)

	for _, member := range []string{aj, tim} {
		results := decodeChatSearchResults(t, searchChatAs(t, member, url.Values{"q": {"Roadmap"}}))
		if len(results) != 1 || results[0].ThreadID != group.ID || results[0].ConversationKind != scoutChatConversationKindHumanGroup {
			t.Fatalf("member %s results=%+v", member, results)
		}
	}
	if results := decodeChatSearchResults(t, searchChatAs(t, tom, url.Values{"q": {"Roadmap"}})); len(results) != 0 {
		t.Fatalf("outsider found human group messages: %+v", results)
	}
}

func TestChatSearchHonorsLimitAndSkipsCardKinds(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj := "aj@shareability.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	messages := make([]scoutChatMessageRecord, 0, 27)
	for index := 0; index < 25; index++ {
		messages = append(messages, chatSearchMessage(fmt.Sprintf("m-%02d", index), "user", aj, fmt.Sprintf("roadmap item %02d", index), base.Add(time.Duration(index)*time.Minute)))
	}
	proposal := chatSearchMessage("m-proposal", "scout", "", "roadmap proposal card", base.Add(time.Hour))
	proposal.Kind = scoutChatMessageKindProposal
	empty := chatSearchMessage("m-empty", "user", aj, "", base.Add(2*time.Hour))
	messages = append(messages, proposal, empty)
	seedScoutChatThreadForSearch(t, aj, "Backlog", scoutChatVisibilityPublic, "", nil, messages...)

	defaults := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}}))
	if len(defaults) != chatSearchDefaultLimit || defaults[0].MessageID != "m-24" {
		t.Fatalf("default limit results=%d first=%q", len(defaults), defaults[0].MessageID)
	}
	two := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}, "limit": {"2"}}))
	if len(two) != 2 || two[0].MessageID != "m-24" || two[1].MessageID != "m-23" {
		t.Fatalf("limit=2 results=%+v", two)
	}
	all := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}, "limit": {"500"}}))
	if len(all) != 25 {
		t.Fatalf("limit=500 (capped at %d) results=%d", chatSearchMaxLimit, len(all))
	}
	for _, result := range all {
		if result.MessageID == "m-proposal" || result.MessageID == "m-empty" {
			t.Fatalf("card/empty message surfaced in search: %+v", result)
		}
	}
}

func TestChatSearchSnippetEllipsizesAroundTheMatch(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj := "aj@shareability.com"
	filler := strings.Repeat("alpha ", 30)
	text := filler + "the\nROADMAP\tlanded " + filler
	seedScoutChatThreadForSearch(t, aj, "Long note", scoutChatVisibilityPrivate, "", nil,
		chatSearchMessage("m-long", "user", aj, text, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)),
	)
	results := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}}))
	if len(results) != 1 {
		t.Fatalf("results=%+v", results)
	}
	snippet := results[0].Snippet
	if !strings.HasPrefix(snippet, "…") || !strings.HasSuffix(snippet, "…") {
		t.Fatalf("snippet not ellipsized on both ends: %q", snippet)
	}
	if !strings.Contains(snippet, "the ROADMAP landed") {
		t.Fatalf("snippet did not collapse whitespace around the match: %q", snippet)
	}
	if runes := len([]rune(snippet)); runes > 2*chatSearchSnippetContext+len("roadmap")+2 {
		t.Fatalf("snippet too long: %d runes %q", runes, snippet)
	}
}

func TestChatSearchRejectsShortQueriesAndAnonymousCallers(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj := "aj@shareability.com"
	for _, query := range []string{"", "a", " a "} {
		response := searchChatAs(t, aj, url.Values{"q": {query}})
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "query too short") {
			t.Fatalf("q=%q status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
	if response := searchChatAs(t, aj, url.Values{"q": {strings.Repeat("x", chatSearchMaxQueryRunes+1)}}); response.Code != http.StatusBadRequest {
		t.Fatalf("overlong query status=%d body=%s", response.Code, response.Body.String())
	}
	if response := searchChatAs(t, "", url.Values{"q": {"roadmap"}}); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d body=%s", response.Code, response.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/assistant/chat-search?q=roadmap", nil)
	response := httptest.NewRecorder()
	assistantChatSearchHandler(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", response.Code)
	}
}

// The sidebar index hides archived threads, so a search hit inside one would
// be a dead click. Search scope is exactly the sidebar scope.
func TestChatSearchExcludesArchivedThreads(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj := "aj@shareability.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	thread := seedScoutChatThreadForSearch(t, aj, "Old plans", scoutChatVisibilityPublic, "", nil,
		chatSearchMessage("m-archived-1", "user", aj, "roadmap from last quarter", base),
	)
	if results := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}})); len(results) != 1 || results[0].ThreadID != thread.ID {
		t.Fatalf("live thread results=%+v, want the one hit", results)
	}
	if _, err := kanbanApp.setScoutChatThreadArchived(aj, thread.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if results := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}})); len(results) != 0 {
		t.Fatalf("archived thread surfaced in search: %+v", results)
	}
	if _, err := kanbanApp.setScoutChatThreadArchived(aj, thread.ID, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if results := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}})); len(results) != 1 {
		t.Fatalf("restored thread results=%+v, want the hit back", results)
	}
}

// Riffs are not sidebar rows, so a hit inside one would be a dead click.
func TestChatSearchExcludesRiffThreads(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj := "aj@shareability.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	riff, err := kanbanApp.createScoutChatThreadRecord("scout-chat-search-riff", aj, "AJ", "Riff on #team", scoutChatVisibilityPrivate, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	riff.ConversationKind = "channel_riff"
	riff.Messages = []scoutChatMessageRecord{chatSearchMessage("m-riff-1", "user", aj, "roadmap riff aside", base)}
	if err := kanbanApp.saveScoutChatThread(riff); err != nil {
		t.Fatal(err)
	}
	if stored, _, err := kanbanApp.scoutChatThreadByID(aj, riff.ID); err != nil || scoutChatThreadConversationKind(stored) != "channel_riff" {
		t.Fatalf("riff fixture kind=%q err=%v", scoutChatThreadConversationKind(stored), err)
	}
	for _, row := range kanbanApp.scoutChatThreadsIndexView(aj, false, 100) {
		if row["id"] == riff.ID {
			t.Fatal("fixture riff is a sidebar row; the exclusion test is meaningless")
		}
	}
	ordinary := seedScoutChatThreadForSearch(t, aj, "Notes", scoutChatVisibilityPrivate, "", nil,
		chatSearchMessage("m-ordinary-1", "user", aj, "roadmap note", base.Add(time.Minute)),
	)
	results := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}}))
	if len(results) != 1 || results[0].ThreadID != ordinary.ID {
		t.Fatalf("results=%+v, want only the ordinary thread", results)
	}
}

// An unstamped author resolves through the account directory with the same
// display-name rule as typing and @-completion.
func TestChatSearchAuthorNameFallsBackToAccountDirectory(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	aj := "aj@shareability.com"
	extra := registerNonSeedAccountForTest(t, "future-teammate@shareability.com", "Future Teammate")
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	unstamped := scoutChatMessageRecord{ID: "m-unstamped", Kind: "message", Role: "user", Text: "roadmap from the new hire", AuthorEmail: "Future-Teammate@shareability.com", CreatedAt: base.Format(time.RFC3339Nano)}
	seedScoutChatThreadForSearch(t, aj, "Office channel", scoutChatVisibilityPublic, "", nil, unstamped)
	results := decodeChatSearchResults(t, searchChatAs(t, aj, url.Values{"q": {"roadmap"}}))
	if len(results) != 1 || results[0].AuthorName != "Future Teammate" || results[0].AuthorEmail != extra.Email {
		t.Fatalf("results=%+v, want the account directory name", results)
	}
}

func TestClampQueryLimit(t *testing.T) {
	for raw, want := range map[string]int{"": 20, "x": 20, "0": 20, "-3": 20, " 7 ": 7, "50": 50, "51": 50, "999": 50} {
		if got := clampQueryLimit(raw, 20, 50); got != want {
			t.Fatalf("clampQueryLimit(%q)=%d, want %d", raw, got, want)
		}
	}
}
