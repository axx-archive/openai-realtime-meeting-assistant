# 015 — Five open/close surfaces move from one-way keyframes to reversible transitions

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: MEDIUM
- **Category**: Interruptibility
- **Estimated scope**: 1 file: 5 surfaces

## Problem

These surfaces open with a keyframe and close by `display: none`, so closing mid-open hard-cuts and re-opening restarts from zero: the notification panel (`.notification-panel { animation: bf-islandin … }` ~L23208 + `[hidden] { display: none }` ~L23212), the memory card body (`.memory-card__body { animation: bf-tabin … }` ~L3953), the Drive details panel (`.drive-details.is-open { animation: bf-slidein … both }` ~L24092 and its mobile `bf-sheetin` twin ~L24116), the goalcard working disclosure (`animation: comment-preview-open …` ~L34908 — a modal wobble with rotate on an inline expand), and the in-room chat panel (`.room-chat { animation: bf-fadein … }` ~L22475 + mobile `bf-sheetin`).

## Target

Each becomes the `.bf-menu` pattern: `transition-property: opacity, transform, display; transition-behavior: allow-discrete;` on tokens, a hidden state with the exit transform, and an `@starting-style` entry. Example for the notification panel:

```css
/* target */
.notification-panel { transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease), var(--ease); }
.notification-panel[hidden] { display: none; opacity: 0; transform: translateY(-6px); }
@starting-style { .notification-panel:not([hidden]) { opacity: 0; transform: translateY(-6px); } }
```
Drive details: `translateX(28px)` as the hidden/starting transform (desktop) and `translateY(16px)` (mobile sheet). Memory card body and goalcard working: `opacity` + `translateY(-4px)`, no rotate. Room chat: `translateY(8px)`. Remove the `animation:` lines and the `both` fills.

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. For each of the five surfaces, replace the `animation:` declaration with the transition triple, add the hidden-state exit transform, add the `@starting-style` block (same specificity as the visible rule).
2. Where the surface is toggled with a class instead of `hidden` (`.drive-details.is-open`, `.room-chat`), key the hidden state on the absence of that class.
3. Under `@media (prefers-reduced-motion: reduce)` the tokens are zero — no extra rule needed unless the surface used a raw duration.
4. Grep pins for the old `animation:` literals; update with reason "plan 015: reversible transitions".

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|Notification|Memory|Drive|Room' .` green.
- **Feel check**: restart the sandbox, then:
  - Double-click the bell quickly: the panel reverses mid-flight instead of restarting.
  - Open and immediately close Drive details: it slides back out rather than vanishing.
  - Expand/collapse a memory card and a goalcard's working area: symmetric, no wobble.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: none of the five surfaces declares an `animation:`; all reverse mid-flight.

**Result (2026-09-02)**: all five `animation:` lines replaced with token-timed transitions + `@starting-style` entries. Fully reversible (display in the triple, `allow-discrete`): `.notification-panel` (`[hidden]` exit `translateY(6px) scale(0.96)` — rises from the bell, keeping the declared `left bottom` origin; the plan example's `-6px` sign would drop it away from its anchor) and `.goalcard__working` (`translateY(-4px)`, no rotate; `comment-preview-open` keyframe kept for `.comment-preview`). Entrance-only (display deliberately NOT in the list, reason in a CSS comment at each site): `.memory-card__body` (the card re-renders on toggle, body mounts fresh), `.drive-details` (`translateX(28px)`; mobile `translateY(16px)` on `--dur-slow` — `renderFilesDetails` empties the panel + collapses the shell column synchronously on close, so a lingering exit would show an empty box: JS follow-up for the Drive lane), `.room-chat` (`translateY(8px)`; in-room sheet `translateY(16px)` on `--dur-slow` — the host rail re-shows Scout / hides itself the instant chat closes). `bf-slidein`/`bf-sheetin` literals on `.drive-details.is-open` removed (re-anchored after plan 012 retimed the mobile one to `--dur-slow`). No pins re-pinned. Reduced-motion `animation: none` residue on these selectors left for plan 019. Gate `TestIndex|TestPolishWave10|Notification|Memory|Drive|Room` green (80.7s).
