package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatMutationSourceFenceStaticContract(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		"function captureScoutChatMutationScope(threadId = activeScoutThreadId)",
		"scope.generation === chatThreadsGeneration",
		"function scoutChatMutationSourceIsActive(scope)",
		"upsertScoutChatThread(merged, { select: false })",
		"if (scoutChatMutationSourceIsActive(mutationScope))",
		"desktopChatReactionIntents.forEach(intent => { intent.cancelled = true })",
		"function desktopChatReactionIntentIsCurrent(intent)",
		"if (!desktopChatReactionIntentIsCurrent(intent)) return",
		"generation: chatThreadsGeneration",
		"account: String(authedUser?.email || '').trim().toLowerCase()",
		"finishScoutChatThinking(mutationScope)",
		"const mutationScope = captureScoutChatMutationScope(state.threadId)",
		"if (!scoutChatMutationPayloadMatchesSource(mutationScope, result.data.thread)) return",
		"const scoutChatFailedSendRecoveries = new Map()",
		"rememberScoutChatFailedSendRecovery(mutationScope",
		"not sent · draft restored",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("chat mutation source/auth fence missing %q", want)
		}
	}
}

func TestRenderedChatMutationsStayBoundToSourceAndAuthGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');const http=require('http');const path=require('path');const assert=require('assert/strict');const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.CHAT_MUTATION_INDEX,'utf8');const dictation=fs.readFileSync(path.join(path.dirname(process.env.CHAT_MUTATION_INDEX),'public','composer-dictation.js'),'utf8');
const base=Date.parse('2026-08-22T20:00:00Z');
const message=(id,text,index,authorEmail='aj@shareability.com')=>({id,kind:'message',role:'user',authorName:authorEmail.startsWith('tyler')?'Tyler':'AJ',authorEmail,text,createdAt:new Date(base+index*1000).toISOString()});
const aBase={id:'thread-a',title:'Source channel',preview:'source root',ownerEmail:'aj@shareability.com',visibility:'public',createdAt:new Date(base).toISOString(),updatedAt:new Date(base+1000).toISOString(),messagesLoaded:true,messages:[message('a-root','source root',1)]};
const riffA={id:'riff-a',title:'Private Riff',preview:'private source',ownerEmail:'aj@shareability.com',visibility:'private',createdAt:new Date(base).toISOString(),updatedAt:new Date(base+1500).toISOString(),messagesLoaded:true,riff:{sourceThreadId:'thread-a',sourceTitle:'Source channel',throughMessageId:'a-root',sourceAvailable:true,activeEpisodeId:'episode-a',viewedEpisodeId:'episode-a',episodeCount:1,agentName:'Scout'},messages:[{...message('riff-root','private source',1),riffEpisodeId:'episode-a'}]};
const bMessages=Array.from({length:70},(_,i)=>message('b-'+i,'Destination message '+i,i+2,'tyler@example.com'));
const bBase={id:'thread-b',title:'Destination channel',preview:'destination',ownerEmail:'tyler@example.com',visibility:'public',createdAt:new Date(base).toISOString(),updatedAt:new Date(base+80000).toISOString(),messagesLoaded:true,messages:bMessages};
const sharedBRoot=message('shared-root','same ids across sessions',1,'tyler@example.com');const sharedB={id:'shared',title:'Shared collision',visibility:'public',ownerEmail:'tyler@example.com',createdAt:new Date(base).toISOString(),updatedAt:new Date(base+2000).toISOString(),messagesLoaded:true,messages:[sharedBRoot]};
const held={send:[],sendFail:[],reply:[],riff:[],edit:[],reactionSwitch:[],reactionAuth:[]};let sharedReactionOrdinal=0;let authEmail='aj@shareability.com';
const release=(kind,index=0)=>{const item=held[kind][index];assert.ok(item,'missing held '+kind+' '+index);item();};
const waitHeld=async(kind,count=1)=>{const until=Date.now()+5000;while(held[kind].length<count&&Date.now()<until)await new Promise(r=>setTimeout(r,10));assert.ok(held[kind].length>=count,'timed out waiting for '+kind)};
const json=(res,status,payload)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify(payload))};
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation)}
 if(req.url==='/auth/me')return json(res,200,{email:authEmail,name:authEmail.startsWith('tyler')?'Tyler':'AJ',shellAccess:'full'});
 if(req.url==='/assistant/chat-threads?view=index')return json(res,200,{ok:true,threads:authEmail.startsWith('tyler')?[sharedB]:[aBase,bBase]});
 if(req.url.startsWith('/assistant/chat-threads/')&&req.method==='POST'&&req.url.endsWith('/messages')){let raw='';req.on('data',chunk=>raw+=chunk);return req.on('end',()=>{const body=JSON.parse(raw||'{}');const failed=body.text==='fail from source';const isReply=Boolean(body.replyToMessageId);const isRiff=body.text==='private riff A';const source=isRiff?riffA:aBase;const sent=message(isReply?'a-reply':isRiff?'riff-sent':'a-sent',body.text,isReply?105:100);if(isReply)sent.replyTo={messageId:'a-root',rootMessageId:'a-root',authorName:'AJ',authorEmail:'aj@shareability.com',text:'source root'};if(isRiff)sent.riffEpisodeId='episode-a';const payload={ok:true,thread:{...source,updatedAt:new Date(base+(isReply?105000:100000)).toISOString(),messages:[...source.messages,sent]},message:sent,history:{mode:'tail',messageCount:2,hasEarlier:false,replyCounts:isReply?{'a-root':1}:undefined}};held[isRiff?'riff':isReply?'reply':failed?'sendFail':'send'].push(()=>failed?json(res,500,{error:'source send failed'}):json(res,200,payload))})}
 if(req.url.includes('/thread-a/messages/a-root')&&req.method==='PATCH'){let raw='';req.on('data',chunk=>raw+=chunk);return req.on('end',()=>{const edited={...aBase.messages[0],text:JSON.parse(raw).text,editedAt:new Date(base+110000).toISOString()};held.edit.push(()=>json(res,200,{ok:true,thread:{...aBase,updatedAt:new Date(base+110000).toISOString(),messages:[edited]},message:edited,history:{mode:'tail',messageCount:1,hasEarlier:false}}))})}
 if(req.url.includes('/thread-a/messages/a-root/reaction')){const reacted={...aBase.messages[0],reactions:[{emoji:'❤️',actorEmail:'aj@shareability.com',actorName:'AJ',createdAt:new Date(base+120000).toISOString()}]};return held.reactionSwitch.push(()=>json(res,200,{ok:true,thread:{...aBase,updatedAt:new Date(base+120000).toISOString(),messages:[reacted]},message:reacted,history:{mode:'tail',messageCount:1,hasEarlier:false}}))}
 if(req.url.includes('/shared/messages/shared-root/reaction')){const ordinal=sharedReactionOrdinal++;const actor=ordinal===0?{email:'aj@shareability.com',name:'AJ'}:{email:'tyler@example.com',name:'Tyler'};const reacted=message('shared-root','same ids across sessions',1,actor.email);reacted.reactions=[{emoji:'❤️',actorEmail:actor.email,actorName:actor.name,createdAt:new Date(base+130000+ordinal*1000).toISOString()}];const thread={id:'shared',title:'Shared collision',visibility:'public',ownerEmail:actor.email,createdAt:new Date(base).toISOString(),updatedAt:new Date(base+130000+ordinal*1000).toISOString(),messagesLoaded:true,messages:[reacted]};return held.reactionAuth.push(()=>json(res,200,{ok:true,thread,message:reacted,history:{mode:'tail',messageCount:1,hasEarlier:false}}))}
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')||req.url.startsWith('/artifacts'))return json(res,503,{});
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
const settle=async page=>{await page.waitForFunction(()=>!scoutChatProgressiveProjection||scoutChatThread.dataset.projectionState==='complete');await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))))};
const select=async(page,id)=>{await page.evaluate(id=>{activeScoutThreadId=id;renderActiveScoutThread()},id);await settle(page)};
const destinationSnapshot=page=>page.evaluate(()=>{const anchor=document.querySelector('[data-message-id="b-35"]');anchor?.scrollIntoView({block:'start'});return {active:activeScoutThreadId,top:anchor?.getBoundingClientRect().top,scrollTop:scoutChatThread.scrollTop,text:scoutChatThread.textContent}});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:900}});const errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('http://127.0.0.1:'+server.address().port+'/chat',{waitUntil:'domcontentloaded'});await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(({aBase,bBase,riffA})=>{scoutChatThreads=[structuredClone(aBase),structuredClone(bBase),structuredClone(riffA)];activeScoutThreadId='thread-a';renderActiveScoutThread()},{aBase,bBase,riffA});await settle(page);

 // A successful send may repair the source cache, but switching to B while it
 // is in flight must never select A, rebuild B, or force B to its bottom.
 await page.evaluate(()=>{window.__sourceSend=sendScoutChatViaOffice('sent from source')});await waitHeld('send');await select(page,'thread-b');const sendBefore=await destinationSnapshot(page);release('send');await page.evaluate(()=>window.__sourceSend);await settle(page);const sendAfter=await page.evaluate(()=>{const anchor=document.querySelector('[data-message-id="b-35"]');return {active:activeScoutThreadId,top:anchor?.getBoundingClientRect().top,scrollTop:scoutChatThread.scrollTop,text:scoutChatThread.textContent,source:scoutChatThreads.find(t=>t.id==='thread-a')?.messages?.some(m=>m.id==='a-sent')};});assert.equal(sendAfter.active,'thread-b');assert.equal(sendAfter.source,true);assert.ok(Math.abs(sendAfter.top-sendBefore.top)<=1,JSON.stringify({sendBefore,sendAfter}));assert.equal(sendAfter.scrollTop,sendBefore.scrollTop);assert.doesNotMatch(sendAfter.text,/sent from source/);

 // A failed source send is equally fenced: no error card or preview can land
 // in the destination channel after navigation. Returning to the source must
 // recover the exact draft and an honest not-sent receipt.
 await select(page,'thread-a');await page.evaluate(()=>{window.__failedSend=sendScoutChatViaOffice('fail from source')});await waitHeld('sendFail');await select(page,'thread-b');const failBefore=await destinationSnapshot(page);release('sendFail');await page.evaluate(()=>window.__failedSend);const failAfter=await page.evaluate(()=>({active:activeScoutThreadId,text:scoutChatThread.textContent,top:document.querySelector('[data-message-id="b-35"]')?.getBoundingClientRect().top}));assert.equal(failAfter.active,'thread-b');assert.doesNotMatch(failAfter.text,/source send failed|not sent|fail from source/);assert.ok(Math.abs(failAfter.top-failBefore.top)<=1,JSON.stringify({failBefore,failAfter}));
 await select(page,'thread-a');const recovered=await page.evaluate(()=>({draft:scoutChatInput.value,feed:scoutChatThread.textContent,recovery:document.querySelector('[data-failed-send-recovery="true"]')?.dataset.delivery||''}));assert.equal(recovered.draft,'fail from source',JSON.stringify(recovered));assert.match(recovered.feed,/fail from source/);assert.match(recovered.feed,/not sent.*draft restored/);assert.equal(recovered.recovery,'failed',JSON.stringify(recovered));await page.evaluate(()=>{scoutChatFailedSendRecoveries.clear();scoutChatInput.value='';renderActiveScoutThread()});

 // The thread-rail composer is another send surface. Its bounded repair page
 // must merge into A without clearing or scrolling whichever context B owns.
 await select(page,'thread-a');await page.evaluate(()=>{chatContextState={mode:'thread',threadId:'thread-a',rootMessageId:'a-root'};chatContextReplyInput.value='reply only in A';window.__threadReply=submitDesktopThreadReply(new Event('submit',{cancelable:true}))});await waitHeld('reply');await select(page,'thread-b');const replyBefore=await destinationSnapshot(page);release('reply');await page.evaluate(()=>window.__threadReply);await page.waitForFunction(()=>scoutChatThreads.find(t=>t.id==='thread-a')?.messages?.some(m=>m.id==='a-reply'));const replyAfter=await page.evaluate(()=>({active:activeScoutThreadId,text:scoutChatThread.textContent,top:document.querySelector('[data-message-id="b-35"]')?.getBoundingClientRect().top}));assert.equal(replyAfter.active,'thread-b');assert.doesNotMatch(replyAfter.text,/reply only in A/);assert.ok(Math.abs(replyAfter.top-replyBefore.top)<=1,JSON.stringify({replyBefore,replyAfter}));

 // Feed editing uses the same source fence and select:false repair merge.
 await select(page,'thread-a');await page.evaluate(()=>{const thread=selectedScoutChatThread();const msg=thread.messages.find(m=>m.id==='a-root');const node=document.querySelector('[data-message-id="a-root"]');beginDesktopFeedMessageEdit(thread,msg,node);const form=node.querySelector('.chat-context-card__editor');form.querySelector('textarea').value='edited only in A';form.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}))});await waitHeld('edit');await select(page,'thread-b');const editBefore=await destinationSnapshot(page);release('edit');await page.waitForFunction(()=>scoutChatThreads.find(t=>t.id==='thread-a')?.messages?.find(m=>m.id==='a-root')?.text==='edited only in A');const editAfter=await page.evaluate(()=>({active:activeScoutThreadId,text:scoutChatThread.textContent,top:document.querySelector('[data-message-id="b-35"]')?.getBoundingClientRect().top}));assert.equal(editAfter.active,'thread-b');assert.doesNotMatch(editAfter.text,/edited only in A/);assert.ok(Math.abs(editAfter.top-editBefore.top)<=1,JSON.stringify({editBefore,editAfter}));

 // Reactions repair A in memory without repainting or moving B.
 await select(page,'thread-a');await page.evaluate(()=>updateDesktopChatReaction('a-root','❤️',true));await waitHeld('reactionSwitch');await select(page,'thread-b');const reactionBefore=await destinationSnapshot(page);release('reactionSwitch');await page.waitForFunction(()=>scoutChatThreads.find(t=>t.id==='thread-a')?.messages?.find(m=>m.id==='a-root')?.reactions?.length===1);const reactionAfter=await page.evaluate(()=>({active:activeScoutThreadId,text:scoutChatThread.textContent,top:document.querySelector('[data-message-id="b-35"]')?.getBoundingClientRect().top}));assert.equal(reactionAfter.active,'thread-b');assert.ok(Math.abs(reactionAfter.top-reactionBefore.top)<=1,JSON.stringify({reactionBefore,reactionAfter}));

 // The strongest collision: A logs out with a request in flight, B logs in
 // and creates the same thread/message/emoji intent. A's completion cannot
 // merge, delete B's intent, or trigger a recursive request in B's authority.
 const sharedA={id:'shared',title:'Shared collision',visibility:'public',ownerEmail:'aj@shareability.com',createdAt:new Date(base).toISOString(),updatedAt:new Date(base+1000).toISOString(),messagesLoaded:true,messages:[message('shared-root','same ids across sessions',1)]};
 await page.evaluate(shared=>{scoutChatThreads=[structuredClone(shared)];activeScoutThreadId='shared';renderActiveScoutThread();updateDesktopChatReaction('shared-root','❤️',true)},sharedA);await waitHeld('reactionAuth',1);
 authEmail='tyler@example.com';await page.evaluate(({base})=>{authedUser=null;resetPersistedScoutChatState();authedUser={email:'tyler@example.com',name:'Tyler',shellAccess:'full'};const root={id:'shared-root',kind:'message',role:'user',authorName:'Tyler',authorEmail:'tyler@example.com',text:'same ids across sessions',createdAt:new Date(base+1000).toISOString()};scoutChatThreads=[{id:'shared',title:'Shared collision',visibility:'public',ownerEmail:'tyler@example.com',createdAt:new Date(base).toISOString(),updatedAt:new Date(base+2000).toISOString(),messagesLoaded:true,messages:[root]}];activeScoutThreadId='shared';renderActiveScoutThread();updateDesktopChatReaction('shared-root','❤️',true)},{base});await waitHeld('reactionAuth',2);
	let authority=await page.evaluate(()=>({account:String(authedUser?.email||''),generation:chatThreadsGeneration,intents:[...desktopChatReactionIntents.values()].map(i=>({account:i.account,generation:i.generation,cancelled:i.cancelled}))}));assert.equal(authority.intents.length,1,'one B intent '+JSON.stringify(authority));assert.equal(authority.intents[0].account,'tyler@example.com','B intent authority '+JSON.stringify(authority));assert.equal(authority.intents[0].generation,authority.generation,'B generation '+JSON.stringify(authority));
 release('reactionAuth',0);await page.waitForTimeout(80);authority=await page.evaluate(()=>({account:String(authedUser?.email||''),active:activeScoutThreadId,reactions:desktopChatMessageById('shared','shared-root')?.reactions||[],intents:[...desktopChatReactionIntents.values()].map(i=>i.account)}));assert.equal(authority.account,'tyler@example.com');assert.equal(authority.active,'shared');assert.deepEqual(authority.reactions,[]);assert.deepEqual(authority.intents,['tyler@example.com']);assert.equal(held.reactionAuth.length,2,'stale A intent recursively issued a B-authority request');
	release('reactionAuth',1);await page.waitForFunction(()=>desktopChatMessageById('shared','shared-root')?.reactions?.[0]?.actorEmail==='tyler@example.com');authority=await page.evaluate(()=>({account:String(authedUser?.email||''),reactions:desktopChatMessageById('shared','shared-root').reactions,intents:desktopChatReactionIntents.size,active:activeScoutThreadId}));assert.equal(authority.active,'shared','B active '+JSON.stringify(authority));assert.equal(authority.reactions.length,1,'B reaction count '+JSON.stringify(authority));assert.equal(authority.reactions[0].actorEmail,'tyler@example.com','B reaction actor '+JSON.stringify(authority));assert.equal(authority.intents,0,'B intent cleanup '+JSON.stringify(authority));

 // Private Riff is a separate send surface. A delayed A response may not
 // inject its private thread, clear B's rail draft, or repaint B after login.
 authEmail='aj@shareability.com';await page.evaluate(({aBase,riffA})=>{resetPersistedScoutChatState();authedUser={email:'aj@shareability.com',name:'AJ',shellAccess:'full'};scoutChatThreads=[structuredClone(aBase),structuredClone(riffA)];activeScoutThreadId='thread-a';chatContextState={mode:'riff',threadId:'riff-a',pending:false};chatContextReplyInput.value='private riff A';window.__riffSend=submitDesktopThreadReply(new Event('submit',{cancelable:true}))},{aBase,riffA});await waitHeld('riff');
 authEmail='tyler@example.com';await page.evaluate(({bBase})=>{resetPersistedScoutChatState();authedUser={email:'tyler@example.com',name:'Tyler',shellAccess:'full'};scoutChatThreads=[structuredClone(bBase)];activeScoutThreadId='thread-b';chatContextState={mode:'thread',threadId:'thread-b',rootMessageId:'b-0'};chatContextReplyInput.value='B draft stays';renderActiveScoutThread()},{bBase});const riffBefore=await destinationSnapshot(page);release('riff');await page.evaluate(()=>window.__riffSend);const riffAfter=await page.evaluate(()=>({account:String(authedUser?.email||''),active:activeScoutThreadId,threadIds:scoutChatThreads.map(t=>t.id),draft:chatContextReplyInput.value,mode:chatContextState?.mode,text:scoutChatThread.textContent,top:document.querySelector('[data-message-id="b-35"]')?.getBoundingClientRect().top}));assert.equal(riffAfter.account,'tyler@example.com',JSON.stringify(riffAfter));assert.equal(riffAfter.active,'thread-b');assert.deepEqual(riffAfter.threadIds,['thread-b']);assert.equal(riffAfter.draft,'B draft stays');assert.equal(riffAfter.mode,'thread');assert.doesNotMatch(riffAfter.text,/private riff A|private source/);assert.ok(Math.abs(riffAfter.top-riffBefore.top)<=1,JSON.stringify({riffBefore,riffAfter}));
 assert.deepEqual(errors,[]);await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CHAT_MUTATION_INDEX="+indexPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered mutation source fence failed: %v\n%s", err, output)
	}
}
