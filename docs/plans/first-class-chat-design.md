# First-Class Chat - Unified Design

Date: 2026-06-21

## Executive Summary

Bonfire should evolve from "private ephemeral Scout chat plus durable artifacts" into a personal, account-owned Codex-style workspace. The core architecture is hybrid: fast personal chat uses the OpenAI Responses API with persisted thread state, while long-running work launches Codex-backed work threads through the already-live private `codex-runner` sidecar. Compaction becomes a visible product event and a durable record: the machine continuation state is stored intact, and a human-readable compaction artifact is saved for audit and organizational memory. Scout's future "god-level view" should be built from permissioned summaries, artifacts, blockers, decisions, and published signals rather than leaking raw private chats.

## Architecture

The target system has six layers:

1. **Personal Chat Thread Store**
   - New account-owned records store chat threads, turns, model state, compaction state, and lineage.
   - Source of truth is server-side, not browser arrays or websocket session state.
   - Existing websocket chat becomes a transport for the active thread, not the owner of history.

2. **Responses State Engine**
   - Ordinary chat turns use `gpt-5.5` through the Responses API.
   - Threads store `previous_response_id` when server-side state is used.
   - If the app needs stateless replay, it stores returned response items and preserves `phase` fields.
   - Context management and compaction thresholds are part of the thread profile.

3. **Compaction And Continuity Ledger**
   - Each compaction creates two durable records:
     - `chat_compaction_state`: opaque model continuation output used by the next model call.
     - `chat_compaction_artifact`: human-readable summary of preserved goals, assumptions, decisions, tool outcomes, open blockers, and next action.
   - The UI receives `chat_compaction_started` and `chat_compaction_completed` events so users understand why the thread briefly condenses.
   - The human artifact is visible in Artifacts/Memory according to thread visibility; the machine state is not edited or interpreted by users.

4. **Codex Work Thread Bridge**
   - Long-running work keeps using `/assistant/threads` and the `codex-runner` sidecar.
   - Current `codex exec` queue remains the batch execution path for research/design/grill/workflow artifacts.
   - A future `codex app-server` broker should be added when Bonfire needs true embedded Codex threads, turns, approvals, streamed item events, and richer resume/fork behavior.
   - The public web process only brokers authenticated requests and artifact updates. It never exposes Codex auth or runner transports directly.

5. **Capability Registry**
   - A server-side registry describes skills, tools, MCP servers, hosted tools, Bonfire app tools, and Codex runner capabilities.
   - Every entry includes: id, display name, source, description, authority level, side-effect class, required approvals, availability probe, scopes, and mode routing.
   - Thread creation captures a capability snapshot so future resumes know what tools were available at the time.
   - `$critic-loop`, `$strategic-design`, and `$wave-plan` should be checked into repo-scoped `.agents/skills` or packaged as a Bonfire plugin so the VPS runner can use them independent of a developer's local home directory.

6. **Scout Organizational Intelligence Layer**
   - Personal/private thread contents stay private by default.
   - Threads and artifacts can emit permissioned intelligence events: decisions, blockers, owners, project references, source artifact ids, confidence, visibility, and redaction status.
   - Scout's org view reads from this structured intelligence ledger plus meeting brain summaries and published artifacts.
   - Proactive suggestions are future work and must start as explainable, non-mutating recommendations.

## Current Gap Inventory

| Need | Current State | Target State |
| --- | --- | --- |
| Personal saved chat threads | Per-websocket in-memory `scoutChatSession`; browser arrays | Server-owned `chat_threads` per account |
| Resume after reconnect | No durable chat history beyond artifacts | Thread list, active thread resume, last turn state |
| Compaction | Meeting transcript summaries only; no chat compaction | Model compaction state plus readable artifact |
| Thread ownership | Artifact attribution metadata only | Owner email/id, visibility, ACL, audit fields |
| Codex execution | Live sidecar queue for artifact threads | Sidecar retained, with optional app-server broker for richer Codex tabs |
| Skills | Local Codex skills in developer home, not product registry | Repo/plugin-scoped skill inventory surfaced to workers |
| Tool availability | Hard-coded Realtime tools and string summaries | Dynamic registry with authority, approval, and health |
| Scout org memory | Shared meeting memory and published artifacts | Permissioned intelligence ledger with no private leakage |

## Detailed Design

### 1. Data Model

Use append-friendly JSONL stores first, matching the repo's current persistence style, then consider SQLite/Postgres after the shape stabilizes.

New store paths:

- `BONFIRE_CHAT_THREADS_PATH`, default `data/chat-threads.jsonl`.
- `BONFIRE_CHAT_TURNS_PATH`, default `data/chat-turns.jsonl`.
- `BONFIRE_CHAT_COMPACTIONS_PATH`, default `data/chat-compactions.jsonl`.
- `BONFIRE_INTELLIGENCE_EVENTS_PATH`, default `data/intelligence-events.jsonl`.
- `BONFIRE_CAPABILITIES_PATH`, default `data/capabilities-cache.json`.

`chat_thread` fields:

- `id`: stable id such as `chat-thread-<timestamp>-<random>`.
- `ownerEmail`: normalized account email.
- `title`: user or model-generated.
- `status`: `active`, `archived`, `deleted`.
- `visibility`: `private`, `shared`, `org`.
- `mode`: `chat`, `research`, `design`, `grill`, `workflow`, or future project mode.
- `activeArtifactId`: optional current work artifact.
- `codexThreadId`: optional Codex app-server thread id if/when app-server is adopted.
- `latestResponseId`: latest OpenAI Responses id for stateful continuation.
- `latestCompactionId`: latest compaction ledger id.
- `capabilitySnapshotId`: registry snapshot used at thread creation or latest rebase.
- `createdAt`, `updatedAt`, `archivedAt`.

`chat_turn` fields:

- `id`, `threadId`, `ownerEmail`.
- `role`: `user`, `assistant`, `tool`, `system_event`.
- `text`: display text.
- `items`: optional serialized Responses items for stateless continuation.
- `responseId`: upstream response id.
- `phase`: preserved when returned by Responses output items.
- `toolCalls`: structured tool call summaries.
- `artifactIds`: artifacts created or updated in the turn.
- `authority`: `none`, `read_only`, `workspace_write`, `external_write`.
- `createdAt`, `tokenUsage`, `latencyMs`, `error`.

`chat_compaction` fields:

- `id`, `threadId`, `ownerEmail`.
- `trigger`: `threshold`, `manual`, `before_long_work`, `pre_deploy_gate`.
- `inputTurnRange`: first/last turn ids compacted.
- `machineState`: encrypted or opaque compact output items, stored exactly as returned.
- `summaryArtifactId`: human-readable companion artifact.
- `summaryText`: short display preview only; source of truth is the artifact.
- `preservedState`: structured JSON with completed actions, active assumptions, ids, tool outcomes, unresolved blockers, and next goal.
- `createdAt`, `model`, `usage`, `status`.

`intelligence_event` fields:

- `id`, `sourceType`, `sourceId`, `threadId`, `artifactId`, `meetingId`.
- `ownerEmail`, `visibility`, `redactionLevel`.
- `kind`: `decision`, `blocker`, `follow_up`, `artifact_published`, `skill_lesson`, `project_signal`, `expertise_signal`.
- `summary`, `entities`, `owners`, `dueDates`, `confidence`, `sourceRefs`.
- `createdAt`, `expiresAt`, `reviewStatus`.

### 2. API Surface

Add authenticated HTTP endpoints:

- `GET /assistant/chat/threads`: list the signed-in user's threads plus shared/org threads they can see.
- `POST /assistant/chat/threads`: create a thread with mode, title, visibility, and optional seed prompt.
- `GET /assistant/chat/threads/{id}`: fetch thread metadata, recent turns, artifacts, compaction cards, and capability snapshot.
- `POST /assistant/chat/threads/{id}/turns`: append a user turn and stream/return the assistant result.
- `POST /assistant/chat/threads/{id}/compact`: manually compact and create a summary artifact.
- `POST /assistant/chat/threads/{id}/fork`: create a new thread from current state.
- `PATCH /assistant/chat/threads/{id}`: rename, archive, restore, visibility changes.
- `GET /assistant/capabilities`: return user-visible capabilities and health.
- `GET /assistant/intelligence`: filtered Scout org view for allowed events.

Update websocket events:

- `chat_thread_started`
- `chat_turn_delta`
- `chat_turn_completed`
- `chat_compaction_started`
- `chat_compaction_completed`
- `chat_worker_status`
- `chat_artifact_linked`
- `capability_snapshot_changed`

Keep existing endpoints during migration:

- `/assistant/query` remains the compatibility path for simple queries.
- `/assistant/threads` remains the long-running artifact-thread launch path.
- `/artifacts` remains the artifact source of truth until a richer artifact store exists.

### 3. Model Profiles

Use named model profiles instead of scattering model strings:

- `chat-fast`: `gpt-5.5`, reasoning `low`, verbosity `low` or `medium`.
- `chat-deep`: `gpt-5.5`, reasoning `medium`, verbosity `medium`.
- `workflow-codex`: Codex runner model `gpt-5.5` when configured; reasoning `high` for complex implementation or plan work.
- `workflow-hard`: `gpt-5.5`, reasoning `xhigh`, only for expensive gated workflows or eval-proven cases.
- `voice-private`: `gpt-realtime-2`, current voice config, with private tools only outside the room.
- `voice-room`: `gpt-realtime-2`, room-safe tools and wake-phrase behavior.
- `summary-compaction`: `gpt-5.5`, reasoning `low` or `medium`, structured output for human-readable compaction artifacts.

The repo already defaults `defaultMeetingBrainModel` to `gpt-5.5`. Keep that. Add explicit config for chat and Codex:

- `OPENAI_CHAT_MODEL`, default `gpt-5.5`.
- `OPENAI_CHAT_REASONING_EFFORT`, default `medium`.
- `OPENAI_COMPACTION_MODEL`, default `gpt-5.5`.
- `BONFIRE_CODEX_MODEL`, default empty to inherit Codex default, but production should set `gpt-5.5` after access is confirmed.

### 4. Skill And Tool Availability

The product should not merely say "skills exist"; it should prove what the thread can use.

Capability registry examples:

```json
{
  "id": "skill.critic-loop",
  "kind": "skill",
  "source": "repo",
  "authority": "none",
  "sideEffects": "none",
  "description": "Adversarial quality gate for plans, designs, code, and outputs.",
  "available": true,
  "modes": ["chat", "workflow", "design"],
  "approvalRequired": false
}
```

```json
{
  "id": "tool.codex.exec",
  "kind": "codex_runner",
  "source": "codex-runner",
  "authority": "workspace_write",
  "sideEffects": "workspace_files",
  "available": true,
  "health": "heartbeat_ok",
  "approvalRequiredFor": ["external_write"]
}
```

Skill routing rules:

- Explicit `$critic-loop`, `$strategic-design`, and `$wave-plan` mentions are honored.
- Implicit routing is allowed only when the capability registry says the skill is enabled for that thread mode.
- Selected skills are written to the turn record and worker prompt.
- Worker output must include which skills were actually used.

Tool routing rules:

- Fast chat can call Bonfire read tools, memory search, artifact search, and app navigation tools.
- Work threads can use Codex runner, subagents, repo tools, browser tools, and MCP tools according to the thread authority.
- External writes require an approval artifact before the worker runs the side effect.

### 5. Codex Runner Versus App-Server

Keep the existing `codex-runner` sidecar for batch work now. It is live, tested, and aligned with the current artifact model.

Add a Codex app-server broker only after the personal thread store lands. The app-server is the right fit for a richer embedded Codex client because it provides Codex-native threads, turns, approvals, conversation history, and streamed events. It should run behind the backend, over stdio or Unix socket, not as an unauthenticated public websocket.

Recommended path:

1. Phase A: Persist Bonfire chat threads and continue using `codex exec` for work artifacts.
2. Phase B: Add a broker that can create/resume Codex app-server threads for selected `workflow` threads.
3. Phase C: Stream app-server item events into Bonfire `chat_turn` records and artifact progress cards.
4. Phase D: Use app-server thread ids for rich Codex tabs while preserving Bonfire ownership, visibility, and org intelligence events.

Do not replace the Realtime voice loop with Codex. Realtime stays the fast conversational and voice control surface; Codex handles slower work.

### 6. Compaction UX

When a thread approaches compaction:

1. Server emits `chat_compaction_started`.
2. Chat shows a small "condensing context" animation in the timeline.
3. Composer remains usable if possible; otherwise it shows a short disabled state with a retry-safe message.
4. Server stores machine compaction output exactly.
5. Server creates a human-readable artifact titled `Context handoff - <thread title>`.
6. Server emits `chat_compaction_completed` with the compaction card and artifact id.
7. The next turn resumes from compact state plus the new user input.

Human compaction artifact format:

- Objective
- What has been completed
- Active assumptions
- Important ids and artifacts
- Tool outcomes
- Open blockers
- Next concrete action
- Permission/visibility note

The artifact must not pretend to be the full raw conversation. It is the durable handoff record.

### 7. Personal Privacy And Scout's Org View

Default visibility:

- New personal chat threads are `private`.
- Generated compaction artifacts inherit thread visibility.
- Work artifacts are `private` until published/shared.
- Meeting memory remains room/team scoped.

Org intelligence rules:

- Private chat turns never flow into org intelligence directly.
- A private thread can emit an org-level event only when the user publishes an artifact, explicitly shares the thread, or a policy-approved summary is created.
- Org events contain concise structured signals, not raw chat.
- Every org event has source references and visibility metadata.
- Scout's proactive suggestions cite the source signal and offer actions; they do not mutate production systems without approval.

This gives Scout the "god-level view" as a permissioned intelligence map, not a backdoor into private work.

### 8. UI Design

Chat tab expected behavior:

- Left column or top selector: thread list with title, last activity, mode, status, and unread/worker state.
- Main pane: active thread timeline.
- Composer: supports mode selector, skill mentions, file/artifact references, and launch-as-work-thread affordance.
- Right inspector: artifacts, compaction cards, capability snapshot, runner evidence, and share/publish controls.

Thread states:

- `idle`: ready for a turn.
- `thinking`: ordinary model turn running.
- `compacting`: context is being condensed.
- `queued`: Codex sidecar job queued.
- `running`: Codex sidecar or app-server turn running.
- `approval_required`: user must approve external write.
- `blocked`: worker could not continue.
- `verified`: goal complete and gated.

Mobile:

- Thread list collapses into a drawer.
- Artifact/inspector opens as a sheet.
- Compaction and worker status render inline in the timeline.
- Long artifact panes must be height-capped with no horizontal overflow.

### 9. Migration And Rollout

Wave migration order:

1. Add stores and APIs without changing existing chat UX.
2. Persist new chat turns while still sending old websocket events.
3. Add UI thread list/resume using persisted data.
4. Add compaction ledger and visual events.
5. Add capability registry and skill routing.
6. Add org intelligence events.
7. Add optional Codex app-server broker after the simpler thread system is stable.

Rollback:

- Disable new chat endpoints with a feature flag.
- Fall back to current `scoutChatSession` behavior.
- Leave JSONL stores intact for later replay.
- Keep `/assistant/threads` and artifacts unaffected.

Feature flags:

- `BONFIRE_FIRST_CLASS_CHAT=true`
- `BONFIRE_CHAT_COMPACTION=true`
- `BONFIRE_CAPABILITY_REGISTRY=true`
- `BONFIRE_CODEX_APP_SERVER=false`
- `BONFIRE_ORG_INTELLIGENCE=false`

### 10. Testing And Acceptance

Local tests:

- `go test -count=1 ./...`
- `git diff --check`
- Store tests for append, update, per-user filtering, archiving, forking, and compaction rollback.
- API tests for auth, ownership, visibility, and CSRF/origin handling.
- Runner tests proving external writes remain approval-gated.
- Frontend static tests for thread list rendering, compaction markers, mobile overflow, and progress cards.

Live readiness:

- `/readyz` must include chat store health, compaction queue health, capability registry health, and codex runner heartbeat.
- `/assistant/chat/threads` must return only the signed-in user's private threads plus allowed shared/org records.
- A launched workflow should create/update an artifact and preserve runner evidence.

Manual acceptance:

- User signs in, creates a thread, refreshes, and resumes it.
- User starts a second thread and switches back.
- User launches a workflow with `$strategic-design $critic-loop $wave-plan`; the thread records selected skills and creates a work artifact.
- A long thread compacts, shows animation, stores a compaction artifact, and answers the next turn with preserved context.
- A private thread does not appear in another user's thread list.
- A published artifact creates an org intelligence event Scout can cite.

## Source-Goal Coverage Matrix

| Required capability | Design section | Wave | Acceptance test | User-visible proof |
| --- | --- | --- | --- | --- |
| Identify and set goal | Executive Summary, Codex Work Thread Bridge | 5 | worker prompt includes goal loop and selected skills | timeline/work artifact starts with goal |
| Decompose work and assign agents | Capability Registry, Codex Work Thread Bridge | 4, 5 | skill routing and worker prompt tests | work artifact shows agent assignment |
| Coordinate dependencies and execute in order | Codex Runner Versus App-Server, Wave plan | 5 | runner metadata and state transition tests | queued/running/blocked/verified status cards |
| Review against original goal | Skill And Tool Availability | 4, 5 | `$critic-loop` mention persists and reaches worker | artifact review section and gate state |
| Gate before shipping | Privacy, ACL, And Audit Matrix; Codex bridge | 5 | external-write phrase tests require approval | approval-required card before commit/push/deploy/SSH |
| Save what worked | Saved Learnings | 6 | reviewed learning emits intelligence event | Scout memory shows approved lesson |
| Report only what matters | UI Design, Compaction UX | 2, 3, 5 | compact artifact and final report shape tests | concise timeline summaries and artifacts |
| Verify goal completed | Thread State Machine, Testing | 5, 8 | verified terminal state requires evidence | thread ends in `verified` with evidence |
| Personal saved chat tabs | Personal Chat Thread Store | 1, 2 | per-user list/read/fork/archive tests | user can refresh and resume a thread |
| Compaction animation and artifact | Compaction And Continuity Ledger, Compaction UX | 3 | fake compact client and UI marker tests | condensing animation plus context handoff artifact |
| Skills: critic-loop, strategic-design, wave-plan | Capability Registry | 4 | explicit skill mention tests | capability inspector lists selected skills |
| Codex on VPS with honest boundary | Trust Evidence Appendix, Codex bridge | 5, 8 | readiness heartbeat and runner callback tests | UI says sidecar evidence, not generic magic |
| Scout org intelligence without leaks | Privacy matrix, Org Intelligence Layer | 6 | private turn does not emit org event | org view shows structured source-linked signals only |

## API Contract Appendix

All new endpoints are feature-flagged by `BONFIRE_FIRST_CLASS_CHAT`.

Common HTTP rules:

- Reuse the existing `userFromRequest`, `writeAuthJSON`, and `writeAuthError` helpers.
- Existing `writeAuthError` currently serializes `{"error":"<message>"}`. New chat endpoints should keep that error shape for compatibility; success payloads may include `{"ok":true,...}`.
- Unauthenticated requests return `401` with `{"error":"not signed in"}`.
- Write endpoints must apply the same origin allowlist used by existing authenticated POST actions. Cross-origin writes return `403` with `{"error":"forbidden origin"}`.
- When `BONFIRE_FIRST_CLASS_CHAT` is disabled, all new `/assistant/chat/*`, `/assistant/capabilities`, and `/assistant/intelligence` routes return `404` so the old UI path remains authoritative.
- Existing-but-inaccessible private ids return `404` with `{"error":"thread not found"}` to avoid existence leaks.
- Malformed JSON returns `400`; payloads over `64 KiB` return `413`; unsupported mode/status/visibility returns `422`; edit conflicts return `409`.
- `requestId` is scoped to `ownerEmail + route + logical action`. Reusing the same `requestId` with the same semantic payload returns the original success response. Reusing it with a conflicting payload returns `409`.
- Cursors are opaque base64url JSON containing `updatedAt`, `id`, and `direction`. Invalid or expired cursors return `400`; cursors never expose records the user cannot read.

Endpoint summary:

| Method | Path | Success | Important errors | Notes |
| --- | --- | --- | --- | --- |
| `GET` | `/assistant/chat/threads` | `200` | `400`, `401`, `404` disabled | list visible threads |
| `POST` | `/assistant/chat/threads` | `201` | `400`, `401`, `403`, `409`, `413`, `422` | create private thread |
| `GET` | `/assistant/chat/threads/{id}` | `200` | `400`, `401`, `404` | read metadata and timeline |
| `PATCH` | `/assistant/chat/threads/{id}` | `200` | `400`, `401`, `403`, `404`, `409`, `413`, `422` | rename/archive/restore |
| `POST` | `/assistant/chat/threads/{id}/fork` | `201` | `400`, `401`, `403`, `404`, `409`, `413`, `422` | create child thread |
| `POST` | `/assistant/chat/threads/{id}/turns` | `200` or `202` | `400`, `401`, `403`, `404`, `409`, `413`, `422`, `501` before Wave 3 | execute ordinary turn |
| `POST` | `/assistant/chat/threads/{id}/compact` | `202` or `200` | `400`, `401`, `403`, `404`, `409`, `413`, `422`, `501` before Wave 3 | compact context |
| `GET` | `/assistant/capabilities` | `200` | `401`, `404` disabled | safe capability inventory |
| `GET` | `/assistant/intelligence` | `200` | `400`, `401`, `404` disabled | allowed org signals only |

`GET /assistant/chat/threads?status=active&limit=50&cursor=<opaque>`

- Lists only the signed-in user's private threads plus records explicitly visible to them.
- `status` supports `active`, `archived`, and `all`; default is `active`.
- `limit` is clamped to `1..100`.
- Response is `200` with `{"ok":true,"threads":[...],"nextCursor":""}`.
- Archived threads appear only when `status=archived` or `status=all`.
- Deleted/tombstoned threads are omitted unless a future admin audit endpoint is added.

Thread row shape:

```json
{
  "id": "chat-thread-20260621-184700-abc123",
  "title": "Launch plan",
  "mode": "chat",
  "status": "active",
  "visibility": "private",
  "ownerEmail": "aj@shareability.com",
  "lastTurnPreview": "Let's turn this into a launch wave plan.",
  "activeArtifactId": "os-artifact-workflow-...",
  "latestCompactionId": "",
  "workerStatus": "idle",
  "createdAt": "2026-06-21T18:47:00Z",
  "updatedAt": "2026-06-21T18:48:00Z"
}
```

`POST /assistant/chat/threads`

Request:

```json
{
  "title": "Launch plan",
  "mode": "chat",
  "visibility": "private",
  "seedText": "Optional first user prompt",
  "requestId": "optional-client-idempotency-key"
}
```

- `mode` defaults to `chat`.
- `visibility` defaults to `private`; Wave 1 rejects `shared` and `org` until ACLs land.
- Repeated `requestId` for the same owner/action returns the original thread.
- If `seedText` is present, Wave 1 stores it as a user turn with `assistantStatus:"not_run"`; Wave 3 may execute it.
- Response is `201` with `{"ok":true,"thread":{...},"turns":[]}` or `turns:[seedTurn]`.

`GET /assistant/chat/threads/{id}?turnLimit=50&before=<turnId>`

- Returns thread metadata, recent turns, compactions, linked artifacts, and capability snapshot.
- Missing or inaccessible thread returns `404`.
- Archived threads are readable by owner.
- `turnLimit` is clamped to `1..100`.
- `before` is an opaque turn cursor or concrete `turnId`; invalid cursors return `400`.
- Response is `200` with `{"ok":true,"thread":{...},"turns":[...],"compactions":[...],"artifacts":[...],"capabilities":{...}}`.

`PATCH /assistant/chat/threads/{id}`

Request:

```json
{
  "title": "Updated title",
  "status": "archived",
  "visibility": "private",
  "requestId": "optional-client-idempotency-key"
}
```

- Wave 1 supports `active` and `archived`.
- `deleted` is a future soft-delete with retention policy.
- Visibility changes beyond `private` are rejected until Wave 6.
- Archiving a thread with `thinking`, `compacting`, `queued`, or `running` work returns `409` unless `force:true` is added by a later wave.
- Restoring an archived thread returns the thread to `active/idle` unless a linked worker remains running, in which case it returns `409`.
- Response is `{"ok":true,"thread":{...},"updated":true}`.

`POST /assistant/chat/threads/{id}/fork`

Request:

```json
{
  "title": "Launch plan - branch",
  "fromTurnId": "optional-last-included-turn",
  "requestId": "optional-client-idempotency-key"
}
```

- Forks preserve owner and visibility.
- The child stores `parentThreadId` and `forkedFromTurnId`.
- Large turn bodies should be referenced by lineage, not duplicated.
- Forking an archived thread is allowed; the child starts `active/idle`.
- If `fromTurnId` is missing, fork from the latest visible turn.
- Response is `201` with `{"ok":true,"thread":{...},"parentThreadId":"...","forkedFromTurnId":"..."}`.

`POST /assistant/chat/threads/{id}/turns`

- Wave 3 owns model execution; Wave 1 may reserve the route or return `501`.
- Request includes `text`, optional `mode`, `skillMentions`, `artifactRefs`, and `requestId`.
- Archived threads return `409`.
- Existing `thinking`, `compacting`, `queued`, or `running` work returns `409` with `{"error":"thread busy","retryAfterMs":...}` unless a later wave adds explicit queueing.
- Response includes `{"ok":true,"thread":{...},"userTurn":{...},"assistantTurn":{...},"compaction":null,"actions":[]}` for synchronous turns or `202` with `workerStatus` for asynchronous turns.

`POST /assistant/chat/threads/{id}/compact`

- Wave 3 owns execution.
- Request includes `trigger` and optional `requestId`.
- Archived threads return `409`.
- Existing `thinking`, `compacting`, `queued`, or `running` work returns `409`.
- `trigger` supports `manual`, `threshold`, `milestone`, and `pre_fork`; invalid values return `422`.
- Response includes `{"ok":true,"compaction":{...},"artifact":{...},"machineStateIncluded":false}`. `machineStateIncluded` must always be `false` in browser responses.

`GET /assistant/capabilities`

- Returns a safe snapshot of user-visible capabilities.
- Response shape: `{"ok":true,"capabilities":[...],"generatedAt":"...","runnerHealth":"heartbeat_ok|unavailable|disabled"}`.
- Do not return runner tokens, local paths, Codex home, raw environment variables, API keys, or per-run job payloads.

`GET /assistant/intelligence?scope=org&kind=decision&limit=50&cursor=<opaque>`

- Wave 6 owns this endpoint.
- Lists only approved private learnings for the owner plus shared/org signals visible to the session user.
- `scope` supports `mine`, `shared`, and `org`; default is `mine`.
- Response shape: `{"ok":true,"events":[...],"nextCursor":""}`.
- Raw chat turns and machine compaction state are never returned.

## Thread State Machine

Thread status values:

- `active`: default usable state.
- `archived`: hidden from default list, readable by owner, no new turns until restored.
- `deleted`: future soft-delete state, hidden from normal reads.

Work status values:

- `idle`, `thinking`, `compacting`, `queued`, `running`, `approval_required`, `blocked`, `verified`.

| From | Event | To | Rule |
| --- | --- | --- | --- |
| none | create thread | active/idle | owner is session email |
| active/idle | append user turn | active/thinking | reject archived threads |
| thinking | assistant complete | active/idle | store response id and assistant turn |
| thinking | model error | active/blocked | store safe error summary |
| active/idle | manual/threshold compact | active/compacting | emit started event |
| compacting | compact complete | active/idle | store machine state and artifact |
| compacting | compact error | active/blocked | preserve previous usable state |
| active/idle | launch work thread | active/queued | link artifact and runner job |
| queued | runner claimed | active/running | callback updates timeline |
| running | external write marker | active/approval_required | explicit approval required |
| approval_required | approve | active/queued | enqueue external-write job |
| approval_required | reject | active/blocked | store rejection metadata |
| running | worker success | active/verified | store evidence and artifacts |
| running | worker failure | active/blocked | store evidence and safe error |
| active | archive | archived | no new turns |
| archived | restore | active/idle | unless linked worker is still running |
| active/archived | fork | active/idle | new thread stores parent lineage |

## Durability And Limits

- The app backend is the single writer for chat JSONL, compaction state, capability snapshots, and intelligence events. Codex sidecar and future app-server broker integrations must write through authenticated/internal backend callbacks, not directly to JSONL.
- Each JSONL store owns a `sync.Mutex`; reads clone records before unlocking. Multi-store mutations acquire locks in this order: threads, turns, compactions, artifacts, intelligence, audit.
- Appends use one JSON object per line with `O_APPEND`, file mode `0600`, a single complete line write, and `File.Sync()` before returning success.
- Updates use temp-file rewrite in the same directory, chmod `0600`, `File.Sync()`, close, atomic rename, and best-effort parent-directory sync.
- Mutations accept optional `requestId` idempotency keys.
- Request bodies default to `64 KiB`.
- Timeline display text is capped at `32 KiB`; larger output links to artifacts/evidence.
- Serialized model state is capped at `512 KiB` in JSONL. Larger state moves to `data/chat-state/<threadId>/<recordId>.json` with a SHA-256 digest.
- Corrupt JSONL lines are skipped with warnings and counted in `/readyz`. A corrupt final partial line is copied to a `.corrupt-<timestamp>` quarantine file during the next successful rewrite; earlier corrupt lines remain ignored until a maintenance command repairs them.
- Crash recovery is replay-from-disk at startup. A leftover temp file is ignored unless its target is missing, in which case readiness reports `needsOperatorReview:true`.
- Runner callbacks carry `threadId`, `runId`, `jobId`, and monotonic `sequence`. Duplicate callbacks are idempotent. Stale callbacks with a lower stored sequence are ignored and audited.
- Simultaneous turn, compact, fork, archive, and restore conflicts return `409` rather than trying to merge state. Fork may proceed from the last committed turn while another run is busy only after Wave 5 explicitly supports branch-from-running semantics.
- Schema changes are additive until a dedicated migration command exists.
- Backups must include chat JSONL files and `data/chat-state/`.

## Privacy, ACL, And Audit Matrix

ACL subject fields:

- `ownerEmail`: normalized account email that owns a private thread.
- `viewerEmails`: explicit named user access list.
- `teamIds`: explicit group access list after team membership exists.
- `orgId`: organization boundary for org-visible intelligence.
- `visibility`: `private`, `shared`, or `org`.
- `reviewStatus`: `pending`, `approved`, `rejected`, or `revoked` for promoted learnings.

Audit event shape:

```json
{
  "id": "audit-20260621-190200-abc123",
  "actorEmail": "aj@shareability.com",
  "action": "chat_thread.visibility_changed",
  "resourceKind": "chat_thread",
  "resourceId": "chat-thread-...",
  "before": {"visibility": "private"},
  "after": {"visibility": "shared", "viewerEmails": ["teammate@example.com"]},
  "reason": "user_share",
  "requestId": "optional-client-idempotency-key",
  "createdAt": "2026-06-21T19:02:00Z"
}
```

| Record | Private read | Shared read | Org read | Raw content in org memory | Audit actions | Required tests |
| --- | --- | --- | --- | --- | --- | --- |
| `chat_thread` | owner only | `viewerEmails`/`teamIds` | title/status only after promotion | no | create/archive/restore/visibility | cross-user list/read returns `404` |
| `chat_turn` | owner only | explicit viewers | never by default | no | create/tool_call/redact | private turn does not emit intelligence |
| machine compaction state | backend only | backend only | never | no | create/rotate/delete | not in browser, logs, artifacts, `/readyz` |
| compaction artifact | inherits thread | inherits ACL | visible only if promoted | summary only | create/publish/revoke | redacted artifact content |
| `os_artifact` | creator/private unless shared | named viewers/team | visible when published | artifact text only | update/publish/gate | publish creates reviewed event only |
| `intelligence_event` | approved owner-private | explicit ACL | approved org ACL | structured signal only | create/review/revoke | org view has source refs, no raw chat |
| worker evidence | owner/approvers | shared viewers if artifact shared | redacted unless published | no secrets/logs | callback/rerun/approval | external-write gate evidence |
| capability snapshot | thread viewers | thread viewers | aggregate health only | no | snapshot | no tokens, paths, env, job payloads |

Named-team examples for tests:

- `alice@example.com` owns a private thread.
- `bob@example.com` cannot list or read Alice's private thread and receives `404`.
- Alice shares a compaction artifact with `team:product`; Bob can read only if his account is in `team:product`.
- `admin@example.com` can review approved org learnings but still cannot read raw private turns without explicit share.

Machine compaction state handling:

- Treat it as opaque model continuation state.
- Do not log it, expose it in `/readyz`, return it to the browser, put it in artifacts, or emit it to org intelligence.
- Store in the `0600` data volume initially.
- Add `BONFIRE_CHAT_STATE_ENCRYPTION_KEY` before storing sensitive state outside JSONL or before broader sharing.
- If no encryption key is configured, readiness reports `encryptionConfigured:false`.
- Key rotation is a maintenance operation: write new state records with the new key id, keep old keys read-only until all records are re-encrypted, then audit `chat_state.key_rotated`.
- Redaction must remove bearer tokens, session cookies, runner callback tokens, OpenAI keys, passkey challenge values, password-like fields, private email content not explicitly shared, and raw command logs over the display cap.

Human-readable compaction artifacts:

- Are generated from a redacted summary path, not by copying raw model state.
- Strip bearer tokens, cookies, API keys, password-like fields, large raw logs, and private runner auth details.
- Clearly state they are handoff summaries, not the full conversation.

Prompt and tool-output safety:

- Treat prior user text, artifact text, meeting memory, and tool output as untrusted context.
- Tool-output instructions cannot override system, developer, tool, approval, or visibility policy.
- External write requests still go through approval artifacts even if a prior turn asked Scout to skip gates.

Retention:

- Archive hides but does not delete.
- Delete is future soft-delete plus tombstone.
- Hard delete requires a maintenance command, backup, and audit entry.

## Saved Learnings

Saved learnings are the safe way for private work to improve Scout's organizational view.

Triggers:

- User explicitly publishes or shares an artifact.
- A work thread ends `verified` and the user accepts "Save lesson".
- A critic gate records a reusable implementation or product rule.
- A compaction artifact is explicitly promoted.

Review and approval:

- Default is `reviewStatus=pending`.
- The owner can approve, edit, or reject the proposed learning.
- Org admins can review shared/org learnings.

Stored shape:

- `kind=skill_lesson`, `project_signal`, `blocker`, `decision`, or `expertise_signal`.
- `summary`, `sourceRefs`, `visibility`, `redactionLevel`, `confidence`, `createdBy`, `reviewedBy`, `reviewStatus`.

Retrieval:

- Fast chat can use approved private learnings for the owner.
- Scout org view can use approved shared/org learnings.
- Codex workers receive relevant learnings as source-linked context, not as hidden instructions.

Privacy rule:

- Raw private turns never become learnings automatically.
- A learning stores the reusable lesson and source references, not the whole private conversation.

## OpenAI Adapter Verification

Current docs checked on 2026-06-21:

- `POST /v1/responses` is the ordinary Responses create endpoint.
- The OpenAPI spec lists `POST /v1/responses/compact`.
- The compaction guide states that standalone compaction sends a full context window and returns a new compacted context window to pass as-is to the next `/responses` call.
- The server-side compaction guide states that `responses.create` can use `context_management.compact_threshold`; no separate compact call is needed in that mode.

Wave 3 must re-check the current docs/spec before implementation and fail closed if `responses.compact` parameters or output shape have drifted.

`responses.create` request fields this plan expects:

- `model`
- `input`
- `store:false`
- `previous_response_id` only for server-managed stateful continuation
- `context_management:{compact_threshold:<number>}` only for server-side compaction mode
- `reasoning:{effort:<low|medium|high|xhigh>}` when supported by the selected model profile
- `metadata` with safe Bonfire ids such as `chatThreadId`, `turnId`, and `phase`
- `stream:true` only when the UI path is ready to persist stream events safely

`responses.compact` request fields this plan expects:

- `model`
- `input` containing the full current context window that still fits in the model context

Compaction decision table:

| Mode | When to use | Input stored by Bonfire | Next turn input | Risk |
| --- | --- | --- | --- | --- |
| Server-side `previous_response_id` | Simple personal chat with OpenAI-managed continuation | `previousResponseId`, response metadata, safe output text | newest user message plus `previous_response_id` | less local control over exact compact item |
| Server-side stateless array | Bonfire stores input/output items and wants server compaction threshold | local item window plus response output | append output items as usual | must avoid logging opaque compaction items |
| Standalone `/responses/compact` | Explicit user-visible compaction milestone or pre-fork handoff | compacted output window as opaque machine state | compacted output exactly as returned plus new user message | must pass as-is, not summarize or prune |
| Human-summary fallback | Compact endpoint unavailable or model/profile lacks support | human artifact only, `machineStateUnavailable:true` | regular summarized prompt with lower continuity guarantee | not first-class machine continuity |

Adapter boundary:

- Stateful `previous_response_id` continuation when available.
- Stateless replay with returned output items and preserved `phase`.
- Explicit compaction calls when server-side compaction is not suitable.
- A fallback that creates only a human summary artifact and marks `machineStateUnavailable:true` if first-class compaction is unavailable.
- Fake clients in tests; no network calls in unit tests.

Suggested fake-client interface:

```go
type chatModelClient interface {
	CreateResponse(ctx context.Context, req responseCreateRequest) (responseCreateResult, error)
	CompactResponse(ctx context.Context, req responseCompactRequest) (responseCompactResult, error)
}
```

No-network unit test contract:

- Fake `CreateResponse` stores the exact request, returns deterministic `responseId`, `outputItems`, `usage`, and assistant text.
- Fake `CompactResponse` returns an opaque `encrypted_content` item plus a retained item; tests assert the next create request passes both items through unchanged.
- Tests assert `phase` is preserved across replay, compact, fork, and resume records.
- Tests assert `store:false` is set unless a later privacy review intentionally changes it.
- Tests assert no fake opaque compaction item appears in browser payloads, artifacts, `/readyz`, logs, or intelligence events.

## Trust Evidence Appendix

Evidence gathered for this design:

- Repo code inspected: `scout_chat.go`, `main.go`, `agent_thread_runner.go`, `codex_runner.go`, `codex_runner_queue.go`, `memory.go`, `memory_query.go`, `accounts.go`, `index.html`, and `deploy/digitalocean/docker-compose.yml`.
- Live runner check: `curl -fsS --max-time 20 https://thebonfire.xyz/readyz` returned `codexRunner.enabled:true`, `worker:"codex_exec"`, `runnerMode:"sidecar_queue"`, `callbackSecured:true`, `heartbeatOK:true`, `heartbeatAgeSeconds:1`, `codexCwd:"/workspace/meetingassist"`, and `workspaceGit:true`.
- Live Compose check: `ssh root@146.190.171.224 'cd /opt/meetingassist/deploy/digitalocean && docker compose ps'` showed `digitalocean-codex-runner-1` up and `digitalocean-meetingassist-1` healthy.
- Official OpenAI docs consulted:
  - `https://developers.openai.com/api/docs/guides/latest-model.md`
  - `https://developers.openai.com/api/docs/guides/voice-agents#build-a-speech-to-speech-voice-agent`
  - `https://developers.openai.com/api/docs/guides/deployment-checklist#leverage-compaction`
- Codex manual consulted via `node /Users/ajhart/.codex/skills/.system/openai-docs/scripts/fetch-codex-manual.mjs`, including sections on skills, MCP, app-server, and subagents.

Reverify before implementation:

- Current OpenAI Responses and compaction API shape.
- Current Codex CLI/app-server flags in the runner image.
- Live `/readyz` runner heartbeat.
- Whether `BONFIRE_CODEX_MODEL` should be pinned to `gpt-5.5` on the VPS.

## Current Planning Gate

This planning run is complete only when the final report records:

- Files created or modified.
- Source evidence reviewed.
- Validation commands and results.
- Critic verdicts and revision rounds.
- Commit hash.
- Push remote and branch.
- Confirmation that the VPS was not deployed or restarted for this planning-only commit.

## Open Questions

None block Wave 1. The following should be decided before later waves:

- Whether to keep JSONL long-term or move chat/intelligence records into SQLite/Postgres after the schema proves itself.
- Whether every account gets Codex app-server-backed threads or only workflow mode gets the richer Codex integration.
- What exact org-level sharing policy should govern automatic intelligence event extraction from private work.
- Whether to expose user-installable skills in-product, or keep skills curated by repo/plugin configuration for now.

## Critic Gate Criteria

This design should be accepted only if it satisfies:

- Completeness: addresses personal threads, Codex runner, skills, tools, compaction, Scout org view, privacy, rollout, and verification.
- Technical rigor: maps to current Go files and live VPS constraints.
- Privacy and security: avoids raw private chat leakage and keeps Codex credentials private.
- Feasibility: can ship incrementally without breaking current artifacts.
- User value: gives users a real Codex-like chat experience, not just a renamed artifact list.
