# STRIDE E10 permanent Meeting Record evidence — 2026-08-14

This is the current-source replacement for the historical 2026-08-13 render
checkpoint. It binds the permanent Meeting Record web and native UI to exact
source and Release-simulator hashes. It is not a production deployment receipt
and it does not claim that Apple has processed a new TestFlight artifact.

## Deterministic source fixture

- Server: `scripts/meeting-record-render-fixture.mjs`
- Loopback API: `http://127.0.0.1:3000`
- Account: synthetic AJ; no production records or customer data
- Records: one live/catching-up sitting, one 18-segment corrected record, and
  one unavailable/empty record
- Exact source state: corrected claims include segment ID, revision, speaker,
  time, and correction state; unavailable transcript state remains body-free
- The fixture serves the current repository frontend assets, rather than HTML
  fallbacks for imported JavaScript.

## Current-source native artifact

- Git HEAD: `8336ea6300bfc5bd4d7254b8fe41a5892fb24d57`
- Configuration: `Release-iphonesimulator`
- Artifact: `mobile/ios/build/release-meeting-record-current/Build/Products/Release-iphonesimulator/Stride.app`
- Bundle ID: `xyz.thebonfire.app`
- Embedded API and web base: `http://127.0.0.1:3000`
- iPhone 17 Pro: `EE7DD6DF-0F68-475B-B439-AF49BB80CCE3`, iOS 26.5
- iPad Pro 13-inch (M5): `8DF72850-970B-480F-A2CB-4FC0BA46BB85`, iOS 26.5
- Accessibility capture: `accessibility-extra-extra-extra-large`
- Exact executable and bundle hashes are in `CANDIDATE-MANIFEST.txt`.

The embedded Expo configuration declares iOS Build 63, while the checked-in
native simulator target stamps `CFBundleVersion` 62. These captures therefore
bind current UI/source bytes and the exact local simulator artifact; they are
not evidence of the separately completed EAS Build 63 or App Store processing.

## Captures and verified behavior

Web:

- `web/desktop-dark-current-record.png`: current live sitting honestly reports
  that analysis is catching up.
- `web/desktop-dark-corrected-record.png`: corrected record with grounded claim
  sections and accessible exact-source affordances.
- `web/desktop-dark-unavailable-record.png`: unavailable source state without
  invented recap, decisions, or commitments.
- `web/desktop-light-corrected-record.png`: the same corrected record in light
  appearance.
- `web/phone-390-dark-live-record.png`: live/catching-up record at 390×844.
- `web/phone-390-dark-corrected-record.png`: corrected claim layout at 390×844.
- `web/phone-390-dark-unavailable-record.png`: phone-width unavailable state.

Native:

- `native/iphone-meeting-record-list.png`: standard iPhone list.
- `native/iphone-corrected-meeting-record.png`: corrected iPhone detail.
- `native/iphone-light-unavailable-meeting-record.png`: unavailable iPhone
  detail in light appearance.
- `native/iphone-dark-axxxl-meeting-record-list.png`: AXXXL list; every card
  retains a readable title and places its status on a separate line.
- `native/iphone-dark-axxxl-corrected-meeting-record.png`: corrected detail at
  AXXXL.
- `native/ipad-meeting-record-list.png`: iPad list.
- `native/ipad-corrected-meeting-record.png`: corrected iPad detail.
- `native/ipad-exact-corrected-source.png`: exact corrected transcript source.

The iPad exact-source action was also checked through the accessibility tree:
focus moved to the exact row and announced the source as corrected with its
revision. The visible native list/detail controls expose current, corrected,
and unavailable states without relying on color alone.

## Capture procedure

1. Start the deterministic fixture on loopback and validate its identity,
   index, detail, corrected, and unavailable responses.
2. Open Meetings through the real Work navigation, not a hidden test route.
3. Exercise live, corrected, unavailable, light, dark, and 390px web states.
4. Build the current source as `Release-iphonesimulator` with the fixture URL
   embedded, then install that same `.app` on iPhone and iPad simulators.
5. Exercise list, corrected detail, exact-source focus, unavailable detail, and
   AXXXL Dynamic Type before capturing with `simctl`.
6. Hash every retained image, scoped source, executable, JavaScript bundle,
   embedded config, and Info.plist.
