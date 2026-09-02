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
		"const studioCustomerPhaseDefinitions",
		"packaging_studio: [",
		"document_report: [",
		"function packagingStudioCustomerProgress(plan, artifact, ref, status)",
		"function packagingStudioPhaseListNode(progress)",
		"function packagingStudioTechnicalWorkNode(plan)",
		"id: 'frame', label: 'Frame'",
		"id: 'build', label: 'Build'",
		"id: 'compose', label: 'Compose'",
		"id: 'review', label: 'Review & deliver'",
		"stages: ['external_research', 'source_snapshot', 'evidence_entailment', 'evidence', 'red_team'",
		"Phase ${customerProgress.currentNumber}/${customerProgress.count}",
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
	phaseMapStart := strings.Index(html, "packaging_studio: [")
	if phaseMapStart < 0 {
		t.Fatal("Packaging Studio customer phase map boundaries missing")
	}
	phaseMapEnd := strings.Index(html[phaseMapStart:], "document_report: [")
	if phaseMapEnd < 0 {
		t.Fatal("Packaging Studio customer phase map end missing")
	}
	phaseMap := html[phaseMapStart : phaseMapStart+phaseMapEnd]
	if got := strings.Count(phaseMap, "id: '"); got != 4 {
		t.Errorf("customer phase map has %d phases, want 4", got)
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
		`data-pd1-destination="Work" aria-label="Work"`,
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
		`const contentStudioLayoutStorageKey = 'stride.content-studio.layout.v1'`,
		`function setContentStudioPanelWidth(drawer, requestedWidth, options = {})`,
		`function setContentStudioWorkspaceMode(drawer, full, options = {})`,
		`function bindContentStudioResize(drawer, resize)`,
		`resize.addEventListener('lostpointercapture', onLostPointerCapture)`,
		`window.addEventListener('pointerup', onWindowPointerUp, true)`,
		`window.addEventListener('blur', onWindowBlur)`,
		`document.addEventListener('visibilitychange', onVisibilityChange)`,
		`status.textContent = 'Open'`,
		`resize.setAttribute('role', 'separator')`,
		`resize.setAttribute('aria-orientation', 'vertical')`,
		`.content-studio-drawer[data-workspace-mode="full"]`,
		`body:has(#contentStudioDrawer[data-workspace-mode="full"]) #toolRail`,
		`left: 56px;`,
		`.pd1-primary-nav__external:active { transform: scale(var(--press-scale)); }`, // plan 011: one press token
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
	if strings.Contains(html, `id="contentStudioRailLink"`) || strings.Contains(html, `class="pd1-primary-nav__external"`) {
		t.Error("Build 18 retired the external Content Studio rail entry; Work is the canonical destination")
	}
}

func TestContentStudioWorkspaceLayoutRendered(t *testing.T) {
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
const html=fs.readFileSync(process.env.CONTENT_STUDIO_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:820}});
 await page.route('https://kino.grok.me/**',route=>route.fulfill({status:200,contentType:'text/html',body:'<!doctype html><title>KINO</title><main>KINO fixture</main>'}));
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 const work=page.locator('.pd1-primary-nav__item[data-pd1-destination="Work"]');
 await work.waitFor({state:'visible'});
 assert.equal(await page.locator('#contentStudioRailLink,.pd1-primary-nav__external').count(),0);
 await work.click();
 await page.waitForFunction(()=>document.getElementById('appShell')?.dataset.pd1Destination==='Work'&&location.pathname==='/work');
 assert.equal(await work.getAttribute('aria-current'),'page');
 assert.equal(await page.locator('#appShell').getAttribute('data-tool'),'research');
 assert.equal(await page.locator('#researchTool').isVisible(),true);
 assert.equal(await page.locator('#studioAppsTitle').textContent(),'Installed apps');
 assert.equal(await page.locator('#contentStudioDrawer').count(),0);
 await browser.close();server.close();return;
 await page.evaluate(()=>{localStorage.removeItem('stride.content-studio.layout.v1');contentStudioLayoutPreference=null;});
 const railLink=page.locator('#contentStudioRailLink');
 await railLink.waitFor({state:'visible'});
 await railLink.click();
 const drawer=page.locator('#contentStudioDrawer');
 const panel=drawer.locator('.content-studio-drawer__panel');
 const frame=drawer.locator('.content-studio-drawer__frame');
 const resize=drawer.locator('.content-studio-drawer__resize');
 const mode=drawer.locator('.content-studio-drawer__workspace-mode');
 await drawer.waitFor({state:'visible'});
 await frame.waitFor({state:'visible'});
	 await page.waitForFunction(()=>document.querySelector('.content-studio-drawer__status')?.textContent==='Open');
 await page.evaluate(()=>{
   const node=document.querySelector('.content-studio-drawer__frame');
   window.__contentStudioStableFrame=node;
   window.__contentStudioLayoutLoads=0;
   node.addEventListener('load',()=>window.__contentStudioLayoutLoads++);
 });

 // The left edge is a full-height, accessible separator with explicit bounds.
 assert.equal(await resize.getAttribute('role'),'separator');
 assert.equal(await resize.getAttribute('aria-orientation'),'vertical');
 assert.equal(await resize.getAttribute('aria-label'),'Resize Content Studio');
 assert.equal(Number(await resize.getAttribute('aria-valuemin')),520);
 assert.equal(Number(await resize.getAttribute('aria-valuemax')),1224);
 const initialWidth=(await panel.boundingBox()).width;
 const initialHandle=await resize.boundingBox();
 assert.ok(initialHandle&&initialHandle.height===820&&initialHandle.width===40,JSON.stringify(initialHandle));
 await page.mouse.move(initialHandle.x+20,initialHandle.y+100);
 await page.mouse.down();
 await page.mouse.move(initialHandle.x+200,initialHandle.y+100,{steps:5});
 await page.mouse.up();
	 const draggedWidth=(await panel.boundingBox()).width;
	 assert.ok(Math.abs(draggedWidth-(initialWidth-180))<=2,JSON.stringify({initialWidth,draggedWidth}));
	 assert.equal(Number(await resize.getAttribute('aria-valuenow')),Math.round(draggedWidth));
	 assert.equal(JSON.parse(await page.evaluate(()=>localStorage.getItem('stride.content-studio.layout.v1'))).width,Math.round(draggedWidth));

	 // Losing capture or application focus cancels the gesture and always restores
	 // iframe interaction; neither path may strand the surface in resize mode.
	 await page.evaluate(()=>{
	   const node=document.querySelector('.content-studio-drawer__resize');
	   node.addEventListener('pointerdown',event=>{window.__contentStudioPointerId=event.pointerId},{once:true});
	 });
	 const captureHandle=await resize.boundingBox();
	 await page.mouse.move(captureHandle.x+20,captureHandle.y+100);
	 await page.mouse.down();
	 assert.equal(await drawer.evaluate(node=>node.classList.contains('is-resizing')),true);
	 await page.evaluate(()=>{
	   const node=document.querySelector('.content-studio-drawer__resize');
	   node.releasePointerCapture(window.__contentStudioPointerId);
	 });
	 await page.mouse.move(captureHandle.x+80,captureHandle.y+100);
	 await page.mouse.up();
	 await page.waitForFunction(()=>!document.getElementById('contentStudioDrawer').classList.contains('is-resizing'));
	 assert.equal(await frame.evaluate(node=>getComputedStyle(node).pointerEvents),'auto');
	 const blurHandle=await resize.boundingBox();
	 await page.mouse.move(blurHandle.x+20,blurHandle.y+100);
	 await page.mouse.down();
	 await page.evaluate(()=>window.dispatchEvent(new Event('blur')));
	 await page.waitForFunction(()=>!document.getElementById('contentStudioDrawer').classList.contains('is-resizing'));
	 assert.equal(await frame.evaluate(node=>getComputedStyle(node).pointerEvents),'auto');
	 await page.mouse.up();
	 const visibilityHandle=await resize.boundingBox();
	 await page.mouse.move(visibilityHandle.x+20,visibilityHandle.y+100);
	 await page.mouse.down();
	 await page.evaluate(()=>{
	   Object.defineProperty(document,'visibilityState',{configurable:true,value:'hidden'});
	   document.dispatchEvent(new Event('visibilitychange'));
	   delete document.visibilityState;
	 });
	 await page.waitForFunction(()=>!document.getElementById('contentStudioDrawer').classList.contains('is-resizing'));
	 assert.equal(await frame.evaluate(node=>getComputedStyle(node).pointerEvents),'auto');
	 await page.mouse.up();

 // Keyboard ownership mirrors the edge: left widens, right narrows, Home/End clamp.
 await resize.focus();
 await page.keyboard.press('End');
 assert.equal((await panel.boundingBox()).width,1224);
 await page.keyboard.press('ArrowLeft');
 assert.equal((await panel.boundingBox()).width,1224);
 await page.keyboard.press('Home');
 assert.equal((await panel.boundingBox()).width,520);
 await page.keyboard.press('ArrowLeft');
 assert.equal((await panel.boundingBox()).width,536);
 await page.keyboard.press('ArrowRight');
 assert.equal((await panel.boundingBox()).width,520);

 // Persist a human-sized panel through a real edge drag and reopen.
 const minimumHandle=await resize.boundingBox();
 await page.mouse.move(minimumHandle.x+20,minimumHandle.y+120);
 await page.mouse.down();
 await page.mouse.move(minimumHandle.x-164,minimumHandle.y+120,{steps:5});
 await page.mouse.up();
 const preferredWidth=(await panel.boundingBox()).width;
 assert.ok(Math.abs(preferredWidth-704)<=2,JSON.stringify({preferredWidth}));
 assert.deepEqual(await page.evaluate(()=>({same:window.__contentStudioStableFrame===document.querySelector('.content-studio-drawer__frame'),loads:window.__contentStudioLayoutLoads,src:document.querySelector('.content-studio-drawer__frame').src})),{same:true,loads:0,src:'https://kino.grok.me/'});
 await drawer.locator('.content-studio-drawer__close').click();
 await drawer.waitFor({state:'detached'});
 assert.equal(await railLink.evaluate(node=>node===document.activeElement),true);
 await railLink.click();
 await drawer.waitFor({state:'visible'});
	 await page.waitForFunction(()=>document.querySelector('.content-studio-drawer__status')?.textContent==='Open');
 assert.ok(Math.abs((await panel.boundingBox()).width-preferredWidth)<=1,JSON.stringify({preferredWidth,restored:(await panel.boundingBox()).width}));

 // Workspace mode owns x=56..viewport: the rail stays visibly outside it.
 await page.evaluate(()=>{
   const node=document.querySelector('.content-studio-drawer__frame');
   window.__contentStudioStableFrame=node;
   window.__contentStudioLayoutLoads=0;
   node.addEventListener('load',()=>window.__contentStudioLayoutLoads++);
 });
 assert.equal(await mode.getAttribute('aria-pressed'),'false');
 await mode.click();
 assert.equal(await mode.getAttribute('aria-pressed'),'true');
 assert.equal(await mode.getAttribute('aria-label'),'Return Content Studio to side panel');
 const fullGeometry=await page.evaluate(()=>{
   const root=document.getElementById('contentStudioDrawer').getBoundingClientRect();
   const panel=document.querySelector('.content-studio-drawer__panel').getBoundingClientRect();
   const rail=document.querySelector('.tool-rail');
   const railRect=rail.getBoundingClientRect();
   const railStyle=getComputedStyle(rail);
	   const railLink=document.getElementById('contentStudioRailLink');
	   return {root:root.toJSON(),panel:panel.toJSON(),rail:railRect.toJSON(),railDisplay:railStyle.display,railVisibility:railStyle.visibility,railOpacity:railStyle.opacity,railPointerEvents:railStyle.pointerEvents,railAmbient:Boolean(railLink.closest('[inert]')),sameFrame:window.__contentStudioStableFrame===document.querySelector('.content-studio-drawer__frame'),loads:window.__contentStudioLayoutLoads};
 });
 assert.equal(fullGeometry.root.left,56,JSON.stringify(fullGeometry));
 assert.equal(fullGeometry.root.right,1280,JSON.stringify(fullGeometry));
 assert.equal(fullGeometry.panel.left,56,JSON.stringify(fullGeometry));
 assert.equal(fullGeometry.panel.right,1280,JSON.stringify(fullGeometry));
 assert.equal(fullGeometry.rail.right,56,JSON.stringify(fullGeometry));
 assert.equal(fullGeometry.railDisplay,'flex',JSON.stringify(fullGeometry));
 assert.equal(fullGeometry.railVisibility,'visible',JSON.stringify(fullGeometry));
	 assert.equal(fullGeometry.railOpacity,'0.48',JSON.stringify(fullGeometry));
	 assert.equal(fullGeometry.railPointerEvents,'none',JSON.stringify(fullGeometry));
	 assert.equal(fullGeometry.railAmbient,true,JSON.stringify(fullGeometry));
 assert.equal(fullGeometry.sameFrame,true,JSON.stringify(fullGeometry));
 assert.equal(fullGeometry.loads,0,JSON.stringify(fullGeometry));
 assert.equal(await resize.getAttribute('tabindex'),'-1');

 // Both mode and width restore, while responsive layout keeps mobile controls absent.
 await drawer.locator('.content-studio-drawer__close').click();
 await railLink.click();
 await drawer.waitFor({state:'visible'});
 assert.equal(await drawer.getAttribute('data-workspace-mode'),'full');
 await mode.click();
 assert.equal(await drawer.getAttribute('data-workspace-mode'),'panel');
 assert.ok(Math.abs((await panel.boundingBox()).width-preferredWidth)<=1);
 await page.evaluate(()=>{
   const node=document.querySelector('.content-studio-drawer__frame');
   window.__contentStudioStableFrame=node;
   window.__contentStudioLayoutLoads=0;
   node.addEventListener('load',()=>window.__contentStudioLayoutLoads++);
 });
 await page.setViewportSize({width:390,height:844});
 await page.waitForTimeout(40);
 assert.equal(await railLink.isVisible(),false);
 assert.equal(await mode.isVisible(),false);
 assert.equal(await resize.isVisible(),false);
 assert.equal((await panel.boundingBox()).width,390);
 assert.deepEqual(await page.evaluate(()=>({same:window.__contentStudioStableFrame===document.querySelector('.content-studio-drawer__frame'),loads:window.__contentStudioLayoutLoads})),{same:true,loads:0});
 await page.setViewportSize({width:1280,height:820});
 await page.waitForTimeout(40);
 assert.ok(Math.abs((await panel.boundingBox()).width-preferredWidth)<=1);
 await page.emulateMedia({reducedMotion:'reduce'});
 assert.equal(await panel.evaluate(node=>getComputedStyle(node).transitionDuration),'0s');
 await drawer.locator('.content-studio-drawer__close').click();
 assert.equal(await railLink.evaluate(node=>node===document.activeElement),true);
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CONTENT_STUDIO_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Content Studio layout harness: %v\n%s", err, output)
	}
}

func TestContentStudioEmbedFailureFocusRendered(t *testing.T) {
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
const html=fs.readFileSync(process.env.CONTENT_STUDIO_FAILURE_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:820}});
 const pattern='https://kino.grok.me/**';
 await page.route(pattern,route=>route.abort('failed'));
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 const work=page.locator('.pd1-primary-nav__item[data-pd1-destination="Work"]');
 await work.waitFor({state:'visible'});
 assert.equal(await page.locator('#contentStudioRailLink,.pd1-primary-nav__external').count(),0);
 await work.click();
 await page.waitForFunction(()=>document.getElementById('appShell')?.dataset.pd1Destination==='Work');
 assert.equal(await page.locator('#researchTool').isVisible(),true);
 assert.equal(await page.locator('#contentStudioDrawer').count(),0);
 assert.equal(await page.locator('iframe[src^="https://kino.grok.me"]').count(),0);
 await browser.close();server.close();return;
 const rail=page.locator('#contentStudioRailLink');

 // Cross-origin frame failures can surface as load, not error. The parent must
 // describe only its own open surface and keep the truthful external exit live.
 await rail.click();
 let drawer=page.locator('#contentStudioDrawer');
 await drawer.waitFor({state:'visible'});
 await page.waitForFunction(()=>document.querySelector('.content-studio-drawer__status')?.textContent==='Open');
 assert.equal(await drawer.locator('.content-studio-drawer__status').first().textContent(),'Open');
 assert.equal(await drawer.locator('.content-studio-drawer__fallback').isVisible(),false);
 assert.equal(await drawer.locator('.content-studio-drawer__actions a').isVisible(),true);
 assert.doesNotMatch(await drawer.textContent(),/\bReady\b/);
 await drawer.locator('.content-studio-drawer__close').click();
 await drawer.waitFor({state:'detached'});

 // A genuine error/stall fallback remains an airtight modal cycle. Hold the
 // navigation, dispatch the browser error path, and prove both tab directions.
 await page.unroute(pattern);
 let heldRoute=null;
 await page.route(pattern,route=>{heldRoute=route;});
 await rail.click();
 drawer=page.locator('#contentStudioDrawer');
 await drawer.waitFor({state:'visible'});
 for(let attempt=0;attempt<50&&!heldRoute;attempt++)await new Promise(resolve=>setTimeout(resolve,10));
 assert.ok(heldRoute,'KINO request was not held');
 await drawer.locator('iframe').dispatchEvent('error');
 const fallback=drawer.locator('.content-studio-drawer__fallback');
 await fallback.waitFor({state:'visible'});
 assert.equal(await drawer.locator('.content-studio-drawer__status').first().textContent(),'Unable to embed');
 assert.equal(await drawer.locator('iframe').getAttribute('tabindex'),'-1');
 assert.deepEqual(await drawer.locator('.content-studio-drawer__focus-sentinel').evaluateAll(nodes=>nodes.map(node=>node.tabIndex)),[-1,-1]);
 const headerExternal=drawer.locator('.content-studio-drawer__actions a');
 const fallbackExternal=fallback.locator('a');
 await fallbackExternal.focus();
 await page.keyboard.press('Tab');
 assert.equal(await headerExternal.evaluate(node=>node===document.activeElement),true);
 await page.keyboard.press('Shift+Tab');
 assert.equal(await fallbackExternal.evaluate(node=>node===document.activeElement),true);
 assert.equal(await page.locator('#appShell').getAttribute('inert'),'');
 await page.keyboard.press('Escape');
 await drawer.waitFor({state:'detached'});
 assert.equal(await rail.evaluate(node=>node===document.activeElement),true);
 await heldRoute.abort().catch(()=>{});
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CONTENT_STUDIO_FAILURE_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Content Studio failure harness: %v\n%s", err, output)
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
 assert.equal(quietChannel.eyebrow,'Presentation · Draft · Phase 2 of 4');
 assert.match(quietChannel.meta,/grounding and building the argument/i);
 assert.match(quietChannel.meta,/11%/);
 assert.equal(quietChannel.checkpoints,0);
 assert.doesNotMatch(quietChannel.eyebrow+quietChannel.meta,/Needs input/i);
 assert.equal(quietChannel.runlogs,0);
 assert.equal(quietChannel.progressbars,0);
 await page.setViewportSize({width:390,height:844});await page.waitForTimeout(60);
 const compactProgress=await page.locator('[data-work-artifact-id="packaging-run"].scout-chat-work-card--presentation').evaluate(card=>{const rect=node=>node.getBoundingClientRect().toJSON();const title=card.querySelector('.scout-chat-work-card__title');const project=card.querySelector('.scout-chat-work-card__project');const eyebrow=card.querySelector('.scout-chat-work-card__eyebrow');return {className:card.className,card:rect(card),title:rect(title),project:rect(project),eyebrow:eyebrow.textContent,titleText:title.textContent,projectText:project.textContent,titleWhiteSpace:getComputedStyle(title).whiteSpace,projectWhiteSpace:getComputedStyle(project).whiteSpace,scrollWidth:document.documentElement.scrollWidth};});
 assert.equal(compactProgress.eyebrow,'Presentation · Draft · Phase 2 of 4');
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
 assert.equal(await activityDrawer.locator('.chat-context-phase-entry').count(),4);
 assert.equal(await activityDrawer.locator('.chat-context-phase-entry.is-current').getAttribute('data-phase'),'build');
 assert.match(await activityDrawer.locator('.chat-context-phase-entry.is-current').textContent(),/Build.*grounding and building the argument/is);
 assert.equal(await activityDrawer.locator('.chat-context-phase-entry[data-phase="compose"] .chat-context-phase-entry__sentence').count(),0);
 assert.match(await activityDrawer.locator('#chatContextMeta').textContent(),/Phase 2 of 4.*11%/);
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
 assert.equal(await activityDrawer.getByRole('button',{name:'Open',exact:true}).count(),0,'a completed run without an immutable result receipt must not open its process record as the customer deliverable');
 assert.match(await activityDrawer.locator('.chat-context-event__note').textContent(),/without an exact deliverable attached/i);
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
 assert.equal(blockedWithoutDecision.eyebrow,'Presentation · Blocked · Phase 2 of 4');
 assert.match(blockedWithoutDecision.meta,/strengthening the story before design begins/i);
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
 assert.deepEqual(blockedStoryPrecedesReadyDesign,{id:'build',status:'blocked',number:2,sentence:'Scout is strengthening the story before design begins.'});

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

 if(false){
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
 const studioExternal=studio.locator('.content-studio-drawer__actions a.content-studio-drawer__action');
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
 }

 const workDestination=page.locator('[data-pd1-destination="Work"]');
 await workDestination.waitFor({state:'visible'});
 assert.equal(await page.locator('#contentStudioRailLink,.pd1-primary-nav__external').count(),0);

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
 assert.equal(await page.locator('#contentStudioRailLink,.pd1-primary-nav__external').count(),0);
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
