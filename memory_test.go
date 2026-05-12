package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMeetingMemoryPersistsAndSearchesTranscripts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")

	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, appended, err := store.appendTranscript("event-1", "item-1", "  Alice said launch is blocked by billing review. "); err != nil {
		t.Fatalf("appendTranscript: %v", err)
	} else if !appended {
		t.Fatal("appendTranscript appended=false, want true")
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload memory store: %v", err)
	}

	matches := reloaded.search("billing review", 3)
	if len(matches) != 1 {
		t.Fatalf("search matches=%d, want 1", len(matches))
	}
	if got := matches[0].Entry.Text; !strings.Contains(got, "billing review") {
		t.Fatalf("match text %q does not include expected phrase", got)
	}
}

func TestMeetingMemoryDedupesEventIDs(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	if _, appended, err := store.appendTranscript("event-1", "item-1", "First version."); err != nil {
		t.Fatalf("first append: %v", err)
	} else if !appended {
		t.Fatal("first append appended=false, want true")
	}
	if _, appended, err := store.appendTranscript("event-1", "item-1", "Duplicate version."); err != nil {
		t.Fatalf("second append: %v", err)
	} else if appended {
		t.Fatal("second append appended=true, want false")
	}

	entries := store.snapshot(10)
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
}
