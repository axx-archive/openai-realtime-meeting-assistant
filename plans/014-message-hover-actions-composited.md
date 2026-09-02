# 014 — Message hover actions: opacity reveal, no layout tween

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: HIGH
- **Category**: Purpose & frequency / Performance
- **Estimated scope**: 1 file: 2 rule pairs

## Problem

The action cluster on every message row reveals by tweening `max-width: 0 → 360px` (and `0 → 280px` on context cards), so each hover-in and hover-out relayouts the row — on the single most-hovered element in the product.

```css
/* index.html ~L20824-20850 — current */
#chatTool .desktop-chat-actions { max-width: 0; overflow: clip; opacity: 0; transition-property: max-width, opacity; … }
#chatTool .scout-chat-msg:hover .desktop-chat-actions { max-width: 360px; opacity: 1; }
/* index.html ~L19569-19586 — current */
#chatTool .chat-context-card__message-actions { max-width: 0; … transition-property: max-width, opacity; }
… :hover .chat-context-card__message-actions { max-width: 280px; }
```

## Target

The cluster is always laid out (its width reserved, `pointer-events: none` while hidden) and reveals with opacity only, on `--dur-fast`:

```css
/* target */
#chatTool .desktop-chat-actions { opacity: 0; pointer-events: none; transition: opacity var(--dur-fast) var(--ease); }
#chatTool .scout-chat-msg:hover .desktop-chat-actions,
#chatTool .scout-chat-msg:focus-within .desktop-chat-actions { opacity: 1; pointer-events: auto; }
```
Same for `.chat-context-card__message-actions`. If reserving width changes the row layout, position the cluster absolutely against the row's right edge (the row already has `position: relative` from the hover-actions containing-block fix) so no space is taken.

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. Rewrite the two rule pairs per Target; remove `max-width` and `overflow: clip` from both.
2. Verify the hover-actions containing block (`.scout-chat-msg { position: relative }`) still exists; if the cluster overlaps message text at narrow widths, keep it hidden below 861 px as before.
3. Grep pins for `transition-property: max-width, opacity` and update with reason "plan 014: composited hover reveal".

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndexChat|DesktopChat|TestPolishWave10' .` green; `grep -c 'transition-property: max-width, opacity' index.html` → 0.
- **Feel check**: restart the sandbox, then:
  - Hover down a long thread: actions fade in/out with no text reflow; DevTools Performance shows no Layout entries per hover.
  - Tab into a message: actions appear on focus-within.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: no max-width tweens remain; tests green.

## Result

- 2026-09-02 (re-anchored): the cluster sits INSIDE the visible glass `.desktop-chat-interactions` pill, which is in-flow at rest whenever a message has reactions — reserving the cluster width per the literal Target would leave a wide empty glass stub beside the chips, and the fallback (absolute against the row edge) would break the ratified single-pill reveal (`frontend_chat_sports_car_test.go` "same pill"). So both rule pairs (`#chatTool .desktop-chat-actions`, `#chatTool .chat-context-card__message-actions`) now reveal with opacity only on `--dur-fast`, and the `max-width` switch is a discrete `0s` step delayed by `--dur-fast` on the way out (`transition-property: opacity, max-width; transition-duration: var(--dur-fast), 0s; transition-delay: 0s, var(--dur-fast)`; hover/focus-within/show-actions rules add `transition-delay: 0s` so the width expands at once, then fades in) — the codebase's `visibility 0s linear` recipe; no frame ever relayouts. For the invisible-at-rest pill (`[data-has-reactions="false"]`, the common case) the cluster stays fully laid out (`max-width: 360px`/`280px; overflow: visible`) — the plan's exact target, pure opacity, zero layout. No markup change; no pin quoted the old `transition-property: max-width, opacity` (→ 0). Gate `TestIndexChat|DesktopChat|TestPolishWave10` green. Playwright (sandbox :3171, computed styles): rest `opacity 0`, `transition 0.12s, 0s`, delays `0s, 0.12s`, cluster `max-width 360px` with no reactions; hover `opacity 1`, delay `0s`; reduced motion → all `0s`.
