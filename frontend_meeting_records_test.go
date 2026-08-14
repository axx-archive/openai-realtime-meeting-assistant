package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeetingRecordWebAndNativeSurfacesKeepExactAuthorityAndCanonicalOrder(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, marker := range []string{
		`data-tool="room" aria-label="Video"`,
		`data-tool="memory" aria-label="Meetings"`,
		"fetch(`/assistant/meetings?view=index&limit=60",
		`meetingCursor=${encodeURIComponent(cursor)}`,
		`Load older meetings`,
		`memoryMeetingsListRequestGeneration`,
		`memoryMeetingDetailRequestGeneration`,
		`memoryConversationRequestGeneration`,
		`memoryMeetingDetails.clear()`,
		`memoryTranscriptOpenIds.clear()`,
		`/assistant/meetings/${encodeURIComponent(meetingId)}/conversation`,
		`{ recordRevision: revision }`,
		`current?.recordRevision !== revision`,
		`setScoutTab('private')`,
		`source?.kind === 'meeting_transcript'`,
		`{ segmentId, force: true }`,
		`row.dataset.segmentId`,
		`row.tabIndex = -1`,
		`row.setAttribute('aria-label',`,
		`row.focus({ preventScroll: true })`,
		`openPermanentMeetingRecordFromRoom`,
		`returnToLiveMeetingRecordOrigin`,
		`roomMeetingRecordReturn`,
		`Back to live ${roomMeetingRecordReturn.mode}`,
		`Open linked ${kind === 'project' ? 'Project' : 'Work'}`,
		`if (!openId || (openKind !== 'project' && openKind !== 'artifact')) continue`,
		`openArtifactStage(openId, String(reference?.title || 'Work result'))`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("web Meeting Record surface missing %q", marker)
		}
	}
	for _, label := range []string{"Home", "Video", "Chat", "Work", "Network", "Work Search", "You"} {
		if !strings.Contains(html, `data-pd1-destination="`+label+`" aria-label="`+label+`"`) {
			t.Fatalf("responsive primary destination %q lacks a durable accessible name", label)
		}
	}
	if !strings.Contains(html, "Horizontal overflow clipped the entire menu") || !strings.Contains(html, "overflow: visible;") {
		t.Fatal("phone Work menu must escape the fixed dock instead of being vertically clipped")
	}
	if !strings.Contains(html, ".work-tool-menu .tool-rail__label {\n        display: inline-block;") {
		t.Fatal("phone Work menu must retain text labels after the dock hides icon labels")
	}
	render := functionBody(html, "function renderMemoryMeetingBody(meeting)")
	if render == "" {
		t.Fatal("missing Meeting Record body renderer")
	}
	ordered := []string{
		"Executive recap", "What everyone needs to know", "Decisions", "Commitments & follow-ups",
		"Blockers, risks & unresolved", "People, Work, Projects & artifacts", "Source coverage", "meetingRecordTranscriptNode",
	}
	last := -1
	for _, label := range ordered {
		index := strings.Index(render, label)
		if index < 0 || index <= last {
			t.Fatalf("Meeting Record canonical order broke at %q", label)
		}
		last = index
	}
	if !strings.Contains(render, "Ask Scout about this meeting") {
		t.Fatal("web Meeting Record omitted the exact-revision private Scout action")
	}

	nativeRaw, err := os.ReadFile("mobile/src/screens/MeetingsScreen.tsx")
	if err != nil {
		t.Fatal(err)
	}
	native := string(nativeRaw)
	for _, marker := range []string{
		"rowsGenerationRef", "detailGenerationRef", "conversationGenerationRef",
		"rowsToken === sessionToken", "detailToken === sessionToken",
		"api.meetingConversation(token, current.id, current.recordRevision)",
		"navigation.navigate('Thread'", "Ask Scout about this meeting",
		"segmentId: route.params?.meetingId === selectedId", "setTranscriptOpen(true)",
		"Load older Meeting Records", "Open linked ${reference.kind === 'project' ? 'Project' : 'Work'}",
		"filter(meetingRecordReferenceHasExactDestination)", "navigation.navigate('Files', { fileId: openId })",
		"[...visibleDetail.work, ...visibleDetail.projects, ...visibleDetail.artifacts].filter(meetingRecordReferenceHasExactDestination)",
	} {
		if !strings.Contains(native, marker) {
			t.Fatalf("native Meeting Record surface missing %q", marker)
		}
	}
	last = -1
	for _, label := range ordered[:7] {
		index := strings.Index(native, label)
		if index < 0 || index <= last {
			t.Fatalf("native Meeting Record canonical order broke at %q", label)
		}
		last = index
	}

	threadRaw, err := os.ReadFile("mobile/src/screens/ThreadScreen.tsx")
	if err != nil {
		t.Fatal(err)
	}
	thread := string(threadRaw)
	if !strings.Contains(thread, "source.kind === 'meeting_transcript'") ||
		!strings.Contains(thread, "navigation.navigate('Meetings', { meetingId: source.meetingId, segmentId: source.segmentId })") {
		t.Fatal("native transcript citation does not return to the exact Meeting Record interval")
	}
}

func TestMeetingRecordRenderedWorkLinkOpensExactSuccessorArtifact(t *testing.T) {
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
const html=fs.readFileSync(process.env.MEETING_RECORD_INDEX,'utf8');
const meetingId='meeting-exact-work';
const row={contract:'meeting-record-v1',id:meetingId,roomId:'room-1',title:'Exact work review',outcomePreview:'The current result is ready.',recordRevision:'record-rev-1',startedAt:'2026-08-14T16:00:00Z',endedAt:'2026-08-14T16:30:00Z',active:false,durationSeconds:1800,participants:['AJ'],coverageState:'full',decisionCount:0,commitmentCount:1,unresolvedCount:0,transcriptCount:1};
const source={segmentId:'segment-1',revision:'segment-rev-1',speaker:'AJ',at:'2026-08-14T16:10:00Z',correctionState:'current'};
const exactWork={id:'legacy-card-exact',title:'Prepare the exact result',kind:'work',openKind:'artifact',openId:'artifact-exact-result'};
const legacyOnly={id:'legacy-card-no-successor',title:'Old unprojected card',kind:'work'};
const detail={...row,executiveRecap:[],needsToKnow:[],decisions:[],commitments:[{kind:'commitment',text:'Prepare the exact result.',owner:'AJ',ownerState:'resolved',dueState:'unresolved',workState:'resolved',projectState:'unresolved',work:[legacyOnly,exactWork],projects:[],status:'open',sources:[source]}],blockers:[],people:['AJ'],work:[exactWork],projects:[],artifacts:[{id:'artifact-exact-result',title:'Prepare the exact result',kind:'artifact',openKind:'artifact',openId:'artifact-exact-result'}],coverage:{state:'full',transcriptCount:1,unavailableClaims:0,gaps:[],listenOnly:false},transcript:{segments:[{id:'segment-1',revision:'segment-rev-1',speaker:'AJ',at:'2026-08-14T16:10:00Z',text:'Prepare the exact result.',source:'room',correctionState:'current'}],hasMore:false}};
const server=http.createServer((req,res)=>{
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/assistant/meetings?view=index')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,contract:'meeting-record-v1',meetings:[row],hasMore:false}));}
  if(req.url.startsWith('/assistant/meetings/'+meetingId+'?')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,contract:'meeting-record-v1',meeting:detail}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  await page.goto('http://127.0.0.1:'+server.address().port+'/meetings',{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.evaluate(async()=>{setActiveTool('memory');await loadMeetingsForMemory(true);});
  const exact=page.getByRole('button',{name:'Open linked Work Prepare the exact result'});
  await exact.waitFor();
  assert.equal(await page.getByRole('button',{name:'Open linked Work Old unprojected card'}).count(),0,'legacy card without an exact successor rendered as actionable Work');
  await page.evaluate(()=>{globalThis.__meetingWorkOpen=[];openArtifactStage=(id,title)=>{globalThis.__meetingWorkOpen=[id,title];};});
  await exact.click();
  assert.deepEqual(await page.evaluate(()=>globalThis.__meetingWorkOpen),['artifact-exact-result','Prepare the exact result']);
  const globalExact=page.getByRole('button',{name:'Open Meeting Record Work Prepare the exact result'});
  await globalExact.click();
  assert.deepEqual(await page.evaluate(()=>globalThis.__meetingWorkOpen),['artifact-exact-result','Prepare the exact result']);
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1);});`
	command := exec.Command("node", "-e", script)
	command.Env = append(os.Environ(), "MEETING_RECORD_INDEX="+indexPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rendered exact Meeting Work destination: %v\n%s", err, output)
	}
}
