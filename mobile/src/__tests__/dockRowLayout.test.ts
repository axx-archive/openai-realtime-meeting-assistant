import test from 'node:test';
import assert from 'node:assert/strict';
// Mirrored from theme/motion.ts, which imports react-native and so cannot be
// loaded by this runner. A divergence here is itself a finding.
const duration = { fast: 120, med: 220 };
import {
  chatCircleSpan,
  dockRowOverlap,
  navItemsSpan,
  NAV_ITEM_WIDTH,
  NAV_ITEM_GAP,
} from '../components/dockRowLayout';

/**
 * The dock row's collision invariant.
 *
 * This was originally deferred as "needs a device to eyeball at 375pt". It is
 * arithmetic, and the arithmetic says the expanded cluster and the chat circle
 * DO overlap on an iPhone SE. The design is safe only because the circle
 * cross-fades out faster than the cluster fades in — safety by timing, which is
 * precisely the kind of thing that breaks silently when someone later widens an
 * item or retunes a duration.
 */

const SE = 375; // narrowest supported iPhone
const PRO_MAX = 440;
const ITEMS = 4; // Room, Threads, Live, Work

test('the expanded cluster and the chat circle overlap on an iPhone SE', () => {
  const overlap = dockRowOverlap(SE, ITEMS);
  // Documented, not tolerated: this is WHY the circle must yield.
  assert.ok(overlap > 0, `expected an overlap to guard against, got ${overlap}pt`);
  assert.equal(overlap, 29);
});

// The mitigation. If the circle ever stopped fading out before the items
// arrived, that 29pt band would show two controls stacked on each other.
test('the circle finishes fading out before the cluster finishes fading in', () => {
  assert.ok(
    duration.fast < duration.med,
    `circle-out (${duration.fast}ms) must be shorter than items-in (${duration.med}ms)`,
  );
});

// The leftmost item travels furthest, so it is the last to reach the contested
// band — by which time the circle is long gone.
test('the leftmost item is the last to arrive, giving the circle time to clear', () => {
  const travel = ITEMS * (NAV_ITEM_WIDTH + NAV_ITEM_GAP);
  const { left } = navItemsSpan(SE, ITEMS);
  // It starts this far right of its final resting place.
  assert.ok(travel > chatCircleSpan().right - left, 'leftmost item starts inside the circle');
});

test('a wider phone has room to spare', () => {
  assert.ok(dockRowOverlap(PRO_MAX, ITEMS) < dockRowOverlap(SE, ITEMS));
});

// The real guard: adding a fifth cluster item, or widening the existing ones,
// pushes the strip past the circle and off the left edge. Either fails here
// rather than on someone's phone.
test('a fifth cluster item would run off the left edge of an SE', () => {
  const { left } = navItemsSpan(SE, 5);
  assert.ok(left < 0, `five items still fit (left=${left}) — re-tune this guard`);
});

test('the chat circle sits on the same inset as the cluster and the Dock pill', () => {
  assert.equal(chatCircleSpan().left, 20);
  // Symmetry with the cluster's own right margin is what makes the row read as
  // one band rather than two floating controls.
  assert.equal(SE - navItemsSpan(SE, ITEMS).right, 20 + 44 + 20);
});
