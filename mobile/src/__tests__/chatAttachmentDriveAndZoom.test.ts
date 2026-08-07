import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('the authenticated image preview supports native pinch zoom without replacing its toolbar', () => {
  const preview = source('src', 'components', 'FilePreviewModal.tsx');

  assert.match(preview, /maximumZoomScale=\{4\}/);
  assert.match(preview, /minimumZoomScale=\{1\}/);
  assert.match(preview, /pinchGestureEnabled/);
  assert.match(preview, /bouncesZoom/);
  assert.match(preview, /accessibilityLabel="Close file preview"/);
  assert.match(preview, /square\.and\.arrow\.up/);
});

test('a long-pressed chat attachment exposes explicit named Drive save', () => {
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');
  const actions = source('src', 'messaging', 'MessageActionSheet.tsx');
  const screen = source('src', 'screens', 'ThreadScreen.tsx');
  const client = source('src', 'api', 'client.ts');

  assert.match(bubble, /onLongPress\?\.\(message, own, \{ file, index \}\)/);
  assert.match(actions, /Save to Drive/);
  assert.match(screen, /defaultName=\{attachmentSaveTarget\?\.file\.name \?\? 'Attachment'\}/);
  assert.match(screen, /sourceFileId = `\$\{route\.params\.threadId\}:\$\{messageID\}:\$\{target\.index\}`/);
  assert.match(client, /saveChatAttachmentToFiles/);
  assert.match(client, /body: \{ sourceFileId, fileName, folderId \}/);
});
