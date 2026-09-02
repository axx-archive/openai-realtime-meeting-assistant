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

// Wave 8 D1: a deliberate remember writes a recall-eligible note carrying
// rememberedBy / at / aliases, and it surfaces in memoryMatchesAndContext for a
// matching query — including by alias.
func TestRememberNoteWritesRecallEligibleNote(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seeded aj account missing")
	}

	entry, recorded, err := app.rememberNote(user, rememberNoteRequest{
		Text:    "Zebra's packaging invoice is due on the 15th of every month.",
		Subject: "Zebra invoicing",
		Aliases: []string{"the packaging vendor bill"},
	}, "test-scope")
	if err != nil {
		t.Fatalf("rememberNote: %v", err)
	}
	if !recorded || entry.Kind != meetingMemoryKindNote {
		t.Fatalf("entry=%+v recorded=%v, want a recorded note", entry, recorded)
	}
	if entry.Metadata[noteRememberedByMetadataKey] != "aj@shareability.com" || entry.Metadata[noteAtMetadataKey] == "" {
		t.Fatalf("note must stamp rememberedBy + at, got %v", entry.Metadata)
	}
	if entry.Metadata["visibility"] != "organization" || entry.Metadata[noteSubjectMetadataKey] != "Zebra invoicing" {
		t.Fatalf("company note must be organization-visible with its subject, got %v", entry.Metadata)
	}
	if !strings.Contains(entry.Metadata[digestAliasesMetadataKey], "packaging vendor bill") {
		t.Fatalf("aliases must land in searchable metadata, got %q", entry.Metadata[digestAliasesMetadataKey])
	}

	matches, contextEntries := app.memoryMatchesAndContext("when is the Zebra invoice due?")
	foundMatch := false
	for _, match := range matches {
		if match.Entry.ID == entry.ID {
			foundMatch = true
		}
	}
	if !foundMatch {
		t.Fatalf("memoryMatchesAndContext did not match the remembered note; matches=%d", len(matches))
	}
	foundContext := false
	for _, ctxEntry := range contextEntries {
		if ctxEntry.ID == entry.ID {
			foundContext = true
		}
	}
	if !foundContext {
		t.Fatal("remembered note missing from the model context")
	}
	// alias band: the query names the note only through its alias.
	if aliasMatches := app.memory.search("packaging vendor bill", 8); len(aliasMatches) == 0 || aliasMatches[0].Entry.ID != entry.ID {
		t.Fatalf("alias search did not surface the note: %+v", aliasMatches)
	}

	// idempotent in the same scope; distinct in another.
	if _, again, err := app.rememberNote(user, rememberNoteRequest{Text: "Zebra's packaging invoice is due on the 15th of every month.", Subject: "Zebra invoicing", Aliases: []string{"the packaging vendor bill"}}, "test-scope"); err != nil || again {
		t.Fatalf("same-scope re-file: recorded=%v err=%v, want dedupe", again, err)
	}
}

// Wave 8 D1 ACL: a private note is owner-only on every recall lane.
func TestRememberPrivateNoteInvisibleToOtherViewer(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	other := accountStore().findUser("tim@shareability.com")
	if owner == nil || other == nil {
		t.Fatal("seeded accounts missing")
	}
	const canary = "quokkaprivateremember5521"
	entry, _, err := app.rememberNote(owner, rememberNoteRequest{Text: canary + " stays with AJ", Private: true}, "private-scope")
	if err != nil {
		t.Fatalf("rememberNote: %v", err)
	}
	if entry.Metadata["visibility"] != "private" || entry.Metadata["ownerEmail"] != "aj@shareability.com" {
		t.Fatalf("private note must stamp visibility=private + ownerEmail, got %v", entry.Metadata)
	}

	ctx := context.Background()
	if matches := app.scopedRecallApp(ctx, recallPrincipalForUser(owner)).memory.search(canary, 8); len(matches) != 1 {
		t.Fatalf("owner search=%d matches, want 1", len(matches))
	}
	if matches := app.scopedRecallApp(ctx, recallPrincipalForUser(other)).memory.search(canary, 8); len(matches) != 0 {
		t.Fatalf("PRIVACY LEAK: another viewer found the private note: %+v", matches)
	}
	otherContext := app.scopedRecallApp(ctx, recallPrincipalForUser(other)).memory.contextEntriesForQuery(canary, 50, time.Now())
	for _, ctxEntry := range otherContext {
		if strings.Contains(ctxEntry.Text, canary) {
			t.Fatal("PRIVACY LEAK: private note entered another viewer's context")
		}
	}
	// the shared-room service principal (room Scout) never sees private notes either.
	if matches := app.scopedRecallApp(ctx, sharedRoomRecallPrincipal(officeRoomID, "")).memory.search(canary, 8); len(matches) != 0 {
		t.Fatalf("PRIVACY LEAK: shared-room recall found the private note: %+v", matches)
	}
}

// Wave 8 D8: an explicit remember on a PRIVATE thread message copies that
// message into a note — by the owner, deliberately — while the implicit
// contract (TestPrivateChatBrainContract) stays untouched.
func TestRememberCopiesPrivateThreadMessageExplicitly(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	other := accountStore().findUser("tim@shareability.com")
	const canary = "marmosetexplicitremember7719"

	thread, err := app.createScoutChatThread(owner.Email, "AJ", "Private notes", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		t.Fatal("fixture thread must be private")
	}
	msg := scoutChatMessageRecord{ID: "msg-remember-me", Kind: "message", Role: "user", Text: canary + " remember this one", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorEmail: owner.Email}
	if _, err := app.commitScoutChatThreadMessages(owner.Email, thread.ID, msg); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// implicit: nothing searchable yet.
	if matches := app.memory.search(canary, 8); len(matches) != 0 {
		t.Fatalf("implicit leak: private message searchable before remember: %+v", matches)
	}
	// another member cannot lift a private thread's message.
	if _, _, err := app.rememberNote(other, rememberNoteRequest{ThreadID: thread.ID, MessageID: msg.ID}, "x"); err == nil {
		t.Fatal("non-owner remember of a private thread message must fail")
	}
	entry, recorded, err := app.rememberNote(owner, rememberNoteRequest{ThreadID: thread.ID, MessageID: msg.ID}, "x")
	if err != nil || !recorded {
		t.Fatalf("owner remember: recorded=%v err=%v", recorded, err)
	}
	if !strings.Contains(entry.Text, canary) || entry.Metadata[noteSourceThreadMetadataKey] != thread.ID || entry.Metadata[noteSourceMessageMetadataKey] != msg.ID {
		t.Fatalf("note must copy the message text and keep the source pointer, got %+v", entry)
	}
	if entry.Metadata["sourceVisibility"] != scoutChatVisibilityPrivate || entry.Metadata[noteRememberedByMetadataKey] != owner.Email {
		t.Fatalf("explicit private remember must record its provenance, got %v", entry.Metadata)
	}
	matches := app.memory.search(canary, 8)
	if len(matches) != 1 || matches[0].Entry.Kind != meetingMemoryKindNote {
		t.Fatalf("after remember, search must find exactly the note: %+v", matches)
	}
}

// Wave 8 D1: the HTTP seam and the Scout tool share one contract; the tool
// schema round-trips through JSON and is registered in every dispatch table.
func TestRememberHandlerAndToolRegistration(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previous := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previous })

	// unauthenticated → 401
	rec := httptest.NewRecorder()
	assistantRememberHandler(rec, httptest.NewRequest(http.MethodPost, "/assistant/remember", strings.NewReader(`{"text":"x"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous remember status=%d, want 401", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/assistant/remember", strings.NewReader(`{"text":"Tim prefers Tuesday syncs","subject":"Tim's schedule","aliases":["tuesday standup"]}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec = httptest.NewRecorder()
	assistantRememberHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remember status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["ok"] != true || payload["kind"] != "note" || payload["recorded"] != true || payload["rememberedBy"] != "aj@shareability.com" || payload["subject"] != "Tim's schedule" {
		t.Fatalf("payload=%v", payload)
	}
	if aliases, _ := payload["aliases"].([]any); len(aliases) != 1 || aliases[0] != "tuesday standup" {
		t.Fatalf("aliases=%v", payload["aliases"])
	}

	// tool schema round-trip
	raw, err := json.Marshal(rememberNoteToolDefinition())
	if err != nil {
		t.Fatalf("marshal tool: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal tool: %v", err)
	}
	params, _ := decoded["parameters"].(map[string]any)
	props, _ := params["properties"].(map[string]any)
	if decoded["name"] != rememberNoteToolName || decoded["type"] != "function" || props["text"] == nil || props["aliases"] == nil || props["private"] == nil {
		t.Fatalf("tool schema did not round-trip: %s", raw)
	}
	inMaster := false
	for _, tool := range app.kanbanTools() {
		if name, _ := tool["name"].(string); name == rememberNoteToolName {
			inMaster = true
		}
	}
	if !inMaster {
		t.Fatal("remember_note missing from kanbanTools()")
	}
	if !orchestratorToolAllowlist[rememberNoteToolName] || !privateRealtimeVoiceServerActionAllowed(rememberNoteToolName) || privateRealtimeVoiceToolAllowed(rememberNoteToolName) {
		t.Fatal("remember_note must be orchestrator + private-voice server-side callable, never a model voice tool")
	}

	// tool execution attributes the requester; the shared room voice refuses.
	result, _, err := app.applyPrivateRealtimeVoiceTool("tim@shareability.com", rememberNoteToolName, map[string]any{"text": "remember the demo is Friday", "aliases": []any{"friday demo"}})
	if err != nil || result["rememberedBy"] != "tim@shareability.com" {
		t.Fatalf("private voice remember: result=%v err=%v", result, err)
	}
	if _, _, err := app.applyToolCallArgs(rememberNoteToolName, map[string]any{"text": "anonymous"}); err == nil {
		t.Fatal("room-voice remember_note without a requester must be refused")
	}
}
