# 018 — Composite-only sweep: PiP drag, voice bars, transfer panel, voice island, resize grip, receipt fill

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: HIGH
- **Category**: Performance
- **Estimated scope**: 1 file: 6 sites

## Problem

Six sites animate layout or paint properties, several over live video or a blurred layer:

1. PiP drag writes `pipMeeting.style.left/top` per `pointermove` (~L61461) on a fixed `.glass-sheet` holding `<video>`; each frame relayouts and re-rasterises the backdrop blur.
2. Voice bars: `.home-realtime-voice__bar { height: var(--wave-height…); transition: height 90ms linear }` (~L8610) written per frame from `driveStrideSignal` (~L59299) — a layout tween retargeted 60×/s.
3. `.room-transfer__panel` is `.glass-sheet` AND transitions `filter: blur(4px) → 0` (~L28472-28488) — double blur over the room.
4. `.voice-island[data-state="hand-raised"] { animation: voice-island-consent 1.8s … infinite }` animates `box-shadow` on a backdrop-filtered fixed island (~L9729-9744).
5. `.content-studio-drawer__resize::after` transitions `width, height` on hover/focus AND during `.is-resizing` (~L37445-37467).
6. `.scout-studio-receipt__progress-fill { transition: width var(--dur-med) var(--ease) }` (~L14977-14983), fed by `style.width = %` (~L79679) — the meter plan 006 missed.

## Target

1. PiP: `transform: translate3d(x, y, 0)`; add `will-change: transform` on pointerdown, remove on pointerup; final position may be committed to left/top on release.
2. Voice bars: `transform: scaleY(var(--wave-level))` with `transform-origin: bottom`, no `height` transition (the rAF loop already updates per frame; `transition: transform 90ms linear` is fine).
3. Transfer panel: remove `filter` from the transition and the blur keyframes; entrance = `opacity + translateY(12px)` on `var(--dur-med) var(--ease)`.
4. Voice island: the consent ring becomes a `::after` pseudo-element with the ring pre-rendered (`box-shadow` static) whose `opacity` animates `0 → 1 → 0` over 1.8 s; still gated in the reduced-motion block.
5. Resize grip: `transform: scaleX(1.25)`/`scaleY(1.33)` from `transform-origin: center` on hover/focus; no transition while `.is-resizing`.
6. Receipt fill: `transform: translateX(calc(-100% + var(--progress)))` pattern from plan 006 (`translateX(-100%)` honest zero), `transition: transform var(--dur-med) var(--ease)`, updated in place (not rebuilt) so it actually plays.

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1–6. Apply each Target; for JS sites (PiP drag, voice bars, receipt fill) keep the existing event wiring and only change the properties written.
7. Grep pins (`transition: height 90ms linear`, `transition: width var(--dur-med) var(--ease)`) and update with reason "plan 018: composite-only".

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|Pip|Voice|Room|Studio|TestPolishWave10' .` green; `grep -c 'transition: height 90ms linear' index.html` → 0; `grep -c 'transition: width var(--dur-med) var(--ease)' index.html` → 0.
- **Feel check**: restart the sandbox, then:
  - Drag the PiP over a live tile with DevTools Performance recording: no Layout entries during the drag; frames stay ≤16 ms.
  - Speak into the composer mic: bars scale from the bottom with no relayout flashes.
  - Raise a hand in a room: the island ring pulses without repainting the blur (Layers panel shows one composited layer).
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: all six sites animate transform/opacity only; tests green.

**Result (2026-09-02)**: (1) PiP drag rides the individual `translate` property per pointermove with `will-change: translate` for the gesture, starting box pinned to left/top once and the final spot committed to left/top on release — `translate` rather than `transform` because `.pip-meeting`'s `bf-popin … both` fill owns `transform`. (2) `.home-realtime-voice__bar` → `transform: scaleY(var(--wave-level, 1))` + `transition: transform 90ms linear`, `--wave-level` = clamp(rest+live·9, 22)/rest written by `updateStrideCradle`, cleared in `restoreStrideSignalIdlePath`; `transform-origin: center` kept (the flex row centres the bars — a bottom origin would break the centred rest shape; plan said bottom). (3) `.room-transfer__panel` filter tween removed. (4) hand-raised ring → `.voice-island[data-state="hand-raised"]::after` with a static box-shadow and an opacity-only `voice-island-consent` loop; reduced-motion cover re-targeted. (5) `.content-studio-drawer__resize::after` → `transform: scale(1.25, 1.333)`, `transition-property: transform, opacity, background-color`, `transition: none` while `.is-resizing`. (6) `.scout-studio-receipt__progress-fill` → `translateX(calc(-100% + var(--progress, 0%)))`, `--progress` written by `scoutStudioReceiptNode`, which carries the previously-drawn value (`var scoutStudioReceiptProgressShown`, lazy Map) and lands the new one a macrotask later so the transition plays across the feed rebuild. Greps: `transition: height 90ms linear` → 0, `transition: width var(--dur-med) var(--ease)` → 0. No pins re-pinned. Gate `TestIndex|Pip|Voice|Room|Studio|TestPolishWave10`: all green except 3 failures owned by concurrent token work (dark ramp `--surface-1: #050506` → `#0E0E10` etc. by another lane; `TestPolishWave10LightRampAndButtonHierarchy`, `TestIndexDarkThemeUsesNativeParityBlackCanvas`, `TestDocumentStudioRenderedNestedListsAndSafeImagesRoundTrip`) — verified: those literals exist at HEAD and were removed by an edit outside this plan.
