package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type surfaceRecordingArtifactAuthorizer struct {
	mu    sync.Mutex
	allow func(ACLAction, meetingMemoryEntry) bool
	calls []struct {
		action ACLAction
		id     string
	}
}

func (authorizer *surfaceRecordingArtifactAuthorizer) AuthorizeArtifactHeader(_ context.Context, _ *userAccount, action ACLAction, artifact ArtifactAuthorizationHeader) bool {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	authorizer.calls = append(authorizer.calls, struct {
		action ACLAction
		id     string
	}{action: action, id: artifact.ObjectID})
	entry := meetingMemoryEntry{ID: artifact.ObjectID, Metadata: map[string]string{"visibility": artifact.Visibility}}
	return authorizer.allow == nil || authorizer.allow(action, entry)
}

func (authorizer *surfaceRecordingArtifactAuthorizer) saw(action ACLAction, id string) bool {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	for _, call := range authorizer.calls {
		if call.action == action && call.id == id {
			return true
		}
	}
	return false
}

func installRecordingArtifactAuthorizer(t *testing.T, authorizer ObjectAuthorizer) {
	t.Helper()
	previous := artifactObjectAuthorizer
	artifactObjectAuthorizer = authorizer
	t.Cleanup(func() { artifactObjectAuthorizer = previous })
}

func TestArtifactOpenSurveyAndActionsDenyOpaqueWithExactAction(t *testing.T) {
	cookies, org, _ := setupArtifactAuthorizationSlice(t)
	originalProbe := artifactBodyReadProbe
	defer func() { artifactBodyReadProbe = originalProbe }()
	authorizer := &surfaceRecordingArtifactAuthorizer{allow: func(ACLAction, meetingMemoryEntry) bool { return false }}
	installRecordingArtifactAuthorizer(t, authorizer)

	open := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/open", fmt.Sprintf(`{"id":%q}`, org.ID), cookies, artifactOpenHandler)
	if open.Code != http.StatusNotFound || !authorizer.saw(ACLReadContent, org.ID) {
		t.Fatalf("open status=%d calls=%+v", open.Code, authorizer.calls)
	}
	surveyAuthorizer := &surfaceRecordingArtifactAuthorizer{allow: func(action ACLAction, _ meetingMemoryEntry) bool { return action != ACLWrite }}
	artifactObjectAuthorizer = surveyAuthorizer
	surveyBodyReads := 0
	artifactBodyReadProbe = func(string) { surveyBodyReads++ }
	survey := artifactAuthorizationRequest(t, http.MethodPost, "/signals/survey", fmt.Sprintf(`{"artifactId":%q,"verdict":"landed"}`, org.ID), cookies, signalSurveyHandler)
	if survey.Code != http.StatusNotFound || !surveyAuthorizer.saw(ACLReadContent, org.ID) || !surveyAuthorizer.saw(ACLWrite, org.ID) || surveyBodyReads != 0 || strings.Contains(survey.Body.String(), "stored") {
		t.Fatalf("survey status=%d body=%s", survey.Code, survey.Body.String())
	}

	for action, expected := range map[string][]ACLAction{
		"approve": {ACLReadMetadata, ACLApprove, ACLWrite}, "reject": {ACLReadMetadata, ACLApprove, ACLWrite},
		"resume": {ACLReadContent, ACLExecute, ACLWrite}, "rerun": {ACLReadContent, ACLExecute}, "unknown": {ACLWrite},
	} {
		deny := expected[len(expected)-1]
		actionAuthorizer := &surfaceRecordingArtifactAuthorizer{allow: func(candidate ACLAction, _ meetingMemoryEntry) bool { return candidate != deny }}
		artifactObjectAuthorizer = actionAuthorizer
		bodyReads := 0
		artifactBodyReadProbe = func(string) { bodyReads++ }
		response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/action", fmt.Sprintf(`{"id":%q,"action":%q}`, org.ID, action), cookies, artifactRunnerActionHandler)
		for _, want := range expected {
			if !actionAuthorizer.saw(want, org.ID) {
				t.Fatalf("action %s missed %s: %+v", action, want, actionAuthorizer.calls)
			}
		}
		if response.Code != http.StatusNotFound || bodyReads != 0 || strings.Contains(response.Body.String(), org.Text) {
			t.Fatalf("action %s status=%d body=%s bodyReads=%d calls=%+v", action, response.Code, response.Body.String(), bodyReads, actionAuthorizer.calls)
		}
	}
}

func TestDealRoomRequestRequiresReadAndShareForEveryPackageArtifact(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	packageID, binderID := seedPackageWithBinder(t, "# Confidential binder")
	gallery := attachGalleryArtifact(t, packageID, "Confidential gallery", "private gallery body", map[string]string{"type": "html_deck", "status": "approved"})
	authorizer := &surfaceRecordingArtifactAuthorizer{allow: func(action ACLAction, artifact meetingMemoryEntry) bool {
		return !(artifact.ID == gallery.ID && action == ACLShare)
	}}
	installRecordingArtifactAuthorizer(t, authorizer)
	bodyReads := 0
	previousProbe := artifactBodyReadProbe
	artifactBodyReadProbe = func(string) { bodyReads++ }
	defer func() { artifactBodyReadProbe = previousProbe }()
	response := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	if response.Code != http.StatusNotFound || bodyReads != 0 || strings.Contains(response.Body.String(), binderID) || strings.Contains(response.Body.String(), gallery.ID) {
		t.Fatalf("deal request status=%d body=%s", response.Code, response.Body.String())
	}
	for _, id := range []string{binderID, gallery.ID} {
		if !authorizer.saw(ACLReadContent, id) || !authorizer.saw(ACLShare, id) {
			t.Fatalf("artifact %s was not read+share authorized: %+v", id, authorizer.calls)
		}
	}
	if rooms := kanbanApp.dealRoomsSnapshot(); len(rooms) != 0 {
		t.Fatalf("denied deal room persisted: %+v", rooms)
	}
}

func TestPrivateArtifactIsFilteredFromFilesAndDeniedSaveAndFollowUp(t *testing.T) {
	ownerCookies, _, private := setupArtifactAuthorizationSlice(t)
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(private.ID, map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete",
		"savedToFiles": "true", "title": "PRIVATE FILE CANARY",
	}); err != nil {
		t.Fatal(err)
	}
	teammateCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	teammateList := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/files", "", teammateCookies, assistantFilesHandler)
	if teammateList.Code != http.StatusOK || strings.Contains(teammateList.Body.String(), private.ID) || strings.Contains(teammateList.Body.String(), "PRIVATE FILE CANARY") || strings.Contains(teammateList.Body.String(), private.Text) {
		t.Fatalf("private deliverable leaked in Files: status=%d body=%s", teammateList.Code, teammateList.Body.String())
	}
	ownerList := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/files", "", ownerCookies, assistantFilesHandler)
	if ownerList.Code != http.StatusOK || !strings.Contains(ownerList.Body.String(), private.ID) {
		t.Fatalf("owner Files list omitted private deliverable: %s", ownerList.Body.String())
	}

	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(private.ID, map[string]string{"savedToFiles": "false"}); err != nil {
		t.Fatal(err)
	}
	save := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/files/save", fmt.Sprintf(`{"artifactId":%q}`, private.ID), teammateCookies, assistantFileSaveHandler)
	if save.Code != http.StatusNotFound || strings.Contains(save.Body.String(), private.ID) {
		t.Fatalf("private save denial=%d body=%s", save.Code, save.Body.String())
	}
	afterSave, _ := kanbanApp.osArtifactByID(private.ID)
	if strings.EqualFold(afterSave.Metadata["savedToFiles"], "true") {
		t.Fatal("denied private save mutated artifact")
	}

	bodyReads := 0
	previousProbe := artifactBodyReadProbe
	artifactBodyReadProbe = func(string) { bodyReads++ }
	defer func() { artifactBodyReadProbe = previousProbe }()
	follow := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/threads/follow-up", fmt.Sprintf(`{"artifactId":%q,"text":"leak it"}`, private.ID), teammateCookies, assistantThreadFollowUpHandler)
	if follow.Code != http.StatusNotFound || bodyReads != 0 || strings.Contains(follow.Body.String(), private.ID) || strings.Contains(follow.Body.String(), private.Text) {
		t.Fatalf("private follow-up denial=%d body=%s reads=%d", follow.Code, follow.Body.String(), bodyReads)
	}
}

func TestEnforcedArtifactSurfaceRegistryMatchesHandlerContracts(t *testing.T) {
	want := map[string][]ACLAction{
		"http.assistant.agent_followup":    {ACLReadContent, ACLExecute, ACLWrite},
		"http.assistant.files":             {ACLReadContent},
		"http.assistant.file_save":         {ACLReadContent, ACLWrite},
		"http.assistant.deal_room_request": {ACLReadContent, ACLShare},
		"http.artifacts":                   {ACLReadContent, ACLWrite},
		"http.artifacts.action":            {ACLReadMetadata, ACLReadContent, ACLApprove, ACLExecute, ACLWrite},
		"http.artifacts.open":              {ACLReadContent},
		"http.artifacts.render_token":      {ACLReadContent, ACLExport},
		"http.artifacts.blob":              {ACLReadContent},
		"http.artifacts.share":             {ACLReadMetadata, ACLReadContent, ACLShare},
		"http.artifacts.export_pdf":        {ACLExport},
		"http.signals.survey":              {ACLReadContent, ACLWrite},
	}
	registered := map[string]AuthorizationSurface{}
	for _, surface := range AuthorizationSurfaces() {
		registered[surface.ID] = surface
	}
	for id, actions := range want {
		surface, ok := registered[id]
		if !ok {
			t.Fatalf("implemented surface %s missing", id)
		}
		if surface.Status != AuthorizationCanonicalEnforced || !surface.AuthorizeBeforeBodyRead || !surface.ReadsBody || !equalACLActions(surface.RequiredActions, actions) {
			t.Fatalf("implemented surface registry drift for %s: %+v want actions=%v", id, surface, actions)
		}
	}
	if _, err := json.Marshal(registered); err != nil {
		t.Fatal(err)
	}
}

func TestPublicChannelCannotDropAnotherUsersPrivateArtifact(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	private, _, err := kanbanApp.createOSArtifactWithMetadata("research", "PRIVATE DROP CANARY", "private body canary", "AJ", map[string]string{
		"visibility": "private", "requestedBy": "aj@shareability.com", "source": "scout_thread",
		"status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "public review", "public")
	if err != nil {
		t.Fatal(err)
	}
	teammate := accountStore().findUser("tim@shareability.com")
	if teammate == nil {
		t.Fatal("missing teammate fixture")
	}
	if _, err := kanbanApp.appendScoutChatThreadMessageWithTool(context.Background(), teammate, channel.ID, "revise this", nil, private.ID, ""); err == nil {
		t.Fatal("private artifact drop into public channel was allowed")
	}
	stored, _, err := kanbanApp.scoutChatThreadByID(teammate.Email, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(stored)
	if strings.Contains(string(raw), private.ID) || strings.Contains(string(raw), private.Text) || strings.Contains(string(raw), "PRIVATE DROP CANARY") {
		t.Fatalf("denied private drop exposed artifact in public thread: %s", raw)
	}
}

func TestFilesSaveConditionalRevisionAndRealtimePrincipal(t *testing.T) {
	ownerCookies, _, private := setupArtifactAuthorizationSlice(t)
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(private.ID, map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete", "title": "PRIVATE REALTIME CANARY",
	}); err != nil {
		t.Fatal(err)
	}

	previousProbe := artifactBodyReadProbe
	artifactBodyReadProbe = func(id string) {
		artifactBodyReadProbe = nil
		if id == private.ID {
			_, _, _ = kanbanApp.memory.updateOSArtifactMetadata(id, map[string]string{"title": "changed after authorization"})
		}
	}
	defer func() { artifactBodyReadProbe = previousProbe }()
	save := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/files/save", fmt.Sprintf(`{"artifactId":%q}`, private.ID), ownerCookies, assistantFileSaveHandler)
	if save.Code != http.StatusNotFound {
		t.Fatalf("stale authorized save status=%d body=%s", save.Code, save.Body.String())
	}
	after, _ := kanbanApp.osArtifactByID(private.ID)
	if strings.EqualFold(after.Metadata["savedToFiles"], "true") {
		t.Fatal("stale authorized snapshot stamped Files metadata")
	}

	result, _, err := kanbanApp.applyPrivateRealtimeVoiceTool("tim@shareability.com", "save_to_files", map[string]any{
		"fileNames": []any{"changed after authorization"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved, _ := result["saved"].([]string); len(saved) != 0 {
		t.Fatalf("realtime tool saved private artifact: %v", saved)
	}
	after, _ = kanbanApp.osArtifactByID(private.ID)
	if strings.EqualFold(after.Metadata["savedToFiles"], "true") {
		t.Fatal("realtime teammate saved private artifact")
	}
}

func TestFilesSaveFolderRaceCompensatesArtifactStamp(t *testing.T) {
	ownerCookies, _, private := setupArtifactAuthorizationSlice(t)
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(private.ID, map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete", "title": "folder race report",
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := kanbanApp.osArtifactByID(private.ID)
	folder, err := createFileFolder("Race Folder", "AJ")
	if err != nil {
		t.Fatal(err)
	}
	previousProbe := fileSaveAfterArtifactStampProbe
	fileSaveAfterArtifactStampProbe = func() {
		fileSaveAfterArtifactStampProbe = nil
		_ = sharedFileFolderStore().remove(folder.ID)
	}
	defer func() { fileSaveAfterArtifactStampProbe = previousProbe }()
	response := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/files/save", fmt.Sprintf(`{"artifactId":%q,"folderId":%q}`, private.ID, folder.ID), ownerCookies, assistantFileSaveHandler)
	if response.Code == http.StatusOK {
		t.Fatalf("folder race save unexpectedly succeeded: %s", response.Body.String())
	}
	after, _ := kanbanApp.osArtifactByID(private.ID)
	for _, key := range []string{"savedToFiles", "savedToFilesBy", "savedToFilesAt"} {
		beforeValue, beforeExists := before.Metadata[key]
		afterValue, afterExists := after.Metadata[key]
		if beforeExists != afterExists || beforeValue != afterValue {
			t.Fatalf("compensation changed %s: before=%q/%v after=%q/%v", key, beforeValue, beforeExists, afterValue, afterExists)
		}
	}
	_, assignments := sharedFileFolderStore().snapshot()
	if _, assigned := assignments[private.ID]; assigned {
		t.Fatalf("failed folder save left assignment: %+v", assignments)
	}
}

func TestGoalFollowUpRequiresIndependentParentAuthority(t *testing.T) {
	cookies, _, _ := setupArtifactAuthorizationSlice(t)
	parent, _, err := kanbanApp.memory.appendOSArtifact("parent-goal-authority", "parent secret body", map[string]string{"mode": "goal", "title": "PARENT PLAN CANARY", "visibility": "organization"})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := kanbanApp.memory.appendOSArtifact("child-goal-authority", "child body", map[string]string{
		"source": "goal_stage", "goalParentId": parent.ID, "status": "complete", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &surfaceRecordingArtifactAuthorizer{allow: func(action ACLAction, artifact meetingMemoryEntry) bool {
		return artifact.ID != parent.ID || action != ACLWrite
	}}
	installRecordingArtifactAuthorizer(t, authorizer)
	bodyReads := map[string]int{}
	previousProbe := artifactBodyReadProbe
	artifactBodyReadProbe = func(id string) { bodyReads[id]++ }
	defer func() { artifactBodyReadProbe = previousProbe }()
	response := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/threads/follow-up", fmt.Sprintf(`{"artifactId":%q,"text":"revise"}`, child.ID), cookies, assistantThreadFollowUpHandler)
	if response.Code == http.StatusAccepted || bodyReads[parent.ID] != 0 || strings.Contains(response.Body.String(), parent.Text) || strings.Contains(response.Body.String(), "PARENT PLAN CANARY") {
		t.Fatalf("parent authority bypass status=%d body=%s reads=%v calls=%+v", response.Code, response.Body.String(), bodyReads, authorizer.calls)
	}
	if !authorizer.saw(ACLReadContent, parent.ID) || !authorizer.saw(ACLExecute, parent.ID) || !authorizer.saw(ACLWrite, parent.ID) {
		t.Fatalf("parent conjunction not checked: %+v", authorizer.calls)
	}
}

func TestGoalFollowUpRejectsConcurrentParentRevision(t *testing.T) {
	cookies, _, _ := setupArtifactAuthorizationSlice(t)
	plan := goalPlan{Objective: "revise safely", State: goalStateBlocked, Subtasks: []goalSubtask{{ID: "write", Status: subtaskBlocked}}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := kanbanApp.memory.appendOSArtifact("parent-goal-concurrent", "parent body", map[string]string{
		"mode": "goal", "title": "parent goal", "goalPlan": string(planJSON), "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := kanbanApp.memory.appendOSArtifact("child-goal-concurrent", "child body", map[string]string{
		"source": "goal_stage", "goalParentId": parent.ID, "goalSubtaskId": "write", "status": "complete", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	previousProbe := artifactBodyReadProbe
	artifactBodyReadProbe = func(id string) {
		if id == parent.ID {
			artifactBodyReadProbe = nil
			_, _, _ = kanbanApp.memory.updateOSArtifactMetadata(parent.ID, map[string]string{"title": "parent changed concurrently"})
		}
	}
	defer func() { artifactBodyReadProbe = previousProbe }()
	started := false
	previousStart := startGoalFeedbackResumeAsync
	startGoalFeedbackResumeAsync = func(func()) { started = true }
	defer func() { startGoalFeedbackResumeAsync = previousStart }()
	response := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/threads/follow-up", fmt.Sprintf(`{"artifactId":%q,"text":"revise"}`, child.ID), cookies, assistantThreadFollowUpHandler)
	if response.Code == http.StatusAccepted || started {
		t.Fatalf("stale parent resumed: status=%d body=%s started=%v", response.Code, response.Body.String(), started)
	}
	after, _ := kanbanApp.osArtifactByID(parent.ID)
	if after.Metadata["goalPlan"] != string(planJSON) {
		t.Fatalf("stale parent plan mutated: %s", after.Metadata["goalPlan"])
	}
}

func TestGoalFollowUpResponseUsesExactPersistedSnapshot(t *testing.T) {
	cookies, _, _ := setupArtifactAuthorizationSlice(t)
	plan := goalPlan{Objective: "respond safely", State: goalStateBlocked, Subtasks: []goalSubtask{{ID: "write", Status: subtaskBlocked}}}
	planJSON, _ := json.Marshal(plan)
	parent, _, err := kanbanApp.memory.appendOSArtifact("parent-goal-response-race", "authorized parent body", map[string]string{
		"mode": "goal", "title": "authorized parent", "goalPlan": string(planJSON), "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := kanbanApp.memory.appendOSArtifact("child-goal-response-race", "child body", map[string]string{
		"source": "goal_stage", "goalParentId": parent.ID, "goalSubtaskId": "write", "status": "complete", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	previousStart := startGoalFeedbackResumeAsync
	startGoalFeedbackResumeAsync = func(func()) {}
	defer func() { startGoalFeedbackResumeAsync = previousStart }()
	previousProbe := goalFeedbackAfterPersistProbe
	goalFeedbackAfterPersistProbe = func() {
		goalFeedbackAfterPersistProbe = nil
		_, _, _ = kanbanApp.updateOSArtifactWithMetadata(parent.ID, "replacement title", "NEW PRIVATE BODY MUST NOT LEAK", "other", map[string]string{"visibility": "private", "ownerEmail": "tim@shareability.com"})
	}
	defer func() { goalFeedbackAfterPersistProbe = previousProbe }()
	response := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/threads/follow-up", fmt.Sprintf(`{"artifactId":%q,"text":"revise safely"}`, child.ID), cookies, assistantThreadFollowUpHandler)
	if response.Code != http.StatusAccepted {
		t.Fatalf("follow-up status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Artifact.Text != "authorized parent body" || payload.Artifact.Metadata["visibility"] != "organization" || strings.Contains(response.Body.String(), "NEW PRIVATE BODY MUST NOT LEAK") {
		t.Fatalf("response refetched concurrent private revision: %+v body=%s", payload.Artifact, response.Body.String())
	}
	current, _ := kanbanApp.osArtifactByID(parent.ID)
	if current.Text != "NEW PRIVATE BODY MUST NOT LEAK" || current.Metadata["visibility"] != "private" {
		t.Fatalf("race probe did not replace current parent: %+v", current)
	}
}

func equalACLActions(left, right []ACLAction) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
