package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebChatSportsCarStaticContract(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		"function captureScoutChatViewport(options = {})",
		"anchorMessageId:",
		"function patchActiveScoutChatMessage(previous, message)",
		"function appendActiveScoutChatMessage(thread, message, options = {})",
		"function trimScoutChatIncrementalFeed(options = {})",
		"function repairScoutChatRetainedBoundary()",
		"node !== scoutChatThinking",
		"records.length > scoutChatMaxNodes",
		"{ protectedNode: viewport?.anchor }",
		"var desktopChatReactionIntents = new Map()",
		"function flushDesktopChatReactionIntent(intent)",
		"queueMicrotask(() => void flushDesktopChatReactionIntent(intent))",
		"data-chat-reaction-message-id",
		"{ skipRail: patched }",
		"renderActiveScoutThread({ forceBottom: true })",
		"const boundedFeedMessages = feedMessages.slice(-scoutChatMaxNodes)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("web chat sports-car contract missing %q", want)
		}
	}
}

func TestWebChatSportsCarRenderedJourney(t *testing.T) {
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
const html=fs.readFileSync(process.env.CHAT_SPORTS_CAR_INDEX,'utf8');
const baseTime=Date.parse('2026-08-20T18:00:00Z');
const messages=Array.from({length:90},(_,index)=>({
 id:'message-'+index,kind:'message',role:'user',authorName:index%3===0?'AJ':'Tyler',
 authorEmail:index%3===0?'aj@shareability.com':'tyler@example.test',
 text:'Message '+index+' · '+('context '.repeat(7)),createdAt:new Date(baseTime+index*1000).toISOString(),reactions:[]
}));
const root=messages[60];
const replies=Array.from({length:16},(_,index)=>({id:'reply-'+(index+1),kind:'message',role:'user',authorName:'Tyler',authorEmail:'tyler@example.test',text:'Reply context '+(index+1)+' stays open. '+('detail '.repeat(8)),createdAt:new Date(baseTime+91000+index*1000).toISOString(),reactions:[],replyTo:{messageId:root.id,authorName:root.authorName,text:root.text}}));
const reply=replies[0];
messages.push(...replies);
let serverThread={id:'channel-fast',title:'Country Golf',visibility:'public',messagesLoaded:true,preview:'fast chat',createdAt:new Date(baseTime).toISOString(),updatedAt:new Date(baseTime+92000).toISOString(),messages};
let reactionRequests=[];
let failedHeart=false;
const server=http.createServer((req,res)=>{
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
 if(req.url==='/assistant/chat-threads?view=index'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,threads:[]}));}
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
function clone(value){return JSON.parse(JSON.stringify(value));}
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:900}});
 await page.route(/\/assistant\/chat-threads\/channel-fast\/messages\/[^/]+\/reaction/,async route=>{
   const request=route.request();
   const url=new URL(request.url());
   const messageId=decodeURIComponent(url.pathname.split('/').slice(-2,-1)[0]);
   const posted=request.method()==='PUT'?JSON.parse(request.postData()||'{}'):{};
   const emoji=String(posted.emoji||url.searchParams.get('emoji')||'');
   reactionRequests.push({messageId,emoji,method:request.method(),at:Date.now()});
   await new Promise(resolve=>setTimeout(resolve,450));
   if(emoji==='❤️'&&!failedHeart){failedHeart=true;return route.fulfill({status:500,contentType:'application/json',body:JSON.stringify({error:'fixture rejection'})});}
   const message=serverThread.messages.find(candidate=>candidate.id===messageId);
   const mine=reaction=>reaction.emoji===emoji&&reaction.actorEmail==='aj@shareability.com';
   message.reactions=(message.reactions||[]).filter(reaction=>!mine(reaction));
   if(request.method()==='PUT')message.reactions.push({emoji,actorEmail:'aj@shareability.com',actorName:'AJ'});
   serverThread.updatedAt=new Date(Date.parse(serverThread.updatedAt)+1000).toISOString();
   return route.fulfill({status:200,contentType:'application/json',body:JSON.stringify({ok:true,thread:clone(serverThread)})});
 });
 await page.goto('http://127.0.0.1:'+server.address().port+'/chat',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.waitForFunction(()=>document.getElementById('appShell').dataset.tool==='chat'&&getComputedStyle(document.getElementById('chatTool')).display!=='none');
 await page.evaluate(thread=>{
   scoutChatThreads=[thread];activeScoutThreadId=thread.id;renderActiveScoutThread({forceBottom:true});
 },clone(serverThread));
	 const reactionButton=(messageId,emoji,context=false)=>page.locator((context?'#chatContextRail ':'#scoutChatThread ')+'[data-message-id="'+messageId+'"] [data-chat-reaction-emoji="'+emoji+'"]:not([data-chat-reaction-summary])').first();

 // Immediate paint plus latest-intent coalescing: true -> false -> true while
 // one PUT is in flight results in one request and never waits to feel pressed.
 const like=reactionButton('message-70','👍');
 const started=Date.now();
	 await like.evaluate(node=>node.click());
 assert.equal(await like.getAttribute('aria-pressed'),'true');
 assert.ok(Date.now()-started<300,'optimistic reaction waited for the delayed response');
	 await like.evaluate(node=>node.click());
	 await like.evaluate(node=>node.click());
 assert.equal(await like.getAttribute('aria-pressed'),'true');
 await page.waitForTimeout(650);
 assert.equal(reactionRequests.filter(item=>item.messageId==='message-70'&&item.emoji==='👍').length,1,'rapid latest intent was not coalesced');
 assert.equal(await like.getAttribute('aria-busy'),'false');

 // Failure restores the last server-owned state and remains keyboard-legible.
 const heart=reactionButton('message-71','❤️');
 await heart.focus();
 await page.keyboard.press('Enter');
 assert.equal(await heart.getAttribute('aria-pressed'),'true');
 await page.waitForTimeout(600);
 assert.equal(await heart.getAttribute('aria-pressed'),'false');
 assert.equal(await heart.getAttribute('aria-busy'),'false');

 // A reaction changes only its keyed surfaces: the exact visible message and
 // pixel offset remain fixed, and focus stays on the same DOM control.
 await page.locator('[data-message-id="message-35"]').evaluate(node=>node.scrollIntoView({block:'start'}));
	 const fire=reactionButton('message-35','🔥');
	 await fire.focus();
	 const anchorBefore=await page.locator('[data-message-id="message-35"]').evaluate(node=>node.getBoundingClientRect().top);
 await page.keyboard.press('Enter');
 const anchorAfter=await page.locator('[data-message-id="message-35"]').evaluate(node=>node.getBoundingClientRect().top);
	 assert.ok(Math.abs(anchorAfter-anchorBefore)<=1,'reaction anchor drifted '+(anchorAfter-anchorBefore)+'px');
	 assert.equal(await fire.evaluate(node=>node===document.activeElement),true);
	 await page.waitForTimeout(600);

	 // Reply-context reactions share the optimistic keyed projection without
	 // rebuilding or closing the rail, losing the draft, or replacing focus.
	 await page.evaluate(()=>{
	   const thread=scoutChatThreads.find(candidate=>candidate.id==='channel-fast');
	   const root=thread.messages.find(message=>message.id==='message-60');
	   openDesktopMessageContext(root,document.querySelector('[data-message-id="message-60"]'));
	 });
	 await page.locator('#chatContextRail').waitFor({state:'visible'});
	 await page.locator('#chatContextReplyInput').fill('reply draft survives');
	 const replyLike=reactionButton('reply-1','👍',true);
	 await replyLike.focus();
	 await page.keyboard.press('Enter');
	 assert.equal(await replyLike.getAttribute('aria-pressed'),'true');
	 await page.waitForTimeout(600);
	 assert.equal(await page.locator('#chatContextRail').isVisible(),true);
	 assert.equal(await page.locator('#chatContextReplyInput').inputValue(),'reply draft survives');
	 assert.equal(await replyLike.evaluate(node=>node===document.activeElement),true);

	 // Editing a much earlier reply uses the reply rail's own element+offset
	 // anchor; hostile height growth above the reader causes no rail drift.
	 await page.locator('#chatContextBody [data-message-id="reply-9"]').evaluate(node=>node.scrollIntoView({block:'start'}));
	 const replyAnchor=await page.evaluate(()=>{
	   const thread=scoutChatThreads.find(candidate=>candidate.id==='channel-fast');
	   const current=thread.messages.find(message=>message.id==='reply-1');
	   const anchor=document.querySelector('#chatContextBody [data-message-id="reply-9"]');
	   const before=anchor.getBoundingClientRect().top;
	   const nextUpdatedAt=new Date(Date.parse(thread.updatedAt)+1000).toISOString();
	   handleChatThreadEvent({id:thread.id,updatedAt:nextUpdatedAt,message:{...current,text:'Expanded reply '+('substantial reply line '.repeat(90)),editedAt:nextUpdatedAt}});
	   return {before,after:anchor.getBoundingClientRect().top,draft:chatContextReplyInput.value};
	 });
	 assert.ok(Math.abs(replyAnchor.after-replyAnchor.before)<=1,'reply rail anchor drifted '+JSON.stringify(replyAnchor));
	 assert.equal(replyAnchor.draft,'reply draft survives');

	 // An edit above the viewport grows materially without moving the reader's
	 // chosen anchor. The composer selection and draft survive the socket patch.
	 await page.locator('[data-message-id="message-35"]').evaluate(node=>node.scrollIntoView({block:'start'}));
	 await page.locator('#scoutChatInput').fill('draft selection stays here');
 await page.locator('#scoutChatInput').evaluate(input=>input.setSelectionRange(6,15));
 const editResult=await page.evaluate(()=>{
   const thread=scoutChatThreads.find(candidate=>candidate.id==='channel-fast');
   const current=thread.messages.find(message=>message.id==='message-5');
   const edited={...current,text:'Expanded edit '+('substantial new line '.repeat(80)),editedAt:'2026-08-20T18:03:00Z'};
   const anchor=document.querySelector('[data-message-id="message-35"]');
   const before=anchor.getBoundingClientRect().top;
	   const nextUpdatedAt=new Date(Date.parse(thread.updatedAt)+1000).toISOString();
	   handleChatThreadEvent({id:thread.id,updatedAt:nextUpdatedAt,message:{...edited,editedAt:nextUpdatedAt}});
   return {before,after:anchor.getBoundingClientRect().top,selection:[scoutChatInput.selectionStart,scoutChatInput.selectionEnd],draft:scoutChatInput.value};
 });
 assert.ok(Math.abs(editResult.after-editResult.before)<=1,'edited content changed reader anchor '+JSON.stringify(editResult));
 assert.deepEqual(editResult.selection,[6,15]);
 assert.equal(editResult.draft,'draft selection stays here');

 // Incoming peer work does not pull an older reader away. At the tail the
 // same path follows new comments; an own comment always navigates to bottom.
 const older=await page.evaluate(()=>{
   const anchor=document.querySelector('[data-message-id="message-35"]');
   const before=anchor.getBoundingClientRect().top;
   handleChatThreadEvent({id:'channel-fast',updatedAt:'2026-08-20T18:03:02Z',message:{id:'peer-new',kind:'message',role:'user',authorName:'Tyler',authorEmail:'tyler@example.test',text:'A new peer comment',createdAt:'2026-08-20T18:03:02Z',reactions:[]}});
   return {before,after:anchor.getBoundingClientRect().top,bottom:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight};
 });
 assert.ok(Math.abs(older.after-older.before)<=1,'incoming peer moved older reader '+JSON.stringify(older));
 assert.ok(older.bottom>48,'older reader was pulled to bottom');
 const tail=await page.evaluate(()=>{
   scoutChatThread.scrollTop=scoutChatThread.scrollHeight;
   handleChatThreadEvent({id:'channel-fast',updatedAt:'2026-08-20T18:03:03Z',message:{id:'peer-tail',kind:'message',role:'user',authorName:'Tyler',authorEmail:'tyler@example.test',text:'Tail update',createdAt:'2026-08-20T18:03:03Z',reactions:[]}});
   return scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight;
 });
 assert.ok(tail<=1,'near-bottom reader did not follow new comment: '+tail);
 const ownBottom=await page.evaluate(()=>{
   document.querySelector('[data-message-id="message-45"]').scrollIntoView({block:'start'});
   handleChatThreadEvent({id:'channel-fast',updatedAt:'2026-08-20T18:03:04Z',message:{id:'own-new',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'My new comment',createdAt:'2026-08-20T18:03:04Z',reactions:[]}});
   return scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight;
 });
	 assert.ok(ownBottom<=1,'own comment did not pin to bottom: '+ownBottom);

	 // Sustained socket traffic remains incremental and hard-bounded. A focused
	 // root at the reader's anchor is retained with its live reply summary while
	 // the oldest other records are evicted and the boundary is repaired.
	 const retainedRoot=page.locator('#scoutChatThread > [data-message-id="message-60"]');
	 await retainedRoot.evaluate(node=>node.scrollIntoView({block:'start'}));
	 const retainedSummary=retainedRoot.locator('.desktop-chat-thread-summary');
	 await retainedSummary.focus();
	 const sustained=await page.evaluate(()=>{
	   const retained=document.querySelector('#scoutChatThread > [data-message-id="message-60"]');
	   const anchor=captureScoutChatViewport().anchor;
	   const before=anchor.getBoundingClientRect().top-scoutChatThread.getBoundingClientRect().top;
	   const samples=[];
	   for(let index=0;index<230;index+=1){
	     const thread=scoutChatThreads.find(candidate=>candidate.id==='channel-fast');
	     const updatedAt=new Date(Date.parse(thread.updatedAt)+1000).toISOString();
	     handleChatThreadEvent({id:thread.id,updatedAt,message:{id:'sustained-'+index,kind:'message',role:'user',authorName:'Tyler',authorEmail:'tyler@example.test',text:'Incremental socket message '+index,createdAt:updatedAt,reactions:[]}});
	     const now=anchor.getBoundingClientRect().top-scoutChatThread.getBoundingClientRect().top;
	     if(Math.abs(now-before)>1&&samples.length<5)samples.push({index,now,scrollTop:scoutChatThread.scrollTop,records:scoutChatFeedRecordNodes().length});
	   }
	   const records=scoutChatFeedRecordNodes();
	   return {
	     before,after:anchor.getBoundingClientRect().top-scoutChatThread.getBoundingClientRect().top,samples,anchorMessageId:anchor.dataset.messageId||'',eligible:records.length,
	     messages:scoutChatThread.querySelectorAll(':scope > .scout-chat-msg').length,
	     thinkingConnected:scoutChatThinking.isConnected,
	     activeMessageId:document.activeElement?.closest?.('[data-message-id]')?.dataset?.messageId||'',
	     summary:retained.querySelector('.desktop-chat-thread-summary')?.textContent||'',
	     contextOpen:!chatContextRail.hidden,
	     distanceFromBottom:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight,
	     retainedBoundary:Boolean(scoutChatThread.querySelector(':scope > .scout-chat-daybreak[data-retained-boundary="true"]')),
	   };
	 });
	 assert.ok(sustained.eligible<=200&&sustained.messages<=200,JSON.stringify(sustained));
	 assert.equal(sustained.thinkingConnected,true);
	 assert.equal(sustained.activeMessageId,'message-60');
	 assert.match(sustained.summary,/16 replies/);
	 assert.equal(sustained.contextOpen,true);
	 assert.equal(sustained.retainedBoundary,true);
	 assert.ok(sustained.distanceFromBottom>48,JSON.stringify(sustained));
	 assert.ok(Math.abs(sustained.after-sustained.before)<=1,'sustained append anchor drifted '+JSON.stringify(sustained));
	 const sustainedTail=await page.evaluate(()=>{
	   let thread=scoutChatThreads.find(candidate=>candidate.id==='channel-fast');
	   let updatedAt=new Date(Date.parse(thread.updatedAt)+1000).toISOString();
	   handleChatThreadEvent({id:thread.id,updatedAt,message:{id:'sustained-own',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'My bounded tail message',createdAt:updatedAt,reactions:[]}});
	   const ownDistance=scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight;
	   thread=scoutChatThreads.find(candidate=>candidate.id==='channel-fast');
	   updatedAt=new Date(Date.parse(thread.updatedAt)+1000).toISOString();
	   handleChatThreadEvent({id:thread.id,updatedAt,message:{id:'sustained-peer-tail',kind:'message',role:'user',authorName:'Tyler',authorEmail:'tyler@example.test',text:'Peer follows at bounded tail',createdAt:updatedAt,reactions:[]}});
	   return {ownDistance,peerDistance:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight,eligible:scoutChatFeedRecordNodes().length};
	 });
	 assert.ok(sustainedTail.ownDistance<=1&&sustainedTail.peerDistance<=1,JSON.stringify(sustainedTail));
	 assert.ok(sustainedTail.eligible<=200,JSON.stringify(sustainedTail));

	 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CHAT_SPORTS_CAR_INDEX="+indexPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered web chat sports-car journey failed: %v\n%s", err, output)
	}
}
