# Realtime Agent Workforce - Strategic Design Context

## Mission

Make Realtime 2 / Scout the primary control surface for Bonfire OS work while keeping durable company output as artifacts.

## Current System

Realtime tools are declared in `kanban.go` and include `control_app`, `create_artifact`, `launch_agent_thread`, `update_artifact`, and `publish_artifact`. Private Scout chat is a per-websocket FIFO session in `scout_chat.go`. Work threads are launched through `/assistant/threads` or the Realtime `launch_agent_thread` tool, then stored as `os_artifact` entries in meeting memory. The Chat, Office, agent app, and Artifacts views in `index.html` render those artifacts.

## Design Requirements

Chat should discuss, recall, and relay answers without creating needless artifacts. Longer research, design, grill, workflow, or multi-agent requests should become artifact-backed work threads with progress and a shareable result. Realtime must not claim external Codex/browser/SSH execution unless worker evidence exists.

## Perspectives Needed

- Product: choose when chat stays conversational versus when work becomes an artifact.
- Architecture: keep the existing memory/artifact source of truth and avoid a separate thread store.
- UX: show status in Chat without adding a heavy new frontend stack.
- Quality: verify Realtime tool contracts, chat behavior, artifact metadata, and frontend markers.

## Deliverable

A committed proof of concept where Realtime 2 and private Scout chat can start research/design/grill/workflow threads, show staged progress, update artifacts asynchronously, and expose resulting artifacts for sharing.
