package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagingStageDrawerProgressiveJudgmentContract(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"const packagingStudioJudgmentStages",
		"red_team:",
		"compete_architects:",
		"compete_judges:",
		"function artifactStageActivityContext(entry)",
		"Blocking verdict in output",
		"Full stage output",
		"{ stageActivity: true }",
		"artifact-read__section-disclosure",
		"artifact-read__code-block",
		"wrap.setAttribute('role', 'region')",
		"position: sticky;",
		"overscroll-behavior: contain;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("packaging stage drawer contract missing %q", want)
		}
	}
	stageCSSStart := strings.Index(html, ".artifact-stage-activity {")
	stageCSSEnd := strings.Index(html[stageCSSStart:], ".artifact-stage__body--deck")
	if stageCSSStart < 0 || stageCSSEnd < 0 {
		t.Fatal("stage activity CSS boundaries missing")
	}
	if strings.Contains(html[stageCSSStart:stageCSSStart+stageCSSEnd], "transition: all") {
		t.Error("stage polish must not introduce transition: all")
	}
}

func TestContentStudioDesktopRailContract(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		`class="pd1-primary-nav__external"`,
		`href="https://kino.grok.me"`,
		`aria-label="Open Content Studio"`,
		`aria-haspopup="dialog"`,
		`function openContentStudio(returnFocus)`,
		`function closeContentStudio()`,
		`frame.referrerPolicy = 'strict-origin-when-cross-origin'`,
		`allow-scripts allow-forms allow-same-origin allow-popups allow-popups-to-escape-sandbox allow-downloads`,
		`external.target = '_blank'`,
		`external.rel = 'noopener noreferrer'`,
		`function setContentStudioBackgroundInert(inert)`,
		`appShell.setAttribute('inert', '')`,
		`ariaHidden: appShell.getAttribute('aria-hidden')`,
		`content-studio-drawer__focus-sentinel--before`,
		`content-studio-drawer__focus-sentinel--after`,
		`drawer.dataset.lastFocusBoundary = 'after'`,
		`.pd1-primary-nav__external:active { scale: 0.96; }`,
		`.pd1-primary-nav__external-wrap { display: none !important; }`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Content Studio rail contract missing %q", want)
		}
	}
	if strings.Contains(html, `href="https://www.kino.grok.me"`) {
		t.Error("Content Studio rail points at the unresolvable www host")
	}
	if strings.Contains(html, `Content Studio ↗`) {
		t.Error("Content Studio visible label must not add punctuation")
	}
}

func TestPackagingStageDrawerRenderedJourney(t *testing.T) {
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
const html=fs.readFileSync(process.env.PACKAGING_STAGE_INDEX,'utf8');
const tick=String.fromCharCode(96);
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
const tableRows=Array.from({length:18},(_,i)=>'| '+(i+1)+' | Objection '+(i+1)+' | '+('Specific risk evidence '.repeat(16))+' | Fix the claim |').join('\n');
const fixtures=[
 {id:'stage-red',stage:'red_team',title:'Red-team — the hostile room, with teeth',role:'panel',text:'# Red-team verdict\n\nBLOCKED FOR PRODUCTION.\n\nThe saved output explicitly says the source context is incomplete and the claims should not ship.\n\n## Objection ledger\n\n| # | objection | evidence | repair |\n|---|---|---|---|\n'+tableRows+'\n\n## Strengths to keep\n\n- Founder language is direct.\n- The wedge is memorable.'},
 {id:'stage-identity',stage:'identity',title:'Identity — develop the visual system',role:'judges',text:'# Identity direction\n\n## Decision\n\nDirection B wins because the panel recorded the strongest audience fit.\n\n## Tokens\n\n'+tick+tick+tick+'css\n:root {\n  --ink: #151513;\n  --paper: #f5f0e7;\n  --heat: #e45d32;\n}\n'+tick+tick+tick+'\n\n## Sources\n\n- [OpenAI API](https://api.openai.com/v1)'},
 {id:'stage-architects',stage:'compete_architects',title:'Compete — three rival narrative architects',role:'panel',text:'# Narrative competition\n\n## Spine matrix\n\n| beat | cultural moment | franchise playbook | founder conviction |\n|---|---|---|---|\n| opening | The shift | The machine | The earned insight |\n| ask | Move now | Build the flywheel | Back the founder |\n\n## Cultural moment\n\n'+('A complete slide-by-slide spine. '.repeat(85))+'\n\n## Franchise playbook\n\n'+('A distinct expandable narrative. '.repeat(85))},
 {id:'stage-judges',stage:'compete_judges',title:'Compete — judge the spines',role:'judges',text:'# Jury verdict\n\n## Winner\n\nFounder conviction wins unanimously, 4–0.\n\n## Scorecard\n\n| spine | excitement | coherence | credibility |\n|---|---:|---:|---:|\n| founder conviction | 9 | 9 | 9 |\n| cultural moment | 8 | 7 | 8 |'},
 {id:'stage-write',stage:'write',title:'Write — graft the winning spine',role:'synthesizer',text:'# Deck manuscript\n\n## Slide 1 — The opening\n\nThe recorded opening line.\n\n## Slide 2 — The shift\n\nThe recorded argument.\n\n## Speaker notes\n\n[BEAT] The recorded delivery note.\n\n## Composition\n\n| slide | layout | source note |\n|---|---|---|\n| 1 | full bleed | founder brief |'}
].map(entry=>({id:entry.id,text:entry.text,createdAt:new Date().toISOString(),metadata:{title:entry.title,type:'markdown',status:'complete',threadStatus:'complete',source:'process_stage',processId:'packaging_studio',processStage:entry.stage,goalSubtaskId:entry.stage,goalParentId:'packaging-goal',processRole:entry.role}}));
const checkpoint={id:'checkpoint-aaaaaaaaaaaaaaaaaaaaaaaa',stageId:'compete_choice',question:'Which narrative spine should become the deck backbone?',options:[{id:'option-111111111111111111111111',label:'Founder conviction',action:'proceed'},{id:'option-222222222222222222222222',label:'Cultural moment',action:'revise'},{id:'option-333333333333333333333333',label:'Hold for founder review',action:'hold'}]};
const parentPlan={state:'approval_required',checkpoint,subtasks:[{id:'compete_judges',title:'Compete — judge the spines',role:'judges',status:'complete',artifactId:'stage-judges'},{id:'compete_choice',title:'Choose the winning spine',role:'human_checkpoint',status:'running',dependsOn:['compete_judges']}]};
const parent={id:'packaging-goal',text:'# Packaging Studio',createdAt:new Date().toISOString(),metadata:{title:'Packaging Studio',mode:'goal',processId:'packaging_studio',status:'approval_required',threadStatus:'approval_required',goalPlan:JSON.stringify(parentPlan),checkpoint:JSON.stringify(checkpoint)}};
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const page=await browser.newPage({viewport:{width:1280,height:820}});
 await page.route('https://kino.grok.me/**',route=>route.fulfill({status:200,contentType:'text/html',body:'<!doctype html><title>KINO</title><main>KINO fixture</main>'}));
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 await page.evaluate(({entries,parent})=>{artifactEntries=[...entries,parent];const trigger=document.createElement('button');trigger.id='stage-return-focus';trigger.textContent='Open stage';document.body.appendChild(trigger);trigger.focus();},{entries:fixtures,parent});

 const external=page.locator('.pd1-primary-nav__external');
 await external.waitFor({state:'visible'});
 assert.equal(await external.getAttribute('href'),'https://kino.grok.me');
 assert.equal(await external.getAttribute('aria-haspopup'),'dialog');
 const externalBox=await external.boundingBox();
 assert.ok(externalBox.width>=40&&externalBox.height>=40,JSON.stringify(externalBox));
 const backgroundBefore=await page.locator('#appShell').evaluate(node=>({hadInert:node.hasAttribute('inert'),ariaHidden:node.getAttribute('aria-hidden')}));
 await external.click();
 const studio=page.locator('#contentStudioDrawer');
 await studio.waitFor({state:'visible'});
 assert.equal(await studio.locator('.content-studio-drawer__title').textContent(),'Content Studio');
 const studioFrame=studio.locator('iframe');
 assert.equal(await studioFrame.getAttribute('src'),'https://kino.grok.me');
 assert.equal(await studioFrame.getAttribute('title'),'Content Studio');
 assert.equal(await studioFrame.getAttribute('referrerpolicy'),'strict-origin-when-cross-origin');
 assert.match(await studioFrame.getAttribute('sandbox'),/allow-scripts/);
 const studioExternal=studio.locator('.content-studio-drawer__actions .content-studio-drawer__action');
 assert.equal(await studioExternal.getAttribute('href'),'https://kino.grok.me');
 assert.equal(await studioExternal.getAttribute('target'),'_blank');
 assert.equal(await studioExternal.getAttribute('rel'),'noopener noreferrer');
 assert.equal(await studio.locator('.content-studio-drawer__close').evaluate(node=>node===document.activeElement),true);
 assert.equal(await page.locator('#appShell').getAttribute('inert'),'');
 assert.equal(await page.locator('#appShell').getAttribute('aria-hidden'),'true');
 await page.evaluate(()=>{window.__contentStudioBackgroundFocus=[];document.addEventListener('focusin',event=>{const drawer=document.getElementById('contentStudioDrawer');const shell=document.getElementById('appShell');if(drawer&&shell?.contains(event.target))window.__contentStudioBackgroundFocus.push(event.target.id||event.target.getAttribute?.('aria-label')||event.target.tagName);});});
 // Close -> iframe is an explicit parent-owned handoff. When focus leaves the
 // foreign document, the after sentinel returns it to the first header action.
 await page.keyboard.press('Tab');
 assert.equal(await page.evaluate(()=>document.activeElement?.tagName),'IFRAME');
 await page.keyboard.press('Tab');
 await page.waitForTimeout(30);
 assert.equal(await studio.getAttribute('data-last-focus-boundary'),'after');
 assert.equal(await studioExternal.evaluate(node=>node===document.activeElement),true);
 // Reverse traversal mirrors the same contract through the before sentinel.
 await page.keyboard.press('Shift+Tab');
 assert.equal(await page.evaluate(()=>document.activeElement?.tagName),'IFRAME');
 await page.keyboard.press('Shift+Tab');
 await page.waitForTimeout(30);
 assert.equal(await studio.getAttribute('data-last-focus-boundary'),'before');
 assert.equal(await studio.locator('.content-studio-drawer__close').evaluate(node=>node===document.activeElement),true);
 assert.deepEqual(await page.evaluate(()=>window.__contentStudioBackgroundFocus),[]);
 await page.keyboard.press('Escape');
 await studio.waitFor({state:'detached'});
 assert.equal(await external.evaluate(node=>node===document.activeElement),true);
 assert.deepEqual(await page.locator('#appShell').evaluate(node=>({hadInert:node.hasAttribute('inert'),ariaHidden:node.getAttribute('aria-hidden')})),backgroundBefore);

 await page.locator('#stage-return-focus').focus();
 await page.evaluate(()=>openArtifactStage('stage-red','Red-team'));
 const dialog=page.locator('.artifact-stage');
 await dialog.waitFor({state:'visible'});
 assert.match(await dialog.locator('.artifact-stage__kicker').textContent(),/packaging studio · Red-team · Blocking verdict/);
 assert.equal(await dialog.locator('.artifact-stage-activity__state').getAttribute('data-tone'),'attention');
 assert.match(await dialog.locator('.artifact-stage-activity__summary').textContent(),/BLOCKED FOR PRODUCTION/);
 assert.equal(await dialog.locator('.artifact-stage-activity__record').getAttribute('open'),null);
 assert.equal(await dialog.locator('.artifact-stage-activity__record-body .artifact-read__section').count(),0);
 assert.equal(await dialog.locator('.artifact-stage__close').evaluate(node=>node===document.activeElement),true);
 assert.equal((await dialog.locator('.artifact-stage__head').evaluate(node=>getComputedStyle(node).position)),'sticky');
 await dialog.locator('.artifact-stage-activity__record > summary').click();
 await dialog.locator('.artifact-read__section').first().waitFor({state:'visible'});
 assert.ok(await dialog.locator('.artifact-read__section-disclosure').count()>=1);
 const ledger=dialog.locator('.artifact-read__section-disclosure').first();
 await ledger.locator('summary').click();
 const tableRegion=dialog.locator('.artifact-read__table-wrap').first();
 await tableRegion.waitFor({state:'visible'});
 assert.equal(await tableRegion.getAttribute('role'),'region');
 assert.equal(await tableRegion.getAttribute('tabindex'),'0');
 assert.match(await tableRegion.getAttribute('aria-label'),/Scrollable table/);
 await page.keyboard.press('Escape');
 await dialog.waitFor({state:'detached'});
 assert.equal(await page.locator('#stage-return-focus').evaluate(node=>node===document.activeElement),true);

 for(const fixture of fixtures.slice(1)){
   await page.evaluate(id=>openArtifactStage(id,id),fixture.id);
   await page.locator('.artifact-stage').waitFor({state:'visible'});
   assert.match(await page.locator('.artifact-stage__kicker').textContent(),new RegExp(fixture.metadata.title.split(' — ')[0].replace('Compete','Compete')));
   await page.locator('.artifact-stage__close').click();
 }

 await page.evaluate(()=>openArtifactStage('stage-judges','Compete judges'));
 const checkpointPanel=page.locator('.artifact-stage-activity__checkpoint');
 await checkpointPanel.waitFor({state:'visible'});
 assert.equal(await checkpointPanel.locator('.scout-chat-work-card__checkpoint-question').textContent(),'Which narrative spine should become the deck backbone?');
 assert.equal(await checkpointPanel.locator('.scout-chat-work-card__checkpoint-choice').count(),3);
 assert.equal(await checkpointPanel.locator('[role="group"]').getAttribute('aria-labelledby'),await checkpointPanel.locator('.scout-chat-work-card__checkpoint-question').getAttribute('id'));
 await page.locator('.artifact-stage__close').click();

 await page.evaluate(()=>openArtifactStage('stage-identity','Identity'));
 await page.locator('.artifact-stage-activity__record > summary').click();
 await page.locator('.artifact-read__code-block').waitFor({state:'visible'});
 assert.match(await page.locator('.artifact-read__code-block').textContent(),/--heat/);
 assert.equal(await page.locator('.artifact-stage-activity__record-body').textContent().then(text=>text.includes('api.openai.com')),false);
 await page.locator('.artifact-stage__close').click();

 await page.setViewportSize({width:390,height:844});
 assert.equal(await external.isVisible(),false);
 await page.evaluate(()=>openArtifactStage('stage-architects','Compete architects'));
 const mobileDialog=page.locator('.artifact-stage');
 await mobileDialog.waitFor({state:'visible'});
 await page.waitForTimeout(400);
 const geometry=await page.evaluate(()=>{const panel=document.querySelector('.artifact-stage__panel').getBoundingClientRect();const overview=document.querySelector('.artifact-stage-activity__overview').getBoundingClientRect();const record=document.querySelector('.artifact-stage-activity__record > summary').getBoundingClientRect();return {panel:panel.toJSON(),overview:overview.toJSON(),record:record.toJSON(),scrollWidth:document.documentElement.scrollWidth};});
 assert.ok(geometry.panel.width<=390&&geometry.overview.right<=390&&geometry.record.right<=390,JSON.stringify(geometry));
 assert.ok(geometry.record.height>=52,JSON.stringify(geometry));
 assert.ok(geometry.scrollWidth<=390,JSON.stringify(geometry));
 await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PACKAGING_STAGE_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered packaging stage drawer harness: %v\n%s", err, output)
	}
}
