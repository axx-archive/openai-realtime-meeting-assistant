import assert from 'node:assert/strict'
import test from 'node:test'

import {
  assertDequalificationIdleReadiness,
  privateRealtimeVoiceDequalificationEnvPatch,
  validateDequalificationJournal
} from './private-realtime-dequalification-bridge.mjs'

test('dequalification patch changes only one canonical true assignment and preserves bytes', () => {
  const before = Buffer.from('A=1\r\nPRIVATE_REALTIME_VOICE_QUALIFIED=true\r\nB=two\r\n')
  const patch = privateRealtimeVoiceDequalificationEnvPatch(before)
  assert.equal(patch.after.toString(), 'A=1\r\nPRIVATE_REALTIME_VOICE_QUALIFIED=false\r\nB=two\r\n')
  assert.notEqual(patch.beforeSha256, patch.afterSha256)
  assert.deepEqual(patch.before, before)
  for (const invalid of [
    'A=1\n',
    'PRIVATE_REALTIME_VOICE_QUALIFIED=false\n',
    'PRIVATE_REALTIME_VOICE_QUALIFIED =true\n',
    'PRIVATE_REALTIME_VOICE_QUALIFIED=true\nPRIVATE_REALTIME_VOICE_QUALIFIED=true\n'
  ]) assert.throws(() => privateRealtimeVoiceDequalificationEnvPatch(invalid), /dequalification requires|canonical/)
})

test('dequalification requires an idle room and realtime inventory', () => {
  const idle = {
    ok: true,
    capabilities: { brain: { rooms: [{ participants: 0, sittingId: '', media: { active: false, actor: false, mixer: false } }] } },
    checks: { realtime: { connected: false } }
  }
  assert.equal(assertDequalificationIdleReadiness(idle), true)
  assert.throws(() => assertDequalificationIdleReadiness({ ...idle, ok: false }), /readiness/)
  assert.throws(() => assertDequalificationIdleReadiness({ ok: true, capabilities: { brain: {} } }), /room inventory/)
  assert.throws(() => assertDequalificationIdleReadiness({
    ...idle, capabilities: { brain: { rooms: [{ participants: 1, media: { active: true } }] } }
  }), /active/)
  assert.throws(() => assertDequalificationIdleReadiness({ ...idle, checks: { realtime: { connected: true } } }), /Realtime/)
})

test('dequalification journal is exact and digest bound', () => {
  const token = '12345678-1234-4234-8234-123456789abc'
  const journal = {
    schema: 'bonfire.private-realtime-dequalification-bridge.v1',
    token,
    activeReleaseCommit: 'a'.repeat(40),
    previousReleaseCommit: 'b'.repeat(40),
    generation: 225,
    baseEnvPath: '/opt/meetingassist/deploy/digitalocean/.env',
    backupPath: `/opt/meetingassist-backups/private-realtime-dequalification-${token}.base-env`,
    receiptPath: `/opt/meetingassist-backups/private-realtime-dequalification-${token}.receipt.json`,
    beforeSha256: 'c'.repeat(64),
    afterSha256: 'd'.repeat(64),
    healthUrl: 'https://thebonfire.xyz/healthz',
    readyUrl: 'https://thebonfire.xyz/readyz',
    phase: 'prepared',
    createdAt: '2026-08-29T00:00:00.000Z',
    updatedAt: '2026-08-29T00:00:00.000Z'
  }
  assert.deepEqual(validateDequalificationJournal(journal), journal)
  assert.throws(() => validateDequalificationJournal({ ...journal, extra: true }), /journal is invalid/)
  assert.throws(() => validateDequalificationJournal({ ...journal, phase: 'committed' }), /journal is invalid/)
  assert.throws(() => validateDequalificationJournal({ ...journal, beforeSha256: 'bad' }), /journal is invalid/)
})
