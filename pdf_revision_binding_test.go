package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestArtifactExportPDFBindsAuthorizedSourceRevision(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	queueDir := setupRenderSidecarEnv(t)
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "revision-runner"); err != nil {
		t.Fatal(err)
	}
	deck := seedShareArtifact(t, "draft", "<!doctype html><html><body>bound deck</body></html>", map[string]string{"type": artifactTypeHTMLDeck})

	stale := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, deck.ID, artifactVersion(deck)+1), member)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale request status=%d body=%s, want 409", stale.Code, stale.Body.String())
	}
	if entries, err := os.ReadDir(queueDir); (err != nil && !os.IsNotExist(err)) || len(entries) != 0 {
		t.Fatalf("stale request queued work: entries=%d err=%v", len(entries), err)
	}

	accepted := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, deck.ID, artifactVersion(deck)), member)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("bound request status=%d body=%s, want 202", accepted.Code, accepted.Body.String())
	}
	fresh, _ := kanbanApp.osArtifactByID(deck.ID)
	if fresh.Metadata[renderSourceArtifactVersionMetadataKey] != strconv.Itoa(artifactVersion(deck)) ||
		fresh.Metadata[renderSourceSceneRefMetadataKey] != "" || strings.TrimSpace(fresh.Metadata["renderJobId"]) == "" {
		t.Fatalf("queued source binding=%v", fresh.Metadata)
	}

	// Native scene identity is independently optimistic: even a metadata-only
	// scene switch cannot be exported through a stale editor request.
	const sceneA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const sceneB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	native := seedShareArtifact(t, "draft", "<!doctype html><html><body>native</body></html>", map[string]string{
		"type": artifactTypeHTMLDeck, deckSceneRefMetadataKey: sceneA,
	})
	sceneConflict := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d,"sceneRef":%q}`, native.ID, artifactVersion(native), sceneB), member)
	if sceneConflict.Code != http.StatusConflict {
		t.Fatalf("scene conflict status=%d body=%s, want 409", sceneConflict.Code, sceneConflict.Body.String())
	}
}

func TestRenderCallbackDiscardsChangedArtifactWithoutAttaching(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		seed   map[string]string
		mutate func(*testing.T, meetingMemoryEntry)
	}{
		{
			name: "body revision",
			seed: map[string]string{"type": artifactTypeHTMLDeck},
			mutate: func(t *testing.T, artifact meetingMemoryEntry) {
				t.Helper()
				if _, changed, err := kanbanApp.memory.updateOSArtifactWithMetadata(artifact.ID, "", "<!doctype html><html><body>edited while rendering</body></html>", "AJ", nil); err != nil || !changed {
					t.Fatalf("edit artifact: changed=%v err=%v", changed, err)
				}
			},
		},
		{
			name: "native scene",
			seed: map[string]string{"type": artifactTypeHTMLDeck, deckSceneRefMetadataKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			mutate: func(t *testing.T, artifact meetingMemoryEntry) {
				t.Helper()
				if _, changed, err := kanbanApp.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{
					deckSceneRefMetadataKey: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				}); err != nil || !changed {
					t.Fatalf("switch scene: changed=%v err=%v", changed, err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			shareLinkTestEnv(t)
			t.Setenv("BONFIRE_RUNNER_TOKEN", "render-secret")
			artifact := seedShareArtifact(t, "draft", "<!doctype html><html><body>source</body></html>", testCase.seed)
			if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(artifact.ID, queuedRenderMetadata(artifact, "render-stale", renderJobKindDeck)); err != nil {
				t.Fatal(err)
			}
			testCase.mutate(t, artifact)

			complete := renderRunnerCallbackPayload{
				JobID: "render-stale", ArtifactID: artifact.ID, Status: renderJobStatusComplete,
				PDFBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 stale")), Flattened: true, PageCount: 1,
			}
			recorder := renderCallbackRequest(t, "render-secret", complete)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"stale":true`) {
				t.Fatalf("stale callback status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			fresh, _ := kanbanApp.osArtifactByID(artifact.ID)
			if len(artifactAssets(fresh)) != 0 || fresh.Metadata["renderStatus"] != renderJobStatusStale || fresh.Metadata["renderJobId"] != "" || fresh.Metadata[renderPDFAssetRefMetadataKey] != "" {
				t.Fatalf("stale callback mutated deliverable: version=%d metadata=%v assets=%v", artifactVersion(fresh), fresh.Metadata, artifactAssets(fresh))
			}
			for _, entry := range kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
				if signal, ok := decodeSignalEntry(entry); ok && signal.Event == signalEventPDFExported && signal.ArtifactID == artifact.ID {
					t.Fatal("stale callback recorded a pdf_exported signal")
				}
			}
		})
	}
}

func TestFrontendPDFSelectionIsRevisionBound(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	selection := functionBody(html, "function artifactNewestPdfAsset(entry)")
	for _, want := range []string{"renderPdfArtifactVersion", "renderPdfSourceSceneRef", "renderPdfAssetRef", "currentVersion === 1", "if (pending) return null"} {
		if !strings.Contains(selection, want) {
			t.Errorf("artifactNewestPdfAsset missing %q", want)
		}
	}
	waiter := functionBody(html, "async function waitForArtifactPdfAsset(artifactId, tries = 40)")
	if !strings.Contains(waiter, "renderStatus === 'failed' || renderStatus === 'stale'") {
		t.Fatal("PDF waiter does not fail fast for a stale source render")
	}
	if !strings.Contains(waiter, "fetchArtifactEntryById(artifactId, { refresh: true })") {
		t.Fatal("PDF waiter does not poll an older or cached artifact by exact id")
	}
}
