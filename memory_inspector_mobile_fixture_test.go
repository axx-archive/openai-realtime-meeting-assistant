package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in, isolated synthetic data only. Never writes the running QA server's files.
func TestMemoryInspectorExportMobileFixture(t *testing.T) {
	destination := os.Getenv("STRIDE_MEMORY_MOBILE_FIXTURE_DIR")
	if destination == "" {
		t.Skip("opt-in native memory fixture")
	}
	if !strings.HasPrefix(filepath.Clean(destination), "/tmp/") {
		t.Fatal("fixture output must be under /tmp")
	}
	app := setupInspectorTest(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Synthetic QA · Pilot decision", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	thread.Messages = []scoutChatMessageRecord{{ID: "native-memory-pilot-source", Role: "user", Text: "Synthetic QA: We will run a two-week pilot and measure willingness to pay before expanding. No business result has been observed.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: user.Name, AuthorEmail: user.Email}}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	note, _, err := app.rememberNote(user, rememberNoteRequest{Text: "Synthetic QA: The pilot is planned for two weeks. Measure willingness to pay before expansion.", Subject: "Pilot expansion decision", ThreadID: thread.ID, MessageID: "native-memory-pilot-source", Private: true}, "native-memory-fixture")
	if err != nil {
		t.Fatal(err)
	}
	items := inspectAs(t, user.Email, "?subject=pilot")
	if len(items) != 1 || items[0].ID != note.ID {
		t.Fatalf("expected exact pilot note, got %+v", items)
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "memory.jsonl"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"noteId": note.ID, "threadId": thread.ID, "messageId": "native-memory-pilot-source", "items": items})
	if err := os.WriteFile(filepath.Join(destination, "fixture.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("synthetic mobile fixture exported to " + destination)
}

// Rich media QA reads real public provider metadata but never starts a Scout turn.
func TestMemoryInspectorExportRichMediaFixture(t *testing.T) {
	destination := os.Getenv("STRIDE_RICHMEDIA_MOBILE_FIXTURE_DIR")
	if destination == "" {
		t.Skip("opt-in native rich-media fixture")
	}
	if !strings.HasPrefix(filepath.Clean(destination), "/tmp/") {
		t.Fatal("fixture output must be under /tmp")
	}
	app := setupInspectorTest(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Synthetic QA · Rich media", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	urls := []string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://x.com/i/article/2034593904100618240", "https://www.nasa.gov/", "https://preview-unavailable.invalid/canonical-page"}
	thread.Messages = []scoutChatMessageRecord{{ID: "native-richmedia-intro", Role: "user", Text: "Synthetic QA only: public links for preview, overflow, and source-opening verification. No message was sent to Scout.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: user.Name, AuthorEmail: user.Email}}
	for index, url := range urls {
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{ID: fmt.Sprintf("native-richmedia-source-%d", index), Role: "user", Text: url, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: user.Name, AuthorEmail: user.Email})
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "memory.jsonl"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"threadId": thread.ID, "urls": urls})
	if err := os.WriteFile(filepath.Join(destination, "fixture.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("synthetic native rich-media fixture exported to " + destination)
}
