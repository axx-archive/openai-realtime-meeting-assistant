package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
)

type strideRelationshipPreferenceSource struct {
	Kind        string     `json:"kind"`
	Label       string     `json:"label"`
	Available   bool       `json:"available"`
	ThreadID    string     `json:"threadId,omitempty"`
	ThreadTitle string     `json:"threadTitle,omitempty"`
	MessageID   string     `json:"messageId,omitempty"`
	OccurredAt  *time.Time `json:"occurredAt,omitempty"`
}

type strideRelationshipPreferenceHTTP struct {
	STRIDECollaborationContextPreference
	Source strideRelationshipPreferenceSource `json:"source"`
}

func notifySTRIDERelationshipMemoryChanged(userEmail string, revision int64, at time.Time) {
	sendKanbanEventToUser(userEmail, "relationship_memory_changed", map[string]any{
		"revision":  revision,
		"changedAt": at.UTC(),
	})
}

func inspectSTRIDERelationshipPreferences(store *durableSTRIDECollaborationStore, product STRIDEProductContext, user *userAccount, at time.Time) ([]strideRelationshipPreferenceHTTP, int64, error) {
	principal := strideRuntimePrincipalForEmail(user.Email)
	projections, err := product.Conversation.ProjectForTenantPrincipal(product.Config.TenantID, principal)
	if err != nil {
		return nil, 0, err
	}
	live := make(map[string]STRIDEConversationMessageProjection, len(projections))
	liveReferences := make(map[string]STRIDEReference, len(projections))
	for _, projection := range projections {
		if projection.RecallEligible {
			live[strideConversationReferenceKey(projection.LatestEvent)] = projection
			liveReferences[strideConversationReferenceKey(projection.LatestEvent)] = projection.LatestEvent
		}
	}
	reconciled, reconciledRevision, err := store.ReconcileSourceAuthority(principal, liveReferences, at)
	if err != nil {
		return nil, 0, err
	}
	if reconciled {
		// A source edit, deletion, expiry, or ACL loss changes the instructions
		// already captured by any live personal Realtime session. Fan out the
		// new revision so every signed-in device closes that stale session and
		// reloads the server-authoritative projection.
		notifySTRIDERelationshipMemoryChanged(user.Email, reconciledRevision, at)
	}
	preferences, revision, err := store.Inspect(principal, at)
	if err != nil {
		return nil, revision, err
	}
	result := make([]strideRelationshipPreferenceHTTP, 0, len(preferences))
	for _, preference := range preferences {
		source := strideRelationshipPreferenceSource{Kind: "company_context", Label: "Authorized company context", Available: false}
		for _, reference := range preference.Evidence {
			if strings.HasPrefix(reference.ID, "relationship_control_") {
				source = strideRelationshipPreferenceSource{Kind: "settings", Label: "Added by you in Settings", Available: true}
				break
			}
			projection, ok := live[strideConversationReferenceKey(reference)]
			if !ok || projection.LatestEvent != reference {
				source = strideRelationshipPreferenceSource{Kind: "conversation", Label: "Original conversation is no longer available", Available: false, ThreadID: preference.Relationship.Scope}
				continue
			}
			source = strideRelationshipPreferenceSource{Kind: "conversation", Label: "Conversation you addressed to Scout", Available: true, ThreadID: projection.ThreadID, MessageID: projection.SourceID}
			if thread, _, threadErr := kanbanApp.scoutChatThreadByID(user.Email, projection.ThreadID); threadErr == nil {
				source.ThreadTitle = chatThreadDisplayTitleServer(thread)
				if index := scoutChatMessageIndex(thread, projection.SourceID); index >= 0 {
					if occurredAt, parseErr := parseSTRIDEChatTime(thread.Messages[index].CreatedAt); parseErr == nil {
						source.OccurredAt = &occurredAt
					}
				}
			}
			break
		}
		result = append(result, strideRelationshipPreferenceHTTP{STRIDECollaborationContextPreference: preference, Source: source})
	}
	return result, revision, nil
}

func chatThreadDisplayTitleServer(thread scoutChatThreadRecord) string {
	title := strings.TrimSpace(thread.Title)
	if thread.Table {
		return "#team"
	}
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic && title != "" {
		return "#" + strings.TrimPrefix(title, "#")
	}
	return firstNonEmptyString(title, "Scout chat")
}

func strideCoworkerSubrouteHandler(w http.ResponseWriter, r *http.Request) {
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil || kanbanApp.strideRuntime == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "STRIDE coworker preview is unavailable")
		return
	}
	if r.URL.Query().Get("tenantId") != "" || r.URL.Query().Get("orgId") != "" {
		writeAuthError(w, http.StatusForbidden, "tenant scope is server-derived")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, strideRuntimeAPIBase+"coworker/")
	switch path {
	case "context":
		strideCoworkerContextHTTP(w, r, user)
	case "relationships":
		strideCoworkerRelationshipsHTTP(w, r, user)
	case "relationships/consent":
		strideCoworkerRelationshipConsentHTTP(w, r, user)
	case "relationships/remember":
		strideCoworkerRelationshipRememberHTTP(w, r, user)
	case "relationships/import":
		strideCoworkerRelationshipImportHTTP(w, r, user)
	case "relationships/correct":
		strideCoworkerRelationshipCorrectHTTP(w, r, user)
	case "relationships/forget":
		strideCoworkerRelationshipForgetHTTP(w, r, user)
	case "files/select":
		strideCoworkerFileSelectHTTP(w, r, user)
	case "files/post":
		strideCoworkerFilePostHTTP(w, r, user)
	case "gifs/post":
		strideCoworkerGIFPostHTTP(w, r, user)
	default:
		writeAuthError(w, http.StatusNotFound, "STRIDE coworker route not found")
	}
}

func admittedSTRIDECoworkerCollaboration() (*durableSTRIDECollaborationStore, STRIDEProductContext, string, error) {
	if kanbanApp == nil || kanbanApp.strideRuntime == nil {
		return nil, STRIDEProductContext{}, "", ErrSTRIDECollaborationStoreDisabled
	}
	productContext, err := admittedSTRIDECoworkerProduct(kanbanApp.strideRuntime)
	if err != nil || !productContext.Config.RelationshipMemoryEnabled {
		return nil, STRIDEProductContext{}, "", ErrSTRIDECollaborationStoreDisabled
	}
	product, err := kanbanApp.strideCoworkerProduct()
	if err != nil || product.collaborationRepo == nil || product.collaborationRepo.requireEnabled() != nil {
		return nil, STRIDEProductContext{}, "", ErrSTRIDECollaborationStoreDisabled
	}
	return product.collaborationRepo, productContext, productContext.Config.TenantID, nil
}

func strideCoworkerRelationshipsHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, product, _, err := admittedSTRIDECoworkerCollaboration()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	principal := strideRuntimePrincipalForEmail(user.Email)
	now := strideCollaborationNow(product.Config)
	preferences, revision, err := inspectSTRIDERelationshipPreferences(store, product, user, now)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	consent, consentRevision, err := store.Consent(principal)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	if consentRevision != revision {
		writeSTRIDECoworkerError(w, ErrSTRIDECollaborationStoreConflict)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "revision": revision, "consent": consent, "preferences": preferences})
}

func strideCoworkerRelationshipConsentHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Action           string `json:"action"`
		ExpectedRevision int64  `json:"expectedRevision"`
		AllowInferred    bool   `json:"allowInferred"`
		AllowShared      bool   `json:"allowShared"`
	}
	if err := decodeSTRIDECoworkerJSON(w, r, &payload); err != nil || !oneOf(payload.Action, "enable", "disable") || payload.ExpectedRevision < 0 {
		writeAuthError(w, http.StatusBadRequest, "invalid relationship-memory consent control")
		return
	}
	store, product, _, err := admittedSTRIDECoworkerCollaboration()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	enable := payload.Action == "enable"
	if !enable && (payload.AllowInferred || payload.AllowShared) {
		writeAuthError(w, http.StatusBadRequest, "revoked consent cannot retain memory permissions")
		return
	}
	principal := strideRuntimePrincipalForEmail(user.Email)
	now := strideCollaborationNow(product.Config)
	if err := store.SetConsent(principal, payload.ExpectedRevision, enable, payload.AllowInferred, payload.AllowShared, now); err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	consent, revision, err := store.Consent(principal)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	notifySTRIDERelationshipMemoryChanged(user.Email, revision, now)
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "revision": revision, "consent": consent})
}

func strideCoworkerRelationshipRememberHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Action           string `json:"action"`
		ExpectedRevision int64  `json:"expectedRevision"`
		ThreadID         string `json:"threadId"`
		SourceMessageID  string `json:"sourceMessageId"`
		PreferenceType   string `json:"preferenceType"`
		Value            string `json:"value"`
		Scope            string `json:"scope"`
	}
	if err := decodeSTRIDECoworkerJSON(w, r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid explicit relationship-memory record")
		return
	}
	payload.PreferenceType = strings.ToLower(strings.TrimSpace(payload.PreferenceType))
	payload.Value = strings.TrimSpace(payload.Value)
	payload.Scope = strings.ToLower(strings.TrimSpace(payload.Scope))
	payload.ThreadID = strings.TrimSpace(payload.ThreadID)
	payload.SourceMessageID = strings.TrimSpace(payload.SourceMessageID)
	if payload.Action != "remember" || payload.ExpectedRevision < 0 || !safeSTRIDECollaborationPreferenceType(payload.PreferenceType) || payload.Value == "" || len(payload.Value) > 500 || !oneOf(payload.Scope, stridePreferencePrivate, stridePreferenceShared) || (payload.ThreadID == "") != (payload.SourceMessageID == "") {
		writeAuthError(w, http.StatusBadRequest, "invalid explicit relationship-memory record")
		return
	}
	store, product, _, err := admittedSTRIDECoworkerCollaboration()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	principal := strideRuntimePrincipalForEmail(user.Email)
	now := strideCollaborationNow(product.Config)
	audience := STRIDEAudience{Visibility: "private", Principals: []string{principal}}
	var control *STRIDECollaborationControlEvidence
	var evidence []STRIDEReference
	if payload.ThreadID != "" {
		thread, source, invocationErr := kanbanApp.strideCoworkerPublicInvocation(user, payload.ThreadID, payload.SourceMessageID, false)
		if invocationErr != nil {
			writeSTRIDECoworkerError(w, invocationErr)
			return
		}
		sourceProjection, projectionErr := strideCoworkerPendingChatProjection(thread, source)
		if projectionErr != nil {
			writeSTRIDECoworkerError(w, projectionErr)
			return
		}
		evidence = []STRIDEReference{sourceProjection.LatestEvent}
		if payload.Scope == stridePreferenceShared {
			audience = sourceProjection.Audience
		}
	} else {
		if payload.Scope != stridePreferencePrivate {
			writeAuthError(w, http.StatusBadRequest, "shared memory requires an explicit channel source")
			return
		}
		minted, mintErr := mintSTRIDECollaborationControlEvidence(product.Config, product.Receipt, principal, "remember", "", payload.PreferenceType, payload.Value, payload.Scope, audience, payload.ExpectedRevision, now)
		if mintErr != nil {
			writeSTRIDECoworkerError(w, mintErr)
			return
		}
		control = &minted
		evidence = []STRIDEReference{minted.Reference()}
	}
	scopeID := principal
	if payload.Scope == stridePreferenceShared {
		scopeID = payload.ThreadID
	}
	event := STRIDECollaborationPreferenceEvent{
		Action: stridePreferenceObserve, SubjectPrincipal: principal, Scope: payload.Scope, ScopeID: scopeID,
		PreferenceType: payload.PreferenceType, Value: payload.Value, Origin: stridePreferenceExplicit,
		Evidence: evidence, Confidence: 1, ObservedAt: now, ExpiresAt: now.Add(180 * 24 * time.Hour), Audience: audience,
	}
	if control != nil {
		err = store.RememberFromControl(principal, payload.ExpectedRevision, event, *control)
	} else {
		err = store.Remember(principal, payload.ExpectedRevision, event)
	}
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	preferences, revision, err := inspectSTRIDERelationshipPreferences(store, product, user, now)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	notifySTRIDERelationshipMemoryChanged(user.Email, revision, now)
	writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "revision": revision, "preferences": preferences})
}

type strideRelationshipImportEntry struct {
	Category string `json:"category"`
	Date     string `json:"date"`
	Value    string `json:"value"`
}

func normalizeSTRIDEMemoryImportValue(value string) string {
	value = strings.ToValidUTF8(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"), "")
	value = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || !unicode.IsControl(character) {
			return character
		}
		return -1
	}, value)
	lines := strings.Split(value, "\n")
	normalized := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(normalized) > 0 && !blank {
				normalized = append(normalized, "")
				blank = true
			}
			continue
		}
		normalized = append(normalized, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(normalized, "\n"))
}

func strideMemoryImportPreferenceType(category, date, value string) (string, bool) {
	base, ok := map[string]string{
		"instructions": "user_instruction", "identity": "identity_context", "career": "career_context",
		"projects": "project_context", "preferences": "personal_preference",
	}[strings.ToLower(strings.TrimSpace(category))]
	if !ok {
		return "", false
	}
	return base + "_import_" + sha256Hex([]byte(base + "\x00" + date + "\x00" + value))[:24], true
}

func strideCoworkerRelationshipImportHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Action           string                          `json:"action"`
		ExpectedRevision int64                           `json:"expectedRevision"`
		Entries          []strideRelationshipImportEntry `json:"entries"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, strideMemoryImportRequestMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || ensureJSONEOF(decoder) != nil || payload.Action != "import" || payload.ExpectedRevision < 0 || len(payload.Entries) == 0 || len(payload.Entries) > strideMemoryImportMaxEntries {
		writeAuthError(w, http.StatusBadRequest, "invalid relationship-memory import")
		return
	}
	type normalizedImport struct{ preferenceType, value string }
	normalized := make([]normalizedImport, 0, len(payload.Entries))
	seen := make(map[string]struct{}, len(payload.Entries))
	totalBytes := 0
	for _, entry := range payload.Entries {
		date := strings.ToLower(strings.TrimSpace(entry.Date))
		if date != "unknown" {
			parsed, err := time.Parse("2006-01-02", date)
			if err != nil || parsed.Format("2006-01-02") != date {
				writeAuthError(w, http.StatusBadRequest, "invalid relationship-memory import")
				return
			}
		}
		value := normalizeSTRIDEMemoryImportValue(entry.Value)
		preferenceType, categoryOK := strideMemoryImportPreferenceType(entry.Category, date, value)
		if !categoryOK || value == "" || len(value) > strideCollaborationPreferenceValueMaxBytes {
			writeAuthError(w, http.StatusBadRequest, "invalid relationship-memory import")
			return
		}
		value = "[" + date + "] " + value
		totalBytes += len(value)
		if totalBytes > strideMemoryImportNormalizedMaxBytes {
			writeAuthError(w, http.StatusRequestEntityTooLarge, "memory import is larger than 96 KiB")
			return
		}
		if _, duplicate := seen[preferenceType]; duplicate {
			continue
		}
		seen[preferenceType] = struct{}{}
		normalized = append(normalized, normalizedImport{preferenceType: preferenceType, value: value})
	}
	if len(normalized) == 0 {
		writeAuthError(w, http.StatusBadRequest, "memory import contains no usable entries")
		return
	}
	store, product, _, err := admittedSTRIDECoworkerCollaboration()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	principal := strideRuntimePrincipalForEmail(user.Email)
	now := strideCollaborationNow(product.Config)
	consent, revision, err := store.Consent(principal)
	if err != nil || revision != payload.ExpectedRevision {
		if err == nil {
			err = ErrSTRIDECollaborationStoreConflict
		}
		writeSTRIDECoworkerError(w, err)
		return
	}
	existing, existingRevision, err := store.Inspect(principal, now)
	if err != nil || existingRevision != payload.ExpectedRevision {
		if err == nil {
			err = ErrSTRIDECollaborationStoreConflict
		}
		writeSTRIDECoworkerError(w, err)
		return
	}
	existingValues := make(map[string]string, len(existing))
	for _, preference := range existing {
		existingValues[preference.PreferenceType] = preference.Value
	}
	items := make([]STRIDECollaborationImportItem, 0, len(normalized))
	alreadyPresent := 0
	audience := STRIDEAudience{Visibility: "private", Principals: []string{principal}}
	for index, entry := range normalized {
		if existingValues[entry.preferenceType] == entry.value {
			alreadyPresent++
			continue
		}
		observedAt := now.Add(time.Duration(index) * time.Nanosecond)
		control, mintErr := mintSTRIDECollaborationControlEvidence(product.Config, product.Receipt, principal, "remember", "", entry.preferenceType, entry.value, stridePreferencePrivate, audience, payload.ExpectedRevision, observedAt)
		if mintErr != nil {
			writeSTRIDECoworkerError(w, mintErr)
			return
		}
		items = append(items, STRIDECollaborationImportItem{Event: STRIDECollaborationPreferenceEvent{
			Action: stridePreferenceObserve, SubjectPrincipal: principal, Scope: stridePreferencePrivate, ScopeID: principal,
			PreferenceType: entry.preferenceType, Value: entry.value, Origin: stridePreferenceExplicit, Evidence: []STRIDEReference{control.Reference()},
			Confidence: 1, ObservedAt: observedAt, ExpiresAt: strideMemoryImportDurableExpiresAt, Audience: audience,
		}, Evidence: control})
	}
	if len(items) > 0 {
		if err := store.ImportFromControls(principal, payload.ExpectedRevision, items, now); err != nil {
			writeSTRIDECoworkerError(w, err)
			return
		}
	} else if !consent.Enabled {
		// A disabled subject cannot have active duplicates. Keep this explicit so
		// a malformed durable state cannot silently turn memory on.
		writeSTRIDECoworkerError(w, ErrSTRIDECollaborationPreferenceDenied)
		return
	}
	preferences, revision, err := inspectSTRIDERelationshipPreferences(store, product, user, now)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	consent, consentRevision, err := store.Consent(principal)
	if err != nil || consentRevision != revision {
		if err == nil {
			err = ErrSTRIDECollaborationStoreConflict
		}
		writeSTRIDECoworkerError(w, err)
		return
	}
	if len(items) > 0 {
		notifySTRIDERelationshipMemoryChanged(user.Email, revision, now)
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "revision": revision, "consent": consent,
		"preferences": preferences, "importedCount": len(items), "alreadyPresentCount": alreadyPresent,
	})
}

func strideCoworkerRelationshipCorrectHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Action           string `json:"action"`
		ExpectedRevision int64  `json:"expectedRevision"`
		RelationshipID   string `json:"relationshipId"`
		Value            string `json:"value"`
		ThreadID         string `json:"threadId"`
		SourceMessageID  string `json:"sourceMessageId"`
	}
	if err := decodeSTRIDECoworkerJSON(w, r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid relationship-memory correction")
		return
	}
	payload.RelationshipID = strings.TrimSpace(payload.RelationshipID)
	payload.Value = strings.TrimSpace(payload.Value)
	payload.ThreadID = strings.TrimSpace(payload.ThreadID)
	payload.SourceMessageID = strings.TrimSpace(payload.SourceMessageID)
	if payload.Action != "correct" || payload.ExpectedRevision < 0 || !strideIdentifier(payload.RelationshipID) || payload.Value == "" || len(payload.Value) > strideCollaborationPreferenceValueMaxBytes || (payload.ThreadID == "") != (payload.SourceMessageID == "") {
		writeAuthError(w, http.StatusBadRequest, "invalid relationship-memory correction")
		return
	}
	store, product, _, err := admittedSTRIDECoworkerCollaboration()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	principal := strideRuntimePrincipalForEmail(user.Email)
	now := strideCollaborationNow(product.Config)
	current, currentRevision, inspectErr := inspectSTRIDERelationshipPreferences(store, product, user, now)
	if inspectErr != nil || currentRevision != payload.ExpectedRevision {
		if inspectErr != nil {
			writeSTRIDECoworkerError(w, inspectErr)
		} else {
			writeSTRIDECoworkerError(w, ErrSTRIDECollaborationStoreConflict)
		}
		return
	}
	var selected *strideRelationshipPreferenceHTTP
	for index := range current {
		if current[index].Reference.ID == payload.RelationshipID {
			selected = &current[index]
			break
		}
	}
	if selected == nil {
		writeSTRIDECoworkerError(w, ErrSTRIDECollaborationPreferenceDenied)
		return
	}
	if payload.ThreadID != "" {
		thread, source, invocationErr := kanbanApp.strideCoworkerPublicInvocation(user, payload.ThreadID, payload.SourceMessageID, false)
		if invocationErr != nil {
			writeSTRIDECoworkerError(w, invocationErr)
			return
		}
		sourceProjection, projectionErr := strideCoworkerPendingChatProjection(thread, source)
		if projectionErr != nil {
			writeSTRIDECoworkerError(w, projectionErr)
			return
		}
		if selected.Scope != stridePreferenceShared || selected.Relationship.Scope != sourceProjection.ThreadID || !sameAudience(selected.Relationship.Audience, sourceProjection.Audience) {
			writeSTRIDECoworkerError(w, ErrSTRIDECollaborationPreferenceDenied)
			return
		}
		err = store.Correct(principal, payload.RelationshipID, payload.ExpectedRevision, payload.Value, []STRIDEReference{sourceProjection.LatestEvent}, now)
	} else {
		control, mintErr := mintSTRIDECollaborationControlEvidence(product.Config, product.Receipt, principal, "correct", payload.RelationshipID, selected.PreferenceType, payload.Value, selected.Scope, selected.Relationship.Audience, payload.ExpectedRevision, now)
		if mintErr != nil {
			writeSTRIDECoworkerError(w, mintErr)
			return
		}
		err = store.CorrectFromControl(principal, payload.RelationshipID, payload.ExpectedRevision, payload.Value, control, now)
	}
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	preferences, revision, err := inspectSTRIDERelationshipPreferences(store, product, user, now)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	notifySTRIDERelationshipMemoryChanged(user.Email, revision, now)
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "revision": revision, "preferences": preferences})
}

// reconcileSTRIDERelationshipSourceMutation runs immediately after a public
// source message is edited or deleted. Human chat remains authoritative even
// if the optional relationship-memory subsystem is unavailable; on an
// unexpected reconciliation error the revision-less event still forces every
// client to discard a potentially stale Realtime prompt snapshot.
func (app *kanbanBoardApp) reconcileSTRIDERelationshipSourceMutation(userEmail string) {
	userEmail = normalizeAccountEmail(userEmail)
	if app == nil || app.strideRuntime == nil || userEmail == "" {
		return
	}
	productContext, err := admittedSTRIDECoworkerProduct(app.strideRuntime)
	if err != nil || !productContext.Config.RelationshipMemoryEnabled {
		return
	}
	product, err := app.strideCoworkerProduct()
	if err != nil || product.collaborationRepo == nil || product.collaborationRepo.requireEnabled() != nil {
		return
	}
	now := strideCollaborationNow(productContext.Config)
	if _, _, err := inspectSTRIDERelationshipPreferences(product.collaborationRepo, productContext, &userAccount{Email: userEmail}, now); err != nil {
		notifySTRIDERelationshipMemoryChanged(userEmail, 0, now)
	}
}

func strideCoworkerRelationshipForgetHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Action           string `json:"action"`
		ExpectedRevision int64  `json:"expectedRevision"`
		RelationshipID   string `json:"relationshipId"`
	}
	if err := decodeSTRIDECoworkerJSON(w, r, &payload); err != nil || payload.Action != "forget" || payload.ExpectedRevision < 0 || !strideIdentifier(strings.TrimSpace(payload.RelationshipID)) {
		writeAuthError(w, http.StatusBadRequest, "invalid relationship-memory forget control")
		return
	}
	store, product, _, err := admittedSTRIDECoworkerCollaboration()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	principal := strideRuntimePrincipalForEmail(user.Email)
	now := strideCollaborationNow(product.Config)
	if err := store.Forget(principal, strings.TrimSpace(payload.RelationshipID), payload.ExpectedRevision, now); err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	preferences, revision, err := inspectSTRIDERelationshipPreferences(store, product, user, now)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	notifySTRIDERelationshipMemoryChanged(user.Email, revision, now)
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "revision": revision, "preferences": preferences})
}

func admittedSTRIDECoworkerProduct(runtime *STRIDERuntime) (STRIDEProductContext, error) {
	var admitted STRIDEProductContext
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeCoworker, func(product STRIDEProductContext) error {
		admitted = product
		return nil
	})
	return admitted, err
}

func strideCoworkerContextHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	thread, message, err := kanbanApp.strideCoworkerPublicInvocation(user, r.URL.Query().Get("threadId"), r.URL.Query().Get("messageId"), false)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	product, err := admittedSTRIDECoworkerProduct(kanbanApp.strideRuntime)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	assembled, err := kanbanApp.assembleSTRIDECoworkerContext(product, user, thread, message, true)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "context": assembled})
}

func strideCoworkerFileSelectHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Action          string `json:"action"`
		ThreadID        string `json:"threadId"`
		SourceMessageID string `json:"sourceMessageId"`
		FileID          string `json:"fileId"`
	}
	if err := decodeSTRIDECoworkerJSON(w, r, &payload); err != nil || payload.Action != "select" {
		writeAuthError(w, http.StatusBadRequest, "invalid explicit file selection")
		return
	}
	thread, _, err := kanbanApp.strideCoworkerPublicInvocation(user, payload.ThreadID, payload.SourceMessageID, false)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	productContext, err := admittedSTRIDECoworkerProduct(kanbanApp.strideRuntime)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	product, err := kanbanApp.strideCoworkerProduct()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	source, err := kanbanApp.resolveSTRIDECoworkerFileSource(r.Context(), user, payload.FileID)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	authority := strideCoworkerFileAuthority{app: kanbanApp}
	destination, err := authority.CurrentDestination(r.Context(), thread.ID)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	service := product.fileService(productContext.Config.Now)
	token, err := service.Mint(r.Context(), STRIDEFileSelectionMintRequest{Requester: user.Email, Source: source.Object, SourceRevision: source.Revision, Destination: destination, Purpose: "share_existing_file", TTL: 5 * time.Minute})
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "handleId": token.ID, "expiresAt": token.ExpiresAt, "file": source.Row})
}

func strideCoworkerFilePostHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Action       string `json:"action"`
		HandleID     string `json:"handleId"`
		ExecutionKey string `json:"executionKey"`
	}
	if err := decodeSTRIDECoworkerJSON(w, r, &payload); err != nil || payload.Action != "post" || strings.TrimSpace(payload.HandleID) == "" || len(strings.TrimSpace(payload.ExecutionKey)) < 16 || len(payload.ExecutionKey) > 200 {
		writeAuthError(w, http.StatusBadRequest, "invalid explicit file post")
		return
	}
	productContext, err := admittedSTRIDECoworkerProduct(kanbanApp.strideRuntime)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	product, err := kanbanApp.strideCoworkerProduct()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	receipt, err := product.fileService(productContext.Config.Now).Post(r.Context(), payload.HandleID, user.Email, payload.ExecutionKey)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "receipt": receipt})
}

func strideCoworkerGIFPostHTTP(w http.ResponseWriter, r *http.Request, user *userAccount) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Action          string `json:"action"`
		ThreadID        string `json:"threadId"`
		SourceMessageID string `json:"sourceMessageId"`
		Reaction        string `json:"reaction"`
		Tone            string `json:"tone"`
		ExecutionKey    string `json:"executionKey"`
	}
	if err := decodeSTRIDECoworkerJSON(w, r, &payload); err != nil || payload.Action != "post" || len(strings.TrimSpace(payload.ExecutionKey)) < 16 || len(payload.ExecutionKey) > 200 {
		writeAuthError(w, http.StatusBadRequest, "invalid explicit GIF post")
		return
	}
	thread, source, err := kanbanApp.strideCoworkerPublicInvocation(user, payload.ThreadID, payload.SourceMessageID, true)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	if _, err := admittedSTRIDECoworkerProduct(kanbanApp.strideRuntime); err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	product, err := kanbanApp.strideCoworkerProduct()
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	message, replayed, action, err := product.postLocalGIF(r.Context(), user, thread, source, payload.Reaction, payload.Tone, payload.ExecutionKey)
	if err != nil {
		writeSTRIDECoworkerError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "providerExecutionFenced": true, "mode": "deterministic_local", "replayed": replayed, "message": message, "gif": map[string]any{"provider": firstNonEmptyString(action.Provider, "local_fixture"), "rating": "g", "immutable": true}})
}

func (app *kanbanBoardApp) strideCoworkerPublicInvocation(user *userAccount, threadID, messageID string, requireTable bool) (scoutChatThreadRecord, scoutChatMessageRecord, error) {
	if app == nil || user == nil {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, ErrSTRIDECoworkerDenied
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, strings.TrimSpace(threadID))
	if err != nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" || requireTable && !thread.Table {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, ErrSTRIDECoworkerDenied
	}
	index := scoutChatMessageIndex(thread, strings.TrimSpace(messageID))
	if index < 0 {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, ErrSTRIDECoworkerDenied
	}
	message := thread.Messages[index]
	if message.Role != "user" || normalizeAccountEmail(message.AuthorEmail) != normalizeAccountEmail(user.Email) || !scoutChatMessageMentionsScout(message) {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, ErrSTRIDECoworkerDenied
	}
	// An older invocation cannot be replayed after this member has addressed
	// Scout again in the same channel; actions must bind to their latest explicit
	// turn and a fresh server-derived destination snapshot.
	for _, later := range thread.Messages[index+1:] {
		if later.Role == "user" && normalizeAccountEmail(later.AuthorEmail) == normalizeAccountEmail(user.Email) && scoutChatMessageMentionsScout(later) {
			return scoutChatThreadRecord{}, scoutChatMessageRecord{}, ErrSTRIDECoworkerDenied
		}
	}
	return thread, message, nil
}

func decodeSTRIDECoworkerJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func writeSTRIDECoworkerError(w http.ResponseWriter, err error) {
	status := http.StatusForbidden
	message := "STRIDE coworker action is unavailable"
	switch {
	case errors.Is(err, ErrSTRIDEProductDisabled), errors.Is(err, ErrSTRIDERuntimeDisabled), errors.Is(err, ErrSTRIDERuntimeUnavailable), errors.Is(err, ErrSTRIDERuntimeClosed), errors.Is(err, ErrSTRIDECollaborationStoreDisabled):
		status = http.StatusServiceUnavailable
		message = "STRIDE coworker preview is disabled"
	case errors.Is(err, ErrSTRIDECoworkerConflict), errors.Is(err, ErrSTRIDEFileDispatchState), errors.Is(err, ErrSTRIDEFileDispatchUnknown), errors.Is(err, ErrSTRIDECollaborationStoreConflict):
		status = http.StatusConflict
		message = "STRIDE coworker action state changed"
	}
	writeAuthError(w, status, message)
}
