import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { parseStridePersonalContextExport, parseStridePersonalContextSource, parseStridePersonalContextSources } from '../personalContext/parser';

const source = {
  personId: 'person_one', sourceId: 'source_one', revision: 1, kind: 'preference', body: 'Lead with the recommendation.',
  bodyDigest: 'a'.repeat(64), consentRevision: 1, updatedAt: '2026-08-09T12:00:00Z',
};

test('personal context parser admits only exact private source and export envelopes', () => {
  assert.deepEqual(parseStridePersonalContextSource(source), source);
  assert.deepEqual(parseStridePersonalContextSources([source]), [source]);
  assert.deepEqual(parseStridePersonalContextExport({ personId: 'person_one', exportedAt: '2026-08-09T12:01:00Z', sources: [source], manifestDigest: 'b'.repeat(64) }).sources, [source]);
});

test('personal context parser rejects authority extras, duplicates, malformed digests, and oversized bodies', () => {
  assert.throws(() => parseStridePersonalContextSource({ ...source, organizationId: 'org_one' }));
  assert.throws(() => parseStridePersonalContextSources([source, source]));
  assert.throws(() => parseStridePersonalContextSource({ ...source, bodyDigest: 'not-a-digest' }));
  assert.throws(() => parseStridePersonalContextSource({ ...source, body: 'x'.repeat(16_385) }));
  assert.throws(() => parseStridePersonalContextExport({ personId: 'person_one', exportedAt: 'now', sources: [], manifestDigest: 'b'.repeat(64) }));
});

test('personal context Settings uses direct private custody routes without client persistence', () => {
  const root = path.resolve(import.meta.dirname, '..');
  const component = fs.readFileSync(path.join(root, 'components', 'PersonalContextSettings.tsx'), 'utf8');
  const client = fs.readFileSync(path.join(root, 'api', 'client.ts'), 'utf8');
  for (const route of ['/api/mymind/v1/sources', '/api/mymind/v1/export']) assert.match(client, new RegExp(route.replaceAll('/', '\\/')));
  assert.match(component, /Private context you control/);
  assert.match(component, /source encryption key/);
  assert.doesNotMatch(component, /AsyncStorage|SecureStore|StrideMutationLedger|mymind/i);
});
