package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentCallPipSourceContract(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"function pipMeetingActive()",
		"roomMediaActive()",
		"awayFromRoom || callPipObscuringSurfaceOpen()",
		"document.getElementById('contentStudioDrawer')",
		"function callPipConnectionPresentation(participantCount)",
		"reconnecting · your call is still active",
		"function callPipApplySafePlacement(placement = null)",
		"function callPipContentStudioWorkspaceCandidate(stored, width, height, margin)",
		"stride.call-pip.placement.compact.v1",
		"stride.call-pip.placement.wide.v1",
		"function callPipHandleKeyboardMove(event)",
		"function callPipTrapCompositeTab(event)",
		"root.classList?.contains('content-studio-drawer')",
		"function callPipSyncOpenSurfaceContracts()",
		"surface.setAttribute('aria-modal', composite ? 'false' : 'true')",
		"selectors.push('.artifact-stage__panel', '.content-studio-drawer__panel')",
		`.pip-meeting[data-composite="true"]`,
		"z-index: 10000",
		`id="pipEnd"`,
		"pipEnd.addEventListener('click', () => leaveButton.click())",
		"pipReturn?.addEventListener('click', () => selectPD1Destination('Video'))",
		"pipExpand.addEventListener('click', () => selectPD1Destination('Video'))",
		"callPipSurfaceObserver.observe(document.body, { childList: true })",
		"clearVideoElementStream(video)",
		"callPipScheduleSafePlacement()",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("persistent call PiP contract missing %q", want)
		}
	}
	if strings.Contains(html, "function pipMeetingActive() {\n        return false") {
		t.Fatal("persistent call PiP is still hard-disabled")
	}
	if strings.Contains(html, ".pip-control {\n        transition: all") {
		t.Fatal("PiP controls must transition only explicit properties")
	}
}

func TestPersistentCallPipBrowserLifecycle(t *testing.T) {
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
const html=fs.readFileSync(process.env.CALL_PIP_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/document?id=pip-stage'&&req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,artifact:{id:'pip-stage',title:'Call notes',type:'markdown',version:1,savedToFiles:true},document:{schemaVersion:1,markdown:'# Active call\n\nThe document stays open beside the call.'},canWrite:true}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:820}});
	 let kinoRouteMode='open';
	 let heldKinoRoute=null;
	 await page.route('https://kino.grok.me/**',route=>{
	   if(kinoRouteMode==='hold'){heldKinoRoute=route;return;}
	   return route.fulfill({status:200,contentType:'text/html',body:'<!doctype html><title>KINO</title><button>Inside KINO</button>'});
	 });
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(()=>{
   localStream=new MediaStream();
   currentParticipantName='AJ';
   participantsInRoom=['AJ','Tyler'];
   activeSpeakerName='Tyler';
   ws={readyState:WebSocket.OPEN,send(){},close(){this.readyState=WebSocket.CLOSED}};
   pc=new RTCPeerConnection();
   leaveButton.disabled=false;
   muteMicButton.disabled=false;
   toggleCameraButton.disabled=false;
   window.__pipControlHits={mute:0,camera:0,return:0};
   muteMicButton.click=()=>{window.__pipControlHits.mute+=1};
   toggleCameraButton.click=()=>{window.__pipControlHits.camera+=1};
   pipReturn.addEventListener('click',()=>{window.__pipControlHits.return+=1});
   appShell.classList.add('is-in-room');
   setConnectionState('listening','room is listening');
   selectPD1Destination('Chat');
   renderPipMeeting();
 });
 const pip=page.locator('#pipMeeting');
 await pip.waitFor({state:'visible'});
 await page.waitForTimeout(500);
 assert.match(await page.locator('#pipStatus').textContent(),/live · 2 people/);
 assert.equal(await page.locator('#pipEnd').getAttribute('aria-label'),'End call');
 const muteBox=await page.locator('#pipMute').boundingBox();
 const cameraBox=await page.locator('#pipCamera').boundingBox();
 assert.ok(muteBox.width>=40,JSON.stringify(muteBox));
 assert.ok(cameraBox.height>=40,JSON.stringify(cameraBox));

 const assertHitTarget=async selector=>{
   const result=await page.locator(selector).evaluate(node=>{
     const rect=node.getBoundingClientRect();
     const hit=document.elementFromPoint(rect.left+rect.width/2,rect.top+rect.height/2);
     return {hit:Boolean(hit&&(hit===node||node.contains(hit))),tag:hit?.tagName,id:hit?.id,rect:rect.toJSON()};
   });
   assert.equal(result.hit,true,selector+' is not hit-testable: '+JSON.stringify(result));
 };
 const assertComposite=async(surfaceSelector,panelSelector,{desktopAvoidsPanel=true}={})=>{
   const contract=await page.evaluate(({surfaceSelector,panelSelector})=>{
     const pip=document.getElementById('pipMeeting');
     const surface=document.querySelector(surfaceSelector);
     const panel=document.querySelector(panelSelector);
     const pipRect=pip.getBoundingClientRect();
     const panelRect=panel.getBoundingClientRect();
     return {
       pipZ:Number.parseInt(getComputedStyle(pip).zIndex,10),
       surfaceZ:Number.parseInt(getComputedStyle(surface).zIndex,10),
       ariaModal:surface.getAttribute('aria-modal'),
       composite:surface.dataset.callPipComposite,
       appInert:document.getElementById('appShell').hasAttribute('inert'),
       pip:pipRect.toJSON(),panel:panelRect.toJSON()
     };
   },{surfaceSelector,panelSelector});
   assert.ok(contract.pipZ>contract.surfaceZ,JSON.stringify(contract));
   assert.equal(contract.ariaModal,'false',JSON.stringify(contract));
   assert.equal(contract.composite,'true',JSON.stringify(contract));
   assert.equal(contract.appInert,true,JSON.stringify(contract));
   if(desktopAvoidsPanel){
     assert.ok(contract.pip.right<=contract.panel.left,JSON.stringify(contract));
   }
   for(const selector of ['#pipMute','#pipCamera','#pipEnd','#pipReturn']) await assertHitTarget(selector);
 };
 const exerciseCompositeControls=async()=>{
   const before=await page.evaluate(()=>({...window.__pipControlHits}));
   await page.locator('#pipMute').click();
   await page.locator('#pipCamera').click();
   const after=await page.evaluate(()=>({...window.__pipControlHits}));
   assert.equal(after.mute,before.mute+1);
   assert.equal(after.camera,before.camera+1);
   await page.locator('#pipReturn').focus();
   await page.keyboard.press('Tab');
   assert.ok(await page.evaluate(()=>Array.from(document.querySelectorAll('.document-editor,.deck-editor,.deck-presenter,.artifact-stage,.content-studio-drawer')).some(surface=>surface.contains(document.activeElement))), 'Tab from PiP must return to the open surface');
   await page.locator('#pipReturn').click();
   assert.equal(await page.evaluate(()=>window.__pipControlHits.return),before.return+1);
   assert.equal(await page.locator('#appShell').getAttribute('data-tool'),'room');
   assert.equal(new URL(page.url()).pathname,'/video');
   assert.equal(await pip.isVisible(),true);
   await page.evaluate(()=>{selectPD1Destination('Conversations');renderPipMeeting()});
   assert.equal(new URL(page.url()).pathname,'/conversations');
 };

 // Navigating channels and opening/closing a deck/document stage never owns
 // or tears down the call; the PiP stays mounted throughout.
 await page.evaluate(()=>{
   artifactEntries=[{id:'pip-stage',text:'# Active call\n\nThe document stays open beside the call.',createdAt:new Date().toISOString(),metadata:{title:'Call notes',type:'markdown',status:'complete'}}];
   openArtifactStage('pip-stage','Call notes');
 });
 await page.locator('.artifact-stage').waitFor({state:'visible'});
 assert.equal(await pip.isVisible(),true);
 await page.waitForTimeout(100);
 await assertComposite('.artifact-stage','.artifact-stage__panel');
 await exerciseCompositeControls();
 await page.evaluate(async()=>openDocumentStudio('pip-stage','Call notes',{}));
 await page.locator('.document-editor').waitFor({state:'visible'});
 await page.waitForTimeout(80);
 assert.equal(await pip.isVisible(),true);
 await assertComposite('.document-editor','.document-editor',{desktopAvoidsPanel:false});
 await exerciseCompositeControls();
 await page.locator('.document-editor').getByRole('button',{name:'Close Document Studio'}).click();
 await page.locator('.document-editor').waitFor({state:'detached'});
 assert.equal(await pip.isVisible(),true);
 await page.locator('.artifact-stage__close').click();
 await page.locator('.artifact-stage').waitFor({state:'detached'});
 assert.equal(await pip.isVisible(),true);

 // The KINO right-side browser is another transient SPA surface. PiP moves to
 // the safe edge rather than sitting over the drawer, then survives close.
 await page.evaluate(()=>openContentStudio(document.getElementById('contentStudioRailLink')));
 await page.locator('#contentStudioDrawer').waitFor({state:'visible'});
 await page.waitForTimeout(80);
 assert.equal(await pip.isVisible(),true);
	 await assertComposite('#contentStudioDrawer','.content-studio-drawer__panel');
	 await exerciseCompositeControls();

	 // A persisted top-left call position cannot cover either the ambient rail or
	 // KINO's header when the canvas expands to full workspace. The call composes
	 // within the explicit safe region instead of choosing an invalid candidate.
	 await page.evaluate(()=>{
	   const placement={dock:'left',y:0};
	   localStorage.setItem('stride.call-pip.placement.wide.v1',JSON.stringify(placement));
	   setContentStudioWorkspaceMode(document.getElementById('contentStudioDrawer'),true);
	   callPipScheduleSafePlacement(placement);
	 });
	 await page.waitForTimeout(100);
	 const fullWorkspaceGeometry=await page.evaluate(()=>{
	   const pip=document.getElementById('pipMeeting').getBoundingClientRect();
	   const rail=document.getElementById('toolRail').getBoundingClientRect();
	   const head=document.querySelector('.content-studio-drawer__head').getBoundingClientRect();
	   const panel=document.querySelector('.content-studio-drawer__panel').getBoundingClientRect();
	   return {pip:pip.toJSON(),rail:rail.toJSON(),head:head.toJSON(),panel:panel.toJSON(),mode:document.getElementById('contentStudioDrawer').dataset.workspaceMode};
	 });
	 assert.equal(fullWorkspaceGeometry.mode,'full',JSON.stringify(fullWorkspaceGeometry));
	 assert.ok(fullWorkspaceGeometry.pip.left>=fullWorkspaceGeometry.rail.right+10,JSON.stringify(fullWorkspaceGeometry));
	 assert.ok(fullWorkspaceGeometry.pip.top>=fullWorkspaceGeometry.head.bottom+10,JSON.stringify(fullWorkspaceGeometry));
	 assert.ok(fullWorkspaceGeometry.pip.right<=fullWorkspaceGeometry.panel.right,JSON.stringify(fullWorkspaceGeometry));
	 assert.ok(fullWorkspaceGeometry.pip.bottom<=fullWorkspaceGeometry.panel.bottom,JSON.stringify(fullWorkspaceGeometry));
	 await assertComposite('#contentStudioDrawer','.content-studio-drawer__panel',{desktopAvoidsPanel:false});
	 await exerciseCompositeControls();
	 await page.evaluate(()=>closeContentStudio());
	 await page.locator('#contentStudioDrawer').waitFor({state:'detached'});
	 assert.equal(await pip.isVisible(),true);

	 // Fallback KINO and the live call form one keyboard composite. Capture-phase
	 // PiP routing owns both boundaries; the drawer's no-call wrap must not skip it.
	 kinoRouteMode='hold';
	 heldKinoRoute=null;
	 await page.evaluate(()=>openContentStudio(document.getElementById('contentStudioRailLink')));
	 const fallbackStudio=page.locator('#contentStudioDrawer');
	 await fallbackStudio.waitFor({state:'visible'});
	 for(let attempt=0;attempt<50&&!heldKinoRoute;attempt++)await new Promise(resolve=>setTimeout(resolve,10));
	 assert.ok(heldKinoRoute,'KINO request was not held');
	 await fallbackStudio.locator('iframe').dispatchEvent('error');
	 const fallback=fallbackStudio.locator('.content-studio-drawer__fallback');
	 await fallback.waitFor({state:'visible'});
	 const fallbackExternal=fallback.locator('a');
	 const headerExternal=fallbackStudio.locator('.content-studio-drawer__actions a');
	 const focusCompositeEdge=async edge=>page.evaluate(edge=>{
	   const nodes=callPipFocusableNodes(document.getElementById('pipMeeting'));
	   nodes[edge==='first'?0:nodes.length-1].focus();
	 },edge);
	 await fallbackExternal.focus();
	 await page.keyboard.press('Tab');
	 assert.equal(await page.evaluate(()=>document.getElementById('pipMeeting').contains(document.activeElement)),true);
	 await focusCompositeEdge('last');
	 await page.keyboard.press('Tab');
	 assert.equal(await headerExternal.evaluate(node=>node===document.activeElement),true);
	 await page.keyboard.press('Shift+Tab');
	 assert.equal(await page.evaluate(()=>document.getElementById('pipMeeting').contains(document.activeElement)),true);
	 await focusCompositeEdge('first');
	 await page.keyboard.press('Shift+Tab');
	 assert.equal(await fallbackExternal.evaluate(node=>node===document.activeElement),true);
	 await page.keyboard.press('Escape');
	 await fallbackStudio.waitFor({state:'detached'});
	 await heldKinoRoute.abort().catch(()=>{});
	 kinoRouteMode='open';

	 // Resize and mobile-web keep the full control surface inside the viewport.
 await page.evaluate(()=>{
   const node=document.getElementById('pipMeeting');
   node.style.left='1200px';node.style.top='760px';
 });
 await page.setViewportSize({width:390,height:700});
 await page.waitForTimeout(100);
 const bounded=await page.evaluate(()=>{
   const rect=document.getElementById('pipMeeting').getBoundingClientRect();
   return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,innerWidth,innerHeight};
 });
 assert.ok(bounded.left>=0&&bounded.top>=0&&bounded.right<=bounded.innerWidth&&bounded.bottom<=bounded.innerHeight,JSON.stringify(bounded));
 assert.ok(bounded.width<=366,JSON.stringify(bounded));

 // Full-width mobile drawers cannot leave an outside rail. PiP intentionally
 // composes above the panel, remains bounded, and avoids its critical header.
 await page.evaluate(()=>openContentStudio(document.getElementById('contentStudioRailLink')));
 await page.locator('#contentStudioDrawer').waitFor({state:'visible'});
 await page.waitForTimeout(100);
 await assertComposite('#contentStudioDrawer','.content-studio-drawer__panel',{desktopAvoidsPanel:false});
 const mobileGeometry=await page.evaluate(()=>({
   pip:document.getElementById('pipMeeting').getBoundingClientRect().toJSON(),
   head:document.querySelector('.content-studio-drawer__head').getBoundingClientRect().toJSON()
 }));
 assert.ok(mobileGeometry.pip.top>=mobileGeometry.head.bottom||mobileGeometry.pip.bottom<=mobileGeometry.head.top,JSON.stringify(mobileGeometry));
 await exerciseCompositeControls();
 await page.evaluate(()=>closeContentStudio());

 // Reconnect state is explicit and does not remove the call view.
 await page.evaluate(()=>{
   isSignalReconnecting=true;
   pc=undefined;
   ws=undefined;
   setConnectionState('connecting','reconnecting…');
   renderPipMeeting();
 });
 assert.equal(await pip.isVisible(),true);
 assert.match(await page.locator('#pipStatus').textContent(),/reconnecting/);
 assert.equal(await pip.getAttribute('data-state'),'reconnecting');

 // A terminal media fault is distinguished from an in-progress reconnect.
 await page.evaluate(()=>{
   isSignalReconnecting=false;
   ws={readyState:WebSocket.OPEN,send(){},close(){this.readyState=WebSocket.CLOSED}};
   pc={connectionState:'failed',close(){}};
   setConnectionState('offline','media stalled');
 });
 assert.match(await page.locator('#pipStatus').textContent(),/connection issue/);
 assert.equal(await pip.getAttribute('data-state'),'error');

 // Only the canonical hangup control ends the room and removes PiP.
 await page.locator('#pipEnd').click();
 await pip.waitFor({state:'hidden'});
 assert.equal(await page.locator('#appShell').evaluate(node=>node.classList.contains('is-in-room')),false);

 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CALL_PIP_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered persistent call PiP harness: %v\n%s", err, output)
	}
}
