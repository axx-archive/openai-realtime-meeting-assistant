package main

import (
	"context"
	"sort"
	"strings"
	"time"
)

type roomOperationalPointers struct {
	roomID          string
	sittingID       string
	mediaGeneration uint64
	participants    int
	recording       bool
	media           bool
	mixer           bool
	lane            *meetingTranscriptionLane
	scout           *roomRealtimeBundle
}

// roomOperationalCapabilityRows keeps live-call truth room-scoped. It does
// not treat a healthy office lane as evidence for another room and it does not
// make AI health a prerequisite for human media.
func roomOperationalCapabilityRows(app *kanbanBoardApp, now time.Time, providerReady bool, consentErr error) ([]map[string]any, []string) {
	if app == nil {
		return nil, nil
	}
	app.mu.Lock()
	pointers := make([]roomOperationalPointers, 0, len(app.roomLive))
	for roomID, state := range app.roomLive {
		if state == nil {
			continue
		}
		lane := state.lane
		if normalizeRoomID(roomID) == officeRoomID && app.transcriptLane != nil {
			lane = app.transcriptLane
		}
		pointers = append(pointers, roomOperationalPointers{
			roomID: normalizeRoomID(roomID), sittingID: state.mediaSittingID, mediaGeneration: state.mediaGen,
			participants: len(state.participants), recording: state.recordingEnabled,
			media: state.mediaActor != nil, mixer: state.mixer != nil, lane: lane, scout: state.realtime,
		})
	}
	app.mu.Unlock()

	rows := make([]map[string]any, 0, len(pointers))
	degraded := []string{}
	for _, pointer := range pointers {
		active := pointer.media || pointer.participants > 0
		row := map[string]any{
			"roomId": pointer.roomID, "sittingId": pointer.sittingID, "mediaGeneration": pointer.mediaGeneration,
			"participants": pointer.participants, "recordingEnabled": pointer.recording,
		}
		media := map[string]any{"status": "idle", "active": active, "actor": pointer.media, "mixer": pointer.mixer}
		if active {
			if pointer.media && pointer.mixer {
				media["status"] = "healthy"
			} else {
				media["status"] = "degraded"
				degraded = append(degraded, "rooms."+pointer.roomID+".media")
			}
		}
		row["media"] = media

		consent := map[string]any{"policyVersion": roomConsentPolicyVersion(), "status": "healthy"}
		if consentErr != nil {
			consent["status"] = "degraded"
			consent["lastError"] = consentErr.Error()
			if active {
				degraded = append(degraded, "rooms."+pointer.roomID+".consent")
			}
		}
		row["consent"] = consent

		lastTranscript, transcriptFound := latestRoomMemoryArtifact(app.memory, pointer.roomID, pointer.sittingID, meetingMemoryKindTranscript)
		transcript := roomArtifactHealth(lastTranscript, transcriptFound, now, 5*time.Minute)
		transcript["authoritative"] = true
		row["transcript"] = transcript

		stt := map[string]any{"enabled": transcriptionLaneEnabled(), "connected": false, "model": transcriptionLaneModel(), "status": "idle"}
		if !transcriptionLaneEnabled() {
			stt["status"] = "disabled"
		} else if pointer.lane != nil {
			connected := pointer.lane.isConnected()
			stt["connected"] = connected
			if connected && providerReady {
				stt["status"] = "healthy"
			} else {
				stt["status"] = "degraded"
			}
		} else if active && pointer.recording {
			stt["status"] = "degraded"
		}
		if stt["status"] == "degraded" && active {
			degraded = append(degraded, "rooms."+pointer.roomID+".stt")
		}
		row["stt"] = stt

		analysisAt, analysisFound := latestRoomMemoryArtifact(app.memory, pointer.roomID, pointer.sittingID, meetingMemoryKindBrain)
		analysis := roomArtifactHealth(analysisAt, analysisFound, now, 2*meetingBrainAgent().interval())
		row["analysis"] = analysis
		brain := map[string]any{}
		for key, value := range analysis {
			brain[key] = value
		}
		brain["source"] = "authorized_room_projection"
		row["brain"] = brain

		recapAt, recapFound := latestRoomMemoryArtifact(app.memory, pointer.roomID, pointer.sittingID, meetingMemoryKindMeetingDigest)
		row["recap"] = roomArtifactHealth(recapAt, recapFound, now, 24*time.Hour)

		scout := map[string]any{"status": "idle", "connected": false, "circuit": "closed"}
		if pointer.scout != nil {
			snapshot := pointer.scout.snapshot()
			scout["status"] = string(snapshot.Status)
			scout["connected"] = snapshot.Status == RoomScoutReady
			if snapshot.LastError != "" {
				scout["lastError"] = snapshot.LastError
			}
			pointer.scout.mu.Lock()
			transport := pointer.scout.transport
			pointer.scout.mu.Unlock()
			if providerTransport, ok := transport.(*openAIRoomScoutTransport); ok {
				circuit := providerTransport.providerCircuitSnapshot()
				scout["retryAttempts"] = circuit.Failures
				if circuit.Open {
					scout["circuit"] = "open"
					scout["retrySuppressed"] = true
				}
			}
		} else if active {
			scout["status"] = "degraded"
		}
		if !providerReady && active {
			scout["status"] = "degraded"
			scout["provider"] = providerOpenAI
		}
		if scout["status"] == "degraded" && active {
			degraded = append(degraded, "rooms."+pointer.roomID+".scout")
		}
		row["scout"] = scout
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return asString(rows[i]["roomId"]) < asString(rows[j]["roomId"]) })
	sort.Strings(degraded)
	return rows, degraded
}

func latestRoomMemoryArtifact(store *meetingMemoryStore, roomID, sittingID, kind string) (time.Time, bool) {
	if store == nil {
		return time.Time{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var latest time.Time
	for _, entry := range store.entries {
		if entry.Kind != kind || memoryEntryHiddenFromRecall(entry) || normalizeRoomID(entry.Metadata["roomId"]) != normalizeRoomID(roomID) {
			continue
		}
		if sittingID != "" && entry.Metadata["meetingId"] != sittingID {
			continue
		}
		if entry.CreatedAt.After(latest) {
			latest = entry.CreatedAt
		}
	}
	return latest, !latest.IsZero()
}

func roomArtifactHealth(at time.Time, found bool, now time.Time, staleAfter time.Duration) map[string]any {
	if !found {
		return map[string]any{"status": "missing", "freshness": "missing"}
	}
	age := now.Sub(at)
	if age < 0 {
		age = 0
	}
	status := "fresh"
	if staleAfter > 0 && age > staleAfter {
		status = "stale"
	}
	return map[string]any{
		"status": status, "freshness": status, "lastSuccessAt": at.UTC().Format(time.RFC3339Nano), "lagSeconds": int64(age.Seconds()),
	}
}

func roomConsentHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	return currentConsentLaneAuthority().Health(ctx)
}

func roomConsentPolicyVersion() string {
	authority := currentConsentLaneAuthority()
	if authority == nil {
		return "not_configured"
	}
	if policy := strings.TrimSpace(authority.PolicyVersion); policy != "" {
		return policy
	}
	return "not_configured"
}
