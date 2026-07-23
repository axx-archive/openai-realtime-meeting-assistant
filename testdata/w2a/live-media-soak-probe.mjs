#!/usr/bin/env node

// Same-commit W2A collector. It runs two independent 3x3 browser/SFU probes,
// samples the live container, requires explicit room-A HOL, AI-provider-fault,
// and scope-canary hooks, sanitizes the resulting artifacts, and signs only
// raw evidence with the independently-custodied collector key. The Go gate
// recomputes every aggregate and applies the release-operator signature.

import { execFile, spawn } from 'node:child_process'
import { createHash, createHmac, createPrivateKey, createPublicKey, randomBytes, sign, verify } from 'node:crypto'
import { createWriteStream } from 'node:fs'
import { mkdir, open, readFile, rm } from 'node:fs/promises'
import { basename, dirname, isAbsolute, relative, resolve } from 'node:path'
import { promisify } from 'node:util'

const execFileAsync = promisify(execFile)
const root = process.cwd()
const evidenceRoot = resolve(process.env.BONFIRE_MEDIA_SOAK_EVIDENCE_ROOT || root)
const options = parseArgs(process.argv.slice(2))
const releaseCommit = String(options.releaseCommit || process.env.BONFIRE_RELEASE_COMMIT || '').trim()
const outputPath = resolveOutput(options.output || process.env.BONFIRE_MEDIA_SOAK_SIGNED_EVIDENCE_PATH)
const durationMs = Math.max(7_320_000, Number(process.env.BONFIRE_MEDIA_SOAK_DURATION_MS) || 7_320_000)
const sampleIntervalMs = Math.min(60_000, Math.max(5_000, Number(process.env.BONFIRE_MEDIA_SOAK_SAMPLE_INTERVAL_MS) || 30_000))
const backend = 'pion-room-actor'
const featureFlag = 'BONFIRE_MEDIA_BACKEND'
const featureFlagValue = 'pion'
const collectorVersion = 'bonfire-live-media-soak-collector-v2'
const requiredEnvironment = [
  'BONFIRE_MEDIA_SOAK_URL',
  'BONFIRE_MEDIA_SOAK_PASSWORD',
  'BONFIRE_MEDIA_SOAK_OBSERVER_TOKEN',
  'BONFIRE_MEDIA_SOAK_EXPECTED_IMAGE_DIGEST',
	'BONFIRE_MEDIA_SOAK_COLLECTOR_PRIVATE_KEY_FILE',
	'BONFIRE_MEDIA_SOAK_BUILD_MANIFEST_FILE',
	'BONFIRE_MEDIA_SOAK_BUILD_PUBLIC_KEY_FILE'
]

let rooms = []
let session = null
let temporaryPaths = []
let stopSampling = false
class CollectionStopped extends Error {}

try {
  const missing = requiredEnvironment.filter(name => !String(process.env[name] || '').trim())
  if (!/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(releaseCommit)) {
    missing.push('BONFIRE_RELEASE_COMMIT(full lowercase Git SHA)')
  }
  if (!outputPath) {
    missing.push('--output')
  }
  if (missing.length) {
    await failClosed(`missing live collector inputs: ${missing.join(', ')}`)
    throw new CollectionStopped()
  }

  const baseURL = new URL(process.env.BONFIRE_MEDIA_SOAK_URL)
  if (baseURL.protocol !== 'https:' && baseURL.hostname !== '127.0.0.1' && baseURL.hostname !== 'localhost') {
    throw new Error('live collection requires HTTPS')
  }
  const executable = await verifyGitIdentity(releaseCommit)
	const buildManifest = await verifySignedBuildManifest(executable)
  session = await memberSession(baseURL, process.env.BONFIRE_MEDIA_SOAK_PASSWORD)
  const runID = sha256(`${releaseCommit}:${Date.now()}:${process.pid}`)
  const nonce = sha256(`${runID}:${cryptoRandomSeed()}`)
  rooms = await Promise.all([
    createRoom(baseURL, session, `W2A-${runID.slice(0, 8)}-A`),
    createRoom(baseURL, session, `W2A-${runID.slice(0, 8)}-B`)
  ])
  const roomA = roomIdentity(rooms[0], runID, 'a')
  const roomB = roomIdentity(rooms[1], runID, 'b')
  const evidenceDirectory = dirname(outputPath)
  const rawDirectory = resolve(evidenceDirectory, 'raw')
  await mkdir(rawDirectory, { recursive: true, mode: 0o700 })

  const reportA = resolve(evidenceDirectory, '.room-a-report.json')
  const reportB = resolve(evidenceDirectory, '.room-b-report.json')
  const logA = resolve(evidenceDirectory, '.room-a.log')
  const logB = resolve(evidenceDirectory, '.room-b.log')
  const faultControlDirectory = resolve(evidenceDirectory, '.fault-control')
  await mkdir(faultControlDirectory, { recursive: true, mode: 0o700 })
  temporaryPaths = [reportA, reportB, logA, logB, faultControlDirectory]

  const probeA = launchBrowserProbe(baseURL, rooms[0].id, ['AJ', 'Tim', 'Tom'], reportA, logA)
  const probeB = launchBrowserProbe(baseURL, rooms[1].id, ['Caitlyn', 'Tyler', 'Joel'], reportB, logB, faultControlDirectory)
  const resourceSamples = []

  // Let both rooms reach steady-state before injecting independent failures.
  await delay(90_000)
  const observerInputs = { roomAId: rooms[0].id, roomBId: rooms[1].id }
  const headProof = await observeFaultWithBrowserProof(baseURL, session, 'head-of-line', observerInputs, nonce, faultControlDirectory)
  const headInput = headProof.observation
  bindRuntimeScopes(headInput, roomA, roomB)
  const headOfLine = normalizeHOL(headInput, roomA, roomB, headProof.attempts)
  const resourceSampler = sampleResources(resourceSamples, nonce, observerInputs)
  const aiProof = await observeFaultWithBrowserProof(baseURL, session, 'ai-failure', observerInputs, nonce, faultControlDirectory)
  const aiFailure = normalizeAIFailure(aiProof.observation, aiProof.attempts)
  await runtimeObservation(baseURL, session, 'canary-plant', observerInputs, nonce)
  let canaryChecks
  try {
    const observed = await runtimeObservation(baseURL, session, 'canary-observe', observerInputs, nonce)
    if (observed?.observed !== true || observed?.readAcknowledged !== true) throw new Error('canary read traversal was not acknowledged')
  } finally {
    const scrub = await runtimeObservation(baseURL, session, 'canary-scrub', observerInputs, nonce)
    if (Number(scrub?.residueCount) !== 0 || scrub?.scrubbed !== true) throw new Error('canary scrub left bounded test residue')
    canaryChecks = normalizeCanaries(scrub, roomA, roomB)
  }

  const [probeResultA, probeResultB] = await Promise.all([probeA, probeB])
  stopSampling = true
  await resourceSampler
  if (probeResultA.code !== 0 || probeResultB.code !== 0) {
    throw new Error(`browser probe failed (room-a=${probeResultA.code}, room-b=${probeResultB.code})`)
  }

  const reportPayloadA = JSON.parse(await readFile(reportA, 'utf8'))
  const reportPayloadB = JSON.parse(await readFile(reportB, 'utf8'))
  if (!reportPayloadA?.result?.ok || !reportPayloadB?.result?.ok) {
    throw new Error('one or both browser reports are non-passing')
  }
  const roomEvidenceA = normalizeBrowserReport(reportPayloadA, roomA)
  const roomEvidenceB = normalizeBrowserReport(reportPayloadB, roomB)
  const roomSamples = [...roomEvidenceA.roomSamples, ...roomEvidenceB.roomSamples]
  const networkSamples = [...roomEvidenceA.networkSamples, ...roomEvidenceB.networkSamples]

  const hostInput = await runtimeObservation(baseURL, session, 'runtime-attestation', observerInputs, nonce)
  const capturedAt = goTime(hostInput.capturedAt || new Date())
  const host = normalizeHost(hostInput, capturedAt, nonce, executable, buildManifest)
  if (!resourceSamples.length) throw new Error('resource sampler produced no checked samples')
  // The release window is the common intersection of both rooms and the
  // container sampler. Starting earlier would manufacture uncovered duration.
  const startedAt = goTime(new Date(Math.max(new Date(roomEvidenceA.firstAt), new Date(roomEvidenceB.firstAt), new Date(resourceSamples[0].at))))
  const endedAt = capturedAt
  resourceSamples.push(normalizeResource(await runtimeObservation(baseURL, session, 'resources', observerInputs, nonce), endedAt))

  const boundedRoomSamples = roomSamples.filter(sample => inWindow(sample.at, startedAt, endedAt))
  const boundedNetworkSamples = networkSamples.filter(sample => inWindow(sample.at, startedAt, endedAt))
  const boundedResourceSamples = resourceSamples.filter(sample => inWindow(sample.at, startedAt, endedAt))
  if (new Date(endedAt) - new Date(startedAt) < 7_200_000) {
    throw new Error('actual common evidence window was shorter than two hours')
  }

  const artifacts = []
  artifacts.push(await writeArtifact(rawDirectory, 'browser_events', 'browser-events.json', {
    roomSamples: boundedRoomSamples, rooms: [roomEvidenceA.browserEvents, roomEvidenceB.browserEvents]
  }))
  artifacts.push(await writeArtifact(rawDirectory, 'webrtc_stats', 'webrtc-stats.json', boundedNetworkSamples))
  artifacts.push(await writeArtifact(rawDirectory, 'container_metrics', 'container-metrics.json', boundedResourceSamples))
  artifacts.push(await writeArtifact(rawDirectory, 'fault_log', 'fault-log.json', { headOfLine, aiFailure, canaryChecks }))
  artifacts.push(await writeArtifact(rawDirectory, 'config_attestation', 'config-attestation.json', host))
  artifacts.push(await writeArtifact(rawDirectory, 'probe_log', 'probe-log.json', {
    collectorVersion, releaseCommit, executable, roomCount: 2, clientsPerRoom: 3,
    browserProbeExitCodes: [probeResultA.code, probeResultB.code], durationMs
  }))
  artifacts.push(await writeArtifact(rawDirectory, 'probe_image', 'container-image.json', {
    imageDigest: host.imageDigest, containerDigest: host.containerDigest
  }))
	artifacts.push(await writeRawArtifact(rawDirectory, 'build_manifest', 'signed-build-manifest.json', buildManifest.raw))

  const rawEvidence = {
    schema: 'bonfire.w2a.media-soak.raw-evidence.v1',
    evidenceMode: 'live_soak',
    synthetic: false,
    runId: runID,
    nonce,
    collectorVersion,
    releaseCommit,
    backend,
    featureFlag,
    featureFlagValue,
    startedAt,
    endedAt,
    executable,
    host,
    roomSamples: boundedRoomSamples,
    networkSamples: boundedNetworkSamples,
    resourceSamples: boundedResourceSamples,
    headOfLine,
    canaryChecks,
    aiFailures: [aiFailure],
    artifacts
  }
	const privateKey = parseEd25519PrivateKey(await readFile(process.env.BONFIRE_MEDIA_SOAK_COLLECTOR_PRIVATE_KEY_FILE))
  if (privateKey.asymmetricKeyType !== 'ed25519') {
    throw new Error('collector private key is not Ed25519')
  }
  const signature = sign(null, Buffer.from(JSON.stringify(rawEvidence)), privateKey).toString('base64')
  await writeExclusive(outputPath, `${JSON.stringify({
    schema: 'bonfire.w2a.media-soak.signed-raw-evidence.v1',
    payload: rawEvidence,
    signature: { algorithm: 'ed25519', keyId: 'bonfire-media-collector', value: signature }
  }, null, 2)}\n`)
  process.stdout.write(`${JSON.stringify({ schema: 'bonfire.w2a.media-soak.collection.v1', releaseCommit, releaseQualified: false, signedRawEvidencePath: relative(evidenceRoot, outputPath) })}\n`)
} catch (error) {
  if (!(error instanceof CollectionStopped)) {
    await failClosed(error?.message || String(error))
  }
} finally {
  stopSampling = true
  await Promise.all(temporaryPaths.map(path => rm(path, { force: true, recursive: true }).catch(() => {})))
  if (session && rooms.length) {
    const baseURL = new URL(process.env.BONFIRE_MEDIA_SOAK_URL)
    await Promise.all(rooms.map(room => archiveRoom(baseURL, session, room.id).catch(() => {})))
  }
}

function parseArgs(args) {
  const result = {}
  for (let index = 0; index < args.length; index += 1) {
    if (args[index] === '--release-commit' && args[index + 1]) result.releaseCommit = args[++index]
    else if (args[index] === '--output' && args[index + 1]) result.output = args[++index]
  }
  return result
}

function resolveOutput(value) {
  const trimmed = String(value || '').trim()
  if (!trimmed) return ''
	const result = isAbsolute(trimmed) ? resolve(trimmed) : resolve(evidenceRoot, trimmed)
	const rel = relative(evidenceRoot, result)
  if (rel === '..' || rel.startsWith(`..${process.platform === 'win32' ? '\\' : '/'}`)) return ''
  return result
}

async function failClosed(reason) {
  const payload = {
    schema: 'bonfire.w2a.media-soak.collection-failure.v1',
    releaseCommit,
    evidenceMode: 'unavailable',
    releaseQualified: false,
    stopTriggered: true,
    reason: String(reason).slice(0, 500)
  }
  if (outputPath) {
    await mkdir(dirname(outputPath), { recursive: true, mode: 0o700 }).catch(() => {})
    await writeExclusive(resolve(dirname(outputPath), 'collection-failure.json'), `${JSON.stringify(payload, null, 2)}\n`).catch(() => {})
  }
  process.stderr.write(`${JSON.stringify(payload)}\n`)
  process.exitCode = 2
}

async function memberSession(baseURL, password) {
  const response = await fetch(new URL('/auth/login', baseURL), {
    method: 'POST', headers: { 'content-type': 'application/json', origin: baseURL.origin },
    body: JSON.stringify({ name: 'AJ', password })
  })
  if (!response.ok) throw new Error(`collector login failed (${response.status})`)
  const rawCookie = response.headers.get('set-cookie') || ''
  const cookie = rawCookie.split(';', 1)[0]
  if (!cookie.includes('=')) throw new Error('collector login returned no session cookie')
  return cookie
}

async function createRoom(baseURL, cookie, name) {
  const response = await fetch(new URL('/rooms', baseURL), {
    method: 'POST', headers: { 'content-type': 'application/json', cookie, origin: baseURL.origin },
    body: JSON.stringify({ name, passcode: '', guestAccess: false })
  })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok || !payload?.room?.id) throw new Error(`room creation failed (${response.status})`)
  return { id: String(payload.room.id) }
}

async function archiveRoom(baseURL, cookie, id) {
  await fetch(new URL(`/rooms/${encodeURIComponent(id)}/archive`, baseURL), {
    method: 'POST', headers: { cookie, origin: baseURL.origin }
  })
}

function roomIdentity(room, runID, suffix) {
  return {
    id: room.id,
    roomDigest: sha256(`room:${room.id}`),
    sittingDigest: '',
    generationDigest: ''
  }
}

function launchBrowserProbe(baseURL, roomID, participants, reportPath, logPath, faultControlDirectory = '') {
  const log = createWriteStream(logPath, { flags: 'wx', mode: 0o600 })
  const url = new URL(baseURL)
  url.searchParams.set('room', roomID)
  const args = [
    'scripts/live-media-smoke.mjs', '--url', url.toString(),
    '--participants', participants.join(','), '--separate-browsers', '--duration-ms', String(durationMs),
    '--emit-report', reportPath, '--timeout-ms', '120000'
  ]
	if (faultControlDirectory) args.push('--media-soak-probe', '--fault-control-dir', faultControlDirectory)
  const child = spawn(process.execPath, args, { cwd: root, env: subprocessEnvironment({ ROOM_PASSWORD: process.env.BONFIRE_MEDIA_SOAK_PASSWORD }), stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout.pipe(log)
  child.stderr.pipe(log)
  return new Promise(resolvePromise => child.once('close', code => {
    log.end()
    resolvePromise({ code: Number(code ?? 1) })
  }))
}

async function observeFaultWithBrowserProof(baseURL, cookie, kind, inputs, nonce, controlDirectory) {
  const requestId = sha256(`${nonce}:${kind}:browser:${cryptoRandomSeed()}`)
  const observationPromise = runtimeObservation(baseURL, cookie, kind, { ...inputs, faultDurationMs: 10_000 }, nonce)
  await delay(200)
  await writeExclusive(resolve(controlDirectory, 'command.json'), `${JSON.stringify({
    schema: 'bonfire.w2a.browser-fault-command.v1', requestId, kind,
    deadline: new Date(Date.now() + 20_000).toISOString()
  })}\n`)
  const responsePath = resolve(controlDirectory, `response-${requestId}.json`)
  let response
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    try {
      response = JSON.parse(await readFile(responsePath, 'utf8'))
      break
    } catch (error) {
      if (error?.code !== 'ENOENT') throw error
      await delay(100)
    }
  }
  const observation = await observationPromise
  await rm(responsePath, { force: true })
  if (response?.schema !== 'bonfire.w2a.browser-fault-proof.v1' || response.requestId !== requestId || response.kind !== kind || response.ok !== true || !Array.isArray(response.attempts)) {
    throw new Error(`${kind} did not return a checked transient-browser proof`)
  }
  return { observation, attempts: response.attempts }
}

async function sampleResources(samples, nonce, observerInputs) {
  while (!stopSampling) {
    try {
      const at = goTime(new Date())
      if (!session || rooms.length !== 2) throw new Error('runtime observation session is unavailable')
      const baseURL = new URL(process.env.BONFIRE_MEDIA_SOAK_URL)
      const value = await runtimeObservation(baseURL, session, 'resources', observerInputs, nonce)
      samples.push(normalizeResource(value, at))
    } catch (error) {
      samples.push({ at: goTime(new Date()), sourceDigest: sha256(`resource-error:${error?.message || error}`), processCpuPercent: 100, rssContainerLimitPercent: 100 })
    }
    await delay(sampleIntervalMs)
  }
}

function normalizeBrowserReport(report, room) {
  const snapshots = Array.isArray(report?.result?.soakSnapshots) ? report.result.soakSnapshots : []
  const groups = new Map()
  for (const snapshot of snapshots) {
    const at = goTime(snapshot.capturedAt)
    if (!groups.has(at)) groups.set(at, [])
    groups.get(at).push(snapshot)
  }
  const roomSamples = []
  const networkSamples = []
  const browserEvents = []
  let cumulativeExpected = 0
  let cumulativeLost = 0
  let previousExpected = 0
  let previousLost = 0
  for (const [at, batch] of [...groups.entries()].sort(([left], [right]) => new Date(left) - new Date(right))) {
    const names = new Set(batch.flatMap(snapshot => Array.isArray(snapshot.participantsInRoom) ? snapshot.participantsInRoom : []))
    const publishers = batch.filter(snapshot => liveKinds(snapshot.localTracks).has('audio') && liveKinds(snapshot.localTracks).has('video')).length
    const subscribers = batch.filter(snapshot => Number(snapshot.remoteElements) >= 2 && ['connected', 'completed'].includes(String(snapshot.connectionState || snapshot.iceConnectionState))).length
    const sanitized = batch.map(snapshot => ({
      participantDigest: sha256(String(snapshot.name || 'unknown')),
      connectionState: String(snapshot.connectionState || ''), iceConnectionState: String(snapshot.iceConnectionState || ''),
      remoteElements: Number(snapshot.remoteElements) || 0, participantCount: Array.isArray(snapshot.participantsInRoom) ? snapshot.participantsInRoom.length : 0
    }))
    const sourceDigest = sha256(JSON.stringify({ at, room: room.roomDigest, sanitized }))
    roomSamples.push({ at, sourceDigest, roomDigest: room.roomDigest, sittingDigest: room.sittingDigest, mediaGenerationDigest: room.generationDigest, publishers, subscribers, participants: names.size })
    const inbound = batch.flatMap(snapshot => Array.isArray(snapshot.stats) ? snapshot.stats : []).filter(stat => stat.type === 'inbound-rtp')
    const currentLost = inbound.reduce((total, stat) => total + Math.max(0, Number(stat.packetsLost) || 0), 0)
    const currentExpected = inbound.reduce((total, stat) => total + Math.max(0, Number(stat.packetsReceived) || 0) + Math.max(0, Number(stat.packetsLost) || 0), 0)
    cumulativeExpected += currentExpected >= previousExpected ? currentExpected - previousExpected : currentExpected
    cumulativeLost += currentLost >= previousLost ? currentLost - previousLost : currentLost
    previousExpected = currentExpected
    previousLost = currentLost
    networkSamples.push({
      at, sourceDigest: sha256(JSON.stringify({ at, currentExpected, currentLost })), roomDigest: room.roomDigest,
      packetsExpected: cumulativeExpected, packetsLost: cumulativeLost,
      unexpectedDisconnects: batch.filter(snapshot => !['connected', 'completed'].includes(String(snapshot.connectionState || snapshot.iceConnectionState))).length
    })
    browserEvents.push({ at, sourceDigest, connectedClients: subscribers, publishers, participants: names.size })
  }
  if (roomSamples.length < 120) throw new Error(`browser report ${basename(report?.config?.url || 'room')} has too few raw samples`)
  return { roomSamples, networkSamples, browserEvents, firstAt: roomSamples[0].at, lastAt: roomSamples.at(-1).at }
}

function liveKinds(tracks) {
  return new Set((Array.isArray(tracks) ? tracks : []).filter(track => track?.readyState === 'live').map(track => track.kind))
}

function normalizeResource(value, at) {
  const cpu = Number(value.processCpuPercent)
  const rss = Number(value.rssContainerLimitPercent)
  if (!Number.isFinite(cpu) || !Number.isFinite(rss) || cpu < 0 || cpu > 100 || rss < 0 || rss > 100) throw new Error('resource hook returned invalid percentages')
  return { at: goTime(at), sourceDigest: sha256(JSON.stringify(value)), processCpuPercent: cpu, rssContainerLimitPercent: rss }
}

function normalizeHost(value, capturedAt, nonce, executable, buildManifest) {
  if (value.releaseCommit !== releaseCommit || value.backend !== backend || value.featureFlag !== featureFlag || value.featureFlagValue !== featureFlagValue) {
    throw new Error('host attestation does not bind the requested release/backend/flag')
  }
  for (const field of ['hostDigest', 'containerDigest', 'imageDigest', 'gitTreeDigest', 'buildManifestSha256', 'configSha256', 'transitiveInputsSha256', 'sourceArchiveSha256', 'collectorNonce']) {
    if (!/^(?:sha256:)?[0-9a-f]{64}$/.test(String(value[field] || ''))) throw new Error(`runtime attestation missing digest ${field}`)
  }
  if (String(value.collectorNonce).replace(/^sha256:/, '') !== nonce) throw new Error('runtime attestation is not bound to the collector nonce')
  if (String(value.gitTreeDigest).replace(/^sha256:/, '') !== executable.gitTreeDigest) throw new Error('live runtime tree is not the collector release tree')
	if (String(value.buildManifestSha256).replace(/^sha256:/, '') !== buildManifest.digest
	    || String(value.configSha256).replace(/^sha256:/, '') !== executable.configSha256
	    || String(value.transitiveInputsSha256).replace(/^sha256:/, '') !== executable.transitiveInputsSha256
	    || String(value.sourceArchiveSha256).replace(/^sha256:/, '') !== executable.sourceArchiveSha256) {
	  throw new Error('live runtime does not match the signed transitive build manifest')
	}
  const expectedImageDigest = String(process.env.BONFIRE_MEDIA_SOAK_EXPECTED_IMAGE_DIGEST).replace(/^sha256:/, '')
  if (!/^[0-9a-f]{64}$/.test(expectedImageDigest) || String(value.imageDigest).replace(/^sha256:/, '') !== expectedImageDigest) {
    throw new Error('live runtime image does not match the independently supplied image digest')
  }
	if (buildManifest.payload.ociImageDigest.replace(/^sha256:/, '') !== expectedImageDigest
	    || buildManifest.payload.binarySha256.replace(/^sha256:/, '') !== String(value.containerDigest).replace(/^sha256:/, '')) {
	  throw new Error('deployed OCI or executable identity differs from the independently signed build manifest')
	}
  return {
    hostDigest: String(value.hostDigest).replace(/^sha256:/, ''), containerDigest: String(value.containerDigest).replace(/^sha256:/, ''), imageDigest: String(value.imageDigest).replace(/^sha256:/, ''),
	gitTreeDigest: String(value.gitTreeDigest).replace(/^sha256:/, ''), buildManifestSha256: buildManifest.digest,
	configSha256: executable.configSha256, transitiveInputsSha256: executable.transitiveInputsSha256, sourceArchiveSha256: executable.sourceArchiveSha256, collectorNonce: nonce,
    releaseCommit, backend, featureFlag, featureFlagValue, capturedAt
  }
}

function bindRuntimeScopes(value, roomA, roomB) {
  for (const [expected, observed] of [[roomA, value.roomA], [roomB, value.roomB]]) {
    if (String(observed?.roomId) !== expected.id || observed?.roomDigest !== expected.roomDigest ||
        !/^[0-9a-f]{64}$/.test(String(observed?.sittingDigest || '')) || !/^[0-9a-f]{64}$/.test(String(observed?.mediaGenerationDigest || ''))) {
      throw new Error('runtime observer returned an invalid room/sitting/media-generation scope')
    }
    expected.sittingDigest = observed.sittingDigest
    expected.generationDigest = observed.mediaGenerationDigest
  }
}

function normalizeHOL(value, roomA, roomB, attempts) {
  if (String(value.roomAId) !== roomA.id || String(value.roomBId) !== roomB.id) throw new Error('HOL hook targeted the wrong rooms')
  const blockedAt = goTime(value.blockedAt)
  const releasedAt = goTime(value.releasedAt)
  if (new Date(releasedAt) - new Date(blockedAt) < 10_000) throw new Error('HOL hook did not block room A for ten seconds')
  return {
    roomADigest: roomA.roomDigest, roomBDigest: roomB.roomDigest, blockedAt, releasedAt,
    admissionAttempts: normalizeAttempts(attempts), renegotiationEvents: normalizeAttempts(attempts),
    sourceDigest: sha256(JSON.stringify(value))
  }
}

function normalizeAttempts(values) {
  if (!Array.isArray(values)) throw new Error('fault hook omitted latency attempts')
  return values.map(value => {
    const client = String(value.clientId || value.clientDigest || '')
    const offer = String(value.offerCorrelation || value.offerDigest || '')
    const answer = String(value.answerCorrelation || value.answerDigest || '')
    const before = Number(value.rtpPacketsBefore)
    const after = Number(value.rtpPacketsAfter)
    if (!client || !offer || !answer || offer !== answer || !Number.isSafeInteger(before) || !Number.isSafeInteger(after) || before < 0 || after <= before) {
      throw new Error('transient browser proof omitted distinct correlation or monotonic RTP')
    }
    return {
      startedAt: goTime(value.startedAt), completedAt: goTime(value.completedAt), succeeded: value.succeeded === true, sourceDigest: sha256(JSON.stringify(value)),
      clientDigest: sha256(client), offerDigest: sha256(offer), answerDigest: sha256(answer),
      onTrackAt: goTime(value.onTrackAt), rtpPacketsBefore: before, rtpPacketsAfter: after
    }
  })
}

function normalizeAIFailure(value, attempts) {
  const injectedAt = goTime(value.injectedAt)
  const recoveredAt = goTime(value.recoveredAt)
  const mediaInterruptions = Number(value.mediaInterruptions)
  const admissionFailures = Number(value.admissionFailures)
  if (new Date(recoveredAt) <= new Date(injectedAt) || !Number.isInteger(mediaInterruptions) || mediaInterruptions < 0 ||
      !Number.isInteger(admissionFailures) || admissionFailures < 0) {
    throw new Error('AI-failure hook returned an invalid continuity result')
  }
  return {
    injectedAt, recoveredAt, sourceDigest: sha256(JSON.stringify(value)), mediaInterruptions, admissionFailures,
    roomBCompletions: normalizeAttempts(attempts)
  }
}

function normalizeCanaries(value, roomA, roomB) {
  if (!Array.isArray(value.checks)) throw new Error('canary hook omitted checks')
  const allowedRooms = new Set([roomA.roomDigest, roomB.roomDigest])
  return value.checks.map(check => {
    const normalized = {
	  at: goTime(check.at), sourceDigest: sha256(JSON.stringify(check)), surface: String(check.surface),
	  direction: String(check.direction), sentinel: String(check.sentinel),
      sourceRoomDigest: String(check.sourceRoomDigest), observedRoomDigest: String(check.observedRoomDigest),
      sourceSittingDigest: String(check.sourceSittingDigest), observedSittingDigest: String(check.observedSittingDigest),
      sourceGenerationDigest: String(check.sourceGenerationDigest), observedGenerationDigest: String(check.observedGenerationDigest),
      expectedPresent: Boolean(check.expectedPresent), observed: Boolean(check.observed),
      publicationRecipientSetDigest: String(check.publicationRecipientSetDigest || ''), deletionRecipientSetDigest: String(check.deletionRecipientSetDigest || ''),
      publicationRecipientCount: Number(check.publicationRecipientCount), deletionRecipientCount: Number(check.deletionRecipientCount)
      , ingressAcknowledged: check.ingressAcknowledged === true, readAcknowledged: check.readAcknowledged === true,
      scrubAcknowledged: check.scrubAcknowledged === true, residueCount: Number(check.residueCount)
    }
    if (!allowedRooms.has(normalized.sourceRoomDigest) || !allowedRooms.has(normalized.observedRoomDigest)) throw new Error('canary hook referenced an unknown room')
    if (!normalized.ingressAcknowledged || !normalized.readAcknowledged || !normalized.scrubAcknowledged
        || !Number.isSafeInteger(normalized.residueCount) || normalized.residueCount !== 0) {
      throw new Error('canary lifecycle acknowledgement is incomplete or retained residue')
    }
	const publicationSurface = normalized.surface === 'chat' || normalized.surface === 'recap' || normalized.surface === 'artifact'
	if (publicationSurface && (!/^[0-9a-f]{64}$/.test(normalized.publicationRecipientSetDigest)
	    || !/^[0-9a-f]{64}$/.test(normalized.deletionRecipientSetDigest)
	    || !Number.isSafeInteger(normalized.publicationRecipientCount) || normalized.publicationRecipientCount <= 0
	    || normalized.publicationRecipientCount !== normalized.deletionRecipientCount
	    || normalized.publicationRecipientSetDigest !== normalized.deletionRecipientSetDigest)) {
	  throw new Error('canary publication/deletion recipient proof is invalid')
	}
	if (!publicationSurface && (normalized.publicationRecipientSetDigest || normalized.deletionRecipientSetDigest
	    || normalized.publicationRecipientCount !== 0 || normalized.deletionRecipientCount !== 0)) {
	  throw new Error('non-publication canary carried recipient proof')
	}
    return normalized
  })
}

async function runtimeObservation(baseURL, cookie, kind, inputs, nonce) {
  const issuedAt = new Date()
  const requestId = sha256(`${nonce}:${kind}:${issuedAt.toISOString()}:${cryptoRandomSeed()}`)
  const requestPayload = {
    schema: 'bonfire.w2a.runtime-observation-request.v1', releaseCommit, nonce, purpose: kind, requestId,
    issuedAt: issuedAt.toISOString(), expiresAt: new Date(issuedAt.getTime() + 30_000).toISOString(), inputs
  }
  const body = JSON.stringify(requestPayload)
  const path = `/internal/media-soak/${kind}`
  const macMessage = ['POST', path, releaseCommit, nonce, kind, requestId, requestPayload.issuedAt, requestPayload.expiresAt, sha256(body)].join('\n')
  const requestMAC = createHmac('sha256', process.env.BONFIRE_MEDIA_SOAK_OBSERVER_TOKEN).update(macMessage).digest('hex')
  const response = await fetch(new URL(path, baseURL), {
    method: 'POST', headers: {
      'content-type': 'application/json', cookie, origin: baseURL.origin,
      authorization: `Bearer ${process.env.BONFIRE_MEDIA_SOAK_OBSERVER_TOKEN}`,
      'x-bonfire-media-soak-purpose': kind, 'x-bonfire-media-soak-mac': requestMAC
    },
    body
  })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok || payload?.schema !== 'bonfire.w2a.runtime-observation.v1' || payload?.kind !== kind ||
      payload?.releaseCommit !== releaseCommit || payload?.requestId !== requestId || (nonce && payload?.collectorNonce !== nonce)) {
    throw new Error(`checked runtime observer ${kind} is unavailable or unbound`)
  }
  return payload.observation
}

async function verifyGitIdentity(expectedCommit) {
	try {
	  const sourceRaw = await readFile(resolve(root, '.bonfire-release-source.json'))
	  const source = JSON.parse(sourceRaw)
	  if (source?.schema !== 'bonfire.w2a.immutable-source-export.v1' || source?.executable?.head !== expectedCommit
	      || !Array.isArray(source.inputs) || source.inputs.length !== Number(source.executable.inputCount)) {
	    throw new Error('immutable source export manifest is invalid')
	  }
	  let previousPath = ''
	  for (const input of source.inputs) {
	    const path = String(input?.path || '')
	    const rel = relative(root, resolve(root, path))
	    if (!path || path <= previousPath || rel === '..' || rel.startsWith(`..${process.platform === 'win32' ? '\\' : '/'}`)
	        || sha256(await readFile(resolve(root, path))) !== input.sha256) {
	      throw new Error('immutable source export input digest mismatch')
	    }
	    previousPath = path
	  }
	  return source.executable
	} catch (error) {
	  if (error?.code !== 'ENOENT') throw error
	}
  const { stdout: head } = await execFileAsync('git', ['rev-parse', 'HEAD'], { cwd: root, timeout: 10_000 })
  if (head.trim() !== expectedCommit) throw new Error('collector checkout is not the requested release commit')
  const paths = ['testdata/w2a/live-media-soak-probe.mjs', 'scripts/live-media-smoke.mjs', 'cmd/media-soak/main.go']
  const { stdout: dirty } = await execFileAsync('git', ['status', '--porcelain', '--untracked-files=all'], { cwd: root, timeout: 10_000 })
  if (dirty.trim()) throw new Error('collector executable inputs are modified')
  const blobs = {}
  for (const path of paths.slice(0, 3)) {
    const [{ stdout: working }, { stdout: committed }] = await Promise.all([
      execFileAsync('git', ['hash-object', path], { cwd: root, timeout: 10_000 }),
      execFileAsync('git', ['rev-parse', `${expectedCommit}:${path}`], { cwd: root, timeout: 10_000 })
    ])
    if (working.trim() !== committed.trim()) throw new Error(`collector executable blob drift: ${path}`)
    blobs[path] = working.trim()
  }
	const [{ stdout: tree }, { stdout: transitive }, { stdout: archive }] = await Promise.all([
	  execFileAsync('git', ['rev-parse', `${expectedCommit}^{tree}`], { cwd: root, timeout: 10_000 }),
	  execFileAsync('git', ['ls-tree', '-r', '--full-tree', expectedCommit], { cwd: root, timeout: 10_000, maxBuffer: 64 << 20 }),
	  execFileAsync('git', ['archive', '--format=tar', expectedCommit], { cwd: root, timeout: 30_000, maxBuffer: 512 << 20, encoding: 'buffer' })
	])
	const inputLines = String(transitive).trimEnd().split('\n').filter(Boolean)
	const configPaths = new Set(['Dockerfile', 'go.mod', 'go.sum', 'deploy/digitalocean/docker-compose.yml', 'deploy/digitalocean/Caddyfile'])
	const configLines = inputLines.filter(line => configPaths.has(line.split('\t', 2)[1]))
	if (inputLines.length === 0 || configLines.length !== configPaths.size) throw new Error('release transitive/config inputs are incomplete')
  return {
    head: expectedCommit,
	gitTreeObject: tree.trim(),
    gitTreeDigest: sha256(tree.trim()),
	sourceArchiveSha256: sha256(archive),
	transitiveInputsSha256: sha256(String(transitive)),
	configSha256: sha256(`${configLines.join('\n')}\n`),
	inputCount: inputLines.length,
    collectorGitBlob: blobs['testdata/w2a/live-media-soak-probe.mjs'],
    browserGitBlob: blobs['scripts/live-media-smoke.mjs'],
    gateGitBlob: blobs['cmd/media-soak/main.go']
  }
}

async function verifySignedBuildManifest(executable) {
	const raw = await readFile(process.env.BONFIRE_MEDIA_SOAK_BUILD_MANIFEST_FILE)
	const signed = JSON.parse(raw.toString('utf8'))
	if (signed?.schema !== 'bonfire.w2a.signed-build-manifest.v1' || signed?.payload?.schema !== 'bonfire.w2a.build-manifest.v1'
	    || signed?.signature?.algorithm !== 'ed25519' || signed?.signature?.keyId !== 'bonfire-release-operator') {
	  throw new Error('signed build manifest contract is invalid')
	}
	const publicKey = parseEd25519PublicKey(await readFile(process.env.BONFIRE_MEDIA_SOAK_BUILD_PUBLIC_KEY_FILE))
	const signature = Buffer.from(String(signed.signature.value || ''), 'base64')
	if (publicKey.asymmetricKeyType !== 'ed25519' || signature.length !== 64
	    || !verify(null, Buffer.from(stableStringify(signed.payload)), publicKey, signature)) {
	  throw new Error('signed build manifest signature is invalid')
	}
	const payload = signed.payload
	for (const field of ['gitTreeDigest', 'sourceArchiveSha256', 'transitiveInputsSha256', 'configSha256', 'binarySha256', 'ociImageDigest']) {
	  if (!/^(?:sha256:)?[0-9a-f]{64}$/.test(String(payload[field] || ''))) throw new Error(`build manifest missing ${field}`)
	}
	if (payload.releaseCommit !== releaseCommit || payload.gitTreeObject !== executable.gitTreeObject
	    || payload.gitTreeDigest.replace(/^sha256:/, '') !== executable.gitTreeDigest
	    || payload.sourceArchiveSha256.replace(/^sha256:/, '') !== executable.sourceArchiveSha256
	    || payload.transitiveInputsSha256.replace(/^sha256:/, '') !== executable.transitiveInputsSha256
	    || payload.configSha256.replace(/^sha256:/, '') !== executable.configSha256
	    || Number(payload.inputCount) !== executable.inputCount) {
	  throw new Error('signed build manifest does not bind the immutable release export')
	}
	return { payload, digest: sha256(raw), raw }
}

function subprocessEnvironment(extraEnvironment = {}) {
  const environment = { ...process.env, ...extraEnvironment }
  for (const secret of [
    'BONFIRE_MEDIA_SOAK_COLLECTOR_PRIVATE_KEY_FILE',
    'BONFIRE_MEDIA_SOAK_OBSERVER_TOKEN',
	'BONFIRE_MEDIA_SOAK_EXPECTED_IMAGE_DIGEST',
	'BONFIRE_MEDIA_SOAK_BUILD_MANIFEST_FILE',
	'BONFIRE_MEDIA_SOAK_BUILD_PUBLIC_KEY_FILE',
    'BONFIRE_MEDIA_SOAK_RELEASE_PRIVATE_KEY_FILE',
    'BONFIRE_RELEASE_OPERATOR_PRIVATE_KEY',
    'BONFIRE_MEDIA_SOAK_PASSWORD',
    'MEETING_ROOM_PASSWORD'
  ]) {
    delete environment[secret]
  }
  return environment
}

async function writeArtifact(directory, kind, filename, payload) {
  const absolute = resolve(directory, filename)
  const body = `${JSON.stringify(payload, null, 2)}\n`
  await writeExclusive(absolute, body)
	return { kind, path: relative(evidenceRoot, absolute).split('\\').join('/'), sha256: sha256(body), bytes: Buffer.byteLength(body) }
}

async function writeRawArtifact(directory, kind, filename, body) {
	const absolute = resolve(directory, filename)
	await writeExclusive(absolute, body)
	return { kind, path: relative(evidenceRoot, absolute).split('\\').join('/'), sha256: sha256(body), bytes: Buffer.byteLength(body) }
}

async function writeExclusive(path, value) {
  await mkdir(dirname(path), { recursive: true, mode: 0o700 })
  const file = await open(path, 'wx', 0o600)
  try {
    await file.writeFile(value)
    await file.sync()
  } finally {
    await file.close()
  }
}

function inWindow(value, startedAt, endedAt) {
  const at = new Date(value).getTime()
  return at >= new Date(startedAt).getTime() && at <= new Date(endedAt).getTime()
}

function goTime(value) {
  const date = value instanceof Date ? value : new Date(value)
  if (!Number.isFinite(date.getTime())) throw new Error(`invalid timestamp: ${String(value)}`)
  return date.toISOString().replace(/\.([0-9]*?)0+Z$/, (_, digits) => digits ? `.${digits}Z` : 'Z')
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex')
}

function stableStringify(value) {
	if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`
	if (value && typeof value === 'object') {
	  return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${stableStringify(value[key])}`).join(',')}}`
	}
	return JSON.stringify(value)
}

function parseEd25519PublicKey(raw) {
	try { return createPublicKey(raw) } catch {}
	const decoded = Buffer.from(String(raw).trim(), 'base64')
	if (decoded.length !== 32) throw new Error('build manifest public key is not Ed25519')
	return createPublicKey({ key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), decoded]), format: 'der', type: 'spki' })
}

function parseEd25519PrivateKey(raw) {
	try { return createPrivateKey(raw) } catch {}
	const decoded = Buffer.from(String(raw).trim(), 'base64')
	if (decoded.length !== 64) throw new Error('collector private key is not Ed25519')
	return createPrivateKey({ key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), decoded.subarray(0, 32)]), format: 'der', type: 'pkcs8' })
}

function cryptoRandomSeed() {
  return randomBytes(32).toString('hex')
}

function delay(milliseconds) {
  return new Promise(resolvePromise => setTimeout(resolvePromise, milliseconds))
}
