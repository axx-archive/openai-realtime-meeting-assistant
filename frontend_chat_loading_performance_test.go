package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatColdIndexAndHydrationStayBodyFreeFencedAndBounded(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`fetch('/assistant/chat-threads?view=index', { cache: 'no-store' })`,
		`if (chatThreadsRequest)`,
		`if (options.queueIfBusy) chatThreadsRefreshQueued = true`,
		`generation !== chatThreadsGeneration || account !== String(authedUser?.email || '').toLowerCase()`,
		// The index refresh must never drop a thread's already-loaded bodies.
		// The single-line early return was replaced by a keyed merge that
		// keeps the loaded messages AND folds in viewer state; both halves are
		// pinned so neither can regress back to a body refetch.
		`if (current?.messagesLoaded === true && revisionOrder === 0) {`,
		`return { ...row, ...current, ...viewerState, messagesLoaded: current.messagesLoaded === true }`,
		`new URLSearchParams({ view: 'tail', limit: String(scoutChatHydrationPageSize) })`,
		`loadEarlierScoutChatMessages(thread.id, button)`,
		`void hydrateScoutChatThread(nextThreadId)`,
		`const boundedFeedMessages = feedMessages.slice(Math.max(0, feedWindowEnd - scoutChatMaxNodes), feedWindowEnd)`,
		`mobileChatView === 'convo' && phoneLayoutQuery.matches`,
		`const scoutChatNavigationTailPinMs = 12000`,
		`beginScoutChatOwnSendTailPin({ reason: 'navigation' })`,
		`scoutChatThread.addEventListener('pointerdown', pin.cancelForReader, { passive: true })`,
		`window.addEventListener('keydown', pin.cancelForKeyboard)`,
		`resetPersistedScoutChatState()`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("chat cold-load/performance contract missing %q", want)
		}
	}
}

func TestChatColdIndexRendersImmediatelyAndHydratesSelectedThreadAtIPadWidth(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');const http=require('http');const path=require('path');const assert=require('assert/strict');const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.CHAT_LOADING_INDEX,'utf8');const dictation=fs.readFileSync(path.join(path.dirname(process.env.CHAT_LOADING_INDEX),'public','composer-dictation.js'),'utf8');
let indexRequests=0,threadRequests=0;
const rows=[
 {id:'country-golf',title:'Like A Farmer',preview:'Research delivered',ownerEmail:'aj@shareability.com',visibility:'public',createdAt:'2026-08-12T17:00:00Z',updatedAt:'2026-08-12T19:00:00Z',messagesLoaded:false},
 {id:'private-one',title:'Live Voice with Scout',preview:'Useful next move',ownerEmail:'aj@shareability.com',visibility:'private',createdAt:'2026-08-12T17:00:00Z',updatedAt:'2026-08-12T18:00:00Z',messagesLoaded:false}
];
const messages=Array.from({length:1200},(_,i)=>({id:'m'+i,kind:'message',role:i%2?'scout':'user',authorEmail:i%2?'':'aj@shareability.com',authorName:i%2?'Scout':'AJ',text:'Message '+i,createdAt:new Date(Date.UTC(2026,7,12,0,0,i)).toISOString()}));
messages[1188].files=[{ref:'late-channel-media',name:'dense-slide.svg',mime:'image/svg+xml',size:4096}];
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation)}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}))}
 if(req.url==='/assistant/chat-threads?view=index'){indexRequests++;return setTimeout(()=>{res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,threads:rows}))},600)}
 if(req.url.startsWith('/assistant/chat-threads/country-golf?view=tail&limit=80')){threadRequests++;return setTimeout(()=>{res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,thread:{...rows[0],messagesLoaded:true,messages:messages.slice(-80)},history:{mode:'tail',messageCount:80,hasEarlier:true,nextBeforeMessageId:'m1120',oldestMessageId:'m1120',newestMessageId:'m1199'}}))},1500)}
 if(req.url.startsWith('/assistant/chat-threads/private-one?view=tail&limit=80')){threadRequests++;res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,thread:{...rows[1],messagesLoaded:true,messages:[]},history:{mode:'tail',messageCount:0,hasEarlier:false}}))}
 if(req.url.startsWith('/artifacts/blob?ref=late-channel-media')){return setTimeout(()=>{res.writeHead(200,{'content-type':'image/svg+xml','cache-control':'no-store'});res.end('<svg xmlns="http://www.w3.org/2000/svg" width="900" height="2400" viewBox="0 0 900 2400"><rect width="900" height="2400" fill="#d96b42"/></svg>')},6250)}
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}')}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html)
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:820,height:1180}});await page.goto('http://127.0.0.1:'+server.address().port+'/chat',{waitUntil:'domcontentloaded'});await page.waitForSelector('#appShell.is-authed');await page.waitForFunction(()=>document.querySelector('#appShell')?.dataset.tool==='chat');await page.evaluate(()=>{const shell=document.querySelector('#appShell');shell.classList.remove('is-mounting');shell.classList.add('is-fast-mount')});await page.waitForTimeout(80);
 assert.ok(await page.locator('.chat-thread-skeleton').count()>=4,'cold Chat did not reserve public/private rows immediately');assert.equal(indexRequests,1,'auth and Chat navigation did not coalesce the index request');
 const country=page.locator('#chatChannelThreads .chat-thread-item').filter({hasText:'Like A Farmer'});await country.waitFor({state:'visible'});assert.equal(await page.locator('#chatChannelThreads .chat-thread-item').count(),1);assert.equal(await page.locator('#chatAgentThreads .chat-thread-item').count(),1);assert.ok(indexRequests<=2,'unbounded duplicate cold index requests: '+indexRequests);
 // At 820px Chat uses its stacked tablet navigator. Exercise the real row
 // activation so selection also advances threads -> conversation; calling the
 // internal selector alone would correctly leave the conversation pane hidden.
 await page.evaluate(()=>document.querySelector('#chatChannelThreads [data-thread-id="country-golf"]')?.click());await page.locator('.scout-chat-loading').waitFor();assert.equal(threadRequests,1);await page.waitForFunction(()=>document.querySelectorAll('#scoutChatThread .scout-chat-msg').length>0);const rendered=await page.locator('#scoutChatThread .scout-chat-msg').count();assert.ok(rendered<=200,'constructed more than bounded message tail: '+rendered);assert.equal(await page.locator('.scout-chat-loading').count(),0);
	// A fresh selection belongs to the selected channel, not the viewport of
	// whichever conversation happened to be open before it. Hydrate an empty
	// neighbor, then reopen the cached long channel and require the true tail.
	await page.evaluate(()=>selectScoutChatThread('private-one'));await page.waitForFunction(()=>selectedScoutChatThread()?.messagesLoaded===true);await page.evaluate(()=>{scoutChatThread.scrollTop=0;selectScoutChatThread('country-golf')});await page.waitForTimeout(80);const distance=await page.evaluate(()=>scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight);assert.ok(distance<=1,'channel selection inherited the previous viewport instead of landing at the tail: '+distance);
	// Production's dense Like A Farmer channel exposed a later failure: the
	// cached thread initially landed at its then-current bottom, but a rich
	// preview completing just after the old six-second guard expanded content
	// above the tail and left the reader thousands of pixels behind. Keep a
	// channel-navigation pin alive through bounded late geometry, while still
	// allowing an explicit reader gesture to take control immediately.
	await page.waitForTimeout(6500);const lateTail=await page.evaluate(()=>({distance:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight,scrollTop:scoutChatThread.scrollTop,scrollHeight:scoutChatThread.scrollHeight,clientHeight:scoutChatThread.clientHeight,mediaHeight:document.querySelector('img[alt="dense-slide.svg"]')?.getBoundingClientRect().height||0,pinReason:scoutChatOwnSendTailPin?.reason||''}));assert.ok(lateTail.mediaHeight>100,'late rich preview did not expand the dense fixture: '+JSON.stringify(lateTail));assert.ok(Math.abs(lateTail.distance)<=1,'late rich preview stranded channel entry above the true tail: '+JSON.stringify(lateTail));assert.equal(lateTail.pinReason,'navigation',JSON.stringify(lateTail));
	await page.locator('#scoutChatThread').dispatchEvent('wheel');await page.locator('[data-message-id="m1150"]').evaluate(node=>node.scrollIntoView({block:'start'}));const readerIntent=await page.evaluate(()=>{const anchor=document.querySelector('[data-message-id="m1150"]');const before=anchor.getBoundingClientRect().top;const growth=document.createElement('div');growth.dataset.lateTailGrowth='true';growth.style.height='900px';growth.style.flex='0 0 900px';scoutChatThread.insertBefore(growth,scoutChatThinking);return new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve({before,after:anchor.getBoundingClientRect().top,distance:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight,pinActive:Boolean(scoutChatOwnSendTailPin)}))))});assert.equal(readerIntent.pinActive,false,JSON.stringify(readerIntent));assert.ok(readerIntent.distance>48,'explicit reader scroll was pulled back to the tail: '+JSON.stringify(readerIntent));assert.ok(Math.abs(readerIntent.after-readerIntent.before)<=1,'late geometry moved the reader anchor: '+JSON.stringify(readerIntent));
 await browser.close();server.close()})().catch(e=>{console.error(e);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CHAT_LOADING_INDEX="+indexPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered cold Chat loading failed: %v\n%s", err, output)
	}
}
