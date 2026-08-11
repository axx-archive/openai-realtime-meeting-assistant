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
	LastSuccess     time.Time
	LastPoll        time.Time
	LastFailure     time.Time
	LastError       string
	LastMilestone   string
	LastMilestoneAt time.Time
	MilestoneSource string
	Backlog         *int
	DeadLetters     *int
	Circuit         string
}

func recordCapabilityPoll(name string, at time.Time) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	capabilityRuntime.Lock()
	state := capabilityRuntime.states[name]
	state.LastPoll = at
	capabilityRuntime.states[name] = state
	capabilityRuntime.Unlock()
}

const (
	capabilityScout            = "scout"
	capabilitySTT              = "stt"
	capabilityRoomVoice        = "room_voice"
	capabilityPrivateVoice     = "private_voice"
	capabilityMeetingSTT       = "meeting_stt"
	capabilityDictation        = "dictation"
	capabilityTypedScoutRouter = "typed_scout_router"
	capabilityTypedScoutAnswer = "typed_scout_answer"
	capabilityRecap            = "recap"
	capabilityBrain            = "brain"
	capabilityEmbedding        = "embeddings"
	capabilityWorkflows        = "workflows"
	capabilityAttachments      = "attachment_authority"
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

func recordCapabilityMilestone(name, milestone string, at time.Time) {
	recordCapabilityMilestoneFrom(name, milestone, "server", at)
}

func recordCapabilityMilestoneFrom(name, milestone, source string, at time.Time) {
	name = strings.TrimSpace(name)
	milestone = strings.TrimSpace(milestone)
	source = strings.TrimSpace(source)
	if name == "" || milestone == "" {
		return
	}
	capabilityRuntime.Lock()
	state := capabilityRuntime.states[name]
	state.LastMilestone = milestone
	state.LastMilestoneAt = at
	state.MilestoneSource = source
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
	if !state.LastPoll.IsZero() {
		out["lastPollAt"] = state.LastPoll.UTC().Format(time.RFC3339Nano)
		age := now.Sub(state.LastPoll)
		if age < 0 {
			age = 0
		}
		out["pollLagSeconds"] = int64(age.Seconds())
	}
	if !state.LastFailure.IsZero() {
		out["lastFailureAt"] = state.LastFailure.UTC().Format(time.RFC3339Nano)
	}
	if state.LastError != "" {
		out["lastError"] = state.LastError
	}
	if state.LastMilestone != "" {
		out["lastMilestone"] = state.LastMilestone
		out["lastMilestoneAt"] = state.LastMilestoneAt.UTC().Format(time.RFC3339Nano)
		out["lastMilestoneSource"] = state.MilestoneSource
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
	circuit, _ := base["circuit"].(string)
	if !providerReady || base["lastError"] != nil || base["stale"] == true || (circuit != "" && circuit != "closed") {
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

// latestAmbientCapabilityArtifact returns durable success evidence for an
// ambient worker. Most workers own an append-only memory kind. Specialty
// workers that update a shared os_artifact instead provide a typed success
// contract, so an unrelated os_artifact cannot manufacture health.
func latestAmbientCapabilityArtifactForApp(app *kanbanBoardApp, agent ambientAgentConfig) (time.Time, bool) {
	if agent.healthSuccessAt == nil {
		if app == nil || app.memory == nil {
			return time.Time{}, false
		}
		entries := app.memory.entriesOfKind(agent.artifactKind, 1)
		if len(entries) == 0 {
			return time.Time{}, false
		}
		return entries[len(entries)-1].CreatedAt, true
	}
	if app == nil || app.memory == nil {
		return time.Time{}, false
	}

	var latest time.Time
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		at, ok := agent.healthSuccessAt(entry)
		if ok && at.After(latest) {
			latest = at
		}
	}
	return latest, !latest.IsZero()
}

func latestAmbientCapabilityArtifact(agent ambientAgentConfig) (time.Time, bool) {
	return latestAmbientCapabilityArtifactForApp(kanbanApp, agent)
}

// recordSpecialtyCapabilityCompletion separates worker liveness from useful
// work. A specialty pass can legitimately no-op when its due source disappears
// after the cadence check. Only a pass that reports a durable write and whose
// typed artifact can be read back may clear its prior failure and advance the
// runtime completion marker.
func (app *kanbanBoardApp) recordSpecialtyCapabilityCompletion(agent ambientAgentConfig, at time.Time, persisted bool) {
	recordCapabilityPoll(agent.name, at)
	if !persisted || agent.healthSuccessAt == nil {
		return
	}
	if _, ok := latestAmbientCapabilityArtifactForApp(app, agent); ok {
		if err := app.clearAmbientAgentFailure(agent.name); err == nil {
			recordCapabilitySuccess(agent.name, at)
		}
	}
}

// ambientCapabilityEvidence surfaces only state the ambient runner actually
// owns: persisted output is success evidence; live retry records establish an
// open circuit. It never fabricates a success timestamp from process startup.
func ambientCapabilityEvidence(name string, agent ambientAgentConfig, now time.Time) map[string]any {
	out := capabilityEvidence(name, now, 2*agent.interval())
	artifactAt, artifactOK := latestAmbientCapabilityArtifact(agent)
	if agent.healthSuccessAt != nil {
		// Runtime completion is liveness only for specialty workers. Their
		// matching typed artifact revision owns lastSuccessAt authoritatively.
		delete(out, "lastSuccessAt")
		delete(out, "lagSeconds")
		delete(out, "stale")
	}
	if artifactOK {
		at := artifactAt
		staleAfter := 2 * agent.interval()
		if agent.healthWorkDue != nil {
			// Specialty artifact freshness follows its product cadence gate, not
			// its cheap polling interval. An old living profile/style can still be
			// current when there is no signal/binder/monthly work waiting.
			staleAfter = 0
			out["lastArtifactAt"] = at.UTC().Format(time.RFC3339Nano)
			age := now.Sub(at)
			if age < 0 {
				age = 0
			}
			out["artifactLagSeconds"] = int64(age.Seconds())
		}
		persisted := capabilityEvidenceFromSuccess(at, now, staleAfter)
		mergeCapabilityEvidence(out, persisted)
	}
	if agent.healthSuccessAt != nil {
		out["typedArtifactPresent"] = artifactOK
	}
	if agent.healthWorkDue != nil {
		due := kanbanApp != nil && agent.healthWorkDue(kanbanApp, now)
		out["workDue"] = due
		if due {
			out["artifactFreshness"] = "work_due"
			out["stale"] = true
		} else {
			out["artifactFreshness"] = "not_due"
			// Never inherit interval-based staleness for an artifact whose actual
			// work gate says it is current. Provider/circuit/worker errors remain.
			delete(out, "stale")
		}
		state := capabilityState(name)
		if !state.LastPoll.IsZero() {
			out["lastPassAt"] = state.LastPoll.UTC().Format(time.RFC3339Nano)
			age := now.Sub(state.LastPoll)
			if age < 0 {
				age = 0
			}
			out["passLagSeconds"] = int64(age.Seconds())
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
	providerOpen := false
	persistenceOpen := false
	continuityOpen := false
	for key, failure := range kanbanApp.agentFailures {
		if failure == nil || (key != agent.name && !strings.HasPrefix(key, agent.name+"@")) {
			continue
		}
		retries += failure.attempts
		providerOpen = providerOpen || failure.providerOpen
		persistenceOpen = persistenceOpen || failure.persistenceOpen
		continuityOpen = continuityOpen || failure.continuityOpen
		if failure.backoffUntil.After(retryAt) {
			retryAt = failure.backoffUntil
		}
	}
	kanbanApp.mu.Unlock()
	if retries > 0 || persistenceOpen || continuityOpen {
		out["circuit"] = "open"
		out["retryAttempts"] = retries
		if continuityOpen {
			out["circuit"] = "continuity_error"
			out["continuityError"] = true
			out["retrySuppressed"] = true
		} else if persistenceOpen {
			out["circuit"] = "persistence_error"
			out["persistenceError"] = true
			out["retrySuppressed"] = true
		} else if providerOpen {
			out["retrySuppressed"] = true
		} else if !retryAt.IsZero() {
			out["retryAt"] = retryAt.UTC().Format(time.RFC3339Nano)
		}
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

func markNamedProviderFailure(snap map[string]any, provider string, providerReady bool) {
	if providerReady || snap["enabled"] != true {
		return
	}
	snap["provider"] = provider
	snap["lastError"] = strings.ToUpper(provider) + " API key is not configured"
}

func ambientWorkerCapabilitySnapshot(agent ambientAgentConfig, now time.Time, provider string, providerReady bool) map[string]any {
	snap := ambientCapabilityEvidence(agent.name, agent, now)
	for key, value := range readinessAgentSnapshot(agent) {
		snap[key] = value
	}
	backfillArmed := boolEnv(agent.backfillEnv)
	snap["backfillArmed"] = backfillArmed
	registered, running, pendingRooms := ambientWorkerSupervisorState(kanbanApp, agent.name)
	snap["supervisorRegistered"] = registered
	snap["supervisorRunning"] = running
	snap["pendingRoomCount"] = pendingRooms
	if diagnostics := ambientWorkerCheckpointDiagnostics(kanbanApp, agent); len(diagnostics) > 0 {
		for key, value := range diagnostics {
			snap[key] = value
		}
	}
	snap["provider"] = provider
	markNamedProviderFailure(snap, provider, providerReady)
	// Cadence-gated specialty workers can also run synchronously on demand and
	// prove current health from their typed artifact + workDue contract. Generic
	// meeting-intelligence workers always require a live supervisor.
	requiresSupervisor := agent.healthWorkDue == nil
	if snap["enabled"] == true && providerReady && !running && requiresSupervisor {
		snap["supervisorError"] = true
		if strings.TrimSpace(asString(snap["lastError"])) == "" {
			snap["lastError"] = "worker supervisor is not running"
		}
	}
	if snap["enabled"] == false && backfillArmed {
		snap["unsafeActivation"] = true
		snap["activationWarning"] = "worker is disabled while full-history backfill is armed"
	}
	if snap["ambientContinuityHealthy"] == false {
		snap["continuityError"] = true
		if strings.TrimSpace(asString(snap["lastError"])) == "" {
			snap["lastError"] = "ambient continuity checkpoint requires repair"
		}
	}
	snap["status"] = capabilityStatus(snap, providerReady)
	if snap["unsafeActivation"] == true {
		snap["status"] = "degraded"
	}
	snap["analysisReady"] = snap["status"] == "healthy" && running && snap["ambientContinuityHealthy"] != false
	return snap
}

// ambientWorkerSupervisorState reports process liveness without manufacturing
// success. Registration proves a loop was installed this boot; the done channel
// proves whether that exact supervisor is still running. Pending rooms are only
// nudges waiting to be drained, not a fabricated full backlog count.
func ambientWorkerSupervisorState(app *kanbanBoardApp, name string) (registered, running bool, pendingRooms int) {
	if app == nil {
		return false, false, 0
	}
	app.mu.Lock()
	_, registered = app.agentCancels[name]
	done := app.agentDones[name]
	pendingRooms = len(app.agentPendingRooms[name])
	app.mu.Unlock()
	if !registered || done == nil {
		return registered, false, pendingRooms
	}
	select {
	case <-done:
		return true, false, pendingRooms
	default:
		return true, true, pendingRooms
	}
}

// ambientWorkerCheckpointDiagnostics makes restart continuity inspectable while
// keeping the public health surface free of raw message ids. Legacy artifact
// baselines are considered valid only when they normalize to a proven input
// cursor; a held window must always resolve to a later input in the same scope.
func ambientWorkerCheckpointDiagnostics(app *kanbanBoardApp, agent ambientAgentConfig) map[string]any {
	if app == nil || app.memory == nil {
		return nil
	}
	ambientHeldWindowStateMu.Lock()
	state, err := loadAmbientHeldWindowState(app.ambientHeldWindowPath())
	ambientHeldWindowStateMu.Unlock()
	if err != nil {
		return map[string]any{"checkpointStatus": "unreadable", "checkpointError": true}
	}
	prefix := agent.name + "@"
	checkpoints, held, blocked, invalid := 0, 0, 0, 0
	continuityScopes := make([]map[string]any, 0)
	for key, checkpoint := range state.Windows {
		if key != agent.name && !strings.HasPrefix(key, prefix) {
			continue
		}
		checkpoints++
		expectedRoom := officeRoomID
		if key != agent.name {
			expectedRoom = strings.TrimPrefix(key, prefix)
		}
		expectedRoom = agent.scopeRoomID(expectedRoom)
		contractInvalid := key != ambientAgentScopeKey(agent, expectedRoom) || strings.TrimSpace(checkpoint.Agent) != agent.name ||
			(strings.TrimSpace(checkpoint.RoomID) != "" && normalizeRoomID(checkpoint.RoomID) != expectedRoom) ||
			(strings.TrimSpace(checkpoint.InputKind) != "" && checkpoint.InputKind != agent.inputKind) ||
			(strings.TrimSpace(checkpoint.ArtifactKind) != "" && checkpoint.ArtifactKind != agent.artifactKind) ||
			(strings.TrimSpace(checkpoint.CursorMetadataKey) != "" && checkpoint.CursorMetadataKey != agent.cursorMetadataKey)
		reason := strings.TrimSpace(checkpoint.BlockedReason)
		if reason != "" {
			blocked++
		}
		_, baselineOK, windowOK := app.memory.normalizeAmbientCheckpointReferences(agent, expectedRoom, checkpoint.BaselineID, checkpoint.WindowID)
		if strings.TrimSpace(checkpoint.WindowID) != "" {
			held++
		}
		scopeInvalid := contractInvalid || !baselineOK || !windowOK
		if scopeInvalid {
			invalid++
		}
		if scopeInvalid || reason != "" {
			if reason == "" {
				reason = ambientContinuityCheckpointInvalid
			}
			continuityScopes = append(continuityScopes, map[string]any{
				"roomId":        expectedRoom,
				"blockedReason": reason,
				"inputKind":     agent.inputKind,
				"artifactKind":  agent.artifactKind,
				"heldWindow":    strings.TrimSpace(checkpoint.WindowID) != "",
			})
		}
	}
	status := "missing"
	if checkpoints > 0 {
		status = "ready"
	}
	if held > 0 {
		status = "held"
	}
	if blocked > 0 {
		status = "blocked"
	}
	if invalid > 0 {
		status = "invalid"
	}
	out := map[string]any{
		"checkpointStatus":         status,
		"checkpointScopes":         checkpoints,
		"heldScopeCount":           held,
		"blockedScopeCount":        blocked,
		"invalidScopeCount":        invalid,
		"ambientContinuityHealthy": invalid == 0 && blocked == 0,
	}
	if len(continuityScopes) > 0 {
		out["continuityScopes"] = continuityScopes
	}
	return out
}

func ambientWorkersCapabilitySnapshot(now time.Time, openAIReady bool) map[string]any {
	workers := map[string]any{}
	add := func(key string, agent ambientAgentConfig, provider string, ready bool) {
		workers[key] = ambientWorkerCapabilitySnapshot(agent, now, provider, ready)
	}
	add("brain", meetingBrainAgent(), providerOpenAI, openAIReady)
	add("board", meetingBoardAgent(), providerOpenAI, openAIReady)
	add("missionIntel", missionIntelligenceAgent(), providerOpenAI, openAIReady)
	add("decisionLedger", decisionLedgerAgent(), providerOpenAI, openAIReady)
	add("narrative", narrativeMaintainerAgent(), providerOpenAI, openAIReady)
	add("meetingDigest", meetingDigestAgent(), providerOpenAI, openAIReady)
	add("dayDigest", dayDigestAgent(), providerOpenAI, openAIReady)
	add("entityLedger", entityLedgerAgent(), providerOpenAI, openAIReady)
	add("companyDigest", companyDigestAgent(), providerOpenAI, openAIReady)
	add("researchSuggestion", researchSuggestionAgent(), providerOpenAI, openAIReady)
	add("slopClassifier", slopClassifierAgent(), providerOpenAI, openAIReady)
	add("tasteAnalyst", tasteAnalystAgent(), providerOpenAI, openAIReady)
	add("houseStyle", houseStyleDistillerAgent(), providerOpenAI, openAIReady)
	return workers
}

func roomScoutCapabilityRows(app *kanbanBoardApp) ([]map[string]any, bool) {
	if app == nil {
		return nil, false
	}
	app.mu.Lock()
	bundles := make([]*roomRealtimeBundle, 0, len(app.roomLive))
	for _, room := range app.roomLive {
		if room != nil && room.realtime != nil {
			bundles = append(bundles, room.realtime)
		}
	}
	app.mu.Unlock()
	rows := make([]map[string]any, 0, len(bundles))
	anyOpen := false
	for _, bundle := range bundles {
		runtime := bundle.snapshot()
		bundle.mu.Lock()
		transport := bundle.transport
		bundle.mu.Unlock()
		row := map[string]any{
			"roomId":          runtime.Scope.RoomID,
			"sittingId":       runtime.Scope.SittingID,
			"mediaGeneration": runtime.Scope.MediaGeneration,
			"status":          runtime.Status,
			"circuit":         "closed",
		}
		if runtime.LastError != "" {
			row["lastError"] = runtime.LastError
		}
		if providerTransport, ok := transport.(*openAIRoomScoutTransport); ok {
			provider := providerTransport.providerCircuitSnapshot()
			row["retryAttempts"] = provider.Failures
			if provider.Open {
				row["circuit"] = "open"
				row["retrySuppressed"] = true
				anyOpen = true
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return asString(rows[i]["roomId"]) < asString(rows[j]["roomId"]) })
	return rows, anyOpen
}

func capabilitySnapshot(now time.Time) (map[string]any, []string) {
	providerReady := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
	degraded := []string{}

	roomVoice := capabilityEvidence(capabilityRoomVoice, now, 5*time.Minute)
	roomVoice["enabled"] = true
	roomVoice["connected"] = false
	roomVoice["provider"] = providerOpenAI
	roomVoice["model"] = realtimeModel()
	privateVoice := capabilityEvidence(capabilityPrivateVoice, now, 5*time.Minute)
	privateVoice["enabled"] = true
	privateVoice["provider"] = providerOpenAI
	privateVoice["model"] = realtimeModel()
	meetingSTT := capabilityEvidence(capabilityMeetingSTT, now, 5*time.Minute)
	meetingSTT["enabled"] = true
	meetingSTT["connected"] = false
	meetingSTT["provider"] = providerOpenAI
	meetingSTT["model"] = transcriptionLaneModel()
	dictation := capabilityEvidence(capabilityDictation, now, 5*time.Minute)
	dictation["enabled"] = true
	dictation["provider"] = providerOpenAI
	dictation["model"] = dictationTranscriptionModel()
	typedRouter := capabilityEvidence(capabilityTypedScoutRouter, now, 5*time.Minute)
	typedRouter["enabled"] = true
	typedRouter["provider"] = providerOpenAI
	typedRouter["model"] = scoutRouterModel()
	typedAnswer := capabilityEvidence(capabilityTypedScoutAnswer, now, 5*time.Minute)
	typedAnswer["enabled"] = true
	typedAnswer["provider"] = providerOpenAI
	typedAnswer["model"] = scoutChatModel()
	if kanbanApp != nil {
		kanbanApp.mu.Lock()
		roomVoice["connected"] = kanbanApp.connected
		kanbanApp.mu.Unlock()
		meetingSTT["connected"] = kanbanApp.transcriptionLaneConnected()
	}
	if roomRows, roomCircuitOpen := roomScoutCapabilityRows(kanbanApp); len(roomRows) > 0 {
		roomVoice["rooms"] = roomRows
		if roomCircuitOpen {
			roomVoice["circuit"] = "open"
			roomVoice["retrySuppressed"] = true
		}
	}
	if at, ok := latestCapabilityArtifact(meetingMemoryKindTranscript); ok {
		if _, reported := meetingSTT["lastSuccessAt"]; !reported {
			mergeCapabilityEvidence(meetingSTT, capabilityEvidenceFromSuccess(at, now, 5*time.Minute))
		}
	}
	for name, snap := range map[string]map[string]any{
		"roomVoice": roomVoice, "privateVoice": privateVoice,
		"meetingSTT": meetingSTT, "dictation": dictation,
		"typedScoutRouter": typedRouter, "typedScoutAnswer": typedAnswer,
	} {
		markProviderFailure(snap, providerReady)
		snap["status"] = capabilityStatus(snap, providerReady)
		if snap["status"] == "degraded" {
			degraded = append(degraded, name)
		}
	}
	// Backward-compatible aggregates remain while clients adopt the split
	// contract. They are healthy only when every required child is healthy.
	scout := map[string]any{
		"enabled": true, "connected": roomVoice["connected"], "status": "healthy",
		"lanes": map[string]any{"roomVoice": roomVoice, "privateVoice": privateVoice, "typedRouter": typedRouter, "typedAnswer": typedAnswer},
	}
	if roomVoice["status"] != "healthy" || privateVoice["status"] != "healthy" || typedRouter["status"] != "healthy" || typedAnswer["status"] != "healthy" {
		scout["status"] = "degraded"
		degraded = append(degraded, capabilityScout)
	}
	if circuit, ok := roomVoice["circuit"]; ok {
		scout["circuit"] = circuit
	}
	if rooms, ok := roomVoice["rooms"]; ok {
		scout["rooms"] = rooms
	}
	if retrySuppressed, ok := roomVoice["retrySuppressed"]; ok {
		scout["retrySuppressed"] = retrySuppressed
	}
	stt := map[string]any{
		"enabled": true, "connected": meetingSTT["connected"], "status": "healthy",
		"lanes": map[string]any{"meeting": meetingSTT, "dictation": dictation},
	}
	if meetingSTT["status"] != "healthy" || dictation["status"] != "healthy" {
		stt["status"] = "degraded"
		degraded = append(degraded, capabilitySTT)
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
	ambientWorkers := ambientWorkersCapabilitySnapshot(now, providerReady)
	for key, raw := range ambientWorkers {
		worker, _ := raw.(map[string]any)
		if worker["status"] == "degraded" {
			degraded = append(degraded, "ambient."+key)
		}
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
	mergeCapabilityEvidence(embeddings, embeddingProviderCircuitEvidence())
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

	// Attachment authority is intentionally a capability health signal, not a
	// traffic/readiness prerequisite. A failed source ledger suppresses binary
	// projections and model reads while ordinary text chat and live media remain
	// usable. Do not infer health from a successful attachment upload: only the
	// durable source authority store is the guard being reported here.
	attachments := map[string]any{"enabled": true}
	if kanbanApp == nil {
		attachments["status"] = "degraded"
		attachments["lastError"] = "application unavailable"
	} else {
		kanbanApp.pendingAttachmentUploadsMu.Lock()
		storeErr := kanbanApp.attachmentSourceStoreErr
		kanbanApp.pendingAttachmentUploadsMu.Unlock()
		if storeErr != nil {
			attachments["status"] = "degraded"
			attachments["lastError"] = storeErr.Error()
		} else {
			attachments["status"] = "healthy"
		}
	}
	if attachments["status"] == "degraded" {
		degraded = append(degraded, capabilityAttachments)
	}

	backup := backupCapabilitySnapshot(now)
	if backup["status"] != "healthy" && backup["status"] != "disabled" {
		degraded = append(degraded, "backup")
	}
	roomRows, roomDegraded := roomOperationalCapabilityRows(kanbanApp, now, providerReady, roomConsentHealth())
	degraded = append(degraded, roomDegraded...)
	strideRuntime := strideRuntimeCapabilitySnapshot(kanbanApp)
	if strideRuntime["status"] == "degraded" {
		degraded = append(degraded, "stride_runtime_unavailable")
	}
	replayStatus := ambientReplayRuntimeSnapshot()
	replay := map[string]any{"mode": replayStatus.Mode, "enabled": replayStatus.Enabled, "database": replayStatus.Database,
		"plannerConfigured": replayStatus.PlannerConfigured, "executorConfigured": replayStatus.ExecutorConfigured,
		"boardExcluded": replayStatus.BoardExcluded, "maxSources": replayStatus.MaxSources, "status": "disabled"}
	if replayStatus.Enabled {
		replay["status"] = map[bool]string{true: "healthy", false: "degraded"}[replayStatus.Ready]
		if !replayStatus.Ready {
			degraded = append(degraded, "ambientReplay")
		}
	}
	sort.Strings(degraded)

	snapshot := map[string]any{
		"scout":               scout,
		"stt":                 stt,
		"roomVoice":           roomVoice,
		"privateVoice":        privateVoice,
		"meetingSTT":          meetingSTT,
		"dictation":           dictation,
		"typedScoutRouter":    typedRouter,
		"typedScoutAnswer":    typedAnswer,
		"recap":               recap,
		"brain":               brain,
		"ambientWorkers":      ambientWorkers,
		"embeddings":          embeddings,
		"workflows":           workflows,
		"attachmentAuthority": attachments,
		"backup":              backup,
		"rooms":               roomRows,
		"strideRuntime":       strideRuntime,
		"ambientReplay":       replay,
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
	case []map[string]any:
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
