package main

import (
	"context"
	"fmt"
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
