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
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);patches.push(body);assert.equal(body.expectedVersion,version);markdown=body.document.markdown;title=body.title;version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact:artifact(),document:{schemaVersion:1,markdown}}));});return;
  }
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

 const source=editor.getByRole('textbox',{name:'Document body'});
 await source.evaluate(node=>{const start=node.value.indexOf('useful');node.focus();node.setSelectionRange(start,start+'useful'.length);});
 await editor.getByRole('button',{name:'B',exact:true}).click();
 await source.press('End');
 await source.type('\n\n## Recommendation\n\nShip the stronger story.');
 assert.match(await editor.locator('[data-doc-status]').textContent(),/Unsaved changes/);
 assert.equal(await editor.locator('.document-editor__outline-item').count(),3);
 await editor.getByRole('button',{name:'Preview',exact:true}).click();
 await editor.locator('[data-doc-preview]').waitFor({state:'visible'});
 const renderedPreview=await editor.locator('[data-doc-preview]').textContent();
 assert.match(renderedPreview,/Recommendation/);
 assert.match(renderedPreview,/useful/);
 await editor.getByRole('button',{name:'Edit',exact:true}).click();
 await editor.getByRole('textbox',{name:'Document name'}).fill('Field Notes — final');
 await editor.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('[data-doc-status]')?.textContent==='Saved');
 assert.equal(patches.length,1);
 assert.equal(patches[0].title,'Field Notes — final');
 assert.match(patches[0].document.markdown,/\*\*useful\*\*/);
 assert.match(patches[0].document.markdown,/## Recommendation/);

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
 const mobileCopy=await editor.getByRole('button',{name:'Save a copy…',exact:true}).evaluate(node=>{const rect=node.getBoundingClientRect();const style=getComputedStyle(node);return {rect:rect.toJSON(),display:style.display,visibility:style.visibility,opacity:style.opacity,hidden:node.hidden,connected:node.isConnected,viewport:{width:innerWidth,height:innerHeight}};});
 assert.ok(mobileCopy.display!=='none'&&mobileCopy.visibility!=='hidden'&&mobileCopy.rect.width>0&&mobileCopy.rect.height>0&&mobileCopy.rect.right>0&&mobileCopy.rect.left<mobileCopy.viewport.width&&mobileCopy.rect.bottom>0&&mobileCopy.rect.top<mobileCopy.viewport.height,JSON.stringify(mobileCopy));
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
