import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('long-press message actions expose a real clipboard copy path', () => {
  const sheet = source('src', 'messaging', 'MessageActionSheet.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');

  assert.match(sheet, /accessibilityLabel="Copy message"/);
  assert.match(sheet, /onPress=\{onCopy\}/);
  assert.match(thread, /Clipboard\.setStringAsync\(text\)/);
  assert.match(thread, /onCopy=\{\(\) => \{ if \(actionMessage\) copyMessage\(actionMessage\.message\); \}\}/);
  assert.match(source('src', 'messaging', 'MessageBubble.tsx'), /name: 'longpress', label: 'Show message actions'/);
});

test('focused drafting uses the native text layer and editing gets a full-height workspace', () => {
  const composer = source('src', 'messaging', 'MentionComposerInput.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');

  assert.match(composer, /const showMentionOverlay = !focused/);
  assert.match(composer, /onFocus=\{\(\) => setFocused\(true\)\}/);
  assert.match(composer, /showMentionOverlay \? styles\.inputTransparent : null/);
  assert.match(composer, /scrollEnabled=\{measuredHeight >= maxHeight\}/);
  assert.match(thread, /style=\{styles\.messageEditorInput\}/);
  assert.match(thread, /accessibilityLabel="Save edited message"/);
  assert.match(thread, /\{editingMessage \? \(/);
});

test('the thread owns one full-message sheet instead of mounting one per recycled bubble', () => {
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');

  assert.doesNotMatch(bubble, /import \{ LongMessageSheet \}/);
  assert.doesNotMatch(bubble, /<LongMessageSheet/);
  assert.match(bubble, /onOpenLongMessage\?\./);
  assert.match(thread, /<LongMessageSheet/);
  assert.match(thread, /visible=\{Boolean\(expandedMessage\)\}/);
});

test('private thread names save inline on Done or blur', () => {
  const list = source('src', 'messaging', 'ChannelList.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');

  assert.match(list, /onLongPress=\{thread\.visibility === 'public' \? undefined : \(\) => beginRename\(thread\)\}/);
  assert.match(list, /thread\.visibility === 'public'/);
  assert.match(list, /onBlur=\{\(\) => \{ void commitRename\(thread\); \}\}/);
  assert.match(list, /onSubmitEditing=\{\(\) => \{ void commitRename\(thread\); \}\}/);
  assert.match(list, /returnKeyType="done"/);
  assert.match(list, /api\.updateScoutThread\(sessionToken, threadID, \{ title \}\)/);
  assert.match(thread, /onLongPress=\{beginThreadTitleRename\}/);
  assert.match(thread, /name: 'longpress', label: 'Rename thread'/);
  assert.match(thread, /disabled=\{loading \|\| threadVisibility !== 'private'\}/);
  assert.match(thread, /onBlur=\{\(\) => \{ void commitThreadTitleRename\(\); \}\}/);
  assert.match(thread, /onSubmitEditing=\{\(\) => \{ void commitThreadTitleRename\(\); \}\}/);
  assert.match(thread, /navigation\.setParams\(\{ title \}\)/);
});

test('only private Scout composers claim Scout-directed dictation context', () => {
  const canvas = source('src', 'screens', 'CanvasScreen.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  const room = source('src', 'components', 'RoomConversationSheet.tsx');

  assert.match(canvas, /useComposerDictation\(\{\s*context: 'scout'/);
  assert.match(thread, /context: threadVisibility === 'private' \? 'scout' : 'chat'/);
  assert.doesNotMatch(room, /context: 'scout'/);
});
