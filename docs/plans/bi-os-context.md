# BI OS - Strategic Design Context

## Mission

Build The Bonfire into a business intelligence operating system. Signed-in users should land in an office dashboard, not directly inside a video room, with apps for the live room, assistant chat, memory, artifacts, research, design work, and pitch review. The assistant should feel like a persistent operating layer, while meeting rooms keep a clear shared recording/transcript contract.

## Current System

The app is a Go server with a framework-free `index.html` client. Authentication is cookie-based through `/auth/*`; the current UI only marks the shell as authenticated after joining the room. The frontend already has a left tool rail and client-side tool routing for `room`, `chat`, `board`, and `memory`, but the signed-in landing state is still the login gate.

The room has a durable realtime foundation:

- WebRTC room transport and media fan-out live in `main.go`.
- The assistant room peer, board tools, transcript lane, archive generation, and ambient workers live around `kanban.go`, `transcription_lane.go`, `brain_worker.go`, `board_worker.go`, and `agent_runner.go`.
- Private Scout chat exists in `scout_chat.go`, but its websocket path currently requires room entry.
- The memory and answer engine in `memory_query.go` can answer from the board and durable meeting memory without broadcasting.

## Design Requirements

- Login lands in a core OS dashboard.
- The existing video room remains a first-class app inside the OS.
- A liquid-glass floating assistant can be opened from anywhere in the authenticated OS.
- The assistant has mode affordances for chat, artifacts, research, design studio, and grill mode.
- Personal assistant behavior must not silently listen to a shared meeting.
- Meeting transcription and recording state must be shared and visible in the room.
- The first implementation should preserve the no-build, single-file frontend unless a later wave justifies a framework.
- Existing media reliability and smoke coverage remain non-negotiable.

## Perspectives Needed

- Product lead: make the dashboard feel like an operating system rather than a meeting demo.
- Technical architect: split OS auth state from room state and reuse the current answer/memory engine.
- UX designer: keep the Bonfire design system while making the assistant feel liquid, alive, and high-end.
- Privacy and trust reviewer: distinguish personal assistant capture from shared room capture.
- Quality analyst: require tests around auth boundaries and room regressions.

## Deliverable

A unified BI OS design and a first implementation slice: authenticated dashboard, floating assistant shell, authenticated assistant query endpoint, and an explicit architecture for shared room transcript/artifact generation.
