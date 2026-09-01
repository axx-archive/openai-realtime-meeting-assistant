package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func patchScoutChatMembersAs(t *testing.T, email, threadID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return requestScoutChatThreadAs(t, email, http.MethodPatch, "/assistant/chat-threads/"+threadID+"/members", body)
}

func createScoutChatHumanGroupForTest(t *testing.T, owner, title string, members ...string) scoutChatThreadRecord {
	t.Helper()
	encoded, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	response := postScoutChatThreadAs(t, owner, `{"title":`+strconv.Quote(title)+`,"conversationKind":"human_group","memberEmails":`+string(encoded)+`}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create human group status=%d body=%s", response.Code, response.Body.String())
	}
	var created scoutChatCreateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.Thread
}

func decodeScoutChatMembersResponse(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("members status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		OK     bool           `json:"ok"`
		Thread map[string]any `json:"thread"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode members response: %v body=%s", err, response.Body.String())
	}
	if !payload.OK || payload.Thread == nil {
		t.Fatalf("members response=%s, want ok thread view", response.Body.String())
	}
	return payload.Thread
}

func storedScoutChatMembers(t *testing.T, viewer, threadID string) []string {
	t.Helper()
	thread, _, err := kanbanApp.scoutChatThreadByID(viewer, threadID)
	if err != nil {
		t.Fatalf("load thread %s as %s: %v", threadID, viewer, err)
	}
	return scoutChatThreadMemberEmails(thread)
}

func TestScoutChatMembersOwnerAddsAndRemovesMembers(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner, tim, caitlyn := "aj@shareability.com", "tim@shareability.com", "caitlyn@shareability.com"
	group := createScoutChatHumanGroupForTest(t, owner, "Launch crew", tim)

	added := decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, group.ID, `{"add":["Caitlyn@shareability.com"]}`))
	if got := strings.Join(asStringSlice(added["memberEmails"]), ","); got != owner+","+caitlyn+","+tim {
		t.Fatalf("memberEmails after add=%q", got)
	}
	if asString(added["conversationKind"]) != scoutChatConversationKindHumanGroup || asString(added["visibility"]) != scoutChatVisibilityPublic {
		t.Fatalf("mutation view lost kind/visibility: %#v", added)
	}
	for _, member := range []string{owner, tim, caitlyn} {
		if !scoutChatListContains(listScoutChatThreadsAs(t, member), group.ID) {
			t.Fatalf("member %s cannot list the group after add", member)
		}
		if read := requestScoutChatThreadAs(t, member, http.MethodGet, "/assistant/chat-threads/"+group.ID, ""); read.Code != http.StatusOK {
			t.Fatalf("member %s read status=%d body=%s", member, read.Code, read.Body.String())
		}
	}

	removed := decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, group.ID, `{"remove":["tim@shareability.com"]}`))
	if got := strings.Join(asStringSlice(removed["memberEmails"]), ","); got != owner+","+caitlyn {
		t.Fatalf("memberEmails after remove=%q", got)
	}
	if scoutChatListContains(listScoutChatThreadsAs(t, tim), group.ID) {
		t.Fatal("removed member can still list the group")
	}
	// The thread GET is deliberately non-enumerating for non-viewers: a removed
	// member gets the same opaque 404 an outsider gets, never a 403 that would
	// confirm the thread exists.
	if read := requestScoutChatThreadAs(t, tim, http.MethodGet, "/assistant/chat-threads/"+group.ID, ""); read.Code != http.StatusNotFound {
		t.Fatalf("removed member read status=%d body=%s", read.Code, read.Body.String())
	}
	if read := requestScoutChatThreadAs(t, caitlyn, http.MethodGet, "/assistant/chat-threads/"+group.ID, ""); read.Code != http.StatusOK {
		t.Fatalf("remaining member read status=%d body=%s", read.Code, read.Body.String())
	}
	if got := strings.Join(storedScoutChatMembers(t, owner, group.ID), ","); got != owner+","+caitlyn {
		t.Fatalf("durable memberEmails=%q", got)
	}
}

func TestScoutChatMembersRejectsNonOwnerAndOutsider(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner, tim := "aj@shareability.com", "tim@shareability.com"
	group := createScoutChatHumanGroupForTest(t, owner, "Launch crew", tim)

	if response := patchScoutChatMembersAs(t, tim, group.ID, `{"add":["caitlyn@shareability.com"]}`); response.Code != http.StatusForbidden {
		t.Fatalf("member non-owner status=%d body=%s", response.Code, response.Body.String())
	}
	if response := patchScoutChatMembersAs(t, "tom@shareability.com", group.ID, `{"add":["caitlyn@shareability.com"]}`); response.Code != http.StatusNotFound {
		t.Fatalf("outsider status=%d body=%s", response.Code, response.Body.String())
	}
	if response := patchScoutChatMembersAs(t, owner, "scout-chat-does-not-exist", `{"add":["caitlyn@shareability.com"]}`); response.Code != http.StatusNotFound {
		t.Fatalf("unknown thread status=%d body=%s", response.Code, response.Body.String())
	}
	if got := strings.Join(storedScoutChatMembers(t, owner, group.ID), ","); got != owner+","+tim {
		t.Fatalf("rejected requests changed membership: %q", got)
	}
}

func TestScoutChatMembersRefusesToShrinkBelowTwoHumans(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner, tim := "aj@shareability.com", "tim@shareability.com"
	group := createScoutChatHumanGroupForTest(t, owner, "Launch crew", tim)

	response := patchScoutChatMembersAs(t, owner, group.ID, `{"remove":["tim@shareability.com"]}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "at least one other member") {
		t.Fatalf("owner-only shrink status=%d body=%s", response.Code, response.Body.String())
	}
	// The owner is never removable: asking to drop them is a no-op success.
	kept := decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, group.ID, `{"remove":["aj@shareability.com"]}`))
	if got := strings.Join(asStringSlice(kept["memberEmails"]), ","); got != owner+","+tim {
		t.Fatalf("owner removal changed membership: %q", got)
	}
	if got := strings.Join(storedScoutChatMembers(t, owner, group.ID), ","); got != owner+","+tim {
		t.Fatalf("durable membership after refused shrink: %q", got)
	}
}

func TestScoutChatMembersRejectsUnregisteredWithoutNamingAccounts(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner, tim := "aj@shareability.com", "tim@shareability.com"
	group := createScoutChatHumanGroupForTest(t, owner, "Launch crew", tim)

	response := patchScoutChatMembersAs(t, owner, group.ID, `{"add":["nobody@example.test","caitlyn@shareability.com"]}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unregistered add status=%d body=%s", response.Code, response.Body.String())
	}
	body := strings.ToLower(response.Body.String())
	for _, leaked := range []string{"nobody@example.test", "caitlyn"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("error names an account (%q): %s", leaked, response.Body.String())
		}
	}
	if got := strings.Join(storedScoutChatMembers(t, owner, group.ID), ","); got != owner+","+tim {
		t.Fatalf("rejected add changed membership: %q", got)
	}
}

func TestScoutChatMembersRejectsThreadsWithoutExplicitMembership(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner := "aj@shareability.com"
	cases := []struct {
		name string
		body string
	}{
		{"private thread", `{"title":"Scout notes","visibility":"private"}`},
		{"organization channel", `{"title":"Office channel","visibility":"public"}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			createResponse := postScoutChatThreadAs(t, owner, test.body)
			if createResponse.Code != http.StatusCreated {
				t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
			}
			var created scoutChatCreateResponse
			if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			response := patchScoutChatMembersAs(t, owner, created.Thread.ID, `{"add":["tim@shareability.com"]}`)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "thread has no explicit membership") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			after, _, err := kanbanApp.scoutChatThreadByID(owner, created.Thread.ID)
			if err != nil || len(after.MemberEmails) != 0 {
				t.Fatalf("thread gained membership: members=%v err=%v", after.MemberEmails, err)
			}
		})
	}
}

func TestScoutChatMembersIsIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner, tim := "aj@shareability.com", "tim@shareability.com"
	group := createScoutChatHumanGroupForTest(t, owner, "Launch crew", tim)

	replay := decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, group.ID, `{"add":["TIM@shareability.com"]}`))
	if got := strings.Join(asStringSlice(replay["memberEmails"]), ","); got != owner+","+tim {
		t.Fatalf("idempotent add changed membership: %q", got)
	}
	if asString(replay["updatedAt"]) != group.UpdatedAt {
		t.Fatalf("no-op add touched updatedAt: %q -> %q", group.UpdatedAt, asString(replay["updatedAt"]))
	}
	if response := patchScoutChatMembersAs(t, owner, group.ID, `{}`); response.Code != http.StatusBadRequest {
		t.Fatalf("empty delta status=%d body=%s", response.Code, response.Body.String())
	}
	if response := patchScoutChatMembersAs(t, owner, group.ID, `not json`); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed body status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestScoutChatMembersProjectThreadAllowsOwnerOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner, tim, caitlyn := "aj@shareability.com", "tim@shareability.com", "caitlyn@shareability.com"
	// A member-scoped public thread with no conversationKind is the project
	// thread shape: explicit membership, but no two-human floor.
	project, err := kanbanApp.createScoutChatThreadRecord("scout-chat-project-members", owner, "AJ", "Project: Atlas", scoutChatVisibilityPublic, []string{tim}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(scoutChatThreadMemberEmails(project), ","); got != owner+","+tim {
		t.Fatalf("project fixture members=%q", got)
	}

	added := decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, project.ID, `{"add":["caitlyn@shareability.com"],"remove":["tim@shareability.com"]}`))
	if got := strings.Join(asStringSlice(added["memberEmails"]), ","); got != owner+","+caitlyn {
		t.Fatalf("project members after swap=%q", got)
	}
	if _, present := added["conversationKind"]; present {
		t.Fatalf("project thread grew a conversationKind: %#v", added)
	}
	shrunk := decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, project.ID, `{"remove":["caitlyn@shareability.com"]}`))
	if got := strings.Join(asStringSlice(shrunk["memberEmails"]), ","); got != owner {
		t.Fatalf("project thread could not shrink to its owner: %q", got)
	}
	if scoutChatListContains(listScoutChatThreadsAs(t, tim), project.ID) || scoutChatListContains(listScoutChatThreadsAs(t, caitlyn), project.ID) {
		t.Fatal("owner-only project thread is still listed for removed members")
	}
	if response := patchScoutChatMembersAs(t, owner, project.ID, `{"add":["nobody@example.test"]}`); response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "nobody") {
		t.Fatalf("project unregistered add status=%d body=%s", response.Code, response.Body.String())
	}
}

// Creation and membership edits must reach members' sidebars live, not after
// the index poll. Observed on real office sockets, the way
// TestMemberScopedProjectUpdatesReachExactOfficeSockets does. Each phase ends
// with ONE public marker broadcast that every socket drains to: members have
// already consumed their group event, and a non-member's socket must show
// nothing but the marker — the broadcast bounds the negative check without
// a sleep.
func TestScoutChatHumanGroupCreationAndMembershipFanOutToExactMembers(t *testing.T) {
	resetAuthRateLimitersForTest()
	server := newIsolatedWebsocketServer(t)
	owner, tim, caitlyn, tom := "aj@shareability.com", "tim@shareability.com", "caitlyn@shareability.com", "tom@shareability.com"
	everyone := []string{owner, tim, caitlyn, tom}
	sockets := map[string]*websocket.Conn{}
	for _, email := range everyone {
		conn := dialIsolatedWebsocket(t, server, email)
		sendOfficeHello(t, conn)
		waitForKanbanEvent(t, conn, "codex_proposals", 5*time.Second)
		sockets[email] = conn
	}
	expectThreadEvent := func(email, threadID, wantMember, wantAbsent string) {
		t.Helper()
		raw := string(waitForKanbanEvent(t, sockets[email], "chat_thread", 5*time.Second))
		if !strings.Contains(raw, threadID) {
			t.Fatalf("%s next chat_thread event is not the group: %s", email, raw)
		}
		if wantMember != "" && !strings.Contains(raw, wantMember) {
			t.Fatalf("%s event memberEmails missing %s: %s", email, wantMember, raw)
		}
		if wantAbsent != "" && strings.Contains(raw, wantAbsent) {
			t.Fatalf("%s event memberEmails still lists %s: %s", email, wantAbsent, raw)
		}
	}
	endPhase := func(marker, threadID string, nonMembers ...string) {
		t.Helper()
		broadcastSignedInKanbanEvent("chat_thread", map[string]any{"id": marker, "visibility": "public"})
		forbidden := map[string]bool{}
		for _, email := range nonMembers {
			forbidden[email] = true
		}
		for _, email := range everyone {
			for {
				raw := string(waitForKanbanEvent(t, sockets[email], "chat_thread", 5*time.Second))
				if forbidden[email] && strings.Contains(raw, threadID) {
					t.Fatalf("%s received a group event they are not a member of: %s", email, raw)
				}
				if strings.Contains(raw, marker) {
					break
				}
			}
		}
	}

	group := createScoutChatHumanGroupForTest(t, owner, "Launch crew", tim)
	expectThreadEvent(owner, group.ID, tim, "")
	expectThreadEvent(tim, group.ID, tim, "")
	endPhase("creation-marker", group.ID, caitlyn, tom)

	decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, group.ID, `{"add":["caitlyn@shareability.com"]}`))
	expectThreadEvent(owner, group.ID, caitlyn, "")
	expectThreadEvent(tim, group.ID, caitlyn, "")
	expectThreadEvent(caitlyn, group.ID, caitlyn, "")
	endPhase("add-marker", group.ID, tom)

	decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, group.ID, `{"remove":["tim@shareability.com"]}`))
	// The ejected member receives only a minimal removal notice — never the
	// post-removal roster — and drops the row on removed:true.
	removedRaw := string(waitForKanbanEvent(t, sockets[tim], "chat_thread", 5*time.Second))
	if !strings.Contains(removedRaw, group.ID) || !strings.Contains(removedRaw, `"removed":true`) || strings.Contains(removedRaw, "memberEmails") || strings.Contains(removedRaw, caitlyn) {
		t.Fatalf("removed member notice=%s, want id + removed:true and no roster", removedRaw)
	}
	expectThreadEvent(owner, group.ID, caitlyn, tim)
	expectThreadEvent(caitlyn, group.ID, caitlyn, tim)
	endPhase("remove-marker", group.ID, tom, tim)
}

func TestScoutChatMembersProjectThreadHonorsMemberCap(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner, tim := "aj@shareability.com", "tim@shareability.com"
	project, err := kanbanApp.createScoutChatThreadRecord("scout-chat-project-cap", owner, "AJ", "Project: Atlas", scoutChatVisibilityPublic, []string{tim}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	overCap := make([]string, 0, scoutChatHumanGroupMaxMembersIncludingOwner)
	for index := 0; index < scoutChatHumanGroupMaxMembersIncludingOwner; index++ {
		overCap = append(overCap, fmt.Sprintf("member-%02d@example.test", index))
	}
	encoded, err := json.Marshal(overCap)
	if err != nil {
		t.Fatal(err)
	}
	response := patchScoutChatMembersAs(t, owner, project.ID, `{"add":`+string(encoded)+`}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "at most") {
		t.Fatalf("over-cap project add status=%d body=%s", response.Code, response.Body.String())
	}
	if got := strings.Join(storedScoutChatMembers(t, owner, project.ID), ","); got != owner+","+tim {
		t.Fatalf("over-cap add changed membership: %q", got)
	}
}

// A membership change is an audience change for already-committed source
// episodes, exactly like archive/restore: their authority snapshot must follow
// the new roster rather than fail closed for everyone.
func TestScoutChatMembersRefreshSourceEpisodeAuthority(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	if kanbanApp.sourceEpisodes == nil || kanbanApp.sourceEpisodesErr != nil {
		t.Fatalf("isolated app has no source-episode ledger: %v", kanbanApp.sourceEpisodesErr)
	}

	owner, tim, caitlyn := "aj@shareability.com", "tim@shareability.com", "caitlyn@shareability.com"
	group := createScoutChatHumanGroupForTest(t, owner, "Launch crew", tim)
	message := scoutChatMessageRecord{ID: "group-message-1", Kind: "message", Role: "user", Text: "roster snapshot", AuthorName: "AJ", AuthorEmail: owner, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	saved, err := kanbanApp.commitScoutChatThreadMessages(owner, group.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	kanbanApp.reconcileConversationSourceEpisodeAuthority(saved)
	episodeID := conversationSourceEpisodeID(group.ID, message.ID)
	before, found, err := kanbanApp.sourceEpisodes.CurrentSourceEpisode(context.Background(), threadTenantID(saved), episodeID)
	if err != nil || !found {
		t.Fatalf("committed group message has no source episode: found=%v err=%v", found, err)
	}
	timPrincipal, caitlynPrincipal := strideRuntimePrincipalForEmail(tim), strideRuntimePrincipalForEmail(caitlyn)
	if !containsString(before.Authority.Audience.Principals, timPrincipal) || containsString(before.Authority.Audience.Principals, caitlynPrincipal) {
		t.Fatalf("pre-change audience=%v", before.Authority.Audience.Principals)
	}

	decodeScoutChatMembersResponse(t, patchScoutChatMembersAs(t, owner, group.ID, `{"add":["caitlyn@shareability.com"],"remove":["tim@shareability.com"]}`))
	after, found, err := kanbanApp.sourceEpisodes.CurrentSourceEpisode(context.Background(), threadTenantID(saved), episodeID)
	if err != nil || !found {
		t.Fatalf("source episode lost after membership change: found=%v err=%v", found, err)
	}
	if !containsString(after.Authority.Audience.Principals, caitlynPrincipal) || containsString(after.Authority.Audience.Principals, timPrincipal) || after.Authority.ACLDigest == before.Authority.ACLDigest {
		t.Fatalf("audience did not follow the roster: before=%v after=%v", before.Authority.Audience.Principals, after.Authority.Audience.Principals)
	}
}

func TestScoutChatHumanGroupPreviewReadsNewGroup(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	owner, tim := "aj@shareability.com", "tim@shareability.com"
	group := createScoutChatHumanGroupForTest(t, owner, "Launch crew", tim)
	if group.Preview != "new group" {
		t.Fatalf("created group preview=%q, want \"new group\"", group.Preview)
	}
	for _, member := range []string{owner, tim} {
		found := false
		for _, row := range listScoutChatThreadsAs(t, member) {
			if asString(row["id"]) != group.ID {
				continue
			}
			found = true
			if asString(row["preview"]) != "new group" {
				t.Fatalf("index row preview for %s=%q, want \"new group\"", member, asString(row["preview"]))
			}
		}
		if !found {
			t.Fatalf("member %s index is missing the group", member)
		}
	}
	// Ordinary channels keep their copy.
	channel := postScoutChatThreadAs(t, owner, `{"title":"Office channel","visibility":"public"}`)
	var created scoutChatCreateResponse
	if err := json.Unmarshal(channel.Body.Bytes(), &created); err != nil || created.Thread.Preview != "new team channel" {
		t.Fatalf("channel preview=%q err=%v, want \"new team channel\"", created.Thread.Preview, err)
	}
}
