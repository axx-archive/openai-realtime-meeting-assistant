package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	scoutReplyStateQueued         = "queued"
	scoutReplyStateRunning        = "running"
	scoutReplyStateCompleted      = "completed"
	scoutReplyStateFailed         = "failed"
	scoutReplyStateCanceled       = "canceled"
	scoutReplyStateProjectPending = "project_pending"

	scoutHomeOpeningMaxRunes = 4000
	scoutReplyWorkerCount    = 2
	scoutReplyQueueSize      = 32
	scoutReplyLeaseDuration  = 90 * time.Second
	scoutReplyRecoveryEvery  = 2 * time.Second
)

const scoutReplySafeFailureText = "Scout couldn't answer yet. Your message is safe."

const scoutReplyCanceledAfterEditText = "Scout reply canceled because the opening message changed."

// cancelScoutOpeningReplyInThread transitions only unfinished work. Callers
// hold the thread mutex and persist the returned mutation with their own
// edit/archive operation, so the lifecycle cannot be left queued after the
// source has become ineligible.
func cancelScoutOpeningReplyInThread(thread *scoutChatThreadRecord, reason string, now time.Time) (scoutChatMessageRecord, bool) {
	if thread == nil || thread.OpeningOperation == nil {
		return scoutChatMessageRecord{}, false
	}
	index := scoutChatMessageIndex(*thread, thread.OpeningOperation.ReplyMessageID)
	if index < 0 || thread.Messages[index].Reply == nil {
		return scoutChatMessageRecord{}, false
	}
	message := thread.Messages[index]
	if message.Reply.State != scoutReplyStateQueued && message.Reply.State != scoutReplyStateRunning && message.Reply.State != scoutReplyStateProjectPending {
		return scoutChatMessageRecord{}, false
	}
	lifecycle := *message.Reply
	lifecycle.State = scoutReplyStateCanceled
	lifecycle.FinishedAt = now.UTC().Format(time.RFC3339Nano)
	lifecycle.LeaseID = ""
	lifecycle.LeaseExpiresAt = ""
	lifecycle.Retryable = false
	lifecycle.ErrorCode = ""
	message.Text = firstNonEmptyString(strings.TrimSpace(reason), scoutReplyCanceledAfterEditText)
	message.Reply = &lifecycle
	thread.Messages[index] = message
	return message, true
}

type scoutHomeOpeningMessage struct {
	Text                string `json:"text"`
	ProjectContextToken string `json:"projectContextToken,omitempty"`
}

var (
	errScoutOpeningConflict = errors.New("idempotency key was already used for a different opening message")
	errScoutOpeningKey      = errors.New("a valid Idempotency-Key is required")
	errScoutProjectTerminal = errors.New("Project access changed before Scout could start")
)

func scoutOpeningDigest(parts ...string) string {
	hash := sha256.New()
	for index, part := range parts {
		if index > 0 {
			hash.Write([]byte{0})
		}
		hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalizeScoutHomeOpeningText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("opening message text is required")
	}
	if len([]rune(text)) > scoutHomeOpeningMaxRunes {
		return "", fmt.Errorf("opening message exceeds %d characters", scoutHomeOpeningMaxRunes)
	}
	return text, nil
}

func normalizeScoutIdempotencyKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 256 {
		return "", errScoutOpeningKey
	}
	for _, char := range key {
		if char < 0x21 || char > 0x7e {
			return "", errScoutOpeningKey
		}
	}
	return key, nil
}

func handleScoutHomeOpening(w http.ResponseWriter, r *http.Request, app *kanbanBoardApp, user *userAccount, title string, visibility string, intake string, opening scoutHomeOpeningMessage) {
	if strings.TrimSpace(title) != "" || strings.TrimSpace(visibility) != "" || strings.TrimSpace(intake) != "" {
		writeAuthError(w, http.StatusBadRequest, "openingMessage cannot be combined with title, visibility, or intake")
		return
	}
	key, err := normalizeScoutIdempotencyKey(r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	text, err := normalizeScoutHomeOpeningText(opening.Text)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	var projectToken homeProjectContextToken
	if strings.TrimSpace(opening.ProjectContextToken) != "" {
		destination := homeProjectDestination{Route: "new-private"}
		acceptedPending := app.acceptedScoutHomeProjectRetry(user, key, text, opening.ProjectContextToken)
		if !acceptedPending {
			writeAuthError(w, http.StatusConflict, errManualProjectAttachmentRetired.Error())
			return
		}
		err = withCurrentHomeProjectAuthority(r, func(snapshot StrideE10TenantAuthoritySnapshot) error {
			var resolveErr error
			projectToken, resolveErr = resolveHomeProjectTokenForRetry(r.Context(), opening.ProjectContextToken, text, destination, snapshot, acceptedPending)
			return resolveErr
		})
		if err != nil {
			writeAuthError(w, http.StatusConflict, errHomeProjectStale.Error())
			return
		}
	}
	thread, created, err := app.ensureScoutHomeOpeningWithProject(r.Context(), user, key, text, opening.ProjectContextToken, projectToken)
	if err != nil {
		if errors.Is(err, errScoutProjectTerminal) && thread.ID != "" {
			writeAuthJSON(w, http.StatusOK, map[string]any{
				"ok": true, "created": created, "replayed": !created, "projectLinked": false,
				"warning": errScoutProjectTerminal.Error(), "thread": app.projectScoutChatThreadForViewer(user.Email, thread),
			})
			return
		}
		if errors.Is(err, errScoutOpeningConflict) {
			writeAuthError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, ErrProjectAuthorityConflict) {
			writeAuthError(w, http.StatusConflict, errHomeProjectStale.Error())
			return
		}
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	app.queueScoutOpeningReply(thread.ID)
	w.Header().Set("Location", "/assistant/chat-threads/"+thread.ID)
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeAuthJSON(w, status, map[string]any{
		"ok":       true,
		"created":  created,
		"replayed": !created,
		"thread":   app.projectScoutChatThreadForViewer(user.Email, thread),
	})
}

// acceptedScoutHomeProjectRetry proves the narrow exception used above from
// server-only durable state. It never trusts a client thread id and never
// projects the journal. A changed key, text, token, owner, operation body, or
// terminal server-owned result does not need a still-valid client token.
func (app *kanbanBoardApp) acceptedScoutHomeProjectRetry(user *userAccount, key, text, encodedProjectToken string) bool {
	if app == nil || app.memory == nil || user == nil || strings.TrimSpace(encodedProjectToken) == "" {
		return false
	}
	owner := normalizeAccountEmail(user.Email)
	key, keyErr := normalizeScoutIdempotencyKey(key)
	text, textErr := normalizeScoutHomeOpeningText(text)
	if owner == "" || keyErr != nil || textErr != nil {
		return false
	}
	operationDigest := scoutOpeningDigest(owner, key)
	threadID := "scout-home-" + operationDigest[:24]
	wantTokenDigest := homeProjectTokenDigest(encodedProjectToken)
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind != meetingMemoryKindScoutChat || entry.ID != threadID {
			continue
		}
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || thread.OpeningOperation == nil || normalizeAccountEmail(thread.OwnerEmail) != owner ||
			thread.OpeningOperation.KeyDigest != scoutOpeningDigest(key) ||
			thread.OpeningOperation.BodyDigest != scoutOpeningDigest(text, wantTokenDigest) {
			return false
		}
		for _, operation := range thread.ProjectLinkOperations {
			if operation.OperationID == thread.OpeningOperation.OperationID && oneOf(operation.State, "pending", "confirmed", "failed_terminal") && operation.TokenDigest == wantTokenDigest {
				return true
			}
		}
		return false
	}
	return false
}

// ensureScoutHomeOpening persists the private thread, first user message, and
// queued reply placeholder as one whole-thread memory record. That single
// append is the crash-safe boundary promised by the home composer.
func (app *kanbanBoardApp) ensureScoutHomeOpening(user *userAccount, key string, text string) (scoutChatThreadRecord, bool, error) {
	return app.ensureScoutHomeOpeningWithProject(context.Background(), user, key, text, "", homeProjectContextToken{})
}

func (app *kanbanBoardApp) ensureScoutHomeOpeningWithProject(ctx context.Context, user *userAccount, key string, text string, encodedProjectToken string, projectToken homeProjectContextToken) (scoutChatThreadRecord, bool, error) {
	if app == nil || app.memory == nil || user == nil {
		return scoutChatThreadRecord{}, false, fmt.Errorf("chat thread memory is unavailable")
	}
	owner := normalizeAccountEmail(user.Email)
	if owner == "" {
		return scoutChatThreadRecord{}, false, fmt.Errorf("thread owner is required")
	}
	key, err := normalizeScoutIdempotencyKey(key)
	if err != nil {
		return scoutChatThreadRecord{}, false, err
	}
	text, err = normalizeScoutHomeOpeningText(text)
	if err != nil {
		return scoutChatThreadRecord{}, false, err
	}
	operationDigest := scoutOpeningDigest(owner, key)
	threadID := "scout-home-" + operationDigest[:24]
	projectTokenDigest := ""
	if strings.TrimSpace(encodedProjectToken) != "" {
		projectTokenDigest = homeProjectTokenDigest(encodedProjectToken)
	}
	operation := &scoutChatOpeningOperation{
		OperationID:    "scout-opening-" + operationDigest[:24],
		KeyDigest:      scoutOpeningDigest(key),
		BodyDigest:     scoutOpeningDigest(text, projectTokenDigest),
		UserMessageID:  threadID + "-user",
		ReplyMessageID: threadID + "-scout",
	}

	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind != meetingMemoryKindScoutChat || entry.ID != threadID {
			continue
		}
		existing, ok := decodeScoutChatThreadEntry(entry)
		if !ok || !scoutOpeningOperationMatches(existing, owner, operation) {
			lock.Unlock()
			return scoutChatThreadRecord{}, false, errScoutOpeningConflict
		}
		lock.Unlock()
		if projectToken.Kind != "" {
			if scoutHomeProjectOperationState(existing, existing.OpeningOperation.OperationID) == "failed_terminal" {
				return existing, false, errScoutProjectTerminal
			}
			confirmed, reconcileErr := app.reconcileScoutHomeProjectLink(ctx, user, existing, key, text, projectToken)
			if reconcileErr != nil {
				if scoutHomeProjectTerminalError(reconcileErr) {
					terminal, terminalErr := app.failScoutHomeProjectLink(user, existing, reconcileErr)
					if terminalErr != nil {
						return existing, false, terminalErr
					}
					return terminal, false, errScoutProjectTerminal
				}
				return existing, false, reconcileErr
			}
			existing = confirmed
		}
		return existing, false, nil
	}

	now := time.Now().UTC()
	userMessage := scoutChatMessageRecord{
		ID:          operation.UserMessageID,
		Kind:        "message",
		Role:        "user",
		Text:        text,
		CreatedAt:   now.Format(time.RFC3339Nano),
		AuthorName:  scoutChatAuthorName(user),
		AuthorEmail: owner,
	}
	if projectToken.Kind != "" {
		userMessage.Project = &scoutChatProjectContext{Status: "pending", ContextRevision: 1, Title: projectToken.ProjectTitle, Basis: projectToken.Basis}
	}
	reply := scoutChatMessageRecord{
		ID:        operation.ReplyMessageID,
		Kind:      "message",
		Role:      "scout",
		CreatedAt: now.Format(time.RFC3339Nano),
		Reply: &scoutChatReplyLifecycle{
			OperationID: operation.OperationID,
			InReplyTo:   operation.UserMessageID,
			State:       scoutReplyStateQueued,
			QueuedAt:    now.Format(time.RFC3339Nano),
		},
	}
	if projectToken.Kind != "" {
		reply.Reply.State = scoutReplyStateProjectPending
	}
	thread := scoutChatThreadRecord{
		ID:               threadID,
		Title:            scoutChatThreadTitle(userMessage),
		Preview:          trimForStorage(text, 140),
		OwnerEmail:       owner,
		CreatedBy:        canonicalRoomActorName(user.Name),
		Visibility:       scoutChatVisibilityPrivate,
		CreatedAt:        now.Format(time.RFC3339Nano),
		UpdatedAt:        now.Format(time.RFC3339Nano),
		Messages:         []scoutChatMessageRecord{userMessage, reply},
		OpeningOperation: operation,
	}
	if projectToken.Kind != "" {
		thread.ProjectLinkOperations = []scoutChatProjectLinkOperation{{
			OperationID: operation.OperationID, TokenDigest: projectTokenDigest, MessageID: userMessage.ID,
			State: "pending", ProjectKind: projectToken.Kind, ProjectID: projectToken.ProjectID,
			ProjectRevision: projectToken.ProjectRevision, ProjectDigest: projectToken.ProjectDigest,
			ProjectTitle: projectToken.ProjectTitle, Basis: projectToken.Basis,
		}}
	}
	entryText, err := encodeScoutChatThread(thread)
	if err == nil {
		_, _, err = app.memory.appendScoutChatThread(thread.ID, entryText, scoutChatThreadMetadata(thread))
	}
	lock.Unlock()
	if err != nil {
		return scoutChatThreadRecord{}, false, err
	}

	if projectToken.Kind != "" {
		confirmed, reconcileErr := app.reconcileScoutHomeProjectLink(ctx, user, thread, key, text, projectToken)
		if reconcileErr != nil {
			if scoutHomeProjectTerminalError(reconcileErr) {
				terminal, terminalErr := app.failScoutHomeProjectLink(user, thread, reconcileErr)
				if terminalErr != nil {
					return thread, true, terminalErr
				}
				return terminal, true, errScoutProjectTerminal
			}
			return thread, true, reconcileErr
		}
		thread = confirmed
	}
	confirmedUserIndex := scoutChatMessageIndex(thread, operation.UserMessageID)
	confirmedReplyIndex := scoutChatMessageIndex(thread, operation.ReplyMessageID)
	if confirmedUserIndex < 0 || confirmedReplyIndex < 0 {
		return thread, true, ErrProjectAuthorityConflict
	}
	confirmedUserMessage := thread.Messages[confirmedUserIndex]
	confirmedReplyMessage := thread.Messages[confirmedReplyIndex]
	app.observeSTRIDETeamChatMessage(thread, confirmedUserMessage, "message", "")
	deliverScoutChatThreadMetadata(thread)
	app.sendScoutChatThreadUpdateToViewer(owner, thread, confirmedUserMessage)
	app.sendScoutChatThreadUpdateToViewer(owner, thread, confirmedReplyMessage)
	return thread, true, nil
}

func scoutOpeningOperationMatches(thread scoutChatThreadRecord, owner string, expected *scoutChatOpeningOperation) bool {
	actual := thread.OpeningOperation
	if actual == nil || expected == nil || normalizeAccountEmail(thread.OwnerEmail) != owner ||
		scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate || thread.Table || thread.Intake != "" ||
		actual.OperationID != expected.OperationID || actual.KeyDigest != expected.KeyDigest ||
		actual.BodyDigest != expected.BodyDigest || actual.UserMessageID != expected.UserMessageID ||
		actual.ReplyMessageID != expected.ReplyMessageID {
		return false
	}
	userIndex := scoutChatMessageIndex(thread, actual.UserMessageID)
	replyIndex := scoutChatMessageIndex(thread, actual.ReplyMessageID)
	return userIndex >= 0 && replyIndex >= 0 && thread.Messages[userIndex].Role == "user" &&
		scoutOpeningBodyMatches(thread, *actual, thread.Messages[userIndex]) &&
		thread.Messages[replyIndex].Reply != nil && thread.Messages[replyIndex].Reply.OperationID == actual.OperationID
}

func scoutOpeningBodyMatches(thread scoutChatThreadRecord, operation scoutChatOpeningOperation, message scoutChatMessageRecord) bool {
	text := strings.TrimSpace(message.Text)
	return scoutOpeningDigest(text) == operation.BodyDigest ||
		scoutOpeningDigest(text, firstScoutProjectTokenDigest(thread, operation.OperationID)) == operation.BodyDigest
}

func (app *kanbanBoardApp) startScoutOpeningReplyWorkers() {
	if app == nil {
		return
	}
	app.scoutReplyStartOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		queue := make(chan string, scoutReplyQueueSize)
		app.scoutReplyMu.Lock()
		app.scoutReplyCancel = cancel
		app.scoutReplyQueue = queue
		app.scoutReplyMu.Unlock()
		for index := 0; index < scoutReplyWorkerCount; index++ {
			app.scoutReplyWG.Add(1)
			go app.scoutOpeningReplyWorker(ctx, queue)
		}
		app.scoutReplyWG.Add(1)
		go app.scoutOpeningReplyRecoveryLoop(ctx)
	})
}

func (app *kanbanBoardApp) stopScoutOpeningReplyWorkers() {
	if app == nil {
		return
	}
	app.scoutReplyMu.Lock()
	cancel := app.scoutReplyCancel
	app.scoutReplyCancel = nil
	app.scoutReplyMu.Unlock()
	if cancel != nil {
		cancel()
		app.scoutReplyWG.Wait()
	}
}

func (app *kanbanBoardApp) queueScoutOpeningReply(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if app == nil || threadID == "" {
		return
	}
	app.startScoutOpeningReplyWorkers()
	app.scoutReplyMu.Lock()
	queue := app.scoutReplyQueue
	app.scoutReplyMu.Unlock()
	if queue == nil {
		return
	}
	select {
	case queue <- threadID:
	default:
		// The durable queued state is the source of truth. The recovery scanner
		// will re-enqueue after transient saturation.
	}
}

func (app *kanbanBoardApp) scoutOpeningReplyWorker(ctx context.Context, queue <-chan string) {
	defer app.scoutReplyWG.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case threadID := <-queue:
			app.processScoutOpeningReply(ctx, threadID)
		}
	}
}

func (app *kanbanBoardApp) scoutOpeningReplyRecoveryLoop(ctx context.Context) {
	defer app.scoutReplyWG.Done()
	app.recoverScoutOpeningReplies()
	app.recoverScoutChatModerations()
	ticker := time.NewTicker(scoutReplyRecoveryEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			app.recoverScoutOpeningReplies()
			app.recoverScoutChatModerations()
		}
	}
}

func (app *kanbanBoardApp) recoverScoutOpeningReplies() {
	if app == nil || app.memory == nil {
		return
	}
	now := time.Now().UTC()
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind != meetingMemoryKindScoutChat {
			continue
		}
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || thread.OpeningOperation == nil {
			continue
		}
		replyIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.ReplyMessageID)
		if replyIndex < 0 || thread.Messages[replyIndex].Reply == nil {
			continue
		}
		reply := thread.Messages[replyIndex].Reply
		if reply.State == scoutReplyStateQueued {
			app.queueScoutOpeningReply(thread.ID)
			continue
		}
		if reply.State != scoutReplyStateRunning {
			continue
		}
		expires, err := time.Parse(time.RFC3339Nano, reply.LeaseExpiresAt)
		if err != nil || !now.Before(expires) {
			app.requeueExpiredScoutOpeningReply(thread.ID, reply.LeaseID)
		}
	}
}

func (app *kanbanBoardApp) requeueExpiredScoutOpeningReply(threadID string, leaseID string) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	thread, ok := app.scoutOpeningThreadByID(threadID)
	if !ok || thread.OpeningOperation == nil {
		lock.Unlock()
		return
	}
	index := scoutChatMessageIndex(thread, thread.OpeningOperation.ReplyMessageID)
	if index < 0 || thread.Messages[index].Reply == nil || thread.Messages[index].Reply.State != scoutReplyStateRunning || thread.Messages[index].Reply.LeaseID != leaseID {
		lock.Unlock()
		return
	}
	reply := thread.Messages[index].Reply
	reply.State = scoutReplyStateQueued
	reply.QueuedAt = time.Now().UTC().Format(time.RFC3339Nano)
	reply.StartedAt = ""
	reply.LeaseID = ""
	reply.LeaseExpiresAt = ""
	if err := app.saveScoutChatThread(thread); err != nil {
		lock.Unlock()
		return
	}
	message := thread.Messages[index]
	lock.Unlock()
	app.sendScoutChatThreadUpdateToViewer(thread.OwnerEmail, thread, message)
	app.queueScoutOpeningReply(thread.ID)
}

func (app *kanbanBoardApp) scoutOpeningThreadByID(threadID string) (scoutChatThreadRecord, bool) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, false
	}
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind == meetingMemoryKindScoutChat && entry.ID == strings.TrimSpace(threadID) {
			thread, ok := decodeScoutChatThreadEntry(entry)
			return thread, ok
		}
	}
	return scoutChatThreadRecord{}, false
}

func randomScoutReplyLeaseID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return scoutOpeningDigest(fmt.Sprintf("%d", time.Now().UnixNano()))[:32]
}

func (app *kanbanBoardApp) claimScoutOpeningReply(threadID string) (scoutChatThreadRecord, scoutChatMessageRecord, string, bool) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	thread, ok := app.scoutOpeningThreadByID(threadID)
	if !ok || thread.OpeningOperation == nil || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate {
		lock.Unlock()
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, "", false
	}
	userIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.UserMessageID)
	replyIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.ReplyMessageID)
	if userIndex < 0 || replyIndex < 0 || thread.Messages[replyIndex].Reply == nil ||
		thread.Messages[replyIndex].Reply.State != scoutReplyStateQueued ||
		!scoutOpeningBodyMatches(thread, *thread.OpeningOperation, thread.Messages[userIndex]) {
		lock.Unlock()
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, "", false
	}
	now := time.Now().UTC()
	leaseID := randomScoutReplyLeaseID()
	reply := thread.Messages[replyIndex].Reply
	reply.State = scoutReplyStateRunning
	reply.Attempt++
	reply.StartedAt = now.Format(time.RFC3339Nano)
	reply.LeaseID = leaseID
	reply.LeaseExpiresAt = now.Add(scoutReplyLeaseDuration).Format(time.RFC3339Nano)
	reply.Retryable = false
	reply.ErrorCode = ""
	if err := app.saveScoutChatThread(thread); err != nil {
		lock.Unlock()
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, "", false
	}
	message := thread.Messages[replyIndex]
	userMessage := thread.Messages[userIndex]
	lock.Unlock()
	app.sendScoutChatThreadUpdateToViewer(thread.OwnerEmail, thread, message)
	return thread, userMessage, leaseID, true
}

func (app *kanbanBoardApp) processScoutOpeningReply(ctx context.Context, threadID string) {
	thread, userMessage, leaseID, claimed := app.claimScoutOpeningReply(threadID)
	if !claimed {
		return
	}
	user := accountStore().findUser(thread.OwnerEmail)
	if user == nil {
		app.finishScoutOpeningReply(threadID, leaseID, scoutChatMessageRecord{}, fmt.Errorf("thread owner is unavailable"))
		return
	}
	resolved, err := app.resolveScoutOpeningReply(ctx, user, thread, userMessage)
	if ctx.Err() != nil {
		// Leave the lease running. A successor process or the recovery scanner
		// will reclaim it; shutdown never converts healthy work into a user error.
		return
	}
	app.finishScoutOpeningReply(threadID, leaseID, resolved, err)
}

func (app *kanbanBoardApp) resolveScoutOpeningReply(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord) (scoutChatMessageRecord, error) {
	// This worker resolves the atomic opening turn. The thread did not exist
	// before that user message, so its causal history is empty even if recovery
	// claims it after the user has already sent later turns in the thread.
	// Folding those later messages backward would change the first answer based
	// on events that happened after it was asked.
	var history []scoutChatTurn
	query := strings.TrimSpace(userMessage.Text)
	// Home is another entrance into the same private Scout conversation, not a
	// different intelligence product. Resolve an explicit meeting/time request
	// through the server-owned briefing plane before the generic router/provider
	// so a brand-new conversation has exact parity with an existing typed thread
	// and Realtime voice.
	if !conversationRequestsDurableMeetingWork(query) {
		if briefingRange, ok := conversationMeetingBriefingRange(query, time.Now()); ok {
			principal := app.recallPrincipalForMemberRoom(user.Email, app.memberCurrentRoom(user.Email))
			briefing, _, err := app.crossMeetingBriefingToolForPrincipal(map[string]any{"range": briefingRange}, principal)
			if err != nil {
				return scoutChatMessageRecord{}, err
			}
			answer := strings.TrimSpace(asString(briefing["briefing"]))
			if answer == "" {
				answer = "Nothing currently authorized was captured in meeting memory for that range."
			}
			return scoutChatMessageRecord{
				Kind: "message", Role: "scout", AuthorName: scoutParticipantName,
				IntentOutcome: string(conversationIntentConversationalReply), CausedByMessageID: userMessage.ID, Text: answer,
			}, nil
		}
	}
	if verdict := app.routeScoutChatTurn(ctx, query, history); verdict != nil {
		if proposal := verdict.proposal; proposal != nil {
			return scoutChatMessageRecord{Kind: scoutChatMessageKindProposal, Role: "scout", Text: proposal.Summary, Proposal: proposal, proposalSource: verdict.source}, nil
		}
		if choices := verdict.choices; choices != nil {
			return scoutChatMessageRecord{Kind: scoutChatMessageKindChoices, Role: "scout", Text: choices.Question, Choices: choices}, nil
		}
	}
	query = app.prepareSTRIDEPrivateRelationshipModelQuery(user.Email, query)
	answerContext := withAssistantModelSuccessRequired(withAssistantResponseStyle(ctx, scoutChatResponseStyle(thread)))
	result, err := app.resolveAssistantQueryContextForUser(answerContext, user.Email, query, history)
	if err != nil {
		return scoutChatMessageRecord{}, err
	}
	answer := strings.TrimSpace(result.answer)
	if answer == "" {
		answer = "no answer yet"
	}
	return scoutChatMessageRecord{
		Kind:    "message",
		Role:    "scout",
		Text:    answer,
		Sources: groundAnswerInMessages(answer, []scoutChatMessageRecord{userMessage}, 3),
	}, nil
}

func scoutOpeningReplyError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(text, "timeout") || strings.Contains(text, "deadline"):
		return "timeout", true
	case strings.Contains(text, "rate limit") || strings.Contains(text, "429"):
		return "rate_limited", true
	case strings.Contains(text, "quota") || strings.Contains(text, "credit"):
		return "provider_quota", true
	case isProviderInvocationFailure(err):
		return "provider_unavailable", true
	case strings.Contains(text, "not configured") || strings.Contains(text, "authentication") || strings.Contains(text, "unauthorized"):
		return "configuration", false
	default:
		return "answer_failed", false
	}
}

func (app *kanbanBoardApp) finishScoutOpeningReply(threadID string, leaseID string, resolved scoutChatMessageRecord, callErr error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	thread, ok := app.scoutOpeningThreadByID(threadID)
	if !ok || thread.OpeningOperation == nil {
		lock.Unlock()
		return
	}
	userIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.UserMessageID)
	replyIndex := scoutChatMessageIndex(thread, thread.OpeningOperation.ReplyMessageID)
	if userIndex < 0 || replyIndex < 0 || thread.Messages[replyIndex].Reply == nil {
		lock.Unlock()
		return
	}
	placeholder := thread.Messages[replyIndex]
	if placeholder.Reply.State != scoutReplyStateRunning || placeholder.Reply.LeaseID != leaseID {
		lock.Unlock()
		return
	}
	now := time.Now().UTC()
	if thread.ArchivedAt != "" || !scoutOpeningBodyMatches(thread, *thread.OpeningOperation, thread.Messages[userIndex]) {
		placeholder.Text = scoutReplyCanceledAfterEditText
		placeholder.Reply.State = scoutReplyStateCanceled
		placeholder.Reply.FinishedAt = now.Format(time.RFC3339Nano)
		placeholder.Reply.LeaseID = ""
		placeholder.Reply.LeaseExpiresAt = ""
		thread.Messages[replyIndex] = placeholder
		if err := app.saveScoutChatThread(thread); err != nil {
			log.Errorf("Failed to persist canceled Scout opening reply: %v", err)
			lock.Unlock()
			return
		}
		lock.Unlock()
		app.sendScoutChatThreadUpdateToViewer(thread.OwnerEmail, thread, placeholder)
		return
	}
	lifecycle := *placeholder.Reply
	lifecycle.FinishedAt = now.Format(time.RFC3339Nano)
	lifecycle.LeaseID = ""
	lifecycle.LeaseExpiresAt = ""
	if callErr != nil {
		code, retryable := scoutOpeningReplyError(callErr)
		lifecycle.State = scoutReplyStateFailed
		lifecycle.Retryable = retryable
		lifecycle.ErrorCode = code
		resolved = scoutChatMessageRecord{
			ID:        placeholder.ID,
			Kind:      "message",
			Role:      "scout",
			Text:      scoutReplySafeFailureText,
			CreatedAt: placeholder.CreatedAt,
			Reply:     &lifecycle,
		}
	} else {
		lifecycle.State = scoutReplyStateCompleted
		lifecycle.Retryable = false
		lifecycle.ErrorCode = ""
		resolved.ID = placeholder.ID
		resolved.CreatedAt = placeholder.CreatedAt
		resolved.Reply = &lifecycle
	}
	thread.Messages[replyIndex] = resolved
	if replyIndex == len(thread.Messages)-1 {
		updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, resolved)
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		lock.Unlock()
		return
	}
	lock.Unlock()
	app.observeSTRIDETeamChatMessage(thread, resolved, "message", "")
	app.sendScoutChatThreadUpdateToViewer(thread.OwnerEmail, thread, resolved)
	if callErr == nil && resolved.Proposal != nil {
		recordProposalEvent(proposalEventMinted, resolved.ID, scoutChatProposalMintFields(
			firstNonEmptyString(resolved.proposalSource, proposalSourceChatRouter), thread.ID, thread.OpeningOperation.UserMessageID, resolved.Proposal,
		))
	}
}

func (app *kanbanBoardApp) retryScoutOpeningReply(viewerEmail string, threadID string, replyID string) (scoutChatThreadRecord, scoutChatMessageRecord, error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		lock.Unlock()
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, err
	}
	if thread.ArchivedAt != "" || thread.OpeningOperation == nil || thread.OpeningOperation.ReplyMessageID != strings.TrimSpace(replyID) {
		lock.Unlock()
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, fmt.Errorf("retryable Scout reply not found")
	}
	index := scoutChatMessageIndex(thread, replyID)
	if index < 0 || thread.Messages[index].Reply == nil || thread.Messages[index].Reply.State != scoutReplyStateFailed || !thread.Messages[index].Reply.Retryable {
		lock.Unlock()
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, fmt.Errorf("Scout reply is not retryable")
	}
	reply := thread.Messages[index].Reply
	reply.State = scoutReplyStateQueued
	reply.QueuedAt = time.Now().UTC().Format(time.RFC3339Nano)
	reply.StartedAt = ""
	reply.FinishedAt = ""
	reply.LeaseID = ""
	reply.LeaseExpiresAt = ""
	reply.Retryable = false
	reply.ErrorCode = ""
	thread.Messages[index].Text = ""
	if err := app.saveScoutChatThread(thread); err != nil {
		lock.Unlock()
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, err
	}
	message := thread.Messages[index]
	lock.Unlock()
	app.sendScoutChatThreadUpdateToViewer(thread.OwnerEmail, thread, message)
	app.queueScoutOpeningReply(thread.ID)
	return thread, message, nil
}
