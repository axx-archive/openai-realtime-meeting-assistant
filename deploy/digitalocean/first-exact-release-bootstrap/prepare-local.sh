#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
export PATH=/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
REPO=${REPO:-$(cd "$SCRIPT_DIR/../../.." && pwd -P)}
REMOTE=${REMOTE:-axx}
BRANCH=${BRANCH:-main}
A=${A:-}
B=${B:-}
PACK_REL=deploy/digitalocean/first-exact-release-bootstrap

die() { printf 'prepare-local: %s\n' "$*" >&2; exit 1; }
require_sha() { [[ ${1:-} =~ ^[0-9a-f]{40}$ ]] || die "invalid full commit SHA: ${1:-missing}"; }

assert_working_operator_pack_matches_b() {
  local repo=$1 checkpoint=$2 pack_rel=$3
  git -C "$repo" diff --quiet "$checkpoint" -- "$pack_rel" || die 'working operator pack differs from exact B'
  test -z "$(git -C "$repo" ls-files --others --exclude-standard -- "$pack_rel")" || die 'working operator pack contains files absent from exact B'
}

assert_checkpoint_diff() {
  local repo=$1 implementation=$2 checkpoint=$3 output=$4
  git -C "$repo" diff --name-status "$implementation" "$checkpoint" >"$output"
  cmp "$output" <(printf 'A\tdocs/plans/stride-next-evolution-master-plan.md\n') \
    || die 'B must add only the approved release-checkpoint plan'
}

main() {
command -v git >/dev/null || die 'git is required'
command -v node >/dev/null || die 'Node is required locally for source preparation'
command -v jq >/dev/null || die 'jq is required'
command -v shasum >/dev/null || die 'shasum is required'
test -d "$REPO/.git" || die "$REPO is not the repository root"
git -C "$REPO" fetch --prune "$REMOTE" "$BRANCH"
remote_main=$(git -C "$REPO" rev-parse "$REMOTE/$BRANCH")
if test -z "$B"; then B=$remote_main; fi
require_sha "$A"
require_sha "$B"
test "$B" = "$remote_main" || die "B must equal reviewed $REMOTE/$BRANCH"
test "$(git -C "$REPO" rev-parse "$B^")" = "$A" || die 'B must be the direct child of implementation commit A'
assert_working_operator_pack_matches_b "$REPO" "$B" "$PACK_REL"

OUT=${OUT:-/tmp/meetingassist-first-release-$B}
test ! -e "$OUT" || die "output already exists: $OUT"
mkdir -m 700 "$OUT"

cleanup_worktrees=()
cleanup() {
  local wt
  for wt in "${cleanup_worktrees[@]:-}"; do
    if test -d "$wt"; then git -C "$REPO" worktree remove --force "$wt" >/dev/null 2>&1 || true; fi
  done
}
trap cleanup EXIT

for sha in "$A" "$B"; do
  wt="/tmp/meetingassist-release-worktree-$sha-$$"
  test ! -e "$wt" || die "temporary worktree already exists: $wt"
  git -C "$REPO" worktree add --detach "$wt" "$sha"
  cleanup_worktrees+=("$wt")
  mkdir -m 700 "$OUT/$sha"
  (
    cd "$wt"
    test -z "$(git status --porcelain --untracked-files=all)" || die "release worktree $sha is dirty"
    test "$(git rev-parse HEAD)" = "$sha" || die "release worktree $sha is not exact"
    node scripts/bonfire-release.mjs scope --reviewed-ref "$sha" >"$OUT/$sha/scope.json"
    node scripts/bonfire-release.mjs prepare \
      --reviewed-ref "$sha" \
      --archive "$OUT/$sha/source.tar" \
      --source-receipt "$OUT/$sha/source-receipt.json"
    if test "$sha" = "$B"; then
      (cd "$PACK_REL" && ./self-check.sh)
      mkdir -m 700 "$OUT/operator-pack"
      cp -a "$PACK_REL/." "$OUT/operator-pack/"
    fi
  )
  git -C "$REPO" worktree remove "$wt"
done
cleanup_worktrees=()

# Commit/time-dependent git-archive fields intentionally differ between A/B.
# Compare only release-owned file identity, inventory, and configuration.
source_selector='. | {gitTreeDigest,reviewedInventorySha256,transitiveInputsSha256,buildConfigSha256,scopePolicySha256,inputCount,configFiles}'
scope_selector='. | {inventorySha256,inputCount,paths}'
cmp \
  <(jq -S "$source_selector" "$OUT/$A/source-receipt.json") \
  <(jq -S "$source_selector" "$OUT/$B/source-receipt.json")
cmp \
  <(jq -S "$scope_selector" "$OUT/$A/scope.json") \
  <(jq -S "$scope_selector" "$OUT/$B/scope.json")

assert_checkpoint_diff "$REPO" "$A" "$B" "$OUT/a-to-b-files.txt"
test -f "$OUT/operator-pack/PACK-SHA256SUMS" || die 'exact B operator pack was not exported'
(cd "$OUT/operator-pack" && shasum -a 256 -c PACK-SHA256SUMS >/dev/null)
operator_pack_sha=$(shasum -a 256 "$OUT/operator-pack/PACK-SHA256SUMS" | awk '{print $1}')
jq -n \
  --arg schema 'bonfire.first-exact-bootstrap-plan.v3' \
  --arg implementationCommit "$A" \
  --arg checkpointCommit "$B" \
  --arg remote "$REMOTE" \
  --arg branch "$BRANCH" \
  --arg operatorPackSha256 "$operator_pack_sha" \
  --arg preparedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{schema:$schema,implementationCommit:$implementationCommit,checkpointCommit:$checkpointCommit,remote:$remote,branch:$branch,operatorPackSha256:$operatorPackSha256,preparedAt:$preparedAt}' \
  >"$OUT/bootstrap-plan.json"

find "$OUT" -type d -exec chmod 700 {} +
find "$OUT" -type f -exec chmod 600 {} +
(
  cd "$OUT"
  find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 shasum -a 256 >SHA256SUMS
  chmod 600 SHA256SUMS
  shasum -a 256 -c SHA256SUMS >/dev/null
)

printf 'Prepared exact A/B source pack: %s\n' "$OUT"
printf 'Copy operator-pack/, bootstrap-plan.json, and each SHA directory from this output only.\n'
}

if [[ ${BASH_SOURCE[0]} != "$0" ]]; then
  return 0
fi

main "$@"
