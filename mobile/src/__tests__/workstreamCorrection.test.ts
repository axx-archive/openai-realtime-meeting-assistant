import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('native chat has no composer-level Project attachment affordance', () => {
  const preflight = source('src', 'messaging', 'projectContextPreflight.ts');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  const reply = source('src', 'messaging', 'ThreadDetailSheet.tsx');
  assert.match(preflight, /explicitProjectAttachmentEnabled = false/u);
  assert.doesNotMatch(thread, /Add project/u);
  assert.doesNotMatch(reply, /Add project/u);
});

test('native Work result owns the only human Project correction control', () => {
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  const sheet = source('src', 'messaging', 'ProjectCorrectionSheet.tsx');
  assert.match(bubble, /Corrects the Work result and future continuity without changing the source conversation/u);
  assert.match(bubble, /onChangeWorkProject\(message/u);
  assert.match(thread, /api\.workstreamCorrection\(sessionToken, target\.artifactId\)/u);
  assert.match(thread, /api\.updateWorkstreamCorrection\(sessionToken, target\.artifactId/u);
  assert.match(thread, /subject="work"/u);
  assert.match(sheet, /This corrects this Work and future continuity\. The source conversation stays unchanged\./u);
});

test('Work correction transport carries only the opaque token and stable operation id', () => {
  const client = source('src', 'api', 'client.ts');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(client, /updateWorkstreamCorrection[\s\S]*body: \{ operationId: string; correctionToken: string \}/u);
  assert.doesNotMatch(client, /updateWorkstreamCorrection[\s\S]{0,650}projectId/u);
  assert.match(thread, /workstreamCorrectionAttemptRef\.current\?\.key === attemptKey/u);
  assert.match(thread, /operationId: createConversationOperationId\(\)/u);
});
