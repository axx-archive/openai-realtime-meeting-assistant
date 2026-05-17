# Codebase Audit - May 17, 2026

## Scope

Audited the full repository: Go server, WebRTC signaling and media forwarding, OpenAI Realtime integration, Kanban board tools, persistence, meeting notes email delivery, single-file browser client, Docker deployment, and local design-system assets.

## Verification

- `go test ./...`
- `go vet ./...`
- `go test -race ./...`

## Refactors Completed

- Removed the unused HTML template execution path in `main.go`, which also removed a captured request-handler `err` assignment that could race under concurrent page requests.
- Made forwarded WebRTC track IDs collision-resistant by deriving local track IDs from stream ID, source track ID, and SSRC. Participant-track broadcasts now include both the forwarded ID and source track ID.
- Normalized initial and persisted Kanban cards with the same domain-term/tag rules used by live tool calls, so tags such as WebRTC, RTP, and NACK do not duplicate under different casing.
- Made idempotent board mutations return `changed=false` instead of touching `updatedAt`, rewriting JSON, and broadcasting redundant board updates.
- Required `add_tags` to receive at least one real tag after cleanup.
- Reused one atomic JSON writer for board state and meeting archives, including directory creation, temp-file writes, newline-terminated JSON, and rename replacement.
- Raised the meeting-memory scanner buffer to 1 MiB so large transcript/archive JSONL entries can be reloaded.
- Added a 10-second SMTP connection timeout for both STARTTLS and TLS-on-connect mail delivery.
- Switched room-passcode comparison to constant-time comparison.
- Added `Cache-Control: no-store` to participant snapshots and archive downloads.
- Ignored local `data/` in Git and Docker build contexts because it can contain meeting memory and archives.
- Updated the DigitalOcean example env and bandwidth guidance to match the current 10-seat room budget.

## Current System

The app is a compact Go 1.24 module, tested locally with Go 1.26.3. It runs one HTTP/WebSocket server, uses Pion WebRTC for browser media, mixes browser audio through Opus into an OpenAI Realtime peer, and lets the model call Kanban tools. The browser client is a single `index.html` file that owns the room UI, WebRTC offer/answer flow, manual board editing, meeting archive actions, and visual state.

OpenAI's current Realtime conversations documentation still names `gpt-realtime-2` for Realtime sessions, documents `session.update`, function calling, and states that Realtime sessions have a 60-minute maximum duration: https://developers.openai.com/api/docs/guides/realtime-conversations.

## Key Findings

1. `main.go` and `kanban.go` are carrying too many responsibilities. Split into focused packages or files for HTTP routing, WebRTC room signaling, Realtime peer lifecycle, board domain logic, persistence, memory, and archive/email delivery.
2. `index.html` is now large enough to slow safe iteration. Extract the client into modules or a small build step while preserving the current no-framework deploy path if demo simplicity matters.
3. The room gate is a shared passcode, not identity. For any non-demo deployment, add signed room sessions, participant identity, role checks for archive/delete actions, and audit logs.
4. Video forwarding scales roughly O(n squared). Ten video participants need about 110 Mbps egress before overhead; larger rooms need simulcast subscription controls or a more complete SFU strategy.
5. Persistence is durable but file-based. For production use, move board state, memory, participants, and archives into SQLite or another transactional store with migrations and backup/retention controls.
6. Meeting notes are deterministic and useful, but keyword-only decision extraction will miss nuance. A model-assisted summary pipeline could improve notes if privacy, cost, and review requirements are clear.
7. Observability is light. Add structured logs, connection/session metrics, archive/email outcomes, Realtime reconnect counters, and dashboardable room health.
8. Browser/WebRTC behavior lacks integration coverage. Add Playwright smoke tests for access gating, board editing, archive download, and at least a mocked signaling loop.
9. Deployment docs are good for a VPS demo, but production needs health checks, graceful shutdown, backup restore notes, TURN guidance, and secret rotation.

## Expansion Roadmap

Near term:
- Split server files along existing boundaries without changing behavior.
- Add HTTP handler tests for `/participants`, `/archives`, origin checks, and access-denied flows.
- Add browser smoke tests around joining, manual card edits, undo delete, and archive UI states.
- Add a graceful HTTP shutdown path and stop the keyframe ticker cleanly.

Medium term:
- Introduce SQLite-backed storage with event history for board changes, memory entries, participant joins/leaves, and archives.
- Add structured Realtime/session telemetry and reconnect backoff policy.
- Extract client JavaScript into tested modules while keeping the Bonfire design tokens intact.
- Add TURN configuration docs and runtime validation for NAT/UDP settings.

Longer term:
- Add real identity and per-room authorization.
- Add subscription/layer controls for video forwarding.
- Improve notes with reviewable AI summaries and explicit action-item ownership.
- Support multiple rooms with isolated state, archives, participants, and assistant sessions.
