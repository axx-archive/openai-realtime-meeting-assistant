package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestDeckActivityUsesExactCompletedResultBinding(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"function manifestDeckDeliveryBinding(manifest, deliverable)",
		"function manifestDeckResultReceipt(manifest, binding, requireComplete = false)",
		"function manifestDeckCompletedWorkMessage(manifest, deckArtifact, binding = null)",
		"String(ref.resultArtifactId || '').trim() !== binding.artifactId",
		"['complete', 'completed', 'published'].includes(String(ref.status || '').trim().toLowerCase())",
		"manifestDeckArtifactMatchesBinding(binding, freshDeck, { requireGoal: true })",
		"binding.artifactId === resultId && binding.version > 0 && Boolean(binding.digest)",
		"manifestDeckAdmissionMatchesBinding(binding, freshAdmission)",
		"function manifestDeckAcceptedResultBinding(artifact)",
		"report.acceptedResultArtifactVersion",
		"deckArtifact?.metadata?.contentDigest",
		"function fetchManifestDeckAdmission(artifactId)",
		"admission.qualityState !== 'admitted'",
		"String(message.thread.resultApprovalState || '').trim() !== 'approved_exact'",
		"String(message.thread.resultQualityState || '').trim() !== 'admitted'",
		"fetchArtifactEntryById(goalId, { refresh: true })",
		"fetchArtifactEntryById(resultId, { refresh: true })",
		"generation !== manifestDeckActivityGeneration",
		"sourceThreadId !== String(activeScoutThreadId || '')",
		"function openManifestDeckReceiptContext(manifest, binding, deckArtifact, trigger, options = {})",
		"mode: 'manifest_receipt'",
		"Historical delivery · unavailable",
		"edit, present, and download controls are hidden",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("manifest deck activity identity contract missing %q", want)
		}
	}
	if strings.Contains(html, "manifestDeckArtifactActivityMessage") || strings.Contains(html, "synthetic: true") {
		t.Fatal("manifest activity still fabricates a completed artifact work message")
	}
	start := strings.Index(html, "function openManifestDeckReceiptContext(manifest, binding, deckArtifact, trigger, options = {})")
	end := strings.Index(html[start:], "async function openManifestDeckActivity")
	if start < 0 || end < 0 {
		t.Fatal("historical receipt helper boundaries missing")
	}
	receipt := html[start : start+end]
	for _, forbidden := range []string{"openArtifactStage", "openDeckStudio", "openDeckPresentation", "desktopSaveToDriveControl"} {
		if strings.Contains(receipt, forbidden) {
			t.Errorf("historical receipt exposed action %s", forbidden)
		}
	}
}

func TestManifestDeckActivityIdentityRendered(t *testing.T) {
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
const html=fs.readFileSync(process.env.MANIFEST_ACTIVITY_INDEX,'utf8');
const completeTasks=['context_snapshot','external_research','evidence','story_architects','write','gate','voice','identity','imagery_direction','imagery_generate','layout_plan','ship_deck','draft_compile','slide_jury','quality_gate','ship_compile'].map(id=>({id,title:id,status:'complete'}));
const blockedPlan={processId:'packaging_studio',state:'needs_attention',objective:'Old blocked request',subtasks:[{id:'context_snapshot',title:'Understand the brief',status:'complete'},{id:'external_research',title:'Verify the facts',status:'blocked'}]};
const deckDocument={schemaVersion:1,width:1920,height:1080,slides:[{id:'cover',background:'#17211c',elements:[{id:'title',type:'text',x:150,y:160,width:1200,height:180,z:1,opacity:1,rotation:0,text:'Exact historical deck',fontSize:82,fontFamily:'Arial',fontWeight:700,color:'#fff',textAlign:'left',lineHeight:1.05,letterSpacing:'normal'}]}]};
const deck=(id,title,goalId,version,digest)=>({id,kind:'os_artifact',text:'<!doctype html><html><body><section class="pg">'+title+'</section></body></html>',createdAt:'2026-08-22T13:57:00Z',metadata:{title,type:'html_deck',source:'packaging_studio_ship',goalId,status:'complete',threadStatus:'complete',artifactVersion:String(version),contentDigest:digest}});
const currentDeck=deck('deck-current','Like A Farmer — Optimization Insights','goal-current',3,'a'.repeat(64));
const fallbackDeck=deck('deck-fallback','Fallback presentation','goal-blocked',1,'b'.repeat(64));
const rerunOldDeck=deck('deck-rerun-old','Superseded presentation','goal-rerun',4,'c'.repeat(64));
const rerunNewDeck=deck('deck-rerun-new','Current rerun presentation','goal-rerun',5,'d'.repeat(64));
const sameIdOldDeck=deck('deck-rerun-stable','Earlier stable-id presentation','goal-rerun-stable',7,'7'.repeat(64));
const sameIdNewDeck=deck('deck-rerun-stable','Current stable-id presentation','goal-rerun-stable',8,'8'.repeat(64));
const switchDeck=deck('deck-switch','Channel switch presentation','goal-switch',2,'e'.repeat(64));
const revokedDeck=deck('deck-revoked','Revoked historical presentation','goal-revoked',2,'f'.repeat(64));
const completePlan=accepted=>({processId:'packaging_studio',state:'verified',objective:'Build the exact current deck',subtasks:completeTasks,report:{acceptedResultArtifactId:accepted.id,acceptedResultArtifactVersion:Number(accepted.metadata.artifactVersion),acceptedResultArtifactDigest:accepted.metadata.contentDigest}});
const goal=(id,title,plan,status,accepted)=>{
  const metadata={title,mode:'goal',processId:'packaging_studio',status,threadStatus:status,progressPercent:status==='complete'?'100':'72',goalPlan:JSON.stringify(plan)};
  if(accepted){metadata.acceptedResultArtifactId=accepted.id;metadata.acceptedResultArtifactVersion=accepted.metadata.artifactVersion;metadata.acceptedResultArtifactDigest=accepted.metadata.contentDigest;}
  return {id,kind:'os_artifact',text:'# '+title,createdAt:'2026-08-22T13:57:00Z',metadata};
};
const goals={
  'goal-current':goal('goal-current','Current deck activity',completePlan(currentDeck),'complete',currentDeck),
  'goal-blocked':goal('goal-blocked','Old blocked activity',blockedPlan,'needs_attention'),
  'goal-rerun':goal('goal-rerun','Current rerun activity',completePlan(rerunNewDeck),'complete',rerunNewDeck),
  'goal-switch':goal('goal-switch','Switch activity',completePlan(switchDeck),'complete',switchDeck)
};
const artifacts={...goals,[currentDeck.id]:currentDeck,[fallbackDeck.id]:fallbackDeck,[rerunOldDeck.id]:rerunOldDeck,[rerunNewDeck.id]:rerunNewDeck,[switchDeck.id]:switchDeck,[revokedDeck.id]:revokedDeck};
const server=http.createServer((req,res)=>{
	  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
	  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
	  if(req.url.startsWith('/artifacts/final-export-capability')){
	    const id=new URL(req.url,'http://127.0.0.1').searchParams.get('id');const artifact=artifacts[id];
	    if(!artifact||id===revokedDeck.id){res.writeHead(404,{'content-type':'application/json'});return res.end('{}');}
	    const current=id===currentDeck.id||id===rerunNewDeck.id||id===switchDeck.id;
	    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifactId:id,artifactVersion:Number(artifact.metadata.artifactVersion),qualityState:'admitted',managed:true,canPresent:current,canExport:current}));
	  }
	  if(req.url.startsWith('/artifacts/deck?id=')){
	    const id=new URL(req.url,'http://127.0.0.1').searchParams.get('id');const artifact=artifacts[id];
	    if(!artifact||id===revokedDeck.id){res.writeHead(404,{'content-type':'application/json'});return res.end('{}');}
	    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:{id,title:artifact.metadata.title,version:Number(artifact.metadata.artifactVersion),contentDigest:artifact.metadata.contentDigest,goalId:artifact.metadata.goalId},deck:deckDocument,canWrite:true,qualityState:'admitted',canPresent:true,canExport:true}));
	  }
  const match=/^\/artifacts\?id=([^&]+)/.exec(req.url||'');
	  if(match&&artifacts[decodeURIComponent(match[1])]&&decodeURIComponent(match[1])!==revokedDeck.id){
    const id=decodeURIComponent(match[1]);
    const respond=()=>{res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({artifacts:[artifacts[id]]}));};
    return id==='goal-switch'?setTimeout(respond,350):respond();
  }
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
const threadMessage=(id,runId,goalId,resultId,status,query,title,capabilities={})=>({id,kind:'thread',role:'scout',createdAt:'2026-08-22T13:57:00Z',thread:{id:runId,mode:'goal',artifactId:goalId,resultArtifactId:resultId,resultArtifactType:'html_deck',resultTitle:title||resultId,resultApprovalState:'approved_exact',resultQualityState:'admitted',status,query,...capabilities}});
const oldSameGoal=threadMessage('old-same-goal','run-old','goal-current','deck-current','needs_attention','Old 02:32 blocked prompt');
const completed=threadMessage('completed-exact','run-complete','goal-current','deck-current','complete','Current completed request');
const unrelated=threadMessage('unrelated-latest','run-unrelated','goal-unrelated','deck-unrelated','needs_attention','Unrelated latest blocked root');
const blockedOnly=threadMessage('blocked-only','run-blocked','goal-blocked','deck-fallback','needs_attention','Ground recommendation Up next');
const rerunOldReceipt=threadMessage('rerun-old-receipt','run-rerun-old','goal-rerun','deck-rerun-old','complete','Old completed rerun','Superseded presentation',{resultCanEdit:false,resultCanPresent:false,resultCanExport:false});
const rerunNewReceipt=threadMessage('rerun-new-receipt','run-rerun-new','goal-rerun','deck-rerun-new','complete','Current completed rerun','Current rerun presentation',{resultCanEdit:true,resultCanPresent:true,resultCanExport:true});
const sameIdOldReceipt=threadMessage('same-id-old-receipt','run-same-id-old','goal-rerun-stable','deck-rerun-stable','complete','Earlier stable-id rerun','Earlier stable-id presentation',{resultArtifactVersion:7,resultArtifactDigest:'7'.repeat(64),resultCanEdit:false,resultCanPresent:false,resultCanExport:false});
const sameIdNewReceipt=threadMessage('same-id-new-receipt','run-same-id-new','goal-rerun-stable','deck-rerun-stable','complete','Current stable-id rerun','Current stable-id presentation',{resultArtifactVersion:8,resultArtifactDigest:'8'.repeat(64),resultCanEdit:true,resultCanPresent:true,resultCanExport:true});
const switchReceipt=threadMessage('switch-exact','run-switch','goal-switch','deck-switch','complete','Switch request','Channel switch presentation');
const revokedReceipt=threadMessage('revoked-receipt','run-revoked','goal-revoked','deck-revoked','complete','Revoked completed request','Revoked historical presentation',{resultCanEdit:true,resultCanPresent:true,resultCanExport:true});
const manifest=(id,goalId,artifact,title)=>({id,kind:'manifest',role:'scout',createdAt:'2026-08-22T13:57:01Z',manifest:{goalId,status:'shipped',title,deliverables:[{badge:'deck',title,artifactId:artifact.id,artifactVersion:Number(artifact.metadata.artifactVersion),contentDigest:artifact.metadata.contentDigest,present:true},{badge:'paper',title:'Copy record',artifactId:id+'-copy'},{badge:'doc',title:'Findings',artifactId:id+'-findings'}],skips:['note one','note two']}});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:820}});
  await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
	  await page.evaluate(({currentDeck,fallbackDeck,rerunOldDeck,rerunNewDeck,switchDeck,revokedDeck,oldSameGoal,completed,unrelated,blockedOnly,rerunOldReceipt,rerunNewReceipt,switchReceipt,revokedReceipt})=>{
    document.getElementById('appShell').dataset.tool='chat';
    document.getElementById('chatTool').style.display='flex';
	    artifactEntries=[currentDeck,fallbackDeck,rerunOldDeck,rerunNewDeck,switchDeck,revokedDeck];
	    scoutChatThreads=[
	      {id:'activity-channel',title:'Like A Farmer',visibility:'public',messagesLoaded:true,messages:[oldSameGoal,completed,unrelated,blockedOnly,rerunOldReceipt,rerunNewReceipt,switchReceipt,revokedReceipt]},
      {id:'other-channel',title:'Other channel',visibility:'public',messagesLoaded:true,messages:[]}
    ];
    activeScoutThreadId='activity-channel';
	  },{currentDeck,fallbackDeck,rerunOldDeck,rerunNewDeck,switchDeck,revokedDeck,oldSameGoal,completed,unrelated,blockedOnly,rerunOldReceipt,rerunNewReceipt,switchReceipt,revokedReceipt});

  await page.evaluate(({currentManifest})=>document.body.appendChild(scoutManifestCardNode(currentManifest)),{currentManifest:manifest('manifest-current','goal-current',currentDeck,currentDeck.metadata.title)});
  await page.locator('.manifest-card').first().getByRole('button',{name:'View activity',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('#chatContextRail')?.hidden===false&&document.querySelector('#chatContextMeta')?.textContent.includes('100%'));
  const exact=await page.evaluate(()=>({messageId:chatContextState?.messageId,title:chatContextTitle.textContent,meta:chatContextMeta.textContent,body:chatContextBody.textContent}));
  assert.equal(exact.messageId,'completed-exact',JSON.stringify(exact));
  assert.match(exact.meta,/Phase 5 of 5.*100%/);
  assert.doesNotMatch(exact.body,/Old 02:32|Ground recommendation Up next|Unrelated latest blocked root/);
  await page.evaluate(()=>document.querySelector('#chatContextClose').click());

	  await page.evaluate(({fallbackManifest})=>document.body.appendChild(scoutManifestCardNode(fallbackManifest)),{fallbackManifest:manifest('manifest-fallback','goal-blocked',fallbackDeck,fallbackDeck.metadata.title)});
	  await page.locator('.manifest-card').last().getByRole('button',{name:'View activity',exact:true}).click();
	  await page.waitForFunction(()=>document.querySelector('#chatContextRail')?.hidden===false&&chatContextState?.mode==='manifest_receipt');
	  const fallback=await page.evaluate(()=>({messageId:chatContextState?.messageId,title:chatContextTitle.textContent,meta:chatContextMeta.textContent,body:chatContextBody.textContent,actions:chatContextBody.querySelectorAll('button,a').length}));
	  assert.equal(fallback.messageId,'manifest-receipt-goal-blocked-deck-fallback',JSON.stringify(fallback));
	  assert.equal(fallback.title,'Fallback presentation',JSON.stringify(fallback));
	  assert.match(fallback.meta,/Historical delivery/);
	  assert.doesNotMatch(fallback.meta,/Delivered/);
	  assert.equal(fallback.actions,0,JSON.stringify(fallback));
	  assert.doesNotMatch(fallback.meta+fallback.body,/72%|Ground recommendation Up next|Old blocked request/);

  // A completed rerun moved the goal's accepted tuple to deck B. The old
  // delivered deck A remains openable, but it may not borrow rerun B's parent
  // activity merely because that parent is complete again.
	  await page.evaluate(()=>document.querySelector('#chatContextClose').click());
	  await page.evaluate(({rerunManifest})=>document.body.appendChild(scoutManifestCardNode(rerunManifest)),{rerunManifest:manifest('manifest-rerun-old','goal-rerun',rerunOldDeck,rerunOldDeck.metadata.title)});
	  await page.locator('.manifest-card').last().getByRole('button',{name:'View activity',exact:true}).click();
	  await page.waitForFunction(()=>document.querySelector('#chatContextRail')?.hidden===false&&chatContextState?.mode==='manifest_receipt');
	  const rerun=await page.evaluate(()=>({messageId:chatContextState?.messageId,title:chatContextTitle.textContent,meta:chatContextMeta.textContent,body:chatContextBody.textContent,actions:chatContextBody.querySelectorAll('button,a').length}));
	  assert.equal(rerun.messageId,'manifest-receipt-goal-rerun-deck-rerun-old',JSON.stringify(rerun));
	  assert.equal(rerun.title,'Superseded presentation',JSON.stringify(rerun));
	  assert.match(rerun.meta,/Historical delivery/);
	  assert.equal(rerun.actions,0,JSON.stringify(rerun));
	  assert.doesNotMatch(rerun.body,/Current rerun activity|Old completed rerun/);

	  // Two completed receipts and two shipped manifests for one rerun must keep
	  // each historical card bound to its own deck. The later B receipt may not
	  // replace A's hero, title, or projected controls.
	  await page.evaluate(({oldManifest,newManifest})=>{
	    const host=document.createElement('div');host.id='rerun-manifest-pair';document.body.appendChild(host);
	    host.append(scoutManifestCardNode(oldManifest),scoutManifestCardNode(newManifest));
	  },{oldManifest:manifest('manifest-rerun-a','goal-rerun',rerunOldDeck,rerunOldDeck.metadata.title),newManifest:manifest('manifest-rerun-b','goal-rerun',rerunNewDeck,rerunNewDeck.metadata.title)});
	  await page.waitForFunction(()=>document.querySelectorAll('#rerun-manifest-pair .chat-deck__native-preview.is-ready').length===2);
	  const pair=await page.evaluate(()=>Array.from(document.querySelectorAll('#rerun-manifest-pair .manifest-card')).map(card=>({
	    artifactId:card.querySelector('.scout-chat-deck-result')?.dataset.resultArtifactId||'',
	    title:card.querySelector('.chat-deck__title')?.textContent||'',
	    edit:card.querySelectorAll('.chat-deck__actions > button.chat-deck__btn--secondary:not([hidden]):not(:disabled)').length,
	    present:card.querySelectorAll('.chat-deck__btn--primary:not([hidden]):not(:disabled)').length,
	    download:card.querySelectorAll('.chat-deck__download:not([hidden])').length
	  })));
	  assert.deepEqual(pair,[
	    {artifactId:'deck-rerun-old',title:'Superseded presentation',edit:0,present:0,download:0},
	    {artifactId:'deck-rerun-new',title:'Current rerun presentation',edit:1,present:1,download:1}
	  ],JSON.stringify(pair));
	  const exactSuppression=await page.evaluate(({oldReceipt,newReceipt,oldManifest,newDeck})=>{
	    const thread=selectedScoutChatThread();const prior=thread.messages;
	    thread.messages=[oldReceipt,newReceipt,oldManifest];
	    runlogOpen=null;
	    const host=document.createElement('div');host.appendChild(scoutHTMLDeckRefRecordNode(newReceipt,newDeck));
	    const artifactId=host.querySelector('.scout-chat-deck-result')?.dataset.resultArtifactId||'';
	    thread.messages=prior;
	    return artifactId;
	  },{oldReceipt:rerunOldReceipt,newReceipt:rerunNewReceipt,oldManifest:manifest('manifest-only-a','goal-rerun',rerunOldDeck,rerunOldDeck.metadata.title),newDeck:rerunNewDeck});
	  assert.equal(exactSuppression,'deck-rerun-new','manifest A suppressed unrelated rerun result B');

	  // Artifact IDs remain stable across an edited/reviewed rerun. Manifest A
	  // may suppress only its immutable version+digest tuple, never revision B.
	  const stableIdSuppression=await page.evaluate(({oldReceipt,newReceipt,oldManifest,newDeck})=>{
	    const thread=selectedScoutChatThread();const prior=thread.messages;
	    thread.messages=[oldReceipt,newReceipt,oldManifest];
	    runlogOpen=null;
	    const host=document.createElement('div');host.appendChild(scoutHTMLDeckRefRecordNode(newReceipt,newDeck));
	    const result={artifactId:host.querySelector('.scout-chat-deck-result')?.dataset.resultArtifactId||'',title:host.querySelector('.chat-deck__title')?.textContent||''};
	    thread.messages=prior;
	    return result;
	  },{oldReceipt:sameIdOldReceipt,newReceipt:sameIdNewReceipt,oldManifest:manifest('manifest-stable-a','goal-rerun-stable',sameIdOldDeck,sameIdOldDeck.metadata.title),newDeck:sameIdNewDeck});
	  assert.deepEqual(stableIdSuppression,{artifactId:'deck-rerun-stable',title:'Current stable-id presentation'},JSON.stringify(stableIdSuppression));

	  // A cached historical hero is not authority. When both exact reads deny or
	  // lose the artifact, the card becomes an explicit inert receipt.
	  await page.evaluate(({revokedManifest})=>document.body.appendChild(scoutManifestCardNode(revokedManifest)),{revokedManifest:manifest('manifest-revoked','goal-revoked',revokedDeck,revokedDeck.metadata.title)});
	  await page.locator('.manifest-card').last().locator('[data-availability="unavailable"]').waitFor({state:'attached'});
	  const revoked=await page.locator('.manifest-card').last().evaluate(card=>({text:card.textContent,actions:card.querySelectorAll('button,a').length,artifactId:card.querySelector('[data-result-artifact-id]')?.dataset.resultArtifactId||''}));
	  assert.match(revoked.text,/Historical presentation unavailable/);
	  assert.equal(revoked.actions,0,JSON.stringify(revoked));
	  assert.equal(revoked.artifactId,'deck-revoked',JSON.stringify(revoked));

  // The goal refresh is deliberately delayed. Switching channels while it is
  // in flight must invalidate the opener even though this test keeps the old
  // button connected outside the feed, proving the generation fence itself.
  await page.evaluate(()=>document.querySelector('#chatContextClose').click());
  await page.evaluate(({switchManifest})=>document.body.appendChild(scoutManifestCardNode(switchManifest)),{switchManifest:manifest('manifest-switch','goal-switch',switchDeck,switchDeck.metadata.title)});
  await page.locator('.manifest-card').last().getByRole('button',{name:'View activity',exact:true}).click();
  await page.evaluate(()=>selectScoutChatThread('other-channel'));
  await page.waitForTimeout(550);
  const switched=await page.evaluate(()=>({active:activeScoutThreadId,state:chatContextState,railHidden:document.querySelector('#chatContextRail')?.hidden}));
  assert.deepEqual(switched,{active:'other-channel',state:null,railHidden:true},JSON.stringify(switched));

  await browser.close();
  server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1);});
`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "MANIFEST_ACTIVITY_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered manifest activity identity contract failed: %v\n%s", err, output)
	}
}
