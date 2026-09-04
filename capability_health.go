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
		// lastRequestAt is the honest-state name for the same evidence: the
		// last time anything asked this lane to do work, success or not.
		out["lastRequestAt"] = out["lastPollAt"]
		age := now.Sub(state.LastPoll)
		if age < 0 {
			age = 0
		}
		out["pollLagSeconds"] = int64(age.Seconds())
		if staleAfter > 0 {
			out["requestInWindow"] = age <= staleAfter
		}
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

// Capability lane states (Wave 9 D3, honest states):
//
//	disabled          the lane is configured off
//	degraded          the last request failed, the provider key is missing, a
//	                  retry/persistence circuit is open, an allocated session is
//	                  disconnected, or due work is overdue
//	paused_by_breaker an ambient worker is skipping passes while its seat's
//	                  provider breaker is open
//	fallback_active   the seat's breaker is steering to the fallback dial, or
//	                  the last completed request was served by it
//	idle              no failure is pending and no success landed inside the
//	                  evidence window (nothing has asked the lane for work
//	                  recently, or ever) — NOT a degradation
//	healthy           the last request inside the window succeeded
const (
	capabilityStatusDisabled        = "disabled"
	capabilityStatusDegraded        = "degraded"
	capabilityStatusPausedByBreaker = "paused_by_breaker"
	capabilityStatusFallbackActive  = "fallback_active"
	capabilityStatusIdle            = "idle"
	capabilityStatusHealthy         = "healthy"
)

func capabilityStatus(base map[string]any, providerReady bool) string {
	if enabled, ok := base["enabled"].(bool); ok && !enabled {
		return capabilityStatusDisabled
	}
	if !providerReady {
		return capabilityStatusDegraded
	}
	circuit, _ := base["circuit"].(string)
	// Durable-state faults always win: a corrupt/blocked checkpoint or a
	// persistence circuit needs an operator regardless of provider weather.
	if circuit == "persistence_error" || circuit == "continuity_error" || base["persistenceError"] == true || base["continuityError"] == true || base["checkpointError"] == true {
		return capabilityStatusDegraded
	}
	// A worker paused behind its seat's provider breaker is deliberately not
	// calling the provider. The failures that opened the breaker are still
	// visible (lastError, retryAttempts, breaker.reason) but the state names
	// what is actually happening now.
	if base["pausedByBreaker"] == true {
		return capabilityStatusPausedByBreaker
	}
	if base["lastError"] != nil || (circuit != "" && circuit != "closed") {
		return capabilityStatusDegraded
	}
	// Due work whose artifact is still stale is a real degradation; ordinary
	// interval staleness with nothing due is idleness (see below).
	if base["overdue"] == true {
		return capabilityStatusDegraded
	}
	if connected, reported := base["connected"].(bool); reported && !connected {
		// A session/lane that exists but is not connected is a fault. A lane
		// with nothing allocated (no live meeting, no realtime session) is
		// simply idle — the 2026-09-01 production symptom was every such lane
		// reporting degraded with zero provider errors in the logs.
		if base["allocated"] == true {
			return capabilityStatusDegraded
		}
		return capabilityStatusIdle
	}
	if base["fallbackActive"] == true {
		return capabilityStatusFallbackActive
	}
	// Configuration is not success evidence. Until a producer or persisted
	// artifact proves that the capability has completed useful work inside the
	// evidence window, report it as idle rather than manufacturing a healthy
	// state from a present key — and never as degraded when nothing failed.
	if _, evidenced := base["lastSuccessAt"]; !evidenced || base["stale"] == true {
		if base["stale"] == true && capabilityLaneLive(base) {
			// Something is live and should be producing (an allocated meeting
			// STT lane, an active voice session) but nothing landed inside the
			// evidence window: that is a stall, not idleness.
			return capabilityStatusDegraded
		}
		return capabilityStatusIdle
	}
	return capabilityStatusHealthy
}

// capabilityLaneLive reports whether a lane has a live producer attached:
// `allocated` decides when the lane reports it; a lane that reports only
// `connected` counts as live while connected. A lane with neither key (typed
// seats, ambient workers) is never "live" in this sense — its staleness is
// ordinary idleness.
func capabilityLaneLive(base map[string]any) bool {
	if allocated, reported := base["allocated"].(bool); reported {
		return allocated
	}
	connected, _ := base["connected"].(bool)
	return connected
}

// capabilityStatusCountsDegraded reports whether a lane status counts toward the
// degraded rollup (/capabilities ok=false, the `degraded` list). A worker
// parked behind its seat's provider breaker keeps its specific
// paused_by_breaker label on the lane, but it is still not doing its job, so
// the rollup must not say ok while it is parked.
func capabilityStatusCountsDegraded(status any) bool {
	switch asString(status) {
	case capabilityStatusDegraded, capabilityStatusPausedByBreaker:
		return true
	}
	return false
}

// aggregateCapabilityStatus folds child lane statuses into one honest parent
// status: any degraded child degrades the parent; otherwise breaker/fallback
// states surface; otherwise the parent is idle only when every child is idle.
func aggregateCapabilityStatus(lanes ...map[string]any) string {
	idle := 0
	fallback := false
	for _, lane := range lanes {
		switch asString(lane["status"]) {
		case capabilityStatusDegraded:
			return capabilityStatusDegraded
		case capabilityStatusPausedByBreaker, capabilityStatusFallbackActive:
			fallback = true
		case capabilityStatusIdle, capabilityStatusDisabled:
			idle++
		}
	}
	if fallback {
		return capabilityStatusFallbackActive
	}
	if len(lanes) > 0 && idle == len(lanes) {
		return capabilityStatusIdle
	}
	return capabilityStatusHealthy
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

	app.memory.mu.Lock()
	defer app.memory.mu.Unlock()
	var latest time.Time
	for _, entry := range app.memory.entries {
		if entry.Kind != meetingMemoryKindOSArtifact || memoryEntryIsMediaSoakCanary(entry) {
			continue
		}
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
			out["overdue"] = true
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
		deadLetters = kanbanApp.memory.countEntriesOfKindByMetadata(meetingMemoryKindDeadLetter, deadLetterAgentMetadataKey, agent.name)
		out["deadLetter"] = deadLetters
		// stuckInputs (ambient_truncation.go): inputs the worker skipped because
		// max_output_truncation survived the halved-window retry. A subset of
		// deadLetter, surfaced separately so a truncation skip reads as "moved
		// on" rather than as a poison window or an open circuit.
		out["stuckInputs"] = kanbanApp.memory.countStuckInputs(agent.name)
		if agent.name == channelDigestAgentName {
			// first-run history still waiting for the oldest-first catch-up
			// (channel_digest.go withChannelDigestRebuilds)
			out["seedPendingRows"] = kanbanApp.channelDigestSeedPendingRows()
		}
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
	// `enabled` is the legacy worker's boot configuration. Product ownership can
	// fence that configured worker before a supervisor is registered, so expose
	// the effective state separately instead of reporting a missing supervisor as
	// an outage for a lane STRIDE intentionally replaced.
	snap["configuredEnabled"] = snap["enabled"]
	snap["effectiveEnabled"] = snap["enabled"]
	supersededBy, superseded := ambientWorkerSupersession(kanbanApp, agent)
	if superseded {
		snap["effectiveEnabled"] = false
		snap["ownershipState"] = "superseded"
		snap["supersededBy"] = supersededBy
		snap["fenced"] = true
		snap["supervisorRequired"] = false
	} else {
		snap["ownershipState"] = "active"
		snap["fenced"] = false
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
	if !superseded {
		markNamedProviderFailure(snap, provider, providerReady)
	}
	if seat := ambientAgentProviderSeat(agent); seat != "" {
		breaker := providerBreakerEvidence(snap, provider, seat, seatFallbackModel(seat, meetingBrainModel()) != "")
		if breaker.State == providerBreakerOpen {
			snap["pausedByBreaker"] = true
			snap["retryAt"] = breaker.RetryAt.UTC().Format(time.RFC3339Nano)
		}
	}
	// Cadence-gated specialty workers can also run synchronously on demand and
	// prove current health from their typed artifact + workDue contract. Generic
	// meeting-intelligence workers always require a live supervisor.
	requiresSupervisor := agent.healthWorkDue == nil && !superseded
	if _, reported := snap["supervisorRequired"]; !reported {
		snap["supervisorRequired"] = requiresSupervisor
	}
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
	if superseded {
		// A superseded worker is neither healthy nor disabled: it is deliberately
		// dormant behind a named owner. Ignore stale/provider/supervisor evidence
		// from the legacy execution lane, but never hide a corrupt or blocked
		// durable checkpoint that still requires operator reconciliation.
		snap["status"] = "superseded"
	}
	if snap["unsafeActivation"] == true || snap["ambientContinuityHealthy"] == false || snap["checkpointError"] == true || snap["persistenceError"] == true {
		snap["status"] = "degraded"
	}
	snap["analysisReady"] = snap["status"] == "healthy" && running && snap["ambientContinuityHealthy"] != false
	return snap
}

// ambientWorkerSupersession names an authority replacement that fences a
// legacy worker before boot. Keep this narrow and product-owned: an absent
// supervisor for any ordinary enabled worker must remain a degradation.
func ambientWorkerSupersession(app *kanbanBoardApp, agent ambientAgentConfig) (string, bool) {
	if agent.name == researchSuggestionAgentName && app != nil && app.strideRuntime != nil && app.strideRuntime.productPreviewOwnsWorkSuggestions() {
		return "stride_suggested_work", true
	}
	return "", false
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
	checkpoints, held, blocked, invalid, firstRun, anchorable := 0, 0, 0, 0, 0, 0
	continuityScopes := make([]map[string]any, 0)
	sparseScopes := 0
	sparseCounts := map[string]int{}
	for key, checkpoint := range state.Windows {
		if key != agent.name && !strings.HasPrefix(key, prefix) {
			continue
		}
		checkpoints++
		if checkpoint.FirstRunAnchor {
			firstRun++
		}
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
		if agent.name == meetingDigestAgentName && !contractInvalid && checkpoint.WindowID == "" && checkpoint.BaselineID == "" {
			recovery, active, loadErr := loadMeetingDigestSparseState(app.meetingDigestSparsePath(expectedRoom), expectedRoom)
			if loadErr != nil {
				invalid++
				continuityScopes = append(continuityScopes, map[string]any{"blockedReason": "digest_coverage_checkpoint_invalid"})
				continue
			}
			if active {
				sparseScopes++
				for _, ref := range recovery.Sources {
					sparseCounts[ref.Status]++
				}
				continue
			}
		}
		reason := strings.TrimSpace(checkpoint.BlockedReason)
		// A blocked scope is not necessarily stuck: one this worker has never
		// produced for, whose block only means "I cannot resolve where to
		// start", anchors itself on the next pass. Gen 249's whole failure was
		// that the two states looked identical from outside, so they are
		// distinguished here, per scope and in the counts.
		scopeAnchorable := false
		if reason != "" {
			blocked++
			if _, ok := app.ambientFirstRunAnchorSupersedesCheckpoint(agent, expectedRoom, checkpoint); ok {
				scopeAnchorable = true
				anchorable++
			}
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
				"anchorable":    scopeAnchorable,
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
		if anchorable == blocked {
			// Blocked, but every blocked scope supersedes itself on the next
			// pass — an operator reading this needs no intervention, only the
			// next tick.
			status = "blocked_anchorable"
		}
	}
	if invalid > 0 {
		status = "invalid"
	}
	out := map[string]any{
		"checkpointStatus":         status,
		"checkpointScopes":         checkpoints,
		"heldScopeCount":           held,
		"blockedScopeCount":        blocked,
		"blockedAnchorableScopes":  anchorable,
		"invalidScopeCount":        invalid,
		"ambientContinuityHealthy": invalid == 0 && blocked == 0,
	}
	if sparseScopes > 0 {
		out["sparseCoverageScopes"] = sparseScopes
		out["sparseCoverageCounts"] = sparseCounts
		out["coverageStatus"] = "partial_source_coverage"
		if sparseCounts["needs_attention"] > 0 {
			out["coverageStatus"] = "source_needs_attention"
		}
	}
	if firstRun > 0 || agent.firstRunAnchor {
		// Scopes the worker anchored instead of failing closed
		// (ambientAgentConfig.firstRunAnchor): healthy, but never silent. Gen
		// 249 could only be diagnosed because this key was ABSENT, so every
		// opted-in worker now publishes it even at zero — absent means "does
		// not opt in", 0 means "opted in and has not anchored yet".
		out["firstRunAnchorScopes"] = firstRun
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
	add("missionIntel", missionIntelligenceAgent(), providerOpenAI, openAIReady)
	add("decisionLedger", decisionLedgerAgent(), providerOpenAI, openAIReady)
	add("narrative", narrativeMaintainerAgent(), providerOpenAI, openAIReady)
	add("meetingDigest", meetingDigestAgent(), providerOpenAI, openAIReady)
	add("dayDigest", dayDigestAgent(), providerOpenAI, openAIReady)
	add("channelDigest", channelDigestAgent(), providerOpenAI, openAIReady)
	add("entityLedger", entityLedgerAgent(), providerOpenAI, openAIReady)
	add("companyDigest", companyDigestAgent(), providerOpenAI, openAIReady)
	add("researchSuggestion", researchSuggestionAgent(), providerOpenAI, openAIReady)
	add("slopClassifier", slopClassifierAgent(), providerOpenAI, openAIReady)
	add("tasteAnalyst", tasteAnalystAgent(), providerOpenAI, openAIReady)
	add("houseStyle", houseStyleDistillerAgent(), providerOpenAI, openAIReady)
	add("scoutFollowup", scoutFollowupAgent(), providerOpenAI, openAIReady)
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
	// In-room Scout speech is an optional qualified layer: JoinConferenceRoom
	// only installs app.roomScoutFactory when currentRoomScoutVoiceAvailability
	// says the receipt is current (kanban.go), so with the gate closed no room
	// realtime session can ever exist and the lane's own success writer
	// (handleRealtimeEventForGeneration) is unreachable. That is configured
	// off, not unasked — say so.
	roomVoiceGate := currentRoomScoutVoiceAvailability()
	roomVoice["enabled"] = roomVoiceGate.Enabled
	if !roomVoiceGate.Enabled && strings.TrimSpace(roomVoiceGate.Reason) != "" {
		roomVoice["reason"] = roomVoiceGate.Reason
	}
	roomVoice["connected"] = false
	roomVoice["provider"] = providerOpenAI
	roomVoice["model"] = realtimeModel()
	privateVoice := capabilityEvidence(capabilityPrivateVoice, now, 5*time.Minute)
	// The private realtime voice lane sits behind a server-owned release gate
	// (PRIVATE_REALTIME_VOICE_QUALIFIED). While that gate is closed every offer
	// 503s at assistantRealtimeOfferHandler before the provider is touched, so
	// the lane is configured off — not merely unasked. Reporting "idle" there
	// would tell an operator nothing has wanted the lane recently when in fact
	// nothing is permitted to use it; capabilityStatus turns enabled=false into
	// the honest word "disabled".
	privateVoice["enabled"] = privateRealtimeVoiceQualified()
	if privateVoice["enabled"] != true {
		privateVoice["reason"] = "awaiting_qualification"
	}
	privateVoice["provider"] = providerOpenAI
	privateVoice["model"] = realtimeModel()
	meetingSTT := capabilityEvidence(capabilityMeetingSTT, now, 5*time.Minute)
	meetingSTT["enabled"] = true
	meetingSTT["connected"] = false
	meetingSTT["allocated"] = false
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
	typedRouter["fallbackModel"] = seatFallbackModel(seatRouter, scoutRouterModel())
	providerBreakerEvidence(typedRouter, providerOpenAI, seatRouter, seatFallbackModel(seatRouter, scoutRouterModel()) != "")
	typedAnswer := capabilityEvidence(capabilityTypedScoutAnswer, now, 5*time.Minute)
	typedAnswer["enabled"] = true
	typedAnswer["provider"] = providerOpenAI
	typedAnswer["model"] = scoutChatModel()
	typedAnswer["fallbackModel"] = seatFallbackModel(seatChat, scoutChatModel())
	providerBreakerEvidence(typedAnswer, providerOpenAI, seatChat, seatFallbackModel(seatChat, scoutChatModel()) != "")
	// Realtime voice lanes have no text breaker; they still carry the honest
	// lane shape so every scout.lanes.* entry reads the same way.
	roomVoice["fallbackUsed"] = false
	privateVoice["fallbackUsed"] = false
	if kanbanApp != nil {
		kanbanApp.mu.Lock()
		roomVoice["connected"] = kanbanApp.connected
		roomVoice["allocated"] = kanbanApp.voiceControlActive
		transcriptLaneAllocated := kanbanApp.transcriptLane != nil
		kanbanApp.mu.Unlock()
		meetingSTT["allocated"] = transcriptLaneAllocated
		meetingSTT["connected"] = kanbanApp.transcriptionLaneConnected()
	}
	if roomRows, roomCircuitOpen := roomScoutCapabilityRows(kanbanApp); len(roomRows) > 0 {
		roomVoice["rooms"] = roomRows
		// A live room holds a real Scout session: a disconnect there is a fault.
		roomVoice["allocated"] = true
		if roomCircuitOpen {
			roomVoice["circuit"] = "open"
			roomVoice["retrySuppressed"] = true
		}
	}
	// A closed gate reads "disabled" only while nothing is running behind it.
	// If a room session is still live — a rollback, a mid-flight revocation, an
	// operator flipping the env under a running room — the lane must keep
	// reporting what that session is actually doing. Letting enabled=false win
	// there would mask a failing live session as a deliberate switch-off, which
	// is the same lie in the other direction.
	if roomVoice["enabled"] != true && capabilityLaneLive(roomVoice) {
		roomVoice["enabled"] = true
		delete(roomVoice, "reason")
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
		if capabilityStatusCountsDegraded(snap["status"]) {
			degraded = append(degraded, name)
		}
	}
	// Backward-compatible aggregates remain while clients adopt the split
	// contract. They degrade only when a required child degrades; idle
	// children never count as degraded (Wave 9 D3).
	scout := map[string]any{
		"enabled": true, "connected": roomVoice["connected"],
		"status": aggregateCapabilityStatus(roomVoice, privateVoice, typedRouter, typedAnswer),
		"lanes":  map[string]any{"roomVoice": roomVoice, "privateVoice": privateVoice, "typedRouter": typedRouter, "typedAnswer": typedAnswer},
	}
	// Wave 6 D7: the chat-only Scout seat sits beside the voice lanes, never
	// inside the aggregate — text availability must not relabel voice status.
	scout["scoutText"] = currentRoomScoutTextAvailability().snapshot()
	if capabilityStatusCountsDegraded(scout["status"]) {
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
		"enabled": true, "connected": meetingSTT["connected"],
		"status": aggregateCapabilityStatus(meetingSTT, dictation),
		"lanes":  map[string]any{"meeting": meetingSTT, "dictation": dictation},
	}
	if capabilityStatusCountsDegraded(stt["status"]) {
		degraded = append(degraded, capabilitySTT)
	}

	brainCfg := readinessAgentSnapshot(meetingBrainAgent())
	brain := ambientCapabilityEvidence(capabilityBrain, meetingBrainAgent(), now)
	for k, v := range brainCfg {
		brain[k] = v
	}
	brain["workers"] = map[string]any{
		"brain":        brainCfg,
		"missionIntel": readinessAgentSnapshot(missionIntelligenceAgent()),
	}
	markProviderFailure(brain, providerReady)
	brain["status"] = capabilityStatus(brain, providerReady)
	if capabilityStatusCountsDegraded(brain["status"]) {
		degraded = append(degraded, "brain")
	}
	ambientWorkers := ambientWorkersCapabilitySnapshot(now, providerReady)
	for key, raw := range ambientWorkers {
		worker, _ := raw.(map[string]any)
		if capabilityStatusCountsDegraded(worker["status"]) {
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
	if capabilityStatusCountsDegraded(recap["status"]) {
		degraded = append(degraded, "recap")
	}

	embeddings := capabilityEvidence(capabilityEmbedding, now, 2*embeddingInterval())
	embeddings["enabled"] = embeddingInterval() > 0 && !boolEnv("EMBEDDINGS_DISABLED")
	embeddings["model"] = embeddingModel()
	mergeCapabilityEvidence(embeddings, embeddingProviderCircuitEvidence())
	markProviderFailure(embeddings, providerReady)
	embeddings["status"] = capabilityStatus(embeddings, providerReady)
	if capabilityStatusCountsDegraded(embeddings["status"]) {
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
	if capabilityStatusCountsDegraded(workflows["status"]) {
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
	if capabilityStatusCountsDegraded(attachments["status"]) {
		degraded = append(degraded, capabilityAttachments)
	}

	backup := backupCapabilitySnapshot(now)
	if backup["status"] != "healthy" && backup["status"] != "disabled" {
		degraded = append(degraded, "backup")
	}
	roomRows, roomDegraded := roomOperationalCapabilityRows(kanbanApp, now, providerReady, roomConsentHealth())
	degraded = append(degraded, roomDegraded...)
	strideRuntime := strideRuntimeCapabilitySnapshot(kanbanApp)
	if capabilityStatusCountsDegraded(strideRuntime["status"]) {
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
