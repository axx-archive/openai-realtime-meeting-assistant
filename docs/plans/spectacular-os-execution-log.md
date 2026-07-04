# Spectacular OS — Execution Log

Date created: 2026-07-03
Owner: Bonfire OS (AJ Hart)
Primary plan: `docs/plans/spectacular-os-execution-plan.md`
Design doc: `docs/plans/spectacular-os-design.md`
Branch: `main`

---

## How To Use This Log

1. Execute exactly one wave per session.
2. Do not begin the next wave until this log marks the current wave as `completed`.
3. After each wave, update: wave status, checklist, files changed, validation commands + outcomes, risks/blockers, and the ready-to-paste prompt for the next wave.
4. **Compact previous waves** after completing a wave: reduce each completed wave to `Status / Files / Risks carried forward` (2–3 lines). Remove old prompts, checklists, validation detail.
5. Output the next-wave prompt directly in your final response — the user copy/pastes it into a fresh context window.

---

## Agent Team Structure

Every wave uses a real team (spawned teammates + SendMessage). Minimum: **lead + dev + reviewer**. All teammates use the strongest available model (pass `model: "opus"`; if the runtime offers a stronger tier — e.g. Fable — omitting `model` to inherit it is equally acceptable).

**Core roles (every wave):**
| Role | `subagent_type` | Responsibility |
|---|---|---|
| **lead** | (executing agent) | Coordinates, delegates, validates via reports, compacts log, writes next-wave prompt. Never reads source or writes code. |
| **dev** | general-purpose | Implements deliverables in a fresh context. Talks to reviewer via SendMessage. |
| **reviewer** | feature-dev:code-reviewer (fallback: general-purpose with a harsh-review prompt) | Reads the actual diff; critiques correctness, patterns, edge cases; DMs dev for fixes; reports pass/fail. |

**Checkpoint waves only:** **ops-agent** (general-purpose) — reads AGENTS.md + `bonfire-vps-deploy-ops` conventions, commits, rsyncs, rebuilds compose, verifies live; **asks the user before any SSH/push**. The lead never does ops itself.

**Team sizing per wave:**

| Wave | Dev role(s) | Optional specialists | Notes |
|---|---|---|---|
| 1 | backend-dev | — | Go only; interface + providers |
| 2 | backend-dev | critic (safety: gate regression) | State machine is high-stakes |
| 3 | backend-dev + frontend-dev | — | Go seam + client consumer |
| 4 | backend-dev + frontend-dev | ops-agent (OPS-1) | Session model + smoke script |
| 5 | frontend-dev | designer (general-purpose + frontend-design/make-interfaces-feel-better skills) | index.html only |
| 6 | backend-dev + frontend-dev | critic (disclosure regression) | Parity + doors |
| 7 | backend-dev | critic (deny-list regression) | Memory + classifier |
| 8 | backend-dev + frontend-dev | ops-agent (OPS-2) | Surfaces + approval loop |
| 9 | frontend-dev (audio) | — | Worklet + settings |
| 10 | backend-dev + prompt-engineer (general-purpose) | critic (eval design) | Registry + prompts + evals |
| 11 | frontend-dev ×2 (palette / cards) | designer or visual-qa | Largest UI wave; serialize index.html regions |
| 12 | backend-dev + frontend-dev | — | Tools + client swap + ritual |
| 13 | frontend-dev (media) | — | Shader pipeline + smoke |
| 14 | frontend-dev + backend-dev | visual-qa + ops-agent (OPS-3) | Polish + acceptance + deploy |

**Context budget:** every dev prompt inlines the specs it needs (from the spec docs' relevant sections) + file line-ranges; no dev reads a whole spec doc or all of index.html. index.html regions are assigned disjointly when two devs touch it.

---

## Ops Debt Tracker

No SQL migrations in this project — ops = `.env` additions, docker compose rebuild, rsync deploy, live verification.

**Deploy sequence (memory: bonfire-vps-deploy-ops + AGENTS.md):**
1. `go test ./...` green locally (VPS has no Go)
2. Commit to `main` with a descriptive message
3. rsync committed tree to `root@146.190.171.224:/opt/meetingassist` — **preserve `.env`, `data/users.json`, `data/sessions.json`**
4. `docker compose up -d --build` in `/opt/meetingassist/deploy/digitalocean/`
5. Verify `https://thebonfire.xyz` + `docker compose ps` health + relevant logs

### Pending Ops

| Source Wave | Type | Item | Status |
|---|---|---|---|
| 1 (expected) | Config | `.env` += `ANTHROPIC_API_KEY`, `BONFIRE_AGENT_RUNNER=anthropic_fable`, `BONFIRE_EXECUTION_RUNNER=codex_sidecar`, `BONFIRE_ORCHESTRATOR_MODEL=claude-fable-5` | expected |
| 3 (expected) | Verify | Record server RSS before/after on the real box (budget gate) | expected |
| 7 (expected) | Config | `.env` += `SLOP_CLASSIFIER_INTERVAL` (default 6h) | expected |

### Ops Checkpoints

| Checkpoint | After Wave | Covers |
|---|---|---|
| OPS-1 | 4 | Waves 1–4 (foundations live: runner, engine, push channel, multi-endpoint) |
| OPS-2 | 8 | Waves 5–8 (shell, parity/doors, memory intelligence, Brief/Portfolio/approval loop) |
| OPS-3 | 14 (final) | Waves 9–14 (AV, suite, grill, looks, polish) + full live acceptance |

### Applied Ops

(none yet)

---

## Wave 1

Status: `completed` (commit 5782f17, reviewed PASS)
Files: agent_runner_iface.go (267), agent_runner_anthropic.go (607), agent_runner_providers.go (120), agent_thread_runner.go (+68/-19), 2 test files (11 tests). Review fixes: orchestrator create_ticket forced draft=true (D4 gate); non-end_turn stop_reasons → needs_attention with orchestratorStop stamp.
Risks carried forward: live per-turn progress streaming deferred to Wave 3 seam (onProgress hook exists, passed nil); refusal `fallbacks` beta deferred (env-gate later); orchestrator timeout 5m may need raising for long goals; CanBrowse=false until a web tool is wired.
Deviations (accepted): keyless default = openai_text (today's exact behavior); stub via BONFIRE_AGENT_RUNNER=stub.

<details><summary>original scope</summary>

Status: `pending`

### Scope Checklist
- [ ] `agent_runner_iface.go` — `AgentJob`/`AgentCapabilities`/`AgentProgress`/`AgentRunner`
- [ ] Goal-spec fields threaded onto thread launch
- [ ] `anthropic_fable` in-process Messages tool-loop runner + `currentAnthropicAPIKey()`
- [ ] Existing worker modes wrapped as `AgentRunner` providers at the dispatch seam
- [ ] Stub runner + `selectAgentRunner` env selection + back-compat aliases
- [ ] Tests: selection matrix, fake-runner progress mapping, mock-endpoint tool-loop round trip

### Prompt For Wave 1

(see final response of the planning session — also reproduced below)

```
Continue Spectacular OS implementation on branch main.

Source of truth:
1) docs/plans/spectacular-os-execution-plan.md — read ONLY "Critical Rules" and "Wave 1" details
2) docs/plans/spectacular-os-execution-log.md — read ONLY the Wave 1 section and Ops Debt Tracker
3) docs/plans/spectacular-os-technical.md — read ONLY §0 (current-system seams) and §1 (agent-runner abstraction; contains the full Go interface definition to implement)

Execute ONLY Wave 1 (AgentRunner foundation). Do not start Wave 2.

## What previous waves shipped
Nothing — this is the first wave. The design was accepted at 9.5/10 by a dual-critic loop; all architecture decisions are settled. Do not re-litigate them.

## Wave 1 scope
- Create agent_runner_iface.go with the AgentJob / AgentCapabilities / AgentProgress / AgentRunner types exactly as specified in technical §1.1 (async channel contract).
- Thread goal-spec fields (objective, toolTemplate, contextRefs, originSurface, requestedBy, authority, visibility, packageId) onto agent-thread launch — additive metadata, absent = today's behavior.
- Implement the anthropic_fable runner: raw Anthropic Messages API tool-use loop, in-process, tools mapped to app.applyToolCallArgs (the registry-wide dispatcher defined at kanban.go:2543; board_worker.go:193 shows the call pattern: `toolResult, changed, err := app.applyToolCallArgs(toolName, args)`). Add currentAnthropicAPIKey() mirroring currentOpenAIAPIKey (agent_thread_runner.go:478). Hard maxTurns cap. Reuse apiRequestFailedError (openai_responses.go:148) so upstream bodies never reach the browser. Model from BONFIRE_ORCHESTRATOR_MODEL, default "claude-fable-5".
- Wrap the three existing worker paths as AgentRunner providers at the produceAgentThreadArtifactWithWorker seam (agent_thread_runner.go:427 — the existing 3-way switch: local codex exec / sidecar enqueue / OpenAI text). The sidecar provider's RunJob enqueues and emits one {queued} progress then closes; the existing HTTP callback (/internal/codex/jobs/result) still lands the terminal state. No behavior regression on any existing path.
- Stub runner for keyless-local (mirrors the "worker not configured" artifact at agent_thread_runner.go:454) + selectAgentRunner reading BONFIRE_AGENT_RUNNER / BONFIRE_EXECUTION_RUNNER, with back-compat: BONFIRE_AGENT_THREAD_WORKER=codex_exec and BONFIRE_CODEX_AGENT_THREADS map to codex providers.
- Tests: (a) selection matrix over all env values incl. aliases and keyless fallback; (b) a fake AgentRunner drains a scripted progress channel and the artifact metadata (progressPercent/currentStage/goalStatus/reviewGate) updates correctly at each step — no network; (c) anthropic_fable against a mock Messages endpoint (injection mirroring openAITextResponder at openai_responses.go:55) asserting a tool_use → applyToolCallArgs → tool_result round trip terminates.

## Inline Context
- The AgentRunner interface, AgentJob/AgentCapabilities/AgentProgress structs, provider table, and the anthropicFableRunner sketch are written out IN FULL in technical §1.1–§1.3 — implement those definitions verbatim (they were code-verified during design review).
- Key seams (verified against source): produceAgentThreadArtifactWithWorker at agent_thread_runner.go:427; worker-mode selection configuredAgentThreadWorkerMode at codex_runner.go:50 and configuredCodexRunnerMode at codex_runner.go:69; queue enqueue/callback in codex_runner_queue.go (authorities :18-28, callback handler :687); applyToolCallArgs defined kanban.go:2543.
- House rules: go test ./... must pass; keyless `go run .` must keep working; never log or persist the API key; additive metadata only.

## Execution (Team)
You are the lead. You coordinate — you do NOT implement. Never read source files or write implementation code yourself.

1) Create tasks via TaskCreate for the six checklist items.
2) Spawn one dev via the Agent tool with name "backend-dev", model "opus" (or inherit the strongest available), subagent_type "general-purpose". Its prompt: the "Wave 1 scope" + "Inline Context" sections above verbatim, plus: "Read technical §0–§1 for the full interface definitions, then read only the cited line ranges of agent_thread_runner.go, codex_runner.go, codex_runner_queue.go, openai_responses.go, and the applyToolCallArgs definition region of kanban.go (~2543-2700). Implement, run go test ./..., report files changed + test output."
3) When the dev reports: spawn a reviewer (subagent_type "feature-dev:code-reviewer" if available, else general-purpose) with: "Review the Wave-1 diff on main (git diff HEAD~1 or unstaged). Check: interface fidelity to technical §1.1, no behavior regression on the codex/openai paths, key never logged, keyless fallback, test quality. DM backend-dev about issues; they fix; re-check. Report pass/fail."
4) After reviewer passes: send shutdown_request to teammates.
5) Update the execution log: Wave 1 status completed, checklist, exact files changed, commands + outcomes, risks; append any new ops to Pending Ops; update the execution plan wave map; write the Wave 2 prompt (state machine — inline the goalPlan JSON schema from technical §2.2 and the state enum from §2.1 so Wave 2's dev doesn't re-read the whole spec).
6) Commit with message "Wave 1: model-agnostic AgentRunner foundation (Fable 5 default)".
7) Output the Wave 2 prompt in full in your final response, in a code fence.

Critical rules: (from the execution plan — include the full Critical Rules section when delegating anything that touches safety gates, index.html, or memory kinds.)

Requirements: keep branch main; go test ./... green; compact nothing yet (first wave); the Wave 2 prompt must inline the state enum + goalPlan schema + failure/retry table from technical §2.
```

---

(end of original Wave-1 scope)</details>

## Wave 2

Status: `completed` (commit 7779513, reviewed PASS after 3 critical fixes)
Files: goal_engine.go (~1,500), goal_engine_test.go (~600), agent_thread_runner.go, agent_runner_iface.go, codex_runner_queue.go, kanban.go:500 (reconciler wire).
Review fixes: sidecar subtasks now fold via internalCodexRunnerResultHandler (goals no longer hang; fold paths mutually exclusive); artifactRunnerActionHandler approve branch routes mode=goal → resumeApprovedGoal (vetted command, not raw-text job); commit_push idempotent via mode=goal_commit child + CommitChildID; child authority clamped ≤ parent.
Risks carried forward: Wave 6 — voice initiate_goal calls launchGoalThread and needs a graceful keyless fallback; sidecar re-derives authority from text (deferred, comment in code) — Wave 6 should honor the engine's clamp. Wave 8 — add requester-notification round-trip on approve/reject (resume path already wired). Wave 10 — tool prompts inject at (e *goalEngine).decompose system prompt. Nice-to-have: HTTP-level test for the approve-routing branch. Narrow known window: crash between commit-child create and CommitChildID persist could duplicate one job (consistent with existing dispatch pattern).

---

## Wave 3

Status: `completed` (commit 4b5b153, reviewed PASS after 3 fixes)
Files: office_events.go (198+), office_events_test.go (289+), memory_query.go +18, codex_proposals.go +10, notifications.go +15, scout_chat_threads.go +11, packages.go +10, index.html +115.
Review fixes: (1) notification os_event leaked message-body excerpts → fixed to kind-derived labels + widened no-leak test; (2) artifact_progress dedup swallowed execute-phase ticks → signature now includes progressPercent/currentStage; (3) board/package refetch now 500ms debounced.
RSS baseline (darwin, keyless): 17,344 KB idle → 23,296 KB after ~1,200 requests — in envelope. Two-session acceptance: PASS (≤2s, no room join).
Risks carried forward: Wave 8 must add an os_event at resolveCodexProposal (approve/reject transition has no event yet); Wave 11 must use osEventHandlers, not a second board refetch; if Wave 7 adds private artifacts, switch emitOSArtifactEvent to owner-scoped sends.

---

## Wave 4

Status: `completed` (reviewed PASS; commit follows as OPS-1)
Files: kanban.go (~110 net — endpoint-keyed sessions, presence-transition gating), main.go (+105 — connection index, hello parse, activeWebsocketHandlers race fix), index.html (+207 — endpoint mint, roster affordance, handoff chip), websocket_auth_test.go (+54), endpoint_session_test.go (309, 9 tests), scripts/multi-endpoint-smoke.mjs (189).
Review fixes: per-device join/left broadcasts caused third-party tile teardown → presence now gates on true person-level transitions (firstEndpoint/stillPresent); bonus: idle-meeting timer no longer arms on partial departure. Pre-existing ws data race fixed (handler drain counter; production never blocks).
**Decisions (deliberate deviations from rtc §1.3.4-5, reviewer-accepted):** (1) self-echo guard = structural same-name track exclusion (acceptsTrack), NOT muted-playback + chip — simpler and echo-proof by construction; (2) one-visible-tile-per-account; true dual-tile per endpoint deferred to follow-up task #24 (~15 call-site re-key, UI risk). "· N devices" affordance reflects presence count honestly.
Smoke: 12/12 keyless two-context same-account (docstring corrected to claim only what it tests). Full-package -race green (340s).
Risks carried forward: dual-tile follow-up (#24); smoke script requires ms-playwright cache (self-SKIPs otherwise).

---

## Wave 5

Status: `completed` (commit 8d3e4a7, reviewed PASS)
Files: index.html (+~220), frontend_latency_test.go (pins + TestIndexBonfireOSRenameAndAgentToken). Review fix: bell pulse primed baseline (no on-load fire).
Notes: rail label needed a scoped text-transform:none exception (house lowercase style); ember hairline scoped to [data-tool][aria-pressed] to exclude theme toggle; "office as a place" metaphor copy (login CTA, memory placeholder, "ask the office…") deliberately kept — AJ can overrule.
Risks carried forward: live browser verification deferred to Wave 14 acceptance.

---

## Wave 6

Status: `completed` (commit 449606d, reviewed PASS after 1 fix)
Files: kanban.go +211, scout_chat_threads.go +221, index.html +434, main.go +84 (/assistant/goal), codex_runner_queue.go +11 (stamped-authority handoff), wave6_scout_parity_test.go (290), 2 test-contract updates.
Allowlist 14 → 27 (single source of truth). Review fix: /goal parser was dead code (insertion aborted mid-write; substring test missed it) — wired into sendScoutChatFromForm, test hardened to in-body assertion, live-verified keyless.
Risks carried forward: originSurface not yet persisted by goal_engine (stamps originKind/Id/MeetingId only) — Wave 11 follow-up if the return card needs it; private-voice system prompt grew (27 schemas) — measure session payload size at acceptance; Wave 8 hooks = hand-raised state + ledger; Wave 12 rides island states.

---

## Wave 7

Status: `completed` (commit 0cc84f6, reviewed PASS after 1 fix)
Files: memory.go +150, memory_query.go +15, domain_terms.go +70, slop_classifier.go (808), main.go (boot + 2 routes, committed with Wave 6), mission_intelligence.go +1 (leak guard), 2 test files (527).
Delete-path audit: deleteEntryByID has exactly 2 callers (expiry sweep, admin endpoint), both gated, both audited. Review fix: Mission Intelligence read raw entriesOfKind → quarantined titles could leak into its prompt — guarded.
Deviations (accepted): boot registration in main.go; own worker loop so expiry rides every tick; keep verdicts terminal (idempotence/cost over re-eval).
Risks carried forward: goal_engine boot reconciler reads raw entriesOfKind (a quarantined non-terminal goal could still be re-driven — edge case, next goal_engine owner); queryPrefersArtifactContext unguarded but leaks nothing (routing bool only). Wave 8 tray consumes GET /assistant/quarantine (payload shape in dev report); event is title-only — tray must fetch detail.

---

## Wave 8

Status: `completed` (commit 101209e + UI in dfbcddf, reviewed PASS after 3 fixes)
Files: office_brief.go (470+), office_brief_test.go (330+), codex_proposals.go, codex_runner_queue.go, kanban.go (portfolio_health tool), main.go (2 routes), office_events.go (gate-entry seam), index.html office-home region.
Review fixes: reject double-fire (transition guard mirroring approve paths + idempotency test); two hardcoded colors → tokens. Dev-added beyond spec: admin gate-entry bell notification (once per gate entry, admin-only) + one-tap bell approve/reject with inline reason.
Risks carried forward: Wave 11 return card should share renderApprovalRow's waiting-card vocabulary + register alongside the existing osEventHandlers consumer; board deltas only as granular as board-worker summaries; OPS-2 code push done (dfbcddf) — VPS deploy batched to OPS-3.

---

## Wave 9

Status: `completed` (commit dfbcddf, reviewed PASS after 3 fixes)
Files: index.html audio/settings regions, public/voice-focus/rnnoise-processor.js (+62), scripts/voice-focus-benchmark.mjs (+147), frontend_latency_test.go, frontend_noise_suppression_test.go (6 tests + in-body wiring test).
Review fixes: silent-worklet-crash chip lie (onprocessorerror handler + 4s staleness guard); mislabeled fallback text unified; marker tests hardened to functionBody() scoping (the Wave-6 lesson applied).
Benchmark evidence: onset retention 0.28→0.49 (1.75×), suppression 11.3dB speech-over-fan / 9.2dB fan-only. Dev also fixed initial-capture double-processing (raw mediaConstraints on first getUserMedia).
Risks carried forward: Wave 13 rides the v8 video sub-record ({look, lookIntensity, preferredCamera}, look enum validated in normalizeVideoSettings — extend it); privacy-mute machinery dormant by design.

---

## Wave 10

Status: `completed` (commits 5c9856f + 6fe017a, reviewed PASS after 1 fix round)
Files: tool_registry.go (471), tool_prompts.go (635), evals_test.go (496), goal_engine.go (+~160), agent_runner_anthropic.go, agent_thread_runner.go, main.go (+/assistant/tools), kanban.go (+2).
Review fix (the program's most important): A++ prompts reached only decompose/review/gate, never GENERATION — goalDeliverableSubtaskID now stamps ToolTemplate on the deliverable subtask; both runner paths hand the writer the full tool body. Flywheel wired incl. the decision-log seam (report() emits optional decision → appendDecision + package link); scope note: logs the goal's settled decision, not mid-run subtask decisions.
Deviations (reviewer-accepted): package_binder_v1 contract id; memo maps to stage `assembled` (no portfolio stage exists).
Risks carried forward: Wave 11 renders GET /assistant/tools directly (pre-ordered); Wave 14 Deal Room = package_assembly + external gate AT THE SHARE SURFACE (the tool itself is deliberately not gated); investor_update_memo already forces the approval gate.

<details><summary>original scope</summary>


### Scope Checklist
- [ ] 12-entry tool registry (single source for palette/parser/voice/engine)
- [ ] Master wrapper as orchestrator scaffold
- [ ] 12 tool bodies + rubrics + kill conditions (existing contracts preserved)
- [ ] Golden evals ×3 + checklist evals ×9 (ship gate)
- [ ] Flywheel wiring (attach-offer, decision-log, context-index, readiness delta)

</details>

---

## Wave 11

Status: `completed` (commit b703fc5, reviewed PASS after 4 fixes)
Files: index.html ~+840 (palette/goalcard/returncard, z-index 1340), wave11_palette_test.go (8 tests), goal_engine.go (originSurface stamp), office_events.go (event prefers originSurface).
Review fixes: originSurface was never persisted (goal_engine now stamps it; events carry it; 2 end-to-end tests); focus trap rebuilt as one live DOM-level handler; mobile keyboard auto-open guarded; + Tools hit target extended to 44px via pseudo-element.
Deviations (accepted): no pause/cancel until a backend endpoint exists (⋯ = open-artifact + hide, honestly labeled); conversational tiles don't stamp toolTemplate — ticket #25 (real cross-wave change: chat-threads pipeline has no toolTemplate concept).

---

## Wave 12

Status: `completed` (commit 1bc4d1e, reviewed PASS after 3 fixes + 2 nits)
Files: grill.go +229, kanban.go +69, index.html +668 (.grillstage__*), wave12_private_grill_test.go (329), realtime_config_test.go +11.
Review fixes: safety-timer revert was a no-op → forcePrivateGrillEnd POSTs the end tool directly (model-independent hard revert); grounding-content injection defense (sanitizeGrillGroundingText + DATA framing, attack-tested with a planted '# Tools' in a real artifact); provisional-grade honesty caption; z-index 1370; Act-II question wiring.
Risks carried forward: verify live that session.update lands on gpt-realtime-2 (Wave 14/OPS-3); [close] button is nudge-only (15-min cap bounds worst case) — cheap hard-revert candidate for Wave 14; binder trend comes from attached-artifact sequence (readinessDelta only stamps same-thread re-grills).

---

## Wave 13

Status: `completed` (commits 18b6db3 + UI in b703fc5, reviewed PASS after 2 rounds / 4 issues)
Files: public/video-looks/look.frag (157) + video-look-pipeline.js (603), scripts/video-look-smoke.mjs (172), frontend_video_looks_test.go (247), index.html ~650 (settings picker + capture seam).
Review fixes: false-Active status on construction failure; WebGL context-loss silent-blank-frames (webglcontextlost listener); track-end {done:true} failover (the realistic unplug case — reviewer tested the real API); one-shot settled guard against teardown races.
Smoke: 4/4 far-end look signatures on the loopback-received track, independently reproduced by review.
Risks carried forward (device matrix): governor under real thermal load (hot Android); Safari canvas tier + iOS default-off on real devices; off-room preview may prompt for camera once.

---

## Wave 14

Status: `completed` — code (commit 090f446, reviewed PASS after 5 fixes across two devs; first dev died at session limit mid-wave, second audited + finished). **OPS-3 live verification REMAINS OPEN** (human step).
Files: deal_room.go (560) + deal_room_test.go, acceptance_test.go, private_voice_payload_test.go, docs/plans/spectacular-os-acceptance.md (the manual matrix/demo/quiet-Tuesday scripts), index.html +~630, kanban.go/main.go/meetings.go/memory.go/frontend_latency_test.go.
Review fixes: Deal Room foreign-artifact exposure (package-scoped binder resolution + proof test) — the critical one; mic-recovery loop bounded (3 attempts/backoff/honest give-up); payload claim turned into a regression gate (<64KB bound + instructions-only swap marker); TestIndexWave14PolishMarkers rewritten functionBody-scoped (false comment removed); Deal Room buttons to 44px on phone. First dev's ReferenceError (undefined loadDealRooms) caught by the audit.
Shipped: wake-word (OFF-default, 4 arming preconditions, no transcript leak), delight items 6/12/14/15, device recovery per rtc §5, Deal Room capstone (crypto/rand tokens, revoke→404, escape-safe renderer, gate at the share surface).

### OPS-3 — REMAINING (execute per docs/plans/spectacular-os-acceptance.md)
1. Deploy: rsync committed tree (090f446) to the VPS per bonfire-vps-deploy-ops (preserve .env + data/users.json + sessions.json), compose rebuild, verify thebonfire.xyz + container health. **`.env` needs: ANTHROPIC_API_KEY (activates Fable orchestration), BONFIRE_AGENT_RUNNER=anthropic_fable, BONFIRE_EXECUTION_RUNNER=codex_sidecar, SLOP_CLASSIFIER_INTERVAL=6h.**
2. Device matrix (acceptance doc §B) on real devices — incl. same-account macOS+iPhone simul-join, iOS lock/resume, Android thermal, looks rows.
3. Wake-word live (§C), Deal Room end-to-end (§E), pillar spot-checks with real OpenAI (§A8-13), quiet-Tuesday journey (§D).
4. Verify live: private grill session.update lands on gpt-realtime-2 (W12 risk); re-grill dial delta across a real "again".
5. Update the memory file with deploy + live-verification dates.

---

## PROGRAM SUMMARY (2026-07-04)

All 14 waves code-complete on main: 8d3e4a7 (W5) → 5782f17 (W1) → 4b5b153 (W3) → 7779513+2c0d418 (W2) → 216d147 (W4, OPS-1 push) → 449606d (W6) → 0cc84f6 (W7) → 101209e (W8) → dfbcddf (W9, OPS-2 push) → 5c9856f+6fe017a (W10) → 1bc4d1e (W12) → 18b6db3 (W13) → b703fc5 (W11) → 090f446 (W14, OPS-3 code push). Full suite green at every commit. **35 real bugs caught by the adversarial review layer pre-commit**, including: the A++ prompts never reaching generation (W10), goals hanging on sidecar subtasks + a dead approval path (W2), a notification body leak on the push channel (W3), third-party tile teardown on multi-device join (W4), a no-op grill safety revert + grounding injection (W12), WebGL context-loss blank frames (W13), a spoofable-disclosure attempt defeated (W6), and a foreign artifact exposable behind a public token (W14).
Open follow-ups: #24 dual-visible-tile per endpoint; #25 conversational tiles stamping toolTemplate; OPS-3 live verification above.
