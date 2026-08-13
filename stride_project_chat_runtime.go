package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	if user == nil || thread.ID == "" || !strideIdentifier(operationID) || !strideIdentifier(messageID) || token.Kind == "" {
		return thread, ErrProjectAuthorityInvalid
	}
	store := currentHomeProjectStore()
	if store == nil || !homeProjectFeatureEnabled(STRIDEFeatureProjectAuthorityWrite) {
		return thread, errHomeProjectUnavailable
	}
	var link confirmedProjectChatLink
	err := withCurrentHomeProjectAuthorityRequestContext(ctx, token, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		sourceAuthority, sourceErr := store.projectChatSourceAuthorityForThread(ctx, snapshot, thread)
		if sourceErr != nil {
			return sourceErr
		}
		var linkErr error
		link, linkErr = store.confirmProjectChatSend(ctx, snapshot, thread, messageID, key, text, token, sourceAuthority)
		return linkErr
	})
	if err != nil {
		return thread, err
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
		Status: "confirmed", ProjectID: link.ProjectID, ProjectRevision: link.ProjectRevision,
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
		if operation.OperationID != operationID || operation.TokenDigest != tokenDigest || !oneOf(operation.State, "pending", "confirmed") {
			continue
		}
		messageIndex := scoutChatMessageIndex(thread, operation.MessageID)
		return messageIndex >= 0 && thread.Messages[messageIndex].SourceOperationID == operationID && thread.Messages[messageIndex].SourceOperationDigest == bodyDigest
	}
	return false
}

func (app *kanbanBoardApp) beginScoutExistingProjectTurn(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord, operation conversationTurnOperation, binding conversationProjectLinkBinding) (scoutChatThreadRecord, scoutChatMessageRecord, bool, error) {
	if app == nil || user == nil || !strideIdentifier(thread.ID) || !strideIdentifier(message.ID) || !strideIdentifier(operation.ID) || !isHexDigest(operation.BodyDigest) || binding.Token.Kind != "project" || binding.Token.Destination != (homeProjectDestination{Route: "thread", ThreadID: thread.ID}) {
		return thread, message, false, ErrProjectAuthorityInvalid
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
		if existing.TokenDigest != tokenDigest || existing.MessageID != message.ID || existing.ProjectKind != binding.Token.Kind || existing.ProjectID != binding.Token.ProjectID || existing.ProjectRevision != binding.Token.ProjectRevision || existing.ProjectDigest != binding.Token.ProjectDigest || existing.ProjectTitle != binding.Token.ProjectTitle || existing.Basis != binding.Token.Basis {
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
	message.Project = &scoutChatProjectContext{Status: "pending", ProjectID: binding.Token.ProjectID, ProjectRevision: binding.Token.ProjectRevision, Title: binding.Token.ProjectTitle, Basis: binding.Token.Basis}
	current.ProjectLinkOperations = append(current.ProjectLinkOperations, scoutChatProjectLinkOperation{
		OperationID: operation.ID, TokenDigest: tokenDigest, MessageID: message.ID, State: "pending", ProjectKind: binding.Token.Kind,
		ProjectID: binding.Token.ProjectID, ProjectRevision: binding.Token.ProjectRevision, ProjectDigest: binding.Token.ProjectDigest,
		ProjectTitle: binding.Token.ProjectTitle, Basis: binding.Token.Basis,
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

func (store *PostgresCanonicalStore) confirmHomeProjectChatSend(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, thread scoutChatThreadRecord, messageID, operationKey, text string, token homeProjectContextToken) (confirmedProjectChatLink, error) {
	return store.confirmProjectChatSend(ctx, snapshot, thread, messageID, operationKey, text, token, privateProjectChatSourceAuthority(snapshot.Person.Header.ID))
}

func (store *PostgresCanonicalStore) confirmProjectChatSend(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, thread scoutChatThreadRecord, messageID, operationKey, text string, token homeProjectContextToken, sourceAuthority projectChatSourceAuthority) (confirmedProjectChatLink, error) {
	var result confirmedProjectChatLink
	if store == nil || store.pool == nil || token.OrganizationID != snapshot.Organization.Header.ID || token.PersonID != snapshot.Person.Header.ID ||
		thread.ID == "" || messageID == "" || strings.TrimSpace(text) == "" || !oneOf(token.Kind, "project", "create") || sourceAuthority.validate(snapshot.Person.Header.ID) != nil {
		return result, ErrProjectAuthorityInvalid
	}
	if token.Kind == "create" && sourceAuthority.Visibility != "private" {
		return result, ErrProjectAuthorityDenied
	}
	requestFingerprint := sha256Hex([]byte(strings.Join([]string{snapshot.Organization.Header.ID, thread.ID, messageID, strings.TrimSpace(text), token.Kind, token.ProjectID, fmt.Sprint(token.ProjectRevision), token.ProjectDigest, token.ProjectTitle, token.Basis, sourceAuthority.SourceType, sourceAuthority.Visibility, strings.Join(sourceAuthority.Principals, ","), fmt.Sprint(sourceAuthority.ACLRevision)}, "\x00")))
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
	var sequence int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM stride_conversation_events WHERE tenant_id=$1`, snapshot.Organization.Header.ID).Scan(&sequence); err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO stride_conversation_events(tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,occurred_at,ingested_at,event_type,content_revision,content_digest,audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,body_ref,structured_refs)
VALUES($1,$2,1,$3,1,$4,$5,$6,$7,$8,$9,$10,$10,'message',1,decode($11,'hex'),sha256(convert_to($12::jsonb::text,'UTF8')),$13,$14,'organization_default',1,'client',$15,'[]')`, snapshot.Organization.Header.ID, eventID, sequence, operationID, sourceAuthority.SourceType, messageID, thread.ID, snapshot.Person.Header.ID, scoutChatAuthorName(&userAccount{Name: snapshot.Person.Header.ID}), now, contentDigest, audience, sourceAuthority.Visibility, sourceAuthority.ACLRevision, "scout-chat://"+thread.ID+"/"+messageID)
	if err != nil {
		return result, err
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
