import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const room = fs.readFileSync(path.join(mobileRoot, 'src', 'screens', 'RoomScreen.tsx'), 'utf8');
const nativeRoom = fs.readFileSync(path.join(mobileRoot, 'src', 'realtime', 'useNativeRoom.ts'), 'utf8');

test('native meeting dock is one compact invariant island with route and leave separated', () => {
  const dock = room.match(/<View accessibilityLabel="Call controls"[\s\S]*?<\/View>\s*\n\s*<\/View>\s*\n\s*<RoomConversationSheet/)?.[0] ?? '';
  assert.match(dock, /label=\{nativeRoom\.state\.microphoneStarting/);
  assert.match(dock, /label="Route"[\s\S]*onPress=\{showAudioOutput\}/);
  assert.match(dock, /label=\{nativeRoom\.state\.screenSharing \? 'Stop share'/);
  assert.match(dock, /label="Leave"[\s\S]*tone="danger"/);
  assert.doesNotMatch(dock, /label="Chat"/);
  assert.match(room, /callControlDockFrame:[\s\S]*alignItems: 'center'/);
  assert.match(room, /callControl:[\s\S]*width: 44[\s\S]*height: 48/);
  assert.match(room, /callRoomIdentity:[\s\S]*minHeight: 44/);
  assert.match(room, /callControlLabel: \{ display: 'none'/);
});

test('chat stays one predictable reveal away and unread state reaches the menu control', () => {
  assert.match(room, /id: 'chat'[\s\S]*Open chat[\s\S]*openConversation\('chat'\)/);
  assert.match(room, /More call options, room chat has \$\{nativeRoom\.conversation\.unreadCount\} unread/);
  assert.match(room, /badge=\{nativeRoom\.conversation\.unreadCount\}/);
});

test('transcript stays one reveal away while truthful audio output is primary', () => {
  assert.match(room, /id: 'recap', label: 'Meeting recap'/);
  assert.match(room, /openConversation\('recap'\)/);
  assert.match(room, /id: 'transcript', label: 'Live transcript'/);
  assert.match(room, /openConversation\('transcript'\)/);
  assert.doesNotMatch(room, /id: 'audio-output'/);
  assert.match(room, /function showAudioOutput\(\)/);
  assert.match(room, /nativeRoom\.state\.audioRoute\?\.outputs\[0\]\?\.name/);
  assert.match(nativeRoom, /audioRoute: MeetingAudioRouteSnapshot \| null/);
  assert.match(nativeRoom, /audioRoute: snapshot/);
});

test('secondary meeting work and privacy actions have one More-menu owner', () => {
  assert.match(room, /id: 'people', label: 'People in this room'/);
  assert.match(room, /if \(meetingAgentControlsAvailable\(\)\)[\s\S]*id: 'specialists', label: 'Agent team'/);
  assert.match(room, /if \(inNativeRoom\) void loadSpecialists\(\)/);
  assert.doesNotMatch(room, /id: 'board', label: 'Board'/);
  assert.match(room, /id: 'data-choices', label: 'Microphone data'/);
  assert.match(room, /<RoomConsentSheet[\s\S]*visible=\{consentVisible\}/);
  assert.doesNotMatch(room, /id: 'audio-output'/);
});

test('recording and screen-share truth remain persistent in the meeting header', () => {
  assert.match(room, /nativeRoom\.state\.recording \? `\$\{meetingIntelligenceStatusLabel\(nativeRoom\.intelligence\)\} · \$\{callActivityLabel\}`/);
  assert.match(room, /nativeRoom\.state\.screenSharing \? 'Sharing screen'/);
  assert.match(room, /accessibilityLabel=\{`Call status: \$\{callStatusLabel\}`\}/);
});
