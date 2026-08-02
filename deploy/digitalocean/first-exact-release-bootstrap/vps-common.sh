#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RELEASE_PARENT=/opt/meetingassist-releases
PLAN_FILE="$RELEASE_PARENT/bootstrap-plan.json"
STATE_DIR="$RELEASE_PARENT/.first-exact-bootstrap-state"
STATE_FILE="$STATE_DIR/state.json"
BASE_ENV=/opt/meetingassist/deploy/digitalocean/.env
HOST=thebonfire.xyz
VPS_IP=146.190.171.224
EXPECTED_NODE_PACKAGE=18.19.1+dfsg-6ubuntu5
HOSTS_MARKER='# bonfire-bootstrap-local-probe'
IPTABLES_CHAIN=BONFIRE_BOOTSTRAP
PERSISTENT_GUARD_NAME=bonfire-bootstrap-ingress-guard
PERSISTENT_GUARD_SCRIPT=/usr/local/sbin/bonfire-bootstrap-ingress-guard
PERSISTENT_GUARD_UNIT=/etc/systemd/system/bonfire-bootstrap-ingress-guard.service
PERSISTENT_GUARD_DROPIN=/etc/systemd/system/docker.service.d/bonfire-bootstrap-ingress-guard.conf

die() {
  printf 'bootstrap: %s\n' "$*" >&2
  exit 1
}

require_root() {
  test "$(id -u)" -eq 0 || die 'must run as root on the VPS'
}

require_commands() {
  local command
  for command in "$@"; do
    command -v "$command" >/dev/null 2>&1 || die "missing command: $command"
  done
}

require_full_sha() {
  [[ ${1:-} =~ ^[0-9a-f]{40}$ ]] || die "invalid full commit SHA: ${1:-missing}"
}

require_sha256() {
  [[ ${1:-} =~ ^[0-9a-f]{64}$ ]] || die "invalid SHA-256: ${1:-missing}"
}

load_plan() {
  require_commands jq sha256sum
  test -f "$PLAN_FILE" || die "missing $PLAN_FILE"
  test ! -L "$PLAN_FILE" || die 'bootstrap plan must not be a symlink'
  test "$(stat -c %U "$PLAN_FILE")" = root || die 'bootstrap plan must be root-owned'
  (( (8#$(stat -c %a "$PLAN_FILE") & 8#022) == 0 )) || die 'bootstrap plan must not be group/world writable'
  A=$(jq -er '.implementationCommit' "$PLAN_FILE")
  B=$(jq -er '.checkpointCommit' "$PLAN_FILE")
  require_full_sha "$A"
  require_full_sha "$B"
  test "$A" != "$B" || die 'A and B must differ'
  ADIR="$RELEASE_PARENT/$A"
  BDIR="$RELEASE_PARENT/$B"
  local expected_pack_sha actual_pack_sha
  expected_pack_sha=$(jq -er '.operatorPackSha256' "$PLAN_FILE")
  require_sha256 "$expected_pack_sha"
  test -f "$SCRIPT_DIR/PACK-SHA256SUMS" && test ! -L "$SCRIPT_DIR/PACK-SHA256SUMS" || die 'operator pack checksum manifest is missing or unsafe'
  test "$(stat -c %U "$SCRIPT_DIR/PACK-SHA256SUMS")" = root || die 'operator pack checksum manifest must be root-owned'
  (( (8#$(stat -c %a "$SCRIPT_DIR/PACK-SHA256SUMS") & 8#022) == 0 )) || die 'operator pack checksum manifest is group/world writable'
  actual_pack_sha=$(sha256sum "$SCRIPT_DIR/PACK-SHA256SUMS" | awk '{print $1}')
  test "$actual_pack_sha" = "$expected_pack_sha" || die 'operator pack differs from the exact B-bound manifest'
  (cd "$SCRIPT_DIR" && sha256sum -c PACK-SHA256SUMS >/dev/null) || die 'operator pack file checksum failed'
}

install_persistent_ingress_guard() {
  require_commands systemctl systemd-analyze iptables ip6tables
  test ! -e "$PERSISTENT_GUARD_SCRIPT" || die "$PERSISTENT_GUARD_SCRIPT already exists"
  test ! -e "$PERSISTENT_GUARD_UNIT" || die "$PERSISTENT_GUARD_UNIT already exists"
  test ! -e "$PERSISTENT_GUARD_DROPIN" || die "$PERSISTENT_GUARD_DROPIN already exists"
  install -m 700 "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.sh" "$PERSISTENT_GUARD_SCRIPT"
  install -m 644 "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.service" "$PERSISTENT_GUARD_UNIT"
  install -d -m 755 "$(dirname "$PERSISTENT_GUARD_DROPIN")"
  install -m 644 "$SCRIPT_DIR/docker-ingress-guard.conf" "$PERSISTENT_GUARD_DROPIN"
  systemctl daemon-reload
  systemd-analyze verify "$PERSISTENT_GUARD_UNIT"
  systemctl enable "$PERSISTENT_GUARD_NAME.service" >/dev/null
  systemctl start "$PERSISTENT_GUARD_NAME.service"
  # A one-shot unit is deliberately inactive after success. Starting it again
  # simulates Docker's boot/restart dependency and proves idempotent reapply.
  systemctl start "$PERSISTENT_GUARD_NAME.service"
  assert_persistent_ingress_guard
}

assert_persistent_ingress_guard() {
  cmp "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.sh" "$PERSISTENT_GUARD_SCRIPT"
  cmp "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.service" "$PERSISTENT_GUARD_UNIT"
  cmp "$SCRIPT_DIR/docker-ingress-guard.conf" "$PERSISTENT_GUARD_DROPIN"
  test "$(systemctl is-enabled "$PERSISTENT_GUARD_NAME.service")" = enabled
  systemctl show docker.service -p Requires --value | tr ' ' '\n' | grep -Fx "$PERSISTENT_GUARD_NAME.service" >/dev/null
  systemctl show docker.service -p After --value | tr ' ' '\n' | grep -Fx "$PERSISTENT_GUARD_NAME.service" >/dev/null
  "$PERSISTENT_GUARD_SCRIPT" status
}

rearm_persistent_ingress_guard() {
  install -m 700 "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.sh" "$PERSISTENT_GUARD_SCRIPT" || return 1
  install -m 644 "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.service" "$PERSISTENT_GUARD_UNIT" || return 1
  install -d -m 755 "$(dirname "$PERSISTENT_GUARD_DROPIN")" || return 1
  install -m 644 "$SCRIPT_DIR/docker-ingress-guard.conf" "$PERSISTENT_GUARD_DROPIN" || return 1
  systemctl daemon-reload || return 1
  systemctl enable "$PERSISTENT_GUARD_NAME.service" >/dev/null || return 1
  "$PERSISTENT_GUARD_SCRIPT" apply || return 1
  assert_persistent_ingress_guard
}

remove_persistent_ingress_guard_rules() {
  assert_persistent_ingress_guard
  "$PERSISTENT_GUARD_SCRIPT" remove
}

retire_persistent_ingress_guard() {
  systemctl disable "$PERSISTENT_GUARD_NAME.service" >/dev/null || return 1
  rm -f "$PERSISTENT_GUARD_DROPIN" "$PERSISTENT_GUARD_UNIT" "$PERSISTENT_GUARD_SCRIPT" || return 1
  rmdir --ignore-fail-on-non-empty "$(dirname "$PERSISTENT_GUARD_DROPIN")" || return 1
  systemctl daemon-reload || return 1
  test "$(systemctl is-enabled "$PERSISTENT_GUARD_NAME.service" 2>/dev/null || true)" != enabled
  ! "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.sh" status >/dev/null 2>&1
}

apply_ephemeral_ingress_guard_family() {
  local tool=$1 wan=$2
  while "$tool" -C DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN" >/dev/null 2>&1; do
    "$tool" -D DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN"
  done
  if "$tool" -S "$IPTABLES_CHAIN" >/dev/null 2>&1; then
    "$tool" -F "$IPTABLES_CHAIN"
  else
    "$tool" -N "$IPTABLES_CHAIN"
  fi
  "$tool" -A "$IPTABLES_CHAIN" -p tcp -m multiport --dports 80,443,3478 -j REJECT --reject-with tcp-reset
  "$tool" -A "$IPTABLES_CHAIN" -p udp --dport 3478 -j DROP
  "$tool" -A "$IPTABLES_CHAIN" -p udp --dport 40000:40100 -j DROP
  "$tool" -A "$IPTABLES_CHAIN" -p udp --dport 49160:49200 -j DROP
  "$tool" -A "$IPTABLES_CHAIN" -j RETURN
  "$tool" -I DOCKER-USER 1 -i "$wan" -j "$IPTABLES_CHAIN"
}

assert_ephemeral_ingress_guard() {
  local wan=$1 tool
  for tool in iptables ip6tables; do
    "$tool" -C DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN"
    test "$("$tool" -S DOCKER-USER | grep -F -- "-A DOCKER-USER -i $wan -j $IPTABLES_CHAIN" | wc -l)" -eq 1
    "$tool" -C "$IPTABLES_CHAIN" -p tcp -m multiport --dports 80,443,3478 -j REJECT --reject-with tcp-reset
    "$tool" -C "$IPTABLES_CHAIN" -p udp --dport 3478 -j DROP
    "$tool" -C "$IPTABLES_CHAIN" -p udp --dport 40000:40100 -j DROP
    "$tool" -C "$IPTABLES_CHAIN" -p udp --dport 49160:49200 -j DROP
    "$tool" -C "$IPTABLES_CHAIN" -j RETURN
    test "$("$tool" -S "$IPTABLES_CHAIN" | wc -l)" -eq 6
  done
}

rearm_ephemeral_ingress_guard() {
  local wan=$1
  apply_ephemeral_ingress_guard_family iptables "$wan"
  apply_ephemeral_ingress_guard_family ip6tables "$wan"
  assert_ephemeral_ingress_guard "$wan"
}

remove_ephemeral_ingress_guard_rules() {
  local wan=$1 tool
  for tool in iptables ip6tables; do
    while "$tool" -C DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN" >/dev/null 2>&1; do
      "$tool" -D DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN"
    done
  done
}

retire_ephemeral_ingress_guard_chains() {
  local wan=$1 tool
  for tool in iptables ip6tables; do
    ! "$tool" -C DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN" >/dev/null 2>&1 || return 1
    "$tool" -F "$IPTABLES_CHAIN" || return 1
    "$tool" -X "$IPTABLES_CHAIN" || return 1
  done
}

load_state() {
  load_plan
  test -f "$STATE_FILE" || die "missing ceremony state; run init-build first"
  test ! -L "$STATE_FILE" || die 'ceremony state must not be a symlink'
  local state_a state_b
  state_a=$(jq -er '.implementationCommit' "$STATE_FILE")
  state_b=$(jq -er '.checkpointCommit' "$STATE_FILE")
  BK=$(jq -er '.backupDir' "$STATE_FILE")
  test "$state_a" = "$A" && test "$state_b" = "$B" || die 'state commit pair differs from bootstrap plan'
  [[ $BK == /opt/meetingassist-backups/*-first-exact-bootstrap ]] || die 'unsafe backup directory in state'
  test -d "$BK" || die "missing backup directory $BK"
  test ! -L "$BK" || die 'backup directory must not be a symlink'
  test "$(stat -c %a "$BK")" = 700 || die 'backup directory mode must be 700'
}

acquire_operator_lock() {
  mkdir -p -m 700 "$STATE_DIR"
  exec 9>"$STATE_DIR/operator.lock"
  flock -n 9 || die 'another bootstrap-pack command is active'
}

marker_path() {
  printf '%s/phase-%s\n' "$STATE_DIR" "$1"
}

mark_phase() {
  local path
  path=$(marker_path "$1")
  test ! -e "$path" || return 0
  printf '%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$path"
  chmod 600 "$path"
}

require_phase() {
  test -f "$(marker_path "$1")" || die "required phase not complete: $1"
}

phase_done() {
  test -f "$(marker_path "$1")"
}

release_tool() {
  printf '%s/sealed-candidate/scripts/bonfire-release.mjs\n' "$1"
}

assert_node_matches_release() {
  local dir=$1 expected
  expected=$(jq -er '.toolchain.releaseToolNode' "$dir/build-manifest.json")
  test "$(node -p process.version)" = "$expected" || die "Node runtime differs from $(basename "$dir") build receipt"
}

local_https() {
  curl -fsS --connect-timeout 5 --max-time 30 \
    --noproxy '*' --resolve "$HOST:443:127.0.0.1" "$@"
}

release_verify() {
  local dir=$1
  node "$(release_tool "$dir")" verify \
    --release-dir "$dir" \
    --base-env "$BASE_ENV" \
    --health-url "https://$HOST/healthz" \
    --ready-url "https://$HOST/readyz"
}

project_service_id() {
  local service=$1
  docker ps -q \
    --filter label=com.docker.compose.project=digitalocean \
    --filter "label=com.docker.compose.service=$service"
}

pg_counts() {
  local container=$1
  docker exec -i "$container" psql -XqAt -F $'\t' -v ON_ERROR_STOP=1 -U bonfire -d bonfire <<'SQL'
SELECT format('SELECT %L, count(*) FROM %I.%I;', schemaname || '.' || tablename, schemaname, tablename)
FROM pg_tables
WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
ORDER BY schemaname, tablename
\gexec
SQL
}

canonical_parity() {
  local output=$1
  local_https "https://$HOST/readyz" >"$output" || return 1
  jq -e '
    . as $r |
    $r.ok == true and
    $r.checks.app == true and
    $r.checks.memoryStore == true and
    $r.checks.boardLifecycle.healthy == true and
    $r.checks.admissionAnchors.healthy == true and
    $r.checks.consentAuthority.healthy == true and
    (($r.checks.restoreGate.enabled | not) or $r.checks.restoreGate.ready == true) and
    ($r.degraded | index("canonical_runtime_degraded") | not) and
    ($r.checks.canonical as $c |
      $c.mode == "shadow" and
      $c.required == false and
      $c.database == true and
      $c.healthy == true and
      (($c.error // "") == "") and
      $c.pending == 0 and
      (($c.frozenFamilies // []) | length) == 0 and
      (($c.uncoveredFamilies // []) | length) == 0 and
      $c.highWater == $c.dirtyHighWater and
      $c.dirtyHighWater == $c.reconciledHighWater and
      $c.outboxKnown == true and
      $c.outboxPending == 0 and
      $c.outboxFailed == 0 and
      $c.outboxOldestSeconds == 0 and
      $c.checkpointValid == true and
      $c.checkpointHighWater == $c.reconciledHighWater and
      $c.catchUpPublication.ready == true and
      $c.catchUpPublication.workerRunning == true and
      $c.catchUpPublication.pending == 0 and
      (($c.catchUpPublication.error // "") == "") and
      (($c.brainProjection.enabled | not) or
        ($c.brainProjection.ready == true and
         $c.brainProjection.database == true and
         $c.brainProjection.durableSink == true and
         $c.brainProjection.workerRunning == true and
         $c.brainProjection.queueKnown == true and
         $c.brainProjection.caughtUp == true and
         $c.brainProjection.pendingScopes == 0 and
         $c.brainProjection.failedScopes == 0 and
         $c.brainProjection.backoffScopes == 0 and
         (($c.brainProjection.error // "") == ""))))
  ' "$output" >/dev/null
}

wait_for_canonical_parity() {
  local label=$1 end output
  output="$BK/${label}-ready-latest.json"
  end=$((SECONDS + 600))
  while (( SECONDS < end )); do
    if canonical_parity "$output"; then
      return 0
    fi
    sleep 10
  done
  canonical_parity "$output"
}

release_data_gate() {
  local dir=$1 label=$2 pgc versions file name version file_hash db_hash after
  pgc=$(project_service_id canonical-postgres)
  test -n "$pgc" || return 1
  versions=$(docker exec "$pgc" psql -XqAt -U bonfire -d bonfire \
    -c "select string_agg(version::text, ',' order by version) from schema_migrations")
  test "$versions" = '1,2,3,4,5,6,7,8,9' || return 1
  shopt -s nullglob
  local files=("$dir"/sealed-candidate/migrations/*.sql)
  test "${#files[@]}" -eq 9 || return 1
  for file in "${files[@]}"; do
    name=$(basename "$file")
    version=$((10#${name%%_*}))
    file_hash=$(sha256sum "$file" | awk '{print $1}')
    db_hash=$(docker exec "$pgc" psql -XqAt -U bonfire -d bonfire \
      -c "select encode(sha256,'hex') from schema_migrations where version=$version")
    test "$db_hash" = "$file_hash" || return 1
  done
  after="$BK/table-counts-after-$label.tsv"
  pg_counts "$pgc" >"$after"
  awk -F '\t' '
    NR == FNR { before[$1] = $2; next }
    { after[$1] = $2 }
    END {
      bad = 0
      for (table in before) {
        if (!(table in after) || after[table] + 0 < before[table] + 0) {
          print "missing/decreased table: " table > "/dev/stderr"
          bad = 1
        }
      }
      exit bad
    }
  ' "$BK/table-counts-before.tsv" "$after"
}

target_topology_gate() {
  diff -u \
    <(printf '%s\n' caddy canonical-postgres coturn meetingassist render-queue-init render-runner | sort) \
    <(docker ps -a --filter label=com.docker.compose.project=digitalocean \
      --format '{{.Label "com.docker.compose.service"}}' | sort -u)
  diff -u \
    <(printf '%s\n' digitalocean_caddy_config digitalocean_caddy_data digitalocean_canonical_postgres \
      digitalocean_codex_queue digitalocean_meeting_data digitalocean_render_queue digitalocean_usage_ledger | sort) \
    <(docker volume ls --format '{{.Name}}' | grep '^digitalocean_' | sort)
  diff -u \
    <(printf '%s\n' digitalocean_default digitalocean_render_internal | sort) \
    <(docker network ls --filter label=com.docker.compose.project=digitalocean --format '{{.Name}}' | sort)
  test -z "$(docker ps -aq --filter label=com.docker.compose.project=digitalocean \
    --filter label=com.docker.compose.service=codex-runner)"
  local init
  init=$(docker ps -aq --filter label=com.docker.compose.project=digitalocean \
    --filter label=com.docker.compose.service=render-queue-init)
  test -n "$init"
  test "$(docker inspect -f '{{.State.Status}}:{{.State.ExitCode}}' "$init")" = 'exited:0'
}

authenticate_operator() {
  local name password login candidate
  read -r -p 'Roster name for read-only proof: ' name
  read -r -s -p 'Password: ' password
  printf '\n' >&2
  login=$(jq -cn --arg name "$name" --arg password "$password" '{name:$name,password:$password}' |
    local_https -H "Origin: https://$HOST" -H 'Content-Type: application/json' \
      -H 'X-Bonfire-Client: native' --data-binary @- "https://$HOST/auth/login")
  unset password
  if ! OPS_SESSION=$(jq -er '.sessionToken | select(type=="string" and length > 20)' <<<"$login"); then
    candidate=$(jq -r '.sessionToken // empty | select(type=="string" and length > 20)' <<<"$login" 2>/dev/null || true)
    if test -n "$candidate"; then
      local_https -X POST -H "Origin: https://$HOST" -H "Authorization: Bearer $candidate" \
        -H 'Content-Type: application/json' --data '{}' "https://$HOST/auth/logout" >/dev/null || true
    fi
    unset candidate login name password
    return 1
  fi
  unset login name
}

logout_operator() {
  if test -n "${OPS_SESSION:-}"; then
    local_https -X POST -H "Origin: https://$HOST" \
      -H "Authorization: Bearer $OPS_SESSION" \
      -H 'Content-Type: application/json' --data '{}' \
      "https://$HOST/auth/logout" >/dev/null || true
    unset OPS_SESSION
  fi
}

remove_maintenance_ingress_block() {
  local wan=$1
  iptables -C DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN"
  iptables -D DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN"
  iptables -F "$IPTABLES_CHAIN"
  iptables -X "$IPTABLES_CHAIN"
  ip6tables -C DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN"
  ip6tables -D DOCKER-USER -i "$wan" -j "$IPTABLES_CHAIN"
  ip6tables -F "$IPTABLES_CHAIN"
  ip6tables -X "$IPTABLES_CHAIN"
}
