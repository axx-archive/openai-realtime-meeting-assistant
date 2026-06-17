# BI OS - Unified Design

## Executive Summary

The Bonfire should become a business intelligence OS by separating "signed in to the office" from "inside the meeting room." The office dashboard becomes the default authenticated landing surface, and the room becomes one app among several. A persistent floating assistant runs at the OS layer for private work, while meetings use a distinct shared room agent with visible transcript/recording state and post-meeting artifact generation.

## Architecture

The first architecture bet is to keep the current single-page client and extend its existing `data-tool` routing. Add an `office` tool as the authenticated default, then keep `room`, `chat`, `board`, and `memory` as routed apps. New app tiles for artifacts, research, design studio, and grill mode can initially route to dashboard sections and assistant modes, then become full tools as their backend capabilities harden.

Authentication state becomes its own shell state:

- `is-authed`: user has a valid `/auth/me` identity.
- `is-in-room`: user has joined the media room and opened the room websocket.
- `data-tool`: current OS app.

The OS assistant should use an authenticated HTTP endpoint first, backed by the existing `resolveAssistantQueryContext` engine. That makes it immediately useful for board and memory questions without joining the room. Later, the floating assistant can negotiate a dedicated personal Realtime session using the same mode contract.

The meeting room should use a shared-room model, not many invisible personal assistants:

- The room has one app-specific shared realtime agent.
- Transcript/recording state is visible to everyone.
- Personal assistant voice capture is disabled while in-room by default.
- A user can still mute themselves to the room and ask their OS assistant privately, but that private exchange must be explicit and visually scoped.
- At archive time, the artifact agent consumes the room transcript, board state, and memory windows to generate summaries, decisions, action items, scorecards, and linked artifacts.

## Detailed Design

### Office Dashboard

The dashboard should show:

- A hero-grade operating status band: live room state, memory count, artifact readiness, and assistant availability.
- App launcher tiles for Room, Chat, Memory, Artifacts, Research, Design Studio, and Grill Mode.
- Insight strips that summarize "what needs attention," "what changed since last meeting," and "what the assistant can do next."
- Direct buttons for entering the room and opening the floating assistant.

### Floating Assistant

The floating assistant is an OS layer:

- Resting state: small liquid-glass button with a live waveform.
- Expanded state: waveform header, mode chips, answer pane, and prompt box.
- Modes: chat, artifacts, research, design, grill.
- First backend: `/assistant/query` with authenticated board/memory answer support.
- Future backend: dedicated personal Realtime session with tools for app navigation, artifact creation, search/research, design handoff, and grill-mode scoring.

### Meeting Room Recording And Transcripts

Use the shared room agent as the source of meeting truth. The first visible room control should be a shared "record/transcript" state, implemented server-side so everyone sees the same state. The transcript lane already captures room audio; the product layer should make that state explicit and controllable.

The best long-term room contract:

1. Shared transcript capture is on or off for the room.
2. Everyone sees the state.
3. The room agent consumes mixed room audio and transcript context.
4. Private personal assistants do not passively listen during the room.
5. Post-meeting artifact generation runs from the room transcript, board, and memory entries.

### Apps

- Room: existing video room, board, shared transcript/recording state, send notes.
- Chat: private assistant thread across meetings.
- Memory: durable transcript, brain, board update, and archive log.
- Artifacts: generated summaries, decisions, action plans, scorecards, research briefs.
- Research: assistant-driven research workspace with citations and saved briefs.
- Design Studio: kickoff surface for design/research prompts and handoff artifacts.
- Grill Mode: pitch/practice room that listens, asks tough questions, scores delivery, and writes a final evaluation artifact.

## Migration And Rollout

Wave 1: Split auth shell from room state, add office dashboard, add floating assistant UI, add authenticated `/assistant/query`.

Wave 2: Add persistent artifact records and an Artifacts app backed by archive outputs.

Wave 3: Add shared room transcript/recording state over websocket and persist it with archives.

Wave 4: Replace text-only OS assistant with personal Realtime voice session and explicit tool permissions.

Wave 5: Add research, design studio, and grill mode agents with saved artifacts and scoring rubrics.

## Testing And Acceptance

- `go test ./...` passes.
- Unauthenticated `/assistant/query` returns `401`.
- Authenticated `/assistant/query` answers from current board/memory without entering the room.
- Signing in lands on the office dashboard.
- Entering the room still starts the current media flow.
- Leaving the room returns to the office dashboard, not the login gate.
- Existing media smoke harness remains the source of truth for multi-participant room regressions.

## Open Questions

- Whether transcript capture defaults on for every room or requires a first-user explicit toggle.
- Which artifact storage model becomes the long-term source of truth: memory entries only, separate artifact records, or both.
- Whether grill mode should run inside the meeting room app, as a solo practice app, or both.
