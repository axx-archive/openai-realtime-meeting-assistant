# The Bonfire — Meeting Room UI kit

Pixel-faithful recreation of `meetingassist/index.html`. The room is the only product surface The Bonfire has, so this is the whole UI kit.

## What's here

| File | What it is |
| --- | --- |
| `index.html` | Demo app. Loads the components and walks through: locked → access verified → in the room → speaking → card moved to Done (with sparks) → meeting archived. |
| `styles.css` | The room's full stylesheet, lifted from production. Cleaned up only where the demo needs different state names. |
| `identicon.js` | The warm-clamped 5×5 mirrored identicon algorithm. Plain JS. |
| `BrandMark.jsx` | The logo with the three halos. Accepts `listening` and `hotEmber` props. |
| `StatusPill.jsx` | Pill in five states (`idle`, `connecting`, `room`, `listening`, `offline`). |
| `Topbar.jsx` | Brand mark + title + status pill. |
| `AccessPanel.jsx` | Participant + password form. Verifies inline. |
| `VideoStack.jsx` | Local video tile + remote tiles with name labels. |
| `MemoryPanel.jsx` | Meeting memory list with transcript/answer/archive items. |
| `AssistantPanel.jsx` | Feed of assistant messages + ask-memory form. |
| `Board.jsx` | Four-column parchment kanban with cards, tags, owner identicons, empty states, hover lift, "moved" wiggle. |
| `CardDetail.jsx` | The modal for editing a card. |
| `MeetingBar.jsx` | Sticky footer with primary actions. |
| `Toast.jsx` | Toast tray (bottom-right). |
| `App.jsx` | Wires it all together and scripts the demo timeline. |

## How to run

Install the local package dependencies and run the Vite preview app:

```bash
npm install
npm run dev
```

The kit now uses the same React 18 runtime through package-managed ES modules, which keeps the files analyzable by React tooling while preserving the self-contained design-system surface.

## What it cuts

- **Real WebRTC.** No actual peers. Remote tiles are CSS placeholders with simulated speaking states.
- **Real OpenAI Realtime.** The assistant is scripted; clicking through the demo fires pre-canned events on a timer.
- **Real auth.** Any password works.
- **Real meeting notes.** The archive flow just resolves with a fake `downloadUrl`.

What it doesn't cut: every visual surface, the mount stagger, the pulse halos, the hot-ember handshake on recognition, sparks-on-done, card-move wiggle, hover lifts with their 60ms shadow warm-up.
