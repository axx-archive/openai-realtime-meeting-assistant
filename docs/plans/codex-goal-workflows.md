# Codex Goal Workflows

## Objective

Turn Bonfire OS requests into reusable, goal-driven workflows that can run across research, design, artifact generation, app control, and eventually code or deployment tasks.

The loop is:

1. Identify and set goal.
2. Decompose the work.
3. Assign the right agent.
4. Coordinate dependencies.
5. Execute in order.
6. Review against the original goal.
7. Gate before shipping.
8. Save what worked.
9. Report only what matters.
10. Verify goal as completed.

## Product Boundary

`gpt-realtime-2` is the vocal control layer. It should stay fast and predictable:

- open Bonfire OS surfaces with `control_app`
- answer questions from board state, memory, meetings, archives, and artifacts
- create local artifacts and goal workflow scaffolds
- queue or prepare a Codex handoff when a task needs longer execution

Codex workers are the execution layer for slower or side-effecting work:

- repo and web research
- design analysis that needs screenshots, source files, or docs
- browser or desktop app control
- shell commands, tests, diffs, PR-style implementation, deploy checks, and SSH tasks
- long-running, resumable, or approval-gated goals

Realtime must not directly run shell commands, SSH, browser automation, filesystem edits, or long web research. Codex workers should not passively listen to room audio or mutate the live board directly; they should write status and output back through artifacts or reviewed app actions.

## First Slice

Bonfire stores goal workflows as `os_artifact` entries with `mode=workflow` and workflow metadata. This keeps the first version simple:

- `/assistant/query` accepts `mode=workflow`
- Realtime `create_artifact` accepts `mode=workflow`
- saved artifacts include the full goal loop scaffold
- the Artifacts app remains the durable source of truth
- external Codex execution is marked as not connected until a worker exists

Research, design, grill, and artifact modes also include the same goal workflow section so the pattern is reusable across app workspaces.

## Codex Runner Path

The safest near-term runner is a server-side Codex SDK or `codex exec` worker beside the existing ambient agents. The worker can claim a saved workflow artifact, run with explicit sandbox/profile settings, then append status, evidence, final output, tests, screenshots, or errors back to memory.

Use Codex app-server only if Bonfire needs a rich embedded Codex client with auth, history, approvals, and streamed events. For automation jobs, prefer the SDK or noninteractive CLI.

An SSH-connected dedicated computer is useful for workflows that need real browser state, desktop apps, credentials, or local device automation. It should be a worker host, not a public app-server endpoint.
