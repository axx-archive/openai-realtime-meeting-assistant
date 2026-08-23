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
		`data-pd1-destination="Work" aria-label="Recent work"`,
		`data-pd1-destination="Presentations" aria-label="Presentations"`,
		`data-pd1-destination="Research" aria-label="Research"`,
		`id="contentStudioRailLink"`,
		`Presentations: '/presentations'`,
		`Research: '/research'`,
		`id="studioProjectList"`,
		`id="studioProjectDetail"`,
		`const params = new URLSearchParams({ limit: '200' })`,
		`function scoutStudioReceiptNode(message)`,
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
	if strings.Contains(pd1Slice(t, html, `<span class="pd1-primary-nav__external-wrap pd1-primary-nav__studio-wrap"`, `</span>`), `aria-haspopup="menu"`) {
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
const legacyDisposition={tenantId:'tenant-studio',artifactId:'deck-final',contentRevision:4,contentDigest:'a'.repeat(64),aclVersion:3,audienceDigest:'b'.repeat(64)};
const actions=[];
const driveSaves=[];
const requestLog=[];
let reviewAttempts=0;
const phases=(active)=>['brief','build','polish','ready'].map((id,index)=>({id,label:id[0].toUpperCase()+id.slice(1),status:index<active?'complete':index===active?'active':'upcoming'}));
const projects=[
 {schemaVersion:1,id:'deck-root',kind:'presentation',title:'Western engagement army',revision:1,status:'ready',progressPercent:100,phase:'ready',phases:phases(4).map(item=>({...item,status:'complete'})),createdAt:'2026-08-23T12:00:00Z',updatedAt:'2026-08-23T12:10:00Z',rootRunId:'deck-run',rootArtifactId:'deck-root',href:'/presentations?project=deck-root',source:{threadId:'private-scout'},result:{artifactId:'deck-final',type:'html_deck',version:4,digest,title:'Western engagement army',qualityState:'admitted',canEdit:true,canPresent:true,canExport:true},canRename:true},
 {schemaVersion:1,id:'doc-running',kind:'document',title:'Market opportunity report',revision:1,status:'running',progressPercent:54,phase:'build',phases:phases(1),createdAt:'2026-08-23T11:00:00Z',updatedAt:'2026-08-23T12:05:00Z',rootRunId:'doc-run',rootArtifactId:'doc-running',href:'/research?project=doc-running',source:{threadId:'private-scout'},canRename:false},
 {schemaVersion:1,id:'doc-needs',kind:'document',title:'Audience choice',revision:1,status:'needs_input',progressPercent:18,phase:'brief',phases:[{id:'brief',label:'Brief',status:'needs_input'},{id:'build',label:'Build',status:'upcoming'},{id:'polish',label:'Polish',status:'upcoming'},{id:'ready',label:'Ready',status:'upcoming'}],createdAt:'2026-08-23T10:00:00Z',updatedAt:'2026-08-23T12:01:00Z',rootRunId:'needs-run',rootArtifactId:'doc-needs',href:'/research?project=doc-needs',source:{threadId:'private-scout'},checkpoint:{id:'goal-checkpoint-aaaaaaaaaaaaaaaaaaaaaaaa',question:'Which audience should anchor the report?',options:[{id:'checkpoint-option-111111111111111111111111',label:'Operators and brand leaders',action:'proceed'},{id:'checkpoint-option-222222222222222222222222',label:'Change the audience',action:'revise'},{id:'checkpoint-option-333333333333333333333333',label:'Hold for now',action:'hold'}]},canRename:false}
 ,{schemaVersion:1,id:'doc-draft',kind:'document',title:'Edited opportunity draft',revision:2,status:'needs_attention',progressPercent:100,phase:'polish',phases:[{id:'brief',label:'Brief',status:'complete'},{id:'build',label:'Build',status:'complete'},{id:'polish',label:'Polish',status:'needs_attention'},{id:'ready',label:'Ready',status:'upcoming'}],createdAt:'2026-08-23T09:00:00Z',updatedAt:'2026-08-23T11:58:00Z',rootRunId:'draft-run',rootArtifactId:'doc-draft',href:'/research?project=doc-draft',source:{threadId:'private-scout'},result:{artifactId:'doc-draft-result',type:'markdown',version:7,digest,title:'Edited opportunity draft',qualityState:'edited_after_admission',canEdit:true,canContinue:true,canPresent:false,canExport:false},canRename:true}
];
const olderProject={schemaVersion:1,id:'older-deck',kind:'presentation',title:'Earlier presentation',revision:1,status:'ready',progressPercent:100,phase:'ready',phases:phases(4).map(item=>({...item,status:'complete'})),createdAt:'2026-08-20T12:00:00Z',updatedAt:'2026-08-20T12:10:00Z',rootRunId:'older-run',rootArtifactId:'older-deck',href:'/presentations?project=older-deck',source:{threadId:'source-older'},result:{artifactId:'older-final',type:'html_deck',version:2,digest,title:'Earlier presentation',qualityState:'',reviewManaged:false,canEdit:false,canContinue:false,canPresent:true,canExport:false},canRename:false};
const server=http.createServer((req,res)=>{
	requestLog.push(req.url);
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'Synthetic',shellAccess:'full'}));}
 if(req.url==='/api/stride/v1/mobile/surfaces/organizations'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[{id:'membership',title:'Synthetic Lab',status:'current',kind:'organization-summary',detail:{kind:'organization-summary',isCurrent:true,role:'owner'},actions:[]}]}));}
 if(req.url.startsWith('/api/studio-projects/v1')){const parsed=new URL(req.url,'http://local');const id=parsed.searchParams.get('id');if(id){if(id==='older-deck'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,project:olderProject}));}res.writeHead(404,{'content-type':'application/json'});return res.end(JSON.stringify({error:'studio project not found'}));}if(parsed.searchParams.get('before')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,projects:[olderProject],hasMore:false}));}res.writeHead(200,{'content-type':'application/json','etag':'"studio-v1"'});return res.end(JSON.stringify({ok:true,projects,hasMore:true,nextBefore:'page-one'}));}
 if(req.url.startsWith('/assistant/chat-threads/source-older?')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({thread:{id:'source-older',title:'Earlier private source',visibility:'private',updatedAt:'2026-08-20T12:11:00Z',messages:[]},history:{mode:'tail',hasEarlier:false,messageCount:0}}));}
 if(req.url==='/artifacts/deck?id=deck-final'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({artifact:{id:'deck-final',title:'Western engagement army',type:'html_deck',version:4,contentDigest:digest,sceneRef},deck:{schemaVersion:1,width:1920,height:1080,slides:[]},canWrite:true}));}
 if(req.url==='/artifacts?id=deck-final'){const artifact={id:'deck-final',kind:'os_artifact',text:'<!doctype html><title>Deck</title>',metadata:{title:'Western engagement army',type:'html_deck',artifactVersion:'4',contentDigest:'0'.repeat(64),capabilityDigest:digest,deckSceneRef:sceneRef,assets:JSON.stringify([{ref:pdfRef,kind:'pdf',mime:'application/pdf',name:'Western engagement army.pdf'}]),renderPdfArtifactVersion:'4',renderPdfSourceSceneRef:sceneRef,renderPdfAssetRef:pdfRef}};res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifacts:[artifact],dispositionRef:legacyDisposition}));}
 if(req.url==='/artifacts/export-pptx'&&req.method==='POST'){res.writeHead(200,{'content-type':'application/vnd.openxmlformats-officedocument.presentationml.presentation'});return res.end('pptx');}
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
 await page.waitForSelector('#appShell.is-authed[data-pd1-destination="Presentations"]');
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
 await page.click('#driveSplitAuthorityProof');
 await page.waitForSelector('.drive-save-dialog[open]');
 await page.fill('.drive-save-dialog__input','Western engagement army — filed');
 await page.click('.drive-save-dialog__button[data-primary="true"]');
 await page.waitForFunction(()=>document.querySelector('#driveSplitAuthorityProof')?.dataset.state==='saved');
 assert.equal(driveSaves.length,1,JSON.stringify(driveSaves));
 assert.deepEqual(driveSaves[0].artifact,legacyDisposition);
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
 await page.click('#pd1PrimaryNav [data-pd1-destination="Research"]');
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
 await page.waitForFunction(()=>document.querySelector('.studio-project-decision__choice')?.textContent!=='Saving…');
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
 const receipt=await page.evaluate(project=>{
   const old={id:'receipt-old',kind:'thread',studioProject:{id:project.id,kind:project.kind,title:project.title,status:'running',href:project.href}};
   const latest={id:'receipt-latest',kind:'manifest',studioProject:{id:project.id,kind:project.kind,title:project.title,status:'ready',href:project.href}};
   scoutChatThreads=[{id:'private-scout',title:'Scout',visibility:'private',messagesLoaded:true,messages:[old,latest]}];activeScoutThreadId='private-scout';
   const oldHost=document.createElement('div');oldHost.appendChild(scoutChatMessageRecordNode(old));
   const newHost=document.createElement('div');newHost.appendChild(scoutChatMessageRecordNode(latest));
   document.body.appendChild(newHost);
   const button=newHost.querySelector('.scout-studio-receipt');button.click();
   return{oldChildren:oldHost.childElementCount,newChildren:newHost.childElementCount,text:button.innerText,path:location.pathname,deckCards:newHost.querySelectorAll('.scout-chat-deck-result,.manifest-card,.scout-chat-work-card').length};
 },projects[0]);
 assert.equal(receipt.oldChildren,0,JSON.stringify(receipt));
 assert.equal(receipt.newChildren,1,JSON.stringify(receipt));
 assert.match(receipt.text,/Western engagement army/);assert.match(receipt.text,/Ready/);
 assert.equal(receipt.path,'/presentations');assert.equal(receipt.deckCards,0);

 await page.goto(base+'/presentations?project=older-deck',{waitUntil:'domcontentloaded'});
 await page.waitForFunction(()=>document.querySelector('.studio-project-detail__title')?.textContent==='Earlier presentation');
 assert.equal(new URL(page.url()).searchParams.get('project'),'older-deck');
 assert.equal(await page.locator('.studio-project-deliverable__kicker').innerText(),'FINAL DELIVERABLE');
 assert.doesNotMatch(await page.locator('.studio-project-deliverable').innerText(),/review needed|review before/i);
 await page.click('.studio-project-context button',{position:{x:10,y:10}});
 await page.waitForFunction(()=>activeScoutThreadId==='source-older'&&scoutChatThreads.some(thread=>thread.id==='source-older'&&thread.messagesLoaded===true));
 assert.equal(new URL(page.url()).pathname,'/chat');

 await page.goto(base+'/presentations?project=doc-running',{waitUntil:'domcontentloaded'});
 await page.waitForFunction(()=>location.pathname==='/research'&&new URL(location.href).searchParams.get('project')==='doc-running');
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
