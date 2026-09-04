package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveTranscriptionPillOwnsStatusAndDestination(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`id="roomTranscriptPill" class="room-transcription-pill"`,
		`aria-controls="roomMeetingTranscript"`,
		`id="roomMeetingTranscript" class="room-meeting-view room-meeting-transcript" role="tabpanel" aria-labelledby="roomMeetingTranscriptTab" tabindex="-1"`,
		`.room-transcription-pill[data-state="live"]`,
		`.room-transcription-pill[data-state="paused"]`,
		`.room-transcription-pill[data-state="unavailable"]`,
		`roomTranscriptPill?.addEventListener('click', openLiveMeetingTranscript)`,
		`function roomMeetingTranscriptToolbar()`,
		`className = 'room-meeting-transcription-action'`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("live transcription surface is missing %q", want)
		}
	}
	openBody := functionBody(html, "function openLiveMeetingTranscript()")
	for _, want := range []string{"setActiveTool('room')", "setRoomChatOpen(true)", "setRoomMeetingMode('transcript')", "roomMeetingTranscript?.focus({ preventScroll: true })"} {
		if !strings.Contains(openBody, want) {
			t.Errorf("openLiveMeetingTranscript is missing %q", want)
		}
	}
	applyBody := functionBody(html, "function applyRoomRecordingState(recording, roomId = '')")
	for _, want := range []string{"roomRecordingAuthorityIsCurrent", "roomRecordingRevision = order.revision", "roomRecordingStatusRevision = order.statusRevision", "roomRecordingConnected = recording.connected === true", "roomRecordingSocketConfirmed = Boolean(ws", "roomRecordingPendingDesired === roomRecordingEnabled"} {
		if !strings.Contains(applyBody, want) {
			t.Errorf("authoritative recording reducer is missing %q", want)
		}
	}
	toggleBody := functionBody(html, "function toggleRoomRecording()")
	if strings.Contains(toggleBody, "roomRecordingEnabled = !roomRecordingEnabled") {
		t.Fatal("recording toggle must not optimistically claim the requested state")
	}
	for _, want := range []string{"const desired = !roomRecordingEnabled", "roomRecordingPendingDesired = desired", "roomRecordingPendingRevision = roomRecordingRevision", "transcription acknowledgement timeout"} {
		if !strings.Contains(toggleBody, want) {
			t.Errorf("recording acknowledgement flow is missing %q", want)
		}
	}
	if !strings.Contains(html, "if (reconnect) resetRoomRecordingAuthority(activeJoin.roomId || 'office')") {
		t.Fatal("a reconnected socket must clear the prior server generation before accepting its first recording snapshot")
	}
	if strings.Contains(functionBody(html, "function phoneToolSubtitle(tool)"), "roomRecordingEnabled ? 'listening'") {
		t.Fatal("the phone subtitle must not duplicate transcription state outside the pill")
	}
	for _, redundant := range []string{`id="roomMoreTranscript"`, `id="roomMoreRecord"`, `>Live transcript</button>`, `>Recording</button>`} {
		if strings.Contains(html, redundant) {
			t.Fatalf("transcript status/control must not be duplicated in More: %s", redundant)
		}
	}
}

func TestLiveTranscriptionPillRenderedAuthorityFocusAndMobileFit(t *testing.T) {
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
const html=fs.readFileSync(process.env.TRANSCRIPTION_PILL_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@example.test',name:'AJ'}));}
  if(req.url.startsWith('/participants')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({roomId:'office',participants:['AJ'],occupiedSeats:1,capacity:10,mediaStates:{},endpointCounts:{AJ:1},recording:{enabled:true,available:true,connected:true,revision:4,statusRevision:4,updatedAt:'2026-08-22T10:00:00Z'}}));}
  if(req.url==='/api/stride/v1/mobile/surfaces/organizations'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[]}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:800}});
 await page.goto('http://127.0.0.1:'+server.address().port+'/video',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.waitForFunction(()=>{const shell=document.getElementById('appShell');return shell?.dataset.tool==='room'&&shell?.dataset.pd1Destination==='Video'});
 await page.waitForLoadState('networkidle');
 await page.waitForTimeout(400);
 await page.evaluate(()=>{for(let timer=1;timer<50000;timer++){clearTimeout(timer);clearInterval(timer)}});
 await page.evaluate(()=>{
   const shell=document.getElementById('appShell');
   shell.dataset.tool='room';shell.classList.add('is-in-room','is-authed');
   activeJoin={roomId:'office',passcode:'',guest:false};guestMode=false;
   localStream={};pc={connectionState:'connected'};
   ws={readyState:WebSocket.OPEN,send:raw=>{window.__lastRecordingFrame=JSON.parse(raw)}};
   applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:8,capturing:true,updatedAt:'2026-08-22T11:00:00Z',updatedBy:'AJ'},'office');
   syncToolTopbar();
 });
 let state=await page.evaluate(()=>{const pill=document.getElementById('roomTranscriptPill');const rect=pill.getBoundingClientRect();return {hidden:pill.hidden,label:pill.textContent.trim(),state:pill.dataset.state,expanded:pill.getAttribute('aria-expanded'),rect:rect.toJSON(),revision:roomRecordingRevision}});
 assert.equal(state.hidden,false,JSON.stringify(state));assert.equal(state.label,'Live transcription');assert.equal(state.state,'live');assert.equal(state.revision,8);

 // Provider connection edges refine the same recording revision under their
 // own monotonic status authority. Reordered delivery cannot restore stale live.
 await page.evaluate(()=>applyRoomRecordingState({enabled:true,available:true,connected:false,revision:8,statusRevision:9,updatedAt:'2026-08-22T11:00:00Z',updatedBy:'AJ'},'office'));
 state=await page.evaluate(()=>({label:document.getElementById('roomTranscriptPill').textContent.trim(),kind:document.getElementById('roomTranscriptPill').dataset.state,revision:roomRecordingRevision}));
 assert.deepEqual(state,{label:'Connecting transcription…',kind:'pending',revision:8});
 await page.evaluate(()=>applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:8,updatedAt:'2026-08-22T11:00:00Z',updatedBy:'AJ'},'office'));
 assert.equal(await page.locator('#roomTranscriptPillLabel').textContent(),'Connecting transcription…');
 await page.evaluate(()=>applyRoomRecordingState({enabled:true,available:true,connected:true,revision:8,statusRevision:10,capturing:true,updatedAt:'2026-08-22T11:00:00Z',updatedBy:'AJ'},'office'));
 assert.equal(await page.locator('#roomTranscriptPillLabel').textContent(),'Live transcription');

 // An older participant snapshot may update the roster, never recording truth.
 await page.evaluate(()=>applyRoomRecordingState({enabled:false,available:true,connected:true,revision:7,statusRevision:7,updatedAt:'2026-08-22T10:59:59Z',updatedBy:'Tyler'},'office'));
 state=await page.evaluate(()=>({label:document.getElementById('roomTranscriptPill').textContent.trim(),revision:roomRecordingRevision,enabled:roomRecordingEnabled}));
 assert.deepEqual(state,{label:'Live transcription',revision:8,enabled:true});

 // Requesting pause is pending until a newer server snapshot confirms it.
 state=await page.evaluate(()=>{toggleRoomRecording();syncRoomTranscriptionPill();return {label:document.getElementById('roomTranscriptPill').textContent.trim(),enabled:roomRecordingEnabled,pending:roomRecordingPendingDesired,frame:window.__lastRecordingFrame}});
 assert.equal(state.label,'Updating transcription…',JSON.stringify(state));assert.equal(state.enabled,true);assert.equal(state.pending,false);assert.equal(JSON.parse(state.frame.data).enabled,false);
 await page.evaluate(()=>applyRoomRecordingState({enabled:false,available:true,connected:true,revision:9,statusRevision:11,updatedAt:'2026-08-22T11:01:00Z',updatedBy:'Tyler'},'office'));
 state=await page.evaluate(()=>({label:document.getElementById('roomTranscriptPill').textContent.trim(),kind:document.getElementById('roomTranscriptPill').dataset.state,pending:roomRecordingPendingDesired,enabled:roomRecordingEnabled}));
 assert.deepEqual(state,{label:'Transcription paused',kind:'paused',pending:null,enabled:false});

 // A transport break cannot leave a green live claim behind.
 await page.evaluate(()=>{roomRecordingSocketConfirmed=false;syncRoomTranscriptionPill()});
 assert.equal(await page.locator('#roomTranscriptPillLabel').textContent(),'Transcription unavailable');
 await page.evaluate(()=>applyRoomRecordingState({enabled:false,available:true,connected:true,revision:9,statusRevision:11,updatedAt:'2026-08-22T11:01:00Z',updatedBy:'Tyler'},'office'));

 // Keyboard activation opens the existing readable transcript and moves focus.
 await page.locator('#roomTranscriptPill').focus();await page.keyboard.press('Enter');
 await page.waitForFunction(()=>document.activeElement?.id==='roomMeetingTranscript');
 state=await page.evaluate(()=>({mode:roomMeetingMode,panelHidden:document.getElementById('roomMeetingTranscript').hidden,expanded:document.getElementById('roomTranscriptPill').getAttribute('aria-expanded')}));
 assert.deepEqual(state,{mode:'transcript',panelHidden:false,expanded:'true'});

 // Pause/resume lives contextually inside Transcript and remains acknowledgement-driven.
 const transcriptControl=page.locator('#roomMeetingTranscript .room-meeting-transcription-action');
 assert.equal(await transcriptControl.textContent(),'Resume transcription');
 state=await page.evaluate(()=>{document.querySelector('#roomMeetingTranscript .room-meeting-transcription-action').click();syncRoomTranscriptionPill();return {enabled:roomRecordingEnabled,pending:roomRecordingPendingDesired,label:document.querySelector('#roomMeetingTranscript .room-meeting-transcription-action')?.textContent}});
 assert.deepEqual(state,{enabled:false,pending:true,label:'Updating transcription…'});
 await page.evaluate(()=>applyRoomRecordingState({enabled:true,available:true,connected:true,revision:10,statusRevision:12,capturing:true,updatedAt:'2026-08-22T11:02:00Z',updatedBy:'AJ'},'office'));
 assert.equal(await page.locator('#roomMeetingTranscript .room-meeting-transcription-action').textContent(),'Pause transcription');

 // A stale frame from the room just left is ignored after a room change.
 await page.evaluate(()=>{activeJoin={roomId:'room-b',passcode:'',guest:false};resetRoomRecordingAuthority('room-b');applyRoomRecordingState({enabled:true,available:true,connected:true,revision:99,statusRevision:99,updatedAt:'2026-08-22T12:00:00Z'},'office');syncRoomTranscriptionPill()});
 state=await page.evaluate(()=>({known:roomRecordingKnown,room:roomRecordingRoomId,label:document.getElementById('roomTranscriptPill').textContent.trim()}));
 assert.deepEqual(state,{known:false,room:'room-b',label:'Transcription unavailable'});

 await page.setViewportSize({width:320,height:700});
 await page.evaluate(()=>{activeJoin={roomId:'room-b',passcode:'',guest:false};localStream={};pc={connectionState:'connected'};ws={readyState:WebSocket.OPEN,send:()=>{}};applyRoomRecordingState({enabled:true,available:true,connected:true,revision:1,statusRevision:1,capturing:true,updatedAt:'2026-08-22T12:01:00Z'},'room-b');syncToolTopbar()});
 state=await page.evaluate(()=>{const pill=document.getElementById('roomTranscriptPill');const rect=pill.getBoundingClientRect();return {hidden:pill.hidden,label:pill.textContent.trim(),left:rect.left,right:rect.right,top:rect.top,bottom:rect.bottom,width:innerWidth,height:innerHeight}});
 assert.equal(state.hidden,false,JSON.stringify(state));assert.equal(state.label,'Live transcription');assert.ok(state.left>=0&&state.right<=state.width&&state.top>=0&&state.bottom<=state.height,JSON.stringify(state));
 if(String(process.env.TRANSCRIPTION_PILL_RENDER_PATH||'').trim())await page.screenshot({path:process.env.TRANSCRIPTION_PILL_RENDER_PATH});
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "TRANSCRIPTION_PILL_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered live-transcription pill harness: %v\n%s", err, output)
	}
}
