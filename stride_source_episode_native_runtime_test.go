package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixedSourceEpisodePurgeResolver int64

func (resolver fixedSourceEpisodePurgeResolver) CurrentPurgeGeneration(context.Context, string) (int64, error) {
	return int64(resolver), nil
}

func newNativeSourceEpisodeRuntimeFixture(t *testing.T) (*kanbanBoardApp, string) {
	t.Helper()
	memory, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(t.TempDir(), "source-episodes.jsonl")
	ledger, err := OpenFileSourceEpisodeLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: memory, sourceEpisodes: ledger}
	app.sourceEpisodeRegistry, err = initializeSourceEpisodeNativeRuntime(app)
	if err != nil {
		t.Fatal(err)
	}
	return app, ledgerPath
}

func nativeSourceEpisodeInventory(t *testing.T, app *kanbanBoardApp, principalID string, start, end time.Time) ([]BrainSourceMetadata, BrainSourceInventoryPage) {
	t.Helper()
	if strings.Contains(principalID, "@") {
		principalID = strideRuntimePrincipalForEmail(principalID)
	}
	adapter, err := NewSourceEpisodeShadowBrainAdapter(app.sourceEpisodes, app.sourceEpisodeRegistry, 16, func() time.Time { return end.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	request := BrainSourceInventoryRequest{
		TenantID: canonicalTenantID(), Principal: ACLPrincipal{TenantID: canonicalTenantID(), Kind: ACLPrincipalUser, ID: principalID, TeamIDs: []string{"organization"}},
		Temporal: TemporalQuery{StartUTC: start, EndUTC: end, Timezone: "UTC", Interpretation: TemporalExplicitRange, InterpretationNote: "native source episode runtime test"},
	}
	page, err := adapter.InventoryBrainSources(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	return page.Sources, page
}

func nativeSourceEpisodeBodies(t *testing.T, app *kanbanBoardApp, sources []BrainSourceMetadata) []string {
	t.Helper()
	adapter, err := NewSourceEpisodeShadowBrainAdapter(app.sourceEpisodes, app.sourceEpisodeRegistry, 16, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	bodies := make([]string, 0, len(sources))
	for _, source := range sources {
		read, err := adapter.ReadBrainSource(context.Background(), source.Evidence)
		if err != nil || !read.BodyAvailable || read.Status != RecallSourceFresh {
			t.Fatalf("read %+v: %+v err=%v", source.Evidence, read, err)
		}
		bodies = append(bodies, read.Body)
	}
	return bodies
}

func TestNativeSourceEpisodeRuntimePublishesConversationAndDriveWithoutPrivatePromotion(t *testing.T) {
	app, ledgerPath := newNativeSourceEpisodeRuntimeFixture(t)
	now := time.Now().UTC().Add(-time.Minute)
	ownerEmail := "aj@shareability.com"
	otherEmail := "tim@shareability.com"

	public, err := app.createScoutChatThreadRecord("source_episode_public", ownerEmail, "AJ", "Public", scoutChatVisibilityPublic, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	publicMessage := scoutChatMessageRecord{ID: "public_message", Kind: "message", Role: "scout", Text: "Public launch decision", CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano), AuthorName: "Scout"}
	if _, err := app.commitScoutChatThreadMessages(ownerEmail, public.ID, publicMessage); err != nil {
		t.Fatal(err)
	}

	private, err := app.createScoutChatThreadRecord("source_episode_private", ownerEmail, "AJ", "Scout", scoutChatVisibilityPrivate, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	privateMessage := scoutChatMessageRecord{ID: "private_message", Kind: "message", Role: "user", Text: "Private acquisition note", CreatedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: ownerEmail}
	if _, err := app.commitScoutChatThreadMessages(ownerEmail, private.ID, privateMessage); err != nil {
		t.Fatal(err)
	}

	driveEntry, _, err := app.memory.appendEntry(meetingMemoryKindFile, "source_episode_drive", "Drive research brief body", map[string]string{
		"name": "brief.txt", "origin": "files", "brainStatus": fileBrainStatusIngested, "uploaderEmail": ownerEmail,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.publishDriveFileSourceEpisode(driveEntry); err != nil {
		t.Fatal(err)
	}
	privateDrive, _, err := app.memory.appendEntry(meetingMemoryKindFile, "source_episode_private_drive", "Private attachment copied to Drive", map[string]string{
		"name": "private.txt", "origin": "files", "brainStatus": fileBrainStatusIngested,
		"uploaderEmail": ownerEmail, "sourceThreadId": private.ID, "sourceMessageId": privateMessage.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.publishDriveFileSourceEpisode(privateDrive); err != nil {
		t.Fatal(err)
	}

	ownerSources, ownerPage := nativeSourceEpisodeInventory(t, app, ownerEmail, now.Add(-time.Hour), now.Add(time.Hour))
	if len(ownerSources) != 4 || ownerPage.ExpectedSourceCount != 4 || ownerPage.SourceHighWater != 4 {
		t.Fatalf("owner inventory leaked counts or missed sources: %+v", ownerPage)
	}
	ownerBodies := strings.Join(nativeSourceEpisodeBodies(t, app, ownerSources), "\n")
	for _, expected := range []string{publicMessage.Text, privateMessage.Text, driveEntry.Text, privateDrive.Text} {
		if !strings.Contains(ownerBodies, expected) {
			t.Fatalf("owner body retrieval missed %q: %q", expected, ownerBodies)
		}
	}

	otherSources, otherPage := nativeSourceEpisodeInventory(t, app, otherEmail, now.Add(-time.Hour), now.Add(time.Hour))
	if len(otherSources) != 2 || otherPage.ExpectedSourceCount != 2 || otherPage.SourceHighWater != 2 {
		t.Fatalf("private source influenced outsider counts: %+v", otherPage)
	}
	otherBodies := strings.Join(nativeSourceEpisodeBodies(t, app, otherSources), "\n")
	if strings.Contains(otherBodies, privateMessage.Text) || strings.Contains(otherBodies, privateDrive.Text) {
		t.Fatal("private conversation or its Drive copy entered another user's retrieval")
	}
	adapterWithoutTeam, err := NewSourceEpisodeShadowBrainAdapter(app.sourceEpisodes, app.sourceEpisodeRegistry, 16, func() time.Time { return now.Add(2 * time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	withoutTeam, err := adapterWithoutTeam.InventoryBrainSources(context.Background(), BrainSourceInventoryRequest{
		TenantID: canonicalTenantID(), Principal: ACLPrincipal{TenantID: canonicalTenantID(), Kind: ACLPrincipalUser, ID: strideRuntimePrincipalForEmail(ownerEmail)},
		Temporal: TemporalQuery{StartUTC: now.Add(-time.Hour), EndUTC: now.Add(time.Hour), Timezone: "UTC", Interpretation: TemporalExplicitRange, InterpretationNote: "team authority count test"},
	}, "")
	if err != nil || withoutTeam.ExpectedSourceCount != 2 || withoutTeam.SourceHighWater != 2 {
		t.Fatalf("organization sources influenced counts without organization authority: %+v err=%v", withoutTeam, err)
	}

	privateEpisode, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), conversationSourceEpisodeID(private.ID, privateMessage.ID))
	if err != nil || !found {
		t.Fatalf("private episode unavailable: found=%v err=%v", found, err)
	}
	if privateEpisode.Scope.MemoryScope == SourceEpisodeMemoryCompany || privateEpisode.Scope.MemoryScope == SourceEpisodeMemoryProject || privateEpisode.Authority.Audience.Visibility != "private" {
		t.Fatalf("private episode promoted scope: %+v", privateEpisode)
	}
	privateDriveEpisode, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), driveSourceEpisodeID(privateDrive.ID))
	if err != nil || !found || privateDriveEpisode.Scope.MemoryScope == SourceEpisodeMemoryCompany || privateDriveEpisode.Authority.Audience.Visibility != "private" {
		t.Fatalf("private Drive copy promoted scope: %+v found=%v err=%v", privateDriveEpisode, found, err)
	}

	adapter, err := NewSourceEpisodeShadowBrainAdapter(app.sourceEpisodes, app.sourceEpisodeRegistry, 16, func() time.Time { return now.Add(2 * time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	planner, err := NewSourceEpisodePlanner(adapter, app.sourceEpisodes, app.sourceEpisodeRegistry, nil, BrainPromptLimits{
		MaxSourceChunkBytes: 128, MaxPromptBytes: 1024, MaxFoldInputs: 4, MaxFoldOutputBytes: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	temporal := TemporalQuery{StartUTC: now.Add(-time.Hour), EndUTC: now.Add(time.Hour), Timezone: "UTC", Interpretation: TemporalExplicitRange, InterpretationNote: "planner source episode test"}
	ownerPrincipal := ACLPrincipal{TenantID: canonicalTenantID(), Kind: ACLPrincipalUser, ID: strideRuntimePrincipalForEmail(ownerEmail), TeamIDs: []string{"organization"}}
	ownerResult, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: ownerPrincipal, Query: "decision research private", Temporal: temporal})
	if err != nil || len(ownerResult.Sources) != 4 || len(ownerResult.Snapshot.Sources) != 4 {
		t.Fatalf("planner did not preserve authorized SourceEpisodes: sources=%d snapshot=%d err=%v", len(ownerResult.Sources), len(ownerResult.Snapshot.Sources), err)
	}
	otherPrincipal := ACLPrincipal{TenantID: canonicalTenantID(), Kind: ACLPrincipalUser, ID: strideRuntimePrincipalForEmail(otherEmail), TeamIDs: []string{"organization"}}
	otherResult, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: otherPrincipal, Query: "decision research private", Temporal: temporal})
	if err != nil || len(otherResult.Sources) != 2 || len(otherResult.Snapshot.Sources) != 2 {
		t.Fatalf("planner private isolation failed: sources=%d snapshot=%d err=%v", len(otherResult.Sources), len(otherResult.Snapshot.Sources), err)
	}

	// Restart replay rebuilds only the body-free ledger projections. Replaying
	// the same native commit is idempotent and does not append a new revision.
	restarted, err := OpenFileSourceEpisodeLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	app.sourceEpisodes = restarted
	if err := app.publishDriveFileSourceEpisode(driveEntry); err != nil {
		t.Fatal(err)
	}
	latestDrive, found, err := restarted.LatestSourceEpisode(context.Background(), canonicalTenantID(), driveSourceEpisodeID(driveEntry.ID))
	if err != nil || !found || latestDrive.Header.Revision != 1 {
		t.Fatalf("restart/idempotent Drive replay changed revision: %+v found=%v err=%v", latestDrive, found, err)
	}
}

func TestNativeSourceEpisodeRuntimeCorrectionDeleteAndRevocationTombstones(t *testing.T) {
	app, _ := newNativeSourceEpisodeRuntimeFixture(t)
	now := time.Now().UTC().Add(-time.Minute)
	owner := &userAccount{Email: "aj@shareability.com", Name: "AJ"}

	private, err := app.createScoutChatThreadRecord("source_episode_mutation_private", owner.Email, owner.Name, "Scout", scoutChatVisibilityPrivate, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "mutation_message", Kind: "message", Role: "user", Text: "Initial private note", CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano), AuthorName: owner.Name, AuthorEmail: owner.Email}
	if _, err := app.commitScoutChatThreadMessages(owner.Email, private.ID, message); err != nil {
		t.Fatal(err)
	}
	corrected := "Corrected private note"
	if _, _, err := app.editScoutChatThreadMessage(context.Background(), owner, private.ID, message.ID, &corrected, nil); err != nil {
		t.Fatal(err)
	}
	current, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), conversationSourceEpisodeID(private.ID, message.ID))
	if err != nil || !found || current.Header.Revision != 2 || current.Source.ContentRevision != 2 {
		t.Fatalf("correction did not supersede exact revision: %+v found=%v err=%v", current, found, err)
	}
	if !sourceEpisodeLedgerHasTombstone(app.sourceEpisodes, SourceEpisodeTombstoneCorrection) {
		t.Fatal("correction did not leave a durable tombstone")
	}
	if _, err := app.deleteScoutChatThreadMessage(owner.Email, private.ID, message.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), conversationSourceEpisodeID(private.ID, message.ID)); found {
		t.Fatal("deleted private source remained active")
	}
	if !sourceEpisodeLedgerHasTombstone(app.sourceEpisodes, SourceEpisodeTombstoneRetraction) {
		t.Fatal("delete did not leave a durable tombstone")
	}
	privateDrive, _, err := app.memory.appendEntry(meetingMemoryKindFile, "source_episode_mutation_private_drive", "Private Drive source", map[string]string{
		"name": "private.txt", "sourceThreadId": private.ID, "sourceMessageId": message.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.publishDriveFileSourceEpisode(privateDrive); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setScoutChatThreadArchived(owner.Email, private.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), driveSourceEpisodeID(privateDrive.ID)); found {
		t.Fatal("private Drive copy survived source-conversation revocation")
	}
	if _, err := app.setScoutChatThreadArchived(owner.Email, private.ID, false); err != nil {
		t.Fatal(err)
	}
	restoredDrive, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), driveSourceEpisodeID(privateDrive.ID))
	if err != nil || !found || restoredDrive.Header.Revision != 2 || restoredDrive.Authority.Audience.Visibility != "private" {
		t.Fatalf("private Drive restore lost lineage or privacy: %+v found=%v err=%v", restoredDrive, found, err)
	}

	public, err := app.createScoutChatThreadRecord("source_episode_mutation_public", owner.Email, owner.Name, "Public", scoutChatVisibilityPublic, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	publicMessage := scoutChatMessageRecord{ID: "revoked_message", Kind: "message", Role: "scout", Text: "Revoke this public source", CreatedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano), AuthorName: "Scout"}
	if _, err := app.commitScoutChatThreadMessages(owner.Email, public.ID, publicMessage); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setScoutChatThreadArchived(owner.Email, public.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), conversationSourceEpisodeID(public.ID, publicMessage.ID)); found {
		t.Fatal("revoked public source remained active")
	}
	if !sourceEpisodeLedgerHasTombstone(app.sourceEpisodes, SourceEpisodeTombstoneACL) {
		t.Fatal("ACL revocation did not leave a durable tombstone")
	}

	driveEntry, _, err := app.memory.appendEntry(meetingMemoryKindFile, "source_episode_deleted_drive", "Delete this Drive body", map[string]string{"name": "delete.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.publishDriveFileSourceEpisode(driveEntry); err != nil {
		t.Fatal(err)
	}
	deleted, ok, err := app.memory.deleteEntryByID(driveEntry.ID)
	if err != nil || !ok {
		t.Fatalf("delete native Drive source: ok=%v err=%v", ok, err)
	}
	if err := app.tombstoneDriveFileSourceEpisode(deleted, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), driveSourceEpisodeID(driveEntry.ID)); found {
		t.Fatal("deleted Drive source remained active")
	}
	provider := &driveSourceEpisodeProvider{app: app}
	if _, err := provider.ReadExactSourceEpisodeBody(context.Background(), current.Source); !errors.Is(err, ErrSourceEpisodeBodyMissing) {
		// current.Source is a conversation ref, so the Drive reader must also
		// fail closed rather than dispatching across native source families.
		t.Fatalf("cross-family body read did not fail closed: %v", err)
	}
}

func TestNativeSourceEpisodeRuntimeRealtimeAndWorkHooksReachFullPlanner(t *testing.T) {
	app, ledgerPath := newNativeSourceEpisodeRuntimeFixture(t)
	now := time.Now().UTC()
	ownerEmail := "aj@shareability.com"
	otherEmail := "tim@shareability.com"

	organizationWork, appended, _, err := app.createOSArtifactWithIDAndMetadataAcknowledged(
		"source-episode-organization-work", "research", "Market decision", "Initial organization research", ownerEmail,
		map[string]string{"goalParentId": "project-market-decision", "visibility": "organization", "ownerEmail": ownerEmail},
	)
	if err != nil || !appended {
		t.Fatalf("create organization work: appended=%v err=%v", appended, err)
	}
	organizationWork, changed, err := app.updateOSArtifact(organizationWork.ID, "Market decision", "Revised organization research", ownerEmail)
	if err != nil || !changed {
		t.Fatalf("revise organization work: changed=%v err=%v", changed, err)
	}
	organizationEpisode, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), workArtifactSourceEpisodeID(organizationWork.ID))
	if err != nil || !found || organizationEpisode.Header.Revision != 2 || organizationEpisode.Source.ContentRevision != int64(artifactVersion(organizationWork)) {
		t.Fatalf("Work revision did not preserve native lineage: %+v found=%v err=%v", organizationEpisode, found, err)
	}
	if !sourceEpisodeLedgerHasTombstone(app.sourceEpisodes, SourceEpisodeTombstoneCorrection) {
		t.Fatal("Work revision did not tombstone the superseded envelope")
	}
	if err := app.publishCommittedWorkArtifactSourceEpisode(organizationWork); err != nil {
		t.Fatalf("idempotent Work retry: %v", err)
	}

	privateWork, appended, _, err := app.createOSArtifactWithIDAndMetadataAcknowledged(
		"source-episode-private-work", "design", "Private board draft", "Private board content", ownerEmail,
		map[string]string{"goalParentId": "project-private-board", "visibility": "private", "ownerEmail": ownerEmail},
	)
	if err != nil || !appended {
		t.Fatalf("create private work: appended=%v err=%v", appended, err)
	}

	const voiceSessionID = "source-episode-realtime-session"
	thread, _, err := app.ensurePrivateRealtimeVoiceConversation(ownerEmail, "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	sessionHash := "source-episode-authenticated-session"
	claim, err := app.claimPrivateRealtimeVoiceLease(ownerEmail, sessionHash, voiceSessionID, thread.ID, "source-episode-offer", privateRealtimeLeaseDigest("offer-sdp", "source-episode-offer"), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.finishPrivateRealtimeVoiceLease(ownerEmail, sessionHash, voiceSessionID, thread.ID, claim, true, "source-episode-answer", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	threadLock := app.scoutChatThreadLock(thread.ID)
	threadLock.Lock()
	thread, err = app.privateRealtimeVoiceConversation(ownerEmail, voiceSessionID, thread.ID)
	if err == nil {
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{
			ID: "source_episode_voice_turn", Kind: "message", Role: "user", Text: "Keep the launch private until Friday",
			CreatedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: ownerEmail,
		})
		thread.UpdatedAt = now.Add(2 * time.Second).Format(time.RFC3339Nano)
		err = app.saveScoutChatThread(thread)
	}
	threadLock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if replayed, stopErr := app.stopPrivateRealtimeVoiceLease(ownerEmail, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "source-episode-stop", now.Add(3*time.Second)); stopErr != nil || replayed {
		t.Fatalf("stop Realtime session: replayed=%v err=%v", replayed, stopErr)
	}
	voiceEpisode, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), realtimeVoiceSourceEpisodeID(thread.ID, privateRealtimeVoiceSessionDigest(voiceSessionID), claim.Generation))
	if err != nil || !found || voiceEpisode.Kind != SourceEpisodeRealtimeVoiceSession || voiceEpisode.PhaseProof.Phase != SourceEpisodePhasePostClose ||
		voiceEpisode.Authority.Audience.Visibility != "private" || voiceEpisode.Scope.MemoryScope == SourceEpisodeMemoryCompany {
		t.Fatalf("Realtime close did not publish a private post-close episode: %+v found=%v err=%v", voiceEpisode, found, err)
	}
	if replayed, stopErr := app.stopPrivateRealtimeVoiceLease(ownerEmail, sessionHash, voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "source-episode-stop", now.Add(4*time.Second)); stopErr != nil || !replayed {
		t.Fatalf("Realtime stop replay: replayed=%v err=%v", replayed, stopErr)
	}

	adapter, err := NewSourceEpisodeShadowBrainAdapter(app.sourceEpisodes, app.sourceEpisodeRegistry, 16, func() time.Time { return now.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	planner, err := NewSourceEpisodePlanner(adapter, app.sourceEpisodes, app.sourceEpisodeRegistry, nil, BrainPromptLimits{
		MaxSourceChunkBytes: 128, MaxPromptBytes: 1024, MaxFoldInputs: 4, MaxFoldOutputBytes: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	temporal := TemporalQuery{StartUTC: now.Add(-time.Hour), EndUTC: now.Add(time.Hour), Timezone: "UTC", Interpretation: TemporalExplicitRange, InterpretationNote: "Realtime and Work planner test"}
	owner := ACLPrincipal{TenantID: canonicalTenantID(), Kind: ACLPrincipalUser, ID: strideRuntimePrincipalForEmail(ownerEmail), TeamIDs: []string{"organization"}}
	ownerResult, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: owner, Query: "launch research board", Temporal: temporal})
	if err != nil || len(ownerResult.Sources) != 3 || len(ownerResult.Snapshot.Sources) != 3 {
		t.Fatalf("owner planner result: sources=%d snapshot=%d err=%v", len(ownerResult.Sources), len(ownerResult.Snapshot.Sources), err)
	}
	ownerBodies := make([]string, 0, len(ownerResult.Sources))
	for _, source := range ownerResult.Sources {
		ownerBodies = append(ownerBodies, source.Body)
	}
	joinedOwnerBodies := strings.Join(ownerBodies, "\n")
	for _, expected := range []string{"Revised organization research", "Private board content", "Keep the launch private until Friday"} {
		if !strings.Contains(joinedOwnerBodies, expected) {
			t.Fatalf("full planner omitted %q from native exact-revision bodies: %q", expected, joinedOwnerBodies)
		}
	}
	other := ACLPrincipal{TenantID: canonicalTenantID(), Kind: ACLPrincipalUser, ID: strideRuntimePrincipalForEmail(otherEmail), TeamIDs: []string{"organization"}}
	otherResult, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: other, Query: "launch research board", Temporal: temporal})
	if err != nil || len(otherResult.Sources) != 1 || !strings.Contains(otherResult.Sources[0].Body, "Revised organization research") {
		t.Fatalf("private Realtime/Work escaped through planner: sources=%+v err=%v", otherResult.Sources, err)
	}

	if _, _, deleted, deleteErr := app.deleteOSArtifactAndEmit(privateWork.ID); deleteErr != nil || !deleted {
		t.Fatalf("delete private Work: deleted=%v err=%v", deleted, deleteErr)
	}
	if _, active, _ := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), workArtifactSourceEpisodeID(privateWork.ID)); active {
		t.Fatal("deleted Work artifact remained active")
	}
	if _, archiveErr := app.setScoutChatThreadArchived(ownerEmail, thread.ID, true); archiveErr != nil {
		t.Fatal(archiveErr)
	}
	if _, active, _ := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), voiceEpisode.Header.ID); active {
		t.Fatal("revoked Realtime session remained active")
	}

	restarted, err := OpenFileSourceEpisodeLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	app.sourceEpisodes = restarted
	if err := app.publishCommittedWorkArtifactSourceEpisode(organizationWork); err != nil {
		t.Fatalf("restart Work retry: %v", err)
	}
	restartedWork, found, err := restarted.CurrentSourceEpisode(context.Background(), canonicalTenantID(), workArtifactSourceEpisodeID(organizationWork.ID))
	if err != nil || !found || restartedWork.Header.Revision != 2 {
		t.Fatalf("restart changed Work lineage: %+v found=%v err=%v", restartedWork, found, err)
	}
}

func TestNativeSourceEpisodeRuntimeRestartReconcilesDurableRealtimeAndWork(t *testing.T) {
	dir := t.TempDir()
	memory, err := newMeetingMemoryStore(filepath.Join(dir, "memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: memory}
	now := time.Now().UTC()
	work, appended, _, err := app.createOSArtifactWithIDAndMetadataAcknowledged(
		"restart-reconcile-work", "research", "Restart research", "Durable work before shadow recovery", "aj@shareability.com",
		map[string]string{"goalParentId": "restart-reconcile-project", "visibility": "organization"},
	)
	if err != nil || !appended {
		t.Fatalf("durable pre-ledger Work commit: appended=%v err=%v", appended, err)
	}
	const voiceSessionID = "restart-reconcile-voice"
	thread, _, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := app.claimPrivateRealtimeVoiceLease("aj@shareability.com", "restart-session", voiceSessionID, thread.ID, "restart-offer", privateRealtimeLeaseDigest("offer-sdp", "restart"), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.finishPrivateRealtimeVoiceLease("aj@shareability.com", "restart-session", voiceSessionID, thread.ID, claim, true, "restart-answer", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	lock := app.scoutChatThreadLock(thread.ID)
	lock.Lock()
	thread, err = app.privateRealtimeVoiceConversation("aj@shareability.com", voiceSessionID, thread.ID)
	if err == nil {
		thread.Messages = append(thread.Messages, scoutChatMessageRecord{ID: "restart-voice-turn", Kind: "message", Role: "user", Text: "Durable voice before shadow recovery", CreatedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano)})
		thread.UpdatedAt = now.Add(2 * time.Second).Format(time.RFC3339Nano)
		err = app.saveScoutChatThread(thread)
	}
	lock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.stopPrivateRealtimeVoiceLease("aj@shareability.com", "restart-session", voiceSessionID, thread.ID, claim.LeaseToken, claim.Generation, claim.TransportRevision, "restart-stop", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	ledgerPath := filepath.Join(dir, "source-episodes.jsonl")
	app.sourceEpisodes, err = OpenFileSourceEpisodeLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	app.sourceEpisodeRegistry, err = initializeSourceEpisodeNativeRuntime(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), workArtifactSourceEpisodeID(work.ID)); err != nil || !found {
		t.Fatalf("restart did not reconcile durable Work: found=%v err=%v", found, err)
	}
	voiceID := realtimeVoiceSourceEpisodeID(thread.ID, privateRealtimeVoiceSessionDigest(voiceSessionID), claim.Generation)
	if _, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), voiceID); err != nil || !found {
		t.Fatalf("restart did not reconcile durable Realtime close: found=%v err=%v", found, err)
	}
	restarted, err := OpenFileSourceEpisodeLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	app.sourceEpisodes = restarted
	if _, err := initializeSourceEpisodeNativeRuntime(app); err != nil {
		t.Fatal(err)
	}
	latest, found, err := restarted.LatestSourceEpisode(context.Background(), canonicalTenantID(), voiceID)
	if err != nil || !found || latest.Header.Revision != 1 {
		t.Fatalf("restart replay duplicated Realtime lineage: %+v found=%v err=%v", latest, found, err)
	}
}

func TestNativeSourceEpisodeRuntimeCanonicalPurgeAndProjectAudience(t *testing.T) {
	app, ledgerPath := newNativeSourceEpisodeRuntimeFixture(t)
	now := time.Now().UTC()
	owner := "aj@shareability.com"
	member := "sam@shareability.com"
	outsider := "tim@shareability.com"
	thread, err := app.createScoutChatThreadRecord("project-audience-thread", owner, "AJ", "Project", scoutChatVisibilityPublic, []string{member}, now)
	if err != nil {
		t.Fatal(err)
	}
	artifact, appended, _, err := app.createOSArtifactWithIDAndMetadataAcknowledged(
		"project-audience-work", "research", "Project evidence", "Exact project member evidence", owner,
		map[string]string{"goalParentId": "project-audience", "originSurface": "chat:" + thread.ID},
	)
	if err != nil || !appended {
		t.Fatalf("create project Work: appended=%v err=%v", appended, err)
	}
	episode, found, err := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), workArtifactSourceEpisodeID(artifact.ID))
	if err != nil || !found || episode.Authority.Audience.Visibility != "channel" || len(episode.Authority.Audience.Principals) != 2 {
		t.Fatalf("project Work widened its audience: %+v found=%v err=%v", episode, found, err)
	}
	provider := &workArtifactSourceEpisodeProvider{app: app}
	for _, email := range []string{owner, member} {
		if allowed, err := provider.AuthorizeSourceEpisodeMetadata(context.Background(), ACLPrincipal{TenantID: canonicalTenantID(), Kind: ACLPrincipalUser, ID: strideRuntimePrincipalForEmail(email)}, episode); err != nil || !allowed {
			t.Fatalf("project member %s denied: allowed=%v err=%v", email, allowed, err)
		}
	}
	if allowed, err := provider.AuthorizeSourceEpisodeMetadata(context.Background(), ACLPrincipal{TenantID: canonicalTenantID(), Kind: ACLPrincipalUser, ID: strideRuntimePrincipalForEmail(outsider), TeamIDs: []string{"organization"}}, episode); !errors.Is(err, ErrSourceEpisodeAuthorityDenied) || allowed {
		t.Fatalf("project outsider admitted: allowed=%v err=%v", allowed, err)
	}

	purge := &CanonicalSourceEpisodePurgeResolver{Canonical: fixedSourceEpisodePurgeResolver(3), Ledger: app.sourceEpisodes, Now: func() time.Time { return now.Add(time.Minute) }}
	if generation, err := purge.CurrentPurgeGeneration(context.Background(), canonicalTenantID()); err != nil || generation != 3 {
		t.Fatalf("canonical purge sync: generation=%d err=%v", generation, err)
	}
	if _, active, _ := app.sourceEpisodes.CurrentSourceEpisode(context.Background(), canonicalTenantID(), episode.Header.ID); active {
		t.Fatal("canonical purge left old-generation Work active")
	}
	restarted, err := OpenFileSourceEpisodeLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if generation, err := restarted.CurrentPurgeGeneration(context.Background(), canonicalTenantID()); err != nil || generation != 3 {
		t.Fatalf("restart lost canonical purge tombstone: generation=%d err=%v", generation, err)
	}
}

func sourceEpisodeLedgerHasTombstone(ledger *FileSourceEpisodeLedger, cause string) bool {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	for _, record := range ledger.records {
		if record.Tombstone != nil && record.Tombstone.Cause == cause {
			return true
		}
	}
	return false
}
