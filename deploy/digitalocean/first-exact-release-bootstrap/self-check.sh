#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
export PATH=/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)

for script in \
  "$SCRIPT_DIR/prepare-local.sh" \
  "$SCRIPT_DIR/mac-public-probe.sh" \
  "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.sh" \
  "$SCRIPT_DIR/vps-common.sh" \
  "$SCRIPT_DIR/vps-bootstrap.sh" \
  "$SCRIPT_DIR/vps-rollback-legacy.sh" \
  "$SCRIPT_DIR/self-check.sh"; do
  bash -n "$script"
done

(
  # Source the operator driver as a function library. These checks exercise
  # release payload contracts and backup tamper detection without touching a
  # network, Docker, Git, or production state.
  # shellcheck source=vps-bootstrap.sh
  source "$SCRIPT_DIR/vps-bootstrap.sh"

  require_sha256 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  ! (require_sha256 0123456789abcdef0123456789abcdef01234567) 2>/dev/null

  topology_fixture=$(mktemp -d)
  trap 'rm -rf "$topology_fixture"' EXIT
  jq -n '
    def service($name; $mounts):
      {Config:{Labels:{"com.docker.compose.service":$name}},Mounts:$mounts};
    def volume($name; $destination):
      {Type:"volume",Name:$name,Destination:$destination,RW:true};
    [
      service("caddy"; [volume("digitalocean_caddy_config"; "/config"), volume("digitalocean_caddy_data"; "/data")]),
      service("canonical-postgres"; [volume("digitalocean_canonical_postgres"; "/var/lib/postgresql/data")]),
      service("codex-runner"; [volume("digitalocean_codex_queue"; "/app/codex-queue"), volume("digitalocean_codex_runner_data"; "/runner-data"), volume("digitalocean_usage_ledger"; "/app/usage-ledger")]),
      service("coturn"; [volume("anonymous-coturn-data"; "/var/lib/coturn")]),
      service("meetingassist"; [volume("digitalocean_codex_queue"; "/app/codex-queue"), volume("digitalocean_meeting_data"; "/app/data"), volume("digitalocean_usage_ledger"; "/app/data/usage")]),
      service("render-runner"; [volume("digitalocean_meeting_data"; "/app/data")])
    ]
  ' >"$topology_fixture/exact.json"
  assert_exact_legacy_container_topology_snapshot "$topology_fixture/exact.json"
  jq '.[0].Mounts[0].RW=false' "$topology_fixture/exact.json" >"$topology_fixture/drift.json"
  ! (assert_exact_legacy_container_topology_snapshot "$topology_fixture/drift.json") 2>/dev/null
  rm -rf "$topology_fixture"

  receipt_fixture=$(mktemp -d)
  trap 'rm -rf "$receipt_fixture"' EXIT
  receipt_release=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  receipt_authority=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
  jq -n '
    {schema:"bonfire.canonical-board-repair.v2",releaseCommit:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
     cloneId:"self-check-clone",environment:"isolated_cold_clone",qualificationRun:true,
     candidateSetSha256:"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
     terminalCandidateSha256:"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
     candidates:[range(0;7)|{objectId:("object-"+(.|tostring)),stateSha256:"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",evidenceBasis:"done_archive_absence"}]}
  ' >"$receipt_fixture/manifest.json"
  receipt_manifest=$(sha256sum "$receipt_fixture/manifest.json" | awk '{print $1}')
  write_repair_receipt_fixture() {
    local output=$1 first=$2 zero=$3 parity=$4 replay=$5
    local empty_candidates_sha
    empty_candidates_sha=$(printf '[]' | sha256sum | awk '{print $1}')
    jq -n \
      --arg release "$receipt_release" --arg manifest_sha "$receipt_manifest" --arg authority "$receipt_authority" \
      --arg empty_candidates_sha "$empty_candidates_sha" --slurpfile manifest "$receipt_fixture/manifest.json" \
      --argjson first "$first" --argjson zero "$zero" \
      --argjson parity "$parity" --argjson replay "$replay" '
        def seal($sha): {size:1,sha256:$sha};
        {schema:"bonfire.canonical-repair-receipt.v1",status:"complete",releaseCommit:$release,version:$release,tenantId:"bonfire",
         cloneId:"self-check-clone",environment:"isolated_cold_clone",qualificationRun:true,
         candidateManifestSha256:$manifest_sha,authorityMarkerSha256:$authority,
         before:{eventHighWater:102,captureSpoolHighWater:100},
         after:{eventHighWater:109,captureSpoolHighWater:100},
         beforeState:{tenantEventCount:102,eventHighWater:102,importOutboxCount:50,versionEntryCount:60,
           versionEntriesSha256:"4444444444444444444444444444444444444444444444444444444444444444",captureSpoolHighWater:100,
           board:seal("1111111111111111111111111111111111111111111111111111111111111111"),
           journal:seal("2222222222222222222222222222222222222222222222222222222222222222"),
           versionMap:seal("4444444444444444444444444444444444444444444444444444444444444444"),
           spool:seal("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
           databaseSha256:"6666666666666666666666666666666666666666666666666666666666666666",
           importInputSha256:"abababababababababababababababababababababababababababababababab",
           proofSha256:"edededededededededededededededededededededededededededededededed",
           candidateCount:7,candidateSha256:"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
         afterState:{tenantEventCount:109,eventHighWater:109,importOutboxCount:57,versionEntryCount:67,
           versionEntriesSha256:"5555555555555555555555555555555555555555555555555555555555555555",captureSpoolHighWater:100,
           board:seal("1111111111111111111111111111111111111111111111111111111111111111"),
           journal:seal("3333333333333333333333333333333333333333333333333333333333333333"),
           versionMap:seal("5555555555555555555555555555555555555555555555555555555555555555"),
           spool:seal("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
           databaseSha256:"8888888888888888888888888888888888888888888888888888888888888888",
           importInputSha256:"abababababababababababababababababababababababababababababababab",
           proofSha256:"9999999999999999999999999999999999999999999999999999999999999999",
           candidateCount:0,candidateSha256:$empty_candidates_sha},
         delta:{tenantEvents:7,importOutbox:7,versionEntries:7},
         journalAppendedRecords:[$manifest[0].candidates[]|{family:"board_card",object_id:.objectId,state_sha256:.stateSha256,
           at:"2026-08-02T10:00:00Z",reason:"legacy_reconciliation_source_absence_backfill_v1",evidence_basis:.evidenceBasis}],
         candidateCount:7,candidateFingerprintSha256:"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
         appliedCount:7,firstAppendObserved:$first,zeroCandidates:$zero,
         principalParity:$parity,projectionParity:$parity,idempotentSecondReplay:$replay,
         boardSha256:"1111111111111111111111111111111111111111111111111111111111111111",
         journalBeforeSha256:"2222222222222222222222222222222222222222222222222222222222222222",
         journalAfterSha256:"3333333333333333333333333333333333333333333333333333333333333333",
         versionMapBeforeSha256:"4444444444444444444444444444444444444444444444444444444444444444",
         versionMapAfterSha256:"5555555555555555555555555555555555555555555555555555555555555555",
         databaseBeforeSha256:"6666666666666666666666666666666666666666666666666666666666666666",
         databaseAfterSha256:"8888888888888888888888888888888888888888888888888888888888888888",
         beforeCandidateSha256:"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
         afterCandidateSha256:$empty_candidates_sha,
         afterFingerprintSha256:"9999999999999999999999999999999999999999999999999999999999999999",
         finalParitySha256:"7777777777777777777777777777777777777777777777777777777777777777",
         completedAt:"2026-08-02T10:00:00Z",receiptSha256:"pending"}
      ' >"$output"
    local digest
    digest=$(canonical_repair_receipt_payload_sha256 "$output")
    jq --arg digest "$digest" '.receiptSha256=$digest' "$output" >"$output.tmp"
    mv "$output.tmp" "$output"
  }
  write_repair_receipt_fixture "$receipt_fixture/exact.json" true true true true
  validate_canonical_repair_receipt_payload "$receipt_fixture/exact.json" "$receipt_release" "$receipt_manifest" "$receipt_authority" "$receipt_fixture/manifest.json"
  ! (validate_canonical_repair_receipt_payload "$receipt_fixture/missing.json" "$receipt_release" "$receipt_manifest" "$receipt_authority" "$receipt_fixture/manifest.json") 2>/dev/null
  ! (validate_canonical_repair_receipt_payload "$receipt_fixture/exact.json" "$receipt_release" "${receipt_manifest:0:63}e" "$receipt_authority" "$receipt_fixture/manifest.json") 2>/dev/null
  write_repair_receipt_fixture "$receipt_fixture/drift.json" true true true true
  jq '.after.captureSpoolHighWater=101' "$receipt_fixture/drift.json" >"$receipt_fixture/drift.tmp" && mv "$receipt_fixture/drift.tmp" "$receipt_fixture/drift.json"
  ! (validate_canonical_repair_receipt_payload "$receipt_fixture/drift.json" "$receipt_release" "$receipt_manifest" "$receipt_authority" "$receipt_fixture/manifest.json") 2>/dev/null
  write_repair_receipt_fixture "$receipt_fixture/partial.json" true false false false
  ! (validate_canonical_repair_receipt_payload "$receipt_fixture/partial.json" "$receipt_release" "$receipt_manifest" "$receipt_authority" "$receipt_fixture/manifest.json") 2>/dev/null
  printf '%s\n' tamper >>"$receipt_fixture/exact.json"
  ! (validate_canonical_repair_receipt_payload "$receipt_fixture/exact.json" "$receipt_release" "$receipt_manifest" "$receipt_authority" "$receipt_fixture/manifest.json") 2>/dev/null

  REPAIR_MANIFEST_SHA=$receipt_manifest
  test "$(canonical_repair_authority_text)" = "CONFIRM CANONICAL BOARD REPAIR $receipt_manifest"

  original_state_dir=$STATE_DIR
  STATE_DIR=$(mktemp -d)
  assert_forward_ceremony_permitted
  for terminal in public-open-attempted ceremony-retired legacy-restored legacy-reopened; do
    : >"$STATE_DIR/phase-$terminal"
    ! (assert_forward_ceremony_permitted) 2>/dev/null
    rm "$STATE_DIR/phase-$terminal"
  done
  assert_restart_untouched_phase_boundary
  for changed in canonical-normalization-setup-started canonical-normalization-started canonical-normalized \
    canonical-manifest-generation-started repair-manifest-generated legacy-retirement-started \
    canonical-repair-execution-started canonical-repair-failed canonical-repaired; do
    : >"$STATE_DIR/phase-$changed"
    ! (assert_restart_untouched_phase_boundary) 2>/dev/null
    rm "$STATE_DIR/phase-$changed"
  done
  rm -rf "$STATE_DIR"
  STATE_DIR=$original_state_dir

  printf '%s' '{"email":"aj@example.com","name":"AJ"}' | validate_authenticated_smoke_payload auth/me
  ! printf '%s' '{"error":"not signed in"}' | validate_authenticated_smoke_payload auth/me
  printf '%s' '{"ok":true,"rooms":[{"id":"office"}]}' | validate_authenticated_smoke_payload rooms
  ! printf '%s' '{}' | validate_authenticated_smoke_payload rooms
  printf '%s' '{"ok":true,"threads":[{"id":"team","table":true}]}' | validate_authenticated_smoke_payload assistant/chat-threads
  ! printf '%s' '{}' | validate_authenticated_smoke_payload assistant/chat-threads
  printf '%s' '{"ok":true,"board":{"cards":[]}}' | validate_authenticated_smoke_payload assistant/board
  ! printf '%s' '{}' | validate_authenticated_smoke_payload assistant/board
  printf '%s' '{"ok":true,"files":[],"folders":[]}' | validate_authenticated_smoke_payload assistant/files
  ! printf '%s' '{}' | validate_authenticated_smoke_payload assistant/files

  migration_fixture=$(mktemp -d)
  mkdir -p "$migration_fixture/root/migrations" "$migration_fixture/release"
  migration_names=(
    0001_canonical.sql
    0002_approval_repository.sql
    0003_purge_ledger.sql
    0004_brain_projection_checkpoints.sql
    0005_purge_ledger_object_type.sql
    0006_brain_projection_work.sql
    0007_catch_up_publications.sql
    0008_stride_contracts.sql
    0009_stride_conversation_ledger.sql
  )
  for migration in "${migration_names[@]}"; do
    printf '%s\n' "exact-$migration" >"$migration_fixture/root/migrations/$migration"
  done
  tar -cf "$migration_fixture/release/source.tar" -C "$migration_fixture/root" migrations
  test ! -e "$migration_fixture/release/sealed-candidate"
  migration_archive_hashes "$migration_fixture/release/source.tar" >"$migration_fixture/exact.tsv"
  test "$(wc -l <"$migration_fixture/exact.tsv")" -eq 9
  assert_migration_hash_rows "$migration_fixture/release/source.tar" "$migration_fixture/exact.tsv"
  cp "$migration_fixture/exact.tsv" "$migration_fixture/wrong.tsv"
  awk -F '\t' 'BEGIN{OFS="\t"} NR==9{$2="ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"} {print}' \
    "$migration_fixture/exact.tsv" >"$migration_fixture/wrong.tsv"
  ! (assert_migration_hash_rows "$migration_fixture/release/source.tar" "$migration_fixture/wrong.tsv") >/dev/null 2>&1

  rm "$migration_fixture/root/migrations/0009_stride_conversation_ledger.sql"
  tar -cf "$migration_fixture/missing.tar" -C "$migration_fixture/root" migrations
  ! (migration_archive_hashes "$migration_fixture/missing.tar") >/dev/null 2>&1
  printf '%s\n' exact-0009_stride_conversation_ledger.sql >"$migration_fixture/root/migrations/0009_stride_conversation_ledger.sql"
  printf '%s\n' exact-extra >"$migration_fixture/root/migrations/0010_extra.sql"
  tar -cf "$migration_fixture/extra.tar" -C "$migration_fixture/root" migrations
  ! (migration_archive_hashes "$migration_fixture/extra.tar") >/dev/null 2>&1
  rm "$migration_fixture/root/migrations/0010_extra.sql"
  rm "$migration_fixture/root/migrations/0009_stride_conversation_ledger.sql"
  ln -s 0008_stride_contracts.sql "$migration_fixture/root/migrations/0009_stride_conversation_ledger.sql"
  tar -cf "$migration_fixture/symlink.tar" -C "$migration_fixture/root" migrations
  ! (migration_archive_hashes "$migration_fixture/symlink.tar") >/dev/null 2>&1

  archive_hash=$(sha256sum "$migration_fixture/release/source.tar" | awk '{print $1}')
  jq -n --arg hash "$archive_hash" \
    '{schema:"bonfire.release-source.v3",releaseCommit:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",sourceArchiveSha256:$hash}' \
    >"$migration_fixture/release/source-receipt.json"
  source_receipt_hash=$(sha256sum "$migration_fixture/release/source-receipt.json" | awk '{print $1}')
  jq -n --arg source_hash "$source_receipt_hash" --slurpfile source "$migration_fixture/release/source-receipt.json" \
    '{schema:"bonfire.release-receipt.v3",sourceReceiptSha256:$source_hash,source:$source[0],buildManifest:{source:$source[0]}}' \
    >"$migration_fixture/release/release-receipt.json"
  assert_release_source_archive_binding "$migration_fixture/release"
  printf '%s\n' tampered >>"$migration_fixture/release/source.tar"
  ! (assert_release_source_archive_binding "$migration_fixture/release") 2>/dev/null
  rm -rf "$migration_fixture"

  backup_fixture=$(mktemp -d)
  trap 'rm -rf "$backup_fixture"' EXIT
  mkdir "$backup_fixture/meta"
  for file in postgres.pgcustom postgres.list migrations-before.tsv table-counts-before.tsv; do
    printf 'exact-%s\n' "$file" >"$backup_fixture/$file"
  done
  printf '%s\n' nested >"$backup_fixture/meta/nested.txt"
  write_backup_checksum_manifest "$backup_fixture"
  for file in postgres.pgcustom postgres.list migrations-before.tsv table-counts-before.tsv; do
    cp "$backup_fixture/$file" "$backup_fixture/$file.clean"
    printf '%s\n' tampered >>"$backup_fixture/$file"
    ! (cd "$backup_fixture" && sha256sum -c backup-SHA256SUMS >/dev/null 2>&1)
    mv "$backup_fixture/$file.clean" "$backup_fixture/$file"
    (cd "$backup_fixture" && sha256sum -c backup-SHA256SUMS >/dev/null)
  done

  # The exact checkpoint contract accepts only a plan-add B and rejects both
  # a dirty operator pack and an otherwise excluded mobile/code change.
  # shellcheck source=prepare-local.sh
  source "$SCRIPT_DIR/prepare-local.sh"
  git_fixture=$(mktemp -d)
  trap 'rm -rf "$backup_fixture" "$git_fixture"' EXIT
  git -C "$git_fixture" init -q
  git -C "$git_fixture" config user.name 'Bootstrap self-check'
  git -C "$git_fixture" config user.email bootstrap@example.invalid
  mkdir -p "$git_fixture/$PACK_REL" "$git_fixture/docs/plans" "$git_fixture/mobile"
  printf '%s\n' exact-pack >"$git_fixture/$PACK_REL/probe.txt"
  git -C "$git_fixture" add .
  git -C "$git_fixture" commit -qm implementation
  implementation=$(git -C "$git_fixture" rev-parse HEAD)
  printf '%s\n' checkpoint >"$git_fixture/docs/plans/stride-next-evolution-master-plan.md"
  git -C "$git_fixture" add docs/plans/stride-next-evolution-master-plan.md
  git -C "$git_fixture" commit -qm checkpoint
  checkpoint=$(git -C "$git_fixture" rev-parse HEAD)
  assert_working_operator_pack_matches_b "$git_fixture" "$checkpoint" "$PACK_REL"
  assert_checkpoint_diff "$git_fixture" "$implementation" "$checkpoint" "$git_fixture/exact-diff.txt"
  rm "$git_fixture/exact-diff.txt"
  printf '%s\n' dirty >>"$git_fixture/$PACK_REL/probe.txt"
  ! (assert_working_operator_pack_matches_b "$git_fixture" "$checkpoint" "$PACK_REL") 2>/dev/null
  git -C "$git_fixture" checkout -q -- "$PACK_REL/probe.txt"
  git -C "$git_fixture" checkout -qb invalid-checkpoint "$implementation"
  mkdir -p "$git_fixture/docs/plans" "$git_fixture/mobile"
  printf '%s\n' checkpoint >"$git_fixture/docs/plans/stride-next-evolution-master-plan.md"
  printf '%s\n' code >"$git_fixture/mobile/extra.ts"
  git -C "$git_fixture" add docs/plans/stride-next-evolution-master-plan.md mobile/extra.ts
  git -C "$git_fixture" commit -qm invalid-checkpoint
  ! (assert_checkpoint_diff "$git_fixture" "$implementation" HEAD "$git_fixture/invalid-diff.txt") >/dev/null 2>&1
)

node "$SCRIPT_DIR/self-check.mjs"

if command -v shasum >/dev/null 2>&1; then
  (cd "$SCRIPT_DIR" && shasum -a 256 -c PACK-SHA256SUMS)
elif command -v sha256sum >/dev/null 2>&1; then
  (cd "$SCRIPT_DIR" && sha256sum -c PACK-SHA256SUMS)
else
  printf '%s\n' 'self-check: shasum or sha256sum is required' >&2
  exit 1
fi

printf '%s\n' 'self-check: exact-release bootstrap pack passed'
