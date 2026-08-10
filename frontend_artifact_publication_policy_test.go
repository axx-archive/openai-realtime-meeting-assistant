package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestArtifactEditorMakesPrivateNoPublishPolicyUnmistakable(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)

	policy := functionBodyAfterSignature(html, "function artifactPublicationLocked(entry)")
	if !strings.Contains(policy, "private_no_publish_no_send") {
		t.Fatal("artifact editor must recognize the server's exact locked publication policy")
	}

	render := functionBodyAfterSignature(html, "function renderArtifactDetail()")
	for _, required := range []string{
		"artifactPublishButton.disabled = disabled || publicationLocked",
		"Private · cannot publish",
		"This private artifact cannot be published, shared, or sent.",
		"!published && !publicationLocked",
	} {
		if !strings.Contains(render, required) {
			t.Fatalf("artifact detail renderer is missing locked-publication affordance %q", required)
		}
	}

	toggle := functionBodyAfterSignature(html, "async function toggleSelectedArtifactPublished()")
	guard := strings.Index(toggle, "if (artifactPublicationLocked(artifact))")
	request := strings.Index(toggle, "fetch('/artifacts'")
	if guard < 0 || request < 0 || guard > request {
		t.Fatal("locked artifact must be rejected in the client before any publication request")
	}
	if !strings.Contains(toggle, "artifactPublishButton.disabled = !currentArtifact || artifactPublicationLocked(currentArtifact)") {
		t.Fatal("publication control must remain disabled after every request outcome")
	}
}

func TestArtifactPublicationPolicyRenderedAndExecuted(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	source := func(signature string) string {
		body := functionBodyAfterSignature(html, signature)
		if body == "" {
			t.Fatalf("missing production function %q", signature)
		}
		return signature + " " + body
	}

	production := strings.Join([]string{
		source("function artifactPublicationLocked(entry)"),
		source("function renderArtifactDetail()"),
		source("async function toggleSelectedArtifactPublished()"),
	}, "\n")
	encoded, err := json.Marshal(production)
	if err != nil {
		t.Fatal(err)
	}

	script := `
import assert from 'node:assert/strict';
const production = ` + string(encoded) + `;
const classes = () => ({ values: new Set(), toggle(name, on) { on ? this.values.add(name) : this.values.delete(name) }, contains(name) { return this.values.has(name) } });
const element = () => ({ disabled: false, hidden: false, title: '', textContent: '', value: '', classList: classes() });
const artifactDetailForm = element();
const artifactTitleInput = element();
const artifactBodyInput = element();
const artifactCopyButton = element();
const artifactPublishButton = element();
const artifactSaveButton = element();
const artifactEditButton = null;
const artifactApproveButton = null;
const artifactRejectButton = null;
const artifactRerunButton = null;
const artifactExportPdfButton = null;
const artifactShareButton = element();
const artifactSharePanel = null;
const artifactDetailMode = element();
const artifactDetailPublished = element();
const artifactDetailStatus = element();
const artifactReadPane = element();
let artifactEditMode = false;
let artifactSharePanelFor = null;
let renderSidecarReady = false;
let currentArtifact = null;
let fetches = [];
let renderCount = 0;
function selectedArtifact() { return currentArtifact }
function artifactPublished(entry) { return String(entry?.metadata?.published || '').trim().toLowerCase() === 'true' }
function artifactStatusValue(entry) { return artifactPublished(entry) ? 'published' : String(entry?.metadata?.status || 'complete') }
function artifactIsHTMLDeck() { return false }
function canApproveExternalWrites() { return false }
function artifactShareable() { return false }
function artifactModeLabel() { return 'workbook' }
function artifactStatusLabel(entry) { return artifactStatusValue(entry) }
function artifactTimeLabel() { return 'now' }
function renderArtifactRead() { renderCount++ }
function ensureArtifactBody() { return Promise.resolve() }
function setArtifactDetailStatus() {}
function showToast() {}
function addArtifactEntry() {}
global.fetch = async (url, options) => { fetches.push({ url, options, body: JSON.parse(options.body) }); return { ok: true, json: async () => ({ updated: true }) } };
eval(production + '\n;globalThis.renderArtifactDetail = renderArtifactDetail; globalThis.toggleSelectedArtifactPublished = toggleSelectedArtifactPublished;');

currentArtifact = { id: 'workbook-1', text: 'body', metadata: { status: 'complete', title: 'Workbook', publicationPolicy: '  PRIVATE_NO_PUBLISH_NO_SEND  ' } };
renderArtifactDetail();
assert.equal(artifactPublishButton.disabled, true);
assert.equal(artifactPublishButton.textContent, 'Private · cannot publish');
assert.equal(artifactPublishButton.title, 'This private artifact cannot be published, shared, or sent.');
assert.equal(artifactPublishButton.classList.contains('btn--primary'), false);
assert.equal(artifactPublishButton.classList.contains('btn--secondary'), true);
await toggleSelectedArtifactPublished();
assert.equal(fetches.length, 0);
assert.equal(artifactPublishButton.disabled, true);

currentArtifact = { id: 'ordinary-1', text: 'body', metadata: { status: 'complete', title: 'Ordinary' } };
renderArtifactDetail();
assert.equal(artifactPublishButton.disabled, false);
assert.equal(artifactPublishButton.textContent, 'Publish to team');
assert.equal(artifactPublishButton.classList.contains('btn--primary'), true);
assert.equal(artifactPublishButton.classList.contains('btn--secondary'), false);
await toggleSelectedArtifactPublished();
assert.equal(fetches.length, 1);
assert.equal(fetches[0].url, '/artifacts');
assert.equal(fetches[0].options.method, 'PATCH');
assert.deepEqual(fetches[0].body, { id: 'ordinary-1', title: 'Ordinary', text: 'body', published: true });
assert.equal(artifactPublishButton.disabled, false);

currentArtifact.metadata.published = 'true';
renderArtifactDetail();
assert.equal(artifactPublishButton.textContent, 'Published ✓');
assert.equal(artifactPublishButton.classList.contains('btn--secondary'), true);
await toggleSelectedArtifactPublished();
assert.equal(fetches.length, 2);
assert.equal(fetches[1].body.published, false);

global.fetch = async () => { throw new Error('network down') };
currentArtifact.metadata.published = 'false';
await toggleSelectedArtifactPublished();
assert.equal(artifactPublishButton.disabled, false);
process.stdout.write(JSON.stringify({ fetchCount: fetches.length, renderCount }));
`
	output, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("execute production publication UI functions: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"fetchCount":2`) {
		t.Fatalf("unexpected executable publication evidence: %s", output)
	}
}
