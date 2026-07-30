import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');

test('expanded shortcut cluster owns the full visible touch region', () => {
  const source = fs.readFileSync(
    path.join(mobileRoot, 'src', 'components', 'NavCluster.tsx'),
    'utf8',
  );

  assert.match(source, /const expandedWidth = hitMin[\s\S]*destinations\.length \* ITEM_WIDTH/);
  assert.match(source, /open && \{ width: expandedWidth, height: hitMin \+ ITEM_LABEL_HEIGHT \}/);
  assert.match(source, /style=\{\[styles\.container, open && styles\.containerOpen\]\}/);
  assert.match(source, /containerOpen: \{[\s\S]*width: '100%'[\s\S]*height: '100%'/);
  assert.match(source, /!open && styles\.inert/);
});

test('canvas shortcuts distinguish the room list from room creation', () => {
  const source = fs.readFileSync(
    path.join(mobileRoot, 'src', 'screens', 'CanvasScreen.tsx'),
    'utf8',
  );

  assert.match(
    source,
    /id: 'rooms',[\s\S]*label: 'Rooms',[\s\S]*navigation\.navigate\('Deck', \{ segment: 'rooms' \}\)/,
  );
  assert.match(
    source,
    /id: 'new-room',[\s\S]*label: 'New',[\s\S]*navigation\.navigate\('CreateRoom'\)/,
  );
  assert.doesNotMatch(source, /label: 'Live'/);
  assert.match(source, /<View style=\{styles\.navOverlay\} pointerEvents="box-none">/);
  assert.match(source, /navOverlay: \{[\s\S]*position: 'absolute',[\s\S]*bottom: 0/);
});
