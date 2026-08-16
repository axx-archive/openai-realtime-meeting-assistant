package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopHomeUsesServerOwnedContextAndEditableSuggestions(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`fetch('/assistant/home', { cache: 'no-store' })`,
		`homeSnapshot.items.filter(item => item?.kind !== 'live-meeting')`,
		`selectedHomeCategoryId === category?.id`,
		`homeContinuity.hidden = !availableContext`,
		`homeScoutInput.value = String(suggestion.text)`,
		`selectedHomeSuggestionDestination = suggestion?.destination || null`,
		`attempt.threadId`,
		`/messages`,
		`homeScoutInput.focus()`,
		`desktopChatScrollToMessage(String(destination.messageId))`,
		`A failed refresh can`,
		`homeSnapshot = { version: 'home-v2', generatedAt: '', items: [], starters: [], allClear: false }`,
		`Home unavailable · Retry`,
		`homeSnapshotGeneration += 1`,
		`id="homeRealtimeVoice"`,
		`aria-label="Start a new private voice chat with Scout"`,
		`.home-starter__text {
        display: none;`,
		`const HOME_CATEGORY_SHELLS = Object.freeze([`,
		`data-hydrated="false"`,
		`aria-busy="true"`,
		`homeStarters.hidden = true`,
		`homeSuggestions.hidden = true`,
		`homeSnapshotRequest?.audienceKey === audienceKey`,
		`if (refreshAfterCurrent) homeSnapshotRefreshQueued = true`,
		`resetHomeProjection()`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("desktop Home missing %q", want)
		}
	}
	if strings.Contains(html, "homeSnapshotReceivedAt") {
		t.Fatal("desktop Home retains authorization-filtered context after a failed refresh")
	}
	starterStart := strings.Index(html, "function homeCategoryNode")
	starterEnd := strings.Index(html[starterStart:], "function renderHomeStarters")
	if starterStart < 0 || starterEnd < 0 {
		t.Fatal("home starter function boundary missing")
	}
	starterBody := html[starterStart : starterStart+starterEnd]
	for _, forbidden := range []string{"/assistant/goal", "toolTemplate", "authorityHint", "data-tool", "sendScoutChat"} {
		if strings.Contains(starterBody, forbidden) {
			t.Fatalf("home starter selects work capability via %q", forbidden)
		}
	}
	for _, removed := range []string{
		`--cradle-width: clamp(330px, 39vw, 520px)`,
		`--cradle-width: min(78vw, 340px)`,
	} {
		if strings.Contains(html, removed) {
			t.Fatalf("oversized Home cradle returned: %q", removed)
		}
	}
}

func TestDesktopExistingThreadRetiresManualProjectAttachment(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`const explicitProjectAttachmentEnabled = false`,
		`if (!explicitProjectAttachmentEnabled) return`,
		`const projectContextToken = explicitProjectAttachmentEnabled && scoutChatProjectContext.selected`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("retired existing-thread Project contract missing %q", want)
		}
	}
	if strings.Contains(html, `id="scoutChatProjectChip"`) {
		t.Fatal("manual existing-thread Project control remains in rendered markup")
	}
}

func TestDesktopThreadReplyRetiresManualProjectAttachment(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`chatContextProjectChip.hidden = !explicitProjectAttachmentEnabled`,
		`const projectContextToken = explicitProjectAttachmentEnabled && selectedProject`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("retired desktop reply Project contract missing %q", want)
		}
	}
	if strings.Contains(html, `id="chatContextProjectChip"`) {
		t.Fatal("manual reply Project control remains in rendered markup")
	}
}

func TestDesktopProjectRefreshFailsSafeWhenOpaqueChoiceIdentityDisappears(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, "function rebindOpaqueProjectChoice")
	endMarker := "\n\t  function scheduleScoutChatProjectContextRefresh"
	end := strings.Index(html[start:], endMarker)
	if start < 0 || end < 0 {
		t.Fatal("opaque Project choice helper boundary missing")
	}
	helper := html[start : start+end]
	script := "'use strict'; const assert=require('node:assert/strict');\n" + helper + `
const current={title:'Country Golf',token:'token-old',choiceKey:'choice-a'};
const refreshed={title:'Country Golf renamed',token:'token-new',choiceKey:'choice-a'};
const replacement={title:'Ball Dogs',token:'token-b',choiceKey:'choice-b'};
assert.deepEqual(rebindOpaqueProjectChoice(current,replacement,[refreshed]),refreshed);
assert.equal(rebindOpaqueProjectChoice(current,replacement,[replacement]),null);
assert.equal(rebindOpaqueProjectChoice({...current,choiceKey:''},replacement,[replacement]),null);
assert.equal(rebindOpaqueProjectChoice(null,{...replacement,choiceKey:''},[replacement]),null);
assert.deepEqual(rebindOpaqueProjectChoice(null,replacement,[]),replacement);
`
	command := exec.Command("node", "-e", script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("opaque Project choice behavior failed: %v\n%s", err, output)
	}
}

func TestDesktopHomeCategoryShellsExistBeforeJavaScriptHydration(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, `<div id="homeStarters"`)
	end := strings.Index(html, `<div id="homeSuggestions"`)
	if start < 0 || end <= start {
		t.Fatal("static Home category shell boundary missing")
	}
	shell := html[start:end]
	// Real work items (homeContinuity) are the primary surface now; the generic
	// Continue/Explore/Create/Challenge starters start hidden and only appear
	// when no continuity items exist.
	if !strings.Contains(strings.SplitN(shell, ">", 2)[0], " hidden") {
		t.Fatal("static Home category shells should be hidden initially (real work is primary)")
	}
	if got := strings.Count(shell, `class="home-starter pressable"`); got != 4 {
		t.Fatalf("static Home category count=%d, want 4 before JavaScript hydration", got)
	}
	for _, label := range []string{"Continue", "Explore", "Create", "Challenge"} {
		if !strings.Contains(shell, `aria-label="`+label+`. Suggestions loading."`) {
			t.Fatalf("static Home category %q is missing its loading semantics", label)
		}
	}
}

func TestDesktopHomeRenderedFocusedSuggestionsAndResponsiveLayout(t *testing.T) {
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
const html=fs.readFileSync(process.env.HOME_INDEX,'utf8');
const dictation=fs.readFileSync(path.join(path.dirname(process.env.HOME_INDEX),'public','composer-dictation.js'),'utf8');
const home={version:'home-v2',generatedAt:'2026-08-11T20:00:00Z',allClear:false,items:[
 {id:'continue',kind:'conversation',eyebrow:'Continue',title:'Country Golf',detail:'Pick up at the venue and membership strategy.',destination:{route:'thread',threadId:'country-golf',title:'Country Golf'}},
 {id:'needs-you',kind:'needs-you',eyebrow:'Needs you',title:'Review the investor package',detail:'Open to respond',destination:{route:'alerts'}},
 {id:'deck',kind:'work',eyebrow:'Presentation · Building',title:'Create the STRIDE pitch deck',detail:'Building the first draft',destination:{route:'thread',threadId:'deck',title:'STRIDE pitch deck'}}
],starters:[
 {id:'continue',label:'Continue',detail:'Pick up recent work',suggestions:[{id:'continue-1',text:'Continue where we left off in Country Golf.',whyThis:'You were last working here.',destination:{route:'thread',threadId:'country-golf',title:'Country Golf'}}]},
 {id:'explore',label:'Explore',detail:'Understand and discover',suggestions:[{id:'explore-1',text:'Explore the biggest open question in Country Golf.',whyThis:'You were last working here.',destination:{route:'new-private'}},{id:'explore-2',text:'Connect what has come up across your conversations about membership and identify the useful next move.',whyThis:'Membership has come up across 2 conversations you can open.',destination:{route:'new-private'}}]},
 {id:'create',label:'Create',detail:'Make the next useful thing',suggestions:[{id:'create-1',text:'Create the next useful deliverable for Country Golf.',whyThis:'Work is already underway here.',destination:{route:'new-private'}}]},
 {id:'challenge',label:'Challenge',detail:'Grill and red-team',suggestions:[{id:'challenge-1',text:'Challenge the current thinking in Country Golf and identify the weakest assumptions.',whyThis:'This decision is waiting on you.',destination:{route:'new-private'}}]}
]};
let homeAvailable=true;
let homeRequestCount=0;
let projectContextRequestCount=0;
let chatMutationCount=0;
let chatRequestBodies=[];
let homeAccount='a';
let delayProjectA=false;
const initialHomeReadyAt=Date.now()+2000;
let roomsMode='quiet';
const server=http.createServer((req,res)=>{
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation);}
if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
 if(req.url==='/assistant/home'){
   homeRequestCount+=1;
   const payloadHome=homeAccount==='b'?JSON.parse(JSON.stringify(home).replaceAll('Country Golf','B launch')):home;
   if(!homeAvailable){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
   const reply=()=>{res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({home:payloadHome}));};
   if(homeAccount==='b'){return setTimeout(reply,1200);}
   if(Date.now()<initialHomeReadyAt){return setTimeout(reply,initialHomeReadyAt-Date.now());}
   return reply();
 }
	 if(req.url==='/assistant/project-context'){
	   projectContextRequestCount+=1;
   const projectAccount=homeAccount;
   let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{
     const body=JSON.parse(raw||'{}');
     const text=String(body.text||'');
     const title=projectAccount==='b'?'B Project':'Country Golf';
     const choice={title,token:projectAccount==='b'?'opaque-b-project':'opaque-country-golf',choiceKey:projectAccount==='b'?'choice-b-project':'choice-country-golf'};
     const threadDestination=body.destination?.route==='thread';
     const projectContext={available:body.destination?.route==='new-private'||threadDestination,scopeKey:projectAccount==='b'?'scope-b':'scope-a',status:threadDestination?'bound':(text.includes(title)?'suggested':'unlinked'),choices:[choice]};
     if(projectContext.status==='suggested'||projectContext.status==='bound')projectContext.suggested={...choice,suggested:projectContext.status==='suggested'};
     const reply=()=>{res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,projectContext}));};
     if(projectAccount==='a'&&delayProjectA)return setTimeout(reply,1000);
     reply();
   });return;
 }
 if(req.url.startsWith('/assistant/chat-threads')&&req.method==='POST'){let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{chatMutationCount+=1;chatRequestBodies.push(raw);res.writeHead(503,{'content-type':'application/json'});res.end('{}');});return;}
 if(req.url==='/__home_account_b'){homeAccount='b';res.writeHead(204);return res.end();}
 if(req.url==='/__delay_project_a'){delayProjectA=true;res.writeHead(204);return res.end();}
 if(req.url==='/__rooms_live'){roomsMode='live';res.writeHead(204);return res.end();}
 if(req.url==='/rooms'){
   const rooms=roomsMode==='live'?[{id:'weekly-product',name:'Weekly product',live:true,participantCount:3,archived:false}]:[];
   res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({rooms}));
 }
 if(req.url.startsWith('/api/stride/')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
 if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(404,{'content-type':'application/json'});return res.end('{}');}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:900}});
 await page.goto(base+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 // Real work items (continuity) are now the primary surface; starters are hidden
 // when continuity items exist.
 await page.waitForFunction(()=>document.querySelectorAll('#homeContinuity .home-continuity__row').length===3);
 const immediate=await page.evaluate(()=>({
   continuity:document.querySelectorAll('#homeContinuity .home-continuity__row').length,
   startersHidden:document.getElementById('homeStarters').hidden,
   composerTop:document.getElementById('homeScoutComposer').getBoundingClientRect().top,
   greeting:document.getElementById('officeLaunchGreeting').getBoundingClientRect().toJSON(),
   wrap:document.querySelector('.office-launch__wrap').getBoundingClientRect().toJSON()
 }));
 assert.equal(immediate.continuity,3,'three real work items paint as primary surface');
 assert.equal(immediate.startersHidden,true,'generic starters are hidden when real work exists');
 assert.equal(homeRequestCount,1,'startup auth and room hydration coalesce into one Home request');
 const hydratedGeometry=await page.evaluate(()=>({composerTop:document.getElementById('homeScoutComposer').getBoundingClientRect().top,greeting:document.getElementById('officeLaunchGreeting').getBoundingClientRect().toJSON(),wrap:document.querySelector('.office-launch__wrap').getBoundingClientRect().toJSON()}));
	 // Chromium can settle the centered svh flex container by one fractional
	 // device-pixel row after first paint. Keep the bound within five CSS pixels;
	 // card/composer dimensions remain identical and no late content is inserted.
	 assert.ok(Math.abs(hydratedGeometry.composerTop-immediate.composerTop)<=5,'Home hydration shifted geometry: '+JSON.stringify({immediate,hydratedGeometry}));
 await page.fill('#homeScoutInput','Unsent local draft');
 homeAvailable=false;
 await page.evaluate(()=>loadHomeSnapshot());
 await page.waitForFunction(()=>!document.getElementById('homeRefreshRetry').hidden);
 const failed=await page.evaluate(()=>({
   continuity:document.querySelectorAll('#homeContinuity .home-continuity__row').length,
   startersHidden:document.getElementById('homeStarters').hidden,
   draft:document.getElementById('homeScoutInput').value,
   retry:document.getElementById('homeRefreshRetry').textContent.trim()
 }));
 // When refresh fails, starters stay hidden (honest empty state, no fake generic pack)
	assert.deepEqual(failed,{continuity:0,startersHidden:true,draft:'Unsent local draft',retry:'Home unavailable · Retry'});
	homeAvailable=true;
	await page.evaluate(()=>document.getElementById('homeRefreshRetry').click());
	await page.waitForFunction(()=>document.querySelectorAll('#homeContinuity .home-continuity__row').length===3&&document.getElementById('homeRefreshRetry').hidden);
 await page.fill('#homeScoutInput','');
 const renderDir=String(process.env.HOME_RENDER_DIR||'').trim();
 if(renderDir)fs.mkdirSync(renderDir,{recursive:true});
 const capture=async(name,theme)=>{
   await page.evaluate(next=>renderTheme(next),theme);
   await page.mouse.move(2,2);await page.waitForTimeout(180);
   const geometry=await page.evaluate(()=>({fits:document.documentElement.scrollWidth<=innerWidth,continuity:document.querySelectorAll('#homeContinuity .home-continuity__row').length,privacyFooter:document.querySelectorAll('.office-launch__hint,#officeLaunchHint').length}));
   assert.deepEqual(geometry,{fits:true,continuity:3,privacyFooter:0});
   if(renderDir)await page.screenshot({path:path.join(renderDir,name+'-'+theme+'.png')});
 };
 for(const theme of ['dark','light'])await capture('desktop-home',theme);
 await page.setViewportSize({width:390,height:844});
 await page.waitForTimeout(100);
 for(const theme of ['dark','light'])await capture('phone-home',theme);
 await page.setViewportSize({width:1440,height:900});
 await page.evaluate(()=>{officeWs={readyState:WebSocket.OPEN,send(){},close(){}};setRealtimeVoiceMode('private');privateRealtimeVoiceThreadID='scout-voice-render';setVoiceIslandState('listening','listening…');});
 await page.waitForFunction(()=>!document.getElementById('voiceIsland').hidden);
 await page.waitForTimeout(320);
 let voiceGeometry=await page.locator('#voiceIsland').evaluate(node=>{const box=node.getBoundingClientRect();return{right:innerWidth-box.right,top:box.top,width:box.width}});
 assert.ok(voiceGeometry.right>=14&&voiceGeometry.right<=24&&voiceGeometry.top>=12&&voiceGeometry.top<=24&&voiceGeometry.width<=300,JSON.stringify(voiceGeometry));
 for(const theme of ['dark','light'])await capture('desktop-home-voice-live',theme);
 await page.setViewportSize({width:390,height:844});
 await page.waitForTimeout(100);
 await page.evaluate(()=>{setRealtimeVoiceMode('private');privateRealtimeVoiceThreadID='scout-voice-render';setVoiceIslandState('listening','listening…');});
 voiceGeometry=await page.locator('#voiceIsland').evaluate(node=>{const box=node.getBoundingClientRect();return{right:innerWidth-box.right,top:box.top,width:box.width}});
 assert.ok(voiceGeometry.right>=8&&voiceGeometry.right<=16&&voiceGeometry.top>=60&&voiceGeometry.top<=72&&voiceGeometry.width<=300,JSON.stringify(voiceGeometry));
 for(const theme of ['dark','light'])await capture('phone-home-voice-live',theme);
 await page.evaluate(()=>{setRealtimeVoiceMode('idle');setVoiceIslandState('idle');});
 await page.setViewportSize({width:1440,height:900});
 // Generic starters are always hidden now (not a fallback for empty Home)
 await page.evaluate(()=>{homeSnapshot.items=[];renderHomeSnapshot();});
 const emptyState=await page.evaluate(()=>({startersHidden:document.getElementById('homeStarters').hidden,continuityHidden:document.getElementById('homeContinuity').hidden}));
 assert.deepEqual(emptyState,{startersHidden:true,continuityHidden:true},'empty Home shows honest empty state, not generic pack');
 // Test composer still works when Home is empty
 await page.fill('#homeScoutInput','Whatever I want to ask');
 assert.equal(await page.locator('#homeScoutInput').inputValue(),'Whatever I want to ask');
	 assert.equal(await page.locator('#homeProjectChip').count(),0,'retired Home Project control remains in the DOM');
	 assert.equal(projectContextRequestCount,0,'retired Home Project control requested a preflight');
 await page.focus('#homeScoutInput');
 const focusedComposer=await page.evaluate(()=>({
   border:getComputedStyle(document.getElementById('homeScoutComposer')).borderColor,
   inputBackground:getComputedStyle(document.getElementById('homeScoutInput')).backgroundColor,
   inputOutline:getComputedStyle(document.getElementById('homeScoutInput')).outlineStyle
 }));
 assert.equal(focusedComposer.border,'rgba(0, 0, 0, 0)');
 assert.equal(focusedComposer.inputBackground,'rgba(0, 0, 0, 0)');
 assert.equal(focusedComposer.inputOutline,'none');
 await page.fill('#homeScoutInput','');
 // Test live meeting rendering with empty Home
 await page.evaluate(async()=>{renderHomeStarters();document.activeElement?.blur();await fetch('/__rooms_live');homeLiveSignature='';await loadRoomsList();});
 for(const theme of ['dark','light'])await capture('desktop-home-live-meeting',theme);
 await page.setViewportSize({width:390,height:844});
 await page.waitForTimeout(100);
 for(const theme of ['dark','light'])await capture('phone-home-live-meeting',theme);
 const phone=await page.evaluate(()=>({fits:document.documentElement.scrollWidth<=innerWidth,startersHidden:document.getElementById('homeStarters').hidden,composer:document.getElementById('homeScoutComposer').getBoundingClientRect().toJSON()}));
 assert.equal(phone.fits,true);assert.equal(phone.startersHidden,true,'starters always hidden');assert.ok(phone.composer.left>=0&&phone.composer.right<=390);
 await page.evaluate(async()=>{
	   homeScoutInput.value='Country Golf';
   homeScoutInput.dispatchEvent(new Event('input',{bubbles:true}));
   await new Promise(resolve=>setTimeout(resolve,300));
   await fetch('/__home_account_b');
   selectedHomeSuggestionDestination={route:'thread',threadId:'country-golf'};
   setAuthenticatedUser({email:'b@example.test',name:'B'});
   void loadHomeSnapshot();
 });
 const switched=await page.evaluate(()=>({
   oldCopy:document.querySelector('.office-launch').textContent.includes('Country Golf'),
   hydrated:document.getElementById('homeStarters').dataset.hydrated,
   destination:selectedHomeSuggestionDestination,
   chipHidden:document.getElementById('homeContextChip').hidden,
	   projectChipPresent:Boolean(document.getElementById('homeProjectChip')),
   chooserOpen:document.getElementById('homeProjectChooser').open
 }));
 // During account switch, old context must be cleared; starters are in loading state
	 assert.deepEqual(switched,{oldCopy:false,hydrated:'false',destination:null,chipHidden:true,projectChipPresent:false,chooserOpen:false},'A context survived the A to B authority boundary');
 // Wait for B's home to hydrate - continuity items will be primary, starters hidden
 await page.waitForFunction(()=>document.querySelectorAll('#homeContinuity .home-continuity__row').length===3);
 await page.waitForTimeout(1100);
 assert.equal(await page.locator('.office-launch').textContent().then(value=>value.includes('Country Golf')),false,'late A Project context rendered for B');
	await page.goto(base+'/chat',{waitUntil:'domcontentloaded'});
	await page.waitForSelector('#appShell.is-authed');
	await page.waitForFunction(()=>document.getElementById('appShell').dataset.tool==='chat'&&getComputedStyle(document.getElementById('chatTool')).display!=='none');
	await page.evaluate(()=>{
	  scoutChatThreads=[{id:'country-golf',title:'Country Golf',visibility:'private',ownerEmail:'b@example.test',messages:[]}];
	  selectScoutChatThread('country-golf');
	  setMobileChatView('convo');
	});
		assert.equal(await page.locator('#scoutChatProjectRow').count(),0,'retired thread Project control remains in the DOM');
		assert.equal(projectContextRequestCount,0,'retired thread Project control requested a preflight');
		await page.fill('#scoutChatInput','Keep this turn in brain-owned context.');
		await page.evaluate(()=>document.getElementById('scoutChatForm').requestSubmit());
		await page.waitForTimeout(250);
		assert.equal(chatMutationCount,1,'ordinary Send did not reach the canonical message endpoint once');
		const retryBodies=chatRequestBodies.map(raw=>JSON.parse(raw));
		assert.equal('projectContextToken' in retryBodies[0],false,'retired Project token reached Send');
	const orderedProjectFrames=await page.evaluate(()=>{
	  scoutChatThreads=[{id:'project-order',title:'Project order',updatedAt:'2026-08-12T00:00:02Z',visibility:'private',messages:[
	    {id:'project-user',role:'user',createdAt:'2026-08-12T00:00:00Z',project:{status:'confirmed',projectId:'project-one',projectRevision:1,title:'Launch Plan',basis:'selected'}},
	    {id:'project-reply',role:'scout',createdAt:'2026-08-12T00:00:01Z',reply:{operationId:'operation-one',inReplyTo:'project-user',state:'queued'}}
	  ]}];
	  handleChatThreadEvent({id:'project-order',message:{id:'project-user',role:'user',createdAt:'2026-08-12T00:00:00Z',project:{status:'pending',title:'Launch Plan',basis:'selected'}}});
	  handleChatThreadEvent({id:'project-order',message:{id:'project-reply',role:'scout',createdAt:'2026-08-12T00:00:01Z',reply:{operationId:'operation-one',inReplyTo:'project-user',state:'project_pending'}}});
	  return {project:scoutChatThreads[0].messages[0].project.status,reply:scoutChatThreads[0].messages[1].reply.state};
	});
	assert.deepEqual(orderedProjectFrames,{project:'confirmed',reply:'queued'},'late socket frames regressed the confirmed HTTP Project response');
 await page.evaluate(()=>signOutOfAccount());
 await page.waitForTimeout(100);
	 const signedOutProject=await page.evaluate(()=>({chipPresent:Boolean(document.getElementById('homeProjectChip')),chooserOpen:homeProjectChooser.open,oldCopy:document.querySelector('.office-launch').textContent.includes('Country Golf')}));
	 assert.deepEqual(signedOutProject,{chipPresent:false,chooserOpen:false,oldCopy:false},'Project context survived logout');
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "HOME_INDEX="+indexPath, "HOME_RENDER_DIR="+os.Getenv("HOME_RENDER_DIR"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Home harness: %v\n%s", err, output)
	}
}

func TestDesktopHomeHasNoPermanentToolOrDeliverablePicker(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, `<section id="officeTool"`)
	end := strings.Index(html[start:], `</section>`)
	if start < 0 || end < 0 {
		t.Fatal("Home section missing")
	}
	home := html[start : start+end]
	for _, forbidden := range []string{"toolTemplate", "data-tool-template", "choose a tool", "Morning Brief", "Portfolio Health", "the company brain", "tap to talk to Scout"} {
		if strings.Contains(home, forbidden) {
			t.Fatalf("Home contains superfluous or capability-selecting copy %q", forbidden)
		}
	}
}
