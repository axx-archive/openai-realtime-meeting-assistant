# First-Class Chat - Execution Log

Date created: 2026-06-21

Primary plan: `docs/plans/first-class-chat-execution-plan.md`

Branch: `main`

## How To Use This Log

Execute one wave per fresh Codex session unless the user explicitly asks for multiple waves in one run. Read the design first, then this log. Update the current wave after validation. Keep completed waves compact: status, files, validation, and carried risks only.

## Ops Debt Tracker

- No production deploy has been performed for this planning commit.
- Future deploy-affecting waves must remember the VPS is not a git checkout at `/opt/meetingassist`.
- If worker behavior changes, rebuild both `meetingassist` and the `codex-runner` Compose profile.
- Preserve the Docker `meeting_data` volume and Codex `codex_home` volume.
- Do not expose Codex app-server websocket transport publicly.
- External-write capabilities remain approval-gated.
- Future compaction work must verify current OpenAI Responses/compaction API shape before coding against it.
- Future compaction work must decide `BONFIRE_CHAT_STATE_ENCRYPTION_KEY` before storing sensitive opaque state outside the existing `0600` data volume.
- Future visibility changes beyond `private` require the ACL matrix from `docs/plans/first-class-chat-design.md`.

## Planning Gate Record

Status: `pre_commit_validated`

Source evidence reviewed during this planning run:

- Repo code: `scout_chat.go`, `main.go`, `agent_thread_runner.go`, `codex_runner.go`, `codex_runner_queue.go`, `openai_responses.go`, `memory.go`, `accounts.go`, `index.html`, and `deploy/digitalocean/docker-compose.yml`.
- Live `/readyz`: confirmed `codexRunner.enabled:true`, `worker:"codex_exec"`, `runnerMode:"sidecar_queue"`, `callbackSecured:true`, `heartbeatOK:true`, `codexCwd:"/workspace/meetingassist"`, and `workspaceGit:true`.
- Live Compose: confirmed `digitalocean-codex-runner-1` running and `digitalocean-meetingassist-1` healthy.
- Official OpenAI docs: latest model guide, Realtime voice agents guide, deployment compaction guidance, conversation-state compaction guide, standalone compaction guide, and `/v1/responses` OpenAPI spec.
- Codex manual: skills, MCP, app-server, and subagents sections.
- Subagents: current-system explorer, deployment/runtime explorer, and two critic rounds.

Critic-loop outcomes:

- Round 1 Quality Hawk: `REJECT`, 8.8/10. Required stronger planning gate evidence, exact HTTP contracts, thread state machine, durability rules, OpenAI adapter verification, privacy matrix, and tests.
- Round 1 User Advocate: `REJECT`, 9.2/10. Required source-goal matrix, full wave prompts, saved learnings, durability/encryption/retention detail, and trust evidence.
- Round 2 User Advocate: `ACCEPT`, 9.6/10.
- Round 2 Quality Hawk: `REJECT`, 8.9/10. Required current gate record, exact endpoint errors, single-writer/file-lock story, OpenAI adapter specifics, concrete ACL/audit fields, and mandatory target tests.
- Current revision addresses the Round 2 Quality Hawk rejection in the design appendix, execution plan, and this log.
- Round 3 Quality Hawk re-review: `ACCEPT`, 9.6/10, no blocking issues. Closeout note was to run a staged diff check before commit because these planning docs are new files.

Validation record:

- `git diff --check`: passed on 2026-06-21 12:00 PDT with no output.
- `go test -count=1 ./...`: passed on 2026-06-21 12:00 PDT, `ok github.com/openai/openai-realtime-meeting-assistant 16.398s`.

Final close fields:

- Commit hash: recorded in final response after commit creation. A commit cannot contain its own final hash without changing that hash.
- Push destination: `axx/main`, recorded in final response after push.
- VPS deploy/restart: no deploy, rebuild, restart, or production file sync is part of this planning-only commit.

## Mandatory Wave Test Matrix

Wave 1 target tests:

- Files: `chat_store_test.go`, `chat_http_test.go`, `assistant_http_test.go`.
- Required negatives: unauthenticated `{"error":"..."}`, cross-origin write rejection, cross-user read/list as `404`, malformed JSON, invalid cursor, payload too large, idempotency collision, archived write conflict, feature disabled behavior, corrupt JSONL readiness, stale temp-file readiness, and old websocket compatibility.

Wave 2 target tests:

- Files: focused frontend static tests, `frontend_latency_test.go` if appropriate, and `chat_http_test.go` when API assumptions are exercised.
- Required negatives: archived composer disabled, stale/deleted thread unavailable state, mobile no-overflow, feature disabled fallback to old websocket behavior, and no false Codex worker state without evidence.

Wave 3 target tests:

- Files: `openai_responses_test.go`, `chat_compaction_test.go`, `chat_http_test.go`, `memory_query_test.go`.
- Required negatives: no network in unit tests, no raw machine state in browser payloads, artifacts, `/readyz`, logs, or intelligence events; `store:false` checked; compact output passed as-is; `phase` preserved; fallback marks `machineStateUnavailable:true`.

Wave 4 target tests:

- Files: `capabilities_test.go`, `chat_store_test.go`, `codex_runner_test.go`.
- Required negatives: unavailable tools not advertised, runner tokens/paths/env/Codex home omitted, explicit `$critic-loop`, `$strategic-design`, and `$wave-plan` persisted and routed.

Wave 5 target tests:

- Files: `codex_runner_test.go`, `codex_runner_queue_test.go`, `agent_thread_runner_test.go`, `chat_http_test.go`.
- Required negatives: commit/push/deploy/SSH/email/external API phrases require approval, duplicate callbacks idempotent, stale callbacks rejected, rerun lineage preserved, and UI/status text never claims Codex work without runner evidence.

Wave 6 target tests:

- Files: `intelligence_store_test.go`, `intelligence_http_test.go`, `memory_query_test.go`, `chat_http_test.go`.
- Required negatives: private turns do not emit org events, machine compaction state never emits intelligence, org view returns structured signals/source refs only, owner/admin review states enforced, visibility revocation respected.

Wave 7 target tests:

- Files: `codex_app_server_test.go`, `capabilities_test.go`, `chat_http_test.go`.
- Required negatives: no public app-server route/websocket/TCP listener, fake transport only in tests, backend single-writer preserved, feature disabled by default.

Wave 8 target tests:

- Files: integration/readiness tests plus operator-doc assertions where existing test patterns support them.
- Required negatives: old endpoint compatibility, feature-flag rollback, secret-free readiness, corrupt JSONL readiness, backup/restore doc coverage, no VPS deploy unless explicitly requested.

## Wave 1

Status: `pending`

### Scope Checklist

- [ ] Add JSONL-backed chat thread store.
- [ ] Add JSONL-backed chat turn store.
- [ ] Add initial compaction record type, even if compaction execution waits for Wave 3.
- [ ] Enforce lock/clone/atomic rewrite/idempotency/payload-limit rules.
- [ ] Add authenticated chat thread endpoints.
- [ ] Enforce owner filtering by normalized account email.
- [ ] Add `BONFIRE_FIRST_CLASS_CHAT` feature flag.
- [ ] Add readiness checks when the flag is enabled.
- [ ] Add focused store/API tests.
- [ ] Preserve current websocket chat behavior.

### Files Changed

(filled after execution)

### Validation

(filled after execution)

### Risks / Blockers

(filled after execution)

### Prompt For Wave 1

```markdown
Continue First-Class Chat on branch main.

Source of truth:
1. `docs/plans/first-class-chat-design.md` - read Architecture, Data Model, API Surface, Migration And Rollout, and Testing And Acceptance.
2. `docs/plans/first-class-chat-execution-plan.md` - read Critical Rules and Wave 1.
3. `docs/plans/first-class-chat-execution-log.md` - update Wave 1 when done.
4. Current code anchors: `accounts.go`, `auth_http.go`, `memory.go`, `main.go`, `assistant_http_test.go`.

Execute ONLY Wave 1: Chat Store Primitives And APIs. Do not start Wave 2.

## Previous Waves

None. The accepted design and execution plan were committed as planning artifacts. Current product behavior is still the pre-existing ephemeral private chat plus durable artifact-thread system.

## Scope

- Implement JSONL-backed stores for `chat_thread`, `chat_turn`, and initial `chat_compaction` records.
- Add authenticated endpoints for listing, creating, reading, patching, and forking chat threads.
- Enforce per-user ownership using normalized account email from `userFromRequest`.
- Follow the design's durability rules: locked clone-on-read, append for new records, atomic temp-file rewrite for updates, optional `requestId` idempotency, payload limits, corrupt-line readiness reporting, and `0600` file permissions.
- Follow the design's HTTP contracts for auth errors, `404` on inaccessible private ids, pagination clamps, archived records, fork lineage, malformed payloads, and disabled feature behavior.
- Add `BONFIRE_FIRST_CLASS_CHAT` as a feature flag. When disabled, new endpoints can return 404 or a disabled response, but tests should cover the enabled path.
- Add readiness checks for chat store files when enabled.
- Keep existing `/assistant/query`, `/assistant/threads`, `/artifacts`, and websocket `scout_chat` behavior unchanged.

## Inline Context

Current store pattern:

- `memory.go` uses a locked in-memory slice plus JSONL append/rewrite.
- `accounts.go` normalizes email with `normalizeAccountEmail`.
- `main.go` uses `writeAuthJSON`, `writeAuthError`, and `userFromRequest` for authenticated endpoints.

Target records are described in `docs/plans/first-class-chat-design.md`.

## Execution

Implement the scoped changes, respecting existing repo patterns and current developer instructions. If using subagents, split store and HTTP work into disjoint ownership, and do not duplicate delegated work.

## Validation

- `go test -count=1 ./...`
- `git diff --check`
- Focused tests for unauthenticated rejection, per-user filtering, archive/restore, fork lineage, malformed payloads, and readiness when the flag is enabled.
- Additional focused tests for cross-origin rejection, inaccessible thread id as `404`, pagination clamp, idempotent create/update/fork, payload rejection, corrupt-line skip, and old websocket compatibility.

## Requirements

1. Do not start Wave 2 UI work.
2. Keep edits limited to Wave 1.
3. Record files changed, validation results, and risks in this log.
4. Keep old chat behavior working.
5. Write the full Wave 2 prompt in this log and final response.
6. Add deploy-affecting notes to the Ops Debt Tracker.
```

## Wave 2

Status: `pending`

### Scope Checklist

- [ ] Add persisted thread list UI.
- [ ] Support create/rename/archive/restore/switch.
- [ ] Persist active thread id client-side.
- [ ] Keep mobile drawer and panes overflow-safe.
- [ ] Preserve existing chat send behavior while binding it to active thread where Wave 1 allows.
- [ ] Render archived/unavailable states safely.

### Done Criteria

- A signed-in user can create two private threads, switch between them, refresh, and see the same active thread.
- Archived threads move out of the active list and cannot receive new messages until restored.
- Existing chat fallback still works when `BONFIRE_FIRST_CLASS_CHAT` is disabled.
- Mobile thread list behaves as a drawer/sheet and does not create horizontal overflow.

### Prompt For Wave 2

```markdown
Continue First-Class Chat on branch main.

Source of truth:
1. `docs/plans/first-class-chat-design.md` - read UI Design, API Contract Appendix, Thread State Machine, and Privacy matrix.
2. `docs/plans/first-class-chat-execution-plan.md` - read Critical Rules and Wave 2.
3. `docs/plans/first-class-chat-execution-log.md` - update Wave 2 when done and compact Wave 1 if completed.

Execute ONLY Wave 2: Persisted Chat UI And Resume/Switch Behavior. Do not start Wave 3.

## Previous Waves

Wave 1 should have added chat stores and authenticated endpoints behind `BONFIRE_FIRST_CLASS_CHAT`.

## Scope

- Add first-class thread list UI to the Chat tab in `index.html`.
- Load/create/rename/archive/restore/switch threads through the Wave 1 API.
- Persist the active thread id client-side and recover it on refresh.
- Render archived threads separately and disable composer for archived threads until restored.
- Render inaccessible/deleted thread loads as a neutral unavailable state.
- Keep existing `scout_chat` websocket fallback working when the feature flag is disabled.
- Keep mobile drawer/pane behavior overflow-safe.

## Validation

- `go test -count=1 ./...`
- `git diff --check`
- Static/frontend tests for thread list markers, active thread id persistence, archive controls, disabled archived composer, unavailable thread state, mobile no-overflow rules, and old websocket fallback.
- Manual local browser smoke if feasible.

## Requirements

1. Do not implement Responses execution or compaction.
2. Keep edits mostly in `index.html` plus focused tests.
3. Update this log with files changed, validation, risks, and the full Wave 3 prompt.
```

## Wave 3

Status: `pending`

### Scope Checklist

- [ ] Add persisted turn execution endpoint.
- [ ] Store Responses state.
- [ ] Add compaction execution.
- [ ] Create machine state plus human-readable artifact.
- [ ] Add compaction lifecycle UI.
- [ ] Verify current OpenAI compaction API shape before implementation.
- [ ] Prove machine state is never exposed to browser/logs/artifacts.

### Done Criteria

- Ordinary chat turns persist user and assistant turns with model profile, response id/state metadata, usage, and latency.
- Compaction creates an opaque machine-state record and a redacted human-readable artifact.
- UI shows compaction started/completed state.
- If machine compaction is unavailable, the system stores a normal summary artifact and marks `machineStateUnavailable:true`.

### Prompt For Wave 3

```markdown
Continue First-Class Chat on branch main.

Source of truth:
1. `docs/plans/first-class-chat-design.md` - read Responses State Engine, Compaction ledger, API Contract Appendix, Durability And Limits, Privacy matrix, and OpenAI Adapter Verification.
2. `docs/plans/first-class-chat-execution-plan.md` - read Critical Rules and Wave 3.
3. `docs/plans/first-class-chat-execution-log.md` - update Wave 3 when done.

Execute ONLY Wave 3: Responses State And Compaction Ledger. Do not start Wave 4.

## Previous Waves

Wave 1 added chat stores/APIs. Wave 2 added persisted Chat UI and thread switching.

## Scope

- Reverify current OpenAI Responses state/compaction docs or spec before coding.
- Add a small adapter for stateful continuation, stateless item replay, and compaction so tests use fakes.
- Implement `POST /assistant/chat/threads/{id}/turns`.
- Store response id, state metadata, selected model profile, usage, latency, and turn text.
- Implement `POST /assistant/chat/threads/{id}/compact`.
- Store machine compaction state exactly and server-only.
- Create a redacted human-readable `os_artifact` companion summary.
- Emit or return compaction lifecycle state for UI animation.
- Add fallback `machineStateUnavailable:true` behavior.

## Validation

- `go test -count=1 ./...`
- `git diff --check`
- Fake OpenAI adapter tests for turn execution and compaction.
- Tests proving machine state is not returned to browser payloads, `/readyz`, artifacts, logs, or intelligence events.
- Redaction tests for human compaction artifacts.

## Requirements

1. Do not add capability registry or org intelligence yet.
2. Do not log raw machine state.
3. Update this log with files changed, validation, risks, and the full Wave 4 prompt.
```

## Wave 4

Status: `pending`

### Scope Checklist

- [ ] Add capability registry.
- [ ] Add skill routing and snapshots.
- [ ] Add `/assistant/capabilities`.
- [ ] Persist selected skills/tool availability on threads and turns.
- [ ] Add safe unavailable-capability reasons.

### Done Criteria

- `GET /assistant/capabilities` returns a signed-in user's safe visible capability inventory.
- `$critic-loop`, `$strategic-design`, and `$wave-plan` mentions persist on turns/threads.
- Codex runner health is reflected without leaking private config.
- Unavailable tools are not advertised as usable.

### Prompt For Wave 4

```markdown
Continue First-Class Chat on branch main.

Source of truth:
1. `docs/plans/first-class-chat-design.md` - read Capability Registry, Skill And Tool Availability, Privacy matrix, and Trust Evidence Appendix.
2. `docs/plans/first-class-chat-execution-plan.md` - read Critical Rules and Wave 4.
3. `docs/plans/first-class-chat-execution-log.md` - update Wave 4 when done.

Execute ONLY Wave 4: Capability Registry And Skill Routing. Do not start Wave 5.

## Previous Waves

Personal chat threads, UI resume/switching, Responses turns, and compaction should exist.

## Scope

- Add capability registry with static built-ins and runtime health probes.
- Include Bonfire tools, Realtime tools, Codex runner, OpenAI hosted-tool slots, MCP slots, and skills.
- Add `GET /assistant/capabilities`.
- Capture capability snapshots on thread creation and work launch.
- Persist explicit skill mentions on turns/threads.
- Route selected skills into worker prompt metadata.
- Add or plan repo/plugin-scoped availability for `critic-loop`, `strategic-design`, and `wave-plan`.

## Validation

- `go test -count=1 ./...`
- `git diff --check`
- Registry tests for authority classes, health states, unavailable tools, and safe output.
- Tests that explicit skill mentions persist and reach worker prompts.

## Requirements

1. Do not copy user-home skills into the repo unless licensing and scope are clear.
2. Do not leak private runner paths, tokens, or config in capability output.
3. Update this log with files changed, validation, risks, and the full Wave 5 prompt.
```

## Wave 5

Status: `pending`

### Scope Checklist

- [ ] Link personal chat turns to Codex work artifacts.
- [ ] Improve runner evidence visibility.
- [ ] Add approval-required timeline states.
- [ ] Harden rerun/resume.

### Done Criteria

- A chat turn can launch a Codex-backed work artifact and show queued/running/blocked/verified state in Chat.
- Runner job ids, evidence, selected skills, and approval state are linked to both chat turns and artifacts.
- External-write requests still stop at `approval_required`.
- Rerun/resume creates a clear new run without corrupting old evidence.

### Prompt For Wave 5

```markdown
Continue First-Class Chat on branch main.

Source of truth:
1. `docs/plans/first-class-chat-design.md` - read Codex Work Thread Bridge, Codex Runner Versus App-Server, Thread State Machine, Capability Registry, and Privacy matrix.
2. `docs/plans/first-class-chat-execution-plan.md` - read Critical Rules and Wave 5.
3. `docs/plans/first-class-chat-execution-log.md` - update Wave 5 when done.

Execute ONLY Wave 5: Codex Work-Thread Integration Hardening. Do not start Wave 6.

## Previous Waves

Personal chat threads, compaction, and capability snapshots should exist.

## Scope

- Link personal chat turns to `/assistant/threads` artifacts.
- Store runner job ids, evidence, approval state, terminal status, selected skills, and capability snapshot ids in chat metadata and artifact metadata.
- Improve Codex worker prompts to include selected skills and capability snapshot.
- Show queued/running/approval_required/blocked/verified states in Chat.
- Add rerun/resume from a personal thread.
- Preserve the sidecar queue and external-write approval gate.

## Validation

- `go test -count=1 ./...`
- `git diff --check`
- Fake runner callback tests.
- Approval gate tests for commit, push, deploy, SSH, email, and external API phrases.
- Tests that UI/status copy does not claim Codex work without runner evidence.

## Requirements

1. Do not add org intelligence yet.
2. Do not weaken external-write gates.
3. Update this log with files changed, validation, risks, and the full Wave 6 prompt.
```

## Wave 6

Status: `pending`

### Scope Checklist

- [ ] Add intelligence event store.
- [ ] Emit permissioned org-level events.
- [ ] Add Scout org view endpoint/UI.
- [ ] Prove private chats do not leak.
- [ ] Add saved-learning review flow.

### Done Criteria

- Approved published/shared artifacts can emit structured intelligence events.
- Private chat turns do not emit org events.
- Saved learnings have pending/approved/rejected review state.
- Scout org view cites source refs and shows structured signals, not raw private chat.

### Prompt For Wave 6

```markdown
Continue First-Class Chat on branch main.

Source of truth:
1. `docs/plans/first-class-chat-design.md` - read Scout Organizational Intelligence Layer, Saved Learnings, Privacy matrix, and Testing.
2. `docs/plans/first-class-chat-execution-plan.md` - read Critical Rules and Wave 6.
3. `docs/plans/first-class-chat-execution-log.md` - update Wave 6 when done.

Execute ONLY Wave 6: Org Intelligence Ledger And Scout View. Do not start Wave 7.

## Previous Waves

Personal chat, compaction, capability routing, and Codex work integration should exist.

## Scope

- Add append-only intelligence event store.
- Emit permissioned events from published/shared artifacts, approved compaction artifacts, meeting brain summaries, blockers, decisions, and saved lessons.
- Add saved-learning proposal/review flow with pending/approved/rejected.
- Add `GET /assistant/intelligence` with visibility filtering.
- Add source references back to artifacts, turns, meetings, and memory ids.
- Add Scout org view UI for allowed signals.
- Ensure proactive records are recommendations only, not mutations.

## Validation

- `go test -count=1 ./...`
- `git diff --check`
- Per-user visibility tests.
- Tests proving private chat turns and machine compaction state do not create org events.
- Tests proving org view returns structured signals and source refs only.

## Requirements

1. Do not add Codex app-server broker in this wave.
2. Keep raw private chat out of org intelligence.
3. Update this log with files changed, validation, risks, and the full Wave 7 prompt.
```

## Wave 7

Status: `pending`

### Scope Checklist

- [ ] Spike backend-only Codex app-server broker.
- [ ] Map Bonfire threads to Codex threads.
- [ ] Stream app-server events into the timeline.
- [ ] Keep disabled by default until security review.

### Done Criteria

- A fake app-server transport can create/resume a Codex thread and stream item events into a Bonfire thread in tests.
- No public app-server listener is exposed.
- Feature remains disabled by default.
- Decision record explains whether app-server should supplement or replace `codex exec` for rich workflow tabs.

### Prompt For Wave 7

```markdown
Continue First-Class Chat on branch main.

Source of truth:
1. `docs/plans/first-class-chat-design.md` - read Codex Runner Versus App-Server, Privacy matrix, Trust Evidence Appendix.
2. `docs/plans/first-class-chat-execution-plan.md` - read Critical Rules and Wave 7.
3. `docs/plans/first-class-chat-execution-log.md` - update Wave 7 when done.

Execute ONLY Wave 7: Optional Codex App-Server Broker Spike. Do not start Wave 8.

## Previous Waves

Core personal chat, compaction, capabilities, Codex sidecar integration, and org intelligence should exist.

## Scope

- Prototype backend-only broker to `codex app-server` over stdio or Unix socket using a fake transport in tests.
- Map Bonfire `chat_thread.id` to Codex `threadId`.
- Stream app-server item/turn events into Bonfire timeline events.
- Keep `BONFIRE_CODEX_APP_SERVER=false` by default.
- Produce a decision note in the execution log: app-server supplement vs replacement for `codex exec`.

## Validation

- `go test -count=1 ./...`
- `git diff --check`
- Fake JSON-RPC transport tests.
- Tests proving no public listener exposure or unauthenticated access.

## Requirements

1. Do not deploy or enable the broker by default.
2. Do not expose app-server websocket remotely.
3. Update this log with files changed, validation, risks, and the full Wave 8 prompt.
```

## Wave 8

Status: `pending`

### Scope Checklist

- [ ] Consolidate readiness, docs, and rollout flags.
- [ ] Run full local QA.
- [ ] Prepare deploy checkpoint, if requested.

### Done Criteria

- Readiness reports chat store, compaction, capability registry, intelligence store, and runner health without leaking secrets.
- README/operator docs explain flags, backup, rollback, and deployment.
- Full local validation passes.
- Deploy instructions are ready but not executed unless explicitly requested.

### Prompt For Wave 8

```markdown
Continue First-Class Chat on branch main.

Source of truth:
1. `docs/plans/first-class-chat-design.md` - read Testing, Migration And Rollout, Trust Evidence Appendix, and Current Planning Gate.
2. `docs/plans/first-class-chat-execution-plan.md` - read Critical Rules and Wave 8.
3. `docs/plans/first-class-chat-execution-log.md` - update Wave 8 when done.
4. `AGENTS.md` - deployment rules if and only if the user explicitly asks to deploy.

Execute ONLY Wave 8: Full QA, Rollout Flags, Readiness, And Deploy Checkpoint.

## Previous Waves

Core implementation waves should be complete.

## Scope

- Consolidate readiness output for chat store, compaction, capability registry, intelligence store, and Codex runner.
- Update README/operator docs for flags, data paths, backup, rollback, and runner profile rebuild requirements.
- Run full local QA.
- Prepare a deployment checkpoint. Do not deploy, SSH mutate, rebuild, or restart production unless the user explicitly asks.

## Validation

- `go test -count=1 ./...`
- `git diff --check`
- Local server smoke for `/healthz`, `/readyz`, auth gates, and chat endpoints.
- If deployment is explicitly requested: follow `AGENTS.md`, back up VPS files/data, sync committed tree, rebuild Compose including codex profile when needed, and verify live host plus runner heartbeat.

## Requirements

1. Report exact validation.
2. Keep deploy separate unless explicitly requested.
3. Update this log with final files, validation, risks, and rollout state.
```
