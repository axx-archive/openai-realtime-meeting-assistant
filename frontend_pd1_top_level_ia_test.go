package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func pd1Index(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func pd1Slice(t *testing.T, source, start, end string) string {
	t.Helper()
	from := strings.Index(source, start)
	if from < 0 {
		t.Fatalf("missing slice start %q", start)
	}
	to := strings.Index(source[from:], end)
	if to < 0 {
		t.Fatalf("missing slice end %q after %q", end, start)
	}
	return source[from : from+to]
}

func TestPD1PrimaryInformationArchitectureIsExactAndOrdered(t *testing.T) {
	html := pd1Index(t)
	nav := pd1Slice(t, html, `<nav id="pd1PrimaryNav"`, `</nav>`)
	re := regexp.MustCompile(`data-pd1-destination="([^"]+)"`)
	matches := re.FindAllStringSubmatch(nav, -1)
	want := []string{"Home", "Work", "Network", "Work Search", "You"}
	if len(matches) != len(want) {
		t.Fatalf("primary destination count=%d, want %d: %v", len(matches), len(want), matches)
	}
	for i, destination := range want {
		if matches[i][1] != destination {
			t.Fatalf("primary destination[%d]=%q, want %q", i, matches[i][1], destination)
		}
	}
	for _, marker := range []string{
		`aria-label="Primary"`,
		`aria-current="page" tabindex="0">Home`,
		`aria-current="false" tabindex="-1">Work`,
		`const PD1_DESTINATIONS = Object.freeze(['Home', 'Work', 'Network', 'Work Search', 'You'])`,
		`aria-label="Work tools"`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("missing governed IA marker %q", marker)
		}
	}
}

func TestPD1ParentOffDestinationsAreOpaqueAndMakeNoProjectionRequest(t *testing.T) {
	html := pd1Index(t)
	surface := pd1Slice(t, html, `<section id="pd1DestinationSurface"`, `</section>`)
	for _, marker := range []string{
		`data-pd1-state="network-off"`,
		`The public network is off.`,
		`Feature off · no public projection loaded`,
		`data-pd1-state="work-search-off"`,
		`Work Search is off.`,
		`Feature off · no search performed`,
	} {
		if !strings.Contains(surface, marker) {
			t.Errorf("off-state surface missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		`/api/`, `fetch(`, `data-stride-w2-route`, `data-stride-w2-action`,
		`WorkSearchResult`, `PublicWorkspaceView`, `ConsensusDisplay`, `ModerationCaseView`,
	} {
		if strings.Contains(surface, forbidden) {
			t.Errorf("opaque parent-off surface exposes child/projection marker %q", forbidden)
		}
	}
	handler := pd1Slice(t, html, `function selectPD1Destination(destination, options = {})`, `window.selectPD1Destination`)
	for _, exact := range []string{
		`showPD1DestinationSurface('network-off', destination, options.focus !== false)`,
		`showPD1DestinationSurface('work-search-off', destination, options.focus !== false)`,
		`showPD1DestinationSurface('you', 'You', options.focus !== false)`,
	} {
		if !strings.Contains(handler, exact) {
			t.Errorf("closed destination handler missing %q", exact)
		}
	}
	for _, forbidden := range []string{`fetch(`, `loadProjection(`, `openStrideContributionSurface`, `/network/search`, `/network/preview`} {
		if strings.Contains(handler, forbidden) {
			t.Errorf("primary destination handler bypasses parent gate through %q", forbidden)
		}
	}
}

func TestPD1NavigationPreservesExistingHomeWorkAndAuthorityFences(t *testing.T) {
	html := pd1Index(t)
	handler := pd1Slice(t, html, `function selectPD1Destination(destination, options = {})`, `window.selectPD1Destination`)
	for _, marker := range []string{
		`if (!closePD1OverlaysForNavigation()) return false`,
		`setActiveTool('office', { history: false })`,
		`setActiveTool('chat', { history: false })`,
		`openSettings({ section: 'profile'`,
		`window.closeStrideContributionSurface?.() === false`,
		`const PD1_PATHS = Object.freeze({ Home: '/', Work: '/work', Network: '/network', 'Work Search': '/work-search', You: '/you' })`,
		`history.pushState({ view: 'pd1', destination }, '', path)`,
		`selectPD1Destination(destination, { push: false, focus: true })`,
		`setActiveTool('chat', { history: false })`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("PD1 compatibility/authority marker missing %q", marker)
		}
	}
	for _, preserved := range []string{
		`scoutChatThread.scrollTop = scoutChatThread.scrollHeight`,
		`loadScoutChatThreads({ onlyIfChanged: true })`,
		`history.pushState({ view: 'tool', tool: next }, '')`,
		`window.openStrideContributionSurface?.(networkPath ? path : '/me')`,
		`function trapSettingsFocus(event)`,
	} {
		if !strings.Contains(html, preserved) {
			t.Errorf("existing route/interaction behavior no longer present: %q", preserved)
		}
	}
	if !strings.Contains(handler, `if (!PD1_DESTINATIONS.includes(destination) || guestMode || !appShell.classList.contains('is-authed')) return false`) {
		t.Fatal("guest/unknown destination navigation is not fail closed")
	}
}

func TestPD1WorkRailIsContextualAndResearchCardsRemainTruthful(t *testing.T) {
	html := pd1Index(t)
	for _, marker := range []string{
		`#appShell:not([data-pd1-destination="Work"]) .tool-rail { display: none !important; }`,
		`toolRail.hidden = !workContext`,
		`toolRail.inert = !workContext`,
		`.scout-chat-thread > .scout-chat-research[data-state="complete"]`,
		`flex: 0 0 auto`,
		`function researchArtifactCurrentSourceSummary(entry)`,
		`metadata.researchCitationCount`,
		`metadata.researchSourceDomainCount`,
		`metadata.researchQualityGate`,
		`metadata.researchSourceWindowDigest`,
		`cited source link`,
		`sourceSummary ? ` + "`Research delivered · ${sourceSummary}`" + ` : 'Research delivered'`,
		`return 'Needs attention'`,
		`String(ref.artifactId || '').trim() === artifactId && String(ref.id || '').trim() === runId`,
		`String(artifactMetadata.originId || '').trim() === String(candidate?.id || '').trim()`,
		`thread: { ...ref, status: String(status || '') }`,
		`artifactStatusValue(artifact) || message.thread.status`,
		`previewEl.textContent = activeWork ? 'Scout is working' : preview`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("missing contextual/truthful layout marker %q", marker)
		}
	}
}

func TestPD1KeyboardFocusResponsiveAndMotionContracts(t *testing.T) {
	html := pd1Index(t)
	for _, marker := range []string{
		`if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return`,
		`button.focus()`,
		`button.tabIndex = current ? 0 : -1`,
		`button.setAttribute('aria-current', current ? 'page' : 'false')`,
		`min-width: 40px`,
		`min-height: 40px`,
		`min-height: 44px`,
		`@media (max-width: 760px)`,
		`.pd1-primary-nav { order: 20; flex: 1 0 100%; justify-content: flex-start; }`,
		`.pd1-primary-nav *, .pd1-destination-surface * { scroll-behavior: auto !important; transition: none !important; animation: none !important; }`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("PD1 interaction/polish contract missing %q", marker)
		}
	}
	css := pd1Slice(t, html, `/* PD1: one governed top-level IA.`, `@media (prefers-reduced-motion: reduce)`)
	if strings.Contains(css, "transition: all") || strings.Contains(css, "will-change") {
		t.Fatal("PD1 shell uses prohibited broad transition/compositing hint")
	}
}

func TestPD1RenderedBrowserNavigationHistoryFocusAndLayout(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
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
const html = fs.readFileSync(process.env.PD1_INDEX, 'utf8');
const projectionRequests = [];
const server = http.createServer((req, res) => {
  if (req.url === '/public/composer-dictation.js') { res.writeHead(200, {'content-type':'application/javascript'}); return res.end(''); }
  if (req.url === '/auth/me') { res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({email:'synthetic@example.test',name:'Synthetic'})); }
  if (req.url.startsWith('/api/stride/')) { projectionRequests.push(req.url); res.writeHead(503, {'content-type':'application/json'}); return res.end('{}'); }
  if (req.url.startsWith('/api/') || req.url.startsWith('/assistant/') || req.url.startsWith('/notifications') || req.url.startsWith('/rooms') || req.url.startsWith('/artifacts')) { res.writeHead(404, {'content-type':'application/json'}); return res.end('{}'); }
  res.writeHead(200, {'content-type':'text/html; charset=utf-8'}); res.end(html);
});
(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = 'http://127.0.0.1:' + server.address().port;
  const browser = await chromium.launch({headless:true});
  const page = await browser.newPage({viewport:{width:1280,height:800}});
  page.on('pageerror', error => console.error('pageerror:', error.message));
  await page.goto(base + '/', {waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.evaluate(() => { const marker=document.createElement('span'); marker.id='pd1-thread-continuity'; document.getElementById('scoutChatThread').append(marker); });
  projectionRequests.length = 0;
  await page.click('#pd1PrimaryNav [data-pd1-destination="Network"]');
  await page.waitForFunction(() => location.pathname === '/network' && document.activeElement?.textContent === 'The public network is off.');
  assert.equal(await page.locator('#toolRail').evaluate(el => el.hidden && el.inert && getComputedStyle(el).display === 'none'), true);
  assert.deepEqual(projectionRequests.filter(url => /network|search|contact|public/i.test(url)), []);
  await page.click('#pd1PrimaryNav [data-pd1-destination="Work Search"]');
  await page.waitForFunction(() => location.pathname === '/work-search');
  await page.click('#pd1PrimaryNav [data-pd1-destination="You"]');
  await page.waitForFunction(() => location.pathname === '/you');
  assert.equal(await page.locator('#pd1-thread-continuity').count(), 1);
  await page.goBack();
  await page.waitForTimeout(250);
  const backState = await page.evaluate(() => ({path:location.pathname,current:document.querySelector('#pd1PrimaryNav [data-pd1-destination="Work Search"]').getAttribute('aria-current'),destination:document.getElementById('appShell').dataset.pd1Destination}));
  assert.deepEqual(backState, {path:'/work-search',current:'page',destination:'Work Search'});
  await page.reload({waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed[data-pd1-destination="Work Search"]');
  const blockedChildren = [
    ['/network/preview','Network'], ['/network/recruiter','Network'], ['/network/search','Work Search'],
    ['/network/contact','Work Search'], ['/network/blocks','Network'], ['/network/future-child','Network'],
  ];
  for (const [path,destination] of blockedChildren) {
    projectionRequests.length = 0;
    await page.goto(base + path, {waitUntil:'domcontentloaded'});
    await page.waitForSelector('#appShell.is-authed');
    await page.waitForFunction(expected => document.getElementById('appShell').dataset.pd1Destination === expected && document.getElementById('strideW2Surface').hidden && document.getElementById('strideW2Canvas').childElementCount === 0, destination);
    assert.deepEqual(projectionRequests.filter(url => /network-(?:preview|recruiter-view|search|blocks)|contact-inbox/.test(url)), [], path + ' must not request a W2 child projection');
    assert.equal(await page.locator('#strideW2Canvas [data-stride-w2-action-id], #strideW2Canvas .stride-w2__main').count(), 0, path + ' must not mount W2 child markup');
    await page.evaluate(() => history.pushState({view:'tool',tool:'work'},'', '/work'));
    await page.goBack();
    await page.waitForTimeout(75);
    assert.equal(new URL(page.url()).pathname, path);
    assert.equal(await page.locator('#strideW2Canvas').evaluate(node => node.childElementCount), 0);
  }
  projectionRequests.length = 0;
  await page.goto(base + '/network/draft', {waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.waitForFunction(() => !document.getElementById('strideW2Surface').hidden);
  assert.ok(projectionRequests.some(url => url.includes('/mobile/surfaces/network-draft')), 'private network draft remains the sole admitted network child');
  await page.click('#pd1PrimaryNav [data-pd1-destination="Work"]');
  await page.waitForFunction(() => location.pathname === '/work' && document.querySelector('#toolRail').hidden === false);
  await page.evaluate(() => { window.__pd1Close = window.closeStrideContributionSurface; window.closeStrideContributionSurface = () => false; });
  await page.click('#pd1PrimaryNav [data-pd1-destination="Network"]');
  assert.equal(new URL(page.url()).pathname, '/work');
  await page.evaluate(() => { window.closeStrideContributionSurface = window.__pd1Close; });
  await page.focus('#pd1PrimaryNav [data-pd1-destination="Work"]');
  await page.keyboard.press('ArrowRight');
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('data-pd1-destination')), 'Network');
  await page.setViewportSize({width:320,height:700});
  const responsive = await page.locator('#pd1PrimaryNav').evaluate(el => ({overflow:getComputedStyle(el).overflowX, fits:document.documentElement.scrollWidth <= innerWidth, scrollable:el.scrollWidth >= el.clientWidth}));
  assert.equal(responsive.overflow, 'auto'); assert.equal(responsive.fits, true); assert.equal(responsive.scrollable, true);
  await page.setViewportSize({width:1280,height:800});
  await page.click('#pd1PrimaryNav [data-pd1-destination="Work"]');
  const card = await page.evaluate(() => {
    const node=document.createElement('article'); node.className='scout-chat-research'; node.dataset.state='complete';
    node.innerHTML='<div class="scout-chat-research__body"><p>complete report</p><div class="scout-chat-research__actions"><button>view report</button></div></div>';
    document.getElementById('scoutChatThread').append(node);
    const action=node.querySelector('button').getBoundingClientRect();
    return {shrink:getComputedStyle(node).flexShrink,height:node.getBoundingClientRect().height,actionHeight:action.height};
  });
  assert.equal(card.shrink, '0'); assert.ok(card.height > 20); assert.ok(card.actionHeight > 0); assert.ok(card.height >= card.actionHeight);
  const qualityDigest = 'a'.repeat(64);
  const summary = await page.evaluate(digest => researchArtifactCurrentSourceSummary({text:'v2 https://one.test\nprevious v1 https://two.test',metadata:{researchQualityGate:'passed',researchSourceWindowDigest:digest,researchCitationCount:12,researchSourceDomainCount:10,researchWebSearchCallCount:15}}), qualityDigest);
  assert.deepEqual(summary, {count:12,label:'12 cited source links · 10 domains',preview:'12 cited source links · 10 domains',authority:'verified_current_metadata'});
  const unverified = await page.evaluate(() => researchArtifactCurrentSourceSummary({text:'v2 https://one.test\nprevious v1 https://two.test',metadata:{researchCitationCount:35,researchSourceDomainCount:20}}));
  assert.deepEqual(unverified, {count:0,label:'',preview:'',authority:'unverified'});
  assert.equal(await page.evaluate(() => scoutResearchTerminalPreview('error', {metadata:{threadStatus:'error'}})), 'Needs attention');
  assert.equal(await page.evaluate(() => scoutResearchTerminalPreview('running', {})), '');
  const terminal = await page.evaluate(() => {
    const artifact={id:'artifact-current',status:'complete',updatedAt:'2026-08-09T23:59:00Z',text:'v2 and prior history',metadata:{threadStatus:'complete',status:'complete',threadId:'run-alpha',originKind:'channel',originId:'channel-alpha',researchQualityGate:'passed',researchSourceWindowDigest:'a'.repeat(64),researchCitationCount:12,researchSourceDomainCount:10}};
    artifactEntries=[artifact];
    scoutChatThreads=[{id:'channel-alpha',title:'Bonfire Chat',visibility:'public',preview:'research workstream confirmed — running now',updatedAt:'2026-08-09T23:00:00Z',messages:[{id:'work-message',kind:'thread',thread:{id:'run-alpha',artifactId:'artifact-current',status:'running'}}]}];
    const marker=document.createElement('article'); marker.dataset.threadArtifactId='artifact-current'; marker.dataset.threadRunId='run-alpha';
    const activeBefore=chatThreadActiveWork(scoutChatThreads[0]);
    const wrongRun=document.createElement('article'); wrongRun.dataset.threadArtifactId='artifact-current'; wrongRun.dataset.threadRunId='run-other';
    const wrongArtifact=document.createElement('article'); wrongArtifact.dataset.threadArtifactId='artifact-other'; wrongArtifact.dataset.threadRunId='run-alpha';
    const mismatches=[syncScoutThreadTerminalPreview(wrongRun,'complete',artifact),syncScoutThreadTerminalPreview(wrongArtifact,'complete',artifact),syncScoutThreadTerminalPreview(marker,'error',artifact),syncScoutThreadTerminalPreview(marker,'complete',{...artifact,metadata:{...artifact.metadata,originId:'channel-other'}})];
    const synced=syncScoutThreadTerminalPreview(marker,'complete',artifact);
    return {activeBefore:Boolean(activeBefore),mismatches,synced,preview:scoutChatThreads[0].preview,status:scoutChatThreads[0].messages[0].thread.status,activeAfter:Boolean(chatThreadActiveWork(scoutChatThreads[0]))};
  });
  assert.deepEqual(terminal,{activeBefore:false,mismatches:[false,false,false,false],synced:true,preview:'Research delivered · 12 cited source links · 10 domains',status:'complete',activeAfter:false});
  const activeRowPreview = await page.evaluate(() => {
    artifactEntries=[{id:'artifact-follow-up',status:'running',metadata:{threadStatus:'running',status:'running',threadId:'run-follow-up',originKind:'channel',originId:'channel-follow-up'}}];
    const thread={id:'channel-follow-up',title:'Bonfire Chat',visibility:'public',preview:'Research delivered · 12 cited source links · 10 domains',updatedAt:'2026-08-10T00:01:00Z',messages:[{id:'follow-up-work',kind:'thread',text:'Research delivered · 12 cited source links · 10 domains',thread:{id:'run-follow-up',artifactId:'artifact-follow-up',status:'running',startedAt:'2026-08-10T00:00:00Z'}}]};
    const row=chatThreadRowNode(thread,'');
    return {preview:row.querySelector('.chat-thread-item__preview')?.textContent,timer:Boolean(row.querySelector('.chat-thread-item__work-timer'))};
  });
  assert.deepEqual(activeRowPreview,{preview:'Scout is working',timer:true});
  await browser.close(); server.close();
})().catch(error => { console.error(error); server.close(); process.exit(1); });`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PD1_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered browser harness: %v\n%s", err, output)
	}
}
