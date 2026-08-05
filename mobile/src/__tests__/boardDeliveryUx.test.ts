import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('mobile Board mirrors the three-stage delivery projection and project filter', () => {
  const board = source('src', 'screens', 'BoardScreen.tsx');
  const types = source('src', 'api', 'types.ts');

  assert.match(board, /\{ id: 'requested', label: 'Work requested' \}/);
  assert.match(board, /\{ id: 'delivered', label: 'Work delivered' \}/);
  assert.match(board, /\{ id: 'drive', label: 'Saved to Drive' \}/);
  assert.match(board, /Filter Board by project/);
  assert.match(board, /row\?\.projectId === projectFilter/);
  assert.match(types, /deliveryStage: 'requested' \| 'delivered' \| 'drive'/);
  assert.match(types, /projectResolution: 'linked' \| 'tag' \| 'missing'/);
});
