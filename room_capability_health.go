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
	scoutInvited    bool
	scoutMode       string

	// Transcript capture census (Fix 1). Every silently dropped frame on the
	// audio -> transcription-lane hop is counted by reason and published here,
	// so the next occurrence of the 2026-09-02 blackout names its own cause in
	// one query instead of a three-agent forensic sweep.
	framesOffered  uint64
	framesAccepted uint64
	frameDrops     map[string]uint64
	lastFrameAt    time.Time
	lastIngressAt  time.Time
	lastDropAt     time.Time
	lastDropReason string
	lastCommitAt   time.Time
	stalledSince   time.Time
	stallReason    string
}

// officeMixerAttachedForGeneration answers the office's version of the
// question the media row asks every other room: is this room's audio wired to
// a mixer for THIS sitting?
//
// state.mixer is nil for the office by design — ensureOfficeMedia uses the
// boot-owned shared roomMixer instead of minting a per-room one — so the plain
// `state.mixer != nil` test made rooms.office.media degraded for the entire
// duration of every office sitting, with nothing wrong. That permanent false
// alarm is precisely the noise a real transcript hole hid inside.
//
// `roomMixer != nil` is NOT the fix: main() assigns it once at boot and never
// nils it, so it would answer true forever and trade a false alarm for a green
// lie — the worse of the two errors. The truthful signal is the wiring
// ensureOfficeMedia itself installs on every admission: the room- AND
// generation-scoped audio activity listener. If the shared mixer is feeding
// some other generation's attribution, or the legacy boot listener, or
// nothing, then the office really is not being mixed for this sitting and
// amber is the honest answer. It is independent of the AI plane (no API key,
// lane or Scout is involved), which keeps human media independent of AI
// health.
//
// Takes only mixer.mu, so callers must hold no app.mu: that preserves the
// app.mu -> mixer.mu order the media paths already use.
func officeMixerAttachedForGeneration(generation uint64) bool {
	if roomMixer == nil || generation == 0 {
		return false
	}
	select {
	case <-roomMixer.stop:
		return false
	default:
	}
	roomMixer.mu.Lock()
	listener := roomMixer.activityListener
	roomMixer.mu.Unlock()
	office, ok := listener.(*roomAudioActivityListener)
	return ok && normalizeRoomID(office.roomID) == officeRoomID && office.generation == generation
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
		drops := make(map[string]uint64, len(state.transcriptFrameDrops))
		for reason, count := range state.transcriptFrameDrops {
			drops[reason] = count
		}
		pointers = append(pointers, roomOperationalPointers{
			roomID: normalizeRoomID(roomID), sittingID: state.mediaSittingID, mediaGeneration: state.mediaGen,
			participants: len(state.participants), recording: state.recordingEnabled,
			media: state.mediaActor != nil, mixer: state.mixer != nil, lane: lane, scout: state.realtime,
			scoutInvited: state.scoutInvited, scoutMode: state.scoutMode,
			framesOffered: state.transcriptFramesOffered, framesAccepted: state.transcriptFramesAccepted,
			frameDrops: drops, lastFrameAt: state.lastTranscriptFrameAt, lastIngressAt: state.lastAudioIngressAt,
			lastDropAt:     state.lastTranscriptDropAt,
			lastDropReason: state.lastTranscriptDropReason, lastCommitAt: state.lastTranscriptCommitAt,
			stalledSince: state.captureStalledSince, stallReason: state.captureStallReason,
		})
	}
	app.mu.Unlock()

	// Resolved after the unlock: officeMixerAttachedForGeneration takes
	// mixer.mu, and app.mu -> mixer.mu is the order the media paths use.
	for index := range pointers {
		if pointers[index].roomID == officeRoomID {
			pointers[index].mixer = officeMixerAttachedForGeneration(pointers[index].mediaGeneration)
		}
	}

	rows := make([]map[string]any, 0, len(pointers))
	degraded := []string{}
	for _, pointer := range pointers {
		active := pointer.media || pointer.participants > 0
		row := map[string]any{
			"roomId": pointer.roomID, "sittingId": pointer.sittingID, "mediaGeneration": pointer.mediaGeneration,
			"participants": pointer.participants, "recordingEnabled": pointer.recording,
			// Top-level so the incident query is one jq filter:
			//   .capabilities.rooms[] | select(.roomId=="office")
			//     | {transcriptFramesOffered, transcriptFramesAccepted,
			//        transcriptFrameDrops, lastTranscriptFrameAt}
			"transcriptFramesOffered": pointer.framesOffered, "transcriptFramesAccepted": pointer.framesAccepted,
			"transcriptFrameDrops": pointer.frameDrops,
		}
		// Same liveness question, same answer, as the watchdog's: audio seen at
		// EITHER seam — handed to the mixer, or refused upstream of it.
		offering := transcriptAudioLive(pointer.lastFrameAt, pointer.lastIngressAt, now)
		if !pointer.lastFrameAt.IsZero() {
			row["lastTranscriptFrameAt"] = pointer.lastFrameAt.UTC().Format(time.RFC3339Nano)
		}
		if !pointer.lastIngressAt.IsZero() {
			row["lastBlockedAudioAt"] = pointer.lastIngressAt.UTC().Format(time.RFC3339Nano)
		}
		if !pointer.lastDropAt.IsZero() {
			row["lastTranscriptDropAt"] = pointer.lastDropAt.UTC().Format(time.RFC3339Nano)
			row["lastTranscriptDropReason"] = pointer.lastDropReason
		}
		if !pointer.lastCommitAt.IsZero() {
			row["lastTranscriptCommitAt"] = pointer.lastCommitAt.UTC().Format(time.RFC3339Nano)
		}
		row["transcriptCapturing"] = pointer.stalledSince.IsZero()
		if !pointer.stalledSince.IsZero() {
			row["transcriptStalledSince"] = pointer.stalledSince.UTC().Format(time.RFC3339Nano)
			row["transcriptStallReason"] = pointer.stallReason
			row["transcriptStalledSeconds"] = int64(now.Sub(pointer.stalledSince).Seconds())
		}
		// `mixer` means "this room's audio is being mixed for this sitting":
		// the room's own lazily-created mixer for a named room, the shared
		// mixer's room+generation wiring for the office (see
		// officeMixerAttachedForGeneration).
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
		// Guardrail: this freshness was computed and thrown away. A live,
		// occupied, recording-enabled room with a 34-minute-stale transcript
		// reported room-level OK for the whole blackout because nothing ever
		// appended it to the degraded slice. Now it escalates.
		//
		// The stale arm carries the SAME evidence the watchdog requires —
		// audio arriving at one of the two seams — so this means "audio is
		// flowing and nothing is landing", never "nobody has spoken in five
		// minutes". Without that clause a merely quiet meeting flipped /readyz
		// to ok:false, which is the false alarm the watchdog was designed to
		// avoid. A stall the watchdog has already vetted escalates on its own
		// evidence and needs no second look at the audio.
		if active && pointer.recording && (!pointer.stalledSince.IsZero() || (offering && asString(transcript["status"]) == "stale")) {
			transcript["status"] = "stale"
			if !pointer.stalledSince.IsZero() {
				transcript["stalledSince"] = pointer.stalledSince.UTC().Format(time.RFC3339Nano)
				transcript["stallReason"] = pointer.stallReason
			}
			degraded = append(degraded, "rooms."+pointer.roomID+".transcript")
		}
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
			// Guardrail: isConnected() proves a websocket is open and nothing
			// more. A connected lane that has landed nothing for 45s while the
			// mixer is still offering it audio is not healthy, it is starving.
			if !pointer.stalledSince.IsZero() {
				stt["status"] = "degraded"
				stt["capture"] = "stalled"
				stt["stallReason"] = pointer.stallReason
			} else if active && pointer.recording && offering && !pointer.lastCommitAt.IsZero() &&
				now.Sub(pointer.lastCommitAt) > transcriptCaptureStallAfter {
				stt["status"] = "degraded"
				stt["capture"] = "stalling"
			}
			if !pointer.lastCommitAt.IsZero() {
				stt["lastCommitAt"] = pointer.lastCommitAt.UTC().Format(time.RFC3339Nano)
				stt["commitLagSeconds"] = int64(now.Sub(pointer.lastCommitAt).Seconds())
			}
			stt["audioOffered"] = offering
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
		} else if active && pointer.scoutInvited {
			// An INVITED Scout with no runtime is a real fault.
			scout["status"] = "degraded"
		} else if active {
			// An uninvited Scout is absent BY DESIGN, not broken: ordinary
			// admission must never silently add an AI participant, and
			// ensureOfficeRealtimePeer early-returns without an invitation. It
			// was degrading rooms.<id>.scout on every active room in the
			// process — the second permanent false alarm in this row set, and
			// half the noise the real hole hid inside.
			scout["status"] = "not_invited"
		}
		// A provider outage is a room fault only where a Scout was actually
		// asked for. `scout != nil` covers a seat mid-teardown whose
		// invitation flag has already been cleared.
		if !providerReady && active && (pointer.scoutInvited || pointer.scout != nil) {
			scout["status"] = "degraded"
			scout["provider"] = providerOpenAI
		}
		if pointer.scoutInvited {
			scout["invited"] = true
			scout["mode"] = pointer.scoutMode
			if pointer.scoutMode == roomScoutModeText {
				// A chat-only seat has no voice runtime by design; the voice
				// row stays whatever the lane truthfully is, and the text
				// seat reports below.
				scout["voiceSeat"] = false
			}
		}
		if scout["status"] == "degraded" && active {
			degraded = append(degraded, "rooms."+pointer.roomID+".scout")
		}
		row["scout"] = scout
		// Wave 6 D7: the chat-only Scout seat is reported separately from the
		// voice lane. It needs only the typed provider path; the voice
		// qualification gate never demotes it.
		scoutText := currentRoomScoutTextAvailability().snapshot()
		scoutText["invited"] = pointer.scoutInvited && pointer.scoutMode == roomScoutModeText
		row["scoutText"] = scoutText
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
