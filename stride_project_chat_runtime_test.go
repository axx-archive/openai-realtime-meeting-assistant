package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestExistingThreadProjectTurnV2JournalsCompleteSourceManifestBeforeReconciliation(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Manifest journal", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	operation := conversationTurnOperation{ID: "manifest-project-turn", BodyDigest: strings.Repeat("c", 64)}
	file := scoutChatFileAttachment{Name: "plan.pdf", Ref: strings.Repeat("d", 64), SourceID: "source_manifest_file", SourceRevision: "source_revision_1"}
	message := scoutChatMessageRecord{ID: "scout-chat-message-manifest-project", Kind: "message", Role: "user", Text: "Review the attached plan",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email),
		SourceOperationID: operation.ID, SourceOperationDigest: operation.BodyDigest, Files: []scoutChatFileAttachment{file},
		attachmentReservationID: "attachment-reservation-manifest-project"}
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(message.Text)),
		Attachments: []projectChatManifestAttachment{{Ordinal: 0, SourceID: file.SourceID, SourceRevision: file.SourceRevision,
			BlobRef: file.Ref, BlobDigest: file.Ref, Mime: "application/pdf", Size: 42, DestinationRevision: "destination_revision_1",
			OriginFileID: "origin_file_1", OriginRevision: "origin_revision_1"}}}
	manifest.Digest = projectChatManifestDigest(manifest)
	token := homeProjectContextToken{Version: homeProjectContextV2, Kind: "project", Destination: manifest.Destination,
		OrganizationID: "organization_project_test", ProjectID: "project_manifest", ProjectRevision: 2, ProjectDigest: strings.Repeat("e", 64),
		ProjectTitle: "Manifest Project", Basis: "selected", SourceManifestDigest: manifest.Digest}
	binding := conversationProjectLinkBinding{EncodedToken: "signed-v2-project-token", Token: token, Manifest: manifest}
	saved, _, created, err := app.beginScoutExistingProjectTurn(context.Background(), user, thread, message, operation, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !created || len(saved.ProjectLinkOperations) != 1 {
		t.Fatalf("created=%v operations=%+v", created, saved.ProjectLinkOperations)
	}
	journal := saved.ProjectLinkOperations[0]
	wantGroupID := projectChatID("project_source_group", token.OrganizationID, thread.ID, message.ID, operation.ID)
	if journal.State != "pending" || journal.SourceManifestDigest != manifest.Digest || journal.SourceGroupID != wantGroupID ||
		journal.ReservationID != message.attachmentReservationID || len(journal.AttachmentSources) != 1 ||
		journal.AttachmentSources[0].DestinationRevision != "destination_revision_1" || journal.AttachmentSources[0].OriginFileID != "origin_file_1" {
		t.Fatalf("incomplete source journal: %+v", journal)
	}
	restarted := newKanbanBoardApp()
	restartedThread, _, err := restarted.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || len(restartedThread.ProjectLinkOperations) != 1 ||
		restartedThread.ProjectLinkOperations[0].SourceManifestDigest != manifest.Digest ||
		restartedThread.ProjectLinkOperations[0].SourceGroupID != wantGroupID ||
		len(restartedThread.ProjectLinkOperations[0].AttachmentSources) != 1 {
		t.Fatalf("restart lost pending source journal thread=%+v err=%v", restartedThread.ProjectLinkOperations, err)
	}
	changed := binding
	changed.Manifest.Digest = strings.Repeat("f", 64)
	if _, _, _, err := app.beginScoutExistingProjectTurn(context.Background(), user, saved, message, operation, changed); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("changed manifest replay err=%v", err)
	}
}

func TestAttachmentBearingProjectGroupJournalsOneAtomicCorrection(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Group correction fence", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "group-correction-message", Kind: "message", Role: "user", Text: "Source", AuthorEmail: normalizeAccountEmail(user.Email),
		Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, ProjectID: "project_group_correction", ProjectRevision: 1,
			Title: "Group Correction", AssociationID: "association_group_root", AssociationRevision: 2}}
	thread.Messages = append(thread.Messages, message)
	thread.ProjectLinkOperations = append(thread.ProjectLinkOperations, scoutChatProjectLinkOperation{OperationID: "group-correction-send",
		MessageID: message.ID, State: "confirmed", SourceGroupID: "project_source_group_correction",
		AssociationIDs: []string{"association_group_root", "association_group_part"}})
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	token := projectChatCorrectionToken{ThreadID: thread.ID, MessageID: message.ID, ContextRevision: 1,
		OldAssociationID: "association_group_root", OldAssociationRevision: 2,
		Target: projectChatCorrectionTarget{Kind: "project", ProjectID: "replacement_project", ProjectRevision: 1}}
	if _, operation, created, err := app.beginScoutProjectCorrection(user, thread.ID, message.ID, "group-correction-operation", "signed-correction-token", token); err != nil || !created || operation.State != "pending" {
		t.Fatalf("group correction journal operation=%+v created=%v err=%v", operation, created, err)
	}
	fresh, _, _ := app.scoutChatThreadByID(user.Email, thread.ID)
	if len(fresh.ProjectCorrectionOperations) != 1 || fresh.Messages[0].Project.Status != "unavailable" {
		t.Fatalf("group correction did not fail closed while pending: %+v", fresh)
	}
}

func TestProjectV2CanonicalFailureHasZeroProviderCalls(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Provider fence", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	text := "Do not call a provider before canonical confirmation."
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text))}
	manifest.Digest = projectChatManifestDigest(manifest)
	binding := conversationProjectLinkBinding{EncodedToken: "provider-fence-token", Manifest: manifest, Token: homeProjectContextToken{
		Version: homeProjectContextV2, Kind: "project", Destination: manifest.Destination, OrganizationID: "organization_provider_fence",
		ProjectID: "project_provider_fence", ProjectRevision: 1, ProjectDigest: strings.Repeat("a", 64), ProjectTitle: "Provider Fence",
		Basis: "selected", SourceManifestDigest: manifest.Digest,
	}}
	ctx, counter := withConversationProviderCallCounter(context.Background())
	ctx = withConversationTurnOperation(ctx, conversationTurnOperation{ID: "provider-fence-operation", BodyDigest: strings.Repeat("b", 64)})
	ctx = withConversationProjectLink(ctx, binding)
	if _, err := app.appendScoutChatThreadMessageWithReplyAndTool(ctx, user, thread.ID, text, nil, "", "", ""); err == nil {
		t.Fatal("canonical-unavailable turn unexpectedly succeeded")
	}
	if counter.Calls != 0 {
		t.Fatalf("provider calls before canonical confirmation=%d", counter.Calls)
	}
}

func TestConfirmedProjectTurnCanonicalResumeRebindsOnlyCurrentSession(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv(homeProjectContextModeEnv, "enabled")
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	priorCanonical := currentCanonicalRuntime()
	setCanonicalRuntime(&CanonicalRuntime{mode: CanonicalModeOff, postgres: store})
	defer setCanonicalRuntime(priorCanonical)
	oldSnapshot := projectChatSnapshotFixture(t)
	user := accountStore().findUser("aj@shareability.com")
	oldSnapshot.Session.Email = user.Email
	accountDigest := sha256Hex([]byte(normalizeAccountEmail(user.Email)))
	if err := execProjectSQLBatch(ctx, store,
		projectSQLStatement{`INSERT INTO stride_account_person_mappings(mapping_id,revision,account_subject_digest,person_id,status,created_at) VALUES('mapping_rotated_resume',1,decode($1,'hex'),'person_project_test','active',clock_timestamp())`, []any{accountDigest}},
		projectSQLStatement{`INSERT INTO stride_account_person_mappings_current(mapping_id,revision,account_subject_digest,person_id,status,updated_at) VALUES('mapping_rotated_resume',1,decode($1,'hex'),'person_project_test','active',clock_timestamp())`, []any{accountDigest}},
	); err != nil {
		t.Fatal(err)
	}
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Rotated Project resume", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := store.confirmHomeProjectChatSend(ctx, oldSnapshot,
		scoutChatThreadRecord{ID: "rotated_resume_project_seed_thread", OwnerEmail: oldSnapshot.Session.Email, Visibility: scoutChatVisibilityPrivate},
		"rotated_resume_project_seed_message", "rotated-resume-project-seed", "Create Project", homeProjectContextToken{
			Kind: "create", ProjectTitle: "Rotated Resume", Basis: "selected", Confidence: 1,
			OrganizationID: oldSnapshot.Organization.Header.ID, PersonID: oldSnapshot.Person.Header.ID,
		})
	if err != nil {
		t.Fatal(err)
	}
	text := "Resume this exact accepted Project turn"
	operationID := "rotated-session-project-operation"
	messageID := "scout-chat-message-" + sha256Hex([]byte("conversation-turn/v1\x00" + normalizeAccountEmail(user.Email) + "\x00" + thread.ID + "\x00" + operationID))[:24]
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text))}
	manifest.Digest = projectChatManifestDigest(manifest)
	key := StrideE10TenantAuthorityEnvelopeKey{ID: "rotated_session_project_key", Version: 1, Secret: []byte(strings.Repeat("r", 32))}
	restoreEnvelope := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: key, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{key.ID: key}})
	defer restoreEnvelope()
	token := homeProjectContextToken{Version: homeProjectContextV2, Kind: "project", TextDigest: sha256Hex([]byte(text)), Destination: manifest.Destination,
		PersonID: oldSnapshot.Person.Header.ID, OrganizationID: oldSnapshot.Organization.Header.ID, MembershipID: oldSnapshot.Membership.Header.ID,
		MembershipRevision: oldSnapshot.Membership.Header.Revision, SessionSubjectDigest: oldSnapshot.SessionHash,
		SessionRevision: oldSnapshot.ActiveSession.SessionRevision, AuthorityGeneration: oldSnapshot.Generation,
		ProjectID: seed.ProjectID, ProjectRevision: seed.ProjectRevision, ProjectDigest: seed.ProjectDigest, ProjectTitle: seed.ProjectTitle,
		Basis: "selected", ClassifierRevision: "project_linker_v1", Confidence: 1, SourceManifestDigest: manifest.Digest,
		IssuedAt: time.Now().UTC().Add(-time.Minute), ExpiresAt: time.Now().UTC().Add(time.Minute), KeyID: key.ID, KeyVersion: key.Version}
	token.ChoiceKey = stableHomeProjectChoiceKey(key, oldSnapshot, token.Kind, homeProjectRow{ID: token.ProjectID, Revision: token.ProjectRevision, Digest: token.ProjectDigest, Title: token.ProjectTitle})
	rawToken, _ := json.Marshal(token)
	encodedToken := base64.RawURLEncoding.EncodeToString(rawToken) + "." + base64.RawURLEncoding.EncodeToString(homeProjectTokenMACVersion(key, rawToken, token.Version))
	bodyFields := map[string]any{"threadId": thread.ID, "requester": normalizeAccountEmail(user.Email), "text": text,
		"files": []scoutChatFileAttachment(nil), "followUpArtifactId": "", "replyToMessageId": "", "projectContextTokenDigest": homeProjectTokenDigest(encodedToken)}
	canonicalBody, _ := canonicalJSON(bodyFields)
	bodyDigest := sha256Hex(append([]byte("conversation-http-turn/v1\x00"), canonicalBody...))
	tokenDigest := homeProjectTokenDigest(encodedToken)
	link, err := store.confirmProjectChatSendWithManifest(ctx, oldSnapshot,
		scoutChatThreadRecord{ID: thread.ID, OwnerEmail: user.Email, Visibility: scoutChatVisibilityPrivate}, messageID, operationID, text, token,
		privateProjectChatSourceAuthority(oldSnapshot.Person.Header.ID), &manifest)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: messageID, Kind: "message", Role: "user", Text: text, AuthorEmail: user.Email,
		SourceOperationID: operationID, SourceOperationDigest: bodyDigest,
		Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, ProjectID: link.ProjectID,
			ProjectRevision: link.ProjectRevision, Title: link.ProjectTitle, AssociationID: link.AssociationID, AssociationRevision: link.AssociationRevision}}
	thread.Messages = append(thread.Messages, message)
	thread.ProjectLinkOperations = append(thread.ProjectLinkOperations, scoutChatProjectLinkOperation{
		OperationID: operationID, TokenDigest: tokenDigest, MessageID: messageID, State: "confirmed", ProjectKind: "project",
		ProjectID: token.ProjectID, ProjectRevision: token.ProjectRevision, ProjectDigest: token.ProjectDigest, ProjectTitle: token.ProjectTitle,
		Basis: token.Basis, AssociationID: link.AssociationID, SourceManifestDigest: manifest.Digest,
		SourceGroupID:  projectChatID("project_source_group", token.OrganizationID, thread.ID, messageID, operationID),
		AssociationIDs: []string{link.AssociationID},
	})
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	oldSnapshot.Person.AccountSubjectDigest = accountDigest
	sessions := userSessionStore()
	rotatedToken, err := sessions.createMemberSession(user.Email, oldSnapshot.Person.Header.ID, oldSnapshot.Organization.Header.ID,
		oldSnapshot.Membership.Header.ID, oldSnapshot.Membership.Header.Revision, 2, func(string, string, string, int64) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	rotated := oldSnapshot
	rotated.SessionHash = hashResetToken(rotatedToken)
	rotated.Session = sessions.sessions[rotated.SessionHash]
	rotated.ActiveSession.SessionSubjectDigest = rotated.SessionHash
	rotated.ActiveSession.SessionRevision = 2
	rotated.Generation = 2
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_active_organization_sessions(session_subject_digest,person_id,organization_id,membership_id,membership_revision,session_revision,authority_generation,status,bound_at,expires_at,updated_at) VALUES(decode($1,'hex'),$2,$3,$4,$5,2,2,'active',clock_timestamp(),clock_timestamp()+interval '1 hour',clock_timestamp())`,
		rotated.SessionHash, rotated.Person.Header.ID, rotated.Organization.Header.ID, rotated.Membership.Header.ID, rotated.Membership.Header.Revision); err != nil {
		t.Fatal(err)
	}
	accepted, err := app.acceptedScoutProjectTurnCanonicalResume(ctx, user, rotated, thread.ID, operationID, bodyDigest, tokenDigest, text, token, manifest)
	if err != nil || !accepted {
		t.Fatalf("rotated confirmed resume accepted=%v err=%v", accepted, err)
	}
	for label, mutate := range map[string]func(*scoutChatThreadRecord, *StrideE10TenantAuthoritySnapshot){
		"pending_before_pg": func(candidate *scoutChatThreadRecord, _ *StrideE10TenantAuthoritySnapshot) {
			candidate.ProjectLinkOperations[0].State = "pending"
		},
		"other_person": func(_ *scoutChatThreadRecord, snapshot *StrideE10TenantAuthoritySnapshot) {
			snapshot.Person.Header.ID = "person_other"
		},
	} {
		candidate, _, _ := app.scoutChatThreadByID(user.Email, thread.ID)
		candidate.ProjectLinkOperations = append([]scoutChatProjectLinkOperation(nil), candidate.ProjectLinkOperations...)
		candidateSnapshot := rotated
		mutate(&candidate, &candidateSnapshot)
		if err := app.saveScoutChatThread(candidate); err != nil {
			t.Fatal(err)
		}
		got, _ := app.acceptedScoutProjectTurnCanonicalResume(ctx, user, candidateSnapshot, thread.ID, operationID, bodyDigest, tokenDigest, text, token, manifest)
		if got {
			t.Fatalf("%s crossed durable resume boundary", label)
		}
		if err := app.saveScoutChatThread(thread); err != nil {
			t.Fatal(err)
		}
	}
	if accepted, _ := app.acceptedScoutProjectTurnCanonicalResume(ctx, user, rotated, thread.ID, operationID, strings.Repeat("d", 64), tokenDigest, text, token, manifest); accepted {
		t.Fatal("changed body crossed durable resume boundary")
	}

	organizations := NewOrganizationAuthorityService()
	organizations.persons[rotated.Person.Header.ID] = rotated.Person
	organizations.accountPersons[rotated.Person.AccountSubjectDigest] = rotated.Person.Header.ID
	organizations.organizations[rotated.Organization.Header.ID] = rotated.Organization
	organizations.memberships[rotated.Membership.Header.ID] = rotated.Membership
	organizations.sessions[rotated.SessionHash] = rotated.ActiveSession
	if err := rotated.Person.Validate(); err != nil {
		t.Fatalf("rotated person invalid: %v", err)
	}
	if err := rotated.Organization.Validate(); err != nil {
		t.Fatalf("rotated organization invalid: %v", err)
	}
	if err := rotated.Membership.Validate(); err != nil {
		t.Fatalf("rotated membership invalid: %v", err)
	}
	if err := rotated.ActiveSession.Validate(); err != nil {
		t.Fatalf("rotated active session invalid: %+v err=%v", rotated.ActiveSession, err)
	}
	restoreConverter := InstallStrideE10TenantRuntimeConverter(&StrideE10TenantConverter{resolver: &strideE10MainTenantAuthorityResolver{
		sessions: sessions, organizations: organizations, now: time.Now,
	}})
	defer restoreConverter()
	priorLive := strideE10LiveProductRuntime
	live := NewStrideE10ProductLiveRuntime(time.Now)
	live.setFeatureForTest(STRIDEFeatureOrganizationAuthorityRead, true)
	live.setFeatureForTest(STRIDEFeatureOrganizationAuthorityWrite, true)
	strideE10LiveProductRuntime = live
	defer func() { strideE10LiveProductRuntime = priorLive }()
	priorApp := kanbanApp
	kanbanApp = app
	app.apiKey = "openai-rotated-resume-test"
	defer func() { kanbanApp = priorApp }()
	var answerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch request.Workflow {
		case "scout_route":
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
		case "scout_chat":
			answerCalls.Add(1)
			return "Resumed once.", nil
		default:
			return "", fmt.Errorf("unexpected workflow %q", request.Workflow)
		}
	})
	body, _ := json.Marshal(map[string]any{"text": text, "operationId": operationID, "projectContextToken": encodedToken})
	request := func(requestBody []byte) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(string(requestBody)))
		r.Header.Set("Authorization", "Bearer "+rotatedToken)
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	if !app.acceptedScoutProjectTurnRetry(user, thread.ID, operationID, bodyDigest, tokenDigest) {
		t.Fatal("exact HTTP body did not match confirmed legacy journal")
	}
	if err := withCurrentHomeProjectAuthority(request(body), func(current StrideE10TenantAuthoritySnapshot) error {
		if current.SessionHash != rotated.SessionHash || current.Generation != 2 {
			return fmt.Errorf("unexpected rotated authority: %+v", current)
		}
		candidate, _, err := decodeSignedHomeProjectToken(context.Background(), encodedToken)
		if err != nil {
			return err
		}
		resume, err := app.acceptedScoutProjectTurnCanonicalResume(context.Background(), user, current, thread.ID, operationID,
			bodyDigest, tokenDigest, text, candidate, manifest)
		if err != nil || !resume {
			return fmt.Errorf("durable resume=%v: %w", resume, err)
		}
		_, err = resolveHomeProjectTokenForRetryWithManifestState(context.Background(), encodedToken, text, manifest.Destination, manifest, current, true, true)
		return err
	}); err != nil {
		t.Fatalf("rotated HTTP authority unavailable: %v", err)
	}
	first := httptest.NewRecorder()
	assistantChatThreadHandler(first, request(body))
	if first.Code != http.StatusOK || answerCalls.Load() != 1 {
		t.Fatalf("rotated resume status=%d calls=%d body=%s", first.Code, answerCalls.Load(), first.Body.String())
	}
	second := httptest.NewRecorder()
	assistantChatThreadHandler(second, request(body))
	if second.Code != http.StatusOK || answerCalls.Load() != 1 {
		t.Fatalf("exact replay status=%d calls=%d body=%s", second.Code, answerCalls.Load(), second.Body.String())
	}
	var projected map[string]any
	if json.Unmarshal(second.Body.Bytes(), &projected) != nil || projected["replayed"] != true {
		t.Fatalf("second response=%v body=%s", projected, second.Body.String())
	}
	changedBody, _ := json.Marshal(map[string]any{"text": text + " changed", "operationId": operationID, "projectContextToken": encodedToken})
	changed := httptest.NewRecorder()
	assistantChatThreadHandler(changed, request(changedBody))
	if changed.Code != http.StatusConflict || answerCalls.Load() != 1 {
		t.Fatalf("changed body status=%d calls=%d body=%s", changed.Code, answerCalls.Load(), changed.Body.String())
	}
}

func TestProjectV2LegacyConfirmDurablyCommitsReservedAttachmentSources(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Commit source", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	reservationID := "attachment-reservation-project-v2-commit"
	files, err := app.sanitizeScoutChatFiles(context.Background(), user, thread, []scoutChatFileAttachment{file}, reservationID)
	if err != nil {
		t.Fatal(err)
	}
	messageID := "project-v2-commit-message"
	thread.Messages = append(thread.Messages, scoutChatMessageRecord{ID: messageID, Kind: "message", Role: "user", AuthorEmail: user.Email, Files: files})
	thread.ProjectLinkOperations = append(thread.ProjectLinkOperations, scoutChatProjectLinkOperation{OperationID: "project-v2-commit-operation",
		MessageID: messageID, State: "confirmed", SourceGroupID: "project-v2-commit-group", ReservationID: reservationID,
		AttachmentSources: []scoutChatProjectAttachmentSource{{Ordinal: 0, SourceID: file.SourceID, SourceRevision: file.SourceRevision,
			BlobRef: file.Ref, DestinationRevision: scoutChatAttachmentDestinationRevision(thread)}}})
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	if err := app.commitScoutProjectSourceGroupAttachments(user, thread, "project-v2-commit-operation"); err != nil {
		t.Fatal(err)
	}
	restarted := newKanbanBoardApp()
	if !restarted.committedChatAttachmentAuthorized(user.Email, thread.ID, messageID, files[0]) {
		t.Fatal("confirmed Project source grant did not survive restart")
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

func TestPostgresProjectChatSendV2ConfirmsRootAttachmentGroupAndReplays(t *testing.T) {
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "project_v2_group_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	seed, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, "project_v2_seed_message", "project-v2-seed-operation", "Create the Project.", homeProjectContextToken{
		Kind: "create", ProjectTitle: "V2 Group Project", Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := "" // attachment-only is a valid Project-linked turn
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text)),
		Attachments: []projectChatManifestAttachment{{Ordinal: 0, SourceID: "project_v2_source", SourceRevision: "project_v2_source_revision",
			BlobRef: strings.Repeat("a", 64), BlobDigest: strings.Repeat("a", 64), Mime: "application/pdf", Size: 42,
			DestinationRevision: "project_v2_destination_revision", OriginFileID: "project_v2_origin", OriginRevision: "project_v2_origin_revision"}}}
	manifest.Digest = projectChatManifestDigest(manifest)
	token := homeProjectContextToken{Version: homeProjectContextV2, Kind: "project", ProjectID: seed.ProjectID, ProjectRevision: seed.ProjectRevision,
		ProjectDigest: seed.ProjectDigest, ProjectTitle: seed.ProjectTitle, Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID, SourceManifestDigest: manifest.Digest}
	link, err := store.confirmProjectChatSendWithManifest(ctx, snapshot, thread, "project_v2_group_message", "project-v2-group-operation", text, token,
		privateProjectChatSourceAuthority(snapshot.Person.Header.ID), &manifest)
	if err != nil {
		t.Fatal(err)
	}
	restartedStore := NewPostgresCanonicalStore(store.pool, registry)
	replay, err := restartedStore.confirmProjectChatSendWithManifest(ctx, snapshot, thread, "project_v2_group_message", "project-v2-group-operation", text, token,
		privateProjectChatSourceAuthority(snapshot.Person.Header.ID), &manifest)
	if err != nil || replay.AssociationID != link.AssociationID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	groupID := projectChatID("project_source_group", snapshot.Organization.Header.ID, thread.ID, "project_v2_group_message", "project-v2-group-operation")
	var members, authorized, parts int
	if err := store.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM stride_project_chat_source_group_members WHERE organization_id=$1 AND group_id=$2),
(SELECT count(*) FROM stride_project_associations_authorized_current authorized JOIN stride_project_chat_source_group_members member
 ON member.organization_id=authorized.organization_id AND member.association_id=authorized.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2),
(SELECT count(*) FROM stride_rich_message_parts_current part JOIN stride_project_chat_source_group_members member
 ON member.organization_id=part.organization_id AND member.subject_id=part.part_id WHERE member.organization_id=$1 AND member.group_id=$2)`,
		snapshot.Organization.Header.ID, groupID).Scan(&members, &authorized, &parts); err != nil {
		t.Fatal(err)
	}
	if members != 2 || authorized != 2 || parts != 1 {
		t.Fatalf("group members=%d authorized=%d parts=%d", members, authorized, parts)
	}
	replacementSeed, err := store.confirmHomeProjectChatSend(ctx, snapshot,
		scoutChatThreadRecord{ID: "project_v2_replacement_seed_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate},
		"project_v2_replacement_seed_message", "project-v2-replacement-seed-operation", "Create replacement Project.", homeProjectContextToken{
			Kind: "create", ProjectTitle: "V2 Replacement Project", Basis: "selected", Confidence: 1,
			OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
		})
	if err != nil {
		t.Fatal(err)
	}
	correctionToken := projectChatCorrectionToken{ContextRevision: 1, OldAssociationID: link.AssociationID,
		OldAssociationRevision: link.AssociationRevision, OrganizationID: snapshot.Organization.Header.ID,
		Target: projectChatCorrectionTarget{Kind: "project", ProjectID: replacementSeed.ProjectID,
			ProjectRevision: replacementSeed.ProjectRevision, ProjectDigest: replacementSeed.ProjectDigest, ProjectTitle: replacementSeed.ProjectTitle}}
	correction, err := restartedStore.replaceProjectChatSourceGroup(ctx, snapshot, groupID, "project-v2-group-correction", correctionToken)
	if err != nil {
		t.Fatal(err)
	}
	replacementGroupID := projectChatID("project_source_group_correction", snapshot.Organization.Header.ID, groupID, "project-v2-group-correction")
	var oldAuthorized, replacementAuthorized int
	if err := store.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM stride_project_associations_authorized_current authorized JOIN stride_project_chat_source_group_members member
 ON member.organization_id=authorized.organization_id AND member.association_id=authorized.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2),
(SELECT count(*) FROM stride_project_associations_authorized_current authorized JOIN stride_project_chat_source_group_members member
 ON member.organization_id=authorized.organization_id AND member.association_id=authorized.association_id
 WHERE member.organization_id=$1 AND member.group_id=$3)`, snapshot.Organization.Header.ID, groupID, replacementGroupID).
		Scan(&oldAuthorized, &replacementAuthorized); err != nil {
		t.Fatal(err)
	}
	if correction.AssociationID == link.AssociationID || correction.ProjectID != replacementSeed.ProjectID || oldAuthorized != 0 || replacementAuthorized != 2 {
		t.Fatalf("group correction=%+v old authorized=%d replacement authorized=%d", correction, oldAuthorized, replacementAuthorized)
	}
	legacyApp := newIsolatedKanbanBoardApp(t)
	legacyUser := accountStore().findUser("aj@shareability.com")
	legacyThread, err := legacyApp.createScoutChatThread(legacyUser.Email, legacyUser.Name, "Group correction recovery", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	legacyMessage := scoutChatMessageRecord{ID: "project-v2-group-recovery-message", Kind: "message", Role: "user", Text: "Recover me",
		AuthorEmail: normalizeAccountEmail(legacyUser.Email), Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1,
			ProjectID: link.ProjectID, ProjectRevision: link.ProjectRevision, Title: link.ProjectTitle,
			AssociationID: link.AssociationID, AssociationRevision: link.AssociationRevision}}
	legacyThread.Messages = append(legacyThread.Messages, legacyMessage)
	legacyThread.ProjectLinkOperations = append(legacyThread.ProjectLinkOperations, scoutChatProjectLinkOperation{OperationID: "project-v2-group-operation",
		MessageID: legacyMessage.ID, State: "confirmed", SourceGroupID: groupID, AssociationIDs: []string{link.AssociationID, "project_v2_part"}})
	if err := legacyApp.saveScoutChatThread(legacyThread); err != nil {
		t.Fatal(err)
	}
	legacyToken := correctionToken
	legacyToken.ThreadID, legacyToken.MessageID = legacyThread.ID, legacyMessage.ID
	if _, _, _, err := legacyApp.beginScoutProjectCorrection(legacyUser, legacyThread.ID, legacyMessage.ID,
		"project-v2-group-correction", "project-v2-group-signed-token", legacyToken); err != nil {
		t.Fatal(err)
	}
	restartedLegacy := newKanbanBoardApp()
	committed, found, err := restartedStore.committedProjectChatSourceGroupCorrection(ctx, snapshot.Organization.Header.ID,
		snapshot.Person.Header.ID, "project-v2-group-correction", legacyToken)
	if err != nil || !found {
		t.Fatalf("committed group correction found=%v result=%+v err=%v", found, committed, err)
	}
	recoveredThread, err := restartedLegacy.finishCommittedScoutProjectCorrection(legacyThread.ID, legacyMessage.ID,
		"project-v2-group-correction", committed)
	if err != nil || recoveredThread.Messages[0].Project.Status != "confirmed" ||
		recoveredThread.Messages[0].Project.AssociationID != correction.AssociationID {
		t.Fatalf("fresh-app group correction recovery project=%+v err=%v", recoveredThread.Messages[0].Project, err)
	}
	driftLegacy := newIsolatedKanbanBoardApp(t)
	driftUser := accountStore().findUser("aj@shareability.com")
	driftThread, err := driftLegacy.createScoutChatThread(driftUser.Email, driftUser.Name, "Group drift recovery", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	driftMessage := scoutChatMessageRecord{ID: "project-v2-drift-recovery-message", Kind: "message", Role: "user", Text: "Drift me",
		AuthorEmail: normalizeAccountEmail(driftUser.Email), Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1,
			ProjectID: correction.ProjectID, ProjectRevision: correction.ProjectRevision, Title: correction.ProjectTitle,
			AssociationID: correction.AssociationID, AssociationRevision: correction.AssociationRevision},
		Files: []scoutChatFileAttachment{{SourceID: "project_v2_source", SourceRevision: "project_v2_source_revision", Ref: strings.Repeat("a", 64), Name: "source.pdf"}}}
	driftThread.Messages = append(driftThread.Messages, driftMessage)
	driftThread.ProjectLinkOperations = append(driftThread.ProjectLinkOperations, scoutChatProjectLinkOperation{OperationID: "project-v2-drift-send",
		MessageID: driftMessage.ID, State: "drift_pending",
		SourceGroupID: replacementGroupID, AssociationIDs: []string{correction.AssociationID, "project_v2_drift_part"}})
	if err := driftLegacy.saveScoutChatThread(driftThread); err != nil {
		t.Fatal(err)
	}
	previousRuntime := currentCanonicalRuntime()
	setCanonicalRuntime(&CanonicalRuntime{mode: CanonicalModeOff, postgres: restartedStore})
	t.Cleanup(func() { setCanonicalRuntime(previousRuntime) })
	driftLegacy.pendingAttachmentUploadsMu.Lock()
	driftLegacy.pendingAttachmentUploads["project_v2_source"] = pendingAttachmentUploadGrant{SourceID: "project_v2_source", State: attachmentSourceCommitted}
	driftLegacy.pendingAttachmentUploadsMu.Unlock()
	originalMemoryPath := driftLegacy.memory.path
	driftLegacy.memory.path = t.TempDir()
	fileID := driftThread.ID + ":" + driftMessage.ID + ":0"
	if err := driftLegacy.deleteChatAttachmentFromDrive(driftUser, fileID); err == nil {
		t.Fatal("legacy save failure unexpectedly succeeded")
	}
	staleThread, _, err := driftLegacy.scoutChatThreadByID(driftUser.Email, driftThread.ID)
	if err != nil || len(staleThread.Messages[0].Files) != 1 ||
		driftLegacy.committedChatAttachmentAuthorized(driftUser.Email, driftThread.ID, driftMessage.ID, staleThread.Messages[0].Files[0]) {
		t.Fatalf("post-PG/save-failure stale file readable=%v files=%d err=%v",
			driftLegacy.committedChatAttachmentAuthorized(driftUser.Email, driftThread.ID, driftMessage.ID, staleThread.Messages[0].Files[0]),
			len(staleThread.Messages[0].Files), err)
	}
	driftLegacy.memory.path = originalMemoryPath
	if err := driftLegacy.deleteChatAttachmentFromDrive(driftUser, fileID); err != nil {
		t.Fatalf("attachment deletion exact retry: %v", err)
	}
	restartedDrift := newKanbanBoardApp()
	driftOperationID := projectChatID("project_attachment_source_revoke", snapshot.Organization.Header.ID, replacementGroupID, "project_v2_source")
	if err := restartedDrift.finishCommittedScoutProjectSourceGroupDrift(ctx, driftUser, driftThread.ID, "project-v2-drift-send",
		snapshot.Organization.Header.ID, replacementGroupID, driftOperationID); err != nil {
		t.Fatal(err)
	}
	driftRecovered, _, err := restartedDrift.scoutChatThreadByID(driftUser.Email, driftThread.ID)
	if err != nil || driftRecovered.ProjectLinkOperations[0].State != "drifted" || driftRecovered.Messages[0].Project.Status != "unavailable" {
		t.Fatalf("fresh-app drift recovery operation=%+v project=%+v err=%v", driftRecovered.ProjectLinkOperations,
			driftRecovered.Messages[0].Project, err)
	}
	var revoked, purges, remaining int
	if err := store.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM stride_project_chat_source_group_members member JOIN stride_project_associations_current current_association
 ON current_association.organization_id=member.organization_id AND current_association.association_id=member.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2 AND current_association.state='revoked'),
(SELECT count(*) FROM stride_project_chat_source_group_members member JOIN stride_project_projection_outbox outbox
 ON outbox.organization_id=member.organization_id AND outbox.association_id=member.association_id
 WHERE member.organization_id=$1 AND member.group_id=$2 AND outbox.operation='purge'),
(SELECT count(*) FROM stride_project_associations_authorized_current authorized JOIN stride_project_chat_source_group_members member
 ON member.organization_id=authorized.organization_id AND member.association_id=authorized.association_id
WHERE member.organization_id=$1 AND member.group_id=$2)`, snapshot.Organization.Header.ID, replacementGroupID).Scan(&revoked, &purges, &remaining); err != nil {
		t.Fatal(err)
	}
	if revoked != 2 || purges != 8 || remaining != 0 {
		t.Fatalf("runtime drift revoked=%d purges=%d authorized=%d", revoked, purges, remaining)
	}
}

func TestPostgresProjectChatSendV2ConfirmsZeroAndMaximumAttachmentGroups(t *testing.T) {
	for _, count := range []int{0, scoutChatMaxFilesPerMessage} {
		t.Run(fmt.Sprintf("attachments_%d", count), func(t *testing.T) {
			ctx, store, _ := migratedPostgresCanonicalStore(t)
			seedProjectPostgresAuthority(t, ctx, store)
			snapshot := projectChatSnapshotFixture(t)
			thread := scoutChatThreadRecord{ID: fmt.Sprintf("project_v2_count_%d_thread", count), OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
			seed, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, fmt.Sprintf("project_v2_count_%d_seed", count), fmt.Sprintf("project-v2-count-%d-seed", count), "Create Project.", homeProjectContextToken{
				Kind: "create", ProjectTitle: fmt.Sprintf("V2 Count %d", count), Basis: "selected", Confidence: 1,
				OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
			})
			if err != nil {
				t.Fatal(err)
			}
			text := fmt.Sprintf("Review %d files.", count)
			manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
				Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text))}
			for ordinal := 0; ordinal < count; ordinal++ {
				digest := sha256Hex([]byte(fmt.Sprintf("project-v2-count-blob-%d", ordinal)))
				manifest.Attachments = append(manifest.Attachments, projectChatManifestAttachment{Ordinal: ordinal,
					SourceID: fmt.Sprintf("project_v2_count_source_%d", ordinal), SourceRevision: fmt.Sprintf("project_v2_count_revision_%d", ordinal),
					BlobRef: digest, BlobDigest: digest, Mime: "application/pdf", Size: int64(ordinal + 1),
					DestinationRevision: fmt.Sprintf("project_v2_count_destination_%d", ordinal)})
			}
			manifest.Digest = projectChatManifestDigest(manifest)
			token := homeProjectContextToken{Version: homeProjectContextV2, Kind: "project", ProjectID: seed.ProjectID, ProjectRevision: seed.ProjectRevision,
				ProjectDigest: seed.ProjectDigest, ProjectTitle: seed.ProjectTitle, Basis: "selected", Confidence: 1,
				OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID, SourceManifestDigest: manifest.Digest}
			messageID, operation := fmt.Sprintf("project_v2_count_%d_message", count), fmt.Sprintf("project-v2-count-%d-operation", count)
			link, err := store.confirmProjectChatSendWithManifest(ctx, snapshot, thread, messageID, operation, text, token,
				privateProjectChatSourceAuthority(snapshot.Person.Header.ID), &manifest)
			if err != nil {
				t.Fatal(err)
			}
			groupID := projectChatID("project_source_group", snapshot.Organization.Header.ID, thread.ID, messageID, operation)
			if err := store.projectChatSourceGroupAuthorizedCurrent(ctx, snapshot.Organization.Header.ID, groupID, count+1); err != nil {
				t.Fatal(err)
			}
			if count == 0 {
				if err := store.invalidateProjectChatRootGroupForMutation(ctx, snapshot.Organization.Header.ID, groupID,
					"project_v2_root_delete", "source_deleted"); err != nil {
					t.Fatal(err)
				}
				if err := store.invalidateProjectChatRootGroupForMutation(ctx, snapshot.Organization.Header.ID, groupID,
					"project_v2_root_delete", "source_deleted"); err != nil {
					t.Fatalf("receipt replay: %v", err)
				}
				var authorized int
				if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current authorized
JOIN stride_project_chat_source_group_members member ON member.organization_id=authorized.organization_id AND member.association_id=authorized.association_id
WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID, groupID).Scan(&authorized); err != nil || authorized != 0 {
					t.Fatalf("root mutation authorized=%d err=%v", authorized, err)
				}
			} else {
				removeToken := projectChatCorrectionToken{ContextRevision: 1, OldAssociationID: link.AssociationID,
					OldAssociationRevision: link.AssociationRevision, OrganizationID: snapshot.Organization.Header.ID,
					Target: projectChatCorrectionTarget{Kind: "remove"}}
				removed, err := store.removeProjectChatSourceGroup(ctx, snapshot, groupID, "project-v2-group-remove", removeToken)
				if err != nil {
					t.Fatal(err)
				}
				legacyApp := newIsolatedKanbanBoardApp(t)
				legacyUser := accountStore().findUser("aj@shareability.com")
				legacyThread, err := legacyApp.createScoutChatThread(legacyUser.Email, legacyUser.Name, "Group removal recovery", scoutChatVisibilityPrivate)
				if err != nil {
					t.Fatal(err)
				}
				legacyMessage := scoutChatMessageRecord{ID: "project-v2-remove-recovery-message", Kind: "message", Role: "user", Text: "Remove me",
					AuthorEmail: normalizeAccountEmail(legacyUser.Email), Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1,
						ProjectID: link.ProjectID, ProjectRevision: link.ProjectRevision, Title: link.ProjectTitle,
						AssociationID: link.AssociationID, AssociationRevision: link.AssociationRevision}}
				legacyThread.Messages = append(legacyThread.Messages, legacyMessage)
				legacyThread.ProjectLinkOperations = append(legacyThread.ProjectLinkOperations, scoutChatProjectLinkOperation{OperationID: operation,
					MessageID: legacyMessage.ID, State: "confirmed", SourceGroupID: groupID,
					AssociationIDs: []string{link.AssociationID, "project_v2_remove_part"}})
				if err := legacyApp.saveScoutChatThread(legacyThread); err != nil {
					t.Fatal(err)
				}
				removeToken.ThreadID, removeToken.MessageID = legacyThread.ID, legacyMessage.ID
				if _, _, _, err := legacyApp.beginScoutProjectCorrection(legacyUser, legacyThread.ID, legacyMessage.ID,
					"project-v2-group-remove", "project-v2-group-remove-token", removeToken); err != nil {
					t.Fatal(err)
				}
				restartedLegacy := newKanbanBoardApp()
				committed, found, err := store.committedProjectChatSourceGroupCorrection(ctx, snapshot.Organization.Header.ID,
					snapshot.Person.Header.ID, "project-v2-group-remove", removeToken)
				if err != nil || !found || committed != removed {
					t.Fatalf("committed removal found=%v result=%+v want=%+v err=%v", found, committed, removed, err)
				}
				recovered, err := restartedLegacy.finishCommittedScoutProjectCorrection(legacyThread.ID, legacyMessage.ID,
					"project-v2-group-remove", committed)
				if err != nil || recovered.Messages[0].Project.Status != "removed" {
					t.Fatalf("fresh-app removal recovery project=%+v err=%v", recovered.Messages[0].Project, err)
				}
			}
		})
	}
}

func TestPostgresProjectChatSendV2BindsExactReplyAncestry(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	thread := scoutChatThreadRecord{ID: "project_v2_reply_thread", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	seed, err := store.confirmHomeProjectChatSend(ctx, snapshot, thread, "project_v2_reply_parent", "project-v2-reply-parent-operation", "Canonical parent.", homeProjectContextToken{
		Kind: "create", ProjectTitle: "V2 Reply Project", Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	parentID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, "project_v2_reply_parent")
	var parentRevision, parentACL, parentPurge int64
	var parentDigest, parentAudience string
	if err := store.pool.QueryRow(ctx, `SELECT content_revision,encode(content_digest,'hex'),encode(audience_digest,'hex'),acl_version,purge_generation
FROM stride_conversation_events WHERE tenant_id=$1 AND event_id=$2`, snapshot.Organization.Header.ID, parentID).
		Scan(&parentRevision, &parentDigest, &parentAudience, &parentACL, &parentPurge); err != nil {
		t.Fatal(err)
	}
	text := "Reply with exact ancestry."
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion,
		Destination: homeProjectDestination{Route: "thread", ThreadID: thread.ID}, TextDigest: sha256Hex([]byte(text)),
		Reply: &projectChatManifestReply{MessageID: "project_v2_reply_parent", EventID: parentID, SourceRevision: parentRevision,
			SourceDigest: parentDigest, LegacyDigest: sha256Hex([]byte("project-v2-parent-legacy")), AuthorEmail: snapshot.Session.Email,
			AuthorPersonID: snapshot.Person.Header.ID, AudienceDigest: parentAudience, ACLRevision: parentACL, PurgeGeneration: parentPurge}}
	manifest.Digest = projectChatManifestDigest(manifest)
	token := homeProjectContextToken{Version: homeProjectContextV2, Kind: "project", ProjectID: seed.ProjectID, ProjectRevision: seed.ProjectRevision,
		ProjectDigest: seed.ProjectDigest, ProjectTitle: seed.ProjectTitle, Basis: "selected", Confidence: 1,
		OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID, SourceManifestDigest: manifest.Digest}
	messageID, operation := "project_v2_reply_child", "project-v2-reply-child-operation"
	if _, err := store.confirmProjectChatSendWithManifest(ctx, snapshot, thread, messageID, operation, text, token,
		privateProjectChatSourceAuthority(snapshot.Person.Header.ID), &manifest); err != nil {
		t.Fatal(err)
	}
	childID := projectChatID("conversation_event", snapshot.Organization.Header.ID, thread.ID, messageID)
	var dependencyParent string
	if err := store.pool.QueryRow(ctx, `SELECT parent_event_id FROM stride_project_chat_reply_dependencies WHERE organization_id=$1 AND child_event_id=$2`,
		snapshot.Organization.Header.ID, childID).Scan(&dependencyParent); err != nil || dependencyParent != parentID {
		t.Fatalf("reply parent=%q err=%v", dependencyParent, err)
	}
	groupID := projectChatID("project_source_group", snapshot.Organization.Header.ID, thread.ID, messageID, operation)
	if _, err := store.pool.Exec(ctx, `UPDATE stride_conversation_events SET content_revision=content_revision+1,
content_digest=decode($3,'hex') WHERE tenant_id=$1 AND event_id=$2`, snapshot.Organization.Header.ID, parentID,
		sha256Hex([]byte("edited reply parent"))); err != nil {
		t.Fatal(err)
	}
	if err := store.invalidateProjectChatReplyGroupForDrift(ctx, snapshot.Organization.Header.ID, groupID,
		"project_v2_reply_parent_drift", "parent_edited"); err != nil {
		t.Fatal(err)
	}
	if err := store.invalidateProjectChatReplyGroupForDrift(ctx, snapshot.Organization.Header.ID, groupID,
		"project_v2_reply_parent_drift", "parent_edited"); err != nil {
		t.Fatalf("reply drift receipt replay: %v", err)
	}
	var authorized int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current authorized
JOIN stride_project_chat_source_group_members member ON member.organization_id=authorized.organization_id AND member.association_id=authorized.association_id
WHERE member.organization_id=$1 AND member.group_id=$2`, snapshot.Organization.Header.ID, groupID).Scan(&authorized); err != nil || authorized != 0 {
		t.Fatalf("reply-drift group authorized=%d err=%v", authorized, err)
	}
}
