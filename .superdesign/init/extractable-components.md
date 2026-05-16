# Extractable Components

## Topbar
- Source: `index.html`
- Category: layout
- Description: Bonfire brand mark, room title, and live connection status.
- Extractable props: statusLabel, statusKind
- Hardcoded: Brand SVG, Bonfire product name, agentic meeting room subtitle.

## HearthStage
- Source: `index.html`
- Category: layout
- Description: Active speaker stage, screen-share stage, participant seats.
- Extractable props: activeSpeakerName, isScreenSharing
- Hardcoded: Ember motion, video geometry, tile labels.

## BoardRail
- Source: `index.html`
- Category: layout
- Description: Compact standup board preview with expanded board entry.
- Extractable props: cardCount, isBoardReady
- Hardcoded: Kanban columns and Bonfire board copy.

## ScoutPanel
- Source: `index.html`
- Category: layout
- Description: Assistant query panel and memory count surface.
- Extractable props: memoryCount, assistantState
- Hardcoded: Scout naming and message styles.

## MeetingBar
- Source: `index.html`
- Category: layout
- Description: Sticky bottom media controls, share, notes, invite link, and room clock.
- Extractable props: isMuted, isCameraOff, isSharing, elapsedTime
- Hardcoded: Control order and icon SVGs.
