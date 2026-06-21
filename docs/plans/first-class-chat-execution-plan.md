# First-Class Chat - Execution Plan

Date created: 2026-06-21

Primary plan: `docs/plans/first-class-chat-design.md`

Execution log: `docs/plans/first-class-chat-execution-log.md`

Branch: `main`

## Reference Documents

- `docs/plans/first-class-chat-context.md`: source-of-truth context, current system, live runner proof, requirements.
- `docs/plans/first-class-chat-design.md`: accepted target architecture and acceptance criteria.
- `docs/plans/realtime-agent-workforce-design.md`: current artifact-backed agent thread design.
- `docs/plans/codex-goal-workflows.md`: existing goal-loop and Codex runner boundary.
- `docs/plans/agentic-memory-design.md`: current meeting memory architecture.
- `scout_chat.go`: current private ephemeral chat session implementation.
- `main.go`: current assistant, artifacts, readiness, and internal callback endpoints.
- `agent_thread_runner.go`: current artifact-backed work-thread lifecycle.
- `codex_runner.go` and `codex_runner_queue.go`: current Codex runner config, queue, execution, callback, approval gate.
- `memory.go` and `memory_query.go`: current JSONL memory/artifact store.
- `index.html`: current single-file shell and Chat/Artifacts UI.
- `deploy/digitalocean/docker-compose.yml`: live Compose services, including `codex-runner`.

## Critical Rules

- Preserve existing artifacts, meeting memory, and `/assistant/threads` behavior while migrating.
- Ordinary personal chat is private by default and must not leak raw contents into org-level memory.
- External writes remain approval-gated: commit, push, deploy, SSH, email, external API writes, and production mutations need explicit approval.
- The public app must never expose Codex auth, Codex home, runner filesystem, app-server transport, or privileged sandbox controls directly.
- Store machine compaction state exactly as returned. The human-readable compaction artifact is a companion summary, not a replacement.
- Never log raw machine compaction state, runner auth, session cookies, bearer tokens, or unredacted tool output.
- Use `404` for inaccessible private thread ids.
- JSONL stores must use locked clone-on-read, append-only writes for new records, atomic temp-file rewrite for updates, optional `requestId` idempotency, strict payload limits, and corrupt-line readiness reporting.
- The backend is the single writer for chat, compaction, capability, intelligence, and audit stores. Codex sidecar jobs and future app-server broker flows must write through backend callbacks, not direct file writes.
- New chat endpoint errors must preserve the existing `writeAuthError` JSON shape: `{"error":"..."}`. Success payloads may include `{"ok":true,...}`.
- Keep Realtime voice as the fast transport/control layer. Codex handles slower work.
- Use repo-native Go and JSONL persistence first; defer database migration until the data shape is validated.
- Ship behind feature flags so rollback can return to current `scoutChatSession` behavior.
- Validate each wave with focused tests plus `go test -count=1 ./...` before commit.
- Do not deploy to the VPS unless the user explicitly asks for deploy; pushing `axx/main` does not update `/opt/meetingassist`.

## Wave Map

| Wave | Scope | Dependencies | Status |
| --- | --- | --- | --- |
| 1 | Chat store primitives and authenticated APIs | Current auth/session and JSONL memory patterns | pending |
| 2 | Persisted Chat UI and resume/switch behavior | Wave 1 | pending |
| 3 | Responses state and compaction ledger/artifacts | Wave 1, Wave 2 | pending |
| 4 | Capability registry and skill routing | Wave 1, existing Codex runner | pending |
| 5 | Codex work-thread integration hardening | Wave 4, current sidecar | pending |
| 6 | Org intelligence ledger and Scout view | Wave 3, Wave 4 | pending |
| 7 | Optional Codex app-server broker spike | Waves 1-5 stable | pending |
| 8 | Full QA, rollout flags, readiness, and deploy checkpoint | Waves 1-6; Wave 7 optional | pending |

## Wave Details

### Wave 1: Chat Store Primitives And APIs

Deliverables:

- Add JSONL-backed stores for chat threads, chat turns, and chat compactions.
- Add typed records and cloning helpers modeled after `meetingMemoryStore`.
- Add max payload limits from the design: `64 KiB` HTTP requests, `32 KiB` display text, and `512 KiB` model state before sidecar state-file storage.
- Add optional `requestId` idempotency handling on create/update/fork.
- Add authenticated endpoints:
  - `GET /assistant/chat/threads`
  - `POST /assistant/chat/threads`
  - `GET /assistant/chat/threads/{id}`
  - `PATCH /assistant/chat/threads/{id}`
  - `POST /assistant/chat/threads/{id}/fork`
- Enforce owner filtering by normalized account email.
- Add feature flag `BONFIRE_FIRST_CLASS_CHAT`.
- Add readiness checks for the new store files when the flag is enabled.
- Keep current Chat behavior untouched until UI migration.

Likely files:

- `chat_store.go` new.
- `chat_http.go` new.
- `chat_store_test.go` new.
- `chat_http_test.go` new.
- `main.go` route registration and readiness.
- `README.md` config notes.

Validation:

- `go test -count=1 ./...`
- `git diff --check`
- Target files: `chat_store_test.go`, `chat_http_test.go`, `assistant_http_test.go`.
- Store tests for append, atomic update with fsync/rename path, clone-on-read, corrupt-line skip/quarantine, stale temp-file readiness, idempotent create/update/fork, callback ordering helpers, and payload rejection.
- HTTP tests for unauthenticated rejection using `{"error":"..."}`, cross-origin rejection, per-user filtering, inaccessible id as `404`, archive/restore, archived write conflict, fork lineage, pagination clamp, malformed payloads, invalid cursor, idempotency collision, feature disabled behavior, and readiness output.
- Compatibility test or source assertion proving old `/assistant/query`, `/assistant/threads`, `/artifacts`, and websocket `scout_chat` behavior is not changed.

Risks:

- JSONL rewrite races if update helpers are not locked.
- Accidentally exposing all threads in list endpoints.
- Large turn payloads if serialized Responses items are stored without limits.

### Wave 2: Persisted Chat UI And Resume/Switch Behavior

Deliverables:

- Add a first-class thread list to the Chat tab.
- Load threads from `/assistant/chat/threads`.
- Create, rename, archive, restore, and switch threads.
- Persist new chat turns through the Wave 1 API.
- Keep websocket `scout_chat` compatibility while active persisted thread transport stabilizes.
- Show thread mode, last activity, and worker/artifact state.
- Make mobile thread switching drawer-based and overflow-safe.
- Render archived threads separately and disable composer for archived threads until restored.
- Render a neutral "thread unavailable" state for inaccessible/deleted thread loads.

Likely files:

- `index.html`.
- `frontend_latency_test.go` or focused frontend string/static tests.
- `assistant_http_test.go` or new `chat_http_test.go` cases.

Validation:

- `go test -count=1 ./...`
- `git diff --check`
- Target files: `frontend_latency_test.go` or focused frontend static tests, plus `chat_http_test.go` when API assumptions are exercised.
- Static frontend marker tests for thread list, active thread id persistence, archive/restore controls, visibility labels, capability inspector mount point, and mobile drawer.
- Static/frontend tests for disabled archived composer, unavailable thread state, mobile no-overflow rules, stale/deleted thread recovery, and old websocket fallback when the flag is disabled.
- Manual local browser smoke if feasible.

Risks:

- Current single-file UI is large; keep changes narrow and avoid a second frontend framework.
- Browser-side arrays can drift from server state if not refreshed after writes.

### Wave 3: Responses State And Compaction Ledger

Deliverables:

- Add `POST /assistant/chat/threads/{id}/turns`.
- Route ordinary turns through Responses using `OPENAI_CHAT_MODEL`.
- Store `responseId`, usage, latency, selected model profile, and turn text.
- Verify current OpenAI Responses and compaction API shape before coding the adapter.
- Add an adapter interface for stateful continuation, stateless item replay, and compact calls.
- Add compaction thresholds and `POST /assistant/chat/threads/{id}/compact`.
- Store machine compaction output exactly in `chat_compaction`.
- Create a human-readable `os_artifact` companion summary for each compaction.
- Emit/return compaction lifecycle states for UI animation.
- Add feature flag `BONFIRE_CHAT_COMPACTION`.
- Add fallback behavior: if machine compaction is unavailable, create a normal summary artifact and set `machineStateUnavailable:true`.

Likely files:

- `openai_responses.go` extended for stateful calls and compaction.
- `chat_compaction.go` new.
- `chat_http.go`.
- `memory_query.go` for compaction artifact creation.
- `index.html` for visual markers.
- Tests for compaction storage and artifact creation.

Validation:

- `go test -count=1 ./...`
- `git diff --check`
- Target files: `openai_responses_test.go`, `chat_compaction_test.go`, `chat_http_test.go`, `memory_query_test.go`.
- Unit tests with fake OpenAI responder/compact client and no network calls.
- Tests for `responses.create` request construction, `store:false`, `previous_response_id` mode, `context_management.compact_threshold` mode, standalone compact mode, fallback `machineStateUnavailable:true`, and `phase` preservation.
- Ensure compacted machine state is not parsed, edited, pruned, summarized, or exposed.
- Tests proving machine state is not returned to browser payloads, `/readyz`, artifacts, logs, or intelligence events.
- Redaction tests for human compaction artifacts.

Risks:

- Compaction output can be opaque/encrypted and should not be mixed into human summaries.
- API shape for compaction must be verified during implementation against current OpenAI docs/spec.
- Encryption-at-rest may require a staged config flag if the VPS has no `BONFIRE_CHAT_STATE_ENCRYPTION_KEY`.

### Wave 4: Capability Registry And Skill Routing

Deliverables:

- Add capability registry with static built-ins and runtime health probes.
- Include Bonfire tools, Codex runner, Realtime tools, OpenAI hosted tool availability, MCP slots, and skills.
- Add repo-scoped skill packaging plan or checked-in copies for:
  - `critic-loop`
  - `strategic-design`
  - `wave-plan`
- Add `GET /assistant/capabilities`.
- Capture capability snapshots on thread creation and Codex work launch.
- Route explicit skill mentions into thread metadata and worker prompts.
- Return unavailable capabilities with safe user-facing reasons; never leak private runner config.

Likely files:

- `capabilities.go` new.
- `capabilities_test.go` new.
- `chat_store.go` snapshot references.
- `agent_thread_runner.go`.
- `codex_runner_queue.go`.
- Optional `.agents/skills/...` if this wave includes repo-scoped skill material.

Validation:

- `go test -count=1 ./...`
- Registry tests for authority classes and health states.
- Target files: `capabilities_test.go`, `chat_store_test.go`, `codex_runner_test.go`.
- Tests that explicit `$critic-loop`, `$strategic-design`, and `$wave-plan` mentions are persisted and passed to worker prompts.
- Tests that unavailable tools are not advertised as usable.
- Tests that capability output redacts runner paths, environment variables, tokens, Codex home, and per-job payloads.

Risks:

- Copying local user-home skills into repo should be intentional and license-safe.
- Do not tell users a tool is available unless the availability probe proves it.

### Wave 5: Codex Work-Thread Integration Hardening

Deliverables:

- Link personal chat threads to launched `/assistant/threads` artifacts.
- Store runner job ids, evidence, approval state, and terminal status in both chat turn metadata and artifact metadata.
- Improve worker prompts to include capability snapshot and selected skills.
- Add a rerun/resume path from a personal thread.
- Make approval-required states visible in the Chat timeline, not only the Artifact view.
- Keep the existing sidecar queue and external-write gate.

Likely files:

- `agent_thread_runner.go`.
- `codex_runner.go`.
- `codex_runner_queue.go`.
- `chat_store.go`.
- `chat_http.go`.
- `index.html`.
- `codex_runner_test.go`, `agent_thread_runner_test.go`, `chat_http_test.go`.

Validation:

- `go test -count=1 ./...`
- Fake runner callback tests.
- Target files: `codex_runner_test.go`, `codex_runner_queue_test.go`, `agent_thread_runner_test.go`, `chat_http_test.go`.
- Approval gate tests for commit, push, deploy, SSH, email, external API writes, and production mutation phrases.
- Tests for queued/running/approval_required/blocked/verified state transitions, duplicate callback idempotency, stale callback rejection, rerun lineage, and UI/status copy that refuses to claim Codex work without runner evidence.
- Readiness still reports runner heartbeat without leaking callback token or raw job payloads.

Risks:

- A batch `codex exec` worker is not the same as interactive Codex app-server. Be explicit in UI labels and evidence.
- Do not accidentally auto-run external writes after approval metadata changes.

### Wave 6: Org Intelligence Ledger And Scout View

Deliverables:

- Add append-only intelligence event store.
- Emit events from published artifacts, compaction artifacts with explicit share, meeting brain summaries, blockers, and decisions.
- Add `GET /assistant/intelligence` with visibility filtering.
- Add source references back to artifacts, turns, meetings, and memory ids.
- Add UI Scout view for allowed org-level signals.
- Add a future-safe proactive recommendation record shape without autonomous mutations.
- Add saved-learning review flow with pending/approved/rejected status.

Likely files:

- `intelligence_store.go` new.
- `intelligence_http.go` new.
- `memory_query.go` and artifact publish paths.
- `board_worker.go` or `brain_worker.go` event emission hooks.
- `index.html`.

Validation:

- `go test -count=1 ./...`
- Per-user visibility tests.
- Target files: `intelligence_store_test.go`, `intelligence_http_test.go`, `memory_query_test.go`, `chat_http_test.go`.
- Tests proving private chat turns do not create org events.
- Tests proving org view shows structured signals and source refs only, not raw private turns or machine compaction state.
- Tests for owner review, admin review, pending/approved/rejected/revoked learnings, visibility transitions, and source-reference revocation.

Risks:

- Over-capturing private thought into org memory.
- Duplicated signals from repeated artifact updates.

### Wave 7: Optional Codex App-Server Broker Spike

Deliverables:

- Prototype a private backend-only broker to `codex app-server` over stdio or Unix socket.
- Map Bonfire `chat_thread.id` to Codex `threadId`.
- Stream app-server item/turn events into Bonfire chat timeline events.
- Evaluate whether app-server should replace or supplement `codex exec` for workflow mode.
- Keep behind `BONFIRE_CODEX_APP_SERVER=false` until security and operations are accepted.

Likely files:

- `codex_app_server.go` new.
- `codex_app_server_test.go` new with fake process/transport.
- `chat_http.go`.
- `index.html` if UI event shape differs.
- `deploy/digitalocean/docker-compose.yml` only if a separate broker service is needed.

Validation:

- Unit tests with fake JSON-RPC transport.
- Target files: `codex_app_server_test.go`, `capabilities_test.go`, `chat_http_test.go`.
- No public listener exposure: test that no route, websocket, or TCP listener exposes app-server directly.
- Explicit auth and capability-token rules if websocket/unix transport is ever used.
- Tests that app-server item events stream into Bonfire records through the backend writer only.

Risks:

- App-server websocket transport is experimental and should not be exposed remotely.
- Process lifecycle and per-user isolation need careful resource limits.

### Wave 8: Full QA, Rollout Flags, Readiness, And Deploy Checkpoint

Deliverables:

- Consolidate tests and docs.
- Add readiness output for chat store, compaction, capability registry, intelligence store, and runner.
- Add operator docs for flags, rollback, and data backup.
- Run local full validation.
- If and only if the user asks to deploy, follow `AGENTS.md`: commit/push if requested, back up VPS files, sync changed files or committed tree, rebuild Compose including codex profile when needed, and verify live host.

Likely files:

- `README.md`.
- `deploy/digitalocean/README.md`.
- `main.go`.
- Tests touched by earlier waves.

Validation:

- `go test -count=1 ./...`
- `git diff --check`
- Local server smoke for `/healthz`, `/readyz`, auth gates, and chat endpoints.
- Target files: integration/readiness tests plus operator-doc assertions where this repo already uses static doc tests.
- Negative cases: cross-user reads, private-turn non-emission to org memory, machine-state non-exposure, external-write approval gates, no-public-app-server listener proof, feature-flag rollback, old endpoint compatibility, corrupt JSONL readiness, and backup/restore doc coverage.
- If deployed: `curl` live host, `/readyz`, Compose ps/logs, and runner heartbeat.

Risks:

- Shipping code without rebuilding the `codex-runner` profile can leave worker behavior stale.
- Live data is in a Docker volume; backup/restore commands must preserve it.

## Agent Assignment Pattern

Use the main agent as integrator and gatekeeper. Use subagents when explicitly authorized for a wave:

- Explorer: current code/API trace for a narrow subsystem.
- Worker: disjoint implementation with explicit file ownership.
- Critic: security/privacy or UX gate against the wave acceptance criteria.

Example parallel split after Wave 1:

- Worker A owns `chat_store.go` and store tests.
- Worker B owns HTTP handlers and auth tests.
- Main agent owns route registration, readiness integration, review, and final validation.

Every worker must be told it is not alone in the codebase, must not revert others' edits, and must keep changes within assigned files.

## Quality Gate

Before any implementation wave is called complete:

- The original goal is restated and mapped to delivered behavior.
- Tests pass or blockers are documented with exact failure output.
- External-write paths are still gated.
- Private data filtering has tests.
- UI text/status does not imply Codex did work unless runner evidence exists.
- The execution log is updated with files changed, validation, risks, and the next prompt.

## Planning Gate Evidence

For this planning-only commit, completion requires:

- `go test -count=1 ./...` passing.
- `git diff --check` passing.
- Critic loop accepted after revisions.
- Commit created on `main`.
- Push to `axx/main` completed.
- Explicit final note that the VPS was not deployed or restarted.

The planning artifacts intentionally cannot contain their own final commit hash, because adding that hash would change the commit. The final response is the authoritative close record for commit hash, push destination, and git directives.
