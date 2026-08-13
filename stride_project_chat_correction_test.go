package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func correctionTokenFromTruth(snapshot StrideE10TenantAuthoritySnapshot, threadID, messageID string, contextRevision int64, truth projectChatCorrectionTruth, target projectChatCorrectionTarget) projectChatCorrectionToken {
	return projectChatCorrectionToken{
		Version: projectChatCorrectionTokenVersion, Purpose: "sent_message_project_correction", ThreadID: threadID, MessageID: messageID,
		ContextRevision: contextRevision, OldAssociationID: truth.AssociationID, OldAssociationRevision: truth.AssociationRevision,
		OldAssociationDigest: truth.AssociationDigest, SourceEventID: truth.SourceEventID, SourceEventRevision: truth.SourceEventRevision,
		SourceDigest: truth.SourceDigest, SourceACLRevision: truth.SourceACLRevision, SourceACLDigest: truth.SourceACLDigest,
		PurgeGeneration: truth.PurgeGeneration, PersonID: snapshot.Person.Header.ID, OrganizationID: snapshot.Organization.Header.ID,
		MembershipID: snapshot.Membership.Header.ID, MembershipRevision: snapshot.Membership.Header.Revision,
		SessionSubjectDigest: snapshot.SessionHash, SessionRevision: snapshot.ActiveSession.SessionRevision,
		AuthorityGeneration: snapshot.Generation, Target: target,
	}
}

func TestPostgresProjectChatCorrectionReplacesThenRemovesExactly(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	threadA := scoutChatThreadRecord{ID: "scout_correction_source", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	linkA, err := store.confirmHomeProjectChatSend(ctx, snapshot, threadA, "message_correction_source", "operation-create-correction-a", "Start Project Alpha.", homeProjectContextToken{Kind: "create", ProjectTitle: "Project Alpha", Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID})
	if err != nil {
		t.Fatal(err)
	}
	threadB := scoutChatThreadRecord{ID: "scout_correction_target", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	linkB, err := store.confirmHomeProjectChatSend(ctx, snapshot, threadB, "message_correction_target", "operation-create-correction-b", "Start Project Beta.", homeProjectContextToken{Kind: "create", ProjectTitle: "Project Beta", Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID})
	if err != nil {
		t.Fatal(err)
	}
	messageProject := &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, ProjectID: linkA.ProjectID, ProjectRevision: linkA.ProjectRevision, Title: linkA.ProjectTitle, AssociationID: linkA.AssociationID, AssociationRevision: linkA.AssociationRevision}
	truthA, err := store.projectChatCorrectionTruth(ctx, snapshot, threadA.ID, "message_correction_source", messageProject)
	if err != nil {
		t.Fatal(err)
	}
	targetB := projectChatCorrectionTarget{Kind: "project", ProjectID: linkB.ProjectID, ProjectRevision: linkB.ProjectRevision, ProjectDigest: linkB.ProjectDigest, ProjectTitle: linkB.ProjectTitle}
	tokenAB := correctionTokenFromTruth(snapshot, threadA.ID, "message_correction_source", 1, truthA, targetB)
	corrected, err := store.correctProjectChatAssociation(ctx, snapshot, "correction-alpha-to-beta", "opaque-token-a-b", tokenAB)
	if err != nil {
		t.Fatal(err)
	}
	if corrected.Status != "confirmed" || corrected.ContextRevision != 2 || corrected.ProjectID != linkB.ProjectID || corrected.AssociationRevision != 1 || corrected.OldResultRevision != 3 {
		t.Fatalf("unexpected correction result: %+v", corrected)
	}
	pendingJournal := scoutChatProjectCorrectionOperation{OperationID: "correction-alpha-to-beta", TokenDigest: homeProjectTokenDigest("opaque-token-a-b"), MessageID: "message_correction_source", ExpectedContextRevision: 1, State: "pending"}
	pendingJournal.OrganizationID = snapshot.Organization.Header.ID
	pendingJournal.ActorPersonID = snapshot.Person.Header.ID
	recovered, found, err := store.committedProjectChatCorrection(ctx, snapshot.Organization.Header.ID, snapshot.Person.Header.ID, threadA.ID, pendingJournal)
	if err != nil || !found || recovered != corrected {
		t.Fatalf("committed correction recovery found=%v result=%+v err=%v", found, recovered, err)
	}
	badJournal := pendingJournal
	badJournal.TokenDigest = strings.Repeat("b", 64)
	if _, _, err := store.committedProjectChatCorrection(ctx, snapshot.Organization.Header.ID, snapshot.Person.Header.ID, threadA.ID, badJournal); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("changed recovery journal err=%v", err)
	}
	// Simulate the cross-store crash boundary: PostgreSQL committed above, but
	// the legacy thread has only the pending journal. Recovery must finalize
	// from the immutable receipt without any installed live-session resolver.
	priorCanonical := currentCanonicalRuntime()
	setCanonicalRuntime(&CanonicalRuntime{mode: CanonicalModeOff, postgres: store})
	t.Cleanup(func() { setCanonicalRuntime(priorCanonical) })
	app := newIsolatedKanbanBoardApp(t)
	owner := &userAccount{Email: snapshot.Session.Email, Name: "AJ"}
	legacyThread := threadA
	legacyThread.Messages = []scoutChatMessageRecord{{
		ID: "message_correction_source", Kind: "message", Role: "user", Text: "Start Project Alpha.",
		AuthorEmail: normalizeAccountEmail(owner.Email), Project: messageProject,
	}}
	legacyRaw, err := encodeScoutChatThread(legacyThread)
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendScoutChatThread(legacyThread.ID, legacyRaw, scoutChatThreadMetadata(legacyThread)); err != nil || !appended {
		t.Fatalf("append legacy correction thread appended=%v err=%v", appended, err)
	}
	journaled, _, created, err := app.beginScoutProjectCorrection(owner, threadA.ID, "message_correction_source", "correction-alpha-to-beta", "opaque-token-a-b", tokenAB)
	if err != nil || !created || journaled.Messages[0].Project.Status != "unavailable" {
		t.Fatalf("pending correction did not fail closed: created=%v project=%+v err=%v", created, journaled.Messages[0].Project, err)
	}
	reconciled, err := app.reconcileCommittedProjectCorrections(context.Background(), owner, threadA.ID)
	if err != nil || !projectChatContextMatchesCorrection(reconciled.Messages[0].Project, corrected) || reconciled.ProjectCorrectionOperations[0].State != "confirmed" {
		t.Fatalf("receipt-only correction recovery thread=%+v err=%v", reconciled, err)
	}
	var oldState, newState string
	if err := store.pool.QueryRow(ctx, `SELECT state FROM stride_project_associations_current WHERE association_id=$1`, linkA.AssociationID).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT state FROM stride_project_associations_authorized_current WHERE association_id=$1`, corrected.AssociationID).Scan(&newState); err != nil {
		t.Fatal(err)
	}
	if oldState != "corrected" || newState != "confirmed" {
		t.Fatalf("association states old=%q new=%q", oldState, newState)
	}
	// If an operation never reached PostgreSQL, a later canonical session for
	// the same person/org can prove the old association is still current,
	// retire the abandoned journal, and reopen the chooser after restart.
	recoveryApp := newIsolatedKanbanBoardApp(t)
	recoveryOwner := &userAccount{Email: snapshot.Session.Email, Name: "AJ"}
	recoveryThread := threadB
	recoveryThread.Messages = []scoutChatMessageRecord{{ID: "message_correction_target", Kind: "message", Role: "user", Text: "Start Project Beta.", AuthorEmail: normalizeAccountEmail(recoveryOwner.Email), Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, ProjectID: linkB.ProjectID, ProjectRevision: linkB.ProjectRevision, Title: linkB.ProjectTitle, AssociationID: linkB.AssociationID, AssociationRevision: linkB.AssociationRevision}}}
	recoveryRaw, err := encodeScoutChatThread(recoveryThread)
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := recoveryApp.memory.appendScoutChatThread(recoveryThread.ID, recoveryRaw, scoutChatThreadMetadata(recoveryThread)); err != nil || !appended {
		t.Fatalf("append recovery thread appended=%v err=%v", appended, err)
	}
	recoveryTruth, err := store.projectChatCorrectionTruth(ctx, snapshot, recoveryThread.ID, "message_correction_target", recoveryThread.Messages[0].Project)
	if err != nil {
		t.Fatal(err)
	}
	recoveryToken := correctionTokenFromTruth(snapshot, recoveryThread.ID, "message_correction_target", 1, recoveryTruth, projectChatCorrectionTarget{Kind: "remove"})
	journaledRecovery, _, created, err := recoveryApp.beginScoutProjectCorrection(recoveryOwner, recoveryThread.ID, "message_correction_target", "correction-abandoned-before-pg", "opaque-abandoned", recoveryToken)
	if err != nil || !created || journaledRecovery.Messages[0].Project.Status != "unavailable" {
		t.Fatalf("abandoned correction journal=%+v created=%v err=%v", journaledRecovery, created, err)
	}
	sessions := newSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	sessionToken, err := sessions.createMemberSession(snapshot.Session.Email, snapshot.Person.Header.ID, snapshot.Organization.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.ActiveSession.SessionRevision, func(string, string, string, int64) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	recoverySnapshot := snapshot
	recoverySnapshot.Person.AccountSubjectDigest = sha256Hex([]byte(normalizeAccountEmail(snapshot.Session.Email)))
	recoverySnapshot.SessionHash = hashResetToken(sessionToken)
	recoverySnapshot.ActiveSession.SessionSubjectDigest = recoverySnapshot.SessionHash
	recoverySnapshot.ActiveSession.ExpiresAt = time.Now().UTC().Add(time.Hour)
	organizations := NewOrganizationAuthorityService()
	organizations.persons[snapshot.Person.Header.ID] = recoverySnapshot.Person
	organizations.accountPersons[recoverySnapshot.Person.AccountSubjectDigest] = snapshot.Person.Header.ID
	organizations.organizations[snapshot.Organization.Header.ID] = recoverySnapshot.Organization
	organizations.memberships[snapshot.Membership.Header.ID] = recoverySnapshot.Membership
	organizations.sessions[recoverySnapshot.SessionHash] = recoverySnapshot.ActiveSession
	restoreConverter := InstallStrideE10TenantRuntimeConverter(&StrideE10TenantConverter{resolver: &strideE10MainTenantAuthorityResolver{sessions: sessions, organizations: organizations, now: time.Now}})
	defer restoreConverter()
	recoveryContext := strideE10TenantContextWithSessionHash(context.Background(), recoverySnapshot.SessionHash)
	recoveredThread, err := recoveryApp.reconcileCommittedProjectCorrections(recoveryContext, recoveryOwner, recoveryThread.ID)
	if err != nil || recoveredThread.Messages[0].Project.Status != "confirmed" || recoveredThread.ProjectCorrectionOperations[0].State != "failed" {
		t.Fatalf("receipt-absent restart recovery thread=%+v err=%v", recoveredThread, err)
	}
	if _, err := store.correctProjectChatAssociation(ctx, recoverySnapshot, "correction-abandoned-before-pg", "opaque-abandoned", recoveryToken); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("durably abandoned correction committed after recovery: %v", err)
	}
	abandonmentOperationID := projectChatID("project_correction", snapshot.Organization.Header.ID, recoveryThread.ID, "message_correction_target", "correction-abandoned-before-pg")
	var abandonmentCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_chat_correction_abandonments WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, abandonmentOperationID).Scan(&abandonmentCount); err != nil || abandonmentCount != 1 {
		t.Fatalf("durable abandonment count=%d err=%v", abandonmentCount, err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM stride_project_chat_correction_abandonments WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, abandonmentOperationID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("correction abandonment was mutable: %v", err)
	}
	if _, _, created, err := recoveryApp.beginScoutProjectCorrection(recoveryOwner, recoveryThread.ID, "message_correction_target", "correction-after-restart", "opaque-new", recoveryToken); err != nil || !created {
		t.Fatalf("replacement correction after restart created=%v err=%v", created, err)
	}
	var unlist, list int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE association_id=$1 AND association_revision=$2 AND operation='unlist_old'),count(*) FILTER (WHERE association_id=$3 AND association_revision=1 AND operation='list_new') FROM stride_project_projection_outbox`, linkA.AssociationID, corrected.OldResultRevision, corrected.AssociationID).Scan(&unlist, &list); err != nil || unlist != 4 || list != 4 {
		t.Fatalf("projection jobs unlist=%d list=%d err=%v", unlist, list, err)
	}
	replay, err := store.correctProjectChatAssociation(ctx, snapshot, "correction-alpha-to-beta", "opaque-token-a-b", tokenAB)
	if err != nil || replay != corrected {
		t.Fatalf("correction replay=%+v err=%v", replay, err)
	}
	changed := tokenAB
	changed.Target.ProjectTitle = "tampered"
	if _, err := store.correctProjectChatAssociation(ctx, snapshot, "correction-alpha-to-beta", "opaque-token-a-b", changed); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("changed correction replay err=%v", err)
	}
	truthB, err := store.projectChatCorrectionTruth(ctx, snapshot, threadA.ID, "message_correction_source", &scoutChatProjectContext{Status: "confirmed", ContextRevision: 2, AssociationID: corrected.AssociationID, AssociationRevision: corrected.AssociationRevision})
	if err != nil {
		t.Fatal(err)
	}
	tokenRemove := correctionTokenFromTruth(snapshot, threadA.ID, "message_correction_source", 2, truthB, projectChatCorrectionTarget{Kind: "remove"})
	removed, err := store.correctProjectChatAssociation(ctx, snapshot, "correction-beta-to-none", "opaque-token-remove", tokenRemove)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Status != "removed" || removed.ContextRevision != 3 || removed.AssociationID != "" || removed.OldAssociationID != corrected.AssociationID || removed.OldResultRevision != 2 {
		t.Fatalf("unexpected removal result: %+v", removed)
	}
	if err := store.pool.QueryRow(ctx, `SELECT state FROM stride_project_associations_current WHERE association_id=$1`, corrected.AssociationID).Scan(&oldState); err != nil || oldState != "removed" {
		t.Fatalf("removed current state=%q err=%v", oldState, err)
	}
}

func TestProjectChatCorrectionJournalIsAuthorOnlyAndRetryStable(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	other := accountStore().findUser("tim@shareability.com")
	if owner == nil || other == nil {
		t.Fatal("test accounts unavailable")
	}
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "Correction", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "message_project_correction", Kind: "message", Role: "user", Text: "Plan it", AuthorEmail: normalizeAccountEmail(owner.Email), CreatedAt: "2026-08-12T20:00:00Z", Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, Title: "Alpha", AssociationID: "association_alpha", AssociationRevision: 2}}
	thread.Messages = append(thread.Messages, message)
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	token := projectChatCorrectionToken{ThreadID: thread.ID, MessageID: message.ID, ContextRevision: 1, OldAssociationID: "association_alpha", OldAssociationRevision: 2}
	if _, _, _, err := app.beginScoutProjectCorrection(other, thread.ID, message.ID, "correction-other", "token", token); err == nil {
		t.Fatalf("another user correction err=%v", err)
	}
	legacy := thread
	legacy.Messages = append([]scoutChatMessageRecord(nil), thread.Messages...)
	legacy.Messages[0].AuthorEmail = ""
	if err := app.saveScoutChatThread(legacy); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := app.beginScoutProjectCorrection(owner, thread.ID, message.ID, "correction-legacy", "token", token); !errors.Is(err, ErrProjectAuthorityDenied) {
		t.Fatalf("unstamped legacy correction err=%v", err)
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	first, operation, created, err := app.beginScoutProjectCorrection(owner, thread.ID, message.ID, "correction-owner", "token", token)
	if err != nil || !created || operation.State != "pending" || len(first.ProjectCorrectionOperations) != 1 || first.Messages[0].Project.Status != "unavailable" {
		t.Fatalf("first correction created=%v operation=%+v err=%v", created, operation, err)
	}
	second, replay, created, err := app.beginScoutProjectCorrection(owner, thread.ID, message.ID, "correction-owner", "token", token)
	if err != nil || created || replay != operation || len(second.ProjectCorrectionOperations) != 1 {
		t.Fatalf("retry correction created=%v operation=%+v err=%v", created, replay, err)
	}
	if _, _, _, err := app.beginScoutProjectCorrection(owner, thread.ID, message.ID, "correction-competing", "other-token", token); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("competing pending correction err=%v", err)
	}
	if err := app.failScoutProjectCorrection(owner, thread.ID, message.ID, "correction-owner", true); err != nil {
		t.Fatal(err)
	}
	failedProjection, _, err := app.scoutChatThreadByID(owner.Email, thread.ID)
	if err != nil || failedProjection.Messages[0].Project.Status != "confirmed" {
		t.Fatalf("receipt-proven-absent failure did not restore retryable truth: %+v err=%v", failedProjection.Messages[0].Project, err)
	}
	if _, _, _, err := app.beginScoutProjectCorrection(owner, thread.ID, message.ID, "correction-owner", "token", token); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("failed operation replay err=%v", err)
	}
	third, replacement, created, err := app.beginScoutProjectCorrection(owner, thread.ID, message.ID, "correction-replacement", "replacement-token", token)
	if err != nil || !created || replacement.State != "pending" || len(third.ProjectCorrectionOperations) != 2 {
		t.Fatalf("replacement correction created=%v operation=%+v err=%v", created, replacement, err)
	}
}

func TestProjectChatCorrectionConfirmedReplayPrecedesOldMessageCASAndRejectsProjectionDrift(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "Correction replay", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "message_project_replay", Kind: "message", Role: "user", Text: "Plan it", AuthorEmail: normalizeAccountEmail(owner.Email), CreatedAt: "2026-08-12T20:00:00Z", Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, ProjectID: "project_alpha", ProjectRevision: 1, Title: "Alpha", AssociationID: "association_alpha", AssociationRevision: 2}}
	thread.Messages = append(thread.Messages, message)
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	token := projectChatCorrectionToken{ThreadID: thread.ID, MessageID: message.ID, ContextRevision: 1, OldAssociationID: "association_alpha", OldAssociationRevision: 2}
	journaled, operation, _, err := app.beginScoutProjectCorrection(owner, thread.ID, message.ID, "correction-replay", "opaque-token", token)
	if err != nil || operation.State != "pending" {
		t.Fatalf("begin operation=%+v err=%v", operation, err)
	}
	result := confirmedProjectChatCorrection{Status: "confirmed", ContextRevision: 2, OldAssociationID: "association_alpha", OldAssociationRevision: 2, OldResultRevision: 3, ProjectID: "project_beta", ProjectRevision: 4, ProjectTitle: "Beta", AssociationID: "association_beta", AssociationRevision: 1}
	finished, err := app.finishScoutProjectCorrection(owner, thread.ID, message.ID, "correction-replay", result)
	if err != nil || !projectChatContextMatchesCorrection(finished.Messages[0].Project, result) {
		t.Fatalf("finish thread=%+v err=%v", finished, err)
	}
	_, replay, created, err := app.beginScoutProjectCorrection(owner, thread.ID, message.ID, "correction-replay", "opaque-token", token)
	if err != nil || created || replay.State != "confirmed" || replay.ResultOldResultRevision != 3 {
		t.Fatalf("confirmed replay after message CAS operation=%+v created=%v err=%v", replay, created, err)
	}
	corrupt := journaled
	corrupt = finished
	corrupt.Messages[0].Project.Title = "Corrupt"
	if err := app.saveScoutChatThread(corrupt); err != nil {
		t.Fatal(err)
	}
	if _, err := app.finishScoutProjectCorrection(owner, thread.ID, message.ID, "correction-replay", result); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("corrupt projection replay err=%v", err)
	}
}

func TestPostgresProjectChatSourceMutationInvalidatesReplacementRevisionAndReplays(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	seedProjectPostgresAuthority(t, ctx, store)
	snapshot := projectChatSnapshotFixture(t)
	threadA := scoutChatThreadRecord{ID: "scout_source_mutation_a", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	linkA, err := store.confirmHomeProjectChatSend(ctx, snapshot, threadA, "message_source_mutation", "operation-source-a", "Start Alpha.", homeProjectContextToken{Kind: "create", ProjectTitle: "Alpha", Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID})
	if err != nil {
		t.Fatal(err)
	}
	threadB := scoutChatThreadRecord{ID: "scout_source_mutation_b", OwnerEmail: snapshot.Session.Email, Visibility: scoutChatVisibilityPrivate}
	linkB, err := store.confirmHomeProjectChatSend(ctx, snapshot, threadB, "message_source_target", "operation-source-b", "Start Beta.", homeProjectContextToken{Kind: "create", ProjectTitle: "Beta", Basis: "selected", Confidence: 1, OrganizationID: snapshot.Organization.Header.ID, PersonID: snapshot.Person.Header.ID})
	if err != nil {
		t.Fatal(err)
	}
	truthA, err := store.projectChatCorrectionTruth(ctx, snapshot, threadA.ID, "message_source_mutation", &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, AssociationID: linkA.AssociationID, AssociationRevision: linkA.AssociationRevision})
	if err != nil {
		t.Fatal(err)
	}
	token := correctionTokenFromTruth(snapshot, threadA.ID, "message_source_mutation", 1, truthA, projectChatCorrectionTarget{Kind: "project", ProjectID: linkB.ProjectID, ProjectRevision: linkB.ProjectRevision, ProjectDigest: linkB.ProjectDigest, ProjectTitle: linkB.ProjectTitle})
	corrected, err := store.correctProjectChatAssociation(ctx, snapshot, "source-alpha-to-beta", "opaque-source-token", token)
	if err != nil || corrected.AssociationRevision != 1 {
		t.Fatalf("corrected=%+v err=%v", corrected, err)
	}
	message := scoutChatMessageRecord{ID: "message_source_mutation", AuthorEmail: snapshot.Session.Email, Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 2, ProjectID: corrected.ProjectID, ProjectRevision: corrected.ProjectRevision, Title: corrected.ProjectTitle, AssociationID: corrected.AssociationID, AssociationRevision: corrected.AssociationRevision}}
	digest := projectSourceMutationRequestDigest("edit", threadA.ID, message.ID, true, "Edited body")
	operationID := projectSourceMutationOperationID("edit", threadA.ID, message.ID, digest)
	resultRevision, err := store.invalidateProjectChatSourceForMutation(ctx, snapshot, threadA, message, operationID, digest, "edit")
	if err != nil || resultRevision < 2 {
		t.Fatalf("invalidate revision=%d err=%v", resultRevision, err)
	}
	receiptOperationID := projectChatID("project_source_mutation", snapshot.Organization.Header.ID, threadA.ID, message.ID, operationID)
	_, forgedErr := store.pool.Exec(ctx, `INSERT INTO stride_project_chat_source_mutation_receipts
(organization_id,operation_id,operation_key_digest,request_fingerprint,mutation_kind,thread_id,message_id,source_event_id,source_prior_revision,source_result_revision,association_id,association_revision,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at)
SELECT organization_id,operation_id,decode($2,'hex'),request_fingerprint,mutation_kind,thread_id,message_id,source_event_id,source_prior_revision,source_result_revision,$3,$4,actor_person_id,actor_membership_id,actor_membership_revision,session_subject_digest,session_revision,authority_generation,recorded_at
FROM stride_project_chat_source_mutation_receipts WHERE organization_id=$1 AND operation_id=$5`, snapshot.Organization.Header.ID, strings.Repeat("d", 64), linkB.AssociationID, linkB.AssociationRevision, receiptOperationID)
	if forgedErr == nil || !strings.Contains(forgedErr.Error(), "exact invalidated canonical truth") {
		t.Fatalf("unrelated association receipt was not rejected by exact truth guard: %v", forgedErr)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_project_chat_source_mutation_receipts SET recorded_at=recorded_at+interval '1 second' WHERE organization_id=$1 AND operation_id=$2`, snapshot.Organization.Header.ID, receiptOperationID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("source mutation receipt update was not rejected: %v", err)
	}
	var authorized, purges int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_associations_authorized_current WHERE association_id=$1`, corrected.AssociationID).Scan(&authorized); err != nil || authorized != 0 {
		t.Fatalf("authorized=%d err=%v", authorized, err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM stride_project_projection_outbox WHERE association_id=$1 AND operation='purge'`, corrected.AssociationID).Scan(&purges); err != nil || purges != 4 {
		t.Fatalf("purges=%d err=%v", purges, err)
	}
	replay, err := store.invalidateProjectChatSourceForMutation(ctx, snapshot, threadA, message, operationID, digest, "edit")
	if err != nil || replay != resultRevision {
		t.Fatalf("replay revision=%d err=%v", replay, err)
	}
	changedDigest := strings.Repeat("a", 64)
	if _, err := store.invalidateProjectChatSourceForMutation(ctx, snapshot, threadA, message, operationID, changedDigest, "edit"); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("changed replay err=%v", err)
	}
	operation := scoutChatProjectSourceMutationOperation{
		OperationID: operationID, RequestDigest: digest, Kind: "edit", MessageID: message.ID,
		ActorEmail: normalizeAccountEmail(snapshot.Session.Email), OrganizationID: snapshot.Organization.Header.ID,
		ActorPersonID: snapshot.Person.Header.ID, ExpectedProject: *message.Project, State: "pending",
		TextPresent: true, Text: "Edited body", ResultContextRevision: 3,
	}
	receiptedRevision, found, err := store.committedProjectChatSourceMutation(ctx, operation.OrganizationID, operation.ActorPersonID, threadA.ID, operation)
	if err != nil || !found || receiptedRevision != resultRevision {
		t.Fatalf("source mutation receipt recovery revision=%d found=%v err=%v", receiptedRevision, found, err)
	}
	badOperation := operation
	badOperation.ExpectedProject.AssociationID = linkA.AssociationID
	if _, _, err := store.committedProjectChatSourceMutation(ctx, operation.OrganizationID, operation.ActorPersonID, threadA.ID, badOperation); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("wrong association recovery err=%v", err)
	}

	priorCanonical := currentCanonicalRuntime()
	setCanonicalRuntime(&CanonicalRuntime{mode: CanonicalModeOff, postgres: store})
	t.Cleanup(func() { setCanonicalRuntime(priorCanonical) })
	app := newIsolatedKanbanBoardApp(t)
	owner := &userAccount{Email: snapshot.Session.Email, Name: "AJ"}
	legacyThread := threadA
	legacyMessage := message
	legacyMessage.Text = "Before"
	legacyThread.Messages = []scoutChatMessageRecord{legacyMessage}
	legacyRaw, err := encodeScoutChatThread(legacyThread)
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendScoutChatThread(legacyThread.ID, legacyRaw, scoutChatThreadMetadata(legacyThread)); err != nil || !appended {
		t.Fatalf("append legacy source thread appended=%v err=%v", appended, err)
	}
	journaled, pending, created, err := app.beginScoutProjectSourceMutationLocked(owner, legacyThread, 0, "edit", true, "Edited body", projectChatMutationAuthorityBinding{OrganizationID: snapshot.Organization.Header.ID, ActorPersonID: snapshot.Person.Header.ID})
	if err != nil || !created || journaled.Messages[0].Project.Status != "unavailable" || pending.OperationID != operationID {
		t.Fatalf("pending source mutation journal=%+v created=%v err=%v", pending, created, err)
	}
	resumed, err := app.resumePendingProjectSourceMutations(context.Background(), owner, threadA.ID)
	if err != nil || resumed.Messages[0].Text != "Edited body" || resumed.ProjectSourceMutationOperations[0].State != "confirmed" || resumed.Messages[0].Project.Status != "unavailable" {
		t.Fatalf("receipt-only source recovery thread=%+v err=%v", resumed, err)
	}
}

func TestProjectLinkedEditValidatesBeforeJournalOrCanonicalMutation(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "Safe edit", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "message_safe_edit", Kind: "message", Role: "user", Text: "Keep me", AuthorEmail: normalizeAccountEmail(owner.Email), CreatedAt: "2026-08-12T20:00:00Z", Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, Title: "Alpha", AssociationID: "association_alpha", AssociationRevision: 2}}
	thread.Messages = append(thread.Messages, message)
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if _, _, err := app.editScoutChatThreadMessage(context.Background(), owner, thread.ID, message.ID, &empty, nil); err == nil {
		t.Fatal("empty linked edit unexpectedly succeeded")
	}
	current, _, err := app.scoutChatThreadByID(owner.Email, thread.ID)
	if err != nil || current.Messages[0].Text != "Keep me" || current.Messages[0].Project.Status != "confirmed" || len(current.ProjectSourceMutationOperations) != 0 {
		t.Fatalf("invalid edit mutated thread=%+v err=%v", current, err)
	}
}

func TestProjectSourceMutationJournalFailsClosedAndIsRetryStable(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "Mutation journal", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "message_mutation_journal", Kind: "message", Role: "user", Text: "Before", AuthorEmail: normalizeAccountEmail(owner.Email), CreatedAt: "2026-08-12T20:00:00Z", Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 2, ProjectID: "project_beta", ProjectRevision: 1, Title: "Beta", AssociationID: "association_beta", AssociationRevision: 1}}
	thread.Messages = append(thread.Messages, message)
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	journaled, operation, created, err := app.beginScoutProjectSourceMutationLocked(owner, thread, 0, "edit", true, "After")
	if err != nil || !created || operation.State != "pending" || journaled.Messages[0].Text != "Before" || journaled.Messages[0].Project.Status != "unavailable" || journaled.Messages[0].Project.ContextRevision != 3 {
		t.Fatalf("journaled=%+v operation=%+v created=%v err=%v", journaled, operation, created, err)
	}
	persisted, _, err := app.scoutChatThreadByID(owner.Email, thread.ID)
	if err != nil || len(persisted.ProjectSourceMutationOperations) != 1 || persisted.Messages[0].Project.Status != "unavailable" {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
	_, replay, created, err := app.beginScoutProjectSourceMutationLocked(owner, persisted, 0, "edit", true, "After")
	if err != nil || created || replay != operation {
		t.Fatalf("replay=%+v created=%v err=%v", replay, created, err)
	}
	if _, _, _, err := app.beginScoutProjectSourceMutationLocked(owner, persisted, 0, "edit", true, "Different"); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("changed pending mutation err=%v", err)
	}
	terminal := persisted
	terminal.Messages = append([]scoutChatMessageRecord(nil), persisted.Messages...)
	if err := app.failScoutProjectSourceMutationLocked(terminal, operation.OperationID, true); err != nil {
		t.Fatal(err)
	}
	failed, _, err := app.scoutChatThreadByID(owner.Email, thread.ID)
	if err != nil || failed.Messages[0].Project.Status != "confirmed" || failed.ProjectSourceMutationOperations[0].State != "failed" {
		t.Fatalf("terminal recovery thread=%+v err=%v", failed, err)
	}
	if _, _, _, err := app.beginScoutProjectSourceMutationLocked(owner, failed, 0, "edit", true, "After"); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("terminal exact retry err=%v", err)
	}
}

func TestProjectSourceDeleteJournalReturnsExactConfirmedReplay(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "Delete journal", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "message_delete_journal", Kind: "message", Role: "user", Text: "Delete me", AuthorEmail: normalizeAccountEmail(owner.Email), CreatedAt: "2026-08-12T20:00:00Z", Project: &scoutChatProjectContext{Status: "confirmed", ContextRevision: 1, ProjectID: "project_alpha", ProjectRevision: 1, Title: "Alpha", AssociationID: "association_alpha", AssociationRevision: 2}}
	thread.Messages = append(thread.Messages, message)
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	journaled, operation, _, err := app.beginScoutProjectSourceMutationLocked(owner, thread, 0, "delete", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if projected := app.projectScoutChatThreadForViewer(owner.Email, journaled); len(projected.Messages) != 0 || projected.Preview == message.Text {
		t.Fatalf("pending canonical deletion leaked legacy body messages=%+v preview=%q", projected.Messages, projected.Preview)
	}
	if entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, thread.ID); !ok || entry.Metadata["preview"] == message.Text {
		t.Fatalf("pending canonical deletion leaked through index metadata: %+v", entry.Metadata)
	}
	if _, _, err := applyProjectSourceDeleteToThread(&journaled, message.ID); err != nil {
		t.Fatal(err)
	}
	if err := markProjectSourceMutationConfirmed(&journaled, operation.OperationID, 2, ""); err != nil {
		t.Fatal(err)
	}
	if err := app.saveScoutChatThread(journaled); err != nil {
		t.Fatal(err)
	}
	replayed, err := app.deleteScoutChatThreadMessageWithContext(context.Background(), owner, thread.ID, message.ID)
	if err != nil || scoutChatMessageIndex(replayed, message.ID) >= 0 || len(replayed.ProjectSourceMutationOperations) != 1 {
		t.Fatalf("confirmed delete replay thread=%+v err=%v", replayed, err)
	}
}

var _ = context.Background
