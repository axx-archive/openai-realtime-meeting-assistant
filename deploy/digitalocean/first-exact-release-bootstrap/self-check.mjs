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
const [operatorReadme, prepare, common, bootstrap, rollback, guard, guardUnit, dockerDropin, releaseTool, digitalOceanReadme, rendererAppArmor, rendererSeccompText] = await Promise.all([
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
  readFile(resolve(repoRoot, 'deploy/digitalocean/bonfire-render-runner-v1.seccomp.json'), 'utf8')
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
assert.match(rollback, /docker ps -aq --filter label=com\.docker\.compose\.project=digitalocean \| xargs -r docker rm -f[\s\S]*remove_renderer_security_profiles/, 'rollback must stop candidate containers before unloading profiles')
assert.match(common, /for terminal in public-open-attempted legacy-restored legacy-reopened;/, 'forward terminal-state set drifted')
assert.match(common, /assert_forward_maintenance_state\(\)[\s\S]*assert_forward_ceremony_permitted[\s\S]*assert_persistent_ingress_guard[\s\S]*assert_ephemeral_ingress_guard "\$wan"[\s\S]*grep -Fxc "\$marker_line" \/etc\/hosts[\s\S]*getent ahostsv4 "\$HOST"[\s\S]*assert_renderer_security_profiles/, 'forward maintenance gate must prove terminal state, both ingress guards, exact loopback, and renderer profiles')
for (const phase of ['phase_retire_legacy', 'phase_bootstrap_a', 'phase_activate_b']) {
  assert.match(bootstrap, new RegExp(`${phase}\\(\\)[\\s\\S]*assert_forward_maintenance_state`), `${phase} must re-prove live maintenance state`)
}
for (const phase of ['phase_init_build', 'phase_preflight', 'phase_isolate', 'phase_acknowledge_external_block', 'phase_prove_empty', 'phase_backup', 'phase_rehearse', 'phase_retire_legacy', 'phase_bootstrap_a', 'phase_activate_b', 'phase_reopen']) {
  assert.match(bootstrap, new RegExp(`run_forward_phase ${phase}`), `forward dispatch must terminal-gate ${phase}`)
}
assert.match(common, /assert_no_renderer_profile_container_users[\s\S]*if test "\$apparmor_exists" = false && test "\$seccomp_exists" = false[\s\S]*if test "\$apparmor_exists" = true && test "\$seccomp_exists" = true[\s\S]*elif test -f "\$cleanup_started" && test -z "\$loaded_lines"/, 'renderer cleanup must reject users and support absent, exact-full, and owned interrupted states')
assert.match(common, /apparmor_parser -R "\$RENDERER_APPARMOR_PATH"[\s\S]*mark_phase renderer-profiles-remove-started[\s\S]*rm -f "\$RENDERER_APPARMOR_PATH" "\$RENDERER_SECCOMP_PATH"[\s\S]*mark_phase renderer-profiles-removed/, 'renderer cleanup state transitions must be interruption-resumable')
assert.match(rollback, /restart_untouched_legacy\(\)[\s\S]*remove_renderer_security_profiles[\s\S]*docker start "\$pgc"/, 'untouched rollback must remove profiles before restarting legacy')

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
assert.match(common, /operator pack differs from the exact B-bound manifest/, 'VPS must verify the B-bound operator pack digest')
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
