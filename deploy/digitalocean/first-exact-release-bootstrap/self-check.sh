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

  original_state_dir=$STATE_DIR
  STATE_DIR=$(mktemp -d)
  assert_forward_ceremony_permitted
  for terminal in public-open-attempted legacy-restored legacy-reopened; do
    : >"$STATE_DIR/phase-$terminal"
    ! (assert_forward_ceremony_permitted) 2>/dev/null
    rm "$STATE_DIR/phase-$terminal"
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
