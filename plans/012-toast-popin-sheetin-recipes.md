# 012 — Toasts, pop-ins and sheets: one recipe each, on tokens

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: HIGH
- **Category**: Easing & duration / Cohesion & tokens
- **Estimated scope**: 1 file: ~12 declarations

## Problem

The toast enters on `--dur-slow` with spring (360 ms, over the UI budget) and exits on a hand-typed `220ms`; `bf-popin` runs at three durations (360/400/450 ms); the same `bf-sheetin` keyframe runs in four recipes (2 durations × 2 easings) across Drive details, the previewer, room sheets and the mobile bottom sheet. Sheets are one spatial idea; in a crisp productivity tool they should arrive at one speed with no bounce.

```css
/* index.html — current */
.toast { animation: bf-popin var(--dur-slow) var(--ease-spring); }          /* ~L5718 */
.toast.is-leaving { animation: toast-out 220ms var(--ease) forwards; }       /* ~L5741 */
… { animation: bf-popin 0.4s var(--ease-spring); }                            /* ~L5784 */
… { animation: bf-popin 0.45s var(--ease-spring) both; }                      /* ~L27911 */
… { animation: bf-sheetin var(--dur-slow) var(--ease-spring) both; }          /* ~L5996, ~L31728 */
… { animation: bf-sheetin var(--dur-med) var(--ease) both; }                  /* ~L23733, ~L24116 */
```

## Target

- Toast enter: `animation: bf-popin var(--dur-med) var(--ease);` — exit: `animation: toast-out var(--dur-med) var(--ease) forwards;`
- Every other `bf-popin` user: `var(--dur-med) var(--ease)` (keep `both` where present).
- Every `bf-sheetin` user: `animation: bf-sheetin var(--dur-slow) var(--ease) both;` (no spring; `--dur-slow` is the panel/sheet rung).
- `bf-popin` keyframe itself: keep its translateY(16px)→0 + opacity; if it carries a scale below 0.96, raise it to 0.96.

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. `grep -n "bf-popin" index.html` — normalise every `animation:` to `var(--dur-med) var(--ease)`.
2. `grep -n "toast-out" index.html` — the exit becomes `var(--dur-med) var(--ease) forwards`; if the reduced-motion block has a separate `animation-duration: 1ms` override for `.toast.is-leaving`, leave it.
3. `grep -n "bf-sheetin" index.html` — normalise every `animation:` to `var(--dur-slow) var(--ease) both`.
4. Check `frontend_*_test.go` for pins quoting `bf-popin 0.4s`, `toast-out 220ms`, or `bf-sheetin var(--dur-med)`; update with reason "plan 012: one recipe per surface".

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|Toast|Drive|Previewer' .` green; `grep -cE 'bf-popin (0\.4s|0\.45s|var\(--dur-slow\))' index.html` → 0; `grep -cE 'bf-sheetin var\(--dur-(med|slow)\) var\(--ease-spring\)' index.html` → 0.
- **Feel check**: restart the sandbox, then:
  - Trigger a toast (star a file): it rises ~16 px and lands in ~220 ms with no overshoot; dismissal takes the same time.
  - Open Drive details, then the previewer, then a room sheet: all three arrive with the same 360 ms glide, none bounce.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: one recipe per keyframe, tests green.

## Result

- 2026-09-02: `bf-popin` → `var(--dur-med) var(--ease)` on `.toast`, `.update-banner`, `.pip-meeting` (keeps `both`) and the mobile bottom sheet (was `--dur-slow`/spring ×2, `0.4s`, `0.45s`); `toast-out 220ms` → `var(--dur-med)`; `bf-popin` keyframe `scale(0.94)` → `scale(0.96)`; every `bf-sheetin` user → `var(--dur-slow) var(--ease) both` (was spring ×2, `--dur-med` ×3 incl. `.drive-details.is-open`, one without `both`). The reduced-motion `.toast.is-leaving { animation-duration: 1ms }` cover left in place. Pin re-pinned (reason "plan 012: one recipe per surface"): `frontend_design_system_v2_test.go` D6 toast recipe. Gate `TestIndex|TestPolishWave10|Toast|Drive|Previewer` green; `bf-popin (0.4s|0.45s|--dur-slow)` → 0, `bf-sheetin … --ease-spring` → 0.
