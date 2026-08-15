package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func appendPreparedLifecycleForTest(t *testing.T, path string, record CanonicalLifecycleJournalRecord) {
	t.Helper()
	canonicalLifecycleJournalMu.Lock()
	defer canonicalLifecycleJournalMu.Unlock()
	if err := appendCanonicalLifecycleRecordsLocked(path, []CanonicalLifecycleJournalRecord{record}); err != nil {
		t.Fatal(err)
	}
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

type canonicalLifecycleHarness struct {
	versions *FileCanonicalObjectVersionMap
	registry *CanonicalPayloadRegistry
	store    *MemoryCanonicalEventStore
}

func newCanonicalLifecycleHarness(t *testing.T) canonicalLifecycleHarness {
	t.Helper()
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return canonicalLifecycleHarness{versions: versions, registry: registry, store: NewMemoryCanonicalEventStore(registry)}
}

func (h canonicalLifecycleHarness) build(t *testing.T, paths CanonicalImportPaths) CanonicalImportPlan {
	t.Helper()
	plan, err := (&CanonicalImporter{
		TenantID: "tenant-lifecycle-test", Paths: paths, Versions: h.versions, Registry: h.registry,
		Principals: func(CanonicalImportedObject) []string { return []string{"team:company"} },
	}).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func (h canonicalLifecycleHarness) applyAndRequireParity(t *testing.T, plan CanonicalImportPlan) {
	t.Helper()
	if err := plan.Apply(context.Background(), h.store); err != nil {
		t.Fatal(err)
	}
	report, err := ReconcileCanonicalPlanWithOptions(context.Background(), plan, h.store, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(plan)})
	if err != nil || report.Diverged || len(report.Candidates) != 0 {
		t.Fatalf("lifecycle parity diverged=%v candidates=%+v err=%v", report.Diverged, report.Candidates, err)
	}
}

func requireDeletedLifecycleObject(t *testing.T, plan CanonicalImportPlan, family, id, priorDigest string) {
	t.Helper()
	for _, object := range plan.Objects {
		if object.Family == family && object.ObjectID == id && object.Deleted {
			if object.StateDigest == priorDigest {
				t.Fatalf("deleted lifecycle object %s/%s reused live state digest", family, id)
			}
			return
		}
	}
	t.Fatalf("missing deleted lifecycle object %s/%s", family, id)
}

func TestCanonicalMemoryAndArtifactDeletionLifecyclePreventsNewTargetOnlyDrift(t *testing.T) {
	setupAuthTestEnv(t)
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	store, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	note, _, err := store.appendNote("memory-delete-lifecycle", "Delete this memory", map[string]string{"createdBy": "aj@shareability.com"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := store.appendOSArtifact("artifact-delete-lifecycle", "Artifact body", map[string]string{
		"ownerEmail": "aj@shareability.com", artifactVersionMetadataKey: "2",
		artifactVersionsMetadataKey: `[{"v":1,"at":"2026-08-14T12:00:00Z","bodyBlobRef":"` + strings.Repeat("a", 64) + `"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	noteObjects, _ := canonicalMemoryImportedObjects(note)
	artifactObjects, _ := canonicalMemoryImportedObjects(artifact)
	paths := CanonicalImportPaths{MeetingMemory: memoryPath, DeletedJournal: journalPath}
	harness := newCanonicalLifecycleHarness(t)
	initial := harness.build(t, paths)
	harness.applyAndRequireParity(t, initial)

	if _, removed, err := store.deleteEntryByID(note.ID); err != nil || !removed {
		t.Fatalf("delete memory removed=%v err=%v", removed, err)
	}
	if _, projection, removed, err := store.deleteOSArtifactWithProjection(artifact.ID); err != nil || !removed {
		t.Fatalf("delete artifact removed=%v projection=%+v err=%v", removed, projection, err)
	}
	records, err := boardLifecycleCommittedRecords(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	want := append(noteObjects, artifactObjects...)
	for _, object := range want {
		found := false
		for _, record := range records {
			if record.Family == object.Family && record.ObjectID == object.ObjectID && record.StateDigest == object.StateDigest {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing exact committed lifecycle record for %s/%s digest=%s records=%+v", object.Family, object.ObjectID, object.StateDigest, records)
		}
	}
	restarted, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := restarted.entryByKindAndID(meetingMemoryKindOSArtifact, artifact.ID); found {
		t.Fatal("deleted artifact returned after restart")
	}
	after := harness.build(t, paths)
	harness.applyAndRequireParity(t, after)
	for _, object := range want {
		requireDeletedLifecycleObject(t, after, object.Family, object.ObjectID, object.StateDigest)
	}
}

func TestCanonicalMemoryDeletionAmbiguousPublishReloadsVisibleGeneration(t *testing.T) {
	for _, artifact := range []bool{false, true} {
		name := "memory"
		if artifact {
			name = "artifact"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
			journalPath := filepath.Join(dir, "deleted-objects.jsonl")
			t.Setenv("MEETING_MEMORY_PATH", memoryPath)
			t.Setenv("BONFIRE_CANONICAL_MODE", "shadow")
			setCanonicalRuntime(nil)
			t.Cleanup(func() { setCanonicalRuntime(nil) })
			store, err := newMeetingMemoryStore(memoryPath)
			if err != nil {
				t.Fatal(err)
			}
			var entry meetingMemoryEntry
			if artifact {
				entry, _, err = store.appendOSArtifact("ambiguous-artifact", "artifact body", map[string]string{"ownerEmail": "owner@example.com"})
			} else {
				entry, _, err = store.appendNote("ambiguous-memory", "memory body", nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Ensure the journal already exists so the injected directory-sync
			// failure occurs after the source rename, not while preparing it.
			if err := os.WriteFile(journalPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			previousSync := syncDirectoryForAtomicWrite
			syncDirectoryForAtomicWrite = func(string) error { return errors.New("injected directory fsync failure") }
			t.Cleanup(func() { syncDirectoryForAtomicWrite = previousSync })
			if artifact {
				if _, _, removed, deleteErr := store.deleteOSArtifactWithProjection(entry.ID); !errors.Is(deleteErr, ErrDurableReplaceAmbiguous) || removed {
					t.Fatalf("artifact ambiguous delete removed=%v err=%v", removed, deleteErr)
				}
			} else if _, removed, deleteErr := store.deleteEntryByID(entry.ID); !errors.Is(deleteErr, ErrDurableReplaceAmbiguous) || removed {
				t.Fatalf("memory ambiguous delete removed=%v err=%v", removed, deleteErr)
			}
			if _, found := store.entryByKindAndID(entry.Kind, entry.ID); found {
				t.Fatal("visible-after deletion was resurrected in RAM")
			}
			if objects, importErr := importMemoryObjects(memoryPath); importErr != nil {
				t.Fatal(importErr)
			} else {
				for _, object := range objects {
					if object.Family == "memory" && object.ObjectID == entry.ID {
						t.Fatal("visible-after deletion remained on disk")
					}
				}
			}
			syncDirectoryForAtomicWrite = previousSync
			if _, _, appendErr := store.appendNote("post-ambiguous-write", "must not resurrect", nil); appendErr != nil {
				t.Fatal(appendErr)
			}
			restarted, restartErr := newMeetingMemoryStore(memoryPath)
			if restartErr != nil {
				t.Fatal(restartErr)
			}
			if _, found := restarted.entryByKindAndID(entry.Kind, entry.ID); found {
				t.Fatal("later rewrite or restart resurrected ambiguously deleted entry")
			}
			committed, commitErr := boardLifecycleCommittedRecords(journalPath)
			if commitErr != nil {
				t.Fatal(commitErr)
			}
			foundMemory := false
			for _, record := range committed {
				foundMemory = foundMemory || record.Family == "memory" && record.ObjectID == entry.ID
			}
			if !foundMemory {
				t.Fatalf("restart did not commit pending ambiguous deletion: %+v", committed)
			}
		})
	}
}

func TestCanonicalNotificationTruncationAndFolderRemovalLifecycleReconcile(t *testing.T) {
	setupAuthTestEnv(t)
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "meeting-memory.jsonl"))
	notificationPath := filepath.Join(dir, "notifications.json")
	folderPath := filepath.Join(dir, "file-folders.json")
	t.Setenv("NOTIFICATIONS_PATH", notificationPath)
	app := &kanbanBoardApp{}
	now := time.Now().UTC()
	for index := 0; index < notificationStoreCap; index++ {
		app.notifications = append(app.notifications, notificationRecord{ID: fmt.Sprintf("notification-%03d", index), Kind: notificationKindInfo, Text: fmt.Sprintf("notice %d", index), CreatedAt: now.Add(time.Duration(index) * time.Millisecond).Format(time.RFC3339Nano)})
	}
	app.notifications[0].Kind = "ALERT"
	app.notifications[0].UserEmail = "AJ@SHAREABILITY.COM"
	oldestObject, _ := canonicalNotificationImportedObject(app.notifications[0])
	if err := app.persistNotificationsLocked(); err != nil {
		t.Fatal(err)
	}
	loadedAtBoot, err := loadNotificationStoreState(notificationPath)
	if err != nil {
		t.Fatal(err)
	}
	app.notifications = loadedAtBoot
	folders := newFileFolderStore(folderPath)
	folder, err := folders.create("Lifecycle folder", "aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := folders.assign("file-a", folder.ID); err != nil {
		t.Fatal(err)
	}
	if err := folders.assign("file-b", folder.ID); err != nil {
		t.Fatal(err)
	}
	folderObject, _ := canonicalFileFolderImportedObject(folder)
	assignmentA, _ := canonicalFileAssignmentImportedObject("file-a", folder.ID)
	assignmentB, _ := canonicalFileAssignmentImportedObject("file-b", folder.ID)
	paths := CanonicalImportPaths{Notifications: notificationPath, FileFolders: folderPath, DeletedJournal: filepath.Join(dir, "deleted-objects.jsonl")}
	harness := newCanonicalLifecycleHarness(t)
	harness.applyAndRequireParity(t, harness.build(t, paths))

	if _, err := app.createNotificationRecord("", nil, notificationKindInfo, "new retained notification", "", "", "", "", "", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	if err := folders.remove(folder.ID); err != nil {
		t.Fatal(err)
	}
	reloadedNotifications, err := loadNotificationStoreState(notificationPath)
	if err != nil || len(reloadedNotifications) != notificationStoreCap || reloadedNotifications[0].ID == oldestObject.ObjectID {
		t.Fatalf("reloaded notifications=%d first=%q err=%v", len(reloadedNotifications), reloadedNotifications[0].ID, err)
	}
	restartedFolders := newFileFolderStore(folderPath)
	if restartedFolders.loadErr != nil {
		t.Fatal(restartedFolders.loadErr)
	}
	if gotFolders, gotAssignments := restartedFolders.snapshot(); len(gotFolders) != 0 || len(gotAssignments) != 0 {
		t.Fatalf("folder lifecycle survived restart folders=%+v assignments=%+v", gotFolders, gotAssignments)
	}
	after := harness.build(t, paths)
	harness.applyAndRequireParity(t, after)
	for _, object := range []CanonicalImportedObject{oldestObject, folderObject, assignmentA, assignmentB} {
		requireDeletedLifecycleObject(t, after, object.Family, object.ObjectID, object.StateDigest)
	}
}

func TestCanonicalLifecycleJournalFailureRetainsLegacySources(t *testing.T) {
	setupAuthTestEnv(t)
	previousAppend := canonicalLifecycleAppend
	canonicalLifecycleAppend = func(string, []byte) error { return errors.New("journal unavailable") }
	t.Cleanup(func() { canonicalLifecycleAppend = previousAppend })

	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	memory, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := memory.appendNote("journal-failure-memory", "must survive", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, removed, err := memory.deleteEntryByID(entry.ID); err == nil || removed {
		t.Fatalf("memory journal failure removed=%v err=%v", removed, err)
	}
	if restarted, err := newMeetingMemoryStore(memoryPath); err != nil {
		t.Fatal(err)
	} else if _, found := restarted.entryByKindAndID(entry.Kind, entry.ID); !found {
		t.Fatal("memory source disappeared after journal failure")
	}

	notificationPath := filepath.Join(dir, "notifications.json")
	t.Setenv("NOTIFICATIONS_PATH", notificationPath)
	app := &kanbanBoardApp{}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for index := 0; index < notificationStoreCap; index++ {
		app.notifications = append(app.notifications, notificationRecord{ID: fmt.Sprintf("failure-notification-%03d", index), Kind: notificationKindInfo, Text: "must survive", CreatedAt: now})
	}
	if err := app.persistNotificationsLocked(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.createNotificationRecord("", nil, notificationKindInfo, "must fail", "", "", "", "", "", "", "", "", true); err == nil {
		t.Fatal("notification truncation ignored journal failure")
	}
	loaded, err := loadNotificationStoreState(notificationPath)
	if err != nil || len(loaded) != notificationStoreCap || loaded[0].ID != "failure-notification-000" {
		t.Fatalf("notification source changed after journal failure len=%d first=%q err=%v", len(loaded), loaded[0].ID, err)
	}

	folderPath := filepath.Join(dir, "file-folders.json")
	folders := newFileFolderStore(folderPath)
	folder, err := folders.create("Must survive", "aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := folders.assign("file-survives", folder.ID); err != nil {
		t.Fatal(err)
	}
	if err := folders.remove(folder.ID); err == nil {
		t.Fatal("folder deletion ignored journal failure")
	}
	restartedFolders := newFileFolderStore(folderPath)
	gotFolders, gotAssignments := restartedFolders.snapshot()
	if len(gotFolders) != 1 || gotFolders[0].ID != folder.ID || gotAssignments["file-survives"] != folder.ID {
		t.Fatalf("folder source changed after journal failure folders=%+v assignments=%+v", gotFolders, gotAssignments)
	}
}

func TestCanonicalJournaledLegacyDeletionRecoveryCompletesOnlyExactSources(t *testing.T) {
	setupAuthTestEnv(t)
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "meeting-memory.jsonl"))
	now := time.Now().UTC()

	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	memory, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := memory.appendNote("journaled-memory-recovery", "recover exact source", nil)
	if err != nil {
		t.Fatal(err)
	}
	memoryObjects, _ := canonicalMemoryImportedObjects(entry)
	if err := ensureCanonicalLifecycleJournalBatch(journalPath, canonicalLifecycleDeletionRecords(memoryObjects, time.Now().UTC(), "memory_deleted")); err != nil {
		t.Fatal(err)
	}
	restartedMemory, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := restartedMemory.entryByKindAndID(entry.Kind, entry.ID); found {
		t.Fatal("restart recovery retained exact journaled memory source")
	}

	notificationPath := filepath.Join(dir, "notifications.json")
	notification := notificationRecord{ID: "journaled-notification-recovery", Kind: notificationKindInfo, Text: "recover exact notification", CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano)}
	if err := writeJSONFileAtomically(notificationPath, "notification recovery fixture", notificationStoreState{Notifications: []notificationRecord{notification}}); err != nil {
		t.Fatal(err)
	}
	notificationObject, _ := canonicalNotificationImportedObject(notification)
	if err := ensureCanonicalLifecycleJournalBatch(journalPath, canonicalLifecycleDeletionRecords([]CanonicalImportedObject{notificationObject}, now.Add(2*time.Second), "notification_retention_truncated")); err != nil {
		t.Fatal(err)
	}
	if recovered, err := loadNotificationStoreState(notificationPath); err != nil || len(recovered) != 0 {
		t.Fatalf("notification recovery=%+v err=%v", recovered, err)
	}

	folderPath := filepath.Join(dir, "file-folders.json")
	folders := newFileFolderStore(folderPath)
	folder, err := folders.create("Journaled recovery", "aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := folders.assign("journaled-file", folder.ID); err != nil {
		t.Fatal(err)
	}
	folderObject, _ := canonicalFileFolderImportedObject(folder)
	assignmentObject, _ := canonicalFileAssignmentImportedObject("journaled-file", folder.ID)
	if err := ensureCanonicalLifecycleJournalBatch(journalPath, canonicalLifecycleDeletionRecords([]CanonicalImportedObject{folderObject, assignmentObject}, now.Add(3*time.Second), "file_folder_deleted")); err != nil {
		t.Fatal(err)
	}
	restartedFolders := newFileFolderStore(folderPath)
	if restartedFolders.loadErr != nil {
		t.Fatal(restartedFolders.loadErr)
	}
	if gotFolders, gotAssignments := restartedFolders.snapshot(); len(gotFolders) != 0 || len(gotAssignments) != 0 {
		t.Fatalf("folder recovery folders=%+v assignments=%+v", gotFolders, gotAssignments)
	}
}

func TestCanonicalLegacyLifecycleRecoveryPreservesSameStateRecreations(t *testing.T) {
	setupAuthTestEnv(t)
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	notificationPath := filepath.Join(dir, "notifications.json")
	folderPath := filepath.Join(dir, "file-folders.json")
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("NOTIFICATIONS_PATH", notificationPath)
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", folderPath)

	deletedAt := time.Now().UTC().Add(-time.Minute)
	memoryEntry := meetingMemoryEntry{ID: "aba-memory", Kind: meetingMemoryKindNote, Text: "same state", CreatedAt: deletedAt.Add(time.Minute)}
	memoryObject, _ := canonicalMemoryImportedObjects(memoryEntry)
	notification := notificationRecord{ID: "aba-notification", Kind: notificationKindInfo, Text: "same state", CreatedAt: deletedAt.Add(time.Minute).Format(time.RFC3339Nano)}
	notificationObject, _ := canonicalNotificationImportedObject(notification)
	folder := fileFolderRecord{ID: "aba-folder", Name: "Same state", CreatedAt: deletedAt.Add(time.Minute).Format(time.RFC3339Nano)}
	folderObject, _ := canonicalFileFolderImportedObject(folder)
	assignmentObject, _ := canonicalFileAssignmentImportedObject("aba-file", folder.ID)
	records := canonicalLifecycleDeletionRecords(append(append(memoryObject, notificationObject), folderObject, assignmentObject), deletedAt, "aba_fixture")
	if err := ensureCanonicalLifecycleJournalBatch(journalPath, records); err != nil {
		t.Fatal(err)
	}
	memoryRaw, _ := json.Marshal(memoryEntry)
	if err := os.WriteFile(memoryPath, append(memoryRaw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSONFixture(t, notificationPath, notificationStoreState{Notifications: []notificationRecord{notification}})
	writeJSONFixture(t, folderPath, fileFolderStoreState{
		Folders: []fileFolderRecord{folder}, Assignments: map[string]string{"aba-file": folder.ID},
		AssignmentUpdatedAt: map[string]string{"aba-file": deletedAt.Add(time.Minute).Format(time.RFC3339Nano)},
	})

	if store, err := newMeetingMemoryStore(memoryPath); err != nil {
		t.Fatal(err)
	} else if _, found := store.entryByKindAndID(memoryEntry.Kind, memoryEntry.ID); !found {
		t.Fatal("same-state newer memory recreation was removed")
	}
	if records, err := loadNotificationStoreState(notificationPath); err != nil || len(records) != 1 {
		t.Fatalf("same-state newer notification recreation records=%+v err=%v", records, err)
	}
	folders := newFileFolderStore(folderPath)
	if folders.loadErr != nil {
		t.Fatal(folders.loadErr)
	}
	if gotFolders, gotAssignments := folders.snapshot(); len(gotFolders) != 1 || gotAssignments["aba-file"] != folder.ID {
		t.Fatalf("same-state newer folder/assignment recreation removed folders=%+v assignments=%+v", gotFolders, gotAssignments)
	}
}

func TestCanonicalFileAssignmentLegacyGenerationFailsSafe(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	folderPath := filepath.Join(dir, "file-folders.json")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", folderPath)
	folder := fileFolderRecord{ID: "legacy-folder", Name: "Legacy", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	legacy := fileFolderStoreState{Folders: []fileFolderRecord{folder}, Assignments: map[string]string{"legacy-file": folder.ID}}
	writeJSONFixture(t, folderPath, legacy)
	if store := newFileFolderStore(folderPath); store.loadErr != nil {
		t.Fatalf("legacy file without assignment generation stopped loading: %v", store.loadErr)
	}
	assignment, _ := canonicalFileAssignmentImportedObject("legacy-file", folder.ID)
	if err := ensureCanonicalLifecycleJournalBatch(canonicalDeletedLifecycleJournalPath(), canonicalLifecycleDeletionRecords([]CanonicalImportedObject{assignment}, time.Now().UTC().Add(time.Second), "legacy_assignment_fixture")); err != nil {
		t.Fatal(err)
	}
	store := newFileFolderStore(folderPath)
	if store.loadErr == nil || !strings.Contains(store.loadErr.Error(), "lacks generation") {
		t.Fatalf("unversioned assignment recovery did not fail safe: %v", store.loadErr)
	}
	var durable fileFolderStoreState
	if _, err := readJSONIfExists(folderPath, &durable); err != nil {
		t.Fatal(err)
	}
	if durable.Assignments["legacy-file"] != folder.ID {
		t.Fatal("unversioned assignment was erased solely on digest match")
	}
}

func TestCanonicalLifecycleOverrideSourcesUseCentralJournal(t *testing.T) {
	setupAuthTestEnv(t)
	central := t.TempDir()
	notificationDir := t.TempDir()
	folderDir := t.TempDir()
	memoryPath := filepath.Join(central, "meeting-memory.jsonl")
	notificationPath := filepath.Join(notificationDir, "notifications.json")
	folderPath := filepath.Join(folderDir, "file-folders.json")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("NOTIFICATIONS_PATH", notificationPath)
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", folderPath)
	app := &kanbanBoardApp{}
	for index := 0; index < notificationStoreCap; index++ {
		app.notifications = append(app.notifications, notificationRecord{ID: fmt.Sprintf("override-%03d", index), Kind: notificationKindInfo, Text: "notice", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	}
	if err := app.persistNotificationsLocked(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.createNotificationRecord("", nil, notificationKindInfo, "overflow", "", "", "", "", "", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	folders := newFileFolderStore(folderPath)
	folder, err := folders.create("Override", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := folders.remove(folder.ID); err != nil {
		t.Fatal(err)
	}
	centralRecords, err := boardLifecycleCommittedRecords(filepath.Join(central, "deleted-objects.jsonl"))
	if err != nil || len(centralRecords) != 2 {
		t.Fatalf("central lifecycle records=%+v err=%v", centralRecords, err)
	}
	for _, adjacent := range []string{filepath.Join(notificationDir, "deleted-objects.jsonl"), filepath.Join(folderDir, "deleted-objects.jsonl")} {
		if _, err := os.Stat(adjacent); !os.IsNotExist(err) {
			t.Fatalf("override source received a split lifecycle journal %s: %v", adjacent, err)
		}
	}
}

func TestCanonicalNotificationBootTruncationSourceFailureAbortsThenRetries(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	notificationPath := filepath.Join(dir, "notifications.json")
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("NOTIFICATIONS_PATH", notificationPath)
	records := make([]notificationRecord, 0, notificationStoreCap+1)
	for index := 0; index <= notificationStoreCap; index++ {
		records = append(records, notificationRecord{ID: fmt.Sprintf("boot-overflow-%03d", index), Kind: notificationKindInfo, Text: "retention", CreatedAt: time.Now().UTC().Add(time.Duration(index) * time.Millisecond).Format(time.RFC3339Nano)})
	}
	writeJSONFixture(t, notificationPath, notificationStoreState{Notifications: records})
	previousReplace := notificationLifecycleReplace
	notificationLifecycleReplace = func(string, notificationStoreState) error { return errors.New("source replace unavailable") }
	t.Cleanup(func() { notificationLifecycleReplace = previousReplace })
	if _, err := loadNotificationStoreState(notificationPath); err == nil {
		t.Fatal("boot truncation ignored source replacement failure")
	}
	var durable notificationStoreState
	if _, err := readJSONIfExists(notificationPath, &durable); err != nil || len(durable.Notifications) != notificationStoreCap+1 {
		t.Fatalf("failed boot truncation changed source len=%d err=%v", len(durable.Notifications), err)
	}
	committed, err := boardLifecycleCommittedRecords(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 0 {
		t.Fatalf("aborted boot truncation became committed evidence: %+v", committed)
	}
	notificationLifecycleReplace = previousReplace
	loaded, err := loadNotificationStoreState(notificationPath)
	if err != nil || len(loaded) != notificationStoreCap {
		t.Fatalf("retry boot truncation len=%d err=%v", len(loaded), err)
	}
	committed, err = boardLifecycleCommittedRecords(journalPath)
	if err != nil || len(committed) != 1 || committed[0].ObjectID != records[0].ID {
		t.Fatalf("retry committed evidence=%+v err=%v", committed, err)
	}
}

func TestCanonicalNotificationBootTruncationCrashAfterSourceReplaceRecovers(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	notificationPath := filepath.Join(dir, "notifications.json")
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("NOTIFICATIONS_PATH", notificationPath)
	removed := notificationRecord{ID: "boot-crash-removed", Kind: notificationKindInfo, Text: "removed before crash", CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)}
	object, _ := canonicalNotificationImportedObject(removed)
	prepared := canonicalLifecycleDeletionRecords([]CanonicalImportedObject{object}, time.Now().UTC(), "notification_retention_truncated")[0]
	prepared.OperationID, prepared.Phase = "notification-boot-crash-op", canonicalLifecyclePhasePrepared
	appendPreparedLifecycleForTest(t, journalPath, prepared)
	retained := []notificationRecord{{ID: "boot-crash-retained", Kind: notificationKindInfo, Text: "retained", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	writeJSONFixture(t, notificationPath, notificationStoreState{Notifications: retained})
	loaded, err := loadNotificationStoreState(notificationPath)
	if err != nil || len(loaded) != 1 || loaded[0].ID != retained[0].ID {
		t.Fatalf("crash recovery loaded=%+v err=%v", loaded, err)
	}
	committed, err := boardLifecycleCommittedRecords(journalPath)
	if err != nil || len(committed) != 1 || committed[0].ObjectID != removed.ID {
		t.Fatalf("crash recovery committed evidence=%+v err=%v", committed, err)
	}
}

func TestCanonicalLegacyPreparedLifecycleCrashWindowsAndRestartIdempotence(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	t.Setenv("BONFIRE_CANONICAL_MODE", "off")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	oldAt := time.Now().UTC().Add(-time.Minute)
	entry := meetingMemoryEntry{ID: "prepared-present", Kind: meetingMemoryKindNote, Text: "present", CreatedAt: oldAt}
	object, _ := canonicalMemoryImportedObjects(entry)
	raw, _ := json.Marshal(entry)
	if err := os.WriteFile(memoryPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared := canonicalLifecycleDeletionRecords(object, time.Now().UTC(), "crash_present")[0]
	prepared.OperationID, prepared.Phase = "prepared-present-op", canonicalLifecyclePhasePrepared
	appendPreparedLifecycleForTest(t, journalPath, prepared)
	if _, err := newMeetingMemoryStore(memoryPath); err != nil {
		t.Fatal(err)
	}

	absentEntry := meetingMemoryEntry{ID: "prepared-absent", Kind: meetingMemoryKindNote, Text: "absent", CreatedAt: oldAt}
	absentObject, _ := canonicalMemoryImportedObjects(absentEntry)
	absentPrepared := canonicalLifecycleDeletionRecords(absentObject, time.Now().UTC(), "crash_absent")[0]
	absentPrepared.OperationID, absentPrepared.Phase = "prepared-absent-op", canonicalLifecyclePhasePrepared
	appendPreparedLifecycleForTest(t, journalPath, absentPrepared)
	if _, err := newMeetingMemoryStore(memoryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := newMeetingMemoryStore(memoryPath); err != nil {
		t.Fatal(err)
	}
	records, err := readCanonicalLifecycleJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	committed, operations, err := classifyLifecycleJournal(records)
	if err != nil {
		t.Fatal(err)
	}
	if operations["prepared-present-op"].terminal == nil || operations["prepared-present-op"].terminal.Phase != canonicalLifecyclePhaseAborted {
		t.Fatalf("source-present operation=%+v", operations["prepared-present-op"])
	}
	if operations["prepared-absent-op"].terminal == nil || operations["prepared-absent-op"].terminal.Phase != canonicalLifecyclePhaseCommitted {
		t.Fatalf("source-absent operation=%+v", operations["prepared-absent-op"])
	}
	terminalCounts := map[string]int{}
	for _, record := range records {
		if record.Phase == canonicalLifecyclePhaseCommitted || record.Phase == canonicalLifecyclePhaseAborted {
			terminalCounts[record.OperationID]++
		}
	}
	if terminalCounts["prepared-present-op"] != 1 || terminalCounts["prepared-absent-op"] != 1 {
		t.Fatalf("restart duplicated terminal phases: %v", terminalCounts)
	}
	foundAbsent := false
	for _, record := range committed {
		foundAbsent = foundAbsent || record.ObjectID == absentEntry.ID
		if record.ObjectID == entry.ID {
			t.Fatal("aborted source-present lifecycle became committed evidence")
		}
	}
	if !foundAbsent {
		t.Fatal("source-absent prepared lifecycle did not commit")
	}
}

func TestCanonicalRequiredBootRecoversLegacyPreparedBeforeImport(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "required")
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	committedEntry := meetingMemoryEntry{ID: "required-committed-live-source", Kind: meetingMemoryKindNote, Text: "journal committed before crash", CreatedAt: time.Now().UTC().Add(-2 * time.Minute)}
	committedObjects, _ := canonicalMemoryImportedObjects(committedEntry)
	committedRaw, _ := json.Marshal(committedEntry)
	if err := os.WriteFile(memoryPath, append(committedRaw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureCanonicalLifecycleJournalBatch(journalPath, canonicalLifecycleDeletionRecords(committedObjects, time.Now().UTC().Add(-time.Minute), "required_committed_crash_window")); err != nil {
		t.Fatal(err)
	}
	entry := meetingMemoryEntry{ID: "required-crash-window", Kind: meetingMemoryKindNote, Text: "deleted before crash", CreatedAt: time.Now().UTC().Add(-time.Minute)}
	objects, _ := canonicalMemoryImportedObjects(entry)
	prepared := canonicalLifecycleDeletionRecords(objects, time.Now().UTC(), "required_crash_window")[0]
	prepared.OperationID, prepared.Phase = "required-crash-op", canonicalLifecyclePhasePrepared
	appendPreparedLifecycleForTest(t, journalPath, prepared)
	_, err := initializeCanonicalRuntime(context.Background())
	if err == nil || !strings.Contains(err.Error(), "BONFIRE_CANONICAL_DATABASE_URL") {
		t.Fatalf("required boot error=%v, want missing database after recovery", err)
	}
	if raw, readErr := os.ReadFile(memoryPath); readErr != nil || len(strings.TrimSpace(string(raw))) != 0 {
		t.Fatalf("required pre-import recovery retained committed live source raw=%q err=%v", raw, readErr)
	}
	committed, readErr := boardLifecycleCommittedRecords(journalPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(committed) != 2 {
		t.Fatalf("required boot did not recover legacy lifecycle before import/database gate: %+v", committed)
	}
}

func TestBoardAndLegacyPendingLifecycleRecoveryRemainFamilyScoped(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "board.json")
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	journalPath := filepath.Join(dir, "deleted-objects.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	card := kanbanCard{ID: "mixed-card", Title: "Mixed pending", Status: kanbanStatusBacklog}
	boardObject, err := canonicalBoardCardImportedObject(card, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	beforeRaw, _ := marshalKanbanBoardState(kanbanBoardState{Cards: []kanbanCard{card}, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	afterRaw, _ := marshalKanbanBoardState(kanbanBoardState{Cards: nil, UpdatedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)})
	if err := os.WriteFile(boardPath, beforeRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	boardPrepared := CanonicalLifecycleJournalRecord{Family: "board_card", ObjectID: card.ID, StateDigest: boardObject.StateDigest, OperationID: "mixed-board-op", Phase: canonicalLifecyclePhasePrepared, BoardBeforeSHA256: exactSHA256(beforeRaw), BoardAfterSHA256: exactSHA256(afterRaw), At: time.Now().UTC(), Reason: "mixed_fixture"}
	legacyPrepared := CanonicalLifecycleJournalRecord{Family: "memory", ObjectID: "mixed-memory", StateDigest: strings.Repeat("a", 64), OperationID: "mixed-memory-op", Phase: canonicalLifecyclePhasePrepared, At: time.Now().UTC(), Reason: "mixed_fixture"}
	canonicalLifecycleJournalMu.Lock()
	if err := appendCanonicalLifecycleRecordsLocked(journalPath, []CanonicalLifecycleJournalRecord{boardPrepared, legacyPrepared}); err != nil {
		canonicalLifecycleJournalMu.Unlock()
		t.Fatal(err)
	}
	canonicalLifecycleJournalMu.Unlock()
	if err := recoverBoardLifecycleTransactions(boardPath, journalPath); err != nil {
		t.Fatal(err)
	}
	_, operations, _ := classifyLifecycleJournal(mustReadLifecycleRecords(t, journalPath))
	if operations["mixed-board-op"].terminal == nil || operations["mixed-memory-op"].terminal != nil {
		t.Fatalf("board recovery crossed family boundary: board=%+v memory=%+v", operations["mixed-board-op"], operations["mixed-memory-op"])
	}
	if err := recoverLegacyLifecycleTransactionsForPaths(CanonicalImportPaths{MeetingMemory: memoryPath}); err != nil {
		t.Fatal(err)
	}
	_, operations, _ = classifyLifecycleJournal(mustReadLifecycleRecords(t, journalPath))
	if operations["mixed-memory-op"].terminal == nil || operations["mixed-memory-op"].terminal.Phase != canonicalLifecyclePhaseCommitted {
		t.Fatalf("legacy recovery did not own non-board pending operation: %+v", operations["mixed-memory-op"])
	}
}

func mustReadLifecycleRecords(t *testing.T, path string) []CanonicalLifecycleJournalRecord {
	t.Helper()
	records, err := readCanonicalLifecycleJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}
