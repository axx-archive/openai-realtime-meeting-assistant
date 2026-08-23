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
		"data-deck-title",
		"title: requestedTitle",
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
		"data-action=\"export-pptx\"",
		"data-action=\"export-pdf\"",
		"data-inspector-tab=\"design\"",
		"data-inspector-tab=\"notes\"",
		"data-inspector-tab=\"scout\"",
		"data-slide-quick-tools",
		"data-slide-background-value",
		"data-action=\"open-scout\"",
		"data-action=\"duplicate-element\"",
		"data-action=\"align-center\"",
		"data-slide-background",
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
const sceneRef='c'.repeat(64);
let version=4;let canWrite=true;let deckTitle='Studio proof';let managed=false;let publicationReady=false;
let deck={schemaVersion:1,width:1920,height:1080,theme:{background:'#10141c'},slides:[{id:'slide-one',background:'#10141c',notes:'Opening field story [BEAT]',elements:[{id:'headline',type:'text',x:150,y:130,width:1100,height:190,z:3,opacity:1,rotation:0,text:'A first-class deck',fontSize:76,fontFamily:'Arial',fontWeight:700,color:'#ffffff',textAlign:'left',lineHeight:1.08,letterSpacing:'normal',fill:'#ffffff',stroke:'#000000'},{id:'rich-proof',type:'text',x:150,y:360,width:900,height:260,z:4,opacity:1,rotation:0,text:'OBSERVED 6.1M',richText:'OBSERVED <span style="display:block;font-family:Georgia;font-size:75px;letter-spacing:.13em;margin:9px 0">6.1M</span>',fontSize:24,fontFamily:'Arial',fontWeight:700,color:'#ffffff',textAlign:'left',lineHeight:1.08,letterSpacing:'normal'}]}]};
let patches=[];let imageRequests=[];let uploadRequests=[];let copies=[];let fileRetries=[];let pptxRequests=[];
const artifact=()=>({id:artifactId,title:deckTitle,version,sceneRef,metadata:{title:deckTitle,type:'html_deck',savedToFiles:'true',artifactVersion:String(version),deckSceneRef:sceneRef}});
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/deck?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:artifact(),deck,canWrite,managed,qualityState:managed?(publicationReady?'admitted':'draft_needs_attention'):'',canPresent:managed?publicationReady:true,canExport:managed?publicationReady:true}));}
  if(req.url==='/artifacts/final-export-capability?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({artifactId,artifactVersion:version,managed,qualityState:publicationReady?'admitted':'draft_needs_attention',canPresent:publicationReady,canExport:publicationReady}));}
  if(req.url==='/artifacts/render-token?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,url:'/mock-deck-render'}));}
  if(req.url==='/mock-deck-render'){res.writeHead(200,{'content-type':'text/html'});return res.end('<!doctype html><title>safe viewer</title>');}
  if(req.url==='/artifacts/deck'&&req.method==='PATCH'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);patches.push(body);assert.equal(body.expectedVersion,version);deck=body.deck;deckTitle=body.title||deckTitle;version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,updated:true,artifact:artifact(),deck}));});return;
  }
  if(req.url==='/artifacts/deck/image-generations'&&req.method==='POST'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);imageRequests.push(body);const element={id:'generated-image',type:'image',x:260,y:360,width:780,height:440,z:8,opacity:1,rotation:0,ref:'a'.repeat(64),name:'generated.png',fit:'cover'};deck.slides[0].elements.push(element);version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,updated:true,artifact:artifact(),deck,element,image:{ref:element.ref,name:element.name,mime:'image/png'}}));});return;
  }
  if(req.url==='/artifacts/deck/assets'&&req.method==='POST'){
    let bytes=0;req.on('data',chunk=>bytes+=chunk.length);req.on('end',()=>{uploadRequests.push({contentType:req.headers['content-type'],bytes});const element={id:'uploaded-image',type:'image',x:300,y:280,width:760,height:500,z:9,opacity:1,rotation:0,ref:'b'.repeat(64),name:'field-notes.png',fit:'cover'};deck.slides[0].elements.push(element);version++;res.writeHead(201,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,updated:true,artifact:artifact(),deck,element,image:{ref:element.ref,name:element.name,mime:'image/png'}}));});return;
  }
  if(req.url==='/artifacts/deck/copies'&&req.method==='POST'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);copies.push(body);res.writeHead(404,{'content-type':'application/json'});res.end(JSON.stringify({ok:false,partialSuccess:true,error:'deck copy was created, but Files filing failed',artifact:{id:'deck-copy',title:body.title,type:'html_deck',version:1,savedToFiles:false},deck:body.deck,receipt:{outcome:'copy_created_files_failed',artifactId:'deck-copy',artifactVersion:1,contentSaved:true,filingCompleted:false,savedToFiles:false,retryable:true,retryUrl:'/assistant/files/save',retryMethod:'POST',fileName:body.fileName,folderId:body.folderId}}));});return;
  }
  if(req.url==='/assistant/files/save'&&req.method==='POST'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{const body=JSON.parse(raw);fileRetries.push(body);res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,file:{id:body.artifactId,name:body.fileName,folderId:body.folderId}}));});return;
  }
  if(req.url==='/artifacts/export-pptx'&&req.method==='POST'){
    let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{pptxRequests.push(JSON.parse(raw));res.writeHead(200,{'content-type':'application/vnd.openxmlformats-officedocument.presentationml.presentation'});res.end(Buffer.from('mock-pptx'));});return;
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

 // Native text-input history stays native. The deck-level shortcut must not
 // swallow Cmd/Ctrl-Z while the title field owns focus.
 const deckName=page.getByRole('textbox',{name:'Deck name'});
 await deckName.evaluate(node=>{node.focus();node.setSelectionRange(node.value.length,node.value.length);});
 await page.keyboard.press('x');
 await page.keyboard.press(process.platform==='darwin'?'Meta+z':'Control+z');
 assert.equal(await deckName.inputValue(),'Studio proof');
 assert.match(await page.locator('[data-save-state]').textContent(),/^Saved/);

 const slideQuick=page.locator('[data-slide-quick-tools]');
 assert.equal(await slideQuick.isVisible(),true);
 assert.match(await page.locator('[data-slide-quick-copy]').textContent(),/2 already on this slide/);
 const quickGeometry=await slideQuick.evaluate(node=>({clientWidth:node.clientWidth,scrollWidth:node.scrollWidth,buttons:Array.from(node.querySelectorAll('button')).map(button=>button.getBoundingClientRect().toJSON())}));
 assert.ok(quickGeometry.scrollWidth<=quickGeometry.clientWidth,JSON.stringify(quickGeometry));
 quickGeometry.buttons.forEach(rect=>assert.ok(rect.width>0&&rect.height>=40,JSON.stringify(quickGeometry)));
 await page.getByRole('button',{name:'Create imagery with Scout',exact:true}).click();
 assert.equal(await page.getByRole('tab',{name:'Scout'}).getAttribute('aria-selected'),'true');
 assert.equal(await page.evaluate(()=>document.activeElement?.matches('[data-image-prompt]')),true);
 await page.getByRole('tab',{name:'Design'}).click();

 const zoomBaseline=geometry.canvas.width;
 await page.locator('[data-zoom]').fill('140');
 await page.locator('[data-zoom]').dispatchEvent('input');
 await page.waitForTimeout(30);
 const zoomed=await page.evaluate(()=>{const canvas=document.querySelector('.deck-editor__canvas').getBoundingClientRect();const wrap=document.querySelector('.deck-editor__canvas-wrap');return {width:canvas.width,label:document.querySelector('[data-zoom-output]').textContent,scrollWidth:wrap.scrollWidth,clientWidth:wrap.clientWidth};});
 assert.ok(Math.abs(zoomed.width/zoomBaseline-1.4)<.02,JSON.stringify({zoomBaseline,zoomed}));
 assert.equal(zoomed.label,'140%');
 assert.ok(zoomed.scrollWidth>zoomed.clientWidth,JSON.stringify(zoomed));
 await page.locator('[data-zoom]').fill('100');
 await page.locator('[data-zoom]').dispatchEvent('input');

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

	 const inspectorGeometry=await page.locator('.deck-editor__props').evaluate(node=>({clientWidth:node.clientWidth,scrollWidth:node.scrollWidth}));
	 assert.ok(inspectorGeometry.scrollWidth<=inspectorGeometry.clientWidth,JSON.stringify(inspectorGeometry));
	 if(process.env.DECK_STUDIO_SCREENSHOT){await page.screenshot({path:process.env.DECK_STUDIO_SCREENSHOT,fullPage:true});}

	 // iPad landscape keeps every group in view by moving creation tools to a
	 // second toolbar row, rather than silently clipping them.
	 await page.setViewportSize({width:1024,height:768});await page.waitForTimeout(80);
	 const ipad=await page.evaluate(()=>{const rect=node=>node.getBoundingClientRect();const toolbar=rect(document.querySelector('.deck-editor__toolbar'));const right=rect(document.querySelector('.deck-editor__toolbar-right'));const center=document.querySelector('.deck-editor__toolbar-center');const canvas=rect(document.querySelector('.deck-editor__canvas'));const sidebar=document.querySelector('.deck-editor__sidebar');const sidebarRect=rect(sidebar);const controls=Array.from(sidebar.querySelectorAll('.deck-editor__slide-actions > *')).map(node=>({rect:rect(node).toJSON(),clientWidth:node.clientWidth,scrollWidth:node.scrollWidth}));return {toolbar:{right:toolbar.right,bottom:toolbar.bottom},right:{left:right.left,right:right.right,bottom:right.bottom},center:{clientWidth:center.clientWidth,scrollWidth:center.scrollWidth,bottom:rect(center).bottom},canvas:{left:canvas.left,right:canvas.right,width:canvas.width},sidebar:{rect:sidebarRect.toJSON(),clientWidth:sidebar.clientWidth,scrollWidth:sidebar.scrollWidth,controls}};});
	 assert.ok(ipad.right.left>=0&&ipad.right.right<=1024&&ipad.center.scrollWidth<=ipad.center.clientWidth,JSON.stringify(ipad));
	 assert.ok(ipad.center.bottom<=ipad.toolbar.bottom&&ipad.canvas.left>=0&&ipad.canvas.right<=1024,JSON.stringify(ipad));
	 assert.ok(ipad.sidebar.scrollWidth<=ipad.sidebar.clientWidth,JSON.stringify(ipad.sidebar));
	 ipad.sidebar.controls.forEach(control=>{assert.ok(control.rect.left>=ipad.sidebar.rect.left&&control.rect.right<=ipad.sidebar.rect.right&&control.clientWidth>=control.scrollWidth,JSON.stringify(ipad.sidebar));});

	 // Compact tablet keeps direct navigation, Inspector, More, and Save visible;
	 // the inspector is inert while closed and fully on-screen when opened.
	 await page.setViewportSize({width:768,height:1024});await page.waitForTimeout(80);
	 const compact=await page.evaluate(()=>{const visible=node=>{const r=node.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:r.width,height:r.height}};const direct=[document.querySelector('.deck-editor__slide-nav'),document.querySelector('[data-action="toggle-inspector"]'),document.querySelector('[data-action="mobile-tools-menu"]'),document.querySelector('.deck-editor__toolbar-right > [data-action="save"]')].map(visible);const props=document.querySelector('.deck-editor__props');return {direct,propsHidden:props.getAttribute('aria-hidden'),propsInert:props.inert,slideActions:getComputedStyle(document.querySelector('.deck-editor__slide-actions')).display,scrollWidth:document.documentElement.scrollWidth};});
	 compact.direct.forEach(rect=>assert.ok(rect.left>=0&&rect.right<=768&&rect.width>0,JSON.stringify(compact)));
	 assert.equal(compact.propsHidden,'true');assert.equal(compact.propsInert,true);assert.equal(compact.slideActions,'none');assert.ok(compact.scrollWidth<=768,JSON.stringify(compact));
	 await page.getByRole('button',{name:'Inspector',exact:true}).click();
	 await page.waitForFunction(()=>document.querySelector('.deck-editor__props')?.getBoundingClientRect().bottom<=innerHeight);
	 const openInspector=await page.locator('.deck-editor__props').evaluate(node=>({rect:node.getBoundingClientRect().toJSON(),hidden:node.getAttribute('aria-hidden'),inert:node.inert}));
	 assert.ok(openInspector.rect.left>=0&&openInspector.rect.right<=768&&openInspector.rect.bottom<=1024,JSON.stringify(openInspector));
	 assert.equal(openInspector.hidden,null);assert.equal(openInspector.inert,false);
	 if(process.env.DECK_STUDIO_TABLET_SCREENSHOT){await page.screenshot({path:process.env.DECK_STUDIO_TABLET_SCREENSHOT,fullPage:true});}
	 await page.getByRole('button',{name:'Close slide inspector'}).click();
	 await page.setViewportSize({width:1440,height:900});await page.waitForTimeout(80);

	 const downloadPromise=page.waitForEvent('download');
 await page.getByRole('button',{name:'Download',exact:true}).click();
 await page.getByRole('menuitem',{name:/PowerPoint/}).click();
	 const downloaded=await downloadPromise;
	 assert.match(downloaded.suggestedFilename(),/\.pptx$/);
	 assert.deepEqual(pptxRequests,[{artifactId,expectedVersion:version,sceneRef}]);

	 // Keyboard users can select and manipulate canvas objects, use the inspector
	 // tablist, and remain contained inside the modal surface.
	 const keyboardObject=page.locator('[data-scene] [data-element-id="headline"]');
	 await keyboardObject.focus();
	 await page.keyboard.press('Enter');
	 await page.waitForFunction(()=>document.querySelector('[data-scene] [data-element-id="headline"]')?.getAttribute('aria-pressed')==='true');
	 await keyboardObject.focus();
	 const keyboardX=Number(await page.locator('[data-prop="x"]').inputValue());
	 const keyboardWidth=Number(await page.locator('[data-prop="width"]').inputValue());
	 await page.keyboard.press('ArrowRight');
	 assert.equal(Number(await page.locator('[data-prop="x"]').inputValue()),keyboardX+1);
	 await keyboardObject.focus();
	 await page.keyboard.press('Alt+ArrowRight');
	 assert.equal(Number(await page.locator('[data-prop="width"]').inputValue()),keyboardWidth+1);
	 assert.equal(await page.locator('[data-save-state]').textContent(),'Unsaved');
	 await page.locator('[data-deck-position-details] summary').click();
	 const setProp=async(prop,value)=>{const input=page.locator('[data-prop="'+prop+'"]');await input.fill(String(value));await input.dispatchEvent('change');};
	 await setProp('x',-500);
	 assert.equal(Number(await page.locator('[data-prop="x"]').inputValue()),0);
	 await setProp('width',99999);
	 assert.equal(Number(await page.locator('[data-prop="width"]').inputValue()),1920);
	 await setProp('rotation',999);
	 assert.equal(Number(await page.locator('[data-prop="rotation"]').inputValue()),360);
	 await setProp('fontSize',2);
	 assert.equal(Number(await page.locator('[data-prop="fontSize"]').inputValue()),8);
	 await setProp('lineHeight',99);
	 assert.equal(Number(await page.locator('[data-prop="lineHeight"]').inputValue()),4);
	 await setProp('width',keyboardWidth+1);
	 await setProp('x',keyboardX+1);
	 await setProp('rotation',0);
	 await setProp('fontSize',64);
	 await setProp('lineHeight',1.08);
	 await page.keyboard.press('Escape');
	 const designTab=page.getByRole('tab',{name:'Design'});
	 await designTab.focus();
	 await page.keyboard.press('ArrowRight');
	 assert.equal(await page.getByRole('tab',{name:'Notes'}).getAttribute('aria-selected'),'true');
	 await page.keyboard.press('End');
	 assert.equal(await page.getByRole('tab',{name:'Scout'}).getAttribute('aria-selected'),'true');
	 await page.keyboard.press('Home');
	 assert.equal(await designTab.getAttribute('aria-selected'),'true');
	 await page.evaluate(()=>{const editor=document.querySelector('.deck-editor');const focusable=Array.from(editor.querySelectorAll('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex="0"]')).filter(node=>!node.hidden&&!node.closest('[inert]')&&node.getClientRects().length);focusable.at(-1).dataset.deckLastFocus='true';});
	 await page.locator('[data-deck-last-focus="true"]').focus();
	 await page.keyboard.press('Tab');
	 assert.equal(await page.evaluate(()=>document.activeElement?.getAttribute('aria-label')),'Close Deck Studio');

	 await page.locator('[data-scene] [data-element-id="rich-proof"]').dispatchEvent('dblclick');
 await page.locator('.deck-editor__text-input').waitFor({state:'visible'});
	 await page.getByRole('button',{name:'Rectangle',exact:true}).focus();

	 await page.locator('[data-scene] [data-element-id="headline"]').click();
	 const designPanel=page.locator('[data-inspector-panel="design"]');
	 await designPanel.evaluate(node=>{node.scrollTop=node.scrollHeight});
	 assert.ok(await designPanel.evaluate(node=>node.scrollTop)>0,'fixture must exercise a deeply scrolled inspector');
	 await page.locator('[data-scene] [data-element-id="rich-proof"]').click();
	 assert.equal(await designPanel.evaluate(node=>node.scrollTop),0,'selecting another object must reveal its primary formatting controls');
	 await page.locator('[data-scene] [data-element-id="headline"]').click();
	 assert.equal(await page.locator('[data-slide-controls]').isHidden(),true,'selected-object controls should replace slide settings');
	 const selectionSurface=page.locator('[data-element-controls]');
	 const selectionSurfaceState=await selectionSurface.evaluate(node=>({radius:getComputedStyle(node).borderRadius,shadow:getComputedStyle(node).boxShadow,firstLabel:node.querySelector('.deck-editor__prop-label')?.textContent,advancedOpen:node.querySelector('[data-deck-position-details]')?.open,top:node.getBoundingClientRect().top,panelTop:node.closest('[data-inspector-panel]')?.getBoundingClientRect().top}));
	 assert.notEqual(selectionSurfaceState.radius,'0px',JSON.stringify(selectionSurfaceState));
	 assert.notEqual(selectionSurfaceState.shadow,'none',JSON.stringify(selectionSurfaceState));
	 assert.equal(selectionSurfaceState.firstLabel,'Typography',JSON.stringify(selectionSurfaceState));
	 assert.equal(selectionSurfaceState.advancedOpen,true,'the user-opened advanced section should stay open while refining another text object');
	 assert.ok(selectionSurfaceState.top>=selectionSurfaceState.panelTop,JSON.stringify(selectionSurfaceState));
 if(process.env.DECK_STUDIO_INSPECTOR_SCREENSHOT){await page.screenshot({path:process.env.DECK_STUDIO_INSPECTOR_SCREENSHOT,fullPage:true});}
 await page.locator('[data-prop="fontFamily"]').fill('Georgia, Times New Roman, serif');
 await page.locator('[data-prop="fontFamily"]').dispatchEvent('change');
 await page.locator('[data-prop="textAlign"]').selectOption('center');
 await page.locator('[data-prop="lineHeight"]').fill('1.2');
 await page.locator('[data-prop="lineHeight"]').dispatchEvent('change');
 await page.locator('[data-prop="letterSpacing"]').fill('.04em');
 await page.locator('[data-prop="letterSpacing"]').dispatchEvent('change');
 await page.getByRole('tab',{name:'Notes'}).click();
 await page.locator('[data-slide-notes]').fill('Revised opening note [BEAT]');
 await page.locator('[data-slide-notes]').dispatchEvent('change');

 await page.getByRole('tab',{name:'Design'}).click();
 await page.getByRole('button',{name:'Rectangle',exact:true}).click();
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
 await page.getByRole('button',{name:'Bring to front',exact:true}).click();
	 await page.getByRole('button',{name:'Undo'}).click();
	 await page.getByRole('button',{name:'Redo'}).click();
	 await deckName.fill('Studio proof — final');
	 await page.getByRole('button',{name:'Save',exact:true}).click();
	 await page.waitForFunction(()=>{const button=document.querySelector('.deck-editor [data-action="save"]');return button&&!button.disabled&&button.textContent==='Save';});
	 assert.equal(await page.locator('.deck-editor').count(),1,'Save must keep Deck Studio open');
	 assert.match(await page.locator('[data-save-state]').textContent(),/^Saved/);
	 assert.equal(patches.length,1);
 assert.equal(patches[0].artifactId,artifactId);
 assert.equal(patches[0].title,'Studio proof — final');
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
	 // Save is a persistence boundary, not an undo-history reset. The title
	 // rename remains reversible, and redo returns exactly to the saved state.
	 await page.getByRole('button',{name:'Undo'}).click();
	 assert.equal(await deckName.inputValue(),'Studio proof');
	 assert.equal(await page.locator('[data-save-state]').textContent(),'Unsaved');
	 await page.getByRole('button',{name:'Redo'}).click();
	 assert.equal(await deckName.inputValue(),'Studio proof — final');
	 assert.match(await page.locator('[data-save-state]').textContent(),/^Saved/);
	 await page.getByRole('button',{name:'Close Deck Studio'}).click();
	 await page.waitForFunction(()=>!document.querySelector('.deck-editor'));

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
 assert.equal(await page.getByRole('textbox',{name:'Deck name'}).inputValue(),'Studio proof — final');
 await page.getByRole('tab',{name:'Scout'}).click();
 const scoutHint=await page.locator('[data-inspector-panel="scout"] .deck-editor__inspector-hint').evaluate(node=>({whiteSpace:getComputedStyle(node).whiteSpace,clientHeight:node.clientHeight,scrollHeight:node.scrollHeight,text:node.textContent}));
 assert.notEqual(scoutHint.whiteSpace,'nowrap',JSON.stringify(scoutHint));
 assert.ok(scoutHint.clientHeight>=scoutHint.scrollHeight,JSON.stringify(scoutHint));
 assert.match(scoutHint.text,/whole slide/i);
 if(process.env.DECK_STUDIO_SCOUT_SCREENSHOT){await page.screenshot({path:process.env.DECK_STUDIO_SCOUT_SCREENSHOT,fullPage:true});}
 await page.locator('[data-image-prompt]').fill('Documentary wide image of a working farm at first light');
 await page.getByRole('button',{name:'Generate image'}).click();
 await page.waitForFunction(()=>document.querySelector('[data-image-status]')?.textContent.includes('added'));
 assert.equal(imageRequests.length,1);
 assert.equal(imageRequests[0].artifactId,artifactId);
 assert.equal(imageRequests[0].slideId,'slide-one');
 assert.equal(await page.locator('[data-scene] [data-element-id="generated-image"]').count(),1);
 assert.equal(await page.locator('.deck-editor__scout-status').textContent(),'Image generated and added to this slide.');
	 await page.getByRole('button',{name:'Undo'}).click();
	 assert.equal(await page.locator('[data-scene] [data-element-id="generated-image"]').count(),0,'generated imagery must be undoable');
	 await page.getByRole('button',{name:'Redo'}).click();
	 assert.equal(await page.locator('[data-scene] [data-element-id="generated-image"]').count(),1,'generated imagery must be redoable');

 await page.getByRole('tab',{name:'Design'}).click();
 const chooserPromise=page.waitForEvent('filechooser');
 await page.getByRole('button',{name:'Upload',exact:true}).click();
 const chooser=await chooserPromise;
 await chooser.setFiles({name:'field-notes.png',mimeType:'image/png',buffer:Buffer.from('89504e470d0a1a0a','hex')});
 await page.waitForFunction(()=>document.querySelector('[data-image-status]')?.textContent.includes('uploaded'));
 assert.equal(uploadRequests.length,1);
 assert.match(uploadRequests[0].contentType,/^multipart\/form-data; boundary=/);
 assert.ok(uploadRequests[0].bytes>8);
 assert.equal(await page.locator('[data-scene] [data-element-id="uploaded-image"]').count(),1);
	 await page.getByRole('button',{name:'Undo'}).click();
	 assert.equal(await page.locator('[data-scene] [data-element-id="uploaded-image"]').count(),0,'uploaded imagery must be undoable');
	 await page.getByRole('button',{name:'Redo'}).click();
	 assert.equal(await page.locator('[data-scene] [data-element-id="uploaded-image"]').count(),1,'uploaded imagery must be redoable');

 await page.locator('[data-prop="fit"]').selectOption('contain');
 await page.getByRole('button',{name:'Back one',exact:true}).click();
 await page.getByRole('button',{name:'Forward one',exact:true}).click();
 await page.getByRole('button',{name:'Save a copy…',exact:true}).click();
 const copyDialog=page.locator('.drive-save-dialog');
 await copyDialog.waitFor({state:'visible'});
 await copyDialog.getByRole('textbox',{name:'File name'}).fill('Studio proof — team copy');
 await copyDialog.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('.deck-editor')&&!document.querySelector('.drive-save-dialog'));
 await page.waitForFunction(()=>{const button=document.querySelector('.deck-editor [data-action="save-copy"]');return button&&!button.disabled&&button.textContent==='Save a copy…';});
 assert.equal(copies.length,1);
 assert.equal(copies[0].title,'Studio proof — team copy');
 assert.equal(copies[0].deck.slides[0].elements.find(element=>element.id==='uploaded-image').fit,'contain');
 assert.deepEqual(fileRetries,[{artifactId:'deck-copy',fileName:'Studio proof — team copy',folderId:''}]);
 assert.equal(copies.length,1,'Files retry must file the created copy rather than POSTing another deck copy');

	 await page.locator('[data-scene] [data-element-id="rich-proof"]').dispatchEvent('dblclick');
	 await page.locator('.deck-editor__text-input').fill('Edited plain text');
	 await page.getByRole('button',{name:'Save',exact:true}).click();
	 await page.waitForFunction(()=>{const button=document.querySelector('.deck-editor [data-action="save"]');return button&&!button.disabled&&button.textContent==='Save';});
	 assert.equal(await page.locator('.deck-editor').count(),1,'subsequent saves must also stay in Deck Studio');
	 assert.equal(patches.length,2);
	 const flattenedRich=patches[1].deck.slides[0].elements.find(element=>element.id==='rich-proof');
	 assert.equal(flattenedRich.text,'Edited plain text');
	 assert.equal(flattenedRich.richText,'');
	 await page.getByRole('button',{name:'Close Deck Studio'}).click();
	 await page.waitForFunction(()=>!document.querySelector('.deck-editor'));

	 // An async rendered review can admit the exact saved revision while Studio
	 // remains open. A later local edit revokes publication, but Undo must
	 // restore the newly admitted saved capability immediately instead of
	 // waiting for the next polling interval.
	 managed=true;publicationReady=false;
	 await page.evaluate(id=>openDeckStudio(id,'Studio proof',{}),artifactId);
	 await page.waitForSelector('.deck-editor');
	 assert.equal(await page.getByRole('button',{name:'Present',exact:true}).count(),0);
	 await page.waitForTimeout(80);
	 publicationReady=true;
	 await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
	 await page.waitForFunction(()=>{const button=document.querySelector('.deck-editor [data-action="present"]');return button&&!button.hidden&&!button.disabled;});
	 assert.equal(await page.locator('.deck-editor__download').isVisible(),true);
	 await page.getByRole('button',{name:'Rectangle',exact:true}).click();
	 assert.equal(await page.getByRole('button',{name:'Present',exact:true}).count(),0);
	 await page.getByRole('button',{name:'Undo'}).click();
	 assert.equal(await page.getByRole('button',{name:'Present',exact:true}).isVisible(),true,'Undo must restore the newly admitted saved presentation capability immediately');
	 assert.equal(await page.locator('.deck-editor__download').isVisible(),true,'Undo must restore the newly admitted saved export capability immediately');
	 await page.getByRole('button',{name:'Close Deck Studio'}).click();
	 await page.waitForFunction(()=>!document.querySelector('.deck-editor'));
	 managed=false;publicationReady=false;

 canWrite=false;
 deck.slides.push({id:'slide-two',background:'#f2eee5',elements:[{id:'second-title',type:'text',x:180,y:160,width:1300,height:180,z:1,opacity:1,text:'The second slide',fontSize:72,fontWeight:700,color:'#151515'}]});
 await page.evaluate(id=>openDeckStudio(id,'Read-only proof',{}),artifactId);
 await page.waitForTimeout(50);
 assert.equal(await page.locator('.deck-editor').count(),0);
 await page.evaluate(id=>{const host=document.createElement('div');host.id='readonly-deck-host';document.body.appendChild(host);renderArtifactDeck(host,{id,kind:'os_artifact',text:'<!doctype html>',metadata:{type:'html_deck',title:'Read-only proof',savedToFiles:'true'}},{});},artifactId);
 const readonlyHost=page.locator('#readonly-deck-host');
 await readonlyHost.getByRole('button',{name:'Present'}).waitFor({state:'visible'});
 await readonlyHost.locator('.chat-deck__native-preview.is-ready').waitFor({state:'attached'});
 assert.equal(await readonlyHost.locator('.chat-deck__nav-count').textContent(),'1 / 2');
 const nativeNext=readonlyHost.getByRole('button',{name:'Next slide'});
 const nativePrevious=readonlyHost.getByRole('button',{name:'Previous slide'});
 assert.equal(await nativePrevious.isDisabled(),true);
 await nativeNext.click();
 assert.equal(await readonlyHost.locator('.chat-deck__nav-count').textContent(),'2 / 2');
 assert.equal(await readonlyHost.locator('.chat-deck__native-element').filter({hasText:'The second slide'}).count(),1);
 const nativeChromeGeometry=await readonlyHost.evaluate(host=>{const rendered=node=>{const style=getComputedStyle(node);const rect=node.getBoundingClientRect();return !node.hidden&&!node.closest('[hidden],[aria-hidden="true"],[inert]')&&style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0;};const shell=host.querySelector('.chat-deck').getBoundingClientRect();const nav=host.querySelector('.chat-deck__nav').getBoundingClientRect();const actions=host.querySelector('.chat-deck__actions').getBoundingClientRect();const navButtons=Array.from(host.querySelectorAll('.chat-deck__nav button')).filter(rendered).map(node=>node.getBoundingClientRect().toJSON());const actionButtons=Array.from(host.querySelectorAll('.chat-deck__actions .chat-deck__btn')).filter(rendered).map(node=>node.getBoundingClientRect().toJSON());return {shell:shell.toJSON(),nav:nav.toJSON(),actions:actions.toJSON(),navButtons,actionButtons};});
 assert.ok(nativeChromeGeometry.nav.top<nativeChromeGeometry.shell.top+nativeChromeGeometry.shell.height/2,JSON.stringify(nativeChromeGeometry));
 assert.ok(nativeChromeGeometry.actions.top>nativeChromeGeometry.shell.top+nativeChromeGeometry.shell.height/2,JSON.stringify(nativeChromeGeometry));
 nativeChromeGeometry.navButtons.forEach(rect=>assert.ok(rect.width>=40&&rect.height>=40,JSON.stringify(nativeChromeGeometry)));
 nativeChromeGeometry.actionButtons.forEach(rect=>assert.ok(rect.height>=40,JSON.stringify(nativeChromeGeometry)));
 await nativePrevious.click();
 assert.equal(await readonlyHost.locator('.chat-deck__nav-count').textContent(),'1 / 2');
 const cardDownload=page.waitForEvent('download');
 await readonlyHost.getByRole('button',{name:'Download',exact:true}).click();
 await readonlyHost.getByRole('menuitem',{name:/PowerPoint/}).click();
 assert.match((await cardDownload).suggestedFilename(),/\.pptx$/);
 assert.deepEqual(pptxRequests.at(-1),{artifactId,expectedVersion:version,sceneRef});
 const previewBefore=await readonlyHost.locator('.chat-deck__native-preview').boundingBox();
 await page.waitForTimeout(160);
 const stablePreview=await page.evaluate(before=>{const frame=document.querySelector('#readonly-deck-host .chat-deck__native-preview').getBoundingClientRect();return {before,after:{width:frame.width,height:frame.height},opacity:getComputedStyle(document.querySelector('#readonly-deck-host .chat-deck__native-preview')).opacity};},previewBefore);
 assert.ok(Math.abs(stablePreview.before.width-stablePreview.after.width)<0.5&&Math.abs(stablePreview.before.height-stablePreview.after.height)<0.5,JSON.stringify(stablePreview));
 assert.equal(stablePreview.opacity,'1');
 await page.waitForFunction(()=>{const host=document.querySelector('#readonly-deck-host');return host?.querySelector('button')?.disabled===true&&Array.from(host.querySelectorAll('button')).some(button=>button.textContent.includes('Present')&&!button.disabled)});
 const readonlyEdit=readonlyHost.locator('.chat-deck__btn--secondary').filter({hasText:'Edit'});
 assert.equal(await readonlyHost.getByRole('button',{name:'Edit'}).count(),0);
 assert.equal(await readonlyEdit.count(),1);
 assert.equal(await readonlyEdit.evaluate(node=>node.hidden),true);
 assert.equal(await page.locator('.deck-editor').count(),0);
 await readonlyHost.getByRole('button',{name:'Present'}).click();
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
 await page.locator('.deck-presenter').getByRole('button',{name:'Next slide'}).click();
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

func TestDeckStudioRenderedPhoneTouchControls(t *testing.T) {
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
const artifactId='deck-phone-touch';
const deck={schemaVersion:1,width:1920,height:1080,theme:{background:'#10141c'},slides:[
  {id:'one',background:'#10141c',elements:[{id:'headline',type:'text',x:120,y:160,width:1200,height:220,z:1,opacity:1,text:'Touch the canvas',fontSize:76,fontWeight:700,color:'#fff'}]},
  {id:'two',background:'#f2eee5',elements:[{id:'second',type:'text',x:120,y:160,width:1200,height:220,z:1,opacity:1,text:'Move this slide',fontSize:76,fontWeight:700,color:'#111'}]}
]};
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/deck?id='+artifactId){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:{id:artifactId,title:'Phone proof',version:1,sceneRef:'e'.repeat(64),metadata:{savedToFiles:'true'}},deck,canWrite:true}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const context=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,deviceScaleFactor:2});
 const page=await context.newPage();
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(id=>{const host=document.createElement('div');host.id='deck-channel-phone';host.style.cssText='position:fixed;inset:96px 12px auto;z-index:9999';document.body.appendChild(host);renderArtifactDeck(host,{id,title:'Phone proof — a readable launch presentation',text:'<!doctype html>',metadata:{title:'Phone proof — a readable launch presentation',type:'html_deck',savedToFiles:'true'}},{});},artifactId);
 const channelCard=page.locator('#deck-channel-phone .chat-deck');
 await channelCard.getByRole('button',{name:'Edit',exact:true}).waitFor({state:'visible'});
 await page.waitForFunction(()=>{const card=document.querySelector('#deck-channel-phone .chat-deck');return card?.dataset.previewState==='ready'&&Array.from(card.querySelectorAll('button')).some(button=>button.textContent.includes('Present')&&!button.disabled);});
 assert.equal(await channelCard.getByRole('button',{name:'Edit',exact:true}).count(),1);
 assert.equal(await channelCard.getByRole('button',{name:'Present',exact:true}).count(),1);
 assert.equal(await channelCard.getByRole('button',{name:'Download',exact:true}).count(),1);
 const channelGeometry=await channelCard.evaluate(card=>{const rect=node=>node.getBoundingClientRect().toJSON();const title=card.querySelector('.chat-deck__title');const actions=Array.from(card.querySelectorAll('.chat-deck__actions .chat-deck__btn')).filter(button=>{const style=getComputedStyle(button);return !button.hidden&&style.display!=='none'&&style.visibility!=='hidden'&&button.getClientRects().length;});return {card:rect(card),title:rect(title),titleText:title.textContent,titleLines:Math.round(title.scrollHeight/parseFloat(getComputedStyle(title).lineHeight)),actions:actions.map(button=>({name:button.getAttribute('aria-label')||button.textContent.trim(),rect:rect(button)})).sort((a,b)=>a.rect.left-b.rect.left),downloadLabel:getComputedStyle(card.querySelector('.chat-deck__download-label')).display,downloadMore:getComputedStyle(card.querySelector('.chat-deck__download-more')).display,scrollWidth:document.documentElement.scrollWidth};});
 assert.equal(channelGeometry.titleText,'Phone proof — a readable launch presentation');
 assert.ok(channelGeometry.title.width>300&&channelGeometry.titleLines<=2,JSON.stringify(channelGeometry));
 assert.deepEqual(channelGeometry.actions.map(action=>action.name),['Edit','Present','Download']);
 channelGeometry.actions.forEach(action=>assert.ok(action.rect.height>=44&&action.rect.left>=channelGeometry.card.left&&action.rect.right<=channelGeometry.card.right,JSON.stringify(channelGeometry)));
 assert.equal(channelGeometry.downloadLabel,'none');assert.notEqual(channelGeometry.downloadMore,'none');
 assert.ok(channelGeometry.card.left>=0&&channelGeometry.card.right<=390&&channelGeometry.scrollWidth<=390,JSON.stringify(channelGeometry));
 if(process.env.DECK_CHANNEL_PHONE_SCREENSHOT){await page.screenshot({path:process.env.DECK_CHANNEL_PHONE_SCREENSHOT,fullPage:true});}
 await channelCard.getByRole('button',{name:'Download',exact:true}).tap();
 const channelDownloadMenu=channelCard.getByRole('menu');
 await channelDownloadMenu.waitFor({state:'visible'});
 const channelMenuTargets=await channelDownloadMenu.locator('button').evaluateAll(buttons=>buttons.map(button=>button.getBoundingClientRect().toJSON()));
 channelMenuTargets.forEach(rect=>assert.ok(rect.height>=44,JSON.stringify(channelMenuTargets)));
 if(process.env.DECK_CHANNEL_PHONE_MENU_SCREENSHOT){await page.screenshot({path:process.env.DECK_CHANNEL_PHONE_MENU_SCREENSHOT,fullPage:true});}
 await page.evaluate(()=>document.getElementById('deck-channel-phone')?.remove());
 await page.evaluate(id=>openDeckStudio(id,'Phone proof',{}),artifactId);
 await page.waitForSelector('.deck-editor');
 assert.equal(await page.evaluate(()=>matchMedia('(pointer: coarse)').matches),true);
 const firstFrame=await page.locator('.deck-editor').evaluate(node=>({background:getComputedStyle(node).backgroundColor,animation:getComputedStyle(node).animationName,animations:node.getAnimations().length}));
 assert.notEqual(firstFrame.background,'rgba(0, 0, 0, 0)',JSON.stringify(firstFrame));
 assert.equal(firstFrame.animation,'none',JSON.stringify(firstFrame));
 assert.equal(firstFrame.animations,0,JSON.stringify(firstFrame));
 const phone=await page.evaluate(()=>{const rect=node=>node.getBoundingClientRect().toJSON();const direct=[document.querySelector('.deck-editor__slide-nav'),document.querySelector('[data-action="toggle-inspector"]'),document.querySelector('[data-action="mobile-tools-menu"]'),document.querySelector('.deck-editor__toolbar-right > [data-action="save"]')].map(rect);const canvas=rect(document.querySelector('.deck-editor__canvas'));return {direct,canvas,sidebar:getComputedStyle(document.querySelector('.deck-editor__sidebar')).display,scrollWidth:document.documentElement.scrollWidth};});
 phone.direct.forEach(value=>assert.ok(value.left>=0&&value.right<=390&&value.width>=40&&value.height>=40,JSON.stringify(phone)));
 assert.equal(phone.sidebar,'none');
 assert.ok(phone.canvas.left>=0&&phone.canvas.right<=390&&phone.canvas.width>340,JSON.stringify(phone));
 assert.ok(Math.abs(phone.canvas.width/phone.canvas.height-16/9)<.01,JSON.stringify(phone));
 assert.ok(phone.scrollWidth<=390,JSON.stringify(phone));

 const headline=page.locator('[data-scene] [data-element-id="headline"]');
 await headline.tap();
 assert.equal(await headline.getAttribute('aria-pressed'),'true');
 const handleInset=await page.locator('[data-scene] [data-handle="se"]').evaluate(node=>getComputedStyle(node,'::after').inset);
 assert.equal(handleInset,'-16px');

 // Selecting an object is navigation, not an edit. A simple tap followed by
 // Close must never manufacture an unsaved-changes confirmation.
 await page.evaluate(()=>{window.__deckConfirmCalls=0;window.confirm=()=>{window.__deckConfirmCalls++;return false;};});
 await page.getByRole('button',{name:'Close Deck Studio'}).tap();
 await page.waitForFunction(()=>!document.querySelector('.deck-editor'));
 assert.equal(await page.evaluate(()=>window.__deckConfirmCalls),0);
 await page.evaluate(id=>openDeckStudio(id,'Phone proof',{}),artifactId);
 await page.waitForSelector('.deck-editor');

 await page.getByRole('button',{name:'Inspector',exact:true}).tap();
 await page.waitForFunction(()=>document.querySelector('.deck-editor__props')?.getBoundingClientRect().bottom<=innerHeight);
 const inspector=await page.locator('.deck-editor__props').evaluate(node=>({rect:node.getBoundingClientRect().toJSON(),inert:node.inert,hidden:node.getAttribute('aria-hidden')}));
 assert.ok(inspector.rect.left>=0&&inspector.rect.right<=390&&inspector.rect.bottom<=844,JSON.stringify(inspector));
 assert.equal(inspector.inert,false);assert.equal(inspector.hidden,null);
 const inspectorTargets=await page.locator('.deck-editor__props').evaluate(node=>Array.from(node.querySelectorAll('button:not([disabled]),input:not([disabled]),select:not([disabled])')).filter(control=>{const style=getComputedStyle(control);const rect=control.getBoundingClientRect();return !control.hidden&&!control.closest('[hidden],[aria-hidden="true"],[inert]')&&style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0;}).map(control=>({name:control.getAttribute('aria-label')||control.textContent.trim(),rect:control.getBoundingClientRect().toJSON()})));
 inspectorTargets.forEach(target=>assert.ok(target.rect.height>=44,JSON.stringify(target)));
 if(process.env.DECK_STUDIO_PHONE_SCREENSHOT){await page.screenshot({path:process.env.DECK_STUDIO_PHONE_SCREENSHOT,fullPage:true});}
 await page.getByRole('button',{name:'Close slide inspector'}).tap();
 await page.waitForFunction(()=>document.querySelector('.deck-editor__props')?.inert===true);

 await page.getByRole('button',{name:'Next slide'}).tap();
 assert.equal(await page.locator('[data-slide-indicator]').textContent(),'2 / 2');
 await page.getByRole('button',{name:'More'}).tap();
 const menu=page.getByRole('menu',{name:'More deck actions'});
 await menu.waitFor({state:'visible'});
 const menuRect=await menu.evaluate(node=>node.getBoundingClientRect().toJSON());
 assert.ok(menuRect.left>=0&&menuRect.right<=390&&menuRect.top>=0&&menuRect.bottom<=844,JSON.stringify(menuRect));
 await menu.getByRole('menuitem',{name:'Move slide earlier'}).tap();
 assert.equal(await page.locator('[data-slide-indicator]').textContent(),'1 / 2');
 await page.getByRole('button',{name:'More'}).tap();
 await page.getByRole('menuitem',{name:'Move slide later'}).tap();
 assert.equal(await page.locator('[data-slide-indicator]').textContent(),'2 / 2');
 await context.close();await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DECK_STUDIO_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Deck Studio phone touch harness: %v\n%s", err, output)
	}
}

func TestDeckStudioRenderedRichTextEditPreservesSafeInlineRuns(t *testing.T) {
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
const artifactId='deck-rich-text-artifact';
let version=1;
let saved=null;
const original='Lead <span style="color:#ff6600;font-family:Georgia;font-size:64px;font-weight:700">Proof</span> <em style="font-style:italic">now</em>';
let deck={schemaVersion:1,width:1920,height:1080,theme:{background:'#10141c'},slides:[{id:'slide-one',background:'#10141c',elements:[{id:'mixed-runs',type:'text',x:140,y:180,width:1200,height:260,z:1,opacity:1,rotation:0,text:'Lead Proof now',richText:original,fontSize:40,fontFamily:'Arial',fontWeight:600,color:'#ffffff',textAlign:'left',lineHeight:1.08,letterSpacing:'normal'}]}]};
const artifact=()=>({id:artifactId,title:'Rich text proof',version,sceneRef:'d'.repeat(64),metadata:{type:'html_deck',artifactVersion:String(version),savedToFiles:'true'}});
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/deck?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:artifact(),deck,canWrite:true}));}
  if(req.url==='/artifacts/deck'&&req.method==='PATCH'){
    let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{const body=JSON.parse(raw);saved=body.deck.slides[0].elements[0];deck=body.deck;version++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact:artifact(),deck}));});return;
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
 await page.evaluate(id=>openDeckStudio(id,'Rich text proof',{}),artifactId);
 await page.locator('[data-scene] [data-element-id="mixed-runs"]').dispatchEvent('dblclick');
 const editor=page.locator('.deck-editor__text-input');
 await editor.waitFor({state:'visible'});
 assert.equal(await editor.getAttribute('contenteditable'),'true');
 await editor.evaluate(node=>{const span=node.querySelector('span');span.firstChild.nodeValue='Proof point';node.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText'}));});
 assert.equal(await editor.locator('span').count(),1);
 assert.equal(await editor.locator('em').count(),1);
 await editor.evaluate(node=>{node.querySelector('span').setAttribute('onclick','window.__unsafe=true');const image=document.createElement('img');image.src='x';image.setAttribute('onerror','window.__unsafe=true');node.appendChild(image);node.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertFromPaste'}));});
 assert.equal(await editor.locator('img').count(),0);
 assert.equal(await editor.locator('[onclick],[onerror]').count(),0);
 await page.getByRole('button',{name:'Save',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('.deck-editor [data-action="save"]')?.textContent==='Save'&&!document.querySelector('.deck-editor [data-action="save"]')?.disabled);
 assert.ok(saved);
 assert.equal(saved.text,'Lead Proof point now');
 assert.equal(saved.richText,'Lead <span style="color:#ff6600;font-family:Georgia;font-size:64px;font-weight:700">Proof point</span> <em style="font-style:italic">now</em>');
 assert.doesNotMatch(saved.richText,/(?:onclick|onerror|<img|javascript:)/i);
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DECK_STUDIO_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Deck Studio rich-text harness: %v\n%s", err, output)
	}
}
