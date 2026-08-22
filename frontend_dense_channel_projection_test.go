package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDenseChannelNavigationProjectsExactTailBeforeRetainedHistory(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		"const scoutChatInitialNavigationNodes = 18",
		"function startScoutChatProgressiveProjection(thread, messages, tailStart, firstUnreadIndex, status)",
		"scoutChatThread.dataset.projectionState = 'partial'",
		"scoutChatThread.dataset.projectionState = 'complete'",
		"Loading earlier messages",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("dense-channel progressive projection contract missing %q", want)
		}
	}
}

func TestDenseChannelNavigationFirstPaintAndEventualFullProjectionRendered(t *testing.T) {
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
const html=fs.readFileSync(process.env.DENSE_CHANNEL_INDEX,'utf8');
const base=Date.parse('2026-08-22T12:00:00Z');
const denseMessages=Array.from({length:200},(_,index)=>{
  const common={id:'dense-'+index,role:'scout',createdAt:new Date(base+index*1000).toISOString()};
  if(index%5!==0)return {...common,kind:'message',authorName:index%2?'Scout':'Designer',text:'Decision '+index+'\n\n- source-grounded proof\n- exact channel context\n\n> '+('A deliberately substantial retained message. '.repeat(8))};
  return {...common,kind:'manifest',manifest:{goalId:'goal-'+index,status:'shipped',title:'Retained package '+index,subline:'Exact deliverables remain available.',deliverables:[
    {badge:'paper',title:'Story record '+index,artifactId:'story-'+index,facts:'doc · source grounded'},
    {badge:'doc',title:'Findings '+index,artifactId:'findings-'+index,facts:'doc · every verdict'},
    {badge:'paper',title:'Presenter notes '+index,artifactId:'notes-'+index,facts:'pdf · text native'},
    {badge:'doc',title:'Source map '+index,artifactId:'sources-'+index,facts:'doc · linked proof'}
  ],skips:['No unsupported claim was promoted.']}};
});
const threads=[
  {id:'small',title:'Small channel',visibility:'public',messagesLoaded:true,createdAt:new Date(base).toISOString(),updatedAt:new Date(base+1000).toISOString(),messages:[{id:'small-1',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'Small channel ready.',createdAt:new Date(base).toISOString()}]},
  {id:'dense',title:'Like A Farmer',visibility:'public',messagesLoaded:true,createdAt:new Date(base).toISOString(),updatedAt:new Date(base+201000).toISOString(),messages:denseMessages}
];
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat',{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.waitForFunction(()=>document.getElementById('appShell')?.dataset.tool==='chat');
  await page.evaluate(threads=>{artifactEntries=[];scoutChatThreads=threads;activeScoutThreadId='small';renderActiveScoutThread({forceBottom:true});},threads);

  const first=await page.evaluate(async()=>{
    const started=performance.now();
    selectScoutChatThread('dense');
    const returnedAt=performance.now();
    const immediate={
      syncMs:returnedAt-started,
      state:scoutChatThread.dataset.projectionState,
      status:document.querySelector('.scout-chat-history-loading')?.textContent||'',
      firstPresent:Boolean(document.querySelector('[data-message-id="dense-0"]')),
      lastPresent:Boolean(document.querySelector('[data-message-id="dense-199"]')),
      projected:document.querySelectorAll('#scoutChatThread > .scout-chat-msg, #scoutChatThread > .manifest-card').length,
      tail:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight
    };
    await new Promise(resolve=>requestAnimationFrame(()=>resolve()));
    immediate.firstPaintMs=performance.now()-started;
    return immediate;
  });
  assert.equal(first.state,'partial',JSON.stringify(first));
  assert.equal(first.status,'Loading earlier messages',JSON.stringify(first));
  assert.equal(first.firstPresent,false,JSON.stringify(first));
  assert.equal(first.lastPresent,true,JSON.stringify(first));
  assert.ok(first.projected<=18,'navigation synchronously projected retained history: '+JSON.stringify(first));
  assert.ok(first.syncMs<700,'dense click path stayed synchronously blocked: '+JSON.stringify(first));
  assert.ok(first.firstPaintMs<1000,'exact tail did not paint promptly: '+JSON.stringify(first));
  assert.ok(Math.abs(first.tail)<=1,'initial exact tail was not at the true bottom: '+JSON.stringify(first));

  await page.waitForFunction(()=>scoutChatThread.dataset.projectionState==='complete'&&!scoutChatProgressiveAnchorSettle&&document.querySelector('[data-message-id="dense-0"]'),null,{timeout:15000});
  const complete=await page.evaluate(()=>({
    count:document.querySelectorAll('#scoutChatThread > .scout-chat-msg, #scoutChatThread > .manifest-card').length,
    statusCount:document.querySelectorAll('.scout-chat-history-loading').length,
    firstTitle:document.querySelector('[data-message-id="dense-0"] .manifest-card__title')?.textContent||'',
    lastText:document.querySelector('[data-message-id="dense-199"]')?.textContent||'',
    tail:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight
  }));
  assert.equal(complete.count,200,JSON.stringify(complete));
  assert.equal(complete.statusCount,0,JSON.stringify(complete));
  assert.equal(complete.firstTitle,'Retained package 0',JSON.stringify(complete));
  assert.match(complete.lastText,/Decision 199/);
  assert.ok(Math.abs(complete.tail)<=1,'eventual full projection lost the true tail: '+JSON.stringify(complete));

  // A reader who takes control during the progressive pass keeps the exact
  // selected record and pixel offset when prepared history attaches above it.
  await page.evaluate(()=>selectScoutChatThread('small'));
  const readerStart=await page.evaluate(async()=>{
    selectScoutChatThread('dense');
    scoutChatThread.dispatchEvent(new WheelEvent('wheel',{bubbles:true}));
    const stateAtIntent=scoutChatThread.dataset.projectionState;
    document.querySelector('[data-message-id="dense-190"]').scrollIntoView({block:'start'});
	await new Promise(resolve=>requestAnimationFrame(()=>setTimeout(resolve,0)));
	const viewport=scoutChatProgressiveProjection?.readerViewport||captureScoutChatViewport();
    viewport.anchor.dataset.readerAnchor='true';
    return {top:viewport.anchor.getBoundingClientRect().top,stateAtIntent,state:scoutChatThread.dataset.projectionState,messageId:viewport.anchor.dataset.messageId||''};
  });
  // The intent must land while projection is active. On a fast machine the
  // bounded projection may truthfully finish before the next animation-frame
  // callback; that is not a failure as long as the reader anchor survives.
  assert.equal(readerStart.stateAtIntent,'partial',JSON.stringify(readerStart));
  assert.ok(['partial','complete'].includes(readerStart.state),JSON.stringify(readerStart));
  await page.waitForFunction(()=>scoutChatThread.dataset.projectionState==='complete'&&!scoutChatProgressiveAnchorSettle&&document.querySelector('[data-message-id="dense-0"]'),null,{timeout:15000});
  const readerEnd=await page.evaluate(()=>({top:document.querySelector('[data-reader-anchor="true"]').getBoundingClientRect().top,distance:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight}));
  assert.ok(Math.abs(readerEnd.top-readerStart.top)<=1,'retained history moved the reader anchor: '+JSON.stringify({readerStart,readerEnd}));
  assert.ok(readerEnd.distance>48,'reader intent was pulled back to the tail: '+JSON.stringify(readerEnd));

  // A second gesture after the prepend wins over the bounded settle token.
  // This models someone changing their mind while late rich-card geometry is
  // still stabilizing; the app must never tug them back to the first anchor.
  await page.evaluate(()=>selectScoutChatThread('small'));
  const secondGesture=await page.evaluate(()=>new Promise(resolve=>{
    let acted=false;
    const observer=new MutationObserver(()=>{
      if(acted||scoutChatThread.dataset.projectionState!=='complete'||!scoutChatProgressiveAnchorSettle)return;
      acted=true;observer.disconnect();
      scoutChatThread.dispatchEvent(new WheelEvent('wheel',{bubbles:true}));
      const next=document.querySelector('[data-message-id="dense-195"]');
      next.scrollIntoView({block:'start'});
      const before=next.getBoundingClientRect().top;
      requestAnimationFrame(()=>requestAnimationFrame(()=>resolve({before,after:next.getBoundingClientRect().top,settleActive:Boolean(scoutChatProgressiveAnchorSettle),distance:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight})));
    });
    observer.observe(scoutChatThread,{attributes:true,attributeFilter:['data-projection-state']});
    selectScoutChatThread('dense');
    scoutChatThread.dispatchEvent(new WheelEvent('wheel',{bubbles:true}));
    document.querySelector('[data-message-id="dense-190"]').scrollIntoView({block:'start'});
  }));
  assert.equal(secondGesture.settleActive,false,JSON.stringify(secondGesture));
  assert.ok(Math.abs(secondGesture.after-secondGesture.before)<=1,'a later reader gesture was overridden by anchor settle: '+JSON.stringify(secondGesture));
  assert.ok(secondGesture.distance>48,JSON.stringify(secondGesture));
  assert.deepEqual(errors,[]);
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1);});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DENSE_CHANNEL_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dense-channel rendered projection contract failed: %v\n%s", err, output)
	}
}
