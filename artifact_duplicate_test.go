package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func duplicateArtifact(t *testing.T, cookies []*http.Cookie, body string) (int, map[string]any) {
	t.Helper()
	response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/duplicate", body, cookies, artifactDuplicateHandler)
	payload := map[string]any{}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	return response.Code, payload
}

func duplicateArtifactID(t *testing.T, payload map[string]any) string {
	t.Helper()
	artifact, _ := payload["artifact"].(map[string]any)
	id, _ := artifact["id"].(string)
	if id == "" {
		t.Fatalf("duplicate payload lacks an artifact id: %v", payload)
	}
	return id
}

func TestArtifactDuplicateCopiesDecksDocumentsAndResearchWithTheSameFence(t *testing.T) {
	cookies, aj := setupPackagingStudioTest(t)

	// Deck: the native copy path, "Copy of …", filed to Drive.
	deck, _, err := kanbanApp.createOSArtifactWithMetadata("design", "Like a Farmer", faithfulDeckHTML, "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "visibility": "organization", "requestedBy": aj.Email, "source": "scout_thread", "threadId": "agent-thread-design-1", "mode": "presentation",
		"status": artifactStatusComplete, "threadStatus": artifactStatusComplete,
	})
	if err != nil {
		t.Fatal(err)
	}
	code, payload := duplicateArtifact(t, cookies, `{"artifactId":"`+deck.ID+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("deck duplicate status=%d payload=%v", code, payload)
	}
	deckCopy, _ := kanbanApp.osArtifactByID(duplicateArtifactID(t, payload))
	if deckCopy.Metadata["title"] != "Copy of Like a Farmer" || artifactType(deckCopy) != artifactTypeHTMLDeck || deckCopy.Metadata["copiedFromArtifactId"] != deck.ID || !strings.EqualFold(deckCopy.Metadata["savedToFiles"], "true") || deckCopy.Metadata["visibility"] != "organization" {
		t.Fatalf("deck copy metadata=%v", deckCopy.Metadata)
	}
	if kind, ok := studioLegacyProjectCandidate(deckCopy); !ok || kind != studioProjectKindPresentation {
		t.Fatalf("deck copy classified as %q ok=%v", kind, ok)
	}
	if copyDeck, _, quality, loadErr := loadDeckDocument(deckCopy); loadErr != nil || quality != "native" || len(copyDeck.Slides) != 1 {
		t.Fatalf("deck copy scene quality=%q err=%v", quality, loadErr)
	}

	// Document: body and images carried, editable in the document editor.
	doc, err := createDocumentStudioArtifact(aj, "Board memo", "# Board memo\n\nThe short version.", nil)
	if err != nil {
		t.Fatal(err)
	}
	code, payload = duplicateArtifact(t, cookies, `{"artifactId":"`+doc.ID+`","title":"Board memo v2","fileName":"Board memo v2"}`)
	if code != http.StatusCreated {
		t.Fatalf("document duplicate status=%d payload=%v", code, payload)
	}
	docCopy, _ := kanbanApp.osArtifactByID(duplicateArtifactID(t, payload))
	if docCopy.Metadata["title"] != "Board memo v2" || docCopy.Text != doc.Text || docCopy.Metadata["copiedFromArtifactId"] != doc.ID || docCopy.Metadata["driveFileName"] != "Board memo v2" {
		t.Fatalf("document copy=%v text=%q", docCopy.Metadata, docCopy.Text)
	}
	if kind, ok := studioLegacyProjectCandidate(docCopy); !ok || kind != studioProjectKindDocument {
		t.Fatalf("document copy classified as %q ok=%v", kind, ok)
	}

	// Research report: duplicates as a research report (contract + mode kept)
	// so the copy still opens branded and stays a Research row.
	report, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Nordic mid-market", "# Nordic mid-market\n\n## Executive Summary\n\nBig.", "Scout", map[string]string{
		"source": "scout_thread", "mode": "research", "threadId": "agent-thread-research-1", "artifactContract": "research_brief_v2",
		"status": artifactStatusComplete, "threadStatus": artifactStatusComplete, "requestedBy": aj.Email, "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, payload = duplicateArtifact(t, cookies, `{"artifactId":"`+report.ID+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("research duplicate status=%d payload=%v", code, payload)
	}
	reportCopy, _ := kanbanApp.osArtifactByID(duplicateArtifactID(t, payload))
	if reportCopy.Metadata["title"] != "Copy of Nordic mid-market" || reportCopy.Metadata["artifactContract"] != "research_brief_v2" || reportCopy.Metadata["mode"] != "research" || reportCopy.Text != report.Text {
		t.Fatalf("research copy=%v", reportCopy.Metadata)
	}
	if kind, ok := studioLegacyProjectCandidate(reportCopy); !ok || kind != studioProjectKindResearch {
		t.Fatalf("research copy classified as %q ok=%v", kind, ok)
	}
	if artifact, _ := payload["artifact"].(map[string]any); artifact["kind"] != studioProjectKindResearch {
		t.Fatalf("duplicate view=%v", payload["artifact"])
	}
	if !strings.Contains(renderResearchReportPrintHTML(reportCopy), "Prepared by Scout for") {
		t.Fatal("research copy lost its branded export")
	}

	// Project tags ride along: the copy files into the same Projects folder.
	if code, tagPayload := tagProject(t, cookies, doc.ID, "Board"); code != http.StatusOK {
		t.Fatalf("tag status=%d payload=%v", code, tagPayload)
	}
	code, payload = duplicateArtifact(t, cookies, `{"artifactId":"`+doc.ID+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("tagged duplicate status=%d payload=%v", code, payload)
	}
	taggedCopy, _ := kanbanApp.osArtifactByID(duplicateArtifactID(t, payload))
	_, children := projectFolderIDs(t)
	_, assignments := sharedFileFolderStore().snapshot()
	if taggedCopy.Metadata[artifactProjectMetadataKey] != "Board" || assignments[taggedCopy.ID] != children["Board"] {
		t.Fatalf("tagged copy project=%q folder=%q want %q", taggedCopy.Metadata[artifactProjectMetadataKey], assignments[taggedCopy.ID], children["Board"])
	}

	// Refusals: unknown artifact, a non-duplicable kind, an overlong title,
	// and another member's private artifact all fail closed.
	if code, _ = duplicateArtifact(t, cookies, `{"artifactId":"os-artifact-nope"}`); code != http.StatusNotFound {
		t.Fatalf("unknown duplicate status=%d", code)
	}
	if code, _ = duplicateArtifact(t, cookies, `{"artifactId":"`+doc.ID+`","title":"`+strings.Repeat("x", 200)+`"}`); code != http.StatusBadRequest {
		t.Fatalf("overlong title status=%d", code)
	}
	image, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "photo", "![photo](data:image/png;base64,AA==)", "Scout", map[string]string{"type": artifactTypeImage, "source": "chat_image", "status": artifactStatusComplete})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ = duplicateArtifact(t, cookies, `{"artifactId":"`+image.ID+`"}`); code != http.StatusNotFound {
		t.Fatalf("image duplicate status=%d, want 404", code)
	}
	joel := accountStore().findUser("joel@shareability.com")
	private, err := createDocumentStudioArtifact(joel, "Joel private", "secret", map[string]string{"visibility": "private"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ = duplicateArtifact(t, cookies, `{"artifactId":"`+private.ID+`"}`); code != http.StatusNotFound {
		t.Fatalf("private duplicate status=%d, want 404", code)
	}
}

// TestArtifactDuplicateNeverFilesIntoAnotherMembersProjectFolder pins the
// check-then-substitute hole: the writability gate ran on the caller-supplied
// folderId ("" — always writable) and the handler then swapped in the source
// artifact's project folder, which /assistant/files/save would have refused
// the same caller. The substitution now happens first and an inherited folder
// the caller may not write falls back to the Drive root.
func TestArtifactDuplicateNeverFilesIntoAnotherMembersProjectFolder(t *testing.T) {
	setupPackagingStudioTest(t)
	joelCookies := loginAs(t, "joel@shareability.com", "B0NFIRE!")
	joel := accountStore().findUser("joel@shareability.com")
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	tim := accountStore().findUser("tim@shareability.com")
	if joel == nil || tim == nil {
		t.Fatal("seed members missing")
	}
	// Joel tags an organization-visible deliverable, so Projects/Northstar is
	// Joel's folder and the artifact carries its id.
	shared, err := createDocumentStudioArtifact(joel, "Northstar plan", "# Northstar plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, payload := tagProject(t, joelCookies, shared.ID, "Northstar"); code != http.StatusOK || payload["saved"] != true {
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

	// Tim may read and copy the organization-visible deliverable, but his copy
	// must not land in Joel's folder — and must not carry its id forward.
	code, payload := duplicateArtifact(t, timCookies, `{"artifactId":"`+shared.ID+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("Tim duplicate status=%d payload=%v", code, payload)
	}
	timCopy, _ := kanbanApp.osArtifactByID(duplicateArtifactID(t, payload))
	_, assignments := sharedFileFolderStore().snapshot()
	if assignments[timCopy.ID] == joelFolder {
		t.Fatal("the duplicate door filed one member's copy into another member's project folder")
	}
	if assignments[timCopy.ID] != "" {
		t.Fatalf("the inherited folder should fall back to the Drive root, got %q", assignments[timCopy.ID])
	}
	if timCopy.Metadata[artifactProjectMetadataKey] != "Northstar" || timCopy.Metadata[artifactProjectFolderIDMetadataKey] != "" {
		t.Fatalf("copy inherited an unusable folder id: %v", timCopy.Metadata)
	}
	// Naming the same folder outright still fails closed, unchanged.
	if code, _ = duplicateArtifact(t, timCookies, `{"artifactId":"`+shared.ID+`","folderId":"`+joelFolder+`"}`); code != http.StatusNotFound {
		t.Fatalf("named unwritable folder status=%d, want 404", code)
	}
	// Joel's own duplicate still inherits his project folder.
	code, payload = duplicateArtifact(t, joelCookies, `{"artifactId":"`+shared.ID+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("Joel duplicate status=%d payload=%v", code, payload)
	}
	joelCopy, _ := kanbanApp.osArtifactByID(duplicateArtifactID(t, payload))
	_, assignments = sharedFileFolderStore().snapshot()
	if assignments[joelCopy.ID] != joelFolder || joelCopy.Metadata[artifactProjectFolderIDMetadataKey] != joelFolder {
		t.Fatalf("the owner's duplicate stopped inheriting its project folder: assignment=%q metadata=%v", assignments[joelCopy.ID], joelCopy.Metadata)
	}
}
