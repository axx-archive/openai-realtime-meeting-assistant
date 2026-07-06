# Memory-store split assessment — Wave 5 (2026-07-05)

VERDICT: DEFER — with evidence and named re-open triggers. The packaging-OS analysis predicted defer ("memory-store split if signal volume demands it", item 21); Waves 1-4 confirmed the prediction and, critically, shipped the compaction that makes it stick.

## The mechanics, measured (memory.go / signals.go)

- One JSONL store (`data/meeting-memory.jsonl`), held whole in RAM (`store.entries`), loaded once at boot.
- New entries are cheap: `appendEntry` opens O_APPEND and writes one line (memory.go:787) — signals never trigger a rewrite at capture time.
- Any metadata or body UPDATE rewrites the entire file (`rewriteLocked`, memory.go:807 — temp file + rename, every entry re-encoded). Rewrite triggers in practice: artifact body edits/versions, `openedAt` and render-status stamps, relevance changes, quarantine expiry deletes, and the Taste Analyst's `distilledInto` stamp (deliberately ONE batched rewrite per distillation window, taste_analyst.go:309).
- The blob store (Wave 3, blobs.go) already took the byte weight out: PDFs, page rasters, superseded artifact bodies, and now Wave 5's generated imagery live in `data/blobs` as refs. The split question is therefore purely signal-entry COUNT, not payload bytes.

## Signal volume at 6 users, honestly

Capture seams live today (grep `recordSignalEvent`: 18 call sites — Waves 1-4 added goal_lessons, goal_cancelled, grill_delta, pdf_exported, share_opened, deal-room opens, and scout-chat seams beyond the original artifact/proposal set):

- Implicit seams fire once per human reaction; opens are FIRST-open only by design (signals.go:318-325), surveys are hard-capped at 1/user/UTC-day, deduped per package stage, and suppressed at implicit volume >= 3.
- Working estimate: 10-20 goal/tool runs studio-wide per active day (each landing 1-3 completion-adjacent signals) plus 20-40 artifact interactions => roughly 40-80 signals/day, ~250-500/week.
- Each line is small by construction: payload values truncated at 500 bytes, survey notes at 300 => a typical record is 300-600 bytes, so ~100-300 KB of signal lines per week BEFORE compaction.
- Compaction (shipped with the analyst, as the analysis demanded): consumed signals are stamped `distilledInto` and `sweepDistilledSignals` (slop_classifier.go:173) hard-deletes them after the 30-day reprieve. Steady state is a rolling ~4-8 week window: roughly 1,500-4,000 signal lines, 0.5-2 MB — bounded, not compounding.
- Ground truth today: the local store is 2 lines / 2.9 KB; the VPS store post-OPS-3 is the one to watch.

## Why not split now

1. The cost that motivated the split — whole-file rewrites — scales with TOTAL store size, and the steady state is single-digit MB. A 1-5 MB atomic rewrite is milliseconds, invisible next to the model calls around it.
2. Signals are already logically isolated: excluded from search, recall, snapshots, and model context (`visibleEntriesLocked`, memory.go:869-878), with metadata mirrors so distillers filter without parsing JSON. A physical split buys zero correctness — only rewrite amortization we do not yet need.
3. A split has real cost today: a second file to fsync/back up/rsync in the VPS deploy, a second seen-map and loader path, cross-store integrity for `artifactId` back-references, and new test surface — against a six-person studio's write rate.

## Re-open triggers (evidence, not vibes)

Split `kind=signal` into its own append-only file when ANY of these holds:

1. `meeting-memory.jsonl` exceeds 64 MB or ~100k lines (boot load + rewrite cost become real);
2. signal entries exceed half of all lines DESPITE compaction (compaction is losing the race);
3. observed `rewriteLocked` p95 exceeds 250 ms on the VPS — add the one-line duration log first, it is the cheapest instrumentation and should land before any split;
4. a distiller needs signal history beyond the 30-day reprieve (the sweep and a reader start fighting over the same lines).

The cheap shape at trigger time is the routing split, not a schema migration: `appendEntry` routes `kind=signal` to `data/signals.jsonl`, the boot loader merges both, and search/recall change nothing because signals never entered them.

Wave 5's own additions add no pressure: an imagery board files ONE artifact per run and its images are blobs.
