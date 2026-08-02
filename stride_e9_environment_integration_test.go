package main

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e9readiness"
)

func TestE9DirectRootIntegrationRejectsHostileParentEnvironment(t *testing.T) {
	outside := t.TempDir()
	hostile := map[string]string{
		"E9_LOCAL_INTEGRATION_ROOT":                 "",
		"E9_LOCAL_INTEGRATION_RECEIPT":              "",
		e9readiness.LocalIntegrationHermeticEnv:     "",
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
	}
	for key, name := range map[string]string{
		"MEETING_MEMORY_PATH":                               "meeting-memory.jsonl",
		"KANBAN_BOARD_PATH":                                 "kanban-board.json",
		"BONFIRE_USERS_PATH":                                "users.json",
		"BONFIRE_SESSIONS_PATH":                             "sessions.json",
		"BONFIRE_ROOMS_PATH":                                "rooms.json",
		"MEETINGS_PATH":                                     "meetings.json",
		"NOTIFICATIONS_PATH":                                "notifications.json",
		"ADMISSION_ANCHORS_PATH":                            "admission-anchors.json",
		"EMBEDDINGS_PATH":                                   "embeddings.jsonl",
		"USAGE_LEDGER_PATH":                                 "usage-ledger.jsonl",
		"STRIDE_RUNTIME_SNAPSHOT_PATH":                      "runtime.snapshot.json",
		"STRIDE_RUNTIME_GENERATION_PATH":                    "runtime.generation.json",
		"STRIDE_MEETING_SPECIALIST_CONTROL_ACTIVATION_PATH": "meeting-specialist-activation.json",
	} {
		path := filepath.Join(outside, name)
		if err := os.WriteFile(path, []byte("direct-outside-sentinel:"+key+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		hostile[key] = path
	}
	before := e9RootSentinelTree(t, outside)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run", "^"+e9readiness.LocalIntegrationTestName+"$", "-test.count=1")
	command.Env = e9RootEnvironment(os.Environ(), hostile)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("direct root integration timed out: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("direct root integration failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	after := e9RootSentinelTree(t, outside)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("direct root integration changed outside sentinels\nbefore: %#v\nafter:  %#v", before, after)
	}
}

type e9RootSentinel struct {
	Mode fs.FileMode
	Data string
}

func e9RootSentinelTree(t *testing.T, root string) map[string]e9RootSentinel {
	t.Helper()
	result := map[string]e9RootSentinel{}
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
		observation := e9RootSentinel{Mode: info.Mode()}
		if entry.Type().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			observation.Data = string(raw)
		}
		result[relative] = observation
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func e9RootEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
