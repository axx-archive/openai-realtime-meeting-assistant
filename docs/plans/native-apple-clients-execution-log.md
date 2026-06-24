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
