package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagingStageDrawerProgressiveJudgmentContract(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"const packagingStudioStagePresentation",
		"const packagingStudioCustomerPhases",
		"function packagingStudioCustomerProgress(plan, artifact, ref, status)",
		"function packagingStudioPhaseListNode(progress)",
		"function packagingStudioTechnicalWorkNode(plan)",
		"Frame the decision",
		"Ground the recommendation",
		"stages: ['external_research', 'source_snapshot', 'evidence_entailment', 'evidence']",
		"Build the story",
		"Design the presentation",
		"Finish the presentation",
		"Phase ${customerProgress.currentNumber} of ${customerProgress.count}",
		"function packagingStudioTaskDisplayTitle(plan, task)",
		"function packagingStudioCheckpointQuestion(plan, checkpoint)",
		"Write the deck",
		"packagingStudioRequestedSlideCount",
		"Add presenter notes",
		"Build the editable presentation",
		"const packagingStudioJudgmentStages",
		"red_team:",
		"compete_architects:",
		"compete_judges:",
		"function artifactStageActivityContext(entry)",
		"Blocking verdict in output",
		"Full stage output",
		"{ stageActivity: true }",
		"artifact-read__section-disclosure",
		"artifact-read__code-block",
		"wrap.setAttribute('role', 'region')",
		"position: sticky;",
		"overscroll-behavior: contain;",
		"artifactAssetIsRenderedPage(asset)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("packaging stage drawer contract missing %q", want)
		}
	}
	phaseMapStart := strings.Index(html, "const packagingStudioCustomerPhases = [")
	phaseMapEnd := strings.Index(html[phaseMapStart:], "function packagingStudioCanonicalProgress")
	if phaseMapStart < 0 || phaseMapEnd < 0 {
		t.Fatal("Packaging Studio customer phase map boundaries missing")
	}
	phaseMap := html[phaseMapStart : phaseMapStart+phaseMapEnd]
	if got := strings.Count(phaseMap, "id: '"); got != 5 {
		t.Errorf("customer phase map has %d phases, want 5", got)
	}
	stageCSSStart := strings.Index(html, ".artifact-stage-activity {")
	stageCSSEnd := strings.Index(html[stageCSSStart:], ".artifact-stage__body--deck")
	if stageCSSStart < 0 || stageCSSEnd < 0 {
		t.Fatal("stage activity CSS boundaries missing")
	}
	if strings.Contains(html[stageCSSStart:stageCSSStart+stageCSSEnd], "transition: all") {
		t.Error("stage polish must not introduce transition: all")
	}
}

func TestContentStudioDesktopRailContract(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		`class="pd1-primary-nav__external"`,
		`href="https://kino.grok.me"`,
		`aria-label="Open Content Studio"`,
		`aria-haspopup="dialog"`,
		`function openContentStudio(returnFocus)`,
		`function closeContentStudio()`,
		`frame.referrerPolicy = 'strict-origin-when-cross-origin'`,
		`allow-scripts allow-forms allow-same-origin allow-popups allow-popups-to-escape-sandbox allow-downloads`,
		`external.target = '_blank'`,
		`external.rel = 'noopener noreferrer'`,
		`function setContentStudioBackgroundInert(inert)`,
		`appShell.setAttribute('inert', '')`,
		`ariaHidden: appShell.getAttribute('aria-hidden')`,
		`content-studio-drawer__focus-sentinel--before`,
		`content-studio-drawer__focus-sentinel--after`,
		`drawer.dataset.lastFocusBoundary = 'after'`,
		`.pd1-primary-nav__external:active { scale: 0.96; }`,
		`.pd1-primary-nav__external-wrap { display: none !important; }`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Content Studio rail contract missing %q", want)
		}
	}
	if strings.Contains(html, `href="https://www.kino.grok.me"`) {
		t.Error("Content Studio rail points at the unresolvable www host")
	}
	if strings.Contains(html, `Content Studio ↗`) {
		t.Error("Content Studio visible label must not add punctuation")
	}
}

func TestPackagingStageDrawerRenderedJourney(t *testing.T) {
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
const html=fs.readFileSync(process.env.PACKAGING_STAGE_INDEX,'utf8');
const tick=String.fromCharCode(96);
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
const tableRows=Array.from({length:18},(_,i)=>'| '+(i+1)+' | Objection '+(i+1)+' | '+('Specific risk evidence '.repeat(16))+' | Fix the claim |').join('\n');
const fixtures=[
 {id:'stage-red',stage:'red_team',title:'Red-team — the hostile room, with teeth',display:'Stress-test the brief',role:'panel',text:'# Red-team verdict\n\nBLOCKED FOR PRODUCTION.\n\nThe saved output explicitly says the source context is incomplete and the claims should not ship.\n\n## Objection ledger\n\n| # | objection | evidence | repair |\n|---|---|---|---|\n'+tableRows+'\n\n## Strengths to keep\n\n- Source language is direct.\n- The wedge is memorable.'},
 {id:'stage-identity',stage:'identity',title:'Identity — develop the visual system',display:'Build the visual system',role:'judges',text:'# Identity direction\n\n## Decision\n\nDirection B wins because the panel recorded the strongest audience fit.\n\n## Tokens\n\n'+tick+tick+tick+'css\n:root {\n  --ink: #151513;\n  --paper: #f5f0e7;\n  --heat: #e45d32;\n}\n'+tick+tick+tick+'\n\n## Sources\n\n- [OpenAI API](https://api.openai.com/v1)'},
 {id:'stage-architects',stage:'compete_architects',title:'Compete — three rival narrative architects',display:'Explore narrative directions',role:'panel',text:'# Narrative competition\n\n## Spine matrix\n\n| beat | cultural moment | franchise playbook | leadership conviction |\n|---|---|---|---|\n| opening | The shift | The machine | The earned insight |\n| ask | Move now | Build the flywheel | Back the team |\n\n## Cultural moment\n\n'+('A complete slide-by-slide spine. '.repeat(85))+'\n\n## Franchise playbook\n\n'+('A distinct expandable narrative. '.repeat(85))},
 {id:'stage-judges',stage:'compete_judges',title:'Compete — judge the spines',display:'Choose the strongest story',role:'judges',text:'# Jury verdict\n\n## Winner\n\nLeadership conviction wins unanimously, 4–0.\n\n## Scorecard\n\n| spine | excitement | coherence | credibility |\n|---|---:|---:|---:|\n| leadership conviction | 9 | 9 | 9 |\n| cultural moment | 8 | 7 | 8 |'},
 {id:'stage-write',stage:'write',title:'Write — graft the winning spine',display:'Write the 8-slide deck',role:'synthesizer',text:'# Deck manuscript\n\n## Slide 1 — The opening\n\nThe recorded opening line.\n\n## Slide 2 — The shift\n\nThe recorded argument.\n\n## Speaker notes\n\n[BEAT] The recorded delivery note.\n\n## Composition\n\n| slide | layout | source note |\n|---|---|---|\n| 1 | full bleed | source brief |'}
].map(entry=>({id:entry.id,display:entry.display,text:entry.text,createdAt:new Date().toISOString(),metadata:{title:entry.title,type:'markdown',status:'complete',threadStatus:'complete',source:'process_stage',processId:'packaging_studio',processStage:entry.stage,goalSubtaskId:entry.stage,goalParentId:'packaging-goal',processRole:entry.role}}));
const checkpoint={id:'checkpoint-aaaaaaaaaaaaaaaaaaaaaaaa',stageId:'compete_choice',question:'Which narrative spine should become the deck backbone?',options:[{id:'option-111111111111111111111111',label:'Founder conviction',action:'proceed'},{id:'option-222222222222222222222222',label:'Cultural moment',action:'revise'},{id:'option-333333333333333333333333',label:'Hold for founder review',action:'hold'}]};
const parentPlan={processId:'packaging_studio',state:'execute',objective:'Create an 8-slide presentation for the operating team',subtasks:[
 {id:'context_snapshot',title:'Understand the brief',role:'synthesizer',status:'complete',artifactId:'stage-identity'},
 {id:'external_research',title:'Verify the facts that matter',role:'writer',status:'running'},
 {id:'evidence',title:'Lock the evidence',role:'synthesizer',status:'pending'},
 {id:'story_architects',title:'Find the strongest story',role:'panel',status:'pending'},
 {id:'write',title:'Build the 10-slide story',role:'synthesizer',status:'pending'},
 {id:'gate',title:'Stress-test the story and copy',role:'gate',status:'pending'},
 {id:'voice',title:'Write presenter notes',role:'writer',status:'pending'},
 {id:'identity',title:'Create the visual identity',role:'judges',status:'pending'},
 {id:'imagery_direction',title:'Direct the imagery',role:'writer',status:'pending'},
 {id:'imagery_generate',title:'Generate selected imagery',role:'compile',status:'pending'},
 {id:'layout_plan',title:'Compose every slide',role:'writer',status:'pending'},
 {id:'ship_deck',title:'Build the editable presentation',role:'writer',status:'pending'},
 {id:'draft_compile',title:'Render the draft for review',role:'compile',status:'pending'},
 {id:'slide_jury',title:'Review every rendered slide',role:'compile',status:'pending'},
 {id:'quality_gate',title:'Hold or repair the presentation',role:'gate',status:'pending'},
 {id:'ship_compile',title:'Presentation ready',role:'compile',status:'pending'}
]};
const parent={id:'packaging-goal',text:'# Packaging Studio',createdAt:new Date().toISOString(),metadata:{title:'Packaging Studio',mode:'goal',processId:'packaging_studio',status:'running',threadStatus:'running',currentStage:'execute',progressPercent:'11',goalPlan:JSON.stringify(parentPlan)}};
const checkpointParentPlan={processId:'packaging_studio',state:'approval_required',objective:'Create an 8-slide presentation for the operating team',checkpoint,subtasks:[{id:'compete_judges',title:'Compete — judge the spines',role:'judges',status:'complete',artifactId:'stage-judges'},{id:'compete_choice',title:'Choose the winning spine',role:'human_checkpoint',status:'running',dependsOn:['compete_judges']}]};
const checkpointParent={id:'packaging-checkpoint-goal',text:'# Packaging Studio checkpoint',createdAt:new Date().toISOString(),metadata:{title:'Packaging Studio',mode:'goal',processId:'packaging_studio',status:'approval_required',threadStatus:'approval_required',goalPlan:JSON.stringify(checkpointParentPlan),checkpoint:JSON.stringify(checkpoint)}};
fixtures.find(entry=>entry.id==='stage-judges').metadata.goalParentId=checkpointParent.id;
fixtures.push({id:'legacy-writer-stage',display:'',text:'Writer output',createdAt:new Date().toISOString(),metadata:{title:'Ship — the self-contained presenter deck',source:'agent_thread',goalParentId:'packaging-goal',goalSubtaskId:'ship_deck',processStage:'ship_deck',status:'complete'}});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:820}});
 await page.route('https://kino.grok.me/**',route=>route.fulfill({status:200,contentType:'text/html',body:'<!doctype html><title>KINO</title><main>KINO fixture</main>'}));
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(({entries,parent,checkpointParent})=>{document.getElementById('appShell').dataset.tool='chat';artifactEntries=[...entries,parent,checkpointParent];const trigger=document.createElement('button');trigger.id='stage-return-focus';trigger.textContent='Open stage';document.body.appendChild(trigger);trigger.focus();},{entries:fixtures,parent,checkpointParent});

 const customerStage=await page.evaluate(()=>{
   runlogOpen=null;
   const node=scoutStageArtifactNode({text:'Write — graft the winning spine is in — synthesizer output',thread:{artifactId:'stage-write',query:'Write — graft the winning spine'}});
   document.body.appendChild(node);
   const artifact=artifactEntries.find(entry=>entry.id==='stage-write');
   return {
     runlogs:document.querySelectorAll('.runlog').length,
     threadQuery:scoutThreadFromArtifact(artifact).query,
     artifactTitle:artifactDisplayTitle(artifact)
   };
 });
 assert.deepEqual(customerStage,{runlogs:0,threadQuery:'Write the 8-slide deck',artifactTitle:'Write the 8-slide deck'});
 const neutralWrite=await page.evaluate(()=>packagingStudioTaskDisplayTitle({processId:'packaging_studio',objective:'Build a concise presentation'},{id:'write',title:'Build the 10-slide story'}));
 assert.equal(neutralWrite,'Write the deck');
 const legacyWriter=await page.evaluate(()=>{const artifact=artifactEntries.find(entry=>entry.id==='legacy-writer-stage');return {title:artifactDisplayTitle(artifact),query:scoutThreadFromArtifact(artifact).query};});
 assert.deepEqual(legacyWriter,{title:'Build the editable presentation',query:'Build the editable presentation'});
 const legacyResearchCard=await page.evaluate(()=>{const artifact=artifactEntries.find(entry=>entry.id==='legacy-writer-stage');const card=scoutChatResearchNode({id:'legacy-run',mode:'artifacts',query:'Ship — the self-contained presenter deck — raw writer prompt',status:'complete',artifact});return card.querySelector('.scout-chat-research__title').textContent;});
 assert.equal(legacyResearchCard,'artifact · Build the editable presentation');
 const ordinaryResearchCard=await page.evaluate(()=>{const artifact={id:'ordinary-research',text:'Analysis',metadata:{title:'Pricing analysis',source:'scout_thread',status:'complete'}};const card=scoutChatResearchNode({id:'ordinary-run',mode:'research',query:'Pricing analysis',status:'complete',artifact});updateScoutChatResearchNode(card,'complete',artifact);return card.querySelector('.scout-chat-research__title').textContent;});
 assert.equal(ordinaryResearchCard,'research · Pricing analysis');

	// A process worker emits a generic terminal thread record and an authored
	// stage receipt for the same artifact. Only the compact receipt belongs in
	// the channel; the generic card must never leak HTML/CSS or double the DOM.
	const duplicateProjection=await page.evaluate(()=>{
	  const duplicateArtifact={id:'stage-ship-duplicate',text:'<!doctype html><html><head><title>Like A Farmer</title><style>railwrap{display:grid}</style></head><body></body></html>',metadata:{title:'railwrap{display:grid}',type:'markdown',status:'complete',threadStatus:'complete',source:'process_stage',processId:'packaging_studio',processStage:'ship_deck',goalSubtaskId:'ship_deck',goalParentId:'packaging-goal'}};
	  artifactEntries.push(duplicateArtifact);
	  const generic={id:'generic-ship',kind:'thread',role:'scout',createdAt:'2026-08-20T20:00:00Z',thread:{id:'run-ship',mode:'workflow',artifactId:duplicateArtifact.id,status:'complete',query:'railwrap{display:grid}'}};
	  const receipt={id:'receipt-ship',kind:'artifact',role:'scout',text:'Build the editable presentation is in',createdAt:'2026-08-20T20:00:01Z',thread:{id:'run-ship',mode:'workflow',artifactId:duplicateArtifact.id,status:'complete',query:'Build the editable presentation'}};
	  scoutChatThreads=[{id:'duplicate-channel',title:'Like A Farmer',visibility:'public',messagesLoaded:true,updatedAt:'2026-08-20T20:00:01Z',messages:[generic,receipt]}];
	  activeScoutThreadId='duplicate-channel';
	  renderActiveScoutThread({forceBottom:true});
	  return {research:scoutChatThread.querySelectorAll('.scout-chat-research').length,runlogs:scoutChatThread.querySelectorAll('.runlog').length,text:scoutChatThread.textContent};
	});
	assert.equal(duplicateProjection.research,0,JSON.stringify(duplicateProjection));
	assert.equal(duplicateProjection.runlogs,0,JSON.stringify(duplicateProjection));
	assert.doesNotMatch(duplicateProjection.text,/railwrap/);
 await page.evaluate(()=>{document.querySelector('.runlog')?.remove();runlogOpen=null;});

 const quietChannel=await page.evaluate(parent=>{
   document.getElementById('appShell').dataset.tool='chat';
   document.getElementById('chatTool').style.display='flex';
   const message={id:'goal-message',kind:'thread',role:'scout',thread:{id:'packaging-run',mode:'goal',artifactId:parent.id,status:'running',progressPercent:11,query:'Like A Farmer presentation',projectTitle:'Like A Farmer'}};
   const card=scoutDesktopGoalWorkCardNode(message,parent);
   document.getElementById('chatTool').appendChild(card);
   return {
     title:card.querySelector('.scout-chat-work-card__title')?.textContent,
     eyebrow:card.querySelector('.scout-chat-work-card__eyebrow')?.textContent,
     meta:card.querySelector('.scout-chat-work-card__meta')?.textContent,
     checkpoints:card.querySelectorAll('.scout-chat-work-card__checkpoint').length,
     runlogs:document.querySelectorAll('.runlog').length,
     progressbars:card.querySelectorAll('[role="progressbar"]').length
   };
 },parent);
 assert.equal(quietChannel.title,'Packaging Studio');
 assert.equal(quietChannel.eyebrow,'Presentation · Draft · Phase 2 of 5');
 assert.match(quietChannel.meta,/verifying the proof points/i);
 assert.match(quietChannel.meta,/11%/);
 assert.equal(quietChannel.checkpoints,0);
 assert.doesNotMatch(quietChannel.eyebrow+quietChannel.meta,/Needs input/i);
 assert.equal(quietChannel.runlogs,0);
 assert.equal(quietChannel.progressbars,0);
 await page.setViewportSize({width:390,height:844});await page.waitForTimeout(60);
 const compactProgress=await page.locator('[data-work-artifact-id="packaging-run"].scout-chat-work-card--presentation').evaluate(card=>{const rect=node=>node.getBoundingClientRect().toJSON();const title=card.querySelector('.scout-chat-work-card__title');const project=card.querySelector('.scout-chat-work-card__project');const eyebrow=card.querySelector('.scout-chat-work-card__eyebrow');return {className:card.className,card:rect(card),title:rect(title),project:rect(project),eyebrow:eyebrow.textContent,titleText:title.textContent,projectText:project.textContent,titleWhiteSpace:getComputedStyle(title).whiteSpace,projectWhiteSpace:getComputedStyle(project).whiteSpace,scrollWidth:document.documentElement.scrollWidth};});
 assert.equal(compactProgress.eyebrow,'Presentation · Draft · Phase 2 of 5');
 assert.equal(compactProgress.titleText,'Packaging Studio');
 assert.equal(compactProgress.projectText,'Like A Farmer');
 assert.equal(compactProgress.titleWhiteSpace,'nowrap',JSON.stringify(compactProgress));
 assert.equal(compactProgress.projectWhiteSpace,'nowrap',JSON.stringify(compactProgress));
 assert.ok(compactProgress.title.width>80&&compactProgress.project.width>40,JSON.stringify(compactProgress));
 assert.ok(compactProgress.card.left>=0&&compactProgress.card.right<=390&&compactProgress.scrollWidth<=390,JSON.stringify(compactProgress));
 if(process.env.PACKAGING_PROGRESS_PHONE_SCREENSHOT){await page.screenshot({path:process.env.PACKAGING_PROGRESS_PHONE_SCREENSHOT,fullPage:true});}
 await page.setViewportSize({width:1280,height:820});await page.waitForTimeout(60);
 await page.locator('.scout-chat-work-card--presentation').getByRole('button',{name:/View .* activity/}).click();
 const activityDrawer=page.locator('#chatContextRail');
 await page.waitForFunction(()=>document.querySelector('#chatContextRail')?.hidden===false);
 assert.equal(await activityDrawer.locator('.chat-context-section-title').filter({hasText:'Presentation activity'}).count(),1);
 assert.equal(await activityDrawer.locator('.chat-context-phase-entry').count(),5);
 assert.equal(await activityDrawer.locator('.chat-context-phase-entry.is-current').getAttribute('data-phase'),'ground');
 assert.match(await activityDrawer.locator('.chat-context-phase-entry.is-current').textContent(),/Ground the recommendation.*verifying the proof points/is);
 assert.equal(await activityDrawer.locator('.chat-context-phase-entry[data-phase="design"] .chat-context-phase-entry__sentence').count(),0);
 assert.match(await activityDrawer.locator('#chatContextMeta').textContent(),/Phase 2 of 5.*11%/);
 assert.equal(await activityDrawer.locator('.chat-context-technical').count(),0);
 assert.equal(await activityDrawer.getByText(/internal steps/i).count(),0);
 const inspectWork=activityDrawer.getByRole('button',{name:'Inspect work',exact:true});
 assert.equal(await inspectWork.count(),1);
 assert.equal(await inspectWork.getAttribute('aria-expanded'),'false');
 await inspectWork.click();
 const technicalWork=activityDrawer.locator('#chatContextTechnicalWork');
 assert.equal(await technicalWork.count(),1);
 assert.match(await technicalWork.textContent(),/Internal work.*16 steps/is);
 assert.equal(await technicalWork.locator('.chat-context-log-entry').count(),16);
 const hideWork=activityDrawer.getByRole('button',{name:'Hide work',exact:true});
 assert.equal(await hideWork.getAttribute('aria-expanded'),'true');
 await page.waitForFunction(()=>document.activeElement?.id==='chatContextTechnicalWork');
 const savedStage=technicalWork.getByRole('button',{name:'Open Understand the request and company context',exact:true});
 assert.equal(await savedStage.count(),1);
 await savedStage.click();
 const stageDrawer=page.locator('.artifact-stage');
 await stageDrawer.waitFor({state:'visible'});
 assert.match(await stageDrawer.locator('.artifact-stage__title').textContent(),/Build the visual system/);
 assert.equal(await stageDrawer.locator('.artifact-stage-activity').count(),1);
 await stageDrawer.getByRole('button',{name:'Close',exact:true}).click();
 await hideWork.click();
 assert.equal(await activityDrawer.locator('.chat-context-technical').count(),0);
 assert.equal(await activityDrawer.getByRole('button',{name:'Inspect work',exact:true}).getAttribute('aria-expanded'),'false');
 await activityDrawer.getByRole('button',{name:'Inspect work',exact:true}).click();
 assert.equal(await activityDrawer.locator('.chat-context-technical').count(),1);
 await page.evaluate(parent=>{
   const plan=JSON.parse(parent.metadata.goalPlan);
   plan.state='complete';
   plan.subtasks=plan.subtasks.map(task=>({...task,status:'complete'}));
   const artifact={...parent,metadata:{...parent.metadata,status:'complete',threadStatus:'complete',progressPercent:'100',goalPlan:JSON.stringify(plan)}};
   const message={id:'goal-message',kind:'thread',role:'scout',thread:{id:'packaging-run',mode:'goal',artifactId:artifact.id,status:'complete',progressPercent:100,query:'Like A Farmer presentation'}};
   renderDesktopWorkContext(message,artifact);
 },parent);
 assert.equal(await activityDrawer.locator('.chat-context-technical').count(),0);
 assert.equal(await activityDrawer.getByRole('button',{name:'Hide work',exact:true}).count(),0);
 assert.equal(await activityDrawer.getByRole('button',{name:'Open',exact:true}).count(),1);
 await page.locator('#chatContextClose').evaluate(node=>node.click());
 await page.locator('.scout-chat-work-card--presentation').evaluate(node=>node.remove());

 const blockedWithoutDecision=await page.evaluate(parent=>{
   const plan=JSON.parse(parent.metadata.goalPlan);
   plan.state='needs_attention';
   plan.subtasks=plan.subtasks.map(task=>task.id==='external_research'?{...task,status:'blocked'}:task);
   const artifact={...parent,id:'packaging-blocked',metadata:{...parent.metadata,status:'needs_attention',threadStatus:'needs_attention',progressPercent:'12',goalPlan:JSON.stringify(plan)}};
   const message={id:'goal-blocked-message',kind:'thread',role:'scout',thread:{id:'packaging-blocked-run',mode:'goal',artifactId:artifact.id,status:'needs_attention',progressPercent:12,query:'Like A Farmer presentation'}};
   const card=scoutDesktopGoalWorkCardNode(message,artifact);
   document.body.appendChild(card);
   return {text:card.textContent,eyebrow:card.querySelector('.scout-chat-work-card__eyebrow')?.textContent,meta:card.querySelector('.scout-chat-work-card__meta')?.textContent,checkpoints:card.querySelectorAll('.scout-chat-work-card__checkpoint').length};
 },parent);
 assert.equal(blockedWithoutDecision.eyebrow,'Presentation · Blocked · Phase 2 of 5');
 assert.match(blockedWithoutDecision.meta,/recovering from an evidence check/i);
 assert.equal(blockedWithoutDecision.checkpoints,0);
 assert.doesNotMatch(blockedWithoutDecision.text,/Needs input/i);
 await page.locator('[data-work-artifact-id="packaging-blocked-run"]').evaluate(node=>node.remove());

 const blockedStoryPrecedesReadyDesign=await page.evaluate(parent=>{
   const base=JSON.parse(parent.metadata.goalPlan);
   const complete=new Set(['context_snapshot','external_research','evidence','story_architects','write']);
   const plan={...base,state:'needs_attention',subtasks:base.subtasks.map(task=>{
     if(complete.has(task.id))return {...task,status:'complete'};
     if(task.id==='gate')return {...task,status:'blocked'};
     if(task.id==='identity')return {...task,status:'ready'};
     return {...task,status:'pending'};
   })};
   const artifact={...parent,metadata:{...parent.metadata,status:'needs_attention',threadStatus:'needs_attention',progressPercent:'29',goalPlan:JSON.stringify(plan)}};
   const progress=packagingStudioCustomerProgress(plan,artifact,null,'needs_attention');
   return {id:progress.current.id,status:progress.current.status,number:progress.currentNumber,sentence:progress.current.sentence};
 },parent);
 assert.deepEqual(blockedStoryPrecedesReadyDesign,{id:'story',status:'blocked',number:3,sentence:'Scout is revising the story against the brief.'});

 const savedGoalCopy=await page.evaluate(()=>{
   const terminal=document.createElement('div');
   const card=document.createElement('article');
   const savedPlan={processId:'packaging_studio',subtasks:[
     {id:'compete_judges',title:'Compete — judge the spines, steal the best beats',artifactId:'stage-judges'},
     {id:'compete_choice',title:'Choose the winning spine',role:'human_checkpoint',dependsOn:['compete_judges']}
   ]};
   const savedCheckpoint={stageId:'compete_choice',question:"Which of the founder's VERBATIM words should shape the deck?",options:[]};
   goalCardRenderCheckpoint(terminal,card,{id:'saved-packaging-goal'},savedPlan,savedCheckpoint);
   document.body.appendChild(terminal);
   return {
     question:terminal.querySelector('.goalcard__checkpoint-question')?.textContent,
     door:terminal.querySelector('.goalcard__terminal-actions .goalcard__link')?.textContent
   };
 });
 assert.deepEqual(savedGoalCopy,{question:'Scout evaluated three narrative directions. Which one should shape the deck?',door:'review · Choose the strongest story'});

 const external=page.locator('.pd1-primary-nav__external');
 await external.waitFor({state:'visible'});
 assert.equal(await external.getAttribute('href'),'https://kino.grok.me');
 assert.equal(await external.getAttribute('aria-haspopup'),'dialog');
 const externalBox=await external.boundingBox();
 assert.ok(externalBox.width>=40&&externalBox.height>=40,JSON.stringify(externalBox));
 const backgroundBefore=await page.locator('#appShell').evaluate(node=>({hadInert:node.hasAttribute('inert'),ariaHidden:node.getAttribute('aria-hidden')}));
 await external.click();
 const studio=page.locator('#contentStudioDrawer');
 await studio.waitFor({state:'visible'});
 assert.equal(await studio.locator('.content-studio-drawer__title').textContent(),'Content Studio');
 const studioFrame=studio.locator('iframe');
 assert.equal(await studioFrame.getAttribute('src'),'https://kino.grok.me');
 assert.equal(await studioFrame.getAttribute('title'),'Content Studio');
 assert.equal(await studioFrame.getAttribute('referrerpolicy'),'strict-origin-when-cross-origin');
 assert.match(await studioFrame.getAttribute('sandbox'),/allow-scripts/);
 const studioExternal=studio.locator('.content-studio-drawer__actions .content-studio-drawer__action');
 assert.equal(await studioExternal.getAttribute('href'),'https://kino.grok.me');
 assert.equal(await studioExternal.getAttribute('target'),'_blank');
 assert.equal(await studioExternal.getAttribute('rel'),'noopener noreferrer');
 assert.equal(await studio.locator('.content-studio-drawer__close').evaluate(node=>node===document.activeElement),true);
 assert.equal(await page.locator('#appShell').getAttribute('inert'),'');
 assert.equal(await page.locator('#appShell').getAttribute('aria-hidden'),'true');
 await page.evaluate(()=>{window.__contentStudioBackgroundFocus=[];document.addEventListener('focusin',event=>{const drawer=document.getElementById('contentStudioDrawer');const shell=document.getElementById('appShell');if(drawer&&shell?.contains(event.target))window.__contentStudioBackgroundFocus.push(event.target.id||event.target.getAttribute?.('aria-label')||event.target.tagName);});});
 // Close -> iframe is an explicit parent-owned handoff. When focus leaves the
 // foreign document, the after sentinel returns it to the first header action.
 await page.keyboard.press('Tab');
 assert.equal(await page.evaluate(()=>document.activeElement?.tagName),'IFRAME');
 await page.keyboard.press('Tab');
 await page.waitForTimeout(30);
 assert.equal(await studio.getAttribute('data-last-focus-boundary'),'after');
 assert.equal(await studioExternal.evaluate(node=>node===document.activeElement),true);
 // Reverse traversal mirrors the same contract through the before sentinel.
 await page.keyboard.press('Shift+Tab');
 assert.equal(await page.evaluate(()=>document.activeElement?.tagName),'IFRAME');
 await page.keyboard.press('Shift+Tab');
 await page.waitForTimeout(30);
 assert.equal(await studio.getAttribute('data-last-focus-boundary'),'before');
 assert.equal(await studio.locator('.content-studio-drawer__close').evaluate(node=>node===document.activeElement),true);
 assert.deepEqual(await page.evaluate(()=>window.__contentStudioBackgroundFocus),[]);
 await page.keyboard.press('Escape');
 await studio.waitFor({state:'detached'});
 assert.equal(await external.evaluate(node=>node===document.activeElement),true);
 assert.deepEqual(await page.locator('#appShell').evaluate(node=>({hadInert:node.hasAttribute('inert'),ariaHidden:node.getAttribute('aria-hidden')})),backgroundBefore);

 await page.locator('#stage-return-focus').focus();
 await page.evaluate(()=>openArtifactStage('stage-red','Red-team'));
 const dialog=page.locator('.artifact-stage');
 await dialog.waitFor({state:'visible'});
 assert.match(await dialog.locator('.artifact-stage__kicker').textContent(),/packaging studio · Stress-test the brief · Blocking verdict/);
 assert.equal(await dialog.locator('.artifact-stage-activity__state').getAttribute('data-tone'),'attention');
 assert.match(await dialog.locator('.artifact-stage-activity__summary').textContent(),/BLOCKED FOR PRODUCTION/);
 assert.equal(await dialog.locator('.artifact-stage-activity__record').getAttribute('open'),null);
 assert.equal(await dialog.locator('.artifact-stage-activity__record-body .artifact-read__section').count(),0);
 assert.equal(await dialog.locator('.artifact-stage__close').evaluate(node=>node===document.activeElement),true);
 assert.equal((await dialog.locator('.artifact-stage__head').evaluate(node=>getComputedStyle(node).position)),'sticky');
 await dialog.locator('.artifact-stage-activity__record > summary').click();
 await dialog.locator('.artifact-read__section').first().waitFor({state:'visible'});
 assert.ok(await dialog.locator('.artifact-read__section-disclosure').count()>=1);
 const ledger=dialog.locator('.artifact-read__section-disclosure').first();
 await ledger.locator('summary').click();
 const tableRegion=dialog.locator('.artifact-read__table-wrap').first();
 await tableRegion.waitFor({state:'visible'});
 assert.equal(await tableRegion.getAttribute('role'),'region');
 assert.equal(await tableRegion.getAttribute('tabindex'),'0');
 assert.match(await tableRegion.getAttribute('aria-label'),/Scrollable table/);
 await page.keyboard.press('Escape');
 await dialog.waitFor({state:'detached'});
 assert.equal(await page.locator('#stage-return-focus').evaluate(node=>node===document.activeElement),true);

 for(const fixture of fixtures.slice(1)){
   await page.evaluate(id=>openArtifactStage(id,id),fixture.id);
   await page.locator('.artifact-stage').waitFor({state:'visible'});
   assert.match(await page.locator('.artifact-stage__kicker').textContent(),new RegExp(fixture.display));
   await page.locator('.artifact-stage__close').click();
 }

 await page.evaluate(()=>openArtifactStage('stage-judges','Compete judges'));
 const checkpointPanel=page.locator('.artifact-stage-activity__checkpoint');
 await checkpointPanel.waitFor({state:'visible'});
 assert.equal(await checkpointPanel.locator('.scout-chat-work-card__checkpoint-question').textContent(),'Scout evaluated three narrative directions. Which one should shape the deck?');
 assert.equal(await checkpointPanel.locator('.scout-chat-work-card__checkpoint-choice').count(),3);
 assert.equal(await checkpointPanel.locator('[role="group"]').getAttribute('aria-labelledby'),await checkpointPanel.locator('.scout-chat-work-card__checkpoint-question').getAttribute('id'));
 await page.locator('.artifact-stage__close').click();

 await page.evaluate(()=>openArtifactStage('stage-identity','Identity'));
 await page.locator('.artifact-stage-activity__record > summary').click();
 await page.locator('.artifact-read__code-block').waitFor({state:'visible'});
 assert.match(await page.locator('.artifact-read__code-block').textContent(),/--heat/);
 assert.equal(await page.locator('.artifact-stage-activity__record-body').textContent().then(text=>text.includes('api.openai.com')),false);
 await page.locator('.artifact-stage__close').click();

 await page.setViewportSize({width:390,height:844});
 assert.equal(await external.isVisible(),false);
 await page.evaluate(()=>openArtifactStage('stage-architects','Compete architects'));
 const mobileDialog=page.locator('.artifact-stage');
 await mobileDialog.waitFor({state:'visible'});
 await page.waitForTimeout(400);
 const geometry=await page.evaluate(()=>{const panel=document.querySelector('.artifact-stage__panel').getBoundingClientRect();const overview=document.querySelector('.artifact-stage-activity__overview').getBoundingClientRect();const record=document.querySelector('.artifact-stage-activity__record > summary').getBoundingClientRect();return {panel:panel.toJSON(),overview:overview.toJSON(),record:record.toJSON(),scrollWidth:document.documentElement.scrollWidth};});
 assert.ok(geometry.panel.width<=390&&geometry.overview.right<=390&&geometry.record.right<=390,JSON.stringify(geometry));
 assert.ok(geometry.record.height>=52,JSON.stringify(geometry));
 assert.ok(geometry.scrollWidth<=390,JSON.stringify(geometry));
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PACKAGING_STAGE_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered packaging stage drawer harness: %v\n%s", err, output)
	}
}
