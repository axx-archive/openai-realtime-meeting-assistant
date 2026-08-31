package main

// Card 096 — the concept render's frontend contract. Grep-style pins (the
// frontend_router_test.go pattern) holding the client half: a Kind=image
// message renders the picture inline from a validated blob ref beside
// save/regenerate actions, the generating pill resolves on the image path,
// legacy proposal cards remain compatible, and filed image assets render
// inline in the data room.

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
		"function openScoutChatImagePreview(src, alt)",
		"preview.addEventListener('click', () => openScoutChatImagePreview(img.src, img.alt))",
		"scout-chat-image-viewer__close",
		// the open-artifact action
		"openArtifactStage(artifactId, String(image.prompt || 'Concept render'))",
		// the shimmer resolves on the image kind too
		"'artifact', 'image', 'image_pending'].includes(recordKind)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing concept-render hook %q", want)
		}
	}
}

func TestIndexChatImageReservesItsFeedGeometryBeforeLoad(t *testing.T) {
	html := readIndexForChatImage(t)
	for _, want := range []string{
		"aspect-ratio: 3 / 2",
		".scout-chat-image__img {",
		"height: 100%;",
		"object-fit: contain;",
		"overflow-anchor: none;",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("chat media can shift a loaded channel because index.html is missing %q", want)
		}
	}
}

func TestIndexDirectChatImageControls(t *testing.T) {
	html := readIndexForChatImage(t)
	for _, want := range []string{
		"function scoutChatImagePendingNode(message)",
		"generating image…",
		"function scoutChatImageSaveControl(image)",
		"/assistant/files/save",
		"button.setAttribute('aria-label', 'Image saved to Drive')",
		"function beginScoutChatImageRegenerate(message, figure)",
		"Prompt used",
		"/messages/${encodeURIComponent(messageId)}/regenerate",
		"function attachScoutChatImageRegenerateControl(message, figure)",
		"event.pointerType !== 'touch'",
		"figure.classList.add('show-regenerate')",
		"Regenerate image",
		"More image actions",
		"label.htmlFor = input.id",
		"outline: 1px solid rgba(0, 0, 0, 0.1)",
		"outline-color: rgba(255, 255, 255, 0.1)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing direct image-generation hook %q", want)
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
		// one server-owned objective is the only editable field; the retired
		// package/tool field map must not be reconstructed client-side
		"const objectiveInput = document.createElement('textarea')",
		// Run confirms via the proposal route (workstream/image branch) with the
		// edited objective and returns — never reaching runGoalPipeline
		"if (isWorkstream || isImage) {",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing image proposal-card hook %q", want)
		}
	}
	card := functionBody(html, "function buildScoutProposalCardNode(message)")
	if strings.Contains(card, "paletteBuildPackageField()") {
		t.Fatal("image proposal card exposes a client-side package/tool chooser")
	}
	// The image branch returns through the persisted proposal route only.
	if !strings.Contains(html, "the async concept render for card 096") {
		t.Fatal("index.html missing the async concept-render confirmation contract")
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
