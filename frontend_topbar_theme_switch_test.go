package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// AJ 2026-09-03: "let's put a light/dark mode icon to the left of the
// notification bells too, users like the quick switch ability".
func TestTopbarThemeSwitchSitsOnTheBellAxisAndCarriesNoPressedState(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		// same box as the bell, so the two glyphs share one height
		`<button id="topbarThemeToggle" class="topbar__notify topbar__theme" type="button"`,
		`.topbar__theme .tool-rail__icon--sun {`,
		`[data-theme="dark"] .topbar__theme .tool-rail__icon--sun {`,
		// phones hide it with the bell; theme lives in the settings sheet there
		`        .topbar__theme,`,
		`topbarThemeToggleButton?.addEventListener('click', () => syncThemeToggles(toggleTheme()))`,
		// every theme path lands in syncThemeToggles, so the label cannot drift
		`const label = theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("topbar theme switch contract missing %q", want)
		}
	}
	// The switch names its DESTINATION, so aria-pressed would contradict the
	// label ("pressed, switch to light"). Pin its absence on this button only.
	start := strings.Index(html, `<button id="topbarThemeToggle"`)
	if start < 0 {
		t.Fatal("topbar theme switch button not found")
	}
	if open := html[start : start+strings.Index(html[start:], ">")]; strings.Contains(open, "aria-pressed") {
		t.Fatalf("topbar theme switch must not claim aria-pressed: %s", open)
	}
	// It must precede the bell in source order as well as on screen.
	bell := strings.Index(html, `<button id="notificationBell"`)
	if bell < 0 || bell < start {
		t.Fatalf("theme switch must be authored left of the bell (switch=%d bell=%d)", start, bell)
	}
}

func TestTopbarThemeSwitchRendersUniformWithTheBellAndFlipsTheTheme(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');const http=require('http');const path=require('path');const assert=require('assert/strict');const {chromium}=require('playwright');
const idx=process.env.THEME_SWITCH_INDEX;const html=fs.readFileSync(idx,'utf8');
const dictation=fs.readFileSync(path.join(path.dirname(idx),'public','composer-dictation.js'),'utf8');
const server=http.createServer((req,res)=>{
 if(require('./scripts/frontend-design-assets.cjs')(req,res))return;
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation)}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full',organization:'Bonfire'}))}
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')||req.url.startsWith('/auth/theme')){res.writeHead(200,{'content-type':'application/json'});return res.end('{}')}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html)
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));
const b=await chromium.launch({headless:true});const page=await b.newPage({viewport:{width:1280,height:800}});
await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
await page.waitForSelector('#appShell.is-authed');
// uniformity is the point AJ raised about the old header: same height, same axis
const geom=await page.evaluate(()=>{const t=document.getElementById('topbarThemeToggle'),n=document.getElementById('notificationBell');
 const tr=t.getBoundingClientRect(),nr=n.getBoundingClientRect();
 return {leftOfBell:tr.right<=nr.left+1,dh:Math.abs(tr.height-nr.height),dy:Math.abs((tr.top+tr.height/2)-(nr.top+nr.height/2)),h:tr.height,theme:document.documentElement.dataset.theme==='dark'?'dark':'light'}});
assert.ok(geom.leftOfBell,'theme switch is not left of the bell: '+JSON.stringify(geom));
assert.ok(geom.dh<0.5&&geom.dy<0.5,'theme switch and bell are not uniform: '+JSON.stringify(geom));
// exactly one glyph is visible per theme, and it is the destination
const before=geom.theme;
const shown=async()=>page.evaluate(()=>({sun:getComputedStyle(document.querySelector('#topbarThemeToggle .tool-rail__icon--sun')).opacity,
 moon:getComputedStyle(document.querySelector('#topbarThemeToggle .tool-rail__icon--moon')).opacity,
 // light is the ABSENCE of data-theme (renderTheme deletes it), so resolve
 // the same way the app does rather than reading the raw attribute
 theme:document.documentElement.dataset.theme==='dark'?'dark':'light',label:document.getElementById('topbarThemeToggle').getAttribute('aria-label')}));
const dark0=await shown();
assert.equal(dark0.theme,'dark','dark is the product default');
assert.equal(dark0.sun,'1');assert.equal(dark0.moon,'0');
assert.ok(/light/.test(dark0.label),'dark theme must offer light: '+JSON.stringify(dark0));
await page.click('#topbarThemeToggle');await page.waitForTimeout(160);
const light=await shown();
assert.equal(light.theme,'light','click did not switch the theme');
assert.equal(light.sun,'0');assert.equal(light.moon,'1');
assert.ok(/dark/.test(light.label),'light theme must offer dark: '+JSON.stringify(light));
// the switch writes an EXPLICIT preference, which is what overrides a stored "system"
const stored=await page.evaluate(()=>window.localStorage.getItem('bonfire.theme.v1'));
assert.equal(stored,'light','the quick switch must persist an explicit theme, got '+stored);
await page.click('#topbarThemeToggle');await page.waitForTimeout(160);
assert.equal((await shown()).theme,before,'second click did not return');
// phones keep theme in the settings sheet; the switch hides with the bell
await page.setViewportSize({width:390,height:844});await page.waitForTimeout(160);
const phone=await page.evaluate(()=>({t:getComputedStyle(document.getElementById('topbarThemeToggle')).display,n:getComputedStyle(document.getElementById('notificationBell')).display}));
assert.equal(phone.t,'none','theme switch must hide on phones with the bell: '+JSON.stringify(phone));
assert.equal(phone.n,'none','fixture drifted: the bell should also be dock-hidden here');
await b.close();server.close()})().catch(e=>{console.error(e);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "THEME_SWITCH_INDEX="+indexPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered topbar theme switch failed: %v\n%s", err, output)
	}
}
