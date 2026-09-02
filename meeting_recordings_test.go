package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func postRecordingChunk(t *testing.T, cookies []*http.Cookie, fields map[string]string, chunk []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if chunk != nil {
		part, err := writer.CreateFormFile("chunk", "part.webm")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/assistant/meetings/recording/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	meetingRecordingUploadHandler(recorder, req)
	return recorder
}

func decodeRecordingUpload(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode upload: %v body=%s", err, recorder.Body.String())
	}
	return payload
}

// D2: the per-room setting defaults to false and only the room manager flips it.
func TestMeetingRecordingSettingDefaultsOffAndIsManagerOnly(t *testing.T) {
	aj := setupRoomsTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	room, err := appRoomStore().create("Ops", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatal(err)
	}
	tim := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	recorder := doRoomsRequest(t, meetingRecordingSettingsHandler, http.MethodGet, "/assistant/meetings/recording/settings?roomId="+room.ID, "", tim)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"recordingEnabled":false`) || !strings.Contains(recorder.Body.String(), `"manageable":false`) {
		t.Fatalf("default setting status=%d body=%s, want recordingEnabled=false", recorder.Code, recorder.Body.String())
	}
	if roomListPayload(room)["recordingEnabled"] != false {
		t.Fatalf("rooms list payload=%v, want recordingEnabled=false by default", roomListPayload(room))
	}
	if recorder := doRoomsRequest(t, meetingRecordingSettingsHandler, http.MethodPatch, "/assistant/meetings/recording/settings", fmt.Sprintf(`{"roomId":%q,"recordingEnabled":true}`, room.ID), tim); recorder.Code != http.StatusNotFound {
		t.Fatalf("non-manager patch status=%d, want opaque 404", recorder.Code)
	}
	if roomMediaRecordingEnabled(room.ID) {
		t.Fatal("non-manager patch must not flip the setting")
	}
	recorder = doRoomsRequest(t, meetingRecordingSettingsHandler, http.MethodPatch, "/assistant/meetings/recording/settings", fmt.Sprintf(`{"roomId":%q,"recordingEnabled":true}`, room.ID), aj)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"recordingEnabled":true`) {
		t.Fatalf("manager patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !roomMediaRecordingEnabled(room.ID) || roomListPayload(room)["recordingEnabled"] != true {
		t.Fatal("manager patch must persist and surface in the rooms list payload")
	}
	if reopened := newMeetingRecordingStore(meetingRecordingsPath()); !reopened.setting(room.ID).RecordingEnabled {
		t.Fatal("setting must survive a store reopen")
	}
	if recorder := doRoomsRequest(t, meetingRecordingSettingsHandler, http.MethodGet, "/assistant/meetings/recording/settings?roomId=room-missing", "", aj); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown room status=%d, want 404", recorder.Code)
	}
	if recorder := doRoomsRequest(t, meetingRecordingSettingsHandler, http.MethodGet, "/assistant/meetings/recording/settings?roomId="+room.ID, "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want 401", recorder.Code)
	}
}

// D2: upload is refused for non-participants, while the room setting is off,
// and without recording consent; then chunks assemble into one webm blob that
// lands on the Meeting Record as the `recording` output stage and plays back
// through the session-gated blob route.
func TestMeetingRecordingUploadGatesConsentAssemblesChunksAndAttachesOutputStage(t *testing.T) {
	aj := setupRoomsTestEnv(t)
	t.Setenv("MEETING_RECORDING_MAX_BYTES", "1024")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	app.noteMeetingAdmission(officeRoomID, "AJ")
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("missing active meeting")
	}
	tim := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	fields := func(part int, final bool) map[string]string {
		return map[string]string{"meetingId": record.ID, "partIndex": fmt.Sprint(part), "final": fmt.Sprint(final), "mime": "video/webm", "durationSeconds": "42"}
	}

	if recorder := postRecordingChunk(t, nil, fields(0, false), []byte("AAAA")); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out upload status=%d, want 401", recorder.Code)
	}
	if recorder := postRecordingChunk(t, tim, fields(0, false), []byte("AAAA")); recorder.Code != http.StatusNotFound {
		t.Fatalf("non-participant upload status=%d, want opaque 404", recorder.Code)
	}
	recorder := postRecordingChunk(t, aj, fields(0, false), []byte("AAAA"))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "recording is off") {
		t.Fatalf("setting-off upload status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
	if _, err := appMeetingRecordingStore().setRecordingEnabled(officeRoomID, true, "aj@shareability.com", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recorder = postRecordingChunk(t, aj, fields(0, false), []byte("AAAA"))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "consent") {
		t.Fatalf("no-consent upload status=%d body=%s, want 403 consent", recorder.Code, recorder.Body.String())
	}
	if _, found := appMeetingRecordingStore().recording(record.ID); found {
		t.Fatal("a refused chunk must not open a recording")
	}

	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "aj@shareability.com")
	recorder = postRecordingChunk(t, aj, fields(0, false), []byte("AAAA"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("part 0 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if payload := decodeRecordingUpload(t, recorder); payload["state"] != meetingRecordingStateUploading || payload["partCount"] != float64(1) {
		t.Fatalf("part 0 payload=%v", payload)
	}
	// R5: the ordering 409 carries the record's current partCount so the
	// client resyncs its next partIndex instead of stalling.
	if recorder := postRecordingChunk(t, aj, fields(5, false), []byte("ZZZZ")); recorder.Code != http.StatusConflict || decodeRecordingUpload(t, recorder)["partCount"] != float64(1) {
		t.Fatalf("out-of-order part status=%d body=%s, want 409 with partCount 1", recorder.Code, recorder.Body.String())
	}
	if recorder := postRecordingChunk(t, aj, fields(0, false), []byte("AAAA")); recorder.Code != http.StatusOK || decodeRecordingUpload(t, recorder)["partCount"] != float64(1) {
		t.Fatalf("re-delivered part 0 status=%d body=%s, want idempotent ack", recorder.Code, recorder.Body.String())
	}
	if recorder := postRecordingChunk(t, aj, fields(1, false), []byte("BBBB")); recorder.Code != http.StatusOK {
		t.Fatalf("part 1 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = postRecordingChunk(t, aj, fields(2, true), []byte("CC"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("final part status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeRecordingUpload(t, recorder)
	output, _ := payload["recording"].(map[string]any)
	ref, _ := output["ref"].(string)
	if payload["state"] != meetingRecordingStateStored || !validBlobRef(ref) || output["mime"] != "video/webm" || output["size"] != float64(10) || output["durationSeconds"] != float64(42) {
		t.Fatalf("final payload=%v", payload)
	}
	data, meta, err := getBlob(ref)
	if err != nil || string(data) != "AAAABBBBCC" || meta.Mime != "video/webm" {
		t.Fatalf("blob=%q meta=%+v err=%v, want the assembled chunks", data, meta, err)
	}
	stored, ok := app.meetings.recordByID(record.ID)
	if !ok || stored.Recording == nil || stored.Recording.Ref != ref || !strings.Contains(stored.Recording.PlaybackPath, "/artifacts/blob?ref="+ref) {
		t.Fatalf("record recording=%+v, want the output stage", stored.Recording)
	}
	if projected := app.meetingRecordPayload(stored, time.Now().UTC()); projected["recording"] == nil {
		t.Fatalf("meeting payload=%v, want recording stage", projected)
	}
	if refs := meetingRecordingBlobRefs(app); len(refs) != 1 || refs[0] != ref {
		t.Fatalf("GC walker refs=%v, want the stored recording", refs)
	}
	if recorder := postRecordingChunk(t, aj, fields(3, false), []byte("DD")); recorder.Code != http.StatusConflict {
		t.Fatalf("post-store chunk status=%d, want 409", recorder.Code)
	}
	if entries, err := os.ReadDir(appMeetingRecordingStore().partsDir); err == nil && len(entries) != 0 {
		t.Fatalf("temp parts left behind: %d", len(entries))
	}

	// Playback rides the Meeting Record's own authorization: the viewer needs a
	// current source for the meeting (index projection), never just the ref.
	ctx := context.Background()
	viewer := accountStore().findUser("aj@shareability.com")
	if blobAuthorized(ctx, viewer, ref) {
		t.Fatal("a recording with no readable meeting source must not be served")
	}
	if _, _, err := app.memory.appendBrainWriteUp("recording-source", "Office meeting source.", map[string]string{"meetingId": record.ID}); err != nil {
		t.Fatal(err)
	}
	if !blobAuthorized(ctx, viewer, ref) || blobAuthorized(ctx, nil, ref) {
		t.Fatal("recording playback must follow Meeting Record visibility")
	}
	req := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+ref, nil)
	for _, cookie := range aj {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	artifactBlobHandler(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "video/webm" || !strings.HasPrefix(recorder.Header().Get("Content-Disposition"), "inline") || recorder.Body.String() != "AAAABBBBCC" {
		t.Fatalf("playback status=%d type=%q disposition=%q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Header().Get("Content-Disposition"))
	}
	detailReq := httptest.NewRequest(http.MethodGet, "/assistant/meetings/"+record.ID, nil)
	for _, cookie := range aj {
		detailReq.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	assistantMeetingsHandler(recorder, detailReq)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"recording":{"ref":"`+ref+`"`) {
		t.Fatalf("meeting record detail status=%d body=%s, want recording stage", recorder.Code, recorder.Body.String())
	}
}

// D2: the size cap (env MEETING_RECORDING_MAX_BYTES) answers 413 and refuses
// the recording; a consent denial on the sitting refuses further chunks.
func TestMeetingRecordingUploadRefusesOverCapAndAfterConsentWithdrawal(t *testing.T) {
	aj := setupRoomsTestEnv(t)
	t.Setenv("MEETING_RECORDING_MAX_BYTES", "8")
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	app.noteMeetingAdmission(officeRoomID, "AJ")
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("missing active meeting")
	}
	if _, err := appMeetingRecordingStore().setRecordingEnabled(officeRoomID, true, "aj@shareability.com", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "aj@shareability.com")
	fields := func(part int) map[string]string {
		return map[string]string{"meetingId": record.ID, "partIndex": fmt.Sprint(part), "final": "0"}
	}
	if recorder := postRecordingChunk(t, aj, fields(0), []byte("123456")); recorder.Code != http.StatusOK {
		t.Fatalf("part 0 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder := postRecordingChunk(t, aj, fields(1), []byte("789012"))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-cap status=%d body=%s, want 413", recorder.Code, recorder.Body.String())
	}
	if refused, _ := appMeetingRecordingStore().recording(record.ID); refused.State != meetingRecordingStateRefused {
		t.Fatalf("recording=%+v, want refused", refused)
	}
	if stored, _ := app.meetings.recordByID(record.ID); stored.Recording != nil {
		t.Fatal("a refused recording must never reach the Meeting Record")
	}

	// A second sitting: consent withdrawal on the sitting ends the recording.
	app.meetings.mu.Lock()
	generation := app.meetings.idleGenerations[officeRoomID]
	app.meetings.mu.Unlock()
	app.endMeetingForIdle(officeRoomID, generation)
	t.Setenv("MEETING_RECORDING_MAX_BYTES", "1024")
	app.noteMeetingAdmission(officeRoomID, "AJ")
	second, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || second.ID == record.ID {
		t.Fatalf("second sitting=%+v", second)
	}
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "aj@shareability.com")
	secondFields := func(part int) map[string]string {
		return map[string]string{"meetingId": second.ID, "partIndex": fmt.Sprint(part), "final": "0"}
	}
	if recorder := postRecordingChunk(t, aj, secondFields(0), []byte("AAAA")); recorder.Code != http.StatusOK {
		t.Fatalf("second sitting part 0 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	handleConsentDecision(ConsentDecisionNotice{
		Binding: ConsentAdmissionBinding{RoomID: officeRoomID, SittingID: second.ID}, Scope: ConsentRecording, Disposition: ConsentDenied,
	})
	recorder = postRecordingChunk(t, aj, secondFields(1), []byte("BBBB"))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("post-denial chunk status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
	if revoked, _ := appMeetingRecordingStore().recording(second.ID); revoked.ConsentRevokedAt == "" || revoked.State != meetingRecordingStateRefused {
		t.Fatalf("recording=%+v, want consent-revoked refusal", revoked)
	}
}

// The recording lane is the third consent state: guests must grant it
// explicitly (no default-allow), and it never loosens the transcription lane.
func TestConsentRecordingLaneRequiresExplicitGuestGrant(t *testing.T) {
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy-v1")
	authority.CaptureCutoff = func() (uint64, error) { return 0, nil }
	binding := consentLaneTestBinding("guest-1", "room-1", "sitting-1")
	grantConsentScope(t, authority, binding, ConsentAudioCapture)
	if decision, err := authority.Authorize(context.Background(), binding, ConsentLaneTranscription); err != nil || !decision.Allowed {
		t.Fatalf("transcription default-allow changed: %+v err=%v", decision, err)
	}
	decision, err := authority.Authorize(context.Background(), binding, ConsentLaneRecording)
	if err != nil || decision.Allowed || len(decision.MissingScopes) != 1 || decision.MissingScopes[0] != ConsentRecording {
		t.Fatalf("recording without explicit grant=%+v err=%v, want denied on the recording scope", decision, err)
	}
	grantConsentScope(t, authority, binding, ConsentRecording)
	if decision, err := authority.Authorize(context.Background(), binding, ConsentLaneRecording); err != nil || !decision.Allowed {
		t.Fatalf("recording after grant=%+v err=%v, want allowed", decision, err)
	}
	if _, err := authority.RecordDecision(context.Background(), binding, ConsentRecording, ConsentDenied); err != nil {
		t.Fatal(err)
	}
	if decision, _ := authority.Authorize(context.Background(), binding, ConsentLaneRecording); decision.Allowed {
		t.Fatal("recording denial must fail closed")
	}
	member := binding
	member.PrincipalKind = ACLPrincipalUser
	member.PrincipalID = "aj@shareability.com"
	if decision, err := authority.Authorize(context.Background(), member, ConsentLaneRecording); err != nil || !decision.Allowed {
		t.Fatalf("member recording lane=%+v err=%v, want rules-of-the-road grant", decision, err)
	}
	if got := consentScopeDependencies(ConsentRecording); len(got) != 2 || got[0] != ConsentAudioCapture || got[1] != ConsentRecording {
		t.Fatalf("recording dependencies=%v", got)
	}
}

// Abandoned chunk streams never leak raw media: the liveness tick reaps
// `uploading` records past MEETING_RECORDING_UPLOAD_TTL and drops the part,
// fresh uploads are untouched, and turning the room setting off reaps its
// in-flight uploads immediately.
func TestMeetingRecordingSweepReapsAbandonedUploadsAndSettingOffReapsInFlight(t *testing.T) {
	setupRoomsTestEnv(t)
	t.Setenv("MEETING_RECORDINGS_PATH", filepath.Join(t.TempDir(), "meeting-recordings.json"))
	t.Setenv("MEETING_RECORDING_UPLOAD_TTL", "30m")
	store := newMeetingRecordingStore(meetingRecordingsPath())
	meetingRecordingStoreMu.Lock()
	meetingRecordingStoreCache[meetingRecordingsPath()] = store
	meetingRecordingStoreMu.Unlock()
	now := time.Now().UTC()
	stale := now.Add(-3 * time.Hour)
	if _, err := store.appendPart("meeting-stale", officeRoomID, "aj@shareability.com", "video/webm", 0, []byte("OLD"), 1024, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := store.appendPart("meeting-fresh", officeRoomID, "aj@shareability.com", "video/webm", 0, []byte("NEW"), 1024, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.appendPart("meeting-ops", "room-ops", "aj@shareability.com", "video/webm", 0, []byte("OPS"), 1024, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	partExists := func(id string) bool {
		_, err := os.Stat(store.partPath(id))
		return err == nil
	}
	if !partExists("meeting-stale") || !partExists("meeting-fresh") || !partExists("meeting-ops") {
		t.Fatal("expected three in-flight part files")
	}

	sweepAbandonedMeetingRecordingUploads()
	if reaped, _ := store.recording("meeting-stale"); reaped.State != meetingRecordingStateRefused || reaped.Error != errMeetingRecordingAbandoned.Error() || partExists("meeting-stale") {
		t.Fatalf("stale upload=%+v partExists=%v, want refused with parts removed", reaped, partExists("meeting-stale"))
	}
	if fresh, _ := store.recording("meeting-fresh"); fresh.State != meetingRecordingStateUploading || !partExists("meeting-fresh") {
		t.Fatalf("fresh upload=%+v, want untouched", fresh)
	}
	if _, err := store.appendPart("meeting-stale", officeRoomID, "aj@shareability.com", "video/webm", 1, []byte("LATE"), 1024, now); !errors.Is(err, errMeetingRecordingRefused) {
		t.Fatalf("late chunk after reap err=%v, want refused", err)
	}
	if reopened := newMeetingRecordingStore(meetingRecordingsPath()); func() bool {
		r, _ := reopened.recording("meeting-stale")
		return r.State != meetingRecordingStateRefused
	}() {
		t.Fatal("reap must be durable across a store reopen")
	}

	// Turning recording off for a room reaps only that room's in-flight uploads.
	if _, err := store.setRecordingEnabled("room-ops", false, "aj@shareability.com", now); err != nil {
		t.Fatal(err)
	}
	if ops, _ := store.recording("meeting-ops"); ops.State != meetingRecordingStateRefused || ops.Error != errMeetingRecordingDisabled.Error() || partExists("meeting-ops") {
		t.Fatalf("setting-off upload=%+v partExists=%v, want refused with parts removed", ops, partExists("meeting-ops"))
	}
	if fresh, _ := store.recording("meeting-fresh"); fresh.State != meetingRecordingStateUploading || !partExists("meeting-fresh") {
		t.Fatalf("other room's upload=%+v, want untouched by a different room's setting", fresh)
	}
	if _, err := store.setRecordingEnabled(officeRoomID, true, "aj@shareability.com", now); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := store.recording("meeting-fresh"); fresh.State != meetingRecordingStateUploading {
		t.Fatal("turning recording on must not touch in-flight uploads")
	}
	if got := meetingRecordingUploadTTL(); got != 30*time.Minute {
		t.Fatalf("upload ttl=%s, want env override", got)
	}
	t.Setenv("MEETING_RECORDING_UPLOAD_TTL", "")
	if got := meetingRecordingUploadTTL(); got != meetingRecordingDefaultUploadTTL {
		t.Fatalf("default upload ttl=%s, want 2h", got)
	}
}

/* ---------- code-review fixes: consent fail-closed, persist-before-refuse, ordering 409 ---------- */

// R6: a participant the resolver cannot map is skipped only when they have
// LEFT (no longer captured). While still seated — here a member whose
// account no longer exists — the chunk is refused with the consent copy.
func TestMeetingRecordingConsentFailsClosedForSeatedUnresolvableParticipant(t *testing.T) {
	aj := setupRoomsTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	app.noteMeetingAdmission(officeRoomID, "AJ")
	app.noteMeetingAdmission(officeRoomID, "Tom")
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("missing active meeting")
	}
	if !meetingHasParticipant(record, "Tom") || !meetingHasParticipant(record, "AJ") {
		t.Fatalf("participants=%v, want AJ and Tom", record.Participants)
	}
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "aj@shareability.com")
	ctx := context.Background()
	ajUser := accountStore().findUser("aj@shareability.com")

	// Tom's account disappears from the (test-isolated) user store: the
	// resolver can no longer map him to a consent principal.
	users := accountStore()
	users.mu.Lock()
	delete(users.users, "tom@shareability.com")
	users.mu.Unlock()
	if _, resolvable := app.consentPrincipalForTranscriptSpeaker(officeRoomID, "Tom"); resolvable {
		t.Fatal("Tom must be unresolvable once his account is gone")
	}
	// Tom holds no live seat (departed): not captured, not re-evaluated.
	if allowed, reason := app.meetingRecordingConsentDecision(ctx, record, ajUser); !allowed {
		t.Fatalf("departed unresolvable participant blocked recording: %s", reason)
	}
	// Tom is seated: being captured with unverifiable consent — fail closed.
	if _, _, err := app.admitParticipantSessionEndpointInRoom(officeRoomID, "Tom", "tom-session", "tom-endpoint"); err != nil {
		t.Fatal(err)
	}
	if allowed, reason := app.meetingRecordingConsentDecision(ctx, record, ajUser); allowed || reason != "a participant has not consented to recording" {
		t.Fatalf("seated unresolvable participant allowed=%t reason=%q, want refusal with the consent copy", allowed, reason)
	}
	// The upload path answers with the same copy.
	if _, err := appMeetingRecordingStore().setRecordingEnabled(officeRoomID, true, "aj@shareability.com", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"meetingId": record.ID, "partIndex": "0", "final": "false", "mime": "video/webm"}
	if recorder := postRecordingChunk(t, aj, fields, []byte("AAAA")); recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "recording consent is missing: a participant has not consented to recording") {
		t.Fatalf("chunk with a seated unresolvable participant status=%d body=%s, want 403 consent copy", recorder.Code, recorder.Body.String())
	}
	if _, found := appMeetingRecordingStore().recording(record.ID); found {
		t.Fatal("a refused chunk must not open a recording")
	}
	// Once Tom leaves, AJ's consent carries the meeting again.
	if removed, still := app.forgetParticipantSessionResultInRoom(officeRoomID, "Tom", "tom-session"); !removed || still {
		t.Fatalf("forget Tom removed=%t still=%t", removed, still)
	}
	if allowed, reason := app.meetingRecordingConsentDecision(ctx, record, ajUser); !allowed {
		t.Fatalf("recording blocked after the unresolvable participant left: %s", reason)
	}
	if recorder := postRecordingChunk(t, aj, fields, []byte("AAAA")); recorder.Code != http.StatusOK {
		t.Fatalf("chunk after the participant left status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
}

// R9: turning recording off persists BEFORE any in-flight upload is refused
// or its parts are dropped. A persist failure leaves the setting and every
// upload exactly as they were; once persistence works the refusal lands.
func TestMeetingRecordingSettingOffPersistsBeforeRefusingUploads(t *testing.T) {
	setupRoomsTestEnv(t)
	t.Setenv("MEETING_RECORDINGS_PATH", filepath.Join(t.TempDir(), "meeting-recordings.json"))
	store := newMeetingRecordingStore(meetingRecordingsPath())
	now := time.Now().UTC()
	if _, err := store.appendPart("meeting-live", "room-ops", "aj@shareability.com", "video/webm", 0, []byte("LIVE"), 1024, now); err != nil {
		t.Fatal(err)
	}
	partExists := func() bool {
		_, err := os.Stat(store.partPath("meeting-live"))
		return err == nil
	}
	if !partExists() {
		t.Fatal("expected the in-flight part file")
	}
	// Break persistence: a regular file where the store's directory must be.
	goodPath := store.path
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blocked, "meeting-recordings.json")
	if _, err := store.setRecordingEnabled("room-ops", false, "aj@shareability.com", now); err == nil {
		t.Fatal("setRecordingEnabled succeeded with persistence broken")
	}
	if live, _ := store.recording("meeting-live"); live.State != meetingRecordingStateUploading || live.Error != "" || !partExists() {
		t.Fatalf("failed persist refused the upload anyway: %+v partExists=%t", live, partExists())
	}
	store.mu.Lock()
	_, settingWritten := store.state.RoomSettings["room-ops"]
	store.mu.Unlock()
	if settingWritten {
		t.Fatal("failed persist left the new setting in memory")
	}
	// Persistence restored: the upload is still live and accepts chunks; then
	// the flip lands, followed by the refusal and the reap.
	store.path = goodPath
	if _, err := store.appendPart("meeting-live", "room-ops", "aj@shareability.com", "video/webm", 1, []byte("MORE"), 1024, now); err != nil {
		t.Fatalf("upload after the failed flip must still accept chunks: %v", err)
	}
	if _, err := store.setRecordingEnabled("room-ops", false, "aj@shareability.com", now); err != nil {
		t.Fatal(err)
	}
	if live, _ := store.recording("meeting-live"); live.State != meetingRecordingStateRefused || live.Error != errMeetingRecordingDisabled.Error() || partExists() {
		t.Fatalf("setting-off upload=%+v partExists=%t, want refused with parts removed", live, partExists())
	}
	reopened := newMeetingRecordingStore(goodPath)
	if reopenedRecord, _ := reopened.recording("meeting-live"); reopenedRecord.State != meetingRecordingStateRefused || reopened.setting("room-ops").RecordingEnabled {
		t.Fatalf("durable state=%+v setting=%+v, want refused + off", reopenedRecord, reopened.setting("room-ops"))
	}
	// R5 (store half): an out-of-order chunk on a fresh id reports partCount 0.
	recorder := httptest.NewRecorder()
	writeMeetingRecordingUploadError(recorder, errMeetingRecordingPartOrder, 1024, -1)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"partCount":0`) {
		t.Fatalf("ordering 409 status=%d body=%s, want partCount 0", recorder.Code, recorder.Body.String())
	}
}

// Sandbox defect (member alone in a room, recording on, first chunk 403
// "could not be verified"): without Postgres the runtime consent authority
// has no durable store, so every lane check errors. The recorder's OWN seat
// consents by the act of recording — its principal is the uploader's session
// (the durable anchor proves the seat) and no lane authority is consulted.
func TestMeetingRecordingConsentAcceptsRecorderOwnSeat(t *testing.T) {
	aj := setupRoomsTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	// Exactly what a Postgres-less deployment builds: an authority with no store.
	installConsentAuthorityForTest(t, NewConsentLaneAuthority(nil, "ambient-policy-test-v1"))
	ctx := context.Background()
	sittingID := app.prepareMeetingSittingID(officeRoomID)
	if _, _, err := app.admitParticipantWithAnchor(ctx, officeRoomID, "AJ", "aj-session", "aj-endpoint", sittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || !meetingHasParticipant(record, "AJ") {
		t.Fatalf("active record=%+v ok=%t, want AJ seated", record, ok)
	}
	ajUser := accountStore().findUser("aj@shareability.com")
	if allowed, reason := app.meetingRecordingConsentDecision(ctx, record, ajUser); !allowed {
		t.Fatalf("recorder alone refused: %s", reason)
	}
	// The roster name alone never vouches: with no uploader session the lane
	// authority is asked for AJ and fails closed.
	if allowed, reason := app.meetingRecordingConsentDecision(ctx, record, nil); allowed || reason != "recording consent could not be verified" {
		t.Fatalf("no-recorder decision allowed=%t reason=%q, want unverifiable", allowed, reason)
	}
	// A recorder with no durable anchor for this sitting is refused.
	if allowed, reason := app.meetingRecordingConsentDecision(ctx, record, accountStore().findUser("tim@shareability.com")); allowed || reason != "recording consent could not be verified" {
		t.Fatalf("un-anchored recorder allowed=%t reason=%q, want unverifiable", allowed, reason)
	}
	// The first upload part lands and opens the recording.
	if _, err := appMeetingRecordingStore().setRecordingEnabled(officeRoomID, true, "aj@shareability.com", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"meetingId": record.ID, "partIndex": "0", "final": "false", "mime": "video/webm"}
	recorder := postRecordingChunk(t, aj, fields, []byte("AAAA"))
	if recorder.Code != http.StatusOK || decodeRecordingUpload(t, recorder)["partCount"] != float64(1) {
		t.Fatalf("recorder-alone first chunk status=%d body=%s, want 200 partCount 1", recorder.Code, recorder.Body.String())
	}
}

// The own-seat rule reaches no one else: another seated member whose lane the
// (store-less) authority cannot verify still refuses the chunk, fail closed.
func TestMeetingRecordingConsentStillRefusesUnverifiableOtherParticipant(t *testing.T) {
	aj := setupRoomsTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	installConsentAuthorityForTest(t, NewConsentLaneAuthority(nil, "ambient-policy-test-v1"))
	ctx := context.Background()
	sittingID := app.prepareMeetingSittingID(officeRoomID)
	for _, seat := range [][3]string{{"AJ", "aj-session", "aj@shareability.com"}, {"Tom", "tom-session", "tom@shareability.com"}} {
		if _, _, err := app.admitParticipantWithAnchor(ctx, officeRoomID, seat[0], seat[1], seat[1]+"-endpoint", sittingID, memberAdmissionPrincipal(seat[2])); err != nil {
			t.Fatal(err)
		}
	}
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || !meetingHasParticipant(record, "Tom") {
		t.Fatalf("active record=%+v ok=%t, want Tom seated", record, ok)
	}
	ajUser := accountStore().findUser("aj@shareability.com")
	if allowed, reason := app.meetingRecordingConsentDecision(ctx, record, ajUser); allowed || reason != "recording consent could not be verified" {
		t.Fatalf("unverifiable other participant allowed=%t reason=%q, want refusal", allowed, reason)
	}
	if _, err := appMeetingRecordingStore().setRecordingEnabled(officeRoomID, true, "aj@shareability.com", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"meetingId": record.ID, "partIndex": "0", "final": "false", "mime": "video/webm"}
	if recorder := postRecordingChunk(t, aj, fields, []byte("AAAA")); recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "recording consent could not be verified") {
		t.Fatalf("chunk with an unverifiable other participant status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
	if _, found := appMeetingRecordingStore().recording(record.ID); found {
		t.Fatal("a refused chunk must not open a recording")
	}
}
