package main

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// capabilityRuntimeState is the common producer-facing health contract. Workers
// update it when they have authoritative evidence; the HTTP snapshot combines
// that evidence with boot configuration and live in-process state.
type capabilityRuntimeState struct {
	LastSuccess time.Time
	LastFailure time.Time
	LastError   string
	Backlog     *int
	DeadLetters *int
	Circuit     string
}

const (
	capabilityScout     = "scout"
	capabilitySTT       = "stt"
	capabilityRecap     = "recap"
	capabilityBrain     = "brain"
	capabilityEmbedding = "embeddings"
	capabilityWorkflows = "workflows"
)

var capabilityRuntime = struct {
	sync.RWMutex
	states map[string]capabilityRuntimeState
}{states: make(map[string]capabilityRuntimeState)}

func recordCapabilitySuccess(name string, at time.Time) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	capabilityRuntime.Lock()
	state := capabilityRuntime.states[name]
	state.LastSuccess = at
	state.LastError = ""
	capabilityRuntime.states[name] = state
	capabilityRuntime.Unlock()
}

func recordCapabilityFailure(name string, at time.Time, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	capabilityRuntime.Lock()
	state := capabilityRuntime.states[name]
	state.LastFailure = at
	if err != nil {
		state.LastError = err.Error()
	} else {
		state.LastError = "unknown failure"
	}
	capabilityRuntime.states[name] = state
	capabilityRuntime.Unlock()
}

// recordCapabilityQueue is intentionally separate from success/failure: queue
// depth and dead letters are authoritative only for producers that own a queue.
func recordCapabilityQueue(name string, backlog, deadLetters int, circuit string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if backlog < 0 {
		backlog = 0
	}
	if deadLetters < 0 {
		deadLetters = 0
	}
	capabilityRuntime.Lock()
	state := capabilityRuntime.states[name]
	state.Backlog = intPointer(backlog)
	state.DeadLetters = intPointer(deadLetters)
	state.Circuit = strings.TrimSpace(circuit)
	capabilityRuntime.states[name] = state
	capabilityRuntime.Unlock()
}

func intPointer(v int) *int { return &v }

func capabilityState(name string) capabilityRuntimeState {
	capabilityRuntime.RLock()
	defer capabilityRuntime.RUnlock()
	return capabilityRuntime.states[name]
}

func capabilityEvidence(name string, now time.Time, staleAfter time.Duration) map[string]any {
	state := capabilityState(name)
	out := map[string]any{}
	if !state.LastSuccess.IsZero() {
		out["lastSuccessAt"] = state.LastSuccess.UTC().Format(time.RFC3339Nano)
		age := now.Sub(state.LastSuccess)
		if age < 0 {
			age = 0
		}
		out["lagSeconds"] = int64(age.Seconds())
		if staleAfter > 0 {
			out["stale"] = age > staleAfter
		}
	}
	if !state.LastFailure.IsZero() {
		out["lastFailureAt"] = state.LastFailure.UTC().Format(time.RFC3339Nano)
	}
	if state.LastError != "" {
		out["lastError"] = state.LastError
	}
	if state.Backlog != nil {
		out["backlog"] = *state.Backlog
	}
	if state.DeadLetters != nil {
		out["deadLetter"] = *state.DeadLetters
	}
	if state.Circuit != "" {
		out["circuit"] = state.Circuit
	}
	return out
}

func capabilityStatus(base map[string]any, providerReady bool) string {
	if enabled, ok := base["enabled"].(bool); ok && !enabled {
		return "disabled"
	}
	if !providerReady || base["lastError"] != nil || base["stale"] == true || base["circuit"] == "open" {
		return "degraded"
	}
	if connected, reported := base["connected"].(bool); reported && !connected {
		return "degraded"
	}
	// Configuration is not success evidence. Until a producer or persisted
	// artifact proves that the capability has completed useful work, report it
	// as degraded instead of manufacturing a healthy state from a present key.
	if _, evidenced := base["lastSuccessAt"]; !evidenced {
		return "degraded"
	}
	return "healthy"
}

func mergeCapabilityEvidence(dst map[string]any, src map[string]any) {
	for key, value := range src {
		dst[key] = value
	}
}

func latestCapabilityArtifact(kind string) (time.Time, bool) {
	if kanbanApp == nil || kanbanApp.memory == nil {
		return time.Time{}, false
	}
	entries := kanbanApp.memory.entriesOfKind(kind, 1)
	if len(entries) == 0 {
		return time.Time{}, false
	}
	return entries[len(entries)-1].CreatedAt, true
}

// ambientCapabilityEvidence surfaces only state the ambient runner actually
// owns: persisted output is success evidence; live retry records establish an
// open circuit. It never fabricates a success timestamp from process startup.
func ambientCapabilityEvidence(name string, agent ambientAgentConfig, now time.Time) map[string]any {
	out := capabilityEvidence(name, now, 2*agent.interval())
	if at, ok := latestCapabilityArtifact(agent.artifactKind); ok {
		persisted := capabilityEvidenceFromSuccess(at, now, 2*agent.interval())
		if _, reported := out["lastSuccessAt"]; !reported {
			mergeCapabilityEvidence(out, persisted)
		}
	}
	if kanbanApp == nil {
		return out
	}
	deadLetters := 0
	if kanbanApp.memory != nil {
		for _, entry := range kanbanApp.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0) {
			if strings.TrimSpace(entry.Metadata[deadLetterAgentMetadataKey]) == agent.name {
				deadLetters++
			}
		}
		out["deadLetter"] = deadLetters
	}
	kanbanApp.mu.Lock()
	retries := 0
	var retryAt time.Time
	for key, failure := range kanbanApp.agentFailures {
		if failure == nil || (key != agent.name && !strings.HasPrefix(key, agent.name+"@")) {
			continue
		}
		retries += failure.attempts
		if failure.backoffUntil.After(retryAt) {
			retryAt = failure.backoffUntil
		}
	}
	kanbanApp.mu.Unlock()
	if retries > 0 {
		out["circuit"] = "open"
		out["retryAttempts"] = retries
		out["retryAt"] = retryAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func capabilityEvidenceFromSuccess(at, now time.Time, staleAfter time.Duration) map[string]any {
	out := map[string]any{"lastSuccessAt": at.UTC().Format(time.RFC3339Nano)}
	age := now.Sub(at)
	if age < 0 {
		age = 0
	}
	out["lagSeconds"] = int64(age.Seconds())
	if staleAfter > 0 {
		out["stale"] = age > staleAfter
	}
	return out
}

func markProviderFailure(snap map[string]any, providerReady bool) {
	if providerReady || snap["enabled"] != true {
		return
	}
	snap["provider"] = "openai"
	snap["lastError"] = "OPENAI_API_KEY is not configured"
}

func capabilitySnapshot(now time.Time) (map[string]any, []string) {
	providerReady := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
	degraded := []string{}

	scout := capabilityEvidence(capabilityScout, now, 5*time.Minute)
	scout["enabled"] = true
	scout["connected"] = false
	stt := capabilityEvidence(capabilitySTT, now, 5*time.Minute)
	stt["enabled"] = true
	stt["connected"] = false
	if kanbanApp != nil {
		kanbanApp.mu.Lock()
		scout["connected"] = kanbanApp.connected
		stt["connected"] = kanbanApp.transcriptLane != nil
		kanbanApp.mu.Unlock()
	}
	if at, ok := latestCapabilityArtifact(meetingMemoryKindTranscript); ok {
		if _, reported := stt["lastSuccessAt"]; !reported {
			mergeCapabilityEvidence(stt, capabilityEvidenceFromSuccess(at, now, 5*time.Minute))
		}
	}
	for name, snap := range map[string]map[string]any{"scout": scout, "stt": stt} {
		markProviderFailure(snap, providerReady)
		snap["status"] = capabilityStatus(snap, providerReady)
		if snap["status"] == "degraded" {
			degraded = append(degraded, name)
		}
	}

	brainCfg := readinessAgentSnapshot(meetingBrainAgent())
	brain := ambientCapabilityEvidence(capabilityBrain, meetingBrainAgent(), now)
	for k, v := range brainCfg {
		brain[k] = v
	}
	brain["workers"] = map[string]any{
		"brain":        brainCfg,
		"board":        readinessAgentSnapshot(meetingBoardAgent()),
		"missionIntel": readinessAgentSnapshot(missionIntelligenceAgent()),
	}
	markProviderFailure(brain, providerReady)
	brain["status"] = capabilityStatus(brain, providerReady)
	if brain["status"] == "degraded" {
		degraded = append(degraded, "brain")
	}
	recap := capabilityEvidence(capabilityRecap, now, 2*meetingBrainAgent().interval())
	recap["enabled"] = brainCfg["enabled"]
	recap["source"] = "brain"
	if at, ok := latestCapabilityArtifact(meetingMemoryKindBrain); ok {
		if _, reported := recap["lastSuccessAt"]; !reported {
			mergeCapabilityEvidence(recap, capabilityEvidenceFromSuccess(at, now, 2*meetingBrainAgent().interval()))
		}
	}
	markProviderFailure(recap, providerReady)
	recap["status"] = capabilityStatus(recap, providerReady)
	if recap["status"] == "degraded" {
		degraded = append(degraded, "recap")
	}

	embeddings := capabilityEvidence(capabilityEmbedding, now, 2*embeddingInterval())
	embeddings["enabled"] = embeddingInterval() > 0 && !boolEnv("EMBEDDINGS_DISABLED")
	embeddings["model"] = embeddingModel()
	markProviderFailure(embeddings, providerReady)
	embeddings["status"] = capabilityStatus(embeddings, providerReady)
	if embeddings["status"] == "degraded" {
		degraded = append(degraded, "embeddings")
	}

	workflows := capabilityEvidence(capabilityWorkflows, now, 2*workflowTickerInterval())
	workflowState := readinessWorkflowTickerSnapshot()
	for k, v := range workflowState {
		workflows[k] = v
	}
	workflowTickerStatMu.Lock()
	lastWorkflowPass := workflowTickerLastPass
	workflowTickerStatMu.Unlock()
	if !lastWorkflowPass.IsZero() {
		mergeCapabilityEvidence(workflows, capabilityEvidenceFromSuccess(lastWorkflowPass, now, 2*workflowTickerInterval()))
	}
	workflows["status"] = capabilityStatus(workflows, true)
	if workflows["status"] == "degraded" {
		degraded = append(degraded, "workflows")
	}

	backup := backupCapabilitySnapshot(now)
	if backup["status"] != "healthy" && backup["status"] != "disabled" {
		degraded = append(degraded, "backup")
	}
	sort.Strings(degraded)

	snapshot := map[string]any{
		"scout":      scout,
		"stt":        stt,
		"recap":      recap,
		"brain":      brain,
		"embeddings": embeddings,
		"workflows":  workflows,
		"backup":     backup,
	}
	redactCapabilityErrors(snapshot)
	return snapshot, degraded
}

// Capability endpoints are intentionally public for load balancers and guest
// boot diagnostics. Preserve status/timestamps/circuit truth while withholding
// provider messages and filesystem/network details from unauthenticated JSON.
func redactCapabilityErrors(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch key {
			case "lastError", "offsiteError", "restoreError":
				delete(typed, key)
			default:
				redactCapabilityErrors(child)
			}
		}
	case []any:
		for _, child := range typed {
			redactCapabilityErrors(child)
		}
	}
}

func capabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	capabilities, degraded := capabilitySnapshot(time.Now())
	writeSystemStatusJSON(w, r, http.StatusOK, map[string]any{
		"ok":           len(degraded) == 0,
		"service":      "meetingassist",
		"trafficReady": trafficReadiness().ready,
		"status":       map[bool]string{true: "healthy", false: "degraded"}[len(degraded) == 0],
		"degraded":     degraded,
		"capabilities": capabilities,
		"time":         time.Now().UTC().Format(time.RFC3339Nano),
	})
}

type readinessResult struct {
	ready           bool
	appAvailable    bool
	memoryAvailable bool
	memoryCheck     map[string]any
	boardCheck      map[string]any
}

func trafficReadiness() readinessResult {
	appAvailable := kanbanApp != nil
	memoryAvailable := appAvailable && kanbanApp.memory != nil
	memoryCheck := readinessStateFileCheck(meetingMemoryPath())
	boardCheck := readinessStateFileCheck(kanbanBoardPath())
	return readinessResult{
		ready:        appAvailable && memoryAvailable && readinessCheckOK(memoryCheck) && readinessCheckOK(boardCheck),
		appAvailable: appAvailable, memoryAvailable: memoryAvailable,
		memoryCheck: memoryCheck, boardCheck: boardCheck,
	}
}

func liveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeSystemStatusJSON(w, r, http.StatusOK, map[string]any{
		"ok": true, "service": "meetingassist", "version": serverBuildVersion,
		"time": time.Now().UTC().Format(time.RFC3339Nano),
	})
}
