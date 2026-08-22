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
	for _, secondary := range []string{"#consentToggle", "#roomChatToggle", "#recordMeeting", "#inviteToggle", "#roomScoutQuickAction", "#archiveMeeting"} {
		if !strings.Contains(html, `#appShell.is-in-room[data-tool="room"] .meeting-bar `+secondary) {
			t.Fatalf("secondary action %s is not removed from the primary island", secondary)
		}
	}
	for _, menuItem := range []string{`id="roomMoreChat"`, `id="roomMorePeople"`, `id="roomMoreSettings"`} {
		if !strings.Contains(controls, menuItem) {
			t.Fatalf("More menu does not own %s", menuItem)
		}
	}
	for _, removed := range []string{`id="roomMoreRecap"`, `id="roomMoreTranscript"`, `id="roomMoreRecord"`, `id="roomMoreWorkspace"`, `id="roomMoreSpecialists"`, `id="roomMoreInvite"`, `id="roomMoreConsent"`, `id="roomMoreArchive"`, `Open advanced workspace`} {
		if strings.Contains(controls, removed) {
			t.Fatalf("redundant or product-language More action remains: %s", removed)
		}
	}
	if strings.Count(controls, `class="room-more__action"`) != 3 {
		t.Fatal("the live More hierarchy must stay bounded to three immediate actions")
	}
	if !strings.Contains(html, `roomMoreMenu?.addEventListener('keydown'`) || !strings.Contains(html, `event.key === 'ArrowDown'`) || !strings.Contains(html, `roomMoreToggleButton?.focus()`) {
		t.Fatal("More menu lacks keyboard traversal or exact focus return")
	}
	if strings.Contains(html, `#appShell.is-guest.is-in-room .room-more`) {
		t.Fatal("guest-safe Chat must remain reachable through More")
	}
	if !strings.Contains(html, `roomChatInput.focus()`) {
		t.Fatal("Chat action lacks an explicit visible focus destination")
	}
	if !strings.Contains(html, `meetingSpecialistsRestoreFocus = roomMoreToggleButton`) || !strings.Contains(html, `target.getClientRects().length`) {
		t.Fatal("Agent team must restore focus to the visible More button, never a hidden menu item")
	}
	for _, nestedCapability := range []string{`Bring someone into this room`, `Scout and specialist participants`, `roomMeetingRecapToolbar()`, `Microphone privacy`} {
		if !strings.Contains(html, nestedCapability) {
			t.Fatalf("removed top-level action is orphaned instead of moving to a contextual surface: %s", nestedCapability)
		}
	}
	for _, retired := range []string{`id="roomBoardToggle"`, `id="roomBoardPanel"`, `id="roomMoreBoard"`, `id="toolBoard"`} {
		if strings.Contains(html, retired) {
			t.Fatalf("retired Board control remains mounted: %s", retired)
		}
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
 await page.goto(base+'/video',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.waitForFunction(()=>{const shell=document.getElementById('appShell');return shell?.dataset.tool==='room'&&shell?.dataset.pd1Destination==='Video'});
 await page.waitForLoadState('networkidle');
 await page.waitForTimeout(400);
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
	   if(open)await page.evaluate(()=>{syncRoomMoreActions();const menu=document.getElementById('roomMoreMenu');for(const item of menu.querySelectorAll('button'))item.style.display=item.hidden?'none':'flex';menu.hidden=false;menu.style.display='grid';document.getElementById('roomMoreToggle').setAttribute('aria-expanded','true');}); else await page.evaluate(()=>{const menu=document.getElementById('roomMoreMenu');menu.hidden=true;menu.style.display='none';document.getElementById('roomMoreToggle').setAttribute('aria-expanded','false')});
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
	 assert.equal(await page.locator('#roomMoreBoard').count(),0,'retired Board action remains mounted in the meeting menu');
	 for(const retired of ['roomMoreRecap','roomMoreTranscript','roomMoreRecord','roomMoreWorkspace','roomMoreSpecialists','roomMoreInvite','roomMoreConsent','roomMoreArchive'])assert.equal(await page.locator('#'+retired).count(),0,retired+' remains mounted in More');
 await page.setViewportSize({width:1280,height:800}); await page.evaluate(()=>{renderTheme('dark');const shell=document.getElementById('appShell');shell.dataset.tool='room';shell.classList.add('is-authed','is-in-room');document.querySelector('.meeting-bar').style.display='block';document.querySelector('.meeting-bar .controls').style.display='inline-flex';document.getElementById('roomChatInput').disabled=false;document.getElementById('roomMoreMenu').hidden=false;document.getElementById('roomMoreMenu').style.display='grid';});
 await page.evaluate(()=>{document.getElementById('appShell').classList.add('is-in-room');ws={readyState:WebSocket.OPEN};updateRoomChatAvailability();setRoomMoreOpen(true);document.getElementById('roomMoreChat').click()}); await page.waitForFunction(()=>document.activeElement?.id==='roomChatInput');
	 await page.evaluate(()=>document.getElementById('roomMeetingRecapTab').click()); assert.equal(await page.evaluate(()=>roomMeetingMode),'recap');
	 await page.evaluate(()=>document.getElementById('roomMeetingTranscriptTab').click()); assert.equal(await page.evaluate(()=>roomMeetingMode),'transcript'); assert.equal(await page.locator('#roomMeetingTranscript > .room-meeting-transcription-toolbar').count(),1);
	 const unqualified=await page.evaluate(()=>{meetingSpecialistsRoomId=activeJoin.roomId||'office';meetingSpecialistsSnapshot={available:false};roomScoutVoiceAvailability={enabled:false,reason:'quality_gate_pending'};roomAgentParticipants=[];openRoomPeoplePopover();return {manage:Array.from(document.querySelectorAll('.invite-pop__action')).some(button=>button.textContent.trim()==='Manage'),chatHidden:document.getElementById('roomMoreChat').hidden}});
	 assert.deepEqual(unqualified,{manage:false,chatHidden:false},'unqualified agent controls must disappear inside People while meeting chat stays available');
	 await page.evaluate(()=>{closeInvitePopover();meetingSpecialistsSnapshot={available:true};openRoomPeoplePopover();Array.from(document.querySelectorAll('.invite-pop__action')).find(button=>button.textContent.trim()==='Manage').click()}); await page.waitForFunction(()=>!document.getElementById('meetingSpecialistsPanel').hidden);
 await page.evaluate(()=>document.getElementById('meetingSpecialistsClose').click()); assert.equal(await page.evaluate(()=>document.activeElement?.id),'roomMoreToggle');
	 const peopleActions=await page.evaluate(()=>{meetingSpecialistsRoomId=activeJoin.roomId||'office';meetingSpecialistsSnapshot={available:true};openRoomPeoplePopover();return Array.from(document.querySelectorAll('.invite-pop__action')).map(button=>button.textContent.trim())}); assert.deepEqual(peopleActions,['Invite','Manage']);
	 await page.evaluate(()=>Array.from(document.querySelectorAll('.invite-pop__action')).find(button=>button.textContent.trim()==='Invite').click());
	 const inviteActions=await page.locator('.invite-pop__action').allTextContents(); assert.deepEqual(inviteActions.map(value=>value.trim()),['copy room link','mint guest link']); await page.evaluate(()=>closeInvitePopover());
	 // Room settings remains a truthful action while the transient dock-level
	 // device button is locked during media reconciliation.
	 await page.evaluate(()=>{document.getElementById('audioSettingsButton').disabled=true;syncRoomMoreActions();setRoomMoreOpen(true);document.getElementById('roomMoreSettings').click()}); await page.locator('#settingsClose').waitFor({state:'visible'});
	 // A late auth/path reconciliation of the already-current Video destination
	 // must not erase the newer, deliberate Room settings interaction.
	 await page.evaluate(()=>syncAuthenticatedShell());
	 assert.equal(await page.locator('#settingsClose').isVisible(),true,'same-destination reconciliation closed Room settings');
	 assert.equal(await page.locator('section[data-settings-section="devices"]').evaluate(section=>section.hidden),false); await page.locator('#settingsClose').click(); assert.equal(await page.evaluate(()=>document.activeElement?.id),'roomMoreToggle');
 await page.evaluate(()=>{guestMode=true;const shell=document.getElementById('appShell');shell.dataset.tool='room';shell.classList.add('is-guest','is-in-room');syncRoomMoreActions();setRoomMoreOpen(true)});
 const guest=await page.locator('#roomMoreMenu button').evaluateAll(items=>items.filter(item=>!item.hidden).map(item=>item.textContent.trim()));
	 assert.deepEqual(guest,['Chat','People','Microphone privacy'],guest.join(','));
	 await page.evaluate(()=>{const consent=document.getElementById('consentToggle');consent.hidden=false;consent.disabled=false;syncRoomMoreActions();setRoomMoreOpen(true);document.getElementById('roomMoreSettings').click()}); await page.waitForFunction(()=>!document.getElementById('consentPanel').hidden&&document.activeElement?.id==='consentClose');
	 await page.locator('#consentClose').click(); assert.equal(await page.evaluate(()=>document.activeElement?.id),'roomMoreToggle');
	 await page.evaluate(()=>{const shell=document.getElementById('appShell');shell.dataset.tool='room';shell.classList.add('is-in-room');ws={readyState:WebSocket.OPEN};updateRoomChatAvailability();syncRoomMoreActions();document.getElementById('roomMoreChat').click()});
	 await page.waitForTimeout(50);
	 const guestChatFocus=await page.evaluate(()=>{const input=document.getElementById('roomChatInput');const panel=document.getElementById('roomChatPanel');const rail=document.querySelector('.scout-rail');return {active:document.activeElement?.id||document.activeElement?.tagName||'',disabled:input.disabled,panelHidden:panel.hidden,roomChatOpen:document.getElementById('appShell').classList.contains('is-room-chat-open'),mode:roomMeetingMode,railDisplay:getComputedStyle(rail).display,inputDisplay:getComputedStyle(input).display,inputRects:input.getClientRects().length};});
	 assert.equal(guestChatFocus.active,'roomChatInput',JSON.stringify(guestChatFocus));
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "MEETING_CONTROL_INDEX="+indexPath, "MEETING_CONTROL_RENDER_DIR="+os.Getenv("MEETING_CONTROL_RENDER_DIR"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered meeting-control harness: %v\n%s", err, output)
	}
}
