#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=vps-common.sh
source "$SCRIPT_DIR/vps-common.sh"

usage() {
  cat <<'EOF'
Usage: vps-bootstrap.sh PHASE

Phases, in order:
  start-next-ceremony        archive one exact restored/reopened prior ceremony
  init-build                 install/check Node; build retained A and B
  preflight                  require exact missing-render-volume verify failures
  isolate                    block public ingress; install exact renderer profiles
  acknowledge-external-block record operator's independent Mac failure proof
  prove-empty                prove every room is empty under member authentication
  backup                     quiesce writers; make complete private cold backup
  rehearse                   restore-test every volume and PostgreSQL dump
  normalize-canonical        converge exact A without lifecycle append; require seven
  qualify-repair-clones      prove normalization, repair, replay and restart on two fresh clones
  generate-repair-manifest   seal post-backup private evidence and stop for approval
  retire-legacy              remove only codex-runner and two archived legacy volumes
  repair-canonical           run exact A's manifest-bound canonical repair one-shot
  bootstrap-a                manually boot and fully gate exact A without a ledger
  activate-b                 activate exact B through retained A and create generation 1
  reopen                     remove only this ceremony's ingress block after B gates
  acknowledge-public         record independent Mac success/B-identity proof
  status                     print non-secret phase state

On any failure after isolate, public ingress remains blocked. Never delete the
release operation lock merely because it is old. Use vps-rollback-legacy.sh
only after the rehearsed backup exists and only when that lock is absent.
EOF
}

initialize_state() {
  load_plan
  mkdir -p -m 700 "$RELEASE_PARENT"
  test "$(stat -c %A "$RELEASE_PARENT" | cut -c6,9)" = '--' || die 'release parent is group/world writable'
  mkdir -p -m 700 "$STATE_DIR"
  acquire_operator_lock
  if test -e "$STATE_FILE"; then
    load_state
    return
  fi
  BK="/opt/meetingassist-backups/$(date -u +%Y%m%dT%H%M%SZ)-first-exact-bootstrap"
  install -d -m 700 "$BK" "$BK/meta" "$BK/private" "$BK/volumes" "$BK/images"
  jq -n --arg a "$A" --arg b "$B" --arg backup "$BK" --arg started "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{schema:"bonfire.first-exact-bootstrap-state.v1",implementationCommit:$a,checkpointCommit:$b,backupDir:$backup,startedAt:$started}' \
    >"$STATE_FILE"
  chmod 600 "$STATE_FILE"
}

phase_start_next_ceremony() {
  require_root; require_commands docker jq sha256sum tar curl iptables ip6tables; load_plan
  test -f "$STATE_FILE" && test ! -L "$STATE_FILE" || die 'no prior ceremony state exists to roll over'
  test "$(stat -c %U:%G "$STATE_DIR")" = root:root && test "$(stat -c %a "$STATE_DIR")" = 700 \
    || die 'prior ceremony state directory is not exact root-private state'
  acquire_operator_lock
  local old_a old_b old_bk archive_stamp archive_root archive_manifest archive_sha confirmation
  old_a=$(jq -er '.implementationCommit|select(type=="string" and test("^[0-9a-f]{40}$"))' "$STATE_FILE")
  old_b=$(jq -er '.checkpointCommit|select(type=="string" and test("^[0-9a-f]{40}$"))' "$STATE_FILE")
  old_bk=$(jq -er '.backupDir|select(type=="string" and startswith("/opt/meetingassist-backups/") and endswith("-first-exact-bootstrap"))' "$STATE_FILE")
  test "$old_a" != "$A" || test "$old_b" != "$B" || die 'next-ceremony rollover requires a different reviewed A/B pair'
  test -d "$old_bk" && test ! -L "$old_bk" && test "$(stat -c %U:%G "$old_bk")" = root:root && test "$(stat -c %a "$old_bk")" = 700 \
    || die 'prior ceremony backup is missing or not exact root-private state'
  test -f "$STATE_DIR/phase-legacy-restored" && test -f "$STATE_DIR/phase-legacy-reopened" \
    || die 'prior ceremony is not the exact restored-and-reopened terminal state'
  test -f "$STATE_DIR/phase-public-open-attempted" \
    || die 'prior restored legacy reopen lacks its irreversible public-open boundary marker'
  test -z "$(find "$STATE_DIR" -type l -print -quit)" || die 'prior ceremony state contains a symlink'
  while IFS= read -r path; do
    test "$(stat -c %U:%G "$path")" = root:root && test "$(stat -c %a "$path")" = 700 \
      || die 'prior ceremony state contains a non-private directory'
  done < <(find "$STATE_DIR" -mindepth 1 -type d -print)
  while IFS= read -r path; do
    test -f "$path" && test "$(stat -c %U:%G "$path")" = root:root && test "$(stat -c %a "$path")" = 600 \
      || die 'prior ceremony state contains a non-private or non-regular file'
  done < <(find "$STATE_DIR" -type f -print)
  test ! -e "$RELEASE_PARENT/.bonfire-release-operation.lock" || die 'release operation lock makes rollover ambiguous'
  test ! -e "$RELEASE_PARENT/active-release.json" || die 'active release ledger means this is not a restored legacy terminal'
  ! docker volume inspect digitalocean_render_queue >/dev/null 2>&1 || die 'render queue remains after prior terminal restore'
  ! docker network inspect "$REPAIR_NETWORK" >/dev/null 2>&1 || die 'repair network remains after prior terminal restore'
  local owned
  for owned in "$NORMALIZE_CONTAINER" "$MANIFEST_CONTAINER" "$REPAIR_CONTAINER"; do
    ! docker inspect "$owned" >/dev/null 2>&1 || die 'owned repair one-shot remains after prior terminal restore'
  done
  test ! -e "$PERSISTENT_GUARD_SCRIPT" && test ! -e "$PERSISTENT_GUARD_UNIT" && test ! -e "$PERSISTENT_GUARD_DROPIN" \
    || die 'persistent ingress guard files remain after prior terminal reopen'
  ! iptables -S "$IPTABLES_CHAIN" >/dev/null 2>&1 && ! ip6tables -S "$IPTABLES_CHAIN" >/dev/null 2>&1 \
    && ! iptables -t mangle -S BONFIRE_BOOTSTRAP_RAW >/dev/null 2>&1 \
    && ! ip6tables -t mangle -S BONFIRE_BOOTSTRAP_RAW >/dev/null 2>&1 \
    || die 'ingress guard chains remain after prior terminal reopen'
  test -z "$(grep -F "$HOSTS_MARKER" /etc/hosts || true)" || die 'maintenance hosts marker remains after prior terminal reopen'
  test ! -e "$RENDERER_APPARMOR_PATH" && test ! -e "$RENDERER_SECCOMP_PATH" \
    || die 'renderer profiles remain after prior terminal restore'
  ! grep -F "$RENDERER_APPARMOR_NAME (" /sys/kernel/security/apparmor/profiles >/dev/null 2>&1 \
    || die 'renderer AppArmor profile remains loaded after prior terminal restore'
  mapfile -t prior_containers < <(docker ps -aq --no-trunc --filter label=com.docker.compose.project=digitalocean)
  mapfile -t prior_running < <(docker ps -q --no-trunc --filter label=com.docker.compose.project=digitalocean)
  test "${#prior_containers[@]}" -eq 6 && test "${#prior_running[@]}" -eq 6 \
    || die 'restored legacy terminal does not have exactly six running project containers'
  docker inspect "${prior_containers[@]}" | jq -e '
    ([.[]|.Config.Labels["com.docker.compose.service"]]|sort)==
      (["caddy","canonical-postgres","codex-runner","coturn","meetingassist","render-runner"]|sort)
  ' >/dev/null || die 'restored legacy terminal service topology is not exact'
  curl -fsS --noproxy '*' --connect-timeout 5 --max-time 30 "https://$HOST/healthz" | jq -e '.ok==true' >/dev/null \
    || die 'restored legacy public health is not currently good'
  curl -fsS --noproxy '*' --connect-timeout 5 --max-time 30 "https://$HOST/readyz" | jq -e '.ok==true' >/dev/null \
    || die 'restored legacy public readiness is not currently good'

  read -r -p "Type START NEXT EXACT CEREMONY $A $B: " confirmation
  test "$confirmation" = "START NEXT EXACT CEREMONY $A $B" || die 'next exact ceremony rollover was not explicitly confirmed'
  archive_stamp=$(date -u +%Y%m%dT%H%M%SZ)
  archive_root="$old_bk/private/prior-ceremony-$archive_stamp-$old_b"
  test ! -e "$archive_root" || die 'prior ceremony archive path collision'
  install -d -o root -g root -m 700 "$archive_root"
  mv "$STATE_DIR" "$archive_root/state"
  tar --xattrs --acls --numeric-owner -C "$archive_root" -cpf "$archive_root/state.tar" state
  chmod 600 "$archive_root/state.tar"
  archive_manifest="$archive_root/state-SHA256SUMS"
  (cd "$archive_root" && find state -type f -print0 | sort -z | xargs -0 sha256sum >"$archive_manifest.tmp")
  (cd "$archive_root" && sha256sum state.tar) >>"$archive_manifest.tmp"
  mv "$archive_manifest.tmp" "$archive_manifest"; chmod 600 "$archive_manifest"
  (cd "$archive_root" && sha256sum -c state-SHA256SUMS >/dev/null)
  archive_sha=$(sha256sum "$archive_manifest"|awk '{print $1}')
  jq -n --arg oldA "$old_a" --arg oldB "$old_b" --arg newA "$A" --arg newB "$B" \
    --arg archive "$archive_sha" --arg completed "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '
      {schema:"bonfire.prior-ceremony-rollover.v1",status:"complete",priorImplementationCommit:$oldA,
       priorCheckpointCommit:$oldB,nextImplementationCommit:$newA,nextCheckpointCommit:$newB,
       terminalState:"legacy_restored_and_reopened",stateArchiveManifestSha256:$archive,completedAt:$completed}
    ' >"$archive_root/rollover-receipt.raw"
  write_self_digest_json "$archive_root/rollover-receipt.raw" "$archive_root/rollover-receipt.json"
  rm "$archive_root/rollover-receipt.raw"
  (cd "$archive_root" && sha256sum state-SHA256SUMS rollover-receipt.json >ROLLOVER-SHA256SUMS && chmod 600 ROLLOVER-SHA256SUMS && sha256sum -c ROLLOVER-SHA256SUMS >/dev/null)

  initialize_state
  jq --arg prior "$archive_root" '.priorCeremonyArchive=$prior' "$STATE_FILE" >"$STATE_FILE.tmp"
  mv "$STATE_FILE.tmp" "$STATE_FILE"; chmod 600 "$STATE_FILE"
  printf 'Prior terminal ceremony preserved at %s; fresh A/B ceremony state initialized.\n' "$archive_root"
}

phase_init_build() {
  require_root
  require_commands apt-get apt-cache awk sha256sum tar jq docker
  initialize_state
  test ! -e "$RELEASE_PARENT/active-release.json" || die 'active release ledger already exists; this is not a genesis ceremony'
  test ! -e "$RELEASE_PARENT/.bonfire-release-operation.lock" || die 'release operation lock already exists'

  apt-get update
  local candidate
  candidate=$(apt-cache policy nodejs | awk '/Candidate:/{print $2}')
  test "$candidate" = "$EXPECTED_NODE_PACKAGE" || die "Node candidate changed from reviewed $EXPECTED_NODE_PACKAGE to $candidate"
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "nodejs=$candidate"
  node --input-type=module -e '
    if (typeof fetch !== "function" || typeof AbortSignal.timeout !== "function" ||
        typeof Object.hasOwn !== "function" || typeof [].at !== "function") process.exit(1)
    await import("node:fs/promises"); await import("node:crypto")
  '
  {
    node --version
    dpkg-query -W -f='${Package}=${Version}\n' nodejs
    sha256sum /usr/bin/node
    docker version --format '{{json .}}'
    docker compose version --format json
  } >"$BK/meta/build-host-toolchain.txt"

  local sha dir expected_tool actual_tool
  for sha in "$A" "$B"; do
    dir="$RELEASE_PARENT/$sha"
    test -r "$dir/source.tar" && test -r "$dir/source-receipt.json" || die "missing prepared source bundle for $sha"
    test "$(jq -er '.releaseCommit' "$dir/source-receipt.json")" = "$sha" || die "source receipt commit mismatch for $sha"
    if test ! -e "$dir/release-receipt.json"; then
      for output in build-manifest.json release.env candidate-bundle.json sealed-candidate; do
        test ! -e "$dir/$output" || die "partial build output exists for $sha: $output"
      done
      mkdir -m 700 "$dir/tool"
      tar -xf "$dir/source.tar" -C "$dir/tool" scripts/bonfire-release.mjs
      expected_tool=$(jq -er '.configFiles["scripts/bonfire-release.mjs"]' "$dir/source-receipt.json")
      actual_tool=$(sha256sum "$dir/tool/scripts/bonfire-release.mjs" | awk '{print $1}')
      test "$actual_tool" = "$expected_tool" || die "extracted release tool differs for $sha"
      node "$dir/tool/scripts/bonfire-release.mjs" build \
        --archive "$dir/source.tar" \
        --source-receipt "$dir/source-receipt.json" \
        --image "meetingassist:release-$sha" \
        --render-image "meetingassist-render:release-$sha" \
        --build-manifest "$dir/build-manifest.json" \
        --release-receipt "$dir/release-receipt.json" \
        --runtime-env "$dir/release.env" \
        >"$BK/meta/build-$sha.json"
    fi
    assert_node_matches_release "$dir"
    test "$(jq -er '.source.releaseCommit' "$dir/release-receipt.json")" = "$sha" || die "release receipt commit mismatch for $sha"
    docker image inspect \
      "$(jq -er '.images.meetingassist.imageId' "$dir/release-receipt.json")" \
      "$(jq -er '.images.renderRunner.imageId' "$dir/release-receipt.json")" >/dev/null
  done

  local selector
  selector='.source | {gitTreeDigest,reviewedInventorySha256,transitiveInputsSha256,buildConfigSha256,scopePolicySha256,inputCount,configFiles}'
  cmp <(jq -S "$selector" "$ADIR/release-receipt.json") <(jq -S "$selector" "$BDIR/release-receipt.json")
  mark_phase built
}

expected_preflight_failure() {
  local dir=$1 log=$2 rc
  set +e
  release_verify "$dir" >"$log" 2>&1
  rc=$?
  set -e
  test "$rc" -eq 1 || return 1
  mapfile -t lines < <(sed '/^[[:space:]]*$/d' "$log")
  test "${#lines[@]}" -eq 2 || return 1
  test "${lines[0]}" = 'bonfire-release: Command failed: docker volume inspect digitalocean_render_queue' || return 1
  test "${lines[1]}" = 'Error response from daemon: get digitalocean_render_queue: no such volume'
}

phase_preflight() {
  require_root; load_state; acquire_operator_lock; require_phase built
  assert_node_matches_release "$ADIR"; assert_node_matches_release "$BDIR"
  expected_preflight_failure "$ADIR" "$BK/preflight-a.log" || die 'A did not reach the exact expected missing-render-volume gate'
  expected_preflight_failure "$BDIR" "$BK/preflight-b.log" || die 'B did not reach the exact expected missing-render-volume gate'
  mark_phase preflight
}

phase_isolate() {
  require_root; require_commands iptables ip6tables curl getent systemctl systemd-analyze apparmor_parser sysctl; load_state; acquire_operator_lock; require_phase preflight
  phase_done isolated && die 'maintenance isolation is already installed'
  local wan
  wan=$(ip route get 1.1.1.1 | sed -n 's/.* dev \([^ ]*\).*/\1/p' | head -1)
  test "$wan" = eth0 || die "review unexpected WAN interface $wan"
  if ! phase_done isolation-install-started; then
    ! iptables -S "$IPTABLES_CHAIN" >/dev/null 2>&1 || die "IPv4 chain $IPTABLES_CHAIN already exists"
    ! ip6tables -S "$IPTABLES_CHAIN" >/dev/null 2>&1 || die "IPv6 chain $IPTABLES_CHAIN already exists"
    ! iptables -t mangle -S BONFIRE_BOOTSTRAP_RAW >/dev/null 2>&1 || die 'IPv4 persistent guard chain already exists'
    ! ip6tables -t mangle -S BONFIRE_BOOTSTRAP_RAW >/dev/null 2>&1 || die 'IPv6 persistent guard chain already exists'
    test ! -e "$PERSISTENT_GUARD_SCRIPT" && test ! -e "$PERSISTENT_GUARD_UNIT" && test ! -e "$PERSISTENT_GUARD_DROPIN" \
      || die 'persistent guard install path already exists'
    test ! -e "$RENDERER_APPARMOR_PATH" && test ! -e "$RENDERER_SECCOMP_PATH" \
      || die 'renderer security profile install path already exists'
    ! grep -F "$RENDERER_APPARMOR_NAME (" /sys/kernel/security/apparmor/profiles >/dev/null 2>&1 \
      || die 'renderer AppArmor profile name is already loaded without ceremony ownership'
    iptables-save >"$BK/meta/iptables.before"
    ip6tables-save >"$BK/meta/ip6tables.before"
    cp -a /etc/hosts "$BK/meta/hosts.before"
    test -z "$(grep -F "$HOSTS_MARKER" /etc/hosts || true)" || die 'hosts marker already exists'
    mark_phase isolation-install-started
    install_persistent_ingress_guard
  else
    rearm_persistent_ingress_guard || die 'could not resume the exact persistent ingress guard installation'
  fi
  rearm_ephemeral_ingress_guard "$wan"
  restore_hosts_marker_for_maintenance
  getent ahostsv4 "$HOST" | awk 'NR==1{seen=1; exit($1!="127.0.0.1")} END{if(!seen)exit 1}'
  local_https "https://$HOST/healthz" >"$BK/meta/isolated-loopback-health.json"
  assert_persistent_ingress_guard
  assert_ephemeral_ingress_guard "$wan"
  install_renderer_security_profiles
  iptables -S "$IPTABLES_CHAIN" >"$BK/meta/iptables-maintenance-chain.txt"
  ip6tables -S "$IPTABLES_CHAIN" >"$BK/meta/ip6tables-maintenance-chain.txt"
  jq --arg wan "$wan" '. + {wanInterface:$wan}' "$STATE_FILE" >"$STATE_FILE.tmp"
  mv "$STATE_FILE.tmp" "$STATE_FILE"; chmod 600 "$STATE_FILE"
  mark_phase isolated
  printf 'Public app/TURN ingress is now fail-closed. Run mac-public-probe.sh blocked from the Mac, then acknowledge-external-block.\n'
}

phase_acknowledge_external_block() {
  require_root; load_state; acquire_operator_lock; require_phase isolated
  local confirmation
  read -r -p 'Type PUBLIC APP TURN BLOCK CONFIRMED FROM MAC: ' confirmation
  test "$confirmation" = 'PUBLIC APP TURN BLOCK CONFIRMED FROM MAC' || die 'external block was not confirmed'
  mark_phase external-block-confirmed
}

all_rooms_empty() {
  OCC_RUN=$((OCC_RUN + 1))
  local rooms="$BK/private/occupancy/rooms-$OCC_RUN.json" room snapshot
  local_https -H "Origin: https://$HOST" -H "Authorization: Bearer $OPS_SESSION" \
    "https://$HOST/rooms" >"$rooms"
  jq -e '.ok==true and (.rooms|length>=1) and all(.rooms[]; .live==false and .participantCount==0)' "$rooms" >/dev/null || return 1
  while IFS= read -r room; do
    [[ $room =~ ^[a-zA-Z0-9._-]+$ ]] || return 1
    snapshot="$BK/private/occupancy/room-$OCC_RUN-$room.json"
    local_https -G -H "Origin: https://$HOST" -H "Authorization: Bearer $OPS_SESSION" \
      --data-urlencode "room=$room" "https://$HOST/participants" >"$snapshot"
    jq -e --arg room "$room" '
      .roomId==$room and .occupiedSeats==0 and (.participants|length)==0 and
      (.endpointCounts|length)==0 and (.endpointMediaStates|length)==0
    ' "$snapshot" >/dev/null || return 1
  done < <(jq -r '.rooms[].id' "$rooms")
}

phase_prove_empty() {
  require_root; load_state; acquire_operator_lock; require_phase external-block-confirmed
  install -d -m 700 "$BK/private/occupancy"
  authenticate_operator
  trap logout_operator EXIT
  OCC_RUN=0
  local deadline=$((SECONDS + 150))
  until all_rooms_empty; do
    (( SECONDS < deadline )) || die 'all rooms did not quiesce within the WebSocket liveness bound'
    sleep 5
  done
  sleep 30
  all_rooms_empty || die 'room occupancy was not stably empty across one sweep interval'
  logout_operator
  trap - EXIT
  mark_phase rooms-empty
}

write_backup_checksum_manifest() {
  local backup_dir=$1
  (
    cd "$backup_dir"
    find . -type f ! -path ./backup-SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >backup-SHA256SUMS
    chmod 600 backup-SHA256SUMS
    sha256sum -c backup-SHA256SUMS >/dev/null
  )
}

canonical_legacy_volume_names() {
  printf '%s\n' \
    digitalocean_caddy_config digitalocean_caddy_data digitalocean_canonical_postgres \
    digitalocean_codex_home digitalocean_codex_queue digitalocean_codex_runner_data \
    digitalocean_meeting_data digitalocean_usage_ledger
}

assert_backup_checksum_manifest_semantic() {
  local manifest=${1:-$BK/backup-SHA256SUMS} line digest relative resolved backup_root
  local paths_file required_file volume
  test "$manifest" = "$BK/backup-SHA256SUMS" \
    || die 'semantic backup validation accepts only the matched ceremony backup manifest'
  assert_root_private_regular_file "$manifest" 'backup checksum manifest'
  paths_file=$(mktemp "$STATE_DIR/backup-manifest-paths.XXXXXX")
  required_file=$(mktemp "$STATE_DIR/backup-required-paths.XXXXXX")
  backup_root=$(readlink -f "$BK")
  while IFS= read -r line; do
    if [[ $line =~ ^([0-9a-f]{64})[[:space:]][[:space:]](\./.+)$ ]]; then
      digest=${BASH_REMATCH[1]}
      relative=${BASH_REMATCH[2]}
    else
      rm -f "$paths_file" "$required_file"
      die 'backup checksum manifest contains a non-canonical checksum record'
    fi
    [[ $relative != /* && $relative != ./../* && $relative != */../* && $relative != */./* ]] \
      || { rm -f "$paths_file" "$required_file"; die 'backup checksum manifest contains an unsafe path'; }
    resolved=$(readlink -f "$BK/${relative#./}")
    test "$resolved" = "$BK/${relative#./}" && [[ $resolved == "$backup_root/"* ]] \
      || { rm -f "$paths_file" "$required_file"; die 'backup checksum payload is a symlink or escapes the matched backup'; }
    test -f "$resolved" && test ! -L "$resolved" \
      || { rm -f "$paths_file" "$required_file"; die 'backup checksum payload is not a regular non-symlink file'; }
    test "$(sha256sum "$resolved" | awk '{print $1}')" = "$digest" \
      || { rm -f "$paths_file" "$required_file"; die 'backup checksum payload digest mismatch'; }
    printf '%s\n' "$relative" >>"$paths_file"
  done <"$manifest"
  test -s "$paths_file" || { rm -f "$paths_file" "$required_file"; die 'backup checksum manifest is empty'; }
  test "$(wc -l <"$paths_file")" -eq "$(LC_ALL=C sort -u "$paths_file" | wc -l)" \
    || { rm -f "$paths_file" "$required_file"; die 'backup checksum manifest contains duplicate paths'; }
  {
    printf '%s\n' \
      ./postgres.pgcustom ./postgres.list ./migrations-before.tsv ./table-counts-before.tsv \
      ./private/volumes.inspect.json ./private/containers.inspect.json \
      ./private/base.env ./private/legacy-docker-compose.yml ./private/legacy-Caddyfile \
      ./private/legacy-compose-resolved.yml ./private/legacy-compose-provenance.json \
      ./private/opt-meetingassist.tar ./private/opt-meetingassist-workspace.tar \
      ./images/legacy-images.tar ./meta/legacy-image-map.tsv ./meta/networks.inspect.json \
      ./meta/legacy-container-authority.tsv ./meta/expected-volumes ./meta/actual-volumes
    while IFS= read -r volume; do printf './volumes/%s.tar\n' "$volume"; done < <(canonical_legacy_volume_names)
  } | LC_ALL=C sort -u >"$required_file"
  while IFS= read -r relative; do
    test "$(grep -Fxc "$relative" "$paths_file")" -eq 1 \
      || { rm -f "$paths_file" "$required_file"; die "backup checksum manifest omits required artifact: $relative"; }
  done <"$required_file"
  rm -f "$paths_file" "$required_file"
}

exact_utc_epoch_nanoseconds() {
  local timestamp=$1 label=${2:-timestamp}
  [[ $timestamp =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$ ]] \
    || die "$label is not canonical UTC"
  date -u -d "$timestamp" +%s%N 2>/dev/null || die "$label is not valid UTC"
}

assert_cold_clone_rehearsal_receipt() {
  local receipt=$1 expected_clone=${2:-} backup_sha pg_sha migrations_sha counts_sha completed_at
  assert_self_digest_json "$receipt"
  backup_sha=$(sha256sum "$BK/backup-SHA256SUMS" | awk '{print $1}')
  pg_sha=$(sha256sum "$BK/postgres.pgcustom" | awk '{print $1}')
  migrations_sha=$(sha256sum "$BK/migrations-before.tsv" | awk '{print $1}')
  counts_sha=$(sha256sum "$BK/table-counts-before.tsv" | awk '{print $1}')
  jq -e --arg a "$A" --arg clone "$expected_clone" --arg backup "$backup_sha" --arg pg "$pg_sha" \
    --arg migrations "$migrations_sha" --arg counts "$counts_sha" '
      .schema=="bonfire.cold-clone-rehearsal-receipt.v1" and .status=="complete" and
      .releaseCommit==$a and (.cloneId|type=="string" and length>0) and
      ($clone=="" or .cloneId==$clone) and .qualificationRun==true and
      .backupManifestSha256==$backup and
      .restoredVolumeCount==8 and
      (.restoredVolumes|sort)==(["digitalocean_caddy_config","digitalocean_caddy_data",
        "digitalocean_canonical_postgres","digitalocean_codex_home","digitalocean_codex_queue",
        "digitalocean_codex_runner_data","digitalocean_meeting_data","digitalocean_usage_ledger"]|sort) and
      .rawVolumeCompare==true and .postgresRestore==true and
      .postgresDumpSha256==$pg and .migrationRowsSha256==$migrations and
      .tableCountsSha256==$counts and
      (.completedAt|type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]{1,9})?Z$"))
    ' "$receipt" >/dev/null || die 'cold-clone rehearsal receipt does not bind exact A, all eight volumes, and PostgreSQL restore evidence'
  completed_at=$(jq -er .completedAt "$receipt")
  exact_utc_epoch_nanoseconds "$completed_at" 'cold-clone rehearsal receipt completedAt' >/dev/null
}

assert_release_source_evidence_receipt() {
  local receipt=$1 source_sha
  assert_release_source_archive_binding "$ADIR"
  assert_root_private_regular_file "$receipt" 'exact A release source evidence receipt'
  cmp "$ADIR/source-receipt.json" "$receipt" \
    || die 'release source evidence receipt differs from exact A source receipt'
  source_sha=$(sha256sum "$ADIR/source.tar" | awk '{print $1}')
  jq -e --arg a "$A" --arg source "$source_sha" '
    .schema=="bonfire.release-source.v3" and .releaseCommit==$a and .sourceArchiveSha256==$source
  ' "$receipt" >/dev/null || die 'release source evidence receipt does not bind exact A and its source archive'
}

assert_exact_legacy_container_topology_snapshot() {
  local inspect=$1
  jq -e '
    . as $root |
    def service($name): [$root[] | select(.Config.Labels["com.docker.compose.service"]==$name)];
    def volumes($name): [service($name)[0].Mounts[] | select(.Type=="volume") | {Name,Destination,RW}] | sort_by(.Destination);
    ([$root[] | .Config.Labels["com.docker.compose.service"]] | sort) ==
      (["caddy","canonical-postgres","codex-runner","coturn","meetingassist","render-runner"] | sort) and
    all(["caddy","canonical-postgres","codex-runner","coturn","meetingassist","render-runner"][];
      . as $name | [$root[] | select(.Config.Labels["com.docker.compose.service"]==$name)] | length==1) and
    volumes("caddy")==([
      {Name:"digitalocean_caddy_config",Destination:"/config",RW:true},
      {Name:"digitalocean_caddy_data",Destination:"/data",RW:true}]|sort_by(.Destination)) and
    volumes("canonical-postgres")==[{Name:"digitalocean_canonical_postgres",Destination:"/var/lib/postgresql/data",RW:true}] and
    volumes("meetingassist")==([
      {Name:"digitalocean_codex_queue",Destination:"/app/codex-queue",RW:true},
      {Name:"digitalocean_meeting_data",Destination:"/app/data",RW:true},
      {Name:"digitalocean_usage_ledger",Destination:"/app/data/usage",RW:true}]|sort_by(.Destination)) and
    volumes("codex-runner")==([
      {Name:"digitalocean_codex_queue",Destination:"/app/codex-queue",RW:true},
      {Name:"digitalocean_codex_runner_data",Destination:"/runner-data",RW:true},
      {Name:"digitalocean_usage_ledger",Destination:"/app/usage-ledger",RW:true}]|sort_by(.Destination)) and
    volumes("render-runner")==[{Name:"digitalocean_meeting_data",Destination:"/app/data",RW:true}] and
    (volumes("coturn") | length==1 and .[0].Destination=="/var/lib/coturn" and .[0].RW==true and
      (.[0].Name | type=="string" and length>0 and (startswith("digitalocean_")|not)))
  ' "$inspect" >/dev/null \
    || die 'legacy service identity or protected-volume mount inventory differs from the exact reviewed six-container topology'
}

phase_backup() {
  require_root; require_commands docker tar jq sha256sum; load_state; acquire_operator_lock; require_phase rooms-empty
  local volumes=(
    digitalocean_caddy_config digitalocean_caddy_data digitalocean_canonical_postgres
    digitalocean_codex_home digitalocean_codex_queue digitalocean_codex_runner_data
    digitalocean_meeting_data digitalocean_usage_ledger
  )
  printf '%s\n' "${volumes[@]}" | sort >"$BK/meta/expected-volumes"
  docker volume ls --format '{{.Name}}' | grep '^digitalocean_' | sort >"$BK/meta/actual-volumes"
  cmp "$BK/meta/expected-volumes" "$BK/meta/actual-volumes"
  ! docker volume inspect digitalocean_render_queue >/dev/null 2>&1 || die 'render_queue unexpectedly exists before backup'
  docker volume inspect "${volumes[@]}" >"$BK/private/volumes.inspect.json"

  mapfile -t containers < <(docker ps -aq --no-trunc --filter label=com.docker.compose.project=digitalocean)
  test "${#containers[@]}" -eq 6 || die 'legacy project container inventory changed'
  docker inspect "${containers[@]}" >"$BK/private/containers.inspect.json"
  assert_exact_legacy_container_topology_snapshot "$BK/private/containers.inspect.json"
  jq -r '.[] | [.Id,.Config.Labels["com.docker.compose.service"],.Image,
    ([.Mounts[]|select(.Type=="volume")|.Name]|sort|join(","))] | @tsv' \
    "$BK/private/containers.inspect.json" | sort >"$BK/meta/legacy-container-authority.tsv"
  mapfile -t refs < <(docker inspect "${containers[@]}" --format '{{.Config.Image}}' | sort -u)
  docker image inspect "${refs[@]}" >"$BK/meta/images.inspect.json"
  docker image save -o "$BK/images/legacy-images.tar" "${refs[@]}"
  jq -r '.[]|[.Config.Image,.Image]|@tsv' "$BK/private/containers.inspect.json" | sort -u >"$BK/meta/legacy-image-map.tsv"
  docker network inspect digitalocean_default digitalocean_render_internal >"$BK/meta/networks.inspect.json"
  docker ps -a --no-trunc --format '{{json .}}' >"$BK/meta/docker-ps-before.jsonl"
  docker system df -v >"$BK/meta/docker-df-before.txt"
  docker compose version --format json >"$BK/meta/compose-version.json"
  local_https "https://$HOST/healthz" >"$BK/meta/precutover-health.json"
  local_https "https://$HOST/readyz" >"$BK/meta/precutover-ready.json"

  install -m 600 "$BASE_ENV" "$BK/private/base.env"
  install -m 600 /opt/meetingassist/deploy/digitalocean/docker-compose.yml "$BK/private/legacy-docker-compose.yml"
  install -m 600 /opt/meetingassist/deploy/digitalocean/Caddyfile "$BK/private/legacy-Caddyfile"
  local legacy_config_files legacy_project_dir legacy_env_file legacy_path legacy_sha
  local legacy_compose_args=()
  legacy_config_files=$(jq -er '[.[]|.Config.Labels["com.docker.compose.project.config_files"]]|unique|select(length==1)|.[0]' \
    "$BK/private/containers.inspect.json")
  legacy_project_dir=$(jq -er '[.[]|.Config.Labels["com.docker.compose.project.working_dir"]]|unique|select(length==1)|.[0]' \
    "$BK/private/containers.inspect.json")
  legacy_env_file=$(jq -er '[.[]|.Config.Labels["com.docker.compose.project.environment_file"]]|unique|select(length==1)|.[0]' \
    "$BK/private/containers.inspect.json")
  test "$legacy_project_dir" = /opt/meetingassist/deploy/digitalocean \
    || die 'running predecessor Compose project directory is not exact'
  [[ $legacy_env_file == /* ]] || die 'running predecessor Compose environment path is not absolute'
  assert_root_private_regular_file "$legacy_env_file" 'running predecessor Compose environment file'
  cmp "$legacy_env_file" "$BASE_ENV" >/dev/null \
    || die 'running predecessor Compose environment differs from the exact live base environment'
  : >"$BK/private/legacy-compose-sources.jsonl"
  IFS=',' read -r -a legacy_paths <<<"$legacy_config_files"
  test "${#legacy_paths[@]}" -ge 1 || die 'running predecessor has no Compose source files'
  for legacy_path in "${legacy_paths[@]}"; do
    [[ $legacy_path == /* ]] || die 'running predecessor Compose source path is not absolute'
    test -f "$legacy_path" && test ! -L "$legacy_path" \
      || die 'running predecessor Compose source is not a regular file'
    legacy_sha=$(sha256sum "$legacy_path" | awk '{print $1}')
    require_sha256 "$legacy_sha"
    jq -cn --arg path "$legacy_path" --arg sha256 "$legacy_sha" '{path:$path,sha256:$sha256}' \
      >>"$BK/private/legacy-compose-sources.jsonl"
    legacy_compose_args+=(--file "$legacy_path")
  done
  BONFIRE_BASE_ENV_FILE="$BASE_ENV" docker compose \
    --project-name digitalocean --project-directory "$legacy_project_dir" \
    --env-file "$legacy_env_file" "${legacy_compose_args[@]}" \
    --profile codex --profile render config >"$BK/private/legacy-compose-resolved.yml.tmp"
  chown root:root "$BK/private/legacy-compose-resolved.yml.tmp"
  chmod 600 "$BK/private/legacy-compose-resolved.yml.tmp"
  mv "$BK/private/legacy-compose-resolved.yml.tmp" "$BK/private/legacy-compose-resolved.yml"
  jq -n --arg projectDirectory "$legacy_project_dir" --arg environmentFile "$legacy_env_file" \
    --arg environmentFileSha256 "$(sha256sum "$legacy_env_file" | awk '{print $1}')" \
    --arg resolvedComposeSha256 "$(sha256sum "$BK/private/legacy-compose-resolved.yml" | awk '{print $1}')" \
    --arg baseEnvironmentSha256 "$(sha256sum "$BK/private/base.env" | awk '{print $1}')" \
    --slurpfile sources "$BK/private/legacy-compose-sources.jsonl" '
      {schema:"bonfire.legacy-compose-provenance.v1",status:"complete",projectName:"digitalocean",
       projectDirectory:$projectDirectory,environmentFile:$environmentFile,
       environmentFileSha256:$environmentFileSha256,
       resolvedComposeSha256:$resolvedComposeSha256,baseEnvironmentSha256:$baseEnvironmentSha256,
       sourceConfigFiles:$sources}
    ' >"$BK/private/legacy-compose-provenance.raw"
  write_self_digest_json "$BK/private/legacy-compose-provenance.raw" "$BK/private/legacy-compose-provenance.json"
  rm "$BK/private/legacy-compose-provenance.raw" "$BK/private/legacy-compose-sources.jsonl"
  assert_legacy_compose_recovery_bundle
  tar --xattrs --acls --numeric-owner --one-file-system --exclude='meetingassist/data' \
    -C /opt -cpf "$BK/private/opt-meetingassist.tar" meetingassist
  tar --xattrs --acls --numeric-owner --one-file-system \
    -C /opt -cpf "$BK/private/opt-meetingassist-workspace.tar" meetingassist-workspace

  local service id pgc volume container mount
  for service in meetingassist render-runner render-queue-init codex-runner coturn caddy; do
    while IFS= read -r id; do test -z "$id" || docker stop "$id" >>"$BK/meta/stopped-containers.txt"; done \
      < <(docker ps -q --no-trunc --filter label=com.docker.compose.project=digitalocean --filter "label=com.docker.compose.service=$service")
  done
  pgc=$(project_service_id canonical-postgres)
  test -n "$pgc" || die 'canonical PostgreSQL is not the sole remaining service'
  for volume in "${volumes[@]}"; do
    while IFS= read -r container; do
      test -z "$container" || test "$container" = "$pgc" || die "$container still writes $volume"
    done < <(docker ps -q --no-trunc --filter "volume=$volume")
  done
  docker exec "$pgc" pg_dump -U bonfire -d bonfire -Fc --no-owner --no-acl >"$BK/postgres.pgcustom"
  docker exec -i "$pgc" pg_restore -l <"$BK/postgres.pgcustom" >"$BK/postgres.list"
  docker exec "$pgc" psql -XqAt -F $'\t' -v ON_ERROR_STOP=1 -U bonfire -d bonfire \
    -c "select version,encode(sha256,'hex') from schema_migrations order by version" >"$BK/migrations-before.tsv"
  pg_counts "$pgc" >"$BK/table-counts-before.tsv"
  docker stop "$pgc" >>"$BK/meta/stopped-containers.txt"
  test -z "$(docker ps -q --no-trunc --filter label=com.docker.compose.project=digitalocean)" || die 'a project writer still runs after quiescence'

  for volume in "${volumes[@]}"; do
    mount=$(docker volume inspect -f '{{.Mountpoint}}' "$volume")
    test -d "$mount" || die "missing volume mountpoint for $volume"
    tar --xattrs --acls --numeric-owner --one-file-system -C "$mount" -cpf "$BK/volumes/$volume.tar" .
    tar -tf "$BK/volumes/$volume.tar" >/dev/null
  done
  write_backup_checksum_manifest "$BK"
  assert_backup_checksum_manifest_semantic
  sync
  mark_phase backup
}

phase_rehearse() {
  require_root; load_state; acquire_operator_lock; require_phase backup
  assert_backup_checksum_manifest_semantic
  local volumes=(
    digitalocean_caddy_config digitalocean_caddy_data digitalocean_canonical_postgres
    digitalocean_codex_home digitalocean_codex_queue digitalocean_codex_runner_data
    digitalocean_meeting_data digitalocean_usage_ledger
  )
  local i=0 volume temporary mount
  for volume in "${volumes[@]}"; do
    i=$((i + 1)); temporary="bonfire_bootstrap_restore_${i}_$$"
    (
      set -Eeuo pipefail
      trap 'docker volume rm "$temporary" >/dev/null 2>&1 || true' EXIT
      docker volume create "$temporary" >/dev/null
      mount=$(docker volume inspect -f '{{.Mountpoint}}' "$temporary")
      tar --same-owner --xattrs --acls -xpf "$BK/volumes/$volume.tar" -C "$mount"
      tar --xattrs --acls --compare -f "$BK/volumes/$volume.tar" -C "$mount"
    )
  done

  (
    set -Eeuo pipefail
    local pg_volume="bonfire_bootstrap_pg_$$" pg_container="bonfire-bootstrap-pg-$$" pg_image
    cleanup_pg_rehearsal() {
      docker rm -f "$pg_container" >/dev/null 2>&1 || true
      docker volume rm "$pg_volume" >/dev/null 2>&1 || true
    }
    trap cleanup_pg_rehearsal EXIT
    docker volume create "$pg_volume" >/dev/null
    pg_image=$(jq -er '.sidecars.canonicalPostgres.imageId' "$ADIR/release-receipt.json")
    docker run -d --name "$pg_container" --network none \
      -e POSTGRES_USER=bonfire -e POSTGRES_DB=bonfire -e POSTGRES_HOST_AUTH_METHOD=trust \
      -v "$pg_volume:/var/lib/postgresql/data" "$pg_image" >/dev/null
    local ready=false
    for _ in $(seq 1 60); do
      if docker exec "$pg_container" pg_isready -U bonfire -d bonfire >/dev/null 2>&1; then ready=true; break; fi
      sleep 1
    done
    test "$ready" = true
    docker exec -i "$pg_container" pg_restore -U bonfire -d bonfire --no-owner --no-acl <"$BK/postgres.pgcustom"
    docker exec "$pg_container" psql -XqAt -F $'\t' -U bonfire -d bonfire \
      -c "select version,encode(sha256,'hex') from schema_migrations order by version" | cmp "$BK/migrations-before.tsv" -
    pg_counts "$pg_container" | cmp "$BK/table-counts-before.tsv" -
  )
  local clone_receipt_tmp="$BK/private/rehearsal-clone-receipt.raw.json"
  local rehearsal_clone="cold-rehearsal-exact-a-$A"
  jq -n --arg a "$A" --arg clone "$rehearsal_clone" \
    --arg backup "$(sha256sum "$BK/backup-SHA256SUMS" | awk '{print $1}')" \
    --arg pgdump "$(sha256sum "$BK/postgres.pgcustom" | awk '{print $1}')" \
    --arg migrations "$(sha256sum "$BK/migrations-before.tsv" | awk '{print $1}')" \
    --arg counts "$(sha256sum "$BK/table-counts-before.tsv" | awk '{print $1}')" \
    --arg completed "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" '
      {schema:"bonfire.cold-clone-rehearsal-receipt.v1",status:"complete",releaseCommit:$a,
       cloneId:$clone,qualificationRun:true,
       backupManifestSha256:$backup,restoredVolumeCount:8,
       restoredVolumes:["digitalocean_caddy_config","digitalocean_caddy_data","digitalocean_canonical_postgres",
         "digitalocean_codex_home","digitalocean_codex_queue","digitalocean_codex_runner_data",
         "digitalocean_meeting_data","digitalocean_usage_ledger"],rawVolumeCompare:true,
       postgresRestore:true,postgresDumpSha256:$pgdump,migrationRowsSha256:$migrations,
       tableCountsSha256:$counts,completedAt:$completed}
    ' >"$clone_receipt_tmp"
  write_self_digest_json "$clone_receipt_tmp" "$BK/private/rehearsal-clone-receipt.json"
  rm "$clone_receipt_tmp"
  assert_cold_clone_rehearsal_receipt "$BK/private/rehearsal-clone-receipt.json" "$rehearsal_clone"
  mark_phase rehearsed
}

assert_canonical_stage_container_exact() {
  local name=$1 expected_image=$2 role=$3 command_flag=$4 writable=$5 actual_image
  actual_image=$(docker inspect -f '{{.Image}}' "$name")
  test "$actual_image" = "$expected_image" || die "$role container does not use exact A image ID"
  docker inspect "$name" | jq -e \
    --arg network "$REPAIR_NETWORK" --arg runtime "$REPAIR_CEREMONY_DIR" \
    --arg role "$role" --arg command_flag "$command_flag" --argjson writable "$writable" '
      length==1 and
      .[0].Config.Labels["bonfire.bootstrap.role"]==$role and
      .[0].HostConfig.NetworkMode==$network and
      (.[0].NetworkSettings.Networks | keys)==[$network] and
      .[0].HostConfig.ReadonlyRootfs==true and
      .[0].HostConfig.RestartPolicy.Name=="no" and
      ((.[0].HostConfig.CapDrop // []) | index("ALL") != null) and
      ((.[0].HostConfig.SecurityOpt // []) | index("no-new-privileges:true") != null) and
      (.[0].HostConfig.PortBindings // {} | length)==0 and
      ((.[0].Config.Env // []) | index("BONFIRE_CODEX_QUEUE_PATH=/app/codex-queue/jobs") != null) and
      ((.[0].Config.Env // []) | index("BONFIRE_CODEX_HEARTBEAT_PATH=/app/codex-queue/heartbeat.json") != null) and
      ((.[0].Config.Env // []) | index("BONFIRE_RENDER_QUEUE_PATH=/app/render-queue/jobs") != null) and
      ((.[0].Config.Env // []) | index("BONFIRE_RENDER_HEARTBEAT_PATH=/app/render-queue/heartbeat.json") != null) and
      ([.[0].Mounts[] | select(.Type=="volume") | [.Name,.Destination,.RW]] | sort) ==
        ([
          ["digitalocean_codex_queue","/app/codex-queue",$writable],
          ["digitalocean_meeting_data","/app/data",$writable],
          ["digitalocean_render_queue","/app/render-queue",$writable],
          ["digitalocean_usage_ledger","/app/data/usage",$writable]
        ] | sort) and
      any(.[0].Mounts[]; .Type=="bind" and .Source==$runtime and .Destination=="/run/bonfire-repair" and .RW==true) and
      (.[0].Config.Cmd | index($command_flag) != null)
    ' >/dev/null || die "$role container confinement, mounts, queues, or exact command drifted"
  # The created container is stopped, so Docker omits it from network-inspect
  # active endpoints. Container inspect above proves its sole configured
  # network; exact active membership is checked across the start/run/stop
  # lifecycle below.
  assert_canonical_repair_network_membership "$(canonical_repair_postgres_id)"
}

create_canonical_stage_container() {
  local name=$1 role=$2 writable=$3 image=$4 command_flag=$5
  shift 5
  local mounts=(
    --volume "digitalocean_meeting_data:/app/data:$writable"
    --volume "digitalocean_usage_ledger:/app/data/usage:$writable"
    --volume "digitalocean_codex_queue:/app/codex-queue:$writable"
    --volume "digitalocean_render_queue:/app/render-queue:$writable"
  )
  docker create --name "$name" \
    --label "bonfire.bootstrap.role=$role" \
    --network "$REPAIR_NETWORK" --restart no --read-only --cap-drop ALL \
    --security-opt no-new-privileges:true --pids-limit 256 --memory 1024m \
    --tmpfs /tmp:rw,nosuid,nodev,noexec,size=256m \
    --env-file "$BASE_ENV" --env-file "$ADIR/release.env" \
    --env BONFIRE_CODEX_QUEUE_PATH=/app/codex-queue/jobs \
    --env BONFIRE_CODEX_HEARTBEAT_PATH=/app/codex-queue/heartbeat.json \
    --env BONFIRE_RENDER_QUEUE_PATH=/app/render-queue/jobs \
    --env BONFIRE_RENDER_HEARTBEAT_PATH=/app/render-queue/heartbeat.json \
    "${mounts[@]}" --volume "$REPAIR_CEREMONY_DIR:/run/bonfire-repair" \
    --entrypoint /app/meetingassist "$image" "$command_flag" "$@" >/dev/null
  if test "$writable" = rw; then
    assert_canonical_stage_container_exact "$name" "$image" "$role" "$command_flag" true
  else
    assert_canonical_stage_container_exact "$name" "$image" "$role" "$command_flag" false
  fi
  assert_canonical_repair_network_membership "$(canonical_repair_postgres_id)"
  assert_protected_volume_container_whitelist "$name"
}

run_canonical_stage_container() {
  local name=$1 log=$2 exit_file=$3 exit_code
  assert_canonical_repair_network_membership "$(canonical_repair_postgres_id)"
  docker start "$name" >/dev/null
  assert_canonical_repair_network_membership "$(canonical_repair_postgres_id)" "$name"
  docker wait "$name" >"$exit_file"
  assert_canonical_repair_network_membership "$(canonical_repair_postgres_id)"
  exit_code=$(tr -d '[:space:]' <"$exit_file")
  [[ $exit_code =~ ^[0-9]+$ ]] || die "$name returned an invalid exit code"
  docker logs "$name" >"$log" 2>&1 || true
  chmod 600 "$log" "$exit_file"
  test "$exit_code" -eq 0 || return "$exit_code"
}

assert_canonical_observation() {
  local observation=$1 expected_count=${2:-}
  assert_root_private_regular_file "$observation" 'canonical repair observation'
  jq -e --arg a "$A" --arg expected_count "$expected_count" '
    .schema=="bonfire.canonical-board-repair-observation.v1" and .releaseCommit==$a and
    .tenantId=="bonfire" and .dataDir=="/app/data" and
    (.databaseUrlSha256|type=="string" and test("^[0-9a-f]{64}$")) and
    (.databaseSha256|type=="string" and test("^[0-9a-f]{64}$")) and
    (.importInputSha256|type=="string" and test("^[0-9a-f]{64}$")) and
    (.candidateFingerprintSha256|type=="string" and test("^[0-9a-f]{64}$")) and
    (.proofFingerprintSha256|type=="string" and test("^[0-9a-f]{64}$")) and
    (.candidateCount|type=="number" and .>=0 and floor==.) and
    ($expected_count=="" or .candidateCount==($expected_count|tonumber)) and
    .principalParity==true and .projectionReplayValid==true and
    (.observedAt|type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T"))
  ' "$observation" >/dev/null || die 'exact A canonical observation is invalid'
}

write_normalization_fence_receipt() {
  local observation=$1 output=$2
  local inventory="$REPAIR_EVIDENCE_DIR/protected-container-fence.json" raw="$output.raw"
  assert_forward_maintenance_state
  assert_no_canonical_repair_writers
  docker ps -a --no-trunc --format '{{json .}}' | jq -sS . >"$inventory.tmp"
  chown root:root "$inventory.tmp"; chmod 600 "$inventory.tmp"; mv "$inventory.tmp" "$inventory"
  local backup_ref observation_ref inventory_ref
  backup_ref=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR")
  observation_ref=$(private_file_reference_json "$observation" "$REPAIR_EVIDENCE_DIR")
  inventory_ref=$(private_file_reference_json "$inventory" "$REPAIR_EVIDENCE_DIR")
  jq -n --arg a "$A" --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --argjson backup "$backup_ref" --argjson observation "$observation_ref" --argjson inventory "$inventory_ref" \
    '{schema:"bonfire.canonical-normalization-fence-receipt.v1",status:"complete",releaseCommit:$a,
      ingressBlocked:true,writersFenced:true,internalNetworkOnly:true,protectedContainerWhitelistExact:true,
      backupReceipt:$backup,beforeObservation:$observation,containerInventory:$inventory,createdAt:$created}' >"$raw"
  write_self_digest_json "$raw" "$output"
  rm "$raw"
  assert_self_digest_json "$output"
}

write_normalization_input() {
  local observation=$1 fence=$2 authority=$3 output=$4
  local raw="$output.raw"
  local backup_ref fence_ref authority_ref observation_ref
  backup_ref=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR")
  fence_ref=$(private_file_reference_json "$fence" "$REPAIR_EVIDENCE_DIR")
  authority_ref=$(private_file_reference_json "$authority" "$REPAIR_EVIDENCE_DIR")
  observation_ref=$(private_file_reference_json "$observation" "$REPAIR_EVIDENCE_DIR")
  jq -n --arg a "$A" --arg db "$(jq -er .databaseUrlSha256 "$observation")" \
    --arg clone "production-exact-a-$A" \
    --arg before "$(jq -er .proofFingerprintSha256 "$observation")" \
    --argjson backup "$backup_ref" --argjson fence "$fence_ref" \
    --argjson authority "$authority_ref" --argjson observation "$observation_ref" '
      {schema:"bonfire.canonical-board-normalization-input.v1",releaseCommit:$a,tenantId:"bonfire",cloneId:$clone,qualificationRun:false,
       dataDir:"/app/data",environment:"production_protected_maintenance",databaseUrlSha256:$db,
       evidenceDir:"/run/bonfire-repair/evidence",backupReceipt:$backup,fenceReceipt:$fence,
       normalizationAuthorityMarker:$authority,beforeObservation:$observation,
       beforeFingerprintSha256:$before,expectedTerminalCandidateCount:7,maxApplyPasses:8}
    ' >"$raw"
  install -o root -g root -m 600 "$raw" "$output"
  rm "$raw"
}

assert_canonical_normalization_receipt() {
  local receipt=$1 input_sha=$2 evidence_dir=${3:-$REPAIR_EVIDENCE_DIR} run_dir payload_sha backup_sha fence_sha observation_sha
  assert_root_private_regular_file "$receipt" 'canonical normalization receipt'
  run_dir=$(dirname "$receipt")
  payload_sha=$(jq -cS 'del(.receiptSha256)' "$receipt" | tr -d '\n' | sha256sum | awk '{print $1}')
  backup_sha=$(sha256sum "$evidence_dir/backup-SHA256SUMS" | awk '{print $1}')
  fence_sha=$(sha256sum "$run_dir/normalization-fence-receipt.json" | awk '{print $1}')
  observation_sha=$(sha256sum "$run_dir/before-observation.json" | awk '{print $1}')
  jq -e --arg a "$A" --arg input "$input_sha" --arg payload "$payload_sha" \
    --arg backup "$backup_sha" --arg fence "$fence_sha" --arg observation "$observation_sha" \
    --slurpfile beforeObservation "$run_dir/before-observation.json" --slurpfile normalizationInput "$run_dir/normalization-input.json" '
    def fileSeal:
      type=="object" and (.size|type=="number" and .>=0 and floor==.) and
      (.sha256|type=="string" and test("^[0-9a-f]{64}$"));
    def stateSeal:
      type=="object" and
      all([.tenantEventCount,.eventHighWater,.importOutboxCount,.versionEntryCount,.captureSpoolHighWater][];
        type=="number" and .>=0 and floor==.) and
      all([.versionEntriesSha256,.databaseSha256,.importInputSha256,.proofSha256,.candidateSha256][];
        type=="string" and test("^[0-9a-f]{64}$")) and
      (.candidateCount|type=="number" and .>=0 and floor==.) and
      (.board|fileSeal) and (.journal|fileSeal) and (.versionMap|fileSeal) and (.spool|fileSeal);
    .schema=="bonfire.canonical-board-normalization-receipt.v1" and .status=="complete" and
    .releaseCommit==$a and .version==$a and .tenantId=="bonfire" and
    .cloneId==$normalizationInput[0].cloneId and .environment==$normalizationInput[0].environment and
    .qualificationRun==$normalizationInput[0].qualificationRun and
    .normalizationInputSha256==$input and .receiptSha256==$payload and
    .backupReceiptSha256==$backup and .fenceReceiptSha256==$fence and
    .beforeObservationSha256==$observation and
    (.beforeFingerprintSha256|test("^[0-9a-f]{64}$")) and (.afterFingerprintSha256|test("^[0-9a-f]{64}$")) and
    (.beforeCandidateCount|type=="number" and .>=0 and floor==.) and
    (.beforeCandidateSha256|test("^[0-9a-f]{64}$")) and
    .afterCandidateCount==7 and (.afterCandidateSha256|test("^[0-9a-f]{64}$")) and
    (.applyPasses|type=="number" and .>=2 and .<=9 and floor==.) and .lifecycleAppendCount==0 and
    (.beforeState|stateSeal) and (.afterState|stateSeal) and
    ([ $beforeObservation[0].candidates[] |
       select(.Kind=="missing_event" or .Kind=="state_mismatch") |
       (.Family+"\u0000"+.ObjectID) ] | unique | length) as $ordinaryDelta and
    .delta=={tenantEvents:$ordinaryDelta,importOutbox:$ordinaryDelta,versionEntries:$ordinaryDelta} and
    (.afterState.tenantEventCount-.beforeState.tenantEventCount)==$ordinaryDelta and
    (.afterState.eventHighWater-.beforeState.eventHighWater)==$ordinaryDelta and
    (.afterState.importOutboxCount-.beforeState.importOutboxCount)==$ordinaryDelta and
    (.afterState.versionEntryCount-.beforeState.versionEntryCount)==$ordinaryDelta and
    .beforeState.board==.afterState.board and .beforeState.journal==.afterState.journal and
    .beforeState.spool==.afterState.spool and
    .beforeState.captureSpoolHighWater==.afterState.captureSpoolHighWater and
    .beforeState.importInputSha256==.afterState.importInputSha256 and .afterState.candidateCount==7 and
    .beforeState.candidateCount==$beforeObservation[0].candidateCount and
    .beforeState.candidateSha256==$beforeObservation[0].candidateFingerprintSha256 and
    .beforeState.proofSha256==$beforeObservation[0].proofFingerprintSha256 and
    .beforeState.tenantEventCount==$beforeObservation[0].tenantEventCount and
    .beforeState.eventHighWater==$beforeObservation[0].eventHighWater and
    .beforeState.importOutboxCount==$beforeObservation[0].outboxCount and
    .beforeState.versionEntryCount==$beforeObservation[0].versionEntryCount and
    .beforeState.versionEntriesSha256==$beforeObservation[0].versionEntriesSha256 and
    .beforeState.board==$beforeObservation[0].board and .beforeState.journal==$beforeObservation[0].journal and
    .beforeState.versionMap==$beforeObservation[0].versionMap and .beforeState.spool==$beforeObservation[0].spool and
    .beforeState.databaseSha256==$beforeObservation[0].databaseSha256 and
    .beforeState.importInputSha256==$beforeObservation[0].importInputSha256 and
    .journalBefore==.journalAfter and .exactTerminalSeven==true and .principalParity==true and
    .projectionReplayValid==true and .fullZeroDeltaSecondReplay==true and
    all([.journalBefore,.journalAfter,.boardAfter,.versionMapAfter,.spoolAfter][];
      (.size|type=="number" and .>=0 and floor==.) and (.sha256|test("^[0-9a-f]{64}$"))) and
    all([.versionEntriesSha256,.databaseAfterSha256][]; test("^[0-9a-f]{64}$")) and
    (.eventHighWater|type=="number" and .>=0 and floor==.) and
    .eventHighWater==.afterState.eventHighWater and .tenantEventCount==.afterState.tenantEventCount and
    .outboxCount==.afterState.importOutboxCount and .versionEntryCount==.afterState.versionEntryCount and
    .versionEntriesSha256==.afterState.versionEntriesSha256 and
    .captureSpoolHighWater==.afterState.captureSpoolHighWater and
    .boardAfter==.afterState.board and .journalAfter==.afterState.journal and
    .versionMapAfter==.afterState.versionMap and .spoolAfter==.afterState.spool and
    .databaseAfterSha256==.afterState.databaseSha256 and
    (.captureSpoolHighWater|type=="number" and .>=0 and floor==.) and
    (.outboxCount|type=="number" and .>=0 and floor==.) and
    (.versionEntryCount|type=="number" and .>=0 and floor==.) and
    (.completedAt|type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T"))
  ' "$receipt" >/dev/null || die 'canonical normalization receipt is not exact-seven, lifecycle-zero, and full-replay-stable'
}

assert_production_normalization_input() {
  local input=$1 observation=$2 fence=$3 authority=$4 backup_ref observation_ref fence_ref authority_ref expected
  assert_root_private_regular_file "$input" 'production normalization input'
  assert_root_private_regular_file "$observation" 'production before observation'
  assert_root_private_regular_file "$fence" 'production normalization fence receipt'
  assert_root_private_regular_file "$authority" 'production normalization authority marker'
  assert_self_digest_json "$fence"
  expected=$(mktemp "$STATE_DIR/normalization-authority-expected.XXXXXX")
  printf 'AUTHORIZE CANONICAL BOARD NORMALIZATION %s %s\n' \
    "$(sha256sum "$observation"|awk '{print $1}')" \
    "$(sha256sum "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS"|awk '{print $1}')" >"$expected"
  cmp "$expected" "$authority" || { rm -f "$expected"; die 'production normalization authority marker bytes drifted'; }
  rm -f "$expected"
  backup_ref=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR")
  observation_ref=$(private_file_reference_json "$observation" "$REPAIR_EVIDENCE_DIR")
  fence_ref=$(private_file_reference_json "$fence" "$REPAIR_EVIDENCE_DIR")
  authority_ref=$(private_file_reference_json "$authority" "$REPAIR_EVIDENCE_DIR")
  jq -e --arg a "$A" --arg clone "production-exact-a-$A" \
    --arg db "$(jq -er .databaseUrlSha256 "$observation")" \
    --arg before "$(jq -er .proofFingerprintSha256 "$observation")" \
    --argjson backup "$backup_ref" --argjson observation "$observation_ref" \
    --argjson fence "$fence_ref" --argjson authority "$authority_ref" '
      .schema=="bonfire.canonical-board-normalization-input.v1" and .releaseCommit==$a and
      .tenantId=="bonfire" and .cloneId==$clone and .qualificationRun==false and
      .dataDir=="/app/data" and .environment=="production_protected_maintenance" and
      .databaseUrlSha256==$db and .evidenceDir=="/run/bonfire-repair/evidence" and
      .backupReceipt==$backup and .fenceReceipt==$fence and
      .normalizationAuthorityMarker==$authority and .beforeObservation==$observation and
      .beforeFingerprintSha256==$before and .expectedTerminalCandidateCount==7 and .maxApplyPasses==8
    ' "$input" >/dev/null || die 'production normalization input schema or sealed authority binding drifted'
}

run_canonical_normalization_after_setup() {
  local pgc=$1 image observation fence authority input input_sha receipt
  ensure_canonical_repair_render_queue_volume
  ensure_canonical_repair_network "$pgc"
  start_canonical_repair_postgres "$pgc"
  image=$(jq -er '.images.meetingassist.imageId' "$ADIR/release-receipt.json")
  observation="$REPAIR_EVIDENCE_DIR/before-observation.json"
  create_canonical_stage_container "$MANIFEST_CONTAINER" canonical-observation ro "$image" --observe-canonical-repair \
    --repair-observation /run/bonfire-repair/evidence/before-observation.json
  if ! run_canonical_stage_container "$MANIFEST_CONTAINER" "$BK/meta/canonical-before-observation.log" "$BK/private/canonical-before-observation.exit"; then
    mark_phase canonical-normalization-failed; die 'exact A pre-normalization observation failed; run the exact cold restore'
  fi
  docker rm "$MANIFEST_CONTAINER" >/dev/null
  assert_canonical_observation "$observation"
  authority="$REPAIR_EVIDENCE_DIR/normalization-authority-marker"
  printf 'AUTHORIZE CANONICAL BOARD NORMALIZATION %s %s\n' \
    "$(sha256sum "$observation" | awk '{print $1}')" \
    "$(sha256sum "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" | awk '{print $1}')" >"$authority.tmp"
  chown root:root "$authority.tmp"; chmod 600 "$authority.tmp"; mv "$authority.tmp" "$authority"
  fence="$REPAIR_EVIDENCE_DIR/normalization-fence-receipt.json"
  write_normalization_fence_receipt "$observation" "$fence"
  input="$NORMALIZATION_INPUT_PATH"
  write_normalization_input "$observation" "$fence" "$authority" "$input"
  assert_production_normalization_input "$input" "$observation" "$fence" "$authority"
  input_sha=$(sha256sum "$input" | awk '{print $1}')
  require_sha256 "$input_sha"
  receipt="$NORMALIZATION_RECEIPT_PATH"
  mark_phase canonical-normalization-started
  create_canonical_stage_container "$NORMALIZE_CONTAINER" canonical-normalization rw "$image" --normalize-canonical \
    --normalization-input /run/bonfire-repair/evidence/normalization-input.json \
    --normalization-input-sha256 "$input_sha" \
    --normalization-receipt /run/bonfire-repair/evidence/normalization-receipt.json
  if ! run_canonical_stage_container "$NORMALIZE_CONTAINER" "$BK/meta/canonical-normalization.log" "$BK/private/canonical-normalization.exit"; then
    mark_phase canonical-normalization-failed; die 'exact A normalization failed; run the exact cold restore before any further forward phase'
  fi
  assert_canonical_normalization_receipt "$receipt" "$input_sha"
  docker rm "$NORMALIZE_CONTAINER" >/dev/null
  create_canonical_stage_container "$MANIFEST_CONTAINER" canonical-observation ro "$image" --observe-canonical-repair \
    --repair-observation /run/bonfire-repair/evidence/normalized-observation.json
  if ! run_canonical_stage_container "$MANIFEST_CONTAINER" "$BK/meta/canonical-normalized-observation.log" "$BK/private/canonical-normalized-observation.exit"; then
    mark_phase canonical-normalization-failed; die 'normalized observation failed; run the exact cold restore'
  fi
  docker rm "$MANIFEST_CONTAINER" >/dev/null
  assert_canonical_observation "$REPAIR_OBSERVATION_PATH" 7
  test "$(jq -er .proofFingerprintSha256 "$REPAIR_OBSERVATION_PATH")" = "$(jq -er .afterFingerprintSha256 "$receipt")" \
    || { mark_phase canonical-normalization-failed; die 'normalized observation differs from normalization receipt; run the exact cold restore'; }
  cleanup_canonical_repair_runtime "$pgc" || die 'normalization runtime cleanup was incomplete'
  assert_forward_maintenance_state
  assert_no_canonical_repair_writers
}

revalidate_completed_canonical_normalization() {
  local pgc input_sha image live_observation
  ! phase_done canonical-normalization-failed \
    || die 'completed normalization also carries a failure marker; exact cold restore is required'
  assert_forward_maintenance_state
  assert_backup_checksum_manifest_semantic
  assert_cold_clone_rehearsal_receipt "$BK/private/rehearsal-clone-receipt.json" "cold-rehearsal-exact-a-$A"
  assert_release_source_archive_binding "$ADIR"
  cmp "$BK/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS"
  assert_cold_clone_rehearsal_receipt "$REPAIR_EVIDENCE_DIR/cold-clone-rehearsal-receipt.json" "cold-rehearsal-exact-a-$A"
  assert_release_source_evidence_receipt "$REPAIR_EVIDENCE_DIR/release-source-receipt.json"
  assert_canonical_observation "$REPAIR_EVIDENCE_DIR/before-observation.json"
  assert_production_normalization_input "$NORMALIZATION_INPUT_PATH" \
    "$REPAIR_EVIDENCE_DIR/before-observation.json" \
    "$REPAIR_EVIDENCE_DIR/normalization-fence-receipt.json" \
    "$REPAIR_EVIDENCE_DIR/normalization-authority-marker"
  input_sha=$(sha256sum "$NORMALIZATION_INPUT_PATH"|awk '{print $1}')
  require_sha256 "$input_sha"
  assert_canonical_normalization_receipt "$NORMALIZATION_RECEIPT_PATH" "$input_sha"
  assert_canonical_observation "$REPAIR_OBSERVATION_PATH" 7
  test "$(jq -er .proofFingerprintSha256 "$REPAIR_OBSERVATION_PATH")" = \
    "$(jq -er .afterFingerprintSha256 "$NORMALIZATION_RECEIPT_PATH")" \
    || die 'completed normalized observation differs from its receipt'
  assert_canonical_repair_render_queue_volume
  ! docker network inspect "$REPAIR_NETWORK" >/dev/null 2>&1 \
    || die 'completed normalization retained its internal network'
  local owned
  for owned in "$NORMALIZE_CONTAINER" "$MANIFEST_CONTAINER" "$REPAIR_CONTAINER"; do
    ! docker inspect "$owned" >/dev/null 2>&1 || die 'completed normalization retained an owned one-shot'
  done
  pgc=$(canonical_repair_postgres_id)
  test "$(docker inspect -f '{{.State.Running}}' "$pgc")" = false \
    || die 'completed normalization retained running PostgreSQL'
  docker inspect "$pgc" | jq -e '(.[0].NetworkSettings.Networks|keys|length)==0' >/dev/null \
    || die 'completed normalization retained PostgreSQL network attachment'
  assert_no_canonical_repair_writers

  ensure_canonical_repair_network "$pgc"
  start_canonical_repair_postgres "$pgc"
  trap 'docker rm -f "$MANIFEST_CONTAINER" >/dev/null 2>&1 || true; cleanup_canonical_repair_runtime "$pgc" >/dev/null 2>&1 || true' EXIT
  image=$(jq -er '.images.meetingassist.imageId' "$ADIR/release-receipt.json")
  live_observation="$REPAIR_EVIDENCE_DIR/completed-normalization-live-observation.json"
  create_canonical_stage_container "$MANIFEST_CONTAINER" canonical-normalization-live-revalidation ro "$image" --observe-canonical-repair \
    --repair-observation /run/bonfire-repair/evidence/completed-normalization-live-observation.json
  run_canonical_stage_container "$MANIFEST_CONTAINER" \
    "$BK/meta/completed-normalization-live-observation.log" \
    "$BK/private/completed-normalization-live-observation.exit"
  docker rm "$MANIFEST_CONTAINER" >/dev/null
  assert_canonical_observation "$live_observation" 7
  jq -e --slurpfile receipt "$NORMALIZATION_RECEIPT_PATH" '
    .databaseSha256==$receipt[0].afterState.databaseSha256 and
    .importInputSha256==$receipt[0].afterState.importInputSha256 and
    .board==$receipt[0].afterState.board and .journal==$receipt[0].afterState.journal and
    .versionMap==$receipt[0].afterState.versionMap and
    .versionEntriesSha256==$receipt[0].afterState.versionEntriesSha256 and
    .spool==$receipt[0].afterState.spool and
    .proofFingerprintSha256==$receipt[0].afterState.proofSha256 and
    .candidateCount==$receipt[0].afterState.candidateCount and
    .candidateFingerprintSha256==$receipt[0].afterState.candidateSha256 and
    .tenantEventCount==$receipt[0].afterState.tenantEventCount and
    .eventHighWater==$receipt[0].afterState.eventHighWater and
    .outboxCount==$receipt[0].afterState.importOutboxCount and
    .versionEntryCount==$receipt[0].afterState.versionEntryCount
  ' "$live_observation" >/dev/null || die 'exact-A live normalized state differs from the sealed normalization after-state'
  jq -S 'del(.observedAt)' "$REPAIR_OBSERVATION_PATH" | \
    cmp - <(jq -S 'del(.observedAt)' "$live_observation") \
    || die 'exact-A live normalized observation differs from the original complete observation'
  cleanup_canonical_repair_runtime "$pgc" || die 'completed normalization live revalidation cleanup was incomplete'
  trap - EXIT
  ! docker network inspect "$REPAIR_NETWORK" >/dev/null 2>&1 \
    || die 'completed normalization live revalidation retained its internal network'
  for owned in "$NORMALIZE_CONTAINER" "$MANIFEST_CONTAINER" "$REPAIR_CONTAINER"; do
    ! docker inspect "$owned" >/dev/null 2>&1 || die 'completed normalization live revalidation retained an owned one-shot'
  done
  test "$(docker inspect -f '{{.State.Running}}' "$pgc")" = false \
    || die 'completed normalization live revalidation retained running PostgreSQL'
  docker inspect "$pgc" | jq -e '(.[0].NetworkSettings.Networks|keys|length)==0' >/dev/null \
    || die 'completed normalization live revalidation retained PostgreSQL network attachment'
  assert_no_canonical_repair_writers
}

phase_normalize_canonical() {
  require_root; require_commands docker jq sha256sum; load_state; acquire_operator_lock; require_phase rehearsed
  local pgc status
  if phase_done canonical-normalized; then
    set +e
    ( set -Eeuo pipefail; revalidate_completed_canonical_normalization )
    status=$?
    set -e
    if test "$status" -ne 0; then
      mark_phase canonical-normalization-failed
      die 'completed normalization failed full revalidation; run the exact cold restore'
    fi
    return 0
  fi
  if phase_done canonical-normalization-setup-started || phase_done canonical-normalization-started \
    || phase_done canonical-normalization-failed; then
    mark_phase canonical-normalization-failed
    die 'normalization setup or execution crossed a process boundary; run the exact cold restore'
  fi
  assert_forward_maintenance_state
  assert_repair_source_volumes_match_rehearsed_backup
  assert_backup_checksum_manifest_semantic
  assert_cold_clone_rehearsal_receipt "$BK/private/rehearsal-clone-receipt.json" "cold-rehearsal-exact-a-$A"
  assert_release_source_archive_binding "$ADIR"
  assert_no_canonical_repair_writers
  install -d -o root -g root -m 700 "$REPAIR_CEREMONY_DIR" "$REPAIR_EVIDENCE_DIR"
  install -o root -g root -m 600 "$BK/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS"
  install -o root -g root -m 600 "$BK/private/rehearsal-clone-receipt.json" "$REPAIR_EVIDENCE_DIR/cold-clone-rehearsal-receipt.json"
  install -o root -g root -m 600 "$ADIR/source-receipt.json" "$REPAIR_EVIDENCE_DIR/release-source-receipt.json"
  cmp "$BK/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS"
  assert_cold_clone_rehearsal_receipt "$REPAIR_EVIDENCE_DIR/cold-clone-rehearsal-receipt.json" "cold-rehearsal-exact-a-$A"
  assert_release_source_evidence_receipt "$REPAIR_EVIDENCE_DIR/release-source-receipt.json"
  pgc=$(canonical_repair_postgres_id)
  mark_phase canonical-normalization-setup-started
  set +e
  ( set -Eeuo pipefail; run_canonical_normalization_after_setup "$pgc" )
  status=$?
  set -e
  if test "$status" -ne 0; then
    mark_phase canonical-normalization-failed
    die 'normalization setup, schema, input, observation, or execution failed; run the exact cold restore'
  fi
  mark_phase canonical-normalized
}

assert_classified_target_evidence_input() {
  local input=$CLASSIFIED_TARGET_EVIDENCE_PATH reference path resolved evidence_root size digest
  assert_root_private_regular_file "$input" 'classified target evidence input'
  jq -e --arg a "$A" '
    .schema=="bonfire.canonical-board-repair-target-evidence.v1" and
    .releaseCommit==$a and .tenantId=="bonfire" and
    (.targets|type=="array" and length==7 and
      (map(.objectId)==(map(.objectId)|sort)) and (map(.objectId)|unique|length)==7 and
      all(.[];
        (.objectId|type=="string" and length>0) and
        (.observedAbsenceAt|type=="string" and test("Z$")) and
        (.selectedStateRole=="source_record" or .selectedStateRole=="archive_record" or
          .selectedStateRole=="positive_observation") and
        (.evidenceBasis=="done_archive_absence" or .evidenceBasis=="last_positive_source_current_absence") and
        all([.sourceRecord,.archiveRecord,.positiveObservation,.absenceEvidence][];
          (.path|type=="string" and length>0) and (.path|startswith("/")|not) and
          (.size|type=="number" and .>=0 and floor==.) and (.sha256|test("^[0-9a-f]{64}$")))))
  ' "$input" >/dev/null || die 'classified target evidence input is not the exact sorted seven-target contract'
  evidence_root=$(readlink -f "$REPAIR_EVIDENCE_DIR")
  while IFS= read -r reference; do
    path=$(jq -er .path <<<"$reference")
    [[ $path != ../* && $path != */../* && $path != */./* ]] || die 'classified target evidence reference escapes its directory'
    path="$REPAIR_EVIDENCE_DIR/$path"
    resolved=$(readlink -f "$path")
    test "$resolved" = "$path" && [[ $resolved == "$evidence_root/"* ]] \
      || die 'classified target evidence path contains a symlink or escapes its root'
    assert_root_private_regular_file "$path" 'classified target evidence record'
    size=$(jq -er .size <<<"$reference")
    digest=$(jq -er .sha256 <<<"$reference")
    test "$(stat -c %s "$path")" -eq "$size" && test "$(sha256sum "$path" | awk '{print $1}')" = "$digest" \
      || die 'classified target evidence record differs from its input seal'
  done < <(jq -c '.targets[].sourceRecord,.targets[].archiveRecord,.targets[].positiveObservation,.targets[].absenceEvidence' "$input")
  assert_semantic_target_evidence_records "$input"

  jq -e --slurpfile observation "$REPAIR_OBSERVATION_PATH" '
    ([.targets[].objectId]|sort)==([$observation[0].targets[].objectId]|sort)
  ' "$input" >/dev/null || die 'classified target identities do not exactly match the normalized production observation'
  local target object_id role field record selected_sha observed_sha
  while IFS= read -r target; do
    object_id=$(jq -er .objectId <<<"$target")
    role=$(jq -er .selectedStateRole <<<"$target")
    case "$role" in
      source_record) field=sourceRecord ;;
      archive_record) field=archiveRecord ;;
      positive_observation) field=positiveObservation ;;
    esac
    record="$REPAIR_EVIDENCE_DIR/$(jq -er --arg field "$field" '.[$field].path' <<<"$target")"
    selected_sha=$(jq -er .stateSha256 "$record")
    observed_sha=$(jq -er --arg object "$object_id" '.targets[]|select(.objectId==$object)|.stateSha256' "$REPAIR_OBSERVATION_PATH")
    test "$selected_sha" = "$observed_sha" \
      || die 'selected classified target state does not match the normalized production candidate state'
  done < <(jq -c '.targets[]' "$input")
}

write_production_classified_evidence_descriptor() {
  local raw="$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH.raw" backup normalization qualification source observation
  test ! -e "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH" || die 'production classified evidence descriptor already exists before pack generation'
  backup=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR")
  normalization=$(private_file_reference_json "$NORMALIZATION_RECEIPT_PATH" "$REPAIR_EVIDENCE_DIR")
  qualification=$(private_file_reference_json "$CLONE_QUALIFICATION_PATH" "$REPAIR_EVIDENCE_DIR")
  source=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/release-source-receipt.json" "$REPAIR_EVIDENCE_DIR")
  observation=$(private_file_reference_json "$REPAIR_OBSERVATION_PATH" "$REPAIR_EVIDENCE_DIR")
  jq -n --arg a "$A" --arg clone "production-exact-a-$A" \
    --argjson backup "$backup" --argjson normalization "$normalization" \
    --argjson qualification "$qualification" --argjson source "$source" --argjson observation "$observation" \
    --slurpfile targetInput "$CLASSIFIED_TARGET_EVIDENCE_PATH" '
      {schema:"bonfire.canonical-board-repair-evidence.v1",releaseCommit:$a,tenantId:"bonfire",
       dataDir:"/app/data",cloneId:$clone,environment:"production_protected_maintenance",
       qualificationRun:false,backupManifest:$backup,normalizationReceipt:$normalization,
       cloneAuthority:$qualification,releaseSourceReceipt:$source,normalizedObservation:$observation,
       targets:$targetInput[0].targets}
    ' >"$raw"
  install -o root -g root -m 600 "$raw" "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH"
  rm "$raw"
}

assert_clone_network_membership() {
  local network=$1 pg=$2 owned=${3:-} pg_id owned_id expected
  pg_id=$(docker inspect -f '{{.Id}}' "$pg")
  require_sha256 "$pg_id"
  if test -n "$owned"; then
    owned_id=$(docker inspect -f '{{.Id}}' "$owned")
    require_sha256 "$owned_id"
    expected=$(jq -cn --arg pg "$pg_id" --arg owned "$owned_id" '[$pg,$owned]|sort')
  else
    expected=$(jq -cn --arg pg "$pg_id" '[$pg]')
  fi
  docker network inspect "$network" | jq -e --argjson expected "$expected" '
    length==1 and .[0].Internal==true and
    .[0].Labels["bonfire.bootstrap.role"]=="canonical-repair-clone-qualification" and
    ((.[0].Containers // {})|keys|sort)==$expected
  ' >/dev/null || die 'qualification clone network contains an unknown endpoint'
}

create_clone_stage_container() {
  local name=$1 role=$2 writable=$3 image=$4 network=$5 pg=$6 meeting=$7 usage=$8 codex=$9 render=${10} command_flag=${11}
  shift 11
  docker create --name "$name" \
    --label "bonfire.bootstrap.role=$role" --label bonfire.bootstrap.clone-qualification=true \
    --network "$network" --restart no --read-only --cap-drop ALL \
    --security-opt no-new-privileges:true --pids-limit 256 --memory 1024m \
    --tmpfs /tmp:rw,nosuid,nodev,noexec,size=256m \
    --env-file "$BASE_ENV" --env-file "$ADIR/release.env" \
    --env BONFIRE_CODEX_QUEUE_PATH=/app/codex-queue/jobs \
    --env BONFIRE_CODEX_HEARTBEAT_PATH=/app/codex-queue/heartbeat.json \
    --env BONFIRE_RENDER_QUEUE_PATH=/app/render-queue/jobs \
    --env BONFIRE_RENDER_HEARTBEAT_PATH=/app/render-queue/heartbeat.json \
    --volume "$meeting:/app/data:$writable" --volume "$usage:/app/data/usage:$writable" \
    --volume "$codex:/app/codex-queue:$writable" --volume "$render:/app/render-queue:$writable" \
    --volume "$REPAIR_CEREMONY_DIR:/run/bonfire-repair" \
    --entrypoint /app/meetingassist "$image" "$command_flag" "$@" >/dev/null
  local writable_json=false
  test "$writable" = rw && writable_json=true
  docker inspect "$name" | jq -e --arg image "$image" --arg network "$network" \
    --arg runtime "$REPAIR_CEREMONY_DIR" --arg meeting "$meeting" --arg usage "$usage" \
    --arg codex "$codex" --arg render "$render" --arg role "$role" \
    --arg command "$command_flag" --argjson writable "$writable_json" '
      length==1 and .[0].Image==$image and .[0].Config.Labels["bonfire.bootstrap.role"]==$role and
      .[0].Config.Labels["bonfire.bootstrap.clone-qualification"]=="true" and
      .[0].HostConfig.NetworkMode==$network and (.[0].NetworkSettings.Networks|keys)==[$network] and
      .[0].HostConfig.ReadonlyRootfs==true and .[0].HostConfig.RestartPolicy.Name=="no" and
      ((.[0].HostConfig.CapDrop//[])|index("ALL")!=null) and
      ((.[0].HostConfig.SecurityOpt//[])|index("no-new-privileges:true")!=null) and
      (.[0].HostConfig.PortBindings//{}|length)==0 and
      ([.[0].Mounts[]|select(.Type=="volume")|[.Name,.Destination,.RW]]|sort)==
        ([[$meeting,"/app/data",$writable],[$usage,"/app/data/usage",$writable],
          [$codex,"/app/codex-queue",$writable],[$render,"/app/render-queue",$writable]]|sort) and
      any(.[0].Mounts[];.Type=="bind" and .Source==$runtime and .Destination=="/run/bonfire-repair" and .RW==true) and
      (.[0].Config.Cmd|index($command)!=null)
    ' >/dev/null || die 'qualification clone one-shot confinement, image, mounts, or command drifted'
  assert_clone_network_membership "$network" "$pg" "$name"
}

run_clone_stage_container() {
  local name=$1 pg=$2 network=$3 log=$4 exit_file=$5 exit_code
  assert_clone_network_membership "$network" "$pg" "$name"
  docker start "$name" >/dev/null
  assert_clone_network_membership "$network" "$pg" "$name"
  docker wait "$name" >"$exit_file"
  assert_clone_network_membership "$network" "$pg" "$name"
  exit_code=$(tr -d '[:space:]' <"$exit_file")
  [[ $exit_code =~ ^[0-9]+$ ]] || die 'qualification clone one-shot returned an invalid exit code'
  docker logs "$name" >"$log" 2>&1 || true
  chmod 600 "$log" "$exit_file"
  test "$exit_code" -eq 0
}

write_clone_normalization_input() {
  local run_dir=$1 clone_id=$2 observation=$3 output=$4 evidence_dir=$REPAIR_EVIDENCE_DIR
  local fence="$run_dir/normalization-fence-receipt.json" authority="$run_dir/normalization-authority-marker"
  local inventory="$run_dir/container-fence.json" raw="$output.raw" backup_ref observation_ref inventory_ref
  local fence_ref authority_ref
  docker ps -a --no-trunc --format '{{json .}}' | jq -sS . >"$inventory.tmp"
  install -o root -g root -m 600 "$inventory.tmp" "$inventory"; rm "$inventory.tmp"
  backup_ref=$(private_file_reference_json "$evidence_dir/backup-SHA256SUMS" "$evidence_dir")
  observation_ref=$(private_file_reference_json "$observation" "$evidence_dir")
  inventory_ref=$(private_file_reference_json "$inventory" "$evidence_dir")
  jq -n --arg a "$A" --arg clone "$clone_id" --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --argjson backup "$backup_ref" --argjson observation "$observation_ref" --argjson inventory "$inventory_ref" '
      {schema:"bonfire.canonical-normalization-fence-receipt.v1",status:"complete",releaseCommit:$a,
       cloneId:$clone,ingressBlocked:true,writersFenced:true,internalNetworkOnly:true,
       protectedContainerWhitelistExact:true,backupReceipt:$backup,beforeObservation:$observation,
       containerInventory:$inventory,createdAt:$created}
    ' >"$fence.raw"
  write_self_digest_json "$fence.raw" "$fence"; rm "$fence.raw"
  printf 'AUTHORIZE CANONICAL BOARD NORMALIZATION %s %s %s\n' "$clone_id" \
    "$(sha256sum "$observation"|awk '{print $1}')" "$(sha256sum "$evidence_dir/backup-SHA256SUMS"|awk '{print $1}')" >"$authority.raw"
  install -o root -g root -m 600 "$authority.raw" "$authority"; rm "$authority.raw"
  fence_ref=$(private_file_reference_json "$fence" "$evidence_dir")
  authority_ref=$(private_file_reference_json "$authority" "$evidence_dir")
  jq -n --arg a "$A" --arg clone "$clone_id" --arg db "$(jq -er .databaseUrlSha256 "$observation")" \
    --arg before "$(jq -er .proofFingerprintSha256 "$observation")" \
    --argjson backup "$backup_ref" --argjson fence "$fence_ref" \
    --argjson authority "$authority_ref" --argjson observation "$observation_ref" '
      {schema:"bonfire.canonical-board-normalization-input.v1",releaseCommit:$a,tenantId:"bonfire",cloneId:$clone,qualificationRun:true,
       dataDir:"/app/data",environment:"isolated_cold_clone",databaseUrlSha256:$db,
       evidenceDir:"/run/bonfire-repair/evidence",backupReceipt:$backup,fenceReceipt:$fence,
       normalizationAuthorityMarker:$authority,beforeObservation:$observation,
       beforeFingerprintSha256:$before,expectedTerminalCandidateCount:7,maxApplyPasses:8}
    ' >"$raw"
  install -o root -g root -m 600 "$raw" "$output"; rm "$raw"
}

write_clone_run_descriptor() {
  local run_dir=$1 clone_id=$2 normalization=$3 authority=$4 observation=$5 output=$6
  local raw="$output.raw"
  local backup_ref normalization_ref authority_ref source_ref observation_ref
  backup_ref=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR")
  normalization_ref=$(private_file_reference_json "$normalization" "$REPAIR_EVIDENCE_DIR")
  authority_ref=$(private_file_reference_json "$authority" "$REPAIR_EVIDENCE_DIR")
  source_ref=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/release-source-receipt.json" "$REPAIR_EVIDENCE_DIR")
  observation_ref=$(private_file_reference_json "$observation" "$REPAIR_EVIDENCE_DIR")
  jq -n --arg a "$A" --arg clone "$clone_id" --argjson backup "$backup_ref" \
    --argjson normalization "$normalization_ref" --argjson authority "$authority_ref" \
    --argjson source "$source_ref" --argjson observation "$observation_ref" \
    --slurpfile targetInput "$CLASSIFIED_TARGET_EVIDENCE_PATH" '
      {schema:"bonfire.canonical-board-repair-evidence.v1",releaseCommit:$a,tenantId:"bonfire",
       dataDir:"/app/data",cloneId:$clone,environment:"isolated_cold_clone",qualificationRun:true,
       backupManifest:$backup,normalizationReceipt:$normalization,cloneAuthority:$authority,
       releaseSourceReceipt:$source,normalizedObservation:$observation,targets:$targetInput[0].targets}
    ' >"$raw"
  install -o root -g root -m 600 "$raw" "$output"; rm "$raw"
}

assert_clone_restart_observation() {
  local receipt=$1 clone_id=$2 normalization=$3 repair=$4 normalization_sha repair_sha payload_sha
  normalization_sha=$(sha256sum "$normalization"|awk '{print $1}')
  repair_sha=$(sha256sum "$repair"|awk '{print $1}')
  payload_sha=$(jq -cS 'del(.receiptSha256)' "$receipt"|tr -d '\n'|sha256sum|awk '{print $1}')
  jq -e --arg a "$A" --arg clone "$clone_id" --arg normalization "$normalization_sha" \
    --arg repair "$repair_sha" --arg payload "$payload_sha" --slurpfile repairReceipt "$repair" '
      .schema=="bonfire.canonical-board-repair-restart-observation.v1" and .status=="complete" and
      .releaseCommit==$a and .cloneId==$clone and .environment=="isolated_cold_clone" and .qualificationRun==true and
      .normalizationReceiptSha256==$normalization and .repairReceiptSha256==$repair and
      .state==$repairReceipt[0].afterState and .state.candidateCount==0 and
      .zeroCandidates==true and .principalParity==true and .projectionReplayValid==true and
      .zeroDeltaReplay==true and (.observedAt|type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")) and
      .receiptSha256==$payload
    ' "$receipt" >/dev/null || die 'qualification clone restart observation is not exact, zero-candidate, and replay-stable'
}

run_repair_clone_qualification() (
  set -Eeuo pipefail
  local run_number=$1 clone_id token run_rel run_dir network pg stage image pg_image original short volume mount
  local meeting usage codex render pg_volume before_obs normalization_input normalization_receipt normalized_obs
  local cold_receipt clone_authority descriptor manifest manifest_sha repair_authority repair_receipt repair_sha
  local restart_before restart_after restart_receipt receipt_sha field
  token="q${run_number}-$(tr -d - < /proc/sys/kernel/random/uuid | cut -c1-12)"
  run_rel="qualification/run-$run_number"; run_dir="$REPAIR_EVIDENCE_DIR/$run_rel"
  network="bonfire-repair-$token"; pg="bonfire-repair-pg-$token"; stage="bonfire-repair-stage-$token"
  install -d -o root -g root -m 700 "$run_dir"
  declare -A clone_volumes=()
  cleanup_clone() {
    docker rm -f "$stage" "$pg" >/dev/null 2>&1 || true
    docker network rm "$network" >/dev/null 2>&1 || true
    for volume in "${clone_volumes[@]:-}"; do docker volume rm "$volume" >/dev/null 2>&1 || true; done
  }
  trap cleanup_clone EXIT
  while IFS= read -r original; do
    short=${original#digitalocean_}; volume="bonfire-repair-$token-$short"
    ! docker volume inspect "$volume" >/dev/null 2>&1 || die 'qualification clone volume name collision'
    docker volume create --label bonfire.bootstrap.role=canonical-repair-clone-qualification \
      --label "bonfire.bootstrap.clone-id=$clone_id" "$volume" >/dev/null
    clone_volumes[$original]=$volume
    mount=$(docker volume inspect -f '{{.Mountpoint}}' "$volume")
    tar --same-owner --xattrs --acls -xpf "$BK/volumes/$original.tar" -C "$mount"
    tar --xattrs --acls --compare -f "$BK/volumes/$original.tar" -C "$mount"
  done < <(canonical_legacy_volume_names)
  render="bonfire-repair-$token-render_queue"
  docker volume create --label bonfire.bootstrap.role=canonical-repair-clone-qualification \
    --label "bonfire.bootstrap.clone-id=$clone_id" "$render" >/dev/null
  clone_volumes[render]=$render
  mount=$(docker volume inspect -f '{{.Mountpoint}}' "$render")
  install -d -o root -g root -m 700 "$mount/jobs"
  meeting=${clone_volumes[digitalocean_meeting_data]}; usage=${clone_volumes[digitalocean_usage_ledger]}
  codex=${clone_volumes[digitalocean_codex_queue]}
  pg_volume="bonfire-repair-$token-postgres-runtime"
  docker volume create --label bonfire.bootstrap.role=canonical-repair-clone-qualification \
    --label "bonfire.bootstrap.clone-id=$clone_id" "$pg_volume" >/dev/null
  clone_volumes[postgres_runtime]=$pg_volume

  docker network create --internal --label bonfire.bootstrap.role=canonical-repair-clone-qualification "$network" >/dev/null
  pg_image=$(jq -er '.sidecars.canonicalPostgres.imageId' "$ADIR/release-receipt.json")
  docker create --name "$pg" --label bonfire.bootstrap.role=canonical-repair-clone-qualification \
    --label "bonfire.bootstrap.clone-id=$clone_id" --network "$network" --network-alias canonical-postgres \
    --restart no --security-opt no-new-privileges:true --cap-drop ALL \
    -e POSTGRES_USER=bonfire -e POSTGRES_DB=bonfire -e POSTGRES_HOST_AUTH_METHOD=trust \
    -v "$pg_volume:/var/lib/postgresql/data" "$pg_image" >/dev/null
  docker inspect "$pg" | jq -e --arg image "$pg_image" --arg network "$network" --arg volume "$pg_volume" '
    length==1 and .[0].Image==$image and .[0].HostConfig.NetworkMode==$network and
    (.[0].NetworkSettings.Networks|keys)==[$network] and (.[0].HostConfig.PortBindings//{}|length)==0 and
    ([.[0].Mounts[]|select(.Type=="volume")|[.Name,.Destination,.RW]])==
      [[$volume,"/var/lib/postgresql/data",true]]
  ' >/dev/null || die 'qualification clone PostgreSQL identity, network, or volume drifted'
  assert_clone_network_membership "$network" "$pg"
  docker start "$pg" >/dev/null
  local ready=false
  for _ in $(seq 1 60); do docker exec "$pg" pg_isready -U bonfire -d bonfire >/dev/null 2>&1 && { ready=true; break; }; sleep 1; done
  test "$ready" = true || die 'qualification clone PostgreSQL did not become ready'
  docker exec -i "$pg" pg_restore -U bonfire -d bonfire --no-owner --no-acl <"$BK/postgres.pgcustom"
  docker exec "$pg" psql -XqAt -F $'\t' -U bonfire -d bonfire \
    -c "select version,encode(sha256,'hex') from schema_migrations order by version" | cmp "$BK/migrations-before.tsv" -
  pg_counts "$pg" | cmp "$BK/table-counts-before.tsv" -

  cold_receipt="$run_dir/cold-clone-receipt.json"
  jq -n --arg a "$A" --arg clone "$clone_id" \
    --arg backup "$(sha256sum "$BK/backup-SHA256SUMS"|awk '{print $1}')" \
    --arg pgdump "$(sha256sum "$BK/postgres.pgcustom"|awk '{print $1}')" \
    --arg migrations "$(sha256sum "$BK/migrations-before.tsv"|awk '{print $1}')" \
    --arg counts "$(sha256sum "$BK/table-counts-before.tsv"|awk '{print $1}')" \
    --arg completed "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" '
      {schema:"bonfire.cold-clone-rehearsal-receipt.v1",status:"complete",releaseCommit:$a,
       cloneId:$clone,qualificationRun:true,
       backupManifestSha256:$backup,restoredVolumeCount:8,
       restoredVolumes:["digitalocean_caddy_config","digitalocean_caddy_data","digitalocean_canonical_postgres",
        "digitalocean_codex_home","digitalocean_codex_queue","digitalocean_codex_runner_data",
        "digitalocean_meeting_data","digitalocean_usage_ledger"],rawVolumeCompare:true,postgresRestore:true,
       postgresDumpSha256:$pgdump,migrationRowsSha256:$migrations,tableCountsSha256:$counts,completedAt:$completed}
    ' >"$cold_receipt.raw"
  write_self_digest_json "$cold_receipt.raw" "$cold_receipt"; rm "$cold_receipt.raw"
  assert_cold_clone_rehearsal_receipt "$cold_receipt" "$clone_id"

  clone_authority="$run_dir/clone-run-authority.json"
  local cold_ref
  cold_ref=$(private_file_reference_json "$cold_receipt" "$REPAIR_EVIDENCE_DIR")
  jq -n --arg a "$A" --arg clone "$clone_id" \
    --arg backup "$(sha256sum "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS"|awk '{print $1}')" \
    --arg source "$(sha256sum "$REPAIR_EVIDENCE_DIR/release-source-receipt.json"|awk '{print $1}')" \
    --arg created "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" --argjson cold "$cold_ref" '
      {schema:"bonfire.canonical-board-repair-clone-run-authority.v1",status:"authorized",releaseCommit:$a,
       cloneId:$clone,qualificationRun:true,backupManifestSha256:$backup,releaseSourceReceiptSha256:$source,
       coldCloneReceipt:$cold,createdAt:$created}
    ' >"$clone_authority.raw"
  write_self_digest_json "$clone_authority.raw" "$clone_authority"; rm "$clone_authority.raw"

  image=$(jq -er '.images.meetingassist.imageId' "$ADIR/release-receipt.json")
  before_obs="$run_dir/before-observation.json"
  create_clone_stage_container "$stage" canonical-clone-observation ro "$image" "$network" "$pg" \
    "$meeting" "$usage" "$codex" "$render" --observe-canonical-repair \
    --repair-observation "/run/bonfire-repair/evidence/$run_rel/before-observation.json"
  run_clone_stage_container "$stage" "$pg" "$network" "$BK/meta/clone-$run_number-before.log" "$BK/private/clone-$run_number-before.exit"
  docker rm "$stage" >/dev/null; assert_clone_network_membership "$network" "$pg"
  assert_canonical_observation "$before_obs"

  normalization_input="$run_dir/normalization-input.json"
  write_clone_normalization_input "$run_dir" "$clone_id" "$before_obs" "$normalization_input"
  normalization_receipt="$run_dir/normalization-receipt.json"
  create_clone_stage_container "$stage" canonical-clone-normalization rw "$image" "$network" "$pg" \
    "$meeting" "$usage" "$codex" "$render" --normalize-canonical \
    --normalization-input "/run/bonfire-repair/evidence/$run_rel/normalization-input.json" \
    --normalization-input-sha256 "$(sha256sum "$normalization_input"|awk '{print $1}')" \
    --normalization-receipt "/run/bonfire-repair/evidence/$run_rel/normalization-receipt.json"
  run_clone_stage_container "$stage" "$pg" "$network" "$BK/meta/clone-$run_number-normalize.log" "$BK/private/clone-$run_number-normalize.exit"
  docker rm "$stage" >/dev/null; assert_clone_network_membership "$network" "$pg"
  assert_canonical_normalization_receipt "$normalization_receipt" "$(sha256sum "$normalization_input"|awk '{print $1}')" "$REPAIR_EVIDENCE_DIR"

  normalized_obs="$run_dir/normalized-observation.json"
  create_clone_stage_container "$stage" canonical-clone-observation ro "$image" "$network" "$pg" \
    "$meeting" "$usage" "$codex" "$render" --observe-canonical-repair \
    --repair-observation "/run/bonfire-repair/evidence/$run_rel/normalized-observation.json"
  run_clone_stage_container "$stage" "$pg" "$network" "$BK/meta/clone-$run_number-normalized.log" "$BK/private/clone-$run_number-normalized.exit"
  docker rm "$stage" >/dev/null; assert_clone_network_membership "$network" "$pg"
  assert_canonical_observation "$normalized_obs" 7
  descriptor="$run_dir/classified-evidence-descriptor.json"
  write_clone_run_descriptor "$run_dir" "$clone_id" "$normalization_receipt" "$clone_authority" "$normalized_obs" "$descriptor"
  manifest="$run_dir/candidate-manifest.json"
  create_clone_stage_container "$stage" canonical-clone-manifest ro "$image" "$network" "$pg" \
    "$meeting" "$usage" "$codex" "$render" --generate-canonical-repair-manifest \
    --evidence-dir /run/bonfire-repair/evidence \
    --classified-evidence-descriptor "/run/bonfire-repair/evidence/$run_rel/classified-evidence-descriptor.json" \
    --normalization-receipt "/run/bonfire-repair/evidence/$run_rel/normalization-receipt.json" \
    --repair-observation "/run/bonfire-repair/evidence/$run_rel/normalized-observation.json" \
    --candidate-manifest "/run/bonfire-repair/evidence/$run_rel/candidate-manifest.json"
  run_clone_stage_container "$stage" "$pg" "$network" "$BK/meta/clone-$run_number-manifest.log" "$BK/private/clone-$run_number-manifest.exit"
  docker rm "$stage" >/dev/null; assert_clone_network_membership "$network" "$pg"
  manifest_sha=$(sha256sum "$manifest"|awk '{print $1}'); require_sha256 "$manifest_sha"
  jq -e --arg a "$A" --arg clone "$clone_id" '
    .schema=="bonfire.canonical-board-repair.v2" and .releaseCommit==$a and
    .environment=="isolated_cold_clone" and .qualificationRun==true and .cloneId==$clone and
    (.candidates|length==7)
  ' "$manifest" >/dev/null || die 'qualification clone manifest lacks exact isolated-run authority and seven candidates'

  repair_authority="$run_dir/repair-authority"
  printf 'CONFIRM CANONICAL BOARD REPAIR %s\n' "$manifest_sha" >"$repair_authority.raw"
  install -o root -g root -m 600 "$repair_authority.raw" "$repair_authority"; rm "$repair_authority.raw"
  repair_receipt="$run_dir/repair-receipt.json"
  create_clone_stage_container "$stage" canonical-clone-repair rw "$image" "$network" "$pg" \
    "$meeting" "$usage" "$codex" "$render" --repair-canonical \
    --candidate-manifest "/run/bonfire-repair/evidence/$run_rel/candidate-manifest.json" \
    --candidate-manifest-sha256 "$manifest_sha" \
    --authority-marker "/run/bonfire-repair/evidence/$run_rel/repair-authority" \
    --repair-receipt "/run/bonfire-repair/evidence/$run_rel/repair-receipt.json"
  run_clone_stage_container "$stage" "$pg" "$network" "$BK/meta/clone-$run_number-repair.log" "$BK/private/clone-$run_number-repair.exit"
  repair_sha=$(sha256sum "$repair_authority"|awk '{print $1}')
  validate_canonical_repair_receipt_payload "$repair_receipt" "$A" "$manifest_sha" "$repair_sha" "$manifest"
  receipt_sha=$(sha256sum "$repair_receipt"|awk '{print $1}')
  docker rm "$stage" >/dev/null; assert_clone_network_membership "$network" "$pg"

  docker stop "$pg" >/dev/null; docker start "$pg" >/dev/null
  ready=false
  for _ in $(seq 1 60); do docker exec "$pg" pg_isready -U bonfire -d bonfire >/dev/null 2>&1 && { ready=true; break; }; sleep 1; done
  test "$ready" = true || die 'qualification clone PostgreSQL failed its required restart'
  assert_clone_network_membership "$network" "$pg"
  restart_before="$run_dir/restart-before-observation.json"
  create_clone_stage_container "$stage" canonical-clone-restart-observation ro "$image" "$network" "$pg" \
    "$meeting" "$usage" "$codex" "$render" --observe-canonical-repair \
    --repair-observation "/run/bonfire-repair/evidence/$run_rel/restart-before-observation.json"
  run_clone_stage_container "$stage" "$pg" "$network" "$BK/meta/clone-$run_number-restart-before.log" "$BK/private/clone-$run_number-restart-before.exit"
  docker rm "$stage" >/dev/null; assert_clone_network_membership "$network" "$pg"
  assert_canonical_observation "$restart_before" 0

  create_clone_stage_container "$stage" canonical-clone-repair-revalidation rw "$image" "$network" "$pg" \
    "$meeting" "$usage" "$codex" "$render" --repair-canonical \
    --candidate-manifest "/run/bonfire-repair/evidence/$run_rel/candidate-manifest.json" \
    --candidate-manifest-sha256 "$manifest_sha" \
    --authority-marker "/run/bonfire-repair/evidence/$run_rel/repair-authority" \
    --repair-receipt "/run/bonfire-repair/evidence/$run_rel/repair-receipt.json"
  run_clone_stage_container "$stage" "$pg" "$network" "$BK/meta/clone-$run_number-revalidate.log" "$BK/private/clone-$run_number-revalidate.exit"
  test "$(sha256sum "$repair_receipt"|awk '{print $1}')" = "$receipt_sha" || die 'qualification clone receipt changed on completed-state replay'
  docker rm "$stage" >/dev/null; assert_clone_network_membership "$network" "$pg"
  restart_after="$run_dir/restart-after-observation.json"
  create_clone_stage_container "$stage" canonical-clone-restart-observation ro "$image" "$network" "$pg" \
    "$meeting" "$usage" "$codex" "$render" --observe-canonical-repair \
    --repair-observation "/run/bonfire-repair/evidence/$run_rel/restart-after-observation.json"
  run_clone_stage_container "$stage" "$pg" "$network" "$BK/meta/clone-$run_number-restart-after.log" "$BK/private/clone-$run_number-restart-after.exit"
  docker rm "$stage" >/dev/null; assert_clone_network_membership "$network" "$pg"
  assert_canonical_observation "$restart_after" 0
  jq -S 'del(.observedAt)' "$restart_before" | cmp - <(jq -S 'del(.observedAt)' "$restart_after") \
    || die 'qualification clone changed across completed-receipt replay after restart'
  jq -e --slurpfile repair "$repair_receipt" '
    .databaseSha256==$repair[0].afterState.databaseSha256 and
    .importInputSha256==$repair[0].afterState.importInputSha256 and
    .board==$repair[0].afterState.board and .journal==$repair[0].afterState.journal and
    .versionMap==$repair[0].afterState.versionMap and
    .versionEntriesSha256==$repair[0].afterState.versionEntriesSha256 and
    .spool==$repair[0].afterState.spool and .proofFingerprintSha256==$repair[0].afterState.proofSha256 and
    .candidateCount==$repair[0].afterState.candidateCount and
    .candidateFingerprintSha256==$repair[0].afterState.candidateSha256 and
    .tenantEventCount==$repair[0].afterState.tenantEventCount and
    .eventHighWater==$repair[0].afterState.eventHighWater and
    .outboxCount==$repair[0].afterState.importOutboxCount and
    .versionEntryCount==$repair[0].afterState.versionEntryCount
  ' "$restart_after" >/dev/null || die 'fresh post-restart exact-A observation differs from the sealed repair after-state'

  restart_receipt="$run_dir/restart-observation.json"
  jq -n --arg a "$A" --arg clone "$clone_id" \
    --arg normalization "$(sha256sum "$normalization_receipt"|awk '{print $1}')" \
    --arg repair "$(sha256sum "$repair_receipt"|awk '{print $1}')" \
    --arg observed "$(jq -er .observedAt "$restart_after")" --slurpfile repairReceipt "$repair_receipt" '
      {schema:"bonfire.canonical-board-repair-restart-observation.v1",status:"complete",releaseCommit:$a,
       cloneId:$clone,environment:"isolated_cold_clone",qualificationRun:true,
       normalizationReceiptSha256:$normalization,repairReceiptSha256:$repair,
       state:$repairReceipt[0].afterState,zeroCandidates:true,principalParity:true,
       projectionReplayValid:true,zeroDeltaReplay:true,observedAt:$observed}
    ' >"$restart_receipt.raw"
  write_self_digest_json "$restart_receipt.raw" "$restart_receipt"; rm "$restart_receipt.raw"
  assert_clone_restart_observation "$restart_receipt" "$clone_id" "$normalization_receipt" "$repair_receipt"
  printf '%s\n' "$clone_id" >"$run_dir/clone-id"; chmod 600 "$run_dir/clone-id"
  docker stop "$pg" >/dev/null
  cleanup_clone
  trap - EXIT
)

assert_clone_qualification_receipt() {
  local receipt=$CLONE_QUALIFICATION_PATH payload_sha backup_sha source_sha run clone_id normalization repair restart manifest authority cold
  local cold_ref qualification_completed qualification_ns cold_ns authority_ns normalization_ns repair_ns restart_ns
  assert_self_digest_json "$receipt"
  payload_sha=$(jq -cS 'del(.receiptSha256)' "$receipt"|tr -d '\n'|sha256sum|awk '{print $1}')
  backup_sha=$(sha256sum "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS"|awk '{print $1}')
  source_sha=$(sha256sum "$REPAIR_EVIDENCE_DIR/release-source-receipt.json"|awk '{print $1}')
  jq -e --arg a "$A" --arg backup "$backup_sha" --arg source "$source_sha" --arg payload "$payload_sha" '
    .schema=="bonfire.canonical-board-repair-clone-qualification.v1" and .status=="complete" and
    .releaseCommit==$a and .backupManifestSha256==$backup and .releaseSourceReceiptSha256==$source and
    (.runs|type=="array" and length==2 and (map(.cloneId)|unique|length)==2) and
    (.runs|map(.cloneId)==(map(.cloneId)|sort)) and
    all(.runs[]; (.cloneId|type=="string" and length>0) and
      all([.normalizationReceipt,.manifest,.cloneRunAuthority,.coldCloneReceipt,.repairReceipt,.restartObservation][];
        (.path|type=="string" and length>0) and (.size|type=="number" and .>=0 and floor==.) and
        (.sha256|test("^[0-9a-f]{64}$")))) and
    ([.runs[]|.normalizationReceipt,.manifest,.cloneRunAuthority,.coldCloneReceipt,.repairReceipt,.restartObservation]|map(.path)|unique|length)==12 and
    ([.runs[]|.normalizationReceipt,.manifest,.cloneRunAuthority,.coldCloneReceipt,.repairReceipt,.restartObservation]|map(.sha256)|unique|length)==12 and
    (.completedAt|type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")) and .receiptSha256==$payload
  ' "$receipt" >/dev/null || die 'clone qualification receipt is not exact A, self-digested, and two-run complete'
  qualification_completed=$(jq -er .completedAt "$receipt")
  qualification_ns=$(exact_utc_epoch_nanoseconds "$qualification_completed" 'clone qualification completedAt')
  while IFS= read -r run; do
    clone_id=$(jq -er .cloneId <<<"$run")
    normalization="$REPAIR_EVIDENCE_DIR/$(jq -er .normalizationReceipt.path <<<"$run")"
    repair="$REPAIR_EVIDENCE_DIR/$(jq -er .repairReceipt.path <<<"$run")"
    restart="$REPAIR_EVIDENCE_DIR/$(jq -er .restartObservation.path <<<"$run")"
    manifest="$REPAIR_EVIDENCE_DIR/$(jq -er .manifest.path <<<"$run")"
    authority="$REPAIR_EVIDENCE_DIR/$(jq -er .cloneRunAuthority.path <<<"$run")"
    cold="$REPAIR_EVIDENCE_DIR/$(jq -er .coldCloneReceipt.path <<<"$run")"
    for field in normalizationReceipt manifest cloneRunAuthority coldCloneReceipt repairReceipt restartObservation; do
      local file="$REPAIR_EVIDENCE_DIR/$(jq -er --arg field "$field" '.[$field].path' <<<"$run")"
      local expected_size expected_sha
      expected_size=$(jq -er --arg field "$field" '.[$field].size' <<<"$run")
      expected_sha=$(jq -er --arg field "$field" '.[$field].sha256' <<<"$run")
      assert_root_private_regular_file "$file" 'clone qualification run evidence'
      test "$(stat -c %s "$file")" -eq "$expected_size" && test "$(sha256sum "$file"|awk '{print $1}')" = "$expected_sha" \
        || die 'clone qualification run evidence seal mismatch'
    done
    assert_canonical_normalization_receipt "$normalization" "$(jq -er .normalizationInputSha256 "$normalization")" "$REPAIR_EVIDENCE_DIR"
    cold_ref=$(private_file_reference_json "$cold" "$REPAIR_EVIDENCE_DIR")
    jq -e --arg a "$A" --arg clone "$clone_id" --arg backup "$backup_sha" --arg source "$source_sha" \
      --argjson cold "$cold_ref" '
        .schema=="bonfire.canonical-board-repair-clone-run-authority.v1" and .status=="authorized" and
        .releaseCommit==$a and .cloneId==$clone and .qualificationRun==true and .backupManifestSha256==$backup and
        .releaseSourceReceiptSha256==$source and .coldCloneReceipt==$cold
      ' "$authority" >/dev/null || die 'clone qualification run authority does not cross-bind its clone, backup, source, and cold clone receipt'
    assert_self_digest_json "$authority"
    assert_cold_clone_rehearsal_receipt "$cold" "$clone_id"
    jq -e --arg a "$A" --arg clone "$clone_id" '
      .schema=="bonfire.canonical-board-repair.v2" and .releaseCommit==$a and .cloneId==$clone and
      .environment=="isolated_cold_clone" and .qualificationRun==true and (.candidates|length==7)
    ' "$manifest" >/dev/null || die 'clone qualification sealed manifest is not the exact isolated run manifest'
    validate_canonical_repair_receipt_payload "$repair" "$A" \
      "$(sha256sum "$manifest"|awk '{print $1}')" "$(sha256sum "$(dirname "$repair")/repair-authority"|awk '{print $1}')" "$manifest"
    assert_clone_restart_observation "$restart" "$clone_id" "$normalization" "$repair"
    cold_ns=$(exact_utc_epoch_nanoseconds "$(jq -er .completedAt "$cold")" 'clone cold receipt completedAt')
    authority_ns=$(exact_utc_epoch_nanoseconds "$(jq -er .createdAt "$authority")" 'clone run authority createdAt')
    normalization_ns=$(exact_utc_epoch_nanoseconds "$(jq -er .completedAt "$normalization")" 'clone normalization completedAt')
    repair_ns=$(exact_utc_epoch_nanoseconds "$(jq -er .completedAt "$repair")" 'clone repair completedAt')
    restart_ns=$(exact_utc_epoch_nanoseconds "$(jq -er .observedAt "$restart")" 'clone restart observedAt')
    test "$cold_ns" -le "$authority_ns" && test "$authority_ns" -le "$normalization_ns" \
      && test "$normalization_ns" -le "$repair_ns" && test "$repair_ns" -le "$restart_ns" \
      && test "$restart_ns" -le "$qualification_ns" \
      || die 'clone qualification evidence timestamps violate exact causal order'
  done < <(jq -c '.runs[]' "$receipt")
}

phase_qualify_repair_clones() {
  require_root; require_commands docker jq sha256sum tar date readlink; load_state; acquire_operator_lock; require_phase canonical-normalized
  assert_forward_maintenance_state
  assert_no_canonical_repair_writers
  assert_backup_checksum_manifest_semantic
  assert_canonical_normalization_receipt "$NORMALIZATION_RECEIPT_PATH" "$(sha256sum "$NORMALIZATION_INPUT_PATH"|awk '{print $1}')"
  assert_canonical_observation "$REPAIR_OBSERVATION_PATH" 7
  assert_classified_target_evidence_input
  if phase_done clone-qualified; then
    assert_clone_qualification_receipt
    test -f "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH" || die 'completed clone qualification lacks pack-generated production descriptor'
    assert_classified_evidence_descriptor "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH"
    return 0
  fi
  test ! -e "$CLONE_QUALIFICATION_PATH" && test ! -e "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH" \
    || die 'partial clone qualification authority exists without its completed marker'
  mark_phase clone-qualification-started
  rm -rf "$CLONE_QUALIFICATION_DIR"
  install -d -o root -g root -m 700 "$CLONE_QUALIFICATION_DIR"
  local clone_one clone_two run1 run2 raw
  clone_one=$(cat /proc/sys/kernel/random/uuid); clone_two=$(cat /proc/sys/kernel/random/uuid)
  test "$clone_one" != "$clone_two" || die 'fresh qualification clone identities collided'
  if ! run_repair_clone_qualification 1 "$clone_one"; then mark_phase clone-qualification-failed; die 'first fresh repair clone qualification failed'; fi
  if ! run_repair_clone_qualification 2 "$clone_two"; then mark_phase clone-qualification-failed; die 'second fresh repair clone qualification failed'; fi
  run1="$CLONE_QUALIFICATION_DIR/run-1"; run2="$CLONE_QUALIFICATION_DIR/run-2"
  raw="$CLONE_QUALIFICATION_PATH.raw"
  jq -n --arg a "$A" --arg backup "$(sha256sum "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS"|awk '{print $1}')" \
    --arg source "$(sha256sum "$REPAIR_EVIDENCE_DIR/release-source-receipt.json"|awk '{print $1}')" \
    --arg completed "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" \
    --arg c1 "$clone_one" --arg c2 "$clone_two" \
    --argjson n1 "$(private_file_reference_json "$run1/normalization-receipt.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson m1 "$(private_file_reference_json "$run1/candidate-manifest.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson a1 "$(private_file_reference_json "$run1/clone-run-authority.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson k1 "$(private_file_reference_json "$run1/cold-clone-receipt.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson r1 "$(private_file_reference_json "$run1/repair-receipt.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson s1 "$(private_file_reference_json "$run1/restart-observation.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson n2 "$(private_file_reference_json "$run2/normalization-receipt.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson m2 "$(private_file_reference_json "$run2/candidate-manifest.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson a2 "$(private_file_reference_json "$run2/clone-run-authority.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson k2 "$(private_file_reference_json "$run2/cold-clone-receipt.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson r2 "$(private_file_reference_json "$run2/repair-receipt.json" "$REPAIR_EVIDENCE_DIR")" \
    --argjson s2 "$(private_file_reference_json "$run2/restart-observation.json" "$REPAIR_EVIDENCE_DIR")" '
      {schema:"bonfire.canonical-board-repair-clone-qualification.v1",status:"complete",releaseCommit:$a,
       backupManifestSha256:$backup,releaseSourceReceiptSha256:$source,
       runs:([{cloneId:$c1,normalizationReceipt:$n1,manifest:$m1,cloneRunAuthority:$a1,coldCloneReceipt:$k1,
              repairReceipt:$r1,restartObservation:$s1},
             {cloneId:$c2,normalizationReceipt:$n2,manifest:$m2,cloneRunAuthority:$a2,coldCloneReceipt:$k2,
              repairReceipt:$r2,restartObservation:$s2}]|sort_by(.cloneId)),
       completedAt:$completed}
    ' >"$raw"
  write_self_digest_json "$raw" "$CLONE_QUALIFICATION_PATH"; rm "$raw"
  assert_clone_qualification_receipt
  write_production_classified_evidence_descriptor
  assert_classified_evidence_descriptor "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH"
  mark_phase clone-qualified
}

assert_classified_evidence_descriptor() {
  local descriptor=$1 reference path size digest resolved evidence_root
  assert_root_private_regular_file "$descriptor" 'classified canonical repair evidence descriptor'
  jq -e --arg a "$A" '
    .schema=="bonfire.canonical-board-repair-evidence.v1" and .releaseCommit==$a and
    .tenantId=="bonfire" and .dataDir=="/app/data" and
    .environment=="production_protected_maintenance" and .qualificationRun==false and
    (.cloneId|type=="string" and length>0) and
    (.targets|type=="array" and length==7 and
      (map(.objectId)==(map(.objectId)|sort)) and (map(.objectId)|unique|length)==7 and
      all(.[]; (.objectId|type=="string" and length>0) and
        (.observedAbsenceAt|type=="string" and test("Z$")) and
        (.selectedStateRole=="source_record" or .selectedStateRole=="archive_record" or
          .selectedStateRole=="positive_observation") and
        (.evidenceBasis=="done_archive_absence" or .evidenceBasis=="last_positive_source_current_absence"))) and
    all([.backupManifest,.normalizationReceipt,.cloneAuthority,.releaseSourceReceipt,.normalizedObservation,
      .targets[].sourceRecord,.targets[].archiveRecord,.targets[].positiveObservation,.targets[].absenceEvidence][];
      (.path|type=="string" and length>0) and (.path|startswith("/")|not) and
      (.size|type=="number" and .>=0 and floor==.) and (.sha256|test("^[0-9a-f]{64}$")))
  ' "$descriptor" >/dev/null || die 'classified evidence descriptor is not the exact A, exact-seven production contract'
  while IFS= read -r reference; do
    path=$(jq -er .path <<<"$reference")
    [[ $path != ../* && $path != */../* && $path != */./* ]] || die 'classified evidence reference escapes its directory'
    path="$REPAIR_EVIDENCE_DIR/$path"
    resolved=$(readlink -f "$path")
    evidence_root=$(readlink -f "$REPAIR_EVIDENCE_DIR")
    test "$resolved" = "$path" && [[ $resolved == "$evidence_root/"* ]] \
      || die 'classified evidence path contains a symlink or escapes its root'
    assert_root_private_regular_file "$path" 'classified evidence payload'
    size=$(jq -er .size <<<"$reference")
    digest=$(jq -er .sha256 <<<"$reference")
    test "$(stat -c %s "$path")" -eq "$size" && test "$(sha256sum "$path" | awk '{print $1}')" = "$digest" \
      || die 'classified evidence file differs from its descriptor seal'
  done < <(jq -c '.backupManifest,.normalizationReceipt,.cloneAuthority,.releaseSourceReceipt,.normalizedObservation,
    .targets[].sourceRecord,.targets[].archiveRecord,.targets[].positiveObservation,.targets[].absenceEvidence' "$descriptor")
  local expected
  expected=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR")
  jq -e --argjson expected "$expected" '.backupManifest==$expected' "$descriptor" >/dev/null \
    || die 'classified evidence descriptor does not bind the actual backup checksum manifest'
  expected=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/normalization-receipt.json" "$REPAIR_EVIDENCE_DIR")
  jq -e --argjson expected "$expected" '.normalizationReceipt==$expected' "$descriptor" >/dev/null \
    || die 'classified evidence descriptor does not bind the accepted normalization receipt'
  expected=$(private_file_reference_json "$CLONE_QUALIFICATION_PATH" "$REPAIR_EVIDENCE_DIR")
  jq -e --argjson expected "$expected" '.cloneAuthority==$expected' "$descriptor" >/dev/null \
    || die 'classified evidence descriptor does not bind the exact two-run clone qualification'
  expected=$(private_file_reference_json "$REPAIR_EVIDENCE_DIR/release-source-receipt.json" "$REPAIR_EVIDENCE_DIR")
  jq -e --argjson expected "$expected" '.releaseSourceReceipt==$expected' "$descriptor" >/dev/null \
    || die 'classified evidence descriptor does not bind exact A source receipt'
  expected=$(private_file_reference_json "$REPAIR_OBSERVATION_PATH" "$REPAIR_EVIDENCE_DIR")
  jq -e --argjson expected "$expected" '.normalizedObservation==$expected' "$descriptor" >/dev/null \
    || die 'classified evidence descriptor does not bind the fresh normalized observation'
  assert_backup_checksum_manifest_semantic
  cmp "$BK/backup-SHA256SUMS" "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" \
    || die 'classified evidence backup manifest is not the matched semantic backup manifest'
  assert_release_source_evidence_receipt "$REPAIR_EVIDENCE_DIR/release-source-receipt.json"
  assert_clone_qualification_receipt
  assert_semantic_target_evidence_records "$descriptor"
}

assert_semantic_target_evidence_records() {
  local descriptor=$1 target object_id absence_at absence_epoch selected_role field role expected_present
  local reference record_relative record_path artifact_relative artifact_path artifact_size artifact_sha
  local observed_at observed_epoch state_sha selected_state='' resolved evidence_root
  local unique_paths
  unique_paths=$(mktemp "$STATE_DIR/classified-evidence-paths.XXXXXX")
  evidence_root=$(readlink -f "$REPAIR_EVIDENCE_DIR")
  while IFS= read -r target; do
    object_id=$(jq -er '.objectId' <<<"$target")
    absence_at=$(jq -er '.observedAbsenceAt' <<<"$target")
    absence_epoch=$(date -u -d "$absence_at" +%s 2>/dev/null) \
      || die 'classified target absence timestamp is not valid UTC time'
    selected_role=$(jq -er '.selectedStateRole' <<<"$target")
    case "$(jq -er .evidenceBasis <<<"$target"):$selected_role" in
      done_archive_absence:archive_record|last_positive_source_current_absence:source_record|last_positive_source_current_absence:positive_observation) ;;
      *) die 'classified target evidence basis and selected state role are semantically inconsistent' ;;
    esac
    for field in sourceRecord archiveRecord positiveObservation absenceEvidence; do
      case "$field" in
        sourceRecord) role=source_record; expected_present=true ;;
        archiveRecord) role=archive_record; expected_present=true ;;
        positiveObservation) role=positive_observation; expected_present=true ;;
        absenceEvidence) role=absence_observation; expected_present=false ;;
      esac
      reference=$(jq -ec --arg field "$field" '.[$field]' <<<"$target")
      record_relative=$(jq -er .path <<<"$reference")
      record_path="$REPAIR_EVIDENCE_DIR/$record_relative"
      jq -e --arg role "$role" --arg object "$object_id" --argjson present "$expected_present" '
        .schema=="bonfire.canonical-board-repair-evidence-record.v1" and
        .role==$role and .objectId==$object and .present==$present and
        (.observedAt|type=="string" and test("Z$")) and
        (.sourceArtifact|type=="object" and (.sourceArtifact.path|type=="string" and length>0) and
          (.sourceArtifact.size|type=="number" and .>=0 and floor==.) and
          (.sourceArtifact.sha256|type=="string" and test("^[0-9a-f]{64}$"))) and
        (if $present then
          (.stateSha256|type=="string" and test("^[0-9a-f]{64}$"))
         else (has("stateSha256")|not) end)
      ' "$record_path" >/dev/null \
        || die 'classified target evidence record violates its fixed role, object, presence, state, or artifact-seal contract'
      artifact_relative=$(jq -er .sourceArtifact.path "$record_path")
      [[ $artifact_relative != /* && $artifact_relative != ../* && $artifact_relative != */../* && $artifact_relative != */./* ]] \
        || die 'classified source artifact path escapes its evidence directory'
      test "$artifact_relative" != "$record_relative" \
        || die 'classified evidence wrapper cannot cite itself as its source artifact'
      artifact_path="$REPAIR_EVIDENCE_DIR/$artifact_relative"
      resolved=$(readlink -f "$artifact_path")
      test "$resolved" = "$artifact_path" && [[ $resolved == "$evidence_root/"* ]] \
        || die 'classified source artifact is a symlink or escapes its evidence directory'
      assert_root_private_regular_file "$artifact_path" 'classified source artifact'
      artifact_size=$(jq -er .sourceArtifact.size "$record_path")
      artifact_sha=$(jq -er .sourceArtifact.sha256 "$record_path")
      test "$(stat -c %s "$artifact_path")" -eq "$artifact_size" &&
        test "$(sha256sum "$artifact_path" | awk '{print $1}')" = "$artifact_sha" \
        || die 'classified source artifact differs from its record seal'
      printf '%s\n%s\n' "$record_relative" "$artifact_relative" >>"$unique_paths"
      observed_at=$(jq -er .observedAt "$record_path")
      observed_epoch=$(date -u -d "$observed_at" +%s 2>/dev/null) \
        || die 'classified target evidence timestamp is not valid UTC time'
      if test "$expected_present" = true; then
        test "$observed_epoch" -le "$absence_epoch" \
          || die 'positive target evidence was observed after the claimed current absence'
        if test "$role" = "$selected_role"; then
          state_sha=$(jq -er .stateSha256 "$record_path")
          require_sha256 "$state_sha"
          selected_state=$state_sha
        fi
      else
        test "$observed_at" = "$absence_at" \
          || die 'absence evidence timestamp does not exactly bind observedAbsenceAt'
      fi
    done
    test -n "$selected_state" || die 'selected target state role did not resolve to one present evidence record'
    selected_state=''
  done < <(jq -c '.targets[]' "$descriptor")
  test "$(wc -l <"$unique_paths")" -eq "$(LC_ALL=C sort -u "$unique_paths" | wc -l)" \
    || die 'classified evidence wrapper or source-artifact paths alias across roles or targets'
  rm -f "$unique_paths"
}

assert_manifest_selected_state_bindings() {
  local target object_id selected_role field record_path selected_sha manifest_sha
  while IFS= read -r target; do
    object_id=$(jq -er .objectId <<<"$target")
    selected_role=$(jq -er .selectedStateRole <<<"$target")
    case "$selected_role" in
      source_record) field=sourceRecord ;;
      archive_record) field=archiveRecord ;;
      positive_observation) field=positiveObservation ;;
      *) die 'manifest target selected-state role is not a present evidence role' ;;
    esac
    record_path="$REPAIR_EVIDENCE_DIR/$(jq -er --arg field "$field" '.[$field].path' <<<"$target")"
    selected_sha=$(jq -er .stateSha256 "$record_path")
    manifest_sha=$(jq -er --arg object "$object_id" '.candidates[]|select(.objectId==$object)|.stateSha256' "$REPAIR_MANIFEST_PATH")
    test "$manifest_sha" = "$selected_sha" \
      || die 'generated manifest target state does not match its explicitly selected evidence record'
  done < <(jq -c '.targets[]' "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH")
}

assert_generated_manifest_evidence() {
  local reference path size digest resolved evidence_root
  assert_canonical_repair_manifest_binding
  jq -e --slurpfile descriptor "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH" --slurpfile observation "$REPAIR_OBSERVATION_PATH" '
    .backupManifest==$descriptor[0].backupManifest and
    .normalizationReceipt==$descriptor[0].normalizationReceipt and
    .qualificationRun==false and .cloneAuthority==$descriptor[0].cloneAuthority and
    .releaseSourceReceipt==$descriptor[0].releaseSourceReceipt and
    .normalizedObservation==$descriptor[0].normalizedObservation and
    .databaseUrlSha256==$observation[0].databaseUrlSha256 and
    .databaseSha256==$observation[0].databaseSha256 and
    .normalizedProofSha256==$observation[0].proofFingerprintSha256 and
    .importInputSha256==$observation[0].importInputSha256 and
    .terminalCandidateSha256==$observation[0].candidateFingerprintSha256 and
    .board==$observation[0].board and .journalPrefix==$observation[0].journal and
    .versionMap==$observation[0].versionMap and .versionEntriesSha256==$observation[0].versionEntriesSha256 and
    .spool==$observation[0].spool and
    ([.candidates[] | {objectId,selectedStateRole,sourceRecord,archiveRecord,positiveObservation,absenceEvidence}] | sort_by(.objectId)) ==
      ([$descriptor[0].targets[] | {objectId,selectedStateRole,sourceRecord,archiveRecord,positiveObservation,absenceEvidence}] | sort_by(.objectId))
  ' "$REPAIR_MANIFEST_PATH" >/dev/null || die 'generated repair manifest does not bind normalized state and every classified evidence target'
  while IFS= read -r reference; do
    path=$(jq -er .path <<<"$reference")
    [[ $path != /* && $path != ../* && $path != */../* && $path != */./* ]] || die 'generated manifest evidence reference escapes its directory'
    path="$REPAIR_EVIDENCE_DIR/$path"
    resolved=$(readlink -f "$path")
    evidence_root=$(readlink -f "$REPAIR_EVIDENCE_DIR")
    test "$resolved" = "$path" && [[ $resolved == "$evidence_root/"* ]] \
      || die 'generated manifest evidence path contains a symlink or escapes its root'
    assert_root_private_regular_file "$path" 'generated manifest evidence payload'
    size=$(jq -er .size <<<"$reference")
    digest=$(jq -er .sha256 <<<"$reference")
    test "$(stat -c %s "$path")" -eq "$size" && test "$(sha256sum "$path" | awk '{print $1}')" = "$digest" \
      || die 'generated manifest evidence file differs from its actual root-only payload'
  done < <(jq -c '.evidenceDescriptor,.backupManifest,.normalizationReceipt,.cloneAuthority,.releaseSourceReceipt,.normalizedObservation,
    .candidates[].sourceRecord,.candidates[].archiveRecord,.candidates[].positiveObservation,.candidates[].absenceEvidence' "$REPAIR_MANIFEST_PATH")
  assert_manifest_selected_state_bindings
}

phase_generate_repair_manifest() {
  require_root; require_commands docker jq sha256sum readlink; load_state; acquire_operator_lock; require_phase clone-qualified
  assert_forward_maintenance_state
  phase_done repair-manifest-generated && { assert_generated_manifest_evidence; return 0; }
  test ! -e "$REPAIR_MANIFEST_PATH" && test ! -e "$REPAIR_MANIFEST_STATE" \
    || die 'unbound or partial candidate manifest state exists; run the exact cold restore'
  assert_classified_evidence_descriptor "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH"
  assert_clone_qualification_receipt
  assert_canonical_normalization_receipt "$NORMALIZATION_RECEIPT_PATH" "$(sha256sum "$NORMALIZATION_INPUT_PATH" | awk '{print $1}')"
  assert_canonical_observation "$REPAIR_OBSERVATION_PATH" 7
  assert_no_canonical_repair_writers
  local pgc image descriptor_ref raw_state manifest_sha
  pgc=$(canonical_repair_postgres_id)
  ensure_canonical_repair_network "$pgc"
  start_canonical_repair_postgres "$pgc"
  image=$(jq -er '.images.meetingassist.imageId' "$ADIR/release-receipt.json")
  mark_phase canonical-manifest-generation-started
  create_canonical_stage_container "$MANIFEST_CONTAINER" canonical-manifest-generation ro "$image" --generate-canonical-repair-manifest \
    --evidence-dir /run/bonfire-repair/evidence \
    --classified-evidence-descriptor /run/bonfire-repair/evidence/classified-evidence-descriptor.json \
    --normalization-receipt /run/bonfire-repair/evidence/normalization-receipt.json \
    --repair-observation /run/bonfire-repair/evidence/normalized-observation.json \
    --candidate-manifest /run/bonfire-repair/evidence/candidate-manifest.json
  if ! run_canonical_stage_container "$MANIFEST_CONTAINER" "$BK/meta/canonical-manifest-generation.log" "$BK/private/canonical-manifest-generation.exit"; then
    mark_phase canonical-manifest-generation-failed; die 'exact A manifest generation failed; run the exact cold restore'
  fi
  docker rm "$MANIFEST_CONTAINER" >/dev/null
  assert_root_private_regular_file "$REPAIR_MANIFEST_PATH" 'generated canonical repair manifest'
  manifest_sha=$(sha256sum "$REPAIR_MANIFEST_PATH" | awk '{print $1}')
  require_sha256 "$manifest_sha"
  descriptor_ref=$(private_file_reference_json "$CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH" "$REPAIR_EVIDENCE_DIR")
  raw_state="$REPAIR_MANIFEST_STATE.raw"
  jq -n --arg a "$A" --arg manifest "$manifest_sha" --arg generated "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg observation "$(sha256sum "$REPAIR_OBSERVATION_PATH" | awk '{print $1}')" \
    --arg normalization "$(sha256sum "$NORMALIZATION_RECEIPT_PATH" | awk '{print $1}')" \
    --arg backup "$(sha256sum "$REPAIR_EVIDENCE_DIR/backup-SHA256SUMS" | awk '{print $1}')" \
    --argjson descriptor "$descriptor_ref" '
      {schema:"bonfire.canonical-repair-ceremony-state.v1",releaseCommit:$a,
       candidateManifestSha256:$manifest,normalizedObservationSha256:$observation,
       normalizationReceiptSha256:$normalization,backupReceiptSha256:$backup,
       classifiedEvidenceDescriptor:$descriptor,generatedAt:$generated}
    ' >"$raw_state"
  write_self_digest_json "$raw_state" "$REPAIR_MANIFEST_STATE" stateSha256
  rm "$raw_state"
  assert_generated_manifest_evidence
  cleanup_canonical_repair_runtime "$pgc" || die 'manifest generation runtime cleanup was incomplete'
  assert_no_canonical_repair_writers
  mark_phase repair-manifest-generated
  printf 'Private exact-seven repair manifest generated and sealed. Next command will require: CONFIRM CANONICAL BOARD REPAIR %s\n' "$manifest_sha"
}

phase_retire_legacy() {
  require_root; load_state; acquire_operator_lock; require_phase repair-manifest-generated
  assert_forward_maintenance_state
  assert_canonical_repair_manifest_binding
  create_canonical_repair_authority_marker
  assert_canonical_repair_authority_marker
  renderer_security_canary "$ADIR"
  assert_forward_maintenance_state
  mapfile -t orphan < <(docker ps -aq --no-trunc --filter label=com.docker.compose.project=digitalocean \
    --filter label=com.docker.compose.service=codex-runner)
  test "${#orphan[@]}" -eq 1 || die 'expected exactly one archived legacy codex-runner container'
  mark_phase legacy-retirement-started
  docker rm "${orphan[0]}" >"$BK/meta/removed-codex-runner.txt"
  local volume
  for volume in digitalocean_codex_home digitalocean_codex_runner_data; do
    test -z "$(docker ps -aq --no-trunc --filter "volume=$volume")" || die "$volume is still referenced"
    docker volume rm "$volume" >>"$BK/meta/removed-legacy-volumes.txt"
  done
  assert_canonical_repair_render_queue_volume
  mark_phase legacy-retired
}

canonical_repair_postgres_id() {
  local ids=()
  mapfile -t ids < <(docker ps -aq --no-trunc --filter label=com.docker.compose.project=digitalocean \
    --filter label=com.docker.compose.service=canonical-postgres)
  test "${#ids[@]}" -eq 1 || die 'expected exactly one retained canonical-postgres container for repair'
  printf '%s\n' "${ids[0]}"
}

assert_no_canonical_repair_writers() {
  local service ids
  for service in meetingassist render-runner render-queue-init codex-runner caddy coturn; do
    ids=$(docker ps -q --no-trunc --filter label=com.docker.compose.project=digitalocean \
      --filter "label=com.docker.compose.service=$service")
    test -z "$ids" || die "project writer or public service is running during canonical repair: $service"
  done
  assert_protected_volume_container_whitelist
}

assert_protected_volume_container_whitelist() {
  local owned=${1:-} volume id allowed_file expected actual
  (cd "$BK" && sha256sum -c backup-SHA256SUMS >/dev/null) \
    || die 'sealed backup authority failed before protected-volume whitelist validation'
  allowed_file=$(mktemp "$STATE_DIR/allowed-containers.XXXXXX")
  jq -r '.[].Id' "$BK/private/containers.inspect.json" >"$allowed_file"
  while IFS= read -r id; do
    docker inspect "$id" >/dev/null 2>&1 || continue
    expected=$(jq -cS --arg id "$id" '.[]|select(.Id==$id)|
      {Id,Image,service:.Config.Labels["com.docker.compose.service"],
       mounts:[.Mounts[]|select(.Type=="volume")|{Name,Destination,RW}]|sort_by(.Name,.Destination)}' \
      "$BK/private/containers.inspect.json")
    actual=$(docker inspect "$id" | jq -cS '.[0]|
      {Id,Image,service:.Config.Labels["com.docker.compose.service"],
       mounts:[.Mounts[]|select(.Type=="volume")|{Name,Destination,RW}]|sort_by(.Name,.Destination)}')
    test "$actual" = "$expected" || { rm -f "$allowed_file"; die "sealed legacy container identity or mounts drifted: $id"; }
  done < <(jq -r '.[].Id' "$BK/private/containers.inspect.json")
  if test -n "$owned"; then
    case "$owned" in
      "$NORMALIZE_CONTAINER"|"$MANIFEST_CONTAINER"|"$REPAIR_CONTAINER") ;;
      *) rm -f "$allowed_file"; die "unknown owned one-shot name: $owned" ;;
    esac
    docker inspect -f '{{.Id}}' "$owned" >>"$allowed_file"
  fi
  sort -u -o "$allowed_file" "$allowed_file"
  for volume in digitalocean_caddy_config digitalocean_caddy_data digitalocean_canonical_postgres \
    digitalocean_codex_home digitalocean_codex_queue digitalocean_codex_runner_data \
    digitalocean_meeting_data digitalocean_usage_ledger digitalocean_render_queue; do
    docker volume inspect "$volume" >/dev/null 2>&1 || continue
    while IFS= read -r id; do
      test -z "$id" || grep -Fx "$id" "$allowed_file" >/dev/null \
        || { rm -f "$allowed_file"; die "unowned container $id mounts protected volume $volume"; }
    done < <(docker ps -aq --no-trunc --filter "volume=$volume")
  done
  rm -f "$allowed_file"
}

assert_canonical_repair_render_queue_volume() {
  docker volume inspect digitalocean_render_queue | jq -e '
    length==1 and .[0].Name=="digitalocean_render_queue" and .[0].Driver=="local" and
    .[0].Labels["com.docker.compose.project"]=="digitalocean" and
    .[0].Labels["com.docker.compose.volume"]=="render_queue"
  ' >/dev/null || die 'canonical repair render queue volume is not the exact Compose-owned volume'
}

ensure_canonical_repair_render_queue_volume() {
  local mount
  if ! docker volume inspect digitalocean_render_queue >/dev/null 2>&1; then
    docker volume create --driver local \
      --label com.docker.compose.project=digitalocean \
      --label com.docker.compose.volume=render_queue \
      digitalocean_render_queue >/dev/null
  fi
  assert_canonical_repair_render_queue_volume
  mount=$(docker volume inspect -f '{{.Mountpoint}}' digitalocean_render_queue)
  test -d "$mount" && test ! -L "$mount" || die 'canonical repair render queue mountpoint is unsafe'
  install -d -o root -g root -m 700 "$mount/jobs"
  test "$(stat -c %U:%G "$mount/jobs")" = root:root && test "$(stat -c %a "$mount/jobs")" = 700 \
    || die 'canonical repair render jobs directory is not exact and private'
}

ensure_canonical_repair_network() {
  local pgc=$1 network_id attached
  if docker network inspect "$REPAIR_NETWORK" >/dev/null 2>&1; then
    docker network inspect "$REPAIR_NETWORK" | jq -e '
      length==1 and .[0].Internal==true and
      .[0].Labels["bonfire.bootstrap.role"]=="canonical-repair"
    ' >/dev/null || die 'preexisting canonical repair network is not the exact internal ceremony network'
  else
    docker network create --internal --label bonfire.bootstrap.role=canonical-repair "$REPAIR_NETWORK" >/dev/null
  fi
  network_id=$(docker network inspect -f '{{.Id}}' "$REPAIR_NETWORK")
  test -n "$network_id" || die 'canonical repair network has no identity'
  test "$(docker inspect -f '{{.State.Running}}' "$pgc")" = false \
    || die 'canonical PostgreSQL must be stopped before ceremony network isolation'
  while IFS= read -r attached; do
    test -z "$attached" || test "$attached" = "$REPAIR_NETWORK" || docker network disconnect "$attached" "$pgc"
  done < <(docker inspect "$pgc" | jq -r '.[0].NetworkSettings.Networks | keys[]')
  if ! docker network inspect "$REPAIR_NETWORK" | jq -e --arg pgc "$pgc" \
    '.[0].Containers | has($pgc)' >/dev/null; then
    docker network connect --alias canonical-postgres "$REPAIR_NETWORK" "$pgc"
  fi
  docker inspect "$pgc" | jq -e --arg network "$REPAIR_NETWORK" '
    (.[0].NetworkSettings.Networks | keys)==[$network] and
    (.[0].HostConfig.PortBindings // {} | length)==0
  ' >/dev/null || die 'retained canonical PostgreSQL is not isolated to the sole internal ceremony network'
  # Docker does not publish a stopped container in network-inspect .Containers.
  # At this boundary container inspect proves the sole configured attachment;
  # the network itself must have no active endpoints. Full endpoint membership
  # is checked immediately after PostgreSQL starts.
  docker network inspect "$REPAIR_NETWORK" | jq -e '
    length==1 and .[0].Internal==true and
    .[0].Labels["bonfire.bootstrap.role"]=="canonical-repair" and
    ((.[0].Containers // {})|length)==0
  ' >/dev/null || die 'stopped canonical PostgreSQL network has an unexpected active endpoint'
}

assert_canonical_repair_network_membership() {
  local pgc=$1 owned=${2:-} pg_id owned_id expected
  pg_id=$(docker inspect -f '{{.Id}}' "$pgc")
  require_sha256 "$pg_id"
  if test -n "$owned"; then
    case "$owned" in
      "$NORMALIZE_CONTAINER"|"$MANIFEST_CONTAINER"|"$REPAIR_CONTAINER") ;;
      *) die "unknown owned ceremony network member: $owned" ;;
    esac
    owned_id=$(docker inspect -f '{{.Id}}' "$owned")
    require_sha256 "$owned_id"
    expected=$(jq -cn --arg pg "$pg_id" --arg owned "$owned_id" '[$pg,$owned]|sort')
  else
    expected=$(jq -cn --arg pg "$pg_id" '[$pg]')
  fi
  docker network inspect "$REPAIR_NETWORK" | jq -e --argjson expected "$expected" '
    length==1 and .[0].Internal==true and
    .[0].Labels["bonfire.bootstrap.role"]=="canonical-repair" and
    ((.[0].Containers // {}) | keys | sort)==$expected
  ' >/dev/null || die 'canonical repair network membership is not exactly retained PostgreSQL plus the owned one-shot'
}

start_canonical_repair_postgres() {
  local pgc=$1 ready=false
  docker start "$pgc" >/dev/null
  for _ in $(seq 1 60); do
    if docker exec "$pgc" pg_isready -U bonfire -d bonfire >/dev/null 2>&1; then ready=true; break; fi
    sleep 1
  done
  test "$ready" = true || die 'canonical PostgreSQL did not become ready for the isolated repair'
  docker inspect "$pgc" | jq -e --arg network "$REPAIR_NETWORK" \
    '(.[0].NetworkSettings.Networks | keys)==[$network]' >/dev/null \
    || die 'running canonical PostgreSQL gained a non-ceremony network'
  assert_canonical_repair_network_membership "$pgc"
  assert_protected_volume_container_whitelist
}

cleanup_canonical_repair_runtime() {
  local pgc=${1:-} owned
  for owned in "$NORMALIZE_CONTAINER" "$MANIFEST_CONTAINER" "$REPAIR_CONTAINER"; do
    if docker inspect "$owned" >/dev/null 2>&1; then
      test "$(docker inspect -f '{{.State.Running}}' "$owned")" = false || return 1
      docker rm "$owned" >/dev/null || return 1
    fi
  done
  if test -n "$pgc" && docker inspect "$pgc" >/dev/null 2>&1; then
    docker stop "$pgc" >/dev/null || return 1
    docker network disconnect "$REPAIR_NETWORK" "$pgc" >/dev/null 2>&1 || true
  fi
  if docker network inspect "$REPAIR_NETWORK" >/dev/null 2>&1; then
    docker network rm "$REPAIR_NETWORK" >/dev/null || return 1
  fi
}

assert_canonical_repair_container_exact() {
  local expected_image=$1 runtime_dir=$2 actual_image
  actual_image=$(docker inspect -f '{{.Image}}' "$REPAIR_CONTAINER")
  test "$actual_image" = "$expected_image" || die 'canonical repair container does not use exact A image ID'
  docker inspect "$REPAIR_CONTAINER" | jq -e \
    --arg network "$REPAIR_NETWORK" --arg runtime "$runtime_dir" \
    --arg manifest "$REPAIR_MANIFEST_SHA" '
      length==1 and
      .[0].Config.Labels["bonfire.bootstrap.role"]=="canonical-repair" and
      .[0].HostConfig.NetworkMode==$network and
      (.[0].NetworkSettings.Networks | keys)==[$network] and
      .[0].HostConfig.ReadonlyRootfs==true and
      .[0].HostConfig.RestartPolicy.Name=="no" and
      ((.[0].HostConfig.CapDrop // []) | index("ALL") != null) and
      ((.[0].HostConfig.SecurityOpt // []) | index("no-new-privileges:true") != null) and
      (.[0].HostConfig.PortBindings // {} | length)==0 and
      all(.[0].Mounts[]; .Type!="volume" or .RW==true) and
      ((.[0].Config.Env // []) | index("BONFIRE_CODEX_QUEUE_PATH=/app/codex-queue/jobs") != null) and
      ((.[0].Config.Env // []) | index("BONFIRE_CODEX_HEARTBEAT_PATH=/app/codex-queue/heartbeat.json") != null) and
      ((.[0].Config.Env // []) | index("BONFIRE_RENDER_QUEUE_PATH=/app/render-queue/jobs") != null) and
      ((.[0].Config.Env // []) | index("BONFIRE_RENDER_HEARTBEAT_PATH=/app/render-queue/heartbeat.json") != null) and
      ([.[0].Mounts[] | select(.Type=="volume") | [.Name,.Destination]] | sort) ==
        ([
          ["digitalocean_codex_queue","/app/codex-queue"],
          ["digitalocean_meeting_data","/app/data"],
          ["digitalocean_render_queue","/app/render-queue"],
          ["digitalocean_usage_ledger","/app/data/usage"]
        ] | sort) and
      any(.[0].Mounts[]; .Type=="bind" and .Source==$runtime and .Destination=="/run/bonfire-repair" and .RW==true) and
      (.[0].Config.Cmd | index("--repair-canonical") != null) and
      (.[0].Config.Cmd | index("--candidate-manifest-sha256") != null) and
      (.[0].Config.Cmd | index($manifest) != null) and
      (.[0].Config.Cmd | index("--repair-receipt") != null)
    ' >/dev/null || die 'canonical repair container confinement, mounts, or exact command drifted'
}

create_canonical_repair_container() {
  local runtime_dir=$1 image=$2
  docker create --name "$REPAIR_CONTAINER" \
    --label bonfire.bootstrap.role=canonical-repair \
    --network "$REPAIR_NETWORK" --restart no --read-only --cap-drop ALL \
    --security-opt no-new-privileges:true --pids-limit 256 --memory 1024m \
    --tmpfs /tmp:rw,nosuid,nodev,noexec,size=256m \
    --env-file "$BASE_ENV" --env-file "$ADIR/release.env" \
    --env BONFIRE_CODEX_QUEUE_PATH=/app/codex-queue/jobs \
    --env BONFIRE_CODEX_HEARTBEAT_PATH=/app/codex-queue/heartbeat.json \
    --env BONFIRE_RENDER_QUEUE_PATH=/app/render-queue/jobs \
    --env BONFIRE_RENDER_HEARTBEAT_PATH=/app/render-queue/heartbeat.json \
    --volume digitalocean_meeting_data:/app/data \
    --volume digitalocean_usage_ledger:/app/data/usage \
    --volume digitalocean_codex_queue:/app/codex-queue \
    --volume digitalocean_render_queue:/app/render-queue \
    --volume "$runtime_dir:/run/bonfire-repair" \
    --entrypoint /app/meetingassist "$image" \
    --repair-canonical \
    --candidate-manifest /run/bonfire-repair/evidence/candidate-manifest.json \
    --candidate-manifest-sha256 "$REPAIR_MANIFEST_SHA" \
    --authority-marker /run/bonfire-repair/operator-authority \
    --repair-receipt /run/bonfire-repair/repair-receipt.json >/dev/null
  assert_canonical_repair_container_exact "$image" "$runtime_dir"
  assert_canonical_repair_network_membership "$(canonical_repair_postgres_id)" "$REPAIR_CONTAINER"
  assert_protected_volume_container_whitelist "$REPAIR_CONTAINER"
}

revalidate_completed_canonical_repair() (
  set -Eeuo pipefail
  local runtime_dir=$REPAIR_CEREMONY_DIR receipt="$REPAIR_CEREMONY_DIR/repair-receipt.json"
  local pgc image receipt_sha current_before current_after
  pgc=$(canonical_repair_postgres_id)
  cleanup_completed_revalidation() {
    docker rm -f "$REPAIR_CONTAINER" >/dev/null 2>&1 || true
    docker stop "$pgc" >/dev/null 2>&1 || true
    docker network disconnect "$REPAIR_NETWORK" "$pgc" >/dev/null 2>&1 || true
    docker network rm "$REPAIR_NETWORK" >/dev/null 2>&1 || true
  }
  trap cleanup_completed_revalidation EXIT

  assert_canonical_repair_receipt "$receipt"
  assert_canonical_repair_authority_marker false
  assert_canonical_repair_fingerprint_file "$runtime_dir/repair-after-fingerprint.json"
  assert_no_canonical_repair_writers
  ensure_canonical_repair_render_queue_volume
  ensure_canonical_repair_network "$pgc"
  start_canonical_repair_postgres "$pgc"
  assert_canonical_repair_network_membership "$pgc"

  current_before="$runtime_dir/completed-revalidation-before-fingerprint.json"
  current_after="$runtime_dir/completed-revalidation-after-fingerprint.json"
  capture_stable_canonical_repair_fingerprint "$pgc" "$current_before"
  cmp "$runtime_dir/repair-after-fingerprint.json" "$current_before" \
    || die 'completed canonical state drifted before exact-A receipt revalidation'

  receipt_sha=$(sha256sum "$receipt" | awk '{print $1}')
  require_sha256 "$receipt_sha"
  image=$(jq -er '.images.meetingassist.imageId' "$ADIR/release-receipt.json")
  require_sha256 "${image#sha256:}"
  create_canonical_repair_container "$runtime_dir" "$image"
  if ! run_canonical_stage_container "$REPAIR_CONTAINER" \
    "$BK/meta/canonical-repair-completed-revalidation.log" \
    "$BK/private/canonical-repair-completed-revalidation.exit"; then
    die 'exact A rejected the completed canonical repair state during resume revalidation'
  fi
  assert_canonical_repair_receipt "$receipt"
  test "$(sha256sum "$receipt" | awk '{print $1}')" = "$receipt_sha" \
    || die 'completed repair receipt changed during read-only exact-A revalidation'
  capture_stable_canonical_repair_fingerprint "$pgc" "$current_after"
  cmp "$runtime_dir/repair-after-fingerprint.json" "$current_after" \
    || die 'completed canonical state changed during exact-A receipt revalidation'
  rm -f "$current_before" "$current_before.sha256" "$current_after" "$current_after.sha256"

  docker rm "$REPAIR_CONTAINER" >/dev/null
  docker stop "$pgc" >/dev/null
  docker network disconnect "$REPAIR_NETWORK" "$pgc" >/dev/null
  docker network rm "$REPAIR_NETWORK" >/dev/null
  trap - EXIT
  assert_no_canonical_repair_writers
)

phase_repair_canonical() {
  require_root; require_commands docker jq sha256sum tar sed; load_state; acquire_operator_lock; require_phase legacy-retired
  assert_forward_maintenance_state
  test ! -e "$RELEASE_PARENT/active-release.json" || die 'release ledger appeared before canonical repair'
  test ! -e "$RELEASE_PARENT/.bonfire-release-operation.lock" || die 'release operation lock appeared before canonical repair'
  assert_node_matches_release "$ADIR"
  assert_canonical_repair_manifest_binding
  assert_generated_manifest_evidence

  local runtime_dir="$REPAIR_CEREMONY_DIR" receipt pgc image log before_fingerprint after_fingerprint
  receipt="$runtime_dir/repair-receipt.json"

  if phase_done canonical-repaired; then
    ! docker inspect "$REPAIR_CONTAINER" >/dev/null 2>&1 || die 'completed repair retained its one-shot container'
    ! docker network inspect "$REPAIR_NETWORK" >/dev/null 2>&1 || die 'completed repair retained its internal network'
    assert_canonical_repair_render_queue_volume
    pgc=$(canonical_repair_postgres_id)
    test "$(docker inspect -f '{{.State.Running}}' "$pgc")" = false || die 'completed repair retained canonical PostgreSQL as a background writer'
    assert_canonical_repair_receipt "$receipt"
    assert_canonical_repair_fingerprint_file "$runtime_dir/repair-before-fingerprint.json"
    assert_canonical_repair_fingerprint_file "$runtime_dir/repair-after-fingerprint.json"
    assert_no_canonical_repair_writers
    revalidate_completed_canonical_repair \
      || die 'completed canonical repair could not be re-observed and revalidated by exact A'
    return 0
  fi
  create_canonical_repair_authority_marker
  assert_canonical_repair_authority_marker
  phase_done canonical-repair-execution-started \
    && die 'canonical repair was interrupted or rejected; append retry is forbidden, run the exact cold restore'
  assert_no_canonical_repair_writers
  test "$(stat -c %Y "$(marker_path rehearsed)")" -ge "$(stat -c %Y "$(marker_path backup)")" \
    || die 'repair requires a rehearsal completed after the matched backup'
  test ! -e "$receipt" || die 'canonical repair receipt exists before the first owned execution'
  test ! -e "$(marker_path canonical-repair-receipted)" || die 'repair receipt marker exists before execution'

  pgc=$(canonical_repair_postgres_id)
  ensure_canonical_repair_render_queue_volume
  ensure_canonical_repair_network "$pgc"
  start_canonical_repair_postgres "$pgc"
  image=$(jq -er '.images.meetingassist.imageId' "$ADIR/release-receipt.json")
  require_sha256 "${image#sha256:}"

  before_fingerprint="$runtime_dir/repair-before-fingerprint.json"
  after_fingerprint="$runtime_dir/repair-after-fingerprint.json"
  capture_stable_canonical_repair_fingerprint "$pgc" "$before_fingerprint"
  assert_forward_maintenance_state
  assert_no_canonical_repair_writers
  assert_canonical_repair_manifest_binding
  assert_canonical_repair_authority_marker
  mark_phase canonical-repair-execution-started
  create_canonical_repair_container "$runtime_dir" "$image"
  log="$BK/meta/canonical-repair-container.log"
  if ! run_canonical_stage_container "$REPAIR_CONTAINER" "$log" "$BK/private/canonical-repair.exit"; then
    mark_phase canonical-repair-failed
    die 'exact A canonical repair refused, failed, or was interrupted; run the exact cold restore before any further forward phase'
  fi
  if ! (assert_canonical_repair_receipt "$receipt"); then
    mark_phase canonical-repair-failed
    die 'canonical repair exited zero without an authoritative exact receipt; stdout is rejected and exact cold restore is required'
  fi
  mark_phase canonical-repair-receipted
  capture_stable_canonical_repair_fingerprint "$pgc" "$after_fingerprint"
  jq -e --arg a "$A" --arg manifest_sha "$REPAIR_MANIFEST_SHA" --slurpfile manifest "$runtime_dir/evidence/candidate-manifest.json" '
    ($manifest[0].schema=="bonfire.canonical-board-repair.v2") and
    ($manifest[0].releaseCommit==$a) and ($manifest[0].tenantId=="bonfire") and
    ($manifest[0].environment=="production_protected_maintenance") and
    .candidateManifestSha256==$manifest_sha and
    .candidateFingerprintSha256==$manifest[0].candidateSetSha256 and
    .candidateCount==7 and .appliedCount==7 and
    ($manifest[0].candidates | type=="array" and length==7)
  ' "$receipt" >/dev/null || die 'canonical repair receipt differs from the production-authority exact-seven manifest or candidate set'
  cleanup_canonical_repair_runtime "$pgc" || die 'canonical repair runtime cleanup was incomplete'
  assert_no_canonical_repair_writers
  assert_forward_maintenance_state
  mark_phase canonical-repaired
}

manual_a_compose_up() {
  local compose="$ADIR/sealed-candidate/deploy/digitalocean/docker-compose.yml"
  local docker_variable
  for docker_variable in DOCKER_HOST DOCKER_CONTEXT DOCKER_TLS_VERIFY DOCKER_CERT_PATH DOCKER_CONFIG; do
    test -z "${!docker_variable:-}" || return 1
  done
  (
    cd "$(dirname "$compose")"
    env -i PATH="$PATH" HOME=/root BONFIRE_BASE_ENV_FILE="$BASE_ENV" \
      docker compose --project-name digitalocean --project-directory "$(dirname "$compose")" \
      --env-file "$BASE_ENV" --env-file "$ADIR/release.env" --file "$compose" --profile render \
      up -d --no-build --wait --wait-timeout 300
  )
}

a_ledgerless_identity_exact() {
  local log="$BK/a-ledgerless.log" rc
  set +e; release_verify "$ADIR" >"$log" 2>&1; rc=$?; set -e
  test "$rc" -eq 1 || return 1
  mapfile -t lines < <(sed '/^[[:space:]]*$/d' "$log")
  test "${#lines[@]}" -eq 1 && test "${lines[0]}" = 'bonfire-release: active release ledger is missing'
}

phase_bootstrap_a() {
  require_root; load_state; acquire_operator_lock; require_phase canonical-repaired
  test ! -e "$RELEASE_PARENT/active-release.json" || die 'ledger appeared before A bootstrap'
  test ! -e "$RELEASE_PARENT/.bonfire-release-operation.lock" || die 'release operation lock appeared before A bootstrap'
  assert_node_matches_release "$ADIR"
  assert_forward_maintenance_state
  local accepted=false
  if manual_a_compose_up && a_ledgerless_identity_exact &&
     release_data_gate "$ADIR" a && target_topology_gate && wait_for_canonical_parity a; then
    accepted=true
  fi
  if test "$accepted" != true; then
    printf '%s\n' 'A failed identity, data, topology, or canonical parity. Public ingress remains blocked. Run vps-rollback-legacy.sh.' \
      | tee "$BK/A-FAILURE.txt" >&2
    mark_phase a-failed
    return 1
  fi
  mark_phase a-accepted
}

assert_generation_one_ledger() {
  local ledger="$RELEASE_PARENT/active-release.json"
  test -f "$ledger" && test ! -L "$ledger"
  test "$(stat -c %a "$ledger")" = 600
  test "$(stat -c %U:%G "$ledger")" = root:root
  jq -e --arg ad "$ADIR" --arg bd "$BDIR" \
    --slurpfile a "$ADIR/release-receipt.json" --slurpfile b "$BDIR/release-receipt.json" '
      .schema=="bonfire.active-release-ledger.v1" and .generation==1 and
      .active.releaseDir==$bd and .active.releaseCommit==$b[0].source.releaseCommit and
      .active.bundleSha256==$b[0].bundleSha256 and
      .active.meetingassistImageId==$b[0].images.meetingassist.imageId and
      .active.renderRunnerImageId==$b[0].images.renderRunner.imageId and
      .previous.releaseDir==$ad and .previous.releaseCommit==$a[0].source.releaseCommit and
      .previous.bundleSha256==$a[0].bundleSha256 and
      .previous.meetingassistImageId==$a[0].images.meetingassist.imageId and
      .previous.renderRunnerImageId==$a[0].images.renderRunner.imageId
    ' "$ledger" >/dev/null
}

authenticated_read_smoke() {
  authenticate_operator
  trap logout_operator EXIT
  local path safe
  for path in auth/me rooms assistant/chat-threads assistant/board assistant/files; do
    safe=${path//\//-}
    local_https -H "Origin: https://$HOST" -H "Authorization: Bearer $OPS_SESSION" \
      "https://$HOST/$path" >"$BK/private/b-smoke-$safe.json"
    validate_authenticated_smoke_payload "$path" <"$BK/private/b-smoke-$safe.json"
  done
  logout_operator
  trap - EXIT
}

validate_authenticated_smoke_payload() {
  local path=$1
  case "$path" in
    auth/me)
      jq -e '
        type=="object" and
        (.email | type=="string" and test("^[^@[:space:]]+@[^@[:space:]]+$")) and
        (.name | type=="string" and length>0)
      ' >/dev/null
      ;;
    rooms)
      jq -e '
        type=="object" and .ok==true and
        (.rooms | type=="array" and length>=1 and all(.[]; (.id | type=="string" and length>0)))
      ' >/dev/null
      ;;
    assistant/chat-threads)
      jq -e '
        type=="object" and .ok==true and
        (.threads | type=="array" and length>=1 and
          any(.[]; .table==true and (.id | type=="string" and length>0)))
      ' >/dev/null
      ;;
    assistant/board)
      jq -e '
        type=="object" and .ok==true and
        (.board | type=="object" and (.cards | type=="array"))
      ' >/dev/null
      ;;
    assistant/files)
      jq -e '
        type=="object" and .ok==true and
        (.files | type=="array") and (.folders | type=="array")
      ' >/dev/null
      ;;
    *)
      return 1
      ;;
  esac
}

phase_activate_b() {
  require_root; load_state; acquire_operator_lock; require_phase a-accepted
  assert_node_matches_release "$ADIR"; assert_node_matches_release "$BDIR"
  assert_forward_maintenance_state
  local log="$BK/activate-b.log" rc
  if test -e "$RELEASE_PARENT/active-release.json"; then
    test ! -e "$RELEASE_PARENT/.bonfire-release-operation.lock" || {
      mark_phase b-ambiguous
      die 'generation-1 ledger and release operation lock coexist; preserve both and inspect manually'
    }
    assert_generation_one_ledger || die 'existing release ledger is not the exact generation-1 B/A ledger'
  else
    set +e
    node "$(release_tool "$ADIR")" activate \
      --release-dir "$BDIR" --rollback-release-dir "$ADIR" --base-env "$BASE_ENV" \
      --health-url "https://$HOST/healthz" --ready-url "https://$HOST/readyz" >"$log" 2>&1
    rc=$?
    set -e
    if test "$rc" -ne 0; then
      if test -e "$RELEASE_PARENT/.bonfire-release-operation.lock"; then
        mark_phase b-ambiguous
        die 'B activation recovery is ambiguous; preserve the release operation lock and inspect manually'
      fi
      if test -e "$RELEASE_PARENT/active-release.json"; then
        assert_generation_one_ledger || die 'failed activation left an unexpected release ledger'
      else
        a_ledgerless_identity_exact || die 'failed B activation did not restore exact ledgerless A'
        mark_phase b-recovered-failure
        die 'B activation failed but retained A was restored; remain in maintenance and diagnose or cold-rollback'
      fi
    else
      jq -e --arg b "$B" '.activated==true and .ledgerGeneration==1 and .verified==true and .releaseCommit==$b' "$log" >/dev/null
      assert_generation_one_ledger || die 'successful activation did not create the exact generation-1 B/A ledger'
    fi
  fi
  mark_phase b-activation-committed
  release_verify "$BDIR" >"$BK/verify-b.json"
  jq -e --arg b "$B" '.verified==true and .releaseCommit==$b' "$BK/verify-b.json" >/dev/null
  assert_generation_one_ledger
  release_data_gate "$BDIR" b
  target_topology_gate
  wait_for_canonical_parity b
  authenticated_read_smoke
  mark_phase b-accepted
}

remove_hosts_marker_exactly() {
  local candidate
  candidate=$(mktemp /etc/hosts.bonfire-bootstrap.XXXXXX)
  awk -v marker="$HOSTS_MARKER" 'index($0, marker)==0 { print }' /etc/hosts >"$candidate"
  cmp "$candidate" "$BK/meta/hosts.before" || { rm -f "$candidate"; die '/etc/hosts changed outside the ceremony; leave ingress blocked and inspect'; }
  chown --reference=/etc/hosts "$candidate"
  chmod --reference=/etc/hosts "$candidate"
  mv "$candidate" /etc/hosts
}

restore_hosts_marker_for_maintenance() {
  test -z "$(grep -F "$HOSTS_MARKER" /etc/hosts || true)" || return 0
  printf '127.0.0.1 %s %s\n' "$HOST" "$HOSTS_MARKER" >>/etc/hosts
}

reblock_maintenance_ingress() {
  local wan=$1
  rearm_ephemeral_ingress_guard "$wan" || return 1
  rearm_persistent_ingress_guard || return 1
  restore_hosts_marker_for_maintenance
  assert_ephemeral_ingress_guard "$wan"
  assert_persistent_ingress_guard
}

phase_reopen() {
  require_root; load_state; acquire_operator_lock; require_phase b-accepted
  local confirmation wan
  read -r -p 'Type REOPEN EXACT B PUBLIC TRAFFIC: ' confirmation
  test "$confirmation" = 'REOPEN EXACT B PUBLIC TRAFFIC' || die 'reopen not confirmed'
  wan=$(jq -er '.wanInterface' "$STATE_FILE")
  mark_phase public-open-attempted
  reblock_maintenance_ingress "$wan" || die 'could not establish a durable maintenance block before reopen'
  release_verify "$BDIR" >"$BK/verify-b-before-reopen.json"
  jq -e --arg b "$B" '.verified==true and .releaseCommit==$b' "$BK/verify-b-before-reopen.json" >/dev/null
  assert_generation_one_ledger
  release_data_gate "$BDIR" b-reopen
  target_topology_gate
  wait_for_canonical_parity b-reopen
  remove_hosts_marker_exactly
  getent ahostsv4 "$HOST" | awk -v expected="$VPS_IP" 'NR==1{seen=1; exit($1!=expected)} END{if(!seen)exit 1}'
  if ! remove_persistent_ingress_guard_rules || ! remove_ephemeral_ingress_guard_rules "$wan"; then
    reblock_maintenance_ingress "$wan" || true
    die 'could not remove both maintenance guards; ingress was reblocked where possible'
  fi
  if ! curl -fsS --noproxy '*' --connect-timeout 5 --max-time 30 "https://$HOST/healthz" >"$BK/public-health-after-reopen.json" ||
     ! curl -fsS --noproxy '*' --connect-timeout 5 --max-time 30 "https://$HOST/readyz" >"$BK/public-ready-after-reopen.json"; then
    reblock_maintenance_ingress "$wan" || true
    die 'public probe failed; maintenance ingress block was restored'
  fi
  if ! jq -e --arg b "$B" '.ok==true and .version==$b and .release.releaseCommit==$b' "$BK/public-health-after-reopen.json" >/dev/null ||
     ! jq -e --arg b "$B" '.ok==true and .version==$b and .release.releaseCommit==$b' "$BK/public-ready-after-reopen.json" >/dev/null; then
    reblock_maintenance_ingress "$wan" || true
    die 'public release identity was wrong; maintenance ingress block was restored'
  fi
  if ! retire_ephemeral_ingress_guard_chains "$wan" || ! retire_persistent_ingress_guard; then
    reblock_maintenance_ingress "$wan" || true
    die 'maintenance guard cleanup failed; ingress was reblocked where possible'
  fi
  mark_phase reopened
  printf 'Run mac-public-probe.sh open %s from the Mac, perform real desktop/mobile smoke, then acknowledge-public.\n' "$B"
}

phase_acknowledge_public() {
  require_root; load_state; acquire_operator_lock; require_phase reopened
  local confirmation
  read -r -p 'Type PUBLIC EXACT B CONFIRMED FROM MAC: ' confirmation
  test "$confirmation" = 'PUBLIC EXACT B CONFIRMED FROM MAC' || die 'public B was not confirmed'
  mark_phase public-confirmed
}

phase_status() {
  require_root; load_state
  local wan persistent=inactive ephemeral=inactive hosts=absent renderer_profiles=absent
  wan=$(jq -r '.wanInterface // empty' "$STATE_FILE")
  "$SCRIPT_DIR/bonfire-bootstrap-ingress-guard.sh" status >/dev/null 2>&1 && persistent=active
  if test -n "$wan" && assert_ephemeral_ingress_guard "$wan" >/dev/null 2>&1; then ephemeral=active; fi
  test -n "$(grep -F "$HOSTS_MARKER" /etc/hosts || true)" && hosts=present
  if (assert_renderer_security_profiles) >/dev/null 2>&1; then renderer_profiles=exact-enforcing; \
  elif test -e "$RENDERER_APPARMOR_PATH" || test -e "$RENDERER_SECCOMP_PATH"; then renderer_profiles=DRIFTED; fi
  printf 'A=%s\nB=%s\nbackup=%s\npersistent-ingress-guard=%s\nephemeral-ingress-guard=%s\nhosts-marker=%s\nrenderer-security-profiles=%s\n' \
    "$A" "$B" "$BK" "$persistent" "$ephemeral" "$hosts" "$renderer_profiles"
  find "$STATE_DIR" -maxdepth 1 -type f -name 'phase-*' -exec basename {} \; | sort
  if test -e "$RELEASE_PARENT/.bonfire-release-operation.lock"; then printf 'release-operation-lock=PRESENT\n'; else printf 'release-operation-lock=absent\n'; fi
  if test -e "$RELEASE_PARENT/active-release.json"; then printf 'active-ledger=PRESENT\n'; else printf 'active-ledger=absent\n'; fi
}

if [[ ${BASH_SOURCE[0]} != "$0" ]]; then
  return 0
fi

run_forward_phase() {
  assert_forward_ceremony_permitted
  "$@"
}

case ${1:-} in
  start-next-ceremony) phase_start_next_ceremony ;;
  init-build) run_forward_phase phase_init_build ;;
  preflight) run_forward_phase phase_preflight ;;
  isolate) run_forward_phase phase_isolate ;;
  acknowledge-external-block) run_forward_phase phase_acknowledge_external_block ;;
  prove-empty) run_forward_phase phase_prove_empty ;;
  backup) run_forward_phase phase_backup ;;
  rehearse) run_forward_phase phase_rehearse ;;
  normalize-canonical) run_forward_phase phase_normalize_canonical ;;
  qualify-repair-clones) run_forward_phase phase_qualify_repair_clones ;;
  generate-repair-manifest) run_forward_phase phase_generate_repair_manifest ;;
  retire-legacy) run_forward_phase phase_retire_legacy ;;
  repair-canonical) run_forward_phase phase_repair_canonical ;;
  bootstrap-a) run_forward_phase phase_bootstrap_a ;;
  activate-b) run_forward_phase phase_activate_b ;;
  reopen) run_forward_phase phase_reopen ;;
  acknowledge-public) phase_acknowledge_public ;;
  status) phase_status ;;
  *) usage; exit 2 ;;
esac
