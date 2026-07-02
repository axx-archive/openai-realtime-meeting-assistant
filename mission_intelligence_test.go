package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func runMissionIntelOnceForTest(t *testing.T, app *kanbanBoardApp, responder openAITextResponder) meetingMemoryEntry {
	t.Helper()
	entry, err := app.runAmbientAgentOnce(missionIntelligenceAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("runAmbientAgentOnce(mission intelligence): %v", err)
	}
	return entry
}

func TestMissionIntelligenceAgentContract(t *testing.T) {
	agent := missionIntelligenceAgent()
	if agent.name != "mission intelligence" {
		t.Fatalf("agent name=%q, want mission intelligence", agent.name)
	}
	if agent.inputKind != meetingMemoryKindBrain || agent.artifactKind != meetingMemoryKindMissionInsight {
		t.Fatalf("agent kinds=%q->%q, want brain->mission_insight", agent.inputKind, agent.artifactKind)
	}
	if agent.cursorMetadataKey != "throughBrainId" {
		t.Fatalf("cursor key=%q, want throughBrainId", agent.cursorMetadataKey)
	}
	if agent.produce == nil {
		t.Fatal("agent produce func must be set")
	}
	if agent.intervalEnv != "MISSION_INTEL_INTERVAL" || agent.disabledEnv != "MISSION_INTEL_DISABLED" || agent.backfillEnv != "MISSION_INTEL_BACKFILL" {
		t.Fatalf("agent envs=%q/%q/%q, want MISSION_INTEL_*", agent.intervalEnv, agent.disabledEnv, agent.backfillEnv)
	}
	if agent.defaultMinBatch != 1 {
		t.Fatalf("defaultMinBatch=%d, want 1 so a single brain write-up can synthesize", agent.defaultMinBatch)
	}
}

func TestProduceMissionInsightAppendsParsedInsightWithCursor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nAJ and Tim aligned on the intel canvas.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendBrainWriteUp("brain-2", "## Overview\nTyler raised open pricing questions.", nil); err != nil || !appended {
		t.Fatalf("append brain-2: appended=%v err=%v", appended, err)
	}

	insightJSON := `{"themes":[{"label":"intel canvas","summary":"The team keeps circling the intelligence surface.","mentions":3,"people":["AJ","Tim"]}],"openQuestions":["What is the pricing model?"],"alignments":["Ship the intel canvas first."]}`
	entry := runMissionIntelOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "brain-1") || !strings.Contains(request.Input, "brain-2") {
			t.Fatalf("mission input missing brain window: %s", request.Input)
		}
		if !strings.Contains(request.Instructions, "STRICT JSON") {
			t.Fatalf("instructions=%q, want strict JSON contract", request.Instructions)
		}
		return insightJSON, nil
	})

	if entry.Kind != meetingMemoryKindMissionInsight {
		t.Fatalf("entry kind=%q, want mission_insight", entry.Kind)
	}
	if entry.Metadata["fromBrainId"] != "brain-1" || entry.Metadata["throughBrainId"] != "brain-2" {
		t.Fatalf("cursor metadata=%v, want brain-1..brain-2", entry.Metadata)
	}
	if entry.Metadata["brainCount"] != "2" {
		t.Fatalf("brainCount=%q, want 2", entry.Metadata["brainCount"])
	}
	insight, _, ok := parseMissionInsight(entry.Text)
	if !ok {
		t.Fatalf("persisted insight is not parseable JSON: %q", entry.Text)
	}
	if len(insight.Themes) != 1 || insight.Themes[0].Label != "intel canvas" || insight.Themes[0].Mentions != 3 {
		t.Fatalf("insight themes=%#v, want intel canvas theme", insight.Themes)
	}
	payload := missionInsightEventPayload(entry, insight)
	if payload["id"] != entry.ID || payload["brainCount"] != 2 {
		t.Fatalf("broadcast payload=%v, want id + brainCount", payload)
	}
	if _, ok := payload["insight"].(missionInsightPayload); !ok {
		t.Fatalf("broadcast payload insight type=%T, want missionInsightPayload", payload["insight"])
	}

	// cursor advanced: a second pass sees no unconsumed brains
	if remaining := app.memory.unconsumedEntriesAfter(meetingMemoryKindBrain, meetingMemoryKindMissionInsight, "throughBrainId", 10, ""); len(remaining) != 0 {
		t.Fatalf("unconsumed brains=%d, want 0 after insight pass", len(remaining))
	}
}

// The archive flush includes the mission worker: the archived meeting's
// dominant theme titles the ARCHIVED record before the id rotates, and the
// mid-occupancy successor never inherits the old meeting's theme.
func TestArchiveFlushRunsMissionPassAndTitlesArchivedMeeting(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return `{"themes":[{"label":"intel canvas","summary":"The team keeps circling the intelligence surface.","mentions":3,"people":["AJ"]}],"openQuestions":[],"alignments":[]}`, nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admitParticipant: %v", err)
	}
	app.noteMeetingAdmission("AJ")
	meetingID := app.memory.currentMeetingID()
	if _, appended, err := app.memory.appendBrainWriteUp("brain-flush-1", "## Overview\nThe intel canvas came up repeatedly.", nil); err != nil || !appended {
		t.Fatalf("append brain write-up: appended=%v err=%v", appended, err)
	}

	if _, err := app.archiveMeeting("AJ"); err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}

	if insights := app.memory.entriesOfKind(meetingMemoryKindMissionInsight, 10); len(insights) == 0 {
		t.Fatal("archive flush did not run the mission-intel pass")
	}
	var closed meetingRecord
	for _, record := range app.meetings.recent(0) {
		if record.ID == meetingID {
			closed = record
		}
	}
	if closed.ID == "" {
		t.Fatal("archived meeting record not found")
	}
	if closed.Title != "intel canvas" || closed.TitleSource != meetingTitleSourceAuto {
		t.Fatalf("archived record=%#v, want the flushed dominant theme as its auto title", closed)
	}

	// AJ never left, so a successor record opened — it must NOT be titled
	// after the archived meeting's content.
	successor, ok := app.meetings.activeRecord()
	if !ok {
		t.Fatal("mid-occupancy archive left no successor record")
	}
	if successor.Title != "" {
		t.Fatalf("successor title=%q, want untitled (the old theme belongs to the archived meeting)", successor.Title)
	}
}

func TestProduceMissionInsightSkipsUnparseableOutput(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nA thin window.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	entry := runMissionIntelOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return "Sorry, here are some themes in prose instead.", nil
	})
	if entry.ID != "" {
		t.Fatalf("entry=%v, want nothing persisted for non-JSON output", entry)
	}
	if insights := app.memory.entriesOfKind(meetingMemoryKindMissionInsight, 10); len(insights) != 0 {
		t.Fatalf("insights=%d, want 0", len(insights))
	}

	// the cursor did not advance: a later valid pass consumes the same window
	entry = runMissionIntelOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "brain-1") {
			t.Fatalf("retry input missing unconsumed brain window: %s", request.Input)
		}
		return `{"themes":[],"openQuestions":[],"alignments":["Thin window noted."]}`, nil
	})
	if entry.Kind != meetingMemoryKindMissionInsight || entry.Metadata["throughBrainId"] != "brain-1" {
		t.Fatalf("retry entry=%v, want mission_insight through brain-1", entry)
	}
}

func TestMissionInsightExcludedFromSearchAndClientTimeline(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendMissionInsight("mission-insight-1", `{"themes":[{"label":"pricing themes","summary":"pricing","mentions":2,"people":["AJ"]}],"openQuestions":[],"alignments":[]}`, nil); err != nil || !appended {
		t.Fatalf("append mission insight: appended=%v err=%v", appended, err)
	}

	for _, match := range app.memory.search("pricing themes", 10) {
		if match.Entry.Kind == meetingMemoryKindMissionInsight {
			t.Fatalf("mission_insight leaked into Scout search: %#v", match.Entry)
		}
	}
	for _, entry := range app.memorySnapshotForClients(20) {
		if entry.Kind == meetingMemoryKindMissionInsight {
			t.Fatalf("mission_insight leaked into the client memory timeline: %#v", entry)
		}
	}

	// contextEntriesForQuery's time-range, participant-mention, and
	// empty-selection fallback branches read raw snapshots — the search()
	// kind exclusion must hold on every path into Scout's model context
	for _, query := range []string{
		"what happened in the last 10 minutes?", // time-range branch
		"what has AJ been working on?",          // participant-mention branch
		"zzz nothing matches this",              // empty-selection fallback branch
	} {
		for _, entry := range app.memory.contextEntriesForQuery(query, 20, time.Now()) {
			if isUIStateMemoryKind(entry.Kind) {
				t.Fatalf("UI-state kind %q leaked into Scout context for %q: %#v", entry.Kind, query, entry)
			}
		}
	}
}

func TestMissionPulseWindowBucketsByWindow(t *testing.T) {
	now := time.Now().UTC()
	entries := []meetingMemoryEntry{
		{ID: "t-live", Kind: meetingMemoryKindTranscript, Text: "AJ: hello", CreatedAt: now.Add(-time.Hour)},
		{ID: "t-chat", Kind: meetingMemoryKindTranscript, Text: "Tim: shipped", CreatedAt: now.Add(-2 * time.Hour), Metadata: map[string]string{"source": transcriptSourceRoomChat}},
		{ID: "t-old", Kind: meetingMemoryKindTranscript, Text: "AJ: last week", CreatedAt: now.Add(-3 * 24 * time.Hour)},
		{ID: "t-ancient", Kind: meetingMemoryKindTranscript, Text: "AJ: last month", CreatedAt: now.Add(-10 * 24 * time.Hour)},
		{ID: "brain-1", Kind: meetingMemoryKindBrain, Text: "## Overview", CreatedAt: now.Add(-2 * 24 * time.Hour)},
		{ID: "board-1", Kind: meetingMemoryKindBoardUpdate, Text: "moved a card", CreatedAt: now.Add(-30 * time.Minute)},
		{ID: "artifact-1", Kind: meetingMemoryKindOSArtifact, Text: "brief", CreatedAt: now.Add(-6 * 24 * time.Hour)},
		{ID: "proposal-1", Kind: meetingMemoryKindCodexProposal, Text: "Scout proposes", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "archive-1", Kind: meetingMemoryKindArchive, Text: "archived", CreatedAt: now.Add(-5 * 24 * time.Hour)},
		{ID: "thread-1", Kind: meetingMemoryKindScoutChat, Text: "{}", CreatedAt: now.Add(-9 * 24 * time.Hour), Metadata: map[string]string{"updatedAt": now.Add(-time.Hour).Format(time.RFC3339Nano)}},
		{ID: "thread-2", Kind: meetingMemoryKindScoutChat, Text: "{}", CreatedAt: now.Add(-9 * 24 * time.Hour), Metadata: map[string]string{"updatedAt": now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)}},
	}

	day := missionPulseWindow(entries, now.Add(-24*time.Hour))
	if day["spokenTranscripts"] != 1 || day["roomChatMessages"] != 1 || day["boardUpdates"] != 1 || day["proposals"] != 1 {
		t.Fatalf("24h window=%v, want 1 spoken, 1 chat, 1 board, 1 proposal", day)
	}
	if day["brainWriteUps"] != 0 || day["artifactsCreated"] != 0 || day["meetingsArchived"] != 0 {
		t.Fatalf("24h window=%v, want older kinds excluded", day)
	}
	if day["threadsActive"] != 1 {
		t.Fatalf("24h threadsActive=%d, want 1 (bucketed on updatedAt, not createdAt)", day["threadsActive"])
	}

	week := missionPulseWindow(entries, now.Add(-7*24*time.Hour))
	if week["spokenTranscripts"] != 2 || week["brainWriteUps"] != 1 || week["artifactsCreated"] != 1 || week["meetingsArchived"] != 1 {
		t.Fatalf("7d window=%v, want week counts", week)
	}
	if week["spokenTranscripts"] == 3 {
		t.Fatalf("7d window=%v, want 10-day-old transcript excluded", week)
	}
	if week["threadsActive"] != 1 {
		t.Fatalf("7d threadsActive=%d, want stale thread excluded", week["threadsActive"])
	}
}

func TestMissionContributionsAttributionAndPrivacy(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendAttributedTranscript("t-1", "i-1", "AJ", "dominant", "We decided to ship the mission intel canvas."); err != nil || !appended {
		t.Fatalf("append t-1: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendAttributedTranscript("t-2", "i-2", "AJ + Tim", "mixed", "We will split the aggregation work."); err != nil || !appended {
		t.Fatalf("append t-2: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendAttributedTranscript("t-3", "i-3", "", "", "Something unclear happened in the room."); err != nil || !appended {
		t.Fatalf("append t-3: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendRoomChatTranscript("chat-1", "Tim", "shipping the fix now"); err != nil || !appended {
		t.Fatalf("append chat-1: appended=%v err=%v", appended, err)
	}

	publicThread := scoutChatThreadRecord{
		ID:         "scout-chat-public",
		Title:      "growth",
		OwnerEmail: "aj@shareability.com",
		CreatedBy:  "AJ",
		Visibility: scoutChatVisibilityPublic,
		Messages: []scoutChatMessageRecord{
			{ID: "m-1", Kind: "message", Role: "user", Text: "channel post one", AuthorName: "Tim", AuthorEmail: "tim@shareability.com"},
			{ID: "m-2", Kind: "message", Role: "user", Text: "channel post two", AuthorName: "Tim", AuthorEmail: "tim@shareability.com"},
			{ID: "m-3", Kind: "message", Role: "scout", Text: "noted"},
		},
	}
	privateThread := scoutChatThreadRecord{
		ID:         "scout-chat-private",
		Title:      "Scout",
		OwnerEmail: "tim@shareability.com",
		CreatedBy:  "Tim",
		Visibility: "private",
		Messages: []scoutChatMessageRecord{
			{ID: "p-1", Kind: "message", Role: "user", Text: "private one", AuthorName: "Tim", AuthorEmail: "tim@shareability.com"},
			{ID: "p-2", Kind: "message", Role: "user", Text: "private two", AuthorName: "Tim", AuthorEmail: "tim@shareability.com"},
			{ID: "p-3", Kind: "message", Role: "user", Text: "private three", AuthorName: "Tim", AuthorEmail: "tim@shareability.com"},
		},
	}
	for _, thread := range []scoutChatThreadRecord{publicThread, privateThread} {
		encoded, err := encodeScoutChatThread(thread)
		if err != nil {
			t.Fatalf("encode thread %s: %v", thread.ID, err)
		}
		if _, appended, err := app.memory.appendScoutChatThread(thread.ID, encoded, scoutChatThreadMetadata(thread)); err != nil || !appended {
			t.Fatalf("append thread %s: appended=%v err=%v", thread.ID, appended, err)
		}
	}

	if _, appended, err := app.memory.appendCodexProposal("codex-proposal-1", "Scout proposes research task", map[string]string{
		"status":      codexProposalStatusConfirmed,
		"confirmedBy": "Tyler",
	}); err != nil || !appended {
		t.Fatalf("append proposal: appended=%v err=%v", appended, err)
	}
	if _, _, err := app.createOSArtifact("research", "demand brief", "Research brief\n\nEvidence.", "AJ"); err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}

	snapshot := app.missionIntelligenceSnapshot(time.Now())
	contributions, ok := snapshot["contributions"].(map[string]any)
	if !ok {
		t.Fatalf("contributions type=%T, want map", snapshot["contributions"])
	}
	people, ok := contributions["people"].([]missionContributionRow)
	if !ok {
		t.Fatalf("people type=%T, want []missionContributionRow", contributions["people"])
	}
	if len(people) != len(meetingParticipantNames) {
		t.Fatalf("people=%d, want all %d roster rows (zero-state included)", len(people), len(meetingParticipantNames))
	}
	rows := map[string]missionContributionRow{}
	for _, row := range people {
		rows[row.Name] = row
	}

	aj := rows["AJ"]
	if aj.Spoken != 2 || aj.ThreadsStarted != 1 || aj.ArtifactsCreated != 1 {
		t.Fatalf("AJ row=%+v, want spoken 2 (incl. split), 1 thread, 1 artifact", aj)
	}
	if want := 2 + 3*1 + 5*1; aj.Fuel != want {
		t.Fatalf("AJ fuel=%d, want %d", aj.Fuel, want)
	}
	tim := rows["Tim"]
	if tim.Spoken != 1 || tim.Chat != 1 || tim.ThreadsStarted != 1 {
		t.Fatalf("Tim row=%+v, want split spoken 1, chat 1, threadsStarted 1", tim)
	}
	if tim.ChannelMessages != 2 {
		t.Fatalf("Tim channelMessages=%d, want 2 — private thread messages must never be counted", tim.ChannelMessages)
	}
	tyler := rows["Tyler"]
	if tyler.ProposalsConfirmed != 1 {
		t.Fatalf("Tyler row=%+v, want proposalsConfirmed 1", tyler)
	}
	if unattributed, _ := contributions["unattributed"].(int); unattributed != 1 {
		t.Fatalf("unattributed=%v, want 1", contributions["unattributed"])
	}
	fuelMax, _ := contributions["fuelMax"].(int)
	if fuelMax != aj.Fuel {
		t.Fatalf("fuelMax=%d, want AJ's %d", fuelMax, aj.Fuel)
	}
	if people[0].Fuel < people[len(people)-1].Fuel {
		t.Fatalf("people not sorted by fuel desc: first=%+v last=%+v", people[0], people[len(people)-1])
	}

	// hard privacy rule: the snapshot JSON must never carry thread text
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, leaked := range []string{"private one", "channel post one", "Research brief"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("mission snapshot leaked message/artifact text %q", leaked)
		}
	}
}

func TestAssistantMissionHandlerAuth(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	// unsigned -> 401
	recorder := httptest.NewRecorder()
	assistantMissionHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/mission", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	// signed-in NON-admin -> 200 with a mission payload
	req := httptest.NewRequest(http.MethodGet, "/assistant/mission", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	assistantMissionHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("non-admin status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK      bool `json:"ok"`
		Mission struct {
			Pulse           map[string]any `json:"pulse"`
			Contributions   map[string]any `json:"contributions"`
			ThemesAvailable bool           `json:"themesAvailable"`
			Degraded        []string       `json:"degraded"`
		} `json:"mission"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode mission payload: %v", err)
	}
	if !payload.OK || payload.Mission.Pulse == nil || payload.Mission.Contributions == nil {
		t.Fatalf("mission payload=%s, want pulse + contributions", recorder.Body.String())
	}
	if payload.Mission.ThemesAvailable {
		t.Fatal("themesAvailable=true, want false with no insight entries")
	}
	if len(payload.Mission.Degraded) != 1 || payload.Mission.Degraded[0] != "openai_api_key_missing" {
		t.Fatalf("degraded=%v, want honest keyless flag", payload.Mission.Degraded)
	}

	// trust boundary = the signed-in team: /artifacts serves the same
	// non-admin account too (only external-write approval stays admin-only)
	artifactsReq := httptest.NewRequest(http.MethodGet, "/artifacts", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		artifactsReq.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	artifactsHandler(recorder, artifactsReq)
	if recorder.Code != http.StatusOK {
		t.Fatalf("/artifacts non-admin status=%d, want 200", recorder.Code)
	}
}

func TestAssistantMissionRefreshKeylessAndRateLimit(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	postRefresh := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/mission/refresh", nil)
		for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantMissionRefreshHandler(recorder, req)
		return recorder
	}

	// unsigned -> 401
	recorder := httptest.NewRecorder()
	assistantMissionRefreshHandler(recorder, httptest.NewRequest(http.MethodPost, "/assistant/mission/refresh", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned refresh status=%d, want 401", recorder.Code)
	}

	// keyless -> graceful 200, no rate-limit slot consumed
	recorder = postRefresh()
	if recorder.Code != http.StatusOK {
		t.Fatalf("keyless refresh status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var keyless struct {
		OK        bool   `json:"ok"`
		Refreshed bool   `json:"refreshed"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &keyless); err != nil {
		t.Fatalf("decode keyless refresh: %v", err)
	}
	if !keyless.OK || keyless.Refreshed || keyless.Reason != "openai_api_key_missing" {
		t.Fatalf("keyless refresh=%+v, want polite refusal", keyless)
	}

	// with a key (and no new brain input, so the model is never called):
	// first attempt is accepted, the second inside the window is 429
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()

	recorder = postRefresh()
	if recorder.Code != http.StatusOK {
		t.Fatalf("first refresh status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var first struct {
		Refreshed bool `json:"refreshed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first refresh: %v", err)
	}
	if first.Refreshed {
		t.Fatal("refreshed=true, want false with no unconsumed brain input")
	}

	recorder = postRefresh()
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second refresh status=%d body=%s, want 429", recorder.Code, recorder.Body.String())
	}

	// the window expires: a stale timestamp admits the next attempt
	kanbanApp.mu.Lock()
	kanbanApp.missionIntelRefreshAt = time.Now().Add(-missionIntelRefreshCooldown - time.Second)
	kanbanApp.mu.Unlock()
	recorder = postRefresh()
	if recorder.Code != http.StatusOK {
		t.Fatalf("post-cooldown refresh status=%d, want 200", recorder.Code)
	}
}

// The intel canvas "meeting live" dot keys on real room occupancy — the
// ingest meeting id stays non-empty between archives and must not drive it.
func TestMissionSnapshotLiveParticipantsTrackRoomOccupancy(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nUnarchived memory exists between archives.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	snapshot := app.missionIntelligenceSnapshot(time.Now())
	pulse, ok := snapshot["pulse"].(map[string]any)
	if !ok {
		t.Fatalf("pulse type=%T, want map", snapshot["pulse"])
	}
	if meetingID, _ := pulse["currentMeetingId"].(string); meetingID == "" {
		t.Fatal("currentMeetingId should stay in the payload (non-empty while unarchived memory exists)")
	}
	if live, _ := pulse["liveParticipants"].(int); live != 0 {
		t.Fatalf("liveParticipants=%v, want 0 with an empty room despite a live ingest id", pulse["liveParticipants"])
	}

	app.mu.Lock()
	app.participantCounts["AJ"] = 1
	app.mu.Unlock()

	snapshot = app.missionIntelligenceSnapshot(time.Now())
	pulse, _ = snapshot["pulse"].(map[string]any)
	if live, _ := pulse["liveParticipants"].(int); live != 1 {
		t.Fatalf("liveParticipants=%v, want 1 with one seated participant", pulse["liveParticipants"])
	}
}

// A transient synthesis failure must not burn the shared 5-minute cooldown:
// the reserved slot is rolled back so the next attempt is admitted.
func TestAssistantMissionRefreshReleasesCooldownOnFailure(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	if _, appended, err := kanbanApp.memory.appendBrainWriteUp("brain-1", "## Overview\nFresh input for the refresh pass.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()

	previousResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = previousResponder })
	failing := true
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		if failing {
			return "", errors.New("transient upstream error")
		}
		return `{"themes":[],"openQuestions":[],"alignments":["Recovered."]}`, nil
	}

	postRefresh := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/mission/refresh", nil)
		for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantMissionRefreshHandler(recorder, req)
		return recorder
	}

	recorder := postRefresh()
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("failed refresh status=%d body=%s, want 502", recorder.Code, recorder.Body.String())
	}

	// the failed pass released the slot: an immediate retry is admitted and
	// succeeds instead of hitting the 429 cooldown
	failing = false
	recorder = postRefresh()
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry refresh status=%d body=%s, want 200 (slot released after failure)", recorder.Code, recorder.Body.String())
	}
	var retry struct {
		Refreshed bool `json:"refreshed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &retry); err != nil {
		t.Fatalf("decode retry refresh: %v", err)
	}
	if !retry.Refreshed {
		t.Fatal("refreshed=false, want a successful retry after the released slot")
	}
}

func TestMissionSnapshotThemesSurfaceLatestInsight(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	stale := `{"themes":[{"label":"old theme","summary":"old","mentions":1,"people":[]}],"openQuestions":[],"alignments":[]}`
	fresh := `{"themes":[{"label":"fresh theme","summary":"fresh","mentions":4,"people":["AJ"]}],"openQuestions":["Q1"],"alignments":["A1"]}`
	if _, appended, err := app.memory.appendMissionInsight("mission-insight-old", stale, map[string]string{"brainCount": "1"}); err != nil || !appended {
		t.Fatalf("append stale insight: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendMissionInsight("mission-insight-new", fresh, map[string]string{"brainCount": "4", "generatedAt": time.Now().UTC().Format(time.RFC3339)}); err != nil || !appended {
		t.Fatalf("append fresh insight: appended=%v err=%v", appended, err)
	}

	snapshot := app.missionIntelligenceSnapshot(time.Now())
	if available, _ := snapshot["themesAvailable"].(bool); !available {
		t.Fatalf("themesAvailable=%v, want true", snapshot["themesAvailable"])
	}
	themes, ok := snapshot["themes"].(map[string]any)
	if !ok {
		t.Fatalf("themes type=%T, want map", snapshot["themes"])
	}
	if themes["id"] != "mission-insight-new" || themes["brainCount"] != 4 {
		t.Fatalf("themes=%v, want the newest insight", themes)
	}
	insight, ok := themes["insight"].(missionInsightPayload)
	if !ok || len(insight.Themes) != 1 || insight.Themes[0].Label != "fresh theme" {
		t.Fatalf("insight=%#v, want fresh theme", themes["insight"])
	}
}

func TestMissionInsightTextSurvivesAppendNormalization(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	multiline := "{\n  \"themes\": [],\n  \"openQuestions\": [\"Q  with  spacing\"],\n  \"alignments\": []\n}"
	entry, appended, err := app.memory.appendMissionInsight("mission-insight-json", multiline, nil)
	if err != nil || !appended {
		t.Fatalf("append: appended=%v err=%v", appended, err)
	}
	if _, _, ok := parseMissionInsight(entry.Text); !ok {
		t.Fatalf("persisted mission insight no longer parses: %q", entry.Text)
	}
}
