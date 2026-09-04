package main

// Hotfix gen 249 — AJ: "Scout's avatar (the flame) looks nice beside his
// name, why are humans' avatars kind of floating?" A teammate's run wears the
// same header row as Scout: one 22px identity tile (initials) + name + time
// above the first bubble, bubbles beneath on the tile's left edge, no disc
// floating beside a bubble; follow-up rows in a run hide the header; own
// messages keep their right-aligned attribution line. Desktop + phone.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexChatAuthorHeaderRow(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	for _, want := range []string{
		"if (agentSender || (kind === 'peer' && authorLabel)) {",
		"badge.textContent = chatPeerInitials(label)",
		".scout-chat-msg--peer .scout-chat-msg__idbadge {",
		".scout-chat-msg.is-followup .scout-chat-msg__idrow {",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing author header hook %q", want)
		}
	}
	start := strings.Index(html, "function scoutChatMessageNode(kind, text, ts, files, authorLabel, viaScout = false)")
	end := strings.Index(html, "function appendChatMentionTextNodes")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope scoutChatMessageNode")
	}
	node := html[start:end]
	for _, gone := range []string{"avatar.className = 'scout-chat-msg__avatar'", "author.className = 'scout-chat-msg__author'"} {
		if strings.Contains(node, gone) {
			t.Fatalf("scoutChatMessageNode still builds the floating peer avatar / detached name: %q", gone)
		}
	}
	if testing.Short() {
		return
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');const http=require('http');const path=require('path');const assert=require('assert/strict');const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.CHAT_AUTHOR_INDEX,'utf8');const dictation=fs.readFileSync(path.join(path.dirname(process.env.CHAT_AUTHOR_INDEX),'public','composer-dictation.js'),'utf8');
const base=Date.parse('2026-09-02T12:00:00Z');
const at=s=>new Date(base+s*1000).toISOString();
const messages=[
 {id:'m1',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'Morning team',createdAt:at(0)},
 {id:'m2',kind:'message',role:'scout',authorName:'Scout',authorEmail:'scout@thebonfire.xyz',text:'Morning AJ — the deck is queued.',createdAt:at(10)},
 {id:'m3',kind:'message',role:'user',authorName:'Tim',authorEmail:'tim@shareability.com',text:'Looking at the fanout now',createdAt:at(20)},
 {id:'m4',kind:'message',role:'user',authorName:'Tim',authorEmail:'tim@shareability.com',text:'Second thought in the same run',createdAt:at(30)},
 {id:'m5',kind:'message',role:'user',authorName:'Tim',authorEmail:'tim@shareability.com',text:'Third — still one header',createdAt:at(40)},
 {id:'m6',kind:'message',role:'scout',authorName:'Scout',authorEmail:'scout@thebonfire.xyz',text:'Noted.',createdAt:at(50)},
];
const row={id:'authors',title:'Bonfire Chat',preview:'Noted.',ownerEmail:'aj@shareability.com',visibility:'public',createdAt:at(0),updatedAt:at(50)};
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation)}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}))}
 if(req.url==='/assistant/chat-threads?view=index'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,threads:[row]}))}
 if(req.url.startsWith('/assistant/chat-threads/authors?')){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,thread:{...row,messages},history:{mode:'tail',messageCount:messages.length,hasEarlier:false}}))}
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}')}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
const probe=()=>[...document.querySelectorAll('#scoutChatThread .scout-chat-msg')].map(m=>{const b=n=>{if(!n)return null;const r=n.getBoundingClientRect();return [Math.round(r.x),Math.round(r.y),Math.round(r.width),Math.round(r.height)]};const badge=m.querySelector('.scout-chat-msg__idbadge');const idrow=m.querySelector('.scout-chat-msg__idrow');return {cls:m.className,badge:b(badge),radius:badge?getComputedStyle(badge).borderRadius:'',idrowShown:Boolean(idrow)&&getComputedStyle(idrow).display!=='none',name:m.querySelector('.scout-chat-msg__idname')?.textContent||'',time:m.querySelector('.scout-chat-msg__idtime')?.textContent||'',text:b(m.querySelector('.scout-chat-text')),floating:Boolean(m.querySelector('.scout-chat-msg__avatar, .scout-chat-msg__author')),meta:m.querySelector('.scout-chat-msg__meta')?.textContent||''}});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const origin='http://127.0.0.1:'+server.address().port;const browser=await chromium.launch({headless:true});
 for(const viewport of [{width:1440,height:900},{width:390,height:844}]){
  const page=await browser.newPage({viewport,colorScheme:'dark'});const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(origin+'/chat',{waitUntil:'domcontentloaded'});await page.waitForSelector('#appShell.is-authed');
  if(viewport.width<700){await page.click('.chat-thread-item[data-thread-id="authors"]')}
  await page.waitForFunction(()=>document.querySelectorAll('#scoutChatThread .scout-chat-msg').length===6);await page.waitForTimeout(200);
  const rows=await page.evaluate(probe);const tag=viewport.width+'px '+JSON.stringify(rows);
  const scout=rows[1],tim=rows[2],tim2=rows[3],tim3=rows[4];
  assert.ok(scout.idrowShown&&scout.badge&&scout.name==='Scout',tag);
  // the human header row mirrors Scout's: same tile size and radius, name + time on the row, no floating disc / detached name
  assert.ok(tim.idrowShown,tag);assert.equal(tim.name,'Tim',tag);assert.ok(tim.time.length>0,tag);assert.equal(tim.floating,false,tag);
  assert.deepEqual([tim.badge[2],tim.badge[3]],[scout.badge[2],scout.badge[3]],'tile size differs from Scout: '+tag);assert.equal(tim.radius,scout.radius,'tile radius differs from Scout: '+tag);
  assert.ok(tim.badge[1]<tim.text[1],'header row is not above the bubble: '+tag);
  // bubbles sit on the same left edge as the tile — exactly Scout's anatomy
  assert.equal(tim.text[0],tim.badge[0],'human bubble not on the tile edge: '+tag);assert.equal(scout.text[0],scout.badge[0],tag);assert.equal(tim.text[0],scout.text[0],'human and Scout bubbles do not share a left edge: '+tag);
  // one header per run: follow-ups hide it and stay column-aligned
  assert.ok(tim2.cls.includes('is-followup')&&tim3.cls.includes('is-followup'),tag);assert.equal(tim2.idrowShown,false,tag);assert.equal(tim3.idrowShown,false,tag);assert.equal(tim2.text[0],tim.text[0],tag);assert.equal(tim2.floating,false,tag);
  // own messages keep the attribution line beneath a right-aligned bubble
  assert.ok(rows[0].cls.includes('scout-chat-msg--user')&&rows[0].meta.length>0&&!rows[0].idrowShown,tag);
  assert.equal(errors.length,0,JSON.stringify(errors));
  await page.close();
 }
 await browser.close();server.close();
})().catch(error=>{console.error(error);process.exit(1)});
`
	cmd := exec.Command("node", "-e", script)
	cmd.Dir = filepath.Dir(indexPath)
	cmd.Env = append(os.Environ(), "CHAT_AUTHOR_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered author header contract failed: %v\n%s", err, output)
	}
}
