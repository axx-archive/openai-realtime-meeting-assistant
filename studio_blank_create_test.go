package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The Work launcher's "New" buttons mint blank, immediately-editable studio
// artifacts. These pins hold the whole contract: the routes create, the
// Studio classifier recognizes the studio-native sources, and the projection
// lists them as ready, editable work.
func TestStudioBlankCreatesAreEditableReadyProjects(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })

	owner := "aj@shareability.com"
	cookies := loginAs(t, owner, "B0NFIRE!")

	docResponse := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/new", `{}`, cookies, documentEditorNewHandler)
	if docResponse.Code != http.StatusCreated {
		t.Fatalf("blank document create returned %d: %s", docResponse.Code, docResponse.Body.String())
	}
	docPayload := struct {
		Artifact documentStudioArtifactView `json:"artifact"`
		Document documentStudioDocument     `json:"document"`
	}{}
	if err := json.Unmarshal(docResponse.Body.Bytes(), &docPayload); err != nil {
		t.Fatal(err)
	}
	if docPayload.Artifact.ID == "" || docPayload.Artifact.Title != "Untitled document" || docPayload.Document.Markdown != "" {
		t.Fatalf("blank document shape wrong: %+v", docPayload)
	}

	deckResponse := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/deck/new", `{"title":"Board update"}`, cookies, deckEditorNewHandler)
	if deckResponse.Code != http.StatusCreated {
		t.Fatalf("blank deck create returned %d: %s", deckResponse.Code, deckResponse.Body.String())
	}
	deckPayload := struct {
		Artifact struct {
			ID string `json:"id"`
		} `json:"artifact"`
		Deck deckDocument `json:"deck"`
	}{}
	if err := json.Unmarshal(deckResponse.Body.Bytes(), &deckPayload); err != nil {
		t.Fatal(err)
	}
	if deckPayload.Artifact.ID == "" || len(deckPayload.Deck.Slides) != 1 || deckPayload.Deck.Width != deckDocumentWidth {
		t.Fatalf("blank deck shape wrong: %+v", deckPayload)
	}

	docEntry, found := kanbanApp.osArtifactByID(docPayload.Artifact.ID)
	if !found {
		t.Fatal("blank document not stored")
	}
	if kind, ok := studioLegacyProjectCandidate(docEntry); !ok || kind != studioProjectKindDocument {
		t.Fatalf("blank document not classified as a document project: kind=%q ok=%v", kind, ok)
	}
	deckEntry, found := kanbanApp.osArtifactByID(deckPayload.Artifact.ID)
	if !found {
		t.Fatal("blank deck not stored")
	}
	if kind, ok := studioLegacyProjectCandidate(deckEntry); !ok || kind != studioProjectKindPresentation {
		t.Fatalf("blank deck not classified as a presentation project: kind=%q ok=%v", kind, ok)
	}

	// The blank deck's scene must round-trip through the deck boundary.
	if deck, _, quality, err := loadDeckDocument(deckEntry); err != nil || quality != "native" || len(deck.Slides) != 1 {
		t.Fatalf("blank deck scene did not load natively: quality=%q err=%v", quality, err)
	}

	list := artifactAuthorizationRequest(t, http.MethodGet, "/api/studio-projects/v1", "", cookies, studioProjectsHandler)
	if list.Code != http.StatusOK {
		t.Fatalf("studio projects list returned %d: %s", list.Code, list.Body.String())
	}
	listPayload := struct {
		Projects []studioProjectView `json:"projects"`
	}{}
	if err := json.Unmarshal(list.Body.Bytes(), &listPayload); err != nil {
		t.Fatal(err)
	}
	byID := map[string]studioProjectView{}
	for _, project := range listPayload.Projects {
		byID[project.ID] = project
	}
	for id, wantKind := range map[string]string{
		docPayload.Artifact.ID:  studioProjectKindDocument,
		deckPayload.Artifact.ID: studioProjectKindPresentation,
	} {
		project, ok := byID[id]
		if !ok {
			t.Fatalf("blank %s missing from studio projects list", wantKind)
		}
		if project.Kind != wantKind || project.Status != studioProjectStatusReady {
			t.Fatalf("blank %s projected wrong: kind=%q status=%q", wantKind, project.Kind, project.Status)
		}
		if project.Result == nil || !project.Result.CanEdit {
			t.Fatalf("blank %s is not editable in projection: %+v", wantKind, project.Result)
		}
	}

	// Overlong titles are refused, empty bodies are not.
	tooLong := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/new", `{"title":"`+strings.Repeat("x", 200)+`"}`, cookies, documentEditorNewHandler)
	if tooLong.Code != http.StatusBadRequest {
		t.Fatalf("161+ rune title should be rejected, got %d", tooLong.Code)
	}
}
