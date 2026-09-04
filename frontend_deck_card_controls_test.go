package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A compact/legacy thread projection can omit true publication booleans even
// though the exact current deck endpoint admits the revision. The channel card
// must let that fresh server grant win, while a current draft still stays
// closed and explains why.
func TestLatestAdmittedDeckCardHasOneCompleteControlSetRendered(t *testing.T) {
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
const html=fs.readFileSync(process.env.DECK_CARD_INDEX,'utf8');
const admittedId='admitted-channel-deck';
const draftId='draft-channel-deck';
const readOnlyId='read-only-channel-deck';
const digest='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const deck={schemaVersion:1,width:1920,height:1080,slides:[
  {id:'cover',background:'#17211c',elements:[{id:'cover-title',type:'text',x:150,y:160,width:1200,height:180,z:1,opacity:1,rotation:0,text:'A field-tested idea',fontSize:82,fontFamily:'Arial',fontWeight:700,color:'#fff',textAlign:'left',lineHeight:1.05,letterSpacing:'normal'}]},
  {id:'proof',background:'#f4eddf',elements:[{id:'proof-title',type:'text',x:150,y:160,width:1200,height:180,z:1,opacity:1,rotation:0,text:'The proof',fontSize:82,fontFamily:'Arial',fontWeight:700,color:'#17211c',textAlign:'left',lineHeight:1.05,letterSpacing:'normal'}]}
]};
const json=(res,status,payload)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify(payload));};
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me')return json(res,200,{email:'aj@shareability.com',name:'AJ',shellAccess:'full'});
  if(req.url==='/readyz')return json(res,200,{ok:true,checks:{agents:{renderRunner:{heartbeatOK:true}}}});
  if(req.url.startsWith('/artifacts/final-export-capability')){
    const id=new URL(req.url,'http://127.0.0.1').searchParams.get('id');
    const admitted=id===admittedId||id===readOnlyId;
    return json(res,200,{ok:true,artifactId:id,artifactVersion:1,qualityState:admitted?'admitted':'draft_needs_attention',managed:true,canPresent:admitted,canExport:id===admittedId});
  }
  if(req.url.startsWith('/artifacts/deck?id=')){
    const id=new URL(req.url,'http://127.0.0.1').searchParams.get('id');
    const admitted=id===admittedId||id===readOnlyId;
    return json(res,200,{ok:true,artifact:{id,title:admitted?'Like A Farmer — Field Network':'Working deck',version:1,contentDigest:digest},deck,canWrite:id!==readOnlyId,qualityState:admitted?'admitted':'draft_needs_attention',canPresent:admitted,canExport:id===admittedId});
  }
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')||req.url.startsWith('/brain/'))return json(res,503,{});
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:1000}});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto('http://127.0.0.1:'+server.address().port,{waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.evaluate(({admittedId,draftId,readOnlyId,digest})=>{
    const artifact=(id,title)=>({id,text:'<!doctype html><html><body></body></html>',metadata:{title,type:'html_deck',status:'complete',artifactVersion:'1',contentDigest:digest,goalParentId:id+'-goal'}});
    const admitted=artifact(admittedId,'Like A Farmer — Field Network');
    const draft=artifact(draftId,'Working deck');
    const readOnly=artifact(readOnlyId,'Like A Farmer — Read only');
    const older={id:'admitted-result-old',kind:'thread',thread:{artifactId:admittedId+'-goal',mode:'goal',resultArtifactId:admittedId,resultArtifactType:'html_deck',resultArtifactVersion:1,resultArtifactDigest:digest,resultTitle:'Like A Farmer — Field Network',resultQualityState:'admitted',resultCanEdit:true}};
    // Deliberately omit resultCanPresent/resultCanExport. Older compact
    // projections encoded false by omission, but the current endpoint below
    // is the exact-revision authority and admits this deck.
    const latest={...older,id:'admitted-result-latest'};
    const draftMessage={id:'draft-result',kind:'thread',thread:{artifactId:draftId+'-goal',mode:'goal',resultArtifactId:draftId,resultArtifactType:'html_deck',resultArtifactVersion:1,resultArtifactDigest:digest,resultTitle:'Working deck',resultQualityState:'draft_needs_attention',resultCanEdit:true}};
    const readOnlyMessage={id:'read-only-result',kind:'thread',thread:{artifactId:readOnlyId+'-goal',mode:'goal',resultArtifactId:readOnlyId,resultArtifactType:'html_deck',resultArtifactVersion:1,resultArtifactDigest:digest,resultTitle:'Like A Farmer — Read only',resultQualityState:'admitted',resultCanEdit:false,resultCanPresent:true,resultCanExport:false}};
    artifactEntries=[admitted,draft,readOnly];
    scoutChatThreads=[{id:'deck-controls-thread',messages:[older,latest,draftMessage,readOnlyMessage]}];
    activeScoutThreadId='deck-controls-thread';
    const host=document.createElement('main');host.id='deck-controls-host';host.style.cssText='width:760px;padding:20px';document.body.append(host);
    host.append(scoutHTMLDeckRefRecordNode(older,admitted),scoutHTMLDeckRefRecordNode(latest,admitted),scoutHTMLDeckRefRecordNode(draftMessage,draft),scoutHTMLDeckRefRecordNode(readOnlyMessage,readOnly));
  },{admittedId,draftId,readOnlyId,digest});

	const admitted=page.locator('[data-result-artifact-id="'+admittedId+'"]');
	await admitted.locator('.chat-deck__native-preview.is-ready').waitFor({state:'attached'});
	await admitted.getByRole('button',{name:'Present',exact:true}).waitFor({state:'visible'});
	assert.equal(await admitted.getAttribute('aria-label'),'Like A Farmer — Field Network presentation');
	assert.equal(await page.locator('[data-result-artifact-id="'+admittedId+'"]').count(),1,'latest-wins must leave one rich card');
  assert.equal(await admitted.getByRole('button',{name:'Previous slide',exact:true}).count(),1);
  assert.equal(await admitted.getByRole('button',{name:'Next slide',exact:true}).count(),1);
  assert.equal(await admitted.getByRole('button',{name:'Edit',exact:true}).count(),1);
  assert.equal(await admitted.getByRole('button',{name:'Present',exact:true}).count(),1);
  const geometry=await admitted.evaluate(card=>{
    const box=node=>node.getBoundingClientRect().toJSON();
    return {card:box(card.querySelector('.chat-deck')),nav:box(card.querySelector('.chat-deck__nav')),actions:box(card.querySelector('.chat-deck__actions'))};
  });
  assert.ok(geometry.nav.top<geometry.card.top+geometry.card.height/2,JSON.stringify(geometry));
  assert.ok(geometry.nav.left>geometry.card.left+geometry.card.width/2,JSON.stringify(geometry));
  assert.ok(geometry.actions.top>geometry.card.top+geometry.card.height/2,JSON.stringify(geometry));
  assert.ok(geometry.actions.left>geometry.card.left+geometry.card.width/2,JSON.stringify(geometry));

  const readOnly=page.locator('[data-result-artifact-id="'+readOnlyId+'"]');
  await readOnly.locator('.chat-deck__native-preview.is-ready').waitFor({state:'attached'});
  await readOnly.getByRole('button',{name:'Present',exact:true}).waitFor({state:'visible'});
  assert.equal(await readOnly.getByRole('button',{name:'Edit',exact:true}).count(),0,'read authority must not imply edit authority');
  assert.equal(await readOnly.locator('.chat-deck__download:visible').count(),0,'presentation authority must not imply export authority');

  const draft=page.locator('[data-result-artifact-id="'+draftId+'"]');
  await draft.locator('.chat-deck__native-preview.is-ready').waitFor({state:'attached'});
  assert.equal(await draft.getByRole('button',{name:'Present',exact:true}).count(),0,'a current unreviewed revision must remain closed');
  assert.match(await draft.innerText(),/Draft · needs attention/);
  await draft.getByRole('button',{name:'Edit',exact:true}).click();
  const editor=page.locator('.deck-editor');
  await editor.waitFor({state:'visible'});
  assert.equal((await editor.locator('[data-publication-state]').innerText()).trim(),'Review needed to present');
  assert.match(await editor.locator('[data-publication-state]').getAttribute('title'),/has not passed review/);
  assert.deepEqual(errors,[]);
  await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DECK_CARD_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered deck-card controls harness: %v\n%s", err, output)
	}
}
