package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrivateThreadHeaderKeepsActivePresentationAuthorityRendered(t *testing.T) {
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
const html=fs.readFileSync(process.env.CHAT_TITLE_INDEX,'utf8');
const json=(res,status,payload)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify(payload));};
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me')return json(res,200,{email:'aj@shareability.com',name:'AJ',shellAccess:'full'});
  if(req.url==='/readyz')return json(res,200,{ok:true,checks:{agents:{renderRunner:{heartbeatOK:true}}}});
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')||req.url.startsWith('/brain/'))return json(res,503,{});
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat',{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  const result=await page.evaluate(()=>{
    const presentationArtifact={id:'presentation-goal',text:'',metadata:{status:'running',mode:'goal',processId:'packaging_studio',threadQuery:'Build a six-slide presentation for the team'}};
    const documentArtifact={id:'supporting-document',text:'',metadata:{status:'complete',mode:'document',threadQuery:'Write the supporting document'}};
    artifactEntries=[];
    const presentationMessage={id:'presentation-work',kind:'thread',role:'scout',createdAt:'2026-08-22T20:55:00Z',thread:{artifactId:presentationArtifact.id,mode:'goal',processId:'packaging_studio',outputFamily:'Presentation',status:'running',query:'Build a six-slide presentation for the team'}};
    const compact={id:'presentation-thread',title:'Build a six-slide presentation for the team',visibility:'private',messagesLoaded:false,activeWork:{createdAt:presentationMessage.createdAt,thread:presentationMessage.thread}};
    const compactTitle=chatThreadDisplayTitle(compact);
    const compactInfo=chatThreadLatestArtifactInfo(compact);
    const compactActive=chatThreadActiveWork(compact);
    artifactEntries=[presentationArtifact,documentArtifact];
    const exact={...compact,messagesLoaded:true,activeWork:null,messages:[
      presentationMessage,
      {id:'document-work',kind:'thread',role:'scout',createdAt:'2026-08-22T20:56:00Z',thread:{artifactId:documentArtifact.id,mode:'document',status:'complete',query:'Write the supporting document'}}
    ]};
    scoutChatThreads=[exact];
    activeScoutThreadId=exact.id;
    syncChatConvoHeader();
    return {compactTitle,compactStatus:compactInfo?.status,compactStatusLabel:compactInfo?.statusLabel,compactActive:Boolean(compactActive),exactTitle:chatThreadDisplayTitle(exact),header:chatConvoTitle.textContent};
  });
  assert.deepEqual(result,{compactTitle:'Presentation',compactStatus:'running',compactStatusLabel:'in progress',compactActive:true,exactTitle:'Presentation',header:'Presentation'});
  assert.deepEqual(errors,[]);
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	nodeModules := filepath.Join(filepath.Dir(indexPath), "node_modules")
	if _, err := os.Stat(filepath.Join(nodeModules, "playwright")); err != nil {
		nodeModules = "/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules"
	}
	if _, err := os.Stat(filepath.Join(nodeModules, "playwright")); err != nil {
		t.Skip("Playwright unavailable")
	}
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodeModules, "CHAT_TITLE_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered private-thread title harness: %v\n%s", err, output)
	}
}
