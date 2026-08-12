package main

import (
	"fmt"
	"strings"
	"time"
)

const privateRealtimeVoiceTransportAttemptLimit = 32

type scoutChatVoiceTransportMilestone struct {
	OperationID string `json:"operationId"`
	Name        string `json:"name"`
	ObservedAt  string `json:"observedAt"`
}

type scoutChatVoiceTransportAttempt struct {
	Revision   int                                `json:"revision"`
	State      string                             `json:"state"`
	StartedAt  string                             `json:"startedAt"`
	AcceptedAt string                             `json:"acceptedAt,omitempty"`
	FailedAt   string                             `json:"failedAt,omitempty"`
	Milestones []scoutChatVoiceTransportMilestone `json:"milestones,omitempty"`
}

type privateRealtimeVoiceLatencySnapshot struct {
	OfferAcceptedMS   int64 `json:"offerAcceptedMs,omitempty"`
	PeerConnectedMS   int64 `json:"peerConnectedMs,omitempty"`
	DataChannelOpenMS int64 `json:"dataChannelOpenMs,omitempty"`
	RemoteTrackMS     int64 `json:"remoteTrackMs,omitempty"`
	FirstAudioMS      int64 `json:"firstAudioMs,omitempty"`
	ResponseDoneMS    int64 `json:"responseDoneMs,omitempty"`
}

func validPrivateRealtimeVoiceTransportMilestone(name string) bool {
	switch strings.TrimSpace(name) {
	case "peer_connected", "data_channel_open", "remote_track", "first_audio", "response_done", "transport_error":
		return true
	default:
		return false
	}
}

func (app *kanbanBoardApp) beginPrivateRealtimeVoiceTransport(requesterEmail, voiceSessionID, threadID string, at time.Time) (int, error) {
	if app == nil {
		return 0, fmt.Errorf("private Realtime voice is unavailable")
	}
	lock := app.scoutChatThreadLock(strings.TrimSpace(threadID))
	lock.Lock()
	defer lock.Unlock()
	thread, err := app.privateRealtimeVoiceConversation(requesterEmail, voiceSessionID, threadID)
	if err != nil {
		return 0, err
	}
	binding := thread.VoiceSession
	for index := range binding.TransportAttempts {
		attempt := &binding.TransportAttempts[index]
		if attempt.State == "offering" || attempt.State == "accepted" {
			attempt.State = "superseded"
			attempt.FailedAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	binding.TransportRevision++
	revision := binding.TransportRevision
	binding.TransportAttempts = append(binding.TransportAttempts, scoutChatVoiceTransportAttempt{
		Revision:  revision,
		State:     "offering",
		StartedAt: at.UTC().Format(time.RFC3339Nano),
	})
	if len(binding.TransportAttempts) > privateRealtimeVoiceTransportAttemptLimit {
		binding.TransportAttempts = append([]scoutChatVoiceTransportAttempt(nil), binding.TransportAttempts[len(binding.TransportAttempts)-privateRealtimeVoiceTransportAttemptLimit:]...)
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return 0, err
	}
	return revision, nil
}

func (app *kanbanBoardApp) finishPrivateRealtimeVoiceTransport(requesterEmail, voiceSessionID, threadID string, revision int, accepted bool, at time.Time) error {
	if app == nil || revision <= 0 {
		return fmt.Errorf("private Realtime voice transport is invalid")
	}
	lock := app.scoutChatThreadLock(strings.TrimSpace(threadID))
	lock.Lock()
	defer lock.Unlock()
	thread, err := app.privateRealtimeVoiceConversation(requesterEmail, voiceSessionID, threadID)
	if err != nil {
		return err
	}
	if thread.VoiceSession.TransportRevision != revision {
		return fmt.Errorf("private Realtime voice transport revision was superseded")
	}
	attempt := privateRealtimeVoiceTransportAttemptByRevision(thread.VoiceSession, revision)
	if attempt == nil || attempt.State != "offering" {
		return fmt.Errorf("private Realtime voice transport revision is unavailable")
	}
	if accepted {
		attempt.State = "accepted"
		attempt.AcceptedAt = at.UTC().Format(time.RFC3339Nano)
	} else {
		attempt.State = "failed"
		attempt.FailedAt = at.UTC().Format(time.RFC3339Nano)
	}
	return app.saveScoutChatThread(thread)
}

func (app *kanbanBoardApp) appendPrivateRealtimeVoiceTransportMilestone(requesterEmail, voiceSessionID, threadID string, revision int, operationID, milestone string, at time.Time) (privateRealtimeVoiceLatencySnapshot, bool, error) {
	operationID, err := normalizeScoutIdempotencyKey(operationID)
	if err != nil {
		return privateRealtimeVoiceLatencySnapshot{}, false, fmt.Errorf("private Realtime voice milestone operation id is invalid")
	}
	milestone = strings.ToLower(strings.TrimSpace(milestone))
	if !validPrivateRealtimeVoiceTransportMilestone(milestone) {
		return privateRealtimeVoiceLatencySnapshot{}, false, fmt.Errorf("unknown realtime milestone")
	}
	lock := app.scoutChatThreadLock(strings.TrimSpace(threadID))
	lock.Lock()
	defer lock.Unlock()
	thread, err := app.privateRealtimeVoiceConversation(requesterEmail, voiceSessionID, threadID)
	if err != nil {
		return privateRealtimeVoiceLatencySnapshot{}, false, err
	}
	if thread.VoiceSession.TransportRevision != revision {
		return privateRealtimeVoiceLatencySnapshot{}, false, fmt.Errorf("private Realtime voice transport revision was superseded")
	}
	attempt := privateRealtimeVoiceTransportAttemptByRevision(thread.VoiceSession, revision)
	if attempt == nil || attempt.State == "offering" || attempt.AcceptedAt == "" {
		return privateRealtimeVoiceLatencySnapshot{}, false, fmt.Errorf("private Realtime voice transport revision is unavailable")
	}
	for _, receipt := range attempt.Milestones {
		if receipt.OperationID == operationID {
			if receipt.Name != milestone {
				return privateRealtimeVoiceLatencySnapshot{}, false, fmt.Errorf("private Realtime voice milestone operation id was reused")
			}
			return privateRealtimeVoiceTransportLatency(*attempt), true, nil
		}
		if receipt.Name == milestone {
			return privateRealtimeVoiceTransportLatency(*attempt), true, nil
		}
	}
	if attempt.State == "interrupted" {
		return privateRealtimeVoiceLatencySnapshot{}, false, fmt.Errorf("private Realtime voice transport is terminal")
	}
	if milestone == "first_audio" && (!privateRealtimeVoiceTransportHasMilestone(*attempt, "data_channel_open") || !privateRealtimeVoiceTransportHasMilestone(*attempt, "remote_track")) {
		return privateRealtimeVoiceLatencySnapshot{}, false, fmt.Errorf("private Realtime voice first audio arrived before transport readiness")
	}
	if milestone == "response_done" && !privateRealtimeVoiceTransportHasMilestone(*attempt, "data_channel_open") {
		return privateRealtimeVoiceLatencySnapshot{}, false, fmt.Errorf("private Realtime voice response completed before the data channel opened")
	}
	attempt.Milestones = append(attempt.Milestones, scoutChatVoiceTransportMilestone{
		OperationID: operationID,
		Name:        milestone,
		ObservedAt:  at.UTC().Format(time.RFC3339Nano),
	})
	if milestone == "transport_error" {
		attempt.State = "interrupted"
		attempt.FailedAt = at.UTC().Format(time.RFC3339Nano)
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return privateRealtimeVoiceLatencySnapshot{}, false, err
	}
	return privateRealtimeVoiceTransportLatency(*attempt), false, nil
}

func privateRealtimeVoiceTransportAttemptByRevision(binding *scoutChatVoiceSessionBinding, revision int) *scoutChatVoiceTransportAttempt {
	if binding == nil || revision <= 0 {
		return nil
	}
	for index := range binding.TransportAttempts {
		if binding.TransportAttempts[index].Revision == revision {
			return &binding.TransportAttempts[index]
		}
	}
	return nil
}

func privateRealtimeVoiceTransportHasMilestone(attempt scoutChatVoiceTransportAttempt, name string) bool {
	for _, milestone := range attempt.Milestones {
		if milestone.Name == name {
			return true
		}
	}
	return false
}

func privateRealtimeVoiceTransportLatency(attempt scoutChatVoiceTransportAttempt) privateRealtimeVoiceLatencySnapshot {
	startedAt, _ := time.Parse(time.RFC3339Nano, attempt.StartedAt)
	latency := privateRealtimeVoiceLatencySnapshot{}
	set := func(value string, destination *int64) {
		observedAt, err := time.Parse(time.RFC3339Nano, value)
		if err == nil && !startedAt.IsZero() && !observedAt.Before(startedAt) {
			*destination = observedAt.Sub(startedAt).Milliseconds()
		}
	}
	set(attempt.AcceptedAt, &latency.OfferAcceptedMS)
	for _, milestone := range attempt.Milestones {
		switch milestone.Name {
		case "peer_connected":
			set(milestone.ObservedAt, &latency.PeerConnectedMS)
		case "data_channel_open":
			set(milestone.ObservedAt, &latency.DataChannelOpenMS)
		case "remote_track":
			set(milestone.ObservedAt, &latency.RemoteTrackMS)
		case "first_audio":
			set(milestone.ObservedAt, &latency.FirstAudioMS)
		case "response_done":
			set(milestone.ObservedAt, &latency.ResponseDoneMS)
		}
	}
	return latency
}
