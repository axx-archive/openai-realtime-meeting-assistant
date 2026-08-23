package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebTimelineRoutesProcessToActivityAndKeepsRichResults(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	projection := functionBody(html, "function scoutChatRecordBelongsInTimeline(message)")
	if projection == "" {
		t.Fatal("timeline projection contract missing")
	}
	for _, want := range []string{
		"projection.richResult || projection.checkpoint",
		"kind === 'image'",
		"kind === 'manifest'",
		"status === '' || status === 'proposed'",
		"scoutThreadTimelineProjection(resultMessage).richResult",
		"scoutManifestHasRichDeck(message)",
		"return false",
	} {
		if !strings.Contains(projection, want) {
			t.Errorf("timeline projection missing %q", want)
		}
	}
	router := functionBody(html, "function scoutChatMessageRecordNode(message)")
	for _, want := range []string{
		"scoutChatRecordRendersInTimeline(message)",
		"scoutHTMLDeckRefRecordNode(message, resultArtifact)",
		"scoutMarkdownDocumentRefRecordNode(message, resultArtifact)",
		"projection.checkpoint",
	} {
		if !strings.Contains(router, want) {
			t.Errorf("timeline router missing %q", want)
		}
	}
	renderGate := functionBody(html, "function scoutChatRecordRendersInTimeline(message)")
	for _, want := range []string{"scoutChatRecordBelongsInTimeline(message)", "!scoutProcessThreadHasArtifactReceipt(message)"} {
		if !strings.Contains(renderGate, want) {
			t.Errorf("exact timeline render gate missing %q", want)
		}
	}
	if !strings.Contains(html, "boundedFeedMessages.slice(sourceIndex).filter(scoutChatRecordRendersInTimeline).length") {
		t.Error("unread seam must count only records the customer timeline can render")
	}
	progressiveUnread := functionBody(html, "function scoutChatProgressiveUnreadBoundary(job, sourceIndex)")
	if !strings.Contains(progressiveUnread, ".filter(scoutChatRecordRendersInTimeline).length") {
		t.Error("progressive unread seam must use the same exact visible-record contract")
	}
	if !strings.Contains(html, "String(message?.createdAt || '') > priorSeenAt && scoutChatRecordRendersInTimeline(message)") {
		t.Error("the first unread index must point to a renderable record, not hidden lifecycle activity")
	}
	if !strings.Contains(html, `id="chatWorkIndicatorText" class="chat-work-indicator__text" role="status" aria-live="polite" aria-atomic="true"`) {
		t.Error("work status pill must announce meaningful status changes")
	}
	for _, leaked := range []string{
		"scoutChatResearchNode(run)",
		"jump to the card",
		"projection.customerReply",
		"scoutChatWorkRecordNode(message)",
	} {
		if strings.Contains(router, leaked) {
			t.Errorf("generic process renderer leaked back into timeline: %q", leaked)
		}
	}
	indicator := functionBody(html, "function syncDesktopActiveWorkIndicator()")
	for _, want := range []string{
		"'needs_attention'",
		"'failed'",
		"'blocked'",
		"scoutOwnedActivityMessage(messages)",
		"openDesktopWorkContext",
	} {
		if want == "openDesktopWorkContext" {
			if !strings.Contains(html, "chatWorkIndicatorOpen?.addEventListener") || !strings.Contains(html, want) {
				t.Errorf("bottom activity indicator no longer opens the work context")
			}
			continue
		}
		if !strings.Contains(indicator, want) {
			t.Errorf("activity indicator missing %q", want)
		}
	}
	owned := functionBody(html, "function scoutOwnedActivityMessage(messages)")
	for _, want := range []string{"parentRunId", "rootRunId", "Preserve the explicit customer root"} {
		if !strings.Contains(owned, want) {
			t.Errorf("activity ownership missing explicit topology %q", want)
		}
	}
	if strings.Contains(owned, "delegatedBy") || strings.Contains(owned, "agentName") {
		t.Error("activity ownership must not guess delegation from presentation identity")
	}
	for _, want := range []string{"scoutThreadTimelineProjection(message)", "terminalResultArtifact", "expectedBinding", "desktopSaveToDriveControl(terminalResultArtifact"} {
		if !strings.Contains(indicator, want) {
			t.Errorf("desktop Activity exact-result action missing %q", want)
		}
	}
	if strings.Contains(indicator, "openArtifactStage(artifact.id") || strings.Contains(indicator, "desktopSaveToDriveControl(artifact") {
		t.Error("desktop Activity must never open or save the lifecycle artifact")
	}
	if !strings.Contains(owned, "...latestResult") || !strings.Contains(owned, "scoutResultReceiptExact(latest.thread)") {
		t.Error("explicit root Activity must carry the newest delegated exact-result tuple")
	}
	for _, want := range []string{
		"hydrateScoutResultArtifact(message)",
		"fetchArtifactEntryById(artifactId",
		"metadata?.contentDigest",
		"scoutStructuredResultRecordNode",
		"scoutStructuredResultEnvelopeValid",
		"resultArtifactVersion",
		"resultArtifactDigest",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("typed/historical result contract missing %q", want)
		}
	}
}

func TestWebExactResultHydrationRetriesAndSaveRefusesRevisionDrift(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	hydrate := functionBody(html, "function hydrateScoutResultArtifact(message)")
	for _, want := range []string{
		"scoutResultArtifactHydrationAttempts.delete(attemptKey)",
		"Number(entry?.metadata?.artifactVersion || 0) === version",
		"contentDigest",
	} {
		if !strings.Contains(hydrate, want) {
			t.Fatalf("exact result hydration retry contract missing %q", want)
		}
	}
	save := functionBodyAfterSignature(html, "function artifactSaveToFilesControl(entry, options = {})")
	for _, want := range []string{
		"options?.expectedBinding",
		"refreshedArtifact",
		"metadata?.artifactVersion",
		"artifactEntryCapabilityDigest(refreshedArtifact)",
		"validArtifactDispositionRef(dispositionRef)",
		"dispositionRef.contentRevision",
		"sameArtifactDispositionRef(receipt?.artifact, dispositionRef)",
		"This deliverable changed before it could be saved",
	} {
		if !strings.Contains(save, want) {
			t.Fatalf("exact Save to Drive drift gate missing %q", want)
		}
	}
	stage := functionBody(html, "async function openArtifactStage(artifactId, fallbackTitle, options)")
	if !strings.Contains(stage, "artifactSaveToFilesControl(entry, { expectedBinding })") {
		t.Fatal("exact stage did not pass its displayed result tuple into Save to Drive")
	}
}

func TestWebTimelineProjectionRenderedContract(t *testing.T) {
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
const html=fs.readFileSync(process.env.RICH_TIMELINE_INDEX,'utf8');
const historicalDigest='a'.repeat(64);
const raceDigest='b'.repeat(64);
const historicalDeck={id:'historical-deck',kind:'os_artifact',text:'<!doctype html><html><body><main><h1>Historical exact deck</h1></main></body></html>',metadata:{title:'Historical exact deck',type:'html_deck',status:'complete',threadStatus:'complete',artifactVersion:'4',contentDigest:historicalDigest}};
const raceDeck={id:'race-deck',kind:'os_artifact',text:'<!doctype html><html><body><main><h1>Stale race deck</h1></main></body></html>',metadata:{title:'Stale race deck',type:'html_deck',status:'complete',threadStatus:'complete',artifactVersion:'2',contentDigest:raceDigest}};
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts?id=historical-deck'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({artifacts:[historicalDeck]}));}
  if(req.url==='/artifacts?id=race-deck'){return setTimeout(()=>{res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({artifacts:[raceDeck]}));},120);}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/artifacts')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/brain/')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true,executablePath:process.env.RICH_TIMELINE_CHROME});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  await page.goto('http://127.0.0.1:'+server.address().port,{waitUntil:'domcontentloaded'});
  await page.waitForFunction(()=>typeof scoutChatRecordBelongsInTimeline==='function');
  await page.waitForFunction(()=>Boolean(authedUser));
  const result=await page.evaluate(()=>{
    const deck={id:'deck-result',text:'<!doctype html><html><body><h1>Real deck</h1></body></html>',metadata:{title:'Real deck',type:'html_deck',status:'complete',artifactVersion:'1',contentDigest:'d'.repeat(64)}};
    const doc={id:'doc-result',text:'# Real report\n\nThis is the completed report.',metadata:{title:'Real report',type:'markdown',status:'complete',artifactVersion:'1',contentDigest:'e'.repeat(64)}};
    const running={id:'goal-running',text:'',metadata:{title:'Running work',mode:'goal',status:'running',threadStatus:'running'}};
    const failed={id:'goal-failed',text:'',metadata:{title:'Failed work',mode:'goal',status:'needs_attention',threadStatus:'needs_attention'}};
    const stage={id:'stage-row',text:'# Internal stage',metadata:{title:'Internal stage',status:'complete',goalParentId:'goal-running',processStage:'research'}};
    const checkpoint={id:'goal-checkpoint',text:'',metadata:{title:'Choose a direction',mode:'goal',status:'needs_input',threadStatus:'needs_input',goalPlan:JSON.stringify({state:'approval_required',checkpoint:{id:'cp-1',stageId:'direction',question:'Which direction should Scout use?',options:[{id:'choice-1',label:'Editorial',action:'proceed'}]},subtasks:[{id:'direction',title:'Choose direction',role:'human_checkpoint',status:'running'}]})}};
    artifactEntries=[deck,doc,running,failed,stage,checkpoint];
    const messages={
      status:{id:'status',kind:'thread',text:'Scout started this work.',thread:{id:'run-1',artifactId:running.id,mode:'goal',status:'running',query:'Build the deck'}},
      launch:{id:'launch',kind:'thread',text:'I will deliver the finished presentation here with the evidence linked.',thread:{id:'run-2',artifactId:running.id,mode:'goal',status:'running',query:'Build the deck'}},
      answer:{id:'answer',kind:'message',role:'scout',text:'The market evidence supports a narrow pilot before a national launch.'},
      failed:{id:'failed',kind:'thread',text:'Work failed.',thread:{id:'run-3',artifactId:failed.id,mode:'goal',status:'needs_attention',query:'Build the deck'}},
      stage:{id:'stage',kind:'artifact',text:'Research landed',thread:{artifactId:stage.id,status:'complete'}},
      workRecord:{id:'record',kind:'work_record',work:{status:'complete',title:'Completed work'}},
      governedResult:{id:'governed',kind:'work_result',work:{status:'completed',title:'Evidence brief',summary:'The evidence-linked brief is ready.',workerName:'Researcher',artifactHref:'/api/stride/v1/work/runs/run-1/artifact'}},
      pendingProposal:{id:'proposal-pending',kind:'proposal',proposal:{kind:'goal_run',objective:'Build the presentation',summary:'Review the held request.',status:''}},
      resolvedProposal:{id:'proposal-resolved',kind:'proposal',proposal:{kind:'goal_run',objective:'Build the presentation',summary:'Review the held request.',status:'accepted'}},
      checkpoint:{id:'checkpoint',kind:'thread',thread:{id:'run-4',artifactId:checkpoint.id,mode:'goal',status:'needs_input',query:'Build the deck'}},
      deck:{id:'deck',kind:'thread',text:'Presentation delivered.',thread:{id:'run-5',artifactId:'deck-goal',mode:'goal',status:'complete',goalStatus:'complete',resultArtifactId:deck.id,resultArtifactType:'html_deck',resultArtifactVersion:1,resultArtifactDigest:deck.metadata.contentDigest,resultTitle:'Real deck'}},
      doc:{id:'doc',kind:'thread',text:'Document delivered.',thread:{id:'run-6',artifactId:'doc-goal',mode:'goal',status:'complete',goalStatus:'complete',resultArtifactId:doc.id,resultArtifactType:'markdown',resultArtifactVersion:1,resultArtifactDigest:doc.metadata.contentDigest,resultTitle:'Real report',resultCanEdit:true}}
    };
    scoutChatThreads=[{id:'timeline',messages:Object.values(messages)}];activeScoutThreadId='timeline';
    const renderClass=message=>{const host=document.createElement('div');host.append(scoutChatMessageRecordNode(message));return {children:host.childElementCount,html:host.innerHTML};};
    const visible=Object.fromEntries(Object.entries(messages).map(([key,message])=>[key,scoutChatRecordBelongsInTimeline(message)]));
    const rendered=Object.fromEntries(Object.entries(messages).map(([key,message])=>[key,renderClass(message)]));
    const families={
      processDeck:desktopWorkFamily({processId:'packaging_studio',outputFamily:'Research',mode:'research',query:'research'},null),
      resultDocument:desktopWorkFamily({resultArtifactType:'markdown',outputFamily:'Research',mode:'research'},null),
      scheduled:desktopWorkFamily({mode:'scheduled',query:'make a deck'},null),
	  directDeck:desktopWorkFamily({mode:'deck',query:'research this'},null),
	  directDocument:desktopWorkFamily({mode:'report',query:'analyze this'},null),
      queryIgnored:desktopWorkFamily({query:'make a deck and research it'},null)
    };
    const phaseLabels=Object.fromEntries(Object.entries(studioCustomerPhaseDefinitions).map(([key,phases])=>[key,phases.map(phase=>phase.label)]));
    const phaseStages={
      packagingCompose:studioCustomerPhaseDefinitions.packaging_studio.find(phase=>phase.id==='compose').stages,
      documentReview:studioCustomerPhaseDefinitions.document_report.find(phase=>phase.id==='review').stages
    };
    const plan={processId:'document_report',subtasks:[
      {id:'context_snapshot',status:'complete'},
      {id:'external_research',status:'running'},
      {id:'source_snapshot',status:'pending'},
      {id:'quality_gate',status:'pending'},
      {id:'document_jury',status:'pending'}
    ]};
    const activeGoal={id:'document-goal',text:'# Market report\n\nDecision-ready report.',metadata:{title:'Market report',mode:'goal',processId:'document_report',status:'running',threadStatus:'running',currentStage:'external_research',progressPercent:'42',goalPlan:JSON.stringify(plan)}};
    const activeMessage={id:'document-work',kind:'thread',thread:{id:'document-run',artifactId:activeGoal.id,mode:'goal',processId:'document_report',outputFamily:'Document',status:'running'}};
    artifactEntries=[activeGoal];scoutChatThreads=[{id:'document-thread',messages:[activeMessage]}];activeScoutThreadId='document-thread';
    syncDesktopActiveWorkIndicator();
    const activePill=chatWorkIndicatorText.textContent;
    chatContextState={mode:'work',messageId:activeMessage.id,threadId:'document-thread'};
    renderDesktopWorkContext(activeMessage,activeGoal);
    const sidecarDefault={text:chatContextBody.textContent,internal:Boolean(chatContextBody.querySelector('#chatContextTechnicalWork'))};
    chatContextBody.querySelector('[data-inspect-work="true"]').click();
    const sidecarInspected={text:chatContextBody.textContent,internal:Boolean(chatContextBody.querySelector('#chatContextTechnicalWork'))};
    plan.state='needs_attention';activeGoal.metadata.goalPlan=JSON.stringify(plan);activeGoal.metadata.status='needs_attention';activeGoal.metadata.threadStatus='needs_attention';activeMessage.thread.status='needs_attention';
    syncDesktopActiveWorkIndicator();
    const failedPill=chatWorkIndicatorText.textContent;
    artifactEntries=[];
    const compactPresentation={id:'compact-presentation',kind:'thread',thread:{id:'compact-run',artifactId:'hydrating-artifact',mode:'goal',processId:'packaging_studio',outputFamily:'Presentation',status:'running',currentStage:'external_research',progressPercent:38}};
    scoutChatThreads=[{id:'compact-thread',messages:[compactPresentation]}];activeScoutThreadId='compact-thread';
    syncDesktopActiveWorkIndicator();
    const compactPresentationPill=chatWorkIndicatorText.textContent;
    artifactEntries=[];
	    const blockedWithoutArtifact={id:'blocked-without-artifact',kind:'thread',thread:{id:'blocked-run',artifactId:'missing-artifact',mode:'goal',processId:'packaging_studio',outputFamily:'Presentation',status:'blocked'}};
	    scoutChatThreads=[{id:'blocked-thread',messages:[blockedWithoutArtifact]}];activeScoutThreadId='blocked-thread';
	    syncDesktopActiveWorkIndicator();
	    const blockedWithoutArtifactPill=chatWorkIndicatorText.textContent;
	    const failedStudioRoot={id:'failed-studio-root',kind:'os_artifact',text:'# Failed presentation root',metadata:{title:'Failed presentation root',mode:'goal',processId:'packaging_studio',status:'needs_attention',threadStatus:'needs_attention',progressPercent:'9',goalPlan:JSON.stringify({processId:'packaging_studio',state:'needs_attention',subtasks:[{id:'context_snapshot',title:'Understand the brief',status:'failed'}]})}};
	    const completedInternalStage={id:'completed-internal-stage',kind:'os_artifact',text:'# Internal brief',metadata:{title:'Understand the brief',status:'complete',threadStatus:'complete',goalParentId:failedStudioRoot.id,processStage:'context_snapshot'}};
	    const failedStudioMessage={id:'failed-studio-message',kind:'thread',studioProject:{id:failedStudioRoot.id,kind:'presentation',title:'Failed presentation root',status:'needs_attention',href:'/presentations?project='+failedStudioRoot.id},thread:{id:'failed-studio-run',rootRunId:'failed-studio-run',artifactId:failedStudioRoot.id,mode:'goal',processId:'packaging_studio',outputFamily:'Presentation',status:'error',progressPercent:9}};
	    const anonymousInternalStage={id:'internal-stage-receipt',kind:'artifact',text:'Understand the brief is saved — needs attention: synthesizer output',thread:{id:'',artifactId:completedInternalStage.id,status:'needs_attention'}};
	    artifactEntries=[failedStudioRoot,completedInternalStage];
	    scoutChatThreads=[{id:'failed-studio-thread',messagesLoaded:true,messages:[failedStudioMessage,anonymousInternalStage]}];activeScoutThreadId='failed-studio-thread';
	    syncDesktopActiveWorkIndicator();
	    const internalStageTruth={owned:scoutOwnedActivityMessage([failedStudioMessage,anonymousInternalStage])?.id,pill:chatWorkIndicatorText.textContent,actions:chatWorkIndicatorActions.childElementCount};
	    artifactEntries=[deck];
	    const deliveredThread={id:'delivered-before-switch',messagesLoaded:true,messages:[messages.deck]};
	    const unloadedThread={id:'indicator-loading',messagesLoaded:false};
	    scoutChatThreads=[deliveredThread,unloadedThread];activeScoutThreadId=deliveredThread.id;
	    syncDesktopActiveWorkIndicator();
	    const deliveredBeforeSwitch=chatWorkIndicatorText.textContent;
	    chatWorkIndicatorActions.appendChild(bfEl('button','chat-work-indicator__action-button','stale action'));
	    chatWorkIndicatorActions.hidden=false;
	    scoutChatThreadRequests.set(unloadedThread.id,Promise.resolve(null));
	    selectScoutChatThread(unloadedThread.id);
	    const unloadedSwitch={hidden:chatWorkIndicator.hidden,messageId:chatWorkIndicator.dataset.messageId||'',actions:chatWorkIndicatorActions.childElementCount,actionsHidden:chatWorkIndicatorActions.hidden};
	    scoutChatThreadRequests.delete(unloadedThread.id);
	    unloadedThread.messagesLoaded=true;unloadedThread.messages=[failedStudioMessage,anonymousInternalStage];artifactEntries=[failedStudioRoot,completedInternalStage];
	    renderActiveScoutThread();
	    const hydratedAfterSwitch=chatWorkIndicatorText.textContent;
	    const asset=(ref,kind,mime,name)=>({ref:ref.repeat(64),kind,mime,name});
    const structuredMessages={
      imageV1:{id:'image-v1',kind:'thread',thread:{id:'image-run',rootRunId:'image-run',mode:'work',status:'complete',resultArtifactId:'image-result',resultArtifactType:'image',resultArtifactVersion:2,resultArtifactDigest:'1'.repeat(64),resultTitle:'Campaign hero',resultAssets:[asset('a','image','image/png','hero.png')]}},
      imageV2:{id:'image-v2',kind:'thread',thread:{id:'image-run',rootRunId:'image-run',mode:'work',status:'complete',resultArtifactId:'image-result',resultArtifactType:'image',resultArtifactVersion:3,resultArtifactDigest:'2'.repeat(64),resultTitle:'Campaign hero final',resultAssets:[asset('b','image','image/png','hero-final.png')]}},
      pdf:{id:'pdf',kind:'thread',thread:{id:'pdf-run',rootRunId:'pdf-run',mode:'work',status:'complete',resultArtifactId:'pdf-result',resultArtifactType:'pdf',resultArtifactVersion:1,resultArtifactDigest:'3'.repeat(64),resultTitle:'Board brief',resultAssets:[asset('c','pdf','application/pdf','brief.pdf')]}},
      table:{id:'table',kind:'thread',thread:{id:'table-run',rootRunId:'table-run',mode:'work',status:'complete',resultArtifactId:'table-result',resultArtifactType:'table',resultArtifactVersion:1,resultArtifactDigest:'4'.repeat(64),resultTitle:'Market table',resultTable:{columns:['Market','ARR'],rows:[['North','$2.4M']]}}},
      workbook:{id:'workbook',kind:'thread',thread:{id:'book-run',rootRunId:'book-run',mode:'work',status:'complete',resultArtifactId:'book-result',resultArtifactType:'workbook',resultArtifactVersion:1,resultArtifactDigest:'5'.repeat(64),resultTitle:'Operating model',resultAssets:[asset('d','export','application/vnd.openxmlformats-officedocument.spreadsheetml.sheet','model.xlsx')],resultWorkbook:{fileName:'model.xlsx',mime:'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',sheetCount:3,formulaCount:42,sheets:[{name:'Inputs',purpose:'Assumptions'}]}}},
      workbookPDF:{id:'workbook-pdf',kind:'thread',thread:{id:'book-pdf-run',rootRunId:'book-pdf-run',mode:'work',status:'complete',resultArtifactId:'book-pdf-result',resultArtifactType:'workbook',resultArtifactVersion:1,resultArtifactDigest:'a'.repeat(64),resultTitle:'Fake operating model',resultAssets:[asset('a','export','application/pdf','model.pdf')],resultWorkbook:{fileName:'model.xlsx',mime:'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',sheetCount:3,formulaCount:42}}},
      missingTuple:{id:'missing-tuple',kind:'thread',thread:{id:'missing-tuple-run',rootRunId:'missing-tuple-run',mode:'work',status:'complete',resultArtifactId:'missing-tuple-result',resultArtifactType:'image',resultTitle:'Unbound image',resultAssets:[asset('b','image','image/png','unbound.png')]}},
      bundle:{id:'bundle',kind:'thread',thread:{id:'bundle-run',rootRunId:'bundle-run',mode:'work',status:'complete',resultArtifactId:'bundle-result',resultArtifactType:'bundle',resultArtifactVersion:1,resultArtifactDigest:'6'.repeat(64),resultTitle:'Launch package',resultAssets:[asset('e','export','application/pdf','launch.pdf'),asset('f','file','text/csv','data.csv')]}},
      file:{id:'file',kind:'thread',thread:{id:'file-run',rootRunId:'file-run',mode:'work',status:'complete',resultArtifactId:'file-result',resultArtifactType:'file',resultArtifactVersion:1,resultArtifactDigest:'7'.repeat(64),resultTitle:'Source file',resultAssets:[asset('8','file','text/plain','notes.txt')]}},
      malformed:{id:'malformed',kind:'thread',text:'{"raw":"json"}',thread:{id:'bad-run',rootRunId:'bad-run',mode:'work',status:'complete',resultArtifactId:'bad-result',resultArtifactType:'image',resultArtifactVersion:1,resultArtifactDigest:'9'.repeat(64),resultTitle:'Unsafe image',resultPreview:'{"raw":"json"}',resultAssets:[]}}
    };
    artifactEntries=[];scoutChatThreads=[{id:'structured-thread',messages:Object.values(structuredMessages)}];activeScoutThreadId='structured-thread';
    const structured=Object.fromEntries(Object.entries(structuredMessages).map(([key,message])=>[key,renderClass(message)]));
    const rootV1={id:'root-v1',kind:'thread',thread:{id:'root-run',rootRunId:'root-run',mode:'work',status:'running',query:'Root work'}};
    const rootV2={id:'root-v2',kind:'thread',thread:{id:'root-run',rootRunId:'root-run',mode:'work',status:'running',query:'Current root work'}};
    const explicitChild={id:'child',kind:'thread',thread:{id:'child-run',rootRunId:'root-run',parentRunId:'root-run',mode:'work',status:'needs_attention',query:'Delegated pass'}};
    const nameOnlyChild={id:'name-only',kind:'thread',thread:{id:'name-only-run',mode:'work',status:'running',query:'Unbound pass',agentName:'Designer',delegatedBy:'Scout'}};
    const owned=scoutOwnedActivityMessage([rootV1,rootV2,explicitChild]);
    const nameOnlyOwned=scoutOwnedActivityMessage([rootV2,nameOnlyChild]);
    const governedDirect={id:'governed-latest',kind:'work_record',work:{id:'record',runId:'governed-run',rootRunId:'governed-run',title:'New governed work',status:'running',workerName:'Scout'}};
    const governedOwned=scoutOwnedActivityMessage([structuredMessages.imageV2,governedDirect]);
	    return {visible,rendered,families,phaseLabels,phaseStages,activePill,failedPill,compactPresentationPill,blockedWithoutArtifactPill,internalStageTruth,deliveredBeforeSwitch,unloadedSwitch,hydratedAfterSwitch,sidecarDefault,sidecarInspected,structured,owned:{id:owned?.id,status:owned?.thread?.status},nameOnlyOwned:nameOnlyOwned?.id,governedOwned:governedOwned?.id};
  });
  assert.deepEqual(result.visible,{status:false,launch:false,answer:true,failed:false,stage:false,workRecord:false,governedResult:false,pendingProposal:true,resolvedProposal:false,checkpoint:true,deck:true,doc:true});
  assert.equal(result.rendered.status.children,0);
  assert.equal(result.rendered.failed.children,0);
  assert.equal(result.rendered.stage.children,0);
  assert.equal(result.rendered.workRecord.children,0);
  assert.equal(result.rendered.governedResult.children,0);
  assert.match(result.rendered.pendingProposal.html,/scout-proposal-card/);
  assert.equal(result.rendered.resolvedProposal.children,0);
  assert.equal(result.rendered.launch.children,0);
  assert.match(result.rendered.answer.html,/market evidence supports a narrow pilot/);
  assert.match(result.rendered.checkpoint.html,/scout-chat-work-card__checkpoint/);
  assert.match(result.rendered.deck.html,/scout-chat-deck-result/);
  assert.match(result.rendered.doc.html,/scout-chat-document-result/);
  assert.deepEqual(result.families,{processDeck:'Presentation',resultDocument:'Document',scheduled:'Scheduled work',directDeck:'Presentation',directDocument:'Document',queryIgnored:'Work'});
  assert.deepEqual(result.phaseLabels,{packaging_studio:['Frame','Build','Compose','Review & deliver'],document_report:['Frame','Build','Compose','Review & deliver']});
  assert.ok(result.phaseStages.packagingCompose.includes('identity_candidates'));
  assert.ok(result.phaseStages.packagingCompose.includes('identity_judges'));
  assert.ok(result.phaseStages.packagingCompose.includes('identity_critic'));
  assert.deepEqual(result.phaseStages.documentReview,['document_jury','rendered_admission','publish']);
  assert.equal(result.activePill,'Document · Phase 2/4 · 42%');
  assert.equal(result.failedPill,'Document · Needs attention');
	  assert.equal(result.compactPresentationPill,'Presentation · Phase 2/4 · 38%');
	  assert.equal(result.blockedWithoutArtifactPill,'Presentation · Needs attention');
	  assert.deepEqual(result.internalStageTruth,{owned:'failed-studio-message',pill:'Presentation · Needs attention',actions:0},'an internal completed stage cannot claim that the failed presentation was delivered');
	  assert.equal(result.deliveredBeforeSwitch,'Presentation · Delivered');
	  assert.deepEqual(result.unloadedSwitch,{hidden:true,messageId:'',actions:0,actionsHidden:true},'thread navigation must clear the prior conversation status before hydration');
	  assert.equal(result.hydratedAfterSwitch,'Presentation · Needs attention');
  assert.match(result.sidecarDefault.text,/Frame/);
  assert.match(result.sidecarDefault.text,/Build/);
  assert.match(result.sidecarDefault.text,/Compose/);
  assert.match(result.sidecarDefault.text,/Review & deliver/);
  assert.match(result.sidecarDefault.text,/Inspect work/);
  assert.equal(result.sidecarDefault.internal,false);
  assert.equal(result.sidecarInspected.internal,true);
  assert.match(result.sidecarInspected.text,/Internal work/);
  assert.equal(result.structured.imageV1.children,0,'superseded image result must not render');
  assert.match(result.structured.imageV2.html,/scout-chat-structured-result__image/);
  assert.match(result.structured.pdf.html,/scout-chat-structured-result__pdf/);
  assert.match(result.structured.table.html,/<table/);
  assert.match(result.structured.table.html,/North/);
  assert.match(result.structured.workbook.html,/<strong>3<\/strong><span>Sheets/);
  assert.match(result.structured.workbook.html,/Download XLSX/);
  assert.equal(result.structured.workbookPDF.children,0,'a PDF export cannot be labeled as an XLSX workbook');
  assert.equal(result.structured.missingTuple.children,0,'a result without an exact revision tuple must fail closed');
  assert.match(result.structured.bundle.html,/launch\.pdf/);
  assert.match(result.structured.file.html,/notes\.txt/);
  assert.equal(result.structured.malformed.children,0,'raw JSON/prose cannot substitute for a missing image envelope');
  assert.deepEqual(result.owned,{id:'root-v2',status:'needs_attention'});
  assert.equal(result.nameOnlyOwned,'name-only','display names cannot invent a delegation root');
  assert.equal(result.governedOwned,'governed-latest','new direct governed work must outrank an older typed result in Activity');
  await page.evaluate(({historicalDigest})=>{
    artifactEntries=[];
    const historical={id:'historical-message',kind:'thread',role:'scout',createdAt:'2026-08-22T14:00:00Z',thread:{id:'historical-run',rootRunId:'historical-run',artifactId:'historical-deck',mode:'presentation',status:'complete',goalStatus:'complete',resultArtifactId:'historical-deck',resultArtifactType:'html_deck',resultArtifactVersion:4,resultArtifactDigest:historicalDigest,resultTitle:'Historical exact deck'}};
    scoutChatThreads=[{id:'historical-thread',messages:[historical]}];activeScoutThreadId='historical-thread';
  },{historicalDigest});
  const exactFetched=await page.evaluate(()=>fetchArtifactEntryById('historical-deck',{refresh:true}));
  assert.equal(exactFetched?.id,'historical-deck');
  assert.equal(exactFetched?.metadata?.artifactVersion,'4');
  assert.equal(exactFetched?.metadata?.contentDigest,historicalDigest);
  const historical=await page.evaluate(({historicalDigest})=>{
    const entry=artifactEntries.find(candidate=>candidate.id==='historical-deck');
    const card=scoutChatThread.querySelector('[data-result-artifact-id="historical-deck"]');
    const exactMessage=selectedScoutChatThread()?.messages?.[0];
    const exactHost=document.createElement('div');exactHost.append(scoutChatMessageRecordNode(exactMessage));
    const mismatch={id:'mismatch',kind:'thread',thread:{id:'mismatch-run',artifactId:'historical-deck',mode:'presentation',status:'complete',goalStatus:'complete',resultArtifactId:'historical-deck',resultArtifactType:'html_deck',resultArtifactVersion:4,resultArtifactDigest:'f'.repeat(64),resultTitle:'Wrong digest'}};
    scoutChatThreads=[{id:'mismatch-thread',messages:[mismatch]}];activeScoutThreadId='mismatch-thread';
    const host=document.createElement('div');host.append(scoutChatMessageRecordNode(mismatch));
    return {loaded:Boolean(entry),digest:entry?.metadata?.contentDigest,card:Boolean(card),exactChildren:exactHost.childElementCount,exactHTML:exactHost.innerHTML,mismatchChildren:host.childElementCount,mismatchHTML:host.innerHTML};
	  },{historicalDigest});
	  assert.equal(historical.exactChildren,1);
  assert.match(historical.exactHTML,/scout-chat-deck-result/);
  assert.equal(historical.mismatchChildren,0,'a digest mismatch must fail closed instead of showing a current-by-ID doorway');
  assert.doesNotMatch(historical.mismatchHTML,/Historical exact deck/,'a mismatched current artifact body must not substitute into the bound card');
  const racePreserved=await page.evaluate(async ({raceDigest})=>{
    artifactEntries=artifactEntries.filter(candidate=>candidate.id!=='race-deck');
    const race={id:'race-message',kind:'thread',thread:{id:'race-run',rootRunId:'race-run',artifactId:'race-deck',mode:'presentation',status:'complete',goalStatus:'complete',resultArtifactId:'race-deck',resultArtifactType:'html_deck',resultArtifactVersion:2,resultArtifactDigest:raceDigest,resultTitle:'Stale race deck'}};
    scoutChatThreads=[{id:'race-thread',messages:[race]},{id:'replacement-thread',messages:[]}];activeScoutThreadId='race-thread';
    scoutChatThread.appendChild(Object.assign(document.createElement('p'),{id:'race-sentinel',textContent:'replacement remains'}));
    hydrateScoutResultArtifact(race);
    activeScoutThreadId='replacement-thread';
    await fetchArtifactEntryById('race-deck',{refresh:true});
    await new Promise(resolve=>setTimeout(resolve,0));
    return document.querySelector('#race-sentinel')?.textContent||'';
  },{raceDigest});
  assert.equal(racePreserved,'replacement remains','late exact-ID hydration must not rerender a superseded thread');
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	nodeModules := "/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules"
	if _, err := os.Stat(filepath.Join(nodeModules, "playwright")); err != nil {
		t.Skip("bundled Playwright unavailable")
	}
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodeModules, "RICH_TIMELINE_INDEX="+indexPath, "RICH_TIMELINE_CHROME="+chrome)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered rich timeline contract: %v\n%s", err, output)
	}
}

func TestDesktopActivityPillPreservesDelegatedExactResultOnOpenAndRefreshRendered(t *testing.T) {
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
const html=fs.readFileSync(process.env.ACTIVITY_PILL_INDEX,'utf8');
const digest='d'.repeat(64);
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/artifacts')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/brain/')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true,executablePath:process.env.ACTIVITY_PILL_CHROME});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  await page.goto('http://127.0.0.1:'+server.address().port,{waitUntil:'domcontentloaded'});
  await page.waitForFunction(()=>typeof syncDesktopActiveWorkIndicator==='function'&&typeof syncDesktopOpenChatContext==='function'&&Boolean(authedUser));
  const result=await page.evaluate(({digest})=>{
    const lifecycle={id:'pill-root-lifecycle',kind:'os_artifact',text:'RAW_LIFECYCLE_SENTINEL {"provider":"internal"}',metadata:{title:'Internal work record',mode:'goal',processId:'packaging_studio',status:'running',threadStatus:'running',currentStage:'compose',artifactVersion:'11',contentDigest:'1'.repeat(64),goalPlan:JSON.stringify({processId:'packaging_studio',state:'running',subtasks:[{id:'compose',title:'Compose',status:'running'}]})}};
    const exact={id:'pill-exact-child',kind:'os_artifact',text:'<!doctype html><html><body><main><h1>Exact customer deck</h1></main></body></html>',metadata:{title:'Exact customer deck',type:'html_deck',status:'complete',threadStatus:'complete',artifactVersion:'7',contentDigest:digest}};
    const root={id:'pill-root-message',kind:'thread',thread:{id:'pill-root-run',rootRunId:'pill-root-run',artifactId:lifecycle.id,mode:'work',processId:'packaging_studio',status:'running',query:'Build the exact deck'}};
    const child={id:'pill-child-message',kind:'thread',thread:{id:'pill-child-run',rootRunId:'pill-root-run',parentRunId:'pill-root-run',artifactId:lifecycle.id,mode:'work',status:'complete',resultArtifactId:exact.id,resultArtifactType:'html_deck',resultArtifactVersion:7,resultArtifactDigest:digest,resultTitle:'Exact customer deck',resultCanEdit:true,resultCanPresent:true,resultCanExport:true}};
    artifactEntries=[lifecycle,exact];
    scoutChatThreads=[{id:'pill-thread',messages:[root,child]}];
    activeScoutThreadId='pill-thread';
    const opens=[];const saves=[];
    const originalOpen=openArtifactStage;
    const originalSave=desktopSaveToDriveControl;
    openArtifactStage=(id,title,options)=>{opens.push({id,binding:options?.expectedBinding});return true;};
    desktopSaveToDriveControl=(entry,className,binding)=>{saves.push({id:entry?.id,binding});return bfEl('button',className,'Save to Drive');};
    syncDesktopActiveWorkIndicator();
    const pill={id:chatWorkIndicator.dataset.messageId,text:chatWorkIndicatorText.textContent};
    chatWorkIndicatorOpen.click();
    const snapshot=()=>({
      meta:chatContextMeta.textContent,
      body:chatContextBody.textContent,
      labels:Array.from(chatContextBody.querySelectorAll('button')).map(button=>button.textContent.trim()),
      state:{messageId:chatContextState?.messageId,ownedActivity:chatContextState?.ownedActivity}
    });
    const first=snapshot();
    Array.from(chatContextBody.querySelectorAll('button')).find(button=>button.textContent.trim()==='Open')?.click();
    syncDesktopOpenChatContext();
    const refreshed=snapshot();
    Array.from(chatContextBody.querySelectorAll('button')).find(button=>button.textContent.trim()==='Open')?.click();
    openArtifactStage=originalOpen;
    desktopSaveToDriveControl=originalSave;
    return {pill,first,refreshed,opens,saves};
  },{digest});
  assert.deepEqual(result.pill,{id:'pill-root-message',text:'Presentation · Delivered'});
  for(const snapshot of [result.first,result.refreshed]){
    assert.equal(snapshot.state.messageId,'pill-root-message');
    assert.equal(snapshot.state.ownedActivity,true);
    assert.match(snapshot.meta,/Delivered/);
    assert.match(snapshot.body,/Complete/);
    assert.ok(snapshot.labels.includes('Open'));
    assert.ok(snapshot.labels.includes('Save to Drive'));
    assert.ok(!snapshot.labels.includes('Inspect work'));
    assert.doesNotMatch(snapshot.body,/RAW_LIFECYCLE_SENTINEL/);
  }
  assert.deepEqual(result.opens.map(call=>call.id),['pill-exact-child','pill-exact-child']);
  assert.ok(result.opens.every(call=>call.binding?.artifactId==='pill-exact-child'&&call.binding?.version===7&&call.binding?.digest===digest));
  assert.ok(result.saves.length>=2,'the pill and refreshed sidecar must both offer exact-result Save');
  assert.ok(result.saves.every(call=>call.id==='pill-exact-child'));
  assert.ok(result.saves.every(call=>call.binding?.artifactId==='pill-exact-child'&&call.binding?.version===7&&call.binding?.digest===digest));
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
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodeModules, "ACTIVITY_PILL_INDEX="+indexPath, "ACTIVITY_PILL_CHROME="+chrome)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered delegated Activity pill contract: %v\n%s", err, output)
	}
}
