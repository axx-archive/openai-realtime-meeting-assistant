package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		Version  string   `json:"version"`
		Degraded []string `json:"degraded"`
		Checks   struct {
			App              bool           `json:"app"`
			Memory           bool           `json:"memoryStore"`
			MemoryFile       map[string]any `json:"memoryFile"`
			BoardFile        map[string]any `json:"boardFile"`
			AdmissionAnchors map[string]any `json:"admissionAnchors"`
			StrideE10W4      map[string]any `json:"strideE10W4"`
			StrideE10W5      map[string]any `json:"strideE10W5"`
			StrideE10W6      map[string]any `json:"strideE10W6"`
			Agents           struct {
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
	if payload.Version != serverBuildVersion {
		t.Fatalf("readiness version=%q, want running build %q", payload.Version, serverBuildVersion)
	}
	if !payload.Checks.App || !payload.Checks.Memory {
		t.Fatalf("readiness app/memory checks=%+v, want true", payload.Checks)
	}
	if payload.Checks.MemoryFile["ok"] != true || payload.Checks.BoardFile["ok"] != true {
		t.Fatalf("storage checks memory=%v board=%v, want ok", payload.Checks.MemoryFile, payload.Checks.BoardFile)
	}
	if payload.Checks.AdmissionAnchors["healthy"] != true {
		t.Fatalf("admission anchor readiness=%v, want healthy", payload.Checks.AdmissionAnchors)
	}
	if _, ok := payload.Checks.StrideE10W4["ready"]; !ok || payload.Checks.StrideE10W4["generation"] == nil || payload.Checks.StrideE10W4["enabledFeatures"] == nil {
		t.Fatalf("W4 readiness missing signed-runtime state: %v", payload.Checks.StrideE10W4)
	}
	if payload.Checks.StrideE10W5["ready"] != true || payload.Checks.StrideE10W6["ready"] == nil {
		t.Fatalf("W5/W6 readiness checks missing: W5=%v W6=%v", payload.Checks.StrideE10W5, payload.Checks.StrideE10W6)
	}
	if payload.Checks.Agents.Brain["enabled"] != true || payload.Checks.Agents.Board["enabled"] != true {
		t.Fatalf("agent checks=%+v, want enabled defaults", payload.Checks.Agents)
	}
	if len(payload.Degraded) == 0 || payload.Degraded[0] != "openai_api_key_missing" {
		t.Fatalf("degraded=%v, want missing OpenAI key noted without failing readiness", payload.Degraded)
	}
}

func TestReadinessHandlerFailsClosedWhenW5OrW6EnabledWithoutRuntime(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	previousLive := strideE10LiveProductRuntime
	previousW6 := strideE10W6RuntimeReadinessSnapshot()
	strideE10W5ProductState.Lock()
	previousW5Handler := strideE10W5ProductState.handler
	strideE10W5ProductState.handler = nil
	strideE10W5ProductState.Unlock()
	t.Cleanup(func() {
		strideE10LiveProductRuntime = previousLive
		publishStrideE10W6RuntimeReadiness(previousW6)
		strideE10W5ProductState.Lock()
		strideE10W5ProductState.handler = previousW5Handler
		strideE10W5ProductState.Unlock()
	})

	for _, tc := range []struct {
		feature  STRIDEFeature
		degraded string
	}{
		{STRIDEFeaturePersonMyMindContext, "stride_e10_w5_custody_unavailable"},
		{STRIDEFeatureNetworkProfilePublication, "stride_e10_w6_runtime_unavailable"},
	} {
		t.Run(string(tc.feature), func(t *testing.T) {
			runtime := NewStrideE10ProductLiveRuntime(nil)
			runtime.features[tc.feature] = true
			strideE10LiveProductRuntime = runtime
			publishStrideE10W6RuntimeReadiness(StrideE10W6RuntimeReadiness{})
			recorder := httptest.NewRecorder()
			readinessHandler(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), tc.degraded) {
				t.Fatalf("feature %s readiness status=%d body=%s", tc.feature, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestReadinessHandlerFailsClosedWhenAdmissionAnchorStoreIsUnavailable(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.admissionAnchors = nil
	app.admissionAnchorErr = ErrAdmissionAnchorStore
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	recorder := httptest.NewRecorder()
	readinessHandler(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status=%d body=%s, want 503", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "admission_anchor_store_degraded") || strings.Contains(recorder.Body.String(), ErrAdmissionAnchorStore.Error()) {
		t.Fatalf("readiness did not expose sanitized admission-anchor degradation: %s", recorder.Body.String())
	}
}

func TestReadinessHandlerFailsClosedWhenW4PersistenceFails(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	isolateStrideE10W4ReadinessForTest(t)
	updateStrideE10W4RuntimeReadiness(strideE10W4CanaryMode, 17, 2, "activation-test", "receipt-test", []STRIDEFeature{STRIDEFeatureWorkRecordPrivate})
	markStrideE10W4RuntimePersistenceFailed()

	recorder := httptest.NewRecorder()
	readinessHandler(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status=%d body=%s, want 503", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK       bool     `json:"ok"`
		Degraded []string `json:"degraded"`
		Checks   struct {
			StrideE10W4 map[string]any `json:"strideE10W4"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OK || !slices.Contains(payload.Degraded, "stride_e10_w4_persistence_failed") || payload.Checks.StrideE10W4["ready"] != false || payload.Checks.StrideE10W4["generation"] != float64(17) || payload.Checks.StrideE10W4["reason"] != "persistence_failed" {
		t.Fatalf("readiness payload=%+v", payload)
	}
}

func TestReadinessHandlerFailsClosedWhenBoardLifecycleIsFrozen(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.boardLifecycleFrozen = true
	app.boardLifecycleErr = errors.New("sensitive lifecycle detail")
	app.mu.Unlock()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	recorder := httptest.NewRecorder()
	readinessHandler(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status=%d body=%s, want 503", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK       bool     `json:"ok"`
		Degraded []string `json:"degraded"`
		Checks   struct {
			BoardLifecycle map[string]any `json:"boardLifecycle"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if payload.OK || !slices.Contains(payload.Degraded, "board_lifecycle_recovery_required") {
		t.Fatalf("readiness payload=%+v, want hard board-lifecycle failure", payload)
	}
	if payload.Checks.BoardLifecycle["healthy"] != false || payload.Checks.BoardLifecycle["reason"] != "recovery_required" {
		t.Fatalf("board lifecycle check=%v", payload.Checks.BoardLifecycle)
	}
	if strings.Contains(recorder.Body.String(), "sensitive lifecycle detail") {
		t.Fatalf("readiness leaked internal lifecycle error: %s", recorder.Body.String())
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
