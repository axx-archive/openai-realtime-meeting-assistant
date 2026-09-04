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

func projectFolderIDs(t *testing.T) (string, map[string]string) {
	t.Helper()
	rootID := ""
	children := map[string]string{}
	folders := listFileFolders()
	for _, folder := range folders {
		if folder.ParentID == "" && folder.Name == artifactProjectsRootFolderName {
			rootID = folder.ID
		}
	}
	for _, folder := range folders {
		if rootID != "" && folder.ParentID == rootID {
			children[folder.Name] = folder.ID
		}
	}
	return rootID, children
}

func tagProject(t *testing.T, cookies []*http.Cookie, artifactID string, project string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"artifactId": artifactID, "project": project})
	response := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/project", string(body), cookies, artifactProjectHandler)
	payload := map[string]any{}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	return response.Code, payload
}

func TestArtifactProjectTagAutoFilesIntoProjectsFolderAndMovesOnRetag(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	doc, err := createDocumentStudioArtifact(aj, "Launch memo", "# Launch memo\n\nbody", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(doc.Metadata["savedToFiles"], "true") {
		t.Fatal("fixture: document already saved")
	}

	code, payload := tagProject(t, cookies, doc.ID, "  Nordic   launch ")
	if code != http.StatusOK {
		t.Fatalf("tag status=%d payload=%v", code, payload)
	}
	rootID, children := projectFolderIDs(t)
	if rootID == "" || children["Nordic launch"] == "" {
		t.Fatalf("Projects/<name> folders not created: root=%q children=%v", rootID, children)
	}
	if payload["project"] != "Nordic launch" || payload["folderId"] != children["Nordic launch"] || payload["saved"] != true || payload["moved"] != true || payload["fileId"] != doc.ID {
		t.Fatalf("tag payload=%v", payload)
	}
	tagged, _ := kanbanApp.osArtifactByID(doc.ID)
	if tagged.Metadata[artifactProjectMetadataKey] != "Nordic launch" || tagged.Metadata[artifactProjectFolderIDMetadataKey] != children["Nordic launch"] || !strings.EqualFold(tagged.Metadata["savedToFiles"], "true") || tagged.Metadata[artifactProjectTaggedByMetadataKey] != aj.Email {
		t.Fatalf("tagged metadata=%v", tagged.Metadata)
	}
	_, assignments := sharedFileFolderStore().snapshot()
	if assignments[doc.ID] != children["Nordic launch"] {
		t.Fatalf("Drive copy filed under %q, want %q", assignments[doc.ID], children["Nordic launch"])
	}

	// The projects list and the studio filter see the tag.
	projects := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/projects", "", cookies, assistantProjectsHandler)
	if projects.Code != http.StatusOK {
		t.Fatalf("projects status=%d body=%s", projects.Code, projects.Body.String())
	}
	var projectsPayload struct {
		Projects []artifactProjectView `json:"projects"`
	}
	if err := json.Unmarshal(projects.Body.Bytes(), &projectsPayload); err != nil {
		t.Fatal(err)
	}
	if len(projectsPayload.Projects) != 1 || projectsPayload.Projects[0].Name != "Nordic launch" || projectsPayload.Projects[0].Count != 1 || projectsPayload.Projects[0].FolderID != children["Nordic launch"] {
		t.Fatalf("projects=%+v", projectsPayload.Projects)
	}
	filtered := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?project=nordic%20launch", "", cookies, studioProjectsHandler)
	var rows struct {
		Projects []studioProjectView `json:"projects"`
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if filtered.Code != http.StatusOK || len(rows.Projects) != 1 || rows.Projects[0].ID != doc.ID || rows.Projects[0].Project != "Nordic launch" {
		t.Fatalf("filtered studio list status=%d rows=%+v", filtered.Code, rows.Projects)
	}
	// A name outside the viewer's project set is indistinguishable from a
	// nonexistent project.
	other := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?project=Other", "", cookies, studioProjectsHandler)
	if other.Code != http.StatusNotFound {
		t.Fatalf("other project filter status=%d body=%s, want 404", other.Code, other.Body.String())
	}

	// Retag moves the Drive copy; the prior folder stays (empty).
	code, payload = tagProject(t, cookies, doc.ID, "Growth")
	if code != http.StatusOK || payload["moved"] != true || payload["saved"] != false {
		t.Fatalf("retag status=%d payload=%v", code, payload)
	}
	_, children = projectFolderIDs(t)
	_, assignments = sharedFileFolderStore().snapshot()
	if children["Growth"] == "" || assignments[doc.ID] != children["Growth"] || children["Nordic launch"] == "" {
		t.Fatalf("retag folders=%v assignment=%q", children, assignments[doc.ID])
	}
	// Retagging the same project is a no-op move.
	if code, payload = tagProject(t, cookies, doc.ID, "growth"); code != http.StatusOK || payload["moved"] != false || payload["project"] != "Growth" {
		t.Fatalf("same-project retag status=%d payload=%v", code, payload)
	}

	// Untag clears the tag and leaves the file where it is.
	code, payload = tagProject(t, cookies, doc.ID, "")
	if code != http.StatusOK || payload["project"] != "" {
		t.Fatalf("untag status=%d payload=%v", code, payload)
	}
	untagged, _ := kanbanApp.osArtifactByID(doc.ID)
	_, assignments = sharedFileFolderStore().snapshot()
	if untagged.Metadata[artifactProjectMetadataKey] != "" || assignments[doc.ID] != children["Growth"] {
		t.Fatalf("untag metadata=%v assignment=%q", untagged.Metadata, assignments[doc.ID])
	}
	projects = artifactAuthorizationRequest(t, http.MethodGet, "/assistant/projects", "", cookies, assistantProjectsHandler)
	if err := json.Unmarshal(projects.Body.Bytes(), &projectsPayload); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, project := range projectsPayload.Projects {
		counts[project.Name] = project.Count
	}
	if len(projectsPayload.Projects) != 2 || counts["Growth"] != 0 || counts["Nordic launch"] != 0 {
		t.Fatalf("projects after untag=%+v", projectsPayload.Projects)
	}

	// Validation: overlong names, unknown artifacts, non-deliverables.
	if code, _ = tagProject(t, cookies, doc.ID, strings.Repeat("x", 61)); code != http.StatusBadRequest {
		t.Fatalf("overlong project status=%d", code)
	}
	if code, _ = tagProject(t, cookies, "os-artifact-nope", "Growth"); code != http.StatusNotFound {
		t.Fatalf("unknown artifact status=%d", code)
	}
	stage, _, err := kanbanApp.createOSArtifactWithMetadata("workflow", "stage", "stage body", "Scout", map[string]string{"source": "process_stage", "processStage": "write", "goalParentId": "parent-x"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ = tagProject(t, cookies, stage.ID, "Growth"); code != http.StatusNotFound {
		t.Fatalf("stage artifact tag status=%d, want 404", code)
	}
	overlong := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?project="+strings.Repeat("x", 70), "", cookies, studioProjectsHandler)
	if overlong.Code != http.StatusBadRequest {
		t.Fatalf("overlong filter status=%d", overlong.Code)
	}
}

func TestArtifactProjectTagOnResearchAndStoryOutlines(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)
	report, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Nordic mid-market", "# Nordic mid-market\n\n## Executive Summary\n\nBig.", "Scout", map[string]string{
		"source": "scout_thread", "mode": "research", "threadId": "agent-thread-research-1", "artifactContract": "research_brief_v2",
		"status": artifactStatusComplete, "threadStatus": artifactStatusComplete, "requestedBy": aj.Email, "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	if kind, ok := studioLegacyProjectCandidate(report); !ok || kind != studioProjectKindResearch {
		t.Fatalf("fixture: report classified as %q ok=%v", kind, ok)
	}
	code, payload := tagProject(t, cookies, report.ID, "Nordics")
	if code != http.StatusOK || payload["saved"] != true {
		t.Fatalf("research tag status=%d payload=%v", code, payload)
	}
	_, children := projectFolderIDs(t)
	_, assignments := sharedFileFolderStore().snapshot()
	if assignments[report.ID] != children["Nordics"] {
		t.Fatalf("research report filed under %q, want %q", assignments[report.ID], children["Nordics"])
	}
	story, _, err := kanbanApp.createPackagingStory(aj, packagingStoryBrief{Subject: "Nordic story"})
	if err != nil {
		t.Fatal(err)
	}
	code, payload = tagProject(t, cookies, story.ID, "Nordics")
	if code != http.StatusOK || payload["project"] != "Nordics" || payload["saved"] != false {
		t.Fatalf("story tag status=%d payload=%v", code, payload)
	}
	get := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/packaging/stories/"+story.ID, "", cookies, packagingStoryHandler)
	if !strings.Contains(get.Body.String(), `"project":"Nordics"`) {
		t.Fatalf("story view lacks the project: %s", get.Body.String())
	}
	projects := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/projects", "", cookies, assistantProjectsHandler)
	if !strings.Contains(projects.Body.String(), fmt.Sprintf(`"name":"Nordics","count":2,"folderId":%q`, children["Nordics"])) {
		t.Fatalf("projects=%s", projects.Body.String())
	}
}

func TestListArtifactProjectsFencesNamesToReadableProjects(t *testing.T) {
	ajCookies, aj := setupPackagingStudioTest(t)
	joelCookies := loginAs(t, "joel@shareability.com", "B0NFIRE!")
	joel := accountStore().findUser("joel@shareability.com")
	// AJ tags a private document into a codename project only AJ can read.
	private, err := createDocumentStudioArtifact(aj, "Deal memo", "# Deal memo", map[string]string{"visibility": "private"})
	if err != nil {
		t.Fatal(err)
	}
	if code, payload := tagProject(t, ajCookies, private.ID, "Project Falcon"); code != http.StatusOK {
		t.Fatalf("tag status=%d payload=%v", code, payload)
	}
	// AJ also tags an organization document into a shared project.
	shared, err := createDocumentStudioArtifact(aj, "Team plan", "# Team plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := tagProject(t, ajCookies, shared.ID, "Launch"); code != http.StatusOK {
		t.Fatal("shared tag failed")
	}
	names := func(views []artifactProjectView) string {
		out := make([]string, 0, len(views))
		for _, view := range views {
			out = append(out, fmt.Sprintf("%s:%d", view.Name, view.Count))
		}
		return strings.Join(out, ",")
	}
	if got := names(kanbanApp.listArtifactProjects(context.Background(), aj)); got != "Launch:1,Project Falcon:1" {
		t.Fatalf("AJ projects=%s", got)
	}
	// Joel reads the shared deliverable but not the private one: the codename
	// never appears, while the readable project does with its readable count.
	if got := names(kanbanApp.listArtifactProjects(context.Background(), joel)); got != "Launch:1" {
		t.Fatalf("Joel projects=%s", got)
	}
	joelList := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/projects", "", joelCookies, assistantProjectsHandler)
	if strings.Contains(joelList.Body.String(), "Falcon") {
		t.Fatalf("codename leaked to Joel: %s", joelList.Body.String())
	}
	// The studio filter refuses names outside Joel's set with 404, and still
	// answers AJ.
	if filtered := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?project=Project%20Falcon", "", joelCookies, studioProjectsHandler); filtered.Code != http.StatusNotFound {
		t.Fatalf("Joel filter status=%d body=%s, want 404", filtered.Code, filtered.Body.String())
	}
	if filtered := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?project=project%20falcon", "", ajCookies, studioProjectsHandler); filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), private.ID) {
		t.Fatalf("AJ filter status=%d body=%s", filtered.Code, filtered.Body.String())
	}
	if filtered := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1?project=Launch", "", joelCookies, studioProjectsHandler); filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), shared.ID) {
		t.Fatalf("Joel readable filter status=%d body=%s", filtered.Code, filtered.Body.String())
	}
	// A project Joel created himself stays visible to him even when its only
	// deliverable becomes unreadable (AJ untagged his shared doc into it).
	// Organization-visible on purpose: a PRIVATE deliverable no longer mints a
	// Projects/<name> folder (the codename would be listed to every member by
	// GET /assistant/files), and this case pins the folder-derived rule that a
	// project you created stays yours after its last deliverable leaves it.
	joelDoc, err := createDocumentStudioArtifact(joel, "Joel plan", "# Joel plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := tagProject(t, joelCookies, joelDoc.ID, "Joel Corner"); code != http.StatusOK {
		t.Fatal("Joel tag failed")
	}
	if code, _ := tagProject(t, joelCookies, joelDoc.ID, ""); code != http.StatusOK {
		t.Fatal("Joel untag failed")
	}
	if got := names(kanbanApp.listArtifactProjects(context.Background(), joel)); got != "Joel Corner:0,Launch:1" {
		t.Fatalf("Joel projects after untag=%s", got)
	}
	if got := names(kanbanApp.listArtifactProjects(context.Background(), aj)); strings.Contains(got, "Joel Corner") {
		t.Fatalf("Joel's empty project leaked to AJ: %s", got)
	}
}

// TestArtifactProjectTagFencesPrivateCodenamesAndOtherMembersFolders pins the
// two ways a project tag touches Drive, both of which are fences the Drive
// doors already enforce and the tag door must not route around:
//
//   - GET /assistant/files returns the folder tree with no per-viewer filter,
//     so minting `Projects/<codename>` for a PRIVATE deliverable would publish
//     the very name /assistant/projects and the studio `?project=` filter hide.
//   - POST /assistant/files/save refuses a folder the caller does not manage,
//     so a tag must not file a caller's row into another member's folder.
//
// Neither fence fails the tag: a project is a name on the artifact.
func TestArtifactProjectTagFencesPrivateCodenamesAndOtherMembersFolders(t *testing.T) {
	ajCookies, aj := setupPackagingStudioTest(t)
	joelCookies := loginAs(t, "joel@shareability.com", "B0NFIRE!")
	joel := accountStore().findUser("joel@shareability.com")
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	tim := accountStore().findUser("tim@shareability.com")
	if joel == nil || tim == nil {
		t.Fatal("seed members missing")
	}

	// 1. A private deliverable's codename never becomes a Drive folder.
	secret, err := createDocumentStudioArtifact(aj, "Deal memo", "# Deal memo", map[string]string{"visibility": "private"})
	if err != nil {
		t.Fatal(err)
	}
	code, payload := tagProject(t, ajCookies, secret.ID, "Chimera")
	if code != http.StatusOK || payload["project"] != "Chimera" || payload["folderId"] != "" || payload["saved"] != false || payload["moved"] != false {
		t.Fatalf("private tag status=%d payload=%v", code, payload)
	}
	if folder, ok := projectFolderByName("Chimera"); ok {
		t.Fatalf("a private codename minted the Drive folder %q", folder.ID)
	}
	tagged, _ := kanbanApp.osArtifactByID(secret.ID)
	if tagged.Metadata[artifactProjectMetadataKey] != "Chimera" || tagged.Metadata[artifactProjectFolderIDMetadataKey] != "" {
		t.Fatalf("private tag metadata=%v", tagged.Metadata)
	}
	// The tag still names the project for the one viewer who may know it.
	if !kanbanApp.artifactProjectVisibleToViewer(context.Background(), aj, "Chimera") {
		t.Fatal("the tagger lost their own project")
	}
	drive := artifactAuthorizationRequest(t, http.MethodGet, "/assistant/files", "", joelCookies, assistantFilesHandler)
	if drive.Code != http.StatusOK {
		t.Fatalf("Joel Drive status=%d body=%s", drive.Code, drive.Body.String())
	}
	if strings.Contains(drive.Body.String(), "Chimera") {
		t.Fatalf("the codename leaked into another member's Drive folder tree: %s", drive.Body.String())
	}

	// 2. A tag never files a row into another member's project folder. Joel
	// creates Projects/Northstar; Tim tags his own deliverable with the same
	// name (neither is the approval admin, so neither manages the other's
	// folders).
	joelDoc, err := createDocumentStudioArtifact(joel, "Joel plan", "# Joel plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, payload = tagProject(t, joelCookies, joelDoc.ID, "Northstar"); code != http.StatusOK || payload["saved"] != true {
		t.Fatalf("Joel tag status=%d payload=%v", code, payload)
	}
	_, children := projectFolderIDs(t)
	joelFolder := children["Northstar"]
	if joelFolder == "" {
		t.Fatalf("Projects/Northstar missing: %v", children)
	}
	if fileFolderWritableFromContext(context.Background(), tim, joelFolder) {
		t.Fatal("fixture: Tim already manages Joel's folder, the fence cannot be observed")
	}
	timDoc, err := createDocumentStudioArtifact(tim, "Tim plan", "# Tim plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	code, payload = tagProject(t, timCookies, timDoc.ID, "Northstar")
	if code != http.StatusOK || payload["project"] != "Northstar" {
		t.Fatalf("Tim tag status=%d payload=%v", code, payload)
	}
	if payload["folderId"] != "" || payload["saved"] != false || payload["moved"] != false {
		t.Fatalf("Tim's tag wrote into Joel's folder: %v", payload)
	}
	timTagged, _ := kanbanApp.osArtifactByID(timDoc.ID)
	if timTagged.Metadata[artifactProjectMetadataKey] != "Northstar" || timTagged.Metadata[artifactProjectFolderIDMetadataKey] != "" {
		t.Fatalf("Tim tag metadata=%v", timTagged.Metadata)
	}
	_, assignments := sharedFileFolderStore().snapshot()
	if assignments[timDoc.ID] == joelFolder {
		t.Fatal("a project tag filed one member's Drive row into another member's folder")
	}
	if assignments[joelDoc.ID] != joelFolder {
		t.Fatalf("Joel's own tag stopped filing: %q", assignments[joelDoc.ID])
	}
	// Tim keeps the project in his own list: the tag is the record, the
	// folder is only Drive's copy of it.
	if !kanbanApp.artifactProjectVisibleToViewer(context.Background(), tim, "Northstar") {
		t.Fatal("Tim lost the project his own deliverable is tagged with")
	}
}

// TestProjectFolderByNameOrCreateSurvivesConcurrentFirstUse pins the race two
// people tagging into the same brand-new project hit: every caller lists,
// sees nothing, and creates. Only one create can win — the loser must
// re-resolve the winner's folder instead of failing the tag with
// errFileFolderDuplicate.
func TestProjectFolderByNameOrCreateSurvivesConcurrentFirstUse(t *testing.T) {
	_, aj := setupPackagingStudioTest(t)
	const racers = 8
	var wait sync.WaitGroup
	folders := make([]fileFolderRecord, racers)
	names := make([]string, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	for index := 0; index < racers; index++ {
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			<-start
			folders[slot], names[slot], errs[slot] = projectFolderByNameOrCreate("Everest", normalizeAccountEmail(aj.Email), true)
		}(index)
	}
	close(start)
	wait.Wait()
	for index := 0; index < racers; index++ {
		if errs[index] != nil {
			t.Fatalf("racer %d failed the tag: %v", index, errs[index])
		}
		if names[index] != "Everest" || folders[index].ID == "" || folders[index].ID != folders[0].ID {
			t.Fatalf("racer %d resolved folder %q/%q, want the one folder %q", index, folders[index].ID, names[index], folders[0].ID)
		}
	}
	roots := 0
	everest := 0
	for _, folder := range listFileFolders() {
		if strings.TrimSpace(folder.ParentID) == "" && folder.Name == artifactProjectsRootFolderName {
			roots++
		}
		if strings.EqualFold(folder.Name, "Everest") {
			everest++
		}
	}
	if roots != 1 || everest != 1 {
		t.Fatalf("concurrent first use made %d Projects roots and %d Everest folders", roots, everest)
	}

	// Deterministically LOSE both creates: the probe fires between the lookup
	// and the create, exactly where the other tagger's folder appears. Both
	// levels must re-resolve the winner's folder rather than surface
	// errFileFolderDuplicate as a failed tag.
	previous := projectFolderCreateRaceProbe
	t.Cleanup(func() { projectFolderCreateRaceProbe = previous })
	if err := sharedFileFolderStore().remove(folders[0].ID); err != nil {
		t.Fatal(err)
	}
	root, ok := topLevelFileFolderByName(artifactProjectsRootFolderName)
	if !ok {
		t.Fatal("Projects root missing")
	}
	if err := sharedFileFolderStore().remove(root.ID); err != nil {
		t.Fatal(err)
	}
	raced := map[string]bool{}
	projectFolderCreateRaceProbe = func(parentID string, name string) {
		if raced[name] {
			return
		}
		raced[name] = true
		if _, err := sharedFileFolderStore().createInParent(name, parentID, "rival@shareability.com"); err != nil {
			t.Errorf("rival create of %q failed: %v", name, err)
		}
	}
	folder, name, err := projectFolderByNameOrCreate("K2", normalizeAccountEmail(aj.Email), true)
	if err != nil {
		t.Fatalf("a lost first-use race failed the tag: %v", err)
	}
	if !raced[artifactProjectsRootFolderName] || !raced["K2"] {
		t.Fatalf("the race probe never fired at both levels: %v", raced)
	}
	rival, ok := projectFolderByName("K2")
	if !ok || folder.ID != rival.ID || name != "K2" {
		t.Fatalf("lost race resolved %q/%q, want the rival's folder %q", folder.ID, name, rival.ID)
	}
}
