package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func seedWorkstreamAffinitySource(t *testing.T, app *kanbanBoardApp, user *userAccount, projectTitle string) (scoutChatThreadRecord, scoutChatMessageRecord, scoutChatThreadRecord) {
	t.Helper()
	project, err := app.createScoutChatThread(user.Email, user.Name, projectTitle, scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	source, err := app.createScoutChatThread(user.Email, user.Name, "Private work request", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "workstream-affinity-source-message", Kind: "message", Role: "user",
		Text: "Research the " + projectTitle + " launch evidence", AuthorName: user.Name,
		AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	source, err = app.commitScoutChatThreadMessages(user.Email, source.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	return source, source.Messages[len(source.Messages)-1], project
}

func TestWorkstreamAffinityIsServerOwnedExactAndFailsClosed(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	const projectTitle = "Neon Orchard Launch"
	source, message, project := seedWorkstreamAffinitySource(t, app, user, projectTitle)

	binding, found := app.resolveWorkstreamAffinity(user, source, message, "Research the Neon Orchard Launch distribution plan", time.Now().UTC())
	if !found || binding.ProjectThreadID != project.ID || binding.ProjectTitle != projectTitle || binding.Basis != "exact_project_title" || binding.Confidence != .96 {
		t.Fatalf("binding=%+v found=%v", binding, found)
	}
	encoded, err := encodeWorkstreamAffinity(binding)
	if err != nil {
		t.Fatal(err)
	}
	artifact := meetingMemoryEntry{Metadata: map[string]string{
		workstreamAffinityMetadataKey: encoded,
		"requestedBy":                 normalizeAccountEmail(user.Email), "originId": source.ID, "sourceMessageId": message.ID,
		"sourceMessageDigest": binding.SourceMessageDigest, "sourceWindowDigest": binding.SourceWindowDigest,
		"projectWorkId": project.ID, "projectWorkTitle": projectTitle,
	}}
	if !app.workstreamAffinityCurrent(context.Background(), artifact) || !app.projectBoundArtifactCurrent(context.Background(), artifact) {
		t.Fatal("exact current affinity was not admitted")
	}

	if _, err := app.renameScoutChatThread(user.Email, project.ID, "Neon Orchard Archive"); err != nil {
		t.Fatal(err)
	}
	if app.workstreamAffinityCurrent(context.Background(), artifact) || app.projectBoundArtifactCurrent(context.Background(), artifact) {
		t.Fatal("renamed project left inferred workstream current")
	}
}

func TestWorkstreamAffinityFailsClosedWhenOriginatingMessageChanges(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	source, message, project := seedWorkstreamAffinitySource(t, app, user, "Silver Current Launch")
	binding, found := app.resolveWorkstreamAffinity(user, source, message, "Research the Silver Current Launch evidence", time.Now().UTC())
	if !found {
		t.Fatal("exact initial affinity was not resolved")
	}
	encoded, err := encodeWorkstreamAffinity(binding)
	if err != nil {
		t.Fatal(err)
	}
	artifact := meetingMemoryEntry{Metadata: map[string]string{
		workstreamAffinityMetadataKey: encoded,
		"requestedBy":                 normalizeAccountEmail(user.Email), "originId": source.ID, "sourceMessageId": message.ID,
		"sourceMessageDigest": binding.SourceMessageDigest, "sourceWindowDigest": binding.SourceWindowDigest,
		"projectWorkId": project.ID, "projectWorkTitle": project.Title,
	}}
	if !app.workstreamAffinityCurrent(context.Background(), artifact) {
		t.Fatal("exact initial affinity was not current")
	}
	changed := "Research a different launch instead"
	if _, _, err := app.editScoutChatThreadMessage(context.Background(), user, source.ID, message.ID, &changed, nil); err != nil {
		t.Fatal(err)
	}
	if app.workstreamAffinityCurrent(context.Background(), artifact) || app.projectBoundArtifactCurrent(context.Background(), artifact) {
		t.Fatal("edited source message left inferred workstream current")
	}
}

func TestWorkstreamAffinityAbstainsWhenProjectMatchIsMissingOrAmbiguous(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	const projectTitle = "Copper Kite Program"
	source, message, _ := seedWorkstreamAffinitySource(t, app, user, projectTitle)
	if _, found := app.resolveWorkstreamAffinity(user, source, message, "Analyze a different initiative", time.Now().UTC()); found {
		t.Fatal("missing project match was guessed")
	}
	if _, err := app.createScoutChatThread(user.Email, user.Name, projectTitle, scoutChatVisibilityPublic); err != nil {
		t.Fatal(err)
	}
	if _, found := app.resolveWorkstreamAffinity(user, source, message, "Build the Copper Kite Program launch plan", time.Now().UTC()); found {
		t.Fatal("ambiguous project match was guessed")
	}
}

func TestPublicWorkstreamAffinityBindsOnlyItsOwnExactChannel(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	project, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Atlas Relay", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "public-affinity-source", Kind: "message", Role: "user", Text: "Research the Atlas Relay launch",
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	project, err = app.commitScoutChatThreadMessages(user.Email, project.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	message = project.Messages[len(project.Messages)-1]
	binding, found := app.resolveWorkstreamAffinity(user, project, message, "Research the Atlas Relay launch", time.Now().UTC())
	if !found || binding.ProjectThreadID != project.ID || binding.SourceThreadID != project.ID {
		t.Fatalf("same-channel binding=%+v found=%v", binding, found)
	}

	other, err := app.createScoutChatThread("aj@shareability.com", "AJ", "General Planning", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	otherMessage := scoutChatMessageRecord{
		ID: "public-affinity-cross-audience-source", Kind: "message", Role: "user", Text: "Research the Atlas Relay launch",
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	other, err = app.commitScoutChatThreadMessages(user.Email, other.ID, otherMessage)
	if err != nil {
		t.Fatal(err)
	}
	otherBinding, matched := app.resolveWorkstreamAffinity(user, other, other.Messages[len(other.Messages)-1], "Research the Atlas Relay launch", time.Now().UTC())
	if !matched || otherBinding.ProjectThreadID != other.ID || otherBinding.SourceThreadID != other.ID || otherBinding.ProjectThreadID == project.ID || otherBinding.Basis != workstreamAffinitySourceBasis {
		t.Fatalf("cross-channel workstream=%+v matched=%v, want only its own exact channel and never Atlas Relay", otherBinding, matched)
	}
}

func TestPublicWorkstreamAffinityUsesItsChannelWithoutRepeatingTheProjectTitle(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	project, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Harbor Lantern", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "public-affinity-implicit-source", Kind: "message", Role: "user", Text: "Research the distribution evidence and prepare the launch brief",
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	project, err = app.commitScoutChatThreadMessages(user.Email, project.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	message = project.Messages[len(project.Messages)-1]
	binding, found := app.resolveWorkstreamAffinity(user, project, message, "Research the distribution evidence and prepare the launch brief", time.Now().UTC())
	if !found || binding.ProjectThreadID != project.ID || binding.SourceThreadID != project.ID || binding.Basis != workstreamAffinitySourceBasis || binding.Confidence != .99 {
		t.Fatalf("implicit same-channel binding=%+v found=%v", binding, found)
	}
	encoded, err := encodeWorkstreamAffinity(binding)
	if err != nil {
		t.Fatal(err)
	}
	artifact := meetingMemoryEntry{Metadata: map[string]string{
		workstreamAffinityMetadataKey: encoded,
		"requestedBy":                 normalizeAccountEmail(user.Email), "originId": project.ID, "sourceMessageId": message.ID,
		"sourceMessageDigest": binding.SourceMessageDigest, "sourceWindowDigest": binding.SourceWindowDigest,
		"projectWorkId": project.ID, "projectWorkTitle": project.Title,
	}}
	if !app.workstreamAffinityCurrent(context.Background(), artifact) {
		t.Fatal("same-channel affinity was not current")
	}
	private, err := app.createScoutChatThread(user.Email, user.Name, "Unrelated private request", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateMessage := scoutChatMessageRecord{
		ID: "private-affinity-no-guess", Kind: "message", Role: "user", Text: "Research the distribution evidence and prepare the launch brief",
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	private, err = app.commitScoutChatThreadMessages(user.Email, private.ID, privateMessage)
	if err != nil {
		t.Fatal(err)
	}
	if guessed, matched := app.resolveWorkstreamAffinity(user, private, private.Messages[len(private.Messages)-1], privateMessage.Text, time.Now().UTC()); matched {
		t.Fatalf("private work guessed the public Project without exact evidence: %+v", guessed)
	}
}

func TestPrivateWorkstreamAffinityContinuesOnlyFromCurrentAcceptedWork(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	source, firstMessage, project := seedWorkstreamAffinitySource(t, app, user, "Northstar Launch")
	rootBinding, found := app.resolveWorkstreamAffinity(user, source, firstMessage, "Research the Northstar Launch evidence", time.Now().UTC())
	if !found || rootBinding.Basis != workstreamAffinityExactBasis {
		t.Fatalf("root affinity=%+v found=%v", rootBinding, found)
	}
	rootEncoded, err := encodeWorkstreamAffinity(rootBinding)
	if err != nil {
		t.Fatal(err)
	}
	rootArtifact, appended, err := app.createOSArtifactWithMetadata("research", "Northstar evidence", "Current accepted evidence", user.Name, map[string]string{
		workstreamAffinityMetadataKey: rootEncoded,
		"requestedBy":                 normalizeAccountEmail(user.Email), "originKind": agentThreadOriginPrivateThread, "originId": source.ID,
		"sourceMessageId": firstMessage.ID, "sourceMessageDigest": rootBinding.SourceMessageDigest, "sourceWindowDigest": rootBinding.SourceWindowDigest,
		"projectWorkId": project.ID, "projectWorkTitle": project.Title,
	})
	if err != nil || !appended {
		t.Fatalf("create root artifact appended=%v err=%v", appended, err)
	}
	source, err = app.commitScoutChatThreadMessages(user.Email, source.ID,
		scoutChatMessageRecord{ID: "continuity-root-card", Kind: "message", Role: "scout", Text: "The evidence is ready.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Thread: &scoutChatThreadRef{ID: "root-run", ArtifactID: rootArtifact.ID, ProjectID: project.ID, ProjectTitle: project.Title}},
		scoutChatMessageRecord{ID: "continuity-follow-up", Kind: "message", Role: "user", Text: "Turn that into the distribution brief and next steps.", AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Add(time.Millisecond).Format(time.RFC3339Nano)},
	)
	if err != nil {
		t.Fatal(err)
	}
	followUp := source.Messages[len(source.Messages)-1]
	continued, found := app.resolveWorkstreamAffinity(user, source, followUp, followUp.Text, time.Now().UTC())
	if !found || continued.Basis != workstreamAffinityThreadBasis || continued.Confidence != .92 || continued.ProjectThreadID != project.ID || continued.SupportArtifactID != rootArtifact.ID || continued.SupportAffinityDigest != rootBinding.Digest {
		t.Fatalf("continued affinity=%+v found=%v", continued, found)
	}
	continuedEncoded, err := encodeWorkstreamAffinity(continued)
	if err != nil {
		t.Fatal(err)
	}
	continuedArtifact := meetingMemoryEntry{Metadata: map[string]string{
		workstreamAffinityMetadataKey: continuedEncoded,
		"requestedBy":                 normalizeAccountEmail(user.Email), "originId": source.ID, "sourceMessageId": followUp.ID,
		"sourceMessageDigest": continued.SourceMessageDigest, "sourceWindowDigest": continued.SourceWindowDigest,
		"projectWorkId": project.ID, "projectWorkTitle": project.Title,
	}}
	if !app.workstreamAffinityCurrent(context.Background(), continuedArtifact) {
		t.Fatal("current accepted Work did not carry private-thread continuity")
	}
	previousStarter := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })
	response, err := app.startConversationPrivateWork(context.Background(), user, source, followUp, conversationWorkDecision{
		Kind: conversationWorkWorkstream, Mode: "research", Objective: followUp.Text,
	}, "", proposalSourceChatRouter, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return app.commitScoutChatThreadMessages(user.Email, source.ID, messages...)
	})
	if err != nil {
		t.Fatal(err)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.Artifact.Metadata["projectWorkId"] != project.ID || launched.Artifact.Metadata["projectWorkTitle"] != project.Title {
		t.Fatalf("continued Work launch=%+v", response["agentThread"])
	}
	launchedBinding, present := decodeWorkstreamAffinity(launched.Artifact.Metadata)
	if !present || launchedBinding.Basis != workstreamAffinityThreadBasis || launchedBinding.SupportArtifactID != rootArtifact.ID || !app.workstreamAffinityCurrent(context.Background(), launched.Artifact) {
		t.Fatalf("launched continuity=%+v present=%v", launchedBinding, present)
	}
	changed := "Research an unrelated topic"
	if _, _, err := app.editScoutChatThreadMessage(context.Background(), user, source.ID, firstMessage.ID, &changed, nil); err != nil {
		t.Fatal(err)
	}
	if app.workstreamAffinityCurrent(context.Background(), continuedArtifact) {
		t.Fatal("stale supporting Work left private-thread continuity current")
	}
	if app.workstreamAffinityCurrent(context.Background(), launched.Artifact) {
		t.Fatal("stale supporting Work reached the launched artifact provider seam")
	}
}

func TestPrivateWorkstreamAffinityAbstainsAcrossCompetingCurrentWork(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	projectA, err := app.createScoutChatThread(user.Email, user.Name, "Cedar Signal", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	projectB, err := app.createScoutChatThread(user.Email, user.Name, "Marble Current", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	source, err := app.createScoutChatThread(user.Email, user.Name, "Mixed private work", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	appendSupport := func(messageID, text string, project scoutChatThreadRecord) {
		message := scoutChatMessageRecord{ID: messageID, Kind: "message", Role: "user", Text: text, AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		var commitErr error
		source, commitErr = app.commitScoutChatThreadMessages(user.Email, source.ID, message)
		if commitErr != nil {
			t.Fatal(commitErr)
		}
		message = source.Messages[len(source.Messages)-1]
		binding, matched := app.resolveWorkstreamAffinity(user, source, message, text, time.Now().UTC())
		if !matched || binding.ProjectThreadID != project.ID || binding.Basis != workstreamAffinityExactBasis {
			t.Fatalf("root %s binding=%+v matched=%v", project.Title, binding, matched)
		}
		encoded, encodeErr := encodeWorkstreamAffinity(binding)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		artifact, appended, createErr := app.createOSArtifactWithMetadata("research", text, "Current accepted work", user.Name, map[string]string{
			workstreamAffinityMetadataKey: encoded,
			"requestedBy":                 normalizeAccountEmail(user.Email), "originKind": agentThreadOriginPrivateThread, "originId": source.ID,
			"sourceMessageId": message.ID, "sourceMessageDigest": binding.SourceMessageDigest, "sourceWindowDigest": binding.SourceWindowDigest,
			"projectWorkId": project.ID, "projectWorkTitle": project.Title,
		})
		if createErr != nil || !appended {
			t.Fatalf("create %s support appended=%v err=%v", project.Title, appended, createErr)
		}
		source, commitErr = app.commitScoutChatThreadMessages(user.Email, source.ID, scoutChatMessageRecord{ID: messageID + "-card", Kind: "message", Role: "scout", Text: "Work ready.", CreatedAt: time.Now().UTC().Add(time.Millisecond).Format(time.RFC3339Nano), Thread: &scoutChatThreadRef{ID: messageID + "-run", ArtifactID: artifact.ID, ProjectID: project.ID, ProjectTitle: project.Title}})
		if commitErr != nil {
			t.Fatal(commitErr)
		}
	}
	appendSupport("cedar-source", "Research the Cedar Signal launch", projectA)
	appendSupport("marble-source", "Research the Marble Current launch", projectB)
	generic := scoutChatMessageRecord{ID: "mixed-generic-follow-up", Kind: "message", Role: "user", Text: "Now prepare the next steps.", AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Add(2 * time.Millisecond).Format(time.RFC3339Nano)}
	source, err = app.commitScoutChatThreadMessages(user.Email, source.ID, generic)
	if err != nil {
		t.Fatal(err)
	}
	if guessed, matched := app.resolveWorkstreamAffinity(user, source, source.Messages[len(source.Messages)-1], generic.Text, time.Now().UTC()); matched {
		t.Fatalf("competing current Work affinities were guessed as %+v", guessed)
	}
}

func TestMeetingConversationAffinityUsesExactRecordAndNoBoardAuthority(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	started := time.Now().UTC().Add(-20 * time.Minute)
	meetingID := "meeting-workstream-affinity"
	app.meetings.startMeeting(officeRoomID, meetingID, started, []string{user.Name})
	app.meetings.endMeeting(meetingID, started.Add(15*time.Minute), meetingEndedReasonIdle, "")
	segment, _, err := app.memory.appendAttributedTranscriptWithMetadata(
		"meeting-workstream-affinity-segment", "meeting-workstream-affinity-item", user.Name, "high",
		"Research the Aurora Signal launch evidence.", map[string]string{"meetingId": meetingID, "visibility": "organization"},
	)
	if err != nil {
		t.Fatal(err)
	}
	projection, found := app.meetingRecordProjectionForPrincipal(context.Background(), recallPrincipalForUser(user), meetingID)
	if !found {
		t.Fatal("authorized Meeting Record projection is unavailable")
	}
	meetingThread, _, err := app.ensureMeetingRecordConversation(user, projection)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "meeting-affinity-source-message", Kind: "message", Role: "user", Text: "Research the Aurora Signal launch evidence",
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	meetingThread, err = app.commitScoutChatThreadMessages(user.Email, meetingThread.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	project, err := app.createScoutChatThread(user.Email, user.Name, "Aurora Signal", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	binding, matched := app.resolveWorkstreamAffinity(user, meetingThread, meetingThread.Messages[len(meetingThread.Messages)-1], "Research the Aurora Signal launch evidence", time.Now().UTC())
	if !matched || binding.ProjectThreadID != project.ID || binding.SourceThreadID != meetingThread.ID {
		t.Fatalf("Meeting affinity=%+v matched=%v", binding, matched)
	}
	previousStarter := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })
	response, err := app.startConversationPrivateWork(context.Background(), user, meetingThread, message, conversationWorkDecision{
		Kind: conversationWorkWorkstream, Mode: "research", Objective: "Research the Aurora Signal launch evidence",
	}, "", proposalSourceChatRouter, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return app.commitScoutChatThreadMessages(user.Email, meetingThread.ID, messages...)
	})
	if err != nil {
		t.Fatal(err)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.Artifact.Metadata["projectWorkId"] != project.ID {
		t.Fatalf("Meeting work launch=%+v", response["agentThread"])
	}
	if currentBinding, present := decodeWorkstreamAffinity(launched.Artifact.Metadata); !present || currentBinding.SourceThreadID != meetingThread.ID || currentBinding.ProjectThreadID != project.ID {
		t.Fatalf("Meeting work affinity=%+v present=%v", currentBinding, present)
	}
	if !app.workstreamAffinityCurrent(context.Background(), launched.Artifact) {
		t.Fatal("exact Meeting Record affinity was not current")
	}
	meetingThread, ok = response["thread"].(scoutChatThreadRecord)
	if !ok {
		t.Fatalf("Meeting work response thread=%T", response["thread"])
	}
	followUp := scoutChatMessageRecord{
		ID: "meeting-affinity-continuation", Kind: "message", Role: "user", Text: "Turn that into the launch brief and next actions.",
		AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Add(time.Millisecond).Format(time.RFC3339Nano),
	}
	meetingThread, err = app.commitScoutChatThreadMessages(user.Email, meetingThread.ID, followUp)
	if err != nil {
		t.Fatal(err)
	}
	followUp = meetingThread.Messages[len(meetingThread.Messages)-1]
	continued, matched := app.resolveWorkstreamAffinity(user, meetingThread, followUp, followUp.Text, time.Now().UTC())
	if !matched || continued.Basis != workstreamAffinityThreadBasis || continued.ProjectThreadID != project.ID ||
		continued.SupportArtifactID != launched.Artifact.ID || continued.SupportAffinityDigest == "" ||
		continued.SourceMeetingID != meetingID || continued.SourceMeetingRevision != projection.index.RecordRevision {
		t.Fatalf("Meeting continuity=%+v matched=%v", continued, matched)
	}
	continuedResponse, err := app.startConversationPrivateWork(context.Background(), user, meetingThread, followUp, conversationWorkDecision{
		Kind: conversationWorkWorkstream, Mode: "research", Objective: followUp.Text,
	}, "", proposalSourceChatRouter, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return app.commitScoutChatThreadMessages(user.Email, meetingThread.ID, messages...)
	})
	if err != nil {
		t.Fatal(err)
	}
	continuedLaunch, ok := continuedResponse["agentThread"].(scoutAgentThread)
	if !ok || continuedLaunch.Artifact.Metadata["projectWorkId"] != project.ID || !app.workstreamAffinityCurrent(context.Background(), continuedLaunch.Artifact) {
		t.Fatalf("Meeting continuity launch=%+v", continuedResponse["agentThread"])
	}
	if _, deleted, err := app.memory.deleteEntryByID(segment.ID); err != nil || !deleted {
		t.Fatalf("withdraw Meeting source deleted=%v err=%v", deleted, err)
	}
	if app.workstreamAffinityCurrent(context.Background(), launched.Artifact) || app.projectBoundArtifactCurrent(context.Background(), launched.Artifact) ||
		app.workstreamAffinityCurrent(context.Background(), continuedLaunch.Artifact) || app.projectBoundArtifactCurrent(context.Background(), continuedLaunch.Artifact) {
		t.Fatal("withdrawn Meeting source left inferred workstream current")
	}
	providerAdmitted := false
	if err := app.withCurrentAgentThreadSource(continuedLaunch, func() error {
		providerAdmitted = true
		return nil
	}); err == nil || providerAdmitted {
		t.Fatalf("withdrawn Meeting source reached provider admission: err=%v admitted=%v", err, providerAdmitted)
	}
}

type meetingClaimAffinityFixture struct {
	meetingID string
	segment   meetingMemoryEntry
	project   scoutChatThreadRecord
	card      kanbanCard
	support   meetingMemoryEntry
	thread    scoutChatThreadRecord
	message   scoutChatMessageRecord
}

func seedMeetingClaimAffinityFixture(t *testing.T, app *kanbanBoardApp, user *userAccount, suffix, projectTitle string) meetingClaimAffinityFixture {
	t.Helper()
	started := time.Now().UTC().Add(-25 * time.Minute)
	meetingID := "meeting-claim-affinity-" + suffix
	app.meetings.startMeeting(officeRoomID, meetingID, started, []string{user.Name})
	app.meetings.endMeeting(meetingID, started.Add(20*time.Minute), meetingEndedReasonIdle, "")
	segment, _, err := app.memory.appendAttributedTranscriptWithMetadata(
		"meeting-claim-segment-"+suffix, "meeting-claim-item-"+suffix, user.Name, "high",
		"I will prepare the launch evidence and operating brief.", map[string]string{"meetingId": meetingID, "visibility": "organization"},
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := meetingDigestPayload{
		MeetingID: meetingID, Title: "Launch operating review", Day: started.Format("2006-01-02"),
		ActionItems: []meetingDigestAction{{A: "Prepare the launch evidence and operating brief.", Owner: user.Name, Status: "open", Anchor: segment.ID, Importance: 5}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	segments := meetingRecordSegments(app.memory.snapshotForMeeting(meetingID, 0), meetingID)
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId": meetingID, "visibility": "organization", digestCoverageMetadataKey: coverageLabelFull,
		digestSpanEndMetadataKey:                      started.Add(20 * time.Minute).Format(time.RFC3339),
		meetingRecordDigestSourceRevisionsMetadataKey: meetingRecordDigestSourceRevisionMetadata(payload, segments),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := app.createScoutChatThread(user.Email, user.Name, projectTitle, scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	cardResult, _, err := app.applyRetiredMeetingBoardToolCallArgs("create_ticket", map[string]any{"title": "Legacy launch follow-up " + suffix})
	if err != nil {
		t.Fatal(err)
	}
	card, ok := cardResult["card"].(kanbanCard)
	if !ok {
		t.Fatalf("legacy card=%#v", cardResult["card"])
	}
	support, appended, err := app.createOSArtifactWithMetadata("research", "Accepted launch Work "+suffix, "Current accepted Work evidence.", user.Name, map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete", "boardCardId": card.ID,
		"originKind": agentThreadOriginChannel, "originId": project.ID, "originSurface": "chat:" + project.ID,
		"requestedBy": normalizeAccountEmail(user.Email), "createdBy": normalizeAccountEmail(user.Email), "visibility": "organization",
	})
	if err != nil || !appended {
		t.Fatalf("support appended=%v err=%v", appended, err)
	}
	claimLinks, err := json.Marshal([]meetingBoardClaimCardLink{{SegmentID: segment.ID, CardID: card.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.memory.appendBoardUpdate("meeting-claim-link-"+suffix, "Historical exact claim link.", map[string]string{
		"meetingId": meetingID, "cardIds": card.ID, "claimCardLinks": string(claimLinks), "visibility": "organization",
	}); err != nil {
		t.Fatal(err)
	}
	projection, found := app.meetingRecordProjectionForPrincipal(context.Background(), recallPrincipalForUser(user), meetingID)
	if !found {
		t.Fatal("Meeting Record projection is unavailable")
	}
	thread, _, err := app.ensureMeetingRecordConversation(user, projection)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "meeting-claim-work-request-" + suffix, Kind: "message", Role: "user",
		Text: "Turn that follow-up into the operating brief and action plan.", AuthorName: user.Name,
		AuthorEmail: normalizeAccountEmail(user.Email), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	return meetingClaimAffinityFixture{meetingID: meetingID, segment: segment, project: project, card: card, support: support, thread: thread, message: thread.Messages[len(thread.Messages)-1]}
}

func TestMeetingClaimSeedsFirstExactWorkstreamWithoutBoardAuthority(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	fixture := seedMeetingClaimAffinityFixture(t, app, user, "first", "Signal Lantern")
	binding, found := app.resolveWorkstreamAffinityWithContext(context.Background(), user, fixture.thread, fixture.message, fixture.message.Text, time.Now().UTC())
	if !found || binding.Basis != workstreamAffinityMeetingClaimBasis || binding.Confidence != .94 ||
		binding.ProjectThreadID != fixture.project.ID || binding.SourceClaimSegmentID != fixture.segment.ID ||
		binding.SupportArtifactID != fixture.support.ID || !isHexDigest(binding.SupportArtifactHeaderDigest) {
		t.Fatalf("Meeting claim affinity=%+v found=%v", binding, found)
	}
	encoded, err := encodeWorkstreamAffinity(binding)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, fixture.card.ID) {
		t.Fatalf("successor affinity retained retired Board authority: %s", encoded)
	}
	artifact := meetingMemoryEntry{Metadata: map[string]string{
		workstreamAffinityMetadataKey: encoded,
		"requestedBy":                 normalizeAccountEmail(user.Email), "originId": fixture.thread.ID,
		"sourceMessageId": fixture.message.ID, "sourceMessageDigest": binding.SourceMessageDigest, "sourceWindowDigest": binding.SourceWindowDigest,
		"projectWorkId": fixture.project.ID, "projectWorkTitle": fixture.project.Title,
	}}
	if !app.workstreamAffinityCurrent(context.Background(), artifact) {
		t.Fatal("exact Meeting claim affinity was not current")
	}
	previousStarter := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })
	response, err := app.startConversationPrivateWork(context.Background(), user, fixture.thread, fixture.message, conversationWorkDecision{
		Kind: conversationWorkWorkstream, Mode: "research", Objective: fixture.message.Text,
	}, "", proposalSourceChatRouter, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return app.commitScoutChatThreadMessages(user.Email, fixture.thread.ID, messages...)
	})
	if err != nil {
		t.Fatal(err)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.Artifact.Metadata["projectWorkId"] != fixture.project.ID {
		t.Fatalf("Meeting claim Work launch=%+v", response["agentThread"])
	}
	launchedBinding, present := decodeWorkstreamAffinity(launched.Artifact.Metadata)
	if !present || launchedBinding.Basis != workstreamAffinityMeetingClaimBasis || launchedBinding.SourceClaimSegmentID != fixture.segment.ID ||
		launchedBinding.SupportArtifactID != fixture.support.ID || !app.workstreamAffinityCurrent(context.Background(), launched.Artifact) {
		t.Fatalf("launched Meeting claim affinity=%+v present=%v", launchedBinding, present)
	}

	// The card was a one-time migration locator. Once the successor receipt is
	// minted, deleting every legacy card cannot remove or widen its authority.
	app.mu.Lock()
	app.cards = nil
	app.mu.Unlock()
	if !app.workstreamAffinityCurrent(context.Background(), artifact) {
		t.Fatal("retired Board state remained an authority dependency")
	}
	if !app.workstreamAffinityCurrent(context.Background(), launched.Artifact) {
		t.Fatal("launched Work retained retired Board authority")
	}

	header, ok := app.memory.artifactAuthorizationHeaderByID(fixture.support.ID)
	if !ok {
		t.Fatal("support header missing")
	}
	if _, changed, err := app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, fixture.support.ID, "", "Changed support evidence.", user.Name, nil); err != nil || !changed {
		t.Fatalf("support edit changed=%v err=%v", changed, err)
	}
	if app.workstreamAffinityCurrent(context.Background(), artifact) {
		t.Fatal("changed supporting Work left Meeting affinity current")
	}
	if app.workstreamAffinityCurrent(context.Background(), launched.Artifact) {
		t.Fatal("changed supporting Work reached the launched provider seam")
	}
}

func TestMeetingClaimWorkstreamAbstainsAcrossProjects(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	fixture := seedMeetingClaimAffinityFixture(t, app, user, "ambiguous", "Signal Lantern")
	otherProject, err := app.createScoutChatThread(user.Email, user.Name, "River Compass", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	otherResult, _, err := app.applyRetiredMeetingBoardToolCallArgs("create_ticket", map[string]any{"title": "Second legacy follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	otherCard := otherResult["card"].(kanbanCard)
	if _, appended, err := app.createOSArtifactWithMetadata("research", "Second accepted Work", "Second current accepted Work.", user.Name, map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete", "boardCardId": otherCard.ID,
		"originKind": agentThreadOriginChannel, "originId": otherProject.ID, "originSurface": "chat:" + otherProject.ID,
		"requestedBy": normalizeAccountEmail(user.Email), "createdBy": normalizeAccountEmail(user.Email), "visibility": "organization",
	}); err != nil || !appended {
		t.Fatalf("second support appended=%v err=%v", appended, err)
	}
	links, _ := json.Marshal([]meetingBoardClaimCardLink{{SegmentID: fixture.segment.ID, CardID: otherCard.ID}})
	if _, _, err := app.memory.appendBoardUpdate("meeting-claim-link-second", "Second historical exact claim link.", map[string]string{
		"meetingId": fixture.meetingID, "cardIds": otherCard.ID, "claimCardLinks": string(links), "visibility": "organization",
	}); err != nil {
		t.Fatal(err)
	}
	if guessed, found := app.resolveWorkstreamAffinityWithContext(context.Background(), user, fixture.thread, fixture.message, fixture.message.Text, time.Now().UTC()); found {
		t.Fatalf("competing Meeting claim Projects were guessed as %+v", guessed)
	}
}

func TestPrivateWorkLaunchCarriesInferredAffinityWithoutComposerProject(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	const projectTitle = "Signal Garden Initiative"
	source, message, project := seedWorkstreamAffinitySource(t, app, user, projectTitle)
	if message.Project != nil {
		t.Fatal("fixture unexpectedly used composer-selected Project authority")
	}
	previousStarter := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })

	response, err := app.startConversationPrivateWork(context.Background(), user, source, message, conversationWorkDecision{
		Kind: conversationWorkWorkstream, Mode: "research", Objective: "Research the Signal Garden Initiative launch evidence",
	}, "", proposalSourceChatRouter, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return app.commitScoutChatThreadMessages(user.Email, source.ID, messages...)
	})
	if err != nil {
		t.Fatal(err)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.Artifact.Metadata["projectWorkId"] != project.ID || launched.Artifact.Metadata["projectWorkTitle"] != projectTitle {
		t.Fatalf("launched=%+v", response["agentThread"])
	}
	if _, present := decodeWorkstreamAffinity(launched.Artifact.Metadata); !present {
		t.Fatal("launched private work omitted the server-owned affinity receipt")
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Thread == nil || answer.Thread.ProjectID != project.ID || answer.Thread.ProjectTitle != projectTitle {
		t.Fatalf("work card=%+v", response["answer"])
	}
}
