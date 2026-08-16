package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrivateRiffSpaceConcurrentCreationIsCanonicalAndIdempotent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	type outcome struct {
		thread  scoutChatThreadRecord
		created bool
		err     error
	}
	results := make(chan outcome, 10)
	var group sync.WaitGroup
	for index := 0; index < 10; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			thread, created, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "concurrent-same-operation")
			results <- outcome{thread: thread, created: created, err: err}
		}()
	}
	group.Wait()
	close(results)
	spaceID := ""
	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent create: %v", result.err)
		}
		if result.created {
			createdCount++
		}
		if spaceID == "" {
			spaceID = result.thread.ID
		}
		if result.thread.ID != spaceID {
			t.Fatalf("concurrent create escaped space: %s != %s", result.thread.ID, spaceID)
		}
	}
	raw, _, err := app.scoutChatThreadByID(user.Email, spaceID)
	if err != nil || createdCount != 1 || len(raw.Riff.EpisodeRecords) != 1 {
		t.Fatalf("created=%d episodes=%d err=%v", createdCount, len(raw.Riff.EpisodeRecords), err)
	}
}

func commitRiffSpaceConversation(t *testing.T, app *kanbanBoardApp, user *userAccount, riff scoutChatThreadRecord, prefix string) scoutChatThreadRecord {
	t.Helper()
	now := time.Now().UTC()
	turns := []scoutChatMessageRecord{
		{ID: prefix + "-user", Kind: "message", Role: "user", AuthorName: user.Name, AuthorEmail: user.Email, Text: prefix + " private question", CreatedAt: now.Format(time.RFC3339Nano)},
		{ID: prefix + "-scout", Kind: "message", Role: "scout", AuthorName: scoutParticipantName, Text: prefix + " private answer", CreatedAt: now.Add(time.Nanosecond).Format(time.RFC3339Nano), Activity: completedPrivateRiffActivity(riff)},
	}
	updated, err := app.commitScoutChatThreadMessages(user.Email, riff.ID, turns...)
	if err != nil {
		t.Fatalf("commit %s conversation: %v", prefix, err)
	}
	return updated
}

func TestPrivateRiffSpaceTenInvocationsUseOneCanonicalThreadAndIsolatedEpisodes(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 12)
	spaceID := ""
	for index := 1; index <= 10; index++ {
		through := fmt.Sprintf("riff-source-%02d", index)
		riff, created, err := app.createPrivateRiffWithEntryPoint(user, source.ID, through, "", fmt.Sprintf("space-invocation-%02d", index), privateRiffEntryPointMessage, "")
		if err != nil || !created {
			t.Fatalf("invocation %d created=%v err=%v", index, created, err)
		}
		if spaceID == "" {
			spaceID = riff.ID
		}
		if riff.ID != spaceID || riff.Riff.SpaceID != spaceID || riff.Riff.ActiveEpisodeID == "" || riff.Riff.CheckpointID == "" {
			t.Fatalf("invocation %d escaped canonical space: %+v", index, riff.Riff)
		}
		riff = commitRiffSpaceConversation(t, app, user, riff, fmt.Sprintf("episode-%02d", index))
	}
	raw, _, err := app.scoutChatThreadByID(user.Email, spaceID)
	if err != nil || len(raw.Riff.EpisodeRecords) != 10 || len(raw.Messages) != 20 {
		t.Fatalf("canonical raw space episodes=%d messages=%d err=%v", len(raw.Riff.EpisodeRecords), len(raw.Messages), err)
	}
	projected := app.projectScoutChatThreadForViewer(user.Email, raw)
	if projected.ConversationKind != "channel_riff" || len(projected.Messages) != 2 || projected.Riff.EpisodeCount != 10 || len(projected.Riff.Episodes) != 10 {
		t.Fatalf("active projection=%+v messages=%d", projected.Riff, len(projected.Messages))
	}
	for _, message := range projected.Messages {
		if message.RiffEpisodeID != raw.Riff.ActiveEpisodeID || message.RiffCheckpointID == "" {
			t.Fatalf("message escaped active episode: %+v", message)
		}
	}
	for _, row := range app.scoutChatThreadsIndexView(user.Email, false, 100) {
		if row["conversationKind"] == "channel_riff" || row["id"] == spaceID {
			t.Fatalf("canonical Riff leaked into standard index: %+v", row)
		}
	}
	for _, row := range app.scoutChatThreadsView(user.Email, false, 100) {
		if row["conversationKind"] == "channel_riff" || row["id"] == spaceID {
			t.Fatalf("canonical Riff leaked into standard full list: %+v", row)
		}
	}
	replay, created, err := app.createPrivateRiffWithEntryPoint(user, source.ID, "riff-source-10", "", "space-invocation-10", privateRiffEntryPointMessage, "")
	if err != nil || created || replay.ID != spaceID || replay.Riff.ViewedEpisodeID != raw.Riff.ActiveEpisodeID {
		t.Fatalf("creation replay=%+v created=%v err=%v", replay.Riff, created, err)
	}
}

func TestPrivateRiffSpaceAutoFreshnessAdvancesOnBrainHighWaterOnly(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "brain-fresh-create")
	if err != nil {
		t.Fatal(err)
	}
	firstCheckpoint := riff.Riff.CheckpointID
	if _, appended, err := app.memory.appendBrainWriteUp("riff-brain-new-high-water", "## Overview\nA newly authorized company fact.", map[string]string{"visibility": "organization"}); err != nil || !appended {
		t.Fatalf("append brain high-water appended=%v err=%v", appended, err)
	}
	refreshed, err := app.autoRefreshPrivateRiffForTurn(user, riff.ID)
	if err != nil || refreshed.Riff.CheckpointID == firstCheckpoint || refreshed.Riff.ContextRevision != 2 || refreshed.Riff.BrainRevision != "riff-brain-new-high-water" {
		t.Fatalf("brain-only refresh binding=%+v err=%v", refreshed.Riff, err)
	}
	episodeIndex := privateRiffEpisodeIndex(refreshed.Riff, refreshed.Riff.ActiveEpisodeID)
	if episodeIndex < 0 || len(refreshed.Riff.EpisodeRecords[episodeIndex].Checkpoints) != 2 {
		t.Fatalf("brain-only checkpoint history=%+v", refreshed.Riff.EpisodeRecords)
	}
}

func TestPrivateRiffSpacePriorEpisodeReadAndExplicitResumeDoNotRewriteCheckpoint(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 3)
	first, _, err := app.createPrivateRiffWithEntryPoint(user, source.ID, "riff-source-02", "", "episode-first", privateRiffEntryPointMessage, "")
	if err != nil {
		t.Fatal(err)
	}
	firstID, firstCheckpoint := first.Riff.ActiveEpisodeID, first.Riff.CheckpointID
	first = commitRiffSpaceConversation(t, app, user, first, "first")
	second, _, err := app.createPrivateRiffWithEntryPoint(user, source.ID, "riff-source-03", "", "episode-second", privateRiffEntryPointMessage, "")
	if err != nil {
		t.Fatal(err)
	}
	secondID := second.Riff.ActiveEpisodeID
	second = commitRiffSpaceConversation(t, app, user, second, "second")
	prior := app.projectScoutChatThreadForViewerEpisode(user.Email, second, firstID)
	if prior.Riff.ViewedEpisodeID != firstID || prior.Riff.ActiveEpisodeID != secondID || prior.Riff.CheckpointID != firstCheckpoint || len(prior.Messages) != 2 {
		t.Fatalf("prior projection binding=%+v messages=%d", prior.Riff, len(prior.Messages))
	}
	staleSecondTurn := scoutChatMessageRecord{
		ID: "stale-second-turn", Kind: "message", Role: "user", AuthorName: user.Name, AuthorEmail: user.Email,
		Text: "must not land after episode switch", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		RiffEpisodeID: secondID, RiffCheckpointID: second.Riff.CheckpointID,
	}
	resumed, created, err := app.createPrivateRiffWithEntryPoint(user, source.ID, "riff-source-02", "", "resume-first-explicitly", privateRiffEntryPointResume, firstID)
	if err != nil || created || resumed.Riff.ActiveEpisodeID != firstID || resumed.Riff.CheckpointID != firstCheckpoint {
		t.Fatalf("resume created=%v binding=%+v err=%v", created, resumed.Riff, err)
	}
	episodeIndex := privateRiffEpisodeIndex(resumed.Riff, firstID)
	if episodeIndex < 0 || len(resumed.Riff.EpisodeRecords[episodeIndex].Checkpoints) != 1 {
		t.Fatalf("resume rewrote checkpoints: %+v", resumed.Riff.EpisodeRecords)
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, resumed.ID, staleSecondTurn); err == nil || !strings.Contains(err.Error(), "episode or checkpoint changed") {
		t.Fatalf("stale episode commit err=%v", err)
	}
	if _, _, err := app.refreshPrivateRiff(user, resumed.ID, "refresh-first-operation"); err != nil {
		t.Fatal(err)
	}
	third, _, err := app.createPrivateRiffWithEntryPoint(user, source.ID, "riff-source-03", "", "episode-third", privateRiffEntryPointMessage, "")
	if err != nil {
		t.Fatal(err)
	}
	replayedResume, replayCreated, err := app.createPrivateRiffWithEntryPoint(user, source.ID, "riff-source-02", "", "resume-first-explicitly", privateRiffEntryPointResume, firstID)
	if err != nil || replayCreated || replayedResume.Riff.ActiveEpisodeID != third.Riff.ActiveEpisodeID || replayedResume.Riff.ViewedEpisodeID != firstID {
		t.Fatalf("resume replay mutated newer active episode: created=%v binding=%+v err=%v", replayCreated, replayedResume.Riff, err)
	}
	if _, _, err := app.refreshPrivateRiff(user, third.ID, "refresh-first-operation"); err == nil || !strings.Contains(err.Error(), "different episode") {
		t.Fatalf("refresh idempotency crossed episodes err=%v", err)
	}
}

func TestPrivateRiffSpaceAutoFreshnessPreservesEarlierAnswerCheckpoint(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "auto-fresh-create")
	if err != nil {
		t.Fatal(err)
	}
	firstCheckpoint := riff.Riff.CheckpointID
	riff = commitRiffSpaceConversation(t, app, user, riff, "before-refresh")
	later := scoutChatMessageRecord{ID: "riff-source-03", Kind: "message", Role: "user", AuthorName: "Tim", AuthorEmail: "tim@shareability.com", Text: "new authorized fact", CreatedAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages(user.Email, source.ID, later); err != nil {
		t.Fatal(err)
	}
	refreshed, err := app.autoRefreshPrivateRiffForTurn(user, riff.ID)
	if err != nil || refreshed.Riff.CheckpointID == firstCheckpoint || refreshed.Riff.ContextRevision != 2 {
		t.Fatalf("auto refresh binding=%+v err=%v", refreshed.Riff, err)
	}
	episodeIndex := privateRiffEpisodeIndex(refreshed.Riff, refreshed.Riff.ActiveEpisodeID)
	if episodeIndex < 0 || len(refreshed.Riff.EpisodeRecords[episodeIndex].Checkpoints) != 2 {
		t.Fatalf("checkpoint history=%+v", refreshed.Riff.EpisodeRecords)
	}
	for _, message := range refreshed.Messages {
		if strings.HasPrefix(message.ID, "before-refresh-") && message.RiffCheckpointID != firstCheckpoint {
			t.Fatalf("older message checkpoint changed: %+v", message)
		}
	}
	refreshed = commitRiffSpaceConversation(t, app, user, refreshed, "after-refresh")
	for _, message := range refreshed.Messages {
		if strings.HasPrefix(message.ID, "after-refresh-") && message.RiffCheckpointID != refreshed.Riff.CheckpointID {
			t.Fatalf("new message missed latest checkpoint: %+v", message)
		}
	}
}

func TestPrivateRiffSpacePublicationIsBoundedToActiveEpisode(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 3)
	first, _, _ := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "publish-first")
	first = commitRiffSpaceConversation(t, app, user, first, "publish-first")
	firstID, firstReplyID := first.Riff.ActiveEpisodeID, "publish-first-scout"
	second, _, _ := app.createPrivateRiff(user, source.ID, "riff-source-03", "", "publish-second")
	second = commitRiffSpaceConversation(t, app, user, second, "publish-second")
	if _, err := app.publishPrivateRiffConversationEpisode(user, second.ID, firstID, "publish-inactive", privateRiffPublicationScopeReply, firstReplyID); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("inactive episode publication err=%v", err)
	}
	result, err := app.publishPrivateRiffConversationEpisode(user, second.ID, second.Riff.ActiveEpisodeID, "publish-active", privateRiffPublicationScopeAll, "")
	if err != nil || result.PublishedCount != 2 {
		t.Fatalf("active publication=%+v err=%v", result, err)
	}
	destination, _, _ := app.scoutChatThreadByID(user.Email, source.ID)
	posted := destination.Messages[len(destination.Messages)-2:]
	if posted[0].Text != "publish-second private question" || posted[1].Text != "publish-second private answer" {
		t.Fatalf("publication crossed episodes: %+v", posted)
	}
}

func TestPrivateRiffSpaceReferencesLegacyEpisodesWithoutMergingBodies(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	window, sourceBinding, through, err := privateRiffSourceBinding(source, "riff-source-02")
	if err != nil {
		t.Fatal(err)
	}
	legacyID := "private-riff-legacy-preserved"
	legacy := scoutChatThreadRecord{
		ID: legacyID, Title: "Riff on #country-golf", Preview: "legacy private body", OwnerEmail: user.Email,
		Visibility: scoutChatVisibilityPrivate, CreatedAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Messages: []scoutChatMessageRecord{{ID: "legacy-private-message", Kind: "message", Role: "user", AuthorName: user.Name, AuthorEmail: user.Email, Text: "legacy body must remain separate", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}},
		Riff: &privateRiffBinding{Version: privateRiffBindingVersion, SourceThreadID: source.ID, SourceTitle: source.Title,
			SourceMessageID: sourceBinding.MessageID, SourceMessageDigest: sourceBinding.MessageDigest, SourceWindowDigest: sourceBinding.WindowDigest,
			SourceAudienceDigest: conversationContinuityAudienceDigest(source), ThroughMessageID: through.ID, ThroughCreatedAt: through.CreatedAt,
			MessageCount: len(window), ContextRevision: 1, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), AgentID: agentMindScoutID, AgentName: scoutParticipantName, SourceAvailable: true},
	}
	encoded, _ := encodeScoutChatThread(legacy)
	legacyMetadata := scoutChatThreadMetadata(legacy)
	delete(legacyMetadata, "conversationKind")
	legacyMetadata["title"] = legacy.Title
	legacyMetadata["preview"] = legacy.Preview
	if _, _, err := app.memory.appendScoutChatThread(legacy.ID, encoded, legacyMetadata); err != nil {
		t.Fatal(err)
	}
	for _, row := range app.scoutChatThreadsIndexView(user.Email, false, 100) {
		if row["id"] == legacyID {
			t.Fatalf("pre-backfill legacy Riff flashed in standard index: %+v", row)
		}
	}
	space, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "canonical-after-legacy")
	if err != nil || space.ID == legacyID || space.Riff.LegacyEpisodeCount != 1 || len(space.Riff.LegacyEpisodeIDs) != 1 || space.Riff.LegacyEpisodeIDs[0] != legacyID {
		t.Fatalf("legacy references binding=%+v err=%v", space.Riff, err)
	}
	reloaded, _, err := app.scoutChatThreadByID(user.Email, legacyID)
	if err != nil || len(reloaded.Messages) != 1 || reloaded.Messages[0].Text != "legacy body must remain separate" || len(space.Messages) != 0 {
		t.Fatalf("legacy merge/delete reloaded=%+v spaceMessages=%d err=%v", reloaded, len(space.Messages), err)
	}
	if err := app.memory.backfillScoutChatIndexMetadata(); err != nil {
		t.Fatal(err)
	}
	for _, row := range app.scoutChatThreadsIndexView(user.Email, false, 100) {
		if row["id"] == legacyID || row["id"] == space.ID {
			t.Fatalf("legacy/canonical Riff leaked into standard index: %+v", row)
		}
	}
}

func TestPrivateRiffSourceRevocationHidesBodiesAndBlocksTurnsAndPublication(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	source.OwnerEmail = "tim@shareability.com"
	source.MemberEmails = []string{user.Email, source.OwnerEmail}
	if err := app.saveScoutChatThread(source); err != nil {
		t.Fatal(err)
	}
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "revocation-create")
	if err != nil {
		t.Fatal(err)
	}
	riff = commitRiffSpaceConversation(t, app, user, riff, "revoked")
	source.MemberEmails = []string{source.OwnerEmail}
	if err := app.saveScoutChatThread(source); err != nil {
		t.Fatal(err)
	}
	projected := app.projectScoutChatThreadForViewer(user.Email, riff)
	if len(projected.Messages) != 0 || projected.Title != "Private Riff" || projected.Riff == nil || projected.Riff.SourceAvailable {
		t.Fatalf("revoked projection leaked body/source: %+v messages=%+v", projected, projected.Messages)
	}
	if _, err := app.autoRefreshPrivateRiffForTurn(user, riff.ID); err == nil {
		t.Fatal("revoked source admitted a new turn")
	}
	if _, err := app.publishPrivateRiffConversation(user, riff.ID, "revoked-publish", privateRiffPublicationScopeAll, ""); err == nil {
		t.Fatal("revoked source admitted publication")
	}
	metadata := scoutChatThreadMetadata(riff)
	if metadata["conversationKind"] != "channel_riff" || strings.Contains(metadata["title"], source.Title) || strings.Contains(metadata["preview"], "revoked private") {
		t.Fatalf("unsafe Riff index metadata=%+v", metadata)
	}
}

func TestPrivateRiffSpaceHTTPEntryPointAndPriorEpisodeReadContract(t *testing.T) {
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	cookies := loginAs(t, user.Email, "B0NFIRE!")
	source := seedPrivateRiffChannel(t, kanbanApp, user.Email, 2)
	post := func(body string) map[string]any {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+source.ID+"/riff", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	firstResponse := post(`{"throughMessageId":"riff-source-01","operationId":"http-space-first","entryPoint":"message"}`)
	secondResponse := post(`{"throughMessageId":"riff-source-02","operationId":"http-space-second","entryPoint":"message"}`)
	firstThread := firstResponse["thread"].(map[string]any)
	secondThread := secondResponse["thread"].(map[string]any)
	if firstThread["id"] != secondThread["id"] {
		t.Fatalf("HTTP invocations created different spaces: %v %v", firstThread["id"], secondThread["id"])
	}
	firstRiff := firstThread["riff"].(map[string]any)
	firstEpisode := firstRiff["activeEpisodeId"].(string)
	request := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads/"+firstThread["id"].(string)+"?episodeId="+firstEpisode, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadHandler(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"viewedEpisodeId":"`+firstEpisode+`"`) {
		t.Fatalf("prior episode GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
