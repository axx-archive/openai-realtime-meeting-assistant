# STRIDE E10 Meeting Record rendered evidence — 2026-08-13

> **Historical render checkpoint, not the current release candidate.** After
> these captures, source-current, permanent-library, navigation, accessibility,
> meeting-recall and Priority-1 fixes changed multiple files named in
> `CANDIDATE-MANIFEST.txt`. The images remain valid evidence for the exact
> earlier hashes recorded there, but they must not be used to claim that the
> current dirty candidate was rendered or physically accepted. Rebuild and
> recapture from the final reviewed archive before release. The current local
> code/test boundary is recorded separately in
> `../stride-e10-priority1-meeting-intelligence-local-20260813.json`.

This directory is verification evidence for the permanent Meeting Record
checkpoint. It is not a deployment receipt and does not claim that the dirty
candidate is represented by Git `HEAD` alone.

`CANDIDATE-MANIFEST.txt` binds the reviewed Meeting Record source files to
their SHA-256 digests and separately binds the native Release executable,
JavaScript bundle, embedded API configuration, `Info.plist`, and build log.
`SHA256SUMS` binds every retained rendered PNG.

## Deterministic fixture

- Local app API: `http://127.0.0.1:3000`
- Synthetic account: `AJ`
- Records: one partial/gapped record, one empty/unavailable transcript, and one
  18-segment corrected record.
- The web captures were refreshed after restarting the server from the exact
  source hashes in `CANDIDATE-MANIFEST.txt`.
- No production records or customer data were used.

## Native artifact

- Configuration: `Release-iphonesimulator`
- Artifact: `mobile/ios/build/release-current/Build/Products/Release-iphonesimulator/Stride.app`
- Bundle id: `xyz.thebonfire.app`
- API base embedded in `EXConstants.bundle/app.config`: `http://127.0.0.1:3000`
- iPhone 17 Pro simulator: `EE7DD6DF-0F68-475B-B439-AF49BB80CCE3`, iOS 26.5
- iPad Pro 13-inch (M5) simulator: `8DF72850-970B-480F-A2CB-4FC0BA46BB85`, iOS 26.5
- Dynamic Type capture: `accessibility-extra-extra-large`
- The installed iPhone and iPad executable, `main.jsbundle`, and embedded API
  config hashes each matched the Release artifact hashes in
  `CANDIDATE-MANIFEST.txt` after capture.

## Captures

- `desktop-dark-corrected-record.png`: corrected desktop record.
- `desktop-dark-exact-corrected-source.png`: exact corrected source after
  programmatic focus; focused label included corrected state and revision.
- `desktop-dark-unavailable.png`: dark desktop unavailable/empty record.
- `desktop-light-empty.png`: light desktop empty record.
- `phone-dark-corrected-long.png`: long corrected record at 390×844.
- `phone-dark-exact-corrected-source.png`: exact focused source at 390×844;
  focused row was fully inside the viewport.
- `phone-dark-unavailable.png`: phone-web unavailable state.
- `iphone-dark-meeting-record-list.png`: final Release list at normal text size.
- `iphone-dark-corrected-record.png`: final Release corrected detail.
- `iphone-dark-exact-corrected-source.png`: final Release exact source focus.
- `iphone-dark-dynamic-type-meeting-record-list.png`: final Release list at
  accessibility-extra-extra-large, with title, outcome, badge, and status all
  retained.
- `iphone-dark-dynamic-type-exact-source.png`: final Release exact source at
  accessibility-extra-extra-large.
- `ipad-light-meeting-record-list.png`: final Release iPad list.
- `ipad-light-corrected-record.png`: final Release iPad corrected detail.
- `ipad-light-exact-corrected-source.png`: final Release iPad exact source.

## Capture procedure

1. Start the current Go source against a fresh synthetic Meeting/Board/memory
   fixture on loopback only.
2. Sign in as the synthetic AJ account.
3. Exercise list, corrected detail, exact source, empty/unavailable, phone-width,
   normal native, iPad, and accessibility Dynamic Type states.
4. For exact-source actions, assert the focused element is the exact transcript
   row and its accessible label includes source state and revision.
5. Build `Release-iphonesimulator`, install the same `.app` on both simulators,
   capture with `simctl`, and hash the executable/bundle/config/build log.
6. Hash every retained PNG and every scoped source/artifact listed in the
   candidate manifest.
