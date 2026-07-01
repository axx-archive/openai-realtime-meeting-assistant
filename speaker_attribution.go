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
}

func (app *kanbanBoardApp) NoteAudioActivity(at time.Time, levels []audioActivityLevel) {
	if app == nil || len(levels) == 0 {
		return
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	energyByParticipant := map[string]float64{}
	for _, level := range levels {
		name := canonicalParticipantName(level.ParticipantName)
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

	app.audioActivity = append(app.audioActivity, participantAudioFrame{
		At:                  at.UTC(),
		EnergyByParticipant: energyByParticipant,
	})
	cutoff := at.Add(-speakerActivityRetention)
	keepFrom := 0
	for keepFrom < len(app.audioActivity) && app.audioActivity[keepFrom].At.Before(cutoff) {
		keepFrom++
	}
	if keepFrom > 0 {
		app.audioActivity = append([]participantAudioFrame(nil), app.audioActivity[keepFrom:]...)
	}
	activeSpeaker = app.noteActiveSpeakerActivityLocked(at.UTC(), energyByParticipant)
	app.mu.Unlock()

	if activeSpeaker != nil {
		log.Infof("room_active_speaker name=%q level=%.5f confidence=%.3f", activeSpeaker.Name, activeSpeaker.Level, activeSpeaker.Confidence)
		broadcastKanbanEvent("active_speaker", activeSpeaker)
	}
}

func (app *kanbanBoardApp) activeSpeakerSnapshot() *activeSpeakerPayload {
	if app == nil {
		return nil
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.activeSpeakerPayload == nil {
		return nil
	}
	payload := *app.activeSpeakerPayload
	if !app.participantCanBeActiveSpeakerLocked(payload.Name) {
		return nil
	}
	return &payload
}

func (app *kanbanBoardApp) noteActiveSpeakerActivityLocked(at time.Time, energyByParticipant map[string]float64) *activeSpeakerPayload {
	ranked := rankedActiveSpeakerEnergyLocked(app, energyByParticipant)
	if len(ranked) == 0 {
		return nil
	}

	leader := ranked[0]
	if leader.Name != app.activeSpeakerCandidate {
		app.activeSpeakerCandidate = leader.Name
		app.activeSpeakerCandidateAt = at
		return nil
	}
	if app.activeSpeakerCandidateAt.IsZero() || at.Sub(app.activeSpeakerCandidateAt) < activeSpeakerStabilityWindow {
		return nil
	}
	if leader.Name == app.activeSpeakerName {
		if app.activeSpeakerPayload != nil && at.Sub(time.UnixMilli(app.activeSpeakerPayload.At)) >= activeSpeakerRefreshInterval {
			payload := activeSpeakerPayloadForLeader(at, leader, ranked)
			app.activeSpeakerPayload = payload
			return payload
		}
		return nil
	}

	app.activeSpeakerName = leader.Name
	payload := activeSpeakerPayloadForLeader(at, leader, ranked)
	app.activeSpeakerPayload = payload
	return payload
}

func activeSpeakerPayloadForLeader(at time.Time, leader participantEnergyScore, ranked []participantEnergyScore) *activeSpeakerPayload {
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
	}
}

func rankedActiveSpeakerEnergyLocked(app *kanbanBoardApp, energyByParticipant map[string]float64) []participantEnergyScore {
	ranked := make([]participantEnergyScore, 0, len(energyByParticipant))
	for name, energy := range energyByParticipant {
		name = canonicalParticipantName(name)
		if name == "" || energy <= 0 || !app.participantCanBeActiveSpeakerLocked(name) {
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

func (app *kanbanBoardApp) participantCanBeActiveSpeakerLocked(name string) bool {
	name = canonicalParticipantName(name)
	if name == "" {
		return false
	}
	if _, ok := app.participants[name]; !ok {
		return false
	}
	if app.participantMedia[name].MicMuted {
		return false
	}
	return true
}

func (app *kanbanBoardApp) noteRealtimeSpeechStarted() {
	if app == nil {
		return
	}

	app.mu.Lock()
	app.currentSpeechStartedAt = time.Now().UTC()
	app.currentSpeechStoppedAt = time.Time{}
	app.mu.Unlock()
}

func (app *kanbanBoardApp) noteRealtimeSpeechStopped() {
	if app == nil {
		return
	}

	app.mu.Lock()
	app.currentSpeechStoppedAt = time.Now().UTC()
	app.mu.Unlock()
}

func (app *kanbanBoardApp) speakerForCompletedTranscript(completedAt time.Time) (string, string) {
	if app == nil {
		return "", "unknown"
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}

	app.mu.Lock()
	startedAt := app.currentSpeechStartedAt
	stoppedAt := app.currentSpeechStoppedAt
	if stoppedAt.IsZero() || (!startedAt.IsZero() && stoppedAt.Before(startedAt)) {
		stoppedAt = completedAt
	}
	if startedAt.IsZero() {
		startedAt = stoppedAt.Add(-speakerAttributionFallbackSpan)
	}
	windowStart := startedAt.Add(-speakerAttributionStartPadding)
	windowStop := stoppedAt.Add(speakerAttributionStopPadding)

	scores := map[string]float64{}
	for _, frame := range app.audioActivity {
		if frame.At.Before(windowStart) || frame.At.After(windowStop) {
			continue
		}
		for participant, energy := range frame.EnergyByParticipant {
			scores[participant] += energy
		}
	}

	app.currentSpeechStartedAt = time.Time{}
	app.currentSpeechStoppedAt = time.Time{}
	app.mu.Unlock()

	return dominantTranscriptSpeaker(scores)
}

func dominantTranscriptSpeaker(scores map[string]float64) (string, string) {
	ranked := make([]participantEnergyScore, 0, len(scores))
	for name, energy := range scores {
		name = canonicalParticipantName(name)
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
