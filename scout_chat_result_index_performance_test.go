package main

import (
	"fmt"
	"testing"
	"time"
)

func TestScoutChatTailResultProjectionIsBoundedByArtifactsAndVisibleMessages(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const (
		lifetimeRows = 20_000
		resultCount  = 63
	)
	owner := "aj@shareability.com"
	base := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	thread := scoutChatThreadRecord{
		ID: "dense-result-tail", Title: "Dense result tail", OwnerEmail: owner,
		Visibility: scoutChatVisibilityPrivate, CreatedAt: base.Format(time.RFC3339Nano), UpdatedAt: base.Add(resultCount * time.Second).Format(time.RFC3339Nano),
	}

	app.memory.mu.Lock()
	app.memory.entries = append(app.memory.entries, meetingMemoryEntry{
		ID: thread.ID, Kind: meetingMemoryKindScoutChat, Text: `{"id":"dense-result-tail"}`, CreatedAt: base,
		Metadata: map[string]string{"visibility": scoutChatVisibilityPrivate, "ownerEmail": owner},
	})
	for index := 0; index < lifetimeRows; index++ {
		app.memory.entries = append(app.memory.entries, meetingMemoryEntry{
			ID: fmt.Sprintf("lifetime-row-%05d", index), Kind: meetingMemoryKindBrain,
			Text: "bounded historical context", CreatedAt: base.Add(-time.Duration(index+1) * time.Second),
			Metadata: map[string]string{"visibility": "organization"},
		})
	}
	for index := 0; index < resultCount; index++ {
		runID := fmt.Sprintf("terminal-run-%02d", index)
		artifactID := fmt.Sprintf("terminal-artifact-%02d", index)
		artifact := meetingMemoryEntry{
			ID: artifactID, Kind: meetingMemoryKindOSArtifact, Text: fmt.Sprintf("# Result %02d\n\nCurrent exact result.", index), CreatedAt: base.Add(time.Duration(index) * time.Second),
			Metadata: map[string]string{
				"type": artifactTypeMarkdown, "source": "scout_thread", "threadId": runID,
				"status": "complete", "threadStatus": "complete", "visibility": scoutChatVisibilityPrivate,
				"ownerEmail": owner, "requestedBy": owner, "tenantId": canonicalArtifactTenantID(),
				"objectId": artifactID, "aclVersion": "1", artifactVersionMetadataKey: "1",
				"originSurface": "chat:" + thread.ID,
			},
		}
		artifact.Metadata[artifactContentDigestMetadataKey] = artifactCapabilityDigest(artifact)
		app.memory.entries = append(app.memory.entries, artifact)
		app.memory.seen[artifact.ID] = struct{}{}
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{
			ID: fmt.Sprintf("terminal-message-%02d", index), Kind: "thread", Role: "scout", Text: "Deliverable ready",
			CreatedAt: base.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
			Thread:    &scoutChatThreadRef{ID: runID, Mode: "research", Status: "complete", ArtifactID: artifactID},
		})
	}
	app.memory.rebuildMeetingEntryIndexesLocked()
	visits := 0
	chatVisits := 0
	app.memory.artifactEntryVisitHook = func() { visits++ }
	app.memory.scoutChatEntryVisitHook = func() { chatVisits++ }
	app.memory.mu.Unlock()

	started := time.Now()
	projected, page, err := app.projectScoutChatThreadTailForViewer(owner, thread, "", "", 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.MessageCount != resultCount || len(projected.Messages) != resultCount {
		t.Fatalf("tail page=%+v messages=%d, want %d", page, len(projected.Messages), resultCount)
	}
	for index, message := range projected.Messages {
		if message.Thread == nil || message.Thread.ResultArtifactID != fmt.Sprintf("terminal-artifact-%02d", index) || message.Thread.ResultPreview == "" {
			t.Fatalf("message %d result=%+v", index, message.Thread)
		}
	}
	// One metadata-directory pass plus a small constant number of exact current
	// header/body checks per visible terminal card. Lifetime transcript/Brain
	// rows must not enter the bound.
	if maximum := resultCount * 5; visits > maximum {
		t.Fatalf("artifact projection visited %d rows, want <= %d for %d lifetime rows", visits, maximum, lifetimeRows)
	}
	if maximum := resultCount * 6; chatVisits > maximum {
		t.Fatalf("artifact audience projection visited %d chat rows, want <= %d for %d lifetime rows", chatVisits, maximum, lifetimeRows)
	}
	t.Logf("projected %d terminal results over %d lifetime rows with %d artifact + %d chat indexed visits in %s", resultCount, lifetimeRows, visits, chatVisits, time.Since(started))
}

func TestScoutChatTailWithoutTerminalWorkSkipsResultDirectory(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := scoutChatThreadRecord{ID: "ordinary-tail", OwnerEmail: "aj@shareability.com", Visibility: scoutChatVisibilityPrivate}
	for index := 0; index < 80; index++ {
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{ID: fmt.Sprintf("message-%02d", index), Kind: "message", Role: "user", Text: "ordinary chat"})
	}
	previousProbe := scoutChatResultIndexProbe
	builds := 0
	scoutChatResultIndexProbe = func() { builds++ }
	t.Cleanup(func() { scoutChatResultIndexProbe = previousProbe })

	if _, _, err := app.projectScoutChatThreadTailForViewer(thread.OwnerEmail, thread, "", "", 80, ""); err != nil {
		t.Fatal(err)
	}
	if builds != 0 {
		t.Fatalf("ordinary tail built %d result indexes, want 0", builds)
	}
}

func TestArtifactDirectoryTracksCurrentRevisionAndDeletion(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Indexed result", "v1", "AJ", map[string]string{
		"visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	visits := 0
	app.memory.mu.Lock()
	app.memory.artifactEntryVisitHook = func() { visits++ }
	app.memory.mu.Unlock()
	headerV1, found := app.memory.artifactAuthorizationHeaderByID(artifact.ID)
	if !found || visits != 1 {
		t.Fatalf("initial directory lookup found=%t visits=%d", found, visits)
	}
	updated, changed, err := app.updateOSArtifact(artifact.ID, "", "v2", "AJ")
	if err != nil || !changed {
		t.Fatalf("update changed=%t err=%v", changed, err)
	}
	visits = 0
	headerV2, found := app.memory.artifactAuthorizationHeaderByID(artifact.ID)
	if !found || visits != 1 || headerV2.ContentRevision != int64(artifactVersion(updated)) || headerV2.ContentDigest == headerV1.ContentDigest {
		t.Fatalf("updated directory header=%+v found=%t visits=%d prior=%+v", headerV2, found, visits, headerV1)
	}
	if _, _, deleted, err := app.memory.deleteOSArtifactWithProjection(artifact.ID); err != nil || !deleted {
		t.Fatalf("delete deleted=%t err=%v", deleted, err)
	}
	if _, found := app.memory.artifactAuthorizationHeaderByID(artifact.ID); found {
		t.Fatal("deleted artifact remained in exact directory")
	}
}
