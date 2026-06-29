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

## Wave 4

Status: `wave4_native_webrtc_audio_adapter_checkpoint_validated`

Scope:
- Add a pinned SwiftPM WebRTC binary dependency through
  `livekit/webrtc-xcframework` version `144.7559.10` and commit the resolver
  lockfile.
- Replace the placeholder `NativeRoomRTCClient` with a LiveKitWebRTC-backed
  audio-only peer connection implementation.
- Apply `/client-config.rtcConfiguration` before media setup, including STUN
  and TURN server parsing.
- Create a native audio track, set the server offer as the remote description,
  create/set a local answer, add remote ICE candidates, restart ICE, and close
  cleanly on leave.
- Add local ICE candidate callbacks from the RTC adapter into the existing
  websocket `candidate` event.
- Preserve the Pion/browser candidate JSON shape by keeping `candidate`,
  `sdpMid`, `sdpMLineIndex`, and optional `usernameFragment`.
- Keep camera/video capture explicitly deferred to the next wave.

Files changed:
- `apple/Package.swift`
- `apple/Package.resolved`
- `apple/Sources/MeetingAssistCore/SignalingModels.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoomRTC/RoomRTCClient.swift`
- `apple/Tests/MeetingAssistRoomRTCTests/NativeRoomRTCClientTests.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/README.md`
- `docs/native-apple-protocol.md`
- `docs/plans/native-apple-clients-execution-log.md`

Validation:
- `swift build` passed after switching from `stasel/WebRTC` to LiveKitWebRTC.
- `swift test` passed 17 tests in `apple/`, including four direct
  `NativeRoomRTCClientTests` that instantiate the WebRTC binary and prepare
  audio-only local media.
- `xcodegen generate --spec project.yml` passed in `apple/`.
- `xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test` passed and processed LiveKitWebRTC into the iOS app target graph.
- `xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS,arch=arm64' test` passed and processed LiveKitWebRTC into the macOS app target graph.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local temporary-room smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.

Risks / blockers:
- This proves the native WebRTC binary imports, creates a peer connection, and
  prepares audio locally. It does not yet prove native mic packets reach a
  browser peer, Scout, or the server recording path.
- Physical iPhone/iPad/Mac media proof, TURN validation, audible remote audio,
  and browser/native mixed-room smokes remain required before claiming quality
  or stability improvements.
- Camera/video capture and remote video rendering are still next-wave work.

What worked:
- Trying the stasel M149 package first reproduced the macOS header-import
  blocker directly, making the package decision evidence-based.
- Keeping the WebRTC dependency behind `RoomRTCClient` let the room coordinator
  stay stable while the binary implementation changed.
- The server-contract subagent caught `usernameFragment` and the JSON-string
  websocket envelope risk before the Swift candidate model shipped.
- Moving RTC configuration until after websocket admission prevents denied room
  joins from leaving a native peer connection alive.

## Wave 5

Status: `wave5_native_room_ui_checkpoint_validated`

Scope:
- Add `MeetingAssistRoomUI`, a shared SwiftUI room join/control layer used by
  both the iOS/iPadOS and macOS app targets.
- Add `NativeRoomViewModel` to load `/native/config`, select a participant,
  join the room through `NativeRoomSessionCoordinator`, publish mute state, and
  leave cleanly.
- Add platform client identity for iOS, iPadOS, and macOS without leaking UIKit
  main-actor calls into sendable room factories.
- Replace the demo app root views with `NativeRoomView` so the app targets now
  exercise the real native room-entry path instead of static shell controls.
- Add focused view-model tests for roster loading, successful join, failed join
  cleanup, and mute-state publication.

Files changed:
- `apple/Package.swift`
- `apple/Apps/MeetingAssistIOS/MeetingAssistIOSRootView.swift`
- `apple/Apps/MeetingAssistMac/MeetingAssistMacRootView.swift`
- `apple/Sources/MeetingAssistRoomUI/`
- `apple/Tests/MeetingAssistRoomUITests/`
- `apple/README.md`
- `docs/plans/native-apple-clients-execution-log.md`

Validation:
- `swift test` passed 22 tests in `apple/`, including five
  `NativeRoomViewModelTests`.
- `xcodegen generate --spec project.yml` passed in `apple/`.
- `xcodebuild -quiet -project MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed after the app target compiled the shared room UI.
- `xcodebuild -quiet -project MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS,arch=arm64' test`
  passed after the macOS app target compiled the shared room UI.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local temporary-room smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- Temporary local `/readyz` reported Realtime connected while using ignored
  `.env.local` with isolated temp state files.

Risks / blockers:
- The native app UI currently exposes the audio-only join path. Native camera
  capture, remote video rendering, and mixed browser/native media proof remain
  future waves.
- Physical iPhone, iPad, and Mac tests remain required before claiming native
  quality or stability improvements.
- TestFlight/App Store distribution, macOS signing/notarization, and deployed
  VPS rollout were not part of this commit.

What worked:
- A single `MeetingAssistRoomUI` product keeps iOS/iPadOS and macOS room entry
  aligned instead of forking app-specific join screens.
- Injecting config loaders and session controllers made the UI model testable
  without network or WebRTC side effects.
- Capturing the client identity before creating the sendable session factory
  removed UIKit actor-isolation warnings from the iOS build.
- Keeping app roots as thin wrappers made the app targets prove the same shared
  room surface on both Apple platforms.

## Wave 6

Status: `wave6_native_video_plumbing_checkpoint_validated`

Scope:
- Replace the explicit native-video deferral with local camera capture plumbing
  in `NativeRoomRTCClient`.
- Add retained `LKRTCVideoSource`, `LKRTCVideoTrack`, and
  `LKRTCCameraVideoCapturer` ownership so the capturer/source/track survive
  beyond setup.
- Add a `joinWithCamera` room path that sends `media_ready` with
  `video: true`, publishes participant media state after the answer, and keeps
  the existing audio-only path intact.
- Make mute and camera toggles update local WebRTC track enabled state, not only
  published participant metadata.
- Add remote video track callbacks from Unified Plan receivers and a shared
  SwiftUI Metal renderer tile for iOS/iPadOS and macOS.
- Add app-facing controls for Join video, camera on/off, and a remote video
  grid.
- Keep participant-labeled remote tiles, physical device proof, TestFlight,
  notarization, and release signing out of this checkpoint.

Files changed:
- `apple/Package.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoomRTC/RoomRTCClient.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRemoteVideoTrackView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomViewModel.swift`
- `apple/Tests/MeetingAssistRoomRTCTests/NativeRoomRTCClientTests.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/Tests/MeetingAssistRoomUITests/NativeRoomViewModelTests.swift`
- `apple/README.md`
- `docs/plans/native-apple-clients-execution-log.md`

Validation:
- `swift test` passed 27 tests in `apple/`.
- `xcodegen generate --spec project.yml` passed in `apple/`.
- `xcodebuild -quiet -project MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed with the native video renderer in the iOS app target graph.
- `xcodebuild -quiet -project MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS,arch=arm64' test`
  passed with the native video renderer in the macOS app target graph.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local temporary-room smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- The first local smoke attempt cleared the early media/recording/screen-share
  checkpoints but hung waiting on the external transcription websocket. The
  passing retry set `MEETING_TRANSCRIPT_LANE_ENABLED=false`,
  `MEETING_BRAIN_DISABLED=true`, and `MEETING_BOARD_DISABLED=true` for the
  isolated media smoke while `/readyz` still reported Realtime connected.

Risks / blockers:
- Unit and simulator tests prove compile-time video plumbing, signaling flags,
  callback flow, and UI state. They do not prove camera frames reach browser
  peers or Scout/recording paths.
- The passing browser smoke was a browser-browser preservation gate with
  transcription lane disabled after an external transcription websocket timeout;
  it is not proof of native camera-to-browser media or Scout recording.
- Remote video tiles currently key by WebRTC track id. `participant_track`
  mapping is still needed before the UI can reliably label tiles by
  participant.
- Real iPhone, iPad, and Mac mixed-room media proof remains mandatory before
  claiming quality or stability gains.
- Release packaging remains blocked on app icons, signing team/profiles,
  monotonic build numbers, app/privacy review metadata, macOS sandbox or
  Developer ID/notarization decisions, and archive validation.

What worked:
- Keeping video capture inside `RoomRTCClient` preserved the existing
  coordinator and UI test seams.
- Reusing the existing `media_ready`, `participant_media_state`, and
  `candidate` events kept browser compatibility intact.
- The remote video wrapper lets tests and UI use a stable, type-safe reference
  without exposing LiveKit internals everywhere.
- Adding the Metal renderer as shared SwiftUI kept iOS/iPadOS and macOS on one
  room surface while still using platform-native views.

## Wave 7

Status: `wave7_native_participant_labeled_remote_video_validated`

Scope:
- Verify the ignored local `.env.local` has the same variable names and
  per-variable values as the VPS runtime `.env` without printing secrets.
- Decode the existing `kanban` / `participant_track` metadata in the native
  coordinator instead of adding a new server contract.
- Cache participant labels by forwarded track id, source track id, and reliable
  stream id, matching the browser's resilient remote-media labeling strategy.
- Add a post-join receive loop so late participant-track replays, renegotiation
  offers, and ICE candidates keep flowing after the initial answer.
- Request a `request_participant_tracks` replay when native receives an
  unlabeled remote video track.
- Replace raw remote video track UI state with `NativeRemoteVideoTrackInfo`,
  allowing existing tiles to relabel without duplicating when metadata arrives
  after WebRTC `ontrack`.

Files changed:
- `apple/Sources/MeetingAssistCore/SignalingModels.swift`
- `apple/Sources/MeetingAssistRoom/NativeRemoteVideoTrackInfo.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRemoteVideoTrackView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomViewModel.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/Tests/MeetingAssistRoomUITests/NativeRoomViewModelTests.swift`
- `docs/plans/native-apple-clients-execution-log.md`

Validation:
- `.env.local` and `/opt/meetingassist/deploy/digitalocean/.env` matched by
  variable name and per-variable SHA-256 comparison; no secret values were
  printed or committed.
- `swift test` passed 31 tests in `apple/`.
- `go test ./...` passed.
- `xcodebuild -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed.
- `xcodebuild -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- The package product schemes `MeetingAssistApple` and `MeetingAssistMac` are
  not configured for the Xcode `test` action; the app schemes above are the
  executable Xcode test gates.

Risks / blockers:
- This proves participant-label propagation in unit tests, Swift package tests,
  and simulator/macOS app test builds. It still does not prove real native
  camera/audio frames across iPhone, iPad, Mac, and browser peers.
- Physical device mixed-room media proof, TURN validation, TestFlight upload,
  and macOS signing/notarization remain release gates.

What worked:
- Treating `RoomRTCClient` as a track transport kept participant identity in the
  signaling/room layer where the server contract already lives.
- Reusing the browser's track/source/stream label keys avoided a native-only
  labeling protocol.
- A replay request for unlabeled tracks gives native the same recovery hook the
  browser uses without making the first render path depend on perfect event
  ordering.

## Wave 8

Status: `wave8_native_room_board_surface_validated`

Scope:
- Pull the current VPS runtime secrets into the ignored local deployment env and
  verify `.env.local` already matches the VPS key set and per-variable values
  without printing secret values.
- Decode existing `participants` and `board` Kanban websocket events into typed
  native room models.
- Cache and replay the latest room and board state when native handlers attach.
- Publish room participants, media states, capacity, recording state, and board
  cards through `NativeRoomViewModel`.
- Add a compact native room-state and board-preview surface to the shared
  iOS/iPadOS/macOS SwiftUI room.
- Wire native recording pause/resume and archive buttons to the existing
  `set_recording` and `archive_meeting` websocket events.

Files changed:
- `apple/Sources/MeetingAssistCore/RoomModels.swift`
- `apple/Sources/MeetingAssistCore/SignalingModels.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomViewModel.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/Tests/MeetingAssistRoomUITests/NativeRoomViewModelTests.swift`
- `docs/plans/native-apple-clients-execution-log.md`

Validation:
- VPS `/opt/meetingassist/deploy/digitalocean/.env` and local `.env.local`
  matched by variable name and per-variable comparison; 27/27 values present,
  no secret values printed, and no env file staged.
- Ignored `deploy/digitalocean/.env` refreshed from the VPS with mode `600`.
- `swift test` passed 35 tests in `apple/`.
- Focused native room/board Swift tests passed:
  `NativeRoomSessionCoordinatorTests/testRoomAndBoardSnapshotsAreEmittedDuringJoin`
  and `NativeRoomViewModelTests/testRoomAndBoardSnapshotsUpdateNativeState`.
- `go test ./...` passed.
- `xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed.
- `xcodebuild -project MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.

Risks / blockers:
- This wave proves native room/board state decoding, caching, view-model
  publication, app-target builds, and websocket command wiring. It does not
  prove real native camera/audio frames across iPhone, iPad, Mac, and browser
  peers.
- The native room surface now shows the board and recording controls, but full
  board editing, Scout assistant-event rendering, and archive download handling
  remain future native slices.
- Physical device mixed-room media proof, TURN validation, TestFlight upload,
  and macOS signing/notarization remain release gates.

What worked:
- Keeping room and board state inside the existing `kanban` websocket envelope
  avoided a parallel native protocol.
- A handler replay cache makes late SwiftUI/view-model attachment deterministic
  without forcing an HTTP board bootstrap.
- Reusing `set_recording` and `archive_meeting` preserved browser/native room
  parity while keeping OpenAI and TURN secrets server-side.

## Wave 9

Status: `wave9_native_board_edit_events_validated`

Scope:
- Keep the existing websocket room contract as the native board-edit path; no
  HTTP board API or server protocol fork.
- Add native constants and Codable payloads for browser-parity board commands:
  `manual_create_ticket`, `manual_update_ticket`, `manual_delete_ticket`, and
  `undo_delete_ticket`.
- Send full-card create/update payloads with the server-required `card_id`
  coding key for updates and deletes.
- Decode and replay the existing `undo_available` event into native state so
  native undo stays synchronized with browser clients.
- Add native board create/edit/delete/undo controls to the shared SwiftUI room
  surface, with local sheet draft state and delete confirmation.
- Preserve server snapshots as authoritative; native sends mutation requests and
  waits for the next `board` snapshot rather than mutating `boardCards`
  optimistically.

Files changed:
- `apple/Sources/MeetingAssistCore/RoomModels.swift`
- `apple/Sources/MeetingAssistCore/SignalingModels.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomViewModel.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/Tests/MeetingAssistRoomUITests/NativeRoomViewModelTests.swift`
- `docs/plans/native-apple-clients-execution-log.md`

Validation:
- Server contract explorer confirmed board edit event names, payload shapes,
  validation rules, and snapshot authority against `main.go`, `kanban.go`,
  `index.html`, and `docs/native-apple-protocol.md`.
- Native seam explorer confirmed the smallest safe Swift seam: coordinator
  send methods, view-model actions, local editor draft state, and
  `undo_available` handling.
- `swift test` passed 39 tests in `apple/`.
- `go test ./...` passed.
- `xcodebuild -quiet -project MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed.
- `xcodebuild -quiet -project MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.

Risks / blockers:
- Native board edits are request/response-by-snapshot, matching the browser.
  Native does not yet decode `assistant_event` error messages, `memory`,
  `memory_*`, or `meeting_archived` payloads into first-class UI.
- The board editor covers the source-of-truth card fields used by browser
  manual edits, but it is intentionally compact. Rich board column management,
  card detail comments, Scout prompts, and archive-download rendering remain
  future native slices.
- Physical device mixed-room media proof, TURN validation, TestFlight upload,
  and macOS signing/notarization remain release gates.

What worked:
- Treating server board snapshots as authoritative avoided local/native-only
  state divergence.
- Using a local SwiftUI draft sheet preserved in-progress edits when live board
  snapshots arrive.
- Matching the browser's exact board event names and payload keys kept native
  edits on the existing compatibility surface.

## Wave 10

Status: `wave10_native_scout_memory_archive_validated`

Scope:
- Confirmed the needed runtime keys are already present locally from the VPS:
  `.env.local` and `deploy/digitalocean/.env` both match
  `/opt/meetingassist/deploy/digitalocean/.env`; no Vercel project config was
  present in this repo, no secret values were printed, and no env files were
  changed or staged.
- Added native Codable models for room Scout events, meeting memory entries,
  memory answers, meeting archive results, archive email status, and private
  Scout chat events.
- Decoded and replayed the existing `assistant_event`, `memory`,
  `memory_transcript`, `memory_brain`, `memory_board_update`, `memory_answer`,
  `meeting_archived`, and `scout_chat` Kanban websocket events.
- Added native outbound commands for `assistant_query`, `scout_chat`, and
  `scout_chat_reset`, preserving the browser/server wire contract.
- Published room Scout feed, memory timeline, archive download link, and
  private Scout chat state through `NativeRoomViewModel`.
- Added compact SwiftUI controls for room Scout questions, private Scout chat,
  private thread reset, memory snippets, and archive download on the shared
  iOS/iPadOS/macOS room surface.
- Resolved server-issued relative archive URLs against the configured room base
  URL before presenting native `Link` controls.
- Preserved and rendered private Scout `thread` and `actions` payloads so
  longer research/design/grill/workflow thread launches do not collapse to
  anonymous plain text.

Files changed:
- `apple/Sources/MeetingAssistCore/RoomModels.swift`
- `apple/Sources/MeetingAssistCore/SignalingModels.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomViewModel.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/Tests/MeetingAssistRoomUITests/NativeRoomViewModelTests.swift`
- `docs/plans/native-apple-clients-execution-log.md`

Validation:
- Server contract explorer confirmed Scout, memory, archive, and private
  `scout_chat` event names and payloads against `main.go`, `kanban.go`,
  `memory.go`, `scout_chat.go`, and `index.html`.
- Native seam explorer confirmed the smallest safe Swift seam: replayable
  coordinator handlers, view-model publication, compact SwiftUI rows, and
  avoiding a new REST/native protocol.
- `swift test --package-path apple` passed 45 tests.
- `go test ./...` passed.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- `git diff --check` passed.
- Critic revision pass added a server-shaped `scout_chat` thread/action fixture,
  relative archive URL assertions, and a local `.git/info/exclude` entry for
  the unrelated `.design-import/` worktree import.

Risks / blockers:
- Native now preserves and summarizes room Scout, memory, archive, private
  Scout chat, thread, and action payloads, but it does not yet implement full
  artifact-library navigation or rich action execution from native controls.
- Physical device mixed-room media proof, TURN validation from restrictive
  networks, TestFlight upload, and macOS signing/notarization remain release
  gates before this can be called shippable to end users.
- The Xcode project path is `apple/MeetingAssist.xcodeproj`; root-level
  `MeetingAssist.xcodeproj` commands are stale.

What worked:
- Keeping all Scout/memory/archive work inside the existing Kanban websocket
  envelope preserved browser/native parity and kept secrets server-side.
- Handler replay caches made late SwiftUI attachment deterministic for both
  room-wide events and per-connection private Scout chat.
- The private Scout composer could share the existing websocket session and
  FIFO server worker without introducing a parallel native chat service.

## Wave 11

Status: `wave11_native_macos_screen_share_validated`

Scope:
- Reconfirmed runtime keys without exposing values: this repo has no Vercel
  project marker, and ignored `.env.local` matches the VPS
  `/opt/meetingassist/deploy/digitalocean/.env` key set and normalized
  fingerprint.
- Kept screen sharing on the existing browser/server contract:
  `participant_media_state`, `screen_share_started`, and
  `screen_share_stopped`; no server protocol fork was needed.
- Added native constants for `screen_share_started` and
  `screen_share_stopped`.
- Added a macOS WebRTC desktop-capture path using the bundled
  `LKRTCDesktopCapturer`, replacing the existing outgoing camera video sender
  with a screen track at 15 fps and restoring the camera track on stop.
- Preflights/requests macOS Screen Recording permission before replacing the
  video sender so native does not announce a screen share when capture cannot
  start.
- Added native session ordering that matches browser behavior: start publishes
  screen-sharing media state before `screen_share_started`; stop sends
  `screen_share_stopped` before publishing the restored media state.
- Added native handling for incoming `screen_share_started/stopped` Kanban
  broadcasts so participant badges update even if a participants snapshot is
  delayed.
- Added a macOS-only SwiftUI screen-share toggle beside the room media
  controls, backed by view-model state and readable unavailable-error text.
- Preserved the audio-only path by rejecting screen share when no outgoing
  video sender exists.

Files changed:
- `apple/Sources/MeetingAssistCore/SignalingModels.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoomRTC/RoomRTCClient.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomViewModel.swift`
- `apple/Tests/MeetingAssistCoreTests/SignalingModelTests.swift`
- `apple/Tests/MeetingAssistRoomRTCTests/NativeRoomRTCClientTests.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/Tests/MeetingAssistRoomUITests/NativeRoomViewModelTests.swift`
- `docs/plans/native-apple-clients-execution-log.md`

Validation:
- Server/browser contract explorer confirmed the browser replaces the outgoing
  video track, sends `{ event: "screen_share_started", data: "{}" }` and
  `{ event: "screen_share_stopped", data: "{}" }`, and the server already
  broadcasts participant snapshots, screen-share events, assistant status, and
  keyframes without any native-specific server change.
- Native seam explorer confirmed `ParticipantMediaState.screenSharing` was
  already modeled, while the missing piece was sender retention/replacement,
  explicit screen-share events, and view-model/UI controls.
- `swift test --package-path apple` passed.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local browser live media smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- `git diff --check` passed.

Risks / blockers:
- Automated gates compile and unit-test the macOS native desktop-capture path,
  including the Screen Recording permission error path, but a real Mac must
  still grant Screen Recording permission and prove native Mac share is visible
  to browser/iOS clients.
- Physical iPhone/iPad/Mac mixed-room proof, restrictive-network TURN
  validation, TestFlight upload, and macOS signing/notarization remain release
  gates before this can be called end-user shippable.
- Native iOS/iPadOS ReplayKit broadcast sharing is still intentionally deferred;
  the current first-class native outbound screen share is macOS.

What worked:
- Matching the browser's existing replace-track model avoided SDP/server
  protocol churn.
- Retaining the WebRTC video sender at camera publication time created a small
  reliable seam for macOS screen-track replacement and camera restoration.
- Updating screen-share badges from both participant snapshots and explicit
  screen-share broadcasts reduced ordering sensitivity for late or delayed
  room-state events.

## Wave 12

Status: `wave12_native_media_recovery_validated`

Scope:
- Continued the release-hardening track from the native Apple client plan,
  focused on foreground and network-path recovery.
- Attempted to assign separate native-seam and server-contract explorer agents;
  both delegated workers hit the account usage limit, so the lead agent folded
  those roles back into the main loop and kept the work scoped locally.
- Reused the existing browser/server `restart_ice` contract instead of adding
  a native-only recovery protocol.
- Added `requestICERestart(reason:)` to the native room session UI protocol and
  wired `NativeRoomViewModel.requestMediaRecovery(reason:)` through the existing
  coordinator ICE restart path.
- Added a testable `NativeConnectivityRecoveryPolicy` plus
  `NativeConnectivityMonitor` backed by `NWPathMonitor`.
- Wired the SwiftUI room view to request media recovery when the app returns to
  the active scene phase or when the network path recovers/changes after the
  first stable path sample.
- Kept recovery no-op before join so opening the native app or refreshing the
  roster cannot send room signaling before the user has joined.

Files changed:
- `apple/Sources/MeetingAssistRoomUI/NativeConnectivityMonitor.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomView.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomViewModel.swift`
- `apple/Tests/MeetingAssistRoomUITests/NativeRoomViewModelTests.swift`

Validation:
- `swift test --package-path apple` passed 60 tests.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local browser live media smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- `git diff --check` passed.
- Critic gate accepted after tightening monitor state locking.

Risks / blockers:
- This adds native ICE-restart requests on foreground/network recovery, but it
  does not prove real iPhone/iPad/Mac network switching, restrictive-network
  TURN relay use, background audio route interruptions, or long soak stability.
- Physical device mixed-room proof, restrictive-network TURN validation,
  TestFlight upload, and macOS signing/notarization remain release gates before
  this can be called end-user shippable.

What worked:
- The existing `restart_ice` event was already enough for native recovery,
  which kept the browser/server contract unchanged.
- A pure recovery policy made network flapping behavior testable without
  simulator network manipulation.
- Routing scene and network recovery through the view model preserved the
  existing SwiftUI ownership pattern and kept room signaling out of the view.

## Wave 13

Status: `wave13_native_audio_route_recovery_validated`

Scope:
- Continued release hardening from Wave 12, focused on iOS/iPadOS audio-session
  correctness, route changes, and interruption recovery.
- Confirmed no server-contract change was needed; audio and route recovery reuse
  the existing native `restart_ice` request path.
- Fixed the native join path to configure the video-chat audio session before
  WebRTC prepares local audio/video media.
- Made `MediaSessionCoordinator` expose a thread-safe participant media-state
  snapshot and an injectable audio-session configurator so ordering is testable.
- Added an iOS-only `NativeAudioRecoveryMonitor` that listens for
  `AVAudioSession` interruptions, route changes, and media-services reset, then
  routes recoverable events through `NativeRoomViewModel.requestMediaRecovery`.
- Added a pure audio recovery policy so interruption-start, non-resumable
  interruption end, and category-only route changes do not create noisy recovery
  loops.
- Added `deinit` cleanup to the native recovery monitors in addition to the
  SwiftUI `onDisappear` stop path.

Files changed:
- `apple/Package.swift`
- `apple/Sources/MeetingAssistMedia/MediaSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeConnectivityMonitor.swift`
- `apple/Sources/MeetingAssistRoomUI/NativeRoomView.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `apple/Tests/MeetingAssistRoomUITests/NativeRoomViewModelTests.swift`

Validation:
- `swift test --package-path apple` passed 62 tests.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -destination 'platform=iOS Simulator,name=iPhone 17' test`
  passed.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- Local browser live media smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- `git diff --check` passed.
- Critic gate accepted after adding monitor `deinit` cleanup.

Risks / blockers:
- This proves the native code paths compile and the recovery policy is unit
  tested, but it still does not prove real headset/Bluetooth route changes,
  phone-call interruptions, long background/foreground cycles, or thermal soak
  on physical iPhone/iPad hardware.
- Physical iPhone/iPad/Mac mixed-room proof, restrictive-network TURN
  validation, TestFlight upload, and macOS signing/notarization remain release
  gates before this can be called end-user shippable.

What worked:
- Fixing audio-session configuration at the native room join boundary made the
  route/interruption monitor meaningful without changing the browser/server
  signaling contract.
- A pure audio recovery policy let simulator-safe tests cover the noisy edge
  cases while leaving physical-device route proof as an explicit release gate.
- Keeping recovery dispatch in `NativeRoomViewModel.requestMediaRecovery`
  avoided a second media-recovery pathway and kept scene, network, and audio
  recovery aligned.

## Wave 14

Status: `wave14_native_turn_readiness_validated`

Scope:
- Continued release hardening from Wave 13, focused on native TURN readiness
  before physical restrictive-network validation.
- Assigned a server/browser ICE-contract explorer and a native WebRTC parser
  explorer, then folded both completed findings into the scoped implementation.
- Confirmed the server/browser ICE contract already preserves `rtcConfiguration`
  for native clients and that existing Go tests cover static and ephemeral TURN
  config generation.
- Added a testable `NativeICEServerDescriptor` parser for Apple clients so
  STUN/TURN/TURNS URL arrays, username, and credential handling are explicit
  before `LKRTCIceServer` creation.
- Tightened native parsing to trim blank values, skip malformed ICE server
  entries, and preserve multi-URL relay definitions such as `turn:` plus
  `turns:` in one server.
- Added `scripts/native-ice-readiness.mjs`, a sanitized preflight for captured
  `/client-config` JSON from `--file`, `--stdin`, or synthetic `--json`
  fixtures. It reports counts and relay transports only; it does not print
  usernames or credentials.
- Documented that real credential-bearing config captures should use `--file`
  or `--stdin`, because inline `--json` can be exposed through shell history or
  process listings.
- Kept `/client-config` auth behavior unchanged, so live checks should use an
  authenticated capture or a copied JSON fixture rather than weakening the
  endpoint.

Files changed:
- `apple/Sources/MeetingAssistRoomRTC/RoomRTCClient.swift`
- `apple/Tests/MeetingAssistRoomRTCTests/NativeRoomRTCClientTests.swift`
- `scripts/native-ice-readiness.mjs`
- `scripts/native-ice-readiness.test.mjs`

Validation:
- `swift test --package-path apple` passed 64 tests.
- XcodeBuildMCP iOS simulator test for `MeetingAssistAppleApp` on `iPhone 17`
  passed.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- `node scripts/native-ice-readiness.test.mjs` passed 5 checks.
- `node scripts/native-ice-readiness.mjs --json '<valid TURN fixture>' --require-turn`
  passed.
- `node scripts/native-ice-readiness.mjs --json '<STUN-only fixture>' --require-turn`
  failed as expected with `No TURN or TURNS relay URLs were found.`
- `node scripts/native-ice-readiness.mjs --json '<unknown-scheme fixture>'`
  failed as expected with `No STUN, STUNS, TURN, or TURNS ICE server URLs were found.`
- Local browser live media smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- `git diff --check` passed.

Risks / blockers:
- This proves the native parser and preflight contract, but it does not prove
  actual TURN relay use on a restrictive network.
- Physical iPhone/iPad/Mac mixed-room proof, restrictive-network TURN relay
  validation, TestFlight upload, and macOS signing/notarization remain release
  gates before this can be called end-user shippable.

What worked:
- Reusing the existing server `rtcConfiguration` payload kept browser and native
  clients on one ICE contract.
- Pulling ICE parsing into a pure descriptor made TURN credentials and multi-URL
  relay fixtures testable without constructing a live peer connection.
- Making the preflight consume captured JSON avoided weakening the authenticated
  `/client-config` endpoint while still giving the release process a repeatable
  TURN readiness check.

## Wave 15

Status: `wave15_native_release_preflight_scaffold_validated`

Scope:
- Continued the release-hardening track from Wave 14, focused on repo-owned
  TestFlight/macOS signing and notarization prerequisites that can be improved
  without Apple account credentials.
- Assigned a release-readiness explorer and a media-QA explorer. The release
  explorer found the immediately actionable slice; the media-QA explorer
  identified native `media_quality` diagnostics as the next useful media slice.
- Added macOS hardened runtime and `MeetingAssistMacApp.entitlements` for
  camera and audio-input access, wired from `project.yml` and regenerated into
  `MeetingAssist.xcodeproj`.
- Moved iOS/iPadOS and macOS version/build strings to `MARKETING_VERSION` and
  `CURRENT_PROJECT_VERSION` build settings, with build number `15` for this
  checkpoint.
- Added `scripts/native-apple-release-readiness.mjs`, a non-secret release
  preflight that checks repo-owned prerequisites in default mode and reports
  external distribution blockers in `--strict` mode.
- Added `scripts/native-apple-release-readiness.test.mjs` with synthetic
  blocked and strict-ready fixtures so the checker is not coupled to today's
  specific blocker set.
- Documented the preflight semantics in `apple/README.md`: default mode is a
  repo prerequisite check, not proof of TestFlight upload or notarization.

Files changed:
- `apple/MeetingAssist.xcodeproj/project.pbxproj`
- `apple/Xcode/MeetingAssistAppleApp-Info.plist`
- `apple/Xcode/MeetingAssistMacApp-Info.plist`
- `apple/Xcode/MeetingAssistMacApp.entitlements`
- `apple/project.yml`
- `apple/README.md`
- `scripts/native-apple-release-readiness.mjs`
- `scripts/native-apple-release-readiness.test.mjs`

Validation:
- `xcodegen generate --spec project.yml` passed in `apple/`.
- `plutil -lint apple/Xcode/MeetingAssistAppleApp-Info.plist apple/Xcode/MeetingAssistMacApp-Info.plist apple/Xcode/MeetingAssistMacApp.entitlements`
  passed.
- `node scripts/native-apple-release-readiness.mjs` passed default mode with
  repo-owned checks green.
- `node scripts/native-apple-release-readiness.mjs --strict` failed as expected
  with external blockers: Apple development team/signing config, real iOS and
  macOS app icons, and `PrivacyInfo.xcprivacy`.
- `node scripts/native-apple-release-readiness.test.mjs` passed 3 checks.
- `swift test --package-path apple` passed 64 tests.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- `node scripts/native-ice-readiness.test.mjs` passed 5 checks.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistAppleApp -sdk iphonesimulator -configuration Debug build CODE_SIGNING_ALLOWED=NO`
  passed as a fallback iOS app compile gate.
- Local browser live media smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.

Risks / blockers:
- The required iPhone simulator app test could not run on this machine because
  Xcode 26.6 reports CoreSimulator version skew: current `1051.54.0`, required
  `1051.55.0`. The fallback generic iOS simulator build passed, but it is not a
  substitute for the simulator test gate.
- This wave does not upload to TestFlight, notarize a macOS build, add final app
  icons, create `PrivacyInfo.xcprivacy`, configure an Apple development team, or
  prove physical-device media.
- Physical iPhone/iPad/Mac mixed-room proof, restrictive-network TURN relay
  validation, native media diagnostics, TestFlight upload, and macOS
  signing/notarization remain release gates before this can be called end-user
  shippable.

What worked:
- Keeping account-specific signing outside the repo avoided committing secrets
  or machine-local Apple configuration while still making missing prerequisites
  mechanically visible.
- Treating strict release readiness as an expected failure preserved honesty:
  default mode proves repo-owned scaffold health; strict mode tracks what still
  needs product/account/device evidence.
- Regenerating from XcodeGen kept the generated project aligned with
  `project.yml`, which is the durable source of truth for this Apple scaffold.

## Wave 16

Status: `wave16_native_media_quality_diagnostics_checkpoint_validated`

Scope:
- Continued the native media-readiness track with diagnostics parity, not a
  media-quality or release-readiness claim.
- Assigned two read-only explorers: one inspected the LiveKitWebRTC stats API
  and native RTC test seam; the other inspected the browser/server
  `media_quality` contract and confirmed the event is log-only server
  diagnostics, not a broadcast path.
- Added `RoomRTCClient.mediaQualitySnapshot()` and browser-compatible native
  snapshot/delta DTOs for outbound/inbound RTP counters, jitter/loss, selected
  ICE candidate-pair summary, and safe candidate metadata only.
- Wrapped LiveKitWebRTC `statistics` reports into a pure internal stat-entry
  normalizer so synthetic Swift tests can cover aggregation without
  constructing unavailable WebRTC report objects.
- Started a conservative native coordinator media-quality report loop after a
  successful join, stopped it on leave/rejoin reset, and exposed
  `sendMediaQualityReport()` for deterministic tests.
- Emitted existing websocket event `media_quality` from native clients with a
  browser-compatible payload plus explicit native `client.platform` and
  `client.version`.
- Renamed the Go logger from browser-specific to `logClientMediaQualityReport`
  and updated the log prefix to `Client media quality`, while preserving the
  current browser payload path.

Files changed:
- `apple/Sources/MeetingAssistRoomRTC/RoomRTCClient.swift`
- `apple/Sources/MeetingAssistRoom/NativeRoomSessionCoordinator.swift`
- `apple/Tests/MeetingAssistRoomRTCTests/NativeRoomRTCClientTests.swift`
- `apple/Tests/MeetingAssistRoomTests/NativeRoomSessionCoordinatorTests.swift`
- `frontend_latency_test.go`
- `main.go`

Validation:
- `swift test --package-path apple` passed 70 tests.
- `go test ./...` passed.
- `node scripts/media-fix-verification.mjs` passed 21 checks.
- `node scripts/voice-focus-benchmark.mjs` passed with no failures.
- `node scripts/native-ice-readiness.test.mjs` passed 5 checks.
- `node scripts/native-apple-release-readiness.test.mjs` passed 3 checks.
- `node scripts/native-apple-release-readiness.mjs` passed default mode with
  repo-owned checks green.
- `node scripts/native-apple-release-readiness.mjs --strict` failed as expected
  with external blockers: Apple development team/signing config, real iOS and
  macOS app icons, and `PrivacyInfo.xcprivacy`.
- XcodeBuildMCP `test_sim` passed `MeetingAssistAppleAppTests` on `iPhone 17`.
- `xcodebuild -quiet -project apple/MeetingAssist.xcodeproj -scheme MeetingAssistMacApp -destination 'platform=macOS' test`
  passed.
- Local browser live media smoke passed:
  `node scripts/live-media-smoke.mjs --url http://127.0.0.1:3100 --participants Tom,Caitlyn --timeout-ms 100000`.
- The local smoke exercised the renamed server logger, producing
  `Client media quality ...` reports for both browser participants.
- `git diff --check` passed.

Risks / blockers:
- This proves native diagnostics plumbing and browser/server regression health;
  it does not prove physical iPhone/iPad/Mac media quality, mixed-room
  stability, or restrictive-network TURN relay behavior.
- Native reports summarize safe candidate metadata only and intentionally do
  not forward raw WebRTC statistics or candidate addresses.
- The strict release preflight still blocks on external Apple distribution
  inputs: development team/signing config, final app icons, and
  `PrivacyInfo.xcprivacy`.
- Physical iPhone/iPad/Mac mixed-room proof, restrictive-network TURN relay
  validation, TestFlight upload, and macOS signing/notarization remain release
  gates before this can be called end-user shippable.

What worked:
- Keeping stats at the `RoomRTCClient` seam preserved UI/session layering and
  made future native media renderers observable without changing signaling.
- Summarizing WebRTC stats into the same shape the browser already sends let
  the server stay additive and avoided a native-only diagnostics fork.
- The local browser smoke doubled as live proof that the renamed Go logger
  still handles existing browser `media_quality` reports.

## Wave 17

Status: `wave17_native_app_icon_release_readiness_checkpoint_validated`

Scope:
- Continued the repo-owned Apple release-readiness track by removing the iOS,
  iPadOS, and macOS app icon blockers without using Apple account credentials.
- Assigned an asset-catalog explorer and a preflight explorer. They confirmed a
  single shared `Xcode/Assets.xcassets/AppIcon.appiconset` is the safest shape
  for both `MeetingAssistAppleApp` and `MeetingAssistMacApp`, and that the old
  preflight icon check was too shallow.
- Added `Xcode/AppIconSource.svg` plus
  `scripts/generate-native-apple-app-icons.mjs` so the committed icon PNGs are
  reproducible from a source asset.
- Generated a complete shared AppIcon set for iPhone, iPad, iOS marketing, and
  macOS idioms.
- Wired `Xcode/Assets.xcassets` into both app targets in `project.yml`, set
  `ASSETCATALOG_COMPILER_APPICON_NAME: AppIcon`, and regenerated
  `MeetingAssist.xcodeproj`.
- Strengthened `scripts/native-apple-release-readiness.mjs` so icon readiness
  requires expected slots, actual PNG files, correct PNG dimensions, asset
  catalog target wiring, and generated Xcode build settings.
- Updated release-readiness tests so synthetic strict-ready fixtures include
  the full icon matrix, while blocked fixtures still prove missing icons remain
  a strict blocker.
- Updated `apple/README.md` to remove app icons from the current strict blocker
  list and document the icon generation command.

Files changed:
- `apple/MeetingAssist.xcodeproj/project.pbxproj`
- `apple/README.md`
- `apple/project.yml`
- `apple/Xcode/AppIconSource.svg`
- `apple/Xcode/Assets.xcassets/`
- `scripts/generate-native-apple-app-icons.mjs`
- `scripts/native-apple-release-readiness.mjs`
- `scripts/native-apple-release-readiness.test.mjs`

Validation:
- `node scripts/generate-native-apple-app-icons.mjs` regenerated 28 icon PNGs.
- Asset catalog JSON parsed successfully for
  `apple/Xcode/Assets.xcassets/Contents.json` and
  `apple/Xcode/Assets.xcassets/AppIcon.appiconset/Contents.json`.
- `xcodegen generate --spec project.yml` passed in `apple/`.
- `node scripts/native-apple-release-readiness.test.mjs` passed 3 checks.
- `node scripts/native-apple-release-readiness.mjs` passed default mode with
  repo-owned checks green and no icon blockers.
- `node scripts/native-apple-release-readiness.mjs --strict` failed as
  expected with only `apple_development_team` and `privacy_manifest`.

Risks / blockers:
- This proves the app icon asset catalog is present, complete, and wired into
  generated app targets. It does not prove Apple signing, TestFlight upload, or
  macOS notarization.
- The icon is a committed generated brand-ready placeholder for release
  readiness; final brand review may still replace `AppIconSource.svg` and
  regenerate the PNGs.
- Strict release readiness still blocks on Apple development team/signing
  configuration and `PrivacyInfo.xcprivacy` after product-owned privacy answers
  are final.
- Physical iPhone/iPad/Mac mixed-room proof, restrictive-network TURN relay
  validation, TestFlight upload, and macOS signing/notarization remain release
  gates before this can be called end-user shippable.

What worked:
- Treating the SVG as the source of truth made binary icon assets
  reproducible instead of manually maintained.
- Tightening the release preflight prevented an empty `Contents.json` from
  satisfying icon readiness.
- Regenerating from XcodeGen kept asset catalog wiring in `project.yml` as the
  durable source of truth.
