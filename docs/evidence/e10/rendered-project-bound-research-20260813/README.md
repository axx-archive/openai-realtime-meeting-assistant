# Project-bound Research — bounded local candidate

> **Historical render checkpoint, not the current release candidate.** Later
> Project retirement, workstream-affinity, Meeting Record and chat-source fixes
> changed multiple files named in `CANDIDATE-MANIFEST.txt`. These captures bind
> only the earlier exact hashes in that manifest. They remain design evidence,
> but a final release claim requires a fresh render from the reviewed archive.
> See `../stride-e10-priority1-meeting-intelligence-local-20260813.json` for the
> current local source/test boundary.

Date: 2026-08-13
Repository HEAD: `9cfb43ed0d9c8f08df27429479ee5a44987a1b5d`
State: dirty, uncommitted, unreleased, not live

This evidence binds the current local Project-bound Research journey. The Git
HEAD alone does **not** identify these bytes; `CANDIDATE-MANIFEST.txt` hashes
the scoped source and test files, while `SHA256SUMS` hashes the rendered PNGs.

## Accepted bounded journey

- one confirmed Project-linked private conversation turn launches one
  server-owned Research workstream;
- the work card carries the exact Project title without exposing client-owned
  authority;
- provider admission, terminal publication, artifact reads/edits, rich Open,
  follow-up regeneration, Drive projection and share/blob routes re-check the
  exact current canonical Project association;
- a human edit and evidence-grade regeneration advance the same artifact's
  immutable revision lineage while preserving its Project binding;
- Save uses the exact body-free disposition receipt and settles to **Open in
  Drive**, which selects the exact saved Drive file;
- desktop exposes Open/Save/PDF/Regenerate in the work rail; compact web exposes
  Open/Open in Drive/Regenerate inside the expanded work card and returns focus
  to the normal composer with an explicit follow-up target;
- the native component test executes the same Project chip, Open, Save/Open in
  Drive and regenerate callbacks on the production work-card component;
- after source/Project invalidation, provider publication, edit, follow-up,
  artifact read and Drive projection all fail closed without changing the last
  accepted artifact.

## Captures

- `desktop-project-research-actions-dark.png` — completed Project work card and
  full desktop action rail.
- `phone-project-research-actions-light.png` — compact card with the equivalent
  Open/Open in Drive/Regenerate actions.
- `desktop-project-research-dark.png` — exact saved state and composer-bound
  regeneration target.
- `phone-project-research-light.png` — the same follow-up state at 390 px.

All identities and content in the harness are synthetic. No production data,
provider, Drive, release, TestFlight or deployment mutation was used.

## Reproduction

```sh
PROJECT_WORK_RENDER_DIR="$PWD/docs/evidence/e10/rendered-project-bound-research-20260813" \
  go test . -run '^TestProjectBoundResearchRenderedOpenDriveAndRegenerateJourney$' -count=1

go test . -run '^TestProjectBoundResearchCarriesExactCurrentAssociationAndFailsClosed$' -count=1
go test -race . -run '^TestProjectBoundResearchCarriesExactCurrentAssociationAndFailsClosed$' -count=1

cd mobile
node --import tsx --test src/__tests__/*.test.ts
npm run typecheck
```
