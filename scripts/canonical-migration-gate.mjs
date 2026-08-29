#!/usr/bin/env node

import { execFile as execFileCallback } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmod, lstat, open, readFile, rename, rm, unlink } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { promisify } from 'node:util'

import {
  acquireReleaseOperationLock,
  composeIngressArgs,
  composePrivateActivationArgs,
  releaseComposeEnvironment,
  validateActiveReleaseLedger
} from './bonfire-release.mjs'

const execFileAsync = promisify(execFileCallback)
const journalSchema = 'bonfire.canonical-migration-gate-journal.v1'
const receiptSchema = 'bonfire.canonical-migration-gate-receipt.v1'
const baseEnvPath = '/opt/meetingassist/deploy/digitalocean/.env'
const backupRoot = '/opt/meetingassist-backups'
const releasesRoot = '/opt/meetingassist-releases'
const migrationKey = 'BONFIRE_CANONICAL_MIGRATION_MAX_VERSION'
const shaPattern = /^[0-9a-f]{64}$/
const commitPattern = /^[0-9a-f]{40}$/

function sha256(value) { return createHash('sha256').update(value).digest('hex') }
function jsonLine(value) { return Buffer.from(`${JSON.stringify(value)}\n`) }
function parseJSON(value, label) {
  try { return JSON.parse(value) } catch { throw new Error(`${label} is not valid JSON`) }
}

export function canonicalMigrationGatePatch(body, fromVersion, targetVersion) {
  if (!Number.isSafeInteger(fromVersion) || !Number.isSafeInteger(targetVersion) || fromVersion < 1 || targetVersion !== fromVersion + 1) {
    throw new Error('canonical migration gate must advance exactly one positive version')
  }
  const before = Buffer.isBuffer(body) ? body : Buffer.from(body)
  const text = before.toString('utf8')
  const expression = new RegExp(`(^|\\n)${migrationKey}=([^\\r\\n]*)(\\r?\\n|$)`, 'g')
  const matches = [...text.matchAll(expression)]
  if (matches.length !== 1 || matches[0][2] !== String(fromVersion)) {
    throw new Error(`migration gate requires exactly one canonical ${migrationKey}=${fromVersion} assignment`)
  }
  const match = matches[0]
  const replacement = `${match[1]}${migrationKey}=${targetVersion}${match[3]}`
  const after = Buffer.from(`${text.slice(0, match.index)}${replacement}${text.slice(match.index + match[0].length)}`)
  return { before, after, beforeSha256: sha256(before), afterSha256: sha256(after) }
}

export function assertMigrationIdleReadiness(payload) {
  if (payload?.ok !== true || !Array.isArray(payload?.capabilities?.rooms)) {
    throw new Error('readiness lacks the healthy room inventory required by the migration gate')
  }
  const busy = payload.capabilities.rooms.filter(room => Number(room?.participants || 0) !== 0 ||
    room?.media?.active === true || room?.media?.actor === true || room?.media?.mixer === true || String(room?.sittingId || '').trim())
  if (busy.length) throw new Error(`migration gate is blocked while ${busy.length} room(s) are active`)
  if (payload?.checks?.realtime?.connected === true) throw new Error('migration gate is blocked while shared Realtime is connected')
  return true
}

export function validateMigrationProof(value, targetVersion, expectedSha256) {
  if (value?.version !== targetVersion || value?.sha256 !== expectedSha256 || value?.migrationCount < targetVersion ||
      (targetVersion === 25 && (value.sourceEpisodeTables !== 5 || value.sourceEpisodeTriggers !== 6))) {
    throw new Error('canonical migration proof is incomplete or differs from the sealed migration')
  }
  return value
}

function parseArgs(argv) {
  const options = { command: argv[0] || '' }
  for (let index = 1; index < argv.length; index += 2) {
    const name = argv[index]
    const value = argv[index + 1]
    if (!name?.startsWith('--') || value === undefined) throw new Error('migration gate arguments must be --name value pairs')
    const key = name.slice(2).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase())
    if (Object.hasOwn(options, key)) throw new Error(`duplicate migration gate argument ${name}`)
    options[key] = value
  }
  return options
}

function required(options, key, expected = '') {
  const value = String(options[key] || '')
  if (!value) throw new Error(`--${key.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`)} is required`)
  if (expected && value !== expected) throw new Error(`${key} must be exactly ${expected}`)
  return value
}

async function syncDirectory(directory) {
  const handle = await open(directory, 'r')
  try { await handle.sync() } finally { await handle.close() }
}

async function writeExclusive(path, body) {
  const handle = await open(path, 'wx', 0o600)
  try { await handle.writeFile(body); await handle.sync() } finally { await handle.close() }
  await syncDirectory(dirname(path))
}

async function atomicWrite(path, body, token) {
  const temporary = join(dirname(path), `.${path.split('/').at(-1)}.${token}.tmp`)
  await rm(temporary, { force: true })
  let renamed = false
  try {
    const handle = await open(temporary, 'wx', 0o600)
    try { await handle.writeFile(body); await handle.sync() } finally { await handle.close() }
    await rename(temporary, path)
    renamed = true
    await chmod(path, 0o600)
    await syncDirectory(dirname(path))
  } finally {
    if (!renamed) await rm(temporary, { force: true }).catch(() => {})
  }
}

async function assertRootPrivate(path, directory = false) {
  const info = await lstat(path)
  if (info.uid !== 0 || info.isSymbolicLink() || (directory ? !info.isDirectory() : !info.isFile()) ||
      (info.mode & 0o777) !== (directory ? 0o700 : 0o600)) throw new Error('migration gate path is not root-private')
}

function releasePaths(releaseDir) {
  return {
    compose: join(releaseDir, 'sealed-candidate/deploy/digitalocean/docker-compose.yml'),
    runtimeEnv: join(releaseDir, 'release.env'),
    tool: join(releaseDir, 'sealed-candidate/scripts/bonfire-release.mjs')
  }
}

async function dockerCompose(args, activeDir) {
  return execFileAsync('docker', args, {
    cwd: dirname(releasePaths(activeDir).compose),
    env: releaseComposeEnvironment(process.env, baseEnvPath),
    maxBuffer: 32 << 20
  })
}

async function exactVerify(plan, activeDir) {
  const paths = releasePaths(activeDir)
  const { stdout } = await execFileAsync('node', [paths.tool, 'verify', '--release-dir', activeDir,
    '--base-env', baseEnvPath, '--health-url', plan.healthUrl, '--ready-url', plan.readyUrl], { maxBuffer: 32 << 20 })
  const result = parseJSON(String(stdout).trim().split('\n').at(-1), 'exact-release verification')
  if (result?.verified !== true || result?.releaseCommit !== plan.activeReleaseCommit) {
    throw new Error('exact-release verification did not prove the active commit')
  }
}

async function stopIngress(activeDir) {
  const paths = releasePaths(activeDir)
  await dockerCompose(composeIngressArgs(baseEnvPath, paths.runtimeEnv, paths.compose, 'stop'), activeDir)
}

async function startPrivate(activeDir) {
  const paths = releasePaths(activeDir)
  await dockerCompose(composePrivateActivationArgs(baseEnvPath, paths.runtimeEnv, paths.compose), activeDir)
}

async function startIngress(activeDir) {
  const paths = releasePaths(activeDir)
  await dockerCompose(composeIngressArgs(baseEnvPath, paths.runtimeEnv, paths.compose, 'start'), activeDir)
}

async function readLedger(activeDir, previousDir) {
  const ledger = validateActiveReleaseLedger(parseJSON(await readFile(join(releasesRoot, 'active-release.json')), 'active release ledger'))
  if (resolve(ledger.active.releaseDir) !== activeDir || resolve(ledger.previous.releaseDir) !== previousDir) {
    throw new Error('migration gate release directories differ from the active ledger')
  }
  return ledger
}

async function proveMigration(activeDir, targetVersion) {
  const archive = join(activeDir, 'source.tar')
  const { stdout: inventory } = await execFileAsync('tar', ['-tf', archive], { maxBuffer: 16 << 20 })
  const prefix = `migrations/${String(targetVersion).padStart(4, '0')}_`
  const names = String(inventory).trim().split('\n').filter(name => name.startsWith(prefix) && name.endsWith('.sql'))
  if (names.length !== 1) throw new Error('reviewed source migration inventory is ambiguous')
  const { stdout: migrationBody } = await execFileAsync('tar', ['-xOf', archive, names[0]], { encoding: 'buffer', maxBuffer: 16 << 20 })
  const expectedSha256 = sha256(migrationBody)
  const { stdout } = await execFileAsync('docker', ['exec', 'digitalocean-canonical-postgres-1', 'psql', '-U', 'bonfire', '-d', 'bonfire', '-Atqc', `
    SELECT json_build_object(
      'version', ${targetVersion},
      'sha256', COALESCE((SELECT encode(sha256,'hex') FROM schema_migrations WHERE version=${targetVersion}),''),
      'migrationCount', (SELECT count(*) FROM schema_migrations),
      'sourceEpisodeTables', (SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name LIKE 'stride_source_episode_%'),
      'sourceEpisodeTriggers', (SELECT count(*) FROM information_schema.triggers WHERE trigger_schema='public' AND trigger_name LIKE 'stride_source_episode_%')
    );
  `], { maxBuffer: 1 << 20 })
  return validateMigrationProof(parseJSON(stdout, 'canonical migration proof'), targetVersion, expectedSha256)
}

async function updateJournal(plan, phase) {
  const next = { ...plan, phase, updatedAt: new Date().toISOString() }
  await atomicWrite(plan.journalPath, jsonLine(next), plan.token)
  return next
}

async function writeReceipt(plan, state, proof = null) {
  const receipt = {
    schema: receiptSchema, state, token: plan.token, activeReleaseCommit: plan.activeReleaseCommit,
    previousReleaseCommit: plan.previousReleaseCommit, generation: plan.generation,
    fromVersion: plan.fromVersion, targetVersion: plan.targetVersion, beforeSha256: plan.beforeSha256,
    afterSha256: plan.afterSha256, backupPath: plan.backupPath, migrationProof: proof,
    completedAt: new Date().toISOString()
  }
  await atomicWrite(plan.receiptPath, jsonLine(receipt), plan.token)
  await assertRootPrivate(plan.receiptPath)
  return receipt
}

async function recover(plan, activeDir) {
  await stopIngress(activeDir).catch(() => {})
  const current = await readFile(baseEnvPath)
  if (sha256(current) === plan.afterSha256) {
    const backup = await readFile(plan.backupPath)
    if (sha256(backup) !== plan.beforeSha256) throw new Error('migration gate backup differs from the journal')
    await atomicWrite(baseEnvPath, backup, plan.token)
  } else if (sha256(current) !== plan.beforeSha256) {
    throw new Error('base env is neither the migration prior nor target state')
  }
  await startPrivate(activeDir)
  await startIngress(activeDir)
  await exactVerify(plan, activeDir)
  return writeReceipt(plan, 'prior_recovered')
}

async function execute(options) {
  const activeDir = resolve(required(options, 'activeReleaseDir'))
  const previousDir = resolve(required(options, 'previousReleaseDir'))
  required(options, 'baseEnv', baseEnvPath)
  required(options, 'backupDir', backupRoot)
  const healthUrl = required(options, 'healthUrl')
  const readyUrl = required(options, 'readyUrl')
  const fromVersion = Number(required(options, 'fromVersion'))
  const targetVersion = Number(required(options, 'targetVersion'))
  if (dirname(activeDir) !== releasesRoot || dirname(previousDir) !== releasesRoot || activeDir === previousDir) {
    throw new Error('migration gate release directories must be distinct exact siblings')
  }
  await assertRootPrivate(baseEnvPath)
  await assertRootPrivate(backupRoot, true)
  const ledger = await readLedger(activeDir, previousDir)
  const patch = canonicalMigrationGatePatch(await readFile(baseEnvPath), fromVersion, targetVersion)
  const response = await fetch(readyUrl, { headers: { accept: 'application/json' }, signal: AbortSignal.timeout(20_000) })
  if (!response.ok) throw new Error(`readiness returned ${response.status}`)
  assertMigrationIdleReadiness(await response.json())
  const lock = await acquireReleaseOperationLock(activeDir, previousDir)
  let plan = null
  try {
    await exactVerify({ healthUrl, readyUrl, activeReleaseCommit: ledger.active.releaseCommit }, activeDir)
    const token = lock.token
    plan = {
      schema: journalSchema, token, activeReleaseCommit: ledger.active.releaseCommit,
      previousReleaseCommit: ledger.previous.releaseCommit, generation: ledger.generation,
      fromVersion, targetVersion, beforeSha256: patch.beforeSha256, afterSha256: patch.afterSha256,
      backupPath: join(backupRoot, `canonical-migration-${fromVersion}-to-${targetVersion}-${token}.base-env`),
      receiptPath: join(backupRoot, `canonical-migration-${fromVersion}-to-${targetVersion}-${token}.receipt.json`),
      journalPath: join(backupRoot, `canonical-migration-${fromVersion}-to-${targetVersion}-${token}.journal.json`),
      healthUrl, readyUrl, phase: 'prepared', createdAt: new Date().toISOString(), updatedAt: new Date().toISOString()
    }
    await writeExclusive(plan.backupPath, patch.before)
    await writeExclusive(plan.journalPath, jsonLine(plan))
    await stopIngress(activeDir)
    plan = await updateJournal(plan, 'ingress_stopped')
    await atomicWrite(baseEnvPath, patch.after, token)
    plan = await updateJournal(plan, 'env_patched')
    await startPrivate(activeDir)
    plan = await updateJournal(plan, 'runtime_migrated')
    const proof = await proveMigration(activeDir, targetVersion)
    plan = await updateJournal(plan, 'migration_proved')
    await startIngress(activeDir)
    await exactVerify(plan, activeDir)
    const receipt = await writeReceipt(plan, 'target_committed', proof)
    await unlink(plan.journalPath)
    await syncDirectory(backupRoot)
    await lock.release()
    process.stdout.write(`${JSON.stringify({ advanced: true, releaseCommit: ledger.active.releaseCommit, generation: ledger.generation,
      targetVersion, receiptPath: plan.receiptPath, state: receipt.state })}\n`)
  } catch (error) {
    if (plan) {
      try {
        const receipt = await recover(plan, activeDir)
        await unlink(plan.journalPath).catch(() => {})
        await syncDirectory(backupRoot)
        await lock.release()
        throw new AggregateError([error], `migration gate failed and production was restored; receipt ${plan.receiptPath} state ${receipt.state}`)
      } catch (recoveryError) {
        if (recoveryError instanceof AggregateError) throw recoveryError
        throw new AggregateError([error, recoveryError], 'migration gate failed and recovery remains locked')
      }
    }
    await lock.release().catch(() => {})
    throw error
  }
}

async function main() {
  const options = parseArgs(process.argv.slice(2))
  if (options.command !== 'advance') throw new Error('command must be advance')
  await execute(options)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    process.stderr.write(`canonical-migration-gate: ${error?.message || error}\n`)
    process.exitCode = 1
  })
}
