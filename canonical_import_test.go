package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func writeCanonicalFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func canonicalImportFixture(t *testing.T) CanonicalImportPaths {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	artifact := meetingMemoryEntry{ID: "artifact-1", Kind: meetingMemoryKindOSArtifact, Text: "SECRET artifact body", CreatedAt: now, Metadata: map[string]string{
		"roomId": "", "meetingId": "meeting-1", "title": "SECRET title", artifactVersionMetadataKey: "2",
		artifactVersionsMetadataKey: `[{"v":1,"at":"2026-07-12T19:00:00Z","bodyBlobRef":"` + strings.Repeat("a", 64) + `"}]`,
	}}
	transcript := meetingMemoryEntry{ID: "transcript-1", Kind: meetingMemoryKindTranscript, Text: "SECRET transcript", CreatedAt: now.Add(time.Second), Metadata: map[string]string{"meetingId": "meeting-1"}}
	var lines strings.Builder
	for _, entry := range []meetingMemoryEntry{artifact, transcript, transcript} { // exact duplicate must collapse globally
		raw, _ := json.Marshal(entry)
		lines.Write(raw)
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(memoryPath, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	boardPath := filepath.Join(dir, "kanban-board.json")
	writeCanonicalFixtureJSON(t, boardPath, kanbanBoardState{Cards: []kanbanCard{{ID: "card-b", Title: "SECRET roadmap", Status: kanbanStatusBacklog}, {ID: "card-a", Title: "SECRET launch", Status: kanbanStatusDone}}, UpdatedAt: now.Format(time.RFC3339Nano)})
	roomsPath := filepath.Join(dir, "rooms.json")
	writeCanonicalFixtureJSON(t, roomsPath, []roomRecord{{ID: officeRoomID, Name: "SECRET office", CreatedAt: now, PasscodeHash: "SECRET bcrypt", GuestEnabled: true, GuestLinks: []guestLinkRecord{{ID: "deadbeef", Hash: "SECRET guest token hash", Expires: now.Add(time.Hour), CreatedAt: now}}}})
	meetingsPath := filepath.Join(dir, "meetings.json")
	writeCanonicalFixtureJSON(t, meetingsPath, meetingStoreState{Meetings: []meetingRecord{{ID: "meeting-1", StartedAt: now.Format(time.RFC3339Nano), Participants: []string{"AJ"}}}})
	notificationsPath := filepath.Join(dir, "notifications.json")
	writeCanonicalFixtureJSON(t, notificationsPath, notificationStoreState{Notifications: []notificationRecord{{ID: "notification-1", UserEmail: "alice@example.com", Kind: notificationKindChat, Text: "SECRET notification", CreatedAt: now.Format(time.RFC3339Nano)}}})
	sharePath := filepath.Join(dir, "share-links.json")
	writeCanonicalFixtureJSON(t, sharePath, []shareLinkRecord{{ID: "share-1", ArtifactID: artifact.ID, Token: "SECRET-public-token", Status: shareLinkStatusActive, CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}})
	foldersPath := filepath.Join(dir, "file-folders.json")
	writeCanonicalFixtureJSON(t, foldersPath, fileFolderStoreState{Folders: []fileFolderRecord{{ID: "folder-1", Name: "SECRET deals", CreatedAt: now.Format(time.RFC3339Nano)}}, Assignments: map[string]string{"artifact-1": "folder-1"}})
	queueDir := filepath.Join(dir, "codex-jobs")
	writeCanonicalFixtureJSON(t, filepath.Join(queueDir, "job-1.json"), codexRunnerJob{ID: "job-1", ArtifactID: artifact.ID, ThreadID: "thread-1", Prompt: "SECRET prompt", Query: "SECRET query", Authority: codexJobAuthorityWorkspaceWrite, Status: codexJobStatusQueued, CreatedAt: now})
	archivesDir := filepath.Join(dir, "archives")
	writeCanonicalFixtureJSON(t, filepath.Join(archivesDir, "archive-1.json"), meetingArchive{ID: "archive-1", MeetingID: "meeting-1", ArchivedAt: now, Memory: []meetingMemoryEntry{transcript}})
	blobsDir := filepath.Join(dir, "blobs")
	ref := strings.Repeat("b", 64)
	writeCanonicalFixtureJSON(t, filepath.Join(blobsDir, ref[:2], ref+blobMetaSuffix), blobMeta{Mime: "application/pdf", Size: 42, CreatedAt: now.Format(time.RFC3339Nano)})
	deletedJournal := filepath.Join(dir, "deleted.jsonl")
	deleted, _ := json.Marshal(CanonicalLifecycleJournalRecord{Family: "memory", ObjectID: "old", StateDigest: strings.Repeat("c", 64), At: now, Reason: "SECRET deletion reason"})
	if err := os.WriteFile(deletedJournal, append(deleted, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return CanonicalImportPaths{MeetingMemory: memoryPath, Board: boardPath, Rooms: roomsPath, Meetings: meetingsPath, Notifications: notificationsPath, ShareLinks: sharePath, FileFolders: foldersPath, QueueDirs: []string{queueDir}, ArchivesDir: archivesDir, BlobsDir: blobsDir, DeletedJournal: deletedJournal}
}

func buildCanonicalFixturePlan(t *testing.T, paths CanonicalImportPaths, versionsPath string) (CanonicalImportPlan, *CanonicalPayloadRegistry) {
	t.Helper()
	versions, err := OpenFileCanonicalObjectVersionMap(versionsPath)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	importer := &CanonicalImporter{TenantID: "tenant-1", Paths: paths, Versions: versions, Registry: registry, Principals: func(object CanonicalImportedObject) []string {
		if object.Family == "notification" {
			return []string{"user:alice"}
		}
		return []string{"team:company"}
	}}
	plan, err := importer.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plan, registry
}

func TestCanonicalImportProductionShapedFixtureIsDeterministicAndSecretFree(t *testing.T) {
	paths := canonicalImportFixture(t)
	versionPath := filepath.Join(t.TempDir(), "versions.json")
	first, registry := buildCanonicalFixturePlan(t, paths, versionPath)
	second, _ := buildCanonicalFixturePlan(t, paths, versionPath)
	if len(first.Events) == 0 || len(first.Events) != len(second.Events) {
		t.Fatalf("event counts=%d/%d", len(first.Events), len(second.Events))
	}
	for index := range first.Events {
		if first.Events[index].EventID != second.Events[index].EventID || first.Events[index].AggregateVersion != second.Events[index].AggregateVersion {
			t.Fatalf("event %d changed across import: %+v / %+v", index, first.Events[index], second.Events[index])
		}
	}
	serialized, _ := json.Marshal(first.Events)
	for _, secret := range []string{"SECRET artifact body", "SECRET transcript", "SECRET-public-token", "alice@example.com", "SECRET notification", "SECRET prompt", "SECRET query", "SECRET title", "SECRET deletion reason"} {
		if strings.Contains(string(serialized), secret) {
			t.Fatalf("immutable events leaked %q", secret)
		}
	}
	seenFamilies := map[string]bool{}
	for _, object := range first.Objects {
		seenFamilies[object.Family] = true
	}
	for _, family := range []string{"memory", "artifact_revision", "board_card", "room", "guest_link", "meeting", "notification", "share_link", "file_folder", "file_assignment", "queue_job", "archive", "blob", "tombstone"} {
		if !seenFamilies[family] {
			t.Fatalf("fixture missing imported family %s", family)
		}
	}
	memoryCount := 0
	for _, object := range first.Objects {
		if object.Family == "memory" {
			memoryCount++
		}
	}
	if memoryCount != 2 {
		t.Fatalf("memory objects=%d, want duplicate id collapsed", memoryCount)
	}
	store := NewMemoryCanonicalEventStore(registry)
	if err := first.Apply(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if err := second.Apply(context.Background(), store); err != nil {
		t.Fatalf("second apply must be idempotent: %v", err)
	}
}

func TestCanonicalImportUnrelatedBoardReorderPreservesExistingIdentity(t *testing.T) {
	paths := canonicalImportFixture(t)
	left, _ := buildCanonicalFixturePlan(t, paths, filepath.Join(t.TempDir(), "left.json"))
	var board kanbanBoardState
	if ok, err := readJSONIfExists(paths.Board, &board); err != nil || !ok {
		t.Fatal(err)
	}
	board.Cards = []kanbanCard{{ID: "card-0", Title: "new unrelated", Status: kanbanStatusBacklog}, board.Cards[1], board.Cards[0]}
	writeCanonicalFixtureJSON(t, paths.Board, board)
	right, _ := buildCanonicalFixturePlan(t, paths, filepath.Join(t.TempDir(), "right.json"))
	ids := func(plan CanonicalImportPlan) map[string]uuid.UUID {
		result := map[string]uuid.UUID{}
		for _, object := range plan.Objects {
			if object.Family == "board_card" {
				result[object.ObjectID] = object.EventID
			}
		}
		return result
	}
	leftIDs, rightIDs := ids(left), ids(right)
	for _, id := range []string{"card-a", "card-b"} {
		if leftIDs[id] != rightIDs[id] {
			t.Fatalf("%s identity changed: %s != %s", id, leftIDs[id], rightIDs[id])
		}
	}
}

func TestCanonicalImportConflictingGlobalMemoryIDPreservesFirstOccurrence(t *testing.T) {
	paths := canonicalImportFixture(t)
	file, err := os.OpenFile(paths.MeetingMemory, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	conflict := meetingMemoryEntry{ID: "transcript-1", Kind: meetingMemoryKindBrain, Text: "different content and kind", CreatedAt: time.Now().UTC()}
	raw, _ := json.Marshal(conflict)
	_, _ = file.Write(append(raw, '\n'))
	_ = file.Close()
	versions, _ := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "versions.json"))
	registry, _ := NewCanonicalImportPayloadRegistry()
	plan, err := (&CanonicalImporter{TenantID: "tenant-1", Paths: paths, Versions: versions, Registry: registry}).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstDigest := digestText("SECRET transcript")
	found := 0
	for _, object := range plan.Objects {
		if object.Family == "memory" && object.ObjectID == "transcript-1" {
			found++
			if object.ContentDigest != firstDigest {
				t.Fatalf("duplicate replaced first occurrence: %s", object.ContentDigest)
			}
		}
	}
	if found != 1 {
		t.Fatalf("transcript-1 objects=%d, want exactly first occurrence", found)
	}
}

func TestCanonicalCheckedDigestRejectsUnsupportedNumericState(t *testing.T) {
	if digest, err := digestAny(math.NaN()); err == nil || digest != "" {
		t.Fatalf("digestAny(NaN)=(%q,%v), want checked failure", digest, err)
	}
}

func TestCanonicalParitySnapshotIsPerFamilyAndPrincipal(t *testing.T) {
	paths := canonicalImportFixture(t)
	plan, _ := buildCanonicalFixturePlan(t, paths, filepath.Join(t.TempDir(), "versions.json"))
	snapshot := BuildCanonicalParitySnapshot(plan.Objects)
	if snapshot.Families["memory"].Count != 2 || snapshot.Principals["user:alice"]["notification"].Count != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	ids := append([]string(nil), snapshot.Families["board_card"].IDs...)
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("ids not sorted: %v", ids)
	}
}
