# 002 — Command palette: tiles render instantly

- **Status**: DONE
- **Commit**: bd289db
- **Severity**: HIGH
- **Category**: Purpose & frequency / Interruptibility
- **Estimated scope**: 1 file, 1 CSS edit + 4 JS edits

## Problem

Every `.palette__tile` plays a spring pop-in with a per-tile stagger:

```css
/* index.html:19505-19513 — current (tail of the .palette__tile rule) */
        transition: transform var(--dur-fast) var(--ease), box-shadow var(--dur-fast) var(--ease), border-color var(--dur-fast) var(--ease);
        animation: bf-popin var(--dur-med) var(--ease-spring) both;
        animation-delay: var(--palette-stagger, 0ms);
```

Worse: `paletteRenderList()` (index.html:39645-39648) calls `paletteBodyEl.replaceChildren()` and rebuilds every tile **on every keystroke** in the palette search (`input` handler at ≈39529), so the whole grid replays the spring + up-to-320ms stagger while the user types. The stagger is set here:

```js
/* index.html:39656 — current */
        const stagger = (tile) => { tile.style.setProperty('--palette-stagger', `${Math.min(shown, 8) * 40}ms`); shown += 1 }
```

and applied at three call sites (≈39664, 39671, 39683), each shaped like:

```js
for (const tool of recentTools) { const tile = paletteMakeTile(tool, tokens); stagger(tile); grid.appendChild(tile) }
```

A launcher is a 100+/day surface: per the frequency rule it gets **no** entrance motion. The sheet itself already animates (`.palette` container: `animation: bf-sheetin var(--dur-med) var(--ease) both;` ≈line 19662) — that stays.

NOTE: line numbers are from commit bd289db — always match on the exact code excerpt.

## Target

- `.palette__tile` keeps its `transition:` line (hover/press feedback) but has **no** `animation` and **no** `animation-delay` declaration.
- The `stagger` helper, the `let shown = 0` counter that only feeds it, and all three `stagger(tile); ` call fragments are deleted. Tiles are created and appended with no per-tile motion.

## Repo conventions to follow

- The palette sheet entrance (`bf-sheetin` on the container) is the sanctioned motion for this surface — do not add anything to replace the tile pop.
- Press/hover feedback pattern to preserve: `.palette__tile:hover { transform: translateY(-2px); ... }` and `.palette__tile:active:not([disabled]) { transform: scale(var(--press-scale)); }` (≈19515-19516).

## Steps

1. In `/Users/ajhart/meetingassist/index.html`, in the `.palette__tile` rule (≈19495-19513), delete these two lines exactly:
   `animation: bf-popin var(--dur-med) var(--ease-spring) both;`
   `animation-delay: var(--palette-stagger, 0ms);`
2. In `paletteRenderList()` (≈39645), delete the line
   `const stagger = (tile) => { tile.style.setProperty('--palette-stagger', \`${Math.min(shown, 8) * 40}ms\`); shown += 1 }`
   and the `let shown = 0` line directly above it (verify nothing else reads `shown` inside the function first — if something does, STOP and report).
3. At each of the three call sites (≈39664, 39671, 39683), remove only the `stagger(tile); ` fragment, e.g.
   `{ const tile = paletteMakeTile(tool, tokens); stagger(tile); grid.appendChild(tile) }`
   → `{ const tile = paletteMakeTile(tool, tokens); grid.appendChild(tile) }`
4. Confirm no other references remain: `grep -n 'palette-stagger\|stagger(' index.html` → 0 hits in the palette region.

## Boundaries

- Do NOT touch `paletteMakeTile`, the focus trap, keyboard handling, or render order.
- Do NOT remove the `.palette__tile` `transition:` line or hover/active rules.
- Do NOT touch the `bf-popin` keyframe definition (other surfaces use it, e.g. toasts).
- Do NOT touch the `.palette` container's `bf-sheetin` entrance.
- If a step doesn't match the code you find, STOP and report instead of improvising.

## Verification

- **Mechanical**:
  - `grep -c 'palette-stagger' index.html` → 0.
  - `cd /Users/ajhart/meetingassist && go test -count=1 -run 'TestIndexWave11|TestIndexPalette' .` → PASS (or "no tests to run" if the regex matches none — then run `go test -count=1 -run 'TestIndex' .`).
  - `node --check` is not applicable (inline script) — instead: `grep -c 'stagger(tile)' index.html` → 0.
- **Feel check** (reviewer): open the palette (Tools button or `/` in composer) — tiles are simply there when the sheet lands; type in the search — the grid filters with zero pop/stagger replay; tile hover lift and press scale still work.
- **Done when**: both greps are 0, tests pass, palette filters without any tile animation.
