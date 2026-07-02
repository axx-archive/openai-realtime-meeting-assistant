package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func createTestPackage(t *testing.T, app *kanbanBoardApp, name string, thesis string) venturePackageRecord {
	t.Helper()
	record, err := app.createVenturePackage(name, thesis, "AJ")
	if err != nil {
		t.Fatalf("createVenturePackage %q: %v", name, err)
	}
	return record
}

func TestCreateVenturePackageValidatesAndStartsAtThesis(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	record := createTestPackage(t, app, "Nimbus creator platform", "creators need a home base")
	if record.Stage != "thesis" {
		t.Fatalf("stage=%q, want thesis", record.Stage)
	}
	if record.CreatedBy != "AJ" || record.Name != "Nimbus creator platform" {
		t.Fatalf("record=%#v, want AJ's named package", record)
	}
	if record.ID == "" || record.CreatedAt == "" || record.UpdatedAt == "" {
		t.Fatalf("record=%#v, want id and timestamps", record)
	}

	// names are required and unique case-insensitively.
	if _, err := app.createVenturePackage("", "", "AJ"); err == nil {
		t.Fatal("empty name must fail")
	}
	if _, err := app.createVenturePackage("nimbus CREATOR platform", "", "Tom"); err == nil {
		t.Fatal("case-insensitive duplicate name must fail")
	}

	// packages survive a restart through the memory store.
	reopened := newKanbanBoardApp()
	if _, ok := reopened.venturePackageByID(record.ID); !ok {
		t.Fatal("package did not reload from the JSONL store on boot")
	}
}

// The package kind is UI state: raw record JSON must never pollute Scout
// search context or the client memory timeline, and the multi-line list in
// normalizeMemoryEntryText must preserve the JSON.
func TestPackageKindStaysOutOfScoutSearchAndTimeline(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := createTestPackage(t, app, "Nimbus creator platform", "line one\nline two")

	if !isUIStateMemoryKind(meetingMemoryKindPackage) {
		t.Fatal("kind package must be a UI-state kind (excluded from Scout search)")
	}
	for _, entry := range visibleMeetingMemoryEntries(app.memory.snapshot(0), 0) {
		if entry.Kind == meetingMemoryKindPackage {
			t.Fatal("package entries must not render in the client memory timeline")
		}
	}
	stored, ok := app.venturePackageByID(record.ID)
	if !ok || stored.Thesis != "line one\nline two" {
		t.Fatalf("thesis=%q, want the multi-line thesis preserved through JSON storage", stored.Thesis)
	}
}

func TestAdvancePackageStageStepsAndSetsExplicitly(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := createTestPackage(t, app, "Nimbus creator platform", "")

	// empty stage steps forward one.
	advanced, err := app.advancePackageStage(record.ID, "", "Tom")
	if err != nil || advanced.Stage != "research" {
		t.Fatalf("stage=%q err=%v, want research", advanced.Stage, err)
	}
	// explicit stage may jump anywhere, forward or back.
	set, err := app.advancePackageStage(record.ID, "grill", "Tom")
	if err != nil || set.Stage != "grill" {
		t.Fatalf("stage=%q err=%v, want grill", set.Stage, err)
	}
	back, err := app.advancePackageStage(record.ID, "thesis", "Tom")
	if err != nil || back.Stage != "thesis" {
		t.Fatalf("stage=%q err=%v, want thesis (backwards is allowed)", back.Stage, err)
	}
	// invalid stages error.
	if _, err := app.advancePackageStage(record.ID, "shipping", "Tom"); err == nil {
		t.Fatal("invalid stage must fail")
	}
	// terminal: assembled stays assembled on a default advance.
	if _, err := app.advancePackageStage(record.ID, "assembled", "Tom"); err != nil {
		t.Fatalf("set assembled: %v", err)
	}
	terminal, err := app.advancePackageStage(record.ID, "", "Tom")
	if err != nil || terminal.Stage != "assembled" {
		t.Fatalf("stage=%q err=%v, want the terminal no-op at assembled", terminal.Stage, err)
	}
	// unknown package errors.
	if _, err := app.advancePackageStage("package-missing", "", "Tom"); err == nil {
		t.Fatal("unknown package must fail")
	}
}

func TestAttachToPackageIsIdempotentAndBidirectional(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := createTestPackage(t, app, "Nimbus creator platform", "")

	artifact, _, err := app.createOSArtifactWithMetadata("research", "Nimbus market scan", "Vision: scan done.", "AJ", nil)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	attached, err := app.attachToPackage(record.ID, "artifact", artifact.ID, "Tom")
	if err != nil || len(attached.ArtifactIDs) != 1 || attached.ArtifactIDs[0] != artifact.ID {
		t.Fatalf("artifactIds=%v err=%v, want the attached artifact", attached.ArtifactIDs, err)
	}
	// bidirectional: the artifact carries packageId.
	stampedArtifact, _ := app.osArtifactByID(artifact.ID)
	if stampedArtifact.Metadata["packageId"] != record.ID {
		t.Fatalf("artifact packageId=%q, want %q", stampedArtifact.Metadata["packageId"], record.ID)
	}
	// idempotent: re-attaching adds nothing.
	again, err := app.attachToPackage(record.ID, "artifact", artifact.ID, "Tom")
	if err != nil || len(again.ArtifactIDs) != 1 {
		t.Fatalf("artifactIds=%v err=%v after re-attach, want still one", again.ArtifactIDs, err)
	}

	// decisions stamp packageId back onto the decision entry.
	decisionEntry, _, err := app.memory.appendDecision("decision-nimbus-1", "Nimbus launches in Q4.", map[string]string{"status": decisionStatusActive})
	if err != nil {
		t.Fatalf("append decision: %v", err)
	}
	if _, err := app.attachToPackage(record.ID, "decision", decisionEntry.ID, "Tom"); err != nil {
		t.Fatalf("attach decision: %v", err)
	}
	stampedDecision, _ := app.memory.entryByKindAndID(meetingMemoryKindDecision, decisionEntry.ID)
	if stampedDecision.Metadata["packageId"] != record.ID {
		t.Fatalf("decision packageId=%q, want %q", stampedDecision.Metadata["packageId"], record.ID)
	}

	// cards attach through the same ref validation.
	card := createLinkageTestCard(t, app, "Nimbus launch checklist")
	withCard, err := app.attachToPackage(record.ID, "card", card.ID, "Tom")
	if err != nil || len(withCard.BoardCardIDs) != 1 {
		t.Fatalf("boardCardIds=%v err=%v, want the card", withCard.BoardCardIDs, err)
	}

	// channel is single-valued: a second attach replaces the first.
	first, err := app.createScoutChatThread("aj@shareability.com", "AJ", "nimbus channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	second, err := app.createScoutChatThread("aj@shareability.com", "AJ", "nimbus war room", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create second channel: %v", err)
	}
	if _, err := app.attachToPackage(record.ID, "channel", first.ID, "Tom"); err != nil {
		t.Fatalf("attach channel: %v", err)
	}
	replaced, err := app.attachToPackage(record.ID, "channel", second.ID, "Tom")
	if err != nil || replaced.ChannelID != second.ID {
		t.Fatalf("channelId=%q err=%v, want the replacement channel", replaced.ChannelID, err)
	}

	// unknown refs and ref types fail.
	if _, err := app.attachToPackage(record.ID, "artifact", "artifact-missing", "Tom"); err == nil {
		t.Fatal("unknown artifact ref must fail")
	}
	if _, err := app.attachToPackage(record.ID, "meeting", card.ID, "Tom"); err == nil {
		t.Fatal("invalid ref_type must fail")
	}

	// detach removes the ref and clears the reverse stamp.
	detached, err := app.detachFromPackage(record.ID, "artifact", artifact.ID, "Tom")
	if err != nil || len(detached.ArtifactIDs) != 0 {
		t.Fatalf("artifactIds=%v err=%v after detach, want empty", detached.ArtifactIDs, err)
	}
	cleared, _ := app.osArtifactByID(artifact.ID)
	if cleared.Metadata["packageId"] != "" {
		t.Fatalf("artifact packageId=%q after detach, want cleared", cleared.Metadata["packageId"])
	}
}

func TestFindPackageByNameOrID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := createTestPackage(t, app, "Nimbus creator platform", "")

	if found, ok := app.findPackageByNameOrID(record.ID); !ok || found.ID != record.ID {
		t.Fatalf("by id ok=%v, want the package", ok)
	}
	if found, ok := app.findPackageByNameOrID("nimbus creator PLATFORM"); !ok || found.ID != record.ID {
		t.Fatalf("by exact name ok=%v, want the package", ok)
	}
	// fuzzy: a close spoken form resolves when there is a single clear winner.
	if found, ok := app.findPackageByNameOrID("Nimbus creator platform plan"); !ok || found.ID != record.ID {
		t.Fatalf("fuzzy ok=%v, want the package", ok)
	}
	if _, ok := app.findPackageByNameOrID("Zanzibar merch line"); ok {
		t.Fatal("unrelated name must not resolve")
	}
	// ambiguity: two equally-close names resolve nothing.
	createTestPackage(t, app, "Nimbus creator platform east", "")
	createTestPackage(t, app, "Nimbus creator platform west", "")
	if _, ok := app.findPackageByNameOrID("Nimbus creator platform east west"); ok {
		t.Fatal("near-tied packages must be ambiguous, not a coin flip")
	}
	// exact-name helper never fuzzy-matches.
	if _, ok := app.venturePackageByExactName("Nimbus creator platform launch"); ok {
		t.Fatal("venturePackageByExactName must not fuzzy-match")
	}
	if _, ok := app.venturePackageByExactName("NIMBUS creator platform"); !ok {
		t.Fatal("venturePackageByExactName must match case-insensitively")
	}
}

func TestPackagePayloadCarriesStatsGapsAndGrillScore(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := createTestPackage(t, app, "Nimbus creator platform", "creators need a home base")

	if _, err := app.advancePackageStage(record.ID, "grill", "AJ"); err != nil {
		t.Fatalf("set stage: %v", err)
	}

	research, _, err := app.createOSArtifactWithMetadata("research", "Nimbus market scan", "Vision: scan done.", "AJ", nil)
	if err != nil {
		t.Fatalf("create research artifact: %v", err)
	}
	if _, err := app.attachToPackage(record.ID, "artifact", research.ID, "AJ"); err != nil {
		t.Fatalf("attach research: %v", err)
	}

	payload := app.packagePayload(mustFindPackage(t, app, record.ID))
	stats := payload["stats"].(map[string]any)
	if stats["artifactCount"] != 1 || stats["cardCount"] != 0 {
		t.Fatalf("stats=%#v, want one artifact and zero cards", stats)
	}
	gaps := stats["gaps"].([]string)
	if strings.Join(gaps, ",") != "design,grill" {
		t.Fatalf("gaps=%v, want design,grill at stage grill with only research attached", gaps)
	}
	if _, hasScore := stats["grillScore"]; hasScore {
		t.Fatal("grillScore must be omitted before a grill artifact lands")
	}

	// a grill artifact with a Score line fills the gap and surfaces the score.
	grill, _, err := app.createOSArtifactWithMetadata("grill", "Nimbus grill", "Verdict: promising.\nScore: 7.5/10 overall.", "AJ", map[string]string{
		"readiness": "7.5/10",
	})
	if err != nil {
		t.Fatalf("create grill artifact: %v", err)
	}
	if _, err := app.attachToPackage(record.ID, "artifact", grill.ID, "AJ"); err != nil {
		t.Fatalf("attach grill: %v", err)
	}
	payload = app.packagePayload(mustFindPackage(t, app, record.ID))
	stats = payload["stats"].(map[string]any)
	if stats["grillScore"] != "7.5" {
		t.Fatalf("grillScore=%v, want 7.5 parsed from the artifact body", stats["grillScore"])
	}
	if gaps := stats["gaps"].([]string); strings.Join(gaps, ",") != "design" {
		t.Fatalf("gaps=%v, want only design once grill is covered", gaps)
	}

	// artifact tuples carry titles + optional readiness metadata, never text.
	artifacts := payload["artifacts"].([]map[string]any)
	if len(artifacts) != 2 {
		t.Fatalf("artifacts=%d, want 2", len(artifacts))
	}
	var grillTuple map[string]any
	for _, tuple := range artifacts {
		raw, err := json.Marshal(tuple)
		if err != nil {
			t.Fatalf("marshal tuple: %v", err)
		}
		if strings.Contains(string(raw), "Verdict: promising") {
			t.Fatal("package payload must never carry artifact text")
		}
		if tuple["id"] == grill.ID {
			grillTuple = tuple
		}
	}
	if grillTuple == nil || grillTuple["readiness"] != "7.5/10" {
		t.Fatalf("grill tuple=%#v, want the readiness metadata rendered when present", grillTuple)
	}
}

func mustFindPackage(t *testing.T, app *kanbanBoardApp, id string) venturePackageRecord {
	t.Helper()
	record, ok := app.venturePackageByID(id)
	if !ok {
		t.Fatalf("package %q not found", id)
	}
	return record
}

// The three Scout tools ride the shared dispatch and the private voice
// allowlists, with the same schema/allowlist/dispatch contract as
// propose_codex_task.
func TestPackageToolsContract(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	toolsJSON := string(rawTools)
	for _, name := range []string{"create_package", "attach_to_package", "advance_package_stage"} {
		if !strings.Contains(toolsJSON, `"name":"`+name+`"`) {
			t.Fatalf("kanbanTools missing %s", name)
		}
		if !privateRealtimeVoiceToolAllowed(name) {
			t.Fatalf("%s must be allowed on the private voice surface", name)
		}
	}
	if !strings.Contains(toolsJSON, `"package_id":{"description":"id or exact name of the venture package this task belongs to; omit if none."`) {
		t.Fatal("propose_codex_task schema must expose the optional package_id arg")
	}
	rawPrivate, err := json.Marshal(app.privateRealtimeVoiceTools())
	if err != nil {
		t.Fatalf("marshal private tools: %v", err)
	}
	for _, name := range []string{"create_package", "attach_to_package", "advance_package_stage"} {
		if !strings.Contains(string(rawPrivate), `"name":"`+name+`"`) {
			t.Fatalf("private voice session must expose %s", name)
		}
	}
	for _, instructions := range []string{app.privateRealtimeVoiceSessionInstructions(), app.sessionInstructions()} {
		if !strings.Contains(instructions, "create_package / attach_to_package / advance_package_stage") {
			t.Fatal("both voice instruction strings must teach the package tools")
		}
	}

	// shared dispatch: create → attach → advance, attributed to Scout.
	result, changed, err := app.applyToolCallArgs("create_package", map[string]any{
		"name":   "Nimbus creator platform",
		"thesis": "creators need a home base",
	})
	if err != nil || !changed {
		t.Fatalf("create_package: changed=%v err=%v", changed, err)
	}
	created := result["package"].(map[string]any)
	if created["stage"] != "thesis" || created["createdBy"] != scoutParticipantName {
		t.Fatalf("created=%#v, want a thesis-stage package attributed to Scout", created)
	}
	packageID := asString(created["id"])

	artifact, _, err := app.createOSArtifactWithMetadata("research", "Nimbus market scan", "Vision: scan done.", "AJ", nil)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	// ref_title fuzzy resolution stands in for a missing ref_id.
	if _, _, err := app.applyToolCallArgs("attach_to_package", map[string]any{
		"package":   "Nimbus creator platform",
		"ref_type":  "artifact",
		"ref_title": artifact.Metadata["title"],
	}); err != nil {
		t.Fatalf("attach_to_package: %v", err)
	}
	attached := mustFindPackage(t, app, packageID)
	if len(attached.ArtifactIDs) != 1 || attached.ArtifactIDs[0] != artifact.ID {
		t.Fatalf("artifactIds=%v, want the title-resolved artifact", attached.ArtifactIDs)
	}

	if _, _, err := app.applyToolCallArgs("advance_package_stage", map[string]any{
		"package": "Nimbus creator platform",
	}); err != nil {
		t.Fatalf("advance_package_stage: %v", err)
	}
	if advanced := mustFindPackage(t, app, packageID); advanced.Stage != "research" {
		t.Fatalf("stage=%q, want research after the default advance", advanced.Stage)
	}

	// the private voice path attributes mutations to the signed-in requester.
	privateResult, _, err := app.applyPrivateRealtimeVoiceTool("aj@shareability.com", "create_package", map[string]any{
		"name": "Zanzibar merch line",
	})
	if err != nil {
		t.Fatalf("private create_package: %v", err)
	}
	privateCreated := privateResult["package"].(map[string]any)
	if privateCreated["createdBy"] == scoutParticipantName || asString(privateCreated["createdBy"]) == "" {
		t.Fatalf("createdBy=%v, want the requesting user's identity, not Scout", privateCreated["createdBy"])
	}
}

// GET/POST /assistant/packages and the action endpoint keep chat-thread-grade
// guards: origin-checked, any signed-in user, no admin gate.
func TestAssistantPackagesHandlersAuthAndActions(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	// signed-out requests stay rejected.
	recorder := httptest.NewRecorder()
	assistantPackagesHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/packages", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out GET status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	recorder = httptest.NewRecorder()
	assistantPackageActionHandler(recorder, httptest.NewRequest(http.MethodPost, "/assistant/packages/pkg-1/action", strings.NewReader(`{"action":"advance_stage"}`)))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out action status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	send := func(method string, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, reader)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		if strings.HasPrefix(path, "/assistant/packages/") {
			assistantPackageActionHandler(recorder, req)
		} else {
			assistantPackagesHandler(recorder, req)
		}
		payload := map[string]any{}
		_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
		return recorder, payload
	}

	// create as the signed-in (non-admin) user.
	created, payload := send(http.MethodPost, "/assistant/packages", map[string]string{
		"name":   "Nimbus creator platform",
		"thesis": "creators need a home base",
	})
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	record := payload["package"].(map[string]any)
	if record["createdBy"] != "Tim" {
		t.Fatalf("createdBy=%v, want the signed-in user", record["createdBy"])
	}
	packageID := asString(record["id"])

	// duplicates surface as 400s.
	if dup, _ := send(http.MethodPost, "/assistant/packages", map[string]string{"name": "nimbus creator platform"}); dup.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status=%d, want %d", dup.Code, http.StatusBadRequest)
	}

	// list is readable by any signed-in user.
	list, listPayload := send(http.MethodGet, "/assistant/packages", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	if packages := listPayload["packages"].([]any); len(packages) != 1 {
		t.Fatalf("packages=%d, want 1", len(packages))
	}

	// actions: advance, set_stage, update, attach, detach.
	if advanced, advancedPayload := send(http.MethodPost, "/assistant/packages/"+packageID+"/action", map[string]string{"action": "advance_stage"}); advanced.Code != http.StatusOK {
		t.Fatalf("advance status=%d", advanced.Code)
	} else if advancedPayload["package"].(map[string]any)["stage"] != "research" {
		t.Fatalf("stage=%v, want research", advancedPayload["package"].(map[string]any)["stage"])
	}
	if set, setPayload := send(http.MethodPost, "/assistant/packages/"+packageID+"/action", map[string]string{"action": "set_stage", "stage": "pitch"}); set.Code != http.StatusOK {
		t.Fatalf("set_stage status=%d", set.Code)
	} else if setPayload["package"].(map[string]any)["stage"] != "pitch" {
		t.Fatalf("stage=%v, want pitch", setPayload["package"].(map[string]any)["stage"])
	}

	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Nimbus market scan", "Vision: scan done.", "AJ", nil)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	if attach, _ := send(http.MethodPost, "/assistant/packages/"+packageID+"/action", map[string]string{"action": "attach", "refType": "artifact", "refId": artifact.ID}); attach.Code != http.StatusOK {
		t.Fatalf("attach status=%d body=%s", attach.Code, attach.Body.String())
	}
	if detach, _ := send(http.MethodPost, "/assistant/packages/"+packageID+"/action", map[string]string{"action": "detach", "refType": "artifact", "refId": artifact.ID}); detach.Code != http.StatusOK {
		t.Fatalf("detach status=%d", detach.Code)
	}

	// bad action, unknown id, malformed path.
	if bad, _ := send(http.MethodPost, "/assistant/packages/"+packageID+"/action", map[string]string{"action": "explode"}); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad action status=%d, want %d", bad.Code, http.StatusBadRequest)
	}
	if missing, _ := send(http.MethodPost, "/assistant/packages/package-missing/action", map[string]string{"action": "advance_stage"}); missing.Code != http.StatusNotFound {
		t.Fatalf("missing package status=%d, want %d", missing.Code, http.StatusNotFound)
	}
	if malformed, _ := send(http.MethodPost, "/assistant/packages/"+packageID+"/rename", map[string]string{"action": "advance_stage"}); malformed.Code != http.StatusNotFound {
		t.Fatalf("malformed path status=%d, want %d", malformed.Code, http.StatusNotFound)
	}
}
