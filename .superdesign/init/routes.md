# Routes

BonfireOS uses one rendered client page and stateful tool switching rather than file-based client routes.

| URL | Source | UI role |
| --- | --- | --- |
| `/` | `main.go` + `index.html` | Full authenticated BonfireOS shell, lobby, and room. |
| `/websocket` | `main.go` | Signaling, participant/media state, room events, board and Scout broadcasts. |
| `/participants` | `main.go` | Authenticated room snapshot used by lobby and smoke verification. |
| `/healthz` / `/readyz` | `main.go` | Liveness and production readiness. |
| `/archives/{id}.json` | meeting/archive handlers | Meeting archive download. |

Within `/`, `data-tool` selects Office, Room, Chat, Artifacts, Board, Memory, Files, Research, Design, and Grill surfaces. Designs must preserve these tool keys even if labels change.
