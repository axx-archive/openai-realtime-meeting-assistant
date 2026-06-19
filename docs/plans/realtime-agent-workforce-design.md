# Realtime Agent Workforce - Unified Design

## Executive Summary

Scout uses Chat for discussion and uses artifacts for durable company work. A normal private chat message stays in the per-user chat session; a work-shaped request launches an agent thread that immediately creates a running artifact. Realtime 2 gets the same behavior through the existing `launch_agent_thread` function tool, so voice and text share one source of truth.

## Architecture

The source of truth remains `os_artifact` memory entries. `scout_chat.go` classifies research/design/grill/workflow requests before the normal answer queue and calls `launchAgentThread`. `agent_thread_runner.go` stores lifecycle metadata such as `agentLoop`, `workflowStages`, `goalStatus`, `reviewGate`, and `progressPercent`. `index.html` renders real work threads from artifact metadata and syncs running progress from `/artifacts`.

## Detailed Design

Opening Chat focuses the active private Scout conversation. Starting a new chat thread resets the websocket chat session and clears browser-visible history. Longer asks launch work threads with a staged loop: goal, split, agents, execute, gate, artifact. The progress card opens the underlying artifact, where the user can edit, copy, publish, or share it through the existing Artifacts app.

## Migration And Rollout

No schema migration is required because metadata is additive on `os_artifact` entries. Existing artifacts still render. If a worker fails, the artifact moves to `threadStatus=error`, `reviewGate=blocked`, and Chat shows an inspectable terminal state.

## Testing And Acceptance

Acceptance requires `go test ./...`, static frontend markers for chat reset and progress visualizer, Realtime tool-schema tests for workforce wording, and HTTP/chat tests for running thread artifacts. Live deployment must push `main`, sync the committed tree to the VPS, rebuild Compose, and verify `thebonfire.xyz` plus container health.

## Open Questions

Persistent named chat threads are still a future product decision. The current proof of concept keeps chat private and ephemeral while making real work threads durable through artifacts.
