import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const canvas = readFileSync(path.join(mobileRoot, 'src', 'screens', 'CanvasScreen.tsx'), 'utf8');
test('home is conversation-first through voice or direct text with no picker', () => {
  assert.match(canvas, /usePersonalRealtime/);
  assert.match(canvas, /useComposerDictation/);
  assert.match(canvas, /Start a new private voice chat with Scout/);
  assert.match(canvas, /submitHomeScoutOpening/);
  assert.match(canvas, /api\.createScoutThread/);
  assert.match(canvas, /placeholder="Message Scout"/);
  assert.match(canvas, /openingAttemptRef/);
  assert.match(canvas, /realtime\.stop\('cancelled'\)/);
  assert.match(canvas, /<Text maxFontSizeMultiplier=\{1\.35\} style=\{styles\.greeting\}>/);
  assert.match(canvas, /maxFontSizeMultiplier=\{1\.6\}[\s\S]*?style=\{styles\.composerInput\}/);
  assert.match(canvas, /<Text maxFontSizeMultiplier=\{1\} style=\{styles\.composerSendGlyph\}>/);
  assert.doesNotMatch(canvas, /toolTemplate|deliverable picker/);
});

test('the home surface does not render a question-answer transcript', () => {
  assert.doesNotMatch(canvas, /voiceTurn \?/);
  assert.doesNotMatch(canvas, /styles\.question|styles\.answer/);
});
