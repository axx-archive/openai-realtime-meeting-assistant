package main

import (
	"context"
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
		scoutChatMessageRecord{ID: "false-promise", Kind: "message", Role: "scout", AuthorName: scoutParticipantName, Text: "I'll review it and come back with recommendations.", CreatedAt: now},
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
