package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthHandlerReportsService(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()

	healthHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if payload["ok"] != true || payload["service"] != "meetingassist" {
		t.Fatalf("health payload=%v, want ok meetingassist", payload)
	}
}

func TestReadinessHandlerReportsStorageAndAgentState(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()

	readinessHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("readiness status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK       bool     `json:"ok"`
		Service  string   `json:"service"`
		Degraded []string `json:"degraded"`
		Checks   struct {
			App        bool           `json:"app"`
			Memory     bool           `json:"memoryStore"`
			MemoryFile map[string]any `json:"memoryFile"`
			BoardFile  map[string]any `json:"boardFile"`
			Agents     struct {
				Brain map[string]any `json:"brain"`
				Board map[string]any `json:"board"`
			} `json:"agents"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode readiness payload: %v", err)
	}
	if !payload.OK || payload.Service != "meetingassist" {
		t.Fatalf("readiness payload=%+v, want ok meetingassist", payload)
	}
	if !payload.Checks.App || !payload.Checks.Memory {
		t.Fatalf("readiness app/memory checks=%+v, want true", payload.Checks)
	}
	if payload.Checks.MemoryFile["ok"] != true || payload.Checks.BoardFile["ok"] != true {
		t.Fatalf("storage checks memory=%v board=%v, want ok", payload.Checks.MemoryFile, payload.Checks.BoardFile)
	}
	if payload.Checks.Agents.Brain["enabled"] != true || payload.Checks.Agents.Board["enabled"] != true {
		t.Fatalf("agent checks=%+v, want enabled defaults", payload.Checks.Agents)
	}
	if len(payload.Degraded) == 0 || payload.Degraded[0] != "openai_api_key_missing" {
		t.Fatalf("degraded=%v, want missing OpenAI key noted without failing readiness", payload.Degraded)
	}
}

func TestReadinessHandlerFailsWhenStateDirectoryIsUnwritable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "missing-parent", "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "missing-parent", "board.json"))
	if err := os.WriteFile(filepath.Join(dir, "missing-parent"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	previousApp := kanbanApp
	kanbanApp = &kanbanBoardApp{memory: &meetingMemoryStore{}}
	t.Cleanup(func() {
		kanbanApp = previousApp
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()

	readinessHandler(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status=%d body=%s, want 503", recorder.Code, recorder.Body.String())
	}
}
