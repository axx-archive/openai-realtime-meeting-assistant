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
		`if (existing?.messagesLoaded === true) return existing`,
		`void hydrateScoutChatThread(nextThreadId)`,
		`const boundedFeedMessages = feedMessages.slice(-scoutChatMaxNodes)`,
		`mobileChatView === 'convo' && phoneLayoutQuery.matches`,
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
 {id:'country-golf',title:'Country Golf',preview:'Research delivered',ownerEmail:'aj@shareability.com',visibility:'public',createdAt:'2026-08-12T17:00:00Z',updatedAt:'2026-08-12T19:00:00Z',messagesLoaded:false},
 {id:'private-one',title:'Live Voice with Scout',preview:'Useful next move',ownerEmail:'aj@shareability.com',visibility:'private',createdAt:'2026-08-12T17:00:00Z',updatedAt:'2026-08-12T18:00:00Z',messagesLoaded:false}
];
const messages=Array.from({length:1200},(_,i)=>({id:'m'+i,kind:'message',role:i%2?'scout':'user',authorEmail:i%2?'':'aj@shareability.com',authorName:i%2?'Scout':'AJ',text:'Message '+i,createdAt:new Date(Date.UTC(2026,7,12,0,0,i)).toISOString()}));
const server=http.createServer((req,res)=>{
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation)}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}))}
 if(req.url==='/assistant/chat-threads?view=index'){indexRequests++;return setTimeout(()=>{res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,threads:rows}))},600)}
 if(req.url==='/assistant/chat-threads/country-golf'){threadRequests++;return setTimeout(()=>{res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,thread:{...rows[0],messagesLoaded:true,messages}}))},1500)}
 if(req.url==='/assistant/chat-threads/private-one'){threadRequests++;res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,thread:{...rows[1],messagesLoaded:true,messages:[]}}))}
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}')}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html)
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:820,height:1180}});await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});await page.waitForSelector('#appShell.is-authed');await page.evaluate(()=>{const shell=document.querySelector('#appShell');shell.classList.remove('is-mounting');shell.classList.add('is-fast-mount');shell.dataset.tool='chat'});await page.waitForTimeout(80);
 assert.ok(await page.locator('.chat-thread-skeleton').count()>=4,'cold Chat did not reserve public/private rows immediately');assert.equal(indexRequests,1,'auth and Chat navigation did not coalesce the index request');
 const country=page.locator('#chatChannelThreads .chat-thread-item').filter({hasText:'Country Golf'});await country.waitFor({state:'attached'});await page.evaluate(()=>{document.querySelector('#appShell').dataset.tool='chat'});await country.waitFor({state:'visible'});assert.equal(await page.locator('#chatChannelThreads .chat-thread-item').count(),1);assert.equal(await page.locator('#chatAgentThreads .chat-thread-item').count(),1);assert.ok(indexRequests<=2,'unbounded duplicate cold index requests: '+indexRequests);
 await page.evaluate(()=>selectScoutChatThread('country-golf'));await page.locator('.scout-chat-loading').waitFor();assert.equal(threadRequests,1);await page.waitForFunction(()=>document.querySelectorAll('#scoutChatThread .scout-chat-msg').length>0);const rendered=await page.locator('#scoutChatThread .scout-chat-msg').count();assert.ok(rendered<=200,'constructed more than bounded message tail: '+rendered);assert.equal(await page.locator('.scout-chat-loading').count(),0);
 await browser.close();server.close()})().catch(e=>{console.error(e);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CHAT_LOADING_INDEX="+indexPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered cold Chat loading failed: %v\n%s", err, output)
	}
}
