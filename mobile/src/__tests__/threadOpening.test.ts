import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const source = readFileSync(
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'screens', 'ThreadScreen.tsx'),
  'utf8',
);

test('threads render from the latest message and finalize at the bottom after layout', () => {
  assert.match(
    source,
    /maintainVisibleContentPosition=\{\{ disabled: true, startRenderingFromBottom: true \}\}/,
  );
  assert.match(source, /onLoad=\{\(\) => \{[\s\S]*scrollToEnd\(\{ animated: false \}\)[\s\S]*markRead\(\)/);
  assert.doesNotMatch(source, /initialScrollIndex=\{boundary/);
});

test('an explicit message link keeps control of the final opening position', () => {
  assert.match(source, /onLoad=\{\(\) => \{\s*if \(route\.params\.messageId\) return;/);
});
