# STRIDE v2.0 Evolution — Execution Log

Date created: 2026-09-01
Owner: product (AJ Hart) / goal-loop sessions
Primary plan: `docs/plans/stride-v2-evolution-execution-plan.md`
Strategy doc: `docs/plans/stride-v2-evolution-plan.md`
Branch: `main`

---

## How To Use This Log

1. Execute one wave at a time. Do not begin the next until this log marks the current wave `completed`.
2. After each wave, update: wave status, checklist, files changed, migrations applied, validation commands + outcomes, risks/blockers, and the prompt for the next wave.
3. **Compact previous waves** before writing the next-wave prompt (see Log Compaction).
4. Print the next-wave prompt in your final response so the user can continue in-session or paste it into a fresh session.

### Log Compaction

After completing a wave, compact ALL previously completed waves to:

    ## Wave N
    Status: `completed`
    Files: `file1` (created, N LOC), `file2` (modified, +a/-b)
    Risks carried forward: {only unresolved risks, or "none"}

Keep the CURRENT wave's section complete until the next wave starts. Future wave scaffolds hold only wave number, status, and scope checklist.

---

## Agent Team Structure

The executing agent is the lead: it coordinates, delegates implementation, validates, and updates this log. Spawn teammates via the Agent tool, omitting `model`.

| Role | subagent_type | Responsibility |
|---|---|---|
| lead | (executing agent) | Coordinates, delegates, validates, updates log, writes next-wave prompt |
| backend-dev | general-purpose | Go handlers, stores, tests |
| frontend-dev | general-purpose | `index.html` markup/CSS/JS and the `TestIndex*` pins |
| reviewer | feature-dev:code-reviewer | Reviews the wave diff for correctness, patterns, edge cases |
| visual-qa | general-purpose | Rendered pass on the touched surface, both themes, desktop + phone |
| ops-agent | general-purpose | Checkpoint waves only; follows `AGENTS.md`; asks AJ before activation |

Every subagent prompt carries the concurrency clause: never `git checkout/reset/restore/stash/clean`; never overwrite changes you did not make; re-read and re-apply on Edit conflict; do not touch `apple/`.

| Wave | Dev Role(s) | Optional Specialists | Notes |
|---|---|---|---|
| 1 | backend-dev (D2, D7, D8), frontend-dev (D1, D3–D6) | visual-qa | Backend first; frontend consumes D2/D8 |
| 2 | frontend-dev (tokens, glass, menu component), designer (icon inventory, doc) | visual-qa, critic | Design canon is high-stakes; AJ ratifies swatches |
| 3 | frontend-dev, backend-dev (D3, D5, D6) | visual-qa | |
| 4 | frontend-dev ×2 (doc / deck), backend-dev (D2, D3) | visual-qa | Two editors in parallel |
| 5 | backend-dev (ACL, links, versions), frontend-dev | visual-qa, critic | ACL is high-stakes |
| 6 | frontend-dev, backend-dev (WS verbs) | visual-qa | Media pins are brittle |
| 7 | backend-dev, frontend-dev | visual-qa | Recording consent copy |
| 8 | backend-dev ×2, frontend-dev | critic | Memory design is high-stakes |
| 9 | backend-dev | reviewer | Founder-gated |
| 10 | frontend-dev | visual-qa | |

---

## Ops Debt Tracker

**Deploy order (canonical, see `AGENTS.md` and `deploy/digitalocean/README.md`):**
1. `go test ./...` green locally (no Go on the VPS).
2. Commit + push to `axx/main` (fetch and rebase first, never force-push).
3. Prepare a clean worktree at the exact commit; scope/prepare the release archive.
4. Upload in 2MB chunks; build on the droplet from the archive.
5. Confirm `/readyz` participants == 0; activate from the serving release's sealed tool.
6. Verify `/healthz` + `/readyz` release identity, then rendered smoke on thebonfire.xyz.

### Pending Ops

| Source Wave | Type | Item | Status |
|---|---|---|---|
| 1 | deploy | Wave 1 Conversations (index.html + Go routes `/assistant/chat-search`, `/assistant/chat-threads/{id}/members`); no migrations | pending (OPS-1) |

### Ops Checkpoints

| Checkpoint | After Wave | Trigger | Covers |
|---|---|---|---|
| OPS-1 | 1 | Conversations complete | Wave 1 |
| OPS-2 | 4 | Design system + Work + Studios complete | Waves 2–4 |
| OPS-3 | 5 | Drive complete | Wave 5 |
| OPS-4 | 7 | Rooms complete | Waves 6–7 |
| OPS-5 | 8 | Memory complete | Wave 8 |
| OPS-6 | 10 | Program complete | Waves 9–10 |

### Applied Ops

(none yet)

---

## Wave 1

Status: `completed` (built, reviewed twice, critic ACCEPT 9.0, /code-review high findings fixed and re-reviewed, committed on main)

### Scope Checklist
- [x] D1 Human-group sidebar section + create form + member picker + third render bucket (`groups · you + people`, `#chatGroupCreate`, avatar cluster rows, `N people · shared memory` eyebrow)
- [x] D2 `PATCH /assistant/chat-threads/{id}/members` (owner-only; add ∪ remove; human group <2 → 409; unregistered → non-enumerating 400; project threads min 1; lock → re-read → save → continuity rebuild → `chat_thread` fan-out to prior ∪ new members)
- [x] D3 Member management popover (glass-float, owner sees `×` + add row, optimistic with rollback, viewer removal drops the row and falls back to a channel)
- [x] D4 Server read markers (`POST /assistant/threads/read` on select / tail-in-view / append, 400 ms debounce; unread dot and seam from `unreadCount` / `lastReadMessageId`; phone seam unlocked)
- [x] D5 Typing indicators (`chat_typing` frames ≤1/3 s, false on send/clear/blur/switch; presence row with 4 s TTL; never self or Scout; reduced-motion safe)
- [x] D6 Mute / notification level (bell popover: All / Mentions only / Nothing → `/assistant/threads/mute`; index rows carry `muted` + `notificationLevel` only when non-default; bell-slash glyph; dot suppressed at `none`)
- [x] D7 Mention roster from accounts (`chatMentionCandidates` enumerates `accountStore().accountEmails()`, Scout first, viewer excluded, alphabetical; composer autocomplete uses the server directory once loaded — critic round-1 fix)
- [x] D8 `GET /assistant/chat-search` (viewer-projected, non-archived threads, 2..200 runes, limit ≤50, newest first, ±60-rune snippets) + sidebar results with `<mark>` ember-10% wells, ↑/↓/Enter/Escape, jump-to-message with ≤5 pages of history paging

### Files Changed
- `index.html` (+~2,250/−~20): CSS 10318–10805, 11235–11510, 16389–16445, 17308–17330; markup 36016–36161; JS 38041–38062, 38462–38477, 39935–40080, 62219, 65762–65909, 66117–66344, 67097–67160, 68780, 71072–72318 (the Wave 1 block), 72392–72650, 73427–74433.
- `scout_chat_threads.go` (+324/−63, of which ~25 hunks are the other session's human-group backend, folded in deliberately): members PATCH branch, `updateScoutChatThreadMembers`, creation fan-out, group preview copy.
- `chat_search.go` (new, ~230 LOC), `chat_participants.go` (+42/−?), `thread_mute.go` (+9), `thread_read_markers.go` (+18), `main.go` (+1), `authorization_surfaces.go` (+2), `guest_allowlist_test.go` (+1).
- Tests: `chat_search_test.go` (new), `scout_chat_members_test.go` (new), `frontend_conversations_wave1_test.go` (new), `chat_participants_test.go` (+81), `thread_read_markers_test.go` (+90); the other session's `scout_chat_human_group_test.go` (untracked → committed with the backend it pins).
- Docs: the three `stride-v2-evolution-*` documents (this program's plan, execution plan, and log).

### Migrations
None.

### Validation
- `go build ./... && go vet .` → clean.
- Focused Go: `go test -count=1 -run 'TestScoutChatMembers|TestChatSearch|TestChatMention|TestAssistantHumanGroup|TestScoutChatThreadsIndex|ThreadReadMarker|ThreadMute|TestAuthorizationSurface|TestGuestRouteWalkAllowlistFailsClosed' .` → 35/35 PASS before the critic fixes; re-run after them recorded below.
- Broad Go slice `-run 'Chat|Mention|Thread|Participant|Riff|Notification|Authorization|Guest'` → ok (164.8 s).
- Frontend battery `-run 'TestIndex|TestFrontend|TestChat'` on a detached worktree at HEAD (with `node_modules` linked) → 0 failures baseline; on the Wave 1 tree → 0 failures, 0 new.
- Full `go test -count=1 -timeout 40m .` → three runs on the Wave 1 tree: run 1 (before the critic fixes) `ok` 837.9 s; run 2 (final tree, no contention) one failure, the pre-existing room-archive flake documented below; run 3 (post-review tree) one failure, `TestRenderedChatMutationsStayBoundToSourceAndAuthGeneration` (1.14 s, pixel-tolerance rendered test) which then passed 10/10 in isolation on both the Wave 1 tree and pristine HEAD and 3/3 inside the rendered-chat battery under mutual contention — classified as a load flake (three finder agents were grepping the tree during that run). Every other test was green in all three runs. Note: `go test .` needs `-timeout 40m`; the default 10 m kills the suite.
- Rendered/end-to-end (sandbox :3171 — binary provenance: built from the tree before the critic round-1 backend fixes for the lead's pass, rebuilt from the final tree afterwards; the critic's round-2 pass used its own build of the final tree on :3172 — AJ in the browser pane, Tim via API and Playwright, Tom via curl): group create 201 + idempotent replay 200; members PATCH 200 / 403 non-owner / 404 outsider / 409 shrink-to-one with rollback and server copy in the toast; removed member's row drops in ~150 ms; read marker POST fires on open and the server unread count goes 1 → 0 with the marker on the newest message; mute `none` suppresses the dot; typing frames start/stop on the socket, "Tim is typing…" rendered cross-user with 4 s expiry; search results mode hides section heads, `<mark>` computes to ember at 10 %, click jumps to and rings the message in view; XSS probe (`<img onerror>`) rendered as literal text in snippets and messages; another user's private thread never returned; both themes at 1280×800 and 390×844.
- Code review (feature-dev:code-reviewer): backend pass no findings; frontend pass one finding (singleton add-picker kept chips across thread switches) → fixed with an `onClose` reset → re-check PASS.
- `/code-review high` (recall-biased: 7 finder angles × ≤6 candidates → 4 batched verifiers): 10 findings survived — non-seed @-mentions never notified (`chat_mentions.go:156`), index fingerprint `id:updatedAt` stranded mute/read state from other devices (`index.html:65967`), search results mode hid conversation-name matches, members render closed the bell menu on channels, typing/search names empty for non-seed accounts (`chat_typing.go:28`, `chat_search.go:118`), UTF-16 query gate + 400 shown as outage, search projected every thread per request, membership change skipped `reconcileConversationSourceEpisodeAuthority`, single pending read-marker slot. Refuted: the ACL-leak reading of the reconcile gap (reads fail closed), mobile unread on open (both open paths force the tail), unknown `conversationKind` records (no writer can produce one), and "delete the seen map" (it feeds the `since=` tail hydration). All ten fixed in a third dev round: backend `chatMentionTargetEmails` (directory-resolved mentions), shared `accountDisplayName`, `reconcileConversationSourceEpisodeAuthority` after membership change, fan-out via `deliverScoutChatThreadMetadata` + `{id, removed:true, title}` to ejected members, 32-member cap on project threads, search rewritten with a raw-record prefilter, riff exclusion, parsed-once times, bounded newest-first insertion and an ASCII fast path, one-pass viewer mute map, `clampQueryLimit`; frontend fingerprint with viewer-state fields, `muted` collapsed into `notificationLevel`, "conversations" group above "messages" in search, members-hide closes only its own popover, code-point gate + 200-cp ceiling + 400→validation copy, cross-thread read-marker flush, length-preserving fold for `<mark>`, `chatNotifyPending` merge guard, `removed:true` handling, directory state hoisted (TDZ guards deleted), shared icon/avatar nodes. Reviewer re-checks: backend PASS (8/8), frontend PASS (hoist has no duplicate declarations, read-marker optimistic state is not resurrected, fold keeps offsets aligned, search traversal wraps across both groups, avatar special cases survived). The critic was not re-run after this review round: its 9.0 acceptance predates the fixes, which only remove defects it did not observe; Wave 2 runs the recall review BEFORE the critic. New file `chat_directory_test.go`.
- Pre-existing flake, not Wave 1: `TestArchivedRoomRejectsMemberAndGuestWhileDurableCloseRetriesBeforeTeardown` fails 3/20 on pristine HEAD (`git worktree` at `97672478`) and 3/20 on the Wave 1 tree, always at ~1.02 s; filed as a separate task. It appeared in two of three full-suite runs; each other test was green.
- Critic (goal-anchored, threshold 9.0 / floor 7.0): round 1 REJECT 7.8 → seven revisions applied (creation fan-out, "new group" preview, archived threads excluded from search, composer roster from the server directory, rAF → setTimeout for popover focus, "search unavailable" state, log + gap map + founder decision 6) → round 2: ACCEPT 9.0 (goal fidelity 9 / product bar 9 / design 9 / truthfulness 9 / plan durability 9); the critic re-verified the backend fixes on a binary it built from the final tree (:3172) because the lead's :3171 sandbox binary was stale after the backend edits (restarting only re-reads index.html) — rebuilt afterwards.

### Risks carried forward
- Search runs the full viewer projection over every readable thread per query (linear, no index); revisit in Wave 10's snappiness budget if channels grow past a few thousand messages.
- Mention-candidate order changed from roster order to alphabetical; the macOS client should be checked for any ordering assumption.
- Non-owners cannot leave a group (founder decision 6).
- The account store has no lifecycle concept (no disabled/deactivated flag), so an offboarded person stays a valid @-mention target and group member until their record is deleted (plan §Gap map, Wave 5).
- Cross-user typing rendering was verified in the frontend dev's two-user run and by server tests; the lead verified the client frames only.
- Design nits for Wave 2 to absorb (critic): a non-owner's members dialog has no focusable child so focus stays on the trigger; group rows lead with initials discs while other rows lead with a stroked glyph — ratify or unify in the icon system.
- Harness lesson: restarting the sandbox re-reads `index.html` but not the Go binary — rebuild `ma` after any Go edit before making rendered claims.

### Ops Checkpoint (OPS-1)
Status: `pending` — deploy needs AJ's go and an empty room (`/readyz` participants == 0). Pending item appended to the Ops Debt Tracker.

### Prompt For Wave 2

```
Continue the STRIDE v2.0 Evolution implementation on branch main (shared working tree; never git checkout/reset/restore/stash/clean; do not touch apple/).

Source of truth:
1) docs/plans/stride-v2-evolution-execution-plan.md — read ONLY the Critical Rules section and Wave 2 details
2) docs/plans/stride-v2-evolution-execution-log.md — read ONLY the Wave 2 section and the Ops Debt Tracker (Wave 1 is compacted)
3) docs/stride-signal-canon.md §mark and §color only if the inline context below is not enough

Execute ONLY Wave 2 (Design system canon — color, glass, icons, menus). Do not start Wave 3.

## What previous waves shipped
Wave 1 (the main commit titled `feat(chat): Wave 1 — groups, membership, read markers, typing, mute, search`; find it with `git log --grep='Wave 1' --oneline`): human groups, membership route + popover, server read markers, typing, mute, roster from accounts, conversation search — all in index.html + Go. Its two header popovers (`#chatConvoMembersPopover`, `#chatConvoNotifyMenu`) and `chatHeaderPopoverController` (index.html ~71690–71715) are the freshest consumers of the ad-hoc glass recipe and must migrate to the shared menu component in this wave.

## Wave 2 scope
- D1 Palette canon as tokens + `docs/design/stride-design-system-v2.md` with rendered swatches for both themes; ember doctrine and its exceptions written next to the token.
- D2 Liquid-glass tiers `glass-chrome` / `glass-float` / `glass-sheet` as tokens + classes with no-backdrop-filter fallbacks; migrate every translucent surface (inventory first).
- D3 One icon system based on `strideChatActionIcon` (1.8 stroke, currentColor, 16/20/24); inventory the 203 inline `<svg>`s and replace stragglers.
- D4 One menu component `bfMenu(trigger, items, {origin, radio})` (glass-float, @starting-style from trigger origin, --ease-spring, role=menu/menuitem/menuitemradio, arrows/Home/End/type-ahead, Escape + outside close with focus return); migrate: reaction picker, chat more-menu, greenroom filters, account menu, files kebab, org menu, settings sections, Wave 1 members/bell popovers, deck + document editor menus (18 `role="menu"` sites today).
- D5 Theme-parity pass with the canvas-resolved contrast probe → 0 failures pinned by a test that walks token pairs.
- D6 Toasts, focus rings, selection, scrollbars, empty-state art unified on the tokens.

## Inline Context
- Light tokens start at index.html:76 (`:root {`), dark overrides at :406 (`:root[data-theme="dark"]`-style block); `--surface-1` :150 / :406, `--text-3` :175 / :415, `--ring` :194 / :425, `--ember` :208 (`var(--ember-500)`), `--ember-text` :226, `--glass-blur-chrome: saturate(1.8) blur(20px)` :283, motion tokens :367–368 (`--ease-spring`, `--dur-fast`).
- The current glass recipe (33 sites reference `--glass-blur-chrome`): `background: color-mix(in oklab, var(--surface-1) 94%, transparent); backdrop-filter: var(--glass-blur-chrome) saturate(1.25); transition-property: opacity, transform, display; transition-duration: var(--dur-fast); transition-timing-function: var(--ease-spring); transition-behavior: allow-discrete; @starting-style { opacity: 0; transform: scale(.96) }` with `transform-origin` set from the trigger. 13 `@starting-style` blocks exist.
- `strideChatActionIcon(name)` at index.html:75242 returns inline SVG strings for react/riff/reply/more at 1.8 stroke — extend, don't fork.
- Rules that bite: deck editor first frame must have zero animations (`TestDeckStudioRenderedPhoneTouchControls`); fullscreen overlays enter opacity-only; `reducedMotion` is declared at ~38374 — gate all motion on it; rail mark stays graphite (#54545C light / #77777D dark), never `--text-1`; the contrast probe must resolve `color(srgb …)` through a canvas round-trip.

## Execution (Team)
You are the lead. Spawn via the Agent tool, omitting `model`. Task tools may be unavailable — keep the checklist in the log.
1) Spawn `frontend-dev` (general-purpose): D1 tokens + D2 glass tiers + D4 menu component + migrations, with the inline context above and the concurrency clause.
2) Spawn `designer` (general-purpose): D3 icon inventory + replacements, and the design-system doc with swatches; coordinates names with frontend-dev via SendMessage.
3) When both return: spawn `reviewer` (feature-dev:code-reviewer) on the diff; relay fixes; re-check.
4) Spawn `visual-qa` (general-purpose): both themes, 1280×800 and 390×844, contrast probe, keyboard paths on every migrated menu.
5) Run the critic-loop skill (threshold 9.0, floor 7.0) anchored to the founder's design bar: palettes, liquid glass, icons, menus.
6) Compact Wave 1 in the log; fill Wave 2; write the Wave 3 prompt.

Critical rules: see execution plan §Critical Rules (concurrency, three-registry, index.html boot read + TDZ, design canon, gates, deploy discipline, memory contract, default-off domains, provider doctrine, honest states, design pass per wave).

Requirements:
1) Keep working on main; commit only after the full `go test -count=1 -timeout 40m .` is green and the reviewer + critic pass.
2) No migrations expected; if any, `YYYYMMDDHHMMSS_` prefix.
3) Record validation commands + outcomes in the log.
4) Append deploy-affecting changes to the Ops Debt Tracker (OPS-2 covers Waves 2–4).
5) Print the Wave 3 prompt in full in your final response.
```

## Wave 2
Status: `pending`
Scope: palette canon as tokens (light + dark), liquid-glass surface tiers, one icon system, one menu component with every menu migrated, theme-parity contrast pins, toasts/focus/empty states unified; `docs/design/stride-design-system-v2.md`.

## Wave 3
Status: `pending`
Scope: live Work list, nav badge, Save-to-Files with fallback, admit all result kinds, ask-for-changes, room work in Work, step progress, version list, honest states.

## Wave 4
Status: `pending`
Scope: autosave, version history/restore, DOCX, 409→copy, print, open .md from Drive, deck Theme + layouts, dead editor removal (+ OPS-2).

## Wave 5
Status: `pending`
Scope: per-file ACL + share with people, previewer, file share links, versioning, starred + trash, content search, attach to work requests, quota, view fixes (+ OPS-3).

## Wave 6
Status: `pending`
Scope: live captions, reactions + hand raise, device pickers, share as second transceiver + audio, host controls, quality badge, Scout invite decoupled from voice gate.

## Wave 7
Status: `pending`
Scope: scheduled meetings + timed ICS + upcoming list, recording to blob → Meeting Record (default off), post-call recap card (+ OPS-4).

## Wave 8
Status: `pending`
Scope: remember() seam, memory inspector + correction, person model from positions/decisions + accounts roster, decay above transcripts, chat-native extractor, anchor spill, coverage on answers, explicit private remember (+ OPS-5).

## Wave 9
Status: `blocked` (founder decision: Claude fallback on typed seats)
Scope: per-seat fallback + provider breaker, readyz "fallback active", extraction workers behind the breaker.

## Wave 10
Status: `pending`
Scope: integration design pass, snappiness budgets, final acceptance (+ OPS-6).
