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
booleans, safe RTP counters, remote tile count, renderer-observed remote frame
counts/dimensions/timestamp, lifecycle, app version/build/target, device
kind/hardware model/OS, platform/version, and selected candidate-pair type/RTT
summary; it must not include raw SDP, raw ICE candidates, candidate IDs, IP
addresses, TURN URLs, TURN usernames, TURN credentials, cookies, headers, API
keys, Team IDs, provisioning data, iPhone or iPad device names, macOS host
names, screenshots, pixels, or raw video frames. Release `runId` and `roomId`
are operator/proof-pack fields, not auto-discovered room state. The snapshot
status remains `observed` for QA exports. Remote video proof requires native
renderer observation plus decoded inbound video, not decoded stats alone.

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

The proof-pack generator also creates ignored `inbox/*.template.json` files for
the external release run. These templates are scaffolds, not observations and
not proof. They intentionally keep placeholder values such as
`status: "template"`, false media assertions, non-physical device flags, empty
build ids, and incomplete notarization fields so promotion fails until an
operator replaces them with real sanitized observations from the run.

Restrictive-network TURN proof follows the same rule. The app/operator may
export a sanitized `native_turn_relay_observation`, but it is not release proof
until `scripts/native-apple-promote-turn-evidence.mjs` validates the same-room
native app version/build, physical device context, selected relay candidate-pair
facts, and sanitized ICE-readiness summary. Native UI exports must derive that
observation from native media stats plus `ClientRTCConfig` ICE server counts,
not from raw ICE config serialization. They should reject blank network labels,
unclean ICE readiness, ambiguous `turn:` plus `turns:` protocol mixes,
non-relay selected candidates, and zero or missing RTT. The promoted
`native_restrictive_turn` artifact may include only safe summary fields such as
relay protocol/type, local/remote candidate type labels, RTT, app/build/device
metadata, and TURN-readiness counts. It must not include raw SDP, raw ICE
candidates, candidate IDs, IP addresses, TURN URLs, usernames, credentials,
cookies, headers, API keys, Team IDs, certificates, profiles, or private keys.

Distribution proof is also operator-only. Sanitized App Store Connect/TestFlight
and macOS notarization observations are local proof-pack inputs, not websocket
events and not app-exported media diagnostics. `scripts/native-apple-promote-distribution-evidence.mjs`
promotes those observations only after an operator confirms the current build,
completed upload or notarization/stapling/Gatekeeper checks, and absence of
secret-bearing fields. TestFlight proof may include app version/build, target,
bundle id, App Store Connect build id, processing status, and timestamps. macOS
notarization proof may include app version/build, target, bundle id, distribution
artifact filename/hash, Developer ID signing booleans, notary request id/status,
stapling validation, Gatekeeper acceptance, and timestamps. It must not include
raw upload/notary/codesign/spctl logs, API keys, Apple IDs, Team IDs, p8 or p12
files, provisioning profiles, certificates, private keys, keychain identities,
usernames, headers, cookies, or other account identifiers.

Copying `ReleaseEvidence.draft.json` to `apple/ReleaseEvidence.local.json` is a
local evidence handoff, not a release claim. The proof-pack script refuses to
copy incomplete drafts by default, but strict readiness is still the authority
because it validates promoted artifact contents, current version/build, signing,
privacy, and local artifact references together.

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
