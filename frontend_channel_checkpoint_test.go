package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelWorkCardOwnsCheckpointDecision(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"function scoutDesktopCheckpointNode(card, artifact, plan, checkpoint)",
		"const customerQuestion = packagingStudioCheckpointQuestion(plan, checkpoint)",
		"const stageTitle = stage ? packagingStudioTaskDisplayTitle(plan, stage) : ''",
		"ref?.checkpoint || goalPendingCheckpoint(artifact, plan)",
		"scout-chat-work-card__checkpoint-question",
		"scout-chat-work-card__checkpoint-context",
		"scout-chat-work-card__checkpoint-choices",
		"candidate.disabled = true",
		"panel.dataset.state = 'pending'",
		"panel.dataset.state = 'failed'",
		"panel.dataset.state = 'selected'",
		"submitCheckpointOption(artifact.id, checkpointId, option.id, revisionNote)",
		"checkpointId: String(checkpointId || '').trim()",
		"checkpointOptionId: String(checkpointOptionId || '').trim()",
		"body.checkpointNote = note",
		"Changes for Scout",
		"Send changes",
		"function desktopCheckpointChoiceLabel(label)",
		"function desktopWorkRecoveryControl(artifact, plan, ref, options = {})",
		"const control = bfEl('button', 'chat-context-action', canResumeProcess ? 'Retry from here' : 'Ask Scout')",
		"postAuthJSON('/artifacts/action', { id: artifact.id, action: 'resume' })",
		"Resuming at the blocker…",
		"This work remains held.",
		"The work was sent back for revision.",
		"Scout is continuing.",
		"['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End']",
		"return { ok, data }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("channel checkpoint contract missing %q", want)
		}
	}
	checkpointStart := strings.Index(html, "function scoutDesktopCheckpointNode(")
	if checkpointStart < 0 {
		t.Fatal("channel checkpoint implementation boundaries missing")
	}
	checkpointEnd := strings.Index(html[checkpointStart:], "function scoutDesktopGoalWorkCardNode(")
	if checkpointEnd < 0 {
		t.Fatal("channel checkpoint implementation boundaries missing")
	}
	checkpointSource := html[checkpointStart : checkpointStart+checkpointEnd]
	if strings.Contains(checkpointSource, ".slice(0, 3)") {
		t.Error("channel checkpoint silently truncates the server-projected option set")
	}
}

func TestChannelWorkCardCheckpointRenderedChoiceJourney(t *testing.T) {
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
const html=fs.readFileSync(process.env.CHANNEL_CHECKPOINT_INDEX,'utf8');
const requests=[];let attempts=0;
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
  if(req.url==='/artifacts/action'&&req.method==='POST'){
    let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{const body=JSON.parse(raw);requests.push(body);attempts++;setTimeout(()=>{if(attempts===1){res.writeHead(503,{'content-type':'application/json'});res.end(JSON.stringify({error:'Choice could not be saved. Try again.'}));return;}res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({ok:true,replayed:false,message:'choice recorded'}));},100);});return;
  }
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:390,height:844}});
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(()=>{
   setActiveTool('chat');
	   const chatTool=document.getElementById('chatTool');chatTool.hidden=false;chatTool.classList.add('is-active');chatTool.style.cssText='display:block!important;position:fixed;inset:0;z-index:99999;overflow:auto';
	   window.mountChannelCheckpointFixture=(fixtureId,artifactId,checkpoint,query='Build a launch deck')=>{
	     const plan={state:'approval_required',checkpoint,subtasks:[{id:checkpoint.stageId,title:'Creative direction',role:'human_checkpoint'}]};
	     const artifact={id:artifactId,kind:'os_artifact',text:'# Launch deck',metadata:{mode:'goal',type:'html_deck',title:'Launch deck',status:'needs_input',threadStatus:'needs_input',goalPlan:JSON.stringify(plan),checkpoint:JSON.stringify(checkpoint)}};
	     const message={id:'work-message-'+fixtureId,thread:{id:artifactId,artifactId,status:'needs_input',mode:'goal',agentName:'Scout',query,checkpoint}};
	     const fixture=document.createElement('div');fixture.id=fixtureId;fixture.appendChild(scoutDesktopGoalWorkCardNode(message,artifact));chatTool.appendChild(fixture);
	   };
	   const checkpoint={id:'goal-checkpoint-aaaaaaaaaaaaaaaaaaaaaaaa',stageId:'direction',question:'Which creative direction should Scout build?',options:[{id:'checkpoint-option-111111111111111111111111',label:'Documentary',action:'proceed'},{id:'checkpoint-option-222222222222222222222222',label:'Premium editorial',action:'revise'},{id:'checkpoint-option-333333333333333333333333',label:'Bold and playful with an expansive visual system for every slide',action:'hold'}]};
	   mountChannelCheckpointFixture('channel-checkpoint-fixture','goal-channel-checkpoint',checkpoint);
 });
 const card=page.locator('#channel-checkpoint-fixture .scout-chat-work-card');
 await card.waitFor({state:'visible'});
 assert.match(await card.getAttribute('aria-label'),/Question: Which creative direction/);
 assert.equal(await card.locator('.scout-chat-work-card__eyebrow').textContent(),'Presentation · Needs input');
 assert.equal(await card.locator('.scout-chat-work-card__checkpoint-question').textContent(),'Which creative direction should Scout build?');
 assert.match(await card.locator('.scout-chat-work-card__checkpoint-context').textContent(),/Creative direction/);
 const optionButtons=card.locator('.scout-chat-work-card__checkpoint-choices > .scout-chat-work-card__checkpoint-choice');
 assert.equal(await optionButtons.count(),3);
	assert.equal(await optionButtons.nth(2).getAttribute('data-checkpoint-action'),'hold');
 assert.equal(await card.locator('[role="group"]').getAttribute('aria-labelledby'),await card.locator('.scout-chat-work-card__checkpoint-question').getAttribute('id'));
	const bounds=await page.evaluate(()=>{const card=document.querySelector('#channel-checkpoint-fixture .scout-chat-work-card').getBoundingClientRect();const choices=[...document.querySelectorAll('#channel-checkpoint-fixture .scout-chat-work-card__checkpoint-choice')].map(node=>node.getBoundingClientRect().toJSON());return {card:card.toJSON(),choices};});
	assert.ok(bounds.card.width<=390&&bounds.card.width>300,JSON.stringify(bounds));
	assert.ok(bounds.choices.every(choice=>choice.left>=bounds.card.left&&choice.right<=bounds.card.right),JSON.stringify(bounds));
 await optionButtons.nth(0).focus();await page.keyboard.press('ArrowRight');
 assert.equal(await optionButtons.nth(1).evaluate(node=>node===document.activeElement),true);
 await optionButtons.nth(1).evaluate(button=>{button.click();button.click();});
	const revision=card.locator('.scout-chat-work-card__checkpoint-revision');
	await revision.waitFor({state:'visible'});
	assert.equal(await card.locator('.scout-chat-work-card__checkpoint').getAttribute('data-state'),'idle');
	assert.equal(requests.length,0);
	const revisionNote='Keep the current opening. Rebuild slides 2 and 3 with complete rendered content.';
	await revision.locator('textarea').fill(revisionNote);
	await revision.getByRole('button',{name:'Send changes'}).evaluate(button=>{button.click();button.click();});
 assert.equal(await card.locator('.scout-chat-work-card__checkpoint').getAttribute('data-state'),'pending');
 assert.equal(await optionButtons.nth(1).getAttribute('aria-pressed'),'true');
 assert.equal(await optionButtons.nth(0).isDisabled(),true);
	await page.waitForTimeout(40);
	assert.equal(requests.length,1);
	assert.deepEqual(requests[0],{id:'goal-channel-checkpoint',action:'approve',checkpointId:'goal-checkpoint-aaaaaaaaaaaaaaaaaaaaaaaa',checkpointOptionId:'checkpoint-option-222222222222222222222222',checkpointNote:revisionNote});
 await page.waitForFunction(()=>document.querySelector('#channel-checkpoint-fixture .scout-chat-work-card__checkpoint')?.dataset.state==='failed');
 assert.equal(await optionButtons.nth(1).getAttribute('data-choice-state'),'failed');
 assert.equal(await optionButtons.nth(0).isEnabled(),true);
 assert.match(await card.locator('[role="status"]').textContent(),/could not be saved/i);
	await revision.getByRole('button',{name:'Send changes'}).click();
 await page.waitForFunction(()=>document.querySelector('#channel-checkpoint-fixture .scout-chat-work-card__checkpoint')?.dataset.state==='selected');
	assert.equal(await optionButtons.nth(1).getAttribute('data-choice-state'),'selected');
	assert.equal(await optionButtons.nth(1).getAttribute('aria-pressed'),'true');
	assert.match(await card.locator('[role="status"]').textContent(),/sent back for revision/);
	assert.equal(requests.length,2);
	assert.deepEqual(requests[1],{id:'goal-channel-checkpoint',action:'approve',checkpointId:'goal-checkpoint-aaaaaaaaaaaaaaaaaaaaaaaa',checkpointOptionId:'checkpoint-option-222222222222222222222222',checkpointNote:revisionNote});
	assert.equal(Object.prototype.hasOwnProperty.call(requests[0],'choice'),false);
	await page.evaluate(()=>mountChannelCheckpointFixture('channel-hold-fixture','goal-channel-hold',{id:'goal-checkpoint-bbbbbbbbbbbbbbbbbbbbbbbb',stageId:'direction',question:'Should this work remain held?',options:[{id:'checkpoint-option-444444444444444444444444',label:'Keep this held',action:'hold'},{id:'checkpoint-option-555555555555555555555555',label:'Proceed now',action:'proceed'}]},'Hold this launch deck'));
	const holdCard=page.locator('#channel-hold-fixture .scout-chat-work-card');
	await holdCard.locator('.scout-chat-work-card__checkpoint-choice').nth(0).click();
	await page.waitForFunction(()=>document.querySelector('#channel-hold-fixture .scout-chat-work-card__checkpoint')?.dataset.state==='selected');
	assert.match(await holdCard.locator('[role="status"]').textContent(),/remains held/);
	assert.deepEqual(requests[2],{id:'goal-channel-hold',action:'approve',checkpointId:'goal-checkpoint-bbbbbbbbbbbbbbbbbbbbbbbb',checkpointOptionId:'checkpoint-option-444444444444444444444444'});
	assert.equal(requests.length,3);
	assert.ok(requests.every(request=>!Object.prototype.hasOwnProperty.call(request,'choice')));
	await page.evaluate(()=>mountChannelCheckpointFixture('channel-studio-copy','goal-channel-studio',{id:'goal-checkpoint-cccccccccccccccccccccccc',stageId:'ship_approval',question:'The deck is ready. What would you like to do?',options:[{id:'checkpoint-option-666666666666666666666666',label:'approve the ship',action:'proceed'},{id:'checkpoint-option-777777777777777777777777',label:'send back — rebuild the deck',action:'revise'},{id:'checkpoint-option-888888888888888888888888',label:'hold the package',action:'hold'},{id:'checkpoint-option-999999999999999999999999',label:'franchise-playbook',action:'proceed'}]},'Finish this deck'));
	const studioLabels=await page.locator('#channel-studio-copy .scout-chat-work-card__checkpoint-choice').allTextContents();
	assert.deepEqual(studioLabels,['Approve this version','Request changes','Keep on hold','Franchise playbook']);
	await page.evaluate(()=>{
	  const plan={state:'needs_attention',processId:'packaging_studio',subtasks:[{id:'voice',title:'Voice',status:'blocked'}]};
	  const artifact={id:'goal-channel-blocked',kind:'os_artifact',text:'# Saved deck draft',metadata:{mode:'goal',title:'Like A Farmer deck',status:'needs_attention',threadStatus:'needs_attention',goalPlan:JSON.stringify(plan)}};
	  const message={id:'work-message-blocked',thread:{id:'goal-channel-blocked',artifactId:'goal-channel-blocked',status:'needs_attention',mode:'goal',agentName:'Scout',query:'Finish the Like A Farmer deck'}};
	  addArtifactEntry(artifact,{select:false});
	  const fixture=document.createElement('div');fixture.id='channel-blocked-fixture';fixture.appendChild(scoutDesktopGoalWorkCardNode(message,artifact));chatTool.appendChild(fixture);
	});
	const blockedCard=page.locator('#channel-blocked-fixture .scout-chat-work-card');
	await blockedCard.getByRole('button',{name:/View .* activity/}).click();
	const retry=blockedCard.getByRole('button',{name:'Retry from here'});
	await retry.click();
	await page.waitForFunction(()=>document.querySelector('#channel-blocked-fixture .chat-context-action:last-child')?.textContent==='Resuming at the blocker…');
	assert.deepEqual(requests[3],{id:'goal-channel-blocked',action:'resume'});
	assert.equal(requests.length,4);
	await page.setViewportSize({width:1440,height:900});
	await blockedCard.getByRole('button',{name:/View .* activity/}).click();
	const contextPanel=page.getByRole('complementary',{name:'Conversation context'});
	const desktopRetry=contextPanel.getByRole('button',{name:'Retry from here'});
	assert.equal(await desktopRetry.count(),1);
	await desktopRetry.click();
	await page.waitForFunction(()=>[...document.querySelectorAll('.chat-context-action')].some(node=>node.textContent==='Resuming at the blocker…'));
	assert.deepEqual(requests[4],{id:'goal-channel-blocked',action:'resume'});
	assert.equal(requests.length,5);
	await page.setViewportSize({width:390,height:844});
	await page.evaluate(()=>{
	  const mount=(id,status,plan)=>{const artifact={id,kind:'os_artifact',text:'# Saved work',metadata:{mode:'goal',title:id,status,threadStatus:status,goalPlan:JSON.stringify(plan)}};const message={id:'message-'+id,thread:{id,artifactId:id,status,mode:'goal',agentName:'Scout',query:'Continue this work'}};const fixture=document.createElement('div');fixture.id='fixture-'+id;fixture.appendChild(scoutDesktopGoalWorkCardNode(message,artifact));chatTool.appendChild(fixture)};
	  mount('goal-channel-rejected','rejected',{state:'approval_required',processId:'packaging_studio',subtasks:[]});
	  mount('goal-channel-legacy','needs_attention',{state:'needs_attention',subtasks:[]});
	});
	for(const id of ['goal-channel-rejected','goal-channel-legacy']){
	  const candidate=page.locator('#fixture-'+id+' .scout-chat-work-card');
	  await candidate.getByRole('button',{name:/View .* activity/}).click();
	  assert.equal(await candidate.getByRole('button',{name:'Retry from here'}).count(),0);
	  await candidate.getByRole('button',{name:'Ask Scout'}).click();
	  assert.equal(requests.length,5);
	}
	await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "CHANNEL_CHECKPOINT_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered channel checkpoint harness: %v\n%s", err, output)
	}
}
