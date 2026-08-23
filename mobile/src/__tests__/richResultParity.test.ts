import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const source = (...parts: string[]) => readFileSync(path.resolve(import.meta.dirname, '..', ...parts), 'utf8');

test('live-room chat renders conversation plus exact typed results while lifecycle stays in Activity', () => {
  const sheet = source('components', 'RoomConversationSheet.tsx');
  const reducer = source('realtime', 'roomConversation.ts');

  assert.match(sheet, /roomConversationFeedMessages\(messages\)/u);
  assert.match(sheet, /roomConversationActivityMessages\(messages\)/u);
  assert.match(sheet, /data=\{feedMessages\}/u);
  assert.match(sheet, /accessibilityLabel=\{`Open Activity\./u);
  assert.match(sheet, /presentationStyle="pageSheet"[\s\S]*visible=\{activityOpen\}/u);
  assert.match(sheet, /<FlatList[\s\S]*data=\{activityRows\}[\s\S]*renderItem=\{renderActivityItem\}/u);
  assert.doesNotMatch(sheet, /\[\.\.\.activityMessages\]\.reverse\(\)\.map/u);
  assert.doesNotMatch(sheet, /item\.artifactId && item\.workRunId && item\.workStatus/u);
  assert.doesNotMatch(sheet, /onOpenArtifact\?\.\(item\.artifactId/u);
  assert.match(sheet, /Activity is the bounded process surface/u);

  assert.match(reducer, /roomChatMessageBelongsInConversation\(message\)/u);
  assert.match(reducer, /message\.workStatus === 'complete'/u);
  assert.match(reducer, /Number\.isSafeInteger\(version\)/u);
  assert.match(reducer, /version > 0/u);
  assert.match(reducer, /\^\[0-9a-f\]\{64\}\$/u);
  assert.match(reducer, /if \(parentRunId && !rootRunId\) return ''/u);
});

test('room result hydration binds exact id, type, revision, and digest and never substitutes raw state', () => {
  const sheet = source('components', 'RoomConversationSheet.tsx');
  assert.match(sheet, /api\.artifact\(sessionToken, artifactId\)/u);
  assert.match(sheet, /artifact\.id !== artifactId/u);
  assert.match(sheet, /loadedType !== declaredType/u);
  assert.match(sheet, /loadedVersion !== artifactVersion/u);
  assert.match(sheet, /loadedDigest !== artifactDigest/u);
  assert.match(sheet, /artifactVersion,[\s\S]*artifactDigest,[\s\S]*satisfies RoomResultArtifactRef/u);
  assert.match(sheet, /workRefHasClosedResultEnvelope/u);
  assert.match(sheet, /declaredType === 'markdown' \? String\(artifact\.text/u);
  assert.doesNotMatch(sheet, /JSON\.stringify\(artifact/u);
});

test('native Present and live-room opens preserve and reverify the immutable result tuple', () => {
  const thread = source('screens', 'ThreadScreen.tsx');
  const room = source('screens', 'RoomScreen.tsx');

  assert.match(thread, /if \(explicitResultArtifactId\) \{[\s\S]{0,180}void openWorkArtifact\(message\)/u);
  assert.match(thread, /navigation\.navigate\('DeckViewer', \{[\s\S]*artifactVersion: resultArtifactVersion,[\s\S]*artifactDigest: resultArtifactDigest/u);
  assert.match(thread, /artifactStudioPath\([\s\S]*\{ version: resultArtifactVersion, digest: resultArtifactDigest \}/u);
  assert.match(room, /async \(result: RoomResultArtifactRef\)/u);
  assert.match(room, /api\.artifact\(sessionToken, artifactId\)/u);
  assert.match(room, /Number\(metadata\.artifactVersion \?\? 0\) !== artifactVersion/u);
  assert.match(room, /String\(metadata\.contentDigest \?\? ''\)[\s\S]*!== artifactDigest/u);
  assert.match(room, /artifactVersion,[\s\S]*artifactDigest,[\s\S]*desktopEditable: false/u);
  assert.match(room, /artifactStudioPath\(artifactId, 'document', 'present', \{ version: artifactVersion, digest: artifactDigest \}\)/u);
  assert.doesNotMatch(room, /roomWorkPreview/u);
  assert.doesNotMatch(room, /LongMessageSheet/u);
});

test('native structured results use premium previews and authenticated exact-file actions', () => {
  const preview = source('messaging', 'InlineArtifactPreview.tsx');
  const bubble = source('messaging', 'MessageBubble.tsx');
  const screen = source('screens', 'ThreadScreen.tsx');

  for (const kind of ['pdf', 'image', 'table', 'workbook', 'bundle', 'file']) {
    assert.match(preview, new RegExp(`${kind}:`), `missing ${kind} presentation label`);
  }
  assert.match(preview, /authenticatedFileUrl/u);
  assert.match(preview, /authenticatedFileHeaders/u);
  assert.match(preview, /onOpenAsset\?\.\(asset\)/u);
  assert.match(preview, /kind === 'table'[\s\S]*tableColumns/u);
  assert.match(preview, /kind === 'workbook'[\s\S]*workbookFacts/u);
  assert.match(bubble, /assets=\{workThread\.ref\.resultAssets\}/u);
  assert.match(bubble, /structuredInlineArtifact \? undefined/u);
  assert.match(screen, /explicitResultArtifactId && structuredResult/u);
  assert.match(screen, /setPreviewFile/u);
  assert.doesNotMatch(screen, /structuredResult[\s\S]{0,500}setExpandedMessage/u);
});
