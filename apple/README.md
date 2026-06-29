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
go test ./...
cd apple
swift test
xcodegen generate --spec project.yml
xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test
xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS,arch=arm64' test
cd ..
node scripts/native-apple-release-readiness.mjs
```

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
enable local archive or device builds, copy
`Config/Signing.local.example.xcconfig` to ignored
`Config/Signing.local.xcconfig` and set your real `DEVELOPMENT_TEAM`, or provide
`DEVELOPMENT_TEAM` / `APPLE_DEVELOPMENT_TEAM` in the build environment. Keep
real team IDs out of git.

Do not add `Xcode/PrivacyInfo.xcprivacy` until the product-owned answers in
`../docs/native-apple-privacy-review.md` are final. The strict preflight rejects
missing, empty, or shape-incomplete privacy manifests because this client sends
user, room, media, and diagnostic data to the MeetingAssist service.

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
operator-fillable artifacts plus `ReleaseEvidence.draft.json`. Replace the
pending artifacts with real device, TURN, TestFlight, and notarization proof,
then copy the draft into ignored local evidence:

```bash
node scripts/native-apple-release-proofpack.mjs --proofpack-dir artifacts/native-apple/<run-id> --write-evidence
node scripts/native-apple-release-readiness.mjs --strict
```

The proof-pack runner is an evidence workflow, not a release claim. Strict
readiness still fails until the draft contains completed statuses, local
artifact references point at files that exist, signing/privacy blockers are
resolved, and Apple/TestFlight/notarization proof is real.

The native room UI includes a QA evidence panel that captures a non-secret
`native_device_media` JSON snapshot from summarized WebRTC stats. The snapshot
can be copied into the matching
`artifacts/native-apple/<run-id>/evidence/{iphone,ipad,mac}-media.json` file
during a real device run. These snapshots carry `claimScope: "qa_snapshot"`,
`releaseEligible: false`, and `status: "observed"` even when all media
assertions are true. Their assertion sources are cumulative peer-connection
counters, so they are diagnostic observations, not fresh-interval current-health
proof. The native app auto-fills app version/build/target plus device kind,
hardware model, OS version, and physical-vs-simulator metadata; it deliberately
does not collect iPhone/iPad device names or macOS host names. Release `runId`
and `roomId` still come from the proof-pack/operator workflow. Do not promote a
snapshot into `ReleaseEvidence.local.json` as passed physical proof unless it
was captured on the matching physical iPhone, iPad, or Mac for the same run,
room, version, and build. Simulator or repo-only snapshots are diagnostic
artifacts only.

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
