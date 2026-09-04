package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the actual shared menu and message renderer. A semantic click on a
// hover-hidden action is not a valid pointer journey: first reveal its message.
func TestChatMessageMenuCancelsDeleteWhenDismissed(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered message menu contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs = require('fs');
const http = require('http');
const assert = require('assert/strict');
const { chromium } = require('playwright');
const html = fs.readFileSync(process.env.CHAT_MENU_INDEX, 'utf8');
const deleted = [];
const thread = {id:'menu-qa',title:'Message menu QA',visibility:'private',messagesLoaded:true,messages:[0,1].map(i=>({id:'own-'+i,kind:'message',role:'user',authorName:'QA owner',authorEmail:'menu@example.test',text:'Synthetic menu message '+i,createdAt:'2026-09-04T19:0'+i+':00Z'}))};
const server = http.createServer((req,res)=>{
 const json=(code,body)=>{res.writeHead(code,{'content-type':'application/json'});res.end(JSON.stringify(body));};
 const url=new URL(req.url,'http://fixture.test');
 if(url.pathname==='/auth/me')return json(200,{email:'menu@example.test',name:'QA owner',shellAccess:'full'});
 if(url.pathname==='/api/stride/v1/mobile/surfaces/organizations')return json(200,{availability:'available',surface:'organizations',revision:1,items:[]});
 if(req.method==='DELETE'&&url.pathname.startsWith('/assistant/chat-threads/menu-qa/messages/')){
  const id=url.pathname.split('/').at(-1);deleted.push(id);thread.messages=thread.messages.filter(m=>m.id!==id);return json(200,{thread});
 }
 if(url.pathname==='/assistant/chat-threads')return json(200,{threads:[thread]});
 if(url.pathname==='/assistant/chat-threads/menu-qa')return json(200,{thread});
 if(url.pathname.startsWith('/public/')){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
 if(url.pathname.startsWith('/api/')||url.pathname.startsWith('/assistant/')||url.pathname.startsWith('/notifications')||url.pathname.startsWith('/rooms')||url.pathname.startsWith('/artifacts'))return json(404,{});
 res.writeHead(200,{'content-type':'text/html'});res.end(html);
});
(async()=>{
 let browser;
 try {
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1366,height:900}});
  page.setDefaultTimeout(10000);
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat',{waitUntil:'domcontentloaded'});
  const message=page.locator('#scoutChatThread [data-message-id="own-0"]');
  await message.waitFor();
  const trigger=message.getByRole('button',{name:'More message actions',exact:true});
  const menu=message.getByRole('menu',{name:'More message actions',exact:true});
  const focused=()=>page.evaluate(()=>document.activeElement?.textContent);
  // Real pointer path: the bubble reveals the actions before the click.
  await message.hover();
  await trigger.click();
  assert.equal(await trigger.getAttribute('aria-expanded'),'true');
  assert.equal(await menu.isVisible(),true);
  assert.equal(await focused(),'Edit message');
  await page.keyboard.press('ArrowDown');
  assert.equal(await focused(),'Remember this');
  await page.keyboard.press('End');
  assert.equal(await focused(),'Delete message…');
  await page.keyboard.press('Enter');
  assert.equal(await focused(),'Delete message?');
  assert.deepEqual(deleted,[]);
  await page.keyboard.press('Escape');
  assert.equal(await trigger.getAttribute('aria-expanded'),'false');
  assert.equal(await trigger.evaluate(e=>e===document.activeElement),true);
  await page.keyboard.press('Enter');
  assert.equal(await menu.isVisible(),true);
  // Escape cancels the confirmation, rather than leaving a hidden armed delete.
  assert.equal(await menu.getByRole('menuitem',{name:'Delete message…',exact:true}).count(),1);
  assert.equal(await menu.locator('[data-armed="1"]').count(),0);
  await page.keyboard.press('End');
  await page.keyboard.press('Enter');
  assert.deepEqual(deleted,[],'reopening requires a fresh confirmation');
  await page.getByRole('heading',{name:'Conversations',exact:true}).click();
  assert.equal(await trigger.getAttribute('aria-expanded'),'false');
  await trigger.focus();
  await page.keyboard.press('Space');
  assert.equal(await menu.getByRole('menuitem',{name:'Delete message…',exact:true}).count(),1);
  await page.keyboard.press('Tab');
  assert.equal(await trigger.getAttribute('aria-expanded'),'false');
  // The intended, twice-confirmed action still calls the supported exact DELETE.
  await message.hover(); await trigger.click();
  await menu.getByRole('menuitem',{name:'Delete message…',exact:true}).click();
  await menu.getByRole('menuitem',{name:'Delete message?',exact:true}).click();
  await message.waitFor({state:'detached'});
  assert.deepEqual(deleted,['own-0']);
  assert.equal(await page.locator('#scoutChatThread').getByText('Synthetic menu message 1',{exact:true}).count(),1);
  console.log('PASS pointer menu, keyboard navigation, Escape/outside cancellation, Tab dismissal, exact confirmed DELETE');
 } finally {
  if(browser)await browser.close();
  await new Promise(resolve=>server.close(resolve));
 }
})().catch(error=>{console.error(error);process.exitCode=1;});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CHAT_MENU_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("message menu browser contract: %v\n%s", err, output)
	}
	t.Log(string(output))
}
