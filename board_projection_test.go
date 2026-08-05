package main

import (
	"context"
	"testing"
)

func TestBoardProjectionUsesOnlyViewerAuthorizedArtifactAndProjectContext(t *testing.T) {
	setupAuthTestEnv(t)
	previousAuthorizer := artifactObjectAuthorizer
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { artifactObjectAuthorizer = previousAuthorizer })
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seeded AJ account missing")
	}
	channel, err := app.createScoutChatThread(user.Email, user.Name, "Country Golf", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create project channel: %v", err)
	}
	card := app.snapshotState().Cards[0]
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Country Golf brief", "# Country Golf\n\nDelivered.", user.Name, map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete",
		"boardCardId": card.ID, "originKind": agentThreadOriginChannel, "originId": channel.ID,
		"requestedBy": user.Email, "createdBy": user.Email, "savedToFiles": "true",
	})
	if err != nil {
		t.Fatalf("create linked artifact: %v", err)
	}

	projection := app.boardProjectionForViewer(context.Background(), user)
	var found *boardCardViewerProjection
	for index := range projection.Cards {
		if projection.Cards[index].CardID == card.ID {
			found = &projection.Cards[index]
			break
		}
	}
	if found == nil {
		t.Fatalf("projection missing card %q: %+v", card.ID, projection.Cards)
	}
	if found.DeliveryStage != boardDeliveryDrive || found.ProjectID != channel.ID || found.ProjectTitle != channel.Title || found.ProjectResolution != "linked" || found.ArtifactID != artifact.ID {
		t.Fatalf("projection=%+v, want Drive + authorized Country Golf linkage", *found)
	}
}

func TestBoardProjectionLeavesAmbiguousCardsInNeedsProject(t *testing.T) {
	setupAuthTestEnv(t)
	previousAuthorizer := artifactObjectAuthorizer
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { artifactObjectAuthorizer = previousAuthorizer })
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seeded Tim account missing")
	}
	projection := app.boardProjectionForViewer(context.Background(), user)
	if len(projection.Cards) == 0 {
		t.Fatal("expected seeded cards")
	}
	for _, card := range projection.Cards {
		if card.ProjectID != "needs-project" || card.ProjectResolution != "missing" {
			t.Fatalf("unlinked card projection=%+v, want explicit Needs project", card)
		}
	}
}
