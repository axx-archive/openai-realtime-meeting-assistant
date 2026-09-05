package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestW0ServingPoolBudgetPreservesIdentity(t *testing.T) {
	for name, parse := range map[string]func(string) (*pgxpool.Config, error){"canonical": canonicalPoolConfig, "business": businessPoolConfig} {
		t.Run(name, func(t *testing.T) {
			c, err := parse("postgres://restricted_runtime:synthetic@localhost:5432/tenant_db?pool_max_conns=28&pool_min_conns=12&pool_min_idle_conns=12&application_name=w0_test&statement_timeout=45000")
			if err != nil {
				t.Fatal(err)
			}
			if c.MaxConns != 4 || c.MinConns != 0 || c.MinIdleConns != 0 {
				t.Fatalf("unexpected pool budget: %d/%d", c.MaxConns, c.MinConns)
			}
			if c.ConnConfig.User != "restricted_runtime" || c.ConnConfig.Database != "tenant_db" || c.ConnConfig.Password != "synthetic" || c.ConnConfig.RuntimeParams["statement_timeout"] != "45000" || c.ConnConfig.RuntimeParams["application_name"] != "w0_test" {
				t.Fatal("connection identity/settings changed")
			}
			if name == "business" && c.ConnConfig.ConnectTimeout != 5*time.Second {
				t.Fatal("Business connect bound changed")
			}
		})
	}
}

func TestW0RetiredRunnerIgnoresHeartbeat(t *testing.T) {
	previous := codexExecutionTestGate.Load()
	codexExecutionTestGate.Store(false)
	t.Cleanup(func() { codexExecutionTestGate.Store(previous) })
	t.Setenv("BONFIRE_AGENT_THREAD_WORKER", "codex_exec")
	t.Setenv("BONFIRE_CODEX_AGENT_THREADS", "true")
	t.Setenv("BONFIRE_CODEX_RUNNER_MODE", "sidecar_queue")
	for _, kind := range []string{"missing", "old", "fresh", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "heartbeat.json")
			t.Setenv("BONFIRE_CODEX_HEARTBEAT_PATH", path)
			var raw []byte
			switch kind {
			case "old", "fresh":
				at := time.Now()
				if kind == "old" {
					at = at.Add(-33 * 24 * time.Hour)
				}
				raw, _ = json.Marshal(map[string]any{"time": at.Format(time.RFC3339Nano), "ok": true, "runnerId": "historical"})
			case "invalid":
				raw = []byte("not json")
			}
			if raw != nil {
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := readinessCodexRunnerSnapshot()
			if s["enabled"] != false || s["status"] != "retired" || s["reason"] != "legacy_codex_executor_retired" {
				t.Fatalf("unexpected readiness: %v", s)
			}
			for _, key := range []string{"heartbeatOK", "heartbeatError", "heartbeatAgeSeconds", "runnerId"} {
				if _, ok := s[key]; ok {
					t.Fatalf("retired producer inspected heartbeat: %s", key)
				}
			}
			if codexExecutionEnabled() {
				t.Fatal("readiness opened dispatch")
			}
			if raw != nil {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(raw) {
					t.Fatal("historical heartbeat modified")
				}
			}
		})
	}
}
