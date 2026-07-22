package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestTranscriptCaptureSequenceIsDurableMonotonicAcrossDeletionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meeting-memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first, appended, err := store.appendEntryForMeeting("room-a", meetingMemoryKindTranscript, "transcript-a", "first", nil, "")
	if err != nil || !appended || first.Metadata["captureSequence"] != "1" {
		t.Fatalf("first append=%+v appended=%v err=%v", first, appended, err)
	}

	// Simulate retention removing the latest source row. The separately durable
	// counter must prevent sequence reuse even though the JSONL maximum fell.
	store.mu.Lock()
	store.entries = nil
	store.seen = map[string]struct{}{}
	if err := store.rewriteLocked(true); err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	store.mu.Unlock()
	second, appended, err := store.appendEntryForMeeting("room-a", meetingMemoryKindTranscript, "transcript-b", "second", nil, "")
	if err != nil || !appended || second.Metadata["captureSequence"] != "2" {
		t.Fatalf("post-delete append=%+v appended=%v err=%v", second, appended, err)
	}

	restarted, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	third, appended, err := restarted.appendEntryForMeeting("room-a", meetingMemoryKindTranscript, "transcript-c", "third", nil, "")
	if err != nil || !appended || third.Metadata["captureSequence"] != "3" {
		t.Fatalf("post-restart append=%+v appended=%v err=%v", third, appended, err)
	}
	highWater, found, err := loadCaptureSequence(captureSequencePath(path))
	if err != nil || !found || highWater != 3 {
		t.Fatalf("durable high-water=%d found=%v err=%v", highWater, found, err)
	}
}

func TestTranscriptCaptureSequenceOverridesCallerAndFailsClosedOnCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meeting-memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, appended, err := store.appendEntryForMeeting("room-a", meetingMemoryKindTranscript, "transcript-a", "first", map[string]string{"captureSequence": strconv.FormatUint(^uint64(0), 10)}, "")
	if err != nil || !appended || entry.Metadata["captureSequence"] != "1" {
		t.Fatalf("caller controlled sequence: entry=%+v appended=%v err=%v", entry, appended, err)
	}
	if err := os.WriteFile(captureSequencePath(path), []byte(`{"format":1,"highWater":1,"checksum":"tampered"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(store.entries)
	if _, appended, err := store.appendEntryForMeeting("room-a", meetingMemoryKindTranscript, "transcript-b", "second", nil, ""); err == nil || appended {
		t.Fatalf("corrupt counter append appended=%v err=%v, want fail closed", appended, err)
	}
	if len(store.entries) != before {
		t.Fatal("failed sequence persistence changed in-memory transcript inventory")
	}
}
