# Spectacular OS — Technical Architecture

**Owner:** Data/Technical Analyst · **Date:** 2026-07-03 · **Status:** design input for lead synthesis

This is the rigor layer for the Spectacular OS wave: the model-agnostic agent-runner abstraction, the `/goal` loop execution engine, Realtime-2 tool-parity plumbing, the slop-quarantine data model, the recall/embeddings decision, a risk register, and a test strategy. It is grounded in the current code (file:line refs throughout) and is schema-first: every long-running state that matters is written down as a struct or JSON shape.

Design invariants this document holds to:

1. **Model-agnostic = swappable by env var, not by refactor.** One selection function, N provider implementations behind one interface.
2. **Every long-running job is resumable and inspectable.** State lives on the durable `os_artifact` record (and the queue file), never only in a goroutine.
3. **Existing safety gates are load-bearing.** The `read_only / workspace_write / external_write` authority ladder (`codex_runner_queue.go:19-21`), `approval_required` (`:27`), and the admin gate on `aj@shareability.com` (`main.go:1187`, `isArtifactApprovalAdmin`) are extended, never bypassed.
4. **Schemas first.** If it isn't in a schema, it doesn't exist.
5. **Six users, one VPS.** Boring, observable tech. JSONL + files-on-a-shared-volume over any new datastore.

---

## 0. Current-system map (the seams we extend)

The two swap points already exist and are the whole reason this is tractable:

- **Worker selection:** `configuredAgentThreadWorkerMode()` (`codex_runner.go:50`) returns `openai_text_response` (default) or `codex_exec`, driven by `BONFIRE_AGENT_THREAD_WORKER` / `BONFIRE_CODEX_AGENT_THREADS`. `configuredCodexRunnerMode()` (`:69`) chooses `sidecar_queue` (default) vs `local_exec`.
- **Dispatch fork:** `produceAgentThreadArtifactWithWorker()` (`agent_thread_runner.go:427`) is a 3-way switch: local codex exec, sidecar enqueue, or the OpenAI text responder. **This function is the single seam the new `AgentRunner` interface replaces.**
- **The OpenAI path is not agentic.** `createOpenAITextResponse` (`openai_responses.go:55`) is one text completion with `Instructions`+`Input`, no tool loop. It produces a *description* of the 10-step loop, it does not *execute* it. That is the gap this wave closes.
- **The codex path is agentic but external.** Jobs are JSON files in a queue dir on the shared `meeting_data` volume (`codex_runner_queue.go:33-50, 89`), claimed by the `-codex-runner` sidecar, which runs `codex exec` in a sandboxed git workspace and POSTs an idempotent callback to `/internal/codex/jobs/result` (`:687`) gated by `BONFIRE_RUNNER_TOKEN` (`:662`).
- **Artifact = job record.** Every thread writes a running `os_artifact` (`agent_thread_runner.go:68-95`) carrying `agentLoop`, `workflowStages`, `goalStatus`, `currentStage`, `progressPercent`, `reviewGate`, `status`/`threadStatus`, `worker`, `workerBoundary`. Terminal seams (`runAgentThread` `:135`, the callback handler `:687`) update it and fire linkage/notification/delivery. **This is our resumable store — we do not add a database.**

The upshot: we are not building an orchestrator from scratch. We are (a) making the default worker actually run a tool loop instead of writing prose, (b) formalizing the 10-step scaffold that is already stamped in metadata into a real state machine, and (c) putting a provider interface at `produceAgentThreadArtifactWithWorker`.

---

## 1. Model-agnostic agent-runner abstraction

### 1.1 The interface

One interface, in-process, async-first. It replaces the ad-hoc `agentThreadWorkerResult` return with a typed job/handle pair. Progress is reported by callback so a slow provider (codex sidecar) and a fast one (in-process API loop) map to the same artifact-update path.

```go
// agent_runner_iface.go (new)

// AgentJob is the provider-agnostic unit of agentic work. It is derived from a
// scoutAgentThread + its os_artifact, so nothing new is persisted to launch a
// job — the artifact IS the durable record.
type AgentJob struct {
    JobID        string            // == artifact/thread run id
    ArtifactID   string            // durable os_artifact this job writes back to
    ThreadID     string
    Mode         string            // research|design|grill|workflow|artifacts|goal
    Objective    string            // the user's goal text (thread.Query)
    Authority    string            // read_only|workspace_write|external_write
    Plan         *GoalPlan         // nil for single-shot modes; set for /goal (§2)
    Context      AgentJobContext   // board snapshot, memory window, domain vocab, tool prompts
    Origin       map[string]string // originKind/originId/originMeetingId (delivery)
    RequestedBy  string            // signed-in email; provenance + authority checks
}

// AgentCapabilities is what a provider can physically do. The engine reads these
// to decide which runner may take a subtask (a can_shell:false runner never gets
// a "run the tests" subtask).
type AgentCapabilities struct {
    CanShell    bool          // run commands, tests
    CanBrowse   bool          // live web / --search
    CanEditRepo bool          // mutate files in a git workspace
    CanCommit   bool          // external_write side effects (still admin-gated)
    ToolLoop    bool          // can call Bonfire in-process tools mid-run
    MaxRuntime  time.Duration
}

// AgentProgress is one streamed update. The engine translates it to artifact
// metadata (progressPercent, currentStage, goalStatus, reviewGate) and a
// broadcastAssistantEvent, reusing the exact terminal-seam plumbing today.
type AgentProgress struct {
    Stage           string // one of goalWorkflowStage* (§2.1)
    ProgressPercent int    // 0..100 -> metadata["progressPercent"]
    GoalStatus      string // running|review|approval_required|verified|needs_attention
    ReviewGate      string // pending|passed|blocked|approval_required
    Note            string // short operator-voice line, broadcast to the UI
    Terminal        bool
    Text            string // set on Terminal: the finished artifact body
    Err             error  // set on Terminal failure
    Metadata        map[string]string // provider evidence (codexSandbox, model, tokens…)
}

// AgentRunner is the one seam. RunJob is non-blocking: it returns a channel of
// progress the engine drains onto the artifact. Cancel is honored on restart /
// timeout / user stop.
type AgentRunner interface {
    Name() string
    Capabilities() AgentCapabilities
    RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error)
}
```

Why async/channel rather than the current synchronous `(agentThreadWorkerResult, error)`: the codex sidecar path is *already* asynchronous (it returns a non-terminal "queued" result at `agent_thread_runner.go:141` and lands the real result via the HTTP callback minutes later). The channel model makes that first-class instead of a special case, and lets the in-process Anthropic loop stream `execute_in_order → review → gate` transitions to the UI as they happen. For the sidecar provider, `RunJob` enqueues the file and returns a channel that emits one `{queued}` progress then closes; the callback handler (`internalCodexRunnerResultHandler`) feeds the terminal progress through the same artifact-update code. No behavior regresses.

### 1.2 Provider implementations

| Provider | `Name()` | Runs where | Capabilities | Backing |
|---|---|---|---|---|
| **anthropic_fable** (default) | `anthropic_fable` | in-process (Go) | `ToolLoop:true, CanBrowse:true(server tool), CanShell:false, CanEditRepo:false, CanCommit:false` | Anthropic Messages API tool-use loop, `claude-fable-5` |
| **codex_sidecar** | `codex_sidecar` | sidecar container | `CanShell/CanEditRepo/CanBrowse:true, CanCommit:true(gated), ToolLoop:false` | today's `codex exec` queue (`codex_runner_queue.go`) |
| **codex_local** | `codex_local` | in-process exec | same as sidecar minus isolation | today's `runCodexExecCommand` (`codex_runner.go:304`) |
| **openai_text** (fallback) | `openai_text` | in-process | `ToolLoop:false` (single completion) | today's `createOpenAITextResponse` |
| **claude_sidecar** (optional, later) | `claude_sidecar` | sidecar container | same as codex_sidecar | `claude` CLI non-interactive, drop-in on the same queue contract |

The default orchestrator is **anthropic_fable, in-process, raw Messages API tool-loop.** The reasoning is in §1.4.

`anthropic_fable` runner sketch (the new code):

```go
type anthropicFableRunner struct {
    app       *kanbanBoardApp
    apiKey    func() string        // ANTHROPIC_API_KEY, mirrors currentOpenAIAPIKey()
    model     string               // BONFIRE_ORCHESTRATOR_MODEL, default "claude-fable-5"
    maxTurns  int                  // hard cap on tool-loop iterations
}

func (r *anthropicFableRunner) RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error) {
    out := make(chan AgentProgress, 8)
    go func() {
        defer close(out)
        // 1. Build the Messages request: system = orchestrator instructions +
        //    mode contract (agentThreadModeContract) + authority rules; tools =
        //    the Bonfire tool schemas the orchestrator may call (a curated subset
        //    of kanbanTools(), see §3), each mapped to applyToolCallArgs.
        // 2. Loop: POST /v1/messages; while stop_reason == "tool_use", execute
        //    each tool_use via app.applyToolCallArgs(name, input), append
        //    tool_result, emit AgentProgress{Stage, ProgressPercent, Note}.
        // 3. On a `report_goal_state` tool call (a control tool we define),
        //    translate its args straight to goalStatus/reviewGate/progress.
        // 4. Terminal: emit AgentProgress{Terminal:true, Text: final message}.
    }()
    return out, nil
}
```

Crucially, the orchestrator's tools are the *same in-process Go functions* the Realtime bridge already calls (`applyToolCallArgs`, `applyPrivateRealtimeVoiceTool`). We do not re-expose them over MCP/HTTP. Board mutation, memory recall, package moves, notification, sub-thread launch — all reachable from the loop with zero new transport.

### 1.3 Selection & config (env only)

```
BONFIRE_AGENT_RUNNER = anthropic_fable | codex_sidecar | codex_local | openai_text   # orchestrator default
BONFIRE_ORCHESTRATOR_MODEL = claude-fable-5                                            # anthropic path
BONFIRE_EXECUTION_RUNNER   = codex_sidecar | claude_sidecar | codex_local | none       # who does can_shell/can_edit work
ANTHROPIC_API_KEY = ...                                                                # secret (§1.5)
```

`configuredAgentThreadWorkerMode()` is rewritten to return a runner name; a new `selectAgentRunner(job)` returns the concrete `AgentRunner`. Back-compat: the existing `BONFIRE_AGENT_THREAD_WORKER=codex_exec` and `BONFIRE_CODEX_AGENT_THREADS` envs map onto `BONFIRE_AGENT_RUNNER=codex_sidecar` so no deploy config breaks. Keyless-local (`go run .` with no keys) must keep working: `selectAgentRunner` falls back to a **stub runner** that writes the same "worker not configured — handoff pending" artifact the OpenAI path writes today when `OPENAI_API_KEY` is empty (`agent_thread_runner.go:454`). The whole shell stays up; agentic features 503 cleanly, exactly as the context brief requires.

### 1.4 Recommendation: in-process API tool-loop for orchestration, sidecar for execution

The brief asks whether the default should be the **Claude Agent SDK / `claude` CLI non-interactively in the sidecar** or a **raw Anthropic Messages API tool-loop in-process**. Recommendation: **raw Messages API tool-loop, in-process, as the orchestrator; keep the codex (or claude) sidecar as the execution backend for shell/repo/commit subtasks.** This is a two-tier split, not an either/or.

Reasoning:

- **The orchestration work is reasoning + Bonfire tools, and every Bonfire tool is already an in-process Go function.** Decompose / assign / coordinate / review / gate / report are model turns that call `applyToolCallArgs`, `answerAssistantQuery`, `launchAgentThread`, `attachToPackage`, `createNotification`. Shipping those to a CLI sidecar would force re-exposing them over HTTP or MCP and re-plumbing session auth for six users — pure cost, no benefit. In-process, the loop calls the same function the Realtime bridge calls.
- **Latency and observability.** The room/private Scout is a fast voice control plane; when a user says "/goal package the Aurora IP," the first few steps (restate goal, decompose, assign, kick off sub-threads) should stream back in seconds. An in-process loop emits `AgentProgress` directly onto the artifact and the office socket. A CLI sidecar adds container hop + file-queue latency to *every* orchestration step, which is right for a 20-minute code job and wrong for a control loop.
- **Secrets stay in one place.** `ANTHROPIC_API_KEY` lives in the main process env (like `OPENAI_API_KEY` today, read via a `currentAnthropicAPIKey()` mirror of `openai_responses.go`'s pattern). A CLI sidecar would need the key mounted into a second container plus `~/.claude` credential state on a volume (the compose file already does this for codex via `codex_home`), doubling the secret surface.
- **Execution stays in the sidecar because that is where the safety already lives.** The sidecar has the sandboxed git workspace (`/workspace/meetingassist`), bubblewrap, `--sandbox read-only|workspace-write`, and — most importantly — it is downstream of the `external_write → approval_required → admin gate` ladder. Shell/commit/deploy work must NOT move in-process where it would bypass that isolation. When the orchestrator needs code changed or tests run, it launches a *sub-job* whose runner is `BONFIRE_EXECUTION_RUNNER` (codex_sidecar today), inheriting the authority ladder unchanged.
- **`claude` CLI in the sidecar is a first-class swap, later, not the default.** If we want Claude (not codex) doing the repo work, add a `claude-runner` container target to the Dockerfile with the same `-codex-runner`-style queue-claim loop and the same callback contract (`sendCodexRunnerCallback` → `/internal/codex/jobs/result`). Because the queue file schema (`codexRunnerJob`) is provider-neutral, this is an ENTRYPOINT + a claim loop, not a redesign. That satisfies "model-agnostic on the execution side too" without making it the risky default on day one.

So: **Fable 5 orchestrates in-process; codex (or, by env, claude) executes in the sidecar behind the existing gate.** Both are swappable by env var.

### 1.5 Secrets handling

- `ANTHROPIC_API_KEY` in the main process env via `.env` (never committed — the deploy preserves `.env`, memory: bonfire-vps-deploy-ops). Read through a single `currentAnthropicAPIKey()` accessor mirroring `currentOpenAIAPIKey()` (`agent_thread_runner.go:478`) so tests can inject and keyless-local degrades gracefully.
- The key is **never** written to an artifact, a queue job file, or a log line. Provider evidence stamped on artifacts records model name and token counts, not credentials — same discipline as `appendCodexWorkerEvidence` (`codex_runner.go:277`), which already redacts to sandbox/model/reasoning.
- `apiRequestFailedError` (`openai_responses.go:148`) logs the upstream body server-side and returns a status-only user error; the Anthropic client reuses this so a 401/429 body never reaches the browser.
- The sidecar keeps its own key (`CODEX_API_KEY`/`OPENAI_API_KEY`, or a future `ANTHROPIC_API_KEY` for claude_sidecar), mounted via `.env` into the `codex`/`claude` runner container only — the split-secret surface is intentional and bounded to the isolation container.

---

## 2. The `/goal` execution engine

Today the 10-step loop is *scaffolding text* stamped in metadata (`goalWorkflowStageMetadata`, `currentStage`, `progressPercent`) and *narrated* by a single model call. The engine turns it into a **persisted state machine on the artifact record**, executed by the orchestrator runner, with real gates that are themselves model calls.

### 2.1 State machine

The ten steps collapse to a state enum stamped in `metadata["currentStage"]` (the field already exists and drives the UI progress card). Percent is advisory display; **state is authoritative.**

```
identify_goal ─▶ decompose ─▶ assign ─▶ coordinate ─▶ execute_in_order
     ▲                                                      │
     │ (replan on subtask failure, bounded)                 ▼
 needs_attention ◀──────────────── review_against_goal ◀────┘
     ▲                                    │
     │                          pass ─────┤───── fail ─▶ (retry/replan)
     │                                    ▼
     └──── blocked ◀──── gate_before_shipping ──▶ approval_required ──(admin approve)──┐
                                    │ pass                                              │
                                    ▼                                                   ▼
                            save_what_worked ─▶ report ─▶ verify_goal ─▶ commit_push ─▶ verified(terminal)
                                                                            │ (external_write only)
                                                                            └─▶ approval_required
```

State enum (Go consts, extending the existing stage strings so no UI change is forced):

```go
const (
    goalStateIdentify   = "identify_goal"
    goalStateDecompose  = "decompose"
    goalStateAssign     = "assign"
    goalStateCoordinate = "coordinate"
    goalStateExecute    = "execute_in_order"
    goalStateReview     = "review_against_goal"
    goalStateGate       = "gate_before_shipping"
    goalStateSave       = "save_what_worked"
    goalStateReport     = "report"
    goalStateVerify     = "verify_goal_completed"
    goalStateCommit     = "commit_push"          // external_write path only
    goalStateVerified   = "verified"             // terminal success
    goalStateBlocked    = "needs_attention"      // terminal-until-human
    goalStateApproval   = "approval_required"    // waiting on admin gate
)
```

These are a superset of what `agent_thread_runner.go` already writes (`currentStage:"execute_in_order"`, `goalStatus:"verified"`, `reviewGate:"passed"`), so the running-artifact card renders unchanged; new states just add fidelity.

### 2.2 The goal plan schema

The plan is JSON, stored in `metadata["goalPlan"]` on the artifact (string-encoded, like `workflowStages` is today). One artifact = one goal = one plan. Subtasks are the unit of assignment and dependency.

```json
{
  "planVersion": 2,
  "goalId": "agent-thread-goal-1720000000000000000",
  "objective": "Package the Aurora IP into an investor-ready one-pager and deck",
  "createdBy": "aj@shareability.com",
  "authority": "workspace_write",
  "packageId": "pkg-aurora",
  "subtasks": [
    {
      "id": "st-1",
      "title": "Market map for Aurora's category",
      "mode": "research",
      "runner": "anthropic_fable",
      "authority": "read_only",
      "dependsOn": [],
      "status": "complete",
      "artifactId": "os-artifact-...",
      "attempts": 1,
      "review": {"verdict": "pass", "score": 8.5, "by": "reviewer_model"}
    },
    {
      "id": "st-2",
      "title": "One-pager draft from the market map",
      "mode": "design",
      "runner": "anthropic_fable",
      "authority": "workspace_write",
      "dependsOn": ["st-1"],
      "status": "running",
      "artifactId": "os-artifact-...",
      "attempts": 1,
      "review": null
    }
  ],
  "gate": {"status": "pending", "reviewedBy": "", "approvalRequired": false},
  "report": {"headline": "", "artifactIds": []},
  "verification": {"verdict": "pending", "checkedAt": ""}
}
```

Subtask `status` enum: `pending | ready | running | review | complete | failed | blocked`. A subtask is `ready` when every `dependsOn` id is `complete`. The coordinator (§2.3) advances `pending → ready` and executes ready subtasks respecting concurrency limits.

### 2.3 Transition rules & execution

- **identify_goal → decompose:** one orchestrator model call produces the `subtasks[]` (JSON, validated against the schema; a malformed plan is a retryable error, capped at 2 attempts, then `needs_attention`). Decomposition is bounded: **max 6 subtasks** (six users, one VPS — a plan that wants 40 subtasks is a modeling error, reject and ask the model to coarsen).
- **assign:** each subtask's `runner` is chosen by capability match: a subtask tagged `can_shell`/`can_edit_repo` (mode implies it, or the objective text does per `codexJobAuthorityForThread` `codex_runner_queue.go:204`) is assigned the `BONFIRE_EXECUTION_RUNNER`; everything else goes to the orchestrator runner. Assignment is pure and re-derivable on restart.
- **coordinate → execute_in_order:** a topological pass. **Concurrency cap = 2 in-flight subtasks** (VPS memory ceiling; §6 risk). Each subtask runs as its own `launchAgentThreadWithOrigin` child whose completion callback advances the parent plan — reusing the existing linkage/delivery seams rather than inventing a scheduler. The parent artifact's `progressPercent` = completed/total subtasks * 80 (reserve 80–100 for review/gate/verify).
- **review_against_goal:** **this step is a model call, not a heuristic.** A `reviewer` role (same runner, a distinct system prompt) is handed the objective + each subtask's produced artifact and returns `{verdict: pass|fail|revise, score, reasons}`. `fail`/`revise` on a subtask bumps its `attempts` and re-queues it with the review notes appended (bounded: **max 2 revisions per subtask**, then the subtask is `blocked` and the whole goal goes `needs_attention` with a precise blocker line). This mirrors the grill/critic pattern the codebase already trusts.
- **gate_before_shipping:** a second, cheaper model call answering one question: "is this safe and complete to publish/deliver?" It sets `gate.status`. If the goal's authority is `external_write` (commit/push/deploy — detected exactly as today at `codex_runner_queue.go:207`), the gate forces `approval_required` and the engine **stops**, writing the same approval artifact `codexApprovalRequiredResult` writes today (`:248`). No code path lets the orchestrator self-approve an external write.
- **save_what_worked:** the successful plan + which subtask prompts/runners produced passing artifacts is written as a durable `os_artifact` (kind stays `os_artifact`, `mode=goal`) and, when the objective named a package, `attachToPackage` files it (idempotent, `linkage.go:181`). This is the "business as intelligence" flywheel input.
- **report:** one short model call → `report.headline` + the artifact ids. Only this headline is spoken/notified; the detail lives in the artifact. ("Report only what matters.")
- **verify_goal_completed:** a final model call checks the objective against the produced artifacts and sets `verification.verdict`. `pass` → `verified` (terminal). `fail` → `needs_attention`.
- **commit_push (external_write only):** never reached without a prior admin approval flipping the gate. When approved, the engine enqueues a single `codex_sidecar` job with `external_write` authority for the exact command the gate recorded, and that job's callback lands the terminal state. Commit/push therefore stays behind the sidecar isolation AND the admin gate — two independent controls.

### 2.4 Where human approval slots in

Exactly one place in the happy path: `gate_before_shipping` → `approval_required` for `external_write` goals. It reuses the entire existing mechanism — the artifact goes to `reviewGate:"approval_required"`, the creator is notified (`notifyAgentThreadCreator`), and only `aj@shareability.com` sees approve/reject (`isArtifactApprovalAdmin`, `main.go:1187`). Separately, `/goal` initiated by *voice* still passes through `propose_codex_task` when the user says "later/queue it" (`codex_proposals.go:33`), which requires a human confirm before the plan even starts. Two independent human gates, both pre-existing, both extended not bypassed.

### 2.5 Resumability after restart — the hard requirement

Because the plan lives in `metadata["goalPlan"]` on a durable JSONL artifact, a server restart loses only in-flight goroutines, not state. On boot we add a **reconciler** (a tiny ambient pass, one tick, mirrors `flushAmbientAgentsForArchive` `agent_runner.go:226`):

```
for each os_artifact where mode=goal and currentStage not in {verified, needs_attention, approval_required}:
    load goalPlan
    for each subtask in {running}: if its child artifact is terminal -> fold result into plan
                                   else -> re-mark ready and re-dispatch (idempotent by subtask id)
    recompute plan state and resume from the earliest non-complete state
```

Idempotence keys already exist and are reused: `deliveredAt` (`agent_thread_runner.go:251`), the `changed` guard on callbacks (`codex_runner_queue.go:797`), and `runnerJobId` match (`:731`). A subtask re-dispatched after restart re-uses its own artifact id, so a duplicate completion is a no-op. **No orphaned states:** any goal not in a terminal stage is, by construction, resumable from its plan. Codex sidecar jobs already survive restart the same way (queue files on the shared volume; the sidecar re-claims stale jobs, `defaultCodexRunnerStaleAfter` `:30`).

### 2.6 Failure & retry semantics (summary table)

| Failure | Detection | Response | Bound |
|---|---|---|---|
| Malformed decompose JSON | schema validate | re-prompt orchestrator | 2 attempts → `needs_attention` |
| Subtask worker error | child artifact `status=error` | fold error, review decides retry vs block | 2 revisions → subtask `blocked` |
| Review verdict = fail | reviewer model call | re-queue subtask with notes | shares the 2-revision bound |
| Gate = unsafe | gate model call | `needs_attention` + blocker line | terminal-until-human |
| External write needed | authority classifier | `approval_required`, stop | admin unblocks |
| Provider 429/timeout | HTTP error | exp backoff, then `needs_attention` | `MaxRuntime` per runner |
| Server restart mid-goal | boot reconciler | resume from plan | — |

---

## 3. Realtime-2 tool parity ("she can do it all")

Today the private voice allowlist is 14 tools (`privateRealtimeVoiceTools()` `kanban.go:1041`) out of the 29-tool registry. The goal is near-parity while keeping the room/private boundary honest and the safety gates intact.

### 3.1 Classification of the 29 registry tools for private voice

**Already allowed (14):** `control_app`, `create_artifact`, `launch_agent_thread`, `answer_memory_question`, `propose_codex_task`, `create_package`, `attach_to_package`, `advance_package_stage`, `send_notification`, `post_to_channel`, `create_channel`, `meeting_recap`, `catch_me_up`, `do_nothing`.

**Add to private voice (existing tools, safe to expose):**
- `update_artifact`, `publish_artifact` — the private user owns artifact editing; the trust boundary is the seeded team (`main.go` admin comment). Dispatch already exists (`updateRealtimeArtifact` `kanban.go:2808`, `publishRealtimeArtifact` `:2849`).
- Board mutations `create_ticket`, `move_ticket`, `update_ticket`, `add_tags`, `add_key_date`, `remove_key_dates`, `delete_ticket`, `undo_delete_ticket` — **with a caveat.** The private-voice instructions currently say "do not mutate the shared Kanban board from this private surface" (`kanban.go:1031`). Product intent for "she can do it all" is that the *private* Scout CAN drive the board for you. Recommendation: **allow board mutation from private voice**, and rewrite that instruction line to "you may update the board on the user's behalf; you are not the room's shared voice." The mutation path (`applyToolCallArgs`) is identical and already broadcasts to all clients, so this is an allowlist + instruction change, no new dispatch.

**Keep room-only (with reason):**
- `set_voice_control`, `set_recording`, `archive_meeting`, `start_grill_session`/`end_grill_session` — these operate the *shared room* session/recording. `set_recording` and `archive_meeting` mutate room-wide state for every participant; the private surface has no room. `start_grill_session` swaps the *shared* room session instructions (`grill.go:1-8`, room-only by construction). Private grill is a *new* variant (§3.3), not this tool.

### 3.2 New tool schemas for the gaps

Four capabilities in the brief have no existing tool. New schemas (names + params), each added to `kanbanTools()` and the private allowlist:

**(a) `read_thread_aloud` — read responses aloud.** The private voice is already an audio-out Realtime session; "read aloud" means fetching text and letting the model speak it. This is a *recall-shaped* tool, not a TTS pipeline.
```json
{"name": "read_thread_aloud",
 "parameters": {"properties": {
   "target": {"enum": ["channel", "private_thread", "artifact", "notifications"]},
   "ref": {"type": "string", "description": "channel name, thread id, or artifact id"},
   "limit": {"type": "integer", "description": "how many recent messages to read; default 3"}
 }, "required": ["target", "ref"]}}
```
Dispatch returns the resolved text in the tool result; the model reads it in its spoken turn. No new audio plumbing — the session already outputs audio.

**(b) `start_chat_as_user` — start a chat and post on the user's behalf, with disclosure.** Post-as-user already exists for channels (`post_to_channel` posts as the requester). The gap is (i) *starting* a new thread/DM and (ii) an explicit disclosure stamp so a human-authored message and a Scout-posted-on-behalf message are distinguishable.
```json
{"name": "start_chat_as_user",
 "parameters": {"properties": {
   "audience": {"enum": ["channel", "thread"]},
   "name": {"type": "string", "description": "channel/thread name to create or address"},
   "text": {"type": "string", "description": "message to post, quoting the user faithfully"},
   "disclose": {"type": "boolean", "description": "always true; stamps 'posted by Scout on behalf of <user>'"}
 }, "required": ["audience", "name", "text"]}}
```
Dispatch: create the thread if needed (`createChannelByVoice`/thread create), then commit a message whose record carries `postedOnBehalfOf: <email>` and a rendered "· via Scout" tag. **Disclosure is mandatory** — the record is stamped server-side regardless of the model's `disclose` value, so Scout can never silently impersonate. This is the one place a model action speaks *as* a human; the audit stamp is the safety control.

**(c) `initiate_goal` — start the /goal pipeline by voice.** Voice, `/goal <objective>` text, and the quick-select menu all converge here.
```json
{"name": "initiate_goal",
 "parameters": {"properties": {
   "objective": {"type": "string"},
   "package": {"type": "string", "description": "optional package name/id to file the result under"},
   "authority_hint": {"enum": ["read_only", "workspace_write"], "description": "external_write is never set by voice; it is earned at the gate"}
 }, "required": ["objective"]}}
```
Dispatch launches a `mode=goal` thread through the §2 engine as the signed-in requester. It deliberately cannot request `external_write` — that authority is only reachable through the gate + admin approval, never from a voice argument.

**(d) `open_tabs` — open surfaces.** *Already covered* by `control_app` (opens office/room/chat/artifacts/research/design/grill/board/memory). To "open tabs" plural (e.g. open the market-map artifact AND the deck), the model calls `control_app` twice, or we extend it with an optional `also_open` array. Recommend the minimal extension:
```json
"also_open": {"type": "array", "items": {"enum": ["office","room","chat","artifacts","research","design","grill","board","memory"]}}
```
No new tool; `controlApp` (`kanban.go:2693`) loops the extra surfaces into `osAssistantActions`.

**Notify users / post in threads:** `send_notification` and `post_to_channel` already cover these. The only missing piece for *arbitrary* threads (not just public channels) is posting into a private thread you own — folded into `start_chat_as_user` (b) with `audience:"thread"`.

### 3.3 Private grill — feasibility and mechanism

The brief flags this as needing verification because `grill.go` is room-only. **Finding: private grill is feasible but must be driven client-side, because the private and room sessions have different ownership.**

- **Room Scout:** the Go server owns the peer and the data channel (`app.events`, `SendEvent` `kanban.go:1133`). `start_grill_session` pushes `session.update` server-side to swap instructions (`grill.go` → `refreshRealtimeBoardContext` → `sessionConfig`). The server can do this because it holds the channel.
- **Private voice:** `createPrivateRealtimeVoiceCall` (`kanban.go:981`) only *proxies the SDP*; the **browser** holds the peer connection and data channel. Tool calls come back to the server over HTTP (`assistantRealtimeToolHandler` `main.go:1132` → `applyPrivateRealtimeVoiceTool`). The server has **no handle** to push `session.update` into the private session.

Therefore private grill works like this:
- New tools `start_private_grill` / `end_private_grill` (private-allowlisted). Their **dispatch returns the replacement instruction block** (persona + grill rules, built by a private variant of the grill instruction builder, sanitized via the existing `sanitizeGrillStyleText` `grill.go:29`) in the tool result.
- The **browser** applies `session.update` over its own data channel using those instructions, then reverts on `end_private_grill`. index.html already owns the private session's data channel (`beginPrivateRealtimeVoiceSession` `index.html:20628`), so it is the natural place to apply the swap.
- A client-side safety timer mirrors `defaultGrillMaxDuration` (`grill.go`, 15 min) so a private grill can't hold the session forever. On end, the graded report is filed exactly as room grill does — via `launch_agent_thread` mode=grill, which parses `READINESS: X/10` (`agent_thread_runner.go:188`).

This keeps the instruction-swap where the channel actually lives and reuses the grill persona/report machinery. It is a genuinely different mechanism from room grill and the design must say so — hence the client-driven note.

### 3.4 Dispatch + instruction-builder changes (concrete)

- `privateRealtimeVoiceTools()` (`kanban.go:1041`): extend the `allowed` set with the tools from §3.1 (board mutations + update/publish) and the new tools from §3.2–3.3.
- `applyPrivateRealtimeVoiceTool` (`kanban.go:2643`): add requester-aware branches for `start_chat_as_user` (stamps `postedOnBehalfOf`), `initiate_goal` (launches as requester), `read_thread_aloud` (resolves + returns text), `start_private_grill`/`end_private_grill` (return instructions, no server session mutation). Board tools fall through to the existing `applyToolCallArgs`.
- `privateRealtimeVoiceSessionInstructions()` (`kanban.go:1028`): rewrite the "do not mutate the board / do not grill" boundary lines to reflect the new powers, while keeping the honest boundaries that remain (not the shared room voice; external writes still gated; disclosure required when posting as the user).

---

## 4. Slop-quarantine data model

Quarantine is a **memory-entry lifecycle**, not a new store. It rides on the existing JSONL `metadata` map and the existing `isUIStateMemoryKind` search-exclusion mechanism (`memory.go:708`).

### 4.1 New fields (on `meetingMemoryEntry.Metadata`)

```
relevance        = active | quarantined | expired      # default active (absent == active)
classifierVerdict= keep | slop | uncertain
classifierScore  = "0.0".."1.0"                         # confidence
classifierReason = short phrase, why it was flagged
quarantinedAt    = RFC3339
reviewedBy       = <email> | "classifier"              # who moved it (restore stamps the human)
expiresAt        = RFC3339 (quarantinedAt + 30d)
```

No schema migration: entries without these keys are `active`. The lifecycle is `active → quarantined → (restored:active | expired)`. Expired entries are hard-deleted by the expiry job (§4.4) — the only hard delete in the system, and only after 30 visible days.

### 4.2 Search / recall exclusion

`store.search` (`memory.go:712`) currently skips `isUIStateMemoryKind`. Add one guard: skip entries where `metadata["relevance"]` is `quarantined` or `expired`. That single line removes quarantined material from Scout recall, the model context builders (`memoryMatchesAndContext`), and the client timeline (`visibleMeetingMemoryEntries`), because they all funnel through the same store. Restore = flip `relevance` back to `active`; it re-enters recall immediately.

### 4.3 The ambient classifier worker

Reuse the `ambientAgentConfig` recipe (`agent_runner.go:31`) verbatim — same cursor/baseline/backfill/interval machinery every other ambient agent uses.

```
name:            "slop_classifier"
inputKind:       transcript  (also a second pass over os_artifact)  // see rules below
artifactKind:    slop_pass    (new UI-state cursor kind, like decision_pass)
cursorMetadataKey: "slopConsumedThrough"
defaultInterval: 15m   (SLOP_CLASSIFIER_INTERVAL; "off" disables)
minBatch:        8     (don't wake the model for a trickle)
```

- **Prompt I/O contract:** input = a batch of candidate entries (id, kind, one-line excerpt) + the company's relevance criteria (Domain Strategist owns the criteria text; the classifier consumes it as a system prompt). Output = strict JSON `[{id, verdict: keep|slop|uncertain, score, reason}]`. Anything `verdict:slop && score >= SLOP_CONFIDENCE_MIN (default 0.8)` is quarantined; `uncertain` or low-confidence is left `active` (bias to keep — a false quarantine is worse than a missed one, and it's recoverable either way). The worker stamps `relevance=quarantined` + the verdict fields via `updateEntryWithMetadata` (the same mutator proposals use).
- **Cadence & idempotence:** cursor-based, so each entry is classified once; the `slop_pass` cursor advances only when the batch is written, so overlapping ticks can't double-classify (the run-lock in `runAmbientAgentOnce` `agent_runner.go:148` already guarantees this).

### 4.4 Safety rules — what the classifier may NEVER touch

Stated as hard rules enforced *before* the model sees a candidate (a deny-list in the batch builder, not a prompt instruction the model could ignore):

1. **Never quarantine `decision`** — decisions are knowledge that grounds Scout (`memory.go:34-39`). Excluded from candidacy.
2. **Never quarantine `archive`** — meeting archives are the permanent record. Excluded.
3. **Never quarantine `package`** or any UI-state kind — they're already out of recall; quarantining them is meaningless and risks binder corruption.
4. **Never quarantine a published or package-attached `os_artifact`** — if `metadata["published"]=="true"` or it carries a `packageId`, it's load-bearing. Excluded.
5. **Only `transcript` and unpublished/unattached `os_artifact`** are ever candidates. This is the "snowballing slop" the thesis targets (chatter transcripts, dead-end drafts), nothing curated.

### 4.5 Restore path, expiry job, audit trail

- **Restore:** a settings surface lists quarantined entries (a new `/assistant/quarantine` GET returning entries where `relevance=quarantined`, newest first). Restore POSTs an id → `relevance=active`, `reviewedBy=<email>`, `quarantinedAt/expiresAt` cleared. Same origin/session guards as the proposal action handler (`codex_proposals.go:268`).
- **30-day expiry:** a second ambient pass (or a daily `CronCreate`-style tick) hard-deletes entries where `relevance=quarantined && now > expiresAt`. Deletion appends a `slop_expiry` audit entry (kind `slop_pass`) recording the deleted id + reason, so the *fact* of deletion survives even though the content doesn't. **Never a silent hard delete** — 30 visible days + an audit stub satisfies the user's "quarantine-then-auto-expire, reviewable, never silent" policy.
- **Audit trail:** every transition (quarantine, restore, expire) is stamped with actor + timestamp on the entry (or the audit stub for expiry). The JSONL append-only log is itself the audit history.

---

## 5. Recall quality — is text-match enough?

Current recall is substring + token-overlap scoring (`memory.go:712-763`): `+10` for a full-query substring hit, `+3` per query token present, newest-first tiebreak. It feeds a model call that synthesizes the answer (`memory_query.go:185`). For six users and a memory store measured in thousands of entries, **text-match is sufficient for v1 of "she can recall anything," and embeddings are a justified deferral — with two cheap upgrades now.**

Reasoning:

- The recall answer is *model-synthesized*, not raw-ranked. Text-match's job is only to pull a candidate window into context; the model does the reasoning. So recall quality is bounded more by *candidate coverage* than by ranking precision. The failure mode of pure substring match is **vocabulary mismatch** — the user asks "runway" and the entry says "cash-out date." That is real but narrow at this scale.
- The corpus is small and English, single-domain, with a **domain-vocabulary canonicalizer already in place** (`canonicalizeDomainTerms`, applied in both `search` and `linkageMatchTokens`). Canonicalization is a poor-man's synonym layer and already handles the highest-value mismatches (brand/acronym spellings).

Two upgrades now, embeddings later:

1. **Query expansion (cheap, now):** before search, one small model call (or a static synonym map maintained by the Domain Strategist) expands the query with 2–3 synonyms, OR'd into the token match. This closes most vocabulary-mismatch gaps at ~0 infra cost and no new storage. Recommend this for the wave.
2. **Embeddings (deferred, designed):** if recall misses persist, the smallest credible design is: compute an embedding per durable entry at append time (kinds `transcript/brain/archive/decision/os_artifact` only), store the vector inline in the JSONL entry as `metadata["embedding"]` (base64 float16, ~1–3KB) — **no new datastore, no index server.** Recall becomes hybrid: text-match candidates ∪ top-K cosine over an in-memory vector slice loaded at boot (6 users × thousands of entries = a few MB of floats, trivially in RAM). Rank = `w1*textScore + w2*cosine`. Local embeddings (a small ONNX model in-process) avoid a per-entry API cost but add a CGO/runtime dependency; **API embeddings** (one call per new durable entry, batched by the ambient worker) are simpler and fit the existing "ambient worker calls a model" pattern. Recommend API embeddings *if and when* triggered, gated behind `BONFIRE_RECALL_EMBEDDINGS=on`, defaulting off.

**Verdict:** ship query expansion this wave; design embeddings as a documented, env-gated follow-up. Do not build a vector DB — it violates the "boring tech, one VPS" principle for a corpus this size.

---

## 6. Risk register (top 10)

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| 1 | **In-process Anthropic tool-loop latency/cost** balloons on multi-turn goals (many tool round-trips). | Med | Med | Hard `maxTurns` cap; concurrency cap 2; stream progress so slow loops are visible not hung; `MaxRuntime` per runner; cheap models for review/gate/verify sub-calls. |
| 2 | **JSONL store unbounded growth** (transcripts + now goal plans + embeddings) degrades boot load & search. | High | Med | Slop expiry (§4) hard-deletes after 30d; goal plans are one artifact each (bounded); embeddings gated off by default; if boot slows, add a compaction pass that rolls archived meetings into a cold file. |
| 3 | **Realtime session token limits / instruction bloat** as the private allowlist grows to ~24 tools + board JSON + vocab in the system prompt. | Med | Med | Keep tool *schemas* lean; move board context to on-demand `answer_memory_question` instead of always-in-prompt; measure session.update payload size (grill already caps dictated text `grill.go:21`). |
| 4 | **index.html merge conflicts** — one 34.9k-line file, parallel waves all editing it (private grill client swap, quick-select menu, tab rename). | High | Med | Sequence index.html edits into one wave lane; assign disjoint line regions per the context map; land the Go/schema work first so frontend integrates against a stable contract. |
| 5 | **VPS memory exhaustion** (4GB) — orchestrator goroutines + codex sidecar + coturn + Pion room + embeddings in RAM. | Med | High | Concurrency cap 2 subtasks; sidecar is `profiles:["codex"]` (opt-in, not always-on); embeddings off by default; reconciler is single-pass not a hot loop. Watch RSS; the sidecar is the biggest tenant. |
| 6 | **External-write gate bypass** via a new code path (goal engine, private voice `initiate_goal`, or a runner that self-approves). | Low | Critical | `external_write` is *only* set by the authority classifier, *only* cleared by admin approval; `initiate_goal` cannot request it; commit runs only through the sidecar job. Regression test asserts no path launches external_write without an approval record. |
| 7 | **Orphaned goal states** after crash mid-`execute_in_order`. | Med | High | Boot reconciler (§2.5) resumes any non-terminal `mode=goal` artifact from its persisted plan; idempotence keys (`deliveredAt`, `changed`, `runnerJobId`) make re-dispatch safe. |
| 8 | **Classifier false-positive quarantines** hide real knowledge from Scout. | Med | Med | Confidence floor 0.8; bias-to-keep on `uncertain`; hard deny-list protects decisions/archives/published/attached (§4.4); 30-day visible restore window; audit trail. |
| 9 | **Provider drift** — Anthropic/OpenAI/codex API shape or model-id change breaks a runner silently. | Med | Med | One thin client per provider behind `AgentRunner`; status-only error surfacing (`apiRequestFailedError`); a smoke test per provider that asserts a trivial tool-loop completes; keyless-local stub keeps the shell up if all providers fail. |
| 10 | **Disclosure failure** — `start_chat_as_user` posts as a human without the "via Scout" stamp. | Low | High | Disclosure is stamped **server-side** unconditionally, not from the model's `disclose` arg; test asserts every on-behalf message record carries `postedOnBehalfOf`. |

---

## 7. Test strategy

`go test ./...` is mandatory pre-deploy and the VPS has no Go, so **everything is asserted from Go tests** (including frontend markers, per `frontend_latency_test.go` / `assistant_http_test.go` precedent). Per subsystem:

**Agent-runner abstraction**
- Unit: `selectAgentRunner` returns the right provider for each `BONFIRE_AGENT_RUNNER` value incl. back-compat aliases and keyless fallback to the stub.
- Unit: a fake `AgentRunner` drains a scripted `AgentProgress` channel and the engine writes the expected artifact metadata (progressPercent/currentStage/goalStatus) at each step — no network.
- Contract: `anthropicFableRunner` tool-loop against a mock Messages endpoint (mirror `openAITextResponder` injection at `openai_responses.go:55`) asserting tool_use → `applyToolCallArgs` → tool_result round-trips.

**/goal engine**
- Table test over the state machine: every transition incl. review-fail retry (bounded to 2), gate → approval_required for external_write, verify-fail → needs_attention.
- Plan schema validate: malformed/oversized (>6 subtasks) plans rejected.
- **Resumability test:** build a `mode=goal` artifact with an in-flight plan, run the reconciler, assert it resumes from the right state and re-dispatch is idempotent (no duplicate child artifacts).
- **Gate test (safety-critical):** assert no code path launches an `external_write` job without a prior approval record — the risk-6 regression.

**Realtime-2 parity**
- Unit: `privateRealtimeVoiceTools()` includes the new set and excludes the room-only set (`set_recording`, `archive_meeting`, `start_grill_session`).
- Dispatch: `applyPrivateRealtimeVoiceTool` routes each new tool; `start_chat_as_user` stamps `postedOnBehalfOf` server-side regardless of args (risk-10 regression).
- Feasibility marker: a frontend-marker test asserts index.html contains the private-grill `session.update` client handler (the client-driven swap must exist, since the server can't push it).

**Slop quarantine**
- Unit: `store.search` excludes `relevance=quarantined|expired`; restore re-includes.
- Deny-list: classifier candidate builder never yields `decision`/`archive`/`package`/published/attached entries.
- Expiry: an entry past `expiresAt` is deleted and leaves a `slop_pass` audit stub; one not yet expired survives.
- Idempotence: two classifier passes over the same window don't double-quarantine (cursor + run-lock).

**Recall**
- Query-expansion unit: a synonym-expanded query matches an entry the raw query misses; canonicalization still applied.
- (If embeddings land) hybrid-rank unit with a fixed tiny vector set; and a guard that `BONFIRE_RECALL_EMBEDDINGS=off` is a pure text path.

**HTTP**
- `/assistant/quarantine` GET/POST behind origin+session guards; `/goal` initiation endpoint launches a `mode=goal` thread as the requester and cannot set `external_write`.

---

## Summary — five key architecture calls

1. **One `AgentRunner` interface at the `produceAgentThreadArtifactWithWorker` seam**, async/channel-based, selected purely by env — Fable-5 in-process tool-loop is the default *orchestrator*; codex (or, later, claude) sidecar is the swappable *executor*. Model-agnostic by env var, not refactor.
2. **Two-tier split: orchestrate in-process, execute in the sidecar.** Reasoning + Bonfire tools stay in Go (reusing `applyToolCallArgs`); shell/repo/commit stays in the sandboxed, admin-gated sidecar. `ANTHROPIC_API_KEY` lives in one process; the external-write gate is never bypassed.
3. **The `/goal` loop is a persisted state machine on the `os_artifact` record** with a JSON `goalPlan` (≤6 subtasks, deps, per-subtask review), model-call review/gate/verify steps, and a **boot reconciler** that makes every non-terminal goal resumable — no orphaned states.
4. **Realtime-2 parity grows the private allowlist to ~24 tools + 5 new ones**; private grill is **client-driven `session.update`** (the browser owns the private data channel, the server doesn't), and posting-as-user carries a mandatory server-stamped disclosure.
5. **Slop quarantine is a memory-entry lifecycle** (`relevance` field + one search-exclusion guard + an ambient classifier with a hard deny-list), 30-day visible expiry with an audit stub, never a silent delete. **Recall stays text-match + query expansion this wave; embeddings are an env-gated, no-new-datastore follow-up.**
