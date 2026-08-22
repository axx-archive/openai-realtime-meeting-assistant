package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDenseChatTailRequestAndEarlierMergeRendered(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');const http=require('http');const path=require('path');const assert=require('assert/strict');const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.CHAT_TAIL_INDEX,'utf8');const dictation=fs.readFileSync(path.join(path.dirname(process.env.CHAT_TAIL_INDEX),'public','composer-dictation.js'),'utf8');
const base=Date.parse('2026-08-22T12:00:00Z');
const all=Array.from({length:240},(_,index)=>({id:'m'+index,kind:'message',role:index%2?'scout':'user',authorName:index%2?'Scout':'AJ',authorEmail:index%2?'scout@thebonfire.xyz':'aj@shareability.com',text:'Message '+index+' '+('bounded history '.repeat(6)),createdAt:new Date(base+index*1000).toISOString()}));
const row={id:'dense-tail',title:'Like A Farmer',preview:'Message 239',ownerEmail:'aj@shareability.com',visibility:'public',createdAt:new Date(base).toISOString(),updatedAt:new Date(base+239000).toISOString()};
const detailRequests=[];
const server=http.createServer((req,res)=>{
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation)}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}))}
 if(req.url==='/assistant/chat-threads?view=index'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,threads:[row]}))}
 if(req.url.startsWith('/assistant/chat-threads/dense-tail?')){
   detailRequests.push(req.url);const url=new URL(req.url,'http://local');const before=url.searchParams.get('before')||'';
   assert.equal(url.searchParams.get('view'),'tail');assert.equal(url.searchParams.get('limit'),'80');
   const messages=before==='m80'?all.slice(0,80):before==='m160'?all.slice(80,160):all.slice(160);
   const history=before==='m80'
     ?{mode:'tail',messageCount:80,hasEarlier:false,oldestMessageId:'m0',newestMessageId:'m79'}
     :before==='m160'
       ?{mode:'tail',messageCount:80,hasEarlier:true,nextBeforeMessageId:'m80',oldestMessageId:'m80',newestMessageId:'m159'}
       :{mode:'tail',messageCount:80,hasEarlier:true,nextBeforeMessageId:'m160',oldestMessageId:'m160',newestMessageId:'m239'};
   res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,thread:{...row,messages},history}));
 }
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}')}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:900}});const errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('http://127.0.0.1:'+server.address().port+'/chat',{waitUntil:'domcontentloaded'});await page.waitForSelector('#appShell.is-authed');await page.waitForFunction(()=>selectedScoutChatThread()?.messagesLoaded===true);await page.waitForFunction(()=>scoutChatThread.dataset.projectionState==='complete'||!scoutChatProgressiveProjection);
 const first=await page.evaluate(()=>({messages:selectedScoutChatThread().messages.length,first:selectedScoutChatThread().messages[0]?.id,last:selectedScoutChatThread().messages.at(-1)?.id,dom:document.querySelectorAll('#scoutChatThread > .scout-chat-msg').length,load:document.querySelector('.scout-chat-history-control__button')?.textContent||''}));
 assert.deepEqual({messages:first.messages,first:first.first,last:first.last},{messages:80,first:'m160',last:'m239'});assert.ok(first.dom<=80,JSON.stringify(first));assert.equal(first.load,'Load earlier messages');assert.equal(detailRequests.length,1);assert.match(detailRequests[0],/view=tail&limit=80/);
 const before=await page.evaluate(()=>{const button=document.querySelector('.scout-chat-history-control__button');button.scrollIntoView({block:'start'});const anchor=document.querySelector('[data-message-id="m160"]');return {top:anchor.getBoundingClientRect().top,scrollTop:scoutChatThread.scrollTop}});
 await page.locator('.scout-chat-history-control__button').click();await page.waitForFunction(()=>selectedScoutChatThread()?.messages?.length===160&&selectedScoutChatThread()?.history?.nextBeforeMessageId==='m80');
 const merged=await page.evaluate(()=>{const messages=selectedScoutChatThread().messages;return {count:messages.length,unique:new Set(messages.map(message=>message.id)).size,first:messages[0]?.id,last:messages.at(-1)?.id,anchorTop:document.querySelector('[data-message-id="m160"]')?.getBoundingClientRect().top,dom:document.querySelectorAll('#scoutChatThread > .scout-chat-msg').length}});
 assert.deepEqual({count:merged.count,unique:merged.unique,first:merged.first,last:merged.last},{count:160,unique:160,first:'m80',last:'m239'});assert.ok(Math.abs(merged.anchorTop-before.top)<=1,'earlier merge moved viewport anchor: '+JSON.stringify({before,merged}));assert.ok(merged.dom<=160);assert.equal(detailRequests.length,2);assert.match(detailRequests[1],/before=m160/);

 const mutationAnchor=await page.evaluate(()=>{const mutationBase=Date.parse('2026-08-22T12:00:00Z');const anchor=document.querySelector('[data-message-id="m180"]');anchor.scrollIntoView({block:'start'});const top=anchor.getBoundingClientRect().top;const current=selectedScoutChatThread();const incoming={...current,updatedAt:new Date(mutationBase+240000).toISOString(),messages:[...current.messages.slice(-79),{id:'m240',kind:'message',role:'scout',authorName:'Scout',authorEmail:'scout@thebonfire.xyz',text:'Message 240',createdAt:new Date(mutationBase+240000).toISOString()}]};const patched=mergeScoutChatMutationPage(current,incoming,{mode:'tail',messageCount:80,hasEarlier:true,nextBeforeMessageId:'m161'});upsertScoutChatThread(patched,{select:false});renderActiveScoutThread();return {top,after:document.querySelector('[data-message-id="m180"]').getBoundingClientRect().top,count:selectedScoutChatThread().messages.length,unique:new Set(selectedScoutChatThread().messages.map(message=>message.id)).size,last:selectedScoutChatThread().messages.at(-1).id};});
 assert.ok(Math.abs(mutationAnchor.after-mutationAnchor.top)<=1,'bounded mutation merge moved reader: '+JSON.stringify(mutationAnchor));assert.deepEqual({count:mutationAnchor.count,unique:mutationAnchor.unique,last:mutationAnchor.last},{count:161,unique:161,last:'m240'});

 // An exact edit/reaction record outside both the loaded history and repair
 // tail is inserted by canonical creation time, never appended after "latest".
 const oldExact=await page.evaluate(()=>{const current=selectedScoutChatThread();const exact={id:'m40',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'Edited message 40',createdAt:new Date(Date.parse('2026-08-22T12:00:00Z')+40000).toISOString(),editedAt:new Date().toISOString()};const patched=mergeScoutChatMutationPage(current,{...current,messages:current.messages.slice(-80)},current.history,exact);return {first:patched.messages[0]?.id,after:patched.messages[1]?.id,last:patched.messages.at(-1)?.id,unique:new Set(patched.messages.map(message=>message.id)).size,count:patched.messages.length};});
 assert.deepEqual(oldExact,{first:'m40',after:'m80',last:'m240',unique:162,count:162});

 // Walking the final cursor retains every loaded record in memory while the
 // DOM stays capped. Returning to latest is explicit and lands at the tail.
 const beforeFinal=await page.evaluate(()=>{const button=document.querySelector('.scout-chat-history-control__button');button.scrollIntoView({block:'start'});const anchor=document.querySelector('[data-message-id="m80"]');return {top:anchor.getBoundingClientRect().top};});
 await page.locator('.scout-chat-history-control__button').click();await page.waitForFunction(()=>selectedScoutChatThread()?.messages?.length===241&&selectedScoutChatThread()?.history?.hasEarlier===false);
 const finalHistory=await page.evaluate(()=>{const messages=selectedScoutChatThread().messages;return {count:messages.length,unique:new Set(messages.map(message=>message.id)).size,first:messages[0]?.id,last:messages.at(-1)?.id,anchorTop:document.querySelector('[data-message-id="m80"]')?.getBoundingClientRect().top,dom:document.querySelectorAll('#scoutChatThread > .scout-chat-msg').length,returnLabel:document.querySelector('.scout-chat-history-control--latest button')?.textContent||''};});
 assert.deepEqual({count:finalHistory.count,unique:finalHistory.unique,first:finalHistory.first,last:finalHistory.last},{count:241,unique:241,first:'m0',last:'m240'});assert.ok(Math.abs(finalHistory.anchorTop-beforeFinal.top)<=1,'final history page moved viewport anchor: '+JSON.stringify({beforeFinal,finalHistory}));assert.equal(finalHistory.dom,200);assert.equal(finalHistory.returnLabel,'Return to latest');assert.equal(detailRequests.length,3);assert.match(detailRequests[2],/before=m80/);
 await page.locator('.scout-chat-history-control--latest button').click();const latest=await page.evaluate(()=>({visible:Boolean(document.querySelector('[data-message-id="m240"]')),distance:scoutChatThread.scrollHeight-scoutChatThread.scrollTop-scoutChatThread.clientHeight,dom:document.querySelectorAll('#scoutChatThread > .scout-chat-msg').length}));assert.equal(latest.visible,true,JSON.stringify(latest));assert.ok(Math.abs(latest.distance)<=1,JSON.stringify(latest));assert.equal(latest.dom,200);

 // A raw tail made mostly of replies still paints its roots, and its compact
 // summaries disclose the full server count instead of pretending the loaded
 // recent subset is the complete conversation.
 const rootOld={id:'root-old',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'Older root',createdAt:new Date(base).toISOString()};
 const rootCurrent={id:'root-current',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'Current root',createdAt:new Date(base+1000).toISOString()};
 const replies=Array.from({length:80},(_,index)=>{const root=index%2?rootCurrent:rootOld;return {id:'reply-'+index,kind:'message',role:'user',authorName:'Tyler',authorEmail:'tyler@example.com',text:'Reply '+index,createdAt:new Date(base+(index+2)*1000).toISOString(),replyTo:{messageId:root.id,authorName:'AJ',authorEmail:'aj@shareability.com',text:root.text}}});
 await page.evaluate(({rootOld,rootCurrent,replies,base})=>{const thread={id:'reply-heavy',title:'Reply heavy',visibility:'public',messagesLoaded:true,createdAt:new Date(base).toISOString(),updatedAt:new Date(base+90000).toISOString(),history:{mode:'tail',hasEarlier:true,nextBeforeMessageId:'reply-0',replyCounts:{'root-old':45,'root-current':45}},messages:[rootOld,rootCurrent,...replies]};scoutChatThreads=[thread];activeScoutThreadId=thread.id;renderActiveScoutThread();},{rootOld,rootCurrent,replies,base});
 const replyUI=await page.evaluate(()=>{const result={roots:document.querySelectorAll('#scoutChatThread > .scout-chat-msg').length,summaries:Array.from(document.querySelectorAll('.desktop-chat-thread-summary__count')).map(node=>node.textContent)};document.querySelector('[data-message-id="root-old"] .desktop-chat-thread-summary')?.click();return result;});
 assert.equal(replyUI.roots,2,JSON.stringify(replyUI));assert.deepEqual(replyUI.summaries.sort(),['45 replies','45 replies']);
 assert.match(await page.locator('#chatContextMeta').textContent(),/45 replies · 40 recent loaded/);
 assert.deepEqual(errors,[]);await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CHAT_TAIL_INDEX="+indexPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered tail hydration failed: %v\n%s", err, output)
	}
}
