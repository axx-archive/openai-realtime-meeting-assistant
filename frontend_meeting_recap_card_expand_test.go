package main

// AJ 2026-09-03 — "the meeting cards should expand in chat or just show all the
// info, when you click open record it goes to something that for users means
// nothing, and users shouldn't be taken to".
//
// The posted recap message only ever carried the top three decisions and the
// mono counts (meeting_recap_card.go), so the chat card now fetches the record
// detail it never had and renders it INSIDE the message. Two pins here: the
// static shape of the card, and a rendered contract that drives the real node.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexMeetingRecapCardExpandsInChatInsteadOfRouting(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	card := functionBodyAfterSignature(html, "function meetingRecapCardNode(spec = {})")
	if card == "" {
		t.Fatal("missing meetingRecapCardNode")
	}
	// the chat half (spec.inRecord falsy) is a disclosure, never a route out
	for _, want := range []string{
		"} else if (spec.meetingId && authedUser && !guestMode) {",
		"const disclose = bfEl('button', 'meeting-recap-card__disclose')",
		"disclose.setAttribute('aria-controls', `meeting-recap-detail-${++meetingRecapCardDetailSeq}`)",
		"syncMeetingRecapCardDisclosure(disclose, false, title)",
		"toggleMeetingRecapCardDetail(card, meetingId, disclose, title)",
		"if (meetingRecapCardExpanded.has(meetingId)) {",
		"'the full recap is for signed-in members'",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("meetingRecapCardNode missing %q", want)
		}
	}
	// the record surface keeps its own card untouched
	if !strings.Contains(card, "'Open in conversation'") {
		t.Error("the Meeting Record recap block lost its 'Open in conversation' control")
	}
	for _, gone := range []string{"'Open record'", "openMeetingRecordDeepLink("} {
		if strings.Contains(card, gone) {
			t.Errorf("the chat recap card must not route to the record surface: %q", gone)
		}
	}
	// the collapsed card stays the compact card (AJ 2026-09-02)
	for _, gone := range []string{".slice(0, 5)", "listSection('Action items'", "meeting-recap-card__owner"} {
		if strings.Contains(card, gone) {
			t.Errorf("compact recap card (AJ 2026-09-02) must not contain %q", gone)
		}
	}
	detail := functionBody(html, "function meetingRecapCardDetailNode(meetingId)")
	for _, want := range []string{
		"memoryMeetingDetails.get(String(meetingId || ''))",
		"'Loading the full recap…'", "'could not load the full recap'",
		"claimSection('Decisions', detail.decisions",
		"claimSection('Action items', detail.commitments",
		"claimSection('Open & unresolved', detail.blockers",
		"meeting-recap-card__by", "'In the room'",
		"meetingRecordRecordingNode(String(meetingId || ''), detail)",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("meetingRecapCardDetailNode missing %q", want)
		}
	}
	toggle := functionBody(html, "function toggleMeetingRecapCardDetail(card, meetingId, button, title)")
	for _, want := range []string{
		"const scroller = meetingRecapCardScroller(card)",
		"card.getBoundingClientRect().top - scroller.getBoundingClientRect().top",
		"scroller.scrollTop += after - before",
		"ensureMeetingRecapCardDetail(meetingId)",
	} {
		if !strings.Contains(toggle, want) {
			t.Errorf("toggleMeetingRecapCardDetail missing %q — expanding must not yank the chat viewport", want)
		}
	}
	// one lazy fetch per meeting, through the loader that already owns the cache
	ensure := functionBodyAfterSignature(html, "function ensureMeetingRecapCardDetail(meetingId, options = {})")
	for _, want := range []string{
		"if (!meetingId || !authedUser || guestMode) return",
		"if (current?.state === 'loading' || current?.state === 'ready') return",
		"if (current?.state === 'error' && !options.retry) return",
		"void loadMeetingRecordDetail(meetingId",
	} {
		if !strings.Contains(ensure, want) {
			t.Errorf("ensureMeetingRecapCardDetail missing %q", want)
		}
	}
	loader := functionBodyAfterSignature(html, "async function loadMeetingRecordDetail(meetingId, options = {})")
	if strings.Contains(loader, "renderMemory()") || !strings.Contains(loader, "renderMeetingRecordDetailSurfaces(meetingId)") {
		t.Error("loadMeetingRecordDetail must repaint both surfaces through renderMeetingRecordDetailSurfaces")
	}
	surfaces := functionBody(html, "function renderMeetingRecordDetailSurfaces(meetingId)")
	if !strings.Contains(surfaces, "renderMemory()") || !strings.Contains(surfaces, "refreshMeetingRecapCardDetails(meetingId)") {
		t.Error("renderMeetingRecordDetailSurfaces must repaint the memory timeline and the open chat cards")
	}
	// motion: the house @starting-style entrance, no animated height to clip a
	// long recap, on tokens that zero themselves under reduced motion
	for _, want := range []string{
		".meeting-recap-card__disclose {",
		".meeting-recap-card__detail {",
		"transition-duration: var(--dur-fast), var(--dur-med);",
		"@starting-style {\n        .meeting-recap-card__detail {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("recap card expansion CSS missing %q", want)
		}
	}
	if !strings.Contains(html, ".meeting-recap-card.is-recap-full .meeting-recap-card__compact { display: none; }") {
		t.Error("a fully loaded expansion must replace the compact rows, not repeat them")
	}
	if strings.Contains(html, ".meeting-recap-card__detail {\n        max-height:") {
		t.Error("the expanded recap must never animate from a hardcoded height")
	}
}

// The rendered contract: a real posted card in a real scroller. No navigation
// control, the full action items and open items on expand, the compact shape
// back on collapse, one fetch, and nothing for a viewer who cannot read the
// record but a dead button.
func TestMeetingRecapCardRenderedExpandsInPlace(t *testing.T) {
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
const html=fs.readFileSync(process.env.MEETING_RECAP_INDEX,'utf8');
const meetingId='meeting-recap-expand-1';
const row={contract:'meeting-record-v1',id:meetingId,roomId:'room-1',title:'Launch review',outcomePreview:'Pricing ships Friday.',recordRevision:'rev-1',startedAt:'2026-09-02T16:00:00Z',endedAt:'2026-09-02T17:12:00Z',active:false,durationSeconds:4320,participants:['AJ','Tim','Tyler'],coverageState:'full',decisionCount:4,commitmentCount:2,unresolvedCount:1,transcriptCount:3};
const claim=(kind,text,owner)=>({kind,text,owner:owner||'',ownerState:owner?'resolved':'unresolved',dueState:'unresolved',workState:'unresolved',projectState:'unresolved',work:[],projects:[],status:'open',sources:[]});
const detail={...row,
  executiveRecap:[claim('recap','The team locked the launch order.')],
  needsToKnow:[],
  decisions:[claim('decision','Ship the pricing page Friday'),claim('decision','Freeze scope on the mobile app'),claim('decision','Move the review to Tuesday'),claim('decision','Cut the fourth vendor')],
  commitments:[claim('commitment','Draft the pricing sheet','Tyler'),claim('commitment','Book the launch review','Tim')],
  blockers:[claim('blocker','Which SKU ships first?')],
  people:['AJ','Tim','Tyler'],work:[],projects:[],artifacts:[],
  recording:{playbackPath:'/recordings/launch.webm',mime:'video/webm',size:1048576,durationSeconds:4320,storedAt:'2026-09-02T17:20:00Z',uploadedBy:'aj@shareability.com'},
  coverage:{state:'full',transcriptCount:3,unavailableClaims:0,gaps:[],listenOnly:false},
  transcript:{segments:[],hasMore:false}};
let detailHits=0;
let signedOut=false;
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
  if(req.url.startsWith('/public/')){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){
    if(signedOut){res.writeHead(401,{'content-type':'application/json'});return res.end('{"error":"not signed in"}');}
    res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));
  }
  if(req.url.startsWith('/assistant/meetings/'+meetingId+'?')){detailHits++;res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,contract:'meeting-record-v1',meeting:detail}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')||req.url.startsWith('/recordings')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
const cardText=['**Meeting recap — Launch review**','the office · 1h 12m · 3 people','','Decisions','• Ship the pricing page Friday','• Freeze scope on the mobile app','• Move the review to Tuesday','','+1 decision · 2 action items · 1 open'].join('\n');
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  const errors=[];
  page.on('pageerror',error=>errors.push(String(error.message)));
  await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  // mount the posted channel message the way chat does, inside a scroller
  await page.evaluate(({id,text})=>{
    globalThis.__deepLinkCalls=[];
    openMeetingRecordDeepLink=meetingId=>{globalThis.__deepLinkCalls.push(meetingId);return Promise.resolve(false);};
    const host=document.createElement('div');
    host.id='recapHost';
    host.style.cssText='position:fixed;left:0;top:0;width:640px;height:280px;overflow-y:auto;background:#fff;z-index:99999';
    const filler=document.createElement('div');
    filler.style.height='400px';
    host.appendChild(filler);
    host.appendChild(meetingRecapCardMessageNode({id:'meeting-recap-card-'+id,role:'scout',kind:'message',text,createdAt:'2026-09-02T17:30:00Z',authorName:'Scout'}));
    host.appendChild(Object.assign(document.createElement('div'),{style:'height:400px'}));
    document.body.appendChild(host);
    host.scrollTop=380;
  },{id:meetingId,text:cardText});
  const card=page.locator('#recapHost .meeting-recap-card');
  await card.waitFor();
  // 1. the chat card carries NO route out of the conversation
  assert.equal(await card.getByRole('button',{name:/open record/i}).count(),0,'the chat recap card still offers a route to the record surface');
  assert.equal(await card.locator('button').count(),1,'the chat recap card must carry exactly one control');
  assert.equal(await page.evaluate(()=>document.querySelector('#recapHost .meeting-recap-card').textContent.includes('Meeting Record')),false,'the card still names the raw record surface');
  // 2. collapsed = the compact shape the server posted
  const compactItems=await card.locator('li').allInnerTexts();
  assert.deepEqual(compactItems,['Ship the pricing page Friday','Freeze scope on the mobile app','Move the review to Tuesday']);
  assert.equal(await card.locator('.meeting-recap-card__more').innerText(),'+1 decision · 2 action items · 1 open');
  const disclose=card.locator('.meeting-recap-card__disclose');
  assert.equal(await disclose.getAttribute('aria-expanded'),'false');
  assert.match(await disclose.innerText(),/Show the full recap/);
  assert.equal(detailHits,0,'the card fetched the record before anyone asked');
  // 3. expanding reveals everything, in place, without moving the card
  const topBefore=await page.evaluate(()=>document.querySelector('#recapHost .meeting-recap-card').getBoundingClientRect().top);
  await disclose.click();
  const detailId=await disclose.getAttribute('aria-controls');
  await card.locator('#'+detailId+' h4', {hasText:'Action items'}).waitFor();
  await card.getByText('Draft the pricing sheet').waitFor();
  const topAfter=await page.evaluate(()=>document.querySelector('#recapHost .meeting-recap-card').getBoundingClientRect().top);
  assert.ok(Math.abs(topAfter-topBefore)<=1,'expanding yanked the reader: card top moved '+(topAfter-topBefore)+'px');
  const expanded=await card.locator('.meeting-recap-card__detail').innerText();
  for(const want of ['The team locked the launch order.','Cut the fourth vendor','Draft the pricing sheet','— Tyler','Book the launch review','Which SKU ships first?','AJ · Tim · Tyler']){
    assert.ok(expanded.includes(want),'expanded recap missing '+JSON.stringify(want)+':\n'+expanded);
  }
  assert.equal(await card.locator('video.meeting-record__media').count(),1,'the expanded recap dropped the recording');
  // a whole recap REPLACES the compact half — the top three decisions must not
  // sit above all four of them
  assert.equal(await card.locator('.meeting-recap-card__more').isVisible(),false,'the mono counts line survived a complete expansion');
  const shown=await card.innerText();
  assert.equal(shown.split('Ship the pricing page Friday').length-1,1,'the expanded card repeats the compact decisions:\n'+shown);
  assert.equal(await disclose.getAttribute('aria-expanded'),'true');
  assert.match(await disclose.innerText(),/Show less/);
  assert.equal(detailHits,1,'the expand did not fetch the record exactly once');
  // The recording must own its height in document flow. A stage-wide
  // video height:100% used to push metadata across the disclosure button.
  const recordingFlow=await card.evaluate(node=>{
    const media=node.querySelector('.meeting-record__media').getBoundingClientRect();
    const metadata=node.querySelector('.meeting-record__recording-meta').getBoundingClientRect();
    const footer=node.querySelector('.meeting-recap-card__foot').getBoundingClientRect();
    return {mediaBottom:media.bottom,metadataTop:metadata.top,metadataBottom:metadata.bottom,footerTop:footer.top};
  });
  assert.ok(recordingFlow.metadataTop>=recordingFlow.mediaBottom,'recording metadata overlaps the player');
  assert.ok(recordingFlow.footerTop>=recordingFlow.metadataBottom,'recording metadata overlaps the recap disclosure');
  // 4. collapsing restores the compact shape
  await disclose.click();
  assert.equal(await card.locator('.meeting-recap-card__detail').count(),0);
  assert.deepEqual(await card.locator('li').allInnerTexts(),compactItems);
  assert.equal(await card.locator('.meeting-recap-card__more').innerText(),'+1 decision · 2 action items · 1 open');
  assert.equal(await disclose.getAttribute('aria-expanded'),'false');
  // 5. re-expanding is cached: no second fetch, and the deep link never ran
  await disclose.click();
  await card.getByText('Draft the pricing sheet').waitFor();
  assert.equal(detailHits,1,'re-expanding refetched the record instead of using the cache');
  assert.deepEqual(await page.evaluate(()=>globalThis.__deepLinkCalls),[],'the chat card routed the reader to the record surface');
  // a durable PRE-2026-09-03 card (with the record tail) still renders as the
  // same expandable card, and never leaks the raw URL as prose
  const legacy=await page.evaluate(({id,text})=>{
    const host=document.createElement('div');
    host.id='legacyHost';
    host.appendChild(meetingRecapCardMessageNode({id:'meeting-recap-card-'+id,role:'scout',kind:'message',text:text+'\n\nMeeting Record: https://bonfire.test/?record='+id,createdAt:'2026-09-02T17:30:00Z',authorName:'Scout'}));
    document.body.appendChild(host);
    const node=host.querySelector('.meeting-recap-card');
    return {text:node.innerText,discloses:node.querySelectorAll('.meeting-recap-card__disclose').length,meetingId:node.dataset.meetingId};
  },{id:meetingId,text:cardText});
  assert.equal(legacy.discloses,1,'a legacy card lost the disclosure');
  assert.equal(legacy.meetingId,meetingId);
  assert.ok(!legacy.text.includes('bonfire.test'),'a legacy card leaked its record URL as prose:\n'+legacy.text);
  assert.deepEqual(errors,[],'page errors: '+errors.join(' | '));
  // 6. signed out (and guests in room chat): no dead control, an honest line
  signedOut=true;
  const guest=await browser.newPage({viewport:{width:1280,height:900}});
  await guest.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
  await guest.waitForFunction(()=>typeof meetingRecapCardMessageNode==='function');
  await guest.evaluate(({id,text})=>{
    const host=document.createElement('div');
    host.id='recapHost';
    host.appendChild(meetingRecapCardMessageNode({id:'meeting-recap-card-'+id,role:'scout',kind:'message',text,createdAt:'2026-09-02T17:30:00Z',authorName:'Scout'}));
    document.body.appendChild(host);
  },{id:meetingId,text:cardText});
  const guestCard=guest.locator('#recapHost .meeting-recap-card');
  await guestCard.waitFor();
  assert.equal(await guestCard.locator('button').count(),0,'a viewer who cannot read the record still gets a control that does nothing');
  assert.match(await guestCard.locator('.meeting-recap-card__posted').innerText(),/signed-in members/);
  assert.equal(detailHits,1,'a signed-out viewer hit the member-only detail endpoint');
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1);});
`
	command := exec.Command("node", "-e", script)
	command.Env = append(os.Environ(), "MEETING_RECAP_INDEX="+indexPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rendered recap card expansion: %v\n%s", err, output)
	}
}
