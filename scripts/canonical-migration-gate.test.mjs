import assert from 'node:assert/strict'
import test from 'node:test'

import {
  assertMigrationIdleReadiness,
  canonicalMigrationGatePatch,
  validateMigrationProof
} from './canonical-migration-gate.mjs'

test('migration gate advances exactly one canonical assignment and preserves bytes', () => {
  const before = Buffer.from('A=1\r\nBONFIRE_CANONICAL_MIGRATION_MAX_VERSION=24\r\nB=2\r\n')
  const patch = canonicalMigrationGatePatch(before, 24, 25)
  assert.equal(patch.after.toString(), 'A=1\r\nBONFIRE_CANONICAL_MIGRATION_MAX_VERSION=25\r\nB=2\r\n')
  assert.throws(() => canonicalMigrationGatePatch(before, 23, 25), /exactly one/)
  assert.throws(() => canonicalMigrationGatePatch(Buffer.from(`${before}BONFIRE_CANONICAL_MIGRATION_MAX_VERSION=24\n`), 24, 25), /exactly one canonical/)
})

test('migration gate requires idle rooms and disconnected shared realtime', () => {
  assert.equal(assertMigrationIdleReadiness({ ok: true, capabilities: { rooms: [{ participants: 0, media: { active: false } }] }, checks: { realtime: { connected: false } } }), true)
  assert.throws(() => assertMigrationIdleReadiness({ ok: true, capabilities: { rooms: [{ participants: 1, media: {} }] }, checks: {} }), /room/)
  assert.throws(() => assertMigrationIdleReadiness({ ok: true, capabilities: { rooms: [] }, checks: { realtime: { connected: true } } }), /Realtime/)
})

test('migration proof binds the installed checksum and SourceEpisode schema', () => {
  const digest = 'a'.repeat(64)
  assert.equal(validateMigrationProof({ version: 25, sha256: digest, migrationCount: 25, sourceEpisodeTables: 4, sourceEpisodeTriggers: 4 }, 25, digest).version, 25)
  assert.throws(() => validateMigrationProof({ version: 25, sha256: digest, migrationCount: 25, sourceEpisodeTables: 3, sourceEpisodeTriggers: 4 }, 25, digest), /incomplete/)
})
