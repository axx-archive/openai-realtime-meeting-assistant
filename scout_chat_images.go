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

// scoutChatImageGenerationTimeout bounds one async render. The provider owns a
// five-minute wire window; this outer ceiling leaves time for the blob store +
// artifact + message commits around it.
const scoutChatImageGenerationTimeout = openAIImageProviderTimeout + time.Minute

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
	Phase             string `json:"phase,omitempty"`
	PhaseGeneration   int64  `json:"phaseGeneration,omitempty"`
	ReplacesMessageID string `json:"replacesMessageId,omitempty"`
	RequestedByEmail  string `json:"requestedByEmail,omitempty"`
	RequestedByName   string `json:"requestedByName,omitempty"`
	// Prompt is intentionally not rendered by the normal feed. It remains
	// durable so an in-flight request can be diagnosed/recovered without asking
	// the user to repeat the optimized prompt.
	Prompt       string `json:"prompt,omitempty"`
	ResultRef    string `json:"resultRef,omitempty"`
	ResultMime   string `json:"resultMime,omitempty"`
	ArtifactID   string `json:"artifactId,omitempty"`
	FailureClass string `json:"failureClass,omitempty"`
	FailureText  string `json:"failureText,omitempty"`
}

const (
	scoutChatImageGenerationStatusGenerating = "generating"
	scoutChatImageGenerationStatusFailed     = "failed"

	scoutChatImagePhaseQueued          = "queued"
	scoutChatImagePhaseProviderStarted = "provider_started"
	scoutChatImagePhaseGenerated       = "generated"
	scoutChatImagePhaseArtifactFiled   = "artifact_filed"
	scoutChatImagePhaseProviderFailed  = "provider_failed"
	scoutChatImagePhaseCleanupRequired = "cleanup_required"
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

var saveScoutChatImageThread = func(app *kanbanBoardApp, thread scoutChatThreadRecord) error {
	return app.saveScoutChatThread(thread)
}

var deleteScoutChatImageArtifact = func(app *kanbanBoardApp, artifactID string) (meetingMemoryEntry, []scopedRoomDeliveryAcknowledgement, bool, error) {
	return app.deleteOSArtifactAndEmit(artifactID)
}

var appendScoutChatImageAsset = func(app *kanbanBoardApp, artifactID string, asset artifactAsset) (meetingMemoryEntry, error) {
	return app.appendArtifactAsset(artifactID, asset)
}

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
	if pendingMessageID == "" {
		app.runLegacyScoutChatImageGeneration(ctx, threadID, ownerEmail, prompt, createdBy)
		return
	}
	thread, _, threadErr := app.scoutChatThreadByID(ownerEmail, threadID)
	if threadErr != nil || thread.ArchivedAt != "" {
		return
	}
	commitOwner := firstNonEmptyString(normalizeAccountEmail(thread.OwnerEmail), normalizeAccountEmail(ownerEmail))
	if thread.Riff != nil {
		pending, ok := app.scoutChatImagePending(commitOwner, threadID, pendingMessageID)
		if !ok {
			return
		}
		if _, _, sourceErr := app.privateRiffWorkSourceWindow(commitOwner, thread, pending); sourceErr != nil {
			changed, _ := app.transitionScoutChatImagePending(commitOwner, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseQueued}, func(state *scoutChatImageGenerationState) {
				state.Phase = scoutChatImagePhaseProviderFailed
				state.FailureClass = "source_authority_changed"
				state.FailureText = "the private Riff source changed before the render started; send the request again from current context"
			})
			if changed {
				app.resumeScoutChatImagePending(commitOwner, threadID, pendingMessageID, createdBy)
			}
			return
		}
	}
	claimed, err := app.transitionScoutChatImagePending(commitOwner, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseQueued}, func(state *scoutChatImageGenerationState) {
		state.Phase = scoutChatImagePhaseProviderStarted
	})
	if err != nil || !claimed {
		return
	}
	ref, mime, err := createScoutChatImage(ctx, prompt, openAIImageOptions{})
	if err != nil {
		failureClass := "provider_error"
		failureText := scoutChatImageFriendlyDetail(err)
		if errors.Is(ctx.Err(), context.Canceled) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			failureClass = "provider_ambiguous"
			failureText = "the render was interrupted before its result could be verified; please start a new render"
		}
		_, _ = app.transitionScoutChatImagePending(commitOwner, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseProviderStarted}, func(state *scoutChatImageGenerationState) {
			state.Phase = scoutChatImagePhaseProviderFailed
			state.FailureClass = failureClass
			state.FailureText = failureText
		})
		app.resumeScoutChatImagePending(commitOwner, threadID, pendingMessageID, createdBy)
		return
	}
	if latest, _, latestErr := app.scoutChatThreadByID(commitOwner, threadID); latestErr != nil {
		return
	} else if latest.Riff != nil {
		pending, ok := app.scoutChatImagePending(commitOwner, threadID, pendingMessageID)
		if !ok {
			return
		}
		if _, _, sourceErr := app.privateRiffWorkSourceWindow(commitOwner, latest, pending); sourceErr != nil {
			changed, _ := app.transitionScoutChatImagePending(commitOwner, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseProviderStarted}, func(state *scoutChatImageGenerationState) {
				state.Phase = scoutChatImagePhaseProviderFailed
				state.FailureClass = "source_authority_changed"
				state.FailureText = "the private Riff source changed before the render could be filed; send the request again from current context"
			})
			if changed {
				app.resumeScoutChatImagePending(commitOwner, threadID, pendingMessageID, createdBy)
			}
			return
		}
	}
	stored, storeErr := app.transitionScoutChatImagePending(commitOwner, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseProviderStarted}, func(state *scoutChatImageGenerationState) {
		state.Phase = scoutChatImagePhaseGenerated
		state.ResultRef = strings.TrimSpace(ref)
		state.ResultMime = strings.TrimSpace(mime)
	})
	if storeErr != nil || !stored {
		// The provider may have completed, but without a durable result checkpoint
		// recovery must never guess or invoke it again. The provider_started phase is
		// intentionally left ambiguous for restart reconciliation.
		return
	}
	app.resumeScoutChatImagePending(commitOwner, threadID, pendingMessageID, createdBy)
}

func (app *kanbanBoardApp) runLegacyScoutChatImageGeneration(ctx context.Context, threadID, ownerEmail, prompt, createdBy string) {
	ref, mime, err := createScoutChatImage(ctx, prompt, openAIImageOptions{})
	if err != nil {
		app.commitScoutChatImageError(threadID, ownerEmail, nil, err)
		return
	}
	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil || thread.ArchivedAt != "" {
		return
	}
	artifact, asset, err := app.fileScoutChatImageArtifact(thread, "", prompt, ref, mime, createdBy)
	if err != nil {
		app.commitScoutChatImageError(threadID, ownerEmail, nil, err)
		return
	}
	message := scoutChatMessageRecord{ID: fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()), Kind: scoutChatMessageKindImage, Role: "scout", Text: "here's the concept render.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Image: &scoutChatImageRef{Ref: ref, Mime: mime, Name: asset.Name, ArtifactID: artifact.ID, Prompt: prompt}}
	if _, err := app.commitScoutChatThreadMessages(ownerEmail, threadID, message); err != nil {
		log.Errorf("scout chat image: commit legacy image on thread %s failed: %v", threadID, err)
	}
}

func (app *kanbanBoardApp) fileScoutChatImageArtifact(thread scoutChatThreadRecord, generationID, prompt, ref, mime, createdBy string) (meetingMemoryEntry, artifactAsset, error) {
	if generationID != "" {
		// The thread-scoped filing lane is bounded by durable chat threads. A
		// generation-scoped key would leak one retained mutex for every render.
		lock := app.scoutChatThreadLock("image-file:" + thread.ID)
		lock.Lock()
		defer lock.Unlock()
	}
	if generationID != "" {
		for _, entry := range app.memory.snapshot(0) {
			if entry.Kind == meetingMemoryKindOSArtifact && entry.Metadata["source"] == "chat_image" && entry.Metadata["generationId"] == generationID {
				if entry.Metadata["originSurface"] != "chat:"+thread.ID ||
					normalizeAccountEmail(entry.Metadata["ownerEmail"]) != normalizeAccountEmail(thread.OwnerEmail) ||
					entry.Metadata["visibility"] != normalizeScoutChatVisibility(thread.Visibility) ||
					artifactIsPublished(entry) || entry.Metadata["imagePrompt"] != prompt ||
					entry.Metadata["status"] != artifactStatusComplete || entry.Metadata["type"] != artifactTypeMarkdown ||
					entry.Text != scoutChatImageArtifactBody(prompt, ref, mime) {
					return meetingMemoryEntry{}, artifactAsset{}, errScoutChatImageArtifactBinding
				}
				assets := artifactAssets(entry)
				for _, candidate := range assets {
					if candidate.Ref == ref && candidate.Mime == mime && candidate.Kind == "image" {
						return entry, candidate, nil
					}
				}
				return meetingMemoryEntry{}, artifactAsset{}, errScoutChatImageArtifactBinding
			}
		}
	}

	// File the render as a design artifact. The asset (not just the chat
	// message's ref) is what keeps the blob live under sweepUnreferencedBlobs,
	// so file the artifact + attach the asset BEFORE committing the message.
	metadata := map[string]string{
		"type":          artifactTypeMarkdown,
		"source":        "chat_image",
		"imagePrompt":   prompt,
		"status":        artifactStatusComplete,
		"published":     "false",
		"originSurface": "chat:" + thread.ID,
		"visibility":    normalizeScoutChatVisibility(thread.Visibility),
		"ownerEmail":    normalizeAccountEmail(thread.OwnerEmail),
		"generationId":  strings.TrimSpace(generationID),
	}
	artifact, appended, err := app.createOSArtifactWithMetadata("design", scoutChatImageTitle(prompt), scoutChatImageArtifactBody(prompt, ref, mime), createdBy, metadata)
	if err != nil || !appended || strings.TrimSpace(artifact.ID) == "" {
		return meetingMemoryEntry{}, artifactAsset{}, fmt.Errorf("the render generated but could not be filed")
	}
	asset := artifactAsset{
		Ref:  ref,
		Mime: mime,
		Name: "concept-render" + imageryAssetExtension(mime),
		Kind: "image",
	}
	updated, attachErr := appendScoutChatImageAsset(app, artifact.ID, asset)
	if attachErr != nil {
		_, _, _, _ = deleteScoutChatImageArtifact(app, artifact.ID)
		if _, stillPresent := app.osArtifactByID(artifact.ID); stillPresent {
			return artifact, asset, fmt.Errorf("the render generated but its partial artifact requires cleanup")
		}
		return meetingMemoryEntry{}, artifactAsset{}, fmt.Errorf("the render generated but could not be filed")
	}
	return updated, asset, nil
}

var errScoutChatImageAuthorityChanged = errors.New("image destination authority changed")
var errScoutChatImageArtifactBinding = errors.New("image artifact generation binding changed")

func scoutChatImagePhase(state *scoutChatImageGenerationState) string {
	if state == nil {
		return ""
	}
	phase := strings.TrimSpace(state.Phase)
	if phase == "" && state.Status == scoutChatImageGenerationStatusGenerating {
		// Legacy rows may have crossed the provider boundary before this journal
		// existed. Never reinterpret them as queued and double-call the provider.
		return scoutChatImagePhaseProviderStarted
	}
	return phase
}

func (app *kanbanBoardApp) transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt string, expected []string, mutate func(*scoutChatImageGenerationState)) (bool, error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil {
		return false, err
	}
	index := scoutChatMessageIndex(thread, pendingMessageID)
	if index < 0 || thread.Messages[index].Kind != scoutChatMessageKindImagePending || thread.Messages[index].ImageGeneration == nil {
		return false, nil
	}
	state := thread.Messages[index].ImageGeneration
	if strings.TrimSpace(state.Prompt) != strings.TrimSpace(prompt) {
		return false, errScoutChatImageAuthorityChanged
	}
	phase := scoutChatImagePhase(state)
	allowed := false
	for _, candidate := range expected {
		if phase == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return false, nil
	}
	mutate(state)
	state.PhaseGeneration++
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveScoutChatImageThread(app, thread); err != nil {
		return false, err
	}
	return true, nil
}

func (app *kanbanBoardApp) resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy string) {
	pending, ok := app.scoutChatImagePending(ownerEmail, threadID, pendingMessageID)
	if !ok || pending.ImageGeneration == nil {
		return
	}
	state := pending.ImageGeneration
	prompt := strings.TrimSpace(state.Prompt)
	requesterEmail := firstNonEmptyString(normalizeAccountEmail(state.RequestedByEmail), normalizeAccountEmail(ownerEmail))
	switch scoutChatImagePhase(state) {
	case scoutChatImagePhaseProviderStarted:
		// The process cannot distinguish a provider-side success from a lost
		// response. Seal an honest terminal error instead of spending twice.
		changed, _ := app.transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseProviderStarted}, func(current *scoutChatImageGenerationState) {
			current.Phase = scoutChatImagePhaseProviderFailed
			current.FailureClass = "provider_ambiguous"
			current.FailureText = "the prior render was interrupted before its result could be verified; please start a new render"
		})
		if changed {
			app.resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy)
		}
	case scoutChatImagePhaseGenerated:
		thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
		if err != nil {
			return
		}
		if thread.Riff != nil {
			if _, _, sourceErr := app.privateRiffWorkSourceWindow(ownerEmail, thread, pending); sourceErr != nil {
				changed, _ := app.transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseGenerated}, func(current *scoutChatImageGenerationState) {
					current.Phase = scoutChatImagePhaseProviderFailed
					current.FailureClass = "source_authority_changed"
					current.FailureText = "the private Riff source changed before the render could be filed; send the request again from current context"
				})
				if changed {
					app.resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy)
				}
				return
			}
		}
		artifact, asset, err := app.fileScoutChatImageArtifact(thread, pendingMessageID, prompt, state.ResultRef, state.ResultMime, createdBy)
		if err != nil {
			if errors.Is(err, errScoutChatImageArtifactBinding) {
				log.Errorf("scout chat image: quarantined mismatched generation artifact for %s", pendingMessageID)
				return
			}
			if strings.TrimSpace(artifact.ID) != "" {
				_, _ = app.transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseGenerated}, func(current *scoutChatImageGenerationState) {
					current.Phase = scoutChatImagePhaseCleanupRequired
					current.ArtifactID = artifact.ID
					current.FailureClass = "filing_failed"
					current.FailureText = "the render generated but could not be filed"
				})
				app.resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy)
				return
			}
			_, _ = app.transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseGenerated}, func(current *scoutChatImageGenerationState) {
				current.Phase = scoutChatImagePhaseProviderFailed
				current.FailureClass = "filing_failed"
				current.FailureText = "the render generated but could not be filed"
			})
			app.resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy)
			return
		}
		stored, _ := app.transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseGenerated}, func(current *scoutChatImageGenerationState) {
			current.Phase = scoutChatImagePhaseArtifactFiled
			current.ArtifactID = artifact.ID
			current.ResultRef = asset.Ref
			current.ResultMime = asset.Mime
		})
		if stored {
			app.resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy)
		}
	case scoutChatImagePhaseArtifactFiled:
		var replyTo *scoutChatReplyRef
		if pending.ReplyTo != nil {
			copy := *pending.ReplyTo
			replyTo = &copy
		}
		message := scoutChatMessageRecord{ID: fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()), Kind: scoutChatMessageKindImage, Role: "scout", Text: "here's the concept render.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ReplyTo: replyTo, Image: &scoutChatImageRef{Ref: state.ResultRef, Mime: state.ResultMime, Name: "concept-render" + imageryAssetExtension(state.ResultMime), ArtifactID: state.ArtifactID, Prompt: prompt, GenerationID: pendingMessageID, ReplacesMessageID: state.ReplacesMessageID}}
		if _, err := app.commitScoutChatImageCompletion(requesterEmail, threadID, pendingMessageID, prompt, replyTo, message); err != nil {
			if errors.Is(err, errScoutChatImageAuthorityChanged) {
				_, _ = app.transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseArtifactFiled}, func(current *scoutChatImageGenerationState) {
					current.Phase = scoutChatImagePhaseCleanupRequired
					current.FailureClass = "authority_denied"
				})
				app.resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy)
			}
		}
	case scoutChatImagePhaseProviderFailed:
		var replyTo *scoutChatReplyRef
		if pending.ReplyTo != nil {
			copy := *pending.ReplyTo
			replyTo = &copy
		}
		message := scoutChatImageErrorMessage(replyTo, errors.New(firstNonEmptyString(state.FailureText, "image generation failed")))
		if _, err := app.commitScoutChatImageCompletion(requesterEmail, threadID, pendingMessageID, prompt, replyTo, message); err != nil && errors.Is(err, errScoutChatImageAuthorityChanged) {
			_, _ = app.transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseProviderFailed}, func(current *scoutChatImageGenerationState) { current.Phase = scoutChatImagePhaseCleanupRequired })
			app.resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy)
		}
	case scoutChatImagePhaseCleanupRequired:
		artifactID := strings.TrimSpace(state.ArtifactID)
		if artifactID != "" {
			thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
			if err != nil {
				return
			}
			if artifact, stillPresent := app.osArtifactByID(artifactID); stillPresent {
				if artifact.Metadata["source"] != "chat_image" || artifact.Metadata["generationId"] != pendingMessageID ||
					artifact.Metadata["originSurface"] != "chat:"+threadID || normalizeAccountEmail(artifact.Metadata["ownerEmail"]) != normalizeAccountEmail(thread.OwnerEmail) ||
					artifact.Metadata["visibility"] != normalizeScoutChatVisibility(thread.Visibility) || artifactIsPublished(artifact) {
					log.Errorf("scout chat image: refusing cleanup of mismatched artifact %s", artifactID)
					return
				}
			}
			_, _, _, _ = deleteScoutChatImageArtifact(app, artifactID)
			if _, stillPresent := app.osArtifactByID(artifactID); stillPresent {
				return
			}
		}
		if state.FailureClass == "filing_failed" {
			changed, _ := app.transitionScoutChatImagePending(ownerEmail, threadID, pendingMessageID, prompt, []string{scoutChatImagePhaseCleanupRequired}, func(current *scoutChatImageGenerationState) {
				current.Phase = scoutChatImagePhaseProviderFailed
				current.ArtifactID = ""
			})
			if changed {
				app.resumeScoutChatImagePending(ownerEmail, threadID, pendingMessageID, createdBy)
			}
			return
		}
		_ = app.removeScoutChatImagePending(ownerEmail, threadID, pendingMessageID)
	}
}

// commitScoutChatImageError commits the friendly error bubble a failed render
// earns. A mapped OpenAI failure (the live prod 429 insufficient_quota, or a
// rate limit) uses openAIAPIRequestUserMessage so the raw upstream body never
// reaches the user; anything else uses the compacted error line.
func (app *kanbanBoardApp) commitScoutChatImageError(threadID string, ownerEmail string, replyTo *scoutChatReplyRef, err error) {
	message := scoutChatImageErrorMessage(replyTo, err)
	if _, commitErr := app.commitScoutChatThreadMessages(ownerEmail, threadID, message); commitErr != nil {
		log.Errorf("scout chat image: commit error message on thread %s failed: %v", threadID, commitErr)
	}
}

func scoutChatImageErrorMessage(replyTo *scoutChatReplyRef, err error) scoutChatMessageRecord {
	detail := scoutChatImageFriendlyDetail(err)
	return scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      "message",
		Role:      "error",
		Text:      "the concept render didn't go through — " + detail + ".",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ReplyTo:   replyTo,
	}

}

func scoutChatImageFriendlyDetail(err error) string {
	detail := scoutChatImageErrorDetail(err)
	if friendly, _, ok := openAIAPIRequestUserMessage(err); ok && strings.TrimSpace(friendly) != "" {
		return friendly
	}
	return detail
}

func (app *kanbanBoardApp) finishScoutChatImageError(requesterEmail, commitOwner, threadID, pendingMessageID, prompt string, replyTo *scoutChatReplyRef, err error) {
	message := scoutChatImageErrorMessage(replyTo, err)
	if pendingMessageID == "" {
		app.commitScoutChatImageError(threadID, requesterEmail, replyTo, err)
		return
	}
	if _, commitErr := app.commitScoutChatImageCompletion(requesterEmail, threadID, pendingMessageID, prompt, replyTo, message); commitErr != nil {
		log.Errorf("scout chat image: final error authority changed on thread %s: %v", threadID, commitErr)
		_ = app.removeScoutChatImagePending(commitOwner, threadID, pendingMessageID)
		return
	}
	_ = app.removeScoutChatImagePending(commitOwner, threadID, pendingMessageID)
}

func scoutChatImageErrorDetail(err error) string {
	if err == nil {
		return "image generation failed"
	}
	// Do not expose the provider URL or Go's transport wording in the feed. A
	// timeout is safe to retry manually; automatic retry could double-bill when
	// the provider completed the image but the response arrived too late.
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "image generation took too long; please try the request again"
	}
	return compactAssistantLine(err.Error())
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

func scoutChatImageReplyEqual(left, right *scoutChatReplyRef) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// commitScoutChatImageCompletion is the final-use authority boundary. It
// holds the exact chat lock while re-reading the pending request, requester
// membership, archive state, and reply target, then persists and publishes the
// image before releasing that authority. A late provider response can never
// escape into a channel top level after its thread or audience changed.
func (app *kanbanBoardApp) commitScoutChatImageCompletion(requesterEmail, threadID, pendingMessageID, prompt string, replyTo *scoutChatReplyRef, message scoutChatMessageRecord) (scoutChatThreadRecord, error) {
	// A Riff image has two mutable authorities: its private destination and the
	// public checkpoint it was derived from. Resolve the source id without using
	// its body, then hold both per-thread locks in lexical order through the
	// final commit. This closes the edit-after-reauthorization gap without ever
	// holding the public source lock across the provider call.
	preflight, _, preflightErr := app.scoutChatThreadByID(requesterEmail, threadID)
	if preflightErr != nil {
		return scoutChatThreadRecord{}, errScoutChatImageAuthorityChanged
	}
	preflightRiff := preflight.Riff != nil
	sourceThreadID := ""
	if preflightRiff {
		sourceThreadID = strings.TrimSpace(preflight.Riff.SourceThreadID)
		if sourceThreadID == "" || sourceThreadID == strings.TrimSpace(threadID) {
			return scoutChatThreadRecord{}, errScoutChatImageAuthorityChanged
		}
	}
	firstID, secondID := strings.TrimSpace(threadID), sourceThreadID
	if secondID != "" && secondID < firstID {
		firstID, secondID = secondID, firstID
	}
	firstLock := app.scoutChatThreadLock(firstID)
	firstLock.Lock()
	unlockSecond := func() {}
	if secondID != "" {
		secondLock := app.scoutChatThreadLock(secondID)
		secondLock.Lock()
		unlockSecond = secondLock.Unlock
	}
	locked := true
	unlock := func() {
		if !locked {
			return
		}
		unlockSecond()
		firstLock.Unlock()
		locked = false
	}
	defer unlock()

	thread, _, err := app.scoutChatThreadByID(requesterEmail, threadID)
	if err != nil || thread.ArchivedAt != "" || !scoutChatThreadAllowsViewer(thread, requesterEmail) ||
		(thread.Riff != nil) != preflightRiff || thread.Riff != nil && strings.TrimSpace(thread.Riff.SourceThreadID) != sourceThreadID {
		unlock()
		return scoutChatThreadRecord{}, errScoutChatImageAuthorityChanged
	}
	pendingIndex := scoutChatMessageIndex(thread, pendingMessageID)
	if pendingIndex < 0 {
		unlock()
		return scoutChatThreadRecord{}, errScoutChatImageAuthorityChanged
	}
	pending := thread.Messages[pendingIndex]
	phase := scoutChatImagePhase(pending.ImageGeneration)
	wantPhase := scoutChatImagePhaseProviderFailed
	if message.Kind == scoutChatMessageKindImage {
		wantPhase = scoutChatImagePhaseArtifactFiled
	}
	if pending.Kind != scoutChatMessageKindImagePending || pending.ImageGeneration == nil ||
		pending.ImageGeneration.Status != scoutChatImageGenerationStatusGenerating ||
		phase != wantPhase ||
		strings.TrimSpace(pending.ImageGeneration.Prompt) != strings.TrimSpace(prompt) ||
		normalizeAccountEmail(pending.ImageGeneration.RequestedByEmail) != normalizeAccountEmail(requesterEmail) ||
		!scoutChatImageReplyEqual(pending.ReplyTo, replyTo) {
		unlock()
		return scoutChatThreadRecord{}, errScoutChatImageAuthorityChanged
	}
	if replyTo != nil && scoutChatMessageIndex(thread, replyTo.MessageID) < 0 {
		unlock()
		return scoutChatThreadRecord{}, errScoutChatImageAuthorityChanged
	}
	message.ReplyTo = replyTo
	if thread.Riff != nil {
		if message.Kind == scoutChatMessageKindImage {
			if _, _, sourceErr := app.privateRiffWorkSourceWindow(requesterEmail, thread, pending); sourceErr != nil {
				unlock()
				return scoutChatThreadRecord{}, errScoutChatImageAuthorityChanged
			}
		}
		message.AuthorName = firstNonEmptyString(strings.TrimSpace(message.AuthorName), scoutParticipantName)
		message.RiffEpisodeID = pending.RiffEpisodeID
		message.RiffCheckpointID = pending.RiffCheckpointID
		authority, authorityErr := privateRiffMessageAuthorityForThread(thread, message)
		if authorityErr != nil {
			unlock()
			return scoutChatThreadRecord{}, errScoutChatImageAuthorityChanged
		}
		message.RiffAuthority = authority
	}
	removed := []scoutChatMessageRecord{pending}
	filtered := make([]scoutChatMessageRecord, 0, len(thread.Messages))
	for _, candidate := range thread.Messages {
		if candidate.ID == pendingMessageID {
			continue
		}
		if message.Kind == scoutChatMessageKindImage && pending.ImageGeneration.ReplacesMessageID != "" && candidate.ID == pending.ImageGeneration.ReplacesMessageID && candidate.Kind == scoutChatMessageKindImage && candidate.Image != nil {
			removed = append(removed, candidate)
			continue
		}
		filtered = append(filtered, candidate)
	}
	thread.Messages = append(filtered, message)
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, message)
	if store := currentHomeProjectStore(); store != nil {
		for _, removedMessage := range removed {
			if removedMessage.Kind == scoutChatMessageKindImage && removedMessage.Image != nil {
				if err := store.invalidateProjectChatReplyParentsByLegacyMutation(context.Background(), thread.ID, removedMessage.ID, "parent_regenerated"); err != nil {
					unlock()
					return scoutChatThreadRecord{}, err
				}
			}
		}
	}
	if err := saveScoutChatImageThread(app, thread); err != nil {
		unlock()
		return scoutChatThreadRecord{}, err
	}
	for _, removedMessage := range removed {
		app.observeSTRIDETeamChatMessage(thread, removedMessage, "delete", requesterEmail)
		deliverScoutChatThreadDeletion(thread, removedMessage.ID)
	}
	app.observeSTRIDETeamChatMessage(thread, message, "message", "")
	deliverScoutChatThreadUpdate(thread, message)
	app.rebuildPrivateConversationContinuity(thread, "message")
	unlock()
	for _, removedMessage := range removed {
		if removedMessage.Kind == scoutChatMessageKindImage {
			app.discardUnsavedScoutChatImageArtifact(removedMessage.Image)
		}
	}
	return thread, nil
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
	if store := currentHomeProjectStore(); store != nil && replacedImage != nil {
		if err := store.invalidateProjectChatReplyParentsByLegacyMutation(context.Background(), thread.ID, replacesMessageID, "parent_regenerated"); err != nil {
			lock.Unlock()
			return err
		}
	}
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
	if app == nil || app.memory == nil {
		return
	}
	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok {
			continue
		}
		for _, message := range thread.Messages {
			if message.Kind != scoutChatMessageKindImagePending || message.ImageGeneration == nil || strings.TrimSpace(message.ImageGeneration.Prompt) == "" {
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
			requesterName := firstNonEmptyString(strings.TrimSpace(message.ImageGeneration.RequestedByName), thread.CreatedBy, requesterEmail)
			phase := scoutChatImagePhase(message.ImageGeneration)
			if phase == scoutChatImagePhaseQueued && openAIImageGenerationAvailable() {
				app.startScoutChatImageGeneration(thread.ID, requesterEmail, message.ImageGeneration.Prompt, requesterName, message.ID)
				continue
			}
			app.resumeScoutChatImagePending(thread.OwnerEmail, thread.ID, message.ID, requesterName)
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
	if err := saveScoutChatImageThread(app, thread); err != nil {
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
	var replyTo *scoutChatReplyRef
	if oldMessage.ReplyTo != nil {
		copy := *oldMessage.ReplyTo
		replyTo = &copy
	}
	pending := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      scoutChatMessageKindImagePending,
		Role:      "scout",
		Text:      "generating image…",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ReplyTo:   replyTo,
		ImageGeneration: &scoutChatImageGenerationState{
			Status:            scoutChatImageGenerationStatusGenerating,
			Phase:             scoutChatImagePhaseQueued,
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
