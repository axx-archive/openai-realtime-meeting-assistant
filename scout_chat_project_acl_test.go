package main

import "testing"

func TestScoutChatProjectMembershipRestrictsReadAndListing(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, created, err := app.ensureScoutChatThread(
		"stride_project_example",
		"aj@shareability.com",
		"AJ",
		"Dog Perfect",
		scoutChatVisibilityPublic,
		[]string{"tim@shareability.com"},
	)
	if err != nil || !created {
		t.Fatalf("create member-scoped project: created=%v err=%v", created, err)
	}
	if got := scoutChatThreadMemberEmails(thread); len(got) != 2 || got[0] != "aj@shareability.com" || got[1] != "tim@shareability.com" {
		t.Fatalf("members=%v, want canonical owner+member", got)
	}
	if !scoutChatThreadAllowsViewer(thread, "AJ@SHAREABILITY.COM") || !scoutChatThreadAllowsViewer(thread, "tim@shareability.com") {
		t.Fatal("exact project members must retain access")
	}
	if scoutChatThreadAllowsViewer(thread, "caitlyn@shareability.com") {
		t.Fatal("nonmember gained project access")
	}
	metadata := scoutChatThreadMetadata(thread)
	if !scoutChatThreadMetadataAllowsViewer(metadata, "tim@shareability.com") || scoutChatThreadMetadataAllowsViewer(metadata, "caitlyn@shareability.com") {
		t.Fatalf("metadata authority did not preserve exact project membership: %#v", metadata)
	}
	if _, _, err := app.scoutChatThreadByID("caitlyn@shareability.com", thread.ID); err == nil {
		t.Fatal("nonmember read member-scoped project by id")
	}
	for _, listed := range app.scoutChatThreadsSnapshot("caitlyn@shareability.com", true, 100) {
		if listed.ID == thread.ID {
			t.Fatal("member-scoped project leaked into nonmember thread list")
		}
	}
	if _, _, err := app.scoutChatThreadByID("tim@shareability.com", thread.ID); err != nil {
		t.Fatalf("member could not read project: %v", err)
	}
	if _, err := scoutChatTypingEventPayload(app, &userAccount{Email: "caitlyn@shareability.com"}, thread.ID, true); err == nil {
		t.Fatal("nonmember emitted a project typing signal")
	}
	if _, err := scoutChatTypingEventPayload(app, &userAccount{Email: "tim@shareability.com"}, thread.ID, true); err != nil {
		t.Fatalf("member typing signal was rejected: %v", err)
	}
	if audience := strideRuntimeChatAudience(thread); audience.Visibility != "project" || len(audience.Principals) != 2 {
		t.Fatalf("project audience=%#v, want two exact principals", audience)
	}
}

func TestEnsureScoutChatThreadIsCrashRetryIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	first, created, err := app.ensureScoutChatThread("stride_project_retry", "aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic, []string{"tim@shareability.com"})
	if err != nil || !created {
		t.Fatalf("first ensure: created=%v err=%v", created, err)
	}
	second, created, err := app.ensureScoutChatThread("stride_project_retry", "aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic, []string{"tim@shareability.com"})
	if err != nil || created || second.ID != first.ID || second.CreatedAt != first.CreatedAt {
		t.Fatalf("exact retry: first=%#v second=%#v created=%v err=%v", first, second, created, err)
	}
	if _, _, err := app.ensureScoutChatThread("stride_project_retry", "aj@shareability.com", "AJ", "Different project", scoutChatVisibilityPublic, []string{"tim@shareability.com"}); err == nil {
		t.Fatal("same operation id with a different title must fail closed")
	}
	if _, _, err := app.ensureScoutChatThread("stride_project_retry", "aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic, []string{"caitlyn@shareability.com"}); err == nil {
		t.Fatal("same operation id with different membership must fail closed")
	}
	count := 0
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindScoutChat && entry.ID == first.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("durable records=%d, want exactly one", count)
	}
}

func TestOrganizationPublicThreadCompatibilityRemainsOpen(t *testing.T) {
	thread := scoutChatThreadRecord{ID: "general", OwnerEmail: "aj@shareability.com", Visibility: scoutChatVisibilityPublic}
	if !scoutChatThreadIsOrganizationPublic(thread) || !scoutChatThreadAllowsViewer(thread, "caitlyn@shareability.com") {
		t.Fatal("empty project membership must preserve legacy organization-wide channel access")
	}
}
