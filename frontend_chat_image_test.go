package main

// Card 096 — the concept render's frontend contract. Grep-style pins (the
// frontend_router_test.go pattern) holding the client half: a Kind=image
// message renders the picture inline from a validated blob ref beside an
// "open artifact" action, the shimmer resolves on the image kind, the image
// proposal card confirms WITHOUT runGoalPipeline (the single-pass path), and
// filed image assets render inline in the data room.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForChatImage(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The image message render branch: a Kind=image message becomes the scout
// caption + inline picture, the ref is validated with the same 64-hex pattern
// artifactAssetEntries uses, the src is the session-gated /artifacts/blob url,
// and the open-artifact action jumps to the filed design artifact.
func TestIndexChatImageRenderBranch(t *testing.T) {
	html := readIndexForChatImage(t)
	for _, want := range []string{
		// render branch, everywhere the thread renders (live send + reload)
		"=== 'image' && message.image",
		"function scoutChatImageNode(message)",
		// validated ref before any <img> is built
		"/^[0-9a-f]{64}$/.test(ref)",
		// the picture is served inline by the blob route via artifactBlobUrl
		"img.src = artifactBlobUrl({ ref, name: image.name })",
		"scout-chat-image__img",
		// the open-artifact action
		"openArtifactStage(artifactId, String(image.prompt || 'Concept render'))",
		// the shimmer resolves on the image kind too
		"'artifact', 'image'].includes(recordKind)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing concept-render hook %q", want)
		}
	}
}

// The image proposal card: an image proposal keeps the editable objective,
// skips the package field, and its Run confirms via the proposal route WITHOUT
// runGoalPipeline (the single-pass path shared with workstreams).
func TestIndexImageProposalCardNeverRunsGoalPipeline(t *testing.T) {
	html := readIndexForChatImage(t)
	for _, want := range []string{
		"const isImage = String(proposal.kind || '') === 'image'",
		// the head names the concept render
		"? 'Concept render'",
		// no package field for an image render
		"if (!isWorkstream && !isImage) {",
		// Run confirms via the proposal route (workstream/image branch) with the
		// edited objective and returns — never reaching runGoalPipeline
		"if (isWorkstream || isImage) {",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing image proposal-card hook %q", want)
		}
	}
	// The single-pass branch returns with only postScoutProposalAction (no
	// runGoalPipeline) — pinned by the comment that rides it.
	if !strings.Contains(html, "the server does the single pass") {
		t.Fatal("index.html missing the single-pass confirm comment on the image/workstream Run branch")
	}
}

// Filed image assets render inline in the data room (concept renders + imagery
// boards), above the existing download link — currently image kinds were
// download-only.
func TestIndexRenderArtifactAssetsInlinesImages(t *testing.T) {
	html := readIndexForChatImage(t)
	for _, want := range []string{
		"const isImage = String(asset.kind || '').toLowerCase() === 'image' || String(asset.mime || '').toLowerCase().startsWith('image/')",
		"} else if (isImage) {",
		"artifact-asset__image",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing inline image-asset hook %q", want)
		}
	}
}
