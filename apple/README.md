# MeetingAssist Apple

This directory is the native Apple foundation for MeetingAssist. It is package
first on purpose: the shared protocol, API, signaling, media, Scout, and design
modules compile and test before the real WebRTC adapter and device media matrix
are allowed to claim production call quality.

The native clients speak the existing MeetingAssist room contract:

1. Read `GET /native/config` for roster and endpoint discovery.
2. Sign in with `POST /auth/login` and retain the cookie session.
3. Read authenticated `GET /client-config` for ICE and protocol metadata.
4. Open `/websocket`, send `participant`, wait for `access_granted`, send
   `media_ready`, then answer server offers.

The current package includes the shared Swift models, API client, signaling
actor, room-session coordinator, media/session abstractions, shared room UI,
and tests. The generated Xcode project adds thin native iOS/iPadOS and macOS
app bundle targets around those shared modules so command-line app builds and
smoke-level XCTest gates are repeatable. The `MeetingAssistRoomRTC` module is
intentionally protocol-first and now uses a pinned `LiveKitWebRTC` XCFramework
package behind that small surface for audio-only peer-connection setup. A first
pass with the `stasel/WebRTC` 149.0.0 binary package resolved successfully but
failed the macOS Swift package test build on framework header imports;
LiveKit's prefixed XCFramework imported and built cleanly on macOS.

`MeetingAssistRoom` is the first native room-entry coordinator. It sequences
native discovery, cookie login, `/client-config`, websocket `participant`,
`kanban/access_granted`, audio-only `media_ready`, top-level server `offer`,
client `answer`, pending remote ICE candidates, `restart_ice`, `select_layer`,
and `participant_media_state` publication through the existing protocol-first
RTC adapter. Local ICE candidates gathered by the native peer connection are
trickled back through the existing top-level `candidate` event.

`MeetingAssistRoomUI` is the first shared native join/control surface. The
iOS/iPadOS and macOS apps now launch it directly, with room URL entry, roster
refresh from `/native/config`, participant selection, password entry,
audio-only or camera join, mute/camera publication, remote video track
rendering, and leave controls backed by `NativeRoomSessionCoordinator`.
Native media failures now emit browser-compatible `media_error` diagnostics, and
server `media_disconnected` events return the UI to a rejoinable state instead
of failing silently.

## Xcode Project

The project is generated from `project.yml` with XcodeGen:

```bash
cd apple
xcodegen generate --spec project.yml
```

`MeetingAssist.xcworkspace` opens the generated `MeetingAssist.xcodeproj`.
`MeetingAssistAppleApp` is a universal iPhone/iPad app target, and
`MeetingAssistMacApp` is a native macOS target. Both depend on the local
SwiftPM package products rather than duplicating app logic.

## Local Gates

```bash
node scripts/native-apple-local-gates.mjs \
  --live-url http://127.0.0.1:3100 \
  --participants Tom,Caitlyn \
  --live-timeout-ms 100000 \
  --run-xcode
```

`scripts/native-apple-local-gates.mjs` runs the repo-owned checkpoint suite in
one place: browser media fix verification, voice-focus benchmark, Go tests,
SwiftPM tests, default native Apple release readiness, optional live media smoke,
and optional iOS simulator/macOS Xcode tests. Run it with `--dry-run` to inspect
the command plan without executing it.

When `--run-xcode` is enabled, app-target tests are generated with
`CODE_SIGNING_ALLOWED=NO` so the local gate can compile and test without Apple
Developer credentials. Real archive/device builds still require the signing
preflight and external release evidence below.

The live media smoke is intentionally not run unless `--live-url` is provided.
Without that URL the command reports `complete:false` and a skipped
`liveMediaSmoke` blocker. Add `--require-live-media-smoke` when a mergeable
checkpoint must fail closed if no local or live room URL is available.

## Release Preflight

`scripts/native-apple-release-readiness.mjs` checks the repo-owned Apple
release prerequisites without requiring Apple account credentials. Default mode
must pass before each release-readiness checkpoint:

```bash
node scripts/native-apple-release-readiness.mjs
node scripts/native-apple-release-readiness.test.mjs
```

Strict mode is expected to fail until the external release blockers are
resolved:

```bash
node scripts/native-apple-release-readiness.mjs --strict
```

Current strict blockers are intentionally explicit: Apple development team or
private signing configuration, `PrivacyInfo.xcprivacy` after product-owned
privacy answers are final, physical device media proof, and actual
TestFlight/notarization credentials. Do not treat a passing default preflight
as a TestFlight upload or notarized macOS app.

Signing is wired through `Config/Signing.xcconfig` for both app targets. To
enable local archive or device builds, either provide `DEVELOPMENT_TEAM` /
`APPLE_DEVELOPMENT_TEAM` in the build environment or generate the ignored local
config from your Apple Developer Team ID:

```bash
node ../scripts/native-apple-configure-signing.mjs \
  --apple-dir . \
  --team-id TEAMID12345 \
  --confirm-local-only
node ../scripts/native-apple-configure-signing.mjs --apple-dir . --validate-only
```

Replace `TEAMID12345` with the real 10-character Team ID from your Apple
Developer account. The helper refuses placeholders, refuses to write a local
config unless it is ignored by git, and redacts Team IDs from JSON output. This
only configures the local Team ID; it does not prove certificates, provisioning
profiles, App Store Connect access, TestFlight upload, Developer ID signing, or
notarization.

Do not add `Xcode/PrivacyInfo.xcprivacy` until the product-owned answers in
`../docs/native-apple-privacy-review.md` are final. The strict preflight rejects
missing, empty, or shape-incomplete privacy manifests because this client sends
user, room, media, and diagnostic data to the MeetingAssist service.

After product/legal approval, create the manifest from an ignored copy of the
decisions template:

```bash
cp PrivacyManifest.decisions.example.json PrivacyManifest.decisions.local.json
# Fill the local file with approved answers, set approval.approved to true,
# then generate and wire the manifest:
node ../scripts/native-apple-generate-privacy-manifest.mjs \
  --apple-dir . \
  --decisions-file PrivacyManifest.decisions.local.json \
  --confirm-approved \
  --wire-project \
  --generate-xcode-project
```

The generator refuses unapproved, placeholder, tracking-domain-incomplete, or
secret-shaped decisions. It writes `Xcode/PrivacyInfo.xcprivacy`, wires the file
into both app targets, and can regenerate `MeetingAssist.xcodeproj` so strict
readiness can verify the manifest is bundled.

Strict mode also requires build-bound distribution proof before it can report
`readyForDistribution: true`. Copy `ReleaseEvidence.example.json` to ignored
`ReleaseEvidence.local.json` after the real run, or pass an explicit evidence
file:

```bash
node scripts/native-apple-release-readiness.mjs --strict --evidence-file /path/to/ReleaseEvidence.json
```

The release operator can create a non-secret proof pack before the real run:

```bash
node scripts/native-apple-release-proofpack.mjs --run-id native-apple-YYYYMMDD-a --room-id release-room-YYYYMMDD
```

The proof pack is written under ignored `artifacts/native-apple/` and contains
pending `evidence/` artifacts, fill-in `inbox/*.template.json` observation
scaffolds, and `ReleaseEvidence.draft.json`. Fill sanitized inbox observations
from the real external run, promote them with the helper scripts below, then copy
the completed draft into ignored local evidence:

```bash
node scripts/native-apple-release-proofpack.mjs --proofpack-dir artifacts/native-apple/<run-id> --write-evidence
node scripts/native-apple-release-readiness.mjs --strict
```

The proof-pack runner is an evidence workflow, not a release claim.
`--write-evidence` refuses incomplete drafts by default; `--force` exists only
for diagnostic local checks. Strict readiness still fails until the draft
contains completed statuses, local artifact references point at files that
exist, signing/privacy blockers are resolved, and Apple/TestFlight/notarization
proof is real.

Files under `inbox/` are operator inputs. Files under `evidence/` are the
pending or promoted release artifacts. Do not edit promoted `evidence/` files by
hand; copy a generated `inbox/*.template.json` file to the matching non-template
name only after replacing placeholders with values from the real run, then let
the promoter rewrite `evidence/` and `ReleaseEvidence.draft.json`.

The generated `inbox/README.md` includes a non-secret native launch-link
template:

```text
meetingassist://room?url=https%3A%2F%2Fthebonfire.xyz&name=<participant-name>&runId=<run-id>&roomId=<room-id>
```

Open that link on each TestFlight/device-run app, replacing only
`<participant-name>`. The app pre-fills the room URL, participant, release
run ID, and release room ID, but it does not include a password and does not
auto-join. Passwords, tokens, cookies, signed URLs, TURN credentials, Apple
account identifiers, Team IDs, provisioning details, certificates, private keys,
and raw logs must stay out of launch links and inbox files.

Before moving to the Apple-account machine, generate the non-secret operator
command pack for the proof pack:

```bash
node scripts/native-apple-release-package-plan.mjs \
  --proofpack-dir artifacts/native-apple/<run-id> \
  --write
```

This writes `operator/release-command-plan.json`,
`operator/release-commands.md`, and the iOS/macOS export option plists under the
ignored proof-pack directory. The command pack contains the Xcode archive,
TestFlight export/upload, Developer ID export, notarytool, stapler, Gatekeeper,
post-run promotion commands, and an offline operator preflight. It does not run
the archive/upload/notarization commands, does not contact Apple, and does not
write Team IDs, certificate names, provisioning profiles, App Store Connect
keys, notarytool profile names, or raw command logs. Use it as the deterministic
checklist on the machine that has the Apple account, certificates, profiles, and
notarytool keychain profile configured.

Before archive/upload/notarization on that machine, run the generated
`operatorPreflight` command or call it directly:

```bash
node scripts/native-apple-release-operator-preflight.mjs \
  --proofpack-dir artifacts/native-apple/<run-id> \
  --require-privacy-manifest \
  --require-notary-profile \
  --run-build-rehearsal
```

The preflight checks local Apple tooling, schemes, ignored signing config,
default release readiness, approved privacy manifest presence/wiring,
proof-pack command-plan consistency, export option plists, notary profile
environment presence, and Release generic iOS/macOS builds with signing
disabled. The generated command pack includes `--require-privacy-manifest` so
the Apple-account operator run hard-stops until `PrivacyInfo.xcprivacy` exists
and is bundled. It still does not prove App Store Connect login,
provisioning-profile download, notary profile validity, physical devices, or
actual upload/notarization.

The native room UI includes a QA evidence panel that captures a non-secret
`native_device_media` JSON snapshot from summarized WebRTC stats. Use the
panel's Save button during a real device run to export the matching proof-pack
inbox file directly: `iphone-qa_snapshot.json`, `ipad-qa_snapshot.json`, or
`mac-qa_snapshot.json`. The Copy button remains useful for inspection, but the
saved filenames match the promotion commands. Promote those saved inbox files
with `scripts/native-apple-promote-media-evidence.mjs`. These snapshots carry
`claimScope: "qa_snapshot"`, `releaseEligible: false`, and `status: "observed"`
even when all media assertions are true. Their assertion sources are cumulative
peer-connection counters plus count-only native renderer observation, so they
are diagnostic observations, not fresh-interval current-health proof. Remote
video proof requires the native renderer to observe at least one remote frame;
decoded inbound frames and a visible tile are not enough by themselves. The
native app auto-fills app version/build/target plus device kind, hardware
model, OS version, physical-vs-simulator metadata, renderer frame
count/dimensions/timestamp, and the proof-pack `runId`/`roomId` from the launch
link or QA evidence fields; it deliberately does not collect iPhone/iPad device
names, macOS host names, screenshots, pixels, or raw frames. The media promoter
now rejects blank or mismatched `runId`/`roomId` even when the operator confirms
same-room manually.
Do not promote a snapshot into `ReleaseEvidence.local.json` as passed physical
proof unless it was captured on the matching physical iPhone, iPad, or Mac for
the same run, room, version, and build. Simulator or repo-only snapshots are
diagnostic artifacts only.

The same QA panel also has a separate TURN Relay capture flow for restrictive
network runs. After joining the room on the restrictive network, enter the
network label, capture the TURN observation, and save the resulting
`turn-relay-observation.json` into
`artifacts/native-apple/<run-id>/inbox/turn-relay-observation.json`. This export
is built from a fresh native media-stat snapshot plus count-only
`ClientRTCConfig` ICE readiness. It does not include raw ICE candidates, TURN
URLs, usernames, credentials, IP addresses, SDP, cookies, headers, Team IDs, or
account data, and it remains an observation until the promotion helper validates
it.

Use the promotion helper to turn a real app-copied physical-device snapshot
into the matching proof-pack artifact and draft summary:

```bash
node scripts/native-apple-promote-media-evidence.mjs \
  --proofpack-dir artifacts/native-apple/<run-id> \
  --platform iphone \
  --input artifacts/native-apple/<run-id>/inbox/iphone-qa_snapshot.json \
  --confirm-physical-device \
  --confirm-same-room
```

Repeat for `ipad` and `mac`. The helper validates that the input is still a
`qa_snapshot`, came from the expected app version/build and physical platform,
has connected lifecycle, has all four media assertions backed by counters, and
does not contain raw media/credential details. It updates only the selected
device media artifact and `ReleaseEvidence.draft.json`.

The physical-device inbox observation must have `status: "observed"`,
`claimScope: "qa_snapshot"`, `releaseEligible: false`, matching `runId` and
`roomId`, physical device metadata, connected lifecycle, all four media
assertions set to true, positive assertion evidence and counters, and
`remoteVideoTiles > 0`. A generated template has `status: "template"` and
`physical: false`, so promotion rejects it until a real device snapshot replaces
those placeholders.

Promote the restrictive-network TURN observation the same way, using a
sanitized selected-relay artifact copied from the operator run:

```bash
node scripts/native-apple-promote-turn-evidence.mjs \
  --proofpack-dir artifacts/native-apple/<run-id> \
  --input artifacts/native-apple/<run-id>/inbox/turn-relay-observation.json \
  --network "restricted guest network" \
  --confirm-restrictive-network \
  --confirm-same-room
```

The TURN helper requires a same-room native app observation for the current
version/build, a physical iPhone/iPad/Mac context, selected candidate-pair relay
facts, a sanitized `native-ice-readiness.mjs --require-turn` summary, and no raw
ICE candidates, TURN URLs, usernames, credentials, IP addresses, or account
identifiers. It updates only `restrictiveNetworkTurn` and the matching
`selected-turn-relay.json` artifact.

The TURN inbox observation must have `status: "observed"`, matching `runId` and
`roomId`, a named restrictive network, physical device metadata, selected relay
candidate-pair facts, positive RTT, and a clean ICE-readiness summary with
credentialed TURN/TURNS and no warnings or errors.

Promote the App Store Connect/TestFlight upload observation with a sanitized
operator artifact after a real archive/upload:

```bash
node scripts/native-apple-promote-distribution-evidence.mjs \
  --proofpack-dir artifacts/native-apple/<run-id> \
  --kind testflight \
  --input artifacts/native-apple/<run-id>/inbox/testflight-observation.json \
  --confirm-app-store-connect-upload \
  --confirm-no-secrets \
  --confirm-current-build
```

Promote accepted, stapled macOS notarization proof separately:

```bash
node scripts/native-apple-promote-distribution-evidence.mjs \
  --proofpack-dir artifacts/native-apple/<run-id> \
  --kind notarization \
  --input artifacts/native-apple/<run-id>/inbox/notarization-observation.json \
  --confirm-developer-id-archive \
  --confirm-notary-accepted \
  --confirm-stapled-app \
  --confirm-gatekeeper-accepted \
  --confirm-current-build
```

The distribution helper does not upload to TestFlight, notarize, staple, or
access Apple credentials. It promotes already-completed, non-secret operator
observations into the proof pack and updates only `testFlight` or
`macNotarization` in `ReleaseEvidence.draft.json`.

The TestFlight inbox observation must have `status: "observed"`, matching
`runId`, current iOS/iPad app version/build/bundle id, an App Store Connect
build id, and processing status of `ready`, `uploaded`, `processing`, or
`accepted`. This proves upload evidence, not external tester availability.

The macOS notarization inbox observation must have `status: "accepted"`, matching
`runId`, current macOS app version/build/bundle id, distribution artifact
filename and SHA-256, Developer ID signing booleans, accepted notary request
with zero issues, stapling validation, and Gatekeeper acceptance. It is still
only an inbox observation until the notarization promoter writes the release
artifact.

Evidence must match the current `MARKETING_VERSION` and
`CURRENT_PROJECT_VERSION`, use one shared `runId` and `roomId`, and include
artifact references for the underlying proof. Physical-device entries must
cover iPhone, iPad, and Mac in the same mixed-room run and assert camera,
microphone, remote-audio, and remote-video success. Restrictive-network TURN
evidence must be tied to the same run and include a selected relay-candidate
artifact. TestFlight/App Store Connect upload and accepted/stapled macOS
notarization also need artifact references. Keep raw TURN credentials, App
Store Connect API keys, provisioning profiles, cert private keys, and real Team
IDs out of evidence files; the strict checker rejects unknown or secret-shaped
evidence fields.

When a physical-device `artifactRef` points to a local JSON file, strict
readiness also inspects the artifact content. The artifact must be promoted
physical-device release proof for the same version, build, run, room, platform,
and device OS; it must have `claimScope: "physical_device"`,
`releaseEligible: true`, `status: "passed"`, `lifecycle: "connected"`, physical
device metadata, all four media assertions true, and supporting assertion
evidence/counters. A copied QA snapshot with `claimScope: "qa_snapshot"` or a
simulator artifact remains useful diagnostic evidence, but it cannot satisfy
physical-device release readiness.

When a restrictive TURN `artifactRef` points to a local JSON file, strict
readiness also inspects that content. The artifact must be promoted
`native_restrictive_turn` proof with `claimScope: "restrictive_network_turn"`,
`releaseEligible: true`, a matching run/room/network/timestamp, physical native
app/device metadata for the current build, selected relay candidate-pair facts,
and a sanitized ICE-readiness summary with credentialed TURN/TURNS servers and
no warnings or errors.

When a TestFlight `artifactRef` points to a local JSON file, strict readiness
requires promoted `native_testflight_upload` proof with
`claimScope: "app_store_connect_upload"`, `releaseEligible: true`, current
version/build, the matching App Store Connect build id, processing status, and
operator confirmations for upload, current build, and no secret-bearing fields.

When a macOS notarization `artifactRef` points to a local JSON file, strict
readiness requires promoted `native_macos_notarization` proof with
`claimScope: "macos_notarization"`, `releaseEligible: true`, current
version/build, Developer ID signing assertions, accepted notary status, zero
issues, stapling validation, Gatekeeper acceptance, distribution artifact
filename/hash, and operator confirmations for the current build and completed
notarization/stapling/Gatekeeper checks.

The app icon asset catalog is generated from `Xcode/AppIconSource.svg`:

```bash
node scripts/generate-native-apple-app-icons.mjs
cd apple
xcodegen generate --spec project.yml
```

This checkpoint has a real native WebRTC binary linked, can create the
audio-only peer connection locally, and now includes native camera publishing
and remote video renderer plumbing in the app targets. It is not a finished
release-quality native video client. Browser/native media proof, physical
iPhone, iPad, and Mac media tests, participant-labeled remote video, signing,
privacy, and release packaging remain blockers before claiming native call
quality or stability improvements.
