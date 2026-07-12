package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func resetCapabilityRuntimeForTest(t *testing.T) {
	t.Helper()
	capabilityRuntime.Lock()
	previous := capabilityRuntime.states
	capabilityRuntime.states = make(map[string]capabilityRuntimeState)
	capabilityRuntime.Unlock()
	t.Cleanup(func() {
		capabilityRuntime.Lock()
		capabilityRuntime.states = previous
		capabilityRuntime.Unlock()
	})
}

func TestAIProviderFailureDoesNotFailTrafficReadiness(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("BACKUP_DISABLED", "true")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	recorder := httptest.NewRecorder()
	readinessHandler(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("traffic readiness status=%d body=%s, want 200 despite provider failure", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK           bool                      `json:"ok"`
		Degraded     []string                  `json:"degraded"`
		Capabilities map[string]map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if !payload.OK {
		t.Fatalf("readiness ok=false: %+v", payload)
	}
	for _, name := range []string{"scout", "stt", "recap", "brain", "embeddings"} {
		if payload.Capabilities[name]["status"] != "degraded" {
			t.Errorf("%s status=%v, want degraded", name, payload.Capabilities[name]["status"])
		}
		if !slices.Contains(payload.Degraded, name) {
			t.Errorf("degraded=%v, want %s", payload.Degraded, name)
		}
	}
}

func TestCapabilitiesHandlerExposesRequiredCapabilitySet(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("BACKUP_DISABLED", "true")
	recordCapabilityFailure(capabilityEmbedding, time.Now(), errors.New("dial /secret/internal/socket: provider exploded"))
	recorder := httptest.NewRecorder()
	capabilitiesHandler(recorder, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	for _, name := range []string{"scout", "stt", "recap", "brain", "embeddings", "workflows", "backup"} {
		if _, ok := payload.Capabilities[name]; !ok {
			t.Errorf("capabilities missing %q: %v", name, payload.Capabilities)
		}
	}
	if strings.Contains(recorder.Body.String(), "/secret/internal/socket") || strings.Contains(recorder.Body.String(), "provider exploded") {
		t.Fatalf("public capability response leaked raw operational error: %s", recorder.Body.String())
	}
}

func TestCapabilityProducerEvidenceIsAuthoritative(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	now := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	recordCapabilitySuccess("workflows", now.Add(-20*time.Second))
	recordCapabilityQueue("workflows", 3, 1, "half-open")
	evidence := capabilityEvidence("workflows", now, time.Minute)
	if evidence["lagSeconds"] != int64(20) || evidence["backlog"] != 3 || evidence["deadLetter"] != 1 || evidence["circuit"] != "half-open" {
		t.Fatalf("success/queue evidence=%v", evidence)
	}
	recordCapabilityQueue("workflows", 3, 1, "open")
	if status := capabilityStatus(capabilityEvidence("workflows", now, time.Minute), true); status != "degraded" {
		t.Fatalf("open circuit status=%q, want degraded", status)
	}

	recordCapabilityFailure("workflows", now, errors.New("launch failed"))
	evidence = capabilityEvidence("workflows", now, time.Minute)
	if evidence["lastError"] != "launch failed" {
		t.Fatalf("failure evidence=%v", evidence)
	}
	recordCapabilitySuccess("workflows", now.Add(time.Second))
	if evidence = capabilityEvidence("workflows", now.Add(time.Second), time.Minute); evidence["lastError"] != nil {
		t.Fatalf("success must clear prior error: %v", evidence)
	}
}

func TestCapabilityConfigurationAloneIsNotHealthyEvidence(t *testing.T) {
	if status := capabilityStatus(map[string]any{"enabled": true, "connected": true}, true); status != "degraded" {
		t.Fatalf("configured but unevidenced status=%q, want degraded", status)
	}
	if status := capabilityStatus(map[string]any{"enabled": true, "connected": false, "lastSuccessAt": time.Now().UTC().Format(time.RFC3339Nano)}, true); status != "degraded" {
		t.Fatalf("disconnected status=%q, want degraded", status)
	}
}

func TestBackupCapabilityDoesNotClaimLocalSnapshotIsDisasterRecovery(t *testing.T) {
	t.Setenv("BACKUP_DISABLED", "false")
	t.Setenv("BACKUP_INTERVAL_HOURS", "24")
	for _, name := range []string{"BACKUP_S3_ENDPOINT", "BACKUP_S3_BUCKET", "BACKUP_S3_ACCESS_KEY", "BACKUP_S3_SECRET_KEY", "BACKUP_S3_REGION", "BACKUP_ENCRYPTION_KEY"} {
		t.Setenv(name, "")
	}
	backupStatMu.Lock()
	previous := struct {
		run, restore                         time.Time
		ok                                   bool
		size                                 int64
		err, offsite, offsiteErr, restoreErr string
		ring                                 int
	}{backupLastRunAt, backupRestoreAt, backupLastOK, backupLastSize, backupLastErr, backupLastOffsite, backupOffsiteErr, backupRestoreErr, backupRingCount}
	backupLastRunAt = time.Time{}
	backupRestoreAt = time.Time{}
	backupLastOK = false
	backupLastSize = 0
	backupLastErr = ""
	backupLastOffsite = ""
	backupOffsiteErr = ""
	backupRestoreErr = ""
	backupRingCount = 0
	backupStatMu.Unlock()
	t.Cleanup(func() {
		backupStatMu.Lock()
		backupLastRunAt, backupRestoreAt = previous.run, previous.restore
		backupLastOK, backupLastSize = previous.ok, previous.size
		backupLastErr, backupLastOffsite, backupOffsiteErr, backupRestoreErr = previous.err, previous.offsite, previous.offsiteErr, previous.restoreErr
		backupRingCount = previous.ring
		backupStatMu.Unlock()
	})

	now := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	recordBackupOutcome(now, backupOutcome{sizeBytes: 42, ringKept: 2, offsite: "dormant"}, nil)
	snapshot := backupCapabilitySnapshot(now.Add(time.Minute))
	if snapshot["status"] != "degraded" || snapshot["localLastOK"] != true || snapshot["offsite"] != "dormant" || snapshot["restoreVerified"] != false {
		t.Fatalf("local-only backup snapshot=%v", snapshot)
	}

	recordBackupOutcome(now.Add(time.Minute), backupOutcome{offsite: "dormant"}, errors.New("disk full"))
	snapshot = backupCapabilitySnapshot(now.Add(2 * time.Minute))
	if snapshot["localLastOK"] != false || snapshot["lastError"] != "disk full" || snapshot["offsite"] != "dormant" {
		t.Fatalf("failed backup retained stale success evidence: %v", snapshot)
	}
	recordBackupRestoreVerification(now.Add(2*time.Minute), errors.New("restore checksum mismatch"))
	snapshot = backupCapabilitySnapshot(now.Add(3 * time.Minute))
	if snapshot["restoreVerified"] != false || snapshot["restoreError"] != "restore checksum mismatch" {
		t.Fatalf("failed restore snapshot=%v", snapshot)
	}
}

func TestLiveHandlerIsLivenessOnly(t *testing.T) {
	recorder := httptest.NewRecorder()
	liveHandler(recorder, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("live status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
