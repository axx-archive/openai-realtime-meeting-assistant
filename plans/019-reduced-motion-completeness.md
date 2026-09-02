# 019 — Reduced motion: close the remaining gaps (recording dots, transcription dot, smooth scrolls, GIFs, grill counter)

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: MEDIUM
- **Category**: Accessibility / Interruptibility
- **Estimated scope**: 1 file: ~10 sites

## Problem

- `--dur-breathe` is not zeroed under `reduce`, and `.btn--recording.is-recording .record-dot` / `.board-dock-icon.recording-toggle.is-recording .record-dot` (`animation: bf-breathe var(--dur-breathe) var(--ease) infinite`, ~L5239) sit in no reduced-motion block → recording indicators pulse forever.
- The live-transcription dot's source rule (`.room-transcription-pill[data-state="live"] .room-transcription-pill__dot`, specificity 0,3,0, ~L1714) beats the reduced-motion kill on bare `.room-transcription-pill__dot` (0,1,0, ~L36850).
- Five `scrollIntoView({behavior:'smooth'})` calls are ungated while five others use `reducedMotion?.matches ? 'auto' : 'smooth'` (re-anchor by grepping `behavior: 'smooth'` / `behavior:'smooth'`).
- GIF picker grid and inline GIF cards autoplay with no still frame under `reduce` and no pause affordance; `.scout-gif__item:hover { transform: scale(1.03) }` (~L19012) is outside the `(hover: hover) and (pointer: fine)` guard.
- `#chatTool .private-riff-thinking::before { animation: pulse 1.2s ease-in-out infinite }` (~L19367) references a keyframe that does not exist (dead today; an ungated loop the day someone defines it).
- `countUpGrillScore` (~L59951) runs a 600 ms rAF tween with no cancel handle (two calls fight) and no `reducedMotion` gate.

## Target

- Add `--dur-breathe: 0ms` to the `:root` reduced-motion zeroing block, OR list the two record-dot selectors in the nearest reduced-motion block with `animation: none` — do the first (one rule heals every breathe).
- Reduced-motion kill for the transcription dot uses the full selector (0,3,0) so it wins.
- Every `scrollIntoView` with `behavior: 'smooth'` uses `reducedMotion?.matches ? 'auto' : 'smooth'`.
- GIFs: under `reduce`, render the still (`gif.stillUrl` if the payload has one, else the first frame via a paused `<video>`/`<canvas>` snapshot is out of scope — use `stillUrl` when present and otherwise show the image with a "GIF · tap to play" overlay that swaps in the animated src on click); move `.scout-gif__item:hover` into the `(hover: hover) and (pointer: fine)` guard.
- Delete the dead `animation: pulse …` declaration (or define `pulse` as an opacity 0.4↔1 loop timed by `--dur-breathe` and list it in the reduced-motion block).
- `countUpGrillScore`: store the rAF id on the node (`node._grillRaf`), cancel any previous before starting, and under `reducedMotion.matches` set the final value immediately.

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1–6. Apply each Target; keep the existing JS gates' style (`reducedMotion?.matches`).
7. Pin: extend the reduced-motion pin test (the Wave 10 motion pin) to assert `--dur-breathe: 0ms` appears in the reduce block and that no `scrollIntoView` call has an ungated `'smooth'` (regex over the file).

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|ReducedMotion|Gif' .` green; `grep -cE "behavior: ?'smooth' ?\}" index.html` → 0 ungated (every hit must be inside the ternary).
- **Feel check**: restart the sandbox, then:
  - With reduced motion on: start a recording (sandbox) — the dot is static; open a room with live transcription — the dot is static; open the GIF picker — tiles are still until tapped.
  - Trigger a grill score twice quickly: the number settles once, on the last target.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: reduce zeroes every loop; scrolls are gated; GIFs have a still state; counter is cancellable.

**Result (2026-09-02)**: `--dur-breathe: 0ms` added to the `:root` reduce block (also zeroes `--pulse-cycle`, so `bf-think`/`goalcard-node-breathe` heal too); full-specificity kill `.room-transcription-pill[data-state="live"] .room-transcription-pill__dot { animation: none; }` + `#chatTool .private-riff-thinking::before { animation: none; }` added beside the pill-dot kills; the 5 ungated `scrollIntoView` calls now use `reducedMotion?.matches ? 'auto' : 'smooth'` (`intelLibrary` ×2, `card` ×2, `technicalWork`); GIF picker tiles load `gif.stillUrl` under reduce (tap still sends — the picker's one job); inline GIF link previews under reduce skip the eager `bfChatImage` and show a `desktop-chat-link-preview__gif-play` "GIF · tap to play" overlay that loads the frames on click (new CSS rule beside the `__visual` rule); `.scout-gif__item:hover` wrapped in `(hover: hover) and (pointer: fine)`; `pulse` defined as an opacity 0.4↔1 loop on `--dur-breathe` (was an undefined keyframe on a raw 1.2s); `countUpGrillScore` keeps `node._grillRaf`, cancels the running tween and lands the final value at once under reduce. Out of scope, noted: GIF *attachments* sent from the picker render via the chat image path and still animate under reduce. Pin extended (`TestPolishWave10MotionTokensAndReducedMotion`): `--dur-breathe: 0ms` in the reduce block + zero `behavior: 'smooth'` literals. Gate `TestIndex|TestPolishWave10|ReducedMotion|Gif` green; ungated-smooth grep → 0.
