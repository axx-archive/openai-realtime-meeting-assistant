# 006 — Meter and progress fills: animate transform, not width

- **Status**: DONE
- **Commit**: d8846df
- **Severity**: HIGH
- **Category**: Performance
- **Estimated scope**: 2 files (index.html + frontend_noise_suppression_test.go), ~8 CSS rule edits + 6 JS driver edits + 1 test-string update

## Problem

Every meter/progress fill in the app animates the `width` property — a layout property that forces layout + paint on each frame. The worst case is the live noise-suppression meter, which updates continuously from audio metrics while WebRTC media is running on the same main thread. The rule (from the animation playbook): animate `transform` and `opacity` only.

All fills sit inside tracks that already have `overflow: hidden` + a rounded radius, so the composite-only replacement is: fill keeps `width: 100%`, rests at `transform: translateX(-100%)` (empty), and progress `p`% renders as `transform: translateX(-(100 - p)%)`. Translation preserves the rounded leading edge and runs on the compositor.

Current code (all in `index.html`, commit d8846df — match on the excerpt text, not line numbers):

```css
/* ≈4401-4409 — live suppression meter (continuous updates, highest value) */
      .av-suppression__bar {
        display: block;
        width: 0%;
        height: 100%;
        border-radius: inherit;
        background: var(--live);
        transition: width var(--dur-med) var(--ease);
      }
```

```css
/* ≈4523-4530 — DEAD CSS (no markup/JS references .voice-meter anywhere); convert anyway so zero width-transitions remain */
      .voice-meter__bar {
        display: block;
        width: 0%;
        height: 100%;
        border-radius: inherit;
        background: var(--live);
        transition: width 80ms linear;
      }
```

```css
/* ≈6188-6196 — office agent card rail; driven by --agent-progress set on the PARENT card */
      .office-agent-card__bar {
        display: block;
        width: var(--agent-progress, 0%);
        height: 100%;
        border-radius: inherit;
        background: var(--accent);
        transition: width var(--dur-med) var(--ease);
      }
```

```css
/* ≈6823-6830 — grill pressure edge (currently has NO JS driver; width stays 0%) */
      .grillstage__pressure-fill {
        display: block;
        height: 100%;
        width: 0%;
        border-radius: var(--r-full);
        background: linear-gradient(90deg, color-mix(in srgb, var(--agent) 55%, transparent), var(--agent));
        transition: width var(--dur-slow) var(--ease);
      }
```

```css
/* ≈6917-6924 — grill score meters */
      .grillstage__meter-fill {
        display: block;
        height: 100%;
        width: 0%;
        border-radius: var(--r-full);
        background: var(--agent);
        transition: width var(--dur-slow) var(--ease);
      }
```

```css
/* ≈9479-9484 — scout chat research progress */
      .scout-chat-research__fill {
        height: 100%;
        background: var(--accent);
        border-radius: 999px;
        transition: width 0.2s linear;
      }
```

```css
/* ≈11596-11602 — intelligence contribution bars */
      .intel-contrib__fill {
        display: block;
        height: 100%;
        border-radius: inherit;
        background: var(--accent);
        opacity: 0.8;
        transition: width var(--dur-slow) var(--ease);
      }
```

```css
/* ≈13566-13572 — research run ember bar */
      .research-bar__fill {
        height: 100%;
        border-radius: inherit;
        background: var(--ember);
        transition: width var(--dur-med) var(--ease);
      }
```

JS drivers (verbatim current lines, `index.html`):

```js
/* ≈25386 — renderSuppressionMeter() */
        if (audioSuppressionBar) audioSuppressionBar.style.width = `${Math.round((db / 30) * 100)}%`
/* ≈29684 — updateResearchRunningStage(); the comment above it says "the ember fill animates its width transition" — update that comment wording too */
        wrap.querySelectorAll('.research-bar__fill').forEach(fill => { fill.style.width = `${pct}%` })
/* ≈30524 & ≈30527 — grill stage rows, both branches of the reduceMotion conditional */
              fill.style.width = `${value * 10}%`
/* ≈44453 — scout chat research card refresh */
          fill.style.width = `${Math.max(0, Math.min(100, pct))}%`
/* ≈47551-47553 — intel contribution rows (conditional) */
          fill.style.width = lineCount === 0
            ? '0%'
            : `${Math.round(Math.min(1, (Number(person.fuel) || 0) / fuelMax) * 100)}%`
/* ≈48098 — package rail reusing .intel-contrib__fill */
          fill.style.width = `${packageStagePercent(record)}%`
```

NOT in scope: `≈36775 fill.style.width = ...` on `.portfolio__meterfill` — that class has **no** width transition (render-once static set; writing width once at render is not an animation problem). Leave it and its CSS alone. Same for `≈54100 card.style.setProperty('--agent-progress', ...)` — leave the JS line untouched; the CSS conversion below absorbs it.

## Target

For every fill rule above, replace the width geometry with the translate pattern, keeping each rule's **existing duration and easing** (only the animated property changes):

```css
/* target shape — shown for .av-suppression__bar; apply the same transform lines to each rule */
      .av-suppression__bar {
        display: block;
        width: 100%;
        height: 100%;
        border-radius: inherit;
        background: var(--live);
        transform: translateX(-100%);
        transition: transform var(--dur-med) var(--ease);
      }
```

Per-rule specifics:
- `.voice-meter__bar`: same pattern, `transition: transform 80ms linear;`
- `.office-agent-card__bar`: `width: 100%; transform: translateX(calc(var(--agent-progress, 0%) - 100%)); transition: transform var(--dur-med) var(--ease);` (the parent-set `--agent-progress` var keeps working; the card is rebuilt via `replaceChildren` on update so the transition rarely plays — this is a cold path, which is why the var-driven transform is acceptable here)
- `.grillstage__pressure-fill` and `.grillstage__meter-fill`: `width: 100%; transform: translateX(-100%); transition: transform var(--dur-slow) var(--ease);`
- `.scout-chat-research__fill`: add `display: block; width: 100%; transform: translateX(-100%);` and `transition: transform 0.2s linear;`
- `.intel-contrib__fill`: add `width: 100%; transform: translateX(-100%);` and `transition: transform var(--dur-slow) var(--ease);`
- `.research-bar__fill`: add `display: block; width: 100%; transform: translateX(-100%);` and `transition: transform var(--dur-med) var(--ease);`

Every JS driver converts `style.width = \`${p}%\`` to `style.transform = \`translateX(${p - 100}%)\`` (clamp exactly as the current code clamps). Verbatim targets:

```js
        if (audioSuppressionBar) audioSuppressionBar.style.transform = `translateX(${Math.round((db / 30) * 100) - 100}%)`
```
```js
        wrap.querySelectorAll('.research-bar__fill').forEach(fill => { fill.style.transform = `translateX(${pct - 100}%)` })
```
```js
              fill.style.transform = `translateX(${value * 10 - 100}%)`
```
(both grill branches)
```js
          fill.style.transform = `translateX(${Math.max(0, Math.min(100, pct)) - 100}%)`
```
```js
          fill.style.transform = lineCount === 0
            ? 'translateX(-100%)'
            : `translateX(${Math.round(Math.min(1, (Number(person.fuel) || 0) / fuelMax) * 100) - 100}%)`
```
```js
          fill.style.transform = `translateX(${packageStagePercent(record) - 100}%)`
```

Test update — `frontend_noise_suppression_test.go` ≈172. The assertion's intent is "the bar transitions a *named* property"; retarget it:

```go
	// current
	if !strings.Contains(html, "transition: width var(--dur-med) var(--ease)") {
		t.Errorf("index.html suppression bar must transition a named property")
	}
	// target
	if !strings.Contains(html, "transition: transform var(--dur-med) var(--ease)") {
		t.Errorf("index.html suppression bar must transition a named property")
	}
```

## Repo conventions to follow

- Motion tokens: `--dur-fast` 120ms / `--dur-med` 220ms / `--dur-slow` 360ms / `--ease`; do not invent new tokens, do not change any rule's duration or easing in this plan.
- The reduced-motion block (≈21279-21280) already contains `.grillstage__pressure-fill, .grillstage__meter-fill { transition: none; }` — `transition: none` kills the transform transition just as it killed width; leave those lines exactly where they are (`wave12_private_grill_test.go:528` requires `grillstage__` text within 900 chars after `.voice-ledger { animation: none; }` at ≈21254 — do not insert anything between them).
- `frontend_latency_test.go:557-558` pins the `.intel-contrib__fill` selector AND its `background: var(--accent);` declaration — keep that line in the rule, unmoved and byte-identical.
- The codebase writes inline styles directly on elements (see the current drivers); do not introduce a shared helper function.

## Steps

1. Convert the 8 CSS rules in `index.html` exactly as specified in Target (find each by its excerpt text; line numbers are approximate).
2. Convert the 6 JS driver sites exactly as specified (note ≈30524/30527 is two lines in one function).
3. Update the comment above `updateResearchRunningStage` (≈29679): change the phrase "the ember fill animates its width transition" to "the ember fill animates its transform transition".
4. Update the pinned string in `frontend_noise_suppression_test.go` ≈172 as shown in Target.
5. Run the mechanical verification below.

## Boundaries

- Do NOT touch `.portfolio__meterfill` (CSS or its JS at ≈36775) or the `--agent-progress` JS write at ≈54100.
- Do NOT change any duration or easing value — property swaps only.
- Do NOT change markup/HTML structure, class names, or element types.
- Do NOT add dependencies or helper functions.
- Do NOT edit any file other than `index.html` and `frontend_noise_suppression_test.go`.
- If an excerpt doesn't match the code you find, STOP and report instead of improvising.

## Post-execution addendum (2026-07-11 review finding, fixed)

The adversarial diff review caught two sites this plan's driver list missed: the **template-literal creation sites** for `.research-bar__fill` (`researchRunningStage` ≈29687 and `researchAgentCard` ≈29764) emitted `style="width:${pct}%"` — the HTML-attribute form, invisible to the `style.width = ` grep. With the converted CSS those bars rendered empty (inline width + resting `translateX(-100%)`). Both now emit `style="transform:translateX(${pct - 100}%)"`. Lesson encoded below: verify BOTH write forms.

## Verification

- **Mechanical**:
  - `grep -c 'transition: width' index.html` → **0**
  - `grep -c 'style.width = ' index.html` → **1** (the portfolio render-once site only)
  - `grep -c 'style="width:$' index.html` → **0** (attribute-form template writes — the form the original verification missed)
  - `cd /Users/ajhart/meetingassist && go test -count=1 -run 'TestIndex' .` → ok
- **Feel check** (run `go build -o /tmp/ma . && MEETING_ROOM_PASSWORD="smoke-pass-1234" /tmp/ma -addr :3171`, browser at localhost:3171):
  - Open audio settings with noise suppression active: the "quieter" meter slides smoothly; in DevTools Performance, no purple Layout blocks attributable to the meter while it updates.
  - Start a research run: the ember progress bar fills with the same eased motion as before; the rounded leading edge of the fill still renders as a semicircle cap, not a squashed ellipse.
  - Bars at 0% show an empty track (nothing peeking out on the left edge).
- **Done when**: both greps hit their exact counts, TestIndex is green, and the suppression meter animates without layout work.
