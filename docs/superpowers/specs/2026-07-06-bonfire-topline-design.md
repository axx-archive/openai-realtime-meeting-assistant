# Bonfire top-line polish + capability wave — design

**Date:** 2026-07-06
**Author:** AJ + Scout (Claude)
**Status:** awaiting approval

## Goal (verbatim intent)

Make the Bonfire agentic OS hit top-tier marks as a workstation the team runs active
projects on. Five founder concerns, plus one mobile-composer polish item:

1. **Slick / uncrowded / no odd line-breaks** on desktop *and* mobile; elegant to use.
2. **Super clear + intuitive** to run each type of task.
3. **PDF deliverables download the whole document** (the StationTenn deck PDF was one page).
4. **Decks carry imagery used intelligently** — an art-direction agent decides where images
   earn an emotional beat (attract consensus / talent / capital) and where their absence is
   stronger; imagery is editorial judgment, not a forced default or a toggle.
5. **Feedback reaches the worker + a Google-Drive-style deliverables drawer**: select a
   deliverable, drop it into an existing/new public channel or private chat, give feedback,
   and work resumes on *that specific* deliverable — deep 1:1 linkage.
6. **Mobile composer**: kill the white border around the input; center the 📎 / + / ↑ icons.

Decisions locked with AJ: imagery is **art-directed** (an agent decides where it enhances the
story — no forced count, no toggle); deliverables/feedback **full build** (incl. cross-channel
drop); ship as **one gated push to main**.

## Non-goals

- No change to the `/packaging` Claude-Code skill's Midjourney path (orthogonal; skill edit).
- No redesign of the goal engine's stage model — we extend resume dispatch, not rewrite it.
- No new design-token system — we reconcile the chat UI's inline token fork toward the
  shipped design system only where it causes the visible defects below.

## Verified findings (recon + adversarial verify)

- **PDF (CONFIRMED blocker):** one deck→PDF path. `ship_deck` authors a presenter-mode deck
  (one slide visible on screen); `render_runner.go executeRenderExportPDF` prints via chromium
  `--print-to-pdf` with **no injected `@page`/print CSS** (`injectRenderPrintCSP` pins only a CSP
  meta, `render_runner.go:452-473`). Chromium paginates solely off the doc's own print CSS →
  presenter deck prints 1 slide → 1 page. Flattener (`jpeg_pdf.go`) and page-count
  (`goal_manifest.go:253` / `slide_jury.go:129`) are honest and self-heal once N pages print.
- **Imagery (CONFIRMED, premise corrected):** OpenAI image code (`createOpenAIImage` ←
  `runImageryBoard`) has **zero live callers**; `imagery_board` is deliberately unreachable
  (`tool_registry.go:474-490`). The 14-stage `packaging_studio` has **no imagery stage**
  (`packaging_studio.go:198-382`). Credits are irrelevant — nothing spends them.
- **UI polish:** run card sprawls full lane, breaks the 680px feed spine; 3 card families,
  3 alignments; over-chromed pills; `--type-label-sm` undefined; 15px fields → iOS zoom;
  uppercase-mono jargon labels; deliverable titles ellipsis-truncate.
- **Composer:** the "white border" is the global `:focus-visible` ring (`index.html:325`)
  painting the focused `<textarea>` (the pill already shows focus via `:focus-within`, `9024`);
  icons off-center from `.scout-chat-form{align-items:flex-end}` (`9013`).
- **Task discovery:** 14 run-types, one taxonomy source (`buildToolsPayload`), but discovery is
  gesture-gated (unlabeled +, no starter chips, voice has no enum).
- **Feedback:** one true resume seam exists (`scoutFollowUpTarget` → `followUpArtifactId` →
  `launchAgentThreadFollowUp`), gated two ways: server rejects artifacts not already referenced
  in the thread (`scout_chat_threads.go:396`), and hard-rejects source≠`scout_thread`
  (`agent_thread_followup.go:116`). "send notes" only prefills text (`index.html:34605`);
  manifest card is open-only; no drawer/drop exists.

## Architecture — six waves (dependency-ordered)

Each wave is TDD (repo has strong Go + `frontend_*_test.go` culture that parses `index.html`).
Each wave passes an adversarial gate against this goal before the next starts.

### Wave 1 — mobile composer polish (frontend only; ~2 edits)
- `.scout-chat-input:focus-visible { box-shadow: none; }` (outline already 0) — suppress the
  redundant ring; the `.scout-chat-form:focus-within` pill remains the focus signal. Applies to
  both `#scoutChatInput` and `#roomChatInput` (shared class).
- `.scout-chat-form { align-items: center; }` — center 📎/+/↑ in the bar; verify single-line and
  multi-line (max-height growth) both read centered.
- Test: extend `frontend_chat_*`/design tests to assert the input has no focus-ring box-shadow and
  the form centers.

### Wave 2 — UI polish pass (frontend only)
- **Unify the spine:** `.scout-proposal-card` (7600) + `.manifest-card` (7780) →
  `align-self:center; width:min(var(--feed-measure),100%)` so all card families share the 680px
  column (matches goalcard/letter).
- **De-chrome the run card** (`buildScoutProposalCardNode`, 36782): drop the two bordered meta
  pills (7647-7653); render authority+weight as one quiet caption beneath the actions
  ("writes to your workspace · ~5–15 min"); collapse redundant workstream/group naming.
- **Type + zoom:** define `--type-label-sm` (`~500 11px/1.2 mono`) near 150; add
  `@media (max-width:640px){ .palette__field, run-card fields { font-size:16px } }` to kill iOS zoom.
- **Copy:** sentence-case + shorten checkpoint notes label (`goalCardRenderCheckpoint` 34524) to
  "Notes for the next stage"; push do_not_touch guidance into placeholder/helper text.
- **Wrapping:** deliverable rows wrap titles (2-line clamp) instead of hard ellipsis; manifest
  footer gets a wrap fallback so actions never clip.
- Tests: `frontend_design_p0_test.go`, `frontend_manifest_test.go`, `frontend_feed_design_test.go`.

### Wave 3 — PDF multipage fix (Go; depends on nothing, coordinates with Wave 5)
- **Standardize the deck on the `.pg` chassis.** Add a `go:embed`ed chassis fragment (the
  invariant print CSS from `deck-template.html:101-109` + the `.pg`/`#stage` screen model) and
  inject it into the `ship_deck` PromptBody (`packaging_studio.go:335-339`) as the required
  scaffold, so the authored deck (a) uses `.pg` slides and (b) always contains:
  `@page{size:1920px 1080px;margin:0}` + `@media print{ .pg{display:block;width:1920px;height:1080px;break-after:page;break-inside:avoid;print-color-adjust:exact} .pg:last-of-type{break-after:auto} #prompt,#phint,#railwrap,.navzone{display:none!important} }`.
- **Defense-in-depth:** in `executeRenderExportPDF` (before write, alongside `injectRenderPrintCSP`,
  `render_runner.go:452/514`) inject a fallback print stylesheet keyed to the `.pg` contract, so a
  deck that omits it still paginates. Fallback is idempotent (skip if deck already declares `@page`).
- **Guard:** if a `html_deck` kind flattens to exactly 1 page, mark a disclosed skip/warning
  (surface in the manifest, mirroring existing skip disclosure) rather than shipping silently.
- Tests: `render_runner_test.go` (multi-`.pg` HTML → >1 page; fallback injection idempotent),
  `packaging_studio_test.go` (ship_deck prompt carries the print contract), `jpeg_pdf_test.go`.

### Wave 4 — task discovery (frontend + tiny Go)
- **Seed the empty Scout thread** (`ensureScoutChatEmptyState` 36433 / markup 18828) with 3–5
  tappable starter run-cards (reuse the `data-agent-template` pattern, 19083) for the headline
  runs (packaging_studio, deck_outline, deep_research, grill_pressure_test) → call
  `openToolPalette()`/`promptScoutForWork()`.
- **Label the + door** (18853): visible "Run a task" affordance / enriched placeholder
  (`scoutChatDefaultPlaceholder` 33237): "ask Scout, or tap + / type / to run a task".
- **Palette weight badges:** palette tile shows the run's weight ("~5–15 min" vs "quick single pass").
- **Voice parity:** build `initiate_goal`'s `tool` enum from `buildToolsPayload()`
  (`kanban.go:2255`) exactly as `scoutRouterTools()` does, so voice can pick a run-type reliably.
- Tests: `frontend_survey_chips_test.go`/new empty-state test; `tool_registry`/`process_definitions` tests.

### Wave 5 — art-directed imagery (Go; coordinates with Wave 3's chassis)

Imagery is decided by an agent, not a flag. Scope: the **`packaging_studio`** path (the only one
that renders a self-contained HTML deck via `ship_deck`); the quick `deck_outline` tool emits an
outline, not a rendered deck, so imagery naturally doesn't apply there — which also keeps cost off
the light runs. Three sub-parts:

- **(a) Imagery direction stage** — a new `processRoleWriter`/reasoning stage in
  `packagingStudioDefinition` (`packaging_studio.go:187`), placed after `identity` (visual system
  decided) + the chosen narrative (`write`/`voice`/`founder_pass`), before `ship_deck`. It is the
  **art director**: reading the chosen narrative page-by-page and the identity visual system, it
  decides the imagery *strategy* for THIS package — which specific beats want an image and the
  emotional job each one does (drive consensus / talent interest / capital), and where absence is
  stronger. It honors the chassis laws verbatim (max ~5 full-bleeds, exactly one crescendo page at
  `--heat:.45`, bone-ledger pages carry no imagery, one `FIG.` per photo plate, duotone/heat
  treatment tied to the visual-system tokens). Output: a structured **shot list** — per selected
  slide → `{slot (plate|bleed), subject, composition, mood, treatment, aspect, caption/FIG, why}`.
  Zero images is a legitimate output (a deliberately typographic package). Leans on the existing
  taste discipline (`taste_analyst.go`/`house_style.go`) so the direction reads as one visual system.
- **(b) Generation** — a `processRoleCompile` step (mirrors `slide_jury`) fulfills the director's
  briefs via the existing generator (`runImageryBoard`/`createOpenAIImage`, OpenAI `gpt-image-2`,
  `openai_images.go:49`): one image per brief, `putBlob`, filed as `{kind:image}` assets on the
  package with the brief's caption/slot metadata. Per-shot failure is disclosed and skipped, never
  fatal (keyless/quota/timeout → that image is dropped with a note, the ship proceeds).
- **(c) Placement + inline** — relax `ship_deck`'s "no external references" to allow **data: URIs
  only**; feed `ship_deck`/`ship_compile` the director's shot list + the generated blobs so each
  image lands at its directed slot as a base64 data-URI on the chassis's `.plate .ph` / `.bleed .ph`
  `background-image` (with its `FIG.`/caption), surviving the renderer's `img-src data:` CSP and the
  print reveal.
- **Editorial check:** the existing `slide_jury` already sees the rendered pages — extend the
  design-eye seat to judge whether each image earns its place; images it rejects land as revision
  notes on the findings record (advisory, consistent with the jury's current contract), so the bar
  stays "imagery only where it enhances."
- Tests: `packaging_studio_test.go` (direction stage present + ordered; shot list honors the
  chassis laws; zero-image path valid; per-shot skip disclosed), `openai_images_test.go` (briefs →
  generator → data-URI inline), `slide_jury_test.go` (imagery scored on rendered pages).

### Wave 6 — deliverables drawer + feedback linkage (frontend + Go)
- **Drawer (frontend):** a deliverables picker sourced from already-loaded `artifactEntries`
  (`loadArtifacts` 41174), filtered to shippable deliverables (isGoalArtifact OR non-empty
  asset entries OR source in {scout_thread, goal_thread, packaging_studio_ship}). Opens from a new
  composer affordance beside `scoutChatAttach`; available in public channels *and* private chat.
- **Drop = arm the live seam:** selecting a deliverable arms the existing `scoutFollowUpTarget`
  ({artifactId, mode, query, version, threadId}) and shows its existing chip
  (`renderScoutFollowUpTarget` 33249). The next send already posts `followUpArtifactId`
  (`sendScoutChatViaOffice` 33094) — no new send plumbing.
- **Re-point the two dead surfaces:** goal-card "send notes" (34605) and manifest-card rows
  (`scoutManifestCardNode` 37028-37051) arm `scoutFollowUpTarget` on that deliverable's artifactId
  instead of prefilling text / being open-only.
- **Server gate A** (`scout_chat_threads.go:396`): when a dropped deliverable isn't yet referenced
  in the thread, **add it as a thread ref** (via `updateScoutChatThreadRefs`/`commitScoutChatThreadRefStatus`,
  1077-1095) before the follow-up, instead of rejecting — this is what unlocks drop-into-a-new-channel.
- **Server gate B** (dispatch by source): a thin switch on `artifact.Metadata["source"]` in the
  `followUpArtifactId` branch — `scout_thread` → `launchAgentThreadFollowUp` (unchanged); goal /
  process / packaging deliverables → goal-engine resume with a feedback note
  (`resumeBlockedGoal`/`resumeApprovedGoalWithChoice` 3019/3067, threaded through `goalRevisionNote`
  2833/3228), allowing a **completed** goal to re-open for a feedback-driven revision.
- Tests: `scout_chat_threads_test.go` (drop adds ref + routes), `agent_thread_followup_test.go`
  (source dispatch), `goal_engine_test.go` (completed-goal re-open w/ note), `linkage_test.go`,
  frontend drawer test.

## Gate → verify → ship

- **Per-wave gate:** an adversarial reviewer scores the wave's diff against the specific concern it
  serves; ship-blockers fixed before proceeding. Reuse the repo's gate discipline.
- **Verify (evidence before claims):** `go build ./... && go test ./...`; then run the app and
  observe: (a) mobile composer — no white ring, centered icons; (b) a real packaging run → deck PDF
  is multi-page **and** carries images; (c) drop a deliverable into a fresh channel + feedback →
  the worker resumes that deliverable.
- **Ship:** one branch off `main`, all waves, single clean push. Per the concurrent-sessions rule:
  never discard others' working-tree changes; rebase, don't force-push; no-undo clause in any
  subagent prompt that touches shared files.

## Risks / watch-items

- `index.html` is a single 44k-line file touched by waves 1, 2, 4, 6 → **serialize** frontend edits
  (no parallel agents on `index.html`) to avoid clobbering.
- Waves 3 + 5 both touch `ship_deck` authoring and the "no external references" rule → design the
  chassis and the data-URI inlining **together**; the chassis must support both print pagination
  and embedded data-URI imagery.
- Imagery is art-directed (agent decides count 0..N), scoped to the deck-rendering `packaging_studio`
  path, so cost lands only where a visual deck is actually produced — not on light runs. The
  per-shot skip path must be robust (keyless/quota/timeout disclose and proceed) so a generation
  failure never blocks a ship.
- The imagery direction stage must not become a slop generator: the chassis laws (full-bleed cap,
  one crescendo, FIG discipline) are hard constraints in its prompt, and the slide-jury design eye
  is the backstop that keeps imagery earning its place.
