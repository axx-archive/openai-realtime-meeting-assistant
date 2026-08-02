package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const meetingSpecialistProductBodyLimit = 16 << 10

type appMeetingSpecialistProductAuthority struct {
	app     *kanbanBoardApp
	runtime *STRIDERuntime
}

// revokeMeetingSpecialistParticipantAuthority is called only after app.mu has
// been released by the presence owner. That ordering prevents an inversion
// with request-time scope resolution while still closing a joined specialist
// synchronously before the roster mutation returns to its caller.
func (app *kanbanBoardApp) revokeMeetingSpecialistParticipantAuthority(roomID, reason string) {
	if app == nil || app.meetingSpecialists == nil {
		return
	}
	_ = app.meetingSpecialists.RevokeScopeAuthority(roomID, "", reason)
}

func bindMeetingSpecialistConsentObserver(product *MeetingSpecialistProduct) {
	if product == nil {
		return
	}
	product.unsubscribeConsentDecisions = subscribeConsentDecisions(func(notice ConsentDecisionNotice) {
		if notice.Scope != ConsentAudioCapture && notice.Scope != ConsentTranscription && notice.Scope != ConsentModelAnalysis {
			return
		}
		_ = product.RevokeScopeAuthority(notice.Binding.RoomID, notice.Binding.SittingID, "consent_authority_changed")
	})
}

func (authority *appMeetingSpecialistProductAuthority) ResolveScope(ctx context.Context, user *userAccount, requestedRoomID string) (meetingSpecialistProductScope, error) {
	return authority.resolveScope(ctx, user, requestedRoomID, true)
}

func (authority *appMeetingSpecialistProductAuthority) ResolveControlScope(ctx context.Context, user *userAccount, requestedRoomID string) (meetingSpecialistProductScope, error) {
	return authority.resolveScope(ctx, user, requestedRoomID, false)
}

func (authority *appMeetingSpecialistProductAuthority) resolveScope(ctx context.Context, user *userAccount, requestedRoomID string, requireConsent bool) (meetingSpecialistProductScope, error) {
	if authority == nil || authority.app == nil || user == nil || authority.runtime == nil {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	roomID, ok := authority.app.activeMemberConsentRoom(user.Email)
	if !ok || normalizeRoomID(requestedRoomID) != roomID {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	room, found := appRoomStore().byID(roomID)
	if !found || room.Archived {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	meeting, active := authority.app.meetings.activeRecord(roomID)
	if !active {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	authority.app.mu.Lock()
	live := authority.app.roomLiveLocked(roomID)
	if live.mediaGen == 0 || live.mediaActor == nil || live.mediaSittingID != meeting.ID {
		authority.app.mu.Unlock()
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	mediaGeneration := live.mediaGen
	participants := authority.app.participantSnapshotLocked(live)
	authority.app.mu.Unlock()
	if len(participants) == 0 {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}

	audience := make([]string, 0, len(participants))
	consentFences := make([]ConsentFence, 0, len(participants)*3)
	for _, name := range participants {
		principal, found := authority.app.consentPrincipalForTranscriptSpeaker(roomID, name)
		if !found || principal.Kind != string(ACLPrincipalUser) {
			return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
		}
		for _, lane := range []ConsentLane{ConsentLaneAudioCapture, ConsentLaneTranscription, ConsentLaneModelAnalysis} {
			if !requireConsent {
				break
			}
			decision, err := authority.app.effectiveConsentLane(ctx, principal, roomID, meeting.ID, lane)
			if err != nil || !decision.Allowed {
				if err != nil {
					return meetingSpecialistProductScope{}, err
				}
				return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
			}
			consentFences = append(consentFences, decision.Fence)
		}
		audience = append(audience, meetingSpecialistAudiencePrincipal(principal))
	}
	audience = meetingSpecialistSortedUnique(audience)
	requester := meetingSpecialistAudiencePrincipal(memberAdmissionPrincipal(user.Email))
	if !meetingSpecialistContainsString(audience, requester) {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	policy := currentConsentLaneAuthority().PolicyVersion
	return meetingSpecialistProductScope{
		TenantID: canonicalTenantID(), RoomID: roomID, SittingID: meeting.ID, MediaGeneration: mediaGeneration,
		RequesterPrincipal: requester, Audience: STRIDEAudience{Visibility: "meeting", Principals: audience},
		ConsentPolicyRevision: STRIDEReference{ContractType: STRIDEContractKnowledgeAssertion, ID: "consent_policy_" + sha256Hex([]byte(policy))[:16], Revision: 1, Digest: sha256Hex([]byte(policy))},
		ConsentFences:         consentFences,
	}, nil
}

func (authority *appMeetingSpecialistProductAuthority) EligibleRoster(_ context.Context, scope meetingSpecialistProductScope) ([]MeetingSpecialistCandidate, error) {
	if authority == nil || authority.runtime == nil {
		return nil, ErrMeetingSpecialistProductDisabled
	}
	// Resolve Product's human-controlled assignment revision and Workforce's
	// lifecycle/capability revision under one tenant lock. Neither domain may
	// make the other broader, and hire/default organization membership alone is
	// never meeting authority.
	result := []MeetingSpecialistCandidate{}
	err := authority.runtime.WithProductContext(scope.TenantID, STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		agents := make(map[string]STRIDEProductTeamAgent)
		for _, agent := range ctx.Product.agentRoster() {
			agents[agent.ID] = agent
		}
		for _, seat := range ctx.Workforce.ScoutRosterView().Seats {
			candidate, eligible := meetingSpecialistCandidateForScope(agents[seat.ID], seat, scope.RoomID)
			if eligible {
				result = append(result, candidate)
			}
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result, err
}

func (authority *appMeetingSpecialistProductAuthority) ScopeCurrent(ctx context.Context, scope meetingSpecialistProductScope) error {
	if authority == nil || authority.app == nil {
		return ErrMeetingSpecialistProductScope
	}
	room, found := appRoomStore().byID(scope.RoomID)
	if !found || room.Archived {
		return ErrMeetingSpecialistProductScope
	}
	meeting, active := authority.app.meetings.activeRecord(scope.RoomID)
	if !active || meeting.ID != scope.SittingID {
		return ErrMeetingSpecialistProductScope
	}
	authority.app.mu.Lock()
	live, found := authority.app.roomLive[scope.RoomID]
	current := found && live.mediaActor != nil && live.mediaGen == scope.MediaGeneration && live.mediaSittingID == scope.SittingID
	participants := authority.app.participantSnapshotLocked(live)
	authority.app.mu.Unlock()
	if !current || len(participants) == 0 {
		return ErrMeetingSpecialistProductScope
	}
	audience := make([]string, 0, len(participants))
	for _, name := range participants {
		principal, admitted := authority.app.consentPrincipalForTranscriptSpeaker(scope.RoomID, name)
		if !admitted || principal.Kind != string(ACLPrincipalUser) {
			return ErrMeetingSpecialistProductScope
		}
		audience = append(audience, meetingSpecialistAudiencePrincipal(principal))
	}
	audience = meetingSpecialistSortedUnique(audience)
	if len(audience) != len(scope.Audience.Principals) || !meetingSpecialistContainsString(audience, scope.RequesterPrincipal) {
		return ErrMeetingSpecialistProductScope
	}
	for index := range audience {
		if audience[index] != scope.Audience.Principals[index] {
			return ErrMeetingSpecialistProductScope
		}
	}
	consent := currentConsentLaneAuthority()
	for _, fence := range scope.ConsentFences {
		if err := consent.ValidateFence(ctx, fence); err != nil {
			return err
		}
	}
	return nil
}

func initializeMeetingSpecialistProduct(app *kanbanBoardApp, runtime *STRIDERuntime) *MeetingSpecialistProduct {
	// The env boolean alone has no authority. Activation additionally requires
	// a valid, short-lived, MAC-signed receipt for only the three control-plane
	// features. Provider, token, tool, and audio gates remain hard off.
	activation, receiptValid := loadMeetingSpecialistControlActivation(runtime, time.Now())
	enabled := boolEnv("STRIDE_MEETING_SPECIALIST_CONTROL_ENABLED") && receiptValid
	config := MeetingSpecialistProductConfig{Enabled: enabled, TenantID: canonicalTenantID(), Authority: &appMeetingSpecialistProductAuthority{app: app, runtime: runtime}}
	if enabled {
		activationDigest, _ := STRIDEContractDigest(activation)
		config.ControlCurrent = func() bool {
			if !boolEnv("STRIDE_MEETING_SPECIALIST_CONTROL_ENABLED") {
				return false
			}
			current, ok := loadMeetingSpecialistControlActivation(runtime, time.Now())
			if !ok {
				return false
			}
			digest, err := STRIDEContractDigest(current)
			return err == nil && digest == activationDigest
		}
	}
	if enabled && runtime != nil {
		dataDir := filepath.Dir(meetingMemoryPath())
		config.Persistence = &MeetingSpecialistProductPersistence{
			SnapshotPath: filepath.Join(dataDir, defaultMeetingSpecialistProductSnapshot), GenerationPath: filepath.Join(dataDir, defaultMeetingSpecialistProductGeneration),
			Authority: runtime.config.Authority, MinimumGeneration: activation.StateMinimumGeneration, BootstrapEmpty: activation.BootstrapEmpty,
		}
	}
	product := NewMeetingSpecialistProduct(config)
	if enabled {
		bindMeetingSpecialistAuthorityObserver(runtime, product)
		bindMeetingSpecialistConsentObserver(product)
	}
	return product
}

func registerMeetingSpecialistProductRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/api/stride/v1/meeting-specialists", meetingSpecialistProductStatusHandler)
	mux.HandleFunc("/api/stride/v1/meeting-specialists/invitations", meetingSpecialistProductInvitationHandler)
	mux.HandleFunc("/api/stride/v1/meeting-specialists/invitations/", meetingSpecialistProductInvitationHandler)
}

func meetingSpecialistProductStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, product, ok := meetingSpecialistRequestContext(w, r)
	if !ok {
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "specialists": product.Status(r.Context(), user, r.URL.Query().Get("roomId"))})
}

func meetingSpecialistProductInvitationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, product, ok := meetingSpecialistRequestContext(w, r)
	if !ok {
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var payload struct {
		RoomID         string `json:"roomId"`
		AgentID        string `json:"agentId"`
		Purpose        string `json:"purpose"`
		IdempotencyKey string `json:"idempotencyKey"`
		Revision       int64  `json:"revision"`
		Decision       string `json:"decision"`
	}
	if err := decodeMeetingSpecialistProductBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid specialist request")
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/stride/v1/meeting-specialists/invitations/"))
	var view meetingSpecialistInvitationView
	var err error
	if id == "" || id == r.URL.Path {
		view, err = product.Request(r.Context(), user, payload.RoomID, payload.AgentID, payload.Purpose, payload.IdempotencyKey, 5*time.Minute)
	} else {
		view, err = product.Resolve(r.Context(), user, payload.RoomID, id, payload.Revision, payload.Decision)
	}
	if err != nil {
		writeMeetingSpecialistProductError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "invitation": view, "providerSessionStarted": view.ProviderSessionStarted})
}

func meetingSpecialistRequestContext(w http.ResponseWriter, r *http.Request) (*userAccount, *MeetingSpecialistProduct, bool) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil, nil, false
	}
	if kanbanApp == nil || kanbanApp.meetingSpecialists == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "meeting specialists are unavailable")
		return nil, nil, false
	}
	return user, kanbanApp.meetingSpecialists, true
}

func decodeMeetingSpecialistProductBody(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, meetingSpecialistProductBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func writeMeetingSpecialistProductError(w http.ResponseWriter, err error) {
	status := http.StatusForbidden
	switch {
	case errors.Is(err, ErrMeetingSpecialistProductDisabled), errors.Is(err, ErrConsentAuthorityUnavailable), errors.Is(err, ErrSTRIDERuntimeUnavailable), errors.Is(err, ErrSTRIDERuntimeDisabled):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrMeetingSpecialistProductRevision):
		status = http.StatusConflict
	case errors.Is(err, ErrMeetingSpecialistProductDecision):
		status = http.StatusBadRequest
	}
	writeAuthError(w, status, "specialist request was not authorized")
}

func meetingSpecialistAudiencePrincipal(principal CanonicalPrincipalRef) string {
	return strings.TrimSpace(principal.Kind) + ":" + sha256Hex([]byte(strings.TrimSpace(principal.ID)))[:24]
}

func meetingSpecialistContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func meetingSpecialistSortedUnique(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if write > 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}
