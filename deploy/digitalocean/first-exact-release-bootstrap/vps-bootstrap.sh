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
  init-build                 install/check Node; build retained A and B
  preflight                  require exact missing-render-volume verify failures
  isolate                    block public ingress; install exact renderer profiles
  acknowledge-external-block record operator's independent Mac failure proof
  prove-empty                prove every room is empty under member authentication
  backup                     quiesce writers; make complete private cold backup
  rehearse                   restore-test every volume and PostgreSQL dump
  retire-legacy              remove only codex-runner and two archived legacy volumes
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

  mapfile -t containers < <(docker ps -aq --filter label=com.docker.compose.project=digitalocean)
  test "${#containers[@]}" -eq 6 || die 'legacy project container inventory changed'
  docker inspect "${containers[@]}" >"$BK/private/containers.inspect.json"
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
  tar --xattrs --acls --numeric-owner --one-file-system --exclude='meetingassist/data' \
    -C /opt -cpf "$BK/private/opt-meetingassist.tar" meetingassist
  tar --xattrs --acls --numeric-owner --one-file-system \
    -C /opt -cpf "$BK/private/opt-meetingassist-workspace.tar" meetingassist-workspace

  local service id pgc volume container mount
  for service in meetingassist render-runner render-queue-init codex-runner coturn caddy; do
    while IFS= read -r id; do test -z "$id" || docker stop "$id" >>"$BK/meta/stopped-containers.txt"; done \
      < <(docker ps -q --filter label=com.docker.compose.project=digitalocean --filter "label=com.docker.compose.service=$service")
  done
  pgc=$(project_service_id canonical-postgres)
  test -n "$pgc" || die 'canonical PostgreSQL is not the sole remaining service'
  for volume in "${volumes[@]}"; do
    while IFS= read -r container; do
      test -z "$container" || test "$container" = "$pgc" || die "$container still writes $volume"
    done < <(docker ps -q --filter "volume=$volume")
  done
  docker exec "$pgc" pg_dump -U bonfire -d bonfire -Fc --no-owner --no-acl >"$BK/postgres.pgcustom"
  docker exec -i "$pgc" pg_restore -l <"$BK/postgres.pgcustom" >"$BK/postgres.list"
  docker exec "$pgc" psql -XqAt -F $'\t' -v ON_ERROR_STOP=1 -U bonfire -d bonfire \
    -c "select version,encode(sha256,'hex') from schema_migrations order by version" >"$BK/migrations-before.tsv"
  pg_counts "$pgc" >"$BK/table-counts-before.tsv"
  docker stop "$pgc" >>"$BK/meta/stopped-containers.txt"
  test -z "$(docker ps -q --filter label=com.docker.compose.project=digitalocean)" || die 'a project writer still runs after quiescence'

  for volume in "${volumes[@]}"; do
    mount=$(docker volume inspect -f '{{.Mountpoint}}' "$volume")
    test -d "$mount" || die "missing volume mountpoint for $volume"
    tar --xattrs --acls --numeric-owner --one-file-system -C "$mount" -cpf "$BK/volumes/$volume.tar" .
    tar -tf "$BK/volumes/$volume.tar" >/dev/null
  done
  write_backup_checksum_manifest "$BK"
  sync
  mark_phase backup
}

phase_rehearse() {
  require_root; load_state; acquire_operator_lock; require_phase backup
  (cd "$BK" && sha256sum -c backup-SHA256SUMS >/dev/null)
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
  mark_phase rehearsed
}

phase_retire_legacy() {
  require_root; load_state; acquire_operator_lock; require_phase rehearsed
  assert_forward_maintenance_state
  renderer_security_canary "$ADIR"
  assert_forward_maintenance_state
  mapfile -t orphan < <(docker ps -aq --filter label=com.docker.compose.project=digitalocean \
    --filter label=com.docker.compose.service=codex-runner)
  test "${#orphan[@]}" -eq 1 || die 'expected exactly one archived legacy codex-runner container'
  mark_phase legacy-retirement-started
  docker rm "${orphan[0]}" >"$BK/meta/removed-codex-runner.txt"
  local volume
  for volume in digitalocean_codex_home digitalocean_codex_runner_data; do
    test -z "$(docker ps -aq --filter "volume=$volume")" || die "$volume is still referenced"
    docker volume rm "$volume" >>"$BK/meta/removed-legacy-volumes.txt"
  done
  ! docker volume inspect digitalocean_render_queue >/dev/null 2>&1 || die 'render_queue exists before A bootstrap'
  mark_phase legacy-retired
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
  require_root; load_state; acquire_operator_lock; require_phase legacy-retired
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
  init-build) run_forward_phase phase_init_build ;;
  preflight) run_forward_phase phase_preflight ;;
  isolate) run_forward_phase phase_isolate ;;
  acknowledge-external-block) run_forward_phase phase_acknowledge_external_block ;;
  prove-empty) run_forward_phase phase_prove_empty ;;
  backup) run_forward_phase phase_backup ;;
  rehearse) run_forward_phase phase_rehearse ;;
  retire-legacy) run_forward_phase phase_retire_legacy ;;
  bootstrap-a) run_forward_phase phase_bootstrap_a ;;
  activate-b) run_forward_phase phase_activate_b ;;
  reopen) run_forward_phase phase_reopen ;;
  acknowledge-public) phase_acknowledge_public ;;
  status) phase_status ;;
  *) usage; exit 2 ;;
esac
