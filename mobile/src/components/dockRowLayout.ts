/**
 * Geometry of the band above the Dock — the chat circle on the left, the
 * NavCluster toggle on the right, and the cluster expanding right-to-left
 * between them.
 *
 * These numbers live here rather than inline in NavCluster because they are
 * load-bearing and NOT obvious: on a 375pt iPhone SE the expanded cluster and
 * the chat circle geometrically OVERLAP by 29pt. The design survives that only
 * because the circle cross-fades out faster than the cluster fades in, so the
 * two are never both opaque in the same band.
 *
 * That is safety by timing, not by construction. Shared constants plus the
 * pinned invariants in dockRowLayout.test.ts mean a future change to either the
 * item width or the animation durations fails a test instead of shipping a
 * collision nobody notices until a screenshot.
 */

/**
 * Deliberately free of `theme/tokens` imports: that module pulls in
 * react-native, which the node:test runner cannot parse, and geometry that
 * cannot be tested is geometry that gets verified by eyeballing a screenshot.
 * The values mirror `hitMin` (44), `space[2]` (8) and `space[5]` (20), and
 * `dockRowLayout.test.ts` pins them against the rendered inset.
 */
const HIT_MIN = 44;
const SPACE_2 = 8;
const SPACE_5 = 20;

/** Wide enough for "Threads" at 10px without truncating. */
export const NAV_ITEM_WIDTH = 58;

/** Gap between expanded cluster items. */
export const NAV_ITEM_GAP = SPACE_2;

/** Outer inset of both ends of the row, matching the Dock pill's edge. */
export const DOCK_ROW_MARGIN = SPACE_5;

/** Lift the two corner controls above the device edge as a deliberate band. */
export const DOCK_ROW_BOTTOM_MARGIN = 32;

/** The standard 44pt tap target both end controls use. */
export const DOCK_ROW_CONTROL = HIT_MIN;

/**
 * Distance the cluster's item strip is held clear of the toggle. Larger than
 * GlassContainer's merge `spacing`, so the last item and the toggle stay two
 * distinct pieces of glass instead of bridging into a lump.
 */
export const NAV_ITEMS_RIGHT_INSET = HIT_MIN + SPACE_5;

export type DockRowSpan = { left: number; right: number };

export function chatCircleSpan(): DockRowSpan {
  return { left: DOCK_ROW_MARGIN, right: DOCK_ROW_MARGIN + DOCK_ROW_CONTROL };
}

/** Horizontal span the expanded cluster's items occupy on a screen `width` wide. */
export function navItemsSpan(width: number, itemCount: number): DockRowSpan {
  const clusterRight = width - DOCK_ROW_MARGIN;
  const right = clusterRight - NAV_ITEMS_RIGHT_INSET;
  const itemsWidth = itemCount * NAV_ITEM_WIDTH + Math.max(0, itemCount - 1) * NAV_ITEM_GAP;
  return { left: right - itemsWidth, right };
}

/** Positive when the two would collide if both were visible at once. */
export function dockRowOverlap(width: number, itemCount: number): number {
  return chatCircleSpan().right - navItemsSpan(width, itemCount).left;
}
