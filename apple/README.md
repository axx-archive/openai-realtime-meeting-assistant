# MeetingAssist Apple

This directory is the native Apple foundation for MeetingAssist. It is package
first on purpose: the shared protocol, API, signaling, media, Scout, and design
modules can compile and test before installable iOS/macOS app bundle targets are
added.

The native clients speak the existing MeetingAssist room contract:

1. Read `GET /native/config` for roster and endpoint discovery.
2. Sign in with `POST /auth/login` and retain the cookie session.
3. Read authenticated `GET /client-config` for ICE and protocol metadata.
4. Open `/websocket`, send `participant`, wait for `access_granted`, send
   `media_ready`, then answer server offers.

The current package includes the shared Swift models, API client, signaling
actor, media/session abstractions, SwiftUI shell views, and tests. The
`MeetingAssistRoomRTC` module is intentionally protocol-first so the real
WebRTC adapter can be hardened behind a small surface. A first pass with the
`stasel/WebRTC` 149.0.0 binary package resolved successfully but failed the
macOS Swift package test build on framework header imports, so the binary
adapter belongs in the next WebRTC-specific wave instead of this foundation
commit.

## Local Gates

```bash
go test ./...
cd apple
swift test
```

The committed checkpoint is a SwiftPM foundation, not an installable app bundle
yet. Xcode 26.5 on this machine did not expose package schemes from the
workspace for command-line `xcodebuild test`, so simulator app-target and
physical-device media gates remain next-wave release blockers. They are required
before claiming native call quality or stability improvements.
