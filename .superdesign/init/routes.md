# Routes

This app does not use file-based routing or a client router.

| URL | Handler | UI |
| --- | --- | --- |
| `/` | `main.go` root handler | Renders `index.html` through `text/template` with the WebSocket URL. |
| `/websocket` | `websocketHandler` in `main.go` | Signaling, room state, board edits, assistant events. |
| `/participants` | `participantsHandler` in `main.go` | JSON room snapshot for waiting room preview. |
| `/archives/{id}.json` | `meetingArchiveHandler` in `main.go` | Meeting archive download. |

Key page: `/` is the Bonfire room.
