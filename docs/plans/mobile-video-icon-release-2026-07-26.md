# Mobile Video And Icon Release - Execution Ledger

Goal and source pointers: ship the verified mobile framing/SFU quality repair and approved STRIDE momentum icon to GitHub, the live VPS, and TestFlight.
Current phase: release preparation

## Invariants

- Preserve the production data volume `digitalocean_meeting_data`; never sync local or stale `data/`.
- Commit only the media, release, and approved icon work; preserve unrelated strategy and prototype files.
- The VPS, Git revision, and TestFlight candidate must derive from the same committed source.
- Wide Upright is capability-gated and automatic; Center Stage remains independent; unsupported phones retain the desktop equal-fill fallback.

## Wave Map

| Wave | Outcome | Dependencies | Gate / rollback | Status |
| --- | --- | --- | --- | --- |
| 1 | Replace remaining legacy brand marks and reserve build 16 | Approved icon master | Rendered mobile/web inspection; revert scoped UI/assets | Complete |
| 2 | Validate and publish exact source revision | Wave 1 | Full Go/mobile/native release gates; Git fast-forward | In progress |
| 3 | Deploy exact revision to VPS | Wave 2 | Timestamped backup; healthy containers and live asset/media checks | Pending |
| 4 | Build, inspect, and upload build 16 to TestFlight | Wave 2 | Exact IPA metadata, entitlements, compiled icon, EAS submission and App Store processing | Pending |

## Current Wave

- Writable scope: media/SFU files already changed, mobile brand component/navigation/tests, approved icon assets, web PWA/brand assets, release metadata, and this ledger.
- Excluded: `data/`, `stride-site/`, the proactive-workflow design, loose icon concepts, and unrelated root artifacts.
- Completion evidence: rendered mobile tab/login/home mark plus web rail/login mark; no legacy Bonfire silhouette on brand surfaces.

## Completed Evidence

- Physical iPhone matrix passed all four Center Stage/Wide Upright combinations with continuous frames and self-preview.
- Fixed SFU relayed Wide Upright as 1280x720 at 30 fps; full Go and 104 mobile tests passed before release preparation.
- EAS build 15 already exists, so this release reserves build 16.
- Mobile Home, login, and home-screen branding now share the approved momentum assets; the Home tab uses its tintable silhouette.
- Web rail and sign-in render the approved icon, and the regenerated iOS/iPadOS/macOS Apple catalogs derive from the same master.
- Rendered web sign-in inspection passed; 104 mobile tests, TypeScript, targeted Go branding/media tests, and 69 native Apple readiness checks are green.
- The complete `go test ./...` suite passes after the brand selector migration.
- Expo Doctor passes 20/20 checks, the production iOS bundle export succeeds, and a clean-prebuilt Release app compiles with the ReplayKit extension and Expo Image.
- Simulator inspection confirms build 16, the new compiled 120px app icon, the full-color Home mark, and the monochrome momentum glyph in the main tab.

## Operations And Authority Queue

- Git commit and push: authorized by the user.
- VPS backup, sync, rebuild, restart, and verification: authorized by the user.
- EAS production build and TestFlight submission: authorized by the user.

## Risks And Decisions

- `axx/main` is an ancestor of this branch, so publication can remain fast-forward.
- A successful EAS upload is not release proof; inspect the exact IPA and wait for TestFlight processing evidence.

## Resume Here

- Inspect and stage only the approved release scope, then commit and fast-forward `axx/main`.
