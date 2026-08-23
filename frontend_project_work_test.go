package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectBoundResearchActivityStaysAvailableOutsideTimelineJourney(t *testing.T) {
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
 const browser=await chromium.launch({headless:true,executablePath:process.env.PROJECT_WORK_CHROME||undefined});
 const page=await browser.newPage({viewport:{width:1440,height:900}});
 await page.goto(base+'/chat',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(({thread,artifact,work})=>{artifactEntries=[artifact];scoutChatThreads=[thread];selectScoutChatThread(thread.id);setMobileChatView('convo');const card=scoutDesktopGoalWorkCardNode(work,artifact);card.id='project-work-activity-fixture';document.getElementById('chatTool').appendChild(card);}, {thread,artifact,work});
 assert.equal(await page.locator('#scoutChatThread .scout-chat-work-card').count(),0,'generic completed Work leaked into the conversation');
 const card=page.locator('#project-work-activity-fixture');
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
 await correctionDialog.getByRole('button',{name:'Cancel',exact:true}).click();
 await regenerate.click();
 await page.waitForFunction(()=>!document.getElementById('scoutFollowUpTarget').hidden&&document.getElementById('scoutChatInput').value.includes('Research the durable creator-economy evidence'));
 assert.match(await page.locator('#scoutFollowUpTarget').textContent(),/follow-up/);
 assert.equal(await page.evaluate(()=>document.activeElement?.id),'scoutChatInput');
 assert.equal(saveBodies.length,0);assert.equal(correctionBodies.length,0);assert.equal(filesLoads,0);
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PROJECT_WORK_INDEX="+indexPath, "PROJECT_WORK_RENDER_DIR="+os.Getenv("PROJECT_WORK_RENDER_DIR"), "PROJECT_WORK_CHROME=/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
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
