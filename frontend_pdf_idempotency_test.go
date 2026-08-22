package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStudioPDFControlsKeepTruthfulResumableRenderingState(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		"async function currentArtifactPdfAsset(artifactId)",
		"button.dataset.pdfPending = 'true'",
		"Rendering · check",
		"Rendering PDF · check",
		"PDF rendering continues. Choose again to check progress.",
		"kind: 'note'",
		"let landed = await currentArtifactPdfAsset(state.artifactId)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("resumable PDF UI contract missing %q", want)
		}
	}
	for _, boundary := range []string{"async function exportDocumentPDF(button)", "async function exportPDF(button)"} {
		body := functionBody(html, boundary)
		if !strings.Contains(body, "currentArtifactPdfAsset(state.artifactId)") || !strings.Contains(body, "waitForArtifactPdfAsset(state.artifactId)") {
			t.Errorf("%s must check a late asset before resuming the exact render job", boundary)
		}
		if strings.Contains(body, "throw new Error('PDF is still rendering") {
			t.Errorf("%s still treats an honest polling timeout as an export failure", boundary)
		}
	}
}

func TestDeckAndDocumentStudioRenderedPDFTimeoutRetryAndLateAsset(t *testing.T) {
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
const html=fs.readFileSync(process.env.PDF_IDEMPOTENCY_INDEX,'utf8');
let harnessStage='boot';let debugPage=null;const harnessTimeout=setTimeout(async()=>{const detail=debugPage?await debugPage.evaluate(()=>({button:document.querySelector('[data-action="export-pdf"]')?.outerHTML,toasts:window.__pdfTestToasts||[],waiter:String(waitForArtifactPdfAsset)})).catch(()=>null):null;console.error('timeout at '+harnessStage,JSON.stringify({detail,exportRequests}));process.exit(2)},10000);
const deckId='pdf-retry-deck';const documentId='pdf-retry-document';
const sceneRef='c'.repeat(64);const deckPdfRef='d'.repeat(64);const documentPdfRef='e'.repeat(64);
let deckVersion=4;let documentVersion=7;let deckPending=false;let documentPending=false;let deckReady=false;let documentReady=false;
const exportRequests=[];
const pdfAsset=(ref,name)=>({ref,mime:'application/pdf',name,kind:'pdf'});
function artifact(id){
 if(id===deckId){
  const asset=pdfAsset(deckPdfRef,'Creator engine.pdf');
  return {id,title:'Creator engine',text:'',metadata:{title:'Creator engine',type:'html_deck',artifactVersion:String(deckVersion),deckSceneRef:sceneRef,renderJobId:deckPending&&!deckReady?'deck-job-1':'',renderStatus:deckReady?'complete':deckPending?'running':'',renderPdfArtifactVersion:deckReady?String(deckVersion):'',renderPdfSourceSceneRef:deckReady?sceneRef:'',renderPdfAssetRef:deckReady?deckPdfRef:'',assets:deckReady?JSON.stringify([asset]):''}};
 }
 const asset=pdfAsset(documentPdfRef,'Market opportunity.pdf');
 return {id,title:'Market opportunity',text:'# Market opportunity\n\nA first-class report.',metadata:{title:'Market opportunity',type:'markdown',artifactVersion:String(documentVersion),renderJobId:documentPending&&!documentReady?'document-job-1':'',renderStatus:documentReady?'complete':documentPending?'running':'',renderPdfArtifactVersion:documentReady?String(documentVersion):'',renderPdfSourceSceneRef:'',renderPdfAssetRef:documentReady?documentPdfRef:'',assets:documentReady?JSON.stringify([asset]):''}};
}
const deck={schemaVersion:1,width:1920,height:1080,theme:{background:'#111111'},slides:[{id:'slide-1',background:'#111111',notes:'',elements:[{id:'title',type:'text',x:180,y:180,width:1200,height:180,z:1,opacity:1,rotation:0,text:'Creator engine',fontSize:72,fontFamily:'Arial',fontWeight:700,color:'#ffffff',textAlign:'left',lineHeight:1.08,letterSpacing:'normal'}]}]};
const sendJSON=(res,status,value)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify(value));};
const server=http.createServer(async(req,res)=>{
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
 if(req.url==='/auth/me')return sendJSON(res,200,{email:'synthetic@example.test',name:'AJ',shellAccess:'full'});
 if(req.method==='GET'&&req.url==='/artifacts/deck?id='+deckId)return sendJSON(res,200,{ok:true,artifact:{id:deckId,title:'Creator engine',version:deckVersion,sceneRef,metadata:artifact(deckId).metadata},deck,canWrite:true});
 if(req.method==='GET'&&req.url==='/artifacts/document?id='+documentId)return sendJSON(res,200,{ok:true,artifact:{id:documentId,title:'Market opportunity',version:documentVersion,metadata:artifact(documentId).metadata},document:{schemaVersion:1,markdown:'# Market opportunity\n\nA first-class report.'},canWrite:true});
 if(req.method==='GET'&&req.url.startsWith('/artifacts?id=')){const id=decodeURIComponent(req.url.split('=')[1]);return sendJSON(res,200,{artifacts:[artifact(id)]});}
 if(req.method==='GET'&&req.url==='/artifacts/render-token?id='+deckId)return sendJSON(res,200,{ok:true,url:'/mock-deck-render'});
 if(req.url==='/mock-deck-render'){res.writeHead(200,{'content-type':'text/html'});return res.end('<!doctype html><title>safe preview</title>');}
 if(req.method==='POST'&&req.url==='/artifacts/export-pdf'){
  let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);exportRequests.push(body);
  if(body.artifactId===deckId){deckPending=true;return sendJSON(res,202,{ok:true,jobId:'deck-job-1',kind:'deck',sourceVersion:deckVersion,sceneRef,reused:exportRequests.filter(item=>item.artifactId===deckId).length>1,renderStatus:'running'});}
  documentPending=true;return sendJSON(res,202,{ok:true,jobId:'document-job-1',kind:'paper',sourceVersion:documentVersion,sceneRef:'',reused:exportRequests.filter(item=>item.artifactId===documentId).length>1,renderStatus:'running'});
 }
 if(req.method==='POST'&&req.url==='/test/deck-ready'){deckReady=true;deckPending=false;deckVersion++;res.writeHead(204);return res.end();}
 if(req.method==='POST'&&req.url==='/test/document-ready'){documentReady=true;documentPending=false;documentVersion++;res.writeHead(204);return res.end();}
 if(req.url.startsWith('/artifacts/blob?')){res.writeHead(200,{'content-type':'application/pdf','content-disposition':'attachment'});return res.end(Buffer.from('%PDF-1.7 ready'));}
 if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts'))return sendJSON(res,503,{});
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 harnessStage='listen';
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 harnessStage='browser';
 const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:900},acceptDownloads:true});debugPage=page;page.on('pageerror',error=>console.error('pageerror',error));
 const base='http://127.0.0.1:'+server.address().port;await page.goto(base+'/',{waitUntil:'domcontentloaded'});await page.waitForSelector('#appShell.is-authed');
 // Collapse the two-minute production polling window only in this synthetic
 // browser. The control must remain truthful and clickable after null.
 await page.evaluate(()=>{waitForArtifactPdfAsset=async()=>null;window.__pdfTestToasts=[];const originalToast=showToast;showToast=(payload)=>{window.__pdfTestToasts.push(payload);originalToast(payload)};});

 harnessStage='open deck';await page.evaluate(id=>openDeckStudio(id,'Creator engine',{}),deckId);await page.waitForSelector('.deck-editor');
 const deckDownload=page.getByRole('button',{name:'Download',exact:true});
 harnessStage='deck first menu';await deckDownload.click();
 harnessStage='deck first click';await page.getByRole('menuitem',{name:/PDF/}).click();
 harnessStage='deck first wait';
 await page.waitForFunction(()=>document.querySelector('[data-action="export-pdf"]')?.dataset.pdfPending==='true');
 const deckPDF=page.locator('.deck-editor__download [data-action="export-pdf"]');
 assert.match(await deckPDF.innerText(),/Rendering · check/);
 assert.equal(await deckPDF.isEnabled(),true);
 harnessStage='deck retry';await deckDownload.click();await page.getByRole('menuitem',{name:/Rendering · check/}).click();
 await page.waitForFunction(()=>document.querySelector('[data-action="export-pdf"]')?.dataset.pdfPending==='true');
 await page.waitForTimeout(100);
 assert.equal(exportRequests.filter(item=>item.artifactId===deckId).length,2);
 assert.deepEqual(exportRequests.filter(item=>item.artifactId===deckId).map(item=>({version:item.expectedVersion,scene:item.sceneRef})),[{version:4,scene:sceneRef},{version:4,scene:sceneRef}]);
 harnessStage='deck ready';await page.evaluate(base=>fetch(base+'/test/deck-ready',{method:'POST'}),base);
 const deckDownloadEvent=page.waitForEvent('download');await deckDownload.click();await page.getByRole('menuitem',{name:/Rendering · check/}).click();await deckDownloadEvent;
 assert.equal(exportRequests.filter(item=>item.artifactId===deckId).length,2,'late PDF asset should download without a stale third POST');
 harnessStage='close deck';await page.getByRole('button',{name:'Close Deck Studio'}).click();await page.waitForSelector('.deck-editor',{state:'detached'});

 harnessStage='open document';await page.evaluate(id=>openDocumentStudio(id,'Market opportunity',{}),documentId);await page.waitForSelector('.document-editor');
 harnessStage='document first';const documentPDF=page.locator('[data-doc-action="pdf"]');await documentPDF.click();
 await page.waitForFunction(()=>document.querySelector('[data-doc-action="pdf"]')?.dataset.pdfPending==='true');
 assert.equal(await documentPDF.textContent(),'Rendering · check');assert.equal(await documentPDF.isEnabled(),true);
 harnessStage='document retry';await documentPDF.click();await page.waitForFunction(()=>document.querySelector('[data-doc-action="pdf"]')?.dataset.pdfPending==='true');
 await page.waitForTimeout(100);
 assert.equal(exportRequests.filter(item=>item.artifactId===documentId).length,2);
 assert.deepEqual(exportRequests.filter(item=>item.artifactId===documentId).map(item=>item.expectedVersion),[7,7]);
 harnessStage='document ready';await page.evaluate(base=>fetch(base+'/test/document-ready',{method:'POST'}),base);
 const documentDownloadEvent=page.waitForEvent('download');await documentPDF.click();await documentDownloadEvent;
 assert.equal(exportRequests.filter(item=>item.artifactId===documentId).length,2,'late document PDF should download without a stale third POST');

 harnessStage='done';clearTimeout(harnessTimeout);await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PDF_IDEMPOTENCY_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Studio PDF idempotency harness: %v\n%s", err, output)
	}
}
