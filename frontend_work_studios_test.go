package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkStudiosOwnDurablePresentationAndResearchProjects(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, marker := range []string{
		`data-pd1-destination="Work" aria-label="Work"`,
		`'/presentations': { destination: 'Work', output: 'presentation' }`,
		`'/research': { destination: 'Work', output: 'document' }`,
		`id="studioProjectList"`,
		`id="studioProjectDetail"`,
		`const params = new URLSearchParams({ limit: '200' })`,
		`function scoutStudioReceiptNode(message)`,
		`scout-studio-receipt__progress`,
		`View in Work`,
		`selectPD1Destination('Work', { projectId: selectedStudioProjectId })`,
		`if (message?.studioProject?.id) return scoutStudioReceiptIsLatest(message)`,
		`openDeckStudio(result.artifactId`,
		`openDocumentStudio(result.artifactId`,
		`openDeckPresentation(result.artifactId`,
		`fetch('/artifacts/export-pptx'`,
		`fetch('/artifacts/export-pdf'`,
		`function studioProjectDecisionNode(project, checkpoint)`,
		`function studioProjectResultIsFinal(project)`,
		`submitCheckpointOption(project.rootArtifactId, checkpoint.id, option.id, checkpointNote)`,
		`expectedBinding`,
		`artifactEntryCapabilityDigest(refreshedArtifact)`,
		`metadata?.capabilityDigest || entry?.metadata?.contentDigest`,
		`artifactPdfMatchesExpectedRenderSuccessor`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("Studio workspace contract missing %q", marker)
		}
	}
	if !strings.Contains(html, `.scout-studio-receipt`) || !strings.Contains(html, `scout-studio-receipt__status`) {
		t.Fatal("chat does not have the quiet Studio receipt surface")
	}
	if !strings.Contains(html, `['Needs you', project => project.status === 'needs_input']`) || !strings.Contains(html, `['Needs attention', project => project.status === 'needs_attention']`) || !strings.Contains(html, `['In progress', project =>`) || !strings.Contains(html, `['Recent', project =>`) {
		t.Fatal("Studio work does not separate decisions, recovery, active work, and recent files")
	}
	for _, marker := range []string{
		`.agent-tool__inner.studio-projects`,
		`grid-template-rows: auto minmax(0, 1fr)`,
		`.studio-projects__library {`,
		`overscroll-behavior: contain`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("Studio bounded-pane contract missing %q", marker)
		}
	}
	if strings.Contains(html, `id="workToolMenu"`) || strings.Contains(html, `.work-tool-menu`) {
		t.Fatal("the Studio rail regressed into the retired Work flyout")
	}
}

func TestWorkStudiosRenderedFilterDetailAndQuietReceipt(t *testing.T) {
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
const path=require('path');
const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.STUDIO_INDEX,'utf8');
const digest='d'.repeat(64);
const sceneRef='e'.repeat(64);
const pdfRef='f'.repeat(64);
const documentDigest='c'.repeat(64);
const documentSuccessorDigest='9'.repeat(64);
const documentPdfRef='8'.repeat(64);
const legacyDisposition={tenantId:'tenant-studio',artifactId:'deck-final',contentRevision:4,contentDigest:'a'.repeat(64),aclVersion:3,audienceDigest:'b'.repeat(64)};
const actions=[];
const driveSaves=[];
const requestLog=[];
const conditionalStudioRequests=[];
const pdfExports=[];
let reviewAttempts=0;
let documentRendered=false;
let transientExactAttempts=0;
const phases=(active)=>['brief','build','polish','ready'].map((id,index)=>({id,label:id[0].toUpperCase()+id.slice(1),status:index<active?'complete':index===active?'active':'upcoming'}));
const projects=[
 {schemaVersion:1,id:'deck-root',kind:'presentation',title:'Western engagement army',revision:1,status:'ready',progressPercent:100,phase:'ready',phases:phases(4).map(item=>({...item,status:'complete'})),createdAt:'2026-08-23T12:00:00Z',updatedAt:'2026-08-23T12:10:00Z',rootRunId:'deck-run',rootArtifactId:'deck-root',href:'/work?project=deck-root',source:{threadId:'private-scout'},result:{artifactId:'deck-final',type:'html_deck',version:4,digest,title:'Western engagement army',qualityState:'admitted',canEdit:true,canPresent:true,canExport:true},canRename:true},
 {schemaVersion:1,id:'doc-running',kind:'document',title:'Market opportunity report',revision:1,status:'running',progressPercent:54,phase:'build',phases:phases(1),createdAt:'2026-08-23T11:00:00Z',updatedAt:'2026-08-23T12:05:00Z',rootRunId:'doc-run',rootArtifactId:'doc-running',href:'/work?project=doc-running',source:{threadId:'private-scout'},canRename:false},
 {schemaVersion:1,id:'doc-needs',kind:'document',title:'Audience choice',revision:1,status:'needs_input',progressPercent:18,phase:'brief',phases:[{id:'brief',label:'Brief',status:'needs_input'},{id:'build',label:'Build',status:'upcoming'},{id:'polish',label:'Polish',status:'upcoming'},{id:'ready',label:'Ready',status:'upcoming'}],createdAt:'2026-08-23T10:00:00Z',updatedAt:'2026-08-23T12:01:00Z',rootRunId:'needs-run',rootArtifactId:'doc-needs',href:'/research?project=doc-needs',source:{threadId:'private-scout'},checkpoint:{id:'goal-checkpoint-aaaaaaaaaaaaaaaaaaaaaaaa',question:'Which audience should anchor the report?',options:[{id:'checkpoint-option-111111111111111111111111',label:'Operators and brand leaders',action:'proceed'},{id:'checkpoint-option-222222222222222222222222',label:'Change the audience',action:'revise'},{id:'checkpoint-option-333333333333333333333333',label:'Hold for now',action:'hold'}]},canRename:false}
 ,{schemaVersion:1,id:'doc-draft',kind:'document',title:'Edited opportunity draft',revision:2,status:'needs_attention',progressPercent:100,phase:'polish',phases:[{id:'brief',label:'Brief',status:'complete'},{id:'build',label:'Build',status:'complete'},{id:'polish',label:'Polish',status:'needs_attention'},{id:'ready',label:'Ready',status:'upcoming'}],createdAt:'2026-08-23T09:00:00Z',updatedAt:'2026-08-23T11:58:00Z',rootRunId:'draft-run',rootArtifactId:'doc-draft',href:'/research?project=doc-draft',source:{threadId:'private-scout'},result:{artifactId:'doc-draft-result',type:'markdown',version:7,digest,title:'Edited opportunity draft',qualityState:'edited_after_admission',canEdit:true,canContinue:true,canPresent:false,canExport:false},canRename:true}
];
const olderProject={schemaVersion:1,id:'older-deck',kind:'presentation',title:'Earlier presentation',revision:1,status:'ready',progressPercent:100,phase:'ready',phases:phases(4).map(item=>({...item,status:'complete'})),createdAt:'2026-08-20T12:00:00Z',updatedAt:'2026-08-20T12:10:00Z',rootRunId:'older-run',rootArtifactId:'older-deck',href:'/presentations?project=older-deck',source:{threadId:'source-older'},result:{artifactId:'older-final',type:'html_deck',version:2,digest,title:'Earlier presentation',qualityState:'',reviewManaged:false,canEdit:false,canContinue:false,canPresent:true,canExport:false},canRename:false};
const receiptOnlyProject={...olderProject,id:'receipt-only-deck',title:'Receipt-only presentation',rootRunId:'receipt-only-run',rootArtifactId:'receipt-only-deck',href:'/work?project=receipt-only-deck'};
const transientExactProject={...olderProject,id:'transient-deck',title:'Recovered exact presentation',rootRunId:'transient-run',rootArtifactId:'transient-deck',href:'/work?project=transient-deck'};
const firstRenderDocument={schemaVersion:1,id:'doc-render-root',kind:'document',title:'First-render report',revision:1,status:'ready',progressPercent:100,phase:'ready',phases:phases(4).map(item=>({...item,status:'complete'})),createdAt:'2026-08-23T12:00:00Z',updatedAt:'2026-08-23T12:10:00Z',rootRunId:'doc-render-run',rootArtifactId:'doc-render-root',href:'/research?project=doc-render-root',source:{threadId:'private-scout'},result:{artifactId:'doc-render',type:'markdown',version:3,digest:documentDigest,title:'First-render report',qualityState:'admitted',canEdit:true,canPresent:false,canExport:true},canRename:true};
const server=http.createServer((req,res)=>{
	requestLog.push(req.url);
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'Synthetic',shellAccess:'full'}));}
 if(req.url==='/api/stride/v1/mobile/surfaces/organizations'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[{id:'membership',title:'Synthetic Lab',status:'current',kind:'organization-summary',detail:{kind:'organization-summary',isCurrent:true,role:'owner'},actions:[]}]}));}
 if(req.url.startsWith('/api/studio-projects/v1')){const parsed=new URL(req.url,'http://local');const id=parsed.searchParams.get('id');if(id){if(id==='older-deck'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,project:olderProject}));}if(id==='receipt-only-deck'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,project:receiptOnlyProject}));}if(id==='transient-deck'){if(++transientExactAttempts===1){res.writeHead(503,{'content-type':'application/json'});return res.end(JSON.stringify({error:'Temporary Work service interruption'}));}res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,project:transientExactProject}));}res.writeHead(404,{'content-type':'application/json'});return res.end(JSON.stringify({error:'studio project not found'}));}if(parsed.searchParams.get('before')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,projects:[olderProject],hasMore:false}));}if(req.headers['if-none-match']==='"studio-v1"'){conditionalStudioRequests.push(req.url);res.writeHead(304);return res.end();}res.writeHead(200,{'content-type':'application/json','etag':'"studio-v1"'});return res.end(JSON.stringify({ok:true,projects,hasMore:true,nextBefore:'page-one'}));}
 if(req.url.startsWith('/assistant/chat-threads/source-older?')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({thread:{id:'source-older',title:'Earlier private source',visibility:'private',updatedAt:'2026-08-20T12:11:00Z',messages:[]},history:{mode:'tail',hasEarlier:false,messageCount:0}}));}
 if(req.url==='/artifacts/deck?id=deck-final'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({artifact:{id:'deck-final',title:'Western engagement army',type:'html_deck',version:4,contentDigest:digest,sceneRef},deck:{schemaVersion:1,width:1920,height:1080,slides:[]},canWrite:true}));}
 if(req.url==='/artifacts?id=deck-final'){const artifact={id:'deck-final',kind:'os_artifact',text:'<!doctype html><title>Deck</title>',metadata:{title:'Western engagement army',type:'html_deck',artifactVersion:'4',contentDigest:'0'.repeat(64),capabilityDigest:digest,deckSceneRef:sceneRef,assets:JSON.stringify([{ref:pdfRef,kind:'pdf',mime:'application/pdf',name:'Western engagement army.pdf'}]),renderPdfArtifactVersion:'4',renderPdfSourceSceneRef:sceneRef,renderPdfAssetRef:pdfRef}};res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifacts:[artifact],dispositionRef:legacyDisposition}));}
 if(req.url==='/artifacts/document?id=doc-render'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({artifact:{id:'doc-render',title:'First-render report',type:'markdown',version:3,contentDigest:documentDigest},document:{schemaVersion:1,markdown:'# First-render report\n\nEvidence-backed copy.'},canWrite:true}));}
 if(req.url==='/artifacts?id=doc-render'){const metadata=documentRendered?{title:'First-render report',type:'markdown',artifactVersion:'4',contentDigest:documentSuccessorDigest,capabilityDigest:documentSuccessorDigest,renderStatus:'complete',renderJobId:'',renderSourceArtifactVersion:'3',renderPdfSourceArtifactVersion:'3',renderPdfArtifactVersion:'4',renderSourceSceneRef:'',renderPdfSourceSceneRef:'',renderPdfAssetRef:documentPdfRef,assets:JSON.stringify([{ref:documentPdfRef,kind:'pdf',mime:'application/pdf',name:'First-render report.pdf',sourceArtifactVersion:3}])}:{title:'First-render report',type:'markdown',artifactVersion:'3',contentDigest:documentDigest,capabilityDigest:documentDigest};res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifacts:[{id:'doc-render',kind:'os_artifact',text:'# First-render report\n\nEvidence-backed copy.',metadata}]}));}
 if(req.url==='/artifacts/export-pptx'&&req.method==='POST'){res.writeHead(200,{'content-type':'application/vnd.openxmlformats-officedocument.presentationml.presentation'});return res.end('pptx');}
 if(req.url==='/artifacts/export-pdf'&&req.method==='POST'){let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{const request=JSON.parse(body);pdfExports.push(request);if(request.artifactId!=='doc-render'||request.expectedVersion!==3||request.sceneRef!==''){res.writeHead(409,{'content-type':'application/json'});return res.end(JSON.stringify({error:'wrong source tuple'}));}documentRendered=true;res.writeHead(202,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,jobId:'render-job',kind:'paper',sourceVersion:3,sceneRef:'',renderStatus:'queued',reused:false}));});return;}
 if(req.url.startsWith('/artifacts/blob?')){res.writeHead(200,{'content-type':'application/pdf'});return res.end('%PDF-1.4\n%%EOF');}
 if(req.url==='/api/artifact-drive-saves/v1'&&req.method==='POST'){let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{const save=JSON.parse(body);driveSaves.push(save);const drive={id:'deck-final',sourceArtifactId:'deck-final',name:save.fileName,folderId:save.folderId,artifact:save.artifact};res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,receipt:{operationId:save.operationId,action:'save',outcome:'saved',artifact:save.artifact,drive}}));});return;}
 if(req.url==='/artifacts/action'&&req.method==='POST'){let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{const action=JSON.parse(body);actions.push(action);if(action.action==='review_changes'&&++reviewAttempts===1){res.writeHead(409,{'content-type':'application/json'});return res.end(JSON.stringify({error:'This exact draft changed. Refresh and try again.'}));}res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true}));});return;}
 if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(404,{'content-type':'application/json'});return res.end('{}');}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.STUDIO_CHROME});
 const page=await browser.newPage({viewport:{width:1440,height:900}});
 const pageErrors=[];page.on('pageerror',error=>pageErrors.push(error.message));
 await page.goto(base+'/presentations',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed[data-pd1-destination="Work"]');
 await page.waitForSelector('#studioProjectList .studio-project-row');
 assert.equal(await page.locator('#studioProjectsTitle').innerText(),'Presentations');
 assert.equal(await page.locator('#studioProjectList .studio-project-row').count(),1);
 assert.equal(await page.locator('#studioProjectDetail .studio-project-detail__title').innerText(),'Western engagement army');
 assert.deepEqual(await page.locator('#studioProjectDetail .studio-project-detail__actions button').allInnerTexts(),['Present','Edit','PowerPoint','PDF']);
 const pptxToast=await page.evaluate(async project=>{await downloadStudioProjectResult(project,'pptx',null);return document.querySelector('#toastRegion')?.textContent||'';},projects[0]);
 assert.match(pptxToast,/PowerPoint downloaded/);
 const pdfToast=await page.evaluate(async project=>{await downloadStudioProjectResult(project,'pdf',null);return document.querySelector('#toastRegion')?.textContent||'';},projects[0]);
 assert.match(pdfToast,/PDF downloaded/);
 assert.ok(requestLog.includes('/artifacts/export-pptx'),JSON.stringify(requestLog));
 assert.ok(requestLog.filter(url=>url==='/artifacts/deck?id=deck-final').length>=2,JSON.stringify(requestLog));
 assert.ok(requestLog.includes('/artifacts?id=deck-final'),JSON.stringify(requestLog));
 await page.evaluate(project=>{const entry={id:'deck-final',kind:'os_artifact',text:'',metadata:{title:'Western engagement army',type:'html_deck',artifactVersion:'4',source:'scout_thread',status:'complete'}};const button=artifactSaveToFilesControl(entry,{expectedBinding:studioProjectExpectedBinding(project),readyLabel:'Save exact deck'});if(!button)throw new Error('Save control unavailable');button.id='driveSplitAuthorityProof';document.body.appendChild(button);},projects[0]);
 await page.$eval('#driveSplitAuthorityProof', el => el.click()); /* harness-appended body control: a DOM click, since the wide rail's account row can overlap body origin */
 await page.waitForSelector('.drive-save-dialog[open]');
 await page.fill('.drive-save-dialog__input','Western engagement army — filed');
 await page.click('.drive-save-dialog__button[data-primary="true"]');
 await page.waitForFunction(()=>document.querySelector('#driveSplitAuthorityProof')?.dataset.state==='saved');
 assert.equal(driveSaves.length,1,JSON.stringify(driveSaves));
 assert.deepEqual(driveSaves[0].artifact,legacyDisposition);
 const firstRenderToast=await page.evaluate(async project=>{await downloadStudioProjectResult(project,'pdf',null);return document.querySelector('#toastRegion')?.textContent||'';},firstRenderDocument);
 assert.match(firstRenderToast,/PDF downloaded/);
 assert.ok(requestLog.includes('/artifacts/export-pdf'),JSON.stringify(requestLog));
 assert.deepEqual(pdfExports,[{artifactId:'doc-render',expectedVersion:3,sceneRef:''}]);
 assert.ok(requestLog.filter(url=>url==='/artifacts?id=doc-render').length>=2,JSON.stringify(requestLog));
 const successorLaw=await page.evaluate(({digest,pdfRef,sceneRef})=>{const expected={artifactId:'doc-render',version:3,digest};const source={sourceVersion:3,sceneRef};const pdf={ref:pdfRef,sourceArtifactVersion:3,sourceSceneRef:sceneRef};const metadata={artifactVersion:'4',renderStatus:'complete',renderJobId:'',renderSourceArtifactVersion:'3',renderPdfSourceArtifactVersion:'3',renderPdfArtifactVersion:'4',renderPdfAssetRef:pdfRef,deckSceneRef:sceneRef,renderSourceSceneRef:sceneRef,renderPdfSourceSceneRef:sceneRef};const entry={id:'doc-render',metadata};const {sourceSceneRef:discardedScene,...pdfWithoutScene}=pdf;return{safe:artifactPdfMatchesExpectedRenderSuccessor(entry,pdf,expected,source),versionJump:artifactPdfMatchesExpectedRenderSuccessor({id:'doc-render',metadata:{...metadata,artifactVersion:'5',renderPdfArtifactVersion:'5'}},pdf,expected,source),liveJob:artifactPdfMatchesExpectedRenderSuccessor({id:'doc-render',metadata:{...metadata,renderJobId:'still-running'}},pdf,expected,source),wrongSource:artifactPdfMatchesExpectedRenderSuccessor(entry,{...pdf,sourceArtifactVersion:2},expected,source),wrongRef:artifactPdfMatchesExpectedRenderSuccessor(entry,{...pdf,ref:'7'.repeat(64)},expected,source),wrongScene:artifactPdfMatchesExpectedRenderSuccessor({id:'doc-render',metadata:{...metadata,deckSceneRef:'6'.repeat(64)}},pdf,expected,source),wrongAssetScene:artifactPdfMatchesExpectedRenderSuccessor(entry,{...pdf,sourceSceneRef:'6'.repeat(64)},expected,source),missingAssetScene:artifactPdfMatchesExpectedRenderSuccessor(entry,pdfWithoutScene,expected,source)};},{digest:documentDigest,pdfRef:documentPdfRef,sceneRef});
 assert.deepEqual(successorLaw,{safe:true,versionJump:false,liveJob:false,wrongSource:false,wrongRef:false,wrongScene:false,wrongAssetScene:false,missingAssetScene:false});
 assert.deepEqual(await page.locator('.studio-project-phase__label').allInnerTexts(),['Brief','Build','Polish','Ready']);
 const geometry=await page.evaluate(()=>{const workspace=document.querySelector('.studio-projects__workspace').getBoundingClientRect();const detail=document.querySelector('.studio-projects__detail').getBoundingClientRect();return{workspace:workspace.toJSON(),detail:detail.toJSON(),fits:document.documentElement.scrollWidth<=innerWidth};});
 assert.equal(geometry.fits,true,JSON.stringify(geometry));assert.ok(geometry.workspace.width>900&&geometry.detail.width>500,JSON.stringify(geometry));
	const renderDir=String(process.env.STUDIO_RENDER_DIR||'').trim();
	if(renderDir){fs.mkdirSync(renderDir,{recursive:true});await page.screenshot({path:path.join(renderDir,'presentations-ready.png'),fullPage:true});}
	await page.waitForFunction(()=>document.querySelector('.studio-projects__load-more')?.textContent==='Load older');
	assert.equal(await page.locator('.studio-projects__load-more').innerText(),'Load older');
 await page.click('.studio-projects__load-more');
 await page.waitForSelector('.studio-project-row[data-project-id="older-deck"]');
 assert.equal(await page.locator('#studioProjectDetail .studio-project-detail__title').innerText(),'Western engagement army');
	await page.goto(base+'/research',{waitUntil:'domcontentloaded'});
	await page.waitForFunction(()=>location.pathname==='/research'&&document.querySelectorAll('#studioProjectList .studio-project-row').length===3);
 assert.equal(await page.locator('#studioProjectsTitle').innerText(),'Research');
 assert.deepEqual(await page.locator('.studio-projects__group-title').allTextContents(),['Needs you','Needs attention','In progress']);
 const bounded=await page.evaluate(()=>{
   const tool=document.querySelector('#researchTool');
   const workspace=document.querySelector('.studio-projects__workspace');
   const list=document.querySelector('.studio-projects__list');
   const detail=document.querySelector('.studio-projects__detail');
   const group=list.querySelector('.studio-projects__group:last-of-type');
   for(let index=0;index<36;index+=1){const clone=group.querySelector('.studio-project-row').cloneNode(true);clone.dataset.projectId='overflow-'+index;group.appendChild(clone);}
   const workspaceRect=workspace.getBoundingClientRect();
   return{
     toolOverflow:getComputedStyle(tool).overflowY,
     listOverflow:getComputedStyle(list).overflowY,
     detailOverflow:getComputedStyle(detail).overflowY,
     workspaceBottom:workspaceRect.bottom,
     viewportBottom:innerHeight,
     listClient:list.clientHeight,
     listScroll:list.scrollHeight,
     toolClient:tool.clientHeight,
     toolScroll:tool.scrollHeight
   };
 });
 assert.equal(bounded.toolOverflow,'hidden',JSON.stringify(bounded));
 assert.equal(bounded.listOverflow,'auto',JSON.stringify(bounded));
 assert.equal(bounded.detailOverflow,'auto',JSON.stringify(bounded));
 assert.ok(bounded.workspaceBottom<=bounded.viewportBottom+1,JSON.stringify(bounded));
 assert.ok(bounded.listScroll>bounded.listClient,JSON.stringify(bounded));
 assert.ok(bounded.toolScroll<=bounded.toolClient+1,JSON.stringify(bounded));
 await page.click('.studio-project-row[data-project-id="doc-needs"]');
 assert.equal(await page.locator('.studio-project-decision__question').innerText(),'Which audience should anchor the report?');
 assert.deepEqual(await page.locator('.studio-project-decision__choice').allInnerTexts(),['Operators and brand leaders','Change the audience','Hold for now']);
 if(renderDir)await page.screenshot({path:path.join(renderDir,'research-needs-you.png'),fullPage:true});
 await page.click('.studio-project-decision__choice[data-action="proceed"]');
 await page.waitForFunction(()=>document.querySelector('.studio-project-decision__choice')?.textContent!=='Submitting…');
 assert.deepEqual(actions[0],{id:'doc-needs',action:'approve',checkpointId:'goal-checkpoint-aaaaaaaaaaaaaaaaaaaaaaaa',checkpointOptionId:'checkpoint-option-111111111111111111111111'});
 await page.click('.studio-project-row[data-project-id="doc-draft"]');
 assert.match(await page.locator('.studio-project-deliverable__kicker').innerText(),/DRAFT AVAILABLE/);
 assert.deepEqual(await page.locator('.studio-project-detail__actions button').allInnerTexts(),['Review changes','Edit','Open']);
 await page.click('.studio-project-detail__actions button',{position:{x:12,y:12}});
 await page.waitForFunction(()=>document.querySelector('.studio-project-detail__actions button')?.textContent==='Review changes');
 assert.deepEqual(actions[1],{id:'doc-draft',action:'review_changes',resultArtifactId:'doc-draft-result',expectedResultVersion:7,expectedResultDigest:digest});
 assert.match(await page.locator('.toast').last().innerText(),/exact draft changed/i);
 await page.click('.studio-project-detail__actions button',{position:{x:12,y:12}});
 await page.waitForTimeout(120);
 assert.deepEqual(actions[2],{id:'doc-draft',action:'review_changes',resultArtifactId:'doc-draft-result',expectedResultVersion:7,expectedResultDigest:digest});
 const conditionalBefore=conditionalStudioRequests.length;
 const receipt=await page.evaluate(project=>{
   const old={id:'receipt-old',kind:'thread',studioProject:{id:project.id,kind:project.kind,title:project.title,status:'running',href:project.href}};
   const latest={id:'receipt-latest',kind:'manifest',studioProject:{id:project.id,kind:project.kind,title:project.title,status:'ready',progressPercent:100,phase:'ready',href:project.href}};
   scoutChatThreads=[{id:'private-scout',title:'Scout',visibility:'private',messagesLoaded:true,messages:[old,latest]}];activeScoutThreadId='private-scout';
   const oldHost=document.createElement('div');oldHost.appendChild(scoutChatMessageRecordNode(old));
   const newHost=document.createElement('div');newHost.appendChild(scoutChatMessageRecordNode(latest));
   document.body.appendChild(newHost);
   const button=newHost.querySelector('.scout-studio-receipt');button.click();
   return{
     oldChildren:oldHost.childElementCount,
     newChildren:newHost.childElementCount,
     text:button.innerText,
     path:location.pathname,
     projectParam:new URL(location.href).searchParams.get('project'),
     selectedProjectId:selectedStudioProjectId,
     deckCards:newHost.querySelectorAll('.scout-chat-deck-result,.manifest-card,.scout-chat-work-card').length,
   };
 },receiptOnlyProject);
 await page.waitForFunction(()=>document.querySelector('.studio-project-detail__title')?.textContent==='Receipt-only presentation');
 if(renderDir){await page.evaluate(()=>document.querySelectorAll('.toast').forEach(node=>node.remove()));await page.locator('.scout-studio-receipt').last().screenshot({path:path.join(renderDir,'chat-work-receipt.png')});}
 assert.equal(receipt.oldChildren,0,JSON.stringify(receipt));
 assert.equal(receipt.newChildren,1,JSON.stringify(receipt));
 assert.match(receipt.text,/Receipt-only presentation/);assert.match(receipt.text,/Ready/);assert.match(receipt.text,/View in Work/);
 assert.equal(receipt.path,'/work');assert.equal(receipt.deckCards,0);
 assert.equal(receipt.projectParam,'receipt-only-deck',JSON.stringify(receipt));
 assert.equal(receipt.selectedProjectId,'receipt-only-deck',JSON.stringify(receipt));
 assert.equal(await page.locator('.studio-project-detail__title').innerText(),'Receipt-only presentation');
 assert.ok(conditionalStudioRequests.length>conditionalBefore,JSON.stringify({conditionalBefore,conditionalStudioRequests,requestLog}));
 assert.ok(requestLog.includes('/api/studio-projects/v1?id=receipt-only-deck'),JSON.stringify(requestLog));

 const transientFirst=await page.evaluate(async()=>{
   history.replaceState({view:'pd1',destination:'Work'},'','/work?project=transient-deck');
   selectedStudioProjectId='transient-deck';
   await loadStudioProjects({force:true,projectId:'transient-deck'});
   return{unavailable:studioProjectDeepLinkUnavailable,error:studioProjectsError,selected:selectedStudioProjectId};
 });
 assert.equal(transientFirst.unavailable,'',JSON.stringify(transientFirst));
 assert.match(transientFirst.error,/Temporary Work service interruption/);
 assert.equal(transientFirst.selected,'transient-deck');
 await page.evaluate(()=>loadStudioProjects({force:true,projectId:'transient-deck'}));
 await page.waitForFunction(()=>document.querySelector('.studio-project-detail__title')?.textContent==='Recovered exact presentation');
 assert.equal(await page.locator('.studio-project-detail__title').innerText(),'Recovered exact presentation');
 assert.equal(transientExactAttempts,2);

 await page.evaluate(project=>renderStudioProjectDetail({...project,id:'blocked-report',status:'needs_attention',result:undefined,attention:{title:'Quality checks didn’t pass',body:'Scout stopped instead of publishing a result it could not verify.',actionLabel:'Open conversation'}}),projects[1]);
 assert.equal(await page.locator('.studio-project-deliverable__title').innerText(),'Quality checks didn’t pass');
 assert.match(await page.locator('.studio-project-deliverable__meta').innerText(),/stopped instead of publishing/i);
 assert.equal(await page.locator('.studio-project-detail__actions button').innerText(),'Open conversation');
 if(renderDir)await page.locator('.studio-project-detail').screenshot({path:path.join(renderDir,'work-needs-attention.png')});

 await page.goto(base+'/presentations?project=older-deck',{waitUntil:'domcontentloaded'});
 await page.waitForFunction(()=>document.querySelector('.studio-project-detail__title')?.textContent==='Earlier presentation');
 assert.equal(new URL(page.url()).searchParams.get('project'),'older-deck');
 assert.equal(await page.locator('.studio-project-deliverable__kicker').innerText(),'FINAL DELIVERABLE');
 assert.doesNotMatch(await page.locator('.studio-project-deliverable').innerText(),/review needed|review before/i);
 await page.click('.studio-project-context button',{position:{x:10,y:10}});
 await page.waitForFunction(()=>activeScoutThreadId==='source-older'&&scoutChatThreads.some(thread=>thread.id==='source-older'&&thread.messagesLoaded===true));
 assert.equal(new URL(page.url()).pathname,'/conversations');

 await page.goto(base+'/presentations?project=doc-running',{waitUntil:'domcontentloaded'});
 await page.waitForFunction(()=>location.pathname==='/work'&&new URL(location.href).searchParams.get('project')==='doc-running');
 assert.equal(await page.locator('.studio-project-detail__title').innerText(),'Market opportunity report');

 await page.goto(base+'/research?project=missing',{waitUntil:'domcontentloaded'});
 await page.waitForFunction(()=>document.querySelector('.studio-project-empty h3')?.textContent==='This project is not available');
 assert.equal(await page.locator('.studio-project-empty h3').innerText(),'This project is not available');
 assert.equal(new URL(page.url()).searchParams.get('project'),'missing');

 await page.setViewportSize({width:390,height:844});
 await page.goto(base+'/presentations',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('.studio-project-row[data-project-id="deck-root"]');
 assert.equal(await page.locator('.studio-projects__workspace').getAttribute('class'),'studio-projects__workspace is-library-view');
 assert.equal(await page.locator('#studioProjectDetail').isVisible(),false);
 if(renderDir)await page.screenshot({path:path.join(renderDir,'presentations-mobile-library.png'),fullPage:true});
 await page.click('.studio-project-row[data-project-id="deck-root"]');
 await page.waitForFunction(()=>document.querySelector('.studio-project-detail__title')?.textContent==='Western engagement army');
 assert.equal(await page.locator('.studio-project-detail__back').isVisible(),true);
 await page.evaluate(()=>document.querySelector('.studio-project-deliverable')?.scrollIntoView({block:'center'}));
 const compactGeometry=await page.evaluate(()=>{const dock=document.querySelector('.tool-rail').getBoundingClientRect();const intersects=rect=>rect.left<dock.right&&rect.right>dock.left&&rect.top<dock.bottom&&rect.bottom>dock.top;const visibleSelectors=['.studio-project-deliverable','.studio-project-detail__actions','.studio-project-decision','.studio-project-context'];const overlaps=visibleSelectors.flatMap(selector=>Array.from(document.querySelectorAll(selector))).filter(node=>{const rect=node.getBoundingClientRect();return rect.bottom>0&&rect.top<innerHeight&&intersects(rect)}).map(node=>node.className);return{dock:dock.toJSON(),overlaps,scrollWidth:document.documentElement.scrollWidth,innerWidth};});
 assert.deepEqual(compactGeometry.overlaps,[],JSON.stringify(compactGeometry));
 assert.ok(compactGeometry.scrollWidth<=compactGeometry.innerWidth,JSON.stringify(compactGeometry));
 if(renderDir)await page.screenshot({path:path.join(renderDir,'presentations-mobile.png'),fullPage:true});
 await page.click('.studio-project-detail__back');
 await page.waitForFunction(()=>document.querySelector('.studio-projects__workspace')?.classList.contains('is-library-view'));
 assert.equal(new URL(page.url()).searchParams.has('project'),false);
 assert.deepEqual(pageErrors,[]);
 await browser.close();server.close();
})().catch(error=>{console.error(error,{actions,driveSaves,requestLog});server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	nodeModules := "/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules"
	if _, err := os.Stat(filepath.Join(nodeModules, "playwright")); err != nil {
		t.Skip("bundled Playwright unavailable")
	}
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodeModules, "STUDIO_INDEX="+indexPath, "STUDIO_CHROME="+chrome)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Studio workspace contract: %v\n%s", err, output)
	}
}
