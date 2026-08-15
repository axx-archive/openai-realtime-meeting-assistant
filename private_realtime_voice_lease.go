package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	privateRealtimeVoiceLeaseVersion = "private-realtime-voice-lease/v1"
	privateRealtimeVoiceLeaseTTL     = 30 * time.Second
	privateRealtimeVoiceRenewLimit   = 32
	privateRealtimeOfferReplayLimit  = 256
)

var (
	errPrivateRealtimeLeaseConflict          = errors.New("private Realtime voice lease is held by another client")
	errPrivateRealtimeLeaseStale             = errors.New("private Realtime voice lease generation is stale")
	errPrivateRealtimeLeaseReplayUnavailable = errors.New("private Realtime voice replay expired with the server process")
)

// scoutChatVoiceLease contains only authority digests and lifecycle receipts.
// LeaseTokenDigest is one-way; OfferDigest cannot reconstruct raw SDP.
type scoutChatVoiceLease struct {
	Version           string                       `json:"version"`
	Generation        int                          `json:"generation"`
	State             string                       `json:"state"`
	AuthSessionDigest string                       `json:"authSessionDigest"`
	ClientDigest      string                       `json:"clientDigest"`
	LeaseTokenDigest  string                       `json:"leaseTokenDigest"`
	OperationID       string                       `json:"operationId"`
	OfferDigest       string                       `json:"offerDigest"`
	TransportRevision int                          `json:"transportRevision"`
	AcquiredAt        string                       `json:"acquiredAt"`
	RenewedAt         string                       `json:"renewedAt,omitempty"`
	ExpiresAt         string                       `json:"expiresAt"`
	TerminalAt        string                       `json:"terminalAt,omitempty"`
	Renewals          []scoutChatVoiceLeaseRenewal `json:"renewals,omitempty"`
	StopOperationID   string                       `json:"stopOperationId,omitempty"`
}

type scoutChatVoiceLeaseRenewal struct {
	OperationID string `json:"operationId"`
	AcceptedAt  string `json:"acceptedAt"`
	ExpiresAt   string `json:"expiresAt"`
}

type privateRealtimeOfferReplay struct {
	OfferDigest       string
	LeaseToken        string
	AnswerSDP         string
	VoiceSessionID    string
	ThreadID          string
	OperationID       string
	Generation        int
	TransportRevision int
	ExpiresAt         time.Time
}

type privateRealtimeLeaseClaim struct {
	Replay            bool
	LeaseToken        string
	AnswerSDP         string
	OperationID       string
	Generation        int
	TransportRevision int
	ExpiresAt         time.Time
}

func privateRealtimeLeaseDigest(label, value string) string {
	return sha256Hex([]byte(privateRealtimeVoiceLeaseVersion + "\x00" + label + "\x00" + strings.TrimSpace(value)))
}

func privateRealtimeLeaseClientDigest(sessionHash, _ string) string {
	// The authenticated session is the client instance. Its voiceSessionID is
	// validated independently against the exact thread. Keeping it out of this
	// digest lets the same signed-in client recover immediately with a new voice
	// session while a different browser/device session still conflicts.
	return privateRealtimeLeaseDigest("client", sessionHash)
}

func privateRealtimeLeaseReplayKey(requesterEmail, sessionHash, operationID string) string {
	return sha256Hex([]byte(privateRealtimeVoiceLeaseVersion + "\x00replay\x00" + normalizeAccountEmail(requesterEmail) + "\x00" + sessionHash + "\x00" + operationID))
}

func newPrivateRealtimeLeaseToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("mint private Realtime voice lease: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func privateRealtimeLeaseActive(lease *scoutChatVoiceLease) bool {
	return lease != nil && (lease.State == "claimed" || lease.State == "accepted")
}

func privateRealtimeLeaseExpiry(lease *scoutChatVoiceLease) time.Time {
	if lease == nil {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	return parsed.UTC()
}

func (app *kanbanBoardApp) privateRealtimeOfferReplay(key, offerDigest string, at time.Time) (privateRealtimeOfferReplay, bool) {
	app.privateRealtimeOfferReplayMu.Lock()
	defer app.privateRealtimeOfferReplayMu.Unlock()
	app.prunePrivateRealtimeOfferReplaysLocked(at)
	if app.privateRealtimeOfferReplays == nil {
		return privateRealtimeOfferReplay{}, false
	}
	replay, ok := app.privateRealtimeOfferReplays[key]
	if !ok || replay.OfferDigest != offerDigest {
		return privateRealtimeOfferReplay{}, false
	}
	return replay, true
}

func (app *kanbanBoardApp) rememberPrivateRealtimeOfferReplay(requesterEmail, sessionHash string, replay privateRealtimeOfferReplay, at time.Time) {
	app.privateRealtimeOfferReplayMu.Lock()
	defer app.privateRealtimeOfferReplayMu.Unlock()
	if app.privateRealtimeOfferReplays == nil {
		app.privateRealtimeOfferReplays = map[string]privateRealtimeOfferReplay{}
	}
	app.privateRealtimeOfferReplays[privateRealtimeLeaseReplayKey(requesterEmail, sessionHash, replay.OperationID)] = replay
	app.prunePrivateRealtimeOfferReplaysLocked(at)
}

func (app *kanbanBoardApp) prunePrivateRealtimeOfferReplays(at time.Time) {
	app.privateRealtimeOfferReplayMu.Lock()
	defer app.privateRealtimeOfferReplayMu.Unlock()
	app.prunePrivateRealtimeOfferReplaysLocked(at)
}

func (app *kanbanBoardApp) prunePrivateRealtimeOfferReplaysLocked(at time.Time) {
	at = at.UTC()
	for key, replay := range app.privateRealtimeOfferReplays {
		if !at.Before(replay.ExpiresAt) {
			delete(app.privateRealtimeOfferReplays, key)
		}
	}
	for len(app.privateRealtimeOfferReplays) > privateRealtimeOfferReplayLimit {
		oldestKey := ""
		oldestExpiry := time.Time{}
		for key, replay := range app.privateRealtimeOfferReplays {
			if oldestKey == "" || replay.ExpiresAt.Before(oldestExpiry) {
				oldestKey = key
				oldestExpiry = replay.ExpiresAt
			}
		}
		delete(app.privateRealtimeOfferReplays, oldestKey)
	}
}

func (app *kanbanBoardApp) forgetPrivateRealtimeOfferReplay(requesterEmail, sessionHash, operationID string) {
	app.privateRealtimeOfferReplayMu.Lock()
	defer app.privateRealtimeOfferReplayMu.Unlock()
	delete(app.privateRealtimeOfferReplays, privateRealtimeLeaseReplayKey(requesterEmail, sessionHash, operationID))
}

func (app *kanbanBoardApp) extendPrivateRealtimeOfferReplay(requesterEmail, sessionHash, operationID string, expiresAt, at time.Time) {
	app.privateRealtimeOfferReplayMu.Lock()
	defer app.privateRealtimeOfferReplayMu.Unlock()
	app.prunePrivateRealtimeOfferReplaysLocked(at)
	key := privateRealtimeLeaseReplayKey(requesterEmail, sessionHash, operationID)
	replay, ok := app.privateRealtimeOfferReplays[key]
	if !ok {
		return
	}
	replay.ExpiresAt = expiresAt.UTC()
	app.privateRealtimeOfferReplays[key] = replay
}

func (app *kanbanBoardApp) claimPrivateRealtimeVoiceLease(requesterEmail, sessionHash, voiceSessionID, threadID, operationID, offerDigest string, at time.Time) (privateRealtimeLeaseClaim, error) {
	if app == nil || strings.TrimSpace(sessionHash) == "" {
		return privateRealtimeLeaseClaim{}, fmt.Errorf("private Realtime voice authenticated session is unavailable")
	}
	operationID, err := normalizeScoutIdempotencyKey(operationID)
	if err != nil || strings.TrimSpace(offerDigest) == "" {
		return privateRealtimeLeaseClaim{}, fmt.Errorf("private Realtime voice lease operation is invalid")
	}
	requesterEmail = normalizeAccountEmail(requesterEmail)
	voiceSessionID, err = normalizePrivateRealtimeVoiceSessionID(voiceSessionID)
	if err != nil {
		return privateRealtimeLeaseClaim{}, err
	}
	at = at.UTC()
	app.prunePrivateRealtimeOfferReplays(at)
	authDigest := privateRealtimeLeaseDigest("auth-session", sessionHash)
	clientDigest := privateRealtimeLeaseClientDigest(sessionHash, voiceSessionID)
	ownerLock := app.scoutChatThreadLock("private-realtime-lease-owner-" + sha256Hex([]byte(requesterEmail))[:24])
	ownerLock.Lock()
	defer ownerLock.Unlock()

	maxGeneration := 0
	threads := app.scoutChatThreadsSnapshot(requesterEmail, true, 0)
	for _, candidate := range threads {
		lease := candidate.VoiceSession
		if lease == nil || lease.Lease == nil {
			continue
		}
		if lease.Lease.Generation > maxGeneration {
			maxGeneration = lease.Lease.Generation
		}
		if !privateRealtimeLeaseActive(lease.Lease) {
			continue
		}
		if !at.Before(privateRealtimeLeaseExpiry(lease.Lease)) {
			if err := app.terminalizePrivateRealtimeLeaseAttempt(requesterEmail, candidate.ID, lease.Lease.Generation, "expired", at); err != nil {
				return privateRealtimeLeaseClaim{}, err
			}
			continue
		}
		if candidate.ID == threadID && lease.Lease.OperationID == operationID {
			if lease.Lease.OfferDigest != offerDigest || lease.Lease.AuthSessionDigest != authDigest || lease.Lease.ClientDigest != clientDigest {
				return privateRealtimeLeaseClaim{}, errPrivateRealtimeLeaseConflict
			}
			key := privateRealtimeLeaseReplayKey(requesterEmail, sessionHash, operationID)
			if replay, ok := app.privateRealtimeOfferReplay(key, offerDigest, at); ok {
				return privateRealtimeLeaseClaim{Replay: true, LeaseToken: replay.LeaseToken, AnswerSDP: replay.AnswerSDP, OperationID: operationID, Generation: replay.Generation, TransportRevision: replay.TransportRevision, ExpiresAt: replay.ExpiresAt}, nil
			}
			return privateRealtimeLeaseClaim{}, errPrivateRealtimeLeaseReplayUnavailable
		}
		if lease.Lease.ClientDigest != clientDigest || lease.Lease.AuthSessionDigest != authDigest {
			return privateRealtimeLeaseClaim{}, errPrivateRealtimeLeaseConflict
		}
		if err := app.terminalizePrivateRealtimeLeaseAttempt(requesterEmail, candidate.ID, lease.Lease.Generation, "superseded", at); err != nil {
			return privateRealtimeLeaseClaim{}, err
		}
		app.forgetPrivateRealtimeOfferReplay(requesterEmail, sessionHash, lease.Lease.OperationID)
	}

	token, err := newPrivateRealtimeLeaseToken()
	if err != nil {
		return privateRealtimeLeaseClaim{}, err
	}
	threadLock := app.scoutChatThreadLock(threadID)
	threadLock.Lock()
	defer threadLock.Unlock()
	thread, err := app.privateRealtimeVoiceConversation(requesterEmail, voiceSessionID, threadID)
	if err != nil {
		return privateRealtimeLeaseClaim{}, err
	}
	binding := thread.VoiceSession
	for index := range binding.TransportAttempts {
		attempt := &binding.TransportAttempts[index]
		if attempt.State == "offering" || attempt.State == "accepted" {
			attempt.State = "superseded"
			attempt.FailedAt = at.Format(time.RFC3339Nano)
		}
	}
	binding.TransportRevision++
	revision := binding.TransportRevision
	binding.TransportAttempts = append(binding.TransportAttempts, scoutChatVoiceTransportAttempt{Revision: revision, State: "offering", StartedAt: at.Format(time.RFC3339Nano)})
	if len(binding.TransportAttempts) > privateRealtimeVoiceTransportAttemptLimit {
		binding.TransportAttempts = append([]scoutChatVoiceTransportAttempt(nil), binding.TransportAttempts[len(binding.TransportAttempts)-privateRealtimeVoiceTransportAttemptLimit:]...)
	}
	expiresAt := at.Add(privateRealtimeVoiceLeaseTTL)
	binding.Lease = &scoutChatVoiceLease{
		Version: privateRealtimeVoiceLeaseVersion, Generation: maxGeneration + 1, State: "claimed",
		AuthSessionDigest: authDigest, ClientDigest: clientDigest, LeaseTokenDigest: privateRealtimeLeaseDigest("token", token),
		OperationID: operationID, OfferDigest: offerDigest, TransportRevision: revision,
		AcquiredAt: at.Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano),
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return privateRealtimeLeaseClaim{}, err
	}
	return privateRealtimeLeaseClaim{LeaseToken: token, OperationID: operationID, Generation: maxGeneration + 1, TransportRevision: revision, ExpiresAt: expiresAt}, nil
}

func (app *kanbanBoardApp) terminalizePrivateRealtimeLeaseAttempt(requesterEmail, threadID string, generation int, state string, at time.Time) error {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, _, err := app.scoutChatThreadByID(requesterEmail, threadID)
	if err != nil || thread.VoiceSession == nil || thread.VoiceSession.Lease == nil || thread.VoiceSession.Lease.Generation != generation {
		if err != nil {
			return err
		}
		return errPrivateRealtimeLeaseStale
	}
	lease := thread.VoiceSession.Lease
	lease.State = state
	lease.TerminalAt = at.UTC().Format(time.RFC3339Nano)
	if attempt := privateRealtimeVoiceTransportAttemptByRevision(thread.VoiceSession, lease.TransportRevision); attempt != nil && (attempt.State == "offering" || attempt.State == "accepted") {
		attempt.State = state
		attempt.FailedAt = lease.TerminalAt
		if state == "stopped" {
			attempt.StoppedAt = lease.TerminalAt
		}
	}
	return app.saveScoutChatThread(thread)
}

func (app *kanbanBoardApp) finishPrivateRealtimeVoiceLease(requesterEmail, sessionHash, voiceSessionID, threadID string, claim privateRealtimeLeaseClaim, accepted bool, answerSDP string, at time.Time) error {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, err := app.privateRealtimeVoiceConversation(requesterEmail, voiceSessionID, threadID)
	if err != nil {
		return err
	}
	lease := thread.VoiceSession.Lease
	if lease == nil || lease.Generation != claim.Generation || lease.TransportRevision != claim.TransportRevision || lease.OperationID != claim.OperationID || lease.LeaseTokenDigest != privateRealtimeLeaseDigest("token", claim.LeaseToken) {
		return errPrivateRealtimeLeaseStale
	}
	attempt := privateRealtimeVoiceTransportAttemptByRevision(thread.VoiceSession, claim.TransportRevision)
	if attempt == nil || attempt.State != "offering" {
		return errPrivateRealtimeLeaseStale
	}
	if accepted {
		lease.State = "accepted"
		attempt.State = "accepted"
		attempt.AcceptedAt = at.UTC().Format(time.RFC3339Nano)
	} else {
		lease.State = "failed"
		lease.TerminalAt = at.UTC().Format(time.RFC3339Nano)
		attempt.State = "failed"
		attempt.FailedAt = lease.TerminalAt
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return err
	}
	if accepted {
		app.rememberPrivateRealtimeOfferReplay(requesterEmail, sessionHash, privateRealtimeOfferReplay{OfferDigest: lease.OfferDigest, LeaseToken: claim.LeaseToken, AnswerSDP: answerSDP, VoiceSessionID: voiceSessionID, ThreadID: threadID, OperationID: claim.OperationID, Generation: claim.Generation, TransportRevision: claim.TransportRevision, ExpiresAt: privateRealtimeLeaseExpiry(lease)}, at)
	}
	return nil
}

func (app *kanbanBoardApp) renewPrivateRealtimeVoiceLease(requesterEmail, sessionHash, voiceSessionID, threadID, token string, generation, revision int, operationID string, at time.Time) (time.Time, bool, error) {
	operationID, err := normalizeScoutIdempotencyKey(operationID)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("private Realtime voice renew operation is invalid")
	}
	lock := app.scoutChatThreadLock("private-realtime-lease-owner-" + sha256Hex([]byte(normalizeAccountEmail(requesterEmail)))[:24])
	lock.Lock()
	defer lock.Unlock()
	threadLock := app.scoutChatThreadLock(threadID)
	threadLock.Lock()
	defer threadLock.Unlock()
	thread, err := app.privateRealtimeVoiceConversation(requesterEmail, voiceSessionID, threadID)
	if err != nil {
		return time.Time{}, false, err
	}
	lease := thread.VoiceSession.Lease
	if err := validatePrivateRealtimeLease(lease, sessionHash, token, generation, revision); err != nil {
		return time.Time{}, false, err
	}
	for _, renewal := range lease.Renewals {
		if renewal.OperationID == operationID {
			expiresAt, _ := time.Parse(time.RFC3339Nano, renewal.ExpiresAt)
			return expiresAt.UTC(), true, nil
		}
	}
	at = at.UTC()
	if !at.Before(privateRealtimeLeaseExpiry(lease)) {
		return time.Time{}, false, errPrivateRealtimeLeaseStale
	}
	lease.RenewedAt = at.Format(time.RFC3339Nano)
	lease.ExpiresAt = at.Add(privateRealtimeVoiceLeaseTTL).Format(time.RFC3339Nano)
	lease.Renewals = append(lease.Renewals, scoutChatVoiceLeaseRenewal{OperationID: operationID, AcceptedAt: lease.RenewedAt, ExpiresAt: lease.ExpiresAt})
	if len(lease.Renewals) > privateRealtimeVoiceRenewLimit {
		lease.Renewals = append([]scoutChatVoiceLeaseRenewal(nil), lease.Renewals[len(lease.Renewals)-privateRealtimeVoiceRenewLimit:]...)
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return time.Time{}, false, err
	}
	expiresAt := privateRealtimeLeaseExpiry(lease)
	app.extendPrivateRealtimeOfferReplay(requesterEmail, sessionHash, lease.OperationID, expiresAt, at)
	return expiresAt, false, nil
}

func (app *kanbanBoardApp) stopPrivateRealtimeVoiceLease(requesterEmail, sessionHash, voiceSessionID, threadID, token string, generation, revision int, operationID string, at time.Time) (bool, error) {
	operationID, err := normalizeScoutIdempotencyKey(operationID)
	if err != nil {
		return false, fmt.Errorf("private Realtime voice stop operation is invalid")
	}
	lock := app.scoutChatThreadLock("private-realtime-lease-owner-" + sha256Hex([]byte(normalizeAccountEmail(requesterEmail)))[:24])
	lock.Lock()
	defer lock.Unlock()
	threadLock := app.scoutChatThreadLock(threadID)
	threadLock.Lock()
	defer threadLock.Unlock()
	thread, err := app.privateRealtimeVoiceConversation(requesterEmail, voiceSessionID, threadID)
	if err != nil {
		return false, err
	}
	lease := thread.VoiceSession.Lease
	if err := validatePrivateRealtimeLeaseIdentity(lease, sessionHash, token, generation, revision); err != nil {
		return false, err
	}
	if lease.State == "stopped" && lease.StopOperationID == operationID {
		return true, nil
	}
	if !privateRealtimeLeaseActive(lease) {
		return false, errPrivateRealtimeLeaseStale
	}
	at = at.UTC()
	lease.State = "stopped"
	lease.StopOperationID = operationID
	lease.TerminalAt = at.Format(time.RFC3339Nano)
	if attempt := privateRealtimeVoiceTransportAttemptByRevision(thread.VoiceSession, revision); attempt != nil && (attempt.State == "offering" || attempt.State == "accepted") {
		attempt.State = "stopped"
		attempt.StoppedAt = lease.TerminalAt
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return false, err
	}
	app.forgetPrivateRealtimeOfferReplay(requesterEmail, sessionHash, lease.OperationID)
	return false, nil
}

func validatePrivateRealtimeLeaseIdentity(lease *scoutChatVoiceLease, sessionHash, token string, generation, revision int) error {
	if lease == nil || generation <= 0 || revision <= 0 || lease.Generation != generation || lease.TransportRevision != revision || lease.AuthSessionDigest != privateRealtimeLeaseDigest("auth-session", sessionHash) || lease.LeaseTokenDigest != privateRealtimeLeaseDigest("token", token) {
		return errPrivateRealtimeLeaseStale
	}
	return nil
}

func validatePrivateRealtimeLease(lease *scoutChatVoiceLease, sessionHash, token string, generation, revision int) error {
	if err := validatePrivateRealtimeLeaseIdentity(lease, sessionHash, token, generation, revision); err != nil {
		return err
	}
	if !privateRealtimeLeaseActive(lease) {
		return errPrivateRealtimeLeaseStale
	}
	return nil
}

func validatePrivateRealtimeLeaseAdmission(lease *scoutChatVoiceLease, sessionHash, voiceSessionID, token string, generation, revision int, at time.Time) error {
	if err := validatePrivateRealtimeLease(lease, sessionHash, token, generation, revision); err != nil {
		return err
	}
	if lease.ClientDigest != privateRealtimeLeaseClientDigest(sessionHash, voiceSessionID) || !at.UTC().Before(privateRealtimeLeaseExpiry(lease)) {
		return errPrivateRealtimeLeaseStale
	}
	return nil
}

// authorizePrivateRealtimeVoiceLease is an admission fence. It intentionally
// does not hold the lease lock across provider/tool work: stop or expiry after
// admission cannot cancel an already-running effect, but no stale request can
// begin an effect after this check returns an error.
func (app *kanbanBoardApp) authorizePrivateRealtimeVoiceLease(requesterEmail, sessionHash, voiceSessionID, threadID, token string, generation, revision int, at time.Time) error {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, err := app.privateRealtimeVoiceConversation(requesterEmail, voiceSessionID, threadID)
	if err != nil {
		return err
	}
	return validatePrivateRealtimeLeaseAdmission(thread.VoiceSession.Lease, sessionHash, voiceSessionID, token, generation, revision, at)
}

func privateRealtimeVoiceLeaseHTTPStatus(err error) int {
	switch {
	case errors.Is(err, errPrivateRealtimeLeaseConflict), errors.Is(err, errPrivateRealtimeLeaseStale):
		return http.StatusConflict
	case errors.Is(err, errPrivateRealtimeLeaseReplayUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusConflict
	}
}

type privateRealtimeVoiceLeaseMutationPayload struct {
	VoiceSessionID    string `json:"voiceSessionId"`
	ThreadID          string `json:"threadId"`
	LeaseToken        string `json:"leaseToken"`
	LeaseGeneration   int    `json:"leaseGeneration"`
	TransportRevision int    `json:"transportRevision"`
	OperationID       string `json:"operationId"`
}

func readPrivateRealtimeVoiceLeaseMutation(w http.ResponseWriter, r *http.Request) (*userAccount, privateRealtimeVoiceLeaseMutationPayload, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil, privateRealtimeVoiceLeaseMutationPayload{}, false
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return nil, privateRealtimeVoiceLeaseMutationPayload{}, false
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil, privateRealtimeVoiceLeaseMutationPayload{}, false
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return nil, privateRealtimeVoiceLeaseMutationPayload{}, false
	}
	payload := privateRealtimeVoiceLeaseMutationPayload{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read realtime lease operation")
		return nil, privateRealtimeVoiceLeaseMutationPayload{}, false
	}
	if strings.TrimSpace(payload.ThreadID) == "" || strings.TrimSpace(payload.LeaseToken) == "" || payload.LeaseGeneration <= 0 || payload.TransportRevision <= 0 {
		writeAuthError(w, http.StatusBadRequest, "realtime lease binding is required")
		return nil, privateRealtimeVoiceLeaseMutationPayload{}, false
	}
	return user, payload, true
}

func assistantRealtimeLeaseRenewHandler(w http.ResponseWriter, r *http.Request) {
	user, payload, ok := readPrivateRealtimeVoiceLeaseMutation(w, r)
	if !ok {
		return
	}
	expiresAt, replayed, err := kanbanApp.renewPrivateRealtimeVoiceLease(
		user.Email, strideE10SessionHashFromRequest(r), payload.VoiceSessionID, payload.ThreadID,
		payload.LeaseToken, payload.LeaseGeneration, payload.TransportRevision, payload.OperationID, time.Now().UTC(),
	)
	if err != nil {
		writeAuthError(w, privateRealtimeVoiceLeaseHTTPStatus(err), err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "replayed": replayed, "leaseExpiresAt": expiresAt.Format(time.RFC3339Nano)})
}

func assistantRealtimeLeaseStopHandler(w http.ResponseWriter, r *http.Request) {
	user, payload, ok := readPrivateRealtimeVoiceLeaseMutation(w, r)
	if !ok {
		return
	}
	replayed, err := kanbanApp.stopPrivateRealtimeVoiceLease(
		user.Email, strideE10SessionHashFromRequest(r), payload.VoiceSessionID, payload.ThreadID,
		payload.LeaseToken, payload.LeaseGeneration, payload.TransportRevision, payload.OperationID, time.Now().UTC(),
	)
	if err != nil {
		writeAuthError(w, privateRealtimeVoiceLeaseHTTPStatus(err), err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "replayed": replayed, "state": "stopped"})
}
