package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const roomAgentControlBodyLimit = 8 << 10

var (
	ErrRoomAgentControlScope   = errors.New("room agent control scope is unavailable")
	ErrRoomAgentControlAction  = errors.New("room agent control action is invalid")
	ErrRoomAgentConsentMissing = errors.New("room agent consent is unavailable")
	ErrRoomScoutVoiceDisabled  = errors.New("room Scout voice has not passed qualification")
	ErrRoomScoutModeInvalid    = errors.New("room Scout mode must be voice or text")
)

// Scout seat modes (Wave 6 D7). Voice is the qualified realtime lane; text
// seats Scout as a chat-only participant answering @Scout through the
// server-owned room-chat path with no realtime lane and no provider audio.
const (
	roomScoutModeVoice = "voice"
	roomScoutModeText  = "text"
)

func normalizeRoomScoutMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", roomScoutModeVoice:
		return roomScoutModeVoice, nil
	case roomScoutModeText:
		return roomScoutModeText, nil
	default:
		return "", ErrRoomScoutModeInvalid
	}
}

// roomAgentParticipant is the server-owned participant projection. Clients do
// not infer an agent from a track name or an invitation card: the room sitting
// publishes the exact agent identity, lifecycle and voice state that currently
// owns the provider session.
type roomAgentParticipant struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Kind                   string `json:"kind"`
	Color                  string `json:"color"`
	Mode                   string `json:"mode"`
	Status                 string `json:"status"`
	VoiceState             string `json:"voiceState"`
	InvitationID           string `json:"invitationId"`
	InvitedAt              string `json:"invitedAt"`
	InvitedBy              string `json:"invitedBy,omitempty"`
	Model                  string `json:"model,omitempty"`
	ProviderSessionStarted bool   `json:"providerSessionStarted"`
}

type roomScoutControlScope struct {
	RoomID          string
	SittingID       string
	MediaGeneration uint64
	Participants    []string
	ConsentFences   []ConsentFence
}

func canonicalParticipantSet(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = canonicalRoomParticipantName(value); value != "" {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func sameCanonicalParticipants(left, right []string) bool {
	left, right = canonicalParticipantSet(left), canonicalParticipantSet(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !sameParticipantName(left[index], right[index]) {
			return false
		}
	}
	return true
}

// resolveRoomScoutControlScope binds an invite to the current member room,
// sitting, media generation, exact audience and all three required consent
// lanes. Employees inherit the rules-of-the-road policy; external guests must
// have made explicit choices. An audience change or listen-only sitting fails
// closed.
func (app *kanbanBoardApp) resolveRoomScoutControlScope(ctx context.Context, user *userAccount, requestedRoomID string, requireConsent bool) (roomScoutControlScope, error) {
	if app == nil || user == nil || app.meetings == nil {
		return roomScoutControlScope{}, ErrRoomAgentControlScope
	}
	roomID, active := app.activeMemberConsentRoom(user.Email)
	if !active || normalizeRoomID(requestedRoomID) != roomID || app.sittingListenOnly(roomID) {
		return roomScoutControlScope{}, ErrRoomAgentControlScope
	}
	room, found := appRoomStore().byID(roomID)
	if !found || room.Archived {
		return roomScoutControlScope{}, ErrRoomAgentControlScope
	}
	meeting, active := app.meetings.activeRecord(roomID)
	if !active {
		return roomScoutControlScope{}, ErrRoomAgentControlScope
	}
	app.mu.Lock()
	live := app.roomLiveLocked(roomID)
	if live.mediaActor == nil || live.mediaGen == 0 || live.mediaSittingID != meeting.ID {
		app.mu.Unlock()
		return roomScoutControlScope{}, ErrRoomAgentControlScope
	}
	generation := live.mediaGen
	participants := app.participantSnapshotLocked(live)
	app.mu.Unlock()
	if len(participants) == 0 {
		return roomScoutControlScope{}, ErrRoomAgentControlScope
	}

	fences := make([]ConsentFence, 0, len(participants)*3)
	requesterPresent := false
	for _, name := range participants {
		principal, admitted := app.consentPrincipalForTranscriptSpeaker(roomID, name)
		if !admitted || (principal.Kind != string(ACLPrincipalUser) && principal.Kind != string(ACLPrincipalGuest)) {
			return roomScoutControlScope{}, ErrRoomAgentControlScope
		}
		if principal.Kind == string(ACLPrincipalUser) && normalizeAccountEmail(principal.ID) == normalizeAccountEmail(user.Email) {
			requesterPresent = true
		}
		if !requireConsent {
			continue
		}
		for _, lane := range []ConsentLane{ConsentLaneAudioCapture, ConsentLaneTranscription, ConsentLaneModelAnalysis} {
			decision, err := app.effectiveConsentLane(ctx, principal, roomID, meeting.ID, lane)
			if err != nil {
				return roomScoutControlScope{}, err
			}
			if !decision.Allowed {
				return roomScoutControlScope{}, ErrRoomAgentConsentMissing
			}
			fences = append(fences, decision.Fence)
		}
	}
	if !requesterPresent {
		return roomScoutControlScope{}, ErrRoomAgentControlScope
	}

	// Recheck the audience under its owner lock after the consent reads. A join
	// or leave is wholly before this comparison (and changes the set) or after it
	// (and synchronously revokes the invitation through the existing authority
	// hook).
	app.mu.Lock()
	live = app.roomLiveLocked(roomID)
	current := live.mediaActor != nil && live.mediaGen == generation && live.mediaSittingID == meeting.ID && sameCanonicalParticipants(participants, app.participantSnapshotLocked(live))
	app.mu.Unlock()
	if !current {
		return roomScoutControlScope{}, ErrRoomAgentControlScope
	}
	return roomScoutControlScope{RoomID: roomID, SittingID: meeting.ID, MediaGeneration: generation, Participants: canonicalParticipantSet(participants), ConsentFences: fences}, nil
}

func (app *kanbanBoardApp) roomScoutInvitationCurrentLocked(scope RoomScoutScope) bool {
	if app == nil || !scope.valid() {
		return false
	}
	state := app.roomLiveLocked(scope.RoomID)
	return state.scoutInvited && state.mediaActor != nil && state.mediaGen == scope.MediaGeneration && state.mediaSittingID == strings.TrimSpace(scope.SittingID)
}

func (app *kanbanBoardApp) officeRoomScoutScopeForGeneration(generation uint64) (RoomScoutScope, bool) {
	if app == nil || generation == 0 {
		return RoomScoutScope{}, false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(officeRoomID)
	scope := RoomScoutScope{RoomID: officeRoomID, SittingID: state.mediaSittingID, MediaGeneration: generation}
	return scope, app.roomScoutInvitationCurrentLocked(scope)
}

func (app *kanbanBoardApp) inviteRoomScout(ctx context.Context, user *userAccount, requestedRoomID string) ([]roomAgentParticipant, error) {
	return app.inviteRoomScoutWithMode(ctx, user, requestedRoomID, roomScoutModeVoice)
}

// inviteRoomScoutWithMode seats Scout in one of two modes. Voice is gated by
// the physical-meeting qualification and every participant's three consent
// lanes because it opens a provider audio session. Text opens nothing new: it
// is the same server-owned @Scout room-chat turn members already have, made
// visible as a roster seat, so it needs only the control scope (a current
// member in an active, non-listen-only sitting). A text seat upgrades to voice
// when a qualified voice invite follows; a voice seat already answers text.
func (app *kanbanBoardApp) inviteRoomScoutWithMode(ctx context.Context, user *userAccount, requestedRoomID string, requestedMode string) ([]roomAgentParticipant, error) {
	mode, err := normalizeRoomScoutMode(requestedMode)
	if err != nil {
		return nil, err
	}
	if mode == roomScoutModeVoice && !currentRoomScoutVoiceAvailability().Enabled {
		return nil, ErrRoomScoutVoiceDisabled
	}
	control, err := app.resolveRoomScoutControlScope(ctx, user, requestedRoomID, mode == roomScoutModeVoice)
	if err != nil {
		return nil, err
	}
	scope := RoomScoutScope{RoomID: control.RoomID, SittingID: control.SittingID, MediaGeneration: control.MediaGeneration}
	now := time.Now().UTC()
	app.mu.Lock()
	state := app.roomLiveLocked(control.RoomID)
	if state.mediaActor == nil || state.mediaGen != control.MediaGeneration || state.mediaSittingID != control.SittingID || !sameCanonicalParticipants(control.Participants, app.participantSnapshotLocked(state)) {
		app.mu.Unlock()
		return nil, ErrRoomAgentControlScope
	}
	if !state.scoutInvited {
		state.scoutInvited = true
		state.scoutInvitationID = durableTimestampID("scout-invite", now)
		state.scoutInvitedAt = now
		state.scoutInvitedBy = canonicalRoomActorName(user.Name)
		state.scoutConsentFences = append([]ConsentFence(nil), control.ConsentFences...)
		state.scoutMode = mode
		state.scoutLastStatusReason = ""
		if mode == roomScoutModeText {
			state.scoutRuntimeStatus = RoomScoutReady
			state.scoutVoiceState = "off"
		} else {
			state.scoutRuntimeStatus = RoomScoutStarting
			state.scoutVoiceState = "starting"
		}
	} else if state.scoutMode == roomScoutModeText && mode == roomScoutModeVoice {
		// Upgrade the chat-only seat: same invitation, now with the consent
		// fences the voice lane requires and a starting runtime.
		state.scoutMode = roomScoutModeVoice
		state.scoutConsentFences = append([]ConsentFence(nil), control.ConsentFences...)
		state.scoutRuntimeStatus = RoomScoutStarting
		state.scoutVoiceState = "starting"
		state.scoutLastStatusReason = ""
	}
	startVoice := state.scoutMode == roomScoutModeVoice
	apiKey := app.apiKey
	participants := app.roomAgentParticipantsLocked(control.RoomID, state)
	app.mu.Unlock()

	broadcastRoomKanbanEvent(control.RoomID, "agent_participants", participants)
	if !startVoice {
		// Text mode: no realtime lane, no provider audio. @Scout in room chat
		// already routes through submitRoomScoutTextMention.
		return participants, nil
	}
	if control.RoomID == officeRoomID {
		if strings.TrimSpace(apiKey) == "" {
			app.updateRoomScoutParticipantState(control.RoomID, RoomScoutDegraded, "degraded", "Realtime provider is not configured")
		} else {
			go app.ensureOfficeRealtimePeer(apiKey)
		}
	} else {
		app.ensureRoomScoutRuntime(scope.RoomID, scope.SittingID, scope.MediaGeneration)
	}
	return participants, nil
}

func (app *kanbanBoardApp) dismissRoomScout(roomID, sittingID, reason string) []roomAgentParticipant {
	if app == nil {
		return nil
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if sittingID != "" && state.mediaSittingID != strings.TrimSpace(sittingID) {
		participants := app.roomAgentParticipantsLocked(roomID, state)
		app.mu.Unlock()
		return participants
	}
	bundle := state.realtime
	state.realtime = nil
	wasInvited := state.scoutInvited
	state.scoutInvited = false
	state.scoutInvitationID = ""
	state.scoutInvitedAt = time.Time{}
	state.scoutInvitedBy = ""
	state.scoutConsentFences = nil
	state.scoutMode = ""
	state.scoutRuntimeStatus = RoomScoutClosed
	state.scoutVoiceState = ""
	state.scoutLastStatusReason = trimForStorage(reason, 160)
	if roomID == officeRoomID {
		app.realtimeStartToken++
		app.realtimeStarting = false
		app.realtimeRestartToken = 0
	}
	app.mu.Unlock()

	if bundle != nil {
		_ = bundle.close()
	}
	if roomID == officeRoomID {
		app.teardownRealtimePeerForIdle()
	}
	if wasInvited {
		broadcastRoomKanbanEvent(roomID, "agent_participants", []roomAgentParticipant{})
	}
	return nil
}

func (app *kanbanBoardApp) revokeRoomScoutParticipantAuthority(roomID, reason string) {
	if app == nil {
		return
	}
	app.dismissRoomScout(roomID, "", reason)
}

func (app *kanbanBoardApp) roomAgentParticipantsLocked(roomID string, state *roomLiveState) []roomAgentParticipant {
	if state == nil || !state.scoutInvited {
		return []roomAgentParticipant{}
	}
	status := strings.TrimSpace(string(state.scoutRuntimeStatus))
	if status == "" {
		status = string(RoomScoutStarting)
	}
	voiceState := strings.TrimSpace(state.scoutVoiceState)
	if voiceState == "" {
		voiceState = "starting"
	}
	mode := state.scoutMode
	if mode == "" {
		mode = roomScoutModeVoice
	}
	if mode == roomScoutModeText {
		// A chat-only seat has no provider session to report on: it is ready
		// the moment it is seated, and its voice lane is honestly "off".
		return []roomAgentParticipant{{
			ID: "scout", Name: scoutParticipantName, Kind: "scout", Color: "#FF6B35", Mode: mode,
			Status: string(RoomScoutReady), VoiceState: "off", InvitationID: state.scoutInvitationID,
			InvitedAt: state.scoutInvitedAt.Format(time.RFC3339Nano), InvitedBy: state.scoutInvitedBy,
			Model: scoutChatModel(), ProviderSessionStarted: false,
		}}
	}
	return []roomAgentParticipant{{
		ID: "scout", Name: scoutParticipantName, Kind: "scout", Color: "#FF6B35", Mode: mode,
		Status: status, VoiceState: voiceState, InvitationID: state.scoutInvitationID,
		InvitedAt: state.scoutInvitedAt.Format(time.RFC3339Nano), InvitedBy: state.scoutInvitedBy,
		Model: realtimeModel(), ProviderSessionStarted: state.scoutRuntimeStatus == RoomScoutReady,
	}}
}

// roomScoutModeSnapshot reports the current seat mode ("" when not invited).
func (app *kanbanBoardApp) roomScoutModeSnapshot(roomID string) string {
	if app == nil {
		return ""
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if !state.scoutInvited {
		return ""
	}
	if state.scoutMode == "" {
		return roomScoutModeVoice
	}
	return state.scoutMode
}

func (app *kanbanBoardApp) roomAgentParticipantsSnapshot(roomID string) []roomAgentParticipant {
	if app == nil {
		return []roomAgentParticipant{}
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	participants := app.roomAgentParticipantsLocked(roomID, app.roomLiveLocked(roomID))
	app.mu.Unlock()
	return participants
}

func roomScoutVoiceState(payload any) string {
	data, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	switch state := strings.ToLower(strings.TrimSpace(asString(data["voiceState"]))); state {
	case "starting", "listening", "hearing", "thinking", "talking", "degraded":
		return state
	default:
		return ""
	}
}

func (app *kanbanBoardApp) publishRoomScoutParticipantEvent(scope RoomScoutScope, event string, payload any) {
	publishRoomScoutAssistantEvent(scope.RoomID, event, payload)
	voiceState := roomScoutVoiceState(payload)
	status := RoomScoutStatus("")
	reason := ""
	switch strings.TrimSpace(event) {
	case "error":
		status, voiceState = RoomScoutDegraded, "degraded"
		reason = asString(payload)
	case "status":
		if voiceState == "listening" {
			status = RoomScoutReady
		} else if voiceState == "thinking" {
			status = RoomScoutDegraded
		}
	case "audio", "answer", "transcript":
		status = RoomScoutReady
	}
	app.mu.Lock()
	if !app.roomScoutInvitationCurrentLocked(scope) {
		app.mu.Unlock()
		return
	}
	state := app.roomLiveLocked(scope.RoomID)
	changed := false
	if status != "" && state.scoutRuntimeStatus != status {
		state.scoutRuntimeStatus = status
		changed = true
	}
	if voiceState != "" && state.scoutVoiceState != voiceState {
		state.scoutVoiceState = voiceState
		changed = true
	}
	if reason != "" {
		state.scoutLastStatusReason = trimForStorage(reason, 160)
	}
	participants := app.roomAgentParticipantsLocked(scope.RoomID, state)
	app.mu.Unlock()
	if changed {
		broadcastRoomKanbanEvent(scope.RoomID, "agent_participants", participants)
	}
}

func (app *kanbanBoardApp) syncRoomScoutParticipantStatus(scope RoomScoutScope, bundle *roomRealtimeBundle) {
	if bundle == nil {
		return
	}
	snapshot := bundle.snapshot()
	voiceState := "listening"
	if snapshot.Status == RoomScoutStarting {
		voiceState = "starting"
	} else if snapshot.Status == RoomScoutDegraded {
		voiceState = "degraded"
	}
	app.mu.Lock()
	if !app.roomScoutInvitationCurrentLocked(scope) || app.roomLiveLocked(scope.RoomID).realtime != bundle {
		app.mu.Unlock()
		return
	}
	state := app.roomLiveLocked(scope.RoomID)
	state.scoutRuntimeStatus = snapshot.Status
	state.scoutVoiceState = voiceState
	state.scoutLastStatusReason = snapshot.LastError
	participants := app.roomAgentParticipantsLocked(scope.RoomID, state)
	app.mu.Unlock()
	broadcastRoomKanbanEvent(scope.RoomID, "agent_participants", participants)
}

func (app *kanbanBoardApp) updateRoomScoutParticipantState(roomID string, status RoomScoutStatus, voiceState, reason string) {
	if app == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	if !state.scoutInvited {
		app.mu.Unlock()
		return
	}
	if status != "" {
		state.scoutRuntimeStatus = status
	}
	if voiceState != "" {
		state.scoutVoiceState = voiceState
	}
	state.scoutLastStatusReason = trimForStorage(reason, 160)
	participants := app.roomAgentParticipantsLocked(roomID, state)
	app.mu.Unlock()
	broadcastRoomKanbanEvent(roomID, "agent_participants", participants)
}

func registerRoomAgentRoutes(mux *http.ServeMux) {
	if mux != nil {
		mux.HandleFunc("/api/rooms/agents/scout", roomScoutControlHandler)
	}
}

func roomScoutControlHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "room agents are unavailable")
		return
	}
	roomID := r.URL.Query().Get("roomId")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		activeRoomID, ok := kanbanApp.activeMemberConsentRoom(user.Email)
		if !ok || normalizeRoomID(roomID) != activeRoomID {
			writeAuthError(w, http.StatusForbidden, "room agent status was not authorized")
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "agents": kanbanApp.roomAgentParticipantsSnapshot(activeRoomID), "voice": currentRoomScoutVoiceAvailability(), "scoutText": currentRoomScoutTextAvailability()})
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var payload struct {
		RoomID string `json:"roomId"`
		Action string `json:"action"`
		// Mode selects the seat kind on invite: "voice" (default, qualified
		// realtime lane) or "text" (chat-only, allowed while voice is off).
		Mode string `json:"mode"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, roomAgentControlBodyLimit))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || ensureJSONEOF(decoder) != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid room agent request")
		return
	}
	roomID = normalizeRoomID(payload.RoomID)
	var agents []roomAgentParticipant
	var err error
	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "invite":
		agents, err = kanbanApp.inviteRoomScoutWithMode(r.Context(), user, roomID, payload.Mode)
	case "dismiss":
		if _, resolveErr := kanbanApp.resolveRoomScoutControlScope(r.Context(), user, roomID, false); resolveErr != nil {
			err = resolveErr
		} else {
			agents = kanbanApp.dismissRoomScout(roomID, "", "dismissed_by_participant")
		}
	default:
		err = ErrRoomAgentControlAction
	}
	if err != nil {
		status := http.StatusForbidden
		message := "Only a current member participant can control Scout during an active call."
		if errors.Is(err, ErrRoomAgentControlAction) {
			status = http.StatusBadRequest
			message = "Choose invite or dismiss for Scout."
		} else if errors.Is(err, ErrRoomScoutModeInvalid) {
			status = http.StatusBadRequest
			message = "Choose voice or text for Scout's seat."
		} else if errors.Is(err, ErrRoomAgentConsentMissing) {
			message = "Every current participant must allow microphone capture, transcription, and AI analysis before Scout can join."
		} else if errors.Is(err, ErrConsentAuthorityUnavailable) {
			status = http.StatusServiceUnavailable
			message = "Consent choices are temporarily unavailable, so Scout cannot join yet."
		} else if errors.Is(err, ErrRoomScoutVoiceDisabled) {
			status = http.StatusServiceUnavailable
			message = "In-room Scout voice is unavailable until it passes the physical meeting quality gate. Meeting transcription and @Scout chat remain available."
		}
		writeAuthError(w, status, message)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "agents": agents, "voice": currentRoomScoutVoiceAvailability(), "scoutText": currentRoomScoutTextAvailability()})
}

// rememberRoomAgentTranscript commits provider-authored speech into the same
// durable transcript as human speech, attributed to the exact agent. It does
// not feed agent audio back through STT, avoiding duplicate text, billing and
// agent-to-agent audio loops. The invitation's complete human audience fences
// remain the authority for the derived output.
func (app *kanbanBoardApp) rememberRoomAgentTranscript(scope RoomScoutScope, event kanbanRealtimeEvent, source, model, agentID, speaker string) {
	app.rememberRoomAgentTranscriptForEpoch(scope, event, source, model, agentID, speaker, 0)
}

func (app *kanbanBoardApp) rememberRoomAgentTranscriptForEpoch(scope RoomScoutScope, event kanbanRealtimeEvent, source, model, agentID, speaker string, expectedRecordingEpoch uint64) {
	text := canonicalizeBoardText(firstNonEmptyString(event.Transcript, event.Text))
	if app == nil || app.memory == nil || text == "" || !app.transcriptRecordingActiveInRoom(scope.RoomID) {
		return
	}
	app.mu.Lock()
	if !app.roomScoutInvitationCurrentLocked(scope) {
		app.mu.Unlock()
		return
	}
	state := app.roomLiveLocked(scope.RoomID)
	if expectedRecordingEpoch != 0 && state.recordingEpoch != expectedRecordingEpoch {
		app.mu.Unlock()
		return
	}
	fences := append([]ConsentFence(nil), state.scoutConsentFences...)
	invitationID := state.scoutInvitationID
	app.mu.Unlock()
	bindings := durableConsentContributorBindings(fences)
	contributorsJSON, err := encodeConsentContributorBindings(bindings)
	if err != nil || len(bindings) == 0 {
		return
	}
	meeting, active := app.meetings.activeRecord(scope.RoomID)
	if !active || meeting.ID != scope.SittingID {
		return
	}
	metadata := map[string]string{
		"source": source, "model": model, "speakerKind": "agent", "agentId": agentID,
		"speaker":           speaker,
		"agentInvitationId": invitationID, "mediaGeneration": strconv.FormatUint(scope.MediaGeneration, 10),
		consentContributorBindingsMetadataKey: contributorsJSON,
	}
	var entry meetingMemoryEntry
	var appended bool
	err = currentConsentLaneAuthority().CommitWithFences(context.Background(), fences, func() error {
		app.mu.Lock()
		defer app.mu.Unlock()
		if !app.roomScoutInvitationCurrentLocked(scope) || app.roomLiveLocked(scope.RoomID).scoutInvitationID != invitationID {
			return ErrRoomScoutFence
		}
		if expectedRecordingEpoch != 0 && app.roomLiveLocked(scope.RoomID).recordingEpoch != expectedRecordingEpoch {
			return ErrRoomScoutFence
		}
		var appendErr error
		// Agent identities are service principals rather than roster display
		// names, so the ordinary human-speaker normalizer intentionally rejects
		// them. Stamp the server-owned speaker explicitly and pass an empty human
		// speaker to avoid converting a service identity into a user authority.
		entry, appended, appendErr = app.memory.appendAttributedTranscriptEntry(scope.RoomID, event.EventID, event.ItemID, "", "", speaker+": "+text, metadata, true, meeting.ID)
		return appendErr
	})
	if err != nil || !appended {
		return
	}
	app.broadcastCurrentMeetingTranscript(scope.RoomID, entry)
	if _, consentErr := (appBrainSourceConsentVerifier{App: app}).AuthorizeBrainSourceConsent(context.Background(), entry); consentErr == nil {
		app.nudgeAmbientAgentForRoom(meetingBrainAgentName, scope.RoomID)
		if err := app.projectSTRIDEAuthoritativeTranscript(context.Background(), meeting, entry); err != nil {
			log.Infof("STRIDE agent transcript projection unavailable: %v", err)
		}
	}
}
