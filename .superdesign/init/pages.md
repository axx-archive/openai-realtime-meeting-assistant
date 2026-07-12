# Pages

## `/` BonfireOS

Entry and complete client source: `index.html`.

The authenticated shell switches first-class tools without a client router:

- `office` — Scout waveform home and operational context.
- `room` — WebRTC lobby, green room, active meeting, screen share, and room chat.
- `chat` — private Scout threads and channels.
- `artifacts` / `research` / `design` / `grill` — work products and agent workflows.
- `board` — meeting Kanban view.
- `memory` — recalled meeting context.
- `files` — file surface.

## Complete UI dependency tree

- `index.html` — current tokens, CSS, inline SVG icons, DOM, responsive branches, and client/WebRTC logic.
- `main.go` — template serving, auth/session routes, WebSocket signaling, and HTTP handlers.
- `kanban.go` — room/board state, Scout tools, recording, memory, and archive events.
- `participants.go` — seeded participant identities and room constraints.
- `frontend_latency_test.go` — source-level frontend behavior contracts.
- `scripts/live-media-smoke.mjs` — end-to-end media and geometry verification.

For design work on the room, always read the actual `index.html` mobile branch and do not infer the current layout from older Hearth/Bonfire naming.
