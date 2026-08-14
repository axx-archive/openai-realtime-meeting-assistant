# Native personal Realtime and AppIcon local checkpoint — 2026-08-14

This is local, unsigned simulator evidence for the dirty E10 candidate. It is
not a TestFlight build, App Store artifact, physical-iPhone result, or live
release receipt.

## What this checkpoint proves

- one enabled idle Home waveform tap enters the private Realtime start path
  exactly once;
- an errored transport is closed before the same control retries, so a visible
  white circle cannot remain inert merely because the previous transport ended
  in `error`;
- an active tap stops the call and a build with the production flag disabled
  cannot start it;
- the production EAS profile explicitly enables private Realtime;
- the locally generated iOS app and ReplayKit extension both carry Build 62;
- an unsigned Release simulator bundle builds, installs, and launches; and
- SpringBoard renders the current composed Stride icon rather than the old
  regressed icon.

## Executed gates

```text
node --import tsx --test --test-reporter=dot src/__tests__/*.test.ts
PASS — 559 tests

npm run typecheck
PASS

EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED=true xcodebuild \
  -workspace Stride.xcworkspace -scheme Stride -configuration Release \
  -sdk iphonesimulator \
  -destination platform=iOS\ Simulator,id=EE7DD6DF-0F68-475B-B439-AF49BB80CCE3 \
  -derivedDataPath build/priority1-realtime-icon \
  CODE_SIGNING_ALLOWED=NO build
PASS — BUILD SUCCEEDED

xcrun simctl install ... Stride.app
xcrun simctl launch ... xyz.thebonfire.app
PASS — process launched
```

The build used the production Realtime environment flag. The app and embedded
extension both report `CFBundleVersion=62`. The generated `mobile/ios` project
is gitignored; its hashes bind this local simulator run, not the reviewed source
identity of a future EAS carrier.

## Captures

- `release-simulator-launch.png` — installed unsigned Release bundle at the
  authenticated-app boundary.
- `release-simulator-springboard.png` — current Stride icon rendered by the
  booted iPhone 17 Pro simulator.

## Open external gate

The founder's physical iPhone was not connected to this host. A signed reviewed
carrier still needs to be built, distributed, installed on that phone, and
accepted against a real account and microphone. That device pass must prove a
single tap reaches visible connecting/listening state, audible two-way speech,
meeting recall, recoverable failure, and the current SpringBoard icon. No local
simulator evidence authorizes shipping.
