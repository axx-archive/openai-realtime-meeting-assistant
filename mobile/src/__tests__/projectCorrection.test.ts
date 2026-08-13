import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('native Project correction is read-only until an explicit Update or Remove', () => {
  const sheet = source('src', 'messaging', 'ProjectCorrectionSheet.tsx');
  assert.match(sheet, /accessibilityRole="radio"/u);
  assert.match(sheet, /setSelection\(\{ kind: 'project'/u);
  assert.match(sheet, /setSelection\(\{ kind: 'remove'/u);
  assert.match(sheet, /disabled=\{!selection \|\| updating\}/u);
  assert.match(sheet, /selection && onSubmit\(selection\)/u);
  assert.match(sheet, /This changes only this message\./u);
  assert.match(sheet, /onRequestClose=\{close\}/u);
});

test('only the signed correction token crosses the native mutation boundary', () => {
  const client = source('src', 'api', 'client.ts');
  assert.match(client, /updateProjectCorrection\([\s\S]*body: \{ operationId: string; correctionToken: string \}/u);
  assert.match(client, /method: "PATCH", body, sessionToken/u);
  assert.doesNotMatch(client, /updateProjectCorrection[\s\S]{0,700}projectId/u);
});

test('author-owned terminal Project chips and message actions open correction while pending is disabled', () => {
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');
  const actions = source('src', 'messaging', 'MessageActionSheet.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(bubble, /projectCorrectable = own && !projectPending && !projectRemoved/u);
  assert.match(bubble, /\. Change project`/u);
  assert.match(actions, /accessibilityLabel="Change project for this message"/u);
  assert.match(actions, /accessibilityState=\{\{ disabled: projectChangePending \}\}/u);
  assert.match(thread, /isOwnMessageForViewer\(message, \{[\s\S]*viewerEmail: email/u);
  assert.match(thread, /status !== "confirmed" && status !== "unavailable"/u);
});

test('correction retry is stable and account or thread changes clear all authority state', () => {
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(thread, /projectCorrectionAttemptRef\.current\?\.key === attemptKey/u);
  assert.match(thread, /operationId: createConversationOperationId\(\)/u);
  assert.match(thread, /projectCorrectionTargetRef\.current = null/u);
  assert.match(thread, /\}, \[route\.params\.threadId, sessionToken\]\);/u);
  assert.match(thread, /caught\.status === 409[\s\S]*"projectCorrection" in caught\.data/u);
});

test('correction sheet manages VoiceOver focus and supports contained reply presentation', () => {
  const sheet = source('src', 'messaging', 'ProjectCorrectionSheet.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(sheet, /AccessibilityInfo\.setAccessibilityFocus/u);
  assert.match(sheet, /returnFocusHandle/u);
  assert.match(sheet, /<ScrollView/u);
  assert.match(sheet, /flexWrap: 'wrap'/u);
  assert.match(thread, /renderProjectCorrectionSheet\(true\)/u);
  assert.match(thread, /threadContextRoot \? null : renderProjectCorrectionSheet\(\)/u);
});
