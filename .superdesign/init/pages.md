# Pages

## `/` Bonfire Room

Entry: `index.html`

Dependencies:

- `index.html`
  - Inline CSS tokens and components.
  - Inline SVG brand marks and control icons.
  - Inline browser/WebRTC client logic.
- `main.go`
  - Serves `index.html`, WebSocket signaling, participant snapshots, archives.
- `kanban.go`
  - Board model, Realtime assistant, memory and archive broadcasts.
- `participants.go`
  - Participant names, password, room capacity, email mapping.
- `meeting_notes.go`
  - Archive and email notes content.
- `memory.go`
  - Meeting memory store and recall search.
- `design-system/README.md`
  - Brand, copy, layout, motion, and visual rules.

There are no nested client imports because the app is intentionally framework-free.
