package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStudioDeepLinksOpenAuthenticatedSurfacesAndReturnToFiles(t *testing.T) {
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
const html=fs.readFileSync(process.env.STUDIO_DEEPLINK_INDEX,'utf8');
const deckId='deck-deep-link';
const documentId='document-deep-link';
const documentDigest='e'.repeat(64);
const requests=[];
const deck={schemaVersion:1,width:1920,height:1080,theme:{background:'#15191f'},slides:[{id:'cover',background:'#15191f',notes:'Opening note.',elements:[{id:'headline',type:'text',x:160,y:160,width:1180,height:220,z:2,opacity:1,rotation:0,text:'Deep-link proof',fontSize:82,fontFamily:'Arial',fontWeight:700,color:'#ffffff',textAlign:'left',lineHeight:1.05,letterSpacing:'normal'}]}]};
const deckArtifact={id:deckId,title:'Deep-link deck',version:2,savedToFiles:true,metadata:{title:'Deep-link deck',type:'html_deck',savedToFiles:'true',artifactVersion:'2'}};
const documentArtifact={id:documentId,title:'Insights report',version:3,contentDigest:documentDigest,savedToFiles:true,metadata:{title:'Insights report',type:'markdown',savedToFiles:'true',artifactVersion:'3',contentDigest:documentDigest}};
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){
    if(req.headers['x-test-auth']==='denied'){res.writeHead(401,{'content-type':'application/json'});return res.end(JSON.stringify({error:'not signed in'}));}
    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));
  }
  if(req.url==='/artifacts/deck?id='+deckId&&req.method==='GET'){
    requests.push({kind:'deck',id:deckId});res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:deckArtifact,deck,canWrite:true}));
  }
  if(req.url==='/artifacts/document?id='+documentId&&req.method==='GET'){
    requests.push({kind:'document',id:documentId});res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:documentArtifact,document:{schemaVersion:1,markdown:'# Insights report\n\n## Opportunity\n\nA grounded first draft.'},canWrite:true}));
  }
  if(req.url==='/artifacts?id='+documentId&&req.method==='GET'){
    requests.push({kind:'document-read',id:documentId});res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({artifacts:[{...documentArtifact,text:'# Insights report\n\n## Opportunity\n\nA grounded first draft.'}]}));
  }
  if(req.url.startsWith('/artifacts/deck?id=')||req.url.startsWith('/artifacts/document?id=')){
    requests.push({kind:'unexpected-artifact',url:req.url});res.writeHead(404,{'content-type':'application/json'});return res.end(JSON.stringify({error:'artifact not found'}));
  }
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')||req.url.startsWith('/brain/')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
const count=kind=>requests.filter(request=>request.kind===kind).length;
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const origin='http://127.0.0.1:'+server.address().port;
  const browser=await chromium.launch({headless:true});

  // Desktop edit links open the full Deck Studio rather than the JSON API.
  const desktop=await browser.newPage({viewport:{width:1440,height:900}});
  await desktop.goto(origin+'/studio/deck/'+deckId+'?mode=edit',{waitUntil:'domcontentloaded'});
  const deckEditor=desktop.locator('.deck-editor');
  await deckEditor.waitFor({state:'visible'});
  assert.equal(count('deck'),1,JSON.stringify(requests));
  assert.equal(await deckEditor.getAttribute('aria-label'),'Deck Studio — Deep-link deck');
  const desktopGeometry=await deckEditor.evaluate(node=>({rect:node.getBoundingClientRect().toJSON(),viewport:{width:innerWidth,height:innerHeight},scrollWidth:document.documentElement.scrollWidth}));
  assert.equal(desktopGeometry.rect.width,desktopGeometry.viewport.width,JSON.stringify(desktopGeometry));
  assert.equal(desktopGeometry.rect.height,desktopGeometry.viewport.height,JSON.stringify(desktopGeometry));
  assert.ok(desktopGeometry.scrollWidth<=desktopGeometry.viewport.width,JSON.stringify(desktopGeometry));
  await deckEditor.getByRole('button',{name:'Close Deck Studio'}).click();
  await deckEditor.waitFor({state:'detached'});
  await desktop.waitForFunction(()=>location.pathname==='/files');
  assert.equal(new URL(desktop.url()).pathname,'/files');
  assert.equal(await desktop.locator('#appShell').getAttribute('data-pd1-destination'),'Files');
  assert.equal(await desktop.locator('#appShell').getAttribute('data-tool'),'files');

  // Phone present links keep the stage and every direct control inside the
  // viewport, then return to Files when the presentation closes.
  const phone=await browser.newPage({viewport:{width:390,height:844}});
  await phone.goto(origin+'/studio/deck/'+deckId+'?mode=present',{waitUntil:'domcontentloaded'});
  const presenter=phone.locator('.deck-presenter');
  await presenter.waitFor({state:'visible'});
  assert.equal(count('deck'),2,JSON.stringify(requests));
  assert.equal(await presenter.getByText('1 / 1',{exact:true}).textContent(),'1 / 1');
  const phoneGeometry=await presenter.evaluate(node=>{const rect=element=>element.getBoundingClientRect().toJSON();return {root:rect(node),stage:rect(node.querySelector('[data-present-stage]')),controls:Array.from(node.querySelectorAll('button')).map(rect),viewport:{width:innerWidth,height:innerHeight},scrollWidth:document.documentElement.scrollWidth};});
  assert.ok(phoneGeometry.root.left>=0&&phoneGeometry.root.right<=phoneGeometry.viewport.width&&phoneGeometry.root.bottom<=phoneGeometry.viewport.height,JSON.stringify(phoneGeometry));
  assert.ok(Math.abs(phoneGeometry.stage.width/phoneGeometry.stage.height-16/9)<0.02,JSON.stringify(phoneGeometry));
  phoneGeometry.controls.forEach(rect=>assert.ok(rect.left>=0&&rect.right<=phoneGeometry.viewport.width&&rect.top>=0&&rect.bottom<=phoneGeometry.viewport.height,JSON.stringify(phoneGeometry)));
  assert.ok(phoneGeometry.scrollWidth<=phoneGeometry.viewport.width,JSON.stringify(phoneGeometry));
  await presenter.getByRole('button',{name:'Close',exact:true}).click();
  await presenter.waitFor({state:'detached'});
  await phone.waitForFunction(()=>location.pathname==='/files');
  assert.equal(await phone.locator('#appShell').getAttribute('data-pd1-destination'),'Files');

  // iPad document links open the editable native paper at a usable width.
  const ipad=await browser.newPage({viewport:{width:1024,height:768}});
  const ipadErrors=[];ipad.on('pageerror',error=>ipadErrors.push(String(error)));ipad.on('console',message=>{if(message.type()==='error')ipadErrors.push(message.text())});
  await ipad.goto(origin+'/studio/document/'+documentId+'?mode=edit',{waitUntil:'domcontentloaded'});
  const documentEditor=ipad.locator('.document-editor');
  try { await documentEditor.waitFor({state:'visible',timeout:5000}); }
  catch(error) { throw new Error('Document deep link did not open: '+JSON.stringify({url:ipad.url(),requests,authed:await ipad.locator('#appShell.is-authed').count(),toasts:await ipad.locator('.toast').allTextContents(),errors:ipadErrors})+'; '+error); }
  assert.equal(count('document'),1,JSON.stringify(requests));
  assert.equal(await documentEditor.getByRole('textbox',{name:'Document name'}).inputValue(),'Insights report');
  const ipadGeometry=await documentEditor.evaluate(node=>{const paper=node.querySelector('.document-editor__paper').getBoundingClientRect();const root=node.getBoundingClientRect();return {root:root.toJSON(),paper:paper.toJSON(),viewport:{width:innerWidth,height:innerHeight},scrollWidth:document.documentElement.scrollWidth};});
  assert.equal(ipadGeometry.root.width,ipadGeometry.viewport.width,JSON.stringify(ipadGeometry));
  assert.equal(ipadGeometry.root.height,ipadGeometry.viewport.height,JSON.stringify(ipadGeometry));
  assert.ok(ipadGeometry.paper.width>600&&ipadGeometry.paper.left>=0&&ipadGeometry.paper.right<=ipadGeometry.viewport.width,JSON.stringify(ipadGeometry));
  assert.ok(ipadGeometry.scrollWidth<=ipadGeometry.viewport.width,JSON.stringify(ipadGeometry));
  await documentEditor.getByRole('button',{name:'Close Document Studio'}).click();
  await documentEditor.waitFor({state:'detached'});
  await ipad.waitForFunction(()=>location.pathname==='/files');
  assert.equal(await ipad.locator('#appShell').getAttribute('data-pd1-destination'),'Files');

  // Inside the iOS shell, Studio closes through one exact, versioned bridge
  // message. The web document is removed, but navigation stays on the Studio
  // URL because the native modal owns dismissal (OSWebScreen calls goBack).
  const nativeContext=await browser.newContext({viewport:{width:390,height:844},userAgent:'Mozilla/5.0 Stride-Expo/73'});
  const native=await nativeContext.newPage();
  await native.addInitScript(()=>{
    window.__studioCloseMessages=[];
    window.ReactNativeWebView={postMessage:value=>window.__studioCloseMessages.push(value)};
  });
  await native.goto(origin+'/studio/document/'+documentId+'?mode=edit',{waitUntil:'domcontentloaded'});
  const nativeDocumentEditor=native.locator('.document-editor');
  await nativeDocumentEditor.waitFor({state:'visible'});
  assert.equal(count('document'),2,JSON.stringify(requests));
  await nativeDocumentEditor.getByRole('button',{name:'Close Document Studio'}).click();
  await nativeDocumentEditor.waitFor({state:'detached'});
  await native.waitForFunction(()=>window.__studioCloseMessages.length===1);
  const nativeClose=JSON.parse(await native.evaluate(()=>window.__studioCloseMessages[0]));
  assert.deepEqual(nativeClose,{type:'stride.studio.close',version:1,kind:'document',mode:'edit',artifactId:documentId});
  assert.equal(new URL(native.url()).pathname,'/studio/document/'+documentId);

  const nativeView=await nativeContext.newPage();
  await nativeView.addInitScript(()=>{
    window.__studioCloseMessages=[];
    window.ReactNativeWebView={postMessage:value=>window.__studioCloseMessages.push(value)};
  });
  await nativeView.goto(origin+'/studio/document/'+documentId+'?mode=view',{waitUntil:'domcontentloaded'});
  const nativeReader=nativeView.locator('.artifact-stage');
  await nativeReader.waitFor({state:'visible'});
  assert.equal(count('document-read'),1,JSON.stringify(requests));
  assert.equal(await nativeReader.getByRole('button',{name:'Edit document'}).count(),0);
  await nativeReader.getByRole('button',{name:'Close'}).click();
  await nativeReader.waitFor({state:'detached'});
  await nativeView.waitForFunction(()=>window.__studioCloseMessages.length===1);
  const nativeViewClose=JSON.parse(await nativeView.evaluate(()=>window.__studioCloseMessages[0]));
  assert.deepEqual(nativeViewClose,{type:'stride.studio.close',version:1,kind:'document',mode:'view',artifactId:documentId});
  await nativeContext.close();

  // A completed Markdown ResultArtifact is a bounded, first-class document in
  // the feed. Its two direct actions reach the reader and native editor; a
  // process artifact without resultArtifactId never enters this function.
  await desktop.evaluate(({documentId,documentDigest})=>{
    const digest=documentDigest;
    const artifact={id:documentId,title:'Insights report',text:'# Insights report\n\n## Opportunity\n\nA western-culture engagement network can turn distributed creator trust into on-demand launch energy.\n\n## Signal table\n\n| Signal | What it suggests | Decision use |\n|---|---|---|\n| Distributed creator trust across regional communities | Thousands of creators can coordinate around experience launches without flattening their individual voices | Test a measured activation cohort before scaling the network |\n| On-demand posting moments | Concentrated release windows can create useful reach while preserving source attribution | Define opt-in briefs, disclosure rules, and outcome instrumentation |\n\n## Proof points\n\nThe report separates measured evidence from assumptions.',version:3,metadata:{title:'Insights report',type:'markdown',status:'complete',mode:'report',artifactVersion:'3',contentDigest:digest}};
    const message={id:'document-result-message',kind:'thread',thread:{artifactId:'goal-document',mode:'goal',goalStatus:'completed',resultArtifactId:documentId,resultArtifactType:'markdown',resultArtifactVersion:3,resultArtifactDigest:digest,resultTitle:'Insights report',resultCanEdit:true}};
    artifactEntries=[artifact];
    scoutChatThreads=[{id:'document-result-thread',messages:[message]}];
    activeScoutThreadId='document-result-thread';
    document.body.appendChild(scoutMarkdownDocumentRefRecordNode(message,artifact));
  },{documentId,documentDigest});
  const documentResult=desktop.locator('.scout-chat-document-result');
  await documentResult.waitFor({state:'visible'});
  assert.equal(await documentResult.getByRole('heading',{name:'Insights report'}).count(),1);
  assert.match(await documentResult.locator('.scout-chat-document-result__preview').innerText(),/Opportunity[\s\S]*western-culture engagement network/);
  const resultPreviewHeight=await documentResult.locator('.scout-chat-document-result__preview').evaluate(node=>node.getBoundingClientRect().height);
  assert.ok(resultPreviewHeight<=330,JSON.stringify({resultPreviewHeight}));
  await documentResult.getByRole('button',{name:'Edit Insights report'}).click();
  const resultEditor=desktop.locator('.document-editor');
  await resultEditor.waitFor({state:'visible'});
  await resultEditor.getByRole('button',{name:'Close Document Studio'}).click();
  await resultEditor.waitFor({state:'detached'});
  await documentResult.getByRole('button',{name:'Open Insights report'}).click();
  const resultReader=desktop.locator('.artifact-stage');
  await resultReader.waitFor({state:'visible'});
  await resultReader.getByRole('button',{name:'Close'}).click();
  await resultReader.waitFor({state:'detached'});

  await desktop.setViewportSize({width:390,height:844});await desktop.waitForTimeout(60);
  const teaserGeometry=await documentResult.evaluate(card=>{const rect=node=>node.getBoundingClientRect().toJSON();const preview=card.querySelector('.scout-chat-document-result__preview');const wrap=card.querySelector('.artifact-read__table-wrap');const table=card.querySelector('.artifact-read__table');const controls=Array.from(card.querySelectorAll('button')).filter(button=>{const style=getComputedStyle(button);const bounds=button.getBoundingClientRect();return !button.hidden&&style.display!=='none'&&style.visibility!=='hidden'&&bounds.width>0&&bounds.height>0;}).map(button=>({name:button.getAttribute('aria-label')||button.textContent.trim(),rect:rect(button)}));return {card:rect(card),preview:rect(preview),wrap:rect(wrap),table:rect(table),wrapClientWidth:wrap.clientWidth,tableScrollWidth:table.scrollWidth,cellWhiteSpace:getComputedStyle(table.querySelector('td')).whiteSpace,controls,scrollWidth:document.documentElement.scrollWidth};});
  assert.ok(teaserGeometry.card.left>=0&&teaserGeometry.card.right<=390&&teaserGeometry.scrollWidth<=390,JSON.stringify(teaserGeometry));
  assert.ok(teaserGeometry.wrap.left>=teaserGeometry.preview.left&&teaserGeometry.wrap.right<=teaserGeometry.preview.right+1,JSON.stringify(teaserGeometry));
  assert.ok(teaserGeometry.table.left>=teaserGeometry.wrap.left&&teaserGeometry.table.right<=teaserGeometry.wrap.right+1,JSON.stringify(teaserGeometry));
  assert.ok(teaserGeometry.tableScrollWidth<=teaserGeometry.wrapClientWidth+1,JSON.stringify(teaserGeometry));
  assert.equal(teaserGeometry.cellWhiteSpace,'normal',JSON.stringify(teaserGeometry));
  teaserGeometry.controls.forEach(control=>assert.ok(control.rect.height>=44,JSON.stringify(teaserGeometry)));
  if(process.env.STUDIO_RESULT_PHONE_SCREENSHOT){await desktop.screenshot({path:process.env.STUDIO_RESULT_PHONE_SCREENSHOT,fullPage:true});}

  // Authentication and route validation happen before artifact retrieval.
  const artifactRequestsBeforeGuards=requests.length;
  const signedOut=await browser.newPage({viewport:{width:390,height:844},extraHTTPHeaders:{'x-test-auth':'denied'}});
  await signedOut.goto(origin+'/studio/deck/'+deckId+'?mode=edit',{waitUntil:'domcontentloaded'});
  await signedOut.waitForTimeout(250);
  assert.equal(await signedOut.locator('.deck-editor,.deck-presenter,.document-editor').count(),0);
  assert.equal(requests.length,artifactRequestsBeforeGuards,JSON.stringify(requests));

  const invalid=await browser.newPage({viewport:{width:1024,height:768}});
  await invalid.goto(origin+'/studio/deck/'+deckId+'?mode=explode',{waitUntil:'domcontentloaded'});
  await invalid.waitForSelector('#appShell.is-authed');
  await invalid.waitForTimeout(250);
  assert.equal(await invalid.locator('.deck-editor,.deck-presenter,.document-editor').count(),0);
  assert.equal(requests.length,artifactRequestsBeforeGuards,JSON.stringify(requests));

  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "STUDIO_DEEPLINK_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Studio deep-link harness: %v\n%s", err, output)
	}
}
