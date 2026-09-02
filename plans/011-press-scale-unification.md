# 011 — One press depth everywhere: `--press-scale` replaces six hand-typed scales

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: HIGH
- **Category**: Cohesion & tokens / Easing & duration
- **Estimated scope**: 1 file: ~45 declarations

## Problem

`--press-scale: 0.97` exists (61 uses) but 44 `:active` rules hand-type their own depth: 36× `scale(0.96)`/`scale(.96)`, 5× `0.98`/`0.985`, `scale(0.94)` on the chat send button (the deepest press in the app on the most-pressed control), `scale(0.95)` on a home control. The organization button presses twice: the parent squashes to `.96` via the `scale` property while its child mark squashes to `--press-scale` — compounding transforms.

```css
/* index.html ~L1427-1432 — current */
.topbar__organization { transition-property: scale, background-color, box-shadow; transition-duration: 150ms; transition-timing-function: ease-out; }
.topbar__organization:active { scale: .96; }
.topbar__organization:active .topbar__mark { transform: scale(var(--press-scale)); }
/* index.html ~L8654 — current */
.home-scout-composer__send:active:not(:disabled) { transform: scale(0.94); }
```

## Target

Every `:active` press uses `transform: scale(var(--press-scale))` (0.97). The org button transitions on tokens and presses once (the child mark rule removed; only the parent scales).

```css
/* target */
.topbar__organization { transition-property: transform, background-color, box-shadow; transition-duration: var(--dur-fast); transition-timing-function: var(--ease); }
.topbar__organization:active { transform: scale(var(--press-scale)); }
/* delete: .topbar__organization:active .topbar__mark { … } */
```

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. `grep -n ":active" index.html | grep -E "scale\((0?\.9[0-9]+)\)"` — for every match that is a press feedback (not a data-viz or icon-specific transform), replace the literal with `scale(var(--press-scale))`. Keep any rule where the comment documents a deliberate exception (there are none known).
2. Fix the organization button per Target; delete the child-mark press rule.
3. Grep `frontend_*_test.go` for any of the replaced literals (`scale(0.94)`, `scale(.96)`, `scale: .96`) and update pins with the reason "plan 011: one press token".

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` green; `grep -c 'scale(0.94)' index.html` → 0; `grep -cE ':active[^}]*scale\(0?\.9(4|5|6|8)' index.html` → 0.
- **Feel check**: restart the sandbox, then:
  - Press the chat send button, a Drive row, a rail tab and the org tile: all four sink by the same amount and release in ~120 ms.
  - The org tile's flame does not shrink more than the tile.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: no hand-typed press scales remain; tests green.

## Result

- 2026-09-02: 83 `:active` press declarations now read `transform: scale(var(--press-scale))` (was 0.92/0.94/0.95/0.96/.96/0.97/0.98/.98/0.985/0.99/0.996 — the 0.97 hand-types and a 0.996 on `#filesUploadButton` were folded too); `.topbar__organization` now `transition-property: transform, background-color, box-shadow; transition-duration: var(--dur-fast); transition-timing-function: var(--ease)`, presses once via `transform` (the `.topbar__organization:active .topbar__mark` child rule deleted; comment updated). Four `scale:`-property presses (`.artifact-stage__escape/__close`, `.pd1-primary-nav__external`, `.content-studio-drawer__action/__close`) moved to `transform:` with their `transition-property … scale` → `… transform`. Left alone: `.office-launch__wave:active .office-launch__cradle { transform: scale(0.96) }` (documented aperture glyph cue, not a press) and three non-press `scale(0.94)` (account-menu + greenroom-filters `@starting-style`, `bf-popin` keyframe — raised to 0.96 by plan 012), so `grep -c "scale(0.94)"` reads 3 for non-press states while `grep -cE ":active[^}]*scale\(0?\.9(4|5|6|8)"` → 0. Pins re-pinned (reason "plan 011: one press token"): `frontend_desktop_chat_quality_test.go` ×3, `frontend_mobile_chat_quality_test.go`, `frontend_device_transfer_test.go`, `frontend_packaging_stage_drawer_test.go`, `frontend_private_riff_test.go`. Gate `TestIndex|TestPolishWave10|DesignSystem` green.
