#!/usr/bin/env node
import { lstat, readFile } from 'node:fs/promises'
import { isAbsolute, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const INPUT_SCHEMA = 'bonfire.e10.room-disconnect-incident-soak.input.v1'
export const ASSESSMENT_SCHEMA = 'bonfire.e10.room-disconnect-incident-soak.assessment.v1'

export const SAFETY_CONTRACT = Object.freeze({
  collection: 'none',
  inputMode: 'pre_recorded_content_free_evidence_only',
  networkAccess: 'forbidden',
  productionMutation: 'forbidden',
  roomJoin: 'forbidden',
  filesystemWrites: 'none',
  formalMediaSoakQualification: false,
})

export const FROZEN_THRESHOLDS = Object.freeze({
  schema: 'bonfire.e10.room-disconnect-incident-soak.thresholds.2026-08-14.v1',
  minimumDurationSeconds: 7200,
  expectedParticipantCount: 2,
  maximumSampleGapSeconds: 30,
  maximumEndpointSkewSeconds: 30,
  requiredMemoryLimitBytes: 3 * 1024 * 1024 * 1024,
  maximumMemoryLimitPercent: 75,
  maximumContainerRestartDelta: 0,
  maximumOomEventDelta: 0,
  maximumOomKillEventDelta: 0,
  maximumUnexpectedDisconnectDelta: 0,
  maximumUnexpectedSocketCloseDelta: 0,
  maximumRecoverySeconds: 20,
  maximumPacketLossPercent: 2,
  minimumInboundAudioPacketDeltaPerParticipant: 100,
  minimumOutboundAudioPacketDeltaPerParticipant: 100,
})

const releaseCommitPattern = /^[a-f0-9]{40}$/
const sha256Pattern = /^[a-f0-9]{64}$/
const imageIDPattern = /^sha256:[a-f0-9]{64}$/
const containerIDPattern = /^[a-f0-9]{12,64}$/
const opaqueIDPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/
const participantAliases = ['participant-a', 'participant-b']
const connectionStates = new Set(['new', 'connecting', 'connected', 'disconnected', 'failed', 'closed'])
const iceStates = new Set(['new', 'checking', 'connected', 'completed', 'disconnected', 'failed', 'closed'])
const signalingStates = new Set(['stable', 'have-local-offer', 'have-remote-offer', 'have-local-pranswer', 'have-remote-pranswer', 'closed'])
const candidateTypes = new Set(['host', 'srflx', 'prflx', 'relay'])
const protocols = new Set(['udp', 'tcp', 'tls'])
const candidatePairStates = new Set(['frozen', 'waiting', 'in-progress', 'failed', 'succeeded'])

function exactObject(value, keys, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`)
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    throw new Error(`${label} has unexpected or missing fields`)
  }
  return value
}

function finiteNumber(value, label, { minimum = 0 } = {}) {
  if (!Number.isFinite(value) || value < minimum) throw new Error(`${label} must be a finite number >= ${minimum}`)
  return value
}

function nonnegativeInteger(value, label) {
  if (!Number.isSafeInteger(value) || value < 0) throw new Error(`${label} must be a nonnegative integer`)
  return value
}

function timestampMillis(value, label) {
  if (typeof value !== 'string' || !value.endsWith('Z')) throw new Error(`${label} must be an ISO-8601 UTC timestamp`)
  const millis = Date.parse(value)
  if (!Number.isFinite(millis) || new Date(millis).toISOString() !== value) throw new Error(`${label} must be a canonical ISO-8601 UTC timestamp`)
  return millis
}

function opaqueID(value, label) {
  if (typeof value !== 'string' || !opaqueIDPattern.test(value)) throw new Error(`${label} must be a content-free opaque identifier`)
  return value
}

function enumValue(value, values, label) {
  if (!values.has(value)) throw new Error(`${label} is invalid`)
  return value
}

function validateBinding(raw) {
  const binding = exactObject(raw, [
    'releaseCommit', 'activeReleaseDirectory', 'releaseReceiptSha256', 'serviceImageId',
    'roomId', 'sittingId', 'mediaGeneration',
  ], 'binding')
  if (!releaseCommitPattern.test(binding.releaseCommit ?? '')) throw new Error('binding.releaseCommit must be an exact lowercase 40-character commit')
  const expectedDirectory = `/opt/meetingassist-releases/${binding.releaseCommit}`
  if (binding.activeReleaseDirectory !== expectedDirectory) throw new Error('binding.activeReleaseDirectory must bind the exact release commit')
  if (!sha256Pattern.test(binding.releaseReceiptSha256 ?? '')) throw new Error('binding.releaseReceiptSha256 must be a lowercase SHA-256 digest')
  if (!imageIDPattern.test(binding.serviceImageId ?? '')) throw new Error('binding.serviceImageId must be an exact Docker image ID')
  opaqueID(binding.roomId, 'binding.roomId')
  opaqueID(binding.sittingId, 'binding.sittingId')
  opaqueID(binding.mediaGeneration, 'binding.mediaGeneration')
  return binding
}

function validateWindow(raw) {
  const window = exactObject(raw, ['startedAt', 'endedAt'], 'window')
  const start = timestampMillis(window.startedAt, 'window.startedAt')
  const end = timestampMillis(window.endedAt, 'window.endedAt')
  if (end <= start) throw new Error('window.endedAt must be after window.startedAt')
  return { start, end, durationSeconds: (end - start) / 1000 }
}

function validateFrozenThresholds(raw) {
  exactObject(raw, Object.keys(FROZEN_THRESHOLDS), 'thresholds')
  if (JSON.stringify(raw) !== JSON.stringify(FROZEN_THRESHOLDS)) throw new Error('thresholds must exactly match the frozen incident-soak thresholds')
}

function validateMemoryEvents(raw, label) {
  const value = exactObject(raw, ['oom', 'oomKill'], label)
  nonnegativeInteger(value.oom, `${label}.oom`)
  nonnegativeInteger(value.oomKill, `${label}.oomKill`)
  return value
}

function validateContainerSample(raw, index, binding, window) {
  const label = `containerSamples[${index}]`
  const sample = exactObject(raw, [
    'timestamp', 'containerId', 'imageId', 'restartCount', 'oomKilled', 'cpuPercent',
    'memoryCurrentBytes', 'memoryPeakBytes', 'memoryLimitBytes', 'memoryEvents',
  ], label)
  const timestamp = timestampMillis(sample.timestamp, `${label}.timestamp`)
  if (timestamp < window.start || timestamp > window.end) throw new Error(`${label}.timestamp is outside the soak window`)
  if (!containerIDPattern.test(sample.containerId ?? '')) throw new Error(`${label}.containerId must be an exact content-free container ID`)
  if (sample.imageId !== binding.serviceImageId) throw new Error(`${label}.imageId does not match the bound service image`)
  nonnegativeInteger(sample.restartCount, `${label}.restartCount`)
  if (typeof sample.oomKilled !== 'boolean') throw new Error(`${label}.oomKilled must be boolean`)
  finiteNumber(sample.cpuPercent, `${label}.cpuPercent`)
  nonnegativeInteger(sample.memoryCurrentBytes, `${label}.memoryCurrentBytes`)
  nonnegativeInteger(sample.memoryPeakBytes, `${label}.memoryPeakBytes`)
  if (sample.memoryPeakBytes < sample.memoryCurrentBytes) throw new Error(`${label}.memoryPeakBytes cannot be lower than memoryCurrentBytes`)
  nonnegativeInteger(sample.memoryLimitBytes, `${label}.memoryLimitBytes`)
  validateMemoryEvents(sample.memoryEvents, `${label}.memoryEvents`)
  return { ...sample, timestampMillis: timestamp }
}

function validateCandidatePair(raw, label) {
  const value = exactObject(raw, [
    'state', 'nominated', 'protocol', 'localCandidateType', 'remoteCandidateType',
    'currentRoundTripTimeSeconds', 'availableOutgoingBitrate',
  ], label)
  enumValue(value.state, candidatePairStates, `${label}.state`)
  if (typeof value.nominated !== 'boolean') throw new Error(`${label}.nominated must be boolean`)
  enumValue(value.protocol, protocols, `${label}.protocol`)
  enumValue(value.localCandidateType, candidateTypes, `${label}.localCandidateType`)
  enumValue(value.remoteCandidateType, candidateTypes, `${label}.remoteCandidateType`)
  finiteNumber(value.currentRoundTripTimeSeconds, `${label}.currentRoundTripTimeSeconds`)
  finiteNumber(value.availableOutgoingBitrate, `${label}.availableOutgoingBitrate`)
  return value
}

function validateInboundAudio(raw, label) {
  const value = exactObject(raw, [
    'packetsReceived', 'packetsLost', 'bytesReceived', 'jitterSeconds',
    'totalAudioEnergy', 'concealedSamples',
  ], label)
  nonnegativeInteger(value.packetsReceived, `${label}.packetsReceived`)
  nonnegativeInteger(value.packetsLost, `${label}.packetsLost`)
  nonnegativeInteger(value.bytesReceived, `${label}.bytesReceived`)
  finiteNumber(value.jitterSeconds, `${label}.jitterSeconds`)
  finiteNumber(value.totalAudioEnergy, `${label}.totalAudioEnergy`)
  nonnegativeInteger(value.concealedSamples, `${label}.concealedSamples`)
  return value
}

function validateOutboundAudio(raw, label) {
  const value = exactObject(raw, ['packetsSent', 'bytesSent'], label)
  nonnegativeInteger(value.packetsSent, `${label}.packetsSent`)
  nonnegativeInteger(value.bytesSent, `${label}.bytesSent`)
  return value
}

function validateBrowserSample(raw, index, alias, window) {
  const label = `${alias}.samples[${index}]`
  const sample = exactObject(raw, [
    'timestamp', 'connectionState', 'iceConnectionState', 'signalingState',
    'unexpectedDisconnectCount', 'unexpectedSocketCloseCount', 'reconnectAttemptCount',
    'reconnectSuccessCount', 'maxRecoverySeconds', 'stats',
  ], label)
  const timestamp = timestampMillis(sample.timestamp, `${label}.timestamp`)
  if (timestamp < window.start || timestamp > window.end) throw new Error(`${label}.timestamp is outside the soak window`)
  enumValue(sample.connectionState, connectionStates, `${label}.connectionState`)
  enumValue(sample.iceConnectionState, iceStates, `${label}.iceConnectionState`)
  enumValue(sample.signalingState, signalingStates, `${label}.signalingState`)
  nonnegativeInteger(sample.unexpectedDisconnectCount, `${label}.unexpectedDisconnectCount`)
  nonnegativeInteger(sample.unexpectedSocketCloseCount, `${label}.unexpectedSocketCloseCount`)
  nonnegativeInteger(sample.reconnectAttemptCount, `${label}.reconnectAttemptCount`)
  nonnegativeInteger(sample.reconnectSuccessCount, `${label}.reconnectSuccessCount`)
  finiteNumber(sample.maxRecoverySeconds, `${label}.maxRecoverySeconds`)
  const stats = exactObject(sample.stats, ['candidatePair', 'inboundAudio', 'outboundAudio'], `${label}.stats`)
  validateCandidatePair(stats.candidatePair, `${label}.stats.candidatePair`)
  validateInboundAudio(stats.inboundAudio, `${label}.stats.inboundAudio`)
  validateOutboundAudio(stats.outboundAudio, `${label}.stats.outboundAudio`)
  return { ...sample, timestampMillis: timestamp }
}

function validateSeries(samples, label, window) {
  if (!Array.isArray(samples) || samples.length < 2) throw new Error(`${label} must contain at least two samples`)
  for (let index = 1; index < samples.length; index += 1) {
    if (samples[index].timestampMillis <= samples[index - 1].timestampMillis) throw new Error(`${label} timestamps must be strictly increasing`)
  }
  const startSkew = (samples[0].timestampMillis - window.start) / 1000
  const endSkew = (window.end - samples.at(-1).timestampMillis) / 1000
  const maximumGap = samples.slice(1).reduce((maximum, sample, index) => (
    Math.max(maximum, (sample.timestampMillis - samples[index].timestampMillis) / 1000)
  ), 0)
  return { startSkew, endSkew, maximumGap }
}

function validateMonotonic(samples, reader, label) {
  let previous = reader(samples[0])
  for (let index = 1; index < samples.length; index += 1) {
    const current = reader(samples[index])
    if (current < previous) throw new Error(`${label} must be cumulative and monotonic`)
    previous = current
  }
}

function addReason(reasons, condition, reason) {
  if (condition && !reasons.includes(reason)) reasons.push(reason)
}

function assessCoverage(reasons, prefix, coverage) {
  addReason(reasons, coverage.startSkew > FROZEN_THRESHOLDS.maximumEndpointSkewSeconds, `${prefix}_start_coverage_missing`)
  addReason(reasons, coverage.endSkew > FROZEN_THRESHOLDS.maximumEndpointSkewSeconds, `${prefix}_end_coverage_missing`)
  addReason(reasons, coverage.maximumGap > FROZEN_THRESHOLDS.maximumSampleGapSeconds, `${prefix}_sample_gap_exceeded`)
}

function assessContainer(rawSamples, binding, window, reasons) {
  if (!Array.isArray(rawSamples)) throw new Error('containerSamples must be an array')
  const samples = rawSamples.map((sample, index) => validateContainerSample(sample, index, binding, window))
  assessCoverage(reasons, 'container', validateSeries(samples, 'containerSamples', window))
  const first = samples[0]
  const last = samples.at(-1)
  for (const sample of samples) {
    addReason(reasons, sample.containerId !== first.containerId, 'container_identity_changed')
    addReason(reasons, sample.memoryLimitBytes !== FROZEN_THRESHOLDS.requiredMemoryLimitBytes, 'container_memory_limit_mismatch')
    addReason(reasons, sample.oomKilled, 'container_oom_killed')
  }
  validateMonotonic(samples, sample => sample.restartCount, 'container restartCount')
  validateMonotonic(samples, sample => sample.memoryPeakBytes, 'container memoryPeakBytes')
  validateMonotonic(samples, sample => sample.memoryEvents.oom, 'container memoryEvents.oom')
  validateMonotonic(samples, sample => sample.memoryEvents.oomKill, 'container memoryEvents.oomKill')
  const restartDelta = last.restartCount - first.restartCount
  const oomEventDelta = last.memoryEvents.oom - first.memoryEvents.oom
  const oomKillEventDelta = last.memoryEvents.oomKill - first.memoryEvents.oomKill
  const maximumMemoryLimitPercent = Math.max(...samples.map(sample => sample.memoryLimitBytes > 0
    ? Math.max(sample.memoryCurrentBytes, sample.memoryPeakBytes) / sample.memoryLimitBytes * 100
    : Number.POSITIVE_INFINITY))
  addReason(reasons, restartDelta > FROZEN_THRESHOLDS.maximumContainerRestartDelta, 'container_restart_delta_exceeded')
  addReason(reasons, oomEventDelta > FROZEN_THRESHOLDS.maximumOomEventDelta, 'container_oom_event_delta_exceeded')
  addReason(reasons, oomKillEventDelta > FROZEN_THRESHOLDS.maximumOomKillEventDelta, 'container_oom_kill_event_delta_exceeded')
  addReason(reasons, maximumMemoryLimitPercent > FROZEN_THRESHOLDS.maximumMemoryLimitPercent, 'container_memory_limit_percent_exceeded')
  return {
    containerId: first.containerId,
    imageId: first.imageId,
    sampleCount: samples.length,
    restartDelta,
    oomEventDelta,
    oomKillEventDelta,
    maximumCpuPercent: Math.max(...samples.map(sample => sample.cpuPercent)),
    maximumMemoryCurrentBytes: Math.max(...samples.map(sample => sample.memoryCurrentBytes)),
    maximumMemoryPeakBytes: Math.max(...samples.map(sample => sample.memoryPeakBytes)),
    maximumMemoryLimitPercent,
  }
}

function assessParticipant(raw, index, binding, window, reasons) {
  const label = `participants[${index}]`
  const participant = exactObject(raw, ['alias', 'roomId', 'sittingId', 'mediaGeneration', 'samples'], label)
  if (participant.alias !== participantAliases[index]) throw new Error(`${label}.alias must be ${participantAliases[index]}`)
  for (const key of ['roomId', 'sittingId', 'mediaGeneration']) {
    if (participant[key] !== binding[key]) throw new Error(`${label}.${key} does not match the exact incident binding`)
  }
  if (!Array.isArray(participant.samples)) throw new Error(`${label}.samples must be an array`)
  const samples = participant.samples.map((sample, sampleIndex) => validateBrowserSample(sample, sampleIndex, participant.alias, window))
  assessCoverage(reasons, participant.alias, validateSeries(samples, `${label}.samples`, window))
  const cumulativeReaders = {
    unexpectedDisconnectCount: sample => sample.unexpectedDisconnectCount,
    unexpectedSocketCloseCount: sample => sample.unexpectedSocketCloseCount,
    reconnectAttemptCount: sample => sample.reconnectAttemptCount,
    reconnectSuccessCount: sample => sample.reconnectSuccessCount,
    packetsReceived: sample => sample.stats.inboundAudio.packetsReceived,
    packetsLost: sample => sample.stats.inboundAudio.packetsLost,
    bytesReceived: sample => sample.stats.inboundAudio.bytesReceived,
    concealedSamples: sample => sample.stats.inboundAudio.concealedSamples,
    totalAudioEnergy: sample => sample.stats.inboundAudio.totalAudioEnergy,
    packetsSent: sample => sample.stats.outboundAudio.packetsSent,
    bytesSent: sample => sample.stats.outboundAudio.bytesSent,
  }
  for (const [name, reader] of Object.entries(cumulativeReaders)) validateMonotonic(samples, reader, `${participant.alias} ${name}`)
  const first = samples[0]
  const last = samples.at(-1)
  for (const sample of samples) {
    addReason(reasons, sample.connectionState !== 'connected', `${participant.alias}_connection_not_continuously_connected`)
    addReason(reasons, !['connected', 'completed'].includes(sample.iceConnectionState), `${participant.alias}_ice_not_continuously_connected`)
    addReason(reasons, sample.signalingState !== 'stable', `${participant.alias}_signaling_not_stable`)
    addReason(reasons, sample.stats.candidatePair.state !== 'succeeded', `${participant.alias}_selected_candidate_pair_not_succeeded`)
    addReason(reasons, !sample.stats.candidatePair.nominated, `${participant.alias}_selected_candidate_pair_missing`)
  }
  const delta = reader => reader(last) - reader(first)
  const unexpectedDisconnectDelta = delta(cumulativeReaders.unexpectedDisconnectCount)
  const unexpectedSocketCloseDelta = delta(cumulativeReaders.unexpectedSocketCloseCount)
  const reconnectAttemptDelta = delta(cumulativeReaders.reconnectAttemptCount)
  const reconnectSuccessDelta = delta(cumulativeReaders.reconnectSuccessCount)
  const inboundPackets = delta(cumulativeReaders.packetsReceived)
  const inboundPacketsLost = delta(cumulativeReaders.packetsLost)
  const outboundPackets = delta(cumulativeReaders.packetsSent)
  const packetLossPercent = inboundPackets + inboundPacketsLost > 0
    ? inboundPacketsLost / (inboundPackets + inboundPacketsLost) * 100
    : 100
  const maximumRecoverySeconds = Math.max(...samples.map(sample => sample.maxRecoverySeconds))
  addReason(reasons, unexpectedDisconnectDelta > FROZEN_THRESHOLDS.maximumUnexpectedDisconnectDelta, `${participant.alias}_unexpected_disconnect_delta_exceeded`)
  addReason(reasons, unexpectedSocketCloseDelta > FROZEN_THRESHOLDS.maximumUnexpectedSocketCloseDelta, `${participant.alias}_unexpected_socket_close_delta_exceeded`)
  addReason(reasons, reconnectAttemptDelta !== reconnectSuccessDelta, `${participant.alias}_recovery_incomplete`)
  addReason(reasons, maximumRecoverySeconds > FROZEN_THRESHOLDS.maximumRecoverySeconds, `${participant.alias}_recovery_time_exceeded`)
  addReason(reasons, packetLossPercent > FROZEN_THRESHOLDS.maximumPacketLossPercent, `${participant.alias}_packet_loss_exceeded`)
  addReason(reasons, inboundPackets < FROZEN_THRESHOLDS.minimumInboundAudioPacketDeltaPerParticipant, `${participant.alias}_inbound_audio_coverage_insufficient`)
  addReason(reasons, outboundPackets < FROZEN_THRESHOLDS.minimumOutboundAudioPacketDeltaPerParticipant, `${participant.alias}_outbound_audio_coverage_insufficient`)
  return {
    alias: participant.alias,
    sampleCount: samples.length,
    unexpectedDisconnectDelta,
    unexpectedSocketCloseDelta,
    reconnectAttemptDelta,
    reconnectSuccessDelta,
    maximumRecoverySeconds,
    inboundAudioPacketDelta: inboundPackets,
    inboundAudioPacketLossDelta: inboundPacketsLost,
    outboundAudioPacketDelta: outboundPackets,
    packetLossPercent,
    maximumRoundTripTimeSeconds: Math.max(...samples.map(sample => sample.stats.candidatePair.currentRoundTripTimeSeconds)),
    minimumAvailableOutgoingBitrate: Math.min(...samples.map(sample => sample.stats.candidatePair.availableOutgoingBitrate)),
    candidatePaths: [...new Set(samples.map(sample => [
      sample.stats.candidatePair.localCandidateType,
      sample.stats.candidatePair.remoteCandidateType,
      sample.stats.candidatePair.protocol,
    ].join(':')))].sort(),
  }
}

export function assessRoomDisconnectSoak(input) {
  const root = exactObject(input, ['schema', 'binding', 'window', 'thresholds', 'containerSamples', 'participants'], 'input')
  if (root.schema !== INPUT_SCHEMA) throw new Error(`input.schema must be ${INPUT_SCHEMA}`)
  const binding = validateBinding(root.binding)
  const window = validateWindow(root.window)
  validateFrozenThresholds(root.thresholds)
  if (!Array.isArray(root.participants) || root.participants.length !== FROZEN_THRESHOLDS.expectedParticipantCount) {
    throw new Error(`participants must contain exactly ${FROZEN_THRESHOLDS.expectedParticipantCount} content-free participant streams`)
  }
  const reasons = []
  addReason(reasons, window.durationSeconds < FROZEN_THRESHOLDS.minimumDurationSeconds, 'minimum_duration_not_met')
  const container = assessContainer(root.containerSamples, binding, window, reasons)
  const participants = root.participants.map((participant, index) => assessParticipant(participant, index, binding, window, reasons))
  return {
    schema: ASSESSMENT_SCHEMA,
    assessmentMode: 'offline_supplied_evidence_only',
    incidentScopeOnly: true,
    formalMediaSoakQualification: false,
    safety: SAFETY_CONTRACT,
    binding: { ...binding },
    thresholds: { ...FROZEN_THRESHOLDS },
    observed: {
      startedAt: root.window.startedAt,
      endedAt: root.window.endedAt,
      durationSeconds: window.durationSeconds,
      container,
      participants,
    },
    pass: reasons.length === 0,
    reasons,
  }
}

async function readSafeLocalJSON(path) {
  if (!isAbsolute(path) || resolve(path) !== path) throw new Error('input path must be absolute')
  const stat = await lstat(path)
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 4 * 1024 * 1024) throw new Error('input must be a regular non-symlink JSON file no larger than 4 MiB')
  return JSON.parse(await readFile(path, 'utf8'))
}

function usage() {
  return [
    'Offline incident-soak assessment is disabled by default.',
    'Usage: node scripts/room-disconnect-soak.mjs --offline-evaluate <absolute-json-path> --acknowledge-pre-recorded-input',
    'Reads one local content-free evidence file and writes only the assessment JSON to stdout.',
    'It never joins a room, accesses a network, mutates production, writes evidence, or qualifies the formal W2A media soak.',
  ].join('\n')
}

async function main(argv) {
  if (argv.length === 1 && ['--help', '-h'].includes(argv[0])) {
    process.stdout.write(`${usage()}\n`)
    return
  }
  if (argv.length !== 3 || argv[0] !== '--offline-evaluate' || argv[2] !== '--acknowledge-pre-recorded-input') {
    throw new Error(usage())
  }
  const assessment = assessRoomDisconnectSoak(await readSafeLocalJSON(argv[1]))
  process.stdout.write(`${JSON.stringify(assessment)}\n`)
  if (!assessment.pass) process.exitCode = 1
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) {
  main(process.argv.slice(2)).catch(error => {
    process.stderr.write(`${JSON.stringify({ pass: false, error: String(error?.message || error) })}\n`)
    process.exitCode = 1
  })
}
