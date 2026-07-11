# Animation improvement plans — 2026-07-11 audit

Produced by the `improve-animations` audit (commit `bd289db`). Full audit: 4 parallel category agents over the 8-category playbook; every finding re-verified at its file:line by the orchestrator before planning. The client's motion foundation is strong (single token system, no `ease-in`, no `scale(0)`, only one weak `ease`, comprehensive reduced-motion block with 3 gaps) — these plans are targeted corrections, not a redesign.

## Plans

| # | Title | Severity | Status |
|---|---|---|---|
| 001 | Cut tool-switch and message entrances to token speed | HIGH | DONE |
| 002 | Command palette: tiles render instantly | HIGH | DONE |
| 003 | Board video tiles: composite-only speaker/hover emphasis | HIGH | DONE |
| 004 | Gate hover motion behind (hover:hover); finish reduced-motion coverage | MEDIUM | DONE |
| 005 | Motion token hygiene: fold stray durations/easings into tokens | MEDIUM | DONE |

Gate evidence (2026-07-11): full `go test -count=1 .` → ok (95.8s); keyless browser smoke on :3171 → 9/9 PASS (tab-in 0.22s, palette animation-none + error-free filtering, tile transition width-free + scale(1.23) speaker, reduced-motion research dots gated, body 0.36s, zero JS errors).

## Execution order & dependencies

Run **strictly sequentially, 001 → 005** — all five edit the same file (`index.html`); parallel execution will conflict.

- 001, 002, 003: independent of each other, but run in order anyway (single file).
- 004 depends on 003: it wraps the `.board-video-tile:hover` rule **as rewritten by 003** (read it fresh; split `:focus-visible` out of the media query).
- 005 runs last: it touches many scattered lines and would otherwise churn context under the other plans. It must pin-check every edit against `*_test.go` first.

Gate for every plan: `go test -count=1 -run 'TestIndex' .` green before moving on.

## Deferred findings (audited, vetted, not in this wave)

| Finding | Category | Why deferred |
|---|---|---|
| Meter/progress `width` transitions → `scaleX()` (voice meter ≈4517 `width 80ms linear`, `--agent-progress` driver ≈54040, research bars ≈29647, audio meter ≈25349) | Performance | 8 sites + JS drivers + a pinned string in `frontend_noise_suppression_test.go:172`; needs its own careful pass |
| Toast enter/leave as transitions + FLIP stack shift (≈3737, 3759) | Interruptibility | Medium complexity; toasts are occasional-frequency |
| Video-tile drag drop: FLIP + spring settle (≈31893) | Interruptibility | Touches live-room drag code; too risky to batch |
| PiP release: velocity corner-snap with `--ease-spring` (≈31462) | Interruptibility | Feature work, not a fix |
| Room exit choreography (mirror of `rise-in`) | Missed opportunity | Requires JS wiring in the leave path — must never delay teardown |
| Anchored popovers scale-in from trigger (invite-pop ≈15142, account/board/files menus) | Physicality | `@starting-style` support decision needed |
| Goalcard node tooltip: fade → scale-from-anchor (≈19849) | Physicality | Low leverage, niche surface |
| Kanban card mount stagger (≈15811) | Missed opportunity | Polish; board rides panel entrance today |

## Explicitly rejected (by-design, do not re-report)

- Boot mount cascade (`mount-rise` 520ms) and room-entry `rise-in` 480ms choreography — deliberate one-shot cinematics, comment-documented, test-adjacent.
- Board-expanded `animation: none !important` kill-switch (≈5386) — documented deliberate ("the expanded board keeps its animation kill-switch").
- `--ease-spring` bounce on popovers/PiP/goal-advance — sanctioned earned delight.
- Modal center origins — correct for centered surfaces.
