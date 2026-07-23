package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func consentLaneTestBinding(principalID, roomID, sittingID string) ConsentAdmissionBinding {
	return ConsentAdmissionBinding{
		TenantID: "tenant-1", PrincipalKind: ACLPrincipalUser, PrincipalID: principalID,
		RoomID: roomID, SittingID: sittingID, AnchorID: "anchor-1",
	}
}

func grantConsentScope(t *testing.T, authority *ConsentLaneAuthority, binding ConsentAdmissionBinding, scope ConsentScope) ConsentRecord {
	t.Helper()
	record, err := authority.RecordDecision(context.Background(), binding, scope, ConsentGranted)
	if err != nil {
		t.Fatalf("grant %s: %v", scope, err)
	}
	return record
}

func installConsentAuthorityForTest(t *testing.T, authority *ConsentLaneAuthority) {
	t.Helper()
	consentAuthorityRuntime.Lock()
	previousOverride := consentAuthorityRuntime.override
	consentAuthorityRuntime.override = authority
	consentAuthorityRuntime.Unlock()
	t.Cleanup(func() {
		consentAuthorityRuntime.Lock()
		consentAuthorityRuntime.override = previousOverride
		consentAuthorityRuntime.Unlock()
	})
}

func enableFullTranscriptConsentForTest(t *testing.T, app *kanbanBoardApp, principal CanonicalPrincipalRef, roomID, sittingID string) *ConsentLaneAuthority {
	t.Helper()
	store := NewMemoryConsentStore()
	authority := NewConsentLaneAuthority(store, "policy-test-v1")
	authority.CaptureCutoff = func() (uint64, error) { return 0, nil }
	installConsentAuthorityForTest(t, authority)
	binding, err := app.consentBindingForPrincipal(context.Background(), principal, roomID, sittingID)
	if err != nil {
		t.Fatalf("resolve transcript consent binding: %v", err)
	}
	for _, scope := range []ConsentScope{ConsentAudioCapture, ConsentTranscription, ConsentModelAnalysis, ConsentOrgMemory} {
		grantConsentScope(t, authority, binding, scope)
	}
	return authority
}

func newAmbientConsentAuthorityForTest(t *testing.T) *ConsentLaneAuthority {
	t.Helper()
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "ambient-policy-test-v1")
	authority.CaptureCutoff = func() (uint64, error) { return 0, nil }
	installConsentAuthorityForTest(t, authority)
	return authority
}

// grantAmbientConsentForTest installs exact durable anchors and grants for
// tests that intentionally synthesize transcript rows without going through a
// live websocket admission. It is explicit per room, sitting, and principal;
// production has no corresponding bypass.
func grantAmbientConsentForTest(t *testing.T, app *kanbanBoardApp, authority *ConsentLaneAuthority, roomID string, emails ...string) string {
	t.Helper()
	roomID = normalizeRoomID(roomID)
	sittingID := app.memory.ensureMeetingID(roomID)
	for _, email := range emails {
		principal := memberAdmissionPrincipal(email)
		anchor := AdmissionAnchor{
			TenantID: canonicalTenantID(), RoomID: roomID, SittingID: sittingID, Principal: principal,
			AdmittedAt: time.Now().UTC(), CaptureWatermark: time.Now().UTC(),
		}
		anchor.AnchorID = deterministicAdmissionAnchorID(anchor)
		if _, err := app.admissionAnchors.RecordFirst(context.Background(), anchor); err != nil {
			t.Fatal(err)
		}
		binding, err := app.consentBindingForPrincipal(context.Background(), principal, roomID, sittingID)
		if err != nil {
			t.Fatal(err)
		}
		for _, scope := range []ConsentScope{ConsentAudioCapture, ConsentTranscription, ConsentModelAnalysis, ConsentOrgMemory} {
			grantConsentScope(t, authority, binding, scope)
		}
	}
	return sittingID
}

func attributeNextTranscriptForTest(app *kanbanBoardApp, roomID, participant string) {
	now := time.Now().UTC()
	app.noteAudioActivityForRoom(roomID, now, []audioActivityLevel{{
		TrackKey: "consent-test-track", ParticipantName: participant, RMS: 900,
	}})
	app.noteRealtimeSpeechStartedForRoom(roomID)
	app.noteRealtimeSpeechStoppedForRoom(roomID)
	app.freezeAttributionWindowAtCommitForRoom(roomID)
}

func admitMemberWithTranscriptConsentForTest(t *testing.T, app *kanbanBoardApp, roomID, email string) string {
	t.Helper()
	sittingID := app.prepareMeetingSittingID(roomID)
	name := participantNameForEmail(email)
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), roomID, name, "transcript-consent-session", "transcript-consent-endpoint", sittingID, memberAdmissionPrincipal(email)); err != nil {
		t.Fatalf("admit transcript member: %v", err)
	}
	if got := app.noteMeetingAdmissionForSitting(roomID, name, sittingID); got != sittingID {
		t.Fatalf("open transcript sitting=%q want=%q", got, sittingID)
	}
	enableFullTranscriptConsentForTest(t, app, memberAdmissionPrincipal(email), roomID, sittingID)
	return sittingID
}

func TestConsentLanesKeepTransportSeparateAndEnforceDependencies(t *testing.T) {
	binding := consentLaneTestBinding("user-1", "room-1", "sitting-1")
	unavailable := NewConsentLaneAuthority(nil, "policy-v1")
	if decision, err := unavailable.Authorize(context.Background(), binding, ConsentLaneAudioTransport); err != nil || !decision.Allowed {
		t.Fatalf("direct audio transport decision=%+v err=%v, want allowed despite consent outage", decision, err)
	}
	if decision, err := unavailable.Authorize(context.Background(), binding, ConsentLaneAudioCapture); !errors.Is(err, ErrConsentAuthorityUnavailable) || decision.Allowed {
		t.Fatalf("capture decision=%+v err=%v, want fail closed", decision, err)
	}

	store := NewMemoryConsentStore()
	authority := NewConsentLaneAuthority(store, "policy-v1")
	grantConsentScope(t, authority, binding, ConsentOrgMemory)
	decision, err := authority.Authorize(context.Background(), binding, ConsentLaneOrgMemory)
	if err != nil || decision.Allowed {
		t.Fatalf("later-only org grant decision=%+v err=%v, want denied", decision, err)
	}
	wantMissing := []ConsentScope{ConsentAudioCapture, ConsentModelAnalysis, ConsentTranscription}
	for _, scope := range wantMissing {
		found := false
		for _, missing := range decision.MissingScopes {
			found = found || missing == scope
		}
		if !found {
			t.Fatalf("missing scopes=%v, want dependency %s", decision.MissingScopes, scope)
		}
	}
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	grantConsentScope(t, authority, binding, ConsentTranscription)
	grantConsentScope(t, authority, binding, ConsentModelAnalysis)
	if decision, err := authority.Authorize(context.Background(), binding, ConsentLaneOrgMemory); err != nil || !decision.Allowed {
		t.Fatalf("complete chain decision=%+v err=%v", decision, err)
	}
}

func TestConsentWithdrawalStampsServerCutoffAndInvalidatesInFlightFence(t *testing.T) {
	store := NewMemoryConsentStore()
	authority := NewConsentLaneAuthority(store, "policy-v1")
	authority.CaptureCutoff = func() (uint64, error) { return 73, nil }
	var notice ConsentWithdrawalNotice
	authority.OnWithdrawal = func(received ConsentWithdrawalNotice) { notice = received }
	binding := consentLaneTestBinding("user-1", "room-1", "sitting-1")
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	grantConsentScope(t, authority, binding, ConsentTranscription)
	before, err := authority.Authorize(context.Background(), binding, ConsentLaneTranscription)
	if err != nil || !before.Allowed {
		t.Fatalf("pre-withdraw decision=%+v err=%v", before, err)
	}
	record, err := authority.RecordDecision(context.Background(), binding, ConsentAudioCapture, ConsentWithdrawn)
	if err != nil {
		t.Fatal(err)
	}
	if record.LastAcceptedCaptureSequence == nil || *record.LastAcceptedCaptureSequence != 73 {
		t.Fatalf("withdrawal cutoff=%v, want server-stamped 73", record.LastAcceptedCaptureSequence)
	}
	if notice.RecordID != record.ID || notice.Scope != ConsentAudioCapture || notice.LastAcceptedCaptureSequence != 73 || notice.Binding.SittingID != binding.SittingID {
		t.Fatalf("withdrawal notice=%+v, want durable exact-binding invalidation", notice)
	}
	if err := authority.ValidateFence(context.Background(), before.Fence); !errors.Is(err, ErrConsentFenceStale) {
		t.Fatalf("old fence err=%v, want stale", err)
	}
	if after, err := authority.Authorize(context.Background(), binding, ConsentLaneTranscription); err != nil || after.Allowed {
		t.Fatalf("post-withdraw decision=%+v err=%v, want denied", after, err)
	}
}

func TestCommitWithFencesUsesStableMultiContributorLockOrder(t *testing.T) {
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1")
	bindings := []ConsentAdmissionBinding{
		consentLaneTestBinding("user-a", "room-1", "sitting-1"),
		consentLaneTestBinding("user-b", "room-1", "sitting-1"),
	}
	fences := make([]ConsentFence, 0, len(bindings))
	for _, binding := range bindings {
		grantConsentScope(t, authority, binding, ConsentAudioCapture)
		grantConsentScope(t, authority, binding, ConsentTranscription)
		decision, err := authority.Authorize(context.Background(), binding, ConsentLaneTranscription)
		if err != nil || !decision.Allowed {
			t.Fatalf("decision=%+v err=%v", decision, err)
		}
		fences = append(fences, decision.Fence)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, ordered := range [][]ConsentFence{fences, {fences[1], fences[0]}} {
		ordered := append([]ConsentFence(nil), ordered...)
		go func() {
			<-start
			done <- authority.CommitWithFences(context.Background(), ordered, func() error {
				time.Sleep(10 * time.Millisecond)
				return nil
			})
		}()
	}
	close(start)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("opposite contributor orders deadlocked")
		}
	}
}

func TestConsentDecisionSurvivesAuthorityRestartAndNeverCrossesScope(t *testing.T) {
	store := NewMemoryConsentStore()
	binding := consentLaneTestBinding("user-1", "room-1", "sitting-1")
	first := NewConsentLaneAuthority(store, "policy-v1")
	grantConsentScope(t, first, binding, ConsentAudioCapture)

	restarted := NewConsentLaneAuthority(store, "policy-v1")
	if decision, err := restarted.Authorize(context.Background(), binding, ConsentLaneAudioCapture); err != nil || !decision.Allowed {
		t.Fatalf("restart decision=%+v err=%v", decision, err)
	}
	mutations := []ConsentAdmissionBinding{
		func() ConsentAdmissionBinding { value := binding; value.PrincipalID = "user-2"; return value }(),
		func() ConsentAdmissionBinding { value := binding; value.RoomID = "room-2"; return value }(),
		func() ConsentAdmissionBinding { value := binding; value.SittingID = "sitting-2"; return value }(),
	}
	for index, mutated := range mutations {
		if decision, err := restarted.Authorize(context.Background(), mutated, ConsentLaneAudioCapture); err != nil || decision.Allowed {
			t.Fatalf("mutation %d decision=%+v err=%v, inherited another admission", index, decision, err)
		}
	}
	wrongPolicy := NewConsentLaneAuthority(store, "policy-v2")
	if decision, err := wrongPolicy.Authorize(context.Background(), binding, ConsentLaneAudioCapture); err != nil || decision.Allowed {
		t.Fatalf("later policy decision=%+v err=%v, inherited old policy", decision, err)
	}
}

func TestConsentHTTPDerivesAdmissionAndRejectsSelfAttestedFields(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	sittingID := app.prepareMeetingSittingID(officeRoomID)
	name := participantNameForEmail("aj@shareability.com")
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, name, "consent-session", "consent-endpoint", sittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}
	if got := app.noteMeetingAdmissionForSitting(officeRoomID, name, sittingID); got != sittingID {
		t.Fatalf("opened sitting=%q want=%q", got, sittingID)
	}

	store := NewMemoryConsentStore()
	authority := NewConsentLaneAuthority(store, "policy-v1")
	authority.Now = func() time.Time { return time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) }
	authority.CaptureCutoff = func() (uint64, error) { return 88, nil }
	installConsentAuthorityForTest(t, authority)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/consent", strings.NewReader(body))
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		consentHandler(recorder, req)
		return recorder
	}
	if recorder := post(`{"scope":"audio_capture","disposition":"granted","principalId":"attacker"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("self-attested identity status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.records) != 0 {
		t.Fatalf("self-attested request persisted records=%d", len(store.records))
	}
	recorder := post(`{"scope":"audio_capture","disposition":"granted"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("grant status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.records) != 1 {
		t.Fatalf("records=%d want=1", len(store.records))
	}
	persisted := store.records[0]
	if persisted.PrincipalID != "aj@shareability.com" || persisted.RoomID != officeRoomID || persisted.SittingID != sittingID || persisted.PolicyVersion != "policy-v1" || persisted.EvidenceKind != "server_authenticated_choice" {
		t.Fatalf("server-derived record=%+v", persisted)
	}
	if persisted.EvidenceRef == "" || persisted.RecordedAt.IsZero() {
		t.Fatalf("missing server evidence=%+v", persisted)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/consent", nil)
	for _, cookie := range cookies {
		get.AddCookie(cookie)
	}
	getRecorder := httptest.NewRecorder()
	consentHandler(getRecorder, get)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	var response consentStatusResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Lanes[ConsentLaneAudioTransport].Allowed || !response.Lanes[ConsentLaneAudioCapture].Allowed || response.Lanes[ConsentLaneTranscription].Allowed {
		t.Fatalf("lane response=%+v", response.Lanes)
	}
	if response.Scopes[ConsentAudioCapture] != ConsentGranted {
		t.Fatalf("scope response=%+v, want explicit audio_capture grant", response.Scopes)
	}
}

func TestConsentCommitAndWithdrawalCannotInterleave(t *testing.T) {
	binding := consentLaneTestBinding("user-1", "room-1", "sitting-1")
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1")
	authority.CaptureCutoff = func() (uint64, error) { return 17, nil }
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	grantConsentScope(t, authority, binding, ConsentTranscription)
	decision, err := authority.Authorize(context.Background(), binding, ConsentLaneTranscription)
	if err != nil || !decision.Allowed {
		t.Fatalf("authorize=%+v err=%v", decision, err)
	}

	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- authority.CommitWithFence(context.Background(), decision.Fence, func() error {
			close(commitEntered)
			<-releaseCommit
			return nil
		})
	}()
	<-commitEntered
	withdrawDone := make(chan error, 1)
	go func() {
		_, err := authority.RecordDecision(context.Background(), binding, ConsentTranscription, ConsentWithdrawn)
		withdrawDone <- err
	}()
	select {
	case err := <-withdrawDone:
		t.Fatalf("withdrawal crossed active commit: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-commitDone; err != nil {
		t.Fatal(err)
	}
	if err := <-withdrawDone; err != nil {
		t.Fatal(err)
	}
	if err := authority.ValidateFence(context.Background(), decision.Fence); !errors.Is(err, ErrConsentFenceStale) {
		t.Fatalf("pre-withdraw fence err=%v, want stale", err)
	}
}

func TestConsentMixerUsesExactFrameAndPurgesWithdrawalBuffer(t *testing.T) {
	binding := consentLaneTestBinding("user-1", "room-1", "sitting-1")
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1")
	fence := ConsentFence{binding: binding, lane: ConsentLaneTranscription, policy: "policy-v1", issuedAt: time.Now().UTC()}
	source := &audioSource{
		trackKey: "track-1", participantName: "AJ",
		buffer:      make([]int16, roomAudioMixFrameSize),
		laneBuffers: map[ConsentLane][]int16{ConsentLaneTranscription: make([]int16, roomAudioMixFrameSize)},
		laneFences:  map[ConsentLane]ConsentFence{ConsentLaneTranscription: fence}, captureFence: fence,
	}
	for index := range source.buffer {
		source.buffer[index] = 1000
		source.laneBuffers[ConsentLaneTranscription][index] = 321
	}
	_, _, activities := mixAudioFrameSetWithActivity(map[string]*audioSource{"track-1": source})
	if len(source.laneBuffers[ConsentLaneTranscription]) != 0 {
		t.Fatal("source lane frame was not consumed")
	}
	mixed, fences := mixConsentLaneFrame(activities, ConsentLaneTranscription, authority)
	if len(fences) != 1 || len(mixed) != roomAudioMixFrameSize || mixed[0] != 321 {
		t.Fatalf("mixed len=%d sample=%d fences=%d, want exact queued frame", len(mixed), mixed[0], len(fences))
	}
	source.laneBuffers[ConsentLaneTranscription] = []int16{8, 9, 10}
	invalidateMixerSourcesForWithdrawal(map[string]*audioSource{"track-1": source}, ConsentWithdrawalNotice{Binding: binding, Scope: ConsentTranscription})
	if _, ok := source.laneFences[ConsentLaneTranscription]; ok {
		t.Fatal("withdrawn lane fence survived")
	}
	for _, sample := range source.laneBuffers[ConsentLaneTranscription] {
		if sample != 0 {
			t.Fatalf("withdrawn buffered sample=%d, want purge to silence", sample)
		}
	}
}

func TestTranscriptionProviderIngressRejectsWithdrawnFence(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	binding := consentLaneTestBinding("user-1", officeRoomID, "sitting-1")
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1")
	authority.CaptureCutoff = func() (uint64, error) { return 4, nil }
	installConsentAuthorityForTest(t, authority)
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	grantConsentScope(t, authority, binding, ConsentTranscription)
	decision, err := authority.Authorize(context.Background(), binding, ConsentLaneTranscription)
	if err != nil || !decision.Allowed {
		t.Fatalf("authorize=%+v err=%v", decision, err)
	}
	lane := &meetingTranscriptionLane{consentInput: make(chan consentAudioFrame, 2)}
	app.mu.Lock()
	app.transcriptLane = lane
	app.mu.Unlock()
	sink := &roomLaneAudioSink{app: app, roomID: officeRoomID, lane: ConsentLaneTranscription}
	pcm := make([]int16, roomAudioMixFrameSize)
	for index := range pcm {
		pcm[index] = 900
	}
	if err := sink.WriteMixedPCMWithConsent(pcm, []ConsentFence{decision.Fence}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lane.consentInput:
	case <-time.After(5 * time.Second):
		t.Fatal("authorized provider frame was not queued")
	}
	if _, err := authority.RecordDecision(context.Background(), binding, ConsentTranscription, ConsentWithdrawn); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteMixedPCMWithConsent(pcm, []ConsentFence{decision.Fence}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lane.consentInput:
		t.Fatal("withdrawn fence reached provider queue")
	case <-time.After(40 * time.Millisecond):
	}
}

func TestTranscriptCaptureIdentityIsReservedBeforeProviderCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	stamp, err := store.reserveTranscriptCapture(started)
	if err != nil {
		t.Fatal(err)
	}
	cutoff, err := currentDurableCaptureSequence(path, 0)
	if err != nil || cutoff != stamp.CaptureSequence {
		t.Fatalf("cutoff=%d stamp=%d err=%v", cutoff, stamp.CaptureSequence, err)
	}
	stamp.OccurredEnd = started.Add(3 * time.Second)
	entry, appended, err := store.appendAttributedTranscriptEntryWithCapture(officeRoomID, "late-stt", "", "AJ", "dominant", "speech completed after admission", nil, true, "", stamp)
	if err != nil || !appended {
		t.Fatalf("append=%v err=%v", appended, err)
	}
	if entry.Metadata["captureSequence"] != fmt.Sprint(cutoff) || entry.Metadata["occurredStart"] != started.Format(time.RFC3339Nano) || entry.Metadata["occurredEnd"] != stamp.OccurredEnd.Format(time.RFC3339Nano) {
		t.Fatalf("capture metadata=%v", entry.Metadata)
	}
}

func TestConsentHTTPRequiresCurrentSeatEvenWithValidLogin(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	installConsentAuthorityForTest(t, NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1"))

	req := httptest.NewRequest(http.MethodGet, "/api/consent", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	consentHandler(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("seatless status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestConsentHTTPRejectsUnauthenticatedPrincipalBeforeStoreAccess(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	installConsentAuthorityForTest(t, NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1"))
	recorder := httptest.NewRecorder()
	consentHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/consent", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRememberTranscriptChecksDurableConsentAtTheCommitSeam(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	sittingID := app.prepareMeetingSittingID(officeRoomID)
	name := participantNameForEmail("aj@shareability.com")
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, name, "commit-session", "commit-endpoint", sittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}
	if got := app.noteMeetingAdmissionForSitting(officeRoomID, name, sittingID); got != sittingID {
		t.Fatalf("opened sitting=%q want=%q", got, sittingID)
	}
	binding, err := app.consentBindingForPrincipal(context.Background(), memberAdmissionPrincipal("aj@shareability.com"), officeRoomID, sittingID)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryConsentStore()
	authority := NewConsentLaneAuthority(store, "policy-v1")
	nextRecordAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	authority.Now = func() time.Time {
		nextRecordAt = nextRecordAt.Add(time.Second)
		return nextRecordAt
	}
	authority.CaptureCutoff = func() (uint64, error) { return 19, nil }
	installConsentAuthorityForTest(t, authority)

	attributeNextTranscriptForTest(app, officeRoomID, name)
	app.rememberTranscript(officeRoomID, kanbanRealtimeEvent{EventID: "commit-denied", Transcript: "must not persist before consent"}, "transcript_lane", "test-model")
	if entries := app.memorySnapshot(10); len(entries) != 0 {
		t.Fatalf("pre-consent entries=%d want=0", len(entries))
	}
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	grantConsentScope(t, authority, binding, ConsentTranscription)
	attributeNextTranscriptForTest(app, officeRoomID, name)
	app.rememberTranscript(officeRoomID, kanbanRealtimeEvent{EventID: "commit-allowed", Transcript: "transcription only is allowed"}, "transcript_lane", "test-model")
	entries := app.memorySnapshot(10)
	if len(entries) != 1 {
		t.Fatalf("authorized entries=%d want=1", len(entries))
	}
	if entries[0].Metadata["consentTranscriptionRecordIds"] == "" || entries[0].Metadata["consentPolicyVersion"] != "policy-v1" {
		t.Fatalf("audit metadata=%v", entries[0].Metadata)
	}
	if entries[0].Metadata["consentModelAnalysisRecordIds"] != "" || entries[0].Metadata["consentOrgMemoryRecordIds"] != "" {
		t.Fatalf("later lanes were self-attested by a transcription grant: %v", entries[0].Metadata)
	}
	if _, err := authority.RecordDecision(context.Background(), binding, ConsentAudioCapture, ConsentWithdrawn); err != nil {
		t.Fatal(err)
	}
	attributeNextTranscriptForTest(app, officeRoomID, name)
	app.rememberTranscript(officeRoomID, kanbanRealtimeEvent{EventID: "commit-withdrawn", Transcript: "must not persist after withdrawal"}, "transcript_lane", "test-model")
	if entries := app.memorySnapshot(10); len(entries) != 1 {
		t.Fatalf("post-withdraw entries=%d want=1", len(entries))
	}
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	// No frozen attribution window means there is no speaker principal. Even
	// with valid room consent, unknown audio is discarded rather than assigned
	// to whichever participant happens to be present.
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.audioActivity = nil
	state.currentSpeechStartedAt = time.Time{}
	state.currentSpeechStoppedAt = time.Time{}
	state.pendingAttributionWindows = nil
	app.mu.Unlock()
	app.rememberTranscript(officeRoomID, kanbanRealtimeEvent{EventID: "commit-unknown", Transcript: "unknown speaker must fail closed"}, "transcript_lane", "test-model")
	if entries := app.memorySnapshot(10); len(entries) != 1 {
		t.Fatalf("unknown-speaker entries=%d want=1", len(entries))
	}
}

func TestMixedTranscriptRequiresOrgMemoryConsentFromEveryContributor(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	roomID := officeRoomID
	sittingID := app.prepareMeetingSittingID(roomID)
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "mixed-policy-v1")
	authority.CaptureCutoff = func() (uint64, error) { return 0, nil }
	installConsentAuthorityForTest(t, authority)

	type contributor struct {
		email string
		name  string
	}
	contributors := []contributor{
		{email: "aj@shareability.com", name: participantNameForEmail("aj@shareability.com")},
		{email: "tyler@shareability.com", name: participantNameForEmail("tyler@shareability.com")},
	}
	bindings := make([]ConsentAdmissionBinding, 0, len(contributors))
	for index, contributor := range contributors {
		if _, _, err := app.admitParticipantWithAnchor(context.Background(), roomID, contributor.name, fmt.Sprintf("mixed-session-%d", index), fmt.Sprintf("mixed-endpoint-%d", index), sittingID, memberAdmissionPrincipal(contributor.email)); err != nil {
			t.Fatal(err)
		}
		if got := app.noteMeetingAdmissionForSitting(roomID, contributor.name, sittingID); got != sittingID {
			t.Fatalf("sitting=%q want=%q", got, sittingID)
		}
		binding, err := app.consentBindingForPrincipal(context.Background(), memberAdmissionPrincipal(contributor.email), roomID, sittingID)
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, binding)
		for _, scope := range []ConsentScope{ConsentAudioCapture, ConsentTranscription, ConsentModelAnalysis} {
			grantConsentScope(t, authority, binding, scope)
		}
	}
	// Only the dominant speaker grants organization memory. The quieter second
	// speaker still contributed samples and therefore retains veto authority.
	grantConsentScope(t, authority, bindings[0], ConsentOrgMemory)
	fences := make([]ConsentFence, 0, len(bindings))
	for _, binding := range bindings {
		decision, err := authority.Authorize(context.Background(), binding, ConsentLaneTranscription)
		if err != nil || !decision.Allowed {
			t.Fatalf("transcription decision=%+v err=%v", decision, err)
		}
		fences = append(fences, decision.Fence)
	}
	now := time.Now().UTC()
	app.noteAudioActivityForRoom(roomID, now, []audioActivityLevel{
		{TrackKey: "mixed-a", ParticipantName: contributors[0].name, RMS: 1200},
		{TrackKey: "mixed-b", ParticipantName: contributors[1].name, RMS: 400},
	})
	app.noteRealtimeSpeechStartedForRoom(roomID)
	app.noteRealtimeSpeechStoppedForRoom(roomID)
	app.freezeAttributionWindowAtCommitForRoomWithCaptureAndConsent(roomID, nil, fences)
	app.rememberTranscript(roomID, kanbanRealtimeEvent{EventID: "mixed-two-speaker", Transcript: "AJ led and Tyler added a confidential constraint."}, "transcript_lane", "test-model")

	entries := app.memorySnapshot(10)
	if len(entries) != 1 {
		t.Fatalf("transcript entries=%d want=1", len(entries))
	}
	storedBindings, err := decodeConsentContributorBindings(entries[0].Metadata[consentContributorBindingsMetadataKey])
	if err != nil || len(storedBindings) != 2 {
		t.Fatalf("stored contributors=%+v err=%v", storedBindings, err)
	}
	verifier := appBrainSourceConsentVerifier{App: app}
	if _, err := verifier.AuthorizeBrainSourceConsent(context.Background(), entries[0]); !errors.Is(err, ErrBrainSourceConsentAbsent) {
		t.Fatalf("mixed org-memory decision err=%v, want contributor denial", err)
	}
	if entry, err := app.produceMeetingBrainWriteUp(context.Background(), "test-key", entries, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("denied contributor's words reached the brain provider")
		return "", nil
	}); err != nil || entry.ID != "" {
		t.Fatalf("brain entry=%+v err=%v, want omitted", entry, err)
	}
}

func TestOfficeScoutConsentWithdrawalCancelsAndWaitsForBlockedMutatingTool(t *testing.T) {
	app := newW2ATestApp(t)
	defer app.Close()
	sittingID := app.memory.ensureMeetingID(officeRoomID)
	binding := consentLaneTestBinding("office-user", officeRoomID, sittingID)
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "office-work-policy-v1")
	authority.CaptureCutoff = func() (uint64, error) { return 0, nil }
	authority.OnWithdrawal = func(notice ConsentWithdrawalNotice) {
		app.cancelOfficeScoutWorkForSitting(notice.Binding.SittingID)
	}
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	grantConsentScope(t, authority, binding, ConsentModelAnalysis)

	entered := make(chan struct{})
	exited := make(chan struct{})
	toolDone := make(chan error, 1)
	go func() {
		toolDone <- app.runOfficeScoutWorkFenced(context.Background(), sittingID, func(ctx context.Context, _ uint64) error {
			close(entered)
			<-ctx.Done()
			close(exited)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, _, err := app.applyToolCallArgs("create_ticket", map[string]any{"title": "must not be created"})
			return err
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("mutating tool did not enter blocked work")
	}
	withdrawDone := make(chan error, 1)
	go func() {
		_, err := authority.RecordDecision(context.Background(), binding, ConsentModelAnalysis, ConsentWithdrawn)
		withdrawDone <- err
	}()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("withdrawal did not cancel the office tool context")
	}
	select {
	case err := <-withdrawDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("withdrawal did not wait for the blocked tool")
	}
	if err := <-toolDone; !errors.Is(err, ErrRoomScoutFence) {
		t.Fatalf("tool err=%v, want office consent epoch fence", err)
	}
	for _, card := range app.snapshotState().Cards {
		if card.Title == "must not be created" {
			t.Fatal("canceled office Scout mutation was committed")
		}
	}
}

func TestOfficeScoutWithdrawalAfterPrecheckCannotCommitBoardOrArtifactMutation(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{name: "board card", tool: "create_ticket", args: map[string]any{"title": "post-withdrawal card canary"}},
		{name: "artifact", tool: "create_artifact", args: map[string]any{
			"mode": "research", "query": "post-withdrawal artifact canary", "content": "must not persist",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newW2ATestApp(t)
			defer app.Close()
			sittingID := app.memory.ensureMeetingID(officeRoomID)
			binding := consentLaneTestBinding("office-gap-user", officeRoomID, sittingID)
			authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "office-gap-policy-v1")
			authority.CaptureCutoff = func() (uint64, error) { return 0, nil }
			installConsentAuthorityForTest(t, authority)
			grantConsentScope(t, authority, binding, ConsentAudioCapture)
			grantConsentScope(t, authority, binding, ConsentModelAnalysis)

			prechecked := make(chan struct{})
			releaseCommit := make(chan struct{})
			app.mu.Lock()
			app.officeToolBeforeCommit = func() {
				close(prechecked)
				<-releaseCommit
			}
			app.mu.Unlock()
			defer func() {
				app.mu.Lock()
				app.officeToolBeforeCommit = nil
				app.mu.Unlock()
			}()

			durableWithdrawal := make(chan struct{})
			authority.OnWithdrawal = func(notice ConsentWithdrawalNotice) {
				close(durableWithdrawal)
				app.cancelOfficeScoutWorkForSitting(notice.Binding.SittingID)
			}
			beforeArtifacts := len(app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 100))
			fence := authority.roomDecisionFence(binding.TenantID, binding.RoomID, binding.SittingID)
			toolDone := make(chan error, 1)
			go func() {
				toolDone <- app.runOfficeScoutWorkFenced(context.Background(), sittingID, func(workCtx context.Context, epoch uint64) error {
					return app.finishToolCallInEpoch(workCtx, epoch, sittingID, kanbanRealtimeOutputItem{
						Name: test.tool, CallID: "gap-" + test.name,
					}, test.args, nil, false, fence)
				})
			}()
			select {
			case <-prechecked:
			case <-time.After(5 * time.Second):
				t.Fatal("tool did not reach the post-precheck commit barrier")
			}

			withdrawDone := make(chan error, 1)
			go func() {
				_, err := authority.RecordDecision(context.Background(), binding, ConsentModelAnalysis, ConsentWithdrawn)
				withdrawDone <- err
			}()
			select {
			case <-durableWithdrawal:
			case <-time.After(5 * time.Second):
				t.Fatal("withdrawal did not become durable while tool was parked")
			}
			select {
			case err := <-withdrawDone:
				t.Fatalf("withdrawal returned before stale tool drained: %v", err)
			default:
			}
			close(releaseCommit)
			select {
			case err := <-toolDone:
				if !errors.Is(err, ErrRoomScoutFence) {
					t.Fatalf("tool err=%v, want room consent fence", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stale tool did not drain")
			}
			select {
			case err := <-withdrawDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("withdrawal did not finish after stale tool drained")
			}

			for _, card := range app.snapshotState().Cards {
				if card.Title == "post-withdrawal card canary" {
					t.Fatal("board mutation committed after durable withdrawal")
				}
			}
			if after := len(app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 100)); after != beforeArtifacts {
				t.Fatalf("artifact count=%d, want unchanged %d", after, beforeArtifacts)
			}
		})
	}
}

func TestGuestConsentRequiresOneWaySessionPrincipalAndPolicyStaysSeparate(t *testing.T) {
	plain := consentFixture("guest-plain", ConsentGranted, ConsentAudioCapture)
	plain.PrincipalKind = ACLPrincipalGuest
	plain.PrincipalID = "guest-session-cookie"
	if err := plain.Validate(); !errors.Is(err, ErrConsentInvalid) {
		t.Fatalf("plaintext guest principal err=%v", err)
	}
	digest := strings.Repeat("b", 64)
	binding := ConsentAdmissionBinding{
		TenantID: "tenant-1", PrincipalKind: ACLPrincipalGuest, PrincipalID: digest,
		RoomID: "room-guest", SittingID: "sitting-guest", AnchorID: "anchor-guest",
		GuestPolicyListenOnly: true,
	}
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1")
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	decision, err := authority.Authorize(context.Background(), binding, ConsentLaneAudioCapture)
	if err != nil || !decision.Allowed || !decision.Fence.binding.GuestPolicyListenOnly {
		t.Fatalf("guest decision=%+v err=%v", decision, err)
	}
}

func TestWithdrawalNotificationsCannotDropWhenQueuesAreSaturated(t *testing.T) {
	notice := ConsentWithdrawalNotice{
		Binding:  consentLaneTestBinding("person-a", "room-a", "sitting-a"),
		Scope:    ConsentAudioCapture,
		RecordID: "withdrawal-delivered",
	}

	t.Run("mixer", func(t *testing.T) {
		mixer := &audioMixer{input: make(chan audioInput, 1), stop: make(chan struct{})}
		mixer.input <- audioInput{trackKey: "already-queued"}
		returned := make(chan struct{})
		go func() {
			mixer.noteWithdrawal(notice)
			close(returned)
		}()
		select {
		case <-returned:
			t.Fatal("saturated mixer silently dropped withdrawal")
		case <-time.After(50 * time.Millisecond):
		}
		<-mixer.input
		select {
		case delivered := <-mixer.input:
			if delivered.withdrawal == nil || delivered.withdrawal.RecordID != notice.RecordID {
				t.Fatalf("delivered=%+v, want withdrawal", delivered)
			}
		case <-time.After(time.Second):
			t.Fatal("mixer withdrawal was not delivered after capacity became available")
		}
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("mixer withdrawal sender remained blocked")
		}
		close(mixer.stop)
	})

	t.Run("transcription", func(t *testing.T) {
		lane := &meetingTranscriptionLane{roomID: "room-a", withdrawals: make(chan ConsentWithdrawalNotice, 1), stop: make(chan struct{})}
		lane.withdrawals <- ConsentWithdrawalNotice{RecordID: "already-queued"}
		returned := make(chan struct{})
		go func() {
			lane.noteWithdrawal(notice)
			close(returned)
		}()
		select {
		case <-returned:
			t.Fatal("saturated transcription lane silently dropped withdrawal")
		case <-time.After(50 * time.Millisecond):
		}
		<-lane.withdrawals
		select {
		case delivered := <-lane.withdrawals:
			if delivered.RecordID != notice.RecordID {
				t.Fatalf("record=%q, want %q", delivered.RecordID, notice.RecordID)
			}
		case <-time.After(time.Second):
			t.Fatal("transcription withdrawal was not delivered after capacity became available")
		}
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("transcription withdrawal sender remained blocked")
		}
		close(lane.stop)
	})
}
