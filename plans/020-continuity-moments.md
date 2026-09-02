# 020 — Continuity moments: thread-list FLIP, work-step transitions, theme crossfade, previewer direction, move attribution

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: MEDIUM
- **Category**: Missed opportunities
- **Estimated scope**: 1 file: 5 sites, ~150 lines JS/CSS

## Problem

Five state changes snap in one frame where a beautiful product would carry the eye:

1. `renderChatAgentThreads` (~L78422, three `replaceChildren`) re-sorts the thread list on every new message; rows jump and the unread badge (`.chat-thread-item__unread`, `__unread-dot` ~L12410/12428, no transition) materialises instantly.
2. Work step nodes (`studioProjectStepsNode` ~L48287; `.studio-project-step__node::before` ~L10891) jump queued→running (8→9 px, ink→ember, glow) and done deletes the dot via `display: none` while a check appears the same frame.
3. `applyTheme` (~L40791) crossfades only `html, body` (~L616); every `[data-theme]` surface, line, glass tier and icon flips on the first frame.
4. Drive previewer `show(index)` → `stage.replaceChildren(node)` (~L93418) swaps with a blank decode gap and no direction.
5. `moveFileToFolderClient` (~L94435) re-renders and the row vanishes with no trace; the board already prints "moved · just now" via `.board-preview__card.is-moved::after` + `attribution-fade` (~L30418).

## Target

1. FLIP the thread list: before `replaceChildren`, record each row's `getBoundingClientRect()` by thread id; after, for rows whose top changed, set `transform: translateY(prevTop - newTop)`, force a style read, then transition to `none` over `var(--dur-med) var(--ease)`; rows that did not move get no transform (an unchanged poll render is a visual no-op). New rows fade in (`opacity 0→1`, `--dur-med`). Badge/dot: `transition: opacity var(--dur-fast) var(--ease), transform var(--dur-fast) var(--ease)` with a hidden state `opacity: 0; transform: scale(0.9)`. Gate the FLIP on `!reducedMotion.matches`.
2. Step nodes: `transition: width var(--dur-fast) …` is NOT allowed — use `transform: scale(1.125)` for running, `transition: transform var(--dur-med) var(--ease), background-color var(--dur-med), box-shadow var(--dur-med)`; done: the dot fades out (`opacity`) while the check fades in over `--dur-med` (both present in the DOM, toggled by opacity, not `display`).
3. Theme: add `transition: background-color var(--dur-slow) var(--ease), border-color var(--dur-slow) var(--ease), color var(--dur-slow) var(--ease)` to the surface/line-bearing roles (`.glass-chrome|float|sheet`, `.tool-rail`, `.topbar`, `.surface-*` ladder rules, `.bf-menu`) ONLY while `html[data-theme-transition]` is set; `applyTheme` sets that attribute before flipping the theme and removes it after `--dur-slow` (setTimeout, not rAF). Never a global `* { transition }`.
4. Previewer: keep both stages; on step, await `img.decode()` (with a 300 ms cap) before swapping; the outgoing stage moves `translateX(∓24px)` + fades over `--dur-med`, the incoming from `translateX(±24px)`; direction follows the arrow; gated on reduced motion (instant swap).
5. Move attribution: after a successful move, keep a ghost row in place for one `--dur-med` fade-out with the line `moved to <folder> · undo` (reuse the board's `attribution-fade` timing: 10 s visible, then fade) — the `undo` link calls the existing move with the previous folder id.

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1–5. Implement each Target as a small, self-contained function next to the render it decorates; no new module-level `const` read during boot (`let`/`var`).
6. Pins: static tests that (a) the thread-list FLIP helper exists and gates on `reducedMotion`, (b) step nodes transition `transform` not `width`, (c) `applyTheme` sets `data-theme-transition`, (d) previewer awaits `decode` with a cap, (e) the move ghost carries `undo`.

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|Chat|Studio|Theme|Drive|TestPolishWave10' .` green.
- **Feel check**: restart the sandbox, then:
  - Send a message from a second thread: its row glides to the top; the others slide, nothing pops.
  - Toggle the theme: every surface crossfades together over ~360 ms; no two-stage snap.
  - Press → in the previewer: the next photo slides in from the right with no blank gap.
  - Move a file: a ghost shows where it went with an undo link, then fades.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: all five moments animate as specified; unchanged renders produce no motion; tests green.

**Result (2026-09-02)**: (1) `flipChatThreadLists(render)` wraps the three `replaceChildren` in `renderChatAgentThreads` (pinned literals stay inside the body): rows are keyed by `data-thread-id`, tops recorded before/after, movers get an inverse `translateY` then glide on `transform var(--dur-med) var(--ease)`, unmoved rows untouched (live-verified: no-op render → 0 transforms; reorder → 3/3 glide), new rows `.is-entering` (`bf-fade`), a badge/dot that just appeared `.is-arriving` (`chat-unread-in`); gated on `reducedMotion.matches`. (2) step nodes: `::before` transitions transform/colour/box-shadow/opacity; running = `transform: scale(1.125)` (was 9px width/height); done/failed dot → `opacity: 0; transform: scale(0.6)` (was `display: none`) with the `.stride-mark` absolutely centred and crossfading in; `studioProjectStepsNode` carries the last drawn state (`var studioStepShownStates`) and flips a macrotask later so the transition plays across the hub rebuild (`item.dataset.state = state` pin intact). (3) `applyTheme` holds `html[data-theme-transition]` for `motionTokenMs('--dur-slow')` (new helper beside `reducedMotion`); one attribute-scoped rule near `html, body` transitions background/border/color on the glass tiers, `.tool-rail`, `.topbar`, `.bf-menu`, `.well`, `.btn`, thread/context/message cards, file rows/tiles and form fields — no rule of the shell's was edited. (4) previewer `show(index, direction)` is async: a stepped image awaits `decode()` capped at 300 ms (sequence-guarded), then both stages share `grid-area: 1 / 1` and the outgoing slides ∓24px/fades while the incoming slides in ±24px (`step` passes `Math.sign(delta)`); reduced motion / first show → flat `replaceChildren`. (5) `moveFileToFolderClient` records the row's slot (`filesMoveGhostSlot`, via new `data-file-id` on `.files-row`/`.file-tile`) and after the re-render inserts a `.files-row--ghost` "moved to <folder> · undo" (`attribution-fade 10s`, then removed; `undo` calls the move with the previous folder); reduce cover added beside the board's. New pin `TestPolishWave10ContinuityMoments`. Gate `TestIndex|Chat|Studio|Theme|Drive|TestPolishWave10`: green except `TestDocumentStudioRenderedNestedListsAndSafeImagesRoundTrip` (a colour assertion `rgba(255,255,255,0.1)` vs `rgba(0,0,0,0.1)` — the same concurrent dark-ramp token change that broke it in the 018 gate, before any 019/020 edit). Feel (Playwright, JS-asserted on :3171): org menu no inline origin + `0px 0px`; notification panel `opacity, transform, display` / `allow-discrete`; bars `transition-property: transform, opacity`; theme attribute held then cleared with `background-color, border-color, color` on the rail; `--dur-breathe` = 0ms under reduce; grill counter lands 7.5 at once under reduce; zero page errors.
