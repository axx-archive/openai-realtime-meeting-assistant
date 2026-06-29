# Native Apple Privacy Review

This is the product-owned checklist that must be completed before adding
`apple/Xcode/PrivacyInfo.xcprivacy` and attempting TestFlight, App Store, or
notarized macOS distribution.

Apple privacy manifests describe the app or SDK data practices and required
reason API use. The native client must not ship with an empty or guessed
manifest.

## Current Native Client Facts

- The iOS/iPadOS and macOS apps request camera and microphone access when a
  user joins a video room. The macOS app also includes camera and audio-input
  entitlements.
- The macOS app has a native screen-share path through the WebRTC desktop
  capturer and must be reviewed as screen/content capture.
- Authentication sends a roster name and password to `/auth/login`, then keeps
  the resulting room session in the shared `URLSession` cookie store.
- The user-entered room URL, selected participant, room roster names/emails,
  room recording state, media state, board cards, Scout prompts/chats, memory
  entries, archive metadata/download URLs, and archive email-recipient status
  all flow through the native client.
- Native diagnostics send `media_quality` over the existing websocket. The
  payload includes platform/version, enabled media state, remote tile counts,
  WebRTC RTP counters, jitter/loss/RTT summaries, and ICE candidate-pair
  metadata.
- Native QA evidence snapshots can be copied locally from the app into ignored
  release proof-pack artifacts. They contain assertion booleans, platform/build
  context, app version/build/target, device kind, hardware model, OS version,
  physical-vs-simulator state, safe WebRTC counters, remote tile counts,
  renderer-observed remote frame counts/dimensions/timestamp, and candidate-pair
  type/RTT summaries. They intentionally omit raw SDP, raw ICE candidates, IP
  addresses, TURN credentials, cookies, headers, API keys, Team IDs,
  certificates, provisioning data, iPhone/iPad device names, macOS host names,
  screenshots, pixels, and raw video frames.
- The Apple package has no app-owned analytics SDK in source. Its direct native
  binary dependency is `LiveKitWebRTC`, pinned through SwiftPM.

## Product Decisions Required

For each data category above, product/legal must decide:

- Apple privacy data type category.
- Purpose, such as app functionality, diagnostics, or analytics.
- Whether the data is linked to the user.
- Whether the data is used for tracking.
- Whether the data is retained or logged server-side.
- Whether the data is shared with processors such as OpenAI, Resend, TURN/WebRTC
  infrastructure, email systems, or hosting infrastructure.

For required-reason APIs, engineering must confirm whether app code or bundled
SDKs access any categories that require declarations. If none are used by the
app target, keep `NSPrivacyAccessedAPITypes` present as an empty array in the
final manifest so the review decision is explicit.

## Release Gate

Only after those answers are final:

1. Copy `apple/PrivacyManifest.decisions.example.json` to ignored
   `apple/PrivacyManifest.decisions.local.json`.
2. Fill in the approved data-practice answers, including approval metadata, and
   set `approval.approved` to `true`.
3. Run:

   ```bash
   node scripts/native-apple-generate-privacy-manifest.mjs \
     --apple-dir apple \
     --decisions-file apple/PrivacyManifest.decisions.local.json \
     --confirm-approved \
     --wire-project \
     --generate-xcode-project
   ```

4. Include non-empty `NSPrivacyCollectedDataTypes` declarations matching the
   approved data practices.
5. Include `NSPrivacyTracking`, `NSPrivacyTrackingDomains`, and
   `NSPrivacyAccessedAPITypes`.
6. Run `node scripts/native-apple-release-readiness.mjs --strict`.

The readiness script intentionally rejects a missing, empty, or shape-incomplete
privacy manifest because this app already transmits user, room, media, and
diagnostic data to the MeetingAssist service. It also rejects a manifest that is
present on disk but not wired into the generated Xcode app-target resources.

After privacy is approved, strict release readiness still requires
`apple/ReleaseEvidence.local.json` or an explicit `--evidence-file` with
physical-device, restrictive-TURN, TestFlight, and macOS notarization proof for
the same version/build, tied to one release run and backed by non-secret
artifact references.
