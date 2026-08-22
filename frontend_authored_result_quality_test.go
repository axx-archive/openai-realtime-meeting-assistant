package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBlockedAuthoredDeckAndDocumentRenderAsEditableDrafts(t *testing.T) {
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
const html=fs.readFileSync(process.env.AUTHORED_RESULT_INDEX,'utf8');
const deckId='blocked-deck';
const documentId='blocked-document';
const deck={schemaVersion:1,width:1920,height:1080,slides:[{id:'cover',background:'#15191f',elements:[{id:'title',type:'text',x:160,y:160,width:1200,height:220,z:1,opacity:1,rotation:0,text:'Working deck',fontSize:84,fontFamily:'Arial',fontWeight:700,color:'#ffffff',textAlign:'left',lineHeight:1.05,letterSpacing:'normal'}]}]};
const actions=[];
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/deck?id='+deckId&&req.method==='GET'){
    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:{id:deckId,title:'Working deck',version:2},deck,canWrite:true,qualityState:'draft_needs_attention',canPresent:false,canExport:false}));
  }
  if(req.url==='/artifacts/document?id='+documentId&&req.method==='GET'){
    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:{id:documentId,title:'Working report',version:3},document:{schemaVersion:1,markdown:'# Working report\n\nA useful draft that still needs review.'},canWrite:true,qualityState:'draft_needs_attention',canExport:false}));
  }
  if(req.url==='/artifacts/action'&&req.method==='POST'){
    let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{actions.push(JSON.parse(body));res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true}));});return;
  }
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')||req.url.startsWith('/brain/')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:1000}});
  await page.goto('http://127.0.0.1:'+server.address().port,{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.evaluate(({deckId,documentId})=>{
    const deckArtifact={id:deckId,text:'<!doctype html><html><body>deck</body></html>',metadata:{title:'Working deck',type:'html_deck',status:'complete',artifactVersion:'2'}};
    const documentArtifact={id:documentId,text:'# Working report\n\nA useful draft that still needs review.',metadata:{title:'Working report',type:'markdown',status:'complete',artifactVersion:'3',assets:JSON.stringify([{ref:'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',kind:'pdf',mime:'application/pdf',name:'working-report.pdf'}])}};
    const deckMessage={id:'deck-result',kind:'thread',thread:{artifactId:'deck-goal',mode:'goal',goalStatus:'needs_attention',resultArtifactId:deckId,resultArtifactType:'html_deck',resultTitle:'Working deck',resultQualityState:'draft_needs_attention',resultCanEdit:true,resultCanContinue:true,resultCanPresent:false,resultCanExport:false}};
    const documentMessage={id:'document-result',kind:'thread',thread:{artifactId:'document-goal',mode:'goal',goalStatus:'completed',resultArtifactId:documentId,resultArtifactType:'markdown',resultTitle:'Working report',resultQualityState:'draft_needs_attention',resultCanEdit:true,resultCanContinue:true,resultCanPresent:false,resultCanExport:false}};
    artifactEntries=[deckArtifact,documentArtifact];
    scoutChatThreads=[{id:'quality-thread',messages:[deckMessage,documentMessage]}];
    activeScoutThreadId='quality-thread';
    document.body.append(scoutHTMLDeckRefRecordNode(deckMessage,deckArtifact),scoutMarkdownDocumentRefRecordNode(documentMessage,documentArtifact));
    const editedNote=authoredResultQualityNote({thread:{artifactId:'edited-goal',resultArtifactId:'edited-deck',resultQualityState:'edited_after_admission',resultCanContinue:true}},'presentation');
    editedNote.id='edited-result-quality';
    document.body.append(editedNote);
  },{deckId,documentId});

  const deckResult=page.locator('.scout-chat-deck-result');
  await deckResult.waitFor({state:'visible'});
  await deckResult.getByRole('button',{name:'Edit'}).waitFor({state:'visible'});
  assert.match(await deckResult.innerText(),/Draft · needs attention/);
  assert.match(await deckResult.innerText(),/has not passed the final quality review/);
  assert.equal(await deckResult.locator('.chat-deck__btn--primary:visible').count(),0);
  assert.equal(await deckResult.locator('.chat-deck__download:visible').count(),0);
  assert.equal(await deckResult.getByRole('button',{name:'Continue',exact:true}).count(),1);

  await deckResult.getByRole('button',{name:'Edit'}).click();
  const deckEditor=page.locator('.deck-editor');
  await deckEditor.waitFor({state:'visible'});
  assert.equal(await deckEditor.locator('[data-action="present"]:visible').count(),0);
  assert.equal(await deckEditor.locator('.deck-editor__download:visible').count(),0);
  assert.equal(await deckEditor.locator('[data-action="export-pptx"]:visible,[data-action="export-pdf"]:visible').count(),0);
  await deckEditor.getByRole('button',{name:'Close Deck Studio'}).click();
  await deckEditor.waitFor({state:'detached'});

  const documentResult=page.locator('.scout-chat-document-result');
  await documentResult.waitFor({state:'visible'});
  assert.match(await documentResult.locator('.scout-chat-document-result__kicker').innerText(),/document · draft/i);
  assert.doesNotMatch(await documentResult.innerText(),/document · ready/i);
  assert.match(await documentResult.innerText(),/Draft · needs attention/);
  assert.equal(await documentResult.getByRole('button',{name:'Continue',exact:true}).count(),1);
  assert.equal(await documentResult.locator('a[download$=".pdf"],object[type="application/pdf"]').count(),0);

  await documentResult.getByRole('button',{name:'Open Working report'}).click();
  const reader=page.locator('.artifact-stage');
  await reader.waitFor({state:'visible'});
  assert.match(await reader.locator('.artifact-stage__kicker').innerText(),/document · draft needs attention/i);
  assert.equal(await reader.getByRole('button',{name:'PDF',exact:true}).count(),0);
  assert.equal(await reader.locator('a[download$=".pdf"],object[type="application/pdf"]').count(),0);
  await reader.getByRole('button',{name:'Close'}).click();

  await documentResult.getByRole('button',{name:'Edit Working report'}).click();
  const documentEditor=page.locator('.document-editor');
  await documentEditor.waitFor({state:'visible'});
  assert.equal(await documentEditor.locator('[data-doc-action="pdf"]:visible').count(),0);
  await documentEditor.getByRole('button',{name:'Close Document Studio'}).click();

  const editedQuality=page.locator('#edited-result-quality');
  assert.match(await editedQuality.innerText(),/Edited draft · review required/);
  assert.equal(await editedQuality.getByRole('button',{name:'Review changes',exact:true}).count(),1);

  await page.setViewportSize({width:390,height:844});
  assert.equal(await deckResult.locator('.chat-deck__btn--primary:visible').count(),0);
  assert.equal(await deckResult.locator('.chat-deck__download:visible').count(),0);
  await deckResult.getByRole('button',{name:'Edit'}).click();
  await deckEditor.waitFor({state:'visible'});
  assert.equal(await deckEditor.locator('[data-action="present"]:visible').count(),0);
  assert.equal(await deckEditor.locator('.deck-editor__download:visible').count(),0);
  assert.equal(await deckEditor.locator('[data-action="export-pptx"]:visible,[data-action="export-pdf"]:visible').count(),0);
  await deckEditor.getByRole('button',{name:'Close Deck Studio'}).click();
  await deckEditor.waitFor({state:'detached'});
  assert.equal(await documentResult.locator('a[download$=".pdf"],object[type="application/pdf"]').count(),0);

  await deckResult.getByRole('button',{name:'Continue',exact:true}).click();
  await documentResult.getByRole('button',{name:'Continue',exact:true}).click();
  await editedQuality.getByRole('button',{name:'Review changes',exact:true}).evaluate(node=>node.click());
  await page.waitForFunction(()=>{const buttons=Array.from(document.querySelectorAll('.scout-authored-result__continue'));return buttons.length===3&&buttons.filter(node=>/Resuming review/.test(node.textContent)).length===2;});
  await page.waitForFunction(()=>/Reviewing changes/.test(document.querySelector('#edited-result-quality')?.textContent||''));
  assert.deepEqual(actions,[{id:'deck-goal',action:'resume'},{id:'document-goal',action:'resume'},{id:'edited-goal',action:'review_changes',resultArtifactId:'edited-deck'}]);

  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "AUTHORED_RESULT_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered authored-result quality harness: %v\n%s", err, output)
	}
}
