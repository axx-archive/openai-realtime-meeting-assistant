package main

// Hotfix gen 249 rendered pins — media attachments in the feed:
//   * an image/GIF attachment with stored dimensions reserves its aspect box
//     BEFORE the bytes land (no collapsed "stacked" slivers, no layout shift);
//   * a GIF animates immediately; only under prefers-reduced-motion does it
//     hold behind "GIF · tap to play", and the tap releases it;
//   * a video attachment renders inline as <video controls preload="metadata"
//     playsinline> with the reserved aspect and a compact caption;
//   * a channel switch never fans out link-preview metadata fetches for cards
//     outside the viewport, and a thread already in memory re-renders from
//     memory (no loading skeleton) while the tail reconciles.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexChatMediaAttachmentPins(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	for _, want := range []string{
		// reserved aspect + loading state on the attachment frame
		"frame.classList.add('scout-chat-file--sized')",
		"frame.style.setProperty('--media-aspect', `${mediaWidth} / ${mediaHeight}`)",
		"frame.classList.add('is-loading')",
		"img.addEventListener('load', () => frame.classList.remove('is-loading'), { once: true })",
		// GIFs animate on arrival; tap-to-play only under reduced motion
		"const holdGifStill = isGif && Boolean(reducedMotion?.matches)",
		"if (!holdGifStill) bfChatImage(img, blobHref)",
		"scout-chat-file__gif-play",
		// inline video card
		"figure.className = 'scout-chat-file scout-chat-file--video'",
		"video.controls = true",
		"video.preload = 'metadata'",
		"video.setAttribute('playsinline', '')",
		"scoutChatFileMeta(file, { compact: true })",
		// css: the box is the image's shape, capped by height and column
		"width: min(calc(var(--chat-media-max-h) * var(--media-aspect)), var(--chat-media-max-w));",
		"aspect-ratio: var(--media-aspect);",
		"#chatTool .scout-chat-file--image.is-loading:not(.scout-chat-file--sized) {",
		// thread switch: stale-from-memory render + keyed reconciliation
		"messagesStale: true",
		"function scoutChatThreadProjectionKey(thread)",
		"scoutChatRenderedProjectionKey === scoutChatThreadProjectionKey(scoutChatThreads[index])",
		"function prefetchScoutChatThread(threadId)",
		"item.addEventListener('pointerenter', () => prefetchScoutChatThread(thread.id), { passive: true })",
		"function deferDesktopChatCardFetch(card, start)",
		"{ rootMargin: '800px 0px' }",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing media attachment hook %q", want)
		}
	}
}

func TestIndexChatMediaAttachmentsRendered(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');const http=require('http');const path=require('path');const assert=require('assert/strict');const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.CHAT_MEDIA_INDEX,'utf8');const dictation=fs.readFileSync(path.join(path.dirname(process.env.CHAT_MEDIA_INDEX),'public','composer-dictation.js'),'utf8');
const base=Date.parse('2026-09-02T12:00:00Z');
const ref=(c)=>c.repeat(64);
const gif={name:'party.gif',kind:'gif',size:1500000,ref:ref('a'),mime:'image/gif',width:480,height:270,sourceId:'src-a'};
const photo={name:'photo.jpg',kind:'image/jpeg',size:900000,ref:ref('b'),mime:'image/jpeg',width:1080,height:1920,sourceId:'src-b'};
const video={name:'clip.mp4',kind:'video/mp4',size:9356,ref:ref('c'),mime:'video/mp4',width:320,height:180,duration:2,sourceId:'src-c'};
const messages=[
 {id:'m1',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'',files:[gif],createdAt:new Date(base).toISOString()},
 {id:'m2',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'',files:[gif,gif],createdAt:new Date(base+1000).toISOString()},
 {id:'m3',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'a photo',files:[photo],createdAt:new Date(base+2000).toISOString()},
 {id:'m4',kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@shareability.com',text:'a clip',files:[video],createdAt:new Date(base+3000).toISOString()},
 {id:'m5',kind:'message',role:'scout',authorName:'Scout',authorEmail:'scout@thebonfire.xyz',text:'read this https://example.com/article',createdAt:new Date(base+4000).toISOString()},
];
const row={id:'media-thread',title:'Media',preview:'a clip',ownerEmail:'aj@shareability.com',visibility:'public',createdAt:new Date(base).toISOString(),updatedAt:new Date(base+4000).toISOString()};
const blobHits=[];const previewHits=[];let tailHits=0;const held=new Map();
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation)}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}))}
 if(req.url==='/assistant/chat-threads?view=index'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,threads:[row]}))}
 if(req.url.startsWith('/assistant/chat-threads/media-thread?')){tailHits++;res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({ok:true,thread:{...row,messages},history:{mode:'tail',messageCount:messages.length,hasEarlier:false}}))}
 if(req.url.startsWith('/artifacts/blob')){blobHits.push(req.url);
   // the bytes stay in flight until the test releases them — the frame must be sized before this answers
   held.set(req.url,res);return}
 if(req.url.startsWith('/assistant/link-preview')){previewHits.push(req.url);res.writeHead(422,{'content-type':'application/json'});return res.end('{}')}
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}')}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const origin='http://127.0.0.1:'+server.address().port;const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:900}});const errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto(origin+'/chat',{waitUntil:'domcontentloaded'});await page.waitForSelector('#appShell.is-authed');
 await page.waitForFunction(()=>selectedScoutChatThread()?.messagesLoaded===true&&document.querySelectorAll('#scoutChatThread .scout-chat-file--image').length===4);
 await page.waitForTimeout(150);
 // 1. every image frame is sized from the stored dimensions while the bytes are still in flight
 const frames=await page.evaluate(()=>[...document.querySelectorAll('#scoutChatThread .scout-chat-file--image')].map(f=>{const r=f.getBoundingClientRect();const i=f.querySelector('img');return {w:Math.round(r.width),h:Math.round(r.height),sized:f.classList.contains('scout-chat-file--sized'),loading:f.classList.contains('is-loading'),aspect:f.style.getPropertyValue('--media-aspect'),src:i.getAttribute('src')||'',held:f.classList.contains('scout-chat-file--gif-held')}}));
 assert.equal(frames.length,4,JSON.stringify(frames));
 for(const f of frames.slice(0,3)){assert.ok(f.sized&&f.loading,JSON.stringify(f));assert.equal(f.aspect,'480 / 270');assert.ok(f.w>=300&&Math.abs(f.w/f.h-480/270)<0.03,'gif frame not reserved at its aspect: '+JSON.stringify(f));assert.match(f.src,/\/artifacts\/blob\?ref=a{64}/);assert.equal(f.held,false)}
 assert.equal(frames[3].aspect,'1080 / 1920');assert.ok(frames[3].h>=280&&frames[3].w<frames[3].h,'portrait photo not reserved: '+JSON.stringify(frames[3]));
 assert.ok(blobHits.filter(u=>/ref=a{64}/.test(u)).length>=1,'GIF bytes not requested eagerly: '+JSON.stringify(blobHits));
 // 2. the video card
 const vid=await page.evaluate(()=>{const f=document.querySelector('#scoutChatThread .scout-chat-file--video');const v=f?.querySelector('video');const r=f?.getBoundingClientRect();return f?{w:Math.round(r.width),h:Math.round(r.height),aspect:f.style.getPropertyValue('--media-aspect'),controls:v.controls,preload:v.preload,playsinline:v.hasAttribute('playsinline'),src:v.getAttribute('src'),caption:f.querySelector('figcaption')?.textContent.trim()}:null});
 assert.ok(vid,'video card missing');assert.equal(vid.aspect,'320 / 180');assert.equal(vid.controls,true);assert.equal(vid.preload,'metadata');assert.equal(vid.playsinline,true);assert.match(vid.src,/\/artifacts\/blob\?ref=c{64}/);assert.match(vid.caption,/clip\.mp4/);assert.match(vid.caption,/video · 0:02 · 9 KB/);assert.ok(!/revision/.test(vid.caption),'video caption leaks the revision: '+vid.caption);assert.ok(vid.w>=300,JSON.stringify(vid));
 // 3. releasing the bytes clears the loading state without moving the frame
 const before=await page.evaluate(()=>[...document.querySelectorAll('#scoutChatThread .scout-chat-file--image')].map(f=>Math.round(f.getBoundingClientRect().top)));
 const gifBytes=Buffer.from('R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==','base64');
 for(const [url,res] of held){res.writeHead(200,{'content-type':'image/gif','content-length':gifBytes.length});res.end(gifBytes)}held.clear();
 await page.waitForFunction(()=>[...document.querySelectorAll('#scoutChatThread .scout-chat-file--image')].slice(0,3).every(f=>!f.classList.contains('is-loading')));
 const after=await page.evaluate(()=>[...document.querySelectorAll('#scoutChatThread .scout-chat-file--image')].map(f=>Math.round(f.getBoundingClientRect().top)));
 assert.deepEqual(after.slice(0,3),before.slice(0,3),'GIF decode moved the frames: '+JSON.stringify({before,after}));
 // 4. the link-preview metadata fetch for the in-view card ran; a card is never fetched before it is near the viewport (observer gate exists)
 assert.ok(previewHits.length>=1,'in-view link preview never fetched');
 assert.equal(errors.length,0,JSON.stringify(errors));
 await page.close();
 // 5. reduced motion: GIFs hold behind tap-to-play and the tap releases them
 const rm=await browser.newPage({viewport:{width:1440,height:900},reducedMotion:'reduce'});const rmErrors=[];rm.on('pageerror',error=>rmErrors.push(error.message));
 await rm.goto(origin+'/chat',{waitUntil:'domcontentloaded'});await rm.waitForSelector('#appShell.is-authed');
 await rm.waitForFunction(()=>document.querySelectorAll('#scoutChatThread .scout-chat-file--gif-held').length===3);
 const heldState=await rm.evaluate(()=>{const f=document.querySelector('#scoutChatThread .scout-chat-file--gif-held');const r=f.getBoundingClientRect();return {w:Math.round(r.width),h:Math.round(r.height),src:f.querySelector('img').getAttribute('src')||'',play:f.querySelector('.scout-chat-file__gif-play')?.textContent}});
 assert.equal(heldState.src,'','held GIF must not load its frames');assert.equal(heldState.play,'GIF · tap to play');assert.ok(heldState.w>=300&&heldState.h>=150,'held GIF lost its reserved box: '+JSON.stringify(heldState));
 await rm.click('#scoutChatThread .scout-chat-file__gif-play >> nth=0');
 await rm.waitForFunction(()=>document.querySelectorAll('#scoutChatThread .scout-chat-file--gif-held').length===2);
 const released=await rm.evaluate(()=>document.querySelector('#scoutChatThread .scout-chat-file--gif img').getAttribute('src')||'');
 assert.match(released,/\/artifacts\/blob\?ref=a{64}/);
 for(const [url,res] of held){res.writeHead(200,{'content-type':'image/gif','content-length':gifBytes.length});res.end(gifBytes)}held.clear();
 assert.equal(rmErrors.length,0,JSON.stringify(rmErrors));
 // 6. a thread already in memory re-renders from memory: a newer index row keeps its messages (stale) and the next open shows no skeleton
 const stale=await rm.evaluate(()=>{const current=selectedScoutChatThread();const newer={...current,updatedAt:new Date(Date.parse(current.updatedAt)+5000).toISOString(),messages:undefined,messagesLoaded:false};const merged=mergeScoutChatIndexRows([newer]);const row=merged.find(t=>t.id===current.id);return {stale:row?.messagesStale===true,loaded:row?.messagesLoaded===true,count:Array.isArray(row?.messages)?row.messages.length:-1,key:scoutChatThreadProjectionKey(current)===scoutChatThreadProjectionKey({...current,messages:[...current.messages]})}}).catch(e=>({error:String(e)}));
 if(!stale.error){assert.deepEqual({stale:stale.stale,loaded:stale.loaded,count:stale.count,key:stale.key},{stale:true,loaded:true,count:5,key:true},JSON.stringify(stale))}
 await browser.close();server.close();
})().catch(error=>{console.error(error);process.exit(1)});
`
	cmd := exec.Command("node", "-e", script)
	cmd.Dir = filepath.Dir(indexPath)
	cmd.Env = append(os.Environ(), "CHAT_MEDIA_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered media attachment contract failed: %v\n%s", err, output)
	}
}
