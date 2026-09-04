package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHistoricalPrivateRiffOffersOnlyOwnDeleteWithoutResuming(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered historical Riff contract")
	}
	index, _ := filepath.Abs("index.html")
	script := `
const fs=require('fs'),http=require('http'),assert=require('assert/strict');
const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.RIFF_INDEX,'utf8');
const source={id:'riff-source',title:'Riff source QA',visibility:'public',messagesLoaded:true,messages:[{id:'anchor',kind:'message',role:'user',authorEmail:'owner@example.test',text:'Synthetic source',createdAt:'2026-09-04T12:00:00Z'}]};
const episodes=[{id:'old',createdAt:'2026-08-16T05:04:09Z',messageCount:2,status:'closed'},{id:'current',createdAt:'2026-09-02T12:00:00Z',messageCount:0,status:'active'}];
let oldMessages=[{id:'own-old',kind:'message',role:'user',authorEmail:'owner@example.test',text:'Synthetic old owner message',createdAt:'2026-08-16T05:05:00Z',riffEpisodeId:'old'},{id:'other-old',kind:'message',role:'user',authorEmail:'other@example.test',text:'Synthetic other author message',createdAt:'2026-08-16T05:06:00Z',riffEpisodeId:'old'}];
const project=episode=>({id:'riff-private',title:'Riff on source',ownerEmail:'owner@example.test',visibility:'private',messagesLoaded:true,messages:episode==='old'?oldMessages:[],riff:{sourceThreadId:source.id,sourceTitle:source.title,sourceAvailable:true,activeEpisodeId:'current',viewedEpisodeId:episode,episodeCount:2,episodes}});
const mutations=[];
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
 const json=(code,body)=>{res.writeHead(code,{'content-type':'application/json'});res.end(JSON.stringify(body));};
 const u=new URL(req.url,'http://fixture.test');
 if(req.method!=='GET')mutations.push({method:req.method,path:u.pathname});
 if(u.pathname==='/auth/me')return json(200,{email:'owner@example.test',name:'QA owner',shellAccess:'full'});
 if(u.pathname==='/assistant/chat-threads')return json(200,{threads:[source]});
 if(u.pathname==='/assistant/chat-threads/riff-source')return json(200,{thread:source});
 if(u.pathname==='/assistant/chat-threads/riff-source/riff')return json(200,{thread:project('current')});
 if(u.pathname==='/assistant/chat-threads/riff-private/messages/own-old'&&req.method==='DELETE'){oldMessages=oldMessages.filter(m=>m.id!=='own-old');return json(200,{thread:project('current')});}
 if(u.pathname==='/assistant/chat-threads/riff-private')return json(200,{thread:project(u.searchParams.get('episodeId')||'current')});
 if(u.pathname.startsWith('/public/')){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
 if(u.pathname.startsWith('/api/')||u.pathname.startsWith('/assistant/')||u.pathname.startsWith('/notifications')||u.pathname.startsWith('/rooms')||u.pathname.startsWith('/artifacts'))return json(404,{});
 res.writeHead(200,{'content-type':'text/html'});res.end(html);
});
(async()=>{let browser;try{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1366,height:900}});page.setDefaultTimeout(10000);
 await page.goto('http://127.0.0.1:'+server.address().port+'/chat',{waitUntil:'domcontentloaded'});
 await page.locator('.chat-thread-item[data-thread-id="riff-source"]').click();
 await page.getByRole('button',{name:'Ask Scout privately about this channel',exact:true}).click();
 await page.getByText('1 earlier pass',{exact:true}).click();
 await page.getByRole('button',{name:/View earlier Scout pass/}).click();
 const own=page.locator('#chatContextBody [data-message-id="own-old"]');await own.waitFor();
 assert.equal(await page.locator('#chatContextBody [data-message-id="other-old"]').getByRole('button',{name:'More reply actions',exact:true}).count(),0);
 await own.hover();const trigger=own.getByRole('button',{name:'More reply actions',exact:true});await trigger.click();
 const menu=own.getByRole('menu',{name:'More reply actions',exact:true});
 assert.deepEqual(await menu.getByRole('menuitem').allTextContents(),['Delete message…']);
 await menu.getByRole('menuitem',{name:'Delete message…',exact:true}).click();
 await page.keyboard.press('Escape');await trigger.click();
 assert.deepEqual(await menu.getByRole('menuitem').allTextContents(),['Delete message…']);
 await menu.getByRole('menuitem',{name:'Delete message…',exact:true}).click();
 await menu.getByRole('menuitem',{name:'Delete?',exact:true}).click();
 await own.waitFor({state:'detached'});
 assert.equal(await page.locator('#chatContextBody').getByText('Synthetic other author message',{exact:true}).count(),1);
 assert.equal(await page.getByText('Earlier pass in #Riff source QA',{exact:true}).count(),1);
 assert.equal(await page.locator('#chatContextReplyInput').isDisabled(),true);
 assert.deepEqual(mutations.filter(x=>x.path.includes('/riff')||x.method==='DELETE'),[{method:'POST',path:'/assistant/chat-threads/riff-source/riff'},{method:'DELETE',path:'/assistant/chat-threads/riff-private/messages/own-old'}]);
 console.log('PASS historical own delete only; Escape disarms; exact DELETE; earlier pass retained; no resume/edit/share or other-author actions');
}finally{if(browser)await browser.close();await new Promise(r=>server.close(r));}})().catch(e=>{console.error(e);process.exitCode=1;});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "RIFF_INDEX="+index)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("historical Riff browser: %v\n%s", err, out)
	}
	t.Log(string(out))
}
