package main

// scout_chat_images.go — the concept-render runner behind Scout chat image
// requests. All generation infra already exists
// (createOpenAIImage in openai_images.go: gpt-image-2, putBlob, the graceful
// apiRequestFailure error type). This file owns the async handoff: a direct
// request commits a generating pill immediately, calls the high-quality
// gpt-image-2 Images API off the HTTP path, files a design artifact with a
// kind=image asset, and commits a Kind="image" chat message that renders the
// picture inline via the session-gated /artifacts/blob route.
//
// A single chat image is a DIRECT API call, not a contract-gated goal run — so
// it never touches the goal pipeline and never promotes the hidden
// imagery_board tool. On the prod key's current 429 (insufficient_quota) it
// commits a friendly error bubble ("OpenAI API quota is exhausted") instead of
// a silent failure.

import (
	"context"
	"errors"
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
	Ref               string `json:"ref"`
	Mime              string `json:"mime,omitempty"`
	Name              string `json:"name,omitempty"`
	ArtifactID        string `json:"artifactId,omitempty"`
	Prompt            string `json:"prompt,omitempty"`
	GenerationID      string `json:"generationId,omitempty"`
	ReplacesMessageID string `json:"replacesMessageId,omitempty"`
	SavedToFiles      bool   `json:"savedToFiles,omitempty"`
}

type scoutChatImageGenerationState struct {
	Status            string `json:"status"`
	ReplacesMessageID string `json:"replacesMessageId,omitempty"`
	RequestedByEmail  string `json:"requestedByEmail,omitempty"`
	RequestedByName   string `json:"requestedByName,omitempty"`
	// Prompt is intentionally not rendered by the normal feed. It remains
	// durable so an in-flight request can be diagnosed/recovered without asking
	// the user to repeat the optimized prompt.
	Prompt string `json:"prompt,omitempty"`
}

const (
	scoutChatImageGenerationStatusGenerating = "generating"
	scoutChatImageGenerationStatusFailed     = "failed"
)

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
	app.startScoutChatImageGeneration(threadID, ownerEmail, prompt, createdBy, "")
}

// startScoutChatImageAsyncWithPending is the direct-feed seam. The legacy
// startScoutChatImageAsync remains for persisted proposal cards and older tests;
// new requests carry the pending pill id so the runner can remove it when the
// final image or an error arrives.
var startScoutChatImageAsyncWithPending = func(app *kanbanBoardApp, threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
	app.startScoutChatImageGeneration(threadID, ownerEmail, prompt, createdBy, pendingMessageID)
}

var createScoutChatImage = createOpenAIImage

func (app *kanbanBoardApp) startScoutChatImageWorkers() {
	if app == nil {
		return
	}
	started := false
	app.scoutImageStartOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		app.scoutImageMu.Lock()
		app.scoutImageCtx = ctx
		app.scoutImageCancel = cancel
		app.scoutImageInFlight = map[string]struct{}{}
		app.scoutImageMu.Unlock()
		started = true
	})
	if started {
		app.recoverScoutChatImageGenerations()
	}
}

func (app *kanbanBoardApp) stopScoutChatImageWorkers() {
	if app == nil {
		return
	}
	app.scoutImageMu.Lock()
	cancel := app.scoutImageCancel
	app.scoutImageCtx = nil
	app.scoutImageCancel = nil
	app.scoutImageMu.Unlock()
	if cancel != nil {
		cancel()
		app.scoutImageWG.Wait()
	}
}

func (app *kanbanBoardApp) startScoutChatImageGeneration(threadID, ownerEmail, prompt, createdBy, pendingMessageID string) {
	if app == nil {
		return
	}
	app.startScoutChatImageWorkers()
	jobKey := strings.TrimSpace(pendingMessageID)
	if jobKey == "" {
		jobKey = "legacy:" + temporalDigest(threadID+"\x00"+prompt+"\x00"+fmt.Sprint(time.Now().UnixNano()))
	}
	app.scoutImageMu.Lock()
	ctx := app.scoutImageCtx
	if ctx == nil {
		app.scoutImageMu.Unlock()
		return
	}
	if _, exists := app.scoutImageInFlight[jobKey]; exists {
		app.scoutImageMu.Unlock()
		return
	}
	app.scoutImageInFlight[jobKey] = struct{}{}
	app.scoutImageWG.Add(1)
	app.scoutImageMu.Unlock()
	go func() {
		defer func() {
			app.scoutImageMu.Lock()
			delete(app.scoutImageInFlight, jobKey)
			app.scoutImageMu.Unlock()
			app.scoutImageWG.Done()
		}()
		app.runScoutChatImageGenerationForPendingContext(ctx, threadID, ownerEmail, prompt, createdBy, pendingMessageID)
	}()
}

// runScoutChatImageGeneration generates one image, files it, and delivers it.
// Happy path: createOpenAIImage -> createOSArtifactWithMetadata(design,
// source=chat_image) -> appendArtifactAsset(kind=image) -> commit a Kind=image
// message (live delivery to the owner is free via commitScoutChatThreadMessages
// -> deliverScoutChatThreadUpdate). Error path: a friendly Role=error bubble.
func (app *kanbanBoardApp) runScoutChatImageGeneration(threadID string, ownerEmail string, prompt string, createdBy string) {
	app.runScoutChatImageGenerationForPendingContext(context.Background(), threadID, ownerEmail, prompt, createdBy, "")
}

func (app *kanbanBoardApp) runScoutChatImageGenerationForPending(threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
	app.runScoutChatImageGenerationForPendingContext(context.Background(), threadID, ownerEmail, prompt, createdBy, pendingMessageID)
}

func (app *kanbanBoardApp) runScoutChatImageGenerationForPendingContext(parent context.Context, threadID string, ownerEmail string, prompt string, createdBy string, pendingMessageID string) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, scoutChatImageGenerationTimeout)
	defer cancel()

	prompt = strings.TrimSpace(prompt)
	if pendingMessageID != "" && !app.scoutChatImagePendingCurrent(ownerEmail, threadID, pendingMessageID, prompt) {
		return
	}
	ref, mime, err := createScoutChatImage(ctx, prompt, openAIImageOptions{})
	if err != nil {
		// App shutdown deliberately leaves the durable pending message intact; the
		// next process recovers it. Provider failures and timeouts are terminal and
		// earn the normal user-visible error.
		if errors.Is(ctx.Err(), context.Canceled) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return
		}
		if pendingMessageID != "" {
			_ = app.removeScoutChatImagePending(ownerEmail, threadID, pendingMessageID)
		}
		app.commitScoutChatImageError(threadID, ownerEmail, err)
		return
	}
	if pendingMessageID != "" && !app.scoutChatImagePendingCurrent(ownerEmail, threadID, pendingMessageID, prompt) {
		return
	}

	// File the render as a design artifact. The asset (not just the chat
	// message's ref) is what keeps the blob live under sweepUnreferencedBlobs,
	// so file the artifact + attach the asset BEFORE committing the message.
	metadata := map[string]string{
		"type":        artifactTypeMarkdown,
		"source":      "chat_image",
		"imagePrompt": prompt,
		"status":      artifactStatusComplete,
		"published":   "true",
	}
	artifact, appended, err := app.createOSArtifactWithMetadata("design", scoutChatImageTitle(prompt), scoutChatImageArtifactBody(prompt, ref, mime), createdBy, metadata)
	if err != nil || !appended || strings.TrimSpace(artifact.ID) == "" {
		if pendingMessageID != "" {
			_ = app.removeScoutChatImagePending(ownerEmail, threadID, pendingMessageID)
		}
		app.commitScoutChatImageError(threadID, ownerEmail, fmt.Errorf("the render generated but could not be filed"))
		return
	}
	asset := artifactAsset{
		Ref:  ref,
		Mime: mime,
		Name: "concept-render" + imageryAssetExtension(mime),
		Kind: "image",
	}
	updated, attachErr := app.appendArtifactAsset(artifact.ID, asset)
	if attachErr != nil {
		_, _, _, _ = app.deleteOSArtifactAndEmit(artifact.ID)
		if pendingMessageID != "" {
			_ = app.removeScoutChatImagePending(ownerEmail, threadID, pendingMessageID)
		}
		app.commitScoutChatImageError(threadID, ownerEmail, fmt.Errorf("the render generated but could not be filed"))
		return
	}
	artifact = updated

	// Refresh every signed-in library so the new design artifact appears (the
	// launchAgentThreadWithSpec precedent).
	broadcastSignedInKanbanEvent("memory", nil)

	message := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      scoutChatMessageKindImage,
		Role:      "scout",
		Text:      "here's the concept render.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Image: &scoutChatImageRef{
			Ref:          ref,
			Mime:         mime,
			Name:         asset.Name,
			ArtifactID:   artifact.ID,
			Prompt:       prompt,
			GenerationID: strings.TrimSpace(pendingMessageID),
		},
	}
	if pending, ok := app.scoutChatImagePending(ownerEmail, threadID, pendingMessageID); ok && pending.ImageGeneration != nil {
		message.Image.ReplacesMessageID = strings.TrimSpace(pending.ImageGeneration.ReplacesMessageID)
	}
	if _, err := app.commitScoutChatThreadMessages(ownerEmail, threadID, message); err != nil {
		log.Errorf("scout chat image: commit image message on thread %s failed: %v", threadID, err)
		if pendingMessageID != "" {
			_ = app.removeScoutChatImagePending(ownerEmail, threadID, pendingMessageID)
			app.commitScoutChatImageError(threadID, ownerEmail, fmt.Errorf("the render generated but could not be delivered"))
		}
		return
	}
	if pendingMessageID != "" {
		// The image is committed first so a live client never sees the generating
		// pill disappear before it has the replacement content.
		if finalizeErr := app.completeScoutChatImagePending(ownerEmail, threadID, pendingMessageID); finalizeErr != nil {
			log.Errorf("scout chat image: finalize pending %s failed: %v", pendingMessageID, finalizeErr)
		}
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

func (app *kanbanBoardApp) scoutChatImagePending(ownerEmail, threadID, pendingMessageID string) (scoutChatMessageRecord, bool) {
	if app == nil || strings.TrimSpace(pendingMessageID) == "" {
		return scoutChatMessageRecord{}, false
	}
	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil || thread.ArchivedAt != "" {
		return scoutChatMessageRecord{}, false
	}
	index := scoutChatMessageIndex(thread, pendingMessageID)
	if index < 0 || thread.Messages[index].Kind != scoutChatMessageKindImagePending || thread.Messages[index].ImageGeneration == nil {
		return scoutChatMessageRecord{}, false
	}
	return thread.Messages[index], true
}

func (app *kanbanBoardApp) scoutChatImagePendingCurrent(ownerEmail, threadID, pendingMessageID, prompt string) bool {
	pending, ok := app.scoutChatImagePending(ownerEmail, threadID, pendingMessageID)
	return ok && pending.ImageGeneration.Status == scoutChatImageGenerationStatusGenerating && strings.TrimSpace(pending.ImageGeneration.Prompt) == strings.TrimSpace(prompt)
}

// completeScoutChatImagePending makes successful regeneration lossless. The
// old image stays visible and its artifact stays live until the replacement is
// committed. Only then are the pending pill and superseded message removed in
// one thread rewrite; a saved old artifact is preserved by the discard helper.
func (app *kanbanBoardApp) completeScoutChatImagePending(ownerEmail, threadID, pendingMessageID string) error {
	pendingMessageID = strings.TrimSpace(pendingMessageID)
	if pendingMessageID == "" {
		return nil
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()

	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil {
		lock.Unlock()
		return err
	}
	pendingIndex := scoutChatMessageIndex(thread, pendingMessageID)
	if pendingIndex < 0 || thread.Messages[pendingIndex].Kind != scoutChatMessageKindImagePending {
		lock.Unlock()
		return nil
	}
	pending := thread.Messages[pendingIndex]
	replacesMessageID := ""
	if pending.ImageGeneration != nil {
		replacesMessageID = strings.TrimSpace(pending.ImageGeneration.ReplacesMessageID)
	}
	removed := []scoutChatMessageRecord{pending}
	var replacedImage *scoutChatImageRef
	filtered := make([]scoutChatMessageRecord, 0, len(thread.Messages))
	for _, message := range thread.Messages {
		if message.ID == pendingMessageID {
			continue
		}
		if replacesMessageID != "" && message.ID == replacesMessageID && message.Kind == scoutChatMessageKindImage && message.Image != nil {
			image := *message.Image
			replacedImage = &image
			removed = append(removed, message)
			continue
		}
		filtered = append(filtered, message)
	}
	thread.Messages = filtered
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	thread.Preview = scoutChatThreadPreview(thread)
	if err := app.saveScoutChatThread(thread); err != nil {
		lock.Unlock()
		return err
	}
	for _, message := range removed {
		app.observeSTRIDETeamChatMessage(thread, message, "delete", ownerEmail)
		deliverScoutChatThreadDeletion(thread, message.ID)
	}
	app.rebuildPrivateConversationContinuity(thread, "delete")
	lock.Unlock()

	app.discardUnsavedScoutChatImageArtifact(replacedImage)
	return nil
}

// recoverScoutChatImageGenerations resumes the durable generating pills once
// per process. If the replacement message landed before a crash, recovery only
// finishes cleanup; otherwise the persisted, viewer-redacted prompt is queued
// through the normal in-process dedupe and shutdown lifecycle.
func (app *kanbanBoardApp) recoverScoutChatImageGenerations() {
	if app == nil || app.memory == nil || !openAIImageGenerationAvailable() {
		return
	}
	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || thread.ArchivedAt != "" {
			continue
		}
		for _, message := range thread.Messages {
			if message.Kind != scoutChatMessageKindImagePending || message.ImageGeneration == nil ||
				message.ImageGeneration.Status != scoutChatImageGenerationStatusGenerating || strings.TrimSpace(message.ImageGeneration.Prompt) == "" {
				continue
			}
			completed := false
			for _, candidate := range thread.Messages {
				if candidate.Kind == scoutChatMessageKindImage && candidate.Image != nil && candidate.Image.GenerationID == message.ID {
					completed = true
					break
				}
			}
			if completed {
				if err := app.completeScoutChatImagePending(thread.OwnerEmail, thread.ID, message.ID); err != nil {
					log.Errorf("scout chat image: recovery finalize %s failed: %v", message.ID, err)
				}
				continue
			}
			requesterEmail := firstNonEmptyString(normalizeAccountEmail(message.ImageGeneration.RequestedByEmail), normalizeAccountEmail(thread.OwnerEmail))
			if !scoutChatThreadAllowsViewer(thread, requesterEmail) {
				_ = app.removeScoutChatImagePending(thread.OwnerEmail, thread.ID, message.ID)
				app.commitScoutChatImageError(thread.ID, thread.OwnerEmail, fmt.Errorf("the original requester is no longer authorized for this channel"))
				continue
			}
			requesterName := firstNonEmptyString(strings.TrimSpace(message.ImageGeneration.RequestedByName), thread.CreatedBy, requesterEmail)
			app.startScoutChatImageGeneration(thread.ID, requesterEmail, message.ImageGeneration.Prompt, requesterName, message.ID)
		}
	}
}

// removeScoutChatImagePending removes the generating pill after the terminal
// image/error event has been delivered. It is intentionally scoped to the
// image-pending kind so a stale completion callback cannot delete a later chat
// message that reused an id.
func (app *kanbanBoardApp) removeScoutChatImagePending(ownerEmail string, threadID string, pendingMessageID string) error {
	pendingMessageID = strings.TrimSpace(pendingMessageID)
	if pendingMessageID == "" {
		return nil
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil {
		return err
	}
	index := scoutChatMessageIndex(thread, pendingMessageID)
	if index < 0 || thread.Messages[index].Kind != scoutChatMessageKindImagePending {
		return nil
	}
	removed := thread.Messages[index]
	thread.Messages = append(thread.Messages[:index], thread.Messages[index+1:]...)
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	thread.Preview = scoutChatThreadPreview(thread)
	if err := app.saveScoutChatThread(thread); err != nil {
		return err
	}
	app.observeSTRIDETeamChatMessage(thread, removed, "delete", ownerEmail)
	app.rebuildPrivateConversationContinuity(thread, "delete")
	deliverScoutChatThreadDeletion(thread, pendingMessageID)
	return nil
}

// discardUnsavedScoutChatImageArtifact keeps the Files/Drive choice honest:
// regenerating an image removes its un-saved design artifact as well, while a
// render the user explicitly saved to Drive remains durable. Blob bytes become
// eligible for the existing administrator-controlled orphan sweep after the
// artifact deletion; this path never sweeps unrelated workspace data.
func (app *kanbanBoardApp) discardUnsavedScoutChatImageArtifact(image *scoutChatImageRef) {
	if app == nil || image == nil || strings.TrimSpace(image.ArtifactID) == "" {
		return
	}
	artifactID := strings.TrimSpace(image.ArtifactID)
	lock := app.scoutChatThreadLock("artifact-files-" + artifactID)
	lock.Lock()
	defer lock.Unlock()
	artifact, ok := app.osArtifactByID(artifactID)
	if !ok || strings.TrimSpace(artifact.Metadata["source"]) != "chat_image" ||
		strings.EqualFold(strings.TrimSpace(artifact.Metadata["savedToFiles"]), "true") {
		return
	}
	if _, _, deleted, err := app.deleteOSArtifactAndEmit(artifact.ID); err != nil {
		log.Errorf("scout chat image: discard superseded artifact %s failed: %v", artifact.ID, err)
	} else if deleted {
		broadcastSignedInKanbanEvent("memory", nil)
	}
}

// regenerateScoutChatImage replaces one completed image in-place at the chat
// contract level: remove the old image message, append one generating pill,
// preserve the old artifact only when it was explicitly saved to Drive, then
// hand the optimized/editable prompt to the same async gpt-image-2 runner.
func (app *kanbanBoardApp) regenerateScoutChatImage(ctx context.Context, user *userAccount, threadID string, messageID string, prompt string) (map[string]any, error) {
	if user == nil {
		return nil, fmt.Errorf("chat thread not found")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	if !openAIImageGenerationAvailable() {
		return nil, fmt.Errorf("image generation is unavailable")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("image prompt is required")
	}
	if len([]rune(prompt)) > 12000 {
		return nil, fmt.Errorf("image prompt is too long")
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil, fmt.Errorf("message id is required")
	}

	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	if thread.ArchivedAt != "" {
		lock.Unlock()
		return nil, fmt.Errorf("chat thread is archived")
	}
	index := scoutChatMessageIndex(thread, messageID)
	if index < 0 || thread.Messages[index].Kind != scoutChatMessageKindImage || thread.Messages[index].Image == nil {
		lock.Unlock()
		return nil, fmt.Errorf("generated image not found")
	}
	oldMessage := thread.Messages[index]
	pending := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      scoutChatMessageKindImagePending,
		Role:      "scout",
		Text:      "generating image…",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ImageGeneration: &scoutChatImageGenerationState{
			Status:            scoutChatImageGenerationStatusGenerating,
			Prompt:            prompt,
			ReplacesMessageID: oldMessage.ID,
			RequestedByEmail:  normalizeAccountEmail(user.Email),
			RequestedByName:   strings.TrimSpace(user.Name),
		},
	}
	thread.Messages = append(thread.Messages, pending)
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	thread.Preview = scoutChatThreadPreview(thread)
	if err := app.saveScoutChatThread(thread); err != nil {
		lock.Unlock()
		return nil, err
	}
	deliverScoutChatThreadUpdate(thread, pending)
	lock.Unlock()

	startScoutChatImageAsyncWithPending(app, threadID, user.Email, prompt, user.Name, pending.ID)
	return map[string]any{
		"ok":     true,
		"answer": pending,
		"thread": thread,
		"imageGeneration": map[string]any{
			"status":    scoutChatImageGenerationStatusGenerating,
			"messageId": pending.ID,
		},
	}, nil
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
