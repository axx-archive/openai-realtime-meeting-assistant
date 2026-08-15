# Canonical legacy lifecycle repair candidate

This receipt binds the local stopped-app repair foundation added after the
2026-08-14 STRIDE candidate freeze. It does not itself authorize or claim a
production repair.

## Exact reviewed files

- `canonical_legacy_repair.go` — SHA-256
  `eb0cf9713e8c14e857ee6a947ef8691599524c8519328448edd4fd41092547f3`
- `canonical_legacy_repair_test.go` — SHA-256
  `8e1b4212fd90a994a8669098bb787ab9e575af80609ca7e9e0e287a6da6a7911`
- `main.go` — SHA-256
  `c2765b4763240c1760b009e42dfd7eba3ca36a3dcf61e523009e397bdb6952c3`

## Accepted contract

The tool admits only sorted, target-only `tombstone_required` candidates in
the six observed legacy families: memory, artifact revision, notification,
file folder, file assignment, and board card. Two root-only observations must
be byte-sealed, exactly stable, and separated by 10 seconds to 15 minutes.
The release, tenant, database URL digest, complete import-input fingerprint,
database/proof/file seals, principals, projection replay, candidates, versions,
outbox, and high-water are bound into the manifest.

Mutation requires a root-only manifest, exact SHA-256, and a fresh exact-text
authority marker. Marker content and freshness are checked again at the
mutation seam. The lifecycle journal is replaced atomically with its sealed
prefix plus the complete planned batch, so a crash exposes either the prior
generation or the entire new generation. Exact recovery is bounded to that
batch and its original non-journal input fingerprint. Ordinary replay must
produce exactly two events, two outbox rows, and two version entries per
candidate, then converge to zero candidates with principal/projection parity
and a byte-stable second replay. The root-only receipt has a detached
self-digest and binds the final live state.

## Validation

- focused normal: PASS;
- focused race: PASS;
- real disposable-PostgreSQL exact-delta and second-replay test: PASS;
- six-family stable-observation, manifest tamper, atomic batch, logical partial,
  bounded recovery, stale-state, and existing-receipt adversarial tests: PASS;
- full main Go package: PASS (`619.951s`);
- `go vet .`: PASS;
- `git diff --check`: PASS;
- independent critic: PASS with no P0-P2 findings on the exact hashes above.

Production execution remains a separate ceremony requiring an exact release,
stopped application writers, matched data/PostgreSQL backup, encrypted off-host
copy, isolated restore proof, fresh live observations and authority marker,
zero-candidate restart proof, complete outbox drain, a distinct pre-migration
backup, migration 24 while replay remains off, and retained rollback identity.
