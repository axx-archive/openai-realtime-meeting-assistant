package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func seedCorrectableWorkstreamArtifact(t *testing.T, app *kanbanBoardApp, user *userAccount) (meetingMemoryEntry, scoutChatThreadRecord, scoutChatMessageRecord, scoutChatThreadRecord) {
	t.Helper()
	source, message, project := seedWorkstreamAffinitySource(t, app, user, "Northstar Signal")
	binding, found := app.resolveWorkstreamAffinityWithContext(context.Background(), user, source, message, message.Text, time.Now().UTC())
	if !found {
		t.Fatal("root affinity was not resolved")
	}
	encoded, err := encodeWorkstreamAffinity(binding)
	if err != nil {
		t.Fatal(err)
	}
	artifact, appended, err := app.createOSArtifactWithMetadata("research", "Northstar operating brief", "Current accepted Work.", user.Name, map[string]string{
		workstreamAffinityMetadataKey: encoded,
		"requestedBy":                 normalizeAccountEmail(user.Email), "createdBy": normalizeAccountEmail(user.Email),
		"originKind": agentThreadOriginPrivateThread, "originId": source.ID, "originSurface": "chat:" + source.ID, "visibility": "private",
		"sourceMessageId": message.ID, "sourceMessageDigest": binding.SourceMessageDigest, "sourceWindowDigest": binding.SourceWindowDigest,
		"projectWorkId": project.ID, "projectWorkTitle": project.Title, "status": "complete", "threadStatus": "complete",
	})
	if err != nil || !appended {
		t.Fatalf("artifact appended=%v err=%v", appended, err)
	}
	source, err = app.commitScoutChatThreadMessages(user.Email, source.ID, scoutChatMessageRecord{
		ID: "correctable-work-card", Kind: "message", Role: "scout", Text: "The operating brief is ready.",
		CreatedAt: time.Now().UTC().Add(time.Millisecond).Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: "correctable-run", ArtifactID: artifact.ID, ProjectID: project.ID, ProjectTitle: project.Title},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact, source, message, project
}

func TestWorkstreamAffinityCorrectionIsResultSideReplayableAndTeachesContinuity(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	artifact, source, sourceMessage, oldProject := seedCorrectableWorkstreamArtifact(t, app, user)
	newProject, err := app.createScoutChatThread(user.Email, user.Name, "River Compass", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(artifact.ID)
	if !found {
		t.Fatal("artifact header missing")
	}
	oldAffinity := strings.TrimSpace(artifact.Metadata[workstreamAffinityMetadataKey])
	updated, replayed, err := app.applyWorkstreamAffinityCorrection(context.Background(), user, artifact.ID, "project", newProject.ID,
		"workstream-correction-one", header, sha256Hex([]byte(oldAffinity)), sha256Hex(nil), time.Now().UTC())
	if err != nil || replayed {
		t.Fatalf("correction replayed=%v err=%v", replayed, err)
	}
	receipt, ok := decodeWorkstreamAffinityCorrection(updated.Metadata)
	if !ok || receipt.Revision != 1 || receipt.TargetKind != "project" || receipt.ProjectThreadID != newProject.ID || receipt.PreviousAffinityDigest == "" {
		t.Fatalf("correction receipt=%+v ok=%v", receipt, ok)
	}
	binding, ok := decodeWorkstreamAffinity(updated.Metadata)
	if !ok || binding.Basis != workstreamAffinityCorrectionBasis || binding.Confidence != 1 || binding.ProjectThreadID != newProject.ID ||
		binding.CorrectionReceiptDigest != receipt.Digest || updated.Metadata["projectWorkId"] != newProject.ID || !app.workstreamAffinityCurrent(context.Background(), updated) {
		t.Fatalf("corrected affinity=%+v ok=%v metadata=%v", binding, ok, updated.Metadata)
	}
	if updated.Metadata["projectWorkId"] == oldProject.ID {
		t.Fatal("old Project survived the result-side correction")
	}

	// A lost response replays the exact accepted operation even though its
	// preview expected the pre-correction metadata. Changed bytes under the same
	// operation id conflict instead of silently reclassifying Work.
	replayedArtifact, replayed, err := app.applyWorkstreamAffinityCorrection(context.Background(), user, artifact.ID, "project", newProject.ID,
		"workstream-correction-one", header, sha256Hex([]byte(oldAffinity)), sha256Hex(nil), time.Now().UTC().Add(time.Second))
	if err != nil || !replayed || replayedArtifact.Metadata[workstreamAffinityCorrectionMetadataKey] != updated.Metadata[workstreamAffinityCorrectionMetadataKey] {
		t.Fatalf("exact replay replayed=%v err=%v", replayed, err)
	}
	if _, _, err := app.applyWorkstreamAffinityCorrection(context.Background(), user, artifact.ID, "none", "",
		"workstream-correction-one", header, sha256Hex([]byte(oldAffinity)), sha256Hex(nil), time.Now().UTC()); !errors.Is(err, errWorkstreamAffinityConflict) {
		t.Fatalf("changed replay err=%v, want conflict", err)
	}

	currentAffinity := strings.TrimSpace(updated.Metadata[workstreamAffinityMetadataKey])
	currentCorrection := strings.TrimSpace(updated.Metadata[workstreamAffinityCorrectionMetadataKey])
	removed, replayed, err := app.applyWorkstreamAffinityCorrection(context.Background(), user, artifact.ID, "none", "",
		"workstream-correction-two", header, sha256Hex([]byte(currentAffinity)), sha256Hex([]byte(currentCorrection)), time.Now().UTC().Add(2*time.Second))
	if err != nil || replayed {
		t.Fatalf("remove correction replayed=%v err=%v", replayed, err)
	}
	removedReceipt, ok := decodeWorkstreamAffinityCorrection(removed.Metadata)
	history, historyOK := decodeWorkstreamAffinityCorrectionHistory(removed.Metadata)
	if !ok || !historyOK || removedReceipt.TargetKind != "none" || removedReceipt.Revision != 2 || len(history) != 2 ||
		strings.TrimSpace(removed.Metadata[workstreamAffinityMetadataKey]) != "" || strings.TrimSpace(removed.Metadata["projectWorkId"]) != "" ||
		!app.workstreamAffinityCorrectionCurrent(context.Background(), removed, removedReceipt) {
		t.Fatalf("removed receipt=%+v ok=%v history=%+v historyOK=%v metadata=%v", removedReceipt, ok, history, historyOK, removed.Metadata)
	}

	// The explicit result-side "No project" is a continuity barrier. The old
	// source title and an older accepted Work receipt cannot reattach the next
	// generic turn, and no composer authority is involved.
	followUp := scoutChatMessageRecord{ID: "after-none-follow-up", Kind: "message", Role: "user", Text: "Prepare the next operating update.",
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Add(3 * time.Second).Format(time.RFC3339Nano)}
	source, err = app.commitScoutChatThreadMessages(user.Email, source.ID, followUp)
	if err != nil {
		t.Fatal(err)
	}
	if guessed, found := app.resolveWorkstreamAffinityWithContext(context.Background(), user, source, source.Messages[len(source.Messages)-1], followUp.Text, time.Now().UTC()); found {
		t.Fatalf("No project correction was ignored; guessed %+v", guessed)
	}

	changed := "Research a different source after correction."
	if _, _, err := app.editScoutChatThreadMessage(context.Background(), user, source.ID, sourceMessage.ID, &changed, nil); err != nil {
		t.Fatal(err)
	}
	if app.workstreamAffinityCorrectionCurrent(context.Background(), removed, removedReceipt) {
		t.Fatal("source edit left the result-side correction current")
	}
}

func TestWorkstreamAffinityCorrectionIsAuthorOnlyAndCASFenced(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	other := accountStore().findUser("tim@shareability.com")
	if owner == nil || other == nil {
		t.Fatal("seed users missing")
	}
	artifact, _, _, _ := seedCorrectableWorkstreamArtifact(t, app, owner)
	target, err := app.createScoutChatThread(owner.Email, owner.Name, "River Compass", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	header, _ := app.memory.artifactAuthorizationHeaderByID(artifact.ID)
	raw := strings.TrimSpace(artifact.Metadata[workstreamAffinityMetadataKey])
	if _, _, err := app.applyWorkstreamAffinityCorrection(context.Background(), other, artifact.ID, "project", target.ID,
		"workstream-correction-other", header, sha256Hex([]byte(raw)), sha256Hex(nil), time.Now().UTC()); !errors.Is(err, errWorkstreamAffinityConflict) {
		t.Fatalf("non-author correction err=%v", err)
	}
	if _, _, err := app.applyWorkstreamAffinityCorrection(context.Background(), owner, artifact.ID, "project", target.ID,
		"workstream-correction-stale", header, sha256Hex([]byte("stale")), sha256Hex(nil), time.Now().UTC()); !errors.Is(err, errWorkstreamAffinityConflict) {
		t.Fatalf("stale correction err=%v", err)
	}
	current, found := app.memory.entryByKindAndID(meetingMemoryKindOSArtifact, artifact.ID)
	if !found || strings.TrimSpace(current.Metadata[workstreamAffinityCorrectionMetadataKey]) != "" || current.Metadata["projectWorkId"] == target.ID {
		t.Fatalf("rejected correction mutated artifact: %+v", current.Metadata)
	}
}

func TestWorkstreamAffinityCorrectionTokenBindsExactResultAndCurrentAuthority(t *testing.T) {
	snapshot := projectChatSnapshotFixture(t)
	key := StrideE10TenantAuthorityEnvelopeKey{ID: "workstream_correction_key", Version: 1, Secret: []byte(strings.Repeat("w", 32))}
	restore := InstallStrideE10TenantAuthorityEnvelopeRuntime(&strideE10TenantEnvelopeTestKeyring{current: key, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{key.ID: key}})
	defer restore()
	artifact := meetingMemoryEntry{ID: "artifact_workstream_token", Metadata: map[string]string{
		workstreamAffinityMetadataKey: strings.Repeat("a", 64),
	}}
	header := ArtifactAuthorizationHeader{ObjectID: artifact.ID, ContentRevision: 3, ContentDigest: strings.Repeat("b", 64)}
	target := projectChatCorrectionTarget{Kind: "project", ProjectID: "project_workstream_token", ProjectRevision: 4, ProjectDigest: strings.Repeat("c", 64), ProjectTitle: "Northstar"}
	encoded, err := mintWorkstreamAffinityCorrectionToken(context.Background(), snapshot, artifact, header, target)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveWorkstreamAffinityCorrectionToken(context.Background(), encoded, snapshot)
	if err != nil || resolved.ArtifactID != artifact.ID || resolved.ArtifactContentRevision != header.ContentRevision || resolved.Target.ProjectID != target.ProjectID {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	if _, err := resolveWorkstreamAffinityCorrectionToken(context.Background(), "x"+encoded[1:], snapshot); err == nil {
		t.Fatal("tampered Work correction token was accepted")
	}
	other := snapshot
	other.Generation++
	if _, err := resolveWorkstreamAffinityCorrectionToken(context.Background(), encoded, other); err == nil {
		t.Fatal("Work correction token crossed an authority generation")
	}
}
