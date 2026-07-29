import assert from 'node:assert/strict';
import test from 'node:test';

import {
  messageLongPressDelayMs,
  messageReactionChoices,
  shouldBeginTimestampReveal,
  timestampRevealProgress,
} from '../messaging/messageGestures';

test('timestamp reveal starts only for an intentional horizontal left drag', () => {
  assert.equal(shouldBeginTimestampReveal(-9, 0), true);
  assert.equal(shouldBeginTimestampReveal(-32, 10), true);
  assert.equal(shouldBeginTimestampReveal(-8, 0), false);
  assert.equal(shouldBeginTimestampReveal(30, 0), false);
  assert.equal(shouldBeginTimestampReveal(-16, 14), false);
});

test('timestamp reveal progress clamps and fully reveals at 68 points', () => {
  assert.equal(timestampRevealProgress(12), 0);
  assert.equal(timestampRevealProgress(-34), 0.5);
  assert.equal(timestampRevealProgress(-68), 1);
  assert.equal(timestampRevealProgress(-160), 1);
  assert.equal(timestampRevealProgress(Number.NaN), 0);
});

test('long press and reaction contracts stay deliberate and complete', () => {
  assert.equal(messageLongPressDelayMs, 430);
  assert.deepEqual(messageReactionChoices, ['❤️', '👍', '👎', '😂', '‼️', '❓', '🔥']);
});
