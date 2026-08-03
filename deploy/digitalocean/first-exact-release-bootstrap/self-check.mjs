#!/usr/bin/env node

import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const packDir = dirname(fileURLToPath(import.meta.url))
const repoRoot = resolve(packDir, '../../..')
const releaseToolPath = resolve(repoRoot, 'scripts/bonfire-release.mjs')
const { composeActivationArgs } = await import(releaseToolPath)

const read = name => readFile(resolve(packDir, name), 'utf8')
const [operatorReadme, prepare, common, bootstrap, rollback, guard, guardUnit, dockerDropin, releaseTool, digitalOceanReadme, rendererAppArmor, rendererSeccompText, mainSource, repairSource] = await Promise.all([
  read('README.md'),
  read('prepare-local.sh'),
  read('vps-common.sh'),
  read('vps-bootstrap.sh'),
  read('vps-rollback-legacy.sh'),
  read('bonfire-bootstrap-ingress-guard.sh'),
  read('bonfire-bootstrap-ingress-guard.service'),
  read('docker-ingress-guard.conf'),
  readFile(releaseToolPath, 'utf8'),
  readFile(resolve(packDir, '../README.md'), 'utf8'),
  readFile(resolve(repoRoot, 'deploy/digitalocean/bonfire-render-runner-v1.apparmor'), 'utf8'),
  readFile(resolve(repoRoot, 'deploy/digitalocean/bonfire-render-runner-v1.seccomp.json'), 'utf8'),
  readFile(resolve(repoRoot, 'main.go'), 'utf8'),
  readFile(resolve(repoRoot, 'canonical_board_repair.go'), 'utf8')
])
const rendererSeccomp = JSON.parse(rendererSeccompText)

const baseEnv = '/opt/meetingassist/deploy/digitalocean/.env'
const runtimeEnv = '/opt/meetingassist-releases/a/release.env'
const candidateCompose = '/opt/meetingassist-releases/a/sealed-candidate/deploy/digitalocean/docker-compose.yml'
assert.deepEqual(
  composeActivationArgs(baseEnv, runtimeEnv, candidateCompose),
  [
    'compose',
    '--project-name', 'digitalocean',
    '--project-directory', dirname(candidateCompose),
    '--env-file', baseEnv,
    '--env-file', runtimeEnv,
    '--file', candidateCompose,
    '--profile', 'render',
    'up', '-d', '--no-build', '--wait', '--wait-timeout', '120'
  ],
  'manual A Compose command drifted from the retained release tool'
)

const shellSources = [prepare, common, bootstrap, rollback].join('\n')
assert.equal(shellSources.includes('--remove-orphans'), false, 'bootstrap scripts must not remove ambiguous orphans')
assert.equal(shellSources.includes('--no-sandbox'), false, 'operator pack must not disable Chrome sandboxing')
assert.equal(shellSources.includes('seccomp=unconfined'), false, 'operator pack must not disable Docker seccomp confinement')
assert.equal(/local[^\n]*\boutput=[^\n]*\$output/.test(shellSources), false, 'strict-mode shell must not reference output in the same local declaration that initializes it')

for (const flag of [
  'observe-canonical-repair', 'repair-observation', 'normalize-canonical',
  'normalization-input', 'normalization-input-sha256', 'normalization-receipt',
  'generate-canonical-repair-manifest', 'evidence-dir', 'classified-evidence-descriptor',
  'repair-canonical', 'candidate-manifest', 'candidate-manifest-sha256', 'authority-marker', 'repair-receipt'
]) {
  assert.match(mainSource, new RegExp(`flag\\.(?:Bool|String)\\("${flag}"`), `exact A source is missing frozen maintenance flag --${flag}`)
  assert.match(bootstrap, new RegExp(`--${flag}`), `operator pack is missing frozen maintenance flag --${flag}`)
}
for (const contract of [
  'bonfire.canonical-board-normalization-input.v1',
  'bonfire.canonical-board-normalization-receipt.v1',
  'bonfire.canonical-board-repair-evidence.v1',
  'bonfire.canonical-board-repair-evidence-record.v1',
  'bonfire.canonical-board-repair-clone-run-authority.v1',
  'bonfire.canonical-board-repair-clone-qualification.v1',
  'bonfire.canonical-board-repair-restart-observation.v1',
  'bonfire.cold-clone-rehearsal-receipt.v1',
  'bonfire.canonical-board-repair.v2',
  'afterFingerprintSha256',
  'normalizedProofSha256',
  'importInputSha256',
  'normalizedObservation'
]) {
  assert.match(repairSource, new RegExp(contract.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `exact A repair source is missing frozen contract ${contract}`)
  assert.match([common, bootstrap].join('\n'), new RegExp(contract.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `operator pack is missing frozen contract ${contract}`)
}

assert.match(rendererAppArmor, /profile "bonfire-render-runner-v1" flags=\(attach_disconnected,mediate_deleted\)/, 'renderer AppArmor profile name or flags drifted')
assert.match(rendererAppArmor, /^\s*userns,\s*$/m, 'renderer AppArmor profile must explicitly mediate the Chrome user namespace')
assert.equal(rendererSeccomp.defaultAction, 'SCMP_ACT_ERRNO', 'renderer seccomp must fail closed by default')
assert.ok(Array.isArray(rendererSeccomp.archMap) && rendererSeccomp.archMap.length > 0, 'renderer seccomp architecture map is missing')
assert.ok(Array.isArray(rendererSeccomp.syscalls) && rendererSeccomp.syscalls.length > 0, 'renderer seccomp syscall rules are missing')
for (const comment of [
  'Bonfire renderer Chrome 150 namespace probe: exactly CLONE_NEWUSER|SIGCHLD',
  'Bonfire renderer Chrome 150 namespace launch: exactly CLONE_NEWUSER|CLONE_NEWPID|CLONE_NEWNET|SIGCHLD',
  'Bonfire renderer Chrome 150 inner namespace: exactly CLONE_NEWPID|SIGCHLD',
  'Bonfire renderer Chrome 150 namespace probe: exactly CLONE_NEWUSER',
  'Chrome namespace-sandbox chroot; the zero-capability outer process cannot pass the kernel check'
]) {
  assert.ok(rendererSeccomp.syscalls.some(rule => rule.comment === comment), `renderer seccomp exception is missing: ${comment}`)
}
assert.match(common, /RENDERER_APPARMOR_PATH=\/etc\/apparmor\.d\/bonfire-render-runner-v1/, 'renderer AppArmor install path drifted')
assert.match(common, /RENDERER_SECCOMP_PATH=\/etc\/docker\/seccomp\/bonfire-render-runner-v1\.json/, 'renderer seccomp install path drifted')
assert.match(common, /kernel\.apparmor_restrict_unprivileged_userns/, 'restricted user namespace sysctl gate is missing')
assert.match(common, /install -o root -g root -m 644 "\$source_apparmor" "\$RENDERER_APPARMOR_PATH"/, 'AppArmor install ownership or mode drifted')
assert.match(common, /install -o root -g root -m 644 "\$source_seccomp" "\$RENDERER_SECCOMP_PATH"/, 'seccomp install ownership or mode drifted')
assert.match(common, /cmp "\$source_apparmor" "\$RENDERER_APPARMOR_PATH"[\s\S]*cmp "\$source_seccomp" "\$RENDERER_SECCOMP_PATH"/, 'installed renderer profiles must byte-match exact release sources')
assert.match(common, /grep -Fx "\$RENDERER_APPARMOR_NAME \(enforce\)" \/sys\/kernel\/security\/apparmor\/profiles/, 'AppArmor enforce-mode attestation is missing')
assert.match(common, /--security-opt "apparmor=\$RENDERER_APPARMOR_NAME"[\s\S]*--security-opt no-new-privileges:true[\s\S]*--security-opt "seccomp=\$RENDERER_SECCOMP_PATH"/, 'disposable canary must use all exact confinement layers')
assert.match(common, /--network none --user 65532:65532 --cap-drop ALL --read-only/, 'disposable canary must remain networkless, non-root, capability-free, and read-only')
assert.match(common, /--print-to-pdf="\$work\/output\.pdf"[\s\S]*pdftoppm -jpeg -singlefile/, 'disposable renderer must prove PDF and JPEG output')
assert.match(bootstrap, /renderer_security_canary "\$ADIR"[\s\S]*mark_phase legacy-retirement-started/, 'renderer canary must pass before the retirement boundary')
assert.match(bootstrap, /up -d --no-build --wait --wait-timeout 300/, 'manual A cold-start health wait must be 300 seconds')
assert.match(rollback, /docker ps -aq --no-trunc --filter label=com\.docker\.compose\.project=digitalocean \| xargs -r docker rm -f[\s\S]*remove_renderer_security_profiles/, 'rollback must stop candidate containers before unloading profiles')
assert.match(common, /for terminal in public-open-attempted ceremony-retired legacy-restored legacy-reopened;/, 'forward terminal-state set drifted')
assert.match(common, /assert_forward_maintenance_state\(\)[\s\S]*assert_forward_ceremony_permitted[\s\S]*assert_persistent_ingress_guard[\s\S]*assert_ephemeral_ingress_guard "\$wan"[\s\S]*grep -Fxc "\$marker_line" \/etc\/hosts[\s\S]*getent ahostsv4 "\$HOST"[\s\S]*assert_renderer_security_profiles/, 'forward maintenance gate must prove terminal state, both ingress guards, exact loopback, and renderer profiles')
for (const phase of ['phase_normalize_canonical', 'phase_qualify_repair_clones', 'phase_generate_repair_manifest', 'phase_retire_legacy', 'phase_repair_canonical', 'phase_bootstrap_a', 'phase_activate_b']) {
  assert.match(bootstrap, new RegExp(`${phase}\\(\\)[\\s\\S]*assert_forward_maintenance_state`), `${phase} must re-prove live maintenance state`)
}
for (const phase of ['phase_init_build', 'phase_preflight', 'phase_isolate', 'phase_acknowledge_external_block', 'phase_prove_empty', 'phase_backup', 'phase_rehearse', 'phase_normalize_canonical', 'phase_qualify_repair_clones', 'phase_generate_repair_manifest', 'phase_retire_legacy', 'phase_repair_canonical', 'phase_bootstrap_a', 'phase_activate_b', 'phase_reopen']) {
  assert.match(bootstrap, new RegExp(`run_forward_phase ${phase}`), `forward dispatch must terminal-gate ${phase}`)
}
assert.match(common, /assert_no_renderer_profile_container_users[\s\S]*if test "\$apparmor_exists" = false && test "\$seccomp_exists" = false[\s\S]*if test "\$apparmor_exists" = true && test "\$seccomp_exists" = true[\s\S]*elif test -f "\$cleanup_started" && test -z "\$loaded_lines"/, 'renderer cleanup must reject users and support absent, exact-full, and owned interrupted states')
assert.match(common, /apparmor_parser -R "\$RENDERER_APPARMOR_PATH"[\s\S]*mark_phase renderer-profiles-remove-started[\s\S]*rm -f "\$RENDERER_APPARMOR_PATH" "\$RENDERER_SECCOMP_PATH"[\s\S]*mark_phase renderer-profiles-removed/, 'renderer cleanup state transitions must be interruption-resumable')
assert.match(rollback, /restart_untouched_legacy\(\)[\s\S]*remove_renderer_security_profiles[\s\S]*docker start "\$pgc"/, 'untouched rollback must remove profiles before restarting legacy')
assert.match(common, /assert_release_source_archive_binding "\$dir"/, 'release data gate must bind source.tar to both exact receipts')
assert.match(common, /migrations\/0001_canonical\.sql[\s\S]*migrations\/0009_stride_conversation_ledger\.sql[\s\S]*tar -tf "\$archive"/, 'migration gate must enumerate the exact 0001 through 0009 archive inventory')
assert.match(common, /tar -tvf "\$archive" -- "\$path"[\s\S]*\[\[ \$verbose == -\* \]\]/, 'migration gate must require each migration member to be regular')
assert.match(common, /tar -xOf "\$archive" -- "\$path" \| sha256sum/, 'migration hashes must stream archive bytes without extraction')
assert.equal(/sealed-candidate\/migrations/.test(common), false, 'migration verification must not read the config-only sealed candidate')
assert.match(common, /select version,encode\(sha256,'hex'\) from schema_migrations order by version[\s\S]*assert_migration_hash_rows "\$dir\/source\.tar"/, 'database hashes must be compared to exact source archive bytes')

for (const expected of [
  "bonfire-release: Command failed: docker volume inspect digitalocean_render_queue",
  "Error response from daemon: get digitalocean_render_queue: no such volume",
  "bonfire-release: active release ledger is missing"
]) {
  assert.match(bootstrap, new RegExp(expected.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `missing fail-closed verifier contract: ${expected}`)
}
assert.match(releaseTool, /throw new Error\('active release ledger is missing'\)/, 'retained verifier ledger terminal changed')
assert.match(releaseTool, /render_queue:\s*\{ name: 'digitalocean_render_queue', external: false \}/, 'retained render volume contract changed')

const sourceSelector = '{gitTreeDigest,reviewedInventorySha256,transitiveInputsSha256,buildConfigSha256,scopePolicySha256,inputCount,configFiles}'
assert.match(prepare, new RegExp(sourceSelector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), 'local A/B source selector drifted')
assert.match(bootstrap, new RegExp(sourceSelector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), 'VPS A/B source selector drifted')
for (const source of [prepare, bootstrap]) {
  const selectorLine = source.split('\n').find(line => line.includes('selector=') && line.includes('gitTreeDigest'))
  assert.ok(selectorLine, 'A/B source selector is missing')
  assert.equal(selectorLine.includes('sourceArchiveSha256'), false, 'commit-dependent archive digest must not be compared across A/B')
}
assert.match(prepare, /git -C "\$repo" diff --quiet "\$checkpoint" -- "\$pack_rel"/, 'mutable operator pack must match exact B')
assert.match(prepare, /cp -a "\$PACK_REL\/\." "\$OUT\/operator-pack\/"/, 'operator pack must be exported from the detached B worktree')
assert.match(prepare, /cmp "\$output" <\(printf 'A\\tdocs\/plans\/stride-next-evolution-master-plan\.md\\n'\)/, 'A to B diff must be the approved plan-only checkpoint')
assert.match(prepare, /operatorPackSha256:\$operatorPackSha256/, 'bootstrap plan must bind the exact operator pack')
assert.match(prepare, /bonfire\.first-exact-bootstrap-plan\.v3/, 'bootstrap plan must use the late-bound manifest schema')
assert.equal(prepare.includes('REPAIR_MANIFEST'), false, 'local preparation must not require a circular prerelease repair manifest')
assert.equal(prepare.includes('canonicalRepairManifestSha256'), false, 'bootstrap plan must not predict a production manifest hash')
assert.match(common, /operator pack differs from the exact B-bound manifest/, 'VPS must verify the B-bound operator pack digest')
assert.match(common, /assert_root_private_regular_file "\$REPAIR_MANIFEST_STATE"[\s\S]*test "\$actual" = "\$REPAIR_MANIFEST_SHA"/, 'VPS must require an exact root-private manifest bound only after generation')
assert.match(common, /schema=="bonfire\.canonical-board-repair\.v2"[\s\S]*environment=="production_protected_maintenance"[\s\S]*\.candidates \| type=="array" and length==7/, 'private repair manifest must bind exact A production maintenance and exactly seven candidates')
assert.equal(common.includes('.normalization | type=="array"'), false, 'stale schema-v1 normalization arrays must not remain in the pack validator')
assert.match(common, /CONFIRM CANONICAL BOARD REPAIR %s\\n/, 'repair authority must be the exact manifest-bound confirmation bytes')
assert.match(common, /assert_root_private_regular_file "\$REPAIR_AUTHORITY_PATH"[\s\S]*cmp "\$expected" "\$REPAIR_AUTHORITY_PATH"/, 'repair authority marker must be root-private and byte exact')
assert.match(common, /age <= 300/, 'repair authority must be fresh within five minutes at the start of a new append run')
assert.match(common, /del\(\.receiptSha256\)[\s\S]*bonfire\.canonical-repair-receipt\.v1[\s\S]*\.zeroCandidates==true[\s\S]*\.projectionParity==true[\s\S]*\.idempotentSecondReplay==true/, 'repair receipt must have an independently recomputed self-digest and terminal zero-parity proof')
assert.match(common, /expected_pack_sha=.*[\s\S]*require_sha256 "\$expected_pack_sha"/, 'operator pack digest must use the 64-hex SHA-256 validator')
assert.match(operatorReadme, /chown -R root:root \/root\/bonfire-first-exact-release-pack \/opt\/meetingassist-releases\/bootstrap-plan\.json/, 'copied operator inputs must be root-owned before load_plan')

const legacyVolumes = [
  'digitalocean_caddy_config',
  'digitalocean_caddy_data',
  'digitalocean_canonical_postgres',
  'digitalocean_codex_home',
  'digitalocean_codex_queue',
  'digitalocean_codex_runner_data',
  'digitalocean_meeting_data',
  'digitalocean_usage_ledger'
]
for (const volume of legacyVolumes) {
  assert.match(bootstrap, new RegExp(`\\b${volume}\\b`), `legacy backup omits ${volume}`)
  assert.match(rollback, new RegExp(`\\b${volume}\\b`), `legacy rollback omits ${volume}`)
}

const targetVolumes = [
  'digitalocean_caddy_config',
  'digitalocean_caddy_data',
  'digitalocean_canonical_postgres',
  'digitalocean_codex_queue',
  'digitalocean_meeting_data',
  'digitalocean_render_queue',
  'digitalocean_usage_ledger'
]
for (const volume of targetVolumes) {
  assert.match(common, new RegExp(`\\b${volume}\\b`), `target topology gate omits ${volume}`)
  assert.match(releaseTool, new RegExp(`\\b${volume}\\b`), `retained release tool omits ${volume}`)
}

assert.match(bootstrap, /docker rm "\$\{orphan\[0\]\}"/, 'legacy retirement must remove one explicitly identified codex-runner')
assert.match(bootstrap, /for volume in digitalocean_codex_home digitalocean_codex_runner_data;/, 'legacy retirement volume allowlist drifted')
assert.match(bootstrap, /phase_normalize_canonical\(\)[\s\S]*require_phase rehearsed[\s\S]*assert_repair_source_volumes_match_rehearsed_backup/, 'normalization must start only from the exact rehearsed cold backup')
assert.match(bootstrap, /--observe-canonical-repair[\s\S]*--normalize-canonical[\s\S]*--normalization-input-sha256/, 'normalization must be driven by a fresh exact-A observation and sealed input')
assert.match(bootstrap, /AUTHORIZE CANONICAL BOARD NORMALIZATION %s %s\\n/, 'normalization marker must bind the exact observation and backup receipt')
assert.match(bootstrap, /AUTHORIZE CANONICAL BOARD NORMALIZATION %s %s %s\\n/, 'isolated clone normalization marker must additionally bind the exact clone ID')
assert.match(repairSource, /canonicalNormalizationAuthorityText\(input[\s\S]*input\.Environment == "isolated_cold_clone" && input\.QualificationRun[\s\S]*input\.CloneID[\s\S]*input\.BeforeObservation\.SHA256[\s\S]*input\.BackupReceipt\.SHA256/, 'exact A must environment-discriminate clone normalization authority bytes')
assert.match(bootstrap, /canonical-normalization-started[\s\S]*exact A normalization failed; run the exact cold restore/, 'normalization interruption or failure must require cold restore')
assert.match(bootstrap, /if phase_done canonical-normalized; then[\s\S]*revalidate_completed_canonical_normalization[\s\S]*completed normalization failed full revalidation[\s\S]*return 0/, 'completed normalization must fully revalidate before an idempotent return')
assert.match(bootstrap, /revalidate_completed_canonical_normalization\(\)[\s\S]*canonical-normalization-live-revalidation[\s\S]*--observe-canonical-repair[\s\S]*exact-A live normalized state differs from the sealed normalization after-state[\s\S]*del\(\.observedAt\)[\s\S]*completed normalization live revalidation cleanup was incomplete/, 'completed normalization must take an exact-A read-only live observation, compare full state, and cleanly stop the isolated topology')
assert.match(bootstrap, /if phase_done canonical-normalization-setup-started \|\| phase_done canonical-normalization-started[\s\S]*normalization setup or execution crossed a process boundary/, 'a prior setup marker must fail closed before any cross-process normalization resume')
assert.match(bootstrap, /mark_phase canonical-normalization-setup-started[\s\S]*run_canonical_normalization_after_setup[\s\S]*mark_phase canonical-normalization-failed[\s\S]*schema, input, observation, or execution failed/, 'every post-setup normalization failure must durably mark the ceremony failed')
assert.match(bootstrap, /assert_production_normalization_input\(\)[\s\S]*bonfire\.canonical-board-normalization-input\.v1[\s\S]*normalizationAuthorityMarker==\$authority/, 'the pack must independently validate the production normalization input schema and sealed authority')
assert.match(bootstrap, /\.afterCandidateCount==7[\s\S]*\.lifecycleAppendCount==0[\s\S]*\.fullZeroDeltaSecondReplay==true/, 'normalization receipt must prove exact seven, no lifecycle append, and full second replay stability')
assert.match(bootstrap, /phase_qualify_repair_clones\(\)[\s\S]*require_phase canonical-normalized[\s\S]*run_repair_clone_qualification 1[\s\S]*run_repair_clone_qualification 2[\s\S]*clone-qualified/, 'two fresh clone qualifications must follow production normalization')
assert.match(bootstrap, /assert_stopped_clone_configured_attachment\(\)[\s\S]*State\.Running==false[\s\S]*HostConfig\.NetworkMode==\$network[\s\S]*NetworkSettings\.Networks\|keys\)==\[\$network\]/, 'stopped clone containers must prove their configured attachment through container inspect')
assert.match(bootstrap, /\(\(\.sourceArtifact\|type\)=="object"[\s\S]*\.sourceArtifact\.path\|type=="string"[\s\S]*\.sourceArtifact\.sha256\|type=="string"/, 'classified evidence validation must keep sourceArtifact field checks in the wrapper context')
assert.match(bootstrap, /run_clone_stage_container\(\)[\s\S]*assert_stopped_clone_configured_attachment "\$name" "\$network"[\s\S]*assert_clone_network_membership "\$network" "\$pg"[\s\S]*docker start "\$name"[\s\S]*assert_clone_network_membership "\$network" "\$pg" "\$name"[\s\S]*docker wait[\s\S]*assert_stopped_clone_configured_attachment "\$name" "\$network"[\s\S]*assert_clone_network_membership "\$network" "\$pg"/, 'clone one-shots must distinguish stopped configured attachment from active endpoint membership')
assert.match(bootstrap, /--cap-drop ALL[\s\\]*--cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETGID --cap-add SETUID[\s\S]*HostConfig\.CapAdd[\s\S]*\["CHOWN","DAC_OVERRIDE","FOWNER","SETGID","SETUID"\]/, 'qualification PostgreSQL must receive and prove only the exact capabilities its official entrypoint needs for a fresh private volume')
assert.match(bootstrap, /select\(\.Kind=="missing_event" or \.Kind=="state_mismatch"\)[\s\S]*as \$ordinaryDelta[\s\S]*\.delta\.tenantEvents==\$ordinaryDelta[\s\S]*\.delta\.importOutbox==\$ordinaryDelta[\s\S]*\.delta\.versionEntries[\s\S]*\.<=\$ordinaryDelta[\s\S]*versionEntryCount-\.beforeState\.versionEntryCount\)==\.delta\.versionEntries/, 'normalization receipt must derive exact event and outbox deltas plus the exact bounded version-entry delta from the sealed before-observation candidates')
assert.match(common, /\.delta==\{tenantEvents:7,importOutbox:7,versionEntries:7\}[\s\S]*journalAppendedRecords[\s\S]*length==7/, 'repair receipt must prove exact +7/+7/+7 and seven exact journal records')
assert.match(bootstrap, /all\(\[\.normalizationReceipt,\.manifest,\.cloneRunAuthority,\.coldCloneReceipt,\.repairReceipt,\.restartObservation\]/, 'clone qualification must explicitly seal every run authority and outcome artifact')
assert.match(bootstrap, /\.runs\|map\(\.cloneId\)==\(map\(\.cloneId\)\|sort\)\) and\s+all\(\.runs\[\];/, 'clone qualification sortedness must be evaluated against the runs array before per-run validation')
assert.match(bootstrap, /map\(\.sha256\)\|unique\|length\)==12/, 'clone qualification must reject digest aliasing across both runs')
assert.match(bootstrap, /cloneId:\$clone,qualificationRun:true[\s\S]*assert_cold_clone_rehearsal_receipt "\$cold_receipt" "\$clone_id"/, 'each qualification cold-clone receipt must bind its unique clone ID and qualification authority')
assert.match(bootstrap, /exact_utc_epoch_nanoseconds\(\)[\s\S]*cold_ns[\s\S]*authority_ns[\s\S]*normalization_ns[\s\S]*repair_ns[\s\S]*restart_ns[\s\S]*qualification_ns/, 'pack must validate canonical UTC timestamps and complete clone causal order')
assert.match(bootstrap, /sourceArtifact\.path[\s\S]*classified source artifact differs from its record seal/, 'classified evidence must validate each wrapper and its sealed source artifact')
assert.match(bootstrap, /phase_generate_repair_manifest\(\)[\s\S]*require_phase clone-qualified[\s\S]*--generate-canonical-repair-manifest[\s\S]*--classified-evidence-descriptor/, 'manifest generation must follow two-run clone qualification and consume classified sealed evidence')
assert.match(bootstrap, /assert_generated_manifest_evidence[\s\S]*repair-manifest-generated/, 'pack must independently verify generated manifest evidence before approval')
assert.match(bootstrap, /phase_retire_legacy\(\)[\s\S]*require_phase repair-manifest-generated[\s\S]*create_canonical_repair_authority_marker/, 'legacy retirement must follow late manifest generation and exact literal approval')
assert.match(bootstrap, /phase_repair_canonical\(\)[\s\S]*require_phase legacy-retired/, 'append repair must start only after approved legacy retirement')
assert.match(bootstrap, /docker network create --internal --label bonfire\.bootstrap\.role=canonical-repair/, 'repair database network must be internal and ceremony-owned')
assert.match(bootstrap, /assert_canonical_repair_network_membership\(\)[\s\S]*\(\.\[0\]\.Containers \/\/ \{\}\) \| keys \| sort\)==\$expected/, 'production repair network must have exact endpoint membership')
assert.match(bootstrap, /Docker does not publish a stopped container[\s\S]*\(\(\.\[0\]\.Containers \/\/ \{\}\)\|length\)==0[\s\S]*unexpected active endpoint/, 'stopped PostgreSQL setup must reject active endpoints without expecting a stopped endpoint in network inspect')
assert.match(bootstrap, /created container is stopped[\s\S]*assert_canonical_repair_network_membership "\$\(canonical_repair_postgres_id\)"[\s\S]*docker start "\$name"[\s\S]*assert_canonical_repair_network_membership "\$\(canonical_repair_postgres_id\)" "\$name"[\s\S]*docker wait "\$name"[\s\S]*assert_canonical_repair_network_membership "\$\(canonical_repair_postgres_id\)"/, 'canonical one-shot network checks must distinguish configured stopped attachment from active running membership')
assert.match(bootstrap, /com\.docker\.compose\.project\.config_files[\s\S]*legacy-compose-resolved\.yml[\s\S]*legacy-compose-provenance\.json[\s\S]*assert_legacy_compose_recovery_bundle/, 'backup must seal the Compose files that actually created the running predecessor topology')
assert.match(bootstrap, /assert_root_private_regular_file "\$legacy_env_file"[\s\S]*cmp "\$legacy_env_file" "\$BASE_ENV"[\s\S]*environmentFileSha256/, 'a chained restored predecessor environment must be root-private and byte-identical before it is resealed')
assert.match(common, /assert_legacy_compose_recovery_bundle\(\)[\s\S]*legacy-compose-resolved\.yml[\s\S]*legacy-compose-provenance\.json[\s\S]*render-queue-init[\s\S]*resolved legacy Compose recovery topology is not exact/, 'resolved predecessor Compose recovery bundle must be self-bound and exactly six services')
assert.match(rollback, /assert_legacy_compose_recovery_bundle[\s\S]*local compose="\$BK\/private\/legacy-compose-resolved\.yml"[\s\S]*--env-file "\$BK\/private\/base\.env" --file "\$compose"/, 'cold rollback must launch only the sealed resolved predecessor Compose bundle')
assert.doesNotMatch(rollback, /local compose=\/opt\/meetingassist\/deploy\/digitalocean\/docker-compose\.yml/, 'cold rollback must not infer topology from the mutable current Compose source')
assert.match(bootstrap, /assert_clone_network_membership\(\)[\s\S]*clone network contains an unknown endpoint/, 'qualification clone network must reject every unknown endpoint')
assert.match(bootstrap, /--network "\$REPAIR_NETWORK" --restart no --read-only --cap-drop ALL[\s\S]*--security-opt no-new-privileges:true/, 'repair one-shot must have no public network or privilege escape')
assert.match(bootstrap, /--env-file "\$BASE_ENV" --env-file "\$ADIR\/release\.env"[\s\S]*BONFIRE_CODEX_QUEUE_PATH=\/app\/codex-queue\/jobs[\s\S]*BONFIRE_RENDER_QUEUE_PATH=\/app\/render-queue\/jobs[\s\S]*digitalocean_meeting_data:\/app\/data[\s\S]*digitalocean_usage_ledger:\/app\/data\/usage[\s\S]*digitalocean_codex_queue:\/app\/codex-queue[\s\S]*digitalocean_render_queue:\/app\/render-queue/, 'repair must use exact A, normal production env, and both normal runtime queues at their exact writable mounts')
assert.match(bootstrap, /docker volume create --driver local[\s\S]*com\.docker\.compose\.volume=render_queue[\s\S]*digitalocean_render_queue/, 'repair must create the formerly missing render queue as the exact Compose-owned volume')
for (const flag of ['--repair-canonical', '--candidate-manifest', '--candidate-manifest-sha256', '--authority-marker', '--repair-receipt']) {
  assert.match(bootstrap, new RegExp(flag), `repair one-shot is missing ${flag}`)
}
assert.match(bootstrap, /mark_phase canonical-repair-execution-started[\s\S]*create_canonical_repair_container/, 'repair execution boundary must be durable before the one-shot can append')
assert.match(bootstrap, /append retry is forbidden, run the exact cold restore/, 'partial append must never be resumed across processes')
assert.match(bootstrap, /exited zero without an authoritative exact receipt; stdout is rejected and exact cold restore is required/, 'stdout-only repair success must be rejected in favor of cold restore')
assert.match(bootstrap, /capture_stable_canonical_repair_fingerprint[\s\S]*canonical-repair-execution-started[\s\S]*capture_stable_canonical_repair_fingerprint/, 'repair must record stable full pre/post fingerprints around execution')
assert.match(bootstrap, /revalidate_completed_canonical_repair\(\) \([\s\S]*completed-revalidation-before-fingerprint[\s\S]*create_canonical_repair_container[\s\S]*capture_stable_canonical_repair_fingerprint "\$pgc" "\$current_after"/, 'completed repair resume must invoke exact A and prove unchanged live post-state')
assert.match(common, /docker_volume_tree_sha256 digitalocean_render_queue[\s\S]*renderQueueSha256/, 'stable repair fingerprints must include the complete render queue volume')
assert.match(bootstrap, /\.environment=="production_protected_maintenance"[\s\S]*\.candidateFingerprintSha256==\$manifest\[0\]\.candidateSetSha256[\s\S]*\.candidateCount==7/, 'accepted receipt must reproduce the private exact-seven production candidate fingerprint')
assert.match(bootstrap, /legacy service identity or protected-volume mount inventory differs[\s\S]*legacy-container-authority\.tsv/, 'backup must bind exact legacy services, IDs, images, and protected mounts')
for (const exactMount of [
  'digitalocean_caddy_config",Destination:"/config",RW:true',
  'digitalocean_caddy_data",Destination:"/data",RW:true',
  'digitalocean_canonical_postgres",Destination:"/var/lib/postgresql/data",RW:true',
  'digitalocean_codex_queue",Destination:"/app/codex-queue",RW:true',
  'digitalocean_meeting_data",Destination:"/app/data",RW:true',
  'digitalocean_usage_ledger",Destination:"/app/data/usage",RW:true',
  'digitalocean_codex_runner_data",Destination:"/runner-data",RW:true',
  'digitalocean_usage_ledger",Destination:"/app/usage-ledger",RW:true'
]) assert.ok(bootstrap.includes(exactMount), `sealed legacy protected mount drifted: ${exactMount}`)
assert.match(bootstrap, /volumes\("coturn"\) \| length==1[\s\S]*Destination=="\/var\/lib\/coturn"[\s\S]*RW==true[\s\S]*startswith\("digitalocean_"\)\|not/, 'coturn must have exactly one expected anonymous RW state volume and no protected digitalocean volume')
assert.match(bootstrap, /assert_protected_volume_container_whitelist\(\)[\s\S]*sealed legacy container identity or mounts drifted[\s\S]*unowned container/, 'every protected-volume mounter must be an exact sealed legacy ID or owned one-shot')
assert.match(bootstrap, /\(\.\[0\]\.NetworkSettings\.Networks \| keys\)==\[\$network\]/, 'retained PostgreSQL and each one-shot must have only the internal ceremony network')
assert.match(rollback, /restart_untouched_legacy\(\)[\s\S]*assert_restart_untouched_phase_boundary[\s\S]*docker volume inspect digitalocean_render_queue[\s\S]*docker network inspect "\$REPAIR_NETWORK"[\s\S]*for owned in "\$NORMALIZE_CONTAINER" "\$MANIFEST_CONTAINER" "\$REPAIR_CONTAINER"[\s\S]*retained PostgreSQL network set changed/, 'restart-untouched must reject every normalization/runtime artifact and changed PostgreSQL networking')
assert.match(rollback, /current_pg_ids < <\(docker ps -aq --no-trunc[\s\S]*current_pgc=\$\{current_pg_ids\[0\]\}/, 'restart-untouched must discover the stopped retained PostgreSQL by full ID')
assert.match(bootstrap, /phase_start_next_ceremony\(\)[\s\S]*phase-legacy-restored[\s\S]*phase-legacy-reopened[\s\S]*phase-public-open-attempted[\s\S]*phase-built[\s\S]*phase-preflight[\s\S]*unexpected_phases[\s\S]*premaintenance_preflight_only[\s\S]*mv "\$STATE_DIR" "\$archive_root\/state"[\s\S]*terminalState:\$terminal[\s\S]*initialize_state/, 'next ceremony rollover must preserve only exact restored/reopened or exact pre-maintenance preflight-only state')
assert.match(common, /assert_restart_untouched_phase_boundary\(\)[\s\S]*canonical-normalization-setup-started[\s\S]*canonical-manifest-generation-started[\s\S]*canonical-repair-execution-started/, 'restart-untouched phase boundary must fail closed across normalization, manifest, and repair')
assert.match(bootstrap, /phase_bootstrap_a\(\)[\s\S]*require_phase canonical-repaired/, 'A bootstrap must remain blocked until exact repair acceptance')
assert.match(bootstrap, /find \. -type f ! -path \.\/backup-SHA256SUMS -print0/, 'backup checksum manifest must cover root and nested payloads')
assert.match(bootstrap, /if test -e "\$RELEASE_PARENT\/active-release\.json"; then[\s\S]*assert_generation_one_ledger \|\| die 'existing release ledger is not the exact generation-1 B\/A ledger'[\s\S]*else[\s\S]*node "\$\(release_tool "\$ADIR"\)" activate/, 'activate-b must resume post-activation gates from exact B')
assert.match(bootstrap, /mark_phase b-activation-committed[\s\S]*release_verify "\$BDIR"/, 'B activation must be marked before restartable post-activation gates')
assert.match(bootstrap, /validate_authenticated_smoke_payload "\$path"/, 'authenticated smokes must use endpoint-specific schemas')
for (const path of ['auth/me', 'rooms', 'assistant/chat-threads', 'assistant/board', 'assistant/files']) {
  assert.match(bootstrap, new RegExp(path.replace('/', '\\/')), `authenticated smoke validator omits ${path}`)
}
for (const token of ['80,443,3478', '3478', '40000:40100', '49160:49200']) {
  assert.match(guard, new RegExp(token.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `persistent ingress guard omits ${token}`)
}
assert.match(guard, /WAN_INTERFACE=eth0/, 'persistent guard must bind only the reviewed WAN interface')
assert.match(guardUnit, /Before=docker\.service/, 'persistent guard must run before Docker')
assert.match(dockerDropin, /Requires=bonfire-bootstrap-ingress-guard\.service[\s\S]*After=bonfire-bootstrap-ingress-guard\.service/, 'Docker must require the persistent guard')
assert.match(bootstrap, /mark_phase public-open-attempted[\s\S]*remove_persistent_ingress_guard_rules[\s\S]*remove_ephemeral_ingress_guard_rules/, 'B reopen must mark the irreversible boundary before removing either guard')
assert.match(rollback, /mark_phase public-open-attempted[\s\S]*remove_persistent_ingress_guard_rules[\s\S]*remove_ephemeral_ingress_guard_rules/, 'legacy reopen must mark the irreversible boundary before removing either guard')
assert.match(bootstrap, /retire_ephemeral_ingress_guard_chains "\$wan" \|\| ! retire_persistent_ingress_guard/, 'B cleanup must retain reboot protection until ephemeral cleanup succeeds')
assert.match(rollback, /retire_ephemeral_ingress_guard_chains "\$wan" \|\| ! retire_persistent_ingress_guard/, 'legacy cleanup must retain reboot protection until ephemeral cleanup succeeds')
assert.match(
  rollback,
  /phase_done public-open-attempted && die 'public traffic was opened or attempted; cold restore is now a possible data-loss\/reconciliation incident and this script refuses it'/,
  'cold rollback public-write boundary drifted'
)
assert.equal(rollback.includes('roll-forward-only'), false, 'cold rollback must remain supported after normalization or repair failure')
assert.match(rollback, /mark_phase ceremony-retired[\s\S]*for owned in "\$NORMALIZE_CONTAINER" "\$MANIFEST_CONTAINER" "\$REPAIR_CONTAINER"[\s\S]*docker network rm "\$REPAIR_NETWORK"[\s\S]*remove_renderer_security_profiles/, 'cold rollback must retire the ceremony and remove all owned one-shots, network, and profiles')
assert.match(rollback, /acknowledge_restored_block\(\)[\s\S]*RESTORED LEGACY BLOCK CONFIRMED FROM MAC[\s\S]*legacy-restored-health-reprobe/, 'restored legacy must receive an independent blocked-ingress and local health re-probe before reopen')
assert.match(
  digitalOceanReadme,
  /\[first exact-release bootstrap operator pack\]\(\.\/first-exact-release-bootstrap\/README\.md\)/,
  'DigitalOcean runbook must link to this versioned pack'
)
assert.match(
  digitalOceanReadme,
  /implementation commit \*\*A\*\*[\s\S]*docs-only, direct-child release-checkpoint commit \*\*B\*\*/,
  'DigitalOcean runbook must preserve the A-to-direct-child-B ceremony'
)

process.stdout.write('self-check: repository and bootstrap contracts passed\n')
