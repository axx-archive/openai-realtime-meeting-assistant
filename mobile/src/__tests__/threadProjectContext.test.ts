import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { rebindOpaqueProjectChoice } from '../messaging/projectChoice';
import {
  explicitProjectAttachmentEnabled,
  safeProjectContextFromResponse,
  shouldRequestMainThreadProjectContext,
  shouldRequestReplyThreadProjectContext,
} from '../messaging/projectContextPreflight';

const screen = readFileSync(path.resolve(import.meta.dirname, '..', 'screens', 'ThreadScreen.tsx'), 'utf8');
const client = readFileSync(path.resolve(import.meta.dirname, '..', 'api', 'client.ts'), 'utf8');
const detail = readFileSync(path.resolve(import.meta.dirname, '..', 'messaging', 'ThreadDetailSheet.tsx'), 'utf8');
const canvas = readFileSync(path.resolve(import.meta.dirname, '..', 'screens', 'CanvasScreen.tsx'), 'utf8');

test('native chat retires manual Project attachment and sends no composer-selected token', () => {
  assert.equal(explicitProjectAttachmentEnabled, false);
  assert.match(screen, /projectContextToken = explicitProjectAttachmentEnabled && !editingMessage/u);
  assert.match(detail, /projectContextToken = explicitProjectAttachmentEnabled && selectedProject/u);
  assert.match(canvas, /projectContextToken = explicitProjectAttachmentEnabled && projectSessionToken/u);
  assert.match(screen, /visible=\{explicitProjectAttachmentEnabled && projectChooserOpen/u);
  assert.match(detail, /visible=\{explicitProjectAttachmentEnabled && projectChooserOpen/u);
  assert.match(canvas, /visible=\{explicitProjectAttachmentEnabled && projectChooserOpen/u);
  for (const source of [screen, detail, canvas]) assert.doesNotMatch(source, /Add project/u);
  assert.doesNotMatch(screen, /onChangeProject=\{openProjectCorrection\}/u);
});

test('opening ordinary Chat and Scout threads performs no automatic Project preflight', () => {
  assert.equal(shouldRequestMainThreadProjectContext({ sessionToken: 'session', editingMessage: false, chooserOpen: false, hasSelectedProject: false }), false);
  assert.equal(shouldRequestMainThreadProjectContext({ sessionToken: 'session', editingMessage: false, chooserOpen: true, hasSelectedProject: false }), false);
  assert.equal(shouldRequestMainThreadProjectContext({ sessionToken: 'session', editingMessage: false, chooserOpen: false, hasSelectedProject: true }), false);
  assert.equal(shouldRequestMainThreadProjectContext({ sessionToken: 'session', editingMessage: true, chooserOpen: true, hasSelectedProject: false }), false);
  assert.equal(shouldRequestMainThreadProjectContext({ sessionToken: null, editingMessage: false, chooserOpen: true, hasSelectedProject: false }), false);
  assert.match(screen, /shouldRequestMainThreadProjectContext/u);
  assert.doesNotMatch(screen, /accessibilityHint="Opens the authorized Project chooser/u);
  assert.equal(shouldRequestReplyThreadProjectContext({ visible: true, sessionToken: 'session', threadId: 'thread', rootMessageId: 'root', chooserOpen: false, hasSelectedProject: false }), false);
  assert.equal(shouldRequestReplyThreadProjectContext({ visible: true, sessionToken: 'session', threadId: 'thread', rootMessageId: 'root', chooserOpen: true, hasSelectedProject: false }), false);
  assert.equal(shouldRequestReplyThreadProjectContext({ visible: false, sessionToken: 'session', threadId: 'thread', rootMessageId: 'root', chooserOpen: true, hasSelectedProject: false }), false);
  assert.equal(shouldRequestReplyThreadProjectContext({ visible: true, sessionToken: 'session', threadId: '', rootMessageId: 'root', chooserOpen: true, hasSelectedProject: false }), false);
  assert.match(detail, /shouldRequestReplyThreadProjectContext/u);
  assert.doesNotMatch(detail, /accessibilityHint="Opens the authorized Project chooser/u);
});

test('missing or malformed Project envelopes fail safe instead of crashing Release renders', () => {
  assert.equal(safeProjectContextFromResponse(undefined), null);
  assert.equal(safeProjectContextFromResponse({ ok: true }), null);
  assert.equal(safeProjectContextFromResponse({ ok: true, projectContext: {} }), null);
  assert.equal(safeProjectContextFromResponse({ ok: true, projectContext: { available: 'yes' } }), null);
  assert.deepEqual(safeProjectContextFromResponse({
    ok: true,
    projectContext: {
      available: true,
      scopeKey: 'scope-a',
      choices: [
        { title: 'Country Golf', token: 'token-a', choiceKey: 'choice-a' },
        null,
        { title: 'missing token' },
      ],
      suggested: { title: 'Country Golf', token: 'token-a', choiceKey: 'choice-a', suggested: true },
    },
  }), {
    available: true,
    scopeKey: 'scope-a',
    choices: [{ title: 'Country Golf', token: 'token-a', choiceKey: 'choice-a' }],
    suggested: { title: 'Country Golf', token: 'token-a', choiceKey: 'choice-a', suggested: true },
  });
  assert.match(screen, /safeProjectContextFromResponse\(response\)/u);
  assert.match(detail, /safeProjectContextFromResponse\(response\)/u);
  assert.match(canvas, /safeProjectContextFromResponse\(response\)/u);
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
