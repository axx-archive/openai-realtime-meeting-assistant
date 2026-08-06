package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The omitempty round-trip rule: scout_chat_threads.go:131 documents that every
// new thread field must be omitempty so pre-existing records on disk decode
// unchanged. A Table field serializing as "table":false on every legacy thread
// would rewrite the entire store on first save.
func TestTableFieldOmitsWhenFalse(t *testing.T) {
	encoded, err := json.Marshal(scoutChatThreadRecord{ID: "t1", Title: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "table") {
		t.Fatalf("legacy thread serialized a table key: %s", encoded)
	}
}

func TestTableFieldRoundTrips(t *testing.T) {
	encoded, err := json.Marshal(scoutChatThreadRecord{ID: "t1", Title: "#team", Table: true})
	if err != nil {
		t.Fatal(err)
	}
	var decoded scoutChatThreadRecord
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Table {
		t.Fatalf("Table did not round-trip: %s", encoded)
	}
}

func newTableTestApp(t *testing.T) {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("THREAD_READ_MARKERS_PATH", filepath.Join(t.TempDir(), "markers.json"))
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
}

func TestEnsureTableIsIdempotent(t *testing.T) {
	newTableTestApp(t)

	first, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	second, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("second call created a new Table: %s != %s", first.ID, second.ID)
	}
}

// A second user loading their thread list must ADOPT the existing Table, not
// mint a private one of their own. Otherwise every teammate ends up talking
// into a different "#team".
func TestEnsureTableIsSharedAcrossUsers(t *testing.T) {
	newTableTestApp(t)

	mine, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatalf("ensure as aj: %v", err)
	}
	theirs, err := kanbanApp.ensureTable("dana@shareability.com")
	if err != nil {
		t.Fatalf("ensure as dana: %v", err)
	}
	if mine.ID != theirs.ID {
		t.Fatalf("two users got different Tables: %s != %s", mine.ID, theirs.ID)
	}
}

func TestEnsureTableCreatesAPublicChannel(t *testing.T) {
	newTableTestApp(t)

	table, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Public visibility is what makes it a CHANNEL — the #-prefix, broadcast
	// notify, and @-mention parsing all come free from that one value.
	if table.Visibility != scoutChatVisibilityPublic {
		t.Fatalf("visibility = %q, want public", table.Visibility)
	}
	if !table.Table {
		t.Fatal("the provisioned thread is not flagged as the Table")
	}
	if table.Title != tableThreadTitle {
		t.Fatalf("title = %q, want %q", table.Title, tableThreadTitle)
	}
}

// The flag must survive STORAGE, not just live in the returned struct.
//
// This exists because the first implementation persisted through
// appendScoutChatThread, which is append-only with dedup by id: it silently
// no-ops on a known id and reports appended=false rather than an error. Every
// other test still passed, because findTableThread's title-adoption fallback
// matched the unflagged "#team" thread and masked it. Assert on the flag as
// read back from the store, with no title to fall back to.
func TestTableFlagSurvivesStorage(t *testing.T) {
	newTableTestApp(t)

	created, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	for _, thread := range kanbanApp.scoutChatThreadsSnapshot("aj@shareability.com", false, 100) {
		if thread.ID != created.ID {
			continue
		}
		if !thread.Table {
			t.Fatal("the Table flag did not persist — it was lost on write-back")
		}
		return
	}
	t.Fatalf("thread %s vanished from the snapshot", created.ID)
}

// Adoption is a repair path, not the primary one: a thread that happens to be
// titled "team" but was never flagged must get flagged, and the flag must stick.
func TestUnflaggedTeamThreadIsAdoptedAndFlagged(t *testing.T) {
	newTableTestApp(t)

	orphan, err := kanbanApp.createScoutChatThread("dana@shareability.com", "Dana", tableThreadTitle, "public")
	if err != nil {
		t.Fatalf("create orphan: %v", err)
	}

	adopted, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if adopted.ID != orphan.ID {
		t.Fatalf("ensure created a duplicate instead of adopting: %s != %s", adopted.ID, orphan.ID)
	}

	found, ok := kanbanApp.findTableThread()
	if !ok || !found.Table {
		t.Fatalf("adopted thread was not flagged: ok=%v table=%v", ok, found.Table)
	}
}

func TestLegacyTeamTableMigratesToCanonicalBonfireChat(t *testing.T) {
	newTableTestApp(t)

	legacy, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", legacyTableThreadTitle, scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Table = true
	legacy.ArchivedAt = "2026-08-01T00:00:00Z"
	legacy.Preview = "archived"
	if err := kanbanApp.saveScoutChatThread(legacy); err != nil {
		t.Fatal(err)
	}

	table, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	if table.ID != legacy.ID || table.Title != tableThreadTitle || table.ArchivedAt != "" || !table.Table {
		t.Fatalf("canonicalized table=%+v", table)
	}
	persisted, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", table.ID)
	if err != nil || persisted.Title != tableThreadTitle || persisted.ArchivedAt != "" || !persisted.Table {
		t.Fatalf("persisted canonical table=%+v err=%v", persisted, err)
	}
}

func TestTableRejectsRenameAndArchive(t *testing.T) {
	newTableTestApp(t)
	table, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.renameScoutChatThread("aj@shareability.com", table.ID, "General"); err == nil || !strings.Contains(err.Error(), "cannot be renamed") {
		t.Fatalf("rename err=%v", err)
	}
	if _, err := kanbanApp.setScoutChatThreadArchived("aj@shareability.com", table.ID, true); err == nil || !strings.Contains(err.Error(), "cannot be archived") {
		t.Fatalf("archive err=%v", err)
	}
	persisted, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", table.ID)
	if err != nil || persisted.Title != tableThreadTitle || persisted.ArchivedAt != "" || !persisted.Table {
		t.Fatalf("permanent table mutated=%+v err=%v", persisted, err)
	}
}

// Two devices hitting the thread list simultaneously on a fresh deployment is
// the exact moment this races. Two Tables would split the team permanently and
// there is no natural repair.
func TestEnsureTableIsRaceSafe(t *testing.T) {
	newTableTestApp(t)

	const callers = 8
	ids := make([]string, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := 0; index < callers; index++ {
		go func(slot int) {
			defer wait.Done()
			table, err := kanbanApp.ensureTable("aj@shareability.com")
			if err != nil {
				t.Errorf("ensure: %v", err)
				return
			}
			ids[slot] = table.ID
		}(index)
	}
	wait.Wait()

	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("concurrent ensure produced multiple Tables: %v", ids)
		}
	}
}

// The Table is the app's permanent home thread, so it must not sink down the
// list the moment another channel gets a message.
func TestTableSortsFirstRegardlessOfRecency(t *testing.T) {
	newTableTestApp(t)

	if _, err := kanbanApp.ensureTable("aj@shareability.com"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Created after the Table, so strictly newer by updatedAt.
	if _, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "pricing", "public"); err != nil {
		t.Fatalf("create pricing: %v", err)
	}

	rows := kanbanApp.scoutChatThreadsView("aj@shareability.com", false, 100)
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 threads, got %d", len(rows))
	}
	if table, _ := rows[0]["table"].(bool); !table {
		t.Fatalf("first row is not the Table: %v", rows[0]["title"])
	}
}
