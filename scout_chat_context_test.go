package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func replyContextTestMessage(id, role, author, text string, replyTo *scoutChatReplyRef) scoutChatMessageRecord {
	return scoutChatMessageRecord{
		ID: id, Kind: "message", Role: role, AuthorName: author, Text: text,
		CreatedAt: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), ReplyTo: replyTo,
	}
}

func TestScoutChatReplyContextProductionRouterAnswerAndProposal(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "reply-context-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "reply-context-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	channel, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "Like A Farmer", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	rootRef := &scoutChatReplyRef{MessageID: "production-root", AuthorName: scoutParticipantName, Text: "Please paste Tom's recommendations."}
	longPaste := strings.Repeat("podcast evidence before the old router cutoff ", 230) + " PRODUCTION_DR_MAY_SENTINEL"
	root := replyContextTestMessage("production-root", "scout", scoutParticipantName, rootRef.Text, nil)
	root.CausedByMessageID = "production-tyler"
	seed := []scoutChatMessageRecord{
		replyContextTestMessage("production-unrelated", "user", "Coworker", "PRODUCTION_UNRELATED_SENTINEL", nil),
		replyContextTestMessage("production-tyler", "user", "Tyler", "Use Tom's recommendations to build the Like A Farmer optimization report.", nil),
		root,
		replyContextTestMessage("production-paste", "user", "AJ", longPaste, rootRef),
		replyContextTestMessage("production-ack", "scout", scoutParticipantName, "I have Dr. May's full source now.", rootRef),
	}
	for index := 0; index < 28; index++ {
		seed = append(seed, replyContextTestMessage("production-chatter-"+string(rune('a'+index)), "user", "AJ", "short threaded follow-up", rootRef))
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(user.Email, channel.ID, seed...); err != nil {
		t.Fatal(err)
	}

	var routerInputs, answerInputs []string
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch request.Workflow {
		case "scout_route":
			routerInputs = append(routerInputs, request.Input)
			if strings.Contains(request.Input, "10-slide") {
				return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
					Outcome: string(conversationIntentApprovalRequired), Route: "workstream", Mode: "research",
					Objective:   "Create the 10-slide Like A Farmer podcast optimization presentation.",
					EffectClass: "expanded_audience", Message: "Approve the presentation work?",
				}), nil
			}
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		case "scout_chat":
			answerInputs = append(answerInputs, request.Input)
			return "Dr. May's recommendations are the source.", nil
		default:
			return "", nil
		}
	})

	if _, err := kanbanApp.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, channel.ID, "What source did I give you?", nil, "", root.ID, ""); err != nil {
		t.Fatal(err)
	}
	if len(routerInputs) != 1 || len(answerInputs) != 1 || !strings.Contains(routerInputs[0], "PRODUCTION_DR_MAY_SENTINEL") || !strings.Contains(answerInputs[0], "PRODUCTION_DR_MAY_SENTINEL") {
		t.Fatalf("production answer path lost pinned source: routers=%d answers=%d router=%s answer=%s", len(routerInputs), len(answerInputs), firstNonBlank(strings.Join(routerInputs, "\n"), "none"), firstNonBlank(strings.Join(answerInputs, "\n"), "none"))
	}
	if routerLeak := strings.Contains(routerInputs[0], "PRODUCTION_UNRELATED_SENTINEL"); routerLeak || strings.Contains(answerInputs[0], "PRODUCTION_UNRELATED_SENTINEL") {
		position := strings.Index(answerInputs[0], "PRODUCTION_UNRELATED_SENTINEL")
		start, end := position-200, position+300
		if start < 0 {
			start = 0
		}
		if end > len(answerInputs[0]) {
			end = len(answerInputs[0])
		}
		t.Fatalf("production reply path leaked unrelated top-level body: router=%t answer=%t context=%q", routerLeak, strings.Contains(answerInputs[0], "PRODUCTION_UNRELATED_SENTINEL"), answerInputs[0][start:end])
	}

	response, err := kanbanApp.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, channel.ID, "Put Dr. May's source into a 10-slide presentation for this channel.", nil, "", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok || proposal == nil || !strings.Contains(proposal.Objective, "PRODUCTION_DR_MAY_SENTINEL") || !strings.Contains(proposal.Objective, "Tyler") {
		t.Fatalf("production proposal lost resolved source: %#v", response["proposal"])
	}
	if len(routerInputs) != 2 || !strings.Contains(routerInputs[1], "PRODUCTION_DR_MAY_SENTINEL") {
		t.Fatalf("production work router lost pinned source: %#v", routerInputs)
	}
}

func TestScoutChatReplyRecallScopeKeepsOtherAuthorizedChannels(t *testing.T) {
	ctx := withAssistantConversationRecallScope(context.Background(), "like-a-farmer", map[string]bool{"branch-source": true})
	entries := []meetingMemoryEntry{
		{ID: "allowed-branch", Kind: memoryContextKindCompanyConversation, Metadata: map[string]string{"threadId": "like-a-farmer", "messageId": "branch-source"}},
		{ID: "blocked-sibling-transcript", Kind: meetingMemoryKindTranscript, Metadata: map[string]string{"threadId": "like-a-farmer", "messageId": "other-root"}},
		{ID: "allowed-other-channel", Kind: memoryContextKindCompanyConversation, Metadata: map[string]string{"threadId": "company-strategy", "messageId": "relevant-company-source"}},
	}
	filtered := filterAssistantConversationRecallEntries(ctx, entries)
	if len(filtered) != 2 || filtered[0].ID != "allowed-branch" || filtered[1].ID != "allowed-other-channel" {
		t.Fatalf("reply recall scope filtered the wrong sources: %#v", filtered)
	}
}

func TestScoutChatReplyContextPreservesCausalBranchAndLongPaste(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	rootRef := &scoutChatReplyRef{MessageID: "scout-root", AuthorName: "Scout", Text: "Please paste the source."}
	otherRef := &scoutChatReplyRef{MessageID: "other-root", AuthorName: "Scout", Text: "Other discussion."}
	sentinel := "DR_MAY_SENTINEL " + strings.Repeat("podcast optimization evidence ", 220)
	thread := scoutChatThreadRecord{ID: "like-a-farmer", Title: "Like A Farmer", Visibility: scoutChatVisibilityPublic}
	thread.Messages = append(thread.Messages,
		replyContextTestMessage("dr-may", "user", "Dr. May", "Original recommendations", nil),
		replyContextTestMessage("prior-unrelated", "user", "Coworker", "PRIOR_TOP_LEVEL_SENTINEL", nil),
		replyContextTestMessage("tyler-ask", "user", "Tyler", "Can you make Tom's recommendations into a presentation?", nil),
	)
	root := replyContextTestMessage("scout-root", "scout", "Scout", "Please paste Tom's recommendations.", nil)
	root.CausedByMessageID = "tyler-ask"
	ack := replyContextTestMessage("branch-ack", "scout", "Scout", "I have the source now.", rootRef)
	ack.Sources = []answerSource{{MessageID: "other-secret", Quote: "CROSS_BRANCH_QUOTE_SENTINEL"}}
	thread.Messages = append(thread.Messages, root)
	thread.Messages = append(thread.Messages,
		replyContextTestMessage("branch-correction", "user", "AJ", "He's talking about Dr. May.", rootRef),
		replyContextTestMessage("branch-paste", "user", "AJ", sentinel, rootRef),
		ack,
		replyContextTestMessage("other-root", "scout", "Scout", "Unrelated reply root.", nil),
		replyContextTestMessage("other-secret", "user", "Coworker", "CROSS_BRANCH_SENTINEL", otherRef),
		replyContextTestMessage("later-long-one", "user", "AJ", strings.Repeat("later competing analysis one ", 260), rootRef),
		replyContextTestMessage("later-long-two", "user", "AJ", strings.Repeat("later competing analysis two ", 260), rootRef),
	)
	for index := 0; index < 30; index++ {
		thread.Messages = append(thread.Messages, replyContextTestMessage("branch-chatter-"+string(rune('a'+index)), "user", "AJ", "short branch follow-up", rootRef))
	}
	for index := 0; index < 20; index++ {
		thread.Messages = append(thread.Messages, replyContextTestMessage("noise-"+string(rune('a'+index)), "user", "Coworker", "unrelated main-feed traffic", nil))
	}
	current := replyContextTestMessage("current", "user", "AJ", "Use Dr. May's full recommendation pasted above.", rootRef)
	context := app.scoutChatTurnContextForViewer("aj@shareability.com", thread, current)

	if context.ReplyRootID != "scout-root" {
		t.Fatalf("reply root=%q, want scout-root", context.ReplyRootID)
	}
	routerInput := scoutRouterInput(current.Text, context.History)
	if !strings.Contains(routerInput, "DR_MAY_SENTINEL") || !strings.Contains(routerInput, "Tyler") || !strings.Contains(routerInput, "Please paste Tom's recommendations") {
		t.Fatalf("router input lost causal branch or long paste: %s", routerInput)
	}
	if strings.Contains(routerInput, "CROSS_BRANCH_SENTINEL") || strings.Contains(routerInput, "CROSS_BRANCH_QUOTE_SENTINEL") || strings.Contains(routerInput, "PRIOR_TOP_LEVEL_SENTINEL") || strings.Contains(routerInput, "noise-") {
		t.Fatalf("router input leaked unrelated channel/reply content: %s", routerInput)
	}
	if !strings.Contains(context.RecallQuery, "Dr. May") || !strings.Contains(context.RecallQuery, "DR_MAY_SENTINEL") {
		t.Fatalf("semantic recall query lost resolved people/source text: %s", context.RecallQuery)
	}
	decision := conversationIntentDecision{Outcome: conversationIntentApprovalRequired, Approval: &conversationApprovalDecision{
		EffectClass: "expanded_audience", Summary: "Approve the deck work.",
		Work: &conversationWorkDecision{Kind: conversationWorkWorkstream, Mode: "research", Objective: "Create the requested presentation."},
	}}
	resolved := bindScoutReplyContextToWork(decision, context.WorkContext, context.SourceComplete)
	if resolved.Approval == nil || resolved.Approval.Work == nil || !strings.Contains(resolved.Approval.Work.Objective, "DR_MAY_SENTINEL") || !strings.Contains(resolved.Approval.Work.Objective, "Tyler") {
		t.Fatalf("approved work lost reply-thread source material: %+v", resolved.Approval)
	}
}

func TestScoutChatReplyContextScrubsDeletedParentSnapshots(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	stale := &scoutChatReplyRef{MessageID: "deleted-parent", AuthorName: "Former coworker", Text: "DELETED_PARENT_SECRET"}
	thread := scoutChatThreadRecord{ID: "deleted-parent-thread", Visibility: scoutChatVisibilityPublic, Messages: []scoutChatMessageRecord{
		replyContextTestMessage("surviving-child", "user", "AJ", "The surviving reply body is allowed.", stale),
	}}
	current := replyContextTestMessage("current", "user", "AJ", "continue", &scoutChatReplyRef{MessageID: "surviving-child", AuthorName: "AJ", Text: "The surviving reply body is allowed."})
	context := app.scoutChatTurnContextForViewer("aj@shareability.com", thread, current)
	combined := scoutRouterInput(current.Text, context.History) + context.RecallQuery + context.WorkContext
	if strings.Contains(combined, "DELETED_PARENT_SECRET") {
		t.Fatalf("deleted parent snapshot leaked into model context: %s", combined)
	}
}

func TestScoutChatRootlessContextScrubsDeletedParentSnapshots(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	stale := &scoutChatReplyRef{MessageID: "deleted-parent", AuthorName: "Former coworker", Text: "ROOTLESS_DELETED_PARENT_SECRET"}
	thread := scoutChatThreadRecord{ID: "rootless-deleted-parent", Visibility: scoutChatVisibilityPublic, Messages: []scoutChatMessageRecord{
		replyContextTestMessage("surviving-child", "user", "AJ", "Allowed surviving body.", stale),
	}}
	current := replyContextTestMessage("current", "user", "AJ", "@Scout continue", nil)
	context := app.scoutChatTurnContextForViewer("aj@shareability.com", thread, current)
	if combined := scoutRouterInput(current.Text, context.History) + context.RecallQuery; strings.Contains(combined, "ROOTLESS_DELETED_PARENT_SECRET") {
		t.Fatalf("rootless context leaked deleted parent snapshot: %s", combined)
	}
}

func TestScoutChatOversizedReplySourceFailsClosedForWork(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	rootRef := &scoutChatReplyRef{MessageID: "ambiguous-root", AuthorName: "Scout", Text: "Paste the source."}
	thread := scoutChatThreadRecord{ID: "ambiguous-source", Visibility: scoutChatVisibilityPublic, Messages: []scoutChatMessageRecord{
		replyContextTestMessage("ambiguous-root", "scout", "Scout", "Paste the source.", nil),
	}}
	for index := 0; index < 5; index++ {
		thread.Messages = append(thread.Messages, replyContextTestMessage("substantive-"+string(rune('a'+index)), "user", "AJ", strings.Repeat("substantive source candidate ", 24), rootRef))
	}
	current := replyContextTestMessage("current", "user", "AJ", "build the report from the source above", rootRef)
	turnContext := app.scoutChatTurnContextForViewer("aj@shareability.com", thread, current)
	if turnContext.SourceComplete {
		t.Fatal("ambiguous bounded source selection incorrectly reported complete")
	}
	decision := conversationIntentDecision{Outcome: conversationIntentStartPrivateWork, Work: &conversationWorkDecision{
		Kind: conversationWorkWorkstream, Mode: "research", Objective: "Build the deck",
	}}
	resolved := bindScoutReplyContextToWork(decision, turnContext.WorkContext, turnContext.SourceComplete)
	if resolved.Outcome != conversationIntentUnavailable || resolved.Unavailable == nil || resolved.Unavailable.Code != "reply_source_too_large" {
		t.Fatalf("oversized source did not fail closed: %+v", resolved)
	}
}

func TestScoutChatClarificationBudgetIsReplyRootScoped(t *testing.T) {
	rootA := &scoutChatReplyRef{MessageID: "root-a"}
	rootB := &scoutChatReplyRef{MessageID: "root-b"}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{
		replyContextTestMessage("root-a", "scout", "Scout", "A", nil),
		replyContextTestMessage("clarify-a", "scout", "Scout", "Which source?", rootA),
		replyContextTestMessage("root-b", "scout", "Scout", "B", nil),
		replyContextTestMessage("human-b", "user", "AJ", "Unrelated answer", rootB),
	}}
	thread.Messages[1].Kind = scoutChatMessageKindChoices
	thread.Messages[1].Choices = &scoutChatChoices{Question: "Which source?", Options: []scoutChatChoiceOption{{ID: "one", Label: "One"}, {ID: "two", Label: "Two"}}}
	if !scoutChatClarificationAlreadyAsked(thread, "root-a") {
		t.Fatal("root A lost its already-asked clarification after activity in root B")
	}
	if scoutChatClarificationAlreadyAsked(thread, "root-b") {
		t.Fatal("root B inherited root A's clarification budget")
	}
}
