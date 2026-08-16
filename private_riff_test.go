package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seedPrivateRiffChannel(t *testing.T, app *kanbanBoardApp, owner string, count int) scoutChatThreadRecord {
	t.Helper()
	thread, err := app.createScoutChatThread(owner, "AJ", "country-golf", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create source channel: %v", err)
	}
	for index := 1; index <= count; index++ {
		message := scoutChatMessageRecord{
			ID: fmt.Sprintf("riff-source-%02d", index), Kind: "message", Role: "user",
			Text:       fmt.Sprintf("Country Golf checkpoint detail number %02d has the approved venue plan", index),
			CreatedAt:  time.Now().UTC().Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
			AuthorName: "AJ", AuthorEmail: owner,
		}
		thread, err = app.commitScoutChatThreadMessages(owner, thread.ID, message)
		if err != nil {
			t.Fatalf("append source %d: %v", index, err)
		}
	}
	return thread
}

func TestPrivateRiffCreatesExactOwnerOnlyWindowAndRefreshesExplicitly(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 20)

	riff, created, err := app.createPrivateRiff(user, source.ID, "riff-source-20", "", "riff-create-op")
	if err != nil || !created {
		t.Fatalf("create riff created=%v err=%v", created, err)
	}
	if riff.Riff == nil || riff.Riff.MessageCount != agentThreadSourceConversationWindow || riff.Riff.ContextRevision != 1 {
		t.Fatalf("riff binding=%+v", riff.Riff)
	}
	if _, _, err := app.scoutChatThreadByID("tim@shareability.com", riff.ID); err == nil {
		t.Fatal("another channel member could read the owner-only riff")
	}
	projected := app.projectScoutChatThreadForViewer(user.Email, riff)
	if projected.Riff == nil || !projected.Riff.SourceAvailable || projected.Riff.SourceMessageDigest != "" || projected.Riff.SourceWindowDigest != "" || projected.Riff.SourceAudienceDigest != "" || projected.Riff.BrainRevision != "" || projected.Riff.CreationOperationID != "" {
		t.Fatalf("unsafe or incomplete riff projection=%+v", projected.Riff)
	}

	later := scoutChatMessageRecord{
		ID: "riff-source-21", Kind: "message", Role: "user", Text: "This later channel detail must not enter the frozen riff until refresh.",
		CreatedAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano), AuthorName: "Tim", AuthorEmail: "tim@shareability.com",
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, source.ID, later); err != nil {
		t.Fatalf("append later source: %v", err)
	}
	projected = app.projectScoutChatThreadForViewer(user.Email, riff)
	if projected.Riff.NewMessageCount != 1 {
		t.Fatalf("new message count=%d, want 1", projected.Riff.NewMessageCount)
	}
	query, count, err := app.privateRiffModelQuery(user.Email, riff, "What was the approved venue plan?")
	if err != nil || count != 16 {
		t.Fatalf("model context count=%d err=%v", count, err)
	}
	if strings.Contains(query, later.Text) || !strings.Contains(query, "detail number 20") {
		t.Fatalf("frozen query leaked later source or missed checkpoint: %s", query)
	}

	refreshed, changed, err := app.refreshPrivateRiff(user, riff.ID, "riff-refresh-op")
	if err != nil || !changed || refreshed.Riff.ContextRevision != 2 || refreshed.Riff.ThroughMessageID != later.ID {
		t.Fatalf("refresh changed=%v err=%v binding=%+v", changed, err, refreshed.Riff)
	}
	refreshedAgain, changedAgain, err := app.refreshPrivateRiff(user, riff.ID, "riff-refresh-op")
	if err != nil || changedAgain || refreshedAgain.Riff.ContextRevision != 2 {
		t.Fatalf("refresh replay changed=%v err=%v binding=%+v", changedAgain, err, refreshedAgain.Riff)
	}
}

func TestPrivateRiffAudienceDriftFailsClosedEvenWhenOwnerRetainsAccess(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-audience-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}

	// Narrow the organization-wide channel while preserving AJ's membership.
	// Access alone is insufficient: the checkpoint's audience contract changed.
	source.MemberEmails = []string{user.Email, "tim@shareability.com"}
	source.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(source); err != nil {
		t.Fatalf("save changed audience: %v", err)
	}
	if _, _, err := app.currentPrivateRiffSource(user.Email, riff); err == nil || !strings.Contains(err.Error(), "audience changed") {
		t.Fatalf("audience drift err=%v, want fail-closed audience error", err)
	}
}

func TestPrivateRiffCannotEnterLegacyArtifactFollowUpPath(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-work-fence-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}

	result, err := app.appendScoutChatThreadMessage(context.Background(), user, riff.ID, "Revise that deliverable", nil, "known-artifact-id")
	if err == nil || !strings.Contains(err.Error(), "conversation only") || result != nil {
		t.Fatalf("follow-up result=%v err=%v, want early Riff work fence", result, err)
	}
	unchanged, _, readErr := app.scoutChatThreadByID(user.Email, riff.ID)
	if readErr != nil || len(unchanged.Messages) != 0 {
		t.Fatalf("fenced Riff turn mutated transcript messages=%d err=%v", len(unchanged.Messages), readErr)
	}
}

func TestPrivateRiffRouterCannotPreemptCheckpointAnswer(t *testing.T) {
	for _, decision := range []conversationIntentDecision{
		unavailableConversationDecision("source_missing", "I don't have the checkpoint content.", proposalSourceChatRouter),
		{Outcome: conversationIntentClarifyOnce, Question: "Which tradeoff?", Source: proposalSourceChatRouter},
	} {
		constrained := constrainPrivateRiffDecision(decision)
		if constrained.Outcome != conversationIntentConversationalReply || constrained.Source != proposalSourceDeterministicGuard {
			t.Fatalf("constrained decision=%+v, want checkpoint answer stage", constrained)
		}
	}

	work := conversationIntentDecision{
		Outcome: conversationIntentStartPrivateWork,
		Work:    &conversationWorkDecision{Kind: conversationWorkWorkstream},
		Source:  proposalSourceChatRouter,
	}
	constrained := constrainPrivateRiffDecision(work)
	if constrained.Outcome != conversationIntentUnavailable || constrained.Unavailable == nil || constrained.Unavailable.Code != "private_riff_work_unavailable" {
		t.Fatalf("work decision=%+v, want Riff authority fence", constrained)
	}
}

func TestPrivateRiffSourceDriftFailsClosed(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 3)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-03", "", "riff-drift-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}

	source.Messages[2].Text = "Edited after the checkpoint"
	source.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(source); err != nil {
		t.Fatalf("save edited source: %v", err)
	}
	if _, _, err := app.currentPrivateRiffSource(user.Email, riff); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("source drift err=%v, want fail-closed changed error", err)
	}
	projected := app.projectScoutChatThreadForViewer(user.Email, riff)
	if projected.Riff == nil || projected.Riff.SourceAvailable || projected.Riff.UnavailableReason == "" {
		t.Fatalf("drift projection=%+v", projected.Riff)
	}
}

func TestPrivateRiffPublishesOnlySelectedParagraphsWithPublicProvenance(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-publish-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	answer := scoutChatMessageRecord{
		ID: "riff-answer", Kind: "message", Role: "scout", AuthorName: "Scout",
		Text:      "First public-safe paragraph.\n\nSECOND-SELECTED-PARAGRAPH\n\nPRIVATE-UNSELECTED-PARAGRAPH",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Activity: &scoutChatAnswerActivity{
			Version: privateRiffBindingVersion, Status: "completed", ContextRevision: riff.Riff.ContextRevision,
			SourceThreadID: riff.Riff.SourceThreadID, ThroughMessageID: riff.Riff.ThroughMessageID,
			SourceMessageDigest: riff.Riff.SourceMessageDigest, SourceWindowDigest: riff.Riff.SourceWindowDigest, SourceAudienceDigest: riff.Riff.SourceAudienceDigest,
		},
	}
	riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, answer)
	if err != nil {
		t.Fatalf("commit riff answer: %v", err)
	}
	projectedRiff := app.projectScoutChatThreadForViewer(user.Email, riff)
	projectedAnswer := projectedRiff.Messages[len(projectedRiff.Messages)-1]
	if projectedAnswer.Activity == nil || projectedAnswer.Activity.ContextRevision != 1 || projectedAnswer.Activity.ThroughMessageID != "riff-source-02" ||
		projectedAnswer.Activity.SourceMessageDigest != "" || projectedAnswer.Activity.SourceWindowDigest != "" || projectedAnswer.Activity.SourceAudienceDigest != "" {
		t.Fatalf("unsafe or incomplete answer checkpoint projection=%+v", projectedAnswer.Activity)
	}
	later := scoutChatMessageRecord{
		ID: "riff-source-03", Kind: "message", Role: "user", Text: "A later channel update that was not used by the existing answer.",
		CreatedAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano), AuthorName: "Tim", AuthorEmail: "tim@shareability.com",
	}
	source, err = app.commitScoutChatThreadMessages(user.Email, source.ID, later)
	if err != nil {
		t.Fatalf("append later source: %v", err)
	}
	riff, _, err = app.refreshPrivateRiff(user, riff.ID, "riff-publish-refresh")
	if err != nil || riff.Riff.ContextRevision != 2 {
		t.Fatalf("refresh after answer: binding=%+v err=%v", riff.Riff, err)
	}
	_, _, paragraphs, err := app.privateRiffSharePreview(user, riff.ID, answer.ID)
	if err != nil || len(paragraphs) != 3 {
		t.Fatalf("preview paragraphs=%+v err=%v", paragraphs, err)
	}
	draft, err := app.publishPrivateRiffSelection(user, riff.ID, answer.ID, "riff-draft-op", "draft", []string{paragraphs[0].Token})
	if err != nil || draft["draft"] != "First public-safe paragraph." || draft["mode"] != "draft" {
		t.Fatalf("draft result=%v err=%v", draft, err)
	}
	unchanged, _, err := app.scoutChatThreadByID(user.Email, source.ID)
	if err != nil || len(unchanged.Messages) != len(source.Messages) {
		t.Fatalf("draft mutated public channel messages=%d want=%d err=%v", len(unchanged.Messages), len(source.Messages), err)
	}
	result, err := app.publishPrivateRiffSelection(user, riff.ID, answer.ID, "riff-publish-op", "agent", []string{paragraphs[1].Token})
	if err != nil || result["messageId"] == "" {
		t.Fatalf("publish result=%v err=%v", result, err)
	}
	updated, _, err := app.scoutChatThreadByID(user.Email, source.ID)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	posted := updated.Messages[len(updated.Messages)-1]
	if posted.Text != "SECOND-SELECTED-PARAGRAPH" || strings.Contains(posted.Text, "PRIVATE-UNSELECTED") {
		t.Fatalf("published text=%q", posted.Text)
	}
	if posted.Role != "scout" || posted.Publication == nil || posted.Publication.SharedBy != "AJ" || posted.Via != "private_riff" {
		t.Fatalf("published provenance=%+v role=%q via=%q", posted.Publication, posted.Role, posted.Via)
	}
	publicView := app.projectScoutChatThreadForViewer("tim@shareability.com", updated)
	visible := publicView.Messages[len(publicView.Messages)-1]
	if visible.Publication == nil || visible.Publication.SourceThreadID != source.ID || visible.Publication.SourceThroughMessageID != "riff-source-02" || visible.Publication.RiffThreadID != "" || visible.Publication.SourceMessageID != "" || visible.Publication.OperationID != "" || visible.Publication.SelectionDigest != "" {
		t.Fatalf("public projection leaked private receipt=%+v", visible.Publication)
	}
	replay, err := app.publishPrivateRiffSelection(user, riff.ID, answer.ID, "riff-publish-op", "agent", []string{paragraphs[1].Token})
	if err != nil || replay["replayed"] != true {
		t.Fatalf("publish replay=%v err=%v", replay, err)
	}
	contextSources := []privateRiffMemorySource{{ID: "private-meeting", Kind: "meeting", BodyDigest: sha256Hex([]byte("private")), MetadataDigest: sha256Hex([]byte("private-metadata"))}}
	contextManifest, manifestErr := digestAny(contextSources)
	if manifestErr != nil {
		t.Fatal(manifestErr)
	}
	broaderActivity := completedPrivateRiffActivity(riff)
	broaderActivity.ContextSources = contextSources
	broaderActivity.ContextManifestDigest = contextManifest
	broader := scoutChatMessageRecord{
		ID: "riff-legacy-broader-context", Kind: "message", Role: "scout", AuthorName: "Scout", Text: "Private-context paragraph.",
		CreatedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano), Activity: broaderActivity,
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, riff.ID, broader); err != nil {
		t.Fatalf("commit broader-context answer: %v", err)
	}
	broaderParagraphs, _, err := privateRiffParagraphs(broader)
	if err != nil {
		t.Fatalf("paragraphs: %v", err)
	}
	if _, err := app.publishPrivateRiffSelection(user, riff.ID, broader.ID, "riff-legacy-broader-publish", "agent", []string{broaderParagraphs[0].Token}); err == nil || !strings.Contains(err.Error(), "current Private Riff share flow") {
		t.Fatalf("legacy broader-context publication err=%v", err)
	}
}

func completedPrivateRiffActivity(riff scoutChatThreadRecord) *scoutChatAnswerActivity {
	return &scoutChatAnswerActivity{
		Version: privateRiffBindingVersion, Status: "completed", ContextRevision: riff.Riff.ContextRevision,
		EpisodeID: riff.Riff.ActiveEpisodeID, CheckpointID: riff.Riff.CheckpointID,
		SourceThreadID: riff.Riff.SourceThreadID, ThroughMessageID: riff.Riff.ThroughMessageID,
		SourceMessageDigest: riff.Riff.SourceMessageDigest, SourceWindowDigest: riff.Riff.SourceWindowDigest,
		SourceAudienceDigest: riff.Riff.SourceAudienceDigest,
	}
}

func TestPrivateRiffShareAllPublishesAuthoredRootAndOrderedRepliesAtomically(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-all-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	turns := []scoutChatMessageRecord{
		{ID: "riff-user-root", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email, Text: "Help me sharpen the launch angle.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		{ID: "riff-scout-answer", Kind: "message", Role: "scout", AuthorName: "Scout", Text: "Lead with the proof point, then the ambition.", CreatedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano), Activity: completedPrivateRiffActivity(riff)},
		{ID: "riff-user-followup", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email, Text: "Good. Make the proof point concrete.", CreatedAt: time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339Nano)},
	}
	for _, turn := range turns {
		riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, turn)
		if err != nil {
			t.Fatalf("commit %s: %v", turn.ID, err)
		}
	}

	result, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-all-publish", privateRiffPublicationScopeAll, "")
	if err != nil || !result.OK || result.Replayed || result.PublishedCount != 3 || len(result.MessageIDs) != 3 {
		t.Fatalf("share all result=%+v err=%v", result, err)
	}
	updated, _, err := app.scoutChatThreadByID(user.Email, source.ID)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	posted := updated.Messages[len(updated.Messages)-3:]
	if posted[0].ID != result.RootMessageID || posted[0].ReplyTo != nil || posted[0].Role != "user" || posted[0].AuthorEmail != user.Email {
		t.Fatalf("root=%+v result=%+v", posted[0], result)
	}
	for index := 1; index < len(posted); index++ {
		if posted[index].ReplyTo == nil || posted[index].ReplyTo.MessageID != posted[0].ID {
			t.Fatalf("reply %d missing root topology: %+v", index, posted[index].ReplyTo)
		}
		if posted[index].Role != turns[index].Role || posted[index].AuthorName != turns[index].AuthorName {
			t.Fatalf("reply %d authorship=%q/%q want=%q/%q", index, posted[index].Role, posted[index].AuthorName, turns[index].Role, turns[index].AuthorName)
		}
	}
	publicView := app.projectScoutChatThreadForViewer("tim@shareability.com", updated)
	for _, message := range publicView.Messages[len(publicView.Messages)-3:] {
		if message.Publication == nil || message.Publication.Scope != "all" || message.Publication.RootMessageID != posted[0].ID ||
			message.Publication.RiffThreadID != "" || message.Publication.SourceMessageID != "" || message.Publication.OperationID != "" || message.Publication.SelectionDigest != "" {
			t.Fatalf("unsafe or incomplete public provenance=%+v", message.Publication)
		}
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, riff.ID, scoutChatMessageRecord{
		ID: "riff-later-after-lost-response", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email,
		Text: "This later turn must not widen the frozen publication replay.", CreatedAt: time.Now().UTC().Add(3 * time.Second).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("append after publication: %v", err)
	}
	replay, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-all-publish", privateRiffPublicationScopeAll, "")
	if err != nil || !replay.Replayed || replay.PublishedCount != 0 || strings.Join(replay.MessageIDs, ",") != strings.Join(result.MessageIDs, ",") {
		t.Fatalf("share all replay=%+v err=%v", replay, err)
	}
}

func TestPrivateRiffPreparedPublicationRecoversFrozenBatchAfterLaterTurn(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-prepared-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	root := scoutChatMessageRecord{ID: "riff-prepared-root", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email, Text: "Frozen original.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, root)
	if err != nil {
		t.Fatalf("commit root: %v", err)
	}
	result, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-prepared-publish", privateRiffPublicationScopeAll, "")
	if err != nil {
		t.Fatalf("initial publish: %v", err)
	}
	riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, scoutChatMessageRecord{
		ID: "riff-prepared-later", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email,
		Text: "Later turn must remain private.", CreatedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("append later turn: %v", err)
	}
	if len(riff.Riff.PublicationOperations) != 1 {
		t.Fatalf("operations=%+v", riff.Riff.PublicationOperations)
	}
	riff.Riff.PublicationOperations[0].State = "prepared"
	riff.Riff.PublicationOperations[0].CommittedAt = ""
	if err := app.saveScoutChatThread(riff); err != nil {
		t.Fatalf("restore prepared receipt: %v", err)
	}
	destination, _, err := app.scoutChatThreadByID(user.Email, source.ID)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	kept := destination.Messages[:0]
	for _, message := range destination.Messages {
		if message.ID != result.RootMessageID {
			kept = append(kept, message)
		}
	}
	destination.Messages = kept
	if err := app.saveScoutChatThread(destination); err != nil {
		t.Fatalf("simulate lost destination write: %v", err)
	}
	recovered, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-prepared-publish", privateRiffPublicationScopeAll, "")
	if err != nil || recovered.PublishedCount != 1 || len(recovered.MessageIDs) != 1 || recovered.MessageIDs[0] != result.RootMessageID {
		t.Fatalf("prepared recovery=%+v err=%v", recovered, err)
	}
	destination, _, _ = app.scoutChatThreadByID(user.Email, source.ID)
	posted := destination.Messages[len(destination.Messages)-1]
	if posted.Text != root.Text || strings.Contains(posted.Text, "Later turn") {
		t.Fatalf("prepared recovery widened batch: %+v", posted)
	}
}

func TestPrivateRiffShareReplyPreservesSelectedAuthorAsTopLevelPost(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-reply-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	answer := scoutChatMessageRecord{
		ID: "riff-share-one", Kind: "message", Role: "scout", AuthorName: "Scout", Text: "This is the one reply to publish.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Activity: completedPrivateRiffActivity(riff),
	}
	riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, answer)
	if err != nil {
		t.Fatalf("commit answer: %v", err)
	}
	result, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-reply-publish", privateRiffPublicationScopeReply, answer.ID)
	if err != nil || !result.OK || result.PublishedCount != 1 || result.RootMessageID != result.MessageIDs[0] {
		t.Fatalf("share reply result=%+v err=%v", result, err)
	}
	updated, _, err := app.scoutChatThreadByID(user.Email, source.ID)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	posted := updated.Messages[len(updated.Messages)-1]
	if posted.Text != answer.Text || posted.Role != "scout" || posted.AuthorName != "Scout" || posted.ReplyTo != nil || posted.Publication == nil || posted.Publication.Scope != "reply" {
		t.Fatalf("posted reply=%+v", posted)
	}
}

func TestPrivateRiffConversationPublicationFailsClosedOnScopeAndSourceDrift(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-share-guard-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	message := scoutChatMessageRecord{ID: "riff-guard-user", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email, Text: "Do not publish after source drift.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, message)
	if err != nil {
		t.Fatalf("commit message: %v", err)
	}
	if _, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-invalid-scope", privateRiffPublicationScopeReply, ""); err == nil {
		t.Fatal("reply scope without messageId did not fail")
	}
	source.Messages[1].Text = "Edited source invalidates the frozen checkpoint."
	if err := app.saveScoutChatThread(source); err != nil {
		t.Fatalf("save drifted source: %v", err)
	}
	if _, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-drifted-publish", privateRiffPublicationScopeAll, ""); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("source drift err=%v", err)
	}
}

func TestPrivateRiffPublicationJournalRecoversAndNeverResurrectsDeletedCopy(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-journal-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	message := scoutChatMessageRecord{ID: "riff-journal-message", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email, Text: "Publish me once.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, message)
	if err != nil {
		t.Fatalf("commit message: %v", err)
	}
	result, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-journal-publish", privateRiffPublicationScopeAll, "")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	reloaded, _, err := app.scoutChatThreadByID(user.Email, riff.ID)
	if err != nil || len(reloaded.Riff.PublicationOperations) != 1 || reloaded.Riff.PublicationOperations[0].State != "committed" {
		t.Fatalf("journal=%+v err=%v", reloaded.Riff, err)
	}
	projected := app.projectScoutChatThreadForViewer(user.Email, reloaded)
	if projected.Riff.PublicationOperations != nil || projected.Riff.InitiatingMessageID != "" {
		t.Fatalf("journal leaked in projection=%+v", projected.Riff)
	}
	destination, _, err := app.scoutChatThreadByID(user.Email, source.ID)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	kept := destination.Messages[:0]
	for _, current := range destination.Messages {
		if current.ID != result.RootMessageID {
			kept = append(kept, current)
		}
	}
	destination.Messages = kept
	if err := app.saveScoutChatThread(destination); err != nil {
		t.Fatalf("delete public copy fixture: %v", err)
	}
	if _, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-journal-publish", privateRiffPublicationScopeAll, ""); err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("deleted-copy replay err=%v", err)
	}
	after, _, _ := app.scoutChatThreadByID(user.Email, source.ID)
	if scoutChatMessageIndex(after, result.RootMessageID) >= 0 {
		t.Fatal("deleted public copy was resurrected")
	}
}

func TestPrivateRiffRealtimeBindsExactThreadAndVoiceAsksOnlyTwoShareChoices(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-voice-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	turns := []scoutChatMessageRecord{
		{ID: "riff-voice-user", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email, Text: "Give me a sharper line.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		{ID: "riff-voice-answer", Kind: "message", Role: "scout", AuthorName: "Scout", Text: "Country Golf turns participation into belonging.", CreatedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano), Activity: completedPrivateRiffActivity(riff)},
	}
	for _, turn := range turns {
		riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, turn)
		if err != nil {
			t.Fatalf("commit %s: %v", turn.ID, err)
		}
	}
	bound, err := app.bindPrivateRealtimeVoiceToRiff(user.Email, "riff-voice-session", riff.ID)
	if err != nil || bound.ID != riff.ID || bound.VoiceSession == nil {
		t.Fatalf("bind result=%+v err=%v", bound, err)
	}
	if _, err := app.privateRealtimeVoiceConversation(user.Email, "riff-voice-session", riff.ID); err != nil {
		t.Fatalf("resolve exact Riff voice: %v", err)
	}
	if _, err := app.privateRealtimeVoiceConversation("tim@shareability.com", "riff-voice-session", riff.ID); err == nil {
		t.Fatal("non-owner resolved Riff voice")
	}
	config := app.privateRealtimeVoiceSessionConfigForThread(realtimeModel(), user.Email, bound)
	instructions, _ := config["instructions"].(string)
	if !strings.Contains(instructions, "Country Golf checkpoint detail number 02") || !strings.Contains(instructions, "Speak the durable message") {
		t.Fatalf("Riff Realtime instructions missed bound context: %s", instructions)
	}

	clarify, handled, err := app.handlePrivateRiffVoiceShareIntent(context.Background(), user, bound, "riff-voice-share-ask", "Can you share that to the source channel?")
	if err != nil || !handled || clarify["outcome"] != "clarify_once" {
		t.Fatalf("clarify result=%v handled=%v err=%v", clarify, handled, err)
	}
	choices, _ := clarify["choices"].([]string)
	if len(choices) != 2 || choices[0] != "share_all_to_source" || choices[1] != "share_this_reply_to_source" {
		t.Fatalf("choices=%v", choices)
	}
	shared, handled, err := app.handlePrivateRiffVoiceShareIntent(context.Background(), user, bound, "riff-voice-share-choice", "this reply")
	if err != nil || !handled || !strings.Contains(asString(shared["message"]), "Shared this reply") {
		t.Fatalf("share result=%v handled=%v err=%v", shared, handled, err)
	}
	destination, _, err := app.scoutChatThreadByID(user.Email, source.ID)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	posted := destination.Messages[len(destination.Messages)-1]
	if posted.Text != turns[1].Text || posted.Role != "scout" || posted.ReplyTo != nil {
		t.Fatalf("voice-published reply=%+v", posted)
	}
	for _, message := range destination.Messages {
		if message.Via == privateRiffPublicationControlVia || strings.Contains(message.Text, "share that") {
			t.Fatalf("voice control leaked public: %+v", message)
		}
	}
}

func TestPrivateRiffManualShareAllSupersedesPendingVoiceChoiceAndIncludesLaterTurn(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-manual-after-voice-create")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []scoutChatMessageRecord{
		{ID: "riff-manual-root", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email, Text: "Start the Riff.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		{ID: "riff-manual-answer-a", Kind: "message", Role: "scout", AuthorName: "Scout", Text: "Reply A.", CreatedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano), Activity: completedPrivateRiffActivity(riff)},
	} {
		riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, message)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, handled, err := app.handlePrivateRiffVoiceShareIntent(context.Background(), user, riff, "riff-pending-voice", "Can you share that to the source channel?"); err != nil || !handled {
		t.Fatalf("pending voice choice handled=%v err=%v", handled, err)
	}
	riff, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, scoutChatMessageRecord{
		ID: "riff-manual-later-b", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email,
		Text: "Reply B after the voice question.", CreatedAt: time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.publishPrivateRiffConversation(user, riff.ID, "riff-manual-all", privateRiffPublicationScopeAll, "")
	if err != nil || result.PublishedCount != 3 {
		t.Fatalf("manual share all=%+v err=%v", result, err)
	}
	destination, _, _ := app.scoutChatThreadByID(user.Email, source.ID)
	if destination.Messages[len(destination.Messages)-1].Text != "Reply B after the voice question." {
		t.Fatalf("manual share omitted later turn: %+v", destination.Messages[len(destination.Messages)-3:])
	}
	reloaded, _, _ := app.scoutChatThreadByID(user.Email, riff.ID)
	if reloaded.Riff.PendingShareChoice != nil {
		t.Fatalf("manual share left stale voice choice: %+v", reloaded.Riff.PendingShareChoice)
	}
}

func TestPrivateRiffPublishHTTPAcceptsOnlyClosedTwoScopeContract(t *testing.T) {
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	cookies := loginAs(t, user.Email, "B0NFIRE!")
	source := seedPrivateRiffChannel(t, kanbanApp, user.Email, 2)
	riff, _, err := kanbanApp.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-http-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	riff, err = kanbanApp.commitScoutChatThreadMessages(user.Email, riff.ID, scoutChatMessageRecord{
		ID: "riff-http-root", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: user.Email,
		Text: "Publish this via the closed endpoint.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("commit root: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+riff.ID+"/riff-publish", strings.NewReader(`{"operationId":"riff-http-publish","scope":"all"}`))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result privateRiffPublicationResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || !result.OK || result.Scope != privateRiffPublicationScopeAll || len(result.MessageIDs) != 1 {
		t.Fatalf("publish result=%+v err=%v", result, err)
	}

	bad := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+riff.ID+"/riff-publish", strings.NewReader(`{"operationId":"riff-http-bad","scope":"all","messageId":"riff-http-root","text":"client content forbidden"}`))
	bad.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		bad.AddCookie(cookie)
	}
	badRecorder := httptest.NewRecorder()
	assistantChatThreadHandler(badRecorder, bad)
	if badRecorder.Code != http.StatusBadRequest {
		t.Fatalf("open publication body status=%d body=%s", badRecorder.Code, badRecorder.Body.String())
	}
}

func TestPrivateRiffPublicProjectionRedactsWholeBatchAfterContextRevocation(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	contextSources := []privateRiffMemorySource{{
		Kind: "meeting", ID: "revoked-meeting", BodyDigest: sha256Hex([]byte("private meeting detail")), MetadataDigest: sha256Hex([]byte("metadata")),
	}}
	manifestDigest, err := digestAny(contextSources)
	if err != nil {
		t.Fatalf("context manifest: %v", err)
	}
	publication := func(sourceMessageID string, sources []privateRiffMemorySource) *scoutChatPublicationProvenance {
		return &scoutChatPublicationProvenance{
			Version: privateRiffConversationPublicationVersion, Kind: "private_riff", SourceTitle: "country-golf",
			RootMessageID: "public-riff-root", SourceMessageID: sourceMessageID,
			ContextManifestDigest: func() string {
				if len(sources) == 0 {
					return ""
				}
				return manifestDigest
			}(),
			ContextSources: append([]privateRiffMemorySource(nil), sources...),
		}
	}
	thread := scoutChatThreadRecord{
		ID: "country-golf", Visibility: scoutChatVisibilityPublic,
		Messages: []scoutChatMessageRecord{
			{ID: "public-riff-root", Kind: "message", Role: "user", Text: "Original question", Publication: publication("private-root", nil)},
			{ID: "public-riff-reply", Kind: "message", Role: "scout", Text: "Answer containing private meeting detail", Sources: []answerSource{{Kind: "meeting", Quote: "private meeting detail"}}, Publication: publication("private-reply", contextSources), ReplyTo: &scoutChatReplyRef{MessageID: "public-riff-root", AuthorName: "AJ", AuthorEmail: "aj@shareability.com", Text: "Original question"}},
		},
	}
	for index := 0; index < 24; index++ {
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{
			ID: fmt.Sprintf("public-riff-reply-%02d", index), Kind: "message", Role: "scout", Text: "Another private-context reply",
			Publication: publication(fmt.Sprintf("private-reply-%02d", index), contextSources),
			ReplyTo:     &scoutChatReplyRef{MessageID: "public-riff-root", AuthorName: "AJ", Text: "Original question"},
		})
	}
	if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, "projection-scan-control", "Authorized control context", map[string]string{"visibility": "organization"}); err != nil {
		t.Fatal(err)
	}
	app.memory.mu.Lock()
	sourceEntryCount := len(app.memory.entries)
	app.memory.mu.Unlock()
	visits := 0
	app.memory.authorizationEntryVisitHook = func() { visits++ }
	t.Cleanup(func() { app.memory.authorizationEntryVisitHook = nil })
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if len(projected.Messages) != len(thread.Messages) {
		t.Fatalf("projected messages=%d", len(projected.Messages))
	}
	if visits != sourceEntryCount {
		t.Fatalf("Riff projection scanned recall %d times for %d entries; want one scan of %d", visits, len(thread.Messages), sourceEntryCount)
	}
	for _, message := range projected.Messages {
		if message.IntentOutcome != string(conversationIntentUnavailable) || !strings.Contains(message.Text, "no longer authorized") || len(message.Sources) != 0 {
			t.Fatalf("batch member was not safely redacted: %+v", message)
		}
		if message.Publication == nil || message.Publication.ContextManifestDigest != "" || len(message.Publication.ContextSources) != 0 {
			t.Fatalf("server-only context receipt leaked: %+v", message.Publication)
		}
		if message.ReplyTo != nil && (message.ReplyTo.Text != "" || message.ReplyTo.AuthorName != "" || message.ReplyTo.AuthorEmail != "") {
			t.Fatalf("revoked root preview leaked through reply reference: %+v", message.ReplyTo)
		}
	}
	visits = 0
	liveRoot := app.projectScoutChatMessageForViewer("aj@shareability.com", thread, thread.Messages[0])
	if liveRoot.IntentOutcome != string(conversationIntentUnavailable) || !strings.Contains(liveRoot.Text, "no longer authorized") {
		t.Fatalf("single-message event projection exposed a context-free sibling: %+v", liveRoot)
	}
	if visits != sourceEntryCount {
		t.Fatalf("live Riff event scanned recall %d times; want one scan of %d", visits, sourceEntryCount)
	}
	visits = 0
	ordinary := scoutChatMessageRecord{ID: "ordinary-event", Kind: "message", Role: "user", Text: "Ordinary channel update"}
	thread.Messages = append(thread.Messages, ordinary)
	if projectedOrdinary := app.projectScoutChatMessageForViewer("aj@shareability.com", thread, ordinary); projectedOrdinary.Text != ordinary.Text || visits != 0 {
		t.Fatalf("ordinary event inherited full-thread projection text=%q visits=%d", projectedOrdinary.Text, visits)
	}
}

func TestPrivateRiffVoiceShareIntentFailsClosedForEveryNegatedPublicationVerb(t *testing.T) {
	for _, utterance := range []string{
		"Don't share this reply to the source channel",
		"Do not publish this reply publicly",
		"don't post this reply to the source",
		"never send this reply to the channel",
		"not post this one publicly",
		"Don’t send this one to the source channel",
		"I don't want to post this reply to the source",
		"don't ever share this reply publicly",
		"I can't publish this reply to the channel",
		"you shouldn't send this reply public",
		"refrain from posting this reply to the channel",
	} {
		intent, scope, ambiguous := privateRiffVoiceShareIntent(utterance)
		if intent || scope != "" || ambiguous {
			t.Fatalf("negated utterance %q produced intent=%v scope=%q ambiguous=%v", utterance, intent, scope, ambiguous)
		}
	}
	for _, utterance := range []string{
		"Share this reply to the source channel",
		"Please publish all to the channel",
		"Can you post this reply publicly?",
		"Scout, send everything to the source channel",
	} {
		intent, _, _ := privateRiffVoiceShareIntent(utterance)
		if !intent {
			t.Fatalf("affirmative utterance %q was not recognized", utterance)
		}
	}
}

func TestPrivateRiffRejectsAgentAuthorshipOutsideItsScoutBinding(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, _, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "riff-agent-binding-create")
	if err != nil {
		t.Fatalf("create riff: %v", err)
	}
	_, err = app.commitScoutChatThreadMessages(user.Email, riff.ID, scoutChatMessageRecord{
		ID: "riff-wrong-agent", Kind: "message", Role: "scout", AuthorName: "Mary",
		Text: "This answer must not be attributed to another agent.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Activity: completedPrivateRiffActivity(riff),
	})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wrong agent binding err=%v", err)
	}
}

func TestPublicKeywordFallbackRespectsNegationAndExplicitMode(t *testing.T) {
	for _, text := range []string{
		"@Scout do not start research; just talk with me",
		"@Scout research: do not run anything; just talk with me",
		"@Scout let's discuss the research",
		"@Scout this design is interesting",
	} {
		if mode := scoutChatThreadModeForChannelText(text); mode != "" {
			t.Fatalf("%q routed to %q, want conversation", text, mode)
		}
	}
	if mode := scoutChatThreadModeForChannelText("@Scout research: compare these options"); mode != "research" {
		t.Fatalf("explicit prefix mode=%q, want research", mode)
	}
}
