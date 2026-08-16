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

test('Canvas has no duplicate shortcut band competing with the universal shell', () => {
  const canvas = fs.readFileSync(path.join(mobileRoot, 'src', 'screens', 'CanvasScreen.tsx'), 'utf8');
  const shell = fs.readFileSync(path.join(mobileRoot, 'src', 'navigation', 'NativeUniversalShell.tsx'), 'utf8');
  assert.doesNotMatch(canvas, /<ChatCircle|<NavCluster|styles\.navRow|composerDock/);
  assert.match(shell, /compactItem: \{[\s\S]*minHeight: 48/);
  assert.match(shell, /bottomRail: \{[\s\S]*minHeight: 58/);
  assert.match(shell, /compactItemSelected: \{ backgroundColor: colors\.accentSoft \}/);
  // Slim rail design: icon-only marks without labels (STRIDE mobile E2E evolution)
  assert.match(shell, /DestMark/);
});
