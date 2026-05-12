# Agentic Memory - Strategic Design Context

## Mission
Turn the demo from a voice-operated Kanban room into a work assistant with durable meeting memory. The first milestone is automatic transcript capture, visible meeting memory, and voice-triggered recall.

## Current System
The app is a Go WebRTC server with a browser UI. Browser peers send audio/video through Pion, the server mixes audio, and an OpenAI Realtime peer operates the Kanban board through tool calls. The Realtime session already enables transcription, but `conversation.item.input_audio_transcription.completed` events were ignored. Board state is in memory, and the DigitalOcean deployment runs the app behind Caddy with Docker Compose.

## Design Requirements
Memory must survive container restarts, require no new managed service for the first iteration, and be immediately useful during a live meeting. The UI should show fresh memory without distracting from the board. Voice recall should work through the existing assistant channel. The design must keep the current Kanban workflow compatible.

## Perspectives Needed
Product lead: make recall feel like a real work companion rather than a transcript dump. Technical architect: add durable storage and recall without destabilizing WebRTC. SRE: preserve the simple VPS deployment and make persistence explicit. Quality analyst: verify transcript ingestion, recall, restart durability, and public-room behavior.

## Deliverable
A deployed foundation for persistent meeting memory: transcript persistence, visible memory feed, and a Realtime tool that answers recall questions from saved memory.
