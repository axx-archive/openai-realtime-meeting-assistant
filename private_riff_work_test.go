package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func privateRiffWorkFixture(t *testing.T) (*kanbanBoardApp, *userAccount, scoutChatThreadRecord, scoutChatThreadRecord) {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "private-riff-router-test"
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	source := seedPrivateRiffChannel(t, app, user.Email, 2)
	riff, created, err := app.createPrivateRiff(user, source.ID, "riff-source-02", "", "private-riff-work-fixture")
	if err != nil || !created {
		t.Fatalf("create private Riff created=%v err=%v", created, err)
	}
	return app, user, source, riff
}

func TestPrivateRiffDeckAndDocumentWorkStayOwnerOnlyAndBindPublicCheckpoint(t *testing.T) {
	cases := []struct {
		name      string
		request   string
		processID string
	}{
		{name: "deck", request: "Create a ten-slide investor presentation from this channel context", processID: packagingStudioProcessID},
		{name: "document", request: "Write a market opportunity report from this channel context", processID: documentReportProcessID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, user, source, riff := privateRiffWorkFixture(t)
			previousApp := kanbanApp
			kanbanApp = app
			t.Cleanup(func() { kanbanApp = previousApp })
			previousStarter := startGoalThreadAsync
			var launches atomic.Int64
			startGoalThreadAsync = func(*kanbanBoardApp, string) { launches.Add(1) }
			t.Cleanup(func() { startGoalThreadAsync = previousStarter })
			swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
				t.Fatalf("deterministic private Riff authored output called provider workflow %q", request.Workflow)
				return "", nil
			})

			operation := conversationTurnOperation{ID: "private-riff-" + tc.name + "-work", BodyDigest: sha256Hex([]byte(tc.request))}
			response, err := app.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), operation), user, riff.ID, tc.request, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			launched, ok := response["agentThread"].(scoutAgentThread)
			if !ok || launches.Load() != 1 || launched.Mode != "goal" {
				t.Fatalf("launches=%d response=%#v", launches.Load(), response)
			}
			metadata := launched.Artifact.Metadata
			if metadata["processId"] != tc.processID || metadata["originKind"] != agentThreadOriginPrivateThread ||
				metadata["originId"] != riff.ID || metadata["originSurface"] != "chat:"+riff.ID ||
				metadata["visibility"] != scoutChatVisibilityPrivate || normalizeAccountEmail(metadata["ownerEmail"]) != normalizeAccountEmail(user.Email) ||
				normalizeAccountEmail(metadata["requestedBy"]) != normalizeAccountEmail(user.Email) || artifactIsPublished(launched.Artifact) {
				t.Fatalf("private Riff artifact authority=%v", metadata)
			}
			other := accountStore().findUser("tim@shareability.com")
			if other == nil {
				other = &userAccount{Email: "tim@shareability.com", Name: "Tim"}
			}
			if app.artifactAuthorized(context.Background(), other, ACLReadContent, launched.Artifact) {
				t.Fatal("another channel member could read the private Riff artifact")
			}
			unauthorizedRequest := "Create another " + tc.name + " from this private Riff"
			unauthorizedOperation := conversationTurnOperation{ID: "private-riff-unauthorized-" + tc.name, BodyDigest: sha256Hex([]byte(unauthorizedRequest))}
			if unauthorized, unauthorizedErr := app.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), unauthorizedOperation), other, riff.ID, unauthorizedRequest, nil, ""); unauthorizedErr == nil || unauthorized != nil {
				t.Fatalf("non-owner launched private Riff work response=%#v err=%v", unauthorized, unauthorizedErr)
			}

			plan, ok := decodeGoalPlan(metadata["goalPlan"])
			if !ok || plan.RouteReceipt == nil {
				t.Fatalf("goal route receipt is missing: %v", metadata)
			}
			selection, err := app.goalRouteSourceSelection(*plan.RouteReceipt)
			if err != nil || selection.Digest != plan.RouteReceipt.SourceSelectionDigest {
				t.Fatalf("private Riff source selection=%+v err=%v", selection, err)
			}
			foundCheckpoint := false
			for _, evidence := range selection.InternalEvidenceSources {
				if strings.Contains(evidence.Text, "Country Golf checkpoint detail number 02") {
					foundCheckpoint = true
					break
				}
			}
			if !foundCheckpoint {
				t.Fatalf("public Riff checkpoint missing from exact work sources: %+v", selection.InternalEvidenceSources)
			}
			engine := newGoalEngine(app)
			if err := engine.prepareGoalRoute(&plan, launched.Artifact.ID); err != nil {
				t.Fatalf("private Riff process route was not executable: %v", err)
			}
			process, found := processByID(plan.ProcessID)
			if !found {
				t.Fatalf("private Riff process %q is not registered", plan.ProcessID)
			}
			if err := instantiateProcessPlan(process, &plan); err != nil || len(plan.Subtasks) == 0 {
				t.Fatalf("private Riff process could not instantiate: subtasks=%d err=%v", len(plan.Subtasks), err)
			}
			savedRiff := response["thread"].(scoutChatThreadRecord)
			workCard := savedRiff.Messages[len(savedRiff.Messages)-1]
			if workCard.Kind != "thread" || workCard.Thread == nil || workCard.Thread.ArtifactID != launched.Artifact.ID ||
				workCard.RiffEpisodeID != riff.Riff.ActiveEpisodeID || workCard.RiffCheckpointID != riff.Riff.CheckpointID {
				t.Fatalf("private Riff work projection=%+v", workCard)
			}
			unchangedSource, _, err := app.scoutChatThreadByID(user.Email, source.ID)
			if err != nil || len(unchangedSource.Messages) != len(source.Messages) {
				t.Fatalf("work mutated public source messages=%d want=%d err=%v", len(unchangedSource.Messages), len(source.Messages), err)
			}

			// Process outputs inherit the exact Riff destination and the standard
			// stage reporter returns their card to the Riff, never to its public
			// checkpoint channel.
			resultMetadata := goalRouteChildBindingMetadata(&plan)
			resultMetadata["goalParentId"] = launched.Artifact.ID
			resultMetadata["processId"] = plan.ProcessID
			resultMetadata["processStage"] = plan.ResultStageID
			resultMetadata["artifactContract"] = plan.ResultOutputContract
			result, appended, err := app.createOSArtifactWithMetadata("workflow", "Private Riff "+tc.name+" result", "Private result body", scoutParticipantName, resultMetadata)
			if err != nil || !appended {
				t.Fatalf("file private Riff result appended=%v err=%v", appended, err)
			}
			if result.Metadata["originId"] != riff.ID || result.Metadata["originSurface"] != "chat:"+riff.ID ||
				result.Metadata["visibility"] != scoutChatVisibilityPrivate || normalizeAccountEmail(result.Metadata["ownerEmail"]) != normalizeAccountEmail(user.Email) || artifactIsPublished(result) {
				t.Fatalf("private Riff result authority=%v", result.Metadata)
			}
			if app.artifactAuthorized(context.Background(), other, ACLReadContent, result) {
				t.Fatal("another channel member could read the private Riff process result")
			}
			app.postGoalStageMessage(launched.Artifact.ID, "Private Riff result", processRoleWriter, result.ID, "Private result is in")
			withResult, _, err := app.scoutChatThreadByID(user.Email, riff.ID)
			if err != nil {
				t.Fatal(err)
			}
			resultLanded := false
			for _, message := range withResult.Messages {
				if message.Thread != nil && message.Thread.ArtifactID == result.ID {
					resultLanded = message.Kind == "artifact" && message.RiffAuthority != nil && message.RiffEpisodeID == riff.Riff.ActiveEpisodeID
				}
			}
			if !resultLanded {
				t.Fatalf("private Riff result card did not land with Riff authority: %+v", withResult.Messages)
			}
			stillPublic, _, _ := app.scoutChatThreadByID(user.Email, source.ID)
			if len(stillPublic.Messages) != len(source.Messages) {
				t.Fatalf("private result card mutated public source messages=%d want=%d", len(stillPublic.Messages), len(source.Messages))
			}

			// Work cards and artifacts are never implicit publication payloads.
			if _, err := app.publishPrivateRiffConversation(user, riff.ID, "private-riff-work-share-all", privateRiffPublicationScopeAll, ""); err == nil || !strings.Contains(err.Error(), "ordinary completed Riff reply") {
				t.Fatalf("work card crossed publication boundary err=%v", err)
			}
			stillUnchanged, _, _ := app.scoutChatThreadByID(user.Email, source.ID)
			if len(stillUnchanged.Messages) != len(source.Messages) {
				t.Fatalf("failed implicit publication mutated source messages=%d", len(stillUnchanged.Messages))
			}

			// The existing explicit publication control can still move one ordinary
			// human reply, proving that privacy is a deliberate boundary, not a dead end.
			publishable := scoutChatMessageRecord{
				ID: "private-riff-publishable-" + tc.name, Kind: "message", Role: "user", Text: "Approved private summary for the source channel.",
				AuthorName: user.Name, AuthorEmail: user.Email, CreatedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano),
			}
			if _, err := app.commitScoutChatThreadMessages(user.Email, riff.ID, publishable); err != nil {
				t.Fatal(err)
			}
			published, err := app.publishPrivateRiffConversation(user, riff.ID, "private-riff-explicit-publish-"+tc.name, privateRiffPublicationScopeReply, publishable.ID)
			if err != nil || !published.OK || published.PublishedCount != 1 {
				t.Fatalf("explicit publication=%+v err=%v", published, err)
			}
			afterExplicit, _, _ := app.scoutChatThreadByID(user.Email, source.ID)
			if len(afterExplicit.Messages) != len(source.Messages)+1 {
				t.Fatalf("explicit publication messages=%d want=%d", len(afterExplicit.Messages), len(source.Messages)+1)
			}

			// Later edits to the public checkpoint invalidate every downstream stage.
			afterExplicit.Messages[1].Text = "edited source after the private work launch"
			afterExplicit.UpdatedAt = time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339Nano)
			if err := app.saveScoutChatThread(afterExplicit); err != nil {
				t.Fatal(err)
			}
			if err := app.verifyGoalRouteReceipt(&plan, *plan.RouteReceipt); err == nil || !strings.Contains(err.Error(), "source") {
				t.Fatalf("edited public checkpoint remained executable err=%v", err)
			}
		})
	}
}

func TestPrivateRiffImageGenerationFilesOnlyToRiffAndCannotImplicitlyPublish(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "private-riff-image-test")
	t.Setenv("OPENAI_IMAGE_MODEL", "")
	app, user, source, riff := privateRiffWorkFixture(t)
	previousApp := kanbanApp
	kanbanApp = app
	app.apiKey = "private-riff-image-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	previousRunner := startScoutChatImageAsyncWithPending
	startScoutChatImageAsyncWithPending = func(runApp *kanbanBoardApp, threadID, ownerEmail, prompt, createdBy, pendingMessageID string) {
		runApp.runScoutChatImageGenerationForPending(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousRunner })
	var providerCalls atomic.Int64
	withFakeImagesAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("private-riff-image"))}},
			"output_format": "png",
		})
	})
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "image", Prompt: "a premium editorial western-culture creator portrait at golden hour",
		}), nil
	})
	request := "Generate a premium image for the western creator concept in this Riff"
	operation := conversationTurnOperation{ID: "private-riff-image-work", BodyDigest: sha256Hex([]byte(request))}
	response, err := app.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), operation), user, riff.ID, request, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, proposed := response["proposal"]; proposed || providerCalls.Load() != 1 {
		t.Fatalf("providerCalls=%d response=%#v", providerCalls.Load(), response)
	}
	saved, _, err := app.scoutChatThreadByID(user.Email, riff.ID)
	if err != nil {
		t.Fatal(err)
	}
	var image *scoutChatMessageRecord
	for index := range saved.Messages {
		if saved.Messages[index].Kind == scoutChatMessageKindImage {
			image = &saved.Messages[index]
		}
	}
	if image == nil || image.Image == nil || image.RiffAuthority == nil || image.RiffEpisodeID != riff.Riff.ActiveEpisodeID || image.RiffCheckpointID != riff.Riff.CheckpointID {
		t.Fatalf("private Riff image result=%+v messages=%+v", image, saved.Messages)
	}
	filed, ok := app.osArtifactByID(image.Image.ArtifactID)
	if !ok || filed.Metadata["source"] != "chat_image" || filed.Metadata["originSurface"] != "chat:"+riff.ID ||
		filed.Metadata["visibility"] != scoutChatVisibilityPrivate || normalizeAccountEmail(filed.Metadata["ownerEmail"]) != normalizeAccountEmail(user.Email) || artifactIsPublished(filed) {
		t.Fatalf("private Riff image artifact=%+v found=%v", filed, ok)
	}
	other := accountStore().findUser("tim@shareability.com")
	if other == nil {
		other = &userAccount{Email: "tim@shareability.com", Name: "Tim"}
	}
	if app.artifactAuthorized(context.Background(), other, ACLReadContent, filed) {
		t.Fatal("another channel member could read the private Riff image artifact")
	}
	unchangedSource, _, _ := app.scoutChatThreadByID(user.Email, source.ID)
	if len(unchangedSource.Messages) != len(source.Messages) {
		t.Fatalf("image generation mutated source messages=%d want=%d", len(unchangedSource.Messages), len(source.Messages))
	}
	if _, err := app.publishPrivateRiffConversation(user, riff.ID, "private-riff-image-implicit-publish", privateRiffPublicationScopeReply, image.ID); err == nil || !strings.Contains(err.Error(), "ordinary completed Riff reply") {
		t.Fatalf("generated image crossed publication boundary err=%v", err)
	}
}

func TestPrivateRiffImageGenerationReauthorizesSourceBeforeProviderSpend(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "private-riff-image-fence-test")
	app, user, source, riff := privateRiffWorkFixture(t)
	request := scoutChatMessageRecord{
		ID: "private-riff-image-source-request", Kind: "message", Role: "user", Text: "Generate the image from this checkpoint.",
		AuthorName: user.Name, AuthorEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	pending := scoutChatMessageRecord{
		ID: "private-riff-image-source-pending", Kind: scoutChatMessageKindImagePending, Role: "scout", AuthorName: scoutParticipantName,
		Text: "generating image…", CreatedAt: time.Now().UTC().Add(time.Nanosecond).Format(time.RFC3339Nano), CausedByMessageID: request.ID,
		ImageGeneration: &scoutChatImageGenerationState{
			Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "a source-bound image",
			RequestedByEmail: user.Email, RequestedByName: user.Name,
		},
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, riff.ID, request, pending); err != nil {
		t.Fatal(err)
	}
	source.Messages[1].Text = "changed before the provider call"
	source.UpdatedAt = time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(source); err != nil {
		t.Fatal(err)
	}
	var providerCalls atomic.Int64
	previousCreate := createScoutChatImage
	createScoutChatImage = func(context.Context, string, openAIImageOptions) (string, string, error) {
		providerCalls.Add(1)
		return strings.Repeat("a", 64), "image/png", nil
	}
	t.Cleanup(func() { createScoutChatImage = previousCreate })
	app.runScoutChatImageGenerationForPending(riff.ID, user.Email, pending.ImageGeneration.Prompt, user.Name, pending.ID)
	if providerCalls.Load() != 0 {
		t.Fatalf("changed public checkpoint spent provider calls=%d", providerCalls.Load())
	}
	for _, artifact := range app.osArtifactsSnapshot(0) {
		if artifact.Metadata["generationId"] == pending.ID {
			t.Fatalf("changed public checkpoint filed image artifact %s", artifact.ID)
		}
	}
}
