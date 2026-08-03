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
RENDERER_APPARMOR_NAME=bonfire-render-runner-v1
RENDERER_APPARMOR_PATH=/etc/apparmor.d/bonfire-render-runner-v1
RENDERER_SECCOMP_PATH=/etc/docker/seccomp/bonfire-render-runner-v1.json
REPAIR_CEREMONY_DIR="$STATE_DIR/canonical-repair"
REPAIR_EVIDENCE_DIR="$REPAIR_CEREMONY_DIR/evidence"
REPAIR_MANIFEST_PATH="$REPAIR_EVIDENCE_DIR/candidate-manifest.json"
REPAIR_MANIFEST_STATE="$REPAIR_CEREMONY_DIR/manifest-state.json"
REPAIR_AUTHORITY_PATH="$REPAIR_CEREMONY_DIR/operator-authority"
NORMALIZATION_INPUT_PATH="$REPAIR_EVIDENCE_DIR/normalization-input.json"
NORMALIZATION_RECEIPT_PATH="$REPAIR_EVIDENCE_DIR/normalization-receipt.json"
REPAIR_OBSERVATION_PATH="$REPAIR_EVIDENCE_DIR/normalized-observation.json"
CLASSIFIED_EVIDENCE_DESCRIPTOR_PATH="$REPAIR_EVIDENCE_DIR/classified-evidence-descriptor.json"
CLASSIFIED_TARGET_EVIDENCE_PATH="$REPAIR_EVIDENCE_DIR/classified-target-evidence.json"
CLONE_QUALIFICATION_PATH="$REPAIR_EVIDENCE_DIR/clone-qualification.json"
CLONE_QUALIFICATION_DIR="$REPAIR_EVIDENCE_DIR/qualification"
REPAIR_RUNTIME_DIR_NAME=canonical-repair-runtime
NORMALIZE_CONTAINER=bonfire-canonical-normalize
MANIFEST_CONTAINER=bonfire-canonical-manifest
REPAIR_CONTAINER=bonfire-canonical-repair
REPAIR_NETWORK=bonfire-canonical-repair-internal

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

assert_root_private_regular_file() {
  local path=$1 label=${2:-private file}
  test -f "$path" && test ! -L "$path" || die "$label must be a regular non-symlink file"
  test "$(stat -c %U:%G "$path")" = root:root || die "$label must be root-owned"
  test "$(stat -c %a "$path")" = 600 || die "$label must have exact mode 0600"
}

assert_legacy_compose_recovery_bundle() {
  local compose="$BK/private/legacy-compose-resolved.yml"
  local provenance="$BK/private/legacy-compose-provenance.json"
  local config_json compose_sha env_sha project_dir
  assert_root_private_regular_file "$compose" 'resolved legacy Compose recovery file'
  assert_root_private_regular_file "$provenance" 'legacy Compose provenance receipt'
  assert_root_private_regular_file "$BK/private/base.env" 'legacy base environment backup'
  assert_self_digest_json "$provenance"
  compose_sha=$(sha256sum "$compose" | awk '{print $1}')
  env_sha=$(sha256sum "$BK/private/base.env" | awk '{print $1}')
  project_dir=$(jq -er '.projectDirectory' "$provenance")
  test "$project_dir" = /opt/meetingassist/deploy/digitalocean \
    || die 'legacy Compose recovery project directory drifted'
  jq -e --arg compose "$compose_sha" --arg env "$env_sha" '
    .schema=="bonfire.legacy-compose-provenance.v1" and .status=="complete" and
    .projectName=="digitalocean" and
    .projectDirectory=="/opt/meetingassist/deploy/digitalocean" and
    (.environmentFile|type=="string" and startswith("/")) and
    .environmentFileSha256==$env and
    .resolvedComposeSha256==$compose and .baseEnvironmentSha256==$env and
    (.sourceConfigFiles|type=="array" and length>=1 and
      all(.[]; (.path|type=="string" and startswith("/")) and
        (.sha256|type=="string" and test("^[0-9a-f]{64}$"))))
  ' "$provenance" >/dev/null || die 'legacy Compose provenance receipt is invalid'
  config_json=$(mktemp "$STATE_DIR/legacy-compose-config.XXXXXX")
  BONFIRE_BASE_ENV_FILE="$BK/private/base.env" docker compose \
    --project-name digitalocean --project-directory "$project_dir" \
    --env-file "$BK/private/base.env" --file "$compose" \
    --profile codex --profile render config --format json >"$config_json"
  jq -e '
    (.services|keys|sort)==
      (["caddy","canonical-postgres","codex-runner","coturn","meetingassist","render-runner"]|sort) and
    ((.services|has("render-queue-init"))|not) and
    ((.volumes|keys|sort)==
      (["caddy_config","caddy_data","canonical_postgres","codex_queue",
        "codex_runner_data","meeting_data","usage_ledger"]|sort))
  ' "$config_json" >/dev/null || { rm -f "$config_json"; die 'resolved legacy Compose recovery topology is not exact'; }
  rm -f "$config_json"
}

private_file_reference_json() {
  local file=$1 evidence_dir=$2 relative size digest
  assert_root_private_regular_file "$file" 'sealed private evidence file'
  [[ $file == "$evidence_dir/"* ]] || die "private evidence file escapes evidence directory: $file"
  relative=${file#"$evidence_dir/"}
  test -n "$relative" && [[ $relative != /* && $relative != ../* && $relative != */../* ]] \
    || die "unsafe private evidence path: $relative"
  size=$(stat -c %s "$file")
  digest=$(sha256sum "$file" | awk '{print $1}')
  require_sha256 "$digest"
  jq -cn --arg path "$relative" --argjson size "$size" --arg sha256 "$digest" \
    '{path:$path,size:$size,sha256:$sha256}'
}

write_self_digest_json() {
  local source=$1 destination=$2 field=${3:-receiptSha256} digest
  test -f "$source" && test ! -L "$source" || die 'self-digest JSON source is unsafe'
  digest=$(jq -cS --arg field "$field" 'del(.[$field])' "$source" | tr -d '\n' | sha256sum | awk '{print $1}')
  require_sha256 "$digest"
  jq --arg field "$field" --arg digest "$digest" '.[$field]=$digest' "$source" >"$destination.tmp"
  chown root:root "$destination.tmp"
  chmod 600 "$destination.tmp"
  mv "$destination.tmp" "$destination"
}

assert_self_digest_json() {
  local file=$1 field=${2:-receiptSha256} expected actual
  assert_root_private_regular_file "$file" 'self-digest JSON evidence'
  expected=$(jq -er --arg field "$field" '.[$field]' "$file")
  require_sha256 "$expected"
  actual=$(jq -cS --arg field "$field" 'del(.[$field])' "$file" | tr -d '\n' | sha256sum | awk '{print $1}')
  test "$actual" = "$expected" || die "self-digest mismatch: $file"
}

assert_canonical_repair_manifest_binding() {
  local actual state_manifest
  assert_root_private_regular_file "$REPAIR_MANIFEST_STATE" 'canonical repair manifest ceremony state'
  assert_self_digest_json "$REPAIR_MANIFEST_STATE" stateSha256
  jq -e --arg a "$A" '
    .schema=="bonfire.canonical-repair-ceremony-state.v1" and
    .releaseCommit==$a and
    (.candidateManifestSha256 | type=="string" and test("^[0-9a-f]{64}$")) and
    (.generatedAt | type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")) and
    (.stateSha256 | type=="string" and test("^[0-9a-f]{64}$"))
  ' "$REPAIR_MANIFEST_STATE" >/dev/null || die 'canonical repair ceremony manifest state is invalid'
  state_manifest=$(jq -er '.candidateManifestSha256' "$REPAIR_MANIFEST_STATE")
  require_sha256 "$state_manifest"
  REPAIR_MANIFEST_SHA=$state_manifest
  assert_root_private_regular_file "$REPAIR_MANIFEST_PATH" 'canonical repair candidate manifest'
  jq -e 'type=="object"' "$REPAIR_MANIFEST_PATH" >/dev/null \
    || die 'canonical repair candidate manifest must contain one JSON object'
  actual=$(sha256sum "$REPAIR_MANIFEST_PATH" | awk '{print $1}')
  require_sha256 "$actual"
  test "$actual" = "$REPAIR_MANIFEST_SHA" \
    || die 'canonical repair candidate manifest differs from the root-only ceremony binding'
  jq -e --arg a "$A" '
    .schema=="bonfire.canonical-board-repair.v2" and .releaseCommit==$a and
    .tenantId=="bonfire" and .dataDir=="/app/data" and
    .environment=="production_protected_maintenance" and
    .evidenceDir=="/run/bonfire-repair/evidence" and (.cloneId|type=="string" and length>0) and
    .qualificationRun==false and
    all([.evidenceDescriptor,.backupManifest,.normalizationReceipt,.cloneAuthority,.releaseSourceReceipt,.normalizedObservation][];
      (.path|type=="string" and length>0) and (.size|type=="number" and .>=0 and floor==.) and
      (.sha256|type=="string" and test("^[0-9a-f]{64}$"))) and
    all([.databaseUrlSha256,.databaseSha256,.versionEntriesSha256,.normalizedProofSha256,.importInputSha256][];
      type=="string" and test("^[0-9a-f]{64}$")) and
    all([.board,.journalPrefix,.versionMap,.spool][];
      (.size|type=="number" and .>=0 and floor==.) and (.sha256|type=="string" and test("^[0-9a-f]{64}$"))) and
    (.candidateSetSha256 | type=="string" and test("^[0-9a-f]{64}$")) and
    (.terminalCandidateSha256 | type=="string" and test("^[0-9a-f]{64}$")) and
    (.candidates | type=="array" and length==7 and all(.[];
      (.objectId|type=="string" and length>0) and (.stateSha256|test("^[0-9a-f]{64}$")) and
      (.targetVersion|type=="number" and .>=1 and floor==.) and
      (.priorPrincipals|type=="array" and length>0)))
  ' "$REPAIR_MANIFEST_PATH" >/dev/null \
    || die 'canonical repair manifest is not the exact A production-maintenance exact-seven contract'
}

canonical_repair_authority_text() {
  printf 'CONFIRM CANONICAL BOARD REPAIR %s\n' "$REPAIR_MANIFEST_SHA"
}

assert_canonical_repair_authority_marker() {
  local require_fresh=${1:-true} expected actual now mtime age
  assert_root_private_regular_file "$REPAIR_AUTHORITY_PATH" 'canonical repair operator authority marker'
  expected=$(mktemp "$STATE_DIR/authority-expected.XXXXXX")
  canonical_repair_authority_text >"$expected"
  chmod 600 "$expected"
  cmp "$expected" "$REPAIR_AUTHORITY_PATH" \
    || { rm -f "$expected"; die 'canonical repair authority marker is not the exact manifest-bound confirmation'; }
  rm -f "$expected"
  actual=$(sha256sum "$REPAIR_AUTHORITY_PATH" | awk '{print $1}')
  require_sha256 "$actual"
  if test "$require_fresh" = true; then
    now=$(date +%s)
    mtime=$(stat -c %Y "$REPAIR_AUTHORITY_PATH")
    age=$((now - mtime))
    (( age >= 0 && age <= 300 )) || die 'canonical repair authority marker is older than five minutes; reconfirm the exact manifest'
  fi
  REPAIR_AUTHORITY_SHA=$actual
}

create_canonical_repair_authority_marker() {
  local confirmation expected
  if test -e "$REPAIR_AUTHORITY_PATH"; then
    if assert_canonical_repair_authority_marker >/dev/null 2>&1; then
      mark_phase canonical-repair-authorized
      return
    fi
    rm -f "$REPAIR_AUTHORITY_PATH"
  fi
  install -d -o root -g root -m 700 "$REPAIR_CEREMONY_DIR"
  expected="CONFIRM CANONICAL BOARD REPAIR $REPAIR_MANIFEST_SHA"
  read -r -p "Type $expected: " confirmation
  test "$confirmation" = "$expected" || die 'canonical repair authority was not explicitly confirmed for the exact manifest'
  canonical_repair_authority_text >"$REPAIR_AUTHORITY_PATH.tmp"
  chown root:root "$REPAIR_AUTHORITY_PATH.tmp"
  chmod 600 "$REPAIR_AUTHORITY_PATH.tmp"
  mv "$REPAIR_AUTHORITY_PATH.tmp" "$REPAIR_AUTHORITY_PATH"
  assert_canonical_repair_authority_marker
  mark_phase canonical-repair-authorized
}

canonical_repair_receipt_payload_sha256() {
  local receipt=$1
  jq -cS 'del(.receiptSha256)' "$receipt" | tr -d '\n' | sha256sum | awk '{print $1}'
}

validate_canonical_repair_receipt_payload() {
  local receipt=$1 expected_release=$2 manifest_sha=$3 authority_sha=$4 manifest_path=${5:-$REPAIR_MANIFEST_PATH} actual_payload_sha
  actual_payload_sha=$(canonical_repair_receipt_payload_sha256 "$receipt")
  require_sha256 "$actual_payload_sha"
  test -f "$manifest_path" && test ! -L "$manifest_path" \
    || die 'canonical repair receipt-bound manifest must be a regular non-symlink file'
  test "$(sha256sum "$manifest_path" | awk '{print $1}')" = "$manifest_sha" \
    || die 'canonical repair receipt validator manifest seal mismatch'
  jq -e \
    --arg release "$expected_release" --arg manifest_sha "$manifest_sha" \
    --arg authority "$authority_sha" --arg payload_sha "$actual_payload_sha" \
    --arg empty_candidates_sha "$(printf '[]' | sha256sum | awk '{print $1}')" \
    --slurpfile manifest "$manifest_path" '
      . as $receipt |
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
      .schema=="bonfire.canonical-repair-receipt.v1" and
      .status=="complete" and
      .releaseCommit==$release and .version==$release and
      .tenantId=="bonfire" and
      .cloneId==$manifest[0].cloneId and .environment==$manifest[0].environment and
      .qualificationRun==$manifest[0].qualificationRun and
      .candidateManifestSha256==$manifest_sha and
      .authorityMarkerSha256==$authority and
      (.before | type=="object" and
        (.eventHighWater | type=="number" and .>=0 and floor==.) and
        (.captureSpoolHighWater | type=="number" and .>=0 and floor==.)) and
      (.after | type=="object" and
        (.eventHighWater | type=="number" and .>=0 and floor==.) and
        (.captureSpoolHighWater | type=="number" and .>=0 and floor==.)) and
      .after.eventHighWater>=.before.eventHighWater and
      .after.captureSpoolHighWater==.before.captureSpoolHighWater and
      (.beforeState|stateSeal) and (.afterState|stateSeal) and
      .delta=={tenantEvents:7,importOutbox:7,versionEntries:7} and
      (.afterState.tenantEventCount-.beforeState.tenantEventCount)==7 and
      (.afterState.eventHighWater-.beforeState.eventHighWater)==7 and
      (.afterState.importOutboxCount-.beforeState.importOutboxCount)==7 and
      (.afterState.versionEntryCount-.beforeState.versionEntryCount)==7 and
      .beforeState.board==.afterState.board and .beforeState.spool==.afterState.spool and
      .beforeState.captureSpoolHighWater==.afterState.captureSpoolHighWater and
      .before.eventHighWater==.beforeState.eventHighWater and .after.eventHighWater==.afterState.eventHighWater and
      .before.captureSpoolHighWater==.beforeState.captureSpoolHighWater and
      .after.captureSpoolHighWater==.afterState.captureSpoolHighWater and
      (.candidateCount | type=="number" and .==7 and floor==.) and
      (.candidateFingerprintSha256 | type=="string" and test("^[0-9a-f]{64}$")) and
      .candidateFingerprintSha256==$manifest[0].candidateSetSha256 and
      .beforeCandidateSha256==.beforeState.candidateSha256 and
      .beforeCandidateSha256==$manifest[0].terminalCandidateSha256 and
      (.appliedCount | type=="number" and .==$receipt.candidateCount and floor==.) and
      .firstAppendObserved==true and
      .zeroCandidates==true and .principalParity==true and .projectionParity==true and .idempotentSecondReplay==true and
      .beforeState.candidateCount==7 and .afterState.candidateCount==0 and
      .beforeCandidateSha256==.beforeState.candidateSha256 and
      .afterCandidateSha256==.afterState.candidateSha256 and
      .afterCandidateSha256==$empty_candidates_sha and
      .afterFingerprintSha256==.afterState.proofSha256 and
      .journalBeforeSha256==.beforeState.journal.sha256 and .journalAfterSha256==.afterState.journal.sha256 and
      .versionMapBeforeSha256==.beforeState.versionMap.sha256 and .versionMapAfterSha256==.afterState.versionMap.sha256 and
      .databaseBeforeSha256==.beforeState.databaseSha256 and .databaseAfterSha256==.afterState.databaseSha256 and
      (.journalAppendedRecords|type=="array" and length==7) and
      all(.journalAppendedRecords[];
        .family=="board_card" and (.object_id|type=="string" and length>0) and
        (.state_sha256|type=="string" and test("^[0-9a-f]{64}$")) and
        .reason=="legacy_reconciliation_source_absence_backfill_v1" and
        (.evidence_basis=="done_archive_absence" or .evidence_basis=="last_positive_source_current_absence") and
        ((.operation_id // "")=="") and ((.phase // "")=="") and
        ((.board_before_sha256 // "")=="") and ((.board_after_sha256 // "")=="") and
        (.at|type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T"))) and
      ([.journalAppendedRecords[] | {objectId:.object_id,stateSha256:.state_sha256,evidenceBasis:.evidence_basis}] ==
        [$manifest[0].candidates[] | {objectId,stateSha256,evidenceBasis}]) and
      all([.boardSha256,.journalBeforeSha256,.journalAfterSha256,.versionMapBeforeSha256,
        .versionMapAfterSha256,.databaseBeforeSha256,.databaseAfterSha256,
        .beforeCandidateSha256,.afterCandidateSha256,.afterFingerprintSha256,.finalParitySha256][];
        type=="string" and test("^[0-9a-f]{64}$")) and
      .databaseBeforeSha256!=.databaseAfterSha256 and
      (.completedAt | type=="string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")) and
      .receiptSha256==$payload_sha
    ' "$receipt" >/dev/null || die 'canonical repair receipt did not satisfy the exact sealed zero-parity contract'
}

assert_canonical_repair_receipt() {
  local receipt=$1
  assert_root_private_regular_file "$receipt" 'canonical repair receipt'
  assert_canonical_repair_manifest_binding
  assert_canonical_repair_authority_marker false
  validate_canonical_repair_receipt_payload "$receipt" "$A" "$REPAIR_MANIFEST_SHA" "$REPAIR_AUTHORITY_SHA" "$REPAIR_MANIFEST_PATH"
}

load_plan() {
  require_commands jq sha256sum
  test -f "$PLAN_FILE" || die "missing $PLAN_FILE"
  test ! -L "$PLAN_FILE" || die 'bootstrap plan must not be a symlink'
  test "$(stat -c %U "$PLAN_FILE")" = root || die 'bootstrap plan must be root-owned'
  (( (8#$(stat -c %a "$PLAN_FILE") & 8#022) == 0 )) || die 'bootstrap plan must not be group/world writable'
  jq -e '
    .schema=="bonfire.first-exact-bootstrap-plan.v3" and .remote=="axx" and .branch=="main" and
    (has("canonicalRepairManifestSha256") | not)
  ' "$PLAN_FILE" >/dev/null \
    || die 'bootstrap plan schema or reviewed remote/branch binding is invalid'
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

assert_forward_ceremony_permitted() {
  local terminal
  for terminal in public-open-attempted ceremony-retired legacy-restored legacy-reopened; do
    ! phase_done "$terminal" \
      || die "terminal ceremony state $terminal forbids every forward bootstrap phase"
  done
}

assert_restart_untouched_phase_boundary() {
  local changed
  for changed in \
    canonical-normalization-setup-started canonical-normalization-started canonical-normalization-failed canonical-normalized \
    canonical-manifest-generation-started canonical-manifest-generation-failed repair-manifest-generated \
    canonical-repair-authorized legacy-retirement-started legacy-retired \
    clone-qualification-started clone-qualification-failed clone-qualified \
    canonical-repair-execution-started canonical-repair-failed canonical-repair-receipted canonical-repaired \
    a-accepted b-activation-committed b-accepted public-open-attempted ceremony-retired legacy-restored legacy-reopened; do
    ! phase_done "$changed" || die "production state may have changed at phase $changed; restart-untouched is forbidden and exact cold restore is required"
  done
}

assert_forward_maintenance_state() {
  local wan marker_line
  assert_forward_ceremony_permitted
  require_phase isolated
  require_phase external-block-confirmed
  wan=$(jq -er '.wanInterface' "$STATE_FILE")
  test "$wan" = eth0 || die "maintenance WAN interface drifted from eth0 to $wan"
  assert_persistent_ingress_guard
  assert_ephemeral_ingress_guard "$wan"
  marker_line="127.0.0.1 $HOST $HOSTS_MARKER"
  test "$(grep -Fxc "$marker_line" /etc/hosts)" -eq 1 \
    || die 'exact maintenance loopback hosts marker is missing or duplicated'
  getent ahostsv4 "$HOST" | awk 'NR==1{seen=1; exit($1!="127.0.0.1")} END{if(!seen)exit 1}' \
    || die 'maintenance hostname no longer resolves first to loopback'
  assert_renderer_security_profiles
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

renderer_apparmor_source() {
  printf '%s/sealed-candidate/deploy/digitalocean/bonfire-render-runner-v1.apparmor\n' "$1"
}

renderer_seccomp_source() {
  printf '%s/sealed-candidate/deploy/digitalocean/bonfire-render-runner-v1.seccomp.json\n' "$1"
}

assert_renderer_profile_sources() {
  local a_apparmor a_seccomp b_apparmor b_seccomp source
  a_apparmor=$(renderer_apparmor_source "$ADIR")
  a_seccomp=$(renderer_seccomp_source "$ADIR")
  b_apparmor=$(renderer_apparmor_source "$BDIR")
  b_seccomp=$(renderer_seccomp_source "$BDIR")
  for source in "$a_apparmor" "$a_seccomp" "$b_apparmor" "$b_seccomp"; do
    test -f "$source" && test ! -L "$source" || die "renderer security profile is missing or unsafe: $source"
  done
  cmp "$a_apparmor" "$b_apparmor" || die 'A/B renderer AppArmor profiles differ'
  cmp "$a_seccomp" "$b_seccomp" || die 'A/B renderer seccomp profiles differ'
  grep -Fx "profile \"$RENDERER_APPARMOR_NAME\" flags=(attach_disconnected,mediate_deleted) {" "$a_apparmor" >/dev/null \
    || die 'renderer AppArmor profile name or flags changed'
  grep -Eq '^[[:space:]]*userns,[[:space:]]*$' "$a_apparmor" \
    || die 'renderer AppArmor profile does not explicitly permit a confined user namespace'
  jq -e '
    type=="object" and
    (.defaultAction | type=="string" and startswith("SCMP_ACT_")) and
    (.archMap | type=="array" and length>0) and
    (.syscalls | type=="array" and length>0 and all(.[]; (.names | type=="array" and length>0) and (.action | type=="string")))
  ' "$a_seccomp" >/dev/null || die 'renderer seccomp profile is not a valid reviewed Docker seccomp policy'
}

assert_renderer_security_profiles() {
  local restriction
  require_commands apparmor_parser cmp grep jq sha256sum stat sysctl
  assert_renderer_profile_files_exact
  restriction=$(sysctl -n kernel.apparmor_restrict_unprivileged_userns)
  test "$restriction" = 1 || die 'kernel.apparmor_restrict_unprivileged_userns must remain 1'
  grep -Fx "$RENDERER_APPARMOR_NAME (enforce)" /sys/kernel/security/apparmor/profiles >/dev/null \
    || die 'renderer AppArmor profile is not loaded in enforce mode'
}

assert_renderer_profile_files_exact() {
  local source_apparmor source_seccomp
  require_commands cmp jq stat
  assert_renderer_profile_sources
  source_apparmor=$(renderer_apparmor_source "$ADIR")
  source_seccomp=$(renderer_seccomp_source "$ADIR")
  test -f "$RENDERER_APPARMOR_PATH" && test ! -L "$RENDERER_APPARMOR_PATH" \
    || die 'installed renderer AppArmor profile is missing or unsafe'
  test -f "$RENDERER_SECCOMP_PATH" && test ! -L "$RENDERER_SECCOMP_PATH" \
    || die 'installed renderer seccomp profile is missing or unsafe'
  test "$(stat -c %U:%G "$RENDERER_APPARMOR_PATH")" = root:root \
    && test "$(stat -c %a "$RENDERER_APPARMOR_PATH")" = 644 \
    || die 'installed renderer AppArmor profile must be root:root 0644'
  test "$(stat -c %U:%G "$RENDERER_SECCOMP_PATH")" = root:root \
    && test "$(stat -c %a "$RENDERER_SECCOMP_PATH")" = 644 \
    || die 'installed renderer seccomp profile must be root:root 0644'
  cmp "$source_apparmor" "$RENDERER_APPARMOR_PATH" \
    || die 'installed renderer AppArmor profile differs from exact A/B source'
  cmp "$source_seccomp" "$RENDERER_SECCOMP_PATH" \
    || die 'installed renderer seccomp profile differs from exact A/B source'
  jq -e . "$RENDERER_SECCOMP_PATH" >/dev/null \
    || die 'installed renderer seccomp profile is not valid JSON'
}

install_renderer_security_profiles() {
  local source_apparmor source_seccomp
  require_commands apparmor_parser install jq sysctl
  assert_renderer_profile_sources
  source_apparmor=$(renderer_apparmor_source "$ADIR")
  source_seccomp=$(renderer_seccomp_source "$ADIR")
  test "$(sysctl -n kernel.apparmor_restrict_unprivileged_userns)" = 1 \
    || die 'refusing renderer profile install while restricted user namespaces are disabled'
  apparmor_parser -Q -K "$source_apparmor" \
    || die 'renderer AppArmor profile failed a no-load parse'
  install -d -o root -g root -m 755 /etc/docker/seccomp
  install -o root -g root -m 644 "$source_apparmor" "$RENDERER_APPARMOR_PATH"
  install -o root -g root -m 644 "$source_seccomp" "$RENDERER_SECCOMP_PATH"
  apparmor_parser -r -W "$RENDERER_APPARMOR_PATH"
  assert_renderer_security_profiles
  {
    printf 'kernel.apparmor_restrict_unprivileged_userns=%s\n' "$(sysctl -n kernel.apparmor_restrict_unprivileged_userns)"
    sha256sum "$RENDERER_APPARMOR_PATH" "$RENDERER_SECCOMP_PATH"
    grep -Fx "$RENDERER_APPARMOR_NAME (enforce)" /sys/kernel/security/apparmor/profiles
  } >"$BK/meta/renderer-security-profiles.txt"
  chmod 600 "$BK/meta/renderer-security-profiles.txt"
}

remove_renderer_security_profiles() {
  local apparmor_exists=false seccomp_exists=false loaded_lines cleanup_started
  require_commands apparmor_parser docker jq
  assert_no_renderer_profile_container_users
  test ! -e "$RENDERER_APPARMOR_PATH" || apparmor_exists=true
  test ! -e "$RENDERER_SECCOMP_PATH" || seccomp_exists=true
  loaded_lines=$(grep -F "$RENDERER_APPARMOR_NAME (" /sys/kernel/security/apparmor/profiles 2>/dev/null || true)
  cleanup_started=$(marker_path renderer-profiles-remove-started)

  if test "$apparmor_exists" = false && test "$seccomp_exists" = false; then
    test -z "$loaded_lines" || die 'renderer AppArmor profile is loaded without its exact release files'
    return 0
  fi

  if test "$apparmor_exists" = true && test "$seccomp_exists" = true; then
    assert_renderer_profile_files_exact
  elif test -f "$cleanup_started" && test -z "$loaded_lines"; then
    # An interruption between the two unlink syscalls is resumable only when
    # every surviving file is still the exact release-owned regular file.
    if test "$apparmor_exists" = true; then
      test -f "$RENDERER_APPARMOR_PATH" && test ! -L "$RENDERER_APPARMOR_PATH" \
        && test "$(stat -c %U:%G "$RENDERER_APPARMOR_PATH")" = root:root \
        && test "$(stat -c %a "$RENDERER_APPARMOR_PATH")" = 644 \
        && cmp "$(renderer_apparmor_source "$ADIR")" "$RENDERER_APPARMOR_PATH" \
        || die 'interrupted renderer AppArmor cleanup left drifted state'
    fi
    if test "$seccomp_exists" = true; then
      test -f "$RENDERER_SECCOMP_PATH" && test ! -L "$RENDERER_SECCOMP_PATH" \
        && test "$(stat -c %U:%G "$RENDERER_SECCOMP_PATH")" = root:root \
        && test "$(stat -c %a "$RENDERER_SECCOMP_PATH")" = 644 \
        && cmp "$(renderer_seccomp_source "$ADIR")" "$RENDERER_SECCOMP_PATH" \
        || die 'interrupted renderer seccomp cleanup left drifted state'
    fi
  else
    die 'renderer security profile files are partial or drifted without an owned cleanup transition'
  fi

  if test -n "$loaded_lines"; then
    test "$loaded_lines" = "$RENDERER_APPARMOR_NAME (enforce)" \
      || die 'renderer AppArmor profile is loaded in an unexpected mode or duplicate state'
    test "$apparmor_exists" = true && test "$seccomp_exists" = true \
      || die 'renderer AppArmor profile is loaded without both exact release files'
    apparmor_parser -R "$RENDERER_APPARMOR_PATH"
  fi
  ! grep -F "$RENDERER_APPARMOR_NAME (" /sys/kernel/security/apparmor/profiles >/dev/null 2>&1 \
    || die 'renderer AppArmor profile remained loaded after removal'
  mark_phase renderer-profiles-remove-started
  rm -f "$RENDERER_APPARMOR_PATH" "$RENDERER_SECCOMP_PATH"
  test ! -e "$RENDERER_APPARMOR_PATH" && test ! -e "$RENDERER_SECCOMP_PATH" \
    || die 'renderer security profile cleanup was incomplete'
  mark_phase renderer-profiles-removed
}

assert_no_renderer_profile_container_users() {
  local ids=()
  mapfile -t ids < <(docker ps -aq --no-trunc)
  test "${#ids[@]}" -eq 0 && return 0
  docker inspect "${ids[@]}" | jq -e --arg profile "$RENDERER_APPARMOR_NAME" '
    all(.[];
      ((.AppArmorProfile // "") != $profile) and
      all((.HostConfig.SecurityOpt // [])[];
        . != ("apparmor=" + $profile) and . != ("apparmor:" + $profile)))
  ' >/dev/null || die 'a running or restartable container still uses the renderer AppArmor profile'
}

renderer_security_canary() {
  local dir=$1 image output
  assert_renderer_security_profiles
  image=$(jq -er '.images.renderRunner.imageId' "$dir/release-receipt.json")
  output="$BK/meta/renderer-security-canary.txt"
  docker run --rm --name "bonfire-render-security-canary-$$" \
    --network none --user 65532:65532 --cap-drop ALL --read-only \
    --security-opt "apparmor=$RENDERER_APPARMOR_NAME" \
    --security-opt no-new-privileges:true \
    --security-opt "seccomp=$RENDERER_SECCOMP_PATH" \
    --pids-limit 256 --memory 1024m --shm-size 256m \
    --tmpfs /tmp:rw,nosuid,nodev,noexec,size=512m \
    --env HOME=/tmp --entrypoint /bin/sh "$image" -eu -c '
      grep -Eq "^Uid:[[:space:]]+65532[[:space:]]+65532[[:space:]]+65532[[:space:]]+65532$" /proc/self/status
      grep -Eq "^Gid:[[:space:]]+65532[[:space:]]+65532[[:space:]]+65532[[:space:]]+65532$" /proc/self/status
      for field in CapInh CapPrm CapEff CapBnd CapAmb; do grep -Eq "^${field}:[[:space:]]+0+$" /proc/self/status; done
      grep -Eq "^NoNewPrivs:[[:space:]]+1$" /proc/self/status
      grep -Eq "^Seccomp:[[:space:]]+2$" /proc/self/status
      ! chroot / /bin/true >/dev/null 2>&1
      unshare --user /bin/true
      for flag in --mount --uts --ipc --net --pid --cgroup; do ! unshare "$flag" /bin/true >/dev/null 2>&1; done
      ! unshare --user --mount /bin/true >/dev/null 2>&1
      ! nsenter --user=/proc/self/ns/user /bin/true >/dev/null 2>&1
      work=/tmp/bonfire-render-canary
      mkdir -p "$work/profile"
      printf "%s\n" "<!doctype html><meta charset=utf-8><title>Bonfire renderer canary</title><h1>Sandboxed PDF canary</h1>" >"$work/input.html"
      /opt/chrome-headless-shell/chrome-headless-shell \
        --headless=new --disable-setuid-sandbox --disable-gpu \
        --disable-background-networking --disable-component-update \
        --disable-default-apps --disable-extensions --disable-sync \
        --metrics-recording-only --no-first-run --proxy-server=127.0.0.1:9 \
        --proxy-bypass-list=127.0.0.1 --user-data-dir="$work/profile" \
        --no-pdf-header-footer --virtual-time-budget=15000 \
        --print-to-pdf="$work/output.pdf" "file://$work/input.html"
      test -s "$work/output.pdf"
      pdftoppm -jpeg -singlefile -r 72 "$work/output.pdf" "$work/page"
      test -s "$work/page.jpg"
      sha256sum "$work/output.pdf" "$work/page.jpg"
    ' >"$output"
  test "$(wc -l <"$output")" -eq 2 || die 'renderer sandbox/PDF canary returned unexpected evidence'
  chmod 600 "$output"
}

project_service_id() {
  local service=$1
  docker ps -q --no-trunc \
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

docker_volume_tree_sha256() {
  local volume=$1 relative=${2:-.} mount root
  mount=$(docker volume inspect -f '{{.Mountpoint}}' "$volume")
  test -d "$mount" || die "missing volume mountpoint for $volume"
  root="$mount/$relative"
  test -d "$root" || die "missing required $volume tree: $relative"
  tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    --format=posix --pax-option=delete=atime,delete=ctime \
    --xattrs --acls --one-file-system -C "$root" -cpf - . | sha256sum | awk '{print $1}'
}

canonical_repair_database_sha256() {
  local container=$1
  docker exec "$container" pg_dump -U bonfire -d bonfire --no-owner --no-acl --format=p \
    | sed -e '/^\\restrict /d' -e '/^\\unrestrict /d' \
    | sha256sum | awk '{print $1}'
}

canonical_repair_database_watermarks() {
  local container=$1
  docker exec -i "$container" psql -XqAt -v ON_ERROR_STOP=1 -U bonfire -d bonfire <<'SQL' | jq -cS .
SELECT jsonb_build_object(
  'schemaMigrationCount', (SELECT count(*) FROM schema_migrations),
  'schemaMigrationHighWater', (SELECT COALESCE(max(version),0) FROM schema_migrations),
  'canonicalEventCount', (SELECT count(*) FROM canonical_events),
  'canonicalEventHighWater', (SELECT COALESCE(max(sequence),0) FROM canonical_events),
  'objectCount', (SELECT count(*) FROM objects),
  'objectLastEventHighWater', (SELECT COALESCE(max(last_event_sequence),0) FROM objects),
  'outboxCount', (SELECT count(*) FROM outbox),
  'outboxHighWater', (SELECT COALESCE(max(outbox_id),0) FROM outbox),
  'outboxPending', (SELECT count(*) FROM outbox WHERE delivered_at IS NULL),
  'outboxFailed', (SELECT count(*) FROM outbox WHERE last_error_code IS NOT NULL)
)::text;
SQL
}

capture_canonical_repair_fingerprint() {
  local pgc=$1 output=$2 data_sha codex_sha render_sha usage_sha spool_sha database_sha checkpoint_high_water checkpoint_sha watermarks
  data_sha=$(docker_volume_tree_sha256 digitalocean_meeting_data)
  codex_sha=$(docker_volume_tree_sha256 digitalocean_codex_queue)
  render_sha=$(docker_volume_tree_sha256 digitalocean_render_queue)
  usage_sha=$(docker_volume_tree_sha256 digitalocean_usage_ledger)
  spool_sha=$(docker_volume_tree_sha256 digitalocean_meeting_data canonical)
  database_sha=$(canonical_repair_database_sha256 "$pgc")
  watermarks=$(canonical_repair_database_watermarks "$pgc")
  local meeting_mount checkpoint
  meeting_mount=$(docker volume inspect -f '{{.Mountpoint}}' digitalocean_meeting_data)
  checkpoint="$meeting_mount/canonical/reconcile-checkpoint.json"
  test -f "$checkpoint" && test ! -L "$checkpoint" || die 'canonical reconcile checkpoint is missing or unsafe'
  checkpoint_sha=$(sha256sum "$checkpoint" | awk '{print $1}')
  checkpoint_high_water=$(jq -er '.highWater | select(type=="number" and .>=0 and floor==.)' "$checkpoint")
  for digest in "$data_sha" "$codex_sha" "$render_sha" "$usage_sha" "$spool_sha" "$database_sha" "$checkpoint_sha"; do
    require_sha256 "$digest"
  done
  jq -nS \
    --arg schema 'bonfire.canonical-repair-fingerprint.v1' \
    --arg dataSha256 "$data_sha" --arg codexQueueSha256 "$codex_sha" \
    --arg renderQueueSha256 "$render_sha" \
    --arg usageLedgerSha256 "$usage_sha" --arg canonicalSpoolSha256 "$spool_sha" \
    --arg databaseSha256 "$database_sha" --arg checkpointSha256 "$checkpoint_sha" \
    --argjson checkpointHighWater "$checkpoint_high_water" --argjson databaseWatermarks "$watermarks" \
    '{schema:$schema,dataSha256:$dataSha256,codexQueueSha256:$codexQueueSha256,renderQueueSha256:$renderQueueSha256,usageLedgerSha256:$usageLedgerSha256,canonicalSpoolSha256:$canonicalSpoolSha256,databaseSha256:$databaseSha256,checkpointSha256:$checkpointSha256,checkpointHighWater:$checkpointHighWater,databaseWatermarks:$databaseWatermarks}' \
    >"$output"
  chmod 600 "$output"
}

capture_stable_canonical_repair_fingerprint() {
  local pgc=$1 output=$2
  local second="$output.second"
  capture_canonical_repair_fingerprint "$pgc" "$output"
  sync
  capture_canonical_repair_fingerprint "$pgc" "$second"
  cmp "$output" "$second" || die 'production data, database, spool, or high-water fingerprint was not stable'
  rm "$second"
  sha256sum "$output" | awk '{print $1}' >"$output.sha256"
  chmod 600 "$output.sha256"
  assert_canonical_repair_fingerprint_file "$output"
}

assert_canonical_repair_fingerprint_file() {
  local fingerprint=$1 expected actual
  assert_root_private_regular_file "$fingerprint" 'canonical repair stable fingerprint'
  assert_root_private_regular_file "$fingerprint.sha256" 'canonical repair stable fingerprint digest'
  expected=$(tr -d '[:space:]' <"$fingerprint.sha256")
  require_sha256 "$expected"
  actual=$(sha256sum "$fingerprint" | awk '{print $1}')
  test "$actual" = "$expected" || die 'canonical repair stable fingerprint self-digest mismatch'
  jq -e '
    .schema=="bonfire.canonical-repair-fingerprint.v1" and
    all([.dataSha256,.codexQueueSha256,.renderQueueSha256,.usageLedgerSha256,
      .canonicalSpoolSha256,.databaseSha256,.checkpointSha256][];
      type=="string" and test("^[0-9a-f]{64}$")) and
    (.checkpointHighWater | type=="number" and .>=0 and floor==.) and
    (.databaseWatermarks | type=="object")
  ' "$fingerprint" >/dev/null || die 'canonical repair stable fingerprint payload is invalid'
}

assert_repair_source_volumes_match_rehearsed_backup() {
  local volume mount
  (cd "$BK" && sha256sum -c backup-SHA256SUMS >/dev/null) \
    || die 'rehearsed cold backup checksum validation failed'
  for volume in digitalocean_meeting_data digitalocean_codex_queue digitalocean_usage_ledger digitalocean_canonical_postgres; do
    mount=$(docker volume inspect -f '{{.Mountpoint}}' "$volume")
    test -d "$mount" || die "missing production volume $volume"
    tar --xattrs --acls --compare -f "$BK/volumes/$volume.tar" -C "$mount" \
      || die "$volume drifted from the matched rehearsed cold backup before repair"
  done
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

assert_release_source_archive_binding() {
  local dir=$1 archive source_receipt release_receipt archive_hash source_receipt_hash
  archive="$dir/source.tar"
  source_receipt="$dir/source-receipt.json"
  release_receipt="$dir/release-receipt.json"
  for file in "$archive" "$source_receipt" "$release_receipt"; do
    test -f "$file" && test ! -L "$file" || die "release archive binding input is missing or unsafe: $file"
  done
  archive_hash=$(sha256sum "$archive" | awk '{print $1}')
  source_receipt_hash=$(sha256sum "$source_receipt" | awk '{print $1}')
  require_sha256 "$archive_hash"
  require_sha256 "$source_receipt_hash"
  jq -e --arg archive_hash "$archive_hash" '
    .schema=="bonfire.release-source.v3" and
    .sourceArchiveSha256==$archive_hash
  ' "$source_receipt" >/dev/null || die 'source archive differs from its reviewed source receipt'
  jq -e --arg source_receipt_hash "$source_receipt_hash" --arg archive_hash "$archive_hash" \
    --slurpfile source "$source_receipt" '
      .schema=="bonfire.release-receipt.v3" and
      .sourceReceiptSha256==$source_receipt_hash and
      .source==$source[0] and
      .buildManifest.source==$source[0] and
      .source.sourceArchiveSha256==$archive_hash
    ' "$release_receipt" >/dev/null || die 'release receipt does not bind the exact source receipt and archive'
}

migration_archive_hashes() {
  local archive=$1 entry verbose path version_name version digest
  local expected=(
    migrations/
    migrations/0001_canonical.sql
    migrations/0002_approval_repository.sql
    migrations/0003_purge_ledger.sql
    migrations/0004_brain_projection_checkpoints.sql
    migrations/0005_purge_ledger_object_type.sql
    migrations/0006_brain_projection_work.sql
    migrations/0007_catch_up_publications.sql
    migrations/0008_stride_contracts.sql
    migrations/0009_stride_conversation_ledger.sql
  )
  local actual=()
  tar -tf "$archive" >/dev/null || die 'source archive cannot be listed safely'
  while IFS= read -r entry; do
    case "$entry" in
      /*|./*|../*|*/../*|*/..|*/./*) die "source archive contains unsafe path: $entry" ;;
    esac
    if [[ $entry == migrations/* ]]; then actual+=("$entry"); fi
  done < <(tar -tf "$archive")
  test "${#actual[@]}" -eq "${#expected[@]}" \
    || die 'source archive migration inventory count is not exact'
  cmp <(printf '%s\n' "${expected[@]}" | LC_ALL=C sort) \
      <(printf '%s\n' "${actual[@]}" | LC_ALL=C sort) \
    || die 'source archive migration inventory is missing, duplicated, or extra'
  verbose=$(tar -tvf "$archive" | grep -E ' migrations/$' || true)
  test "$(printf '%s\n' "$verbose" | sed '/^$/d' | wc -l)" -eq 1 && [[ $verbose == d* ]] \
    || die 'source archive migrations root is not one exact directory entry'
  for path in "${expected[@]:1}"; do
    verbose=$(tar -tvf "$archive" -- "$path")
    test "$(printf '%s\n' "$verbose" | sed '/^$/d' | wc -l)" -eq 1 && [[ $verbose == -* ]] \
      || die "source archive migration is not one regular entry: $path"
    version_name=${path##*/}
    version=${version_name%%_*}
    version=$((10#$version))
    digest=$(tar -xOf "$archive" -- "$path" | sha256sum | awk '{print $1}')
    require_sha256 "$digest"
    printf '%s\t%s\n' "$version" "$digest"
  done
}

assert_migration_hash_rows() {
  local archive=$1 database_rows=$2
  cmp <(migration_archive_hashes "$archive") "$database_rows" \
    || die 'database migration hashes differ from the exact source archive bytes'
}

release_data_gate() {
  local dir=$1 label=$2 pgc versions after database_rows
  assert_release_source_archive_binding "$dir"
  pgc=$(project_service_id canonical-postgres)
  test -n "$pgc" || return 1
  versions=$(docker exec "$pgc" psql -XqAt -U bonfire -d bonfire \
    -c "select string_agg(version::text, ',' order by version) from schema_migrations")
  test "$versions" = '1,2,3,4,5,6,7,8,9' || return 1
  database_rows="$BK/database-migration-hashes-$label.tsv"
  docker exec "$pgc" psql -XqAt -F $'\t' -v ON_ERROR_STOP=1 -U bonfire -d bonfire \
    -c "select version,encode(sha256,'hex') from schema_migrations order by version" >"$database_rows"
  test "$(wc -l <"$database_rows")" -eq 9 || return 1
  assert_migration_hash_rows "$dir/source.tar" "$database_rows"
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
  assert_renderer_security_profiles
  diff -u \
    <(printf '%s\n' caddy canonical-postgres coturn meetingassist render-queue-init render-runner | sort) \
    <(docker ps -a --no-trunc --filter label=com.docker.compose.project=digitalocean \
      --format '{{.Label "com.docker.compose.service"}}' | sort -u)
  diff -u \
    <(printf '%s\n' digitalocean_caddy_config digitalocean_caddy_data digitalocean_canonical_postgres \
      digitalocean_codex_queue digitalocean_meeting_data digitalocean_render_queue digitalocean_usage_ledger | sort) \
    <(docker volume ls --format '{{.Name}}' | grep '^digitalocean_' | sort)
  diff -u \
    <(printf '%s\n' digitalocean_default digitalocean_render_internal | sort) \
    <(docker network ls --filter label=com.docker.compose.project=digitalocean --format '{{.Name}}' | sort)
  test -z "$(docker ps -aq --no-trunc --filter label=com.docker.compose.project=digitalocean \
    --filter label=com.docker.compose.service=codex-runner)"
  local init
  init=$(docker ps -aq --no-trunc --filter label=com.docker.compose.project=digitalocean \
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
