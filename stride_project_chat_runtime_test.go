package main

import (
	"context"
	"errors"
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
		SessionHash: strings.Repeat("2", 64), Session: sessionRecord{Email: "aj@example.com"}, Person: authority.Person, Organization: authority.Organization,
		Membership: authority.Membership, ActiveSession: authority.ActiveSession, Generation: 1,
	}
}

func TestPostgresProjectChatSendAdmitsExactMultiplayerAudience(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	ajAccountDigest := sha256Hex([]byte(normalizeAccountEmail(snapshot.Session.Email)))
	tylerAccountDigest := sha256Hex([]byte(normalizeAccountEmail("tyler@example.com")))
	if err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_account_person_mappings(mapping_id,revision,account_subject_digest,person_id,status,created_at) VALUES('mapping_project_aj',1,decode($1,'hex'),'person_project_test','active',clock_timestamp())`, []any{ajAccountDigest}},
		projectSQLStatement{`INSERT INTO stride_account_person_mappings_current(mapping_id,revision,account_subject_digest,person_id,status,updated_at) VALUES('mapping_project_aj',1,decode($1,'hex'),'person_project_test','active',clock_timestamp())`, []any{ajAccountDigest}},
		projectSQLStatement{`INSERT INTO stride_person_principals(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at) VALUES('person_project_tyler',1,decode($1,'hex'),'active',1,1,clock_timestamp())`, []any{tylerAccountDigest}},
		projectSQLStatement{`INSERT INTO stride_organization_membership_revisions(membership_id,revision,organization_id,person_id,role,status,granted_at,created_at,created_by_person_id) VALUES('membership_project_tyler',1,'organization_project_test','person_project_tyler','member','active',clock_timestamp(),clock_timestamp(),'person_project_test')`, nil},
		projectSQLStatement{`INSERT INTO stride_organization_memberships_current(membership_id,revision,organization_id,person_id,role,status,updated_at) VALUES('membership_project_tyler',1,'organization_project_test','person_project_tyler','member','active',clock_timestamp())`, nil},
		projectSQLStatement{`INSERT INTO stride_account_person_mappings(mapping_id,revision,account_subject_digest,person_id,status,created_at) VALUES('mapping_project_tyler',1,decode($1,'hex'),'person_project_tyler','active',clock_timestamp())`, []any{tylerAccountDigest}},
		projectSQLStatement{`INSERT INTO stride_account_person_mappings_current(mapping_id,revision,account_subject_digest,person_id,status,updated_at) VALUES('mapping_project_tyler',1,decode($1,'hex'),'person_project_tyler','active',clock_timestamp())`, []any{tylerAccountDigest}},
	); err != nil {
		t.Fatal(err)
	}
	privateThread := scoutChatThreadRecord{ID: "scout_private_project_seed", OwnerEmail: "aj@example.com", Visibility: scoutChatVisibilityPrivate}
	created, err := store.confirmHomeProjectChatSend(ctx, snapshot, privateThread, "message_private_seed", "operation-private-seed", "Start Country Golf.", homeProjectContextToken{Kind: "create", ProjectTitle: "Country Golf", Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID})
	if err != nil {
		t.Fatal(err)
	}
	channel := scoutChatThreadRecord{ID: "scout_country_golf_channel", OwnerEmail: "aj@example.com", Visibility: scoutChatVisibilityPublic}
	authority, err := store.projectChatSourceAuthorityForThread(ctx, snapshot, channel)
	if err != nil {
		t.Fatal(err)
	}
	if authority.Visibility != "channel" || authority.SourceType != "channel_chat_message" || len(authority.Principals) != 2 || authority.Principals[0] != snapshot.Person.Header.ID || authority.Principals[1] != "person_project_tyler" {
		t.Fatalf("unexpected channel authority: %+v", authority)
	}
	token := homeProjectContextToken{Kind: "project", ProjectID: created.ProjectID, ProjectRevision: created.ProjectRevision, ProjectDigest: created.ProjectDigest, ProjectTitle: created.ProjectTitle, Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID}
	linked, err := store.confirmProjectChatSend(ctx, snapshot, channel, "message_channel_project", "operation-channel-project", "Challenge the Country Golf plan.", token, authority)
	if err != nil {
		t.Fatal(err)
	}
	if linked.AssociationRevision != 2 || linked.ProjectID != created.ProjectID {
		t.Fatalf("unexpected linked channel turn: %+v", linked)
	}
	var visibility, sourceType string
	var audience string
	if err := store.pool.QueryRow(ctx, `SELECT visibility,source_type,(SELECT string_agg(value,'|' ORDER BY value) FROM jsonb_array_elements_text((SELECT source_audience->'principals' FROM stride_project_association_revisions WHERE association_id=$1 AND revision=2))) FROM stride_conversation_events WHERE tenant_id=$2 AND source_id='message_channel_project'`, linked.AssociationID, snapshot.Organization.Header.ID).Scan(&visibility, &sourceType, &audience); err != nil {
		t.Fatal(err)
	}
	if visibility != "channel" || sourceType != "channel_chat_message" || audience != "person_project_test|person_project_tyler" {
		t.Fatalf("canonical channel source = visibility %q type %q audience %q", visibility, sourceType, audience)
	}
	memberThread := scoutChatThreadRecord{ID: "scout_country_golf_members", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPublic, MemberEmails: []string{snapshot.Session.Email, "tyler@example.com"}}
	memberAuthority, err := store.projectChatSourceAuthorityForThread(ctx, snapshot, memberThread)
	if err != nil {
		t.Fatal(err)
	}
	if memberAuthority.Visibility != "project" || len(memberAuthority.Principals) != 2 || memberAuthority.Principals[0] != snapshot.Person.Header.ID || memberAuthority.Principals[1] != "person_project_tyler" {
		t.Fatalf("unexpected member-scoped authority: %+v", memberAuthority)
	}
	memberLink, err := store.confirmProjectChatSend(ctx, snapshot, memberThread, "message_member_project", "operation-member-project", "Plan the member launch.", token, memberAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT visibility,(SELECT string_agg(value,'|' ORDER BY value) FROM jsonb_array_elements_text((SELECT source_audience->'principals' FROM stride_project_association_revisions WHERE association_id=$1 AND revision=2))) FROM stride_conversation_events WHERE tenant_id=$2 AND source_id='message_member_project'`, memberLink.AssociationID, snapshot.Organization.Header.ID).Scan(&visibility, &audience); err != nil {
		t.Fatal(err)
	}
	if visibility != "project" || audience != "person_project_test|person_project_tyler" {
		t.Fatalf("canonical member source = visibility %q audience %q", visibility, audience)
	}
}

func TestExistingThreadProjectTurnJournalsBeforeReconciliationAndReplaysExactly(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Country Golf", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	operation := conversationTurnOperation{ID: "country-golf-project-turn", BodyDigest: strings.Repeat("a", 64)}
	message := scoutChatMessageRecord{ID: "scout-chat-message-country-golf-project", Kind: "message", Role: "user", Text: "Challenge the launch plan", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), SourceOperationID: operation.ID, SourceOperationDigest: operation.BodyDigest}
	token := homeProjectContextToken{Kind: "project", Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, ProjectID: "project_country_golf", ProjectRevision: 3, ProjectDigest: strings.Repeat("b", 64), ProjectTitle: "Country Golf", Basis: "selected"}
	binding := conversationProjectLinkBinding{EncodedToken: "signed-project-token", Token: token}
	saved, stored, created, err := app.beginScoutExistingProjectTurn(context.Background(), user, thread, message, operation, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !created || stored.Project == nil || stored.Project.Status != "pending" || len(saved.ProjectLinkOperations) != 1 || saved.ProjectLinkOperations[0].MessageID != message.ID || saved.ProjectLinkOperations[0].State != "pending" {
		t.Fatalf("pending thread journal=%+v message=%+v", saved.ProjectLinkOperations, stored.Project)
	}
	replayed, replayedMessage, created, err := app.beginScoutExistingProjectTurn(context.Background(), user, saved, message, operation, binding)
	if err != nil || created || len(replayed.Messages) != 1 || replayedMessage.ID != message.ID {
		t.Fatalf("exact replay created=%v messages=%d message=%+v err=%v", created, len(replayed.Messages), replayedMessage, err)
	}
	changed := binding
	changed.Token.ProjectTitle = "Another Project"
	if _, _, _, err := app.beginScoutExistingProjectTurn(context.Background(), user, saved, message, operation, changed); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("changed Project token err=%v", err)
	}
	failed, err := app.failScoutProjectLink(user, saved, operation.ID, message.ID, "", errHomeProjectStale)
	if err != nil {
		t.Fatal(err)
	}
	index := scoutChatMessageIndex(failed, message.ID)
	if index < 0 || failed.Messages[index].Project == nil || failed.Messages[index].Project.Status != "unavailable" || failed.ProjectLinkOperations[0].State != "failed_terminal" {
		t.Fatalf("terminal existing turn=%+v", failed)
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
