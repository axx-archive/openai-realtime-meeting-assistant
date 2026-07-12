# Components

BonfireOS is framework-free. Reusable components are CSS classes and DOM factory functions inside `index.html`.

## Current shell primitives

- `tool-rail`, `tool-rail__tool`, `tool-rail__live`, `topbar-live` — global navigation and live-room continuity.
- `btn`, `btn--primary`, `btn--ghost`, `btn--danger`, `pressable` — action system with explicit states and press feedback.
- `pill`, `status-pill`, `toast`, `glass-menu`, `settings-sheet` — neutral Glass & Ink feedback and overlays.
- `field`, `device-control`, `device-select`, `assistant-input` — form controls.
- `run-card`, `artifact-card`, `chat-thread-item`, `memory-item`, `column` — OS content primitives.

## Room primitives

- `presentation-tile`, `hearth-stage`, `hearth-center`, `hearth-seats` — video-stage layout.
- `video-tile hearth-seat`, `is-mobile-hero`, `is-active-speaker`, `is-on-stage` — canonical participant tile and state classes.
- `tile-avatar`, `video-label`, `media-flags`, `pin-speaker` — participant overlays.
- `screen-stage`, `screen-stage__pip` — screen-share layout.
- `meeting-bar`, `controls`, `room-chat-toggle`, `btn--recording` — contextual call controls.

## DOM factories

Key factories include `renderOwnerAvatar`, `renderIdenticon`, `renderAvatarStack`, `renderMediaFlags`, `renderAssistantMessage`, `renderMemoryEntry`, and the board/card render helpers. The implementation source of truth is `index.html`.
