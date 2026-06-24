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
actor, room-session coordinator, media/session abstractions, SwiftUI shell
views, and tests. The generated Xcode project adds thin native iOS/iPadOS and
macOS app bundle targets around those shared modules so command-line app builds
and smoke-level XCTest gates are repeatable. The `MeetingAssistRoomRTC` module
is intentionally protocol-first so the real WebRTC adapter can be hardened
behind a small surface. A first pass with the `stasel/WebRTC` 149.0.0 binary
package resolved successfully but failed the macOS Swift package test build on
framework header imports, so the binary adapter belongs in the next WebRTC-
specific wave instead of this foundation commit.

`MeetingAssistRoom` is the first native room-entry coordinator. It sequences
native discovery, cookie login, `/client-config`, websocket `participant`,
`kanban/access_granted`, audio-only `media_ready`, top-level server `offer`,
client `answer`, pending remote ICE candidates, `restart_ice`, `select_layer`,
and `participant_media_state` publication through the existing protocol-first
RTC adapter.

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
```

This checkpoint is an installable-shell foundation, not a finished native video
client. Physical iPhone, iPad, and Mac media tests remain release blockers
before claiming native call quality or stability improvements.
