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
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing codex proposal anchor %s", want)
		}
	}

	kanbanSwitch := functionBody(html, "function handleKanbanMessage(message)")
	if !strings.Contains(kanbanSwitch, "case 'codex_proposal':") {
		t.Fatal("handleKanbanMessage must route the codex_proposal event")
	}
	if !strings.Contains(kanbanSwitch, "case 'codex_proposals':") {
		t.Fatal("handleKanbanMessage must route the codex_proposals admission replay")
	}
}
