package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func captureDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func openTestCaptureSpool(t *testing.T) (*CanonicalCaptureSpool, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capture.spool")
	spool, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow)
	if err != nil {
		t.Fatal(err)
	}
	return spool, path
}

type faultCanonicalAppendFile struct {
	*os.File
	writeBytes int
	failSync   int
	failClose  bool
}

func (file *faultCanonicalAppendFile) Write(data []byte) (int, error) {
	if file.writeBytes >= 0 {
		limit := file.writeBytes
		file.writeBytes = -1
		if limit > len(data) {
			limit = len(data)
		}
		written, err := file.File.Write(data[:limit])
		if err != nil {
			return written, err
		}
		return written, errors.New("injected append write failure")
	}
	return file.File.Write(data)
}

func (file *faultCanonicalAppendFile) Sync() error {
	if file.failSync > 0 {
		file.failSync--
		return errors.New("injected append sync failure")
	}
	return file.File.Sync()
}

func (file *faultCanonicalAppendFile) Close() error {
	err := file.File.Close()
	if file.failClose {
		return errors.New("injected append close failure")
	}
	return err
}

func installCanonicalAppendFault(t *testing.T, configure func(*faultCanonicalAppendFile)) {
	t.Helper()
	originalOpen := canonicalCaptureOpenAppend
	canonicalCaptureOpenAppend = func(path string) (canonicalAppendFile, bool, error) {
		_, statErr := os.Stat(path)
		created := errors.Is(statErr, os.ErrNotExist)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, false, err
		}
		wrapped := &faultCanonicalAppendFile{File: file, writeBytes: -1}
		configure(wrapped)
		return wrapped, created, nil
	}
	t.Cleanup(func() { canonicalCaptureOpenAppend = originalOpen })
}

func TestCanonicalCaptureDeliversCommittedOnlyAndReopens(t *testing.T) {
	spool, path := openTestCaptureSpool(t)
	before, after := captureDigest("before"), captureDigest("after")
	prepare, err := spool.Prepare("m-1", "artifact", "a-1", before, after, json.RawMessage(`{"artifact_id":"a-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if prepare.Sequence != 1 || len(spool.CommittedFacts()) != 0 {
		t.Fatal("prepared fact became deliverable")
	}
	commit, err := spool.Commit("m-1")
	if err != nil {
		t.Fatal(err)
	}
	if commit.Sequence != 2 || !isHexDigest(commit.FamilyDigest) {
		t.Fatalf("bad commit: %+v", commit)
	}
	if repeat, err := spool.Commit("m-1"); err != nil || repeat.Sequence != commit.Sequence {
		t.Fatalf("commit is not idempotent: %+v %v", repeat, err)
	}

	if _, err := spool.Prepare("m-2", "artifact", "a-2", "", captureDigest("new"), json.RawMessage(`{"artifact_id":"a-2"}`)); err != nil {
		t.Fatal(err)
	}
	facts := spool.CommittedFacts()
	if len(facts) != 1 || facts[0].Prepare.MutationID != "m-1" {
		t.Fatalf("deliverable facts = %+v", facts)
	}

	reopened, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow)
	if err != nil {
		t.Fatal(err)
	}
	if facts = reopened.CommittedFacts(); len(facts) != 1 || facts[0].Commit.FamilyDigest != commit.FamilyDigest {
		t.Fatalf("reopened facts = %+v", facts)
	}
	if next, err := reopened.Abort("m-2"); err != nil || next.Sequence != 4 {
		t.Fatalf("sequence not monotonic after reopen: %+v %v", next, err)
	}
}

func TestCanonicalCaptureFirstCreateIsDurableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "capture.spool")
	spool, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open created an unframed spool: %v", err)
	}
	if _, err := spool.Prepare("m-1", "room", "r", "", captureDigest("r"), json.RawMessage(`{"room_id":"r"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("spool mode = %o", info.Mode().Perm())
	}
	if _, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow); err != nil {
		t.Fatalf("first durable frame did not reopen: %v", err)
	}
}

func TestCanonicalCaptureAppendFailuresRollbackOrPoison(t *testing.T) {
	t.Run("partial write rolls back", func(t *testing.T) {
		spool, path := openTestCaptureSpool(t)
		installCanonicalAppendFault(t, func(file *faultCanonicalAppendFile) { file.writeBytes = 11 })
		if _, err := spool.Prepare("m-1", "room", "r", "", captureDigest("r"), json.RawMessage(`{"room_id":"r"}`)); err == nil {
			t.Fatal("expected injected write failure")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial first frame survived rollback: %v", err)
		}
		canonicalCaptureOpenAppend = func(path string) (canonicalAppendFile, bool, error) {
			_, statErr := os.Stat(path)
			created := errors.Is(statErr, os.ErrNotExist)
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			return file, created, err
		}
		if record, err := spool.Prepare("m-1", "room", "r", "", captureDigest("r"), json.RawMessage(`{"room_id":"r"}`)); err != nil || record.Sequence != 1 {
			t.Fatalf("safe retry = %+v %v", record, err)
		}
	})

	t.Run("sync failure rolls back", func(t *testing.T) {
		spool, _ := openTestCaptureSpool(t)
		installCanonicalAppendFault(t, func(file *faultCanonicalAppendFile) { file.failSync = 1 })
		if _, err := spool.Prepare("m-1", "room", "r", "", captureDigest("r"), json.RawMessage(`{"room_id":"r"}`)); err == nil {
			t.Fatal("expected injected sync failure")
		}
		canonicalCaptureOpenAppend = func(path string) (canonicalAppendFile, bool, error) {
			_, statErr := os.Stat(path)
			created := errors.Is(statErr, os.ErrNotExist)
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			return file, created, err
		}
		if _, err := spool.Prepare("m-1", "room", "r", "", captureDigest("r"), json.RawMessage(`{"room_id":"r"}`)); err != nil {
			t.Fatalf("safe retry failed: %v", err)
		}
	})

	t.Run("close failure poisons handle", func(t *testing.T) {
		spool, path := openTestCaptureSpool(t)
		installCanonicalAppendFault(t, func(file *faultCanonicalAppendFile) { file.failClose = true })
		if _, err := spool.Prepare("m-1", "room", "r", "", captureDigest("r"), json.RawMessage(`{"room_id":"r"}`)); err == nil {
			t.Fatal("expected injected close failure")
		}
		if _, err := spool.Prepare("m-2", "room", "r2", "", captureDigest("r2"), json.RawMessage(`{"room_id":"r2"}`)); !errors.Is(err, ErrCanonicalSpoolPoisoned) {
			t.Fatalf("poisoned append = %v", err)
		}
		canonicalCaptureOpenAppend = func(path string) (canonicalAppendFile, bool, error) {
			_, statErr := os.Stat(path)
			created := errors.Is(statErr, os.ErrNotExist)
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			return file, created, err
		}
		if _, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow); err != nil {
			t.Fatalf("reopen could not classify synced frame: %v", err)
		}
	})

	t.Run("directory sync failure poisons handle", func(t *testing.T) {
		spool, path := openTestCaptureSpool(t)
		original := canonicalCaptureSyncParent
		canonicalCaptureSyncParent = func(string) error { return fmt.Errorf("injected directory sync failure") }
		defer func() { canonicalCaptureSyncParent = original }()
		if _, err := spool.Prepare("m-1", "room", "r", "", captureDigest("r"), json.RawMessage(`{"room_id":"r"}`)); err == nil {
			t.Fatal("expected injected directory sync failure")
		}
		if _, err := spool.Prepare("m-2", "room", "r2", "", captureDigest("r2"), json.RawMessage(`{"room_id":"r2"}`)); !errors.Is(err, ErrCanonicalSpoolPoisoned) {
			t.Fatalf("poisoned append = %v", err)
		}
		canonicalCaptureSyncParent = original
		if _, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow); err != nil {
			t.Fatalf("reopen could not classify frame: %v", err)
		}
	})
}

func TestCanonicalCaptureTruncatesOnlyTornFinalFrame(t *testing.T) {
	spool, path := openTestCaptureSpool(t)
	if _, err := spool.Prepare("m-1", "room", "r-1", "", captureDigest("after"), json.RawMessage(`{"room_id":"r-1"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	goodSize := info.Size()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{'B', 'C', 'S'}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow); err != nil {
		t.Fatalf("torn tail was not recoverable: %v", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != goodSize {
		t.Fatalf("torn tail size = %d want %d", info.Size(), goodSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[10] ^= 0x01 // complete first frame remains present but its checksum no longer matches
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow); !errors.Is(err, ErrCanonicalSpoolCorrupt) {
		t.Fatalf("interior corruption = %v", err)
	}
}

func TestCanonicalCaptureRecoveryProvesOrFreezes(t *testing.T) {
	t.Run("exact after commits", func(t *testing.T) {
		spool, _ := openTestCaptureSpool(t)
		after := captureDigest("after")
		if _, err := spool.Prepare("m-1", "artifact", "a", captureDigest("before"), after, json.RawMessage(`{"artifact_id":"a"}`)); err != nil {
			t.Fatal(err)
		}
		result, err := spool.Recover(func(family, object string) (string, bool, error) { return after, true, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(result.SynthesizedCommits) != 1 || len(spool.CommittedFacts()) != 1 {
			t.Fatalf("recovery = %+v", result)
		}
	})

	t.Run("exact before aborts", func(t *testing.T) {
		spool, _ := openTestCaptureSpool(t)
		before := captureDigest("before")
		if _, err := spool.Prepare("m-1", "artifact", "a", before, captureDigest("after"), json.RawMessage(`{"artifact_id":"a"}`)); err != nil {
			t.Fatal(err)
		}
		result, err := spool.Recover(func(family, object string) (string, bool, error) { return before, true, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(result.SynthesizedAborts) != 1 || len(spool.CommittedFacts()) != 0 {
			t.Fatalf("recovery = %+v", result)
		}
	})

	t.Run("ambiguous freezes family", func(t *testing.T) {
		spool, _ := openTestCaptureSpool(t)
		if _, err := spool.Prepare("m-1", "artifact", "a", captureDigest("before"), captureDigest("after"), json.RawMessage(`{"artifact_id":"a"}`)); err != nil {
			t.Fatal(err)
		}
		result, err := spool.Recover(func(family, object string) (string, bool, error) { return captureDigest("other"), true, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(result.FrozenFamilies) != 1 || !spool.Frozen("artifact") {
			t.Fatalf("recovery = %+v", result)
		}
		if _, err := spool.Prepare("m-2", "artifact", "b", "", captureDigest("b"), json.RawMessage(`{"artifact_id":"b"}`)); !errors.Is(err, ErrCanonicalFamilyFrozen) {
			t.Fatalf("frozen prepare = %v", err)
		}
	})
}

func TestCanonicalCaptureRecoveryAcceptsProvenLaterFamilyChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.spool")
	first := CanonicalSpoolRecord{Sequence: 1, Phase: CanonicalSpoolPrepared, MutationID: "m-1", Family: "board", ObjectKey: "a", BeforeStateDigest: captureDigest("a0"), AfterStateDigest: captureDigest("a1"), Fact: json.RawMessage(`{"card_id":"a"}`)}
	second := CanonicalSpoolRecord{Sequence: 2, Phase: CanonicalSpoolPrepared, MutationID: "m-2", Family: "board", ObjectKey: "b", BeforeStateDigest: captureDigest("b0"), AfterStateDigest: captureDigest("b1"), PreviousFamilyDigest: canonicalFamilyDigest(first), Fact: json.RawMessage(`{"card_id":"b"}`)}
	if _, err := appendCanonicalSpoolFrame(path, first); err != nil {
		t.Fatal(err)
	}
	if _, err := appendCanonicalSpoolFrame(path, second); err != nil {
		t.Fatal(err)
	}
	spool, err := OpenCanonicalCaptureSpool(path, CanonicalModeShadow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := spool.Recover(func(family, object string) (string, bool, error) {
		if object == "b" {
			return second.BeforeStateDigest, true, nil
		}
		return captureDigest("ambiguous"), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SynthesizedCommits) != 1 || result.SynthesizedCommits[0] != "m-1" || len(result.SynthesizedAborts) != 1 {
		t.Fatalf("recovery = %+v", result)
	}
}
