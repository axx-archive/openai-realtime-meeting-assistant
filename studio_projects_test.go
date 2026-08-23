package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStudioProjectPollingUsesNarrowIncrementalDirectory(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })

	kanbanApp.memory.mu.Lock()
	for index := 0; index < 32844; index++ {
		kanbanApp.memory.entries = append(kanbanApp.memory.entries, meetingMemoryEntry{
			ID: fmt.Sprintf("unrelated-artifact-%05d", index), Kind: meetingMemoryKindOSArtifact,
			Metadata: map[string]string{"type": artifactTypeMarkdown, "source": "ordinary_file", "status": codexJobStatusComplete},
		})
	}
	kanbanApp.memory.rebuildMeetingEntryIndexesLocked()
	kanbanApp.memory.mu.Unlock()

	owner := "aj@shareability.com"
	thread, err := kanbanApp.createScoutChatThread(owner, "AJ", "Narrow Studio directory", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	root := seedStudioProjectRoot(t, kanbanApp, thread, packagingStudioProcessID, "One project among years of files", goalStateBlocked)
	visits := 0
	kanbanApp.memory.mu.Lock()
	kanbanApp.memory.artifactEntryVisitHook = func() { visits++ }
	kanbanApp.memory.mu.Unlock()
	t.Cleanup(func() {
		kanbanApp.memory.mu.Lock()
		kanbanApp.memory.artifactEntryVisitHook = nil
		kanbanApp.memory.mu.Unlock()
	})

	cookies := loginAs(t, owner, "B0NFIRE!")
	for attempt := 0; attempt < 2; attempt++ {
		visits = 0
		response := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?limit=100", "", cookies, studioProjectsHandler)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), root.ID) {
			t.Fatalf("Studio poll %d status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
		if visits > 20 {
			t.Fatalf("Studio poll %d visited %d artifact rows for one project among 32,844 unrelated files", attempt+1, visits)
		}
	}
}

func TestStudioProjectDirectoryTracksRelevanceTransitions(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	root, _, err := app.createOSArtifactWithMetadata("research", "Late-bound Studio root", "# Late-bound Studio root", "Scout", map[string]string{
		"source": "goal_thread", "mode": "goal", "processId": packagingStudioProcessID,
		"status": "running", "threadStatus": "running", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	contains := func(candidates []artifactListAuthorizationCandidate, id string) bool {
		for _, candidate := range candidates {
			if candidate.Entry.ID == id {
				return true
			}
		}
		return false
	}
	if contains(app.memory.studioProjectProjectionSnapshot(), root.ID) {
		t.Fatal("a pre-plan artifact entered the Studio directory")
	}
	plan := goalPlan{PlanVersion: goalPlanVersion, GoalID: root.ID, Objective: "Late-bound Studio root", CreatedBy: "AJ", RequestedBy: "aj@shareability.com", Authority: codexJobAuthorityReadOnly, ProcessID: packagingStudioProcessID, State: goalStateExecute}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err = app.updateOSArtifactWithMetadata(root.ID, "", root.Text, "Scout", map[string]string{"threadId": root.ID, "goalPlan": string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(app.memory.studioProjectProjectionSnapshot(), root.ID) {
		t.Fatal("adding the canonical goal plan did not add the root to the incremental Studio directory")
	}
	if _, _, err = app.updateOSArtifactWithMetadata(root.ID, "", root.Text, "Scout", map[string]string{"source": "ordinary_file"}); err != nil {
		t.Fatal(err)
	}
	if contains(app.memory.studioProjectProjectionSnapshot(), root.ID) {
		t.Fatal("removing canonical root provenance left a stale Studio directory entry")
	}
}

func TestStudioCompanyProjectProjectionReauthorizesSeparateProject(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
		studioProjectCompanyAuthorizationProbe = nil
	})

	owner := accountStore().findUser("aj@shareability.com")
	if owner == nil {
		t.Fatal("seed owner missing")
	}
	project, created, err := kanbanApp.ensureScoutChatThread(
		"stride_project_studio_acl",
		owner.Email,
		owner.Name,
		"Hidden Ranch Launch",
		scoutChatVisibilityPublic,
		[]string{"caitlyn@shareability.com"},
	)
	if err != nil || !created {
		t.Fatalf("create restricted Company Project: created=%v err=%v", created, err)
	}
	source, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Private Studio request", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "studio-company-affinity-source", Kind: "message", Role: "user",
		Text: "Build the Hidden Ranch Launch presentation", AuthorName: owner.Name,
		AuthorEmail: owner.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	source, err = kanbanApp.commitScoutChatThreadMessages(owner.Email, source.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	message = source.Messages[len(source.Messages)-1]
	binding, found := kanbanApp.resolveWorkstreamAffinity(owner, source, message, message.Text, time.Now().UTC())
	if !found || binding.ProjectThreadID != project.ID {
		t.Fatalf("Company Project affinity=%+v found=%v", binding, found)
	}
	encoded, err := encodeWorkstreamAffinity(binding)
	if err != nil {
		t.Fatal(err)
	}
	bindRoot := func(root meetingMemoryEntry) meetingMemoryEntry {
		updated, _, updateErr := kanbanApp.updateOSArtifactWithMetadata(root.ID, "", root.Text, "Scout", map[string]string{
			workstreamAffinityMetadataKey: encoded,
			"sourceMessageId":             message.ID,
			"sourceMessageDigest":         binding.SourceMessageDigest,
			"sourceWindowDigest":          binding.SourceWindowDigest,
			"projectWorkId":               project.ID,
			"projectWorkTitle":            project.Title,
		})
		if updateErr != nil {
			t.Fatal(updateErr)
		}
		return updated
	}
	first := bindRoot(seedStudioProjectRoot(t, kanbanApp, source, packagingStudioProcessID, "Hidden Ranch story", goalStateBlocked))
	_ = bindRoot(seedStudioProjectRoot(t, kanbanApp, source, documentReportProcessID, "Hidden Ranch evidence", goalStateBlocked))

	ownerCookies := loginAs(t, owner.Email, "B0NFIRE!")
	projectReads := 0
	studioProjectCompanyAuthorizationProbe = func(string) { projectReads++ }
	ownerList := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1", "", ownerCookies, studioProjectsHandler)
	studioProjectCompanyAuthorizationProbe = nil
	if ownerList.Code != http.StatusOK || projectReads != 1 {
		t.Fatalf("owner Studio list status=%d project_reads=%d body=%s", ownerList.Code, projectReads, ownerList.Body.String())
	}
	var listPayload struct {
		Projects []studioProjectView `json:"projects"`
	}
	if err := json.Unmarshal(ownerList.Body.Bytes(), &listPayload); err != nil {
		t.Fatal(err)
	}
	if len(listPayload.Projects) != 2 {
		t.Fatalf("owner projects=%+v", listPayload.Projects)
	}
	for _, studio := range listPayload.Projects {
		if studio.CompanyProject == nil || studio.CompanyProject.ID != project.ID || studio.CompanyProject.Title != project.Title {
			t.Fatalf("authorized Company Project projection=%+v", studio)
		}
	}

	// Sharing the Studio root does not share either its private source or its
	// separately member-scoped Company Project.
	artifactObjectAuthorizer = deckReadOnlyAuthorizer{}
	teammateCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	shared := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+first.ID, "", teammateCookies, studioProjectsHandler)
	if shared.Code != http.StatusOK {
		t.Fatalf("shared Studio detail=%d body=%s", shared.Code, shared.Body.String())
	}
	var sharedPayload struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(shared.Body.Bytes(), &sharedPayload); err != nil {
		t.Fatal(err)
	}
	if sharedPayload.Project.Source != nil || sharedPayload.Project.CompanyProject != nil || sharedPayload.Project.CanRename {
		t.Fatalf("shared Studio root leaked separate authority: %+v", sharedPayload.Project)
	}

	// A member of the exact Company Project may receive its current name while
	// the unrelated private source remains suppressed.
	memberCookies := loginAs(t, "caitlyn@shareability.com", "B0NFIRE!")
	member := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+first.ID, "", memberCookies, studioProjectsHandler)
	if member.Code != http.StatusOK {
		t.Fatalf("project member Studio detail=%d body=%s", member.Code, member.Body.String())
	}
	var memberPayload struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(member.Body.Bytes(), &memberPayload); err != nil {
		t.Fatal(err)
	}
	if memberPayload.Project.Source != nil || memberPayload.Project.CompanyProject == nil || memberPayload.Project.CompanyProject.ID != project.ID {
		t.Fatalf("project member projection=%+v", memberPayload.Project)
	}
}

func TestStudioProjectLivePayloadIsImmediateAndViewerAuthorized(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Public Studio receipt", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	root := seedStudioProjectRoot(t, kanbanApp, thread, packagingStudioProcessID, "Live presentation", goalStateExecute)
	message := scoutChatMessageRecord{ID: "live-studio-receipt", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{
		ID: root.ID, RootRunID: root.ID, Mode: "goal", ProcessID: packagingStudioProcessID,
		Status: codexJobStatusRunning, ArtifactID: root.ID,
	}}
	indexBuilds := 0
	previousProbe := scoutChatResultIndexProbe
	scoutChatResultIndexProbe = func() { indexBuilds++ }
	t.Cleanup(func() { scoutChatResultIndexProbe = previousProbe })
	owner := chatThreadPayloadMessage(t, kanbanApp.scoutChatThreadUpdatePayload("aj@shareability.com", thread, message))
	if indexBuilds != 1 || owner.StudioProject == nil || owner.StudioProject.ID != root.ID {
		t.Fatalf("live Studio payload did not immediately project the authorized quiet receipt: builds=%d message=%+v", indexBuilds, owner)
	}
	publicViewer := chatThreadPayloadMessage(t, kanbanApp.scoutChatThreadUpdatePayload("tim@shareability.com", thread, message))
	if publicViewer.StudioProject == nil || publicViewer.StudioProject.ID != root.ID {
		t.Fatalf("organization Studio receipt did not reach another authorized channel viewer: %+v", publicViewer)
	}

	privateThread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Private Studio receipt", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := seedStudioProjectRoot(t, kanbanApp, privateThread, documentReportProcessID, "Private research", goalStateExecute)
	privateMessage := scoutChatMessageRecord{ID: "private-live-studio-receipt", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{
		ID: privateRoot.ID, RootRunID: privateRoot.ID, Mode: "goal", ProcessID: documentReportProcessID,
		Status: codexJobStatusRunning, ArtifactID: privateRoot.ID,
	}}
	privateOwner := chatThreadPayloadMessage(t, kanbanApp.scoutChatThreadUpdatePayload("aj@shareability.com", privateThread, privateMessage))
	if privateOwner.StudioProject == nil || privateOwner.StudioProject.ID != privateRoot.ID {
		t.Fatalf("private Studio receipt did not reach its owner: %+v", privateOwner)
	}
	denied := chatThreadPayloadMessage(t, kanbanApp.scoutChatThreadUpdatePayload("tim@shareability.com", privateThread, privateMessage))
	if denied.StudioProject != nil {
		t.Fatalf("private Studio receipt leaked to another viewer: %+v", denied.StudioProject)
	}
}

func seedStudioProjectRoot(t *testing.T, app *kanbanBoardApp, thread scoutChatThreadRecord, processID, title, state string) meetingMemoryEntry {
	t.Helper()
	root, _, err := app.createOSArtifactWithMetadata("research", title, "# "+title, "Scout", map[string]string{
		"source": "goal_thread", "mode": "goal", "processId": processID,
		"threadQuery": title, "title": title, "status": "running", "threadStatus": "running",
		"progressPercent": "42", "originKind": agentThreadOriginPrivateThread, "originId": thread.ID,
		"originSurface": "chat:" + thread.ID, "visibility": scoutChatVisibilityPrivate,
		"ownerEmail": thread.OwnerEmail, "requestedBy": thread.OwnerEmail,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{PlanVersion: goalPlanVersion, GoalID: root.ID, Objective: title, CreatedBy: "AJ", RequestedBy: thread.OwnerEmail, Authority: codexJobAuthorityReadOnly, ProcessID: processID, State: state}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	status := "running"
	progress := "42"
	if state == goalStateApproval {
		status, progress = codexJobStatusApprovalRequired, "68"
	}
	if state == goalStateBlocked {
		status, progress = "needs_attention", "72"
	}
	if state == goalStateVerified {
		status, progress = codexJobStatusComplete, "100"
	}
	root, _, err = app.updateOSArtifactWithMetadata(root.ID, "", root.Text, "Scout", map[string]string{
		"threadId": root.ID, "goalPlan": string(raw), "currentStage": state,
		"status": status, "threadStatus": status, "progressPercent": progress,
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func updateStudioProjectPlan(t *testing.T, app *kanbanBoardApp, root meetingMemoryEntry, update func(*goalPlan)) meetingMemoryEntry {
	t.Helper()
	plan, ok := decodeGoalPlan(root.Metadata["goalPlan"])
	if !ok {
		t.Fatal("studio goal plan did not decode")
	}
	update(&plan)
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := app.updateOSArtifactWithMetadata(root.ID, "", root.Text, "Scout", map[string]string{"goalPlan": string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestStudioProjectsKeepLegacyStandaloneDecksAndReportsReachable(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })

	owner := "aj@shareability.com"
	source, err := kanbanApp.createScoutChatThread(owner, "AJ", "Historical authored work", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	baseMetadata := map[string]string{
		"source": "scout_thread", "status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete,
		"originKind": agentThreadOriginPrivateThread, "originId": source.ID, "visibility": scoutChatVisibilityPrivate,
		"ownerEmail": owner, "requestedBy": owner,
	}
	legacyDeckMetadata := cloneStringMap(baseMetadata)
	legacyDeckMetadata["mode"], legacyDeckMetadata["threadId"] = "presentation", "legacy-presentation-run"
	legacyDeckMetadata["type"] = artifactTypeHTMLDeck
	legacyDeck, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Historical presentation", "<!doctype html><html><body><main>Historical presentation</main></body></html>", "Scout", legacyDeckMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if kind, ok := studioLegacyProjectCandidate(legacyDeck); !ok || kind != studioProjectKindPresentation {
		t.Fatalf("legacy presentation was not classified: kind=%q ok=%v entry=%+v", kind, ok, legacyDeck)
	}
	legacyReportMetadata := cloneStringMap(baseMetadata)
	legacyReportMetadata["mode"], legacyReportMetadata["threadId"] = "research", "legacy-report-run"
	legacyReportMetadata["type"] = artifactTypeMarkdown
	legacyReport, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Historical report", "# Historical report\n\nEvidence that must remain reachable.", "Scout", legacyReportMetadata)
	if err != nil {
		t.Fatal(err)
	}
	failedReportMetadata := cloneStringMap(legacyReportMetadata)
	failedReportMetadata["threadId"], failedReportMetadata["status"], failedReportMetadata["threadStatus"] = "legacy-failed-report-run", codexJobStatusFailed, codexJobStatusFailed
	failedReport, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Historical failed report", "Research failed before a deliverable was produced.", "Scout", failedReportMetadata)
	if err != nil {
		t.Fatal(err)
	}
	namedCopyMetadata := cloneStringMap(baseMetadata)
	delete(namedCopyMetadata, "threadId")
	namedCopyMetadata["mode"], namedCopyMetadata["type"] = "artifacts", artifactTypeHTMLDeck
	namedCopyMetadata[deckSceneRefMetadataKey] = strings.Repeat("a", 64)
	namedCopyMetadata["copiedFromArtifactId"] = legacyDeck.ID
	namedCopy, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Historical named copy", "<!doctype html><html><body><main>Historical named copy</main></body></html>", "Scout", namedCopyMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if kind, ok := studioLegacyProjectCandidate(namedCopy); !ok || kind != studioProjectKindPresentation {
		t.Fatalf("native named copy was not classified: kind=%q ok=%v entry=%+v", kind, ok, namedCopy)
	}
	childMetadata := cloneStringMap(legacyReportMetadata)
	childMetadata["threadId"], childMetadata["goalParentId"], childMetadata["processStage"] = "legacy-child-run", "legacy-parent", "research"
	if _, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Internal process child", "# Internal process child", "Scout", childMetadata); err != nil {
		t.Fatal(err)
	}
	ordinaryMetadata := cloneStringMap(legacyReportMetadata)
	ordinaryMetadata["source"], ordinaryMetadata["threadId"] = "ordinary_file", "ordinary-file-run"
	if _, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Ordinary file", "# Ordinary file", "Scout", ordinaryMetadata); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"design", "grill", "workflow", "artifacts"} {
		metadata := cloneStringMap(legacyReportMetadata)
		metadata["mode"], metadata["threadId"] = mode, "legacy-"+mode+"-run"
		if _, _, err := kanbanApp.createOSArtifactWithMetadata(mode, "Internal "+mode+" output", "# Internal "+mode+" output", "Scout", metadata); err != nil {
			t.Fatal(err)
		}
	}
	untetheredDeckMetadata := cloneStringMap(namedCopyMetadata)
	untetheredDeckMetadata["source"] = ""
	if _, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Internal untethered deck stage", "<!doctype html><html><body>Internal stage</body></html>", "Scout", untetheredDeckMetadata); err != nil {
		t.Fatal(err)
	}

	cookies := loginAs(t, owner, "B0NFIRE!")
	list := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1", "", cookies, studioProjectsHandler)
	if list.Code != http.StatusOK {
		t.Fatalf("legacy Studio list=%d body=%s", list.Code, list.Body.String())
	}
	var payload struct {
		Projects []studioProjectView `json:"projects"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Projects) != 4 || strings.Contains(list.Body.String(), "Evidence that must remain reachable") || strings.Contains(list.Body.String(), "Internal process child") || strings.Contains(list.Body.String(), "Ordinary file") || strings.Contains(list.Body.String(), "Internal design output") || strings.Contains(list.Body.String(), "Internal grill output") || strings.Contains(list.Body.String(), "Internal workflow output") || strings.Contains(list.Body.String(), "Internal artifacts output") || strings.Contains(list.Body.String(), "Internal untethered deck stage") {
		t.Fatalf("legacy migration projection=%s", list.Body.String())
	}
	byID := map[string]studioProjectView{}
	for _, project := range payload.Projects {
		byID[project.ID] = project
	}
	if deck := byID[legacyDeck.ID]; deck.Kind != studioProjectKindPresentation || deck.Status != studioProjectStatusReady || deck.Result == nil || deck.Result.ArtifactID != legacyDeck.ID || deck.Result.ReviewManaged || !deck.Result.CanEdit || !deck.Result.CanPresent || !deck.Result.CanExport || deck.Result.QualityState != "" {
		t.Fatalf("legacy presentation=%+v", deck)
	}
	if report := byID[legacyReport.ID]; report.Kind != studioProjectKindDocument || report.Status != studioProjectStatusReady || report.Result == nil || report.Result.ArtifactID != legacyReport.ID || report.Result.ReviewManaged || !report.Result.CanEdit || !report.Result.CanExport || report.Result.Preview != "" {
		t.Fatalf("legacy report=%+v", report)
	}
	if failed := byID[failedReport.ID]; failed.Kind != studioProjectKindDocument || failed.Status != studioProjectStatusNeedsAttention || failed.Result != nil || failed.ProgressPercent != 0 {
		t.Fatalf("failed legacy report=%+v", failed)
	}
	if copy := byID[namedCopy.ID]; copy.Kind != studioProjectKindPresentation || copy.Status != studioProjectStatusReady || copy.RootRunID != namedCopy.ID || copy.Result == nil || copy.Result.ArtifactID != namedCopy.ID || !copy.Result.CanEdit || !copy.Result.CanPresent || !copy.Result.CanExport {
		t.Fatalf("native named copy=%+v", copy)
	}

	rename := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", `{"id":"`+legacyReport.ID+`","title":"Renamed historical report","expectedRevision":1}`, cookies, studioProjectsHandler)
	if rename.Code != http.StatusOK {
		t.Fatalf("rename legacy report=%d body=%s", rename.Code, rename.Body.String())
	}
	var renamed struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(rename.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Project.Title != "Renamed historical report" || renamed.Project.Revision != 2 || renamed.Project.Result == nil || renamed.Project.Result.ArtifactID != legacyReport.ID {
		t.Fatalf("renamed legacy report=%+v", renamed.Project)
	}
}

func TestStudioProjectsRootOnlyClassificationAndViewerReceipt(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })

	owner := "aj@shareability.com"
	thread, err := kanbanApp.createScoutChatThread(owner, "AJ", "Studio source", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	presentation := seedStudioProjectRoot(t, kanbanApp, thread, packagingStudioProcessID, "Western culture opportunity", goalStateBlocked)
	document := seedStudioProjectRoot(t, kanbanApp, thread, documentReportProcessID, "Engagement army research", goalStateApproval)
	checkpointRaw, err := json.Marshal(goalProcessCheckpoint{
		StageID: "audience", Question: "Which audience should anchor the report?",
		Options: []goalCheckpointOption{{Label: "Operators and brand leaders", Action: processCheckpointActionProceed}, {Label: "Hold for now", Action: processCheckpointActionHold}},
	})
	if err != nil {
		t.Fatal(err)
	}
	document, _, err = kanbanApp.updateOSArtifactWithMetadata(document.ID, "", document.Text, "Scout", map[string]string{"checkpoint": string(checkpointRaw)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Internal stage", "not a project", "Scout", map[string]string{
		"source": "goal_thread", "mode": "goal", "processId": packagingStudioProcessID,
		"threadId": "child-run", "goalParentId": presentation.ID, "goalPlan": presentation.Metadata["goalPlan"],
		"originSurface": "chat:" + thread.ID, "requestedBy": owner,
	}); err != nil {
		t.Fatal(err)
	}

	deck, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Opportunity deck", "<!doctype html><html><body>Deck</body></html>", "Scout", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": presentation.ID,
		"threadId": "deck-child", "status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete,
		"originSurface": "chat:" + thread.ID, "requestedBy": owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	presentation = updateStudioProjectPlan(t, kanbanApp, presentation, func(plan *goalPlan) {
		plan.Report.DeliverableArtifactID = deck.ID
	})

	cookies := loginAs(t, owner, "B0NFIRE!")
	sourceAuthorizationCalls := 0
	previousSourceProbe := studioProjectSourceAuthorizationProbe
	studioProjectSourceAuthorizationProbe = func(string) { sourceAuthorizationCalls++ }
	t.Cleanup(func() { studioProjectSourceAuthorizationProbe = previousSourceProbe })
	response := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1", "", cookies, studioProjectsHandler)
	if response.Code != http.StatusOK {
		t.Fatalf("studio list=%d body=%s", response.Code, response.Body.String())
	}
	if sourceAuthorizationCalls != 1 {
		t.Fatalf("two Studio roots sharing one source authorized/decoded it %d times, want one", sourceAuthorizationCalls)
	}
	studioProjectSourceAuthorizationProbe = nil
	var payload struct {
		Projects []studioProjectView `json:"projects"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Projects) != 2 {
		t.Fatalf("projects=%+v, want exactly the two roots", payload.Projects)
	}
	if strings.Contains(response.Body.String(), "# Western culture opportunity") || strings.Contains(response.Body.String(), "# Engagement army research") {
		t.Fatalf("Studio list leaked root artifact bodies: %s", response.Body.String())
	}
	byID := map[string]studioProjectView{}
	for _, project := range payload.Projects {
		byID[project.ID] = project
	}
	if got := byID[presentation.ID]; got.Kind != studioProjectKindPresentation || got.Status != studioProjectStatusNeedsAttention || got.Result == nil || got.Result.ArtifactID != deck.ID || !got.Result.CanEdit {
		t.Fatalf("presentation=%+v, want blocked root plus exact editable deck", got)
	}
	if got := byID[document.ID]; got.Kind != studioProjectKindDocument || got.Status != studioProjectStatusNeedsInput || got.Checkpoint == nil || len(got.Checkpoint.Options) != 2 {
		t.Fatalf("document=%+v, want one actionable needs-input report root", got)
	}
	plan, ok := decodeGoalPlan(document.Metadata["goalPlan"])
	if !ok || studioProjectStatus(document, plan, false) != studioProjectStatusNeedsAttention {
		t.Fatal("an approval state without an actionable viewer checkpoint must degrade to needs attention")
	}
	if studioProjectCheckpointForViewer(document, &userAccount{Email: "tim@shareability.com", Name: "Tim"}) != nil {
		t.Fatal("an admin-only checkpoint was projected as actionable to an ordinary member")
	}

	message := scoutChatMessageRecord{ID: "studio-receipt", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{
		ID: presentation.ID, Mode: "goal", ProcessID: packagingStudioProcessID, ArtifactID: presentation.ID, Status: "needs_attention",
	}}
	receiptSourceCalls := 0
	studioProjectSourceAuthorizationProbe = func(string) { receiptSourceCalls++ }
	projected := kanbanApp.projectScoutChatMessageForViewer(owner, thread, message, context.Background())
	_ = kanbanApp.projectScoutChatMessageForViewer(owner, thread, message, context.Background())
	studioProjectSourceAuthorizationProbe = nil
	if receiptSourceCalls != 0 {
		t.Fatalf("quiet receipts authorized/decoded an unprojected source %d times, want zero", receiptSourceCalls)
	}
	if projected.Thread == nil || projected.StudioProject == nil || projected.StudioProject.ID != presentation.ID || projected.StudioProject.Href == "" {
		t.Fatalf("viewer projection lost legacy ref or studio receipt: %+v", projected)
	}
	denied := kanbanApp.projectScoutChatMessageForViewer("tim@shareability.com", thread, message, context.Background())
	if denied.StudioProject != nil {
		t.Fatalf("private Studio receipt leaked to non-owner: %+v", denied.StudioProject)
	}
	decisionMessage := scoutChatMessageRecord{ID: "studio-decision", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{
		ID: document.ID, Mode: "goal", ProcessID: documentReportProcessID, ArtifactID: document.ID,
		Status: codexJobStatusApprovalRequired, Checkpoint: scoutChatCheckpointRefForArtifact(document),
	}}
	decisionProjection := kanbanApp.projectScoutChatMessageForViewer(owner, thread, decisionMessage, context.Background())
	if decisionProjection.StudioProject == nil || decisionProjection.StudioProject.Checkpoint == nil || decisionProjection.Thread == nil || decisionProjection.Thread.Checkpoint != nil {
		t.Fatalf("Studio decision was not moved onto the viewer-gated receipt: %+v", decisionProjection)
	}

	teammateCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	teammateList := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1", "", teammateCookies, studioProjectsHandler)
	if teammateList.Code != http.StatusOK || strings.Contains(teammateList.Body.String(), presentation.ID) || strings.Contains(teammateList.Body.String(), document.ID) {
		t.Fatalf("private Studio list leaked to teammate: status=%d body=%s", teammateList.Code, teammateList.Body.String())
	}
	teammateRead := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+presentation.ID, "", teammateCookies, studioProjectsHandler)
	if teammateRead.Code != http.StatusNotFound || strings.Contains(teammateRead.Body.String(), presentation.ID) {
		t.Fatalf("private Studio detail was not opaque: status=%d body=%s", teammateRead.Code, teammateRead.Body.String())
	}
	// A separately shared/read-only artifact does not imply access to the private
	// conversation that created it. The Studio may show the project but must omit
	// that unauthorized source id and its dead navigation affordance.
	artifactObjectAuthorizer = deckReadOnlyAuthorizer{}
	sharedRead := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+presentation.ID, "", teammateCookies, studioProjectsHandler)
	if sharedRead.Code != http.StatusOK {
		t.Fatalf("shared read-only Studio detail=%d body=%s", sharedRead.Code, sharedRead.Body.String())
	}
	var sharedPayload struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(sharedRead.Body.Bytes(), &sharedPayload); err != nil {
		t.Fatal(err)
	}
	if sharedPayload.Project.Source != nil || sharedPayload.Project.CanRename {
		t.Fatalf("shared read-only project leaked its private source or write affordance: %+v", sharedPayload.Project)
	}
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}

	pageOne := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?limit=1", "", cookies, studioProjectsHandler)
	if pageOne.Code != http.StatusOK || pageOne.Header().Get("ETag") == "" {
		t.Fatalf("Studio page one status=%d etag=%q body=%s", pageOne.Code, pageOne.Header().Get("ETag"), pageOne.Body.String())
	}
	var firstPage struct {
		Projects   []studioProjectView `json:"projects"`
		HasMore    bool                `json:"hasMore"`
		NextBefore string              `json:"nextBefore"`
	}
	if err := json.Unmarshal(pageOne.Body.Bytes(), &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Projects) != 1 || !firstPage.HasMore || firstPage.NextBefore != firstPage.Projects[0].ID {
		t.Fatalf("Studio pagination=%+v", firstPage)
	}
	pageTwo := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?limit=1&before="+firstPage.NextBefore, "", cookies, studioProjectsHandler)
	if pageTwo.Code != http.StatusOK || strings.Contains(pageTwo.Body.String(), firstPage.Projects[0].ID) {
		t.Fatalf("Studio page two overlaps page one: status=%d body=%s", pageTwo.Code, pageTwo.Body.String())
	}
	etagRequest := httptest.NewRequest(http.MethodGet, "/api/studio-projects/v1?limit=1", nil)
	etagRequest.Header.Set("If-None-Match", pageOne.Header().Get("ETag"))
	for _, cookie := range cookies {
		etagRequest.AddCookie(cookie)
	}
	etagResponse := httptest.NewRecorder()
	studioProjectsHandler(etagResponse, etagRequest)
	if etagResponse.Code != http.StatusNotModified || etagResponse.Body.Len() != 0 {
		t.Fatalf("Studio conditional read status=%d body=%s", etagResponse.Code, etagResponse.Body.String())
	}
}

func TestStudioProjectRenameUsesRevisionCASWithoutRenamingResult(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })

	owner := "aj@shareability.com"
	thread, err := kanbanApp.createScoutChatThread(owner, "AJ", "Rename source", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	root := seedStudioProjectRoot(t, kanbanApp, thread, packagingStudioProcessID, "Original project", goalStateBlocked)
	cookies := loginAs(t, owner, "B0NFIRE!")
	body := `{"id":"` + root.ID + `","title":"A sharper project name","expectedRevision":1}`
	response := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", body, cookies, studioProjectsHandler)
	if response.Code != http.StatusOK {
		t.Fatalf("rename=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Project.Title != "A sharper project name" || payload.Project.Revision != 2 {
		t.Fatalf("renamed project=%+v", payload.Project)
	}
	stored, ok := kanbanApp.osArtifactByID(root.ID)
	if !ok || stored.Metadata["title"] != "Original project" || stored.Metadata["studioTitle"] != "A sharper project name" {
		t.Fatalf("rename changed canonical artifact/result title: %+v", stored.Metadata)
	}
	stale := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", body, cookies, studioProjectsHandler)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale rename=%d body=%s", stale.Code, stale.Body.String())
	}

	artifactObjectAuthorizer = deckReadOnlyAuthorizer{}
	readOnlyList := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+root.ID, "", cookies, studioProjectsHandler)
	if readOnlyList.Code != http.StatusOK {
		t.Fatalf("read-only Studio detail=%d body=%s", readOnlyList.Code, readOnlyList.Body.String())
	}
	var readOnlyPayload struct {
		Project studioProjectView `json:"project"`
	}
	if err := json.Unmarshal(readOnlyList.Body.Bytes(), &readOnlyPayload); err != nil {
		t.Fatal(err)
	}
	if readOnlyPayload.Project.CanRename {
		t.Fatalf("read-only Studio detail advertised Rename: %+v", readOnlyPayload.Project)
	}
	readOnlyRename := artifactAuthorizationRequest(t, http.MethodPatch, "/api/studio-projects/v1", `{"id":"`+root.ID+`","title":"Should not land","expectedRevision":2}`, cookies, studioProjectsHandler)
	if readOnlyRename.Code != http.StatusNotFound {
		t.Fatalf("read-only rename=%d body=%s, want opaque denial", readOnlyRename.Code, readOnlyRename.Body.String())
	}
}

func TestStudioProjectReadyRequiresExactAuthorizedAdmittedResult(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })

	owner := "aj@shareability.com"
	thread, err := kanbanApp.createScoutChatThread(owner, "AJ", "Ready truth", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	root := seedStudioProjectRoot(t, kanbanApp, thread, packagingStudioProcessID, "Exact result truth", goalStateVerified)
	cookies := loginAs(t, owner, "B0NFIRE!")
	detail := func() studioProjectView {
		response := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?id="+root.ID, "", cookies, studioProjectsHandler)
		if response.Code != http.StatusOK {
			t.Fatalf("Studio detail=%d body=%s", response.Code, response.Body.String())
		}
		var payload struct {
			Project studioProjectView `json:"project"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Project
	}
	if got := detail(); got.Status != studioProjectStatusNeedsAttention || got.Result != nil {
		t.Fatalf("verified root without an exact deliverable claimed Ready: %+v", got)
	}

	deck, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Exact result", "<!doctype html><html><body>Exact</body></html>", "Scout", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": root.ID,
		"threadId": "exact-result-child", "status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete,
		"originSurface": "chat:" + thread.ID, "requestedBy": owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	shipRecord, _, err := kanbanApp.createOSArtifactWithMetadata("workflow", "Ship exact result", "Exact draft compiled", "Scout", map[string]string{
		"source": "process_stage", "processStage": "ship_compile", "goalParentId": root.ID,
		"deckArtifactId": deck.ID, "status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete,
	})
	if err != nil {
		t.Fatal(err)
	}
	root = updateStudioProjectPlan(t, kanbanApp, root, func(plan *goalPlan) {
		plan.Report.DeliverableArtifactID = deck.ID
		plan.Subtasks = append(plan.Subtasks, goalSubtask{ID: "ship_compile", Status: subtaskComplete, ArtifactID: shipRecord.ID})
	})
	if got := detail(); got.Status != studioProjectStatusNeedsAttention || got.Result == nil || got.Result.ArtifactID != deck.ID || !got.Result.ReviewManaged || got.Result.QualityState != authoredResultQualityDraftNeedsAttention {
		t.Fatalf("verified root with an unadmitted exact deliverable claimed Ready: %+v", got)
	}
	if !studioProjectResultReady(&studioProjectResultRef{ArtifactID: deck.ID, QualityState: authoredResultQualityAdmitted, ReviewManaged: true}) {
		t.Fatal("an exact admitted result was not eligible for Ready")
	}
	if studioProjectResultReady(&studioProjectResultRef{ArtifactID: deck.ID, QualityState: authoredResultQualityEditedAfterAdmission, ReviewManaged: true, CanContinue: true}) {
		t.Fatal("an edited-after-admission result remained eligible for Ready")
	}
	if !studioProjectResultReady(&studioProjectResultRef{ArtifactID: "legacy", ReviewManaged: false}) {
		t.Fatal("an unmanaged exact legacy result was not eligible for Ready")
	}
	message := scoutChatMessageRecord{ID: "ready-receipt", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{
		ID: root.ID, Mode: "goal", ProcessID: packagingStudioProcessID, ArtifactID: root.ID, Status: codexJobStatusComplete,
	}}
	projected := kanbanApp.projectScoutChatMessageForViewer(owner, thread, message, context.Background())
	if projected.StudioProject == nil || projected.StudioProject.Status != studioProjectStatusNeedsAttention {
		t.Fatalf("receipt did not inherit exact-result review truth: %+v", projected.StudioProject)
	}
}
