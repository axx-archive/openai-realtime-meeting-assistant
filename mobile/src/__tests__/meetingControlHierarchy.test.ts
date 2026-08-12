import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const room = fs.readFileSync(path.join(mobileRoot, 'src', 'screens', 'RoomScreen.tsx'), 'utf8');
const nativeRoom = fs.readFileSync(path.join(mobileRoot, 'src', 'realtime', 'useNativeRoom.ts'), 'utf8');

test('native meeting dock keeps pressure-critical controls stable and leave separated', () => {
  const dock = room.match(/<View accessibilityLabel="Call controls"[\s\S]*?<\/View>\s*\n\s*<RoomConversationSheet/)?.[0] ?? '';
  assert.match(dock, /label=\{nativeRoom\.state\.microphoneStarting/);
  assert.match(dock, /label=\{nativeRoom\.state\.screenSharing \? 'Stop share'/);
  assert.match(dock, /label="Leave"[\s\S]*tone="danger"/);
  assert.doesNotMatch(dock, /label="Chat"/);
});

test('chat stays one predictable reveal away and unread state reaches the menu control', () => {
  assert.match(room, /id: 'chat'[\s\S]*Open chat[\s\S]*openConversation\('chat'\)/);
  assert.match(room, /More call options, room chat has \$\{nativeRoom\.conversation\.unreadCount\} unread/);
  assert.match(room, /badge=\{nativeRoom\.conversation\.unreadCount\}/);
});

test('transcript and truthful audio output stay one reveal away', () => {
  assert.match(room, /id: 'recap', label: 'Meeting recap'/);
  assert.match(room, /openConversation\('recap'\)/);
  assert.match(room, /id: 'transcript', label: 'Live transcript'/);
  assert.match(room, /openConversation\('transcript'\)/);
  assert.match(room, /id: 'audio-output'/);
  assert.match(room, /nativeRoom\.state\.audioRoute\?\.outputs\[0\]\?\.name/);
  assert.match(nativeRoom, /audioRoute: MeetingAudioRouteSnapshot \| null/);
  assert.match(nativeRoom, /audioRoute: snapshot/);
});

test('recording and screen-share truth remain persistent in the meeting header', () => {
  assert.match(room, /nativeRoom\.state\.recording \? `\$\{meetingIntelligenceStatusLabel\(nativeRoom\.intelligence\)\} · \$\{callActivityLabel\}`/);
  assert.match(room, /nativeRoom\.state\.screenSharing \? 'Sharing screen'/);
  assert.match(room, /accessibilityLabel=\{`Call status: \$\{callStatusLabel\}`\}/);
});
