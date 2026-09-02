# 010 — Tab switch: remove the 360 ms icon bounce from both navs

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: HIGH
- **Category**: Purpose & frequency
- **Estimated scope**: 1 file: 2 CSS rules

## Problem

Switching tabs (rail click or ⌘1–5) is the most repeated navigation in the product. Plan 001 put the pane on a diet, but the ICON still bounces 360 ms with overshoot on every switch, in both nav copies, and restarts from zero when hopping quickly.

```css
/* index.html ~L1104 — current */
.tool-rail__tool[aria-pressed="true"] svg { animation: rail-icon-pop var(--dur-slow) var(--ease-spring) 1; }
/* index.html ~L37146 — current */
.pd1-primary-nav__item[aria-current="page"] > svg { animation: rail-icon-pop var(--dur-slow) var(--ease-spring) 1; }
@keyframes rail-icon-pop { 0% { transform: scale(0.8) } 55% { transform: scale(1.14) } 100% { transform: scale(1) } }
```

## Target

No animation on the selected icon. The active state is communicated by the ember glyph + the glow well (already in place) — instantly.

```css
/* target: both rules deleted; keyframe deleted if it has no other users */
```
If a pin greps `rail-icon-pop`, keep the keyframe definition and delete only the two `animation:` declarations.

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. Delete the `animation: rail-icon-pop …` declaration from `.tool-rail__tool[aria-pressed="true"] svg`.
2. Delete the `animation: rail-icon-pop …` declaration from `.pd1-primary-nav__item[aria-current="page"] > svg`.
3. `grep -n "rail-icon-pop" index.html *_test.go` — if no other users and no pin, delete the keyframe; otherwise leave it.

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|TestPD1' .` green.
- **Feel check**: restart the sandbox, then:
  - Press ⌘1…⌘5 rapidly: the glyph colour and glow change instantly; nothing scales.
  - DevTools Animations panel shows no animation on the nav svg after a switch.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: neither nav rule declares an animation; tests green.

## Result

- 2026-09-02: removed `animation: rail-icon-pop …` from `.tool-rail__tool[aria-pressed="true"] svg` (rule + its "arrival pop" comment deleted, replaced by a one-line note) and from `.pd1-primary-nav__item[aria-current="page"] > svg` (keeps `stroke-width: 2.2`); `@keyframes rail-icon-pop` deleted (no other users, no pin). No pins re-pinned. Gate `TestIndex|TestPolishWave10|TestPD1` green except `TestIndexRoomsWave7UpcomingListAndScheduleForm` (lobby `--live` → `--live-text` literal, another lane's concurrent edit at ~L29284 — not this plan).
