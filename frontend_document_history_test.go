package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentStudioHistoryContract(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, "// Native Document Studio keeps Markdown")
	end := strings.Index(html, "// Structured Deck Studio")
	if start < 0 || end <= start {
		t.Fatal("Document Studio source boundary is missing")
	}
	documentStudio := html[start:end]
	for _, want := range []string{
		`data-doc-action="undo" disabled`,
		`data-doc-action="redo" disabled`,
		"function documentSnapshot()",
		"function commitDocumentMutation(",
		"function applyDocumentHistory(",
		"rich.addEventListener('beforeinput'",
		"source.addEventListener('beforeinput'",
		"titleInput.addEventListener('beforeinput'",
		"event.shiftKey ? 'redo' : 'undo'",
		"documentHistory.past = []",
		"documentHistory.future = []",
	} {
		if !strings.Contains(documentStudio, want) {
			t.Errorf("Document Studio history contract missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"document.execCommand(action)",
		"document.execCommand('undo'",
		`document.execCommand("undo"`,
		"document.execCommand('redo'",
		`document.execCommand("redo"`,
	} {
		if strings.Contains(documentStudio, forbidden) {
			t.Errorf("Document Studio history must not delegate %q to execCommand", forbidden)
		}
	}
}

func TestDocumentStudioRenderedTruthfulSnapshotHistory(t *testing.T) {
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
const html=fs.readFileSync(process.env.DOCUMENT_HISTORY_INDEX,'utf8');
const artifactId='document-history-artifact';
let version=1;
let title='Brief';
let markdown='# Brief\n\nOriginal.';
const patches=[];
const artifact=()=>({id:artifactId,title,version,savedToFiles:true,metadata:{title,type:'markdown',savedToFiles:'true',artifactVersion:String(version)}});
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/document?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown},canWrite:true}));}
  if(req.url==='/artifacts/document'&&req.method==='PATCH'){
    let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{const body=JSON.parse(raw);patches.push(body);title=body.title;markdown=body.document.markdown;version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown}}));});return;
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
  await page.evaluate(id=>openDocumentStudio(id,'Brief',{}),artifactId);
  const editor=page.locator('.document-editor');
  const rich=editor.getByRole('textbox',{name:'Document body'});
  const undo=editor.getByRole('button',{name:'Undo',exact:true});
  const redo=editor.getByRole('button',{name:'Redo',exact:true});
  await rich.waitFor({state:'visible'});
  assert.equal(await undo.isDisabled(),true);
  assert.equal(await redo.isDisabled(),true);

  // A synthetic/no-op input cannot invent history or mark a clean document dirty.
  const initialStatus=await editor.locator('[data-doc-status]').textContent();
  await rich.evaluate(node=>node.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText'})));
  assert.equal(await undo.isDisabled(),true);
  assert.equal(await redo.isDisabled(),true);
  assert.equal(await editor.locator('[data-doc-status]').textContent(),initialStatus);
  await editor.getByRole('button',{name:'Bold',exact:true}).click();
  await editor.getByRole('button',{name:'Bold',exact:true}).click();
  assert.equal(await undo.isDisabled(),true,'format controls with no editable selection must not invent history');

  // A burst of visual typing is one useful checkpoint, with keyboard undo/redo.
  await rich.locator('p').last().evaluate(node=>{const range=document.createRange();range.selectNodeContents(node);range.collapse(false);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);node.closest('[contenteditable]')?.focus();});
  await page.keyboard.type(' Added insight.');
  assert.match(await rich.textContent(),/Original\. Added insight\./);
  assert.equal(await undo.isDisabled(),false);
  assert.equal(await redo.isDisabled(),true);
  assert.equal(await editor.locator('[data-doc-status]').textContent(),'Unsaved changes');
  await page.keyboard.press('Control+z');
  assert.doesNotMatch(await rich.textContent(),/Added insight/);
  assert.equal(await undo.isDisabled(),true,'the typing burst should undo in one step');
  assert.equal(await redo.isDisabled(),false);
  assert.equal(await editor.locator('[data-doc-status]').textContent(),'Saved');
  assert.equal(await editor.locator('[data-doc-words]').textContent(),'3 words');
  await page.keyboard.press('Control+Shift+z');
  assert.match(await rich.textContent(),/Added insight/);
  assert.equal(await undo.isDisabled(),false);
  assert.equal(await redo.isDisabled(),true);

  // An idle pause starts another batch: undo removes only the latest burst,
  // rather than making the whole writing session one permanent checkpoint.
  await rich.locator('p').last().evaluate(node=>{const range=document.createRange();range.selectNodeContents(node);range.collapse(false);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);node.closest('[contenteditable]')?.focus();});
  await page.keyboard.type(' First burst.');
  await page.waitForTimeout(760);
  await page.keyboard.type(' Second burst.');
  await page.keyboard.press('Control+z');
  assert.match(await rich.textContent(),/First burst/);
  assert.doesNotMatch(await rich.textContent(),/Second burst/);
  await page.keyboard.press('Control+Shift+z');
  assert.match(await rich.textContent(),/First burst\. Second burst\./);

  // Source edits restore exact Markdown, source mode, outline, and counts.
  await editor.getByRole('button',{name:'Source',exact:true}).click();
  const source=editor.getByRole('textbox',{name:'Markdown source'});
  await source.waitFor({state:'visible'});
  const proofMarkdown='# Brief\n\n## Proof\n\nOriginal. Added insight. First burst. Second burst.';
  await source.fill(proofMarkdown);
  assert.equal(await editor.locator('.document-editor__outline-item').count(),2);
  const proofWords=await editor.locator('[data-doc-words]').textContent();
  await undo.click();
  assert.equal(await source.isVisible(),true);
  assert.doesNotMatch(await source.inputValue(),/## Proof/);
  assert.equal(await editor.locator('.document-editor__outline-item').count(),1);
  await redo.click();
  assert.equal(await source.inputValue(),proofMarkdown);
  assert.equal(await editor.locator('.document-editor__outline-item').count(),2);
  assert.equal(await editor.locator('[data-doc-words]').textContent(),proofWords);

  // Title history is independent, and save keeps meaningful history while
  // changing the clean baseline to the acknowledged server revision.
  const titleInput=editor.getByRole('textbox',{name:'Document name'});
  await titleInput.fill('Decision Brief');
  await page.keyboard.press('Control+z');
  assert.equal(await titleInput.inputValue(),'Brief');
  await page.keyboard.press('Control+Shift+z');
  assert.equal(await titleInput.inputValue(),'Decision Brief');
  await editor.getByRole('button',{name:'Save',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Saved');
  assert.equal(patches.length,1);
  assert.equal(patches[0].title,'Decision Brief');
  assert.equal(patches[0].document.markdown,proofMarkdown);
  assert.equal(await undo.isDisabled(),false,'saving should not pretend useful local history vanished');
  await undo.click();
  assert.equal(await titleInput.inputValue(),'Brief');
  assert.equal(await editor.locator('[data-doc-status]').textContent(),'Unsaved changes');
  await redo.click();
  assert.equal(await titleInput.inputValue(),'Decision Brief');
  assert.equal(await editor.locator('[data-doc-status]').textContent(),'Saved');

  // Reload is an explicit destructive boundary, so both stacks reset and the
  // editor, source, outline, counts, and status all reflect the server version.
  await source.fill(proofMarkdown+'\n\nUnsaved local paragraph.');
  assert.equal(await undo.isDisabled(),false);
  page.once('dialog',dialog=>dialog.accept());
  await editor.getByRole('button',{name:'Reload current version'}).click();
  await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Current version loaded');
  assert.equal(await undo.isDisabled(),true);
  assert.equal(await redo.isDisabled(),true);
  assert.equal(await rich.isVisible(),true);
  assert.doesNotMatch(await rich.textContent(),/Unsaved local paragraph/);
  assert.equal(await editor.locator('.document-editor__outline-item').count(),2);
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DOCUMENT_HISTORY_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Document Studio history harness: %v\n%s", err, output)
	}
}
