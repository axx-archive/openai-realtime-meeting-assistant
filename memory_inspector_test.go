package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setupInspectorTest(t *testing.T) *kanbanBoardApp {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previous := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previous })
	return app
}

func inspectAs(t *testing.T, email string, query string) []memoryInspectItem {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/assistant/memory/inspect"+query, nil)
	for _, cookie := range loginAs(t, email, "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantMemoryInspectHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect as %s status=%d body=%s", email, rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []memoryInspectItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return payload.Items
}

func inspectActionAs(t *testing.T, email string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/assistant/memory/inspect/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, email, "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantMemoryInspectActionHandler(rec, req)
	return rec
}

func seedTasteProfileForTest(t *testing.T, app *kanbanBoardApp, userName string, body string) meetingMemoryEntry {
	t.Helper()
	entry, appended, err := app.createOSArtifactWithMetadata("workflow", tasteProfileTitle(userName), body, scoutParticipantName, map[string]string{
		"title":                     tasteProfileTitle(userName),
		tasteProfileArtifactTypeKey: tasteProfileArtifactType,
		tasteProfileUserKey:         userName,
		tasteProfileDistilledAtKey:  time.Now().UTC().Format(time.RFC3339Nano),
		"signalCount":               "3",
	})
	if err != nil || !appended {
		t.Fatalf("seed profile for %s: appended=%v err=%v", userName, appended, err)
	}
	return entry
}

// Wave 8 D2: the inventory is viewer-scoped — a viewer sees their own living
// profile and never another's; notes, decisions, ledger records list with
// provenance and status.
func TestMemoryInspectViewerSeesOwnProfileNotAnothers(t *testing.T) {
	app := setupInspectorTest(t)
	seedTasteProfileForTest(t, app, "AJ", "## Voice & style\n- Terse (signal-1)")
	seedTasteProfileForTest(t, app, "Tim", "## Voice & style\n- Warm (signal-2)")
	aj := accountStore().findUser("aj@shareability.com")
	if _, _, err := app.rememberNote(aj, rememberNoteRequest{Text: "The Zebra pilot ships in October", Subject: "Zebra pilot"}, "s"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if _, _, err := app.memory.appendDecision("decision-inspect-1", "Choose vendor Zebra for the packaging pilot", map[string]string{"status": decisionStatusActive, "madeBy": "AJ", "meetingId": "meeting-1"}); err != nil {
		t.Fatalf("decision: %v", err)
	}

	items := inspectAs(t, "aj@shareability.com", "")
	kinds := map[string]int{}
	profileOwner := ""
	for _, item := range items {
		kinds[item.Kind]++
		if item.Kind == memoryInspectKindProfile {
			profileOwner = item.Person
			if !item.Own {
				t.Fatalf("own profile must be flagged own: %+v", item)
			}
		}
		if item.Kind == memoryInspectKindNote && (item.Status != relevanceActive || item.Person != "aj@shareability.com" || len(item.Provenance) == 0) {
			t.Fatalf("note item missing status/person/provenance: %+v", item)
		}
		if item.Kind == memoryInspectKindDecision && (item.Status != decisionStatusActive || item.Provenance[0].ID != "meeting-1") {
			t.Fatalf("decision item missing status/provenance: %+v", item)
		}
	}
	if kinds[memoryInspectKindProfile] != 1 || profileOwner != "AJ" {
		t.Fatalf("AJ must see exactly their own profile, got kinds=%v owner=%q", kinds, profileOwner)
	}
	if kinds[memoryInspectKindNote] != 1 || kinds[memoryInspectKindDecision] != 1 {
		t.Fatalf("expected the note and decision in the inventory, got %v", kinds)
	}

	for _, item := range inspectAs(t, "tim@shareability.com", "") {
		if item.Kind == memoryInspectKindProfile && item.Person != "Tim" {
			t.Fatalf("Tim saw another person's profile: %+v", item)
		}
	}
	// filters: person + kinds + subject
	filtered := inspectAs(t, "aj@shareability.com", "?kinds=decision&person=AJ&subject=zebra")
	if len(filtered) != 1 || filtered[0].Kind != memoryInspectKindDecision {
		t.Fatalf("filtered inventory=%+v, want the one Zebra decision by AJ", filtered)
	}
	if none := inspectAs(t, "aj@shareability.com", "?since=2999-01-01"); len(none) != 0 {
		t.Fatalf("since-the-future must list nothing, got %d", len(none))
	}
}

// Wave 8 D2: correct writes a ledger event carrying the correction text (and
// applies it to the entry); close writes a close event; forget on someone
// else's note is 403, on your own note a tombstone.
func TestMemoryInspectActionsWriteLedgerEventsAndGateForget(t *testing.T) {
	app := setupInspectorTest(t)
	aj := accountStore().findUser("aj@shareability.com")
	if _, _, err := app.memory.appendDecision("decision-correct-me", "Launch moved to July 31", map[string]string{"status": decisionStatusActive, "madeBy": "AJ"}); err != nil {
		t.Fatalf("decision: %v", err)
	}
	note, _, err := app.rememberNote(aj, rememberNoteRequest{Text: "Forget-me canary walrus9182", Subject: "canary"}, "s")
	if err != nil {
		t.Fatalf("remember: %v", err)
	}

	rec := inspectActionAs(t, "tim@shareability.com", `{"id":"decision-correct-me","action":"correct","correction":"Launch moved to August 7"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct status=%d body=%s", rec.Code, rec.Body.String())
	}
	events := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	if len(events) != 1 {
		t.Fatalf("ledger events=%d, want 1 correction event", len(events))
	}
	event := events[0]
	if event.Metadata["inspectorAction"] != inspectorActionCorrect || event.Metadata[inspectorCorrectionMetadataKey] != "Launch moved to August 7" || event.Metadata["actor"] != "tim@shareability.com" {
		t.Fatalf("correction event metadata=%v", event.Metadata)
	}
	var payload ledgerEventPayload
	if err := json.Unmarshal([]byte(event.Text), &payload); err != nil || payload.Op != ledgerOpUpdate || payload.Record.Title != "Launch moved to August 7" || payload.Record.Entity != ledgerEntityCorrection || !strings.Contains(payload.Reason, "corrected by") {
		t.Fatalf("correction event payload=%+v err=%v", payload, err)
	}
	corrected, _ := app.memory.entryByID("decision-correct-me")
	if corrected.Text != "Launch moved to August 7" || corrected.Metadata[inspectorStatusMetadataKey] != "corrected" || corrected.Metadata[inspectorCorrectedFromKey] != "Launch moved to July 31" {
		t.Fatalf("corrected decision=%+v", corrected)
	}
	// the corrected record never enters the live current-state view.
	view := app.ledgerCurrentStateView(10)
	if len(view.Decisions) != 0 {
		t.Fatalf("correction leaked into the current-state decisions: %+v", view.Decisions)
	}

	rec = inspectActionAs(t, "aj@shareability.com", `{"id":"decision-correct-me","action":"close"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("close status=%d body=%s", rec.Code, rec.Body.String())
	}
	if events := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0); len(events) != 2 || events[1].Metadata["op"] != ledgerOpClose {
		t.Fatalf("close must append a close event, got %d events", len(events))
	}

	// forget: not the author → 403, entry untouched.
	rec = inspectActionAs(t, "tim@shareability.com", `{"id":"`+note.ID+`","action":"forget"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forget someone else's note status=%d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if kept, _ := app.memory.entryByID(note.ID); kept.Text != note.Text {
		t.Fatal("a refused forget must not touch the note")
	}
	// forget a decision → 403 (never hard-deleted).
	rec = inspectActionAs(t, "aj@shareability.com", `{"id":"decision-correct-me","action":"forget"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forget a decision status=%d, want 403", rec.Code)
	}
	// forget own note → tombstone, gone from recall, fact survives.
	rec = inspectActionAs(t, "aj@shareability.com", `{"id":"`+note.ID+`","action":"forget"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("forget own note status=%d body=%s", rec.Code, rec.Body.String())
	}
	tombstone, found := app.memory.entryByID(note.ID)
	if !found || tombstone.Text != noteForgottenText || tombstone.Metadata[noteForgottenByMetadataKey] != "aj@shareability.com" || memoryEntryRelevance(tombstone) != relevanceExpired {
		t.Fatalf("tombstone=%+v found=%v", tombstone, found)
	}
	if matches := app.memory.search("walrus9182", 8); len(matches) != 0 {
		t.Fatalf("forgotten note still searchable: %+v", matches)
	}
	for _, item := range app.memoryInspectItems(context.Background(), aj, memoryInspectFilter{}) {
		if item.ID == note.ID {
			t.Fatal("forgotten note still listed by the inspector")
		}
	}
	// unauthenticated action → 401
	anon := httptest.NewRecorder()
	assistantMemoryInspectActionHandler(anon, httptest.NewRequest(http.MethodPost, "/assistant/memory/inspect/action", strings.NewReader(`{"id":"x","action":"close"}`)))
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous action status=%d, want 401", anon.Code)
	}
}

// The viewer's own user_profile accepts correct (an overriding statement on the
// body + a private audit event) and refuses close/forget; another person's
// profile id is not an inspectable item for the viewer.
func TestMemoryInspectProfileCorrectOnlyForOwner(t *testing.T) {
	app := setupInspectorTest(t)
	ajProfile := seedTasteProfileForTest(t, app, "AJ", "## Voice & style\n- Terse (signal-1)")
	timProfile := seedTasteProfileForTest(t, app, "Tim", "## Voice & style\n- Warm (signal-2)")

	rec := inspectActionAs(t, "aj@shareability.com", `{"id":"`+ajProfile.ID+`","action":"close"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("close own profile status=%d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	rec = inspectActionAs(t, "aj@shareability.com", `{"id":"`+ajProfile.ID+`","action":"forget"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forget own profile status=%d, want 403", rec.Code)
	}
	rec = inspectActionAs(t, "aj@shareability.com", `{"id":"`+timProfile.ID+`","action":"correct","correction":"Tim actually prefers long-form"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("correct another's profile status=%d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	rec = inspectActionAs(t, "aj@shareability.com", `{"id":"`+ajProfile.ID+`","action":"correct","correction":"I prefer long-form memos, not terse bullets"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct own profile status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload["kind"] != memoryInspectKindProfile || payload["status"] != "corrected" || payload["eventId"] == "" {
		t.Fatalf("payload=%v err=%v", payload, err)
	}
	updated, ok := app.tasteProfileForUser("AJ")
	if !ok || !strings.Contains(updated.Text, profileCorrectionsHeading) || !strings.Contains(updated.Text, "I prefer long-form memos, not terse bullets") || !strings.Contains(updated.Text, "Terse (signal-1)") {
		t.Fatalf("profile body after correct=%q ok=%v", updated.Text, ok)
	}
	if updated.Metadata[inspectorCorrectionMetadataKey] != "I prefer long-form memos, not terse bullets" || updated.Metadata[inspectorStatusMetadataKey] != "corrected" {
		t.Fatalf("profile metadata=%v", updated.Metadata)
	}
	events := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	if len(events) != 1 || events[0].Metadata["targetKind"] != memoryInspectKindProfile || events[0].Metadata["visibility"] != "private" || events[0].Metadata["ownerEmail"] != "aj@shareability.com" {
		t.Fatalf("profile correction event=%v", events)
	}
	// a second correction appends under the same heading.
	rec = inspectActionAs(t, "aj@shareability.com", `{"id":"`+ajProfile.ID+`","action":"correct","correction":"Never open with a summary"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second correct status=%d", rec.Code)
	}
	updated, _ = app.tasteProfileForUser("AJ")
	if strings.Count(updated.Text, profileCorrectionsHeading) != 1 || !strings.Contains(updated.Text, "Never open with a summary") {
		t.Fatalf("second correction body=%q", updated.Text)
	}
	// the inspector still lists exactly one own profile, now marked corrected.
	for _, item := range inspectAs(t, "aj@shareability.com", "?kinds=user_profile") {
		if item.ID != ajProfile.ID || item.Kind != memoryInspectKindProfile {
			t.Fatalf("unexpected profile item %+v", item)
		}
	}
}
