package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeckStudioUsesStructuredDurableSecurityContract(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"async function openDeckStudio(artifactId, title, options = {})",
		"/artifacts/deck?id=${encodeURIComponent(artifactId)}",
		"fetch('/artifacts/deck',",
		"expectedVersion: state.version",
		"if (payload?.canWrite !== true)",
		"payload?.writeBlockedReason",
		"const canEdit = deckAccess?.canWrite === true",
		"fetch('/artifacts/deck/image-generations'",
		"fetch('/artifacts/deck/assets'",
		"fetch('/artifacts/deck/copies'",
		"data-action=\"save-copy\"",
		"data-action=\"send-backward\"",
		"data-action=\"bring-forward\"",
		"data-prop=\"fit\"",
		"data-prop=\"rotation\"",
		"data-action=\"duplicate-slide\"",
		"data-action=\"add-rectangle\"",
		"data-action=\"add-ellipse\"",
		"data-action=\"undo\"",
		"data-action=\"redo\"",
		"data-slide-notes",
		"data-prop=\"fontFamily\"",
		"data-prop=\"textAlign\"",
		"data-prop=\"lineHeight\"",
		"data-prop=\"letterSpacing\"",
		"openDeckStudio(artifactId, title,",
		"if (e.source !== state.currentBackdrop?.contentWindow) return",
		"artifactIsDeckOutline(entry)",
		"Generate deck",
		"async function openDeckPresentation(artifactId, title, initialPayload = null)",
		"data-present-action=\"next\"",
		"data-present-action=\"notes\"",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Deck Studio contract missing %q", want)
		}
	}
	deckStart := strings.Index(html, "async function openDeckStudio")
	if deckStart < 0 {
		t.Fatal("Deck Studio implementation boundary is missing")
	}
	deckSurface := html[deckStart:]
	for _, banned := range []string{"src" + "doc", "allow-same-origin"} {
		if strings.Contains(deckSurface, banned) {
			t.Errorf("generated deck HTML can escape the tokened/opaque sandbox through %q", banned)
		}
	}
	if strings.Contains(html, "artifactInFilesOrChannelShared") {
		t.Fatal("Deck Studio still contains the retired client-side write-access heuristic")
	}
	saveStart := strings.Index(html, "async function saveDeck(closeAfter = true)")
	if saveStart < 0 {
		t.Fatal("Deck Studio durable save function is missing")
	}
	saveContract := html[saveStart:]
	responseGate := strings.Index(saveContract, "if (!response.ok || !result?.deck) throw")
	successToast := strings.Index(saveContract, "showToast({ text: 'Deck saved', kind: 'done' })")
	if responseGate < 0 || successToast < 0 || successToast < responseGate {
		t.Fatal("Deck Studio success UI is not gated on a successful durable PATCH response")
	}
}

func TestDeckStudioRenderedFitEditSaveAndImageJourney(t *testing.T) {
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
const html=fs.readFileSync(process.env.DECK_STUDIO_INDEX,'utf8');
const artifactId='deck-studio-artifact';
let version=4;let canWrite=true;
let deck={schemaVersion:1,width:1920,height:1080,theme:{background:'#10141c'},slides:[{id:'slide-one',background:'#10141c',notes:'Opening field story [BEAT]',elements:[{id:'headline',type:'text',x:150,y:130,width:1100,height:190,z:3,opacity:1,rotation:0,text:'A first-class deck',fontSize:76,fontFamily:'Arial',fontWeight:700,color:'#ffffff',textAlign:'left',lineHeight:1.08,letterSpacing:'normal',fill:'#ffffff',stroke:'#000000'},{id:'rich-proof',type:'text',x:150,y:360,width:900,height:260,z:4,opacity:1,rotation:0,text:'OBSERVED 6.1M',richText:'OBSERVED <span style="display:block;font-family:Georgia;font-size:75px;letter-spacing:.13em;margin:9px 0">6.1M</span>',fontSize:24,fontFamily:'Arial',fontWeight:700,color:'#ffffff',textAlign:'left',lineHeight:1.08,letterSpacing:'normal'}]}]};
let patches=[];let imageRequests=[];let uploadRequests=[];let copies=[];
const artifact=()=>({id:artifactId,title:'Studio proof',version,metadata:{title:'Studio proof',type:'html_deck',savedToFiles:'true',artifactVersion:String(version)}});
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/deck?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:artifact(),deck,canWrite}));}
  if(req.url==='/artifacts/render-token?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,url:'/mock-deck-render'}));}
  if(req.url==='/mock-deck-render'){res.writeHead(200,{'content-type':'text/html'});return res.end('<!doctype html><title>safe viewer</title>');}
  if(req.url==='/artifacts/deck'&&req.method==='PATCH'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);patches.push(body);assert.equal(body.expectedVersion,version);deck=body.deck;version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,updated:true,artifact:artifact(),deck}));});return;
  }
  if(req.url==='/artifacts/deck/image-generations'&&req.method==='POST'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);imageRequests.push(body);const element={id:'generated-image',type:'image',x:260,y:360,width:780,height:440,z:8,opacity:1,rotation:0,ref:'a'.repeat(64),name:'generated.png',fit:'cover'};deck.slides[0].elements.push(element);version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,updated:true,artifact:artifact(),deck,element,image:{ref:element.ref,name:element.name,mime:'image/png'}}));});return;
  }
  if(req.url==='/artifacts/deck/assets'&&req.method==='POST'){
    let bytes=0;req.on('data',chunk=>bytes+=chunk.length);req.on('end',()=>{uploadRequests.push({contentType:req.headers['content-type'],bytes});const element={id:'uploaded-image',type:'image',x:300,y:280,width:760,height:500,z:9,opacity:1,rotation:0,ref:'b'.repeat(64),name:'field-notes.png',fit:'cover'};deck.slides[0].elements.push(element);version++;res.writeHead(201,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,updated:true,artifact:artifact(),deck,element,image:{ref:element.ref,name:element.name,mime:'image/png'}}));});return;
  }
  if(req.url==='/artifacts/deck/copies'&&req.method==='POST'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);copies.push(body);res.writeHead(201,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact:{id:'deck-copy',title:body.title,type:'html_deck',version:1,savedToFiles:true},deck:body.deck,file:{id:'deck-copy',name:body.fileName,folderId:body.folderId}}));});return;
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
 await page.evaluate(id=>openDeckStudio(id,'Studio proof',{}),artifactId);
 await page.waitForSelector('.deck-editor');
	await page.evaluate(()=>{document.documentElement.dataset.theme='dark'});
	const darkThemeSlideBackground=await page.locator('.deck-editor__canvas').evaluate(node=>getComputedStyle(node).backgroundColor);
	assert.equal(darkThemeSlideBackground,'rgb(16, 20, 28)');
 const geometry=await page.evaluate(()=>{const canvas=document.querySelector('.deck-editor__canvas').getBoundingClientRect();const wrap=document.querySelector('.deck-editor__canvas-wrap').getBoundingClientRect();return {canvas:canvas.toJSON(),wrap:wrap.toJSON(),ratio:canvas.width/canvas.height};});
 assert.ok(geometry.canvas.width>700,JSON.stringify(geometry));
 assert.ok(Math.abs(geometry.ratio-16/9)<0.01,JSON.stringify(geometry));
 assert.ok(geometry.canvas.left>=geometry.wrap.left&&geometry.canvas.right<=geometry.wrap.right,JSON.stringify(geometry));
 assert.ok(geometry.canvas.top>=geometry.wrap.top&&geometry.canvas.bottom<=geometry.wrap.bottom,JSON.stringify(geometry));

 const richMetrics=async()=>page.evaluate(()=>{const canvas=document.querySelector('.deck-editor__canvas').getBoundingClientRect();const span=document.querySelector('[data-scene] [data-element-id="rich-proof"] span');const style=getComputedStyle(span);return {canvasHeight:canvas.height,fontSize:parseFloat(style.fontSize),marginTop:parseFloat(style.marginTop)};});
 const richLarge=await richMetrics();
 assert.ok(Math.abs(richLarge.fontSize/richLarge.canvasHeight-75/1080)<0.002,JSON.stringify(richLarge));
 assert.ok(Math.abs(richLarge.marginTop/richLarge.canvasHeight-9/1080)<0.002,JSON.stringify(richLarge));
 await page.setViewportSize({width:1100,height:700});await page.waitForTimeout(80);
 const richSmall=await richMetrics();
 assert.ok(richSmall.canvasHeight<richLarge.canvasHeight,JSON.stringify({richLarge,richSmall}));
 assert.ok(Math.abs(richSmall.fontSize/richSmall.canvasHeight-75/1080)<0.002,JSON.stringify(richSmall));
 assert.ok(Math.abs(richSmall.marginTop/richSmall.canvasHeight-9/1080)<0.002,JSON.stringify(richSmall));
 await page.setViewportSize({width:1440,height:900});await page.waitForTimeout(80);

 await page.locator('[data-scene] [data-element-id="rich-proof"]').dispatchEvent('dblclick');
 await page.locator('.deck-editor__text-input').waitFor({state:'visible'});
 await page.getByRole('button',{name:'Rectangle'}).focus();

 await page.locator('[data-scene] [data-element-id="headline"]').click();
 await page.locator('[data-prop="fontFamily"]').fill('Georgia, Times New Roman, serif');
 await page.locator('[data-prop="fontFamily"]').dispatchEvent('change');
 await page.locator('[data-prop="textAlign"]').selectOption('center');
 await page.locator('[data-prop="lineHeight"]').fill('1.2');
 await page.locator('[data-prop="lineHeight"]').dispatchEvent('change');
 await page.locator('[data-prop="letterSpacing"]').fill('.04em');
 await page.locator('[data-prop="letterSpacing"]').dispatchEvent('change');
 await page.locator('[data-slide-notes]').fill('Revised opening note [BEAT]');
 await page.locator('[data-slide-notes]').dispatchEvent('change');

 await page.getByRole('button',{name:'Rectangle'}).click();
 await page.locator('[data-prop="fill"]').fill('#3366ff');
 await page.locator('[data-prop="opacity"]').fill('0.55');
 await page.locator('[data-prop="opacity"]').dispatchEvent('change');
 assert.equal(await page.locator('.deck-editor__element[data-selected="true"]').count(),1);
 const selectedBox=await page.locator('[data-scene] .deck-editor__element[data-selected="true"]').boundingBox();
 await page.mouse.move(selectedBox.x+selectedBox.width/2,selectedBox.y+selectedBox.height/2);
 await page.mouse.down();await page.mouse.move(selectedBox.x+selectedBox.width/2+60,selectedBox.y+selectedBox.height/2+36);await page.mouse.up();
 const resizeHandle=await page.locator('[data-scene] .deck-editor__element[data-selected="true"] [data-handle="se"]').boundingBox();
 await page.mouse.move(resizeHandle.x+resizeHandle.width/2,resizeHandle.y+resizeHandle.height/2);
 await page.mouse.down();await page.mouse.move(resizeHandle.x+resizeHandle.width/2+45,resizeHandle.y+resizeHandle.height/2+28);await page.mouse.up();
 await page.getByRole('button',{name:'Front',exact:true}).click();
 await page.getByRole('button',{name:'Undo'}).click();
 await page.getByRole('button',{name:'Redo'}).click();
 await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>!document.querySelector('.deck-editor'));
 assert.equal(patches.length,1);
 assert.equal(patches[0].artifactId,artifactId);
 const savedShape=patches[0].deck.slides[0].elements.find(element=>element.type==='shape'&&element.shape==='rectangle');
 assert.ok(savedShape);
 assert.ok(savedShape.x>240&&savedShape.y>240,JSON.stringify(savedShape));
 assert.ok(savedShape.width>520&&savedShape.height>360,JSON.stringify(savedShape));
 const savedHeadline=patches[0].deck.slides[0].elements.find(element=>element.id==='headline');
 const savedRich=patches[0].deck.slides[0].elements.find(element=>element.id==='rich-proof');
 assert.match(savedRich.richText,/font-size:75px/);
 assert.equal(savedHeadline.fontFamily,'Georgia, Times New Roman, serif');
 assert.equal(savedHeadline.textAlign,'center');
 assert.equal(savedHeadline.lineHeight,1.2);
 assert.equal(savedHeadline.letterSpacing,'.04em');
 assert.equal(patches[0].deck.slides[0].notes,'Revised opening note [BEAT]');

 await page.evaluate(async id=>{await openDeckPresentation(id,'Studio proof')},artifactId);
 await page.waitForSelector('.deck-presenter');
 const presenterRichMetrics=async()=>page.evaluate(()=>{const stage=document.querySelector('[data-present-stage]').getBoundingClientRect();const span=document.querySelector('[data-present-element-id="rich-proof"] span');const style=getComputedStyle(span);return {stageHeight:stage.height,fontSize:parseFloat(style.fontSize),marginTop:parseFloat(style.marginTop)};});
 const presenterRichLarge=await presenterRichMetrics();
 assert.ok(Math.abs(presenterRichLarge.fontSize/presenterRichLarge.stageHeight-75/1080)<0.002,JSON.stringify(presenterRichLarge));
 assert.ok(Math.abs(presenterRichLarge.marginTop/presenterRichLarge.stageHeight-9/1080)<0.002,JSON.stringify(presenterRichLarge));
 await page.setViewportSize({width:1100,height:700});await page.waitForTimeout(80);
 const presenterRichSmall=await presenterRichMetrics();
 assert.ok(presenterRichSmall.stageHeight<presenterRichLarge.stageHeight,JSON.stringify({presenterRichLarge,presenterRichSmall}));
 assert.ok(Math.abs(presenterRichSmall.fontSize/presenterRichSmall.stageHeight-75/1080)<0.002,JSON.stringify(presenterRichSmall));
 assert.ok(Math.abs(presenterRichSmall.marginTop/presenterRichSmall.stageHeight-9/1080)<0.002,JSON.stringify(presenterRichSmall));
 await page.getByRole('button',{name:'Close'}).click();
 await page.setViewportSize({width:1440,height:900});await page.waitForTimeout(80);

 await page.evaluate(id=>openDeckStudio(id,'Studio proof',{}),artifactId);
 await page.waitForSelector('.deck-editor');
 await page.locator('[data-image-prompt]').fill('Documentary wide image of a working farm at first light');
 await page.getByRole('button',{name:'Generate'}).click();
 await page.waitForFunction(()=>document.querySelector('[data-image-status]')?.textContent.includes('added'));
 assert.equal(imageRequests.length,1);
 assert.equal(imageRequests[0].artifactId,artifactId);
 assert.equal(imageRequests[0].slideId,'slide-one');
 assert.equal(await page.locator('[data-scene] [data-element-id="generated-image"]').count(),1);
 assert.equal(await page.locator('.deck-editor__scout-status').textContent(),'Image generated and added to this slide.');

 const chooserPromise=page.waitForEvent('filechooser');
 await page.getByRole('button',{name:'Upload',exact:true}).click();
 const chooser=await chooserPromise;
 await chooser.setFiles({name:'field-notes.png',mimeType:'image/png',buffer:Buffer.from('89504e470d0a1a0a','hex')});
 await page.waitForFunction(()=>document.querySelector('[data-image-status]')?.textContent.includes('uploaded'));
 assert.equal(uploadRequests.length,1);
 assert.match(uploadRequests[0].contentType,/^multipart\/form-data; boundary=/);
 assert.ok(uploadRequests[0].bytes>8);
 assert.equal(await page.locator('[data-scene] [data-element-id="uploaded-image"]').count(),1);

 await page.locator('[data-prop="fit"]').selectOption('contain');
 await page.getByRole('button',{name:'Backward',exact:true}).click();
 await page.getByRole('button',{name:'Forward',exact:true}).click();
 await page.getByRole('button',{name:'Save a copy…',exact:true}).click();
 const copyDialog=page.locator('.drive-save-dialog');
 await copyDialog.waitFor({state:'visible'});
 await copyDialog.getByRole('textbox',{name:'File name'}).fill('Studio proof — team copy');
 await copyDialog.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('.deck-editor')&&!document.querySelector('.drive-save-dialog'));
 assert.equal(copies.length,1);
 assert.equal(copies[0].title,'Studio proof — team copy');
 assert.equal(copies[0].deck.slides[0].elements.find(element=>element.id==='uploaded-image').fit,'contain');

 await page.locator('[data-scene] [data-element-id="rich-proof"]').dispatchEvent('dblclick');
 await page.locator('.deck-editor__text-input').fill('Edited plain text');
 await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>!document.querySelector('.deck-editor'));
 assert.equal(patches.length,2);
 const flattenedRich=patches[1].deck.slides[0].elements.find(element=>element.id==='rich-proof');
 assert.equal(flattenedRich.text,'Edited plain text');
 assert.equal(flattenedRich.richText,'');

 canWrite=false;
 deck.slides.push({id:'slide-two',background:'#f2eee5',elements:[{id:'second-title',type:'text',x:180,y:160,width:1300,height:180,z:1,opacity:1,text:'The second slide',fontSize:72,fontWeight:700,color:'#151515'}]});
 await page.evaluate(id=>openDeckStudio(id,'Read-only proof',{}),artifactId);
 await page.waitForTimeout(50);
 assert.equal(await page.locator('.deck-editor').count(),0);
 await page.evaluate(id=>{const host=document.createElement('div');host.id='readonly-deck-host';document.body.appendChild(host);renderArtifactDeck(host,{id,kind:'os_artifact',text:'<!doctype html>',metadata:{type:'html_deck',title:'Read-only proof',savedToFiles:'true'}},{autoPresent:true});const frame=host.querySelector('.chat-deck__frame').getBoundingClientRect();window.__deckPreviewInitial={width:frame.width,height:frame.height};},artifactId);
 const readonlyHost=page.locator('#readonly-deck-host');
 await readonlyHost.getByRole('button',{name:'Present'}).waitFor({state:'visible'});
 await readonlyHost.locator('.chat-deck__frame.is-ready').waitFor({state:'attached'});
 await page.waitForTimeout(160);
 const stablePreview=await page.evaluate(()=>{const frame=document.querySelector('#readonly-deck-host .chat-deck__frame').getBoundingClientRect();const iframe=document.querySelector('#readonly-deck-host iframe');return {before:window.__deckPreviewInitial,after:{width:frame.width,height:frame.height},opacity:getComputedStyle(iframe).opacity};});
 assert.ok(Math.abs(stablePreview.before.width-stablePreview.after.width)<0.5&&Math.abs(stablePreview.before.height-stablePreview.after.height)<0.5,JSON.stringify(stablePreview));
 assert.equal(stablePreview.opacity,'1');
 await page.waitForFunction(()=>{const host=document.querySelector('#readonly-deck-host');return host?.querySelector('button')?.disabled===true&&Array.from(host.querySelectorAll('button')).some(button=>button.textContent.includes('Present')&&!button.disabled)});
 const readonlyEdit=readonlyHost.getByRole('button',{name:'Edit'});
 assert.equal(await readonlyEdit.isDisabled(),true);
 await page.evaluate(()=>document.querySelector('#readonly-deck-host button')?.click());
 assert.equal(await page.locator('.deck-editor').count(),0);
 await page.waitForSelector('.deck-presenter');
 assert.equal(await page.locator('[data-present-counter]').textContent(),'1 / 2');
 await page.getByRole('button',{name:'Close'}).click();
 await readonlyHost.getByRole('button',{name:'Present'}).click();
 await page.waitForSelector('.deck-presenter');
 assert.equal(await page.locator('[data-present-counter]').textContent(),'1 / 2');
 assert.equal(await page.locator('[data-present-element-id="headline"]').count(),1);
 const stageBeforeNotes=await page.locator('[data-present-stage]').boundingBox();
 await page.getByRole('button',{name:'Notes',exact:true}).click();
 await page.waitForTimeout(50);
 assert.equal(await page.locator('[data-present-notes]').textContent(),'Revised opening note [BEAT]');
 const stageWithNotes=await page.locator('[data-present-stage]').boundingBox();
 const notesRail=await page.locator('[data-present-notes]').boundingBox();
 assert.ok(stageWithNotes.width<stageBeforeNotes.width,JSON.stringify({stageBeforeNotes,stageWithNotes,notesRail}));
 assert.ok(stageWithNotes.x+stageWithNotes.width<=notesRail.x+1,JSON.stringify({stageWithNotes,notesRail}));
 await page.getByRole('button',{name:'Next slide'}).click();
 assert.equal(await page.locator('[data-present-counter]').textContent(),'2 / 2');
 assert.equal(await page.locator('[data-present-element-id="second-title"]').textContent(),'The second slide');
 await page.keyboard.press('ArrowLeft');
 assert.equal(await page.locator('[data-present-counter]').textContent(),'1 / 2');
 await page.getByRole('button',{name:'Close'}).click();
 assert.equal(await page.locator('.deck-presenter').count(),0);
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DECK_STUDIO_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Deck Studio harness: %v\n%s", err, output)
	}
}
