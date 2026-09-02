# 017 — bfMenu: trigger-anchored transform-origin on every branch (adopted and non-fixed)

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: MEDIUM
- **Category**: Physicality & origin
- **Estimated scope**: 1 file: `bfMenu.place()` (~L67380-67400)

## Problem

`place()` computes the origin box as `!adopted && options.fixed ? {offsetWidth/offsetHeight box} : menu.getBoundingClientRect()`; `open()` sets `hidden = false` and calls `place()` synchronously, so on the non-fixed/adopted branch the rect is read while the `@starting-style` transform (`scale(0.92)`/`0.94`) is applied — the origin is skewed 10–20 px on a 340 px menu and OVERWRITES the correct static `transform-origin` (e.g. `top right`) that the eight adopted menus declare in CSS (account menu ~L45249, chat message More ~L50475/85040, green-room filters ~L99536, room More ~L102286, …). The fixed branch was corrected earlier today; this finishes the job.

## Target

- For adopted menus: do NOT write an inline `transformOrigin` at all — keep the CSS-declared origin (they are anchored by CSS position already).
- For built non-fixed menus: compute the origin from the trigger rect and the menu's `offsetWidth/offsetHeight` (layout metrics, unaffected by the transform), exactly like the fixed branch now does.

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. In `place()`, branch: `if (adopted) { /* leave transform-origin to CSS */ } else { const box = { left: <written left or trigger-derived>, top: <written top>, width: menu.offsetWidth, height: menu.offsetHeight }; … }` and derive the origin side from the trigger rect vs that box.
2. Remove any inline `transformOrigin` write on the adopted path; if a pin greps the `transformOrigin` line, keep the line for the built path only.
3. Verify each adopted menu's CSS declares `transform-origin` (add `transform-origin: top right` / `top left` where missing, matching where its trigger sits).

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|Menu|DesignSystem|TestPolishWave10' .` green.
- **Feel check**: restart the sandbox, then:
  - Open the account menu, a message More menu and the room More menu at 10% playback: each grows out of its trigger corner, not from beside it.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: no adopted menu carries an inline transform-origin; built menus originate at their trigger.

**Result (2026-09-02)**: `place()` now returns early for adopted menus (no inline `transformOrigin`; comment re-anchored on "place the built menu against the trigger"); built menus derive the origin box from layout metrics on both branches (fixed: written left/top; non-fixed: `offsetParent` rect + `offsetLeft/offsetTop`) with the pinned `menu.style.transformOrigin = ...` line kept; `bfMenuAdoptOpen` dropped its now-dead deferred `place()`; CSS `transform-origin` added where adopted menus lacked one — `.artifact-stage__hero-menu` (bottom right), `.manifest-card__more-menu` (bottom left), `.package-row__more-menu` (bottom right), `.room-more__menu` (bottom right); the eight others already declared theirs. Pin re-pinned: `frontend_design_system_v2_test.go` org-menu browser proof now asserts NO inline origin + computed `0px 0px` (CSS top-left) instead of `/px/` inline. Gate `TestIndex|Menu|DesignSystem|TestPolishWave10` green.
