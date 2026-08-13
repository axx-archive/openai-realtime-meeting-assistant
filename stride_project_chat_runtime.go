package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type confirmedProjectChatLink struct {
	ProjectID           string
	ProjectRevision     int64
	ProjectDigest       string
	ProjectTitle        string
	AssociationID       string
	AssociationRevision int64
}

type projectChatSourceAuthority struct {
	SourceType  string
	Visibility  string
	Principals  []string
	ACLRevision int64
}

func privateProjectChatSourceAuthority(personID string) projectChatSourceAuthority {
	return projectChatSourceAuthority{SourceType: "private_chat_message", Visibility: "private", Principals: []string{personID}, ACLRevision: 1}
}

func (v projectChatSourceAuthority) validate(actorPersonID string) error {
	if !oneOf(v.SourceType, "private_chat_message", "channel_chat_message") ||
		!oneOf(v.Visibility, "private", "project", "channel") || v.ACLRevision < 1 || !strideIdentifier(actorPersonID) {
		return ErrProjectAuthorityInvalid
	}
	seen := map[string]bool{}
	foundActor := false
	for _, principal := range v.Principals {
		if !strideIdentifier(principal) || seen[principal] {
			return ErrProjectAuthorityInvalid
		}
		seen[principal] = true
		foundActor = foundActor || principal == actorPersonID
	}
	if !foundActor || len(v.Principals) == 0 || (v.Visibility == "private" && len(v.Principals) != 1) {
		return ErrProjectAuthorityDenied
	}
	return nil
}

func projectChatID(prefix string, parts ...string) string {
	return prefix + "_" + sha256Hex([]byte(strings.Join(parts, "\x00")))[:32]
}

// projectChatSourceAuthorityForThread resolves the current canonical people
// behind the legacy chat audience while holding the Project transaction's
// organization boundary. Missing mappings fail only Project linking; they can
// never widen the ordinary chat audience or manufacture Person ids from email.
func (store *PostgresCanonicalStore) projectChatSourceAuthorityForThread(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, thread scoutChatThreadRecord) (projectChatSourceAuthority, error) {
	if store == nil || store.pool == nil || !scoutChatThreadAllowsViewer(thread, snapshot.Session.Email) {
		return projectChatSourceAuthority{}, ErrProjectAuthorityDenied
	}
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPrivate {
		authority := privateProjectChatSourceAuthority(snapshot.Person.Header.ID)
		return authority, authority.validate(snapshot.Person.Header.ID)
	}
	authority := projectChatSourceAuthority{SourceType: "channel_chat_message", ACLRevision: 1}
	if scoutChatThreadIsOrganizationPublic(thread) {
		authority.Visibility = "channel"
		rows, err := store.pool.Query(ctx, `SELECT person_id FROM stride_organization_memberships_current WHERE organization_id=$1 AND status='active' ORDER BY person_id`, snapshot.Organization.Header.ID)
		if err != nil {
			return authority, err
		}
		defer rows.Close()
		for rows.Next() {
			var personID string
			if err := rows.Scan(&personID); err != nil {
				return authority, err
			}
			authority.Principals = append(authority.Principals, personID)
		}
		if err := rows.Err(); err != nil {
			return authority, err
		}
	} else {
		authority.Visibility = "project"
		for _, email := range scoutChatThreadMemberEmails(thread) {
			accountDigest := sha256Hex([]byte(normalizeAccountEmail(email)))
			var personID string
			err := store.pool.QueryRow(ctx, `SELECT mapping.person_id
FROM stride_account_person_mappings_current mapping
JOIN stride_organization_memberships_current membership ON membership.person_id=mapping.person_id
WHERE mapping.account_subject_digest=decode($1,'hex') AND mapping.status='active'
  AND membership.organization_id=$2 AND membership.status='active'`, accountDigest, snapshot.Organization.Header.ID).Scan(&personID)
			if errors.Is(err, pgx.ErrNoRows) {
				return authority, ErrProjectAuthorityDenied
			}
			if err != nil {
				return authority, err
			}
			authority.Principals = append(authority.Principals, personID)
		}
		sort.Strings(authority.Principals)
	}
	return authority, authority.validate(snapshot.Person.Header.ID)
}

func (app *kanbanBoardApp) reconcileScoutHomeProjectLink(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, key, text string, token homeProjectContextToken) (scoutChatThreadRecord, error) {
	if user == nil || thread.ID == "" || thread.OpeningOperation == nil || token.Kind == "" {
		return thread, ErrProjectAuthorityInvalid
	}
	return app.reconcileScoutProjectLink(ctx, user, thread, thread.OpeningOperation.OperationID, thread.OpeningOperation.UserMessageID, thread.OpeningOperation.ReplyMessageID, key, text, token)
}

func (app *kanbanBoardApp) reconcileScoutProjectLink(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, operationID, messageID, replyMessageID, key, text string, token homeProjectContextToken) (scoutChatThreadRecord, error) {
	return app.reconcileScoutProjectLinkWithManifest(ctx, user, thread, operationID, messageID, replyMessageID, key, text,
		conversationProjectLinkBinding{Token: token})
}

func (app *kanbanBoardApp) reconcileScoutProjectLinkWithManifest(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, operationID, messageID, replyMessageID, key, text string, binding conversationProjectLinkBinding) (scoutChatThreadRecord, error) {
	token := binding.Token
	if user == nil || thread.ID == "" || !strideIdentifier(operationID) || !strideIdentifier(messageID) || token.Kind == "" {
		return thread, ErrProjectAuthorityInvalid
	}
	store := currentHomeProjectStore()
	if store == nil || !homeProjectFeatureEnabled(STRIDEFeatureProjectAuthorityWrite) {
		return thread, errHomeProjectUnavailable
	}
	var link confirmedProjectChatLink
	var err error
	var replySourceLock *sync.Mutex
	if binding.Manifest.Version == projectChatSourceManifestV3 && binding.Manifest.Reply != nil {
		replySourceLock = app.scoutChatThreadLock(thread.ID)
		replySourceLock.Lock()
		freshThread, _, freshErr := app.scoutChatThreadByID(user.Email, thread.ID)
		if freshErr != nil || !app.projectChatReplyMediaManifestCurrent(user.Email, freshThread, binding.Manifest.Reply) {
			replySourceLock.Unlock()
			return thread, errHomeProjectStale
		}
		thread = freshThread
	}
	if replySourceLock != nil {
		defer func() {
			if replySourceLock != nil {
				replySourceLock.Unlock()
			}
		}()
	}
	if homeProjectTokenHasSourceManifest(token.Version) {
		link, _, err = store.committedProjectChatSendWithManifest(ctx, token, thread, messageID, key, text, binding.Manifest)
	}
	if err != nil {
		return thread, err
	}
	if replySourceLock != nil {
		replySourceLock.Unlock()
		replySourceLock = nil
	}
	if link.AssociationID == "" {
		err = withCurrentHomeProjectAuthorityRequestContext(ctx, token, func(snapshot StrideE10TenantAuthoritySnapshot) error {
			sourceAuthority, sourceErr := store.projectChatSourceAuthorityForThread(ctx, snapshot, thread)
			if sourceErr != nil {
				return sourceErr
			}
			var linkErr error
			if homeProjectTokenHasSourceManifest(token.Version) {
				link, linkErr = store.confirmProjectChatSendWithManifest(ctx, snapshot, thread, messageID, key, text, token, sourceAuthority, &binding.Manifest)
			} else {
				link, linkErr = store.confirmProjectChatSend(ctx, snapshot, thread, messageID, key, text, token, sourceAuthority)
			}
			return linkErr
		})
	}
	if err != nil {
		return thread, err
	}
	var groupAssociationIDs []string
	if homeProjectTokenHasSourceManifest(token.Version) {
		groupID := projectChatID("project_source_group", token.OrganizationID, thread.ID, messageID, key)
		groupAssociationIDs, err = store.projectChatSourceGroupAssociationIDs(ctx, token.OrganizationID, groupID)
		if err != nil || len(groupAssociationIDs) != len(binding.Manifest.Attachments)+1 || groupAssociationIDs[0] != link.AssociationID {
			return thread, ErrProjectAuthorityConflict
		}
	}
	lock := app.scoutChatThreadLock(thread.ID)
	lock.Lock()
	defer lock.Unlock()
	current, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		return thread, err
	}
	found := false
	alreadyConfirmed := false
	for index := range current.ProjectLinkOperations {
		operation := &current.ProjectLinkOperations[index]
		if operation.OperationID != operationID {
			continue
		}
		alreadyConfirmed = operation.State == "confirmed"
		if alreadyConfirmed && (operation.ProjectID != link.ProjectID || operation.ProjectRevision != link.ProjectRevision ||
			operation.ProjectDigest != link.ProjectDigest || operation.ProjectTitle != link.ProjectTitle || operation.AssociationID != link.AssociationID) {
			return thread, ErrProjectAuthorityConflict
		}
		operation.State = "confirmed"
		operation.ProjectID = link.ProjectID
		operation.ProjectRevision = link.ProjectRevision
		operation.ProjectDigest = link.ProjectDigest
		operation.ProjectTitle = link.ProjectTitle
		operation.AssociationID = link.AssociationID
		if homeProjectTokenHasSourceManifest(token.Version) {
			operation.SourceManifestDigest = binding.Manifest.Digest
			operation.SourceManifestVersion = binding.Manifest.Version
			operation.SourceGroupID = projectChatID("project_source_group", token.OrganizationID, thread.ID, messageID, key)
			operation.AssociationIDs = append([]string(nil), groupAssociationIDs...)
		}
		found = true
	}
	messageIndex := scoutChatMessageIndex(current, messageID)
	if !found || messageIndex < 0 {
		return thread, ErrProjectAuthorityConflict
	}
	if alreadyConfirmed {
		project := current.Messages[messageIndex].Project
		if project == nil || project.Status != "confirmed" || project.ProjectID != link.ProjectID || project.ProjectRevision != link.ProjectRevision || project.Title != link.ProjectTitle || project.AssociationID != link.AssociationID || project.AssociationRevision != link.AssociationRevision {
			return thread, ErrProjectAuthorityConflict
		}
		return current, nil
	}
	current.Messages[messageIndex].Project = &scoutChatProjectContext{
		Status: "confirmed", ContextRevision: 1, ProjectID: link.ProjectID, ProjectRevision: link.ProjectRevision,
		Title: link.ProjectTitle, Basis: token.Basis, AssociationID: link.AssociationID, AssociationRevision: link.AssociationRevision,
	}
	if replyMessageID != "" {
		replyIndex := scoutChatMessageIndex(current, replyMessageID)
		if replyIndex < 0 || current.Messages[replyIndex].Reply == nil || current.Messages[replyIndex].Reply.State != scoutReplyStateProjectPending {
			return thread, ErrProjectAuthorityConflict
		}
		current.Messages[replyIndex].Reply.State = scoutReplyStateQueued
	}
	if err := app.saveScoutChatThread(current); err != nil {
		return thread, err
	}
	return current, nil
}

func (store *PostgresCanonicalStore) committedProjectChatSendWithManifest(ctx context.Context, token homeProjectContextToken,
	thread scoutChatThreadRecord, messageID, operationKey, text string, manifest projectChatSourceManifest) (confirmedProjectChatLink, bool, error) {
	var result confirmedProjectChatLink
	if store == nil || store.pool == nil || !homeProjectTokenHasSourceManifest(token.Version) || !strideIdentifier(token.OrganizationID) ||
		!strideIdentifier(thread.ID) || !strideIdentifier(messageID) || manifest.Version != projectChatManifestVersionForToken(token.Version) || manifest.Digest != token.SourceManifestDigest ||
		manifest.Digest != projectChatManifestDigest(manifest) || manifest.TextDigest != sha256Hex([]byte(strings.TrimSpace(text))) {
		return result, false, nil
	}
	operationID := projectChatID("project_send", token.OrganizationID, thread.ID, messageID)
	groupID := projectChatID("project_source_group", token.OrganizationID, thread.ID, messageID, operationKey)
	var actorPersonID, storedManifest string
	err := store.pool.QueryRow(ctx, `SELECT receipt.project_id,receipt.project_revision,project.title,encode(project.content_digest,'hex'),
receipt.association_id,receipt.association_revision,receipt.actor_person_id,encode(source_group.source_manifest_digest,'hex')
FROM stride_project_chat_send_receipts receipt
JOIN stride_project_revisions project ON project.organization_id=receipt.organization_id AND project.project_id=receipt.project_id AND project.revision=receipt.project_revision
JOIN stride_project_chat_source_groups source_group ON source_group.organization_id=receipt.organization_id AND source_group.group_id=$3
WHERE receipt.organization_id=$1 AND receipt.operation_id=$2 AND receipt.thread_id=$4 AND receipt.message_id=$5 AND receipt.status='confirmed'
 AND source_group.conversation_event_id=receipt.conversation_event_id AND source_group.root_association_id=receipt.association_id
 AND source_group.source_manifest_version=$6`,
		token.OrganizationID, operationID, groupID, thread.ID, messageID, manifest.Version).Scan(&result.ProjectID, &result.ProjectRevision, &result.ProjectTitle,
		&result.ProjectDigest, &result.AssociationID, &result.AssociationRevision, &actorPersonID, &storedManifest)
	if errors.Is(err, pgx.ErrNoRows) {
		return confirmedProjectChatLink{}, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if actorPersonID != token.PersonID || storedManifest != manifest.Digest || result.ProjectID != token.ProjectID ||
		result.ProjectRevision != token.ProjectRevision || result.ProjectDigest != token.ProjectDigest || result.ProjectTitle != token.ProjectTitle {
		return confirmedProjectChatLink{}, false, ErrProjectAuthorityConflict
	}
	return result, true, nil
}

func scoutHomeProjectOperationState(thread scoutChatThreadRecord, operationID string) string {
	for _, operation := range thread.ProjectLinkOperations {
		if operation.OperationID == operationID {
			return operation.State
		}
	}
	return ""
}

func (app *kanbanBoardApp) acceptedScoutProjectTurnRetry(user *userAccount, threadID, operationID, bodyDigest, tokenDigest string) bool {
	if app == nil || user == nil || !strideIdentifier(threadID) || !strideIdentifier(operationID) || !isHexDigest(bodyDigest) || !isHexDigest(tokenDigest) {
		return false
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return false
	}
	for _, operation := range thread.ProjectLinkOperations {
		if operation.OperationID != operationID || operation.TokenDigest != tokenDigest || !oneOf(operation.State, "pending", "confirmed", "drift_pending", "drifted") {
			continue
		}
		messageIndex := scoutChatMessageIndex(thread, operation.MessageID)
		return messageIndex >= 0 && normalizeAccountEmail(thread.Messages[messageIndex].AuthorEmail) == normalizeAccountEmail(user.Email) &&
			thread.Messages[messageIndex].SourceOperationID == operationID && thread.Messages[messageIndex].SourceOperationDigest == bodyDigest
	}
	return false
}

// acceptedScoutProjectTurnCanonicalResume is the narrow exception that lets an
// exact, already-confirmed operation resume provider admission after its
// interactive session rotates. It proves both durable stores and current
// Project/source/thread authority; a merely pending journal can never use it.
func (app *kanbanBoardApp) acceptedScoutProjectTurnCanonicalResume(ctx context.Context, user *userAccount,
	snapshot StrideE10TenantAuthoritySnapshot, threadID, operationID, bodyDigest, tokenDigest, text string,
	token homeProjectContextToken, manifest projectChatSourceManifest) (bool, error) {
	if app == nil || user == nil || !homeProjectTokenHasSourceManifest(token.Version) || token.Kind != "project" ||
		!strideIdentifier(threadID) || !strideIdentifier(operationID) || !isHexDigest(bodyDigest) || !isHexDigest(tokenDigest) ||
		token.PersonID != snapshot.Person.Header.ID || token.OrganizationID != snapshot.Organization.Header.ID ||
		token.MembershipID != snapshot.Membership.Header.ID || manifest.Digest != token.SourceManifestDigest {
		return false, nil
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil || thread.ArchivedAt != "" {
		return false, err
	}
	var operation *scoutChatProjectLinkOperation
	for index := range thread.ProjectLinkOperations {
		candidate := &thread.ProjectLinkOperations[index]
		if candidate.OperationID == operationID {
			operation = candidate
			break
		}
	}
	if operation == nil || operation.State != "confirmed" || operation.TokenDigest != tokenDigest ||
		operation.ProjectKind != token.Kind || operation.ProjectID != token.ProjectID || operation.ProjectRevision != token.ProjectRevision ||
		operation.ProjectDigest != token.ProjectDigest || operation.ProjectTitle != token.ProjectTitle || operation.Basis != token.Basis ||
		operation.SourceManifestDigest != manifest.Digest || operation.SourceGroupID == "" {
		return false, nil
	}
	messageIndex := scoutChatMessageIndex(thread, operation.MessageID)
	if messageIndex < 0 {
		return false, nil
	}
	message := thread.Messages[messageIndex]
	if normalizeAccountEmail(message.AuthorEmail) != normalizeAccountEmail(user.Email) || message.SourceOperationID != operationID ||
		message.SourceOperationDigest != bodyDigest || message.Project == nil || message.Project.Status != "confirmed" ||
		message.Project.ProjectID != token.ProjectID || message.Project.ProjectRevision != token.ProjectRevision ||
		message.Project.AssociationID != operation.AssociationID || !projectChatManifestMatchesFiles(manifest, message.Files, func() string {
		if message.ReplyTo == nil {
			return ""
		}
		return message.ReplyTo.MessageID
	}()) {
		return false, nil
	}
	store := currentHomeProjectStore()
	if store == nil {
		return false, errHomeProjectUnavailable
	}
	link, committed, err := store.committedProjectChatSendWithManifest(ctx, token, thread, message.ID, operationID, text, manifest)
	if err != nil || !committed {
		return false, err
	}
	if link.AssociationID != operation.AssociationID || link.AssociationRevision != message.Project.AssociationRevision {
		return false, ErrProjectAuthorityConflict
	}
	wantGroupID := projectChatID("project_source_group", token.OrganizationID, thread.ID, message.ID, operationID)
	if operation.SourceGroupID != wantGroupID {
		return false, ErrProjectAuthorityConflict
	}
	if err := store.projectChatSourceGroupFreshAuthority(ctx, snapshot, thread, token, wantGroupID, len(manifest.Attachments)+1); err != nil {
		return false, err
	}
	return true, nil
}

func (app *kanbanBoardApp) beginScoutExistingProjectTurn(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord, operation conversationTurnOperation, binding conversationProjectLinkBinding) (scoutChatThreadRecord, scoutChatMessageRecord, bool, error) {
	if app == nil || user == nil || !strideIdentifier(thread.ID) || !strideIdentifier(message.ID) || !strideIdentifier(operation.ID) || !isHexDigest(operation.BodyDigest) || binding.Token.Kind != "project" || binding.Token.Destination != (homeProjectDestination{Route: "thread", ThreadID: thread.ID}) {
		return thread, message, false, ErrProjectAuthorityInvalid
	}
	manifestDigest := ""
	var attachmentSources []scoutChatProjectAttachmentSource
	var replySource *scoutChatProjectReplySource
	if homeProjectTokenHasSourceManifest(binding.Token.Version) {
		if binding.Manifest.Version != projectChatManifestVersionForToken(binding.Token.Version) || !isHexDigest(binding.Manifest.Digest) ||
			binding.Token.SourceManifestDigest != binding.Manifest.Digest || !projectChatManifestMatchesFiles(binding.Manifest, message.Files, func() string {
			if message.ReplyTo == nil {
				return ""
			}
			return message.ReplyTo.MessageID
		}()) {
			return thread, message, false, ErrProjectAuthorityConflict
		}
		manifestDigest = binding.Manifest.Digest
		attachmentSources, replySource = projectChatManifestJournal(binding.Manifest)
	} else if len(message.Files) != 0 || message.ReplyTo != nil {
		return thread, message, false, ErrProjectAuthorityConflict
	}
	sourceGroupID := ""
	if manifestDigest != "" {
		sourceGroupID = projectChatID("project_source_group", binding.Token.OrganizationID, thread.ID, message.ID, operation.ID)
	}
	lock := app.scoutChatThreadLock(thread.ID)
	lock.Lock()
	defer lock.Unlock()
	current, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || current.ArchivedAt != "" {
		if err == nil {
			err = errHomeProjectStale
		}
		return thread, message, false, err
	}
	tokenDigest := homeProjectTokenDigest(binding.EncodedToken)
	for _, existing := range current.ProjectLinkOperations {
		if existing.OperationID != operation.ID {
			continue
		}
		existingManifestVersion := existing.SourceManifestVersion
		if existingManifestVersion == 0 && existing.SourceManifestDigest != "" {
			existingManifestVersion = projectChatSourceManifestVersion
		}
		if existing.TokenDigest != tokenDigest || existing.MessageID != message.ID || existing.ProjectKind != binding.Token.Kind || existing.ProjectID != binding.Token.ProjectID || existing.ProjectRevision != binding.Token.ProjectRevision || existing.ProjectDigest != binding.Token.ProjectDigest || existing.ProjectTitle != binding.Token.ProjectTitle || existing.Basis != binding.Token.Basis ||
			existing.SourceManifestDigest != manifestDigest || existingManifestVersion != binding.Manifest.Version || existing.SourceGroupID != sourceGroupID {
			return thread, message, false, ErrProjectAuthorityConflict
		}
		messageIndex := scoutChatMessageIndex(current, message.ID)
		if messageIndex < 0 || current.Messages[messageIndex].SourceOperationDigest != operation.BodyDigest {
			return thread, message, false, ErrProjectAuthorityConflict
		}
		return current, current.Messages[messageIndex], false, nil
	}
	if scoutChatMessageIndex(current, message.ID) >= 0 {
		return thread, message, false, ErrProjectAuthorityConflict
	}
	message.Project = &scoutChatProjectContext{Status: "pending", ContextRevision: 1, ProjectID: binding.Token.ProjectID, ProjectRevision: binding.Token.ProjectRevision, Title: binding.Token.ProjectTitle, Basis: binding.Token.Basis}
	current.ProjectLinkOperations = append(current.ProjectLinkOperations, scoutChatProjectLinkOperation{
		OperationID: operation.ID, TokenDigest: tokenDigest, MessageID: message.ID, State: "pending", ProjectKind: binding.Token.Kind,
		ProjectID: binding.Token.ProjectID, ProjectRevision: binding.Token.ProjectRevision, ProjectDigest: binding.Token.ProjectDigest,
		ProjectTitle: binding.Token.ProjectTitle, Basis: binding.Token.Basis, SourceManifestDigest: manifestDigest, SourceManifestVersion: binding.Manifest.Version, SourceGroupID: sourceGroupID,
		AttachmentSources: attachmentSources, ReplySource: replySource, ReservationID: message.attachmentReservationID,
	})
	current.Messages = append(current.Messages, message)
	updateScoutChatThreadSummary(&current, message, scoutChatMessageRecord{})
	if err := app.saveScoutChatThread(current); err != nil {
		return thread, message, false, err
	}
	deliverScoutChatThreadUpdateWithContext(ctx, current, message)
	if scoutChatThreadVisibility(current) == scoutChatVisibilityPublic {
		// The durable human message is immediately part of deterministic
		// continuity, but the public Product/proactive observers stay fenced
		// until the canonical Project association has confirmed.
		if _, _, continuityErr := app.rebuildConversationContinuity(current, "message"); continuityErr != nil {
			log.Errorf("ConversationContinuity rebuild unavailable: %v", continuityErr)
		}
	} else {
		app.rebuildPrivateConversationContinuity(current, "message")
	}
	return current, message, true, nil
}

func scoutHomeProjectTerminalError(err error) bool {
	return errors.Is(err, errHomeProjectStale) || errors.Is(err, ErrProjectAuthorityDenied) ||
		errors.Is(err, ErrProjectAuthorityInvalid) || errors.Is(err, ErrProjectAuthorityConflict) ||
		errors.Is(err, ErrStrideE10TenantAuthorityStale) ||
		strings.Contains(strings.ToLower(err.Error()), "requires current organization session authority")
}

func (app *kanbanBoardApp) failScoutHomeProjectLink(user *userAccount, thread scoutChatThreadRecord, cause error) (scoutChatThreadRecord, error) {
	if app == nil || user == nil || thread.OpeningOperation == nil || !scoutHomeProjectTerminalError(cause) {
		return thread, cause
	}
	return app.failScoutProjectLink(user, thread, thread.OpeningOperation.OperationID, thread.OpeningOperation.UserMessageID, thread.OpeningOperation.ReplyMessageID, cause)
}

func (app *kanbanBoardApp) failScoutProjectLink(user *userAccount, thread scoutChatThreadRecord, operationID, messageID, replyMessageID string, cause error) (scoutChatThreadRecord, error) {
	if app == nil || user == nil || !strideIdentifier(operationID) || !strideIdentifier(messageID) || !scoutHomeProjectTerminalError(cause) {
		return thread, cause
	}
	lock := app.scoutChatThreadLock(thread.ID)
	lock.Lock()
	defer lock.Unlock()
	current, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		return thread, err
	}
	found := false
	for index := range current.ProjectLinkOperations {
		operation := &current.ProjectLinkOperations[index]
		if operation.OperationID != operationID {
			continue
		}
		if operation.State == "confirmed" {
			// A canonical receipt disagreement is not a terminalization request.
			// Never reinterpret an already-confirmed legacy journal as success;
			// only reconcileScoutHomeProjectLink may accept it after comparing
			// every immutable receipt and visible Project field above.
			return current, cause
		}
		operation.State = "failed_terminal"
		found = true
	}
	messageIndex := scoutChatMessageIndex(current, messageID)
	if !found || messageIndex < 0 {
		return thread, ErrProjectAuthorityConflict
	}
	if current.Messages[messageIndex].Project != nil {
		current.Messages[messageIndex].Project.Status = "unavailable"
	}
	if replyMessageID != "" {
		replyIndex := scoutChatMessageIndex(current, replyMessageID)
		if replyIndex < 0 || current.Messages[replyIndex].Reply == nil {
			return thread, ErrProjectAuthorityConflict
		}
		reply := current.Messages[replyIndex].Reply
		reply.State = scoutReplyStateCanceled
		reply.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		reply.LeaseID, reply.LeaseExpiresAt, reply.ErrorCode = "", "", ""
		reply.Retryable = false
		current.Messages[replyIndex].Text = "Scout did not run because Project access changed. Your message is safe in this conversation."
	}
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	current.Preview = trimForStorage(current.Messages[messageIndex].Text, 140)
	if err := app.saveScoutChatThread(current); err != nil {
		return thread, err
	}
	return current, nil
}

func withCurrentHomeProjectAuthorityRequestContext(ctx context.Context, token homeProjectContextToken, use func(StrideE10TenantAuthoritySnapshot) error) error {
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

func (store *PostgresCanonicalStore) projectChatSourceGroupAuthorizedCurrent(ctx context.Context, organizationID, groupID string, wantMembers int) error {
	if store == nil || store.pool == nil || !strideIdentifier(organizationID) || !strideIdentifier(groupID) || wantMembers < 1 {
		return ErrProjectAuthorityInvalid
	}
	var members, authorized int
	err := store.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM stride_project_chat_source_group_members WHERE organization_id=$1 AND group_id=$2),
(SELECT count(*) FROM stride_project_associations_authorized_current authorized
 JOIN stride_project_chat_source_group_members member ON member.organization_id=authorized.organization_id AND member.association_id=authorized.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2)`, organizationID, groupID).Scan(&members, &authorized)
	if err != nil {
		return err
	}
	if members != wantMembers || authorized != wantMembers {
		return ErrProjectAuthorityConflict
	}
	return nil
}

func (store *PostgresCanonicalStore) projectChatSourceGroupFreshAuthority(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot,
	thread scoutChatThreadRecord, token homeProjectContextToken, groupID string, wantMembers int) error {
	var projectCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_projects_current current_project
JOIN stride_project_revisions revision ON revision.organization_id=current_project.organization_id AND revision.project_id=current_project.project_id AND revision.revision=current_project.revision
JOIN stride_organization_memberships_current membership ON membership.organization_id=current_project.organization_id
 AND membership.person_id=$3 AND membership.status='active'
WHERE current_project.organization_id=$1 AND current_project.project_id=$2 AND current_project.lifecycle<>'archived'
 AND (revision.audience->'principals' @> jsonb_build_array($3::text)
   OR revision.controller_memberships @> jsonb_build_array(jsonb_build_object('contractType','organization_membership',
     'id',membership.membership_id,'revision',membership.revision)))`, snapshot.Organization.Header.ID, token.ProjectID,
		snapshot.Person.Header.ID).Scan(&projectCount); err != nil || projectCount != 1 {
		return ErrProjectAuthorityConflict
	}
	authority, err := store.projectChatSourceAuthorityForThread(ctx, snapshot, thread)
	if err != nil {
		return err
	}
	audience, _ := json.Marshal(STRIDEAudience{Visibility: authority.Visibility, Principals: authority.Principals})
	var rootCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_chat_source_groups source_group
JOIN stride_conversation_events event ON event.tenant_id=source_group.organization_id AND event.event_id=source_group.conversation_event_id
WHERE source_group.organization_id=$1 AND source_group.group_id=$2 AND event.visibility=$3 AND event.acl_version=$4
 AND event.audience_digest=sha256(convert_to($5::jsonb::text,'UTF8')) AND event.invalidated_at IS NULL`, snapshot.Organization.Header.ID,
		groupID, authority.Visibility, authority.ACLRevision, audience).Scan(&rootCount); err != nil || rootCount != 1 {
		return ErrProjectAuthorityConflict
	}
	return store.projectChatSourceGroupAuthorizedCurrent(ctx, snapshot.Organization.Header.ID, groupID, wantMembers)
}

func (store *PostgresCanonicalStore) projectChatSourceGroupAssociationIDs(ctx context.Context, organizationID, groupID string) ([]string, error) {
	rows, err := store.pool.Query(ctx, `SELECT association_id FROM stride_project_chat_source_group_members
WHERE organization_id=$1 AND group_id=$2 ORDER BY ordinal`, organizationID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (store *PostgresCanonicalStore) confirmHomeProjectChatSend(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, thread scoutChatThreadRecord, messageID, operationKey, text string, token homeProjectContextToken) (confirmedProjectChatLink, error) {
	return store.confirmProjectChatSend(ctx, snapshot, thread, messageID, operationKey, text, token, privateProjectChatSourceAuthority(snapshot.Person.Header.ID))
}

func (store *PostgresCanonicalStore) confirmProjectChatSend(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, thread scoutChatThreadRecord, messageID, operationKey, text string, token homeProjectContextToken, sourceAuthority projectChatSourceAuthority) (confirmedProjectChatLink, error) {
	return store.confirmProjectChatSendWithManifest(ctx, snapshot, thread, messageID, operationKey, text, token, sourceAuthority, nil)
}

func (store *PostgresCanonicalStore) confirmProjectChatSendWithManifest(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, thread scoutChatThreadRecord, messageID, operationKey, text string, token homeProjectContextToken, sourceAuthority projectChatSourceAuthority, manifest *projectChatSourceManifest) (confirmedProjectChatLink, error) {
	var result confirmedProjectChatLink
	if store == nil || store.pool == nil || token.OrganizationID != snapshot.Organization.Header.ID || token.PersonID != snapshot.Person.Header.ID ||
		thread.ID == "" || messageID == "" || (strings.TrimSpace(text) == "" && (manifest == nil || len(manifest.Attachments) == 0)) ||
		!oneOf(token.Kind, "project", "create") || sourceAuthority.validate(snapshot.Person.Header.ID) != nil {
		return result, ErrProjectAuthorityInvalid
	}
	if token.Kind == "create" && sourceAuthority.Visibility != "private" {
		return result, ErrProjectAuthorityDenied
	}
	manifestDigest := ""
	if manifest != nil {
		if manifest.Version != projectChatManifestVersionForToken(token.Version) || manifest.Destination != (homeProjectDestination{Route: "thread", ThreadID: thread.ID}) ||
			manifest.TextDigest != sha256Hex([]byte(strings.TrimSpace(text))) || manifest.Digest != projectChatManifestDigest(*manifest) {
			return result, ErrProjectAuthorityConflict
		}
		manifestDigest = manifest.Digest
	}
	requestFingerprint := sha256Hex([]byte(strings.Join([]string{snapshot.Organization.Header.ID, thread.ID, messageID, strings.TrimSpace(text), token.Kind, token.ProjectID, fmt.Sprint(token.ProjectRevision), token.ProjectDigest, token.ProjectTitle, token.Basis, sourceAuthority.SourceType, sourceAuthority.Visibility, strings.Join(sourceAuthority.Principals, ","), fmt.Sprint(sourceAuthority.ACLRevision), manifestDigest}, "\x00")))
	operationKeyDigest := sha256Hex([]byte("project-chat-send/v1\x00" + operationKey))
	operationID := projectChatID("project_send", snapshot.Organization.Header.ID, thread.ID, messageID)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var storedFingerprint string
	err = tx.QueryRow(ctx, `SELECT encode(request_fingerprint,'hex'),project_id,project_revision,association_id
FROM stride_project_chat_send_receipts WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, operationID).
		Scan(&storedFingerprint, &result.ProjectID, &result.ProjectRevision, &result.AssociationID)
	if err == nil {
		if storedFingerprint != requestFingerprint {
			return result, ErrProjectAuthorityConflict
		}
		if err := tx.QueryRow(ctx, `SELECT encode(content_digest,'hex'),title FROM stride_project_revisions WHERE project_id=$1 AND revision=$2 AND organization_id=$3`, result.ProjectID, result.ProjectRevision, snapshot.Organization.Header.ID).Scan(&result.ProjectDigest, &result.ProjectTitle); err != nil {
			return result, err
		}
		result.AssociationRevision = 2
		if manifest != nil {
			groupID := projectChatID("project_source_group", snapshot.Organization.Header.ID, thread.ID, messageID, operationKey)
			var storedManifest string
			if err := tx.QueryRow(ctx, `SELECT encode(source_manifest_digest,'hex') FROM stride_project_chat_source_groups WHERE organization_id=$1 AND group_id=$2`, snapshot.Organization.Header.ID, groupID).Scan(&storedManifest); err != nil || storedManifest != manifest.Digest {
				return result, ErrProjectAuthorityConflict
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
	if err = syncProjectSessionAuthority(ctx, tx, snapshot); err != nil {
		return result, err
	}
	now := time.Now().UTC()
	if token.Kind == "project" {
		err = tx.QueryRow(ctx, `SELECT revision.project_id,revision.revision,encode(revision.content_digest,'hex'),revision.title
FROM stride_projects_current current_project JOIN stride_project_revisions revision ON revision.project_id=current_project.project_id AND revision.revision=current_project.revision
WHERE current_project.organization_id=$1 AND current_project.project_id=$2 AND current_project.revision=$3 AND current_project.content_digest=decode($4,'hex')
  AND current_project.lifecycle<>'archived' AND (revision.audience->'principals' @> jsonb_build_array($5::text)
  OR revision.controller_memberships @> jsonb_build_array(jsonb_build_object('contractType','organization_membership','id',$6::text,'revision',$7::bigint,'digest',$8::text)))`,
			snapshot.Organization.Header.ID, token.ProjectID, token.ProjectRevision, token.ProjectDigest, snapshot.Person.Header.ID,
			snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.Membership.Header.ContentDigest).
			Scan(&result.ProjectID, &result.ProjectRevision, &result.ProjectDigest, &result.ProjectTitle)
		if errors.Is(err, pgx.ErrNoRows) {
			return result, errHomeProjectStale
		}
		if err != nil {
			return result, err
		}
	} else {
		result.ProjectID = projectChatID("project", snapshot.Organization.Header.ID, operationID)
		result.ProjectRevision = 1
		result.ProjectTitle = token.ProjectTitle
		result.ProjectDigest = sha256Hex([]byte(strings.Join([]string{"project/v1", result.ProjectID, snapshot.Organization.Header.ID, result.ProjectTitle, snapshot.Person.Header.ID, snapshot.Membership.Header.ID}, "\x00")))
		bindingID := projectChatID("project_binding", result.ProjectID, thread.ID)
		bindingDigest := sha256Hex([]byte(strings.Join([]string{"project-binding/v1", bindingID, result.ProjectID, thread.ID, result.ProjectDigest}, "\x00")))
		threadACLDigest := sha256Hex([]byte(strings.Join([]string{"private-thread/v1", thread.ID, snapshot.Person.Header.ID}, "\x00")))
		controllers, _ := json.Marshal([]STRIDEReference{{ContractType: STRIDEContractOrganizationMembership, ID: snapshot.Membership.Header.ID, Revision: snapshot.Membership.Header.Revision, Digest: snapshot.Membership.Header.ContentDigest}})
		audience, _ := json.Marshal(STRIDEAudience{Visibility: "project", Principals: []string{snapshot.Person.Header.ID}})
		createKey := sha256Hex([]byte("create-project/v1\x00" + operationKeyDigest))
		createFingerprint := sha256Hex([]byte(result.ProjectDigest + "\x00" + bindingDigest))
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,content_digest)
VALUES($1,1,$2,$3,'[]',$4,'organization_default',$5,$6,1,$7,$8,$8,decode($9,'hex'))`, result.ProjectID, snapshot.Organization.Header.ID, result.ProjectTitle, "active", controllers, audience, snapshot.Person.Header.ID, now, result.ProjectDigest)
		if err != nil {
			return result, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_thread_binding_revisions(binding_id,revision,organization_id,project_id,project_revision,thread_id,kind,state,thread_audience_revision,thread_acl_digest,actor_person_id,actor_membership_id,actor_membership_revision,bound_at,content_digest)
VALUES($1,1,$2,$3,1,$4,'primary','active',1,decode($5,'hex'),$6,$7,$8,$9,decode($10,'hex'))`, bindingID, snapshot.Organization.Header.ID, result.ProjectID, thread.ID, threadACLDigest, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, now, bindingDigest)
		if err != nil {
			return result, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,project_revision,binding_id,binding_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at)
VALUES($1,$2,'create_project',$3,1,$4,1,$5,$6,$7,decode($8,'hex'),$9,$10,decode($11,'hex'),decode($12,'hex'),$13)`, projectChatID("project_create", operationID), snapshot.Organization.Header.ID, result.ProjectID, bindingID, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, createKey, createFingerprint, now)
		if err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_projects_current(project_id,revision,organization_id,lifecycle,content_digest,updated_at) VALUES($1,1,$2,'active',decode($3,'hex'),$4)`, result.ProjectID, snapshot.Organization.Header.ID, result.ProjectDigest, now); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_thread_bindings_current(binding_id,revision,organization_id,project_id,thread_id,kind,state,content_digest,updated_at) VALUES($1,1,$2,$3,$4,'primary','active',decode($5,'hex'),$6)`, bindingID, snapshot.Organization.Header.ID, result.ProjectID, thread.ID, bindingDigest, now); err != nil {
			return result, err
		}
	}

	eventID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, messageID)
	contentDigest := sha256Hex([]byte(strings.TrimSpace(text)))
	audience, _ := json.Marshal(STRIDEAudience{Visibility: sourceAuthority.Visibility, Principals: sourceAuthority.Principals})
	parentReceiptID := ""
	if manifest != nil && manifest.Reply != nil {
		parent := manifest.Reply
		parentReceiptID = projectChatID("project_reply_source", snapshot.Organization.Header.ID, eventID, parent.EventID)
		var currentRevision int64
		var currentDigest, currentThread, currentAuthor, currentAudience string
		var currentACL, currentPurge int64
		parentErr := tx.QueryRow(ctx, `SELECT content_revision,encode(content_digest,'hex'),thread_id,author_principal,encode(audience_digest,'hex'),acl_version,purge_generation
FROM stride_conversation_events WHERE tenant_id=$1 AND event_id=$2 AND invalidated_at IS NULL`, snapshot.Organization.Header.ID, parent.EventID).
			Scan(&currentRevision, &currentDigest, &currentThread, &currentAuthor, &currentAudience, &currentACL, &currentPurge)
		if errors.Is(parentErr, pgx.ErrNoRows) {
			var parentSequence int64
			if err = tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM stride_conversation_events WHERE tenant_id=$1`, snapshot.Organization.Header.ID).Scan(&parentSequence); err != nil {
				return result, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO stride_conversation_events(tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,occurred_at,ingested_at,event_type,content_revision,content_digest,audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,body_ref,structured_refs)
VALUES($1,$2,1,$3,1,$4,$5,$6,$7,$8,'',$9,$9,'message',$10,decode($11,'hex'),decode($12,'hex'),$13,$14,'organization_default',$15,'server','', '[]')`,
				snapshot.Organization.Header.ID, parent.EventID, parentSequence, projectChatID("project_reply_admit", operationID, parent.EventID),
				sourceAuthority.SourceType, parent.MessageID, thread.ID, parent.AuthorPersonID, now, parent.SourceRevision, parent.SourceDigest,
				parent.AudienceDigest, sourceAuthority.Visibility, parent.ACLRevision, parent.PurgeGeneration)
			if err != nil {
				return result, err
			}
		} else if parentErr != nil || currentRevision != parent.SourceRevision || currentDigest != parent.SourceDigest || currentThread != thread.ID ||
			currentAuthor != parent.AuthorPersonID || currentAudience != parent.AudienceDigest || currentACL != parent.ACLRevision || currentPurge != parent.PurgeGeneration {
			return result, errHomeProjectStale
		}
		parentRefs, _ := json.Marshal([]STRIDEReference{{ContractType: STRIDEContractConversationEvent, ID: parent.EventID, Revision: parent.SourceRevision, Digest: parent.SourceDigest}})
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
SELECT $1,$2,'conversation_event',$3,$4,decode($5,'hex'),$6::jsonb,decode($5,'hex'),$7::jsonb,$8,
sha256(convert_to(concat_ws(E'\x1f',event.tenant_id,event.event_id,event.content_revision::text,encode(event.content_digest,'hex'),encode(event.audience_digest,'hex'),event.visibility,event.acl_version::text,event.purge_generation::text),'UTF8')),
1,$9,$10,$11,$12,decode($13,'hex'),$14,$15,decode($16,'hex'),decode($17,'hex'),$18,$19
FROM stride_conversation_events event WHERE event.tenant_id=$2 AND event.event_id=$3
ON CONFLICT(source_authority_receipt_id) DO NOTHING`, parentReceiptID, snapshot.Organization.Header.ID, parent.EventID, parent.SourceRevision,
			parent.SourceDigest, parentRefs, audience, parent.ACLRevision, parent.PurgeGeneration, snapshot.Person.Header.ID,
			snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision,
			snapshot.Generation, sha256Hex([]byte("project-reply-source/v1\x00"+operationKeyDigest)),
			sha256Hex([]byte(parent.LegacyDigest+"\x00"+manifest.Digest)), now, now.Add(30*time.Minute))
		if err != nil {
			return result, err
		}
	}
	var sequence int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM stride_conversation_events WHERE tenant_id=$1`, snapshot.Organization.Header.ID).Scan(&sequence); err != nil {
		return result, err
	}
	replyEventID := any(nil)
	if manifest != nil && manifest.Reply != nil {
		replyEventID = manifest.Reply.EventID
	}
	_, err = tx.Exec(ctx, `INSERT INTO stride_conversation_events(tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,occurred_at,ingested_at,event_type,content_revision,content_digest,audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,body_ref,structured_refs,reply_to_event_id)
VALUES($1,$2,1,$3,1,$4,$5,$6,$7,$8,$9,$10,$10,'message',1,decode($11,'hex'),sha256(convert_to($12::jsonb::text,'UTF8')),$13,$14,'organization_default',1,'client',$15,'[]',$16)`, snapshot.Organization.Header.ID, eventID, sequence, operationID, sourceAuthority.SourceType, messageID, thread.ID, snapshot.Person.Header.ID, scoutChatAuthorName(&userAccount{Name: snapshot.Person.Header.ID}), now, contentDigest, audience, sourceAuthority.Visibility, sourceAuthority.ACLRevision, "scout-chat://"+thread.ID+"/"+messageID, replyEventID)
	if err != nil {
		return result, err
	}
	if manifest != nil && manifest.Version == projectChatSourceManifestV3 && manifest.Reply != nil {
		parent := manifest.Reply
		for _, media := range parent.Media {
			if media.Ordinal < 0 || !oneOf(media.Kind, "file", "generated_image") || !strideIdentifier(media.PartID) || !isHexDigest(media.PartDigest) {
				return result, ErrProjectAuthorityConflict
			}
			destinationDigest := sha256Hex([]byte(media.DestinationRevision))
			originID, originRevision := any(nil), any(nil)
			if media.OriginFileID != "" {
				originID, originRevision = media.OriginFileID, media.OriginRevision
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_rich_message_part_revisions(organization_id,part_id,revision,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,source_origin_id,source_origin_revision,blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,author_principal,source_audience,source_acl_revision,purge_generation,recorded_at,content_digest)
VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),$15,$16,$17::jsonb,$18,$19,$20,decode($21,'hex'))
ON CONFLICT(part_id,revision) DO NOTHING`, snapshot.Organization.Header.ID, media.PartID, parent.EventID, parent.SourceRevision,
				media.Ordinal, media.SourceID, media.SourceRevision, originID, originRevision, media.BlobRef, media.BlobDigest, media.Mime,
				media.Size, destinationDigest, media.DestinationRevision, media.AuthorPrincipal, audience, sourceAuthority.ACLRevision,
				parent.PurgeGeneration, now, media.PartDigest); err != nil {
				return result, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_rich_message_parts_current(organization_id,part_id,revision,conversation_event_id,ordinal,content_digest,updated_at)
VALUES($1,$2,1,$3,$4,decode($5,'hex'),$6) ON CONFLICT(part_id) DO NOTHING`, snapshot.Organization.Header.ID, media.PartID,
				parent.EventID, media.Ordinal, media.PartDigest, now); err != nil {
				return result, err
			}
			receiptID := projectChatID("project_reply_media_receipt", operationID, fmt.Sprint(media.Ordinal), media.PartID)
			mediaOperationKey := sha256Hex([]byte("project-chat-reply-media/v1\x00" + operationKeyDigest + "\x00" + fmt.Sprint(media.Ordinal)))
			mediaFingerprint := sha256Hex([]byte(strings.Join([]string{"project-chat-reply-media/v1", snapshot.Organization.Header.ID,
				eventID, parent.EventID, fmt.Sprint(media.Ordinal), media.Kind, media.PartID, "1", media.PartDigest, manifest.Digest, mediaOperationKey}, "\x1f")))
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_reply_media_authority_receipts(organization_id,receipt_id,child_event_id,parent_event_id,part_id,part_revision,part_digest,source_audience,source_acl_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,operation_key_digest,request_fingerprint,recorded_at,expires_at)
VALUES($1,$2,$3,$4,$5,1,decode($6,'hex'),$7::jsonb,$8,$9,$10,$11,$12,decode($13,'hex'),$14,$15,decode($16,'hex'),decode($17,'hex'),$18,$19)
ON CONFLICT(organization_id,receipt_id) DO NOTHING`, snapshot.Organization.Header.ID, receiptID, eventID, parent.EventID, media.PartID,
				media.PartDigest, audience, sourceAuthority.ACLRevision, parent.PurgeGeneration, snapshot.Person.Header.ID,
				snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision,
				snapshot.Generation, mediaOperationKey, mediaFingerprint, now, now.Add(store.projectChatReplyMediaReceiptTTL())); err != nil {
				return result, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_reply_media_dependencies(organization_id,child_event_id,parent_event_id,ordinal,media_kind,part_id,part_revision,part_digest,authority_receipt_id,source_manifest_digest,recorded_at)
VALUES($1,$2,$3,$4,$5,$6,1,decode($7,'hex'),$8,decode($9,'hex'),$10)
ON CONFLICT(organization_id,child_event_id,ordinal) DO NOTHING`, snapshot.Organization.Header.ID, eventID, parent.EventID,
				media.Ordinal, media.Kind, media.PartID, media.PartDigest, receiptID, manifest.Digest, now); err != nil {
				return result, err
			}
		}
	}
	sourceRefs, _ := json.Marshal([]STRIDEReference{{ContractType: STRIDEContractConversationEvent, ID: eventID, Revision: 1, Digest: contentDigest}})
	receiptID := projectChatID("project_source", operationID)
	sourceKey := sha256Hex([]byte("project-source/v1\x00" + operationKeyDigest))
	sourceFingerprint := sha256Hex([]byte(contentDigest + "\x00" + result.ProjectDigest))
	_, err = tx.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
SELECT $1,$2,'conversation_event',$3,1,decode($4,'hex'),$5::jsonb,decode($6,'hex'),$7::jsonb,1,
sha256(convert_to(concat_ws(E'\x1f',event.tenant_id,event.event_id,event.content_revision::text,encode(event.content_digest,'hex'),encode(event.audience_digest,'hex'),event.visibility,event.acl_version::text,event.purge_generation::text),'UTF8')),
1,1,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),$16,$17
FROM stride_conversation_events event WHERE event.tenant_id=$2 AND event.event_id=$3`, receiptID, snapshot.Organization.Header.ID, eventID, contentDigest, sourceRefs, contentDigest, audience, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, sourceKey, sourceFingerprint, now, now.Add(30*time.Minute))
	if err != nil {
		return result, err
	}
	result.AssociationID = projectChatID("project_association", snapshot.Organization.Header.ID, eventID, result.ProjectID)
	proposedDigest := sha256Hex([]byte("project-association/proposed/v1\x00" + result.AssociationID + "\x00" + result.ProjectDigest + "\x00" + contentDigest))
	confirmedDigest := sha256Hex([]byte("project-association/confirmed/v1\x00" + result.AssociationID + "\x00" + proposedDigest))
	associationKey := sha256Hex([]byte("project-association/v1\x00" + operationKeyDigest))
	for _, revision := range []struct {
		n                                  int64
		state, digest, priorDigest, action string
	}{{1, "proposed", proposedDigest, "", "propose"}, {2, "confirmed", confirmedDigest, proposedDigest, "confirm"}} {
		expires := any(nil)
		if revision.state == "proposed" {
			expires = now.Add(15 * time.Minute)
		}
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT $1,$2,$3,$4,$5,'conversation_event',$6,1,decode($7,'hex'),$8::jsonb,$9,decode($7,'hex'),$10,$11,'project_linker_v1',$12,$13,$14,$15,decode($16,'hex'),$17,$18,$19::jsonb,1,receipt.source_acl_digest,1,1,decode($20,'hex'),$21,$22,CASE WHEN $23='' THEN NULL ELSE decode($23,'hex') END,$24,decode($25,'hex')
FROM stride_project_source_authority_receipts receipt WHERE receipt.source_authority_receipt_id=$9 AND receipt.organization_id=$3`, result.AssociationID, revision.n, snapshot.Organization.Header.ID, result.ProjectID, result.ProjectRevision, eventID, contentDigest, sourceRefs, receiptID, revision.state, token.Basis, token.Confidence, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, audience, associationKey, expires, nullablePriorRevision(revision.n), revision.priorDigest, now, revision.digest)
		if err != nil {
			return result, err
		}
		eventFingerprint := sha256Hex([]byte(revision.digest + "\x00" + revision.action))
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$4,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),$16)`, projectChatID("project_association_event", operationID, revision.action), snapshot.Organization.Header.ID, result.AssociationID, revision.n, revision.action, revision.state, revision.n-1, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, sha256Hex([]byte(associationKey+revision.action)), eventFingerprint, now)
		if err != nil {
			return result, err
		}
		if revision.n == 1 {
			_, err = tx.Exec(ctx, `INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at) VALUES($1,1,$2,$3,'proposed',decode($4,'hex'),$5)`, result.AssociationID, snapshot.Organization.Header.ID, result.ProjectID, proposedDigest, now)
		} else {
			_, err = tx.Exec(ctx, `UPDATE stride_project_associations_current SET revision=2,state='confirmed',content_digest=decode($1,'hex'),updated_at=$2 WHERE association_id=$3 AND organization_id=$4`, confirmedDigest, now, result.AssociationID, snapshot.Organization.Header.ID)
		}
		if err != nil {
			return result, err
		}
	}
	result.ProjectRevision = maxInt64(result.ProjectRevision, 1)
	for _, family := range []string{"home", "work", "board", "project_record"} {
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at) VALUES($1,$2,2,'list_new',$3,decode($4,'hex'),decode($5,'hex'),'pending',0,$6) ON CONFLICT DO NOTHING`, snapshot.Organization.Header.ID, result.AssociationID, family, contentDigest, sha256Hex([]byte(snapshot.SessionHash+"\x00"+fmt.Sprint(snapshot.Generation))), now)
		if err != nil {
			return result, err
		}
	}
	if manifest != nil {
		partAssociations := make([]string, 0, len(manifest.Attachments))
		for _, attachment := range manifest.Attachments {
			partID := projectChatID("rich_message_part", snapshot.Organization.Header.ID, eventID, fmt.Sprint(attachment.Ordinal), attachment.SourceID)
			partDigest := sha256Hex([]byte(strings.Join([]string{"rich-message-part/v1", partID, eventID, fmt.Sprint(attachment.Ordinal),
				attachment.SourceID, attachment.SourceRevision, attachment.BlobRef, attachment.BlobDigest, attachment.Mime,
				fmt.Sprint(attachment.Size), attachment.DestinationRevision, attachment.OriginFileID, attachment.OriginRevision}, "\x1f")))
			destinationDigest := sha256Hex([]byte("project-chat-attachment-destination/v1\x00" + attachment.DestinationRevision))
			_, err = tx.Exec(ctx, `INSERT INTO stride_rich_message_part_revisions(
organization_id,part_id,revision,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,source_origin_id,source_origin_revision,
blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,author_principal,source_audience,source_acl_revision,purge_generation,recorded_at,content_digest)
VALUES($1,$2,1,$3,1,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,decode($10,'hex'),$11,$12,decode($13,'hex'),$14,$15,$16::jsonb,$17,1,$18,decode($19,'hex'))`,
				snapshot.Organization.Header.ID, partID, eventID, attachment.Ordinal, attachment.SourceID, attachment.SourceRevision,
				attachment.OriginFileID, attachment.OriginRevision, attachment.BlobRef, attachment.BlobDigest, attachment.Mime,
				attachment.Size, destinationDigest, attachment.DestinationRevision, snapshot.Person.Header.ID, audience,
				sourceAuthority.ACLRevision, now, partDigest)
			if err != nil {
				return result, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_rich_message_parts_current(organization_id,part_id,revision,conversation_event_id,ordinal,content_digest,updated_at)
VALUES($1,$2,1,$3,$4,decode($5,'hex'),$6)`, snapshot.Organization.Header.ID, partID, eventID, attachment.Ordinal, partDigest, now); err != nil {
				return result, err
			}
			partReceiptID := projectChatID("project_part_source", operationID, fmt.Sprint(attachment.Ordinal))
			partRefs, _ := json.Marshal([]STRIDEReference{{ContractType: "rich_message_part", ID: partID, Revision: 1, Digest: partDigest}})
			_, err = tx.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
SELECT $1,$2,'rich_message_part',$3,1,decode($4,'hex'),$5::jsonb,decode($4,'hex'),$6::jsonb,$7,
sha256(convert_to(concat_ws(E'\x1f',part.organization_id,part.part_id,part.revision::text,encode(part.content_digest,'hex'),
 encode(sha256(convert_to(part.source_audience::text,'UTF8')),'hex'),part.source_acl_revision::text,part.purge_generation::text),'UTF8')),
1,1,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),$16,$17
FROM stride_rich_message_part_revisions part WHERE part.organization_id=$2 AND part.part_id=$3 AND part.revision=1`,
				partReceiptID, snapshot.Organization.Header.ID, partID, partDigest, partRefs, audience, sourceAuthority.ACLRevision,
				snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
				snapshot.ActiveSession.SessionRevision, snapshot.Generation, sha256Hex([]byte("project-part-source/v1\x00"+operationKeyDigest+fmt.Sprint(attachment.Ordinal))),
				sha256Hex([]byte(partDigest+"\x00"+result.ProjectDigest)), now, now.Add(30*time.Minute))
			if err != nil {
				return result, err
			}
			partAssociationID := projectChatID("project_association", snapshot.Organization.Header.ID, partID, result.ProjectID)
			partAssociations = append(partAssociations, partAssociationID)
			proposedPartDigest := sha256Hex([]byte("project-association/proposed/v1\x00" + partAssociationID + "\x00" + result.ProjectDigest + "\x00" + partDigest))
			confirmedPartDigest := sha256Hex([]byte("project-association/confirmed/v1\x00" + partAssociationID + "\x00" + proposedPartDigest))
			partAssociationKey := sha256Hex([]byte("project-part-association/v1\x00" + operationKeyDigest + "\x00" + fmt.Sprint(attachment.Ordinal)))
			for _, revision := range []struct {
				n                                  int64
				state, digest, priorDigest, action string
			}{{1, "proposed", proposedPartDigest, "", "propose"}, {2, "confirmed", confirmedPartDigest, proposedPartDigest, "confirm"}} {
				var expires any
				if revision.n == 1 {
					expires = now.Add(15 * time.Minute)
				}
				_, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT $1,$2,$3,$4,$5,'rich_message_part',$6,1,decode($7,'hex'),$8::jsonb,$9,decode($7,'hex'),$10,$11,'project_linker_v2',$12,$13,$14,$15,decode($16,'hex'),$17,$18,$19::jsonb,$20,receipt.source_acl_digest,1,1,decode($21,'hex'),$22,$23,CASE WHEN $24='' THEN NULL ELSE decode($24,'hex') END,$25,decode($26,'hex')
FROM stride_project_source_authority_receipts receipt WHERE receipt.source_authority_receipt_id=$9 AND receipt.organization_id=$3`,
					partAssociationID, revision.n, snapshot.Organization.Header.ID, result.ProjectID, result.ProjectRevision, partID, partDigest,
					partRefs, partReceiptID, revision.state, token.Basis, token.Confidence, snapshot.Person.Header.ID, snapshot.Membership.Header.ID,
					snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, audience,
					sourceAuthority.ACLRevision, partAssociationKey, expires, nullablePriorRevision(revision.n), revision.priorDigest, now, revision.digest)
				if err != nil {
					return result, err
				}
				eventFingerprint := sha256Hex([]byte(revision.digest + "\x00" + revision.action))
				if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$4,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),$16)`,
					projectChatID("project_part_association_event", operationID, fmt.Sprint(attachment.Ordinal), revision.action), snapshot.Organization.Header.ID,
					partAssociationID, revision.n, revision.action, revision.state, revision.n-1, snapshot.Person.Header.ID,
					snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision,
					snapshot.Generation, sha256Hex([]byte(partAssociationKey+revision.action)), eventFingerprint, now); err != nil {
					return result, err
				}
				if revision.n == 1 {
					_, err = tx.Exec(ctx, `INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at) VALUES($1,1,$2,$3,'proposed',decode($4,'hex'),$5)`, partAssociationID, snapshot.Organization.Header.ID, result.ProjectID, proposedPartDigest, now)
				} else {
					_, err = tx.Exec(ctx, `UPDATE stride_project_associations_current SET revision=2,state='confirmed',content_digest=decode($1,'hex'),updated_at=$2 WHERE association_id=$3 AND organization_id=$4`, confirmedPartDigest, now, partAssociationID, snapshot.Organization.Header.ID)
				}
				if err != nil {
					return result, err
				}
			}
			for _, family := range []string{"home", "work", "board", "project_record"} {
				if _, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at) VALUES($1,$2,2,'list_new',$3,decode($4,'hex'),decode($5,'hex'),'pending',0,$6)`, snapshot.Organization.Header.ID, partAssociationID, family, partDigest, sha256Hex([]byte(snapshot.SessionHash+"\x00"+fmt.Sprint(snapshot.Generation))), now); err != nil {
					return result, err
				}
			}
		}
		if manifest.Reply != nil {
			parent := manifest.Reply
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_reply_dependencies(organization_id,child_event_id,child_event_revision,parent_event_id,parent_event_revision,parent_event_digest,parent_author_principal,parent_legacy_snapshot_digest,parent_audience_digest,parent_acl_revision,parent_purge_generation,parent_source_authority_receipt_id,source_manifest_digest,recorded_at)
VALUES($1,$2,1,$3,$4,decode($5,'hex'),$6,decode($7,'hex'),decode($8,'hex'),$9,$10,$11,decode($12,'hex'),$13)`,
				snapshot.Organization.Header.ID, eventID, parent.EventID, parent.SourceRevision, parent.SourceDigest, parent.AuthorPersonID,
				parent.LegacyDigest, parent.AudienceDigest, parent.ACLRevision, parent.PurgeGeneration, parentReceiptID, manifest.Digest, now); err != nil {
				return result, err
			}
		}
		groupID := projectChatID("project_source_group", snapshot.Organization.Header.ID, thread.ID, messageID, operationKey)
		groupOperationID := projectChatID("project_source_group_send", snapshot.Organization.Header.ID, thread.ID, messageID)
		groupOperationKey := sha256Hex([]byte("project-chat-source-group-send/v2\x00" + operationKey))
		groupFingerprint := projectChatSourceGroupRequestFingerprint(snapshot, groupID, groupOperationID, groupOperationKey, manifest.Digest,
			thread.ID, messageID, eventID, result.ProjectID, result.ProjectRevision, result.AssociationID, 2, len(partAssociations)+1)
		_, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at,source_manifest_version)
VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,2,$13,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',$20,$21)`,
			snapshot.Organization.Header.ID, groupID, groupOperationID, groupOperationKey, groupFingerprint, manifest.Digest, thread.ID, messageID,
			eventID, result.ProjectID, result.ProjectRevision, result.AssociationID, len(partAssociations)+1, snapshot.Person.Header.ID,
			snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision,
			snapshot.Generation, now, manifest.Version)
		if err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
VALUES($1,$2,0,'conversation_event',$3,1,decode($4,'hex'),$5,$6,2,$7)`, snapshot.Organization.Header.ID, groupID, eventID,
			contentDigest, receiptID, result.AssociationID, now); err != nil {
			return result, err
		}
		for index, attachment := range manifest.Attachments {
			partID := projectChatID("rich_message_part", snapshot.Organization.Header.ID, eventID, fmt.Sprint(attachment.Ordinal), attachment.SourceID)
			partDigest := sha256Hex([]byte(strings.Join([]string{"rich-message-part/v1", partID, eventID, fmt.Sprint(attachment.Ordinal),
				attachment.SourceID, attachment.SourceRevision, attachment.BlobRef, attachment.BlobDigest, attachment.Mime,
				fmt.Sprint(attachment.Size), attachment.DestinationRevision, attachment.OriginFileID, attachment.OriginRevision}, "\x1f")))
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
VALUES($1,$2,$3,'rich_message_part',$4,1,decode($5,'hex'),$6,$7,2,$8)`, snapshot.Organization.Header.ID, groupID,
				attachment.Ordinal+1, partID, partDigest, projectChatID("project_part_source", operationID, fmt.Sprint(attachment.Ordinal)), partAssociations[index], now); err != nil {
				return result, err
			}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_send_receipts(organization_id,operation_id,operation_key_digest,request_fingerprint,thread_id,message_id,conversation_event_id,conversation_event_revision,project_id,project_revision,association_id,association_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
VALUES($1,$2,decode($3,'hex'),decode($4,'hex'),$5,$6,$7,1,$8,$9,$10,2,$11,$12,$13,decode($14,'hex'),$15,$16,'confirmed',$17)`, snapshot.Organization.Header.ID, operationID, operationKeyDigest, requestFingerprint, thread.ID, messageID, eventID, result.ProjectID, result.ProjectRevision, result.AssociationID, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, now)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.AssociationRevision = 2
	return result, nil
}

func nullablePriorRevision(revision int64) any {
	if revision <= 1 {
		return nil
	}
	return revision - 1
}

func projectChatSourceGroupRequestFingerprint(snapshot StrideE10TenantAuthoritySnapshot, groupID, operationID, operationKeyDigest,
	manifestDigest, threadID, messageID, eventID, projectID string, projectRevision int64, rootAssociationID string,
	rootAssociationRevision int64, memberCount int) string {
	return sha256Hex([]byte(strings.Join([]string{"project-chat-source-group/v1", snapshot.Organization.Header.ID,
		groupID, operationID, operationKeyDigest, manifestDigest, threadID, messageID, eventID, "1", projectID,
		fmt.Sprint(projectRevision), rootAssociationID, fmt.Sprint(rootAssociationRevision), fmt.Sprint(memberCount),
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, fmt.Sprint(snapshot.Membership.Header.Revision), snapshot.SessionHash,
		fmt.Sprint(snapshot.ActiveSession.SessionRevision), fmt.Sprint(snapshot.Generation)}, "\x1f")))
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func syncProjectSessionAuthority(ctx context.Context, tx pgx.Tx, snapshot StrideE10TenantAuthoritySnapshot) error {
	if snapshot.Person.Validate() != nil || snapshot.Organization.Validate() != nil || snapshot.Membership.Validate() != nil || snapshot.ActiveSession.Validate() != nil {
		return ErrProjectAuthorityDenied
	}
	var revision int64
	var generation uint64
	err := tx.QueryRow(ctx, `SELECT session_revision,authority_generation FROM stride_active_organization_sessions WHERE session_subject_digest=decode($1,'hex') FOR UPDATE`, snapshot.SessionHash).Scan(&revision, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO stride_active_organization_sessions(session_subject_digest,person_id,organization_id,membership_id,membership_revision,session_revision,status,bound_at,expires_at,updated_at,authority_generation) VALUES(decode($1,'hex'),$2,$3,$4,$5,$6,'active',$7,$8,$9,$10)`, snapshot.SessionHash, snapshot.Person.Header.ID, snapshot.Organization.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.ActiveSession.SessionRevision, snapshot.ActiveSession.BoundAt, snapshot.ActiveSession.ExpiresAt, time.Now().UTC(), snapshot.Generation)
		return err
	}
	if err != nil {
		return err
	}
	if revision == snapshot.ActiveSession.SessionRevision && generation == snapshot.Generation {
		return nil
	}
	if snapshot.ActiveSession.SessionRevision != revision+1 || snapshot.Generation != generation+1 {
		return ErrProjectAuthorityConflict
	}
	_, err = tx.Exec(ctx, `UPDATE stride_active_organization_sessions SET person_id=$2,organization_id=$3,membership_id=$4,membership_revision=$5,session_revision=$6,status='active',bound_at=$7,expires_at=$8,invalidated_at=NULL,updated_at=$9,authority_generation=$10 WHERE session_subject_digest=decode($1,'hex')`, snapshot.SessionHash, snapshot.Person.Header.ID, snapshot.Organization.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.ActiveSession.SessionRevision, snapshot.ActiveSession.BoundAt, snapshot.ActiveSession.ExpiresAt, time.Now().UTC(), snapshot.Generation)
	return err
}
