package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type scoutChatCreateResponse struct {
	Created bool                  `json:"created"`
	Thread  scoutChatThreadRecord `json:"thread"`
}

func postScoutChatThreadAs(t *testing.T, email, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, email, "B0NFIRE!") {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	assistantChatThreadsHandler(response, request)
	return response
}

func requestScoutChatThreadAs(t *testing.T, email, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range loginAs(t, email, "B0NFIRE!") {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	assistantChatThreadHandler(response, request)
	return response
}

func listScoutChatThreadsAs(t *testing.T, email string) []map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads?view=index", nil)
	for _, cookie := range loginAs(t, email, "B0NFIRE!") {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	assistantChatThreadsHandler(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list as %s: status=%d body=%s", email, response.Code, response.Body.String())
	}
	var payload struct {
		Threads []map[string]any `json:"threads"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list as %s: %v", email, err)
	}
	return payload.Threads
}

func scoutChatListContains(threads []map[string]any, threadID string) bool {
	for _, thread := range threads {
		if asString(thread["id"]) == threadID {
			return true
		}
	}
	return false
}

func countStoredScoutChatThreads(app *kanbanBoardApp) int {
	count := 0
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindScoutChat {
			count++
		}
	}
	return count
}

func TestAssistantHumanGroupCreationIsExactMemberAndIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	first := postScoutChatThreadAs(t, "aj@shareability.com", `{
		"title":"Launch crew",
		"conversationKind":"human_group",
		"memberEmails":["TIM@shareability.com","caitlyn@shareability.com","tim@shareability.com"],
		"operationId":"mac-human-group-launch-crew"
	}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%s", first.Code, first.Body.String())
	}
	var created scoutChatCreateResponse
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	wantMembers := []string{"aj@shareability.com", "caitlyn@shareability.com", "tim@shareability.com"}
	if !created.Created || created.Thread.ConversationKind != scoutChatConversationKindHumanGroup || created.Thread.Visibility != scoutChatVisibilityPublic || strings.Join(created.Thread.MemberEmails, ",") != strings.Join(wantMembers, ",") {
		t.Fatalf("created=%+v, want truthful exact-member human group", created)
	}

	replay := postScoutChatThreadAs(t, "aj@shareability.com", `{
		"title":"Launch crew",
		"visibility":"public",
		"conversationKind":"human_group",
		"memberEmails":["caitlyn@shareability.com","tim@shareability.com"],
		"operationId":"mac-human-group-launch-crew"
	}`)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayed scoutChatCreateResponse
	if err := json.Unmarshal(replay.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.Created || replayed.Thread.ID != created.Thread.ID || replayed.Thread.CreatedAt != created.Thread.CreatedAt {
		t.Fatalf("replay=%+v, want same durable thread", replayed)
	}

	conflict := postScoutChatThreadAs(t, "aj@shareability.com", `{
		"title":"Launch crew",
		"conversationKind":"human_group",
		"memberEmails":["tim@shareability.com"],
		"operationId":"mac-human-group-launch-crew"
	}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("membership-changing retry status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	if got := countStoredScoutChatThreads(kanbanApp); got != 1 {
		t.Fatalf("stored chat threads=%d, want one after retries", got)
	}

	for _, member := range []string{"aj@shareability.com", "tim@shareability.com", "caitlyn@shareability.com"} {
		listedThreads := listScoutChatThreadsAs(t, member)
		if !scoutChatListContains(listedThreads, created.Thread.ID) {
			t.Fatalf("exact member %s could not list group", member)
		}
		for _, listed := range listedThreads {
			if asString(listed["id"]) == created.Thread.ID && (asString(listed["conversationKind"]) != scoutChatConversationKindHumanGroup || len(asStringSlice(listed["memberEmails"])) != len(wantMembers)) {
				t.Fatalf("member %s received untruthful group metadata: %#v", member, listed)
			}
		}
		read := requestScoutChatThreadAs(t, member, http.MethodGet, "/assistant/chat-threads/"+created.Thread.ID, "")
		if read.Code != http.StatusOK {
			t.Fatalf("exact member %s read status=%d body=%s", member, read.Code, read.Body.String())
		}
	}

	post := requestScoutChatThreadAs(t, "tim@shareability.com", http.MethodPost, "/assistant/chat-threads/"+created.Thread.ID+"/messages", `{
		"text":"hello from Tim",
		"operationId":"human-group-tim-message-one"
	}`)
	if post.Code != http.StatusOK {
		t.Fatalf("member post status=%d body=%s", post.Code, post.Body.String())
	}
	saved, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", created.Thread.ID)
	if err != nil || len(saved.Messages) != 1 || saved.Messages[0].AuthorEmail != "tim@shareability.com" || saved.Messages[0].Text != "hello from Tim" {
		t.Fatalf("saved member message=%+v err=%v", saved.Messages, err)
	}

	outsider := "tom@shareability.com"
	if scoutChatListContains(listScoutChatThreadsAs(t, outsider), created.Thread.ID) {
		t.Fatal("outsider could list exact-member human group")
	}
	if read := requestScoutChatThreadAs(t, outsider, http.MethodGet, "/assistant/chat-threads/"+created.Thread.ID, ""); read.Code != http.StatusNotFound {
		t.Fatalf("outsider read status=%d body=%s", read.Code, read.Body.String())
	}
	if write := requestScoutChatThreadAs(t, outsider, http.MethodPost, "/assistant/chat-threads/"+created.Thread.ID+"/messages", `{"text":"intrusion","operationId":"outsider-human-group-post"}`); write.Code != http.StatusNotFound {
		t.Fatalf("outsider post status=%d body=%s", write.Code, write.Body.String())
	}
	if after, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", created.Thread.ID); err != nil || len(after.Messages) != 1 {
		t.Fatalf("outsider changed durable messages=%d err=%v", len(after.Messages), err)
	}
}

func TestAssistantHumanGroupValidationFailsBeforeCreation(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	overCap := make([]string, 0, scoutChatHumanGroupMaxMembersIncludingOwner)
	for index := 0; index < scoutChatHumanGroupMaxMembersIncludingOwner; index++ {
		overCap = append(overCap, fmt.Sprintf("member-%02d@example.test", index))
	}
	overCapJSON, err := json.Marshal(overCap)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		body string
	}{
		{"unknown member", `{"title":"Unknown","conversationKind":"human_group","memberEmails":["nobody@example.test"]}`},
		{"agent id", `{"title":"Agent","conversationKind":"human_group","memberEmails":["agent_researcher"]}`},
		{"scout id", `{"title":"Scout","conversationKind":"human_group","memberEmails":["scout"]}`},
		{"self only", `{"title":"Solo","conversationKind":"human_group","memberEmails":["aj@shareability.com"]}`},
		{"missing kind", `{"title":"Implicit","memberEmails":["tim@shareability.com"]}`},
		{"private group", `{"title":"Wrong storage","visibility":"private","conversationKind":"human_group","memberEmails":["tim@shareability.com"]}`},
		{"unnamed group", `{"conversationKind":"human_group","memberEmails":["tim@shareability.com"]}`},
		{"unsupported kind", `{"title":"Wrong kind","conversationKind":"direct","memberEmails":["tim@shareability.com"]}`},
		{"over cap", `{"title":"Too large","conversationKind":"human_group","memberEmails":` + string(overCapJSON) + `}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := countStoredScoutChatThreads(kanbanApp)
			response := postScoutChatThreadAs(t, "aj@shareability.com", test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if after := countStoredScoutChatThreads(kanbanApp); after != before {
				t.Fatalf("invalid request created a thread: before=%d after=%d", before, after)
			}
		})
	}
}

func TestAssistantHumanGroupContractPreservesPublicAndPrivateBehavior(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	publicResponse := postScoutChatThreadAs(t, "aj@shareability.com", `{"title":"Office channel","visibility":"public","operationId":"legacy-public-create"}`)
	privateResponse := postScoutChatThreadAs(t, "aj@shareability.com", `{"title":"Scout notes","visibility":"private","operationId":"legacy-private-create"}`)
	if publicResponse.Code != http.StatusCreated || privateResponse.Code != http.StatusCreated {
		t.Fatalf("ordinary create statuses public=%d private=%d", publicResponse.Code, privateResponse.Code)
	}
	var publicCreated, privateCreated scoutChatCreateResponse
	if err := json.Unmarshal(publicResponse.Body.Bytes(), &publicCreated); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(privateResponse.Body.Bytes(), &privateCreated); err != nil {
		t.Fatal(err)
	}
	if publicCreated.Thread.ConversationKind != "" || len(publicCreated.Thread.MemberEmails) != 0 || privateCreated.Thread.ConversationKind != "" || len(privateCreated.Thread.MemberEmails) != 0 {
		t.Fatalf("ordinary thread response changed: public=%+v private=%+v", publicCreated.Thread, privateCreated.Thread)
	}
	if response := requestScoutChatThreadAs(t, "tom@shareability.com", http.MethodGet, "/assistant/chat-threads/"+publicCreated.Thread.ID, ""); response.Code != http.StatusOK {
		t.Fatalf("ordinary public thread no longer organization-wide: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := requestScoutChatThreadAs(t, "tom@shareability.com", http.MethodGet, "/assistant/chat-threads/"+privateCreated.Thread.ID, ""); response.Code != http.StatusNotFound {
		t.Fatalf("ordinary private thread leaked: status=%d body=%s", response.Code, response.Body.String())
	}
}
