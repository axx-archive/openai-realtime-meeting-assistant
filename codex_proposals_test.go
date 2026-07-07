package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The board-worker tool: propose_codex_task is whitelisted, instructed, and
// its executor writes a codex_proposal memory entry plus a broadcast
// notification — without launching any agent thread.
func TestProposeCodexTaskToolCreatesProposalAndNotifiesEveryone(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	if !meetingBoardToolAllowed("propose_codex_task") {
		t.Fatal("board worker must whitelist propose_codex_task")
	}
	instructions := meetingBoardInstructions()
	for _, want := range []string{"propose_codex_task", "never auto-run", "read-only"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("board instructions missing %q", want)
		}
	}

	result, changed, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Rodeo creator landscape brief",
		"mode":  "research",
		"query": "Research the rodeo creator landscape and draft a brief with sources.",
	})
	if err != nil {
		t.Fatalf("propose_codex_task: %v", err)
	}
	if changed {
		t.Fatal("proposing must not report a board change")
	}
	if result["ok"] != true {
		t.Fatalf("result=%#v, want ok", result)
	}
	proposal, ok := result["proposal"].(map[string]any)
	if !ok {
		t.Fatalf("result proposal=%#v, want payload map", result["proposal"])
	}
	if proposal["status"] != codexProposalStatusProposed || proposal["mode"] != "research" {
		t.Fatalf("proposal=%#v, want proposed research task", proposal)
	}
	if launches != 0 {
		t.Fatalf("launches=%d, proposing must never start an agent thread", launches)
	}

	// Durable entry with the decided metadata shape.
	entry, found := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, asString(proposal["id"]))
	if !found {
		t.Fatalf("codex_proposal entry %v not persisted", proposal["id"])
	}
	if entry.Metadata["title"] != "Rodeo creator landscape brief" || entry.Metadata["proposedBy"] != "board_worker" {
		t.Fatalf("entry metadata=%#v, want title + proposedBy=board_worker", entry.Metadata)
	}
	if entry.Metadata["status"] != codexProposalStatusProposed {
		t.Fatalf("entry status=%q, want proposed", entry.Metadata["status"])
	}

	// Everyone-notification with the confirm-to-launch nudge, routed to the
	// room where the proposal cards live.
	app.mu.Lock()
	records := append([]notificationRecord(nil), app.notifications...)
	app.mu.Unlock()
	if len(records) != 1 {
		t.Fatalf("notifications=%d, want exactly the proposal broadcast", len(records))
	}
	record := records[0]
	if record.UserEmail != "" || record.Kind != notificationKindTask || record.Tool != "room" {
		t.Fatalf("notification=%#v, want everyone task routed to room", record)
	}
	if !strings.Contains(record.Text, "Rodeo creator landscape brief") || !strings.Contains(record.Text, "confirm to launch") {
		t.Fatalf("notification text=%q, want Scout proposes ... confirm to launch", record.Text)
	}
	if record.ProposalID != asString(proposal["id"]) {
		t.Fatalf("notification proposalID=%q, want the proposal linkage %v", record.ProposalID, proposal["id"])
	}

	// The snapshot replayed on websocket admission carries the proposal.
	snapshot := app.codexProposalsSnapshot(codexProposalHistoryLimit)
	if len(snapshot) != 1 || snapshot[0]["id"] != proposal["id"] {
		t.Fatalf("snapshot=%#v, want the new proposal", snapshot)
	}

	// Validation errors surface through err (the board worker records them as
	// operation errors).
	for name, args := range map[string]map[string]any{
		"missing title": {"mode": "research", "query": "draft a brief"},
		"bad mode":      {"title": "x", "mode": "pitch", "query": "draft a brief"},
		"missing query": {"title": "x", "mode": "design"},
	} {
		if _, _, err := app.applyToolCallArgs("propose_codex_task", args); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

// Card 067/068: propose_codex_task captures an optional thread_id as the
// originThreadId routing hint (only when it resolves to a live public channel),
// and the proposal payload surfaces the lane / originThreadId / laneApprovedBy
// keys the ticker and the proposal deck read.
func TestProposeCodexTaskCapturesOriginThreadAndLanePayload(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// A valid public thread_id is captured as originThreadId.
	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title":     "Growth levers brief",
		"mode":      "research",
		"query":     "Research growth levers and draft a brief.",
		"thread_id": channel.ID,
	})
	if err != nil {
		t.Fatalf("propose with thread_id: %v", err)
	}
	id := asString(result["proposal"].(map[string]any)["id"])
	entry, _ := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, id)
	if entry.Metadata["originThreadId"] != channel.ID {
		t.Fatalf("originThreadId=%q, want the captured channel %q", entry.Metadata["originThreadId"], channel.ID)
	}

	// An unknown thread_id is silently dropped (no bad linkage).
	badResult, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title":     "No such channel brief",
		"mode":      "research",
		"query":     "Research something and draft a brief.",
		"thread_id": "scout-chat-does-not-exist",
	})
	if err != nil {
		t.Fatalf("propose with bad thread_id: %v", err)
	}
	badID := asString(badResult["proposal"].(map[string]any)["id"])
	badEntry, _ := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, badID)
	if _, present := badEntry.Metadata["originThreadId"]; present {
		t.Fatalf("originThreadId=%q, want no linkage for an unknown thread_id", badEntry.Metadata["originThreadId"])
	}

	// The payload surfaces the lane governance keys 069 writes.
	laneEntry, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindCodexProposal, id, entry.Text, map[string]string{
		"lane":           codexProposalLaneAutoRun,
		"laneApprovedBy": "aj@shareability.com",
	})
	if err != nil {
		t.Fatalf("stamp lane metadata: %v", err)
	}
	payload := codexProposalPayload(laneEntry)
	if payload["lane"] != codexProposalLaneAutoRun || payload["originThreadId"] != channel.ID || payload["laneApprovedBy"] != "aj@shareability.com" {
		t.Fatalf("payload=%#v, want lane/originThreadId/laneApprovedBy surfaced", payload)
	}

	if launches != 0 {
		t.Fatalf("launches=%d, proposing must never start an agent thread", launches)
	}
}

// Contract test alongside TestRealtimeSendNotificationToolContract: the
// schema, both private allowlists, and both instruction strings must expose
// propose_codex_task to Scout's voice, and the private voice path must stamp
// the requesting user as the proposal's provenance.
func TestRealtimeProposeCodexTaskToolContract(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	toolsJSON := string(rawTools)
	for _, want := range []string{`"name":"propose_codex_task"`, `"title"`, `"query"`, "human must confirm"} {
		if !strings.Contains(toolsJSON, want) {
			t.Fatalf("tools JSON missing %s", want)
		}
	}

	if !privateRealtimeVoiceToolAllowed("propose_codex_task") {
		t.Fatal("private realtime voice must allow propose_codex_task")
	}
	foundPrivate := false
	for _, tool := range app.privateRealtimeVoiceTools() {
		if asString(tool["name"]) == "propose_codex_task" {
			foundPrivate = true
		}
	}
	if !foundPrivate {
		t.Fatal("privateRealtimeVoiceTools must expose the propose_codex_task schema")
	}

	if !strings.Contains(app.sessionInstructions(), "propose_codex_task") {
		t.Fatal("room session instructions must mention propose_codex_task")
	}
	if !strings.Contains(app.privateRealtimeVoiceSessionInstructions(), "propose_codex_task") {
		t.Fatal("private voice instructions must mention propose_codex_task")
	}

	// Private path: the proposal records the requesting account, never
	// board_worker, and still only proposes — no launch.
	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	result, changed, err := app.applyPrivateRealtimeVoiceTool("AJ@Shareability.com", "propose_codex_task", map[string]any{
		"title": "Comparable exits brief",
		"mode":  "research",
		"query": "Research comparable exits for creator-led IP and draft a brief.",
	})
	if err != nil {
		t.Fatalf("private propose_codex_task: %v", err)
	}
	if changed {
		t.Fatal("proposing must not report a board change")
	}
	proposal, ok := result["proposal"].(map[string]any)
	if !ok {
		t.Fatalf("result proposal=%#v, want payload map", result["proposal"])
	}
	if proposal["proposedBy"] != "aj@shareability.com" {
		t.Fatalf("proposedBy=%v, want the normalized requesting account", proposal["proposedBy"])
	}
	if proposal["status"] != codexProposalStatusProposed {
		t.Fatalf("status=%v, want proposed", proposal["status"])
	}
	if launches != 0 {
		t.Fatalf("launches=%d, voice proposals must never start an agent thread", launches)
	}
}

// Proposals are UI state, not knowledge: excluded from Scout search context
// (like scout_chat_thread) and from the generic client memory timeline.
func TestCodexProposalExcludedFromSearchAndClientMemory(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Zanzibar pricing brief",
		"mode":  "research",
		"query": "Research zanzibar pricing tiers and summarize.",
	}); err != nil {
		t.Fatalf("propose_codex_task: %v", err)
	}
	if _, appended, err := app.memory.appendTranscript("event-zanzibar", "item-1", "AJ said zanzibar pricing needs a revisit"); err != nil || !appended {
		t.Fatalf("append transcript: appended=%v err=%v", appended, err)
	}

	matches := app.memory.search("zanzibar pricing", 10)
	if len(matches) != 1 {
		t.Fatalf("matches=%d, want only the transcript", len(matches))
	}
	if matches[0].Entry.Kind != meetingMemoryKindTranscript {
		t.Fatalf("match kind=%q, want transcript (codex_proposal must stay out of search context)", matches[0].Entry.Kind)
	}

	for _, entry := range app.memorySnapshotForClients(50) {
		if entry.Kind == meetingMemoryKindCodexProposal {
			t.Fatal("codex_proposal leaked into the generic client memory timeline")
		}
	}
}

// POST /assistant/proposals/{id}/action: auth guards, confirm launches an
// agent thread as the confirming user, dismiss settles without launching, and
// a double confirm is idempotent (exactly one thread).
func TestCodexProposalActionEndpoint(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	propose := func(title string) string {
		t.Helper()
		result, _, err := kanbanApp.applyToolCallArgs("propose_codex_task", map[string]any{
			"title": title,
			"mode":  "research",
			"query": "Research " + title + " and draft a brief.",
		})
		if err != nil {
			t.Fatalf("propose %s: %v", title, err)
		}
		return asString(result["proposal"].(map[string]any)["id"])
	}

	act := func(id string, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/assistant/proposals/"+id+"/action", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantProposalActionHandler(recorder, req)
		return recorder
	}

	decode := func(recorder *httptest.ResponseRecorder) (map[string]any, bool) {
		t.Helper()
		var payload struct {
			Proposal map[string]any `json:"proposal"`
			Launched bool           `json:"launched"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode proposal response %s: %v", recorder.Body.String(), err)
		}
		return payload.Proposal, payload.Launched
	}

	proposalID := propose("Creator landscape")

	// Signed-out requests never reach the store.
	if recorder := act(proposalID, `{"action":"confirm"}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if launches != 0 {
		t.Fatalf("launches=%d after unauth request, want 0", launches)
	}

	tomCookies := loginAs(t, "tom@shareability.com", "B0NFIRE!")
	tomName := participantNameForEmail("tom@shareability.com")

	// Confirm: any signed-in user; the thread runs as the confirming user.
	recorder := act(proposalID, `{"action":"confirm"}`, tomCookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	proposal, launched := decode(recorder)
	if !launched || proposal["status"] != codexProposalStatusConfirmed {
		t.Fatalf("confirm response=%#v launched=%v, want confirmed+launched", proposal, launched)
	}
	if proposal["confirmedBy"] != tomName {
		t.Fatalf("confirmedBy=%v, want %q", proposal["confirmedBy"], tomName)
	}
	if launches != 1 {
		t.Fatalf("launches=%d, want 1", launches)
	}
	artifactID := asString(proposal["threadArtifactId"])
	if artifactID == "" {
		t.Fatal("confirmed proposal must link its thread artifact")
	}
	artifact, found := kanbanApp.osArtifactByID(artifactID)
	if !found {
		t.Fatalf("thread artifact %q not found", artifactID)
	}
	if artifact.Metadata["createdBy"] != tomName {
		t.Fatalf("artifact createdBy=%q, want the confirming user %q", artifact.Metadata["createdBy"], tomName)
	}

	// Double confirm is idempotent: settled state, no second thread.
	recorder = act(proposalID, `{"action":"confirm"}`, loginAs(t, "aj@shareability.com", "B0NFIRE!"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("double confirm status=%d, want 200", recorder.Code)
	}
	proposal, launched = decode(recorder)
	if launched || proposal["status"] != codexProposalStatusConfirmed || proposal["confirmedBy"] != tomName {
		t.Fatalf("double confirm=%#v launched=%v, want the original confirmation unchanged", proposal, launched)
	}
	if launches != 1 {
		t.Fatalf("launches=%d after double confirm, want still 1", launches)
	}

	// Dismiss settles a pending proposal without launching anything.
	dismissID := propose("Pricing teardown")
	recorder = act(dismissID, `{"action":"dismiss"}`, tomCookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dismiss status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	proposal, launched = decode(recorder)
	if launched || proposal["status"] != codexProposalStatusDismissed || proposal["dismissedBy"] != tomName {
		t.Fatalf("dismiss=%#v launched=%v, want dismissed by tom without a launch", proposal, launched)
	}
	if launches != 1 {
		t.Fatalf("launches=%d after dismiss, want still 1", launches)
	}
	// Confirm after dismiss stays settled.
	recorder = act(dismissID, `{"action":"confirm"}`, tomCookies)
	proposal, launched = decode(recorder)
	if recorder.Code != http.StatusOK || launched || proposal["status"] != codexProposalStatusDismissed {
		t.Fatalf("confirm-after-dismiss status=%d proposal=%#v, want settled dismissal", recorder.Code, proposal)
	}

	// Validation and lookup failures.
	if recorder := act(proposalID, `{"action":"shipit"}`, tomCookies); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad action status=%d, want 400", recorder.Code)
	}
	if recorder := act("codex-proposal-missing", `{"action":"confirm"}`, tomCookies); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing proposal status=%d, want 404", recorder.Code)
	}
	if launches != 1 {
		t.Fatalf("launches=%d at end, want exactly one confirmed launch", launches)
	}
}

// Confirm persists the settled status BEFORE the agent thread launches: a
// failed post-launch metadata update can then only lose the thread linkage,
// never leave the proposal 'proposed' where a retry would double-launch. A
// failed launch reverts the proposal to proposed so a human can retry.
func TestCodexProposalConfirmPersistsBeforeLaunch(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	var proposalID string
	statusAtLaunch := ""
	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		launches++
		if entry, ok := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, proposalID); ok {
			statusAtLaunch = entry.Metadata["status"]
		}
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Ordering guard",
		"mode":  "research",
		"query": "Research ordering guarantees and draft a brief.",
	})
	if err != nil {
		t.Fatalf("propose_codex_task: %v", err)
	}
	proposalID = asString(result["proposal"].(map[string]any)["id"])

	payload, launched, err := app.resolveCodexProposal(proposalID, "confirm", "Tom", "tom@shareability.com")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !launched || launches != 1 {
		t.Fatalf("launched=%v launches=%d, want exactly one launch", launched, launches)
	}
	if statusAtLaunch != codexProposalStatusConfirmed {
		t.Fatalf("proposal status at launch time=%q, want %q persisted before the launch", statusAtLaunch, codexProposalStatusConfirmed)
	}
	if payload["status"] != codexProposalStatusConfirmed || asString(payload["threadArtifactId"]) == "" {
		t.Fatalf("payload=%#v, want confirmed with thread linkage stamped after launch", payload)
	}

	// A launch failure reverts the proposal to proposed so confirm can retry.
	badID := durableTimestampID("codex-proposal", time.Now())
	if _, appended, err := app.memory.appendCodexProposal(badID, "Scout proposes a task with no query", map[string]string{
		"title":      "No query",
		"mode":       "research",
		"query":      "",
		"status":     codexProposalStatusProposed,
		"proposedBy": "board_worker",
	}); err != nil || !appended {
		t.Fatalf("append bad proposal: appended=%v err=%v", appended, err)
	}
	if _, _, err := app.resolveCodexProposal(badID, "confirm", "Tom", "tom@shareability.com"); err == nil {
		t.Fatal("confirm with an unlaunchable proposal must return the launch error")
	}
	if launches != 1 {
		t.Fatalf("launches=%d after failed launch, want still 1", launches)
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, badID)
	if !ok {
		t.Fatalf("bad proposal %s disappeared", badID)
	}
	if entry.Metadata["status"] != codexProposalStatusProposed || entry.Metadata["confirmedBy"] != "" {
		t.Fatalf("metadata=%#v, want the confirm reverted to proposed", entry.Metadata)
	}
}

func TestCodexProposalActionEndpointRejectsBadPaths(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	for _, path := range []string{
		"/assistant/proposals/",
		"/assistant/proposals/id-only",
		"/assistant/proposals/id/other",
		"/assistant/proposals/id/action/extra",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"action":"confirm"}`))
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantProposalActionHandler(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("path %s status=%d, want 404", path, recorder.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/assistant/proposals/x/action", nil)
	recorder := httptest.NewRecorder()
	assistantProposalActionHandler(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d, want 405", recorder.Code)
	}
}

// CARD 075 ("dismissed by Tyler, then it reappears") + CARDS 062+063:
// settling a proposal settles the propose-time broadcast nudge with it. The
// propose path stamps the proposal id onto the record; resolution rewrites
// the stale "confirm to launch" text into the outcome and stamps ResolvedAt,
// which reads the record as acknowledged for EVERY account — any signed-in
// user could have acted, so once someone has, the nudge never replays into
// anyone's unread backlog on reconnect/rejoin. A confirm also stamps the
// launched run's artifact so the bell entry routes to the workflow status.
func TestCodexProposalResolutionSettlesBroadcastNotification(t *testing.T) {
	server := newIsolatedWebsocketServer(t)

	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	propose := func(title string) string {
		t.Helper()
		result, _, err := kanbanApp.applyToolCallArgs("propose_codex_task", map[string]any{
			"title": title,
			"mode":  "research",
			"query": "Research " + title + " and draft a brief.",
		})
		if err != nil {
			t.Fatalf("propose %s: %v", title, err)
		}
		return asString(result["proposal"].(map[string]any)["id"])
	}
	notificationForProposal := func(proposalID string) (notificationRecord, bool) {
		t.Helper()
		kanbanApp.mu.Lock()
		defer kanbanApp.mu.Unlock()
		for index := len(kanbanApp.notifications) - 1; index >= 0; index-- {
			if kanbanApp.notifications[index].ProposalID == proposalID {
				return kanbanApp.notifications[index], true
			}
		}
		return notificationRecord{}, false
	}

	dismissID := propose("Stale nudge teardown")
	record, linked := notificationForProposal(dismissID)
	if !linked {
		t.Fatalf("propose-time nudge missing the proposal linkage for %s", dismissID)
	}

	if _, _, err := kanbanApp.resolveCodexProposal(dismissID, "dismiss", "Tyler", "tyler@shareability.com"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	settled, _ := notificationForProposal(dismissID)
	if settled.ID != record.ID {
		t.Fatalf("settled record id=%q, want the propose-time record %q rewritten in place", settled.ID, record.ID)
	}
	if !strings.Contains(settled.Text, "dismissed by Tyler") || strings.Contains(settled.Text, "confirm to launch") {
		t.Fatalf("settled text=%q, want the outcome instead of the stale confirm nudge", settled.Text)
	}
	if !notificationReadBy(settled, "tyler@shareability.com") {
		t.Fatal("the dismisser must be stamped read on the propose-time nudge")
	}
	if settled.ResolvedAt == "" {
		t.Fatal("resolution must stamp ResolvedAt so the nudge settles for every account")
	}

	// The dismisser's rejoin replays an empty backlog: the record must not
	// come back from the dead on a fresh socket for the same account.
	tylerConn := dialIsolatedWebsocket(t, server, "tyler@shareability.com")
	writeNativeWebsocketEvent(t, tylerConn, "participant", map[string]any{})
	raw := waitForKanbanEvent(t, tylerConn, "notification_backlog", 5*time.Second)
	var tylerBacklog []map[string]any
	if err := json.Unmarshal(raw, &tylerBacklog); err != nil {
		t.Fatalf("decode tyler backlog: %v", err)
	}
	if len(tylerBacklog) != 0 {
		t.Fatalf("tyler backlog=%#v, want empty after dismissing the proposal", tylerBacklog)
	}

	// CARDS 062+063: the settle reads for every account, not just the
	// resolver — tom's rejoin replays an empty backlog too, and his full
	// list carries the outcome as read, still keyed by the proposal id.
	tomConn := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	writeNativeWebsocketEvent(t, tomConn, "participant", map[string]any{})
	raw = waitForKanbanEvent(t, tomConn, "notification_backlog", 5*time.Second)
	var tomBacklog []map[string]any
	if err := json.Unmarshal(raw, &tomBacklog); err != nil {
		t.Fatalf("decode tom backlog: %v", err)
	}
	if len(tomBacklog) != 0 {
		t.Fatalf("tom backlog=%#v, want empty once anyone settled the proposal", tomBacklog)
	}
	tomList := kanbanApp.notificationsForUser("tom@shareability.com", notificationListLimit)
	if len(tomList) != 1 || tomList[0]["id"] != record.ID {
		t.Fatalf("tom list=%#v, want exactly the settled nudge %s", tomList, record.ID)
	}
	if text := asString(tomList[0]["text"]); !strings.Contains(text, "dismissed by Tyler") || strings.Contains(text, "confirm to launch") {
		t.Fatalf("tom list text=%q, want the outcome instead of the stale confirm nudge", text)
	}
	if tomList[0]["proposalId"] != dismissID || tomList[0]["read"] != true {
		t.Fatalf("tom list entry=%#v, want proposalId %q read for every account", tomList[0], dismissID)
	}

	// Confirm settles its nudge the same way, and stamps the launched run's
	// artifact so the bell entry routes to the resulting workflow status.
	confirmID := propose("Coyote pricing sweep")
	payload, _, err := kanbanApp.resolveCodexProposal(confirmID, "confirm", "Tom", "tom@shareability.com")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if launches != 1 {
		t.Fatalf("launches=%d, want exactly the confirmed launch", launches)
	}
	confirmed, _ := notificationForProposal(confirmID)
	if !strings.Contains(confirmed.Text, "confirmed by Tom") || !strings.Contains(confirmed.Text, "thread launched") {
		t.Fatalf("confirmed text=%q, want the confirmed outcome", confirmed.Text)
	}
	if confirmed.ArtifactID == "" || confirmed.ArtifactID != asString(payload["threadArtifactId"]) {
		t.Fatalf("confirmed artifactID=%q, want the launched run artifact %v", confirmed.ArtifactID, payload["threadArtifactId"])
	}
	for _, viewer := range []string{"tom@shareability.com", "tyler@shareability.com"} {
		for _, unread := range kanbanApp.unreadNotificationsFor(viewer, notificationListLimit) {
			if unread["id"] == confirmed.ID {
				t.Fatalf("confirmed nudge %s replayed into %s's unread backlog: %#v", confirmed.ID, viewer, unread)
			}
		}
	}
}

// CARDS 062+063: the propose-time nudge persists until someone acts on the
// proposal. Generic read receipts (the panel-open mark-all sweep, a row
// click) are refused while the proposal is pending; the receipt seam still
// works for every other record in the same batch, and it works again once
// the proposal is no longer pending — including the legacy case of a nudge
// whose proposal settled before the ResolvedAt stamp existed.
func TestCodexProposalNudgePersistsUntilActedOn(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Sticky nudge audit",
		"mode":  "research",
		"query": "Research the sticky nudge and draft a brief.",
	})
	if err != nil {
		t.Fatalf("propose_codex_task: %v", err)
	}
	proposalID := asString(result["proposal"].(map[string]any)["id"])
	nudge := notificationRecord{}
	app.mu.Lock()
	for index := len(app.notifications) - 1; index >= 0; index-- {
		if app.notifications[index].ProposalID == proposalID {
			nudge = app.notifications[index]
			break
		}
	}
	app.mu.Unlock()
	if nudge.ID == "" {
		t.Fatalf("propose-time nudge missing the proposal linkage for %s", proposalID)
	}
	plain, err := app.createNotification("", "info", "plain broadcast alongside the nudge", "", "", "", false)
	if err != nil {
		t.Fatalf("create plain notification: %v", err)
	}

	// The sweep marks the plain record and refuses the pending nudge.
	marked, err := app.markNotificationsRead("tyler@shareability.com", []string{nudge.ID, plain.ID})
	if err != nil {
		t.Fatalf("markNotificationsRead: %v", err)
	}
	if marked != 1 {
		t.Fatalf("marked=%d, want only the plain record (the pending nudge is sticky)", marked)
	}
	unread := app.unreadNotificationsFor("tyler@shareability.com", notificationListLimit)
	if len(unread) != 1 || unread[0]["id"] != nudge.ID {
		t.Fatalf("unread=%#v, want the sticky nudge %s to survive the sweep", unread, nudge.ID)
	}

	// Acting on the proposal is what settles it — for every account.
	if _, _, err := app.resolveCodexProposal(proposalID, "dismiss", "Tyler", "tyler@shareability.com"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	for _, viewer := range []string{"tyler@shareability.com", "tom@shareability.com"} {
		for _, unread := range app.unreadNotificationsFor(viewer, notificationListLimit) {
			if unread["id"] == nudge.ID {
				t.Fatalf("settled nudge %s still unread for %s: %#v", nudge.ID, viewer, unread)
			}
		}
	}

	// Legacy heal: a nudge whose proposal already settled but never got the
	// ResolvedAt stamp (records written before CARDS 062+063) must accept
	// receipts normally instead of staying sticky forever.
	app.mu.Lock()
	for index := range app.notifications {
		if app.notifications[index].ID == nudge.ID {
			app.notifications[index].ResolvedAt = ""
			app.notifications[index].ReadBy = nil
		}
	}
	app.mu.Unlock()
	marked, err = app.markNotificationsRead("tom@shareability.com", []string{nudge.ID})
	if err != nil {
		t.Fatalf("markNotificationsRead legacy: %v", err)
	}
	if marked != 1 {
		t.Fatalf("legacy marked=%d, want the settled-proposal nudge to accept the receipt", marked)
	}
}

// Frontend wiring guard, following the repo's index.html grep-test pattern:
// the proposal deck, websocket routes, and action wiring must stay together.
func TestIndexCodexProposalWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		`id="proposalDeck"`,
		`id="proposalDeckList"`,
		"/assistant/proposals/",
		"proposal-card__confirm",
		"proposal-card__dismiss",
		// Card 067: the auto-run lane chip and the honest ticker provenance line.
		"proposal-card__lane",
		"auto-run · launches within ~5 min",
		"auto-launched · standing approval",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing codex proposal anchor %s", want)
		}
	}

	proposalNode := functionBody(html, "function codexProposalNode(proposal)")
	if !strings.Contains(proposalNode, "proposal.lane || '') === 'auto_run'") {
		t.Fatal("codexProposalNode must render the auto_run lane chip")
	}
	if !strings.Contains(proposalNode, "by.startsWith('workflow ticker')") {
		t.Fatal("codexProposalNode must show the auto-launched provenance for ticker launches")
	}

	kanbanSwitch := functionBody(html, "function handleKanbanMessage(message)")
	if !strings.Contains(kanbanSwitch, "case 'codex_proposal':") {
		t.Fatal("handleKanbanMessage must route the codex_proposal event")
	}
	if !strings.Contains(kanbanSwitch, "case 'codex_proposals':") {
		t.Fatal("handleKanbanMessage must route the codex_proposals admission replay")
	}

	// CARD 075: the bell rewrite targets the durable propose-time record via
	// its proposalId linkage, and the dock never mints a server-unknown bell
	// id (which could never be marked read server-side, so it resurrected on
	// every backlog replay).
	bellRewrite := functionBody(html, "function resolveCodexProposalBellEntry(proposal)")
	if !strings.Contains(bellRewrite, "candidate.proposalId === proposal.id") {
		t.Fatal("resolveCodexProposalBellEntry must target the durable record by proposalId")
	}
	if strings.Contains(html, "id: `proposal-${id}`") {
		t.Fatal("the proposal dock must not mint local-only bell ids the server cannot settle")
	}

	// CARDS 062+063: the nudge click lands on the actionable deck card — a
	// docked card returns first (the dock keeps the data instead of dropping
	// it), then the card scrolls into view with the one-breath flash.
	dockBody := functionBody(html, "function scheduleCodexProposalDock(id)")
	if !strings.Contains(dockBody, "proposal.docked = true") {
		t.Fatal("the dock must flag the card instead of dropping its data (the nudge click un-docks it)")
	}
	if !strings.Contains(functionBody(html, "function renderCodexProposals()"), "!proposal.docked") {
		t.Fatal("renderCodexProposals must hide docked cards")
	}
	focusBody := functionBody(html, "function focusCodexProposalCard(proposalId)")
	for _, want := range []string{
		"proposal.docked = false",
		"setActiveTool('room')",
		"data-proposal-id",
		"scrollIntoView",
		"is-flashed",
	} {
		if !strings.Contains(focusBody, want) {
			t.Fatalf("focusCodexProposalCard missing %q", want)
		}
	}
}

// Room-confirmed proposals are the room's work: the launched artifact carries
// the proposal id and the live meeting id so completion can deliver the
// finished card back into the origin meeting's chat.
func TestCodexProposalConfirmStampsRoomOrigin(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	var launched scoutAgentThread
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	meetingID := app.memory.ensureMeetingID()
	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Coyote pricing",
		"mode":  "research",
		"query": "Research coyote pricing and draft a brief.",
	})
	if err != nil {
		t.Fatalf("propose_codex_task: %v", err)
	}
	proposalID := asString(result["proposal"].(map[string]any)["id"])

	if _, _, err := app.resolveCodexProposal(proposalID, "confirm", "Tom", "tom@shareability.com"); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	metadata := launched.Artifact.Metadata
	if metadata["originKind"] != agentThreadOriginRoom {
		t.Fatalf("originKind=%q, want %q", metadata["originKind"], agentThreadOriginRoom)
	}
	if metadata["originId"] != proposalID {
		t.Fatalf("originId=%q, want the proposal id %q", metadata["originId"], proposalID)
	}
	if metadata["originMeetingId"] != meetingID {
		t.Fatalf("originMeetingId=%q, want the live meeting %q", metadata["originMeetingId"], meetingID)
	}
}
