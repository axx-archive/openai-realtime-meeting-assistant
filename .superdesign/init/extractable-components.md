# Extractable Components

## ToolRail

- Source: `index.html`
- Category: layout
- Description: desktop 60px global rail and mobile safe-area bottom glass navigation island.
- Props: `activeTool`, `isInRoom`, `roomUnreadCount`.
- Hardcoded: current BonfireOS tool destinations and inline SVG icons.

## MobileRoomStage

- Source: `index.html`
- Category: layout
- Description: canonical active-speaker hero, participant filmstrip, pin/speaking states, and screen-share state.
- Props: `activeSpeakerName`, `pinnedSpeakerName`, `participantCount`, `isScreenSharing`.
- Hardcoded: Glass & Ink video treatment, signal-green audible ring, media-state overlays.

## MeetingBar

- Source: `index.html`
- Category: layout
- Description: contextual mic, camera, recording, room chat, invite, notes, and leave controls.
- Props: `isMuted`, `isCameraOff`, `isRecording`, `unreadCount`.
- Hardcoded: current control icons, order, ARIA labels, and destructive leave treatment.

## TopbarLivePill

- Source: `index.html`
- Category: navigation
- Description: mobile continuity control that returns to the live room from another tool without ending media.
- Props: `isInRoom`, `elapsedTime`.
- Hardcoded: live signal dot and room return behavior.

Skip generic primitives such as buttons, pills, inputs, and cards; keep them inline in drafts.
