# 016 — Proposal cards: entrance only for new cards, not on every re-render

- **Status**: DONE
- **Commit**: 5ef9cece
- **Severity**: MEDIUM
- **Category**: Interruptibility
- **Estimated scope**: 1 file: 1 rule + 1 render function

## Problem

`renderCodexProposals()` (~L75178) does `proposalDeckList.replaceChildren(...visible.map(codexProposalNode))` on every websocket upsert, every 30 s dock timer and every focus call, and `.proposal-card { animation: bf-islandin var(--dur-med) var(--ease) }` (~L23618) replays the 20 px rise + scale on every surviving card each time.

## Target

The entrance plays once per card id. Pattern (the one plan 002 used for palette tiles): the renderer tracks `seenProposalIds` (module-level `Set`, `let` not `const` is fine — it is not read during boot); a card whose id was already seen is created without the entrance class; new ids get `is-entering` which carries the animation, and the class is removed on `animationend`.

```css
/* target */
.proposal-card.is-entering { animation: bf-islandin var(--dur-med) var(--ease); }
```

## Repo conventions to follow

- Motion tokens live at the top of `index.html` (`:root`, ~L444-463): `--ease: cubic-bezier(0.32, 0.72, 0, 1)` (default), `--ease-spring: cubic-bezier(0.34, 1.25, 0.5, 1)` (reserved for menu scale-in and genuine delight), `--dur-fast: 120ms` (hover, press, keystroke feedback), `--dur-med: 220ms` (fades, toggles, toasts, menus), `--dur-slow: 360ms` (panels, sheets), `--press-scale: 0.97`. Never type a raw ms/curve — extend the tokens if a new rung is truly needed.
- Reduced motion: the three `--dur-*` tokens are zeroed at `:root` under `@media (prefers-reduced-motion: reduce)` (~L580), so token-timed motion self-heals. Anything NOT token-timed must be listed in the nearest reduced-motion block. JS motion gates on `reducedMotion.matches` (module-level `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')`).
- Exemplar of a reversible open/close surface: `.bf-menu` (~L799-820): `transition-property: opacity, transform, display; transition-behavior: allow-discrete; transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med); transition-timing-function: var(--ease), var(--ease-spring), var(--ease);` with `.bf-menu[hidden] { opacity: 0; transform: translateY(-4px) scale(0.96); }` and `@starting-style { .bf-menu:not([hidden]) { opacity: 0; transform: translateY(-4px) scale(0.96); } }`.
- Exemplar of a composited meter: `.bf-wave-bar` + `@keyframes bf-wave` use `transform: scaleY()`; plan 006 converted fills to `translateX(-100%)` with `will-change: transform` only while animating.
- Tests: static pins live in `frontend_*_test.go` (grep the literal you change before editing — update a pin only when the change is the point of the plan, with a one-line reason). Run `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` after each plan. `index.html` is read ONCE at boot — restart the sandbox (see README) before a feel check.

## Steps

1. Change the CSS selector from `.proposal-card` to `.proposal-card.is-entering`.
2. In `renderCodexProposals()`, after building each node, add `is-entering` only when `!seenProposalIds.has(id)` and `!reducedMotion.matches`; add the id to the set; remove the class on `animationend` (once).
3. Reset `seenProposalIds` when the proposal deck is cleared for a new session (find where `proposalDeckList` is emptied on identity change and clear the set there).
4. Pin: a static test that `.proposal-card.is-entering` carries the animation and bare `.proposal-card` does not.

## Boundaries

- Do NOT touch `.tool-rail*`, `.topbar*`, `.pd1-primary-nav*` markup (rail geometry is owned by the shell work) except where a step names a rule inside them.
- Do NOT change markup/structure — motion properties only, unless a step says otherwise.
- Do NOT add dependencies; no `requestAnimationFrame` for must-run work.
- Do NOT edit the shared `bfMenu` component except in plan 017.
- If a step doesn't match the code you find (drift since the commit stamp — this file is edited concurrently), re-anchor by the quoted literal; if the literal is gone, STOP and report instead of improvising.
## Verification

- **Mechanical**: `go test -count=1 -timeout 20m -run 'TestIndex|Proposal|Codex' .` green.
- **Feel check**: restart the sandbox, then:
  - With two proposal cards visible, trigger a third via the sandbox socket (or wait for the 30 s timer): only the new card animates; the two survivors stay still.
  - Toggle `prefers-reduced-motion` (DevTools Rendering panel) and confirm movement drops while opacity feedback remains.
- **Done when**: survivors never replay; new cards animate once.

**Result (2026-09-02)**: `.proposal-card { animation: bf-islandin … }` → `.proposal-card.is-entering { animation: bf-islandin var(--dur-med) var(--ease); }`; `let seenProposalIds = new Set()` declared beside `let codexProposals = []` (boot-safe); `renderCodexProposals()` adds `is-entering` only for unseen ids and not under `reducedMotion.matches`, removing it on `animationend` (once); `handleCodexProposalsSnapshot` prunes the set to surviving ids (the replay is the deck's reset point — no separate identity-change clear exists); `scheduleCodexProposalRemoval` deletes the id. Reduced-motion cover re-targeted to `.proposal-card.is-entering`. New static pin `TestIndexProposalCardEntranceKeyedOnNewIds` in `codex_proposals_test.go`. Gate `TestIndex|Proposal|Codex` green (24.1s).
