import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('native TikTok previews are portrait-first and open the canonical video', () => {
  const card = source('src', 'messaging', 'LinkPreviewCard.tsx');
  assert.match(card, /preview\.kind === 'tiktok_video'/);
  assert.match(card, /accessibilityLabel=\{`Play \$\{title\} by \$\{creator\} on TikTok`\}/);
  assert.match(card, /Linking\.openURL\(destination\)/);
  assert.match(card, /tikTokHero: \{ aspectRatio: 3 \/ 4/);
  assert.match(card, /recyclingKey=\{`\$\{url\}-tiktok-poster`\}/);
  assert.match(card, /cachePolicy="memory-disk"/);
  assert.match(card, /contentFit="cover"/);
  assert.match(card, /tikTokImageOutline: \{ borderColor: 'rgba\(255,255,255,0\.10\)' \}/);
  assert.match(card, /transform: \[\{ scale: 0\.96 \}\]/);
});

test('native TikTok posters remain server-proxied and never mount provider HTML', () => {
  const card = source('src', 'messaging', 'LinkPreviewCard.tsx');
  assert.match(card, /buildApiUrl\(API_BASE_URL, path\)/);
  assert.match(card, /headers: buildAuthHeaders\(NATIVE_CLIENT_HEADER, sessionToken, \{ Accept: 'image\/\*' \}\)/);
  assert.doesNotMatch(card, /iframe|embed\.js|WebView|dangerouslySetInnerHTML/);
  assert.doesNotMatch(card, /source=\{\{ uri: preview\.imageUrl/);
});
