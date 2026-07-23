package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const consentRequestBodyLimit = 4 << 10

type consentDecisionRequest struct {
	Scope       ConsentScope       `json:"scope"`
	Disposition ConsentDisposition `json:"disposition"`
}

type consentLaneStatus struct {
	Allowed       bool                    `json:"allowed"`
	MissingScopes []ConsentScope          `json:"missingScopes,omitempty"`
	RecordIDs     map[ConsentScope]string `json:"recordIds,omitempty"`
}

type consentStatusResponse struct {
	PolicyVersion         string                              `json:"policyVersion"`
	PrincipalKind         ACLPrincipalKind                    `json:"principalKind"`
	RoomID                string                              `json:"roomId"`
	SittingID             string                              `json:"sittingId"`
	GuestPolicyListenOnly bool                                `json:"guestPolicyListenOnly"`
	StoreAvailable        bool                                `json:"storeAvailable"`
	Lanes                 map[ConsentLane]consentLaneStatus   `json:"lanes"`
	Scopes                map[ConsentScope]ConsentDisposition `json:"scopes"`
}

// consentHandler is a current-admission surface. The body contains no
// principal, room, sitting, policy, evidence, timestamp, record ID, or capture
// cutoff field; unknown fields are rejected so a client cannot self-attest
// authority by smuggling one alongside its explicit choice.
func consentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Method == http.MethodPost && !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "room state is unavailable")
		return
	}
	binding, err := kanbanApp.consentBindingFromRequest(r)
	if err != nil {
		status := http.StatusForbidden
		if errors.Is(err, ErrConsentUnauthenticated) {
			status = http.StatusUnauthorized
		} else if errors.Is(err, ErrConsentAuthorityUnavailable) || errors.Is(err, ErrAdmissionAnchorStore) {
			status = http.StatusServiceUnavailable
		}
		writeAuthError(w, status, "an active durable room admission is required")
		return
	}
	authority := currentConsentLaneAuthority()
	if r.Method == http.MethodPost {
		var request consentDecisionRequest
		if err := decodeStrictConsentRequest(r, &request); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read consent decision")
			return
		}
		record, err := authority.RecordDecision(r.Context(), binding, request.Scope, request.Disposition)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ErrConsentAuthorityUnavailable) || errors.Is(err, ErrCanonicalStoreUnhealthy) {
				status = http.StatusServiceUnavailable
			}
			writeAuthError(w, status, "consent decision could not be persisted")
			return
		}
		response := kanbanApp.consentStatus(r.Context(), authority, binding)
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"recordId": record.ID, "recordedAt": record.RecordedAt,
			"lastAcceptedCaptureSequence": record.LastAcceptedCaptureSequence,
			"consent":                     response,
		})
		return
	}
	writeAuthJSON(w, http.StatusOK, kanbanApp.consentStatus(r.Context(), authority, binding))
}

func decodeStrictConsentRequest(r *http.Request, destination *consentDecisionRequest) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, consentRequestBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func (app *kanbanBoardApp) consentStatus(ctx context.Context, authority *ConsentLaneAuthority, binding ConsentAdmissionBinding) consentStatusResponse {
	response := consentStatusResponse{
		PolicyVersion: authority.PolicyVersion, PrincipalKind: binding.PrincipalKind,
		RoomID: binding.RoomID, SittingID: binding.SittingID,
		GuestPolicyListenOnly: binding.GuestPolicyListenOnly,
		StoreAvailable:        authority.Health(ctx) == nil,
		Lanes:                 make(map[ConsentLane]consentLaneStatus),
		Scopes:                make(map[ConsentScope]ConsentDisposition),
	}
	for _, lane := range []ConsentLane{
		ConsentLaneAudioTransport, ConsentLaneAudioCapture, ConsentLaneTranscription,
		ConsentLaneModelAnalysis, ConsentLaneOrgMemory,
	} {
		decision, err := authority.Authorize(ctx, binding, lane)
		if err != nil {
			response.Lanes[lane] = consentLaneStatus{Allowed: false}
			continue
		}
		response.Lanes[lane] = consentLaneStatus{
			Allowed: decision.Allowed, MissingScopes: decision.MissingScopes,
			RecordIDs: decision.RecordIDs,
		}
		for scope, disposition := range decision.Dispositions {
			response.Scopes[scope] = disposition
		}
	}
	return response
}

// effectiveConsentLane is the server-stamped adapter seam for transcript,
// model, and company-brain writers. It resolves the durable admission anchor
// itself from principal/room/sitting and then re-reads effective PostgreSQL
// consent. The caller supplies identity facts, never a consent boolean.
func (app *kanbanBoardApp) effectiveConsentLane(ctx context.Context, principal CanonicalPrincipalRef, roomID, sittingID string, lane ConsentLane) (ConsentLaneDecision, error) {
	binding, err := app.consentBindingForPrincipal(ctx, principal, roomID, sittingID)
	if err != nil {
		return ConsentLaneDecision{Lane: lane}, err
	}
	return currentConsentLaneAuthority().Authorize(ctx, binding, lane)
}

func (app *kanbanBoardApp) consentBindingForPrincipal(ctx context.Context, principal CanonicalPrincipalRef, roomID, sittingID string) (ConsentAdmissionBinding, error) {
	if app == nil {
		return ConsentAdmissionBinding{}, ErrConsentAdmissionInvalid
	}
	roomID = normalizeRoomID(roomID)
	sittingID = strings.TrimSpace(sittingID)
	principal.Kind = strings.TrimSpace(principal.Kind)
	principal.ID = strings.TrimSpace(principal.ID)
	var principalKind ACLPrincipalKind
	switch principal.Kind {
	case "user":
		principalKind = ACLPrincipalUser
		principal.ID = normalizeAccountEmail(principal.ID)
	case "guest":
		principalKind = ACLPrincipalGuest
	default:
		return ConsentAdmissionBinding{}, ErrConsentAdmissionInvalid
	}
	app.admissionAnchorMu.RLock()
	anchorStore := app.admissionAnchors
	anchorErr := app.admissionAnchorErr
	app.admissionAnchorMu.RUnlock()
	if anchorErr != nil || anchorStore == nil {
		return ConsentAdmissionBinding{}, ErrAdmissionAnchorStore
	}
	anchor, found, err := anchorStore.Lookup(ctx, canonicalTenantID(), roomID, sittingID, principal)
	if err != nil {
		return ConsentAdmissionBinding{}, err
	}
	if !found {
		return ConsentAdmissionBinding{}, ErrConsentAdmissionInvalid
	}
	binding := ConsentAdmissionBinding{
		TenantID: canonicalTenantID(), PrincipalKind: principalKind, PrincipalID: principal.ID,
		RoomID: roomID, SittingID: sittingID, AnchorID: anchor.AnchorID,
		GuestPolicyListenOnly: app.meetingListenOnly(sittingID),
	}
	if err := binding.Validate(); err != nil {
		return ConsentAdmissionBinding{}, err
	}
	return binding, nil
}

func (app *kanbanBoardApp) consentBindingFromRequest(r *http.Request) (ConsentAdmissionBinding, error) {
	member := userFromRequest(r)
	var guest *guestPrincipal
	if r.URL.Query().Get("as") == "guest" {
		member = nil
		guest = guestFromRequest(r)
	} else if member == nil {
		guest = guestFromRequest(r)
	}
	if member == nil && guest == nil {
		return ConsentAdmissionBinding{}, ErrConsentUnauthenticated
	}
	if guest != nil {
		roomID, ok := app.activeGuestConsentRoom(guest.SessionKey, guest.RoomID)
		if !ok {
			return ConsentAdmissionBinding{}, ErrConsentAdmissionInvalid
		}
		record, ok := app.meetings.activeRecord(roomID)
		if !ok {
			return ConsentAdmissionBinding{}, ErrConsentAdmissionInvalid
		}
		return app.consentBindingForPrincipal(r.Context(), guestAdmissionPrincipal(guest.SessionKey), roomID, record.ID)
	}
	roomID, ok := app.activeMemberConsentRoom(member.Email)
	if !ok {
		return ConsentAdmissionBinding{}, ErrConsentAdmissionInvalid
	}
	record, ok := app.meetings.activeRecord(roomID)
	if !ok {
		return ConsentAdmissionBinding{}, ErrConsentAdmissionInvalid
	}
	return app.consentBindingForPrincipal(r.Context(), memberAdmissionPrincipal(member.Email), roomID, record.ID)
}

func (app *kanbanBoardApp) activeMemberConsentRoom(email string) (string, bool) {
	name := participantNameForEmail(email)
	if app == nil || name == "" {
		return "", false
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	roomID := ""
	for candidate, state := range app.roomLive {
		if state.participantCounts[name] <= 0 {
			continue
		}
		if roomID != "" && roomID != candidate {
			return "", false
		}
		roomID = candidate
	}
	return roomID, roomID != ""
}

func (app *kanbanBoardApp) activeGuestConsentRoom(sessionKey, expectedRoomID string) (string, bool) {
	if app == nil || !isHexDigest(sessionKey) {
		return "", false
	}
	expectedRoomID = normalizeRoomID(expectedRoomID)
	app.mu.Lock()
	defer app.mu.Unlock()
	state, ok := app.roomLive[expectedRoomID]
	if !ok {
		return "", false
	}
	display, ok := state.guestSeats[sessionKey]
	if !ok || state.participantCounts[display] <= 0 {
		return "", false
	}
	return expectedRoomID, true
}

// consentPrincipalForTranscriptSpeaker converts attribution into the durable
// consent principal. Roster membership is verified instead of assuming every
// display name maps to an account; guest attribution is accepted only when the
// live room can resolve the display to one exact hashed guest-session key.
func (app *kanbanBoardApp) consentPrincipalForTranscriptSpeaker(roomID, speaker string) (CanonicalPrincipalRef, bool) {
	speaker = normalizeTranscriptSpeaker(speaker)
	if speaker == "" || app == nil {
		return CanonicalPrincipalRef{}, false
	}
	if email := normalizeAccountEmail(participantEmail(speaker)); email != "" {
		if accountStore().findUser(email) == nil {
			return CanonicalPrincipalRef{}, false
		}
		return memberAdmissionPrincipal(email), true
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	defer app.mu.Unlock()
	state, ok := app.roomLive[roomID]
	if !ok || state.participantCounts[speaker] <= 0 {
		return CanonicalPrincipalRef{}, false
	}
	principalID := ""
	for sessionKey, display := range state.guestSeats {
		if !sameParticipantName(display, speaker) || !isHexDigest(sessionKey) {
			continue
		}
		if principalID != "" && principalID != sessionKey {
			return CanonicalPrincipalRef{}, false
		}
		principalID = sessionKey
	}
	if principalID == "" {
		return CanonicalPrincipalRef{}, false
	}
	return guestAdmissionPrincipal(principalID), true
}
