#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { execFile as execFileCallback } from 'node:child_process'
import {
  chmod, lstat, open, readFile, readdir, rename, rm, unlink
} from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { promisify } from 'node:util'

import {
  acquireReleaseOperationLock,
  composeIngressArgs,
  composePrivateActivationArgs,
  privateRealtimeVoiceQualificationEnvState,
  releaseComposeEnvironment,
  validateActiveReleaseLedger
} from './bonfire-release.mjs'

const execFileAsync = promisify(execFileCallback)
const bridgeSchema = 'bonfire.private-realtime-dequalification-bridge.v1'
const bridgeReceiptSchema = 'bonfire.private-realtime-dequalification-receipt.v1'
const releaseLockSchema = 'bonfire.release-operation-lock.v1'
const baseEnvPath = '/opt/meetingassist/deploy/digitalocean/.env'
const backupRoot = '/opt/meetingassist-backups'
const releasesRoot = '/opt/meetingassist-releases'
const releaseLockPath = join(releasesRoot, '.bonfire-release-operation.lock')
const qualificationKey = 'PRIVATE_REALTIME_VOICE_QUALIFIED'
const shaPattern = /^[0-9a-f]{64}$/
const commitPattern = /^[0-9a-f]{40}$/

function sha256(value) { return createHash('sha256').update(value).digest('hex') }
function jsonLine(value) { return Buffer.from(`${JSON.stringify(value)}\n`) }
function parseJSON(value, label) {
  try { return JSON.parse(value) } catch { throw new Error(`${label} is not valid JSON`) }
}

export function privateRealtimeVoiceDequalificationEnvPatch(body) {
  const current = privateRealtimeVoiceQualificationEnvState(body)
  if (current.state !== 'true' || !current.match) {
    throw new Error(`dequalification requires exactly one canonical ${qualificationKey}=true assignment`)
  }
  const replacement = `${qualificationKey}=false${current.match[2]}`
  const afterText = `${current.text.slice(0, current.match.index)}${replacement}${current.text.slice(current.match.index + current.match[0].length)}`
  const after = Buffer.from(afterText)
  if (after.equals(current.body)) throw new Error('dequalification patch produced no change')
  return {
    before: current.body,
    after,
    beforeSha256: sha256(current.body),
    afterSha256: sha256(after)
  }
}

export function assertDequalificationIdleReadiness(payload) {
  if (payload?.ok !== true) throw new Error('readiness is not healthy enough for the dequalification bridge')
  const rooms = payload?.capabilities?.rooms
  if (!Array.isArray(rooms)) throw new Error('readiness lacks the room inventory required by the dequalification bridge')
  const busy = rooms.filter(room => Number(room?.participants || 0) !== 0 || room?.media?.active === true ||
    room?.media?.actor === true || room?.media?.mixer === true || String(room?.sittingId || '').trim())
  if (busy.length) throw new Error(`dequalification is blocked while ${busy.length} room(s) are active`)
  if (payload?.checks?.realtime?.connected === true) throw new Error('dequalification is blocked while shared Realtime is connected')
  return true
}

export function validateDequalificationJournal(value) {
  const expected = [
    'activeReleaseCommit', 'afterSha256', 'backupPath', 'baseEnvPath', 'beforeSha256', 'createdAt',
    'generation', 'healthUrl', 'phase', 'previousReleaseCommit', 'readyUrl', 'receiptPath', 'schema',
    'token', 'updatedAt'
  ].sort()
  if (!value || Object.keys(value).sort().join('\n') !== expected.join('\n') || value.schema !== bridgeSchema ||
      !/^[0-9a-f-]{36}$/.test(String(value.token || '')) || !commitPattern.test(String(value.activeReleaseCommit || '')) ||
      !commitPattern.test(String(value.previousReleaseCommit || '')) || !shaPattern.test(String(value.beforeSha256 || '')) ||
      !shaPattern.test(String(value.afterSha256 || '')) || !Number.isSafeInteger(value.generation) || value.generation < 1 ||
      value.baseEnvPath !== baseEnvPath || dirname(value.backupPath) !== backupRoot || dirname(value.receiptPath) !== backupRoot ||
      !['prepared', 'ingress_stopped', 'env_patched', 'runtime_unqualified', 'ingress_opened'].includes(value.phase) ||
      Number.isNaN(Date.parse(String(value.createdAt || ''))) || Number.isNaN(Date.parse(String(value.updatedAt || ''))) ||
      !/^https:\/\//.test(String(value.healthUrl || '')) || !/^https:\/\//.test(String(value.readyUrl || ''))) {
    throw new Error('private Realtime dequalification journal is invalid')
  }
  return value
}

function parseArgs(argv) {
  const options = { command: argv[0] || '' }
  for (let index = 1; index < argv.length; index += 2) {
    const name = argv[index]
    const value = argv[index + 1]
    if (!name?.startsWith('--') || value === undefined) throw new Error('bridge arguments must be --name value pairs')
    const key = name.slice(2).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase())
    if (Object.hasOwn(options, key)) throw new Error(`duplicate bridge argument ${name}`)
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

async function writeExclusive(path, body, mode = 0o600) {
  const handle = await open(path, 'wx', mode)
  try { await handle.writeFile(body); await handle.sync() } finally { await handle.close() }
  await syncDirectory(dirname(path))
}

async function atomicWriteBound(path, body, token) {
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

async function assertRootPrivatePath(path, kind, directory = false) {
  const info = await lstat(path)
  if (info.uid !== 0 || info.isSymbolicLink() || (directory ? !info.isDirectory() : !info.isFile()) ||
      (info.mode & 0o777) !== (directory ? 0o700 : 0o600)) {
    throw new Error(`${kind} must be root-owned and ${directory ? 'mode 0700' : 'mode 0600'}`)
  }
  return info
}

async function readLedger(activeDir, previousDir) {
  const path = join(releasesRoot, 'active-release.json')
  const ledger = validateActiveReleaseLedger(parseJSON(await readFile(path), 'active release ledger'))
  if (resolve(ledger.active.releaseDir) !== activeDir || resolve(ledger.previous.releaseDir) !== previousDir) {
    throw new Error('dequalification release directories do not match the active ledger')
  }
  return ledger
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
  const { stdout } = await execFileAsync('node', [
    paths.tool, 'verify', '--release-dir', activeDir, '--base-env', baseEnvPath,
    '--health-url', plan.healthUrl, '--ready-url', plan.readyUrl
  ], { maxBuffer: 32 << 20 })
  const result = parseJSON(String(stdout).trim().split('\n').at(-1), 'retained exact-release verification')
  if (result?.verified !== true || result?.releaseCommit !== plan.activeReleaseCommit) {
    throw new Error('retained exact-release verification did not prove the active commit')
  }
  return result
}

async function appContainer() {
  const { stdout } = await execFileAsync('docker', [
    'container', 'ls', '--all', '--no-trunc', '--filter', 'label=com.docker.compose.project=digitalocean',
    '--filter', 'label=com.docker.compose.service=meetingassist', '--format', '{{.ID}}'
  ])
  const ids = String(stdout).trim().split('\n').filter(Boolean)
  if (ids.length !== 1) throw new Error('dequalification requires exactly one meetingassist container')
  const { stdout: raw } = await execFileAsync('docker', ['container', 'inspect', ids[0]], { maxBuffer: 16 << 20 })
  return parseJSON(raw, 'meetingassist container inspect')[0]
}

async function assertRuntime(plan, expectedState, expectedImage) {
  const inspected = await appContainer()
  if (String(inspected?.Image || '').toLowerCase() !== String(expectedImage || '').toLowerCase() ||
      inspected?.State?.Status !== 'running') throw new Error('meetingassist runtime identity changed during dequalification')
  const environment = Object.fromEntries((inspected?.Config?.Env || []).map(line => {
    const index = line.indexOf('=')
    return [line.slice(0, index), line.slice(index + 1)]
  }))
  const state = privateRealtimeVoiceQualificationEnvState(Buffer.from(`${qualificationKey}=${environment[qualificationKey]}\n`)).state
  if (state !== expectedState) throw new Error(`meetingassist runtime is ${state}, expected ${expectedState}`)
  for (const endpoint of ['/healthz', '/readyz']) {
    const { stdout } = await execFileAsync('docker', ['exec', inspected.Id, 'curl', '-fsS', `http://127.0.0.1:3000${endpoint}`])
    const payload = parseJSON(stdout, `private ${endpoint}`)
    if (payload?.ok !== true || payload?.release?.releaseCommit !== plan.activeReleaseCommit) {
      throw new Error(`private ${endpoint} did not prove the active release`)
    }
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

async function updateJournal(plan, phase) {
  const next = validateDequalificationJournal({ ...plan, phase, updatedAt: new Date().toISOString() })
  await atomicWriteBound(join(backupRoot, `private-realtime-dequalification-${plan.token}.journal.json`), jsonLine(next), plan.token)
  return next
}

async function writeReceipt(plan, state) {
  const receipt = {
    schema: bridgeReceiptSchema,
    state,
    token: plan.token,
    activeReleaseCommit: plan.activeReleaseCommit,
    previousReleaseCommit: plan.previousReleaseCommit,
    generation: plan.generation,
    baseEnvPath: plan.baseEnvPath,
    backupPath: plan.backupPath,
    beforeSha256: plan.beforeSha256,
    afterSha256: plan.afterSha256,
    completedAt: new Date().toISOString()
  }
  await atomicWriteBound(plan.receiptPath, jsonLine(receipt), plan.token)
  await assertRootPrivatePath(plan.receiptPath, 'dequalification receipt')
  return receipt
}

async function recoverPlan(plan, ledger, activeDir) {
  await stopIngress(activeDir)
  const current = await readFile(baseEnvPath)
  const digest = sha256(current)
  if (digest === plan.afterSha256) {
    const backup = await readFile(plan.backupPath)
    if (sha256(backup) !== plan.beforeSha256) throw new Error('dequalification backup differs from the durable journal')
    await atomicWriteBound(baseEnvPath, backup, plan.token)
  } else if (digest !== plan.beforeSha256) {
    throw new Error('base env is neither the dequalification prior nor target state')
  }
  await startPrivate(activeDir)
  await assertRuntime(plan, 'true', ledger.active.meetingassistImageId)
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
  if (dirname(activeDir) !== releasesRoot || dirname(previousDir) !== releasesRoot || activeDir === previousDir) {
    throw new Error('bridge release directories must be distinct exact siblings under /opt/meetingassist-releases')
  }
  await assertRootPrivatePath(baseEnvPath, 'base env')
  await assertRootPrivatePath(backupRoot, 'backup directory', true)
  const ledger = await readLedger(activeDir, previousDir)
  const before = await readFile(baseEnvPath)
  const patch = privateRealtimeVoiceDequalificationEnvPatch(before)
  const readyResponse = await fetch(readyUrl, { headers: { accept: 'application/json' }, signal: AbortSignal.timeout(20_000) })
  if (!readyResponse.ok) throw new Error(`readiness returned ${readyResponse.status}`)
  assertDequalificationIdleReadiness(await readyResponse.json())

  const lock = await acquireReleaseOperationLock(activeDir, previousDir)
  let plan = null
  let candidatePlan = null
  try {
    await exactVerify({ healthUrl, readyUrl, activeReleaseCommit: ledger.active.releaseCommit }, activeDir)
    await assertRuntime({ activeReleaseCommit: ledger.active.releaseCommit }, 'true', ledger.active.meetingassistImageId)
    const token = lock.token
    candidatePlan = validateDequalificationJournal({
      schema: bridgeSchema,
      token,
      activeReleaseCommit: ledger.active.releaseCommit,
      previousReleaseCommit: ledger.previous.releaseCommit,
      generation: ledger.generation,
      baseEnvPath,
      backupPath: join(backupRoot, `private-realtime-dequalification-${token}.base-env`),
      receiptPath: join(backupRoot, `private-realtime-dequalification-${token}.receipt.json`),
      beforeSha256: patch.beforeSha256,
      afterSha256: patch.afterSha256,
      healthUrl,
      readyUrl,
      phase: 'prepared',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString()
    })
    const journalPath = join(backupRoot, `private-realtime-dequalification-${token}.journal.json`)
    await writeExclusive(candidatePlan.backupPath, patch.before)
    await assertRootPrivatePath(candidatePlan.backupPath, 'dequalification backup')
    await writeExclusive(journalPath, jsonLine(candidatePlan))
    await assertRootPrivatePath(journalPath, 'dequalification journal')
    plan = candidatePlan

    await stopIngress(activeDir)
    plan = await updateJournal(plan, 'ingress_stopped')
    // Let existing native lease watchdogs observe ingress loss and close their
    // local microphones before the server qualification state is replaced.
    await new Promise(resolveDelay => setTimeout(resolveDelay, 35_000))
    await atomicWriteBound(baseEnvPath, patch.after, token)
    plan = await updateJournal(plan, 'env_patched')
    await startPrivate(activeDir)
    await assertRuntime(plan, 'false', ledger.active.meetingassistImageId)
    plan = await updateJournal(plan, 'runtime_unqualified')
    await startIngress(activeDir)
    plan = await updateJournal(plan, 'ingress_opened')
    await exactVerify(plan, activeDir)
    const receipt = await writeReceipt(plan, 'target_committed')
    await unlink(journalPath)
    await syncDirectory(backupRoot)
    await lock.release()
    process.stdout.write(`${JSON.stringify({ dequalified: true, releaseCommit: ledger.active.releaseCommit, generation: ledger.generation, receiptPath: plan.receiptPath, state: receipt.state })}\n`)
  } catch (error) {
    if (plan) {
      try {
        const receipt = await recoverPlan(plan, ledger, activeDir)
        await unlink(join(backupRoot, `private-realtime-dequalification-${plan.token}.journal.json`)).catch(() => {})
        await syncDirectory(backupRoot)
        await lock.release()
        throw new AggregateError([error], `dequalification failed and exact qualified production was restored; receipt ${plan.receiptPath} state ${receipt.state}`)
      } catch (recoveryError) {
        if (recoveryError instanceof AggregateError) throw recoveryError
        throw new AggregateError([error, recoveryError], 'dequalification failed and recovery remains locked; run the bridge recover command')
      }
    }
    if (candidatePlan) await rm(candidatePlan.backupPath, { force: true }).catch(() => {})
    await lock.release().catch(() => {})
    throw error
  }
}

async function loadRecovery(options) {
  required(options, 'baseEnv', baseEnvPath)
  required(options, 'backupDir', backupRoot)
  const owner = parseJSON(await readFile(join(releaseLockPath, 'owner.json')), 'release operation lock owner')
  if (owner?.schema !== releaseLockSchema || !/^[0-9a-f-]{36}$/.test(String(owner.token || ''))) {
    throw new Error('release operation lock is not a recoverable bridge lock')
  }
  const entries = (await readdir(releaseLockPath)).sort()
  if (entries.join('\n') !== 'owner.json') throw new Error('release lock contains a native release transaction and is not a bridge lock')
  const plan = validateDequalificationJournal(parseJSON(
    await readFile(join(backupRoot, `private-realtime-dequalification-${owner.token}.journal.json`)),
    'dequalification journal'
  ))
  if (resolve(owner.targetDir) !== resolve(plan.activeReleaseCommit ? join(releasesRoot, plan.activeReleaseCommit) : '') ||
      resolve(owner.rollbackDir) !== resolve(join(releasesRoot, plan.previousReleaseCommit)) || owner.token !== plan.token) {
    throw new Error('release lock and dequalification journal differ')
  }
  return { owner, plan }
}

async function recover(options) {
  const { owner, plan } = await loadRecovery(options)
  const activeDir = resolve(owner.targetDir)
  const previousDir = resolve(owner.rollbackDir)
  const ledger = await readLedger(activeDir, previousDir)
  const receipt = await recoverPlan(plan, ledger, activeDir)
  await unlink(join(backupRoot, `private-realtime-dequalification-${plan.token}.journal.json`))
  await syncDirectory(backupRoot)
  const completed = `${releaseLockPath}.completed-${plan.token}`
  await rename(releaseLockPath, completed)
  await rm(completed, { recursive: true })
  await syncDirectory(releasesRoot)
  process.stdout.write(`${JSON.stringify({ recovered: true, releaseCommit: plan.activeReleaseCommit, receiptPath: plan.receiptPath, state: receipt.state })}\n`)
}

async function main() {
  const options = parseArgs(process.argv.slice(2))
  if (options.command === 'dequalify') await execute(options)
  else if (options.command === 'recover') await recover(options)
  else throw new Error('command must be dequalify or recover')
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    process.stderr.write(`private-realtime-dequalification-bridge: ${error?.message || error}\n`)
    process.exitCode = 1
  })
}
