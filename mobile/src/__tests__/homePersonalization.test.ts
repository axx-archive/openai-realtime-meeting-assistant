import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';

import { homePreferredName, personalizedHomeGreeting } from '../canvas/homeGreeting';

test('Home greeting is personalized without exposing a full account identity', () => {
  assert.equal(homePreferredName('  AJ   Hart '), 'AJ');
  assert.equal(personalizedHomeGreeting('AJ Hart', new Date(2026, 7, 22, 8)), 'Good morning, AJ');
  assert.equal(personalizedHomeGreeting('AJ Hart', new Date(2026, 7, 22, 14)), 'Good afternoon, AJ');
  assert.equal(personalizedHomeGreeting('AJ Hart', new Date(2026, 7, 22, 20)), 'Good evening, AJ');
  assert.equal(personalizedHomeGreeting('', new Date(2026, 7, 22, 20)), 'Good evening');
});

test('Home mounts an accessible custom Ember shortcut that targets Bonfire Chat', () => {
  const root = path.resolve(import.meta.dirname, '..');
  const canvas = readFileSync(path.join(root, 'screens', 'CanvasScreen.tsx'), 'utf8');
  const shortcut = readFileSync(path.join(root, 'components', 'BonfireChatShortcut.tsx'), 'utf8');
  assert.match(canvas, /api\.scoutThreadIndex/u);
  assert.match(canvas, /isBonfireChat/u);
  assert.match(canvas, /navigation\.navigate\('Thread', \{[\s\S]*title: 'Bonfire Chat'/u);
  assert.match(canvas, /<BonfireChatShortcut/u);
  assert.match(shortcut, /accessibilityLabel="Open Bonfire Chat"/u);
  assert.match(shortcut, /<Svg[\s\S]*<Path[\s\S]*<Circle/u);
  assert.match(shortcut, /transform: \[\{ scale: 0\.96 \}\]/u);
});
