package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWebKitPrivateRiffSpaceStaysChannelTiedAndEpisodeScoped(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered WebKit Private Riff Space contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs = require('fs');
const http = require('http');
const assert = require('assert/strict');
const { webkit } = require('playwright');
const html = fs.readFileSync(process.env.PRIVATE_RIFF_INDEX, 'utf8');
const requests = [];
const sourceMessages = [
  {id:'source-1',role:'user',authorName:'AJ',authorEmail:'aj@example.test',text:'First channel premise',createdAt:'2026-08-15T18:00:00Z'},
  {id:'source-2',role:'user',authorName:'AJ',authorEmail:'aj@example.test',text:'Latest channel premise',createdAt:'2026-08-15T19:00:00Z'},
];
const episodes = [
  {id:'ep-1',createdAt:'2026-08-15T18:05:00Z',throughCreatedAt:'2026-08-15T18:00:00Z',messageCount:2,status:'closed'},
  {id:'ep-2',createdAt:'2026-08-15T19:05:00Z',throughCreatedAt:'2026-08-15T19:00:00Z',messageCount:2,status:'active'},
];
function activity(episodeId, throughMessageId) {
  return {version:'stride-private-riff/v1',status:'completed',elapsedMs:1200,sourceCount:2,contextRevision:1,episodeId,checkpointId:'cp-'+episodeId,sourceThreadId:'source',throughMessageId,sourceMessageDigest:'a'.repeat(64),sourceWindowDigest:'b'.repeat(64),sourceAudienceDigest:'c'.repeat(64)};
}
function riffMessages(episodeId) {
  const through = episodeId === 'ep-1' ? 'source-1' : 'source-2';
  return [
    {id:'user-'+episodeId,kind:'message',role:'user',authorName:'AJ',authorEmail:'aj@example.test',text:'Pressure-test this privately',createdAt:'2026-08-15T19:06:00Z',riffEpisodeId:episodeId,riffCheckpointId:'cp-'+episodeId},
    {id:'scout-'+episodeId,kind:'message',role:'scout',authorName:'Scout',text:'Here is the sharper private read.',createdAt:'2026-08-15T19:07:00Z',riffEpisodeId:episodeId,riffCheckpointId:'cp-'+episodeId,activity:activity(episodeId,through)},
  ];
}
function riffThread(activeEpisodeId, viewedEpisodeId = activeEpisodeId) {
  const viewed = episodes.find(item => item.id === viewedEpisodeId);
  const throughMessageId = viewedEpisodeId === 'ep-1' ? 'source-1' : 'source-2';
  return {
    id:'riff-space',title:'Riff on #Country Golf',conversationKind:'channel_riff',visibility:'private',messagesLoaded:true,
    riff:{version:'stride-private-riff/v1',spaceId:'riff-space',activeEpisodeId,viewedEpisodeId,episodeCount:2,episodes,checkpointId:'cp-'+viewedEpisodeId,autoFresh:true,sourceThreadId:'source',sourceTitle:'Country Golf',throughMessageId,throughCreatedAt:viewed.throughCreatedAt,messageCount:2,contextRevision:1,capturedAt:'2026-08-15T19:05:00Z',brainCapturedAt:'2026-08-15T19:04:00Z',agentName:'Scout',sourceAvailable:true,newMessageCount:viewedEpisodeId === 'ep-1' ? 1 : 0},
    messages:riffMessages(viewedEpisodeId),
  };
}
const sourceThread = {id:'source',title:'Country Golf',visibility:'public',messagesLoaded:true,messages:sourceMessages};

const server = http.createServer(async (req, res) => {
  if (req.url === '/public/composer-dictation.js') {
    res.writeHead(200, {'content-type':'application/javascript'}); return res.end('');
  }
  if (req.url === '/auth/me') {
    res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({email:'aj@example.test',name:'AJ',shellAccess:'full'}));
  }
  if (req.url === '/api/stride/v1/mobile/surfaces/organizations') {
    res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[]}));
  }
  if (req.method === 'GET' && req.url === '/assistant/chat-threads?view=index') {
    res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({threads:[sourceThread]}));
  }
  if (req.method === 'GET' && req.url === '/assistant/chat-threads/source') {
    res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({thread:sourceThread}));
  }
  const requestURL = new URL(req.url, 'http://local.test');
  if (req.method === 'GET' && requestURL.pathname === '/assistant/chat-threads/riff-space' && requestURL.searchParams.get('episodeId')) {
    const episodeId = requestURL.searchParams.get('episodeId');
    requests.push({method:'GET',kind:'view',episodeId});
    res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({thread:riffThread('ep-2', episodeId)}));
  }
  if (req.method === 'POST' && req.url === '/assistant/chat-threads/source/riff') {
    let body=''; for await (const chunk of req) body += chunk;
    const parsed=JSON.parse(body || '{}'); requests.push({method:'POST',kind:'open',body:parsed});
    const active = parsed.episodeId || 'ep-2';
    res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({thread:riffThread(active)}));
  }
  if (req.method === 'POST' && req.url === '/assistant/chat-threads/riff-space/riff-publish') {
    let body=''; for await (const chunk of req) body += chunk;
    const parsed=JSON.parse(body || '{}'); requests.push({method:'POST',kind:'publish',body:parsed});
    res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({threadId:'source',rootMessageId:'public-1',messageIds:['public-1'],publishedCount:1,replayed:false}));
  }
  if (req.url.startsWith('/api/') || req.url.startsWith('/assistant/') || req.url.startsWith('/notifications') || req.url.startsWith('/rooms') || req.url.startsWith('/artifacts')) {
    res.writeHead(404, {'content-type':'application/json'}); return res.end('{}');
  }
  res.writeHead(200, {'content-type':'text/html; charset=utf-8'}); res.end(html);
});

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base='http://127.0.0.1:'+server.address().port;
  const browser=await webkit.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1366,height:900}});
  await page.goto(base+'/chat',{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.waitForFunction(() => document.getElementById('appShell').dataset.tool === 'chat');
  await page.evaluate(({sourceThread, riff}) => {
    scoutChatThreads=[sourceThread,riff];
    activeScoutThreadId='source';
    renderChatAgentThreads();
    renderActiveScoutThread();
  }, {sourceThread, riff:riffThread('ep-2')});

  assert.equal(await page.locator('#chatAgentThreads [data-thread-id="riff-space"]').count(),0,'Riff leaked into ordinary private history');
  await page.locator('#chatConvoRiff').click();
  await page.waitForSelector('#chatContextRail:not([hidden])');
  assert.match(await page.locator('#chatContextRail').innerText(),/Your Riff|Current pass/);
  assert.doesNotMatch(await page.locator('#chatContextRail').innerText(),/Update context|Refresh context/);
  assert.match(await page.locator('#chatContextRail').innerText(),/Open source/);
  assert.match(await page.locator('#chatContextRail').innerText(),/Riff in Realtime/);
  assert.match(await page.locator('#chatContextRail').innerText(),/1 earlier pass/);
  const firstOpen=requests.find(item => item.kind === 'open');
  assert.equal(firstOpen.body.entryPoint,'resume');
  assert.equal(firstOpen.body.throughMessageId,'source-2');

  await page.locator('#chatContextRail .private-riff-history summary').click();
  const viewResponse=page.waitForResponse(response=>{
    const url=new URL(response.url());
    return url.pathname==='/assistant/chat-threads/riff-space'&&url.searchParams.get('episodeId')==='ep-1';
  });
  await page.getByRole('button',{name:/View earlier private Riff pass/}).click();
  await viewResponse;
  await page.waitForFunction(() => document.getElementById('chatContextReplyInput').disabled === true);
  assert.match(await page.locator('#chatContextRail').innerText(),/Earlier pass|Read-only pass/);
  assert.match(await page.locator('#chatContextRail').innerText(),/Resume this pass/);
  assert.ok(requests.some(item => item.kind === 'view' && item.episodeId === 'ep-1'));

  await page.getByRole('button',{name:'Resume this pass'}).click();
  await page.waitForFunction(() => document.getElementById('chatContextReplyInput').disabled === false);
  const resume=requests.filter(item => item.kind === 'open').at(-1);
  assert.equal(resume.body.entryPoint,'resume');
  assert.equal(resume.body.episodeId,'ep-1');

  const riffRail=page.locator('#chatContextRail');
  const scoutReply=riffRail.locator('[data-message-id="scout-ep-1"]');
  await scoutReply.scrollIntoViewIfNeeded();
  await scoutReply.hover();
  await scoutReply.getByRole('button',{name:'More reply actions'}).click();
  const publishResponse=page.waitForResponse(response=>response.url().endsWith('/assistant/chat-threads/riff-space/riff-publish'));
  await riffRail.getByRole('menuitem',{name:'Share this reply'}).click();
  await publishResponse;
  const publish=requests.find(item => item.kind === 'publish');
  assert.equal(publish.body.scope,'reply');
  assert.equal(publish.body.episodeId,'ep-1');
  assert.equal(publish.body.messageId,'scout-ep-1');

  await browser.close(); server.close();
})().catch(error => { console.error(error); server.close(); process.exit(1); });`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PRIVATE_RIFF_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered WebKit Private Riff Space harness: %v\n%s", err, output)
	}
}
