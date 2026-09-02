package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Wave 2 of STRIDE v2.0 — design system canon. These pins hold the token
// role aliases (D1), the ember doctrine (D1), the three liquid-glass tiers and
// the surfaces migrated onto them (D2), the one menu component and its
// migrated sites (D4), the theme-parity contrast walk (D5), and the unified
// focus ring / selection / scrollbars / toasts (D6).

func readDesignSystemIndex(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// designSystemCSSBlock returns the first rule that starts with selector, up to its
// closing brace (declaration blocks only — no nested rules).
func designSystemCSSBlock(html, selector string) string {
	start := strings.Index(html, selector)
	if start < 0 {
		return ""
	}
	rest := html[start:]
	end := strings.Index(rest, "}")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func TestDesignSystemV2TokenRoleAliases(t *testing.T) {
	html := readDesignSystemIndex(t)
	lightStart := strings.Index(html, ":root {")
	darkStart := strings.Index(html, `[data-theme="dark"] {`)
	if lightStart < 0 || darkStart < 0 || darkStart < lightStart {
		t.Fatal("token blocks missing or out of order")
	}
	light := html[lightStart:darkStart]
	dark := html[darkStart : darkStart+12000]

	// D1 — the role vocabulary, aliased onto the ratified values (never re-tuned)
	for _, want := range []string{
		"--ground: var(--bg-app);",
		"--surface-0: var(--bg-app);",
		"--well: var(--surface-3);",
		"--ink-1: var(--text-1);",
		"--ink-2: var(--text-2);",
		"--ink-3: var(--text-3);",
		"--ink-4: var(--text-disabled);",
		"--line: var(--line-1);",
		"--line-strong: var(--line-2);",
		"--shadow-float: 0 16px 38px rgba(38, 35, 30, 0.18);",
		"--selection: rgba(38, 35, 30, 0.20);",
	} {
		if !strings.Contains(light, want) {
			t.Errorf("light token block missing role alias %q", want)
		}
	}
	for _, want := range []string{
		"--shadow-float: 0 16px 38px rgba(0, 0, 0, 0.50);",
		"--selection: rgba(245, 245, 247, 0.24);",
	} {
		if !strings.Contains(dark, want) {
			t.Errorf("dark token block missing role alias %q", want)
		}
	}

	// The ratified values the aliases point at must not drift (AJ ratified:
	// putty light ramp, true-black dark ramp, one orange, graphite rail mark).
	for _, want := range []string{
		"--paper-50: #DDD4C6;",
		"--paper-100: #CFC5B7;",
		"--ember-500: #FF5A19;",
		"--text-1: #26231E;",
		"--ring: rgba(38, 35, 30, 0.60);",
		"--ember-text: #86290F;",
	} {
		if !strings.Contains(light, want) {
			t.Errorf("ratified light value drifted: missing %q", want)
		}
	}
	for _, want := range []string{
		// AJ ratified dark ladder v2 2026-09-02 (was #050506)
		"--surface-1: #0E0E10;",
		"--text-1: #F5F5F7;",
		"--ring: rgba(245, 245, 247, 0.50);",
		"--ember-text: var(--ember-500);",
	} {
		if !strings.Contains(dark, want) {
			t.Errorf("ratified dark value drifted: missing %q", want)
		}
	}
}

func TestDesignSystemV2EmberDoctrine(t *testing.T) {
	html := readDesignSystemIndex(t)
	start := strings.Index(html, "EMBER DOCTRINE")
	if start < 0 {
		t.Fatal("the ember doctrine comment block is missing from the token block")
	}
	doctrine := html[start : start+2400]
	for _, want := range []string{
		"One orange: #FF5A19",
		"Ember is EARNED, never ambient",
		"SANCTIONED EXCEPTIONS",
		"ACTIVE RAIL TAB",
		"LIVE / SPEAKING",
		"SEARCH-HIT WELL",
		"graphite",
		"--ember: var(--ember-500);",
	} {
		if !strings.Contains(doctrine, want) {
			t.Errorf("ember doctrine block missing %q", want)
		}
	}
	// the doctrine sits NEXT to the token it governs
	if strings.Index(doctrine, "--ember: var(--ember-500);") > 2200 {
		t.Error("the ember doctrine must be written directly above --ember")
	}
	// empty states are screens at rest: no ember on the studio empty mark
	if strings.Contains(designSystemCSSBlock(html, ".studio-project-empty__mark {"), "--ember") {
		t.Error(".studio-project-empty__mark decorates with ember; empty-state art sits on the well in ink")
	}
}

func TestDesignSystemV2GlassTiers(t *testing.T) {
	html := readDesignSystemIndex(t)
	for _, want := range []string{
		"--glass-chrome-fill: color-mix(in oklab, var(--surface-1) 88%, transparent);",
		"--glass-float-fill: color-mix(in oklab, var(--surface-1) 94%, transparent);",
		"--glass-sheet-fill: color-mix(in oklab, var(--surface-1) 96%, transparent);",
		"--glass-opaque-fill: color-mix(in oklab, var(--surface-1) 98%, transparent);",
		"--glass-chrome-filter: var(--glass-blur-chrome) saturate(1.25);",
		"--glass-float-filter: var(--glass-blur-chrome) saturate(1.25);",
		"--glass-sheet-filter: var(--glass-blur) saturate(1.25);",
		"--glass-float-shadow: var(--glass-highlight), var(--shadow-float);",
		"--glass-sheet-shadow: var(--glass-highlight), var(--shadow-3);",
	} {
		if strings.Count(html, want) < 1 {
			t.Errorf("glass tier token missing: %q", want)
		}
	}
	// the dark block carries the tier variants explicitly: same mixes on the
	// black ramp, and the lift drops the specular edge
	darkStart := strings.Index(html, `[data-theme="dark"] {`)
	if darkStart < 0 {
		t.Fatal("dark token block missing")
	}
	dark := html[darkStart : darkStart+12000]
	for _, want := range []string{
		"liquid-glass tiers · DARK",
		"--glass-float-shadow: var(--shadow-float);",
		"--glass-sheet-shadow: var(--shadow-3);",
	} {
		if !strings.Contains(dark, want) {
			t.Errorf("dark token block must carry the glass tier variant %q", want)
		}
	}

	chrome := designSystemCSSBlock(html, "\n      .glass-chrome,\n") // line-start selector: the plan-020 theme-crossfade `:is(.glass-chrome, …)` rule precedes the tier block
	for _, want := range []string{
		"background: var(--glass-chrome-fill);",
		"backdrop-filter: var(--glass-chrome-filter);",
		"-webkit-backdrop-filter: var(--glass-chrome-filter);",
		".topbar,",
		"#chatTool .chat-convo-head,",
		".drive-detail__head,",
		".artifact-stage__head,",
		".content-studio-drawer__head,",
		".board-dock {",
	} {
		if !strings.Contains(chrome, want) {
			t.Errorf(".glass-chrome tier missing %q", want)
		}
	}
	float := designSystemCSSBlock(html, "\n      .glass-float,\n")
	for _, want := range []string{
		"background: var(--glass-float-fill);",
		"backdrop-filter: var(--glass-float-filter);",
		"border: 1px solid var(--line);",
		"box-shadow: var(--glass-float-shadow);",
		".bf-menu,",
		".topbar__organization-menu,",
		".account-menu,",
		"#chatTool .desktop-chat-more__menu,",
		"#chatTool .desktop-chat-reaction-picker__menu,",
		".files-folder-menu,",
		".greenroom__filters,",
		".manifest-card__more-menu,",
		".package-row__more-menu,",
		".chat-attachment-source-menu,",
		".deck-editor__popover,",
		".chat-channel-activity-popover,",
		".board-menu,",
		".goalcard__menu-pop,",
		".notification-panel,",
		".toast,",
	} {
		if !strings.Contains(float, want) {
			t.Errorf(".glass-float tier missing %q", want)
		}
	}
	sheet := designSystemCSSBlock(html, "\n      .glass-sheet,\n")
	for _, want := range []string{
		"background: var(--glass-sheet-fill);",
		"backdrop-filter: var(--glass-sheet-filter);",
		"border: 1px solid var(--line);",
		".settings-panel,",
		".consent-panel,",
		".login-card,",
		".room-transfer__panel,",
		".os-assistant__panel,",
		".pip-meeting,",
		".board-rail,",
		".board-surface,",
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf(".glass-sheet tier missing %q", want)
		}
	}
	// no-backdrop-filter fallback raises every tier to the 98% fill
	fallback := strings.Index(html, "@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px)))")
	if fallback < 0 || !strings.Contains(html[fallback:fallback+2200], "background: var(--glass-opaque-fill);") {
		t.Error("glass tiers need the @supports not (backdrop-filter) fallback onto --glass-opaque-fill")
	}
	// migrated surfaces no longer carry their own ad-hoc glass recipe
	for _, selector := range []string{
		".topbar__organization-menu {",
		".account-menu {",
		"#chatTool .desktop-chat-more__menu {",
		".files-folder-menu {",
		".greenroom__filters {",
		".manifest-card__more-menu {",
		".package-row__more-menu {",
		".deck-editor__popover {",
		".toast {",
		".notification-panel {",
		".consent-panel {",
		".login-card {",
		".os-assistant__panel {",
		".board-rail {",
	} {
		block := designSystemCSSBlock(html, selector)
		if block == "" {
			t.Errorf("missing rule %q", selector)
			continue
		}
		if strings.Contains(block, "backdrop-filter") || strings.Contains(block, "background: var(--glass-chrome)") || strings.Contains(block, "background: var(--glass-panel)") {
			t.Errorf("%s still carries an ad-hoc glass recipe; it is on a tier now", selector)
		}
	}
}

// designSystemFunctionBody brace-matches from the first "{" AFTER the
// signature (the signature itself carries "options = {}").
func designSystemFunctionBody(source, signature string) string {
	start := strings.Index(source, signature)
	if start < 0 {
		return ""
	}
	from := start + len(signature)
	open := strings.Index(source[from:], "{")
	if open < 0 {
		return ""
	}
	depth := 0
	for index := from + open; index < len(source); index++ {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[from+open : index+1]
			}
		}
	}
	return ""
}

func TestDesignSystemV2MenuComponent(t *testing.T) {
	html := readDesignSystemIndex(t)
	body := designSystemFunctionBody(html, "function bfMenu(trigger, options = {})")
	if body == "" {
		t.Fatal("bfMenu(trigger, options = {}) is missing")
	}
	for _, want := range []string{
		// roles
		"menu.setAttribute('role', options.role || 'menu')",
		"item.setAttribute('role', isRadio ? 'menuitemradio' : 'menuitem')",
		"item.setAttribute('aria-checked', spec?.checked ? 'true' : 'false')",
		// keyboard: arrows wrap, Home/End, type-ahead by first letter, Tab closes
		"const nextKey = horizontal ? 'ArrowRight' : 'ArrowDown'",
		"(current + 1) % list.length",
		"(current - 1 + list.length) % list.length",
		"else if (event.key === 'Home') next = 0",
		"else if (event.key === 'End') next = list.length - 1",
		"bfMenuItemText(item).startsWith(letter)",
		"if (event.key === 'Tab') { close(); return }",
		// dismissal: Escape returns focus, outside pointerdown closes
		"if (event.key !== 'Escape') return",
		"close({ restoreFocus: true })",
		"document.addEventListener('pointerdown', onOutsidePointerDown, true)",
		"document.addEventListener('keydown', onDocumentKeydown, true)",
		"if (restoreFocus) trigger.focus({ preventScroll: true })",
		// one at a time, aria on the trigger, origin from the trigger's rect
		"bfMenuCloseAll(controller)",
		"trigger.setAttribute('aria-expanded', 'true')",
		"menu.style.transformOrigin = `${Math.round(x)}px ${Math.round(y)}px`",
		// adopt mode keeps ids/classes; static mode for the studios
		"menu.dataset.bfMenu = 'adopted'",
		"if (options.animate === false) menu.setAttribute('data-bf-menu-static', '')",
		// must-run focus is a macrotask, never rAF
		"window.setTimeout(() => {\n              if (menu.hidden) return",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("bfMenu missing %q", want)
		}
	}
	if strings.Contains(body, "requestAnimationFrame") {
		t.Error("bfMenu must not use requestAnimationFrame for must-run work")
	}

	// the built menu's CSS: float tier, spring scale-in from the origin,
	// discrete display so it can fade out, reduced motion drops the motion
	menuCSS := designSystemCSSBlock(html, "      .bf-menu {\n        transition-property")
	for _, want := range []string{
		"transition-property: opacity, transform, display;",
		"var(--ease-spring)",
		"transition-behavior: allow-discrete;",
	} {
		if !strings.Contains(menuCSS, want) {
			t.Errorf(".bf-menu motion rule missing %q", want)
		}
	}
	for _, want := range []string{
		".bf-menu:not([hidden]),\n        [data-bf-menu]:not([data-bf-menu-static]):not([hidden]) {\n          opacity: 0;\n          transform: translateY(-4px) scale(0.92);",
		"[data-bf-menu-static] { transition: none; }",
		".bf-menu,\n        [data-bf-menu],\n        [data-bf-menu]:not([data-bf-menu-static]) { transition: none; }",
		".bf-menu__item[role=\"menuitemradio\"][aria-checked=\"true\"]::before",
		".bf-menu__item:focus-visible { background: var(--well); }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("bfMenu CSS missing %q", want)
		}
	}

	// migrated sites — every menu in the inventory goes through bfMenu; the
	// ids and pinned markup stay (adopt mode)
	for _, site := range []struct{ name, want string }{
		{"org menu", "bfMenu(switcher, { menu, radio: true, origin: 'top-left'"},
		{"header popovers (members + bell)", "const shared = bfMenu(trigger, {\n\t          menu: popover,\n\t          radio: isMenu,\n\t          keyboard: isMenu,"},
		{"chat more-menu", "const control = bfMenu(trigger, { menu, origin: 'top-right' })"},
		{"reaction picker", "bfMenu(trigger, { menu, orientation: 'horizontal', itemSelector: 'button'"},
		{"greenroom filters", "bfMenu(greenRoomFiltersBtnEl, { menu: greenRoomFiltersEl"},
		{"account menu", "bfMenu(accountMenuButton, { menu: accountMenu"},
		{"room more menu", "bfMenu(roomMoreToggleButton, { menu: roomMoreMenu"},
		{"deck studio menus", "bfMenu(trigger, { menu, animate: false, origin: 'top-right', bindTrigger: false })"},
		{"document studio menu", "bfMenu(documentMoreButton, { menu: documentMoreMenu, animate: false"},
		{"chat-deck download", "bfMenu(downloadBtn, { menu: downloadMenu"},
		{"attachment source (built)", "className: 'chat-attachment-source-menu'"},
	} {
		if !strings.Contains(html, site.want) {
			t.Errorf("%s is not on bfMenu: missing %q", site.name, site.want)
		}
	}
	if n := strings.Count(html, "bfMenuAdoptOpen(kebab, menu, {"); n != 2 {
		t.Errorf("both Drive kebab menus (folder chip + row) must adopt bfMenu, found %d", n)
	}
	if n := strings.Count(html, "bfMenu(moreTrigger, { menu: moreMenu"); n != 3 {
		t.Errorf("hero, manifest-card and package-row more-menus must adopt bfMenu, found %d", n)
	}
	// the studios keep a still first frame: their menus open static
	if strings.Count(html, "animate: false") < 2 {
		t.Error("deck and document studio menus must pass animate:false")
	}
}

func TestDesignSystemV2FocusSelectionScrollbars(t *testing.T) {
	html := readDesignSystemIndex(t)
	focus := designSystemCSSBlock(html, "      :focus-visible {")
	for _, want := range []string{"outline: 2px solid var(--ring);", "outline-offset: 2px;"} {
		if !strings.Contains(focus, want) {
			t.Errorf("global :focus-visible ring must be 2px of --ring offset 2px; missing %q", want)
		}
	}
	if strings.Contains(focus, "var(--glow-accent)") {
		t.Error("the focus ring halo must derive from --ring, not the accent glow")
	}
	for _, want := range []string{
		"* { scrollbar-width: thin; scrollbar-color: var(--line) transparent; }",
		"*::-webkit-scrollbar-thumb { background: var(--line); border-radius: 999px; }",
		"*::-webkit-scrollbar-thumb:hover { background: var(--line-strong); }",
		"::selection { background: var(--selection); }",
		"animation: bf-popin var(--dur-med) var(--ease);", // plan 012: one recipe per surface
	} {
		if !strings.Contains(html, want) {
			t.Errorf("D6 unification missing %q", want)
		}
	}
	// toasts keep their reduced-motion cover
	reduced := strings.Index(html, ".toast { animation: none; }")
	if reduced < 0 {
		t.Error("toasts must drop their pop-in under reduced motion")
	}
}

// The canvas-resolved contrast walk: every text step on every surface (and
// every glass tier composited over the ground) clears WCAG AA in both themes.
// color-mix() computes to a color() / oklab() string; a canvas fillStyle
// round-trip resolves it and alpha-composites it onto what is beneath, which
// is what the eye sees. Screenshots are never used — they lag the DOM.
func TestDesignSystemV2RenderedContrastWalk(t *testing.T) {
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
const {chromium}=require('playwright');
const html=fs.readFileSync(process.env.DESIGN_SYSTEM_INDEX,'utf8');
const server=http.createServer((req,res)=>{
  if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end('');}
  if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'synthetic@example.test',name:'AJ',shellAccess:'full'}));}
  if(req.url.startsWith('/api/')||req.url.startsWith('/assistant/')||req.url.startsWith('/notifications')||req.url.startsWith('/rooms')||req.url.startsWith('/artifacts')){res.writeHead(503,{'content-type':'application/json'});return res.end('{}');}
  res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html);
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true});
 const context=await browser.newContext({viewport:{width:1280,height:800}});
 const page=await context.newPage();
 await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
 await page.waitForSelector('#appShell.is-authed');
 const walk=await page.evaluate(()=>{
   const probe=document.createElement('div');probe.style.cssText='position:fixed;left:-9999px;top:0;width:1px;height:1px';document.body.appendChild(probe);
   const canvas=document.createElement('canvas');canvas.width=canvas.height=1;const ctx=canvas.getContext('2d',{willReadFrequently:true});
   const computed=(prop,token)=>{probe.style.setProperty(prop,'var('+token+')');const value=getComputedStyle(probe).getPropertyValue(prop);probe.style.removeProperty(prop);return value;};
   const paint=(color,under)=>{ctx.clearRect(0,0,1,1);if(under){ctx.fillStyle=under;ctx.fillRect(0,0,1,1);}ctx.fillStyle='#ff00ff';ctx.fillStyle=color;if(ctx.fillStyle==='#ff00ff'&&color!=='#ff00ff')throw new Error('canvas could not parse '+color);ctx.fillRect(0,0,1,1);const d=ctx.getImageData(0,0,1,1).data;return [d[0],d[1],d[2],d[3]];};
   const rgbString=px=>'rgb('+px[0]+', '+px[1]+', '+px[2]+')';
   const lum=px=>{const c=[px[0],px[1],px[2]].map(v=>{v/=255;return v<=0.03928?v/12.92:Math.pow((v+0.055)/1.055,2.4)});return 0.2126*c[0]+0.7152*c[1]+0.0722*c[2]};
   const ratio=(fg,bg)=>{const a=lum(fg),b=lum(bg);return (Math.max(a,b)+0.05)/(Math.min(a,b)+0.05)};
   const results=[];
   for(const theme of ['light','dark']){
     if(theme==='dark')document.documentElement.dataset.theme='dark';else delete document.documentElement.dataset.theme;
     const ground=paint(computed('background-color','--ground'));
     const surfaces={};
     for(const token of ['--surface-0','--surface-1','--surface-2','--surface-3','--well']){surfaces[token]=paint(computed('background-color',token),rgbString(ground));}
     for(const token of ['--glass-chrome-fill','--glass-float-fill','--glass-sheet-fill','--glass-opaque-fill']){surfaces[token]=paint(computed('background-color',token),rgbString(ground));}
     for(const [surface,bg] of Object.entries(surfaces)){
       for(const text of ['--text-1','--text-2','--text-3','--ink-1','--ink-2','--ink-3','--ember-text']){
         const fg=paint(computed('color',text),rgbString(bg));
         const r=ratio(fg,bg);
         results.push({theme,surface,text,ratio:Math.round(r*100)/100,need:4.5,ok:r>=4.5});
       }
       const ring=paint(computed('color','--ring'),rgbString(bg));
       const rr=ratio(ring,bg);
       results.push({theme,surface,text:'--ring',ratio:Math.round(rr*100)/100,need:3,ok:rr>=3});
     }
   }
   delete document.documentElement.dataset.theme;
   probe.remove();
   return results;
 });
 const failures=walk.filter(row=>!row.ok);
 console.log('contrast walk: '+walk.length+' pairs, '+failures.length+' failures');
 if(failures.length){console.error(JSON.stringify(failures,null,1));process.exitCode=1;}
 // the menu component: opens from its trigger with the origin aimed at it,
 // keyboard wraps, Escape returns focus — and none of it animates under
 // reduced motion
 await page.emulateMedia({reducedMotion:'reduce'});
 const menuProof=await page.evaluate(()=>{
   const switcher=document.getElementById('topbarOrganizationSwitcher');
   const menu=document.getElementById('topbarOrganizationMenu');
   if(!switcher||!menu)return {skipped:'no org switcher'};
   switcher.click();
   const opened=!menu.hidden&&switcher.getAttribute('aria-expanded')==='true';
   const origin=menu.style.transformOrigin;
   const cssOrigin=getComputedStyle(menu).transformOrigin;
   const transition=getComputedStyle(menu).transitionProperty;
   return {opened,origin,cssOrigin,transition,adopted:menu.dataset.bfMenu==='adopted',typeofBfMenu:typeof bfMenu};
 });
 if(menuProof.skipped){console.log('menu proof skipped: '+menuProof.skipped);}
 else{
   const problems=[];
   if(menuProof.typeofBfMenu!=='function')problems.push('bfMenu is not a global function');
   if(!menuProof.opened)problems.push('org menu did not open from its trigger');
   if(!menuProof.adopted)problems.push('org menu was not adopted by bfMenu');
   // plan 017: an adopted menu keeps the transform-origin its CSS declares (top left here) — bfMenu writes no inline origin on it
   if(menuProof.origin!=='')problems.push('adopted menu must not carry an inline transform-origin, got '+menuProof.origin);
   if(!/^0px 0px/.test(menuProof.cssOrigin))problems.push('org menu must grow from its CSS top-left origin, got '+menuProof.cssOrigin);
   if(menuProof.transition!=='none')problems.push('reduced motion must drop the menu transition, got '+menuProof.transition);
   await page.waitForTimeout(30);
   const focusInside=await page.evaluate(()=>document.getElementById('topbarOrganizationMenu').contains(document.activeElement));
   if(!focusInside)problems.push('focus did not move into the menu on open');
   await page.keyboard.press('Escape');
   const afterEscape=await page.evaluate(()=>({hidden:document.getElementById('topbarOrganizationMenu').hidden,focus:document.activeElement?.id}));
   if(!afterEscape.hidden)problems.push('Escape did not close the menu');
   if(afterEscape.focus!=='topbarOrganizationSwitcher')problems.push('Escape did not return focus to the trigger, got '+afterEscape.focus);
   if(problems.length){console.error(problems.join('\n'));process.exitCode=1;}
   else console.log('menu proof ok');
 }
 await context.close();await browser.close();server.close();
})().catch(error=>{console.error(error);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "DESIGN_SYSTEM_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered design-system contrast walk: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "0 failures") {
		t.Fatalf("contrast walk reported failures:\n%s", output)
	}
}
