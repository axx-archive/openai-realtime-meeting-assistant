# BonfireOS Glass & Ink Design System

BonfireOS is a light-first agentic operating system with a live WebRTC room as one first-class tool. The deployed `index.html` is the visual source of truth. Do not revive the retired warm-dark Bonfire room, parchment board, fireplace/hearth ornament, serif display type, or ember-heavy styling.

## Product and shell

- Product name: `BonfireOS`. The live meeting surface is `the room`.
- The authenticated app is a single workspace with Office, Room, Chat, Intelligence, Board, Memory, and Files surfaces.
- Desktop uses a flat 60px left tool rail. Mobile uses a safe-area-aware floating bottom glass navigation island.
- The default theme is light. Dark mode is an explicit user choice via `data-theme="dark"`.
- The room video stage is always true black in either theme.

## Visual language

- UI font: `Google Sans Flex`, then Apple/SF and Segoe system fallbacks.
- System labels and changing numbers: `Geist Mono` with tabular numerals.
- No serif display font. Empty-state poetry uses the UI sans in italic.
- Light canvas: `#F5F5F7`; primary surface: `#FFFFFF`; primary ink: `#0E0E10`.
- Dark canvas: `#000000`; dark surfaces use neutral ink values rather than warm brown.
- Glass chrome uses translucent neutral surfaces, a 28px blur, a subtle highlight, and a soft neutral shadow.
- Speaking/live state alone earns signal green (`#30D158`).
- Ember/coral (`#FF6B4A`) is reserved for agent work or ignition, never ambient decoration.
- Red is destructive/leave only. Blue, amber, and other hues are semantic state only.
- Generous radii, quiet hairlines, and layered glass replace heavy borders and decorative shadows.

## Interaction rules

- Every touch target is at least 44x44px with honest labels and ARIA state.
- Press feedback uses `scale(0.96)` unless a platform-specific control already uses the canonical press token.
- Use named transitions only; never `transition: all`.
- Respect reduced motion. Avoid mount transforms on fixed bottom chrome because they can override centering transforms.
- Focus must remain visible over light surfaces, dark video, and glass.

## Room and media invariants

- Participant videos are canonical elements inside `#videoStack`; never clone, reparent, or repeatedly replace their streams for layout changes.
- Mobile is speaker-first: one dominant hero, with pinned participant first and otherwise the genuinely audible active speaker. The previous hero stays stable through short pauses.
- Active-speaker green requires audible liveness; roster fallback must never light the speaking ring.
- Other participants remain reachable as a canonical filmstrip, including 7-person rooms.
- Screen sharing takes layout priority, then an explicit pin, then the normal grid.
- Global tools and essential call actions must remain reachable without two visually competing full-width control rows.
- Preserve camera/microphone state, local mirroring, attachment revisions, and media continuity through layout and tool changes.

## Primary source

- `index.html` contains the current tokens, CSS, DOM, and responsive branches.
- `frontend_latency_test.go` pins important UI and media contracts.
- `scripts/live-media-smoke.mjs` is the high-signal live geometry/media harness.
