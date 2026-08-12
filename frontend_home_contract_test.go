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
		`homeContinuity.hidden = Boolean(categories.length) || !availableContext`,
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
 {id:'continue',label:'Continue',detail:'Pick up recent work',suggestions:[{id:'continue-1',text:'Continue where we left off in Country Golf.'}]},
 {id:'explore',label:'Explore',detail:'Understand and discover',suggestions:[{id:'explore-1',text:'Explore the biggest open question in Country Golf.'},{id:'explore-2',text:'Discover what we may be missing in Country Golf.'}]},
 {id:'create',label:'Create',detail:'Make the next useful thing',suggestions:[{id:'create-1',text:'Create the next useful deliverable for Country Golf.'}]},
 {id:'challenge',label:'Challenge',detail:'Grill and red-team',suggestions:[{id:'challenge-1',text:'Challenge the current thinking in Country Golf and identify the weakest assumptions.'}]}
]};
let homeAvailable=true;
let roomsMode='quiet';
const server=http.createServer((req,res)=>{
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation);}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ'}));}
 if(req.url==='/assistant/home'){
   if(!homeAvailable){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
   res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({home}));
 }
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
 await page.waitForFunction(()=>document.querySelectorAll('#homeContinuity .home-continuity__row').length===3);
 await page.fill('#homeScoutInput','Unsent local draft');
 homeAvailable=false;
 await page.evaluate(()=>loadHomeSnapshot());
 await page.waitForFunction(()=>!document.getElementById('homeRefreshRetry').hidden);
 const failed=await page.evaluate(()=>({
   continuity:document.querySelectorAll('#homeContinuity .home-continuity__row').length,
   starters:document.querySelectorAll('#homeStarters .home-starter').length,
   draft:document.getElementById('homeScoutInput').value,
   retry:document.getElementById('homeRefreshRetry').textContent.trim()
 }));
	assert.deepEqual(failed,{continuity:0,starters:0,draft:'Unsent local draft',retry:'Home unavailable · Retry'});
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
 await page.focus('#homeScoutInput');
 await page.waitForFunction(()=>document.querySelectorAll('#homeStarters .home-starter').length===4 && document.getElementById('homeContinuity').hidden);
 await page.click('#homeStarters .home-starter:nth-child(2)');
 assert.equal(await page.locator('#homeScoutInput').inputValue(),'');
 assert.equal(await page.locator('#homeSuggestions .home-suggestion').count(),2);
 for(const theme of ['dark','light'])await capture('desktop-home-category-selected',theme);
 await page.setViewportSize({width:390,height:844});
 await page.waitForTimeout(100);
 for(const theme of ['dark','light'])await capture('phone-home-category-selected',theme);
 assert.equal(await page.locator('#homeSuggestions .home-suggestions__back').isVisible(),true);
 await page.click('#homeSuggestions .home-suggestions__back');
 assert.equal(await page.locator('#homeStarters .home-starter:visible').count(),4);
 assert.equal(await page.evaluate(()=>document.activeElement?.getAttribute('data-category-id')),'explore');
 assert.equal(await page.locator('#homeScoutInput').inputValue(),'');
 await page.click('#homeStarters .home-starter:nth-child(2)');
 await page.setViewportSize({width:1440,height:900});
 await page.locator('#homeSuggestions .home-suggestion').first().click();
 assert.equal(await page.locator('#homeScoutInput').inputValue(),'Explore the biggest open question in Country Golf.');
 assert.equal(new URL(page.url()).pathname,'/');
 const focusedComposer=await page.evaluate(()=>({
   border:getComputedStyle(document.getElementById('homeScoutComposer')).borderColor,
   inputBackground:getComputedStyle(document.getElementById('homeScoutInput')).backgroundColor,
   inputOutline:getComputedStyle(document.getElementById('homeScoutInput')).outlineStyle
 }));
 assert.equal(focusedComposer.border,'rgba(0, 0, 0, 0)');
 assert.equal(focusedComposer.inputBackground,'rgba(0, 0, 0, 0)');
 assert.equal(focusedComposer.inputOutline,'none');
 for(const theme of ['dark','light'])await capture('desktop-home-suggestion-populated',theme);
 await page.setViewportSize({width:390,height:844});
 await page.waitForTimeout(100);
 for(const theme of ['dark','light'])await capture('phone-home-suggestion-populated',theme);
 await page.setViewportSize({width:1440,height:900});
 await page.fill('#homeScoutInput','Whatever else I want to ask');
 assert.equal(await page.locator('#homeScoutInput').inputValue(),'Whatever else I want to ask');
 await page.fill('#homeScoutInput','');
 await page.waitForFunction(()=>document.querySelectorAll('#homeStarters .home-starter').length===4);
 await page.evaluate(async()=>{selectedHomeCategoryId='';renderHomeStarters();document.activeElement?.blur();await fetch('/__rooms_live');homeLiveSignature='';await loadRoomsList();});
 for(const theme of ['dark','light'])await capture('desktop-home-live-meeting',theme);
 await page.setViewportSize({width:390,height:844});
 await page.waitForTimeout(100);
 for(const theme of ['dark','light'])await capture('phone-home-live-meeting',theme);
 const phone=await page.evaluate(()=>({fits:document.documentElement.scrollWidth<=innerWidth,starterCount:document.querySelectorAll('#homeStarters .home-starter').length,composer:document.getElementById('homeScoutComposer').getBoundingClientRect().toJSON()}));
 assert.equal(phone.fits,true);assert.equal(phone.starterCount,4);assert.ok(phone.composer.left>=0&&phone.composer.right<=390);
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
