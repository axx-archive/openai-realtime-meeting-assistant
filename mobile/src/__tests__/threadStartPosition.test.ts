import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');

test('normal thread opens render and settle on the latest message', () => {
  const source = fs.readFileSync(
    path.join(mobileRoot, 'src', 'screens', 'ThreadScreen.tsx'),
    'utf8',
  );

  assert.match(source, /startRenderingFromBottom: true/);
  assert.match(
    source,
    /onLoad=\{\(\) => \{[\s\S]*if \(route\.params\.messageId\) return;[\s\S]*scrollToEnd\(\{ animated: false \}\)/,
  );
  assert.doesNotMatch(source, /initialScrollIndex=\{boundary/);
});
