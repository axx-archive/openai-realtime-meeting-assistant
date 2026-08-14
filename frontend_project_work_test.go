package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectBoundResearchRenderedOpenDriveAndRegenerateJourney(t *testing.T) {
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
const path=require('path');
const assert=require('assert/strict');
const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.PROJECT_WORK_INDEX,'utf8');
const dictation=fs.readFileSync(path.join(path.dirname(process.env.PROJECT_WORK_INDEX),'public','composer-dictation.js'),'utf8');
const digest='a'.repeat(64);
const artifactId='artifact-project-research';
const dispositionRef={tenantId:'bonfire',artifactId,contentRevision:3,contentDigest:digest,aclVersion:1,audienceDigest:'b'.repeat(64)};
const artifact={id:artifactId,kind:'os_artifact',text:'# Creator evidence brief\n\nEvidence-grounded final report with exact current sources.',createdAt:'2026-08-13T18:00:00Z',metadata:{source:'scout_thread',mode:'research',status:'complete',threadStatus:'complete',goalStatus:'complete',threadVersion:'2',threadId:'run-project-research',threadQuery:'Research the durable creator-economy evidence',title:'Creator evidence brief',agentName:'Scout',progressPercent:'100',projectWorkId:'project-research',projectWorkTitle:'Research Project',originKind:'private_thread',originId:'thread-project-research',requestedBy:'synthetic@example.test',visibility:'private'}};
const work={id:'work-project-research',kind:'thread',role:'scout',text:'Research delivered.',createdAt:'2026-08-13T18:01:00Z',intentOutcome:'start_private_work',thread:{id:'run-project-research',mode:'research',query:'Research the durable creator-economy evidence',status:'complete',artifactId,agentName:'Scout',progressPercent:100,projectId:'project-research',projectTitle:'Research Project'}};
const thread={id:'thread-project-research',title:'Project Research',visibility:'private',ownerEmail:'synthetic@example.test',updatedAt:'2026-08-13T18:01:00Z',messagesLoaded:true,messages:[work]};
let saveBodies=[];
let correctionBodies=[];
let currentProjectId='project-research';
let currentProjectTitle='Research Project';
let filesLoads=0;
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation);}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/assistant/chat-threads?view=index'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,threads:correctionBodies.length?[thread]:[]}));}
  if(req.url==='/assistant/chat-threads/'+thread.id&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,thread}));}
  if(req.url==='/artifacts/workstream?id='+artifactId&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,workstreamCorrection:{available:true,scopeKey:'work-scope-1',current:{title:currentProjectTitle,status:'current',revision:1},choices:[{title:'Strategy Project',token:'opaque-strategy-project'}],remove:{title:'No project',token:'opaque-no-project'}}}));}
  if(req.url==='/artifacts/workstream?id='+artifactId&&req.method==='PATCH'){
    let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{const body=JSON.parse(raw);correctionBodies.push(body);if(body.correctionToken==='opaque-strategy-project'){currentProjectId='project-strategy';currentProjectTitle='Strategy Project';artifact.metadata.projectWorkId=currentProjectId;artifact.metadata.projectWorkTitle=currentProjectTitle;work.thread.projectId=currentProjectId;work.thread.projectTitle=currentProjectTitle;thread.updatedAt='2026-08-13T18:02:00Z';}res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,artifact,workstreamCorrection:{available:true,scopeKey:'work-scope-1',current:{title:currentProjectTitle,status:'current',revision:2},choices:[],remove:{title:'No project',token:'opaque-no-project'}}}));});return;
  }
  if(req.url==='/artifacts?id='+artifactId){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifacts:[artifact],dispositionRef}));}
  if(req.url==='/api/artifact-drive-saves/v1'&&req.method==='POST'){
    let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{const body=JSON.parse(raw);saveBodies.push(body);res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,receipt:{operationId:body.operationId,action:'save',outcome:'saved',artifact:dispositionRef,drive:{id:artifactId,sourceArtifactId:artifactId,name:body.fileName,folderId:body.folderId,artifact:dispositionRef}}}));});return;
  }
  if(req.url==='/assistant/files'){
    filesLoads+=1;res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,files:[{id:artifactId,artifactId,name:'Creator evidence brief',mime:'text/markdown',origin:'deliverable',brainStatus:'ingested',previewable:true,folderId:''}],folders:[]}));
  }
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:900}});
 await page.goto(base+'/chat',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(({thread,artifact})=>{artifactEntries=[artifact];scoutChatThreads=[thread];selectScoutChatThread(thread.id);setMobileChatView('convo');}, {thread,artifact});
 const card=page.locator('.scout-chat-work-card');
 await card.waitFor();
 assert.match(await card.getAttribute('aria-label'),/Project: Research Project/);
 assert.equal(await card.locator('.scout-chat-work-card__project').textContent(),'Project · Research Project');
 await card.getByRole('button',{name:/View Research activity/}).click();
 await page.waitForFunction(()=>!document.getElementById('chatContextRail').hidden);
 assert.equal(await page.locator('#chatContextRail').getByRole('button',{name:'Open',exact:true}).isVisible(),true);
 const save=page.locator('#chatContextRail').getByRole('button',{name:'Save to Drive'});
 const regenerate=page.locator('#chatContextRail').getByRole('button',{name:'Regenerate'});
 const correctProject=page.locator('#chatContextRail').getByRole('button',{name:'Change project for this Work'});
 assert.equal(await save.isVisible(),true);assert.equal(await regenerate.isVisible(),true);
 assert.equal(await correctProject.isVisible(),true);
 const renderDir=String(process.env.PROJECT_WORK_RENDER_DIR||'').trim();
 if(renderDir){fs.mkdirSync(renderDir,{recursive:true});await page.evaluate(()=>renderTheme('dark'));await page.waitForTimeout(120);await page.screenshot({path:path.join(renderDir,'desktop-project-research-actions-dark.png')});}
 await correctProject.click();
 const correctionDialog=page.getByRole('dialog',{name:'Correct Work project'});
 await correctionDialog.waitFor();
 assert.match(await correctionDialog.textContent(),/source conversation stays unchanged/i);
 assert.equal(await page.locator('#scoutChatComposer').getByText('Add project',{exact:true}).count(),0);
 if(renderDir){await page.waitForTimeout(80);await page.screenshot({path:path.join(renderDir,'desktop-work-project-correction-dark.png')});}
 await correctionDialog.getByRole('radio',{name:'Strategy Project'}).click();
 await correctionDialog.getByRole('button',{name:'Update project'}).click();
 await page.waitForFunction(()=>!document.getElementById('sentMessageProjectDialog').open);
 assert.equal(correctionBodies.length,1);
 assert.deepEqual(Object.keys(correctionBodies[0]).sort(),['correctionToken','operationId']);
 assert.equal(correctionBodies[0].correctionToken,'opaque-strategy-project');
 assert.match(String(correctionBodies[0].operationId||''),/^[0-9a-f-]{20,}$/i);
 await page.waitForFunction(()=>document.querySelector('.scout-chat-work-card__project')?.textContent==='Project · Strategy Project');
 await page.evaluate(()=>{chooseDriveSaveOptions=async()=>({fileName:'Creator evidence brief',folderId:''});});
 await save.click();
 await page.waitForFunction(()=>document.querySelector('#chatContextRail .chat-context-action[data-state="saved"]')?.textContent==='Open in Drive');
 assert.equal(saveBodies.length,1);assert.equal(saveBodies[0].artifact.artifactId,artifactId);assert.equal(saveBodies[0].fileName,'Creator evidence brief');
 const openDrive=page.locator('#chatContextRail').getByRole('button',{name:'Open in Drive'});
 await openDrive.click();
 await page.waitForFunction(id=>document.getElementById('appShell').dataset.tool==='files'&&filesSelectedId===id,artifactId);
 assert.ok(filesLoads>=1,'Open in Drive did not refresh the exact Drive projection');
 await page.setViewportSize({width:390,height:844});
 await page.evaluate(({thread,artifact})=>{artifact.metadata.savedToFiles='true';renderTheme('light');setActiveTool('chat');artifactEntries=[artifact];scoutChatThreads=[thread];selectScoutChatThread(thread.id);setMobileChatView('convo');}, {thread,artifact});
 const compactCard=page.locator('.scout-chat-work-card');
 await compactCard.getByRole('button',{name:/View Research activity/}).click();
 assert.equal(await compactCard.getByRole('button',{name:'Open',exact:true}).isVisible(),true);
 assert.equal(await compactCard.getByRole('button',{name:'Open in Drive'}).isVisible(),true);
 assert.equal(await compactCard.getByRole('button',{name:'Regenerate'}).isVisible(),true);
 const compactCorrection=compactCard.getByRole('button',{name:'Change project for this Work'});
 assert.equal(await compactCorrection.isVisible(),true);
 await compactCorrection.click();
 await page.getByRole('dialog',{name:'Correct Work project'}).waitFor();
 if(renderDir){await page.waitForTimeout(80);await page.screenshot({path:path.join(renderDir,'phone-work-project-correction-light.png')});}
	 await page.getByRole('dialog',{name:'Correct Work project'}).getByRole('button',{name:'Cancel',exact:true}).click();
 if(renderDir){await page.waitForTimeout(320);await page.screenshot({path:path.join(renderDir,'phone-project-research-actions-light.png')});}
 await compactCard.getByRole('button',{name:'Regenerate'}).click();
 await page.waitForFunction(()=>!document.getElementById('scoutFollowUpTarget').hidden&&document.getElementById('scoutChatInput').value.includes('Research the durable creator-economy evidence'));
 assert.match(await page.locator('#scoutFollowUpTarget').textContent(),/follow-up/);
 assert.equal(await page.evaluate(()=>document.activeElement?.id),'scoutChatInput');
 if(renderDir){for(const [name,width,height,theme] of [['desktop',1440,900,'dark'],['phone',390,844,'light']]){await page.setViewportSize({width,height});await page.evaluate(next=>renderTheme(next),theme);await page.waitForTimeout(80);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:path.join(renderDir,name+'-project-research-'+theme+'.png')});}}
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PROJECT_WORK_INDEX="+indexPath, "PROJECT_WORK_RENDER_DIR="+os.Getenv("PROJECT_WORK_RENDER_DIR"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Project-bound Research harness: %v\n%s", err, output)
	}
}

func TestProjectBoundResearchClientsExposeExactProjectDriveAndRegenerateActions(t *testing.T) {
	web, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"scout-chat-work-card__project", "Project · ${projectTitle}", "Open in Drive", "armScoutFollowUpTarget"} {
		if !strings.Contains(string(web), marker) {
			t.Fatalf("web Project Research journey missing %q", marker)
		}
	}
	native, err := os.ReadFile("mobile/src/messaging/MessageBubble.tsx")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"Project · {String(workThread.ref.projectTitle).trim()}", "Open in Drive", "Edit prompt and regenerate deliverable"} {
		if !strings.Contains(string(native), marker) {
			t.Fatalf("native Project Research journey missing %q", marker)
		}
	}
}
