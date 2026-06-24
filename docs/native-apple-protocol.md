# Native Apple Room Protocol

MeetingAssist native Apple clients join the existing browser room as peers on
the Go/Pion SFU. They do not use a WebView for media and they do not create a
parallel room system.

## Discovery

Unauthenticated clients may read `GET /native/config` to render the roster and
discover stable endpoint paths:

- `protocolVersion`: `native-room-v1`
- `auth.mode`: `cookie`
- `auth.loginPath`: `/auth/login`
- `auth.mePath`: `/auth/me`
- `auth.logoutPath`: `/auth/logout`
- `room.clientConfigPath`: `/client-config`
- `room.websocketPath`: `/websocket`
- `room.participants`: canonical roster names and emails
- `room.maxParticipants`: current room capacity

Authenticated clients read `GET /client-config` before joining media. The
existing browser field `rtcConfiguration` remains unchanged; native metadata is
additive:

- `protocolVersion`: `native-room-v1`
- `auth`: `cookie`
- `websocketPath`: `/websocket`
- `signalingRole`: `server-offer`
- `supportedLayers`: `low`, `medium`, `high`
- `nativeHints`: stable event names and media codecs

Native apps must not embed server secrets. OpenAI, TURN shared-secret, Resend,
runner-token, and SMTP credentials stay on the VPS/server; the client only
consumes server-issued public discovery/config responses and cookie-backed
session state.

## Authentication

Native clients call `POST /auth/login` with the roster `name` and password, then
retain the `bonfire_session` cookie in the shared URL session cookie store. Room
identity is always derived from the server-side session; any name sent over the
websocket is ignored.

## Websocket Envelope

All websocket frames use the existing envelope:

```json
{"event":"event_name","data":"json encoded string"}
```

Room, board, Scout, participant, memory, recording, archive, and screen-share
updates are nested under top-level `event:"kanban"` with another `{event,data}`
payload encoded in `data`.

## Join And Media Negotiation

Native clients use the same server-offer flow as the browser:

1. Open `/websocket` with the session cookie.
2. Send `participant`; optional capability data is allowed but ignored by v1.
3. Wait for `kanban/access_granted`.
4. Create the native `RTCPeerConnection`, local audio/video tracks, and
   transceivers.
5. Send `media_ready`.
6. Receive top-level `offer` from the server.
7. Set the remote description, attach local tracks to matching sections, create
   an answer, set the local description, and send top-level `answer`.
8. Exchange top-level `candidate` messages.

Signaling payloads are the same JSON-string payloads used by the browser:

```json
{"event":"answer","data":"{\"type\":\"answer\",\"sdp\":\"...\"}"}
{"event":"candidate","data":"{\"candidate\":\"candidate:...\",\"sdpMid\":\"0\",\"sdpMLineIndex\":0,\"usernameFragment\":\"...\"}"}
```

Native clients should send `restart_ice` when local ICE recovery requires a
server-side ICE restart. Subscriber video quality is selected with
`select_layer` and one of the supported layer strings.

## Compatibility Rules

- Do not rename existing browser events.
- Do not remove or reshape `rtcConfiguration`.
- Do not introduce a client-offer path for v1.
- Keep private Scout voice on `/assistant/realtime-offer`; it must not join the
  shared room peer.
- Keep screen share as a replacement outgoing video track until a companion
  screen-share session protocol is explicitly designed.
