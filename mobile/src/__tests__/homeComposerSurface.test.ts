import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const canvas = readFileSync(path.join(mobileRoot, 'src', 'screens', 'CanvasScreen.tsx'), 'utf8');
const client = readFileSync(path.join(mobileRoot, 'src', 'api', 'client.ts'), 'utf8');

test('home sends one idempotent opening turn and navigates without a message deep link', () => {
  assert.match(client, /headers: buildIdempotencyHeaders\(idempotencyKey\)/);
  assert.match(canvas, /api\.createScoutThread\([\s\S]*sessionToken,[\s\S]*body,[\s\S]*idempotencyKey/);
  const acceptance = canvas.slice(canvas.indexOf('if (result.accepted)'), canvas.indexOf('} else {', canvas.indexOf('if (result.accepted)')));
  assert.match(acceptance, /setComposerDraft\(''\)/);
  assert.match(acceptance, /navigation\.navigate\('Thread'/);
  assert.doesNotMatch(acceptance, /messageId/);
});

test('the home surface does not render a question-answer transcript', () => {
  assert.doesNotMatch(canvas, /voiceTurn \?/);
  assert.doesNotMatch(canvas, /styles\.question|styles\.answer/);
});
