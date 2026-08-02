package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e9readiness"
)

func TestE9LocalIntegrationEnvironmentDropsNonToolParentState(t *testing.T) {
	base := []string{
		"PATH=/trusted/bin",
		"GOROOT=/trusted/go",
		"GOFLAGS=-run=Never",
		"HTTP_PROXY=http://prod.invalid:8080",
		"MEETING_MEMORY_PATH=/opt/meetingassist/data/meeting-memory.jsonl",
		"BONFIRE_CANONICAL_DATABASE_URL=postgres://production.invalid/live",
		"BONFIRE_AGENT_THREAD_WORKER=external",
		"OPENAI_API_KEY=live-secret",
	}
	got := e9EnvironmentMap(e9LocalIntegrationEnvironment(base, map[string]string{
		"HOME":                         "/tmp/e9/home",
		"E9_LOCAL_INTEGRATION_ROOT":    "/tmp/e9",
		"E9_LOCAL_INTEGRATION_RECEIPT": "/tmp/e9/receipt.json",
	}))
	want := map[string]string{
		"PATH":                         "/trusted/bin",
		"GOROOT":                       "/trusted/go",
		"HOME":                         "/tmp/e9/home",
		"E9_LOCAL_INTEGRATION_ROOT":    "/tmp/e9",
		"E9_LOCAL_INTEGRATION_RECEIPT": "/tmp/e9/receipt.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hermetic child environment mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRunLocalIntegrationRejectsHostileParentEnvironmentAndPreservesOutsideSentinels(t *testing.T) {
	outside := t.TempDir()
	hostileFiles := map[string]string{
		"MEETING_MEMORY_PATH":            "meeting-memory.jsonl",
		"KANBAN_BOARD_PATH":              "kanban-board.json",
		"BONFIRE_USERS_PATH":             "users.json",
		"BONFIRE_SESSIONS_PATH":          "sessions.json",
		"BONFIRE_ROOMS_PATH":             "rooms.json",
		"MEETINGS_PATH":                  "meetings.json",
		"NOTIFICATIONS_PATH":             "notifications.json",
		"ADMISSION_ANCHORS_PATH":         "admission-anchors.json",
		"EMBEDDINGS_PATH":                "embeddings.jsonl",
		"USAGE_LEDGER_PATH":              "usage-ledger.jsonl",
		"STRIDE_RUNTIME_SNAPSHOT_PATH":   "runtime.snapshot.json",
		"STRIDE_RUNTIME_GENERATION_PATH": "runtime.generation.json",
	}
	for key, name := range hostileFiles {
		path := filepath.Join(outside, name)
		if err := os.WriteFile(path, []byte("outside-sentinel:"+key+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	for key, name := range map[string]string{
		"BONFIRE_CODEX_SCRATCH_ROOT": "codex-scratch",
		"BONFIRE_CODEX_QUEUE_PATH":   "codex-queue",
		"BONFIRE_RENDER_QUEUE_PATH":  "render-queue",
	} {
		path := filepath.Join(outside, name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "outside-sentinel"), []byte(key), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	activationPath := filepath.Join(outside, "meeting-specialist-activation.json")
	if err := os.WriteFile(activationPath, []byte("outside-activation-sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STRIDE_MEETING_SPECIALIST_CONTROL_ACTIVATION_PATH", activationPath)

	for key, value := range map[string]string{
		"BONFIRE_CANONICAL_MODE":                    "required",
		"BONFIRE_CANONICAL_DATABASE_URL":            "postgres://production.invalid/live",
		"BONFIRE_PUBLIC_URL":                        "https://production.invalid",
		"BONFIRE_RESTORE_MODE":                      "restore",
		"STRIDE_MEETING_SPECIALIST_CONTROL_ENABLED": "true",
		"STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED":      "true",
		"BONFIRE_AGENT_THREAD_WORKER":               "external",
		"BONFIRE_CODEX_EXECUTION_ENABLED":           "true",
		"BONFIRE_CODEX_EXTERNAL_WRITE_ENABLED":      "true",
		"BONFIRE_WORKFLOW_TICKER_DISABLED":          "false",
		"BACKUP_DISABLED":                           "false",
		"BACKUP_S3_ENDPOINT":                        "https://production.invalid/s3",
		"OPENAI_API_KEY":                            "outside-provider-secret",
		"OPENAI_RESPONSES_BASE_URL":                 "https://production.invalid/openai",
		"ANTHROPIC_API_KEY":                         "outside-anthropic-secret",
		"ANTHROPIC_BASE_URL":                        "https://production.invalid/anthropic",
		"RESEND_API_KEY":                            "outside-resend-secret",
		"HTTP_PROXY":                                "http://production.invalid:8080",
		"HTTPS_PROXY":                               "http://production.invalid:8080",
		"GOFLAGS":                                   "-run=Never",
	} {
		t.Setenv(key, value)
	}

	before := e9SnapshotDirectory(t, outside)
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := runLocalIntegration(moduleRoot, 4*time.Minute)
	if err != nil {
		t.Fatalf("run local integration with hostile parent environment: %v", err)
	}
	if !receipt.TempResourcesOnly || receipt.NetworkScope != "loopback_only" || receipt.ProviderCalls || receipt.ProductionMutation || receipt.DockerMutation {
		t.Fatalf("receipt crossed the local-only boundary: %+v", receipt)
	}
	if err := e9readiness.ValidateLocalIntegrationReceipt(receipt); err != nil {
		t.Fatalf("receipt validation: %v", err)
	}
	after := e9SnapshotDirectory(t, outside)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("outside sentinel tree changed\nbefore: %#v\nafter:  %#v", before, after)
	}
}

type e9SentinelSnapshot struct {
	Mode fs.FileMode
	Data []byte
}

func e9SnapshotDirectory(t *testing.T, root string) map[string]e9SentinelSnapshot {
	t.Helper()
	snapshot := map[string]e9SentinelSnapshot{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		observation := e9SentinelSnapshot{Mode: info.Mode()}
		if entry.Type().IsRegular() {
			observation.Data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		snapshot[relative] = observation
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func e9EnvironmentMap(environ []string) map[string]string {
	result := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}
