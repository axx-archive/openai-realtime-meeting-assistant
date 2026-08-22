package main

import (
	"context"
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
		OK           bool                       `json:"ok"`
		Degraded     []string                   `json:"degraded"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if !payload.OK {
		t.Fatalf("readiness ok=false: %+v", payload)
	}
	for _, name := range []string{"scout", "stt", "recap", "brain", "embeddings"} {
		var capability map[string]any
		if err := json.Unmarshal(payload.Capabilities[name], &capability); err != nil {
			t.Errorf("decode %s capability: %v", name, err)
			continue
		}
		if capability["status"] != "degraded" {
			t.Errorf("%s status=%v, want degraded", name, capability["status"])
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
	for _, name := range []string{"scout", "stt", "recap", "brain", "embeddings", "workflows", "attachmentAuthority", "backup"} {
		if _, ok := payload.Capabilities[name]; !ok {
			t.Errorf("capabilities missing %q: %v", name, payload.Capabilities)
		}
	}
	if strings.Contains(recorder.Body.String(), "/secret/internal/socket") || strings.Contains(recorder.Body.String(), "provider exploded") {
		t.Fatalf("public capability response leaked raw operational error: %s", recorder.Body.String())
	}
}

func TestAttachmentAuthorityDegradesCapabilityHealthWithoutBlockingTraffic(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("BACKUP_DISABLED", "true")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	app.pendingAttachmentUploadsMu.Lock()
	app.attachmentSourceStoreErr = errors.New("attachment ledger path is unavailable")
	app.pendingAttachmentUploadsMu.Unlock()

	snapshot, degraded := capabilitySnapshot(time.Now())
	attachments, ok := snapshot["attachmentAuthority"].(map[string]any)
	if !ok || attachments["status"] != "degraded" || !slices.Contains(degraded, capabilityAttachments) {
		t.Fatalf("attachment health snapshot=%v degraded=%v", attachments, degraded)
	}

	readiness := httptest.NewRecorder()
	readinessHandler(readiness, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readiness.Code != http.StatusOK {
		t.Fatalf("attachment-authority failure blocked traffic readiness: status=%d body=%s", readiness.Code, readiness.Body.String())
	}
	var traffic struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(readiness.Body.Bytes(), &traffic); err != nil || !traffic.OK {
		t.Fatalf("traffic readiness=%+v err=%v, want OK despite attachment degradation", traffic, err)
	}

	public := httptest.NewRecorder()
	capabilitiesHandler(public, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
	if strings.Contains(public.Body.String(), "attachment ledger path") {
		t.Fatalf("public capability response leaked attachment authority error: %s", public.Body.String())
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

func TestCapabilitySnapshotExposesEveryAmbientLaneAndCircuitTruth(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	mission := missionIntelligenceAgent()
	setAmbientAgentFailureForTest(app, mission.name, &ambientAgentFailure{windowID: "held-brain", attempts: ambientProviderMaxWindowAttempts, providerOpen: true})

	snapshot, degraded := capabilitySnapshot(time.Now().UTC())
	workers, ok := snapshot["ambientWorkers"].(map[string]any)
	if !ok {
		t.Fatalf("ambientWorkers type=%T, want map", snapshot["ambientWorkers"])
	}
	for _, name := range []string{
		"brain", "missionIntel", "decisionLedger", "narrative",
		"meetingDigest", "dayDigest", "entityLedger", "companyDigest",
		"researchSuggestion", "slopClassifier", "tasteAnalyst", "houseStyle",
	} {
		worker, ok := workers[name].(map[string]any)
		if !ok {
			t.Errorf("ambient worker %q missing: %v", name, workers)
			continue
		}
		if _, ok := worker["enabled"].(bool); !ok {
			t.Errorf("ambient worker %q missing enabled truth: %v", name, worker)
		}
		if strings.TrimSpace(asString(worker["provider"])) == "" || strings.TrimSpace(asString(worker["status"])) == "" {
			t.Errorf("ambient worker %q missing provider/status truth: %v", name, worker)
		}
	}
	missionHealth := workers["missionIntel"].(map[string]any)
	if missionHealth["circuit"] != "open" || missionHealth["retrySuppressed"] != true || missionHealth["retryAttempts"] != ambientProviderMaxWindowAttempts {
		t.Fatalf("mission ambient circuit health=%v", missionHealth)
	}
	if !slices.Contains(degraded, "ambient.missionIntel") {
		t.Fatalf("degraded=%v, want ambient.missionIntel", degraded)
	}
	if narrative := workers["narrative"].(map[string]any); narrative["provider"] != providerOpenAI {
		t.Fatalf("installed Anthropic key changed narrative provider=%v, want %s", narrative["provider"], providerOpenAI)
	}
}

func TestAmbientWorkerHealthExposesSupervisorAndDisabledBackfillHazard(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})

	agent := meetingDigestAgent()
	t.Setenv(agent.disabledEnv, "true")
	t.Setenv(agent.backfillEnv, "1")
	disabled := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if disabled["status"] != "degraded" || disabled["enabled"] != false || disabled["backfillArmed"] != true || disabled["unsafeActivation"] != true {
		t.Fatalf("disabled/backfill health=%v", disabled)
	}
	if disabled["supervisorRegistered"] != false || disabled["supervisorRunning"] != false {
		t.Fatalf("disabled worker supervisor health=%v", disabled)
	}

	t.Setenv(agent.disabledEnv, "false")
	t.Setenv(agent.backfillEnv, "false")
	beforeStart := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if beforeStart["supervisorError"] != true || beforeStart["status"] != "degraded" {
		t.Fatalf("enabled worker without supervisor health=%v", beforeStart)
	}
	app.startAmbientAgent(agent, "test-openai-key")
	afterStart := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if afterStart["supervisorRegistered"] != true || afterStart["supervisorRunning"] != true || afterStart["supervisorError"] != nil {
		t.Fatalf("started worker supervisor health=%v", afterStart)
	}
}

func TestResearchSuggestionHealthNamesSTRIDESupersessionWithoutMaskingLegacyFailures(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("RESEARCH_SUGGESTION_DISABLED", "false")
	t.Setenv("RESEARCH_SUGGESTION_BACKFILL", "false")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	agent := researchSuggestionAgent()

	t.Run("STRIDE Suggested Work owns the lane", func(t *testing.T) {
		app.strideRuntime = &STRIDERuntime{config: STRIDERuntimeConfig{ProductPreviewEnabled: true}}
		health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
		if health["status"] != "superseded" || health["ownershipState"] != "superseded" || health["supersededBy"] != "stride_suggested_work" || health["fenced"] != true {
			t.Fatalf("STRIDE-owned suggestion health=%v", health)
		}
		if health["configuredEnabled"] != true || health["effectiveEnabled"] != false || health["supervisorRequired"] != false || health["supervisorError"] != nil || health["analysisReady"] != false {
			t.Fatalf("STRIDE-owned effective/supervisor health=%v", health)
		}
		_, degraded := capabilitySnapshot(time.Now().UTC())
		if slices.Contains(degraded, "ambient.researchSuggestion") {
			t.Fatalf("intentional STRIDE supersession counted as degradation: %v", degraded)
		}
	})

	t.Run("legacy enabled worker still requires its supervisor", func(t *testing.T) {
		app.strideRuntime = nil
		health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
		if health["status"] != "degraded" || health["ownershipState"] != "active" || health["effectiveEnabled"] != true || health["supervisorRequired"] != true || health["supervisorError"] != true {
			t.Fatalf("unowned legacy suggestion health=%v", health)
		}
		_, degraded := capabilitySnapshot(time.Now().UTC())
		if !slices.Contains(degraded, "ambient.researchSuggestion") {
			t.Fatalf("missing legacy supervisor was not counted as degradation: %v", degraded)
		}
	})

	t.Run("ambiguous legacy checkpoint remains degraded after supersession", func(t *testing.T) {
		app.strideRuntime = &STRIDERuntime{config: STRIDERuntimeConfig{ProductPreviewEnabled: true}}
		roomID := "room-suggestion-ambiguous"
		state := ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{
			ambientAgentScopeKey(agent, roomID): {
				Agent: agent.name, RoomID: roomID, InputKind: agent.inputKind, ArtifactKind: agent.artifactKind,
				CursorMetadataKey: agent.cursorMetadataKey, BlockedReason: ambientContinuityAmbiguous,
			},
		}}
		if err := persistAmbientHeldWindowState(app.ambientHeldWindowPath(), state); err != nil {
			t.Fatalf("persist ambiguous suggestion checkpoint: %v", err)
		}
		health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
		if health["ownershipState"] != "superseded" || health["status"] != "degraded" || health["continuityError"] != true || health["checkpointStatus"] != "blocked" || health["blockedScopeCount"] != 1 {
			t.Fatalf("supersession masked ambiguous continuity=%v", health)
		}
		_, degraded := capabilitySnapshot(time.Now().UTC())
		if !slices.Contains(degraded, "ambient.researchSuggestion") {
			t.Fatalf("ambiguous superseded checkpoint was not counted as degradation: %v", degraded)
		}
	})
}

func TestAmbientWorkerHealthValidatesCheckpointReferencesWithoutLeakingIDs(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	agent := meetingBrainAgent()

	appendTestTranscript(t, app, "checkpoint-transcript", "Authorized meeting context.")
	if _, _, err := app.memory.appendBrainWriteUp("checkpoint-brain", "Prior brain.", map[string]string{
		"roomId":                      officeRoomID,
		meetingBrainCursorMetadataKey: "checkpoint-transcript",
	}); err != nil {
		t.Fatalf("append brain: %v", err)
	}
	appendTestTranscript(t, app, "checkpoint-held-transcript", "Pending authorized meeting context.")
	state := ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{
		agent.name: {Agent: agent.name, RoomID: officeRoomID, BaselineID: "checkpoint-brain", WindowID: "checkpoint-held-transcript"},
	}}
	if err := persistAmbientHeldWindowState(app.ambientHeldWindowPath(), state); err != nil {
		t.Fatalf("persist valid checkpoint: %v", err)
	}
	valid := ambientWorkerCheckpointDiagnostics(app, agent)
	if valid["checkpointStatus"] != "held" || valid["invalidScopeCount"] != 0 || valid["heldScopeCount"] != 1 {
		t.Fatalf("valid legacy-artifact checkpoint diagnostics=%v", valid)
	}

	state.Windows[agent.name] = ambientHeldWindow{Agent: agent.name, RoomID: officeRoomID, BaselineID: "missing-baseline", WindowID: "checkpoint-brain"}
	if err := persistAmbientHeldWindowState(app.ambientHeldWindowPath(), state); err != nil {
		t.Fatalf("persist invalid checkpoint: %v", err)
	}
	invalid := ambientWorkerCheckpointDiagnostics(app, agent)
	if invalid["checkpointStatus"] != "invalid" || invalid["invalidScopeCount"] != 1 {
		t.Fatalf("invalid checkpoint diagnostics=%v", invalid)
	}
	for _, value := range invalid {
		if strings.Contains(asString(value), "checkpoint-brain") || strings.Contains(asString(value), "missing-baseline") {
			t.Fatalf("checkpoint diagnostics leaked raw ids: %v", invalid)
		}
	}
}

func TestSpecialtyWorkerHealthRequiresItsTypedArtifactSuccess(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	// A newly persisted generic artifact is not evidence for either specialty
	// worker: their artifacts share os_artifact with unrelated workflows.
	if _, appended, err := app.createOSArtifactWithMetadata("workflow", "Unrelated workflow", "complete", "AJ", nil); err != nil || !appended {
		t.Fatalf("seed unrelated artifact: appended=%v err=%v", appended, err)
	}
	workers := ambientWorkersCapabilitySnapshot(time.Now().UTC(), true)
	for _, name := range []string{"tasteAnalyst", "houseStyle"} {
		worker := workers[name].(map[string]any)
		if worker["status"] != "degraded" || worker["lastSuccessAt"] != nil {
			t.Fatalf("unrelated os_artifact made %s healthy: %v", name, worker)
		}
	}

	signal := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, map[string]string{"removedSections": "Intro"})
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", func(context.Context, string, openAITextRequest) (string, error) {
		return tasteTestResponse(t, []string{signal.ID}, nil), nil
	}); err != nil {
		t.Fatalf("run taste analyst: %v", err)
	}
	published := seedHouseStyleSourceArtifact(t, app)
	if err := app.runHouseStyleDistillerOnce(context.Background(), "test-key", func(context.Context, string, openAITextRequest) (string, error) {
		return houseStyleTestBody(published.ID), nil
	}); err != nil {
		t.Fatalf("run house style distiller: %v", err)
	}

	workers = ambientWorkersCapabilitySnapshot(time.Now().UTC(), true)
	for _, name := range []string{"tasteAnalyst", "houseStyle"} {
		worker := workers[name].(map[string]any)
		if worker["status"] != "healthy" || strings.TrimSpace(asString(worker["lastSuccessAt"])) == "" {
			t.Fatalf("%s own typed artifact was not accepted as success: %v", name, worker)
		}
	}
}

func TestSpecialtyWorkerHealthUsesWorkDueCadenceNotPollingAge(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	resetCapabilityRuntimeForTest(t)
	now := time.Now().UTC()
	aged := now.Add(-3 * time.Hour) // stale by the 1h poll, current by weekly/monthly product cadence

	t.Run("taste analyst", func(t *testing.T) {
		t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "15")
		app := newIsolatedKanbanBoardApp(t)
		previousApp := kanbanApp
		kanbanApp = app
		defer func() { kanbanApp = previousApp }()
		title := tasteProfileTitle("AJ")
		if _, appended, err := app.createOSArtifactWithMetadata("workflow", title, "## Voice & style\n- Grounded profile.", scoutParticipantName, map[string]string{
			"title": title, tasteProfileArtifactTypeKey: tasteProfileArtifactType,
			tasteProfileUserKey: "AJ", tasteProfileDistilledAtKey: aged.Format(time.RFC3339Nano),
		}); err != nil || !appended {
			t.Fatalf("seed aged taste profile: appended=%v err=%v", appended, err)
		}
		recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, map[string]string{"removedSections": "Intro"})

		health := ambientWorkerCapabilitySnapshot(tasteAnalystAgent(), now, providerAnthropic, true)
		if health["status"] != "healthy" || health["workDue"] != false || health["artifactFreshness"] != "not_due" || health["stale"] == true {
			t.Fatalf("aged but not-due taste health=%v", health)
		}
		t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")
		health = ambientWorkerCapabilitySnapshot(tasteAnalystAgent(), now, providerAnthropic, true)
		if health["status"] != "degraded" || health["workDue"] != true || health["artifactFreshness"] != "work_due" || health["stale"] != true {
			t.Fatalf("due stale taste health=%v", health)
		}
	})

	t.Run("house style", func(t *testing.T) {
		app := newIsolatedKanbanBoardApp(t)
		previousApp := kanbanApp
		kanbanApp = app
		defer func() { kanbanApp = previousApp }()
		seedHouseStyleSourceArtifact(t, app)
		if _, appended, err := app.createOSArtifactWithMetadata("workflow", houseStyleArtifactTitle, "## Structures that survive grills\n- Grounded style.", scoutParticipantName, map[string]string{
			"title": houseStyleArtifactTitle, tasteProfileArtifactTypeKey: houseStyleArtifactType,
			tasteProfileDistilledAtKey: aged.Format(time.RFC3339Nano),
		}); err != nil || !appended {
			t.Fatalf("seed aged house style: appended=%v err=%v", appended, err)
		}

		health := ambientWorkerCapabilitySnapshot(houseStyleDistillerAgent(), now, providerAnthropic, true)
		if health["status"] != "healthy" || health["workDue"] != false || health["artifactFreshness"] != "not_due" || health["stale"] == true {
			t.Fatalf("aged but not-due house health=%v", health)
		}
		seedHouseStyleBinderArtifact(t, app)
		health = ambientWorkerCapabilitySnapshot(houseStyleDistillerAgent(), now, providerAnthropic, true)
		if health["status"] != "degraded" || health["workDue"] != true || health["artifactFreshness"] != "work_due" || health["stale"] != true {
			t.Fatalf("due stale house health=%v", health)
		}
	})
}

func TestSpecialtyIdlePollWithoutTypedArtifactDoesNotManufactureHealth(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	resetCapabilityRuntimeForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	defer func() { kanbanApp = previousApp }()
	now := time.Now().UTC()

	for _, test := range []struct {
		name  string
		agent ambientAgentConfig
	}{
		{name: tasteAnalystAgentName, agent: tasteAnalystAgent()},
		{name: houseStyleAgentName, agent: houseStyleDistillerAgent()},
	} {
		recordCapabilityPoll(test.name, now)
		health := ambientWorkerCapabilitySnapshot(test.agent, now.Add(time.Second), providerAnthropic, true)
		if health["status"] != "degraded" || health["lastSuccessAt"] != nil {
			t.Fatalf("idle poll manufactured %s success: %v", test.name, health)
		}
		if strings.TrimSpace(asString(health["lastPollAt"])) == "" || health["workDue"] != false {
			t.Fatalf("idle poll liveness/work cadence missing for %s: %v", test.name, health)
		}
	}
}

func TestSpecialtyDueSourceDisappearsDoesNotManufactureSuccess(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	assertNoOpPreservesArtifactAndError := func(t *testing.T, app *kanbanBoardApp, agent ambientAgentConfig, oldArtifactAt time.Time, now time.Time, runtimeSuccessBefore time.Time) {
		t.Helper()
		app.recordSpecialtyCapabilityCompletion(agent, now, false)
		state := capabilityState(agent.name)
		if !state.LastSuccess.Equal(runtimeSuccessBefore) {
			t.Fatalf("no-op completion refreshed runtime success: before=%s state=%+v", runtimeSuccessBefore, state)
		}
		if state.LastPoll.IsZero() {
			t.Fatalf("no-op completion did not retain liveness evidence: %+v", state)
		}
		if !strings.Contains(state.LastError, "injected prior provider failure") {
			t.Fatalf("no-op completion cleared prior error: %+v", state)
		}

		health := ambientWorkerCapabilitySnapshot(agent, now.Add(time.Second), providerAnthropic, true)
		if health["status"] != "degraded" || health["lastSuccessAt"] != oldArtifactAt.Format(time.RFC3339Nano) || health["typedArtifactPresent"] != true {
			t.Fatalf("no-op completion refreshed authoritative artifact success: %v", health)
		}
		if !strings.Contains(asString(health["lastError"]), "injected prior provider failure") || strings.TrimSpace(asString(health["lastPassAt"])) == "" {
			t.Fatalf("no-op completion hid error/liveness evidence: %v", health)
		}

		// An ordinary idle poll is liveness only too: it may advance LastPoll,
		// but neither useful-work evidence nor the error contract.
		app.recordSpecialtyCapabilityCompletion(agent, now.Add(2*time.Second), false)
		idle := capabilityState(agent.name)
		if !idle.LastSuccess.Equal(runtimeSuccessBefore) || !strings.Contains(idle.LastError, "injected prior provider failure") {
			t.Fatalf("idle poll changed success/error: %+v", idle)
		}
	}

	assertPersistedOutputAdvancesAndClears := func(t *testing.T, app *kanbanBoardApp, agent ambientAgentConfig, oldArtifactAt time.Time, now time.Time, persisted bool) {
		t.Helper()
		if !persisted {
			t.Fatal("actual specialty output did not report a durable write")
		}
		app.recordSpecialtyCapabilityCompletion(agent, now, true)
		health := ambientWorkerCapabilitySnapshot(agent, now.Add(time.Second), providerAnthropic, true)
		at, err := time.Parse(time.RFC3339Nano, asString(health["lastSuccessAt"]))
		if err != nil || !at.After(oldArtifactAt) {
			t.Fatalf("typed artifact success did not advance beyond %s: %v", oldArtifactAt, health)
		}
		if health["status"] != "healthy" || health["lastError"] != nil || health["typedArtifactPresent"] != true {
			t.Fatalf("persisted specialty output did not clear prior error: %v", health)
		}
	}

	t.Run("taste analyst", func(t *testing.T) {
		resetCapabilityRuntimeForTest(t)
		t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")
		app := newIsolatedKanbanBoardApp(t)
		previousApp := kanbanApp
		kanbanApp = app
		defer func() { kanbanApp = previousApp }()
		now := time.Now().UTC()
		oldArtifactAt := now.Add(-45 * 24 * time.Hour)
		title := tasteProfileTitle("AJ")
		if _, appended, err := app.createOSArtifactWithMetadata("workflow", title, "## Voice & style\n- Old grounded profile.", scoutParticipantName, map[string]string{
			"title": title, tasteProfileArtifactTypeKey: tasteProfileArtifactType,
			tasteProfileUserKey: "AJ", tasteProfileDistilledAtKey: oldArtifactAt.Format(time.RFC3339Nano),
		}); err != nil || !appended {
			t.Fatalf("seed old taste profile: appended=%v err=%v", appended, err)
		}
		runtimeSuccessBefore := now.Add(-time.Minute)
		recordCapabilitySuccess(tasteAnalystAgentName, runtimeSuccessBefore)
		recordCapabilityFailure(tasteAnalystAgentName, now.Add(-30*time.Second), errors.New("injected prior provider failure"))

		signal := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, map[string]string{"removedSections": "Intro"})
		if !tasteAnalystWorkDue(app, now) {
			t.Fatal("taste analyst source did not make work due")
		}
		if _, deleted, err := app.memory.deleteEntryByID(signal.ID); err != nil || !deleted {
			t.Fatalf("remove due taste source: deleted=%v err=%v", deleted, err)
		}
		persisted, err := app.runTasteAnalystOnceResult(context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
			t.Fatal("provider responder called after due source disappeared")
			return "", nil
		})
		if err != nil || persisted {
			t.Fatalf("taste no-op pass: %v", err)
		}
		assertNoOpPreservesArtifactAndError(t, app, tasteAnalystAgent(), oldArtifactAt, now, runtimeSuccessBefore)

		fresh := recordTasteTestSignal(t, app, "AJ", signalEventSurveyOff, map[string]string{"note": "kill the buzzwords"})
		persisted, err = app.runTasteAnalystOnceResult(context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
			return tasteTestResponse(t, []string{fresh.ID}, nil), nil
		})
		if err != nil {
			t.Fatalf("taste persisted pass: %v", err)
		}
		assertPersistedOutputAdvancesAndClears(t, app, tasteAnalystAgent(), oldArtifactAt, time.Now().UTC(), persisted)
	})

	t.Run("house style", func(t *testing.T) {
		resetCapabilityRuntimeForTest(t)
		app := newIsolatedKanbanBoardApp(t)
		previousApp := kanbanApp
		kanbanApp = app
		defer func() { kanbanApp = previousApp }()
		now := time.Now().UTC()
		oldArtifactAt := now.Add(-45 * 24 * time.Hour)
		if _, appended, err := app.createOSArtifactWithMetadata("workflow", houseStyleArtifactTitle, "## Structures that survive grills\n- Old grounded house style.", scoutParticipantName, map[string]string{
			"title": houseStyleArtifactTitle, tasteProfileArtifactTypeKey: houseStyleArtifactType,
			tasteProfileDistilledAtKey: oldArtifactAt.Format(time.RFC3339Nano),
		}); err != nil || !appended {
			t.Fatalf("seed old house style: appended=%v err=%v", appended, err)
		}
		runtimeSuccessBefore := now.Add(-time.Minute)
		recordCapabilitySuccess(houseStyleAgentName, runtimeSuccessBefore)
		recordCapabilityFailure(houseStyleAgentName, now.Add(-30*time.Second), errors.New("injected prior provider failure"))

		published := seedHouseStyleSourceArtifact(t, app)
		if !houseStyleWorkDue(app, now) {
			t.Fatal("house-style source did not make work due")
		}
		_, projection, deleted, err := app.memory.deleteOSArtifactWithProjection(published.ID)
		if err != nil || !deleted {
			t.Fatalf("remove due house-style source: deleted=%v err=%v", deleted, err)
		}
		revokeArtifactDeletionProjection(projection)
		persisted, err := app.runHouseStyleDistillerOnceResult(context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
			t.Fatal("provider responder called after due source disappeared")
			return "", nil
		})
		if err != nil || persisted {
			t.Fatalf("house-style no-op pass: %v", err)
		}
		assertNoOpPreservesArtifactAndError(t, app, houseStyleDistillerAgent(), oldArtifactAt, now, runtimeSuccessBefore)

		fresh := seedHouseStyleSourceArtifact(t, app)
		persisted, err = app.runHouseStyleDistillerOnceResult(context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
			return houseStyleTestBody(fresh.ID), nil
		})
		if err != nil {
			t.Fatalf("house-style persisted pass: %v", err)
		}
		assertPersistedOutputAdvancesAndClears(t, app, houseStyleDistillerAgent(), oldArtifactAt, time.Now().UTC(), persisted)
	})
}

func TestCapabilitySnapshotExposesPerRoomScoutReconnectCircuitAndRedactsError(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	calls := 0
	app, bundle, transport, scope := newScopedRoomScoutTransportTest(t, func(context.Context, *openAIRoomScoutTransport, uint64) (roomScoutProviderSession, error) {
		calls++
		if calls == 1 {
			return &fakeRoomScoutProviderSession{}, nil
		}
		return nil, errors.New("dial /secret/scout-provider.sock: unavailable")
	})
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	for attempt := 0; attempt < roomScoutRestartMaxAttempts; attempt++ {
		transport.mu.Lock()
		generation := transport.generation
		transport.mu.Unlock()
		transport.replaceSession(roomScoutRestartRequest{generation: generation, reason: "health circuit test"})
	}
	bundle.markDegraded(errors.New("dial /secret/scout-provider.sock: unavailable"))

	snapshot, degraded := capabilitySnapshot(time.Now().UTC())
	scout := snapshot["scout"].(map[string]any)
	if scout["circuit"] != "open" || scout["retrySuppressed"] != true || !slices.Contains(degraded, capabilityScout) {
		t.Fatalf("aggregate Scout health=%v degraded=%v", scout, degraded)
	}
	rows, ok := scout["rooms"].([]map[string]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("Scout rooms type=%T rows=%v", scout["rooms"], scout["rooms"])
	}
	row := rows[0]
	if row["roomId"] != scope.RoomID || row["circuit"] != "open" || row["retrySuppressed"] != true || row["retryAttempts"] != roomScoutRestartMaxAttempts {
		t.Fatalf("room Scout health=%v", row)
	}

	recorder := httptest.NewRecorder()
	capabilitiesHandler(recorder, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
	if strings.Contains(recorder.Body.String(), "/secret/scout-provider.sock") || strings.Contains(recorder.Body.String(), "unavailable") {
		t.Fatalf("public room Scout health leaked provider details: %s", recorder.Body.String())
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
