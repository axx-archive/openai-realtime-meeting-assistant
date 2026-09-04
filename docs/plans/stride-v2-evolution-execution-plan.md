# STRIDE v2.0 Evolution — Execution Plan

Date created: 2026-09-01
Primary plan: `docs/plans/stride-v2-evolution-plan.md`
Execution log: `docs/plans/stride-v2-evolution-execution-log.md`
Branch: `main` (shared working tree with concurrent sessions; commit small, rebase, never force-push)

---

## How This Works

1. Execute one wave at a time, in order. A wave can run in the current session or a fresh one; the execution log carries all state either way.
2. Each wave has a ready-to-use prompt at the bottom of its section in the execution log.
3. Do not begin the next wave until the log marks the current wave as `completed`.
4. Every wave updates the execution log with: checklist, files changed, validation, risks, next-wave prompt.

---

## Reference Documents

| Path | Purpose |
|---|---|
| `docs/plans/stride-v2-evolution-plan.md` | Audit findings with file:line evidence, the bar, program shape, founder decisions |
| `AGENTS.md` | Deploy flow, production data location, release ceremony |
| `deploy/digitalocean/README.md` | Exact-release procedure |
| `docs/plans/model-routing-master-plan-2026-07-11.md` | Change #40 (per-seat fallback + breaker) for Wave 9 |
| `docs/memory-architecture-study-2026-07-10.md` | Memory doctrine and loss ledger for Wave 8 |
| `docs/stride-signal-canon.md` | Brand and mark canon |
| `plans/README.md` | Motion audit doctrine and rejected findings |

---

## Critical Rules (Apply to ALL Waves)

1. **Concurrent sessions share this tree.** Never `git checkout/reset/restore/stash/clean` or overwrite changes you did not make. If an Edit fails because the file changed underneath, re-read and re-apply only your own change. Do not touch `apple/`.
2. **New HTTP routes need three registrations:** `main.go` HandleFunc, an `authorization_surfaces.go` inventory row, and a `guest_allowlist_test.go` probe row.
3. **`index.html` is read once at boot** (`os.ReadFile`, not embed). Restart the server to see edits. Module-level `const`/`let` declared below boot-time callers are a live TDZ hazard; declare inside the function or use `var`. `requestAnimationFrame` does not run in background tabs; use `setTimeout` for must-run work. Test responsive behavior only with a real viewport resize.
4. **Design canon:** one orange `#FF5A19`, ember earned-only (exception: active rail tab), rail mark graphite per ground, mono type for machine facts only, glass floating-menu recipe (`color-mix(surface-1 94%)` + `--glass-blur-chrome` + `@starting-style` from trigger origin), all motion behind reduced-motion gating with `--dur-*`/`--ease-*` tokens, deck editor first frame has zero animations, fullscreen fixed overlays enter with opacity only.
5. **Gate before claiming done:** `go build`, focused `go test -run` for the touched pins, then a `TestIndex` baseline diff (swap `git show HEAD:index.html` to establish the red baseline; never stash), then the full `go test .` (~16 min) before shipping. Rendered tests locate chat actions by accessible name.
6. **Deploy discipline:** never activate while `/readyz` shows participants > 0; exact-release ceremony from a clean worktree at the commit; upload in 2MB chunks; never hand-edit base env; live data is the docker volume, `/opt/meetingassist/data/` is stale.
7. **Memory contract:** private you+Scout thread content never enters memory implicitly (`private_chat_brain_contract_test.go`). Only an explicit remember action may write.
8. **Default-off STRIDE domains stay default-off.** Shipping code is not activation.
9. **Provider doctrine:** Scout's typed path is OpenAI-owned. No Claude fallback on core routes without AJ's ratification (Wave 9 gate).
10. **Honest states:** never relabel a degraded lane as healthy; UI copy must say what actually happened (queued/running/ready/failed, saved/not saved).
11. **Every wave carries design:** a designer/visual-qa pass on the touched surface in both themes at 1280×800 and 390×844 before review.

---

## Wave Map

| Wave | Chapter | Scope summary | Status |
|---|---|---|---|
| 1 | A. Conversations | Human groups UI, membership route, server read markers, typing, mute, roster from accounts, conversation search, mobile unread divider (+ OPS-1) | completed · live gen 246/247 |
| 2 | A2. Design system | Light/dark palette canon, liquid-glass surface tiers, one icon system, one menu component, theme-parity contrast pins | built + reviewed · batch gate |
| 3 | B. Work | Live Work list, nav badge, Save-to-Files with fallback, admit all result kinds, ask-for-changes, room work in Work, step progress, version list | built + reviewed · batch gate |
| 4 | B. Studios | Autosave, version history/restore, DOCX, 409→copy, print, open .md from Drive, deck Theme + layouts, dead editor removal (+ OPS-2) | built + reviewed · batch gate |
| 5 | C. Drive | Per-file ACL + share with people, previewer, file share links, versioning, starred + trash, content search, attach to work requests, quota, view fixes (+ OPS-3) | built + reviewed · batch gate (review fixes in flight) |
| 6 | D. Rooms in-call | Live captions, reactions + hand raise, device pickers, share as second transceiver + audio, host controls, quality badge, Scout invite decoupled from voice gate | built + reviewed + Chrome QA · batch gate |
| 7 | D. Rooms around-call | Scheduled meetings + timed ICS + upcoming list, recording to blob → Meeting Record (default off) (+ OPS-4) | built + reviewed · batch gate |
| 8 | E. Memory | remember() seam, memory inspector + correction, person model from positions/decisions + accounts roster, decay above transcripts, chat-native extractor, anchor spill, coverage on answers, explicit private remember (+ OPS-5) | built + reviewed · batch gate (review fixes in flight) |
| 9 | F. Resilience | Per-seat fallback + provider breaker (routing #40); Claude fallback on typed seats only if ratified; readyz "fallback active" | breaker + honest states built; Claude fallback still founder-gated |
| 10 | F. Polish | Integration design pass across all five surfaces, mobile web, motion, empty states; final acceptance (+ OPS-6) | in progress (polish + shell chrome + AJ additions) |
| 11 | G. Packaging Studio | Work → Packaging Studio; New Presentation / New Document / Commission Research flows; Bonfire-branded research reports; rename / duplicate / save / DOCX / PDF on every deliverable; project tags that auto-file into Drive folders (+ OPS-7) | pending (scouting 2026-09-02) |
| 12 | H. Harness + Dissent | First-class harness: streaming answers + tool progress, wide admitted tool set with per-tool authority, inline step cards, budgeted turns; per-seat provider table with Fable 5.1 on the design/judgment seats (founder-ratified 2026-09-02, key pending), eval bake-off before flipping the chat answer seat, provider stamps; Dissent ported in as a STRIDE sub-product (control plane + assurance + receipts) with a founder-only DISSENT admin panel showing token flow and assurance analytics (+ OPS-8) | pending |

---

## Wave Details

### Wave 1: Conversations completeness (+ OPS-1)

**Chapter/Phase:** A. Conversations
**Estimated scope:** ~900 lines across `index.html`, `scout_chat_threads.go`, `chat_participants.go`, one new Go file, tests
**Dependencies:** none (backend for human groups exists uncommitted in the shared tree; build on it)

**Deliverables:**
- [ ] D1 Human-group section in the Conversations sidebar: "groups · you + people" head, `+` opens a create form (title + member multi-select from `/assistant/chat-participants`), posts `{title, conversationKind:'human_group', memberEmails, operationId}`; `renderChatAgentThreads` gets a third bucket keyed on `conversationKind==='human_group'`; group rows show member avatars stack; header eyebrow says "N people".
- [ ] D2 `PATCH /assistant/chat-threads/{id}/members` (owner-only, human_group and project threads): `{add:[], remove:[]}` → `validateScoutChatHumanGroupMembers` on the resulting set, persists, broadcasts `chat_thread` update; removed member loses live events. Three-registry rule. Tests: add/remove/non-owner 403/last-human refusal/idempotent.
- [ ] D3 Member management UI: thread header "people" popover (glass recipe) listing members with remove for owner and an add row; consumes D2.
- [ ] D4 Server read markers: call `POST /assistant/threads/read` on thread select and on tail render when the newest message is visible; unread dot and "N new messages" seam read `unreadCount`/`lastReadMessageId` from the index projection; `chatThreadSeenMap` stays only as an offline fallback; mobile gets the divider too (remove the `desktopChatLayoutQuery` gate at `index.html:71713`).
- [ ] D5 Typing indicators: composer input debounce sends `chat_typing` WS (start/stop, 4s TTL); render a per-thread presence row above the composer ("AJ is typing…", up to 3 names); never for Scout; reduced-motion safe dots.
- [ ] D6 Mute/notification level: thread header overflow gets All / Mentions / None posting to `/assistant/threads/mute`; sidebar dot honors `muted`/`notificationLevel` from the GET payload; muted rows show a small slash glyph.
- [ ] D7 Mention roster from accounts: `chatMentionCandidates` enumerates `accountStore()` users instead of `seededAccounts`, dedupes against the seed roster, keeps Scout first. Test.
- [ ] D8 Conversation search: `GET /assistant/chat-search?q=&limit=` over `meetingMemoryKindScoutChat` messages the viewer may read (`scoutChatThreadAllowsViewer`), returns `{threadId, threadTitle, messageId, author, at, snippet}` ranked by recency with lexical match; the sidebar search box switches to a results list after 2+ chars with debounce, Enter opens the thread at the message. Three-registry rule. Tests: ACL (private thread of another user never returned), snippet highlighting, limit.

Gotchas: the other session's uncommitted `scout_chat_threads.go` hunks will be folded into this wave's commit; say so in the commit body. `frontend_thread_nav_split_test.go` and `frontend_desktop_chat_quality_test.go` pin the sidebar; run them before and after. Composer Enter does not send in this app.

### Wave 2: Design system canon — color, glass, icons, menus

**Chapter/Phase:** A2. Design system
**Estimated scope:** ~900 lines (CSS token block + component CSS in `index.html`, one design doc, contrast/pin tests)
**Dependencies:** Wave 1 (its popovers become the first consumers of the shared menu component)

**Deliverables:**
- [ ] D1 Palette canon as tokens: light ramp (putty ground `#DDD4C6` soft → well → ceiling, luminance ceiling 0.7727) and dark ramp with named roles (`--ground`, `--surface-0..3`, `--well`, `--ink-1..4`, `--line`, `--ring`), semantic hues carried as `--*-text` pairs per ground, one orange `#FF5A19` with the earned-only doctrine written next to the token and the sanctioned exceptions listed (active rail tab, live/speaking, search hit well). Document in `docs/design/stride-design-system-v2.md` with rendered swatches for both themes.
- [ ] D2 Liquid-glass surface tiers as tokens and classes: `glass-chrome` (topbar, rail, in-call dock: `color-mix(surface-1 88%)` + `--glass-blur-chrome` saturate 1.25), `glass-float` (menus, popovers, pickers: 94% mix + blur + 1px `--line` + `--shadow-float`), `glass-sheet` (modals, side sheets, green room: 96% mix + stronger blur), each with a no-`backdrop-filter` fallback that stays legible and a dark-theme variant; migrate every remaining ad-hoc translucent surface in `index.html` to a tier (inventory first, list in the doc).
- [ ] D3 One icon system: inventory every inline SVG in `index.html`; adopt the stride 1.8-stroke set (`strideChatActionIcon` family) as the base at 16/20/24 with `currentColor`; replace stragglers (mixed stroke widths, filled glyphs, emoji-as-icon) and rail/dock/composer icons; document the set with names.
- [ ] D4 One menu component: `bfMenu(trigger, items, {origin, radio})` rendering the glass-float tier with `@starting-style` scale-in from the trigger origin, `--ease-spring`, `role=menu`/`menuitem`/`menuitemradio`, arrow keys, Home/End, type-ahead, Escape + outside click close with focus return; migrate the reaction picker, chat more-menu, greenroom filters, account menu, files kebab, org menu, settings sections, Wave 1's members/bell popovers, and the deck/document editor menus.
- [ ] D5 Theme-parity pass: every surface at 1280×800 and 390×844 in light and dark through the canvas-resolved contrast probe → 0 failures, pinned by a test that walks the token pairs; mid-transition screenshots excluded by design.
- [ ] D6 Toasts, focus rings, selection, scrollbars, and empty-state illustrations unified on the tokens; motion stays on `--dur-*`/`--ease-*` with reduced-motion coverage.

Gotchas: token VALUES are not test-pinned today, but contrast ratios and the ember exception list should be. Never change `--text-1` to white for the rail mark (AJ ratified graphite). Brand assets and mobile keep putty.

### Wave 3: Work hub truth

**Chapter/Phase:** B. Work
**Estimated scope:** ~700 lines across `index.html`, `studio_projects.go`, `stride_artifact_drive_save.go`, tests
**Dependencies:** Wave 2 (design tokens, menu component)

**Deliverables:**
- [ ] D1 Live list: `osEventHandlers` for `artifact_progress`/`artifact_completed` call `loadStudioProjects({onlyIfChanged:true})`; keep the poll as fallback at 30s; running-work count badge on the Work rail item.
- [ ] D2 Save-to-Files from the Work detail actions row via `artifactSaveToFilesControl(project.result)`; client falls back to `POST /assistant/files/save` when the disposition route returns 503; artifact-stage button gets the same fallback.
- [ ] D3 Admit image, spreadsheet, research-report, and generic artifact kinds into `studioProjectKindForProcessID` + `studioLegacyProjectCandidate`; kind glyphs and filters in the list.
- [ ] D4 "Ask for changes" on a Work item: arms `armScoutFollowUpTarget` and navigates to the source thread with the composer focused and a prefilled quote.
- [ ] D5 Version list on the Work detail: project prior `artifactVersion` entries into `studioProjectResultRef.versions[]`; open any version read-only.
- [ ] D6 Room-launched work in Work: admit `originKind=room` goal roots into `studioProjectProjectionRelevantEntry`; detail shows "from room X".
- [ ] D7 Step-level progress in the Work detail: render the per-stage runlog the chat goalcard already receives.
- [ ] D8 Empty and error states honest and designed (queued/running/needs_input/failed copy), both themes.

### Wave 4: Studios (+ OPS-2)

**Chapter/Phase:** B. Studios
**Estimated scope:** ~1,400 lines across `index.html`, `document_editor.go`, `deck_editor.go`, new `document_docx.go`, tests
**Dependencies:** Wave 3 (version projection)

**Deliverables:**
- [ ] D1 Autosave: after the first successful save (destination chosen) both editors debounce-save 1.5s after idle with a quiet "Saved · 12:04" chip; conflicts route to D4; offline queues.
- [ ] D2 Version history + restore: `GET /artifacts/document?id=&version=` and deck equivalent read prior revisions; history rail with restore-as-new-version.
- [ ] D3 DOCX export: `document_docx.go` pure-Go OOXML over the Markdown AST mirroring `deck_pptx.go`; Export menu item; test opens the zip and checks parts.
- [ ] D4 409 → "Save a copy" branch instead of a dead-end toast; copy carries the unsaved body.
- [ ] D5 Print: `@media print` over `.document-editor__paper` and a Print action; deck prints one slide per page.
- [ ] D6 Open uploaded `.md`/`.txt` from Drive in Document Studio (import to a new artifact).
- [ ] D7 Deck `Theme` becomes a real `deckDocument` field (background, accent, type ramp) with three built-in themes and a layout applier (Title, Title+body, Two column, Image left) that emits standard element sets.
- [ ] D8 Remove the dead `openDeckEditor` (~740 lines) after confirming zero callers; pins updated.
- [ ] D9 AI assist in Document Studio: selection-scoped `POST /artifacts/document/assist` (rewrite / summarize / continue) returning replacement Markdown, wired to the toolbar; provider-gated, honest "assist unavailable" state when the Scout lane is degraded.

### Wave 5: Drive (+ OPS-3)

**Chapter/Phase:** C. Drive
**Estimated scope:** ~1,300 lines across `files.go`, `blobs.go`, `share_links.go`, `index.html`, tests
**Dependencies:** Wave 4 (open-in-editor)

**Deliverables:**
- [ ] D1 Per-file ACL: `kind=file` entries gain visibility (`private|company|people`) + grants; `assistantFilesForPrincipal` routes reads through `ObjectAuthorizer`; default for new uploads is `company` (today's behavior) with a one-click "only me"; migration stamps existing rows `company`.
- [ ] D2 Share with people + manage access in the details pane.
- [ ] D3 Share links for plain files (`shareLinkRecord.ObjectType == "file"`, blob-streaming branch).
- [ ] D4 In-app previewer: PDF/images/video (`video/mp4` inline behind range support), decks and docs via the render/artifact stage; keyboard next/prev.
- [ ] D5 Versioning: re-upload with the same name in the same folder chains `versionOf`; Versions list in details.
- [ ] D6 Starred + trash/restore (`deletedAt` tombstone, 30-day purge via the existing sweep).
- [ ] D7 Server-side content search through the memory search used by `assistantFileContextEntries`.
- [ ] D8 Attach Drive files to a work request: the Drive picker emits `contextRefs` on the work-request composer.
- [ ] D9 Quota/usage in the sidebar; fix Home label, Recent semantics (last opened), parent-folder roll-up, scope copy.
- [ ] D10 Blob GC: wire `sweepUnreferencedBlobs` as an admin action plus a weekly sweep that runs after the trash purge, with a dry-run report first.
- [ ] D11 Account lifecycle: `disabledAt` on `userAccount` with an owner-only toggle in settings; disabled accounts are excluded from mention candidates, member pickers, human-group validation, and Drive sharing, and cannot sign in.

### Wave 6: Rooms in-call

**Chapter/Phase:** D. Rooms
**Estimated scope:** ~1,100 lines across `index.html`, `main.go`, `rooms.go`, tests
**Dependencies:** Wave 2 (menu/glass tokens for the control island)

**Deliverables:**
- [ ] D1 Live captions overlay on the stage fed by the existing transcript broadcast; toggle in the control island; reduced-motion safe.
- [ ] D2 Reactions + hand raise: `room_reaction` WS case fanning out via `broadcastRoomKanbanEvent`; emoji row in the island; raised hands ordered in the roster.
- [ ] D3 Camera and speaker pickers (`videoinput`, `audiooutput` + `setSinkId`) in green room and settings.
- [ ] D4 Screen share as a second video transceiver with `audio: true`; camera stays; layout treats share as its own tile.
- [ ] D5 Host controls (`room_moderate`: mute-others, remove, lock) guarded by `roomManagedByUser`.
- [ ] D6 Per-tile quality badge from `media_quality` stats.
- [ ] D7 Scout invite decoupled from the voice gate: chat-only Scout can be invited when voice is unqualified; copy says "text only".
- [ ] D8 Room chat parity: link previews via the shared mount and own-vs-peer bubble differentiation (in-call QA leftovers).

### Wave 7: Rooms around-call (+ OPS-4)

**Chapter/Phase:** D. Rooms
**Estimated scope:** ~800 lines across `meetings.go`, `calendar.go`, `meeting_finalization.go`, `index.html`, tests
**Dependencies:** Wave 5 (blob ACL) for recordings

**Deliverables:**
- [ ] D1 Scheduled meetings: `scheduledMeeting` record (room, start, duration, attendees), create/edit from the lobby, upcoming list in the lobby rail, timed VEVENT with the room join URL in `buildICSCalendar`.
- [ ] D2 Recording (default off, per-room setting, consent banner): `MediaRecorder` on mixed audio + active video/share track → blob store → Meeting Record output stage; playback in Meeting Records.
- [ ] D3 Post-call recap card posted into the room's channel with decisions/actions and a link to the record.

### Wave 8: Memory that compounds (+ OPS-5)

**Chapter/Phase:** E. Memory
**Estimated scope:** ~1,500 lines across `memory*.go`, `taste_analyst.go`, `slop_classifier.go`, `entity_ledger.go`, new handlers, `index.html`, tests
**Dependencies:** Waves 3 and 5

**Deliverables:**
- [ ] D1 Deliberate `remember()` seam: Scout tool + `POST /assistant/remember` writing `kind=note` with subject, aliases, `rememberedBy`; recall-eligible; shown with provenance.
- [ ] D2 Memory inspector: "What Scout knows" surface listing ledger records, narratives, decisions, notes, and the viewer's own profile, filterable by subject/person/time; `close`, `correct`, `forget` actions write ledger events (never hard delete except notes by author).
- [ ] D3 Person model fed by positions and decisions: `taste_analyst.go` pass input adds `searchPositionRecords` + `madeBy` decisions; roster derived from accounts; profile visible and correctable in D2.
- [ ] D4 Decay above the transcript tier: `kind=brain` older than N days and fully covered by a current digest becomes a slop candidate with the 30-day reprieve; digest/narrative supersession chains stay.
- [ ] D5 Chat-native extractor: `channel_digest` producer keyed by thread on the digest chassis; channel rows stop riding the meeting prompt.
- [ ] D6 Ledger anchor spill persisted as ledger_event metadata instead of dropped.
- [ ] D7 Recall coverage stamped into `assistantQueryResult` and rendered beside source chips ("complete / partial / unavailable").
- [ ] D8 Explicit private remember: a "remember this" action on a private thread message calls D1; the implicit-never contract stays pinned.
- [ ] D9 Work results reach the ledger: terminal artifacts and `run_log` entries mint ledger events (what was produced, for whom, from which conversation) so recall can answer "what did our agents produce".
- [ ] D10 Recall budget by query class: replace the fixed 60-entry budget with per-class budgets (status / temporal / fuzzy-topic / who-thinks-what) and stop cloning the whole store under the mutex for search.
- [ ] D12 Scout typed answers refuse with "a company source changed while I was working" on EVERY private-thread question in production (reproduced on both `e448f2db` and the prior `97672478` on 2026-09-02 01:29/01:35 UTC, so pre-existing): `lockCurrentCompanyConversationSources` (stride_conversation_recall.go ~407) fails closed on any drift between a recalled channel row's stored metadata/text and the current thread (archived thread, deleted/edited message, renamed channel, changed membership) instead of only on concurrent change during the answer. Diagnose against the production JSONL, then make the lock tolerant of historical drift (drop the stale row from the answer's sources, keep the concurrent-change guard) with a regression test built from the production shape. Scout is effectively dark until this ships — highest priority in the wave.
- [ ] D11 Meeting-digest truncation recovery: production (2026-09-01) shows `meeting-20260901-142307-118263679` stuck for hours on `meeting digest output rejected: max_output_truncation` then `identical_rejected_output` with the circuit suppressing retries; on truncation, halve the transcript window (or raise the output budget once) and retry, and surface the stuck state in Meeting Records instead of only in logs.

### Wave 9: Scout never dark (founder-gated)

**Chapter/Phase:** F. Resilience
**Estimated scope:** ~600 lines across `scout_openai_routes.go`, `openai_responses.go`, `anthropic_text.go`, capability health, tests
**Dependencies:** AJ ratifies Claude fallback on typed seats (plan §Founder decisions #2); otherwise ship breaker + honest states only

**Deliverables:**
- [ ] D1 Per-provider circuit breaker + per-seat same-call fallback with provenance (routing plan #40).
- [ ] D2 If ratified: typed answer/router fall back to `claude-sonnet-5` when the OpenAI circuit is open; answers stamp `provider`.
- [ ] D3 `/readyz` and the composer state distinguish "fallback active" from "degraded"; UI copy says "Scout is answering on a backup model".
- [ ] D4 Extraction workers accept an Anthropic responder behind the same breaker.

### Wave 10: Integration polish + acceptance (+ OPS-6)

**Chapter/Phase:** F. Polish
**Estimated scope:** ~600 lines, `index.html` mostly
**Dependencies:** all prior waves

**Deliverables:**
- [ ] D1 Cross-surface design pass at 1280×800 and 390×844 in both themes: density, empty states, focus rings, motion tokens, mobile web dock.
- [ ] D2 Snappiness: cold-load and route-switch budgets measured and pinned (chat cold index < 600ms on prod data).
- [ ] D3 Final acceptance against every clause of the pinned goal with evidence in the log.
- [ ] D4 GIF picker in composers (`/assistant/giphy/search` + `/import` into the attachment pipeline), shown only when the server reports the integration enabled.
- [ ] D5 Rich media canon in the feed (AJ, 2026-09-02): one card system for X posts, articles, YouTube (click-to-play privacy embed, never autoload), direct images, GIFs, TikTok — token surfaces, fixed aspects, reserved heights (no layout shift), dark/light parity, keyboard open; verified against real links; find and fix why previews are empty on the local sandbox.
- [ ] D6 Primary nav pass (AJ: "the nav isn't there"): labels beside rail icons at ≥1180px, tooltips + ⌘1–⌘5 below, clearer active state (ember glow ONLY — AJ vetoed accent bars on 2026-09-02 as "a mark of AI design"), org-avatar flame tile replacing the rail aperture, rail + topbar as one seamless glass chrome, utilities on one axis, honest org-switcher fallback ("Bonfire", never "Organization unavailable"), mobile dock aligned to the five destinations — shipped as a proposal with before/after screenshots for AJ to ratify or veto.

### Wave 11: Packaging Studio (+ OPS-7)

**Chapter/Phase:** G. Packaging Studio (AJ direction 2026-09-02)
**Estimated scope:** ~2,800 lines: `packaging_commissions.go` (new), `artifact_projects.go` (new), `scout_followup_watcher.go` (new ambient worker), chat-intake seam in `conversation_work_launch.go`, research report branding, deliverable actions, `index.html` hub + three commission flows + threaded clarifications
**Dependencies:** Waves 3 (Work hub truth), 4 (Studios: DOCX/PDF/versions), 5 (Drive folders + ACL), 8 (memory) shipped; the deep-research lifecycle (`0e98580`) and the deck engine (`packaging_studio.go`) exist and are reused, not rebuilt

**Founder intent, verbatim:** "we want the work tab to be renamed Packaging Studio and in there you can start three types of work: New Presentation, New Document, Commission Research. Commission Research should be a nice flow (questions / pill select / or open-ended) where Scout takes the request, kicks off the research and comes back with an Organization (Bonfire here) branded research report. Any deliverable can of course be edited in our document editor or downloaded as a doc or pdf (and saved to our files). Any created or saved presentation/document/research should be able to be renamed/saved/duplicated etc and should have the ability to tag a project which would auto-place it in that folder (or create one if it hadn't previously existed and the user names a new project). Similarly you would commission presentations by giving instruction about look/feel/subject matter/attaching or commissioning research that should inform it, direction on the style of the presentation from a copy perspective, who the audience is, what type of imagery to use (full-bleed background vs. on-slide, hybrid). Similarly for document commission: what type of doc, what sources to look at."

**Deliverables:**
- [ ] D1 Rename: the primary-nav destination, topbar title/subtitle, hub heading, empty states and every user-facing "Work" label read **Packaging Studio** (routes, ids and `data-pd1-destination="Work"` stay; `?kind=` filters stay); mobile dock + tooltips + ⌘-shortcut labels follow.
- [ ] D2 Hub = two or three efficient mini-apps (AJ refinement, 2026-09-02): **Research Desk** (commission → branded report), **Presentation Studio** (brief → deck), and **Story Studio** (workshop the narrative/outline that a deck is built from — iterate the story in chat-like turns, keep versions, then "build the deck from this outline"). Documents are NOT a studio tile: a document is generic work the chat harness handles ("write me a memo on…" → document artifact opened in the document editor). Blank creates stay reachable inside each studio as "start empty".
- [ ] D3 Commission Research flow: a guided brief sheet (`.glass-sheet`) with pill selects (scope: company / market / competitor / technical / people; depth: brief / standard / deep; format: report / one-pager / memo; audience) + an open-ended question + optional sources (Drive attach via `pickDriveFilesForWork`, links) + a "just describe it" shortcut; Scout confirms in one line and starts the existing deep-research run; progress lives in the hub row (steps rail) and in the originating conversation.
- [ ] D4 Organization-branded research report: the report artifact and its PDF/DOCX carry the organization's identity (name from `workspaceOrganizationName()`, wordmark per ground, ember hairline, "Prepared by Scout for Bonfire · date"), a cover block, table of contents, sources appendix; branding is a server-side render pass so every export matches.
- [ ] D5 Presentation Studio brief: subject, audience, copy style (pills: crisp / narrative / data-led / persuasive), look & feel (theme swatches from `deck_theme.go` + free text), imagery mode (full-bleed / on-slide / hybrid — maps onto the engine's `slot: bleed|plate`; full-bleed lifts the one-bleed cap), research inputs (attach an existing research artifact or "commission research first" which chains D3 → deck), **story outline as context** (link a Story Studio outline if one exists; the outline becomes the deck's settled narrative), length; the brief feeds `packaging_studio.go`'s `context_snapshot` and lands in the hub with steps.
- [ ] D6 Documents in the chat harness: a document ask in any conversation runs the existing `document_report` process (reader / voice / document_shape / sources from the message + attachments) and returns a document artifact that opens in the document editor with the D7 actions; no studio tile, no separate brief sheet — clarifying questions come through D11's threaded replies. Story Studio: `POST /assistant/packaging/stories` (outline artifact with versions; turn-based edits in a private thread bound to it; "build the deck" hands the outline to D5).
- [ ] D7 Deliverable actions on every presentation/document/research row and inside the editors: Rename (inline), Duplicate (new artifact, "Copy of …", same fence), Save to Drive, Download DOCX / PDF, Open in editor; research reports open in the document editor as editable documents (import path from Wave 4) without losing the branded export.
- [ ] D8 Projects: a `project` tag on artifacts (picker with existing projects + "New project…"); tagging auto-files the deliverable (and its saved Drive copy) into `Projects/<name>` in Drive, creating the folder on first use; the hub gets a project filter; the Drive folder shows the project's deliverables; retagging moves; untagging leaves the file where it is.
- [ ] D9 Backend: `POST /assistant/packaging/commissions {kind: research|presentation|document, brief}` → validates the brief, creates the work request with the structured brief on the record, launches the right runner; `GET /assistant/packaging/commissions/{id}`; `PATCH /artifacts/{id}` rename + `POST /artifacts/duplicate` + `PATCH /artifacts/project`; three-registry rule for every route; briefs are stored on the artifact for provenance and shown in the row.
- [ ] D11 Chat intake (AJ, 2026-09-02): "if a user asks Scout in a chat to do work, it should process that request for them in the Packaging Studio and ask any necessary clarifying questions specifically to them in threaded replies to that message." A work ask in any conversation (mention or private thread) becomes a Packaging Studio commission with the brief pre-filled from the message + thread context; when the brief is missing something that changes the output (kind, audience, sources, length, imagery mode), Scout asks ONLY the necessary questions as a threaded reply on that message, addressed to the asker (mention), never as a new top-level post; the commission row in the studio shows "waiting on <name>" with the open questions; answers in the thread complete the brief and the run starts without the user leaving chat.
- [ ] D12 Scout follow-up watcher: an ambient worker on a short cadence (~60–90 s, behind the provider breaker, idle-cheap) scans replies to messages Scout sent (threads Scout is part of + top-level messages after a Scout message in a private thread) since its last read cursor, and decides per thread: **reply** (a direct question to Scout, a brief answer that completes a commission, a correction), **act** (brief complete → run the work; "go ahead" → start), or **stay silent** (people talking to each other, an aside, acknowledgement). If Scout was the last speaker and one person replied, it may auto-reply; if several people replied, it reads all of them and reconciles opinions before answering once ("Tim wants it data-led, Ana prefers narrative — I'll lead with data and keep a narrative thread; say the word to flip"). A fresh @scout mention always forces a reply. Every decision is journaled (thread id, message id, verdict, reason) on the brain run log for the inspector; per-thread rate limit (one unsolicited reply per 10 minutes) and a hard stop on self-replies.
- [ ] D14 Private-thread starter pills (AJ, 2026-09-02: "clicking these buttons doesn't do anything really other than type a few words in"): remove the four prefill pills; the private-thread empty state keeps "Continue <last thread>" and offers only affordances that DO something — open Research Desk / Presentation Studio / Story Studio with the brief sheet pre-scoped to this thread, attach from Drive, and ask about a meeting (opens the memory picker) — plus the composer; nothing that merely types words.
- [ ] D15 Shell consistency across tabs (AJ, 2026-09-02: "look at each tab and how it handles headers/subheaders, search"): ONE header system — the topbar carries the tab name (as Rooms does) + one mono subline per tab; in-content page titles ("Recent work", Drive "Home") and per-tab search bars go; a single unified search (⌘K) sits left of the bell and searches conversations, files, work, meetings and memory with a scoped results rail (the existing chat search, Drive search, studio search and memory inspect routes feed it); inside a tab, filtering is chips, never a second search bar; every tab's first content row starts at the same y.
- [ ] D16 Feed media: GIF/image cards must render immediately in the feed (never a blank "loading" block until click) — fix the media path for giphy/CDN images (proxy allowlist or direct `<img>` with reserved aspect), and allow photos AND videos as attachments (`image/*`, `video/mp4`, `video/quicktime`, `video/webm` up to the cap) rendered inline (`<video controls preload="metadata">` with poster), with the same reserved-height cards.
- [ ] D17 Snappiness of conversations: thread switch and post must feel instant — posts are already optimistic (10 ms to node); make thread switching render from cache with a diffed list (no full `replaceChildren` of 80 messages), virtualise long threads, defer link-preview fetches until visible, and pin a budget (thread switch settle ≤ 120 ms p95 on a 100-message thread).
- [ ] D18 Media retention doctrine (AJ: "be logical about how that feed is analyzed and items eventually deleted automatically … unless something explicitly was saved to Drive"): message TEXT and the brain's derived rows are permanent; media BODIES shared in chat (images, video, GIF imports, files not saved to Drive) expire after a retention window (default 90 days, env `CHAT_MEDIA_RETENTION_DAYS`), earlier for videos if disk pressure (>80%) — the sweeper reuses the blob two-sighting rule; "Save to Drive" (or being referenced by a Drive row / deliverable) makes a body permanent; expired media leaves an honest placeholder in the message ("expired · saved nowhere"); the brain analyses media at ingestion (captions/OCR for images where a key exists) so recall survives expiry; a founder-visible storage panel shows usage by class.
- [ ] D13 Threaded replies in the UI: reply-in-thread on any message (exists) shows Scout's clarifying questions inline with quick-answer pills where a question is closed-ended; answering marks the commission "brief complete"; the studio row links back to the thread.
- [ ] D10 Gates: rendered tests for the three flows and the actions; Go tests for commissions, projects auto-filing and branding; recall `/code-review` BEFORE the critic; end-to-end on the sandbox with a real research run when a key is present (otherwise the deterministic fixture); OPS-7 deploy.

### Wave 12: First-class harness + provider seats (+ OPS-8)

**Chapter/Phase:** H. Harness (AJ direction 2026-09-02: "the harness constraint fixes — you'll do all of them"; "I trust Fable 5.1 way more for design in particular"; "I'm happy to have an Anthropic API key provided")
**Estimated scope:** ~2,500 lines: `openai_tool_loop.go` streaming, websocket progress events, admitted-tool authority table, `agent_runner_anthropic.go` revived as a live runner, `provider_seats.go` (new), eval harness, `dissent_executor.go` (new, flagged), `index.html` thread step cards + composer states
**Dependencies:** Wave 11 (chat intake + studios) shipped; `ANTHROPIC_API_KEY` staged by AJ on 2026-09-02 at `/root/secrets/anthropic.key` (root, 600) — OPS-8 applies it as a base-env patch inside the release transaction, reading the file, never echoing it; never hand-edit the base env; Dissent stays flagged off until its live coordination campaign is sealed (see `~/Documents/business` ledger)

**Who chooses the model:** the PLATFORM, per seat, server-owned — never the user. A per-seat provider table (`provider_seats.go`: seat → provider, model, effort, same-call fallback) pinned in code with env overrides; answers stamp `provider`/`model`; the Scout status popover shows the lane to the founder only. Users never see a model picker (Dissent's own doctrine says the same: "never from a user model picker").

**Deliverables:**
- [ ] D1 Streaming: typed answers stream tokens over the existing websocket (`assistant_event` gains `delta`/`done`), tool progress streams as `tool_progress {name, state, summary}`; the composer shows "Scout is <doing X>" with a stop control; router classify + proposal steps happen before the first token but never block it more than ~1 s (classify in parallel with a provisional answer for pure questions).
- [ ] D2 Admitted tools: widen the typed tool loop from 4 tools to the product set (Drive read/save, calendar, research, deck/document/story, memory remember/inspect, work status, meeting records) with a per-tool authority table (read / write / needs-approval), approvals surfaced inline as a card, and the manifest digest re-pinned; room voice keeps its own list.
- [ ] D3 Inline step cards: a Scout turn that runs tools renders a collapsible step card in the thread (steps rail reused from Work) that persists on the message; the Packaging Studio row and the thread show the same steps.
- [ ] D4 Budgeted turns: replace the fixed 16-turn cap with token + wall-clock budgets per seat (chat 90 s / research minutes), honest "I stopped at the budget — continue?" card, resumable.
- [ ] D5 Anthropic seat runner: revive `agent_runner_anthropic.go` as a live runner (Messages API tool loop, same manifest schemas, `web_search`/`web_fetch` server tools where the seat allows), provider breaker per provider, same-call cross-provider fallback (Fable ↔ Sol) stamped as `fallback_active`; seats on Fable 5.1 first: Story Studio, deck design + slide jury + review gates, research synthesis, the critic/review seats; chat answer seat stays on Sol until D6 says otherwise. Voice, transcription, images, embeddings, routing, extraction fleet stay OpenAI.
- [ ] D6 Eval bake-off: 20 real asks from Bonfire Chat + 5 deck briefs + 5 research briefs; both providers on the same contracts, scored by the existing gates (`research_brief_gate_v1`, slide jury, goal review) plus latency and cost per seat; written up in `docs/evals/`; the chat answer seat flips only on a winning score.
- [ ] D7 Provider stamps + lane visibility: every answer/deliverable carries `provider`, `model`, `effort`, `fallback`; `/readyz` per provider; founder-only lane row in the status popover; the memory inspector shows which model wrote a derived row.
- [ ] D8 Dissent becomes a STRIDE sub-product (AJ, 2026-09-02: "take the bones of that and we build it in as a sub-product of STRIDE … we can 'acquire it'"): port the bones from `~/Documents/business` into Go — the bounded work contract (`coordinate` request/response), the deterministic plan compiler (work class, consequence facts, maker family, provider, exact model, reasoning profile, topology `direct` | `full_dissent`), maker-excluded assurance assignments, decision + coordinate receipts (signed, redacted, idempotent, verifiable), qualification registry, and cost/COGS accounting — implemented against STRIDE's existing seams (provider breaker, usage-ledger seats, `models_pricing.go`, the goal engine's review gates). The `workExecutor` interface routes every Packaging Studio deliverable and every judgment seat through it (`direct` for reversible work, `full_dissent` for consequential deliverables); the harness keeps tools, memory, approvals and authority. Drop for now: Stripe/prepaid credits, the external MCP server, the Node runtime.
- [ ] D10 DISSENT admin panel (founder-only — AJ is the only owner): a first-class surface under Settings → Dissent showing token flow through every model (per provider / seat / model / day: requests, input+output tokens, cost from the pricing rows, latency p50/p95, breaker state, fallback share), assurance analytics (full_dissent rate, challenge outcomes, blind spots caught, route quality vs single-model baseline from the value-proof exercises), receipts browser with verification, qualification registry + per-seat routing controls (flip a seat's provider/model/effort with a reason; changes are journaled), capacity/admission view. Server: `dissent_admin.go` routes gated on `isFounderOwner` (three-registry rule). Design: the same token/glass canon, mono for machine facts, charts on tokens.
- [ ] D11 (later, out of this wave) external onboarding: the MCP/REST surface for harnesses outside STRIDE, admission, billing — only after D8/D10 have run Bonfire's own work for a month.
- [ ] D9 Gates: rendered tests for streaming and step cards; Go tests for the seat table, breaker per provider, fallback stamps, admitted-tool authority; recall `/code-review` BEFORE the critic; end-to-end on the sandbox with real keys; OPS-8.

