# STRIDE v2.0 Evolution Plan — proof-of-concept to fully featured

Date: 2026-09-01
Author: goal-loop session (Claude Fable 5.1) for AJ Hart
Execution plan: `docs/plans/stride-v2-evolution-execution-plan.md`
Execution log: `docs/plans/stride-v2-evolution-execution-log.md`

## Pinned goal

> Bonfire reaches v2.0: the five core surfaces (video meetings, chat/messaging, Work,
> native docs+decks, Files/Drive) and the compounding memory system are each audited
> against the v2.0 bar, a durable wave plan is committed, and every wave that ships
> passes code-review and end-to-end verification on main.

## The bar, in the founder's words

- **Video meetings** people prefer over Zoom.
- **Chat** with three first-class conversation kinds: public channels, small human groups,
  and private threads with agents only. Messaging people prefer over Slack and iMessage.
- **Work**: every request given to an agent from any conversation lands here, shows live
  progress, and can be opened, downloaded, and saved to Files.
- **Native doc and presentation** creation and editing people prefer over Google Docs and
  Keynote.
- **Files/Drive**: our own file system.
- **Compounding intelligence** that analyzes transcripts, chat, and work; learns the company
  and its people; tracks how that flows over time; discards what it no longer needs; and
  recalls intelligently when a user asks Scout.
- **Design**: beautiful, intuitive, snappy, elegant. AJ's explicit inclusions (2026-09-01): the
  light and dark palettes, liquid-glass surfaces, icon choice, and the look and feel of menus are
  first-class deliverables of this program, not incidental polish.

## Where the product actually stands (2026-09-01 audit)

Six read-only audits ran in parallel against the working tree at `97672478` plus the other
session's uncommitted human-group backend, followed by a rendered walk of the local sandbox
and of production as `aj`. The backend is far past proof-of-concept. What is missing on every
surface is the **last mile**: capabilities the server already has that no UI reaches, and
UI affordances a user expects that nothing implements.

### Production status that shapes everything

`GET /readyz` on thebonfire.xyz (release `97672478`, generation 243) reports these lanes
degraded: `scout`, `typedScoutAnswer`, `typedScoutRouter`, `embeddings` (circuit open,
`providerFailureClass: "quota"`, 13 consecutive failures, 24h cooldown), `meetingSTT`,
`dictation`, `privateVoice`, `roomVoice`, `recap`, `workflows`, and every `ambient.*`
producer. A live `@scout` question in the Bonfire Chat channel answered with "I couldn't
answer safely right now." Cause: OpenAI quota is exhausted, and every Scout text seat and
every memory worker is OpenAI-only by doctrine (`scout_openai_routes.go:11-15`: "Scout's
required text path is OpenAI-owned … Anthropic is retired for new product work").

Consequences:

1. **Founder action, not code:** restore OpenAI billing. Until then no wave that depends on
   Scout answering can be verified live, only against the deterministic test harness.
2. **Founder decision:** whether Scout may fall back to Claude when the OpenAI circuit is open
   (routing master plan change #40 designs per-seat fallback + breaker but the doctrine comment
   forbids Claude on the core routes). Wave 9 is gated on this ratification.

### Surface audits (evidence pointers are file:line at `97672478`)

#### Video meetings

Exists end-to-end: persistent rooms with archive/restore (`rooms.go:720`), guest links
(`rooms.go:826`), green room (`index.html:35208`), gallery/pinned/share packing
(`index.html:56223`), screen share (`index.html:79009`), in-call chat with `@Scout`
(`index.html:35142`), live transcription toggle + transcript tab (`index.html:78748`),
consent-fenced Scout participant (`room_agents.go:401`), Meeting Records with claims and
coverage (`meetings.go:1666`), finalization to decisions/actions/notes (`meeting_finalization.go:450`),
persistent PiP (`index.html:36274`), ICE-restart ladder, simulcast, MediaPipe blur.

Gated or broken: Scout in-room voice hard-off in prod and the Add-Scout button hidden with it
(`room_scout_voice_gate.go:22`, `index.html:89988`); "recording" only toggles transcript
persistence, no media is recorded (`main.go:7275`); calendar is a reserved seam with all-day
ICS only (`calendar.go:22-26`, `:225`); blur is Chrome-desktop-only (`index.html:38419`);
screen share replaces the camera track and carries no audio (`index.html:79049`, `:79040`);
quality telemetry is send-only (`index.html:58671`).

Gaps vs the bar: no recording (L), no live captions (M, transcript deltas already stream at
`transcription_lane.go:1248`), no scheduling (L), no reactions/hand-raise (S), AI participant
invisible in prod (M, one condition at `index.html:89988`), no camera/speaker picker (S),
no host controls (M), share blanks camera (M).

#### Chat and messaging

Exists: public channels (`index.html:39188` → `scout_chat_threads.go:1164`), private
you+Scout threads (`index.html:70182`), threaded replies, reactions, edit/delete, mentions of
people/Scout/hired agents with bell notifications (`chat_mentions.go:344`), attachments and
link previews, Web Push, agent work launched from and reported into a conversation
(`conversation_work_launch.go:23`, `conversation_public_work_launch.go:15`), private Riff.

**Human groups:** backend landed uncommitted in the other session's working tree
(`scout_chat_threads.go:1184-1209`, `validateScoutChatHumanGroupMembers`, ≤32 members,
exact-member ACL, idempotent by `operationId`, pinned by `scout_chat_human_group_test.go`).
Its only client is the untracked macOS app. The web app has no group section, member picker,
or `memberEmails` reference.

Server-complete with zero clients: typing (`chat_typing.go`, WS case `main.go:6823`),
mute/notification level (`thread_mute.go:187`), server read markers
(`thread_read_markers.go:347`; the client uses localStorage `chatThreadSeenMap` at
`index.html:65323` and discards `unreadCount`/`lastReadMessageId` from the index projection),
GIF search (`giphy.go`), thread digest. Unread divider is desktop-only (`index.html:71713`).
Mention roster is the hardcoded seed roster (`chat_participants.go:25`).

Gaps: human-group UI (L), membership management route (M), cross-device read state (M),
conversation search endpoint + UI (L), typing client (S), mute UI (S), GIF picker (S),
roster from accounts (S).

#### Work

Exists: chat turn → `conversationWorkDecision` (`conversation_intent.go:219`) →
`launchGoalThread` (`goal_engine.go:898`); Work tab backed by `GET /api/studio-projects/v1`
(`studio_projects.go:723`) grouped Needs you / Needs attention / In progress / Recent; real
statuses; detail with Continue review / Present / Edit / Open / PowerPoint / PDF
(`index.html:43461-43478`); revisions via `armScoutFollowUpTarget` from chat only.

Broken or invisible: the Work list polls every 6s only while open (`index.html:43556`) and
ignores `artifact_progress` / `artifact_completed` os_events that the chat goalcard already
consumes (`index.html:61728`); artifact-stage Save-to-Files posts to
`/api/artifact-drive-saves/v1` which is fail-closed in prod (`stride_artifact_drive_save.go:30-35`)
with no fallback to the working `/assistant/files/save` (`index.html:52096`); Work admits only
presentation and document kinds (`studio_projects.go:220`, `:255`), never images, sheets,
research; room-launched work lives in a separate store (`roomWorkActivityByRun`); the
1517-line STRIDE work orchestrator (`stride_work_orchestration.go:402`) and its feedback API
have no UI; five overlapping work concepts (goal threads, studio projects, work runs,
packages, board cards).

Gaps: admit all result kinds (L), Save-to-Files from Work with fallback (M), live list (S),
ask-for-changes from Work (M), version history (M), room work in Work (M), step-level
progress (S), nav badge (S).

#### Native docs and decks

Exists: Document Studio on Markdown ↔ single `contenteditable` (`index.html:47937`, parser
`:47625`, serializer `:47766`), headings/lists/tables/images/links/find, GET/PATCH with
optimistic `expectedVersion` (`document_editor.go:92`); Deck Studio on a JSON slide document
of free-form elements (`deck_editor.go:62-108`), drag-reorder, speaker notes, presenter mode,
per-slide AI image generation, pure-Go PPTX export (`deck_pptx.go:227`), sidecar PDF export;
blank create routes (`studio_blank_create.go:70`, `:100`); share links; Save to Files.

Broken: no autosave anywhere and first save forces a Drive-destination modal
(`index.html:48608`, `:50308`); no version history; deck `theme` read in four places but
`deckDocument` has no Theme field (`index.html:49634`); ~740 lines of dead editor
(`openDeckEditor` at `index.html:46493`, zero callers); 409 conflict strands unsaved work
(`index.html:50322`); no DOCX; no print; uploaded `.md` cannot open in the editor
(`index.html:85063`); Document Studio has no AI assist.

Gaps: autosave (M), version history/restore (M), DOCX (M), 409→save-a-copy (S), AI assist
in editor (M, provider-dependent), themes + layouts (M), print (S), open from Drive (M).

#### Files / Drive

Exists: content-addressed blob store (`blobs.go:11`, 64MB cap), `kind=file` metadata rows in
the memory JSONL (`files.go:1394`), folders side-store (`file_folders.go:3`), upload with
drag-drop multi (`files.go:1288`, `index.html:85882`), folders CRUD/move, rename/delete,
session-gated blob download, open-in-editor for deliverables, client-side search, grid/list,
attach to chat via `/assistant/attachments/from-file`, save Work results, Scout reads
authorized files (`scout_file_context.go:390`), artifact share links, backup includes blobs.

Broken: direct uploads have **no per-object ACL**, every signed-in user sees every upload
(`files.go:556`); blob GC unwired (`blobs.go:22`); Home labeled "Suggested for you" but
suggests nothing (`index.html:85858`); Recent is createdAt re-bucketed, not access
(`index.html:85853`); parent folders show 0 files when content is in subfolders
(`index.html:85814`); scope copy strings empty (`index.html:84991`); docx/pptx/xlsx land as
`stored` and are unsearchable.

Gaps: per-file permissions/share with people (L), in-app previewer (M), versioning (M),
share links for plain files (M), quota/usage (S), starred + trash/restore (S), server-side
content search (S/M), attach Drive files to work requests via `contextRefs` (M).

#### Compounding memory

Pipeline as built: meeting STT + channel chat + files + run logs + artifacts → one JSONL store
in RAM behind one mutex → eleven ambient workers (T1 brain, meeting digest with silence-preserving
carry-forward, pure-Go day digest, decision + entity ledgers with owner-scoped positions,
narratives, company digest, taste analyst per-person profile, house style, slop classifier,
embeddings) → layered recall (`memory_query.go:2007`): deterministic ledger/position/evolution
lanes lead, then pinned digests, RRF fusion of lexical + semantic, time-range, participant,
artifact, brains, tail, within a 60-entry budget, ACL-enforced per lane
(`recall_authorization_test.go`, 20 tests), deterministic provenance (`answer_sources.go`).

What is genuinely good: carry-forward guard closes the July study's worst loss
(`meeting_digest.go:725`); semantic lane without a vector DB; who-thinks-what positions
(`entity_ledger.go:108`, `:768`); ACL in recall not bolted on; T4 is state not recursive
summary.

Gaps by dimension:

1. **Ingestion:** private you+Scout threads are UI state and never ingested, deliberately and
   test-pinned (`memory.go:3792`, `private_chat_brain_contract_test.go:25`); the guided intake
   is deprecated with no replacement, so there is **no deliberate `remember()` write path**.
2. **Extraction:** channel messages ride the meeting-brain prompt as `transcript` rows; no
   chat-native extractor; work results never reach the ledger.
3. **Consolidation/decay:** slop classifier only considers transcripts and unpublished
   artifacts; brains, digests, narratives, decisions, ledger records, run logs, files have no
   decay path; ledger anchors cap at 12 oldest-drop (`entity_ledger.go:79-80`).
4. **People/company models:** the per-person `user_profile` exists and evolves
   (`taste_analyst.go`) but is fed only by UI-reaction signals, gated on the hardcoded 7-name
   roster (`participants.go:24-32`), and has no UI.
5. **Recall:** fixed budget regardless of query class; O(N) clone under the mutex; coverage
   grading (`recall_coverage.go`) not surfaced on Scout answers.
6. **Observability (weakest):** the memory timeline hides decisions, digests, narratives,
   ledger events, run logs; the only correct/delete affordance is the quarantine tray.

Roughly 500KB of shadow memory Go (`brain_projection_runtime.go`, `ambient_mind_projection.go`,
`stride_person_mymind.go`, `stride_temporal_brain.go`, `stride_conversation_ledger.go`,
`canonical_retention.go`) has zero production effect; `strideRuntimeFeatureAvailability`
returns false on every branch.

#### Design walk (sandbox at 1280×720 and prod at 1670×1218, light + dark)

The shell, chat canvas, Work hub, editors, Drive, and lobby all render cleanly in both themes
and match the ratified canon (ember active tab, graphite mark, glass menus). Observed:
Rooms lobby reveal, deck editor, document editor, and dark chat feel finished; Drive and Work
empty states are honest. The functional gaps dominate, but AJ wants the design system itself
to be a deliverable: the light and dark palettes, the liquid-glass surface tiers, the icon
set, and the menu look and feel are today spread across ~8 ad-hoc recipes in `index.html`
with token values that are not pinned. Wave 2 canonizes them; every later wave then rides on
those tokens (designer/visual-qa role per wave) with one integration polish wave at the end.

## Program shape

Ten waves in seven chapters, dependency-ordered, ops checkpoints at chapter boundaries.
Details, deliverables, and team per wave live in the execution plan.

| Chapter | Waves | Why this order |
|---|---|---|
| A. Conversations | W1 | Backend for all three conversation kinds exists; pure last-mile; pairs with the in-flight human-group backend |
| A2. Design system | W2 | Palette canon (light + dark), liquid-glass tiers, one icon system, one menu component; every later wave builds on these tokens |
| B. Work + Studios | W3, W4 | Work is the hub every other surface reports into; studios share versioning with Work |
| C. Drive | W5 | ACL model must exist before rooms/memory expose more files |
| D. Rooms | W6, W7 | In-call polish first, then scheduling/recording which touch finalization |
| E. Memory | W8 | Needs Drive ACL and Work results settled; largest design surface |
| F. Resilience + polish | W9 (founder-gated), W10 | Provider failover needs ratification; integration polish last |

**Wave 11 (added 2026-09-02, AJ):** the Work surface becomes **Packaging Studio** — three commissions (New Presentation, New Document, Commission Research), organization-branded research reports, deliverable actions (rename / duplicate / save / DOCX / PDF), and project tags that auto-file deliverables into Drive folders. It reuses the deep-research lifecycle and the deck engine rather than rebuilding them.

**Wave 12 (added 2026-09-02, AJ):** first-class harness (streaming, wide tools, step cards, budgets) and platform-chosen per-seat providers with Fable 5.1 on the design/judgment seats, an eval before flipping the chat answer seat, and Dissent (`~/Documents/business`: "independent multi-model review for AI-made work") acquired into STRIDE as a founder-owned sub-product: its control plane, assurance and receipts ported to Go behind every judgment seat and Packaging Studio deliverable, plus a founder-only DISSENT admin panel (token flow through every model, cost, latency, assurance analytics, routing controls). External onboarding of non-STRIDE harnesses comes later.

## Founder decisions this plan does not assume

1. Restore OpenAI billing (blocks live verification of every Scout-dependent wave).
2. Claude fallback for Scout's typed seats when the OpenAI circuit is open (Wave 9).
3. Whether private you+Scout threads may enter memory on explicit "remember this" (Wave 8
   ships the seam opt-in only; the implicit-never contract stays pinned).
4. Whether recording stores media (Wave 7 ships MediaRecorder → blob behind a room setting,
   default off).
5. Google Calendar OAuth build vs. scheduled rooms only (Wave 7 ships scheduled rooms).
6. Whether a non-owner may leave a human group on their own (Wave 1 ships owner-managed
   membership only; a "leave group" action is a product call, not a default).

7. **Packaging Studio naming.** The codebase already uses "packaging studio" for the deck chassis/scene engine (`packaging_studio.go`). Wave 11 takes AJ's meaning — the user-facing hub for all three commission kinds — and treats the deck engine as an internal component of it. If AJ wants a different name for the engine's own surfaces, say so; nothing else assumes it.

## Gap map closure (every audited gap has exactly one home)

| Audited gap | Home |
|---|---|
| GIF picker in composers | Wave 10 (polish; `GIPHY_API_KEY` is env-gated in prod) |
| AI assist inside Document Studio (rewrite / summarize / continue) | Wave 4 D9, provider-gated on OpenAI billing |
| Blob GC (`sweepUnreferencedBlobs` unwired, `blobs.go:22`) | Wave 5 D10, admin action + weekly sweep behind the trash purge |
| Work results never reach the ledger | Wave 8 D9 (`run_log` + terminal artifact → ledger event) |
| Fixed 60-entry recall budget and O(N) store clone under the mutex | Wave 8 D10 (budget by query class) and Wave 10 (snappiness budget) |
| Room chat link previews / bubble differentiation (in-call QA leftovers) | Wave 6 D8 |
| ~500KB of zero-effect memory Go (`brain_projection_runtime.go`, `ambient_mind_projection.go`, `stride_person_mymind.go`, `stride_temporal_brain.go`, `stride_conversation_ledger.go`, `canonical_retention.go`) | Out of scope: default-off STRIDE domains stay untouched (rule 8); a fencing decision belongs to AJ |
| Account lifecycle: no disabled/deactivated state on `userAccount`, so offboarded people remain mentionable and addable (surfaced by the Wave 1 code review) | Wave 5 D11: `disabledAt` on accounts + admin toggle; mentions, member pickers, and group validation exclude disabled accounts |
| Recording, scheduling, calendar OAuth, Claude fallback, private-thread remember, leave-group | Founder decisions above; the wave that would consume each ships the default-off or owner-only form |

## Out of scope for this program

The native macOS and iOS clients (another session owns `apple/`), marketplace/workforce
activation, Board retirement, HA/DR, and every default-off STRIDE domain.
