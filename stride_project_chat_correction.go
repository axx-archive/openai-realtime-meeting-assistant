package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	projectChatCorrectionTokenVersion = 1
	projectChatCorrectionTokenTTL     = 15 * time.Minute
)

type projectChatCorrectionTarget struct {
	Kind            string `json:"kind"`
	ProjectID       string `json:"projectId,omitempty"`
	ProjectRevision int64  `json:"projectRevision,omitempty"`
	ProjectDigest   string `json:"projectDigest,omitempty"`
	ProjectTitle    string `json:"projectTitle,omitempty"`
}

type projectChatCorrectionToken struct {
	Version                int                         `json:"version"`
	Purpose                string                      `json:"purpose"`
	ThreadID               string                      `json:"threadId"`
	MessageID              string                      `json:"messageId"`
	ContextRevision        int64                       `json:"contextRevision"`
	OldAssociationID       string                      `json:"oldAssociationId"`
	OldAssociationRevision int64                       `json:"oldAssociationRevision"`
	OldAssociationDigest   string                      `json:"oldAssociationDigest"`
	SourceEventID          string                      `json:"sourceEventId"`
	SourceEventRevision    int64                       `json:"sourceEventRevision"`
	SourceDigest           string                      `json:"sourceDigest"`
	SourceACLRevision      int64                       `json:"sourceAclRevision"`
	SourceACLDigest        string                      `json:"sourceAclDigest"`
	PurgeGeneration        int64                       `json:"purgeGeneration"`
	PersonID               string                      `json:"personId"`
	OrganizationID         string                      `json:"organizationId"`
	MembershipID           string                      `json:"membershipId"`
	MembershipRevision     int64                       `json:"membershipRevision"`
	SessionSubjectDigest   string                      `json:"sessionSubjectDigest"`
	SessionRevision        int64                       `json:"sessionRevision"`
	AuthorityGeneration    uint64                      `json:"authorityGeneration"`
	Target                 projectChatCorrectionTarget `json:"target"`
	IssuedAt               time.Time                   `json:"issuedAt"`
	ExpiresAt              time.Time                   `json:"expiresAt"`
	KeyID                  string                      `json:"keyId"`
	KeyVersion             uint64                      `json:"keyVersion"`
}

type projectChatCorrectionChoice struct {
	Title string `json:"title"`
	Token string `json:"token"`
}

type projectChatCorrectionCurrent struct {
	Title           string `json:"title"`
	Status          string `json:"status"`
	ContextRevision int64  `json:"contextRevision"`
}

type projectChatCorrectionPreview struct {
	Available bool                          `json:"available"`
	ScopeKey  string                        `json:"scopeKey,omitempty"`
	Current   projectChatCorrectionCurrent  `json:"current"`
	Choices   []projectChatCorrectionChoice `json:"choices,omitempty"`
	Remove    *projectChatCorrectionChoice  `json:"remove,omitempty"`
}

type projectChatCorrectionTruth struct {
	AssociationID       string
	AssociationRevision int64
	AssociationDigest   string
	ProjectID           string
	ProjectRevision     int64
	ProjectDigest       string
	ProjectTitle        string
	SourceEventID       string
	SourceEventRevision int64
	SourceDigest        string
	SourceACLRevision   int64
	SourceACLDigest     string
	PurgeGeneration     int64
}

type confirmedProjectChatCorrection struct {
	Status                 string
	ContextRevision        int64
	OldAssociationID       string
	OldAssociationRevision int64
	OldResultRevision      int64
	ProjectID              string
	ProjectRevision        int64
	ProjectTitle           string
	AssociationID          string
	AssociationRevision    int64
}

func projectChatCorrectionContextRevision(project *scoutChatProjectContext) int64 {
	if project == nil {
		return 0
	}
	if project.ContextRevision < 1 {
		return 1
	}
	return project.ContextRevision
}

func projectChatCorrectionTokenMAC(key StrideE10TenantAuthorityEnvelopeKey, raw []byte) []byte {
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte("project-chat-correction/v1\x00"))
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
}

func mintProjectChatCorrectionToken(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, threadID, messageID string, contextRevision int64, truth projectChatCorrectionTruth, target projectChatCorrectionTarget) (string, error) {
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if runtime == nil || runtime.keys == nil {
		return "", errHomeProjectUnavailable
	}
	key, err := runtime.keys.CurrentStrideE10TenantAuthorityEnvelopeKey(ctx)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) {
		return "", errHomeProjectUnavailable
	}
	now := time.Now().UTC()
	token := projectChatCorrectionToken{
		Version: projectChatCorrectionTokenVersion, Purpose: "sent_message_project_correction",
		ThreadID: threadID, MessageID: messageID, ContextRevision: contextRevision,
		OldAssociationID: truth.AssociationID, OldAssociationRevision: truth.AssociationRevision, OldAssociationDigest: truth.AssociationDigest,
		SourceEventID: truth.SourceEventID, SourceEventRevision: truth.SourceEventRevision, SourceDigest: truth.SourceDigest,
		SourceACLRevision: truth.SourceACLRevision, SourceACLDigest: truth.SourceACLDigest, PurgeGeneration: truth.PurgeGeneration,
		PersonID: snapshot.Person.Header.ID, OrganizationID: snapshot.Organization.Header.ID,
		MembershipID: snapshot.Membership.Header.ID, MembershipRevision: snapshot.Membership.Header.Revision,
		SessionSubjectDigest: snapshot.SessionHash, SessionRevision: snapshot.ActiveSession.SessionRevision,
		AuthorityGeneration: snapshot.Generation, Target: target, IssuedAt: now, ExpiresAt: now.Add(projectChatCorrectionTokenTTL),
		KeyID: key.ID, KeyVersion: key.Version,
	}
	raw, _ := json.Marshal(token)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(projectChatCorrectionTokenMAC(key, raw)), nil
}

func resolveProjectChatCorrectionToken(ctx context.Context, encoded string, snapshot StrideE10TenantAuthoritySnapshot, acceptedPending bool) (projectChatCorrectionToken, error) {
	var token projectChatCorrectionToken
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 {
		return token, errHomeProjectStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(raw) > 16<<10 || json.Unmarshal(raw, &token) != nil {
		return token, errHomeProjectStale
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if err != nil || runtime == nil || runtime.keys == nil {
		return token, errHomeProjectStale
	}
	key, err := runtime.keys.ResolveStrideE10TenantAuthorityEnvelopeKey(ctx, token.KeyID, token.KeyVersion)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) || !hmac.Equal(signature, projectChatCorrectionTokenMAC(key, raw)) {
		return token, errHomeProjectStale
	}
	validTarget := token.Target.Kind == "remove" && token.Target.ProjectID == "" && token.Target.ProjectRevision == 0 && token.Target.ProjectDigest == "" && token.Target.ProjectTitle == ""
	if token.Target.Kind == "project" {
		validTarget = strideIdentifier(token.Target.ProjectID) && token.Target.ProjectRevision > 0 && isHexDigest(token.Target.ProjectDigest) && stridePlainText(token.Target.ProjectTitle, 120, true)
	}
	if token.Version != projectChatCorrectionTokenVersion || token.Purpose != "sent_message_project_correction" ||
		!strideIdentifier(token.ThreadID) || !strideIdentifier(token.MessageID) || token.ContextRevision < 1 ||
		!strideIdentifier(token.OldAssociationID) || token.OldAssociationRevision < 1 || !isHexDigest(token.OldAssociationDigest) ||
		!strideIdentifier(token.SourceEventID) || token.SourceEventRevision < 1 || !isHexDigest(token.SourceDigest) ||
		token.SourceACLRevision < 1 || !isHexDigest(token.SourceACLDigest) || token.PurgeGeneration < 1 ||
		token.PersonID != snapshot.Person.Header.ID || token.OrganizationID != snapshot.Organization.Header.ID ||
		token.MembershipID != snapshot.Membership.Header.ID || token.MembershipRevision != snapshot.Membership.Header.Revision ||
		token.SessionSubjectDigest != snapshot.SessionHash || token.SessionRevision != snapshot.ActiveSession.SessionRevision ||
		token.AuthorityGeneration != snapshot.Generation || token.IssuedAt.IsZero() || token.ExpiresAt.IsZero() || !token.ExpiresAt.After(token.IssuedAt) ||
		(!acceptedPending && !time.Now().UTC().Before(token.ExpiresAt)) || !validTarget {
		return token, errHomeProjectStale
	}
	return token, nil
}

func (store *PostgresCanonicalStore) projectChatCorrectionTruth(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, threadID, messageID string, project *scoutChatProjectContext) (projectChatCorrectionTruth, error) {
	var truth projectChatCorrectionTruth
	if store == nil || store.pool == nil || project == nil || project.Status != "confirmed" || project.AssociationID == "" || project.AssociationRevision < 1 {
		return truth, ErrProjectAuthorityNotFound
	}
	err := store.pool.QueryRow(ctx, `SELECT association.association_id,association.revision,encode(association.content_digest,'hex'),
association.project_id,association.project_revision,encode(project.content_digest,'hex'),project.title,source.event_id,source.content_revision,encode(source.content_digest,'hex'),
source.acl_version,encode(association.source_acl_digest,'hex'),source.purge_generation
FROM stride_project_associations_authorized_current current_association
JOIN stride_project_association_revisions association ON association.association_id=current_association.association_id AND association.revision=current_association.revision AND association.organization_id=current_association.organization_id
JOIN stride_project_revisions project ON project.project_id=association.project_id AND project.revision=association.project_revision AND project.organization_id=association.organization_id
JOIN stride_conversation_events source ON source.tenant_id=association.organization_id AND source.event_id=association.subject_id
WHERE current_association.organization_id=$1 AND current_association.association_id=$2 AND current_association.revision=$3 AND current_association.state='confirmed'
  AND source.thread_id=$4 AND source.source_id=$5 AND source.author_principal=$6 AND source.invalidated_at IS NULL`, snapshot.Organization.Header.ID, project.AssociationID, project.AssociationRevision, threadID, messageID, snapshot.Person.Header.ID).
		Scan(&truth.AssociationID, &truth.AssociationRevision, &truth.AssociationDigest, &truth.ProjectID, &truth.ProjectRevision, &truth.ProjectDigest, &truth.ProjectTitle,
			&truth.SourceEventID, &truth.SourceEventRevision, &truth.SourceDigest, &truth.SourceACLRevision, &truth.SourceACLDigest, &truth.PurgeGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return truth, ErrProjectAuthorityNotFound
	}
	return truth, err
}

func projectChatMessageAuthorOnly(thread scoutChatThreadRecord, message scoutChatMessageRecord, user *userAccount) bool {
	return user != nil && message.Role == "user" && strings.TrimSpace(message.AuthorEmail) != "" &&
		normalizeAccountEmail(message.AuthorEmail) == normalizeAccountEmail(user.Email)
}

func buildProjectChatCorrectionPreview(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord, snapshot StrideE10TenantAuthoritySnapshot) (projectChatCorrectionPreview, error) {
	preview := projectChatCorrectionPreview{Current: projectChatCorrectionCurrent{Status: "unavailable"}}
	if !projectChatMessageAuthorOnly(thread, message, user) || message.Project == nil {
		return preview, ErrProjectAuthorityDenied
	}
	preview.Current = projectChatCorrectionCurrent{Title: message.Project.Title, Status: message.Project.Status, ContextRevision: projectChatCorrectionContextRevision(message.Project)}
	store := currentHomeProjectStore()
	if store == nil || !homeProjectFeatureEnabled(STRIDEFeatureProjectAuthorityWrite) {
		return preview, errHomeProjectUnavailable
	}
	truth, err := store.projectChatCorrectionTruth(ctx, snapshot, thread.ID, message.ID, message.Project)
	if err != nil {
		return preview, err
	}
	projects, err := visibleHomeProjects(ctx, snapshot)
	if err != nil {
		return preview, err
	}
	preview.Available = true
	preview.ScopeKey = homeProjectScopeKey(snapshot)
	for _, project := range projects {
		if project.ID == message.Project.ProjectID && project.Revision == message.Project.ProjectRevision {
			continue
		}
		target := projectChatCorrectionTarget{Kind: "project", ProjectID: project.ID, ProjectRevision: project.Revision, ProjectDigest: project.Digest, ProjectTitle: project.Title}
		token, mintErr := mintProjectChatCorrectionToken(ctx, snapshot, thread.ID, message.ID, preview.Current.ContextRevision, truth, target)
		if mintErr != nil {
			return preview, mintErr
		}
		preview.Choices = append(preview.Choices, projectChatCorrectionChoice{Title: project.Title, Token: token})
	}
	removeToken, err := mintProjectChatCorrectionToken(ctx, snapshot, thread.ID, message.ID, preview.Current.ContextRevision, truth, projectChatCorrectionTarget{Kind: "remove"})
	if err != nil {
		return preview, err
	}
	preview.Remove = &projectChatCorrectionChoice{Title: "No project", Token: removeToken}
	return preview, nil
}

func withProjectChatCorrectionAuthority(r *http.Request, use func(StrideE10TenantAuthoritySnapshot) error) error {
	return withCurrentHomeProjectAuthority(r, use)
}

func handleProjectChatCorrection(w http.ResponseWriter, r *http.Request, user *userAccount, threadID, messageID string) {
	thread, _, err := kanbanApp.scoutChatThreadByID(user.Email, threadID)
	if err != nil || thread.ArchivedAt != "" {
		writeScoutChatThreadError(w, firstError(err, fmt.Errorf("chat thread is archived")))
		return
	}
	if hasPendingProjectCorrection(thread) {
		mutationContext := strideE10TenantContextWithSessionHash(r.Context(), strideE10SessionHashFromRequest(r))
		if reconciled, reconcileErr := kanbanApp.reconcileCommittedProjectCorrections(mutationContext, user, threadID); reconcileErr == nil {
			thread = reconciled
		} else {
			log.Errorf("Project correction remains pending for %s: %v", threadID, reconcileErr)
		}
	}
	index := scoutChatMessageIndex(thread, messageID)
	if index < 0 {
		writeAuthError(w, http.StatusNotFound, "chat message not found")
		return
	}
	message := thread.Messages[index]
	if !projectChatMessageAuthorOnly(thread, message, user) {
		writeAuthError(w, http.StatusForbidden, "only the message author can change its Project")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodGet {
		var preview projectChatCorrectionPreview
		err = withProjectChatCorrectionAuthority(r, func(snapshot StrideE10TenantAuthoritySnapshot) error {
			var buildErr error
			preview, buildErr = buildProjectChatCorrectionPreview(r.Context(), user, thread, message, snapshot)
			return buildErr
		})
		if err != nil {
			status := http.StatusConflict
			if errors.Is(err, ErrProjectAuthorityDenied) {
				status = http.StatusForbidden
			}
			writeAuthError(w, status, "Project correction is unavailable")
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "projectCorrection": preview})
		return
	}
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", "GET, PATCH")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	payload := struct {
		OperationID     string `json:"operationId"`
		CorrectionToken string `json:"correctionToken"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read Project correction")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAuthError(w, http.StatusBadRequest, "Project correction must contain exactly one object")
		return
	}
	operationID, err := normalizeScoutIdempotencyKey(payload.OperationID)
	if err != nil || strings.TrimSpace(payload.CorrectionToken) == "" {
		writeAuthError(w, http.StatusBadRequest, "Project correction operation is invalid")
		return
	}
	var token projectChatCorrectionToken
	err = withProjectChatCorrectionAuthority(r, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		acceptedPending := kanbanApp.acceptedScoutProjectCorrectionRetry(user, threadID, messageID, operationID, payload.CorrectionToken)
		var tokenErr error
		token, tokenErr = resolveProjectChatCorrectionToken(r.Context(), payload.CorrectionToken, snapshot, acceptedPending)
		if tokenErr != nil {
			return tokenErr
		}
		if token.ThreadID != threadID || token.MessageID != messageID {
			return errHomeProjectStale
		}
		return nil
	})
	if err != nil {
		writeProjectChatCorrectionConflict(w, r, user, thread, message)
		return
	}
	updated, _, err := kanbanApp.applyScoutProjectCorrection(r.Context(), user, thread, message, operationID, payload.CorrectionToken, token)
	if err != nil {
		if errors.Is(err, ErrProjectAuthorityConflict) || errors.Is(err, ErrProjectAuthorityDenied) || errors.Is(err, ErrProjectAuthorityNotFound) || errors.Is(err, errHomeProjectStale) {
			writeProjectChatCorrectionConflict(w, r, user, thread, message)
			return
		}
		writeScoutChatThreadError(w, err)
		return
	}
	projected := kanbanApp.projectScoutChatThreadForViewer(user.Email, updated)
	messageIndex := scoutChatMessageIndex(projected, messageID)
	response := map[string]any{"ok": true, "thread": projected}
	if messageIndex >= 0 {
		response["message"] = projected.Messages[messageIndex]
	}
	writeAuthJSON(w, http.StatusOK, response)
}

func writeProjectChatCorrectionConflict(w http.ResponseWriter, r *http.Request, user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord) {
	response := map[string]any{"ok": false, "error": "Project changed elsewhere. Review and confirm again."}
	_ = withProjectChatCorrectionAuthority(r, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		current, _, err := kanbanApp.scoutChatThreadByID(user.Email, thread.ID)
		if err != nil {
			return err
		}
		index := scoutChatMessageIndex(current, message.ID)
		if index < 0 {
			return ErrProjectAuthorityNotFound
		}
		preview, err := buildProjectChatCorrectionPreview(r.Context(), user, current, current.Messages[index], snapshot)
		if err == nil {
			response["projectCorrection"] = preview
		}
		return nil
	})
	writeAuthJSON(w, http.StatusConflict, response)
}

func (app *kanbanBoardApp) acceptedScoutProjectCorrectionRetry(user *userAccount, threadID, messageID, operationID, encodedToken string) bool {
	if app == nil || user == nil || !strideIdentifier(threadID) || !strideIdentifier(messageID) || !strideIdentifier(operationID) {
		return false
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return false
	}
	tokenDigest := homeProjectTokenDigest(encodedToken)
	for _, operation := range thread.ProjectCorrectionOperations {
		if operation.OperationID == operationID && operation.MessageID == messageID && operation.TokenDigest == tokenDigest && oneOf(operation.State, "pending", "confirmed") {
			return true
		}
	}
	return false
}

func (app *kanbanBoardApp) beginScoutProjectCorrection(user *userAccount, threadID, messageID, operationID, encodedToken string, token projectChatCorrectionToken) (scoutChatThreadRecord, scoutChatProjectCorrectionOperation, bool, error) {
	if app == nil || user == nil || !strideIdentifier(threadID) || !strideIdentifier(messageID) || !strideIdentifier(operationID) || token.ThreadID != threadID || token.MessageID != messageID {
		return scoutChatThreadRecord{}, scoutChatProjectCorrectionOperation{}, false, ErrProjectAuthorityInvalid
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil || thread.ArchivedAt != "" {
		if err == nil {
			err = errHomeProjectStale
		}
		return thread, scoutChatProjectCorrectionOperation{}, false, err
	}
	index := scoutChatMessageIndex(thread, messageID)
	if index < 0 || !projectChatMessageAuthorOnly(thread, thread.Messages[index], user) {
		return thread, scoutChatProjectCorrectionOperation{}, false, ErrProjectAuthorityDenied
	}
	for _, linkOperation := range thread.ProjectLinkOperations {
		if linkOperation.MessageID == messageID && linkOperation.SourceGroupID != "" && len(linkOperation.AssociationIDs) > 1 {
			// The canonical group transaction owns every root/part edge. This
			// journal remains one operation so retry can finish the exact group
			// result after either durable store crosses its commit boundary.
			break
		}
	}
	tokenDigest := homeProjectTokenDigest(encodedToken)
	for _, existing := range thread.ProjectCorrectionOperations {
		if existing.OperationID != operationID {
			if existing.MessageID == messageID && existing.State == "pending" {
				return thread, existing, false, ErrProjectAuthorityConflict
			}
			continue
		}
		if existing.TokenDigest != tokenDigest || existing.MessageID != messageID || existing.ExpectedContextRevision != token.ContextRevision {
			return thread, existing, false, ErrProjectAuthorityConflict
		}
		if existing.State == "failed" {
			return thread, existing, false, ErrProjectAuthorityConflict
		}
		return thread, existing, false, nil
	}
	project := thread.Messages[index].Project
	if project == nil || project.Status != "confirmed" || projectChatCorrectionContextRevision(project) != token.ContextRevision || project.AssociationID != token.OldAssociationID || project.AssociationRevision != token.OldAssociationRevision {
		return thread, scoutChatProjectCorrectionOperation{}, false, ErrProjectAuthorityConflict
	}
	expectedProject := *project
	operation := scoutChatProjectCorrectionOperation{
		OperationID: operationID, TokenDigest: tokenDigest, MessageID: messageID,
		OrganizationID: token.OrganizationID, ActorPersonID: token.PersonID,
		ActorEmail: normalizeAccountEmail(user.Email), ExpectedProject: expectedProject,
		ExpectedContextRevision: token.ContextRevision, State: "pending",
	}
	thread.ProjectCorrectionOperations = append(thread.ProjectCorrectionOperations, operation)
	// The journal and canonical transaction live in different durable stores.
	// Once the journal exists, never continue projecting the old association as
	// confirmed: a crash may occur after PostgreSQL commits but before the
	// legacy chat record is finalized.
	thread.Messages[index].Project = &scoutChatProjectContext{
		Status: "unavailable", ContextRevision: token.ContextRevision + 1,
		Title: expectedProject.Title, ProjectID: expectedProject.ProjectID,
		ProjectRevision:     expectedProject.ProjectRevision,
		AssociationID:       expectedProject.AssociationID,
		AssociationRevision: expectedProject.AssociationRevision,
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		return thread, operation, false, err
	}
	deliverScoutChatThreadUpdate(thread, thread.Messages[index])
	return thread, operation, true, nil
}

func withCurrentProjectCorrectionAuthorityRequestContext(ctx context.Context, token projectChatCorrectionToken, use func(StrideE10TenantAuthoritySnapshot) error) error {
	converter := currentStrideE10TenantRuntimeConverter()
	if converter == nil || use == nil {
		return errHomeProjectUnavailable
	}
	resolver, ok := converter.resolver.(*strideE10MainTenantAuthorityResolver)
	if !ok || resolver == nil {
		return errHomeProjectUnavailable
	}
	return resolver.WithCurrentTenantAuthority(ctx, StrideE10TenantSurfaceHTTP, token.SessionSubjectDigest, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		if snapshot.SessionHash != token.SessionSubjectDigest || snapshot.Person.Header.ID != token.PersonID ||
			snapshot.Organization.Header.ID != token.OrganizationID || snapshot.Membership.Header.ID != token.MembershipID ||
			snapshot.Membership.Header.Revision != token.MembershipRevision || snapshot.ActiveSession.SessionRevision != token.SessionRevision ||
			snapshot.Generation != token.AuthorityGeneration {
			return errHomeProjectStale
		}
		return use(snapshot)
	})
}

func withCurrentProjectCorrectionSession(ctx context.Context, use func(StrideE10TenantAuthoritySnapshot) error) error {
	converter := currentStrideE10TenantRuntimeConverter()
	if converter == nil || use == nil {
		return errHomeProjectUnavailable
	}
	resolver, ok := converter.resolver.(*strideE10MainTenantAuthorityResolver)
	if !ok || resolver == nil {
		return errHomeProjectUnavailable
	}
	sessionHash := strideE10TenantSessionHashFromContext(ctx)
	if !validStrideE10SessionHash(sessionHash) {
		return ErrStrideE10TenantAuthorityStale
	}
	return resolver.WithCurrentTenantAuthority(ctx, StrideE10TenantSurfaceHTTP, sessionHash, use)
}

func (app *kanbanBoardApp) applyScoutProjectCorrection(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord, operationID, encodedToken string, token projectChatCorrectionToken) (scoutChatThreadRecord, confirmedProjectChatCorrection, error) {
	var result confirmedProjectChatCorrection
	journaled, operation, _, err := app.beginScoutProjectCorrection(user, thread.ID, message.ID, operationID, encodedToken, token)
	if err != nil {
		return thread, result, err
	}
	store := currentHomeProjectStore()
	if store == nil {
		return journaled, result, errHomeProjectUnavailable
	}
	if groupID, groupMembers, groupErr := store.projectChatSourceGroupForAssociation(ctx, token.OrganizationID, token.OldAssociationID); groupErr != nil {
		return journaled, result, groupErr
	} else if groupID != "" && groupMembers > 1 {
		if committed, found, loadErr := store.committedProjectChatSourceGroupCorrection(ctx, operation.OrganizationID, operation.ActorPersonID,
			operationID, token); loadErr != nil {
			return journaled, result, loadErr
		} else if found {
			updated, finishErr := app.finishCommittedScoutProjectCorrection(thread.ID, message.ID, operationID, committed)
			return updated, committed, finishErr
		}
		err = withCurrentProjectCorrectionAuthorityRequestContext(ctx, token, func(snapshot StrideE10TenantAuthoritySnapshot) error {
			var groupErr error
			if token.Target.Kind == "remove" {
				result, groupErr = store.removeProjectChatSourceGroup(ctx, snapshot, groupID, operationID, token)
			} else {
				result, groupErr = store.replaceProjectChatSourceGroup(ctx, snapshot, groupID, operationID, token)
			}
			return groupErr
		})
		if err != nil {
			if committed, found, loadErr := store.committedProjectChatSourceGroupCorrection(ctx, operation.OrganizationID, operation.ActorPersonID,
				operationID, token); loadErr == nil && found {
				updated, finishErr := app.finishCommittedScoutProjectCorrection(thread.ID, message.ID, operationID, committed)
				return updated, committed, finishErr
			}
			return journaled, result, err
		}
		updated, finishErr := app.finishScoutProjectCorrection(user, thread.ID, message.ID, operationID, result)
		return updated, result, finishErr
	}
	// A prior request may have crossed the PostgreSQL commit boundary and lost
	// its response before the legacy record finalized. Consume that immutable
	// receipt before asking the now-current session to authorize anything new.
	if committed, found, loadErr := store.committedProjectChatCorrection(ctx, operation.OrganizationID, operation.ActorPersonID, thread.ID, operation); loadErr != nil {
		return journaled, result, loadErr
	} else if found {
		updated, finishErr := app.finishCommittedScoutProjectCorrection(thread.ID, message.ID, operationID, committed)
		return updated, committed, finishErr
	}
	err = withCurrentProjectCorrectionAuthorityRequestContext(ctx, token, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		var correctionErr error
		result, correctionErr = store.correctProjectChatAssociation(ctx, snapshot, operationID, encodedToken, token)
		return correctionErr
	})
	if err != nil {
		if committed, found, loadErr := store.committedProjectChatCorrection(ctx, operation.OrganizationID, operation.ActorPersonID, thread.ID, operation); loadErr == nil && found {
			updated, finishErr := app.finishCommittedScoutProjectCorrection(thread.ID, message.ID, operationID, committed)
			return updated, committed, finishErr
		}
		// Keep the operation pending/unavailable. Even after a receipt-absent
		// read, another process can commit before this authority error returns.
		// The exact retry remains resumable and performs receipt-first recovery.
		return journaled, result, err
	}
	updated, err := app.finishScoutProjectCorrection(user, thread.ID, message.ID, operationID, result)
	return updated, result, err
}

func (app *kanbanBoardApp) failScoutProjectCorrection(user *userAccount, threadID, messageID, operationID string, receiptAbsent ...bool) error {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return err
	}
	for index := range thread.ProjectCorrectionOperations {
		operation := &thread.ProjectCorrectionOperations[index]
		if operation.OperationID != operationID || operation.MessageID != messageID {
			continue
		}
		if operation.State == "confirmed" {
			return ErrProjectAuthorityConflict
		}
		operation.State = "failed"
		// Restore only when an exact immutable-receipt lookup has just proved the
		// operation never committed. Ambiguous authority/storage failures stay
		// unavailable because another process may already have committed.
		if len(receiptAbsent) > 0 && receiptAbsent[0] {
			messageIndex := scoutChatMessageIndex(thread, operation.MessageID)
			if messageIndex >= 0 {
				current := thread.Messages[messageIndex].Project
				if current != nil && current.Status == "unavailable" && projectChatCorrectionContextRevision(current) == operation.ExpectedContextRevision+1 {
					project := operation.ExpectedProject
					thread.Messages[messageIndex].Project = &project
				}
			}
		}
		return app.saveScoutChatThread(thread)
	}
	return ErrProjectAuthorityNotFound
}

func projectCorrectionResultFromJournal(operation scoutChatProjectCorrectionOperation) confirmedProjectChatCorrection {
	return confirmedProjectChatCorrection{Status: operation.ResultStatus, ContextRevision: operation.ResultContextRevision,
		ProjectID: operation.ResultProjectID, ProjectRevision: operation.ResultProjectRevision, ProjectTitle: operation.ResultProjectTitle,
		AssociationID: operation.ResultAssociationID, AssociationRevision: operation.ResultAssociationRevision,
		OldAssociationID: operation.ResultOldAssociationID, OldAssociationRevision: operation.ResultOldAssociationRevision,
		OldResultRevision: operation.ResultOldResultRevision}
}

func projectChatContextMatchesCorrection(project *scoutChatProjectContext, result confirmedProjectChatCorrection) bool {
	if project == nil || projectChatCorrectionContextRevision(project) != result.ContextRevision {
		return false
	}
	if result.Status == "removed" {
		return project.Status == "removed" && project.AssociationID == result.OldAssociationID && project.AssociationRevision == result.OldResultRevision
	}
	return project.Status == "confirmed" && project.ProjectID == result.ProjectID && project.ProjectRevision == result.ProjectRevision &&
		project.Title == result.ProjectTitle && project.AssociationID == result.AssociationID && project.AssociationRevision == result.AssociationRevision
}

func (app *kanbanBoardApp) finishScoutProjectCorrection(user *userAccount, threadID, messageID, operationID string, result confirmedProjectChatCorrection) (scoutChatThreadRecord, error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return thread, err
	}
	index := scoutChatMessageIndex(thread, messageID)
	if index < 0 || !projectChatMessageAuthorOnly(thread, thread.Messages[index], user) {
		return thread, ErrProjectAuthorityConflict
	}
	operationIndex := -1
	for candidate := range thread.ProjectCorrectionOperations {
		if thread.ProjectCorrectionOperations[candidate].OperationID == operationID {
			operationIndex = candidate
			break
		}
	}
	if operationIndex < 0 {
		return thread, ErrProjectAuthorityConflict
	}
	operation := &thread.ProjectCorrectionOperations[operationIndex]
	if operation.State == "confirmed" {
		if projectCorrectionResultFromJournal(*operation) != result || !projectChatContextMatchesCorrection(thread.Messages[index].Project, result) {
			return thread, ErrProjectAuthorityConflict
		}
		return thread, nil
	}
	project := thread.Messages[index].Project
	pendingProjection := project != nil && project.Status == "unavailable" &&
		projectChatCorrectionContextRevision(project) == operation.ExpectedContextRevision+1 &&
		project.AssociationID == result.OldAssociationID && project.AssociationRevision == result.OldAssociationRevision
	legacyProjection := project != nil && project.Status == "confirmed" &&
		projectChatCorrectionContextRevision(project) == operation.ExpectedContextRevision &&
		project.AssociationID == result.OldAssociationID && project.AssociationRevision == result.OldAssociationRevision
	if !pendingProjection && !legacyProjection {
		return thread, ErrProjectAuthorityConflict
	}
	operation.State = "confirmed"
	operation.ResultStatus = result.Status
	operation.ResultContextRevision = result.ContextRevision
	operation.ResultProjectID = result.ProjectID
	operation.ResultProjectRevision = result.ProjectRevision
	operation.ResultProjectTitle = result.ProjectTitle
	operation.ResultAssociationID = result.AssociationID
	operation.ResultAssociationRevision = result.AssociationRevision
	operation.ResultOldAssociationID = result.OldAssociationID
	operation.ResultOldAssociationRevision = result.OldAssociationRevision
	operation.ResultOldResultRevision = result.OldResultRevision
	if result.Status == "removed" {
		thread.Messages[index].Project = &scoutChatProjectContext{Status: "removed", ContextRevision: result.ContextRevision, AssociationID: result.OldAssociationID, AssociationRevision: result.OldResultRevision}
	} else {
		thread.Messages[index].Project = &scoutChatProjectContext{Status: "confirmed", ContextRevision: result.ContextRevision,
			ProjectID: result.ProjectID, ProjectRevision: result.ProjectRevision, Title: result.ProjectTitle, Basis: "selected",
			AssociationID: result.AssociationID, AssociationRevision: result.AssociationRevision}
	}
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(thread); err != nil {
		return thread, err
	}
	deliverScoutChatThreadUpdate(thread, thread.Messages[index])
	return thread, nil
}

func (store *PostgresCanonicalStore) correctProjectChatAssociation(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, operationID, encodedToken string, token projectChatCorrectionToken) (confirmedProjectChatCorrection, error) {
	var result confirmedProjectChatCorrection
	if store == nil || store.pool == nil || token.OrganizationID != snapshot.Organization.Header.ID || token.PersonID != snapshot.Person.Header.ID || !strideIdentifier(operationID) {
		return result, ErrProjectAuthorityInvalid
	}
	result.Status = map[string]string{"project": "confirmed", "remove": "removed"}[token.Target.Kind]
	if result.Status == "" {
		return result, ErrProjectAuthorityInvalid
	}
	result.ContextRevision = token.ContextRevision + 1
	result.OldAssociationID = token.OldAssociationID
	result.OldAssociationRevision = token.OldAssociationRevision
	result.OldResultRevision = token.OldAssociationRevision + 1
	requestFingerprint := sha256Hex([]byte(strings.Join([]string{token.ThreadID, token.MessageID, fmt.Sprint(token.ContextRevision), token.OldAssociationID, fmt.Sprint(token.OldAssociationRevision), token.OldAssociationDigest, token.SourceEventID, fmt.Sprint(token.SourceEventRevision), token.SourceDigest, token.Target.Kind, token.Target.ProjectID, fmt.Sprint(token.Target.ProjectRevision), token.Target.ProjectDigest, token.Target.ProjectTitle, homeProjectTokenDigest(encodedToken)}, "\x00")))
	operationKeyDigest := sha256Hex([]byte("project-chat-correction/v1\x00" + operationID))
	receiptOperationID := projectChatID("project_correction", snapshot.Organization.Header.ID, token.ThreadID, token.MessageID, operationID)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var storedFingerprint string
	var replacementProjectID, replacementAssociationID *string
	var replacementProjectRevision, replacementAssociationRevision *int64
	err = tx.QueryRow(ctx, `SELECT encode(request_fingerprint,'hex'),result_state,context_revision,old_association_id,old_association_revision,old_result_revision,replacement_project_id,replacement_project_revision,replacement_association_id,replacement_association_revision
FROM stride_project_chat_correction_receipts WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, receiptOperationID).
		Scan(&storedFingerprint, &result.Status, &result.ContextRevision, &result.OldAssociationID, &result.OldAssociationRevision, &result.OldResultRevision, &replacementProjectID, &replacementProjectRevision, &replacementAssociationID, &replacementAssociationRevision)
	if err == nil {
		if storedFingerprint != requestFingerprint {
			return result, ErrProjectAuthorityConflict
		}
		if replacementProjectID != nil {
			result.Status = "confirmed"
			result.ProjectID = *replacementProjectID
			result.ProjectRevision = *replacementProjectRevision
			result.AssociationID = *replacementAssociationID
			result.AssociationRevision = *replacementAssociationRevision
			if err := tx.QueryRow(ctx, `SELECT title FROM stride_project_revisions WHERE organization_id=$1 AND project_id=$2 AND revision=$3`, snapshot.Organization.Header.ID, result.ProjectID, result.ProjectRevision).Scan(&result.ProjectTitle); err != nil {
				return result, err
			}
		}
		return result, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, snapshot.Organization.Header.ID); err != nil {
		return result, err
	}
	var abandonedTokenDigest string
	err = tx.QueryRow(ctx, `SELECT encode(token_digest,'hex') FROM stride_project_chat_correction_abandonments WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, receiptOperationID).Scan(&abandonedTokenDigest)
	if err == nil {
		return result, ErrProjectAuthorityConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if err = syncProjectSessionAuthority(ctx, tx, snapshot); err != nil {
		return result, err
	}
	now := time.Now().UTC()
	var oldProjectID, oldBasis, oldClassifier string
	var oldProjectRevision, oldSourceACLRevision, oldConsentRevision, oldPurgeGeneration int64
	var oldConfidence float64
	var oldDigest, subjectDigest, evidenceDigest, sourceACLDigest string
	var sourceRefs, sourceAudience []byte
	err = tx.QueryRow(ctx, `SELECT association.project_id,association.project_revision,association.basis,association.classifier_revision,association.confidence,
encode(association.content_digest,'hex'),encode(association.subject_digest,'hex'),association.source_refs,encode(association.evidence_coverage_digest,'hex'),association.source_audience,
association.source_acl_revision,encode(association.source_acl_digest,'hex'),association.consent_revision,association.purge_generation
FROM stride_project_associations_authorized_current current_association
JOIN stride_project_association_revisions association ON association.association_id=current_association.association_id AND association.revision=current_association.revision AND association.organization_id=current_association.organization_id
JOIN stride_conversation_events source ON source.tenant_id=association.organization_id AND source.event_id=association.subject_id
WHERE current_association.organization_id=$1 AND current_association.association_id=$2 AND current_association.revision=$3 AND current_association.state='confirmed'
  AND encode(current_association.content_digest,'hex')=$4 AND association.subject_id=$5 AND association.subject_revision=$6 AND encode(association.subject_digest,'hex')=$7
  AND association.source_acl_revision=$8 AND encode(association.source_acl_digest,'hex')=$9 AND association.purge_generation=$10
  AND source.source_id=$11 AND source.thread_id=$12 AND source.author_principal=$13 AND source.invalidated_at IS NULL`, snapshot.Organization.Header.ID, token.OldAssociationID, token.OldAssociationRevision, token.OldAssociationDigest,
		token.SourceEventID, token.SourceEventRevision, token.SourceDigest, token.SourceACLRevision, token.SourceACLDigest, token.PurgeGeneration,
		token.MessageID, token.ThreadID, snapshot.Person.Header.ID).
		Scan(&oldProjectID, &oldProjectRevision, &oldBasis, &oldClassifier, &oldConfidence, &oldDigest, &subjectDigest, &sourceRefs, &evidenceDigest, &sourceAudience, &oldSourceACLRevision, &sourceACLDigest, &oldConsentRevision, &oldPurgeGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrProjectAuthorityConflict
	}
	if err != nil {
		return result, err
	}
	if token.Target.Kind == "project" {
		if token.Target.ProjectID == oldProjectID && token.Target.ProjectRevision == oldProjectRevision {
			return result, ErrProjectAuthorityConflict
		}
		err = tx.QueryRow(ctx, `SELECT revision.project_id,revision.revision,revision.title FROM stride_projects_current current_project JOIN stride_project_revisions revision ON revision.project_id=current_project.project_id AND revision.revision=current_project.revision
WHERE current_project.organization_id=$1 AND current_project.project_id=$2 AND current_project.revision=$3 AND current_project.content_digest=decode($4,'hex') AND current_project.lifecycle<>'archived'
  AND (revision.audience->'principals' @> jsonb_build_array($5::text) OR revision.controller_memberships @> jsonb_build_array(jsonb_build_object('contractType','organization_membership','id',$6::text,'revision',$7::bigint,'digest',$8::text)))`, snapshot.Organization.Header.ID, token.Target.ProjectID, token.Target.ProjectRevision, token.Target.ProjectDigest, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.Membership.Header.ContentDigest).
			Scan(&result.ProjectID, &result.ProjectRevision, &result.ProjectTitle)
		if errors.Is(err, pgx.ErrNoRows) {
			return result, errHomeProjectStale
		}
		if err != nil {
			return result, err
		}
		result.AssociationID = projectChatID("project_association_correction", snapshot.Organization.Header.ID, token.SourceEventID, result.ProjectID, receiptOperationID)
		result.AssociationRevision = 1
	}
	// The correction uses a freshly minted short-lived source authority receipt;
	// durable association validity remains tied to the canonical source row,
	// not to this receipt's wall-clock lifetime.
	receiptID := projectChatID("project_source_correction", receiptOperationID)
	sourceKey := sha256Hex([]byte("project-source-correction/v1\x00" + operationKeyDigest))
	sourceFingerprint := sha256Hex([]byte(subjectDigest + "\x00" + token.OldAssociationDigest + "\x00" + token.Target.ProjectDigest))
	_, err = tx.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
VALUES($1,$2,'conversation_event',$3,$4,decode($5,'hex'),$6::jsonb,decode($7,'hex'),$8::jsonb,$9,decode($10,'hex'),$11,$12,$13,$14,$15,decode($16,'hex'),$17,$18,decode($19,'hex'),decode($20,'hex'),$21,$22)`, receiptID, snapshot.Organization.Header.ID, token.SourceEventID, token.SourceEventRevision, subjectDigest, sourceRefs, evidenceDigest, sourceAudience, oldSourceACLRevision, sourceACLDigest, oldConsentRevision, oldPurgeGeneration,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, sourceKey, sourceFingerprint, now, now.Add(30*time.Minute))
	if err != nil {
		return result, err
	}
	oldResultState := "removed"
	oldResultDigest := sha256Hex([]byte(strings.Join([]string{"project-association/removed/v1", token.OldAssociationID, oldDigest, receiptOperationID}, "\x00")))
	var correctionID, replacementDigest string
	if token.Target.Kind == "project" {
		oldResultState = "corrected"
		replacementDigest = sha256Hex([]byte(strings.Join([]string{"project-association/correction-confirmed/v1", result.AssociationID, token.Target.ProjectDigest, subjectDigest, receiptOperationID}, "\x00")))
		oldResultDigest = sha256Hex([]byte(strings.Join([]string{"project-association/corrected/v1", token.OldAssociationID, oldDigest, result.AssociationID, replacementDigest}, "\x00")))
		correctionID = projectChatID("project_correction_receipt", receiptOperationID)
	}
	associationKey := sha256Hex([]byte("project-chat-correction-association/v1\x00" + operationKeyDigest))
	sharedCorrectionKey := associationKey
	sharedCorrectionFingerprint := requestFingerprint
	_, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,supersedes_revision,supersedes_digest,replacement_association_id,replacement_association_revision,replacement_association_digest,recorded_at,content_digest)
VALUES($1,$2,$3,$4,$5,'conversation_event',$6,$7,decode($8,'hex'),$9::jsonb,$10,decode($11,'hex'),$12,$13,$14,$15,$16,$17,$18,decode($19,'hex'),$20,$21,$22::jsonb,$23,decode($24,'hex'),$25,$26,decode($27,'hex'),$28,decode($29,'hex'),$30,$31,CASE WHEN $32='' THEN NULL ELSE decode($32,'hex') END,$33,decode($34,'hex'))`,
		token.OldAssociationID, result.OldResultRevision, snapshot.Organization.Header.ID, oldProjectID, oldProjectRevision, token.SourceEventID, token.SourceEventRevision, subjectDigest, sourceRefs, receiptID, evidenceDigest, oldResultState, oldBasis, oldClassifier, oldConfidence,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation,
		sourceAudience, oldSourceACLRevision, sourceACLDigest, oldConsentRevision, oldPurgeGeneration, associationKey, token.OldAssociationRevision, token.OldAssociationDigest,
		nullableString(result.AssociationID), nullableInt64(result.AssociationRevision), replacementDigest, now, oldResultDigest)
	if err != nil {
		return result, err
	}
	oldEventID := projectChatID("project_association_event", receiptOperationID, oldResultState)
	_, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,replacement_association_id,replacement_association_revision,replacement_association_digest,correction_id,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$4,$8,$9,CASE WHEN $10='' THEN NULL ELSE decode($10,'hex') END,$11,$12,$13,$14,decode($15,'hex'),$16,$17,decode($18,'hex'),decode($19,'hex'),$20)`, oldEventID, snapshot.Organization.Header.ID, token.OldAssociationID, result.OldResultRevision, map[string]string{"corrected": "correct", "removed": "remove"}[oldResultState], oldResultState, token.OldAssociationRevision,
		nullableString(result.AssociationID), nullableInt64(result.AssociationRevision), replacementDigest, nullableString(correctionID), snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation,
		sharedCorrectionKey, sharedCorrectionFingerprint, now)
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `UPDATE stride_project_associations_current SET revision=$1,state=$2,content_digest=decode($3,'hex'),updated_at=$4 WHERE organization_id=$5 AND association_id=$6 AND revision=$7 AND state='confirmed'`, result.OldResultRevision, oldResultState, oldResultDigest, now, snapshot.Organization.Header.ID, token.OldAssociationID, token.OldAssociationRevision); err != nil {
		return result, err
	}
	if token.Target.Kind == "project" {
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,recorded_at,content_digest)
VALUES($1,1,$2,$3,$4,'conversation_event',$5,$6,decode($7,'hex'),$8::jsonb,$9,decode($10,'hex'),'confirmed','selected','project_linker_v1',1,$11,$12,$13,decode($14,'hex'),$15,$16,$17::jsonb,$18,decode($19,'hex'),$20,$21,decode($22,'hex'),$23,decode($24,'hex'))`, result.AssociationID, snapshot.Organization.Header.ID, result.ProjectID, result.ProjectRevision, token.SourceEventID, token.SourceEventRevision, subjectDigest, sourceRefs, receiptID, evidenceDigest,
			snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, sourceAudience, oldSourceACLRevision, sourceACLDigest, oldConsentRevision, oldPurgeGeneration, sharedCorrectionKey, now, replacementDigest)
		if err != nil {
			return result, err
		}
		replacementEventID := projectChatID("project_association_event", receiptOperationID, "replacement")
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,correction_id,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
VALUES($1,$2,$3,1,'confirm','confirmed',0,1,$4,$5,$6,$7,decode($8,'hex'),$9,$10,decode($11,'hex'),decode($12,'hex'),$13)`, replacementEventID, snapshot.Organization.Header.ID, result.AssociationID, correctionID, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation,
			sharedCorrectionKey, sharedCorrectionFingerprint, now)
		if err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at) VALUES($1,1,$2,$3,'confirmed',decode($4,'hex'),$5)`, result.AssociationID, snapshot.Organization.Header.ID, result.ProjectID, replacementDigest, now); err != nil {
			return result, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_correction_receipts(correction_id,organization_id,old_association_id,old_association_revision,replacement_association_id,replacement_association_revision,old_event_id,replacement_event_id,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at)
VALUES($1,$2,$3,$4,$5,1,$6,$7,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),$16)`, correctionID, snapshot.Organization.Header.ID, token.OldAssociationID, result.OldResultRevision, result.AssociationID, oldEventID, replacementEventID,
			snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, sharedCorrectionKey, sharedCorrectionFingerprint, now)
		if err != nil {
			return result, err
		}
	}
	for _, family := range []string{"home", "work", "board", "project_record"} {
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at) VALUES($1,$2,$3,'unlist_old',$4,decode($5,'hex'),decode($6,'hex'),'pending',0,$7) ON CONFLICT DO NOTHING`, snapshot.Organization.Header.ID, token.OldAssociationID, result.OldResultRevision, family, subjectDigest, sha256Hex([]byte(snapshot.SessionHash+"\x00"+fmt.Sprint(snapshot.Generation))), now); err != nil {
			return result, err
		}
		if token.Target.Kind == "project" {
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at) VALUES($1,$2,1,'list_new',$3,decode($4,'hex'),decode($5,'hex'),'pending',0,$6) ON CONFLICT DO NOTHING`, snapshot.Organization.Header.ID, result.AssociationID, family, subjectDigest, sha256Hex([]byte(snapshot.SessionHash+"\x00"+fmt.Sprint(snapshot.Generation))), now); err != nil {
				return result, err
			}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_correction_receipts(organization_id,operation_id,operation_key_digest,request_fingerprint,token_digest,thread_id,message_id,source_event_id,source_event_revision,old_association_id,old_association_revision,old_result_revision,result_state,replacement_association_id,replacement_association_revision,replacement_project_id,replacement_project_revision,context_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,decode($3,'hex'),decode($4,'hex'),decode($5,'hex'),$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,decode($22,'hex'),$23,$24,$25)`, snapshot.Organization.Header.ID, receiptOperationID, operationKeyDigest, requestFingerprint, homeProjectTokenDigest(encodedToken), token.ThreadID, token.MessageID, token.SourceEventID, token.SourceEventRevision,
		token.OldAssociationID, token.OldAssociationRevision, result.OldResultRevision, oldResultState, nullableString(result.AssociationID), nullableInt64(result.AssociationRevision), nullableString(result.ProjectID), nullableInt64(result.ProjectRevision), result.ContextRevision,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, now)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (store *PostgresCanonicalStore) committedProjectChatCorrection(ctx context.Context, organizationID, actorPersonID, threadID string, operation scoutChatProjectCorrectionOperation) (confirmedProjectChatCorrection, bool, error) {
	var result confirmedProjectChatCorrection
	if store == nil || store.pool == nil || !strideIdentifier(organizationID) || !strideIdentifier(actorPersonID) ||
		!strideIdentifier(threadID) || !strideIdentifier(operation.OperationID) || !strideIdentifier(operation.MessageID) ||
		operation.State != "pending" || !isHexDigest(operation.TokenDigest) ||
		operation.OrganizationID != organizationID || operation.ActorPersonID != actorPersonID {
		return result, false, ErrProjectAuthorityInvalid
	}
	receiptOperationID := projectChatID("project_correction", organizationID, threadID, operation.MessageID, operation.OperationID)
	var tokenDigest, storedThreadID, storedMessageID, storedActorPersonID string
	var replacementProjectID, replacementAssociationID *string
	var replacementProjectRevision, replacementAssociationRevision *int64
	err := store.pool.QueryRow(ctx, `SELECT encode(token_digest,'hex'),thread_id,message_id,result_state,context_revision,old_association_id,old_association_revision,old_result_revision,replacement_project_id,replacement_project_revision,replacement_association_id,replacement_association_revision,actor_person_id
FROM stride_project_chat_correction_receipts WHERE organization_id=$1 AND operation_id=$2`, organizationID, receiptOperationID).
		Scan(&tokenDigest, &storedThreadID, &storedMessageID, &result.Status, &result.ContextRevision, &result.OldAssociationID, &result.OldAssociationRevision, &result.OldResultRevision, &replacementProjectID, &replacementProjectRevision, &replacementAssociationID, &replacementAssociationRevision, &storedActorPersonID)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if tokenDigest != operation.TokenDigest || storedThreadID != threadID || storedMessageID != operation.MessageID || storedActorPersonID != operation.ActorPersonID ||
		result.ContextRevision != operation.ExpectedContextRevision+1 || result.OldAssociationID == "" || result.OldAssociationRevision < 1 {
		return result, false, ErrProjectAuthorityConflict
	}
	if replacementProjectID != nil {
		if replacementProjectRevision == nil || replacementAssociationID == nil || replacementAssociationRevision == nil {
			return result, false, ErrProjectAuthorityConflict
		}
		result.Status = "confirmed"
		result.ProjectID = *replacementProjectID
		result.ProjectRevision = *replacementProjectRevision
		result.AssociationID = *replacementAssociationID
		result.AssociationRevision = *replacementAssociationRevision
		if err := store.pool.QueryRow(ctx, `SELECT title FROM stride_project_revisions WHERE organization_id=$1 AND project_id=$2 AND revision=$3`, organizationID, result.ProjectID, result.ProjectRevision).Scan(&result.ProjectTitle); err != nil {
			return result, false, err
		}
	}
	return result, true, nil
}

// abandonUncommittedProjectChatCorrection linearizes restart recovery against
// a concurrent exact PATCH. It holds the same organization advisory lock as
// correction commits, rechecks the immutable receipt, verifies the exact old
// source/association under current authority, and durably tombstones the
// operation before the legacy projection may be restored.
func (store *PostgresCanonicalStore) abandonUncommittedProjectChatCorrection(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, threadID string, operation scoutChatProjectCorrectionOperation) (confirmedProjectChatCorrection, bool, error) {
	var committed confirmedProjectChatCorrection
	if store == nil || store.pool == nil || operation.State != "pending" || operation.OrganizationID != snapshot.Organization.Header.ID ||
		operation.ActorPersonID != snapshot.Person.Header.ID || !strideIdentifier(threadID) || !strideIdentifier(operation.OperationID) ||
		!strideIdentifier(operation.MessageID) || !isHexDigest(operation.TokenDigest) ||
		!strideIdentifier(operation.ExpectedProject.AssociationID) || operation.ExpectedProject.AssociationRevision < 1 {
		return committed, false, ErrProjectAuthorityInvalid
	}
	receiptOperationID := projectChatID("project_correction", snapshot.Organization.Header.ID, threadID, operation.MessageID, operation.OperationID)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return committed, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, snapshot.Organization.Header.ID); err != nil {
		return committed, false, err
	}
	var storedTokenDigest string
	var replacementProjectID, replacementAssociationID *string
	var replacementProjectRevision, replacementAssociationRevision *int64
	err = tx.QueryRow(ctx, `SELECT encode(token_digest,'hex'),result_state,context_revision,old_association_id,old_association_revision,old_result_revision,replacement_project_id,replacement_project_revision,replacement_association_id,replacement_association_revision
FROM stride_project_chat_correction_receipts WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, receiptOperationID).
		Scan(&storedTokenDigest, &committed.Status, &committed.ContextRevision, &committed.OldAssociationID, &committed.OldAssociationRevision, &committed.OldResultRevision, &replacementProjectID, &replacementProjectRevision, &replacementAssociationID, &replacementAssociationRevision)
	if err == nil {
		if storedTokenDigest != operation.TokenDigest {
			return committed, false, ErrProjectAuthorityConflict
		}
		if replacementProjectID != nil {
			committed.Status = "confirmed"
			committed.ProjectID = *replacementProjectID
			committed.ProjectRevision = *replacementProjectRevision
			committed.AssociationID = *replacementAssociationID
			committed.AssociationRevision = *replacementAssociationRevision
			if err := tx.QueryRow(ctx, `SELECT title FROM stride_project_revisions WHERE organization_id=$1 AND project_id=$2 AND revision=$3`, snapshot.Organization.Header.ID, committed.ProjectID, committed.ProjectRevision).Scan(&committed.ProjectTitle); err != nil {
				return committed, false, err
			}
		}
		return committed, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return committed, false, err
	}
	var existingTokenDigest string
	err = tx.QueryRow(ctx, `SELECT encode(token_digest,'hex') FROM stride_project_chat_correction_abandonments WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, receiptOperationID).Scan(&existingTokenDigest)
	if err == nil {
		if existingTokenDigest != operation.TokenDigest {
			return committed, false, ErrProjectAuthorityConflict
		}
		return committed, false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return committed, false, err
	}
	if err = syncProjectSessionAuthority(ctx, tx, snapshot); err != nil {
		return committed, false, err
	}
	var sourceEventID string
	err = tx.QueryRow(ctx, `SELECT source.event_id
FROM stride_project_associations_authorized_current current_association
JOIN stride_project_association_revisions association ON association.organization_id=current_association.organization_id AND association.association_id=current_association.association_id AND association.revision=current_association.revision
JOIN stride_conversation_events source ON source.tenant_id=association.organization_id AND source.event_id=association.subject_id
WHERE current_association.organization_id=$1 AND current_association.association_id=$2 AND current_association.revision=$3 AND current_association.state='confirmed'
  AND source.thread_id=$4 AND source.source_id=$5 AND source.author_principal=$6 AND source.invalidated_at IS NULL`, snapshot.Organization.Header.ID, operation.ExpectedProject.AssociationID, operation.ExpectedProject.AssociationRevision, threadID, operation.MessageID, snapshot.Person.Header.ID).Scan(&sourceEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return committed, false, ErrProjectAuthorityConflict
	}
	if err != nil {
		return committed, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_correction_abandonments(organization_id,operation_id,token_digest,thread_id,message_id,old_association_id,old_association_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,decode($3,'hex'),$4,$5,$6,$7,$8,$9,$10,decode($11,'hex'),$12,$13,clock_timestamp())`, snapshot.Organization.Header.ID, receiptOperationID, operation.TokenDigest, threadID, operation.MessageID, operation.ExpectedProject.AssociationID, operation.ExpectedProject.AssociationRevision,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation)
	if err != nil {
		return committed, false, err
	}
	return committed, false, tx.Commit(ctx)
}

func hasPendingProjectCorrection(thread scoutChatThreadRecord) bool {
	for _, operation := range thread.ProjectCorrectionOperations {
		if operation.State == "pending" {
			return true
		}
	}
	return false
}

func (app *kanbanBoardApp) reconcileCommittedProjectCorrections(ctx context.Context, user *userAccount, threadID string) (scoutChatThreadRecord, error) {
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil || !hasPendingProjectCorrection(thread) {
		return thread, err
	}
	store := currentHomeProjectStore()
	if store == nil {
		return thread, errHomeProjectUnavailable
	}
	for _, operation := range thread.ProjectCorrectionOperations {
		if operation.State != "pending" {
			continue
		}
		result, found, loadErr := store.committedProjectChatCorrection(ctx, operation.OrganizationID, operation.ActorPersonID, threadID, operation)
		err = loadErr
		if err != nil {
			return thread, err
		}
		if !found {
			// The client deliberately does not persist the signed target token. If
			// the operation never reached PostgreSQL, prove the exact old
			// association and source are still current under the author's new
			// canonical session, then retire the abandoned journal and reopen the
			// chooser. Any missing/ambiguous authority leaves it unavailable.
			if user == nil || operation.ActorEmail != normalizeAccountEmail(user.Email) {
				continue
			}
			var committedDuringRecovery confirmedProjectChatCorrection
			var committedWon bool
			proofErr := withCurrentProjectCorrectionSession(ctx, func(snapshot StrideE10TenantAuthoritySnapshot) error {
				if snapshot.Organization.Header.ID != operation.OrganizationID || snapshot.Person.Header.ID != operation.ActorPersonID {
					return ErrProjectAuthorityDenied
				}
				var abandonErr error
				committedDuringRecovery, committedWon, abandonErr = store.abandonUncommittedProjectChatCorrection(ctx, snapshot, threadID, operation)
				return abandonErr
			})
			if proofErr != nil {
				continue
			}
			if committedWon {
				thread, err = app.finishCommittedScoutProjectCorrection(threadID, operation.MessageID, operation.OperationID, committedDuringRecovery)
				if err != nil {
					return thread, err
				}
				continue
			}
			if failErr := app.failScoutProjectCorrection(user, threadID, operation.MessageID, operation.OperationID, true); failErr != nil {
				return thread, failErr
			}
			thread, _, err = app.scoutChatThreadByID(user.Email, threadID)
			if err != nil {
				return thread, err
			}
			continue
		}
		thread, err = app.finishCommittedScoutProjectCorrection(threadID, operation.MessageID, operation.OperationID, result)
		if err != nil {
			return thread, err
		}
	}
	return thread, nil
}

// finishCommittedScoutProjectCorrection applies only an immutable PostgreSQL
// receipt to its exact server journal. It intentionally does not require the
// original interactive session: authority was consumed by the committed
// transaction, and session rotation must not leave stale Project truth visible.
func (app *kanbanBoardApp) finishCommittedScoutProjectCorrection(threadID, messageID, operationID string, result confirmedProjectChatCorrection) (scoutChatThreadRecord, error) {
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, threadID)
	if !ok {
		return scoutChatThreadRecord{}, ErrProjectAuthorityNotFound
	}
	thread, decoded := decodeScoutChatThreadEntry(entry)
	if !decoded {
		return scoutChatThreadRecord{}, ErrProjectAuthorityConflict
	}
	operationIndex := -1
	for index := range thread.ProjectCorrectionOperations {
		if thread.ProjectCorrectionOperations[index].OperationID == operationID && thread.ProjectCorrectionOperations[index].MessageID == messageID {
			operationIndex = index
			break
		}
	}
	if operationIndex < 0 {
		return thread, ErrProjectAuthorityConflict
	}
	operation := thread.ProjectCorrectionOperations[operationIndex]
	messageIndex := scoutChatMessageIndex(thread, messageID)
	if messageIndex < 0 || normalizeAccountEmail(thread.Messages[messageIndex].AuthorEmail) != operation.ActorEmail {
		return thread, ErrProjectAuthorityConflict
	}
	// Reuse the ordinary exact finalizer with the server-stamped author only;
	// it re-locks and re-reads before saving.
	return app.finishScoutProjectCorrection(&userAccount{Email: operation.ActorEmail}, threadID, messageID, operationID, result)
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableInt64(value int64) any {
	if value < 1 {
		return nil
	}
	return value
}

// invalidateProjectChatSourceForMutation is the replay-safe canonical half of
// a Project-linked edit/delete. The legacy thread journals the exact mutation
// first. This transaction then invalidates the body-free conversation source,
// synchronously hiding its association and queuing the four purge families,
// and records an immutable receipt before the legacy body is changed.
func invalidateProjectChatSourceForMutation(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord, operationID, requestDigest, kind string) (int64, error) {
	project := message.Project
	if project == nil || project.Status != "confirmed" || !strideIdentifier(project.AssociationID) || project.AssociationRevision < 1 {
		return 0, nil
	}
	if user == nil || !projectChatMessageAuthorOnly(thread, message, user) || !strideIdentifier(operationID) || !isHexDigest(requestDigest) || !oneOf(kind, "edit", "delete") {
		return 0, ErrProjectAuthorityDenied
	}
	store := currentHomeProjectStore()
	converter := currentStrideE10TenantRuntimeConverter()
	if store == nil || converter == nil {
		return 0, errHomeProjectUnavailable
	}
	resolver, ok := converter.resolver.(*strideE10MainTenantAuthorityResolver)
	if !ok || resolver == nil {
		return 0, errHomeProjectUnavailable
	}
	sessionHash := strideE10TenantSessionHashFromContext(ctx)
	if !validStrideE10SessionHash(sessionHash) {
		return 0, ErrStrideE10TenantAuthorityStale
	}
	var resultRevision int64
	err := resolver.WithCurrentTenantAuthority(ctx, StrideE10TenantSurfaceHTTP, sessionHash, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		dependentGroups, dependencyErr := store.projectChatReplyGroupsForParent(ctx, snapshot.Organization.Header.ID,
			projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, message.ID))
		if dependencyErr != nil {
			return dependencyErr
		}
		if groupID, members, groupErr := store.projectChatSourceGroupForAssociation(ctx, snapshot.Organization.Header.ID, project.AssociationID); groupErr != nil {
			return groupErr
		} else if groupID != "" && members > 0 {
			driftOperationID := projectChatID("project_source_group_mutation", snapshot.Organization.Header.ID, groupID, operationID)
			if invalidateErr := store.invalidateProjectChatRootGroupForMutation(ctx, snapshot.Organization.Header.ID, groupID,
				driftOperationID, map[string]string{"edit": "source_edited", "delete": "source_deleted"}[kind]); invalidateErr != nil {
				return invalidateErr
			}
			for _, dependentGroupID := range dependentGroups {
				if driftErr := store.invalidateProjectChatReplyGroupForDrift(ctx, snapshot.Organization.Header.ID, dependentGroupID,
					projectChatID("project_reply_source_mutation", snapshot.Organization.Header.ID, dependentGroupID, operationID),
					map[string]string{"edit": "parent_edited", "delete": "parent_deleted"}[kind]); driftErr != nil {
					return driftErr
				}
			}
			resultRevision = project.AssociationRevision + 1
			return nil
		}
		var invalidateErr error
		resultRevision, invalidateErr = store.invalidateProjectChatSourceForMutation(ctx, snapshot, thread, message, operationID, requestDigest, kind)
		return invalidateErr
	})
	return resultRevision, err
}

func (store *PostgresCanonicalStore) invalidateProjectChatSourceForMutation(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, thread scoutChatThreadRecord, message scoutChatMessageRecord, operationID, requestDigest, kind string) (int64, error) {
	project := message.Project
	if store == nil || store.pool == nil || project == nil || project.Status != "confirmed" || !strideIdentifier(project.AssociationID) || project.AssociationRevision < 1 || !strideIdentifier(operationID) || !isHexDigest(requestDigest) || !oneOf(kind, "edit", "delete") {
		return 0, ErrProjectAuthorityInvalid
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	receiptOperationID := projectChatID("project_source_mutation", snapshot.Organization.Header.ID, thread.ID, message.ID, operationID)
	operationKeyDigest := sha256Hex([]byte("project-chat-source-mutation/v1\x00" + operationID))
	requestFingerprint := sha256Hex([]byte(strings.Join([]string{requestDigest, kind, thread.ID, message.ID, project.AssociationID, fmt.Sprint(project.AssociationRevision)}, "\x00")))
	var resultRevision, storedAssociationRevision int64
	var storedFingerprint, storedKind, storedThreadID, storedMessageID, storedAssociationID string
	err = tx.QueryRow(ctx, `SELECT encode(request_fingerprint,'hex'),mutation_kind,thread_id,message_id,source_result_revision,association_id,association_revision
FROM stride_project_chat_source_mutation_receipts WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, receiptOperationID).
		Scan(&storedFingerprint, &storedKind, &storedThreadID, &storedMessageID, &resultRevision, &storedAssociationID, &storedAssociationRevision)
	if err == nil {
		if storedFingerprint != requestFingerprint || storedKind != kind || storedThreadID != thread.ID || storedMessageID != message.ID || storedAssociationID != project.AssociationID || storedAssociationRevision != project.AssociationRevision {
			return 0, ErrProjectAuthorityConflict
		}
		return resultRevision, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, snapshot.Organization.Header.ID); err != nil {
		return 0, err
	}
	if err = syncProjectSessionAuthority(ctx, tx, snapshot); err != nil {
		return 0, err
	}
	var eventID string
	var sourcePriorRevision int64
	err = tx.QueryRow(ctx, `SELECT source.event_id,source.content_revision
FROM stride_project_associations_authorized_current current_association
JOIN stride_project_association_revisions association ON association.association_id=current_association.association_id AND association.revision=current_association.revision AND association.organization_id=current_association.organization_id
JOIN stride_conversation_events source ON source.tenant_id=association.organization_id AND source.event_id=association.subject_id
WHERE current_association.organization_id=$1 AND current_association.association_id=$2 AND current_association.revision=$3 AND current_association.state='confirmed'
  AND source.thread_id=$4 AND source.source_id=$5 AND source.author_principal=$6 AND source.invalidated_at IS NULL`, snapshot.Organization.Header.ID, project.AssociationID, project.AssociationRevision, thread.ID, message.ID, snapshot.Person.Header.ID).Scan(&eventID, &sourcePriorRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrProjectAuthorityConflict
	}
	if err != nil {
		return 0, err
	}
	invalidationReason := kind + ":" + receiptOperationID
	err = tx.QueryRow(ctx, `UPDATE stride_conversation_events SET content_revision=content_revision+1,purge_generation=purge_generation+1,invalidated_at=clock_timestamp(),invalidation_reason=$1,event_type=$2
WHERE tenant_id=$3 AND event_id=$4 AND content_revision=$5 AND invalidated_at IS NULL RETURNING content_revision`, invalidationReason, kind, snapshot.Organization.Header.ID, eventID, sourcePriorRevision).Scan(&resultRevision)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_mutation_receipts(organization_id,operation_id,operation_key_digest,request_fingerprint,mutation_kind,thread_id,message_id,source_event_id,source_prior_revision,source_result_revision,association_id,association_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,decode($3,'hex'),decode($4,'hex'),$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,decode($16,'hex'),$17,$18,clock_timestamp())`, snapshot.Organization.Header.ID, receiptOperationID, operationKeyDigest, requestFingerprint, kind, thread.ID, message.ID, eventID, sourcePriorRevision, resultRevision, project.AssociationID, project.AssociationRevision,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return resultRevision, nil
}

// committedProjectChatSourceMutation reads only an immutable receipt. It is a
// recovery operation, not a new authority decision, so it must remain usable
// after the interactive session rotates or is revoked. The exact organization,
// actor, thread, message, request, association and operation are all bound by
// the server journal and the database truth trigger.
func (store *PostgresCanonicalStore) committedProjectChatSourceMutation(ctx context.Context, organizationID, actorPersonID, threadID string, operation scoutChatProjectSourceMutationOperation) (int64, bool, error) {
	if store == nil || store.pool == nil || !strideIdentifier(organizationID) || !strideIdentifier(actorPersonID) ||
		operation.OrganizationID != organizationID || operation.ActorPersonID != actorPersonID ||
		!strideIdentifier(threadID) || !strideIdentifier(operation.OperationID) || !strideIdentifier(operation.MessageID) ||
		!isHexDigest(operation.RequestDigest) || !oneOf(operation.Kind, "edit", "delete") || operation.State != "pending" ||
		!strideIdentifier(operation.ExpectedProject.AssociationID) || operation.ExpectedProject.AssociationRevision < 1 {
		return 0, false, ErrProjectAuthorityInvalid
	}
	receiptOperationID := projectChatID("project_source_mutation", organizationID, threadID, operation.MessageID, operation.OperationID)
	requestFingerprint := sha256Hex([]byte(strings.Join([]string{operation.RequestDigest, operation.Kind, threadID, operation.MessageID, operation.ExpectedProject.AssociationID, fmt.Sprint(operation.ExpectedProject.AssociationRevision)}, "\x00")))
	var storedFingerprint, storedKind, storedThreadID, storedMessageID, storedAssociationID, storedActorPersonID string
	var resultRevision, storedAssociationRevision int64
	err := store.pool.QueryRow(ctx, `SELECT encode(request_fingerprint,'hex'),mutation_kind,thread_id,message_id,source_result_revision,association_id,association_revision,actor_person_id
FROM stride_project_chat_source_mutation_receipts WHERE organization_id=$1 AND operation_id=$2`, organizationID, receiptOperationID).
		Scan(&storedFingerprint, &storedKind, &storedThreadID, &storedMessageID, &resultRevision, &storedAssociationID, &storedAssociationRevision, &storedActorPersonID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if storedFingerprint != requestFingerprint || storedKind != operation.Kind || storedThreadID != threadID ||
		storedMessageID != operation.MessageID || storedAssociationID != operation.ExpectedProject.AssociationID ||
		storedAssociationRevision != operation.ExpectedProject.AssociationRevision || storedActorPersonID != actorPersonID || resultRevision < 2 {
		return 0, false, ErrProjectAuthorityConflict
	}
	return resultRevision, true, nil
}

func firstError(err error, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
