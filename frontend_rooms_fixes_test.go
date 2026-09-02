package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Rooms in-call audit fixes (R1-R8), one pin per fix. Each names the exact
// seam a regression would have to remove:
//   R1 share seats + key→source tags clear on reconnect / leave, and a departed
//      participant's keys untag (a reused stream id classifies fresh)
//   R2 leaveRoom sends screen_share_stopped on the live socket BEFORE ws drops
//   R3 the body-portaled tile host menu dies with the tile; focus lands live
//   R4 jumpToMemoryMeeting resolves off a macrotask, never rAF
//   R5 handleScreenShareStopped gates the holder with roomNamesMatch
//   R6 a superseded recording failure never toasts "Your call continues"
//   R7 the reaction-row hand item is a menuitemcheckbox (aria-checked)
//   R8 phones get Lock/Unlock room in the More menu (managers only)

func readIndexHTMLForRoomsFixes(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(body)
}

func requireAllRoomsFixes(t *testing.T, haystack, where string, wants ...string) {
	t.Helper()
	if haystack == "" {
		t.Fatalf("%s could not be extracted", where)
	}
	for _, want := range wants {
		if !strings.Contains(haystack, want) {
			t.Errorf("%s is missing %q", where, want)
		}
	}
}

func TestIndexRoomsFixR2LeaveSendsScreenShareStoppedBeforeSocketDrops(t *testing.T) {
	html := readIndexHTMLForRoomsFixes(t)
	leave := functionBody(html, "function leaveRoom()")
	requireAllRoomsFixes(t, leave, "leaveRoom",
		"if (screenShareStream && ws?.readyState === WebSocket.OPEN) {",
		"ws.send(JSON.stringify({ event: 'screen_share_stopped', data: '{}' }))",
		"stopScreenShare().catch(() => {})",
	)
	sendAt := strings.Index(leave, "ws.send(JSON.stringify({ event: 'screen_share_stopped', data: '{}' }))")
	stopAt := strings.Index(leave, "stopScreenShare().catch(() => {})")
	dropAt := strings.Index(leave, "ws = undefined")
	if sendAt < 0 || stopAt < 0 || dropAt < 0 || !(sendAt < stopAt && stopAt < dropAt) {
		t.Errorf("the synchronous screen_share_stopped send must precede the async stopScreenShare and the ws drop (send=%d stop=%d drop=%d)", sendAt, stopAt, dropAt)
	}
	// the non-leave path keeps its own send after the detach
	stop := functionBody(html, "async function stopScreenShare()")
	requireAllRoomsFixes(t, stop, "stopScreenShare", "await detachScreenShareFromOwnUplink(pc)", "if (ws?.readyState === WebSocket.OPEN) {\n          ws.send(JSON.stringify({ event: 'screen_share_stopped', data: '{}' }))")
}

func TestIndexRoomsFixR1ShareMapsClearOnTeardownAndUntagOnDeparture(t *testing.T) {
	html := readIndexHTMLForRoomsFixes(t)
	for _, name := range []string{"function clearRemoteMediaForReconnect()", "function leaveRoom()"} {
		requireAllRoomsFixes(t, functionBody(html, name), name, "remoteShareByParticipant.clear()", "remoteShareKeysByParticipant.clear()", "remoteTrackSourcesByKey.clear()")
	}
	// the departure untag must not depend on the seat still existing: a prior
	// screen_share_stopped deletes the seat but leaves its keys tagged, so
	// every 'screen' tag is remembered by name and dropped from there
	remove := functionBodyAfterSignature(html, "function removeRemoteShareMediaByName(name, options = {})")
	requireAllRoomsFixes(t, remove, "removeRemoteShareMediaByName", "if (options.untagKeys) untagRemoteShareKeysForName(participantName)")
	if strings.Index(remove, "untagRemoteShareKeysForName(participantName)") < strings.LastIndex(remove, "removed = true") {
		t.Error("the departure untag must run after (outside) the seat loop — the seat is usually already gone")
	}
	requireAllRoomsFixes(t, functionBody(html, "function untagRemoteShareKeysForName(name)"), "untagRemoteShareKeysForName",
		"for (const tagged of set) remoteTrackSourcesByKey.delete(tagged)", "remoteShareKeysByParticipant.delete(key)")
	for _, site := range []string{
		"for (const key of entry.keys) remoteTrackSourcesByKey.set(key, 'screen')\n        rememberRemoteShareKeys(participantName, entry.keys)",
		"remoteTrackSourcesByKey.set(key, 'screen')\n        rememberRemoteShareKeys(participantName, [key])",
		"for (const key of normalized) remoteTrackSourcesByKey.set(key, 'screen')\n        rememberRemoteShareKeys(name, normalized)",
		"if (trackSource === 'screen') rememberRemoteShareKeys(name, trackKeys)",
	} {
		if !strings.Contains(html, site) {
			t.Errorf("a 'screen' tag site does not remember its keys by name: %q", site)
		}
	}
	// departure paths untag; a plain screen_share_stopped keeps the paused
	// pair tagged so the repair loops keep skipping it
	requireAllRoomsFixes(t, functionBody(html, "function removeRemoteParticipantMediaByName(name)"), "removeRemoteParticipantMediaByName",
		"removeRemoteShareMediaByName(participantName, { untagKeys: true })")
	stopped := functionBody(html, "function handleScreenShareStopped(event)")
	requireAllRoomsFixes(t, stopped, "handleScreenShareStopped", "if (name) removeRemoteShareMediaByName(name)\n")
	if strings.Contains(stopped, "untagKeys") {
		t.Error("screen_share_stopped must not untag the paused share pair (restart resumes the same forwarded tracks)")
	}
}

func TestIndexRoomsFixR3TileHostMenuDiesWithTheTile(t *testing.T) {
	html := readIndexHTMLForRoomsFixes(t)
	for _, name := range []string{"function disposeRemoteTile(tile)", "function forgetRemoteTile(tile)"} {
		requireAllRoomsFixes(t, functionBody(html, name), name, "destroyTileHostMenu(tile)")
	}
	dispose := functionBody(html, "function disposeRemoteTile(tile)")
	if strings.Index(dispose, "destroyTileHostMenu(tile)") > strings.Index(dispose, "tile.remove()") {
		t.Error("disposeRemoteTile must destroy the host menu BEFORE tile.remove() (the trigger must still be findable)")
	}
	// placeholder seats carry the menu too — every placeholder teardown
	// (promotion to media, reconcile prune, reconnect / leave sweeps) runs
	// the same destroy; the bare remove() there was the live orphan path
	requireAllRoomsFixes(t, functionBody(html, "function removeParticipantPlaceholder(name)"), "removeParticipantPlaceholder", "destroyTileHostMenu(tile)\n        tile.remove()")
	if strings.Count(html, "participantPlaceholders.forEach(tile => { destroyTileHostMenu(tile); tile.remove() })") != 2 {
		t.Error("the reconnect and leave placeholder sweeps must destroy each seat's host menu before removing it")
	}
	if !strings.Contains(html, "destroyTileHostMenu(tile)\n            tile.remove()\n            participantPlaceholders.delete(name)") {
		t.Error("the placeholder reconcile prune must destroy the host menu before the bare remove()")
	}
	destroy := functionBody(html, "function destroyTileHostMenu(tile)")
	requireAllRoomsFixes(t, destroy, "destroyTileHostMenu",
		"tile?.querySelector?.('.tile-host-menu')",
		"control?.destroy?.()",
		"roomHostMenus.delete(button)",
		"button.remove()",
		".meeting-bar .controls button:not([hidden]):not([disabled])",
		".find(node => node.getClientRects().length > 0)",
		"island?.focus?.({ preventScroll: true })",
	)
}

func TestIndexRoomsFixR4JumpToMemoryMeetingNeverGatesOnAnimationFrame(t *testing.T) {
	html := readIndexHTMLForRoomsFixes(t)
	body := functionBody(html, "function jumpToMemoryMeeting(meetingId)")
	requireAllRoomsFixes(t, body, "jumpToMemoryMeeting",
		"return new Promise(resolve => window.setTimeout(() => {",
		"resolve(false)",
		"resolve(true)",
		"}, 0))",
	)
	if strings.Contains(body, "requestAnimationFrame") {
		t.Error("jumpToMemoryMeeting must not use requestAnimationFrame: it never fires in a hidden tab and openPermanentMeetingRecordFromRoom awaits this result")
	}
}

func TestIndexRoomsFixR5ScreenShareStoppedMatchesHolderCaseInsensitively(t *testing.T) {
	html := readIndexHTMLForRoomsFixes(t)
	body := functionBody(html, "function handleScreenShareStopped(event)")
	requireAllRoomsFixes(t, body, "handleScreenShareStopped",
		"if (!name || roomNamesMatch(name, activeScreenShareParticipant)) {",
		"activeScreenShareParticipant = ''",
	)
	if strings.Contains(body, "name === activeScreenShareParticipant") {
		t.Error("the holder compare must ride roomNamesMatch, not a case-sensitive ===")
	}
}

func TestIndexRoomsFixR6SupersededRecordingFailureNeverToastsInTheLobby(t *testing.T) {
	html := readIndexHTMLForRoomsFixes(t)
	body := functionBody(html, "function roomMediaRecordingFail(state, copy)")
	requireAllRoomsFixes(t, body, "roomMediaRecordingFail",
		"const current = roomMediaRecording === state",
		"if (current) stopRoomMediaRecording(state.failed, { failed: true })",
		"if (!current) {",
		"setLog(`earlier recording segment not uploaded: ${state.failed}`)",
		"if (appShell.classList.contains('is-in-room')) {",
		"showToast({ text: 'an earlier recording segment could not be uploaded', kind: 'note' })",
		"Your call continues.",
		"roomMediaRecordingConsentPrompt(state.failed)",
	)
	staleAt := strings.Index(body, "if (!current) {")
	returnAt := strings.Index(body[staleAt:], "return\n")
	liveAt := strings.Index(body, "Your call continues.")
	if staleAt < 0 || returnAt < 0 || liveAt < 0 || staleAt+returnAt > liveAt {
		t.Error("the superseded branch must return before the live-run toast / consent prompt")
	}
}

func TestIndexRoomsFixR8PhoneMoreMenuCarriesLockRoom(t *testing.T) {
	html := readIndexHTMLForRoomsFixes(t)
	requireAllRoomsFixes(t, html, "More menu markup",
		`<button id="roomMoreLock" class="room-more__action room-more__action--manager" type="button" role="menuitem" hidden>Lock room</button>`,
		"const roomMoreLockButton = document.getElementById('roomMoreLock')",
		"roomMoreLockButton?.addEventListener('click', () => {\n        setRoomMoreOpen(false)\n        sendRoomModerate(roomLocked ? 'unlock' : 'lock')\n      })",
	)
	sync := functionBody(html, "function syncRoomMoreLockAction(inRoom = appShell.classList.contains('is-in-room'))")
	requireAllRoomsFixes(t, sync, "syncRoomMoreLockAction",
		"roomHostControlsAvailable()",
		"roomMoreLockButton.hidden = !manager",
		"roomMoreLockButton.disabled = !manager || !roomSocketOpen()",
		"roomMoreLockButton.textContent = roomLocked ? 'Unlock room' : 'Lock room'",
	)
	requireAllRoomsFixes(t, functionBody(html, "function syncRoomMoreActions()"), "syncRoomMoreActions", "syncRoomMoreLockAction(inRoom)")
	requireAllRoomsFixes(t, functionBody(html, "function syncRoomInCallControls()"), "syncRoomInCallControls", "syncRoomMoreLockAction(inRoom)")
	// the island disc stays on desktop; phones fold it (the More item is its seat there)
	if !strings.Contains(html, "#appShell.is-in-room[data-tool=\"room\"] .meeting-bar .controls #roomLockToggle {\n          display: none;\n        }") {
		t.Error("the phone media query must still fold the island lock disc (the More item is its phone seat)")
	}
	if !strings.Contains(html, "roomLockToggleButton.addEventListener('click', () => sendRoomModerate(roomLocked ? 'unlock' : 'lock'))") {
		t.Error("the desktop lock disc must keep its own room_moderate wiring")
	}
}

func TestIndexRoomsFixR7ReactionRowHandIsAMenuItemCheckbox(t *testing.T) {
	html := readIndexHTMLForRoomsFixes(t)
	menu := functionBody(html, "function ensureRoomReactionMenu()")
	requireAllRoomsFixes(t, menu, "ensureRoomReactionMenu",
		"item.setAttribute('role', 'menuitemcheckbox')",
		"item.setAttribute('aria-checked', roomHandRaisedSelf() ? 'true' : 'false')",
	)
	sync := functionBody(html, "function syncRoomInCallControls()")
	requireAllRoomsFixes(t, sync, "syncRoomInCallControls", "rowHand.setAttribute('aria-checked', raised ? 'true' : 'false')")
	if strings.Contains(sync, "rowHand.setAttribute('aria-pressed'") {
		t.Error("aria-pressed is inert on a menu item — the hand row must carry aria-checked")
	}
	if !strings.Contains(html, `.bf-menu--horizontal.room-react__menu .bf-menu__item[aria-checked="true"] { background: var(--well); }`) {
		t.Error("the raised-hand row style must key off aria-checked")
	}
	// bfMenu's keyboard walk already includes the checkbox role
	if !strings.Contains(functionBodyAfterSignature(html, "function bfMenu(trigger, options = {})"), `[role="menuitemcheckbox"]`) {
		t.Error("bfMenu's item selector must walk menuitemcheckbox rows")
	}
}

// Rendered pin for R3 + R8: after a dispose no orphan host menu remains on
// body, an OPEN menu's focus lands on the control island, and the More-menu
// lock item follows manager state + the locked label.
//
// Flake history (2/10 "open() must focus into the menu"): the false half was
// control.isOpen(), not the focus — at +30 ms the seeded TILE itself was gone
// (tile and menu disconnected, focus correctly moved to #muteMic). The app's
// own dead-tile reaper (repairRemoteMediaHealth → pruneDeadRemoteVideoTiles,
// which disposes any remote tile whose <video> carries no live track) had
// reaped the bare seeded tile. bfMenu.open() focuses synchronously and the
// menu never closes on its own focus moves; the harness raced the reaper, not
// layout. So the seeded tile now carries a live canvas-capture video track,
// and the fixed sleep is a deterministic wait on the un-hidden menu plus one
// layout read.
func TestRoomsFixesRenderedHostMenuDisposalAndMoreLock(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not available")
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
const html=fs.readFileSync(process.env.ROOMS_FIXES_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ'}));}
  if(req.url.startsWith('/participants')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({roomId:'office',participants:['AJ'],occupiedSeats:1,capacity:10,mediaStates:{},endpointMediaStates:{},endpointCounts:{AJ:1},recording:{enabled:true}}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:800}});
 const errors=[]; page.on('pageerror',e=>errors.push(String(e)));
 await page.goto(base+'/video',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.waitForFunction(()=>document.getElementById('appShell')?.dataset.tool==='room');
 await page.waitForTimeout(300);
 const r3=await page.evaluate(async()=>{
   authedUser={email:'aj@shareability.com',name:'AJ'};guestMode=false;currentParticipantName='AJ';
   const shell=document.getElementById('appShell');shell.dataset.tool='room';shell.classList.add('is-authed','is-in-room');
   document.querySelector('.meeting-bar').style.display='block';
   const mute=document.getElementById('muteMic');mute.disabled=false;mute.hidden=false;mute.style.display='grid';
   const hostMenus=()=>Array.from(document.querySelectorAll('body > .bf-menu')).filter(m=>/^Host controls for/.test(m.getAttribute('aria-label')||'')).length;
   const seat=()=>{const tile=document.createElement('div');tile.className='video-tile';tile.dataset.participant='Joel';tile.dataset.remoteKey='k-joel';tile.dataset.remoteKeys='["k-joel"]';const video=document.createElement('video');video.srcObject=document.createElement('canvas').captureStream();tile.appendChild(video);videoStack.appendChild(tile);remoteElements.set('k-joel',tile);ensureTileHostMenu(tile,'Joel');return tile;};
   const tile=seat();
   const seated=hostMenus();
   const button=tile.querySelector('.tile-host-menu');
   const control=roomHostMenus.get(button);control.open();
   // deterministic: open() un-hides synchronously; wait on that (never a
   // fixed sleep the dead-tile reaper could land inside) and read layout once
   const menu=Array.from(document.querySelectorAll('body > .bf-menu')).find(m=>/^Host controls for/.test(m.getAttribute('aria-label')||''));
   for(let i=0;i<50&&menu.hidden;i++) await new Promise(r=>setTimeout(r,0));
   menu.getBoundingClientRect();
   // the contract is "focus INSIDE an open menu lands live after dispose" —
   // pin it on an explicit item focus, not on bfMenu's deferred focus timer
   control.items()[0].focus();
   const focusInMenu=control.isOpen()&&Boolean(document.activeElement?.closest('.bf-menu'));
   disposeRemoteTile(tile);forgetRemoteTile(tile);
   const afterDispose={menus:hostMenus(),tileConnected:tile.isConnected,buttonConnected:button.isConnected,activeInIsland:Boolean(document.activeElement?.closest('.meeting-bar .controls')),active:document.activeElement?.id||document.activeElement?.tagName};
   for(let i=0;i<5;i++){const churn=seat();disposeRemoteTile(churn);forgetRemoteTile(churn);}
   const afterChurn=hostMenus();
   return {seated,focusInMenu,afterDispose,afterChurn};
 });
 assert.equal(r3.seated,1,'one host menu per remote tile: '+JSON.stringify(r3));
 assert.equal(r3.focusInMenu,true,'open() must focus into the menu: '+JSON.stringify(r3));
 assert.equal(r3.afterDispose.menus,0,'disposeRemoteTile must destroy the portaled host menu: '+JSON.stringify(r3));
 assert.equal(r3.afterDispose.buttonConnected,false,JSON.stringify(r3));
 assert.equal(r3.afterDispose.activeInIsland,true,'closing an open menu on dispose must land focus on the control island: '+JSON.stringify(r3));
 assert.equal(r3.afterChurn,0,'tile churn must not accumulate orphan .bf-menu nodes: '+JSON.stringify(r3));
 const r8=await page.evaluate(()=>{
   ws={readyState:WebSocket.OPEN};roomLocked=false;guestMode=false;
   syncRoomMoreActions();
   const item=document.getElementById('roomMoreLock');
   const manager={hidden:item.hidden,disabled:item.disabled,text:item.textContent.trim(),role:item.getAttribute('role')};
   roomLocked=true;syncRoomInCallControls();
   const locked={text:item.textContent.trim(),disc:document.getElementById('roomLockToggle').getAttribute('aria-label')};
   guestMode=true;syncRoomMoreActions();
   const guest={hidden:item.hidden};
   guestMode=false;roomLocked=false;
   return {manager,locked,guest};
 });
 assert.deepEqual(r8.manager,{hidden:false,disabled:false,text:'Lock room',role:'menuitem'},JSON.stringify(r8));
 assert.deepEqual(r8.locked,{text:'Unlock room',disc:'Unlock room'},JSON.stringify(r8));
 assert.equal(r8.guest.hidden,true,'non-managers never see the lock item: '+JSON.stringify(r8));
 assert.deepEqual(errors,[],'page errors: '+errors.join(' | '));
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "ROOMS_FIXES_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered rooms-fixes harness: %v\n%s", err, output)
	}
}
