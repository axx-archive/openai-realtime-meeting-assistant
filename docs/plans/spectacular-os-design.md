# Spectacular OS — Unified Design

**Date:** 2026-07-03 · **Synthesized by:** Lead (strategic-design process) from five specialist deliverables
**Inputs:** `spectacular-os-context.md` (brief) · `-product.md` · `-rtc.md` · `-domain.md` · `-technical.md` · `-ux.md`
**Status:** revision 3 — round-2 feedback applied (Quality Hawk ACCEPT 9.5; User Advocate near-miss 9.3, both optional refinements adopted: checklist evals extend the safety floor to all 12 tools; Morning Brief + Portfolio Health moved to Wave 2 to decouple the daily-value floor from Wave-3 risk; also stated the private-grill restart rule from the Hawk's watch-item). Round-1 feedback previously applied (Quality Hawk 9.3 / User Advocate 8.5, both REJECT). Round-1 fixes: push-channel consumer semantics (light-render vs fetch-by-ref); derived VPS concurrency/memory budget with a measured Wave-1 RSS gate; output-quality trust made visible (gate outcome + ASSUMED count on the return card) and checked (golden-output evals as a Wave-3 ship gate); non-admin approval round-trip loop; quiet-Tuesday journey as a first-class acceptance moment; quarantine permissions (restore-for-all, delete-now admin-only); code-count corrections (9× `transition: all`; 13-real-tool allowlist; `applyToolCallArgs` seam verified via `board_worker.go:193`).

---

## Executive Summary

Bonfire already has every organ of an intelligent company — transcripts, brain, board, ledger, packages, agent threads, a gated codex sidecar, notifications, channels, two Scout voices. This wave wires them into one nervous system. The architectural bet: **one /goal pipeline with three doors** (voice, `/goal` text, quick-select menu) that all emit a single goal-spec schema into a persisted, model-agnostic orchestrator (Fable 5 in-process by default, codex sidecar as the swappable executor), whose every completion **returns to the surface where it was born** and feeds a memory that **keeps only what mattered** (quarantine-then-expire slop policy). In parallel, the WebRTC layer becomes genuinely first-class — the mandated same-user desktop+mobile simultaneous join is broken today by a single-session-slot design and gets a multi-endpoint fix — and the whole shell is lifted to A++ under one new design rule: green means a human is live, ember means a machine is working. The wave's sentence: *any request, spoken or typed or clicked, becomes staffed work that returns — visibly, to everyone who needs it, in the place it was born — and the company's memory keeps only what mattered.*

---

## Architecture

### The spine: one pipeline, three doors

```
   VOICE                    TEXT                       MENU
Scout Realtime-2        "/goal <objective>"      Quick-select tool tile
 initiate_goal tool       in any composer          (12 tools, 4 groups)
      │                        │                        │
      └──────────┬─────────────┴───────────┬────────────┘
                 ▼                          ▼
        ┌─────────────────────────────────────────┐
        │           GOAL SPEC (one schema)         │
        │  objective · toolTemplate? · contextRefs │
        │  originSurface · requestedBy · authority │
        │  visibility · packageId?                 │
        └───────────────────┬─────────────────────┘
                            ▼
        ┌─────────────────────────────────────────┐
        │  /goal ORCHESTRATOR — persisted state    │   control plane
        │  machine on the os_artifact record       │   (AgentRunner: anthropic_fable
        │  identify→decompose→assign→coordinate→   │    default, in-process tool-loop)
        │  execute→review→GATE→save→report→verify  │
        │  →commit (external_write only, gated)    │
        └───────────────────┬─────────────────────┘
                            ▼ dispatches capability-matched subtasks
        ┌─────────────────────────────────────────┐
        │  EXECUTION PLANE (model-agnostic)        │   codex_sidecar today,
        │  shell/repo/commit stays sandboxed +     │   claude_sidecar later,
        │  behind approval_required + admin gate   │   swappable by env var
        └───────────────────┬─────────────────────┘
                            ▼ writes
        ┌─────────────────────────────────────────┐
        │  os_artifact (titled, body-recallable)   │
        │  → RETURN CARD to originSurface          │
        │  → unified push channel fans out         │
        │  → package linkage / readiness dial      │
        │  → slop classifier keeps memory dense    │
        └─────────────────────────────────────────┘
```

**Load-bearing boundary (from `codex-goal-workflows.md`, enforced structurally):** Realtime-2 is the control plane — it emits goal specs, opens surfaces, answers from memory, reads results aloud. It never runs shell/SSH/browser/long research. The execution plane never listens to room audio or mutates the live board directly. The `read_only / workspace_write / external_write` authority ladder and the admin approval gate (`main.go:1187`) live in the orchestrator/queue layer, **backend-agnostic** — both Fable-5 and codex paths inherit them; no backend can self-approve an external write.

### Component inventory

| Component | Status | Owner spec |
|---|---|---|
| Goal-spec schema + /goal orchestrator (state machine, boot reconciler) | **NEW** | technical §2 |
| `AgentRunner` interface + `anthropic_fable` / `codex_sidecar` / `openai_text` / stub providers | **NEW** | technical §1 |
| Quick-select tool palette (button + `/` + voice; desktop sheet, mobile bottom sheet) | **NEW** | ux §1 |
| 12-tool packaging suite + master prompt wrapper + per-tool gate rubrics | **NEW content** | domain §1–2 |
| Return-to-origin card + running-state stage rail | **NEW** | ux §1.6–1.7, product Seam A |
| Unified push channel (office ws upgraded to carry room-scoped events) | **UPGRADE (keystone)** | product Seam B, §Integration below |
| Scout private-voice parity (14 → ~24 tools + 5 new) + server-stamped disclosure | **UPGRADE** | technical §3 |
| Private grill (client-driven `session.update`) + 4-phase ritual + readiness dial | **NEW mechanism** | technical §3.3, domain §5, ux §2.6 |
| Slop quarantine (relevance lifecycle + ambient classifier + review tray + 30-day expiry) | **NEW** | technical §4, domain §4 |
| Artifact-body recall fix + query expansion | **UPGRADE** | technical §5 |
| Multi-endpoint WebRTC sessions (same user, desktop+mobile) | **UPGRADE (mandated fix)** | rtc §1.3 |
| Noise suppression as true denoiser + per-browser strategy + honest status chip | **UPGRADE** | rtc §2, ux §6 |
| Video looks pipeline (4 looks, insertable-streams + canvas fallback, thermal governor) | **NEW** | rtc §3 |
| BonfireOS rename + shell polish + `--agent` ember token + delight inventory | **UPGRADE** | ux §3–4 |
| Morning Brief (home of the quarantine tray) · Portfolio Health · Deal Room | **NEW surfaces** | product IN-7, domain §6 |
| Catch-me-up, wake-word presence, re-grill dial, titled artifacts | **UPGRADE (cheap wiring)** | product IN-3/4/5/6 |

Roughly two-thirds upgrade/reuse, one-third new — the correct ratio for a wiring wave.

---

## Detailed Design

### 1. The /goal engine (control plane)

- **What:** A persisted state machine executed by the orchestrator runner. States: `identify_goal → decompose → assign → coordinate → execute_in_order → review_against_goal → gate_before_shipping → save_what_worked → report → verify_goal_completed → (commit_push) → verified`, with `needs_attention` and `approval_required` as human-facing stops. The plan (`metadata["goalPlan"]`, JSON: ≤6 subtasks, dependencies, per-subtask review verdicts) lives on the durable `os_artifact` — the artifact IS the job record; no new datastore.
- **Why:** The 10-step loop is already *scaffolded* in artifact metadata (`agent_thread_runner.go:56–79`) but only narrated by a single text completion. This makes it real, resumable, and inspectable. (Technical §2; the state strings are a superset of what the UI already renders, so the progress card upgrades without breaking.)
- **How:** Decompose/assign/review/gate/report/verify are each model calls with strict JSON contracts. Review checks the draft **against the original goal statement** (immutable in the wrapper), not against the draft. Bounded retries (2 per subtask, 2 replans) then `needs_attention` with a precise blocker line. Concurrency cap: 2 in-flight subtasks (VPS memory). A **boot reconciler** resumes every non-terminal `mode=goal` artifact from its persisted plan after restart — no orphaned states, idempotent re-dispatch via existing keys (`deliveredAt`, `runnerJobId`, `changed`).
- **Inputs/outputs:** goal spec in; titled artifact + four-line completion report + three flywheel writes out (§4).
- **Edge cases:** malformed plan JSON (2 retries → block); provider 429/timeout (backoff → `needs_attention`); external-write detection at the gate forces `approval_required` and the engine stops — the commit/push step runs only as a sandboxed sidecar job after admin approval. Two independent controls, both pre-existing.

### 2. Model-agnostic AgentRunner (the Fable 5 default)

- **What:** One Go interface at the existing `produceAgentThreadArtifactWithWorker` seam (`agent_thread_runner.go:427`), async/channel-based (`RunJob(ctx, job) (<-chan AgentProgress, error)`), with capability flags (`CanShell/CanBrowse/CanEditRepo/CanCommit/ToolLoop/MaxRuntime`). Selection purely by env: `BONFIRE_AGENT_RUNNER` (orchestrator, default `anthropic_fable`) and `BONFIRE_EXECUTION_RUNNER` (default `codex_sidecar`).
- **Why the two-tier split:** Orchestration is reasoning + Bonfire tools, and every Bonfire tool is already an in-process Go function — the registry-wide dispatcher `app.applyToolCallArgs(toolName, args)` (verified: it is exactly what `board_worker.go:193` already calls model-side; `applyPrivateRealtimeVoiceTool` at `kanban.go:2643` is the private-voice wrapper over the same dispatch). An in-process Anthropic Messages tool-loop reuses `applyToolCallArgs` with zero new transport and streams progress in seconds. Execution (shell/repo/commit) stays in the sandboxed sidecar because that's where the isolation and the approval ladder already live. A `claude_sidecar` (claude CLI non-interactive on the same provider-neutral queue contract) is a documented later swap, not the day-one default. (Technical §1.4 — adopted over the CLI-in-sidecar-for-everything option.)
- **Secrets:** `ANTHROPIC_API_KEY` in the main process only, read via a `currentAnthropicAPIKey()` accessor mirroring the OpenAI pattern; never logged, never on artifacts or queue files. Keyless-local falls back to a stub runner — the whole shell stays usable, agentic features degrade cleanly, exactly as today.
- **Back-compat:** existing `BONFIRE_AGENT_THREAD_WORKER=codex_exec` / `BONFIRE_CODEX_AGENT_THREADS` envs alias onto the new selector; no deploy config breaks. The sidecar path keeps its exact queue/callback contract (`/internal/codex/jobs/result`, `BONFIRE_RUNNER_TOKEN`).

### 3. The tool suite and prompt architecture

- **What:** 12 tools in 4 lifecycle groups — **Ideate** (Deep Research, Comps & Precedent, Market Map), **Package** (One-Pager, Deck Outline, Brand & Design Brief), **Market** (Grill/Pressure-Test, Rights & Chain-of-Title, Economics/Waterfall, Talent Match), **Portfolio** (Package Assembly, Investor-Update Memo). Each is a goal preset: name, promise line, stage mapping, declared `inputMode` (inline form vs conversational), authority class, output contract, and a gate rubric with a kill condition. GTM plans, generic summarize/draft/brainstorm, and MBA clichés were deliberately killed. (Domain §1 — the definitive list; the UX palette reads groups and tiles from this registry, so taxonomy lives in one place.)
- **Prompt architecture:** every tool inherits the **master /goal wrapper** — immutable goal statement, grounding in Bonfire's own memory (package artifacts, decisions, prior briefs; "the record wins or you flag the conflict"), the confirmed-vs-assumed evidence discipline, the 10-step loop as executable instructions, and the four-line completion report. Three exemplar tool bodies are fully written (Deep Research, One-Pager, Grill) at production quality; the other nine follow the identical shape. Kill conditions are the studio's non-negotiables (an invented source, an unreceipted claim on a one-pager, an assumed right presented as confirmed, a malformed `READINESS:` line).
- **Preserved contracts:** `research_brief_v2`, `grill_scorecard_v2`, and the machine-parsed `READINESS: X/10` line are already parsed in code (`packages.go`, `agent_thread_runner.go:188`) and are preserved exactly. New contracts (`one_pager_v1`, `deck_outline_v1`, `rights_map_v1`, `economics_scan_v1`, `update_memo_v1`) follow the same exact-headings, machine-checkable shape.
- **Gate rubric execution:** the engine's `review_against_goal` step runs the tool's rubric (dimensions with bars + the kill condition) as a model call scored against the original goal. Below bar → one revision round → re-score → ship or flag.
- **Output-quality trust, made visible and checked (two controls beyond self-score):** (1) the **return card surfaces the gate outcome to the user** — rubric result, and the count of claims the wrapper's evidence discipline marked ASSUMED or unverified ("shipped at bar · 2 claims marked ASSUMED — verify before sending"). Calibrated trust, not blind trust: the user always knows how much of a data-room-bound document is receipts vs inference. (2) **Golden-output evals are a ship gate for the three exemplar tools** (Deep Research, One-Pager, Grill): a fixed eval input per tool (a known package with known receipts) plus an expected-quality checklist (required headings, receipt coverage, kill-condition triggers on a seeded flaw), run before each deploy that touches prompts or the engine. A same-class model self-score alone is not sufficient control for output that goes in front of investors; the eval catches confident-wrong before the user does. (3) **The other nine tools get checklist-only evals** — no golden input, just an automated check that the output contract's required headings are present and that the tool's kill condition fires on a seeded flaw (an invented source, an unreceipted claim, an ASSUMED right presented as confirmed). Near-zero cost per tool, added as each tool's prompt body lands, so all 12 have machine-checked safety floors even where only 3 have full golden fixtures.

### 4. The flywheel and completion discipline

Every tool completion fires **three writes**: (1) offer `attach_to_package`, closing the matching stage `gap` in `packagePayload` and — when the last gap closes — offering a stage advance (the board stops lying); (2) any surfaced decision lands in the decision ledger, linked to the package, so the next tool's wrapper can't contradict it; (3) the artifact body is indexed for recall so the next tool's `{{relevant_artifacts}}` slot pulls it. The chain is literal: Research → One-Pager → Deck → Grill question bank → Assembly → Update Memo, each hop a `packageId`-linked memory read.

The **four-line completion report** (Changed / Headline / Gap / Next) is the only thing that returns to the origin surface and the only thing Scout speaks — the detail lives in the artifact. Fan-out payloads carry titles only (existing trust boundary, `packages.go:593`).

### 5. Scout Realtime-2 parity ("she can do it all")

- **Allowlist growth:** 14 entries today (13 real capabilities + the `do_nothing` no-op, excluded from parity counts) → ~24 real tools. Add `update_artifact`, `publish_artifact`, and the board-mutation set (`create_ticket`, `move_ticket`, `update_ticket`, `add_tags`, `add_key_date`, `remove_key_dates`, `delete_ticket`, `undo_delete_ticket`) — the private instruction line "do not mutate the shared board" is rewritten to "you may update the board on the user's behalf; you are not the room's shared voice." Keep room-only: `set_voice_control`, `set_recording`, `archive_meeting`, `start_grill_session`/`end_grill_session` (they operate shared room state).
- **Five new tools:** `read_thread_aloud` (recall-shaped; the session already outputs audio), `start_chat_as_user` (creates/addresses a thread or channel and posts with a **server-stamped** `postedOnBehalfOf` + rendered "via Scout" chip — disclosure is unconditional, not model-controlled), `initiate_goal` (the voice door; deliberately cannot request `external_write`), `start_private_grill`/`end_private_grill` (below), plus a minimal `also_open` array extension to `control_app` for multi-surface opens.
- **Private grill mechanism (the key technical finding):** the room session is server-owned, but the private session's data channel is **browser-owned** — the server only proxies SDP. So private grill is **client-driven**: the tool dispatch returns the sanitized persona instruction block; the browser applies `session.update` over its own data channel and reverts on end, with a client-side 15-minute safety timer mirroring `defaultGrillMaxDuration`. The graded report files through the normal grill artifact path.
- **The grill ritual** (domain contract + UX set piece): Act I pitch capture (two-minute timer, Scout holds questions), Act II grilling (persona from package artifacts — soft assumptions, ASSUMED rights, hinge economics, contradicting decisions cited by name: "that's not what the June 12 decision says"), Act III scorecard reveal (staggered rows, count-up scores, serif verdict), filed as `grill_scorecard_v2`, attached to the package, **readiness dial** delta spoken ("6.8, up from 6.2"). Private by default; publishable by choice.
- **Voice UX:** the voice island gains `acting` (ember-tinted, narrated task chip) and `hand-raised` (amber, consent pause) states. The three-beat rhythm — announce → act → confirm — for every action; a durable toast as the receipt; a "what Scout did" session ledger with undo where reversible. Announce-before-act is the rule that makes an autonomous agent a colleague, not a poltergeist.

### 6. Slop quarantine + artifact intelligence

- **Data model:** a `relevance` lifecycle on memory-entry metadata: `active` (default, absent = active) → `archived` | `quarantined` → `restored:active` | `expired`. **Reconciliation of specialist scopes:** the Domain Strategist's `keep | archive | quarantine` verdicts map onto this — `archived` is a new state (searchable but down-ranked, exempt from expiry) for dead-package research and published-but-superseded material; `quarantined` is excluded from search/recall/timeline entirely and hard-deletes after 30 visible days, leaving an audit stub. One guard added in `store.search` (`memory.go:712`) excludes quarantined/expired; a rank penalty (not exclusion) applies to archived.
- **Classifier:** a fifth ambient agent on the existing `ambientAgentConfig` recipe (cursor, run-lock, interval — default 6h, `SLOP_CLASSIFIER_INTERVAL`), consuming the Domain Strategist's criteria as its system prompt, emitting strict JSON `{id, verdict, confidence, reason, evidence}`. **Thresholds (reconciled to the stricter spec): quarantine requires confidence ≥ 0.85; archive ≥ 0.7; otherwise keep and re-evaluate.** Bias to keep.
- **Hard deny-list (enforced in the candidate builder, not the prompt):** never touch `decision`, `archive`, `package`/UI-state kinds, published artifacts, package-attached artifacts, human-pinned material, or anything younger than **7 days**. **Candidate scope this wave (reconciled): transcript segments and unpublished/unattached `os_artifact`s only** — chat threads and stale board cards (the Domain doc's broader table) are a documented later expansion once the classifier's precision is proven on the safe classes. Transcripts are judged at segment granularity; a transcript that produced any theme/decision/card keeps its substantive segments.
- **The review surface:** the quarantine tray lives in the Morning Brief. Each entry shows the 10-second decision kit: one-line title, the classifier's reason ("orphaned tool run, never attached, 12 days old"), what it was/wasn't linked to, age + expiry countdown, one-tap Restore / Delete-now, batch confirm. The OS shows you what it chose to forget and lets you overrule it.
- **Permissions:** **Restore is available to all six users** (undoing the classifier is always safe); **Delete-now is admin-only** (`isArtifactApprovalAdmin` — immediate hard-deletion of shared company memory is not a junior-user footgun); auto-expiry applies regardless. Non-admins see the countdown instead of the delete button.
- **Recall upgrades this wave:** index artifact bodies and rank them above card metadata for reconciliation questions (the simulation's D→A fix), plus **query expansion** (2–3 synonyms OR'd into token match, riding the existing domain-term canonicalizer). **Embeddings are an env-gated, designed deferral** (`BONFIRE_RECALL_EMBEDDINGS`, vectors inline in JSONL, in-memory cosine, no vector DB) — the narrow fix closes the demonstrated gap; escalate only if recall misses persist.

### 7. First-class WebRTC

- **The mandated fix — multi-endpoint sessions:** today one participant name = one session slot (`kanban.go:3886–3888`); a same-account second device evicts the first with `session_replaced` (`main.go:2753`). Fix: endpoint identity = `(participantName, endpointId)` — the client mints a stable per-device id in localStorage (so a refreshed tab still replaces *itself*, preserving the zombie-tab protection), the server keys sessions `map[name]map[endpointId]`, capacity still counts distinct names (a person with two devices is one seat), roster renders one identity with a "· 2 devices" affordance, and the same-account second endpoint defaults to muted *playback* of the first endpoint's tracks (self-echo guard) with a one-tap "this is my other device" chip. Endpoints per account capped at 2. Every existing single-session test must stay green.
- **Noise suppression, first-class:** stop the triple-processing (browser NS + RNNoise + hand-rolled gate). Per-browser strategy: Safari/iOS → platform `voiceIsolation` (no WASM on top); Chrome/Firefox desktop → RNNoise as a **true denoiser** (gate demoted to a gentle VAD floor `[0.5,1.0]`, `noiseBias` subtraction removed, browser NS disabled when the worklet is active); Android → platform NS default, voice-focus as labeled opt-in. AEC stays browser-native everywhere. Default **on** (desktop); modes relabeled *voice focus (intelligent) / standard cleanup / raw mic* — intent vs mechanism, mechanism reported honestly. No user training required — this resolves the mandate's "intelligently just suppresses" question in the affirmative and retires re-training as a concept.
- **Video looks:** `MediaStreamTrackProcessor → OffscreenCanvas(WebGL shader) → MediaStreamTrackGenerator` so **the far end sees the look**; canvas-capture fallback on Safari desktop; CSS-preview-only as the honest last resort ("preview only — not supported here"). Four looks as uniform presets on one shader: Bonfire warm (default-available, not default-on), Studio, Mono, Low-light boost, plus always-available "none" that tears the pipeline down entirely. iOS defaults looks off; a shared **thermal governor** sheds look intensity → look → worklet, each step updating the status chip.
- **Settings honesty:** the AV section's status chip is a function of **live audio-graph state** (the diagnostics already computed at `index.html:22896–22984`), never of the selected radio: active (which mechanism) / fallback / loading / unavailable / off, plus a live suppression-dB meter. Video: "Active (far end sees it)" vs "Preview only." Persistence: unified v8 AV settings record (audio + video), per-device+per-account dual-keyed localStorage with a visible "✓ saved for this device," optional soft server-side default hints, never blocking media on a server round-trip.
- **Recovery hardening:** `track.onended` on active mic/camera (auto-switch to next preferred, `replaceTrack`, honest toast); `devicechange` triggers recovery when the active device vanished, offers (never forces) a switch when a better one appears; mobile visibility-restore resumes suspended AudioContext and rebuilds ended tracks; reconnect choreography polished (counter only from attempt ≥2, manual Rejoin only after auto-recovery is exhausted).

### 8. Design system: BonfireOS + the ember rule

- **Rename:** "Office" → **BonfireOS** in all eight label locations (rail chip, aria-labels, `toolTitles` incl. the phone `'bonfire'` special-case, topbar fallbacks, launch section) — **labels only, never the `office` data-tool key**, which is load-bearing across `TOOL_IDS`/selectors.
- **The one new token:** `--agent` (ember, #FF7A2B family) with exactly one meaning: *a machine is doing work*. Green (`--signal`) keeps its monopoly on *a human is live/speaking*. They never co-occur. Every agent surface — /goal stage rail, palette active mark, bell warmth, Scout's `acting` state — pulls from this single token.
- **The delight inventory:** 15 moments ranked by earn-rate; budget spent on the rare payoffs (artifact-complete ember burst, grill scorecard assemble) and near-zero on the frequent ones (message send). The taste rule: animate state, not decoration; nothing loops; nothing fires on load; everything appears in the `prefers-reduced-motion` block (state kept, motion dropped) as a ship gate.
- **Monolith discipline:** namespaced component classes (`.palette__*`, `.goalcard__*`, `.grillstage__*`), token-only values, no `transition: all` — and remove **all 9 existing `transition: all` occurrences** (verified count; the first is `index.html:375`), replacing each with named properties — documented z-index ladder slots, and the 13-point review checklist (ux §7.4) applied to every index.html diff. Sequencing rule: land Go/schema contracts first, then frontend integrates against stable contracts; index.html edits serialize into one lane to avoid merge collisions.
- **Mobile parity as a gate:** every new surface ships its phone spec with it — palette as bottom sheet (no keyboard auto-open), drawer nav with safe areas, floating tab bar (hide-on-scroll-down), keyboard-avoiding composer, settings as full-height sheet with full-width option rows and sticky live preview.

### 9. The extra surfaces riding this wave

- **Morning Brief** — composed from existing snapshots (pending approvals, overnight results, board deltas, unread channels); the home of the quarantine tray. No new ambient workers.
- **Portfolio Health** — the whole book on one screen: every package's stage, readiness dial + trend, freshness (days since movement), open gaps; "Scout, how's the portfolio?" speaks the summary. Pure aggregation over `venturePackagePayloads`.
- **Deal Room** — one-tap read-only shareable export of a package's assembled binder, behind the `external_write` admin approval gate. The moment internal intelligence becomes an external asset. Sequenced last; explicitly cuttable under timeline pressure without harming the spine.
- **The approval-wait loop is a first-class closing loop (non-admin experience of the gate):** when any of the five non-admins triggers an external-write action (Deal Room share, Investor-Update send, a /goal that wants to commit), the gate must not feel like a request vanishing into AJ's queue. Spec: the requester immediately sees the honest waiting state on their card ("queued for AJ's sign-off") **and is subscribed to the outcome**; AJ gets an approval notification with a one-tap approve/reject from the bell and the Morning Brief; on approval, the **approved asset returns to the requester's origin surface** with an "approved · sent" state and a notification to the requester — the person who asked still owns the outcome. On rejection, the card returns with AJ's one-line reason. This rides the push channel (`proposal` events) and the existing approval endpoints; it is the explicit countermeasure to re-running the "value lands only for the admin" failure on the wave's highest-status actions.
- **Cheap wiring wins:** catch-me-up for late joiners; titled artifacts + read-only reader for everyone (prerequisite, not extra); re-grill readiness dial; wake-word presence (shell "takes a breath" on "Scout" — gated on RTC/voice stability so it never false-fires).
- **Deferred with reasons (product §4):** Scout-interjects, consensus detection, the Panel, market twin, overnight staff (judgment-quality or standing-infra risk — wrong risk profile for a "works flawlessly" wave); embeddings (narrow fix first); codex app-server broker (sidecar suffices).

---

## Integration Points

### The unified push channel (the keystone — reconciled and specified)

The Product Lead named this the wave's true keystone; no specialist owned its full design, so the synthesis specifies it. **Foundation exists:** the office live websocket shipped in the roadmap-foundations wave (`office_socket_test.go`; office-mode sessions already hold a socket). The upgrade: make that socket the **single event stream every authenticated session consumes**, in room or not, carrying typed events:

```
event := {kind: artifact_completed | artifact_progress | proposal | notification |
                channel_post | package_advanced | quarantine_change,
          ref, title, originSurface, actor, at}
```

- Server: one `broadcastOSEvent(event)` seam that the artifact terminal path, proposal store, notification store, channel post path, and package linkage all call — replacing the per-surface polling/snapshot reads the simulation's quick fixes papered in.
- Client: one consumer that routes events by kind, with **two consumer classes** (this is the fan-out contract):
  - **Light consumers render directly from the event payload** — the bell (kind + title + actor), the return card's arrival (title + `originSurface` + ref link), Morning Brief counters (increment by kind). The event carries everything they show.
  - **Rich consumers treat the event as an invalidation signal and fetch-by-ref on receipt** — the board re-reads the affected card/column, the package rail re-reads `packagePayload` (new stage/readiness), the quarantine tray re-reads `/assistant/quarantine`. Events are therefore notifications *about* state, not carriers of it; a missed event self-heals because the same fetch runs on the next snapshot read — that is precisely what "self-heals" means.
  - Consumers are idempotent by `(kind, ref, at)`; replaying an event is a no-op re-fetch. **The polling fallback stays in place until the two-session acceptance test passes** (a change made in session A visibly lands in session B's office view within 2s, no room join, no reload).
- Risk posture: highest blast radius in the wave; lands early (Wave 1), gated hard, additive (polling remains as fallback until proven).

### Contract table (who consumes what)

| Contract | Producer | Consumers |
|---|---|---|
| Goal spec schema (with `originSurface`, `toolTemplate`, `authority`) | all three doors | orchestrator, return card, push channel |
| `goalPlan` JSON on `os_artifact` | orchestrator | progress card (stage rail), boot reconciler, tests |
| Tool registry (12 entries: id, group, promise, inputMode, stage map, authority, rubric ref) | domain | palette UI, `/goal` parser, Scout `initiate_goal`, engine |
| Output contracts (`research_brief_v2`, `grill_scorecard_v2` + `READINESS:`, 5 new `*_v1`) | tool prompts | parsers in `packages.go`/`agent_thread_runner.go`, gate rubric calls |
| `postedOnBehalfOf` + "via Scout" chip | server stamp | thread/channel render, audit, tests |
| `relevance` lifecycle + classifier JSON verdict | classifier worker | search guard, quarantine tray, expiry job, audit stubs |
| endpointId hello field | client | session admission, capacity, roster, native-config untouched (additive/optional) |
| v8 AV settings record | settings UI | audio graph, looks pipeline, status chip |
| Push-channel event schema | `broadcastOSEvent` | bell, return cards, board, package rail, Morning Brief |

### Conflict resolutions (logged)

1. **Tool taxonomy naming:** the UX mockup's group names were placeholders; the Domain Strategist's 12-tool / Ideate-Package-Market-Portfolio taxonomy is canonical. The palette reads groups from the registry, so this is data, not layout.
2. **Slop candidate scope:** Domain proposed four candidate classes; Technical's deny-list allowed two. Resolved conservatively: transcripts + unpublished/unattached artifacts this wave; threads/board-cards as a documented later expansion.
3. **Classifier thresholds:** 0.80 (technical default) vs 0.85 (domain). Resolved to **0.85** for quarantine, 0.70 for the new `archived` state — stricter where deletion is downstream.
4. **`archived` state:** Domain's keep/archive/quarantine verdicts required a third lifecycle value beyond Technical's active/quarantined/expired. Added: `archived` = searchable, down-ranked, exempt from expiry.
5. **Private grill ownership:** Product journey assumed a server-side instruction swap symmetric with room grill; Technical proved the private data channel is browser-owned. The client-driven `session.update` mechanism is canonical; the UX ritual is unchanged.
6. **Push channel:** asserted by Product, unowned by any specialist — specified above as a first-class Wave-1 deliverable on the existing office-socket foundation.
7. **Video look default:** UX's picker implies opt-in; RTC's "Bonfire warm (default)" meant *default selection when enabled*. Resolved: looks ship **off** by default everywhere; "Bonfire warm" is the first suggestion.

---

## Migration / Rollout

- **No schema migrations.** Goal plans, relevance lifecycle, disclosure stamps, and endpoint ids are all additive metadata; absent = today's behavior. AV settings migrate v7→v8 tolerantly (unknown versions degrade to defaults). Existing artifacts, threads, packages, and tests keep working; env aliases preserve deploy configs.
- **Keyless-local keeps working** (`go run .` on :3000): stub runner for agentic features, looks/suppression are pure client-side, multi-endpoint needs no key, push channel needs no key. Native Apple `/native/config` contract untouched (endpointId additive/optional).
- **Wave sequencing (for /wave-plan):**
  1. **Wave 1 — Foundations (parallel lanes):** goal-spec schema + `AgentRunner` interface + orchestrator skeleton (lane A); unified push channel + two-session acceptance gate (lane B); multi-endpoint WebRTC sessions + smoke harness (lane C, fully independent); BonfireOS rename + `--agent` token + monolith conventions (lane D, sets the bar).
  2. **Wave 2 — Doors + intelligence + the daily-value floor:** `/goal` text parser (cheapest proof of the schema); Scout parity growth + new tools + disclosure; artifact titles/reader + body recall + query expansion; slop lifecycle + classifier + tray; noise-suppression overhaul + honest chip; **Morning Brief + Portfolio Health land here, not Wave 3** — they are pure aggregation over existing snapshots with no dependency on the /goal payoff machinery, and sequencing them early protects the quiet-Tuesday daily magic even if Wave 3 slips. (The quarantine tray joins the Brief as soon as the classifier ships in this same wave.)
  3. **Wave 3 — The suite + the payoff:** tool registry + 12 prompts + palette (desktop + mobile) + running-state card + return-to-origin card (incl. visible gate outcome + ASSUMED-claim count); private grill (client swap + ritual, incl. a stated restart rule: an in-flight private grill survives a server restart because the session and instruction swap are browser-owned — the client's 15-min timer and revert run regardless, and only the report-filing call retries against the recovered server); video looks; approval round-trip loop. **Ship gate: golden-output evals green on Deep Research, One-Pager, Grill; checklist evals green on every tool whose prompt body has landed.**
  4. **Wave 4 — Polish + capstones:** delight inventory pass (incl. wake-word, gated on voice stability); device-recovery hardening; re-grill dial surfacing; Deal Room (cuttable); full device-matrix + whole-wave acceptance demo; deploy.
  - Each wave gates on `go test ./...` + its own harness; index.html edits serialize within each wave.
- **Deploy:** unchanged ops — rsync to `/opt/meetingassist`, compose rebuild, verify `thebonfire.xyz` + container health; `.env` gains `ANTHROPIC_API_KEY`, `BONFIRE_AGENT_RUNNER`, `BONFIRE_EXECUTION_RUNNER`, `SLOP_CLASSIFIER_INTERVAL`.
- **Server concurrency/memory budget (derived, not asserted):** the 4GB VPS today carries the Go process (Pion room for ≤6 people + realtime proxying + 4 ambient workers on staggered intervals), coturn, Caddy, and the *opt-in* codex sidecar (compose profile `codex` — the largest tenant, unchanged by this wave). What the wave adds server-side: (a) **≤2 in-flight orchestrator tool-loops** — each is goroutines + HTTP calls to the Anthropic API + bounded message buffers, no local model, RSS cost in the tens of MB, and the cap is enforced in the engine, not hoped for; (b) **one more ambient worker** (the classifier) on the same staggered-interval recipe as the existing four — one batch model call per tick, minBatch-gated; (c) the push channel, which reuses the existing office websocket (connection count unchanged: ≤6 users × their sessions); (d) nothing for looks/suppression (pure client-side) and nothing for embeddings (off by default). Aggregate: the wave's steady-state server addition is two bounded HTTP-loop workers and one low-cadence worker — comfortably inside the envelope the four existing workers already prove out. Contention mitigations if measurement disagrees: drop orchestrator concurrency to 1 (env), lengthen classifier interval, keep the sidecar profile off outside work hours. **Wave 1 records the process RSS before/after as a measured gate**, so the budget is verified on the real box, not assumed.

### The quiet-Tuesday test (day-to-day texture, no big /goal)

"Feels like magic" must hold on a low-intensity portfolio-support day, not just during a packaging push. The journey: Joel opens BonfireOS with coffee — the Morning Brief greets him by name with what moved overnight (the classifier quarantined two orphaned drafts, one approval is waiting on AJ, #dealflow has four unread). Portfolio Health surfaces one nudge: "Nimbus hasn't moved in 11 days — its rights map still has two ASSUMED items." He taps it, says "Scout, what are the two open rights questions?" and she reads them from the artifact body. He says "Scout, tell Maya the rights follow-up is on her — and remind me after tomorrow's call." A disclosed post lands in #nimbus, Maya's bell warms, and a deferred reminder queues. Total time: three minutes, zero /goal launches, and the OS demonstrably *watched the book, tended the memory, and moved a real ball* — the ambient layer (brief, health, classifier, bell warmth, read-aloud, deferred nudges) is what makes the OS feel alive between pushes. This journey is an acceptance moment with equal standing to the demo below.

### Acceptance — the whole-wave demo (no cuts)

Maya joins late on her phone while Tyler shares on desktop and Joel joins simultaneously (multi-endpoint + simul-join, no flicker); "Scout, catch me up" delivers a private summary; "Scout, pull a comp set for Nimbus and put it in #nimbus" — she keeps talking, the card returns to #nimbus, Joel's bell lights though he never entered the room; Joel types `/goal one-pager for Nimbus` and watches the identical staged card; Maya has Scout grill her privately and hears "6.8, up from 6.2"; the Nimbus board stage auto-advances on-screen; next morning Tyler's Morning Brief shows three quarantined items with reasons and he restores one with a tap. Plus the pillar-level tests: three-doors test, gate test (external write stops at approval on **both** backends), the **approval round-trip test** (a non-admin requests an external write, AJ approves from the bell, the approved asset returns to the requester's origin surface with a notification), the **golden-output eval** green on the three exemplar tools, honesty chip vs ground truth per browser, rename test, mobile-parity test, the quiet-Tuesday journey, and the two safety regressions (no external_write without an approval record; every on-behalf post carries the server stamp).

---

## Open Questions

None blocking wave planning. Three documented judgment calls, decided: (1) embeddings deferred behind an env gate with the design written; (2) Deal Room rides but is the designated cut under pressure; (3) slop classifier scope limited to the two safe candidate classes this wave. Each is reversible without rework.

---

## Appendix: Specialist Attribution

- **Product Lead (`-product.md`):** the wiring-wave thesis, one-pipeline/three-doors spine, seams A–D, journey set + acceptance demo, idea triage (IN/DEFER logic), push-channel-as-keystone call, sequencing skeleton.
- **Media/RTC Engineer (`-rtc.md`):** multi-endpoint session design + root-cause (§7), noise-suppression per-browser strategy + gate demotion, video-looks pipeline + thermal governor, settings v8 schema, recovery hardening, verification harness + device matrix.
- **Domain Strategist (`-domain.md`):** the 12-tool suite + kills, master wrapper + exemplar prompts + gate rubrics, flywheel three-writes + four-line report, slop criteria + edge rules + classifier prompt, grill ritual contract, Deal Room + Portfolio Health.
- **Technical Analyst (`-technical.md`):** AgentRunner interface + provider table + two-tier split recommendation, /goal state machine + plan schema + boot reconciler, parity classification + five new tool schemas + client-driven private grill finding, quarantine data model + deny-list, recall verdict + embeddings deferral design, risk register + test strategy.
- **UX Designer (`-ux.md`):** palette (desktop/mobile) + input modes + running-card stage rail, voice-island state vocabulary + narration + disclosure UX + grill set piece, rename table + shell polish list, ember token + delight inventory + taste rule, mobile parity specs, monolith discipline + review checklist.
- **Lead synthesis:** push-channel specification, the seven conflict resolutions, the contract table, wave sequencing, and the `archived` lifecycle state.
