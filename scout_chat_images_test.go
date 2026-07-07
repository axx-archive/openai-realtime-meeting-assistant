package main

// Card 096 — the single-shot concept render behind a private-thread image
// confirm. These tests pin the propose-confirm law for images: the router
// door is gated on OpenAI being configured, an image ask earns a PROPOSAL
// (never a silent generate), the confirm generates + files a design artifact
// with a kind=image asset + commits an inline-renderable message, the live prod
// 429 (insufficient_quota) lands a friendly error bubble instead of the raw
// upstream body, and a dismissal re-asks the stored query as Tier 0.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The propose_image door is gated on OpenAI: a keyless-OpenAI deploy must never
// be offered a render it cannot produce (the four text-route tools stay —
// propose_tool_run / propose_workstream / offer_choices / propose_goal), and a
// configured deploy gains it as the fifth tool.
func TestScoutChatRouterImageToolGatedOnOpenAIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if tools := scoutRouterTools(); len(tools) != 4 {
		t.Fatalf("keyless-OpenAI router tools=%d, want 4 (no propose_image)", len(tools))
	} else {
		for _, tool := range tools {
			if tool.Name == "propose_image" {
				t.Fatal("propose_image must not be offered without OpenAI configured")
			}
		}
	}
	if strings.Contains(scoutRouterSystemPrompt(), "propose_image") {
		t.Fatal("the router system prompt must not name propose_image keyless")
	}

	t.Setenv("OPENAI_API_KEY", "test-image-key")
	tools := scoutRouterTools()
	if len(tools) != 5 || tools[4].Name != "propose_image" {
		t.Fatalf("configured router tools=%#v, want propose_image appended fifth", tools)
	}
	if !strings.Contains(scoutRouterSystemPrompt(), "propose_image") {
		t.Fatal("the configured router system prompt must name propose_image in the intent map")
	}
}

// The persisted Kind=image message survives the store round trip: the blob ref,
// mime, filed artifact id, and prompt all come back intact.
func TestScoutChatImageMessageRoundTrip(t *testing.T) {
	thread := scoutChatThreadRecord{
		ID:         "scout-chat-image-rt",
		Title:      "Scout",
		OwnerEmail: "aj@shareability.com",
		CreatedAt:  "2026-07-06T00:00:00Z",
		UpdatedAt:  "2026-07-06T00:00:00Z",
		Messages: []scoutChatMessageRecord{{
			ID:        "scout-chat-message-1",
			Kind:      scoutChatMessageKindImage,
			Role:      "scout",
			Text:      "here's the concept render.",
			CreatedAt: "2026-07-06T00:00:00Z",
			Image: &scoutChatImageRef{
				Ref:        strings.Repeat("a", 64),
				Mime:       "image/png",
				Name:       "concept-render.png",
				ArtifactID: "os-artifact-design-1",
				Prompt:     "a neon rocket over a harbor at dusk",
			},
		}},
	}
	encoded, err := encodeScoutChatThread(thread)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, ok := decodeScoutChatThreadEntry(meetingMemoryEntry{
		ID:       thread.ID,
		Kind:     meetingMemoryKindScoutChat,
		Text:     encoded,
		Metadata: scoutChatThreadMetadata(thread),
	})
	if !ok {
		t.Fatal("decode round trip failed")
	}
	if len(decoded.Messages) != 1 || decoded.Messages[0].Kind != scoutChatMessageKindImage {
		t.Fatalf("messages=%#v, want the one image message", decoded.Messages)
	}
	image := decoded.Messages[0].Image
	if image == nil {
		t.Fatal("image data lost in the round trip")
	}
	if image.Ref != strings.Repeat("a", 64) || image.Mime != "image/png" || image.ArtifactID != "os-artifact-design-1" || image.Prompt != "a neon rocket over a harbor at dusk" {
		t.Fatalf("image=%#v, want the ref/mime/artifact/prompt preserved", image)
	}
}

// A propose_image routing turn persists a Kind=proposal card with proposal.Kind
// == image and generates NOTHING: the confirm is the only generate door.
func TestScoutChatRouterProposesImageNeverGenerates(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	// OpenAI intentionally UNSET so the deterministic guard's image branch is
	// off and this exercises the MODEL propose_image validation path; the mock
	// returns propose_image regardless (scoutRouterProposalFromToolUse never
	// checks the env, only the block name).
	t.Setenv("OPENAI_API_KEY", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startScoutChatImageAsync
	startScoutChatImageAsync = func(_ *kanbanBoardApp, _ string, _ string, _ string, _ string) {
		t.Fatal("a proposal must never generate an image")
	}
	t.Cleanup(func() { startScoutChatImageAsync = previousRunner })
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("a proposing turn must not also run the Q&A path")
		return "", nil
	})

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{
			StopReason: "tool_use",
			Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_image", "propose_image", map[string]any{
					"prompt": "a rooftop crowd of the crew mid-laugh, hats in the air",
					"title":  "Rooftop celebration",
				}),
			},
		}, nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	text := "let's whip up a rooftop shot of the crew celebrating"
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, text, nil, "")
	if err != nil {
		t.Fatalf("append routed message: %v", err)
	}
	if _, launched := response["agentThread"]; launched {
		t.Fatalf("response keys=%v — NEVER silent-launch", responseKeys(response))
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok {
		t.Fatalf("proposal type=%T, want *scoutRouterProposal", response["proposal"])
	}
	if proposal.Kind != scoutRouterProposalKindImage {
		t.Fatalf("proposal.kind=%q, want image", proposal.Kind)
	}
	if proposal.Objective != "a rooftop crowd of the crew mid-laugh, hats in the air" {
		t.Fatalf("objective=%q, want the routed prompt", proposal.Objective)
	}
	if proposal.Query != text {
		t.Fatalf("query=%q, want the utterance for the Tier-0 escape", proposal.Query)
	}
	if proposal.WeightLabel != scoutProposalWeightImageRender || proposal.Authority != toolAuthorityWorkspaceWrite {
		t.Fatalf("proposal weight/authority=%q/%q, want the concept-render cost line + workspace_write", proposal.WeightLabel, proposal.Authority)
	}

	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != scoutChatMessageKindProposal || answer.Proposal == nil {
		t.Fatalf("answer=%#v, want a persisted Kind=proposal message", response["answer"])
	}
}

// The deterministic pre-router guard (AJ's "image request failed" fix): a
// literal "make an image of X" commits the concept-render card BEFORE the model
// turn, so thread-context gravity can never drag the ask off-route. The model
// responder must never be reached.
func TestScoutChatImageDeterministicGuardProposesConceptRender(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startScoutChatImageAsync
	startScoutChatImageAsync = func(_ *kanbanBoardApp, _ string, _ string, _ string, _ string) {
		t.Fatal("the guard proposes a card, it must never generate")
	}
	t.Cleanup(func() { startScoutChatImageAsync = previousRunner })
	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("the deterministic guard must short-circuit the model turn for a literal image ask")
		return anthropicMessagesResponse{}, nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	text := "make an image of a neon rocket over a harbor at dusk"
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, text, nil, "")
	if err != nil {
		t.Fatalf("append image ask: %v", err)
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok || proposal.Kind != scoutRouterProposalKindImage {
		t.Fatalf("proposal=%#v, want an image proposal from the deterministic guard", response["proposal"])
	}
	if proposal.Objective != text || proposal.Query != text {
		t.Fatalf("proposal objective/query=%q/%q, want the literal ask", proposal.Objective, proposal.Query)
	}
}

// The confirm generates: the accept runs the render synchronously against the
// fake images API, files a design artifact (source=chat_image) with one
// kind=image asset whose blob round-trips, and commits an inline Kind=image
// message carrying a valid 64-hex ref.
func TestScoutChatImageProposalAcceptGeneratesAndFiles(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	t.Setenv("OPENAI_IMAGE_MODEL", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	imageBytes := []byte("\x89PNG\r\n\x1a\nconcept-render-bytes")
	var calls int
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageBytes)}},
			"output_format": "png",
		})
	})

	// Run the async render synchronously for the assertions (the
	// startAgentThreadAsync test pattern).
	previousRunner := startScoutChatImageAsync
	startScoutChatImageAsync = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string) {
		app.runScoutChatImageGeneration(threadID, ownerEmail, prompt, createdBy)
	}
	t.Cleanup(func() { startScoutChatImageAsync = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	messageID := seedScoutChatProposalCard(t, private.ID, "aj@shareability.com", *scoutRouterImageProposal("a harbor at golden hour, container cranes at dawn", "make an image of the harbor"))

	response, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "accepted",
		MessageID: messageID,
	})
	if err != nil {
		t.Fatalf("accept image proposal: %v", err)
	}
	if calls != 1 {
		t.Fatalf("images API called %d times, want exactly once", calls)
	}
	// The immediate response is the activity line, not the picture.
	if answer, ok := response["answer"].(scoutChatMessageRecord); !ok || !strings.Contains(answer.Text, "concept render started") {
		t.Fatalf("immediate answer=%#v, want the activity line", response["answer"])
	}

	saved, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	var imageMessage *scoutChatMessageRecord
	for index := range saved.Messages {
		if saved.Messages[index].Kind == scoutChatMessageKindImage {
			imageMessage = &saved.Messages[index]
		}
	}
	if imageMessage == nil || imageMessage.Image == nil {
		t.Fatalf("messages=%#v, want a committed Kind=image message", saved.Messages)
	}
	if !validBlobRef(imageMessage.Image.Ref) {
		t.Fatalf("image ref=%q, want a content-addressed blob ref", imageMessage.Image.Ref)
	}
	stored, meta, err := getBlob(imageMessage.Image.Ref)
	if err != nil {
		t.Fatalf("getBlob: %v", err)
	}
	if string(stored) != string(imageBytes) || meta.Mime != "image/png" {
		t.Fatalf("stored blob mismatch: mime=%q", meta.Mime)
	}

	// The filed design artifact: source=chat_image, one kind=image asset whose
	// ref matches the message and blob.
	var filed *meetingMemoryEntry
	for _, entry := range kanbanApp.osArtifactsSnapshot(0) {
		if entry.Metadata["source"] == "chat_image" {
			e := entry
			filed = &e
		}
	}
	if filed == nil {
		t.Fatal("no design artifact filed with source=chat_image")
	}
	if filed.Metadata["imagePrompt"] != "a harbor at golden hour, container cranes at dawn" {
		t.Fatalf("artifact imagePrompt=%q, want the confirmed prompt", filed.Metadata["imagePrompt"])
	}
	assets := artifactAssets(*filed)
	if len(assets) != 1 || assets[0].Kind != "image" || assets[0].Ref != imageMessage.Image.Ref {
		t.Fatalf("artifact assets=%#v, want one kind=image asset matching the message ref", assets)
	}
	if imageMessage.Image.ArtifactID != filed.ID {
		t.Fatalf("image message artifactId=%q, want the filed artifact %q", imageMessage.Image.ArtifactID, filed.ID)
	}
}

// The live prod failure (429 insufficient_quota): the accept commits a friendly
// error bubble naming the exhausted quota, NEVER the raw upstream body, and
// files no artifact.
func TestScoutChatImageProposalAcceptQuotaExhaustedFriendlyError(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	const rawBodyFragment = "please check your plan and billing details"
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"insufficient_quota","message":"You exceeded your current quota, ` + rawBodyFragment + `."}}`))
	})

	previousRunner := startScoutChatImageAsync
	startScoutChatImageAsync = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string) {
		app.runScoutChatImageGeneration(threadID, ownerEmail, prompt, createdBy)
	}
	t.Cleanup(func() { startScoutChatImageAsync = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	messageID := seedScoutChatProposalCard(t, private.ID, "aj@shareability.com", *scoutRouterImageProposal("a harbor at golden hour", "make an image of the harbor"))

	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "accepted",
		MessageID: messageID,
	}); err != nil {
		t.Fatalf("accept image proposal: %v", err)
	}

	saved, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	var errorMessage *scoutChatMessageRecord
	for index := range saved.Messages {
		if saved.Messages[index].Role == "error" {
			errorMessage = &saved.Messages[index]
		}
	}
	if errorMessage == nil {
		t.Fatalf("messages=%#v, want a committed error bubble", saved.Messages)
	}
	if !strings.Contains(errorMessage.Text, "quota is exhausted") {
		t.Fatalf("error text=%q, want the friendly quota message", errorMessage.Text)
	}
	if strings.Contains(errorMessage.Text, rawBodyFragment) {
		t.Fatalf("error text=%q leaked the raw upstream body", errorMessage.Text)
	}
	for _, entry := range kanbanApp.osArtifactsSnapshot(0) {
		if entry.Metadata["source"] == "chat_image" {
			t.Fatal("a failed render must file no chat_image artifact")
		}
	}
}

// A dismissed image proposal re-asks the stored query as Tier 0 — the existing
// dismissal path, exercised for the image kind (a regression pin, no new code).
func TestScoutChatImageProposalDismissReAsksTier0(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startScoutChatImageAsync
	startScoutChatImageAsync = func(_ *kanbanBoardApp, _ string, _ string, _ string, _ string) {
		t.Fatal("a dismissal must never generate")
	}
	t.Cleanup(func() { startScoutChatImageAsync = previousRunner })

	var askedTier0 string
	swapAnthropicTextResponder(t, func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		askedTier0 = request.Input
		return "sure — describe the vibe and I can propose a render.", nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	messageID := seedScoutChatProposalCard(t, private.ID, "aj@shareability.com", *scoutRouterImageProposal("the team celebrating on a rooftop", "make an image of the team celebrating"))

	response, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "dismissed",
		MessageID: messageID,
	})
	if err != nil {
		t.Fatalf("dismiss image proposal: %v", err)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Text != "sure — describe the vibe and I can propose a render." {
		t.Fatalf("answer=%#v, want the Tier-0 inline answer", response["answer"])
	}
	if !strings.Contains(askedTier0, "make an image of the team celebrating") {
		t.Fatalf("Tier-0 input=%q, want the stored query re-asked", askedTier0)
	}
}
