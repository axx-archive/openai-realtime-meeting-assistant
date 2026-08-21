package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func withIsolatedScoutFileApp(t *testing.T) (*kanbanBoardApp, *userAccount) {
	t.Helper()
	setupAuthTestEnv(t)
	previous := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previous })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	return app, user
}

func TestScoutFileAdmissionRejectsFilenameOnlyReviewWithoutProviderCall(t *testing.T) {
	app, user := withIsolatedScoutFileApp(t)
	app.apiKey = "sk-openai-test"
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("filename-only dependency must stop before every provider call")
		return "", nil
	})

	thread, err := app.createScoutChatThread(user.Email, user.Name, "Country Golf", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID,
		"@scout review this deck and recommend the Country Golf template",
		[]scoutChatFileAttachment{{Name: "Dog Perfect PDF.pdf", Kind: "pdf", Size: 460251094}}, "")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if response["dependencyRequired"] != true || response["providerCalls"] != 0 {
		t.Fatalf("response=%#v, want dependencyRequired and zero provider calls", response)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || !strings.Contains(answer.Text, "20 MB") || !strings.Contains(answer.Text, "Nothing is running yet") {
		t.Fatalf("answer=%#v, want actionable size limit and honest run state", response["answer"])
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Proposal != nil || saved.Messages[1].Thread != nil {
		t.Fatalf("messages=%#v, filename-only source must not mint or launch work", saved.Messages)
	}
}

func TestReplyToScoutRequestsMissingPDFInsteadOfRepeatingPromise(t *testing.T) {
	app, user := withIsolatedScoutFileApp(t)
	app.apiKey = "sk-openai-test"
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("missing reply dependency must not reach a provider")
		return "", nil
	})

	thread, err := app.createScoutChatThread(user.Email, user.Name, "Country Golf", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID,
		scoutChatMessageRecord{ID: "pdf-message", Kind: "message", Role: "user", Text: "@scout review this deck", AuthorName: "Tyler", AuthorEmail: "tyler@shareability.com", CreatedAt: now, Files: []scoutChatFileAttachment{{Name: "Dog Perfect PDF.pdf", Size: 460251094}}},
		scoutChatMessageRecord{ID: "false-promise", Kind: "message", Role: "scout", AuthorName: scoutParticipantName, Text: "I'll review it and come back with recommendations.", CausedByMessageID: "pdf-message", CreatedAt: now},
	)
	if err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	response, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, thread.ID, "open it and review it", nil, "", "false-promise", "")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	answer := response["answer"].(scoutChatMessageRecord)
	if !strings.Contains(answer.Text, "Dog Perfect PDF.pdf") || !strings.Contains(answer.Text, "don't have readable contents") {
		t.Fatalf("answer=%q, want the exact missing dependency", answer.Text)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 4 {
		t.Fatalf("thread messages=%d, want root, prior Scout turn, human reply, and threaded Scout response", len(saved.Messages))
	}
	humanReply := saved.Messages[2]
	scoutReply := saved.Messages[3]
	if humanReply.ReplyTo == nil || humanReply.ReplyTo.MessageID != "false-promise" {
		t.Fatalf("human reply ancestry=%+v", humanReply.ReplyTo)
	}
	if scoutReply.ReplyTo == nil || scoutReply.ReplyTo.MessageID != "false-promise" || scoutReply.Role != "scout" {
		t.Fatalf("Scout reply ancestry=%+v role=%q, want the same durable side conversation", scoutReply.ReplyTo, scoutReply.Role)
	}
}

func TestReplyExactFilenameMissingOrAmbiguousStopsBeforeApproval(t *testing.T) {
	app, user := withIsolatedScoutFileApp(t)
	app.apiKey = "sk-openai-test"
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("missing or ambiguous exact filename must stop before every provider call")
		return "", nil
	})
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Like A Farmer", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	root := scoutChatMessageRecord{ID: "exact-file-root", Kind: "message", Role: "scout", AuthorName: scoutParticipantName, Text: "Which source should I use?", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, root); err != nil {
		t.Fatal(err)
	}

	missing, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, thread.ID,
		"Review Missing_Like_A_Farmer_Strategy.pdf and create the deck.", nil, "", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	missingAnswer := missing["answer"].(scoutChatMessageRecord)
	if missing["dependencyRequired"] != true || missing["providerCalls"] != 0 || missingAnswer.Proposal != nil ||
		!strings.Contains(missingAnswer.Text, "exact") || !strings.Contains(strings.ToLower(missingAnswer.Text), "nothing was launched") {
		t.Fatalf("missing exact filename crossed approval boundary: %#v", missing)
	}

	for index, body := range []string{"FIRST_EXACT_REVISION", "SECOND_EXACT_REVISION"} {
		current, _, loadErr := app.scoutChatThreadByID(user.Email, thread.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		ref, putErr := putBlob([]byte("%PDF-1.7 "+body), "application/pdf")
		if putErr != nil {
			t.Fatal(putErr)
		}
		reservationID := fmt.Sprintf("ambiguous-exact-%d", index)
		file := reserveTestAttachment(t, app, user, current, scoutChatFileAttachment{Ref: ref, Name: "Exact_Strategy.pdf", Kind: "pdf"}, reservationID)
		file.Text = body
		message := scoutChatMessageRecord{ID: fmt.Sprintf("ambiguous-file-%d", index), Kind: "message", Role: "user", AuthorName: "Tyler", Text: "Source revision", Files: []scoutChatFileAttachment{file}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), attachmentReservationID: reservationID}
		if _, commitErr := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); commitErr != nil {
			t.Fatal(commitErr)
		}
	}
	ambiguous, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, thread.ID,
		"Review Exact_Strategy.pdf and create the deck.", nil, "", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ambiguousAnswer := ambiguous["answer"].(scoutChatMessageRecord)
	if ambiguous["dependencyRequired"] != true || ambiguous["providerCalls"] != 0 || ambiguousAnswer.Proposal != nil ||
		!strings.Contains(ambiguousAnswer.Text, "More than one readable attachment") || !strings.Contains(strings.ToLower(ambiguousAnswer.Text), "nothing was launched") {
		t.Fatalf("ambiguous exact filename crossed approval boundary: %#v", ambiguous)
	}
}

func TestReplyExactFilenameSetIsConjunctiveBoundarySafeAndSupportsQuotedSpaces(t *testing.T) {
	app, user := withIsolatedScoutFileApp(t)
	app.apiKey = "sk-openai-test"
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("exact filename dependency routing must remain deterministic")
		return "", nil
	})
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Like A Farmer", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	root := scoutChatMessageRecord{ID: "filename-set-root", Kind: "message", Role: "scout", AuthorName: scoutParticipantName, Text: "Name the exact files to use.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, root); err != nil {
		t.Fatal(err)
	}
	attach := func(id, name, body string) string {
		current, _, loadErr := app.scoutChatThreadByID(user.Email, thread.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		ref, putErr := putBlob([]byte("%PDF-1.7 "+body), "application/pdf")
		if putErr != nil {
			t.Fatal(putErr)
		}
		reservationID := "filename-set-" + id
		file := reserveTestAttachment(t, app, user, current, scoutChatFileAttachment{Ref: ref, Name: name, Kind: "pdf"}, reservationID)
		file.Text = body
		message := scoutChatMessageRecord{ID: id, Kind: "message", Role: "user", AuthorName: "Tyler", Text: "Authorized source", Files: []scoutChatFileAttachment{file}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), attachmentReservationID: reservationID}
		if _, commitErr := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); commitErr != nil {
			t.Fatal(commitErr)
		}
		return scoutChatFileContextRef(thread.ID, id, 0)
	}
	existingRef := attach("existing-file", "Existing.pdf", "EXISTING_FILE_SENTINEL")
	_ = attach("suffix-collision", "evilfoo.pdf", "EVIL_SUFFIX_SENTINEL")
	spacedRef := attach("spaced-file", "My Strategy.pdf", "SPACED_FILENAME_SENTINEL")

	mixed, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, thread.ID,
		"Review Existing.pdf + Missing.pdf and create the deck.", nil, "", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	mixedAnswer := mixed["answer"].(scoutChatMessageRecord)
	if mixed["dependencyRequired"] != true || mixed["providerCalls"] != 0 || mixedAnswer.Proposal != nil ||
		!strings.Contains(mixedAnswer.Text, "Missing.pdf") || strings.Contains(mixedAnswer.Text, existingRef) {
		t.Fatalf("mixed existing+missing filename set launched partially: %#v", mixed)
	}

	suffix, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, thread.ID,
		"Review foo.pdf and create the deck.", nil, "", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	suffixAnswer := suffix["answer"].(scoutChatMessageRecord)
	if suffix["dependencyRequired"] != true || suffix["providerCalls"] != 0 || suffixAnswer.Proposal != nil ||
		!strings.Contains(suffixAnswer.Text, "foo.pdf") || strings.Contains(suffixAnswer.Text, "evilfoo.pdf") || strings.Contains(suffixAnswer.Text, "EVIL_SUFFIX_SENTINEL") {
		t.Fatalf("suffix collision admitted evilfoo.pdf for foo.pdf: %#v", suffix)
	}

	quoted, err := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, thread.ID,
		"Create a 10-slide presentation from \"My Strategy.pdf\".", nil, "", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	proposal, ok := quoted["proposal"].(*scoutRouterProposal)
	if !ok || proposal == nil {
		t.Fatalf("quoted spaced filename did not deterministically reach approval: %#v", quoted)
	}
	refs := decodeAssistantContextRefs(proposal.ContextRefs)
	if len(refs) != 1 || refs[0] != spacedRef || refs[0] == existingRef {
		t.Fatalf("quoted spaced filename refs=%#v, want only %q", refs, spacedRef)
	}
}

func TestExactFilenamePreflightRejectsSiblingPNGTooManyAndHidesRevokedExistence(t *testing.T) {
	app, user := withIsolatedScoutFileApp(t)
	app.apiKey = "sk-openai-test"
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("invalid exact filename set must stop before every provider call")
		return "", nil
	})
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Like A Farmer", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	root := scoutChatMessageRecord{ID: "filename-preflight-root", Kind: "message", Role: "scout", AuthorName: scoutParticipantName, Text: "Name the exact files to use.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, root); err != nil {
		t.Fatal(err)
	}
	requestUnavailable := func(text string) scoutChatMessageRecord {
		response, requestErr := app.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, thread.ID, text, nil, "", root.ID, "")
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		answer := response["answer"].(scoutChatMessageRecord)
		if response["dependencyRequired"] != true || response["providerCalls"] != 0 || answer.Proposal != nil {
			t.Fatalf("invalid exact source reached routing/approval: %#v", response)
		}
		return answer
	}
	missingPNG := requestUnavailable("Create the deck from Missing.png.")
	if !strings.Contains(missingPNG.Text, "Missing.png") || !strings.Contains(strings.ToLower(missingPNG.Text), "nothing was launched") {
		t.Fatalf("missing upload-safe image was not surfaced: %q", missingPNG.Text)
	}

	attach := func(messageID, name, body string, replyTo *scoutChatReplyRef) scoutChatFileAttachment {
		current, _, loadErr := app.scoutChatThreadByID(user.Email, thread.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		ref, putErr := putBlob([]byte("%PDF-1.7 "+body), "application/pdf")
		if putErr != nil {
			t.Fatal(putErr)
		}
		reservationID := "filename-preflight-" + messageID
		file := reserveTestAttachment(t, app, user, current, scoutChatFileAttachment{Ref: ref, Name: name, Kind: "pdf"}, reservationID)
		file.Text = body
		message := scoutChatMessageRecord{ID: messageID, Kind: "message", Role: "user", AuthorName: "Tyler", Text: "Authorized source", ReplyTo: replyTo, Files: []scoutChatFileAttachment{file}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), attachmentReservationID: reservationID}
		if _, commitErr := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); commitErr != nil {
			t.Fatal(commitErr)
		}
		return file
	}
	siblingRoot := scoutChatMessageRecord{ID: "other-reply-root", Kind: "message", Role: "scout", AuthorName: scoutParticipantName, Text: "Unrelated work", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, siblingRoot); err != nil {
		t.Fatal(err)
	}
	attach("sibling-file", "Sibling.pdf", "SIBLING_EXACT_SECRET", &scoutChatReplyRef{MessageID: siblingRoot.ID})
	sibling := requestUnavailable("Create the deck from Sibling.pdf.")
	if strings.Contains(sibling.Text, "SIBLING_EXACT_SECRET") || !strings.Contains(strings.ToLower(sibling.Text), "couldn't resolve") {
		t.Fatalf("exact filename escaped an unrelated reply branch: %q", sibling.Text)
	}

	names := []string{"One.pdf", "Two.pdf", "Three.pdf", "Four.pdf", "Five.pdf"}
	for index, name := range names {
		attach(fmt.Sprintf("too-many-%d", index), name, "BOUND_"+name, nil)
	}
	tooMany := requestUnavailable("Review One.pdf + Two.pdf + Three.pdf + Four.pdf + Five.pdf and create the deck.")
	if !strings.Contains(tooMany.Text, "no more than 4 exact files") {
		t.Fatalf("over-limit exact refs were not rejected at preflight: %q", tooMany.Text)
	}

	missingRevoked := requestUnavailable("Create the deck from Revoked.pdf.")
	revokedFile := attach("revoked-file", "Revoked.pdf", "REVOKED_EXISTENCE_SECRET", nil)
	app.pendingAttachmentUploadsMu.Lock()
	grant := app.pendingAttachmentUploads[revokedFile.SourceID]
	grant.State = attachmentSourceRevoked
	app.pendingAttachmentUploads[revokedFile.SourceID] = grant
	app.pendingAttachmentUploadsMu.Unlock()
	afterRevoke := requestUnavailable("Create the deck from Revoked.pdf.")
	if afterRevoke.Text != missingRevoked.Text || strings.Contains(afterRevoke.Text, "REVOKED_EXISTENCE_SECRET") {
		t.Fatalf("revoked filename existence leaked through preflight: missing=%q revoked=%q", missingRevoked.Text, afterRevoke.Text)
	}
}

func TestAuthorizedFilesSourceBindsProposalLaunchAndRunningCard(t *testing.T) {
	app, user := withIsolatedScoutFileApp(t)
	entry, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-dog-perfect",
		"File Dog Perfect PDF.pdf uploaded by AJ. Deck thesis: dog retail partnerships create a repeatable golf-event sponsorship template.",
		map[string]string{"name": "Dog Perfect PDF.pdf", "origin": "files", "brainStatus": fileBrainStatusIngested, "visibility": "organization"})
	if err != nil {
		t.Fatalf("seed Files entry: %v", err)
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Country Golf", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID,
		"@scout review this deck and recommend the Country Golf template",
		[]scoutChatFileAttachment{{Name: "Dog Perfect PDF.pdf", Kind: "pdf", Size: 460251094}}, "")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok || proposal.Mode != "research" || proposal.ContextRefs == "" {
		t.Fatalf("proposal=%#v, want source-bound research proposal", response["proposal"])
	}
	if refs := decodeAssistantContextRefs(proposal.ContextRefs); len(refs) != 1 || refs[0] != assistantFileContextRef(entry.ID) {
		t.Fatalf("context refs=%#v, want exact Files row", decodeAssistantContextRefs(proposal.ContextRefs))
	}

	previousStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousStart })
	proposalMessage := response["answer"].(scoutChatMessageRecord)
	resolved, err := app.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{
		Action: "accepted", MessageID: proposalMessage.ID,
	})
	if err != nil {
		t.Fatalf("accept proposal: %v", err)
	}
	run, ok := resolved["agentThread"].(scoutAgentThread)
	if !ok || run.Status != "running" || run.Artifact.Metadata["contextRefs"] != proposal.ContextRefs {
		t.Fatalf("agent thread=%#v, want running exact-source-bound worker", resolved["agentThread"])
	}
	saved := resolved["thread"].(scoutChatThreadRecord)
	card := saved.Messages[len(saved.Messages)-1]
	if card.Kind != "thread" || card.Thread == nil || card.Thread.Status != "running" || card.Thread.ArtifactID == "" {
		t.Fatalf("launch card=%#v, want durable running indicator", card)
	}
}

func TestScoutFilesCatalogAndExactContentReachModelContext(t *testing.T) {
	app, user := withIsolatedScoutFileApp(t)
	app.apiKey = "sk-openai-test"
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	_, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-country-golf",
		"File Country Golf Brief.pdf uploaded by AJ. Exact brief fact: the first pilot is a 24-player creator invitational.",
		map[string]string{"name": "Country Golf Brief.pdf", "origin": "files", "brainStatus": fileBrainStatusIngested, "visibility": "organization"})
	if err != nil {
		t.Fatalf("seed Files entry: %v", err)
	}
	var gotInput string
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_chat" {
			return "", nil
		}
		gotInput = request.Input
		return "The file is available.", nil
	})
	if _, err := app.resolveAssistantQueryContextForUser(context.Background(), user.Email, "What is in our Files tab, especially the Country Golf Brief?", nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, want := range []string{"Authorized Files catalog", "Country Golf Brief.pdf", "24-player creator invitational"} {
		if !strings.Contains(gotInput, want) {
			t.Fatalf("model input missing %q:\n%s", want, gotInput)
		}
	}
}
