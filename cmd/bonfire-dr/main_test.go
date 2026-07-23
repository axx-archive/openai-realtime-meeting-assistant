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
	root := t.TempDir()
	protected := filepath.Join(root, "restore", "meeting-data")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	authorityPath := filepath.Join(root, "independent", "purge-authority.jsonl")
	var stdout bytes.Buffer
	err := run([]string{
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
