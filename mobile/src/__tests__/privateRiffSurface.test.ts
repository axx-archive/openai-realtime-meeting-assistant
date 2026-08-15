import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('mobile uses the exact server-owned riff routes and replay-safe operation ids', () => {
  const client = source('src', 'api', 'client.ts');
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(client, /chat-threads\/\$\{encodeURIComponent\(sourceThreadId\)\}\/riff`/);
  assert.match(client, /chat-threads\/\$\{encodeURIComponent\(riffThreadId\)\}\/riff\/refresh`/);
  assert.match(client, /messages\/\$\{encodeURIComponent\(messageId\)\}\/riff-share-preview`/);
  assert.match(client, /messages\/\$\{encodeURIComponent\(messageId\)\}\/riff-publish`/);
  assert.match(screen, /privateRiffCreateAttemptRef/);
  assert.match(screen, /privateRiffRefreshAttemptRef/);
  assert.match(screen, /privateRiffPublishAttemptRef/);
  assert.match(screen, /throughMessageId: messageID,[\s\S]*agentId: ""/);
});

test('a public channel offers exact-message and latest-message private entry points', () => {
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  const actions = source('src', 'messaging', 'MessageActionSheet.tsx');
  assert.match(screen, /threadVisibility === "public" && latestRiffAnchor/);
  assert.match(screen, /startPrivateRiff\(actionMessage\.message\)/);
  assert.match(actions, /Riff privately from here/);
  assert.match(actions, /minHeight: hitMin/);
  assert.match(screen, /<FlashList/);
});

test('private context and selective sharing use native sheets with explicit privacy copy', () => {
  const context = source('src', 'messaging', 'PrivateRiffContextSheet.tsx');
  const share = source('src', 'messaging', 'PrivateRiffShareSheet.tsx');
  assert.match(context, /presentationStyle="pageSheet"/);
  assert.match(context, /New public messages never enter silently/);
  assert.match(context, /accessibilityHint="Creates a new immutable checkpoint/);
  assert.match(share, /presentationStyle="pageSheet"/);
  assert.match(share, /accessibilityRole="checkbox"/);
  assert.match(share, /Only checked paragraphs will cross the private boundary/);
  assert.match(share, /Use in my message/);
  assert.match(share, /Share agent answer/);
});

test('draft sharing navigates to the source without posting and agent sharing confirms provenance', () => {
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  const navigation = source('src', 'navigation', 'types.ts');
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');
  assert.match(navigation, /draft\?: string;[\s\S]*draftProvenance\?: 'private_riff'/);
  assert.match(screen, /mode === "draft"[\s\S]*navigation\.push\("Thread"[\s\S]*draftProvenance: "private_riff"/);
  assert.match(screen, /Private Riff draft · edit before sending/);
  assert.match(screen, /mode === "agent" && !confirmed[\s\S]*Share as agent/);
  assert.match(screen, /Only the selected paragraphs were posted, with Private Riff provenance/);
  assert.match(bubble, /Shared by \{publication\.sharedBy \|\| ['"]a teammate['"]\} from a private riff/);
  assert.match(bubble, /threadId: publication\.sourceThreadId[\s\S]*messageId: publication\.sourceThroughMessageId/);
  assert.match(screen, /source\.threadId && source\.threadId !== route\.params\.threadId[\s\S]*messageId: source\.messageId/);
});
