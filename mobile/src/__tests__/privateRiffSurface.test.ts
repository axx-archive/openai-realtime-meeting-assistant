import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('mobile publishes through the closed replay-safe all-or-reply contract', () => {
  const client = source('src', 'api', 'client.ts');
  const types = source('src', 'api', 'types.ts');
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  assert.match(client, /chat-threads\/\$\{encodeURIComponent\(riffThreadId\)\}\/riff-publish`/);
  assert.match(client, /scope: "all" \| "reply";[\s\S]*messageId\?: string;[\s\S]*episodeId\?: string;/);
  assert.match(types, /scope: 'all' \| 'reply';[\s\S]*messageIds: string\[\];[\s\S]*publishedCount: number/);
  assert.match(screen, /scope === "reply" \? \{ messageId: messageID \} : \{\}/);
  assert.match(screen, /privateRiff\.activeEpisodeId \? \{ episodeId: privateRiff\.activeEpisodeId \} : \{\}/);
  assert.match(screen, /privateRiffPublishAttemptRef/);
});

test('legacy paragraph transport remains compatible but has no current UI caller', () => {
  const client = source('src', 'api', 'client.ts');
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  const sheet = source('src', 'messaging', 'PrivateRiffShareSheet.tsx');
  assert.match(client, /privateRiffSharePreview/);
  assert.match(client, /publishPrivateRiffSelection/);
  assert.doesNotMatch(screen, /privateRiffSharePreview|publishPrivateRiffSelection|paragraphTokens/);
  assert.doesNotMatch(sheet, /accessibilityRole="checkbox"|paragraphTokens|PrivateRiffParagraph/);
});

test('public channel and Riff headers use a guitar with exact-message affordances', () => {
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  const actions = source('src', 'messaging', 'MessageActionSheet.tsx');
  const context = source('src', 'messaging', 'PrivateRiffContextSheet.tsx');
  assert.match(screen, /threadVisibility === "public" && latestRiffAnchor/);
  assert.match(screen, /startPrivateRiff\(actionMessage\.message\)/);
  assert.match(screen, /startPrivateRiff\(latestRiffAnchor, "resume"\)/);
  assert.match(screen, /navigation\.push\("ChannelRiff"/);
  assert.match(screen, /Share latest reply to #\$\{privateRiff\.sourceTitle/);
  assert.match(actions, /Riff privately from here/);
  assert.match(actions, /Share this reply to source/);
  assert.match(screen, /name="guitars\.fill"/);
  assert.match(actions, /name="guitars\.fill"/);
  assert.match(context, /name="guitars\.fill"/);
  assert.match(screen, /<FlashList/);
});

test('Riff Space is semantic, adaptive, automatic, and absent from ordinary private history', () => {
  const client = source('src', 'api', 'client.ts');
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  const navigator = source('src', 'navigation', 'RootNavigator.tsx');
  const listModel = source('src', 'messaging', 'channelListPerformance.ts');
  const context = source('src', 'messaging', 'PrivateRiffContextSheet.tsx');
  assert.match(navigator, /name="ChannelRiff"/);
  assert.match(navigator, /displayMode === 'sheet'/);
  assert.match(screen, /iPadWorkspace \? "rail" as const : "sheet" as const/);
  assert.match(screen, /selectedThreadId=\{privateRiff\?\.sourceThreadId/);
  assert.match(screen, /privateRiffCurrentEpisodeMessages\(privateRiff, messages\)/);
  assert.match(client, /chat-threads\/\$\{encodeURIComponent\(riffThreadId\)\}\?episodeId=\$\{encodeURIComponent\(episodeId\)\}/);
  assert.match(screen, /Earlier pass · Read-only/);
  assert.match(screen, /Resume this pass/);
  assert.match(screen, /entryPoint: "resume",[\s\S]*episodeId: episodeID/);
  assert.match(listModel, /!thread\.riff/);
  assert.match(listModel, /conversationKind/);
  assert.doesNotMatch(context, /onRefresh|Refresh context|Update context/);
  assert.match(context, /View pass/);
  assert.doesNotMatch(context, /<Text[^>]*>\{episode\.id\}<\/Text>/);
});

test('native share sheet presents exactly the two source publication scopes', () => {
  const sheet = source('src', 'messaging', 'PrivateRiffShareSheet.tsx');
  assert.match(sheet, /presentationStyle="pageSheet"/);
  assert.match(sheet, /`Share all to \$\{source\}`/);
  assert.match(sheet, /`Share this reply to \$\{source\}`/);
  assert.match(sheet, /initiating message as the channel root/);
  assert.match(sheet, /server-stamped author/);
  assert.doesNotMatch(sheet, /Use in my message|Share agent answer|accessibilityRole="checkbox"/);
});

test('Private Riff composer reuses dictation and the singleton Realtime transport with exact thread binding', () => {
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const client = source('src', 'api', 'client.ts');
  assert.match(screen, /useComposerDictation\(\{[\s\S]*threadId: route\.params\.threadId/);
  assert.match(screen, /usePersonalRealtimeContext\(\)/);
  assert.match(screen, /realtime\.start\(\{ threadId: route\.params\.threadId \}\)/);
  assert.match(screen, /say “share to source”/);
  assert.match(realtime, /!reconnecting && expectedThreadId && answer\.threadId !== expectedThreadId/);
  assert.match(client, /\.\.\.\(threadId \? \{ threadId \} : \{\}\)/);
});

test('published channel messages render server-stamped Private Riff provenance', () => {
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');
  assert.match(bubble, /Shared by \{publication\.sharedBy \|\| ['"]a teammate['"]\} from a private riff/);
  assert.match(bubble, /name="guitars\.fill"/);
  assert.match(bubble, /threadId: publication\.sourceThreadId[\s\S]*messageId: publication\.sourceThroughMessageId/);
});
