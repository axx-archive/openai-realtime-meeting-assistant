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

Status: `pending`

### Scope Checklist
- [ ] Goal state enum + goalPlan JSON schema persisted on artifact metadata
- [ ] Transition engine (decompose/assign/coordinate/execute, cap 2, children via launchAgentThreadWithOrigin)
- [ ] Review/gate/verify model calls + bounded retries + external-write → approval_required stop
- [ ] save_what_worked + four-line report + gate-outcome/ASSUMED-count fields
- [ ] Boot reconciler (resume all non-terminal mode=goal artifacts)
- [ ] Tests: state table, plan validation, resumability, external-write safety regression

---

## Wave 3

Status: `completed` (commit 4b5b153, reviewed PASS after 3 fixes)
Files: office_events.go (198+), office_events_test.go (289+), memory_query.go +18, codex_proposals.go +10, notifications.go +15, scout_chat_threads.go +11, packages.go +10, index.html +115.
Review fixes: (1) notification os_event leaked message-body excerpts → fixed to kind-derived labels + widened no-leak test; (2) artifact_progress dedup swallowed execute-phase ticks → signature now includes progressPercent/currentStage; (3) board/package refetch now 500ms debounced.
RSS baseline (darwin, keyless): 17,344 KB idle → 23,296 KB after ~1,200 requests — in envelope. Two-session acceptance: PASS (≤2s, no room join).
Risks carried forward: Wave 8 must add an os_event at resolveCodexProposal (approve/reject transition has no event yet); Wave 11 must use osEventHandlers, not a second board refetch; if Wave 7 adds private artifacts, switch emitOSArtifactEvent to owner-scoped sends.

---

## Wave 4

Status: `pending`

### Scope Checklist
- [ ] Client endpointId (localStorage, additive hello field)
- [ ] Server endpoint-keyed sessions + capacity by name + cap 2/account
- [ ] Roster identity + other-device chip + muted-playback self-echo guard + handoff chip
- [ ] endpoint_session_test.go + renegotiation case + existing participants tests green
- [ ] scripts/multi-endpoint-smoke.mjs
- [ ] OPS-1 (ops-agent: commit, .env additions, rsync, rebuild, verify + RSS)

---

## Wave 5

Status: `completed` (commit 8d3e4a7, reviewed PASS)
Files: index.html (+~220), frontend_latency_test.go (pins + TestIndexBonfireOSRenameAndAgentToken). Review fix: bell pulse primed baseline (no on-load fire).
Notes: rail label needed a scoped text-transform:none exception (house lowercase style); ember hairline scoped to [data-tool][aria-pressed] to exclude theme toggle; "office as a place" metaphor copy (login CTA, memory placeholder, "ask the office…") deliberately kept — AJ can overrule.
Risks carried forward: live browser verification deferred to Wave 14 acceptance.

---

## Wave 6

Status: `pending`

### Scope Checklist
- [ ] Allowlist 13→~24 + instruction rewrite (room-only set unchanged)
- [ ] read_thread_aloud / start_chat_as_user (server-stamped) / initiate_goal / also_open + dispatch
- [ ] /goal composer parser → same goal spec
- [ ] Voice island acting/hand-raised + narration + toasts + "what Scout did" ledger
- [ ] Tests incl. disclosure-regardless-of-args regression

---

## Wave 7

Status: `pending`

### Scope Checklist
- [ ] Artifact titles + read-only reader for all
- [ ] Body recall ranking + query expansion
- [ ] relevance lifecycle + search guard (+ archived down-rank)
- [ ] Classifier worker (0.85/0.70, deny-list in code, 2 candidate kinds, segment-level transcripts)
- [ ] Expiry + audit stubs + /assistant/quarantine endpoints (restore all, delete-now admin)
- [ ] Tests: exclusion/restore, deny-list, expiry, idempotence, expansion, reader access

---

## Wave 8

Status: `pending`

### Scope Checklist
- [ ] Morning Brief (incl. quarantine tray, 10-second kit, permissions)
- [ ] Portfolio Health + "how's the portfolio" tool
- [ ] Approval round-trip loop (requester subscription → admin one-tap → origin-surface return)
- [ ] Mobile specs for all three
- [ ] Tests incl. approval round-trip
- [ ] OPS-2 (ops-agent deploy + SLOP_CLASSIFIER_INTERVAL)

---

## Wave 9

Status: `pending`

### Scope Checklist
- [ ] Per-browser suppression strategy (no stacking)
- [ ] Worklet gate demotion + noiseBias removal + soft ducker
- [ ] Default-on desktop + relabeled modes
- [ ] Honest status chip + suppression meter + v8 settings (tolerant migration) + saved-for-this-device
- [ ] Benchmark extension (suppression-dB + onset preservation)
- [ ] Frontend-marker tests

---

## Wave 10

Status: `pending`

### Scope Checklist
- [ ] 12-entry tool registry (single source for palette/parser/voice/engine)
- [ ] Master wrapper as orchestrator scaffold
- [ ] 12 tool bodies + rubrics + kill conditions (existing contracts preserved)
- [ ] Golden evals ×3 + checklist evals ×9 (ship gate)
- [ ] Flywheel wiring (attach-offer, decision-log, context-index, readiness delta)

---

## Wave 11

Status: `pending`

### Scope Checklist
- [ ] Palette (button + `/`; grid; search; keyboard; empty-state handoff)
- [ ] Input modes (inline morph + conversational prefill) → runGoalPipeline
- [ ] Running-state stage-rail card + show-working + controls
- [ ] Terminal states incl. gate outcome + ASSUMED count on the complete card
- [ ] Return-to-origin card (one component, three contexts)
- [ ] Mobile bottom sheet + compressed rail
- [ ] Marker tests + reduced-motion

---

## Wave 12

Status: `pending`

### Scope Checklist
- [ ] start/end_private_grill tools + dispatch (returns instructions; no server session mutation)
- [ ] Client session.update swap + revert + 15-min timer + restart rule
- [ ] 3-act ritual UI (pitch capture / grill / scorecard reveal)
- [ ] Scorecard filing + package attach + readiness dial trend
- [ ] Tests + client-swap frontend marker

---

## Wave 13

Status: `pending`

### Scope Checklist
- [ ] Insertable-streams pipeline + canvas fallback + preview-only honest fallback
- [ ] 4 looks + none (teardown); off by default; iOS opt-in
- [ ] Thermal governor (shared with audio)
- [ ] Settings picker + live preview + v8 persistence
- [ ] video-look-smoke.mjs (far-end assertion)
- [ ] Never-black-tile exception guard

---

## Wave 14

Status: `pending`

### Scope Checklist
- [ ] Remaining delight items + wake-word (gated on voice stability)
- [ ] Device recovery (onended, devicechange, backgrounding, reconnect polish)
- [ ] Catch-me-up + deferred reminders verified
- [ ] Deal Room (cuttable, external_write-gated)
- [ ] Full acceptance: whole-wave demo + quiet-Tuesday + all pillar tests + device matrix
- [ ] OPS-3 final deploy + live verification + memory-file update
