package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type projectChatTextGroupFixture struct {
	snapshot          StrideE10TenantAuthoritySnapshot
	thread            scoutChatThreadRecord
	link              confirmedProjectChatLink
	messageID         string
	eventID           string
	groupID           string
	manifestDigest    string
	partID            string
	partAssociationID string
}

type projectChatCorrectionMember struct {
	ordinal             int
	associationID       string
	associationRevision int64
	priorDigest         string
}

func seedProjectChatCorrectionPart(t *testing.T, ctx context.Context, store *PostgresCanonicalStore, snapshot StrideE10TenantAuthoritySnapshot, link confirmedProjectChatLink, eventID, suffix string) (projectChatManifestAttachment, string, string) {
	t.Helper()
	partID := "correction_part_" + suffix
	partAssociationID := "correction_part_association_" + suffix
	partDigest := sha256Hex([]byte("correction-part-" + suffix))
	blobDigest := sha256Hex([]byte("correction-part-blob-" + suffix))
	destinationDigest := sha256Hex([]byte("correction-part-destination-" + suffix))
	var audience string
	if err := store.pool.QueryRow(ctx, `SELECT source_audience::text FROM stride_project_association_revisions WHERE association_id=$1 AND revision=$2`, link.AssociationID, link.AssociationRevision).Scan(&audience); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_rich_message_part_revisions(
organization_id,part_id,revision,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,source_origin_id,source_origin_revision,
blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,author_principal,source_audience,source_acl_revision,purge_generation,recorded_at,content_digest)
VALUES($1,$2,1,$3,1,0,$4,$5,$6,$7,$8,decode($8,'hex'),'application/pdf',23,decode($9,'hex'),$10,$11,$12::jsonb,1,1,clock_timestamp(),decode($13,'hex'))`,
		snapshot.Organization.Header.ID, partID, eventID, "correction-source-"+suffix, "sha256:correction-source-"+suffix,
		"correction-origin-"+suffix, "correction-origin-revision-"+suffix, blobDigest, destinationDigest,
		"correction-destination-revision-"+suffix, snapshot.Person.Header.ID, audience, partDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_rich_message_parts_current(organization_id,part_id,revision,conversation_event_id,ordinal,content_digest,updated_at)
VALUES($1,$2,1,$3,0,decode($4,'hex'),clock_timestamp())`, snapshot.Organization.Header.ID, partID, eventID, partDigest); err != nil {
		t.Fatal(err)
	}
	partReceipt := "correction_part_receipt_" + suffix
	partRefs := `[{"contractType":"rich_message_part","id":"` + partID + `","revision":1,"digest":"` + partDigest + `"}]`
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(
source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,
source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,
session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
SELECT $1,$2,'rich_message_part',$3,1,decode($4,'hex'),$5::jsonb,decode($4,'hex'),$6::jsonb,1,
sha256(convert_to(concat_ws(E'\x1f',part.organization_id,part.part_id,part.revision::text,encode(part.content_digest,'hex'),
encode(sha256(convert_to(part.source_audience::text,'UTF8')),'hex'),part.source_acl_revision::text,part.purge_generation::text),'UTF8')),
1,1,$7,$8,$9,decode($10,'hex'),$11,$12,decode($13,'hex'),decode($14,'hex'),clock_timestamp(),clock_timestamp()+interval '30 minutes'
FROM stride_rich_message_part_revisions part WHERE part.organization_id=$2 AND part.part_id=$3 AND part.revision=1`, partReceipt,
		snapshot.Organization.Header.ID, partID, partDigest, partRefs, audience, snapshot.Person.Header.ID, snapshot.Membership.Header.ID,
		snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation,
		sha256Hex([]byte("correction-part-receipt-key-"+suffix)), sha256Hex([]byte("correction-part-receipt-fingerprint-"+suffix))); err != nil {
		t.Fatal(err)
	}
	proposedDigest := sha256Hex([]byte("correction-part-proposed-" + suffix))
	confirmedDigest := sha256Hex([]byte("correction-part-confirmed-" + suffix))
	for _, revision := range []struct {
		n                            int64
		state, digest, prior, action string
	}{{1, "proposed", proposedDigest, "", "propose"}, {2, "confirmed", confirmedDigest, proposedDigest, "confirm"}} {
		var expires any
		if revision.n == 1 {
			expires = time.Now().UTC().Add(15 * time.Minute)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT $1,$2,$3,$4,$5,'rich_message_part',$6,1,decode($7,'hex'),$8::jsonb,$9,decode($7,'hex'),$10,'selected','project_linker_v1',1,
$11,$12,$13,decode($14,'hex'),$15,$16,$17::jsonb,1,receipt.source_acl_digest,1,1,decode($18,'hex'),$19,$20,
CASE WHEN $21='' THEN NULL ELSE decode($21,'hex') END,clock_timestamp(),decode($22,'hex')
FROM stride_project_source_authority_receipts receipt WHERE receipt.organization_id=$3 AND receipt.source_authority_receipt_id=$9`,
			partAssociationID, revision.n, snapshot.Organization.Header.ID, link.ProjectID, link.ProjectRevision, partID, partDigest, partRefs,
			partReceipt, revision.state, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision,
			snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation, audience,
			sha256Hex([]byte("correction-part-association-key-"+suffix+revision.action)), expires, nullablePriorRevision(revision.n), revision.prior, revision.digest); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$4,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),clock_timestamp())`,
			"correction_part_event_"+suffix+"_"+revision.action, snapshot.Organization.Header.ID, partAssociationID, revision.n, revision.action,
			revision.state, revision.n-1, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision,
			snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation,
			sha256Hex([]byte("correction-part-event-key-"+suffix+revision.action)), sha256Hex([]byte("correction-part-event-fingerprint-"+suffix+revision.action))); err != nil {
			t.Fatal(err)
		}
		if revision.n == 1 {
			_, err := store.pool.Exec(ctx, `INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at) VALUES($1,1,$2,$3,'proposed',decode($4,'hex'),clock_timestamp())`, partAssociationID, snapshot.Organization.Header.ID, link.ProjectID, proposedDigest)
			if err != nil {
				t.Fatal(err)
			}
		} else if _, err := store.pool.Exec(ctx, `UPDATE stride_project_associations_current SET revision=2,state='confirmed',content_digest=decode($1,'hex'),updated_at=clock_timestamp() WHERE association_id=$2`, confirmedDigest, partAssociationID); err != nil {
			t.Fatal(err)
		}
	}
	return projectChatManifestAttachment{Ordinal: 0, SourceID: "correction-source-" + suffix, SourceRevision: "sha256:correction-source-" + suffix,
		BlobRef: blobDigest, BlobDigest: blobDigest, Mime: "application/pdf", Size: 23, DestinationRevision: "correction-destination-revision-" + suffix,
		OriginFileID: "correction-origin-" + suffix, OriginRevision: "correction-origin-revision-" + suffix}, partID, partAssociationID
}

func seedProjectChatTextGroup(t *testing.T, ctx context.Context, store *PostgresCanonicalStore, suffix string) projectChatTextGroupFixture {
	t.Helper()
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "correction_group_thread_" + suffix, OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	messageID := "correction_group_message_" + suffix
	text := "Correction group " + suffix
	link, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, messageID, "correction-group-seed-"+suffix, text, homeProjectContextToken{
		Kind: "create", ProjectTitle: "Correction Project " + suffix, Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	eventID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, messageID)
	attachment, partID, partAssociationID := seedProjectChatCorrectionPart(t, ctx, store, snapshot, link, eventID, suffix)
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text)),
		Attachments: []projectChatManifestAttachment{attachment}}
	manifest.Digest = projectChatManifestDigest(manifest)
	groupID := "correction_group_" + suffix
	operationID := "correction_group_seed_operation_" + suffix
	operationKey := sha256Hex([]byte("correction-group-seed-key-" + suffix))
	fingerprint := projectChatSourceGroupFingerprint(snapshot, groupID, operationID, operationKey, manifest.Digest, thread, messageID, eventID, link, 2)
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,
conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,$13,2,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',clock_timestamp())`,
		snapshot.Organization.Header.ID, groupID, operationID, operationKey, fingerprint, manifest.Digest, thread.ID, messageID, eventID,
		link.ProjectID, link.ProjectRevision, link.AssociationID, link.AssociationRevision, snapshot.Person.Header.ID,
		snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
		snapshot.ActiveSession.SessionRevision, snapshot.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(
organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,
association_id,association_revision,recorded_at)
SELECT organization_id,$2,0,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,
association_id,revision,clock_timestamp() FROM stride_project_association_revisions
WHERE organization_id=$1 AND association_id=$3 AND revision=$4
UNION ALL SELECT organization_id,$2,1,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,
association_id,revision,clock_timestamp() FROM stride_project_association_revisions
WHERE organization_id=$1 AND association_id=$5 AND revision=2`, snapshot.Organization.Header.ID, groupID, link.AssociationID, link.AssociationRevision, partAssociationID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return projectChatTextGroupFixture{snapshot: snapshot, thread: thread, link: link, messageID: messageID, eventID: eventID, groupID: groupID,
		manifestDigest: manifest.Digest, partID: partID, partAssociationID: partAssociationID}
}

func projectChatSourceGroupFingerprint(snapshot StrideE10TenantAuthoritySnapshot, groupID, operationID, operationKeyDigest, manifestDigest string, thread scoutChatThreadRecord, messageID, eventID string, link confirmedProjectChatLink, memberCount int) string {
	return sha256Hex([]byte(strings.Join([]string{"project-chat-source-group/v1", snapshot.Organization.Header.ID,
		groupID, operationID, operationKeyDigest, manifestDigest, thread.ID, messageID, eventID, "1", link.ProjectID,
		fmt.Sprint(link.ProjectRevision), link.AssociationID, fmt.Sprint(link.AssociationRevision), fmt.Sprint(memberCount),
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, fmt.Sprint(snapshot.Membership.Header.Revision), snapshot.SessionHash,
		fmt.Sprint(snapshot.ActiveSession.SessionRevision), fmt.Sprint(snapshot.Generation)}, "\x1f")))
}

func TestPostgresProjectChatSourceGroupManifestMatchesGoAndIsComplete(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "source_group_manifest_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	messageID, text := "source_group_manifest_message", "Exact root text"
	link, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, messageID, "source-group-manifest-seed", text, homeProjectContextToken{
		Kind: "create", ProjectTitle: "Manifest Project", Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text))}
	manifest.Digest = projectChatManifestDigest(manifest)
	groupID, operationID := "source_group_manifest_exact", "source_group_manifest_operation"
	operationKeyDigest := sha256Hex([]byte("source-group-manifest-key"))
	eventID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, messageID)
	fingerprint := projectChatSourceGroupFingerprint(snapshot, groupID, operationID, operationKeyDigest, manifest.Digest, thread, messageID, eventID, link, 1)
	// Exercise the deferred completeness guard before the valid group claims the
	// event. Reusing the event after a successful group commit would be rejected
	// by the earlier active-group claim guard and would not prove missing-member
	// completeness at all.
	missingGroup, missingOperation := "source_group_missing_member", "source_group_missing_member_operation"
	missingKey := sha256Hex([]byte("source-group-missing-member-key"))
	missingFingerprint := projectChatSourceGroupFingerprint(snapshot, missingGroup, missingOperation, missingKey, manifest.Digest, thread, messageID, eventID, link, 1)
	missingTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missingTx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,
conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,$13,1,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',clock_timestamp())`,
		snapshot.Organization.Header.ID, missingGroup, missingOperation, missingKey, missingFingerprint, manifest.Digest, thread.ID, messageID,
		eventID, link.ProjectID, link.ProjectRevision, link.AssociationID, link.AssociationRevision, snapshot.Person.Header.ID,
		snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation); err != nil {
		_ = missingTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := missingTx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "exact complete member truth") {
		t.Fatalf("missing-member group commit err=%v", err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,
conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,$13,1,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',clock_timestamp())`,
		snapshot.Organization.Header.ID, groupID, operationID, operationKeyDigest, fingerprint, manifest.Digest, thread.ID, messageID,
		eventID, link.ProjectID, link.ProjectRevision, link.AssociationID, link.AssociationRevision, snapshot.Person.Header.ID,
		snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(
organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,
association_id,association_revision,recorded_at)
SELECT organization_id,$2,0,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,
association_id,revision,clock_timestamp() FROM stride_project_association_revisions
WHERE organization_id=$1 AND association_id=$3 AND revision=$4`, snapshot.Organization.Header.ID, groupID, link.AssociationID, link.AssociationRevision); err != nil {
		t.Fatal(err)
	}
	var postgresDigest string
	if err := tx.QueryRow(ctx, `SELECT encode(stride_project_chat_source_group_manifest_digest($1,$2),'hex')`, snapshot.Organization.Header.ID, groupID).Scan(&postgresDigest); err != nil {
		t.Fatal(err)
	}
	if postgresDigest != manifest.Digest {
		var postgresCanonical string
		if err := tx.QueryRow(ctx, `SELECT concat_ws(chr(30),'project-chat-source-manifest/v2','thread',source_group.thread_id,
encode(root_event.content_digest,'hex'),COALESCE(string_agg(NULL,chr(30)) FILTER(WHERE member.ordinal>0),''),'')
FROM stride_project_chat_source_groups source_group JOIN stride_conversation_events root_event
ON root_event.tenant_id=source_group.organization_id AND root_event.event_id=source_group.conversation_event_id
JOIN stride_project_chat_source_group_members member ON member.organization_id=source_group.organization_id AND member.group_id=source_group.group_id
WHERE source_group.organization_id=$1 AND source_group.group_id=$2 GROUP BY source_group.thread_id,root_event.content_digest`, snapshot.Organization.Header.ID, groupID).Scan(&postgresCanonical); err != nil {
			t.Fatal(err)
		}
		goCanonical := strings.Join([]string{"project-chat-source-manifest/v2", "thread", thread.ID, manifest.TextDigest, "", ""}, "\x1e")
		t.Fatalf("manifest parity postgres=%s go=%s pg-bytes=%q go-bytes=%q", postgresDigest, manifest.Digest, postgresCanonical, goCanonical)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM stride_project_chat_source_group_members WHERE organization_id=$1 AND group_id=$2`, snapshot.Organization.Header.ID, groupID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("group member mutation err=%v", err)
	}
}

func TestPostgresRichMessagePartIsImmutableAndInvalidationPreservesExactIdentity(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "source_group_part_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	link, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, "source_group_part_message", "source-group-part-seed", "Part seed", homeProjectContextToken{
		Kind: "create", ProjectTitle: "Part Project", Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	eventID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, "source_group_part_message")
	audience := `{"visibility":"private","principals":["` + snapshot.Person.Header.ID + `"]}`
	partID := "rich_part_source_group_test"
	digest := strings.Repeat("a", 64)
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_rich_message_part_revisions(
organization_id,part_id,revision,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,
source_origin_id,source_origin_revision,blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,
author_principal,source_audience,source_acl_revision,purge_generation,recorded_at,content_digest)
VALUES($1,$2,1,$3,1,0,'attachment-source-test','sha256:source','file_test','file_revision_1',$4,decode($4,'hex'),
'application/pdf',10,decode($5,'hex'),'destination-v1',$6,$7::jsonb,1,1,clock_timestamp(),decode($8,'hex'))`,
		snapshot.Organization.Header.ID, partID, eventID, strings.Repeat("b", 64), strings.Repeat("c", 64), snapshot.Person.Header.ID, audience, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_rich_message_parts_current(organization_id,part_id,revision,conversation_event_id,ordinal,content_digest,updated_at)
VALUES($1,$2,1,$3,0,decode($4,'hex'),clock_timestamp())`, snapshot.Organization.Header.ID, partID, eventID, digest); err != nil {
		t.Fatal(err)
	}
	var legacyEventForeignKeys int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
WHERE conrelid='stride_project_source_authority_receipts'::regclass
  AND contype='f' AND confrelid='stride_conversation_events'::regclass`).Scan(&legacyEventForeignKeys); err != nil {
		t.Fatal(err)
	}
	if legacyEventForeignKeys != 0 {
		t.Fatalf("legacy event-only receipt foreign keys=%d", legacyEventForeignKeys)
	}
	partReceiptID := "project_source_rich_part_polymorphic_test"
	partRefs := `[{"contractType":"rich_message_part","id":"` + partID + `","revision":1,"digest":"` + digest + `"}]`
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(
source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,
source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
SELECT $1,$2,'rich_message_part',$3,1,decode($4,'hex'),$5::jsonb,decode($4,'hex'),$6::jsonb,1,
sha256(convert_to(concat_ws(E'\x1f',part.organization_id,part.part_id,part.revision::text,encode(part.content_digest,'hex'),
encode(sha256(convert_to(part.source_audience::text,'UTF8')),'hex'),part.source_acl_revision::text,part.purge_generation::text),'UTF8')),
1,1,$7,$8,$9,decode($10,'hex'),$11,$12,decode($13,'hex'),decode($14,'hex'),clock_timestamp(),clock_timestamp()+interval '30 minutes'
FROM stride_rich_message_part_revisions part
WHERE part.organization_id=$2 AND part.part_id=$3 AND part.revision=1`, partReceiptID, snapshot.Organization.Header.ID,
		partID, digest, partRefs, audience, snapshot.Person.Header.ID, snapshot.Membership.Header.ID,
		snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation,
		sha256Hex([]byte("rich-part-receipt-key")), sha256Hex([]byte("rich-part-receipt-fingerprint"))); err != nil {
		t.Fatalf("rich-part source receipt insert: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_rich_message_part_revisions SET media_type='image/png' WHERE part_id=$1 AND revision=1`, partID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("part revision mutation err=%v", err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO stride_rich_message_part_revisions(
organization_id,part_id,revision,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,
source_origin_id,source_origin_revision,blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,
author_principal,source_audience,source_acl_revision,purge_generation,recorded_at,invalidated_at,invalidation_reason,content_digest)
SELECT organization_id,part_id,2,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,
'file_tampered',source_origin_revision,blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,
author_principal,source_audience,source_acl_revision,purge_generation+1,clock_timestamp(),clock_timestamp(),'origin_revoked',content_digest
FROM stride_rich_message_part_revisions WHERE part_id=$1 AND revision=1`, partID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE stride_rich_message_parts_current SET revision=2,updated_at=clock_timestamp() WHERE part_id=$1`, partID); err == nil || !strings.Contains(err.Error(), "exact one-way invalidation") {
		t.Fatalf("changed-origin invalidation err=%v", err)
	}
	_ = link
}

func TestPostgresProjectAuthorizedCurrentRequiresConfirmedState(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "authorized_current_terminal_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	link, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, "authorized_current_terminal_message", "authorized-current-terminal-seed", "Terminal source", homeProjectContextToken{
		Kind: "create", ProjectTitle: "Terminal Project", Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current WHERE association_id=$1`, link.AssociationID).Scan(&before); err != nil || before != 1 {
		t.Fatalf("confirmed authorized rows=%d err=%v", before, err)
	}
	if _, err := store.pool.Exec(ctx, `ALTER TABLE stride_project_associations_current DROP CONSTRAINT stride_project_associations_c_association_id_revision_orga_fkey`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `ALTER TABLE stride_project_associations_current DISABLE TRIGGER stride_project_association_current_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_project_associations_current SET state='removed' WHERE association_id=$1`, link.AssociationID); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current WHERE association_id=$1`, link.AssociationID).Scan(&after); err != nil || after != 0 {
		t.Fatalf("terminal authorized rows=%d err=%v", after, err)
	}
}

func TestPostgresProjectChatReplyDependencyRequiresExactReadableParentReceipt(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "reply_dependency_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	parent, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, "reply_parent_message", "reply-parent-seed", "Parent", homeProjectContextToken{
		Kind: "create", ProjectTitle: "Reply Project", Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.confirmProjectChatSend(ctx, snapshot, thread, "reply_child_message", "reply-child-seed", "Child", homeProjectContextToken{
		Kind: "project", ProjectID: parent.ProjectID, ProjectRevision: parent.ProjectRevision, ProjectDigest: parent.ProjectDigest,
		ProjectTitle: parent.ProjectTitle, Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	}, privateProjectChatSourceAuthority(snapshot.Person.Header.ID))
	if err != nil {
		t.Fatal(err)
	}
	parentEventID := "conversation_event_unlinked_reply_parent"
	childEventID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, "reply_child_message")
	audience := `{"visibility":"private","principals":["` + snapshot.Person.Header.ID + `"]}`
	parentContentDigest := sha256Hex([]byte("Unlinked Parent"))
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_conversation_events(
tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,
occurred_at,ingested_at,event_type,content_revision,content_digest,audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,body_ref,structured_refs)
SELECT $1,$2,1,COALESCE(max(sequence),0)+1,1,'unlinked-reply-parent','private_chat_message','reply_unlinked_parent_message',$3,$4,$4,
clock_timestamp(),clock_timestamp(),'message',1,decode($5,'hex'),sha256(convert_to($6::jsonb::text,'UTF8')),'private',1,'organization_default',1,'client',$7,'[]'
FROM stride_conversation_events WHERE tenant_id=$1`, snapshot.Organization.Header.ID, parentEventID, thread.ID, snapshot.Person.Header.ID, parentContentDigest, audience, "scout-chat://"+thread.ID+"/reply_unlinked_parent_message"); err != nil {
		t.Fatal(err)
	}
	parentReceipt := "project_source_unlinked_reply_parent"
	parentRefs := `[{"contractType":"conversation_event","id":"` + parentEventID + `","revision":1,"digest":"` + parentContentDigest + `"}]`
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(
source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,
source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
SELECT $1,$2,'conversation_event',$3,1,decode($4,'hex'),$5::jsonb,decode($4,'hex'),$6::jsonb,1,
sha256(convert_to(concat_ws(E'\x1f',event.tenant_id,event.event_id,event.content_revision::text,encode(event.content_digest,'hex'),
encode(event.audience_digest,'hex'),event.visibility,event.acl_version::text,event.purge_generation::text),'UTF8')),1,1,$7,$8,$9,
decode($10,'hex'),$11,$12,decode($13,'hex'),decode($14,'hex'),clock_timestamp(),clock_timestamp()+interval '30 minutes'
FROM stride_conversation_events event WHERE event.tenant_id=$2 AND event.event_id=$3`, parentReceipt, snapshot.Organization.Header.ID,
		parentEventID, parentContentDigest, parentRefs, audience, snapshot.Person.Header.ID, snapshot.Membership.Header.ID,
		snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation,
		sha256Hex([]byte("unlinked-parent-key")), sha256Hex([]byte("unlinked-parent-fingerprint"))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_conversation_events SET reply_to_event_id=$1 WHERE tenant_id=$2 AND event_id=$3`, parentEventID, snapshot.Organization.Header.ID, childEventID); err != nil {
		t.Fatal(err)
	}
	var parentAssociations int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_association_revisions WHERE organization_id=$1 AND subject_id=$2`, snapshot.Organization.Header.ID, parentEventID).Scan(&parentAssociations); err != nil || parentAssociations != 0 {
		t.Fatalf("unlinked parent associations=%d err=%v", parentAssociations, err)
	}
	legacyDigest := sha256Hex([]byte("legacy parent snapshot"))
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte("Child")),
		Reply: &projectChatManifestReply{MessageID: "reply_unlinked_parent_message", EventID: parentEventID, SourceRevision: 1,
			SourceDigest: parentContentDigest, LegacyDigest: legacyDigest, AuthorPersonID: snapshot.Person.Header.ID,
			AudienceDigest: "", ACLRevision: 1, PurgeGeneration: 1}}
	if err := store.pool.QueryRow(ctx, `SELECT encode(audience_digest,'hex') FROM stride_conversation_events WHERE tenant_id=$1 AND event_id=$2`, snapshot.Organization.Header.ID, parentEventID).Scan(&manifest.Reply.AudienceDigest); err != nil {
		t.Fatal(err)
	}
	manifestDigest := projectChatManifestDigest(manifest)
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_chat_reply_dependencies(
organization_id,child_event_id,child_event_revision,parent_event_id,parent_event_revision,parent_event_digest,parent_author_principal,
parent_legacy_snapshot_digest,parent_audience_digest,parent_acl_revision,parent_purge_generation,parent_source_authority_receipt_id,
source_manifest_digest,recorded_at)
SELECT tenant_id,$2,1,event_id,content_revision,content_digest,author_principal,decode($3,'hex'),audience_digest,acl_version,purge_generation,$4,decode($5,'hex'),clock_timestamp()
FROM stride_conversation_events WHERE tenant_id=$1 AND event_id=$6`, snapshot.Organization.Header.ID, childEventID, legacyDigest, parentReceipt, manifestDigest, parentEventID); err != nil {
		t.Fatal(err)
	}
	groupID, operationID := "reply_child_source_group", "reply_child_source_group_operation"
	operationKey := sha256Hex([]byte("reply-child-source-group-key"))
	fingerprint := projectChatSourceGroupFingerprint(snapshot, groupID, operationID, operationKey, manifestDigest, thread, "reply_child_message", childEventID, child, 1)
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,
conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,$13,1,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',clock_timestamp())`,
		snapshot.Organization.Header.ID, groupID, operationID, operationKey, fingerprint, manifestDigest, thread.ID, "reply_child_message", childEventID,
		child.ProjectID, child.ProjectRevision, child.AssociationID, child.AssociationRevision, snapshot.Person.Header.ID, snapshot.Membership.Header.ID,
		snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(
organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
SELECT organization_id,$2,0,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,revision,clock_timestamp()
FROM stride_project_association_revisions WHERE organization_id=$1 AND association_id=$3 AND revision=$4`, snapshot.Organization.Header.ID, groupID, child.AssociationID, child.AssociationRevision); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	guessedChildEventID := "conversation_event_reply_guessed_digest_child"
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_conversation_events(
tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,thread_id,author_principal,author_name,
occurred_at,ingested_at,event_type,content_revision,content_digest,reply_to_event_id,audience_digest,visibility,acl_version,
retention_policy,purge_generation,provenance,body_ref,structured_refs)
SELECT $1,$2,1,COALESCE(max(sequence),0)+1,1,'reply-guessed-digest-child','private_chat_message','reply_guessed_digest_child',$3,$4,$4,
clock_timestamp(),clock_timestamp(),'reply',1,decode($5,'hex'),$6,sha256(convert_to($7::jsonb::text,'UTF8')),'private',1,
'organization_default',1,'client',$8,'[]'
FROM stride_conversation_events WHERE tenant_id=$1`, snapshot.Organization.Header.ID, guessedChildEventID, thread.ID,
		snapshot.Person.Header.ID, sha256Hex([]byte("Guessed digest child")), parentEventID, audience,
		"scout-chat://"+thread.ID+"/reply_guessed_digest_child"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_chat_reply_dependencies(
organization_id,child_event_id,child_event_revision,parent_event_id,parent_event_revision,parent_event_digest,parent_author_principal,
parent_legacy_snapshot_digest,parent_audience_digest,parent_acl_revision,parent_purge_generation,parent_source_authority_receipt_id,
source_manifest_digest,recorded_at)
SELECT tenant_id,$2,1,event_id,content_revision,decode($3,'hex'),author_principal,decode($4,'hex'),audience_digest,acl_version,purge_generation,$5,decode($6,'hex'),clock_timestamp()
		FROM stride_conversation_events WHERE tenant_id=$1 AND event_id=$7`, snapshot.Organization.Header.ID, guessedChildEventID, strings.Repeat("d", 64), legacyDigest, parentReceipt, manifestDigest, parentEventID); err == nil || !strings.Contains(err.Error(), "exact same-thread current ancestry") {
		t.Fatalf("guessed parent digest err=%v", err)
	}
	for _, parentThread := range []any{"wrong_reply_parent_thread", nil} {
		if _, err := store.pool.Exec(ctx, `UPDATE stride_conversation_events SET thread_id=$1 WHERE tenant_id=$2 AND event_id=$3`,
			parentThread, snapshot.Organization.Header.ID, parentEventID); err != nil {
			t.Fatal(err)
		}
		var visibleAfterParentThreadDrift int
		if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current WHERE association_id=$1`,
			child.AssociationID).Scan(&visibleAfterParentThreadDrift); err != nil || visibleAfterParentThreadDrift != 0 {
			t.Fatalf("reply parent thread drift %v visible=%d err=%v", parentThread, visibleAfterParentThreadDrift, err)
		}
		if _, err := store.pool.Exec(ctx, `UPDATE stride_conversation_events SET thread_id=$1 WHERE tenant_id=$2 AND event_id=$3`,
			thread.ID, snapshot.Organization.Header.ID, parentEventID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_conversation_events SET author_principal='service:forged' WHERE tenant_id=$1 AND event_id=$2`, snapshot.Organization.Header.ID, parentEventID); err != nil {
		t.Fatal(err)
	}
	var visibleAfterAuthorDrift int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current WHERE association_id=$1`, child.AssociationID).Scan(&visibleAfterAuthorDrift); err != nil || visibleAfterAuthorDrift != 0 {
		t.Fatalf("reply author drift visible=%d err=%v", visibleAfterAuthorDrift, err)
	}
	_ = child
}

func TestPostgresProjectChatSourceGroupRequiresExactRootAndPartAssociations(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "source_group_two_member_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	messageID, text := "source_group_two_member_message", "Root with attachment"
	root, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, messageID, "source-group-two-member-seed", text, homeProjectContextToken{
		Kind: "create", ProjectTitle: "Two Member Project", Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	eventID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, messageID)
	partID, partDigest := "rich_part_two_member", sha256Hex([]byte("two-member-part"))
	blobDigest, destinationDigest := strings.Repeat("b", 64), strings.Repeat("c", 64)
	var audience string
	if err := store.pool.QueryRow(ctx, `SELECT source_audience::text FROM stride_project_association_revisions WHERE association_id=$1 AND revision=$2`, root.AssociationID, root.AssociationRevision).Scan(&audience); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_rich_message_part_revisions(
organization_id,part_id,revision,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,source_origin_id,source_origin_revision,
blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,author_principal,source_audience,source_acl_revision,purge_generation,recorded_at,content_digest)
VALUES($1,$2,1,$3,1,0,'attachment-source-two','sha256:source-two','file_two','file_revision_two',$4,decode($4,'hex'),'application/pdf',22,
decode($5,'hex'),'destination-two',$6,$7::jsonb,1,1,clock_timestamp(),decode($8,'hex'))`, snapshot.Organization.Header.ID, partID, eventID,
		blobDigest, destinationDigest, snapshot.Person.Header.ID, audience, partDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_rich_message_parts_current(organization_id,part_id,revision,conversation_event_id,ordinal,content_digest,updated_at)
VALUES($1,$2,1,$3,0,decode($4,'hex'),clock_timestamp())`, snapshot.Organization.Header.ID, partID, eventID, partDigest); err != nil {
		t.Fatal(err)
	}
	partReceipt := "project_source_two_member_part"
	partRefs := `[{"contractType":"rich_message_part","id":"` + partID + `","revision":1,"digest":"` + partDigest + `"}]`
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_source_authority_receipts(
source_authority_receipt_id,organization_id,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,evidence_coverage_digest,source_audience,
source_acl_revision,source_acl_digest,consent_revision,purge_generation,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,
session_revision,authority_generation,idempotency_key_digest,request_fingerprint,recorded_at,expires_at)
SELECT $1,$2,'rich_message_part',$3,1,decode($4,'hex'),$5::jsonb,decode($4,'hex'),$6::jsonb,1,
sha256(convert_to(concat_ws(E'\x1f',part.organization_id,part.part_id,part.revision::text,encode(part.content_digest,'hex'),
encode(sha256(convert_to(part.source_audience::text,'UTF8')),'hex'),part.source_acl_revision::text,part.purge_generation::text),'UTF8')),
1,1,$7,$8,$9,decode($10,'hex'),$11,$12,decode($13,'hex'),decode($14,'hex'),clock_timestamp(),clock_timestamp()+interval '30 minutes'
FROM stride_rich_message_part_revisions part WHERE part.organization_id=$2 AND part.part_id=$3 AND part.revision=1`, partReceipt,
		snapshot.Organization.Header.ID, partID, partDigest, partRefs, audience, snapshot.Person.Header.ID, snapshot.Membership.Header.ID,
		snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation,
		sha256Hex([]byte("part-receipt-key")), sha256Hex([]byte("part-receipt-fingerprint"))); err != nil {
		t.Fatal(err)
	}
	partAssociation := "project_association_two_member_part"
	proposedDigest, confirmedDigest := sha256Hex([]byte("two-member-proposed")), sha256Hex([]byte("two-member-confirmed"))
	associationKey := sha256Hex([]byte("two-member-association-key"))
	for _, revision := range []struct {
		n                            int64
		state, digest, prior, action string
	}{{1, "proposed", proposedDigest, "", "propose"}, {2, "confirmed", confirmedDigest, proposedDigest, "confirm"}} {
		expires := any(nil)
		if revision.n == 1 {
			expires = time.Now().UTC().Add(15 * time.Minute)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT $1,$2,$3,$4,$5,'rich_message_part',$6,1,decode($7,'hex'),$8::jsonb,$9,decode($7,'hex'),$10,'selected','project_linker_v1',1,
$11,$12,$13,decode($14,'hex'),$15,$16,$17::jsonb,1,receipt.source_acl_digest,1,1,decode($18,'hex'),$19,$20,
CASE WHEN $21='' THEN NULL ELSE decode($21,'hex') END,clock_timestamp(),decode($22,'hex')
FROM stride_project_source_authority_receipts receipt WHERE receipt.organization_id=$3 AND receipt.source_authority_receipt_id=$9`, partAssociation,
			revision.n, snapshot.Organization.Header.ID, root.ProjectID, root.ProjectRevision, partID, partDigest, partRefs, partReceipt, revision.state,
			snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
			snapshot.ActiveSession.SessionRevision, snapshot.Generation, audience, associationKey, expires, nullablePriorRevision(revision.n), revision.prior, revision.digest); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_association_events(event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$4,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),clock_timestamp())`,
			"project_association_event_two_member_"+revision.action, snapshot.Organization.Header.ID, partAssociation, revision.n, revision.action, revision.state,
			revision.n-1, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
			snapshot.ActiveSession.SessionRevision, snapshot.Generation, sha256Hex([]byte(associationKey+revision.action)), sha256Hex([]byte(revision.digest+revision.action))); err != nil {
			t.Fatal(err)
		}
		if revision.n == 1 {
			_, err = store.pool.Exec(ctx, `INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at) VALUES($1,1,$2,$3,'proposed',decode($4,'hex'),clock_timestamp())`, partAssociation, snapshot.Organization.Header.ID, root.ProjectID, proposedDigest)
		} else {
			_, err = store.pool.Exec(ctx, `UPDATE stride_project_associations_current SET revision=2,state='confirmed',content_digest=decode($1,'hex'),updated_at=clock_timestamp() WHERE association_id=$2`, confirmedDigest, partAssociation)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion, Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text)), Attachments: []projectChatManifestAttachment{{Ordinal: 0, SourceID: "attachment-source-two", SourceRevision: "sha256:source-two", BlobRef: blobDigest, BlobDigest: blobDigest, Mime: "application/pdf", Size: 22, DestinationRevision: "destination-two", OriginFileID: "file_two", OriginRevision: "file_revision_two"}}}
	manifest.Digest = projectChatManifestDigest(manifest)
	missingOrdinalGroup := "source_group_two_member_missing_ordinal"
	missingOrdinalOperation := "source_group_two_member_missing_ordinal_operation"
	missingOrdinalKey := sha256Hex([]byte("source-group-two-member-missing-ordinal-key"))
	missingOrdinalFingerprint := projectChatSourceGroupFingerprint(snapshot, missingOrdinalGroup, missingOrdinalOperation, missingOrdinalKey, manifest.Digest, thread, messageID, eventID, root, 2)
	missingOrdinalTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = missingOrdinalTx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,
conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,$13,2,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',clock_timestamp())`,
		snapshot.Organization.Header.ID, missingOrdinalGroup, missingOrdinalOperation, missingOrdinalKey, missingOrdinalFingerprint,
		manifest.Digest, thread.ID, messageID, eventID, root.ProjectID, root.ProjectRevision, root.AssociationID, root.AssociationRevision,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
		snapshot.ActiveSession.SessionRevision, snapshot.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = missingOrdinalTx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(
organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
SELECT organization_id,$2,0,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,revision,clock_timestamp()
FROM stride_project_association_revisions WHERE organization_id=$1 AND association_id=$3 AND revision=$4
UNION ALL
SELECT organization_id,$2,2,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,revision,clock_timestamp()
FROM stride_project_association_revisions WHERE organization_id=$1 AND association_id=$5 AND revision=2`,
		snapshot.Organization.Header.ID, missingOrdinalGroup, root.AssociationID, root.AssociationRevision, partAssociation); err != nil {
		t.Fatal(err)
	}
	if err = missingOrdinalTx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "exact complete member truth") {
		t.Fatalf("missing-middle member group commit err=%v", err)
	}

	duplicateOrdinalGroup := "source_group_two_member_duplicate_ordinal"
	duplicateOrdinalTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer duplicateOrdinalTx.Rollback(ctx)
	duplicateOrdinalOperation := "source_group_two_member_duplicate_ordinal_operation"
	duplicateOrdinalKey := sha256Hex([]byte("source-group-two-member-duplicate-ordinal-key"))
	duplicateOrdinalFingerprint := projectChatSourceGroupFingerprint(snapshot, duplicateOrdinalGroup, duplicateOrdinalOperation, duplicateOrdinalKey, manifest.Digest, thread, messageID, eventID, root, 2)
	if _, err = duplicateOrdinalTx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,
conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,$13,2,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',clock_timestamp())`,
		snapshot.Organization.Header.ID, duplicateOrdinalGroup, duplicateOrdinalOperation, duplicateOrdinalKey, duplicateOrdinalFingerprint,
		manifest.Digest, thread.ID, messageID, eventID, root.ProjectID, root.ProjectRevision, root.AssociationID, root.AssociationRevision,
		snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
		snapshot.ActiveSession.SessionRevision, snapshot.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = duplicateOrdinalTx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(
organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
SELECT organization_id,$2,0,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,revision,clock_timestamp()
FROM stride_project_association_revisions WHERE organization_id=$1 AND association_id=$3 AND revision=$4`,
		snapshot.Organization.Header.ID, duplicateOrdinalGroup, root.AssociationID, root.AssociationRevision); err == nil {
		if _, duplicateErr := duplicateOrdinalTx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(
organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
SELECT organization_id,$2,0,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,revision,clock_timestamp()
FROM stride_project_association_revisions WHERE organization_id=$1 AND association_id=$3 AND revision=2`,
			snapshot.Organization.Header.ID, duplicateOrdinalGroup, partAssociation); duplicateErr == nil || !strings.Contains(duplicateErr.Error(), "duplicate key") {
			t.Fatalf("duplicate member ordinal err=%v", duplicateErr)
		}
	} else {
		t.Fatal(err)
	}
	_ = duplicateOrdinalTx.Rollback(ctx)

	groupID, operationID, operationKey := "source_group_two_member", "source_group_two_member_operation", sha256Hex([]byte("source-group-two-member-key"))
	fingerprint := projectChatSourceGroupFingerprint(snapshot, groupID, operationID, operationKey, manifest.Digest, thread, messageID, eventID, root, 2)
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at) VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,$13,2,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',clock_timestamp())`, snapshot.Organization.Header.ID, groupID, operationID, operationKey, fingerprint, manifest.Digest, thread.ID, messageID, eventID, root.ProjectID, root.ProjectRevision, root.AssociationID, root.AssociationRevision, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash, snapshot.ActiveSession.SessionRevision, snapshot.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at) SELECT organization_id,$2,0,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,revision,clock_timestamp() FROM stride_project_association_revisions WHERE organization_id=$1 AND association_id=$3 AND revision=$4 UNION ALL SELECT organization_id,$2,1,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,revision,clock_timestamp() FROM stride_project_association_revisions WHERE organization_id=$1 AND association_id=$5 AND revision=2`, snapshot.Organization.Header.ID, groupID, root.AssociationID, root.AssociationRevision, partAssociation); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var authorized int
	if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current WHERE association_id IN ($1,$2)`, root.AssociationID, partAssociation).Scan(&authorized); err != nil || authorized != 2 {
		t.Fatalf("authorized group members=%d err=%v", authorized, err)
	}
	for _, rootIdentityDrift := range []struct {
		column string
		value  string
		reset  string
	}{
		{column: "thread_id", value: "wrong_thread", reset: thread.ID},
		{column: "source_id", value: "wrong_message", reset: messageID},
		{column: "author_principal", value: "service:scout", reset: snapshot.Person.Header.ID},
	} {
		if _, err = store.pool.Exec(ctx, `UPDATE stride_conversation_events SET `+rootIdentityDrift.column+`=$1 WHERE tenant_id=$2 AND event_id=$3`,
			rootIdentityDrift.value, snapshot.Organization.Header.ID, eventID); err != nil {
			t.Fatal(err)
		}
		var visibleWithRootDrift int
		if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current
WHERE association_id IN ($1,$2)`, root.AssociationID, partAssociation).Scan(&visibleWithRootDrift); err != nil {
			t.Fatal(err)
		}
		if visibleWithRootDrift != 0 {
			t.Fatalf("root %s drift left %d group associations visible", rootIdentityDrift.column, visibleWithRootDrift)
		}
		if _, err = store.pool.Exec(ctx, `UPDATE stride_conversation_events SET `+rootIdentityDrift.column+`=$1 WHERE tenant_id=$2 AND event_id=$3`,
			rootIdentityDrift.reset, snapshot.Organization.Header.ID, eventID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.pool.Exec(ctx, `UPDATE stride_conversation_events SET thread_id=NULL WHERE tenant_id=$1 AND event_id=$2`,
		snapshot.Organization.Header.ID, eventID); err != nil {
		t.Fatal(err)
	}
	var visibleWithNullRootThread int
	if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current
WHERE association_id IN ($1,$2)`, root.AssociationID, partAssociation).Scan(&visibleWithNullRootThread); err != nil {
		t.Fatal(err)
	}
	if visibleWithNullRootThread != 0 {
		t.Fatalf("NULL root thread left %d group associations visible", visibleWithNullRootThread)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE stride_conversation_events SET thread_id=$1 WHERE tenant_id=$2 AND event_id=$3`,
		thread.ID, snapshot.Organization.Header.ID, eventID); err != nil {
		t.Fatal(err)
	}

	// Source drift is server-owned and must remain executable after the original
	// user session is no longer current. Advance the exact part pointer to its
	// immutable invalidation revision, then atomically terminalize every sibling.
	if _, err = store.pool.Exec(ctx, `UPDATE stride_active_organization_sessions
SET session_revision=session_revision+1,authority_generation=authority_generation+1,status='invalidated',invalidated_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE session_subject_digest=decode($1,'hex')`, snapshot.SessionHash); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO stride_rich_message_part_revisions(
organization_id,part_id,revision,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,source_origin_id,source_origin_revision,
blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,author_principal,source_audience,source_acl_revision,purge_generation,
recorded_at,invalidated_at,invalidation_reason,content_digest)
SELECT organization_id,part_id,revision+1,conversation_event_id,conversation_event_revision,ordinal,source_id,source_revision,source_origin_id,source_origin_revision,
blob_ref,blob_digest,media_type,byte_size,destination_digest,destination_revision,author_principal,source_audience,source_acl_revision,purge_generation+1,
clock_timestamp(),clock_timestamp(),'origin_revoked',content_digest
FROM stride_rich_message_part_revisions WHERE organization_id=$1 AND part_id=$2 AND revision=1`, snapshot.Organization.Header.ID, partID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE stride_rich_message_parts_current current_part
SET revision=2,content_digest=revision.content_digest,updated_at=clock_timestamp()
FROM stride_rich_message_part_revisions revision
WHERE current_part.organization_id=$1 AND current_part.part_id=$2 AND revision.organization_id=current_part.organization_id
  AND revision.part_id=current_part.part_id AND revision.revision=2`, snapshot.Organization.Header.ID, partID); err != nil {
		t.Fatal(err)
	}
	driftOperation := "source_group_two_member_part_drift_operation"
	driftKey := sha256Hex([]byte("source-group-two-member-part-drift-key"))
	driftRecordedAt := time.Now().UTC()
	insertDriftReceipt := func(tx pgx.Tx, operation, key, subjectID, expectedDigest string, recordedAt time.Time) error {
		_, insertErr := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_drift_receipts(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,drift_contract_type,drift_subject_id,
expected_revision,expected_digest,reason,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),
sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,'rich_message_part',$5::text,'1',$6::text,'origin_revoked',$7::timestamptz::text),'UTF8')),
'rich_message_part',$5,1,decode($6,'hex'),'origin_revoked',$7)`, snapshot.Organization.Header.ID, groupID, operation, key, subjectID, expectedDigest, recordedAt)
		return insertErr
	}
	receiptOnlyTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertDriftReceipt(receiptOnlyTx, "source_group_drift_receipt_only", sha256Hex([]byte("source-group-drift-receipt-only-key")), partID, partDigest, driftRecordedAt); err != nil {
		t.Fatal(err)
	}
	if err = receiptOnlyTx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "requires exact drift, revoked group and four-family purge") {
		t.Fatalf("drift receipt-only commit err=%v", err)
	}
	guessedDriftTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertDriftReceipt(guessedDriftTx, "source_group_drift_guessed", sha256Hex([]byte("source-group-drift-guessed-key")), partID, strings.Repeat("e", 64), driftRecordedAt); err != nil {
		t.Fatal(err)
	}
	if err = guessedDriftTx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "requires exact drift") {
		t.Fatalf("guessed drift receipt commit err=%v", err)
	}
	revisionsOnlyTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revisionsOnlyOperation := "source_group_drift_revisions_only"
	revisionsOnlyKey := sha256Hex([]byte("source-group-drift-revisions-only-key"))
	if err = insertDriftReceipt(revisionsOnlyTx, revisionsOnlyOperation, revisionsOnlyKey, partID, partDigest, driftRecordedAt); err != nil {
		_ = revisionsOnlyTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err = revisionsOnlyTx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,replacement_association_id,replacement_association_revision,
replacement_association_digest,recorded_at,content_digest)
SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'revoked',prior.basis,
prior.classifier_revision,prior.confidence,prior.actor_person_id,prior.actor_membership_id,prior.actor_membership_revision,prior.session_subject_digest,
prior.session_revision,prior.authority_generation,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,prior.consent_revision,
prior.purge_generation,decode($3,'hex'),NULL,prior.revision,prior.content_digest,NULL,NULL,NULL,$4,
sha256(convert_to(concat_ws(E'\x1f','project-association/drift-revoked/v1',prior.association_id,(prior.revision+1)::text,
encode(prior.content_digest,'hex'),$5::text),'UTF8'))
FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions prior ON prior.organization_id=member.organization_id
 AND prior.association_id=member.association_id AND prior.revision=member.association_revision
	WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID, groupID,
		revisionsOnlyKey, driftRecordedAt, revisionsOnlyOperation); err != nil {
		_ = revisionsOnlyTx.Rollback(ctx)
		t.Fatalf("drift receipt+revisions insert: %v", err)
	} else if err = revisionsOnlyTx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "drift receipt requires exact drift, revoked group and four-family purge") {
		if err == nil {
			_ = revisionsOnlyTx.Rollback(ctx)
		}
		t.Fatalf("drift receipt+revisions-only commit err=%v", err)
	}
	driftTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer driftTx.Rollback(ctx)
	if err = insertDriftReceipt(driftTx, driftOperation, driftKey, partID, partDigest, driftRecordedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = driftTx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,replacement_association_id,replacement_association_revision,
replacement_association_digest,recorded_at,content_digest)
SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'revoked',prior.basis,
prior.classifier_revision,prior.confidence,prior.actor_person_id,prior.actor_membership_id,prior.actor_membership_revision,prior.session_subject_digest,
prior.session_revision,prior.authority_generation,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,prior.consent_revision,
prior.purge_generation,decode($3,'hex'),NULL,prior.revision,prior.content_digest,NULL,NULL,NULL,$4,
sha256(convert_to(concat_ws(E'\x1f','project-association/drift-revoked/v1',prior.association_id,(prior.revision+1)::text,
encode(prior.content_digest,'hex'),$5::text),'UTF8'))
FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions prior ON prior.organization_id=member.organization_id
 AND prior.association_id=member.association_id AND prior.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID, groupID, driftKey, driftRecordedAt, driftOperation); err != nil {
		t.Fatal(err)
	}
	if _, err = driftTx.Exec(ctx, `INSERT INTO stride_project_association_events(
event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,
actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
SELECT 'project_association_event_drift_'||member.ordinal,prior.organization_id,prior.association_id,prior.revision+1,'revoke','revoked',prior.revision,
prior.revision+1,prior.actor_person_id,prior.actor_membership_id,prior.actor_membership_revision,prior.session_subject_digest,prior.session_revision,
prior.authority_generation,decode($3,'hex'),sha256(convert_to(concat_ws(E'\x1f','project-association-event/drift-revoke/v1',
prior.organization_id,prior.association_id,(prior.revision+1)::text,$4::text),'UTF8')),$5::timestamptz
FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions prior ON prior.organization_id=member.organization_id
 AND prior.association_id=member.association_id AND prior.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID, groupID, driftKey, driftOperation, driftRecordedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = driftTx.Exec(ctx, `UPDATE stride_project_associations_current current_association
SET revision=terminal.revision,state='revoked',content_digest=terminal.content_digest,updated_at=$3
FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions terminal ON terminal.organization_id=member.organization_id
 AND terminal.association_id=member.association_id AND terminal.revision=member.association_revision+1
WHERE member.organization_id=$1 AND member.group_id=$2 AND current_association.organization_id=member.organization_id
 AND current_association.association_id=member.association_id`, snapshot.Organization.Header.ID, groupID, driftRecordedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = driftTx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(
organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at)
SELECT member.organization_id,member.association_id,member.association_revision+1,'purge',family.name,decode($3,'hex'),decode($4,'hex'),'pending',0,$5
FROM stride_project_chat_source_group_members member
CROSS JOIN (VALUES('home'),('work'),('board'),('project_record')) family(name)
WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID, groupID,
		sha256Hex([]byte("source-group-drift-source")), sha256Hex([]byte("source-group-drift-authority")), driftRecordedAt); err != nil {
		t.Fatal(err)
	}
	if err = driftTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var revokedMembers, purgePairs, purgeFamilies, membersWithFourPurges int
	if err = store.pool.QueryRow(ctx, `SELECT
count(DISTINCT current_association.association_id) FILTER(WHERE current_association.state='revoked'),
count(DISTINCT (outbox.association_id,outbox.projection_family)) FILTER(WHERE outbox.operation='purge'),
count(DISTINCT outbox.projection_family) FILTER(WHERE outbox.operation='purge'),
(SELECT count(*) FROM (
  SELECT member_check.association_id
  FROM stride_project_chat_source_group_members member_check
  JOIN stride_project_projection_outbox purge_check ON purge_check.organization_id=member_check.organization_id
   AND purge_check.association_id=member_check.association_id AND purge_check.association_revision=member_check.association_revision+1
   AND purge_check.operation='purge'
  WHERE member_check.organization_id=$1 AND member_check.group_id=$2
  GROUP BY member_check.association_id
  HAVING count(*)=4 AND count(DISTINCT purge_check.projection_family)=4
) exact_member_purges)
FROM stride_project_chat_source_group_members member
JOIN stride_project_associations_current current_association ON current_association.organization_id=member.organization_id
 AND current_association.association_id=member.association_id
LEFT JOIN stride_project_projection_outbox outbox ON outbox.organization_id=member.organization_id
 AND outbox.association_id=member.association_id AND outbox.association_revision=current_association.revision
WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID, groupID).
		Scan(&revokedMembers, &purgePairs, &purgeFamilies, &membersWithFourPurges); err != nil {
		t.Fatal(err)
	}
	if revokedMembers != 2 || purgePairs != 8 || purgeFamilies != 4 || membersWithFourPurges != 2 {
		t.Fatalf("drift terminal truth revoked-members=%d purge-pairs=%d families=%d exact-members=%d",
			revokedMembers, purgePairs, purgeFamilies, membersWithFourPurges)
	}
}

func TestPostgresProjectChatSourceGroupRemovalRequiresExactPerEdgeCAS(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		wrongEventKey    bool
		omitPartTerminal bool
		wantCommitErr    string
	}{
		{name: "exact_remove"},
		{name: "wrong_event_operation_key", wrongEventKey: true, wantCommitErr: "exact per-member terminal lineage"},
		{name: "omit_part_terminal", omitPartTerminal: true, wantCommitErr: "associations transition atomically"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, store, _ := migratedPostgresCanonicalStore(t)
			seedProjectPostgresAuthority(t, ctx, store)
			fixture := seedProjectChatTextGroup(t, ctx, store, testCase.name)
			operationID := "correction_remove_operation_" + testCase.name
			operationKey := sha256Hex([]byte("correction-remove-key-" + testCase.name))
			recordedAt := time.Now().UTC()
			tx, err := store.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,replacement_association_id,replacement_association_revision,
replacement_association_digest,recorded_at,content_digest)
SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'removed',prior.basis,
prior.classifier_revision,prior.confidence,$4,$5,$6,decode($7,'hex'),$8,$9,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,
	prior.consent_revision,prior.purge_generation,
	sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-edge/v1',$3::text,member.ordinal::text,member.association_id),'UTF8')),
	NULL,prior.revision,prior.content_digest,NULL,NULL,NULL,$10,
sha256(convert_to(concat_ws(E'\x1f','project-association/group-remove/v1',prior.association_id,(prior.revision+1)::text,
encode(prior.content_digest,'hex'),$2::text),'UTF8'))
FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions prior ON prior.organization_id=member.organization_id
 AND prior.association_id=member.association_id AND prior.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$11 AND (NOT $12::boolean OR member.ordinal=0)`, fixture.snapshot.Organization.Header.ID,
				operationID, operationKey, fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID,
				fixture.snapshot.Membership.Header.Revision, fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision,
				fixture.snapshot.Generation, recordedAt, fixture.groupID, testCase.omitPartTerminal); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_association_events(
event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,actor_person_id,actor_membership_id,
actor_membership_revision,session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
SELECT 'project_association_event_remove_'||$3::text||'_'||member.ordinal,$1,member.association_id,member.association_revision+1,
'remove','removed',member.association_revision,member.association_revision+1,$4,$5,$6,decode($7,'hex'),$8,$9,
CASE WHEN $10::boolean AND member.ordinal=1 THEN decode($11,'hex') ELSE
sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-edge/v1',$12::text,member.ordinal::text,member.association_id),'UTF8')) END,
decode($13,'hex'),$14
FROM stride_project_chat_source_group_members member WHERE member.organization_id=$1 AND member.group_id=$2 AND (NOT $15::boolean OR member.ordinal=0)`,
				fixture.snapshot.Organization.Header.ID, fixture.groupID, testCase.name, fixture.snapshot.Person.Header.ID,
				fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision, fixture.snapshot.SessionHash,
				fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, testCase.wrongEventKey,
				sha256Hex([]byte("wrong-correction-event-key")), operationKey,
				sha256Hex([]byte("correction-remove-event-fingerprint-"+testCase.name)), recordedAt, testCase.omitPartTerminal); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `UPDATE stride_project_associations_current current_association
SET revision=terminal.revision,state='removed',content_digest=terminal.content_digest,updated_at=$3
FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions terminal ON terminal.organization_id=member.organization_id
 AND terminal.association_id=member.association_id AND terminal.revision=member.association_revision+1
WHERE member.organization_id=$1 AND member.group_id=$2 AND (NOT $4::boolean OR member.ordinal=0) AND current_association.organization_id=member.organization_id
 AND current_association.association_id=member.association_id`, fixture.snapshot.Organization.Header.ID, fixture.groupID, recordedAt, testCase.omitPartTerminal); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(
organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at)
SELECT member.organization_id,member.association_id,member.association_revision+1,'purge',family.name,decode($3,'hex'),decode($4,'hex'),'pending',0,$5
FROM stride_project_chat_source_group_members member
CROSS JOIN (VALUES('home'),('work'),('board'),('project_record')) family(name)
WHERE member.organization_id=$1 AND member.group_id=$2 AND (NOT $6::boolean OR member.ordinal=0)`, fixture.snapshot.Organization.Header.ID, fixture.groupID,
				sha256Hex([]byte("correction-remove-source")),
				sha256Hex([]byte("correction-remove-authority")), recordedAt, testCase.omitPartTerminal); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_invalidations(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,reason,actor_person_id,actor_membership_id,
actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,'project_correction',$5::text,
$6::text,$7::bigint::text,encode(decode($8,'hex'),'hex'),$9::bigint::text,$10::bigint::text,$11::timestamptz::text),'UTF8')),'project_correction',$5,$6,$7::bigint,
decode($8,'hex'),$9::bigint,$10::bigint,$11::timestamptz)`, fixture.snapshot.Organization.Header.ID, fixture.groupID, operationID, operationKey,
				fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
				fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, recordedAt); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_correction_receipts(
organization_id,operation_id,operation_key_digest,request_fingerprint,old_group_id,replacement_group_id,result_state,context_revision,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
VALUES($1,$2,decode($3,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,'','removed','2',$5::text,$6::text,
$7::bigint::text,encode(decode($8,'hex'),'hex'),$9::bigint::text,$10::bigint::text,$11::timestamptz::text),'UTF8')),$4,NULL,'removed',2,$5,$6,$7::bigint,
decode($8,'hex'),$9::bigint,$10::bigint,$11::timestamptz)`, fixture.snapshot.Organization.Header.ID, operationID, operationKey, fixture.groupID,
				fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
				fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, recordedAt); err != nil {
				t.Fatal(err)
			}
			err = tx.Commit(ctx)
			if testCase.wantCommitErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantCommitErr) {
					t.Fatalf("remove CAS commit err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var removedMembers, purgePairs, purgeFamilies, authorized int
			if err = store.pool.QueryRow(ctx, `SELECT
count(DISTINCT current_association.association_id) FILTER(WHERE current_association.state='removed'),
count(DISTINCT (outbox.association_id,outbox.projection_family)) FILTER(WHERE outbox.operation='purge'),
count(DISTINCT outbox.projection_family) FILTER(WHERE outbox.operation='purge'),
(SELECT count(*) FROM stride_project_associations_authorized_current authorized
 JOIN stride_project_chat_source_group_members authorized_member ON authorized_member.organization_id=authorized.organization_id
  AND authorized_member.association_id=authorized.association_id
 WHERE authorized_member.organization_id=$1 AND authorized_member.group_id=$2)
FROM stride_project_chat_source_group_members member
JOIN stride_project_associations_current current_association ON current_association.organization_id=member.organization_id
 AND current_association.association_id=member.association_id
LEFT JOIN stride_project_projection_outbox outbox ON outbox.organization_id=member.organization_id
 AND outbox.association_id=member.association_id AND outbox.association_revision=current_association.revision
WHERE member.organization_id=$1 AND member.group_id=$2`, fixture.snapshot.Organization.Header.ID, fixture.groupID).
				Scan(&removedMembers, &purgePairs, &purgeFamilies, &authorized); err != nil {
				t.Fatal(err)
			}
			if removedMembers != 2 || purgePairs != 8 || purgeFamilies != 4 || authorized != 0 {
				t.Fatalf("remove result members=%d purge-pairs=%d families=%d authorized=%d", removedMembers, purgePairs, purgeFamilies, authorized)
			}
		})
	}
}

func TestPostgresProjectChatSourceGroupSerializesConcurrentEventClaims(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "concurrent_group_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	messageID, text := "concurrent_group_message", "Concurrent group claim"
	link, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, messageID, "concurrent-group-seed", text, homeProjectContextToken{
		Kind: "create", ProjectTitle: "Concurrent Group Project", Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	eventID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, messageID)
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text))}
	manifest.Digest = projectChatManifestDigest(manifest)
	insertHeader := func(tx pgx.Tx, groupID, operationID, operationKey string) error {
		fingerprint := projectChatSourceGroupFingerprint(snapshot, groupID, operationID, operationKey, manifest.Digest, thread, messageID, eventID, link, 1)
		_, insertErr := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(
organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,
conversation_event_id,conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,
actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,$13,1,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',clock_timestamp())`,
			snapshot.Organization.Header.ID, groupID, operationID, operationKey, fingerprint, manifest.Digest, thread.ID, messageID, eventID,
			link.ProjectID, link.ProjectRevision, link.AssociationID, link.AssociationRevision, snapshot.Person.Header.ID,
			snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.SessionHash,
			snapshot.ActiveSession.SessionRevision, snapshot.Generation)
		return insertErr
	}
	firstTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer firstTx.Rollback(ctx)
	if err = insertHeader(firstTx, "concurrent_group_winner", "concurrent_group_winner_operation", sha256Hex([]byte("concurrent-group-winner-key"))); err != nil {
		t.Fatal(err)
	}
	if _, err = firstTx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(
organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
SELECT organization_id,'concurrent_group_winner',0,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,
association_id,revision,clock_timestamp() FROM stride_project_association_revisions
WHERE organization_id=$1 AND association_id=$2 AND revision=$3`, snapshot.Organization.Header.ID, link.AssociationID, link.AssociationRevision); err != nil {
		t.Fatal(err)
	}
	secondTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer secondTx.Rollback(ctx)
	started := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(started)
		secondResult <- insertHeader(secondTx, "concurrent_group_loser", "concurrent_group_loser_operation", sha256Hex([]byte("concurrent-group-loser-key")))
	}()
	<-started
	select {
	case early := <-secondResult:
		t.Fatalf("competing event claim did not wait for winner: %v", early)
	case <-time.After(100 * time.Millisecond):
	}
	if err = firstTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case loserErr := <-secondResult:
		if loserErr == nil || !strings.Contains(loserErr.Error(), "already has an active source group") {
			t.Fatalf("competing event claim err=%v", loserErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("competing event claim did not resolve after winner commit")
	}
	_ = secondTx.Rollback(ctx)
	var active int
	if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_chat_source_groups source_group
WHERE organization_id=$1 AND conversation_event_id=$2
 AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation
  WHERE invalidation.organization_id=source_group.organization_id AND invalidation.group_id=source_group.group_id)
 AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift
  WHERE drift.organization_id=source_group.organization_id AND drift.group_id=source_group.group_id)`, snapshot.Organization.Header.ID, eventID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active event groups=%d", active)
	}
}

func TestPostgresProjectChatSourceGroupAuthorityLossRevokesEveryEdge(t *testing.T) {
	for _, negative := range []string{"project_audience_revoked"} {
		t.Run("reject_unproven_"+negative, func(t *testing.T) {
			ctx, store, _ := migratedPostgresCanonicalStore(t)
			seedProjectPostgresAuthority(t, ctx, store)
			fixture := seedProjectChatTextGroup(t, ctx, store, "authority_loss_negative_"+negative)
			if err := store.invalidateProjectChatSourceGroupForAuthorityLoss(ctx, fixture.snapshot.Organization.Header.ID, fixture.groupID,
				"project_group_authority_loss_negative_"+negative, negative); err == nil {
				t.Fatal("unproven authority loss committed")
			}
			var receipts, revoked int
			if err := store.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM stride_project_chat_source_group_authority_loss_receipts WHERE organization_id=$1 AND group_id=$2),
(SELECT count(*) FROM stride_project_chat_source_group_members member JOIN stride_project_associations_current current_association
 ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2 AND current_association.state='revoked')`, fixture.snapshot.Organization.Header.ID,
				fixture.groupID).Scan(&receipts, &revoked); err != nil || receipts != 0 || revoked != 0 {
				t.Fatalf("failed authority loss receipts=%d revoked=%d err=%v", receipts, revoked, err)
			}
		})
	}
	t.Run("audience_loss_exact", func(t *testing.T) {
		ctx, store, _ := migratedPostgresCanonicalStore(t)
		seedProjectPostgresAuthority(t, ctx, store)
		fixture := seedProjectChatTextGroup(t, ctx, store, "authority_loss_audience")
		digest := sha256Hex([]byte("authority-loss-audience-revision"))
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_person_principals(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at)
VALUES('person_non_viewer',1,decode($1,'hex'),'active',1,1,clock_timestamp())`, sha256Hex([]byte("person-non-viewer"))); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_organization_membership_revisions(membership_id,revision,organization_id,person_id,role,status,granted_at,created_at,created_by_person_id)
VALUES('membership_non_viewer',1,$1,'person_non_viewer','member','active',clock_timestamp(),clock_timestamp(),$2)`, fixture.snapshot.Organization.Header.ID,
			fixture.snapshot.Person.Header.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_organization_memberships_current(membership_id,revision,organization_id,person_id,role,status,active_slot,updated_at)
VALUES('membership_non_viewer',1,$1,'person_non_viewer','member','active',2,clock_timestamp())`, fixture.snapshot.Organization.Header.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,
retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,supersedes_revision,supersedes_digest,content_digest)
SELECT project_id,2,organization_id,title,aliases,'active',retention_policy,
jsonb_build_array(jsonb_build_object('contractType','organization_membership','id','membership_non_viewer','revision',1,
'digest',repeat('9',64))),
jsonb_build_object('visibility','project','principals',jsonb_build_array('person_non_viewer')),acl_revision+1,creator_person_id,
created_at,clock_timestamp(),1,content_digest,decode($2,'hex') FROM stride_project_revisions
WHERE organization_id=$1 AND project_id=$3 AND revision=1`, fixture.snapshot.Organization.Header.ID, digest, fixture.link.ProjectID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,
project_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,
idempotency_key_digest,request_fingerprint,recorded_at) VALUES($1,$2,'revise_project',$3,2,$4,$5,$6,decode($7,'hex'),$8,$9,
decode($10,'hex'),decode($11,'hex'),clock_timestamp())`, "authority_loss_audience_revision", fixture.snapshot.Organization.Header.ID,
			fixture.link.ProjectID, fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
			fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation,
			sha256Hex([]byte("authority-loss-audience-key")), sha256Hex([]byte("authority-loss-audience-fingerprint"))); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `UPDATE stride_projects_current SET revision=2,content_digest=decode($3,'hex'),updated_at=clock_timestamp()
WHERE organization_id=$1 AND project_id=$2`, fixture.snapshot.Organization.Header.ID, fixture.link.ProjectID, digest); err != nil {
			t.Fatal(err)
		}
		var authorized int
		if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_chat_source_group_members member
JOIN stride_project_associations_authorized_current current_association ON current_association.organization_id=member.organization_id
 AND current_association.association_id=member.association_id WHERE member.organization_id=$1 AND member.group_id=$2`,
			fixture.snapshot.Organization.Header.ID, fixture.groupID).Scan(&authorized); err != nil || authorized != 0 {
			t.Fatalf("audience loss authorized=%d err=%v", authorized, err)
		}
		reason, err := store.projectChatSourceGroupAuthorityLossReason(ctx, fixture.snapshot.Organization.Header.ID, fixture.groupID)
		if err != nil || reason != "project_audience_revoked" {
			t.Fatalf("audience loss reason=%q err=%v", reason, err)
		}
		if err := store.invalidateProjectChatSourceGroupForAuthorityLoss(ctx, fixture.snapshot.Organization.Header.ID, fixture.groupID,
			"project_group_authority_loss_audience", reason); err != nil {
			t.Fatal(err)
		}
	})
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	fixture := seedProjectChatTextGroup(t, ctx, store, "authority_loss")
	renameDigest := sha256Hex([]byte("authority-loss-benign-rename"))
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,
retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,supersedes_revision,supersedes_digest,content_digest)
SELECT project_id,2,organization_id,title||' renamed',aliases,'active',retention_policy,controller_memberships,audience,acl_revision+1,
creator_person_id,created_at,clock_timestamp(),1,content_digest,decode($2,'hex') FROM stride_project_revisions
WHERE organization_id=$1 AND project_id=$3 AND revision=1`, fixture.snapshot.Organization.Header.ID, renameDigest, fixture.link.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,
project_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,
idempotency_key_digest,request_fingerprint,recorded_at) VALUES($1,$2,'revise_project',$3,2,$4,$5,$6,decode($7,'hex'),$8,$9,
decode($10,'hex'),decode($11,'hex'),clock_timestamp())`, "authority_loss_benign_rename", fixture.snapshot.Organization.Header.ID,
		fixture.link.ProjectID, fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
		fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation,
		sha256Hex([]byte("authority-loss-rename-key")), sha256Hex([]byte("authority-loss-rename-fingerprint"))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_projects_current SET revision=2,content_digest=decode($3,'hex'),updated_at=clock_timestamp()
WHERE organization_id=$1 AND project_id=$2`, fixture.snapshot.Organization.Header.ID, fixture.link.ProjectID, renameDigest); err != nil {
		t.Fatal(err)
	}
	if err := store.projectChatSourceGroupAuthorizedCurrent(ctx, fixture.snapshot.Organization.Header.ID, fixture.groupID, 2); err != nil {
		t.Fatalf("benign Project rename hid stable associations: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_projects_current SET lifecycle='archived',revision=revision+1
WHERE organization_id=$1 AND project_id=$2`, fixture.snapshot.Organization.Header.ID, fixture.link.ProjectID); err == nil {
		t.Fatal("invalid direct archive bypassed Project current gate")
	}
	archiveDigest := sha256Hex([]byte("authority-loss-legal-archive"))
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_revisions(project_id,revision,organization_id,title,aliases,lifecycle,
retention_policy,controller_memberships,audience,acl_revision,creator_person_id,created_at,updated_at,supersedes_revision,supersedes_digest,content_digest)
SELECT project_id,3,organization_id,title,aliases,'archived',retention_policy,controller_memberships,audience,acl_revision+1,
creator_person_id,created_at,clock_timestamp(),2,content_digest,decode($2,'hex') FROM stride_project_revisions
WHERE organization_id=$1 AND project_id=$3 AND revision=2`, fixture.snapshot.Organization.Header.ID, archiveDigest, fixture.link.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_operation_receipts(operation_id,organization_id,operation_kind,project_id,
project_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,
idempotency_key_digest,request_fingerprint,recorded_at) VALUES($1,$2,'revise_project',$3,3,$4,$5,$6,decode($7,'hex'),$8,$9,
decode($10,'hex'),decode($11,'hex'),clock_timestamp())`, "authority_loss_legal_archive", fixture.snapshot.Organization.Header.ID,
		fixture.link.ProjectID, fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
		fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation,
		sha256Hex([]byte("authority-loss-archive-key")), sha256Hex([]byte("authority-loss-archive-fingerprint"))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_projects_current SET revision=3,lifecycle='archived',content_digest=decode($3,'hex'),updated_at=clock_timestamp()
WHERE organization_id=$1 AND project_id=$2`, fixture.snapshot.Organization.Header.ID, fixture.link.ProjectID, archiveDigest); err != nil {
		t.Fatal(err)
	}
	operationID := "project_group_authority_loss_archive"
	if err := store.invalidateProjectChatSourceGroupForAuthorityLoss(ctx, fixture.snapshot.Organization.Header.ID, fixture.groupID,
		operationID, "project_archived"); err != nil {
		t.Fatal(err)
	}
	if err := store.invalidateProjectChatSourceGroupForAuthorityLoss(ctx, fixture.snapshot.Organization.Header.ID, fixture.groupID,
		operationID, "project_archived"); err != nil {
		t.Fatalf("authority loss replay: %v", err)
	}
	var revoked, purges, authorized, receipts int
	if err := store.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM stride_project_chat_source_group_members member JOIN stride_project_associations_current current_association
 ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2 AND current_association.state='revoked'),
(SELECT count(*) FROM stride_project_chat_source_group_members member JOIN stride_project_projection_outbox outbox
 ON outbox.organization_id=member.organization_id AND outbox.association_id=member.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2 AND outbox.operation='purge'),
(SELECT count(*) FROM stride_project_chat_source_group_members member JOIN stride_project_associations_authorized_current authorized
 ON authorized.organization_id=member.organization_id AND authorized.association_id=member.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2),
(SELECT count(*) FROM stride_project_chat_source_group_authority_loss_receipts WHERE organization_id=$1 AND group_id=$2)`,
		fixture.snapshot.Organization.Header.ID, fixture.groupID).Scan(&revoked, &purges, &authorized, &receipts); err != nil {
		t.Fatal(err)
	}
	if revoked != 2 || purges != 8 || authorized != 0 || receipts != 1 {
		t.Fatalf("authority loss revoked=%d purges=%d authorized=%d receipts=%d", revoked, purges, authorized, receipts)
	}
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Authority loss recovery", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "authority-loss-recovery-message", Kind: "message", Role: "user", Text: "Recover authority loss",
		AuthorEmail: normalizeAccountEmail(user.Email), Project: &scoutChatProjectContext{Status: "unavailable", ContextRevision: 2,
			ProjectID: fixture.link.ProjectID, ProjectRevision: fixture.link.ProjectRevision, Title: fixture.link.ProjectTitle,
			AssociationID: fixture.link.AssociationID, AssociationRevision: fixture.link.AssociationRevision}}
	thread.Messages = append(thread.Messages, message)
	thread.ProjectLinkOperations = append(thread.ProjectLinkOperations, scoutChatProjectLinkOperation{OperationID: "authority-loss-send",
		MessageID: message.ID, State: "drift_pending", SourceGroupID: fixture.groupID, AssociationIDs: []string{fixture.link.AssociationID, fixture.partAssociationID}})
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	priorRuntime := currentCanonicalRuntime()
	setCanonicalRuntime(&CanonicalRuntime{mode: CanonicalModeOff, postgres: store})
	t.Cleanup(func() { setCanonicalRuntime(priorRuntime) })
	restarted := newKanbanBoardApp()
	if err := restarted.finishCommittedScoutProjectSourceGroupAuthorityLoss(ctx, user, thread.ID, "authority-loss-send",
		fixture.snapshot.Organization.Header.ID, fixture.groupID, operationID); err != nil {
		t.Fatal(err)
	}
	recovered, _, err := restarted.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || recovered.ProjectLinkOperations[0].State != "drifted" || recovered.Messages[0].Project.Status != "unavailable" {
		t.Fatalf("authority loss fresh-app recovery operation=%+v project=%+v err=%v", recovered.ProjectLinkOperations,
			recovered.Messages[0].Project, err)
	}
}

func TestPostgresProjectChatSourceGroupCorrectionReplacesEveryEdge(t *testing.T) {
	for _, testCase := range []struct {
		name                      string
		corruptPartEdgeKey        bool
		omitPartListNew           bool
		wrongReplacementKey       bool
		wrongReplacementOperation bool
		wantCommitErr             string
	}{
		{name: "exact"},
		{name: "corrupt_part_edge_key", corruptPartEdgeKey: true, wantCommitErr: "exact per-member terminal lineage"},
		{name: "omit_part_list_new", omitPartListNew: true, wantCommitErr: "exact per-member terminal lineage"},
		{name: "wrong_replacement_group_key", wrongReplacementKey: true, wantCommitErr: "exact replacement group CAS"},
		{name: "wrong_replacement_group_operation", wrongReplacementOperation: true, wantCommitErr: "exact replacement group CAS"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, store, _ := migratedPostgresCanonicalStore(t)
			seedProjectPostgresAuthority(t, ctx, store)
			fixture := seedProjectChatTextGroup(t, ctx, store, "replace_every_edge_"+testCase.name)
			dummyThread := scoutChatThreadRecord{ID: "correction_replacement_project_thread", OwnerEmail: fixture.snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
			replacementProject, err := store.confirmHomeProjectChatSend(ctx, fixture.snapshot, dummyThread, "correction_replacement_project_message",
				"correction-replacement-project-seed", "Replacement project seed", homeProjectContextToken{
					Kind: "create", ProjectTitle: "Replacement Project", Basis: "selected", Confidence: 1,
					OrganizationID: fixture.snapshot.Organization.Header.ID, PersonID: fixture.snapshot.Person.Header.ID,
				})
			if err != nil {
				t.Fatal(err)
			}
			var rivalProject confirmedProjectChatLink
			if testCase.name == "exact" {
				rivalThread := scoutChatThreadRecord{ID: "correction_rival_project_thread", OwnerEmail: fixture.snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
				rivalProject, err = store.confirmHomeProjectChatSend(ctx, fixture.snapshot, rivalThread, "correction_rival_project_message",
					"correction-rival-project-seed", "Rival project seed", homeProjectContextToken{
						Kind: "create", ProjectTitle: "Rival Replacement Project", Basis: "selected", Confidence: 1,
						OrganizationID: fixture.snapshot.Organization.Header.ID, PersonID: fixture.snapshot.Person.Header.ID,
					})
				if err != nil {
					t.Fatal(err)
				}
			}
			rows, err := store.pool.Query(ctx, `SELECT member.ordinal,member.association_id,member.association_revision,encode(revision.content_digest,'hex')
FROM stride_project_chat_source_group_members member
JOIN stride_project_association_revisions revision ON revision.organization_id=member.organization_id
 AND revision.association_id=member.association_id AND revision.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$2 ORDER BY member.ordinal`, fixture.snapshot.Organization.Header.ID, fixture.groupID)
			if err != nil {
				t.Fatal(err)
			}
			var members []projectChatCorrectionMember
			for rows.Next() {
				var member projectChatCorrectionMember
				if err = rows.Scan(&member.ordinal, &member.associationID, &member.associationRevision, &member.priorDigest); err != nil {
					t.Fatal(err)
				}
				members = append(members, member)
			}
			rows.Close()
			if len(members) != 2 {
				t.Fatalf("correction source members=%d", len(members))
			}
			operationID := "correction_replace_every_edge_operation"
			operationKey := sha256Hex([]byte("correction-replace-every-edge-key"))
			recordedAt := time.Now().UTC()
			replacementGroupID := "correction_replacement_group"
			tx, err := store.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			replacementIDs := make([]string, len(members))
			replacementDigests := make([]string, len(members))
			edgeKeys := make([]string, len(members))
			for index, member := range members {
				edgeKeys[index] = sha256Hex([]byte(strings.Join([]string{"project-chat-group-correction-edge/v1", operationKey,
					fmt.Sprint(member.ordinal), member.associationID}, "\x1f")))
				if testCase.corruptPartEdgeKey && member.ordinal == 1 {
					edgeKeys[index] = sha256Hex([]byte("corrupt-part-edge-key"))
				}
				replacementIDs[index] = fmt.Sprintf("correction_replacement_association_%d", member.ordinal)
				replacementDigests[index] = sha256Hex([]byte(fmt.Sprintf("correction-replacement-digest-%d", member.ordinal)))
				if _, err := tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
		association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
		source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
		session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
		idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
		SELECT $1,1,prior.organization_id,$2,$3,prior.subject_contract_type,prior.subject_id,prior.subject_revision,prior.subject_digest,prior.source_refs,
		prior.source_authority_receipt_id,prior.evidence_coverage_digest,'confirmed',prior.basis,prior.classifier_revision,prior.confidence,$4,$5,$6,
		decode($7,'hex'),$8,$9,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,prior.consent_revision,prior.purge_generation,
		decode($10,'hex'),NULL,NULL,NULL,$11,decode($12,'hex')
		FROM stride_project_association_revisions prior WHERE prior.organization_id=$13 AND prior.association_id=$14 AND prior.revision=$15`,
					replacementIDs[index], replacementProject.ProjectID, replacementProject.ProjectRevision, fixture.snapshot.Person.Header.ID,
					fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision, fixture.snapshot.SessionHash,
					fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, edgeKeys[index], recordedAt, replacementDigests[index],
					fixture.snapshot.Organization.Header.ID, member.associationID, member.associationRevision); err != nil {
					t.Fatal(err)
				}
			}
			for index, member := range members {
				terminalDigest := sha256Hex([]byte(fmt.Sprintf("correction-terminal-digest-%d", member.ordinal)))
				correctionID := fmt.Sprintf("correction_edge_receipt_%d", member.ordinal)
				oldEventID := fmt.Sprintf("correction_old_event_%d", member.ordinal)
				replacementEventID := fmt.Sprintf("correction_replacement_event_%d", member.ordinal)
				if _, err := tx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
		association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
		source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
		session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
		idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,replacement_association_id,replacement_association_revision,
		replacement_association_digest,recorded_at,content_digest)
		SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
		prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'corrected',prior.basis,
		prior.classifier_revision,prior.confidence,$5,$6,$7,decode($8,'hex'),$9,$10,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,
		prior.consent_revision,prior.purge_generation,decode($11,'hex'),NULL,prior.revision,prior.content_digest,$12,1,decode($13,'hex'),$14,decode($15,'hex')
		FROM stride_project_association_revisions prior WHERE prior.organization_id=$1 AND prior.association_id=$2 AND prior.revision=$3 AND $4::text<>''`,
					fixture.snapshot.Organization.Header.ID, member.associationID, member.associationRevision, operationID,
					fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
					fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, edgeKeys[index],
					replacementIDs[index], replacementDigests[index], recordedAt, terminalDigest); err != nil {
					t.Fatal(err)
				}
				requestFingerprint := sha256Hex([]byte("correction-edge-fingerprint"))
				if _, err := tx.Exec(ctx, `INSERT INTO stride_project_association_events(
		event_id,organization_id,association_id,association_revision,action,resulting_state,prior_revision,new_revision,replacement_association_id,
		replacement_association_revision,replacement_association_digest,correction_id,actor_person_id,actor_membership_id,actor_membership_revision,
		session_subject_digest,session_revision,authority_generation,idempotency_key_digest,request_fingerprint,occurred_at)
		VALUES($1,$2,$3,$4,'correct','corrected',$5,$4,$6,1,decode($7,'hex'),$8,$9,$10,$11,decode($12,'hex'),$13,$14,
		decode($15,'hex'),decode($16,'hex'),$17),
		($18,$2,$6,1,'confirm','confirmed',0,1,NULL,NULL,NULL,$8,$9,$10,$11,decode($12,'hex'),$13,$14,
		decode($15,'hex'),decode($16,'hex'),$17)`, oldEventID, fixture.snapshot.Organization.Header.ID, member.associationID,
					member.associationRevision+1, member.associationRevision, replacementIDs[index], replacementDigests[index], correctionID,
					fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
					fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, edgeKeys[index],
					requestFingerprint, recordedAt, replacementEventID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO stride_project_correction_receipts(
		correction_id,organization_id,old_association_id,old_association_revision,replacement_association_id,replacement_association_revision,
		old_event_id,replacement_event_id,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,
		authority_generation,idempotency_key_digest,request_fingerprint,recorded_at)
		VALUES($1,$2,$3,$4,$5,1,$6,$7,$8,$9,$10,decode($11,'hex'),$12,$13,decode($14,'hex'),decode($15,'hex'),$16)`,
					correctionID, fixture.snapshot.Organization.Header.ID, member.associationID, member.associationRevision+1, replacementIDs[index],
					oldEventID, replacementEventID, fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID,
					fixture.snapshot.Membership.Header.Revision, fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision,
					fixture.snapshot.Generation, edgeKeys[index], requestFingerprint, recordedAt); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `UPDATE stride_project_associations_current SET revision=$2,state='corrected',content_digest=decode($3,'hex'),updated_at=$4 WHERE association_id=$1`,
					member.associationID, member.associationRevision+1, terminalDigest, recordedAt); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO stride_project_associations_current(association_id,revision,organization_id,project_id,state,content_digest,updated_at)
		VALUES($1,1,$2,$3,'confirmed',decode($4,'hex'),$5)`, replacementIDs[index], fixture.snapshot.Organization.Header.ID,
					replacementProject.ProjectID, replacementDigests[index], recordedAt); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO stride_project_projection_outbox(
		organization_id,association_id,association_revision,operation,projection_family,source_ref_digest,authority_digest,status,attempts,next_attempt_at)
		SELECT $1,$2,$3,'unlist_old',family.name,decode($4,'hex'),decode($5,'hex'),'pending',0,$6::timestamptz FROM (VALUES('home'),('work'),('board'),('project_record')) family(name)
		UNION ALL SELECT $1,$7,1,'list_new',family.name,decode($4,'hex'),decode($5,'hex'),'pending',0,$6::timestamptz FROM (VALUES('home'),('work'),('board'),('project_record')) family(name)
		WHERE NOT ($8::boolean AND $9::integer=1)`,
					fixture.snapshot.Organization.Header.ID, member.associationID, member.associationRevision+1,
					sha256Hex([]byte("correction-group-source")), sha256Hex([]byte("correction-group-authority")), recordedAt, replacementIDs[index],
					testCase.omitPartListNew, member.ordinal); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_invalidations(
		organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,reason,actor_person_id,actor_membership_id,
		actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
		VALUES($1,$2,$3,decode($4,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,'project_correction',$5::text,
		$6::text,$7::bigint::text,encode(decode($8,'hex'),'hex'),$9::bigint::text,$10::bigint::text,$11::timestamptz::text),'UTF8')),'project_correction',$5,$6,$7,
		decode($8,'hex'),$9,$10,$11)`, fixture.snapshot.Organization.Header.ID, fixture.groupID, operationID, operationKey,
				fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
				fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, recordedAt); err != nil {
				t.Fatal(err)
			}
			replacementRoot := confirmedProjectChatLink{ProjectID: replacementProject.ProjectID, ProjectRevision: replacementProject.ProjectRevision,
				AssociationID: replacementIDs[0], AssociationRevision: 1}
			groupOperationKey := sha256Hex([]byte(strings.Join([]string{"project-chat-group-correction-replacement/v1", operationKey, replacementGroupID}, "\x1f")))
			if testCase.wrongReplacementKey {
				groupOperationKey = sha256Hex([]byte("wrong-replacement-group-key"))
			}
			replacementOperationID := operationID
			if testCase.wrongReplacementOperation {
				replacementOperationID += "_wrong"
			}
			groupFingerprint := projectChatSourceGroupFingerprint(fixture.snapshot, replacementGroupID, replacementOperationID, groupOperationKey,
				fixture.manifestDigest, fixture.thread, fixture.messageID, fixture.eventID, replacementRoot, len(members))
			if _, err := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_groups(
		organization_id,group_id,operation_id,operation_key_digest,request_fingerprint,source_manifest_digest,thread_id,message_id,conversation_event_id,
		conversation_event_revision,project_id,project_revision,root_association_id,root_association_revision,member_count,actor_person_id,actor_membership_id,
		actor_membership_revision,session_subject_digest,session_revision,authority_generation,status,recorded_at)
		VALUES($1,$2,$3,decode($4,'hex'),decode($5,'hex'),decode($6,'hex'),$7,$8,$9,1,$10,$11,$12,1,$13,$14,$15,$16,decode($17,'hex'),$18,$19,'confirmed',$20)`,
				fixture.snapshot.Organization.Header.ID, replacementGroupID, replacementOperationID, groupOperationKey, groupFingerprint, fixture.manifestDigest,
				fixture.thread.ID, fixture.messageID, fixture.eventID, replacementProject.ProjectID, replacementProject.ProjectRevision, replacementIDs[0], len(members),
				fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
				fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, recordedAt); err != nil {
				t.Fatal(err)
			}
			for index, member := range members {
				if _, err := tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_members(
		organization_id,group_id,ordinal,subject_contract_type,subject_id,subject_revision,subject_digest,source_authority_receipt_id,association_id,association_revision,recorded_at)
		SELECT prior.organization_id,$2,$3,prior.subject_contract_type,prior.subject_id,prior.subject_revision,prior.subject_digest,prior.source_authority_receipt_id,$4,1,$5
		FROM stride_project_association_revisions prior WHERE prior.organization_id=$1 AND prior.association_id=$6 AND prior.revision=$7`,
					fixture.snapshot.Organization.Header.ID, replacementGroupID, member.ordinal, replacementIDs[index], recordedAt,
					member.associationID, member.associationRevision); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = tx.Exec(ctx, `INSERT INTO stride_project_chat_source_group_correction_receipts(
		organization_id,operation_id,operation_key_digest,request_fingerprint,old_group_id,replacement_group_id,result_state,context_revision,
		actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
		VALUES($1,$2,decode($3,'hex'),sha256(convert_to(concat_ws(E'\x1f',$1::text,$2::text,$3::text,$4::text,$5::text,'corrected','2',$6::text,$7::text,
		$8::bigint::text,encode(decode($9,'hex'),'hex'),$10::bigint::text,$11::bigint::text,$12::timestamptz::text),'UTF8')),$4,$5,'corrected',2,$6,$7,$8,
		decode($9,'hex'),$10,$11,$12)`, fixture.snapshot.Organization.Header.ID, operationID, operationKey, fixture.groupID, replacementGroupID,
				fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
				fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, recordedAt); err != nil {
				t.Fatal(err)
			}
			var rivalResult chan error
			var rivalTx pgx.Tx
			rivalOperationKey := sha256Hex([]byte("correction-rival-group-key"))
			if testCase.name == "exact" {
				rivalTx, err = store.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer rivalTx.Rollback(ctx)
				rivalResult = make(chan error, 1)
				go func() {
					_, rivalErr := rivalTx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,recorded_at,content_digest)
SELECT 'correction_rival_association_'||member.ordinal,1,prior.organization_id,$3,$4,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'confirmed',prior.basis,
prior.classifier_revision,prior.confidence,$5,$6,$7,decode($8,'hex'),$9,$10,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,
prior.consent_revision,prior.purge_generation,
sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-edge/v1',$11::text,member.ordinal::text,member.association_id),'UTF8')),
NULL,NULL,NULL,$12,sha256(convert_to(concat_ws(E'\x1f','rival-replacement',member.ordinal::text),'UTF8'))
FROM stride_project_chat_source_group_members member JOIN stride_project_association_revisions prior
 ON prior.organization_id=member.organization_id AND prior.association_id=member.association_id AND prior.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$2`, fixture.snapshot.Organization.Header.ID, fixture.groupID,
						rivalProject.ProjectID, rivalProject.ProjectRevision, fixture.snapshot.Person.Header.ID,
						fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision, fixture.snapshot.SessionHash,
						fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation, rivalOperationKey, recordedAt)
					if rivalErr == nil {
						_, rivalErr = rivalTx.Exec(ctx, `INSERT INTO stride_project_association_revisions(
association_id,revision,organization_id,project_id,project_revision,subject_contract_type,subject_id,subject_revision,subject_digest,source_refs,
source_authority_receipt_id,evidence_coverage_digest,state,basis,classifier_revision,confidence,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,source_audience,source_acl_revision,source_acl_digest,consent_revision,purge_generation,
idempotency_key_digest,expires_at,supersedes_revision,supersedes_digest,replacement_association_id,replacement_association_revision,
replacement_association_digest,recorded_at,content_digest)
SELECT prior.association_id,prior.revision+1,prior.organization_id,prior.project_id,prior.project_revision,prior.subject_contract_type,prior.subject_id,
prior.subject_revision,prior.subject_digest,prior.source_refs,prior.source_authority_receipt_id,prior.evidence_coverage_digest,'corrected',prior.basis,
prior.classifier_revision,prior.confidence,$3,$4,$5,decode($6,'hex'),$7,$8,prior.source_audience,prior.source_acl_revision,prior.source_acl_digest,
prior.consent_revision,prior.purge_generation,
sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-edge/v1',$9::text,member.ordinal::text,member.association_id),'UTF8')),
NULL,prior.revision,prior.content_digest,'correction_rival_association_'||member.ordinal,1,
sha256(convert_to(concat_ws(E'\x1f','rival-replacement',member.ordinal::text),'UTF8')),$10,
sha256(convert_to(concat_ws(E'\x1f','rival-terminal',member.ordinal::text),'UTF8'))
FROM stride_project_chat_source_group_members member JOIN stride_project_association_revisions prior
 ON prior.organization_id=member.organization_id AND prior.association_id=member.association_id AND prior.revision=member.association_revision
WHERE member.organization_id=$1 AND member.group_id=$2`, fixture.snapshot.Organization.Header.ID, fixture.groupID,
							fixture.snapshot.Person.Header.ID, fixture.snapshot.Membership.Header.ID, fixture.snapshot.Membership.Header.Revision,
							fixture.snapshot.SessionHash, fixture.snapshot.ActiveSession.SessionRevision, fixture.snapshot.Generation,
							rivalOperationKey, recordedAt)
					}
					rivalResult <- rivalErr
				}()
				select {
				case early := <-rivalResult:
					t.Fatalf("concurrent correction claim did not block: %v", early)
				case <-time.After(100 * time.Millisecond):
				}
			}
			err = tx.Commit(ctx)
			if testCase.wantCommitErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantCommitErr) {
					t.Fatalf("correction negative commit err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if rivalResult != nil {
				select {
				case rivalErr := <-rivalResult:
					if rivalErr == nil || !strings.Contains(rivalErr.Error(), "correction is stale") {
						t.Fatalf("concurrent correction loser err=%v", rivalErr)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent correction did not resolve after winner commit")
				}
				_ = rivalTx.Rollback(ctx)
				var loserRows int
				if err = store.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM stride_project_correction_receipts receipt
 JOIN stride_project_chat_source_group_members member ON member.organization_id=receipt.organization_id
  AND member.group_id=$2 AND member.association_id=receipt.old_association_id
 WHERE receipt.organization_id=$1 AND (receipt.idempotency_key_digest=sha256(convert_to(concat_ws(E'\x1f',
  'project-chat-group-correction-edge/v1',$3::text,member.ordinal::text,member.association_id),'UTF8'))
  OR receipt.replacement_association_id LIKE 'correction_rival_association_%'))+
(SELECT count(*) FROM stride_project_chat_source_group_correction_receipts
 WHERE organization_id=$1 AND operation_key_digest=decode($3,'hex'))+
(SELECT count(*) FROM stride_project_chat_source_group_invalidations
 WHERE organization_id=$1 AND operation_key_digest=decode($3,'hex'))+
(SELECT count(*) FROM stride_project_association_events event
 JOIN stride_project_chat_source_group_members member ON member.organization_id=event.organization_id
  AND member.group_id=$2 AND member.association_id=event.association_id
 WHERE event.organization_id=$1 AND event.idempotency_key_digest=sha256(convert_to(concat_ws(E'\x1f',
  'project-chat-group-correction-edge/v1',$3::text,member.ordinal::text,member.association_id),'UTF8')))+
(SELECT count(*) FROM stride_project_association_revisions revision WHERE revision.organization_id=$1 AND
 (revision.association_id LIKE 'correction_rival_association_%' OR EXISTS(
   SELECT 1 FROM stride_project_chat_source_group_members member WHERE member.organization_id=revision.organization_id AND member.group_id=$2
    AND member.association_id=revision.association_id AND revision.idempotency_key_digest=sha256(convert_to(concat_ws(E'\x1f',
     'project-chat-group-correction-edge/v1',$3::text,member.ordinal::text,member.association_id),'UTF8')))))+
(SELECT count(*) FROM stride_project_associations_current WHERE organization_id=$1 AND association_id LIKE 'correction_rival_association_%')+
(SELECT count(*) FROM stride_project_projection_outbox WHERE organization_id=$1 AND association_id LIKE 'correction_rival_association_%')+
(SELECT count(*) FROM stride_project_chat_source_groups WHERE organization_id=$1 AND operation_key_digest=decode($3,'hex'))+
(SELECT count(*) FROM stride_project_chat_source_group_members WHERE organization_id=$1 AND group_id='correction_rival_group')`,
					fixture.snapshot.Organization.Header.ID, fixture.groupID, rivalOperationKey).Scan(&loserRows); err != nil {
					t.Fatal(err)
				}
				if loserRows != 0 {
					t.Fatalf("concurrent correction loser durable rows=%d", loserRows)
				}
				var winnerInvalidations, winnerGroupReceipts, winnerEdgeReceipts, winnerCorrectEvents int
				var rivalAuthorityRows, activeGroups, winnerAuthorized int
				if err = store.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM stride_project_chat_source_group_invalidations WHERE organization_id=$1 AND group_id=$2 AND operation_id=$3),
(SELECT count(*) FROM stride_project_chat_source_group_correction_receipts WHERE organization_id=$1 AND old_group_id=$2
  AND operation_id=$3 AND replacement_group_id=$4),
(SELECT count(*) FROM stride_project_correction_receipts receipt
  JOIN stride_project_chat_source_group_members member ON member.organization_id=receipt.organization_id
   AND member.group_id=$2 AND member.association_id=receipt.old_association_id
  WHERE receipt.organization_id=$1 AND receipt.idempotency_key_digest=sha256(convert_to(concat_ws(E'\x1f','project-chat-group-correction-edge/v1',
    $5::text,member.ordinal::text,member.association_id),'UTF8'))),
(SELECT count(*) FROM stride_project_association_events event
  JOIN stride_project_chat_source_group_members member ON member.organization_id=event.organization_id
   AND member.group_id=$2 AND member.association_id=event.association_id
  WHERE event.organization_id=$1 AND event.action='correct' AND event.idempotency_key_digest=sha256(convert_to(concat_ws(E'\x1f',
    'project-chat-group-correction-edge/v1',$5::text,member.ordinal::text,member.association_id),'UTF8'))),
(SELECT count(*) FROM stride_project_correction_receipts receipt
  JOIN stride_project_chat_source_group_members member ON member.organization_id=receipt.organization_id
   AND member.group_id=$2 AND member.association_id=receipt.old_association_id
  WHERE receipt.organization_id=$1 AND (receipt.idempotency_key_digest=sha256(convert_to(concat_ws(E'\x1f',
   'project-chat-group-correction-edge/v1',$6::text,member.ordinal::text,member.association_id),'UTF8'))
   OR receipt.replacement_association_id LIKE 'correction_rival_association_%'))+
(SELECT count(*) FROM stride_project_chat_source_group_correction_receipts WHERE organization_id=$1 AND operation_key_digest=decode($6,'hex'))+
(SELECT count(*) FROM stride_project_chat_source_group_invalidations WHERE organization_id=$1 AND operation_key_digest=decode($6,'hex'))+
(SELECT count(*) FROM stride_project_association_events event
  JOIN stride_project_chat_source_group_members member ON member.organization_id=event.organization_id
   AND member.group_id=$2 AND member.association_id=event.association_id
  WHERE event.organization_id=$1 AND event.idempotency_key_digest=sha256(convert_to(concat_ws(E'\x1f',
   'project-chat-group-correction-edge/v1',$6::text,member.ordinal::text,member.association_id),'UTF8')))+
(SELECT count(*) FROM stride_project_association_revisions revision WHERE revision.organization_id=$1 AND
  (revision.association_id LIKE 'correction_rival_association_%' OR EXISTS(
   SELECT 1 FROM stride_project_chat_source_group_members member WHERE member.organization_id=revision.organization_id AND member.group_id=$2
    AND member.association_id=revision.association_id AND revision.idempotency_key_digest=sha256(convert_to(concat_ws(E'\x1f',
     'project-chat-group-correction-edge/v1',$6::text,member.ordinal::text,member.association_id),'UTF8')))))+
(SELECT count(*) FROM stride_project_associations_current WHERE organization_id=$1 AND association_id LIKE 'correction_rival_association_%')+
(SELECT count(*) FROM stride_project_projection_outbox WHERE organization_id=$1 AND association_id LIKE 'correction_rival_association_%')+
(SELECT count(*) FROM stride_project_chat_source_groups WHERE organization_id=$1 AND operation_key_digest=decode($6,'hex'))+
(SELECT count(*) FROM stride_project_chat_source_group_members WHERE organization_id=$1 AND group_id='correction_rival_group'),
(SELECT count(*) FROM stride_project_chat_source_groups source_group WHERE source_group.organization_id=$1 AND source_group.conversation_event_id=$7
  AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_invalidations invalidation WHERE invalidation.organization_id=$1 AND invalidation.group_id=source_group.group_id)
  AND NOT EXISTS(SELECT 1 FROM stride_project_chat_source_group_drift_receipts drift WHERE drift.organization_id=$1 AND drift.group_id=source_group.group_id)),
(SELECT count(*) FROM stride_project_associations_authorized_current authorized JOIN stride_project_chat_source_group_members member
  ON member.organization_id=authorized.organization_id AND member.association_id=authorized.association_id
  WHERE member.organization_id=$1 AND member.group_id=$4)`, fixture.snapshot.Organization.Header.ID, fixture.groupID, operationID,
					replacementGroupID, operationKey, rivalOperationKey, fixture.eventID).Scan(&winnerInvalidations, &winnerGroupReceipts,
					&winnerEdgeReceipts, &winnerCorrectEvents, &rivalAuthorityRows, &activeGroups, &winnerAuthorized); err != nil {
					t.Fatal(err)
				}
				if winnerInvalidations != 1 || winnerGroupReceipts != 1 || winnerEdgeReceipts != 2 || winnerCorrectEvents != 2 ||
					rivalAuthorityRows != 0 || activeGroups != 1 || winnerAuthorized != 2 {
					t.Fatalf("correction race durable truth invalidations=%d group-receipts=%d edge-receipts=%d correct-events=%d rival=%d active=%d authorized=%d",
						winnerInvalidations, winnerGroupReceipts, winnerEdgeReceipts, winnerCorrectEvents, rivalAuthorityRows, activeGroups, winnerAuthorized)
				}
			}
			var oldCorrected, replacementConfirmed, unlists, lists, oldAuthorized, replacementAuthorized int
			if err = store.pool.QueryRow(ctx, `SELECT
count(DISTINCT old_current.association_id) FILTER(WHERE old_current.state='corrected'),
count(DISTINCT replacement_current.association_id) FILTER(WHERE replacement_current.state='confirmed'),
count(DISTINCT (old_outbox.association_id,old_outbox.projection_family)) FILTER(WHERE old_outbox.operation='unlist_old'),
count(DISTINCT (new_outbox.association_id,new_outbox.projection_family)) FILTER(WHERE new_outbox.operation='list_new'),
(SELECT count(*) FROM stride_project_associations_authorized_current authorized
 JOIN stride_project_chat_source_group_members member_authorized ON member_authorized.organization_id=authorized.organization_id
  AND member_authorized.association_id=authorized.association_id WHERE member_authorized.organization_id=$1 AND member_authorized.group_id=$2),
(SELECT count(*) FROM stride_project_associations_authorized_current authorized
 JOIN stride_project_chat_source_group_members member_authorized ON member_authorized.organization_id=authorized.organization_id
  AND member_authorized.association_id=authorized.association_id WHERE member_authorized.organization_id=$1 AND member_authorized.group_id=$3)
FROM stride_project_chat_source_group_members old_member
JOIN stride_project_associations_current old_current ON old_current.organization_id=old_member.organization_id AND old_current.association_id=old_member.association_id
JOIN stride_project_chat_source_group_members replacement_member ON replacement_member.organization_id=old_member.organization_id
 AND replacement_member.group_id=$3 AND replacement_member.ordinal=old_member.ordinal
JOIN stride_project_associations_current replacement_current ON replacement_current.organization_id=replacement_member.organization_id
 AND replacement_current.association_id=replacement_member.association_id
LEFT JOIN stride_project_projection_outbox old_outbox ON old_outbox.organization_id=old_member.organization_id AND old_outbox.association_id=old_member.association_id
LEFT JOIN stride_project_projection_outbox new_outbox ON new_outbox.organization_id=replacement_member.organization_id AND new_outbox.association_id=replacement_member.association_id
WHERE old_member.organization_id=$1 AND old_member.group_id=$2`, fixture.snapshot.Organization.Header.ID, fixture.groupID, replacementGroupID).
				Scan(&oldCorrected, &replacementConfirmed, &unlists, &lists, &oldAuthorized, &replacementAuthorized); err != nil {
				t.Fatal(err)
			}
			if oldCorrected != 2 || replacementConfirmed != 2 || unlists != 8 || lists != 8 || oldAuthorized != 0 || replacementAuthorized != 2 {
				t.Fatalf("correction result old=%d replacement=%d unlists=%d lists=%d old-authorized=%d replacement-authorized=%d",
					oldCorrected, replacementConfirmed, unlists, lists, oldAuthorized, replacementAuthorized)
			}
		})
	}
}
