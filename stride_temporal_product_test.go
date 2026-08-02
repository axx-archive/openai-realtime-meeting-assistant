package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func setupSTRIDETemporalProductTest(t *testing.T) (*kanbanBoardApp, *userAccount, RoomScoutScope) {
	t.Helper()
	t.Setenv("STRIDE_RUNTIME_ENABLED", "true")
	t.Setenv("STRIDE_RUNTIME_BOOTSTRAP_EMPTY", "true")
	t.Setenv("STRIDE_RUNTIME_MIN_GENERATION", "1")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_KEY_ID", "temporal_product_test_key")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_MAC_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED", "true")
	app := newW2ATestApp(t)
	t.Cleanup(func() { _ = app.Close() })
	email := "aj@shareability.com"
	user := accountStore().findUser(email)
	if user == nil {
		t.Fatal("test account is unavailable")
	}
	sittingID := admitMemberWithTranscriptConsentForTest(t, app, officeRoomID, email)
	generation := app.ensureRoomMedia(officeRoomID)
	if generation == 0 {
		t.Fatal("office media did not start")
	}
	meeting, ok := app.meetings.activeRecord(officeRoomID)
	if !ok || meeting.ID != sittingID {
		t.Fatalf("active meeting=%+v ok=%t", meeting, ok)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, meeting.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !startedAt.Add(3 * time.Millisecond).Before(time.Now().UTC()) {
		time.Sleep(4 * time.Millisecond)
	}
	audience := STRIDEAudience{Visibility: "meeting", Principals: []string{strideRuntimePrincipalForEmail(email)}}
	event := strideTemporalProductTranscriptEvent(canonicalTenantID(), officeRoomID, sittingID, generation, startedAt.Add(time.Millisecond), startedAt.Add(2*time.Millisecond), audience, "Erick shared the launch brief and AJ committed to review it today.")
	config := TemporalMeetingBrainConfig{TenantID: canonicalTenantID(), RoomID: officeRoomID, SittingID: sittingID, SittingStart: startedAt.UTC()}
	if err := app.strideRuntime.ApplyTemporalEvidence(canonicalTenantID(), config, event); err != nil {
		t.Fatalf("apply temporal event: %v", err)
	}
	return app, user, RoomScoutScope{RoomID: officeRoomID, SittingID: sittingID, MediaGeneration: generation}
}

func strideTemporalProductTranscriptEvent(tenantID, roomID, sittingID string, generation uint64, start, end time.Time, audience STRIDEAudience, text string) TemporalMeetingEvent {
	textDigest := temporalDigest(text)
	createdAt := end.Add(time.Millisecond).UTC()
	conversationHeader := STRIDEContractHeader{TenantID: tenantID, ID: "temporal_product_conversation", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractConversationEvent, ContentDigest: textDigest, CreatedAt: createdAt}
	conversation := ConversationEvent{
		Header: conversationHeader, SourceType: "meeting_transcript", SourceID: "temporal_product_source", RoomID: roomID, SittingID: sittingID,
		AuthorPrincipal: "speaker:temporal_product", AuthorName: "Erick", OccurredAt: start.UTC(), IngestedAt: createdAt,
		EventType: "transcript_turn", ContentRevision: 1, ContentDigest: textDigest, Audience: audience, ACLVersion: 1,
		RetentionPolicy: "company_default", PurgeGeneration: 0, BodyRef: "temporal_product_body", Provenance: "provider",
	}
	segmentHeader := STRIDEContractHeader{TenantID: tenantID, ID: "temporal_product_segment", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractTranscriptSegment, ContentDigest: textDigest, CreatedAt: createdAt}
	segment := TranscriptSegment{
		Header: segmentHeader, ConversationRef: referenceFromHeader(conversationHeader), RoomID: roomID, SittingID: sittingID,
		MediaGeneration: generation, CaptureSequence: 1, SourceStart: start.UTC(), SourceEnd: end.UTC(), Status: "authoritative_final",
		Speaker: "speaker:temporal_product", Attribution: "server_attribution", ConsentScopes: []string{"org_memory", "transcription"},
		ModelDigest: temporalDigest("model"), ConfigDigest: temporalDigest("config"), ContextDigest: temporalDigest("context"), CreatedAt: createdAt,
	}
	revisionHeader := STRIDEContractHeader{TenantID: tenantID, ID: "temporal_product_revision", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractTranscriptRevision, ContentDigest: textDigest, CreatedAt: createdAt}
	revision := TranscriptRevision{Header: revisionHeader, SegmentID: segmentHeader.ID, Revision: 1, TextDigest: textDigest, Status: "authoritative_final", Evidence: []STRIDEReference{referenceFromHeader(segmentHeader)}}
	return TemporalMeetingEvent{Sequence: 1, Kind: TemporalMeetingEventTranscript, Transcript: &TemporalTranscriptRevisionEvent{Conversation: conversation, Segment: segment, Revision: revision, Text: text}}
}

func TestSTRIDETemporalProductAnswersExactWindowThroughMemberAndRoomPaths(t *testing.T) {
	app, user, scope := setupSTRIDETemporalProductTest(t)
	member, err := app.answerSTRIDETemporalForMember(context.Background(), user, officeRoomID, TemporalQueryLastFiveMinutes)
	if err != nil {
		t.Fatal(err)
	}
	if member.Window != TemporalQueryLastFiveMinutes || member.TranscriptHighWater != 1 || len(member.Evidence) != 1 || member.EvidenceDigest == "" || !bytes.Contains([]byte(member.Text), []byte("Erick shared the launch brief")) || member.AnalysisFresh {
		t.Fatalf("member answer=%+v", member)
	}
	room, err := app.answerSTRIDETemporalForRoom(context.Background(), scope, TemporalQueryLastThirtyMinutes)
	if err != nil {
		t.Fatal(err)
	}
	if room.Window != TemporalQueryLastThirtyMinutes || room.EvidenceDigest != member.EvidenceDigest {
		t.Fatalf("room answer=%+v member=%+v", room, member)
	}
	if _, err := app.answerSTRIDETemporalForMember(context.Background(), user, "another-room", TemporalQueryLastFiveMinutes); !errors.Is(err, ErrMeetingSpecialistProductScope) {
		t.Fatalf("cross-room answer err=%v", err)
	}
}

func TestSTRIDETemporalRoomPublicationFailsClosedWhenGuestIsPresent(t *testing.T) {
	app, _, scope := setupSTRIDETemporalProductTest(t)
	guestKey := temporalDigest("guest-session")
	app.mu.Lock()
	live := app.roomLiveLocked(officeRoomID)
	live.participantCounts["Guest Sam"] = 1
	live.participants["Guest Sam"] = time.Now().UTC()
	live.guestSeats[guestKey] = "Guest Sam"
	app.mu.Unlock()
	if _, err := app.answerSTRIDETemporalForRoom(context.Background(), scope, TemporalQueryLastFiveMinutes); !errors.Is(err, ErrSTRIDETemporalProductAudience) {
		t.Fatalf("guest room answer err=%v", err)
	}
}

func TestSTRIDETemporalAnswerHTTPIsAuthenticatedAndServerScoped(t *testing.T) {
	setupAuthTestEnv(t)
	app, _, _ := setupSTRIDETemporalProductTest(t)
	previous := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previous })
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)

	unauthorized := httptest.NewRequest(http.MethodPost, strideRuntimeAPIBase+"temporal/answer", bytes.NewBufferString(`{"window":"last_5_minutes"}`))
	unauthorizedRecorder := httptest.NewRecorder()
	mux.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorizedRecorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	request := httptest.NewRequest(http.MethodPost, strideRuntimeAPIBase+"temporal/answer?tenantId=other_tenant", bytes.NewBufferString(`{"roomId":"office","window":"last_5_minutes"}`))
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("answer status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		OK     bool `json:"ok"`
		Answer struct {
			RoomID              string `json:"roomId"`
			TranscriptHighWater uint64 `json:"transcriptHighWater"`
			Evidence            []any  `json:"evidence"`
		} `json:"answer"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Answer.RoomID != officeRoomID || body.Answer.TranscriptHighWater != 1 || len(body.Answer.Evidence) != 1 {
		t.Fatalf("answer body=%+v", body)
	}
}

func TestSTRIDETemporalToolContractIsBounded(t *testing.T) {
	tool := strideTemporalRecallToolDefinition()
	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"meeting_interval_recall", "last_5_minutes", "last_30_minutes"} {
		if !bytes.Contains(encoded, []byte(strconv.Quote(want))) {
			t.Fatalf("tool schema missing %q: %s", want, encoded)
		}
	}
	if _, err := parseSTRIDETemporalWindow("last hour"); err == nil {
		t.Fatal("unbounded temporal window was accepted")
	}
}
