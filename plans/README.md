# Animation improvement plans — 2026-07-11 audit

Produced by the `improve-animations` audit (commit `bd289db`); wave 2 reconciled and planned at `d8846df`. Full audit: 4 parallel category agents over the 8-category playbook; every finding re-verified at its file:line by the orchestrator before planning. The client's motion foundation is strong (single token system, no `ease-in`, no `scale(0)`, only one weak `ease`, comprehensive reduced-motion block with 3 gaps) — these plans are targeted corrections, not a redesign.

## Plans

| # | Title | Severity | Status |
|---|---|---|---|
| 001 | Cut tool-switch and message entrances to token speed | HIGH | DONE |
| 002 | Command palette: tiles render instantly | HIGH | DONE |
| 003 | Board video tiles: composite-only speaker/hover emphasis | HIGH | DONE |
| 004 | Gate hover motion behind (hover:hover); finish reduced-motion coverage | MEDIUM | DONE |
| 005 | Motion token hygiene: fold stray durations/easings into tokens | MEDIUM | DONE |
| 006 | Meter and progress fills: animate transform, not width | HIGH | DONE |
| 007 | Anchored popovers scale in from their trigger | MEDIUM | DONE |
| 008 | Correct three --dur-slow drift regressions; un-fight the tile drag | MEDIUM | DONE |

Wave-1 gate evidence (2026-07-11): full `go test -count=1 .` → ok (95.8s); keyless browser smoke on :3171 → 9/9 PASS (tab-in 0.22s, palette animation-none + error-free filtering, tile transition width-free + scale(1.23) speaker, reduced-motion research dots gated, body 0.36s, zero JS errors).

Wave-2 reconcile (2026-07-11, HEAD `d8846df`): all five wave-1 plans verified intact at HEAD by a dedicated agent (evidence per plan on file). Drift scan over `bd289db..HEAD` found three duration regressions from 005's own tokenization (rounded 0.3s UP to `--dur-slow`) → plan 008.

Wave-2 gate evidence (2026-07-11): per-plan `TestIndex` green after each of 006/007/008; full `go test -count=1 .` → ok (99.7s); keyless browser smoke on :3171 → 12/12 PASS (login, account-menu origin+transition, intel fill transform-only with honest-zero `translateX(-100%)`, research fill resting state, files-menu transition, tab pane token-timed, reduced-motion token zeroing 0ms×3, dragging tile transition excludes transform, zero console errors). Adversarial diff review: 1 critical caught (research-bar template sites still wrote attribute-form `width` — fixed, addendum in plan 006) + 1 stale comment (fixed); re-gate green.

## Execution order & dependencies

Run **strictly sequentially, 006 → 007 → 008** — all plans edit the same file (`index.html`); parallel execution will conflict. The three are otherwise independent.

Gate for every plan: `go test -count=1 -run 'TestIndex' .` green before moving on.

## Deferred findings (audited, vetted, not yet planned)

| Finding | Category | Why deferred |
|---|---|---|
| Video-tile drag drop: FLIP + spring settle (≈31893) | Interruptibility | Touches live-room drag code; too risky to batch (008 fixes the drag-lag part only) |
| PiP release: velocity corner-snap with `--ease-spring` (≈31462) | Interruptibility | Feature work, not a fix |
| Room exit choreography (mirror of `rise-in`) | Missed opportunity | Requires JS wiring in the leave path — must never delay teardown |
| Goalcard node tooltip: fade → scale-from-anchor (≈19849 at bd289db) | Physicality | Low leverage, niche surface |

## Retired findings (wave-2 reconcile, 2026-07-11 — re-vetted at HEAD and withdrawn)

- **Toast transitions + FLIP stack shift** — stale: toasts follow a one-pill-at-a-time design canon (`while (toastRegion.children.length > 1) … remove()`, comment-documented ≈52262); there is no stack to FLIP. Enter/leave keyframes run on fresh elements (no retrigger-restart risk) and the reduced-motion block handles both states, including the `animation-duration: 1ms` trick so `animationend` removal still fires. By-design; do not re-report.
- **Kanban card mount stagger** — withdrawn: the board rail rebuilds every card on each websocket board update (`replaceChildren`-style render ≈51156), so a mount stagger would replay on every update — the same replay bug class plan 002 removed from the palette. The `is-fresh` / `is-moved` treatments already carry change feedback.
- **Meter width → transform** and **anchored popovers** — promoted to plans 006 and 007 (translateX pattern chosen over scaleX to preserve rounded fill caps; `@starting-style` chosen with instant-appear degradation on older engines).

## Explicitly rejected (by-design, do not re-report)

- Boot mount cascade (`mount-rise` 520ms) and room-entry `rise-in` 480ms choreography — deliberate one-shot cinematics, comment-documented, test-adjacent.
- Board-expanded `animation: none !important` kill-switch (≈5386) — documented deliberate ("the expanded board keeps its animation kill-switch").
- `--ease-spring` bounce on popovers/PiP/goal-advance — sanctioned earned delight.
- Modal center origins — correct for centered surfaces.

# Wave 10 motion pass — 2026-09-02 audit (commit `5ef9cece`)

Produced by `improve-animations` at AJ's request to roll the motion pass INTO Wave 10 ("light/snappy/responsive/modern; consistency end-to-end"). Four parallel category agents (easing+cohesion; purpose+interruptibility; physicality+performance; accessibility+opportunities) over the uncommitted batch tree; the orchestrator re-vetted every quoted literal against the file before planning (two literals re-anchored by grep). The foundation from the July waves held (no `transition: all`, no `ease-in`, no `scale(0)`); the batch itself introduced most of the drift below.

## Plans

| # | Title | Severity | Status |
|---|---|---|---|
| 009 | Chat message entrance: one composited fade-rise at token speed, on every breakpoint | HIGH | DONE |
| 010 | Tab switch: remove the 360 ms icon bounce from both navs | HIGH | DONE |
| 011 | One press depth everywhere: `--press-scale` replaces six hand-typed scales | HIGH | DONE |
| 012 | Toasts, pop-ins and sheets: one recipe each, on tokens | HIGH | DONE |
| 013 | Hover and keystroke feedback on `--dur-fast`; fold the token residue | HIGH | DONE |
| 014 | Message hover actions: opacity reveal, no layout tween | HIGH | DONE |
| 015 | Five open/close surfaces move from one-way keyframes to reversible transitions | MEDIUM | DONE |
| 016 | Proposal cards: entrance only for new cards | MEDIUM | DONE |
| 017 | bfMenu: trigger-anchored transform-origin on every branch | MEDIUM | DONE |
| 018 | Composite-only sweep: PiP drag, voice bars, transfer panel, voice island, resize grip, receipt fill | HIGH | DONE |
| 019 | Reduced motion: close the remaining gaps | MEDIUM | DONE |
| 020 | Continuity moments: thread-list FLIP, work-step transitions, theme crossfade, previewer direction, move attribution | MEDIUM | DONE |

## Execution order & dependencies

Two lanes may run in parallel because they touch disjoint regions of `index.html` (both edit surgically, never rewrite): **Lane A (feel)** 010 → 011 → 012 → 013 → 009 → 014 (chat/rail/toasts/global tokens). **Lane B (mechanics)** 017 → 015 → 016 → 018 → 019 → 020 (bfMenu, panels, proposals, rooms/voice/studio performance, accessibility, continuity). 020 last in its lane (largest). Gate for every plan: `go test -count=1 -timeout 20m -run 'TestIndex|TestPolishWave10|DesignSystem' .` green before the next.

## Vetting notes

Refuted/retired during vetting: none of the 44 findings was by-design; two were narrowed (bfMenu origin already fixed on the fixed branch earlier today → 017 covers the adopted/non-fixed branches only; the reaction-row hand `aria-pressed` is a cleanup, handled by the rooms fix). Deliberately NOT planned: the boot `mount-rise`/`rise-in` cinematics and the board `is-fresh`/`is-moved` feedback (sanctioned), the toast one-pill canon (kept), `linear` on constant fills and `ease-in-out` on ambient alternate pulses (correct per AUDIT.md).

Wave 10 motion gate evidence (2026-09-02): all twelve plans DONE across two lanes (per-plan Result notes in each file); every plan gated on `TestIndex|TestPolishWave10|DesignSystem`; Playwright feel checks on :3171 (computed styles, `getAnimations()`, Performance entries) all pass with zero page errors; 015 left memory body / Drive details / room chat entrance-only because their hosts are emptied or hidden synchronously by JS (noted in-file as a follow-up).
