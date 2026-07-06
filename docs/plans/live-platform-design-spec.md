I have full grounding — every anchor verified against current line numbers, evidence images read, sheet laws confirmed, and the repo's Go-guard test idiom (`functionBody(html, ...)` + `strings.Contains`) understood. Here is the spec.

---

# Implementation Spec — Live-Platform Chat/Deliverable Pass (single-pass worklist)

**Operating law for this pass:** the text the user reacts to must render as language, never as machine syntax, and the primary line in any viewport must be fully legible before anything secondary competes for the row. Where minimalism and actionability collide, **hierarchy of attention wins** (sheet s00 §3: only the current moment carries density; settled content recedes to hairline/mono). All frontend pins follow the repo idiom: a Go `frontend_*_test.go` that reads `index.html`, pulls a function body via `functionBody(html, "function x(")`, and asserts `strings.Contains`.

**Do not touch** (owned by another agent): proposal double-mount, immortal thinking shimmer, parked options not rendering on thread-mounted cards, armed-send prefill objective.

---

## P0 — ship-blockers (raw-syntax leaks + mobile composer/legibility)

### P0-1 — Kill raw markdown in every deliverable body, preview, and the checkpoint inline brief
The founder-named class. The safe artifact renderer tokenizes **only** links; the chat renderer already does bold+code. Four coordinated edits close every surface.

**Surfaces (enumerated):** artifact-stage document body — section paragraphs, list items, table cells, blockquotes (all route through `appendArtifactInlineNodes`); the artifact hero summary; artifact-library card previews; goalcard/thread artifact previews; **the checkpoint inline brief** (founder's literal `> **"…"**` example, a *separate* code path); and stage-body contract names in backticks.

- **(a) `appendArtifactInlineNodes` — index.html:24213** (regex at :24215). Replace the link-only regex with the chat renderer's pattern (reference `appendChatInlineNodes`, index.html:36865, pattern :36867), keeping the artifact link class:
  `const md = /\[([^\]\n]{1,140})\]\((https?:\/\/[^\s)]+)\)|\*\*([^*\n]+)\*\*|`([^`\n]+)`/g`
  Loop branches: group 1+2 → the existing `artifact-read__inline-link` anchor (unchanged); **new** group 3 → `<strong>` (textContent = group 3); **new** group 4 → `<code class="artifact-read__code">` (textContent = group 4). Do **not** add the chat renderer's bare-URL group (group 5) — out of scope, would change existing link behavior.
- **(b) `appendArtifactBodyNodes` — index.html:24125.** It has no `#` heading branch, so a raw body with `## Heading` (the checkpoint brief case) would leak `##` even after (a). Add a heading branch mirroring the chat block renderer (index.html:36799), before the paragraph fallthrough at :24207: `^\s*#{1,6}\s+(.+)` → build `<h5 class="artifact-read__subhead">`, strip a trailing colon, route the label text through `appendArtifactInlineNodes`. (Safe for the main reader: section bodies arrive heading-stripped from `artifactSections`, so this only fires on raw bodies.)
- **(c) Checkpoint inline brief — index.html:34182.** Currently `wrap.appendChild(bfEl('div', 'goalcard__checkpoint-brief', briefText))` sets `briefText` as raw `textContent` (this is the founder's cited leak). Replace with a built div rendered through the now-complete safe block renderer:
  `const brief = bfEl('div','goalcard__checkpoint-brief'); appendArtifactBodyNodes(brief, briefText); wrap.appendChild(brief)`
  Adjust `.goalcard__checkpoint-brief` (index.html:17359): drop `white-space: pre-wrap` (no longer raw text) and add `display:grid; gap:8px` so the block children (`<p>`/`<blockquote>`/`<ul>`) get rhythm. Keep the `max-height:180px; overflow-y:auto` scroll box.
- **(d) `compactArtifactPreview` — index.html:41479.** After the whitespace collapse, strip inline tokens so hero summaries (renderArtifactRead :24020), library previews (:43972), and `artifactPreviewText` (:23875) never show raw markup: `.replace(/\*\*([^*]+)\*\*/g,'$1').replace(/`([^`]+)`/g,'$1').replace(/\[([^\]\n]{1,140})\]\(https?:\/\/[^\s)]+\)/g,'$1')` before the length clamp.
- **CSS add** — next to `.artifact-read__inline-link` (index.html:10982): `.artifact-read__code { font-family: var(--font-mono); font-size: 0.92em; background: var(--surface-3); color: var(--text-1); padding: 1px 6px; border-radius: var(--r-sm); }` and a quiet `.artifact-read__subhead { font: var(--type-label); letter-spacing: var(--track-label); color: var(--text-3); text-transform: lowercase; }` (see P1-3 for the sibling section-label voice).

**Test pin:** new `frontend_artifact_markdown_test.go` — assert `functionBody(html,"function appendArtifactInlineNodes(")` contains `` `([^`\n]+)` `` and `\*\*([^*\n]+)\*\*` and `artifact-read__code`; assert `appendArtifactBodyNodes` body contains `#{1,6}` and `artifact-read__subhead`; assert `compactArtifactPreview` body contains the strip `.replace`; assert the stylesheet contains `.artifact-read__code`. Extend `frontend_checkpoint_test.go` to assert `renderGoalCardCheckpoint`/its body calls `appendArtifactBodyNodes(brief` (not `bfEl('div', 'goalcard__checkpoint-brief', briefText)`).

### P0-2 — Stop the document header speaking machine to the reader
Same disease as P0-1: the stage kicker literally reads **"markdown"**, and the meta row reads **"artifacts artifact · complete · 12:33 · anthropic fable · 100%"** (evidence: mobile/stationtenn-view.png).

- **Kicker `artifactStageKicker` — index.html:23347** (sniff at :23351). Add a display map before the `artifactModeLabel` fallback: `markdown|md|text|doc → 'document'`, `pdf → 'pdf'`, `html_deck`/deck → 'deck' (deck already handled). Never emit a raw file-format word.
- **`artifactModeLabel` — index.html:41409** (return :41414). Collapse the doubled label: if `mode` ends in `artifact`/`artifacts`, return `'artifact'` (kills "artifacts artifact"); keep the `workflow → 'goal workflow'` case.
- **`artifactWorkerLabel` — index.html:23888** (fallback :23892). Add the model identity to match the chat voice (sheet s00: "claude · fable 5"): worker `anthropic_fable` (or any worker containing `fable`) → `'claude · fable 5'`; keep the existing codex/openai maps; keep the generic `replace(/_/g,' ')` only as last resort.
- **Redundant percent — index.html:24011.** In the meta-chip loop (renderArtifactRead :24006-24017), omit the `${…}%` chip when `artifactStatusValue(entry)` is `complete`/`published` (100% beside "complete" is noise); keep it mid-run.

**Test pin:** new `frontend_artifact_header_test.go` — assert `artifactStageKicker` body contains `'document'` and does not `return` a bare `m.type`; assert `artifactModeLabel` body collapses `artifact`/`artifacts`; assert `artifactWorkerLabel` body contains `claude · fable 5`; assert `renderArtifactRead` body guards the percent chip on `complete`/`published`.

### P0-3 — Composer + choice hit targets to 44px (founder's "where you type" mandate)
The codebase's own checklist (index.html:39) mandates ≥44×44 (`--hit-min`), yet at ≤640px the **send** button is 31×31 (index.html:16426), **attach** is 34×34 (index.html:9016), and the checkpoint **choice** pills are 38px (index.html:17420) with no mobile bump. The fix pattern already exists one button over on `.scout-chat-tools::before` (index.html:17845).

- Add `position: relative` to `.scout-chat-send` (index.html:9049) and `.scout-chat-attach` (index.html:9016), then add the centered 44×44 transparent `::before` hit-extension copied verbatim from `.scout-chat-tools::before` (index.html:17845-17851) to both. Visual pill size stays 34px (31px→restore to a 44px *hit* while keeping the small glyph); the transparent overlay keeps pointer events on the button.
- Add `.goalcard__choice { min-height: var(--hit-min); }` inside the existing `@media (max-width: 640px)` block at index.html:17493.
- **Check before commit:** confirm no two 44px `::before` boxes overlap given the actual composer DOM order (attach/tools/send, index.html:18706-18718) — the tools button's own comment (skill rule 16) says its extension fits the 8px flex gap; mirror that, and if attach/send end up adjacent, keep the gap ≥ the overlap so hit areas stay disjoint.

**Test pin:** new `frontend_hit_targets_test.go` — assert the stylesheet contains `.scout-chat-send::before` and `.scout-chat-attach::before` each with `var(--hit-min)`, that `.scout-chat-send`/`.scout-chat-attach` carry `position: relative`, and that the `≤640px` block sets `.goalcard__choice { min-height: var(--hit-min)`.

### P0-4 — Mobile goalcard head + amber parkline: primary/needs-you lines unreadable at 390
Evidence (scratchpad/live-mobile-goalcard-head.png): the title strangles to a 4-line ~100px column ("Turn / this into / a goal / workflow:"), the subtitle truncates to "Waiting for a…", and the single highest-priority line in the grammar truncates to **"your call — the ru…"**. All three are legibility blockers on the sheet's core mobile moment (s09). Add rules to the `@media (max-width: 640px)` block at index.html:17493.

- **(a) Head layout.** `.goalcard__head` (index.html:16991) is a flex row; the mono `.goalcard__meta` (index.html:17171, `flex:none; white-space:nowrap`) + the `⋯` control eat the row and the title's `text-wrap:balance` (index.html:17043) spreads the remainder over 4 lines. **Do not hide `.goalcard__meta`** — its content ("0 of 14 · 01:16" = subtask done-count + elapsed) is *distinct* from the rail-count ("7/10" = stage n-of-m, index.html:17492); hiding it drops real telemetry. Instead **relocate/demote** it: at ≤640px make `.goalcard__id` take the full row (it already `flex:1; min-width:0`) and move `.goalcard__meta` to its own line under the stage-line (e.g. `order` it after `.goalcard__id`, or `flex-basis:100%` on a wrapped head) so the title gets full measure. Clamp the title to 2 lines: `.goalcard__title { display:-webkit-box; -webkit-line-clamp:2; -webkit-box-orient:vertical; overflow:hidden; }`.
- **(b) Parkline.** `.goalcard__parkline-label` (index.html:17336) is `nowrap` + ellipsis while `.goalcard__parkline-count` (index.html:17344) keeps `margin-left:auto` nowrap. At ≤640px stack it: `.goalcard__parkline { flex-wrap: wrap; }` and `.goalcard__parkline-label { white-space: normal; flex: 1 1 100%; }` so "your call — the run is parked" gets a full line and "checkpoint 1 of 4" drops right-aligned beneath (its `margin-left:auto` still holds). **Never ellipsize this string** — it is the single loudest line in the grammar; keep the JS author string intact (index.html:34150, and the ship-gate twin at :34331).

**Test pin:** extend `frontend_checkpoint_test.go` (or a new `frontend_mobile_goalcard_test.go`) — assert the `≤640px` block contains `-webkit-line-clamp: 2` scoped to `.goalcard__title`, a `.goalcard__parkline { flex-wrap: wrap` rule, and `.goalcard__parkline-label { white-space: normal`; assert the block does **not** contain `.goalcard__meta { display: none` (guards against the telemetry-loss regression).

---

## P1 — clarity / actionability

### P1-1 — Proposal card's one sentence ships a double period (do first — trivial, high-visibility)
Evidence (desktop/sim-thread-mid.png): "…LONG LIGHT identity**..** gate:" and "…and to whom**..** gate:". `scoutRouterToolRunSummary` (scout_chat.go:686) concatenates `objective + ". gate:…"` (run, :690) and `objective + ". it parks…"` (process, :688); the objective (set at scout_chat.go:546) is router-authored and usually ends in ".".
- **Change:** at the top of `scoutRouterToolRunSummary`, `objective = strings.TrimRight(strings.TrimSpace(objective), ".")` before both joins.
- **Test pin:** in `scout_chat_threads_test.go` (the summary assertions live at :813) add `if strings.Contains(proposal.Summary, "..") { t.Fatalf(...) }`, or a dedicated `TestScoutRouterToolRunSummaryTrimsTrailingPeriod` feeding an objective ending in "." for both a run and a process tool.

### P1-2 — Document composition: truncated hero duplicates the first section; title echoes as a "no detail yet." tile
Evidence (mobile/stationtenn-view.png): the hero paragraph is cut mid-sentence and the identical full text repeats one card below (Vision), and a first tile echoes the 4-line title with body "no detail yet." on a **complete** artifact. In `renderArtifactRead` (index.html:23971): the hero `summary` is sourced from `artifactPreferredSection` (:23999-24000) which then also renders in the grid (:24030-24037); empty-body/heading-only sections still render, and `appendArtifactBodyNodes` injects "no detail yet." (index.html:24128-24132).
- **Change (hero):** only render the hero `<p>` summary when it is a *true abstract* — i.e. when `parsed.objective` exists and is used; if the summary would be borrowed from a section that also renders in the grid (`summarySection` non-null), **skip the hero paragraph** and keep only the meta chips. (The objective already de-dupes against summary at :24026; extend that so a borrowed section summary doesn't double-print.)
- **Change (title echo / empty sections):** when the artifact has any non-empty section, skip sections whose body is empty (index.html:24030-24037) instead of rendering "no detail yet." — reserve that placeholder for a genuinely empty artifact. Specifically drop a leading heading-only section whose heading case-insensitively equals `artifactStageTitle`/`artifactDisplayTitle` (the `# Title` echo from the body head).
- **Test pin:** new `frontend_artifact_compose_test.go` — assert `renderArtifactRead` body guards the hero `<p>` on a real-abstract condition and skips empty/title-echo sections (contains the heading-equality check and an "any non-empty section" guard).

### P1-3 — Document sections render as equal-weight shouting tiles; the sheet wants a quiet reading measure
Sheet s10 spec 2e ("document — the safe renderer at a reading measure") shows a mono lowercase section label over flowing prose, no boxes; s00 §3 says settled content recedes to hairline/mono. Live: every section is a bordered `surface-1` card with a headline-weight `h4` (index.html:10939, :10953) — five equal tiles on mobile.
- **Change:** scope a stage-surface override. Under `.artifact-stage__read` (index.html:17797): `.artifact-read__grid { grid-template-columns: 1fr; }` (single column) and `.artifact-read__section { border:0; background:none; padding:0; }`; restyle its `h4` to the mono label voice — `font: var(--type-label); letter-spacing: var(--track-label); color: var(--text-3); text-transform: lowercase;` — body stays `text-2` at the reading measure (the read pane is already `max-width:68ch`, :17799) with 24-28px rhythm between sections. **Keep the tile treatment** only where sections are grid summaries (the intelligence data room) — the base `.artifact-read__section` rule stays; the override is parented by `.artifact-stage__read`.
- **Test pin:** extend `frontend_deck_viewer_test.go` (artifact-stage surface) — assert the stylesheet contains an `.artifact-stage__read .artifact-read__section` rule zeroing `border`/`background` and an `.artifact-stage__read .artifact-read__section h4` rule with `var(--type-label)` + `text-transform: lowercase`.

---

## P2 — polish

### P2-1 — Two proceed options render as two co-equal filled pills (false default)
Evidence (desktop/sim-thread-bottom.png): "brand assets provided" and "no brand assets — develop identity" both ink-filled. Sheet s05 grammar is exactly one filled primary with outlined siblings. In the choices loop (index.html:34220-34236) every `action==='proceed'` gets `goalcard__choice--primary` (:34228), so an N-proceed fork yields N primaries.
- **Change:** count proceeds first. If exactly one proceed → keep the current filled/outlined/ghost mapping. If several (a genuine fork) → render all proceed options in the outlined base register (drop `--primary`) so the amber parkline stays the only loud element and no false default is implied.
- **Test pin:** extend `frontend_choices_test.go` — assert the loop counts proceeds and only applies `goalcard__choice--primary` when the proceed count is 1.

### P2-2 — Machine identity leaks: "cancelled by aj@shareability.com" and hardcoded "waiting on AJ"
Sheet s10 cancelled spec reads "cancelled by aj at stage 6" (lowercase handle). Live renders the full email (index.html:34357, `by` sourced at :34356); "waiting on AJ" is a hardcoded literal at index.html:34187 and :34347 — everywhere else the voice is the lowercase handle (`aj · via scout`, and the existing `raw.split('@')[0].toLowerCase()` idiom at :36628 / :36092).
- **Change:** add a client `accountHandle(value)` helper (email → roster short name via the peers/roster map, else the pre-`@` local-part lowercased) and use it in the cancel line (:34357). Replace both `'waiting on AJ'` literals with `` `waiting on ${approverHandle}` `` sourced from the same admin-gate config `canApproveExternalWrites` reads.
- **Test pin:** extend `frontend_goal_cancel_test.go` — assert `accountHandle(` is defined and used in the cancel-line render, and that the string literal `waiting on AJ` no longer appears in index.html.

### P2-3 — Edge-swipe back is dead; leaving a convo needs a 30px chevron (severity reduced to P2 by the gate — a working tappable back exists)
Only one history call exists (`replaceState`, index.html:22413); no `pushState`/`popstate`. Convo↔threads is a `data-chat-view` swap, so the iOS/Android edge-swipe triggers browser-back and exits the SPA; the only affordance is a 30×30 chevron (`.chat-convo-head__back`, index.html:14618; handler :20838).
- **Change:** on threads→convo and office→tool transitions, `history.pushState({view:'convo', thread:id}, '')`; add a `popstate` handler mapping state back to `setActiveTool`/`setMobileChatView('threads')` (reuse the existing back handler at :20838). Give `.chat-convo-head__back` the same 44×44 `::before` hit-extension as P0-3 (it is 30px, below `--hit-min`).
- **Test pin:** extend `frontend_router_test.go` — assert a `popstate` listener exists and that the convo transition calls `history.pushState`; assert `.chat-convo-head__back::before` carries `var(--hit-min)`.

---

## Acceptance checklist — after-capture gate (screenshots + what each must show)

Re-drive the live platform and capture at **desktop 1440** and **mobile 390** (the two audited widths). Gate passes only when every line below is true.

**Mobile 390 — artifact stage (recapture `mobile/stationtenn-view.png`):**
- [ ] No literal `**` or backticks anywhere in the body — `presenter_script_v1` renders as an inline mono code chip, bold renders as weight (P0-1).
- [ ] Kicker reads **"document"**, not "markdown"; meta row reads "artifact · complete · 12:33 · claude · fable 5" with **no** "artifacts artifact" and **no** trailing "100%" (P0-2).
- [ ] First card is a real section, not a title-echo tile; **no** "no detail yet." on a complete artifact; the hero summary is not duplicated verbatim by the section one swipe below (P1-2).
- [ ] Sections read as mono lowercase labels over flowing prose at the 68ch measure — no five equal bordered tiles (P1-3).

**Mobile 390 — goalcard head + parked checkpoint (recapture `live-mobile-goalcard-head.png` / `live-mobile-convo-bottom.png`):**
- [ ] Title reads on ≤2 lines at full width (not a 4-line ~100px column); the stage subtitle is legible (not "Waiting for a…"); the "0 of 14 · 01:16" telemetry is still present, demoted to its own line (P0-4a).
- [ ] The amber line reads the full **"your call — the run is parked"** with "checkpoint 1 of 4" beneath — no "your call — the ru…" (P0-4b).
- [ ] Checkpoint inline brief shows rendered blockquote/bold, no raw `> **"…"**` (P0-1c).

**Mobile 390 — composer (recapture `mobile/thread-with-composer.png` / `composer-focused.png`):**
- [ ] Send, attach, and tools each land a 44×44 tap target (DevTools/measured), visual glyphs unchanged; checkpoint choice pills are ≥44px tall (P0-3).
- [ ] Edge-swipe from the convo returns to the thread list instead of exiting the app; the back chevron is a 44px target (P2-3).

**Desktop 1440 — thread (recapture `desktop/sim-thread-mid.png` / `sim-thread-bottom.png`):**
- [ ] Every proposal-card sentence has single terminal punctuation — no "identity.. gate:" / "to whom.. gate:" (P1-1).
- [ ] A multi-proceed checkpoint shows at most one filled primary pill; a two-proceed fork shows two outlined pills with the amber parkline the only loud element (P2-1).
- [ ] A cancelled card reads "cancelled by aj" (handle), not the full email; a waiting card reads "waiting on <handle>" (P2-2).

**Regression guard (both widths):** chat message bodies (already correct via `appendChatInlineNodes`) still render bold/code/links; the intelligence data-room section tiles are unchanged (the reading-measure override is scoped to `.artifact-stage__read` only).

**Green bar:** `go test ./...` passes with the new/updated pins (P0-1 `frontend_artifact_markdown_test.go` + `frontend_checkpoint_test.go`; P0-2 `frontend_artifact_header_test.go`; P0-3 `frontend_hit_targets_test.go`; P0-4 mobile-goalcard pins; P1-1 `scout_chat_threads_test.go`; P1-2 `frontend_artifact_compose_test.go`; P1-3 `frontend_deck_viewer_test.go`; P2-1 `frontend_choices_test.go`; P2-2 `frontend_goal_cancel_test.go`; P2-3 `frontend_router_test.go`).