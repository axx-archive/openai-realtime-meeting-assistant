# bonfireOS HA/DR restore authority runbook

This runbook covers the fail-closed repository foundation only. It does not provision managed PostgreSQL, object-lock storage, cross-region replicas, or paid services. It must be exercised in an isolated restore environment before any restored app is allowed to answer reads.

Only the v2 `secure-*` path can produce a receipt accepted by application restore mode. The older HMAC `authority-append`, `manifest-create`, and `preflight` commands remain compatibility tools for local drills; their output cannot unlock a restored application and is not production evidence.

## Required trust split

Production uses distinct Ed25519 identities for `authority`, `manifest`, `restore_receipt`, `offsite_provider`, `custody`, `encryption`, and `release`. Public verifiers may be installed on the isolated restore host. Private keys stay with their named issuers; in particular, the restore host never receives the authority, manifest, provider, custody, encryption, or release private keys. Reusing a key ID across roles is refused.

Each key is configured as `BONFIRE_DR_<ROLE>_PUBLIC_KEY_ID` plus `BONFIRE_DR_<ROLE>_PUBLIC_KEY`; commands that sign use the corresponding `PRIVATE_KEY_ID` and `PRIVATE_KEY`. Role names are uppercase forms of the names above. Keys are raw Ed25519 public keys or private seeds/keys encoded as base64 or hex. The external authority adapter mounts the current head digest as a small read-only regular file and sets `BONFIRE_DR_AUTHORITY_HEAD_PIN_PATH`; secure commands use descriptor-relative no-follow traversal and refuse a symlink in any path component, replacement during open, malformed digest, or mismatch with their spec. `BONFIRE_DR_PROTECTED_ROOTS_PATH` names a JSON file with exactly the four volume-name-to-absolute-path mappings. Configured and requested paths must match literally after absolute-path cleaning; symlink aliases are not normalized into acceptance.

## Current production boundary

The existing nightly worker archives `/app/data`. That includes `meeting_data`, render jobs stored beneath it, and the nested `usage_ledger` mount as seen by the app container. It does not create a transaction-consistent snapshot of `digitalocean_canonical_postgres`, and it cannot see the external `digitalocean_codex_queue` mounted at `/app/codex-queue`. Its local ring is inside the same Droplet volume, offsite upload is optional, and its restore-verification status is process memory rather than durable custody evidence.

A DR-qualified backup set therefore has exactly four independently identified artifacts:

| Manifest name | Production source | Required evidence |
|---|---|---|
| `canonical_postgres` | `digitalocean_canonical_postgres` or a transaction-consistent `pg_dump` | provider snapshot/export ID, byte size, SHA-256 |
| `meeting_data` | `digitalocean_meeting_data` | provider snapshot/archive ID, byte size, SHA-256 |
| `codex_queue` | `digitalocean_codex_queue` | provider snapshot/archive ID, byte size, SHA-256 |
| `usage_ledger` | `digitalocean_usage_ledger` | provider snapshot/archive ID, byte size, SHA-256 |

The purge authority is a fifth, separate object. It must not be stored beneath any of those four roots or bundled into their snapshots.

## Independent purge authority

Create a distinct storage location owned by operations. Production requires a separately credentialed, object-lock/versioned authority-head object and an external compare-and-swap pointer to its current SHA-256. A local path is suitable only for drills. Advance the signed head only after independently checking the purge export and the preceding externally pinned head:

```bash
go run ./cmd/bonfire-dr authority-head-advance \
  --spec authority-head-advance.json \
  --database-url "$AUTHORITY_DATABASE_URL" \
  --out authority-head.json
```

The command enumerates tenants across the reviewed registry of every canonical table containing `tenant_id`, verifies that registry against `information_schema`, and derives each tenant's purge high-water and digest from a stable ordered read of `purge_ledger` in the same repeatable-read transaction. The signed head stores every tenant boundary. An advance must prove the exact signed purge prefix for every tenant in the preceding head; it may add newly discovered tenants, but it cannot drop an old tenant. The export used for hashing includes destruction evidence in the digest, but the authority contains only its SHA-256 and row-count high-water.

`authority-head-advance.json` contains the preceding signed head, its externally pinned SHA-256, the new purge-record SHA-256, and `recordedAt`. The command verifies the preceding Ed25519 signature and pin, advances exactly one sequence, and links the prior head digest. Publish the new head with provider object lock and compare-and-swap the external current-head pointer. A locally valid old prefix is never sufficient: restore preflight requires the independently supplied current-head SHA-256.

## Capture a complete backup manifest

1. Quiesce writes and external dispatch. Let already-sent jobs reconcile to terminal or `ambiguous`; an ambiguous side effect blocks the backup gate.
2. Capture all four artifacts from one named backup window. PostgreSQL must use a transaction-consistent export or provider snapshot.
3. Encrypt the complete offsite object using AES-256-GCM. Keep the key in a separate secrets system; manifests contain only its key ID.
4. Record the offsite provider receipt/version, encrypted-object SHA-256, custody receipt, and release Git commit.
5. Obtain independently signed, time-bounded `ExternalEvidence` statements from the offsite provider, custody owner, and encryption/KMS owner. The provider and encryption subjects bind the encrypted envelope SHA-256; custody binds the four-artifact set SHA-256.
6. Create a signed release attestation that binds the Git commit, immutable OCI image digest, opened running-binary bytes, opened source-archive bytes, issue time, and expiry. Its validity window cannot exceed 30 days. Mount the registry-resolved digest at `BONFIRE_RUNNING_IMAGE_DIGEST_PIN_PATH`, then run `go run ./cmd/bonfire-dr release-attest --spec release-attest.json --out release-attestation.json`; the CLI hashes both files itself.
7. Build `backup-spec.json` using `secureManifestCreateSpec`. It names real local snapshot/export paths, the local encrypted-envelope path, the release attestation, all three independent evidence statements, the current externally pinned authority-head digest, and a capture barrier. The CLI hashes the envelope and requires it to match both provider and encryption evidence. The barrier includes a write-fence digest, PostgreSQL LSN, meeting/queue/usage high-waters, and start/end timestamps.
8. Sign the manifest. The CLI opens and hashes the four artifact paths itself and, in one read-only repeatable-read transaction inside the declared barrier, computes both the complete logical PostgreSQL digest and every tenant's ordered purge boundary. The logical digest is part of the signed manifest payload. Capture is accepted only when PostgreSQL is exactly at every tenant boundary in the signed current authority head:

```bash
go run ./cmd/bonfire-dr secure-manifest-create \
  --spec backup-spec.json \
  --database-url "$CAPTURE_DATABASE_URL" \
  --out backup-manifest.json
```

Manifest creation rejects symlinked or non-regular artifact members, replacement during open, missing/duplicate volumes or tenants, reused role keys, invalid/expired independent evidence, mixed envelope digests, an incomplete capture barrier, or an unsigned release. The operator must still freeze writes and capture the four sources inside the declared barrier; the manifest makes that boundary verifiable but does not itself stop application writers.

## Restore drill and readiness gate

1. Keep the restored application stopped and network-isolated.
2. Download the exact offsite object version named by the manifest. Verify its SHA-256 before decryption.
3. Restore each artifact into a staging root. Put only its volume, provider snapshot ID, real path, and the exact downloaded encrypted-envelope path in `restore-preflight.json`; the CLI recomputes every byte digest and size itself.
4. Point `--database-url` at the isolated restored PostgreSQL. In one read-only repeatable-read snapshot, the CLI enumerates all tenants from every registered tenant-bearing table, hashes every ordered purge row, and recomputes the complete logical database digest. That digest includes the registered table set, every column's ordered schema metadata, schema-migration rows, and every row of every canonical table encoded as canonical JSON and sorted with the PostgreSQL `C` collation. Both the complete-table and tenant-table registries are checked against `information_schema`, and migration tests enforce the same registries. The restored digest must exactly match the capture digest signed into the manifest; a database mutated at any point after capture is not eligible for preflight. Each tenant's purge prefix must also match the signed authority boundary.
5. Use the exact immutable OCI digest and release attestation named by the manifest. Set `runningBinaryPath` to the binary that the restored image will run.
6. Generate a fresh random nonce of at least 32 bytes, a unique restore-environment ID, and a receipt expiry no more than one hour after evaluation. Run preflight and persist its body-free receipt:

```bash
go run ./cmd/bonfire-dr secure-preflight \
  --manifest backup-manifest.json \
  --spec restore-preflight.json \
  --database-url "$RESTORED_DATABASE_URL" \
  --receipt-out drill-receipt.json
```

The command exits nonzero and writes `ready:false` when any required evidence is absent or changed. In particular, an older authority head, changed artifact byte, missing tenant, forged or stale external statement, copied candidate field, or release/image/binary mismatch is refused. The foundation does not silently replay purges into an old database; that future operation requires an independently verified purge-event replication source.

7. Inspect `drill-receipt.json`. It contains only identifiers and digests, failure codes, and a signature. It contains no transcript, artifact, filename, prompt, recipient, or destruction-evidence body.
8. Use only `docker-compose.restore.yml` on the isolated host. It installs the fixed `bonfire-restore-profile-v1` marker read-only at `/run/bonfire-dr/restore-profile-v1`, has no public ports or proxy, and sets `BONFIRE_RESTORE_MODE=isolated`. The application refuses startup if that marker is present while restore mode is off or omitted, or if isolated mode lacks the marker.
9. Configure `BONFIRE_RESTORE_RECEIPT_PATH`, `BONFIRE_RESTORE_MANIFEST_PATH`, `BONFIRE_RESTORE_ENVELOPE_PATH`, `BONFIRE_RESTORE_ENVIRONMENT_ID`, `BONFIRE_RESTORE_NONCE`, `BONFIRE_RUNNING_IMAGE_DIGEST_PIN_PATH`, `BONFIRE_DR_AUTHORITY_HEAD_PIN_PATH`, `BONFIRE_DR_PROTECTED_ROOTS_PATH`, all seven public verification identities, and `BONFIRE_RESTORE_RECEIPT_CONSUMED_PATH`. The authority and OCI digest pins are read-only mounts from independent runtime adapters and contain lowercase SHA-256 values without the `sha256:` prefix. The consumed marker must be on fresh environment-owned storage, outside all four restored roots.
10. Restore-mode startup does not trust an environment-supplied candidate digest. Before opening durable stores it reopens and hashes all four configured roots, the encrypted envelope, and its own executable; recomputes tenant boundaries and the complete logical database digest from a fresh repeatable-read snapshot; revalidates the signed manifest, current external authority pin, OCI pin, embedded release, and still-fresh release attestation; then derives the candidate digest and checks the signed receipt's separately bound database digest. Any non-purge row or schema mutation after preflight changes that digest and refuses boot. Receipt verification independently rejects issuance windows over one hour, even when such a receipt has a valid signature. A successful boot atomically consumes the receipt; a second boot requires a fresh nonce and receipt. `/readyz` exposes the restore-gate state. Application health or a readable homepage is not restore proof.

## Rollback and tamper response

- Never solve `purge_rollback` by lowering or replacing the independent authority. Select a backup at or beyond the current purge high-water, or apply a separately retained verified purge stream in a future controlled procedure.
- `purge_mismatch` means the same claimed boundary has different bytes or the manifest is not bound to the current authority record. Quarantine the set.
- `manifest_invalid`, `snapshot_mismatch`, or `authority_invalid` is a custody/tamper failure. Do not decrypt further or start the app.
- `release_mismatch` requires the named immutable release; it is not waived by a successful build.
- Missing, expired, same-key, or invalidly signed encryption, custody, offsite, release, or manifest evidence remains a failed drill even when local bytes look intact.

## Remaining external HA/DR gates

- Managed PostgreSQL multi-node HA/failover is not provisioned.
- No cross-region replica or immutable/versioned object-lock bucket has been configured.
- No dedicated purge-authority volume, independent credential, signing-key custody process, or offsite replication job has been installed.
- The current backup worker does not orchestrate a consistent four-volume backup window or capture PostgreSQL/codex-queue artifacts.
- No production restore host, DNS cutover, media/TURN failover, RPO/RTO measurement, or operator paging drill exists.
- Encryption-key recovery and dual-control custody are operational requirements, not proved by repository tests.
