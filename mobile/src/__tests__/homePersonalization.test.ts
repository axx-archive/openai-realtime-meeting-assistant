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
  assert.match(canvas, /candidate\.title[\s\S]*=== 'bonfire chat'[\s\S]*threads\.find\(isBonfireChat\)/u);
  assert.match(canvas, /navigation\.navigate\('Thread', \{[\s\S]*title: 'Bonfire Chat'/u);
  assert.doesNotMatch(canvas, /navigation\.navigate\('Chat'\)/u);
  assert.match(canvas, /Bonfire Chat is unavailable right now\. Tap the icon to try again\./u);
  assert.match(canvas, /bonfireStatusLanePlacement\(canvasWidth, canvasHeight\)/u);
  assert.match(canvas, /accessibilityRole="alert"[\s\S]*bonfireStatusLane\.compact \? 'Bonfire unavailable' : bonfireError/u);
  assert.match(canvas, /<BonfireChatShortcut[\s\S]*unavailable=\{Boolean\(bonfireError\)\}/u);
  assert.match(canvas, /bonfireMountedRef[\s\S]*bonfireFocusedRef[\s\S]*bonfireAttemptGenerationRef/u);
  assert.match(canvas, /if \(!bonfireAttemptIsCurrent\(token, generation\)\) return;[\s\S]*navigation\.navigate\('Thread'/u);
  assert.match(canvas, /bonfireFocusedRef\.current = false/u);
  assert.match(canvas, /<BonfireChatShortcut/u);
  assert.match(shortcut, /accessibilityLabel=\{unavailable \? 'Try Bonfire Chat again' : 'Open Bonfire Chat'\}/u);
  assert.match(shortcut, /<Svg[\s\S]*<Path[\s\S]*<Circle/u);
  assert.match(shortcut, /transform: \[\{ scale: 0\.96 \}\]/u);
});
