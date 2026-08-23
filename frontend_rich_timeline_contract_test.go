package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebTimelineRoutesProcessToActivityAndKeepsRichResults(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	projection := functionBody(html, "function scoutChatRecordBelongsInTimeline(message)")
	if projection == "" {
		t.Fatal("timeline projection contract missing")
	}
	for _, want := range []string{
		"projection.richResult || projection.checkpoint",
		"kind === 'image'",
		"kind === 'manifest'",
		"status === '' || status === 'proposed'",
		"governedWorkResourcePath(message.work.artifactHref, 'artifact')",
		"scoutManifestHasRichDeck(message)",
		"return false",
	} {
		if !strings.Contains(projection, want) {
			t.Errorf("timeline projection missing %q", want)
		}
	}
	router := functionBody(html, "function scoutChatMessageRecordNode(message)")
	for _, want := range []string{
		"scoutChatRecordBelongsInTimeline(message)",
		"scoutHTMLDeckRefRecordNode(message, resultArtifact)",
		"scoutMarkdownDocumentRefRecordNode(message, resultArtifact)",
		"projection.checkpoint",
	} {
		if !strings.Contains(router, want) {
			t.Errorf("timeline router missing %q", want)
		}
	}
	for _, leaked := range []string{
		"scoutChatResearchNode(run)",
		"jump to the card",
		"projection.customerReply",
	} {
		if strings.Contains(router, leaked) {
			t.Errorf("generic process renderer leaked back into timeline: %q", leaked)
		}
	}
	indicator := functionBody(html, "function syncDesktopActiveWorkIndicator()")
	for _, want := range []string{
		"'needs_attention'",
		"'failed'",
		"!String(candidate.thread.delegatedBy || '').trim()",
		"openDesktopWorkContext",
	} {
		if want == "openDesktopWorkContext" {
			if !strings.Contains(html, "chatWorkIndicatorOpen?.addEventListener") || !strings.Contains(html, want) {
				t.Errorf("bottom activity indicator no longer opens the work context")
			}
			continue
		}
		if !strings.Contains(indicator, want) {
			t.Errorf("activity indicator missing %q", want)
		}
	}
}

func TestWebTimelineProjectionRenderedContract(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	chrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(chrome); err != nil {
		t.Skip("system Chrome unavailable")
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
const html=fs.readFileSync(process.env.RICH_TIMELINE_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/artifacts')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/brain/')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true,executablePath:process.env.RICH_TIMELINE_CHROME});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  await page.goto('http://127.0.0.1:'+server.address().port,{waitUntil:'domcontentloaded'});
  await page.waitForFunction(()=>typeof scoutChatRecordBelongsInTimeline==='function');
  const result=await page.evaluate(()=>{
    const deck={id:'deck-result',text:'<!doctype html><html><body><h1>Real deck</h1></body></html>',metadata:{title:'Real deck',type:'html_deck',status:'complete',artifactVersion:'1'}};
    const doc={id:'doc-result',text:'# Real report\n\nThis is the completed report.',metadata:{title:'Real report',type:'markdown',status:'complete',artifactVersion:'1'}};
    const running={id:'goal-running',text:'',metadata:{title:'Running work',mode:'goal',status:'running',threadStatus:'running'}};
    const failed={id:'goal-failed',text:'',metadata:{title:'Failed work',mode:'goal',status:'needs_attention',threadStatus:'needs_attention'}};
    const stage={id:'stage-row',text:'# Internal stage',metadata:{title:'Internal stage',status:'complete',goalParentId:'goal-running',processStage:'research'}};
    const checkpoint={id:'goal-checkpoint',text:'',metadata:{title:'Choose a direction',mode:'goal',status:'needs_input',threadStatus:'needs_input',goalPlan:JSON.stringify({state:'approval_required',checkpoint:{id:'cp-1',stageId:'direction',question:'Which direction should Scout use?',options:[{id:'choice-1',label:'Editorial',action:'proceed'}]},subtasks:[{id:'direction',title:'Choose direction',role:'human_checkpoint',status:'running'}]})}};
    artifactEntries=[deck,doc,running,failed,stage,checkpoint];
    const messages={
      status:{id:'status',kind:'thread',text:'Scout started this work.',thread:{id:'run-1',artifactId:running.id,mode:'goal',status:'running',query:'Build the deck'}},
      launch:{id:'launch',kind:'thread',text:'I will deliver the finished presentation here with the evidence linked.',thread:{id:'run-2',artifactId:running.id,mode:'goal',status:'running',query:'Build the deck'}},
      answer:{id:'answer',kind:'message',role:'scout',text:'The market evidence supports a narrow pilot before a national launch.'},
      failed:{id:'failed',kind:'thread',text:'Work failed.',thread:{id:'run-3',artifactId:failed.id,mode:'goal',status:'needs_attention',query:'Build the deck'}},
      stage:{id:'stage',kind:'artifact',text:'Research landed',thread:{artifactId:stage.id,status:'complete'}},
      workRecord:{id:'record',kind:'work_record',work:{status:'complete',title:'Completed work'}},
      governedResult:{id:'governed',kind:'work_result',work:{status:'completed',title:'Evidence brief',summary:'The evidence-linked brief is ready.',workerName:'Researcher',artifactHref:'/api/stride/v1/work/runs/run-1/artifact'}},
      pendingProposal:{id:'proposal-pending',kind:'proposal',proposal:{kind:'goal_run',objective:'Build the presentation',summary:'Review the held request.',status:''}},
      resolvedProposal:{id:'proposal-resolved',kind:'proposal',proposal:{kind:'goal_run',objective:'Build the presentation',summary:'Review the held request.',status:'accepted'}},
      checkpoint:{id:'checkpoint',kind:'thread',thread:{id:'run-4',artifactId:checkpoint.id,mode:'goal',status:'needs_input',query:'Build the deck'}},
      deck:{id:'deck',kind:'thread',text:'Presentation delivered.',thread:{id:'run-5',artifactId:'deck-goal',mode:'goal',status:'complete',goalStatus:'complete',resultArtifactId:deck.id,resultArtifactType:'html_deck',resultTitle:'Real deck'}},
      doc:{id:'doc',kind:'thread',text:'Document delivered.',thread:{id:'run-6',artifactId:'doc-goal',mode:'goal',status:'complete',goalStatus:'complete',resultArtifactId:doc.id,resultArtifactType:'markdown',resultTitle:'Real report',resultCanEdit:true}}
    };
    scoutChatThreads=[{id:'timeline',messages:Object.values(messages)}];activeScoutThreadId='timeline';
    const renderClass=message=>{const host=document.createElement('div');host.append(scoutChatMessageRecordNode(message));return {children:host.childElementCount,html:host.innerHTML};};
    return {visible:Object.fromEntries(Object.entries(messages).map(([key,message])=>[key,scoutChatRecordBelongsInTimeline(message)])),rendered:Object.fromEntries(Object.entries(messages).map(([key,message])=>[key,renderClass(message)]))};
  });
  assert.deepEqual(result.visible,{status:false,launch:false,answer:true,failed:false,stage:false,workRecord:false,governedResult:true,pendingProposal:true,resolvedProposal:false,checkpoint:true,deck:true,doc:true});
  assert.equal(result.rendered.status.children,0);
  assert.equal(result.rendered.failed.children,0);
  assert.equal(result.rendered.stage.children,0);
  assert.equal(result.rendered.workRecord.children,0);
  assert.match(result.rendered.governedResult.html,/scout-chat-work-record--deliverable/);
  assert.match(result.rendered.governedResult.html,/Evidence brief/);
  assert.match(result.rendered.pendingProposal.html,/scout-proposal-card/);
  assert.equal(result.rendered.resolvedProposal.children,0);
  assert.equal(result.rendered.launch.children,0);
  assert.match(result.rendered.answer.html,/market evidence supports a narrow pilot/);
  assert.match(result.rendered.checkpoint.html,/scout-chat-work-card__checkpoint/);
  assert.match(result.rendered.deck.html,/scout-chat-deck-result/);
  assert.match(result.rendered.doc.html,/scout-chat-document-result/);
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	nodeModules := "/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules"
	if _, err := os.Stat(filepath.Join(nodeModules, "playwright")); err != nil {
		t.Skip("bundled Playwright unavailable")
	}
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodeModules, "RICH_TIMELINE_INDEX="+indexPath, "RICH_TIMELINE_CHROME="+chrome)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered rich timeline contract: %v\n%s", err, output)
	}
}
