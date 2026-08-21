package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentStudioFrontendContract(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		"function openDocumentStudio(artifactId, title, options = {})",
		"/artifacts/document?id=${encodeURIComponent(artifactId)}",
		"fetch('/artifacts/document'",
		"fetch('/artifacts/document/copies'",
		"document-editor__outline",
		"document-editor__paper",
		"document-editor__rich",
		"contenteditable=\"true\"",
		"data-doc-action=\"image\"",
		"data-doc-action=\"indent\"",
		"data-doc-action=\"outdent\"",
		"data-doc-action=\"status\"",
		"data-doc-action=\"table\"",
		"data-doc-action=\"source\"",
		"data-doc-action=\"pdf\"",
		"data-doc-action=\"save-copy\"",
		"openDocumentStudio(entry.id, stageTitle, { entry })",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing Document Studio contract %q", want)
		}
	}
}

func TestDocumentStudioRenderedEditPreviewSaveAndCopyJourney(t *testing.T) {
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
const artifactId='document-studio-artifact';
let version=3;
let markdown='# Field Notes\n\n## Opening\n\nA useful first draft.';
let title='Field Notes';
const patches=[];const copies=[];
const artifact=()=>({id:artifactId,title,version,savedToFiles:true,metadata:{title,type:'markdown',savedToFiles:'true',artifactVersion:String(version)}});
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/document?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown},canWrite:true}));}
  if(req.url==='/artifacts/document'&&req.method==='PATCH'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);if(body.expectedVersion!==version){res.writeHead(409,{'content-type':'application/json'});return res.end(JSON.stringify({error:'This document changed in another session. Reload the current version or save a copy.'}));}patches.push(body);markdown=body.document.markdown;title=body.title;version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown}}));});return;
  }
  if(req.url==='/test/bump'&&req.method==='POST'){version++;markdown+='\n\nServer-side revision.';res.writeHead(204);return res.end();}
  if(req.url==='/artifacts/document/copies'&&req.method==='POST'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);copies.push(body);res.writeHead(201,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact:{id:'document-copy',title:body.title,version:1},document:body.document,file:{id:'document-copy',name:body.fileName,folderId:body.folderId}}));});return;
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
 const paper=editor.locator('.document-editor__paper');
 const geometry=await page.evaluate(()=>{const editor=document.querySelector('.document-editor').getBoundingClientRect();const paper=document.querySelector('.document-editor__paper').getBoundingClientRect();return {editor:editor.toJSON(),paper:paper.toJSON()};});
 assert.ok(geometry.editor.width>=1439&&geometry.editor.height>=899,JSON.stringify(geometry));
 assert.ok(geometry.paper.width>600&&geometry.paper.left>160,JSON.stringify(geometry));
 assert.equal(await editor.locator('.document-editor__outline-item').count(),2);
 if(process.env.DOCUMENT_STUDIO_SCREENSHOT){await page.screenshot({path:process.env.DOCUMENT_STUDIO_SCREENSHOT,fullPage:true});}

 // Formatting uses a keyboard toolbar pattern, and modal focus cannot escape
 // behind Document Studio.
 const bold=editor.getByRole('button',{name:'Bold',exact:true});
 await bold.focus();await page.keyboard.press('ArrowRight');
 assert.equal(await page.evaluate(()=>document.activeElement?.getAttribute('aria-label')),'Italic');
 await editor.getByRole('button',{name:'Reload current version'}).focus();
 await page.keyboard.press('Tab');
 assert.equal(await page.evaluate(()=>document.activeElement?.getAttribute('aria-label')),'Close Document Studio');

 // iPad landscape keeps the document wide and moves formatting to a complete
 // second row instead of sacrificing the page to two side rails.
 await page.setViewportSize({width:1024,height:768});await page.waitForTimeout(80);
 const ipad=await page.evaluate(()=>{const rect=node=>node.getBoundingClientRect().toJSON();const formatting=document.querySelector('.document-editor [role="toolbar"]');const inspector=document.querySelector('.document-editor__inspector');return {paper:rect(document.querySelector('.document-editor__paper')),outline:rect(document.querySelector('.document-editor__outline')),inspector:{display:getComputedStyle(inspector).display,hidden:inspector.getAttribute('aria-hidden'),inert:inspector.inert},format:{clientWidth:formatting.clientWidth,scrollWidth:formatting.scrollWidth,rect:rect(formatting)},scrollWidth:document.documentElement.scrollWidth};});
 assert.equal(ipad.inspector.display,'block');assert.equal(ipad.inspector.hidden,'true');assert.equal(ipad.inspector.inert,true);assert.ok(ipad.outline.width>=159,JSON.stringify(ipad));
 assert.ok(ipad.paper.width>600&&ipad.paper.right<=1024,JSON.stringify(ipad));
 assert.ok(ipad.format.rect.left>=0&&ipad.format.rect.right<=1024&&ipad.format.scrollWidth<=ipad.format.clientWidth,JSON.stringify(ipad));
 assert.ok(ipad.scrollWidth<=1024,JSON.stringify(ipad));
 await editor.locator('[data-doc-mobile-status]').click();
 await page.waitForFunction(()=>document.querySelector('.document-editor__inspector')?.getBoundingClientRect().right<=innerWidth-11);
 const ipadInspector=await editor.locator('.document-editor__inspector').evaluate(node=>({rect:node.getBoundingClientRect().toJSON(),hidden:node.getAttribute('aria-hidden'),inert:node.inert,visibility:getComputedStyle(node).visibility}));
 assert.ok(ipadInspector.rect.left>=0&&ipadInspector.rect.right<=1024&&ipadInspector.rect.top>=0&&ipadInspector.rect.bottom<=768,JSON.stringify(ipadInspector));assert.equal(ipadInspector.hidden,null);assert.equal(ipadInspector.inert,false);assert.equal(ipadInspector.visibility,'visible');
 await editor.getByRole('button',{name:'Close document status'}).click();

 // Portrait tablet collapses the outline so the editable page remains the
 // dominant surface and never clips outside the viewport.
 await page.setViewportSize({width:768,height:1024});await page.waitForTimeout(80);
 const tablet=await page.evaluate(()=>{const paper=document.querySelector('.document-editor__paper').getBoundingClientRect();return {paper:paper.toJSON(),outline:getComputedStyle(document.querySelector('.document-editor__outline')).display,scrollWidth:document.documentElement.scrollWidth};});
 assert.equal(tablet.outline,'none');assert.ok(tablet.paper.left>=0&&tablet.paper.right<=768&&tablet.paper.width>600,JSON.stringify(tablet));assert.ok(tablet.scrollWidth<=768,JSON.stringify(tablet));
 await page.setViewportSize({width:1440,height:900});await page.waitForTimeout(80);

 const source=editor.getByRole('textbox',{name:'Document body'});
 await source.evaluate(node=>{node.focus();const walker=document.createTreeWalker(node,NodeFilter.SHOW_TEXT);let text;while(text=walker.nextNode()){const start=text.nodeValue.indexOf('useful');if(start>=0){const range=document.createRange();range.setStart(text,start);range.setEnd(text,start+'useful'.length);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);return;}}throw new Error('selection text missing');});
 await editor.getByRole('button',{name:'Bold',exact:true}).click();
 await source.evaluate(node=>{const heading=document.createElement('h2');heading.textContent='Recommendation';const paragraph=document.createElement('p');paragraph.textContent='Ship the stronger story.';node.append(heading,paragraph);node.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText'}));node.focus();const range=document.createRange();range.selectNodeContents(paragraph);range.collapse(false);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);});
 await editor.getByRole('button',{name:'Table',exact:true}).click();
 assert.equal(await source.locator('table').count(),1);
 assert.match(await editor.locator('[data-doc-status]').textContent(),/Unsaved changes/);
 assert.equal(await editor.locator('.document-editor__outline-item').count(),3);
 await editor.getByRole('button',{name:'Source',exact:true}).click();
 const markdownSource=editor.getByRole('textbox',{name:'Markdown source'});
 await markdownSource.waitFor({state:'visible'});
 assert.match(await markdownSource.inputValue(),/## Recommendation/);
 assert.match(await markdownSource.inputValue(),/\| Column 1 \| Column 2 \| Column 3 \|/);
 await editor.getByRole('button',{name:'Visual',exact:true}).click();
 assert.match(await source.textContent(),/Recommendation/);
 assert.match(await source.textContent(),/useful/);
 await editor.getByRole('textbox',{name:'Document name'}).fill('Field Notes — final');
 await editor.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Saved');
 assert.equal(patches.length,1);
 assert.equal(patches[0].title,'Field Notes — final');
 assert.match(patches[0].document.markdown,/\*\*useful\*\*/);
 assert.match(patches[0].document.markdown,/## Recommendation/);
 assert.match(patches[0].document.markdown,/\| Column 1 \| Column 2 \| Column 3 \|/);

 await editor.getByRole('button',{name:'Save a copy…',exact:true}).click();
 const copyDialog=page.locator('.drive-save-dialog');
 await copyDialog.waitFor({state:'visible'});
 await copyDialog.getByRole('textbox',{name:'File name'}).fill('Field Notes — team copy');
 await copyDialog.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent.includes('team copy'));
 assert.equal(copies.length,1);
 assert.equal(copies[0].title,'Field Notes — team copy');
 assert.match(copies[0].document.markdown,/Ship the stronger story/);

 await page.setViewportSize({width:390,height:844});
 await page.waitForTimeout(80);
 const mobile=await page.evaluate(()=>{const editor=document.querySelector('.document-editor').getBoundingClientRect();const paper=document.querySelector('.document-editor__paper').getBoundingClientRect();return {editor:editor.toJSON(),paper:paper.toJSON(),scrollWidth:document.documentElement.scrollWidth};});
 assert.ok(mobile.editor.width<=390&&mobile.paper.right<=390&&mobile.scrollWidth<=390,JSON.stringify(mobile));
 assert.equal(await editor.locator('[data-doc-mobile-status]').textContent(),'Saved');
 assert.notEqual(await editor.locator('[data-doc-mobile-status]').evaluate(node=>getComputedStyle(node).display),'none');
 const formatToolbar=editor.locator('[role="toolbar"]');
 await editor.locator('[data-doc-action="source"]').focus();
 const focusedFormat=await formatToolbar.evaluate(node=>{const active=document.activeElement.getBoundingClientRect();const toolbar=node.getBoundingClientRect();return {active:active.toJSON(),toolbar:toolbar.toJSON(),scrollLeft:node.scrollLeft,clientWidth:node.clientWidth,scrollWidth:node.scrollWidth};});
 assert.ok(focusedFormat.scrollWidth>focusedFormat.clientWidth&&focusedFormat.scrollLeft>0,JSON.stringify(focusedFormat));
 assert.ok(focusedFormat.active.left>=focusedFormat.toolbar.left&&focusedFormat.active.right<=focusedFormat.toolbar.right,JSON.stringify(focusedFormat));
 const mobileCopy=await editor.getByRole('button',{name:'Save a copy…',exact:true}).evaluate(node=>{const rect=node.getBoundingClientRect();const style=getComputedStyle(node);return {rect:rect.toJSON(),display:style.display,visibility:style.visibility,opacity:style.opacity,hidden:node.hidden,connected:node.isConnected,viewport:{width:innerWidth,height:innerHeight}};});
 assert.ok(mobileCopy.display!=='none'&&mobileCopy.visibility!=='hidden'&&mobileCopy.rect.width>0&&mobileCopy.rect.height>0&&mobileCopy.rect.right>0&&mobileCopy.rect.left<mobileCopy.viewport.width&&mobileCopy.rect.bottom>0&&mobileCopy.rect.top<mobileCopy.viewport.height,JSON.stringify(mobileCopy));

 // A revision conflict opens the full actionable status sheet on phone; the
 // person can read the real error and reload without hunting for hidden UI.
 await page.evaluate(()=>fetch('/test/bump',{method:'POST'}));
 await editor.getByRole('textbox',{name:'Document name'}).fill('Stale local title');
 await editor.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('[data-doc-mobile-status]')?.textContent==='Needs attention');
 await page.waitForFunction(()=>document.querySelector('.document-editor__inspector')?.getBoundingClientRect().bottom<=innerHeight);
 const recovery=await page.evaluate(()=>{const panel=document.querySelector('.document-editor__inspector');const reload=document.querySelector('[data-doc-action="reload"]').getBoundingClientRect();return {open:document.querySelector('.document-editor').dataset.inspectorOpen,hidden:panel.getAttribute('aria-hidden'),inert:panel.inert,status:document.querySelector('[data-doc-status]').textContent,panel:panel.getBoundingClientRect().toJSON(),reload:reload.toJSON()};});
 assert.equal(recovery.open,'true');assert.equal(recovery.hidden,null);assert.equal(recovery.inert,false);assert.match(recovery.status,/changed in another session/i);assert.ok(recovery.panel.top>=0&&recovery.panel.bottom<=844&&recovery.reload.width>0&&recovery.reload.height>0,JSON.stringify(recovery));
 page.once('dialog',dialog=>dialog.accept());
 await editor.getByRole('button',{name:'Reload current version'}).click();
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Current version loaded');
 await editor.getByRole('button',{name:'Close Document Studio'}).click();
 await editor.waitFor({state:'detached'});
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DOCUMENT_STUDIO_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Document Studio harness: %v\n%s", err, output)
	}
}

func TestDocumentStudioRenderedNestedListsAndSafeImagesRoundTrip(t *testing.T) {
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
const artifactId='document-list-image-artifact';
let version=2;
let markdown='# Activation plan\n\n1. Recruit\n  - Rodeo creators\n    1. Verify audience\n    2. Brief the cohort\n  - Music creators\n2. Activate\n  1. Launch the first batch\n  2. Measure participation\n\n![Field correspondents](https://images.example.test/field.jpg)\n\n![Unsafe](javascript:alert(1))';
const patches=[];
const artifact=()=>({id:artifactId,title:'Activation plan',version,savedToFiles:true,metadata:{type:'markdown',artifactVersion:String(version),savedToFiles:'true'}});
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/document?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown},canWrite:true}));}
  if(req.url==='/artifacts/document'&&req.method==='PATCH'){
    let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{const body=JSON.parse(raw);patches.push(body);markdown=body.document.markdown;version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown}}));});return;
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
 await page.evaluate(id=>openDocumentStudio(id,'Activation plan',{}),artifactId);
 const editor=page.locator('.document-editor');
 const rich=editor.getByRole('textbox',{name:'Document body'});
 await rich.waitFor({state:'visible'});
 assert.equal(await rich.locator(':scope > ol').count(),1);
 assert.equal(await rich.locator(':scope > ol > li').count(),2);
 assert.equal(await rich.locator(':scope > ol > li').first().locator(':scope > ul > li').count(),2);
 assert.equal(await rich.locator(':scope > ol > li').first().locator(':scope > ul > li').first().locator(':scope > ol > li').count(),2);
 assert.equal(await rich.locator('img').count(),1,'unsafe Markdown image must not become an image node');
 assert.equal(await rich.locator('img').first().getAttribute('alt'),'Field correspondents');
 assert.equal(await rich.locator('img').first().getAttribute('src'),'https://images.example.test/field.jpg');

 await editor.getByRole('button',{name:'Source',exact:true}).click();
 const source=editor.getByRole('textbox',{name:'Markdown source'});
 const normalized=await source.inputValue();
 assert.match(normalized,/1\. Recruit\n  - Rodeo creators\n    1\. Verify audience\n    2\. Brief the cohort\n  - Music creators\n2\. Activate/);
 assert.match(normalized,/!\[Field correspondents\]\(https:\/\/images\.example\.test\/field\.jpg\)/);
 assert.match(normalized,/!\[Unsafe\]\(javascript:alert\(1\)\)/,'an untouched source view should preserve the exact durable Markdown while the visual renderer keeps it inert');
 await editor.getByRole('button',{name:'Visual',exact:true}).click();

 // Tab/Shift+Tab and explicit touch-friendly controls both author nested
 // lists instead of moving focus out of the page.
 const placeCaretInActivate=async()=>rich.locator(':scope > ol > li').filter({hasText:/^Activate/}).evaluate(item=>{const text=item.firstChild;const range=document.createRange();range.selectNodeContents(text);range.collapse(false);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);item.closest('[contenteditable]')?.focus();});
 await placeCaretInActivate();
 await page.keyboard.press('Tab');
 assert.equal(await rich.locator(':scope > ol > li').count(),1,'Tab should indent the selected list item');
 const indentedLabels=await rich.locator(':scope > ol > li').first().locator('li').allTextContents();
 assert.ok(indentedLabels.some(label=>label.trim().startsWith('Activate')),JSON.stringify({indentedLabels,html:await rich.innerHTML()}));
 await page.keyboard.press('Shift+Tab');
 assert.equal(await rich.locator(':scope > ol > li').count(),2,'Shift+Tab should outdent the selected list item');
 await placeCaretInActivate();
 await editor.getByRole('button',{name:'Increase list indent'}).click();
 assert.equal(await rich.locator(':scope > ol > li').count(),1,'Indent control should work without a keyboard');
 await editor.getByRole('button',{name:'Decrease list indent'}).click();
 assert.equal(await rich.locator(':scope > ol > li').count(),2,'Outdent control should work without a keyboard');

 const answerDialogs=async answers=>{
   let index=0;
   const handler=dialog=>dialog.accept(answers[index++] || '');
   page.on('dialog',handler);
   try { await editor.getByRole('button',{name:'Insert image'}).click(); }
   finally { page.off('dialog',handler); }
 };
 await rich.focus();
 await answerDialogs(['https://images.example.test/creator.jpg','Creator at a western event']);
 assert.equal(await rich.locator('img').count(),2);
 const inserted=rich.locator('img[alt="Creator at a western event"]');
 assert.equal(await inserted.count(),1);
 assert.equal(await inserted.getAttribute('alt'),'Creator at a western event');

 await inserted.evaluate(image=>{const range=document.createRange();range.selectNodeContents(image);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);});
 await answerDialogs(['https://images.example.test/creator-edited.jpg','Creator briefing a cohort']);
 assert.equal(await rich.locator('img').count(),2);
 assert.equal(await rich.locator('img[alt="Creator briefing a cohort"]').getAttribute('src'),'https://images.example.test/creator-edited.jpg');

 await answerDialogs(['javascript:alert(1)','Unsafe source']);
 assert.equal(await rich.locator('img').count(),2,'unsafe Image action must not insert a node');
 const sanitizedPaste=await page.evaluate(()=>sanitizedDocumentPasteHTML('<img src="javascript:alert(1)" onerror="window.__unsafe=true" alt="bad"><img src="https://images.example.test/safe.jpg" onerror="window.__unsafe=true" alt="Safe \n description">'));
 assert.doesNotMatch(sanitizedPaste,/(?:javascript:|onerror)/i);
 assert.match(sanitizedPaste,/src="https:\/\/images\.example\.test\/safe\.jpg"/);
 assert.match(sanitizedPaste,/alt="Safe description"/);

 await editor.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Saved');
 assert.equal(patches.length,1);
 const saved=patches[0].document.markdown;
 assert.match(saved,/1\. Recruit\n  - Rodeo creators\n    1\. Verify audience\n    2\. Brief the cohort/);
 assert.match(saved,/!\[Field correspondents\]\(https:\/\/images\.example\.test\/field\.jpg\)/);
 assert.match(saved,/!\[Creator briefing a cohort\]\(https:\/\/images\.example\.test\/creator-edited\.jpg\)/);
 assert.match(saved,/\\!\[Unsafe\]\(javascript:alert\(1\)\)/);
 assert.doesNotMatch(saved,/(?:^|\n)!\[Unsafe\]\(javascript:|onerror/i);
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DOCUMENT_STUDIO_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Document Studio nested-list/image harness: %v\n%s", err, output)
	}
}
