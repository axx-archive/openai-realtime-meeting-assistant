# Native Apple Release Stage Plan

This plan starts from the current checkpoint: the native iOS/iPadOS room client
and the SwiftUI/WebKit macOS workspace shell build locally, repo-owned release
rails exist, and strict readiness is blocked only by external
Apple/account/device evidence.

Do not treat this document as release proof. It is the ordered operator plan
for turning the iOS/iPadOS client into a TestFlight candidate and STRIDE.app
into a direct-distribution macOS DMG.

## Current Stage

Known-good local evidence from June 30, 2026:

- `MeetingAssistAppleApp` iPhone simulator XCTest passed on `iPhone 17`.
- `MeetingAssistAppleApp` iPad simulator XCTest passed on
  `iPad Pro 13-inch (M5)`.
- `MeetingAssistMacApp` macOS XCTest passed on `platform=macOS`.
- `node scripts/native-apple-release-readiness.mjs --strict --apple-dir apple`
  still reports `readyForDistribution:false`.

Current strict blockers:

- `apple_development_team`: real Apple Team ID/signing is not configured in the
  local environment or ignored signing config.
- `privacy_manifest`: `apple/Xcode/PrivacyInfo.xcprivacy` must be generated
  only from approved product/legal decisions and bundled into both app targets.
- `release_evidence_file`: the release proof-pack still needs real
  physical-device, TURN, room-interop, App Store review, TestFlight, and macOS
  notarization evidence for the same version/build.

## Do Not Claim Yet

The following are still unproven until the later gates pass:

- Physical iPhone or iPad native video chat quality.
- Authenticated Mac WebKit workspace and browser-media behavior on the release build.
- Browser/native mixed-room stability.
- TestFlight availability or external testing readiness.
- App Store review readiness.
- Developer ID signing, notarization, stapling, or Gatekeeper acceptance.
- End-user shipping readiness.

Simulator and local macOS XCTest results are necessary build health checks, but
they are not native media-quality, authenticated workspace, or distribution
proof.

## Stage 1 - Repeat Local Xcode Gates

Run these whenever the native app code, project file, signing wiring, privacy
manifest, or package dependencies change.

```bash
xcodebuild test \
  -project apple/MeetingAssist.xcodeproj \
  -scheme MeetingAssistAppleApp \
  -configuration Debug \
  -destination 'platform=iOS Simulator,name=iPhone 17'

xcodebuild test \
  -project apple/MeetingAssist.xcodeproj \
  -scheme MeetingAssistAppleApp \
  -configuration Debug \
  -destination 'platform=iOS Simulator,name=iPad Pro 13-inch (M5)'

xcodebuild test \
  -project apple/MeetingAssist.xcodeproj \
  -scheme MeetingAssistMacApp \
  -configuration Debug \
  -destination 'platform=macOS'
```

Expected result: all three pass. This proves local app-target build/test health
only.

## Stage 2 - Apple Account Prerequisites

On the Apple-account machine:

1. Configure a real Apple Team ID without committing it:

   ```bash
   export MEETINGASSIST_APPLE_TEAM_ID=<your-real-10-character-team-id>
   node scripts/native-apple-configure-signing.mjs \
     --apple-dir apple \
     --team-id "$MEETINGASSIST_APPLE_TEAM_ID" \
     --confirm-local-only
   node scripts/native-apple-configure-signing.mjs --apple-dir apple --validate-only
   ```

2. Configure a local notarytool keychain profile and export only its non-secret
   profile name in the shell:

   ```bash
   export NOTARYTOOL_KEYCHAIN_PROFILE=meetingassist-notary
   ```

3. Confirm Xcode is signed in to the right Apple Developer account and can see
   the iOS App Store Connect app plus the macOS Developer ID identities.

Do not commit Team IDs, Apple IDs, certificate names, provisioning profiles,
notary credentials, App Store Connect keys, command logs, or screenshots.

## Stage 3 - Privacy Manifest

The final privacy manifest must come from approved product/legal decisions.

```bash
cp apple/PrivacyManifest.decisions.example.json \
  apple/PrivacyManifest.decisions.local.json

# Fill the local file with approved answers and set approval.approved to true.

node scripts/native-apple-generate-privacy-manifest.mjs \
  --apple-dir apple \
  --decisions-file apple/PrivacyManifest.decisions.local.json \
  --confirm-approved \
  --wire-project \
  --generate-xcode-project
```

Expected result: `apple/Xcode/PrivacyInfo.xcprivacy` exists, both app targets
include it as a resource, and strict readiness no longer reports
`privacy_manifest`.

Only commit the generated manifest and project wiring after product/legal
approval. Keep `PrivacyManifest.decisions.local.json` ignored.

## Stage 4 - Proof-Pack Setup

Create one release proof-pack for the build and room being tested:

```bash
node scripts/native-apple-release-proofpack.mjs \
  --run-id native-apple-YYYYMMDD-a \
  --room-id release-room-YYYYMMDD

node scripts/native-apple-release-package-plan.mjs \
  --proofpack-dir artifacts/native-apple/<run-id> \
  --write
```

Use the generated `operator/release-commands.md` as the machine-local command
checklist. Before archive/upload/notarization work, run the generated
`operatorPreflight` command, which should include:

- `--require-proofpack`
- `--require-privacy-manifest`
- `--require-notary-profile`
- `--run-build-rehearsal`

Expected result: operator preflight is ready only after signing, privacy
manifest, proof-pack identity, package-plan identity, export options, notary
profile presence, and generic Release build rehearsals pass.

## Stage 5 - Physical Media Evidence

In the real release room:

1. Open the proof-pack launch link on the iPhone and iPad native apps.
2. Join the same room as a browser peer.
3. Verify native mic/camera publish and remote audio/video render.
4. Save the app-generated QA evidence files into the proof-pack inbox:
   - `iphone-qa_snapshot.json`
   - `ipad-qa_snapshot.json`
5. Promote each file with
   `scripts/native-apple-promote-media-evidence.mjs`.

Expected result: `ReleaseEvidence.draft.json` has passed native-media evidence
for both physical mobile platforms. Simulator snapshots must not be promoted
as physical proof. Separately, exercise the authenticated Home, Rooms,
Conversations, Work, and Drive surfaces in the release Mac app, including its
camera/microphone browser permissions and offline recovery.

## Stage 6 - TURN And Room-Interop Evidence

On a restrictive network:

1. Capture and save `turn-relay-observation.json`.
2. Promote it with `scripts/native-apple-promote-turn-evidence.mjs`.

For room interop:

1. Run a same-room smoke with at least three participants.
2. Include at least one browser client and at least one native iPhone/iPad client.
3. Confirm remote audio, remote video, no missing/duplicate/stalled remote
   health, clean leave with `/participants` empty, and recording-off
   transcript/Realtime forwarding stopped.
4. Create the sanitized inbox observation with
   `scripts/native-apple-create-room-interop-observation.mjs`.
5. Promote it with `scripts/native-apple-promote-room-gate-evidence.mjs`.

Expected result: strict-ready TURN and browser/native 3+ participant room proof
are bound to the same proof-pack `runId`, `roomId`, version, and build.

## Stage 7 - TestFlight And App Store Metadata

On the Apple-account machine:

1. Archive and upload `MeetingAssistAppleApp` using the generated command pack.
2. Confirm the uploaded build is visible in App Store Connect/TestFlight.
3. Complete App Store review metadata, app privacy answers, test information,
   screenshots, export compliance, age rating, and external testing group setup.
4. Create and promote:
   - `app-store-review-observation.json`
   - `testflight-observation.json`

Expected result: `ReleaseEvidence.draft.json` has passed App Store review
metadata and TestFlight upload evidence. This still does not mean Apple has
approved public App Store release.

## Stage 8 - macOS Developer ID Distribution

On the Apple-account machine:

1. Archive `MeetingAssistMacApp` and export `STRIDE.app` with Developer ID signing.
2. Build and sign `STRIDE-<version>.dmg` from that exact exported app.
3. Submit the final DMG to Apple notary service and wait for accepted status.
4. Staple and validate the accepted ticket on the DMG.
5. Gatekeeper-assess both the DMG and its mounted `STRIDE.app`.
6. Create and promote `notarization-observation.json` for that exact DMG.

Expected result: `ReleaseEvidence.draft.json` has passed macOS notarization
evidence with distribution artifact kind, basename, SHA-256, notary request ID,
stapling, and Gatekeeper source recorded without secrets.

## Stage 9 - Final Readiness

After every proof-pack lane is promoted:

```bash
node scripts/native-apple-release-proofpack.mjs \
  --proofpack-dir artifacts/native-apple/<run-id> \
  --write-evidence

node scripts/native-apple-release-readiness.mjs \
  --strict \
  --apple-dir apple
```

Completion evidence is `readyForDistribution:true` with no blockers for the
current version/build. Only then can this stage be treated as release-ready.

The native Apple goal is not complete until this final strict readiness state is
proven and any repo-owned generated files are committed and pushed to `main`.
