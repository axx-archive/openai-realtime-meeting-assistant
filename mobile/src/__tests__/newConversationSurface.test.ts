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
  assert.match(canvas, /usePersonalRealtime/);
  assert.match(canvas, /useComposerDictation/);
  assert.match(canvas, /Start a new private voice chat with Scout/);
  assert.match(canvas, /accessibilityLabel="Dictate a message"/);
  assert.match(canvas, /placeholder="Message Scout"/);
  assert.match(canvas, /submitHomeScoutOpening/);
  assert.doesNotMatch(canvas, /toolTemplate|composerDock|<ChatCircle|<NavCluster/);
});

test('Work exposes a real native private-chat and channel creation route', () => {
  const shell = source('src', 'screens', 'NativeShellScreens.tsx');
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  const screen = source('src', 'screens', 'NewConversationScreen.tsx');
  const client = source('src', 'api', 'client.ts');
  assert.match(shell, /route: 'NewConversation'[^\n]*label: 'New conversation'/);
  // NewConversation uses formSheet on phone, card on iPad ≥1024 (workstation mode)
  assert.match(root, /name="NewConversation"[\s\S]*displayMode === 'workstation'/);
  assert.match(root, /name="NewConversation"[\s\S]*presentation: 'formSheet'/);
  assert.match(root, /name="NewConversation"[\s\S]*presentation: 'card'/);
  assert.match(screen, /'Private chat'[\s\S]*'Channel'/);
  assert.match(screen, /api\.createScoutThread\(sessionToken, newConversationBody\(attempt\)\)/);
  assert.match(screen, /navigation\.replace\('Thread'/);
  assert.match(client, /operationId\?: string/);
});

test('Chat destination exposes New conversation directly (not only from Work)', () => {
  const chat = source('src', 'screens', 'ChatScreen.tsx');
  assert.match(chat, /accessibilityLabel="New conversation"/);
  // Chat passes displayMode for iPad workstation support
  assert.match(chat, /navigation\.navigate\('NewConversation', \{ displayMode: useWorkstation \? 'workstation' : 'sheet' \}\)/);
  assert.match(chat, /WORKSTATION_MIN_WIDTH = 1024/);
  assert.match(chat, /<ChannelList/);
});

test('list-open from ChannelList navigates (stacks Thread on Chat)', () => {
  const channelList = source('src', 'messaging', 'ChannelList.tsx');
  // ChannelList opens existing threads with navigate so they stack on Chat.
  // Back from Thread returns to Chat (the list), not Home.
  assert.match(channelList, /navigation\.navigate\('Thread',\s*\{/);
  assert.doesNotMatch(channelList, /navigation\.replace\('Thread'/);
});

test('new conversation create replaces the sheet (dismisses before Thread)', () => {
  const screen = source('src', 'screens', 'NewConversationScreen.tsx');
  // NewConversation is a formSheet. After creating, replace dismisses the sheet
  // so the stack is Chat → Thread. Back from Thread returns to Chat, not a
  // spent form. This matches CreateRoomScreen's pattern.
  assert.match(screen, /navigation\.replace\('Thread',\s*\{/);
  assert.doesNotMatch(screen, /navigation\.navigate\('Thread'/);
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
