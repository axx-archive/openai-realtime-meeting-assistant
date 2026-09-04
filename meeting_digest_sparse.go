package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// Sparse recovery is a source ledger, not a high-water cursor. It is admitted
// only for a never-produced digest scope with no held or consumed baseline.
// A withheld historical source stays explicit even as a newer source completes.
const meetingDigestSparseMetadataKey = "meetingDigestSparseSource"
const meetingDigestSparseVersion = 1
const meetingDigestSparseReconcileLimit = 128
const meetingDigestSparseMaxStateBytes = 16 << 20
const meetingDigestSparseMaxSourceRefs = 32768

// The checkpoint is a bounded audit ledger, not an unlimited memory mirror.
// Reaching capacity holds recovery explicitly; it never truncates prior evidence.
type meetingDigestSparseRoomIndex struct {
	Brains    []int
	Outputs   map[string]int
	HasDigest bool
}

func sparseRefKey(ref meetingDigestSparseRef) string {
	raw, _ := json.Marshal([3]string{ref.Lane, ref.ID, ref.Revision})
	return string(raw)
}
func (store *meetingMemoryStore) indexMeetingDigestSparseEntryLocked(index int, entry meetingMemoryEntry) {
	if entry.Kind != meetingMemoryKindBrain && entry.Kind != meetingMemoryKindMeetingDigest {
		return
	}
	room := normalizeRoomID(entry.Metadata["roomId"])
	if store.sparseRecoveryRooms == nil {
		store.sparseRecoveryRooms = map[string]*meetingDigestSparseRoomIndex{}
	}
	dir := store.sparseRecoveryRooms[room]
	if dir == nil {
		dir = &meetingDigestSparseRoomIndex{Outputs: map[string]int{}}
		store.sparseRecoveryRooms[room] = dir
	}
	if memoryEntryIsMediaSoakCanary(entry) {
		return
	}
	if entry.Kind == meetingMemoryKindBrain {
		dir.Brains = append(dir.Brains, index)
		return
	}
	dir.HasDigest = true
	var ref meetingDigestSparseRef
	if meetingDigestIsSparse(entry) && json.Unmarshal([]byte(entry.Metadata[meetingDigestSparseMetadataKey]), &ref) == nil && ref.ID != "" {
		dir.Outputs[sparseRefKey(ref)] = index
	}
}

// Test-only per-row accounting, carried by the request rather than a global hook.
type meetingDigestSparseVisitKey struct{}

func sparseVisit(ctx context.Context) {
	if visit, _ := ctx.Value(meetingDigestSparseVisitKey{}).(func()); visit != nil {
		visit()
	}
}

type meetingDigestSparseRef struct {
	ID       string    `json:"id"`
	Revision string    `json:"revision"`
	Lane     string    `json:"lane"`
	Status   string    `json:"status"`
	OutputID string    `json:"outputId,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
	RetryAt  time.Time `json:"retryAt,omitempty"`
}
type meetingDigestSparseState struct {
	Version      int                      `json:"version"`
	RoomID       string                   `json:"roomId"`
	Sources      []meetingDigestSparseRef `json:"sources"`
	BrainCursor  int                      `json:"brainCursor,omitempty"`
	ReviewCursor int                      `json:"reviewCursor,omitempty"`
}
type meetingDigestSparsePass struct {
	Source    meetingDigestSparseRef
	Principal RecallPrincipal
	Key       string
}
type meetingDigestSparseContextKey struct{}

func meetingDigestSparsePassFromContext(ctx context.Context) *meetingDigestSparsePass {
	p, _ := ctx.Value(meetingDigestSparseContextKey{}).(*meetingDigestSparsePass)
	return p
}
func (app *kanbanBoardApp) meetingDigestSparsePath(roomID string) string {
	if app == nil || app.memory == nil || app.memory.path == "" {
		return ""
	}
	return app.memory.path + ".digest-coverage-" + sha256Hex([]byte(normalizeRoomID(roomID)))[:20] + ".json"
}
func loadMeetingDigestSparseState(path, roomID string) (meetingDigestSparseState, bool, error) {
	var s meetingDigestSparseState
	if path == "" {
		return s, false, errors.New("digest coverage persistence unavailable")
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, meetingDigestSparseMaxStateBytes+1))
	if len(raw) > meetingDigestSparseMaxStateBytes {
		return s, false, errors.New("digest coverage checkpoint capacity exceeded")
	}
	if os.IsNotExist(err) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if json.Unmarshal(raw, &s) != nil || s.Version != meetingDigestSparseVersion || s.RoomID != normalizeRoomID(roomID) {
		return s, false, errors.New("digest coverage checkpoint invalid")
	}
	if len(s.Sources) > meetingDigestSparseMaxSourceRefs || s.BrainCursor < 0 || s.ReviewCursor < 0 {
		return s, false, errors.New("digest coverage checkpoint bounds invalid")
	}
	seen := map[string]bool{}
	for _, ref := range s.Sources {
		key := ref.ID + ":" + ref.Revision
		if ref.ID == "" || len(ref.Revision) != 64 || seen[key] || ref.Attempts < 0 || ref.Attempts > ambientProviderMaxWindowAttempts {
			return s, false, errors.New("digest coverage source invalid")
		}
		seen[key] = true
	}
	return s, true, nil
}

var persistMeetingDigestSparseState = func(path string, state meetingDigestSparseState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(raw) > meetingDigestSparseMaxStateBytes || len(state.Sources) > meetingDigestSparseMaxSourceRefs {
		return errors.New("digest coverage checkpoint capacity exceeded")
	}
	return writeFileAtomicallyDurable(path, raw, 0o600)
}

func meetingDigestSparseRevision(entry meetingMemoryEntry) string {
	// Exact source body and scope metadata, including correction/withdrawal state.
	raw, _ := json.Marshal(entry)
	return sha256Hex(raw)
}
func meetingDigestSparseReadToken(entry meetingMemoryEntry) string {
	body := entry.BodyDigest
	if entry.Text != "" || body == "" {
		body = sha256Hex([]byte(entry.Text))
	}
	raw, _ := json.Marshal(struct {
		ID, Body string
		Metadata map[string]string
	}{entry.ID, body, entry.Metadata})
	return sha256Hex(raw)
}
func (store *meetingMemoryStore) rememberAuthorizedSparseDigest(entry meetingMemoryEntry) {
	if !meetingDigestIsSparse(entry) {
		return
	}
	if store.authorizedSparseDigests == nil {
		store.authorizedSparseDigests = map[string]string{}
	}
	store.authorizedSparseDigests[entry.ID] = meetingDigestSparseReadToken(entry)
}
func (app *kanbanBoardApp) meetingDigestSparsePrincipal(roomID string) RecallPrincipal {
	// A remembered meeting ID alone cannot reopen an ended private sitting.
	sitting := ""
	if app.meetings != nil {
		if record, active := app.meetings.activeRecord(roomID); active && record.ID == app.memory.currentMeetingID(roomID) {
			sitting = record.ID
		}
	}
	return app.currentRoomMediaRecallPrincipal(roomID, sitting)
}

func meetingDigestSparseLane(entry meetingMemoryEntry) (string, bool) {
	visibility := strings.ToLower(strings.TrimSpace(entry.Metadata["visibility"]))
	tenant := firstNonEmptyString(entry.Metadata["tenantId"], canonicalArtifactTenantID())
	switch visibility {
	case "", "organization", "org", "team", "public", "shared", fileVisibilityCompany:
		return "organization:" + tenant, true
	case "room", "room_only":
		sitting := firstNonEmptyString(entry.Metadata["sittingId"], entry.Metadata["meetingId"])
		room := normalizeRoomID(entry.Metadata["roomId"])
		if sitting == "" || room == officeRoomID {
			return "", false
		}
		return "room:" + tenant + ":" + room + ":" + sitting + ":" + entry.Metadata["mediaGeneration"], true
	default:
		return "", false
	}
}
func meetingDigestSparseCheckpointCompatible(agent ambientAgentConfig, roomID string, checkpoint ambientHeldWindow) bool {
	return (checkpoint.Agent == "" || checkpoint.Agent == agent.name) &&
		(checkpoint.RoomID == "" || normalizeRoomID(checkpoint.RoomID) == normalizeRoomID(roomID)) &&
		(checkpoint.InputKind == "" || checkpoint.InputKind == agent.inputKind) &&
		(checkpoint.ArtifactKind == "" || checkpoint.ArtifactKind == agent.artifactKind) &&
		(checkpoint.CursorMetadataKey == "" || checkpoint.CursorMetadataKey == agent.cursorMetadataKey) &&
		(checkpoint.BlockedReason == "" || checkpoint.BlockedReason == ambientContinuityAmbiguous)
}

func meetingDigestIsSparse(entry meetingMemoryEntry) bool {
	return entry.Metadata[meetingDigestSparseMetadataKey] != ""
}

// Caller holds store.mu. The child depends on one brain only; accepting a
// digest-as-source is forbidden, so dependency cycles and unbounded recursion
// are impossible. Principal nil checks freshness only, never grants access.
func (store *meetingMemoryStore) meetingDigestSparseCurrentLocked(entry meetingMemoryEntry, principal *RecallPrincipal) bool {
	if !meetingDigestIsSparse(entry) {
		return true
	}
	if principal == nil && store.path == "" && store.authorizedSparseDigests[entry.ID] == meetingDigestSparseReadToken(entry) {
		return true
	}
	var ref meetingDigestSparseRef
	outputDigest := entry.BodyDigest
	if entry.Text != "" || outputDigest == "" {
		outputDigest = sha256Hex([]byte(entry.Text))
	}
	if entry.Metadata["meetingDigestSparseOutputDigest"] != outputDigest {
		return false
	}
	if entry.Kind != meetingMemoryKindMeetingDigest || json.Unmarshal([]byte(entry.Metadata[meetingDigestSparseMetadataKey]), &ref) != nil || ref.ID == "" {
		return false
	}
	index, found := store.entryIndexByID[ref.ID]
	if !found || index < 0 || index >= len(store.entries) {
		return false
	}
	source := store.entries[index]
	lane, valid := meetingDigestSparseLane(source)
	return source.ID == ref.ID && source.Kind == meetingMemoryKindBrain && valid && lane == ref.Lane && meetingDigestSparseRevision(source) == ref.Revision && !memoryEntryHiddenFromRecall(source) && (principal == nil || recallEntryScopeAllowed(source.Metadata, *principal))
}

// Scope run lock is held by the chassis. A sparse checkpoint owns this scope
// thereafter; a scalar cursor must never reinterpret its deliberately partial
// output as consumption of inaccessible earlier rows.
func (app *kanbanBoardApp) runMeetingDigestSparseRecovery(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, roomID string) (meetingMemoryEntry, bool, error) {
	if agent.name != meetingDigestAgentName {
		return meetingMemoryEntry{}, false, nil
	}
	roomID = normalizeRoomID(roomID)
	path := app.meetingDigestSparsePath(roomID)
	if path == "" {
		return meetingMemoryEntry{}, false, nil
	}
	state, active, err := loadMeetingDigestSparseState(path, roomID)
	if err != nil {
		return meetingMemoryEntry{}, true, &ambientAgentHoldError{err: err}
	}
	checkpoint, hasCheckpoint, err := app.ambientScopeCheckpoint(ambientAgentScopeKey(agent, roomID))
	if err != nil {
		return meetingMemoryEntry{}, true, &ambientAgentHoldError{err: err}
	}
	if hasCheckpoint && !meetingDigestSparseCheckpointCompatible(agent, roomID, checkpoint) {
		if active {
			return meetingMemoryEntry{}, true, &ambientAgentHoldError{err: errors.New("digest coverage checkpoint contract mismatch")}
		}
		return meetingMemoryEntry{}, false, nil
	}
	// Never supersede held work, even if a stray sparse sidecar exists.
	if hasCheckpoint && (checkpoint.WindowID != "" || checkpoint.BaselineID != "") {
		if active {
			return meetingMemoryEntry{}, true, &ambientAgentHoldError{err: errors.New("digest coverage conflicts with held cursor")}
		}
		return meetingMemoryEntry{}, false, nil
	}
	if !active {
		if !hasCheckpoint || checkpoint.BlockedReason != ambientContinuityAmbiguous {
			return meetingMemoryEntry{}, false, nil
		}
		_, _, ambiguous := app.memory.ambientContinuityBaseline(agent, roomID)
		if !ambiguous {
			return meetingMemoryEntry{}, false, nil
		}

		state = meetingDigestSparseState{Version: meetingDigestSparseVersion, RoomID: roomID}
	}
	principal := app.meetingDigestSparsePrincipal(roomID)
	beforeState, _ := json.Marshal(state)
	app.memory.mu.Lock()
	dir := app.memory.sparseRecoveryRooms[roomID]
	if dir == nil {
		dir = &meetingDigestSparseRoomIndex{}
	}
	if !active && dir.HasDigest {
		app.memory.mu.Unlock()
		return meetingMemoryEntry{}, false, nil
	}
	current := map[string]int{}
	for i, ref := range state.Sources {
		if ref.Status != "superseded" {
			current[ref.ID] = i
		}
	}
	inputs := map[string]meetingMemoryEntry{}
	var candidates []int
	retryHeld := false
	visited := map[string]bool{}
	reconcile := func(id string) error {
		if visited[id] {
			return nil
		}
		visited[id] = true
		sparseVisit(ctx)
		index, found := app.memory.entryIndexByID[id]
		old, known := current[id]
		if !found || index < 0 || index >= len(app.memory.entries) || app.memory.entries[index].Kind != meetingMemoryKindBrain || normalizeRoomID(app.memory.entries[index].Metadata["roomId"]) != roomID {
			if known {
				state.Sources[old].Status = "superseded"
			}
			return nil
		}
		source := app.memory.entries[index]
		revision := meetingDigestSparseRevision(source)
		if known && state.Sources[old].Revision != revision {
			state.Sources[old].Status = "superseded"
			known = false
		}
		lane, _ := meetingDigestSparseLane(source)
		if !known {
			if len(state.Sources) >= meetingDigestSparseMaxSourceRefs {
				return errors.New("digest coverage checkpoint capacity exceeded")
			}
			old = len(state.Sources)
			current[id] = old
			state.Sources = append(state.Sources, meetingDigestSparseRef{ID: id, Revision: revision, Lane: lane, Status: "pending"})
		}
		ref := &state.Sources[old]
		if lane != ref.Lane {
			return errors.New("digest coverage audience checkpoint mismatch")
		}
		switch {
		case memoryEntryHiddenFromRecall(source):
			ref.Status = "withheld"
			return nil
		case lane == "":
			ref.Status = "pending_ambiguous_audience"
			return nil
		case !recallEntryScopeAllowed(source.Metadata, principal):
			ref.Status = "pending_authority"
			return nil
		}
		if outputIndex, exists := dir.Outputs[sparseRefKey(*ref)]; exists && outputIndex >= 0 && outputIndex < len(app.memory.entries) {
			sparseVisit(ctx)
			output := app.memory.entries[outputIndex]
			ref.OutputID = output.ID
			switch {
			case memoryEntryHiddenFromRecall(output):
				ref.Status = "output_withheld"
			case !func() bool { sparseVisit(ctx); return app.memory.meetingDigestSparseCurrentLocked(output, &principal) }():
				ref.Status = "output_changed"
			default:
				ref.Status = "consumed"
			}
			return nil
		}
		// A deleted output is not permission to recreate a deliberately removed result.
		if ref.OutputID != "" {
			ref.Status = "output_unavailable"
			return nil
		}
		if ref.Status != "in_flight" && ref.Status != "needs_attention" {
			ref.Status = "pending"
		}
		if ref.Attempts >= ambientProviderMaxWindowAttempts {
			ref.Status = "needs_attention"
		}
		if ref.Status == "needs_attention" || ((ref.Status == "pending" || ref.Status == "in_flight") && time.Now().Before(ref.RetryAt)) {
			retryHeld = true
		}
		if (ref.Status == "pending" || ref.Status == "in_flight") && !time.Now().Before(ref.RetryAt) {
			inputs[id] = cloneMemoryEntry(source)
			candidates = append(candidates, old)
		}
		return nil
	}
	// Discovery and reconciliation have separate durable rotating cursors. A
	// withheld, exhausted, corrected, or deleted source cannot stall later work.
	brainCount := len(dir.Brains)
	for count := 0; count < meetingDigestSparseReconcileLimit && count < brainCount; count++ {
		if state.BrainCursor >= brainCount {
			state.BrainCursor = 0
		}
		index := dir.Brains[state.BrainCursor]
		state.BrainCursor = (state.BrainCursor + 1) % brainCount
		sparseVisit(ctx)
		if index >= 0 && index < len(app.memory.entries) {
			if err = reconcile(app.memory.entries[index].ID); err != nil {
				break
			}
		}
	}
	sourceCount := len(state.Sources)
	if err == nil {
		for count := 0; count < meetingDigestSparseReconcileLimit && count < sourceCount; count++ {
			if state.ReviewCursor >= sourceCount {
				state.ReviewCursor = 0
			}
			ref := state.Sources[state.ReviewCursor]
			state.ReviewCursor = (state.ReviewCursor + 1) % sourceCount
			if ref.Status != "superseded" {
				if err = reconcile(ref.ID); err != nil {
					break
				}
			}
		}
	}
	afterState, _ := json.Marshal(state)
	// Initial enrollment and the never-produced eligibility check are atomic
	// against a concurrent legacy finalizer. Later manifest writes need only the
	// scope run lock and do not block unrelated memory readers on fsync.
	if err == nil && !active {
		err = persistMeetingDigestSparseState(path, state)
	}
	app.memory.mu.Unlock()
	if err == nil && active && !bytes.Equal(beforeState, afterState) {
		err = persistMeetingDigestSparseState(path, state)
	}
	if err != nil {
		return meetingMemoryEntry{}, true, &ambientAgentHoldError{err: err}
	}
	// Remove only obsolete continuity failure. Retry accounting below is durable
	// in the sparse ledger, never a generic skip/dead-letter cursor.
	app.mu.Lock()
	key := ambientAgentScopeKey(agent, roomID)
	if failure := app.agentFailures[key]; failure != nil && failure.continuityOpen && !failure.persistenceOpen {
		delete(app.agentFailures, key)
	}
	app.mu.Unlock()
	selected := -1
	if len(candidates) > 0 {
		selected = candidates[0]
	}
	if selected < 0 {
		if retryHeld {
			return meetingMemoryEntry{}, true, &ambientAgentHoldError{err: errors.New("digest coverage source retry held")}
		}
		return meetingMemoryEntry{}, true, nil
	}
	ref := &state.Sources[selected]
	ref.Status = "in_flight"
	ref.RetryAt = time.Now().UTC().Add(time.Duration(ref.Attempts+1) * time.Minute)
	if err := persistMeetingDigestSparseState(path, state); err != nil {
		return meetingMemoryEntry{}, true, &ambientAgentHoldError{err: err}
	}
	source := inputs[ref.ID]
	pass := &meetingDigestSparsePass{Source: *ref, Principal: principal, Key: digestKeyForBrain(source) + "~coverage~" + sha256Hex([]byte(ref.Lane + ":" + ref.ID))[:24]}
	ctx = context.WithValue(ctx, meetingDigestSparseContextKey{}, pass)
	if responder == nil {
		responder = createOpenAITextResponse
	}
	guardedResponder := func(callCtx context.Context, key string, request openAITextRequest) (string, error) {
		currentPrincipal := app.meetingDigestSparsePrincipal(roomID)
		app.memory.mu.Lock()
		current := false
		sparseVisit(callCtx)
		if index, found := app.memory.entryIndexByID[pass.Source.ID]; found && index >= 0 && index < len(app.memory.entries) {
			stored := app.memory.entries[index]
			current = stored.ID == pass.Source.ID && stored.Kind == meetingMemoryKindBrain && !memoryEntryHiddenFromRecall(stored) && meetingDigestSparseRevision(stored) == pass.Source.Revision && recallEntryScopeAllowed(stored.Metadata, currentPrincipal)
		}
		app.memory.mu.Unlock()
		if !current || currentPrincipal != pass.Principal {
			return "", errors.New("digest coverage source changed before provider")
		}
		if ref.Attempts >= ambientProviderMaxWindowAttempts {
			return "", &ambientAgentHoldError{err: errors.New("digest coverage provider budget exhausted")}
		}
		ref.Attempts++
		if err := persistMeetingDigestSparseState(path, state); err != nil {
			return "", &ambientAgentHoldError{err: err}
		}
		return responder(callCtx, key, request)
	}
	entry, err := app.produceMeetingDigests(ctx, apiKey, []meetingMemoryEntry{source}, guardedResponder)
	if err != nil {
		return entry, true, err
	}
	if entry.ID == "" || !meetingDigestIsSparse(entry) {
		return entry, true, &ambientAgentHoldError{err: errors.New("digest coverage output missing receipt")}
	}
	ref.Status, ref.OutputID = "consumed", entry.ID
	ref.RetryAt = time.Time{}
	if err := persistMeetingDigestSparseState(path, state); err != nil {
		return entry, true, &ambientAgentHoldError{err: err}
	}
	return entry, true, nil
}

func (app *kanbanBoardApp) persistMeetingDigestOutput(ctx context.Context, key, text string, metadata map[string]string) (meetingMemoryEntry, error) {
	pass := meetingDigestSparsePassFromContext(ctx)
	if pass == nil {
		return app.memory.upsertDigest(meetingMemoryKindMeetingDigest, key, text, metadata)
	}
	raw, _ := json.Marshal(meetingDigestSparseRef{ID: pass.Source.ID, Revision: pass.Source.Revision, Lane: pass.Source.Lane, Status: "consumed"})
	metadata[meetingDigestSparseMetadataKey] = string(raw)
	metadata["meetingDigestSparseOutputDigest"] = sha256Hex([]byte(text))
	metadata["coveragePolicy"] = "source_bound_recall_only"
	metadata[digestCoverageMetadataKey] = "partial"
	// A capture high-water does not establish coverage of withheld inputs.
	delete(metadata, meetingDigestCaptureMetadataKey)
	delete(metadata, meetingDigestCursorMetadataKey)
	delete(metadata, digestSittingStartedAtMetadataKey)
	delete(metadata, digestSittingEndedAtMetadataKey)
	principal := app.meetingDigestSparsePrincipal(metadata["roomId"])
	if principal != pass.Principal {
		return meetingMemoryEntry{}, errors.New("digest coverage authority changed")
	}
	app.memory.mu.Lock()
	defer app.memory.mu.Unlock()
	sparseVisit(ctx)
	probe := meetingMemoryEntry{Kind: meetingMemoryKindMeetingDigest, Text: text, Metadata: metadata}
	if !app.memory.meetingDigestSparseCurrentLocked(probe, &principal) {
		return meetingMemoryEntry{}, errors.New("digest coverage source changed")
	}
	return app.memory.upsertDigestLocked(meetingMemoryKindMeetingDigest, pass.Key, text, metadata, true)
}

// Public health exposes counts only. Source IDs and historical audiences stay
// inside the private ledger and the authorized artifact receipt.
func (app *kanbanBoardApp) meetingDigestSparseDiagnostics(roomID string) map[string]any {
	state, active, err := loadMeetingDigestSparseState(app.meetingDigestSparsePath(roomID), roomID)
	if err != nil {
		return map[string]any{"status": "checkpoint_unavailable"}
	}
	if !active {
		return nil
	}
	counts := map[string]int{}
	for _, ref := range state.Sources {
		counts[ref.Status]++
	}
	return map[string]any{"status": "partial_source_coverage", "sources": len(state.Sources), "counts": counts}
}
