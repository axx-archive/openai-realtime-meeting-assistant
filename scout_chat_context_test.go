package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func replyContextTestMessage(id, role, author, text string, replyTo *scoutChatReplyRef) scoutChatMessageRecord {
	return scoutChatMessageRecord{
		ID: id, Kind: "message", Role: role, AuthorName: author, Text: text,
		CreatedAt: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), ReplyTo: replyTo,
	}
}

func TestScoutChatTopLevelExplicitCollaboratorSourceSurvivesOldChannelHistory(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	channel, err := app.createScoutChatThread(user.Email, user.Name, "Like A Farmer", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	drMay := replyContextTestMessage("named-dr-may-source", "user", "Dr. May", strings.Repeat("Like A Farmer recommendation evidence ", 80)+"NAMED_DR_MAY_SOURCE_SENTINEL", nil)
	drMay.AuthorEmail = "dr.may@example.com"
	unrelated := replyContextTestMessage("named-dr-may-unrelated", "user", "Dr. May", strings.Repeat("automotive campaign launch ", 80)+"UNRELATED_DR_MAY_SENTINEL", nil)
	unrelated.AuthorEmail = drMay.AuthorEmail
	siblingRoot := replyContextTestMessage("named-source-sibling-root", "user", "Coworker", "Unrelated campaign thread", nil)
	sibling := replyContextTestMessage("named-source-sibling", "user", "Dr. May", strings.Repeat("Like A Farmer secret sibling recommendation ", 80)+"SIBLING_DR_MAY_SECRET", &scoutChatReplyRef{MessageID: siblingRoot.ID})
	sibling.AuthorEmail = drMay.AuthorEmail
	seed := []scoutChatMessageRecord{drMay, unrelated, siblingRoot, sibling}
	for index := 0; index < agentThreadSourceConversationWindow+4; index++ {
		seed = append(seed, replyContextTestMessage(fmt.Sprintf("named-source-chatter-%02d", index), "user", "Coworker", "unrelated channel chatter", nil))
	}
	recentSiblingRoot := replyContextTestMessage("named-source-recent-sibling-root", "user", "Coworker", "Recent unrelated campaign thread", nil)
	recentSibling := replyContextTestMessage("named-source-recent-sibling", "user", "Dr. May", strings.Repeat("Like A Farmer recent sibling secret ", 40)+"RECENT_SIBLING_DR_MAY_SECRET", &scoutChatReplyRef{MessageID: recentSiblingRoot.ID})
	recentSibling.AuthorEmail = drMay.AuthorEmail
	seed = append(seed, recentSiblingRoot, recentSibling)
	if _, err := app.commitScoutChatThreadMessages(user.Email, channel.ID, seed...); err != nil {
		t.Fatal(err)
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := replyContextTestMessage("named-source-request", "user", "AJ", "Build the Like A Farmer deck using Dr. May’s full recommendations in this channel.", nil)
	turn := app.scoutChatTurnContextForViewer(user.Email, thread, request)
	if !turn.SourceComplete || !strings.Contains(turn.WorkContext, "NAMED_DR_MAY_SOURCE_SENTINEL") || !strings.Contains(turn.RecallQuery, "NAMED_DR_MAY_SOURCE_SENTINEL") {
		t.Fatalf("top-level named collaborator source was not pinned: complete=%t work=%q recall=%q", turn.SourceComplete, turn.WorkContext, turn.RecallQuery)
	}
	for _, forbidden := range []string{"UNRELATED_DR_MAY_SENTINEL", "SIBLING_DR_MAY_SECRET", "RECENT_SIBLING_DR_MAY_SECRET"} {
		if strings.Contains(turn.WorkContext, forbidden) || strings.Contains(turn.RecallQuery, forbidden) {
			t.Fatalf("named collaborator selection leaked %q: work=%q recall=%q", forbidden, turn.WorkContext, turn.RecallQuery)
		}
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, channel.ID, request); err != nil {
		t.Fatal(err)
	}
	thread, _, err = app.scoutChatThreadByID(user.Email, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	window, binding, err := scoutChatSourceWindow(thread, request.ID)
	if err != nil || !isHexDigest(binding.WindowDigest) {
		t.Fatalf("named source window failed: binding=%+v err=%v", binding, err)
	}
	found := false
	for _, message := range window {
		found = found || message.ID == drMay.ID && strings.Contains(message.Text, "NAMED_DR_MAY_SOURCE_SENTINEL")
	}
	if !found {
		t.Fatalf("old named collaborator source missing from bound window: %#v", window)
	}
	for _, message := range window {
		if strings.Contains(message.Text, "UNRELATED_DR_MAY_SENTINEL") || strings.Contains(message.Text, "SIBLING_DR_MAY_SECRET") || strings.Contains(message.Text, "RECENT_SIBLING_DR_MAY_SECRET") {
			t.Fatalf("bound source window leaked unrelated/sibling named-author content: %+v", message)
		}
	}
	selection, err := app.goalRouteSourceSelection(goalRouteReceipt{Requester: user.Email, OriginID: channel.ID, SourceMessageID: request.ID})
	if err != nil || !strings.Contains(selection.Context, "NAMED_DR_MAY_SOURCE_SENTINEL") {
		t.Fatalf("final provider source selection lost the named source: selection=%+v err=%v", selection, err)
	}
	for _, forbidden := range []string{"SIBLING_DR_MAY_SECRET", "RECENT_SIBLING_DR_MAY_SECRET"} {
		if strings.Contains(selection.Context, forbidden) {
			t.Fatalf("final provider source selection leaked sibling content %q: %s", forbidden, selection.Context)
		}
	}
	originalWindowDigest := binding.WindowDigest
	edited := thread
	edited.Messages = append([]scoutChatMessageRecord(nil), thread.Messages...)
	edited.Messages[scoutChatMessageIndex(edited, drMay.ID)].Text = "edited after approval"
	if _, changed, err := scoutChatSourceWindow(edited, request.ID); err == nil && changed.WindowDigest == originalWindowDigest {
		t.Fatalf("named-source edit did not invalidate the authenticated source window: changed=%+v err=%v", changed, err)
	}
	deleted := thread
	deleted.Messages = append([]scoutChatMessageRecord(nil), thread.Messages...)
	deletedIndex := scoutChatMessageIndex(deleted, drMay.ID)
	deleted.Messages = append(deleted.Messages[:deletedIndex], deleted.Messages[deletedIndex+1:]...)
	if _, changed, err := scoutChatSourceWindow(deleted, request.ID); err == nil && changed.WindowDigest == originalWindowDigest {
		t.Fatalf("named-source deletion did not invalidate the authenticated source window: changed=%+v err=%v", changed, err)
	}
}

func TestScoutChatNamedCollaboratorAskIsNotLatestChatterAndAmbiguityFailsClosed(t *testing.T) {
	tylersAsk := replyContextTestMessage("tyler-real-ask", "user", "Tyler", "@Scout can you put Tom's recommendations into a presentation?", nil)
	tylersAsk.AuthorEmail = "tyler@example.com"
	laterChatter := replyContextTestMessage("tyler-later-chatter", "user", "Tyler", "I'm doing a deeper dive; hang tight.", nil)
	laterChatter.AuthorEmail = tylersAsk.AuthorEmail
	request := replyContextTestMessage("use-tyler-ask", "user", "AJ", "Use Tyler's ask.", nil)
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{tylersAsk, laterChatter}}
	selected, complete := scoutChatExplicitNamedAuthorSources(thread, request)
	if !complete || len(selected) != 1 || selected[0].ID != tylersAsk.ID {
		t.Fatalf("Tyler's ask resolved to latest chatter or failed: complete=%t selected=%+v", complete, selected)
	}
	secondAsk := replyContextTestMessage("tyler-second-ask", "user", "Tyler", "@Scout can you prepare another presentation?", nil)
	secondAsk.AuthorEmail = tylersAsk.AuthorEmail
	thread.Messages = append(thread.Messages, secondAsk)
	if selected, complete := scoutChatExplicitNamedAuthorSources(thread, request); complete || len(selected) != 0 {
		t.Fatalf("two equally plausible Tyler asks did not fail closed: complete=%t selected=%+v", complete, selected)
	}

	firstDrMay := replyContextTestMessage("duplicate-dr-may-a", "user", "Dr. May", strings.Repeat("Like A Farmer recommendation ", 40), nil)
	firstDrMay.AuthorEmail = "dr.may.one@example.com"
	secondDrMay := replyContextTestMessage("duplicate-dr-may-b", "user", "Dr. May", strings.Repeat("Like A Farmer recommendation ", 40), nil)
	secondDrMay.AuthorEmail = "dr.may.two@example.com"
	request = replyContextTestMessage("duplicate-dr-may-request", "user", "AJ", "Use Dr. May's full Like A Farmer recommendations.", nil)
	thread.Messages = []scoutChatMessageRecord{firstDrMay, secondDrMay}
	if selected, complete := scoutChatExplicitNamedAuthorSources(thread, request); complete || len(selected) != 0 {
		t.Fatalf("duplicate display identities did not fail closed: complete=%t selected=%+v", complete, selected)
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
	pdfText := "PRODUCTION_PDF_SENTINEL 6.1M total audience, 3.7M Instagram, 2.6M YouTube, 2.3M TikTok, and 2.8M monthly podcast listeners."
	pdfRef, err := putBlob([]byte("%PDF-1.7 production source"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	grantedPDF := reserveTestAttachment(t, kanbanApp, user, channel, scoutChatFileAttachment{Ref: pdfRef, Name: "Like_A_Farmer_Audience_Growth_Media_Strategy.pdf", Kind: "pdf"}, "production-pdf-reservation")
	grantedPDF.Text = pdfText
	siblingRef, err := putBlob([]byte("%PDF-1.7 sibling secret"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	grantedSibling := reserveTestAttachment(t, kanbanApp, user, channel, scoutChatFileAttachment{Ref: siblingRef, Name: "Sibling_Strategy.pdf", Kind: "pdf"}, "production-sibling-reservation")
	grantedSibling.Text = "PRODUCTION_SIBLING_PDF_SECRET"
	root := replyContextTestMessage("production-root", "scout", scoutParticipantName, rootRef.Text, nil)
	root.CausedByMessageID = "production-tyler"
	paste := replyContextTestMessage("production-paste", "user", "AJ", longPaste, rootRef)
	mainChannelPDF := replyContextTestMessage("production-main-pdf", "user", "Tyler", "The source PDF is attached in the main channel.", nil)
	mainChannelPDF.Files = []scoutChatFileAttachment{grantedPDF}
	mainChannelPDF.attachmentReservationID = "production-pdf-reservation"
	siblingRoot := replyContextTestMessage("production-sibling-root", "scout", scoutParticipantName, "Unrelated campaign work.", nil)
	sibling := replyContextTestMessage("production-sibling", "user", "Coworker", "Use the private sibling plan.", &scoutChatReplyRef{MessageID: siblingRoot.ID})
	sibling.Files = []scoutChatFileAttachment{grantedSibling}
	sibling.attachmentReservationID = "production-sibling-reservation"
	seed := []scoutChatMessageRecord{
		replyContextTestMessage("production-unrelated", "user", "Coworker", "PRODUCTION_UNRELATED_SENTINEL", nil),
		replyContextTestMessage("production-tyler", "user", "Tyler", "Use Tom's recommendations to build the Like A Farmer optimization report.", nil),
		mainChannelPDF,
		root,
		paste,
		replyContextTestMessage("production-ack", "scout", scoutParticipantName, "I have Dr. May's full source now.", rootRef),
		siblingRoot,
		sibling,
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

	response, err := kanbanApp.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, channel.ID, "Review Like_A_Farmer_Audience_Growth_Media_Strategy.pdf and put the source into a 10-slide presentation for this channel.", nil, "", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok || proposal == nil || proposal.ToolID != packagingStudioProcessID ||
		!strings.Contains(proposal.Objective, "PRODUCTION_DR_MAY_SENTINEL") || !strings.Contains(proposal.Objective, "Tyler") ||
		!strings.Contains(proposal.Query, "PRODUCTION_DR_MAY_SENTINEL") || !strings.Contains(proposal.Query, "Tyler") {
		t.Fatalf("production proposal lost resolved source: %#v", response["proposal"])
	}
	if len(routerInputs) != 1 {
		t.Fatalf("deterministic presentation route unexpectedly called the model router: %#v", routerInputs)
	}
	if refs := decodeAssistantContextRefs(proposal.ContextRefs); len(refs) != 1 || !strings.Contains(refs[0], "production-main-pdf") {
		t.Fatalf("proposal context refs=%#v, want the exact explicitly named main-channel PDF", refs)
	}

	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	proposalMessage := response["answer"].(scoutChatMessageRecord)
	accepted, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, channel.ID, scoutChatProposalAction{Action: "accepted", MessageID: proposalMessage.ID})
	if err != nil {
		t.Fatalf("accept production proposal: %v", err)
	}
	run := accepted["agentThread"].(scoutAgentThread)
	plan := mustGoalPlan(t, kanbanApp, run.Artifact.ID)
	if plan.ContextRefs != proposal.ContextRefs || run.Artifact.Metadata["contextRefs"] != proposal.ContextRefs ||
		plan.RouteReceipt == nil || plan.RouteReceipt.ContextRefsDigest != goalContextRefsDigest(proposal.ContextRefs) {
		t.Fatalf("accepted goal did not durably bind exact context refs: plan=%q artifact=%q receipt=%+v", plan.ContextRefs, run.Artifact.Metadata["contextRefs"], plan.RouteReceipt)
	}
	boundSelection, err := kanbanApp.goalRouteSourceSelection(*plan.RouteReceipt)
	if err != nil || boundSelection.Digest != plan.RouteReceipt.SourceSelectionDigest || !strings.Contains(strings.Join(boundSelection.FileProofs, "\n"), grantedPDF.SourceRevision) {
		t.Fatalf("receipt source manifest did not bind the exact named PDF revision: err=%v receipt=%q selection=%+v", err, plan.RouteReceipt.SourceSelectionDigest, boundSelection)
	}
	engine := newGoalEngine(kanbanApp)
	if err := engine.prepareGoalRoute(&plan, run.Artifact.ID); err != nil {
		t.Fatalf("prepare accepted route: %v", err)
	}
	if err := instantiateProcessPlan(packagingStudioDefinition(), &plan); err != nil {
		t.Fatalf("instantiate accepted Studio plan: %v", err)
	}
	contextStage := packagingStudioStage(t, packagingStudioDefinition(), "context_snapshot")
	contextTask, taskErr := engine.processStageTaskAuthorized(context.Background(), &plan, "", plan.subtaskByID("context_snapshot"), contextStage)
	if taskErr != nil {
		t.Fatalf("authorized context task: %v", taskErr)
	}
	for _, want := range []string{"Tyler", "PRODUCTION_DR_MAY_SENTINEL", "PRODUCTION_PDF_SENTINEL", grantedPDF.SourceRevision, plan.RouteReceipt.SourceMessageDigest, plan.RouteReceipt.SourceWindowDigest, plan.RouteReceipt.SourceSelectionDigest} {
		if !strings.Contains(contextTask, want) {
			t.Fatalf("context stage lost %q from authorized source packet:\n%s", want, contextTask)
		}
	}
	for _, forbidden := range []string{"PRODUCTION_UNRELATED_SENTINEL", "PRODUCTION_SIBLING_PDF_SECRET"} {
		if strings.Contains(contextTask, forbidden) {
			t.Fatalf("context stage leaked %q across a channel/reply boundary", forbidden)
		}
	}

	// Execution does not repeatedly inject the raw source packet into every
	// stage. The context snapshot admits exact claims once; downstream stages
	// receive only declared, durable artifacts. Seed the model's valid structured
	// result and the real deterministic evidence compiler to exercise that path.
	authority, authorityErr := processInternalAuthoritySources(kanbanApp, &plan)
	if authorityErr != nil {
		t.Fatalf("resolve internal source authority: %v", authorityErr)
	}
	pdfSourceRef := ""
	for ref, source := range authority {
		if strings.Contains(source.Text, "PRODUCTION_PDF_SENTINEL") {
			pdfSourceRef = ref
			break
		}
	}
	if pdfSourceRef == "" {
		t.Fatal("authorized source map did not contain the exact named PDF")
	}
	contextBody, err := json.Marshal(map[string]any{
		"direct_ask": "Tyler requested a 10-slide presentation using PRODUCTION_DR_MAY_SENTINEL and the named PDF.",
		"audience":   "Like A Farmer team", "decision": "Choose the optimization plan", "desired_response": "Approve the plan", "slide_count": 10,
		"context_used": []string{"Tyler", "PRODUCTION_DR_MAY_SENTINEL"}, "settled_decisions": []string{}, "taste_signals": []string{}, "brand_assets": []string{},
		"research_mode": "internal", "research_questions": []string{}, "reversible_inferences": []string{}, "uncertain_claims": []string{},
		"known_facts": []map[string]string{{"claim": pdfText, "display_claim": pdfText, "exact_quote": pdfText, "source_ref": pdfSourceRef}},
	})
	if err != nil {
		t.Fatal(err)
	}
	contextArtifact, _, err := kanbanApp.createOSArtifactWithMetadata("workflow", "Production context snapshot", string(contextBody), scoutParticipantName, map[string]string{
		"goalParentId": run.Artifact.ID, "goalSubtaskId": "context_snapshot", "processId": plan.ProcessID, "processStage": "context_snapshot", "processRole": processRoleSynthesizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	contextSubtask := plan.subtaskByID("context_snapshot")
	contextSubtask.Status, contextSubtask.ArtifactID = subtaskComplete, contextArtifact.ID
	evidenceStage := packagingStudioStage(t, packagingStudioDefinition(), "evidence")
	evidenceBody, evidenceMetadata, err := compileProcessEvidenceDossier(kanbanApp, &plan, run.Artifact.ID, evidenceStage)
	if err != nil {
		t.Fatalf("compile source-bound evidence: %v", err)
	}
	evidenceMetadata["goalParentId"], evidenceMetadata["goalSubtaskId"] = run.Artifact.ID, "evidence"
	evidenceMetadata["processId"], evidenceMetadata["processStage"], evidenceMetadata["processRole"] = plan.ProcessID, "evidence", processRoleCompile
	evidenceMetadata["status"], evidenceMetadata["threadStatus"] = "complete", "complete"
	evidenceArtifact, _, err := kanbanApp.createOSArtifactWithMetadata("workflow", "Production evidence dossier", evidenceBody, scoutParticipantName, evidenceMetadata)
	if err != nil {
		t.Fatal(err)
	}
	evidenceSubtask := plan.subtaskByID("evidence")
	evidenceSubtask.Status, evidenceSubtask.ArtifactID = subtaskComplete, evidenceArtifact.ID
	var providerMu sync.Mutex
	var redTeamProviderInputs []string
	internalClaimID := sha256Hex([]byte(pdfSourceRef + "\x00" + pdfText))
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerMu.Lock()
		redTeamProviderInputs = append(redTeamProviderInputs, request.Input)
		providerMu.Unlock()
		output, _ := json.Marshal(map[string]any{"story": map[string]any{
			"headline": pdfText, "claim_ids": []string{internalClaimID}, "claim_renderings": []string{pdfText},
		}})
		return string(output), nil
	})
	redTeamProviderStage := plan.subtaskByID("story_architects")
	redTeamProviderStage.Status = subtaskRunning
	engine.runProcessPanelStage(context.Background(), &plan, run.Artifact.ID, redTeamProviderStage, packagingStudioStage(t, packagingStudioDefinition(), "story_architects"))
	if redTeamProviderStage.Status != subtaskComplete || len(redTeamProviderInputs) < 2 {
		reason := ""
		if redTeamProviderStage.Review != nil {
			reason = redTeamProviderStage.Review.Reasons
		}
		t.Fatalf("production Red-team provider stage did not execute: reason=%q stage=%+v calls=%d", reason, redTeamProviderStage, len(redTeamProviderInputs))
	}
	for index, input := range redTeamProviderInputs {
		for _, want := range []string{"Tyler", "PRODUCTION_DR_MAY_SENTINEL", "PRODUCTION_PDF_SENTINEL", pdfSourceRef} {
			if !strings.Contains(input, want) {
				t.Fatalf("Red-team provider call %d lost %q:\n%s", index, want, input)
			}
		}
		if strings.Contains(input, "PRODUCTION_UNRELATED_SENTINEL") || strings.Contains(input, "PRODUCTION_SIBLING_PDF_SECRET") {
			t.Fatalf("Red-team provider call %d leaked another branch:\n%s", index, input)
		}
	}
	// A proposal persisted by the pre-topology exact-name resolver could carry
	// a guessed chatfile ref from an unrelated reply root. Route admission must
	// reject that old accepted receipt before any provider call, even though the
	// file itself is still authorized in the same channel.
	preFixThread, _, err := kanbanApp.scoutChatThreadByID(user.Email, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	preFixMutated := preFixThread
	preFixMutated.Messages = append([]scoutChatMessageRecord(nil), preFixThread.Messages...)
	approvedIndex := scoutChatMessageIndex(preFixMutated, proposalMessage.ID)
	preFixProposal := *preFixMutated.Messages[approvedIndex].Proposal
	siblingContextRef := scoutChatFileContextRef(channel.ID, sibling.ID, 0)
	preFixProposal.ContextRefs = encodeAssistantContextRefs([]string{siblingContextRef})
	preFixMutated.Messages[approvedIndex].Proposal = &preFixProposal
	if err := kanbanApp.saveScoutChatThread(preFixMutated); err != nil {
		t.Fatal(err)
	}
	preFixPlan := plan
	preFixPlan.ContextRefs = preFixProposal.ContextRefs
	preFixReceipt := *plan.RouteReceipt
	preFixOperation, operationErr := conversationApprovedWorkOperation(channel.ID, user.Email, proposalMessage.ID, preFixProposal)
	if operationErr != nil {
		t.Fatal(operationErr)
	}
	preFixReceipt.ContextRefsDigest = goalContextRefsDigest(preFixPlan.ContextRefs)
	preFixReceipt.OperationID = preFixOperation.ID
	preFixReceipt.OperationBodyDigest = preFixOperation.BodyDigest
	preFixReceipt.SourceSelectionDigest = strings.Repeat("a", 64) // opaque digest minted by the pre-topology implementation
	preFixReceipt.Digest, err = preFixReceipt.contractDigest()
	if err != nil {
		t.Fatal(err)
	}
	preFixPlan.RouteReceipt = &preFixReceipt
	preFixPlan.routeVerified = true
	_, preFixErr := engine.processStageTaskAuthorized(context.Background(), &preFixPlan, "", preFixPlan.subtaskByID("context_snapshot"), contextStage)
	if preFixErr == nil || !strings.Contains(preFixErr.Error(), "outside the source topology") || strings.Contains(preFixErr.Error(), "PRODUCTION_SIBLING_PDF_SECRET") {
		t.Fatalf("pre-fix sibling receipt passed context admission or leaked source: %v", preFixErr)
	}
	if err := kanbanApp.saveScoutChatThread(preFixThread); err != nil {
		t.Fatal(err)
	}
	legacyPlan := plan
	legacyReceipt := *plan.RouteReceipt
	legacyReceipt.SourceSelectionDigest = ""
	legacyReceipt.Digest, err = legacyReceipt.contractDigest()
	if err != nil {
		t.Fatal(err)
	}
	legacyPlan.RouteReceipt = &legacyReceipt
	legacyRaw, err := json.Marshal(legacyPlan)
	if err != nil {
		t.Fatal(err)
	}
	currentParent := mustArtifact(t, kanbanApp, run.Artifact.ID)
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(run.Artifact.ID, "", currentParent.Text, scoutParticipantName, map[string]string{"goalPlan": string(legacyRaw), "goalRouteDigest": legacyReceipt.Digest}); err != nil {
		t.Fatal(err)
	}
	badLegacyReceipt := legacyReceipt
	badLegacyReceipt.Digest = strings.Repeat("0", 64)
	badLegacyPlan := legacyPlan
	badLegacyPlan.RouteReceipt = &badLegacyReceipt
	if err := engine.prepareGoalRoute(&badLegacyPlan, run.Artifact.ID); err == nil {
		t.Fatal("legacy upgrade replaced an unauthenticated old receipt digest")
	}
	legacyThread, _, err := kanbanApp.scoutChatThreadByID(user.Email, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	changedLegacyThread := legacyThread
	changedLegacyThread.Messages = append([]scoutChatMessageRecord(nil), legacyThread.Messages...)
	changedLegacyThread.Messages[scoutChatMessageIndex(changedLegacyThread, "production-paste")].Text = "ship"
	if err := kanbanApp.saveScoutChatThread(changedLegacyThread); err != nil {
		t.Fatal(err)
	}
	changedLegacyPlan := legacyPlan
	if err := engine.prepareGoalRoute(&changedLegacyPlan, run.Artifact.ID); err == nil {
		t.Fatal("legacy upgrade accepted shortened mutable source context")
	}
	if persisted := mustGoalPlan(t, kanbanApp, run.Artifact.ID); persisted.RouteReceipt == nil || persisted.RouteReceipt.SourceSelectionDigest != "" {
		t.Fatalf("failed legacy authentication persisted a new selection binding: %+v", persisted.RouteReceipt)
	}
	if err := kanbanApp.saveScoutChatThread(legacyThread); err != nil {
		t.Fatal(err)
	}
	if err := engine.prepareGoalRoute(&legacyPlan, run.Artifact.ID); err != nil {
		t.Fatalf("legacy selection upgrade: %v", err)
	}
	if legacyPlan.RouteReceipt.SourceSelectionDigest == "" || mustGoalPlan(t, kanbanApp, run.Artifact.ID).RouteReceipt.SourceSelectionDigest == "" {
		t.Fatal("legacy source selection upgrade was not durably bound on the parent")
	}
	legacyTask, err := engine.processStageTaskAuthorized(context.Background(), &legacyPlan, "", legacyPlan.subtaskByID("context_snapshot"), packagingStudioStage(t, packagingStudioDefinition(), "context_snapshot"))
	if err != nil || !strings.Contains(legacyTask, "PRODUCTION_PDF_SENTINEL") {
		t.Fatalf("pre-contextRefs live receipt did not rehydrate its exact accepted proposal source: err=%v task=%s", err, legacyTask)
	}

	kanbanApp.pendingAttachmentUploadsMu.Lock()
	revoked := kanbanApp.pendingAttachmentUploads[grantedPDF.SourceID]
	revoked.State = attachmentSourceRevoked
	kanbanApp.pendingAttachmentUploads[grantedPDF.SourceID] = revoked
	kanbanApp.pendingAttachmentUploadsMu.Unlock()
	_, revokedErr := engine.processStageTaskAuthorized(context.Background(), &plan, "", plan.subtaskByID("context_snapshot"), contextStage)
	if revokedErr == nil || (!strings.Contains(revokedErr.Error(), "changed") && !strings.Contains(revokedErr.Error(), "readable")) {
		t.Fatalf("revoked source passed context admission: %v", revokedErr)
	}
	kanbanApp.pendingAttachmentUploadsMu.Lock()
	revoked.State = attachmentSourceCommitted
	kanbanApp.pendingAttachmentUploads[grantedPDF.SourceID] = revoked
	kanbanApp.pendingAttachmentUploadsMu.Unlock()

	loadThread := func() scoutChatThreadRecord {
		current, _, loadErr := kanbanApp.scoutChatThreadByID(user.Email, channel.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return current
	}
	originalThread := loadThread()
	toctouMutated := originalThread
	toctouMutated.Messages = append([]scoutChatMessageRecord(nil), originalThread.Messages...)
	toctouMutated.Messages[scoutChatMessageIndex(toctouMutated, "production-paste")].Text += " MUTATED_BETWEEN_SNAPSHOT_AND_VERIFY"
	engine.sourceSelectionAfterSnapshotProbe = func() {
		engine.sourceSelectionAfterSnapshotProbe = nil
		if saveErr := kanbanApp.saveScoutChatThread(toctouMutated); saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	_, toctouErr := engine.processStageTaskAuthorized(context.Background(), &plan, "", plan.subtaskByID("context_snapshot"), contextStage)
	engine.sourceSelectionAfterSnapshotProbe = nil
	if toctouErr == nil || !strings.Contains(toctouErr.Error(), "changed") {
		t.Fatalf("mutation between selected snapshot and route verification passed context admission: %v", toctouErr)
	}
	if saveErr := kanbanApp.saveScoutChatThread(originalThread); saveErr != nil {
		t.Fatal(saveErr)
	}
	assertMutationBlocked := func(label string, mutate func(*scoutChatThreadRecord)) {
		mutated := originalThread
		mutated.Messages = append([]scoutChatMessageRecord(nil), originalThread.Messages...)
		mutate(&mutated)
		if saveErr := kanbanApp.saveScoutChatThread(mutated); saveErr != nil {
			t.Fatal(saveErr)
		}
		_, mutationErr := engine.processStageTaskAuthorized(context.Background(), &plan, "", plan.subtaskByID("context_snapshot"), contextStage)
		if mutationErr == nil || !strings.Contains(mutationErr.Error(), "changed") {
			t.Fatalf("%s old-branch mutation passed context admission or lacked truthful classification: %v", label, mutationErr)
		}
		if saveErr := kanbanApp.saveScoutChatThread(originalThread); saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	assertMutationBlocked("edit", func(thread *scoutChatThreadRecord) {
		thread.Messages[scoutChatMessageIndex(*thread, "production-paste")].Text += " MUTATED_AFTER_APPROVAL"
	})
	assertMutationBlocked("delete", func(thread *scoutChatThreadRecord) {
		index := scoutChatMessageIndex(*thread, "production-paste")
		thread.Messages = append(thread.Messages[:index], thread.Messages[index+1:]...)
	})
	assertMutationBlocked("author label", func(thread *scoutChatThreadRecord) {
		thread.Messages[scoutChatMessageIndex(*thread, "production-paste")].AuthorName = "Changed speaker label"
	})
	assertMutationBlocked("attachment kind", func(thread *scoutChatThreadRecord) {
		index := scoutChatMessageIndex(*thread, "production-main-pdf")
		thread.Messages[index].Files = append([]scoutChatFileAttachment(nil), thread.Messages[index].Files...)
		thread.Messages[index].Files[0].Kind = "text"
	})

	mutated := originalThread
	mutated.Messages = append([]scoutChatMessageRecord(nil), originalThread.Messages...)
	mutated.Messages[scoutChatMessageIndex(mutated, "production-paste")].Text += " MUTATED_FOR_TERMINAL_PROOF"
	if err := kanbanApp.saveScoutChatThread(mutated); err != nil {
		t.Fatal(err)
	}
	kanbanApp.runGoalThread(run.Artifact.ID)
	terminal := mustArtifact(t, kanbanApp, run.Artifact.ID)
	if terminal.Metadata["threadStatus"] != "error" || !strings.Contains(terminal.Metadata["goalBlocker"], "source selection changed") ||
		strings.Contains(terminal.Metadata["goalBlocker"], "PRODUCTION_PDF_SENTINEL") || strings.Contains(terminal.Metadata["goalBlocker"], "MUTATED_FOR_TERMINAL_PROOF") {
		t.Fatalf("mutated old source did not terminalize visibly and without source leakage: status=%q blocker=%q", terminal.Metadata["threadStatus"], terminal.Metadata["goalBlocker"])
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
