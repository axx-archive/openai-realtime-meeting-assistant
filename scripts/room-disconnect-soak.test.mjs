import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import {
  ASSESSMENT_SCHEMA,
  FROZEN_THRESHOLDS,
  INPUT_SCHEMA,
  SAFETY_CONTRACT,
  assessRoomDisconnectSoak,
} from './room-disconnect-soak.mjs'

const scriptPath = fileURLToPath(new URL('./room-disconnect-soak.mjs', import.meta.url))
const startedAt = Date.parse('2026-08-14T18:00:00.000Z')
const gibibyte = 1024 * 1024 * 1024

function timestamp(index) {
  return new Date(startedAt + index * 30_000).toISOString()
}

function browserSample(index) {
  return {
    timestamp: timestamp(index),
    connectionState: 'connected',
    iceConnectionState: index % 2 === 0 ? 'connected' : 'completed',
    signalingState: 'stable',
    unexpectedDisconnectCount: 0,
    unexpectedSocketCloseCount: 0,
    reconnectAttemptCount: 0,
    reconnectSuccessCount: 0,
    maxRecoverySeconds: 0,
    stats: {
      candidatePair: {
        state: 'succeeded',
        nominated: true,
        protocol: 'udp',
        localCandidateType: 'srflx',
        remoteCandidateType: 'relay',
        currentRoundTripTimeSeconds: 0.04 + index / 100_000,
        availableOutgoingBitrate: 1_000_000 - index,
      },
      inboundAudio: {
        packetsReceived: index * 100,
        packetsLost: Math.floor(index / 10),
        bytesReceived: index * 12_000,
        jitterSeconds: 0.004,
        totalAudioEnergy: index / 10,
        concealedSamples: index,
      },
      outboundAudio: {
        packetsSent: index * 101,
        bytesSent: index * 12_100,
      },
    },
  }
}

function containerSample(index, binding) {
  return {
    timestamp: timestamp(index),
    containerId: '1'.repeat(64),
    imageId: binding.serviceImageId,
    restartCount: 7,
    oomKilled: false,
    cpuPercent: 12 + index / 100,
    memoryCurrentBytes: Math.floor(0.5 * gibibyte + index * 1024),
    memoryPeakBytes: Math.floor(0.7 * gibibyte),
    memoryLimitBytes: 3 * gibibyte,
    memoryEvents: { oom: 2, oomKill: 1 },
  }
}

function fixture() {
  const binding = {
    releaseCommit: 'a'.repeat(40),
    activeReleaseDirectory: `/opt/meetingassist-releases/${'a'.repeat(40)}`,
    releaseReceiptSha256: 'b'.repeat(64),
    serviceImageId: `sha256:${'c'.repeat(64)}`,
    roomId: 'room-incident-17',
    sittingId: 'sitting-42',
    mediaGeneration: 'generation-9',
  }
  return {
    schema: INPUT_SCHEMA,
    binding,
    window: { startedAt: timestamp(0), endedAt: timestamp(240) },
    thresholds: { ...FROZEN_THRESHOLDS },
    containerSamples: Array.from({ length: 241 }, (_, index) => containerSample(index, binding)),
    participants: ['participant-a', 'participant-b'].map(alias => ({
      alias,
      roomId: binding.roomId,
      sittingId: binding.sittingId,
      mediaGeneration: binding.mediaGeneration,
      samples: Array.from({ length: 241 }, (_, index) => browserSample(index)),
    })),
  }
}

test('passes a fully bound two-person offline incident assessment without claiming formal qualification', () => {
  const input = fixture()
  const result = assessRoomDisconnectSoak(input)
  assert.equal(result.schema, ASSESSMENT_SCHEMA)
  assert.equal(result.pass, true)
  assert.deepEqual(result.reasons, [])
  assert.equal(result.binding.releaseCommit, input.binding.releaseCommit)
  assert.equal(result.binding.roomId, input.binding.roomId)
  assert.equal(result.binding.sittingId, input.binding.sittingId)
  assert.equal(result.binding.mediaGeneration, input.binding.mediaGeneration)
  assert.equal(result.observed.durationSeconds, 7200)
  assert.equal(result.observed.participants.length, 2)
  assert.equal(result.formalMediaSoakQualification, false)
  assert.deepEqual(result.safety, SAFETY_CONTRACT)
})

test('fails closed when frozen thresholds or exact release and media bindings differ', () => {
  const changedThreshold = fixture()
  changedThreshold.thresholds.maximumPacketLossPercent = 3
  assert.throws(() => assessRoomDisconnectSoak(changedThreshold), /exactly match the frozen/)

  const changedRelease = fixture()
  changedRelease.binding.activeReleaseDirectory = `/opt/meetingassist-releases/${'d'.repeat(40)}`
  assert.throws(() => assessRoomDisconnectSoak(changedRelease), /exact release commit/)

  const changedGeneration = fixture()
  changedGeneration.participants[1].mediaGeneration = 'generation-10'
  assert.throws(() => assessRoomDisconnectSoak(changedGeneration), /does not match the exact incident binding/)
})

test('strict content-free schemas reject messages, raw candidates, and identity fields', () => {
  const message = fixture()
  message.participants[0].samples[0].message = 'content must never enter evidence'
  assert.throws(() => assessRoomDisconnectSoak(message), /unexpected or missing fields/)

  const rawCandidate = fixture()
  rawCandidate.participants[0].samples[0].stats.candidatePair.address = '192.0.2.4'
  assert.throws(() => assessRoomDisconnectSoak(rawCandidate), /unexpected or missing fields/)

  const identity = fixture()
  identity.participants[0].displayName = 'Person Name'
  assert.throws(() => assessRoomDisconnectSoak(identity), /unexpected or missing fields/)
})

test('reports deterministic restart, OOM, memory, disconnect, recovery, and packet-loss failures', () => {
  const input = fixture()
  const lastContainer = input.containerSamples.at(-1)
  lastContainer.restartCount += 1
  lastContainer.memoryEvents.oom += 1
  lastContainer.memoryEvents.oomKill += 1
  lastContainer.memoryCurrentBytes = Math.floor(2.5 * gibibyte)
  lastContainer.memoryPeakBytes = Math.floor(2.5 * gibibyte)
  const lastParticipant = input.participants[0].samples.at(-1)
  lastParticipant.unexpectedDisconnectCount = 1
  lastParticipant.unexpectedSocketCloseCount = 1
  lastParticipant.reconnectAttemptCount = 2
  lastParticipant.reconnectSuccessCount = 1
  lastParticipant.maxRecoverySeconds = 21
  lastParticipant.stats.inboundAudio.packetsLost = 1_000
  const result = assessRoomDisconnectSoak(input)
  assert.equal(result.pass, false)
  assert.deepEqual(result.reasons, [
    'container_restart_delta_exceeded',
    'container_oom_event_delta_exceeded',
    'container_oom_kill_event_delta_exceeded',
    'container_memory_limit_percent_exceeded',
    'participant-a_unexpected_disconnect_delta_exceeded',
    'participant-a_unexpected_socket_close_delta_exceeded',
    'participant-a_recovery_incomplete',
    'participant-a_recovery_time_exceeded',
    'participant-a_packet_loss_exceeded',
  ])
})

test('fails when peak memory alone exceeds the frozen container threshold', () => {
  const input = fixture()
  input.containerSamples.at(-1).memoryPeakBytes = Math.floor(2.9 * gibibyte)
  const result = assessRoomDisconnectSoak(input)
  assert.equal(result.pass, false)
  assert.deepEqual(result.reasons, ['container_memory_limit_percent_exceeded'])
  assert.ok(result.observed.container.maximumMemoryLimitPercent > 96)
})

test('requires continuous connected state and complete sample coverage', () => {
  const input = fixture()
  input.containerSamples.splice(100, 1)
  input.participants[1].samples[120].connectionState = 'disconnected'
  input.participants[1].samples[120].iceConnectionState = 'disconnected'
  input.participants[1].samples[120].signalingState = 'have-local-offer'
  input.participants[1].samples[120].stats.candidatePair.state = 'failed'
  input.participants[1].samples[120].stats.candidatePair.nominated = false
  const result = assessRoomDisconnectSoak(input)
  assert.equal(result.pass, false)
  assert.deepEqual(result.reasons, [
    'container_sample_gap_exceeded',
    'participant-b_connection_not_continuously_connected',
    'participant-b_ice_not_continuously_connected',
    'participant-b_signaling_not_stable',
    'participant-b_selected_candidate_pair_not_succeeded',
    'participant-b_selected_candidate_pair_missing',
  ])
})

test('the executable is default-off and contains no collection, network, room-join, or write primitive', () => {
  const invocation = spawnSync(process.execPath, [scriptPath], { encoding: 'utf8' })
  assert.equal(invocation.status, 1)
  assert.match(invocation.stderr, /disabled by default/)

  const source = readFileSync(scriptPath, 'utf8')
  for (const forbidden of [
    /\bfetch\s*\(/,
    /\bWebSocket\b/,
    /node:https?/,
    /node:child_process/,
    /\bwriteFile\b/,
    /\bappendFile\b/,
    /\bmkdir\b/,
    /\brename\b/,
  ]) assert.doesNotMatch(source, forbidden)
  assert.deepEqual(SAFETY_CONTRACT, {
    collection: 'none',
    inputMode: 'pre_recorded_content_free_evidence_only',
    networkAccess: 'forbidden',
    productionMutation: 'forbidden',
    roomJoin: 'forbidden',
    filesystemWrites: 'none',
    formalMediaSoakQualification: false,
  })
})
