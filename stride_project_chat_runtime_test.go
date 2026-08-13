package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func projectChatSnapshotFixture(t *testing.T) StrideE10TenantAuthoritySnapshot {
	t.Helper()
	now := time.Now().UTC().Add(-time.Hour)
	authority, _ := projectAuthorityFixture("organization_project_test", "person_project_test", "membership_project_test", now)
	authority.Organization.Slug = "organization-project-test"
	authority.ActiveSession.SessionSubjectDigest = strings.Repeat("2", 64)
	authority.ActiveSession.ExpiresAt = time.Now().UTC().Add(time.Hour)
	return StrideE10TenantAuthoritySnapshot{
		SessionHash: strings.Repeat("2", 64), Person: authority.Person, Organization: authority.Organization,
		Membership: authority.Membership, ActiveSession: authority.ActiveSession, Generation: 1,
	}
}

func TestPostgresHomeProjectChatSendCreatesPrivateProjectAndConfirmedAssociation(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "scout_home_project_test", OwnerEmail: "aj@example.com", Visibility: scoutChatVisibilityPrivate}
	token := homeProjectContextToken{Kind: "create", ProjectTitle: "Launch Plan", Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID}
	link, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, "message_project_test", "operation-key-project-test", "Build the launch plan.", token)
	if err != nil {
		t.Fatal(err)
	}
	if link.ProjectID == "" || link.ProjectTitle != "Launch Plan" || link.ProjectRevision != 1 || link.AssociationID == "" {
		t.Fatalf("unexpected link: %+v", link)
	}
	var state string
	var revision int64
	if err := store.pool.QueryRow(ctx, `SELECT state,revision FROM stride_project_associations_authorized_current WHERE organization_id=$1 AND association_id=$2`, snapshot.Organization.Header.ID, link.AssociationID).Scan(&state, &revision); err != nil {
		t.Fatal(err)
	}
	if state != "confirmed" || revision != 2 {
		t.Fatalf("association = %s rev %d", state, revision)
	}
	var outbox int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_projection_outbox WHERE organization_id=$1 AND association_id=$2 AND operation='list_new'`, snapshot.Organization.Header.ID, link.AssociationID).Scan(&outbox); err != nil || outbox != 4 {
		t.Fatalf("outbox=%d err=%v", outbox, err)
	}
	replay, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, "message_project_test", "operation-key-project-test", "Build the launch plan.", token)
	if err != nil || replay != link {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := store.confirmHomeProjectChatSend(context.Background(), snapshot, thread, "message_project_test", "operation-key-project-test", "Changed body", token); err == nil {
		t.Fatal("same operation accepted changed body")
	}
	operationID := projectChatID("project_send", snapshot.Organization.Header.ID, thread.ID, "message_project_test")
	if _, err := store.pool.Exec(ctx, `UPDATE stride_project_chat_send_receipts SET recorded_at=recorded_at+interval '1 second' WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, operationID); err == nil {
		t.Fatal("canonical Project chat Send receipt was mutable")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_project_chat_send_receipts(
organization_id,operation_id,operation_key_digest,request_fingerprint,thread_id,message_id,conversation_event_id,conversation_event_revision,
project_id,project_revision,association_id,association_revision,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,status,recorded_at)
SELECT organization_id,operation_id||'_forged',decode($3,'hex'),decode($4,'hex'),thread_id,message_id,conversation_event_id,conversation_event_revision,
project_id,project_revision,association_id,1,actor_person_id,actor_membership_id,actor_membership_revision,
session_subject_digest,session_revision,authority_generation,status,recorded_at
FROM stride_project_chat_send_receipts WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, operationID, strings.Repeat("7", 64), strings.Repeat("8", 64)); err == nil {
		t.Fatal("receipt accepted a proposed association as confirmed canonical truth")
	}
}
