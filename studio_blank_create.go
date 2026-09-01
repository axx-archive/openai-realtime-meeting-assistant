package main

// studio_blank_create.go gives the Work launcher a real "New" path. The
// studios historically only opened artifacts that a Scout thread or a copy
// had already created; these handlers mint a blank, immediately-editable
// artifact owned by the signed-in account so "New document" / "New
// presentation" open an editor instead of detouring through a chat brief.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const (
	studioBlankSourceDocument = "document_studio"
	studioBlankSourceDeck     = "deck_studio"
)

func studioBlankCreateUser(w http.ResponseWriter, r *http.Request) *userAccount {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return nil
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return nil
	}
	return user
}

func studioBlankCreateTitle(w http.ResponseWriter, r *http.Request, fallback string) (string, bool) {
	payload := struct {
		Title string `json:"title"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read the create request")
		return "", false
	}
	title := strings.Join(strings.Fields(strings.TrimSpace(payload.Title)), " ")
	if len([]rune(title)) > 160 {
		writeAuthError(w, http.StatusBadRequest, "the title is too long")
		return "", false
	}
	return firstNonEmptyString(title, fallback), true
}

func studioBlankBaseMetadata(user *userAccount, title, source, mode string) map[string]string {
	return map[string]string{
		"title":        title,
		"source":       source,
		"mode":         mode,
		"status":       artifactStatusComplete,
		"threadStatus": artifactStatusComplete,
		"visibility":   "organization",
		"ownerEmail":   normalizeAccountEmail(user.Email),
	}
}

// documentEditorNewHandler POST /artifacts/document/new — a blank Document
// Studio document, editable immediately.
func documentEditorNewHandler(w http.ResponseWriter, r *http.Request) {
	user := studioBlankCreateUser(w, r)
	if user == nil {
		return
	}
	title, ok := studioBlankCreateTitle(w, r, "Untitled document")
	if !ok {
		return
	}
	storedBody, emptyMarker := documentStudioStoredBody("")
	metadata := studioBlankBaseMetadata(user, title, studioBlankSourceDocument, "document")
	metadata["type"] = artifactTypeMarkdown
	metadata["documentSchemaVersion"] = "1"
	metadata[documentStudioEmptyMetadataKey] = emptyMarker
	actor := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
	entry, appended, err := kanbanApp.createOSArtifactWithMetadata("artifacts", title, storedBody, actor, metadata)
	if err != nil || !appended || strings.TrimSpace(entry.ID) == "" {
		log.Errorf("Blank document create failed: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "the document could not be created")
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "artifact": documentStudioView(entry),
		"document": documentStudioDocumentFromEntry(entry),
	})
}

// deckEditorNewHandler POST /artifacts/deck/new — a one-slide native deck,
// editable immediately in Deck Studio.
func deckEditorNewHandler(w http.ResponseWriter, r *http.Request) {
	user := studioBlankCreateUser(w, r)
	if user == nil {
		return
	}
	title, ok := studioBlankCreateTitle(w, r, "Untitled presentation")
	if !ok {
		return
	}
	deck := deckDocument{
		SchemaVersion: deckDocumentSchemaVersion,
		Width:         deckDocumentWidth,
		Height:        deckDocumentHeight,
		Slides: []deckSlide{{
			ID: "slide-1", Background: "#101014",
			Elements: []deckElement{defaultDeckTextElement("text-1", title, 120, 120, 1680, 240, 84, 700)},
		}},
	}
	raw, err := json.Marshal(deck)
	if err != nil || len(raw) > deckDocumentMaxBytes {
		writeAuthError(w, http.StatusInternalServerError, "the presentation could not be created")
		return
	}
	ref, err := putBlob(raw, "application/vnd.bonfire.deck+json")
	if err != nil {
		log.Errorf("Blank deck scene store failed: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "the presentation could not be created")
		return
	}
	metadata := studioBlankBaseMetadata(user, title, studioBlankSourceDeck, "presentation")
	metadata["type"] = artifactTypeHTMLDeck
	metadata[deckSceneRefMetadataKey] = ref
	metadata[deckSchemaMetadataKey] = strconv.Itoa(deckDocumentSchemaVersion)
	actor := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
	entry, appended, err := kanbanApp.createOSArtifactWithMetadata("artifacts", title, compileDeckDocumentHTML(deck, title), actor, metadata)
	if err != nil || !appended || strings.TrimSpace(entry.ID) == "" {
		if err == nil {
			err = fmt.Errorf("blank deck was not appended")
		}
		log.Errorf("Blank deck create failed: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "the presentation could not be created")
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "artifact": deckArtifactViewFromEntry(entry), "deck": deck,
	})
}
