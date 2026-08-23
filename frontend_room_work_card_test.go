package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexRoomWorkLifecycleStaysInActivityAndTypedResultsHydrateExactly(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`id="roomWorkActivity"`,
		`id="roomWorkActivitySheet"`,
		"const roomWorkActivityByRun = new Map()",
		"recordRoomWorkActivity(message)",
		"hydrateRoomTypedResult(message)",
		"if (workRunId) return document.createDocumentFragment()",
		"setRoomWorkActivitySheet(true)",
		"status === 'complete'",
		"status === 'needs_attention'",
		"status === 'approval_required'",
		"status === 'needs_input'",
		"fetchArtifactEntryById(resultId, { refresh: true })",
		"resultArtifactVersion",
		"scoutStructuredResultRecordNode(richMessage, artifact)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing room Activity/rich-result behavior %q", want)
		}
	}
	appendBody := functionBody(html, "function appendRoomChatMessage(message)")
	if !strings.Contains(appendBody, "if (workRunId)") || !strings.Contains(appendBody, "return false") {
		t.Fatal("room lifecycle frames must terminate in Activity before the feed append path")
	}
	if strings.Contains(appendBody, "priorWork.replaceWith") || strings.Contains(appendBody, "data-work-run-id") {
		t.Fatal("room lifecycle must not evolve an inline feed card")
	}
	hydrator := functionBody(html, "function hydrateRoomTypedResult(message)")
	if strings.Contains(hydrator, "message.text") || strings.Contains(hydrator, "innerHTML") {
		t.Fatal("room completion must not substitute raw completion prose/HTML for a typed result")
	}
	if !strings.Contains(hydrator, "metadata?.contentDigest") {
		t.Fatal("room typed hydration must bind the exact result digest")
	}
	if got := strings.Count(hydrator, "roomTypedResultHydrationAttempts.delete(attemptKey)"); got < 5 {
		t.Fatalf("room exact hydration must release every denied/drifted attempt for retry; found %d release paths", got)
	}
	activity := functionBody(html, "function renderRoomWorkActivity()")
	if strings.Contains(activity, "openArtifactStage(artifactId") || strings.Contains(activity, "desktopSaveToDriveControl(artifact") {
		t.Fatal("room Activity must never open or save the lifecycle artifact")
	}
	for _, want := range []string{"roomTypedResultProjection(message)", "expectedBinding", "Open deliverable"} {
		if !strings.Contains(activity, want) {
			t.Fatalf("room Activity exact-result action missing %q", want)
		}
	}
}

func TestIndexRoomExactHydrationRetriesAndActivityTargetsOnlyDistinctResultRendered(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	chrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(chrome); err != nil {
		t.Skip("system Chrome unavailable")
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
const html=fs.readFileSync(process.env.ROOM_EXACT_INDEX,'utf8');
const deniedDigest='a'.repeat(64);
const driftDigest='b'.repeat(64);
let deniedReads=0;
let driftReads=0;
const deck=(id,version,digest,title)=>({id,kind:'os_artifact',text:'<!doctype html><html><body><main><h1>'+title+'</h1></main></body></html>',metadata:{title,type:'html_deck',status:'complete',threadStatus:'complete',artifactVersion:String(version),contentDigest:digest}});
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts?id=room-denied'){
    deniedReads+=1;
    if(deniedReads===1){res.writeHead(403,{'content-type':'application/json'});return res.end('{}');}
    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({artifacts:[deck('room-denied',3,deniedDigest,'Denied then exact')]}));
  }
  if(req.url==='/artifacts?id=room-drift'){
    driftReads+=1;
    const artifact=driftReads===1?deck('room-drift',1,'c'.repeat(64),'Wrong revision'):deck('room-drift',2,driftDigest,'Drift then exact');
    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({artifacts:[artifact]}));
  }
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/artifacts')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/brain/')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true,executablePath:process.env.ROOM_EXACT_CHROME});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  await page.goto('http://127.0.0.1:'+server.address().port,{waitUntil:'domcontentloaded'});
  await page.waitForFunction(()=>typeof hydrateRoomTypedResult==='function'&&typeof syncDesktopActiveWorkIndicator==='function'&&Boolean(authedUser));
  const denied={id:'denied-receipt',workRunId:'denied-run',workRootRunId:'denied-run',workStatus:'complete',workFamily:'Presentation',workTitle:'Denied retry',artifactId:'denied-lifecycle',resultArtifactId:'room-denied',resultArtifactType:'html_deck',resultArtifactVersion:3,resultArtifactDigest:deniedDigest,resultTitle:'Denied then exact'};
  const drift={id:'drift-receipt',workRunId:'drift-run',workRootRunId:'drift-run',workStatus:'complete',workFamily:'Presentation',workTitle:'Drift retry',artifactId:'drift-lifecycle',resultArtifactId:'room-drift',resultArtifactType:'html_deck',resultArtifactVersion:2,resultArtifactDigest:driftDigest,resultTitle:'Drift then exact'};
  await page.evaluate(message=>hydrateRoomTypedResult(message),denied);
  await page.waitForFunction(key=>!roomTypedResultHydrationAttempts.has(key),'room-denied:3:'+deniedDigest);
  assert.equal(await page.locator('[data-result-artifact-id="room-denied"]').count(),0,'403 must fail closed');
  await page.evaluate(message=>hydrateRoomTypedResult(message),denied);
  await page.waitForSelector('[data-result-artifact-id="room-denied"]',{state:'attached'});
  await page.evaluate(message=>hydrateRoomTypedResult(message),drift);
  await page.waitForFunction(key=>!roomTypedResultHydrationAttempts.has(key),'room-drift:2:'+driftDigest);
  assert.equal(await page.locator('[data-result-artifact-id="room-drift"]').count(),0,'revision/digest drift must fail closed');
  await page.evaluate(message=>hydrateRoomTypedResult(message),drift);
  await page.waitForSelector('[data-result-artifact-id="room-drift"]',{state:'attached'});
  const actionLaw=await page.evaluate(({deniedDigest})=>{
    const lifecycle={id:'root-lifecycle',kind:'os_artifact',text:'RAW_LIFECYCLE_SENTINEL {"internal":true}',metadata:{title:'Internal run record',mode:'goal',type:'markdown',status:'complete',threadStatus:'complete',artifactVersion:'9',contentDigest:'9'.repeat(64)}};
    const result={id:'distinct-result',kind:'os_artifact',text:'<!doctype html><html><body><h1>Customer result</h1></body></html>',metadata:{title:'Customer result',type:'html_deck',status:'complete',threadStatus:'complete',artifactVersion:'7',contentDigest:deniedDigest}};
    artifactEntries=[lifecycle,result];
    const root={id:'root-message',kind:'thread',thread:{id:'root-run',rootRunId:'root-run',artifactId:lifecycle.id,mode:'work',status:'running',query:'Build the deck'}};
    const child={id:'child-result',kind:'thread',thread:{id:'child-run',rootRunId:'root-run',parentRunId:'root-run',artifactId:lifecycle.id,mode:'work',status:'complete',resultArtifactId:result.id,resultArtifactType:'html_deck',resultArtifactVersion:7,resultArtifactDigest:deniedDigest,resultTitle:'Customer result',resultCanEdit:true,resultCanPresent:true,resultCanExport:true}};
    scoutChatThreads=[{id:'activity-thread',messages:[root,child]}];activeScoutThreadId='activity-thread';
    const opens=[];const saves=[];
    const originalOpen=openArtifactStage;
    const originalSave=desktopSaveToDriveControl;
    openArtifactStage=(id,title,options)=>{opens.push({id,title,binding:options?.expectedBinding});return true;};
    desktopSaveToDriveControl=(entry,className,binding)=>{saves.push({id:entry?.id,binding});return bfEl('button',className,'Save to Drive');};
    syncDesktopActiveWorkIndicator();
    const owned=scoutOwnedActivityMessage([root,child]);
    const indicatorText=chatWorkIndicatorActions?.textContent||'';
    chatWorkIndicatorActions?.querySelector('button')?.click();
    roomWorkActivityByRun.clear();
    recordRoomWorkActivity({id:'room-complete',workRunId:'room-run',workRootRunId:'room-run',workStatus:'complete',workFamily:'Presentation',workTitle:'Customer result',artifactId:lifecycle.id,resultArtifactId:result.id,resultArtifactType:'html_deck',resultArtifactVersion:7,resultArtifactDigest:deniedDigest,resultTitle:'Customer result'});
    const roomText=roomWorkActivityBody.textContent||'';
    roomWorkActivityBody.querySelector('button')?.click();
    openArtifactStage=originalOpen;desktopSaveToDriveControl=originalSave;
    return {ownedId:owned?.id,ownedResultId:owned?.thread?.resultArtifactId,indicatorText,roomText,opens,saves};
  },{deniedDigest});
  assert.equal(actionLaw.ownedId,'root-message','the explicit root must continue to own Activity');
  assert.equal(actionLaw.ownedResultId,'distinct-result','the root Activity projection must carry the newest exact child result');
  assert.match(actionLaw.indicatorText,/View/);
  assert.doesNotMatch(actionLaw.indicatorText,/RAW_LIFECYCLE_SENTINEL/);
  assert.match(actionLaw.roomText,/Open deliverable/);
  assert.doesNotMatch(actionLaw.roomText,/RAW_LIFECYCLE_SENTINEL/);
  assert.deepEqual(actionLaw.opens.map(call=>call.id),['distinct-result','distinct-result']);
  assert.ok(actionLaw.opens.every(call=>call.binding?.artifactId==='distinct-result'&&call.binding?.version===7&&call.binding?.digest===deniedDigest));
  assert.deepEqual(actionLaw.saves.map(call=>call.id),['distinct-result','distinct-result']);
  assert.ok(actionLaw.saves.every(call=>call.binding?.artifactId==='distinct-result'&&call.binding?.version===7&&call.binding?.digest===deniedDigest));
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	nodeModules := "/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules"
	if _, err := os.Stat(filepath.Join(nodeModules, "playwright")); err != nil {
		nodeModules = filepath.Join(filepath.Dir(indexPath), "node_modules")
	}
	if _, err := os.Stat(filepath.Join(nodeModules, "playwright")); err != nil {
		t.Skip("Playwright unavailable")
	}
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodeModules, "ROOM_EXACT_INDEX="+indexPath, "ROOM_EXACT_CHROME="+chrome)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered room exact-result contract: %v\n%s", err, output)
	}
}
