package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Wave 4 (Studios) static pins: autosave debounce + chip, version history
// menu + read-only stage + restore, DOCX export beside PDF, the 409 →
// "Save a copy" branch, print rules, open-from-Drive import, deck themes +
// layouts, and the absence of the dead openDeckEditor.

func readIndexForStudiosWave4(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func studiosWave4Section(t *testing.T, html, start, end string) string {
	t.Helper()
	from := strings.Index(html, start)
	if from < 0 {
		t.Fatalf("section start %q is missing", start)
	}
	to := strings.Index(html[from:], end)
	if to < 0 {
		t.Fatalf("section end %q is missing after %q", end, start)
	}
	return html[from : from+to]
}

func requireStudiosWave4Markers(t *testing.T, label, body string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s missing %q", label, want)
		}
	}
}

// D1: both studios debounce 1.5 s behind the last edit once the first
// successful filed save armed autosave; the chip is a mono machine fact and
// the retry lane is quiet ("Not saved · retrying"); conflicts suspend it.
func TestIndexStudiosWave4AutosaveContract(t *testing.T) {
	html := readIndexForStudiosWave4(t)
	requireStudiosWave4Markers(t, "autosave helpers", html, []string{
		"var studioAutosaveDelayMs = 1500",
		"function renderStudioAutosaveChip(chip, autosave, stateName)",
		"function studioAutosaveBackoffMs(attempts)",
		"function clearStudioAutosaveTimers(autosave)",
		`data-autosave-chip role="status" aria-live="polite" hidden`,
		"['Not saved', '· retrying']",
		"['Not saved', '· changed elsewhere']",
	})
	chip := studiosWave4Section(t, html, ".studio-autosave {", ".studio-autosave[hidden]")
	if !strings.Contains(chip, "font-family: var(--font-mono)") {
		t.Error("the autosave chip must be set in the mono face (machine facts only)")
	}
	document := studiosWave4Section(t, html, "// Native Document Studio keeps Markdown", "// Structured Deck Studio")
	requireStudiosWave4Markers(t, "Document Studio autosave", document, []string{
		"async function saveDocument(closeAfter = false, { auto = false } = {})",
		"if (auto && (!state.savedToFiles || documentAutosave.conflict || !state.dirty)) return false",
		"documentAutosave.timer = window.setTimeout(runDocumentAutosave, studioAutosaveDelayMs)",
		"documentAutosave.armed = documentAutosave.armed || state.savedToFiles === true",
		"documentAutosave.retry = window.setTimeout(runDocumentAutosave, studioAutosaveBackoffMs(documentAutosave.attempts))",
		"setStatus('Not saved · retrying', '')",
		"window.addEventListener('online', documentOnline)",
		"scheduleDocumentAutosave()",
	})
	deck := studiosWave4Section(t, html, "// Structured Deck Studio", "function closeDeckEditor()")
	requireStudiosWave4Markers(t, "Deck Studio autosave", deck, []string{
		"async function saveDeck(closeAfter = true)",
		"const auto = deckSaveAuto === true",
		"if (auto && (!state.savedToFiles || deckAutosave.conflict || !state.dirty)) return false",
		"deckAutosave.timer = window.setTimeout(runDeckAutosave, studioAutosaveDelayMs)",
		"deckAutosave.armed = deckAutosave.armed || state.savedToFiles === true",
		"if (state.saving || state.textEditingId || state.transforming || state.imageGenerating)",
		"if (!auto) showToast({ text: 'Deck saved', kind: 'done' })",
		"window.addEventListener('online', deckOnline)",
	})
	// The autosave chip carries no animation of its own: the deck's first
	// frame stays still (TestDeckStudioRenderedPhoneTouchControls).
	if strings.Contains(chip, "transition") || strings.Contains(chip, "animation") {
		t.Error("the autosave chip must not animate")
	}
}

// D2: a History bfMenu lists the current revision first, then superseded
// versions as mono rows; a row opens a read-only stage whose Restore PATCHes
// the old body with restoredFrom.
func TestIndexStudiosWave4HistoryContract(t *testing.T) {
	html := readIndexForStudiosWave4(t)
	requireStudiosWave4Markers(t, "history helpers", html, []string{
		"async function loadStudioHistoryInto(controller, kind, artifactId, onPick)",
		"/artifacts/${kind}/versions?id=${encodeURIComponent(artifactId)}",
		"function studioVersionRowLabel(row)",
		"hint: 'current'",
		"function openStudioVersionStage({ kind, row, current = false, body, note = '', restore = null, restoreDisabledReason = '' })",
		"'Restore this version'",
		"className: 'studio-history-menu'",
		`data-doc-action="history"`,
		`data-action="history"`,
	})
	if got := strings.Count(html, "restoredFrom: Number(row.version)"); got != 2 {
		t.Errorf("restore must PATCH with restoredFrom in both studios, found %d sites", got)
	}
	rows := studiosWave4Section(t, html, ".studio-history-menu .bf-menu__label {", "}")
	if !strings.Contains(rows, "font-family: var(--font-mono)") {
		t.Error("history rows are machine facts and must be set in mono")
	}
	stage := studiosWave4Section(t, html, ".studio-version-stage {", ".studio-version-stage__head {")
	if !strings.Contains(stage, "transition: opacity var(--dur-med) var(--ease)") || strings.Contains(stage, "transform") {
		t.Error("the fullscreen version stage must enter opacity-only on token timing")
	}
	requireStudiosWave4Markers(t, "version reopen", html, []string{
		"/artifacts/document?id=${encodeURIComponent(state.artifactId)}&version=${encodeURIComponent(row.version)}",
		"/artifacts/deck?id=${encodeURIComponent(state.artifactId)}&version=${encodeURIComponent(row.version)}",
	})
}

// D3: Word sits beside PDF on the same expectedVersion binding, and an
// edited-after-admission artifact gets honest copy instead of a generic error.
func TestIndexStudiosWave4DocxExportContract(t *testing.T) {
	html := readIndexForStudiosWave4(t)
	requireStudiosWave4Markers(t, "DOCX export", html, []string{
		"data-doc-action=\"pdf\">PDF</button>\n              <button type=\"button\" class=\"deck-editor__btn\" data-doc-action=\"docx\"",
		`<button type="button" role="menuitem" data-doc-action="docx">Download Word</button>`,
		"fetch('/artifacts/export-docx'",
		"body: JSON.stringify({ artifactId: state.artifactId, expectedVersion: state.version })",
		"kind: 'document', format: 'docx'",
		"studioDownloadFileName(state.title, 'report', 'docx')",
		"This document changed after its review. Review the current revision before downloading Word.",
		"[data-doc-action=\"pdf\"], [data-doc-action=\"docx\"]",
	})
}

// D4: a 409 keeps the unsaved body, suspends autosave and offers the existing
// copy handlers instead of the old dead-end toast.
func TestIndexStudiosWave4ConflictSaveCopyContract(t *testing.T) {
	html := readIndexForStudiosWave4(t)
	requireStudiosWave4Markers(t, "conflict branch", html, []string{
		"function markDocumentConflict(message)",
		"function markDeckConflict(message)",
		`data-doc-conflict role="alert" hidden`,
		`data-deck-conflict role="alert" hidden`,
		`<button type="button" class="deck-editor__btn deck-editor__btn--primary" data-doc-action="save-copy">Save a copy…</button>`,
		`<button type="button" class="deck-editor__btn deck-editor__btn--primary" data-action="save-copy">Save a copy…</button>`,
		`data-action="reload-deck"`,
		"async function reloadDeck()",
		"if (response.status === 409) conflict = true",
	})
	document := studiosWave4Section(t, html, "async function saveDocument(closeAfter = false, { auto = false } = {})", "async function saveCopy()")
	if !strings.Contains(document, "if (response.status === 409) {") || !strings.Contains(document, "markDocumentConflict(") {
		t.Error("Document Studio save must route a 409 to the conflict branch")
	}
	// never lose the user's text: a trailing root-level run is flushed before the serializer returns
	serializer := studiosWave4Section(t, html, "function documentEditorToMarkdown(root) {", "function sanitizedDocumentPasteHTML(raw)")
	if !strings.Contains(serializer, "flushInlineRun()\n        return blocks") {
		t.Error("documentEditorToMarkdown must flush the trailing inline run before returning")
	}
	if strings.Contains(html, "This deck changed elsewhere. Reopen it before saving.") {
		t.Error("the dead-end deck conflict toast must be gone")
	}
	banner := studiosWave4Section(t, html, ".studio-conflict {", ".studio-conflict[hidden]")
	if !strings.Contains(banner, "@starting-style") || !strings.Contains(banner, "var(--dur-med)") {
		t.Error("the conflict banner enters via @starting-style on token timing")
	}
}

// D5: print hides the chrome and fills the page with the document paper; the
// deck prints one slide per landscape page from a transient sheet.
func TestIndexStudiosWave4PrintContract(t *testing.T) {
	html := readIndexForStudiosWave4(t)
	print := studiosWave4Section(t, html, "@media print {\n        body[data-studio-print]", ".deck-presenter {")
	requireStudiosWave4Markers(t, "print rules", print, []string{
		`body[data-studio-print="document"] .document-editor__paper {`,
		`body[data-studio-print="document"] .document-editor__toolbar,`,
		`body[data-studio-print="deck"] .deck-sheet--print .deck-sheet__slide {`,
		"break-after: page;",
		"page-break-after: always;",
	})
	requireStudiosWave4Markers(t, "print actions", html, []string{
		"function studioPrint(kind, { before, after } = {})",
		"'@page { size: landscape; margin: 0; }'",
		"function printDocumentStudio()",
		"function printDeckStudio()",
		`<button type="button" role="menuitem" data-doc-action="print">Print</button>`,
		`<button type="button" role="menuitem" data-action="print"><span>Print</span><small>one slide per page</small></button>`,
		"event.key.toLowerCase() === 'p') { event.preventDefault(); printDocumentStudio() }",
		"paintDeckSheet(state.deck, 'deck-sheet--print')",
	})
}

// D6: .md / .txt uploads open in Document Studio through the import route;
// every other file keeps its Open / Download control.
func TestIndexStudiosWave4DriveImportContract(t *testing.T) {
	html := readIndexForStudiosWave4(t)
	requireStudiosWave4Markers(t, "Drive import", html, []string{
		"function fileIsStudioText(file)",
		"['text/markdown', 'text/x-markdown', 'text/plain'].includes(mime)",
		"async function openDriveTextFileInStudio(file, button)",
		"fetch('/artifacts/document/import'",
		"body: JSON.stringify({ fileId: String(file.id) })",
		"return openDocumentStudio(result.id, title)",
		"'Open in Document Studio'",
	})
	control := studiosWave4Section(t, html, "function fileOpenControl(file) {", "function renderFileDetails()")
	if !strings.Contains(control, "if (fileIsStudioText(file)) {") {
		t.Error("fileOpenControl must branch text files into Document Studio")
	}
	if !strings.Contains(control, "link.textContent = file?.previewable ? 'Open' : 'Download'") {
		t.Error("non-text files must keep today's Open / Download control")
	}
}

// D7: three server-owned themes with swatches, a Layouts bfMenu that suffixes
// template ids per slide, and inherited (empty) colors that follow the theme.
func TestIndexStudiosWave4ThemeAndLayoutContract(t *testing.T) {
	html := readIndexForStudiosWave4(t)
	deck := studiosWave4Section(t, html, "// Structured Deck Studio", "function closeDeckEditor()")
	requireStudiosWave4Markers(t, "deck themes + layouts", deck, []string{
		"const deckThemeCatalog = (Array.isArray(payload?.themes) ? payload.themes : [])",
		"const deckLayoutCatalog = (Array.isArray(payload?.layouts) ? payload.layouts : [])",
		"function normalizeDeckTheme(input)",
		`data-deck-theme role="radiogroup" aria-label="Deck theme"`,
		"function renderDeckThemePicker()",
		"function applyDeckTheme(id)",
		`data-action="layouts-menu"`,
		"function applyDeckLayout(layout)",
		"uniqueDeckElementId(`${base}-${slideSuffix}`, current)",
		"className: 'studio-layout-menu'",
		"element.color = String(element.color || '')",
		"element.fontFamily = String(element.fontFamily || '')",
		"content.style.color = element.color || theme?.textColor || '#ffffff'",
		"function slideBackgroundValue(item, deck = state.deck)",
		"background: '', notes: '', elements: [] }",
	})
	// the presenter and the channel preview resolve inherited colors the same way
	if got := strings.Count(html, "documentModel?.theme?.textColor"); got != 2 {
		t.Errorf("presenter and channel preview must both resolve theme text color, found %d", got)
	}
	swatch := studiosWave4Section(t, html, ".deck-editor__theme-swatch {", ".deck-editor__theme-chip {")
	if !strings.Contains(swatch, "background: var(--surface-2)") || strings.Contains(swatch, "--ember") {
		t.Error("theme swatches sit on the surface ramp; the chrome carries no ember")
	}
}

// D8: the dead legacy editor is gone and nothing references it.
func TestIndexStudiosWave4DeadDeckEditorRemoved(t *testing.T) {
	html := readIndexForStudiosWave4(t)
	if strings.Contains(html, "function openDeckEditor(") {
		t.Fatal("the dead openDeckEditor is still defined")
	}
	if got := strings.Count(html, "openDeckEditor"); got != 0 {
		t.Errorf("openDeckEditor is still referenced %d times", got)
	}
	if !strings.Contains(html, "async function openDeckStudio(artifactId, title, options = {})") {
		t.Fatal("the structured Deck Studio must remain")
	}
}

// Rendered: text typed with the caret below the last block (a root-level run
// in Chrome) and a trailing root-level image block both reach the durable
// Markdown — in the source textarea, in the manual Save PATCH, and in the
// autosave PATCH that follows without a Drive modal.
func TestDocumentStudioRenderedRootLevelRunsSurviveSaveAndAutosave(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');
const http=require('http');
const assert=require('assert/strict');
const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.DOCUMENT_STUDIO_INDEX,'utf8');
const artifactId='document-root-runs';
let version=2;
let markdown='# Field Notes\n\nA first paragraph.';
let title='Field Notes';
const patches=[];
const artifact=()=>({id:artifactId,title,version,savedToFiles:true,metadata:{title,type:'markdown',savedToFiles:'true',artifactVersion:String(version)}});
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/document?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown},canWrite:true}));}
  if(req.url==='/artifacts/document'&&req.method==='PATCH'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);if(body.expectedVersion!==version){res.writeHead(409,{'content-type':'application/json'});return res.end(JSON.stringify({error:'document revision changed'}));}patches.push(body);markdown=body.document.markdown;title=body.title;version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown}}));});return;
  }
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:900}});
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(id=>openDocumentStudio(id,'Field Notes',{}),artifactId);
 const editor=page.locator('.document-editor');
 await editor.waitFor({state:'visible'});
 const rich=editor.getByRole('textbox',{name:'Document body'});
 const source=editor.locator('[data-doc-source]');

 // A real click in the empty editable area below the last block (the rich
 // surface keeps a min-height well past its content), then typing.
 const richBox=await rich.boundingBox();
 const lastBlock=await rich.locator(':scope > *').last().boundingBox();
 assert.ok(richBox.y+richBox.height-lastBlock.y-lastBlock.height>80,'fixture must leave empty editable space below the last block '+JSON.stringify({richBox,lastBlock}));
 await page.mouse.click(richBox.x+richBox.width/2,richBox.y+richBox.height-24);
 await page.keyboard.type('Typed below the last block.');
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Unsaved changes');
 assert.match(await source.inputValue(),/Typed below the last block\./,'the source mirror must carry text typed below the last block');

 // Deterministic root-level runs: text, a trailing image block, then text
 // after it — exactly the shape Chrome leaves when the caret sits at the root.
 await page.evaluate(()=>{const root=document.querySelector('[data-doc-rich]');root.append(document.createTextNode('Root run before figure.'),documentImageBlock('/artifacts/blob/trailing-figure.png','Trailing figure'),document.createTextNode(' Root run after figure.'));root.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText'}));});
 const mirrored=await source.inputValue();
 assert.match(mirrored,/^# Field Notes/);
 assert.match(mirrored,/Typed below the last block\./);
 assert.match(mirrored,/Root run before figure\.!\[Trailing figure\]\(\/artifacts\/blob\/trailing-figure\.png\) Root run after figure\.$/,mirrored);

 await editor.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Saved');
 assert.equal(patches.length,1);
 assert.match(patches[0].document.markdown,/Typed below the last block\./);
 assert.match(patches[0].document.markdown,/!\[Trailing figure\]\(\/artifacts\/blob\/trailing-figure\.png\) Root run after figure\.$/,patches[0].document.markdown);
 assert.equal(await page.locator('.drive-save-dialog').count(),0,'a filed document never re-prompts for a destination');

 // The filed save armed autosave: another root-level run lands as its own
 // PATCH within the 1.5 s debounce, with no Save click and no Drive modal.
 await page.waitForFunction(()=>document.querySelector('[data-autosave-chip]')?.hidden===false);
 await page.evaluate(()=>{const root=document.querySelector('[data-doc-rich]');const range=document.createRange();range.selectNodeContents(root);range.collapse(false);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);root.focus();});
 await page.keyboard.type(' Autosaved tail.');
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Unsaved changes');
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Saved',null,{timeout:6000});
 assert.equal(patches.length,2,'autosave must land exactly one more PATCH');
 assert.match(patches[1].document.markdown,/Autosaved tail\./,patches[1].document.markdown);
 assert.match(patches[1].document.markdown,/Root run after figure\./);
 assert.equal(await page.locator('.drive-save-dialog').count(),0);
 assert.match(await page.locator('[data-autosave-chip]').textContent(),/^Saved/);
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DOCUMENT_STUDIO_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Document Studio root-level runs harness: %v\n%s", err, output)
	}
}
