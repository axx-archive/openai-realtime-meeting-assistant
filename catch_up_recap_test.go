package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newW2ATestApp(t *testing.T) *kanbanBoardApp {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	t.Setenv("MEETINGS_PATH", filepath.Join(dir, "meetings.json"))
	t.Setenv("ADMISSION_ANCHORS_PATH", filepath.Join(dir, "admission-anchors.json"))
	t.Setenv("NOTIFICATIONS_PATH", filepath.Join(dir, "notifications.json"))
	return newKanbanBoardApp()
}

type testCatchUpResolver struct {
	resolve func(context.Context, BrainRetrievalRequest) (BrainRetrievalResult, error)
	reauth  func(context.Context, ACLPrincipal, RetrievalSnapshot) error
	commit  func(context.Context, ACLPrincipal, RetrievalSnapshot, func() error) error
}

func (resolver testCatchUpResolver) ResolveCatchUp(ctx context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
	return resolver.resolve(ctx, request)
}

func (resolver testCatchUpResolver) CommitCatchUpPublication(ctx context.Context, principal ACLPrincipal, snapshot RetrievalSnapshot, publish func() error) error {
	if resolver.commit != nil {
		return resolver.commit(ctx, principal, snapshot, publish)
	}
	if resolver.reauth == nil {
		return publish()
	}
	if err := resolver.reauth(ctx, principal, snapshot); err != nil {
		return err
	}
	return publish()
}

func validCatchUpRetrieval(t *testing.T, request BrainRetrievalRequest, body string, status RecallSourceStatus) BrainRetrievalResult {
	t.Helper()
	if status == "" {
		status = RecallSourceFresh
	}
	ref := BrainEvidenceRef{
		TenantID: request.Principal.TenantID, SourceFamily: "meeting_transcript", ObjectID: "transcript-1",
		ContentRevision: 1, ACLVersion: 1, ContentDigest: digestBrainString(body), RoomID: request.Temporal.RoomID,
		SittingID: request.Temporal.SittingID, OccurredStart: request.Temporal.StartUTC.Add(time.Second),
		OccurredEnd: request.Temporal.EndUTC.Add(-time.Second), PurgeGeneration: 0, Trust: BrainEvidenceTrusted,
	}
	evidenceID, err := brainRetrievalEvidenceID(ref)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := request.Temporal.SettleUntil.Add(time.Second)
	snapshot := RetrievalSnapshot{
		TenantID: request.Principal.TenantID, PrincipalKind: request.Principal.Kind, PrincipalID: request.Principal.ID,
		Query: request.Query, QueryDigest: digestBrainString(request.Query), Temporal: request.Temporal,
		SourceHighWater: 9, ProjectionHighWater: 9, PurgeGeneration: 0,
		Sources: []RetrievalSnapshotSource{{EvidenceID: evidenceID, Evidence: ref}}, CreatedAt: createdAt,
	}
	snapshot.SnapshotID, err = snapshot.CanonicalID()
	if err != nil {
		t.Fatal(err)
	}
	coverage := buildBrainRecallCoverage(snapshot, request.Temporal.StartUTC, request.Temporal.EndUTC,
		[]RecallSourceCoverage{{SourceFamily: ref.SourceFamily, ObjectID: ref.ObjectID, ContentDigest: ref.ContentDigest, Status: status}},
		RecallLaneCoverage{Lexical: RecallLaneActive, Semantic: RecallLaneNotRequired, Digest: RecallLaneNotRequired, Raw: RecallLaneActive},
		request.Temporal.CaptureSequenceCutoff, 0, true)
	return BrainRetrievalResult{
		Snapshot: snapshot, Coverage: coverage,
		Sources: []BrainRetrievedSource{{EvidenceID: evidenceID, Evidence: ref, Status: status, Body: body}},
	}
}

func setupCatchUpApp(t *testing.T, roomID, email string, admittedAt time.Time, cutoff uint64) (*kanbanBoardApp, string) {
	t.Helper()
	app := newW2ATestApp(t)
	sittingID := app.memory.ensureMeetingID(roomID)
	if record, changed := app.meetings.startMeeting(roomID, sittingID, admittedAt.Add(-10*time.Minute), []string{participantNameForEmail(email)}); !changed || record.ID != sittingID {
		t.Fatalf("start meeting record=%+v changed=%t", record, changed)
	}
	anchor, err := app.admissionAnchors.RecordFirst(context.Background(), AdmissionAnchor{
		TenantID: canonicalTenantID(), RoomID: roomID, SittingID: sittingID, Principal: memberAdmissionPrincipal(email),
		AdmittedAt: admittedAt, CaptureSequenceCutoff: cutoff, CaptureWatermark: admittedAt.Add(-time.Second),
	})
	if err != nil || anchor.CaptureSequenceCutoff != cutoff {
		t.Fatalf("record anchor=%+v err=%v", anchor, err)
	}
	return app, sittingID
}

func TestExactCatchUpUsesFirstAdmissionBoundaryAndReturnsEvidenceCoverage(t *testing.T) {
	email := "aj@shareability.com"
	roomID := "room-catch1111"
	admittedAt := time.Now().UTC().Add(-time.Minute)
	app, sittingID := setupCatchUpApp(t, roomID, email, admittedAt, 41)
	defer app.Close()

	var mu sync.Mutex
	requests := []BrainRetrievalRequest{}
	app.catchUpRecapResolver = catchUpRecapResolverFunc(func(_ context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		return validCatchUpRetrieval(t, request, "Decision: ship the room isolation gate before routing work.", RecallSourceFresh), nil
	})
	response, err := app.exactCatchUpRecap(context.Background(), email, roomID, "decisions")
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RoomID != roomID || response.SittingID != sittingID || response.Coverage.Status != RecallCoverageComplete || len(response.Evidence) != 1 {
		t.Fatalf("response=%+v", response)
	}
	if !strings.Contains(response.Recap, "Decision: ship") || !strings.Contains(response.Recap, "[evidence:") || response.Evidence[0].Evidence.RoomID != roomID {
		t.Fatalf("recap/evidence=%q / %+v", response.Recap, response.Evidence)
	}
	if !response.Temporal.EndUTC.Equal(admittedAt) || response.Temporal.CaptureSequenceCutoff != 41 || response.Temporal.Interpretation != TemporalBeforeAdmission {
		t.Fatalf("temporal=%+v", response.Temporal)
	}

	// A reconnect or second device proposes a later anchor, but MIN(first
	// admission) semantics must keep the exact same recap boundary.
	if _, err := app.admissionAnchors.RecordFirst(context.Background(), AdmissionAnchor{
		TenantID: canonicalTenantID(), RoomID: roomID, SittingID: sittingID, Principal: memberAdmissionPrincipal(email),
		AdmittedAt: admittedAt.Add(5 * time.Minute), CaptureSequenceCutoff: 99, CaptureWatermark: admittedAt.Add(4 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	second, err := app.exactCatchUpRecap(context.Background(), email, roomID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Temporal.EndUTC.Equal(admittedAt) || second.Temporal.CaptureSequenceCutoff != 41 {
		t.Fatalf("reconnect moved first admission: %+v", second.Temporal)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0].Principal.RoomID != roomID || requests[0].Principal.SittingID != sittingID || requests[0].Principal.Kind != ACLPrincipalUser {
		t.Fatalf("requests=%+v", requests)
	}
}

func TestExactCatchUpFailsClosedForGuestStaleRoomAndInvalidProof(t *testing.T) {
	email := "aj@shareability.com"
	roomID := "room-catch2222"
	app, _ := setupCatchUpApp(t, roomID, email, time.Now().UTC().Add(-time.Minute), 5)
	defer app.Close()
	app.catchUpRecapResolver = catchUpRecapResolverFunc(func(_ context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
		result := validCatchUpRetrieval(t, request, "ROOM-A-CANARY", RecallSourceFresh)
		result.Snapshot.Temporal.RoomID = "room-other3333"
		return result, nil
	})
	if _, err := app.exactCatchUpRecap(context.Background(), "", roomID, ""); !errors.Is(err, ErrCatchUpUnauthorized) {
		t.Fatalf("guest/anonymous error=%v", err)
	}
	if _, err := app.exactCatchUpRecap(context.Background(), email, "room-other3333", ""); !errors.Is(err, ErrCatchUpStale) {
		t.Fatalf("stale room error=%v", err)
	}
	if response, err := app.exactCatchUpRecap(context.Background(), email, roomID, ""); !errors.Is(err, ErrCatchUpUnavailable) || response.Recap != "" || strings.Contains(response.Recap, "ROOM-A-CANARY") {
		t.Fatalf("invalid cross-room proof leaked response=%+v err=%v", response, err)
	}
}

func TestCatchMeUpProviderFailureDoesNotMutateRoomRuntime(t *testing.T) {
	email := "aj@shareability.com"
	roomID := "room-catch4444"
	app, _ := setupCatchUpApp(t, roomID, email, time.Now().UTC().Add(-time.Minute), 8)
	defer app.Close()
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.participants[participantNameForEmail(email)] = time.Now().UTC()
	state.participantCounts[participantNameForEmail(email)] = 1
	before := app.roomSnapshotLockedForRoom(state, configuredMeetingRoomCapacity())
	app.mu.Unlock()
	app.catchUpRecapResolver = catchUpRecapResolverFunc(func(context.Context, BrainRetrievalRequest) (BrainRetrievalResult, error) {
		return BrainRetrievalResult{}, errors.New("model and retrieval provider unavailable")
	})
	if _, _, err := app.catchMeUp(map[string]any{}, email, roomID); !errors.Is(err, ErrCatchUpUnavailable) {
		t.Fatalf("catch-up error=%v", err)
	}
	after := app.roomSnapshotForRoom(roomID)
	if strings.TrimSpace(asString(before["roomId"])) != strings.TrimSpace(asString(after["roomId"])) || len(app.participantSnapshotForRoom(roomID)) != 1 {
		t.Fatalf("AI failure mutated room before=%+v after=%+v", before, after)
	}
}

func TestExactCatchUpRevocationBeforePublicationReturnsNoContent(t *testing.T) {
	email := "aj@shareability.com"
	roomID := "room-catch5555"
	app, _ := setupCatchUpApp(t, roomID, email, time.Now().UTC().Add(-time.Minute), 13)
	defer app.Close()
	app.catchUpRecapResolver = testCatchUpResolver{
		resolve: func(_ context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
			return validCatchUpRetrieval(t, request, "REVOKED-CANARY", RecallSourceFresh), nil
		},
		reauth: func(context.Context, ACLPrincipal, RetrievalSnapshot) error {
			return errors.New("grant revoked")
		},
	}
	response, err := app.exactCatchUpRecap(context.Background(), email, roomID, "")
	if !errors.Is(err, ErrCatchUpUnavailable) || response.Recap != "" || strings.Contains(response.Recap, "REVOKED-CANARY") {
		t.Fatalf("revoked content escaped response=%+v err=%v", response, err)
	}
}

func TestCatchMeUpToolDeliversPrivateEvidenceLinkedNotification(t *testing.T) {
	email := "aj@shareability.com"
	roomID := "room-catch6666"
	app, _ := setupCatchUpApp(t, roomID, email, time.Now().UTC().Add(-time.Minute), 21)
	defer app.Close()
	app.catchUpRecapResolver = catchUpRecapResolverFunc(func(_ context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
		return validCatchUpRetrieval(t, request, "Private pre-join decision.", RecallSourceFresh), nil
	})
	result, changed, err := app.catchMeUp(map[string]any{"focus": "decision"}, email, roomID)
	if err != nil || changed || result["audience"] != notificationAudienceMe || result["roomId"] != roomID {
		t.Fatalf("tool result=%+v changed=%t err=%v", result, changed, err)
	}
	notifications := app.notificationsForUser(email, 10)
	if len(notifications) != 1 || !strings.Contains(asString(notifications[0]["text"]), "Private pre-join decision.") ||
		!strings.Contains(asString(notifications[0]["text"]), "[evidence:") {
		t.Fatalf("private notification=%+v", notifications)
	}
}
