package main

// studio_versions.go — version history for Document Studio and Deck Studio.
// memory.go already journals every superseded revision (artifactVersions
// metadata: version, editor, timestamp, digest, body blob ref, and — since the
// studios kept it — the deck scene ref). This file only READS that journal:
// `GET /artifacts/{document,deck}?id=&version=N` reopens one prior revision
// read-only, and `GET /artifacts/{document,deck}/versions?id=` lists the
// history newest first. Restore is not a route: the client PATCHes the old
// body as a new revision with `restoredFrom: N`, which the editors stamp on
// the new revision so the rail can say "restored from v2".

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const studioVersionListLimit = 50

// studioVersionView is one row of the history rail.
type studioVersionView struct {
	Version      int    `json:"version"`
	At           string `json:"at,omitempty"`
	EditedBy     string `json:"editedBy,omitempty"`
	Digest       string `json:"digest,omitempty"`
	Source       string `json:"source"` // edit | restore
	RestoredFrom int    `json:"restoredFrom,omitempty"`
	Size         int64  `json:"size"`
	// Recoverable is false when the journal kept the version's facts but not
	// its body (records written before the blob seam, or an empty body).
	Recoverable bool `json:"recoverable"`
	Current     bool `json:"current"`
}

func studioRestoredFromVersion(metadata map[string]string) int {
	restored, err := strconv.Atoi(strings.TrimSpace(metadata[artifactRestoredFromMetadataKey]))
	if err != nil || restored < 1 {
		return 0
	}
	return restored
}

// studioRestoredFromMetadata validates an optional restoredFrom against the
// revision the PATCH is superseding and returns the metadata value to stamp.
// Zero means an ordinary save and clears any earlier stamp.
func studioRestoredFromMetadata(restoredFrom, expectedVersion int) (string, bool) {
	if restoredFrom == 0 {
		return "", true
	}
	if restoredFrom < 1 || restoredFrom > expectedVersion {
		return "", false
	}
	return strconv.Itoa(restoredFrom), true
}

func studioCurrentVersionView(entry meetingMemoryEntry) studioVersionView {
	view := studioVersionView{
		Version:     artifactVersion(entry),
		At:          strings.TrimSpace(entry.Metadata["updatedAt"]),
		EditedBy:    firstNonEmptyString(strings.TrimSpace(entry.Metadata["updatedBy"]), strings.TrimSpace(entry.Metadata["createdBy"])),
		Digest:      artifactCapabilityDigest(entry),
		Source:      "edit",
		Size:        int64(len(entry.Text)),
		Recoverable: true,
		Current:     true,
	}
	if view.At == "" && !entry.CreatedAt.IsZero() {
		view.At = entry.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if restored := studioRestoredFromVersion(entry.Metadata); restored > 0 {
		view.Source, view.RestoredFrom = "restore", restored
	}
	return view
}

func studioPriorVersionView(record artifactVersionRecord) studioVersionView {
	view := studioVersionView{Version: record.V, At: record.At, EditedBy: record.EditedBy, Digest: record.ContentDigest, Source: "edit", RestoredFrom: record.RestoredFrom}
	if record.RestoredFrom > 0 {
		view.Source = "restore"
	}
	if validBlobRef(record.BodyBlobRef) {
		if meta, err := blobStatForRef(record.BodyBlobRef); err == nil {
			view.Size, view.Recoverable = meta.Size, true
		}
	}
	return view
}

// studioVersionListResponse lists superseded revisions newest first (cap 50)
// beside the current revision's own facts.
func studioVersionListResponse(entry meetingMemoryEntry) map[string]any {
	records := artifactVersionHistory(entry)
	versions := make([]studioVersionView, 0, len(records))
	for index := len(records) - 1; index >= 0 && len(versions) < studioVersionListLimit; index-- {
		versions = append(versions, studioPriorVersionView(records[index]))
	}
	return map[string]any{
		"ok": true, "artifactId": entry.ID, "currentVersion": artifactVersion(entry),
		"current": studioCurrentVersionView(entry), "versions": versions,
	}
}

// studioVersionRecord finds one superseded version. The latest journal line
// wins if a restarted journal ever repeated a number.
func studioVersionRecord(entry meetingMemoryEntry, version int) (artifactVersionRecord, bool) {
	records := artifactVersionHistory(entry)
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].V == version {
			return records[index], true
		}
	}
	return artifactVersionRecord{}, false
}

func studioVersionBody(record artifactVersionRecord) (string, bool) {
	if !validBlobRef(record.BodyBlobRef) {
		return "", false
	}
	raw, _, err := getBlob(record.BodyBlobRef)
	if err != nil || !utf8.Valid(raw) {
		return "", false
	}
	return string(raw), true
}

func studioParseVersion(w http.ResponseWriter, raw string, noun string) (int, bool) {
	version, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || version < 1 {
		writeAuthError(w, http.StatusBadRequest, noun+" version must be a positive integer")
		return 0, false
	}
	return version, true
}

// documentEditorVersionGET answers GET /artifacts/document?id=&version=N for
// an already-authorized (ACLReadContent) document artifact. The response is a
// read-only snapshot: canWrite is always false, restore goes through PATCH.
func documentEditorVersionGET(w http.ResponseWriter, artifact meetingMemoryEntry, rawVersion string) {
	version, ok := studioParseVersion(w, rawVersion, "document")
	if !ok {
		return
	}
	current := artifactVersion(artifact)
	if version == current {
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok": true, "artifact": documentStudioView(artifact), "version": studioCurrentVersionView(artifact),
			"document": documentStudioDocumentFromEntry(artifact), "readOnly": true, "canWrite": false,
		})
		return
	}
	record, found := studioVersionRecord(artifact, version)
	if !found || version > current {
		writeAuthError(w, http.StatusNotFound, "document version not found")
		return
	}
	body, recoverable := studioVersionBody(record)
	if !recoverable {
		writeAuthError(w, http.StatusNotFound, "document version body is unavailable")
		return
	}
	markdown := body
	if markdown == documentStudioEmptyBodySentinel {
		markdown = ""
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "artifact": documentStudioView(artifact), "version": studioPriorVersionView(record),
		"document": documentStudioDocument{SchemaVersion: 1, Markdown: markdown}, "readOnly": true, "canWrite": false,
	})
}

// deckEditorVersionGET answers GET /artifacts/deck?id=&version=N. A native
// prior revision reopens from its journaled scene ref exactly; a record that
// predates scene journaling falls back to importing the prior HTML projection
// and says so through importQuality ("approximate" is never relabeled).
func deckEditorVersionGET(w http.ResponseWriter, artifact meetingMemoryEntry, rawVersion string) {
	version, ok := studioParseVersion(w, rawVersion, "deck")
	if !ok {
		return
	}
	current := artifactVersion(artifact)
	if version == current {
		deck, imported, quality, err := loadDeckDocument(artifact)
		if err != nil {
			writeAuthError(w, http.StatusConflict, "deck document is unavailable")
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok": true, "artifact": deckArtifactViewFromEntry(artifact), "version": studioCurrentVersionView(artifact),
			"deck": deck, "imported": imported, "importQuality": quality, "readOnly": true, "canWrite": false,
		})
		return
	}
	record, found := studioVersionRecord(artifact, version)
	if !found || version > current {
		writeAuthError(w, http.StatusNotFound, "deck version not found")
		return
	}
	allowedRefs := artifactAssetRefSet(artifact)
	if validBlobRef(record.SceneRef) {
		raw, _, err := getBlob(record.SceneRef)
		var deck deckDocument
		if err != nil || len(raw) > deckDocumentMaxBytes || strictJSONBytes(raw, &deck) != nil || validateDeckDocument(deck, allowedRefs) != nil {
			writeAuthError(w, http.StatusConflict, "deck version scene is unavailable")
			return
		}
		deck.Theme = resolveDeckTheme(deck.Theme)
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok": true, "artifact": deckArtifactViewFromEntry(artifact), "version": studioPriorVersionView(record),
			"deck": deck, "imported": false, "importQuality": "native", "readOnly": true, "canWrite": false,
		})
		return
	}
	body, recoverable := studioVersionBody(record)
	if !recoverable {
		writeAuthError(w, http.StatusNotFound, "deck version body is unavailable")
		return
	}
	snapshot := meetingMemoryEntry{ID: artifact.ID, Kind: artifact.Kind, Text: body, CreatedAt: artifact.CreatedAt, Metadata: artifact.Metadata}
	deck, quality := importLegacyDeckDocument(snapshot)
	if err := validateDeckDocument(deck, allowedRefs); err != nil {
		writeAuthError(w, http.StatusConflict, "deck version scene is unavailable")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "artifact": deckArtifactViewFromEntry(artifact), "version": studioPriorVersionView(record),
		"deck": deck, "imported": true, "importQuality": quality, "readOnly": true, "canWrite": false,
	})
}

func studioVersionsHandler(w http.ResponseWriter, r *http.Request, isStudioArtifact func(meetingMemoryEntry) bool, noun string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	artifact, ok := authorizedArtifactForActions(r.Context(), user, id, ACLReadContent)
	if !ok || !isStudioArtifact(artifact) {
		writeAuthError(w, http.StatusNotFound, noun+" artifact not found")
		return
	}
	writeAuthJSON(w, http.StatusOK, studioVersionListResponse(artifact))
}

// documentEditorVersionsHandler GET /artifacts/document/versions?id=
func documentEditorVersionsHandler(w http.ResponseWriter, r *http.Request) {
	studioVersionsHandler(w, r, artifactIsDocumentStudioDocument, "document")
}

// deckEditorVersionsHandler GET /artifacts/deck/versions?id=
func deckEditorVersionsHandler(w http.ResponseWriter, r *http.Request) {
	studioVersionsHandler(w, r, artifactIsDeckEditorDocument, "deck")
}
