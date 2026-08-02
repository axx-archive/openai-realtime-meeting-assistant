#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=vps-common.sh
source "$SCRIPT_DIR/vps-common.sh"

restore_volume_definition() {
  local volume=$1 driver key value output
  docker volume inspect "$volume" >/dev/null 2>&1 && return 0
  driver=$(jq -er --arg volume "$volume" '.[]|select(.Name==$volume)|.Driver' "$BK/private/volumes.inspect.json")
  local command=(docker volume create --driver "$driver")
  while IFS=$'\t' read -r key value; do command+=(--label "$key=$value"); done \
    < <(jq -r --arg volume "$volume" '.[]|select(.Name==$volume)|(.Labels//{})|to_entries[]|[.key,.value]|@tsv' "$BK/private/volumes.inspect.json")
  while IFS=$'\t' read -r key value; do command+=(--opt "$key=$value"); done \
    < <(jq -r --arg volume "$volume" '.[]|select(.Name==$volume)|(.Options//{})|to_entries[]|[.key,.value]|@tsv' "$BK/private/volumes.inspect.json")
  command+=("$volume")
  output=$("${command[@]}")
  test "$output" = "$volume" || die "could not recreate $volume exactly"
}

restore_legacy() {
  require_root; load_state; acquire_operator_lock; require_phase rehearsed
  phase_done public-open-attempted && die 'public traffic was opened or attempted; cold restore is now a possible data-loss/reconciliation incident and this script refuses it'
  test ! -e "$RELEASE_PARENT/.bonfire-release-operation.lock" || die 'release recovery is ambiguous; preserve the operation lock and do not cold-restore'
  local confirmation
  read -r -p 'Type RESTORE COLD LEGACY SNAPSHOT: ' confirmation
  test "$confirmation" = 'RESTORE COLD LEGACY SNAPSHOT' || die 'legacy restore not confirmed'
  (cd "$BK" && sha256sum -c backup-SHA256SUMS >/dev/null)

  cmp "$BASE_ENV" "$BK/private/base.env"
  cmp /opt/meetingassist/deploy/digitalocean/docker-compose.yml "$BK/private/legacy-docker-compose.yml"
  cmp /opt/meetingassist/deploy/digitalocean/Caddyfile "$BK/private/legacy-Caddyfile"
  tar --xattrs --acls --compare -f "$BK/private/opt-meetingassist.tar" -C /opt
  tar --xattrs --acls --compare -f "$BK/private/opt-meetingassist-workspace.tar" -C /opt

  if test -e "$RELEASE_PARENT/active-release.json"; then
    test -f "$RELEASE_PARENT/active-release.json" && test ! -L "$RELEASE_PARENT/active-release.json"
    install -m 600 "$RELEASE_PARENT/active-release.json" "$BK/private/ledger-before-legacy-restore.json"
    rm "$RELEASE_PARENT/active-release.json"
  fi
  docker ps -aq --filter label=com.docker.compose.project=digitalocean | xargs -r docker rm -f
  if docker volume inspect digitalocean_render_queue >/dev/null 2>&1; then
    test -z "$(docker ps -aq --filter volume=digitalocean_render_queue)"
    docker volume rm digitalocean_render_queue
  fi

  local volumes=(
    digitalocean_caddy_config digitalocean_caddy_data digitalocean_canonical_postgres
    digitalocean_codex_home digitalocean_codex_queue digitalocean_codex_runner_data
    digitalocean_meeting_data digitalocean_usage_ledger
  )
  local volume mount
  for volume in "${volumes[@]}"; do
    restore_volume_definition "$volume"
    mount=$(docker volume inspect -f '{{.Mountpoint}}' "$volume")
    test -d "$mount"
    find "$mount" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
    tar --same-owner --xattrs --acls -xpf "$BK/volumes/$volume.tar" -C "$mount"
    tar --xattrs --acls --compare -f "$BK/volumes/$volume.tar" -C "$mount"
  done

  docker image load -i "$BK/images/legacy-images.tar" >"$BK/meta/legacy-image-load.log"
  local ref image_id
  while IFS=$'\t' read -r ref image_id; do
    test "$(docker image inspect "$ref" --format '{{.Id}}')" = "$image_id" || die "legacy image ref changed: $ref"
  done <"$BK/meta/legacy-image-map.tsv"

  local compose=/opt/meetingassist/deploy/digitalocean/docker-compose.yml
  local compose_dir
  compose_dir=$(dirname "$compose")
  (
    cd "$compose_dir"
    BONFIRE_BASE_ENV_FILE="$BASE_ENV" docker compose \
      --project-name digitalocean --project-directory "$compose_dir" \
      --env-file "$BASE_ENV" --file "$compose" --profile codex --profile render \
      up -d --no-build --wait --wait-timeout 120 canonical-postgres
  )
  local pgc
  pgc=$(project_service_id canonical-postgres)
  test -n "$pgc"
  docker exec "$pgc" psql -XqAt -F $'\t' -U bonfire -d bonfire \
    -c "select version,encode(sha256,'hex') from schema_migrations order by version" | cmp "$BK/migrations-before.tsv" -
  pg_counts "$pgc" | cmp "$BK/table-counts-before.tsv" -
  (
    cd "$compose_dir"
    BONFIRE_BASE_ENV_FILE="$BASE_ENV" docker compose \
      --project-name digitalocean --project-directory "$compose_dir" \
      --env-file "$BASE_ENV" --file "$compose" --profile codex --profile render \
      up -d --no-build --wait --wait-timeout 120
  )

  diff -u \
    <(printf '%s\n' caddy canonical-postgres codex-runner coturn meetingassist render-runner | sort) \
    <(docker ps -a --filter label=com.docker.compose.project=digitalocean \
      --format '{{.Label "com.docker.compose.service"}}' | sort -u)
  diff -u \
    <(printf '%s\n' "${volumes[@]}" | sort) \
    <(docker volume ls --format '{{.Name}}' | grep '^digitalocean_' | sort)
  while IFS=$'\t' read -r ref image_id; do
    test -n "$(docker ps -q --filter label=com.docker.compose.project=digitalocean --filter ancestor="$image_id")" || die "restored legacy image is not running: $ref"
  done <"$BK/meta/legacy-image-map.tsv"
  local_https "https://$HOST/healthz" >"$BK/meta/legacy-restored-health.json"
  local_https "https://$HOST/readyz" >"$BK/meta/legacy-restored-ready.json"
  jq -e '.ok==true' "$BK/meta/legacy-restored-health.json" >/dev/null
  jq -e '.ok==true' "$BK/meta/legacy-restored-ready.json" >/dev/null
  mark_phase legacy-restored
  printf 'Exact cold legacy state is restored. Public ingress remains blocked. Review captured degraded baseline, then run reopen-legacy if authorized.\n'
}

restart_untouched_legacy() {
  require_root; load_state; acquire_operator_lock
  phase_done legacy-retirement-started && die 'legacy retirement began; use the rehearsed cold restore instead'
  test -f "$BK/private/containers.inspect.json" || die 'original container inventory was not captured'
  test ! -e "$RELEASE_PARENT/.bonfire-release-operation.lock" || die 'release operation lock exists'
  local pgc
  pgc=$(jq -er '.[]|select(.Config.Labels["com.docker.compose.service"]=="canonical-postgres")|.Id' "$BK/private/containers.inspect.json")
  docker start "$pgc" >/dev/null
  local ready=false
  for _ in $(seq 1 60); do
    if docker exec "$pgc" pg_isready -U bonfire -d bonfire >/dev/null 2>&1; then ready=true; break; fi
    sleep 1
  done
  test "$ready" = true
  while IFS= read -r container; do
    test -z "$container" || docker start "$container" >/dev/null
  done < <(jq -r '.[]|select(.Config.Labels["com.docker.compose.service"]!="canonical-postgres")|.Id' "$BK/private/containers.inspect.json")
  while IFS=$'\t' read -r ref image_id; do
    test "$(docker image inspect "$ref" --format '{{.Id}}')" = "$image_id"
    test -n "$(docker ps -q --filter label=com.docker.compose.project=digitalocean --filter ancestor="$image_id")"
  done <"$BK/meta/legacy-image-map.tsv"
  local app_ready=false
  for _ in $(seq 1 24); do
    if local_https "https://$HOST/healthz" >"$BK/meta/legacy-restarted-health.json" 2>/dev/null &&
       local_https "https://$HOST/readyz" >"$BK/meta/legacy-restarted-ready.json" 2>/dev/null; then
      app_ready=true; break
    fi
    sleep 5
  done
  test "$app_ready" = true
  jq -e '.ok==true' "$BK/meta/legacy-restarted-health.json" >/dev/null
  jq -e '.ok==true' "$BK/meta/legacy-restarted-ready.json" >/dev/null
  mark_phase legacy-restored
  printf 'Untouched original containers are running again; ingress remains blocked.\n'
}

remove_hosts_marker_exactly() {
  local candidate
  candidate=$(mktemp /etc/hosts.bonfire-bootstrap.XXXXXX)
  awk -v marker="$HOSTS_MARKER" 'index($0, marker)==0 { print }' /etc/hosts >"$candidate"
  cmp "$candidate" "$BK/meta/hosts.before" || { rm -f "$candidate"; die '/etc/hosts changed outside the ceremony'; }
  chown --reference=/etc/hosts "$candidate"; chmod --reference=/etc/hosts "$candidate"; mv "$candidate" /etc/hosts
}

restore_hosts_marker() {
  test -n "$(grep -F "$HOSTS_MARKER" /etc/hosts || true)" || printf '127.0.0.1 %s %s\n' "$HOST" "$HOSTS_MARKER" >>/etc/hosts
}

reblock_legacy_maintenance_ingress() {
  local wan=$1
  rearm_ephemeral_ingress_guard "$wan" || return 1
  rearm_persistent_ingress_guard || return 1
  restore_hosts_marker
  assert_ephemeral_ingress_guard "$wan"
  assert_persistent_ingress_guard
}

reopen_legacy() {
  require_root; load_state; acquire_operator_lock; require_phase legacy-restored
  local confirmation wan
  read -r -p 'Type REOPEN RESTORED LEGACY TRAFFIC: ' confirmation
  test "$confirmation" = 'REOPEN RESTORED LEGACY TRAFFIC' || die 'legacy reopen not confirmed'
  wan=$(jq -er '.wanInterface' "$STATE_FILE")
  mark_phase public-open-attempted
  reblock_legacy_maintenance_ingress "$wan" || die 'could not establish a durable maintenance block before legacy reopen'
  remove_hosts_marker_exactly
  if ! remove_persistent_ingress_guard_rules || ! remove_ephemeral_ingress_guard_rules "$wan"; then
    reblock_legacy_maintenance_ingress "$wan" || true
    die 'could not remove both maintenance guards; ingress was reblocked where possible'
  fi
  if ! curl -fsS --noproxy '*' --connect-timeout 5 --max-time 30 "https://$HOST/healthz" >"$BK/meta/legacy-public-health.json" ||
     ! curl -fsS --noproxy '*' --connect-timeout 5 --max-time 30 "https://$HOST/readyz" >"$BK/meta/legacy-public-ready.json" ||
     ! jq -e '.ok==true' "$BK/meta/legacy-public-health.json" >/dev/null ||
     ! jq -e '.ok==true' "$BK/meta/legacy-public-ready.json" >/dev/null; then
    reblock_legacy_maintenance_ingress "$wan" || true
    die 'restored legacy public validation failed; ingress was reblocked'
  fi
  if ! retire_ephemeral_ingress_guard_chains "$wan" || ! retire_persistent_ingress_guard; then
    reblock_legacy_maintenance_ingress "$wan" || true
    die 'maintenance guard cleanup failed; ingress was reblocked where possible'
  fi
  mark_phase legacy-reopened
  printf 'Run mac-public-probe.sh legacy from the Mac and complete desktop/mobile acceptance.\n'
}

case ${1:-} in
  restore) restore_legacy ;;
  restart-untouched) restart_untouched_legacy ;;
  reopen-legacy) reopen_legacy ;;
  *) printf 'Usage: vps-rollback-legacy.sh restart-untouched|restore|reopen-legacy\n' >&2; exit 2 ;;
esac
