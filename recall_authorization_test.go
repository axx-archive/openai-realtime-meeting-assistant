package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func setupRecallAuthorizationTest(t *testing.T) (*kanbanBoardApp, meetingMemoryEntry) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })
	private, _, err := app.createOSArtifactWithMetadata("research", "private canary", "AJ-PRIVATE-LEXICAL-CANARY", "AJ", map[string]string{
		"visibility": "private", "requestedBy": "aj@shareability.com", "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, private
}

func TestRecallPrincipalFiltersBeforeLexicalSemanticAndArtifactLanes(t *testing.T) {
	app, private := setupRecallAuthorizationTest(t)
	timStore := app.recallStoreForPrincipal(context.Background(), recallPrincipalForEmail("tim@shareability.com"))
	if matches := timStore.search("AJ PRIVATE LEXICAL CANARY", 8); len(matches) != 0 {
		t.Fatalf("lexical leak=%+v", matches)
	}
	if fused := fuseRawCandidates(nil, []embeddingHit{{id: private.ID, kind: meetingMemoryKindOSArtifact, score: 1}}, timStore, 8); len(fused) != 0 {
		t.Fatalf("semantic-only leak=%+v", fused)
	}
	idx := &embeddingIndex{dims: 2, rows: []embeddingRow{
		{id: private.ID, kind: meetingMemoryKindOSArtifact, vec: []float32{1, 0}},
		{id: "allowed", kind: meetingMemoryKindBrain, vec: []float32{0, 1}},
	}}
	hits := idx.topKAllowed([]float32{1, 0}, 8, map[string]struct{}{"allowed": {}})
	if len(hits) != 1 || hits[0].id != "allowed" {
		t.Fatalf("semantic pre-score allowlist=%+v", hits)
	}
	timApp := &kanbanBoardApp{memory: timStore}
	_, contextEntries := timApp.memoryMatchesAndContext("private canary")
	for _, entry := range contextEntries {
		if entry.ID == private.ID || strings.Contains(entry.Text, "AJ-PRIVATE") {
			t.Fatalf("artifact context leak=%+v", entry)
		}
	}
	ajStore := app.recallStoreForPrincipal(context.Background(), recallPrincipalForEmail("aj@shareability.com"))
	if _, ok := ajStore.entryByID(private.ID); !ok {
		t.Fatal("private owner lost authorized artifact")
	}
}

func TestRecallPrincipalFiltersPrivateDigestLedgerAndKeepsLegacyOrgRoomHistory(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	now := time.Now().UTC()
	for _, entry := range []struct {
		kind, id, text string
		metadata       map[string]string
	}{
		{meetingMemoryKindCompanyDigest, "private-digest", "DIGEST-PRIVATE-CANARY", map[string]string{"visibility": "private", "ownerEmail": "aj@shareability.com"}},
		{meetingMemoryKindLedgerEvent, "private-ledger", "LEDGER-PRIVATE-CANARY", map[string]string{"visibility": "private", "ownerEmail": "aj@shareability.com"}},
		{meetingMemoryKindTranscript, "named-room", "ROOM-PRIVATE-CANARY", map[string]string{"roomId": "strategy-room", "meetingId": "sitting-1"}},
	} {
		if _, _, err := app.memory.appendAmbientEntry(entry.kind, entry.id, entry.text, entry.metadata); err != nil {
			t.Fatal(err)
		}
		_ = now
	}
	timStore := app.recallStoreForPrincipal(context.Background(), recallPrincipalForEmail("tim@shareability.com"))
	for _, deniedID := range []string{"private-digest", "private-ledger"} {
		if _, ok := timStore.entryByID(deniedID); ok {
			t.Fatalf("%s leaked", deniedID)
		}
	}
	if _, ok := timStore.entryByID("named-room"); !ok {
		t.Fatal("legacy organization-visible named-room history was hidden")
	}
	roomPrincipal := recallPrincipalForEmail("tim@shareability.com")
	roomPrincipal.RoomID, roomPrincipal.SittingID = "strategy-room", "sitting-1"
	roomStore := app.recallStoreForPrincipal(context.Background(), roomPrincipal)
	if got := roomStore.search("ROOM PRIVATE CANARY", 8); len(got) != 1 {
		t.Fatalf("room-scoped recall=%+v", got)
	}
}

func TestPrivateVoiceRecallUsesRequesterAndNeverBroadcasts(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	previousProbe := recallBroadcastProbe
	broadcasts := 0
	recallBroadcastProbe = func(RecallPrincipal, string) { broadcasts++ }
	t.Cleanup(func() { recallBroadcastProbe = previousProbe })

	aj, _, err := app.applyPrivateRealtimeVoiceTool("aj@shareability.com", "answer_memory_question", map[string]any{"query": "AJ PRIVATE LEXICAL CANARY"})
	if err != nil || !strings.Contains(asString(aj["answer"]), "AJ-PRIVATE-LEXICAL-CANARY") {
		t.Fatalf("owner answer=%v err=%v", aj, err)
	}
	tim, _, err := app.applyPrivateRealtimeVoiceTool("tim@shareability.com", "answer_memory_question", map[string]any{"query": "AJ PRIVATE LEXICAL CANARY"})
	if err != nil || strings.Contains(asString(tim["answer"]), "AJ-PRIVATE") {
		t.Fatalf("non-owner answer=%v err=%v", tim, err)
	}
	if broadcasts != 0 {
		t.Fatalf("private voice broadcasts=%d", broadcasts)
	}
}

func TestRecallModelPromptAndSharedAudienceExcludePrivateCanary(t *testing.T) {
	app, private := setupRecallAuthorizationTest(t)
	org, _, err := app.createOSArtifactWithMetadata("research", "organization canary", "ORG-MODEL-CANARY", "AJ", map[string]string{"visibility": "organization", "status": "complete"})
	if err != nil {
		t.Fatal(err)
	}
	previousProbe := recallModelContextProbe
	var seen []meetingMemoryEntry
	recallModelContextProbe = func(entries []meetingMemoryEntry) { seen = append([]meetingMemoryEntry(nil), entries...) }
	t.Cleanup(func() { recallModelContextProbe = previousProbe })
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	if _, err := app.resolveAssistantQueryContextForUser(context.Background(), "tim@shareability.com", "compare private canary report", nil); err != nil {
		t.Fatal(err)
	}
	for _, entry := range seen {
		if entry.ID == private.ID || strings.Contains(entry.Text, "AJ-PRIVATE") {
			t.Fatalf("model prompt leaked private artifact: %+v", entry)
		}
	}
	shared := app.recallStoreForPrincipal(context.Background(), sharedRoomRecallPrincipal(officeRoomID, ""))
	if _, ok := shared.entryByID(org.ID); !ok {
		t.Fatal("shared audience lost organization artifact")
	}
	if _, ok := shared.entryByID(private.ID); ok {
		t.Fatal("shared audience received private artifact")
	}
}

func TestRecallUnknownVisibilityAndTenantFailClosed(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	for _, row := range []struct {
		id       string
		metadata map[string]string
	}{
		{"future-visibility", map[string]string{"visibility": "partners_v2"}},
		{"foreign-tenant", map[string]string{"visibility": "organization", "tenantId": "other-tenant"}},
	} {
		if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindNote, row.id, "MUST-NOT-LEAK-"+row.id, row.metadata); err != nil {
			t.Fatal(err)
		}
	}
	store := app.recallStoreForPrincipal(context.Background(), recallPrincipalForEmail("tim@shareability.com"))
	if got := store.search("MUST NOT LEAK", 8); len(got) != 0 {
		t.Fatalf("unknown policy or foreign tenant leaked: %+v", got)
	}
}

func TestRecallSemanticAllowlistIncludesAuthorizedHistoryOlderThan250(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	target, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, "old-semantic-authorized", "A concept with no query tokens", map[string]string{"visibility": "organization"})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 280; index++ {
		if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, fmt.Sprintf("newer-%03d", index), fmt.Sprintf("recent filler %03d", index), map[string]string{"visibility": "organization"}); err != nil {
			t.Fatal(err)
		}
	}
	previous := loadedEmbeddingIndex()
	idx := newEmbeddingIndex("", 2, "test", func(context.Context, []string) ([][]float32, error) {
		return [][]float32{{1, 0}}, nil
	})
	idx.rows = []embeddingRow{{id: target.ID, kind: target.Kind, vec: []float32{1, 0}}}
	publishEmbeddingIndex(idx)
	t.Cleanup(func() { publishEmbeddingIndex(previous) })

	store := app.recallStoreForPrincipal(context.Background(), recallPrincipalForEmail("tim@shareability.com"))
	entries := store.contextEntriesForQuery("entirely unrelated semantic phrase", 12, time.Now())
	for _, entry := range entries {
		if entry.ID == target.ID {
			return
		}
	}
	t.Fatalf("authorized semantic target older than 250 was dropped: %+v", entries)
}

func TestOrganizationHistoryCrossesRoomsWhileGuestDurableRecallIsDenied(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	rows := []struct {
		id, text, room, sitting string
	}{
		{"strategy-current", "STRATEGY-CURRENT-CANARY", "strategy", "sitting-1"},
		{"strategy-old", "STRATEGY-OLD-CANARY", "strategy", "sitting-0"},
		{"finance-current", "FINANCE-CURRENT-CANARY", "finance", "sitting-1"},
	}
	for _, row := range rows {
		if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, row.id, row.text, map[string]string{
			"visibility": "shared", "roomId": row.room, "meetingId": row.sitting, "sittingId": row.sitting,
		}); err != nil {
			t.Fatal(err)
		}
	}
	member := recallPrincipalForEmail("tim@shareability.com")
	member.RoomID, member.SittingID = "finance", "sitting-1"
	store := app.recallStoreForPrincipal(context.Background(), member)
	for _, id := range []string{"strategy-current", "strategy-old", "finance-current"} {
		if _, ok := store.entryByID(id); !ok {
			t.Fatalf("organization-visible history %q was hidden across rooms", id)
		}
	}
	guest := recallPrincipalForGuest("guest-hash", "strategy", "sitting-1")
	guestStore := app.recallStoreForPrincipal(context.Background(), guest)
	if got := guestStore.snapshot(0); len(got) != 0 {
		t.Fatalf("guest received durable recall ids/bodies: %+v", got)
	}
	for _, meetingID := range []string{"sitting-0", "sitting-1"} {
		if _, _, err := app.getMeetingDetailForPrincipal(map[string]any{"meeting_id": meetingID}, guest); err == nil {
			t.Fatalf("guest resolved durable meeting %s", meetingID)
		}
	}
}

func TestExplicitRoomOnlyRecallRequiresExactRoomAndSitting(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, "room-only", "ROOM-ONLY-CANARY", map[string]string{
		"visibility": "room_only", "roomId": "strategy", "meetingId": "sitting-1", "sittingId": "sitting-1",
	}); err != nil {
		t.Fatal(err)
	}
	allowed := recallPrincipalForEmail("tim@shareability.com")
	allowed.RoomID, allowed.SittingID = "strategy", "sitting-1"
	if _, ok := app.recallStoreForPrincipal(context.Background(), allowed).entryByID("room-only"); !ok {
		t.Fatal("exact room/sitting lost room-only memory")
	}
	for _, denied := range []RecallPrincipal{
		func() RecallPrincipal { p := allowed; p.RoomID = "finance"; return p }(),
		func() RecallPrincipal { p := allowed; p.SittingID = "sitting-2"; return p }(),
	} {
		if _, ok := app.recallStoreForPrincipal(context.Background(), denied).entryByID("room-only"); ok {
			t.Fatal("room-only memory escaped exact room/sitting")
		}
	}
}

func TestPrivateMeetingRecapCannotBroadcastToRoom(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return "## Overview\nPrivate recap stays with Tim.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	appendTestTranscript(t, app, "private-recap-transcript", "Private recap source line.")

	result, _, err := app.applyPrivateRealtimeVoiceTool("tim@shareability.com", "meeting_recap", map[string]any{"audience": "room"})
	if err != nil || result["audience"] != notificationAudienceMe {
		t.Fatalf("private recap=%v err=%v", result, err)
	}
	if unread := app.unreadNotificationsFor("tim@shareability.com", notificationListLimit); len(unread) != 1 {
		t.Fatalf("caller notifications=%v", unread)
	}
	for _, item := range app.roomChatHistory(roomChatHistoryLimit) {
		if strings.Contains(asString(item["text"]), "Meeting recap:") {
			t.Fatalf("private recap broadcast to room: %#v", item)
		}
	}
}

func TestPrincipalFilteredClientSnapshotMeetingsAndBrief(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	privateMeeting, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, "private-meeting-brain", "AJ-MEETING-BODY", map[string]string{
		"visibility": "private", "ownerEmail": "aj@shareability.com", "meetingId": "meeting-private",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = privateMeeting
	if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindTranscript, "private-quarantine", "AJ-QUARANTINE-EXCERPT", map[string]string{
		"visibility": "private", "ownerEmail": "aj@shareability.com", relevanceMetadataKey: relevanceQuarantined,
	}); err != nil {
		t.Fatal(err)
	}
	privateArtifact, _, err := app.createOSArtifactWithMetadata("research", "private finished", "AJ ARTIFACT BODY", "AJ", map[string]string{
		"visibility": "private", "requestedBy": "aj@shareability.com", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}

	tim := recallPrincipalForEmail("tim@shareability.com")
	for _, entry := range app.memorySnapshotForPrincipal(context.Background(), tim, 100) {
		if entry.ID == "private-meeting-brain" || strings.Contains(entry.Text, "AJ-MEETING") {
			t.Fatalf("private memory leaked in client snapshot: %+v", entry)
		}
	}
	details := app.meetingMemoryDetailsForPrincipal(context.Background(), tim, map[string]struct{}{"meeting-private": {}})
	if detail := details["meeting-private"]; detail != nil && (strings.Contains(detail.Summary, "AJ-MEETING") || len(detail.Log) > 0) {
		t.Fatalf("private meeting detail leaked: %+v", detail)
	}
	brief := app.morningBriefPayloadContext(context.Background(), accountStore().findUser("tim@shareability.com"))
	if strings.Contains(fmt.Sprint(brief), privateArtifact.ID) || strings.Contains(fmt.Sprint(brief), "AJ-QUARANTINE") {
		t.Fatalf("private artifact/quarantine leaked in brief: %v", brief)
	}
	ajBrief := app.morningBriefPayloadContext(context.Background(), accountStore().findUser("aj@shareability.com"))
	if !strings.Contains(fmt.Sprint(ajBrief), privateArtifact.ID) || !strings.Contains(fmt.Sprint(ajBrief), "AJ-QUARANTINE") {
		t.Fatalf("owner lost private brief constituents: %v", ajBrief)
	}
}

func TestDelegatedWorkerContextRequiresRealMemberAndFiltersPrivateMemory(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindNote, "aj-worker-private", "AJ-WORKER-PRIVATE", map[string]string{
		"visibility": "private", "ownerEmail": "aj@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}
	if got := app.delegatedMemorySnapshot(context.Background(), "forged@example.com", "", 20); len(got) != 0 {
		t.Fatalf("forged requester received worker context: %+v", got)
	}
	for _, entry := range app.delegatedMemorySnapshot(context.Background(), "tim@shareability.com", "", 20) {
		if entry.ID == "aj-worker-private" {
			t.Fatalf("worker context laundered another owner's private memory: %+v", entry)
		}
	}
	if err := authorizeOrchestratorTool(AgentJob{Authority: codexJobAuthorityReadOnly, RequestedBy: "forged@example.com"}, "answer_memory_question"); err == nil {
		t.Fatal("forged requester authorized an orchestrator recall tool")
	}
}

func TestFabricatedNonmemberRecallPrincipalFailsClosed(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, "org-visible", "ORG-VISIBLE-CANARY", map[string]string{"visibility": "organization"}); err != nil {
		t.Fatal(err)
	}
	fabricated := RecallPrincipal{User: &userAccount{Email: "fabricated@example.com"}, TenantID: canonicalArtifactTenantID(), Audience: "private"}
	if got := app.recallStoreForPrincipal(context.Background(), fabricated).snapshot(0); len(got) != 0 {
		t.Fatalf("fabricated nonmember received recall: %+v", got)
	}
}

func TestAmbientWorkerFiltersBeforeModelAndPreservesDerivedScope(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	t.Setenv("MEETING_BRAIN_BACKFILL", "true")
	roomID := "strategy"
	meetingID := app.memory.ensureMeetingID(roomID)
	appendInput := func(id, text string, metadata map[string]string) {
		metadata["roomId"], metadata["meetingId"], metadata["sittingId"] = roomID, meetingID, meetingID
		if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindTranscript, id, text, metadata); err != nil {
			t.Fatal(err)
		}
	}
	appendInput("worker-org", "WORKER-ORG-CANARY", map[string]string{"visibility": "organization"})
	appendInput("worker-private", "WORKER-PRIVATE-CANARY", map[string]string{"visibility": "private", "ownerEmail": "aj@shareability.com"})
	appendInput("worker-room", "WORKER-ROOM-CANARY", map[string]string{"visibility": "room_only"})
	appendInput("worker-unknown", "WORKER-UNKNOWN-CANARY", map[string]string{"visibility": "future_policy"})
	appendInput("worker-foreign", "WORKER-FOREIGN-CANARY", map[string]string{"visibility": "organization", "tenantId": "foreign"})

	var modelInput string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		modelInput = request.Input
		return "## Overview\nScoped worker output.", nil
	}
	entry, err := app.runAmbientAgentOnceForRoom(meetingBrainAgent(), context.Background(), "test-key", responder, 1, roomID)
	if err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []string{"WORKER-ORG-CANARY", "WORKER-ROOM-CANARY"} {
		if !strings.Contains(modelInput, allowed) {
			t.Fatalf("model input lost authorized %s: %s", allowed, modelInput)
		}
	}
	for _, denied := range []string{"WORKER-PRIVATE-CANARY", "WORKER-UNKNOWN-CANARY", "WORKER-FOREIGN-CANARY"} {
		if strings.Contains(modelInput, denied) {
			t.Fatalf("model input leaked %s: %s", denied, modelInput)
		}
	}
	if entry.Metadata["visibility"] != "room_only" || entry.Metadata["tenantId"] != canonicalArtifactTenantID() || entry.Metadata["roomId"] != roomID || entry.Metadata["sittingId"] != meetingID {
		t.Fatalf("derived scope promoted or drifted: %+v", entry.Metadata)
	}
	office := recallPrincipalForEmail("tim@shareability.com")
	if _, ok := app.recallStoreForPrincipal(context.Background(), office).entryByID(entry.ID); ok {
		t.Fatal("room-only derived body escaped to office recall")
	}
	roomViewer := office
	roomViewer.RoomID, roomViewer.SittingID = roomID, meetingID
	roomStore := app.recallStoreForPrincipal(context.Background(), roomViewer)
	if _, ok := roomStore.entryByID(entry.ID); !ok {
		t.Fatalf("exact room/sitting lost derived body; principal=%+v entry=%+v visible=%+v", roomViewer, entry, roomStore.snapshot(0))
	}
}

func TestAnthropicPackageDelegationAuthorizesPackageAndReferenceHeaders(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	tim := accountStore().findUser("tim@shareability.com")
	if tim == nil {
		t.Fatal("missing Tim account")
	}
	ajPackage, err := app.createVenturePackage("AJ Secret Package", "private thesis", "AJ")
	if err != nil {
		t.Fatal(err)
	}
	timPackage, err := app.createVenturePackage("Tim Package", "tim thesis", "Tim")
	if err != nil {
		t.Fatal(err)
	}
	privateArtifact, _, err := app.createOSArtifactWithMetadata("research", "AJ Secret Artifact", "secret body", "AJ", map[string]string{
		"visibility": "private", "requestedBy": "aj@shareability.com", "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.advancePackageStageToolForUser(map[string]any{"package": ajPackage.Name, "stage": packageStages[1]}, tim); err == nil {
		t.Fatal("non-owner resolved and advanced another user's package by title")
	}
	if _, _, err := app.attachToPackageToolForUser(context.Background(), map[string]any{
		"package": timPackage.ID, "ref_type": packageRefTypeArtifact, "ref_id": privateArtifact.ID,
	}, tim); err == nil {
		t.Fatal("package tool attached an unauthorized artifact id")
	}
	if resolved := app.resolvePackageRefTitleForUser(context.Background(), tim, packageRefTypeArtifact, "AJ Secret Artifact"); resolved != "" {
		t.Fatalf("title resolution searched unauthorized artifact headers: %s", resolved)
	}
	privateDecision, _, err := app.memory.appendDecision("aj-private-decision", "AJ PRIVATE DECISION", map[string]string{
		"visibility": "private", "ownerEmail": "aj@shareability.com", "status": decisionStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if packageReferenceAuthorized(context.Background(), tim, packageRefTypeDecision, privateDecision.ID) {
		t.Fatal("direct guessed private decision id passed metadata authorization")
	}
}

func TestRestrictedEntityLedgerScopeExcludesPrivateStateAndPropagatesToEvents(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	now := time.Now().UTC()
	privateRecord := ledgerRecord{ID: "ldg-private", Entity: ledgerEntityTopic, Title: "private alpha launch plan", Status: ledgerStatusActive, ValidFrom: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339)}
	raw, _ := json.Marshal(ledgerEventPayload{Op: ledgerOpAdd, Record: privateRecord, At: now.Format(time.RFC3339)})
	if _, err := app.memory.appendLedgerEvents([]meetingMemoryEntry{{
		ID: "private-ledger-event", Kind: meetingMemoryKindLedgerEvent, Text: string(raw), CreatedAt: now,
		Metadata: map[string]string{"visibility": "private", "ownerEmail": "aj@shareability.com", "tenantId": canonicalArtifactTenantID()},
	}}); err != nil {
		t.Fatal(err)
	}
	scope := []meetingMemoryEntry{{ID: "room-digest", Kind: meetingMemoryKindMeetingDigest, Metadata: map[string]string{
		"visibility": "room_only", "tenantId": canonicalArtifactTenantID(), "roomId": "strategy", "sittingId": "sitting-1",
	}}}
	modelCalled := false
	count, err := app.consolidateLedgerFacts(context.Background(), "test-key", []ledgerFact{{Entity: ledgerEntityTopic, Title: "alpha launch budget", At: now.Format(time.RFC3339)}}, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		modelCalled = true
		if strings.Contains(request.Input, "private alpha launch plan") {
			t.Fatalf("private ledger canary reached adjudication model: %s", request.Input)
		}
		return `{"verdicts":[]}`, nil
	}, now.Add(time.Second), scope)
	if err != nil || count != 1 {
		t.Fatalf("consolidate count=%d err=%v", count, err)
	}
	if modelCalled {
		t.Fatal("private candidate reached scorer and created an ambiguity")
	}
	events := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	derived := events[len(events)-1]
	if strings.Contains(derived.Text, "private alpha launch plan") {
		t.Fatalf("private ledger canary leaked into derived event: %s", derived.Text)
	}
	if derived.Metadata["visibility"] != "room_only" || derived.Metadata["roomId"] != "strategy" || derived.Metadata["sittingId"] != "sitting-1" {
		t.Fatalf("restricted ledger event scope drifted: %+v", derived.Metadata)
	}
}

func TestRestrictedMissionAndNarrativeDoNotMutateOrganizationMeetingTitle(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app, _ := setupRecallAuthorizationTest(t)
	roomID := "strategy"
	app.noteMeetingAdmission(roomID, "AJ")
	before, ok := app.meetings.activeRecord(roomID)
	if !ok {
		t.Fatal("missing active meeting")
	}
	meetingID := app.memory.ensureMeetingID(roomID)
	input := meetingMemoryEntry{ID: "restricted-brain", Kind: meetingMemoryKindBrain, Text: "restricted title canary", CreatedAt: time.Now().UTC(), Metadata: map[string]string{
		"visibility": "room_only", "tenantId": canonicalArtifactTenantID(), "roomId": roomID, "sittingId": meetingID,
	}}
	if _, err := app.produceMissionInsight(context.Background(), "test-key", []meetingMemoryEntry{input}, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"themes":[{"label":"RESTRICTED MISSION TITLE","summary":"x","mentions":8}],"openQuestions":[],"alignments":[]}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.produceNarrativeUpdates(context.Background(), "test-key", []meetingMemoryEntry{input}, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"narratives":[{"slug":"restricted-title","title":"RESTRICTED NARRATIVE TITLE","status":"active","body":"## Storyline\nrestricted"}]}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := app.meetings.activeRecord(roomID)
	if after.Title != before.Title {
		t.Fatalf("restricted worker mutated organization meeting title: before=%+v after=%+v", before, after)
	}
}

func TestOrganizationTitleReducerCannotPromotePrivateThemesOrNarratives(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app, _ := setupRecallAuthorizationTest(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("missing active meeting")
	}
	started, _ := time.Parse(time.RFC3339Nano, record.StartedAt)
	privateMeta := map[string]string{
		"visibility": "private", "ownerEmail": "aj@shareability.com", "tenantId": canonicalArtifactTenantID(),
		"roomId": officeRoomID, "generatedAt": started.Add(time.Minute).Format(time.RFC3339),
	}
	if _, appended, err := app.memory.appendMissionInsight("private-title-mission", `{"themes":[{"label":"PRIVATE TITLE CANARY","mentions":99}],"openQuestions":[],"alignments":[]}`, privateMeta); err != nil || !appended {
		t.Fatalf("append private mission: appended=%v err=%v", appended, err)
	}
	privateNarrativeMeta := map[string]string{}
	for key, value := range privateMeta {
		privateNarrativeMeta[key] = value
	}
	privateNarrativeMeta["slug"] = "private-title"
	privateNarrativeMeta["title"] = "PRIVATE NARRATIVE CANARY"
	privateNarrativeMeta["firstSeenAt"] = started.Add(time.Minute).Format(time.RFC3339Nano)
	privateNarrativeMeta["lastSeenAt"] = started.Add(2 * time.Minute).Format(time.RFC3339Nano)
	if _, appended, err := app.memory.appendNarrative("private-title-narrative", "## Storyline\nprivate", privateNarrativeMeta); err != nil || !appended {
		t.Fatalf("append private narrative: appended=%v err=%v", appended, err)
	}
	input := meetingMemoryEntry{ID: "org-title-brain", Kind: meetingMemoryKindBrain, Text: "organization title source", CreatedAt: started.Add(3 * time.Minute), Metadata: map[string]string{
		"visibility": "organization", "tenantId": canonicalArtifactTenantID(), "roomId": officeRoomID,
	}}
	if _, err := app.produceMissionInsight(context.Background(), "test-key", []meetingMemoryEntry{input}, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"themes":[{"label":"ORG SAFE TITLE","summary":"x","mentions":1}],"openQuestions":[],"alignments":[]}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := app.meetings.activeRecord(officeRoomID)
	if strings.Contains(after.Title, "PRIVATE") {
		t.Fatalf("organization title reducer promoted private state: %+v", after)
	}
}

func TestAmbientContextBuildersAndEmbeddingExcludePrivateServiceInputs(t *testing.T) {
	app, _ := setupRecallAuthorizationTest(t)
	privateMeta := map[string]string{"visibility": "private", "ownerEmail": "aj@shareability.com"}
	for _, row := range []struct{ kind, id, text string }{
		{meetingMemoryKindDecision, "private-context-decision", "PRIVATE-DECISION-CONTEXT"},
		{meetingMemoryKindNarrative, "private-context-narrative", "PRIVATE-NARRATIVE-CONTEXT"},
		{meetingMemoryKindRunLog, "private-context-run", "PRIVATE-RUN-CONTEXT"},
	} {
		if _, _, err := app.memory.appendAmbientEntry(row.kind, row.id, row.text, privateMeta); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := app.createOSArtifactWithMetadata("research", "PRIVATE-ARTIFACT-TITLE", "PRIVATE-ARTIFACT-BODY", "AJ", map[string]string{
		"visibility": "private", "requestedBy": "aj@shareability.com", "status": "complete",
	}); err != nil {
		t.Fatal(err)
	}
	input := meetingMemoryEntry{ID: "org-input", Kind: meetingMemoryKindBrain, Text: "organization input", CreatedAt: time.Now().UTC(), Metadata: map[string]string{"visibility": "organization"}}
	contextApp := app.scopedRecallApp(context.Background(), sharedRoomRecallPrincipal(officeRoomID, ""))
	combined := strings.Join([]string{
		contextApp.buildMissionIntelInput([]meetingMemoryEntry{input}, time.Now()),
		contextApp.buildNarrativeMaintainerInput([]meetingMemoryEntry{input}, time.Now()),
		contextApp.buildDecisionLedgerInput([]meetingMemoryEntry{input}, time.Now()),
	}, "\n")
	for _, denied := range []string{"PRIVATE-DECISION-CONTEXT", "PRIVATE-NARRATIVE-CONTEXT", "PRIVATE-RUN-CONTEXT", "PRIVATE-ARTIFACT"} {
		if strings.Contains(combined, denied) {
			t.Fatalf("service context builder leaked %s: %s", denied, combined)
		}
	}
	for _, entry := range app.memory.eligibleEmbeddingEntriesSnapshotForPrincipal(sharedRoomRecallPrincipal(officeRoomID, "")) {
		if strings.Contains(entry.ID, "private-context") || strings.Contains(entry.Text, "PRIVATE-") {
			t.Fatalf("embedding service copied private body: %+v", entry)
		}
	}
}
