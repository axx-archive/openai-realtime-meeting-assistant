import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const messagingRoot = path.resolve(import.meta.dirname, '..', 'messaging');
const screenSource = fs.readFileSync(path.resolve(import.meta.dirname, '..', 'screens', 'ThreadScreen.tsx'), 'utf8');
const sheetSource = fs.readFileSync(path.join(messagingRoot, 'ThreadDetailSheet.tsx'), 'utf8');
const activitySource = fs.readFileSync(path.join(messagingRoot, 'LongMessageSheet.tsx'), 'utf8');

test('mobile channel rows render only topology roots with one persistent reply affordance', () => {
  assert.match(screenSource, /buildThreadReplyTopology\(messages\)/);
  assert.match(screenSource, /const feedMessages = replyTopology\.feedMessages/);
  assert.match(screenSource, /feedMessages\.map\(\(message, index\)/);
  assert.match(screenSource, /threadReplies: replyTopology\.repliesFor\(message\)/);
  assert.match(screenSource, /onOpenThread=\{openThreadContext\}/);
  assert.doesNotMatch(screenSource, /const \[replyingTo,/);
  assert.doesNotMatch(screenSource, /Replying to \{String\(replyingTo/);
});

test('thread replies use a native dismissible page sheet with their own composer', () => {
  assert.match(sheetSource, /presentationStyle="pageSheet"/);
  assert.match(sheetSource, /onRequestClose=\{onClose\}/);
  assert.match(sheetSource, /name="xmark"/);
  assert.match(sheetSource, /contentInsetAdjustmentBehavior="automatic"/);
  assert.match(sheetSource, /showReplyContext=\{false\}/);
  assert.match(sheetSource, /measureComposerHeight\(/);
  assert.match(sheetSource, /setMeasuredComposerHeight\(compactComposerHeight\)/);
  assert.match(sheetSource, /accessibilityLabel="Reply in thread"/);
  assert.match(sheetSource, /onSend\(text\)/);
});

test('mobile work activity uses the same native page-sheet behavior', () => {
  assert.match(activitySource, /presentationStyle="pageSheet"/);
  assert.match(activitySource, /onRequestClose=\{onClose\}/);
  assert.match(activitySource, /name="xmark"/);
  assert.match(activitySource, /SCOUT · ACTIVITY/);
});
