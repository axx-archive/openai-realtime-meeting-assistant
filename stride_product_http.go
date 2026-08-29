package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// Implemented below as closed product lifecycle routers.
func strideProductMarketplaceSubrouteHandler(w http.ResponseWriter, r *http.Request) {
	strideProductMarketplaceRoute(w, r)
}
func strideProductRosterSubrouteHandler(w http.ResponseWriter, r *http.Request) {
	strideProductRosterRoute(w, r)
}
func strideProductMarketplaceRoute(w http.ResponseWriter, r *http.Request) {
	strideProductMarketplaceHandle(w, r)
}
func strideProductRosterRoute(w http.ResponseWriter, r *http.Request) {
	strideProductRosterHandle(w, r)
}

const strideLegacyRosterMutationEnv = "STRIDE_ADMIN_LEGACY_ROSTER_MUTATIONS"

func strideLegacyRosterMutationEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(strideLegacyRosterMutationEnv)), "true")
}

func strideProductAuthenticated(w http.ResponseWriter, r *http.Request, methods ...string) (*userAccount, *STRIDERuntime, bool) {
	allowed := false
	for _, method := range methods {
		allowed = allowed || r.Method == method
	}
	if !allowed {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil, nil, false
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return nil, nil, false
	}
	if strings.TrimSpace(r.URL.Query().Get("tenantId")) != "" || strings.TrimSpace(r.URL.Query().Get("orgId")) != "" {
		writeAuthError(w, http.StatusForbidden, "tenant scope is server-derived")
		return nil, nil, false
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil, nil, false
	}
	if kanbanApp == nil || kanbanApp.strideRuntime == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "STRIDE runtime is unavailable")
		return nil, nil, false
	}
	return user, kanbanApp.strideRuntime, true
}

func decodeSTRIDEProductBody(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrSTRIDEProductInvalid
	}
	return nil
}

func writeSTRIDEProductError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, ErrSTRIDEProductDisabled), errors.Is(err, ErrSTRIDELearningUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrSTRIDEProductUnknown):
		status = http.StatusNotFound
	case errors.Is(err, ErrSTRIDEProductConflict), errors.Is(err, ErrSTRIDELearningGate):
		status = http.StatusConflict
	case errors.Is(err, ErrSTRIDEWorkSourceChanged), errors.Is(err, ErrSTRIDELearningPrivacy):
		status = http.StatusGone
	case errors.Is(err, ErrSTRIDEProductDenied), errors.Is(err, ErrSTRIDEAdminRequired):
		status = http.StatusForbidden
	}
	writeAuthError(w, status, err.Error())
}

func strideProductWorkSubrouteHandler(w http.ResponseWriter, r *http.Request) {
	user, runtime, ok := strideProductAuthenticated(w, r, http.MethodGet, http.MethodPost)
	if !ok {
		return
	}
	parts := splitSTRIDEProductPath(r.URL.Path, strideRuntimeAPIBase+"work/")
	if len(parts) == 2 && parts[0] == "suggestions" && parts[1] == "from-meeting" && r.Method == http.MethodPost {
		strideProductCreateMeetingSuggestion(w, r, user, runtime)
		return
	}
	if len(parts) == 1 && parts[0] == "suggestions" && r.Method == http.MethodPost {
		strideProductCreateSuggestion(w, r, user, runtime)
		return
	}
	if len(parts) >= 2 && parts[0] == "suggestions" {
		if len(parts) == 3 && parts[2] == "evidence" && r.Method == http.MethodGet {
			strideProductEvidence(w, user, runtime, parts[1])
			return
		}
		strideProductSuggestionAction(w, r, user, runtime, parts[1:])
		return
	}
	if len(parts) == 3 && parts[0] == "runs" && parts[2] == "artifact" && r.Method == http.MethodGet {
		strideProductArtifact(w, r, user, runtime, parts[1])
		return
	}
	if len(parts) == 3 && parts[0] == "runs" && parts[2] == "feedback" && r.Method == http.MethodPost {
		strideProductFeedback(w, r, user, runtime, parts[1])
		return
	}
	http.NotFound(w, r)
}

func strideProductCreateMeetingSuggestion(w http.ResponseWriter, r *http.Request, user *userAccount, runtime *STRIDERuntime) {
	var payload struct {
		RoomID string `json:"roomId"`
	}
	if decodeSTRIDEProductBody(w, r, &payload) != nil {
		writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
		return
	}
	result, recipients, meetingScope, err := strideMeetingSuggestionEvidence(r.Context(), kanbanApp, user, strings.TrimSpace(payload.RoomID))
	if err != nil {
		writeSTRIDEProductError(w, ErrSTRIDEProductDenied)
		return
	}
	principal := strideRuntimePrincipalForEmail(user.Email)
	var record STRIDEProductWorkRecord
	err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		authority := &appMeetingSpecialistProductAuthority{app: kanbanApp, runtime: runtime}
		if currentErr := authority.ScopeCurrent(r.Context(), meetingScope); currentErr != nil {
			return ErrSTRIDEProductDenied
		}
		return currentConsentLaneAuthority().CommitWithFences(r.Context(), meetingScope.ConsentFences, func() error {
			var createErr error
			destination := kanbanApp.strideProductRecommendDestination(result.RoomID, result.Text, meetingScope.Audience, recipients, user.Email, ctx.Receipt.IssuedAt)
			record, createErr = ctx.Product.createMeetingSuggestionWithDestination(result, principal, recipients, ctx.Receipt.IssuedAt, destination, meetingScope)
			return createErr
		})
	})
	if err == nil {
		err = runtime.Save()
	}
	if err != nil {
		if errors.Is(err, ErrSTRIDEWorkSourceChanged) {
			err = ErrSTRIDEProductDenied
		}
		writeSTRIDEProductError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "suggestion": record, "source": "consent_authorized_meeting", "providerCalls": 0})
}

func strideMeetingSuggestionEvidence(ctx context.Context, app *kanbanBoardApp, user *userAccount, roomID string) (STRIDETemporalRecallResult, []string, meetingSpecialistProductScope, error) {
	if app == nil || user == nil {
		return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
	}
	authority := &appMeetingSpecialistProductAuthority{app: app, runtime: app.strideRuntime}
	scope, err := authority.ResolveScope(ctx, user, roomID)
	if err != nil {
		return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
	}
	app.mu.Lock()
	live, found := app.roomLive[normalizeRoomID(scope.RoomID)]
	current := found && live.mediaActor != nil && live.mediaGen == scope.MediaGeneration && live.mediaSittingID == scope.SittingID
	participants := []string(nil)
	if current {
		participants = app.participantSnapshotLocked(live)
	}
	app.mu.Unlock()
	if !current || len(participants) < 2 {
		return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
	}
	accounts := make([]*userAccount, 0, len(participants))
	seen := map[string]bool{}
	for _, participant := range participants {
		consentPrincipal, ok := app.consentPrincipalForTranscriptSpeaker(scope.RoomID, participant)
		if !ok {
			return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
		}
		// Guests remain fully represented by the durable scope audience and
		// consent fences, but they do not receive account-only Work UI records.
		if consentPrincipal.Kind == "guest" {
			continue
		}
		if consentPrincipal.Kind != "user" {
			return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
		}
		email := normalizeAccountEmail(consentPrincipal.ID)
		account := accountStore().findUser(email)
		if account == nil {
			return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
		}
		if !seen[email] {
			seen[email] = true
			accounts = append(accounts, account)
		}
	}
	sort.Slice(accounts, func(i, j int) bool {
		return normalizeAccountEmail(accounts[i].Email) < normalizeAccountEmail(accounts[j].Email)
	})
	var shared STRIDETemporalRecallResult
	recipients := make([]string, 0, len(accounts))
	for index, account := range accounts {
		answer, answerErr := app.answerSTRIDETemporalForMember(ctx, account, scope.RoomID, TemporalQueryLastFiveMinutes)
		if answerErr != nil {
			return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
		}
		if index == 0 {
			shared = answer
		} else if answer.EvidenceDigest != shared.EvidenceDigest || answer.TranscriptHighWater != shared.TranscriptHighWater || answer.AnalysisHighWater != shared.AnalysisHighWater {
			return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
		}
		recipients = append(recipients, strideRuntimePrincipalForEmail(account.Email))
	}
	if !strideWorkContainsString(recipients, strideRuntimePrincipalForEmail(user.Email)) || len(recipients) < 2 {
		return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
	}
	if err := authority.ScopeCurrent(ctx, scope); err != nil {
		return STRIDETemporalRecallResult{}, nil, meetingSpecialistProductScope{}, ErrSTRIDEProductDenied
	}
	return shared, uniqueSortedStrings(recipients), scope, nil
}

func strideProductEvidence(w http.ResponseWriter, user *userAccount, runtime *STRIDERuntime, id string) {
	principal := strideRuntimePrincipalForEmail(user.Email)
	var record STRIDEProductWorkRecord
	sourceCurrent := false
	changed := false
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		var readErr error
		record, sourceCurrent, changed, readErr = ctx.reauthorizeWorkForRead(principal, id, ctx.Receipt.IssuedAt)
		return readErr
	})
	if err == nil && changed {
		err = runtime.Save()
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	if !sourceCurrent {
		writeSTRIDEProductError(w, ErrSTRIDEWorkSourceChanged)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "evidence": map[string]any{"sourceEvent": record.SourceEvent, "sourceEvents": record.SourceEvents, "sourceSnippet": record.SourceSnippet, "threadId": record.SourceThreadID, "messageId": record.SourceMessageID, "brainLinked": true, "sourceCurrent": true}})
}

func splitSTRIDEProductPath(path, prefix string) []string {
	suffix := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if suffix == "" {
		return nil
	}
	return strings.Split(suffix, "/")
}

func strideProductCreateSuggestion(w http.ResponseWriter, r *http.Request, user *userAccount, runtime *STRIDERuntime) {
	payload := struct{ ThreadID, MessageID, Title, Outcome string }{}
	if err := decodeSTRIDEProductBody(w, r, &payload); err != nil {
		writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
		return
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(user.Email, strings.TrimSpace(payload.ThreadID))
	if err != nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" {
		writeSTRIDEProductError(w, ErrSTRIDEProductDenied)
		return
	}
	index := scoutChatMessageIndex(thread, strings.TrimSpace(payload.MessageID))
	if index < 0 || thread.Messages[index].Role != "user" {
		writeSTRIDEProductError(w, ErrSTRIDEProductDenied)
		return
	}
	message := thread.Messages[index]
	principal := strideRuntimePrincipalForEmail(user.Email)
	var record STRIDEProductWorkRecord
	err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		snapshot, e := ctx.Conversation.Snapshot()
		if e != nil {
			return e
		}
		var event *ConversationEvent
		for i := len(snapshot.Events) - 1; i >= 0; i-- {
			candidate := snapshot.Events[i].Append.Event
			if !snapshot.Events[i].Invalidated && candidate.ThreadID == thread.ID && candidate.SourceID == message.ID && candidate.EventType == "message" {
				copy := candidate
				event = &copy
				break
			}
		}
		if event == nil {
			return ErrSTRIDEProductDenied
		}
		outcome := firstNonEmptyString(strings.TrimSpace(payload.Outcome), message.Text)
		recipients, _, recipientErr := strideProductConversationRecipients(*event, principal)
		if recipientErr != nil {
			return recipientErr
		}
		destination := kanbanApp.strideProductRecommendDestination(thread.ID, outcome, event.Audience, recipients, user.Email, ctx.Receipt.IssuedAt)
		record, e = ctx.Product.createSuggestionWithDestination(*event, thread, message, firstNonEmptyString(strings.TrimSpace(payload.Title), "Insights & Opportunities report"), outcome, principal, ctx.Receipt.IssuedAt, destination)
		return e
	})
	if err == nil {
		err = runtime.Save()
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "suggestion": record})
}

func strideProductSuggestionAction(w http.ResponseWriter, r *http.Request, user *userAccount, runtime *STRIDERuntime, parts []string) {
	id := parts[0]
	principal := strideRuntimePrincipalForEmail(user.Email)
	if len(parts) == 1 && r.Method == http.MethodGet {
		var record STRIDEProductWorkRecord
		sourceCurrent := false
		changed := false
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
			var readErr error
			record, sourceCurrent, changed, readErr = ctx.reauthorizeWorkForRead(principal, id, ctx.Receipt.IssuedAt)
			return readErr
		})
		if err == nil && changed {
			err = runtime.Save()
		}
		if err != nil {
			writeSTRIDEProductError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "suggestion": record, "sourceCurrent": sourceCurrent})
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	kanbanApp.strideProductMu.Lock()
	defer kanbanApp.strideProductMu.Unlock()
	action := parts[1]
	switch action {
	case "edit":
		payload := struct {
			Revision       int64
			Title, Outcome string
		}{}
		if decodeSTRIDEProductBody(w, r, &payload) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		var record STRIDEProductWorkRecord
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
			_, sourceCurrent, _, sourceErr := ctx.reauthorizeWorkForRead(principal, id, ctx.Receipt.IssuedAt)
			if sourceErr != nil {
				return sourceErr
			}
			if !sourceCurrent {
				return ErrSTRIDEWorkSourceChanged
			}
			if replay, ok := ctx.Product.exactWorkReplay(id, payload.Revision, principal, func(value STRIDEProductWorkRecord) bool {
				return value.Status == "suggested" && value.Title == trimForStorage(strings.TrimSpace(payload.Title), 120) && value.Outcome == trimForStorage(strings.TrimSpace(payload.Outcome), 1200)
			}); ok {
				record = replay
				return nil
			}
			var e error
			record, e = ctx.Product.reviseWork(id, payload.Revision, principal, func(v *STRIDEProductWorkRecord) error {
				title := strings.TrimSpace(payload.Title)
				outcome := strings.TrimSpace(payload.Outcome)
				if title == "" || outcome == "" {
					return ErrSTRIDEProductInvalid
				}
				v.Title = trimForStorage(title, 120)
				v.Outcome = trimForStorage(outcome, 1200)
				v.Lifecycle = append(v.Lifecycle, "scope_edited")
				return nil
			}, time.Now().UTC())
			return e
		})
		strideProductWriteMutation(w, runtime, record, err)
		return
	case "dismiss":
		payload := struct {
			Revision int64
			Reason   string
		}{}
		if decodeSTRIDEProductBody(w, r, &payload) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		var record STRIDEProductWorkRecord
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
			_, sourceCurrent, _, sourceErr := ctx.reauthorizeWorkForRead(principal, id, ctx.Receipt.IssuedAt)
			if sourceErr != nil {
				return sourceErr
			}
			if !sourceCurrent {
				return ErrSTRIDEWorkSourceChanged
			}
			if replay, ok := ctx.Product.exactWorkReplay(id, payload.Revision, principal, func(value STRIDEProductWorkRecord) bool {
				return value.Status == "dismissed"
			}); ok {
				record = replay
				return nil
			}
			var e error
			record, e = ctx.Product.reviseWork(id, payload.Revision, principal, func(v *STRIDEProductWorkRecord) error {
				if strings.TrimSpace(payload.Reason) == "" {
					return ErrSTRIDEProductInvalid
				}
				v.Status = "dismissed"
				v.Lifecycle = append(v.Lifecycle, "dismissed_by_human")
				return nil
			}, time.Now().UTC())
			return e
		})
		strideProductWriteMutation(w, runtime, record, err)
		return
	case "destination":
		strideProductSetDestination(w, r, user, runtime, id, principal)
		return
	case "approve":
		strideProductApprove(w, r, user, runtime, id, principal)
		return
	default:
		http.NotFound(w, r)
	}
}

func strideProductWriteMutation(w http.ResponseWriter, runtime *STRIDERuntime, record STRIDEProductWorkRecord, err error) {
	if err == nil {
		err = runtime.Save()
	} else if errors.Is(err, ErrSTRIDEWorkSourceChanged) {
		if saveErr := runtime.Save(); saveErr != nil {
			err = saveErr
		}
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "suggestion": record})
}

func strideProductSetDestination(w http.ResponseWriter, r *http.Request, user *userAccount, runtime *STRIDERuntime, id, principal string) {
	payload := struct {
		Revision              int64
		Mode, ThreadID, Title string
	}{}
	if decodeSTRIDEProductBody(w, r, &payload) != nil {
		writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
		return
	}
	var pre STRIDEProductWorkRecord
	replayed := false
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		var found bool
		pre, found = ctx.Product.workRecord(id)
		if !found {
			return ErrSTRIDEProductUnknown
		}
		if pre.Revision == payload.Revision+1 && pre.OwnerID == principal && pre.Status == "suggested" && pre.DestinationMode == payload.Mode && ((payload.Mode == "existing" && pre.DestinationThreadID == strings.TrimSpace(payload.ThreadID)) || (payload.Mode == "new" && pre.DestinationTitle == firstNonEmptyString(strings.TrimSpace(payload.Title), pre.Title))) {
			replayed = true
			return nil
		}
		if pre.Revision != payload.Revision || pre.OwnerID != principal {
			return ErrSTRIDEProductConflict
		}
		return nil
	})
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	if replayed {
		destination, _, destinationErr := kanbanApp.scoutChatThreadByID(user.Email, pre.DestinationThreadID)
		destinationAudience, destinationACLVersion, authorityErr := strideProductProjectDestinationAuthority(destination)
		if destinationErr != nil || authorityErr != nil || pre.DestinationAudience == nil ||
			!sameAudience(*pre.DestinationAudience, destinationAudience) || pre.DestinationACLVersion != destinationACLVersion {
			writeSTRIDEProductError(w, ErrSTRIDEProductDenied)
			return
		}
		err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
			_, sourceCurrent, _, sourceErr := ctx.reauthorizeWorkForRead(principal, id, ctx.Receipt.IssuedAt)
			if sourceErr != nil {
				return sourceErr
			}
			if !sourceCurrent {
				return ErrSTRIDEWorkSourceChanged
			}
			return strideProductDestinationAllowed(ctx, principal, pre, destinationAudience)
		})
		if err == nil {
			err = runtime.Save()
		}
		if err != nil {
			writeSTRIDEProductError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "suggestion": pre, "replayed": true})
		return
	}
	// Revalidate the exact source/meeting authority before creating or opening
	// any destination, then revalidate again at the durable selection below.
	err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		_, current, _, readErr := ctx.reauthorizeWorkForRead(principal, id, ctx.Receipt.IssuedAt)
		if readErr != nil {
			return readErr
		}
		if !current {
			return ErrSTRIDEWorkSourceChanged
		}
		return nil
	})
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	var destination scoutChatThreadRecord
	switch payload.Mode {
	case "existing":
		destination, _, err = kanbanApp.scoutChatThreadByID(user.Email, strings.TrimSpace(payload.ThreadID))
		if err != nil || !strideProductProjectDestinationEligible(destination) {
			err = ErrSTRIDEProductDenied
		}
	case "new":
		title := firstNonEmptyString(strings.TrimSpace(payload.Title), pre.Title)
		if strideProductReservedProjectTitle(title) {
			err = ErrSTRIDEProductDenied
			break
		}
		memberEmails, memberErr := strideProductRecipientEmails(pre.RecipientIDs)
		if memberErr != nil {
			err = memberErr
			break
		}
		threadID := "stride_project_" + temporalDigest(pre.ID + "\x00project_destination")[:20]
		destination, _, err = kanbanApp.ensureScoutChatThread(threadID, user.Email, user.Name, title, scoutChatVisibilityPublic, memberEmails)
		if err == nil && !strideProductProjectDestinationEligible(destination) {
			err = ErrSTRIDEProductDenied
		}
	default:
		err = ErrSTRIDEProductInvalid
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	destinationAudience, destinationACLVersion, err := strideProductProjectDestinationAuthority(destination)
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	var record STRIDEProductWorkRecord
	err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		_, sourceCurrent, _, sourceErr := ctx.reauthorizeWorkForRead(principal, id, ctx.Receipt.IssuedAt)
		if sourceErr != nil {
			return sourceErr
		}
		if !sourceCurrent {
			return ErrSTRIDEWorkSourceChanged
		}
		if destinationErr := strideProductDestinationAllowed(ctx, principal, pre, destinationAudience); destinationErr != nil {
			return destinationErr
		}
		var e error
		record, e = ctx.Product.reviseWork(id, payload.Revision, principal, func(v *STRIDEProductWorkRecord) error {
			v.DestinationMode = payload.Mode
			v.DestinationThreadID = destination.ID
			v.DestinationTitle = destination.Title
			audience := cloneAudience(destinationAudience)
			v.DestinationAudience = &audience
			v.DestinationACLVersion = destinationACLVersion
			v.Lifecycle = append(v.Lifecycle, "destination_explicitly_selected")
			return nil
		}, time.Now().UTC())
		return e
	})
	strideProductWriteMutation(w, runtime, record, err)
}

func strideProductApprove(w http.ResponseWriter, r *http.Request, user *userAccount, runtime *STRIDERuntime, id, principal string) {
	payload := struct{ Revision int64 }{}
	if decodeSTRIDEProductBody(w, r, &payload) != nil {
		writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
		return
	}
	var pre STRIDEProductWorkRecord
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		var found bool
		pre, found = ctx.Product.workRecord(id)
		if !found {
			return ErrSTRIDEProductUnknown
		}
		if pre.Status == "completed" && pre.Revision == payload.Revision && strideWorkContainsString(pre.ApprovalPolicy.EligiblePrincipals, principal) {
			return nil
		}
		if pre.Status != "suggested" || pre.Revision != payload.Revision || !strideWorkContainsString(pre.ApprovalPolicy.EligiblePrincipals, principal) || !strideIdentifier(pre.DestinationThreadID) {
			return ErrSTRIDEProductConflict
		}
		return nil
	})
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	// Chat source edits project into STRIDE while holding their source lock.
	// Hold a sorted source+destination lock set through admission and snapshot
	// save, closing both the edit projection window and destination ACL drift.
	unlockThreads := kanbanApp.lockScoutChatThreadSet(pre.SourceThreadID, pre.DestinationThreadID)
	defer unlockThreads()
	destination, _, destinationErr := kanbanApp.scoutChatThreadByID(user.Email, pre.DestinationThreadID)
	currentAudience, currentACLVersion, authorityErr := strideProductProjectDestinationAuthority(destination)
	if destinationErr != nil || authorityErr != nil || pre.DestinationAudience == nil || !sameAudience(*pre.DestinationAudience, currentAudience) || pre.DestinationACLVersion != currentACLVersion {
		writeSTRIDEProductError(w, ErrSTRIDEProductDenied)
		return
	}
	var record STRIDEProductWorkRecord
	err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		_, sourceCurrent, _, sourceErr := ctx.reauthorizeWorkForRead(principal, id, ctx.Receipt.IssuedAt)
		if sourceErr != nil {
			return sourceErr
		}
		if !sourceCurrent {
			return ErrSTRIDEWorkSourceChanged
		}
		current, found := ctx.Product.workRecord(id)
		if !found {
			return ErrSTRIDEProductUnknown
		}
		if current.DestinationAudience == nil || current.DestinationThreadID != destination.ID || current.DestinationTitle != destination.Title ||
			!sameAudience(*current.DestinationAudience, currentAudience) || current.DestinationACLVersion != currentACLVersion {
			return ErrSTRIDEProductDenied
		}
		if destinationErr := strideProductDestinationAllowed(ctx, principal, current, currentAudience); destinationErr != nil {
			return destinationErr
		}
		var e error
		// Bind the deterministic run to the receipt's authoritative clock. A
		// caller timestamp captured before WithProductContext mints the receipt
		// would otherwise (correctly) fail future-issued receipt validation.
		record, e = ctx.approveAndRunWork(principal, id, payload.Revision, ctx.Receipt.IssuedAt)
		return e
	})
	if err == nil {
		err = runtime.Save()
	} else if errors.Is(err, ErrSTRIDEWorkSourceChanged) {
		// approveAndRunWork has already durably invalidated and purged the stale
		// product record in memory. Persist that terminal state before reporting
		// the conflict; never resurrect the stale proposal after restart.
		if saveErr := runtime.Save(); saveErr != nil {
			err = saveErr
		}
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	// The destination lock remains held while the completion is reauthorized
	// and posted, so title, archive, and membership cannot race this boundary.
	thread, _, threadErr := kanbanApp.scoutChatThreadByID(user.Email, record.DestinationThreadID)
	postAudience, postACLVersion, postAuthorityErr := strideProductProjectDestinationAuthority(thread)
	if !record.CompletionPosted && threadErr == nil && postAuthorityErr == nil && record.DestinationAudience != nil && sameAudience(*record.DestinationAudience, postAudience) && record.DestinationACLVersion == postACLVersion {
		resultArtifact, resultErr := ensureSTRIDEProductConversationResult(kanbanApp, thread, user, record)
		if resultErr != nil {
			writeSTRIDEProductError(w, resultErr)
			return
		}
		resultVersion := artifactVersion(resultArtifact)
		resultDigest := strings.ToLower(strings.TrimSpace(artifactCapabilityDigest(resultArtifact)))
		resultType := artifactType(resultArtifact)
		if record.ResultArtifactID != resultArtifact.ID || record.ResultArtifactVersion != resultVersion || !strings.EqualFold(record.ResultArtifactDigest, resultDigest) || record.ResultArtifactType != resultType {
			bindErr := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
				return ctx.withWorkAuthority(principal, "result_artifact_bind", record, func() error {
					ctx.Product.mu.Lock()
					defer ctx.Product.mu.Unlock()
					current, found := ctx.Product.work[id]
					if !found || current.Revision != record.Revision || current.Status != "completed" || (current.ResultArtifactID != "" && (current.ResultArtifactID != resultArtifact.ID || current.ResultArtifactVersion != resultVersion || !strings.EqualFold(current.ResultArtifactDigest, resultDigest) || current.ResultArtifactType != resultType)) {
						return ErrSTRIDEProductConflict
					}
					current.ResultArtifactID = resultArtifact.ID
					current.ResultArtifactType = resultType
					current.ResultArtifactVersion = resultVersion
					current.ResultArtifactDigest = resultDigest
					if !strideWorkContainsString(current.Lifecycle, "conversation_result_bound") {
						current.Lifecycle = append(current.Lifecycle, "conversation_result_bound")
					}
					ctx.Product.work[id] = current
					record = current
					return nil
				})
			})
			if bindErr == nil {
				bindErr = runtime.Save()
			}
			if bindErr != nil {
				writeSTRIDEProductError(w, bindErr)
				return
			}
		}
		message := scoutChatMessageRecord{
			ID: "stride-work-completion-" + temporalDigest(record.ID + "\x00" + record.RunID)[:20], Kind: "work_result", Role: "scout", AuthorName: "Scout",
			Text: fmt.Sprintf("%s is complete. %s\n\nArtifact: %s", record.Title, record.CompletionSummary, record.ArtifactHref), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Work: &scoutChatWorkRecordRef{
				ID: record.ID, RunID: record.RunID, RootRunID: record.RunID, Title: record.Title, Status: record.Status, WorkerName: "Scout",
				CurrentStage: "Report ready", ProgressPercent: 100, Summary: record.CompletionSummary,
				ArtifactID: record.ArtifactID, ArtifactHref: record.ArtifactHref, EvidenceHref: record.BrainHref,
				ResultArtifactID: record.ResultArtifactID, ResultArtifactType: record.ResultArtifactType,
				ResultArtifactVersion: record.ResultArtifactVersion, ResultArtifactDigest: record.ResultArtifactDigest,
				ResultTitle: record.Title, OutputFamily: "Document",
				ProviderExecutionFenced: record.ProviderExecutionFenced,
			},
		}
		completionMessageCommitted := false
		postErr := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
			return ctx.withWorkAuthority(principal, "completion_post", record, func() error {
				// The Product transaction already owns runtime.mu. Persist the
				// exact-once chat message here, but defer its STRIDE conversation
				// projection until after WithProductContext releases runtime.mu;
				// observeSTRIDETeamChatMessage legitimately re-enters the runtime.
				if !strideProductCommitMessageOnceLockedWithoutObservation(thread, message) {
					return ErrSTRIDEProductDenied
				}
				completionMessageCommitted = true
				ctx.Product.mu.Lock()
				updated := ctx.Product.work[id]
				updated.CompletionPosted = true
				updated.Lifecycle = append(updated.Lifecycle, "completion_reported_in_destination_thread")
				ctx.Product.work[id] = updated
				ctx.Product.mu.Unlock()
				record = updated
				return ctx.persistLifecycleCheckpoint("completion_post_saved")
			})
		})
		if completionMessageCommitted {
			// The chat row is already durable and message IDs are deterministic.
			// Projection is idempotent, and startup replay is the crash backstop.
			// Live delivery also happens here, after runtime.mu is released: its
			// viewer projection may consult the Product ledger to upgrade a legacy
			// direct-agent thread, which would self-deadlock inside the transaction.
			kanbanApp.observeSTRIDETeamChatMessage(thread, message, "message", "")
			deliverScoutChatThreadUpdate(thread, message)
		}
		if errors.Is(postErr, ErrSTRIDEWorkSourceChanged) {
			writeSTRIDEProductError(w, postErr)
			return
		}
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "suggestion": record, "providerCalls": 0, "inputTokens": 0, "outputTokens": 0})
}

func ensureSTRIDEProductConversationResult(app *kanbanBoardApp, thread scoutChatThreadRecord, user *userAccount, record STRIDEProductWorkRecord) (meetingMemoryEntry, error) {
	if app == nil || user == nil || record.Status != "completed" || strings.TrimSpace(record.RunID) == "" || strings.TrimSpace(thread.ID) == "" {
		return meetingMemoryEntry{}, ErrSTRIDEProductInvalid
	}
	resultID := "stride-work-result-" + temporalDigest(record.ID + "\x00" + record.RunID)[:24]
	body := strings.TrimSpace(fmt.Sprintf("# %s\n\n%s\n\n## Approved outcome\n\n%s\n\n## Verified source\n\n%s", record.Title, record.CompletionSummary, record.Outcome, record.SourceSnippet))
	metadata := map[string]string{
		"type": artifactTypeMarkdown, "source": "stride_product_work", "status": "complete", "threadStatus": "complete",
		"title": strings.TrimSpace(record.Title), "originSurface": "chat:" + thread.ID, "requestedBy": normalizeAccountEmail(user.Email),
		"threadId": record.RunID, "sourceRunId": record.RunID, "sourceArtifactId": record.ArtifactID, "outputFamily": "Document",
	}
	artifact, _, _, err := app.createOSArtifactWithIDAndMetadataAcknowledged(resultID, "research", record.Title, body, user.Name, metadata)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	if artifact.ID != resultID || artifactType(artifact) != artifactTypeMarkdown || artifact.Metadata["source"] != "stride_product_work" ||
		artifact.Metadata["sourceRunId"] != record.RunID || artifact.Metadata["sourceArtifactId"] != record.ArtifactID ||
		!oneOf(strings.ToLower(strings.TrimSpace(artifact.Metadata["threadStatus"])), codexJobStatusComplete, artifactStatusApproved, artifactStatusPublished) ||
		!scoutChatArtifactHasClosedResultEnvelope(artifact) {
		return meetingMemoryEntry{}, ErrSTRIDEProductConflict
	}
	return artifact, nil
}

func strideProductReservedProjectTitle(value string) bool {
	title := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "#")
	return title == "team" || title == "general"
}

func strideProductProjectDestinationEligible(thread scoutChatThreadRecord) bool {
	return strings.TrimSpace(thread.ID) != "" && strings.TrimSpace(thread.Title) != "" && scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic && thread.ArchivedAt == "" && !thread.Table && !strideProductReservedProjectTitle(thread.Title)
}

func strideProductProjectDestinationAuthority(thread scoutChatThreadRecord) (STRIDEAudience, int64, error) {
	if !strideProductProjectDestinationEligible(thread) {
		return STRIDEAudience{}, 0, ErrSTRIDEProductDenied
	}
	audience, version, err := strideRuntimeChatAuthority(thread)
	if err != nil || audience.Validate() != nil {
		return STRIDEAudience{}, 0, ErrSTRIDEProductDenied
	}
	return audience, version, nil
}

func strideProductRecipientEmails(recipientIDs []string) ([]string, error) {
	wanted := uniqueSortedStrings(recipientIDs)
	if len(wanted) < 2 {
		return nil, ErrSTRIDEProductInvalid
	}
	byPrincipal := make(map[string]string, len(wanted))
	for _, email := range accountStore().accountEmails() {
		email = normalizeAccountEmail(email)
		if principal := strideRuntimePrincipalForEmail(email); principal != "" {
			byPrincipal[principal] = email
		}
	}
	emails := make([]string, 0, len(wanted))
	for _, principal := range wanted {
		email := byPrincipal[principal]
		if email == "" {
			return nil, ErrSTRIDEProductDenied
		}
		emails = append(emails, email)
	}
	sort.Strings(emails)
	return emails, nil
}

func strideProductArtifact(w http.ResponseWriter, r *http.Request, user *userAccount, runtime *STRIDERuntime, runID string) {
	principal := strideRuntimePrincipalForEmail(user.Email)
	kanbanApp.strideProductMu.Lock()
	defer kanbanApp.strideProductMu.Unlock()

	var pre STRIDEProductWorkRecord
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		ctx.Product.mu.RLock()
		defer ctx.Product.mu.RUnlock()
		for _, candidate := range ctx.Product.work {
			if candidate.RunID == runID {
				pre = cloneSTRIDEProductWork(candidate)
				break
			}
		}
		if pre.ID == "" {
			return ErrSTRIDEProductUnknown
		}
		if !strideWorkContainsString(pre.RecipientIDs, principal) {
			return ErrSTRIDEProductDenied
		}
		return nil
	})
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}

	// A durable artifact URL is not durable authorization. Linearize reads with
	// source projection and destination membership changes, then require the
	// exact destination audience/ACL revision that was approved for the work.
	unlockThreads := kanbanApp.lockScoutChatThreadSet(pre.SourceThreadID, pre.DestinationThreadID)
	defer unlockThreads()
	destination, _, destinationErr := kanbanApp.scoutChatThreadByID(user.Email, pre.DestinationThreadID)
	destinationAudience, destinationACLVersion, destinationAuthorityErr := strideProductProjectDestinationAuthority(destination)
	if destinationErr != nil || destinationAuthorityErr != nil || pre.DestinationAudience == nil ||
		!sameAudience(*pre.DestinationAudience, destinationAudience) || pre.DestinationACLVersion != destinationACLVersion {
		writeSTRIDEProductError(w, ErrSTRIDEProductDenied)
		return
	}

	var record STRIDEProductWorkRecord
	var insights STRIDEProductInsightsState
	insightsFound := false
	sourceCurrent := false
	changed := false
	err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		var readErr error
		record, sourceCurrent, changed, readErr = ctx.reauthorizeWorkForRead(principal, pre.ID, ctx.Receipt.IssuedAt)
		if readErr != nil || !sourceCurrent {
			return readErr
		}
		if record.RunID != pre.RunID || record.DestinationAudience == nil || record.DestinationThreadID != destination.ID || record.DestinationTitle != destination.Title ||
			!sameAudience(*record.DestinationAudience, destinationAudience) || record.DestinationACLVersion != destinationACLVersion {
			return ErrSTRIDEProductDenied
		}
		if destinationErr := strideProductDestinationAllowed(ctx, principal, record, destinationAudience); destinationErr != nil {
			return destinationErr
		}
		if readErr == nil && sourceCurrent {
			insights, insightsFound = ctx.Product.insightsState(record.ID)
		}
		return nil
	})
	if err == nil && changed {
		err = runtime.Save()
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	if !sourceCurrent {
		writeSTRIDEProductError(w, ErrSTRIDEWorkSourceChanged)
		return
	}
	artifact := map[string]any{"id": record.ArtifactID, "title": record.Title, "summary": record.CompletionSummary, "approvedOutcome": record.Outcome, "sourceSnippet": record.SourceSnippet, "sourceHref": record.BrainHref, "destinationThreadId": record.DestinationThreadID, "providerExecutionFenced": true, "reportAvailable": false}
	if insightsFound {
		workflow, _, _, restoreErr := restoreSTRIDEProductInsightsState(record, insights)
		if restoreErr != nil {
			writeSTRIDEProductError(w, restoreErr)
			return
		}
		selectedDigest := strings.TrimSpace(r.URL.Query().Get("reportDigest"))
		revision, revisions, feedback, selectionErr := strideProductInsightsArtifactView(workflow, insights, selectedDigest)
		if selectionErr != nil {
			writeSTRIDEProductError(w, selectionErr)
			return
		}
		artifact["reportAvailable"] = true
		artifact["report"] = revision.Report
		artifact["reportArtifact"] = revision.Artifact
		artifact["reportCurrent"] = revision.Current
		artifact["revisions"] = revisions
		artifact["feedback"] = feedback
		artifact["feedbackRevision"] = insights.Revision
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "artifact": artifact})
}

type strideProductInsightsArtifactRevision struct {
	RunID              string                 `json:"runId"`
	RequestRevision    int                    `json:"requestRevision"`
	ParentRunID        string                 `json:"parentRunId,omitempty"`
	ParentReportDigest string                 `json:"parentReportDigest,omitempty"`
	Report             StrideInsightsReport   `json:"report"`
	Artifact           StrideInsightsArtifact `json:"artifact"`
	Current            bool                   `json:"current"`
}

func strideProductInsightsArtifactView(workflow *StrideInsightsWorkflow, state STRIDEProductInsightsState, selectedDigest string) (strideProductInsightsArtifactRevision, []strideProductInsightsArtifactRevision, []StrideInsightsFeedback, error) {
	if workflow == nil {
		return strideProductInsightsArtifactRevision{}, nil, nil, ErrSTRIDEProductInvalid
	}
	revisions := make([]strideProductInsightsArtifactRevision, 0, len(workflow.runs))
	feedback := []StrideInsightsFeedback{}
	for _, run := range workflow.runs {
		if run.Status != StrideInsightsStatusAccepted || len(run.Reports) == 0 || run.Artifact == nil {
			continue
		}
		report := run.Reports[len(run.Reports)-1]
		revisions = append(revisions, strideProductInsightsArtifactRevision{
			RunID: run.RunID, RequestRevision: run.Request.RequestRevision, ParentRunID: run.Request.ParentRunID, ParentReportDigest: run.Request.ParentReportDigest,
			Report: report, Artifact: *run.Artifact, Current: run.RunID == state.CurrentRunID && report.ReportDigest == state.CurrentReportDigest,
		})
		feedback = append(feedback, run.Feedback...)
	}
	sort.Slice(revisions, func(i, j int) bool {
		if revisions[i].RequestRevision != revisions[j].RequestRevision {
			return revisions[i].RequestRevision < revisions[j].RequestRevision
		}
		return revisions[i].RunID < revisions[j].RunID
	})
	sort.Slice(feedback, func(i, j int) bool {
		if !feedback[i].At.Equal(feedback[j].At) {
			return feedback[i].At.Before(feedback[j].At)
		}
		return feedback[i].FeedbackID < feedback[j].FeedbackID
	})
	if selectedDigest == "" {
		selectedDigest = state.CurrentReportDigest
	}
	if !isHexDigest(selectedDigest) {
		return strideProductInsightsArtifactRevision{}, nil, nil, ErrSTRIDEProductInvalid
	}
	for _, revision := range revisions {
		if revision.Report.ReportDigest == selectedDigest {
			return revision, revisions, feedback, nil
		}
	}
	return strideProductInsightsArtifactRevision{}, nil, nil, ErrSTRIDEProductUnknown
}

type strideProductFeedbackRequest struct {
	FeedbackRevision int64  `json:"feedbackRevision"`
	ReportDigest     string `json:"reportDigest"`
	Action           string `json:"action"`
	Correction       string `json:"correction,omitempty"`
	IdempotencyKey   string `json:"idempotencyKey"`
}

func strideProductFeedback(w http.ResponseWriter, r *http.Request, user *userAccount, runtime *STRIDERuntime, runID string) {
	var payload strideProductFeedbackRequest
	if decodeSTRIDEProductBody(w, r, &payload) != nil {
		writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
		return
	}
	payload.ReportDigest = strings.TrimSpace(payload.ReportDigest)
	payload.Action = strings.TrimSpace(payload.Action)
	payload.Correction = trimForStorage(strings.TrimSpace(payload.Correction), 1200)
	payload.IdempotencyKey = strings.TrimSpace(payload.IdempotencyKey)
	if payload.FeedbackRevision < 1 || !isHexDigest(payload.ReportDigest) || len(payload.IdempotencyKey) < 16 || !strideIdentifier(payload.IdempotencyKey) ||
		!oneOf(payload.Action, insightsFeedbackAccept, insightsFeedbackReject, insightsFeedbackCorrect, insightsFeedbackRequestRevision) ||
		(payload.Action == insightsFeedbackCorrect && payload.Correction == "") || (payload.Action != insightsFeedbackCorrect && payload.Action != insightsFeedbackRequestRevision && payload.Correction != "") {
		writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
		return
	}
	principal := strideRuntimePrincipalForEmail(user.Email)
	kanbanApp.strideProductMu.Lock()
	defer kanbanApp.strideProductMu.Unlock()

	var pre STRIDEProductWorkRecord
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		ctx.Product.mu.RLock()
		defer ctx.Product.mu.RUnlock()
		for _, candidate := range ctx.Product.work {
			if candidate.RunID == strings.TrimSpace(runID) {
				pre = cloneSTRIDEProductWork(candidate)
				break
			}
		}
		if pre.ID == "" {
			return ErrSTRIDEProductUnknown
		}
		if !strideWorkContainsString(pre.RecipientIDs, principal) {
			return ErrSTRIDEProductDenied
		}
		return nil
	})
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}

	unlockThreads := kanbanApp.lockScoutChatThreadSet(pre.SourceThreadID, pre.DestinationThreadID)
	defer unlockThreads()
	destination, _, destinationErr := kanbanApp.scoutChatThreadByID(user.Email, pre.DestinationThreadID)
	destinationAudience, destinationACLVersion, destinationAuthorityErr := strideProductProjectDestinationAuthority(destination)
	if destinationErr != nil || destinationAuthorityErr != nil || pre.DestinationAudience == nil || !sameAudience(*pre.DestinationAudience, destinationAudience) || pre.DestinationACLVersion != destinationACLVersion {
		writeSTRIDEProductError(w, ErrSTRIDEProductDenied)
		return
	}

	var responseState STRIDEProductInsightsState
	var responseFeedback StrideInsightsFeedback
	var responseWorkflow *StrideInsightsWorkflow
	replayed := false
	sourceCurrent := false
	sourceChanged := false
	err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		var record STRIDEProductWorkRecord
		var readErr error
		var changed bool
		record, sourceCurrent, changed, readErr = ctx.reauthorizeWorkForRead(principal, pre.ID, ctx.Receipt.IssuedAt)
		sourceChanged = sourceChanged || changed
		if readErr != nil {
			return readErr
		}
		if !sourceCurrent {
			return nil
		}
		if record.RunID != pre.RunID || record.DestinationAudience == nil || record.DestinationThreadID != destination.ID || record.DestinationTitle != destination.Title ||
			!sameAudience(*record.DestinationAudience, destinationAudience) || record.DestinationACLVersion != destinationACLVersion {
			return ErrSTRIDEProductDenied
		}
		if err := strideProductDestinationAllowed(ctx, principal, record, destinationAudience); err != nil {
			return err
		}
		state, found := ctx.Product.insightsState(record.ID)
		if !found {
			return ErrSTRIDEProductConflict
		}
		workflow, current, currentReport, restoreErr := restoreSTRIDEProductInsightsState(record, state)
		if restoreErr != nil {
			return restoreErr
		}
		feedbackID := "insights-feedback-" + temporalDigest(state.TenantID + "\x00" + record.ID + "\x00" + principal + "\x00" + payload.IdempotencyKey)[:24]
		if existing, found := strideProductInsightsFeedbackByID(workflow, feedbackID); found {
			if !strideProductFeedbackMatches(existing, payload, principal) || !strideProductStoredFeedbackMatches(ctx.WorkStore, existing.RunID, existing) {
				return ErrSTRIDEProductConflict
			}
			responseState, responseFeedback, responseWorkflow, replayed = state, existing, workflow, true
			return nil
		}
		if state.Revision != payload.FeedbackRevision || state.CurrentReportDigest != payload.ReportDigest || currentReport.ReportDigest != payload.ReportDigest {
			return ErrSTRIDEProductConflict
		}

		parentRun := current
		feedback := StrideInsightsFeedback{Schema: StrideInsightsFeedbackSchema, FeedbackID: feedbackID, RunID: current.RunID, ReportDigest: currentReport.ReportDigest,
			Action: payload.Action, Correction: payload.Correction, ActorID: principal, Binding: current.Request.Binding, At: ctx.Receipt.IssuedAt.UTC()}
		if payload.Action == insightsFeedbackRequestRevision {
			feedback.NewRequestRevision = current.Request.RequestRevision + 1
			feedback.NewRunID = "insights-revision-" + temporalDigest(record.ID + "\x00" + feedbackID)[:24]
		}
		copyFeedback := feedback
		copyFeedback.FeedbackDigest = ""
		feedbackRaw, digestErr := canonicalJSON(copyFeedback)
		if digestErr != nil {
			return ErrSTRIDEProductInvalid
		}
		feedback.FeedbackDigest = temporalDigestBytes(feedbackRaw)
		updatedParent, submitErr := workflow.SubmitFeedback(ACLPrincipal{TenantID: state.TenantID, Kind: ACLPrincipalUser, ID: principal}, current.RunID, feedback)
		if submitErr != nil {
			return ErrSTRIDEProductDenied
		}
		if payload.Action == insightsFeedbackRequestRevision {
			successor := current.Request
			successor.RunID = feedback.NewRunID
			successor.RequestID = "request-" + feedback.NewRunID
			successor.RequestRevision = feedback.NewRequestRevision
			successor.ParentRunID = current.RunID
			successor.ParentReportDigest = currentReport.ReportDigest
			successor.RequestDigest = ""
			successor.RequestDigest, submitErr = strideInsightsRequestDigest(successor)
			if submitErr != nil {
				return ErrSTRIDEProductInvalid
			}
			note := strideProductRevisionFeedbackNote(updatedParent)
			current, submitErr = workflow.Launch(ACLPrincipal{TenantID: state.TenantID, Kind: ACLPrincipalUser, ID: successor.PrincipalID}, successor, strideProductInsightsStages{source: record, revisionNote: note})
			if submitErr != nil || current.Status != StrideInsightsStatusAccepted || current.Artifact == nil || len(current.Reports) == 0 {
				return ErrSTRIDEProductInvalid
			}
			currentReport = current.Reports[len(current.Reports)-1]
		}
		workflowPayload, snapshotErr := workflow.Snapshot()
		if snapshotErr != nil {
			return ErrSTRIDEProductInvalid
		}
		nextState := STRIDEProductInsightsState{TenantID: state.TenantID, WorkID: record.ID, Revision: state.Revision + 1, WorkflowPayload: workflowPayload,
			WorkflowDigest: temporalDigestBytes(workflowPayload), CurrentRunID: current.RunID, CurrentReportDigest: currentReport.ReportDigest, UpdatedAt: ctx.Receipt.IssuedAt.UTC()}
		if validateSTRIDEProductInsightsState(record, nextState) != nil {
			return ErrSTRIDEProductInvalid
		}
		service := STRIDEWorkOrchestrator{Enabled: true, TenantID: state.TenantID, Store: ctx.WorkStore, Activation: strideProductActivationAuthority{ctx.Config, ctx.Receipt, ctx.Receipt.IssuedAt}, Now: func() time.Time { return ctx.Receipt.IssuedAt.UTC() }}
		workFeedback := STRIDEWorkFeedback{ID: feedback.FeedbackID, RunID: feedback.RunID, Kind: strideProductWorkFeedbackKind(feedback.Action), Author: feedback.ActorID,
			BodyDigest: feedback.FeedbackDigest, CreatedAt: feedback.At, Rerun: feedback.Action == insightsFeedbackRequestRevision}
		return ctx.withWorkAuthority(principal, "insights_feedback", record, func() error {
			ctx.Product.mu.RLock()
			storedWork, workFound := ctx.Product.work[record.ID]
			storedState, stateFound := ctx.Product.insights[record.ID]
			productCurrent := workFound && stateFound && storedWork.Revision == record.Revision && storedWork.Status == "completed" && storedState.Revision == state.Revision && storedState.CurrentReportDigest == state.CurrentReportDigest
			ctx.Product.mu.RUnlock()
			if !productCurrent {
				return ErrSTRIDEProductConflict
			}
			if feedback.Action == insightsFeedbackRequestRevision {
				if rerunErr := strideProductRecordCompletedInsightsRerun(ctx.WorkStore, record, workFeedback, parentRun, current); rerunErr != nil {
					return rerunErr
				}
			} else if _, addErr := service.AddFeedback(workFeedback); addErr != nil {
				return addErr
			}
			ctx.Product.mu.Lock()
			ctx.Product.insights[record.ID] = cloneSTRIDEProductInsightsState(nextState)
			ctx.Product.mu.Unlock()
			if persistErr := ctx.persistLifecycleCheckpoint("insights_feedback_saved"); persistErr != nil {
				return persistErr
			}
			responseState, responseFeedback, responseWorkflow = nextState, feedback, workflow
			return nil
		})
	})
	if err == nil && sourceChanged {
		err = runtime.Save()
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	if !sourceCurrent {
		writeSTRIDEProductError(w, ErrSTRIDEWorkSourceChanged)
		return
	}
	revision, revisions, feedback, viewErr := strideProductInsightsArtifactView(responseWorkflow, responseState, "")
	if viewErr != nil {
		writeSTRIDEProductError(w, viewErr)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "replayed": replayed, "feedback": responseFeedback, "feedbackRevision": responseState.Revision,
		"report": revision.Report, "reportArtifact": revision.Artifact, "revisions": revisions, "feedbackLineage": feedback, "providerCalls": 0, "inputTokens": 0, "outputTokens": 0})
}

func strideProductInsightsFeedbackByID(workflow *StrideInsightsWorkflow, id string) (StrideInsightsFeedback, bool) {
	if workflow == nil {
		return StrideInsightsFeedback{}, false
	}
	for _, run := range workflow.runs {
		for _, feedback := range run.Feedback {
			if feedback.FeedbackID == id {
				return feedback, true
			}
		}
	}
	return StrideInsightsFeedback{}, false
}

func strideProductFeedbackMatches(feedback StrideInsightsFeedback, payload strideProductFeedbackRequest, principal string) bool {
	return feedback.ReportDigest == payload.ReportDigest && feedback.Action == payload.Action && feedback.Correction == payload.Correction && feedback.ActorID == principal
}

func strideProductStoredFeedbackMatches(store *STRIDEWorkOrchestrationStore, runID string, feedback StrideInsightsFeedback) bool {
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record, found := store.Feedback[feedback.FeedbackID]
	return found && record.RunID == runID && record.Kind == strideProductWorkFeedbackKind(feedback.Action) && record.Author == feedback.ActorID && record.BodyDigest == feedback.FeedbackDigest && record.CreatedAt.Equal(feedback.At) && record.Rerun == (feedback.Action == insightsFeedbackRequestRevision)
}

// strideProductRecordCompletedInsightsRerun atomically mirrors a requested
// report revision into the canonical work ledger. The human feedback is the
// child run's authority edge; no revision may exist only inside the private
// workflow snapshot where queue, artifact, outcome, and audit readers cannot
// account for it.
func strideProductRecordCompletedInsightsRerun(store *STRIDEWorkOrchestrationStore, record STRIDEProductWorkRecord, feedback STRIDEWorkFeedback, parentInsightsRun, childInsightsRun StrideInsightsRun) error {
	if store == nil || record.DestinationAudience == nil || !feedback.Rerun || feedback.Kind != "rerun_request" ||
		parentInsightsRun.RunID != feedback.RunID || childInsightsRun.RunID == parentInsightsRun.RunID || childInsightsRun.Request.ParentRunID != parentInsightsRun.RunID ||
		childInsightsRun.Status != StrideInsightsStatusAccepted || childInsightsRun.Artifact == nil || childInsightsRun.Outcome == nil ||
		childInsightsRun.Request.RequestRevision != parentInsightsRun.Request.RequestRevision+1 {
		return ErrSTRIDEWorkState
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	parent, found := store.Runs[feedback.RunID]
	if !found || parent.Status != STRIDERunCompleted || parent.CardID != record.ID || parent.TenantID != childInsightsRun.Request.TenantID ||
		parent.CurrentStage == "" || parent.RouteSnapshots[parent.CurrentStage].StageID == "" || feedback.Author == "" || !strideWorkContainsString(record.RecipientIDs, feedback.Author) {
		return ErrSTRIDEWorkState
	}
	if !validRestoredFeedback(feedback) || childInsightsRun.Request.ParentReportDigest == "" || childInsightsRun.Artifact.RunID != childInsightsRun.RunID || childInsightsRun.Outcome.RunID != childInsightsRun.RunID {
		return ErrSTRIDEWorkState
	}

	createdAt := feedback.CreatedAt.UTC()
	routes := make(map[string]STRIDEWorkRouteSnapshot, len(parent.RouteSnapshots))
	for id, route := range parent.RouteSnapshots {
		routes[id] = route
	}
	checkpoint := STRIDEWorkCheckpoint{ID: "checkpoint_" + childInsightsRun.RunID, StageID: parent.CurrentStage, Status: "passed", EvidenceDigest: childInsightsRun.Outcome.OutcomeDigest,
		CreatedAt: createdAt, VerifierReceiptDigest: temporalDigest("insights-rerun-checkpoint\x00" + childInsightsRun.RunID + "\x00" + childInsightsRun.Outcome.OutcomeDigest)}
	child := STRIDEDurableWorkRun{
		ID: childInsightsRun.RunID, TenantID: parent.TenantID,
		IdempotencyDigest: temporalDigest("stride-insights-rerun/v1\x00" + parent.ID + "\x00" + feedback.ID + "\x00" + childInsightsRun.Request.RequestDigest + "\x00" + childInsightsRun.Artifact.ArtifactDigest),
		CardID:            parent.CardID, CardRevision: parent.CardRevision, Evidence: append([]STRIDEReference(nil), parent.Evidence...), Destination: parent.Destination,
		Owner: parent.Owner, Reviewer: parent.Reviewer, Authority: parent.Authority, Budget: parent.Budget, Status: STRIDERunCompleted, CurrentStage: parent.CurrentStage,
		Attempts: 1, RouteSnapshots: routes, Checkpoints: []STRIDEWorkCheckpoint{checkpoint}, CreatedAt: createdAt, UpdatedAt: createdAt, CompletedAt: createdAt,
		CompletionReceiptDigest: temporalDigest("insights-rerun-complete\x00" + childInsightsRun.RunID + "\x00" + childInsightsRun.Outcome.OutcomeDigest),
		ParentRunID:             parent.ID, ParentFeedbackID: feedback.ID,
	}
	artifactID := "artifact_" + child.ID
	artifact := STRIDEWorkArtifactBinding{ID: artifactID, RunID: child.ID, StageID: child.CurrentStage,
		Artifact: STRIDEReference{ContractType: STRIDEContractOutcome, ID: childInsightsRun.Artifact.ArtifactID, Revision: int64(childInsightsRun.Request.RequestRevision), Digest: childInsightsRun.Artifact.ArtifactDigest},
		Evidence: append([]STRIDEReference(nil), child.Evidence...), Destination: child.Destination, Audience: cloneAudience(child.Destination.Audience), CreatedAt: createdAt}
	outcome := STRIDEWorkOutcomeBinding{ID: "outcome_" + child.ID, RunID: child.ID, Verdict: "accepted", ArtifactIDs: []string{artifact.ID},
		Evidence: append([]STRIDEReference(nil), child.Evidence...), Destination: child.Destination, Audience: cloneAudience(child.Destination.Audience), Reviewer: child.Reviewer, CompletedAt: createdAt}
	if !validRestoredRun(child) || artifact.Artifact.Validate() != nil || artifact.Audience.Validate() != nil || artifact.StageID != child.CurrentStage ||
		!sameReferenceSet(artifact.Evidence, child.Evidence) || !sameThreadResolution(artifact.Destination, child.Destination) || !sameAudience(artifact.Audience, child.Destination.Audience) ||
		!sameReferenceSet(outcome.Evidence, child.Evidence) || !sameThreadResolution(outcome.Destination, child.Destination) || !sameAudience(outcome.Audience, child.Destination.Audience) || outcome.Reviewer != child.Reviewer {
		return ErrSTRIDEWorkState
	}

	existingFeedback, feedbackExists := store.Feedback[feedback.ID]
	existingRun, runExists := store.Runs[child.ID]
	existingArtifact, artifactExists := store.Artifacts[artifact.ID]
	existingOutcome, outcomeExists := store.Outcomes[outcome.ID]
	if feedbackExists || runExists || artifactExists || outcomeExists {
		if feedbackExists && runExists && artifactExists && outcomeExists && existingFeedback == feedback && workDigest(existingRun) == workDigest(child) &&
			workDigest(existingArtifact) == workDigest(artifact) && workDigest(existingOutcome) == workDigest(outcome) {
			return nil
		}
		return ErrSTRIDEWorkState
	}
	store.Feedback[feedback.ID] = feedback
	store.Runs[child.ID] = child
	store.Artifacts[artifact.ID] = artifact
	store.Outcomes[outcome.ID] = outcome
	return nil
}

func strideProductWorkFeedbackKind(action string) string {
	switch action {
	case insightsFeedbackCorrect:
		return "correction"
	case insightsFeedbackRequestRevision:
		return "rerun_request"
	default:
		return "quality"
	}
}

func strideProductRevisionFeedbackNote(run StrideInsightsRun) string {
	notes := []string{}
	for _, feedback := range run.Feedback {
		if note := strings.TrimSpace(feedback.Correction); note != "" {
			notes = append(notes, note)
		}
	}
	if len(notes) == 0 {
		return "A reviewer requested a fresh revision of the approved report."
	}
	return strings.Join(notes, " | ")
}

func strideProductMarketplaceHandle(w http.ResponseWriter, r *http.Request) {
	user, runtime, ok := strideProductAuthenticated(w, r, http.MethodGet, http.MethodPost)
	if !ok {
		return
	}
	parts := splitSTRIDEProductPath(r.URL.Path, strideRuntimeAPIBase+"marketplace/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "templates" && r.Method == http.MethodPost {
		strideProductCreatePrivateTemplate(w, r, user, runtime)
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		var candidate STRIDEProductMarketplaceCandidate
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			for _, visible := range ctx.Product.candidateCatalogForViewer(isArtifactApprovalAdmin(user)) {
				if visible.ID == id {
					candidate = visible
					return nil
				}
			}
			return ErrSTRIDEProductUnknown
		})
		if err != nil {
			writeSTRIDEProductError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "listing": candidate, "liveAdmissionFenced": candidate.ProviderExecutionFenced})
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if (parts[1] == "trial" || parts[1] == "hire") && !strideLegacyRosterMutationEnabled() {
		writeAuthJSON(w, http.StatusGone, map[string]any{
			"ok":      false,
			"error":   "Agent trials and hiring are retired. Scout, Researcher, and Presenter are included in governed Work and conversations.",
			"retired": true,
		})
		return
	}
	if !isArtifactApprovalAdmin(user) {
		writeSTRIDEProductError(w, ErrSTRIDEAdminRequired)
		return
	}
	kanbanApp.strideProductMu.Lock()
	defer kanbanApp.strideProductMu.Unlock()
	now := time.Now().UTC()
	principal := strideRuntimePrincipalForEmail(user.Email)
	switch parts[1] {
	case "trial":
		if r.Body != nil && r.ContentLength != 0 {
			var body struct{}
			if decodeSTRIDEProductBody(w, r, &body) != nil {
				writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
				return
			}
		}
		var agent STRIDEProductTeamAgent
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			var e error
			agent, e = ctx.Product.beginTrial(id, principal, now)
			return e
		})
		strideProductWriteAgentMutation(w, runtime, agent, err)
	case "hire":
		var body struct {
			Revision int64 `json:"revision"`
		}
		if decodeSTRIDEProductBody(w, r, &body) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		agentID := candidateAgentID(id)
		var prior STRIDEProductTeamAgent
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			var found bool
			prior, found = ctx.Product.agentRecord(agentID)
			if !found {
				return ErrSTRIDEProductUnknown
			}
			// An exact transport retry of the successful trial->hire transition
			// replays the resulting seat. It must not create a second direct
			// thread or duplicate Scout's introduction.
			if prior.Status == "hired_fenced" && prior.Revision == body.Revision+1 && strideIdentifier(prior.DirectThreadID) {
				return nil
			}
			if prior.Revision != body.Revision || prior.Status != "trial" {
				return ErrSTRIDEProductConflict
			}
			return nil
		})
		if err != nil {
			writeSTRIDEProductError(w, err)
			return
		}
		if prior.Status == "hired_fenced" {
			if err := runtime.Save(); err != nil {
				writeSTRIDEProductError(w, err)
				return
			}
			writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "seat": prior, "replayed": true, "scoutIntroductionPosted": strideProductPostScoutIntroduction(user, prior), "providerSessionStarted": false})
			return
		}
		directThreadID := strideProductAgentDirectThreadPrefix + temporalDigest(agentID + "\x00" + normalizeAccountEmail(user.Email))[:20]
		thread, _, err := kanbanApp.ensureScoutChatThread(directThreadID, user.Email, user.Name, prior.DisplayName+" · agent", scoutChatVisibilityPrivate, nil)
		if err != nil {
			writeSTRIDEProductError(w, err)
			return
		}
		var agent STRIDEProductTeamAgent
		err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			var e error
			agent, e = ctx.Product.mutateAgent(agentID, body.Revision, func(value *STRIDEProductTeamAgent) error {
				value.Status = "hired_fenced"
				value.DirectThreadID = thread.ID
				value.AccessRevoked = false
				value.Lifecycle = append(value.Lifecycle, "human_approved_hire", "direct_thread_created", "provider_runtime_remains_fenced")
				return nil
			}, now)
			if e != nil {
				return e
			}
			_, e = ctx.Workforce.installFencedInternalPreviewSeat(STRIDEWorkforceActor{ID: principal, IsAdmin: true}, ctx.Receipt, agent, now)
			if e != nil {
				ctx.Product.restoreAgentRecord(prior)
			}
			return e
		})
		if err == nil {
			err = runtime.Save()
		}
		if err != nil {
			writeSTRIDEProductError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "seat": agent, "scoutIntroductionPosted": strideProductPostScoutIntroduction(user, agent), "providerSessionStarted": false})
	default:
		http.NotFound(w, r)
	}
}

func strideProductRosterHandle(w http.ResponseWriter, r *http.Request) {
	user, runtime, ok := strideProductAuthenticated(w, r, http.MethodGet, http.MethodPost)
	if !ok {
		return
	}
	parts := splitSTRIDEProductPath(r.URL.Path, strideRuntimeAPIBase+"roster/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if r.Method == http.MethodGet {
		var agent STRIDEProductTeamAgent
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			var found bool
			agent, found = ctx.Product.agentRecord(id)
			if !found {
				return ErrSTRIDEProductUnknown
			}
			return nil
		})
		if err != nil {
			writeSTRIDEProductError(w, err)
			return
		}
		if len(parts) == 1 {
			writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "seat": agent})
			return
		}
		if len(parts) == 2 && parts[1] == "export" {
			if !isArtifactApprovalAdmin(user) {
				writeSTRIDEProductError(w, ErrSTRIDEAdminRequired)
				return
			}
			exported, exportErr := safeSTRIDEProductAgentExport(agent)
			if exportErr != nil {
				writeSTRIDEProductError(w, exportErr)
				return
			}
			writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "export": exported, "providerRuntimeExported": false})
			return
		}
		http.NotFound(w, r)
		return
	}
	if !isArtifactApprovalAdmin(user) {
		writeSTRIDEProductError(w, ErrSTRIDEAdminRequired)
		return
	}
	kanbanApp.strideProductMu.Lock()
	defer kanbanApp.strideProductMu.Unlock()
	now := time.Now().UTC()
	if len(parts) == 2 {
		strideProductRosterAction(w, r, runtime, id, parts[1], now)
		return
	}
	if len(parts) == 4 && parts[1] == "updates" && (parts[3] == "approve" || parts[3] == "rollback") {
		var body struct {
			Revision int64 `json:"revision"`
		}
		if decodeSTRIDEProductBody(w, r, &body) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		var agent STRIDEProductTeamAgent
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			var e error
			agent, e = ctx.Product.resolveAgentUpdate(id, body.Revision, parts[2], parts[3], now)
			return e
		})
		strideProductWriteAgentMutation(w, runtime, agent, err)
		return
	}
	if len(parts) == 4 && parts[1] == "learning" && (parts[3] == "approve" || parts[3] == "correct" || parts[3] == "forget") {
		var body struct {
			Revision int64  `json:"revision"`
			Summary  string `json:"summary"`
		}
		if decodeSTRIDEProductBody(w, r, &body) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		var agent STRIDEProductTeamAgent
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			current, found := ctx.Product.agentRecord(id)
			if !found {
				return ErrSTRIDEProductUnknown
			}
			return kanbanApp.authorizeCompletedWorkLearningResolution(current, parts[2], parts[3], strideRuntimePrincipalForEmail(user.Email), now, func() error {
				var mutationErr error
				agent, mutationErr = ctx.Product.resolveAgentLearning(id, body.Revision, parts[2], parts[3], body.Summary, now)
				if mutationErr != nil {
					return mutationErr
				}
				if persistErr := ctx.Persist(); persistErr != nil {
					ctx.Product.restoreAgentSnapshot(current)
					return persistErr
				}
				return nil
			})
		})
		if err != nil {
			writeSTRIDEProductError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "seat": agent, "providerSessionStarted": false})
		return
	}
	http.NotFound(w, r)
}

func strideProductCreatePrivateTemplate(w http.ResponseWriter, r *http.Request, user *userAccount, runtime *STRIDERuntime) {
	if !isArtifactApprovalAdmin(user) {
		writeSTRIDEProductError(w, ErrSTRIDEAdminRequired)
		return
	}
	var payload STRIDEProductPrivateTemplateRequest
	if decodeSTRIDEProductBody(w, r, &payload) != nil {
		writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
		return
	}
	kanbanApp.strideProductMu.Lock()
	defer kanbanApp.strideProductMu.Unlock()
	principal := strideRuntimePrincipalForEmail(user.Email)
	var candidate STRIDEProductMarketplaceCandidate
	created := false
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		var createErr error
		candidate, created, createErr = ctx.Product.createPrivateTemplateCandidate(payload, principal, ctx.Receipt.IssuedAt)
		return createErr
	})
	if err == nil && created {
		err = runtime.Save()
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeAuthJSON(w, status, map[string]any{"ok": true, "listing": candidate, "created": created, "organizationPrivate": true, "liveAdmissionFenced": true, "providerCalls": 0})
}

func strideProductRosterAction(w http.ResponseWriter, r *http.Request, runtime *STRIDERuntime, id, action string, now time.Time) {
	var agent STRIDEProductTeamAgent
	switch action {
	case "configure":
		// Direct configuration mutation predates the revisioned semantic-diff
		// workflow. Keep the old route fail-closed so an outdated client cannot
		// silently change personality, access, proactivity, or budget. Clients
		// must POST /updates and obtain a separate human approval instead.
		writeSTRIDEProductError(w, ErrSTRIDEProductDenied)
	case "assign":
		var body struct {
			Revision         int64  `json:"revision"`
			ProjectOrChannel string `json:"projectOrChannel"`
			Role             string `json:"role"`
			Responsibility   string `json:"responsibility"`
			Destination      string `json:"destination"`
		}
		if decodeSTRIDEProductBody(w, r, &body) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			expectedID := "assignment_" + temporalDigest(id + "\x00" + fmt.Sprint(body.Revision))[:20]
			if replay, ok := ctx.Product.exactAgentReplay(id, body.Revision, func(value STRIDEProductTeamAgent) bool {
				for _, assignment := range value.Assignments {
					if assignment.ID == expectedID && assignment.ProjectOrChannel == body.ProjectOrChannel && assignment.Role == body.Role && assignment.Responsibility == trimForStorage(body.Responsibility, 500) && assignment.Destination == body.Destination {
						return true
					}
				}
				return false
			}); ok {
				agent = replay
				return nil
			}
			var e error
			agent, e = ctx.Product.mutateAgent(id, body.Revision, func(value *STRIDEProductTeamAgent) error {
				if value.Status != "hired_fenced" || !strideIdentifier(body.ProjectOrChannel) || !strideIdentifier(body.Role) || !strideIdentifier(body.Destination) || strings.TrimSpace(body.Responsibility) == "" {
					return ErrSTRIDEProductDenied
				}
				assignment := STRIDEProductAgentAssignment{ID: "assignment_" + temporalDigest(id + "\x00" + fmt.Sprint(body.Revision))[:20], ProjectOrChannel: body.ProjectOrChannel, Role: body.Role, Responsibility: trimForStorage(body.Responsibility, 500), Destination: body.Destination, Status: "active_fenced", CreatedAt: now}
				value.Assignments = append(value.Assignments, assignment)
				value.Lifecycle = append(value.Lifecycle, "assignment_recorded_execution_fenced")
				return nil
			}, now)
			return e
		})
		strideProductWriteAgentMutation(w, runtime, agent, err)
	case "updates":
		var body struct {
			Revision  int64                    `json:"revision"`
			Summary   string                   `json:"summary"`
			Candidate STRIDEProductAgentConfig `json:"candidate"`
		}
		if decodeSTRIDEProductBody(w, r, &body) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			var e error
			agent, e = ctx.Product.proposeAgentUpdate(id, body.Revision, body.Summary, body.Candidate, now)
			return e
		})
		strideProductWriteAgentMutation(w, runtime, agent, err)
	case "learning":
		var body struct {
			Revision int64  `json:"revision"`
			Subject  string `json:"subject"`
			Scope    string `json:"scope"`
			Summary  string `json:"summary"`
		}
		if decodeSTRIDEProductBody(w, r, &body) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			var e error
			agent, e = ctx.Product.recordAgentLearning(id, body.Revision, body.Subject, body.Scope, body.Summary, now)
			return e
		})
		strideProductWriteAgentMutation(w, runtime, agent, err)
	case "pause", "offboard":
		var body struct {
			Revision int64 `json:"revision"`
		}
		if decodeSTRIDEProductBody(w, r, &body) != nil {
			writeSTRIDEProductError(w, ErrSTRIDEProductInvalid)
			return
		}
		replayed := false
		err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
			prior, found := ctx.Product.agentRecord(id)
			if !found {
				return ErrSTRIDEProductUnknown
			}
			terminal := map[string]string{"pause": "paused", "offboard": "offboarded"}[action]
			if prior.Status == terminal && prior.Revision == body.Revision+1 {
				agent = prior
				replayed = true
				return nil
			}
			var e error
			agent, e = ctx.Product.mutateAgent(id, body.Revision, func(value *STRIDEProductTeamAgent) error {
				if action == "pause" {
					if value.Status == "offboarded" {
						return ErrSTRIDEProductDenied
					}
					value.Status = "paused"
					value.AccessRevoked = true
					value.Lifecycle = append(value.Lifecycle, "paused_and_access_revoked")
				} else {
					value.Status = "offboarded"
					value.AccessRevoked = true
					value.Lifecycle = append(value.Lifecycle, "offboarded_and_export_preserved")
				}
				return nil
			}, now)
			if e == nil && action == "pause" {
				_, e = ctx.Workforce.Pause(STRIDEWorkforceActor{ID: strideRuntimePrincipalForEmail(artifactLibraryAdminEmail), IsAdmin: true}, id, "product_pause_"+fmt.Sprint(body.Revision), now)
			} else if e == nil && action == "offboard" {
				_, e = ctx.Workforce.Offboard(STRIDEWorkforceActor{ID: strideRuntimePrincipalForEmail(artifactLibraryAdminEmail), IsAdmin: true}, id, "product_offboard_"+fmt.Sprint(body.Revision), now)
			}
			if e != nil {
				ctx.Product.restoreAgentRecord(prior)
			}
			return e
		})
		if err == nil && replayed {
			if saveErr := runtime.Save(); saveErr != nil {
				writeSTRIDEProductError(w, saveErr)
				return
			}
			writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "seat": agent, "replayed": true, "providerSessionStarted": false})
			return
		}
		strideProductWriteAgentMutation(w, runtime, agent, err)
	default:
		http.NotFound(w, r)
	}
}

func strideProductPostScoutIntroduction(user *userAccount, agent STRIDEProductTeamAgent) bool {
	if user == nil || !strideIdentifier(agent.DirectThreadID) {
		return false
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(user.Email, agent.DirectThreadID)
	if err != nil {
		return false
	}
	intro := scoutChatMessageRecord{
		ID:         "stride-agent-intro-" + temporalDigest(agent.ID + "\x00" + agent.DirectThreadID)[:20],
		Kind:       "message",
		Role:       "scout",
		AuthorName: "Scout",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Text:       fmt.Sprintf("Meet %s, our %s specialist. Their identity, access, and ongoing record now live here. Live model sessions remain off until provider-quality receipts pass E10.", agent.DisplayName, agent.Category),
	}
	return strideProductCommitMessageOnce(thread, intro)
}

// Product handlers hold strideProductMu and use a deterministic message ID.
// The outer helper takes the thread mutex; approval uses the locked variant so
// its authority check and completion append share one indivisible boundary.
func strideProductCommitMessageOnce(thread scoutChatThreadRecord, message scoutChatMessageRecord) bool {
	unlock := kanbanApp.lockScoutChatThreadSet(thread.ID)
	defer unlock()
	current, _, err := kanbanApp.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		return false
	}
	return strideProductCommitMessageOnceLocked(current, message)
}

func strideProductCommitMessageOnceLocked(thread scoutChatThreadRecord, message scoutChatMessageRecord) bool {
	return strideProductCommitMessageOnceLockedWithSideEffects(thread, message, true, true)
}

func strideProductCommitMessageOnceLockedWithoutObservation(thread scoutChatThreadRecord, message scoutChatMessageRecord) bool {
	// This path runs inside WithProductContext while runtime.mu is held. Both
	// conversation observation and websocket projection can re-enter the
	// runtime, so the caller performs both only after the transaction releases.
	return strideProductCommitMessageOnceLockedWithSideEffects(thread, message, false, false)
}

func strideProductCommitMessageOnceLockedWithSideEffects(thread scoutChatThreadRecord, message scoutChatMessageRecord, observe, deliver bool) bool {
	for _, existing := range thread.Messages {
		if existing.ID == message.ID {
			return true
		}
	}
	if thread.ArchivedAt != "" {
		return false
	}
	thread.Messages = append(thread.Messages, message)
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, message)
	if err := kanbanApp.saveScoutChatThread(thread); err != nil {
		return false
	}
	if observe {
		kanbanApp.observeSTRIDETeamChatMessage(thread, message, "message", "")
	}
	if deliver {
		deliverScoutChatThreadUpdate(thread, message)
	}
	return true
}

func strideProductWriteAgentMutation(w http.ResponseWriter, runtime *STRIDERuntime, agent STRIDEProductTeamAgent, err error) {
	if err == nil {
		err = runtime.Save()
	}
	if err != nil {
		writeSTRIDEProductError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "seat": agent, "providerSessionStarted": false})
}
