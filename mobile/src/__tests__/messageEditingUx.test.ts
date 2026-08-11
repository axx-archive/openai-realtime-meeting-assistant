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
  assert.match(thread, /onCopy=\{\(\) => \{\s*if \(actionMessage\) copyMessage\(actionMessage\.message\);\s*\}\}/);
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

test('public channels use typed mention autocomplete without a persistent Scout shortcut', () => {
  const composer = source('src', 'messaging', 'MentionComposerInput.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');

  assert.match(composer, /activeMentionQuery\(value\)/);
  assert.match(composer, /candidates\.filter/);
  assert.match(thread, /<MentionComposerInput/);
  assert.match(thread, /candidates=\{participants\}/);
  assert.doesNotMatch(thread, /accessibilityLabel="Ask Scout in this channel"/);
  assert.doesNotMatch(thread, /insertScoutMention/);
  assert.doesNotMatch(thread, /scoutMentionShortcut/);
});

test('threaded replies keep edit and delete in the same long-press action sheet as the feed', () => {
  const detail = source('src', 'messaging', 'ThreadDetailSheet.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  const actions = source('src', 'messaging', 'MessageActionSheet.tsx');

  assert.match(detail, /onLongPress=\{onLongPress\}/);
  assert.match(detail, /\{actionOverlay\}/);
  assert.doesNotMatch(detail, /accessibilityLabel="Edit reply"/);
  assert.doesNotMatch(detail, /accessibilityLabel="Delete reply"/);
  assert.match(thread, /onLongPress=\{openMessageActions\}/);
  assert.match(thread, /renderMessageActionSheet\(true\)/);
  assert.match(actions, /contained\?: boolean/);
  assert.match(actions, /if \(contained\) return visible \? content : null/);
  assert.match(actions, />Edit message</);
  assert.match(actions, />Delete message</);
});

test('the thread owns one full-message sheet instead of mounting one per recycled bubble', () => {
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');

  assert.doesNotMatch(bubble, /import \{ LongMessageSheet \}/);
  assert.doesNotMatch(bubble, /<LongMessageSheet/);
  assert.match(bubble, /onOpenLongMessage\?\./);
  assert.match(thread, /<LongMessageSheet/);
  assert.match(thread, /visible=\{Boolean\(expandedMessage\)\}/);
  assert.match(thread, /renderLongMessageSheet\(true\)/);
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
  assert.match(thread, /name: ["']longpress["'], label: ["']Rename thread["']/);
  assert.match(thread, /disabled=\{loading \|\| threadVisibility !== ["']private["']\}/);
  assert.match(thread, /onBlur=\{\(\) => \{\s*void commitThreadTitleRename\(\);\s*\}\}/);
  assert.match(thread, /onSubmitEditing=\{\(\) => \{\s*void commitThreadTitleRename\(\);\s*\}\}/);
  assert.match(thread, /navigation\.setParams\(\{ title \}\)/);
});

test('only private thread composers claim Scout-directed dictation context', () => {
  const canvas = source('src', 'screens', 'CanvasScreen.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  const room = source('src', 'components', 'RoomConversationSheet.tsx');

  assert.doesNotMatch(canvas, /useComposerDictation/);
  assert.match(thread, /context: threadVisibility === ["']private["'] \? ["']scout["'] : ["']chat["']/);
  assert.doesNotMatch(room, /context: 'scout'/);
});
