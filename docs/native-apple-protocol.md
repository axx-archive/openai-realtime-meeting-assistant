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

## Media Diagnostics And Recovery

Native clients send `participant_media_state`, `media_quality`, and
`media_error` using the existing browser event names. `media_error` is
best-effort and must keep the browser-compatible top-level keys `stage`,
`browser`, `audio`, and `error`; native clients also include `client` and
`video` summaries. Do not include raw ICE candidates, TURN credentials, IP
addresses, or full WebRTC stats in `media_error`.

Native clients may also export a local `native_device_media` QA evidence
snapshot from the same summarized `media_quality` counters. The export is a
local operator artifact, not a websocket event. It includes only assertion
booleans, safe RTP counters, remote tile count, lifecycle, app
version/build/target, device kind/hardware model/OS, platform/version, and
selected candidate-pair type/RTT summary; it must not include raw SDP, raw ICE
candidates, candidate IDs, IP addresses, TURN URLs, TURN usernames, TURN
credentials, cookies, headers, API keys, Team IDs, provisioning data, iPhone or
iPad device names, or macOS host names. Release `runId` and `roomId` are
operator/proof-pack fields, not auto-discovered room state. The snapshot status
remains `observed` for QA exports, and the assertion source is cumulative
peer-connection stats rather than a fresh current-health interval.

If an operator promotes a local `native_device_media` JSON artifact into
physical-device release evidence, it must be a distinct release-proof artifact
for the same run, room, version, build, platform, and physical device. Strict
readiness rejects unpromoted QA snapshots, simulator captures, pending proof
pack placeholders, mismatched artifacts, and artifacts with raw SDP, raw ICE,
IP addresses, credentials, or account identifiers.

Promotion is explicit operator action, not an app behavior. The app continues
to export `qa_snapshot` diagnostics; `scripts/native-apple-promote-media-evidence.mjs`
validates a physical-device snapshot, binds it to the proof-pack run/room, and
writes the promoted proof artifact plus the matching `ReleaseEvidence.draft.json`
device summary.

The server may send `kanban/media_disconnected` when media negotiation has
failed or stalled. Native clients should treat that as a terminal media session
event, leave the broken peer connection, and return the UI to a rejoinable
state with the server-provided message visible to the user.

## Compatibility Rules

- Do not rename existing browser events.
- Do not remove or reshape `rtcConfiguration`.
- Do not introduce a client-offer path for v1.
- Keep private Scout voice on `/assistant/realtime-offer`; it must not join the
  shared room peer.
- Keep screen share as a replacement outgoing video track until a companion
  screen-share session protocol is explicitly designed.
