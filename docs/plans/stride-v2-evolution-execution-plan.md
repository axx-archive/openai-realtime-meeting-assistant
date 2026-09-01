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
| 1 | A. Conversations | Human groups UI, membership route, server read markers, typing, mute, roster from accounts, conversation search, mobile unread divider (+ OPS-1) | completed (OPS-1 pending) |
| 2 | A2. Design system | Light/dark palette canon, liquid-glass surface tiers, one icon system, one menu component, theme-parity contrast pins | pending |
| 3 | B. Work | Live Work list, nav badge, Save-to-Files with fallback, admit all result kinds, ask-for-changes, room work in Work, step progress, version list | pending |
| 4 | B. Studios | Autosave, version history/restore, DOCX, 409→copy, print, open .md from Drive, deck Theme + layouts, dead editor removal (+ OPS-2) | pending |
| 5 | C. Drive | Per-file ACL + share with people, previewer, file share links, versioning, starred + trash, content search, attach to work requests, quota, view fixes (+ OPS-3) | pending |
| 6 | D. Rooms in-call | Live captions, reactions + hand raise, device pickers, share as second transceiver + audio, host controls, quality badge, Scout invite decoupled from voice gate | pending |
| 7 | D. Rooms around-call | Scheduled meetings + timed ICS + upcoming list, recording to blob → Meeting Record (default off) (+ OPS-4) | pending |
| 8 | E. Memory | remember() seam, memory inspector + correction, person model from positions/decisions + accounts roster, decay above transcripts, chat-native extractor, anchor spill, coverage on answers, explicit private remember (+ OPS-5) | pending |
| 9 | F. Resilience | Per-seat fallback + provider breaker (routing #40); Claude fallback on typed seats only if ratified; readyz "fallback active" | blocked on founder decision |
| 10 | F. Polish | Integration design pass across all five surfaces, mobile web, motion, empty states; final acceptance (+ OPS-6) | pending |

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
