# W4 HA/DR foundation - execution ledger

Goal and source pointers: bonfireOS 2.0 W4; `internal/dr`, `cmd/bonfire-dr`, and `docs/bonfire-dr-restore-runbook.md`.

Current phase: repository foundation passed its independent adversarial re-gate. Production infrastructure, custody assignment, and the real restore/failover drill remain pending.

## Invariants

- Restored content, PostgreSQL, queue, and usage snapshots can never roll back the independent purge authority.
- Readiness is denied on missing, mismatched, rolled-back, or tampered evidence.
- No restore drill receipt contains user bodies or destruction-evidence bodies.
- Deployment profiles and live volumes remain unchanged until a separately authorized operations window.

## Wave map

| Wave | Outcome | Dependencies | Gate / rollback | Status |
|---|---|---|---|---|
| Foundation | Externally pinned asymmetric authority head, byte-derived complete backup manifest, independent evidence, restore preflight and boot gate, CLI, tests, runbook | None | Deterministic normal/race/vet plus rollback, forgery, replay, symlink, tenant, byte, and image negatives | Complete; independent PASS |
| Infrastructure | Managed HA PostgreSQL, independent authority custody, encrypted object-lock offsite storage | Cost approval and credentials | Provider failover plus custody receipts; no app cutover on failure | Pending |
| Production drill | Four-volume capture and isolated restore on immutable release | Infrastructure | Signed `ready:true` receipt, measured RPO/RTO, purge rollback canary | Pending |
| Availability cutover | App/media redundancy, health routing, operator alerting | Successful drill | Reversible traffic shift; old host retained until soak | Pending |

## Current wave

- Current writable scope is limited to installing the fail-closed repository foundation on the primary VPS. Provisioning paid HA/offsite services, assigning independent custody, and running an isolated restore drill remain separate operational work.

## Completed evidence

- V2 secure evidence uses descriptor-relative no-follow traversal, derives hashes from opened artifacts rather than candidate JSON, and rejects symlinked path components, non-regular members, inode replacement, and directory mutation during traversal.
- Distinct Ed25519 role keys cover authority, manifest, receipt, provider, custody, encryption, and OCI release evidence; restore hosts receive public verifiers only.
- The externally retained authority-head SHA-256 prevents a locally valid truncated prefix from becoming current; the signed head binds every tenant's purge high-water and ordered-prefix digest.
- The manifest binds a coherent four-volume capture barrier, the complete logical PostgreSQL digest computed inside that barrier, and every tenant enumerated from exhaustive schema-checked canonical registries. Preflight and boot require the live restored digest to match that signed capture digest exactly.
- Restore receipts bind environment, nonce, candidate digest, complete logical database digest, authority head, image digest, embedded release, and a verifier-enforced one-hour maximum validity window. The logical digest covers the exhaustive canonical table/column/row registry and schema migrations in the same repeatable-read snapshot as purge verification. The restore-only deployment carries a fixed read-only marker and exposes no public ports. Restore-mode boot recomputes the candidate from current opened bytes, complete database state, pins, and its executable, then atomically consumes the receipt before opening durable stores.
- The final independent re-gate returned PASS after focused normal/race tests, `go vet ./...`, and `git diff --check`; no production or Git mutation was part of that review.

## Operations and authority queue

- Recurring managed HA/offsite spend: not authorized in this slice.
- Production volume snapshots, VPS changes, credentials, and live restore: not authorized.
- Independent signing-key and encryption-key custody owners: unassigned.

## Resume here

Ship the repository foundation default-off on the primary VPS. Then obtain cost/credential/custody approval and provision the infrastructure wave without weakening the four-volume, tenant-coverage, authority-head, independent-evidence, release-attestation, or boot-gate contracts.
