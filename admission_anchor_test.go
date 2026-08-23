package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func admissionAnchorForTest(at time.Time, sittingID string, principal CanonicalPrincipalRef, cutoff uint64) AdmissionAnchor {
	return AdmissionAnchor{
		TenantID:              "tenant-a",
		RoomID:                "room-a",
		SittingID:             sittingID,
		Principal:             principal,
		AdmittedAt:            at,
		CaptureSequenceCutoff: cutoff,
		CaptureWatermark:      at.Add(-time.Second),
	}
}

func TestAdmissionAnchorAtomicMinimumKeepsWinningObservation(t *testing.T) {
	store, err := OpenAdmissionAnchorStore(filepath.Join(t.TempDir(), "anchors.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	principal := memberAdmissionPrincipal("AJ@Example.COM")

	first, err := store.RecordFirst(ctx, admissionAnchorForTest(base, "sitting-a", principal, 41))
	if err != nil {
		t.Fatal(err)
	}
	if first.AnchorID == "" || first.AnchorID != deterministicAdmissionAnchorID(first) {
		t.Fatalf("record returned invalid anchor id %q", first.AnchorID)
	}
	later := admissionAnchorForTest(base.Add(time.Minute), "sitting-a", principal, 99)
	got, err := store.RecordFirst(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AdmittedAt.Equal(first.AdmittedAt) || got.CaptureSequenceCutoff != 41 || !got.CaptureWatermark.Equal(first.CaptureWatermark) {
		t.Fatalf("reconnect moved first admission: got=%+v first=%+v", got, first)
	}

	earlier := admissionAnchorForTest(base.Add(-time.Minute), "sitting-a", principal, 17)
	got, err = store.RecordFirst(ctx, earlier)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AdmittedAt.Equal(earlier.AdmittedAt) || got.CaptureSequenceCutoff != 17 || !got.CaptureWatermark.Equal(earlier.CaptureWatermark) {
		t.Fatalf("atomic MIN did not retain the earlier observation: got=%+v want=%+v", got, earlier)
	}
}

func TestAdmissionAnchorIDDeterministicallyBindsNormalizedIdentity(t *testing.T) {
	base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	upper := normalizeAdmissionAnchor(admissionAnchorForTest(base, "sitting-a", memberAdmissionPrincipal("AJ@Example.COM"), 1))
	lower := normalizeAdmissionAnchor(admissionAnchorForTest(base.Add(time.Hour), "sitting-a", memberAdmissionPrincipal("aj@example.com"), 99))
	if upper.AnchorID == "" || upper.AnchorID != lower.AnchorID {
		t.Fatalf("same normalized identity produced ids %q and %q", upper.AnchorID, lower.AnchorID)
	}
	newSitting := normalizeAdmissionAnchor(admissionAnchorForTest(base, "sitting-b", upper.Principal, 1))
	guest := normalizeAdmissionAnchor(admissionAnchorForTest(base, "sitting-a", guestAdmissionPrincipal(strings.Repeat("a", 64)), 1))
	if upper.AnchorID == newSitting.AnchorID || upper.AnchorID == guest.AnchorID || newSitting.AnchorID == guest.AnchorID {
		t.Fatalf("distinct identities collided: user=%q sitting=%q guest=%q", upper.AnchorID, newSitting.AnchorID, guest.AnchorID)
	}
}

func TestAdmissionAnchorRejectsTamperedAnchorIDOnWriteAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.json")
	store, err := OpenAdmissionAnchorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	candidate := admissionAnchorForTest(time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC), "sitting-a", memberAdmissionPrincipal("member@example.com"), 1)
	tampered := candidate
	tampered.AnchorID = "admission-anchor-tampered"
	if _, err := store.RecordFirst(context.Background(), tampered); !errors.Is(err, ErrAdmissionAnchorInvalid) {
		t.Fatalf("caller-supplied tampered id error=%v, want ErrAdmissionAnchorInvalid", err)
	}
	stored, err := store.RecordFirst(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	stored.AnchorID = "admission-anchor-tampered"
	checksum, err := admissionAnchorChecksum([]AdmissionAnchor{stored})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(admissionAnchorFile{Format: admissionAnchorFileFormat, Records: []AdmissionAnchor{stored}, Checksum: checksum})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicallyDurable(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAdmissionAnchorStore(path); !errors.Is(err, ErrAdmissionAnchorStore) || !errors.Is(err, ErrAdmissionAnchorInvalid) || !strings.Contains(err.Error(), "anchor id does not match identity") {
		t.Fatalf("restart accepted identity-tampered anchor: %v", err)
	}
}

func TestAdmissionAnchorConcurrentUpsertsChooseOneEarliestRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.json")
	const workers = 48
	stores := make([]*AdmissionAnchorStore, workers)
	for index := range stores {
		var err error
		stores[index], err = OpenAdmissionAnchorStore(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	principal := memberAdmissionPrincipal("member@example.com")
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for index := range stores {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := stores[index].RecordFirst(context.Background(), admissionAnchorForTest(base.Add(-time.Duration(index)*time.Millisecond), "sitting-a", principal, uint64(index)))
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	wantAt := base.Add(-time.Duration(workers-1) * time.Millisecond)
	got, found, err := stores[0].Lookup(context.Background(), "tenant-a", "room-a", "sitting-a", principal)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !got.AdmittedAt.Equal(wantAt) || got.CaptureSequenceCutoff != workers-1 {
		t.Fatalf("concurrent MIN=%+v found=%v, want admittedAt=%s cutoff=%d", got, found, wantAt, workers-1)
	}
	records, err := loadAdmissionAnchors(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("unique key produced %d rows, want 1", len(records))
	}
}

func TestAdmissionAnchorSurvivesRestartAndNewSittingGetsNewAnchor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.json")
	base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	principal := memberAdmissionPrincipal("member@example.com")
	firstStore, err := OpenAdmissionAnchorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first := admissionAnchorForTest(base, "sitting-a", principal, 7)
	if _, err := firstStore.RecordFirst(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenAdmissionAnchorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RecordFirst(context.Background(), admissionAnchorForTest(base.Add(time.Hour), "sitting-a", principal, 90)); err != nil {
		t.Fatal(err)
	}
	second := admissionAnchorForTest(base.Add(2*time.Hour), "sitting-b", principal, 101)
	if _, err := restarted.RecordFirst(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	for sittingID, want := range map[string]AdmissionAnchor{"sitting-a": first, "sitting-b": second} {
		got, found, err := restarted.Lookup(context.Background(), "tenant-a", "room-a", sittingID, principal)
		if err != nil || !found {
			t.Fatalf("lookup %s: found=%v err=%v", sittingID, found, err)
		}
		if !got.AdmittedAt.Equal(want.AdmittedAt) || got.CaptureSequenceCutoff != want.CaptureSequenceCutoff {
			t.Fatalf("lookup %s=%+v want=%+v", sittingID, got, want)
		}
	}
}

func TestAdmissionAnchorMemberAndGuestIdentityDoNotCollide(t *testing.T) {
	store, err := OpenAdmissionAnchorStore(filepath.Join(t.TempDir(), "anchors.json"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	member := memberAdmissionPrincipal("SHARED@example.com")
	guest := guestAdmissionPrincipal(strings.Repeat("a", 64))
	if _, err := store.RecordFirst(context.Background(), admissionAnchorForTest(at, "sitting-a", member, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordFirst(context.Background(), admissionAnchorForTest(at.Add(time.Second), "sitting-a", guest, 2)); err != nil {
		t.Fatal(err)
	}
	records, err := loadAdmissionAnchors(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Principal.Kind == records[1].Principal.Kind {
		t.Fatalf("member and guest principals collided: %+v", records)
	}
	if member.ID != "shared@example.com" || member.Kind != "user" || guest.Kind != "guest" {
		t.Fatalf("principal normalization member=%+v guest=%+v", member, guest)
	}
	plaintext := admissionAnchorForTest(at, "sitting-a", guestAdmissionPrincipal("raw-guest-session-token"), 3)
	if _, err := store.RecordFirst(context.Background(), plaintext); !errors.Is(err, ErrAdmissionAnchorInvalid) {
		t.Fatalf("plaintext guest principal error=%v, want ErrAdmissionAnchorInvalid", err)
	}
}

func TestAdmissionAnchorObservationIsLinearizedAgainstRawCapture(t *testing.T) {
	firstAt := time.Date(2026, 7, 22, 11, 58, 0, 0, time.UTC)
	lastAt := firstAt.Add(time.Minute)
	store := &meetingMemoryStore{entries: []meetingMemoryEntry{
		{Kind: meetingMemoryKindTranscript, CreatedAt: firstAt, Metadata: map[string]string{"roomId": "room-a", "meetingId": "sitting-a", "captureSequence": "17"}},
		{Kind: meetingMemoryKindTranscript, CreatedAt: firstAt.Add(30 * time.Second), Metadata: map[string]string{"roomId": "room-b", "meetingId": "sitting-a", "captureSequence": "18"}},
		{Kind: meetingMemoryKindBrain, CreatedAt: lastAt, Metadata: map[string]string{"roomId": "room-a", "meetingId": "sitting-a"}},
		{Kind: meetingMemoryKindTranscript, CreatedAt: lastAt, Metadata: map[string]string{"roomId": "room-a", "meetingId": "sitting-a", "captureSequence": "19"}},
	}}
	wantAdmission := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	admittedAt, cutoff, watermark, err := store.captureAdmissionObservation("room-a", "sitting-a", func() time.Time { return wantAdmission })
	if err != nil || !admittedAt.Equal(wantAdmission) || cutoff != 19 || !watermark.Equal(lastAt) {
		t.Fatalf("observation admittedAt=%s cutoff=%d watermark=%s err=%v", admittedAt, cutoff, watermark, err)
	}
}

func TestAdmissionAnchorPersistsBeforeBothAccessGrantedBranches(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	participantCase := sourceSectionForAdmissionTest(t, string(raw), `case "participant":`, `case "office":`)
	memberMarker := "// Identity comes from the authenticated session"
	memberAt := strings.Index(participantCase, memberMarker)
	if memberAt < 0 {
		t.Fatalf("participant case missing member branch marker %q", memberMarker)
	}
	branches := []struct {
		name         string
		source       string
		anchoredCall string
	}{
		{name: "guest", source: participantCase[:memberAt], anchoredCall: "admitGuestWithAnchorResult(context.Background()"},
		{name: "member", source: participantCase[memberAt:], anchoredCall: "admitParticipantWithAnchorResult(context.Background()"},
	}
	for _, branch := range branches {
		if count := strings.Count(branch.source, branch.anchoredCall); count != 1 {
			t.Fatalf("%s branch anchored admission call count=%d, want exactly 1", branch.name, count)
		}
		if count := strings.Count(branch.source, `sendKanbanEvent(c, "access_granted"`); count != 1 {
			t.Fatalf("%s branch access_granted count=%d, want exactly 1", branch.name, count)
		}
		admitAt := strings.Index(branch.source, branch.anchoredCall)
		prepareAt := strings.Index(branch.source, "prepareMeetingSittingID(connRoomID)")
		commitAt := strings.Index(branch.source, "publishAnchoredMeetingAdmission(admission)")
		grantAt := strings.Index(branch.source, `sendKanbanEvent(c, "access_granted"`)
		if prepareAt < 0 || commitAt < 0 || !(prepareAt < admitAt && admitAt < commitAt && commitAt < grantAt) {
			t.Fatalf("%s branch order prepare=%d anchored-admit=%d meeting-commit=%d grant=%d", branch.name, prepareAt, admitAt, commitAt, grantAt)
		}
	}
	anchorSource, err := os.ReadFile("admission_anchor.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, function := range []string{"admitParticipantWithAnchorResult", "admitGuestWithAnchorResult"} {
		section := sourceSectionForAdmissionTest(t, string(anchorSource), "func (app *kanbanBoardApp) "+function, "\n}\n")
		persistAt := strings.Index(section, "persistAdmissionAnchor(")
		meetingAt := strings.Index(section, "startMeetingDurable(")
		liveAt := strings.Index(section, "ParticipantSessionEndpointInRoomWithLeaseLocked(")
		if persistAt < 0 || meetingAt < 0 || liveAt < 0 || !(persistAt < meetingAt && meetingAt < liveAt) {
			t.Fatalf("%s order anchor=%d meeting=%d live=%d", function, persistAt, meetingAt, liveAt)
		}
	}
}

func sourceSectionForAdmissionTest(t *testing.T, source, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatalf("missing source start marker %q", startMarker)
	}
	endOffset := strings.Index(source[start+len(startMarker):], endMarker)
	if endOffset < 0 {
		t.Fatalf("missing source end marker %q after %q", endMarker, startMarker)
	}
	end := start + len(startMarker) + endOffset
	return source[start:end]
}

func TestAdmissionAnchorStartupFailureIsExplicitAndAdmissionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: &meetingMemoryStore{}}
	if err := app.initializeAdmissionAnchorStore(path); !errors.Is(err, ErrAdmissionAnchorStore) {
		t.Fatalf("initialize error=%v, want ErrAdmissionAnchorStore", err)
	}
	if err := app.admissionAnchorHealthError(); !errors.Is(err, ErrAdmissionAnchorStore) {
		t.Fatalf("health error=%v, want explicit store failure", err)
	}
	_, err := app.persistAdmissionAnchor(context.Background(), "room-a", "sitting-a", memberAdmissionPrincipal("member@example.com"))
	if !errors.Is(err, ErrAdmissionAnchorStore) {
		t.Fatalf("admission did not fail closed after startup error: %v", err)
	}
}

func TestAdmissionAnchorOpenProvesAtomicWritePathAndRuntimeFailureLatchesHealth(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "anchors.json")
	app := &kanbanBoardApp{memory: &meetingMemoryStore{path: filepath.Join(dir, "meeting-memory.jsonl")}}
	if err := app.initializeAdmissionAnchorStore(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("open did not create the checksummed writable probe: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path + ".lock")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("blocks state directory recreation"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := app.persistAdmissionAnchor(context.Background(), "room-a", "sitting-a", memberAdmissionPrincipal("member@example.com"))
	if err == nil {
		t.Fatal("runtime persistence failure unexpectedly succeeded")
	}
	if healthErr := app.admissionAnchorHealthError(); !errors.Is(healthErr, ErrAdmissionAnchorStore) {
		t.Fatalf("runtime failure did not latch readiness health: %v", healthErr)
	}
}

func TestAdmissionAnchorFailureCannotCreateGhostMeeting(t *testing.T) {
	dir := t.TempDir()
	memory, err := newMeetingMemoryStore(filepath.Join(dir, "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	meetings, err := loadMeetingStore(filepath.Join(dir, "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: memory, meetings: meetings}
	anchorPath := filepath.Join(dir, "anchors", "admission-anchors.json")
	if err := app.initializeAdmissionAnchorStore(anchorPath); err != nil {
		t.Fatal(err)
	}
	sittingID := app.prepareMeetingSittingID("room-a")
	if sittingID == "" {
		t.Fatal("failed to prepare sitting identity")
	}
	if _, found := meetings.activeRecord("room-a"); found {
		t.Fatal("sitting preparation opened a meeting before admission authority")
	}
	if err := os.Remove(anchorPath); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(anchorPath + ".lock")
	anchorDir := filepath.Dir(anchorPath)
	if err := os.Remove(anchorDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(anchorDir, []byte("block persistence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), "room-a", "AJ", "session-new", "endpoint-new", sittingID, memberAdmissionPrincipal("member@example.com")); err == nil {
		t.Fatal("broken anchored admission unexpectedly succeeded")
	}
	if _, found := meetings.activeRecord("room-a"); found {
		t.Fatal("failed anchor persistence left a ghost meeting")
	}
	app.mu.Lock()
	state := app.roomLiveLocked("room-a")
	if state.participantCounts["AJ"] != 0 || len(state.participantEndpoints["AJ"]) != 0 {
		app.mu.Unlock()
		t.Fatal("failed anchor persistence published an unanchored participant")
	}
	app.mu.Unlock()
}

func TestAdmissionAnchorFailurePreservesExistingEndpointSession(t *testing.T) {
	dir := t.TempDir()
	memory, err := newMeetingMemoryStore(filepath.Join(dir, "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	meetings, err := loadMeetingStore(filepath.Join(dir, "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: memory, meetings: meetings}
	anchorPath := filepath.Join(dir, "anchors", "admission-anchors.json")
	if err := app.initializeAdmissionAnchorStore(anchorPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom("room-a", "AJ", "session-old", "endpoint-1"); err != nil {
		t.Fatal(err)
	}
	sittingID := app.prepareMeetingSittingID("room-a")
	if err := os.Remove(anchorPath); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(anchorPath + ".lock")
	anchorDir := filepath.Dir(anchorPath)
	if err := os.Remove(anchorDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(anchorDir, []byte("block persistence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), "room-a", "AJ", "session-new", "endpoint-1", sittingID, memberAdmissionPrincipal("member@example.com")); err == nil {
		t.Fatal("broken anchored refresh unexpectedly succeeded")
	}
	app.mu.Lock()
	got := app.roomLiveLocked("room-a").participantEndpoints["AJ"]["endpoint-1"]
	app.mu.Unlock()
	if got != "session-old" {
		t.Fatalf("failed anchored refresh replaced prior endpoint session with %q", got)
	}
}

func TestTransferAnchorFailurePreservesExistingEndpointsAndMedia(t *testing.T) {
	dir := t.TempDir()
	memory, err := newMeetingMemoryStore(filepath.Join(dir, "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	meetings, err := loadMeetingStore(filepath.Join(dir, "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: memory, meetings: meetings}
	anchorPath := filepath.Join(dir, "anchors", "admission-anchors.json")
	if err := app.initializeAdmissionAnchorStore(anchorPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom("room-a", "AJ", "session-laptop", "endpoint-laptop"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpointInRoom("room-a", "AJ", "session-phone", "endpoint-phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setParticipantEndpointMediaStateInRoom("room-a", "AJ", "endpoint-laptop", "session-laptop", participantMediaState{MicMuted: true, CameraOff: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setParticipantEndpointMediaStateInRoom("room-a", "AJ", "endpoint-phone", "session-phone", participantMediaState{ScreenSharing: true}); err != nil {
		t.Fatal(err)
	}

	app.mu.Lock()
	room := app.roomLiveLocked("room-a")
	wantLaptopMedia := room.participantEndpointMedia["AJ"]["endpoint-laptop"]
	wantPhoneMedia := room.participantEndpointMedia["AJ"]["endpoint-phone"]
	wantLegacyMedia := room.participantMedia["AJ"]
	app.mu.Unlock()

	sittingID := app.prepareMeetingSittingID("room-a")
	if err := os.Remove(anchorPath); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(anchorPath + ".lock")
	anchorDir := filepath.Dir(anchorPath)
	if err := os.Remove(anchorDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(anchorDir, []byte("block persistence"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, retired, err := app.admitParticipantTransferWithAnchor(context.Background(), "room-a", "AJ", "session-tablet", "endpoint-tablet", sittingID, memberAdmissionPrincipal("member@example.com"))
	if !errors.Is(err, ErrAdmissionAnchorStore) {
		t.Fatalf("transfer error=%v, want ErrAdmissionAnchorStore", err)
	}
	if len(retired) != 0 {
		t.Fatalf("failed transfer retired sessions=%v", retired)
	}
	if !app.participantSessionCurrentInRoom("room-a", "AJ", "session-laptop") || !app.participantSessionCurrentInRoom("room-a", "AJ", "session-phone") || app.participantSessionCurrentInRoom("room-a", "AJ", "session-tablet") {
		t.Fatal("failed transfer changed current endpoint sessions")
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	room = app.roomLiveLocked("room-a")
	if len(room.participantEndpoints["AJ"]) != 2 || room.participantEndpoints["AJ"]["endpoint-laptop"] != "session-laptop" || room.participantEndpoints["AJ"]["endpoint-phone"] != "session-phone" {
		t.Fatalf("failed transfer changed endpoints=%+v", room.participantEndpoints["AJ"])
	}
	if got := room.participantEndpointMedia["AJ"]["endpoint-laptop"]; got != wantLaptopMedia {
		t.Fatalf("failed transfer changed laptop media=%+v want=%+v", got, wantLaptopMedia)
	}
	if got := room.participantEndpointMedia["AJ"]["endpoint-phone"]; got != wantPhoneMedia {
		t.Fatalf("failed transfer changed phone media=%+v want=%+v", got, wantPhoneMedia)
	}
	if got := room.participantMedia["AJ"]; got != wantLegacyMedia {
		t.Fatalf("failed transfer changed legacy media=%+v want=%+v", got, wantLegacyMedia)
	}
}

func TestMeetingRecordFailureAfterAnchorRecoversExactSittingOnRestart(t *testing.T) {
	root := t.TempDir()
	memoryPath := filepath.Join(root, "memory", "meeting-memory.jsonl")
	meetingDir := filepath.Join(root, "meeting-store")
	meetingPath := filepath.Join(meetingDir, "meetings.json")
	anchorPath := filepath.Join(root, "anchors", "admission-anchors.json")
	if err := os.MkdirAll(meetingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	memory, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	meetings, err := loadMeetingStore(meetingPath)
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: memory, meetings: meetings}
	if err := app.initializeAdmissionAnchorStore(anchorPath); err != nil {
		t.Fatal(err)
	}
	sittingID := app.prepareMeetingSittingID("room-a")
	if sittingID == "" {
		t.Fatal("missing prepared sitting id")
	}
	if err := os.Remove(meetingDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meetingDir, []byte("block atomic meeting replace"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err = app.admitParticipantWithAnchor(context.Background(), "room-a", "AJ", "session-a", "endpoint-a", sittingID, memberAdmissionPrincipal("aj@example.com"))
	if !errors.Is(err, ErrMeetingRecordStore) {
		t.Fatalf("admission error=%v, want ErrMeetingRecordStore", err)
	}
	if _, found := meetings.activeRecord("room-a"); found {
		t.Fatal("failed record-store commit left an in-memory ghost meeting")
	}
	app.mu.Lock()
	seated := app.roomLiveLocked("room-a").participantCounts["AJ"]
	app.mu.Unlock()
	if seated != 0 {
		t.Fatalf("failed record-store commit published %d live seats", seated)
	}
	starts, err := app.admissionAnchors.SittingStarts(context.Background())
	if err != nil || len(starts) != 1 || starts[0].SittingID != sittingID {
		t.Fatalf("durable anchors=%+v err=%v, want exact sitting %s", starts, err, sittingID)
	}
	wantStartedAt := starts[0].AdmittedAt

	if err := os.Remove(meetingDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(meetingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	restartedMemory, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	restartedMeetings, err := loadMeetingStore(meetingPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &kanbanBoardApp{memory: restartedMemory, meetings: restartedMeetings}
	if err := restarted.initializeAdmissionAnchorStore(anchorPath); err != nil {
		t.Fatal(err)
	}
	restarted.reconcileMeetingRecordsAtBoot()
	restarted.reconcileMeetingRecordsAtBoot()

	recovered, found := restarted.meetings.activeRecord("room-a")
	if !found || recovered.ID != sittingID {
		t.Fatalf("recovered=%+v found=%v, want sitting %s", recovered, found, sittingID)
	}
	gotStartedAt, err := time.Parse(time.RFC3339Nano, recovered.StartedAt)
	if err != nil || !gotStartedAt.Equal(wantStartedAt) {
		t.Fatalf("recovered start=%q err=%v, want anchor %s", recovered.StartedAt, err, wantStartedAt)
	}
	if got := restarted.memory.currentMeetingID("room-a"); got != sittingID {
		t.Fatalf("resumed memory sitting=%q, want %q", got, sittingID)
	}
	if got := len(restarted.meetings.recent(0)); got != 1 {
		t.Fatalf("repeated recovery produced %d meeting records, want 1", got)
	}
}

func TestMeetingRecordFailureAfterIdleCancelRearmsEmptySitting(t *testing.T) {
	root := t.TempDir()
	memory, err := newMeetingMemoryStore(filepath.Join(root, "memory", "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	meetingDir := filepath.Join(root, "meeting-store")
	meetingPath := filepath.Join(meetingDir, "meetings.json")
	meetings, err := loadMeetingStore(meetingPath)
	if err != nil {
		t.Fatal(err)
	}
	defer meetings.stopIdleEndsAndWait()
	app := &kanbanBoardApp{memory: memory, meetings: meetings}
	if err := app.initializeAdmissionAnchorStore(filepath.Join(root, "anchors", "admission-anchors.json")); err != nil {
		t.Fatal(err)
	}
	sittingID := app.prepareMeetingSittingID("room-a")
	if _, _, err := meetings.startMeetingDurable("room-a", sittingID, time.Now().UTC().Add(-time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	meetings.armIdleEnd("room-a", func(generation uint64) { app.endMeetingForIdle("room-a", generation) })

	// Force the anchored admission's participant-union persistence to fail
	// after it has canceled the pending idle timer.
	if err := os.Remove(meetingPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(meetingDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meetingDir, []byte("block atomic meeting replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = app.admitParticipantWithAnchor(context.Background(), "room-a", "AJ", "session-a", "endpoint-a", sittingID, memberAdmissionPrincipal("aj@example.com"))
	if !errors.Is(err, ErrMeetingRecordStore) {
		t.Fatalf("admission error=%v, want ErrMeetingRecordStore", err)
	}
	app.mu.Lock()
	seats := app.activeParticipantCountInRoomLocked(app.roomLiveLocked("room-a"))
	app.mu.Unlock()
	if seats != 0 {
		t.Fatalf("failed admission exposed %d live seats", seats)
	}
	open, found := meetings.activeRecord("room-a")
	if !found || open.ID != sittingID || open.EndedAt != "" {
		t.Fatalf("open record=%+v found=%v, want original empty sitting", open, found)
	}
	meetings.mu.Lock()
	rearmed := meetings.idleTimers[normalizeRoomID("room-a")] != nil
	meetings.mu.Unlock()
	if !rearmed {
		t.Fatal("failed admission canceled the only idle-close guard")
	}
}

func TestAnchoredAdmissionQueuesDefensivelyClosedPriorSittingInProcess(t *testing.T) {
	root := t.TempDir()
	memory, err := newMeetingMemoryStore(filepath.Join(root, "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	meetings, err := loadMeetingStore(filepath.Join(root, "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: memory, meetings: meetings}
	if err := app.initializeAdmissionAnchorStore(filepath.Join(root, "admission-anchors.json")); err != nil {
		t.Fatal(err)
	}
	const priorID = "prior-open-sitting"
	if _, _, err := meetings.startMeetingDurable("room-a", priorID, time.Now().UTC().Add(-time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	newSittingID := memory.ensureMeetingID("room-a")
	if newSittingID == priorID {
		t.Fatal("test requires a distinct durable sitting identity")
	}
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), "room-a", "AJ", "session-a", "endpoint-a", newSittingID, memberAdmissionPrincipal("aj@example.com")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		prior, found := meetings.recordByID(priorID)
		if found && prior.EndedReason == meetingEndedReasonRestart && meetingFinalizationReceiptReady(prior.Finalization) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("defensively closed prior sitting was not finalized in-process: %+v", prior.Finalization)
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, found := meetings.activeRecord("room-a")
	if !found || current.ID != newSittingID || current.EndedAt != "" {
		t.Fatalf("current sitting=%+v found=%v, want open %s", current, found, newSittingID)
	}
}

func TestTwoAnchoredSittingsRecoveryReceiptsOlderBoundary(t *testing.T) {
	root := t.TempDir()
	memory, err := newMeetingMemoryStore(filepath.Join(root, "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	meetings, err := loadMeetingStore(filepath.Join(root, "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := OpenAdmissionAnchorStore(filepath.Join(root, "anchors.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	principal := memberAdmissionPrincipal("aj@example.com")
	for index, sittingID := range []string{"sitting-one", "sitting-two"} {
		candidate := admissionAnchorForTest(base.Add(time.Duration(index)*time.Hour), sittingID, principal, uint64(index+1))
		candidate.TenantID = canonicalTenantID()
		if _, err := anchors.RecordFirst(context.Background(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	app := &kanbanBoardApp{memory: memory, meetings: meetings, admissionAnchors: anchors}
	app.reconcileMeetingRecordsAtBoot()

	first, found := meetings.recordByID("sitting-one")
	if !found || first.EndedAt != base.Add(time.Hour).Format(time.RFC3339Nano) || first.EndedReason != meetingEndedReasonRestart {
		t.Fatalf("older recovered sitting=%+v found=%v", first, found)
	}
	if first.Finalization == nil || first.Finalization.State != meetingFinalizationClosing {
		t.Fatalf("older recovered sitting receipt=%+v, want durable closing", first.Finalization)
	}
	second, found := meetings.activeRecord("room-a")
	if !found || second.ID != "sitting-two" || second.EndedAt != "" {
		t.Fatalf("latest recovered sitting=%+v found=%v", second, found)
	}
	needs := meetings.recordsNeedingFinalization()
	if len(needs) != 1 || needs[0].ID != "sitting-one" {
		t.Fatalf("restart finalization queue=%+v, want older sitting", needs)
	}
}
