# Board doctrine v2 — build | fix | workflow | business

Status: shipped 2026-07-06 (backlog-knockout wave, card doctrine-v2).

## Why this exists

The 2026-07-06 board triage discarded nine business/process cards because the
old category enum (`product | process | business`) gave them nowhere useful to
live: they cluttered the eng lanes until someone quietly cut them. The wave
goal is explicit — *business items are not cut without debate; they stay
captured, distinct, and owned*. Doctrine v2 makes capture the default and the
draft review the debate.

## The four categories

Every card the board worker creates carries exactly one category tag plus
topical tags:

- **build** — something new that gets BUILT (features, systems, assets).
- **fix** — something broken that gets FIXED (bugs, regressions, debt with a
  defect attached).
- **workflow** — a workflow that gets RUN: research, decks, design,
  pressure-tests, written artifacts. A workflow card must name its
  deliverable — the exact artifact title — in its notes, so a finished agent
  thread binds to the card (linkage matches by `card_id` or fuzzy title,
  threshold 0.6) and advances it.
- **business** — commercial/ops items that are not eng work but must not be
  silently dropped: deals, invoices, leases, hiring, vendor decisions.

The prompt lives in `meetingBoardInstructions()` (`board_worker.go`).

## Business-card rules (enforced, not advisory)

`validateMeetingBoardCreateDoctrine` (`board_worker.go`) gates the worker's
`create_ticket` seam:

1. **Named owner.** `owner` must be non-empty and not `Unassigned`. This is a
   presence check only — a non-participant name still counts as owned; we do
   not require a canonical-participant match.
2. **Concrete next step.** `notes` must survive `cleanBoardNotes` non-empty
   and state the concrete next step.
3. **Always a draft.** The worker's D4 draft force applies to every worker
   create, so a business card always lands as a pending draft. Accepting or
   dismissing that draft **is** the debate — nothing is cut without a human
   touching it.

Violations do not crash the pass: the operation is rejected into the
per-operation error rail of the board-update artifact
(`renderMeetingBoardUpdateArtifact`), so the audit trail shows what the worker
tried and why it was refused. Human creates through `createTicket`
(`kanban.go`) are untouched.

## Board UI — the Business track

Business-tagged cards do not render in the four eng lanes. `renderBoard`
(`index.html`) splits them into a collapsed `<details id="businessTrack">`
rail under the columns:

- Collapsed by default; the summary reads `Business track · N`.
- Open state survives re-renders via the module-level `businessTrackOpen`
  flag (the board re-renders on every board event, ~2-minute worker cadence).
- Cards render through `renderCard`, so business drafts keep their
  accept/dismiss buttons and click-to-detail in the rail.
- The left-rail preview (`renderBoardPreview`) also excludes business cards,
  so it mirrors the clean lanes.

## Linkage and card advancement

Workflow deliverable naming is prompt-level; no new linkage code. Linkage
already binds a finished agent thread to a card by `card_id` or fuzzy title
(`matchBoardCard`, `linkage.go`) and advances complete → **In Progress**, not
Done — humans judge Done (the 066 false-Done incident is why).

## Prompt-pinned invariants (do not break)

Three phrases in `meetingBoardInstructions()` are pinned byte-for-byte by
tests. Any future prompt edit must keep them intact:

| Phrase | Pinned by |
| --- | --- |
| `never auto-run` | `codex_proposals_test.go` (TestProposeCodexTaskToolCreatesProposalAndNotifiesEveryone) |
| `read-only` | `codex_proposals_test.go` (same test) |
| `pass its card_id if known, otherwise reuse the card's exact title` | `linkage_test.go` (card_id binding rule) |

`board_doctrine_test.go` re-pins all three alongside the v2 doctrine phrases
so a doctrine rewrite can never silently drop them.

## Legacy data

No migration. Old `product`/`process` tags remain as topical tags and those
cards stay in the eng lanes. Only a literal `business` tag (case-insensitive)
routes a card to the Business track — some already-persisted business-tagged
cards visibly relocate on deploy, which is the intent.
