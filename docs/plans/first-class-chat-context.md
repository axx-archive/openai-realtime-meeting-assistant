# First-Class Chat - Strategic Design Context

Date: 2026-06-21

## Mission

Turn Bonfire's private Scout chat into a first-class, account-owned intelligence workspace. Each signed-in user should have a personal Chat tab with saved threads, resumable context, visible compaction events, durable work artifacts, and access to the same kind of goal loop, skills, tools, subagents, and gates that make Codex useful.

This is not just a chat feature. The goal is to capture company energy without leaking information: ordinary thinking, meeting memory, artifacts, decisions, blockers, and Codex worker evidence should become permissioned intelligence that Scout can use to help the company coordinate work over time.

## Current System

The repo already has several strong pieces:

- `scout_chat.go` implements private text chat over the current websocket. It is delivered only to the requesting connection, answers FIFO, and keeps a bounded in-memory history of 12 turns.
- `main.go` exposes `/assistant/query`, `/assistant/threads`, `/assistant/realtime-offer`, `/assistant/realtime-tool`, `/artifacts`, `/artifacts/action`, and `/internal/codex/jobs/result`.
- `agent_thread_runner.go` creates `scoutAgentThread` records backed by `os_artifact` memory entries. Long work gets lifecycle metadata such as `agentLoop`, `goalStatus`, `currentStage`, `progressPercent`, `reviewGate`, and `worker`.
- `codex_runner.go` and `codex_runner_queue.go` implement a real Codex execution bridge. When `BONFIRE_AGENT_THREAD_WORKER=codex_exec`, launched work can enqueue a sidecar job, run `codex exec`, and write status/evidence back through the artifact.
- `memory.go` persists meeting memory as JSONL records with `transcript`, `brain`, `board_update`, `archive`, and `os_artifact` kinds.
- `brain_worker.go` summarizes transcript windows into `brain` records, and `board_worker.go` consumes those summaries to update board state and write auditable `board_update` artifacts.
- `accounts.go` and `auth_http.go` provide signed-in account identity with email, display name, passkeys, sessions, and profile state.
- `index.html` already renders Chat, Artifacts, Research, Design, Grill, Memory, and worker progress surfaces in a single shell.

Important current gap: the system is artifact-backed more than thread-backed. Durable "threads" are currently inferred from `os_artifact` metadata; ordinary chat history is per websocket/browser session and is not saved as first-class account-owned thread history.

## Live Runner Evidence

Read-only checks on 2026-06-21 showed the VPS is already running a private Codex sidecar:

- `https://thebonfire.xyz/readyz` returned `ok:true`.
- `checks.agents.codexRunner.enabled` was `true`.
- `checks.agents.codexRunner.worker` was `codex_exec`.
- `checks.agents.codexRunner.runnerMode` was `sidecar_queue`.
- `checks.agents.codexRunner.callbackSecured` was `true`.
- `checks.agents.codexRunner.heartbeatOK` was `true`.
- `checks.agents.codexRunner.heartbeatAgeSeconds` was `1`.
- `checks.agents.codexRunner.codexCwd` was `/workspace/meetingassist`.
- `checks.agents.codexRunner.workspaceGit` was `true`.

SSH read-only Compose inspection also showed `digitalocean-codex-runner-1` running for 23 hours and `digitalocean-meetingassist-1` healthy. So the correct statement is: Codex is running on the VPS as a private sidecar queue worker, not as the public chat process itself.

## External Source Notes

OpenAI's current guidance says:

- `gpt-5.5` is the current latest model for complex production workflows, grounded assistants, tool-heavy agents, product-spec-to-plan work, and coding workflows.
- `gpt-realtime-2` remains the appropriate live audio model for speech-to-speech voice agents that need low first-audio latency, barge-in, turn taking, and realtime tool use.
- Compaction should be treated as model state, not a hand-edited human summary. If using Responses state with `previous_response_id`, server-side context management can compact automatically. If managing input manually, `/responses/compact` returns compacted output that should be passed forward as-is.
- The Codex app-server exists for deep product integrations that need authentication, conversation history, approvals, and streamed agent events. `codex exec` remains the better fit for noninteractive automation jobs.
- Skills are reusable workflow packages with `SKILL.md`, progressive disclosure, and optional scripts/resources. MCP connects Codex to external tools and context. Subagents are explicitly requested parallel child agents for complex work.

## Design Requirements

1. Account-owned chat threads:
   - Every signed-in user gets personal threads.
   - Threads survive refresh, reconnect, logout/login, deploys, and context compaction.
   - Threads can be titled, archived, resumed, forked, and searched.

2. Codex-like experience:
   - The chat should understand goals, decomposition, agents, dependencies, execution, review, gates, saved learnings, and verification.
   - Explicit skill mentions such as `$critic-loop`, `$wave-plan`, and `$strategic-design` should route into a capability registry and become available to Codex workers.
   - Long-running or tool-heavy work should stream status, preserve evidence, and produce inspectable artifacts.

3. Model and tool quality:
   - Ordinary text intelligence should use `gpt-5.5` by default unless evals show a better tradeoff.
   - Voice should continue using `gpt-realtime-2`, with transport concerns kept separate from business logic.
   - Tools should be declared with scope, side-effect level, availability, required approvals, and user visibility.

4. Compaction as a product event:
   - Before or during compaction, the user should see a gentle animation and status.
   - Machine compaction state must be stored intact for continuation.
   - A human-readable compaction artifact should also be saved so the company can inspect what was preserved without parsing opaque model state.

5. Scout's organizational view:
   - Scout should gain a permissioned org-level intelligence layer built from artifacts, thread summaries, meeting brain records, decisions, blockers, and explicit publish/share events.
   - Personal/private threads must not leak raw contents into org memory.
   - The org-level layer should store structured, permissioned signals, not unrestricted transcripts of private user thinking.

6. Safety and gates:
   - Read-only, workspace-write, and external-write authority must stay explicit.
   - Commit, push, deploy, SSH, email, external API writes, and production mutations must remain approval-gated.
   - The public app must never expose Codex auth, Codex app-server transports, or runner filesystem access directly.

7. Rollout compatibility:
   - Existing artifacts and meeting memory must keep rendering.
   - The current `/assistant/threads` and `codex-runner` path should remain functional during migration.
   - Data changes should be append-friendly, backup-friendly, and safe for the existing VPS volume.

## Perspectives Needed

- Product lead: define the personal chat journey, thread/tab semantics, adoption path, and where ordinary chat should become durable work.
- Technical architect: design the data model, APIs, state transitions, compaction chain, Codex runner/app-server boundary, and migration sequence.
- Security/privacy engineer: enforce user ownership, visibility, redaction, approval gates, auditability, and no public exposure of Codex credentials.
- AI systems architect: choose model profiles, Responses state/compaction strategy, Realtime boundary, skill loading, tool registry, and Codex worker shape.
- UX designer: make Chat feel first-class, show compaction and worker progress clearly, and keep the shell coherent on desktop and mobile.
- Quality analyst: define tests, live checks, rollout gates, and failure evidence.

## Deliverable

This strategic design produces:

- `docs/plans/first-class-chat-design.md`: accepted unified architecture.
- `docs/plans/first-class-chat-execution-plan.md`: wave-based implementation plan.
- `docs/plans/first-class-chat-execution-log.md`: future execution tracker and Wave 1 prompt.

Implementation is intentionally split into waves. This commit should preserve the plan and push it to `axx/main`; it should not mutate production behavior or deploy the VPS.
