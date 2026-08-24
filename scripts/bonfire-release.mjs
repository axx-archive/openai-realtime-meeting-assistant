#!/usr/bin/env node

// Exact, unsigned release receipts for the whole Docker Compose deployment.
// Source, images, sidecars, and candidate configuration are locally bound.
// Independent signing, registry custody, and off-host attestation remain gates.

import { execFile, spawn } from 'node:child_process'
import { createHash, randomUUID } from 'node:crypto'
import { chmod, lstat, mkdir, mkdtemp, open, readFile, readdir, readlink, rename, rm, unlink } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, dirname, isAbsolute, join, relative, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { promisify } from 'node:util'

const execFileAsync = promisify(execFile)
const schema = 'bonfire.release-identity.v1'
const sourceSchema = 'bonfire.release-source.v3'
const scopeSchema = 'bonfire.release-scope-policy.v1'
const buildInputsSchema = 'bonfire.release-build-inputs.v2'
const buildManifestSchema = 'bonfire.release-build-manifest.v2'
const receiptSchema = 'bonfire.release-receipt.v3'
const receiptSchemaW4 = 'bonfire.release-receipt.v4'
const candidateBundleSchema = 'bonfire.candidate-deployment-bundle.v1'
const activeReleaseLedgerSchema = 'bonfire.active-release-ledger.v1'
const releaseOperationLockSchema = 'bonfire.release-operation-lock.v1'
const releaseTransactionSchema = 'bonfire.release-transaction.v2'
const baseEnvPatchSchema = 'bonfire.base-env-patch.v1'
const baseEnvPatchReceiptSchema = 'bonfire.base-env-patch-receipt.v1'
const privateRealtimeVoiceQualificationKey = 'PRIVATE_REALTIME_VOICE_QUALIFIED'
const privateRealtimeVoiceQualificationValue = 'true'
const baseEnvPatchBackupRoot = '/opt/meetingassist-backups'
const shaPattern = /^[0-9a-f]{64}$/
const commitPattern = /^[0-9a-f]{40}(?:[0-9a-f]{24})?$/
const imageRefPattern = /^.+@sha256:[0-9a-f]{64}$/
const localImageIdPattern = /^sha256:[0-9a-f]{64}$/
const scopePolicyPath = 'deploy/digitalocean/release-scope-policy.json'
const strideE10W4DeploymentPolicyPath = 'deploy/digitalocean/stride-e10-w4-deployment-policy.json'
const strideE10W4DeploymentPolicySchema = 'bonfire.stride-e10-w4-deployment-policy.v1'
const strideE10W4CanaryMode = 'canary'
const strideE10W4NetworkMode = 'bonfire_network_live'
const requiredExcludedPrefixes = ['stride-site/', 'data/', 'docs/evidence/']
const requiredConfigPaths = [
  '.dockerignore', 'Dockerfile', 'Dockerfile.render', 'go.mod', 'go.sum',
  'deploy/digitalocean/docker-compose.yml', 'deploy/digitalocean/Caddyfile',
  'deploy/digitalocean/bonfire-render-runner-v1.apparmor',
  'deploy/digitalocean/bonfire-render-runner-v1.seccomp.json',
  'deploy/digitalocean/release-build-inputs.json', scopePolicyPath,
  'scripts/bonfire-release.mjs'
]
const serviceRoles = {
  meetingassist: 'meetingassist',
  'render-runner': 'renderRunner',
  'render-queue-init': 'renderRunner',
  'canonical-postgres': 'canonicalPostgres',
  coturn: 'coturn',
  caddy: 'caddy'
}
const expectedServiceNames = Object.keys(serviceRoles).sort()
const expectedProjectNetworks = {
  default: { name: 'digitalocean_default', internal: false },
  render_internal: { name: 'digitalocean_render_internal', internal: true }
}
const expectedProjectVolumes = {
  caddy_data: { name: 'digitalocean_caddy_data', external: false },
  caddy_config: { name: 'digitalocean_caddy_config', external: false },
  codex_queue: { name: 'digitalocean_codex_queue', external: true },
  render_queue: { name: 'digitalocean_render_queue', external: false },
  meeting_data: { name: 'digitalocean_meeting_data', external: false },
  usage_ledger: { name: 'digitalocean_usage_ledger', external: true },
  canonical_postgres: { name: 'digitalocean_canonical_postgres', external: true }
}
const expectedProjectNetworkNames = Object.values(expectedProjectNetworks).map(value => value.name).sort()
const expectedProjectVolumeNames = Object.values(expectedProjectVolumes).map(value => value.name).sort()
const rendererAppArmorProfile = 'bonfire-render-runner-v1'
const rendererSeccompProfile = '/etc/docker/seccomp/bonfire-render-runner-v1.json'
const rendererSeccompSourcePath = 'deploy/digitalocean/bonfire-render-runner-v1.seccomp.json'
const rendererSecurityOptions = [
  `apparmor=${rendererAppArmorProfile}`,
  'no-new-privileges:true',
  `seccomp=${rendererSeccompProfile}`
]

export function computeEnvironmentMarker(value) {
  const fields = [schema, value.releaseCommit, value.gitTreeDigest, value.sourceArchiveSha256,
    value.transitiveInputsSha256, value.buildConfigSha256, value.buildInputManifestSha256,
    value.buildManifestSha256, value.binarySha256, value.imageDigest]
  return sha256(`${fields.join('\n')}\n`)
}

export function computeBundleSha256(value) {
  const identity = {
    schema: 'bonfire.whole-deployment-identity.v1',
    releaseCommit: value.source.releaseCommit,
    sourceReceiptSha256: value.sourceReceiptSha256,
    buildManifestSha256: value.buildManifestSha256,
    buildConfigSha256: value.source.buildConfigSha256,
    candidateBundleManifestSha256: value.candidateBundleManifestSha256,
    images: Object.fromEntries(Object.entries(value.images).map(([name, image]) => [name, {
      imageId: image.imageId, binarySha256: image.binarySha256
    }])),
    sidecars: Object.fromEntries(Object.entries(value.sidecars).map(([name, image]) => [name, {
      imageReference: image.imageReference, imageId: image.imageId
    }]))
  }
  if (value.strideE10W4) identity.strideE10W4 = value.strideE10W4
  return sha256(canonical(identity))
}

export function validateStrideE10W4DeploymentPolicy(policy) {
  const keys = Object.keys(policy || {}).sort()
  const expectedKeys = ['activationBackupDir', 'activationReceiptPath', 'liveMode', 'releaseMode', 'schema', 'snapshotPath'].sort()
  if (policy?.schema !== strideE10W4DeploymentPolicySchema || JSON.stringify(keys) !== JSON.stringify(expectedKeys) ||
      policy.liveMode !== strideE10W4NetworkMode || ![strideE10W4CanaryMode, strideE10W4NetworkMode].includes(policy.releaseMode)) {
    throw new Error('STRIDE E10 W4 deployment policy is invalid')
  }
  const paths = [policy.snapshotPath, policy.activationBackupDir, policy.activationReceiptPath]
  if (paths.some(path => !isAbsolute(String(path || '')) || resolve(path) !== path || !path.startsWith('/app/data/')) ||
      new Set(paths).size !== paths.length || paths.some((path, index) => paths.some((other, otherIndex) => index !== otherIndex && other.startsWith(`${path}/`)))) {
    throw new Error('STRIDE E10 W4 deployment policy paths are invalid')
  }
  return policy
}

export function validateStrideE10W4ComposeSource(body, policy) {
  policy = validateStrideE10W4DeploymentPolicy(policy)
  const source = String(body || '')
  const required = [
    'STRIDE_E10_W4_MODE: ${STRIDE_E10_W4_RELEASE_MODE:?STRIDE_E10_W4_RELEASE_MODE is required}',
    'STRIDE_E10_W4_SNAPSHOT_PATH: ${STRIDE_E10_W4_SNAPSHOT_PATH:?STRIDE_E10_W4_SNAPSHOT_PATH is required}',
    'STRIDE_E10_W4_ACTIVATION_BACKUP_DIR: ${STRIDE_E10_W4_ACTIVATION_BACKUP_DIR:?STRIDE_E10_W4_ACTIVATION_BACKUP_DIR is required}',
    'STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH: ${STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH:?STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH is required}'
  ]
  if (policy.releaseMode === strideE10W4CanaryMode) {
    if (required.some(marker => source.includes(marker)) || source.includes('STRIDE_E10_W4_RELEASE_MODE')) {
      throw new Error('compatibility canary Compose must preserve the legacy environment shape')
    }
    return policy
  }
  if (required.some(marker => !source.includes(marker)) || source.includes(':-bonfire_network_live')) {
    throw new Error('live STRIDE E10 W4 Compose binding is not exact')
  }
  return policy
}

export function validateReleaseScopePolicy(policy) {
  if (policy?.schema !== scopeSchema || policy.defaultOwnership !== 'excluded' || policy.stagesFiles !== false ||
      !Array.isArray(policy.includeRules) || policy.includeRules.length === 0 ||
      !Array.isArray(policy.requiredPaths) || !Array.isArray(policy.releaseConfigPaths) ||
      !Array.isArray(policy.excludedPrefixes)) throw new Error('release scope policy is invalid')
  for (const prefix of requiredExcludedPrefixes) {
    if (!policy.excludedPrefixes.includes(prefix)) throw new Error(`release scope policy must exclude ${prefix}`)
  }
  if (JSON.stringify([...policy.releaseConfigPaths].sort()) !== JSON.stringify([...requiredConfigPaths].sort())) {
    throw new Error('release scope config inventory is not exact')
  }
  for (const path of policy.requiredPaths.concat(policy.releaseConfigPaths)) validateRepoPath(path)
  for (const prefix of policy.excludedPrefixes) validateRepoPrefix(prefix)
  for (const rule of policy.includeRules) {
    if (!rule || !['exact', 'prefix', 'rootSuffix', 'prefixSuffix'].includes(rule.kind)) {
      throw new Error('release scope include rule is invalid')
    }
    if (rule.kind === 'exact') validateRepoPath(rule.path)
    if (rule.kind === 'prefix') validateRepoPrefix(rule.prefix)
    if (rule.kind === 'rootSuffix' && (!String(rule.suffix || '').startsWith('.') || String(rule.suffix).includes('/'))) {
      throw new Error('release scope root suffix is invalid')
    }
    if (rule.kind === 'prefixSuffix') {
      validateRepoPrefix(rule.prefix)
      if (!String(rule.suffix || '').startsWith('.') || String(rule.suffix).includes('/')) throw new Error('release scope suffix is invalid')
    }
  }
  for (const path of policy.requiredPaths) {
    if (!releasePathOwned(path, policy)) throw new Error(`required release path is outside policy: ${path}`)
  }
  return policy
}

export function releasePathOwned(path, policy) {
  validateRepoPath(path)
  if (policy.excludedPrefixes.some(prefix => path.startsWith(prefix))) return false
  return policy.includeRules.some(rule => {
    if (rule.kind === 'exact') return path === rule.path
    if (rule.kind === 'prefix') return path.startsWith(rule.prefix)
    if (rule.kind === 'rootSuffix') return !path.includes('/') && path.endsWith(rule.suffix) && !(rule.excludeSuffix && path.endsWith(rule.excludeSuffix))
    return path.startsWith(rule.prefix) && path.endsWith(rule.suffix) && !(rule.excludeSuffix && path.endsWith(rule.excludeSuffix))
  })
}

export function validateReviewedInventory(entries, policy) {
  if (!Array.isArray(entries) || entries.length === 0) throw new Error('reviewed release inventory is empty')
  const seen = new Set()
  for (const entry of entries) {
    validateRepoPath(entry.path)
    if (seen.has(entry.path)) throw new Error(`reviewed release inventory repeats ${entry.path}`)
    seen.add(entry.path)
    if (!releasePathOwned(entry.path, policy)) throw new Error(`reviewed release inventory contains unowned path ${entry.path}`)
    if (entry.type !== 'blob' || !['100644', '100755'].includes(entry.mode) || !/^[0-9a-f]{40,64}$/.test(String(entry.object || ''))) {
      throw new Error(`reviewed release inventory contains unsupported or nested gitlink entry ${entry.path}`)
    }
  }
  for (const path of policy.requiredPaths) if (!seen.has(path)) throw new Error(`reviewed release inventory is missing ${path}`)
  return entries
}

export function reviewedInventoryDigest(entries) {
  return sha256(canonical({ schema: 'bonfire.reviewed-release-inventory.v1', entries: entries.map(({ path, mode, object }) => ({ path, mode, object })) }))
}

export function validateSourceReceipt(receipt) {
  if (receipt?.schema !== sourceSchema || !commitPattern.test(String(receipt.releaseCommit || '')) ||
      !commitPattern.test(String(receipt.gitTreeObject || ''))) throw new Error('source receipt git identity is invalid')
  for (const name of ['gitTreeDigest', 'reviewedInventorySha256', 'scopePolicySha256', 'sourceArchiveSha256', 'transitiveInputsSha256', 'buildConfigSha256']) {
    if (!shaPattern.test(String(receipt[name] || ''))) throw new Error(`source receipt ${name} is invalid`)
  }
  if (receipt.reviewedRef !== receipt.releaseCommit || !commitPattern.test(String(receipt.reviewedRef || ''))) {
    throw new Error('source receipt must bind an exact reviewed commit')
  }
  if (!Number.isSafeInteger(receipt.inputCount) || receipt.inputCount < requiredConfigPaths.length ||
      !Number.isSafeInteger(receipt.sourceDateEpoch) || receipt.sourceDateEpoch <= 0) {
    throw new Error('source receipt inventory/time binding is invalid')
  }
  const configFiles = receipt.configFiles || {}
  if (JSON.stringify(Object.keys(configFiles).sort()) !== JSON.stringify([...requiredConfigPaths].sort()) ||
      Object.values(configFiles).some(value => !shaPattern.test(String(value)))) {
    throw new Error('source receipt candidate config inventory is invalid')
  }
  if (receipt.scopePolicySha256 !== configFiles[scopePolicyPath] ||
      receipt.buildConfigSha256 !== configInventoryDigest(configFiles)) {
    throw new Error('source receipt candidate config binding is invalid')
  }
  return receipt
}

export function validateBuildInputs(manifest) {
  if (manifest?.schema !== buildInputsSchema || manifest.platform !== 'linux/amd64' ||
      !/^1\.26(?:\.|$)/.test(String(manifest.goVersion || '')) ||
      !imageRefPattern.test(String(manifest.goBuildImage || '')) ||
      !imageRefPattern.test(String(manifest.runtimeImage || '')) ||
      !/^\d{8}T\d{6}Z$/.test(String(manifest.debianSnapshot || '')) ||
      !/^\d+\.\d+\.\d+\.\d+$/.test(String(manifest.chromeHeadlessShellVersion || '')) ||
      !shaPattern.test(String(manifest.chromeHeadlessShellArchiveSha256 || ''))) {
    throw new Error('release build-input manifest identity is invalid')
  }
  const expectedDebian = `http://snapshot.debian.org/archive/debian/${manifest.debianSnapshot}`
  const expectedSecurity = `http://snapshot.debian.org/archive/debian-security/${manifest.debianSnapshot}`
  if (manifest.debianArchive !== expectedDebian || manifest.debianSecurityArchive !== expectedSecurity) {
    throw new Error('release build-input package snapshot is not exact')
  }
  for (const name of ['buildPackages', 'runtimePackages', 'renderRuntimePackages', 'dependencyLocks']) {
    if (!Array.isArray(manifest[name]) || manifest[name].length === 0 ||
        manifest[name].some(value => typeof value !== 'string' || value.trim() === '')) {
      throw new Error(`release build-input ${name} is invalid`)
    }
  }
  const sandbox = manifest.rendererSandbox || {}
  if (sandbox.apparmorProfile !== rendererAppArmorProfile || sandbox.apparmorAbi !== '4.0' ||
      sandbox.seccompProfilePath !== rendererSeccompProfile ||
      sandbox.seccompBase !== 'github.com/moby/profiles/seccomp/v0.2.3' ||
      sandbox.seccompBaseSha256 !== '536529b665dd0972c37bfb569f5d4ac8a53592e7b00752bc39ff063ca9864c74' ||
      sandbox.seccompAllowDeltaCount !== 5) {
    throw new Error('release renderer sandbox input is not exact')
  }
  const sidecars = manifest.sidecarImages || {}
  if (JSON.stringify(Object.keys(sidecars).sort()) !== JSON.stringify(['caddy', 'canonicalPostgres', 'coturn']) ||
      Object.values(sidecars).some(value => !imageRefPattern.test(String(value)))) {
    throw new Error('release sidecar image inputs are not exact')
  }
  return manifest
}

export function validateCandidateBundleManifest(manifest, source) {
  if (manifest?.schema !== candidateBundleSchema || manifest.releaseCommit !== source.releaseCommit ||
      manifest.buildConfigSha256 !== source.buildConfigSha256 ||
      JSON.stringify(manifest.configFiles) !== JSON.stringify(source.configFiles)) {
    throw new Error('candidate deployment bundle manifest differs from source receipt')
  }
  return manifest
}

export function validateReleaseReceipt(receipt) {
  if (![receiptSchema, receiptSchemaW4].includes(receipt?.schema) || receipt.attestation !== 'unsigned-local-v1') {
    throw new Error('release receipt schema/attestation is invalid')
  }
  if (receipt.schema === receiptSchema && receipt.strideE10W4 !== undefined) {
    throw new Error('legacy release receipt cannot claim STRIDE E10 W4 policy')
  }
  if (receipt.schema === receiptSchemaW4) {
    const policy = validateStrideE10W4DeploymentPolicy(receipt.strideE10W4)
    if (policy.releaseMode !== strideE10W4NetworkMode) throw new Error('W4 release receipt must bind the live network mode')
  }
  validateSourceReceipt(receipt.source)
  for (const name of ['sourceReceiptSha256', 'buildInputManifestSha256', 'buildManifestSha256', 'candidateBundleManifestSha256', 'bundleSha256', 'environmentMarker']) {
    if (!shaPattern.test(String(receipt[name] || ''))) throw new Error(`release receipt ${name} is invalid`)
  }
  const manifest = receipt.buildManifest
  if (manifest?.schema !== buildManifestSchema || sha256(jsonLine(manifest)) !== receipt.buildManifestSha256 ||
      manifest.sourceReceiptSha256 !== receipt.sourceReceiptSha256 || JSON.stringify(manifest.source) !== JSON.stringify(receipt.source) ||
      manifest.buildInputs?.sha256 !== receipt.buildInputManifestSha256 ||
      JSON.stringify(manifest.outputs?.images) !== JSON.stringify(receipt.images) ||
      JSON.stringify(manifest.outputs?.sidecars) !== JSON.stringify(receipt.sidecars)) {
    throw new Error('release receipt build/output binding is invalid')
  }
  if (!manifest.toolchain?.dockerCompose || typeof manifest.toolchain.dockerCompose !== 'object' ||
      Array.isArray(manifest.toolchain.dockerCompose) ||
      !/^v?\d+\.\d+\.\d+/.test(String(manifest.toolchain.dockerCompose.version || ''))) {
    throw new Error('release receipt Docker Compose toolchain identity is invalid')
  }
  const app = receipt.images?.meetingassist
  const render = receipt.images?.renderRunner
  if (!validBuiltImage(app) || !validBuiltImage(render) || app.binarySha256 !== render.binarySha256 ||
      !shaPattern.test(String(render.chromeHeadlessShellBinarySha256 || ''))) {
    throw new Error('release receipt app/render image binding is invalid')
  }
  for (const name of ['canonicalPostgres', 'coturn', 'caddy']) {
    const image = receipt.sidecars?.[name]
    if (!imageRefPattern.test(String(image?.imageReference || '')) || !localImageIdPattern.test(String(image?.imageId || '')) ||
        image.imageDigest !== normalizeDigest(image.imageId) || image.platform !== 'linux/amd64') {
      throw new Error(`release receipt ${name} sidecar binding is invalid`)
    }
  }
  const buildInputs = validateBuildInputs(manifest.buildInputs?.manifest)
  if (receipt.sidecars.canonicalPostgres.imageReference !== buildInputs.sidecarImages.canonicalPostgres ||
      receipt.sidecars.coturn.imageReference !== buildInputs.sidecarImages.coturn ||
      receipt.sidecars.caddy.imageReference !== buildInputs.sidecarImages.caddy) {
    throw new Error('release receipt sidecars differ from pinned build inputs')
  }
  const commonArgs = commonBuildArgs(receipt.source, buildInputs, receipt.buildInputManifestSha256)
  const expectedBuildArgs = {
    meetingassist: commonArgs,
    renderRunner: {
      ...commonArgs,
      CHROME_HEADLESS_SHELL_VERSION: buildInputs.chromeHeadlessShellVersion,
      CHROME_HEADLESS_SHELL_SHA256: buildInputs.chromeHeadlessShellArchiveSha256
    }
  }
  const dependencyPaths = buildInputs.dependencyLocks.concat(['Dockerfile', 'Dockerfile.render', '.dockerignore',
    'deploy/digitalocean/release-build-inputs.json', scopePolicyPath],
  receipt.schema === receiptSchemaW4 ? [strideE10W4DeploymentPolicyPath] : [])
  const dependencyInputs = manifest.buildInputs?.dependencyInputs || {}
  if (JSON.stringify(manifest.buildArgs) !== JSON.stringify(expectedBuildArgs) ||
      JSON.stringify(manifest.buildInvocations) !== JSON.stringify({
        meetingassist: { platform: buildInputs.platform, dockerfile: 'Dockerfile', target: 'meetingassist-runtime', pull: false },
        renderRunner: { platform: buildInputs.platform, dockerfile: 'Dockerfile.render', target: 'render-runner', pull: false }
      }) ||
      JSON.stringify(manifest.archiveIdentity) !== JSON.stringify(archiveBinding(receipt.source)) ||
      Object.keys(dependencyInputs).length !== dependencyPaths.length ||
      dependencyPaths.some(path => !shaPattern.test(String(dependencyInputs[path] || ''))) ||
      typeof manifest.toolchain?.releaseToolNode !== 'string' || !manifest.toolchain.releaseToolNode ||
      typeof manifest.toolchain?.docker !== 'string' || !manifest.toolchain.docker) {
    throw new Error('release build manifest inputs/toolchain are inconsistent')
  }
  const computedEnvironment = computeEnvironmentMarker({ ...receipt.source,
    buildInputManifestSha256: receipt.buildInputManifestSha256,
    buildManifestSha256: receipt.buildManifestSha256,
    binarySha256: app.binarySha256, imageDigest: app.imageDigest })
  if (receipt.environmentMarker !== computedEnvironment || receipt.bundleSha256 !== computeBundleSha256(receipt)) {
    throw new Error('release receipt environment/bundle binding is invalid')
  }
  return receipt
}

export function validatePrepareState({ dirtyBefore, dirtyAfter, head, headAfter = head, reviewedRef, reviewedCommit, stagedInventory = '' }) {
  if (String(dirtyBefore).trim() || String(dirtyAfter).trim()) throw new Error('release checkout is not clean')
  if (!commitPattern.test(String(reviewedRef || ''))) throw new Error('--reviewed-ref must be an exact full commit SHA')
  if (head !== reviewedCommit || head !== reviewedRef || headAfter !== head) throw new Error('HEAD does not equal the exact reviewed commit')
  if (String(stagedInventory).split('\n').some(line => line.startsWith('160000 '))) {
    throw new Error('release source contains a gitlink/submodule that git archive cannot bind')
  }
}

export async function inspectExtractedArchive(sourceRoot) {
  const policyRaw = await readFile(join(sourceRoot, scopePolicyPath))
  const policy = validateReleaseScopePolicy(parseJSON(policyRaw, 'release scope policy'))
  const entries = []
  async function walk(directory, prefix = '') {
    for (const name of (await readdir(directory)).sort()) {
      const relativePath = prefix ? `${prefix}/${name}` : name
      validateRepoPath(relativePath)
      const absolutePath = join(directory, name)
      const info = await lstat(absolutePath)
      if (info.isDirectory()) {
        await walk(absolutePath, relativePath)
      } else if (info.isFile()) {
        if (!releasePathOwned(relativePath, policy)) throw new Error(`archive contains unowned release path ${relativePath}`)
        const body = await readFile(absolutePath)
        entries.push({ path: relativePath, type: 'file', mode: (info.mode & 0o111) ? '100755' : '100644', size: body.length, sha256: sha256(body) })
      } else if (info.isSymbolicLink()) {
        const target = await readlink(absolutePath)
        throw new Error(`archive contains unsupported symlink ${relativePath} -> ${target}`)
      } else {
        throw new Error(`archive contains unsupported entry ${relativePath}`)
      }
    }
  }
  await walk(sourceRoot)
  const entryPaths = new Set(entries.map(entry => entry.path))
  for (const path of policy.requiredPaths) if (!entryPaths.has(path)) throw new Error(`release archive is missing ${path}`)
  const treeEntries = entries.map(({ path, mode, sha256: digest }) => ({ path, mode, sha256: digest }))
  const configFiles = Object.fromEntries(policy.releaseConfigPaths.map(path => {
    const entry = entries.find(candidate => candidate.path === path)
    if (!entry) throw new Error(`release config inventory is missing ${path}`)
    return [path, entry.sha256]
  }))
  return {
    entries,
    inputCount: entries.length,
    gitTreeDigest: sha256(canonical({ schema: 'bonfire.archive-tree.v2', entries: treeEntries })),
    transitiveInputsSha256: sha256(canonical({ schema: 'bonfire.source-inventory.v2', entries })),
    buildConfigSha256: configInventoryDigest(configFiles),
    configFiles,
    scopePolicySha256: sha256(policyRaw)
  }
}

export function verifyArchiveIdentity(identity, source) {
  for (const [name, expected] of Object.entries({
    gitTreeDigest: source.gitTreeDigest,
    transitiveInputsSha256: source.transitiveInputsSha256,
    buildConfigSha256: source.buildConfigSha256,
    scopePolicySha256: source.scopePolicySha256,
    inputCount: source.inputCount
  })) {
    if (identity[name] !== expected) throw new Error(`extracted archive ${name} differs from reviewed source receipt`)
  }
  if (JSON.stringify(identity.configFiles) !== JSON.stringify(source.configFiles)) {
    throw new Error('extracted archive candidate config files differ from reviewed source receipt')
  }
}

async function readReviewedInventory(reviewedRef) {
  const { stdout: policyRaw } = await git(['show', `${reviewedRef}:${scopePolicyPath}`], { encoding: 'buffer', maxBuffer: 4 << 20 })
  const policy = validateReleaseScopePolicy(parseJSON(policyRaw, 'release scope policy'))
  const { stdout: treeRaw } = await git(['ls-tree', '-rz', '--full-tree', reviewedRef], { encoding: 'buffer', maxBuffer: 128 << 20 })
  const allEntries = parseGitTree(treeRaw)
  const selected = allEntries.filter(entry => releasePathOwned(entry.path, policy))
  validateReviewedInventory(selected, policy)
  return { policy, policyRaw, entries: selected, inventorySha256: reviewedInventoryDigest(selected) }
}

async function scope(options) {
  required('--reviewed-ref', options.reviewedRef)
  if (!commitPattern.test(options.reviewedRef)) throw new Error('--reviewed-ref must be an exact full commit SHA')
  const { stdout: commitRaw } = await git(['rev-parse', `${options.reviewedRef}^{commit}`])
  const reviewedCommit = String(commitRaw).trim()
  if (reviewedCommit !== options.reviewedRef) throw new Error('--reviewed-ref must resolve to itself as an exact commit')
  const inventory = await readReviewedInventory(reviewedCommit)
  process.stdout.write(`${JSON.stringify({
    schema: 'bonfire.reviewed-release-scope.v1', releaseCommit: reviewedCommit,
    scopePolicySha256: sha256(inventory.policyRaw), inventorySha256: inventory.inventorySha256,
    inputCount: inventory.entries.length, paths: inventory.entries.map(entry => entry.path)
  })}\n`)
}

async function prepare(options) {
  required('--reviewed-ref', options.reviewedRef)
  required('--archive', options.archive)
  required('--source-receipt', options.sourceReceipt)
  if (!commitPattern.test(options.reviewedRef)) throw new Error('--reviewed-ref must be an exact full commit SHA')

  const { stdout: dirtyBefore } = await git(['status', '--porcelain', '--untracked-files=all'])
  const { stdout: commitRaw } = await git(['rev-parse', 'HEAD'])
  const releaseCommit = String(commitRaw).trim()
  const { stdout: reviewedRaw } = await git(['rev-parse', `${options.reviewedRef}^{commit}`])
  const { stdout: treeRaw } = await git(['rev-parse', `${releaseCommit}^{tree}`])
  const { stdout: epochRaw } = await git(['show', '-s', '--format=%ct', releaseCommit])
  const reviewed = await readReviewedInventory(releaseCommit)
  const archiveArgs = ['archive', '--format=tar', releaseCommit, '--', ...reviewed.entries.map(entry => entry.path)]
  const { stdout: archive } = await git(archiveArgs, { encoding: 'buffer', maxBuffer: 512 << 20 })
  const { stdout: dirtyAfter } = await git(['status', '--porcelain', '--untracked-files=all'])
  const { stdout: headAfterRaw } = await git(['rev-parse', 'HEAD'])
  validatePrepareState({ dirtyBefore, dirtyAfter, head: releaseCommit, headAfter: String(headAfterRaw).trim(),
    reviewedRef: options.reviewedRef, reviewedCommit: String(reviewedRaw).trim() })

  const temporary = await mkdtemp(`${tmpdir()}/bonfire-release-prepare-`)
  const sourceRoot = resolve(temporary, 'source')
  try {
    await mkdir(sourceRoot)
    await extractArchive(archive, sourceRoot)
    const identity = await inspectExtractedArchive(sourceRoot)
    await verifyExecutingReleaseTool(identity.configFiles['scripts/bonfire-release.mjs'])
    const archivedPathModes = identity.entries.map(({ path, mode }) => ({ path, mode }))
    const reviewedPathModes = reviewed.entries.map(({ path, mode }) => ({ path, mode }))
    if (JSON.stringify(archivedPathModes) !== JSON.stringify(reviewedPathModes)) throw new Error('archive inventory differs from exact reviewed inventory')
    validateBuildInputs(JSON.parse(await readFile(join(sourceRoot, 'deploy/digitalocean/release-build-inputs.json'), 'utf8')))
    const receipt = validateSourceReceipt({
      schema: sourceSchema, releaseCommit, reviewedRef: options.reviewedRef,
      gitTreeObject: String(treeRaw).trim(), gitTreeDigest: identity.gitTreeDigest,
      reviewedInventorySha256: reviewed.inventorySha256, scopePolicySha256: identity.scopePolicySha256,
      sourceArchiveSha256: sha256(archive), transitiveInputsSha256: identity.transitiveInputsSha256,
      buildConfigSha256: identity.buildConfigSha256, configFiles: identity.configFiles,
      inputCount: identity.inputCount, sourceDateEpoch: Number(String(epochRaw).trim())
    })
    await writeExclusive(resolve(options.archive), archive, 0o600)
    await writeExclusive(resolve(options.sourceReceipt), jsonLine(receipt), 0o600)
    process.stdout.write(`${JSON.stringify({ prepared: true, releaseCommit, reviewedInventorySha256: reviewed.inventorySha256,
      inputCount: reviewed.entries.length, archive: resolve(options.archive), sourceReceipt: resolve(options.sourceReceipt) })}\n`)
  } finally {
    await chmod(sourceRoot, 0o700).catch(() => {})
    await rm(temporary, { recursive: true, force: true })
  }
}

async function build(options) {
  for (const [name, value] of [['--archive', options.archive], ['--source-receipt', options.sourceReceipt], ['--image', options.image],
    ['--render-image', options.renderImage], ['--build-manifest', options.buildManifest], ['--release-receipt', options.releaseReceipt],
    ['--runtime-env', options.runtimeEnv]]) required(name, value)
  const sourceRaw = await readFile(resolve(options.sourceReceipt))
  const source = validateSourceReceipt(parseJSON(sourceRaw, 'source receipt'))
  await verifyExecutingReleaseTool(source.configFiles['scripts/bonfire-release.mjs'])
  const archive = await readFile(resolve(options.archive))
  if (sha256(archive) !== source.sourceArchiveSha256) throw new Error('source archive differs from the reviewed source receipt')
  const sourceReceiptSha256 = sha256(sourceRaw)
  const temporary = await mkdtemp(`${tmpdir()}/bonfire-release-build-`)
  const sourceRoot = resolve(temporary, 'source')
  try {
    await mkdir(sourceRoot)
    await extractArchive(archive, sourceRoot)
    const archiveIdentity = await inspectExtractedArchive(sourceRoot)
    verifyArchiveIdentity(archiveIdentity, source)
    const strideE10W4PolicyRaw = await readFile(join(sourceRoot, strideE10W4DeploymentPolicyPath))
    const strideE10W4Policy = validateStrideE10W4DeploymentPolicy(parseJSON(strideE10W4PolicyRaw, 'STRIDE E10 W4 deployment policy'))
    validateStrideE10W4ComposeSource(await readFile(join(sourceRoot, 'deploy/digitalocean/docker-compose.yml'), 'utf8'), strideE10W4Policy)
    const buildInputsRaw = await readFile(join(sourceRoot, 'deploy/digitalocean/release-build-inputs.json'))
    const buildInputs = validateBuildInputs(parseJSON(buildInputsRaw, 'release build-input manifest'))
    const buildInputManifestSha256 = sha256(buildInputsRaw)
    const commonArgs = commonBuildArgs(source, buildInputs, buildInputManifestSha256)
    const buildArgs = {
      meetingassist: commonArgs,
      renderRunner: {
        ...commonArgs,
        CHROME_HEADLESS_SHELL_VERSION: buildInputs.chromeHeadlessShellVersion,
        CHROME_HEADLESS_SHELL_SHA256: buildInputs.chromeHeadlessShellArchiveSha256
      }
    }
    const images = {
      meetingassist: await buildOwnedImage({ sourceRoot, dockerfile: 'Dockerfile', target: 'meetingassist-runtime',
        imageReference: options.image, buildArgs: buildArgs.meetingassist, platform: buildInputs.platform, temporary, role: 'meetingassist' }),
      renderRunner: await buildOwnedImage({ sourceRoot, dockerfile: 'Dockerfile.render', target: 'render-runner',
        imageReference: options.renderImage, buildArgs: buildArgs.renderRunner, platform: buildInputs.platform, temporary, role: 'renderRunner' })
    }
    if (images.meetingassist.binarySha256 !== images.renderRunner.binarySha256) {
      throw new Error('app and render-runner binaries are not byte-identical for the same source identity')
    }
    const sidecars = {}
    for (const [role, imageReference] of Object.entries(buildInputs.sidecarImages)) {
      sidecars[role] = await inspectPinnedImage(imageReference, buildInputs.platform)
    }
    const [{ stdout: dockerVersionRaw }, { stdout: dockerComposeVersionRaw }] = await Promise.all([
      execFileAsync('docker', ['version', '--format', '{{json .}}'], { maxBuffer: 4 << 20 }),
      execFileAsync('docker', ['compose', 'version', '--format', 'json'], { maxBuffer: 4 << 20 })
    ])
    const dependencyPaths = buildInputs.dependencyLocks.concat(['Dockerfile', 'Dockerfile.render', '.dockerignore',
      'deploy/digitalocean/release-build-inputs.json', scopePolicyPath],
    strideE10W4Policy.releaseMode === strideE10W4NetworkMode ? [strideE10W4DeploymentPolicyPath] : [])
    const dependencyInputs = Object.fromEntries(dependencyPaths.map(path => [path,
      archiveIdentity.entries.find(entry => entry.path === path)?.sha256 || 'missing']))
    if (Object.values(dependencyInputs).includes('missing')) throw new Error('build dependency inventory is incomplete')
    const buildManifest = {
      schema: buildManifestSchema, sourceReceiptSha256, source,
      archiveIdentity: archiveBinding(source),
      buildInputs: { sha256: buildInputManifestSha256, manifest: buildInputs, dependencyInputs },
      buildArgs,
      buildInvocations: {
        meetingassist: { platform: buildInputs.platform, dockerfile: 'Dockerfile', target: 'meetingassist-runtime', pull: false },
        renderRunner: { platform: buildInputs.platform, dockerfile: 'Dockerfile.render', target: 'render-runner', pull: false }
      },
      toolchain: { releaseToolNode: process.version, docker: String(dockerVersionRaw).trim(),
        dockerCompose: stableJSONValue(parseJSON(dockerComposeVersionRaw, 'Docker Compose version')) },
      outputs: { images, sidecars }
    }
    const buildManifestRaw = jsonLine(buildManifest)
    const buildManifestSha256 = sha256(buildManifestRaw)
    const releaseDir = dirname(resolve(options.releaseReceipt))
    const candidateBundle = await writeCandidateBundle(sourceRoot, releaseDir, source)
    const candidateBundleManifestRaw = jsonLine(candidateBundle)
    const candidateBundleManifestSha256 = sha256(candidateBundleManifestRaw)
    const environmentMarker = computeEnvironmentMarker({ ...source, buildInputManifestSha256, buildManifestSha256,
      binarySha256: images.meetingassist.binarySha256, imageDigest: images.meetingassist.imageDigest })
    const receiptBase = {
      schema: strideE10W4Policy.releaseMode === strideE10W4NetworkMode ? receiptSchemaW4 : receiptSchema,
      attestation: 'unsigned-local-v1', source, sourceReceiptSha256,
      buildInputManifestSha256, buildManifest, buildManifestSha256,
      candidateBundleManifestSha256, images, sidecars, environmentMarker
    }
    if (strideE10W4Policy.releaseMode === strideE10W4NetworkMode) receiptBase.strideE10W4 = strideE10W4Policy
    const receipt = validateReleaseReceipt({ ...receiptBase, bundleSha256: computeBundleSha256(receiptBase) })
    await writeExclusive(resolve(options.buildManifest), buildManifestRaw, 0o600)
    await writeExclusive(resolve(options.releaseReceipt), jsonLine(receipt), 0o600)
    await writeExclusive(resolve(options.runtimeEnv), Buffer.from(runtimeEnvironment(receipt)), 0o600)
    await writeExclusive(join(releaseDir, 'candidate-bundle.json'), candidateBundleManifestRaw, 0o600)
    process.stdout.write(`${JSON.stringify({ built: true, attestation: receipt.attestation, releaseCommit: source.releaseCommit,
      bundleSha256: receipt.bundleSha256, images: Object.fromEntries(Object.entries(images).map(([name, image]) => [name, image.imageId])),
      buildManifest: resolve(options.buildManifest), releaseReceipt: resolve(options.releaseReceipt), runtimeEnv: resolve(options.runtimeEnv) })}\n`)
  } finally {
    await chmod(sourceRoot, 0o700).catch(() => {})
    await rm(temporary, { recursive: true, force: true })
  }
}

async function buildOwnedImage({ sourceRoot, dockerfile, target, imageReference, buildArgs, platform, temporary, role }) {
  const dockerArgs = ['build', '--platform', platform, '--file', join(sourceRoot, dockerfile), '--target', target, '--tag', imageReference]
  for (const [name, value] of Object.entries(buildArgs)) dockerArgs.push('--build-arg', `${name}=${value}`)
  dockerArgs.push(sourceRoot)
  await execFileAsync('docker', dockerArgs, { maxBuffer: 64 << 20 })
  const inspected = await inspectPinnedImage(imageReference, platform)
  const { stdout: imageInspectRaw } = await execFileAsync('docker', ['image', 'inspect', imageReference], { maxBuffer: 16 << 20 })
  const imageInspect = parseJSON(imageInspectRaw, 'Docker image inspect')[0]
  const source = {
    releaseCommit: buildArgs.BONFIRE_RELEASE_COMMIT,
    gitTreeDigest: buildArgs.BONFIRE_GIT_TREE_DIGEST,
    buildConfigSha256: buildArgs.BONFIRE_BUILD_CONFIG_SHA256,
    transitiveInputsSha256: buildArgs.BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256,
    sourceArchiveSha256: buildArgs.BONFIRE_SOURCE_ARCHIVE_SHA256
  }
  verifyLabels(imageInspect?.Config?.Labels || {}, source, buildArgs.BONFIRE_BUILD_INPUT_MANIFEST_SHA256)
  let container = ''
  try {
    ;({ stdout: container } = await execFileAsync('docker', ['create', inspected.imageId]))
    container = String(container).trim()
    const binaryPath = join(temporary, `${role}-meetingassist`)
    const buildPackagesPath = join(temporary, `${role}-build-packages.txt`)
    const runtimePackagesPath = join(temporary, `${role}-runtime-packages.txt`)
    await execFileAsync('docker', ['cp', `${container}:/app/meetingassist`, binaryPath])
    await execFileAsync('docker', ['cp', `${container}:/app/release-build-packages.txt`, buildPackagesPath])
    await execFileAsync('docker', ['cp', `${container}:/app/release-runtime-packages.txt`, runtimePackagesPath])
    const resolvedPackages = {
      build: normalizeLines(await readFile(buildPackagesPath, 'utf8')),
      runtime: normalizeLines(await readFile(runtimePackagesPath, 'utf8'))
    }
    if (resolvedPackages.build.length === 0 || resolvedPackages.runtime.length === 0) throw new Error(`${role} resolved package inventory is empty`)
    const output = { imageReference, ...inspected, binarySha256: sha256(await readFile(binaryPath)), resolvedPackages }
    if (role === 'renderRunner') {
      const chromePath = join(temporary, 'chrome-headless-shell')
      await execFileAsync('docker', ['cp', `${container}:/opt/chrome-headless-shell/chrome-headless-shell`, chromePath])
      output.chromeHeadlessShellBinarySha256 = sha256(await readFile(chromePath))
    }
    return output
  } finally {
    if (container) await execFileAsync('docker', ['rm', '-f', container]).catch(() => {})
  }
}

async function inspectPinnedImage(imageReference, platform) {
  const { stdout: raw } = await execFileAsync('docker', ['image', 'inspect', imageReference], { maxBuffer: 16 << 20 })
  const inspected = parseJSON(raw, 'Docker image inspect')[0]
  const imageId = String(inspected?.Id || '').toLowerCase()
  if (!localImageIdPattern.test(imageId)) throw new Error(`Docker image ${imageReference} has no immutable local image ID`)
  if (`${inspected?.Os}/${inspected?.Architecture}` !== platform) throw new Error(`Docker image ${imageReference} platform differs from pinned build inputs`)
  return { imageReference, imageId, imageDigest: normalizeDigest(imageId), platform }
}

async function writeCandidateBundle(sourceRoot, releaseDir, source) {
  const manifest = validateCandidateBundleManifest({
    schema: candidateBundleSchema, releaseCommit: source.releaseCommit,
    buildConfigSha256: source.buildConfigSha256, configFiles: source.configFiles
  }, source)
  const candidateRoot = releasePaths(releaseDir).candidateRoot
  for (const path of Object.keys(source.configFiles)) {
    const target = join(candidateRoot, path)
    await writeExclusive(target, await readFile(join(sourceRoot, path)), 0o400)
  }
  const directories = new Set([candidateRoot])
  for (const path of Object.keys(source.configFiles)) {
    let directory = dirname(join(candidateRoot, path))
    while (directory.startsWith(`${candidateRoot}/`)) {
      directories.add(directory)
      directory = dirname(directory)
    }
  }
  for (const directory of [...directories].sort((left, right) => right.length - left.length)) await chmod(directory, 0o500)
  await verifyCandidateConfig(candidateRoot, manifest)
  return manifest
}

async function loadReleaseBundle(options, { verifyTool = true } = {}) {
  const paths = options.releaseDir ? releasePaths(options.releaseDir) : {
    sourceReceipt: resolve(options.sourceReceipt), buildManifest: resolve(options.buildManifest),
    releaseReceipt: resolve(options.releaseReceipt), runtimeEnv: resolve(options.runtimeEnv || join(dirname(resolve(options.releaseReceipt)), 'release.env')),
    candidateBundleManifest: join(dirname(resolve(options.releaseReceipt)), 'candidate-bundle.json'),
    candidateRoot: join(dirname(resolve(options.releaseReceipt)), 'sealed-candidate')
  }
  const receipt = validateReleaseReceipt(parseJSON(await readFile(paths.releaseReceipt), 'release receipt'))
  const sourceRaw = await readFile(paths.sourceReceipt)
  const source = validateSourceReceipt(parseJSON(sourceRaw, 'source receipt'))
  const buildManifestRaw = await readFile(paths.buildManifest)
  const buildManifest = parseJSON(buildManifestRaw, 'build manifest')
  if (sha256(sourceRaw) !== receipt.sourceReceiptSha256 || JSON.stringify(source) !== JSON.stringify(receipt.source)) {
    throw new Error('reviewed source receipt differs from release receipt')
  }
  if (sha256(buildManifestRaw) !== receipt.buildManifestSha256 || JSON.stringify(buildManifest) !== JSON.stringify(receipt.buildManifest)) {
    throw new Error('build manifest differs from release receipt')
  }
  const candidateRaw = await readFile(paths.candidateBundleManifest)
  if (sha256(candidateRaw) !== receipt.candidateBundleManifestSha256) throw new Error('candidate deployment bundle manifest digest differs from release receipt')
  const candidate = validateCandidateBundleManifest(parseJSON(candidateRaw, 'candidate deployment bundle manifest'), source)
  await verifyCandidateConfig(paths.candidateRoot, candidate)
  if (verifyTool) await verifyExecutingReleaseTool(source.configFiles['scripts/bonfire-release.mjs'])
  return { receipt, source, buildManifest, candidate, paths }
}

export async function verifyCandidateConfig(candidateRoot, manifest) {
  for (const [path, expected] of Object.entries(manifest.configFiles)) {
    const absolute = resolve(candidateRoot, path)
    if (relative(resolve(candidateRoot), absolute).startsWith('..')) {
      throw new Error(`candidate deployment config ${path} differs from source receipt`)
    }
    await verifySealedCandidatePath(candidateRoot, path)
    if (sha256(await readFile(absolute)) !== expected) {
      throw new Error(`candidate deployment config ${path} differs from source receipt`)
    }
  }
}

async function verifySealedCandidatePath(candidateRoot, path) {
  validateRepoPath(path)
  const root = resolve(candidateRoot)
  const rootInfo = await lstat(root)
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink() || (rootInfo.mode & 0o777) !== 0o500) {
    throw new Error('sealed candidate root must be a private read-only directory')
  }
  const parts = path.split('/')
  let current = root
  for (let index = 0; index < parts.length; index++) {
    current = join(current, parts[index])
    const info = await lstat(current)
    if (info.isSymbolicLink()) throw new Error(`candidate deployment config ${path} contains a symlink`)
    if (index < parts.length - 1) {
      if (!info.isDirectory() || (info.mode & 0o777) !== 0o500) {
        throw new Error(`candidate deployment config ${path} has an unsealed ancestor`)
      }
    } else if (!info.isFile() || (info.mode & 0o777) !== 0o400) {
      throw new Error(`candidate deployment config ${path} must be a private read-only regular file`)
    }
  }
}

export async function verifyExecutingReleaseTool(expectedDigest, executingPath = process.argv[1]) {
  if (!shaPattern.test(String(expectedDigest || ''))) throw new Error('release tool digest is invalid')
  const path = resolve(String(executingPath || ''))
  const info = await lstat(path)
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('executing release tool must be a regular non-symlink file')
  if (sha256(await readFile(path)) !== expectedDigest) throw new Error('executing release tool differs from the receipted release tool')
}

export function validateProjectServiceInventory(entries, { requireExact = true } = {}) {
  if (!Array.isArray(entries)) throw new Error('Compose project container inventory is invalid')
  const ids = new Set()
  const services = new Set()
  for (const entry of entries) {
    const id = String(entry?.id || '').trim()
    const service = String(entry?.service || '').trim()
    if (!id || ids.has(id)) throw new Error('Compose project container inventory repeats or omits a container ID')
    if (!Object.hasOwn(serviceRoles, service)) throw new Error(`Compose project contains unexpected or orphan service ${service || '<missing>'}`)
    if (services.has(service)) throw new Error(`Compose project contains duplicate containers for service ${service}`)
    ids.add(id)
    services.add(service)
  }
  if (requireExact && JSON.stringify([...services].sort()) !== JSON.stringify(expectedServiceNames)) {
    throw new Error('Compose project service inventory is not exact')
  }
  return Object.fromEntries(entries.map(entry => [entry.service, entry.id]))
}

function exactObjectKeys(value, expected, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value) ||
      JSON.stringify(Object.keys(value).sort()) !== JSON.stringify([...expected].sort())) {
    throw new Error(`${label} inventory is not exact`)
  }
}

function emptyObject(value, label) {
  if (value === undefined || value === null) return
  if (typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== 0) {
    throw new Error(`${label} must remain empty`)
  }
}

function exactStringSequence(value, expected, label, { ordered = false } = {}) {
  const actual = value === undefined || value === null ? [] : value
  if (!Array.isArray(actual) || actual.some(item => typeof item !== 'string')) throw new Error(`${label} is invalid`)
  const normalizedActual = ordered ? actual : [...actual].sort()
  const normalizedExpected = ordered ? expected : [...expected].sort()
  if (JSON.stringify(normalizedActual) !== JSON.stringify(normalizedExpected)) throw new Error(`${label} differs from the approved topology`)
}

function exactStringSequenceOneOf(value, approved, label, { ordered = false } = {}) {
  const actual = value === undefined || value === null ? [] : value
  if (!Array.isArray(actual) || actual.some(item => typeof item !== 'string')) throw new Error(`${label} is invalid`)
  const normalizedActual = ordered ? actual : [...actual].sort()
  const matches = approved.some(expected => {
    const normalizedExpected = ordered ? expected : [...expected].sort()
    return JSON.stringify(normalizedActual) === JSON.stringify(normalizedExpected)
  })
  if (!matches) throw new Error(`${label} differs from the approved topology`)
}

function requireInheritedImageField(value, label) {
  if (value !== undefined && value !== null) {
    throw new Error(`${label} must remain inherited from the receipted image`)
  }
}

function exactSecurityOptions(value, expected, label) {
  const options = value === undefined || value === null ? [] : value
  if (!Array.isArray(options) || options.some(item => typeof item !== 'string')) throw new Error(`${label} is invalid`)
  const normalize = option => /^no-new-privileges(?:=|:)true$/i.test(option) ? 'no-new-privileges:true' : option
  if (JSON.stringify(options.map(normalize).sort()) !== JSON.stringify([...expected].sort())) {
    throw new Error(`${label} differs from the approved topology`)
  }
}

export function validateRendererRuntimeConfinement(inspect, procStatus, expectedSeccompProfile) {
  if (!inspect || typeof inspect !== 'object' || Array.isArray(inspect)) {
    throw new Error('running render-runner inspect is invalid')
  }
  if (inspect.AppArmorProfile !== rendererAppArmorProfile) {
    throw new Error('running render-runner AppArmor profile differs from the release policy')
  }
  const host = inspect.HostConfig || {}
  const securityOptions = Array.isArray(host.SecurityOpt) ? host.SecurityOpt : []
  const normalizedOptions = securityOptions.map(option => /^no-new-privileges(?:=|:)true$/i.test(option)
    ? 'no-new-privileges:true' : option)
  if (securityOptions.some(option => typeof option !== 'string') || securityOptions.length !== 3 ||
      !normalizedOptions.includes(`apparmor=${rendererAppArmorProfile}`) ||
      !normalizedOptions.includes('no-new-privileges:true')) {
    throw new Error('running render-runner security options differ from the release policy')
  }
  const seccompOptions = normalizedOptions.filter(option => option.startsWith('seccomp='))
  if (seccompOptions.length !== 1 || seccompOptions[0] === 'seccomp=unconfined' ||
      !expectedSeccompProfile || typeof expectedSeccompProfile !== 'object' || Array.isArray(expectedSeccompProfile)) {
    throw new Error('running render-runner seccomp option is not an exact confined profile')
  }
  let loadedSeccomp
  try {
    loadedSeccomp = JSON.parse(seccompOptions[0].slice('seccomp='.length))
  } catch {
    throw new Error('running render-runner seccomp option does not attest the loaded JSON profile')
  }
  if (JSON.stringify(loadedSeccomp) !== JSON.stringify(expectedSeccompProfile)) {
    throw new Error('running render-runner loaded seccomp profile differs from the release input')
  }
  exactStringSequence(host.CapDrop, ['ALL'], 'running render-runner dropped capabilities')
  exactStringSequence(host.CapAdd, [], 'running render-runner added capabilities')
  if (host.Privileged === true || host.ReadonlyRootfs !== true || String(inspect.Config?.User || '') !== '65532:65532') {
    throw new Error('running render-runner privilege, filesystem, or user boundary differs from the release policy')
  }
  const networkNames = Object.keys(inspect.NetworkSettings?.Networks || {}).sort()
  if (JSON.stringify(networkNames) !== JSON.stringify(['digitalocean_render_internal'])) {
    throw new Error('running render-runner network attachment differs from the internal-only release policy')
  }
  const status = String(procStatus || '')
  const exactStatus = (name, pattern) => {
    const match = new RegExp(`^${name}:\\s*(.+)$`, 'm').exec(status)
    if (!match || !pattern.test(match[1].trim())) throw new Error(`running render-runner ${name} status is not confined`)
  }
  exactStatus('Uid', /^65532\s+65532\s+65532\s+65532$/)
  exactStatus('Gid', /^65532\s+65532\s+65532\s+65532$/)
  for (const name of ['CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb']) exactStatus(name, /^0+$/)
  exactStatus('NoNewPrivs', /^1$/)
  exactStatus('Seccomp', /^2$/)
  return inspect
}

function byteSize(value, label) {
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) return value
  const match = /^(\d+)(b|k|kb|kib|m|mb|mib|g|gb|gib)?$/i.exec(String(value || '').trim())
  if (!match) throw new Error(`${label} is invalid`)
  const unit = String(match[2] || 'b').toLowerCase()
  const multiplier = { b: 1, k: 1024, kb: 1024, kib: 1024, m: 1024 ** 2, mb: 1024 ** 2,
    mib: 1024 ** 2, g: 1024 ** 3, gb: 1024 ** 3, gib: 1024 ** 3 }[unit]
  const result = Number(match[1]) * multiplier
  if (!Number.isSafeInteger(result)) throw new Error(`${label} is invalid`)
  return result
}

function exactByteSize(value, expected, label) {
  if (expected === null) {
    if (value !== undefined && value !== null) throw new Error(`${label} must remain unset`)
  } else if (!(Array.isArray(expected) ? expected : [expected]).includes(byteSize(value, label))) {
    throw new Error(`${label} differs from the approved topology`)
  }
}

function exactDuration(value, expectedText, expectedNanoseconds, label) {
  if (String(value) !== expectedText && String(value) !== String(expectedNanoseconds)) {
    throw new Error(`${label} differs from the approved topology`)
  }
}

function validateServiceNetworks(serviceName, value, expected) {
  if (expected.length === 0) {
    if (value !== undefined && value !== null &&
        (typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== 0)) {
      throw new Error(`rendered candidate Compose service ${serviceName} network attachment is not exact`)
    }
    return
  }
  exactObjectKeys(value, expected, `rendered candidate Compose service ${serviceName} network attachment`)
  for (const [network, attachment] of Object.entries(value)) {
    if (attachment !== null && (typeof attachment !== 'object' || Array.isArray(attachment) || Object.keys(attachment).length !== 0)) {
      throw new Error(`rendered candidate Compose service ${serviceName} network ${network} options are not exact`)
    }
  }
}

function portRange(value, label) {
  const match = /^(\d+)(?:-(\d+))?$/.exec(String(value || '').trim())
  if (!match) throw new Error(`${label} is invalid`)
  const first = Number(match[1])
  const last = Number(match[2] || match[1])
  if (!Number.isInteger(first) || !Number.isInteger(last) || first < 1 || last > 65535 || last < first) {
    throw new Error(`${label} is invalid`)
  }
  return { first, last }
}

function expandedPort(port, serviceName) {
  if (!port || typeof port !== 'object' || Array.isArray(port)) {
    throw new Error(`rendered candidate Compose service ${serviceName} port mapping is not canonical`)
  }
  const allowed = new Set(['mode', 'target', 'published', 'protocol', 'host_ip'])
  for (const key of Object.keys(port)) if (!allowed.has(key)) {
    throw new Error(`rendered candidate Compose service ${serviceName} port mapping has unsupported field ${key}`)
  }
  const published = String(port.published || '').trim()
  const target = String(port.target || '').trim()
  const protocol = String(port.protocol || 'tcp').toLowerCase()
  const mode = String(port.mode || 'ingress').toLowerCase()
  const hostIP = String(port.host_ip || '').trim()
  if (!published || !['tcp', 'udp'].includes(protocol) || mode !== 'ingress' || hostIP) {
    throw new Error(`rendered candidate Compose service ${serviceName} port mapping is invalid`)
  }
  const targetRange = portRange(target, `rendered candidate Compose service ${serviceName} target port`)
  const publishedRange = portRange(published, `rendered candidate Compose service ${serviceName} published port`)
  if (targetRange.last - targetRange.first !== publishedRange.last - publishedRange.first) {
    throw new Error(`rendered candidate Compose service ${serviceName} port range cardinality is invalid`)
  }
  return Array.from({ length: targetRange.last - targetRange.first + 1 }, (_, index) =>
    `${publishedRange.first + index}:${targetRange.first + index}/${protocol}`)
}

function validateServicePorts(serviceName, value, expected) {
  const ports = value === undefined || value === null ? [] : value
  if (!Array.isArray(ports)) throw new Error(`rendered candidate Compose service ${serviceName} ports are invalid`)
  const actual = ports.flatMap(port => expandedPort(port, serviceName)).sort()
  const expectedPorts = expected.flatMap(description => {
    const match = /^(\d+(?:-\d+)?):(\d+(?:-\d+)?)\/(tcp|udp)$/.exec(description)
    if (!match) throw new Error(`approved Compose service ${serviceName} port policy is invalid`)
    return expandedPort({ published: match[1], target: match[2], protocol: match[3], mode: 'ingress' }, serviceName)
  }).sort()
  if (JSON.stringify(actual) !== JSON.stringify(expectedPorts)) {
    throw new Error(`rendered candidate Compose service ${serviceName} ports differ from the approved topology`)
  }
}

function normalizedMount(mount, serviceName) {
  if (!mount || typeof mount !== 'object' || Array.isArray(mount)) {
    throw new Error(`rendered candidate Compose service ${serviceName} mount is not canonical`)
  }
  const allowed = new Set(['type', 'source', 'target', 'read_only', 'bind', 'volume', 'consistency'])
  for (const key of Object.keys(mount)) if (!allowed.has(key)) {
    throw new Error(`rendered candidate Compose service ${serviceName} mount has unsupported field ${key}`)
  }
  const type = String(mount.type || '')
  const source = String(mount.source || '')
  const target = String(mount.target || '')
  if (!['bind', 'volume'].includes(type) || !source || !target.startsWith('/') ||
      (mount.read_only !== undefined && typeof mount.read_only !== 'boolean') || mount.consistency) {
    throw new Error(`rendered candidate Compose service ${serviceName} mount is invalid`)
  }
  if (type === 'bind') {
    if (mount.volume !== undefined) throw new Error(`rendered candidate Compose service ${serviceName} bind mount is invalid`)
    const bind = mount.bind
    if (bind !== undefined && (!bind || typeof bind !== 'object' || Array.isArray(bind) ||
        Object.keys(bind).some(key => key !== 'create_host_path') ||
        (bind.create_host_path !== undefined && bind.create_host_path !== true))) {
      throw new Error(`rendered candidate Compose service ${serviceName} bind mount options are not exact`)
    }
  } else {
    if (mount.bind !== undefined) throw new Error(`rendered candidate Compose service ${serviceName} volume mount is invalid`)
    const volume = mount.volume
    if (volume !== undefined && (!volume || typeof volume !== 'object' || Array.isArray(volume) ||
        Object.keys(volume).some(key => key !== 'nocopy') ||
        (volume.nocopy !== undefined && volume.nocopy !== false))) {
      throw new Error(`rendered candidate Compose service ${serviceName} volume mount options are not exact`)
    }
  }
  return { type, source, target, readOnly: mount.read_only === true }
}

function validateServiceMounts(serviceName, value, expected) {
  const mounts = value === undefined || value === null ? [] : value
  if (!Array.isArray(mounts)) throw new Error(`rendered candidate Compose service ${serviceName} mounts are invalid`)
  const actual = mounts.map(mount => normalizedMount(mount, serviceName)).sort((left, right) => left.target.localeCompare(right.target))
  const normalizedExpected = [...expected].sort((left, right) => left.target.localeCompare(right.target))
  if (JSON.stringify(actual) !== JSON.stringify(normalizedExpected)) {
    throw new Error(`rendered candidate Compose service ${serviceName} mounts differ from the approved topology`)
  }
}

function validateHealthcheck(serviceName, value, expected) {
  if (!expected) {
    if (value !== undefined && value !== null) throw new Error(`rendered candidate Compose service ${serviceName} healthcheck must remain unset`)
    return
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`rendered candidate Compose service ${serviceName} healthcheck is invalid`)
  const allowed = new Set(['test', 'interval', 'timeout', 'retries', 'start_period', 'start_interval', 'disable'])
  for (const key of Object.keys(value)) if (!allowed.has(key)) throw new Error(`rendered candidate Compose service ${serviceName} healthcheck has unsupported field ${key}`)
  if (expected.tests) {
    exactStringSequenceOneOf(value.test, expected.tests, `rendered candidate Compose service ${serviceName} healthcheck command`, { ordered: true })
  } else {
    exactStringSequence(value.test, expected.test, `rendered candidate Compose service ${serviceName} healthcheck command`, { ordered: true })
  }
  exactDuration(value.interval, expected.interval[0], expected.interval[1], `rendered candidate Compose service ${serviceName} healthcheck interval`)
  exactDuration(value.timeout, expected.timeout[0], expected.timeout[1], `rendered candidate Compose service ${serviceName} healthcheck timeout`)
  exactDuration(value.start_period, expected.startPeriod[0], expected.startPeriod[1], `rendered candidate Compose service ${serviceName} healthcheck start period`)
  if (value.retries !== expected.retries || value.disable === true ||
      (value.start_interval !== undefined && String(value.start_interval) !== '0' && String(value.start_interval) !== '0s')) {
    throw new Error(`rendered candidate Compose service ${serviceName} healthcheck differs from the approved topology`)
  }
}

function validateDependencies(serviceName, value, expected) {
  if (Object.keys(expected).length === 0) {
    if (value !== undefined && value !== null &&
        (typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length !== 0)) {
      throw new Error(`rendered candidate Compose service ${serviceName} dependencies must remain empty`)
    }
    return
  }
  exactObjectKeys(value, Object.keys(expected), `rendered candidate Compose service ${serviceName} dependency`)
  for (const [dependency, condition] of Object.entries(expected)) {
    const actual = value[dependency]
    if (!actual || typeof actual !== 'object' || Array.isArray(actual) ||
        Object.keys(actual).some(key => !['condition', 'required', 'restart'].includes(key)) ||
        actual.condition !== condition || actual.required === false || actual.restart === true) {
      throw new Error(`rendered candidate Compose service ${serviceName} dependency ${dependency} differs from the approved topology`)
    }
  }
}

function environmentMap(value, serviceName) {
  if (value === undefined || value === null) return {}
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`rendered candidate Compose service ${serviceName} environment is not canonical`)
  return value
}

function validateExactEnvironment(serviceName, value, expectedKeys, fixed = {}) {
  const environment = environmentMap(value, serviceName)
  exactObjectKeys(environment, expectedKeys, `rendered candidate Compose service ${serviceName} environment`)
  for (const [name, expected] of Object.entries(fixed)) {
    if (String(environment[name] || '') !== expected) throw new Error(`rendered candidate Compose service ${serviceName} environment ${name} differs from the approved topology`)
  }
  return environment
}

function validateEnvFiles(serviceName, value, baseEnv, allowed, required) {
  if (value === undefined || value === null) {
    if (allowed && required) throw new Error(`rendered candidate Compose service ${serviceName} env-file attachment is not exact`)
    return
  }
  if (!allowed || !baseEnv || !Array.isArray(value) || value.length !== 1) {
    throw new Error(`rendered candidate Compose service ${serviceName} env-file attachment is not exact`)
  }
  const item = value[0]
  const path = typeof item === 'string' ? item : item?.path
  if (!isAbsolute(String(path || '')) || resolve(path) !== resolve(baseEnv) ||
      (typeof item === 'object' && item !== null &&
       (Object.keys(item).some(key => !['path', 'required', 'format'].includes(key)) || item.required === false || item.format))) {
    throw new Error(`rendered candidate Compose service ${serviceName} env-file attachment is not exact`)
  }
}

function validateBuild(serviceName, value, receipt, candidateRoot, dockerfile) {
  if (!dockerfile) {
    if (value !== undefined && value !== null) throw new Error(`rendered candidate Compose service ${serviceName} build configuration must remain unset`)
    return
  }
  if (!value || typeof value !== 'object' || Array.isArray(value) ||
      Object.keys(value).some(key => !['context', 'dockerfile', 'args'].includes(key))) {
    throw new Error(`rendered candidate Compose service ${serviceName} build configuration is not exact`)
  }
  const context = String(value.context || '')
  const dockerfilePath = String(value.dockerfile || '')
  if (!isAbsolute(context) || resolve(context) !== candidateRoot ||
      resolve(context, dockerfilePath) !== resolve(candidateRoot, dockerfile)) {
    throw new Error(`rendered candidate Compose service ${serviceName} build paths differ from the sealed candidate`)
  }
  const expectedArgs = {
    BONFIRE_RELEASE_COMMIT: receipt.source.releaseCommit,
    BONFIRE_GIT_TREE_DIGEST: receipt.source.gitTreeDigest,
    BONFIRE_BUILD_CONFIG_SHA256: receipt.source.buildConfigSha256,
    BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256: receipt.source.transitiveInputsSha256,
    BONFIRE_SOURCE_ARCHIVE_SHA256: receipt.source.sourceArchiveSha256,
    BONFIRE_BUILD_INPUT_MANIFEST_SHA256: receipt.buildInputManifestSha256
  }
  exactObjectKeys(value.args, Object.keys(expectedArgs), `rendered candidate Compose service ${serviceName} build argument`)
  for (const [name, expected] of Object.entries(expectedArgs)) if (String(value.args[name] || '') !== expected) {
    throw new Error(`rendered candidate Compose service ${serviceName} build argument ${name} differs from the release receipt`)
  }
}

function validateTopLevelResources(config) {
  exactObjectKeys(config.networks, Object.keys(expectedProjectNetworks), 'rendered candidate Compose top-level network')
  for (const [key, expected] of Object.entries(expectedProjectNetworks)) {
    const network = config.networks[key]
    if (!network || typeof network !== 'object' || Array.isArray(network) ||
        Object.keys(network).some(name => !['name', 'internal', 'external', 'attachable', 'driver', 'driver_opts', 'enable_ipv4', 'enable_ipv6', 'ipam', 'labels'].includes(name)) ||
        network.name !== expected.name || network.external === true || Boolean(network.internal) !== expected.internal ||
        network.attachable === true || (network.driver !== undefined && network.driver !== 'bridge') ||
        network.enable_ipv4 === false || network.enable_ipv6 === true) {
      throw new Error(`rendered candidate Compose top-level network ${key} differs from the approved topology`)
    }
    emptyObject(network.driver_opts, `rendered candidate Compose top-level network ${key} driver options`)
    emptyObject(network.ipam, `rendered candidate Compose top-level network ${key} IPAM`)
    emptyObject(network.labels, `rendered candidate Compose top-level network ${key} labels`)
  }
  exactObjectKeys(config.volumes, Object.keys(expectedProjectVolumes), 'rendered candidate Compose top-level volume')
  for (const [key, expected] of Object.entries(expectedProjectVolumes)) {
    const volume = config.volumes[key]
    if (!volume || typeof volume !== 'object' || Array.isArray(volume) ||
        Object.keys(volume).some(name => !['name', 'external', 'driver', 'driver_opts', 'labels'].includes(name)) ||
        volume.name !== expected.name || Boolean(volume.external) !== expected.external ||
        (expected.external && volume.driver !== undefined && volume.driver !== null) ||
        (!expected.external && volume.driver !== undefined && volume.driver !== 'local')) {
      throw new Error(`rendered candidate Compose top-level volume ${key} differs from the approved topology`)
    }
    emptyObject(volume.driver_opts, `rendered candidate Compose top-level volume ${key} driver options`)
    emptyObject(volume.labels, `rendered candidate Compose top-level volume ${key} labels`)
  }
  emptyObject(config.secrets, 'rendered candidate Compose top-level secrets')
  emptyObject(config.configs, 'rendered candidate Compose top-level configs')
}

function composeTopologyContext(config, supplied = {}) {
  const caddyMount = Array.isArray(config.services?.caddy?.volumes)
    ? config.services.caddy.volumes.find(mount => mount?.target === '/etc/caddy/Caddyfile') : null
  const caddyfile = String(supplied.candidateCaddyfile || caddyMount?.source || '')
  if (!isAbsolute(caddyfile)) throw new Error('rendered candidate Compose Caddyfile source is not an absolute sealed path')
  const derivedRoot = dirname(dirname(dirname(caddyfile)))
  const candidateRoot = resolve(String(supplied.candidateRoot || derivedRoot))
  if (resolve(caddyfile) !== resolve(candidateRoot, 'deploy/digitalocean/Caddyfile') ||
      (caddyMount && resolve(String(caddyMount.source || '')) !== resolve(caddyfile))) {
    throw new Error('rendered candidate Compose Caddyfile source differs from the sealed candidate')
  }
  return { candidateRoot, candidateCaddyfile: resolve(caddyfile), baseEnv: supplied.baseEnv ? resolve(supplied.baseEnv) : '',
    requireEnvFiles: supplied.requireEnvFiles === true }
}

export function validateRenderedComposeConfig(config, receipt, suppliedTopology = {}) {
  if (!config || typeof config !== 'object' || Array.isArray(config) || config.name !== 'digitalocean' ||
      !config.services || typeof config.services !== 'object' || Array.isArray(config.services)) {
    throw new Error('rendered candidate Compose configuration is invalid')
  }
  const allowedTopLevel = new Set(['name', 'services', 'networks', 'volumes', 'secrets', 'configs'])
  for (const key of Object.keys(config)) if (!allowedTopLevel.has(key)) throw new Error(`rendered candidate Compose has unsupported top-level field ${key}`)
  exactObjectKeys(config.services, expectedServiceNames, 'rendered candidate Compose service')
  validateTopLevelResources(config)
  const topology = composeTopologyContext(config, suppliedTopology)
  const expectedImages = {
    meetingassist: receipt.images.meetingassist.imageId,
    'render-runner': receipt.images.renderRunner.imageId,
    'render-queue-init': receipt.images.renderRunner.imageId,
    'canonical-postgres': receipt.sidecars.canonicalPostgres.imageReference,
    coturn: receipt.sidecars.coturn.imageReference,
    caddy: receipt.sidecars.caddy.imageReference
  }
  const allowedServiceFields = new Set(['image', 'build', 'environment', 'env_file', 'volumes', 'ports', 'healthcheck',
    'mem_limit', 'networks', 'depends_on', 'restart', 'command', 'shm_size', 'profiles', 'user', 'entrypoint',
    'network_mode', 'cap_drop', 'cap_add', 'read_only', 'security_opt', 'tmpfs', 'pids_limit', 'deploy', 'scale'])
  const servicePolicy = {
    meetingassist: {
      profiles: [], networks: ['default', 'render_internal'], networkMode: '', restart: 'unless-stopped', user: '', readOnly: false,
      capAdd: [], capDrop: [], securityOpt: [], ports: ['40000-40100:40000-40100/udp'], memory: [1024 ** 3, 3 * 1024 ** 3], shm: null, pids: null,
      mounts: [
        { type: 'volume', source: 'meeting_data', target: '/app/data', readOnly: false },
        { type: 'volume', source: 'usage_ledger', target: '/app/data/usage', readOnly: false },
        { type: 'volume', source: 'codex_queue', target: '/app/codex-queue', readOnly: false },
        { type: 'volume', source: 'render_queue', target: '/app/render-queue', readOnly: false }
      ], dependencies: { 'canonical-postgres': 'service_healthy' }, dockerfile: 'Dockerfile'
    },
    'canonical-postgres': {
      profiles: [], networks: ['default'], networkMode: '', restart: 'unless-stopped', user: '', readOnly: false,
      capAdd: [], capDrop: [], securityOpt: [], ports: [], memory: 256 * 1024 ** 2, shm: 64 * 1024 ** 2, pids: null,
      mounts: [{ type: 'volume', source: 'canonical_postgres', target: '/var/lib/postgresql/data', readOnly: false }],
      dependencies: {}, dockerfile: ''
    },
    'render-queue-init': {
      profiles: ['render'], networks: [], networkMode: 'none', restart: 'no', user: '0:0', readOnly: true,
      capAdd: ['CHOWN', 'DAC_OVERRIDE'], capDrop: ['ALL'], securityOpt: [], ports: [], memory: null, shm: null, pids: null,
      mounts: [{ type: 'volume', source: 'render_queue', target: '/app/render-queue', readOnly: false }],
      dependencies: {}, dockerfile: 'Dockerfile.render'
    },
    'render-runner': {
      profiles: ['render'], networks: ['render_internal'], networkMode: '', restart: 'unless-stopped', user: '', readOnly: true,
      capAdd: [], capDrop: ['ALL'], securityOpt: rendererSecurityOptions, ports: [], memory: 1024 ** 3,
      shm: 256 * 1024 ** 2, pids: 256,
      mounts: [{ type: 'volume', source: 'render_queue', target: '/app/render-queue', readOnly: false }],
      dependencies: { meetingassist: 'service_healthy', 'render-queue-init': 'service_completed_successfully' }, dockerfile: 'Dockerfile.render'
    },
    coturn: {
      profiles: [], networks: ['default'], networkMode: '', restart: 'unless-stopped', user: '', readOnly: false,
      capAdd: [], capDrop: [], securityOpt: [], ports: ['3478:3478/tcp', '3478:3478/udp', '49160-49200:49160-49200/udp'],
      memory: null, shm: null, pids: null, mounts: [], dependencies: {}, dockerfile: ''
    },
    caddy: {
      profiles: [], networks: ['default'], networkMode: '', restart: 'unless-stopped', user: '', readOnly: false,
      capAdd: [], capDrop: [], securityOpt: [], ports: ['80:80/tcp', '443:443/tcp'], memory: null, shm: null, pids: null,
      mounts: [
        { type: 'bind', source: topology.candidateCaddyfile, target: '/etc/caddy/Caddyfile', readOnly: true },
        { type: 'volume', source: 'caddy_data', target: '/data', readOnly: false },
        { type: 'volume', source: 'caddy_config', target: '/config', readOnly: false }
      ], dependencies: { meetingassist: 'service_started' }, dockerfile: ''
    }
  }

  for (const serviceName of expectedServiceNames) {
    const service = config.services[serviceName]
    const policy = servicePolicy[serviceName]
    if (!service || typeof service !== 'object' || Array.isArray(service)) throw new Error(`rendered candidate Compose service ${serviceName} is invalid`)
    for (const key of Object.keys(service)) if (!allowedServiceFields.has(key)) {
      throw new Error(`rendered candidate Compose service ${serviceName} has unsupported field ${key}`)
    }
    if (String(service.image || '').toLowerCase() !== String(expectedImages[serviceName]).toLowerCase()) {
      throw new Error(`rendered candidate Compose service ${serviceName} image differs from release receipt`)
    }
    if (service.scale !== undefined && service.scale !== 1) throw new Error(`rendered candidate Compose service ${serviceName} scale is not exactly one`)
    const deploy = service.deploy
    if (deploy !== undefined && (!deploy || typeof deploy !== 'object' || Array.isArray(deploy) ||
        Object.keys(deploy).some(key => !['mode', 'replicas'].includes(key)))) {
      throw new Error(`rendered candidate Compose service ${serviceName} deploy configuration is invalid`)
    }
    if (deploy?.mode !== undefined && deploy.mode !== 'replicated') throw new Error(`rendered candidate Compose service ${serviceName} deploy mode is not singleton-compatible`)
    if (deploy?.replicas !== undefined && deploy.replicas !== 1) throw new Error(`rendered candidate Compose service ${serviceName} replica count is not exactly one`)
    exactStringSequence(service.profiles, policy.profiles, `rendered candidate Compose service ${serviceName} profiles`)
    validateServiceNetworks(serviceName, service.networks, policy.networks)
    if (String(service.network_mode || '') !== policy.networkMode) throw new Error(`rendered candidate Compose service ${serviceName} network mode differs from the approved topology`)
    validateServicePorts(serviceName, service.ports, policy.ports)
    validateServiceMounts(serviceName, service.volumes, policy.mounts)
    exactStringSequence(service.cap_add, policy.capAdd, `rendered candidate Compose service ${serviceName} added capabilities`)
    exactStringSequence(service.cap_drop, policy.capDrop, `rendered candidate Compose service ${serviceName} dropped capabilities`)
    exactSecurityOptions(service.security_opt, policy.securityOpt, `rendered candidate Compose service ${serviceName} security options`)
    if (String(service.restart || '') !== policy.restart || String(service.user || '') !== policy.user ||
        Boolean(service.read_only) !== policy.readOnly) {
      throw new Error(`rendered candidate Compose service ${serviceName} restart/user/read-only policy differs from the approved topology`)
    }
    exactByteSize(service.mem_limit, policy.memory, `rendered candidate Compose service ${serviceName} memory limit`)
    exactByteSize(service.shm_size, policy.shm, `rendered candidate Compose service ${serviceName} shared-memory limit`)
    if (policy.pids === null ? service.pids_limit !== undefined && service.pids_limit !== null : service.pids_limit !== policy.pids) {
      throw new Error(`rendered candidate Compose service ${serviceName} PID limit differs from the approved topology`)
    }
    validateDependencies(serviceName, service.depends_on, policy.dependencies)
    validateBuild(serviceName, service.build, receipt, topology.candidateRoot, policy.dockerfile)
    validateEnvFiles(serviceName, service.env_file, topology.baseEnv, ['meetingassist', 'caddy'].includes(serviceName), topology.requireEnvFiles)
  }

  const requiredAppEnvironment = {
    ...environmentValues(receipt), BONFIRE_RELEASE_BUNDLE_SHA256: receipt.bundleSha256,
    BONFIRE_CODEX_QUEUE_PATH: '/app/codex-queue/jobs', BONFIRE_CODEX_HEARTBEAT_PATH: '/app/codex-queue/heartbeat.json',
    BONFIRE_RENDER_QUEUE_PATH: '/app/render-queue/jobs', BONFIRE_RENDER_HEARTBEAT_PATH: '/app/render-queue/heartbeat.json'
  }
  validateExactEnvironment('meetingassist', config.services.meetingassist.environment,
    Object.keys(requiredAppEnvironment), requiredAppEnvironment)
  const postgresEnvironment = validateExactEnvironment('canonical-postgres', config.services['canonical-postgres'].environment,
    ['POSTGRES_DB', 'POSTGRES_USER', 'POSTGRES_PASSWORD'], { POSTGRES_DB: 'bonfire', POSTGRES_USER: 'bonfire' })
  if (!String(postgresEnvironment.POSTGRES_PASSWORD || '')) throw new Error('rendered candidate Compose canonical Postgres password is empty')
  validateExactEnvironment('render-queue-init', config.services['render-queue-init'].environment, [])
  const runnerEnvironment = validateExactEnvironment('render-runner', config.services['render-runner'].environment,
    ['BONFIRE_RUNNER_TOKEN', 'BONFIRE_RENDER_QUEUE_PATH', 'BONFIRE_RENDER_HEARTBEAT_PATH', 'BONFIRE_RENDER_CALLBACK_URL',
      'BONFIRE_RENDER_TIMEOUT', 'BONFIRE_RENDER_MAX_HTML_BYTES', 'BONFIRE_RENDER_MAX_PDF_BYTES'], {
      BONFIRE_RENDER_QUEUE_PATH: '/app/render-queue/jobs', BONFIRE_RENDER_HEARTBEAT_PATH: '/app/render-queue/heartbeat.json',
      BONFIRE_RENDER_CALLBACK_URL: 'http://meetingassist:3000/internal/render/jobs/result'
    })
  for (const name of ['BONFIRE_RUNNER_TOKEN', 'BONFIRE_RENDER_TIMEOUT', 'BONFIRE_RENDER_MAX_HTML_BYTES', 'BONFIRE_RENDER_MAX_PDF_BYTES']) {
    if (!String(runnerEnvironment[name] || '')) throw new Error(`rendered candidate Compose render-runner environment ${name} is empty`)
  }
  validateExactEnvironment('coturn', config.services.coturn.environment, [])
  validateExactEnvironment('caddy', config.services.caddy.environment, [])

  validateHealthcheck('meetingassist', config.services.meetingassist.healthcheck, {
    // The current retained rollback window still contains the exact /readyz
    // transition release. Keep both reviewed commands admissible until one
    // /livez release is active with another /livez release as its predecessor;
    // the next strict successor can then remove the compatibility branch.
    tests: [
      ['CMD', 'curl', '-fsS', 'http://127.0.0.1:3000/readyz'],
      ['CMD', 'curl', '-fsS', 'http://127.0.0.1:3000/livez']
    ], interval: ['30s', 30_000_000_000],
    timeout: ['5s', 5_000_000_000], startPeriod: ['5m0s', 300_000_000_000], retries: 3
  })
  validateHealthcheck('canonical-postgres', config.services['canonical-postgres'].healthcheck, {
    test: ['CMD-SHELL', 'pg_isready -U bonfire -d bonfire'], interval: ['5s', 5_000_000_000],
    timeout: ['3s', 3_000_000_000], startPeriod: ['15s', 15_000_000_000], retries: 20
  })
  for (const serviceName of ['render-queue-init', 'render-runner', 'coturn', 'caddy']) validateHealthcheck(serviceName, config.services[serviceName].healthcheck, null)

  requireInheritedImageField(config.services.meetingassist.command, 'rendered candidate Compose meetingassist command')
  requireInheritedImageField(config.services.meetingassist.entrypoint, 'rendered candidate Compose meetingassist entrypoint')
  requireInheritedImageField(config.services['canonical-postgres'].entrypoint, 'rendered candidate Compose canonical-postgres entrypoint')
  exactStringSequence(config.services['canonical-postgres'].command,
    ['postgres', '-c', 'max_connections=30', '-c', 'shared_buffers=64MB', '-c', 'effective_cache_size=128MB', '-c', 'work_mem=2MB', '-c', 'maintenance_work_mem=32MB'],
    'rendered candidate Compose canonical-postgres command', { ordered: true })
  exactStringSequence(config.services['render-queue-init'].entrypoint, ['/bin/sh', '-eu', '-c'], 'rendered candidate Compose render-queue-init entrypoint', { ordered: true })
  exactStringSequenceOneOf(config.services['render-queue-init'].command,
    [
      ['install -d -o 65532 -g 65532 -m 0700 /app/render-queue /app/render-queue/jobs'],
      ["mkdir -p /app/render-queue/jobs && chown 0:0 /app/render-queue /app/render-queue/jobs && chmod 2770 /app/render-queue /app/render-queue/jobs && chown 65532:65532 /app/render-queue /app/render-queue/jobs && find /app/render-queue/jobs -xdev -maxdepth 1 -type f -name '*.json' -exec chown 0:0 {} + -exec chmod 0660 {} + -exec chown 65532:65532 {} +"]
    ],
    'rendered candidate Compose render-queue-init command', { ordered: true })
  for (const serviceName of ['render-runner', 'caddy']) {
    requireInheritedImageField(config.services[serviceName].command, `rendered candidate Compose ${serviceName} command`)
    requireInheritedImageField(config.services[serviceName].entrypoint, `rendered candidate Compose ${serviceName} entrypoint`)
  }
  const coturnCommand = config.services.coturn.command
  if (!Array.isArray(coturnCommand) || coturnCommand.length !== 12 ||
      JSON.stringify(coturnCommand.slice(0, 4)) !== JSON.stringify(['-n', '--log-file=stdout', '--fingerprint', '--use-auth-secret']) ||
      !/^--static-auth-secret=.+/.test(coturnCommand[4]) || !/^--realm=.+/.test(coturnCommand[5]) ||
      !/^--external-ip=.+/.test(coturnCommand[6]) ||
      JSON.stringify(coturnCommand.slice(7)) !== JSON.stringify(['--listening-port=3478', '--min-port=49160', '--max-port=49200', '--no-cli', '--no-multicast-peers'])) {
    throw new Error('rendered candidate Compose coturn command differs from the approved topology')
  }
  requireInheritedImageField(config.services.coturn.entrypoint, 'rendered candidate Compose coturn entrypoint')
  const tmpfs = config.services['render-runner'].tmpfs
  if (!Array.isArray(tmpfs) || tmpfs.length !== 1 || typeof tmpfs[0] !== 'string') throw new Error('rendered candidate Compose render-runner tmpfs is not exact')
  const [tmpfsTarget, ...tmpfsOptions] = tmpfs[0].split(':')
  const rawTmpfsOptions = tmpfsOptions.join(':').split(',').filter(Boolean)
  const optionSet = new Set(rawTmpfsOptions)
  const sizeOption = [...optionSet].find(value => value.startsWith('size='))
  optionSet.delete(sizeOption)
  if (optionSet.size + 1 !== rawTmpfsOptions.length || tmpfsTarget !== '/tmp' || !sizeOption ||
      byteSize(sizeOption.slice(5), 'render-runner tmpfs size') !== 512 * 1024 ** 2 ||
      JSON.stringify([...optionSet].sort()) !== JSON.stringify(['nodev', 'noexec', 'nosuid', 'rw'])) {
    throw new Error('rendered candidate Compose render-runner tmpfs differs from the approved topology')
  }
  for (const serviceName of ['meetingassist', 'canonical-postgres', 'render-queue-init', 'coturn', 'caddy']) {
    if (config.services[serviceName].tmpfs !== undefined && config.services[serviceName].tmpfs !== null) {
      throw new Error(`rendered candidate Compose service ${serviceName} tmpfs must remain unset`)
    }
  }
  return config
}

export function renderedComposeSha256(config) {
  return sha256(canonical(stableJSONValue(config)))
}

async function inspectProjectContainers() {
  const { stdout } = await execFileAsync('docker', ['container', 'ls', '--all', '--no-trunc', '--filter',
    'label=com.docker.compose.project=digitalocean', '--format', '{{.ID}}'], { maxBuffer: 4 << 20 })
  const ids = normalizeLines(stdout)
  const entries = []
  for (const id of ids) {
    const { stdout: raw } = await execFileAsync('docker', ['container', 'inspect', id], { maxBuffer: 16 << 20 })
    const inspect = parseJSON(raw, 'Docker project container inspect')[0]
    const labels = inspect?.Config?.Labels || {}
    if (labels['com.docker.compose.project'] !== 'digitalocean') throw new Error('Compose project container label differs from digitalocean')
    entries.push({
      id,
      service: labels['com.docker.compose.service'],
      configFiles: labels['com.docker.compose.project.config_files'],
      workingDir: labels['com.docker.compose.project.working_dir'],
      oneoff: labels['com.docker.compose.oneoff'],
      containerNumber: labels['com.docker.compose.container-number'],
      createdAt: inspect?.Created,
      networkIDs: Object.values(inspect?.NetworkSettings?.Networks || {}).map(network => String(network?.NetworkID || '')).filter(Boolean).sort(),
      volumeNames: (inspect?.Mounts || []).filter(mount => mount?.Type === 'volume').map(mount => String(mount?.Name || '')).filter(Boolean).sort()
    })
  }
  return entries
}

async function inspectProjectServiceInventory(requireExact) {
  const entries = await inspectProjectContainers()
  return validateProjectServiceInventory(entries, { requireExact })
}

export function projectContainerSnapshotSha256(entries) {
  validateProjectServiceInventory(entries)
  const snapshot = entries.map(entry => ({ id: String(entry.id), service: String(entry.service) }))
    .sort((left, right) => left.service.localeCompare(right.service) || left.id.localeCompare(right.id))
  return sha256(canonical(snapshot))
}

export function planRollbackProjectCleanup(baselineEntries, currentEntries, failedCandidateCompose, operationStartedAt) {
  validateProjectServiceInventory(baselineEntries)
  if (!Array.isArray(currentEntries)) throw new Error('failed-target Compose project container inventory is invalid')
  const failedComposePath = String(failedCandidateCompose || '').trim()
  const targetCompose = resolve(failedComposePath)
  const targetWorkingDir = dirname(targetCompose)
  const startedAt = Date.parse(String(operationStartedAt || ''))
  if (!isAbsolute(failedComposePath) || Number.isNaN(startedAt)) throw new Error('failed-target cleanup provenance is invalid')
  const baselineByID = new Map(baselineEntries.map(entry => [String(entry.id), String(entry.service)]))
  const currentIDs = new Set()
  const removals = []
  for (const entry of currentEntries) {
    const id = String(entry?.id || '').trim()
    const service = String(entry?.service || '').trim()
    if (!id || currentIDs.has(id)) throw new Error('failed-target Compose project inventory repeats or omits a container ID')
    currentIDs.add(id)
    if (baselineByID.has(id)) {
      if (baselineByID.get(id) !== service) throw new Error('baseline Compose project container identity changed during the release transaction')
      continue
    }
    const createdAt = Date.parse(String(entry?.createdAt || ''))
    const configFiles = String(entry?.configFiles || '').split(',').map(value => value.trim()).filter(Boolean)
    const workingDir = String(entry?.workingDir || '').trim()
    if (!service || !isAbsolute(workingDir) || Number.isNaN(createdAt) || createdAt <= startedAt ||
        String(entry?.oneoff || '').toLowerCase() !== 'false' ||
        !/^[1-9]\d*$/.test(String(entry?.containerNumber || '')) ||
        configFiles.length !== 1 || !isAbsolute(configFiles[0]) || resolve(configFiles[0]) !== targetCompose ||
        resolve(workingDir) !== targetWorkingDir) {
      throw new Error(`project container ${id} is not proven to have been created by the failed target transaction`)
    }
    removals.push(id)
  }
  return removals.sort()
}

export function projectResourceClaimsFromContainers(entries, containerIDs) {
  if (!Array.isArray(entries) || !Array.isArray(containerIDs)) throw new Error('failed-target resource claims are invalid')
  const byID = new Map(entries.map(entry => [String(entry?.id || ''), entry]))
  const networkIDs = new Set()
  const volumeNames = new Set()
  for (const id of containerIDs) {
    const entry = byID.get(String(id))
    if (!entry || !Array.isArray(entry.networkIDs) || !Array.isArray(entry.volumeNames)) {
      throw new Error(`failed-target container ${id} lacks exact resource claims`)
    }
    for (const networkID of entry.networkIDs) if (String(networkID).trim()) networkIDs.add(String(networkID).trim())
    for (const volumeName of entry.volumeNames) if (String(volumeName).trim()) volumeNames.add(String(volumeName).trim())
  }
  return { networkIDs: [...networkIDs].sort(), volumeNames: [...volumeNames].sort() }
}

async function containerIDsForResource(kind, identity) {
  const { stdout } = await execFileAsync('docker', ['container', 'ls', '--all', '--no-trunc', '--filter',
    `${kind}=${identity}`, '--format', '{{.ID}}'], { maxBuffer: 4 << 20 })
  return normalizeLines(stdout).sort()
}

async function inspectProjectNetworks() {
  const { stdout } = await execFileAsync('docker', ['network', 'ls', '--no-trunc', '--filter',
    'label=com.docker.compose.project=digitalocean', '--format', '{{.ID}}'], { maxBuffer: 4 << 20 })
  const entries = []
  for (const listedID of normalizeLines(stdout)) {
    const { stdout: raw } = await execFileAsync('docker', ['network', 'inspect', listedID], { maxBuffer: 16 << 20 })
    const inspect = parseJSON(raw, 'Docker project network inspect')[0]
    const labels = inspect?.Labels || {}
    const id = String(inspect?.Id || listedID)
    if (labels['com.docker.compose.project'] !== 'digitalocean') throw new Error('Compose project network label differs from digitalocean')
    entries.push({
      id,
      name: inspect?.Name,
      project: labels['com.docker.compose.project'],
      resourceKey: labels['com.docker.compose.network'],
      createdAt: inspect?.Created,
      driver: inspect?.Driver,
      scope: inspect?.Scope,
      internal: inspect?.Internal === true,
      attachable: inspect?.Attachable === true,
      ingress: inspect?.Ingress === true,
      configOnly: inspect?.ConfigOnly === true,
      labels: stableJSONValue(labels),
      options: stableJSONValue(inspect?.Options || {}),
      ipam: stableJSONValue(inspect?.IPAM || {}),
      containerIDs: await containerIDsForResource('network', id)
    })
  }
  return entries
}

async function inspectProjectVolumes() {
  const { stdout } = await execFileAsync('docker', ['volume', 'ls', '--filter',
    'label=com.docker.compose.project=digitalocean', '--format', '{{.Name}}'], { maxBuffer: 4 << 20 })
  const names = [...new Set([...normalizeLines(stdout), ...expectedProjectVolumeNames])].sort()
  const entries = []
  for (const requestedName of names) {
    const { stdout: raw } = await execFileAsync('docker', ['volume', 'inspect', requestedName], { maxBuffer: 16 << 20 })
    const inspect = parseJSON(raw, 'Docker project volume inspect')[0]
    const labels = inspect?.Labels || {}
    const name = String(inspect?.Name || requestedName)
    entries.push({
      name,
      project: labels['com.docker.compose.project'] || '',
      resourceKey: labels['com.docker.compose.volume'] || '',
      createdAt: inspect?.CreatedAt,
      driver: inspect?.Driver,
      scope: inspect?.Scope,
      mountpoint: inspect?.Mountpoint,
      labels: stableJSONValue(labels),
      options: stableJSONValue(inspect?.Options || {}),
      containerIDs: await containerIDsForResource('volume', name)
    })
  }
  return entries
}

async function inspectProjectResources() {
  const [networks, volumes] = await Promise.all([inspectProjectNetworks(), inspectProjectVolumes()])
  return { networks, volumes }
}

function networkResourceIdentity(entry) {
  return stableJSONValue({
    id: String(entry?.id || ''), name: String(entry?.name || ''), project: String(entry?.project || ''),
    resourceKey: String(entry?.resourceKey || ''), createdAt: String(entry?.createdAt || ''),
    driver: String(entry?.driver || ''), scope: String(entry?.scope || ''), internal: entry?.internal === true,
    attachable: entry?.attachable === true, ingress: entry?.ingress === true, configOnly: entry?.configOnly === true,
    labels: entry?.labels || {}, options: entry?.options || {}, ipam: entry?.ipam || {}
  })
}

function volumeResourceIdentity(entry) {
  return stableJSONValue({
    name: String(entry?.name || ''), project: String(entry?.project || ''), resourceKey: String(entry?.resourceKey || ''),
    createdAt: String(entry?.createdAt || ''), driver: String(entry?.driver || ''), scope: String(entry?.scope || ''),
    mountpoint: String(entry?.mountpoint || ''), labels: entry?.labels || {}, options: entry?.options || {}
  })
}

export function validateProjectResourceBaseline(resources) {
  if (!resources || !Array.isArray(resources.networks) || !Array.isArray(resources.volumes)) {
    throw new Error('Compose project resource inventory is invalid')
  }
  const networkNames = resources.networks.map(entry => String(entry?.name || '')).sort()
  if (JSON.stringify(networkNames) !== JSON.stringify(expectedProjectNetworkNames)) {
    throw new Error('Compose project network inventory is not exact')
  }
  const networkIDs = new Set()
  for (const [key, expected] of Object.entries(expectedProjectNetworks)) {
    const entry = resources.networks.find(candidate => candidate?.name === expected.name)
    const id = String(entry?.id || '')
    if (!id || networkIDs.has(id) || entry?.project !== 'digitalocean' || entry?.resourceKey !== key ||
        Number.isNaN(Date.parse(String(entry?.createdAt || ''))) || entry?.driver !== 'bridge' || entry?.scope !== 'local' ||
        Boolean(entry?.internal) !== expected.internal || entry?.attachable === true || entry?.ingress === true || entry?.configOnly === true) {
      throw new Error(`Compose project network ${key} identity is invalid`)
    }
    networkIDs.add(id)
  }
  const volumeNames = resources.volumes.map(entry => String(entry?.name || '')).sort()
  if (JSON.stringify(volumeNames) !== JSON.stringify(expectedProjectVolumeNames)) {
    throw new Error('Compose project volume inventory is not exact')
  }
  for (const [key, expected] of Object.entries(expectedProjectVolumes)) {
    const entry = resources.volumes.find(candidate => candidate?.name === expected.name)
    if (!entry || Number.isNaN(Date.parse(String(entry.createdAt || ''))) || !String(entry.driver || '') ||
        !String(entry.scope || '') || !isAbsolute(String(entry.mountpoint || '')) ||
        (!expected.external && (entry.project !== 'digitalocean' || entry.resourceKey !== key)) ||
        (expected.external && entry.project && (entry.project !== 'digitalocean' || entry.resourceKey !== key))) {
      throw new Error(`Compose project volume ${key} identity is invalid`)
    }
  }
  return resources
}

export function projectResourceSnapshotSha256(resources) {
  validateProjectResourceBaseline(resources)
  return sha256(canonical(stableJSONValue({
    networks: resources.networks.map(networkResourceIdentity).sort((left, right) => left.name.localeCompare(right.name)),
    volumes: resources.volumes.map(volumeResourceIdentity).sort((left, right) => left.name.localeCompare(right.name))
  })))
}

function exactComposeResourceLabels(value, logicalLabel, logicalName) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const allowed = new Set(['com.docker.compose.project', logicalLabel, 'com.docker.compose.version'])
  return Object.keys(value).every(key => allowed.has(key)) &&
    value['com.docker.compose.project'] === 'digitalocean' && value[logicalLabel] === logicalName &&
    String(value['com.docker.compose.version'] || '').trim() !== ''
}

export function planRollbackProjectResourceCleanup(baseline, current, claims, operationStartedAt) {
  validateProjectResourceBaseline(baseline)
  if (!current || !Array.isArray(current.networks) || !Array.isArray(current.volumes) ||
      !claims || !Array.isArray(claims.networkIDs) || !Array.isArray(claims.volumeNames)) {
    throw new Error('failed-target Compose project resource cleanup input is invalid')
  }
  const startedAt = Date.parse(String(operationStartedAt || ''))
  if (Number.isNaN(startedAt)) throw new Error('failed-target resource cleanup provenance is invalid')
  const claimedNetworks = new Set(claims.networkIDs.map(value => String(value).trim()))
  const claimedVolumes = new Set(claims.volumeNames.map(value => String(value).trim()))
  if (claimedNetworks.has('') || claimedVolumes.has('') || claimedNetworks.size !== claims.networkIDs.length ||
      claimedVolumes.size !== claims.volumeNames.length) {
    throw new Error('failed-target Compose project resource claims repeat or omit an identity')
  }
  const baselineNetworks = new Map(baseline.networks.map(entry => [String(entry.id), networkResourceIdentity(entry)]))
  const inspectedNetworkIDs = new Set(current.networks.map(entry => String(entry?.id || '')))
  for (const id of claimedNetworks) if (!baselineNetworks.has(id) && !inspectedNetworkIDs.has(id)) {
    throw new Error(`claimed project network ${id} is absent from the exact project inventory; operator inspection is required`)
  }
  const currentNetworkIDs = new Set()
  const currentNetworkNames = new Set()
  const networkIDs = []
  for (const entry of current.networks) {
    const id = String(entry?.id || '')
    const name = String(entry?.name || '')
    if (!id || !name || currentNetworkIDs.has(id) || currentNetworkNames.has(name)) {
      throw new Error('failed-target Compose project network inventory repeats or omits an identity')
    }
    currentNetworkIDs.add(id)
    currentNetworkNames.add(name)
    if (baselineNetworks.has(id)) {
      if (canonical(networkResourceIdentity(entry)).compare(canonical(baselineNetworks.get(id))) !== 0) {
        throw new Error(`baseline Compose project network ${id} identity changed during the release transaction`)
      }
      continue
    }
    const key = String(entry?.resourceKey || '')
    const createdAt = Date.parse(String(entry?.createdAt || ''))
    if (!claimedNetworks.has(id) || !/^[a-z0-9][a-z0-9_-]*$/.test(key) || entry?.project !== 'digitalocean' ||
        name !== `digitalocean_${key}` || !exactComposeResourceLabels(entry?.labels, 'com.docker.compose.network', key) ||
        Number.isNaN(createdAt) || createdAt <= startedAt || entry?.driver !== 'bridge' || entry?.scope !== 'local' ||
        entry?.attachable === true || entry?.ingress === true || entry?.configOnly === true ||
        !entry?.options || typeof entry.options !== 'object' || Array.isArray(entry.options) || Object.keys(entry.options).length !== 0 ||
        !Array.isArray(entry?.containerIDs) || entry.containerIDs.length !== 0 ||
        expectedProjectNetworkNames.includes(entry.name)) {
      throw new Error(`project network ${id} is not proven to have been created and used only by the failed target transaction`)
    }
    networkIDs.push(id)
  }
  for (const id of baselineNetworks.keys()) if (!currentNetworkIDs.has(id)) {
    throw new Error(`baseline Compose project network ${id} is missing after the failed target transaction`)
  }

  const baselineVolumes = new Map(baseline.volumes.map(entry => [String(entry.name), volumeResourceIdentity(entry)]))
  const inspectedVolumeNames = new Set(current.volumes.map(entry => String(entry?.name || '')))
  for (const name of claimedVolumes) if (!baselineVolumes.has(name) && !inspectedVolumeNames.has(name)) {
    throw new Error(`claimed project volume ${name} is absent from the exact project inventory and not proven disposable; operator inspection is required`)
  }
  const currentVolumeNames = new Set()
  for (const entry of current.volumes) {
    const name = String(entry?.name || '')
    if (!name || currentVolumeNames.has(name)) throw new Error('failed-target Compose project volume inventory repeats or omits a name')
    currentVolumeNames.add(name)
    if (baselineVolumes.has(name)) {
      if (canonical(volumeResourceIdentity(entry)).compare(canonical(baselineVolumes.get(name))) !== 0) {
        throw new Error(`baseline Compose project volume ${name} identity changed during the release transaction`)
      }
      continue
    }
    // An attachment to a proven target container establishes correlation, not
    // disposability. The approved topology cannot create an extra volume, so
    // any new named volume is ambiguous and deliberately retains the lock for
    // operator inspection instead of risking data deletion.
    throw new Error(`project volume ${name} is unexpected and not proven disposable; operator inspection is required`)
  }
  for (const name of baselineVolumes.keys()) if (!currentVolumeNames.has(name)) {
    throw new Error(`baseline Compose project volume ${name} is missing after the failed target transaction`)
  }
  return { networkIDs: networkIDs.sort(), volumeNames: [] }
}

async function removeFailedTargetProjectContainers(lock, baselineEntries, failedCandidateCompose) {
  await assertReleaseOperationLock(lock)
  const current = await inspectProjectContainers()
  const removals = planRollbackProjectCleanup(baselineEntries, current, failedCandidateCompose, lock.startedAt)
  const resourceClaims = projectResourceClaimsFromContainers(current, removals)
  if (removals.length > 0) {
    await execFileAsync('docker', ['container', 'rm', '--force', ...removals], { maxBuffer: 16 << 20 })
  }
  await assertReleaseOperationLock(lock)
  const remaining = await inspectProjectContainers()
  const unexpected = planRollbackProjectCleanup(baselineEntries, remaining, failedCandidateCompose, lock.startedAt)
  if (unexpected.length > 0) throw new Error('failed-target Compose project containers remain after bounded cleanup')
  return { containerIDs: removals, ...resourceClaims }
}

async function removeFailedTargetProjectResources(lock, baselineResources, claims) {
  await assertReleaseOperationLock(lock)
  const current = await inspectProjectResources()
  const removals = planRollbackProjectResourceCleanup(baselineResources, current, claims, lock.startedAt)
  if (removals.networkIDs.length > 0) {
    await execFileAsync('docker', ['network', 'rm', ...removals.networkIDs], { maxBuffer: 16 << 20 })
  }
  await assertReleaseOperationLock(lock)
  const remaining = await inspectProjectResources()
  if (projectResourceSnapshotSha256(remaining) !== projectResourceSnapshotSha256(baselineResources)) {
    throw new Error('Compose project resources differ from the exact pre-transaction baseline after bounded cleanup')
  }
  return removals
}

export function verifyRenderRunnerHeartbeat(rawHeartbeat, now = Date.now()) {
  const heartbeat = typeof rawHeartbeat === 'string'
    ? parseJSON(rawHeartbeat, 'render-runner canary heartbeat')
    : rawHeartbeat
  if (!heartbeat || typeof heartbeat !== 'object' || Array.isArray(heartbeat)) {
    throw new Error('render-runner canary heartbeat is not an object')
  }
  if (heartbeat.ok !== true || heartbeat.chromiumOK !== true || heartbeat.pdftoppmOK !== true || heartbeat.canaryOK !== true) {
    throw new Error('render-runner exact print/raster canary is not healthy')
  }
  if (heartbeat.canaryPageCount !== 1 || !Number.isInteger(heartbeat.canaryPDFBytes) || heartbeat.canaryPDFBytes < 5) {
    throw new Error('render-runner exact print/raster canary output evidence is invalid')
  }
  if (String(heartbeat.canaryErrorCode || '') !== '') {
    throw new Error('render-runner exact print/raster canary carries a failure code')
  }
  const heartbeatAt = Date.parse(String(heartbeat.time || ''))
  const checkedAt = Date.parse(String(heartbeat.canaryCheckedAt || ''))
  const maxAgeMs = 120_000
  const futureSkewMs = 5_000
  for (const [label, timestamp] of [['heartbeat', heartbeatAt], ['canary', checkedAt]]) {
    if (!Number.isFinite(timestamp) || timestamp > now + futureSkewMs || now - timestamp > maxAgeMs) {
      throw new Error(`render-runner ${label} evidence is missing, stale, or future-dated`)
    }
  }
  return heartbeat
}

async function verifyRunning(options, printResult = true, {
  verifyTool = true,
  verifyLedger = true,
  expectedRenderedComposeSha256 = '',
  baseEnvPatch = null,
  baseEnvPatchState = ''
} = {}) {
  for (const [name, value] of [['--release-dir', options.releaseDir], ['--base-env', options.baseEnv],
    ['--health-url', options.healthUrl], ['--ready-url', options.readyUrl]]) required(name, value)
  const { receipt, paths } = await loadReleaseBundle(options, { verifyTool })
  verifyReleaseEnvironmentFile(await readFile(paths.runtimeEnv, 'utf8'), receipt)
  const composePreflight = await preflightComposeBundle(options, { receipt, paths }, expectedRenderedComposeSha256)
  validateProjectResourceBaseline(await inspectProjectResources())
  const containers = await inspectProjectServiceInventory(true)
  const expectedImages = {
    meetingassist: receipt.images.meetingassist,
    'render-runner': receipt.images.renderRunner,
    'render-queue-init': receipt.images.renderRunner,
    'canonical-postgres': receipt.sidecars.canonicalPostgres,
    coturn: receipt.sidecars.coturn,
    caddy: receipt.sidecars.caddy
  }
  const inspected = {}
  for (const [service, container] of Object.entries(containers)) {
    const { stdout: raw } = await execFileAsync('docker', ['container', 'inspect', container], { maxBuffer: 16 << 20 })
    inspected[service] = parseJSON(raw, `Docker ${service} container inspect`)[0]
    if (String(inspected[service]?.Image || '').toLowerCase() !== expectedImages[service].imageId) {
      throw new Error(`running ${service} image ID differs from release receipt`)
    }
    if (service === 'render-queue-init') {
      if (inspected[service]?.State?.Status !== 'exited' || inspected[service]?.State?.ExitCode !== 0) {
        throw new Error('render-queue-init did not complete successfully')
      }
    } else if (inspected[service]?.State?.Status !== 'running') {
      throw new Error(`candidate Compose service ${service} is not running`)
    }
  }
  for (const [role, image] of Object.entries(receipt.images)) {
    const { stdout: raw } = await execFileAsync('docker', ['image', 'inspect', image.imageId], { maxBuffer: 16 << 20 })
    verifyLabels(parseJSON(raw, `Docker ${role} image inspect`)[0]?.Config?.Labels || {}, receipt.source, receipt.buildInputManifestSha256)
  }
  for (const image of Object.values(receipt.sidecars)) {
    const resolved = await inspectPinnedImage(image.imageReference, image.platform)
    if (resolved.imageId !== image.imageId) throw new Error(`pinned sidecar ${image.imageReference} no longer resolves to its receipted image ID`)
  }
  if (inspected['render-runner']?.State?.Health?.Status !== 'healthy') {
    throw new Error('render-runner Docker heartbeat health is not healthy')
  }
  const { stdout: rendererStatusRaw } = await execFileAsync('docker', [
    'exec', containers['render-runner'], 'cat', '/proc/1/status'
  ], { maxBuffer: 1 << 20 })
  const loadedRendererSeccompProfile = parseJSON(await readFile(join(paths.candidateRoot, rendererSeccompSourcePath)),
    'renderer seccomp release input')
  validateRendererRuntimeConfinement(inspected['render-runner'], rendererStatusRaw, loadedRendererSeccompProfile)
  const { stdout: renderHeartbeatRaw } = await execFileAsync('docker', [
    'exec', containers['render-runner'], 'cat', '/app/render-queue/heartbeat.json'
  ], { maxBuffer: 1 << 20 })
  verifyRenderRunnerHeartbeat(renderHeartbeatRaw)
  await verifyRunningOwnedImage(containers.meetingassist, receipt.images.meetingassist)
  await verifyRunningOwnedImage(containers['render-runner'], receipt.images.renderRunner)
  const appEnvironment = environmentFromInspect(inspected.meetingassist)
  verifyRuntimeEnvironment(appEnvironment, receipt)
  verifyBaseEnvPatchRuntimeEnvironment(appEnvironment, baseEnvPatch, baseEnvPatchState)
  if (appEnvironment.BONFIRE_RELEASE_BUNDLE_SHA256 !== receipt.bundleSha256) throw new Error('running app bundle identity differs from release receipt')
  const caddyCopy = join(await mkdtemp(`${tmpdir()}/bonfire-caddy-verify-`), 'Caddyfile')
  try {
    await execFileAsync('docker', ['cp', `${containers.caddy}:/etc/caddy/Caddyfile`, caddyCopy])
    if (sha256(await readFile(caddyCopy)) !== receipt.source.configFiles['deploy/digitalocean/Caddyfile']) {
      throw new Error('running Caddy configuration differs from candidate bundle')
    }
  } finally {
    await rm(dirname(caddyCopy), { recursive: true, force: true })
  }
  const [healthResponse, readyResponse] = await Promise.all([
    fetch(options.healthUrl, { headers: { accept: 'application/json' }, signal: AbortSignal.timeout(20_000) }),
    fetch(options.readyUrl, { headers: { accept: 'application/json' }, signal: AbortSignal.timeout(20_000) })
  ])
  if (!healthResponse.ok) throw new Error(`health endpoint returned ${healthResponse.status}`)
  if (!readyResponse.ok) throw new Error(`readiness endpoint returned ${readyResponse.status}`)
  verifyProbeRelease(await healthResponse.json(), receipt, 'health')
  verifyProbeRelease(await readyResponse.json(), receipt, 'readiness')
  if (verifyLedger) {
    const ledger = await readActiveReleaseLedger(options.releaseDir)
    if (!ledger) throw new Error('active release ledger is missing')
    assertLedgerEntryMatches(ledger.active, options.releaseDir, receipt, 'active')
  }
  const result = { verified: true, attestation: 'verified-local-unsigned', releaseCommit: receipt.source.releaseCommit,
    bundleSha256: receipt.bundleSha256, images: Object.fromEntries(Object.entries(receipt.images).map(([name, image]) => [name, image.imageId])),
    healthUrl: options.healthUrl, readyUrl: options.readyUrl }
  // This transaction-local digest binds the fully rendered configuration,
  // including secret-derived values. Keep it non-enumerable so logs do not
  // become an offline oracle for low-entropy operator secrets.
  Object.defineProperty(result, 'renderedComposeSha256', { value: composePreflight.sha256, enumerable: false })
  Object.defineProperty(result, 'qualificationState', {
    value: privateRealtimeVoiceQualificationRuntimeState(appEnvironment), enumerable: false
  })
  if (printResult) process.stdout.write(`${JSON.stringify(result)}\n`)
  return result
}

async function verifyRunningOwnedImage(container, image) {
  const checks = [
    execFileAsync('docker', ['exec', container, 'sha256sum', '/proc/1/exe', '/app/meetingassist']),
    execFileAsync('docker', ['exec', container, 'cat', '/app/release-build-packages.txt']),
    execFileAsync('docker', ['exec', container, 'cat', '/app/release-runtime-packages.txt'])
  ]
  if (image.chromeHeadlessShellBinarySha256) {
    checks.push(execFileAsync('docker', ['exec', container, 'sha256sum', '/opt/chrome-headless-shell/chrome-headless-shell']))
  }
  const [{ stdout: binaryRaw }, { stdout: buildPackagesRaw }, { stdout: runtimePackagesRaw }, chromeResult] = await Promise.all(checks)
  const binaryDigests = normalizeLines(binaryRaw).map(line => line.trim().split(/\s+/, 1)[0])
  if (binaryDigests.length !== 2 || binaryDigests.some(digest => digest !== image.binarySha256)) {
    throw new Error('running executable or image binary differs from release receipt')
  }
  if (JSON.stringify(normalizeLines(buildPackagesRaw)) !== JSON.stringify(image.resolvedPackages.build) ||
      JSON.stringify(normalizeLines(runtimePackagesRaw)) !== JSON.stringify(image.resolvedPackages.runtime)) {
    throw new Error('running resolved package inventory differs from build manifest')
  }
  if (image.chromeHeadlessShellBinarySha256) {
    const chromeDigest = normalizeLines(chromeResult.stdout)[0]?.trim().split(/\s+/, 1)[0]
    if (chromeDigest !== image.chromeHeadlessShellBinarySha256) throw new Error('running render toolchain binary differs from release receipt')
  }
}

export function verifyLabels(labels, source, buildInputManifestSha256) {
  const expected = {
    'org.opencontainers.image.revision': source.releaseCommit,
    'xyz.thebonfire.git-tree-digest': source.gitTreeDigest,
    'xyz.thebonfire.config-digest': source.buildConfigSha256,
    'xyz.thebonfire.transitive-inputs-digest': source.transitiveInputsSha256,
    'xyz.thebonfire.source-archive-digest': source.sourceArchiveSha256,
    'xyz.thebonfire.build-input-manifest-digest': buildInputManifestSha256,
    'xyz.thebonfire.attestation': 'unsigned-external-verification-required'
  }
  for (const [name, value] of Object.entries(expected)) if (normalizeLabel(labels[name]) !== value) throw new Error(`Docker image label ${name} differs from release receipt`)
}

export function verifyRuntimeEnvironment(environment, receipt) {
  const expected = environmentValues(receipt)
  for (const [name, value] of Object.entries(expected)) if (String(environment[name] || '') !== value) throw new Error(`running environment ${name} differs from release receipt`)
}

export function verifyBaseEnvPatchRuntimeEnvironment(environment, plan, state) {
  if (!plan) return
  validateBaseEnvPatchPlan(plan)
  if (!['target', 'prior'].includes(state)) throw new Error('base env runtime verification state is invalid')
  const expected = state === 'target' ? privateRealtimeVoiceQualificationValue : plan.priorQualificationState
  if (privateRealtimeVoiceQualificationRuntimeState(environment) !== expected) {
    throw new Error(`running environment ${privateRealtimeVoiceQualificationKey} differs from the ${state} base env`)
  }
}

export function privateRealtimeVoiceQualificationRuntimeState(environment) {
  if (!Object.hasOwn(environment || {}, privateRealtimeVoiceQualificationKey)) return 'absent'
  const value = String(environment[privateRealtimeVoiceQualificationKey])
  if (!['false', 'true'].includes(value)) throw new Error('running qualification environment is malformed')
  return value
}

export function assertPrivateRealtimeVoiceQualificationHostRuntimeMatch(hostState, runtimeState) {
  if (!['absent', 'false', 'true'].includes(hostState) || !['absent', 'false', 'true'].includes(runtimeState) ||
      hostState !== runtimeState) {
    throw new Error('base env qualification state differs from the currently serving application container')
  }
  return hostState
}

export function verifyReleaseEnvironmentFile(body, receipt) {
  const environment = {}
  for (const line of String(body).split('\n').filter(Boolean)) {
    const index = line.indexOf('=')
    if (index <= 0) throw new Error('release environment file is malformed')
    const name = line.slice(0, index)
    if (Object.hasOwn(environment, name)) throw new Error(`release environment repeats ${name}`)
    environment[name] = line.slice(index + 1)
  }
  const expected = releaseEnvironmentValues(receipt)
  if (Object.keys(environment).length !== Object.keys(expected).length) throw new Error('release environment contains unexpected fields')
  for (const [name, value] of Object.entries(expected)) {
    if (environment[name] !== value) throw new Error(`release environment ${name} differs from release receipt`)
  }
}

export function verifyProbeRelease(payload, receipt, probe) {
  const release = payload?.release
  if (payload?.ok !== true || payload?.version !== receipt.source.releaseCommit || release?.schema !== schema ||
      release?.processQualified !== true || release?.qualified !== false || release?.externallyAttested !== false ||
      release?.externalAttestationRequired !== true || release?.attestationReason !== 'unsigned_external_verification_required') {
    throw new Error(`${probe} release identity is not honestly process-qualified`)
  }
  const expected = {
    releaseCommit: receipt.source.releaseCommit, gitTreeDigest: receipt.source.gitTreeDigest,
    sourceArchiveSha256: receipt.source.sourceArchiveSha256, transitiveInputsSha256: receipt.source.transitiveInputsSha256,
    buildConfigSha256: receipt.source.buildConfigSha256, buildInputManifestSha256: receipt.buildInputManifestSha256,
    claimedBuildManifestSha256: receipt.buildManifestSha256, binarySha256: receipt.images.meetingassist.binarySha256,
    claimedImageDigest: receipt.images.meetingassist.imageDigest, environmentMarker: receipt.environmentMarker
  }
  for (const [name, value] of Object.entries(expected)) if (release?.[name] !== value) throw new Error(`${probe} release field ${name} differs from release receipt`)
}

export function environmentValues(receipt) {
  const values = {
    BONFIRE_RELEASE_IDENTITY_REQUIRED: 'true',
    BONFIRE_RELEASE_COMMIT: receipt.source.releaseCommit,
    BONFIRE_GIT_TREE_DIGEST: receipt.source.gitTreeDigest,
    BONFIRE_SOURCE_ARCHIVE_SHA256: receipt.source.sourceArchiveSha256,
    BONFIRE_BUILD_TRANSITIVE_INPUTS_SHA256: receipt.source.transitiveInputsSha256,
    BONFIRE_BUILD_CONFIG_SHA256: receipt.source.buildConfigSha256,
    BONFIRE_BUILD_INPUT_MANIFEST_SHA256: receipt.buildInputManifestSha256,
    BONFIRE_BUILD_MANIFEST_SHA256: receipt.buildManifestSha256,
    BONFIRE_BINARY_SHA256: receipt.images.meetingassist.binarySha256,
    BONFIRE_IMAGE_DIGEST: receipt.images.meetingassist.imageDigest,
    BONFIRE_RELEASE_ENVIRONMENT_MARKER: receipt.environmentMarker,
    BONFIRE_BUILD_VERSION: receipt.source.releaseCommit
  }
  if (receipt.schema === receiptSchemaW4) {
    const policy = validateStrideE10W4DeploymentPolicy(receipt.strideE10W4)
    values.STRIDE_E10_W4_MODE = policy.releaseMode
    values.STRIDE_E10_W4_SNAPSHOT_PATH = policy.snapshotPath
    values.STRIDE_E10_W4_ACTIVATION_BACKUP_DIR = policy.activationBackupDir
    values.STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH = policy.activationReceiptPath
  }
  return values
}

export function releaseEnvironmentValues(receipt) {
  const values = {
    BONFIRE_MEETINGASSIST_IMAGE: receipt.images.meetingassist.imageId,
    BONFIRE_RENDER_IMAGE: receipt.images.renderRunner.imageId,
    BONFIRE_POSTGRES_IMAGE: receipt.sidecars.canonicalPostgres.imageReference,
    BONFIRE_COTURN_IMAGE: receipt.sidecars.coturn.imageReference,
    BONFIRE_CADDY_IMAGE: receipt.sidecars.caddy.imageReference,
    BONFIRE_RELEASE_BUNDLE_SHA256: receipt.bundleSha256,
    ...environmentValues(receipt)
  }
  if (receipt.schema === receiptSchemaW4) {
    values.STRIDE_E10_W4_RELEASE_MODE = receipt.strideE10W4.releaseMode
    delete values.STRIDE_E10_W4_MODE
  }
  return values
}

function runtimeEnvironment(receipt) {
  return `${Object.entries(releaseEnvironmentValues(receipt)).map(([name, value]) => `${name}=${value}`).join('\n')}\n`
}

export function releasePaths(releaseDir) {
  const root = resolve(releaseDir)
  const candidateRoot = join(root, 'sealed-candidate')
  return {
    sourceReceipt: join(root, 'source-receipt.json'),
    buildManifest: join(root, 'build-manifest.json'),
    releaseReceipt: join(root, 'release-receipt.json'),
    runtimeEnv: join(root, 'release.env'),
    candidateBundleManifest: join(root, 'candidate-bundle.json'),
    releaseTool: join(candidateRoot, 'scripts/bonfire-release.mjs'),
    candidateRoot,
    candidateCompose: join(candidateRoot, 'deploy/digitalocean/docker-compose.yml'),
    candidateCaddyfile: join(candidateRoot, 'deploy/digitalocean/Caddyfile')
  }
}

function releaseLedgerPath(releaseDir) {
  return join(dirname(resolve(releaseDir)), 'active-release.json')
}

function releaseOperationLockPath(releaseDir) {
  return join(dirname(resolve(releaseDir)), '.bonfire-release-operation.lock')
}

const targetBaseEnvPatchOptionNames = [
  'targetBaseEnvExpectedSha256', 'targetBaseEnvPatchKey', 'targetBaseEnvPatchValue', 'targetBaseEnvBackupDir'
]

export function requestedTargetBaseEnvPatch(options, action) {
  const supplied = targetBaseEnvPatchOptionNames.filter(name => String(options?.[name] || '').trim())
  if (!supplied.length) return null
  if (action !== 'activated') throw new Error('target base-env patch arguments are permitted only for activate')
  if (supplied.length !== targetBaseEnvPatchOptionNames.length) {
    throw new Error('target base-env patch requires every explicit target-only argument')
  }
  const rawExpectedBeforeSha256 = String(options.targetBaseEnvExpectedSha256)
  const expectedBeforeSha256 = rawExpectedBeforeSha256.trim()
  if (expectedBeforeSha256 !== rawExpectedBeforeSha256 || !shaPattern.test(expectedBeforeSha256)) {
    throw new Error('--target-base-env-expected-sha256 must be exactly 64 lowercase hexadecimal characters')
  }
  if (options.targetBaseEnvPatchKey !== privateRealtimeVoiceQualificationKey ||
      options.targetBaseEnvPatchValue !== privateRealtimeVoiceQualificationValue) {
    throw new Error('target base-env patch is not the approved private Realtime qualification transition')
  }
  if (!isAbsolute(options.targetBaseEnvBackupDir)) throw new Error('--target-base-env-backup-dir must be absolute')
  const backupDir = resolve(options.targetBaseEnvBackupDir)
  if (options.targetBaseEnvBackupDir !== baseEnvPatchBackupRoot || backupDir !== baseEnvPatchBackupRoot) {
    throw new Error('--target-base-env-backup-dir must be exactly /opt/meetingassist-backups')
  }
  return {
    expectedBeforeSha256,
    patchKey: privateRealtimeVoiceQualificationKey,
    patchValue: privateRealtimeVoiceQualificationValue,
    backupDir
  }
}

export function requestedQualificationRollbackReceipt(options, action) {
  const raw = String(options?.qualificationRollbackReceipt || '')
  if (!raw) return null
  if (action !== 'rolledBack') throw new Error('--qualification-rollback-receipt is permitted only for rollback')
  if (!isAbsolute(raw) || resolve(raw) !== raw || dirname(raw) !== baseEnvPatchBackupRoot) {
    throw new Error('--qualification-rollback-receipt must be one exact receipt under /opt/meetingassist-backups')
  }
  return raw
}

export function privateRealtimeVoiceQualificationEnvState(body) {
  const before = Buffer.isBuffer(body) ? body : Buffer.from(String(body))
  const text = before.toString('utf8')
  if (!Buffer.from(text).equals(before)) throw new Error('base env is not valid UTF-8')
  const mentionedLines = text.split(/\n/).filter(line => line.includes(privateRealtimeVoiceQualificationKey))
  if (mentionedLines.length === 0) return { body: before, text, state: 'absent', match: null }
  const pattern = /^PRIVATE_REALTIME_VOICE_QUALIFIED=(false|true)(\r?)$/gm
  const matches = [...text.matchAll(pattern)]
  if (mentionedLines.length !== 1 || matches.length !== 1 || matches[0].index === undefined) {
    throw new Error('base env qualification key must be absent or one canonical false/true assignment')
  }
  return { body: before, text, state: matches[0][1], match: matches[0] }
}

export function privateRealtimeVoiceQualificationEnvPatch(body) {
  const current = privateRealtimeVoiceQualificationEnvState(body)
  const { body: before, text } = current
  if (current.state === 'absent') {
    const separator = before.length > 0 && !text.endsWith('\n') ? Buffer.from('\n') : Buffer.alloc(0)
    const after = Buffer.concat([before, separator, Buffer.from(`${privateRealtimeVoiceQualificationKey}=${privateRealtimeVoiceQualificationValue}\n`)])
    return { before, after, beforeSha256: sha256(before), afterSha256: sha256(after), priorQualificationState: 'absent' }
  }
  if (current.state !== 'false') {
    throw new Error('base env qualification key must be absent or one canonical PRIVATE_REALTIME_VOICE_QUALIFIED=false assignment')
  }
  const match = current.match
  const replacement = `${privateRealtimeVoiceQualificationKey}=${privateRealtimeVoiceQualificationValue}${match[2]}`
  const afterText = `${text.slice(0, match.index)}${replacement}${text.slice(match.index + match[0].length)}`
  const after = Buffer.from(afterText)
  if (after.equals(before)) throw new Error('base env qualification patch produced no change')
  return { before, after, beforeSha256: sha256(before), afterSha256: sha256(after), priorQualificationState: 'false' }
}

export function assertQualificationTransitionBound(action, currentState, baseEnvPatchMode) {
  if (!['activated', 'rolledBack'].includes(action) || !['absent', 'false', 'true'].includes(currentState) ||
      !['activate', 'rollback', null].includes(baseEnvPatchMode)) {
    throw new Error('qualification transition proof is invalid')
  }
  if (action === 'activated' && currentState === 'true') {
    // The active-release ledger does not yet carry qualification lineage across
    // arbitrary successors. Refuse to create a generation whose only safe
    // rollback receipt is bound to an older active generation.
    throw new Error('qualified current release cannot perform an ordinary activation without durable qualification lineage')
  }
  if (action === 'rolledBack' && currentState === 'true' && baseEnvPatchMode !== 'rollback') {
    throw new Error('qualified explicit rollback requires --qualification-rollback-receipt')
  }
  return { action, currentState, baseEnvPatchMode }
}

export function validatePrivateReleasePathInfo(info, kind, ownerUid = 0) {
  if (!info || !Number.isSafeInteger(ownerUid) || ownerUid < 0) throw new Error(`${kind} ownership proof is invalid`)
  if (kind === 'base env' || kind === 'base env backup' || kind === 'base env patch receipt') {
    if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o600 || info.uid !== ownerUid) {
      throw new Error(`${kind} must be an owner-private regular file`)
    }
  } else if (kind === 'base env parent') {
    if (!info.isDirectory() || info.isSymbolicLink() || (info.mode & 0o022) !== 0 || info.uid !== ownerUid) {
      throw new Error('base env parent must be an owner-controlled non-writable directory')
    }
  } else if (kind === 'base env backup directory') {
    if (!info.isDirectory() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o700 || info.uid !== ownerUid) {
      throw new Error('base env backup directory must be owner-private')
    }
  } else throw new Error('private release path kind is invalid')
  return info
}

export function validateBaseEnvPatchPlan(value, backupRoot = baseEnvPatchBackupRoot) {
  const expected = ['afterSha256', 'backupDir', 'backupPath', 'baseEnvPath', 'beforeSha256', 'patchKey', 'receiptPath',
    'priorQualificationState', 'rollbackReleaseCommit', 'schema', 'targetLedgerGeneration', 'targetReleaseCommit', 'transactionToken'].sort()
  const planStem = value && `base-env-${value.targetReleaseCommit}-${value.transactionToken}`
  if (!isAbsolute(String(backupRoot || '')) || resolve(backupRoot) !== backupRoot || !value ||
      Object.keys(value).sort().join('\n') !== expected.join('\n') || value.schema !== baseEnvPatchSchema ||
      !shaPattern.test(String(value.beforeSha256 || '')) || !shaPattern.test(String(value.afterSha256 || '')) ||
      value.beforeSha256 === value.afterSha256 || value.patchKey !== privateRealtimeVoiceQualificationKey ||
      !['absent', 'false'].includes(value.priorQualificationState) ||
      !commitPattern.test(String(value.targetReleaseCommit || '')) || !commitPattern.test(String(value.rollbackReleaseCommit || '')) ||
      !Number.isSafeInteger(value.targetLedgerGeneration) || value.targetLedgerGeneration < 1 ||
      !/^[A-Za-z0-9-]{1,100}$/.test(String(value.transactionToken || '')) ||
      ![value.baseEnvPath, value.backupDir, value.backupPath, value.receiptPath].every(path => isAbsolute(String(path || ''))) ||
      value.backupDir !== backupRoot || value.backupPath !== join(value.backupDir, `${planStem}.before.env`) ||
      value.receiptPath !== join(value.backupDir, `${planStem}.receipt.json`) ||
      value.backupPath === value.receiptPath) throw new Error('base env patch plan is invalid')
  return value
}

export function validateBaseEnvPatchReceipt(value, plan, backupRoot = baseEnvPatchBackupRoot) {
  validateBaseEnvPatchPlan(plan, backupRoot)
  const expected = ['afterSha256', 'backupPath', 'baseEnvPath', 'beforeSha256', 'committedAt', 'patchKey', 'priorQualificationState', 'priorRestoredAt',
    'rollbackReleaseCommit', 'schema', 'state', 'targetLedgerGeneration', 'targetObservedAt', 'targetReleaseCommit', 'transactionToken'].sort()
  if (!value || Object.keys(value).sort().join('\n') !== expected.join('\n') || value.schema !== baseEnvPatchReceiptSchema ||
      value.transactionToken !== plan.transactionToken || value.targetReleaseCommit !== plan.targetReleaseCommit ||
      value.rollbackReleaseCommit !== plan.rollbackReleaseCommit ||
      value.targetLedgerGeneration !== plan.targetLedgerGeneration || value.baseEnvPath !== plan.baseEnvPath ||
      value.backupPath !== plan.backupPath || value.patchKey !== plan.patchKey ||
      value.priorQualificationState !== plan.priorQualificationState ||
      value.beforeSha256 !== plan.beforeSha256 || value.afterSha256 !== plan.afterSha256 ||
      !['target_installed', 'target_committed', 'prior_installed', 'prior_committed'].includes(value.state) ||
      typeof value.targetObservedAt !== 'string' || Number.isNaN(Date.parse(value.targetObservedAt)) ||
      (value.state === 'target_installed' && (value.committedAt !== null || value.priorRestoredAt !== null)) ||
      (value.state === 'target_committed' && (typeof value.committedAt !== 'string' || Number.isNaN(Date.parse(value.committedAt)) || value.priorRestoredAt !== null)) ||
      (['prior_installed', 'prior_committed'].includes(value.state) && (typeof value.priorRestoredAt !== 'string' || Number.isNaN(Date.parse(value.priorRestoredAt)) ||
        (value.committedAt !== null && (typeof value.committedAt !== 'string' || Number.isNaN(Date.parse(value.committedAt))))))) {
    throw new Error('base env patch receipt is invalid')
  }
  return value
}

export function baseEnvPatchPlanFromReceipt(receiptPath, receipt, backupRoot = baseEnvPatchBackupRoot) {
  const plan = validateBaseEnvPatchPlan({
    schema: baseEnvPatchSchema,
    transactionToken: receipt?.transactionToken,
    targetReleaseCommit: receipt?.targetReleaseCommit,
    rollbackReleaseCommit: receipt?.rollbackReleaseCommit,
    targetLedgerGeneration: receipt?.targetLedgerGeneration,
    baseEnvPath: receipt?.baseEnvPath,
    backupDir: dirname(receiptPath),
    backupPath: receipt?.backupPath,
    receiptPath,
    patchKey: receipt?.patchKey,
    priorQualificationState: receipt?.priorQualificationState,
    beforeSha256: receipt?.beforeSha256,
    afterSha256: receipt?.afterSha256
  }, backupRoot)
  validateBaseEnvPatchReceipt(receipt, plan, backupRoot)
  return plan
}

export function baseEnvPatchDigestState(currentSha256, plan, backupRoot = baseEnvPatchBackupRoot) {
  validateBaseEnvPatchPlan(plan, backupRoot)
  if (currentSha256 === plan.beforeSha256) return 'prior'
  if (currentSha256 === plan.afterSha256) return 'target'
  throw new Error('base env drifted outside the durable patch transaction')
}

export function baseEnvPatchTemporaryPath(plan, backupRoot = baseEnvPatchBackupRoot) {
  validateBaseEnvPatchPlan(plan, backupRoot)
  return join(dirname(plan.baseEnvPath), `.${basename(plan.baseEnvPath)}.bonfire-${plan.transactionToken}.tmp`)
}

async function readPrivateReleaseFile(path, kind, ownerUid) {
  validatePrivateReleasePathInfo(await lstat(path), kind, ownerUid)
  return readFile(path)
}

async function assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot = baseEnvPatchBackupRoot) {
  validateBaseEnvPatchPlan(plan, backupRoot)
  validatePrivateReleasePathInfo(await lstat(dirname(plan.baseEnvPath)), 'base env parent', ownerUid)
  validatePrivateReleasePathInfo(await lstat(plan.baseEnvPath), 'base env', ownerUid)
  validatePrivateReleasePathInfo(await lstat(backupRoot), 'base env backup directory', ownerUid)
  validatePrivateReleasePathInfo(await lstat(plan.backupDir), 'base env backup directory', ownerUid)
}

export async function prepareTargetBaseEnvPatch({
  baseEnv, request, operationLock, targetReleaseCommit, rollbackReleaseCommit, targetLedgerGeneration,
  ownerUid = 0, backupRoot = baseEnvPatchBackupRoot
}) {
  if (!request) return null
  await assertReleaseOperationLock(operationLock)
  const baseEnvPath = resolve(baseEnv)
  const planStem = `base-env-${targetReleaseCommit}-${operationLock.token}`
  const draft = {
    schema: baseEnvPatchSchema,
    transactionToken: operationLock.token,
    targetReleaseCommit,
    rollbackReleaseCommit,
    targetLedgerGeneration,
    baseEnvPath,
    backupDir: request.backupDir,
    backupPath: join(request.backupDir, `${planStem}.before.env`),
    receiptPath: join(request.backupDir, `${planStem}.receipt.json`),
    patchKey: request.patchKey,
    priorQualificationState: 'absent',
    beforeSha256: request.expectedBeforeSha256,
    afterSha256: request.expectedBeforeSha256
  }
  validatePrivateReleasePathInfo(await lstat(dirname(baseEnvPath)), 'base env parent', ownerUid)
  validatePrivateReleasePathInfo(await lstat(baseEnvPath), 'base env', ownerUid)
  if (request.backupDir !== backupRoot) throw new Error('base env patch request backup root differs from policy')
  validatePrivateReleasePathInfo(await lstat(backupRoot), 'base env backup directory', ownerUid)
  validatePrivateReleasePathInfo(await lstat(request.backupDir), 'base env backup directory', ownerUid)
  const patch = privateRealtimeVoiceQualificationEnvPatch(await readFile(baseEnvPath))
  if (patch.beforeSha256 !== request.expectedBeforeSha256) {
    throw new Error('base env digest differs from the explicitly approved prior digest')
  }
  return validateBaseEnvPatchPlan({
    ...draft, afterSha256: patch.afterSha256, priorQualificationState: patch.priorQualificationState
  }, backupRoot)
}

async function readOptionalPatchReceipt(plan, ownerUid, backupRoot = baseEnvPatchBackupRoot) {
  try {
    const body = await readPrivateReleaseFile(plan.receiptPath, 'base env patch receipt', ownerUid)
    return validateBaseEnvPatchReceipt(parseJSON(body, 'base env patch receipt'), plan, backupRoot)
  } catch (error) {
    if (error?.code === 'ENOENT') return null
    throw error
  }
}

export async function prepareQualificationRollbackBaseEnvPatch({
  baseEnv, receiptPath, operationLock, targetReleaseCommit, rollbackReleaseCommit, activeLedgerGeneration,
  ownerUid = 0, backupRoot = baseEnvPatchBackupRoot
}) {
  if (!receiptPath) return null
  await assertReleaseOperationLock(operationLock)
  validatePrivateReleasePathInfo(await lstat(backupRoot), 'base env backup directory', ownerUid)
  validatePrivateReleasePathInfo(await lstat(receiptPath), 'base env patch receipt', ownerUid)
  const receipt = parseJSON(await readFile(receiptPath), 'qualification rollback receipt')
  const plan = baseEnvPatchPlanFromReceipt(receiptPath, receipt, backupRoot)
  if (receipt.state !== 'target_committed' || plan.baseEnvPath !== resolve(baseEnv) ||
      plan.targetReleaseCommit !== rollbackReleaseCommit || plan.rollbackReleaseCommit !== targetReleaseCommit ||
      plan.targetLedgerGeneration !== activeLedgerGeneration) {
    throw new Error('qualification rollback receipt does not bind the exact active and target releases')
  }
  await assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot)
  const backup = await readPrivateReleaseFile(plan.backupPath, 'base env backup', ownerUid)
  if (sha256(backup) !== plan.beforeSha256) throw new Error('qualification rollback backup differs from its committed receipt')
  if (sha256(await readFile(plan.baseEnvPath)) !== plan.afterSha256) {
    throw new Error('qualification rollback requires the exact committed target env digest')
  }
  return plan
}

async function ensureBaseEnvBackup(plan, priorBody, ownerUid) {
  try {
    await writeExclusive(plan.backupPath, priorBody, 0o600)
    await syncDirectory(plan.backupDir)
  } catch (error) {
    if (error?.code !== 'EEXIST') throw error
  }
  const backup = await readPrivateReleaseFile(plan.backupPath, 'base env backup', ownerUid)
  if (sha256(backup) !== plan.beforeSha256) throw new Error('base env backup differs from the approved prior digest')
  return backup
}

async function removeReceiptlessBaseEnvBackup(plan, ownerUid, backupRoot = baseEnvPatchBackupRoot) {
  validateBaseEnvPatchPlan(plan, backupRoot)
  try {
    const info = await lstat(plan.backupPath)
    validatePrivateReleasePathInfo(info, 'base env backup', ownerUid)
    if (info.nlink !== 1) throw new Error('receiptless base env backup has an unexpected link count')
    await unlink(plan.backupPath)
    await syncDirectory(plan.backupDir)
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error
  }
}

async function writeBaseEnvPatchReceipt(plan, state, targetObservedAt, ownerUid, backupRoot = baseEnvPatchBackupRoot) {
  const previous = await readOptionalPatchReceipt(plan, ownerUid, backupRoot)
  const now = new Date().toISOString()
  const receipt = validateBaseEnvPatchReceipt({
    schema: baseEnvPatchReceiptSchema,
    transactionToken: plan.transactionToken,
    targetReleaseCommit: plan.targetReleaseCommit,
    rollbackReleaseCommit: plan.rollbackReleaseCommit,
    targetLedgerGeneration: plan.targetLedgerGeneration,
    baseEnvPath: plan.baseEnvPath,
    backupPath: plan.backupPath,
    patchKey: plan.patchKey,
    priorQualificationState: plan.priorQualificationState,
    beforeSha256: plan.beforeSha256,
    afterSha256: plan.afterSha256,
    state,
    targetObservedAt: previous?.targetObservedAt || targetObservedAt || now,
    committedAt: state === 'target_committed' ? now : (state.startsWith('prior_') ? previous?.committedAt || null : null),
    priorRestoredAt: state.startsWith('prior_') ? (previous?.priorRestoredAt || now) : null
  }, plan, backupRoot)
  await writeAtomicReplace(plan.receiptPath, jsonLine(receipt), 0o600)
  validatePrivateReleasePathInfo(await lstat(plan.receiptPath), 'base env patch receipt', ownerUid)
  return receipt
}

async function installTargetBaseEnvPatchWithPolicy(
  operationLock, plan, ownerUid, allowPriorReinstall, backupRoot = baseEnvPatchBackupRoot
) {
  if (!plan) return null
  await assertReleaseOperationLock(operationLock)
  await assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot)
  const current = await readFile(plan.baseEnvPath)
  const currentSha256 = sha256(current)
  const currentState = baseEnvPatchDigestState(currentSha256, plan, backupRoot)
  let targetObservedAt = ''
  if (currentState === 'prior') {
    const patch = privateRealtimeVoiceQualificationEnvPatch(current)
    if (patch.afterSha256 !== plan.afterSha256 || patch.priorQualificationState !== plan.priorQualificationState) {
      throw new Error('base env patch bytes or prior qualification state differ from the durable plan')
    }
    await ensureBaseEnvBackup(plan, current, ownerUid)
    await writeAtomicReplaceBound(plan.baseEnvPath, patch.after, 0o600,
      baseEnvPatchTemporaryPath(plan, backupRoot), ownerUid)
    targetObservedAt = new Date().toISOString()
  } else if (currentState === 'target') {
    await ensureBaseEnvBackup(plan, await readPrivateReleaseFile(plan.backupPath, 'base env backup', ownerUid), ownerUid)
    targetObservedAt = (await readOptionalPatchReceipt(plan, ownerUid, backupRoot))?.targetObservedAt || new Date().toISOString()
  }
  await assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot)
  if (sha256(await readFile(plan.baseEnvPath)) !== plan.afterSha256) throw new Error('target base env patch was not installed')
  const existingReceipt = await readOptionalPatchReceipt(plan, ownerUid, backupRoot)
  if (existingReceipt?.state === 'target_committed') return existingReceipt
  if (existingReceipt && existingReceipt.state.startsWith('prior_') && !allowPriorReinstall) {
    throw new Error('restored base env patch cannot be reinstalled by forward resume')
  }
  return writeBaseEnvPatchReceipt(plan, 'target_installed', targetObservedAt, ownerUid, backupRoot)
}

export async function installTargetBaseEnvPatch(
  operationLock, plan, ownerUid = 0, { backupRoot = baseEnvPatchBackupRoot } = {}
) {
  return installTargetBaseEnvPatchWithPolicy(operationLock, plan, ownerUid, false, backupRoot)
}

export async function reinstallCommittedTargetBaseEnvPatch(
  operationLock, plan, ownerUid = 0, { backupRoot = baseEnvPatchBackupRoot } = {}
) {
  return installTargetBaseEnvPatchWithPolicy(operationLock, plan, ownerUid, true, backupRoot)
}

export async function assertTargetBaseEnvPatchReady(
  operationLock, plan, ownerUid = 0, { backupRoot = baseEnvPatchBackupRoot } = {}
) {
  if (!plan) return
  await assertReleaseOperationLock(operationLock)
  await assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot)
  if (sha256(await readFile(plan.baseEnvPath)) !== plan.beforeSha256) {
    throw new Error('base env differs from the approved prior digest before patch intent')
  }
}

export async function assertTargetBaseEnvPatchInstalled(
  operationLock, plan, ownerUid = 0, { backupRoot = baseEnvPatchBackupRoot } = {}
) {
  if (!plan) return
  await assertReleaseOperationLock(operationLock)
  await assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot)
  if (sha256(await readFile(plan.baseEnvPath)) !== plan.afterSha256) throw new Error('target base env patch is not installed')
  const backup = await readPrivateReleaseFile(plan.backupPath, 'base env backup', ownerUid)
  if (sha256(backup) !== plan.beforeSha256) throw new Error('base env backup differs from the approved prior digest')
  const receipt = await readOptionalPatchReceipt(plan, ownerUid, backupRoot)
  if (!receipt || !['target_installed', 'target_committed'].includes(receipt.state)) throw new Error('target base env patch receipt is not installed')
}

export async function assertPriorBaseEnvRestored(
  operationLock, plan, ownerUid = 0,
  { backupRoot = baseEnvPatchBackupRoot, requireReceipt = false } = {}
) {
  if (!plan) return
  await assertReleaseOperationLock(operationLock)
  await assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot)
  if (sha256(await readFile(plan.baseEnvPath)) !== plan.beforeSha256) throw new Error('prior base env is not installed')
  const receipt = await readOptionalPatchReceipt(plan, ownerUid, backupRoot)
  if (requireReceipt && !receipt) throw new Error('prior base env restore requires its bound receipt')
  if (receipt && !['prior_installed', 'prior_committed'].includes(receipt.state)) throw new Error('prior base env restore receipt is not installed')
  if (receipt) {
    const backup = await readPrivateReleaseFile(plan.backupPath, 'base env backup', ownerUid)
    if (sha256(backup) !== plan.beforeSha256) throw new Error('base env backup differs from the approved prior digest')
  }
}

export async function commitTargetBaseEnvPatch(
  operationLock, plan, ownerUid = 0, { backupRoot = baseEnvPatchBackupRoot } = {}
) {
  if (!plan) return null
  await assertTargetBaseEnvPatchInstalled(operationLock, plan, ownerUid, { backupRoot })
  const receipt = await readOptionalPatchReceipt(plan, ownerUid, backupRoot)
  if (receipt?.state === 'target_committed') return receipt
  return writeBaseEnvPatchReceipt(plan, 'target_committed', receipt?.targetObservedAt || '', ownerUid, backupRoot)
}

export function priorBaseEnvCommitDisposition(receipt) {
  if (receipt === null) return 'skip'
  if (receipt?.state === 'prior_committed') return 'already_committed'
  if (receipt?.state === 'prior_installed') return 'commit'
  throw new Error('prior base env receipt is not commit-ready')
}

export async function commitPriorBaseEnvRestore(
  operationLock, plan, ownerUid = 0,
  { backupRoot = baseEnvPatchBackupRoot, requireReceipt = false } = {}
) {
  if (!plan) return null
  const receipt = await readOptionalPatchReceipt(plan, ownerUid, backupRoot)
  await assertPriorBaseEnvRestored(operationLock, plan, ownerUid, { backupRoot, requireReceipt })
  // A failure after durable patch intent but before backup/install is a true
  // no-op recovery. Keep it receiptless; inventing a prior receipt would make
  // later assertions require a backup that was never created.
  const disposition = priorBaseEnvCommitDisposition(receipt)
  if (disposition === 'skip') {
    await removeReceiptlessBaseEnvBackup(plan, ownerUid, backupRoot)
    return null
  }
  if (disposition === 'already_committed') return receipt
  return writeBaseEnvPatchReceipt(plan, 'prior_committed', receipt?.targetObservedAt || '', ownerUid, backupRoot)
}

export async function restorePriorBaseEnv(
  operationLock, plan, ownerUid = 0,
  { backupRoot = baseEnvPatchBackupRoot, requireReceipt = false } = {}
) {
  if (!plan) return null
  await assertReleaseOperationLock(operationLock)
  await assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot)
  const startingReceipt = await readOptionalPatchReceipt(plan, ownerUid, backupRoot)
  if (requireReceipt && !startingReceipt) throw new Error('prior base env restore requires its bound receipt')
  const current = await readFile(plan.baseEnvPath)
  const currentSha256 = sha256(current)
  const currentState = baseEnvPatchDigestState(currentSha256, plan, backupRoot)
  let targetObservedAt = ''
  if (currentState === 'target') {
    targetObservedAt = startingReceipt?.targetObservedAt || new Date().toISOString()
    const backup = await readPrivateReleaseFile(plan.backupPath, 'base env backup', ownerUid)
    if (sha256(backup) !== plan.beforeSha256) throw new Error('base env backup differs from the approved prior digest')
    await writeAtomicReplaceBound(plan.baseEnvPath, backup, 0o600,
      baseEnvPatchTemporaryPath(plan, backupRoot), ownerUid)
  } else if (currentState === 'prior') {
    if (!startingReceipt) {
      await removeReceiptlessBaseEnvBackup(plan, ownerUid, backupRoot)
      return null
    }
    targetObservedAt = startingReceipt.targetObservedAt
    const backup = await readPrivateReleaseFile(plan.backupPath, 'base env backup', ownerUid)
    if (sha256(backup) !== plan.beforeSha256) throw new Error('base env backup differs from the approved prior digest')
  }
  await assertBaseEnvPatchFilesystem(plan, ownerUid, backupRoot)
  if (sha256(await readFile(plan.baseEnvPath)) !== plan.beforeSha256) throw new Error('prior base env was not restored')
  return writeBaseEnvPatchReceipt(plan, 'prior_installed', targetObservedAt, ownerUid, backupRoot)
}

async function assertReleaseOperationLock(lock) {
  if (!lock || typeof lock.path !== 'string' || typeof lock.token !== 'string' || !lock.token) {
    throw new Error('release operation lock proof is missing')
  }
  const path = resolve(lock.path)
  const info = await lstat(path)
  if (!info.isDirectory() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o700) {
    throw new Error('release operation lock is not a private regular directory')
  }
  const ownerPath = join(path, 'owner.json')
  const entries = (await readdir(path)).sort()
  const stableEntries = entries.filter(name => !/^\.transaction\.json\.[0-9a-f-]{36}\.tmp$/.test(name))
  if (JSON.stringify(stableEntries) !== JSON.stringify(['owner.json']) &&
      JSON.stringify(stableEntries) !== JSON.stringify(['owner.json', 'transaction.json'])) {
    throw new Error('release operation lock contains unexpected state')
  }
  for (const name of entries.filter(name => name.startsWith('.transaction.json.'))) {
    if (!/^\.transaction\.json\.[0-9a-f-]{36}\.tmp$/.test(name)) throw new Error('release operation lock contains unexpected state')
    const temporaryInfo = await lstat(join(path, name))
    if (!temporaryInfo.isFile() || temporaryInfo.isSymbolicLink() || (temporaryInfo.mode & 0o777) !== 0o600) {
      throw new Error('release transaction temporary journal is not private')
    }
  }
  const ownerInfo = await lstat(ownerPath)
  if (!ownerInfo.isFile() || ownerInfo.isSymbolicLink() || (ownerInfo.mode & 0o777) !== 0o600) {
    throw new Error('release operation lock owner is not a private regular file')
  }
  const owner = parseJSON(await readFile(ownerPath), 'release operation lock owner')
  if (owner?.schema !== releaseOperationLockSchema || owner.token !== lock.token ||
      resolve(String(owner.targetDir || '')) !== resolve(String(lock.targetDir || '')) ||
      resolve(String(owner.rollbackDir || '')) !== resolve(String(lock.rollbackDir || '')) ||
      owner.startedAt !== lock.startedAt || Number.isNaN(Date.parse(String(owner.startedAt || '')))) {
    throw new Error('release operation lock owner differs from the active transaction')
  }
  return owner
}

const releaseTransactionPhases = [
  'prepared', 'ingress_stopped', 'base_env_patch_started', 'base_env_patched',
  'target_preflighted', 'data_transition_started', 'data_ready', 'private_started',
  'private_verified', 'ledger_written', 'ingress_opened', 'external_verified',
  'recovery_started', 'recovery_data_restored', 'recovery_env_restore_started', 'recovery_env_restored', 'recovery_runtime_verified', 'recovery_private_started',
  'recovery_private_verified', 'recovery_ledger_restored', 'recovery_ingress_opened', 'recovery_external_verified'
]

function releaseTransactionPath(lock) { return join(resolve(lock.path), 'transaction.json') }

export function validateReleaseTransactionJournal(value, lock) {
  const keys = Object.keys(value || {}).sort()
  const expected = ['action', 'baseEnvPatch', 'baseEnvPatchMode', 'baselineProjectContainers', 'baselineProjectResources', 'createdAt', 'nextLedger', 'phase',
    'priorLedger', 'recoveryFromPhase', 'rollbackBundleSha256', 'rollbackRenderedComposeSha256', 'schema', 'targetBundleSha256',
    'targetRenderedComposeSha256', 'token', 'updatedAt'].sort()
  if (value?.schema !== releaseTransactionSchema || JSON.stringify(keys) !== JSON.stringify(expected) ||
      value.token !== lock.token || !['activated', 'rolledBack'].includes(value.action) ||
      !releaseTransactionPhases.includes(value.phase) || !shaPattern.test(String(value.targetBundleSha256 || '')) ||
      !shaPattern.test(String(value.rollbackBundleSha256 || '')) ||
      !(value.targetRenderedComposeSha256 === null || shaPattern.test(String(value.targetRenderedComposeSha256 || ''))) ||
      !shaPattern.test(String(value.rollbackRenderedComposeSha256 || '')) || Number.isNaN(Date.parse(value.createdAt)) ||
      Number.isNaN(Date.parse(value.updatedAt)) || !Array.isArray(value.baselineProjectContainers) ||
      !value.baselineProjectResources || !Array.isArray(value.baselineProjectResources.networks) ||
      !Array.isArray(value.baselineProjectResources.volumes)) throw new Error('release transaction journal is invalid')
  validateActiveReleaseLedger(value.nextLedger)
  if (value.priorLedger !== null) validateActiveReleaseLedger(value.priorLedger)
  if (!['activate', 'rollback', null].includes(value.baseEnvPatchMode) || (value.baseEnvPatch === null) !== (value.baseEnvPatchMode === null)) {
    throw new Error('release transaction journal base env patch mode is invalid')
  }
  if (value.baseEnvPatch !== null) {
    const plan = validateBaseEnvPatchPlan(value.baseEnvPatch)
    const activationBinding = value.baseEnvPatchMode === 'activate' && value.action === 'activated' &&
      plan.transactionToken === value.token && plan.targetReleaseCommit === value.nextLedger.active.releaseCommit &&
      plan.rollbackReleaseCommit === value.priorLedger?.active.releaseCommit && plan.targetLedgerGeneration === value.nextLedger.generation
    const rollbackBinding = value.baseEnvPatchMode === 'rollback' && value.action === 'rolledBack' &&
      plan.targetReleaseCommit === value.priorLedger?.active.releaseCommit &&
      plan.rollbackReleaseCommit === value.nextLedger.active.releaseCommit && plan.targetLedgerGeneration === value.priorLedger?.generation
    if (value.priorLedger === null || (!activationBinding && !rollbackBinding)) {
      throw new Error('release transaction journal base env patch binding is invalid')
    }
  }
  const recovery = String(value.phase).startsWith('recovery_')
  if ((recovery && !forwardReleasePhases.includes(value.recoveryFromPhase)) || (!recovery && value.recoveryFromPhase !== null)) {
    throw new Error('release transaction journal recovery origin is invalid')
  }
  const preflightOrigin = recovery ? value.recoveryFromPhase : value.phase
  if (releasePhaseAtLeast(preflightOrigin, 'target_preflighted') && !shaPattern.test(String(value.targetRenderedComposeSha256 || ''))) {
    throw new Error('release transaction journal lacks its durable target preflight digest')
  }
  return value
}

async function readReleaseTransactionJournal(lock) {
  await assertReleaseOperationLock(lock)
  const path = releaseTransactionPath(lock)
  const info = await lstat(path)
  if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o600) throw new Error('release transaction journal must be a private regular file')
  return validateReleaseTransactionJournal(parseJSON(await readFile(path), 'release transaction journal'), lock)
}

async function cleanReleaseTransactionTemps(lock) {
  await assertReleaseOperationLock(lock)
  for (const name of await readdir(lock.path)) {
    if (/^\.transaction\.json\.[0-9a-f-]{36}\.tmp$/.test(name)) await unlink(join(lock.path, name))
  }
  await syncDirectory(lock.path)
  await assertReleaseOperationLock(lock)
}

async function writeReleaseTransactionJournal(lock, journal, phase = journal.phase) {
  const next = validateReleaseTransactionJournal({ ...journal, phase, updatedAt: new Date().toISOString() }, lock)
  await writeAtomicReplace(releaseTransactionPath(lock), jsonLine(next), 0o600)
  await assertReleaseOperationLock(lock)
  return next
}

async function loadReleaseOperationLockForResume(targetReleaseDir) {
  const targetDir = resolve(targetReleaseDir)
  const path = releaseOperationLockPath(targetDir)
  const ownerPath = join(path, 'owner.json')
  const ownerInfo = await lstat(ownerPath)
  if (!ownerInfo.isFile() || ownerInfo.isSymbolicLink() || (ownerInfo.mode & 0o777) !== 0o600) {
    throw new Error('release resume lock owner is invalid')
  }
  const owner = parseJSON(await readFile(ownerPath), 'release operation lock owner')
  const lock = { path, token: owner.token, targetDir: owner.targetDir, rollbackDir: owner.rollbackDir, startedAt: owner.startedAt }
  if (resolve(owner.targetDir) !== targetDir) throw new Error('release resume target differs from the durable operation lock')
  await assertReleaseOperationLock(lock)
  let released = false
  return { ...lock, release: async () => {
    if (released) return
    await releaseOperationLockDirectory(lock)
    released = true
  } }
}

async function releaseOperationLockDirectory(lock) {
  await assertReleaseOperationLock(lock)
  const completedPath = `${lock.path}.completed-${lock.token}`
  await rename(lock.path, completedPath)
  await syncDirectory(dirname(lock.path))
  await rm(completedPath, { recursive: true })
  await syncDirectory(dirname(lock.path))
}

// A durable lock fails closed after an operator crash. The receipted resume or
// recover commands reuse its exact token and journal; PID/age never steals it
// and operators never delete it by hand.
export async function acquireReleaseOperationLock(targetReleaseDir, rollbackReleaseDir) {
  const targetDir = resolve(targetReleaseDir)
  const rollbackDir = resolve(rollbackReleaseDir)
  const parent = dirname(targetDir)
  if (targetDir === rollbackDir || dirname(rollbackDir) !== parent) {
    throw new Error('release operation lock requires distinct sibling releases')
  }
  const parentInfo = await lstat(parent)
  if (!parentInfo.isDirectory() || parentInfo.isSymbolicLink() || (parentInfo.mode & 0o022) !== 0) {
    throw new Error('release operation parent must be a non-symlink directory without group/world write access')
  }
  const path = releaseOperationLockPath(targetDir)
  const token = randomUUID()
  const startedAt = new Date().toISOString()
  try {
    await mkdir(path, { mode: 0o700 })
  } catch (error) {
    if (error?.code === 'EEXIST') throw new Error('another release operation is active or left a stale fail-closed lock')
    throw error
  }
  const lock = { path, token, targetDir, rollbackDir, startedAt }
  try {
    await writeExclusive(join(path, 'owner.json'), jsonLine({ schema: releaseOperationLockSchema, token, targetDir, rollbackDir, pid: process.pid, startedAt }), 0o600)
    await syncDirectory(path)
    await syncDirectory(parent)
    await assertReleaseOperationLock(lock)
  } catch (error) {
    await rm(path, { recursive: true, force: true }).catch(() => {})
    await syncDirectory(parent).catch(() => {})
    throw error
  }
  let released = false
  return {
    ...lock,
    release: async () => {
      if (released) return
      await releaseOperationLockDirectory(lock)
      released = true
    }
  }
}

function releaseLedgerEntry(releaseDir, receipt) {
  return {
    releaseDir: resolve(releaseDir),
    releaseCommit: receipt.source.releaseCommit,
    bundleSha256: receipt.bundleSha256,
    meetingassistImageId: receipt.images.meetingassist.imageId,
    renderRunnerImageId: receipt.images.renderRunner.imageId
  }
}

export function validateActiveReleaseLedger(ledger) {
  if (ledger?.schema !== activeReleaseLedgerSchema || !Number.isSafeInteger(ledger.generation) || ledger.generation < 1 ||
      typeof ledger.updatedAt !== 'string' || Number.isNaN(Date.parse(ledger.updatedAt))) {
    throw new Error('active release ledger is invalid')
  }
  for (const [label, entry] of [['active', ledger.active], ['previous', ledger.previous]]) {
    if (!entry || resolve(String(entry.releaseDir || '')) !== entry.releaseDir ||
        !commitPattern.test(String(entry.releaseCommit || '')) || !shaPattern.test(String(entry.bundleSha256 || '')) ||
        !localImageIdPattern.test(String(entry.meetingassistImageId || '')) || !localImageIdPattern.test(String(entry.renderRunnerImageId || ''))) {
      throw new Error(`active release ledger ${label} entry is invalid`)
    }
  }
  if (ledger.active.releaseDir === ledger.previous.releaseDir || ledger.active.bundleSha256 === ledger.previous.bundleSha256) {
    throw new Error('active release ledger must retain distinct active and previous releases')
  }
  return ledger
}

function assertLedgerEntryMatches(entry, releaseDir, receipt, label) {
  const expected = releaseLedgerEntry(releaseDir, receipt)
  if (JSON.stringify(entry) !== JSON.stringify(expected)) throw new Error(`${label} release ledger entry differs from the verified bundle`)
}

export function validateReleaseTransition(action, ledger, targetDir, targetReceipt, rollbackDir, rollbackReceipt) {
  if (!['activated', 'rolledBack'].includes(action)) throw new Error('release transition action is invalid')
  if (ledger) assertLedgerEntryMatches(ledger.active, rollbackDir, rollbackReceipt, 'current active')
  if (action === 'rolledBack') {
    if (!ledger) throw new Error('rollback requires an existing active release ledger')
    assertLedgerEntryMatches(ledger.previous, targetDir, targetReceipt, 'rollback target')
  }
}

async function readActiveReleaseLedger(releaseDir) {
  const path = releaseLedgerPath(releaseDir)
  try {
    const info = await lstat(path)
    if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o077) !== 0) throw new Error('active release ledger must be a private regular file')
    return validateActiveReleaseLedger(parseJSON(await readFile(path), 'active release ledger'))
  } catch (error) {
    if (error?.code === 'ENOENT') return null
    throw error
  }
}

function nextActiveReleaseLedger(activeDir, activeReceipt, previousDir, previousReceipt, priorLedger) {
  return validateActiveReleaseLedger({
    schema: activeReleaseLedgerSchema,
    generation: (priorLedger?.generation || 0) + 1,
    updatedAt: new Date().toISOString(),
    active: releaseLedgerEntry(activeDir, activeReceipt),
    previous: releaseLedgerEntry(previousDir, previousReceipt)
  })
}

async function writeActiveReleaseLedger(releaseDir, ledger) {
  validateActiveReleaseLedger(ledger)
  await writeAtomicReplace(releaseLedgerPath(releaseDir), jsonLine(ledger), 0o600)
  return ledger
}

async function restoreActiveReleaseLedger(releaseDir, priorLedger, nextLedger, ledgerCommitAttempted) {
  const current = await readActiveReleaseLedger(releaseDir)
  if (sha256(canonical(current)) === sha256(canonical(priorLedger))) return current
  if (!ledgerCommitAttempted || sha256(canonical(current)) !== sha256(canonical(nextLedger))) {
    throw new Error('active release ledger changed outside the failed transaction; recovery will not overwrite it')
  }
  if (priorLedger) return writeActiveReleaseLedger(releaseDir, priorLedger)
  const path = releaseLedgerPath(releaseDir)
  try {
    const info = await lstat(path)
    if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o077) !== 0) {
      throw new Error('active release ledger cannot be safely removed during recovery')
    }
    await unlink(path)
    await syncDirectory(dirname(path))
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error
  }
  return null
}

export function composeCommandPrefix(baseEnv, runtimeEnv, candidateCompose, projectName = 'digitalocean', profile = 'render') {
  if (projectName !== 'digitalocean') throw new Error('release Compose project name must remain digitalocean to preserve named volumes')
  if (!['render', '*'].includes(profile)) throw new Error('release Compose profile preflight is invalid')
  return ['compose', '--project-name', 'digitalocean', '--project-directory', dirname(resolve(candidateCompose)),
    '--env-file', resolve(baseEnv), '--env-file', resolve(runtimeEnv), '--file', resolve(candidateCompose), '--profile', profile]
}

export function composeActivationArgs(baseEnv, runtimeEnv, candidateCompose, projectName = 'digitalocean') {
  return [...composeCommandPrefix(baseEnv, runtimeEnv, candidateCompose, projectName),
    'up', '-d', '--no-build', '--wait', '--wait-timeout', '360']
}

export function composePrivateActivationArgs(baseEnv, runtimeEnv, candidateCompose, projectName = 'digitalocean') {
  return [...composeCommandPrefix(baseEnv, runtimeEnv, candidateCompose, projectName),
    'up', '-d', '--no-build', '--wait', '--wait-timeout', '360',
    'meetingassist', 'canonical-postgres', 'render-runner', 'coturn']
}

export function composeIngressArgs(baseEnv, runtimeEnv, candidateCompose, operation, projectName = 'digitalocean') {
  const prefix = composeCommandPrefix(baseEnv, runtimeEnv, candidateCompose, projectName)
  if (operation === 'stop') return [...prefix, 'stop', '--timeout', '30', 'caddy']
  if (operation === 'start') return [...prefix, 'up', '-d', '--no-build', '--wait', '--wait-timeout', '60', 'caddy']
  throw new Error('release ingress operation is invalid')
}

export function strideE10W4MaintenanceArgs(baseEnv, runtimeEnv, candidateCompose, operation, projectName = 'digitalocean', imageID = '') {
  const flags = {
    activate: '-stride-e10-w4-activate-network',
    verify: '-stride-e10-w4-verify-network-activation',
    verifyRuntime: '-stride-e10-w4-verify-network-runtime',
    rollback: '-stride-e10-w4-rollback-network',
    verifyRollback: '-stride-e10-w4-verify-network-rollback'
  }
  if (!Object.hasOwn(flags, operation)) throw new Error('STRIDE E10 W4 maintenance operation is invalid')
  if (operation === 'verify' || operation === 'verifyRuntime' || operation === 'verifyRollback') {
    if (!localImageIdPattern.test(String(imageID))) throw new Error('STRIDE E10 W4 verifier image is invalid')
    return ['run', '--rm', '--read-only', '--network', 'none', '--env-file', resolve(baseEnv), '--env-file', resolve(runtimeEnv),
      '--volume', 'digitalocean_meeting_data:/app/data:ro',
      '--volume', 'digitalocean_usage_ledger:/app/data/usage:ro',
      '--volume', 'digitalocean_codex_queue:/app/codex-queue:ro',
      '--volume', 'digitalocean_render_queue:/app/render-queue:ro',
      '--entrypoint', '/app/meetingassist', imageID, flags[operation]]
  }
  const args = [...composeCommandPrefix(baseEnv, runtimeEnv, candidateCompose, projectName), 'run', '--rm', '--no-deps']
  return [...args, '--entrypoint', '/app/meetingassist', 'meetingassist', flags[operation]]
}

export function releaseActivationProgress(releaseCommit, state, startedAt, now = Date.now()) {
  return {
    schema: 'bonfire.release-activation-progress.v1', phase: 'candidate_startup', state,
    releaseCommit: String(releaseCommit || 'unknown'), elapsedSeconds: Math.max(0, Math.floor((now - startedAt) / 1000))
  }
}

// Only deliberate Docker transport values survive. The one added interpolation
// value points Compose at the existing secret-bearing base env without copying
// it into the retained candidate bundle.
export function releaseComposeEnvironment(source = process.env, baseEnv = '') {
  const allowed = new Set([
    'PATH', 'HOME', 'TMPDIR', 'TMP', 'TEMP', 'XDG_RUNTIME_DIR',
    'DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH', 'DOCKER_CONFIG', 'SSH_AUTH_SOCK'
  ])
  const environment = Object.fromEntries(Object.entries(source).filter(([name]) => allowed.has(name)))
  if (baseEnv) environment.BONFIRE_BASE_ENV_FILE = resolve(baseEnv)
  return environment
}

async function preflightComposeBundle(options, bundle, expectedSha256 = '') {
  const composeEnv = releaseComposeEnvironment(process.env, options.baseEnv)
  const { stdout: composeVersionRaw } = await execFileAsync('docker', ['compose', 'version', '--format', 'json'], {
    env: composeEnv, maxBuffer: 4 << 20
  })
  const composeVersion = stableJSONValue(parseJSON(composeVersionRaw, 'Docker Compose version'))
  if (JSON.stringify(composeVersion) !== JSON.stringify(stableJSONValue(bundle.receipt.buildManifest?.toolchain?.dockerCompose))) {
    throw new Error('Docker Compose version differs from the receipted release build toolchain')
  }
  const renderConfig = async profile => {
    const composePrefix = composeCommandPrefix(options.baseEnv, bundle.paths.runtimeEnv, bundle.paths.candidateCompose, options.projectName, profile)
    const { stdout } = await execFileAsync('docker', [...composePrefix, 'config', '--no-env-resolution', '--format', 'json'], {
      cwd: dirname(bundle.paths.candidateCompose), env: composeEnv, maxBuffer: 32 << 20
    })
    return validateRenderedComposeConfig(parseJSON(stdout, 'rendered candidate Compose configuration'), bundle.receipt, {
      candidateRoot: bundle.paths.candidateRoot,
      candidateCaddyfile: bundle.paths.candidateCaddyfile,
      baseEnv: options.baseEnv,
      requireEnvFiles: true
    })
  }
  const activationConfig = await renderConfig('render')
  const allProfilesConfig = await renderConfig('*')
  const digest = renderedComposeSha256({ activationConfig, allProfilesConfig })
  if (expectedSha256 && digest !== expectedSha256) {
    throw new Error('rendered candidate Compose configuration changed during the locked release transaction')
  }
  return { sha256: digest }
}

async function verifyReleaseImages(receipt) {
  for (const image of [...Object.values(receipt.images), ...Object.values(receipt.sidecars)]) {
    const owned = receipt.images.meetingassist === image || receipt.images.renderRunner === image
    const inspected = await inspectPinnedImage(owned ? image.imageId : image.imageReference, image.platform)
    if (inspected.imageId !== image.imageId) throw new Error(`candidate image ${image.imageReference} differs from release receipt`)
    if (owned) {
      const { stdout: raw } = await execFileAsync('docker', ['image', 'inspect', image.imageId], { maxBuffer: 16 << 20 })
      verifyLabels(parseJSON(raw, 'Docker image inspect')[0]?.Config?.Labels || {}, receipt.source, receipt.buildInputManifestSha256)
    }
  }
}

function releaseBundleUsesStrideE10W4Network(bundle) {
  return bundle?.receipt?.schema === receiptSchemaW4 &&
    validateStrideE10W4DeploymentPolicy(bundle.receipt.strideE10W4).releaseMode === strideE10W4NetworkMode
}

export function strideE10W4ReleaseTransitionPlan(action, targetReceipt, rollbackReceipt) {
  if (!['activated', 'rolledBack'].includes(action)) throw new Error('release transition action is invalid')
  const live = receipt => receipt?.schema === receiptSchemaW4 &&
    validateStrideE10W4DeploymentPolicy(receipt.strideE10W4).releaseMode === strideE10W4NetworkMode
  const targetLive = live(targetReceipt)
  const rollbackLive = live(rollbackReceipt)
  return {
    activateTargetBeforeStart: targetLive && !rollbackLive,
    verifyTargetRuntimeBeforeStart: rollbackLive,
    rollbackCurrentBeforeExplicitRollback: false,
    rollbackTargetBeforeRecovery: targetLive && !rollbackLive,
    verifyRollbackRuntimeBeforeRecovery: rollbackLive,
    reactivateRollbackBeforeRecovery: false
  }
}

async function runStrideE10W4Maintenance(options, bundle, operation) {
  if (!releaseBundleUsesStrideE10W4Network(bundle) && operation !== 'verifyRuntime') return
  await execFileAsync('docker', strideE10W4MaintenanceArgs(options.baseEnv, bundle.paths.runtimeEnv,
    bundle.paths.candidateCompose, operation, options.projectName, bundle.receipt.images.meetingassist.imageId), {
    cwd: dirname(bundle.paths.candidateCompose), env: releaseComposeEnvironment(process.env, options.baseEnv), maxBuffer: 32 << 20
  })
}

async function stopReleaseApplication(options, bundle) {
  await execFileAsync('docker', [...composeCommandPrefix(options.baseEnv, bundle.paths.runtimeEnv,
    bundle.paths.candidateCompose, options.projectName), 'stop', '--timeout', '30', 'meetingassist'], {
    cwd: dirname(bundle.paths.candidateCompose), env: releaseComposeEnvironment(process.env, options.baseEnv), maxBuffer: 32 << 20
  })
}

async function setReleaseIngress(options, bundle, operation) {
  await execFileAsync('docker', composeIngressArgs(options.baseEnv, bundle.paths.runtimeEnv,
    bundle.paths.candidateCompose, operation, options.projectName), {
    cwd: dirname(bundle.paths.candidateCompose), env: releaseComposeEnvironment(process.env, options.baseEnv), maxBuffer: 32 << 20
  })
}

async function startReleaseApplicationPrivately(options, bundle) {
  await execFileAsync('docker', composePrivateActivationArgs(options.baseEnv, bundle.paths.runtimeEnv,
    bundle.paths.candidateCompose, options.projectName), {
    cwd: dirname(bundle.paths.candidateCompose), env: releaseComposeEnvironment(process.env, options.baseEnv), maxBuffer: 32 << 20
  })
}

async function verifyPrivateRelease(options, bundle, expectedRenderedComposeSha256 = '', baseEnvPatch = null, baseEnvPatchState = '') {
  const preflight = await preflightComposeBundle(options, bundle, expectedRenderedComposeSha256)
  const containers = await inspectProjectServiceInventory(true)
  const { stdout: caddyRaw } = await execFileAsync('docker', ['container', 'inspect', containers.caddy], { maxBuffer: 16 << 20 })
  if (parseJSON(caddyRaw, 'private Docker caddy container inspect')[0]?.State?.Status === 'running') {
    throw new Error('public Caddy ingress is running during private candidate verification')
  }
  const expectedImages = {
    meetingassist: bundle.receipt.images.meetingassist,
    'render-runner': bundle.receipt.images.renderRunner,
    'render-queue-init': bundle.receipt.images.renderRunner,
    'canonical-postgres': bundle.receipt.sidecars.canonicalPostgres,
    coturn: bundle.receipt.sidecars.coturn
  }
  let appContainer = ''
  for (const [service, image] of Object.entries(expectedImages)) {
    const container = containers[service]
    const { stdout: raw } = await execFileAsync('docker', ['container', 'inspect', container], { maxBuffer: 16 << 20 })
    const inspected = parseJSON(raw, `private Docker ${service} container inspect`)[0]
    if (String(inspected?.Image || '').toLowerCase() !== image.imageId) throw new Error(`private candidate ${service} image differs from release receipt`)
    if (service === 'render-queue-init') {
      if (inspected?.State?.Status !== 'exited' || inspected?.State?.ExitCode !== 0) throw new Error('private render-queue-init did not complete')
    } else if (inspected?.State?.Status !== 'running') throw new Error(`private candidate ${service} is not running`)
    if (service === 'meetingassist') {
      const environment = environmentFromInspect(inspected)
      verifyRuntimeEnvironment(environment, bundle.receipt)
      verifyBaseEnvPatchRuntimeEnvironment(environment, baseEnvPatch, baseEnvPatchState)
      appContainer = container
    }
  }
  for (const [path, probe] of [['/healthz', 'health'], ['/readyz', 'readiness']]) {
    const { stdout } = await execFileAsync('docker', ['exec', appContainer, 'curl', '-fsS', `http://127.0.0.1:3000${path}`], { maxBuffer: 4 << 20 })
    verifyProbeRelease(parseJSON(stdout, `private ${probe} probe`), bundle.receipt, probe)
  }
  return { verified: true, renderedComposeSha256: preflight.sha256 }
}

async function activateStrideE10W4Network(options, bundle) {
  if (!releaseBundleUsesStrideE10W4Network(bundle)) return
  await stopReleaseApplication(options, bundle)
  await runStrideE10W4Maintenance(options, bundle, 'activate')
  await runStrideE10W4Maintenance(options, bundle, 'verify')
}

async function rollbackStrideE10W4Network(options, bundle) {
  if (!releaseBundleUsesStrideE10W4Network(bundle)) return
  await stopReleaseApplication(options, bundle)
  await runStrideE10W4Maintenance(options, bundle, 'rollback')
  await runStrideE10W4Maintenance(options, bundle, 'verifyRollback')
}

async function applyReleaseBundle(options, bundle, strideE10W4Transition = 'activate') {
  if (!['activate', 'verifyRuntime'].includes(strideE10W4Transition)) {
    throw new Error('STRIDE E10 W4 apply transition is invalid')
  }
  const startedAt = Date.now()
  const releaseCommit = bundle.source?.releaseCommit || 'unknown'
  const progress = state => process.stderr.write(`${JSON.stringify(releaseActivationProgress(releaseCommit, state, startedAt))}\n`)
  progress('starting')
  const heartbeat = setInterval(() => progress('waiting_for_ready'), 15_000)
  try {
    if (strideE10W4Transition === 'activate') {
      await activateStrideE10W4Network(options, bundle)
    } else {
      await runStrideE10W4Maintenance(options, bundle, 'verifyRuntime')
    }
    await execFileAsync('docker', composeActivationArgs(options.baseEnv, bundle.paths.runtimeEnv, bundle.paths.candidateCompose, options.projectName), {
      cwd: dirname(bundle.paths.candidateCompose), env: releaseComposeEnvironment(process.env, options.baseEnv), maxBuffer: 32 << 20
    })
    progress('ready')
  } catch (error) {
    progress('failed')
    throw error
  } finally {
    clearInterval(heartbeat)
  }
}

async function assertActiveReleaseLedgerUnchanged(releaseDir, expected) {
  const current = await readActiveReleaseLedger(releaseDir)
  if (sha256(canonical(current)) !== sha256(canonical(expected))) {
    throw new Error('active release ledger changed during the locked release transaction')
  }
}

// The lock is released only after one of two completely proven terminal
// states: the target is running with its durable ledger, or the retained
// rollback bundle is running with the exact pre-transaction ledger restored.
// Any ambiguous recovery deliberately leaves the fail-closed lock behind for
// an operator inspection ceremony.
export async function executeReleaseTransaction({
  operationLock,
  priorLedger,
  nextLedger,
  readLedger,
  preflightTarget,
  applyTarget,
  verifyTarget,
  writeLedger,
  restoreRollback,
  restoreLedger
}) {
  if (!operationLock || typeof operationLock.release !== 'function') throw new Error('release transaction lock is invalid')
  for (const [name, callback] of Object.entries({ readLedger, preflightTarget, applyTarget, verifyTarget, writeLedger, restoreRollback, restoreLedger })) {
    if (typeof callback !== 'function') throw new Error(`release transaction ${name} callback is invalid`)
  }

  try {
    if (sha256(canonical(await readLedger())) !== sha256(canonical(priorLedger))) {
      throw new Error('active release ledger changed before the locked release transaction')
    }
  } catch (error) {
    await operationLock.release()
    throw error
  }

  try {
    await preflightTarget()
  } catch (error) {
    await operationLock.release()
    throw error
  }

  let verified
  let ledgerCommitAttempted = false
  try {
    await applyTarget()
    verified = await verifyTarget()
    if (sha256(canonical(await readLedger())) !== sha256(canonical(priorLedger))) {
      throw new Error('active release ledger changed before target commit')
    }
    ledgerCommitAttempted = true
    await writeLedger(nextLedger)
    if (sha256(canonical(await readLedger())) !== sha256(canonical(nextLedger))) {
      throw new Error('active release ledger does not match the committed target')
    }
    // Re-probe after the ledger commit. A target that fails in the commit
    // window is not allowed to remain the claimed active release.
    verified = await verifyTarget()
    if (sha256(canonical(await readLedger())) !== sha256(canonical(nextLedger))) {
      throw new Error('active release ledger changed after target commit')
    }
  } catch (transactionError) {
    try {
      await restoreRollback()
      await restoreLedger(priorLedger, { nextLedger, ledgerCommitAttempted })
      if (sha256(canonical(await readLedger())) !== sha256(canonical(priorLedger))) {
        throw new Error('active release ledger was not restored after rollback')
      }
    } catch (recoveryError) {
      throw new AggregateError([transactionError, recoveryError],
        'release transaction failed and retained-tool recovery is ambiguous; the fail-closed operation lock was retained')
    }
    try {
      await operationLock.release()
    } catch (unlockError) {
      throw new AggregateError([transactionError, unlockError],
        'release transaction failed, rollback was verified, but the fail-closed operation lock could not be released')
    }
    const recovered = new Error(`release transaction failed; the retained rollback tool restored the prior release and ledger: ${transactionError?.message || transactionError}`,
      { cause: transactionError })
    recovered.releaseTransactionRecovered = true
    throw recovered
  }

  await operationLock.release()
  return verified
}

export async function loadRetainedRollbackTool(executingPath, expectedDigest) {
  const path = resolve(executingPath)
  await verifyExecutingReleaseTool(expectedDigest, path)
  const url = pathToFileURL(path)
  url.searchParams.set('sha256', expectedDigest)
  const retained = await import(url.href)
  await verifyExecutingReleaseTool(expectedDigest, path)
  if (typeof retained.restoreReleaseBundleAfterFailedActivation !== 'function') {
    throw new Error('retained rollback tool lacks the verified automatic-restore entrypoint')
  }
  return retained
}

export async function verifyRetainedReleaseActivator(executingPath, rollbackPaths, rollbackSource) {
  const path = resolve(String(executingPath || ''))
  if (path !== resolve(rollbackPaths.releaseTool)) {
    throw new Error('release activation must execute the currently serving retained release tool')
  }
  await verifyExecutingReleaseTool(rollbackSource.configFiles['scripts/bonfire-release.mjs'], path)
}

// This entrypoint is invoked from the verified retained rollback module, not
// from the candidate module whose activation failed. It is intentionally
// transaction-internal: the caller must prove the still-held sibling lock and
// bind the failed target plus retained release exactly.
export async function restoreReleaseBundleAfterFailedActivation(options) {
  for (const [name, value] of [['--release-dir', options.releaseDir], ['--failed-release-dir', options.failedReleaseDir],
    ['--base-env', options.baseEnv], ['--health-url', options.healthUrl], ['--ready-url', options.readyUrl],
    ['--executing-tool-path', options.executingToolPath], ['--operation-lock-path', options.operationLockPath],
    ['--operation-lock-token', options.operationLockToken], ['--operation-lock-started-at', options.operationLockStartedAt],
    ['--rollback-rendered-compose-sha256', options.rollbackRenderedComposeSha256]]) required(name, value)
  if (!Array.isArray(options.baselineProjectContainers)) throw new Error('--baseline-project-containers is required')
  if (!options.baselineProjectResources || !Array.isArray(options.baselineProjectResources.networks) ||
      !Array.isArray(options.baselineProjectResources.volumes)) throw new Error('--baseline-project-resources is required')
  if (options.verifyStrideE10W4RuntimeLineage !== undefined && options.verifyStrideE10W4RuntimeLineage !== true) {
    throw new Error('--verify-stride-e10-w4-runtime-lineage must be exactly true when supplied')
  }
  const releaseDir = resolve(options.releaseDir)
  const failedReleaseDir = resolve(options.failedReleaseDir)
  const lock = {
    path: resolve(options.operationLockPath), token: options.operationLockToken,
    targetDir: failedReleaseDir, rollbackDir: releaseDir, startedAt: options.operationLockStartedAt
  }
  await assertReleaseOperationLock(lock)
  const bundle = await loadReleaseBundle(options, { verifyTool: false })
  if (resolve(options.executingToolPath) !== resolve(bundle.paths.releaseTool) ||
      resolve(options.executingToolPath) !== resolve(fileURLToPath(import.meta.url))) {
    throw new Error('retained rollback entrypoint is not executing the receipted rollback tool')
  }
  await verifyExecutingReleaseTool(bundle.source.configFiles['scripts/bonfire-release.mjs'], options.executingToolPath)
  verifyReleaseEnvironmentFile(await readFile(bundle.paths.runtimeEnv, 'utf8'), bundle.receipt)
  await preflightComposeBundle(options, bundle, options.rollbackRenderedComposeSha256)
  await verifyReleaseImages(bundle.receipt)
  const resourceClaims = await removeFailedTargetProjectContainers(lock, options.baselineProjectContainers,
    releasePaths(failedReleaseDir).candidateCompose)
  await removeFailedTargetProjectResources(lock, options.baselineProjectResources, resourceClaims)
  await assertReleaseOperationLock(lock)
  await applyReleaseBundle(options, bundle, options.verifyStrideE10W4RuntimeLineage === true ? 'verifyRuntime' : 'activate')
  const verified = await verifyRunning(options, false, {
    verifyTool: false,
    verifyLedger: false,
    expectedRenderedComposeSha256: options.rollbackRenderedComposeSha256
  })
  if (projectResourceSnapshotSha256(await inspectProjectResources()) !== projectResourceSnapshotSha256(options.baselineProjectResources)) {
    throw new Error('retained rollback did not restore the exact pre-transaction Compose project resources')
  }
  return verified
}

const forwardReleasePhases = ['prepared', 'ingress_stopped', 'base_env_patch_started', 'base_env_patched', 'target_preflighted', 'data_transition_started', 'data_ready',
  'private_started', 'private_verified', 'ledger_written', 'ingress_opened', 'external_verified']

function releasePhaseAtLeast(phase, expected) {
  const actualIndex = forwardReleasePhases.indexOf(phase)
  const expectedIndex = forwardReleasePhases.indexOf(expected)
  if (actualIndex < 0 || expectedIndex < 0) throw new Error('release transaction phase is not forward-resumable')
  return actualIndex >= expectedIndex
}

export function strideE10W4RecoveryPlan(phase, transition) {
  if (!forwardReleasePhases.includes(phase) || !transition) throw new Error('release recovery plan is invalid')
  const ingressWasOpened = releasePhaseAtLeast(phase, 'ingress_opened')
  const privateAppMayHaveRun = releasePhaseAtLeast(phase, 'private_started')
  const activationMayHaveStarted = releasePhaseAtLeast(phase, 'data_transition_started')
  return {
    rollbackUnexposedInitialActivation: Boolean(transition.rollbackTargetBeforeRecovery && activationMayHaveStarted && !privateAppMayHaveRun),
    verifyRetainedRuntimeWithoutMutation: Boolean(transition.verifyRollbackRuntimeBeforeRecovery ||
      (transition.rollbackTargetBeforeRecovery && privateAppMayHaveRun)),
    preserveEvolvedBytes: privateAppMayHaveRun || ingressWasOpened
  }
}

export async function executeDurableReleasePhaseMachine({ phase, transition, effects, advance }) {
  if (!forwardReleasePhases.includes(phase) || !transition || typeof effects !== 'object' || typeof advance !== 'function') {
    throw new Error('durable release phase machine input is invalid')
  }
  for (const name of ['stopIngress', 'installTargetBaseEnv', 'assertTargetBaseEnv', 'preflightTarget', 'activateTarget', 'verifyTargetRuntime', 'startTargetPrivate', 'verifyTargetPrivate',
    'writeTargetLedger', 'openTargetIngress', 'verifyTargetExternal']) {
    if (typeof effects[name] !== 'function') throw new Error(`durable release phase effect ${name} is invalid`)
  }
  let current = phase
  const move = async (next, evidence) => { await advance(next, evidence); current = next }
  if (!releasePhaseAtLeast(current, 'ingress_stopped')) {
    await effects.stopIngress(); await move('ingress_stopped')
  }
  if (!releasePhaseAtLeast(current, 'base_env_patched')) {
    if (!releasePhaseAtLeast(current, 'base_env_patch_started')) await move('base_env_patch_started')
    await effects.installTargetBaseEnv(); await move('base_env_patched')
  }
  await effects.assertTargetBaseEnv()
  const preflight = await effects.preflightTarget()
  if (!releasePhaseAtLeast(current, 'target_preflighted')) await move('target_preflighted', preflight)
  if (!releasePhaseAtLeast(current, 'data_ready')) {
    if (transition.activateTargetBeforeStart) {
      if (!releasePhaseAtLeast(current, 'data_transition_started')) await move('data_transition_started')
      await effects.activateTarget()
    } else if (transition.verifyTargetRuntimeBeforeStart) await effects.verifyTargetRuntime()
    await move('data_ready')
  }
  if (!releasePhaseAtLeast(current, 'private_started')) { await effects.startTargetPrivate(); await move('private_started') }
  if (!releasePhaseAtLeast(current, 'private_verified')) { await effects.verifyTargetPrivate(); await move('private_verified') }
  if (!releasePhaseAtLeast(current, 'ledger_written')) { await effects.assertTargetBaseEnv(); await effects.writeTargetLedger(); await move('ledger_written') }
  if (!releasePhaseAtLeast(current, 'ingress_opened')) { await effects.assertTargetBaseEnv(); await effects.openTargetIngress(); await move('ingress_opened') }
  if (!releasePhaseAtLeast(current, 'external_verified')) { await effects.verifyTargetExternal(); await move('external_verified') }
  await effects.assertTargetBaseEnv()
  return current
}

const recoveryReleasePhases = ['recovery_started', 'recovery_data_restored', 'recovery_env_restore_started', 'recovery_env_restored',
  'recovery_runtime_verified', 'recovery_private_started', 'recovery_private_verified', 'recovery_ledger_restored',
  'recovery_ingress_opened', 'recovery_external_verified']

function recoveryPhaseAtLeast(phase, expected) {
  const actualIndex = recoveryReleasePhases.indexOf(phase)
  const expectedIndex = recoveryReleasePhases.indexOf(expected)
  if (actualIndex < 0 || expectedIndex < 0) throw new Error('release transaction phase is not recovery-resumable')
  return actualIndex >= expectedIndex
}

export async function executeDurableReleaseRecoveryPhaseMachine({ phase, effects, advance }) {
  if (!recoveryReleasePhases.includes(phase) || typeof effects !== 'object' || typeof advance !== 'function') {
    throw new Error('durable release recovery phase machine input is invalid')
  }
  for (const name of ['restoreTargetData', 'restoreRecoveryBaseEnv', 'verifyRollbackRuntime', 'startRollbackPrivate', 'verifyRollbackPrivate',
    'restoreLedger', 'openRollbackIngress', 'verifyRollbackExternal']) {
    if (typeof effects[name] !== 'function') throw new Error(`durable release recovery phase effect ${name} is invalid`)
  }
  let current = phase
  const move = async next => { await advance(next); current = next }
  if (!recoveryPhaseAtLeast(current, 'recovery_data_restored')) {
    await effects.restoreTargetData(); await move('recovery_data_restored')
  }
  if (!recoveryPhaseAtLeast(current, 'recovery_env_restored')) {
    if (!recoveryPhaseAtLeast(current, 'recovery_env_restore_started')) await move('recovery_env_restore_started')
    await effects.restoreRecoveryBaseEnv()
    await move('recovery_env_restored')
  }
  if (!recoveryPhaseAtLeast(current, 'recovery_runtime_verified')) {
    await effects.verifyRollbackRuntime(); await move('recovery_runtime_verified')
  }
  if (!recoveryPhaseAtLeast(current, 'recovery_private_started')) {
    await effects.startRollbackPrivate(); await move('recovery_private_started')
  }
  if (!recoveryPhaseAtLeast(current, 'recovery_private_verified')) {
    await effects.verifyRollbackPrivate(); await move('recovery_private_verified')
  }
  if (!recoveryPhaseAtLeast(current, 'recovery_ledger_restored')) {
    await effects.restoreLedger(); await move('recovery_ledger_restored')
  }
  if (!recoveryPhaseAtLeast(current, 'recovery_ingress_opened')) {
    await effects.openRollbackIngress(); await move('recovery_ingress_opened')
  }
  if (!recoveryPhaseAtLeast(current, 'recovery_external_verified')) {
    await effects.verifyRollbackExternal(); await move('recovery_external_verified')
  }
  return current
}

async function loadDurableReleaseContext(options, operationLock, journal) {
  const targetDir = resolve(operationLock.targetDir)
  const rollbackDir = resolve(operationLock.rollbackDir)
  if (journal.baseEnvPatch && resolve(options.baseEnv) !== journal.baseEnvPatch.baseEnvPath) {
    throw new Error('resume base env path differs from the durable patch plan')
  }
  const targetOptions = { ...options, releaseDir: targetDir, rollbackReleaseDir: rollbackDir }
  const rollbackOptions = { ...options, releaseDir: rollbackDir, rollbackReleaseDir: targetDir }
  const target = await loadReleaseBundle(targetOptions, { verifyTool: false })
  const rollback = await loadReleaseBundle(rollbackOptions, { verifyTool: false })
  await verifyRetainedReleaseActivator(process.argv[1], rollback.paths, rollback.source)
  await loadRetainedRollbackTool(rollback.paths.releaseTool, rollback.source.configFiles['scripts/bonfire-release.mjs'])
  if (target.receipt.bundleSha256 !== journal.targetBundleSha256 || rollback.receipt.bundleSha256 !== journal.rollbackBundleSha256) {
    throw new Error('release transaction bundles differ from the durable receipt binding')
  }
  verifyReleaseEnvironmentFile(await readFile(target.paths.runtimeEnv, 'utf8'), target.receipt)
  verifyReleaseEnvironmentFile(await readFile(rollback.paths.runtimeEnv, 'utf8'), rollback.receipt)
  await verifyReleaseImages(target.receipt)
  await verifyReleaseImages(rollback.receipt)
  return { targetDir, rollbackDir, targetOptions, rollbackOptions, target, rollback,
    transition: strideE10W4ReleaseTransitionPlan(journal.action, target.receipt, rollback.receipt) }
}

export function releaseTransactionCompletionEvidence(journal, initialPhase) {
  if (!journal || !['activated', 'rolledBack'].includes(journal.action) ||
      !Number.isSafeInteger(journal.nextLedger?.generation) || journal.nextLedger.generation < 1 ||
      !['activate', 'rollback', null].includes(journal.baseEnvPatchMode) ||
      (journal.baseEnvPatchMode === null) !== (journal.baseEnvPatch === null) ||
      typeof initialPhase !== 'string') {
    throw new Error('release completion evidence is invalid')
  }
  return {
    action: journal.action,
    ledgerGeneration: journal.nextLedger.generation,
    resumed: initialPhase !== 'prepared',
    qualificationReceipt: journal.baseEnvPatch?.receiptPath || null,
    qualificationState: journal.baseEnvPatchMode === 'rollback'
      ? 'prior'
      : (journal.baseEnvPatchMode === 'activate' ? 'target' : null)
  }
}

async function resumeDurableReleaseTransaction(options, operationLock, journal) {
  const context = await loadDurableReleaseContext(options, operationLock, journal)
  let current = journal
  const advance = async (phase, evidence) => {
    if (phase === 'target_preflighted') {
      if (!shaPattern.test(String(evidence?.sha256 || ''))) throw new Error('target preflight did not return a durable rendered Compose digest')
      current = { ...current, targetRenderedComposeSha256: evidence.sha256 }
    }
    current = await writeReleaseTransactionJournal(operationLock, current, phase)
  }
  const assertForwardBaseEnv = () => current.baseEnvPatchMode === 'rollback'
    ? assertPriorBaseEnvRestored(operationLock, current.baseEnvPatch, 0, { requireReceipt: true })
    : assertTargetBaseEnvPatchInstalled(operationLock, current.baseEnvPatch)
  const assertOriginBaseEnv = () => current.baseEnvPatchMode === 'rollback'
    ? assertTargetBaseEnvPatchInstalled(operationLock, current.baseEnvPatch)
    : assertTargetBaseEnvPatchReady(operationLock, current.baseEnvPatch)
  const withForwardBaseEnv = async effect => {
    await assertForwardBaseEnv()
    const result = await effect()
    await assertForwardBaseEnv()
    return result
  }
  const withOriginBaseEnv = async effect => {
    await assertOriginBaseEnv()
    const result = await effect()
    await assertOriginBaseEnv()
    return result
  }
  const forwardRuntimeState = current.baseEnvPatchMode === 'rollback' ? 'prior' : 'target'
  await executeDurableReleasePhaseMachine({ phase: current.phase, transition: context.transition, advance, effects: {
    stopIngress: () => withOriginBaseEnv(() => setReleaseIngress(context.rollbackOptions, context.rollback, 'stop')),
    installTargetBaseEnv: () => current.baseEnvPatchMode === 'rollback'
      ? restorePriorBaseEnv(operationLock, current.baseEnvPatch, 0, { requireReceipt: true })
      : installTargetBaseEnvPatch(operationLock, current.baseEnvPatch),
    assertTargetBaseEnv: assertForwardBaseEnv,
    preflightTarget: () => withForwardBaseEnv(() => preflightComposeBundle(context.targetOptions, context.target, current.targetRenderedComposeSha256)),
    activateTarget: () => withForwardBaseEnv(() => activateStrideE10W4Network(context.targetOptions, context.target)),
    verifyTargetRuntime: () => withForwardBaseEnv(() => runStrideE10W4Maintenance(context.targetOptions, context.target, 'verifyRuntime')),
    startTargetPrivate: () => withForwardBaseEnv(() => startReleaseApplicationPrivately(context.targetOptions, context.target)),
    verifyTargetPrivate: () => withForwardBaseEnv(() => verifyPrivateRelease(context.targetOptions, context.target,
      current.targetRenderedComposeSha256, current.baseEnvPatch, forwardRuntimeState)),
    writeTargetLedger: () => withForwardBaseEnv(async () => {
    // Reassert the ingress fence at the ledger linearization point. This is
    // idempotent and prevents a privately verified candidate from becoming
    // ledger-active after an out-of-band Caddy restart.
    await setReleaseIngress(context.rollbackOptions, context.rollback, 'stop')
    const ledger = await readActiveReleaseLedger(context.targetDir)
    if (sha256(canonical(ledger)) === sha256(canonical(current.priorLedger))) {
      await writeActiveReleaseLedger(context.targetDir, current.nextLedger)
    } else if (sha256(canonical(ledger)) !== sha256(canonical(current.nextLedger))) {
      throw new Error('release ledger is neither durable prior nor target state during resume')
    }
    }),
    openTargetIngress: () => withForwardBaseEnv(() => setReleaseIngress(context.targetOptions, context.target, 'start')),
    verifyTargetExternal: () => withForwardBaseEnv(() => verifyRunning(context.targetOptions, false, { verifyTool: false, verifyLedger: true,
      expectedRenderedComposeSha256: current.targetRenderedComposeSha256,
      baseEnvPatch: current.baseEnvPatch, baseEnvPatchState: forwardRuntimeState }))
  } })
  // The receipt becomes committed only after private verification, ledger CAS,
  // ingress opening, and the ledger-bound external verifier all succeeded.
  const targetLedger = await readActiveReleaseLedger(context.targetDir)
  if (sha256(canonical(targetLedger)) !== sha256(canonical(current.nextLedger))) {
    throw new Error('target ledger drifted before the base env patch could be committed')
  }
  await assertForwardBaseEnv()
  const patchReceipt = current.baseEnvPatchMode === 'rollback'
    ? await commitPriorBaseEnvRestore(operationLock, current.baseEnvPatch, 0, { requireReceipt: true })
    : await commitTargetBaseEnvPatch(operationLock, current.baseEnvPatch)
  const expectedReceiptState = current.baseEnvPatchMode === 'rollback' ? 'prior_committed' : 'target_committed'
  if (current.baseEnvPatch && !patchReceipt) throw new Error('base env transition receipt is missing at commit')
  if (patchReceipt && patchReceipt.state !== expectedReceiptState) throw new Error('base env transition receipt is not committed')
  await assertForwardBaseEnv()
  const committedLedger = await readActiveReleaseLedger(context.targetDir)
  if (sha256(canonical(committedLedger)) !== sha256(canonical(current.nextLedger))) {
    throw new Error('target ledger drifted after the base env patch was committed')
  }
  await operationLock.release()
  return releaseTransactionCompletionEvidence(current, journal.phase)
}

async function recoverDurableReleaseTransaction(options, operationLock, journal) {
  const context = await loadDurableReleaseContext(options, operationLock, journal)
  let current = journal
  if (forwardReleasePhases.includes(current.phase)) {
    current = await writeReleaseTransactionJournal(operationLock, {
      ...current,
      recoveryFromPhase: current.phase
    }, 'recovery_started')
  }
  if (!recoveryReleasePhases.includes(current.phase) || !forwardReleasePhases.includes(current.recoveryFromPhase)) {
    throw new Error('release recovery journal has no exact forward failure phase')
  }
  const recoveryPlan = strideE10W4RecoveryPlan(current.recoveryFromPhase, context.transition)
  const recoveryPriorReceiptRequired = current.baseEnvPatchMode === 'rollback' ||
    releasePhaseAtLeast(current.recoveryFromPhase, 'base_env_patched')
  const advance = async phase => { current = await writeReleaseTransactionJournal(operationLock, current, phase) }
  const assertRecoveryBaseEnv = () => current.baseEnvPatchMode === 'rollback'
    ? assertTargetBaseEnvPatchInstalled(operationLock, current.baseEnvPatch)
    : assertPriorBaseEnvRestored(operationLock, current.baseEnvPatch, 0, { requireReceipt: recoveryPriorReceiptRequired })
  const assertForwardBaseEnv = () => current.baseEnvPatchMode === 'rollback'
    ? assertPriorBaseEnvRestored(operationLock, current.baseEnvPatch, 0, { requireReceipt: true })
    : assertTargetBaseEnvPatchInstalled(operationLock, current.baseEnvPatch)
  const withRecoveryBaseEnv = async effect => {
    await assertRecoveryBaseEnv()
    const result = await effect()
    await assertRecoveryBaseEnv()
    return result
  }
  const withForwardBaseEnv = async effect => {
    await assertForwardBaseEnv()
    const result = await effect()
    await assertForwardBaseEnv()
    return result
  }
  const recoveryRuntimeState = current.baseEnvPatchMode === 'rollback' ? 'target' : 'prior'
  // Every recovery entry reasserts the public fence. The first durable recovery
  // phase precedes this call, so an abrupt death can safely repeat it.
  await setReleaseIngress(context.rollbackOptions, context.rollback, 'stop')
  await executeDurableReleaseRecoveryPhaseMachine({ phase: current.phase, advance, effects: {
    // Restore the exact prior env before any retained application or read-only
    // maintenance container is started. Drift retains the lock and starts
    // nothing from the rollback release.
    restoreTargetData: async () => {
      if (recoveryPlan.rollbackUnexposedInitialActivation) {
        // A crash can occur after the durable intent but before activation
        // writes its terminal phase. Complete and reverse only that exact data
        // transition behind the ingress fence; both operations are idempotent.
        await withForwardBaseEnv(() => activateStrideE10W4Network(context.targetOptions, context.target))
        await withForwardBaseEnv(() => rollbackStrideE10W4Network(context.targetOptions, context.target))
      }
      const resourceClaims = await removeFailedTargetProjectContainers(operationLock, current.baselineProjectContainers,
        context.target.paths.candidateCompose)
      await removeFailedTargetProjectResources(operationLock, current.baselineProjectResources, resourceClaims)
    },
    restoreRecoveryBaseEnv: () => current.baseEnvPatchMode === 'rollback'
      ? reinstallCommittedTargetBaseEnvPatch(operationLock, current.baseEnvPatch)
      : restorePriorBaseEnv(operationLock, current.baseEnvPatch, 0, { requireReceipt: recoveryPriorReceiptRequired }),
    verifyRollbackRuntime: () => withRecoveryBaseEnv(async () => {
      await preflightComposeBundle(context.rollbackOptions, context.rollback, current.rollbackRenderedComposeSha256)
      if (recoveryPlan.verifyRetainedRuntimeWithoutMutation) {
        await runStrideE10W4Maintenance(context.rollbackOptions, context.rollback, 'verifyRuntime')
      }
    }),
    startRollbackPrivate: () => withRecoveryBaseEnv(() => startReleaseApplicationPrivately(context.rollbackOptions, context.rollback)),
    verifyRollbackPrivate: () => withRecoveryBaseEnv(() => verifyPrivateRelease(context.rollbackOptions, context.rollback,
      current.rollbackRenderedComposeSha256, current.baseEnvPatch, recoveryRuntimeState)),
    restoreLedger: async () => {
      const ledger = await readActiveReleaseLedger(context.targetDir)
      if (sha256(canonical(ledger)) === sha256(canonical(current.nextLedger))) {
        await writeActiveReleaseLedger(context.targetDir, current.priorLedger)
      } else if (sha256(canonical(ledger)) !== sha256(canonical(current.priorLedger))) {
        throw new Error('release ledger is neither durable prior nor target state during recovery')
      }
    },
    openRollbackIngress: () => withRecoveryBaseEnv(() => setReleaseIngress(context.rollbackOptions, context.rollback, 'start')),
    verifyRollbackExternal: () => withRecoveryBaseEnv(async () => {
      await verifyRunning(context.rollbackOptions, false, { verifyTool: false, verifyLedger: true,
        expectedRenderedComposeSha256: current.rollbackRenderedComposeSha256,
        baseEnvPatch: current.baseEnvPatch, baseEnvPatchState: recoveryRuntimeState })
      if (projectResourceSnapshotSha256(await inspectProjectResources()) !== projectResourceSnapshotSha256(current.baselineProjectResources)) {
        throw new Error('release recovery did not restore exact pre-transaction project resources')
      }
    })
  } })
  await assertRecoveryBaseEnv()
  const recoveryReceipt = current.baseEnvPatchMode === 'rollback'
    ? await commitTargetBaseEnvPatch(operationLock, current.baseEnvPatch)
    : await commitPriorBaseEnvRestore(operationLock, current.baseEnvPatch, 0, { requireReceipt: recoveryPriorReceiptRequired })
  const expectedRecoveryReceiptState = current.baseEnvPatchMode === 'rollback' ? 'target_committed' : 'prior_committed'
  if (current.baseEnvPatch && recoveryPriorReceiptRequired && !recoveryReceipt) {
    throw new Error('recovery base env transition receipt is missing at commit')
  }
  if (recoveryReceipt && recoveryReceipt.state !== expectedRecoveryReceiptState) {
    throw new Error('recovery base env transition receipt is not committed')
  }
  await assertRecoveryBaseEnv()
  const restoredLedger = await readActiveReleaseLedger(context.targetDir)
  if (sha256(canonical(restoredLedger)) !== sha256(canonical(current.priorLedger))) {
    throw new Error('prior release ledger was not restored at recovery completion')
  }
  await operationLock.release()
  return { recovered: true, action: current.action, ledgerGeneration: current.priorLedger.generation }
}

async function activateRelease(options, action) {
  for (const [name, value] of [['--release-dir', options.releaseDir], ['--base-env', options.baseEnv],
    ['--health-url', options.healthUrl], ['--ready-url', options.readyUrl],
    ['--rollback-release-dir', options.rollbackReleaseDir]]) required(name, value)
  const baseEnvPatchRequest = requestedTargetBaseEnvPatch(options, action)
  const qualificationRollbackReceipt = requestedQualificationRollbackReceipt(options, action)
  const targetDir = resolve(options.releaseDir)
  const rollbackDir = resolve(options.rollbackReleaseDir)
  if (targetDir === rollbackDir || dirname(targetDir) !== dirname(rollbackDir)) {
    throw new Error('target and rollback release directories must be distinct siblings')
  }
  const operationLock = await acquireReleaseOperationLock(targetDir, rollbackDir)
  let transactionStarted = false
  try {
    const target = await loadReleaseBundle(options, { verifyTool: false })
    const rollbackOptions = { ...options, releaseDir: rollbackDir }
    const rollback = await loadReleaseBundle(rollbackOptions, { verifyTool: false })
    await verifyRetainedReleaseActivator(process.argv[1], rollback.paths, rollback.source)
    await loadRetainedRollbackTool(rollback.paths.releaseTool, rollback.source.configFiles['scripts/bonfire-release.mjs'])
    verifyReleaseEnvironmentFile(await readFile(target.paths.runtimeEnv, 'utf8'), target.receipt)
    verifyReleaseEnvironmentFile(await readFile(rollback.paths.runtimeEnv, 'utf8'), rollback.receipt)
    await verifyReleaseImages(target.receipt)
    await verifyReleaseImages(rollback.receipt)

    // No mutation is allowed until the caller proves that the complete
    // retained rollback bundle is exactly what is serving now.
    const baselineProjectContainers = await inspectProjectContainers()
    const baselineProjectSha256 = projectContainerSnapshotSha256(baselineProjectContainers)
    const baselineProjectResources = await inspectProjectResources()
    const baselineProjectResourceSha256 = projectResourceSnapshotSha256(baselineProjectResources)
    const rollbackVerified = await verifyRunning(rollbackOptions, false, { verifyTool: false, verifyLedger: false })
    const currentQualification = privateRealtimeVoiceQualificationEnvState(await readFile(resolve(options.baseEnv)))
    assertPrivateRealtimeVoiceQualificationHostRuntimeMatch(currentQualification.state, rollbackVerified.qualificationState)
    const baselineAfterVerification = await inspectProjectContainers()
    if (projectContainerSnapshotSha256(baselineAfterVerification) !== baselineProjectSha256) {
      throw new Error('currently serving Compose project changed during baseline verification')
    }
    if (projectResourceSnapshotSha256(await inspectProjectResources()) !== baselineProjectResourceSha256) {
      throw new Error('currently serving Compose project resources changed during baseline verification')
    }
    const ledger = await readActiveReleaseLedger(targetDir)
    validateReleaseTransition(action, ledger, targetDir, target.receipt, rollbackDir, rollback.receipt)
    await assertActiveReleaseLedgerUnchanged(targetDir, ledger)
    const nextLedger = nextActiveReleaseLedger(targetDir, target.receipt, rollbackDir, rollback.receipt, ledger)
    // Planning reads and hashes only. The first base-env write occurs later in
    // the durable phase machine, still under this lock and only after the exact
    // retained baseline above has been verified.
    let baseEnvPatch = null
    let baseEnvPatchMode = null
    if (baseEnvPatchRequest) {
      baseEnvPatch = await prepareTargetBaseEnvPatch({
        baseEnv: options.baseEnv,
        request: baseEnvPatchRequest,
        operationLock,
        targetReleaseCommit: target.receipt.source.releaseCommit,
        rollbackReleaseCommit: rollback.receipt.source.releaseCommit,
        targetLedgerGeneration: nextLedger.generation
      })
      baseEnvPatchMode = 'activate'
    } else if (qualificationRollbackReceipt) {
      baseEnvPatch = await prepareQualificationRollbackBaseEnvPatch({
        baseEnv: options.baseEnv,
        receiptPath: qualificationRollbackReceipt,
        operationLock,
        targetReleaseCommit: target.receipt.source.releaseCommit,
        rollbackReleaseCommit: rollback.receipt.source.releaseCommit,
        activeLedgerGeneration: ledger.generation
      })
      baseEnvPatchMode = 'rollback'
    }
    const qualificationBeforeMutation = privateRealtimeVoiceQualificationEnvState(await readFile(resolve(options.baseEnv)))
    if (qualificationBeforeMutation.state !== currentQualification.state) {
      throw new Error('base env qualification state changed during release planning')
    }
    assertQualificationTransitionBound(action, qualificationBeforeMutation.state, baseEnvPatchMode)
    if (baseEnvPatch) {
      const qualifiedRollbackVerified = await verifyRunning(rollbackOptions, false, {
        verifyTool: false,
        verifyLedger: false,
        expectedRenderedComposeSha256: rollbackVerified.renderedComposeSha256,
        baseEnvPatch,
        baseEnvPatchState: baseEnvPatchMode === 'rollback' ? 'target' : 'prior'
      })
      if (qualifiedRollbackVerified.renderedComposeSha256 !== rollbackVerified.renderedComposeSha256) {
        throw new Error('retained rollback preflight changed while proving the prior qualification environment')
      }
      if (projectContainerSnapshotSha256(await inspectProjectContainers()) !== baselineProjectSha256 ||
          projectResourceSnapshotSha256(await inspectProjectResources()) !== baselineProjectResourceSha256) {
        throw new Error('currently serving Compose project changed while proving the prior qualification environment')
      }
      await assertActiveReleaseLedgerUnchanged(targetDir, ledger)
    }
    // A qualified transition must render/preflight only after its phase-specific
    // env bytes are installed. Legacy/no-patch releases retain the earlier
    // pre-mutation preflight behavior.
    const targetPreflight = baseEnvPatch ? null : await preflightComposeBundle(options, target)
    transactionStarted = true
    const now = new Date().toISOString()
    const journal = await writeReleaseTransactionJournal(operationLock, {
      schema: releaseTransactionSchema, token: operationLock.token, action, phase: 'prepared',
      targetBundleSha256: target.receipt.bundleSha256, rollbackBundleSha256: rollback.receipt.bundleSha256,
      targetRenderedComposeSha256: targetPreflight?.sha256 || null, rollbackRenderedComposeSha256: rollbackVerified.renderedComposeSha256,
      priorLedger: ledger, nextLedger, baseEnvPatch, baseEnvPatchMode, recoveryFromPhase: null,
      baselineProjectContainers, baselineProjectResources, createdAt: now, updatedAt: now
    })
    try {
      const completed = await resumeDurableReleaseTransaction(options, operationLock, journal)
      process.stdout.write(`${JSON.stringify({ [action]: true, ...completed })}\n`)
    } catch (error) {
      try {
        await recoverDurableReleaseTransaction(options, operationLock, await readReleaseTransactionJournal(operationLock))
      } catch (recoveryError) {
        throw new AggregateError([error, recoveryError], 'release transaction failed and durable recovery remains resumable under the retained lock')
      }
      throw new Error(`release transaction failed; durable recovery restored the prior release: ${error?.message || error}`, { cause: error })
    }
  } finally {
    if (!transactionStarted) await operationLock.release()
  }
}

async function resumeRelease(options, recover = false) {
  for (const [name, value] of [['--release-dir', options.releaseDir], ['--base-env', options.baseEnv],
    ['--health-url', options.healthUrl], ['--ready-url', options.readyUrl]]) required(name, value)
  const operationLock = await loadReleaseOperationLockForResume(options.releaseDir)
  const retainedOptions = { ...options, releaseDir: operationLock.rollbackDir }
  const retained = await loadReleaseBundle(retainedOptions, { verifyTool: false })
  // Identity is proven before cleaning even a private interrupted temp file;
  // an arbitrary candidate tool gets zero resume/recovery side effects.
  await verifyRetainedReleaseActivator(process.argv[1], retained.paths, retained.source)
  await loadRetainedRollbackTool(retained.paths.releaseTool, retained.source.configFiles['scripts/bonfire-release.mjs'])
  await cleanReleaseTransactionTemps(operationLock)
  let journal
  try {
    journal = await readReleaseTransactionJournal(operationLock)
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error
    // The durable journal is created before ingress or application mutation.
    // A crash in that pre-journal window is recoverable by proving the exact
    // retained bundle still serves, then atomically retiring the lock.
    await verifyRunning(retainedOptions, false, { verifyTool: false, verifyLedger: true })
    await operationLock.release()
    process.stdout.write(`${JSON.stringify({ recovered: true, phase: 'pre_journal', noMutation: true })}\n`)
    return
  }
  const result = recover
    ? await recoverDurableReleaseTransaction(options, operationLock, journal)
    : await resumeDurableReleaseTransaction(options, operationLock, journal)
  process.stdout.write(`${JSON.stringify(result)}\n`)
}

function commonBuildArgs(source, buildInputs, buildInputManifestSha256) {
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

function archiveBinding(source) {
  return {
    gitTreeDigest: source.gitTreeDigest,
    reviewedInventorySha256: source.reviewedInventorySha256,
    transitiveInputsSha256: source.transitiveInputsSha256,
    buildConfigSha256: source.buildConfigSha256,
    scopePolicySha256: source.scopePolicySha256,
    inputCount: source.inputCount
  }
}

function validBuiltImage(image) {
  return image && typeof image.imageReference === 'string' && image.imageReference.length > 0 &&
    localImageIdPattern.test(String(image.imageId || '')) && image.imageDigest === normalizeDigest(image.imageId) &&
    shaPattern.test(String(image.binarySha256 || '')) && image.platform === 'linux/amd64' &&
    Array.isArray(image.resolvedPackages?.build) && image.resolvedPackages.build.length > 0 &&
    Array.isArray(image.resolvedPackages?.runtime) && image.resolvedPackages.runtime.length > 0
}

function environmentFromInspect(inspect) {
  return Object.fromEntries((inspect?.Config?.Env || []).map(entry => {
    const index = entry.indexOf('=')
    return index < 0 ? [entry, ''] : [entry.slice(0, index), entry.slice(index + 1)]
  }))
}

function configInventoryDigest(configFiles) {
  return sha256(canonical({ schema: 'bonfire.release-config.v2', files: configFiles }))
}

function parseGitTree(raw) {
  const entries = []
  for (const record of Buffer.from(raw).toString('utf8').split('\0').filter(Boolean)) {
    const tab = record.indexOf('\t')
    const match = /^(\d{6}) (\w+) ([0-9a-f]{40,64})$/.exec(record.slice(0, tab))
    if (tab < 0 || !match) throw new Error('git tree inventory is malformed')
    entries.push({ path: record.slice(tab + 1), mode: match[1], type: match[2], object: match[3] })
  }
  return entries
}

function validateRepoPath(path) {
  const value = String(path || '')
  if (!value || value.startsWith('/') || value.endsWith('/') || value.includes('\\') || value.includes('\0') ||
      value.split('/').some(part => part === '' || part === '.' || part === '..') || value.startsWith('-')) {
    throw new Error(`unsafe release repository path ${value}`)
  }
}

function validateRepoPrefix(prefix) {
  const value = String(prefix || '')
  if (!value.endsWith('/')) throw new Error(`unsafe release repository prefix ${value}`)
  validateRepoPath(value.slice(0, -1))
}

function parseArgs(args) {
  const options = { command: args[0] || '' }
  for (let index = 1; index < args.length; index++) {
    const name = args[index]
    const value = args[++index]
    if (!value || !name.startsWith('--')) throw new Error(`invalid argument ${name}`)
    const key = name.slice(2).replace(/-([a-z])/g, (_, char) => char.toUpperCase())
    if (Object.hasOwn(options, key)) throw new Error(`duplicate argument ${name}`)
    options[key] = value
  }
  return options
}

function required(name, value) { if (!String(value || '').trim()) throw new Error(`${name} is required`) }
function normalizeDigest(value) { return String(value || '').trim().toLowerCase().replace(/^sha256:/, '') }
function normalizeLabel(value) { return normalizeDigest(value) }
function normalizeLines(value) { return String(value).trim().split('\n').filter(Boolean) }
function sha256(value) { return createHash('sha256').update(value).digest('hex') }
function git(args, options = {}) { return execFileAsync('git', args, { cwd: process.cwd(), timeout: 60_000, ...options }) }
function canonical(value) { return Buffer.from(`${JSON.stringify(value)}\n`) }
function jsonLine(value) { return canonical(value) }
function parseJSON(value, label) { try { return JSON.parse(value) } catch { throw new Error(`${label} is not valid JSON`) } }
function stableJSONValue(value) {
  if (Array.isArray(value)) return value.map(stableJSONValue)
  if (!value || typeof value !== 'object') return value
  return Object.fromEntries(Object.keys(value).sort().map(key => [key, stableJSONValue(value[key])]))
}

async function writeExclusive(path, body, mode) {
  await mkdir(dirname(path), { recursive: true })
  const file = await open(path, 'wx', mode)
  try { await file.writeFile(body); await file.sync() } finally { await file.close() }
}

async function writeAtomicReplace(path, body, mode) {
  const directory = dirname(path)
  await mkdir(directory, { recursive: true, mode: 0o700 })
  const temporary = join(directory, `.${String(path).split('/').at(-1)}.${randomUUID()}.tmp`)
  let renamed = false
  try {
    const file = await open(temporary, 'wx', mode)
    try {
      await file.writeFile(body)
      await file.sync()
    } finally {
      await file.close()
    }
    await rename(temporary, path)
    renamed = true
    await syncDirectory(directory)
  } finally {
    if (!renamed) await rm(temporary, { force: true }).catch(() => {})
  }
}

async function removeBoundAtomicTemporary(temporary, ownerUid) {
  try {
    const info = await lstat(temporary)
    validatePrivateReleasePathInfo(info, 'base env', ownerUid)
    if (info.nlink !== 1) throw new Error('bound base env temporary file has an unexpected link count')
    await unlink(temporary)
    await syncDirectory(dirname(temporary))
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error
  }
}

async function writeAtomicReplaceBound(path, body, mode, temporary, ownerUid) {
  const directory = dirname(path)
  if (dirname(temporary) !== directory || temporary === path) {
    throw new Error('bound atomic replacement path is invalid')
  }
  // A SIGKILL after file fsync but before rename cannot run `finally`. The
  // transaction-token-derived path makes that exact private copy discoverable
  // on resume/recover without globbing or touching unrelated secret files.
  await removeBoundAtomicTemporary(temporary, ownerUid)
  let renamed = false
  try {
    const file = await open(temporary, 'wx', mode)
    try {
      await file.writeFile(body)
      await file.sync()
    } finally {
      await file.close()
    }
    await rename(temporary, path)
    renamed = true
    await syncDirectory(directory)
  } finally {
    if (!renamed) await removeBoundAtomicTemporary(temporary, ownerUid)
  }
}

async function syncDirectory(directory) {
  const parent = await open(directory, 'r')
  try { await parent.sync() } finally { await parent.close() }
}

async function extractArchive(archive, sourceRoot) {
  await spawnWithInput('tar', ['-xf', '-', '-C', sourceRoot], archive)
  await chmod(sourceRoot, 0o500)
}

async function spawnWithInput(command, args, input) {
  const child = spawn(command, args, { stdio: ['pipe', 'pipe', 'pipe'] })
  const stderr = []
  child.stderr.on('data', chunk => stderr.push(chunk))
  child.stdin.end(input)
  const code = await new Promise((resolveCode, reject) => { child.once('error', reject); child.once('close', resolveCode) })
  if (code !== 0) throw new Error(`${command} failed (${code}): ${Buffer.concat(stderr).toString('utf8').slice(0, 1000)}`)
}

async function main() {
  const options = parseArgs(process.argv.slice(2))
  if (options.command !== 'activate' && targetBaseEnvPatchOptionNames.some(name => String(options[name] || '').trim())) {
    throw new Error('target base-env patch arguments are permitted only for activate')
  }
  if (options.command !== 'rollback' && String(options.qualificationRollbackReceipt || '').trim()) {
    throw new Error('--qualification-rollback-receipt is permitted only for rollback')
  }
  if (options.command === 'scope') await scope(options)
  else if (options.command === 'prepare') await prepare(options)
  else if (options.command === 'build') await build(options)
  else if (options.command === 'verify') await verifyRunning(options)
  else if (options.command === 'activate') await activateRelease(options, 'activated')
  else if (options.command === 'rollback') await activateRelease(options, 'rolledBack')
  else if (options.command === 'resume') await resumeRelease(options, false)
  else if (options.command === 'recover') await resumeRelease(options, true)
  else throw new Error('command must be scope, prepare, build, verify, activate, rollback, resume, or recover')
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    process.stderr.write(`bonfire-release: ${error?.message || error}\n`)
    process.exitCode = 1
  })
}
