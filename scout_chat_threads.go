package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	scoutChatThreadRequestLimit = 768 << 10
	scoutChatMaxFilesPerMessage = 6
	scoutChatMaxFileTextBytes   = 64 << 10
)

// Chat has exactly two audiences, and this is doctrine, not a gap (card 070):
//
//   - private = the owner + Scout, and NOBODY else. Enforced on every read by
//     scoutChatThreadsSnapshot and scoutChatThreadByID (a non-owner is denied
//     unless the thread is public).
//   - public  = an office channel every signed-in user can read and post to.
//
// There are deliberately NO human-to-human 1:1 DMs: the office is the shared
// surface, so "message a person privately" routes through a public #channel
// (with an @mention) or through each person's own private Scout. The "dm"
// alias accepted by startChatAsUser therefore resolves to the REQUESTER'S OWN
// Scout thread — it never opens a cross-user private channel. Team ratification
// pending; the code already behaves this way and these constants pin it.
const (
	scoutChatVisibilityPrivate = "private"
	scoutChatVisibilityPublic  = "public"
)

// normalizeScoutChatVisibility maps any stored/submitted value onto the two
// sanctioned visibilities. Empty (all pre-channel threads on disk) stays
// private for backward compatibility.
func normalizeScoutChatVisibility(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), scoutChatVisibilityPublic) {
		return scoutChatVisibilityPublic
	}
	return scoutChatVisibilityPrivate
}

func scoutChatThreadVisibility(thread scoutChatThreadRecord) string {
	return normalizeScoutChatVisibility(thread.Visibility)
}

// scoutChatMentionsScout gates Scout in public channels: humans talk to each
// other by default and only summon the model with an explicit @scout mention.
func scoutChatMentionsScout(text string) bool {
	return strings.Contains(strings.ToLower(text), "@scout")
}

type scoutChatFileAttachment struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
	Size int64  `json:"size,omitempty"`
	Text string `json:"text,omitempty"`
	// Ref + Mime (card 085): the content-addressed blob (blobs.go) carrying
	// the file's real bytes, set by the composer after its upload to
	// /assistant/attachments. sanitizeScoutChatFiles validates the ref
	// against the store and stamps Mime from the PINNED sidecar — never the
	// client's claim — and a ref'd binary never keeps client-supplied Text
	// (its Text is the server-derived transcription only).
	Ref  string `json:"ref,omitempty"`
	Mime string `json:"mime,omitempty"`
}

type scoutChatThreadRef struct {
	ID         string `json:"id"`
	Mode       string `json:"mode"`
	Query      string `json:"query"`
	Status     string `json:"status"`
	ArtifactID string `json:"artifactId,omitempty"`
}

type scoutChatMessageRecord struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Role        string `json:"role"`
	Text        string `json:"text,omitempty"`
	CreatedAt   string `json:"createdAt"`
	AuthorName  string `json:"authorName,omitempty"`
	AuthorEmail string `json:"authorEmail,omitempty"`
	// Via marks messages relayed by a tool (e.g. "scout_voice" for
	// post_to_channel from the private dashboard voice).
	Via string `json:"via,omitempty"`
	// PostedOnBehalfOf is the disclosure stamp: when Scout posts a message as a
	// user (start_chat_as_user), this carries that user's email UNCONDITIONALLY
	// — it is set server-side, never from a model argument, so Scout can never
	// silently impersonate. The client renders a visible "via Scout" chip
	// whenever this is present.
	PostedOnBehalfOf string                    `json:"postedOnBehalfOf,omitempty"`
	Files            []scoutChatFileAttachment `json:"files,omitempty"`
	Thread           *scoutChatThreadRef       `json:"thread,omitempty"`
	// Proposal carries a router proposal card (Kind "proposal") — DATA the
	// client renders as the confirmation trust surface, never an action. See
	// the propose-confirm router in scout_chat.go.
	Proposal *scoutRouterProposal `json:"proposal,omitempty"`
	// Choices carries a quick-reply question card (Kind "choices") — Scout's
	// one clarifying question with 2-4 pill options. Same law as Proposal:
	// DATA the client renders; a tap sends a reply, never launches.
	Choices *scoutChatChoices `json:"choices,omitempty"`
	// Manifest carries the package manifest card (Kind "manifest") — the
	// shipped/held deliverable handover a packaging_studio ship_approval
	// posts (goal_manifest.go). Same law again: persisted DATA the client
	// renders, so reloads show the same card.
	Manifest *scoutChatManifest `json:"manifest,omitempty"`
	// Image carries a generated concept render (Kind "image", card 096): the
	// content-addressed blob ref plus its filed artifact id, so the picture
	// renders inline via the session-gated /artifacts/blob route on every
	// reload. Persisted DATA, the Proposal/Choices/Manifest pattern.
	Image *scoutChatImageRef `json:"image,omitempty"`
}

type scoutChatThreadRecord struct {
	ID         string                   `json:"id"`
	Title      string                   `json:"title"`
	Preview    string                   `json:"preview"`
	OwnerEmail string                   `json:"ownerEmail"`
	CreatedBy  string                   `json:"createdBy,omitempty"`
	Visibility string                   `json:"visibility,omitempty"`
	CreatedAt  string                   `json:"createdAt"`
	UpdatedAt  string                   `json:"updatedAt"`
	ArchivedAt string                   `json:"archivedAt,omitempty"`
	Messages   []scoutChatMessageRecord `json:"messages,omitempty"`
}

func assistantChatThreadsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "chat threads are unavailable")
		return
	}

	switch r.Method {
	case http.MethodGet:
		includeArchived := strings.EqualFold(r.URL.Query().Get("archived"), "true") || strings.EqualFold(r.URL.Query().Get("includeArchived"), "true")
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"threads": kanbanApp.scoutChatThreadsSnapshot(user.Email, includeArchived, 100),
		})
	case http.MethodPost:
		payload := struct {
			Title      string `json:"title"`
			Visibility string `json:"visibility"`
		}{}
		if r.Body != nil {
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload)
		}
		thread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, payload.Title, payload.Visibility)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Fan the new thread out like the voice create path and renames do —
		// without this, a channel created from the + button never reaches
		// peers' sidebars until its first message forces a list refresh.
		if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
			broadcastOfficeKanbanEvent("chat_thread", scoutChatThreadEventPayload(thread))
		} else {
			// private threads only need the owner's OTHER tabs to learn of it
			sendKanbanEventToUser(thread.OwnerEmail, "chat_thread", scoutChatThreadEventPayload(thread))
		}
		writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "thread": thread})
	}
}

func assistantChatThreadHandler(w http.ResponseWriter, r *http.Request) {
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "chat threads are unavailable")
		return
	}

	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/assistant/chat-threads/"), "/")
	parts := strings.Split(suffix, "/")
	if suffix == "" || len(parts) > 3 {
		http.NotFound(w, r)
		return
	}
	threadID := parts[0]
	if threadID == "" {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 1 && r.Method == http.MethodPatch {
		payload := struct {
			Archived *bool   `json:"archived"`
			Title    *string `json:"title"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read thread update")
			return
		}
		// A title payload is a rename (D7); otherwise archived keeps its
		// legacy default-true semantics so existing callers stay intact.
		if payload.Title != nil {
			thread, err := kanbanApp.renameScoutChatThread(user.Email, threadID, *payload.Title)
			if err != nil {
				writeScoutChatThreadError(w, err)
				return
			}
			writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "thread": thread})
			return
		}
		archived := true
		if payload.Archived != nil {
			archived = *payload.Archived
		}
		thread, err := kanbanApp.setScoutChatThreadArchived(user.Email, threadID, archived)
		if err != nil {
			writeScoutChatThreadError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "thread": thread})
		return
	}

	if len(parts) == 2 && parts[1] == "proposal" && r.Method == http.MethodPost {
		var action scoutChatProposalAction
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&action); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read proposal action")
			return
		}
		response, err := kanbanApp.resolveScoutChatProposal(r.Context(), user, threadID, action)
		if err != nil {
			writeScoutChatThreadError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, response)
		return
	}

	if len(parts) == 2 && parts[1] == "choice" && r.Method == http.MethodPost {
		var action scoutChatChoiceAction
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&action); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read choice selection")
			return
		}
		response, err := kanbanApp.resolveScoutChatChoice(r.Context(), user, threadID, action)
		if err != nil {
			writeScoutChatThreadError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, response)
		return
	}

	if len(parts) == 2 && parts[1] == "messages" && r.Method == http.MethodPost {
		payload := struct {
			Text               string                    `json:"text"`
			Files              []scoutChatFileAttachment `json:"files"`
			FollowUpArtifactId string                    `json:"followUpArtifactId"`
			ToolTemplate       string                    `json:"toolTemplate"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, scoutChatThreadRequestLimit)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read chat message")
			return
		}
		response, err := kanbanApp.appendScoutChatThreadMessageWithTool(r.Context(), user, threadID, payload.Text, payload.Files, payload.FollowUpArtifactId, payload.ToolTemplate)
		if err != nil {
			writeScoutChatThreadError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, response)
		return
	}

	if len(parts) == 3 && parts[1] == "messages" && r.Method == http.MethodDelete {
		thread, err := kanbanApp.deleteScoutChatThreadMessage(user.Email, threadID, parts[2])
		if err != nil {
			writeScoutChatThreadError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "thread": thread})
		return
	}

	http.NotFound(w, r)
}

func writeScoutChatThreadError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if strings.Contains(err.Error(), "not found") {
		status = http.StatusNotFound
	}
	if strings.Contains(err.Error(), "archived") {
		status = http.StatusConflict
	}
	if strings.Contains(err.Error(), "your own") {
		status = http.StatusForbidden
	}
	writeAuthError(w, status, err.Error())
}

func (app *kanbanBoardApp) createScoutChatThread(ownerEmail string, createdBy string, title string, visibility string) (scoutChatThreadRecord, error) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, fmt.Errorf("chat thread memory is unavailable")
	}
	now := time.Now().UTC()
	ownerEmail = normalizeAccountEmail(ownerEmail)
	if ownerEmail == "" {
		return scoutChatThreadRecord{}, fmt.Errorf("thread owner is required")
	}
	createdBy = canonicalRoomActorName(createdBy)
	visibility = normalizeScoutChatVisibility(visibility)
	defaultTitle := "Scout"
	defaultPreview := "new chat thread"
	if visibility == scoutChatVisibilityPublic {
		defaultTitle = "team channel"
		defaultPreview = "new team channel"
	}
	thread := scoutChatThreadRecord{
		ID:         fmt.Sprintf("scout-chat-%d", now.UnixNano()),
		Title:      firstNonEmptyString(strings.TrimSpace(title), defaultTitle),
		Preview:    defaultPreview,
		OwnerEmail: ownerEmail,
		CreatedBy:  createdBy,
		Visibility: visibility,
		CreatedAt:  now.Format(time.RFC3339Nano),
		UpdatedAt:  now.Format(time.RFC3339Nano),
	}
	entryText, err := encodeScoutChatThread(thread)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	_, _, err = app.memory.appendScoutChatThread(thread.ID, entryText, scoutChatThreadMetadata(thread))
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	return thread, nil
}

func (app *kanbanBoardApp) appendScoutChatThreadMessage(ctx context.Context, user *userAccount, threadID string, text string, files []scoutChatFileAttachment, followUpArtifactID string) (map[string]any, error) {
	return app.appendScoutChatThreadMessageWithTool(ctx, user, threadID, text, files, followUpArtifactID, "")
}

// appendScoutChatThreadMessageWithTool is appendScoutChatThreadMessage plus an
// optional palette tool template. The palette's conversational tiles hand off
// to the composer; carrying tool.id through the send is the §2 fidelity fix —
// the same tool must produce the same contract-gated output from the talk-it-out
// door as from the Run door.
func (app *kanbanBoardApp) appendScoutChatThreadMessageWithTool(ctx context.Context, user *userAccount, threadID string, text string, files []scoutChatFileAttachment, followUpArtifactID string, toolTemplate string) (map[string]any, error) {
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return nil, err
	}
	if thread.ArchivedAt != "" {
		return nil, fmt.Errorf("chat thread is archived")
	}

	files = sanitizeScoutChatFiles(files)
	text = strings.TrimSpace(text)
	if text == "" && len(files) == 0 {
		return nil, fmt.Errorf("message text or attachment is required")
	}

	// Binary attachments (card 085): build the image/document blocks once,
	// then run the bounded derived-text pass BEFORE any commit so file.Text
	// carries what the model read on every path — history folding, channel
	// team replies, previews, and launch objectives all inherit it. Both are
	// best-effort and keyless-safe: only the Anthropic paths can see binary
	// blocks, so keyless deploys skip the blob reads entirely and keep
	// today's name-only behavior — the chips still render.
	var attachmentBlocks []json.RawMessage
	if currentAnthropicAPIKey() != "" {
		attachmentBlocks = attachmentContentBlocks(files)
		files = deriveAttachmentText(ctx, files, attachmentBlocks)
	}

	now := time.Now().UTC()
	userMessage := scoutChatMessageRecord{
		ID:          fmt.Sprintf("scout-chat-message-%d", now.UnixNano()),
		Kind:        "message",
		Role:        "user",
		Text:        text,
		CreatedAt:   now.Format(time.RFC3339Nano),
		AuthorName:  scoutChatAuthorName(user),
		AuthorEmail: normalizeAccountEmail(user.Email),
		Files:       files,
	}
	history := scoutChatHistoryFromThread(thread)

	// @-mention bell nudges are collaborative-channel behavior only, and only
	// for messages that actually persisted: every commit in this function goes
	// through commitUserMessage, which fires the mention notifications exactly
	// once, on the first successful save. Private threads stay a 1:1 with
	// Scout — nobody else can read them, so nobody gets paged into them.
	mentionsPending := scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic
	commitUserMessage := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		saved, err := app.commitScoutChatThreadMessages(user.Email, threadID, messages...)
		if err == nil && mentionsPending {
			mentionsPending = false
			app.notifyScoutChatMentions(saved, userMessage)
		}
		return saved, err
	}

	response := map[string]any{
		"ok":      true,
		"message": userMessage,
	}

	// A follow-up reply re-runs an existing agent-thread artifact in place
	// (agent_thread_followup.go). Explicit engagement: the armed target chip
	// counts as summoning Scout, so this branch runs regardless of channel
	// visibility and never needs @scout.
	if followUpArtifactID = strings.TrimSpace(followUpArtifactID); followUpArtifactID != "" {
		artifact, ok := app.osArtifactByID(followUpArtifactID)
		if !ok {
			return nil, fmt.Errorf("that report is unavailable")
		}
		// Wave 6 drop (deliverables drawer): a deliverable dropped into a
		// thread that never referenced it gets its card ADDED — a Kind
		// "thread" ref committed BEFORE the launch, so the run's status flips
		// (keyed on Thread.ID) land on it — instead of a rejection. Only
		// PERMANENTLY un-routable deliverables refuse before the card exists:
		// its copy promises feedback re-runs the work, and that promise must
		// hold. The add is deduped inside the per-thread lock (a goal's many
		// deliverables collapse onto its one live goalcard), and that lock is
		// released before the dispatch below (the launch takes it again to
		// flip the card to running). A drop whose launch then fails
		// transiently leaves the card in place: the drop itself happened.
		if err := app.artifactFollowUpRouteError(artifact); err != nil {
			return nil, err
		}
		saved, err := app.commitScoutChatThreadArtifactRef(user.Email, threadID, app.scoutChatArtifactRefMessage(artifact))
		if err != nil {
			return nil, err
		}
		thread = saved
		completedAt := firstNonEmptyString(artifact.Metadata["completedAt"], artifact.Metadata["updatedAt"])
		// Unattached channel messages posted after the last run become worker
		// context alongside the explicit reply.
		teamReplies := scoutChatRepliesSince(thread, completedAt)
		agentThread, err := app.dispatchArtifactFollowUpWithAttachments(followUpArtifactID, text, user.Name, teamReplies, attachmentBlocks)
		if err != nil {
			// The reply is a real team answer even when the run cannot launch
			// (e.g. a second teammate answering while a follow-up is already in
			// flight): commit it as a plain message so it survives in the
			// channel history and feeds the NEXT run's team-reply context, then
			// surface the launch error.
			if _, commitErr := commitUserMessage(userMessage); commitErr != nil {
				log.Errorf("Failed to commit follow-up reply after launch rejection: %v", commitErr)
			}
			return nil, err
		}
		// A plain status message, NOT a new Kind "thread" card: the existing
		// card flips via updateScoutChatThreadRefs; a second card would
		// duplicate the artifact key in renderActiveScoutThread. A goal resume
		// gets goal-flavored copy — goals carry no threadVersion, and the card
		// above is the live goalcard, not a versioned report.
		statusText := ""
		if agentThread.Mode == "goal" {
			statusText = "feedback sent — the goal is revising that deliverable; the card above will update"
		} else {
			version := firstNonEmptyString(strings.TrimSpace(agentThread.Artifact.Metadata["threadVersion"]), "2")
			statusText = assistantToolLabel(agentThread.Mode) + " follow-up v" + version + " running — the card above will update"
		}
		statusMessage := scoutChatMessageRecord{
			ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
			Kind:      "message",
			Role:      "scout",
			Text:      statusText,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		saved, err = commitUserMessage(userMessage, statusMessage)
		if err != nil {
			return nil, err
		}
		response["answer"] = statusMessage
		response["thread"] = saved
		response["agentThread"] = agentThread
		response["artifact"] = agentThread.Artifact
		response["actions"] = agentThread.Actions
		return response, nil
	}

	// A palette conversational handoff armed a tool template: launch the
	// tool's base mode with toolTemplate stamped on the artifact, so the run
	// resolves through the SAME toolPromptForThread machinery a palette Run or
	// /goal deliverable uses (assembled wrapper prompt + gate rubric) instead
	// of the generic per-mode contract. The palette tap is itself the explicit
	// invocation, so — like an armed follow-up target — this branch runs
	// regardless of channel visibility and never needs @scout.
	if toolTemplate = strings.TrimSpace(toolTemplate); toolTemplate != "" {
		originKind := agentThreadOriginPrivateThread
		if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
			originKind = agentThreadOriginChannel
		}
		// A PROCESS id launches the goal pipeline — the identical spec the
		// palette Run and /goal post — never a single agent thread: a process
		// is staged, checkpointed work the goal engine owns.
		if process, isProcess := processByID(toolTemplate); isProcess {
			objective := firstNonBlank(text, process.Title)
			goalThread, err := app.launchGoalThread(goalLaunchSpec{
				Objective:    objective,
				CreatedBy:    user.Name,
				Authority:    process.Authority,
				ToolTemplate: process.ID,
				Origin: map[string]string{
					"originKind":    originKind,
					"originId":      threadID,
					"originSurface": "chat:" + threadID,
					"requestedBy":   normalizeAccountEmail(user.Email),
				},
			})
			if err != nil {
				return nil, err
			}
			assistantMessage := scoutChatMessageRecord{
				ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
				Kind:      "thread",
				Role:      "scout",
				Text:      process.Title + " launched — the staged process is running; it will park here at each human checkpoint",
				CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
				Thread: &scoutChatThreadRef{
					ID:         goalThread.ID,
					Mode:       goalThread.Mode,
					Query:      goalThread.Query,
					Status:     goalThread.Status,
					ArtifactID: goalThread.Artifact.ID,
				},
			}
			saved, err := commitUserMessage(userMessage, assistantMessage)
			if err != nil {
				return nil, err
			}
			response["answer"] = assistantMessage
			response["thread"] = saved
			response["agentThread"] = goalThread
			response["artifact"] = goalThread.Artifact
			response["actions"] = goalThread.Actions
			return response, nil
		}
		tool, ok := toolByID(toolTemplate)
		if !ok {
			return nil, fmt.Errorf("unknown tool template %q", toolTemplate)
		}
		objective := firstNonBlank(text, tool.Name)
		agentThread, err := app.launchAgentThreadWithSpec(tool.Mode, objective, user.Name, map[string]string{
			"originKind": originKind,
			"originId":   threadID,
		}, agentThreadGoalSpec{
			Objective:     objective,
			ToolTemplate:  tool.ID,
			OriginSurface: "chat:" + threadID,
			RequestedBy:   normalizeAccountEmail(user.Email),
			Authority:     tool.Authority,
		})
		if err != nil {
			return nil, err
		}
		assistantMessage := scoutChatMessageRecord{
			ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
			Kind:      "thread",
			Role:      "scout",
			Text:      tool.Name + " launched — running against its output contract and gate rubric",
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Thread: &scoutChatThreadRef{
				ID:         agentThread.ID,
				Mode:       agentThread.Mode,
				Query:      agentThread.Query,
				Status:     agentThread.Status,
				ArtifactID: agentThread.Artifact.ID,
			},
		}
		saved, err := commitUserMessage(userMessage, assistantMessage)
		if err != nil {
			return nil, err
		}
		response["answer"] = assistantMessage
		response["thread"] = saved
		response["agentThread"] = agentThread
		response["artifact"] = agentThread.Artifact
		response["actions"] = agentThread.Actions
		return response, nil
	}

	// Public channels are human-to-human by default: Scout (answers and
	// agent-mode keyword launches alike) only engages on an explicit @scout
	// mention. Private threads keep the always-answer behavior.
	scoutEngaged := scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || scoutChatMentionsScout(text)
	if !scoutEngaged {
		saved, err := commitUserMessage(userMessage)
		if err != nil {
			return nil, err
		}
		response["thread"] = saved
		return response, nil
	}

	modelQuery := scoutChatMessageModelText(userMessage)
	// Channels launch agent runs only on an explicit "mode:" prefix or an
	// @scout mention + workstream keyword — the mention is itself the
	// invocation. Private threads NEVER keyword-launch: the propose-confirm
	// router below replaced scoutChatThreadModeForText (spec §2 — the only
	// silent heavy invoke in the system, retired).
	mode := ""
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		mode = scoutChatThreadModeForChannelText(text)
	}
	if mode != "" {
		originKind := agentThreadOriginPrivateThread
		if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
			originKind = agentThreadOriginChannel
		}
		agentThread, err := app.launchAgentThreadWithOrigin(mode, text, user.Name, map[string]string{
			"originKind": originKind,
			"originId":   threadID,
		})
		if err != nil {
			return nil, err
		}
		replyText := assistantToolLabel(mode) + " thread launched"
		if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
			if designReply := scoutWorkstreamReplyText(mode); designReply != "" {
				replyText = designReply
			}
		}
		assistantMessage := scoutChatMessageRecord{
			ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
			Kind:      "thread",
			Role:      "scout",
			Text:      replyText,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Thread: &scoutChatThreadRef{
				ID:         agentThread.ID,
				Mode:       agentThread.Mode,
				Query:      agentThread.Query,
				Status:     agentThread.Status,
				ArtifactID: agentThread.Artifact.ID,
			},
		}
		saved, err := commitUserMessage(userMessage, assistantMessage)
		if err != nil {
			return nil, err
		}
		response["answer"] = assistantMessage
		response["thread"] = saved
		response["agentThread"] = agentThread
		response["artifact"] = agentThread.Artifact
		response["actions"] = agentThread.Actions
		return response, nil
	}

	// One routing turn for private threads (the typed twin of voice
	// initiate_goal): the router may PROPOSE a tool run or a workstream, or
	// ASK one clarifying question with quick-reply pills — both are DATA
	// committed on the reply, NEVER a launch. The user's explicit confirm
	// posts the identical spec the palette Run posts (POST /assistant/goal
	// for tool runs, the proposal route for workstreams); a pill tap goes
	// through the choice route, which at most ARMS a proposal card. Keyless
	// deploys skip the turn inside routeScoutChatTurn and keep plain Q&A.
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		if verdict := app.routeScoutChatTurn(ctx, modelQuery, history); verdict != nil {
			if proposal := verdict.proposal; proposal != nil {
				proposalMessage := scoutChatMessageRecord{
					ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
					Kind:      scoutChatMessageKindProposal,
					Role:      "scout",
					Text:      proposal.Summary,
					CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
					Proposal:  proposal,
				}
				saved, err := commitUserMessage(userMessage, proposalMessage)
				if err != nil {
					return nil, err
				}
				response["answer"] = proposalMessage
				response["proposal"] = proposal
				response["thread"] = saved
				return response, nil
			}
			if choices := verdict.choices; choices != nil {
				choicesMessage := scoutChatMessageRecord{
					ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
					Kind:      scoutChatMessageKindChoices,
					Role:      "scout",
					Text:      choices.Question,
					CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
					Choices:   choices,
				}
				saved, err := commitUserMessage(userMessage, choicesMessage)
				if err != nil {
					return nil, err
				}
				response["answer"] = choicesMessage
				response["choices"] = choices
				response["thread"] = saved
				return response, nil
			}
		}
	}

	result, err := app.resolveAssistantQueryContextWithAttachments(ctx, modelQuery, history, attachmentBlocks)
	if err != nil {
		errorMessage := scoutChatMessageRecord{
			ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
			Kind:      "message",
			Role:      "error",
			Text:      err.Error(),
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		_, _ = commitUserMessage(userMessage, errorMessage)
		return nil, err
	}
	answer := strings.TrimSpace(result.answer)
	if answer == "" {
		answer = "no answer yet"
	}
	assistantMessage := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      "message",
		Role:      "scout",
		Text:      answer,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	saved, err := commitUserMessage(userMessage, assistantMessage)
	if err != nil {
		return nil, err
	}
	response["answer"] = assistantMessage
	response["thread"] = saved
	return response, nil
}

// scoutWorkstreamReplyText is the design-canon channel reply for the three
// public workstreams. The research line is verbatim; design/grill are adapted
// to honest launch tense — the prototype's replies claimed completed seed
// results ("final score 7.4") that a just-launched run cannot promise (D2).
func scoutWorkstreamReplyText(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "research":
		return "on it — research workstream kicked off. the brief lands in the library; i'll link it here when it's done."
	case "design":
		return "on it — design workstream kicked off. screens, states, and handoff questions land in the library."
	case "grill":
		return "on it — grill mode is running on the pitch. the scorecard lands in artifacts."
	}
	return ""
}

// scoutChatProposalAction is the POST /assistant/chat-threads/{id}/proposal
// body: the user's verdict on one router proposal card. The card, not this
// route, is the trust surface — this route records the verdict signal, flips
// the persisted card inert, and (workstreams only) performs the now-explicit
// launch. Tool-run confirms launch through POST /assistant/goal with the
// identical palette spec; this route never duplicates that door.
//
// Only Action, MessageID, and Objective are read server-side: the verdict
// resolves against the PERSISTED proposal record for MessageID, and Objective
// is honored only because the card lets the user edit it before confirming.
// Kind/ToolID/Mode/Query are still sent by older clients and deliberately
// ignored — trusting them let a fabricated post launch arbitrary workstreams
// and pollute the acceptance-rate signal.
type scoutChatProposalAction struct {
	Action    string `json:"action"` // accepted | dismissed
	Kind      string `json:"kind"`   // ignored — stored record wins
	ToolID    string `json:"toolId"` // ignored — stored record wins
	Mode      string `json:"mode"`   // ignored — stored record wins
	Objective string `json:"objective"`
	Query     string `json:"query"` // ignored — stored record wins
	MessageID string `json:"messageId"`
}

// resolveScoutChatProposal applies one accept/dismiss verdict. Claim first:
// the verdict binds to the PERSISTED proposal record (loaded by message id
// under the thread lock, still pending) — never to client-supplied
// kind/mode/toolId — so a replayed or double-posted action cannot launch a
// duplicate workstream, and a fabricated action for a proposal the router
// never made cannot pollute the accept/dismiss acceptance-rate signal (§2
// misfire economics — Q5 fuel from day one). Then the signal, then the side
// effect the verdict earns. A dismissal re-asks the STORED query through the
// normal Q&A path and commits only the scout answer — the user already said
// it once.
func (app *kanbanBoardApp) resolveScoutChatProposal(ctx context.Context, user *userAccount, threadID string, action scoutChatProposalAction) (map[string]any, error) {
	verb := strings.ToLower(strings.TrimSpace(action.Action))
	switch verb {
	case "accepted", "dismissed":
	default:
		return nil, fmt.Errorf("proposal action must be accepted or dismissed")
	}
	messageID := strings.TrimSpace(action.MessageID)
	if messageID == "" {
		return nil, fmt.Errorf("proposal message id is required")
	}

	// Atomically flip the still-pending card to its verdict and read back the
	// stored proposal. A message that carries no proposal, or one already
	// resolved, rejects HERE — before any signal is recorded or launch runs.
	proposal, err := app.claimScoutChatProposal(threadID, user.Email, messageID, verb)
	if err != nil {
		return nil, err
	}

	signalEvent, valence := signalEventRouterProposalAccepted, signalValencePositive
	if verb == "dismissed" {
		signalEvent, valence = signalEventRouterProposalDismissed, signalValenceNegative
	}
	// The objective is the ONE field the card lets the user edit before
	// confirming, so the request value wins over the stored one; kind, mode,
	// toolId, and query always ride the stored record.
	objective := firstNonBlank(strings.TrimSpace(action.Objective), strings.TrimSpace(proposal.Objective))
	app.recordSignalEvent(user.Name, signalEvent, valence, "", "", map[string]string{
		"toolId":    firstNonEmptyString(strings.TrimSpace(proposal.ToolID), strings.TrimSpace(proposal.Mode)),
		"objective": objective,
	})

	response := map[string]any{"ok": true}

	if verb == "accepted" {
		// Tier 1 only: the workstream confirm launches here — exactly the
		// explicit single-shot path channels use. Tier 2 (tool_run) launches
		// via POST /assistant/goal from the card's Run button; this branch
		// records its signal only.
		if strings.EqualFold(strings.TrimSpace(proposal.Kind), scoutRouterProposalKindWorkstream) {
			mode := strings.ToLower(strings.TrimSpace(proposal.Mode))
			switch mode {
			case "research", "design", "grill", "workflow":
			default:
				return nil, fmt.Errorf("workstream mode must be research, design, grill, or workflow")
			}
			if objective == "" {
				return nil, fmt.Errorf("workstream objective is required")
			}
			agentThread, err := app.launchAgentThreadWithOrigin(mode, objective, user.Name, map[string]string{
				"originKind": agentThreadOriginPrivateThread,
				"originId":   threadID,
			})
			if err != nil {
				return nil, err
			}
			assistantMessage := scoutChatMessageRecord{
				ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
				Kind:      "thread",
				Role:      "scout",
				Text:      assistantToolLabel(mode) + " workstream confirmed — running now",
				CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
				Thread: &scoutChatThreadRef{
					ID:         agentThread.ID,
					Mode:       agentThread.Mode,
					Query:      agentThread.Query,
					Status:     agentThread.Status,
					ArtifactID: agentThread.Artifact.ID,
				},
			}
			saved, err := app.commitScoutChatThreadMessages(user.Email, threadID, assistantMessage)
			if err != nil {
				return nil, err
			}
			response["answer"] = assistantMessage
			response["thread"] = saved
			response["agentThread"] = agentThread
			response["artifact"] = agentThread.Artifact
			response["actions"] = agentThread.Actions
		}
		// Concept render (card 096): the confirm is the explicit generate. The
		// image call runs 30-90s, so NEVER inside this HTTP request — commit an
		// activity line now and hand off to the async runner; the finished
		// picture lands as a Kind=image message over the owner's live socket
		// (or the 12s chat poll), and a failure lands as a friendly error bubble.
		if strings.EqualFold(strings.TrimSpace(proposal.Kind), scoutRouterProposalKindImage) {
			if objective == "" {
				return nil, fmt.Errorf("image prompt is required")
			}
			statusMessage := scoutChatMessageRecord{
				ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
				Kind:      "message",
				Role:      "scout",
				Text:      "concept render started — it lands here when it's finished",
				CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			}
			saved, err := app.commitScoutChatThreadMessages(user.Email, threadID, statusMessage)
			if err != nil {
				return nil, err
			}
			startScoutChatImageAsync(app, threadID, user.Email, objective, user.Name)
			response["answer"] = statusMessage
			response["thread"] = saved
		}
		return response, nil
	}

	// Dismissed: the "just answer instead" escape re-asks the STORED query
	// (the message that produced the proposal) as Tier 0.
	query := strings.TrimSpace(proposal.Query)
	if query == "" {
		return response, nil
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return nil, err
	}
	result, err := app.resolveAssistantQueryContext(ctx, query, scoutChatHistoryFromThread(thread))
	if err != nil {
		return nil, err
	}
	answer := strings.TrimSpace(result.answer)
	if answer == "" {
		answer = "no answer yet"
	}
	assistantMessage := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      "message",
		Role:      "scout",
		Text:      answer,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	saved, err := app.commitScoutChatThreadMessages(user.Email, threadID, assistantMessage)
	if err != nil {
		return nil, err
	}
	response["answer"] = assistantMessage
	response["thread"] = saved
	return response, nil
}

// scoutChatChoiceAction is the POST /assistant/chat-threads/{id}/choice body:
// one quick-reply pill tap. Only ids cross the wire — the reply text, the tool
// arm, everything actionable resolves against the PERSISTED choices record, so
// a fabricated post cannot make Scout "say" arbitrary text or arm an unoffered
// tool.
type scoutChatChoiceAction struct {
	MessageID string `json:"messageId"`
	OptionID  string `json:"optionId"`
}

// resolveScoutChatChoice applies one pill tap. Claim first (the stored record
// wins, first tap wins), then the signal, then the side effect the option
// earns: a tool-armed pill commits the user's reply plus the DETERMINISTIC
// proposal card for that tool — the propose-confirm trust surface, so the
// card's Run button stays the only launch door — and a plain pill commits the
// reply and answers it as Tier 0. Nothing here ever launches.
func (app *kanbanBoardApp) resolveScoutChatChoice(ctx context.Context, user *userAccount, threadID string, action scoutChatChoiceAction) (map[string]any, error) {
	messageID := strings.TrimSpace(action.MessageID)
	optionID := strings.TrimSpace(action.OptionID)
	if messageID == "" || optionID == "" {
		return nil, fmt.Errorf("choice message id and option id are required")
	}

	option, choices, err := app.claimScoutChatChoice(threadID, user.Email, messageID, optionID)
	if err != nil {
		return nil, err
	}

	app.recordSignalEvent(user.Name, signalEventRouterChoiceSelected, signalValencePositive, "", "", map[string]string{
		"toolId":   option.ToolID,
		"label":    option.Label,
		"question": choices.Question,
	})

	reply := firstNonBlank(strings.TrimSpace(option.Reply), strings.TrimSpace(option.Label))
	now := time.Now().UTC()
	userMessage := scoutChatMessageRecord{
		ID:          fmt.Sprintf("scout-chat-message-%d", now.UnixNano()),
		Kind:        "message",
		Role:        "user",
		Text:        reply,
		CreatedAt:   now.Format(time.RFC3339Nano),
		AuthorName:  scoutChatAuthorName(user),
		AuthorEmail: normalizeAccountEmail(user.Email),
	}
	response := map[string]any{"ok": true, "message": userMessage}

	if option.ToolID != "" {
		// The pill's reply is usually the best objective (the router wrote it
		// for exactly this route); the originating ask backs it up, and stays
		// the Tier-0 escape query on the card.
		proposal := scoutRouterProposalForToolID(option.ToolID, reply, strings.TrimSpace(choices.Query))
		if proposal == nil {
			return nil, fmt.Errorf("that option's tool is no longer available")
		}
		proposalMessage := scoutChatMessageRecord{
			ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
			Kind:      scoutChatMessageKindProposal,
			Role:      "scout",
			Text:      proposal.Summary,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Proposal:  proposal,
		}
		saved, err := app.commitScoutChatThreadMessages(user.Email, threadID, userMessage, proposalMessage)
		if err != nil {
			return nil, err
		}
		response["answer"] = proposalMessage
		response["proposal"] = proposal
		response["thread"] = saved
		return response, nil
	}

	// Plain pill: the reply is a Tier-0 turn — answer it with the thread as
	// context, exactly like a typed message that routed to no card.
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return nil, err
	}
	result, err := app.resolveAssistantQueryContext(ctx, reply, scoutChatHistoryFromThread(thread))
	if err != nil {
		// The tap already resolved the card; keep the reply on the record so
		// the conversation survives, then surface the answer failure.
		if _, commitErr := app.commitScoutChatThreadMessages(user.Email, threadID, userMessage); commitErr != nil {
			log.Errorf("Failed to commit choice reply after answer failure: %v", commitErr)
		}
		return nil, err
	}
	answer := strings.TrimSpace(result.answer)
	if answer == "" {
		answer = "no answer yet"
	}
	assistantMessage := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      "message",
		Role:      "scout",
		Text:      answer,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	saved, err := app.commitScoutChatThreadMessages(user.Email, threadID, userMessage, assistantMessage)
	if err != nil {
		return nil, err
	}
	response["answer"] = assistantMessage
	response["thread"] = saved
	return response, nil
}

// claimScoutChatChoice atomically resolves one persisted choices card through
// the same per-thread lock + re-read + save path as message commits: it loads
// the card by message id, requires it still PENDING (first tap wins; a replay
// or double-tap rejects), requires the option to be one the card actually
// offered, stamps answered + the selection, persists, and returns copies of
// the stored option and card. The caller acts on those records, never on
// request-body fields.
func (app *kanbanBoardApp) claimScoutChatChoice(threadID string, viewerEmail string, messageID string, optionID string) (scoutChatChoiceOption, scoutChatChoices, error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		return scoutChatChoiceOption{}, scoutChatChoices{}, err
	}
	if thread.ArchivedAt != "" {
		return scoutChatChoiceOption{}, scoutChatChoices{}, fmt.Errorf("chat thread is archived")
	}
	for index := range thread.Messages {
		message := &thread.Messages[index]
		if message.ID != messageID || message.Choices == nil {
			continue
		}
		if message.Choices.Status != "" {
			return scoutChatChoiceOption{}, scoutChatChoices{}, fmt.Errorf("those options were already answered")
		}
		var selected *scoutChatChoiceOption
		for optionIndex := range message.Choices.Options {
			if message.Choices.Options[optionIndex].ID == optionID {
				selected = &message.Choices.Options[optionIndex]
				break
			}
		}
		if selected == nil {
			return scoutChatChoiceOption{}, scoutChatChoices{}, fmt.Errorf("choice option not found")
		}
		message.Choices.Status = "answered"
		message.Choices.SelectedID = selected.ID
		thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := app.saveScoutChatThread(thread); err != nil {
			return scoutChatChoiceOption{}, scoutChatChoices{}, err
		}
		claimedOption := *selected
		claimedChoices := *message.Choices
		deliverScoutChatThreadUpdate(thread, *message)
		return claimedOption, claimedChoices, nil
	}
	return scoutChatChoiceOption{}, scoutChatChoices{}, fmt.Errorf("choice message not found")
}

// claimScoutChatProposal atomically resolves one persisted proposal card
// through the same per-thread lock + re-read + save path as message commits:
// it loads the card by message id, requires it to still be PENDING (empty
// status — first verdict wins; a replay or double-post rejects), stamps the
// verdict, persists, and returns a copy of the stored proposal record. The
// caller acts on that record, never on request-body fields.
func (app *kanbanBoardApp) claimScoutChatProposal(threadID string, viewerEmail string, messageID string, status string) (scoutRouterProposal, error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		return scoutRouterProposal{}, err
	}
	if thread.ArchivedAt != "" {
		return scoutRouterProposal{}, fmt.Errorf("chat thread is archived")
	}
	for index := range thread.Messages {
		message := &thread.Messages[index]
		if message.ID != messageID || message.Proposal == nil {
			continue
		}
		if message.Proposal.Status != "" {
			return scoutRouterProposal{}, fmt.Errorf("proposal was already %s", message.Proposal.Status)
		}
		message.Proposal.Status = status
		thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := app.saveScoutChatThread(thread); err != nil {
			return scoutRouterProposal{}, err
		}
		claimed := *message.Proposal
		deliverScoutChatThreadUpdate(thread, *message)
		return claimed, nil
	}
	return scoutRouterProposal{}, fmt.Errorf("proposal message not found")
}

// scoutChatAuthorName resolves the display name stamped on channel messages.
// canonicalRoomActorName returns "" for any display name outside the seeded
// roster (e.g. "AJ (Founder)"), which used to persist blank authors that every
// reader's client rendered as their own message. Fall back to the raw display
// name, then the roster name for the account email.
func scoutChatAuthorName(user *userAccount) string {
	if user == nil {
		return ""
	}
	if name := canonicalRoomActorName(user.Name); name != "" {
		return name
	}
	return firstNonEmptyString(strings.TrimSpace(user.Name), participantNameForEmail(user.Email))
}

// updateScoutChatThreadRefs rewrites the thread refs embedded in persisted
// chat messages when an agent thread changes status. Office/chat sessions do
// not consume room websocket events, so without this rewrite the requester's
// card would stay at the last streamed progress forever; the commit delivers
// the flip live over the office socket (public broadcast for channels,
// owner-targeted send for private threads), with the 12s chat poll as the
// socket-down fallback.
func (app *kanbanBoardApp) updateScoutChatThreadRefs(agentThreadID string, status string, artifactID string) {
	if app == nil || app.memory == nil {
		return
	}
	agentThreadID = strings.TrimSpace(agentThreadID)
	status = strings.TrimSpace(status)
	if agentThreadID == "" || status == "" {
		return
	}
	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || !scoutChatThreadHasAgentRef(thread, agentThreadID) {
			continue
		}
		if err := app.commitScoutChatThreadRefStatus(thread.ID, thread.OwnerEmail, agentThreadID, status, artifactID); err != nil {
			log.Errorf("Failed to update chat thread %s ref for agent thread %s: %v", thread.ID, agentThreadID, err)
		}
	}
}

func scoutChatThreadHasAgentRef(thread scoutChatThreadRecord, agentThreadID string) bool {
	for _, message := range thread.Messages {
		if message.Thread != nil && message.Thread.ID == agentThreadID {
			return true
		}
	}
	return false
}

// scoutChatThreadHasArtifactRef mirrors scoutChatThreadHasAgentRef keyed on
// the artifact id: a follow-up may only target a report whose card lives in
// this chat thread.
func scoutChatThreadHasArtifactRef(thread scoutChatThreadRecord, artifactID string) bool {
	for _, message := range thread.Messages {
		if message.Thread != nil && message.Thread.ArtifactID == artifactID {
			return true
		}
	}
	return false
}

// scoutChatRepliesSince collects the human messages posted after the given
// RFC3339 timestamp (the artifact's last completedAt) — these become worker
// context so answers that landed as unattached channel messages count. Last
// agentThreadFollowUpMaxReplies entries only.
func scoutChatRepliesSince(thread scoutChatThreadRecord, since string) []scoutChatMessageRecord {
	cutoff, hasCutoff := time.Time{}, false
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(since)); err == nil {
		cutoff, hasCutoff = parsed, true
	}
	replies := make([]scoutChatMessageRecord, 0, len(thread.Messages))
	for _, message := range thread.Messages {
		if message.Kind != "message" || message.Role != "user" {
			continue
		}
		if hasCutoff {
			created, err := time.Parse(time.RFC3339Nano, message.CreatedAt)
			if err != nil || !created.After(cutoff) {
				continue
			}
		}
		replies = append(replies, message)
	}
	if len(replies) > agentThreadFollowUpMaxReplies {
		replies = replies[len(replies)-agentThreadFollowUpMaxReplies:]
	}
	return replies
}

// commitScoutChatThreadRefStatus applies one agent-thread status onto every
// matching message ref in one chat thread through the same lock + re-read +
// save path as commitScoutChatThreadMessages.
func (app *kanbanBoardApp) commitScoutChatThreadRefStatus(threadID string, ownerEmail string, agentThreadID string, status string, artifactID string) error {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil {
		return err
	}
	changed := make([]scoutChatMessageRecord, 0, 1)
	for index := range thread.Messages {
		ref := thread.Messages[index].Thread
		if ref == nil || ref.ID != agentThreadID {
			continue
		}
		if ref.Status == status && (artifactID == "" || ref.ArtifactID == artifactID) {
			continue
		}
		ref.Status = status
		if artifactID != "" {
			ref.ArtifactID = artifactID
		}
		changed = append(changed, thread.Messages[index])
	}
	if len(changed) == 0 {
		return nil
	}
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(thread); err != nil {
		return err
	}
	for _, message := range changed {
		deliverScoutChatThreadUpdate(thread, message)
	}
	return nil
}

// scoutChatArtifactRefMessage builds the Kind "thread" card a dropped
// deliverable lands as (Wave 6 Gate A). An agent-thread report keeps its own
// mode/thread id/artifact id, so the follow-up's running/complete flips
// (updateScoutChatThreadRefs matches Thread.ID) land on the added card. A
// goal-engine deliverable maps to its GOAL: Mode "goal", Thread.ID the goal's
// run id, and — critically — ArtifactID the goal PARENT artifact, exactly the
// shape of the toolTemplate launch card, because the client mounts the live
// goalcard off ref.artifactId and a deliverable id there would pin a dead
// card that never shows the goal's progress. Dedupe keys on the ref's
// ArtifactID, so dropping two deliverables of one goal lands ONE goalcard.
func (app *kanbanBoardApp) scoutChatArtifactRefMessage(artifact meetingMemoryEntry) scoutChatMessageRecord {
	refID := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["threadId"]), artifact.ID)
	refMode := firstNonEmptyString(artifact.Metadata["mode"], artifact.Kind)
	refQuery := firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"])
	refArtifactID := artifact.ID
	refStatus := firstNonEmptyString(agentThreadStatusValue(artifact), "complete")
	droppedTitle := firstNonEmptyString(refQuery, "deliverable")
	if artifact.Metadata["source"] != "scout_thread" {
		if goalID := artifactGoalParentID(artifact); goalID != "" {
			refMode = "goal"
			refID = goalID
			refArtifactID = goalID
			if parent, ok := app.osArtifactByID(goalID); ok {
				refID = firstNonEmptyString(strings.TrimSpace(parent.Metadata["threadId"]), goalID)
				refQuery = firstNonEmptyString(strings.TrimSpace(parent.Metadata["title"]), refQuery)
				refStatus = firstNonEmptyString(agentThreadStatusValue(parent), refStatus)
			}
		}
	}
	return scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:      "thread",
		Role:      "scout",
		Text:      droppedTitle + " — dropped into this thread; feedback below re-runs it",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread: &scoutChatThreadRef{
			ID:         refID,
			Mode:       refMode,
			Query:      refQuery,
			Status:     refStatus,
			ArtifactID: refArtifactID,
		},
	}
}

// commitScoutChatThreadArtifactRef appends a dropped deliverable's card unless
// a ref for that artifact already exists — the same lock + re-read + save
// discipline as commitScoutChatThreadMessages, with the dedupe check INSIDE
// the lock so two concurrent drops of one deliverable can never double its
// card (renderActiveScoutThread keys cards by artifact id). Returns the saved
// thread either way.
func (app *kanbanBoardApp) commitScoutChatThreadArtifactRef(viewerEmail string, threadID string, message scoutChatMessageRecord) (scoutChatThreadRecord, error) {
	if message.Thread == nil || strings.TrimSpace(message.Thread.ArtifactID) == "" {
		return scoutChatThreadRecord{}, fmt.Errorf("artifact ref message requires a thread ref")
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	if scoutChatThreadHasArtifactRef(thread, message.Thread.ArtifactID) {
		return thread, nil
	}
	thread.Messages = append(thread.Messages, message)
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, message)
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, err
	}
	deliverScoutChatThreadUpdate(thread, message)
	return thread, nil
}

// commitScoutChatThreadMessages is the single write path for chat messages.
// Persistence is whole-thread last-write-wins, so concurrent channel posters
// must serialize here: take the per-thread lock, re-read the thread from the
// store (another writer may have appended while this caller's model call ran),
// append, and save. Model/agent calls stay outside the lock.
func (app *kanbanBoardApp) commitScoutChatThreadMessages(viewerEmail string, threadID string, messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
	if len(messages) == 0 {
		return scoutChatThreadRecord{}, fmt.Errorf("chat thread commit requires a message")
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	thread.Messages = append(thread.Messages, messages...)

	userMessage := scoutChatMessageRecord{}
	assistantMessage := scoutChatMessageRecord{}
	for _, message := range messages {
		if message.Role == "user" && userMessage.ID == "" {
			userMessage = message
		}
		if message.Role != "user" {
			assistantMessage = message
		}
	}
	updateScoutChatThreadSummary(&thread, userMessage, assistantMessage)
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, err
	}
	for _, message := range messages {
		deliverScoutChatThreadUpdate(thread, message)
	}
	return thread, nil
}

// deleteScoutChatThreadMessage removes one message its author posted in the
// wrong place — same per-thread lock + re-read + save discipline as message
// commits. Authorship is the whole authz story: only the message's own author
// may remove it (session email vs the server-stamped authorEmail); Scout
// replies and other people's messages stay. Messages persisted before the
// authorEmail stamp existed carry none — in a private thread the owner-only
// visibility already proves authorship, so those stay deletable there.
func (app *kanbanBoardApp) deleteScoutChatThreadMessage(viewerEmail string, threadID string, messageID string) (scoutChatThreadRecord, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return scoutChatThreadRecord{}, fmt.Errorf("message id is required")
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	index := -1
	for candidate := range thread.Messages {
		if thread.Messages[candidate].ID == messageID {
			index = candidate
			break
		}
	}
	if index < 0 {
		return scoutChatThreadRecord{}, fmt.Errorf("chat message not found")
	}
	message := thread.Messages[index]
	own := message.AuthorEmail != "" && normalizeAccountEmail(message.AuthorEmail) == normalizeAccountEmail(viewerEmail)
	if message.AuthorEmail == "" {
		own = scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic
	}
	if message.Role != "user" || !own {
		return scoutChatThreadRecord{}, fmt.Errorf("you can only delete your own messages")
	}
	thread.Messages = append(thread.Messages[:index], thread.Messages[index+1:]...)
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	thread.Preview = scoutChatThreadPreview(thread)
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, err
	}
	deliverScoutChatThreadDeletion(thread, messageID)
	return thread, nil
}

// normalizeChannelName strips a leading '#' and surrounding whitespace from a
// spoken/typed channel reference.
func normalizeChannelName(name string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "#"))
}

// publicChannelByName resolves an open public channel by title
// (case-insensitive, leading '#' tolerated). A miss returns an error listing
// the available channel names so the voice model can self-correct aloud.
func (app *kanbanBoardApp) publicChannelByName(name string) (scoutChatThreadRecord, error) {
	wanted := normalizeChannelName(name)
	if wanted == "" {
		return scoutChatThreadRecord{}, fmt.Errorf("channel name is required")
	}
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, fmt.Errorf("chat thread memory is unavailable")
	}

	titles := make([]string, 0, 4)
	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" {
			continue
		}
		if strings.EqualFold(wanted, strings.TrimSpace(thread.Title)) {
			return thread, nil
		}
		titles = append(titles, thread.Title)
	}
	joined := "none exist yet — use create_channel"
	if len(titles) > 0 {
		joined = strings.Join(titles, ", ")
	}
	return scoutChatThreadRecord{}, fmt.Errorf("no channel named %q; channels: %s", wanted, joined)
}

// postToChannel executes the post_to_channel voice tool: relay the user's
// words into a public team channel through the normal per-thread commit path.
// requesterEmail identifies the private dashboard voice user; the shared room
// voice has no single requester, so the post attributes to Scout there.
// Deliberate: this path never triggers Scout's answer loop, even when the
// text contains "@scout" — the mention gate lives in
// appendScoutChatThreadMessage, which this bypasses.
func (app *kanbanBoardApp) postToChannel(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	text := strings.TrimSpace(asString(args["text"]))
	if text == "" {
		return nil, false, fmt.Errorf("text is required")
	}
	thread, err := app.publicChannelByName(asString(args["channel"]))
	if err != nil {
		return nil, false, err
	}

	now := time.Now().UTC()
	message := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-%d", now.UnixNano()),
		Kind:      "message",
		CreatedAt: now.Format(time.RFC3339Nano),
		Text:      text,
	}
	requesterEmail = normalizeAccountEmail(requesterEmail)
	if requesterEmail != "" {
		message.Role = "user"
		message.AuthorName = participantNameForEmail(requesterEmail)
		message.AuthorEmail = requesterEmail
		message.Via = "scout_voice"
	} else {
		message.Role = "scout"
		message.AuthorName = scoutParticipantName
	}
	author := firstNonEmptyString(message.AuthorName, scoutParticipantName)

	if _, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, message); err != nil {
		return nil, false, err
	}

	// Bell nudge for everyone, deep-linked to the channel.
	if _, err := app.createNotification("", notificationKindChat, author+" posted in #"+thread.Title+": "+trimForStorage(text, 140), "chat", "", thread.ID, false); err != nil {
		log.Errorf("Failed to create channel post notification: %v", err)
	}
	// Optional single-person flag.
	if mention := strings.TrimSpace(asString(args["mention"])); mention != "" {
		if mentionEmail := participantEmail(canonicalParticipantName(mention)); mentionEmail != "" {
			if _, err := app.createNotification(mentionEmail, notificationKindChat, author+" flagged you in #"+thread.Title+": "+trimForStorage(text, 140), "chat", "", thread.ID, false); err != nil {
				log.Errorf("Failed to create channel mention notification: %v", err)
			}
		}
	}

	// Unified push channel: a title-only signal that #channel got a post — the
	// message body never crosses this boundary; a consumer that wants it reads
	// the thread by ref under the normal auth guard.
	broadcastOSEvent(osEvent{
		Kind:          osEventChannelPost,
		Ref:           thread.ID,
		Title:         "#" + thread.Title,
		OriginSurface: "chat",
		Actor:         author,
	})

	// No open_tool actions: auto-navigating everyone mid-meeting is hostile.
	return map[string]any{
		"ok":        true,
		"channel":   thread.Title,
		"threadId":  thread.ID,
		"messageId": message.ID,
	}, false, nil
}

// createChannelByVoice executes the create_channel voice tool. Channels are
// public scout-chat threads and need an owner identity, so only the private
// dashboard voice (a single signed-in user) may create one — the shared room
// peer has no owner and is rejected.
func (app *kanbanBoardApp) createChannelByVoice(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	name := normalizeChannelName(asString(args["name"]))
	if name == "" {
		return nil, false, fmt.Errorf("channel name is required")
	}
	requesterEmail = normalizeAccountEmail(requesterEmail)
	if requesterEmail == "" {
		return nil, false, fmt.Errorf("create channels from your private Scout or the chat surface")
	}

	thread, err := app.createScoutChatThread(requesterEmail, participantNameForEmail(requesterEmail), name, scoutChatVisibilityPublic)
	if err != nil {
		return nil, false, err
	}

	// Office fan-out so open chat rails learn the new channel; the payload
	// carries no message (handleChatThreadEvent tolerates that and refreshes
	// the list for unknown thread ids).
	broadcastOfficeKanbanEvent("chat_thread", map[string]any{
		"id":         thread.ID,
		"title":      thread.Title,
		"preview":    thread.Preview,
		"visibility": scoutChatThreadVisibility(thread),
		"updatedAt":  thread.UpdatedAt,
	})
	creator := firstNonEmptyString(participantNameForEmail(requesterEmail), "Scout")
	if _, err := app.createNotification("", notificationKindChat, creator+" created channel #"+thread.Title, "chat", "", thread.ID, false); err != nil {
		log.Errorf("Failed to create channel-created notification: %v", err)
	}

	return map[string]any{
		"ok":       true,
		"channel":  thread.Title,
		"threadId": thread.ID,
	}, false, nil
}

// startChatAsUser backs the start_chat_as_user private-voice tool: Scout starts
// (or addresses) a channel or private thread and posts a message AS the
// signed-in user, with a mandatory disclosure stamp. The disclosure
// (postedOnBehalfOf) is written server-side UNCONDITIONALLY from the
// authenticated requester — never from a model argument — so Scout can never
// silently impersonate. A missing requester is rejected: there is no "as user"
// without a real user.
func (app *kanbanBoardApp) startChatAsUser(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	text := strings.TrimSpace(asString(args["text"]))
	if text == "" {
		return nil, false, fmt.Errorf("text is required")
	}
	requesterEmail = normalizeAccountEmail(requesterEmail)
	if requesterEmail == "" {
		return nil, false, fmt.Errorf("start chats from your private Scout — an owner identity is required")
	}
	authorName := participantNameForEmail(requesterEmail)

	audience := strings.ToLower(strings.TrimSpace(asString(args["audience"])))
	if audience == "" {
		audience = "channel"
	}

	var thread scoutChatThreadRecord
	var err error
	switch audience {
	case "thread", "private_thread", "dm":
		// "dm" is an alias, not a human-to-human direct message: private threads
		// are owner+Scout only (see the visibility doctrine above), so every
		// private audience resolves to the REQUESTER'S OWN Scout thread. There is
		// no path here to a cross-user private channel.
		thread, err = app.resolveOrCreatePrivateThread(requesterEmail, authorName, asString(args["name"]))
	default:
		audience = "channel"
		thread, err = app.resolveOrCreatePublicChannel(requesterEmail, authorName, asString(args["name"]))
	}
	if err != nil {
		return nil, false, err
	}

	now := time.Now().UTC()
	message := scoutChatMessageRecord{
		ID:          fmt.Sprintf("scout-chat-message-%d", now.UnixNano()),
		Kind:        "message",
		Role:        "user",
		CreatedAt:   now.Format(time.RFC3339Nano),
		Text:        text,
		AuthorName:  authorName,
		AuthorEmail: requesterEmail,
		Via:         "scout_voice",
		// Disclosure is stamped from the authenticated requester, never args —
		// this is the one place a model action speaks as a human, so the audit
		// stamp is the safety control (risk-10).
		PostedOnBehalfOf: requesterEmail,
	}

	// commitScoutChatThreadMessages fans the message out to the visibility-scoped
	// tabs itself, so no extra deliver call here.
	if _, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, message); err != nil {
		return nil, false, err
	}

	author := firstNonEmptyString(authorName, scoutParticipantName)
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		if _, err := app.createNotification("", notificationKindChat, author+" posted in #"+thread.Title+": "+trimForStorage(text, 140), "chat", "", thread.ID, false); err != nil {
			log.Errorf("Failed to create start-chat notification: %v", err)
		}
		broadcastOSEvent(osEvent{
			Kind:          osEventChannelPost,
			Ref:           thread.ID,
			Title:         "#" + thread.Title,
			OriginSurface: "chat",
			Actor:         author,
		})
	}

	return map[string]any{
		"ok":               true,
		"audience":         audience,
		"channel":          thread.Title,
		"threadId":         thread.ID,
		"messageId":        message.ID,
		"postedOnBehalfOf": requesterEmail,
	}, false, nil
}

// resolveOrCreatePublicChannel addresses an existing public channel by name or
// creates it, so start_chat_as_user can "start a chat" idempotently.
func (app *kanbanBoardApp) resolveOrCreatePublicChannel(requesterEmail string, authorName string, name string) (scoutChatThreadRecord, error) {
	if existing, err := app.publicChannelByName(name); err == nil {
		return existing, nil
	}
	channelName := normalizeChannelName(name)
	if channelName == "" {
		return scoutChatThreadRecord{}, fmt.Errorf("channel name is required")
	}
	thread, err := app.createScoutChatThread(requesterEmail, authorName, channelName, scoutChatVisibilityPublic)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	broadcastOfficeKanbanEvent("chat_thread", scoutChatThreadEventPayload(thread))
	return thread, nil
}

// resolveOrCreatePrivateThread addresses the requester's existing private thread
// by title (case-insensitive, non-archived) or creates a new one.
func (app *kanbanBoardApp) resolveOrCreatePrivateThread(requesterEmail string, authorName string, name string) (scoutChatThreadRecord, error) {
	title := trimForStorage(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "#")), 72)
	if title != "" {
		for _, existing := range app.scoutChatThreadsSnapshot(requesterEmail, false, 100) {
			if scoutChatThreadVisibility(existing) == scoutChatVisibilityPublic {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(existing.Title), title) {
				return existing, nil
			}
		}
	}
	thread, err := app.createScoutChatThread(requesterEmail, authorName, title, scoutChatVisibilityPrivate)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	sendKanbanEventToUser(thread.OwnerEmail, "chat_thread", scoutChatThreadEventPayload(thread))
	return thread, nil
}

// readThreadAloud backs the read_thread_aloud private-voice tool. The Realtime
// session already outputs audio, so "read aloud" is recall-shaped: resolve the
// target's recent text and return it in the tool result for the model to speak
// in its next turn. No new audio plumbing.
func (app *kanbanBoardApp) readThreadAloud(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	target := strings.ToLower(strings.TrimSpace(asString(args["target"])))
	ref := strings.TrimSpace(asString(args["ref"]))
	limit := asInt(args["limit"])
	if limit <= 0 || limit > 12 {
		limit = 3
	}
	requesterEmail = normalizeAccountEmail(requesterEmail)

	switch target {
	case "channel":
		thread, err := app.publicChannelByName(ref)
		if err != nil {
			return nil, false, err
		}
		return readThreadAloudResult("#"+thread.Title, scoutChatRecentMessageLines(thread, limit)), false, nil
	case "private_thread", "thread":
		if requesterEmail == "" {
			return nil, false, fmt.Errorf("sign in to read a private thread")
		}
		thread, _, err := app.scoutChatThreadByID(requesterEmail, ref)
		if err != nil {
			return nil, false, err
		}
		title := firstNonEmptyString(thread.Title, "thread")
		return readThreadAloudResult(title, scoutChatRecentMessageLines(thread, limit)), false, nil
	case "artifact":
		entry, ok := app.osArtifactByID(ref)
		if !ok {
			return nil, false, fmt.Errorf("no artifact %q to read", ref)
		}
		artifactTitle := firstNonEmptyString(entry.Metadata["title"], entry.Metadata["threadQuery"], "artifact")
		return readThreadAloudResult(artifactTitle, []string{trimForStorage(entry.Text, 1600)}), false, nil
	case "notifications":
		if requesterEmail == "" {
			return nil, false, fmt.Errorf("sign in to read notifications")
		}
		lines := []string{}
		for _, record := range app.notificationsForUser(requesterEmail, limit) {
			if text := strings.TrimSpace(asString(record["text"])); text != "" {
				lines = append(lines, text)
			}
		}
		return readThreadAloudResult("notifications", lines), false, nil
	default:
		return nil, false, fmt.Errorf("target must be channel, private_thread, artifact, or notifications")
	}
}

func readThreadAloudResult(title string, lines []string) map[string]any {
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	return map[string]any{
		"ok":    true,
		"title": title,
		"text":  strings.Join(clean, "\n"),
		"count": len(clean),
	}
}

// scoutChatRecentMessageLines returns up to limit most-recent message lines
// (newest last) as "author: text" for the model to read aloud.
func scoutChatRecentMessageLines(thread scoutChatThreadRecord, limit int) []string {
	messages := thread.Messages
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		author := firstNonEmptyString(message.AuthorName, map[string]string{"scout": scoutParticipantName}[message.Role], "someone")
		lines = append(lines, author+": "+text)
	}
	return lines
}

// scoutChatThreadEventPayload is the message-less chat_thread event body used
// for metadata-only changes (rename, channel creation) — handleChatThreadEvent
// tolerates a missing message and just updates the row.
func scoutChatThreadEventPayload(thread scoutChatThreadRecord) map[string]any {
	return map[string]any{
		"id":         thread.ID,
		"title":      thread.Title,
		"preview":    thread.Preview,
		"visibility": scoutChatThreadVisibility(thread),
		"updatedAt":  thread.UpdatedAt,
	}
}

// scoutChatThreadUpdatePayload is the chat_thread event body shared by the
// public broadcast and the private owner-targeted delivery.
func scoutChatThreadUpdatePayload(thread scoutChatThreadRecord, message scoutChatMessageRecord) map[string]any {
	payload := scoutChatThreadEventPayload(thread)
	payload["message"] = message
	return payload
}

// broadcastScoutChatThreadUpdate fans a public-channel append out over the
// office channel (every signed-in tab holds an office socket, in-room or
// not) so open chat tabs upsert live; tabs whose office socket is down catch
// up via the 12s fallback poll.
func broadcastScoutChatThreadUpdate(thread scoutChatThreadRecord, message scoutChatMessageRecord) {
	broadcastOfficeKanbanEvent("chat_thread", scoutChatThreadUpdatePayload(thread, message))
}

// deliverScoutChatThreadUpdate routes one committed chat message (or thread
// ref status flip) to the tabs allowed to see it live: public channels fan
// out to every signed-in office socket, private threads go only to the
// owner's authenticated connections via the targeted send. Without the
// targeted path a private thread's agent-run status flip has no live route
// at all — chat_thread broadcasts are public-only and the 12s chat poll
// skips its fetch while the office socket is up.
func deliverScoutChatThreadUpdate(thread scoutChatThreadRecord, message scoutChatMessageRecord) {
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		broadcastScoutChatThreadUpdate(thread, message)
		return
	}
	sendKanbanEventToUser(thread.OwnerEmail, "chat_thread", scoutChatThreadUpdatePayload(thread, message))
}

// deliverScoutChatThreadDeletion routes a message removal the same way
// committed messages travel — broadcast for public channels, owner-targeted
// for private threads — with deletedMessageId (instead of message) telling
// clients to drop the bubble live.
func deliverScoutChatThreadDeletion(thread scoutChatThreadRecord, messageID string) {
	payload := scoutChatThreadEventPayload(thread)
	payload["deletedMessageId"] = messageID
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		broadcastOfficeKanbanEvent("chat_thread", payload)
		return
	}
	sendKanbanEventToUser(thread.OwnerEmail, "chat_thread", payload)
}

// scoutChatThreadLock returns the per-thread mutex serializing chat thread
// read-modify-write commits (mirrors ambientAgentRunLock).
func (app *kanbanBoardApp) scoutChatThreadLock(threadID string) *sync.Mutex {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.chatThreadLocks == nil {
		app.chatThreadLocks = map[string]*sync.Mutex{}
	}
	lock, ok := app.chatThreadLocks[threadID]
	if !ok {
		lock = &sync.Mutex{}
		app.chatThreadLocks[threadID] = lock
	}
	return lock
}

// renameScoutChatThread applies a user-chosen title through the same
// per-thread lock + re-read + save discipline as message commits, then fans
// the change out like a visibility-scoped chat_thread event (broadcast for
// public channels, owner-targeted for private threads). Public threads are
// renamable by any signed-in user (D7 — acceptable on the small roster);
// private threads are only reachable by their owner via scoutChatThreadByID.
func (app *kanbanBoardApp) renameScoutChatThread(viewerEmail string, threadID string, title string) (scoutChatThreadRecord, error) {
	title = trimForStorage(title, 72)
	if title == "" {
		return scoutChatThreadRecord{}, fmt.Errorf("thread title is required")
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	if thread.ArchivedAt != "" {
		return scoutChatThreadRecord{}, fmt.Errorf("chat thread is archived")
	}
	if thread.Title == title {
		return thread, nil
	}
	thread.Title = title
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, err
	}
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		broadcastOfficeKanbanEvent("chat_thread", scoutChatThreadEventPayload(thread))
	} else {
		sendKanbanEventToUser(thread.OwnerEmail, "chat_thread", scoutChatThreadEventPayload(thread))
	}
	return thread, nil
}

func (app *kanbanBoardApp) setScoutChatThreadArchived(ownerEmail string, threadID string, archived bool) (scoutChatThreadRecord, error) {
	// Same per-thread mutex as rename and message commits — an unlocked
	// read-modify-write here could interleave with a concurrent rename and
	// silently revert whichever change saved first.
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	thread, _, err := app.scoutChatThreadByID(ownerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, err
	}
	// Any signed-in user can read a public channel, but only its creator may
	// archive (or restore) it.
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic && normalizeAccountEmail(thread.OwnerEmail) != normalizeAccountEmail(ownerEmail) {
		return scoutChatThreadRecord{}, fmt.Errorf("only the channel creator can archive this channel")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if archived {
		thread.ArchivedAt = now
	} else {
		thread.ArchivedAt = ""
	}
	thread.UpdatedAt = now
	if archived {
		thread.Preview = "archived"
	} else if thread.Preview == "" || thread.Preview == "archived" {
		thread.Preview = scoutChatThreadPreview(thread)
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, err
	}
	return thread, nil
}

func (app *kanbanBoardApp) saveScoutChatThread(thread scoutChatThreadRecord) error {
	entryText, err := encodeScoutChatThread(thread)
	if err != nil {
		return err
	}
	_, _, err = app.memory.updateScoutChatThread(thread.ID, entryText, scoutChatThreadMetadata(thread))
	return err
}

func (app *kanbanBoardApp) scoutChatThreadsSnapshot(ownerEmail string, includeArchived bool, limit int) []scoutChatThreadRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	ownerEmail = normalizeAccountEmail(ownerEmail)
	if ownerEmail == "" {
		return nil
	}

	entries := app.memory.snapshot(0)
	threads := make([]scoutChatThreadRecord, 0, len(entries))
	for _, entry := range entries {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok {
			continue
		}
		// Owner sees their own threads; public channels are readable by every
		// signed-in user (ownerEmail is already verified non-empty above).
		if normalizeAccountEmail(thread.OwnerEmail) != ownerEmail && scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
			continue
		}
		if !includeArchived && thread.ArchivedAt != "" {
			continue
		}
		threads = append(threads, thread)
	}
	sort.SliceStable(threads, func(i, j int) bool {
		return scoutChatThreadTime(threads[i]).After(scoutChatThreadTime(threads[j]))
	})
	if limit > 0 && len(threads) > limit {
		threads = threads[:limit]
	}
	return threads
}

func (app *kanbanBoardApp) scoutChatThreadByID(ownerEmail string, threadID string) (scoutChatThreadRecord, meetingMemoryEntry, error) {
	ownerEmail = normalizeAccountEmail(ownerEmail)
	threadID = strings.TrimSpace(threadID)
	if ownerEmail == "" || threadID == "" {
		return scoutChatThreadRecord{}, meetingMemoryEntry{}, fmt.Errorf("chat thread not found")
	}
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind != meetingMemoryKindScoutChat || entry.ID != threadID {
			continue
		}
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok {
			break
		}
		if normalizeAccountEmail(thread.OwnerEmail) != ownerEmail && scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
			break
		}
		return thread, entry, nil
	}
	return scoutChatThreadRecord{}, meetingMemoryEntry{}, fmt.Errorf("chat thread not found")
}

func encodeScoutChatThread(thread scoutChatThreadRecord) (string, error) {
	raw, err := json.Marshal(thread)
	if err != nil {
		return "", fmt.Errorf("encode chat thread: %w", err)
	}
	return string(raw), nil
}

func decodeScoutChatThreadEntry(entry meetingMemoryEntry) (scoutChatThreadRecord, bool) {
	if entry.Kind != meetingMemoryKindScoutChat {
		return scoutChatThreadRecord{}, false
	}
	var thread scoutChatThreadRecord
	if err := json.Unmarshal([]byte(entry.Text), &thread); err != nil {
		return scoutChatThreadRecord{}, false
	}
	if strings.TrimSpace(thread.ID) == "" {
		thread.ID = entry.ID
	}
	if strings.TrimSpace(thread.OwnerEmail) == "" {
		thread.OwnerEmail = entry.Metadata["ownerEmail"]
	}
	if strings.TrimSpace(thread.Title) == "" {
		thread.Title = firstNonEmptyString(entry.Metadata["title"], "Scout")
	}
	if strings.TrimSpace(thread.CreatedAt) == "" && !entry.CreatedAt.IsZero() {
		thread.CreatedAt = entry.CreatedAt.Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(thread.UpdatedAt) == "" {
		thread.UpdatedAt = firstNonEmptyString(entry.Metadata["updatedAt"], thread.CreatedAt)
	}
	// Pre-channel entries carry no visibility; they stay private.
	thread.Visibility = normalizeScoutChatVisibility(firstNonEmptyString(thread.Visibility, entry.Metadata["visibility"]))
	return thread, true
}

func scoutChatThreadMetadata(thread scoutChatThreadRecord) map[string]string {
	metadata := map[string]string{
		"ownerEmail": normalizeAccountEmail(thread.OwnerEmail),
		"title":      strings.TrimSpace(thread.Title),
		"preview":    strings.TrimSpace(thread.Preview),
		"visibility": scoutChatThreadVisibility(thread),
		"createdAt":  strings.TrimSpace(thread.CreatedAt),
		"updatedAt":  strings.TrimSpace(thread.UpdatedAt),
		"source":     "scout_chat",
		"status":     "active",
	}
	if strings.TrimSpace(thread.CreatedBy) != "" {
		metadata["createdBy"] = strings.TrimSpace(thread.CreatedBy)
	}
	if strings.TrimSpace(thread.ArchivedAt) != "" {
		metadata["archivedAt"] = strings.TrimSpace(thread.ArchivedAt)
		metadata["status"] = "archived"
	}
	return metadata
}

func sanitizeScoutChatFiles(files []scoutChatFileAttachment) []scoutChatFileAttachment {
	if len(files) > scoutChatMaxFilesPerMessage {
		files = files[:scoutChatMaxFilesPerMessage]
	}
	cleaned := make([]scoutChatFileAttachment, 0, len(files))
	for _, file := range files {
		name := trimForStorage(file.Name, 180)
		if name == "" {
			name = "file"
		}
		kind := trimForStorage(file.Kind, 120)
		size := file.Size
		if size < 0 {
			size = 0
		}
		text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(file.Text, "\r\n", "\n"), "\r", "\n"))
		if len(text) > scoutChatMaxFileTextBytes {
			text = text[:scoutChatMaxFileTextBytes]
			for !utf8.ValidString(text) && len(text) > 0 {
				text = text[:len(text)-1]
			}
			text = strings.TrimSpace(text) + "\n[truncated]"
		}
		// A blob ref (card 085) must name a stored blob with a model-safe
		// mime; anything else drops the ref and keeps the name/size chip. A
		// valid ref takes the store's pinned mime over any client claim, and
		// strips client Text — a ref'd binary's Text is the server-derived
		// transcription only, never attacker-supplied "contents".
		ref := strings.TrimSpace(file.Ref)
		mime := ""
		if ref != "" {
			meta, err := blobStatForRef(ref)
			if err == nil && attachmentModelSafeMimes[strings.ToLower(strings.TrimSpace(meta.Mime))] {
				mime = strings.ToLower(strings.TrimSpace(meta.Mime))
				text = ""
			} else {
				ref = ""
			}
		}
		cleaned = append(cleaned, scoutChatFileAttachment{
			Name: name,
			Kind: kind,
			Size: size,
			Text: text,
			Ref:  ref,
			Mime: mime,
		})
	}
	return cleaned
}

func scoutChatHistoryFromThread(thread scoutChatThreadRecord) []scoutChatTurn {
	if len(thread.Messages) == 0 {
		return nil
	}
	start := 0
	if len(thread.Messages) > scoutChatMaxHistoryTurns {
		start = len(thread.Messages) - scoutChatMaxHistoryTurns
	}
	history := make([]scoutChatTurn, 0, len(thread.Messages)-start)
	for _, message := range thread.Messages[start:] {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "assistant", "scout":
			role = "scout"
		case "user":
			role = "user"
		default:
			continue
		}
		text := scoutChatMessageModelText(message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		history = append(history, scoutChatTurn{role: role, text: text})
	}
	return history
}

func scoutChatMessageModelText(message scoutChatMessageRecord) string {
	text := strings.TrimSpace(message.Text)
	parts := make([]string, 0, len(message.Files)+1)
	if text != "" {
		parts = append(parts, text)
	}
	for _, file := range message.Files {
		label := strings.TrimSpace(file.Name)
		if label == "" {
			label = "file"
		}
		metaParts := []string{}
		if strings.TrimSpace(file.Kind) != "" {
			metaParts = append(metaParts, strings.TrimSpace(file.Kind))
		}
		if file.Size > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%d bytes", file.Size))
		}
		meta := strings.Join(metaParts, ", ")
		metaSuffix := ""
		if meta != "" {
			metaSuffix = " (" + meta + ")"
		}
		if strings.TrimSpace(file.Text) == "" {
			parts = append(parts, fmt.Sprintf("Attached file: %s%s.", label, metaSuffix))
			continue
		}
		parts = append(parts, fmt.Sprintf("Attached file: %s%s:\n%s", label, metaSuffix, strings.TrimSpace(file.Text)))
	}
	if len(parts) == 0 && len(message.Files) > 0 {
		return "Use the attached files as context."
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func updateScoutChatThreadSummary(thread *scoutChatThreadRecord, userMessage scoutChatMessageRecord, assistantMessage scoutChatMessageRecord) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	thread.UpdatedAt = now
	if strings.TrimSpace(thread.Title) == "" || thread.Title == "Scout" || thread.Title == "New Scout thread" {
		thread.Title = scoutChatThreadTitle(userMessage)
	}
	thread.Preview = firstNonEmptyString(strings.TrimSpace(assistantMessage.Text), scoutChatThreadPreview(*thread))
}

func scoutChatThreadTitle(message scoutChatMessageRecord) string {
	text := strings.TrimSpace(message.Text)
	if text == "" && len(message.Files) > 0 {
		text = "Files: " + message.Files[0].Name
	}
	if text == "" {
		return "Scout"
	}
	return trimForStorage(text, 72)
}

func scoutChatThreadPreview(thread scoutChatThreadRecord) string {
	for index := len(thread.Messages) - 1; index >= 0; index-- {
		if text := strings.TrimSpace(thread.Messages[index].Text); text != "" {
			return trimForStorage(text, 140)
		}
	}
	return "new chat thread"
}

func scoutChatThreadTime(thread scoutChatThreadRecord) time.Time {
	for _, value := range []string{thread.UpdatedAt, thread.CreatedAt} {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func trimForStorage(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "..."
}
