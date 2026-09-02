# 013 — Hover and keystroke feedback on `--dur-fast`; fold the token residue

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: HIGH
- **Category**: Easing & duration / Cohesion & tokens
- **Estimated scope**: 1 file: ~20 declarations

## Problem

Hover-tier motion runs on the slow token: Drive tile and chat image push-ins take 360 ms (`transition-duration: var(--dur-slow)` on `.file-tile__image` ~L24464 and `.scout-chat-image__img` ~L13468) so the zoom is still crawling after the cursor left; a home launch button splits one hover across 120 ms and 360 ms (~L8397-8402: `background var(--dur-slow)`, `transform var(--dur-fast)`, `border-color var(--dur-fast)`, `box-shadow var(--dur-slow)`) so it visibly finishes twice; the chat send button wakes up over 220 ms on the FIRST character typed (`.scout-ask .assistant-send` ~L11275, every property on `--dur-med` except transform) while the home composer's equivalent correctly uses `--dur-fast`; thread rows put 220 ms on hover and on the selection flip (`.chat-thread-item` ~L12150).

Token residue outside the system: a fourth easing `cubic-bezier(.2, 0, 0, 1)` (~L8950, and `cubic-bezier(0.2, 0, 0, 1)` at ~L5173/5177) with an off-token `150ms`; hand-typed durations that are token values retyped (`bf-slidein 0.36s` ~L31173 vs `bf-slidein var(--dur-med)` ~L24092; `tray-slide-out 220ms` ~L36500) and one-off rungs (`stage-arrive 240ms` ~L29868/29936, `pkg-stage-fill 300ms` ~L36534, `goalcard-settle 180ms` ~L35032); two progress fills on `transition: transform 80ms linear` (~L6546) vs `0.2s linear` (~L18470); the theme toggle's sun/moon rest at `scale(0.25)` with `filter: blur(4px)` (~L1206-1220); the Drive previewer is a pure fade with no transform (~L25021/25040).

## Target

- Hover push-ins: `transition-duration: var(--dur-fast)` for `transform` on `.file-tile__image` and `.scout-chat-image__img` (opacity may stay `--dur-med`).
- Home launch button: all four properties on `var(--dur-fast) var(--ease)`.
- `.scout-ask .assistant-send`: every property `var(--dur-fast)`; `.chat-thread-item`: hover/selection properties `var(--dur-fast)`.
- Replace both `cubic-bezier(.2, 0, 0, 1)` / `(0.2, 0, 0, 1)` with `var(--ease)`; `150ms` → `var(--dur-fast)`.
- `bf-slidein 0.36s` → `var(--dur-slow)` only if that surface is a panel, else `var(--dur-med)` to match its twin at ~L24092 (pick the twin: `var(--dur-med)`).
- `tray-slide-out 220ms` → `var(--dur-med)`; `stage-arrive 240ms` → `var(--dur-med)`; `pkg-stage-fill 300ms` → `var(--dur-slow)`; `goalcard-settle 180ms` → `var(--dur-med)`.
- Both progress fills: `transition: transform var(--dur-fast) linear` (linear is correct for constant fills).
- Sun/moon icons: rest at `scale(0.9)` with no `filter`; drop `filter` from their transition list.
- Previewer: add `transform: scale(0.98)` to the `@starting-style` state and `transform` to its transition (`opacity var(--dur-med) var(--ease), transform var(--dur-med) var(--ease)`).

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. Apply each Target line by grepping its literal; do not touch unrelated properties in the same rule.
2. For every raw duration you replace, check the reduced-motion blocks: once token-timed, remove any now-redundant per-selector `animation-duration: 1ms` override ONLY if a pin does not quote it.
3. Grep `frontend_*_test.go` for each replaced literal; update pins with reason "plan 013: hover/keystroke on --dur-fast; token residue".

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem|Drive|Chat' .` green; `grep -c 'cubic-bezier(.2, 0, 0, 1)' index.html` → 0; `grep -cE 'animation: [a-z-]+ (0\.36s|220ms|240ms|300ms|180ms) ' index.html` → 0.
- **Feel check**: restart the sandbox, then:
  - Sweep the cursor across Drive tiles: each zoom completes before the next tile is reached.
  - Type one character in the chat composer: the send button wakes within a frame or two, not a beat later.
  - Toggle the theme: sun/moon crossfade with a small scale, no blur.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: no raw curves/durations remain outside the token block, hover/keystroke feedback is on --dur-fast, tests green.

## Result

- 2026-09-02: hover push-ins on `--dur-fast` (`.file-tile__image` transform, `.scout-chat-image__img`); `.office-launch__wave` all four properties `--dur-fast`; `.scout-ask .assistant-send` and `.chat-thread-item` every property `--dur-fast`; `.home-suggestion` `150ms`/`cubic-bezier(.2, 0, 0, 1)` → `--dur-fast`/`--ease`; both `room-scout-quick-*` cradle curves `cubic-bezier(0.2, 0, 0, 1)` → `--ease`; `stage-arrive 240ms` ×2, `bf-slidein 0.36s`, `goalcard-settle 180ms`, `tray-slide-out 220ms` → `--dur-med`; `pkg-stage-fill 300ms` → `--dur-slow`; re-anchored one un-named raw duration the mechanical grep catches: `lobby-shake 300ms` → `--dur-med` (its JS removes `.is-shake` at 320 ms, so `--dur-slow` would be cut off); both progress fills `transition: transform var(--dur-fast) linear`; sun/moon icons rest at `scale(0.9)` with no `filter` and `filter` dropped from the `.tool-rail__theme svg` transition — NOTE: applied before the coordinator's later scope note to skip `.tool-rail*`; left in place rather than issue a second edit into the shell region (reviewer may revert); `.drive-previewer` enters with `transform: scale(0.98)` in `@starting-style` and `transform var(--dur-med) var(--ease)` in its transition. The org button's `ease-out/150ms` pair was already folded by plan 011. No reduced-motion `animation-duration: 1ms` override belonged to any re-tokened keyframe (`.lobby__passinput.is-shake` reduced-motion entry is pinned, kept). Pins re-pinned (reason "plan 013: hover/keystroke on --dur-fast; token residue"): `frontend_latency_test.go` (`tray-slide-out var(--dur-med)`; sun/moon `scale(0.9)`, blur pin removed), `frontend_drive_wave5_test.go` (previewer `@starting-style` + transition; the opacity-only fatal replaced). Gate `TestIndex|TestPolishWave10|DesignSystem|Drive|Chat` green; `cubic-bezier(.2, 0, 0, 1)` → 0; raw `animation: … (0.36s|220ms|240ms|300ms|180ms)` → 0.
