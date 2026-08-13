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
	want := []string{"Home", "Video", "Chat", "Work", "Network", "Work Search", "You"}
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
		`data-pd1-destination="Home" aria-current="page" tabindex="0"`,
		`data-pd1-destination="Work" aria-current="false" aria-haspopup="menu" aria-expanded="false" tabindex="-1"`,
		`const PD1_DESTINATIONS = Object.freeze(['Home', 'Video', 'Chat', 'Work', 'Network', 'Work Search', 'You'])`,
		`aria-label="Application navigation"`,
		`id="workToolMenu" class="work-tool-menu" role="menu" aria-label="Work spaces"`,
		`role="menuitemradio" tabindex="-1" data-tool="chat"`,
		`if (button.getAttribute('role') === 'menuitemradio') button.setAttribute('aria-checked', active ? 'true' : 'false')`,
		`#appShell:not([data-pd1-destination="Work"]) .work-tool-menu`,
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
		`setActiveTool('room', { history: false })`,
		`setActiveTool('chat', { history: false })`,
		`setActiveTool('artifacts', { history: false })`,
		`openSettings({ section: 'profile'`,
		`window.closeStrideContributionSurface?.() === false`,
		`const PD1_PATHS = Object.freeze({ Home: '/', Video: '/video', Chat: '/chat', Work: '/work', Network: '/network', 'Work Search': '/work-search', You: '/you' })`,
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
	if !strings.Contains(handler, `if (!pd1DestinationAllowed(destination) || guestMode || !appShell.classList.contains('is-authed')) return false`) {
		t.Fatal("guest/unknown destination navigation is not fail closed")
	}
}

func TestPD1ShellAccessHidesUnfinishedDestinationsAndFailsClosed(t *testing.T) {
	html := pd1Index(t)
	for _, marker := range []string{
		`const PD1_CORE_DESTINATIONS = Object.freeze(['Home', 'Video', 'Chat'])`,
		`authedUser?.shellAccess === 'full'`,
		`button.hidden = !allowed`,
		`button.disabled = !allowed`,
		`button.inert = !allowed`,
		`.pd1-primary-nav__item[hidden] { display: none !important; }`,
		`const visibleButtons = pd1DestinationButtons.filter(button => !button.hidden && !button.disabled)`,
		`history.replaceState({ view: 'pd1', destination: 'Home' }, '', PD1_PATHS.Home)`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("missing server-owned shell access marker %q", marker)
		}
	}
}

func TestPD1SignInRestoresTheCanonicalDestinationForTheCurrentPath(t *testing.T) {
	html := pd1Index(t)
	handler := pd1Slice(t, html, `async function signInToOffice()`, `async function signInWithPasskey()`)
	if !strings.Contains(handler, `renderLoginMode()`) || !strings.Contains(handler, `syncAuthenticatedShell()`) {
		t.Fatal("sign-in must render auth state and restore the canonical path destination")
	}
	if strings.Contains(handler, `setActiveTool('office')`) {
		t.Fatal("sign-in must not override /work or another canonical destination with Home")
	}
}

func TestPD1GlobalRailAndContextualWorkMenuRemainTruthful(t *testing.T) {
	html := pd1Index(t)
	for _, marker := range []string{
		`#appShell:not([data-pd1-destination="Work"]) .work-tool-menu { display: none !important; }`,
		`toolRail.hidden = !shellVisible`,
		`toolRail.inert = !shellVisible`,
		`function setWorkToolMenuOpen(open, options = {})`,
		`workDestinationButton.setAttribute('aria-expanded', next ? 'true' : 'false')`,
		`class="tool-rail__utilities" aria-label="Account and display controls"`,
		`<span class="tool-rail__label">Notifications</span>`,
		`<span class="tool-rail__label">Appearance</span>`,
		`#appShell.is-authed .tool-rail__utilities .tool-rail__label`,
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

func TestPD1NavigationUsesQuietSelectionWithoutRailOrMeetingStatusOrnaments(t *testing.T) {
	html := pd1Index(t)
	for _, forbidden := range []string{
		`.pd1-primary-nav__item[aria-current="page"]::before`,
		`.tool-rail__tool[data-tool][aria-pressed="true"]::after`,
		`class="tool-rail__live"`,
		`.tool-rail__live`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("navigation still exposes decorative active/live ornament %q", forbidden)
		}
	}
	for _, marker := range []string{
		`.pd1-primary-nav__item[aria-current="page"] {`,
		`background: var(--surface-3);`,
		`border-color: var(--line-1);`,
		`color: var(--text-1);`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("quiet selected-state contract missing %q", marker)
		}
	}
}

func TestPD1KeyboardFocusResponsiveAndMotionContracts(t *testing.T) {
	html := pd1Index(t)
	for _, marker := range []string{
		`const horizontal = window.matchMedia('(max-width: 640px)').matches`,
		`const previousKey = horizontal ? 'ArrowLeft' : 'ArrowUp'`,
		`const nextKey = horizontal ? 'ArrowRight' : 'ArrowDown'`,
		`button.focus()`,
		`button.tabIndex = current ? 0 : -1`,
		`button.setAttribute('aria-current', current ? 'page' : 'false')`,
		`min-height: 46px`,
		`min-height: 44px`,
		`@media (max-width: 760px)`,
		`.pd1-primary-nav { flex-direction: row; width: auto; gap: 2px; }`,
		`setWorkToolMenuOpen(workToolMenu.hidden, { focusFirst: event.detail === 0 })`,
		`.pd1-primary-nav *, .pd1-destination-surface * { scroll-behavior: auto !important; transition: none !important; animation: none !important; }`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("PD1 interaction/polish contract missing %q", marker)
		}
	}
	css := pd1Slice(t, html, `/* PD1 shell: one global rail owns the product grammar.`, `@media (prefers-reduced-motion: reduce)`)
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
const path = require('path');
const assert = require('assert/strict');
const { chromium } = require('playwright');
const html = fs.readFileSync(process.env.PD1_INDEX, 'utf8');
const projectionRequests = [];
let shellAccess = 'full';
const server = http.createServer((req, res) => {
  if (req.url === '/public/composer-dictation.js') { res.writeHead(200, {'content-type':'application/javascript'}); return res.end(''); }
  if (req.url === '/auth/me') { res.writeHead(200, {'content-type':'application/json'}); return res.end(JSON.stringify({email:'synthetic@example.test',name:'Synthetic',shellAccess})); }
  if (req.url === '/api/stride/v1/mobile/surfaces/organizations') {
    res.writeHead(200, {'content-type':'application/json'});
    return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[
      {id:'membership-current',title:'Synthetic Lab',status:'current',kind:'organization-summary',detail:{kind:'organization-summary',activeCount:2,capacity:3,pendingCount:0,isCurrent:true,role:'owner'},actions:[]},
      {id:'membership-other',title:'Another Studio',status:'active',kind:'organization-summary',detail:{kind:'organization-summary',activeCount:2,capacity:3,pendingCount:0,isCurrent:false,role:'member'},actions:[]},
    ]}));
  }
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
  await page.waitForTimeout(250);
  const organizationProjectionReady = () => document.getElementById('topbarOrganizationName').textContent === 'Synthetic Lab'
    && document.querySelectorAll('#topbarOrganizationMenu [role="menuitemradio"]').length === 2;
  await page.waitForFunction(organizationProjectionReady);
  // Authentication fencing deliberately clears the prior organization before
  // replaying the current projection. Require the settled projection rather
  // than sampling the one-frame handoff between those two safe states.
  await page.waitForTimeout(100);
  await page.waitForFunction(organizationProjectionReady);
  const shellChrome = await page.evaluate(() => ({
    railWidth: document.getElementById('toolRail').getBoundingClientRect().width,
    navInsideRail: document.getElementById('toolRail').contains(document.getElementById('pd1PrimaryNav')),
    navInsideHeader: document.querySelector('.topbar')?.contains(document.getElementById('pd1PrimaryNav')),
    organizationText: document.getElementById('topbarOrganizationSwitcher').innerText.trim(),
    organizationChildCount: document.getElementById('topbarOrganizationSwitcher').children.length,
  }));
  assert.deepEqual(shellChrome, {railWidth:136,navInsideRail:true,navInsideHeader:false,organizationText:'Synthetic Lab',organizationChildCount:2});
  await page.click('#topbarOrganizationSwitcher');
  assert.equal(await page.locator('#topbarOrganizationMenu').evaluate(el => !el.hidden), true);
  assert.deepEqual(await page.locator('#topbarOrganizationMenu [role="menuitemradio"]').evaluateAll(items => items.map(item => ({name:item.innerText.trim(),current:item.getAttribute('aria-checked')}))), [{name:'Synthetic Lab',current:'true'},{name:'Another Studio',current:'false'}]);
  assert.equal(await page.locator('#topbarOrganizationCreate').innerText(), 'Create organization');
  await page.keyboard.press('Escape');
  assert.equal(await page.evaluate(() => document.activeElement?.id), 'topbarOrganizationSwitcher');
  await page.click('#topbarOrganizationSwitcher');
  await page.click('#topbarOrganizationMenu [role="menuitemradio"]:has-text("Another Studio")');
  await page.waitForFunction(() => document.getElementById('settingsRegion').classList.contains('visible') && !document.querySelector('.settings-body > section[data-settings-section="organizations"]').hidden);
  await page.evaluate(()=>closeSettings({restoreFocus:false}));
  await page.waitForFunction(() => !document.getElementById('settingsRegion').classList.contains('visible'));
  await page.evaluate(() => { const marker=document.createElement('span'); marker.id='pd1-thread-continuity'; document.getElementById('scoutChatThread').append(marker); });
  const shellRenderDir=String(process.env.PD1_SHELL_RENDER_DIR||'').trim();
  if(shellRenderDir)fs.mkdirSync(shellRenderDir,{recursive:true});
  await page.click('#pd1PrimaryNav [data-pd1-destination="Video"]');
  await page.waitForFunction(() => location.pathname === '/video' && document.getElementById('appShell').dataset.tool === 'room');
  if(shellRenderDir){await page.mouse.move(1279,799);await page.evaluate(()=>document.activeElement?.blur());await page.screenshot({path:path.join(shellRenderDir,'desktop-video-nav.png')});}
  await page.click('#pd1PrimaryNav [data-pd1-destination="Chat"]');
  await page.waitForFunction(() => location.pathname === '/chat' && document.getElementById('appShell').dataset.tool === 'chat');
  assert.equal(await page.locator('#pd1PrimaryNav [data-pd1-destination="Chat"]').getAttribute('aria-current'), 'page');
  await page.waitForTimeout(180);
  const chatNavState=await page.evaluate(()=>Object.fromEntries(['Video','Chat'].map(name=>{const node=document.querySelector('#pd1PrimaryNav [data-pd1-destination="'+name+'"]');const style=getComputedStyle(node);return[name,{current:node.getAttribute('aria-current'),background:style.backgroundColor,border:style.borderColor}]})));
  assert.equal(chatNavState.Video.current,'false');
  assert.equal(chatNavState.Chat.current,'page');
  assert.notEqual(chatNavState.Chat.border,chatNavState.Video.border);
  if(shellRenderDir){
    await page.mouse.move(1279,799);
    await page.evaluate(()=>document.activeElement?.blur());
    await page.screenshot({path:path.join(shellRenderDir,'desktop-video-chat-nav.png')});
    await page.setViewportSize({width:390,height:844});
    await page.screenshot({path:path.join(shellRenderDir,'phone-video-chat-nav.png')});
    await page.setViewportSize({width:1280,height:800});
  }
  projectionRequests.length = 0;
  await page.click('#pd1PrimaryNav [data-pd1-destination="Network"]');
  await page.waitForFunction(() => location.pathname === '/network' && document.activeElement?.textContent === 'The public network is off.');
  assert.equal(await page.locator('#toolRail').evaluate(el => !el.hidden && !el.inert && getComputedStyle(el).display !== 'none'), true);
  assert.equal(await page.locator('#workToolMenu').evaluate(el => el.hidden), true);
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
  await page.click('#pd1PrimaryNav [data-pd1-destination="Work"]');
  assert.equal(await page.locator('#workToolMenu').evaluate(el => !el.hidden), true);
  assert.deepEqual(await page.locator('#workToolMenu button[data-tool]').evaluateAll(buttons => buttons.filter(button => button.offsetParent !== null).map(button => button.getAttribute('aria-label'))), ['Work library','Projects','Memory','Files','Agent team']);
  assert.deepEqual(await page.locator('#workToolMenu button[data-tool]').evaluateAll(buttons => buttons.filter(button => button.offsetParent !== null).map(button => ({role:button.getAttribute('role'),tabIndex:button.tabIndex}))), Array(5).fill({role:'menuitemradio',tabIndex:-1}));
  assert.equal(await page.locator('#workToolMenu [data-tool="artifacts"]').getAttribute('aria-checked'), 'true');
  const utilities = await page.locator('.tool-rail__utilities').evaluate(el => ({
    labels:Array.from(el.querySelectorAll('.tool-rail__label')).filter(label => label.offsetParent !== null).map(label => label.textContent.trim()),
    width:el.getBoundingClientRect().width,
    rows:Array.from(el.querySelectorAll(':scope > .tool-rail__tool, :scope > .tool-rail__account')).filter(row => row.offsetParent !== null).map(row => row.getBoundingClientRect().width)
  }));
  assert.deepEqual(utilities.labels, ['Notifications','Appearance','Synthetic']);
  assert.ok(utilities.width >= 110); assert.ok(utilities.rows.every(width => width === utilities.width));
  await page.keyboard.press('Escape');
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('data-pd1-destination')), 'Work');
  await page.evaluate(() => { window.__pd1Close = window.closeStrideContributionSurface; window.closeStrideContributionSurface = () => false; });
  await page.click('#pd1PrimaryNav [data-pd1-destination="Network"]');
  assert.equal(new URL(page.url()).pathname, '/work');
  await page.evaluate(() => { window.closeStrideContributionSurface = window.__pd1Close; });
  await page.focus('#pd1PrimaryNav [data-pd1-destination="Work"]');
  await page.keyboard.press('ArrowDown');
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('data-pd1-destination')), 'Network');
  await page.keyboard.press('ArrowUp');
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('data-pd1-destination')), 'Work');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.activeElement?.getAttribute('aria-label') === 'Work library');
  assert.equal(await page.locator('#workToolMenu').evaluate(el => !el.hidden), true);
  await page.keyboard.press('Escape');
  await page.setViewportSize({width:320,height:700});
  const responsive = await page.locator('#pd1PrimaryNav').evaluate(el => ({direction:getComputedStyle(el).flexDirection, fits:document.documentElement.scrollWidth <= innerWidth, exact:el.scrollWidth === el.clientWidth,scrollWidth:el.scrollWidth,clientWidth:el.clientWidth,railWidth:el.parentElement.getBoundingClientRect().width}));
  assert.equal(responsive.direction, 'row'); assert.equal(responsive.fits, true, JSON.stringify(responsive)); assert.equal(responsive.exact, true, JSON.stringify(responsive));
  await page.focus('#pd1PrimaryNav [data-pd1-destination="Work"]');
  await page.keyboard.press('ArrowRight');
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('data-pd1-destination')), 'Network');
  await page.setViewportSize({width:1280,height:800});
  await page.click('#pd1PrimaryNav [data-pd1-destination="Chat"]');
  const stableMediaGeometry=await page.evaluate(async()=>{
    const figure=document.createElement('figure'); figure.className='scout-chat-image';
    const preview=document.createElement('button'); preview.className='scout-chat-image__preview';
    const image=document.createElement('img'); image.className='scout-chat-image__img'; image.alt='Country Golf concept render';
    preview.append(image); figure.append(preview); document.getElementById('scoutChatThread').append(figure);
    const before=figure.getBoundingClientRect().toJSON();
    const loaded=new Promise(resolve=>image.addEventListener('load',resolve,{once:true}));
    image.src='data:image/svg+xml,'+encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="1536" height="1024"><rect width="1536" height="1024" fill="#c7bca9"/></svg>');
    await loaded; await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
    const after=figure.getBoundingClientRect().toJSON();
    return {before,after};
  });
  assert.ok(stableMediaGeometry.before.width>0&&stableMediaGeometry.before.height>0,JSON.stringify(stableMediaGeometry));
  assert.ok(Math.abs(stableMediaGeometry.before.width-stableMediaGeometry.after.width)<1&&Math.abs(stableMediaGeometry.before.height-stableMediaGeometry.after.height)<1,'Country Golf media shifted the channel: '+JSON.stringify(stableMediaGeometry));
  if(shellRenderDir){
    const currentDestinations=await page.locator('#pd1PrimaryNav [aria-current="page"]').evaluateAll(buttons=>buttons.map(button=>button.dataset.pd1Destination));
    assert.deepEqual(currentDestinations,['Chat']);
    await page.mouse.move(1279,799);await page.evaluate(()=>document.activeElement?.blur());
    const quietNav=await page.evaluate(()=>Object.fromEntries(['Home','Network'].map(name=>{const style=getComputedStyle(document.querySelector('#pd1PrimaryNav [data-pd1-destination="'+name+'"]'));return[name,{background:style.backgroundColor,border:style.borderColor}]})));
    assert.deepEqual(quietNav.Network,quietNav.Home,JSON.stringify(quietNav));
    await page.screenshot({path:path.join(shellRenderDir,'desktop-country-golf-media-stable.png')});
  }
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
  // Begin the rendered ordinary-message acceptance from the clean Chat
  // surface. Earlier navigation/menu assertions intentionally exercise
  // overlays whose dimming must not contaminate the visual evidence.
  await page.goto(base + '/chat', {waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.waitForFunction(() => document.getElementById('appShell').dataset.tool==='chat' && getComputedStyle(document.getElementById('chatTool')).display!=='none');
  const shortMessageText = 'This is an ordinary message from the same person.';
  const shortMessage = {id:'message-short-prose',kind:'message',role:'user',authorName:'Synthetic',authorEmail:'synthetic@example.test',text:shortMessageText,createdAt:'2026-08-11T23:44:00Z'};
  const longMessageText = Array.from({length:14}, (_, index) => 'This is ordinary team-chat prose line ' + (index + 1) + ', with enough detail to make the rendered message taller without turning it into a document.').join(' ');
  const longMessage = {id:'message-long-prose',kind:'message',role:'user',authorName:'Synthetic',authorEmail:'synthetic@example.test',text:longMessageText,createdAt:'2026-08-11T23:45:00Z'};
  await page.evaluate(({shortMessage, longMessage}) => {
    scoutChatThreads=[{id:'channel-long-prose',title:'Bonfire Chat',visibility:'public',messages:[shortMessage,longMessage]}];
    activeScoutThreadId='channel-long-prose';
    const messageNode = message => {
      const node=scoutChatMessageNode('user',message.text,message.createdAt,[],message.authorName,false);
      node.dataset.messageId=message.id;
      decorateDesktopChatMessage(node,message,'user',message.authorName);
      return node;
    };
    document.getElementById('scoutChatThread').replaceChildren(messageNode(shortMessage),messageNode(longMessage));
  }, {shortMessage,longMessage});
  await page.waitForFunction(() => document.querySelector('[data-message-id="message-long-prose"] .scout-chat-msg__expand')?.textContent === 'Show more');
  assert.equal(await page.locator('[data-message-id="message-short-prose"] .scout-chat-msg__expand').count(),0);
  const ordinaryLongMessage = await page.locator('[data-message-id="message-long-prose"]').evaluate(node => ({
    classes:Array.from(node.classList),
    text:node.innerText,
    expanded:node.querySelector('.scout-chat-msg__expand')?.getAttribute('aria-expanded'),
    controls:node.querySelector('.scout-chat-msg__expand')?.getAttribute('aria-controls'),
    bodyId:node.querySelector('.scout-chat-text')?.id,
  }));
  assert.ok(ordinaryLongMessage.classes.includes('scout-chat-msg--longform'));
  assert.ok(!ordinaryLongMessage.text.toLowerCase().includes('letter'));
  assert.equal(ordinaryLongMessage.expanded,'false');
  assert.equal(ordinaryLongMessage.controls,ordinaryLongMessage.bodyId);
  const ordinaryMessageParity = await page.locator('[data-message-id="message-long-prose"]').evaluate(longNode => {
    const shortNode=document.querySelector('[data-message-id="message-short-prose"]');
    const style = node => {
      const item=getComputedStyle(node);
      const body=getComputedStyle(node.querySelector('.scout-chat-text'));
      const rect=node.querySelector('.scout-chat-text').getBoundingClientRect();
      return {
        itemMaxWidth:item.maxWidth,
        itemAlignSelf:item.alignSelf,
        backgroundColor:body.backgroundColor,
        color:body.color,
        border:body.border,
        borderRadius:body.borderRadius,
        fontFamily:body.fontFamily,
        fontSize:body.fontSize,
        fontWeight:body.fontWeight,
        lineHeight:body.lineHeight,
        padding:body.padding,
        right:rect.right,
      };
    };
    return {short:style(shortNode),long:style(longNode)};
  });
  assert.deepEqual({...ordinaryMessageParity.long,right:ordinaryMessageParity.short.right},ordinaryMessageParity.short);
  assert.ok(Math.abs(ordinaryMessageParity.short.right-ordinaryMessageParity.long.right)<1);
  const expandAffordance=await page.locator('[data-message-id="message-long-prose"] .scout-chat-msg__expand').evaluate(button => {
    const style=getComputedStyle(button);
    const after=getComputedStyle(button,'::after');
    const rect=button.getBoundingClientRect();
    const top=document.elementFromPoint(rect.left+rect.width/2,rect.top+rect.height/2);
    return {display:style.display,color:style.color,background:style.backgroundColor,border:style.borderTopWidth,after:after.content,height:rect.height,width:rect.width,visible:rect.bottom<=innerHeight && rect.right<=innerWidth,topId:top?.id||'',topClass:top?.className||'',topTag:top?.tagName||''};
  });
  assert.equal(expandAffordance.display,'flex');
  assert.equal(expandAffordance.border,'1px');
  assert.notEqual(expandAffordance.background,'rgba(0, 0, 0, 0)');
  assert.ok(expandAffordance.after.includes('↓') || expandAffordance.after.includes('2193'));
  assert.ok(expandAffordance.height+0.01>=40 && expandAffordance.width+0.01>=40 && expandAffordance.visible,JSON.stringify(expandAffordance));
  const renderDir=String(process.env.PD1_RENDER_DIR||'').trim();
  if(renderDir){
    fs.mkdirSync(renderDir,{recursive:true});
    for (const candidate of [
      {name:'compact',width:1024,height:768},
      {name:'standard',width:1280,height:800},
      {name:'wide',width:1728,height:1000},
    ]) {
      await page.setViewportSize({width:candidate.width,height:candidate.height});
      for (const theme of ['dark','light']) {
        await page.evaluate(nextTheme => renderTheme(nextTheme),theme);
        await page.mouse.move(2,2);
        await page.waitForTimeout(180);
        const contrast=await page.locator('[data-message-id="message-long-prose"] .scout-chat-msg__expand').evaluate(button => {
          const style=getComputedStyle(button);
          const rgb=value => (value.match(/[\d.]+/g)||[]).slice(0,3).map(Number);
          const luminance=value => {
            const channels=rgb(value).map(channel => {
              const normalized=channel/255;
              return normalized<=0.04045?normalized/12.92:Math.pow((normalized+0.055)/1.055,2.4);
            });
            return 0.2126*channels[0]+0.7152*channels[1]+0.0722*channels[2];
          };
          const foreground=luminance(style.color);
          const background=luminance(style.backgroundColor);
          return {ratio:(Math.max(foreground,background)+0.05)/(Math.min(foreground,background)+0.05),color:style.color,background:style.backgroundColor};
        });
        assert.ok(contrast.ratio>=4.5,theme+' '+candidate.name+' expand contrast '+JSON.stringify(contrast));
        await page.screenshot({path:path.join(renderDir,'desktop-ordinary-messages-'+candidate.name+'-'+theme+'.png')});
      }
    }
    await page.setViewportSize({width:1280,height:800});
    await page.evaluate(() => renderTheme('dark'));
  }
  await page.locator('[data-message-id="message-long-prose"] .scout-chat-msg__expand').click();
  assert.deepEqual(await page.locator('[data-message-id="message-long-prose"] .scout-chat-msg__expand').evaluate(button => ({text:button.textContent,expanded:button.getAttribute('aria-expanded')})),{text:'Show less',expanded:'true'});
  await page.locator('[data-message-id="message-long-prose"] .scout-chat-msg__expand').click();
  assert.deepEqual(await page.locator('[data-message-id="message-long-prose"] .scout-chat-msg__expand').evaluate(button => ({text:button.textContent,expanded:button.getAttribute('aria-expanded')})),{text:'Show more',expanded:'false'});
  await page.evaluate(root => {
    const thread=scoutChatThreads[0];
    thread.messages=[root,...Array.from({length:18},(_,index)=>({
      id:'message-long-reply-'+(index+1),kind:'message',role:index%2===0?'user':'scout',
      authorName:index%2===0?'Synthetic':'Scout',authorEmail:index%2===0?'synthetic@example.test':'',
      text:'Thread reply '+(index+1)+' keeps the discussion connected to the original parent while the reply history grows.',
      createdAt:'2026-08-11T23:' + String(46+Math.floor(index/2)).padStart(2,'0') + ':00Z',
      replyTo:{messageId:root.id,authorName:root.authorName,text:root.text.slice(0,120)},
    }))];
  },longMessage);
  const replyTrigger=page.locator('[data-message-id="message-long-prose"] .desktop-chat-actions button').filter({hasText:'reply'});
  await replyTrigger.evaluate(button => { button.dataset.testThreadTrigger='true'; });
  await replyTrigger.click();
  await page.waitForFunction(() => !document.getElementById('chatContextRail').hidden);
  assert.equal(await page.locator('#chatContextKicker').innerText(),'thread');
  assert.ok((await page.locator('#chatContextParent').innerText()).includes('ordinary team-chat prose line 14'));
  assert.ok((await page.locator('#chatContextBody').innerText()).includes('Thread reply 18'));
  const pinnedTop=await page.locator('#chatContextParent').evaluate(node => node.getBoundingClientRect().top);
  await page.locator('#chatContextBody').evaluate(node => { node.scrollTop=node.scrollHeight; });
  assert.equal(await page.locator('#chatContextParent').evaluate(node => node.getBoundingClientRect().top),pinnedTop);
  assert.ok((await page.locator('#chatContextBody').evaluate(node => node.scrollTop))>0);
  assert.equal(await page.locator('#chatContextReplyInput').getAttribute('placeholder'),'Message the thread…');
  assert.equal(await page.locator('#chatContextReplyInput').isVisible(),true);
  assert.equal(await page.locator('#scoutChatForm').isVisible(),false);
  assert.equal(await page.locator('#scoutChatBrainNote').count(),0);
  if(renderDir){
    for (const candidate of [
      {name:'compact',width:1024,height:768,threadsVisible:false,conversationVisible:false},
      {name:'standard',width:1280,height:800,threadsVisible:false,conversationVisible:true},
      {name:'wide',width:1728,height:1000,threadsVisible:true,conversationVisible:true},
    ]) {
      await page.setViewportSize({width:candidate.width,height:candidate.height});
      const layout=await page.evaluate(() => {
        const reply=document.getElementById('chatContextReplyForm').getBoundingClientRect();
        const rail=document.getElementById('chatContextRail').getBoundingClientRect();
        return {
          documentFits:document.documentElement.scrollWidth<=innerWidth,
          chatFits:document.getElementById('chatTool').scrollWidth<=document.getElementById('chatTool').clientWidth,
          threadsVisible:getComputedStyle(document.querySelector('#chatTool .chat-threads')).display!=='none',
          conversationVisible:getComputedStyle(document.querySelector('#chatTool .chat-conversation')).display!=='none',
          mainComposerVisible:getComputedStyle(document.getElementById('scoutChatForm')).display!=='none',
          replyVisible:reply.width>=320 && reply.bottom<=innerHeight && reply.left>=0 && reply.right<=innerWidth,
          railVisible:rail.width>=360 && rail.right<=innerWidth,
        };
      });
      assert.deepEqual(layout,{documentFits:true,chatFits:true,threadsVisible:candidate.threadsVisible,conversationVisible:candidate.conversationVisible,mainComposerVisible:false,replyVisible:true,railVisible:true});
      for (const theme of ['dark','light']) {
        await page.evaluate(nextTheme => renderTheme(nextTheme),theme);
        await page.mouse.move(2,2);
        await page.waitForTimeout(180);
        await page.screenshot({path:path.join(renderDir,'desktop-long-message-thread-'+candidate.name+'-'+theme+'.png')});
      }
    }
    await page.setViewportSize({width:1280,height:800});
    await page.evaluate(() => renderTheme('dark'));
  }
  await page.keyboard.press('Escape');
  assert.equal(await page.locator('#chatContextRail').evaluate(node => node.hidden),true);
  assert.equal(await page.evaluate(() => document.activeElement?.dataset?.testThreadTrigger),'true');
  const activeRowPreview = await page.evaluate(() => {
    artifactEntries=[{id:'artifact-follow-up',status:'running',metadata:{threadStatus:'running',status:'running',threadId:'run-follow-up',originKind:'channel',originId:'channel-follow-up'}}];
    const thread={id:'channel-follow-up',title:'Bonfire Chat',visibility:'public',preview:'Research delivered · 12 cited source links · 10 domains',updatedAt:'2026-08-10T00:01:00Z',messages:[{id:'follow-up-work',kind:'thread',text:'Research delivered · 12 cited source links · 10 domains',thread:{id:'run-follow-up',artifactId:'artifact-follow-up',status:'running',startedAt:'2026-08-10T00:00:00Z'}}]};
    const row=chatThreadRowNode(thread,'');
    return {preview:row.querySelector('.chat-thread-item__preview')?.textContent,timer:Boolean(row.querySelector('.chat-thread-item__work-timer'))};
  });
  assert.deepEqual(activeRowPreview,{preview:'Scout is working',timer:true});
  shellAccess='core';
  const memberPage=await browser.newPage({viewport:{width:390,height:844}});
  await memberPage.goto(base+'/work',{waitUntil:'domcontentloaded'});
  await memberPage.waitForSelector('#appShell.is-authed');
  await memberPage.waitForFunction(()=>location.pathname==='/'&&document.getElementById('appShell').dataset.pd1Destination==='Home');
  const memberShell=await memberPage.locator('#pd1PrimaryNav [data-pd1-destination]').evaluateAll(buttons=>buttons.map(button=>({name:button.dataset.pd1Destination,visible:!button.hidden&&!button.disabled&&getComputedStyle(button).display!=='none',display:getComputedStyle(button).display})));
  assert.deepEqual(memberShell.filter(item=>item.visible).map(item=>item.name),['Home','Video','Chat']);
  assert.ok(memberShell.filter(item=>!item.visible).every(item=>item.display==='none'),JSON.stringify(memberShell));
  if(shellRenderDir)await memberPage.screenshot({path:path.join(shellRenderDir,'phone-member-core-nav.png')});
  await memberPage.close();
  await browser.close(); server.close();
})().catch(error => { console.error(error); server.close(); process.exit(1); });`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "PD1_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered browser harness: %v\n%s", err, output)
	}
}
