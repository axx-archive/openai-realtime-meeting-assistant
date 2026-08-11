package main

// Card 096 — the single-shot concept render behind a private-thread image
// request. These tests pin the direct-feed law: the router door is gated on
// OpenAI being configured, an image ask gets one hidden prompt-optimization
// turn and immediately earns a generating pill, the high-quality gpt-image-2
// call files a design artifact with a kind=image asset + commits an
// inline-renderable message, the live prod 429 (insufficient_quota) lands a
// friendly error bubble instead of the raw upstream body, and legacy proposal
// cards remain resolvable.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
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

// A render requested from a nested conversation must finish in that same
// conversation. The provider callback is asynchronous, so the immutable reply
// ancestry has to travel through the durable pending message rather than being
// inferred from whichever channel view is open when the image lands.
func TestScoutChatImageCompletionPreservesRequestedReplyThread(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	channel, err := app.createScoutChatThread(user.Email, user.Name, "Design review", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	root := scoutChatMessageRecord{
		ID: "design-review-root", Kind: "message", Role: "user", Text: "Keep the campaign imagery in this thread.",
		AuthorName: user.Name, AuthorEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	reply := &scoutChatReplyRef{MessageID: root.ID, AuthorName: user.Name, AuthorEmail: user.Email, Text: root.Text}
	pending := scoutChatMessageRecord{
		ID: "design-review-image-pending", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ReplyTo: reply,
		ImageGeneration: &scoutChatImageGenerationState{
			Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "a warm editorial campaign portrait",
			RequestedByEmail: user.Email, RequestedByName: user.Name,
		},
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, channel.ID, root, pending); err != nil {
		t.Fatal(err)
	}

	withFakeImagesAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("thread-bound-image"))}},
			"output_format": "png",
		})
	})
	app.runScoutChatImageGenerationForPending(channel.ID, user.Email, pending.ImageGeneration.Prompt, user.Name, pending.ID)

	saved, _, err := app.scoutChatThreadByID(user.Email, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range saved.Messages {
		if message.Kind != scoutChatMessageKindImage {
			continue
		}
		if message.ReplyTo == nil || *message.ReplyTo != *reply {
			t.Fatalf("image reply=%#v, want exact originating thread %#v", message.ReplyTo, reply)
		}
		return
	}
	t.Fatalf("messages=%#v, want completed image in the originating reply thread", saved.Messages)
}

func TestScoutChatImageCompletionFencesDeletedReplyTargetBeforeFinalWrite(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Scout", "")
	if err != nil {
		t.Fatal(err)
	}
	root := scoutChatMessageRecord{ID: "deleted-image-root", Kind: "message", Role: "user", Text: "Put the image under this message.", AuthorName: user.Name, AuthorEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	pending := scoutChatMessageRecord{
		ID: "deleted-image-pending", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ReplyTo:         &scoutChatReplyRef{MessageID: root.ID, AuthorName: user.Name, AuthorEmail: user.Email, Text: root.Text},
		ImageGeneration: &scoutChatImageGenerationState{Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "a private campaign image", RequestedByEmail: user.Email, RequestedByName: user.Name},
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, root, pending); err != nil {
		t.Fatal(err)
	}
	withFakeImagesAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		lock := app.scoutChatThreadLock(thread.ID)
		lock.Lock()
		current, _, loadErr := app.scoutChatThreadByID(user.Email, thread.ID)
		if loadErr != nil {
			lock.Unlock()
			t.Errorf("load thread during provider response: %v", loadErr)
			return
		}
		filtered := current.Messages[:0]
		for _, message := range current.Messages {
			if message.ID != root.ID {
				filtered = append(filtered, message)
			}
		}
		current.Messages = filtered
		if saveErr := app.saveScoutChatThread(current); saveErr != nil {
			t.Errorf("delete root during provider response: %v", saveErr)
		}
		lock.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("late-image"))}},
			"output_format": "png",
		})
	})
	app.runScoutChatImageGenerationForPending(thread.ID, user.Email, pending.ImageGeneration.Prompt, user.Name, pending.ID)

	saved, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hasScoutChatImageMessage(saved.Messages) || hasScoutChatImagePendingMessage(saved.Messages) {
		t.Fatalf("messages=%#v, late completion must not become an orphaned top-level image", saved.Messages)
	}
	for _, artifact := range app.osArtifactsSnapshot(0) {
		if artifact.Metadata["source"] == "chat_image" {
			t.Fatalf("late denied image left artifact %s", artifact.ID)
		}
	}
}

func TestScoutChatImageRecoveryDeliversFiledResultWithoutSecondProviderCall(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Durable image", "")
	if err != nil {
		t.Fatal(err)
	}
	pending := scoutChatMessageRecord{ID: "durable-image-pending", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ImageGeneration: &scoutChatImageGenerationState{Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "durable concept", RequestedByEmail: user.Email, RequestedByName: user.Name}}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, pending); err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	withFakeImagesAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		providerCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("durable-image"))}}, "output_format": "png"})
	})
	previousSave := saveScoutChatImageThread
	failDelivery := true
	saveScoutChatImageThread = func(candidate *kanbanBoardApp, next scoutChatThreadRecord) error {
		if failDelivery && !hasScoutChatImagePendingMessage(next.Messages) && hasScoutChatImageMessage(next.Messages) {
			failDelivery = false
			return errors.New("injected final delivery failure")
		}
		return candidate.saveScoutChatThread(next)
	}
	t.Cleanup(func() { saveScoutChatImageThread = previousSave })
	app.runScoutChatImageGenerationForPending(thread.ID, user.Email, pending.ImageGeneration.Prompt, user.Name, pending.ID)
	if providerCalls != 1 {
		t.Fatalf("provider calls=%d, want one", providerCalls)
	}
	saveScoutChatImageThread = previousSave
	app.recoverScoutChatImageGenerations()
	saved, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if providerCalls != 1 || hasScoutChatImagePendingMessage(saved.Messages) || !hasScoutChatImageMessage(saved.Messages) {
		t.Fatalf("providerCalls=%d messages=%#v, restart must deliver the filed result exactly once", providerCalls, saved.Messages)
	}
}

func TestScoutChatImageCleanupRetriesDeleteAndPendingPersistenceWithoutProvider(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Cleanup image", "")
	if err != nil {
		t.Fatal(err)
	}
	root := scoutChatMessageRecord{ID: "cleanup-root", Kind: "message", Role: "user", Text: "nested", AuthorEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	pending := scoutChatMessageRecord{ID: "cleanup-pending", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ReplyTo: &scoutChatReplyRef{MessageID: root.ID, AuthorEmail: user.Email, Text: root.Text}, ImageGeneration: &scoutChatImageGenerationState{Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "cleanup concept", RequestedByEmail: user.Email, RequestedByName: user.Name}}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, root, pending); err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	withFakeImagesAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		providerCalls++
		lock := app.scoutChatThreadLock(thread.ID)
		lock.Lock()
		current, _, _ := app.scoutChatThreadByID(user.Email, thread.ID)
		filtered := current.Messages[:0]
		for _, message := range current.Messages {
			if message.ID != root.ID {
				filtered = append(filtered, message)
			}
		}
		current.Messages = filtered
		_ = app.saveScoutChatThread(current)
		lock.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("cleanup-image"))}}, "output_format": "png"})
	})
	previousDelete := deleteScoutChatImageArtifact
	deleteFailures := 1
	deleteScoutChatImageArtifact = func(candidate *kanbanBoardApp, artifactID string) (meetingMemoryEntry, []scopedRoomDeliveryAcknowledgement, bool, error) {
		if deleteFailures > 0 {
			deleteFailures--
			return meetingMemoryEntry{}, nil, false, errors.New("injected artifact delete failure")
		}
		return candidate.deleteOSArtifactAndEmit(artifactID)
	}
	t.Cleanup(func() { deleteScoutChatImageArtifact = previousDelete })
	app.runScoutChatImageGenerationForPending(thread.ID, user.Email, pending.ImageGeneration.Prompt, user.Name, pending.ID)
	if providerCalls != 1 || deleteFailures != 0 {
		t.Fatalf("providerCalls=%d deleteFailures=%d", providerCalls, deleteFailures)
	}
	deleteScoutChatImageArtifact = previousDelete
	previousSave := saveScoutChatImageThread
	failRemove := true
	saveScoutChatImageThread = func(candidate *kanbanBoardApp, next scoutChatThreadRecord) error {
		if failRemove && !hasScoutChatImagePendingMessage(next.Messages) {
			failRemove = false
			return errors.New("injected pending removal failure")
		}
		return candidate.saveScoutChatThread(next)
	}
	t.Cleanup(func() { saveScoutChatImageThread = previousSave })
	app.recoverScoutChatImageGenerations()
	saved, _, _ := app.scoutChatThreadByID(user.Email, thread.ID)
	if !hasScoutChatImagePendingMessage(saved.Messages) {
		t.Fatalf("messages=%#v, failed pending removal must retain the cleanup journal", saved.Messages)
	}
	saveScoutChatImageThread = previousSave
	app.recoverScoutChatImageGenerations()
	saved, _, _ = app.scoutChatThreadByID(user.Email, thread.ID)
	if providerCalls != 1 || hasScoutChatImagePendingMessage(saved.Messages) || hasScoutChatImageMessage(saved.Messages) {
		t.Fatalf("providerCalls=%d messages=%#v, cleanup restart must finish without provider replay", providerCalls, saved.Messages)
	}
	for _, artifact := range app.osArtifactsSnapshot(0) {
		if artifact.Metadata["generationId"] == pending.ID {
			t.Fatalf("cleanup left image artifact %s", artifact.ID)
		}
	}
}

func TestScoutChatImageLegacyGeneratingRecoveryNeverRepeatsProvider(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Legacy image", "")
	if err != nil {
		t.Fatal(err)
	}
	pending := scoutChatMessageRecord{ID: "legacy-generating", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ImageGeneration: &scoutChatImageGenerationState{Status: scoutChatImageGenerationStatusGenerating, Prompt: "legacy ambiguous", RequestedByEmail: user.Email, RequestedByName: user.Name}}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, pending); err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	previousCreate := createScoutChatImage
	createScoutChatImage = func(context.Context, string, openAIImageOptions) (string, string, error) {
		providerCalls++
		return "", "", nil
	}
	t.Cleanup(func() { createScoutChatImage = previousCreate })
	app.recoverScoutChatImageGenerations()
	saved, _, _ := app.scoutChatThreadByID(user.Email, thread.ID)
	if providerCalls != 0 || hasScoutChatImagePendingMessage(saved.Messages) {
		t.Fatalf("providerCalls=%d messages=%#v, legacy ambiguous row must settle without provider replay", providerCalls, saved.Messages)
	}
}

func TestScoutChatImagePartialArtifactCleanupSurvivesAttachAndDeleteFailure(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Partial image", "")
	if err != nil {
		t.Fatal(err)
	}
	pending := scoutChatMessageRecord{ID: "partial-image-pending", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ImageGeneration: &scoutChatImageGenerationState{Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "partial concept", RequestedByEmail: user.Email, RequestedByName: user.Name}}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, pending); err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	withFakeImagesAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		providerCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("partial-image"))}}, "output_format": "png"})
	})
	previousAppend := appendScoutChatImageAsset
	appendScoutChatImageAsset = func(*kanbanBoardApp, string, artifactAsset) (meetingMemoryEntry, error) {
		return meetingMemoryEntry{}, errors.New("injected asset attach failure")
	}
	t.Cleanup(func() { appendScoutChatImageAsset = previousAppend })
	previousDelete := deleteScoutChatImageArtifact
	deleteScoutChatImageArtifact = func(*kanbanBoardApp, string) (meetingMemoryEntry, []scopedRoomDeliveryAcknowledgement, bool, error) {
		// A nil no-op is still not a deletion receipt. The journal must remain.
		return meetingMemoryEntry{}, nil, false, nil
	}
	t.Cleanup(func() { deleteScoutChatImageArtifact = previousDelete })
	app.runScoutChatImageGenerationForPending(thread.ID, user.Email, pending.ImageGeneration.Prompt, user.Name, pending.ID)
	saved, _, _ := app.scoutChatThreadByID(user.Email, thread.ID)
	journal, ok := app.scoutChatImagePending(user.Email, thread.ID, pending.ID)
	if providerCalls != 1 || !ok || journal.ImageGeneration == nil || scoutChatImagePhase(journal.ImageGeneration) != scoutChatImagePhaseCleanupRequired {
		t.Fatalf("providerCalls=%d messages=%#v, partial artifact must retain cleanup journal", providerCalls, saved.Messages)
	}
	if _, present := app.osArtifactByID(journal.ImageGeneration.ArtifactID); !present {
		t.Fatal("injected nil delete unexpectedly removed the partial artifact")
	}
	appendScoutChatImageAsset = previousAppend
	deleteScoutChatImageArtifact = previousDelete
	app.recoverScoutChatImageGenerations()
	saved, _, _ = app.scoutChatThreadByID(user.Email, thread.ID)
	if providerCalls != 1 || hasScoutChatImagePendingMessage(saved.Messages) || hasScoutChatImageMessage(saved.Messages) {
		t.Fatalf("providerCalls=%d messages=%#v, recovery must clean and settle once without provider replay", providerCalls, saved.Messages)
	}
	for _, artifact := range app.osArtifactsSnapshot(0) {
		if artifact.Metadata["generationId"] == pending.ID {
			t.Fatalf("partial artifact %s survived verified cleanup", artifact.ID)
		}
	}
}

func TestScoutChatImageConcurrentGeneratedRecoveryFilesAndDeliversOnce(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Concurrent image", "")
	if err != nil {
		t.Fatal(err)
	}
	pending := scoutChatMessageRecord{ID: "concurrent-generated", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ImageGeneration: &scoutChatImageGenerationState{Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseGenerated, PhaseGeneration: 2, Prompt: "concurrent concept", RequestedByEmail: user.Email, RequestedByName: user.Name, ResultRef: strings.Repeat("a", 64), ResultMime: "image/png"}}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, pending); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 2)
	for range 2 {
		go func() {
			app.resumeScoutChatImagePending(user.Email, thread.ID, pending.ID, user.Name)
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	saved, _, _ := app.scoutChatThreadByID(user.Email, thread.ID)
	imageCount := 0
	for _, message := range saved.Messages {
		if message.Kind == scoutChatMessageKindImage && message.Image != nil && message.Image.GenerationID == pending.ID {
			imageCount++
		}
	}
	artifactCount := 0
	for _, artifact := range app.osArtifactsSnapshot(0) {
		if artifact.Metadata["generationId"] == pending.ID {
			artifactCount++
		}
	}
	if imageCount != 1 || artifactCount != 1 || hasScoutChatImagePendingMessage(saved.Messages) {
		t.Fatalf("images=%d artifacts=%d messages=%#v, want one exact terminal", imageCount, artifactCount, saved.Messages)
	}
}

func TestScoutChatImageArtifactReconciliationRejectsTamperedAuthorityAndResult(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Binding image", "")
	if err != nil {
		t.Fatal(err)
	}
	prompt, ref, mime := "bound concept", strings.Repeat("b", 64), "image/png"
	cases := []struct {
		name    string
		updates map[string]string
		asset   artifactAsset
	}{
		{name: "owner", updates: map[string]string{"ownerEmail": "other@example.com"}, asset: artifactAsset{Ref: ref, Mime: mime, Kind: "image"}},
		{name: "visibility", updates: map[string]string{"visibility": scoutChatVisibilityPublic}, asset: artifactAsset{Ref: ref, Mime: mime, Kind: "image"}},
		{name: "published", updates: map[string]string{"published": "true"}, asset: artifactAsset{Ref: ref, Mime: mime, Kind: "image"}},
		{name: "result", asset: artifactAsset{Ref: strings.Repeat("c", 64), Mime: mime, Kind: "image"}},
	}
	for index, tc := range cases {
		generationID := fmt.Sprintf("tampered-generation-%d", index)
		metadata := map[string]string{"type": artifactTypeMarkdown, "source": "chat_image", "imagePrompt": prompt, "status": artifactStatusComplete, "published": "false", "originSurface": "chat:" + thread.ID, "visibility": normalizeScoutChatVisibility(thread.Visibility), "ownerEmail": normalizeAccountEmail(thread.OwnerEmail), "generationId": generationID}
		artifact, appended, err := app.createOSArtifactWithMetadata("design", scoutChatImageTitle(prompt), scoutChatImageArtifactBody(prompt, ref, mime), user.Name, metadata)
		if err != nil || !appended {
			t.Fatalf("%s fixture: appended=%v err=%v", tc.name, appended, err)
		}
		if _, err := app.appendArtifactAsset(artifact.ID, tc.asset); err != nil {
			t.Fatalf("%s asset fixture: %v", tc.name, err)
		}
		if len(tc.updates) > 0 {
			if _, _, err := app.memory.updateOSArtifactMetadata(artifact.ID, tc.updates); err != nil {
				t.Fatalf("%s metadata fixture: %v", tc.name, err)
			}
		}
		if _, _, err := app.fileScoutChatImageArtifact(thread, generationID, prompt, ref, mime, user.Name); !errors.Is(err, errScoutChatImageArtifactBinding) {
			t.Fatalf("%s reconciliation err=%v, want exact binding denial", tc.name, err)
		}
	}
}

func TestScoutChatImageFilingLockCardinalityIsBoundedPerThread(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Bounded image locks", "")
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	before := len(app.chatThreadLocks)
	app.mu.Unlock()
	for index := range 24 {
		generationID := fmt.Sprintf("bounded-generation-%d", index)
		ref := fmt.Sprintf("%064x", index+1)
		if _, _, err := app.fileScoutChatImageArtifact(thread, generationID, "bounded concept", ref, "image/png", user.Name); err != nil {
			t.Fatalf("generation %d: %v", index, err)
		}
	}
	app.mu.Lock()
	after := len(app.chatThreadLocks)
	app.mu.Unlock()
	if after > before+1 {
		t.Fatalf("chat filing locks grew from %d to %d for one thread", before, after)
	}
}

// A propose_image routing turn is an internal prompt-optimization result. It
// commits one generating pill and runs the image call immediately: there is no
// persisted proposal card and no second confirmation.
func TestScoutChatRouterImageResultStartsDirectGeneration(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "openai-router-test")
	t.Setenv("OPENAI_IMAGE_MODEL", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-router-test"
	t.Cleanup(func() { kanbanApp = previousApp })

	imageBytes := []byte("direct-router-image")
	var captured openAIImagePayload
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode image request: %v", err)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageBytes)}},
			"output_format": "png",
		})
	})
	previousRunner := startScoutChatImageAsyncWithPending
	startScoutChatImageAsyncWithPending = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
		app.runScoutChatImageGenerationForPending(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousRunner })
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Model != scoutImageDirectionModel() || request.ReasoningEffort != scoutImageDirectionReasoningEffort() {
			t.Fatalf("prompt optimizer route=%q/%q, want %q/%q", request.Model, request.ReasoningEffort, scoutImageDirectionModel(), scoutImageDirectionReasoningEffort())
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "image", Prompt: "a rooftop crowd of the crew mid-laugh, hats in the air", Title: "Rooftop celebration",
		}), nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	root := scoutChatMessageRecord{ID: "router-image-root", Kind: "message", Role: "user", Text: "Keep the generated campaign in this conversation.", AuthorEmail: user.Email, AuthorName: user.Name, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := kanbanApp.commitScoutChatThreadMessages(user.Email, private.ID, root); err != nil {
		t.Fatal(err)
	}

	text := "let's whip up a rooftop shot of the crew celebrating"
	response, err := kanbanApp.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), user, private.ID, text, nil, "", root.ID, "")
	if err != nil {
		t.Fatalf("append routed message: %v", err)
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("response keys=%v — image requests must not persist a confirmation card", responseKeys(response))
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok {
		t.Fatalf("answer type=%T, want a generating-pending message", response["answer"])
	}
	if answer.Kind != scoutChatMessageKindImagePending || answer.ImageGeneration == nil || answer.ImageGeneration.Status != scoutChatImageGenerationStatusGenerating {
		t.Fatalf("answer=%#v, want image_pending/generating", answer)
	}
	if captured.Prompt != "a rooftop crowd of the crew mid-laugh, hats in the air" {
		t.Fatalf("image prompt=%q, want the optimized router prompt", captured.Prompt)
	}
	if captured.Model != defaultOpenAIImageModel || captured.Quality != defaultOpenAIImageQuality || captured.Size != defaultOpenAIImageSize {
		t.Fatalf("image defaults=%+v, want model=%q size=%q quality=%q", captured, defaultOpenAIImageModel, defaultOpenAIImageSize, defaultOpenAIImageQuality)
	}
	if generation, ok := response["imageGeneration"].(map[string]any); !ok || generation["status"] != scoutChatImageGenerationStatusGenerating {
		t.Fatalf("imageGeneration=%#v, want generating status", response["imageGeneration"])
	}
	if response["providerCalls"] != 1 {
		t.Fatalf("providerCalls=%v, want the completed prompt-optimization call", response["providerCalls"])
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
		if saved.Messages[index].Kind == scoutChatMessageKindProposal {
			t.Fatalf("messages=%#v, want no image proposal card", saved.Messages)
		}
	}
	if imageMessage == nil || imageMessage.Image == nil || imageMessage.Image.Prompt != captured.Prompt {
		t.Fatalf("messages=%#v, want final image with optimized prompt", saved.Messages)
	}
	if imageMessage.ReplyTo == nil || imageMessage.ReplyTo.MessageID != root.ID {
		t.Fatalf("image reply=%#v, want real router request bound to root %s", imageMessage.ReplyTo, root.ID)
	}
	artifacts := kanbanApp.osArtifactsSnapshot(0)
	if len(artifacts) == 0 {
		t.Fatal("direct image generation must file a design artifact")
	}
	filed := artifacts[len(artifacts)-1]
	if artifactIsPublished(filed) || filed.Metadata["originSurface"] != "chat:"+private.ID || filed.Metadata["visibility"] != scoutChatVisibilityPrivate || filed.Metadata["ownerEmail"] != normalizeAccountEmail(user.Email) {
		t.Fatalf("image artifact authority=%#v, want unpublished exact private-thread projection", filed.Metadata)
	}
}

// If prompt optimization is unavailable, an explicit image ask still follows
// the direct-feed contract using the user's wording as the conservative prompt
// fallback. It never turns into a confirmation card or a generic inline answer.
func TestScoutChatImageFallbackGeneratesWhenRouterUnavailable(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	t.Setenv("OPENAI_IMAGE_MODEL", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "test-image-key"
	t.Cleanup(func() { kanbanApp = previousApp })

	imageBytes := []byte("fallback-image")
	var captured openAIImagePayload
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode image request: %v", err)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageBytes)}},
			"output_format": "png",
		})
	})
	previousRunner := startScoutChatImageAsyncWithPending
	startScoutChatImageAsyncWithPending = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
		app.runScoutChatImageGenerationForPending(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousRunner })
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		return "", errors.New("router unavailable")
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
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != scoutChatMessageKindImagePending {
		t.Fatalf("answer=%#v, want an image_pending response", response["answer"])
	}
	if captured.Prompt != text {
		t.Fatalf("fallback prompt=%q, want the literal ask", captured.Prompt)
	}
	saved, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	for _, message := range saved.Messages {
		if message.Kind == scoutChatMessageKindProposal {
			t.Fatalf("messages=%#v, want no proposal card", saved.Messages)
		}
	}
	if !hasScoutChatImageMessage(saved.Messages) {
		t.Fatalf("messages=%#v, want a generated image", saved.Messages)
	}
}

func TestScoutChatImagePublicRouterFailureLaunchesNothing(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "test-image-key"
	t.Cleanup(func() { kanbanApp = previousApp })

	var captured openAIImagePayload
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode image request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("public-fallback-image"))}},
			"output_format": "png",
		})
	})
	previousRunner := startScoutChatImageAsyncWithPending
	startScoutChatImageAsyncWithPending = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
		app.runScoutChatImageGenerationForPending(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousRunner })
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		return "", errors.New("router unavailable")
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	channel, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "Image review", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create public thread: %v", err)
	}
	request := "@scout make an image of a warm mountain studio at sunset"
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, request, nil, ""); err != nil {
		t.Fatalf("append public image ask: %v", err)
	}
	if captured.Prompt != "" {
		t.Fatalf("public router failure launched image prompt=%q", captured.Prompt)
	}
	saved, _, readErr := kanbanApp.scoutChatThreadByID(user.Email, channel.ID)
	if readErr != nil || len(saved.Messages) != 2 || saved.Messages[1].IntentOutcome != string(conversationIntentApprovalRequired) || saved.Messages[1].Proposal == nil {
		t.Fatalf("saved=%#v err=%v, want public-audience approval and no image call", saved.Messages, readErr)
	}
}

func hasScoutChatImageMessage(messages []scoutChatMessageRecord) bool {
	for _, message := range messages {
		if message.Kind == scoutChatMessageKindImage && message.Image != nil {
			return true
		}
	}
	return false
}

func TestScoutChatImageTimeoutIsFriendlyAndRetryable(t *testing.T) {
	err := fmt.Errorf("create OpenAI image: Post %q: %w", openAIImagesURL, context.DeadlineExceeded)
	detail := scoutChatImageErrorDetail(err)
	if detail != "image generation took too long; please try the request again" {
		t.Fatalf("timeout detail=%q, want the retryable user message", detail)
	}
	for _, leaked := range []string{"api.openai.com", "context deadline exceeded", "Post \""} {
		if strings.Contains(detail, leaked) {
			t.Fatalf("timeout detail leaked transport text %q: %s", leaked, detail)
		}
	}
	if scoutChatImageGenerationTimeout <= openAIImageProviderTimeout {
		t.Fatalf("outer image timeout=%s must exceed provider timeout=%s", scoutChatImageGenerationTimeout, openAIImageProviderTimeout)
	}
}

func TestScoutChatImageRegenerateReplacesUnsavedRender(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	t.Setenv("OPENAI_IMAGE_MODEL", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	imageBytes := []byte("replacement-image")
	var captured openAIImagePayload
	withFakeImagesAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode image request: %v", err)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageBytes)}},
			"output_format": "png",
		})
	})
	previousRunner := startScoutChatImageAsyncWithPending
	startScoutChatImageAsyncWithPending = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
		app.runScoutChatImageGenerationForPending(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	oldPrompt := "a warm editorial golf clubhouse at sunset"
	oldRef, err := putBlob([]byte("old-image"), "image/png")
	if err != nil {
		t.Fatalf("put old image: %v", err)
	}
	oldArtifact, appended, err := kanbanApp.createOSArtifactWithMetadata("design", "Old render", scoutChatImageArtifactBody(oldPrompt, oldRef, "image/png"), user.Name, map[string]string{
		"type": "markdown", "source": "chat_image", "imagePrompt": oldPrompt, "status": artifactStatusComplete, "published": "true",
	})
	if err != nil || !appended {
		t.Fatalf("create old image artifact appended=%v err=%v", appended, err)
	}
	oldArtifact, err = kanbanApp.appendArtifactAsset(oldArtifact.ID, artifactAsset{Ref: oldRef, Mime: "image/png", Name: "old-render.png", Kind: "image"})
	if err != nil {
		t.Fatalf("append old image asset: %v", err)
	}
	oldMessageID := fmt.Sprintf("scout-chat-message-old-image-%d", time.Now().UTC().UnixNano())
	root := scoutChatMessageRecord{
		ID: "regenerate-thread-root", Kind: "message", Role: "user", Text: "Keep revisions under this image direction.",
		AuthorName: user.Name, AuthorEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	oldMessage := scoutChatMessageRecord{
		ID: oldMessageID, Kind: scoutChatMessageKindImage, Role: "scout", Text: "here's the concept render.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ReplyTo:   &scoutChatReplyRef{MessageID: root.ID, AuthorName: user.Name, AuthorEmail: user.Email, Text: root.Text},
		Image:     &scoutChatImageRef{Ref: oldRef, Mime: "image/png", Name: "old-render.png", ArtifactID: oldArtifact.ID, Prompt: oldPrompt},
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(user.Email, private.ID, root, oldMessage); err != nil {
		t.Fatalf("commit old image: %v", err)
	}

	newPrompt := "a crisp editorial golf clubhouse at golden hour, wide composition"
	response, err := kanbanApp.regenerateScoutChatImage(context.Background(), user, private.ID, oldMessageID, newPrompt)
	if err != nil {
		t.Fatalf("regenerate image: %v", err)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != scoutChatMessageKindImagePending {
		t.Fatalf("answer=%#v, want generating image_pending message", response["answer"])
	}
	if captured.Prompt != newPrompt {
		t.Fatalf("replacement prompt=%q, want edited prompt", captured.Prompt)
	}
	if _, exists := kanbanApp.osArtifactByID(oldArtifact.ID); exists {
		t.Fatal("unsaved superseded chat image artifact still exists after regenerate")
	}

	saved, _, err := kanbanApp.scoutChatThreadByID(user.Email, private.ID)
	if err != nil {
		t.Fatalf("reload regenerated thread: %v", err)
	}
	if scoutChatMessageIndex(saved, oldMessageID) >= 0 {
		t.Fatalf("messages=%#v, old image message must be removed", saved.Messages)
	}
	if hasScoutChatImagePendingMessage(saved.Messages) {
		t.Fatalf("messages=%#v, synchronous replacement must resolve the generating pill", saved.Messages)
	}
	var replacement *scoutChatMessageRecord
	for index := range saved.Messages {
		if saved.Messages[index].Kind == scoutChatMessageKindImage {
			replacement = &saved.Messages[index]
		}
	}
	if replacement == nil || replacement.Image == nil || replacement.Image.Prompt != newPrompt || !scoutChatImageReplyEqual(replacement.ReplyTo, oldMessage.ReplyTo) {
		t.Fatalf("messages=%#v, want replacement image with edited prompt", saved.Messages)
	}
}

func TestScoutChatImageRegenerateFailurePreservesPriorRender(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	withFakeImagesAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"insufficient_quota","message":"quota exhausted"}}`))
	})
	previousRunner := startScoutChatImageAsyncWithPending
	startScoutChatImageAsyncWithPending = func(app *kanbanBoardApp, threadID, ownerEmail, prompt, createdBy, pendingMessageID string) {
		app.runScoutChatImageGenerationForPending(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Scout", "")
	if err != nil {
		t.Fatal(err)
	}
	artifact, appended, err := app.createOSArtifactWithMetadata("design", "Prior render", "prior", user.Name, map[string]string{
		"type": "markdown", "source": "chat_image", "status": artifactStatusComplete, "published": "true",
	})
	if err != nil || !appended {
		t.Fatalf("create prior artifact appended=%v err=%v", appended, err)
	}
	message := scoutChatMessageRecord{
		ID: "prior-image-message", Kind: scoutChatMessageKindImage, Role: "scout", Text: "prior render",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Image:     &scoutChatImageRef{Ref: strings.Repeat("a", 64), Mime: "image/png", ArtifactID: artifact.ID, Prompt: "prior prompt"},
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); err != nil {
		t.Fatal(err)
	}
	if _, err := app.regenerateScoutChatImage(context.Background(), user, thread.ID, message.ID, "replacement prompt"); err != nil {
		t.Fatal(err)
	}
	saved, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scoutChatMessageIndex(saved, message.ID) < 0 || hasScoutChatImagePendingMessage(saved.Messages) {
		t.Fatalf("messages=%#v, failed regeneration must preserve the prior image and clear pending state", saved.Messages)
	}
	if _, ok := app.osArtifactByID(artifact.ID); !ok {
		t.Fatal("failed regeneration deleted the prior image artifact")
	}
}

func TestScoutChatImageRegenerateRechecksThreadAuthorizationBeforeProvider(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "private-image-message", Kind: scoutChatMessageKindImage, Role: "scout", Text: "private render",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Image:     &scoutChatImageRef{Ref: strings.Repeat("b", 64), Prompt: "private prompt"},
	}
	if _, err := app.commitScoutChatThreadMessages(owner.Email, thread.ID, message); err != nil {
		t.Fatal(err)
	}
	unauthorized := &userAccount{Email: "outsider@example.com", Name: "Outsider"}
	if _, err := app.regenerateScoutChatImage(context.Background(), unauthorized, thread.ID, message.ID, "stolen prompt"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unauthorized regeneration err=%v, want fail-closed not found", err)
	}
	saved, _, err := app.scoutChatThreadByID(owner.Email, thread.ID)
	if err != nil || scoutChatMessageIndex(saved, message.ID) < 0 || hasScoutChatImagePendingMessage(saved.Messages) {
		t.Fatalf("unauthorized regeneration mutated thread: messages=%#v err=%v", saved.Messages, err)
	}
}

func TestScoutChatImagePendingRecoversAfterRestart(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	withFakeImagesAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":          []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("recovered-image"))}},
			"output_format": "png",
		})
	})
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Scout", "")
	if err != nil {
		t.Fatal(err)
	}
	pending := scoutChatMessageRecord{
		ID: "pending-restart-image", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ImageGeneration: &scoutChatImageGenerationState{
			Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "recovered prompt",
			RequestedByEmail: user.Email, RequestedByName: user.Name,
		},
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, pending); err != nil {
		t.Fatal(err)
	}
	app.startScoutChatImageWorkers()
	app.scoutImageWG.Wait()
	app.stopScoutChatImageWorkers()
	saved, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hasScoutChatImagePendingMessage(saved.Messages) || !hasScoutChatImageMessage(saved.Messages) {
		t.Fatalf("messages=%#v, restart recovery must replace the durable pending pill", saved.Messages)
	}
}

func TestScoutChatImageWorkerShutdownDoesNotRepeatAmbiguousProviderCall(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-image-key")
	app := newIsolatedKanbanBoardApp(t)
	requestStarted := make(chan struct{})
	previousCreate := createScoutChatImage
	createScoutChatImage = func(ctx context.Context, _ string, _ openAIImageOptions) (string, string, error) {
		close(requestStarted)
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	t.Cleanup(func() { createScoutChatImage = previousCreate })
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Scout", "")
	if err != nil {
		t.Fatal(err)
	}
	pending := scoutChatMessageRecord{
		ID: "pending-shutdown-image", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ImageGeneration: &scoutChatImageGenerationState{
			Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "resume after shutdown",
			RequestedByEmail: user.Email, RequestedByName: user.Name,
		},
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, pending); err != nil {
		t.Fatal(err)
	}
	app.startScoutChatImageWorkers()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("image worker did not start")
	}
	stopped := make(chan struct{})
	go func() {
		app.stopScoutChatImageWorkers()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("image worker shutdown did not join")
	}
	saved, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hasScoutChatImagePendingMessage(saved.Messages) {
		t.Fatalf("messages=%#v, ambiguous provider call must settle without an automatic retry", saved.Messages)
	}
	foundError := false
	for _, message := range saved.Messages {
		if message.Role == "error" {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("messages=%#v, want an honest interrupted-render terminal", saved.Messages)
	}
}

func hasScoutChatImagePendingMessage(messages []scoutChatMessageRecord) bool {
	for _, message := range messages {
		if message.Kind == scoutChatMessageKindImagePending {
			return true
		}
	}
	return false
}

func TestScoutChatImagePendingPromptIsRedactedFromViewerProjection(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := scoutChatThreadRecord{
		ID: "pending-image-thread", OwnerEmail: "aj@shareability.com", Visibility: "", Messages: []scoutChatMessageRecord{{
			ID: "pending-image-message", Kind: scoutChatMessageKindImagePending, Role: "scout", Text: "generating image…",
			ImageGeneration: &scoutChatImageGenerationState{Status: scoutChatImageGenerationStatusGenerating, Phase: scoutChatImagePhaseQueued, Prompt: "private optimized prompt", RequestedByEmail: "aj@shareability.com", RequestedByName: "AJ"},
		}},
	}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if projected.Messages[0].ImageGeneration == nil || projected.Messages[0].ImageGeneration.Prompt != "" || projected.Messages[0].ImageGeneration.RequestedByEmail != "" || projected.Messages[0].ImageGeneration.RequestedByName != "" {
		t.Fatalf("projected pending generation=%#v, want prompt redacted", projected.Messages[0].ImageGeneration)
	}
	if thread.Messages[0].ImageGeneration.Prompt != "private optimized prompt" {
		t.Fatal("viewer projection mutated the persisted pending prompt")
	}
}

func TestScoutChatImageArtifactCanBeSavedToFiles(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	ref, err := putBlob([]byte("saveable-image"), "image/png")
	if err != nil {
		t.Fatalf("put image: %v", err)
	}
	artifact, appended, err := kanbanApp.createOSArtifactWithMetadata("design", "Saveable render", "## Concept render", user.Name, map[string]string{
		"type": "markdown", "source": "chat_image", "imagePrompt": "a saveable image", "status": artifactStatusComplete, "published": "true",
	})
	if err != nil || !appended {
		t.Fatalf("create artifact appended=%v err=%v", appended, err)
	}
	if _, err := kanbanApp.appendArtifactAsset(artifact.ID, artifactAsset{Ref: ref, Mime: "image/png", Name: "saveable-render.png", Kind: "image"}); err != nil {
		t.Fatalf("append image asset: %v", err)
	}
	row, err := kanbanApp.saveDeliverableToFiles(artifact.ID, "", user.Name)
	if err != nil {
		t.Fatalf("save image to Files: %v", err)
	}
	if row.Origin != "deliverable" || row.ArtifactID != artifact.ID || row.Name != "saveable-render.png" || row.Mime != "image/png" || row.DownloadURL == "" || !row.Previewable {
		t.Fatalf("saved image row=%+v, want image deliverable with download URL", row)
	}
	thread := scoutChatThreadRecord{ID: "saved-image-thread", OwnerEmail: user.Email, Messages: []scoutChatMessageRecord{{
		ID: "saved-image-message", Kind: scoutChatMessageKindImage, Role: "scout",
		Image: &scoutChatImageRef{Ref: ref, ArtifactID: artifact.ID, Prompt: "a saveable image"},
	}}}
	projected := kanbanApp.projectScoutChatThreadForViewer(user.Email, thread)
	if projected.Messages[0].Image == nil || !projected.Messages[0].Image.SavedToFiles {
		t.Fatalf("projected image=%#v, want durable saved-to-Drive state", projected.Messages[0].Image)
	}
}

func TestScoutChatImageSaveWinsConcurrentRegenerationDiscard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	artifact, appended, err := app.createOSArtifactWithMetadata("design", "Race-safe render", "render", "AJ", map[string]string{
		"type": "markdown", "source": "chat_image", "status": artifactStatusComplete, "published": "true",
	})
	if err != nil || !appended {
		t.Fatalf("create artifact appended=%v err=%v", appended, err)
	}
	stamped := make(chan struct{})
	release := make(chan struct{})
	previousProbe := fileSaveAfterArtifactStampProbe
	fileSaveAfterArtifactStampProbe = func() {
		close(stamped)
		<-release
	}
	t.Cleanup(func() { fileSaveAfterArtifactStampProbe = previousProbe })
	saveDone := make(chan error, 1)
	go func() {
		_, saveErr := app.saveDeliverableToFiles(artifact.ID, "", "AJ")
		saveDone <- saveErr
	}()
	<-stamped
	discardDone := make(chan struct{})
	go func() {
		app.discardUnsavedScoutChatImageArtifact(&scoutChatImageRef{ArtifactID: artifact.ID})
		close(discardDone)
	}()
	select {
	case <-discardDone:
		t.Fatal("regeneration discard bypassed the in-flight save lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-saveDone; err != nil {
		t.Fatalf("save image: %v", err)
	}
	<-discardDone
	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok || !strings.EqualFold(stored.Metadata["savedToFiles"], "true") {
		t.Fatalf("saved artifact lost to concurrent discard: ok=%v metadata=%v", ok, stored.Metadata)
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
	previousRunner := startScoutChatImageAsyncWithPending
	startScoutChatImageAsyncWithPending = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
		app.runScoutChatImageGenerationForPending(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousRunner })

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
	// The immediate response is the durable generating state, not the picture.
	if answer, ok := response["answer"].(scoutChatMessageRecord); !ok || answer.Kind != scoutChatMessageKindImagePending {
		t.Fatalf("immediate answer=%#v, want image_pending", response["answer"])
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

	previousRunner := startScoutChatImageAsyncWithPending
	startScoutChatImageAsyncWithPending = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
		app.runScoutChatImageGenerationForPending(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousRunner })

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
	t.Setenv("OPENAI_API_KEY", "openai-chat-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-chat-test"
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startScoutChatImageAsync
	startScoutChatImageAsync = func(_ *kanbanBoardApp, _ string, _ string, _ string, _ string) {
		t.Fatal("a dismissal must never generate")
	}
	t.Cleanup(func() { startScoutChatImageAsync = previousRunner })

	var askedTier0 string
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
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
