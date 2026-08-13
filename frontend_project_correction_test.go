package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSentMessageProjectCorrectionKeepsAuthorityAndConfirmationServerOwned(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`id="sentMessageProjectDialog"`,
		`Choose where this sent message belongs. Nothing changes until you confirm.`,
		`fetch(sentMessageProjectPath(threadId, messageId), { method: 'GET', cache: 'no-store' })`,
		`body: JSON.stringify({ operationId: attempt.operationId, correctionToken: state.selected.token })`,
		`sentMessageProjectAttempt = sentMessageProjectAttempt?.key === attemptKey`,
		`response.status === 409 && payload?.projectCorrection`,
		`The Project changed elsewhere. Review and confirm again.`,
		`Project access changed. Open the message again to review it.`,
		`Change project…`,
		`Project: ${projectTitle}. Change project for this message`,
		`if (current?.project && incoming?.project && currentRevision > incomingRevision) return true`,
		`if (nextThreadId !== activeScoutThreadId) closeSentMessageProjectCorrection({ restoreFocus: false })`,
		`closeSentMessageProjectCorrection({ restoreFocus: false })`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("sent-message Project correction missing %q", want)
		}
	}
	mutationStart := strings.Index(html, "async function submitSentMessageProjectCorrection")
	mutationEnd := strings.Index(html[mutationStart:], "function selectedScoutChatThread")
	if mutationStart < 0 || mutationEnd < 0 {
		t.Fatal("sent-message Project correction mutation boundary missing")
	}
	previewStart := strings.Index(html, "async function openSentMessageProjectCorrection")
	if previewStart < 0 || previewStart >= mutationStart {
		t.Fatal("read-only correction preview is not separate from confirmation")
	}
	preview := html[previewStart:mutationStart]
	for _, forbidden := range []string{"method: 'PATCH'", "projectId:", "associationId:", "authority:", "toolTemplate", "provider:"} {
		if strings.Contains(preview, forbidden) {
			t.Fatalf("read-only Project correction preview contains forbidden authority or mutation field %q", forbidden)
		}
	}
	mutation := html[mutationStart : mutationStart+mutationEnd]
	for _, forbidden := range []string{"projectId:", "associationId:", "expectedRevision:", "model:", "toolTemplate", "authority:"} {
		if strings.Contains(mutation, forbidden) {
			t.Fatalf("Project correction client selects forbidden authority field %q", forbidden)
		}
	}
}

func TestSentMessageProjectCorrectionRenderedZeroEffectRetryAndFocus(t *testing.T) {
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
const html=fs.readFileSync(process.env.PROJECT_CORRECTION_INDEX,'utf8');
const dictation=fs.readFileSync(path.join(path.dirname(process.env.PROJECT_CORRECTION_INDEX),'public','composer-dictation.js'),'utf8');
let getCount=0;
let patchBodies=[];
let conflictNextPatch=true;
let failNextPatch=true;
const correction={available:true,scopeKey:'scope-one',current:{title:'Launch Plan',status:'confirmed',contextRevision:4},choices:[{title:'Roadmap',token:'opaque-roadmap'}],remove:{title:'No project',token:'opaque-remove'}};
const initialMessage={id:'message-one',role:'user',authorEmail:'synthetic@example.test',authorName:'AJ',text:'Lock the launch sequence.',createdAt:'2026-08-12T18:00:00Z',project:{status:'confirmed',title:'Launch Plan',basis:'selected',contextRevision:4}};
const updatedMessage={...initialMessage,project:{status:'confirmed',title:'Roadmap',basis:'corrected',contextRevision:5}};
const initialThread={id:'thread-one',title:'Launch thread',visibility:'private',ownerEmail:'synthetic@example.test',updatedAt:'2026-08-12T18:00:00Z',messagesLoaded:true,messages:[initialMessage]};
const updatedThread={...initialThread,updatedAt:'2026-08-12T18:00:01Z',messages:[updatedMessage]};
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation);}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/assistant/chat-threads/thread-one/messages/message-one/project'&&req.method==='GET'){
    getCount+=1;res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,projectCorrection:correction}));
  }
  if(req.url==='/assistant/chat-threads/thread-one/messages/message-one/project'&&req.method==='PATCH'){
    let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{
      patchBodies.push(JSON.parse(raw));
      if(conflictNextPatch){conflictNextPatch=false;res.writeHead(409,{'content-type':'application/json'});return res.end(JSON.stringify({error:'The Project changed elsewhere. Review and confirm again.',projectCorrection:correction}));}
      if(failNextPatch){failNextPatch=false;res.writeHead(503,{'content-type':'application/json'});return res.end(JSON.stringify({error:'Project service is briefly unavailable.'}));}
      res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,thread:updatedThread,message:updatedMessage}));
    });return;
  }
  if(req.url==='/assistant/chat-threads?view=index'&&req.method==='GET'){
    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,threads:[{...initialThread,messages:undefined,messagesLoaded:false}]}));
  }
  if(req.url==='/assistant/chat-threads/thread-one'&&req.method==='GET'){
    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,thread:initialThread}));
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
 await page.waitForTimeout(300);
 await page.evaluate(thread=>{scoutChatThreads=[thread];selectScoutChatThread(thread.id);setMobileChatView('convo');},initialThread);
 const projectChip=page.getByRole('button',{name:'Project: Launch Plan. Change project for this message'});
 await projectChip.waitFor();
 assert.ok((await projectChip.boundingBox()).height>=40,'Project correction chip has a sub-40px hit target');
 await projectChip.click();
 await page.getByRole('radio',{name:'Roadmap'}).waitFor();
 assert.equal(getCount,1);assert.equal(patchBodies.length,0,'opening the dialog mutated the message');
 await page.getByRole('radio',{name:'Roadmap'}).click();
 assert.equal(patchBodies.length,0,'radio selection mutated before confirmation');
 assert.equal(await page.locator('#sentMessageProjectDialog').getAttribute('open'),'','radio selection closed the review dialog');
 assert.equal(await page.locator('#sentMessageProjectLoading').isVisible(),false,'resolved loading state still occupies the dialog');
 const renderDir=String(process.env.PROJECT_CORRECTION_RENDER_DIR||'').trim();if(renderDir)fs.mkdirSync(renderDir,{recursive:true});
 const capture=async(name,width,height,theme)=>{
   await page.setViewportSize({width,height});await page.evaluate(next=>renderTheme(next),theme);await page.waitForTimeout(80);
   const geometry=await page.locator('#sentMessageProjectDialog').evaluate(node=>{const box=node.getBoundingClientRect();const style=getComputedStyle(node);return{open:node.open,display:style.display,visibility:style.visibility,parentDisplay:getComputedStyle(node.parentElement).display,left:box.left,right:box.right,top:box.top,bottom:box.bottom,width:box.width,height:box.height,viewportWidth:innerWidth,viewportHeight:innerHeight,scrollWidth:document.documentElement.scrollWidth}});
   assert.equal(geometry.scrollWidth<=geometry.viewportWidth,true,JSON.stringify(geometry));
   assert.ok(geometry.width>=300&&geometry.height>=280,JSON.stringify(geometry));
   assert.ok(geometry.left>=0&&geometry.right<=geometry.viewportWidth&&geometry.top>=0&&geometry.bottom<=geometry.viewportHeight,JSON.stringify(geometry));
   if(renderDir)await page.screenshot({path:path.join(renderDir,name+'-'+theme+'.png')});
 };
 for(const theme of ['dark','light'])await capture('desktop-sent-project-correction',1440,900,theme);
 for(const theme of ['dark','light'])await capture('phone-sent-project-correction',390,844,theme);
 await page.setViewportSize({width:1440,height:900});
 await page.getByRole('button',{name:'Cancel',exact:true}).click();
 await page.waitForFunction(()=>!document.getElementById('sentMessageProjectDialog').open);
 await page.waitForFunction(()=>document.activeElement?.getAttribute('data-project-correction')==='true');
 assert.equal(patchBodies.length,0);assert.equal(await page.evaluate(()=>document.activeElement?.getAttribute('data-project-correction')),'true','Cancel did not restore exact chip focus');
 await page.locator('.scout-chat-msg[data-message-id="message-one"]').hover();
 await page.getByRole('button',{name:'More message actions'}).click();
 await page.getByRole('menuitem',{name:'Change project…'}).click();
 await page.getByRole('radio',{name:'Roadmap'}).waitFor();
 await page.keyboard.press('Escape');
 await page.waitForFunction(()=>!document.getElementById('sentMessageProjectDialog').open&&document.activeElement?.getAttribute('aria-label')==='More message actions');
 assert.equal(patchBodies.length,0,'overflow open or Escape mutated the message');
 await projectChip.click();await page.getByRole('radio',{name:'Roadmap'}).waitFor();
 await page.evaluate(()=>sentMessageProjectDialog.dispatchEvent(new MouseEvent('click',{bubbles:true})));
 await page.waitForFunction(()=>!document.getElementById('sentMessageProjectDialog').open&&document.activeElement?.getAttribute('data-project-correction')==='true');
 assert.equal(patchBodies.length,0,'backdrop close mutated the message');
 await projectChip.click();await page.getByRole('radio',{name:'Roadmap'}).waitFor();
 await page.getByRole('radio',{name:'Roadmap'}).click();
 await page.getByRole('button',{name:'Update project'}).click();
 await page.waitForFunction(()=>document.getElementById('sentMessageProjectStatus').textContent.includes('changed elsewhere'));
 assert.equal(await page.getByRole('button',{name:'Update project'}).isDisabled(),true,'stale choice remained confirmable');
 await page.getByRole('radio',{name:'Roadmap'}).click();
 await page.getByRole('button',{name:'Update project'}).click();
 await page.waitForFunction(()=>document.getElementById('sentMessageProjectStatus').textContent.includes('briefly unavailable'));
 assert.match(await page.getByRole('alert').textContent(),/briefly unavailable/);
 assert.equal(await page.locator('#sentMessageProjectDialog').getAttribute('open'),'');
 await page.getByRole('button',{name:'Update project'}).click();
 await page.waitForFunction(()=>!document.getElementById('sentMessageProjectDialog').open);
 assert.equal(patchBodies.length,3);assert.notEqual(patchBodies[0].operationId,patchBodies[1].operationId,'stale correction operation leaked into the reviewed retry');assert.equal(patchBodies[1].operationId,patchBodies[2].operationId,'exact retry minted a different operationId');
 assert.deepEqual(Object.keys(patchBodies[1]).sort(),['correctionToken','operationId']);
 assert.equal(await page.getByRole('button',{name:'Project: Roadmap. Change project for this message'}).textContent(),'Project · Roadmap');
 await page.waitForFunction(()=>document.activeElement?.getAttribute('data-project-correction')==='true');
 assert.equal(await page.evaluate(()=>document.activeElement?.getAttribute('data-project-correction')),'true','success did not restore the replacement chip focus');
 const ordered=await page.evaluate(message=>{
   handleChatThreadEvent({id:'thread-one',message:{...message,project:{status:'confirmed',title:'Old Project',basis:'selected',contextRevision:4}}});
   handleChatThreadEvent({id:'thread-one',message:{...message,project:{status:'pending',title:'Roadmap',basis:'selected',contextRevision:5}}});
   return scoutChatThreads[0].messages[0].project;
 },updatedMessage);
 assert.deepEqual({title:ordered.title,status:ordered.status,contextRevision:ordered.contextRevision},{title:'Roadmap',status:'confirmed',contextRevision:5});
 await page.getByRole('button',{name:'Project: Roadmap. Change project for this message'}).click();
 await page.getByRole('radio',{name:'Roadmap'}).waitFor();
 await page.keyboard.press('Escape');
 await page.waitForFunction(()=>!document.getElementById('sentMessageProjectDialog').open);
 assert.equal(patchBodies.length,3,'Escape mutated the message');
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PROJECT_CORRECTION_INDEX="+indexPath, "PROJECT_CORRECTION_RENDER_DIR="+os.Getenv("PROJECT_CORRECTION_RENDER_DIR"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered sent-message Project correction harness: %v\n%s", err, output)
	}
}
