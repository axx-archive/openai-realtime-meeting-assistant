import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');

function source(...parts: string[]): string {
  return fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');
}

test('native chat renders authenticated generated images with durable actions', () => {
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');
  assert.match(bubble, /messageKind === 'image_pending'/);
  assert.match(bubble, /Generating image…/);
  assert.match(bubble, /authenticatedFileUrl\(generatedImage\)/);
  assert.match(bubble, /authenticatedFileHeaders\(sessionToken, generatedImage\.mime\)/);
  assert.match(bubble, /cachePolicy="memory-disk"/);
  assert.match(bubble, /recyclingKey=\{generatedImage\.ref\}/);
  assert.match(bubble, /Save to Drive/);
  assert.match(bubble, /Edit prompt and regenerate image/);
});

test('native image actions call the authorized server routes and reconcile the thread', () => {
  const client = source('src', 'api', 'client.ts');
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(client, /messages\/\$\{encodeURIComponent\(messageId\)\}\/regenerate/);
  assert.match(client, /["']\/assistant\/files\/save["']/);
  assert.match(screen, /api\.saveArtifactToFiles\(sessionToken, artifactID\)/);
  assert.match(screen, /api\.regenerateScoutImage\(/);
  assert.match(screen, /Alert\.prompt\(/);
  assert.match(screen, /applyTranscriptSnapshot\(generationAtRequest/);
});
