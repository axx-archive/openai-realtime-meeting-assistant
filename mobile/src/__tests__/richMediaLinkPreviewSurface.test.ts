import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('native link previews distinguish YouTube, X, Instagram, and generic articles', () => {
  const card = source('src', 'messaging', 'LinkPreviewCard.tsx');
  assert.match(card, /preview\.kind === 'youtube_video'/);
  assert.match(card, /preview\.kind === 'x_post'/);
  assert.match(card, /preview\.kind === 'instagram_reel'/);
  assert.match(card, /preview\.kind === 'instagram_video'/);
  assert.match(card, /preview\.kind === 'instagram_post'/);
  assert.match(card, /accessibilityHint="Opens the original video on YouTube"/);
  assert.match(card, /accessibilityHint=\{`Opens the original Instagram \$\{format\}`\}/);
  assert.match(card, /instagramVideoHero: \{ aspectRatio: 4 \/ 5/);
  assert.match(card, /instagramPostHero: \{ aspectRatio: 1/);
  assert.match(card, /const playable = preview\.kind === 'video'/);
  assert.match(card, /const visual = Boolean\(imageSource\)/);
});
test('native provider images require the authenticated same-origin proxy', () => {
  const card = source('src', 'messaging', 'LinkPreviewCard.tsx');
  assert.match(card, /path\?\.startsWith\('\/assistant\/link-preview\/image\?'\)/);
  assert.match(card, /uri: buildApiUrl\(API_BASE_URL, path\)/);
  assert.match(card, /headers: buildAuthHeaders\(NATIVE_CLIENT_HEADER, sessionToken, \{ Accept: 'image\/\*' \}\)/);
  assert.doesNotMatch(card, /uri: \^https\?:/);
  assert.doesNotMatch(card, /iframe|embed\.js|WebView|dangerouslySetInnerHTML/);
});
