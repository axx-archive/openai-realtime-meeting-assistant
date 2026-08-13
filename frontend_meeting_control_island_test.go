package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopMeetingControlsUseOneCompactInvariantIsland(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	controlsStart := strings.Index(html, `<div class="controls">`)
	controlsEnd := strings.Index(html[controlsStart:], `</footer>`)
	if controlsStart < 0 || controlsEnd < 0 {
		t.Fatal("meeting controls not found")
	}
	controls := html[controlsStart : controlsStart+controlsEnd]
	ordered := []string{`id="muteMic"`, `id="toggleCamera"`, `id="audioSettingsButton"`, `id="screenShare"`, `id="roomMoreToggle"`, `class="controls__divider"`, `id="leave"`}
	last := -1
	for _, token := range ordered {
		at := strings.Index(controls, token)
		if at < 0 || at <= last {
			t.Fatalf("primary meeting control order is not stable at %s", token)
		}
		last = at
	}
	for _, secondary := range []string{"#roomBoardToggle", "#consentToggle", "#roomChatToggle", "#recordMeeting", "#inviteToggle", "#roomScoutQuickAction", "#archiveMeeting"} {
		if !strings.Contains(html, `#appShell.is-in-room[data-tool="room"] .meeting-bar `+secondary) {
			t.Fatalf("secondary action %s is not removed from the primary island", secondary)
		}
	}
	for _, menuItem := range []string{`id="roomMoreChat"`, `id="roomMoreRecap"`, `id="roomMoreTranscript"`, `id="roomMoreRecord"`, `id="roomMorePeople"`, `id="roomMoreSpecialists"`, `id="roomMoreInvite"`, `id="roomMoreBoard"`, `id="roomMoreConsent"`, `id="roomMoreWorkspace"`, `id="roomMoreArchive"`} {
		if !strings.Contains(controls, menuItem) {
			t.Fatalf("More menu does not own %s", menuItem)
		}
	}
	if !strings.Contains(html, `roomMoreMenu?.addEventListener('keydown'`) || !strings.Contains(html, `event.key === 'ArrowDown'`) || !strings.Contains(html, `roomMoreToggleButton?.focus()`) {
		t.Fatal("More menu lacks keyboard traversal or exact focus return")
	}
	if strings.Contains(html, `#appShell.is-guest.is-in-room .room-more`) {
		t.Fatal("guest-safe Chat must remain reachable through More")
	}
	for _, destination := range []string{`roomChatInput.focus()`, `roomMeetingRecapTab?.focus()`, `roomMeetingTranscriptTab?.focus()`, `roomBoardPanel?.focus?.()`, `artifactSearch?.focus()`} {
		if !strings.Contains(html, destination) {
			t.Fatalf("More action lacks an explicit visible focus destination: %s", destination)
		}
		if !strings.Contains(html, `meetingSpecialistsRestoreFocus = roomMoreToggleButton`) || !strings.Contains(html, `target.getClientRects().length`) {
			t.Fatal("Agent team must restore focus to the visible More button, never a hidden menu item")
		}
	}
	if !strings.Contains(html, `roomMoreWorkspaceButton.hidden = Boolean(guestMode)`) || !strings.Contains(html, `roomMoreBoardButton.hidden = Boolean(guestMode)`) {
		t.Fatal("member-only workspace actions are not hidden independently from guest-safe Chat")
	}
}

func TestDesktopMeetingControlIslandRenderedResponsiveFocusAndGuestChat(t *testing.T) {
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
const html=fs.readFileSync(process.env.MEETING_CONTROL_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ'}));}
  if(req.url.startsWith('/participants')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({roomId:'office',participants:['AJ'],occupiedSeats:1,capacity:10,mediaStates:{},endpointMediaStates:{},endpointCounts:{AJ:1},recording:{enabled:true}}));}
  if(req.url==='/api/stride/v1/mobile/surfaces/organizations'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[{id:'membership-current',title:'STRIDE Agentic Lab',status:'current',kind:'organization-summary',detail:{kind:'organization-summary',activeCount:2,capacity:3,pendingCount:0,isCurrent:true,role:'owner'},actions:[]}]}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:900}});
 await page.goto(base+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.waitForTimeout(700);
 await page.evaluate(()=>{for(let timer=1;timer<50000;timer++){clearTimeout(timer);clearInterval(timer)}});
 const renderDir=String(process.env.MEETING_CONTROL_RENDER_DIR||'').trim(); if(renderDir)fs.mkdirSync(renderDir,{recursive:true});
 const capture=async(candidate,theme,open=false)=>{
   await page.setViewportSize({width:candidate.width,height:candidate.height});
   await page.evaluate(next=>{
     renderTheme(next);
     const shell=document.getElementById('appShell');shell.dataset.tool='room';shell.classList.add('is-authed','is-in-room');
     document.querySelector('.meeting-bar').style.display='block';document.getElementById('presentationTile').style.display='grid';
     occupiedSeats=1;participantsInRoom=['AJ'];updateRoomClock();
     const primary=new Set(['muteMic','toggleCamera','audioSettingsButton','screenShare','roomMoreToggle','leave']);
     for(const button of document.querySelectorAll('.meeting-bar .controls button')){button.disabled=false;button.style.display=primary.has(button.id)?'grid':'none';}
     document.querySelector('.meeting-bar .controls__divider').style.display='block';document.querySelector('.meeting-bar .room-more').style.display='block';
   },theme); await page.waitForTimeout(20);
   if(open)await page.evaluate(()=>{const menu=document.getElementById('roomMoreMenu');for(const item of menu.querySelectorAll('button')){item.hidden=false;item.disabled=false;item.style.display='flex'}menu.hidden=false;menu.style.display='grid';document.getElementById('roomMoreToggle').setAttribute('aria-expanded','true');}); else await page.evaluate(()=>{const menu=document.getElementById('roomMoreMenu');menu.hidden=true;menu.style.display='none';document.getElementById('roomMoreToggle').setAttribute('aria-expanded','false')});
   const state=await page.evaluate(()=>{
     const shell=document.getElementById('appShell');shell.dataset.tool='room';shell.classList.add('is-authed','is-in-room');
     document.querySelector('.meeting-bar').style.setProperty('display','flex','important');document.getElementById('presentationTile').style.display='grid';
     const dock=document.querySelector('.meeting-bar .controls').getBoundingClientRect();
     const menu=document.getElementById('roomMoreMenu').getBoundingClientRect();
     return {fits:document.documentElement.scrollWidth<=innerWidth,dock:dock.toJSON(),menu:menu.toJSON(),menuHidden:document.getElementById('roomMoreMenu').hidden,occupancy:document.getElementById('stageOccupancy').textContent,controlCount:Array.from(document.querySelectorAll('.meeting-bar .controls > button,.meeting-bar .room-more > button')).filter(button=>button.getBoundingClientRect().width).length,tool:document.getElementById('appShell').dataset.tool};
   });
   assert.equal(state.fits,true,JSON.stringify(state)); assert.equal(state.occupancy,'in room · 1',JSON.stringify(state)); assert.equal(state.controlCount,6,JSON.stringify(state));
   assert.ok(state.dock.left>=0&&state.dock.right<=candidate.width&&state.dock.top>=0&&state.dock.bottom<=candidate.height,JSON.stringify(state));
   if(open){assert.ok(state.menu.width>=200&&state.menu.height>=44,JSON.stringify(state));assert.ok(state.menu.left>=0&&state.menu.right<=candidate.width&&state.menu.top>=0&&state.menu.bottom<=candidate.height,JSON.stringify(state));}
   if(renderDir){await page.evaluate(openNow=>{const shell=document.getElementById('appShell');shell.dataset.tool='room';shell.classList.add('is-authed','is-in-room');document.querySelector('.meeting-bar').style.setProperty('display','flex','important');document.getElementById('presentationTile').style.display='grid';document.getElementById('mobilePrimaryNav')?.style.setProperty('display','none','important');const menu=document.getElementById('roomMoreMenu');menu.hidden=!openNow;menu.style.display=openNow?'grid':'none'},open);await page.screenshot({path:path.join(renderDir,candidate.name+'-'+theme+(open?'-menu':'')+'.png')});}
 };
 await capture({name:'desktop-1440',width:1440,height:900},'light',false);
 await capture({name:'desktop-1440',width:1440,height:900},'light',true);
 await capture({name:'desktop-768',width:768,height:900},'light',false);
 await capture({name:'desktop-320',width:320,height:700},'dark',false);
 await capture({name:'desktop-320',width:320,height:700},'dark',true);
 await page.setViewportSize({width:1280,height:800}); await page.evaluate(()=>{renderTheme('dark');const shell=document.getElementById('appShell');shell.dataset.tool='room';shell.classList.add('is-authed','is-in-room');document.querySelector('.meeting-bar').style.display='block';document.querySelector('.meeting-bar .controls').style.display='inline-flex';document.getElementById('roomChatInput').disabled=false;document.getElementById('roomMoreMenu').hidden=false;document.getElementById('roomMoreMenu').style.display='grid';});
 await page.evaluate(()=>{document.getElementById('appShell').classList.add('is-in-room');document.getElementById('roomMoreRecap').click()}); await page.waitForFunction(()=>document.activeElement?.id==='roomMeetingRecapTab');
 await page.evaluate(()=>{document.getElementById('appShell').classList.add('is-in-room');setRoomMoreOpen(true);document.getElementById('roomMoreTranscript').click()}); await page.waitForFunction(()=>document.activeElement?.id==='roomMeetingTranscriptTab');
 await page.evaluate(()=>{document.getElementById('appShell').classList.add('is-in-room');document.getElementById('roomChatInput').disabled=false;setRoomMoreOpen(true);document.getElementById('roomMoreChat').click()}); await page.waitForFunction(()=>document.activeElement?.id==='roomChatInput');
 await page.evaluate(()=>setRoomMoreOpen(true)); await page.waitForTimeout(1); await page.evaluate(()=>document.getElementById('roomMoreSpecialists').click()); await page.waitForFunction(()=>!document.getElementById('meetingSpecialistsPanel').hidden);
 await page.evaluate(()=>document.getElementById('meetingSpecialistsClose').click()); assert.equal(await page.evaluate(()=>document.activeElement?.id),'roomMoreToggle');
 await page.evaluate(()=>{guestMode=true;document.getElementById('appShell').classList.add('is-guest');syncRoomMoreActions();setRoomMoreOpen(true)});
 const guest=await page.locator('#roomMoreMenu button').evaluateAll(items=>items.filter(item=>!item.hidden).map(item=>item.textContent.trim()));
 assert.ok(guest.includes('Chat')); assert.ok(!guest.includes('Agent team')&&!guest.includes('Board')&&!guest.includes('Open advanced workspace')&&!guest.includes('Invite people')&&!guest.includes('Send notes'),guest.join(','));
 await page.evaluate(()=>{document.getElementById('roomChatInput').disabled=false;document.getElementById('roomMoreChat').click()}); await page.waitForFunction(()=>document.activeElement?.id==='roomChatInput');
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "MEETING_CONTROL_INDEX="+indexPath, "MEETING_CONTROL_RENDER_DIR="+os.Getenv("MEETING_CONTROL_RENDER_DIR"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered meeting-control harness: %v\n%s", err, output)
	}
}
