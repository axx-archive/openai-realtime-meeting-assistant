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
)

type participantAudioFrame struct {
	At                  time.Time
	EnergyByParticipant map[string]float64
}

type participantEnergyScore struct {
	Name   string
	Energy float64
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

	app.mu.Lock()
	defer app.mu.Unlock()

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
