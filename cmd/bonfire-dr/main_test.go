package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-realtime-meeting-assistant/internal/dr"
)

func TestAuthorityAppendCLIProducesSignedBodyFreeRecord(t *testing.T) {
	t.Setenv("BONFIRE_DR_SIGNING_KEY_ID", "cli-test-key")
	t.Setenv("BONFIRE_DR_SIGNING_KEY", strings.Repeat("k", 32))
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(root, "restore", "meeting-data")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	authorityPath := filepath.Join(root, "independent", "purge-authority.jsonl")
	var stdout bytes.Buffer
	err = run([]string{
		"authority-append", "--authority", authorityPath, "--protected-root", protected,
		"--tenant", "tenant-a", "--high-water", "3", "--purge-digest", strings.Repeat("b", 64),
		"--release", strings.Repeat("a", 40), "--recorded-at", "2026-07-22T20:00:00Z",
	}, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Appended bool                    `json:"appended"`
		Record   dr.PurgeAuthorityRecord `json:"record"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if !output.Appended || output.Record.PurgeHighWater != 3 || output.Record.Signature == "" {
		t.Fatalf("output=%+v", output)
	}
	if bytes.Contains(stdout.Bytes(), []byte("private transcript body")) {
		t.Fatal("CLI authority output contained content body")
	}
}

func TestEnvelopeCLISealsAndOpensWithoutLeakingKey(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "capture.tar.gz")
	envelope := filepath.Join(root, "capture.enc")
	opened := filepath.Join(root, "opened.tar.gz")
	key := strings.Repeat("q", 32)
	plaintext := []byte("private four-root capture bundle")
	if err := os.WriteFile(input, plaintext, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONFIRE_DR_ENVELOPE_KEY", key)
	var sealed bytes.Buffer
	if err := run([]string{"envelope-seal", "--in", input, "--out", envelope}, &sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed.Bytes(), []byte(key)) || !bytes.Contains(sealed.Bytes(), []byte("plaintextSha256")) {
		t.Fatalf("unsafe or incomplete seal output: %s", sealed.String())
	}
	var openedOutput bytes.Buffer
	if err := run([]string{"envelope-open", "--in", envelope, "--out", opened}, &openedOutput); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("opened=%q want=%q", got, plaintext)
	}
	for _, path := range []string{envelope, opened} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%#o, want 0600", path, info.Mode().Perm())
		}
	}
}

func TestEnvelopeCLIReadsKeyFileWithoutFollowingLinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "envelope.key")
	key := strings.Repeat("r", 32)
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BONFIRE_DR_ENVELOPE_KEY", "")
	input := filepath.Join(root, "capture")
	if err := os.WriteFile(input, []byte("capture"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := run([]string{"envelope-seal", "--in", input, "--out", filepath.Join(root, "capture.enc"), "--key-file", keyFile}, &stdout); err != nil {
		t.Fatal(err)
	}
	linkedKeyFile := filepath.Join(root, "linked.key")
	if err := os.Symlink(keyFile, linkedKeyFile); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"envelope-seal", "--in", input, "--out", filepath.Join(root, "rejected.enc"), "--key-file", linkedKeyFile}, &stdout); err == nil {
		t.Fatal("symlinked key file accepted")
	}
}
