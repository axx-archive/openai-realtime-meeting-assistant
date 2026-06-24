# Native Apple Clients - Execution Log

Date created: 2026-06-24
Primary plan: `/Users/ajhart/Downloads/PLAN.md`
Branch: `main`

## Goal

Implement native Apple clients as first-class peers on the existing
MeetingAssist Go/Pion room while preserving browser parity. The first shipped
increment is the native protocol foundation, buildable Apple Swift package, and
tests that prove native clients can discover, authenticate, enter signaling,
and compile shared client modules.

## Agent Loop

- Goal owner: main Codex thread coordinates implementation, validation, staging,
  commit, and push.
- Server contract agent: inspected Go auth/config/websocket seams and confirmed
  the minimal additive backend scope.
- Native Apple scaffold agent: inspected Apple tooling and recommended a
  package-first foundation before installable app bundle targets.
- Reviewer gate: block any claim of native quality/stability improvement until
  physical iPhone, iPad, and Mac media proof exists.

## Wave 1

Status: `wave1_foundation_checkpoint_validated`

Scope:
- Add native protocol metadata while preserving browser `/client-config`.
- Add a native roster/config endpoint.
- Document the native room protocol.
- Add Go contract tests for native discovery, websocket admission/media-ready,
  answer, candidate, `restart_ice`, and `select_layer`.
- Add a buildable Apple package/workspace foundation with shared modules/tests.

Files changed:
- `.gitignore`
- `main.go`
- `auth_http_test.go`
- `websocket_auth_test.go`
- `participants_test.go`
- `docs/native-apple-protocol.md`
- `apple/`

Validation:
- `go test ./...` passed.
- `git diff --check` passed.
- `swift test` passed in `apple/`.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local temporary-room smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- Local `GET /native/config` returned `native-room-v1`, cookie auth paths, seven
  roster participants, and the `/client-config` plus `/websocket` room paths.
- Focused native websocket contract tests passed:
  `go test ./... -run 'TestWebsocketNative' -count=1`.
- VPS runtime env was copied into ignored local `.env.local` with `0600`
  permissions for validation/local parity. Secret values were not printed and
  are not committed; available names include `OPENAI_API_KEY`,
  `MEETING_TURN_SECRET`, `MEETING_TURN_URLS`, `RESEND_API_KEY`, and
  `BONFIRE_RUNNER_TOKEN`.
- `swift test` initially failed with `stasel/WebRTC` 149.0.0 because the copied
  macOS framework could not import `WebRTC/RTCAudioSource.h`.
- The foundation package was adjusted to keep WebRTC behind
  `MeetingAssistRoomRTC` without linking the binary in Wave 1.
- `xcodebuild -list -packagePath .` is not supported by the installed Xcode
  26.5, and the workspace currently exposes no shared command-line schemes.

Risks / blockers:
- Package-first Apple foundation is not yet an installable iOS/macOS app bundle.
- Xcode app targets/schemes and simulator test gates remain Wave 2 work.
- Physical-device media proof is still required before claiming video quality or
  stability improvement.
- Private Scout Realtime voice was not exercised in the browser smoke; the VPS
  `OPENAI_API_KEY` is now available locally through ignored `.env.local` for a
  later explicit Realtime validation pass.
- Real TestFlight/App Store submission is out of scope for this commit.

What worked:
- Keeping `/client-config` additive avoids breaking the browser client.
- Deriving the native roster from `meetingParticipantNames` keeps room admission
  and native login discovery aligned.
- Treating WebRTC behind `MeetingAssistRoomRTC` keeps the binary dependency from
  leaking into higher-level app modules.
- Validating the binary package during this wave exposed a concrete macOS
  header/import issue before it could destabilize every Apple module.

## Wave 2

Status: `wave2_xcode_app_shell_checkpoint_validated`

Scope:
- Add a repeatable XcodeGen project spec for native app bundle targets.
- Generate `apple/MeetingAssist.xcodeproj` and point the workspace at it.
- Add a universal iPhone/iPad `MeetingAssistAppleApp` target.
- Add a native macOS `MeetingAssistMacApp` target.
- Add app-level XCTest bundles that compile against the shared SwiftPM native
  modules.
- Update Apple README gates to use project-backed `xcodebuild test` commands.

Files changed:
- `apple/project.yml`
- `apple/MeetingAssist.xcodeproj/`
- `apple/MeetingAssist.xcworkspace/contents.xcworkspacedata`
- `apple/Xcode/MeetingAssistAppleApp.swift`
- `apple/Xcode/MeetingAssistMacApp.swift`
- `apple/Xcode/MeetingAssistAppleApp-Info.plist`
- `apple/Xcode/MeetingAssistMacApp-Info.plist`
- `apple/Xcode/Tests/MeetingAssistAppleAppTests/MeetingAssistAppleAppTests.swift`
- `apple/Xcode/Tests/MeetingAssistMacAppTests/MeetingAssistMacAppTests.swift`
- `apple/README.md`

Validation:
- `go test ./...` passed.
- `swift test` passed in `apple/`.
- `git diff --check` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local temporary-room smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- `xcodegen generate --spec project.yml` passed in `apple/`.
- `xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test` passed one XCTest.
- `xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS,arch=arm64' test` passed one XCTest.

Risks / blockers:
- The app shells are installable targets, but they still exercise only the
  shared native module surface and shell views.
- The real WebRTC adapter, native mic/camera publishing, remote media rendering,
  TURN/device validation, TestFlight upload, and macOS signing/notarization are
  still future waves.
- App schemes are named `MeetingAssistAppleApp` and `MeetingAssistMacApp` to
  avoid colliding with SwiftPM package/product scheme names.
- Physical iPhone, iPad, and Mac media proof remains mandatory before claiming
  quality or stability improvements from native clients.

What worked:
- XcodeGen made the app targets reproducible while keeping SwiftPM as the shared
  source-of-truth for native modules.
- Thin `@main` wrappers around `MeetingAssistIOSRootView` and
  `MeetingAssistMacRootView` avoided duplicating app logic.
- Adding generated Info.plists for the test bundles fixed the first Xcode gate
  failure and made simulator/macOS XCTest repeatable.

## Wave 3

Status: `wave3_native_room_session_coordinator_checkpoint_validated`

Scope:
- Add `MeetingAssistRoom`, a shared Swift module that owns native room-entry
  sequencing across API discovery/login, `/client-config`, websocket admission,
  media-ready signaling, server-offer/client-answer flow, queued remote ICE
  candidates, ICE restart, layer selection, participant media state, leave, and
  coordinator reuse after leave.
- Add typed Swift payloads for server `offer` and `candidate` frames.
- Make `MeetingAssistSignalingClient.send` throw `notConnected` instead of
  silently no-oping before websocket connection.
- Keep real WebRTC media behind `RoomRTCClient` so this checkpoint proves the
  orchestration contract without claiming native mic/camera quality yet.
- Update iOS audio-session setup to use `allowBluetoothHFP`.

Files changed:
- `apple/Package.swift`
- `apple/Sources/MeetingAssistCore/SignalingModels.swift`
- `apple/Sources/MeetingAssistMedia/MediaSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistSignaling/MeetingAssistSignalingClient.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/Tests/MeetingAssistSignalingTests/SignalingClientTests.swift`
- `apple/README.md`

Validation:
- `go test ./...` passed.
- `swift test` passed 12 tests in `apple/`, including six
  `NativeRoomSessionCoordinatorTests`.
- `git diff --check` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local temporary-room smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- `xcodegen generate --spec project.yml` passed in `apple/`.
- `xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test` passed one XCTest and compiled the new `MeetingAssistRoom` module.
- `xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS,arch=arm64' test` passed one XCTest and compiled the new `MeetingAssistRoom` module.

Risks / blockers:
- The coordinator uses a mock/protocol RTC adapter in tests. It does not publish
  real native mic audio yet.
- The real WebRTC adapter, audio playback, TURN/device validation, Scout
  recording-path proof, and physical iPhone/iPad/Mac media gates remain required
  before claiming native quality or stability improvements.
- Cookie reuse is still an integration behavior of the shared `URLSession` stack
  and must be exercised against a real local server when the app UI starts
  invoking the coordinator.

What worked:
- Treating the room join as a single actor made lifecycle ownership explicit
  instead of splitting truth between API, websocket, media, and RTC modules.
- Testing candidate-before-offer queuing preserves a race the Go websocket
  contract already supports for native clients.
- Resetting negotiation state on join/leave prevents stale remote-description
  readiness from leaking into a reused native room coordinator.
- Keeping `RoomRTCClient` protocol-first lets the next WebRTC wave focus on
  media implementation without rewriting auth/signaling orchestration.
