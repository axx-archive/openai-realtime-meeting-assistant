package main

// scout_chat_images.go — the concept-render runner behind a private-thread
// image confirm (card 096). All generation infra already exists
// (createOpenAIImage in openai_images.go: gpt-image-2, putBlob, the graceful
// apiRequestFailure error type). This file is the thin wiring the propose-
// confirm law needs: the confirm on an image proposal calls createOpenAIImage
// asynchronously, files a design artifact with a kind=image asset, and commits
// a Kind="image" chat message that renders the picture inline via the
// session-gated /artifacts/blob route.
//
// A single chat image is a DIRECT API call, not a contract-gated goal run — so
// it never touches the goal pipeline and never promotes the hidden
// imagery_board tool. On the prod key's current 429 (insufficient_quota) it
// commits a friendly error bubble ("OpenAI API quota is exhausted") instead of
// a silent failure — half the card's acceptance criteria.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// scoutChatImageGenerationTimeout bounds one async render. The provider's own
// HTTP client caps at 120s; this outer ceiling covers the blob store + the
// artifact + message commits around it.
const scoutChatImageGenerationTimeout = 3 * time.Minute

// scoutChatImageRef is the persisted image payload on a Kind="image" chat
// message: the content-addressed blob ref the /artifacts/blob route serves
// inline, its pinned mime, a display name, the filed design artifact id (for
// the "open artifact" action), and the prompt that produced it.
type scoutChatImageRef struct {
	Ref        string `json:"ref"`
	Mime       string `json:"mime,omitempty"`
	Name       string `json:"name,omitempty"`
	ArtifactID string `json:"artifactId,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
}

// openAIImageGenerationAvailable reports whether image generation is
// configured. The propose_image router tool, its system-prompt intent line, and
// the deterministic pre-router guard all open ONLY when this is true — a
// keyless-OpenAI deploy must never offer a render it cannot produce.
func openAIImageGenerationAvailable() bool {
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
}

// startScoutChatImageAsync is a package-var seam (the startAgentThreadAsync
// pattern) so tests can run generation synchronously against the fake images
// API. The confirm hands off here and returns immediately — image calls run
// 30-90s and must never block the HTTP request.
var startScoutChatImageAsync = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string) {
	go app.runScoutChatImageGeneration(threadID, ownerEmail, prompt, createdBy)
}

// runScoutChatImageGeneration generates one image, files it, and delivers it.
// Happy path: createOpenAIImage -> createOSArtifactWithMetadata(design,
// source=chat_image) -> appendArtifactAsset(kind=image) -> commit a Kind=image
// message (live delivery to the owner is free via commitScoutChatThreadMessages
// -> deliverScoutChatThreadUpdate). Error path: a friendly Role=error bubble.
func (app *kanbanBoardApp) runScoutChatImageGeneration(threadID string, ownerEmail string, prompt string, createdBy string) {
	ctx, cancel := context.WithTimeout(context.Background(), scoutChatImageGenerationTimeout)
	defer cancel()

	prompt = strings.TrimSpace(prompt)
	ref, mime, err := createOpenAIImage(ctx, prompt, openAIImageOptions{})
	if err != nil {
		app.commitScoutChatImageError(threadID, ownerEmail, err)
		return
	}

	// File the render as a design artifact. The asset (not just the chat
	// message's ref) is what keeps the blob live under sweepUnreferencedBlobs,
	// so file the artifact + attach the asset BEFORE committing the message.
	metadata := map[string]string{
		"type":        artifactTypeMarkdown,
		"source":      "chat_image",
		"imagePrompt": prompt,
	}
	artifact, appended, err := app.createOSArtifactWithMetadata("design", scoutChatImageTitle(prompt), scoutChatImageArtifactBody(prompt, ref, mime), createdBy, metadata)
	if err != nil || !appended || strings.TrimSpace(artifact.ID) == "" {
		app.commitScoutChatImageError(threadID, ownerEmail, fmt.Errorf("the render generated but could not be filed"))
		return
	}
	asset := artifactAsset{
		Ref:  ref,
		Mime: mime,
		Name: "concept-render" + imageryAssetExtension(mime),
		Kind: "image",
	}
	if updated, attachErr := app.appendArtifactAsset(artifact.ID, asset); attachErr != nil {
		// Logged, never fatal — the artifact body already carries the ref, and
		// the chat message below carries it too; the picture still renders.
		log.Errorf("scout chat image %s: attach image asset %s failed: %v", artifact.ID, ref, attachErr)
	} else {
		artifact = updated
	}

	// Refresh every signed-in library so the new design artifact appears (the
	// launchAgentThreadWithSpec precedent).
	broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))

	message := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      scoutChatMessageKindImage,
		Role:      "scout",
		Text:      "here's the concept render.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Image: &scoutChatImageRef{
			Ref:        ref,
			Mime:       mime,
			Name:       asset.Name,
			ArtifactID: artifact.ID,
			Prompt:     prompt,
		},
	}
	if _, err := app.commitScoutChatThreadMessages(ownerEmail, threadID, message); err != nil {
		log.Errorf("scout chat image: commit image message on thread %s failed: %v", threadID, err)
	}
}

// commitScoutChatImageError commits the friendly error bubble a failed render
// earns. A mapped OpenAI failure (the live prod 429 insufficient_quota, or a
// rate limit) uses openAIAPIRequestUserMessage so the raw upstream body never
// reaches the user; anything else uses the compacted error line.
func (app *kanbanBoardApp) commitScoutChatImageError(threadID string, ownerEmail string, err error) {
	detail := compactAssistantLine(err.Error())
	if friendly, _, ok := openAIAPIRequestUserMessage(err); ok && strings.TrimSpace(friendly) != "" {
		detail = friendly
	}
	message := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      "message",
		Role:      "error",
		Text:      "the concept render didn't go through — " + detail + ".",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, commitErr := app.commitScoutChatThreadMessages(ownerEmail, threadID, message); commitErr != nil {
		log.Errorf("scout chat image: commit error message on thread %s failed: %v", threadID, commitErr)
	}
}

// scoutChatImageTitle is the filed artifact's title: the prompt, trimmed to the
// storage cap, with a plain fallback.
func scoutChatImageTitle(prompt string) string {
	if title := trimForStorage(prompt, 72); title != "" {
		return title
	}
	return "Concept render"
}

// scoutChatImageArtifactBody is the filed design artifact's markdown: the
// concept-render disclosure (generated imagery is never passed off as
// photography — the imagery law), the prompt, and the generation record with
// the blob ref.
func scoutChatImageArtifactBody(prompt string, ref string, mime string) string {
	lines := []string{
		"## Concept render",
		"",
		"A single image generated from a Scout chat request (" + imageryConceptRenderLabel + " — generated imagery, never passed off as photography).",
		"",
		"## Prompt",
		firstNonEmptyString(strings.TrimSpace(prompt), "(no prompt recorded)"),
		"",
		"## Generation record",
		fmt.Sprintf("- Model %s, size %s, quality %s.", openAIImageModel(), defaultOpenAIImageSize, defaultOpenAIImageQuality),
		"- Image blob ref: " + ref + " (" + mime + ")",
	}
	return strings.Join(lines, "\n")
}
