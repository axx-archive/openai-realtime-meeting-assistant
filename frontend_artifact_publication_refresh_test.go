package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Publication controls are leases, not durable grants. This rendered harness
// keeps every projected result flag stale-positive while the server revokes the
// exact revision, then proves each open surface closes its final-output doors.
func TestManagedArtifactPublicationControlsRefreshAndFailClosedRendered(t *testing.T) {
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
const html=fs.readFileSync(process.env.PUBLICATION_REFRESH_INDEX,'utf8');
const deckId='managed-deck-refresh';
const documentId='managed-document-refresh';
const pdfRef='a'.repeat(64);
const documentDigest='d'.repeat(64);
let admitted=true;
let capabilityRequests=0;
let exportRequests=0;
const deck={schemaVersion:1,width:1920,height:1080,slides:[{id:'cover',background:'#15191f',elements:[{id:'title',type:'text',x:160,y:160,width:1200,height:220,z:1,opacity:1,rotation:0,text:'Reviewed deck',fontSize:84,fontFamily:'Arial',fontWeight:700,color:'#fff',textAlign:'left',lineHeight:1.05,letterSpacing:'normal'}]}]};
const json=(res,status,payload)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify(payload));};
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me')return json(res,200,{email:'aj@shareability.com',name:'AJ',shellAccess:'full'});
  if(req.url==='/readyz')return json(res,200,{ok:true,checks:{agents:{renderRunner:{heartbeatOK:true}}}});
  if(req.url==='/test/admit'){admitted=true;return json(res,200,{ok:true});}
  if(req.url==='/test/revoke'){admitted=false;return json(res,200,{ok:true});}
  if(req.url==='/test/state')return json(res,200,{admitted,capabilityRequests,exportRequests});
  if(req.url.startsWith('/artifacts/final-export-capability')){
    capabilityRequests++;
    const id=new URL(req.url,'http://127.0.0.1').searchParams.get('id');
    const artifactVersion=id===deckId?2:3;
    return json(res,200,{ok:true,artifactId:id,artifactVersion,qualityState:admitted?'admitted':'draft_needs_attention',managed:true,canExport:admitted});
  }
  if(req.url==='/artifacts/deck?id='+deckId&&req.method==='GET')return json(res,200,{ok:true,artifact:{id:deckId,title:'Reviewed deck',version:2},deck,canWrite:true,qualityState:'admitted',canPresent:true,canExport:true});
  if(req.url==='/artifacts/document?id='+documentId&&req.method==='GET')return json(res,200,{ok:true,artifact:{id:documentId,title:'Reviewed report',version:3},document:{schemaVersion:1,markdown:'# Reviewed report\n\nEvidence-backed copy.'},canWrite:true,qualityState:'admitted',canExport:true});
  if((req.url==='/artifacts/export-pdf'||req.url==='/artifacts/export-pptx')&&req.method==='POST'){exportRequests++;return json(res,409,{error:'should remain blocked'});}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')||req.url.startsWith('/brain/'))return json(res,503,{});
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:1000}});
  const pageErrors=[];page.on('pageerror',error=>pageErrors.push(error.message));
  const origin='http://127.0.0.1:'+server.address().port;
  await page.goto(origin,{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.evaluate(({deckId,documentId,pdfRef,documentDigest})=>{
    const pdf={ref:pdfRef,kind:'pdf',mime:'application/pdf',name:'reviewed-report.pdf'};
    const deckEntry={id:deckId,text:'<!doctype html><html><body>deck</body></html>',metadata:{title:'Reviewed deck',type:'html_deck',status:'approved',artifactVersion:'2',goalId:'deck-goal'}};
    const documentEntry={id:documentId,text:'# Reviewed report\n\nEvidence-backed copy.',metadata:{title:'Reviewed report',type:'markdown',status:'approved',artifactVersion:'3',contentDigest:documentDigest,goalId:'document-goal',assets:JSON.stringify([pdf]),renderPdfArtifactVersion:'3',renderPdfAssetRef:pdfRef}};
    artifactEntries=[deckEntry,documentEntry];
    const resultMessage={id:'document-result',kind:'thread',thread:{artifactId:'document-goal',mode:'goal',goalStatus:'completed',resultArtifactId:documentId,resultArtifactType:'markdown',resultArtifactVersion:3,resultArtifactDigest:documentDigest,resultTitle:'Reviewed report',resultQualityState:'admitted',resultCanEdit:true,resultCanPresent:false,resultCanExport:true}};
    scoutChatThreads=[{id:'refresh-thread',messages:[resultMessage]}];
    activeScoutThreadId='refresh-thread';
    globalThis.__refreshFixtures={deckEntry,documentEntry,resultMessage};
  },{deckId,documentId,pdfRef,documentDigest});

  // Intelligence learns the initial admission, then revokes itself while idle.
  await page.evaluate(id=>{selectedArtifactId=id;renderArtifactDetail()},documentId);
  await page.waitForFunction(id=>artifactFinalExportCapabilityCache?.get(id)?.canExport===true,documentId);
  await page.evaluate(()=>renderArtifactDetail());
  assert.equal(await page.locator('#artifactShareButton').isDisabled(),false);
  assert.equal(await page.locator('#artifactPublishButton').isDisabled(),true);
  assert.equal(await page.locator('#artifactReadPane object[type="application/pdf"]').count(),1);
  const beforeIdle=(await (await page.request.get(origin+'/test/state')).json()).capabilityRequests;
  await page.request.get(origin+'/test/revoke');
  await page.waitForFunction(()=>document.querySelector('#artifactShareButton')?.disabled===true&&document.querySelectorAll('#artifactReadPane object[type="application/pdf"],#artifactReadPane a[download$=".pdf"]').length===0,null,{timeout:5500});
  const afterIdle=(await (await page.request.get(origin+'/test/state')).json()).capabilityRequests;
  assert.ok(afterIdle-beforeIdle>=1&&afterIdle-beforeIdle<=3,{beforeIdle,afterIdle});

  // A stale-positive document toolbar rechecks at click time, before export.
  await page.request.get(origin+'/test/admit');
  await page.evaluate(({documentId})=>openDocumentStudio(documentId,'Reviewed report',{entry:__refreshFixtures.documentEntry,qualityState:'admitted',canExport:true}),{documentId});
  const documentStudio=page.locator('.document-editor');
  await documentStudio.waitFor({state:'visible'});
  await documentStudio.locator('[data-doc-action="pdf"]').waitFor({state:'visible'});
  await page.request.get(origin+'/test/revoke');
  await documentStudio.locator('[data-doc-action="pdf"]').click();
  await page.waitForFunction(()=>document.querySelector('[data-doc-action="pdf"]')?.hidden===true);
	assert.equal((await (await page.request.get(origin+'/test/state')).json()).exportRequests,0);
	await documentStudio.getByRole('button',{name:'Close Document Studio'}).click();

	// The standalone reader stage also keeps a live lease while it is open.
	await page.request.get(origin+'/test/admit');
	await page.evaluate(({documentId})=>openArtifactStage(documentId,'Reviewed report',{qualityState:'admitted',canExport:true}),{documentId});
	const readerStage=page.locator('.artifact-stage');
	await readerStage.waitFor({state:'visible'});
	await readerStage.locator('.artifact-stage__read object[type="application/pdf"]').waitFor({state:'attached'});
	await page.request.get(origin+'/test/revoke');
	await page.waitForFunction(()=>document.querySelectorAll('.artifact-stage .artifact-stage__read object[type="application/pdf"],.artifact-stage .artifact-stage__read a[download$=".pdf"]').length===0,null,{timeout:5500});
	await readerStage.getByRole('button',{name:'Close'}).click();

	// Deck Studio refreshes in place within the four-second lease and on focus.
  await page.request.get(origin+'/test/admit');
  await page.evaluate(({deckId})=>openDeckStudio(deckId,'Reviewed deck',{entry:__refreshFixtures.deckEntry,qualityState:'admitted',canPresent:true,canExport:true}),{deckId});
  const deckStudio=page.locator('.deck-editor');
  await deckStudio.waitFor({state:'visible'});
	await deckStudio.locator('[data-action="present"]:visible').first().waitFor({state:'visible'});
  assert.equal(await deckStudio.locator('.deck-editor__download:visible').count(),1);
  await page.request.get(origin+'/test/revoke');
  await page.waitForFunction(()=>document.querySelectorAll('.deck-editor [data-action="present"]:not([hidden]),.deck-editor .deck-editor__download:not([hidden])').length===0,null,{timeout:5500});
  await page.request.get(origin+'/test/admit');
  await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
	await deckStudio.locator('[data-action="present"]:visible').first().waitFor({state:'visible'});
  await page.request.get(origin+'/test/revoke');
	await deckStudio.locator('[data-action="present"]:visible').first().click();
  await page.waitForFunction(()=>document.querySelector('.deck-editor [data-action="present"]')?.hidden===true);
  assert.equal((await (await page.request.get(origin+'/test/state')).json()).exportRequests,0);
  await deckStudio.getByRole('button',{name:'Close Deck Studio'}).click();

  // The channel document card opens only after a fresh exact-revision grant.
  // Build 18 keeps non-deck manifest rows permanently fail-closed because the
  // manifest itself does not carry an exact result-receipt binding.
  await page.request.get(origin+'/test/admit');
  await page.evaluate(()=>{
    const card=scoutMarkdownDocumentRefRecordNode(__refreshFixtures.resultMessage,__refreshFixtures.documentEntry);card.id='refresh-document-card';document.body.append(card);
    const manifest=scoutManifestCardNode({id:'manifest-refresh',kind:'manifest',manifest:{goalId:'document-goal',status:'shipped',title:'Reviewed package',shareArtifactId:'managed-document-refresh',deliverables:[{badge:'paper',title:'Reviewed report',artifactId:'managed-document-refresh',pdfRef:'a'.repeat(64),pdfName:'reviewed-report.pdf'}]}});manifest.id='refresh-manifest';document.body.append(manifest);
  });
  await page.locator('#refresh-document-card object[type="application/pdf"]').waitFor({state:'attached'});
  assert.equal(await page.locator('#refresh-manifest a[download$=".pdf"]:visible,#refresh-manifest .manifest-card__more:visible').count(),0);
  await page.request.get(origin+'/test/revoke');
  await page.waitForFunction(()=>document.querySelectorAll('#refresh-document-card object[type="application/pdf"],#refresh-document-card a[download$=".pdf"],#refresh-manifest a[download$=".pdf"]:not([hidden]),#refresh-manifest .manifest-card__more:not([hidden])').length===0,null,{timeout:5500});

  assert.deepEqual(pageErrors,[]);
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PUBLICATION_REFRESH_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered publication refresh harness: %v\n%s", err, output)
	}
}
