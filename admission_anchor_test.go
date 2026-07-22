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
		{name: "guest", source: participantCase[:memberAt], anchoredCall: "admitGuestWithAnchor(context.Background()"},
		{name: "member", source: participantCase[memberAt:], anchoredCall: "admitParticipantWithAnchor(context.Background()"},
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
		commitAt := strings.Index(branch.source, "noteMeetingAdmissionForSitting(connRoomID, admittedName, sittingID)")
		grantAt := strings.Index(branch.source, `sendKanbanEvent(c, "access_granted"`)
		if prepareAt < 0 || commitAt < 0 || !(prepareAt < admitAt && admitAt < commitAt && commitAt < grantAt) {
			t.Fatalf("%s branch order prepare=%d anchored-admit=%d meeting-commit=%d grant=%d", branch.name, prepareAt, admitAt, commitAt, grantAt)
		}
	}
	anchorSource, err := os.ReadFile("admission_anchor.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, function := range []string{"admitParticipantWithAnchor", "admitGuestWithAnchor"} {
		section := sourceSectionForAdmissionTest(t, string(anchorSource), "func (app *kanbanBoardApp) "+function, "\n}\n")
		persistAt := strings.Index(section, "persistAdmissionAnchor(")
		commitAt := strings.Index(section, "admitParticipantSessionEndpointInRoomLocked(")
		if persistAt < 0 || commitAt < 0 || persistAt >= commitAt {
			t.Fatalf("%s does not persist before live-state commit", function)
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
