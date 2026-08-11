import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const canvas = readFileSync(path.join(mobileRoot, 'src', 'screens', 'CanvasScreen.tsx'), 'utf8');
test('home is voice-first and delegates typed conversation creation to Work', () => {
  assert.match(canvas, /usePersonalRealtime/);
  assert.match(canvas, /<StrideCradle/);
  assert.doesNotMatch(canvas, /api\.createScoutThread|Message Scout|composerDock|useComposerDictation/);
});

test('the home surface does not render a question-answer transcript', () => {
  assert.doesNotMatch(canvas, /voiceTurn \?/);
  assert.doesNotMatch(canvas, /styles\.question|styles\.answer/);
});
