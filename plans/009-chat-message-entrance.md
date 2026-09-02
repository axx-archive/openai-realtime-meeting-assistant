# 009 — Chat message entrance: one composited fade-rise at token speed, on every breakpoint

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: HIGH
- **Category**: Purpose & frequency / Easing & duration / Performance
- **Estimated scope**: 1 file (`index.html`): 1 keyframe, 1 rule, 1 JS gate

## Problem

Every appended chat message — own sends on Enter and every inbound message, the app's most repeated entrance — runs 360 ms with spring overshoot, a 14 px rise, a 0.985 scale and a 4 px `filter: blur()`; the blur forces the whole message subtree into a separate filter pass for the entire entrance. On phones the entrance never runs at all (the rule sits inside `@media (min-width: 861px)` and `scoutChatMessageAppendNarrowPatch` returns early unless the desktop layout matches).

```css
/* index.html ~L20910 — current */
#chatTool .scout-chat-msg.is-entering { animation: chat-msg-in var(--dur-slow) var(--ease-spring) both; }
@keyframes chat-msg-in {
  from { opacity: 0; transform: translateY(14px) scale(0.985); filter: blur(4px); }
  to   { opacity: 1; transform: none; filter: none; }
}
```

The room's equivalent (`assistant-message-in`) was already cut to opacity-only at `--dur-med` by plan 001; the busier desktop chat is now the slower surface.

## Target

```css
/* target — same selector, same keyframe name */
#chatTool .scout-chat-msg.is-entering { animation: chat-msg-in var(--dur-med) var(--ease) both; }
@keyframes chat-msg-in {
  from { opacity: 0; transform: translateY(6px); }
  to   { opacity: 1; transform: none; }
}
```
No blur, no scale, no spring. The rule moves OUT of the `min-width: 861px` block so phones get the same entrance, and the JS that adds `is-entering` (~L80024, `entering.classList.add('is-entering')`, already gated on `reducedMotion.matches`) runs on every breakpoint — remove the `desktopChatLayoutQuery.matches` early-return for the entrance class only (keep it for anything else that function does for the desktop layout).

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. Edit the keyframe `chat-msg-in`: replace the `from` block with `opacity: 0; transform: translateY(6px);` and the `to` block with `opacity: 1; transform: none;`.
2. Edit the `.scout-chat-msg.is-entering` rule: `var(--dur-slow) var(--ease-spring)` → `var(--dur-med) var(--ease)`.
3. Move that rule and its keyframe out of the `@media (min-width: 861px)` block to the nearest top-level `#chatTool` block (same specificity).
4. In the append path (~L80024), make the `is-entering` add apply regardless of `desktopChatLayoutQuery.matches`; keep the existing `reducedMotion.matches` gate.
5. Grep `chat-msg-in` and `is-entering` in `frontend_*_test.go`; update any pin that quotes the old duration/blur with the reason "plan 009: token-speed composited entrance".

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndexChat|DesktopChat|ConversationsWave1|TestPolishWave10' .` green.
- **Feel check**: restart the sandbox, then:
  - Send three messages quickly in a private thread: each rises 6 px and fades in over ~220 ms with no bounce and no blur; the composer never waits.
  - At 390×844 (phone) the same entrance plays.
  - In DevTools Layers, the message subtree is not promoted to a filter layer during the entrance.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: the keyframe has no `filter` and no `scale`, the rule uses `--dur-med`/`--ease`, phones animate, tests green.

## Result

- 2026-09-02: `chat-msg-in` keyframe now `opacity: 0; transform: translateY(6px)` → `opacity: 1; transform: none` (no blur, no scale); `#chatTool .scout-chat-msg.is-entering { animation: chat-msg-in var(--dur-med) var(--ease) both; }`; rule + keyframe moved out of `@media (min-width: 861px)` to the top-level `#chatTool` block just before it (same specificity). JS re-anchor: the plan's `scoutChatMessageAppendNarrowPatch` no longer exists — the `is-entering` add lives in `appendActiveScoutChatMessage`, whose desktop gate is structural (narrow layouts rebuild the feed with inline replies via `renderActiveScoutThread()` instead of appending), so instead of removing that gate the narrow fallback in `commitMessageProjection` now tags the freshly rendered `[data-message-id]` node with `is-entering` (same `reducedMotion` gate, same `animationend` cleanup; no-op if the node is absent). Pin re-pinned (reason "plan 009: token-speed composited entrance"): `frontend_polish_wave10_test.go`. Gate `TestIndexChat|DesktopChat|ConversationsWave1|TestPolishWave10` (run as part of the combined 013/009 gate) green.
