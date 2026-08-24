import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import test from 'node:test'

import {
  acquireReleaseOperationLock, assertPrivateRealtimeVoiceQualificationHostRuntimeMatch,
  assertPriorBaseEnvRestored, assertTargetBaseEnvPatchInstalled,
  commitPriorBaseEnvRestore, commitTargetBaseEnvPatch,
  composeActivationArgs, composeIngressArgs, composePrivateActivationArgs,
  computeBundleSha256, computeEnvironmentMarker, environmentValues,
  executeReleaseTransaction, inspectExtractedArchive, releaseActivationProgress, releaseComposeEnvironment, releaseEnvironmentValues, releasePathOwned,
  loadRetainedRollbackTool, restoreReleaseBundleAfterFailedActivation,
  planRollbackProjectCleanup, planRollbackProjectResourceCleanup, projectContainerSnapshotSha256,
  projectResourceClaimsFromContainers, projectResourceSnapshotSha256, releasePaths, renderedComposeSha256,
  reviewedInventoryDigest, validateBuildInputs, validateCandidateBundleManifest, validatePrepareState,
  validateActiveReleaseLedger, validateProjectResourceBaseline, validateProjectServiceInventory, validateReleaseReceipt, validateReleaseScopePolicy,
  validateReleaseTransition, validateRenderedComposeConfig, validateRendererRuntimeConfinement, validateReviewedInventory, validateSourceReceipt,
  verifyArchiveIdentity, verifyCandidateConfig, verifyLabels, verifyProbeRelease,
  verifyExecutingReleaseTool, verifyReleaseEnvironmentFile, verifyRenderRunnerHeartbeat,
  verifyRetainedReleaseActivator, verifyRuntimeEnvironment,
  assertQualificationTransitionBound, baseEnvPatchDigestState, baseEnvPatchPlanFromReceipt,
  baseEnvPatchTemporaryPath,
  executeDurableReleasePhaseMachine, executeDurableReleaseRecoveryPhaseMachine,
  installTargetBaseEnvPatch, prepareTargetBaseEnvPatch,
  privateRealtimeVoiceQualificationEnvPatch, privateRealtimeVoiceQualificationEnvState,
  privateRealtimeVoiceQualificationRuntimeState,
  priorBaseEnvCommitDisposition, releaseTransactionCompletionEvidence,
  reinstallCommittedTargetBaseEnvPatch, requestedQualificationRollbackReceipt, requestedTargetBaseEnvPatch,
  restorePriorBaseEnv,
  strideE10W4MaintenanceArgs, strideE10W4RecoveryPlan, strideE10W4ReleaseTransitionPlan,
  validateBaseEnvPatchPlan, validateBaseEnvPatchReceipt, validatePrivateReleasePathInfo,
  validateReleaseTransactionJournal, verifyBaseEnvPatchRuntimeEnvironment,
  validateStrideE10W4ComposeSource, validateStrideE10W4DeploymentPolicy
} from './bonfire-release.mjs'

const digest = char => char.repeat(64)
const execFileAsync = promisify(execFile)
const releaseToolPath = fileURLToPath(new URL('./bonfire-release.mjs', import.meta.url))
const repoRoot = fileURLToPath(new URL('../', import.meta.url))
const releaseCommit = '1'.repeat(40)
const configPaths = [
  '.dockerignore', 'Dockerfile', 'Dockerfile.render', 'go.mod', 'go.sum',
  'deploy/digitalocean/docker-compose.yml', 'deploy/digitalocean/Caddyfile',
  'deploy/digitalocean/bonfire-render-runner-v1.apparmor',
  'deploy/digitalocean/bonfire-render-runner-v1.seccomp.json',
  'deploy/digitalocean/release-build-inputs.json', 'deploy/digitalocean/release-scope-policy.json',
  'scripts/bonfire-release.mjs'
]
const sidecarRefs = {
  canonicalPostgres: `postgres@sha256:${digest('a')}`,
  coturn: `coturn/coturn@sha256:${digest('b')}`,
  caddy: `caddy@sha256:${digest('c')}`
}
const buildInputs = {
  schema: 'bonfire.release-build-inputs.v2', platform: 'linux/amd64', goVersion: '1.26',
  goBuildImage: `golang:1.26-bookworm@sha256:${digest('a')}`,
  runtimeImage: `debian:bookworm-slim@sha256:${digest('b')}`,
  debianSnapshot: '20260720T000000Z',
  debianArchive: 'http://snapshot.debian.org/archive/debian/20260720T000000Z',
  debianSecurityArchive: 'http://snapshot.debian.org/archive/debian-security/20260720T000000Z',
  buildPackages: ['libopus-dev', 'pkg-config'], runtimePackages: ['ca-certificates', 'curl', 'libopus0'],
  renderRuntimePackages: ['ca-certificates', 'libopus0', 'poppler-utils'],
  chromeHeadlessShellVersion: '150.0.7871.46', chromeHeadlessShellArchiveSha256: digest('d'),
  rendererSandbox: {
    apparmorProfile: 'bonfire-render-runner-v1', apparmorAbi: '4.0',
    seccompProfilePath: '/etc/docker/seccomp/bonfire-render-runner-v1.json',
    seccompBase: 'github.com/moby/profiles/seccomp/v0.2.3',
    seccompBaseSha256: '536529b665dd0972c37bfb569f5d4ac8a53592e7b00752bc39ff063ca9864c74',
    seccompAllowDeltaCount: 5
  },
  sidecarImages: sidecarRefs, dependencyLocks: ['go.mod', 'go.sum']
}
const configFiles = Object.fromEntries(configPaths.map((path, index) => [path, digest((index % 10).toString())]))
const source = {
  schema: 'bonfire.release-source.v3', releaseCommit, reviewedRef: releaseCommit,
  gitTreeObject: '2'.repeat(40), gitTreeDigest: digest('3'), reviewedInventorySha256: digest('4'),
  scopePolicySha256: configFiles['deploy/digitalocean/release-scope-policy.json'],
  sourceArchiveSha256: digest('5'), transitiveInputsSha256: digest('6'),
  buildConfigSha256: configDigest(configFiles), configFiles, inputCount: 30,
  sourceDateEpoch: 1_700_000_000
}

function makeBaseEnvPatchPlan(overrides = {}) {
  const value = {
    schema: 'bonfire.base-env-patch.v1', transactionToken: 'transaction-token',
    targetReleaseCommit: 'a'.repeat(40), rollbackReleaseCommit: 'b'.repeat(40), targetLedgerGeneration: 2,
    baseEnvPath: '/opt/meetingassist/deploy/digitalocean/.env',
    backupDir: '/opt/meetingassist-backups',
    backupPath: '', receiptPath: '',
    patchKey: 'PRIVATE_REALTIME_VOICE_QUALIFIED', priorQualificationState: 'false',
    beforeSha256: digest('1'), afterSha256: digest('2'),
    ...overrides
  }
  const stem = `base-env-${value.targetReleaseCommit}-${value.transactionToken}`
  if (!overrides.backupPath) value.backupPath = `${value.backupDir}/${stem}.before.env`
  if (!overrides.receiptPath) value.receiptPath = `${value.backupDir}/${stem}.receipt.json`
  return value
}

function makeBaseEnvPatchReceipt(plan = makeBaseEnvPatchPlan(), overrides = {}) {
  return {
    schema: 'bonfire.base-env-patch-receipt.v1', transactionToken: plan.transactionToken,
    targetReleaseCommit: plan.targetReleaseCommit, rollbackReleaseCommit: plan.rollbackReleaseCommit,
    targetLedgerGeneration: plan.targetLedgerGeneration, baseEnvPath: plan.baseEnvPath, backupPath: plan.backupPath,
    patchKey: plan.patchKey, priorQualificationState: plan.priorQualificationState,
    beforeSha256: plan.beforeSha256, afterSha256: plan.afterSha256,
    state: 'target_committed', targetObservedAt: '2026-08-22T12:00:00.000Z',
    committedAt: '2026-08-22T12:01:00.000Z', priorRestoredAt: null,
    ...overrides
  }
}

function sha256(value) { return createHash('sha256').update(value).digest('hex') }
function jsonLine(value) { return Buffer.from(`${JSON.stringify(value)}\n`) }
function configDigest(files) { return sha256(jsonLine({ schema: 'bonfire.release-config.v2', files })) }
async function makeBaseEnvFilesystemFixture(t, priorBody) {
  const ownerUid = typeof process.getuid === 'function' ? process.getuid() : 0
  const root = await mkdtemp(join(tmpdir(), 'bonfire-base-env-'))
  await chmod(root, 0o700)
  const configDir = join(root, 'config')
  const backupRoot = join(root, 'meetingassist-backups')
  await mkdir(configDir, { mode: 0o700 })
  await mkdir(backupRoot, { mode: 0o700 })
  const baseEnv = join(configDir, '.env')
  const prior = Buffer.from(priorBody)
  await writeFile(baseEnv, prior, { mode: 0o600 })
  await chmod(baseEnv, 0o600)
  const targetDir = join(root, 'target-release')
  const rollbackDir = join(root, 'rollback-release')
  const operationLock = await acquireReleaseOperationLock(targetDir, rollbackDir)
  t.after(async () => {
    await operationLock.release().catch(() => {})
    await rm(root, { recursive: true, force: true })
  })
  const plan = await prepareTargetBaseEnvPatch({
    baseEnv,
    request: {
      expectedBeforeSha256: sha256(prior),
      patchKey: 'PRIVATE_REALTIME_VOICE_QUALIFIED',
      patchValue: 'true',
      backupDir: backupRoot
    },
    operationLock,
    targetReleaseCommit: 'a'.repeat(40),
    rollbackReleaseCommit: 'b'.repeat(40),
    targetLedgerGeneration: 2,
    ownerUid,
    backupRoot
  })
  return { ownerUid, root, baseEnv, backupRoot, operationLock, plan, prior }
}
async function makeTreeWritable(path) {
  const info = await lstat(path).catch(() => null)
  if (!info || info.isSymbolicLink() || !info.isDirectory()) return
  await chmod(path, 0o700)
  for (const name of await readdir(path)) await makeTreeWritable(join(path, name))
}
const retained7ac93bContract = Object.freeze({
  toolSha256: '29b13c2597eb9ec34cd65851ad5c24b3281cdc8ea66df8ad572e4b14ae0dce3e',
  receiptSchema: 'bonfire.release-receipt.v3',
  releaseEnvironmentKeys: [
    'BONFIRE_BINARY_SHA256', 'BONFIRE_BUILD_CONFIG_SHA256', 'BONFIRE_BUILD_INPUT_MANIFEST_SHA256',
    'BONFIRE_BUILD_MANIFEST_SHA256', 'BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256', 'BONFIRE_BUILD_VERSION',
    'BONFIRE_CADDY_IMAGE', 'BONFIRE_COTURN_IMAGE', 'BONFIRE_GIT_TREE_DIGEST', 'BONFIRE_IMAGE_DIGEST',
    'BONFIRE_MEETINGASSIST_IMAGE', 'BONFIRE_POSTGRES_IMAGE', 'BONFIRE_RELEASE_BUNDLE_SHA256',
    'BONFIRE_RELEASE_COMMIT', 'BONFIRE_RELEASE_ENVIRONMENT_MARKER', 'BONFIRE_RELEASE_IDENTITY_REQUIRED',
    'BONFIRE_RENDER_IMAGE', 'BONFIRE_SOURCE_ARCHIVE_SHA256'
  ].sort(),
  meetingassistEnvironmentKeys: [
    'BONFIRE_BINARY_SHA256', 'BONFIRE_BUILD_CONFIG_SHA256', 'BONFIRE_BUILD_INPUT_MANIFEST_SHA256',
    'BONFIRE_BUILD_MANIFEST_SHA256', 'BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256', 'BONFIRE_BUILD_VERSION',
    'BONFIRE_CODEX_HEARTBEAT_PATH', 'BONFIRE_CODEX_QUEUE_PATH', 'BONFIRE_GIT_TREE_DIGEST',
    'BONFIRE_IMAGE_DIGEST', 'BONFIRE_RELEASE_BUNDLE_SHA256', 'BONFIRE_RELEASE_COMMIT',
    'BONFIRE_RELEASE_ENVIRONMENT_MARKER', 'BONFIRE_RELEASE_IDENTITY_REQUIRED', 'BONFIRE_RENDER_HEARTBEAT_PATH',
    'BONFIRE_RENDER_QUEUE_PATH', 'BONFIRE_SOURCE_ARCHIVE_SHA256'
  ].sort()
})

function verifyRetained7ac93bCompatibility(receipt, releaseEnvironmentBody, renderedConfig) {
  assert.equal(retained7ac93bContract.toolSha256, '29b13c2597eb9ec34cd65851ad5c24b3281cdc8ea66df8ad572e4b14ae0dce3e')
  assert.equal(receipt.schema, retained7ac93bContract.receiptSchema)
  assert.equal(receipt.strideE10W4, undefined)
  const releaseKeys = String(releaseEnvironmentBody).trim().split('\n').map(line => line.slice(0, line.indexOf('='))).sort()
  assert.deepEqual(releaseKeys, retained7ac93bContract.releaseEnvironmentKeys)
  assert.deepEqual(Object.keys(renderedConfig.services.meetingassist.environment).sort(), retained7ac93bContract.meetingassistEnvironmentKeys)
  assert.equal(Object.keys(receipt.buildManifest.buildInputs.dependencyInputs).includes('deploy/digitalocean/stride-e10-w4-deployment-policy.json'), false)
}
function builtImage(name, char) {
  return {
    imageReference: `${name}:release-${releaseCommit}`, imageId: `sha256:${digest(char)}`,
    imageDigest: digest(char), platform: 'linux/amd64', binarySha256: digest('e'),
    resolvedPackages: { build: ['go=1.26'], runtime: ['debian=12'] }
  }
}
function sidecarImage(name, char) {
  return { imageReference: sidecarRefs[name], imageId: `sha256:${digest(char)}`, imageDigest: digest(char), platform: 'linux/amd64' }
}
function commonBuildArgs(buildInputManifestSha256) {
  return {
    BONFIRE_GO_BUILD_IMAGE: buildInputs.goBuildImage,
    BONFIRE_RUNTIME_IMAGE: buildInputs.runtimeImage,
    BONFIRE_DEBIAN_SNAPSHOT: buildInputs.debianSnapshot,
    BONFIRE_RELEASE_COMMIT: source.releaseCommit,
    BONFIRE_GIT_TREE_DIGEST: source.gitTreeDigest,
    BONFIRE_BUILD_CONFIG_SHA256: source.buildConfigSha256,
    BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256: source.transitiveInputsSha256,
    BONFIRE_SOURCE_ARCHIVE_SHA256: source.sourceArchiveSha256,
    BONFIRE_BUILD_INPUT_MANIFEST_SHA256: buildInputManifestSha256,
    SOURCE_DATE_EPOCH: String(source.sourceDateEpoch)
  }
}

test('render-runner release heartbeat requires fresh exact print and raster evidence', () => {
  const now = Date.parse('2026-08-01T20:00:00.000Z')
  const valid = {
    ok: true, chromiumOK: true, pdftoppmOK: true, canaryOK: true,
    canaryCheckedAt: '2026-08-01T19:59:30.000Z', canaryPageCount: 1,
    canaryPDFBytes: 128, canaryErrorCode: '', time: '2026-08-01T19:59:59.000Z'
  }
  assert.equal(verifyRenderRunnerHeartbeat(JSON.stringify(valid), now).canaryPageCount, 1)
  for (const [name, mutate] of Object.entries({
    failed: value => { value.canaryOK = false },
    shallow: value => { value.canaryPDFBytes = 0 },
    codedFailure: value => { value.canaryErrorCode = 'chromium_print_failed' },
    stale: value => { value.canaryCheckedAt = '2026-08-01T19:57:59.000Z' },
    future: value => { value.time = '2026-08-01T20:00:06.000Z' }
  })) {
    const candidate = structuredClone(valid)
    mutate(candidate)
    assert.throws(() => verifyRenderRunnerHeartbeat(candidate, now), undefined, name)
  }
})

test('renderer sandbox profiles are the exact Docker v0.2.3 base plus five evidenced Chrome 150 allows', async () => {
  const seccompPath = join(repoRoot, 'deploy/digitalocean/bonfire-render-runner-v1.seccomp.json')
  const appArmorPath = join(repoRoot, 'deploy/digitalocean/bonfire-render-runner-v1.apparmor')
  const profile = JSON.parse(await readFile(seccompPath, 'utf8'))
  const additions = profile.syscalls.splice(-5)
  assert.equal(profile.syscalls.length, 33)
  assert.equal(sha256(JSON.stringify(profile)), 'afb4934b023cfceaaec1a9d752ca3f801aaa96eb2e59abe6e7ea16976948e080')
  assert.deepEqual(additions.map(rule => ({
    names: rule.names, action: rule.action, args: rule.args || [], arches: rule.includes?.arches
  })), [
    { names: ['clone'], action: 'SCMP_ACT_ALLOW', args: [{ index: 0, value: 268435473, op: 'SCMP_CMP_EQ' }], arches: ['amd64'] },
    { names: ['clone'], action: 'SCMP_ACT_ALLOW', args: [{ index: 0, value: 1879048209, op: 'SCMP_CMP_EQ' }], arches: ['amd64'] },
    { names: ['clone'], action: 'SCMP_ACT_ALLOW', args: [{ index: 0, value: 536870929, op: 'SCMP_CMP_EQ' }], arches: ['amd64'] },
    { names: ['unshare'], action: 'SCMP_ACT_ALLOW', args: [{ index: 0, value: 268435456, op: 'SCMP_CMP_EQ' }], arches: ['amd64'] },
    { names: ['chroot'], action: 'SCMP_ACT_ALLOW', args: [], arches: ['amd64'] }
  ])
  assert.equal(additions.some(rule => rule.names.includes('setns') || rule.names.includes('clone3')), false)
  const clone3 = profile.syscalls.find(rule => rule.names.includes('clone3') && rule.action === 'SCMP_ACT_ERRNO')
  assert.equal(clone3.errnoRet, 38)

  const appArmor = await readFile(appArmorPath, 'utf8')
  for (const required of ['profile "bonfire-render-runner-v1"', 'abi <abi/4.0>', 'userns,', 'deny mount', 'deny network alg']) {
    assert.ok(appArmor.includes(required), required)
  }
  for (const forbidden of ['flags=(unconfined)', 'default_allow', 'apparmor=unconfined']) assert.equal(appArmor.includes(forbidden), false)
})

test('running renderer confinement requires exact profiles, zero capabilities, NNP, seccomp, readonly root, and internal network', () => {
  const seccomp = { defaultAction: 'SCMP_ACT_ERRNO', syscalls: [{ names: ['read'], action: 'SCMP_ACT_ALLOW' }] }
  const inspect = {
    AppArmorProfile: 'bonfire-render-runner-v1',
    Config: { User: '65532:65532' },
    HostConfig: {
      SecurityOpt: ['apparmor=bonfire-render-runner-v1', 'no-new-privileges=true', `seccomp=${JSON.stringify(seccomp)}`],
      CapDrop: ['ALL'], CapAdd: null, Privileged: false, ReadonlyRootfs: true
    },
    NetworkSettings: { Networks: { digitalocean_render_internal: {} } }
  }
  const status = [
    'Uid:\t65532\t65532\t65532\t65532', 'Gid:\t65532\t65532\t65532\t65532',
    'CapInh:\t0000000000000000', 'CapPrm:\t0000000000000000', 'CapEff:\t0000000000000000',
    'CapBnd:\t0000000000000000', 'CapAmb:\t0000000000000000', 'NoNewPrivs:\t1', 'Seccomp:\t2'
  ].join('\n')
  assert.equal(validateRendererRuntimeConfinement(inspect, status, seccomp), inspect)
  for (const mutate of [
    value => { value.AppArmorProfile = 'docker-default' },
    value => { value.HostConfig.SecurityOpt[2] = 'seccomp=unconfined' },
    value => { value.HostConfig.CapAdd = ['SYS_ADMIN'] },
    value => { value.HostConfig.ReadonlyRootfs = false },
    value => { value.NetworkSettings.Networks.digitalocean_default = {} }
  ]) {
    const drift = structuredClone(inspect)
    mutate(drift)
    assert.throws(() => validateRendererRuntimeConfinement(drift, status, seccomp))
  }
  assert.throws(() => validateRendererRuntimeConfinement(inspect, status.replace('NoNewPrivs:\t1', 'NoNewPrivs:\t0'), seccomp))
  assert.throws(() => validateRendererRuntimeConfinement(inspect, status.replace('CapBnd:\t0000000000000000', 'CapBnd:\t0000000000200000'), seccomp))
  assert.throws(() => validateRendererRuntimeConfinement(inspect, status, { ...seccomp, defaultAction: 'SCMP_ACT_ALLOW' }))
})
const w4LivePolicy = {
  schema: 'bonfire.stride-e10-w4-deployment-policy.v1', releaseMode: 'bonfire_network_live', liveMode: 'bonfire_network_live',
  snapshotPath: '/app/data/stride-e10/w4/runtime-snapshot.json',
  activationBackupDir: '/app/data/stride-e10/w4/network-activation-backup',
  activationReceiptPath: '/app/data/stride-e10/w4/network-activation-receipt.json'
}
const w4CanaryPolicy = { ...w4LivePolicy, releaseMode: 'canary' }

function makeReceipt(w4Policy = null) {
  const sourceReceiptSha256 = digest('7')
  const buildInputManifestSha256 = digest('8')
  const common = commonBuildArgs(buildInputManifestSha256)
  const images = { meetingassist: builtImage('meetingassist', '9'), renderRunner: builtImage('meetingassist-render', 'f') }
  images.renderRunner.chromeHeadlessShellBinarySha256 = digest('1')
  const sidecars = {
    canonicalPostgres: sidecarImage('canonicalPostgres', '2'),
    coturn: sidecarImage('coturn', '3'),
    caddy: sidecarImage('caddy', '4')
  }
  const dependencyInputs = Object.fromEntries([
    'go.mod', 'go.sum', 'Dockerfile', 'Dockerfile.render', '.dockerignore',
    'deploy/digitalocean/release-build-inputs.json', 'deploy/digitalocean/release-scope-policy.json',
    ...(w4Policy ? ['deploy/digitalocean/stride-e10-w4-deployment-policy.json'] : [])
  ].map(path => [path, digest('d')]))
  const buildManifest = {
    schema: 'bonfire.release-build-manifest.v2', sourceReceiptSha256, source,
    archiveIdentity: {
      gitTreeDigest: source.gitTreeDigest, reviewedInventorySha256: source.reviewedInventorySha256,
      transitiveInputsSha256: source.transitiveInputsSha256, buildConfigSha256: source.buildConfigSha256,
      scopePolicySha256: source.scopePolicySha256, inputCount: source.inputCount
    },
    buildInputs: { sha256: buildInputManifestSha256, manifest: buildInputs, dependencyInputs },
    buildArgs: { meetingassist: common, renderRunner: { ...common,
      CHROME_HEADLESS_SHELL_VERSION: buildInputs.chromeHeadlessShellVersion,
      CHROME_HEADLESS_SHELL_SHA256: buildInputs.chromeHeadlessShellArchiveSha256 } },
    buildInvocations: {
      meetingassist: { platform: 'linux/amd64', dockerfile: 'Dockerfile', target: 'meetingassist-runtime', pull: false },
      renderRunner: { platform: 'linux/amd64', dockerfile: 'Dockerfile.render', target: 'render-runner', pull: false }
    },
    toolchain: { releaseToolNode: process.version, docker: 'fake-docker', dockerCompose: { version: 'v2.40.0' } },
    outputs: { images, sidecars }
  }
  const buildManifestSha256 = sha256(jsonLine(buildManifest))
  const candidateBundleManifestSha256 = digest('a')
  const environmentMarker = computeEnvironmentMarker({ ...source, buildInputManifestSha256, buildManifestSha256,
    binarySha256: images.meetingassist.binarySha256, imageDigest: images.meetingassist.imageDigest })
  const base = {
    schema: w4Policy ? 'bonfire.release-receipt.v4' : 'bonfire.release-receipt.v3', attestation: 'unsigned-local-v1', source, sourceReceiptSha256,
    buildInputManifestSha256, buildManifest, buildManifestSha256, candidateBundleManifestSha256,
    images, sidecars, environmentMarker
  }
  if (w4Policy) base.strideE10W4 = structuredClone(w4Policy)
  return { ...base, bundleSha256: computeBundleSha256(base) }
}

const topologyContext = {
  candidateRoot: '/opt/meetingassist-releases/target/sealed-candidate',
  candidateCaddyfile: '/opt/meetingassist-releases/target/sealed-candidate/deploy/digitalocean/Caddyfile',
  baseEnv: '/opt/meetingassist/deploy/digitalocean/.env',
  requireEnvFiles: true
}

function renderedComposeConfig(receipt = makeReceipt()) {
  const buildArgs = {
    BONFIRE_RELEASE_COMMIT: receipt.source.releaseCommit,
    BONFIRE_GIT_TREE_DIGEST: receipt.source.gitTreeDigest,
    BONFIRE_BUILD_CONFIG_SHA256: receipt.source.buildConfigSha256,
    BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256: receipt.source.transitiveInputsSha256,
    BONFIRE_SOURCE_ARCHIVE_SHA256: receipt.source.sourceArchiveSha256,
    BONFIRE_BUILD_INPUT_MANIFEST_SHA256: receipt.buildInputManifestSha256
  }
  const build = dockerfile => ({ context: topologyContext.candidateRoot, dockerfile, args: structuredClone(buildArgs) })
  const port = (target, published, protocol = 'tcp') => ({ mode: 'ingress', target, published, protocol })
  const volume = (source, target, readOnly = false) => ({ type: 'volume', source, target, read_only: readOnly, volume: {} })
  return {
    name: 'digitalocean',
    services: {
      meetingassist: {
        image: receipt.images.meetingassist.imageId,
        build: build('Dockerfile'),
        env_file: [topologyContext.baseEnv],
        environment: {
          ...environmentValues(receipt),
          BONFIRE_RELEASE_BUNDLE_SHA256: receipt.bundleSha256,
          BONFIRE_CODEX_QUEUE_PATH: '/app/codex-queue/jobs',
          BONFIRE_CODEX_HEARTBEAT_PATH: '/app/codex-queue/heartbeat.json',
          BONFIRE_RENDER_QUEUE_PATH: '/app/render-queue/jobs',
          BONFIRE_RENDER_HEARTBEAT_PATH: '/app/render-queue/heartbeat.json'
        },
        volumes: [
          volume('meeting_data', '/app/data'), volume('usage_ledger', '/app/data/usage'),
          volume('codex_queue', '/app/codex-queue'), volume('render_queue', '/app/render-queue')
        ],
        ports: [port('40000-40100', '40000-40100', 'udp')],
        healthcheck: { test: ['CMD', 'curl', '-fsS', 'http://127.0.0.1:3000/livez'], interval: '30s', timeout: '5s', retries: 3, start_period: '5m0s' },
        mem_limit: '3g', networks: { default: null, render_internal: null },
        depends_on: { 'canonical-postgres': { condition: 'service_healthy', required: true } }, restart: 'unless-stopped'
      },
      'canonical-postgres': {
        image: receipt.sidecars.canonicalPostgres.imageReference,
        environment: { POSTGRES_DB: 'bonfire', POSTGRES_USER: 'bonfire', POSTGRES_PASSWORD: 'redacted-fixture' },
        command: ['postgres', '-c', 'max_connections=30', '-c', 'shared_buffers=64MB', '-c', 'effective_cache_size=128MB',
          '-c', 'work_mem=2MB', '-c', 'maintenance_work_mem=32MB'],
        volumes: [volume('canonical_postgres', '/var/lib/postgresql/data')],
        healthcheck: { test: ['CMD-SHELL', 'pg_isready -U bonfire -d bonfire'], interval: '5s', timeout: '3s', retries: 20, start_period: '15s' },
        mem_limit: '256m', shm_size: '64m', networks: { default: null }, restart: 'unless-stopped'
      },
      'render-queue-init': {
        profiles: ['render'], image: receipt.images.renderRunner.imageId, build: build('Dockerfile.render'), user: '0:0',
        entrypoint: ['/bin/sh', '-eu', '-c'], command: ['install -d -o 65532 -g 65532 -m 0700 /app/render-queue /app/render-queue/jobs'],
        volumes: [volume('render_queue', '/app/render-queue')], network_mode: 'none', cap_drop: ['ALL'],
        cap_add: ['CHOWN', 'DAC_OVERRIDE'], read_only: true, restart: 'no'
      },
      'render-runner': {
        profiles: ['render'], image: receipt.images.renderRunner.imageId, build: build('Dockerfile.render'),
        environment: {
          BONFIRE_RUNNER_TOKEN: 'redacted-fixture', BONFIRE_RENDER_QUEUE_PATH: '/app/render-queue/jobs',
          BONFIRE_RENDER_HEARTBEAT_PATH: '/app/render-queue/heartbeat.json',
          BONFIRE_RENDER_CALLBACK_URL: 'http://meetingassist:3000/internal/render/jobs/result', BONFIRE_RENDER_TIMEOUT: '3m',
          BONFIRE_RENDER_MAX_HTML_BYTES: '8388608', BONFIRE_RENDER_MAX_PDF_BYTES: '67108864'
        },
        volumes: [volume('render_queue', '/app/render-queue')], cap_drop: ['ALL'],
        security_opt: ['apparmor=bonfire-render-runner-v1', 'no-new-privileges:true',
          'seccomp=/etc/docker/seccomp/bonfire-render-runner-v1.json'], read_only: true,
        tmpfs: ['/tmp:rw,nosuid,nodev,noexec,size=512m'], shm_size: '256m', pids_limit: 256, mem_limit: '1g',
        networks: { render_internal: null },
        depends_on: { meetingassist: { condition: 'service_healthy', required: true },
          'render-queue-init': { condition: 'service_completed_successfully', required: true } }, restart: 'unless-stopped'
      },
      coturn: {
        image: receipt.sidecars.coturn.imageReference,
        command: ['-n', '--log-file=stdout', '--fingerprint', '--use-auth-secret', '--static-auth-secret=redacted-fixture',
          '--realm=thebonfire.xyz', '--external-ip=146.190.171.224', '--listening-port=3478', '--min-port=49160',
          '--max-port=49200', '--no-cli', '--no-multicast-peers'],
        ports: [port(3478, '3478'), port(3478, '3478', 'udp'), port('49160-49200', '49160-49200', 'udp')],
        networks: { default: null }, restart: 'unless-stopped'
      },
      caddy: {
        image: receipt.sidecars.caddy.imageReference, env_file: [topologyContext.baseEnv],
        depends_on: { meetingassist: { condition: 'service_started', required: true } },
        ports: [port(80, '80'), port(443, '443')],
        volumes: [
          { type: 'bind', source: topologyContext.candidateCaddyfile, target: '/etc/caddy/Caddyfile', read_only: true, bind: { create_host_path: true } },
          volume('caddy_data', '/data'), volume('caddy_config', '/config')
        ],
        networks: { default: null }, restart: 'unless-stopped'
      }
    },
    networks: {
      default: { name: 'digitalocean_default' },
      render_internal: { name: 'digitalocean_render_internal', internal: true }
    },
    volumes: {
      caddy_data: { name: 'digitalocean_caddy_data' }, caddy_config: { name: 'digitalocean_caddy_config' },
      codex_queue: { name: 'digitalocean_codex_queue', external: true }, render_queue: { name: 'digitalocean_render_queue' },
      meeting_data: { name: 'digitalocean_meeting_data' }, usage_ledger: { name: 'digitalocean_usage_ledger', external: true },
      canonical_postgres: { name: 'digitalocean_canonical_postgres', external: true }
    }
  }
}

// Docker Compose v5.1.4 expands published ranges, renders byte sizes as
// decimal strings, preserves env_file metadata, and emits null defaults for
// image-provided commands/entrypoints. Keep this realistic renderer projection
// in the suite so the semantic gate is not validated only against short syntax.
function composeV514NormalizedConfig(receipt = makeReceipt()) {
  const config = renderedComposeConfig(receipt)
  for (const network of Object.values(config.networks)) network.ipam = {}
  for (const service of Object.values(config.services)) {
    if (service.command === undefined) service.command = null
    if (service.entrypoint === undefined) service.entrypoint = null
    if (service.env_file) service.env_file = service.env_file.map(path => ({ path }))
    if (service.mem_limit === '3g') service.mem_limit = '3221225472'
    if (service.mem_limit === '1g') service.mem_limit = '1073741824'
    if (service.mem_limit === '256m') service.mem_limit = '268435456'
    if (service.shm_size === '64m') service.shm_size = '67108864'
    if (service.shm_size === '256m') service.shm_size = '268435456'
    service.ports = (service.ports || []).flatMap(port => {
      const target = /^(\d+)(?:-(\d+))?$/.exec(String(port.target))
      const published = /^(\d+)(?:-(\d+))?$/.exec(String(port.published))
      assert.ok(target && published)
      const targetFirst = Number(target[1])
      const targetLast = Number(target[2] || target[1])
      const publishedFirst = Number(published[1])
      const publishedLast = Number(published[2] || published[1])
      assert.equal(targetLast - targetFirst, publishedLast - publishedFirst)
      return Array.from({ length: targetLast - targetFirst + 1 }, (_, index) => ({
        ...port, target: targetFirst + index, published: String(publishedFirst + index)
      }))
    })
    for (const mount of service.volumes || []) {
      if (mount.read_only === false) delete mount.read_only
      if (mount.type === 'bind') mount.bind = {}
    }
  }
  return config
}

async function fixtureFiles() {
  const policy = JSON.parse(await readFile(join(repoRoot, 'deploy/digitalocean/release-scope-policy.json'), 'utf8'))
  const files = {
    '.dockerignore': '.git\ndata/\n', Dockerfile: 'FROM scratch\n', 'Dockerfile.render': 'FROM scratch\n',
    'go.mod': 'module example.test/release\n\ngo 1.26\n', 'go.sum': '', 'main.go': 'package main\nfunc main() {}\n',
    'index.html': '<!doctype html>\n', 'packaging_deck_chassis.css': 'body{}\n',
    'packaging_studio_v4_definition.json': '{}\n',
    'internal/dr/authority.go': 'package dr\n', 'internal/e10evidence/types.go': 'package e10evidence\n',
    'deploy/digitalocean/docker-compose.yml': 'services: {}\n',
    'deploy/digitalocean/Caddyfile': ':80\n',
    'deploy/digitalocean/bonfire-render-runner-v1.apparmor': 'profile fixture {}\n',
    'deploy/digitalocean/bonfire-render-runner-v1.seccomp.json': '{"defaultAction":"SCMP_ACT_ERRNO"}\n',
    'deploy/digitalocean/release-build-inputs.json': `${JSON.stringify(buildInputs, null, 2)}\n`,
    'deploy/digitalocean/stride-e10-w4-deployment-policy.json': `${JSON.stringify(w4CanaryPolicy, null, 2)}\n`,
    'deploy/digitalocean/release-scope-policy.json': `${JSON.stringify(policy, null, 2)}\n`,
    'scripts/bonfire-release.mjs': await readFile(releaseToolPath, 'utf8'),
    'stride-site/secret.txt': 'not a release input\n', 'data/kanban-board.json': '{}\n',
    'docs/evidence/e10/provider.jsonl': '{"untrusted":true}\n', 'mobile/App.tsx': 'unrelated\n',
    'main_test.go': 'package main\n'
  }
  return files
}

const fakeDockerSource = `#!/usr/bin/env node
const fs = require('node:fs')
const args = process.argv.slice(2)
const statePath = process.env.FAKE_DOCKER_STATE
const sidecars = JSON.parse(process.env.FAKE_DOCKER_SIDECARS || '{}')
const state = fs.existsSync(statePath) ? JSON.parse(fs.readFileSync(statePath, 'utf8')) : { images: {}, containers: {} }
const save = () => fs.writeFileSync(statePath, JSON.stringify(state))
const print = value => process.stdout.write(typeof value === 'string' ? value : JSON.stringify(value))
function inspect(ref) {
  if (state.images[ref]) return state.images[ref]
  if (sidecars[ref]) {
    const image = { Id: sidecars[ref], Os: 'linux', Architecture: 'amd64', Config: { Labels: {} } }
    state.images[ref] = image
    state.images[image.Id] = image
    save()
    return image
  }
  throw new Error('unknown fake image ' + ref)
}
if (args[0] === 'build') {
  const tag = args[args.indexOf('--tag') + 1]
  const target = args[args.indexOf('--target') + 1]
  const values = {}
  for (let i = 0; i < args.length; i++) if (args[i] === '--build-arg') {
    const pair = args[++i]; const at = pair.indexOf('='); values[pair.slice(0, at)] = pair.slice(at + 1)
  }
  const id = target === 'render-runner' ? 'sha256:' + 'f'.repeat(64) : 'sha256:' + '9'.repeat(64)
  const labels = {
    'org.opencontainers.image.revision': values.BONFIRE_RELEASE_COMMIT,
    'xyz.thebonfire.git-tree-digest': values.BONFIRE_GIT_TREE_DIGEST,
    'xyz.thebonfire.config-digest': values.BONFIRE_BUILD_CONFIG_SHA256,
    'xyz.thebonfire.transitive-inputs-digest': values.BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256,
    'xyz.thebonfire.source-archive-digest': values.BONFIRE_SOURCE_ARCHIVE_SHA256,
    'xyz.thebonfire.build-input-manifest-digest': values.BONFIRE_BUILD_INPUT_MANIFEST_SHA256,
    'xyz.thebonfire.attestation': 'unsigned-external-verification-required'
  }
  const image = { Id: id, Os: 'linux', Architecture: 'amd64', Config: { Labels: labels } }
  state.images[tag] = image; state.images[id] = image; save()
} else if (args[0] === 'image' && args[1] === 'inspect') {
  print([inspect(args[2])])
} else if (args[0] === 'create') {
  const image = inspect(args[1]); const container = 'container-' + image.Id.slice(-12)
  state.containers[container] = image.Id; save(); print(container + '\\n')
} else if (args[0] === 'cp') {
  const [containerPath, destination] = args.slice(1)
  const source = containerPath.slice(containerPath.indexOf(':') + 1)
  if (source === '/app/meetingassist') fs.writeFileSync(destination, 'same-release-binary\\n')
  else if (source === '/app/release-build-packages.txt') fs.writeFileSync(destination, 'go=1.26\\n')
  else if (source === '/app/release-runtime-packages.txt') fs.writeFileSync(destination, 'debian=12\\n')
  else if (source === '/opt/chrome-headless-shell/chrome-headless-shell') fs.writeFileSync(destination, 'chrome-headless-shell\\n')
  else throw new Error('unexpected fake docker cp ' + source)
} else if (args[0] === 'rm') {
  process.exit(0)
} else if (args[0] === 'version') {
  print(JSON.stringify({ Client: { Version: 'fake' }, Server: { Version: 'fake' } }))
} else if (args[0] === 'compose' && args[1] === 'version') {
  print(JSON.stringify({ version: 'v2.40.0' }))
} else {
  throw new Error('unexpected fake docker call: ' + args.join(' '))
}
`

test('scope policy is allowlisted, excludes product/evidence/data trees, and never stages', async () => {
  const policy = validateReleaseScopePolicy(JSON.parse(await readFile(join(repoRoot, 'deploy/digitalocean/release-scope-policy.json'), 'utf8')))
  assert.equal(releasePathOwned('main.go', policy), true)
  assert.equal(releasePathOwned('main_test.go', policy), false)
  assert.equal(releasePathOwned('internal/dr/envelope.go', policy), true)
  assert.equal(releasePathOwned('internal/e10evidence/receipt.go', policy), true)
  assert.equal(releasePathOwned('internal/e10evidence/receipt_test.go', policy), false)
  for (const path of ['stride-site/app/page.tsx', 'data/kanban-board.json', 'docs/evidence/e10/provider.jsonl', 'mobile/App.tsx', 'scripts/random.mjs']) {
    assert.equal(releasePathOwned(path, policy), false, path)
  }
  const releaseTool = await readFile(releaseToolPath, 'utf8')
  assert.doesNotMatch(releaseTool, /git[^\n]*\badd\b/)
})

test('scope owns every internal package imported by production root files', async () => {
  const policy = validateReleaseScopePolicy(JSON.parse(await readFile(join(repoRoot, 'deploy/digitalocean/release-scope-policy.json'), 'utf8')))
  const rootFiles = (await readdir(repoRoot)).filter(name => name.endsWith('.go') && !name.endsWith('_test.go'))
  const packagePaths = new Set()
  const importPattern = /"github\.com\/openai\/openai-realtime-meeting-assistant\/(internal\/[^"/]+)"/g
  for (const name of rootFiles) {
    const body = await readFile(join(repoRoot, name), 'utf8')
    for (const match of body.matchAll(importPattern)) packagePaths.add(match[1])
  }
  assert.ok(packagePaths.size > 0)
  for (const packagePath of packagePaths) {
    const sources = (await readdir(join(repoRoot, packagePath))).filter(name => name.endsWith('.go') && !name.endsWith('_test.go'))
    assert.ok(sources.length > 0, `${packagePath} has no production Go source`)
    for (const name of sources) {
      const path = `${packagePath}/${name}`
      assert.equal(releasePathOwned(path, policy), true, `release scope omits runtime source ${path}`)
    }
  }
})

test('scope owns every asset embedded by production Go', async () => {
  const policy = validateReleaseScopePolicy(JSON.parse(await readFile(join(repoRoot, 'deploy/digitalocean/release-scope-policy.json'), 'utf8')))
  const { stdout } = await execFileAsync('git', ['ls-files', '*.go'], { cwd: repoRoot })
  const productionSources = stdout.trim().split('\n').filter(path => path && releasePathOwned(path, policy))
  assert.ok(productionSources.length > 0)
  for (const sourcePath of productionSources) {
    const body = await readFile(join(repoRoot, sourcePath), 'utf8')
    for (const match of body.matchAll(/^\s*\/\/go:embed\s+(.+)$/gm)) {
      const patterns = match[1].match(/`[^`]+`|"(?:\\.|[^"\\])*"|\S+/g) || []
      assert.ok(patterns.length > 0, `${sourcePath} has an empty go:embed directive`)
      for (const encodedPattern of patterns) {
        const pattern = encodedPattern.startsWith('`')
          ? encodedPattern.slice(1, -1)
          : encodedPattern.startsWith('"')
            ? JSON.parse(encodedPattern)
            : encodedPattern
        const sourceDirectory = sourcePath.includes('/') ? sourcePath.slice(0, sourcePath.lastIndexOf('/')) : ''
        const repoPattern = sourceDirectory ? `${sourceDirectory}/${pattern}` : pattern
        const { stdout: matchedRaw } = await execFileAsync('git', ['ls-files', repoPattern], { cwd: repoRoot })
        const matchedPaths = matchedRaw.trim().split('\n').filter(Boolean)
        assert.ok(matchedPaths.length > 0, `${sourcePath} embeds missing release asset ${repoPattern}`)
        for (const embeddedPath of matchedPaths) {
          assert.equal(releasePathOwned(embeddedPath, policy), true, `release scope omits embedded runtime asset ${embeddedPath}`)
        }
      }
    }
  }
})

test('reviewed inventory rejects nested gitlinks and has an exact stable digest', async () => {
  const policy = validateReleaseScopePolicy(JSON.parse(await readFile(join(repoRoot, 'deploy/digitalocean/release-scope-policy.json'), 'utf8')))
  const entries = policy.requiredPaths.map((path, index) => ({ path, mode: '100644', type: 'blob', object: `${index.toString(16).padStart(40, '0')}` }))
  assert.equal(validateReviewedInventory(entries, policy), entries)
  assert.equal(reviewedInventoryDigest(entries), reviewedInventoryDigest(entries))
  const linked = entries.map(entry => entry.path === 'internal/dr/authority.go' ? { ...entry, mode: '160000', type: 'commit' } : entry)
  assert.throws(() => validateReviewedInventory(linked, policy), /gitlink/)
})

test('exact reviewed SHA and two clean observations remain mandatory', () => {
  assert.doesNotThrow(() => validatePrepareState({ dirtyBefore: '', dirtyAfter: '', head: releaseCommit, reviewedRef: releaseCommit, reviewedCommit: releaseCommit }))
  assert.throws(() => validatePrepareState({ dirtyBefore: ' M main.go', dirtyAfter: '', head: releaseCommit, reviewedRef: releaseCommit, reviewedCommit: releaseCommit }), /not clean/)
  assert.throws(() => validatePrepareState({ dirtyBefore: '', dirtyAfter: '?? late.txt', head: releaseCommit, reviewedRef: releaseCommit, reviewedCommit: releaseCommit }), /not clean/)
  assert.throws(() => validatePrepareState({ dirtyBefore: '', dirtyAfter: '', head: releaseCommit, reviewedRef: 'axx\/main', reviewedCommit: releaseCommit }), /exact full commit/)
})

test('scope and prepare end to end emit only exact owned commit inventory', async () => {
  const root = await mkdtemp(join(tmpdir(), 'bonfire-release-e2e-'))
  const repo = join(root, 'repo')
  const output = join(root, 'output')
  try {
    await mkdir(repo, { recursive: true })
    await mkdir(output)
    for (const [path, body] of Object.entries(await fixtureFiles())) {
      await mkdir(join(repo, path, '..'), { recursive: true })
      await writeFile(join(repo, path), body)
    }
    await execFileAsync('git', ['init', '-q'], { cwd: repo })
    await execFileAsync('git', ['config', 'user.email', 'release-test@example.test'], { cwd: repo })
    await execFileAsync('git', ['config', 'user.name', 'Release Test'], { cwd: repo })
    await execFileAsync('git', ['add', '.'], { cwd: repo })
    await execFileAsync('git', ['commit', '-q', '-m', 'fixture'], { cwd: repo })
    const { stdout: commitRaw } = await execFileAsync('git', ['rev-parse', 'HEAD'], { cwd: repo })
    const commit = commitRaw.trim()
    const { stdout: scopeRaw } = await execFileAsync(process.execPath, [releaseToolPath, 'scope', '--reviewed-ref', commit], { cwd: repo })
    const scoped = JSON.parse(scopeRaw)
    assert.equal(scoped.releaseCommit, commit)
    assert.ok(scoped.paths.includes('main.go'))
    assert.equal(scoped.paths.some(path => path.startsWith('stride-site/') || path.startsWith('data/') || path.startsWith('docs/evidence/') || path === 'main_test.go'), false)

    await execFileAsync(process.execPath, [releaseToolPath, 'prepare', '--reviewed-ref', commit,
      '--archive', join(output, 'source.tar'), '--source-receipt', join(output, 'source-receipt.json')], { cwd: repo })
    const receipt = validateSourceReceipt(JSON.parse(await readFile(join(output, 'source-receipt.json'), 'utf8')))
    assert.equal(receipt.reviewedInventorySha256, scoped.inventorySha256)
    assert.equal(receipt.inputCount, scoped.inputCount)
    const extracted = join(output, 'source')
    await mkdir(extracted)
    await execFileAsync('tar', ['-xf', join(output, 'source.tar'), '-C', extracted])
    const identity = await inspectExtractedArchive(extracted)
    assert.doesNotThrow(() => verifyArchiveIdentity(identity, receipt))
    assert.equal(await readFile(join(extracted, 'main.go'), 'utf8'), 'package main\nfunc main() {}\n')
    await assert.rejects(readFile(join(extracted, 'stride-site/secret.txt')), /ENOENT/)

    await writeFile(join(repo, 'main.go'), 'package main\nfunc main(){panic("dirty")}\n')
    await assert.rejects(execFileAsync(process.execPath, [releaseToolPath, 'prepare', '--reviewed-ref', commit,
      '--archive', join(output, 'dirty.tar'), '--source-receipt', join(output, 'dirty.json')], { cwd: repo }), /not clean/)
  } finally {
    await makeTreeWritable(root)
    await rm(root, { recursive: true, force: true })
  }
})

test('build end to end uses a hermetic Docker fake for both owned images and pinned sidecars', async () => {
  const root = await mkdtemp(join(tmpdir(), 'bonfire-release-build-e2e-'))
  const repo = join(root, 'repo')
  const releaseDir = join(root, 'release')
  const fakeBin = join(root, 'bin')
  try {
    await mkdir(repo, { recursive: true })
    await mkdir(releaseDir)
    await mkdir(fakeBin)
    for (const [path, body] of Object.entries(await fixtureFiles())) {
      await mkdir(join(repo, path, '..'), { recursive: true })
      await writeFile(join(repo, path), body)
    }
    await execFileAsync('git', ['init', '-q'], { cwd: repo })
    await execFileAsync('git', ['config', 'user.email', 'release-test@example.test'], { cwd: repo })
    await execFileAsync('git', ['config', 'user.name', 'Release Test'], { cwd: repo })
    await execFileAsync('git', ['add', '.'], { cwd: repo })
    await execFileAsync('git', ['commit', '-q', '-m', 'fixture'], { cwd: repo })
    const { stdout: commitRaw } = await execFileAsync('git', ['rev-parse', 'HEAD'], { cwd: repo })
    const commit = commitRaw.trim()
    await execFileAsync(process.execPath, [releaseToolPath, 'prepare', '--reviewed-ref', commit,
      '--archive', join(releaseDir, 'source.tar'), '--source-receipt', join(releaseDir, 'source-receipt.json')], { cwd: repo })

    const fakeDocker = join(fakeBin, 'docker')
    await writeFile(fakeDocker, fakeDockerSource)
    await chmod(fakeDocker, 0o755)
    const fakeSidecars = Object.fromEntries([
      [sidecarRefs.canonicalPostgres, `sha256:${digest('5')}`],
      [sidecarRefs.coturn, `sha256:${digest('6')}`],
      [sidecarRefs.caddy, `sha256:${digest('7')}`]
    ])
    const env = { ...process.env, PATH: `${fakeBin}:${process.env.PATH}`, FAKE_DOCKER_STATE: join(root, 'docker-state.json'),
      FAKE_DOCKER_SIDECARS: JSON.stringify(fakeSidecars) }
    await execFileAsync(process.execPath, [releaseToolPath, 'build',
      '--archive', join(releaseDir, 'source.tar'), '--source-receipt', join(releaseDir, 'source-receipt.json'),
      '--image', `meetingassist:release-${commit}`, '--render-image', `meetingassist-render:release-${commit}`,
      '--build-manifest', join(releaseDir, 'build-manifest.json'), '--release-receipt', join(releaseDir, 'release-receipt.json'),
      '--runtime-env', join(releaseDir, 'release.env')], { cwd: repo, env })
    const receipt = validateReleaseReceipt(JSON.parse(await readFile(join(releaseDir, 'release-receipt.json'), 'utf8')))
    assert.equal(receipt.schema, 'bonfire.release-receipt.v3')
    assert.equal(receipt.strideE10W4, undefined)
    assert.equal(receipt.images.meetingassist.imageId, `sha256:${digest('9')}`)
    assert.equal(receipt.images.renderRunner.imageId, `sha256:${digest('f')}`)
    assert.equal(receipt.images.meetingassist.binarySha256, receipt.images.renderRunner.binarySha256)
    assert.equal(receipt.sidecars.caddy.imageReference, sidecarRefs.caddy)
    const releaseEnvironmentBody = await readFile(join(releaseDir, 'release.env'), 'utf8')
    assert.match(releaseEnvironmentBody, new RegExp(`BONFIRE_RENDER_IMAGE=sha256:${digest('f')}`))
    assert.doesNotMatch(releaseEnvironmentBody, /STRIDE_E10_W4_/)
    assert.equal(await readFile(join(releaseDir, 'sealed-candidate/deploy/digitalocean/Caddyfile'), 'utf8'), ':80\n')
    verifyRetained7ac93bCompatibility(receipt, releaseEnvironmentBody, renderedComposeConfig(receipt))

    const liveReleaseDir = join(root, 'release-live')
    await mkdir(liveReleaseDir)
    await writeFile(join(repo, 'deploy/digitalocean/stride-e10-w4-deployment-policy.json'), `${JSON.stringify(w4LivePolicy, null, 2)}\n`)
    await writeFile(join(repo, 'deploy/digitalocean/docker-compose.yml'), `services: {}\n# ${[
      'STRIDE_E10_W4_MODE: ${STRIDE_E10_W4_RELEASE_MODE:?STRIDE_E10_W4_RELEASE_MODE is required}',
      'STRIDE_E10_W4_SNAPSHOT_PATH: ${STRIDE_E10_W4_SNAPSHOT_PATH:?STRIDE_E10_W4_SNAPSHOT_PATH is required}',
      'STRIDE_E10_W4_ACTIVATION_BACKUP_DIR: ${STRIDE_E10_W4_ACTIVATION_BACKUP_DIR:?STRIDE_E10_W4_ACTIVATION_BACKUP_DIR is required}',
      'STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH: ${STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH:?STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH is required}'
    ].join('\n# ')}\n`)
    await execFileAsync('git', ['add', 'deploy/digitalocean/docker-compose.yml', 'deploy/digitalocean/stride-e10-w4-deployment-policy.json'], { cwd: repo })
    await execFileAsync('git', ['commit', '-q', '-m', 'live policy'], { cwd: repo })
    const { stdout: liveCommitRaw } = await execFileAsync('git', ['rev-parse', 'HEAD'], { cwd: repo })
    const liveCommit = liveCommitRaw.trim()
    await execFileAsync(process.execPath, [releaseToolPath, 'prepare', '--reviewed-ref', liveCommit,
      '--archive', join(liveReleaseDir, 'source.tar'), '--source-receipt', join(liveReleaseDir, 'source-receipt.json')], { cwd: repo })
    await execFileAsync(process.execPath, [releaseToolPath, 'build',
      '--archive', join(liveReleaseDir, 'source.tar'), '--source-receipt', join(liveReleaseDir, 'source-receipt.json'),
      '--image', `meetingassist:release-${liveCommit}`, '--render-image', `meetingassist-render:release-${liveCommit}`,
      '--build-manifest', join(liveReleaseDir, 'build-manifest.json'), '--release-receipt', join(liveReleaseDir, 'release-receipt.json'),
      '--runtime-env', join(liveReleaseDir, 'release.env')], { cwd: repo, env })
    const liveReceipt = validateReleaseReceipt(JSON.parse(await readFile(join(liveReleaseDir, 'release-receipt.json'), 'utf8')))
    const liveEnvironmentBody = await readFile(join(liveReleaseDir, 'release.env'), 'utf8')
    assert.equal(liveReceipt.schema, 'bonfire.release-receipt.v4')
    assert.deepEqual(liveReceipt.strideE10W4, w4LivePolicy)
    assert.doesNotThrow(() => verifyReleaseEnvironmentFile(liveEnvironmentBody, liveReceipt))
    assert.match(liveEnvironmentBody, /STRIDE_E10_W4_RELEASE_MODE=bonfire_network_live/)
    assert.match(liveEnvironmentBody, /STRIDE_E10_W4_SNAPSHOT_PATH=\/app\/data\/stride-e10\/w4\/runtime-snapshot\.json/)
  } finally {
    await makeTreeWritable(root)
    await rm(root, { recursive: true, force: true })
  }
})

test('source and pinned whole-deployment inputs reject ambiguous identities', () => {
  assert.equal(validateSourceReceipt(source), source)
  assert.equal(validateBuildInputs(buildInputs), buildInputs)
  assert.throws(() => validateSourceReceipt({ ...source, reviewedRef: 'axx/main' }), /exact reviewed commit/)
  assert.throws(() => validateSourceReceipt({ ...source, configFiles: { ...source.configFiles, Dockerfile: digest('f') } }), /config binding/)
  assert.throws(() => validateBuildInputs({ ...buildInputs, goBuildImage: 'golang:1.26-bookworm' }), /identity/)
  assert.throws(() => validateBuildInputs({ ...buildInputs, sidecarImages: { ...sidecarRefs, caddy: 'caddy:2' } }), /sidecar/)
  assert.throws(() => validateBuildInputs({ ...buildInputs, rendererSandbox: {
    ...buildInputs.rendererSandbox, seccompAllowDeltaCount: 6
  } }), /renderer sandbox/)
})

test('repository build and Compose wiring consume pinned app, render, and sidecar inputs', async () => {
  const manifest = validateBuildInputs(JSON.parse(await readFile(join(repoRoot, 'deploy/digitalocean/release-build-inputs.json'), 'utf8')))
  const [dockerfile, renderDockerfile, compose, releaseTool] = await Promise.all([
    readFile(join(repoRoot, 'Dockerfile'), 'utf8'), readFile(join(repoRoot, 'Dockerfile.render'), 'utf8'),
    readFile(join(repoRoot, 'deploy/digitalocean/docker-compose.yml'), 'utf8'), readFile(releaseToolPath, 'utf8')
  ])
  for (const body of [dockerfile, renderDockerfile]) {
    assert.ok(body.includes(manifest.goBuildImage))
    assert.ok(body.includes(manifest.runtimeImage))
    assert.ok(body.includes(manifest.debianSnapshot))
    assert.ok(body.includes('xyz.thebonfire.config-digest'))
  }
  assert.ok(renderDockerfile.includes(manifest.chromeHeadlessShellVersion))
  assert.ok(renderDockerfile.includes(manifest.chromeHeadlessShellArchiveSha256))
  for (const packageName of manifest.renderRuntimePackages) assert.ok(renderDockerfile.includes(packageName), packageName)
  assert.match(renderDockerfile, /HEALTHCHECK[\s\S]*BONFIRE_RENDER_HEARTBEAT_PATH[\s\S]*chromiumOK[\s\S]*pdftoppmOK[\s\S]*canaryOK[\s\S]*canaryPageCount/)
  assert.match(releaseTool, /docker[\s\S]*exec[\s\S]*render-runner[\s\S]*heartbeat\.json[\s\S]*verifyRenderRunnerHeartbeat/)
  assert.match(compose, /render-queue-init:[\s\S]*image: \$\{BONFIRE_RENDER_IMAGE/)
  assert.match(compose, /render-runner:[\s\S]*image: \$\{BONFIRE_RENDER_IMAGE/)
  assert.ok(compose.includes(manifest.sidecarImages.canonicalPostgres))
  assert.ok(compose.includes(manifest.sidecarImages.coturn))
  assert.ok(compose.includes(manifest.sidecarImages.caddy))
  assert.match(compose, /\$\{BONFIRE_BASE_ENV_FILE:-\.env\}/)
  assert.doesNotMatch(compose, /image:\s+(?:postgres:|coturn\/coturn:|caddy:2)/)
  assert.doesNotMatch(releaseTool, /['"]--pull['"]|git[^\n]*\badd\b/)
})

test('extracted archive detects source, mode, and candidate config drift', async () => {
  const root = await mkdtemp(join(tmpdir(), 'bonfire-release-test-'))
  try {
    for (const [path, body] of Object.entries(await fixtureFiles())) {
      if (path.startsWith('stride-site/') || path.startsWith('data/') || path.startsWith('docs/evidence/') || path.startsWith('mobile/') || path.endsWith('_test.go')) continue
      await mkdir(join(root, path, '..'), { recursive: true })
      await writeFile(join(root, path), body)
    }
    const first = await inspectExtractedArchive(root)
    const bound = { ...source, gitTreeDigest: first.gitTreeDigest, transitiveInputsSha256: first.transitiveInputsSha256,
      buildConfigSha256: first.buildConfigSha256, scopePolicySha256: first.scopePolicySha256,
      configFiles: first.configFiles, inputCount: first.inputCount }
    assert.doesNotThrow(() => verifyArchiveIdentity(first, bound))
    await writeFile(join(root, 'go.mod'), 'changed\n')
    const contentChanged = await inspectExtractedArchive(root)
    assert.throws(() => verifyArchiveIdentity(contentChanged, bound), /gitTreeDigest/)
    await writeFile(join(root, 'go.mod'), 'module example.test\/release\n\ngo 1.26\n')
    await chmod(join(root, 'Dockerfile'), 0o755)
    const modeChanged = await inspectExtractedArchive(root)
    assert.throws(() => verifyArchiveIdentity(modeChanged, bound), /gitTreeDigest/)
    await writeFile(join(root, 'mobile.txt'), 'unowned\n')
    await assert.rejects(inspectExtractedArchive(root), /unowned release path/)
  } finally {
    await makeTreeWritable(root)
    await rm(root, { recursive: true, force: true })
  }
})

test('whole-deployment receipt binds both owned images, sidecars, and candidate bundle', () => {
  const receipt = makeReceipt()
  assert.equal(validateReleaseReceipt(receipt), receipt)
  assert.throws(() => validateReleaseReceipt({ ...receipt, images: { ...receipt.images,
    renderRunner: { ...receipt.images.renderRunner, binarySha256: digest('d') } } }), /build\/output/)
  assert.throws(() => validateReleaseReceipt({ ...receipt, sidecars: { ...receipt.sidecars,
    caddy: { ...receipt.sidecars.caddy, imageReference: 'caddy:2' } } }), /build\/output/)
  assert.throws(() => validateReleaseReceipt({ ...receipt, candidateBundleManifestSha256: digest('f') }), /bundle binding/)
  const missingComposeVersion = structuredClone(receipt)
  delete missingComposeVersion.buildManifest.toolchain.dockerCompose
  missingComposeVersion.buildManifestSha256 = sha256(jsonLine(missingComposeVersion.buildManifest))
  assert.throws(() => validateReleaseReceipt(missingComposeVersion), /Docker Compose toolchain identity/)
})

test('candidate bundle config is hash-checked before activation', async () => {
  const root = await mkdtemp(join(tmpdir(), 'bonfire-candidate-test-'))
  try {
    const files = { 'deploy/digitalocean/docker-compose.yml': 'services: {}\n', 'deploy/digitalocean/Caddyfile': ':80\n' }
    for (const [path, body] of Object.entries(files)) {
      await mkdir(join(root, path, '..'), { recursive: true })
      await writeFile(join(root, path), body)
      await chmod(join(root, path), 0o400)
    }
    await chmod(join(root, 'deploy/digitalocean'), 0o500)
    await chmod(join(root, 'deploy'), 0o500)
    await chmod(root, 0o500)
    const sourceFixture = { ...source, buildConfigSha256: digest('a'), configFiles: Object.fromEntries(Object.entries(files).map(([path, body]) => [path, sha256(body)])) }
    const manifest = validateCandidateBundleManifest({ schema: 'bonfire.candidate-deployment-bundle.v1', releaseCommit,
      buildConfigSha256: sourceFixture.buildConfigSha256, configFiles: sourceFixture.configFiles }, sourceFixture)
    await verifyCandidateConfig(root, manifest)
    await chmod(join(root, 'deploy/digitalocean/Caddyfile'), 0o600)
    await assert.rejects(verifyCandidateConfig(root, manifest), /private read-only regular file/)
    await writeFile(join(root, 'deploy/digitalocean/Caddyfile'), ':443\n')
    await assert.rejects(verifyCandidateConfig(root, manifest), /Caddyfile/)
  } finally {
    await makeTreeWritable(root)
    await rm(root, { recursive: true, force: true })
  }
})

test('candidate bundle rejects symlink leaves and ancestors', async () => {
  const root = await mkdtemp(join(tmpdir(), 'bonfire-candidate-link-test-'))
  const outside = await mkdtemp(join(tmpdir(), 'bonfire-candidate-outside-'))
  try {
    const path = 'deploy/digitalocean/Caddyfile'
    const body = ':80\n'
    const sourceFixture = { ...source, buildConfigSha256: digest('a'), configFiles: { [path]: sha256(body) } }
    const manifest = validateCandidateBundleManifest({ schema: 'bonfire.candidate-deployment-bundle.v1', releaseCommit,
      buildConfigSha256: sourceFixture.buildConfigSha256, configFiles: sourceFixture.configFiles }, sourceFixture)
    await mkdir(join(root, 'deploy/digitalocean'), { recursive: true })
    await writeFile(join(outside, 'Caddyfile'), body)
    await symlink(join(outside, 'Caddyfile'), join(root, path))
    await chmod(join(root, 'deploy/digitalocean'), 0o500)
    await chmod(join(root, 'deploy'), 0o500)
    await chmod(root, 0o500)
    await assert.rejects(verifyCandidateConfig(root, manifest), /symlink/)

    const ancestorRoot = await mkdtemp(join(tmpdir(), 'bonfire-candidate-parent-link-test-'))
    try {
      await mkdir(join(outside, 'digitalocean'), { recursive: true })
      await writeFile(join(outside, 'digitalocean/Caddyfile'), body)
      await chmod(join(outside, 'digitalocean/Caddyfile'), 0o400)
      await symlink(outside, join(ancestorRoot, 'deploy'))
      await chmod(ancestorRoot, 0o500)
      await assert.rejects(verifyCandidateConfig(ancestorRoot, manifest), /symlink/)
    } finally {
      await chmod(ancestorRoot, 0o700).catch(() => {})
      await rm(ancestorRoot, { recursive: true, force: true })
    }
  } finally {
    await makeTreeWritable(root)
    await rm(root, { recursive: true, force: true })
    await rm(outside, { recursive: true, force: true })
  }
})

test('executing tool, project inventory, and active ledger are exact fail-closed contracts', async () => {
  const root = await mkdtemp(join(tmpdir(), 'bonfire-release-tool-test-'))
  try {
    const tool = join(root, 'bonfire-release.mjs')
    await writeFile(tool, 'trusted tool\n')
    await verifyExecutingReleaseTool(sha256('trusted tool\n'), tool)
    await writeFile(tool, 'mutated tool\n')
    await assert.rejects(verifyExecutingReleaseTool(sha256('trusted tool\n'), tool), /differs/)

    const entries = ['meetingassist', 'render-runner', 'render-queue-init', 'canonical-postgres', 'coturn', 'caddy']
      .map(service => ({ id: `container-${service}`, service }))
    assert.equal(Object.keys(validateProjectServiceInventory(entries)).length, 6)
    assert.throws(() => validateProjectServiceInventory([...entries, { id: 'orphan', service: 'codex-runner' }]), /unexpected or orphan/)
    assert.throws(() => validateProjectServiceInventory(entries.slice(1)), /not exact/)
    assert.throws(() => validateProjectServiceInventory([...entries, { id: 'duplicate', service: 'meetingassist' }]), /duplicate/)

    const entry = (suffix, char) => ({ releaseDir: `/opt/meetingassist-releases/${suffix}`, releaseCommit,
      bundleSha256: digest(char), meetingassistImageId: `sha256:${digest(char)}`, renderRunnerImageId: `sha256:${digest(char === 'a' ? 'b' : 'a')}` })
    const ledger = { schema: 'bonfire.active-release-ledger.v1', generation: 1, updatedAt: new Date().toISOString(),
      active: entry('active', 'a'), previous: entry('previous', 'b') }
    assert.equal(validateActiveReleaseLedger(ledger), ledger)
    assert.throws(() => validateActiveReleaseLedger({ ...ledger, active: { ...ledger.active, releaseDir: 'relative' } }), /active.*invalid/)
  } finally {
    await rm(root, { recursive: true, force: true })
  }
})

test('rendered candidate Compose is an exact singleton topology before mutation', () => {
  const receipt = makeReceipt()
  const exact = renderedComposeConfig(receipt)
  const validate = config => validateRenderedComposeConfig(config, receipt, topologyContext)
  assert.equal(validate(exact), exact)
  const composeV5 = composeV514NormalizedConfig(receipt)
  assert.equal(validate(composeV5), composeV5)
  assert.equal(renderedComposeSha256(exact), renderedComposeSha256(structuredClone(exact)))

  const reordered = { volumes: exact.volumes, networks: exact.networks,
    services: Object.fromEntries(Object.entries(exact.services).reverse()), name: exact.name }
  assert.equal(renderedComposeSha256(exact), renderedComposeSha256(reordered))

  const added = structuredClone(exact)
  added.services['codex-runner'] = { image: receipt.images.meetingassist.imageId }
  assert.throws(() => validate(added), /service inventory is not exact/)

  const removed = structuredClone(exact)
  delete removed.services.coturn
  assert.throws(() => validate(removed), /service inventory is not exact/)

  const scaled = structuredClone(exact)
  scaled.services.meetingassist.scale = 2
  assert.throws(() => validate(scaled), /scale is not exactly one/)

  const replicated = structuredClone(exact)
  replicated.services['render-runner'].deploy = { replicas: 2 }
  assert.throws(() => validate(replicated), /replica count is not exactly one/)

  const global = structuredClone(exact)
  global.services.caddy.deploy = { mode: 'global' }
  assert.throws(() => validate(global), /mode is not singleton-compatible/)

  const driftedImage = structuredClone(exact)
  driftedImage.services.meetingassist.image = `sha256:${digest('0')}`
  assert.throws(() => validate(driftedImage), /image differs from release receipt/)

  const normalizedSecuritySpelling = structuredClone(exact)
  normalizedSecuritySpelling.services['render-runner'].security_opt = [
    'apparmor=bonfire-render-runner-v1', 'no-new-privileges=true',
    'seccomp=/etc/docker/seccomp/bonfire-render-runner-v1.json'
  ]
  assert.equal(validate(normalizedSecuritySpelling), normalizedSecuritySpelling)

  const migratedQueueOwnership = structuredClone(exact)
  migratedQueueOwnership.services['render-queue-init'].command = ["mkdir -p /app/render-queue/jobs && chown 0:0 /app/render-queue /app/render-queue/jobs && chmod 2770 /app/render-queue /app/render-queue/jobs && chown 65532:65532 /app/render-queue /app/render-queue/jobs && find /app/render-queue/jobs -xdev -maxdepth 1 -type f -name '*.json' -exec chown 0:0 {} + -exec chmod 0660 {} + -exec chown 65532:65532 {} +"]
  assert.equal(validate(migratedQueueOwnership), migratedQueueOwnership)
})

test('rendered candidate Compose rejects security, storage, network, port, and lifecycle widening', () => {
  const receipt = makeReceipt()
  const exact = renderedComposeConfig(receipt)

  const reject = (mutate, pattern) => {
    const candidate = structuredClone(exact)
    mutate(candidate)
    assert.throws(() => validateRenderedComposeConfig(candidate, receipt, topologyContext), pattern)
  }

  reject(config => { config.services.meetingassist.privileged = true }, /privileged/)
  reject(config => { config.services.meetingassist.cap_add = ['SYS_ADMIN'] }, /added capabilities/)
  reject(config => { config.services.meetingassist.volumes.find(mount => mount.target === '/app/data').source = 'attacker' }, /mounts differ/)
  reject(config => { config.services.meetingassist.volumes.find(mount => mount.target === '/app/data').read_only = true }, /mounts differ/)
  reject(config => { config.services['canonical-postgres'].volumes[0] = { type: 'bind', source: '/tmp/postgres', target: '/var/lib/postgresql/data' } }, /mounts differ/)
  reject(config => { config.services.caddy.ports[0].published = '8080' }, /ports differ/)
  reject(config => { config.services.caddy.volumes[0].read_only = false }, /mounts differ/)
  reject(config => { config.services['render-runner'].networks.default = null }, /network attachment inventory is not exact/)
  reject(config => { config.services['render-runner'].environment.ANTHROPIC_API_KEY = 'widened' }, /environment inventory is not exact/)
  reject(config => { config.services.meetingassist.environment.UNREVIEWED_OVERRIDE = 'widened' }, /environment inventory is not exact/)
  reject(config => { config.services.caddy.environment = { CADDY_ADMIN: '0.0.0.0:2019' } }, /environment inventory is not exact/)
  reject(config => { config.services['render-runner'].env_file = [topologyContext.baseEnv] }, /env-file attachment is not exact/)
  reject(config => { config.services.meetingassist.env_file = ['/etc/shadow'] }, /env-file attachment is not exact/)
  reject(config => { delete config.services.meetingassist.env_file }, /env-file attachment is not exact/)
  reject(config => { config.services['render-runner'].volumes.push({ type: 'bind', source: '/var/run/docker.sock', target: '/var/run/docker.sock' }) }, /mounts differ/)
  reject(config => { config.services['render-runner'].user = '0:0' }, /restart\/user\/read-only/)
  reject(config => { config.services['render-runner'].security_opt = ['seccomp:unconfined'] }, /security options/)
  reject(config => { config.services['render-runner'].cap_drop = [] }, /dropped capabilities/)
  reject(config => { config.services['render-runner'].profiles = [] }, /profiles/)
  reject(config => { config.services['render-runner'].restart = 'always' }, /restart\/user\/read-only/)
  reject(config => { config.services['render-runner'].depends_on.meetingassist.condition = 'service_started' }, /dependency meetingassist/)
  reject(config => { config.services.meetingassist.healthcheck.disable = true }, /healthcheck differs/)
  reject(config => { config.services.meetingassist.healthcheck.test = ['CMD', 'curl', '-fsS', 'http://127.0.0.1:3000/readyz'] }, /healthcheck command/)
  reject(config => { config.services.meetingassist.healthcheck.test = ['CMD', 'curl', '-fsS', 'http://127.0.0.1:3000/healthz'] }, /healthcheck command/)
  reject(config => { config.services.meetingassist.healthcheck.start_period = '20s' }, /start period/)
  reject(config => { config.services.meetingassist.healthcheck.start_period = '5m1s' }, /start period/)
  reject(config => { config.services.meetingassist.command = [] }, /command must remain inherited/)
  reject(config => { config.services['render-runner'].entrypoint = [] }, /entrypoint must remain inherited/)
  reject(config => { config.services['render-queue-init'].network_mode = 'host' }, /network mode/)
  reject(config => { config.services['render-queue-init'].cap_add.push('SYS_ADMIN') }, /added capabilities/)
  reject(config => { config.services['render-queue-init'].command = ['chmod -R 0777 /app/render-queue'] }, /command differs/)
  reject(config => { config.services['render-runner'].tmpfs[0] = '/tmp:rw,rw,nosuid,nodev,noexec,size=512m' }, /tmpfs differs/)
  for (const [field, value] of Object.entries({
    pid: 'host', ipc: 'host', uts: 'host', userns_mode: 'host', devices: ['/dev/kvm:/dev/kvm'],
    volumes_from: ['meetingassist'], provider: { type: 'unreviewed' }, post_start: [{ command: 'true' }]
  })) reject(config => { config.services['render-runner'][field] = value }, /unsupported field/)
  reject(config => { config.networks.egress = { name: 'digitalocean_egress' } }, /top-level network inventory is not exact/)
  reject(config => { config.networks.render_internal.internal = false }, /network render_internal differs/)
  reject(config => { config.networks.render_internal.labels = { unreviewed: 'true' } }, /network render_internal.*labels/)
  reject(config => { config.volumes.attacker = { name: 'digitalocean_attacker' } }, /top-level volume inventory is not exact/)
  reject(config => { config.volumes.meeting_data.name = 'attacker_data' }, /volume meeting_data differs/)
  reject(config => { config.volumes.meeting_data.driver_opts = { type: 'nfs' } }, /volume meeting_data.*driver options/)
  reject(config => { config.volumes.canonical_postgres.external = false }, /volume canonical_postgres differs/)
  reject(config => { config.volumes.canonical_postgres.driver = 'local' }, /volume canonical_postgres differs/)
  reject(config => { config.secrets = { docker: { file: '/tmp/docker.sock' } } }, /top-level secrets must remain empty/)
  reject(config => { config.configs = { override: { file: '/tmp/override' } } }, /top-level configs must remain empty/)
})

test('failed-target cleanup removes only post-lock containers bound to the sealed candidate', () => {
  const startedAt = '2026-08-01T12:00:00.000Z'
  const targetCompose = '/opt/meetingassist-releases/target/sealed-candidate/deploy/digitalocean/docker-compose.yml'
  const targetWorkingDir = '/opt/meetingassist-releases/target/sealed-candidate/deploy/digitalocean'
  const baseline = ['meetingassist', 'render-runner', 'render-queue-init', 'canonical-postgres', 'coturn', 'caddy']
    .map((service, index) => ({ id: `baseline-${index}`, service }))
  const targetContainer = (id, service, number = '1') => ({
    id,
    service,
    configFiles: targetCompose,
    workingDir: targetWorkingDir,
    oneoff: 'False',
    containerNumber: number,
    createdAt: '2026-08-01T12:00:01.000Z'
  })

  // The target removed coturn/caddy, replaced meetingassist, scaled the render
  // runner, and added a forbidden service. Only its new IDs are removable;
  // missing baseline services are recreated by the retained Compose bundle.
  const current = [
    ...baseline.filter(entry => !['meetingassist', 'render-runner', 'coturn', 'caddy'].includes(entry.service)),
    targetContainer('target-app', 'meetingassist'),
    targetContainer('target-render-1', 'render-runner'),
    targetContainer('target-render-2', 'render-runner', '2'),
    targetContainer('target-codex', 'codex-runner')
  ]
  assert.deepEqual(planRollbackProjectCleanup(baseline, current, targetCompose, startedAt),
    ['target-app', 'target-codex', 'target-render-1', 'target-render-2'])
  assert.doesNotThrow(() => projectContainerSnapshotSha256(baseline))

  const foreignConfig = [...current, { ...targetContainer('foreign', 'codex-runner'), configFiles: '/tmp/foreign-compose.yml' }]
  assert.throws(() => planRollbackProjectCleanup(baseline, foreignConfig, targetCompose, startedAt), /not proven/)
  const preLock = [...current, { ...targetContainer('pre-lock', 'codex-runner'), createdAt: '2026-08-01T11:59:59.999Z' }]
  assert.throws(() => planRollbackProjectCleanup(baseline, preLock, targetCompose, startedAt), /not proven/)
  const sameTimestamp = [...current, { ...targetContainer('same-timestamp', 'codex-runner'), createdAt: startedAt }]
  assert.throws(() => planRollbackProjectCleanup(baseline, sameTimestamp, targetCompose, startedAt), /not proven/)
  const oneoff = [...current, { ...targetContainer('oneoff', 'codex-runner'), oneoff: 'True' }]
  assert.throws(() => planRollbackProjectCleanup(baseline, oneoff, targetCompose, startedAt), /not proven/)
  const missingWorkingDir = [...current, { ...targetContainer('missing-working-dir', 'codex-runner'), workingDir: '' }]
  assert.throws(() => planRollbackProjectCleanup(baseline, missingWorkingDir, targetCompose, startedAt), /not proven/)
  const relativeProvenance = [...current, { ...targetContainer('relative-provenance', 'codex-runner'), configFiles: 'docker-compose.yml', workingDir: '.' }]
  assert.throws(() => planRollbackProjectCleanup(baseline, relativeProvenance, targetCompose, startedAt), /not proven/)
  assert.throws(() => planRollbackProjectCleanup(baseline, current, 'relative-compose.yml', startedAt), /provenance is invalid/)
  const relabeledBaseline = current.map(entry => entry.id === 'baseline-2' ? { ...entry, service: 'codex-runner' } : entry)
  assert.throws(() => planRollbackProjectCleanup(baseline, relabeledBaseline, targetCompose, startedAt), /identity changed/)
})

test('failed-target resource cleanup removes only claimed empty networks and rejects every unexpected named volume', () => {
  const startedAt = '2026-08-01T12:00:00.000Z'
  const network = (key, internal = false) => ({
    id: `network-${key}`, name: `digitalocean_${key}`, project: 'digitalocean', resourceKey: key,
    createdAt: '2026-07-31T12:00:00.000Z', driver: 'bridge', scope: 'local', internal,
    attachable: false, ingress: false, configOnly: false,
    labels: { 'com.docker.compose.project': 'digitalocean', 'com.docker.compose.network': key, 'com.docker.compose.version': 'v2.40.0' },
    options: {}, ipam: {}, containerIDs: ['baseline-container']
  })
  const volumePolicies = {
    caddy_data: false, caddy_config: false, codex_queue: true, render_queue: false,
    meeting_data: false, usage_ledger: true, canonical_postgres: true
  }
  const volume = (key, external) => ({
    name: `digitalocean_${key}`, project: external ? '' : 'digitalocean', resourceKey: external ? '' : key,
    createdAt: '2026-07-31T12:00:00.000Z', driver: 'local', scope: 'local',
    mountpoint: `/var/lib/docker/volumes/digitalocean_${key}/_data`,
    labels: external ? {} : { 'com.docker.compose.project': 'digitalocean', 'com.docker.compose.volume': key },
    options: {}, containerIDs: ['baseline-container']
  })
  const baseline = {
    networks: [network('default'), network('render_internal', true)],
    volumes: Object.entries(volumePolicies).map(([key, external]) => volume(key, external))
  }
  assert.equal(validateProjectResourceBaseline(baseline), baseline)
  assert.equal(projectResourceSnapshotSha256(baseline), projectResourceSnapshotSha256({
    networks: [...baseline.networks].reverse(), volumes: [...baseline.volumes].reverse()
  }))

  const candidateNetwork = {
    ...network('candidate_net', true), id: 'network-candidate', createdAt: '2026-08-01T12:00:01.000Z', containerIDs: []
  }
  const candidateVolume = {
    ...volume('candidate_cache', false), createdAt: '2026-08-01T12:00:01.000Z', containerIDs: []
  }
  const currentNetworkOnly = { networks: [...baseline.networks, candidateNetwork], volumes: [...baseline.volumes] }
  const current = { networks: [...currentNetworkOnly.networks], volumes: [...baseline.volumes, candidateVolume] }
  const claims = { networkIDs: ['network-default', 'network-candidate'], volumeNames: ['digitalocean_meeting_data'] }
  const unexpectedVolumeClaims = { ...claims, volumeNames: [...claims.volumeNames, 'digitalocean_candidate_cache'] }
  assert.deepEqual(planRollbackProjectResourceCleanup(baseline, currentNetworkOnly, claims, startedAt), {
    networkIDs: ['network-candidate'], volumeNames: []
  })
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, current, unexpectedVolumeClaims, startedAt),
    /volume digitalocean_candidate_cache.*not proven disposable.*operator inspection/)
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, currentNetworkOnly, unexpectedVolumeClaims, startedAt),
    /claimed project volume digitalocean_candidate_cache.*absent.*not proven disposable.*operator inspection/)
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, currentNetworkOnly, {
    ...claims, networkIDs: [...claims.networkIDs, 'network-unlisted']
  }, startedAt), /claimed project network network-unlisted.*absent.*operator inspection/)

  const claimEntries = [{ id: 'target', networkIDs: ['network-default', 'network-candidate'],
    volumeNames: ['digitalocean_meeting_data', 'digitalocean_candidate_cache'] }]
  assert.deepEqual(projectResourceClaimsFromContainers(claimEntries, ['target']), {
    networkIDs: [...unexpectedVolumeClaims.networkIDs].sort(), volumeNames: [...unexpectedVolumeClaims.volumeNames].sort()
  })

  const preLock = structuredClone(currentNetworkOnly)
  preLock.networks.at(-1).createdAt = '2026-08-01T11:59:59.999Z'
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, preLock, claims, startedAt), /not proven/)
  const sameTimestamp = structuredClone(currentNetworkOnly)
  sameTimestamp.networks.at(-1).createdAt = startedAt
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, sameTimestamp, claims, startedAt), /not proven/)
  const unclaimed = structuredClone(currentNetworkOnly)
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, unclaimed, { networkIDs: [], volumeNames: [] }, startedAt), /not proven/)
  const attached = structuredClone(currentNetworkOnly)
  attached.networks.at(-1).containerIDs = ['foreign-container']
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, attached, claims, startedAt), /not proven/)
  const attachable = structuredClone(currentNetworkOnly)
  attachable.networks.at(-1).attachable = true
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, attachable, claims, startedAt), /not proven/)
  const extraNetworkLabel = structuredClone(currentNetworkOnly)
  extraNetworkLabel.networks.at(-1).labels.owner = 'foreign'
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, extraNetworkLabel, claims, startedAt), /not proven/)
  const missingNetworkVersion = structuredClone(currentNetworkOnly)
  delete missingNetworkVersion.networks.at(-1).labels['com.docker.compose.version']
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, missingNetworkVersion, claims, startedAt), /not proven/)
  const networkOptions = structuredClone(currentNetworkOnly)
  networkOptions.networks.at(-1).options = { 'com.docker.network.bridge.enable_icc': 'true' }
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, networkOptions, claims, startedAt), /not proven/)
  const duplicateNetworkName = structuredClone(currentNetworkOnly)
  duplicateNetworkName.networks.at(-1).name = 'digitalocean_default'
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, duplicateNetworkName, claims, startedAt), /repeats or omits an identity/)
  const attachedVolume = structuredClone(current)
  attachedVolume.volumes.at(-1).containerIDs = ['foreign-container']
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, attachedVolume, claims, startedAt), /not proven disposable/)
  const foreignName = structuredClone(current)
  foreignName.volumes.at(-1).name = 'foreign_named_volume'
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, foreignName, {
    ...claims, volumeNames: ['foreign_named_volume']
  }, startedAt), /not proven disposable/)
  const driverOptions = structuredClone(current)
  driverOptions.volumes.at(-1).options = { type: 'nfs' }
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, driverOptions, claims, startedAt), /not proven disposable/)
  const missingBaseline = structuredClone(current)
  missingBaseline.networks = missingBaseline.networks.filter(entry => entry.id !== 'network-default')
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, missingBaseline, claims, startedAt), /baseline.*missing/)
  const changedBaseline = structuredClone(current)
  changedBaseline.volumes.find(entry => entry.name === 'digitalocean_meeting_data').createdAt = '2026-08-01T12:00:02.000Z'
  assert.throws(() => planRollbackProjectResourceCleanup(baseline, changedBaseline, claims, startedAt), /baseline.*identity changed/)
  const unexpectedBaseline = structuredClone(baseline)
  unexpectedBaseline.networks.push(candidateNetwork)
  assert.throws(() => validateProjectResourceBaseline(unexpectedBaseline), /network inventory is not exact/)
})

test('release mutations use one fail-closed sibling lock and a verified retained rollback tool', async () => {
  const parent = await mkdtemp(join(tmpdir(), 'bonfire-release-lock-test-'))
  const target = join(parent, 'target')
  const rollback = join(parent, 'rollback')
  try {
    const first = await acquireReleaseOperationLock(target, rollback)
    assert.equal(Number.isNaN(Date.parse(first.startedAt)), false)
    await assert.rejects(acquireReleaseOperationLock(target, rollback), /active or left a stale fail-closed lock/)
    await assert.rejects(acquireReleaseOperationLock(rollback, target), /active or left a stale fail-closed lock/)
    await first.release()
    const second = await acquireReleaseOperationLock(target, rollback)
    await second.release()

    const guarded = await acquireReleaseOperationLock(target, rollback)
    await writeFile(join(guarded.path, 'unexpected'), 'tamper\n')
    await assert.rejects(guarded.release(), /unexpected state/)
    await rm(join(guarded.path, 'unexpected'))
    await guarded.release()

    const toolDigest = createHash('sha256').update(await readFile(releaseToolPath)).digest('hex')
    const retained = await loadRetainedRollbackTool(releaseToolPath, toolDigest)
    assert.equal(typeof retained.restoreReleaseBundleAfterFailedActivation, 'function')
    await assert.rejects(loadRetainedRollbackTool(releaseToolPath, digest('0')), /differs/)

    const incomplete = join(parent, 'incomplete.mjs')
    await writeFile(incomplete, 'export const safe = false\n')
    const incompleteDigest = createHash('sha256').update(await readFile(incomplete)).digest('hex')
    await assert.rejects(loadRetainedRollbackTool(incomplete, incompleteDigest), /lacks the verified automatic-restore entrypoint/)
    await assert.rejects(restoreReleaseBundleAfterFailedActivation({}), /--release-dir is required/)
    assert.equal(releasePaths(target).releaseTool, join(target, 'sealed-candidate/scripts/bonfire-release.mjs'))

    const rollbackPaths = releasePaths(rollback)
    const targetPaths = releasePaths(target)
    const toolBody = await readFile(releaseToolPath)
    await mkdir(join(rollbackPaths.releaseTool, '..'), { recursive: true })
    await mkdir(join(targetPaths.releaseTool, '..'), { recursive: true })
    await writeFile(rollbackPaths.releaseTool, toolBody)
    await writeFile(targetPaths.releaseTool, toolBody)
    const rollbackSource = { configFiles: { 'scripts/bonfire-release.mjs': sha256(toolBody) } }
    await verifyRetainedReleaseActivator(rollbackPaths.releaseTool, rollbackPaths, rollbackSource)
    let resumeMutation = false
    await assert.rejects((async () => {
      await verifyRetainedReleaseActivator(targetPaths.releaseTool, rollbackPaths, rollbackSource)
      resumeMutation = true
    })(), /currently serving retained release tool/)
    assert.equal(resumeMutation, false)
  } finally {
    await rm(parent, { recursive: true, force: true })
  }
})

test('release transaction commits only after two target verifications and durable ledger CAS', async () => {
  const events = []
  const prior = { generation: 7, state: 'rollback' }
  const next = { generation: 8, state: 'target' }
  let ledger = prior
  let releases = 0
  let verification = 0
  const result = await executeReleaseTransaction({
    operationLock: { release: async () => { events.push('unlock'); releases++ } },
    priorLedger: prior,
    nextLedger: next,
    readLedger: async () => structuredClone(ledger),
    preflightTarget: async () => { events.push('preflight-target') },
    applyTarget: async () => { events.push('apply-target') },
    verifyTarget: async () => { events.push(`verify-target-${++verification}`); return { verified: true, verification } },
    writeLedger: async value => { events.push('commit-ledger'); ledger = structuredClone(value) },
    restoreRollback: async () => { events.push('restore-rollback') },
    restoreLedger: async value => { events.push('restore-ledger'); ledger = structuredClone(value) }
  })
  assert.deepEqual(result, { verified: true, verification: 2 })
  assert.deepEqual(ledger, next)
  assert.equal(releases, 1)
  assert.deepEqual(events, ['preflight-target', 'apply-target', 'verify-target-1', 'commit-ledger', 'verify-target-2', 'unlock'])
})

test('release transaction rolls back target and restores prior ledger when ledger commit is ambiguous', async () => {
  const events = []
  const prior = { generation: 4, state: 'rollback' }
  const next = { generation: 5, state: 'target' }
  let ledger = prior
  let releases = 0
  await assert.rejects(executeReleaseTransaction({
    operationLock: { release: async () => { events.push('unlock'); releases++ } },
    priorLedger: prior,
    nextLedger: next,
    readLedger: async () => structuredClone(ledger),
    preflightTarget: async () => { events.push('preflight-target') },
    applyTarget: async () => { events.push('apply-target') },
    verifyTarget: async () => { events.push('verify-target'); return { verified: true } },
    // Model rename succeeding and the following directory fsync failing.
    writeLedger: async value => { events.push('commit-ledger-visible-then-error'); ledger = structuredClone(value); throw new Error('ledger fsync failed') },
    restoreRollback: async () => { events.push('restore-rollback') },
    restoreLedger: async (value, recovery) => {
      assert.equal(recovery.ledgerCommitAttempted, true)
      assert.deepEqual(recovery.nextLedger, next)
      events.push('restore-ledger')
      ledger = structuredClone(value)
    }
  }), error => {
    assert.equal(error.releaseTransactionRecovered, true)
    assert.match(error.message, /restored the prior release and ledger/)
    return true
  })
  assert.deepEqual(ledger, prior)
  assert.equal(releases, 1)
  assert.deepEqual(events, [
    'preflight-target', 'apply-target', 'verify-target', 'commit-ledger-visible-then-error',
    'restore-rollback', 'restore-ledger', 'unlock'
  ])
})

test('release transaction retains its fail-closed lock when retained recovery is ambiguous', async () => {
  const prior = { generation: 2, state: 'rollback' }
  const events = []
  let releases = 0
  await assert.rejects(executeReleaseTransaction({
    operationLock: { release: async () => { releases++ } },
    priorLedger: prior,
    nextLedger: { generation: 3, state: 'target' },
    readLedger: async () => structuredClone(prior),
    preflightTarget: async () => { events.push('preflight-target') },
    applyTarget: async () => { events.push('apply-target'); throw new Error('compose failed') },
    verifyTarget: async () => { throw new Error('must not verify') },
    writeLedger: async () => { throw new Error('must not write') },
    restoreRollback: async () => { events.push('restore-rollback'); throw new Error('rollback failed') },
    restoreLedger: async () => { events.push('restore-ledger') }
  }), error => {
    assert.ok(error instanceof AggregateError)
    assert.match(error.message, /recovery is ambiguous.*lock was retained/)
    return true
  })
  assert.deepEqual(events, ['preflight-target', 'apply-target', 'restore-rollback'])
  assert.equal(releases, 0)
})

test('release transaction never overwrites an out-of-band ledger during recovery', async () => {
  const prior = { generation: 2, state: 'rollback' }
  const foreign = { generation: 50, state: 'foreign' }
  let ledger = prior
  let releases = 0
  await assert.rejects(executeReleaseTransaction({
    operationLock: { release: async () => { releases++ } },
    priorLedger: prior,
    nextLedger: { generation: 3, state: 'target' },
    readLedger: async () => structuredClone(ledger),
    preflightTarget: async () => {},
    applyTarget: async () => {},
    verifyTarget: async () => { ledger = foreign },
    writeLedger: async () => { throw new Error('must not write') },
    restoreRollback: async () => {},
    restoreLedger: async (_value, recovery) => {
      assert.equal(recovery.ledgerCommitAttempted, false)
      throw new Error('refusing to overwrite foreign ledger')
    }
  }), error => {
    assert.ok(error instanceof AggregateError)
    assert.match(error.message, /recovery is ambiguous.*lock was retained/)
    return true
  })
  assert.deepEqual(ledger, foreign)
  assert.equal(releases, 0)
})

test('release transaction refuses a stale ledger before mutation and safely unlocks', async () => {
  const events = []
  let releases = 0
  await assert.rejects(executeReleaseTransaction({
    operationLock: { release: async () => { events.push('unlock'); releases++ } },
    priorLedger: { generation: 1 },
    nextLedger: { generation: 2 },
    readLedger: async () => ({ generation: 99 }),
    preflightTarget: async () => { events.push('preflight-target') },
    applyTarget: async () => { events.push('apply-target') },
    verifyTarget: async () => { events.push('verify-target') },
    writeLedger: async () => { events.push('write-ledger') },
    restoreRollback: async () => { events.push('restore-rollback') },
    restoreLedger: async () => { events.push('restore-ledger') }
  }), /changed before/)
  assert.deepEqual(events, ['unlock'])
  assert.equal(releases, 1)
})

test('release transaction rejects target topology preflight without mutation or rollback', async () => {
  const events = []
  const ledger = { generation: 11, state: 'rollback' }
  let releases = 0
  await assert.rejects(executeReleaseTransaction({
    operationLock: { release: async () => { events.push('unlock'); releases++ } },
    priorLedger: ledger,
    nextLedger: { generation: 12, state: 'target' },
    readLedger: async () => structuredClone(ledger),
    preflightTarget: async () => { events.push('preflight-target'); throw new Error('rendered service inventory is not exact') },
    applyTarget: async () => { events.push('apply-target') },
    verifyTarget: async () => { events.push('verify-target') },
    writeLedger: async () => { events.push('write-ledger') },
    restoreRollback: async () => { events.push('restore-rollback') },
    restoreLedger: async () => { events.push('restore-ledger') }
  }), /service inventory is not exact/)
  assert.deepEqual(events, ['preflight-target', 'unlock'])
  assert.equal(releases, 1)
})

test('rollback requires the ledger exact previous release and rejects arbitrary siblings', () => {
  const currentDir = '/opt/meetingassist-releases/current'
  const previousDir = '/opt/meetingassist-releases/previous'
  const arbitraryDir = '/opt/meetingassist-releases/arbitrary'
  const transitionReceipt = (commitChar, bundleChar, appChar, renderChar) => ({
    source: { releaseCommit: commitChar.repeat(40) },
    bundleSha256: digest(bundleChar),
    images: {
      meetingassist: { imageId: `sha256:${digest(appChar)}` },
      renderRunner: { imageId: `sha256:${digest(renderChar)}` }
    }
  })
  const current = transitionReceipt('1', 'a', 'b', 'c')
  const previous = transitionReceipt('2', 'd', 'e', 'f')
  const entry = (releaseDir, receipt) => ({
    releaseDir,
    releaseCommit: receipt.source.releaseCommit,
    bundleSha256: receipt.bundleSha256,
    meetingassistImageId: receipt.images.meetingassist.imageId,
    renderRunnerImageId: receipt.images.renderRunner.imageId
  })
  const ledger = validateActiveReleaseLedger({
    schema: 'bonfire.active-release-ledger.v1', generation: 9, updatedAt: new Date().toISOString(),
    active: entry(currentDir, current), previous: entry(previousDir, previous)
  })

  assert.doesNotThrow(() => validateReleaseTransition('rolledBack', ledger, previousDir, previous, currentDir, current))
  assert.throws(() => validateReleaseTransition('rolledBack', null, previousDir, previous, currentDir, current), /requires an existing active release ledger/)
  assert.throws(() => validateReleaseTransition('rolledBack', ledger, arbitraryDir, previous, currentDir, current), /rollback target.*differs/)
  const arbitrary = transitionReceipt('3', '5', '6', '7')
  assert.throws(() => validateReleaseTransition('rolledBack', ledger, previousDir, arbitrary, currentDir, current), /rollback target.*differs/)
  assert.doesNotThrow(() => validateReleaseTransition('activated', null, arbitraryDir, arbitrary, currentDir, current))
})

test('labels, runtime environment, probes, and exact release env fail on drift', () => {
  const receipt = makeReceipt()
  const labels = {
    'org.opencontainers.image.revision': source.releaseCommit,
    'xyz.thebonfire.git-tree-digest': source.gitTreeDigest,
    'xyz.thebonfire.config-digest': source.buildConfigSha256,
    'xyz.thebonfire.transitive-inputs-digest': source.transitiveInputsSha256,
    'xyz.thebonfire.source-archive-digest': source.sourceArchiveSha256,
    'xyz.thebonfire.build-input-manifest-digest': receipt.buildInputManifestSha256,
    'xyz.thebonfire.attestation': 'unsigned-external-verification-required'
  }
  assert.doesNotThrow(() => verifyLabels(labels, source, receipt.buildInputManifestSha256))
  assert.throws(() => verifyLabels({ ...labels, 'org.opencontainers.image.revision': '2'.repeat(40) }, source, receipt.buildInputManifestSha256), /label/)
  const environment = environmentValues(receipt)
  assert.doesNotThrow(() => verifyRuntimeEnvironment(environment, receipt))
  assert.throws(() => verifyRuntimeEnvironment({ ...environment, BONFIRE_BINARY_SHA256: digest('f') }, receipt), /environment/)
  const releaseEnvironmentBody = `${Object.entries(releaseEnvironmentValues(receipt)).map(([name, value]) => `${name}=${value}`).join('\n')}\n`
  assert.doesNotThrow(() => verifyReleaseEnvironmentFile(releaseEnvironmentBody, receipt))
  assert.throws(() => verifyReleaseEnvironmentFile(`${releaseEnvironmentBody}OPENAI_API_KEY=secret\n`, receipt), /unexpected fields/)

  const release = {
    schema: 'bonfire.release-identity.v1', required: true, qualified: false, processQualified: true,
    externallyAttested: false, externalAttestationRequired: true,
    attestationReason: 'unsigned_external_verification_required', releaseCommit: source.releaseCommit,
    gitTreeDigest: source.gitTreeDigest, sourceArchiveSha256: source.sourceArchiveSha256,
    transitiveInputsSha256: source.transitiveInputsSha256, buildConfigSha256: source.buildConfigSha256,
    buildInputManifestSha256: receipt.buildInputManifestSha256, claimedBuildManifestSha256: receipt.buildManifestSha256,
    binarySha256: receipt.images.meetingassist.binarySha256, claimedImageDigest: receipt.images.meetingassist.imageDigest,
    environmentMarker: receipt.environmentMarker
  }
  const probe = { ok: true, version: source.releaseCommit, release }
  assert.doesNotThrow(() => verifyProbeRelease(probe, receipt, 'health'))
  assert.throws(() => verifyProbeRelease({ ...probe, release: { ...release, qualified: true } }, receipt, 'health'), /not honest/)
})

test('W4 deployment policy preserves legacy canary and binds the live receipt environment exactly', () => {
  const canary = validateStrideE10W4DeploymentPolicy({ ...w4LivePolicy, releaseMode: 'canary' })
  assert.equal(canary.releaseMode, 'canary')
  assert.doesNotThrow(() => validateStrideE10W4ComposeSource('services: {}\n', canary))
  assert.throws(() => validateStrideE10W4ComposeSource('STRIDE_E10_W4_RELEASE_MODE=x\n', canary), /legacy environment shape/)
  for (const mutate of [
    value => { value.releaseMode = 'unknown' },
    value => { value.snapshotPath = 'relative' },
    value => { value.activationReceiptPath = value.snapshotPath },
    value => { value.extra = true }
  ]) {
    const value = structuredClone(w4LivePolicy); mutate(value)
    assert.throws(() => validateStrideE10W4DeploymentPolicy(value))
  }

  const liveCompose = [
    'STRIDE_E10_W4_MODE: ${STRIDE_E10_W4_RELEASE_MODE:?STRIDE_E10_W4_RELEASE_MODE is required}',
    'STRIDE_E10_W4_SNAPSHOT_PATH: ${STRIDE_E10_W4_SNAPSHOT_PATH:?STRIDE_E10_W4_SNAPSHOT_PATH is required}',
    'STRIDE_E10_W4_ACTIVATION_BACKUP_DIR: ${STRIDE_E10_W4_ACTIVATION_BACKUP_DIR:?STRIDE_E10_W4_ACTIVATION_BACKUP_DIR is required}',
    'STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH: ${STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH:?STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH is required}'
  ].join('\n')
  assert.doesNotThrow(() => validateStrideE10W4ComposeSource(liveCompose, w4LivePolicy))
  assert.throws(() => validateStrideE10W4ComposeSource(liveCompose.replace(':?', ':-'), w4LivePolicy), /not exact/)

  const legacy = makeReceipt()
  assert.equal(Object.keys(releaseEnvironmentValues(legacy)).some(name => name.startsWith('STRIDE_E10_W4_')), false)
  assert.doesNotThrow(() => validateRenderedComposeConfig(renderedComposeConfig(legacy), legacy, topologyContext))
  const live = makeReceipt(w4LivePolicy)
  assert.equal(validateReleaseReceipt(live), live)
  const releaseEnvironment = releaseEnvironmentValues(live)
  assert.equal(releaseEnvironment.STRIDE_E10_W4_RELEASE_MODE, 'bonfire_network_live')
  assert.equal(releaseEnvironment.STRIDE_E10_W4_SNAPSHOT_PATH, w4LivePolicy.snapshotPath)
  assert.equal(releaseEnvironment.STRIDE_E10_W4_ACTIVATION_BACKUP_DIR, w4LivePolicy.activationBackupDir)
  assert.equal(releaseEnvironment.STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH, w4LivePolicy.activationReceiptPath)
  assert.equal(releaseEnvironment.STRIDE_E10_W4_MODE, undefined)
  assert.doesNotThrow(() => verifyReleaseEnvironmentFile(`${Object.entries(releaseEnvironment).map(([name, value]) => `${name}=${value}`).join('\n')}\n`, live))
  const running = environmentValues(live)
  assert.equal(running.STRIDE_E10_W4_MODE, 'bonfire_network_live')
  assert.doesNotThrow(() => verifyRuntimeEnvironment(running, live))
  assert.doesNotThrow(() => validateRenderedComposeConfig(renderedComposeConfig(live), live, topologyContext))
  assert.throws(() => verifyRuntimeEnvironment({ ...running, STRIDE_E10_W4_MODE: 'canary' }, live), /environment/)
})

test('W4 live activation, explicit rollback, and automatic recovery use closed maintenance commands', () => {
  const candidate = '/opt/meetingassist-releases/live/sealed-candidate/deploy/digitalocean/docker-compose.yml'
  const runtimeEnv = '/opt/meetingassist-releases/live/release.env'
  const baseEnv = '/opt/meetingassist/deploy/digitalocean/.env'
  const imageID = `sha256:${digest('9')}`
  for (const [operation, flag, readOnly] of [
    ['activate', '-stride-e10-w4-activate-network', false],
    ['verify', '-stride-e10-w4-verify-network-activation', true],
    ['verifyRuntime', '-stride-e10-w4-verify-network-runtime', true],
    ['rollback', '-stride-e10-w4-rollback-network', false],
    ['verifyRollback', '-stride-e10-w4-verify-network-rollback', true]
  ]) {
    const args = strideE10W4MaintenanceArgs(baseEnv, runtimeEnv, candidate, operation, 'digitalocean', imageID)
    assert.equal(args.at(-1), flag)
    assert.equal(args.includes('--read-only'), readOnly)
    assert.ok(args.includes(runtimeEnv))
    assert.equal(args.includes(candidate), !readOnly)
    assert.equal(args.includes('--no-deps'), !readOnly)
    assert.equal(args.includes('--network') && args.includes('none') && args.includes(imageID), readOnly)
    for (const mount of ['digitalocean_meeting_data:/app/data:ro', 'digitalocean_usage_ledger:/app/data/usage:ro',
      'digitalocean_codex_queue:/app/codex-queue:ro', 'digitalocean_render_queue:/app/render-queue:ro']) {
      assert.equal(args.includes(mount), readOnly)
    }
  }
  assert.throws(() => strideE10W4MaintenanceArgs(baseEnv, runtimeEnv, candidate, 'shell'), /invalid/)
  assert.throws(() => strideE10W4MaintenanceArgs(baseEnv, runtimeEnv, candidate, 'verify'), /image/)
  const canary = makeReceipt()
  const live = makeReceipt(w4LivePolicy)
  assert.deepEqual(strideE10W4ReleaseTransitionPlan('activated', live, canary), {
    activateTargetBeforeStart: true, verifyTargetRuntimeBeforeStart: false,
    rollbackCurrentBeforeExplicitRollback: false, rollbackTargetBeforeRecovery: true,
    verifyRollbackRuntimeBeforeRecovery: false,
    reactivateRollbackBeforeRecovery: false
  })
  assert.deepEqual(strideE10W4ReleaseTransitionPlan('rolledBack', canary, live), {
    activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: true,
    rollbackCurrentBeforeExplicitRollback: false, rollbackTargetBeforeRecovery: false,
    verifyRollbackRuntimeBeforeRecovery: true,
    reactivateRollbackBeforeRecovery: false
  })
  assert.deepEqual(strideE10W4ReleaseTransitionPlan('activated', live, live), {
    activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: true,
    rollbackCurrentBeforeExplicitRollback: false, rollbackTargetBeforeRecovery: false,
    verifyRollbackRuntimeBeforeRecovery: true,
    reactivateRollbackBeforeRecovery: false
  })
  assert.deepEqual(strideE10W4ReleaseTransitionPlan('rolledBack', live, live), {
    activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: true,
    rollbackCurrentBeforeExplicitRollback: false, rollbackTargetBeforeRecovery: false,
    verifyRollbackRuntimeBeforeRecovery: true,
    reactivateRollbackBeforeRecovery: false
  })
  assert.throws(() => strideE10W4ReleaseTransitionPlan('recover', live, canary), /invalid/)
})

test('live successor failure verifies lineage and restores retained live without data mutation', async () => {
  const plan = strideE10W4ReleaseTransitionPlan('activated', makeReceipt(w4LivePolicy), makeReceipt(w4LivePolicy))
  const calls = []
  const priorLedger = { generation: 1 }
  await assert.rejects(executeReleaseTransaction({
    operationLock: { release: async () => { calls.push('unlock') } }, priorLedger, nextLedger: { generation: 2 },
    readLedger: async () => priorLedger,
    preflightTarget: async () => { calls.push('preflight-successor') },
    applyTarget: async () => {
      if (plan.verifyTargetRuntimeBeforeStart) calls.push('verify-successor-lineage')
      calls.push('start-successor')
      throw new Error('successor startup failed')
    },
    verifyTarget: async () => { throw new Error('unreachable') },
    writeLedger: async () => {},
    restoreRollback: async () => {
      if (plan.rollbackTargetBeforeRecovery) calls.push('rollback-successor-data')
      if (plan.reactivateRollbackBeforeRecovery) calls.push('reactivate-retained-data')
      if (plan.verifyRollbackRuntimeBeforeRecovery) calls.push('verify-retained-lineage')
      calls.push('start-retained-live')
    },
    restoreLedger: async () => { calls.push('restore-ledger') }
  }), /restored the prior release/)
  assert.deepEqual(calls, [
    'preflight-successor', 'verify-successor-lineage', 'start-successor',
    'verify-retained-lineage', 'start-retained-live', 'restore-ledger', 'unlock'
  ])
  assert.equal(calls.includes('rollback-successor-data'), false)
  assert.equal(calls.includes('reactivate-retained-data'), false)
})

test('explicit live to canary downgrade and failure recovery never mutate evolved data', async () => {
  const plan = strideE10W4ReleaseTransitionPlan('rolledBack', makeReceipt(), makeReceipt(w4LivePolicy))
  const calls = []
  const priorLedger = { generation: 1 }
  await assert.rejects(executeReleaseTransaction({
    operationLock: { release: async () => { calls.push('unlock') } }, priorLedger, nextLedger: { generation: 2 },
    readLedger: async () => priorLedger,
    preflightTarget: async () => { calls.push('preflight-canary') },
    applyTarget: async () => {
      if (plan.verifyTargetRuntimeBeforeStart) calls.push('verify-evolved-runtime-read-only')
      calls.push('start-canary')
      throw new Error('canary startup failed')
    },
    verifyTarget: async () => { throw new Error('unreachable') },
    writeLedger: async () => {},
    restoreRollback: async () => {
      if (plan.verifyRollbackRuntimeBeforeRecovery) calls.push('verify-retained-runtime-read-only')
      calls.push('retained-live-restore')
    },
    restoreLedger: async () => { calls.push('restore-ledger') }
  }), /restored the prior release/)
  assert.deepEqual(calls.slice(0, -1), [
    'preflight-canary', 'verify-evolved-runtime-read-only', 'start-canary',
    'verify-retained-runtime-read-only', 'retained-live-restore', 'restore-ledger'
  ])
  assert.equal(calls.some(call => call.includes('rollback') || call.includes('reactivate')), false)
  assert.equal(calls.at(-1), 'unlock')
})

test('activation and rollback pin candidate Compose, project, render profile, and no-build', () => {
  const candidate = '/opt/meetingassist-releases/abc/sealed-candidate/deploy/digitalocean/docker-compose.yml'
  const args = composeActivationArgs('/opt/meetingassist/deploy/digitalocean/.env', '/opt/meetingassist-releases/abc/release.env', candidate)
  assert.ok(args.includes('--file'))
  assert.ok(args.includes(candidate))
  assert.ok(args.includes('--project-name'))
  assert.ok(args.includes('digitalocean'))
  assert.ok(args.includes('render'))
  assert.deepEqual(args.slice(-6), ['up', '-d', '--no-build', '--wait', '--wait-timeout', '360'])
  assert.equal(args.includes('--build'), false)
  assert.throws(() => composeActivationArgs('/tmp/base.env', '/tmp/release.env', candidate, 'attacker'), /preserve named volumes/)
})

test('durable release ingress fence and private start commands exclude public exposure until ledger commit', () => {
  const candidate = '/opt/meetingassist-releases/abc/sealed-candidate/deploy/digitalocean/docker-compose.yml'
  const base = '/opt/meetingassist/deploy/digitalocean/.env'
  const runtime = '/opt/meetingassist-releases/abc/release.env'
  const privateArgs = composePrivateActivationArgs(base, runtime, candidate)
  assert.deepEqual(privateArgs.slice(-4), ['meetingassist', 'canonical-postgres', 'render-runner', 'coturn'])
  assert.equal(privateArgs.includes('caddy'), false)
  assert.deepEqual(composeIngressArgs(base, runtime, candidate, 'stop').slice(-4), ['stop', '--timeout', '30', 'caddy'])
  assert.deepEqual(composeIngressArgs(base, runtime, candidate, 'start').slice(-7),
    ['up', '-d', '--no-build', '--wait', '--wait-timeout', '60', 'caddy'])
  assert.throws(() => composeIngressArgs(base, runtime, candidate, 'reload'), /invalid/)
})

test('receipt-bound private journal rejects drift and permits every resumable phase', () => {
  const entry = char => ({ releaseDir: `/opt/meetingassist-releases/${char.repeat(40)}`,
    releaseCommit: char.repeat(40), bundleSha256: digest(char), meetingassistImageId: `sha256:${digest(char)}`,
    renderRunnerImageId: `sha256:${digest(char === 'a' ? 'b' : 'a')}` })
  const ledger = generation => ({ schema: 'bonfire.active-release-ledger.v1', generation,
    updatedAt: '2026-08-09T12:00:00.000Z', active: entry('a'), previous: entry('b') })
  const lock = { token: 'transaction-token' }
  const baseEnvPatch = validateBaseEnvPatchPlan(makeBaseEnvPatchPlan({ transactionToken: lock.token }))
  const priorLedger = { ...ledger(1), active: entry('b'), previous: entry('a') }
  const base = { schema: 'bonfire.release-transaction.v2', token: lock.token, action: 'activated', phase: 'prepared',
    targetBundleSha256: digest('c'), rollbackBundleSha256: digest('d'), targetRenderedComposeSha256: digest('e'),
    rollbackRenderedComposeSha256: digest('f'), priorLedger, nextLedger: ledger(2),
    baseEnvPatch, baseEnvPatchMode: 'activate', recoveryFromPhase: null,
    baselineProjectContainers: [], baselineProjectResources: { networks: [], volumes: [] },
    createdAt: '2026-08-09T12:00:00.000Z', updatedAt: '2026-08-09T12:00:00.000Z' }
  for (const phase of ['prepared', 'ingress_stopped', 'base_env_patch_started', 'base_env_patched', 'target_preflighted', 'data_transition_started', 'data_ready', 'private_started',
    'private_verified', 'ledger_written', 'ingress_opened', 'external_verified']) {
    assert.equal(validateReleaseTransactionJournal({ ...base, phase }, lock).phase, phase)
  }
  for (const phase of ['recovery_started', 'recovery_data_restored', 'recovery_env_restore_started', 'recovery_env_restored',
    'recovery_runtime_verified', 'recovery_private_started', 'recovery_private_verified', 'recovery_ledger_restored',
    'recovery_ingress_opened', 'recovery_external_verified']) {
    assert.equal(validateReleaseTransactionJournal({ ...base, phase, recoveryFromPhase: 'private_started' }, lock).phase, phase)
  }
  const rollbackPriorLedger = ledger(2)
  const rollbackNextLedger = { ...ledger(3), active: entry('b'), previous: entry('a') }
  const rollbackJournal = {
    ...base, action: 'rolledBack', priorLedger: rollbackPriorLedger, nextLedger: rollbackNextLedger,
    baseEnvPatch: makeBaseEnvPatchPlan({ targetLedgerGeneration: 2 }), baseEnvPatchMode: 'rollback'
  }
  assert.equal(validateReleaseTransactionJournal(rollbackJournal, lock).baseEnvPatchMode, 'rollback')
  assert.equal(validateReleaseTransactionJournal({ ...rollbackJournal, phase: 'base_env_patched',
    targetRenderedComposeSha256: null }, lock).targetRenderedComposeSha256, null)
  assert.throws(() => validateReleaseTransactionJournal({ ...rollbackJournal, phase: 'target_preflighted',
    targetRenderedComposeSha256: null }, lock), /preflight digest/)
  assert.throws(() => validateReleaseTransactionJournal({ ...rollbackJournal,
    baseEnvPatch: makeBaseEnvPatchPlan({ rollbackReleaseCommit: 'c'.repeat(40), targetLedgerGeneration: 2 }) }, lock), /binding/)
  assert.equal(validateReleaseTransactionJournal({ ...base, baseEnvPatch: null, baseEnvPatchMode: null }, lock).baseEnvPatch, null)
  assert.throws(() => validateReleaseTransactionJournal({ ...base, targetBundleSha256: digest('9') }, { token: 'other' }), /invalid/)
  assert.throws(() => validateReleaseTransactionJournal({ ...base, attacker: true }, lock), /invalid/)
  assert.throws(() => validateReleaseTransactionJournal({ ...base, phase: 'recovery_started' }, lock), /recovery origin/)
})

test('target-only qualification arguments are exact, complete, and confined to one private backup directory', async () => {
  const options = {
    targetBaseEnvExpectedSha256: digest('1'),
    targetBaseEnvPatchKey: 'PRIVATE_REALTIME_VOICE_QUALIFIED',
    targetBaseEnvPatchValue: 'true',
    targetBaseEnvBackupDir: '/opt/meetingassist-backups'
  }
  assert.deepEqual(requestedTargetBaseEnvPatch(options, 'activated'), {
    expectedBeforeSha256: digest('1'), patchKey: 'PRIVATE_REALTIME_VOICE_QUALIFIED', patchValue: 'true',
    backupDir: '/opt/meetingassist-backups'
  })
  assert.equal(requestedTargetBaseEnvPatch({}, 'activated'), null)
  assert.throws(() => requestedTargetBaseEnvPatch({ ...options, targetBaseEnvPatchValue: '' }, 'activated'), /every explicit target-only/)
  assert.throws(() => requestedTargetBaseEnvPatch({ ...options, targetBaseEnvPatchKey: 'OTHER' }, 'activated'), /approved private Realtime/)
  assert.throws(() => requestedTargetBaseEnvPatch(options, 'rolledBack'), /only for activate/)
  assert.throws(() => requestedTargetBaseEnvPatch({ ...options,
    targetBaseEnvBackupDir: '/opt/meetingassist-backups/parent/child' }, 'activated'), /exactly \/opt\/meetingassist-backups/)
  assert.throws(() => requestedTargetBaseEnvPatch({ ...options,
    targetBaseEnvBackupDir: '/tmp/attacker' }, 'activated'), /exactly \/opt\/meetingassist-backups/)
  await assert.rejects(execFileAsync(process.execPath, [releaseToolPath, 'scope', '--repo', repoRoot, '--repo', repoRoot]), /duplicate argument --repo/)
})

test('qualification rollback accepts only one exact committed receipt path on rollback', async () => {
  const plan = makeBaseEnvPatchPlan()
  assert.equal(requestedQualificationRollbackReceipt({ qualificationRollbackReceipt: plan.receiptPath }, 'rolledBack'), plan.receiptPath)
  assert.equal(requestedQualificationRollbackReceipt({}, 'rolledBack'), null)
  assert.throws(() => requestedQualificationRollbackReceipt({ qualificationRollbackReceipt: plan.receiptPath }, 'activated'), /only for rollback/)
  assert.throws(() => requestedQualificationRollbackReceipt({ qualificationRollbackReceipt: '/tmp/receipt.json' }, 'rolledBack'), /exact receipt/)
  assert.throws(() => requestedQualificationRollbackReceipt({ qualificationRollbackReceipt: `${plan.receiptPath}/../x` }, 'rolledBack'), /exact receipt/)
  const receipt = makeBaseEnvPatchReceipt(plan)
  assert.deepEqual(baseEnvPatchPlanFromReceipt(plan.receiptPath, receipt), plan)
  assert.throws(() => baseEnvPatchPlanFromReceipt('/opt/meetingassist-backups/wrong.receipt.json', receipt), /invalid/)
  assert.equal(validateBaseEnvPatchReceipt(makeBaseEnvPatchReceipt(plan, {
    state: 'prior_committed', priorRestoredAt: '2026-08-22T12:02:00.000Z'
  }), plan).state, 'prior_committed')
  await assert.rejects(execFileAsync(process.execPath, [releaseToolPath, 'verify',
    '--qualification-rollback-receipt', plan.receiptPath]), /permitted only for rollback/)
})

test('qualified release transitions fail closed without exact generation-bound lineage', () => {
  assert.deepEqual(assertQualificationTransitionBound('activated', 'absent', null),
    { action: 'activated', currentState: 'absent', baseEnvPatchMode: null })
  assert.doesNotThrow(() => assertQualificationTransitionBound('activated', 'false', null))
  assert.doesNotThrow(() => assertQualificationTransitionBound('activated', 'absent', 'activate'))
  assert.throws(() => assertQualificationTransitionBound('activated', 'true', null), /durable qualification lineage/)
  assert.throws(() => assertQualificationTransitionBound('activated', 'true', 'activate'), /durable qualification lineage/)
  assert.throws(() => assertQualificationTransitionBound('rolledBack', 'true', null), /qualification-rollback-receipt/)
  assert.doesNotThrow(() => assertQualificationTransitionBound('rolledBack', 'true', 'rollback'))
  assert.doesNotThrow(() => assertQualificationTransitionBound('rolledBack', 'absent', null))
  assert.doesNotThrow(() => assertQualificationTransitionBound('rolledBack', 'false', null))
  assert.throws(() => assertQualificationTransitionBound('other', 'true', null), /proof is invalid/)
})

test('successful qualification output hands off the exact redacted rollback receipt', () => {
  const plan = makeBaseEnvPatchPlan()
  const activation = releaseTransactionCompletionEvidence({
    action: 'activated', nextLedger: { generation: 2 }, baseEnvPatchMode: 'activate', baseEnvPatch: plan
  }, 'prepared')
  assert.deepEqual(activation, {
    action: 'activated', ledgerGeneration: 2, resumed: false,
    qualificationReceipt: plan.receiptPath, qualificationState: 'target'
  })
  assert.equal(JSON.stringify(activation).includes('do-not-disclose'), false)
  assert.deepEqual(releaseTransactionCompletionEvidence({
    action: 'rolledBack', nextLedger: { generation: 3 }, baseEnvPatchMode: 'rollback', baseEnvPatch: plan
  }, 'private_started'), {
    action: 'rolledBack', ledgerGeneration: 3, resumed: true,
    qualificationReceipt: plan.receiptPath, qualificationState: 'prior'
  })
  assert.deepEqual(releaseTransactionCompletionEvidence({
    action: 'activated', nextLedger: { generation: 1 }, baseEnvPatchMode: null, baseEnvPatch: null
  }, 'prepared'), {
    action: 'activated', ledgerGeneration: 1, resumed: false,
    qualificationReceipt: null, qualificationState: null
  })
  assert.throws(() => releaseTransactionCompletionEvidence({
    action: 'activated', nextLedger: { generation: 2 }, baseEnvPatchMode: null, baseEnvPatch: plan
  }, 'prepared'), /completion evidence is invalid/)
})

test('qualification env patch preserves an exact absent or false prior and rejects ambiguous bytes', () => {
  const before = Buffer.from('OPENAI_API_KEY=do-not-disclose\r\nPRIVATE_REALTIME_VOICE_QUALIFIED=false\r\nOTHER=value\n')
  const patch = privateRealtimeVoiceQualificationEnvPatch(before)
  assert.equal(patch.priorQualificationState, 'false')
  assert.equal(patch.beforeSha256, sha256(before))
  assert.equal(patch.after.toString(), 'OPENAI_API_KEY=do-not-disclose\r\nPRIVATE_REALTIME_VOICE_QUALIFIED=true\r\nOTHER=value\n')
  assert.equal(patch.afterSha256, sha256(patch.after))
  assert.equal(patch.after.toString().replace('PRIVATE_REALTIME_VOICE_QUALIFIED=true', 'PRIVATE_REALTIME_VOICE_QUALIFIED=false'), before.toString())
  for (const [prior, expected] of [
    ['OPENAI_API_KEY=do-not-disclose\n', 'OPENAI_API_KEY=do-not-disclose\nPRIVATE_REALTIME_VOICE_QUALIFIED=true\n'],
    ['OPENAI_API_KEY=do-not-disclose', 'OPENAI_API_KEY=do-not-disclose\nPRIVATE_REALTIME_VOICE_QUALIFIED=true\n'],
    ['', 'PRIVATE_REALTIME_VOICE_QUALIFIED=true\n']
  ]) {
    const absent = privateRealtimeVoiceQualificationEnvPatch(Buffer.from(prior))
    assert.equal(absent.priorQualificationState, 'absent')
    assert.equal(absent.after.toString(), expected)
    assert.deepEqual(absent.after.subarray(0, absent.before.length), absent.before)
  }
  assert.equal(privateRealtimeVoiceQualificationEnvState('OTHER=value\n').state, 'absent')
  assert.equal(privateRealtimeVoiceQualificationEnvState('PRIVATE_REALTIME_VOICE_QUALIFIED=false\n').state, 'false')
  assert.equal(privateRealtimeVoiceQualificationEnvState('PRIVATE_REALTIME_VOICE_QUALIFIED=true\n').state, 'true')
  assert.throws(() => privateRealtimeVoiceQualificationEnvPatch('PRIVATE_REALTIME_VOICE_QUALIFIED=true\n'), /canonical.*false/)
  assert.throws(() => privateRealtimeVoiceQualificationEnvPatch(
    'PRIVATE_REALTIME_VOICE_QUALIFIED=false\n export PRIVATE_REALTIME_VOICE_QUALIFIED=false\n'), /absent or one canonical/)
  for (const invalid of [
    '# PRIVATE_REALTIME_VOICE_QUALIFIED=false\n',
    'export PRIVATE_REALTIME_VOICE_QUALIFIED=false\n',
    'PRIVATE_REALTIME_VOICE_QUALIFIED="false"\n',
    'PRIVATE_REALTIME_VOICE_QUALIFIED =false\n'
  ]) assert.throws(() => privateRealtimeVoiceQualificationEnvPatch(invalid), /absent or one canonical/)
  assert.throws(() => privateRealtimeVoiceQualificationEnvPatch(Buffer.from([0xff, 0xfe])), /valid UTF-8/)
})

test('qualification env transaction is byte-exact and idempotent on a real private filesystem', async t => {
  for (const [name, prior] of [
    ['absent prior', 'OPENAI_API_KEY=do-not-disclose\nOTHER=value\n'],
    ['canonical false prior', 'OPENAI_API_KEY=do-not-disclose\nPRIVATE_REALTIME_VOICE_QUALIFIED=false\nOTHER=value\n']
  ]) {
    await t.test(name, async t => {
      const fixture = await makeBaseEnvFilesystemFixture(t, prior)
      const policy = { backupRoot: fixture.backupRoot }
      const installed = await installTargetBaseEnvPatch(
        fixture.operationLock, fixture.plan, fixture.ownerUid, policy)
      assert.equal(installed.state, 'target_installed')
      assert.deepEqual(await readFile(fixture.plan.backupPath), fixture.prior)
      assert.equal(sha256(await readFile(fixture.baseEnv)), fixture.plan.afterSha256)
      assert.equal((await lstat(fixture.plan.backupPath)).mode & 0o777, 0o600)
      assert.equal((await lstat(fixture.plan.receiptPath)).mode & 0o777, 0o600)
      await assertTargetBaseEnvPatchInstalled(fixture.operationLock, fixture.plan, fixture.ownerUid, policy)

      // A crash after install but before the durable phase update may repeat
      // this exact effect. It must preserve one backup and one receipt.
      const repeatedInstall = await installTargetBaseEnvPatch(
        fixture.operationLock, fixture.plan, fixture.ownerUid, policy)
      assert.equal(repeatedInstall.state, 'target_installed')
      assert.deepEqual(await readFile(fixture.plan.backupPath), fixture.prior)

      const targetCommitted = await commitTargetBaseEnvPatch(
        fixture.operationLock, fixture.plan, fixture.ownerUid, policy)
      assert.equal(targetCommitted.state, 'target_committed')
      const redactedReceipt = await readFile(fixture.plan.receiptPath, 'utf8')
      assert.equal(redactedReceipt.includes('do-not-disclose'), false)
      assert.equal(redactedReceipt.includes('OPENAI_API_KEY'), false)

      const priorInstalled = await restorePriorBaseEnv(
        fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true })
      assert.equal(priorInstalled.state, 'prior_installed')
      assert.deepEqual(await readFile(fixture.baseEnv), fixture.prior)
      await assertPriorBaseEnvRestored(
        fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true })
      const priorCommitted = await commitPriorBaseEnvRestore(
        fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true })
      assert.equal(priorCommitted.state, 'prior_committed')

      // Failed explicit rollback recovery must be able to put the exact true
      // bytes back before restarting the qualified current release.
      const reinstalled = await reinstallCommittedTargetBaseEnvPatch(
        fixture.operationLock, fixture.plan, fixture.ownerUid, policy)
      assert.equal(reinstalled.state, 'target_installed')
      assert.equal(sha256(await readFile(fixture.baseEnv)), fixture.plan.afterSha256)
      assert.equal((await commitTargetBaseEnvPatch(
        fixture.operationLock, fixture.plan, fixture.ownerUid, policy)).state, 'target_committed')
    })
  }
})

test('transaction-bound base env temp is cleaned across pre- and post-rename crash states', async t => {
  const fixture = await makeBaseEnvFilesystemFixture(t, 'OPENAI_API_KEY=do-not-disclose\n')
  const policy = { backupRoot: fixture.backupRoot }
  const temporary = baseEnvPatchTemporaryPath(fixture.plan, fixture.backupRoot)
  assert.equal(temporary, join(dirname(fixture.baseEnv),
    `.${fixture.baseEnv.split('/').at(-1)}.bonfire-${fixture.operationLock.token}.tmp`))

  // SIGKILL after the bound temp is fsynced but before rename: the canonical
  // file is still prior, the exact backup exists, and the secret temp remains.
  await writeFile(fixture.plan.backupPath, fixture.prior, { mode: 0o600 })
  await chmod(fixture.plan.backupPath, 0o600)
  const targetBody = privateRealtimeVoiceQualificationEnvPatch(fixture.prior).after
  await writeFile(temporary, targetBody, { mode: 0o600 })
  await chmod(temporary, 0o600)
  await installTargetBaseEnvPatch(fixture.operationLock, fixture.plan, fixture.ownerUid, policy)
  assert.equal(sha256(await readFile(fixture.baseEnv)), fixture.plan.afterSha256)
  await assert.rejects(lstat(temporary), error => error?.code === 'ENOENT')

  // SIGKILL after rename but before receipt: canonical target and backup are
  // sufficient to recreate the exact receipt without another secret copy.
  await rm(fixture.plan.receiptPath)
  const resumed = await installTargetBaseEnvPatch(
    fixture.operationLock, fixture.plan, fixture.ownerUid, policy)
  assert.equal(resumed.state, 'target_installed')
  await assert.rejects(lstat(temporary), error => error?.code === 'ENOENT')
  await commitTargetBaseEnvPatch(fixture.operationLock, fixture.plan, fixture.ownerUid, policy)

  // The reverse write uses the same bound path and cleans its pre-rename crash
  // copy before restoring the exact prior bytes.
  await writeFile(temporary, fixture.prior, { mode: 0o600 })
  await chmod(temporary, 0o600)
  await restorePriorBaseEnv(
    fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true })
  assert.deepEqual(await readFile(fixture.baseEnv), fixture.prior)
  await assert.rejects(lstat(temporary), error => error?.code === 'ENOENT')
})

test('receiptless prior recovery removes an interrupted real backup before releasing the no-op boundary', async t => {
  const fixture = await makeBaseEnvFilesystemFixture(t, 'OTHER=value\n')
  const policy = { backupRoot: fixture.backupRoot }
  // SIGKILL after exclusive backup creation but before the full write/fsync
  // leaves the exact transaction-bound final path partial and receiptless.
  await writeFile(fixture.plan.backupPath, 'OTHER=partial-secret', { mode: 0o600 })
  await chmod(fixture.plan.backupPath, 0o600)
  await assert.rejects(installTargetBaseEnvPatch(
    fixture.operationLock, fixture.plan, fixture.ownerUid, policy), /backup differs/)
  assert.equal(await restorePriorBaseEnv(
    fixture.operationLock, fixture.plan, fixture.ownerUid, policy), null)
  assert.equal(await commitPriorBaseEnvRestore(
    fixture.operationLock, fixture.plan, fixture.ownerUid, policy), null)
  assert.deepEqual(await readdir(fixture.backupRoot), [])
  await assert.rejects(assertPriorBaseEnvRestored(
    fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true }), /bound receipt/)
  await assert.rejects(commitPriorBaseEnvRestore(
    fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true }), /bound receipt/)
})

test('explicit rollback cannot commit after its real receipt and backup are deleted', async t => {
  const fixture = await makeBaseEnvFilesystemFixture(t, 'OTHER=value\n')
  const policy = { backupRoot: fixture.backupRoot }
  await installTargetBaseEnvPatch(fixture.operationLock, fixture.plan, fixture.ownerUid, policy)
  await commitTargetBaseEnvPatch(fixture.operationLock, fixture.plan, fixture.ownerUid, policy)
  await restorePriorBaseEnv(
    fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true })
  await rm(fixture.plan.receiptPath)
  await rm(fixture.plan.backupPath)
  assert.deepEqual(await readFile(fixture.baseEnv), fixture.prior)
  await assert.rejects(assertPriorBaseEnvRestored(
    fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true }), /bound receipt/)
  await assert.rejects(commitPriorBaseEnvRestore(
    fixture.operationLock, fixture.plan, fixture.ownerUid, { ...policy, requireReceipt: true }), /bound receipt/)
})

test('real filesystem env drift cannot create a backup, receipt, or target install', async t => {
  const fixture = await makeBaseEnvFilesystemFixture(t, 'OTHER=value\n')
  const policy = { backupRoot: fixture.backupRoot }
  await writeFile(fixture.baseEnv, 'OTHER=drifted\n', { mode: 0o600 })
  await assert.rejects(installTargetBaseEnvPatch(
    fixture.operationLock, fixture.plan, fixture.ownerUid, policy), /drifted outside/)
  assert.deepEqual(await readdir(fixture.backupRoot), [])
  assert.equal((await readFile(fixture.baseEnv, 'utf8')), 'OTHER=drifted\n')
})

test('qualification plan and redacted receipt bind both releases, generation, and only digests', () => {
  const plan = validateBaseEnvPatchPlan(makeBaseEnvPatchPlan())
  const receipt = validateBaseEnvPatchReceipt(makeBaseEnvPatchReceipt(plan), plan)
  assert.equal(receipt.rollbackReleaseCommit, 'b'.repeat(40))
  assert.equal(receipt.targetLedgerGeneration, 2)
  assert.deepEqual(baseEnvPatchPlanFromReceipt(plan.receiptPath, receipt), plan)
  assert.deepEqual(Object.keys(receipt).sort(), [
    'afterSha256', 'backupPath', 'baseEnvPath', 'beforeSha256', 'committedAt', 'patchKey', 'priorQualificationState', 'priorRestoredAt',
    'rollbackReleaseCommit', 'schema', 'state', 'targetLedgerGeneration', 'targetObservedAt', 'targetReleaseCommit', 'transactionToken'
  ].sort())
  assert.equal(JSON.stringify(receipt).includes('do-not-disclose'), false)
  assert.throws(() => validateBaseEnvPatchPlan(makeBaseEnvPatchPlan({ backupDir: '/tmp/attacker',
    backupPath: '/tmp/attacker/base-env.before.env', receiptPath: '/tmp/attacker/base-env.receipt.json' })), /invalid/)
  assert.throws(() => validateBaseEnvPatchPlan(makeBaseEnvPatchPlan({ backupDir: '/opt/meetingassist-backups/a/b',
    backupPath: '/opt/meetingassist-backups/a/b/base-env.before.env',
    receiptPath: '/opt/meetingassist-backups/a/b/base-env.receipt.json' })), /invalid/)
})

test('qualification patch fails closed on digest drift and private path ownership or modes', () => {
  const plan = validateBaseEnvPatchPlan(makeBaseEnvPatchPlan())
  assert.equal(baseEnvPatchDigestState(plan.beforeSha256, plan), 'prior')
  assert.equal(baseEnvPatchDigestState(plan.afterSha256, plan), 'target')
  assert.throws(() => baseEnvPatchDigestState(digest('9'), plan), /drifted outside/)
  const stat = ({ file = false, directory = false, symlink = false, mode = 0, uid = 0 }) => ({
    isFile: () => file, isDirectory: () => directory, isSymbolicLink: () => symlink, mode, uid
  })
  assert.doesNotThrow(() => validatePrivateReleasePathInfo(stat({ file: true, mode: 0o600 }), 'base env'))
  assert.doesNotThrow(() => validatePrivateReleasePathInfo(stat({ directory: true, mode: 0o700 }), 'base env backup directory'))
  assert.throws(() => validatePrivateReleasePathInfo(stat({ file: true, mode: 0o640 }), 'base env backup'), /owner-private/)
  assert.throws(() => validatePrivateReleasePathInfo(stat({ directory: true, mode: 0o755 }), 'base env backup directory'), /owner-private/)
  assert.throws(() => validatePrivateReleasePathInfo(stat({ directory: true, symlink: true, mode: 0o700 }), 'base env backup directory'), /owner-private/)
  assert.throws(() => validatePrivateReleasePathInfo(stat({ file: true, mode: 0o600, uid: 501 }), 'base env'), /owner-private/)
})

test('actual container qualification value is required for target and retained rollback verification', () => {
  const plan = validateBaseEnvPatchPlan(makeBaseEnvPatchPlan())
  const absentPlan = validateBaseEnvPatchPlan(makeBaseEnvPatchPlan({ priorQualificationState: 'absent' }))
  assert.equal(privateRealtimeVoiceQualificationRuntimeState({}), 'absent')
  assert.equal(privateRealtimeVoiceQualificationRuntimeState({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'false' }), 'false')
  assert.equal(privateRealtimeVoiceQualificationRuntimeState({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'true' }), 'true')
  assert.throws(() => privateRealtimeVoiceQualificationRuntimeState({ PRIVATE_REALTIME_VOICE_QUALIFIED: ' TRUE ' }), /malformed/)
  assert.equal(assertPrivateRealtimeVoiceQualificationHostRuntimeMatch('absent', 'absent'), 'absent')
  assert.equal(assertPrivateRealtimeVoiceQualificationHostRuntimeMatch('true', 'true'), 'true')
  assert.throws(() => assertPrivateRealtimeVoiceQualificationHostRuntimeMatch('absent', 'true'), /currently serving/)
  assert.doesNotThrow(() => verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'true' }, plan, 'target'))
  assert.doesNotThrow(() => verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'false' }, plan, 'prior'))
  assert.doesNotThrow(() => verifyBaseEnvPatchRuntimeEnvironment({}, absentPlan, 'prior'))
  assert.throws(() => verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'false' }, plan, 'target'), /target base env/)
  assert.throws(() => verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'true' }, plan, 'prior'), /prior base env/)
  assert.throws(() => verifyBaseEnvPatchRuntimeEnvironment({}, plan, 'target'), /target base env/)
  assert.throws(() => verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'false' }, absentPlan, 'prior'), /prior base env/)
})

test('abrupt death resumes every fenced phase without opening ingress before durable ledger', async () => {
  const transition = { activateTargetBeforeStart: true, verifyTargetRuntimeBeforeStart: false }
  const terminalOrder = ['stop-ingress', 'install-target-env', 'preflight-target', 'activate', 'start-private', 'verify-private', 'write-ledger', 'open-ingress', 'verify-external']
  for (const crashPhase of ['ingress_stopped', 'base_env_patch_started', 'base_env_patched', 'target_preflighted', 'data_transition_started', 'data_ready', 'private_started',
    'private_verified', 'ledger_written', 'ingress_opened']) {
    let durablePhase = 'prepared'
    const calls = []
    let crashed = false
    const effects = {
      stopIngress: async () => calls.push('stop-ingress'), installTargetBaseEnv: async () => calls.push('install-target-env'),
      assertTargetBaseEnv: async () => {}, preflightTarget: async () => calls.push('preflight-target'),
      activateTarget: async () => calls.push('activate'),
      verifyTargetRuntime: async () => calls.push('verify-runtime'), startTargetPrivate: async () => calls.push('start-private'),
      verifyTargetPrivate: async () => calls.push('verify-private'), writeTargetLedger: async () => calls.push('write-ledger'),
      openTargetIngress: async () => calls.push('open-ingress'), verifyTargetExternal: async () => calls.push('verify-external')
    }
    await assert.rejects(executeDurableReleasePhaseMachine({ phase: durablePhase, transition, effects, advance: async phase => {
      durablePhase = phase
      if (!crashed && phase === crashPhase) { crashed = true; throw new Error('abrupt death') }
    } }), /abrupt death/)
    await executeDurableReleasePhaseMachine({ phase: durablePhase, transition, effects, advance: async phase => { durablePhase = phase } })
    assert.equal(durablePhase, 'external_verified')
    assert.ok(calls.indexOf('stop-ingress') < calls.indexOf('activate'))
    assert.ok(calls.indexOf('install-target-env') < calls.indexOf('preflight-target'))
    assert.ok(calls.indexOf('preflight-target') < calls.indexOf('activate'))
    assert.ok(calls.indexOf('write-ledger') < calls.indexOf('open-ingress'))
    assert.deepEqual([...new Set(calls)], terminalOrder)
  }
})

test('transactional qualification succeeds only after target preflight, container proof, ledger CAS, and external proof', async () => {
  const plan = validateBaseEnvPatchPlan(makeBaseEnvPatchPlan())
  const events = []
  let environmentState = 'prior'
  let ledgerState = 'prior'
  let phase = 'prepared'
  await executeDurableReleasePhaseMachine({ phase,
    transition: { activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: false },
    advance: async next => { phase = next }, effects: {
      stopIngress: async () => events.push('stop-ingress'),
      installTargetBaseEnv: async () => { environmentState = 'target'; events.push('install-target-env') },
      assertTargetBaseEnv: async () => assert.equal(environmentState, 'target'),
      preflightTarget: async () => { assert.equal(environmentState, 'target'); events.push('preflight-target') },
      activateTarget: async () => assert.fail('activation transition is not required'),
      verifyTargetRuntime: async () => assert.fail('runtime lineage is not required'),
      startTargetPrivate: async () => { assert.equal(environmentState, 'target'); events.push('start-target') },
      verifyTargetPrivate: async () => {
        verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'true' }, plan, 'target')
        events.push('verify-target-private')
      },
      writeTargetLedger: async () => { ledgerState = 'target'; events.push('ledger-cas') },
      openTargetIngress: async () => { assert.equal(ledgerState, 'target'); events.push('open-target-ingress') },
      verifyTargetExternal: async () => {
        assert.equal(ledgerState, 'target')
        verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'true' }, plan, 'target')
        events.push('verify-target-external')
      }
    } })
  assert.equal(phase, 'external_verified')
  events.push('commit-redacted-receipt')
  assert.deepEqual(events, ['stop-ingress', 'install-target-env', 'preflight-target', 'start-target', 'verify-target-private',
    'ledger-cas', 'open-target-ingress', 'verify-target-external', 'commit-redacted-receipt'])
})

test('explicit qualification rollback restores exact prior state before target preflight and commits only after external proof', async () => {
  const plan = validateBaseEnvPatchPlan(makeBaseEnvPatchPlan({ priorQualificationState: 'absent' }))
  let environmentState = 'target'
  let receiptState = 'target_committed'
  let ledgerState = 'qualified-current'
  let phase = 'prepared'
  const events = []
  await executeDurableReleasePhaseMachine({ phase,
    transition: { activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: false },
    advance: async next => { phase = next }, effects: {
      stopIngress: async () => { assert.equal(environmentState, 'target'); events.push('stop-qualified-ingress') },
      installTargetBaseEnv: async () => { environmentState = 'prior'; receiptState = 'prior_installed'; events.push('restore-exact-prior-env') },
      assertTargetBaseEnv: async () => assert.equal(environmentState, 'prior'),
      preflightTarget: async () => { assert.equal(environmentState, 'prior'); events.push('preflight-bootstrap') },
      activateTarget: async () => assert.fail('unexpected data activation'),
      verifyTargetRuntime: async () => assert.fail('unexpected runtime verification'),
      startTargetPrivate: async () => { assert.equal(environmentState, 'prior'); events.push('start-bootstrap') },
      verifyTargetPrivate: async () => {
        verifyBaseEnvPatchRuntimeEnvironment({}, plan, 'prior')
        events.push('verify-bootstrap-private')
      },
      writeTargetLedger: async () => { ledgerState = 'bootstrap-target'; events.push('rollback-ledger-cas') },
      openTargetIngress: async () => { assert.equal(ledgerState, 'bootstrap-target'); events.push('open-bootstrap-ingress') },
      verifyTargetExternal: async () => { assert.equal(environmentState, 'prior'); events.push('verify-bootstrap-external') }
    } })
  assert.equal(phase, 'external_verified')
  assert.equal(receiptState, 'prior_installed')
  receiptState = 'prior_committed'
  events.push('commit-prior-receipt')
  assert.deepEqual(events, ['stop-qualified-ingress', 'restore-exact-prior-env', 'preflight-bootstrap', 'start-bootstrap',
    'verify-bootstrap-private', 'rollback-ledger-cas', 'open-bootstrap-ingress', 'verify-bootstrap-external', 'commit-prior-receipt'])
})

test('failed qualification rollback reinstalls true before restarting the qualified current release', async () => {
  const plan = validateBaseEnvPatchPlan(makeBaseEnvPatchPlan({ priorQualificationState: 'absent' }))
  let environmentState = 'prior'
  let receiptState = 'prior_installed'
  let phase = 'recovery_started'
  const events = []
  await executeDurableReleaseRecoveryPhaseMachine({ phase, advance: async next => { phase = next }, effects: {
    restoreTargetData: async () => { assert.equal(environmentState, 'prior'); events.push('undo-bootstrap-transition') },
    restoreRecoveryBaseEnv: async () => { environmentState = 'target'; receiptState = 'target_installed'; events.push('reinstall-exact-true-env') },
    verifyRollbackRuntime: async () => { assert.equal(environmentState, 'target'); events.push('preflight-qualified-current') },
    startRollbackPrivate: async () => { assert.equal(environmentState, 'target'); events.push('restart-qualified-current') },
    verifyRollbackPrivate: async () => {
      verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'true' }, plan, 'target')
      events.push('verify-qualified-private')
    },
    restoreLedger: async () => events.push('restore-qualified-ledger'),
    openRollbackIngress: async () => { assert.equal(environmentState, 'target'); events.push('open-qualified-ingress') },
    verifyRollbackExternal: async () => { assert.equal(environmentState, 'target'); events.push('verify-qualified-external') }
  } })
  assert.equal(phase, 'recovery_external_verified')
  receiptState = 'target_committed'
  events.push('recommit-target-receipt')
  assert.deepEqual(events.slice(0, 4), ['undo-bootstrap-transition', 'reinstall-exact-true-env', 'preflight-qualified-current', 'restart-qualified-current'])
  assert.equal(receiptState, 'target_committed')
})

test('explicit qualification rollback resumes every forward env-transition crash window', async () => {
  const crashPhases = ['ingress_stopped', 'base_env_patch_started', 'base_env_patched', 'target_preflighted', 'data_ready', 'private_started',
    'private_verified', 'ledger_written', 'ingress_opened']
  for (const crashPhase of crashPhases) {
    let phase = 'prepared'
    let environmentState = 'target'
    let crashed = false
    const calls = []
    const effects = {
      stopIngress: async () => { assert.equal(environmentState, 'target'); calls.push('stop') },
      installTargetBaseEnv: async () => { environmentState = 'prior'; calls.push('restore-prior') },
      assertTargetBaseEnv: async () => assert.equal(environmentState, 'prior'),
      preflightTarget: async () => { assert.equal(environmentState, 'prior'); calls.push('preflight') },
      activateTarget: async () => assert.fail('unexpected'), verifyTargetRuntime: async () => assert.fail('unexpected'),
      startTargetPrivate: async () => { assert.equal(environmentState, 'prior'); calls.push('start') },
      verifyTargetPrivate: async () => calls.push('verify-private'), writeTargetLedger: async () => calls.push('ledger'),
      openTargetIngress: async () => calls.push('open'), verifyTargetExternal: async () => calls.push('verify-external')
    }
    await assert.rejects(executeDurableReleasePhaseMachine({ phase,
      transition: { activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: false }, effects, advance: async next => {
        phase = next
        if (!crashed && next === crashPhase) { crashed = true; throw new Error('abrupt rollback death') }
      } }), /abrupt rollback death/)
    await executeDurableReleasePhaseMachine({ phase,
      transition: { activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: false }, effects,
      advance: async next => { phase = next } })
    assert.equal(phase, 'external_verified')
    assert.ok(calls.indexOf('restore-prior') < calls.indexOf('preflight'))
    assert.ok(calls.indexOf('preflight') < calls.indexOf('start'))
  }
})

test('failed qualification rollback recovery resumes every true-env reinstall crash window', async () => {
  const crashPhases = ['recovery_data_restored', 'recovery_env_restore_started', 'recovery_env_restored',
    'recovery_runtime_verified', 'recovery_private_started', 'recovery_private_verified', 'recovery_ledger_restored', 'recovery_ingress_opened']
  for (const crashPhase of crashPhases) {
    let phase = 'recovery_started'
    let environmentState = 'prior'
    let crashed = false
    const calls = []
    const effects = {
      restoreTargetData: async () => { assert.equal(environmentState, 'prior'); calls.push('undo-target') },
      restoreRecoveryBaseEnv: async () => { environmentState = 'target'; calls.push('reinstall-true') },
      verifyRollbackRuntime: async () => { assert.equal(environmentState, 'target'); calls.push('preflight-current') },
      startRollbackPrivate: async () => { assert.equal(environmentState, 'target'); calls.push('start-current') },
      verifyRollbackPrivate: async () => calls.push('verify-current'), restoreLedger: async () => calls.push('ledger'),
      openRollbackIngress: async () => calls.push('open'), verifyRollbackExternal: async () => calls.push('external')
    }
    await assert.rejects(executeDurableReleaseRecoveryPhaseMachine({ phase, effects, advance: async next => {
      phase = next
      if (!crashed && next === crashPhase) { crashed = true; throw new Error('abrupt rollback recovery death') }
    } }), /abrupt rollback recovery death/)
    await executeDurableReleaseRecoveryPhaseMachine({ phase, effects, advance: async next => { phase = next } })
    assert.equal(phase, 'recovery_external_verified')
    assert.ok(calls.indexOf('reinstall-true') < calls.indexOf('preflight-current'))
    assert.ok(calls.indexOf('preflight-current') < calls.indexOf('start-current'))
  }
})

test('pre-target patch failure starts neither target nor retained release until prior env restore is durable', async () => {
  assert.equal(priorBaseEnvCommitDisposition(null), 'skip')
  assert.equal(priorBaseEnvCommitDisposition(makeBaseEnvPatchReceipt(makeBaseEnvPatchPlan(), {
    state: 'prior_installed', priorRestoredAt: '2026-08-22T12:02:00.000Z'
  })), 'commit')
  assert.throws(() => priorBaseEnvCommitDisposition(makeBaseEnvPatchReceipt()), /not commit-ready/)
  const forwardCalls = []
  let phase = 'prepared'
  await assert.rejects(executeDurableReleasePhaseMachine({ phase,
    transition: { activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: false },
    advance: async next => { phase = next }, effects: {
      stopIngress: async () => forwardCalls.push('stop-ingress'),
      installTargetBaseEnv: async () => { forwardCalls.push('install-target-env'); throw new Error('base env digest drift') },
      assertTargetBaseEnv: async () => forwardCalls.push('unexpected-assert'),
      preflightTarget: async () => forwardCalls.push('unexpected-preflight'),
      activateTarget: async () => forwardCalls.push('unexpected-activate'),
      verifyTargetRuntime: async () => forwardCalls.push('unexpected-runtime'),
      startTargetPrivate: async () => forwardCalls.push('unexpected-target-start'),
      verifyTargetPrivate: async () => forwardCalls.push('unexpected-target-verify'),
      writeTargetLedger: async () => forwardCalls.push('unexpected-ledger'),
      openTargetIngress: async () => forwardCalls.push('unexpected-open'),
      verifyTargetExternal: async () => forwardCalls.push('unexpected-external')
    } }), /base env digest drift/)
  assert.equal(phase, 'base_env_patch_started')
  assert.deepEqual(forwardCalls, ['stop-ingress', 'install-target-env'])

  const recoveryCalls = []
  let recoveryPhase = 'recovery_started'
  let priorRestored = false
  await executeDurableReleaseRecoveryPhaseMachine({ phase: recoveryPhase, advance: async next => { recoveryPhase = next }, effects: {
    restoreTargetData: async () => recoveryCalls.push('cleanup-target'),
    restoreRecoveryBaseEnv: async () => { priorRestored = true; recoveryCalls.push('restore-prior-env') },
    verifyRollbackRuntime: async () => { assert.equal(priorRestored, true); recoveryCalls.push('preflight-retained') },
    startRollbackPrivate: async () => { assert.equal(priorRestored, true); recoveryCalls.push('start-retained') },
    verifyRollbackPrivate: async () => recoveryCalls.push('verify-retained-private'),
    restoreLedger: async () => recoveryCalls.push('restore-ledger'),
    openRollbackIngress: async () => recoveryCalls.push('open-retained-ingress'),
    verifyRollbackExternal: async () => recoveryCalls.push('verify-retained-external')
  } })
  assert.ok(recoveryCalls.indexOf('restore-prior-env') < recoveryCalls.indexOf('preflight-retained'))
  assert.ok(recoveryCalls.indexOf('restore-prior-env') < recoveryCalls.indexOf('start-retained'))
})

test('post-target failure restores target data first, then prior env before every retained verifier or start', async () => {
  let environmentState = 'target'
  const calls = []
  let recoveryPhase = 'recovery_started'
  await executeDurableReleaseRecoveryPhaseMachine({ phase: recoveryPhase, advance: async next => { recoveryPhase = next }, effects: {
    restoreTargetData: async () => { assert.equal(environmentState, 'target'); calls.push('undo-target-data') },
    restoreRecoveryBaseEnv: async () => { environmentState = 'prior'; calls.push('restore-prior-env') },
    verifyRollbackRuntime: async () => { assert.equal(environmentState, 'prior'); calls.push('preflight-retained') },
    startRollbackPrivate: async () => { assert.equal(environmentState, 'prior'); calls.push('start-retained') },
    verifyRollbackPrivate: async () => {
      verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'false' }, makeBaseEnvPatchPlan(), 'prior')
      calls.push('verify-retained-private')
    },
    restoreLedger: async () => calls.push('restore-ledger'),
    openRollbackIngress: async () => { assert.equal(environmentState, 'prior'); calls.push('open-retained-ingress') },
    verifyRollbackExternal: async () => {
      verifyBaseEnvPatchRuntimeEnvironment({ PRIVATE_REALTIME_VOICE_QUALIFIED: 'false' }, makeBaseEnvPatchPlan(), 'prior')
      calls.push('verify-retained-external')
    }
  } })
  assert.equal(recoveryPhase, 'recovery_external_verified')
  assert.deepEqual(calls.slice(0, 4), ['undo-target-data', 'restore-prior-env', 'preflight-retained', 'start-retained'])
})

test('abrupt death resumes every recovery phase without starting retained services before prior env restoration', async () => {
  const crashPhases = ['recovery_data_restored', 'recovery_env_restore_started', 'recovery_env_restored', 'recovery_runtime_verified',
    'recovery_private_started', 'recovery_private_verified', 'recovery_ledger_restored', 'recovery_ingress_opened']
  for (const crashPhase of crashPhases) {
    let phase = 'recovery_started'
    let crashed = false
    let priorRestored = false
    const calls = []
    const effects = {
      restoreTargetData: async () => calls.push('undo-target-data'),
      restoreRecoveryBaseEnv: async () => { priorRestored = true; calls.push('restore-prior-env') },
      verifyRollbackRuntime: async () => { assert.equal(priorRestored, true); calls.push('preflight-retained') },
      startRollbackPrivate: async () => { assert.equal(priorRestored, true); calls.push('start-retained') },
      verifyRollbackPrivate: async () => { assert.equal(priorRestored, true); calls.push('verify-retained-private') },
      restoreLedger: async () => calls.push('restore-ledger'),
      openRollbackIngress: async () => { assert.equal(priorRestored, true); calls.push('open-retained-ingress') },
      verifyRollbackExternal: async () => { assert.equal(priorRestored, true); calls.push('verify-retained-external') }
    }
    await assert.rejects(executeDurableReleaseRecoveryPhaseMachine({ phase, effects, advance: async next => {
      phase = next
      if (!crashed && next === crashPhase) { crashed = true; throw new Error('abrupt death') }
    } }), /abrupt death/)
    if (phase === 'recovery_env_restored' || crashPhases.indexOf(phase) > crashPhases.indexOf('recovery_env_restored')) priorRestored = true
    await executeDurableReleaseRecoveryPhaseMachine({ phase, effects, advance: async next => { phase = next } })
    assert.equal(phase, 'recovery_external_verified')
    assert.ok(calls.indexOf('restore-prior-env') < calls.indexOf('start-retained') ||
      (crashPhase === 'recovery_env_restored' && calls.indexOf('start-retained') >= 0))
  }
})

test('legacy no-patch activation and rollback remain resumable with absent or false qualification', async () => {
  const calls = []
  let phase = 'prepared'
  await executeDurableReleasePhaseMachine({ phase,
    transition: { activateTargetBeforeStart: false, verifyTargetRuntimeBeforeStart: false },
    advance: async next => { phase = next }, effects: {
      stopIngress: async () => calls.push('stop'), installTargetBaseEnv: async () => calls.push('no-patch'),
      assertTargetBaseEnv: async () => {}, preflightTarget: async () => calls.push('preflight'),
      activateTarget: async () => assert.fail('unexpected'), verifyTargetRuntime: async () => assert.fail('unexpected'),
      startTargetPrivate: async () => calls.push('start'), verifyTargetPrivate: async () => calls.push('verify-private'),
      writeTargetLedger: async () => calls.push('ledger'), openTargetIngress: async () => calls.push('open'),
      verifyTargetExternal: async () => calls.push('verify-external')
    } })
  assert.equal(phase, 'external_verified')
  assert.deepEqual(calls, ['stop', 'no-patch', 'preflight', 'start', 'verify-private', 'ledger', 'open', 'verify-external'])
})

test('explicit live to canary phase machine preserves evolved bytes and uses lineage only', async () => {
  const evolved = Buffer.from('v2-snapshot-and-sessions-evolved-after-activation')
  const before = Buffer.from(evolved)
  const calls = []
  await executeDurableReleasePhaseMachine({ phase: 'prepared',
    transition: strideE10W4ReleaseTransitionPlan('rolledBack', makeReceipt(), makeReceipt(w4LivePolicy)),
    advance: async () => {}, effects: {
      stopIngress: async () => calls.push('stop-ingress'), installTargetBaseEnv: async () => {},
      assertTargetBaseEnv: async () => {}, preflightTarget: async () => calls.push('preflight-canary'),
      activateTarget: async () => { evolved.fill(0); calls.push('MUTATED') },
      verifyTargetRuntime: async () => calls.push('verify-runtime-read-only'), startTargetPrivate: async () => calls.push('start-canary-private'),
      verifyTargetPrivate: async () => calls.push('verify-canary-private'), writeTargetLedger: async () => calls.push('write-ledger'),
      openTargetIngress: async () => calls.push('open-canary-ingress'), verifyTargetExternal: async () => calls.push('verify-canary-external')
    } })
  assert.deepEqual(evolved, before)
  assert.equal(calls.includes('MUTATED'), false)
  assert.deepEqual(calls.slice(0, 4), ['stop-ingress', 'preflight-canary', 'verify-runtime-read-only', 'start-canary-private'])
})

test('post-ingress initial activation failure preserves public evolved bytes and restores canary read-only', () => {
  const initial = strideE10W4ReleaseTransitionPlan('activated', makeReceipt(w4LivePolicy), makeReceipt())
  assert.deepEqual(strideE10W4RecoveryPlan('data_transition_started', initial), {
    rollbackUnexposedInitialActivation: true, verifyRetainedRuntimeWithoutMutation: false, preserveEvolvedBytes: false
  })
  assert.deepEqual(strideE10W4RecoveryPlan('private_verified', initial), {
    rollbackUnexposedInitialActivation: false, verifyRetainedRuntimeWithoutMutation: true, preserveEvolvedBytes: true
  })
  assert.deepEqual(strideE10W4RecoveryPlan('ingress_opened', initial), {
    rollbackUnexposedInitialActivation: false, verifyRetainedRuntimeWithoutMutation: true, preserveEvolvedBytes: true
  })
  assert.deepEqual(strideE10W4RecoveryPlan('external_verified', initial), {
    rollbackUnexposedInitialActivation: false, verifyRetainedRuntimeWithoutMutation: true, preserveEvolvedBytes: true
  })
  const publicBytes = Buffer.from('governed-write-after-caddy-open')
  const before = Buffer.from(publicBytes)
  const plan = strideE10W4RecoveryPlan('ingress_opened', initial)
  if (plan.rollbackUnexposedInitialActivation) publicBytes.fill(0)
  assert.deepEqual(publicBytes, before)
})

test('activation progress identifies the candidate and elapsed bounded wait', () => {
  assert.deepEqual(releaseActivationProgress(releaseCommit, 'waiting_for_ready', 1_000, 17_900), {
    schema: 'bonfire.release-activation-progress.v1',
    phase: 'candidate_startup',
    state: 'waiting_for_ready',
    releaseCommit,
    elapsedSeconds: 16
  })
})

test('activation refuses to mutate without an exact retained rollback bundle', async () => {
  await assert.rejects(execFileAsync(process.execPath, [releaseToolPath, 'activate',
    '--release-dir', '/opt/meetingassist-releases/target',
    '--base-env', '/opt/meetingassist/deploy/digitalocean/.env',
    '--health-url', 'https://thebonfire.xyz/healthz',
    '--ready-url', 'https://thebonfire.xyz/readyz']), /--rollback-release-dir is required/)
})

test('activation ignores exported selectors and only adds the explicit base env path', () => {
  const environment = releaseComposeEnvironment({
    PATH: '/usr/bin', HOME: '/root', DOCKER_HOST: 'ssh://release-host',
    BONFIRE_MEETINGASSIST_IMAGE: `sha256:${digest('a')}`, BONFIRE_RENDER_IMAGE: `sha256:${digest('b')}`,
    BONFIRE_RELEASE_COMMIT: '2'.repeat(40), COMPOSE_FILE: '/tmp/attacker-compose.yml', COMPOSE_PROJECT_NAME: 'attacker'
  }, '/opt/meetingassist/deploy/digitalocean/.env')
  assert.deepEqual(environment, {
    PATH: '/usr/bin', HOME: '/root', DOCKER_HOST: 'ssh://release-host',
    BONFIRE_BASE_ENV_FILE: '/opt/meetingassist/deploy/digitalocean/.env'
  })
})
