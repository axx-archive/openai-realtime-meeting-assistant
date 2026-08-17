import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('Meet is a card destination with loading, error, retry, and Create room', () => {
  const meet = source('src', 'screens', 'MeetScreen.tsx');
  const model = source('src', 'navigation', 'nativeShellModel.ts');
  const root = source('src', 'navigation', 'RootNavigator.tsx');

  // Meet screen has proper loading state
  assert.match(meet, /accessibilityLabel="Loading rooms"/);
  assert.match(meet, /accessibilityRole="progressbar"/);

  // Meet screen has error and retry
  assert.match(meet, /Tap to retry/);
  assert.match(meet, /onPress=\{.*void load\(\)/);
  assert.match(meet, /Could not load rooms/);

  // Meet screen has Create room action with iPad workstation support
  assert.match(meet, /accessibilityLabel="Create room"/);
  assert.match(meet, /navigation\.navigate\('CreateRoom', \{ displayMode: useWorkstation \? 'workstation' : 'sheet' \}\)/);
  assert.match(meet, /WORKSTATION_MIN_WIDTH = 1024/);

  // Meet screen has honest empty state
  assert.match(meet, /No rooms yet/);
  assert.match(meet, /Create a room to start a video call/);
  assert.match(meet, /Create your first room/);

  // Meet routes to Meet screen (not Deck with segment)
  assert.match(model, /id: 'video', label: 'Meet', route: 'Meet'/);
  assert.doesNotMatch(model, /id: 'video'[^}]*params:/);

  // Meet is registered in RootNavigator
  assert.match(root, /name="Meet" component=\{MeetScreen\}/);
});

test('Meet destination stays distinct from Work (no junk drawer)', () => {
  const meet = source('src', 'screens', 'MeetScreen.tsx');

  // Meet does not expose Work segment destinations
  assert.doesNotMatch(meet, /Memory|Intelligence|Marketplace|Settings/);
  assert.doesNotMatch(meet, /route: 'Memory'|route: 'Intelligence'|route: 'Settings'/);
  // Meet does not use segment-based routing (that's the old Deck pattern)
  assert.doesNotMatch(meet, /segment: ['"]rooms['"]|DeckSegment/);
});

test('Chat is a card destination with New conversation from Chat', () => {
  const chat = source('src', 'screens', 'ChatScreen.tsx');
  const model = source('src', 'navigation', 'nativeShellModel.ts');
  const root = source('src', 'navigation', 'RootNavigator.tsx');

  // Chat routes to Chat screen (not Deck with segment)
  assert.match(model, /id: 'chat', label: 'Chat', route: 'Chat'/);
  assert.doesNotMatch(model, /id: 'chat'[^}]*params:/);

  // Chat is registered in RootNavigator
  assert.match(root, /name="Chat" component=\{ChatScreen\}/);

  // Chat has ChannelList
  assert.match(chat, /<ChannelList/);
});

test('Chat destination stays distinct from Work (no junk drawer)', () => {
  const chat = source('src', 'screens', 'ChatScreen.tsx');

  // Chat does not expose Work segment destinations
  assert.doesNotMatch(chat, /Memory|Intelligence|Marketplace|Settings/);
  assert.doesNotMatch(chat, /route: 'Memory'|route: 'Intelligence'|route: 'Settings'/);
  // Chat does not use segment-based routing (that's the old Deck pattern)
  assert.doesNotMatch(chat, /segment: ['"]threads['"]|DeckSegment/);
});
