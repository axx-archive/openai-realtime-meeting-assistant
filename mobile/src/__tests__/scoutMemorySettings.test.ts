import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('native settings exposes inspectable, importable, and corrigible employee memory', () => {
  const settings = source('src', 'screens', 'SettingsScreen.tsx');
  const memory = source('src', 'components', 'ScoutMemorySettings.tsx');

  assert.match(settings, /<ScoutMemorySettings sessionToken=\{sessionToken\}/);
  for (const phrase of [
    'What STRIDE remembers about me',
	'Import memory to STRIDE',
	'Copy prompt',
	'Imported by you in Settings',
    'Turn on private memory',
	'Learn from repeated patterns · not active yet',
	'Shared-channel preferences · not active yet',
	'No conversation is inferred into a preference in this build.',
	'Channel conversations are not currently turned into preferences by the app.',
    'Remember privately',
    'Added by you in Settings',
    'Inferred from repeated patterns',
    'Expires',
    'Correct',
	'Forget',
	'View source',
    'Turn off & remove',
  ]) {
    assert.match(memory, new RegExp(phrase.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  }
  assert.match(memory, /preference\.scope === 'shared' \? 'Shared' : 'Private'/);
  assert.match(memory, /paused \? ' · Paused'/);
  assert.match(memory, /Nothing is being learned or stored/);
  assert.match(memory, /if \(!sessionToken\) \{[\s\S]{0,160}setAvailability\('signed-out'\)/);
	assert.match(memory, /sessionToken \? 'loading' : 'signed-out'/);
	assert.match(memory, /const loadEpoch = useRef\(0\)/);
	assert.match(memory, /identity !== sessionTokenRef\.current/);
  assert.match(memory, /chip: \{ minHeight: hitMin/);
	assert.match(memory, /actionButton: \{ minHeight: hitMin/);
	assert.match(memory, /toggleRow: \{ minHeight: 54/);
  assert.match(memory, /onPress=\{confirmDisable\} style=\{styles\.actionButton\}/);
	assert.match(memory, /disabled=\{busy !== null\} onPress=\{\(\) => confirmForget\(preference\)\}/);
	assert.match(memory, /await load\(\);\s*fail\(caught\)/);
	assert.match(memory, /const busyRef = useRef<\{ key: string; identity: string \} \| null>\(null\)/);
	assert.match(memory, /if \(busyRef\.current\) return null/);
	assert.match(memory, /if \(busyRef\.current !== claim\) return/);
	assert.match(memory, /office\.event !== 'relationship_memory_changed'/);
  assert.match(memory, /closeStalePersonalRealtime\(staleVoiceWasActive\)/);
	assert.match(memory, /parseSTRIDEMemoryImport/);
	assert.match(memory, /\[YYYY-MM-DD\] - Entry/);
	assert.match(memory, /strideImportRelationships/);
	assert.match(memory, /expectedRevision: state\.revision/);
	assert.match(memory, /Memory is updating/);
	assert.match(memory, /Saved until you remove it/);
	assert.doesNotMatch(memory, /String\(slot\)\.padStart\(2, '0'\)/);
	assert.match(memory, /No valid memories yet/);
	assert.match(memory, /Do not include credentials, payment data, medical information/);
});

test('native relationship memory uses authenticated private control endpoints', () => {
  const client = source('src', 'api', 'client.ts');
  const types = source('src', 'api', 'types.ts');

  assert.match(client, /strideRelationshipMemory\(\s*sessionToken: string,?\s*\)/);
  assert.match(client, /strideSetRelationshipConsent/);
  assert.match(client, /strideRememberRelationship/);
	assert.match(client, /strideImportRelationships/);
	assert.match(client, /relationships\/import/);
  assert.match(client, /strideCorrectRelationship/);
  assert.match(client, /strideForgetRelationship/);
  assert.match(client, /scope: ["']private["']/);
  assert.doesNotMatch(client, /strideRememberRelationship[\s\S]{0,500}threadId/);
  assert.match(types, /export type StrideRelationshipMemoryResponse/);
  assert.match(types, /scope: 'private' \| 'shared'/);
	assert.match(types, /threadId\?: string/);
	assert.match(types, /messageId\?: string/);
});

test('relationship memory invalidation globally closes stale personal Realtime', () => {
  const office = source('src', 'realtime', 'OfficeEventsContext.tsx');

  assert.match(office, /nested\.event === 'relationship_memory_changed'/);
  assert.match(office, /audioFocusRuntime\.mode === 'personal_realtime'/);
  assert.match(office, /audioFocusRuntime\.forceClose\('forced_close'\)/);
});
