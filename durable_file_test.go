package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomicallyDurableReplacesAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicallyDurable(path, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("durable replace: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new\n" {
		t.Fatalf("contents = %q, want new record", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	assertNoDurableTemps(t, dir)
}

func TestWriteFileAtomicallyDurableRenameFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "occupied")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicallyDurable(target, []byte("never published"), 0o600); err == nil || !strings.Contains(err.Error(), "replace file") {
		t.Fatalf("error = %v, want replace failure", err)
	}
	assertNoDurableTemps(t, dir)
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("target directory changed: %v", entries)
	}
}

func TestWriteFileAtomicallyDurableParentFailureLeavesNoArtifact(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "state.json")
	if err := writeFileAtomicallyDurable(target, []byte("data"), 0o600); err == nil || !strings.Contains(err.Error(), "create parent directory") {
		t.Fatalf("error = %v, want parent creation failure", err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("target unexpectedly exists")
	}
}

func TestAppendFileDurablyPersistsOrderedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := appendFileDurably(path, []byte("one\n"), 0o600); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := appendFileDurably(path, []byte("two\n"), 0o600); err != nil {
		t.Fatalf("second append: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "one\ntwo\n" {
		t.Fatalf("contents = %q", raw)
	}
}

func TestAppendFileDurablyRejectsDirectoryTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := appendFileDurably(target, []byte("record\n"), 0o600); err == nil || !strings.Contains(err.Error(), "open append file") {
		t.Fatalf("error = %v, want open failure", err)
	}
}

func TestMeetingMemoryAppendAndRewriteSurviveReload(t *testing.T) {
	t.Setenv("BONFIRE_CANONICAL_MODE", "shadow")
	path := filepath.Join(t.TempDir(), "meeting-memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, appended, err := store.appendEntry(meetingMemoryKindTranscript, "durable-1", "hello", nil)
	if err != nil || !appended {
		t.Fatalf("append = (%v, %v), error %v", entry.ID, appended, err)
	}

	store.mu.Lock()
	store.entries[0].Metadata["durabilityTest"] = "rewritten"
	err = store.rewriteLocked(false)
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reloaded.entryByKindAndID(meetingMemoryKindTranscript, "durable-1")
	if !ok || got.Metadata["durabilityTest"] != "rewritten" {
		t.Fatalf("reloaded entry = %#v, found=%v", got, ok)
	}
	assertNoDurableTemps(t, filepath.Dir(path))
}

func TestCanonicalLegacyDurabilityMode(t *testing.T) {
	for _, mode := range []string{"shadow", "required", " SHADOW "} {
		t.Setenv("BONFIRE_CANONICAL_MODE", mode)
		if !canonicalLegacyDurabilityRequired() {
			t.Fatalf("mode %q did not require durability", mode)
		}
	}
	t.Setenv("BONFIRE_CANONICAL_MODE", "off")
	if canonicalLegacyDurabilityRequired() {
		t.Fatal("off mode unexpectedly requires canonical durability")
	}
	t.Setenv("BONFIRE_CANONICAL_MODE", "shdaow")
	if err := validateCanonicalModeConfig(); err == nil {
		t.Fatal("misspelled canonical mode did not fail startup validation")
	}
}

func TestWriteAllRejectsNoProgress(t *testing.T) {
	err := writeAll(zeroWriter{}, []byte("data"))
	if err == nil || err.Error() != "short write" {
		t.Fatalf("error = %v, want short write", err)
	}
	err = writeAll(errorWriter{}, []byte("data"))
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("error = %v, want closed pipe", err)
	}
}

func assertNoDurableTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("orphan temp files: %v", matches)
	}
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
