package main

import (
	"math"
	"sort"
	"strings"
	"time"
)

const (
	speakerActivityRetention       = 2 * time.Minute
	speakerAttributionFallbackSpan = 12 * time.Second
	speakerAttributionStartPadding = 450 * time.Millisecond
	speakerAttributionStopPadding  = 650 * time.Millisecond
	speakerAttributionMixedRatio   = 0.82
	activeSpeakerStabilityWindow   = 700 * time.Millisecond
	activeSpeakerRefreshInterval   = time.Second
)

type participantAudioFrame struct {
	At                  time.Time
	EnergyByParticipant map[string]float64
}

type participantEnergyScore struct {
	Name   string
	Energy float64
}

type activeSpeakerPayload struct {
	Name       string  `json:"name"`
	Level      float64 `json:"level"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
	At         int64   `json:"at"`
	RoomID     string  `json:"roomId,omitempty"`
}

// NoteAudioActivity is the OFFICE mixer's activity listener (the boot-started
// global mixer registers kanbanApp itself). Named rooms feed the same state
// machine through roomAudioActivityListener → noteAudioActivityForRoom, so
// two live rooms never blend attribution energy (multi-room W3).
func (app *kanbanBoardApp) NoteAudioActivity(at time.Time, levels []audioActivityLevel) {
	app.noteAudioActivityForRoom(officeRoomID, at, levels)
}

func (app *kanbanBoardApp) noteAudioActivityForRoom(roomID string, at time.Time, levels []audioActivityLevel) {
	if app == nil || len(levels) == 0 {
		return
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	roomID = normalizeRoomID(roomID)

	energyByParticipant := map[string]float64{}
	for _, level := range levels {
		name := canonicalRoomParticipantName(level.ParticipantName)
		if name == "" {
			continue
		}
		energyByParticipant[name] += math.Max(0, level.RMS) * math.Max(0, level.RMS)
	}
	if len(energyByParticipant) == 0 {
		return
	}

	var activeSpeaker *activeSpeakerPayload
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)

	state.audioActivity = append(state.audioActivity, participantAudioFrame{
		At:                  at.UTC(),
		EnergyByParticipant: energyByParticipant,
	})
	cutoff := at.Add(-speakerActivityRetention)
	keepFrom := 0
	for keepFrom < len(state.audioActivity) && state.audioActivity[keepFrom].At.Before(cutoff) {
		keepFrom++
	}
	if keepFrom > 0 {
		state.audioActivity = append([]participantAudioFrame(nil), state.audioActivity[keepFrom:]...)
	}
	activeSpeaker = app.noteActiveSpeakerActivityLocked(state, at.UTC(), energyByParticipant)
	app.mu.Unlock()

	if activeSpeaker != nil {
		log.Infof("room_active_speaker room=%s name=%q level=%.5f confidence=%.3f", roomID, activeSpeaker.Name, activeSpeaker.Level, activeSpeaker.Confidence)
		broadcastRoomKanbanEvent(roomID, "active_speaker", activeSpeaker)
	}
}

func (app *kanbanBoardApp) activeSpeakerSnapshot() *activeSpeakerPayload {
	return app.activeSpeakerSnapshotForRoom(officeRoomID)
}

func (app *kanbanBoardApp) activeSpeakerSnapshotForRoom(roomID string) *activeSpeakerPayload {
	if app == nil {
		return nil
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if state.activeSpeakerPayload == nil {
		return nil
	}
	payload := *state.activeSpeakerPayload
	if !app.participantCanBeActiveSpeakerLocked(state, payload.Name) {
		return nil
	}
	return &payload
}

// activeSpeakerNameForSegmentation returns the current stable active speaker's
// name, or "" when none is confidently held. The lane uses it to break a
// committed segment on a speaker change (A6) so an interjection isn't folded
// under the prior speaker; it rides the same stability gate as the active
// speaker payload, so it won't thrash on momentary crosstalk.
func (app *kanbanBoardApp) activeSpeakerNameForSegmentation() string {
	return app.activeSpeakerNameForSegmentationForRoom(officeRoomID)
}

func (app *kanbanBoardApp) activeSpeakerNameForSegmentationForRoom(roomID string) string {
	snapshot := app.activeSpeakerSnapshotForRoom(roomID)
	if snapshot == nil {
		return ""
	}
	return snapshot.Name
}

func (app *kanbanBoardApp) noteActiveSpeakerActivityLocked(state *roomLiveState, at time.Time, energyByParticipant map[string]float64) *activeSpeakerPayload {
	ranked := rankedActiveSpeakerEnergyLocked(app, state, energyByParticipant)
	if len(ranked) == 0 {
		return nil
	}

	leader := ranked[0]
	if leader.Name != state.activeSpeakerCandidate {
		state.activeSpeakerCandidate = leader.Name
		state.activeSpeakerCandidateAt = at
		return nil
	}
	if state.activeSpeakerCandidateAt.IsZero() || at.Sub(state.activeSpeakerCandidateAt) < activeSpeakerStabilityWindow {
		return nil
	}
	if leader.Name == state.activeSpeakerName {
		if state.activeSpeakerPayload != nil && at.Sub(time.UnixMilli(state.activeSpeakerPayload.At)) >= activeSpeakerRefreshInterval {
			payload := activeSpeakerPayloadForLeader(state.id, at, leader, ranked)
			state.activeSpeakerPayload = payload
			return payload
		}
		return nil
	}

	state.activeSpeakerName = leader.Name
	payload := activeSpeakerPayloadForLeader(state.id, at, leader, ranked)
	state.activeSpeakerPayload = payload
	return payload
}

func activeSpeakerPayloadForLeader(roomID string, at time.Time, leader participantEnergyScore, ranked []participantEnergyScore) *activeSpeakerPayload {
	confidence := 1.0
	if len(ranked) > 1 && leader.Energy > 0 {
		confidence = leader.Energy / (leader.Energy + ranked[1].Energy)
	}
	return &activeSpeakerPayload{
		Name:       leader.Name,
		Level:      math.Min(1, math.Sqrt(leader.Energy)/32768),
		Confidence: math.Max(0, math.Min(1, confidence)),
		Source:     "server",
		At:         at.UnixMilli(),
		RoomID:     roomID,
	}
}

func rankedActiveSpeakerEnergyLocked(app *kanbanBoardApp, state *roomLiveState, energyByParticipant map[string]float64) []participantEnergyScore {
	ranked := make([]participantEnergyScore, 0, len(energyByParticipant))
	for name, energy := range energyByParticipant {
		name = canonicalRoomParticipantName(name)
		if name == "" || energy <= 0 || !app.participantCanBeActiveSpeakerLocked(state, name) {
			continue
		}
		ranked = append(ranked, participantEnergyScore{Name: name, Energy: energy})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Energy != ranked[j].Energy {
			return ranked[i].Energy > ranked[j].Energy
		}
		return ranked[i].Name < ranked[j].Name
	})
	return ranked
}

func (app *kanbanBoardApp) participantCanBeActiveSpeakerLocked(state *roomLiveState, name string) bool {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return false
	}
	if _, ok := state.participants[name]; !ok {
		return false
	}
	if state.participantMedia[name].MicMuted {
		return false
	}
	return true
}

// attributionWindow is a speech-boundary pair frozen at input_audio_buffer.commit
// time and carried with the committed segment until its transcription.completed
// event returns (A6). Freezing at commit — rather than reading the mutable
// shared currentSpeech* markers when completed lands — keeps a rapid speaker
// handoff from mis-attributing the earlier speaker's text to the later one.
type attributionWindow struct {
	startedAt time.Time
	stoppedAt time.Time
}

// maxPendingAttributionWindows caps the frozen-window FIFO so a dropped or
// coalesced completed event can never grow it without bound; the oldest window
// is discarded past the cap (attribution then falls back to the live markers).
const maxPendingAttributionWindows = 64

func (app *kanbanBoardApp) noteRealtimeSpeechStarted() {
	app.noteRealtimeSpeechStartedForRoom(officeRoomID)
}

func (app *kanbanBoardApp) noteRealtimeSpeechStartedForRoom(roomID string) {
	if app == nil {
		return
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.currentSpeechStartedAt = time.Now().UTC()
	state.currentSpeechStoppedAt = time.Time{}
	app.mu.Unlock()
}

func (app *kanbanBoardApp) noteRealtimeSpeechStopped() {
	app.noteRealtimeSpeechStoppedForRoom(officeRoomID)
}

func (app *kanbanBoardApp) noteRealtimeSpeechStoppedForRoom(roomID string) {
	if app == nil {
		return
	}

	app.mu.Lock()
	app.roomLiveLocked(roomID).currentSpeechStoppedAt = time.Now().UTC()
	app.mu.Unlock()
}

// freezeAttributionWindowAtCommit snapshots the current speech-boundary markers
// into the commit-ordered FIFO and clears them so the next turn starts fresh
// (A6). Call it exactly once per input_audio_buffer.commit, right after the
// matching noteRealtimeSpeechStopped, on whichever session persists.
func (app *kanbanBoardApp) freezeAttributionWindowAtCommit() {
	app.freezeAttributionWindowAtCommitForRoom(officeRoomID)
}

func (app *kanbanBoardApp) freezeAttributionWindowAtCommitForRoom(roomID string) {
	if app == nil {
		return
	}

	now := time.Now().UTC()
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	startedAt := state.currentSpeechStartedAt
	stoppedAt := state.currentSpeechStoppedAt
	if stoppedAt.IsZero() || (!startedAt.IsZero() && stoppedAt.Before(startedAt)) {
		stoppedAt = now
	}
	if startedAt.IsZero() {
		startedAt = stoppedAt.Add(-speakerAttributionFallbackSpan)
	}
	state.pendingAttributionWindows = append(state.pendingAttributionWindows, attributionWindow{
		startedAt: startedAt,
		stoppedAt: stoppedAt,
	})
	if overflow := len(state.pendingAttributionWindows) - maxPendingAttributionWindows; overflow > 0 {
		state.pendingAttributionWindows = append([]attributionWindow(nil), state.pendingAttributionWindows[overflow:]...)
	}
	state.currentSpeechStartedAt = time.Time{}
	state.currentSpeechStoppedAt = time.Time{}
	app.mu.Unlock()
}

// speakerForCommittedTranscript resolves the speaker for a completed transcript
// from the window frozen at its commit (A6), popping the FIFO in commit order.
// When no frozen window is queued (e.g. a completed with no preceding commit
// hook) it falls back to the legacy live-marker path.
func (app *kanbanBoardApp) speakerForCommittedTranscript(completedAt time.Time) (string, string) {
	return app.speakerForCommittedTranscriptForRoom(officeRoomID, completedAt)
}

func (app *kanbanBoardApp) speakerForCommittedTranscriptForRoom(roomID string, completedAt time.Time) (string, string) {
	if app == nil {
		return "", "unknown"
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if len(state.pendingAttributionWindows) == 0 {
		app.mu.Unlock()
		return app.speakerForCompletedTranscriptForRoom(roomID, completedAt)
	}
	window := state.pendingAttributionWindows[0]
	state.pendingAttributionWindows = append([]attributionWindow(nil), state.pendingAttributionWindows[1:]...)
	scores := attributionScoresLocked(state, window.startedAt, window.stoppedAt)
	app.mu.Unlock()

	return dominantTranscriptSpeaker(scores)
}

// popPendingAttributionWindow discards the FIFO front without resolving a
// speaker (A6). Call it on a terminal transcription event that produces no
// persisted transcript — chiefly
// conversation.item.input_audio_transcription.failed — so a committed segment
// that never yields a .completed cannot leave its frozen window queued and
// shift every later transcript's attribution by one turn for the rest of the
// sitting. Freeze (at commit) and pop must stay symmetric.
func (app *kanbanBoardApp) popPendingAttributionWindow() {
	app.popPendingAttributionWindowForRoom(officeRoomID)
}

func (app *kanbanBoardApp) popPendingAttributionWindowForRoom(roomID string) {
	if app == nil {
		return
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if len(state.pendingAttributionWindows) > 0 {
		state.pendingAttributionWindows = append([]attributionWindow(nil), state.pendingAttributionWindows[1:]...)
	}
	app.mu.Unlock()
}

// resetPendingAttributionWindows drops every queued window (A6). Call it on lane
// (re)connect and on a recording on/off transition so an orphaned window frozen
// by a commit whose .completed never arrived (a mid-turn disconnect, or a commit
// that landed while recording was off) cannot drift the next connection's
// attribution.
func (app *kanbanBoardApp) resetPendingAttributionWindows() {
	app.resetPendingAttributionWindowsForRoom(officeRoomID)
}

func (app *kanbanBoardApp) resetPendingAttributionWindowsForRoom(roomID string) {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.roomLiveLocked(roomID).pendingAttributionWindows = nil
	app.mu.Unlock()
}

// speakerForCompletedTranscript is the legacy path: it reads (and clears) the
// mutable shared speech markers. Retained as the fallback for
// speakerForCommittedTranscript when no window was frozen at commit.
func (app *kanbanBoardApp) speakerForCompletedTranscript(completedAt time.Time) (string, string) {
	return app.speakerForCompletedTranscriptForRoom(officeRoomID, completedAt)
}

func (app *kanbanBoardApp) speakerForCompletedTranscriptForRoom(roomID string, completedAt time.Time) (string, string) {
	if app == nil {
		return "", "unknown"
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	startedAt := state.currentSpeechStartedAt
	stoppedAt := state.currentSpeechStoppedAt
	if stoppedAt.IsZero() || (!startedAt.IsZero() && stoppedAt.Before(startedAt)) {
		stoppedAt = completedAt
	}
	if startedAt.IsZero() {
		startedAt = stoppedAt.Add(-speakerAttributionFallbackSpan)
	}
	scores := attributionScoresLocked(state, startedAt, stoppedAt)

	state.currentSpeechStartedAt = time.Time{}
	state.currentSpeechStoppedAt = time.Time{}
	app.mu.Unlock()

	return dominantTranscriptSpeaker(scores)
}

// attributionScoresLocked sums per-participant audio energy over the padded
// attribution window. Caller must hold app.mu.
func attributionScoresLocked(state *roomLiveState, startedAt, stoppedAt time.Time) map[string]float64 {
	windowStart := startedAt.Add(-speakerAttributionStartPadding)
	windowStop := stoppedAt.Add(speakerAttributionStopPadding)

	scores := map[string]float64{}
	for _, frame := range state.audioActivity {
		if frame.At.Before(windowStart) || frame.At.After(windowStop) {
			continue
		}
		for participant, energy := range frame.EnergyByParticipant {
			scores[participant] += energy
		}
	}
	return scores
}

func dominantTranscriptSpeaker(scores map[string]float64) (string, string) {
	ranked := make([]participantEnergyScore, 0, len(scores))
	for name, energy := range scores {
		name = canonicalRoomParticipantName(name)
		if name == "" || energy <= 0 {
			continue
		}
		ranked = append(ranked, participantEnergyScore{Name: name, Energy: energy})
	}
	if len(ranked) == 0 {
		return "", "unknown"
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Energy != ranked[j].Energy {
			return ranked[i].Energy > ranked[j].Energy
		}
		return ranked[i].Name < ranked[j].Name
	})
	if len(ranked) > 1 && ranked[1].Energy/ranked[0].Energy >= speakerAttributionMixedRatio {
		return strings.Join([]string{ranked[0].Name, ranked[1].Name}, " + "), "mixed"
	}

	return ranked[0].Name, "dominant"
}
