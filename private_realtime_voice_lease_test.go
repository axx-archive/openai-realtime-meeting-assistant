package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func activatePrivateRealtimeLeaseForTest(t *testing.T, app *kanbanBoardApp, ownerEmail, voiceSessionID, threadID string, cookies []*http.Cookie) privateRealtimeLeaseClaim {
	t.Helper()
	sessionHash := privateRealtimeSessionHashForTest(cookies)
	now := time.Now().UTC()
	claim, err := app.claimPrivateRealtimeVoiceLease(ownerEmail, sessionHash, voiceSessionID, threadID, "test-offer-"+sha256Hex([]byte(voiceSessionID))[:16], privateRealtimeLeaseDigest("offer-sdp", "test-offer-"+voiceSessionID), now)
	if err != nil {
		t.Fatalf("claim test private Realtime lease: %v", err)
	}
	if err := app.finishPrivateRealtimeVoiceLease(ownerEmail, sessionHash, voiceSessionID, threadID, claim, true, "test-answer", now.Add(time.Millisecond)); err != nil {
		t.Fatalf("accept test private Realtime lease: %v", err)
	}
	return claim
}

func privateRealtimeSessionHashForTest(cookies []*http.Cookie) string {
	probe := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range cookies {
		probe.AddCookie(cookie)
	}
	return strideE10SessionHashFromRequest(probe)
}

func privateRealtimeLeaseTestJSON(claim privateRealtimeLeaseClaim) string {
	return fmt.Sprintf(`,"leaseToken":%q,"leaseGeneration":%d,"transportRevision":%d`, claim.LeaseToken, claim.Generation, claim.TransportRevision)
}

func TestPrivateRealtimeHTTPAdmissionRejectsStaleStoppedAndExpiredLeaseBeforeEffectOrReceipt(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "true")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	const (
		owner          = "aj@shareability.com"
		voiceSessionID = "voice-http-admission-fence"
	)
	cookies := loginAs(t, owner, "B0NFIRE!")
	thread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	lease := activatePrivateRealtimeLeaseForTest(t, kanbanApp, owner, voiceSessionID, thread.ID, cookies)
	postTool := func(token string, generation, revision int) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"callId":"admission-tool-call","name":"do_nothing","arguments":{"reason":"test"},"leaseToken":%q,"leaseGeneration":%d,"transportRevision":%d}`, voiceSessionID, thread.ID, token, generation, revision)
		req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-tool", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantRealtimeToolHandler(recorder, req)
		return recorder
	}
	postMilestone := func(operationID string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"operationId":%q,"milestone":"data_channel_open","leaseToken":%q,"leaseGeneration":%d,"transportRevision":%d}`, voiceSessionID, thread.ID, operationID, lease.LeaseToken, lease.Generation, lease.TransportRevision)
		req := httptest.NewRequest(http.MethodPost, "/assistant/realtime/milestone", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantRealtimeMilestoneHandler(recorder, req)
		return recorder
	}

	if stale := postTool(lease.LeaseToken, lease.Generation+1, lease.TransportRevision); stale.Code != http.StatusConflict {
		t.Fatalf("stale generation tool status=%d body=%s", stale.Code, stale.Body.String())
	}
	if stale := postTool(lease.LeaseToken, lease.Generation, lease.TransportRevision+1); stale.Code != http.StatusConflict {
		t.Fatalf("stale revision tool status=%d body=%s", stale.Code, stale.Body.String())
	}
	if accepted := postMilestone("admission-milestone-accepted"); accepted.Code != http.StatusOK {
		t.Fatalf("accepted milestone status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	beforeStop, err := kanbanApp.privateRealtimeVoiceConversation(owner, voiceSessionID, thread.ID)
	if err != nil || len(beforeStop.VoiceSession.TransportAttempts[0].Milestones) != 1 {
		t.Fatalf("before stop binding=%+v err=%v", beforeStop.VoiceSession, err)
	}
	if _, err := kanbanApp.stopPrivateRealtimeVoiceLease(owner, privateRealtimeSessionHashForTest(cookies), voiceSessionID, thread.ID, lease.LeaseToken, lease.Generation, lease.TransportRevision, "admission-stop", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if stopped := postTool(lease.LeaseToken, lease.Generation, lease.TransportRevision); stopped.Code != http.StatusConflict {
		t.Fatalf("stopped tool status=%d body=%s", stopped.Code, stopped.Body.String())
	}
	if stopped := postMilestone("admission-milestone-after-stop"); stopped.Code != http.StatusConflict {
		t.Fatalf("stopped milestone status=%d body=%s", stopped.Code, stopped.Body.String())
	}
	afterStop, err := kanbanApp.privateRealtimeVoiceConversation(owner, voiceSessionID, thread.ID)
	if err != nil || len(afterStop.VoiceSession.TransportAttempts[0].Milestones) != 1 {
		t.Fatalf("stopped request created receipt: binding=%+v err=%v", afterStop.VoiceSession, err)
	}

	const expiredVoiceSessionID = "voice-http-expired-fence"
	expiredThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", expiredVoiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	expiredLease := activatePrivateRealtimeLeaseForTest(t, kanbanApp, owner, expiredVoiceSessionID, expiredThread.ID, cookies)
	lock := kanbanApp.scoutChatThreadLock(expiredThread.ID)
	lock.Lock()
	expiredRecord, err := kanbanApp.privateRealtimeVoiceConversation(owner, expiredVoiceSessionID, expiredThread.ID)
	if err == nil {
		expiredRecord.VoiceSession.Lease.ExpiresAt = time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
		err = kanbanApp.saveScoutChatThread(expiredRecord)
	}
	lock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	expiredBody := fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"callId":"expired-tool-call","name":"do_nothing","arguments":{"reason":"test"}%s}`, expiredVoiceSessionID, expiredThread.ID, privateRealtimeLeaseTestJSON(expiredLease))
	expiredReq := httptest.NewRequest(http.MethodPost, "/assistant/realtime-tool", strings.NewReader(expiredBody))
	expiredReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		expiredReq.AddCookie(cookie)
	}
	expiredResponse := httptest.NewRecorder()
	assistantRealtimeToolHandler(expiredResponse, expiredReq)
	if expiredResponse.Code != http.StatusConflict {
		t.Fatalf("expired tool status=%d body=%s", expiredResponse.Code, expiredResponse.Body.String())
	}
}

func TestPrivateRealtimeQualificationKillSwitchTerminalizesRenewalAndRefusesTools(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "true")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	const owner = "aj@shareability.com"
	cookies := loginAs(t, owner, "B0NFIRE!")

	voiceSessionID := "voice-qualification-renew"
	thread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	lease := activatePrivateRealtimeLeaseForTest(t, kanbanApp, owner, voiceSessionID, thread.ID, cookies)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "false")
	renewBody := fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"leaseToken":%q,"leaseGeneration":%d,"transportRevision":%d,"operationId":"qualification-revoked-renew"}`, voiceSessionID, thread.ID, lease.LeaseToken, lease.Generation, lease.TransportRevision)
	renewRequest := httptest.NewRequest(http.MethodPost, "/assistant/realtime/lease/renew", strings.NewReader(renewBody))
	renewRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		renewRequest.AddCookie(cookie)
	}
	renewResponse := httptest.NewRecorder()
	assistantRealtimeLeaseRenewHandler(renewResponse, renewRequest)
	if renewResponse.Code != http.StatusServiceUnavailable || !strings.Contains(renewResponse.Body.String(), "session is stopping") {
		t.Fatalf("renew status=%d body=%s", renewResponse.Code, renewResponse.Body.String())
	}
	afterRenew, err := kanbanApp.privateRealtimeVoiceConversation(owner, voiceSessionID, thread.ID)
	if err != nil || afterRenew.VoiceSession.Lease.State != "qualification_revoked" || afterRenew.VoiceSession.Lease.TerminalAt == "" || afterRenew.VoiceSession.TransportAttempts[0].State != "qualification_revoked" {
		t.Fatalf("revoked renewal binding=%+v err=%v", afterRenew.VoiceSession, err)
	}
	if privateRealtimeVoiceLeaseTTL != 30*time.Second {
		t.Fatalf("lease fallback bound=%s, want 30s", privateRealtimeVoiceLeaseTTL)
	}

	toolVoiceSessionID := "voice-qualification-tool"
	toolThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", toolVoiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	toolLease := activatePrivateRealtimeLeaseForTest(t, kanbanApp, owner, toolVoiceSessionID, toolThread.ID, cookies)
	before, err := kanbanApp.privateRealtimeVoiceConversation(owner, toolVoiceSessionID, toolThread.ID)
	if err != nil {
		t.Fatal(err)
	}
	messageCount := len(before.Messages)
	toolBody := fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"callId":"qualification-revoked-tool","name":"route_conversation_turn","arguments":{"utterance":"Create work that must not launch"}%s}`, toolVoiceSessionID, toolThread.ID, privateRealtimeLeaseTestJSON(toolLease))
	toolRequest := httptest.NewRequest(http.MethodPost, "/assistant/realtime-tool", strings.NewReader(toolBody))
	toolRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		toolRequest.AddCookie(cookie)
	}
	toolResponse := httptest.NewRecorder()
	assistantRealtimeToolHandler(toolResponse, toolRequest)
	if toolResponse.Code != http.StatusServiceUnavailable || !strings.Contains(toolResponse.Body.String(), "session is stopping") {
		t.Fatalf("tool status=%d body=%s", toolResponse.Code, toolResponse.Body.String())
	}
	afterTool, err := kanbanApp.privateRealtimeVoiceConversation(owner, toolVoiceSessionID, toolThread.ID)
	if err != nil || afterTool.VoiceSession.Lease.State != "qualification_revoked" || len(afterTool.Messages) != messageCount {
		t.Fatalf("revoked tool binding=%+v messages=%d want=%d err=%v", afterTool.VoiceSession, len(afterTool.Messages), messageCount, err)
	}
}

func TestPrivateRealtimeVoiceLeaseAcquireReplayConflictRenewStopAndTakeover(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("PRIVATE_REALTIME_VOICE_QUALIFIED", "true")
	app := newIsolatedKanbanBoardApp(t)
	const (
		owner          = "aj@shareability.com"
		sessionHash    = "authenticated-session-hash"
		voiceSessionID = "voice-lease-contract"
	)
	thread, _, err := app.ensurePrivateRealtimeVoiceConversation(owner, "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	offerDigest := privateRealtimeLeaseDigest("offer-sdp", "v=0\r\n")
	claim, err := app.claimPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, "offer-operation-one", offerDigest, startedAt)
	if err != nil || claim.Replay || claim.Generation != 1 || claim.TransportRevision != 1 || claim.LeaseToken == "" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if err := app.finishPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim, true, "answer-sdp-secret", startedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	replay, err := app.claimPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, "offer-operation-one", offerDigest, startedAt.Add(2*time.Second))
	if err != nil || !replay.Replay || replay.LeaseToken != claim.LeaseToken || replay.AnswerSDP != "answer-sdp-secret" || replay.TransportRevision != claim.TransportRevision {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := app.claimPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, "offer-operation-one", privateRealtimeLeaseDigest("offer-sdp", "changed"), startedAt.Add(2*time.Second)); !errors.Is(err, errPrivateRealtimeLeaseConflict) {
		t.Fatalf("changed digest err=%v, want conflict", err)
	}

	other, _, err := app.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-other-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.claimPrivateRealtimeVoiceLease(owner, "different-authenticated-session", "voice-other-client", other.ID, "other-client-operation", privateRealtimeLeaseDigest("offer-sdp", "other"), startedAt.Add(3*time.Second)); !errors.Is(err, errPrivateRealtimeLeaseConflict) {
		t.Fatalf("concurrent other client err=%v, want conflict", err)
	}

	expiresAt, replayed, err := app.renewPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "renew-operation-one", startedAt.Add(5*time.Second))
	if err != nil || replayed || !expiresAt.Equal(startedAt.Add(5*time.Second+privateRealtimeVoiceLeaseTTL)) {
		t.Fatalf("renew expires=%s replayed=%v err=%v", expiresAt, replayed, err)
	}
	replayExpiry, replayed, err := app.renewPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "renew-operation-one", startedAt.Add(6*time.Second))
	if err != nil || !replayed || !replayExpiry.Equal(expiresAt) {
		t.Fatalf("renew replay expires=%s replayed=%v err=%v", replayExpiry, replayed, err)
	}
	newerExpiry, replayed, err := app.renewPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "renew-operation-two", startedAt.Add(7*time.Second))
	if err != nil || replayed || !newerExpiry.After(expiresAt) {
		t.Fatalf("newer renew expires=%s replayed=%v err=%v", newerExpiry, replayed, err)
	}
	oldReplayExpiry, replayed, err := app.renewPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "renew-operation-one", startedAt.Add(8*time.Second))
	if err != nil || !replayed || !oldReplayExpiry.Equal(expiresAt) {
		t.Fatalf("reordered old renew expires=%s replayed=%v err=%v", oldReplayExpiry, replayed, err)
	}
	if _, _, err := app.renewPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation+1, claim.TransportRevision, "stale-renew", startedAt.Add(6*time.Second)); !errors.Is(err, errPrivateRealtimeLeaseStale) {
		t.Fatalf("stale generation renew err=%v", err)
	}
	if _, err := app.stopPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation+1, claim.TransportRevision, "stale-stop", startedAt.Add(6*time.Second)); !errors.Is(err, errPrivateRealtimeLeaseStale) {
		t.Fatalf("stale generation stop err=%v", err)
	}

	stopReplayed, err := app.stopPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "stop-operation-one", startedAt.Add(7*time.Second))
	if err != nil || stopReplayed {
		t.Fatalf("stop replayed=%v err=%v", stopReplayed, err)
	}
	stopReplayed, err = app.stopPrivateRealtimeVoiceLease(owner, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "stop-operation-one", startedAt.Add(8*time.Second))
	if err != nil || !stopReplayed {
		t.Fatalf("stop replay replayed=%v err=%v", stopReplayed, err)
	}
	reloaded, err := app.privateRealtimeVoiceConversation(owner, voiceSessionID, thread.ID)
	if err != nil || reloaded.VoiceSession.Lease.State != "stopped" || reloaded.VoiceSession.TransportAttempts[0].State != "stopped" || reloaded.VoiceSession.TransportAttempts[0].StoppedAt == "" {
		t.Fatalf("stopped binding=%+v err=%v", reloaded.VoiceSession, err)
	}

	takeover, err := app.claimPrivateRealtimeVoiceLease(owner, sessionHash, "voice-other-client", other.ID, "takeover-after-stop", privateRealtimeLeaseDigest("offer-sdp", "takeover"), startedAt.Add(9*time.Second))
	if err != nil || takeover.Generation != 2 || takeover.TransportRevision != 1 {
		t.Fatalf("takeover=%+v err=%v", takeover, err)
	}
}

func TestPrivateRealtimeVoiceLeaseSameClientRecoversImmediatelyAndLateStopCannotKillIt(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const (
		owner       = "aj@shareability.com"
		sessionHash = "one-authenticated-client"
	)
	startedAt := time.Date(2026, time.August, 14, 12, 30, 0, 0, time.UTC)
	firstThread, _, err := app.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-lost-response")
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.claimPrivateRealtimeVoiceLease(owner, sessionHash, "voice-lost-response", firstThread.ID, "offer-lost-response", privateRealtimeLeaseDigest("offer-sdp", "lost"), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.finishPrivateRealtimeVoiceLease(owner, sessionHash, "voice-lost-response", firstThread.ID, first, true, "old-answer", startedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	secondThread, _, err := app.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-immediate-recovery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.claimPrivateRealtimeVoiceLease(owner, sessionHash, "voice-immediate-recovery", secondThread.ID, "offer-immediate-recovery", privateRealtimeLeaseDigest("offer-sdp", "new"), startedAt.Add(2*time.Second))
	if err != nil || second.Generation != first.Generation+1 {
		t.Fatalf("same-client recovery=%+v err=%v", second, err)
	}
	if _, err := app.stopPrivateRealtimeVoiceLease(owner, sessionHash, "voice-lost-response", firstThread.ID, first.LeaseToken, first.Generation, first.TransportRevision, "late-old-stop", startedAt.Add(3*time.Second)); !errors.Is(err, errPrivateRealtimeLeaseStale) {
		t.Fatalf("late old stop err=%v, want stale", err)
	}
	reloaded, err := app.privateRealtimeVoiceConversation(owner, "voice-immediate-recovery", secondThread.ID)
	if err != nil || reloaded.VoiceSession.Lease.Generation != second.Generation || reloaded.VoiceSession.Lease.State != "claimed" {
		t.Fatalf("replacement lease=%+v err=%v", reloaded.VoiceSession, err)
	}
}

func TestPrivateRealtimeOfferReplayCacheSweepsExpiryAndStaysBounded(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	base := time.Date(2026, time.August, 14, 13, 0, 0, 0, time.UTC)
	for index := 0; index < privateRealtimeOfferReplayLimit+44; index++ {
		operationID := fmt.Sprintf("bounded-replay-%03d", index)
		app.rememberPrivateRealtimeOfferReplay("aj@shareability.com", "session", privateRealtimeOfferReplay{
			OperationID: operationID,
			OfferDigest: "digest",
			LeaseToken:  "raw-token",
			AnswerSDP:   "raw-answer",
			ExpiresAt:   base.Add(time.Duration(index+1) * time.Minute),
		}, base)
	}
	app.privateRealtimeOfferReplayMu.Lock()
	boundedCount := len(app.privateRealtimeOfferReplays)
	app.privateRealtimeOfferReplayMu.Unlock()
	if boundedCount != privateRealtimeOfferReplayLimit {
		t.Fatalf("replay cache count=%d, want %d", boundedCount, privateRealtimeOfferReplayLimit)
	}
	app.prunePrivateRealtimeOfferReplays(base.Add(24 * time.Hour))
	app.privateRealtimeOfferReplayMu.Lock()
	remaining := len(app.privateRealtimeOfferReplays)
	app.privateRealtimeOfferReplayMu.Unlock()
	if remaining != 0 {
		t.Fatalf("expired secret-bearing replay entries=%d, want 0", remaining)
	}
}

func TestPrivateRealtimeVoiceConcurrentRecoveryAndOldStopLeaveReplacementActive(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const (
		owner       = "aj@shareability.com"
		sessionHash = "concurrent-recovery-client"
	)
	startedAt := time.Date(2026, time.August, 14, 13, 30, 0, 0, time.UTC)
	oldThread, _, err := app.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-concurrent-old")
	if err != nil {
		t.Fatal(err)
	}
	oldClaim, err := app.claimPrivateRealtimeVoiceLease(owner, sessionHash, "voice-concurrent-old", oldThread.ID, "offer-concurrent-old", privateRealtimeLeaseDigest("offer-sdp", "old"), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.finishPrivateRealtimeVoiceLease(owner, sessionHash, "voice-concurrent-old", oldThread.ID, oldClaim, true, "old-answer", startedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	newThread, _, err := app.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-concurrent-new")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var replacement privateRealtimeLeaseClaim
	var claimErr, stopErr error
	go func() {
		defer wait.Done()
		<-start
		replacement, claimErr = app.claimPrivateRealtimeVoiceLease(owner, sessionHash, "voice-concurrent-new", newThread.ID, "offer-concurrent-new", privateRealtimeLeaseDigest("offer-sdp", "new"), startedAt.Add(2*time.Second))
	}()
	go func() {
		defer wait.Done()
		<-start
		_, stopErr = app.stopPrivateRealtimeVoiceLease(owner, sessionHash, "voice-concurrent-old", oldThread.ID, oldClaim.LeaseToken, oldClaim.Generation, oldClaim.TransportRevision, "concurrent-old-stop", startedAt.Add(2*time.Second))
	}()
	close(start)
	wait.Wait()
	if claimErr != nil || replacement.Generation != oldClaim.Generation+1 {
		t.Fatalf("replacement=%+v claimErr=%v", replacement, claimErr)
	}
	if stopErr != nil && !errors.Is(stopErr, errPrivateRealtimeLeaseStale) {
		t.Fatalf("old stop err=%v", stopErr)
	}
	reloaded, err := app.privateRealtimeVoiceConversation(owner, "voice-concurrent-new", newThread.ID)
	if err != nil || reloaded.VoiceSession.Lease.Generation != replacement.Generation || !privateRealtimeLeaseActive(reloaded.VoiceSession.Lease) {
		t.Fatalf("replacement binding=%+v err=%v", reloaded.VoiceSession, err)
	}
}

func TestPrivateRealtimeLogoutTerminalizesExactLeaseAndImmediateReloginCanClaim(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	const owner = "aj@shareability.com"

	oldCookies := loginAs(t, owner, "B0NFIRE!")
	oldSessionHash := privateRealtimeSessionHashForTest(oldCookies)
	oldThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-before-logout")
	if err != nil {
		t.Fatal(err)
	}
	oldClaim := activatePrivateRealtimeLeaseForTest(t, kanbanApp, owner, "voice-before-logout", oldThread.ID, oldCookies)

	// Another owner's lease proves the revocation is not a process-wide voice
	// teardown disguised as session cleanup.
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	timThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation("tim@shareability.com", "Tim", "voice-other-owner")
	if err != nil {
		t.Fatal(err)
	}
	timClaim := activatePrivateRealtimeLeaseForTest(t, kanbanApp, "tim@shareability.com", "voice-other-owner", timThread.ID, timCookies)

	logout := postAuthJSON(t, "/auth/logout", "", oldCookies)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	terminal, err := kanbanApp.privateRealtimeVoiceConversation(owner, "voice-before-logout", oldThread.ID)
	if err != nil || terminal.VoiceSession.Lease.State != "session_revoked" || terminal.VoiceSession.Lease.TerminalAt == "" || terminal.VoiceSession.TransportAttempts[0].State != "session_revoked" {
		t.Fatalf("logout lease=%+v err=%v", terminal.VoiceSession, err)
	}
	kanbanApp.privateRealtimeOfferReplayMu.Lock()
	for _, replay := range kanbanApp.privateRealtimeOfferReplays {
		if replay.ThreadID == oldThread.ID && replay.Generation == oldClaim.Generation {
			kanbanApp.privateRealtimeOfferReplayMu.Unlock()
			t.Fatal("revoked session retained its secret-bearing offer replay")
		}
	}
	kanbanApp.privateRealtimeOfferReplayMu.Unlock()
	timCurrent, err := kanbanApp.privateRealtimeVoiceConversation("tim@shareability.com", "voice-other-owner", timThread.ID)
	if err != nil || timCurrent.VoiceSession.Lease.Generation != timClaim.Generation || !privateRealtimeLeaseActive(timCurrent.VoiceSession.Lease) {
		t.Fatalf("other owner lease=%+v err=%v", timCurrent.VoiceSession, err)
	}

	// A request that crossed authentication just before logout cannot recreate
	// authority after the store revocation linearizes.
	staleThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-stale-after-logout")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.claimPrivateRealtimeVoiceLease(owner, oldSessionHash, "voice-stale-after-logout", staleThread.ID, "stale-after-logout-offer", privateRealtimeLeaseDigest("offer-sdp", "stale"), time.Now().UTC()); !errors.Is(err, errPrivateRealtimeLeaseStale) {
		t.Fatalf("revoked session claim err=%v, want stale", err)
	}

	newCookies := loginAs(t, owner, "B0NFIRE!")
	newSessionHash := privateRealtimeSessionHashForTest(newCookies)
	if newSessionHash == oldSessionHash {
		t.Fatal("re-login reused the revoked auth session")
	}
	newThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-immediate-relogin")
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	newClaim, err := kanbanApp.claimPrivateRealtimeVoiceLease(owner, newSessionHash, "voice-immediate-relogin", newThread.ID, "immediate-relogin-offer", privateRealtimeLeaseDigest("offer-sdp", "new"), startedAt)
	if err != nil || newClaim.Generation != oldClaim.Generation+1 || !startedAt.Add(privateRealtimeVoiceLeaseTTL).Equal(newClaim.ExpiresAt) {
		t.Fatalf("immediate re-login claim=%+v err=%v", newClaim, err)
	}
	// Password reset/change and membership revocation use the bulk store seams;
	// they inherit the same synchronous exact-session cleanup.
	userSessionStore().destroyAllForEmail(owner)
	bulkRevoked, err := kanbanApp.privateRealtimeVoiceConversation(owner, "voice-immediate-relogin", newThread.ID)
	if err != nil || bulkRevoked.VoiceSession.Lease.State != "session_revoked" {
		t.Fatalf("bulk session revocation lease=%+v err=%v", bulkRevoked.VoiceSession, err)
	}
}

func TestPrivateRealtimeLateOldSessionRevocationCannotKillNewSessionLease(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	const owner = "aj@shareability.com"
	oldCookies := loginAs(t, owner, "B0NFIRE!")
	newCookies := loginAs(t, owner, "B0NFIRE!")
	oldHash := privateRealtimeSessionHashForTest(oldCookies)
	newHash := privateRealtimeSessionHashForTest(newCookies)
	now := time.Now().UTC()

	oldThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-late-logout-old")
	if err != nil {
		t.Fatal(err)
	}
	oldClaim, err := kanbanApp.claimPrivateRealtimeVoiceLease(owner, oldHash, "voice-late-logout-old", oldThread.ID, "late-logout-old-offer", privateRealtimeLeaseDigest("offer-sdp", "old"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.stopPrivateRealtimeVoiceLease(owner, oldHash, "voice-late-logout-old", oldThread.ID, oldClaim.LeaseToken, oldClaim.Generation, oldClaim.TransportRevision, "late-logout-old-stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	newThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-before-late-logout")
	if err != nil {
		t.Fatal(err)
	}
	newClaim, err := kanbanApp.claimPrivateRealtimeVoiceLease(owner, newHash, "voice-before-late-logout", newThread.ID, "new-session-before-late-logout", privateRealtimeLeaseDigest("offer-sdp", "replacement"), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	if logout := postAuthJSON(t, "/auth/logout", "", oldCookies); logout.Code != http.StatusOK {
		t.Fatalf("late logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	// Replaying the already-revoked logout is idempotent as well.
	if replay := postAuthJSON(t, "/auth/logout", "", oldCookies); replay.Code != http.StatusOK {
		t.Fatalf("logout replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	current, err := kanbanApp.privateRealtimeVoiceConversation(owner, "voice-before-late-logout", newThread.ID)
	if err != nil || current.VoiceSession.Lease.Generation != newClaim.Generation || current.VoiceSession.Lease.AuthSessionDigest != privateRealtimeLeaseDigest("auth-session", newHash) || !privateRealtimeLeaseActive(current.VoiceSession.Lease) {
		t.Fatalf("new session lease=%+v err=%v", current.VoiceSession, err)
	}
}

func TestPrivateRealtimeLogoutRaceEventuallyLeavesOnlyReloginLeaseActive(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	const owner = "aj@shareability.com"
	oldCookies := loginAs(t, owner, "B0NFIRE!")
	newCookies := loginAs(t, owner, "B0NFIRE!")
	oldHash := privateRealtimeSessionHashForTest(oldCookies)
	newHash := privateRealtimeSessionHashForTest(newCookies)
	now := time.Now().UTC()
	oldThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-racing-logout-old")
	if err != nil {
		t.Fatal(err)
	}
	oldClaim, err := kanbanApp.claimPrivateRealtimeVoiceLease(owner, oldHash, "voice-racing-logout-old", oldThread.ID, "racing-logout-old-offer", privateRealtimeLeaseDigest("offer-sdp", "old"), now)
	if err != nil {
		t.Fatal(err)
	}
	newThread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-racing-relogin-new")
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var logoutStatus int
	var claim privateRealtimeLeaseClaim
	var claimErr error
	go func() {
		defer wait.Done()
		<-start
		logout := postAuthJSON(t, "/auth/logout", "", oldCookies)
		logoutStatus = logout.Code
	}()
	go func() {
		defer wait.Done()
		<-start
		claim, claimErr = kanbanApp.claimPrivateRealtimeVoiceLease(owner, newHash, "voice-racing-relogin-new", newThread.ID, "racing-relogin-new-offer", privateRealtimeLeaseDigest("offer-sdp", "new"), now.Add(time.Second))
	}()
	close(start)
	wait.Wait()
	if logoutStatus != http.StatusOK {
		t.Fatalf("racing logout status=%d", logoutStatus)
	}
	if errors.Is(claimErr, errPrivateRealtimeLeaseConflict) {
		claim, claimErr = kanbanApp.claimPrivateRealtimeVoiceLease(owner, newHash, "voice-racing-relogin-new", newThread.ID, "racing-relogin-new-offer", privateRealtimeLeaseDigest("offer-sdp", "new"), now.Add(2*time.Second))
	}
	if claimErr != nil || claim.Generation != oldClaim.Generation+1 {
		t.Fatalf("racing relogin claim=%+v err=%v", claim, claimErr)
	}
	oldCurrent, err := kanbanApp.privateRealtimeVoiceConversation(owner, "voice-racing-logout-old", oldThread.ID)
	if err != nil || privateRealtimeLeaseActive(oldCurrent.VoiceSession.Lease) {
		t.Fatalf("old racing lease=%+v err=%v", oldCurrent.VoiceSession, err)
	}
	newCurrent, err := kanbanApp.privateRealtimeVoiceConversation(owner, "voice-racing-relogin-new", newThread.ID)
	if err != nil || newCurrent.VoiceSession.Lease.Generation != claim.Generation || !privateRealtimeLeaseActive(newCurrent.VoiceSession.Lease) {
		t.Fatalf("new racing lease=%+v err=%v", newCurrent.VoiceSession, err)
	}
}

func TestPrivateRealtimeVoiceLeaseExpiryAndRestartSelfHealWithoutSecretPersistence(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const owner = "aj@shareability.com"
	first, _, err := app.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-expiring-one")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := app.ensurePrivateRealtimeVoiceConversation(owner, "AJ", "voice-expiring-two")
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, time.August, 14, 13, 0, 0, 0, time.UTC)
	digest := privateRealtimeLeaseDigest("offer-sdp", "raw-offer-must-not-persist")
	claim, err := app.claimPrivateRealtimeVoiceLease(owner, "restart-session", "voice-expiring-one", first.ID, "restart-operation", digest, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.finishPrivateRealtimeVoiceLease(owner, "restart-session", "voice-expiring-one", first.ID, claim, true, "raw-answer-must-not-persist", startedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := app.privateRealtimeVoiceConversation(owner, "voice-expiring-one", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustLeaseJSON(t, reloaded.VoiceSession))
	if strings.Contains(encoded, claim.LeaseToken) || strings.Contains(encoded, "raw-offer-must-not-persist") || strings.Contains(encoded, "raw-answer-must-not-persist") {
		t.Fatalf("durable binding leaked raw lease material: %s", encoded)
	}

	restarted := newKanbanBoardApp()
	if _, err := restarted.claimPrivateRealtimeVoiceLease(owner, "restart-session", "voice-expiring-one", first.ID, "restart-operation", digest, startedAt.Add(2*time.Second)); !errors.Is(err, errPrivateRealtimeLeaseReplayUnavailable) {
		t.Fatalf("restart exact replay err=%v, want bounded replay unavailable", err)
	}
	takeover, err := restarted.claimPrivateRealtimeVoiceLease(owner, "restart-session", "voice-expiring-two", second.ID, "post-expiry-takeover", privateRealtimeLeaseDigest("offer-sdp", "second-offer"), startedAt.Add(privateRealtimeVoiceLeaseTTL+time.Second))
	if err != nil || takeover.Generation != 2 {
		t.Fatalf("post-expiry takeover=%+v err=%v", takeover, err)
	}
	expired, err := restarted.privateRealtimeVoiceConversation(owner, "voice-expiring-one", first.ID)
	if err != nil || expired.VoiceSession.Lease.State != "expired" || expired.VoiceSession.TransportAttempts[0].State != "expired" {
		t.Fatalf("expired binding=%+v err=%v", expired.VoiceSession, err)
	}
}

func mustLeaseJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
