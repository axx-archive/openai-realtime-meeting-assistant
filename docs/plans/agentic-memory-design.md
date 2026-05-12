# Agentic Memory - Unified Design

## Executive Summary
The meeting assistant should become a durable work memory that captures what happened, answers recall questions, and turns commitments into structured action. The first architectural bet is to persist Realtime transcript events as server-owned memory, then expose that memory through both UI and voice tools. This keeps the live meeting fast while creating a foundation for summaries, decisions, people, follow-ups, and cross-meeting recall.

## Architecture
Realtime transcription events flow into a `meetingMemoryStore` that appends JSONL records to `MEETING_MEMORY_PATH`. Browser clients receive a recent memory snapshot on join and live transcript/answer events over the existing Kanban WebSocket channel. The Realtime assistant gets a new `answer_memory_question` tool; when a user asks what was said or decided, the model calls the tool, the server searches local memory, and the UI receives a memory answer.

## Detailed Design
Memory entries have `id`, `kind`, `text`, `createdAt`, and optional metadata. The first kind is `transcript`; answers are transient UI entries. Search is intentionally simple lexical ranking for this milestone, avoiding a second model call and keeping latency predictable. Docker Compose mounts `/app/data` as `meeting_data`, so memory survives rebuilds and container replacement.

Future extensions should add meeting/session IDs, speaker labels when available, semantic summaries, decisions, follow-ups, authenticated access, and export/share controls. Once those exist, the assistant can answer higher-order questions such as "what did we decide last week?" or "what changed since the last standup?"

## Migration And Rollout
Existing rooms continue to work. New transcript memory starts after deployment; old meetings are not backfilled. The app falls back to an empty memory file if no prior memory exists. Rollback is removing the memory volume mount and the new Realtime tool, with no change to board operation.

## Testing And Acceptance
Acceptance criteria: Go tests pass; the browser script parses; public HTTPS serves the new memory panel; public WSS returns board, memory, and status events; speech creates durable transcript entries; a recall question triggers an answer in the UI. Restarting the meeting container must preserve `/app/data/meeting-memory.jsonl`.

## Open Questions
The next product decision is identity and privacy: whether memory is per room, per team, or per authenticated user. That choice should happen before cross-meeting recall ships.
