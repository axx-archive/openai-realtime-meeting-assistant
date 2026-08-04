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
    /const threadListPosition = \{ startRenderingFromBottom: true \} as const;/,
  );
  assert.doesNotMatch(source, /threadListPosition = \{[^}]*disabled: true/);
  assert.match(source, /maintainVisibleContentPosition=\{threadListPosition\}/);
  assert.match(source, /onLoad=\{\(\) => \{[\s\S]*scrollToEnd\(\{ animated: false \}\)[\s\S]*markRead\(\)/);
  assert.doesNotMatch(source, /initialScrollIndex=\{boundary/);
});

test('an explicit message link keeps control of the final opening position', () => {
  assert.match(source, /onLoad=\{\(\) => \{\s*if \(route\.params\.messageId\) return;/);
});

test('human scrolling suppresses live tail-follow until drag or momentum settles', () => {
  assert.match(source, /onScrollBeginDrag=\{\(\) => \{[\s\S]*'drag-begin'[\s\S]*atBottomRef\.current = false;/);
  assert.match(source, /onMomentumScrollBegin=\{\(\) => \{[\s\S]*'momentum-begin'/);
  assert.match(source, /onMomentumScrollEnd=\{\(event\) => \{[\s\S]*'momentum-end'/);
  assert.match(source, /shouldFollowThreadTail\([\s\S]*threadScrollInteractionRef\.current/);
  assert.match(source, /threadMomentumGraceTimerRef\.current = setTimeout\([\s\S]*settleThreadScroll/);
});
