import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import {
  newConversationAttempt,
  newConversationBody,
  normalizeConversationTitle,
} from '../conversations/newConversation';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('Home accepts direct voice or text without a tool picker or duplicate navigation cluster', () => {
  const canvas = source('src', 'screens', 'CanvasScreen.tsx');
  assert.match(canvas, /<StrideCradle/);
  assert.match(canvas, /usePersonalRealtime/);
  assert.match(canvas, /placeholder="Message Scout"/);
  assert.match(canvas, /submitHomeScoutOpening/);
  assert.doesNotMatch(canvas, /toolTemplate|composerDock|useComposerDictation|<ChatCircle|<NavCluster/);
});

test('Work exposes a real native private-chat and channel creation route', () => {
  const shell = source('src', 'screens', 'NativeShellScreens.tsx');
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  const screen = source('src', 'screens', 'NewConversationScreen.tsx');
  const client = source('src', 'api', 'client.ts');
  assert.match(shell, /route: 'NewConversation'[^\n]*label: 'New conversation'/);
  assert.match(root, /name="NewConversation"[\s\S]*presentation: 'formSheet'/);
  assert.match(screen, /'Private chat'[\s\S]*'Channel'/);
  assert.match(screen, /api\.createScoutThread\(sessionToken, newConversationBody\(attempt\)\)/);
  assert.match(screen, /navigation\.replace\('Thread'/);
  assert.match(client, /operationId\?: string/);
});

test('creation attempts normalize, replay exactly, and separate private from public', () => {
  let sequence = 0;
  const create = () => `operation-${++sequence}`;
  assert.equal(normalizeConversationTitle('  Investor   research  '), 'Investor research');
  const privateAttempt = newConversationAttempt(null, 'private', '  Investor   research  ', create);
  assert.deepEqual(privateAttempt, { kind: 'private', title: 'Investor research', operationId: 'operation-1' });
  assert.equal(newConversationAttempt(privateAttempt, 'private', 'Investor research', create), privateAttempt);
  assert.deepEqual(newConversationBody(privateAttempt!), {
    title: 'Investor research', visibility: 'private', operationId: 'operation-1',
  });
  const channelAttempt = newConversationAttempt(privateAttempt, 'channel', 'venture-review', create);
  assert.deepEqual(newConversationBody(channelAttempt!), {
    title: 'venture-review', visibility: 'public', operationId: 'operation-2',
  });
  assert.equal(newConversationAttempt(null, 'private', ' ', create), null);
  assert.equal(newConversationAttempt(null, 'private', 'x'.repeat(81), create), null);
});
