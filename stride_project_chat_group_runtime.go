package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type projectChatSourceGroupCorrectionMember struct {
	ordinal             int
	associationID       string
	associationRevision int64
	priorDigest         string
}

func (store *PostgresCanonicalStore) committedProjectChatSourceGroupCorrection(ctx context.Context, organizationID, actorPersonID,
	operationID string, token projectChatCorrectionToken) (confirmedProjectChatCorrection, bool, error) {
	result := confirmedProjectChatCorrection{}
	var replacementGroupID, projectID, projectTitle *string
	var projectRevision *int64
	err := store.pool.QueryRow(ctx, `SELECT receipt.result_state,receipt.context_revision,old_group.root_association_id,
old_group.root_association_revision,receipt.replacement_group_id,replacement_group.project_id,replacement_group.project_revision,project_revision.title
FROM stride_project_chat_source_group_correction_receipts receipt
JOIN stride_project_chat_source_groups old_group ON old_group.organization_id=receipt.organization_id AND old_group.group_id=receipt.old_group_id
LEFT JOIN stride_project_chat_source_groups replacement_group ON replacement_group.organization_id=receipt.organization_id AND replacement_group.group_id=receipt.replacement_group_id
LEFT JOIN stride_project_revisions project_revision ON project_revision.organization_id=replacement_group.organization_id
 AND project_revision.project_id=replacement_group.project_id AND project_revision.revision=replacement_group.project_revision
WHERE receipt.organization_id=$1 AND receipt.operation_id=$2 AND receipt.actor_person_id=$3
 AND old_group.root_association_id=$4 AND old_group.root_association_revision=$5`, organizationID, operationID, actorPersonID,
		token.OldAssociationID, token.OldAssociationRevision).Scan(&result.Status, &result.ContextRevision, &result.OldAssociationID,
		&result.OldAssociationRevision, &replacementGroupID, &projectID, &projectRevision, &projectTitle)
	if err == pgx.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	result.OldResultRevision = result.OldAssociationRevision + 1
	if result.Status == "corrected" {
		if replacementGroupID == nil || projectID == nil || projectRevision == nil || projectTitle == nil ||
			token.Target.Kind != "project" || *projectID != token.Target.ProjectID || *projectRevision != token.Target.ProjectRevision {
			return result, false, ErrProjectAuthorityConflict
		}
		result.Status = "confirmed"
		result.ProjectID, result.ProjectRevision, result.ProjectTitle = *projectID, *projectRevision, *projectTitle
		if err = store.pool.QueryRow(ctx, `SELECT root_association_id,root_association_revision FROM stride_project_chat_source_groups
WHERE organization_id=$1 AND group_id=$2`, organizationID, *replacementGroupID).Scan(&result.AssociationID, &result.AssociationRevision); err != nil {
			return result, false, err
		}
	} else if token.Target.Kind != "remove" {
		return result, false, ErrProjectAuthorityConflict
	}
	return result, true, nil
}

func (store *PostgresCanonicalStore) replaceProjectChatSourceGroup(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot,
	groupID, operationID string, token projectChatCorrectionToken) (confirmedProjectChatCorrection, error) {
	result := confirmedProjectChatCorrection{Status: "confirmed", ContextRevision: token.ContextRevision + 1,
		OldAssociationID: token.OldAssociationID, OldAssociationRevision: token.OldAssociationRevision,
		OldResultRevision: token.OldAssociationRevision + 1, ProjectID: token.Target.ProjectID,
		ProjectRevision: token.Target.ProjectRevision}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if err = syncProjectSessionAuthority(ctx, tx, snapshot); err != nil {
		return result, err
	}
	operationKey := sha256Hex([]byte("project-chat-group-correction/v1\x00" + operationID))
	var manifestDigest, threadID, messageID, eventID string
	var memberCount int
	err = tx.QueryRow(ctx, `SELECT encode(source_manifest_digest,'hex'),thread_id,message_id,conversation_event_id,member_count
FROM stride_project_chat_source_groups WHERE organization_id=$1 AND group_id=$2 FOR UPDATE`, snapshot.Organization.Header.ID, groupID).
		Scan(&manifestDigest, &threadID, &messageID, &eventID, &memberCount)
	if err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT revision.title FROM stride_projects_current current_project
JOIN stride_project_revisions revision ON revision.organization_id=current_project.organization_id AND revision.project_id=current_project.project_id AND revision.revision=current_project.revision
WHERE current_project.organization_id=$1 AND current_project.project_id=$2 AND current_project.revision=$3
 AND encode(current_project.content_digest,'hex')=$4 AND current_project.lifecycle<>'archived'
 AND (revision.audience->'principals' @> jsonb_build_array($5::text)
   OR revision.controller_memberships @> jsonb_build_array(jsonb_build_object('contractType','organization_membership','id',$6::text,'revision',$7::bigint,'digest',$8::text)))`,
		snapshot.Organization.Header.ID, token.Target.ProjectID, token.Target.ProjectRevision, token.Target.ProjectDigest,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.Membership.Header.ContentDigest).
		Scan(&result.ProjectTitle); err != nil {
		if err == pgx.ErrNoRows {
			return result, errHomeProjectStale
		}
		return result, err
	}
	rows, err := tx.Query(ctx, `SELECT member.ordinal,member.association_id,member.association_revision,encode(revision.content_digest,'hex')
FROM stride_project_chat_source_group_members member JOIN stride_project_association_revisions revision
 ON revision.organization_id=member.organization_id AND revision.association_id=member.association_id AND revision.revision=member.association_revision
JOIN stride_project_associations_authorized_current current_association ON current_association.organization_id=member.organization_id
 AND current_association.association_id=member.association_id AND current_association.revision=member.association_revision AND current_association.state='confirmed'
WHERE member.organization_id=$1 AND member.group_id=$2 ORDER BY member.ordinal`, snapshot.Organization.Header.ID, groupID)
	if err != nil {
		return result, err
	}
	var members []projectChatSourceGroupCorrectionMember
	for rows.Next() {
		var member projectChatSourceGroupCorrectionMember
		if err = rows.Scan(&member.ordinal, &member.associationID, &member.associationRevision, &member.priorDigest); err != nil {
			rows.Close()
			return result, err
		}
		members = append(members, member)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(members) != memberCount || memberCount < 1 || members[0].associationID != token.OldAssociationID || members[0].associationRevision != token.OldAssociationRevision {
		return result, ErrProjectAuthorityConflict
	}
	now := time.Now().UTC()
	replacementGroupID := projectChatID("project_source_group_correction", snapshot.Organization.Header.ID, groupID, operationID)
	replacementIDs := make([]string, len(members))
	replacementDigests := make([]string, len(members))
	for index, member := range members {
		edgeKey := sha256Hex([]byte(fmt.Sprintf("project-chat-group-correction-edge/v1\x1f%s\x1f%d\x1f%s", operationKey, member.ordinal, member.associationID)))
		replacementIDs[index] = projectChatID("project_group_replacement_association", snapshot.Organization.Header.ID, groupID, operationID, fmt.Sprint(member.ordinal))
		replacementDigests[index] = sha256Hex([]byte(fmt.Sprintf("project-association/group-correction-confirmed/v1\x1f%s\x1f%s\x1f%s\x1f%d", replacementIDs[index], token.Target.ProjectDigest, member.priorDigest, member.ordinal)))
		receiptID := projectChatID("project_group_replacement_source", snapshot.Organization.Header.ID, groupID, operationID, fmt.Sprint(member.ordinal))
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(source_authority_receipt_id,organization_id,
subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,source_acl_revision,
source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,
session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
SELECT $1,prior.organization_id,prior.subject_contract_type,prior.subject_id,prior.subject_revision,prior.subject_digest,prior.source_refs,
prior.evidence_coverage_digest,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,prior.consent_revision,prior.purge_generation,
$2,$3,$4,decode($5,'hex'),$6,$7,decode($8,'hex'),decode($9,'hex'),$10,$11
FROM stride_project_association_revisions prior WHERE prior.organization_id=$12 AND prior.association_id=$13 AND prior.revision=$14`, receiptID,
			snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
			snapshot.ActiveSession.SessionRevision, snapshot.Generation, edgeKey,
			sha256Hex([]byte("project-chat-group-replacement-source/v1\x00"+operationID+"\x00"+fmt.Sprint(member.ordinal))),
			now, now.Add(30*time.Minute), snapshot.Organization.Header.ID, member.associationID, member.associationRevision); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT $1,1,prior.organization_id,$2,$3,prior.subject_contract_type,prior.subject_id,prior.subject_revision,prior.subject_digest,prior.source_refs,
$16,prior.evidence_coverage_digest,'confirmed',prior.basis,prior.classifier_revision,prior.confidence,$4,$5,$6,
decode($7,'hex'),$8,$9,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,prior.consent_revision,prior.purge_generation,
decode($10,'hex'),NULL,NULL,NULL,$11,decode($12,'hex') FROM stride_project_association_revisions prior
WHERE prior.organization_id=$13 AND prior.association_id=$14 AND prior.revision=$15`, replacementIDs[index], result.ProjectID, result.ProjectRevision,
			snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
			snapshot.ActiveSession.SessionRevision, snapshot.Generation, edgeKey, now, replacementDigests[index], snapshot.Organization.Header.ID,
			member.associationID, member.associationRevision, receiptID); err != nil {
			return result, err
		}
	}
	for index, member := range members {
		edgeKey := sha256Hex([]byte(fmt.Sprintf("project-chat-group-correction-edge/v1\x1f%s\x1f%d\x1f%s", operationKey, member.ordinal, member.associationID)))
		terminalDigest := sha256Hex([]byte(fmt.Sprintf("project-association/group-corrected/v1\x1f%s\x1f%s\x1f%s", member.associationID, member.priorDigest, replacementDigests[index])))
		correctionID := projectChatID("project_group_edge_correction", snapshot.Organization.Header.ID, groupID, operationID, fmt.Sprint(member.ordinal))
		oldEventID := projectChatID("project_group_old_event", snapshot.Organization.Header.ID, groupID, operationID, fmt.Sprint(member.ordinal))
		replacementEventID := projectChatID("project_group_replacement_event", snapshot.Organization.Header.ID, groupID, operationID, fmt.Sprint(member.ordinal))
		requestFingerprint := sha256Hex([]byte("project-chat-group-edge-correction/v1\x00" + operationID + "\x00" + fmt.Sprint(member.ordinal)))
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,replacement_association_id,replacement_association_revision,replacement_association_digest,recorded_at,content_digest)
SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'corrected',prior.basis,
prior.classifier_revision,prior.confidence,$4,$5,$6,decode($7,'hex'),$8,$9,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,
prior.consent_revision,prior.purge_generation,decode($10,'hex'),NULL,prior.revision,prior.content_digest,$11,1,decode($12,'hex'),$13,decode($14,'hex')
FROM stride_project_association_revisions prior WHERE prior.organization_id=$1 AND prior.association_id=$2 AND prior.revision=$3`, snapshot.Organization.Header.ID,
			member.associationID, member.associationRevision, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision,
			snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, edgeKey, replacementIDs[index], replacementDigests[index], now, terminalDigest); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,
prior_revision,new_revision,replacement_association_id,replacement_association_revision,replacement_association_digest,correction_id,actor_person_id,
actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
VALUES($1,$2,$3,$4,'correct','corrected',$5,$4,$6,1,decode($7,'hex'),$8,$9,$10,$11,decode($12,'hex'),$13,$14,decode($15,'hex'),decode($16,'hex'),$17),
($18,$2,$6,1,'confirm','confirmed',0,1,NULL,NULL,NULL,$8,$9,$10,$11,decode($12,'hex'),$13,$14,decode($15,'hex'),decode($16,'hex'),$17)`,
			oldEventID, snapshot.Organization.Header.ID, member.associationID, member.associationRevision+1, member.associationRevision,
			replacementIDs[index], replacementDigests[index], correctionID, snapshot.Person.Header.ID, snapshot.Membership.Header.ID,
			snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, edgeKey, requestFingerprint,
			now, replacementEventID); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_correction_receipts(correction_id,organization_id,old_association_id,old_association_revision,
replacement_association_id,replacement_association_revision,old_event_id,replacement_event_id,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at)
VALUES($1,$2,$3,$4,$5,1,$6,$7,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),$16)`, correctionID,
			snapshot.Organization.Header.ID, member.associationID, member.associationRevision+1, replacementIDs[index], oldEventID, replacementEventID,
			snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
			snapshot.ActiveSession.SessionRevision, snapshot.Generation, edgeKey, requestFingerprint, now); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `UPDATE stride_project_associations_current SET revision=$2,state='corrected',content_digest=decode($3,'hex'),updated_at=$4
WHERE organization_id=$5 AND association_id=$1 AND revision=$6 AND state='confirmed'`, member.associationID, member.associationRevision+1,
			terminalDigest, now, snapshot.Organization.Header.ID, member.associationRevision); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at)
VALUES($1,1,$2,$3,'confirmed',decode($4,'hex'),$5)`, replacementIDs[index], snapshot.Organization.Header.ID, result.ProjectID,
			replacementDigests[index], now); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,projection_family,
source_ref_digest,authority_digest,status,attempts,next_attempt_at)
SELECT $1,$2,$3,'unlist_old',family.name,decode($4,'hex'),decode($5,'hex'),'pending',0,$6::timestamptz FROM (VALUES('home'),('work'),('board'),('project_record')) family(name)
UNION ALL SELECT $1,$7,1,'list_new',family.name,decode($4,'hex'),decode($5,'hex'),'pending',0,$6::timestamptz FROM (VALUES('home'),('work'),('board'),('project_record')) family(name)`,
			snapshot.Organization.Header.ID, member.associationID, member.associationRevision+1, member.priorDigest,
			sha256Hex([]byte(snapshot.SessionHash+"\x00"+fmt.Sprint(snapshot.Generation))), now, replacementIDs[index]); err != nil {
			return result, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_invalidations(organization_id,group_id,operation_id,operation_key_digest,
request_fingerprint,reason,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,'project_correction',$5::text,
$6::text,$7::bigint::text,encode(decode($8,'hex'),'hex'),$9::bigint::text,$10::bigint::text,$11::timestamptz::text),'UTF8')),
'project_correction',$5,$6,$7,decode($8,'hex'),$9,$10,$11)`, snapshot.Organization.Header.ID, groupID, operationID, operationKey,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
		snapshot.ActiveSession.SessionRevision, snapshot.Generation, now); err != nil {
		return result, err
	}
	replacementOperationKey := sha256Hex([]byte(fmt.Sprintf("project-chat-group-correction-replacement/v1\x1f%s\x1f%s", operationKey, replacementGroupID)))
	groupFingerprint := projectChatSourceGroupRequestFingerprint(snapshot, replacementGroupID, operationID, replacementOperationKey, manifestDigest,
		threadID, messageID, eventID, result.ProjectID, result.ProjectRevision, replacementIDs[0], 1, memberCount)
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,
source_manifest_digest,thread_id,message_id,conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,
root_association_revision,member_count,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,
authority_generation,status,recorded_at) VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,1,$13,
$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',$20)`, snapshot.Organization.Header.ID, replacementGroupID, operationID,
		replacementOperationKey, groupFingerprint, manifestDigest, threadID, messageID, eventID, result.ProjectID, result.ProjectRevision,
		replacementIDs[0], memberCount, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision,
		snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, now); err != nil {
		return result, err
	}
	for index, member := range members {
		if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(organization_id,group_id,ordinal,subject_contract_type,subject_id,
subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
SELECT prior.organization_id,$2,$3,prior.subject_contract_type,prior.subject_id,prior.subject_revision,prior.subject_digest,
prior.source_authority_receipt_id,$4,1,$5 FROM stride_project_association_revisions prior
WHERE prior.organization_id=$1 AND prior.association_id=$4 AND prior.revision=1`, snapshot.Organization.Header.ID, replacementGroupID,
			member.ordinal, replacementIDs[index], now); err != nil {
			return result, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_correction_receipts(organization_id,operation_id,operation_key_digest,
request_fingerprint,old_group_id,replacement_group_id,result_state,context_revision,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,decode($3,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,$5::text,'corrected',$6::bigint::text,
$7::text,$8::text,$9::bigint::text,encode(decode($10,'hex'),'hex'),$11::bigint::text,$12::bigint::text,$13::timestamptz::text),'UTF8')),
$4,$5,'corrected',$6,$7,$8,$9,decode($10,'hex'),$11,$12,$13)`, snapshot.Organization.Header.ID, operationID, operationKey, groupID,
		replacementGroupID, result.ContextRevision, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision,
		snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, now); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.AssociationID = replacementIDs[0]
	result.AssociationRevision = 1
	return result, nil
}

func (app *kanbanBoardApp) commitScoutProjectSourceGroupAttachments(user *userAccount, thread scoutChatThreadRecord, operationID string) error {
	if app == nil || user == nil {
		return ErrProjectAuthorityInvalid
	}
	var operation *scoutChatProjectLinkOperation
	for index := range thread.ProjectLinkOperations {
		if thread.ProjectLinkOperations[index].OperationID == operationID {
			operation = &thread.ProjectLinkOperations[index]
			break
		}
	}
	if operation == nil || operation.MessageID == "" || len(operation.AttachmentSources) == 0 {
		return nil
	}
	app.pendingAttachmentUploadsMu.Lock()
	defer app.pendingAttachmentUploadsMu.Unlock()
	if app.attachmentSourceStoreErr != nil {
		return fmt.Errorf("attachment source authority is unavailable")
	}
	for _, source := range operation.AttachmentSources {
		grant, ok := app.pendingAttachmentUploads[source.SourceID]
		if !ok || grant.OwnerEmail != normalizeAccountEmail(user.Email) || grant.SourceRevision != source.SourceRevision ||
			grant.Ref != source.BlobRef || grant.DestinationID != thread.ID || grant.DestinationRevision != source.DestinationRevision ||
			grant.OriginFileID != source.OriginFileID || grant.OriginRevision != source.OriginRevision {
			return ErrProjectAuthorityConflict
		}
		if grant.State == attachmentSourceCommitted {
			if grant.CommittedMessageID != operation.MessageID {
				return ErrProjectAuthorityConflict
			}
			continue
		}
		if grant.State != attachmentSourceReserved || grant.ReservationID != operation.ReservationID || !grant.ExpiresAt.After(time.Now().UTC()) {
			return ErrProjectAuthorityConflict
		}
		grant.State = attachmentSourceCommitted
		grant.CommittedMessageID = operation.MessageID
		grant.ReservationID = ""
		grant.ReservedAt = time.Time{}
		app.pendingAttachmentUploads[source.SourceID] = grant
	}
	return app.persistAttachmentSourceStoreLocked()
}

func (app *kanbanBoardApp) markScoutProjectSourceGroupDrift(user *userAccount, threadID, operationID string, confirmed bool) error {
	if app == nil || user == nil || !strideIdentifier(threadID) || !strideIdentifier(operationID) {
		return ErrProjectAuthorityInvalid
	}
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return err
	}
	found := false
	for index := range thread.ProjectLinkOperations {
		operation := &thread.ProjectLinkOperations[index]
		if operation.OperationID != operationID || operation.SourceGroupID == "" {
			continue
		}
		nextState := map[bool]string{false: "drift_pending", true: "drifted"}[confirmed]
		if operation.State == nextState {
			return nil
		}
		operation.State = nextState
		messageIndex := scoutChatMessageIndex(thread, operation.MessageID)
		if messageIndex >= 0 && thread.Messages[messageIndex].Project != nil {
			thread.Messages[messageIndex].Project.Status = "unavailable"
			thread.Messages[messageIndex].Project.ContextRevision++
		}
		found = true
	}
	if !found {
		return ErrProjectAuthorityNotFound
	}
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return app.saveScoutChatThread(thread)
}

func (app *kanbanBoardApp) finishCommittedScoutProjectSourceGroupDrift(ctx context.Context, user *userAccount, threadID,
	operationID, organizationID, groupID, driftOperationID string) error {
	store := currentHomeProjectStore()
	if store == nil {
		return errHomeProjectUnavailable
	}
	committed, err := store.committedProjectChatSourceGroupDrift(ctx, organizationID, groupID, driftOperationID)
	if err != nil {
		return err
	}
	if !committed {
		return ErrProjectAuthorityConflict
	}
	return app.markScoutProjectSourceGroupDrift(user, threadID, operationID, true)
}

func (app *kanbanBoardApp) finishCommittedScoutProjectSourceGroupAuthorityLoss(ctx context.Context, user *userAccount, threadID,
	operationID, organizationID, groupID, authorityOperationID string) error {
	store := currentHomeProjectStore()
	if store == nil {
		return errHomeProjectUnavailable
	}
	committed, err := store.committedProjectChatSourceGroupAuthorityLoss(ctx, organizationID, groupID, authorityOperationID)
	if err != nil {
		return err
	}
	if !committed {
		return ErrProjectAuthorityConflict
	}
	return app.markScoutProjectSourceGroupDrift(user, threadID, operationID, true)
}

// invalidateProjectChatAttachmentGroupForDrift is server-owned terminal
// recovery. It requires an exact current canonical part, appends its one-way
// invalidation revision, then atomically revokes every group edge and emits the
// four purge families. It deliberately does not require the original session
// to remain active.
func (store *PostgresCanonicalStore) invalidateProjectChatAttachmentGroupForDrift(ctx context.Context, organizationID, groupID, operationID string) error {
	return store.invalidateProjectChatSourceGroupForDrift(ctx, organizationID, groupID, operationID, "rich_message_part", "origin_revoked")
}

func (store *PostgresCanonicalStore) invalidateProjectChatRootGroupForMutation(ctx context.Context, organizationID, groupID, operationID, reason string) error {
	if !oneOf(reason, "source_edited", "source_deleted") {
		return ErrProjectAuthorityInvalid
	}
	return store.invalidateProjectChatSourceGroupForDrift(ctx, organizationID, groupID, operationID, "conversation_event", reason)
}

func (store *PostgresCanonicalStore) invalidateProjectChatReplyGroupForDrift(ctx context.Context, organizationID, groupID, operationID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return ErrProjectAuthorityInvalid
	}
	return store.invalidateProjectChatSourceGroupForDrift(ctx, organizationID, groupID, operationID, "reply_dependency", reason)
}

func (store *PostgresCanonicalStore) projectChatReplyGroupsForParent(ctx context.Context, organizationID, parentEventID string) ([]string, error) {
	rows, err := store.pool.Query(ctx, `SELECT source_group.group_id FROM stride_project_chat_reply_dependencies dependency
JOIN stride_project_chat_source_groups source_group ON source_group.organization_id=dependency.organization_id
 AND source_group.conversation_event_id=dependency.child_event_id
WHERE dependency.organization_id=$1 AND dependency.parent_event_id=$2
 AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift
  WHERE drift.organization_id=source_group.organization_id AND drift.group_id=source_group.group_id)
 AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation
  WHERE invalidation.organization_id=source_group.organization_id AND invalidation.group_id=source_group.group_id)`, organizationID, parentEventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []string
	for rows.Next() {
		var groupID string
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		groups = append(groups, groupID)
	}
	return groups, rows.Err()
}

type projectChatSourceGroupRef struct {
	organizationID string
	groupID        string
}

func (store *PostgresCanonicalStore) activeProjectChatGroupsForAttachmentSource(ctx context.Context, sourceID string) ([]projectChatSourceGroupRef, error) {
	rows, err := store.pool.Query(ctx, `SELECT source_group.organization_id,source_group.group_id
FROM stride_project_chat_source_groups source_group JOIN stride_project_chat_source_group_members member
 ON member.organization_id=source_group.organization_id AND member.group_id=source_group.group_id
JOIN stride_rich_message_part_revisions part ON part.organization_id=member.organization_id AND part.part_id=member.subject_id
 AND part.revision=member.subject_revision
WHERE member.subject_contract_type='rich_message_part' AND part.source_id=$1
 AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift
  WHERE drift.organization_id=source_group.organization_id AND drift.group_id=source_group.group_id)
 AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation
  WHERE invalidation.organization_id=source_group.organization_id AND invalidation.group_id=source_group.group_id)`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []projectChatSourceGroupRef
	for rows.Next() {
		var group projectChatSourceGroupRef
		if err := rows.Scan(&group.organizationID, &group.groupID); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (store *PostgresCanonicalStore) invalidateProjectChatSourceGroupForDrift(ctx context.Context, organizationID, groupID, operationID, contractType, reason string) error {
	if store == nil || store.pool == nil || !strideIdentifier(organizationID) || !strideIdentifier(groupID) || !strideIdentifier(operationID) {
		return ErrProjectAuthorityInvalid
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var committed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts
WHERE organization_id=$1 AND group_id=$2 AND operation_id=$3)`, organizationID, groupID, operationID).Scan(&committed); err != nil {
		return err
	}
	if committed {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1||E'\x1f'||$2,0))`, organizationID, groupID); err != nil {
		return err
	}
	var subjectID, subjectDigest string
	var subjectRevision int64
	if contractType == "rich_message_part" {
		err = tx.QueryRow(ctx, `SELECT part.part_id,part.revision,encode(part.content_digest,'hex')
FROM stride_project_chat_source_group_members member
JOIN stride_rich_message_parts_current current_part ON current_part.organization_id=member.organization_id AND current_part.part_id=member.subject_id
JOIN stride_rich_message_part_revisions part ON part.organization_id=current_part.organization_id AND part.part_id=current_part.part_id AND part.revision=current_part.revision
WHERE member.organization_id=$1 AND member.group_id=$2 AND member.subject_contract_type='rich_message_part'
ORDER BY member.ordinal LIMIT 1 FOR SHARE`, organizationID, groupID).Scan(&subjectID, &subjectRevision, &subjectDigest)
	} else if contractType == "conversation_event" {
		err = tx.QueryRow(ctx, `SELECT member.subject_id,member.subject_revision,encode(member.subject_digest,'hex')
FROM stride_project_chat_source_group_members member
WHERE member.organization_id=$1 AND member.group_id=$2 AND member.ordinal=0 AND member.subject_contract_type='conversation_event' FOR SHARE`, organizationID, groupID).
			Scan(&subjectID, &subjectRevision, &subjectDigest)
	} else {
		err = tx.QueryRow(ctx, `SELECT dependency.parent_event_id,dependency.parent_event_revision,encode(dependency.parent_event_digest,'hex')
FROM stride_project_chat_source_groups source_group JOIN stride_project_chat_reply_dependencies dependency
 ON dependency.organization_id=source_group.organization_id AND dependency.child_event_id=source_group.conversation_event_id
WHERE source_group.organization_id=$1 AND source_group.group_id=$2 FOR SHARE`, organizationID, groupID).
			Scan(&subjectID, &subjectRevision, &subjectDigest)
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if contractType == "rich_message_part" {
		if _, err = tx.Exec(ctx, `INSERT INTO stride_rich_message_part_revisions(
organization_id,part_id,revision,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,source_origin_id,source_origin_revision,
blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,author_principal,source_audience,source_acl_revision,purge_generation,
recorded_at,invalidated_at,invalidation_reason,content_digest)
SELECT organization_id,part_id,revision+1,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,source_origin_id,source_origin_revision,
blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,author_principal,source_audience,source_acl_revision,purge_generation+1,
$4,$4,'origin_revoked',content_digest FROM stride_rich_message_part_revisions
WHERE organization_id=$1 AND part_id=$2 AND revision=$3 AND invalidated_at IS NULL`, organizationID, subjectID, subjectRevision, now); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE stride_rich_message_parts_current current_part SET revision=$3+1,content_digest=prior.content_digest,updated_at=$4
FROM stride_rich_message_part_revisions prior WHERE prior.organization_id=$1 AND prior.part_id=$2 AND prior.revision=$3
	 AND current_part.organization_id=prior.organization_id AND current_part.part_id=prior.part_id AND current_part.revision=prior.revision`, organizationID, subjectID, subjectRevision, now); err != nil {
			return err
		}
	} else if contractType == "conversation_event" && oneOf(reason, "source_edited", "source_deleted") {
		if _, err = tx.Exec(ctx, `UPDATE stride_conversation_events SET invalidated_at=$4,purge_generation=purge_generation+1
WHERE tenant_id=$1 AND event_id=$2 AND content_revision=$3 AND invalidated_at IS NULL`, organizationID, subjectID, subjectRevision, now); err != nil {
			return err
		}
	}
	driftKey := sha256Hex([]byte("project-chat-group-drift/v1\x00" + operationID))
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_drift_receipts(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,drift_contract_type,drift_subject_id,expected_revision,expected_digest,reason,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,
$5::text,$6::text,$7::bigint::text,$8::text,$9::text,$10::timestamptz::text),'UTF8')),
$5,$6,$7,decode($8,'hex'),$9,$10)`, organizationID, groupID, operationID, driftKey, contractType, subjectID, subjectRevision, subjectDigest, reason, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'revoked',prior.basis,
prior.classifier_revision,prior.confidence,prior.actor_person_id,prior.actor_membership_id,prior.actor_membership_revision,prior.session_subject_digest,
prior.session_revision,prior.authority_generation,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,prior.consent_revision,
prior.purge_generation,decode($3,'hex'),NULL,prior.revision,prior.content_digest,$4,
sha256(convert_to(concat_ws(E'\x1f','project-association/drift-revoked/v1',prior.association_id,(prior.revision+1)::text,
encode(prior.content_digest,'hex'),$5::text),'UTF8'))
FROM stride_project_chat_source_group_members member JOIN stride_project_association_revisions prior
 ON prior.organization_id=member.organization_id AND prior.association_id=member.association_id AND prior.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$2`, organizationID, groupID, driftKey, now, operationID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
SELECT $3||'_'||member.ordinal,prior.organization_id,prior.association_id,prior.revision+1,'revoke','revoked',prior.revision,prior.revision+1,
prior.actor_person_id,prior.actor_membership_id,prior.actor_membership_revision,prior.session_subject_digest,prior.session_revision,prior.authority_generation,
decode($4,'hex'),sha256(convert_to(concat_ws(E'\x1f','project-association-event/drift-revoke/v1',prior.organization_id,
prior.association_id,(prior.revision+1)::text,$3::text),'UTF8')),$5
FROM stride_project_chat_source_group_members member JOIN stride_project_association_revisions prior
 ON prior.organization_id=member.organization_id AND prior.association_id=member.association_id AND prior.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$2`, organizationID, groupID, operationID, driftKey, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE stride_project_associations_current current_association
SET revision=terminal.revision,state='revoked',content_digest=terminal.content_digest,updated_at=$3
FROM stride_project_chat_source_group_members member JOIN stride_project_association_revisions terminal
 ON terminal.organization_id=member.organization_id AND terminal.association_id=member.association_id AND terminal.revision=member.association_revision+1
WHERE member.organization_id=$1 AND member.group_id=$2 AND current_association.organization_id=member.organization_id
 AND current_association.association_id=member.association_id`, organizationID, groupID, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at)
SELECT member.organization_id,member.association_id,member.association_revision+1,'purge',family.name,decode($3,'hex'),decode($4,'hex'),'pending',0,$5
FROM stride_project_chat_source_group_members member CROSS JOIN (VALUES('home'),('work'),('board'),('project_record')) family(name)
WHERE member.organization_id=$1 AND member.group_id=$2`, organizationID, groupID,
		sha256Hex([]byte("project-chat-group-drift-source\x00"+operationID)), sha256Hex([]byte("project-chat-group-drift-authority\x00"+operationID)), now); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Project source group drift: %w", err)
	}
	return nil
}

func (store *PostgresCanonicalStore) committedProjectChatSourceGroupDrift(ctx context.Context, organizationID, groupID, operationID string) (bool, error) {
	var committed bool
	err := store.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts
WHERE organization_id=$1 AND group_id=$2 AND operation_id=$3)`, organizationID, groupID, operationID).Scan(&committed)
	return committed, err
}

func (store *PostgresCanonicalStore) invalidateProjectChatSourceGroupForAuthorityLoss(ctx context.Context, organizationID, groupID,
	operationID, reason string) error {
	if store == nil || store.pool == nil || !oneOf(reason, "project_archived", "project_audience_revoked") {
		return ErrProjectAuthorityInvalid
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var committed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stride_project_chat_source_group_authority_loss_receipts
WHERE organization_id=$1 AND group_id=$2 AND operation_id=$3)`, organizationID, groupID, operationID).Scan(&committed); err != nil {
		return err
	}
	if committed {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1||E'\x1f'||$2,0))`, organizationID, groupID); err != nil {
		return err
	}
	var projectID, projectDigest string
	var projectRevision int64
	if err = tx.QueryRow(ctx, `SELECT source_group.project_id,source_group.project_revision,encode(revision.content_digest,'hex')
FROM stride_project_chat_source_groups source_group JOIN stride_project_revisions revision ON revision.organization_id=source_group.organization_id
 AND revision.project_id=source_group.project_id AND revision.revision=source_group.project_revision
WHERE source_group.organization_id=$1 AND source_group.group_id=$2`, organizationID, groupID).Scan(&projectID, &projectRevision, &projectDigest); err != nil {
		return err
	}
	now := time.Now().UTC()
	operationKey := sha256Hex([]byte("project-chat-group-authority-loss/v1\x00" + operationID))
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_authority_loss_receipts(organization_id,group_id,operation_id,
operation_key_digest,request_fingerprint,project_id,expected_project_revision,expected_project_digest,reason,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,$5::text,
$6::bigint::text,$7::text,$8::text,$9::timestamptz::text),'UTF8')),$5,$6,decode($7,'hex'),$8,$9)`, organizationID, groupID,
		operationID, operationKey, projectID, projectRevision, projectDigest, reason, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'revoked',prior.basis,
prior.classifier_revision,prior.confidence,prior.actor_person_id,prior.actor_membership_id,prior.actor_membership_revision,prior.session_subject_digest,
prior.session_revision,prior.authority_generation,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,prior.consent_revision,
prior.purge_generation,decode($3,'hex'),NULL,prior.revision,prior.content_digest,$4,
sha256(convert_to(concat_ws(E'\x1f','project-association/drift-revoked/v1',prior.association_id,(prior.revision+1)::text,
encode(prior.content_digest,'hex'),$5::text),'UTF8')) FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions prior ON prior.organization_id=member.organization_id AND prior.association_id=member.association_id
 AND prior.revision=member.association_revision WHERE member.organization_id=$1 AND member.group_id=$2`, organizationID, groupID, operationKey, now, operationID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,
resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,
authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
SELECT $3||'_'||member.ordinal,prior.organization_id,prior.association_id,prior.revision+1,'revoke','revoked',prior.revision,prior.revision+1,
prior.actor_person_id,prior.actor_membership_id,prior.actor_membership_revision,prior.session_subject_digest,prior.session_revision,prior.authority_generation,
decode($4,'hex'),sha256(convert_to(concat_ws(E'\x1f','project-association-event/drift-revoke/v1',prior.organization_id,prior.association_id,
(prior.revision+1)::text,$3::text),'UTF8')),$5 FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions prior ON prior.organization_id=member.organization_id AND prior.association_id=member.association_id
 AND prior.revision=member.association_revision WHERE member.organization_id=$1 AND member.group_id=$2`, organizationID, groupID, operationID, operationKey, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE stride_project_associations_current current_association SET revision=terminal.revision,state='revoked',
content_digest=terminal.content_digest,updated_at=$3 FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions terminal ON terminal.organization_id=member.organization_id AND terminal.association_id=member.association_id
 AND terminal.revision=member.association_revision+1 WHERE member.organization_id=$1 AND member.group_id=$2
 AND current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id`, organizationID, groupID, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,
projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at)
SELECT member.organization_id,member.association_id,member.association_revision+1,'purge',family.name,decode($3,'hex'),decode($4,'hex'),'pending',0,$5
FROM stride_project_chat_source_group_members member CROSS JOIN (VALUES('home'),('work'),('board'),('project_record')) family(name)
WHERE member.organization_id=$1 AND member.group_id=$2`, organizationID, groupID,
		sha256Hex([]byte("project-authority-loss-source\x00"+operationID)), sha256Hex([]byte("project-authority-loss-authority\x00"+operationID)), now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresCanonicalStore) committedProjectChatSourceGroupAuthorityLoss(ctx context.Context, organizationID, groupID,
	operationID string) (bool, error) {
	var committed bool
	err := store.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stride_project_chat_source_group_authority_loss_receipts
WHERE organization_id=$1 AND group_id=$2 AND operation_id=$3)`, organizationID, groupID, operationID).Scan(&committed)
	return committed, err
}

func (store *PostgresCanonicalStore) projectChatSourceGroupAuthorityLossReason(ctx context.Context, organizationID, groupID string) (string, error) {
	var expectedRevision, currentRevision int64
	var lifecycle string
	var authorized bool
	err := store.pool.QueryRow(ctx, `SELECT source_group.project_revision,current_project.revision,current_project.lifecycle,
EXISTS(SELECT 1 FROM stride_project_revisions revision
 JOIN stride_organization_memberships_current membership ON membership.organization_id=revision.organization_id
  AND membership.person_id=source_group.actor_person_id AND membership.status='active'
 WHERE revision.organization_id=current_project.organization_id
 AND revision.project_id=current_project.project_id AND revision.revision=current_project.revision
 AND (revision.audience->'principals' @> jsonb_build_array(source_group.actor_person_id)
   OR revision.controller_memberships @> jsonb_build_array(jsonb_build_object('contractType','organization_membership',
     'id',membership.membership_id,'revision',membership.revision))))
FROM stride_project_chat_source_groups source_group JOIN stride_projects_current current_project
 ON current_project.organization_id=source_group.organization_id AND current_project.project_id=source_group.project_id
WHERE source_group.organization_id=$1 AND source_group.group_id=$2`, organizationID, groupID).
		Scan(&expectedRevision, &currentRevision, &lifecycle, &authorized)
	if err != nil {
		return "", err
	}
	if lifecycle == "archived" {
		return "project_archived", nil
	}
	_ = currentRevision
	_ = expectedRevision
	if !authorized {
		return "project_audience_revoked", nil
	}
	return "", nil
}

func (store *PostgresCanonicalStore) projectChatSourceGroupForAssociation(ctx context.Context, organizationID, associationID string) (string, int, error) {
	var groupID string
	var members int
	err := store.pool.QueryRow(ctx, `SELECT member.group_id,source_group.member_count
FROM stride_project_chat_source_group_members member JOIN stride_project_chat_source_groups source_group
 ON source_group.organization_id=member.organization_id AND source_group.group_id=member.group_id
WHERE member.organization_id=$1 AND member.association_id=$2`, organizationID, associationID).Scan(&groupID, &members)
	if err == pgx.ErrNoRows {
		return "", 0, nil
	}
	return groupID, members, err
}

func (store *PostgresCanonicalStore) removeProjectChatSourceGroup(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot,
	groupID, operationID string, token projectChatCorrectionToken) (confirmedProjectChatCorrection, error) {
	result := confirmedProjectChatCorrection{Status: "removed", ContextRevision: token.ContextRevision + 1,
		OldAssociationID: token.OldAssociationID, OldAssociationRevision: token.OldAssociationRevision,
		OldResultRevision: token.OldAssociationRevision + 1}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if err = syncProjectSessionAuthority(ctx, tx, snapshot); err != nil {
		return result, err
	}
	operationKey := sha256Hex([]byte("project-chat-group-correction/v1\x00" + operationID))
	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'removed',prior.basis,
prior.classifier_revision,prior.confidence,$4,$5,$6,decode($7,'hex'),$8,$9,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,
prior.consent_revision,prior.purge_generation,sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-edge/v1',$3::text,
member.ordinal::text,member.association_id),'UTF8')),NULL,prior.revision,prior.content_digest,$10,
sha256(convert_to(concat_ws(E'\x1f','project-association/group-remove/v1',prior.association_id,(prior.revision+1)::text,
encode(prior.content_digest,'hex'),$2::text),'UTF8'))
FROM stride_project_chat_source_group_members member JOIN stride_project_association_revisions prior
 ON prior.organization_id=member.organization_id AND prior.association_id=member.association_id AND prior.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$11`, snapshot.Organization.Header.ID, operationID, operationKey,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
		snapshot.ActiveSession.SessionRevision, snapshot.Generation, now, groupID); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
SELECT $3||'_'||member.ordinal,$1,member.association_id,member.association_revision+1,'remove','removed',member.association_revision,
member.association_revision+1,$4,$5,$6,decode($7,'hex'),$8,$9,sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-edge/v1',
$10::text,member.ordinal::text,member.association_id),'UTF8')),decode($11,'hex'),$12
FROM stride_project_chat_source_group_members member WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID,
		groupID, projectChatID("project_group_remove_event", operationID), snapshot.Person.Header.ID, snapshot.Membership.Header.ID,
		snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, operationKey,
		sha256Hex([]byte("project-group-remove-event\x00"+operationID)), now); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `UPDATE stride_project_associations_current current_association
SET revision=terminal.revision,state='removed',content_digest=terminal.content_digest,updated_at=$3
FROM stride_project_chat_source_group_members member JOIN stride_project_association_revisions terminal
 ON terminal.organization_id=member.organization_id AND terminal.association_id=member.association_id AND terminal.revision=member.association_revision+1
WHERE member.organization_id=$1 AND member.group_id=$2 AND current_association.organization_id=member.organization_id
 AND current_association.association_id=member.association_id`, snapshot.Organization.Header.ID, groupID, now); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at)
SELECT member.organization_id,member.association_id,member.association_revision+1,'purge',family.name,decode($3,'hex'),decode($4,'hex'),'pending',0,$5
FROM stride_project_chat_source_group_members member CROSS JOIN (VALUES('home'),('work'),('board'),('project_record')) family(name)
WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID, groupID,
		sha256Hex([]byte("project-group-remove-source\x00"+operationID)), sha256Hex([]byte("project-group-remove-authority\x00"+operationID)), now); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_invalidations(organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,reason,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,'project_correction',$5::text,
$6::text,$7::bigint::text,encode(decode($8,'hex'),'hex'),$9::bigint::text,$10::bigint::text,$11::timestamptz::text),'UTF8')),
'project_correction',$5,$6,$7,decode($8,'hex'),$9,$10,$11)`, snapshot.Organization.Header.ID, groupID, operationID, operationKey,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
		snapshot.ActiveSession.SessionRevision, snapshot.Generation, now); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_correction_receipts(organization_id,operation_id,operation_key_digest,request_fingerprint,old_group_id,replacement_group_id,result_state,context_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,decode($3,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,'','removed',$5::bigint::text,
$6::text,$7::text,$8::bigint::text,encode(decode($9,'hex'),'hex'),$10::bigint::text,$11::bigint::text,$12::timestamptz::text),'UTF8')),
$4,NULL,'removed',$5,$6,$7,$8,decode($9,'hex'),$10,$11,$12)`, snapshot.Organization.Header.ID, operationID, operationKey, groupID,
		result.ContextRevision, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision,
		snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, now); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}
