import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { rebindOpaqueProjectChoice } from '../messaging/projectChoice';

const screen = readFileSync(path.resolve(import.meta.dirname, '..', 'screens', 'ThreadScreen.tsx'), 'utf8');
const client = readFileSync(path.resolve(import.meta.dirname, '..', 'api', 'client.ts'), 'utf8');
const detail = readFileSync(path.resolve(import.meta.dirname, '..', 'messaging', 'ThreadDetailSheet.tsx'), 'utf8');

test('native existing threads keep Project choice zero-effect until explicit Send', () => {
  assert.match(screen, /attachmentHandles: projectAttachmentHandles/u);
  assert.match(screen, /accessibilityHint="Opens the authorized Project chooser\. Nothing changes until you send\."/u);
  assert.match(screen, /accessibilityRole="radio"/u);
  assert.match(screen, /\[\{ title: "No project", token: "" \}, \.\.\.projectContext\.choices\]/u);
  assert.match(screen, /projectExplicitNone \? "No project" : "Add project"/u);
  assert.match(screen, /setProjectExplicitNone\(!choice\.token\)/u);
  assert.match(screen, /rebindOpaqueProjectChoice\(current, next\.suggested, next\.choices, projectExplicitNone\)/u);
  assert.match(screen, /projectContextToken = !editingMessage && selectedProject\?\.text === text/u);
  assert.match(screen, /projectContextToken,\s*\);/u);
  assert.match(screen, /selectedProject\.sourceKey === projectSourceKey/u);
  assert.doesNotMatch(screen, /Send the Project-linked message first, then attach files in the next turn/u);
});

test('native reply sheet binds Project preview to the exact root and ordered attachments', () => {
  assert.match(detail, /replyToMessageId: String\(root\.id\)/u);
  assert.match(detail, /attachmentHandles/u);
  assert.match(detail, /rebindOpaqueProjectChoice\(current, next\.suggested, next\.choices, projectExplicitNone\)/u);
  assert.match(detail, /selectedProject\?\.text === text && selectedProject\.sourceKey === projectSourceKey/u);
  assert.match(detail, /presentationStyle="pageSheet"/u);
  assert.match(detail, /accessibilityRole="radio"/u);
  assert.match(screen, /rootID,\s*replyAttempt\.operationId,\s*projectContextToken/u);
});

test('native Project refresh fails safe when opaque choice identity disappears', () => {
  const current = { title: 'Country Golf', token: 'token-old', choiceKey: 'choice-a' };
  const refreshed = { title: 'Country Golf renamed', token: 'token-new', choiceKey: 'choice-a' };
  const replacement = { title: 'Ball Dogs', token: 'token-b', choiceKey: 'choice-b' };

  assert.deepEqual(rebindOpaqueProjectChoice(current, replacement, [refreshed], false), refreshed);
  assert.equal(rebindOpaqueProjectChoice(current, replacement, [replacement], false), null);
  assert.equal(rebindOpaqueProjectChoice({ ...current, choiceKey: '' }, replacement, [replacement], false), null);
  assert.equal(rebindOpaqueProjectChoice(null, { ...replacement, choiceKey: '' }, [replacement], false), null);
  assert.deepEqual(rebindOpaqueProjectChoice(null, replacement, [], false), replacement);
  assert.equal(rebindOpaqueProjectChoice(current, refreshed, [refreshed], true), null);
});

test('native message transport carries only the signed Project token', () => {
  assert.match(client, /\.\.\.\(projectContextToken \? \{ projectContextToken \} : \{\}\)/u);
  assert.match(client, /attachmentHandles\?: Array<\{ sourceId: string; sourceRevision: string \}>/u);
  assert.match(client, /replyToMessageId\?: string/u);
  assert.doesNotMatch(client, /sendScoutMessage[\s\S]{0,900}projectId/u);
  assert.doesNotMatch(client, /sendScoutMessage[\s\S]{0,900}authorityGeneration/u);
});
